// 작업·세션 공유 상태: 처음 읽기, 실시간 갱신(watch), 확인 전 표시. React에 기대지 않는다(ctx만 있으면 시험할 수 있다).
import {errorMessage, OrbitError} from '../oni.ts';
import {PAUSE_MESSAGE, WorkPaused, WorkToolError, type PauseKind} from './contract.ts';
import {sessionGet, sessionsList, workGet, workList} from './client.ts';
import {defaultList, dismiss as dismissId, loadReview, saveReview, updatePending} from './review.ts';
import {abortableSleep, RETRY_MS, watchChanges} from './watch.ts';
import type {Session, SessionPage, StudioScope, Work, WorkCtx, WorkSummary} from './types.ts';

export interface WorkState {
  status: 'idle' | 'loading' | 'ready' | 'error' | 'paused';
  /** 판이 맞지 않아 이 데이터만 멈춤 */
  pause?: {kind: PauseKind; message: string};
  error?: string;
  scope: StudioScope;
  works: WorkSummary[];
  /** 읽은 작업의 전체 그래프(작업 id별) */
  details: Record<string, Work>;
  sessions: Session[];
  /** 「끝남 · 확인 전」 작업 id */
  pendingReview: string[];
}

/** 처음 읽을 때 따라가는 쪽 수의 상한(근거 없는 구현 값: 쪽 크기 최대 50이므로 작업 200건·세션 100건까지) */
export const MAX_WORK_PAGES = 4;
export const MAX_SESSION_PAGES = 2;

export interface WorkStoreOptions {
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
}

/** 작업·세션 cwd가 범위 디렉토리 중 하나이거나 그 아래인가(경로 구분자 단위) */
export function inScope(scope: StudioScope, cwd: string | undefined): boolean {
  if (scope.kind === 'all') return true;
  if (!cwd) return false;
  return scope.directories.some(d => {
    const dir = d.length > 1 ? d.replace(/\/+$/, '') : d;
    return cwd === dir || cwd.startsWith(dir === '/' ? '/' : `${dir}/`);
  });
}

/** 작업 전체 그래프에서 목록 줄(요약)을 만든다 */
export function summaryOf(w: Work): WorkSummary {
  const verification = {none: 0, pass: 0, fail: 0, needs_user: 0, outdated: 0};
  for (const s of w.steps) {
    const k = s.verification.state;
    if (k === 'none' || k === 'pass' || k === 'fail' || k === 'needs_user') verification[k] += 1;
    if (s.verification.outdated && k !== 'none') verification.outdated += 1;
  }
  const {steps: _s, runs: _r, evidence: _e, children: _c, ...head} = w;
  return {...head, stepCount: w.steps.length, verification};
}

export function createWorkStore(ctx: WorkCtx, options: WorkStoreOptions = {}) {
  const sleep = options.sleep ?? abortableSleep;
  let state: WorkState = {status: 'idle', scope: ctx.scope.current(), works: [], details: {}, sessions: [], pendingReview: []};
  const listeners = new Set<() => void>();
  let abort: AbortController | undefined;
  let offScope: (() => void) | undefined;
  let generation = 0;
  let lastLogged: string | undefined;
  /** 지난번에 본 작업 상태. 열림→끝남 전이를 알아보는 데 쓴다 */
  const known = new Map<string, string>();

  const set = (next: Partial<WorkState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  async function applyWorks(next: WorkSummary[], replaceAll: boolean) {
    const base = replaceAll ? [] : state.works.filter(w => !next.some(n => n.id === w.id));
    const works = [...base, ...next].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.id.localeCompare(b.id));
    // 처음 읽을 때 이미 끝난 옛 작업은 올리지 않는다(known이 비어 있으므로 열림→끝남 전이가 없다)
    const pendingReview = updatePending(state.pendingReview, known, next);
    next.forEach(w => known.set(w.id, w.state));
    const changed = pendingReview.length !== state.pendingReview.length || pendingReview.some((id, i) => id !== state.pendingReview[i]);
    set({works, pendingReview});
    if (changed) await saveReview(ctx, {pending: pendingReview});
  }

  /** 처음부터 읽는다. 시작·reset·범위 바뀜에서 쓴다. 돌려주는 값은 목록 기준점(seq). */
  async function loadAll(): Promise<string> {
    const gen = ++generation;
    const scope = ctx.scope.current();
    const items: WorkSummary[] = [];
    let seq: string | undefined;
    let cursor: string | undefined;
    for (let i = 0; i < MAX_WORK_PAGES; i++) {
      const page = await workList(ctx, {scope, state: 'any', cursor});
      seq ??= page.seq;
      items.push(...page.items);
      cursor = page.nextCursor;
      if (!cursor) break;
    }
    const sessions: Session[] = [];
    cursor = undefined;
    for (let i = 0; i < MAX_SESSION_PAGES; i++) {
      const page = await sessionsList(ctx, {scope, cursor});
      sessions.push(...page.sessions);
      cursor = page.nextCursor;
      if (!cursor) break;
    }
    // 사용자 확인을 기다릴 수 있는 열린 작업만 그래프를 미리 읽는다(받은 일·홈 위젯용)
    const details: Record<string, Work> = {};
    for (const w of items) {
      if (w.state === 'open' && w.counts.blocked > 0) details[w.id] = (await workGet(ctx, w.id)).work;
    }
    if (gen !== generation) return seq ?? ''; // 그동안 범위가 또 바뀜: 이 결과는 버린다
    const keptDetails = Object.fromEntries(Object.entries(state.details).filter(([id]) => items.some(i => i.id === id)));
    set({scope, sessions, details: {...keptDetails, ...details}});
    await applyWorks(items, true);
    return seq as string;
  }

  async function applyChanges(c: {workIds: string[]; sessionIds: string[]}) {
    const scope = ctx.scope.current();
    const details = {...state.details};
    const updated: WorkSummary[] = [];
    const gone = new Set<string>();
    for (const id of c.workIds) {
      try {
        const {work} = await workGet(ctx, id);
        if (!inScope(scope, work.cwd)) {
          gone.add(id);
          continue;
        }
        details[id] = work;
        updated.push(summaryOf(work));
      } catch (e) {
        if (e instanceof WorkToolError && e.code === 'NOT_FOUND') gone.add(id);
        else throw e;
      }
    }
    let sessions = state.sessions;
    for (const id of c.sessionIds) {
      try {
        const {session} = await sessionGet(ctx, id, undefined, 1);
        sessions = [session, ...sessions.filter(s => s.sessionId !== id)];
        if (!inScope(scope, session.cwd)) sessions = sessions.filter(s => s.sessionId !== id);
      } catch (e) {
        if (e instanceof WorkToolError && e.code === 'NOT_FOUND') sessions = sessions.filter(s => s.sessionId !== id);
        else throw e;
      }
    }
    sessions = [...sessions].sort((a, b) => b.lastSeenAt.localeCompare(a.lastSeenAt) || a.sessionId.localeCompare(b.sessionId));
    gone.forEach(id => { delete details[id]; known.delete(id); });
    set({details, sessions, works: state.works.filter(w => !gone.has(w.id))});
    await applyWorks(updated, false);
  }

  function fail(message: string) {
    if (message !== lastLogged) ctx.log.error(`작업 기록을 불러오지 못했습니다: ${message}`);
    lastLogged = message;
    set({status: 'error', error: message});
  }

  function pause(kind: PauseKind) {
    ctx.log.warn(`작업 기록을 읽지 않습니다: ${PAUSE_MESSAGE[kind]}`);
    set({status: 'paused', pause: {kind, message: PAUSE_MESSAGE[kind]}, error: undefined});
  }

  /** 읽고 → 갱신 대기를 건다. 연결 실패면 쉬었다가 처음부터 다시 */
  async function run(signal: AbortSignal) {
    while (!signal.aborted) {
      try {
        if (state.status !== 'ready') set({status: 'loading', error: undefined});
        const seq = await loadAll();
        lastLogged = undefined;
        set({status: 'ready', error: undefined, pause: undefined});
        await watchChanges(ctx, {
          after: seq,
          signal,
          sleep,
          handlers: {
            onChanges: applyChanges,
            onReset: async () => { await loadAll(); },
            onPaused: pause,
            onError: message => { if (state.status === 'ready') ctx.log.warn(`작업 변경 대기를 다시 겁니다: ${message}`); },
          },
        });
        return;
      } catch (e) {
        if (signal.aborted) return;
        if (e instanceof WorkPaused) return pause(e.kind);
        fail(e instanceof OrbitError || e instanceof Error ? errorMessage(e) : String(e));
        await sleep(RETRY_MS, signal);
      }
    }
  }

  async function start(): Promise<void> {
    stop();
    const review = await loadReview(ctx);
    set({pendingReview: review.pending});
    abort = new AbortController();
    offScope = ctx.scope.onChange(() => {
      // 범위가 바뀌면 처음부터 다시 읽는다. 변경 대기는 그대로 이어진다.
      if (state.status === 'ready') {
        loadAll().catch(e => {
          if (e instanceof WorkPaused) pause(e.kind);
          else fail(errorMessage(e));
        });
      }
    });
    void run(abort.signal);
  }

  function stop() {
    abort?.abort();
    abort = undefined;
    offScope?.();
    offScope = undefined;
  }

  async function dismiss(workId: string): Promise<void> {
    const pendingReview = dismissId(state.pendingReview, workId);
    set({pendingReview});
    await saveReview(ctx, {pending: pendingReview});
  }

  /** 단계·실행을 읽는다. 이미 읽었으면 그대로, force면 다시 */
  async function loadDetail(workId: string, force = false): Promise<Work> {
    const have = state.details[workId];
    if (have && !force) return have;
    const {work} = await workGet(ctx, workId);
    set({details: {...state.details, [workId]: work}});
    return work;
  }

  /** 세션 한 쪽(사건·실행·증거). 화면이 쪽 넘김을 한다 */
  function loadSession(sessionId: string, cursor?: string): Promise<SessionPage> {
    return sessionGet(ctx, sessionId, cursor);
  }

  return {
    start,
    dispose: stop,
    dismiss,
    loadDetail,
    loadSession,
    visibleWorks: () => defaultList(state.works, state.pendingReview),
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
  };
}

export type WorkStore = ReturnType<typeof createWorkStore>;

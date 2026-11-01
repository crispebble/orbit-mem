// 작업·세션 공유 상태: 처음 읽기, 실시간 갱신(watch), 확인 전 표시. React에 기대지 않는다(ctx만 있으면 시험할 수 있다).
import {errorMessage, OrbitError} from '../orb.ts';
import {pauseMessage, WorkPaused, WorkToolError, type PauseKind} from './contract.ts';
import {sessionGet, sessionsList, workGet, workList, workMetrics} from './client.ts';
import {defaultList, dismiss as dismissId, loadReview, saveReview, updatePending} from './review.ts';
import {abortableSleep, RETRY_MS, watchChanges} from './watch.ts';
import type {MetricsResult, Session, SessionPage, StudioScope, Work, WorkCtx, WorkSummary} from './types.ts';

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

/**
 * 변경 대기가 이만큼 연달아 실패하면 오류로 알린다. 한 번의 실패는 orb가 다시 뜨는 정상 경로라 경고를 띄우지 않는다
 * (설계 「정상 갱신에서 경고 표시가 튀지 않는다」). 2는 「다시 걸어도 실패」를 뜻하는 구현 값이다.
 */
export const WATCH_FAILURES_TO_REPORT = 2;

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

const sameScope = (a: StudioScope, b: StudioScope) => JSON.stringify(a) === JSON.stringify(b);

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
  let lastLogged: string | undefined;
  let watchFailures = 0;
  /** 지난번에 본 작업 상태. 열림→끝남 전이를 알아보는 데 쓴다 */
  const known = new Map<string, string>();
  /** ensureDetails: 읽는 중인 id와, 없다고 확인한 id */
  const fetching = new Set<string>();
  const absent = new Set<string>();
  /** 목록 읽기와 변경 반영을 한 줄로 세운다. 겹치면 늦게 끝난 쪽이 옛 스냅숏으로 덮어쓸 수 있다. */
  let queue: Promise<unknown> = Promise.resolve();
  const serial = <T>(job: () => Promise<T>): Promise<T> => {
    const next = queue.then(job, job);
    queue = next.catch(() => {});
    return next;
  };

  const set = (next: Partial<WorkState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  async function savePending(pendingReview: string[]) {
    const changed = pendingReview.length !== state.pendingReview.length || pendingReview.some((id, i) => id !== state.pendingReview[i]);
    if (!changed) return;
    set({pendingReview});
    await saveReview(ctx, {pending: pendingReview});
  }

  async function applyWorks(next: WorkSummary[], replaceAll: boolean) {
    const base = replaceAll ? [] : state.works.filter(w => !next.some(n => n.id === w.id));
    const works = [...base, ...next].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.id.localeCompare(b.id));
    // 처음 읽을 때 이미 끝난 옛 작업은 올리지 않는다(known이 비어 있으므로 열림→끝남 전이가 없다)
    const pendingReview = updatePending(state.pendingReview, known, next);
    next.forEach(w => known.set(w.id, w.state));
    set({works});
    await savePending(pendingReview);
  }

  /** 쪽을 nextCursor가 없을 때까지 읽는다. 두 번째 쪽부터는 서버가 밝힌 쪽 상한(limitMax)으로 묻는다. */
  async function allPages<T extends {nextCursor?: string; limitMax: number; seq: string}>(read: (cursor?: string, limit?: number) => Promise<T>): Promise<T[]> {
    const pages: T[] = [];
    let cursor: string | undefined;
    let limit: number | undefined;
    do {
      const page = await read(cursor, limit);
      pages.push(page);
      cursor = page.nextCursor;
      limit = page.limitMax;
    } while (cursor);
    return pages;
  }

  /** 처음부터 읽는다. 시작·reset·범위 바뀜에서 쓴다. 돌려주는 값은 목록 기준점(seq). 읽는 동안 범위가 바뀌면 새 범위로 다시 읽는다. */
  function loadAll(): Promise<string> {
    return serial(async () => {
      for (;;) {
        const scope = ctx.scope.current();
        const pages = await allPages((cursor, limit) => workList(ctx, {scope, state: 'any', cursor, limit}));
        const seq = pages[0].seq;
        const items = pages.flatMap(p => p.items);
        const sessions = (await allPages((cursor, limit) => sessionsList(ctx, {scope, cursor, limit}))).flatMap(p => p.sessions);
        if (!sameScope(scope, ctx.scope.current())) continue;
        // 사용자 확인을 기다릴 수 있는 열린 작업은 그래프를 미리 읽는다(받은 일·홈 위젯용).
        // 이미 읽은 상세는 갱신 시각이 같을 때만 그대로 두고, 바뀌었으면 다시 읽는다(reset 뒤 열린 탭이 옛 그래프를 보이지 않게).
        const details: Record<string, Work> = {};
        for (const w of items) {
          const have = state.details[w.id];
          const want = have !== undefined || (w.state === 'open' && w.counts.blocked > 0);
          if (!want) continue;
          details[w.id] = have && have.updatedAt === w.updatedAt ? have : (await workGet(ctx, w.id)).work;
        }
        if (!sameScope(scope, ctx.scope.current())) continue;
        set({scope, sessions, details});
        await applyWorks(items, true);
        return seq;
      }
    });
  }

  function applyChanges(c: {workIds: string[]; sessionIds: string[]}): Promise<void> {
    return serial(async () => {
      const scope = ctx.scope.current();
      const fetched: Record<string, Work> = {};
      const updated: WorkSummary[] = [];
      const gone = new Set<string>();
      for (const id of c.workIds) {
        try {
          const {work} = await workGet(ctx, id);
          if (!inScope(scope, work.cwd)) {
            gone.add(id);
            continue;
          }
          fetched[id] = work;
          updated.push(summaryOf(work));
        } catch (e) {
          if (e instanceof WorkToolError && e.code === 'NOT_FOUND') gone.add(id);
          else throw e;
        }
      }
      const fresh = new Map<string, Session>();
      const dropped = new Set<string>();
      for (const id of c.sessionIds) {
        try {
          const {session} = await sessionGet(ctx, id, undefined, 1);
          if (inScope(scope, session.cwd)) fresh.set(id, session);
          else dropped.add(id);
        } catch (e) {
          if (e instanceof WorkToolError && e.code === 'NOT_FOUND') dropped.add(id);
          else throw e;
        }
      }
      // 읽는 동안 바뀐 상태 위에 합친다(await 앞의 스냅숏으로 덮어쓰지 않는다)
      const details = {...state.details, ...fetched};
      gone.forEach(id => { delete details[id]; known.delete(id); });
      const sessions = [...fresh.values(), ...state.sessions.filter(s => !fresh.has(s.sessionId) && !dropped.has(s.sessionId))]
        .sort((a, b) => b.lastSeenAt.localeCompare(a.lastSeenAt) || a.sessionId.localeCompare(b.sessionId));
      set({details, sessions, works: state.works.filter(w => !gone.has(w.id))});
      await applyWorks(updated, false);
      if (gone.size) await savePending(state.pendingReview.filter(id => !gone.has(id)));
    });
  }

  function fail(message: string) {
    if (message !== lastLogged) ctx.log.error(`Could not load work history: ${message}`);
    lastLogged = message;
    set({status: 'error', error: message});
  }

  function ok() {
    lastLogged = undefined;
    watchFailures = 0;
    if (state.status === 'error' || state.status === 'loading') set({status: 'ready', error: undefined});
  }

  function pause(kind: PauseKind) {
    ctx.log.warn(`Not reading work history: ${pauseMessage(kind)}`);
    set({status: 'paused', pause: {kind, message: pauseMessage(kind)}, error: undefined});
  }

  /** 범위가 바뀌면 처음부터 다시 읽는다. 실패하면 쉬었다가 될 때까지 다시(변경 대기는 그대로 이어진다). */
  async function reloadForScope(signal: AbortSignal) {
    while (!signal.aborted && state.status !== 'paused') {
      try {
        await loadAll();
        if (!signal.aborted) ok();
        return;
      } catch (e) {
        if (signal.aborted) return;
        if (e instanceof WorkPaused) return pause(e.kind);
        fail(errorMessage(e));
        await sleep(RETRY_MS, signal);
      }
    }
  }

  /** 읽고 → 갱신 대기를 건다. 연결 실패면 쉬었다가 처음부터 다시 */
  async function run(signal: AbortSignal) {
    while (!signal.aborted) {
      try {
        if (state.status !== 'ready') set({status: 'loading', error: undefined});
        const seq = await loadAll();
        if (signal.aborted) return;
        ok();
        set({pause: undefined});
        await watchChanges(ctx, {
          after: seq,
          signal,
          sleep,
          handlers: {
            onChanges: applyChanges,
            onReset: async () => { await loadAll(); },
            onPaused: pause,
            onOk: ok,
            onError: message => {
              watchFailures += 1;
              if (watchFailures >= WATCH_FAILURES_TO_REPORT) fail(message);
              else if (state.status === 'ready') ctx.log.warn(`Re-arming the work change watch: ${message}`);
            },
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

  function begin(signal: AbortSignal) {
    offScope?.();
    offScope = ctx.scope.onChange(() => {
      if (state.status !== 'paused') void reloadForScope(signal);
    });
    void run(signal);
  }

  async function start(): Promise<void> {
    stop();
    // 시작하자마자 중단 신호를 만든다: 확인 전 목록을 읽는 사이에 끄면 읽기를 시작하지 않는다
    const controller = new AbortController();
    abort = controller;
    const review = await loadReview(ctx);
    if (controller.signal.aborted) return;
    set({pendingReview: review.pending});
    begin(controller.signal);
  }

  function stop() {
    abort?.abort();
    abort = undefined;
    offScope?.();
    offScope = undefined;
  }

  /** 판 때문에 멈춘 뒤 사용자가 OpenOrbit을 올렸으면 다시 읽는다 */
  function recheck() {
    if (state.status !== 'paused' || !abort) return;
    const controller = new AbortController();
    abort.abort();
    abort = controller;
    set({status: 'loading', pause: undefined});
    begin(controller.signal);
  }

  async function dismiss(workId: string): Promise<void> {
    await savePending(dismissId(state.pendingReview, workId));
  }

  /** 단계·실행을 읽는다. 이미 읽었으면 그대로, force면 다시 */
  async function loadDetail(workId: string, force = false): Promise<Work> {
    const have = state.details[workId];
    if (have && !force) return have;
    const {work} = await workGet(ctx, workId);
    set({details: {...state.details, [workId]: work}});
    return work;
  }

  /** 세션 줄·세션 탭이 단계 제목을 쓰도록 아직 읽지 않은 작업을 읽어 둔다. 실패하면 다음 부름에 다시, 없는 작업은 다시 묻지 않는다. */
  function ensureDetails(ids: readonly string[]) {
    for (const id of ids) {
      if (state.details[id] || fetching.has(id) || absent.has(id)) continue;
      fetching.add(id);
      loadDetail(id).then(
        () => fetching.delete(id),
        (e: unknown) => {
          fetching.delete(id);
          if (e instanceof WorkToolError && e.code === 'NOT_FOUND') absent.add(id);
        },
      );
    }
  }

  /** AU-7: 작업의 능숙해짐 지표와 비슷한 일의 추이. 화면이 그때그때 읽는다(목록에 두지 않는다) */
  function loadMetrics(workId: string): Promise<MetricsResult> {
    return workMetrics(ctx, workId);
  }

  /** 세션 한 쪽(사건·실행·증거). 화면이 쪽 넘김을 한다 */
  function loadSession(sessionId: string, cursor?: string): Promise<SessionPage> {
    return sessionGet(ctx, sessionId, cursor);
  }

  return {
    start,
    dispose: stop,
    recheck,
    dismiss,
    loadDetail,
    ensureDetails,
    loadSession,
    loadMetrics,
    visibleWorks: () => defaultList(state.works, state.pendingReview),
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
  };
}

export type WorkStore = ReturnType<typeof createWorkStore>;

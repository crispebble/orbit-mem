// Orbit Studio 홈 작업 지도에 주는 작업 공급자(플러그인 API 0.1.25 `ctx.work.provide`, Studio 4차 S4-2).
// 작업 기록은 이 플러그인의 읽는 쪽 연결(work store)이 읽고, 여기서는 Studio가 아는 모양(사실)으로만 옮긴다.
// 상태(답 기다림·진행 중·쉬는 중·끝)는 Studio가 정한다. 옛 Studio(ctx.work 없음)에서는 등록하지 않는다.
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import {waitingSteps} from './display.ts';
import type {WorkState, WorkStore} from './store.ts';
import type {Session, Work, WorkSummary} from './types.ts';

/** Studio `WorkItem`(0.1.25)과 같은 모양. 옛 패키지 타입에는 없어 좁게 둔다. */
export interface HomeWorkItem {
  id: string;
  kind: 'work' | 'session';
  title: string;
  project?: {id: string; label: string};
  done: boolean;
  waiting: boolean;
  running: boolean;
  activeAt: number;
  startedAt?: number;
  doneAt?: number;
  recent?: string[];
  launcher?: string;
  meta?: string;
}

/** Studio `WorkDetail`(0.1.25)과 같은 모양 */
export interface HomeWorkDetail {
  title: string;
  steps?: {id: string; title: string; status: 'pending' | 'ready' | 'in_progress' | 'blocked' | 'done' | 'cancelled'; verification?: 'none' | 'pass' | 'fail' | 'needs_user'; note?: string}[];
  sessions?: {id: string; title?: string; state: 'running' | 'waiting_user' | 'idle' | 'stale' | 'ended'; host?: string; launcher?: string; startedAt?: number}[];
  facts?: {label: string; value: string}[];
}

interface WorkProviderLike {
  id: string;
  list(signal: AbortSignal): Promise<readonly HomeWorkItem[]>;
  subscribe?(onChange: () => void): () => void;
  detail?(id: string, signal: AbortSignal): Promise<HomeWorkDetail | null>;
  /** 플러그인 API 0.1.27: 지도에서 작업을 열라는 요청(작업 대화를 연다) */
  open?(id: string): void | Promise<void>;
  /** 플러그인 API 0.1.27: 지도에서 새 일을 시키라는 요청(작성 중인 글로 작업 대화를 연다) */
  compose?(input: {text: string; cwd?: string}): void | Promise<void>;
}

/** 작업 대화를 여는 쪽(Studio 4차 S4-5, C1). 데몬 실행을 찾는 일과 대화 창 열기를 index.tsx가 이어 준다. */
export interface WorkChatHooks {
  /** 작업(또는 `session:` 노드)에 해당하는 데몬 실행의 id. 데몬이 없거나 모르면 undefined */
  runFor(target: {workId?: string; sessionId?: string}): Promise<string | undefined>;
  open(target: {kind: 'run'; runId: string} | {kind: 'work'; workId: string; cwd?: string} | {kind: 'compose'; text: string; cwd?: string}): void;
}
type ProviderStore = Pick<WorkStore, 'getState' | 'subscribe' | 'ensureDetails' | 'loadDetail'>;

const SESSION_PREFIX = 'session:';
const time = (s: string | undefined) => (s ? Date.parse(s) || undefined : undefined);
const STEP = new Set(['pending', 'ready', 'in_progress', 'blocked', 'done', 'cancelled']);
const SESSION = new Set(['running', 'waiting_user', 'idle', 'stale', 'ended']);
const CHECK = new Set(['none', 'pass', 'fail', 'needs_user']);
const launcherOf = (o: unknown) => (o && typeof o === 'object' && typeof (o as {launcher?: unknown}).launcher === 'string' ? (o as {launcher: string}).launcher : undefined);
/** 저장소가 아닌 폴더는 프로젝트 이름이 절대경로다: 지도에는 마지막 폴더 이름을 보인다. */
const projectOf = (p: {scope: string; label: string} | undefined) =>
  p ? {project: {id: p.scope, label: p.label.startsWith('/') ? p.label.split('/').filter(Boolean).pop() || p.label : p.label}} : {};
const hostName = (host: string) => (host === 'claude' ? 'Claude Code' : host === 'codex' ? 'Codex' : host);

/** 지도 노드 아래 두 줄: 진행 중 단계와 마지막에 끝난 단계. 단계를 아직 읽지 않았으면 끝난 수. */
function recentOf(w: WorkSummary, detail: Work | undefined): string[] {
  const t = pick(workText);
  if (!detail) return w.stepCount ? [t.home.progress(w.counts.done, Math.max(0, w.stepCount - w.counts.cancelled))] : [];
  const active = detail.steps.filter(s => s.status === 'in_progress' || s.status === 'blocked').map(s => s.activeTitle || s.title);
  const done = detail.steps.filter(s => s.status === 'done').sort((a, b) => (b.finishedAt ?? '').localeCompare(a.finishedAt ?? '')).map(s => s.title);
  return [...active, ...done].slice(0, 2);
}

/** 열린 결정 카드가 묶인 작업·세션(Studio 4차 S4-3: 지도의 「답 기다림」은 열린 카드도 센다) */
export interface OpenCardRefs {
  workIds: ReadonlySet<string>;
  sessionIds: ReadonlySet<string>;
}
const NO_CARDS: OpenCardRefs = {workIds: new Set(), sessionIds: new Set()};

/**
 * 홈 작업 지도에 오르는 작업인가: OpenOrbit 오케스트레이션이 만든 작업(`origin.host`가 `orbit-pi`, 또는 목록 종류가 `orbit-pi-plan`)만.
 * 순수 Claude Code·Codex 세션은 작업 현황을 만들지 않고 활동 기록만 남기므로, 옛 기록(work.db에 남아 있다)은 지도에서만 안 보인다.
 * 작업 탭과 작업 상세는 id로 옛 작업도 그대로 연다.
 */
export function isOpenOrbitWork(w: {origin?: {host: string; list?: {kind: string}}}): boolean {
  return w.origin?.host === 'orbit-pi' || w.origin?.list?.kind === 'orbit-pi-plan';
}

/**
 * work store의 상태를 홈 항목으로 옮긴다. OpenOrbit 작업만 올리고, 작업 없는 세션은 노드가 되지 않는다.
 * 열린 결정 카드가 지도 밖 작업·세션에 묶여 있어도 지도에는 아무것도 더하지 않는다(카드는 확인할 일에 그대로 열려 있다).
 */
export function homeItems(state: WorkState, now: number, cards: OpenCardRefs = NO_CARDS): HomeWorkItem[] {
  void now;
  const items: HomeWorkItem[] = [];
  for (const w of state.works.filter(isOpenOrbitWork)) {
    // 상세의 세션 목록과 같은 묶음: 작업을 연 세션과 이 작업을 열어 둔 세션
    const related = state.sessions.filter(s => s.sessionId === w.origin?.sessionId || s.openWorkIds.includes(w.id));
    const detail = state.details[w.id];
    const open = w.state !== 'done';
    items.push({
      id: w.id, kind: 'work', title: w.title,
      ...projectOf(w.project),
      done: !open,
      waiting: open && ((detail ? waitingSteps(detail).length > 0 : false) || related.some(s => s.state === 'waiting_user' || cards.sessionIds.has(s.sessionId)) || cards.workIds.has(w.id)),
      running: open && related.some(s => s.state === 'running'),
      activeAt: time(w.updatedAt) ?? 0,
      ...(time(w.openedAt) ? {startedAt: time(w.openedAt)} : {}),
      ...(time(w.closedAt) ? {doneAt: time(w.closedAt)} : {}),
      recent: recentOf(w, detail),
      ...(launcherOf(w.origin) ? {launcher: launcherOf(w.origin)} : {}),
      ...(w.origin ? {meta: hostName(w.origin.host)} : {}),
    });
  }
  return items;
}

function sessionItem(s: Session, card = false): HomeWorkItem {
  // 긴 경로보다 브랜치·폴더 이름이 한눈에 읽힌다
  const where = s.branch || s.cwd?.split('/').filter(Boolean).pop() || s.project?.label || s.sessionId.slice(0, 8);
  return {
    id: SESSION_PREFIX + s.sessionId, kind: 'session', title: `${hostName(s.host)} · ${where}`,
    ...projectOf(s.project),
    done: s.state === 'ended', waiting: s.state !== 'ended' && (s.state === 'waiting_user' || card), running: s.state === 'running',
    activeAt: time(s.lastSeenAt) ?? 0,
    ...(time(s.startedAt) ? {startedAt: time(s.startedAt)} : {}),
    ...(time(s.endedAt) ? {doneAt: time(s.endedAt)} : {}),
    ...(s.model ? {recent: [s.model]} : {}),
    ...(launcherOf(s) ? {launcher: launcherOf(s)} : {}),
  };
}

const sessionRow = (s: Session): NonNullable<HomeWorkDetail['sessions']>[number] => ({
  id: s.sessionId, title: s.branch ? `${hostName(s.host)} · ${s.branch}` : hostName(s.host),
  state: (SESSION.has(s.state) ? s.state : 'idle') as never, host: hostName(s.host), ...(launcherOf(s) ? {launcher: launcherOf(s)} : {}),
  ...(time(s.startedAt) ? {startedAt: time(s.startedAt)} : {}),
});

/** 오른쪽 상세: 작업이면 단계·검증·세션, 세션 노드면 세션 한 줄과 사실. */
export async function providerDetail(store: Pick<WorkStore, 'getState' | 'loadDetail'>, id: string): Promise<HomeWorkDetail | null> {
  const t = pick(workText);
  const state = store.getState();
  if (id.startsWith(SESSION_PREFIX)) {
    const s = state.sessions.find(x => x.sessionId === id.slice(SESSION_PREFIX.length));
    if (!s) return null;
    return {title: sessionItem(s).title, sessions: [sessionRow(s)],
      facts: [s.cwd && {label: t.home.folder, value: s.cwd}, s.model && {label: t.home.model, value: s.model}].filter((f): f is {label: string; value: string} => !!f)};
  }
  const w = await store.loadDetail(id);
  const related = state.sessions.filter(s => s.sessionId === w.origin?.sessionId || s.openWorkIds.includes(w.id));
  return {
    title: w.title,
    steps: w.steps.map(s => ({id: s.id, title: s.activeTitle && s.status === 'in_progress' ? s.activeTitle : s.title,
      status: (STEP.has(s.status) ? s.status : 'pending') as never,
      ...(CHECK.has(s.verification.state) ? {verification: s.verification.state as never} : {}),
      ...(s.block?.detail ? {note: s.block.detail} : {})})),
    sessions: related.map(sessionRow),
    facts: [w.cwd && {label: t.home.folder, value: w.cwd}, w.origin && {label: t.home.session, value: w.origin.sessionId}].filter((f): f is {label: string; value: string} => !!f),
  };
}

/** 열린 결정 카드(결정 저장소). 없으면 카드는 「답 기다림」에 세지 않는다 */
interface CardRefsSource {
  openRefs(): OpenCardRefs;
  subscribe(listener: () => void): () => void;
}

/** Studio에 작업 공급자를 등록한다. ctx.work가 없는 옛 Studio에서는 아무것도 하지 않는다. 돌려받은 함수로 뺀다. */
export function registerWorkProvider(ctx: {work?: {provide(provider: WorkProviderLike): () => void}}, store: ProviderStore, cards?: CardRefsSource, chat?: WorkChatHooks): () => void {
  const work = ctx.work;
  if (!work || typeof work.provide !== 'function') return () => {};
  return work.provide({
    id: 'orbit',
    async list() {
      const state = store.getState();
      // 판이 맞지 않거나 읽지 못해 보일 것이 없으면 이유와 함께 던진다: Studio가 「공급자가 답하지 않음」을 보인다
      if (state.status === 'paused' && state.pause) throw new Error(state.pause.message);
      if (state.status === 'error' && !state.works.length && !state.sessions.length) throw new Error(state.error ?? 'OpenOrbit work history could not be read');
      // 지도 노드 아래 두 줄을 그리려고 열린 작업의 단계를 미리 읽어 둔다(읽으면 subscribe가 다시 부른다)
      store.ensureDetails(state.works.filter(w => w.state !== 'done').map(w => w.id));
      return homeItems(state, Date.now(), cards?.openRefs());
    },
    subscribe: onChange => {
      const offWork = store.subscribe(onChange);
      const offCards = cards?.subscribe(onChange);
      return () => { offWork(); offCards?.(); };
    },
    detail: id => providerDetail(store, id),
    // 작업 대화가 없는 Studio(API 0.1.27 전)나 데몬 클라이언트가 없으면 두 훅을 주지 않는다: Studio는 열기·새 일 시키기를 보이지 않는다
    ...(chat ? chatHooks(store, chat) : {}),
  });
}

/** open/compose: 해당 데몬 실행이 있으면 그 실행의 대화, 없으면 작업을 이어 하는 대화(`--resume`), compose는 작성 중인 글 */
function chatHooks(store: Pick<WorkStore, 'getState'>, chat: WorkChatHooks): Pick<WorkProviderLike, 'open' | 'compose'> {
  return {
    async open(id) {
      if (id.startsWith(SESSION_PREFIX)) {
        const runId = await chat.runFor({sessionId: id.slice(SESSION_PREFIX.length)}).catch(() => undefined);
        if (runId) chat.open({kind: 'run', runId});
        return;
      }
      const runId = await chat.runFor({workId: id}).catch(() => undefined);
      if (runId) return chat.open({kind: 'run', runId});
      const cwd = store.getState().works.find(w => w.id === id)?.cwd;
      chat.open({kind: 'work', workId: id, ...(cwd ? {cwd} : {})});
    },
    compose({text, cwd}) {
      chat.open({kind: 'compose', text, ...(cwd ? {cwd} : {})});
    },
  };
}

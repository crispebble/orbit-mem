// 작업 그래프·세션 보기의 표시 모델(순수 함수). 계약이 준 값을 다시 계산하지 않고 화면 문구·색조로 옮긴다.
// 화면 문구에는 「Work」를 쓰지 않는다(「작업」「단계」「세션」).
import type {FlowGraphTone} from '@onidot-labs/studio-plugin/ai';
import type {Run, Session, Step, StepBlock, Work, WorkSummary} from './types.ts';

/** Forma `Badge`·`FlowGraph`의 톤 이름 그대로 쓴다 */
export type Tone = FlowGraphTone;

export interface StatusView {
  label: string;
  tone: Tone;
  /** 계약이 아는 값이면 true. 모르는 값은 「알 수 없음」 */
  known: boolean;
}

const UNKNOWN: StatusView = {label: '알 수 없음', tone: 'neutral', known: false};

/**
 * 실행이 「지금 수행 중」이려면 `lastSeenAt`이 이 시간 안이어야 한다.
 * 근거 없는 구현 값: 계약·응답은 숫자를 주지 않는다(PC 설정 `stale` 시간은 응답에 없다). 호스트 훅은 도구 호출마다 관측을 갱신하므로
 * 오래 걸리는 시험·빌드 한 번(수 분)을 넘기지 않게 5분으로 둔다. 설계 페이지에 반영해야 한다.
 */
export const RUN_FRESH_MS = 5 * 60_000;

// ---- 단계 ----
// 문구는 Studio 2차 설계 「상태 표시 대응」을 따른다.
const STEP: Record<string, StatusView> = {
  pending: {label: '대기', tone: 'neutral', known: true},
  ready: {label: '준비됨', tone: 'accent', known: true},
  in_progress: {label: '진행 중', tone: 'accent', known: true},
  blocked: {label: '막힘', tone: 'warning', known: true},
  done: {label: '끝', tone: 'success', known: true},
  cancelled: {label: '취소', tone: 'neutral', known: true},
};
/** 막힌 단계는 사유로 가른다. 사용자가 풀 몫은 `waiting_user`뿐이고, 「실패」는 `run_failed`만 가리킨다. */
const BLOCKED: Record<string, StatusView> = {
  waiting_user: {label: '사용자 확인 기다림', tone: 'warning', known: true},
  run_failed: {label: '실패', tone: 'danger', known: true},
  run_lost: STEP.blocked, interrupted: STEP.blocked, stale: STEP.blocked, declared: STEP.blocked,
};

export function stepStatusView(status: string, block?: StepBlock): StatusView {
  if (status === 'blocked' && block) return BLOCKED[block.reason] ?? UNKNOWN;
  return STEP[status] ?? UNKNOWN;
}

/** 모르는 단계 상태는 배치에서 pending처럼 다룬다 */
export function layoutStatus(status: string): string {
  return STEP[status] ? status : 'pending';
}

// ---- 세션 ----
const SESSION: Record<string, StatusView> = {
  running: {label: '도는 중', tone: 'accent', known: true},
  waiting_user: {label: '사용자 확인 기다림', tone: 'warning', known: true},
  idle: {label: '쉬는 중', tone: 'neutral', known: true},
  stale: {label: '관측 오래됨', tone: 'warning', known: true},
  ended: {label: '끝남', tone: 'neutral', known: true},
};

export function sessionStateView(state: string): StatusView {
  return SESSION[state] ?? UNKNOWN;
}

// ---- 실행 ----
const RUN: Record<string, StatusView> = {
  running: {label: '도는 중', tone: 'accent', known: true},
  finished: {label: '끝남', tone: 'neutral', known: true},
  failed: {label: '실패', tone: 'danger', known: true},
  lost: {label: '연결 끊김', tone: 'warning', known: true},
};

const OBSERVATION_OLD: StatusView = {label: '관측 오래됨', tone: 'warning', known: true};

function ms(iso: string | undefined): number | undefined {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? undefined : t;
}

/** `running`이고 마지막 관측이 오래되지 않은 실행만 「지금 수행」 */
export function isRunningNow(run: Run, now: number): boolean {
  if (run.state !== 'running') return false;
  const seen = ms(run.lastSeenAt) ?? ms(run.startedAt);
  return seen !== undefined && now - seen <= RUN_FRESH_MS;
}

/** `running`인데 관측이 오래됨 */
export function isObservationOld(run: Run, now: number): boolean {
  return run.state === 'running' && !isRunningNow(run, now);
}

export function runStateView(run: Run, now: number): StatusView {
  if (isObservationOld(run, now)) return OBSERVATION_OLD;
  return RUN[run.state] ?? UNKNOWN;
}

/**
 * 단계를 수행 중인 실행. 실행의 `stepId`로만 잇는다.
 * 부모 세션 실행(`stepId` 없음)이나 다른 단계에 묶인 서브에이전트 실행은 이 단계의 수행으로 세지 않는다.
 */
export function stepRuns(work: Pick<Work, 'runs'>, stepId: string, now: number): {active: Run[]; observationOld: Run[]} {
  const mine = work.runs.filter(r => r.stepId === stepId);
  return {
    active: mine.filter(r => isRunningNow(r, now)),
    observationOld: mine.filter(r => isObservationOld(r, now)),
  };
}

// ---- 검증 배지 ----
export interface BadgeView {
  label: string;
  tone: Tone;
  /** 지난 검증(뒤에 커밋이 있음) */
  outdated: boolean;
}

/**
 * 검증은 단계 상태를 바꾸지 않고 배지로만 붙는다(설계 「상태 표시 대응」). 실패는 끝난 단계면 「끝남 · 검증 실패」,
 * 아니면 「검증 실패」, `needs_user`는 「사용자 확인 필요」, 지난 검증은 「오래됨」. 통과·결과 없음은 배지가 없다.
 */
export function verificationBadge(v: Step['verification'] | undefined, status: string): BadgeView | undefined {
  if (!v || v.state === 'none') return undefined;
  // 지난 검증(뒤에 커밋이 있음)은 통과였어도 「오래됨」이다
  if (v.outdated) return {label: '오래됨', tone: 'neutral', outdated: true};
  if (v.state === 'pass') return undefined;
  if (v.state === 'fail') return {label: status === 'done' ? '끝남 · 검증 실패' : '검증 실패', tone: 'danger', outdated: false};
  if (v.state === 'needs_user') return {label: '사용자 확인 필요', tone: 'warning', outdated: false};
  return {label: '알 수 없음', tone: 'neutral', outdated: false};
}

// ---- 막힘 사유 ----
const BLOCK: Record<string, string> = {
  waiting_user: '사용자 확인 기다림',
  run_failed: '진행 중 실패',
  run_lost: '실행 끊김',
  interrupted: '중단됨',
  stale: '오래 멈춤',
  declared: '막힘 보고',
};

/** 막힘 사유 문구. 모르는 사유는 「알 수 없음」, 자세한 글이 있으면 덧붙인다. */
export function blockText(block: StepBlock): string {
  const head = BLOCK[block.reason] ?? '알 수 없음';
  return block.detail ? `${head}: ${block.detail}` : head;
}

// ---- 작업 ----
/** 목록 줄의 상태. pendingReview는 「끝남 · 확인 전」 */
export function workStateView(w: Pick<WorkSummary, 'state'>, pendingReview: boolean): StatusView {
  if (w.state === 'open') return {label: '진행', tone: 'accent', known: true};
  if (w.state === 'done') {
    return pendingReview ? {label: '끝남 · 확인 전', tone: 'warning', known: true} : {label: '끝남', tone: 'neutral', known: true};
  }
  return UNKNOWN;
}

/** 진행 끝난 단계 수 / 전체(취소된 단계는 전체에서 뺀다) */
export function progressOf(w: Pick<WorkSummary, 'counts' | 'stepCount'>): {done: number; total: number} {
  return {done: w.counts.done, total: Math.max(0, w.stepCount - w.counts.cancelled)};
}

/** 사용자 확인을 기다리는 단계 수(작업 목록 줄 표시용). `blocked` 수에서 정확한 사유는 알 수 없으므로 work_get으로 읽은 단계가 있어야 한다. */
export function waitingSteps(work: Pick<Work, 'steps'>): Step[] {
  return work.steps.filter(s => s.status === 'blocked' && s.block?.reason === 'waiting_user');
}

export interface WorkNode<T extends Pick<WorkSummary, 'id' | 'parent' | 'updatedAt'>> {
  work: T;
  children: WorkNode<T>[];
}

/** 하위 작업을 상위 아래에 묶는다. 상위가 목록에 없으면(범위 밖·정리됨) 최상위에 둔다. 같은 층은 최근 갱신순. */
export function groupWorks<T extends Pick<WorkSummary, 'id' | 'parent' | 'updatedAt'>>(works: T[]): WorkNode<T>[] {
  const nodes = new Map<string, WorkNode<T>>(works.map(w => [w.id, {work: w, children: []}]));
  const roots: WorkNode<T>[] = [];
  for (const node of nodes.values()) {
    const parent = node.work.parent ? nodes.get(node.work.parent.workId) : undefined;
    // 자기 자신·순환을 막는다
    if (parent && parent !== node && !isAncestor(nodes, node, parent)) parent.children.push(node);
    else roots.push(node);
  }
  const byRecent = (a: WorkNode<T>, b: WorkNode<T>) => (ms(b.work.updatedAt) ?? 0) - (ms(a.work.updatedAt) ?? 0) || a.work.id.localeCompare(b.work.id);
  const sort = (list: WorkNode<T>[]) => {
    list.sort(byRecent);
    list.forEach(n => sort(n.children));
  };
  sort(roots);
  return roots;
}

function isAncestor<T extends Pick<WorkSummary, 'id' | 'parent' | 'updatedAt'>>(nodes: Map<string, WorkNode<T>>, node: WorkNode<T>, candidate: WorkNode<T>): boolean {
  // candidate의 상위를 따라 올라가 node를 만나면 순환
  const seen = new Set<string>();
  let cur: WorkNode<T> | undefined = candidate;
  while (cur && !seen.has(cur.work.id)) {
    if (cur === node) return true;
    seen.add(cur.work.id);
    cur = cur.work.parent ? nodes.get(cur.work.parent.workId) : undefined;
  }
  return false;
}

// ---- 시각 ----
const timeFormat = (timeZone?: string) =>
  new Intl.DateTimeFormat('ko-KR', {month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false, timeZone});

/** UTC 시각 문자열을 PC 시간대의 「10월 28일 14:05」로. timeZone은 시험용(없으면 PC 시간대). 읽을 수 없으면 원문. */
export function formatTime(iso: string | undefined, timeZone?: string): string {
  const t = ms(iso);
  if (t === undefined) return iso ?? '';
  const part = Object.fromEntries(timeFormat(timeZone).formatToParts(new Date(t)).map(p => [p.type, p.value]));
  // 시각이 자정이면 hour12:false가 「24」를 낼 수 있어 0으로 바꾼다
  const hour = part.hour === '24' ? '00' : part.hour;
  return `${part.month}월 ${part.day}일 ${hour}:${part.minute}`;
}

export function sessionTitle(s: Session): string {
  const where = s.branch ? `${s.branch}` : s.cwd?.split('/').filter(Boolean).pop() ?? s.sessionId.slice(0, 8);
  return `${s.host} · ${where}`;
}

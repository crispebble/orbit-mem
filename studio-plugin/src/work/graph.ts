// 작업 하나를 Forma `FlowGraph` props로 바꾼다(순수 함수). 열·선은 계약의 `wave`·`after`를 그대로 쓰고 다시 계산하지 않는다.
// 이 대응은 orbit-code 계약을 아는 플러그인의 몫이라 Forma 부품에 넣지 않는다(설계 「Forma 2차」 묶음 5).
import type {FlowGraphBadge, FlowGraphColumn, FlowGraphEdge, FlowGraphNode} from '@onidot-labs/orbit-studio-plugin/ai';
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import {stepMarks, stepMarkView} from './assumptions.ts';
import {blockText, stepRuns, stepStatusView, verificationBadge} from './display.ts';
import type {Step, Work} from './types.ts';

export interface GraphData {
  nodes: FlowGraphNode[];
  edges: FlowGraphEdge[];
  columns: FlowGraphColumn[];
  activeColumn?: number;
}

const HOST: Record<string, string> = {claude: 'Claude Code', codex: 'Codex'};

/** 계약의 호스트 값(`claude`·`codex`)을 화면 이름으로. 모르는 값은 「알 수 없음」 */
export function hostLabel(host: string | undefined): string {
  return (host && HOST[host]) || pick(workText).unknown;
}

/** 단계 담당: 메인·서브에이전트(역할)·별도 세션. 모델이 있으면 덧붙인다. */
export function assigneeText(a: Step['assignee']): string | undefined {
  if (!a) return undefined;
  const w = pick(workText).who;
  let text: string;
  if (a.kind === 'main') text = w.main(hostLabel(a.host));
  else if (a.kind === 'subagent') text = a.role || w.subagentUnknown;
  else if (a.kind === 'session') text = w.separateSession(hostLabel(a.host));
  else return pick(workText).unknown;
  return a.model ? `${text} · ${a.model}` : text;
}

/** 걸린 시간 글: 「1분 미만」「40분」「1시간 29분」「1일 2시간」(영어는 「Under 1 min」「40 min」「1 h 29 min」「1 d 2 h」) */
export function spanText(ms: number): string {
  const t = pick(workText).time;
  const minutes = Math.floor(Math.max(0, ms) / 60_000);
  if (minutes < 1) return t.lessThanMinute;
  if (minutes < 60) return t.minutes(minutes);
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return minutes % 60 ? t.hoursMinutes(hours, minutes % 60) : t.hours(hours);
  const days = Math.floor(hours / 24);
  return hours % 24 ? t.daysHours(days, hours % 24) : t.days(days);
}

/** 「3분 전」. 읽을 수 없는 시각이면 undefined */
export function agoText(iso: string | undefined, now: number): string | undefined {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return undefined;
  const time = pick(workText).time;
  return now - t < 60_000 ? time.justNow : time.ago(spanText(now - t));
}

function since(iso: string | undefined, now: number): number | undefined {
  const t = iso ? Date.parse(iso) : NaN;
  return Number.isNaN(t) ? undefined : now - t;
}

/** 진행 중 열: 진행 중 단계가 있는 첫 열, 없으면 끝나지 않은(끝·취소가 아닌) 단계가 있는 첫 열 */
export function activeColumnOf(steps: readonly Step[]): number | undefined {
  const first = (pick: (s: Step) => boolean) => {
    const waves = steps.filter(pick).map(s => s.wave);
    return waves.length ? Math.min(...waves) : undefined;
  };
  return first(s => s.status === 'in_progress') ?? first(s => s.status !== 'done' && s.status !== 'cancelled');
}

function node(step: Step, work: Pick<Work, 'runs' | 'assumptions'>, now: number): FlowGraphNode {
  const view = stepStatusView(step.status, step.block);
  const badges: FlowGraphBadge[] = [];
  const v = verificationBadge(step.verification, step.status);
  if (v) badges.push({label: v.label, tone: v.tone});
  if (step.status === 'in_progress') {
    const runs = stepRuns(work, step.id, now);
    if (runs.active.length === 0 && runs.observationOld.length > 0) badges.push({label: pick(workText).staleObservation, tone: 'warning'});
  }
  // 가정(AU-3): 열린 가정에 기댄 단계·답을 기다리는 단계·뒤집혀 다시 열린 단계
  for (const mark of stepMarks(work, step)) {
    const m = stepMarkView(mark);
    badges.push({label: m.label, tone: m.tone});
  }
  if (step.childWorkIds.length) badges.push({label: pick(workText).graph.childWorks(step.childWorkIds.length), tone: 'violet'});

  let description: string | undefined;
  let meta: string | undefined;
  if (step.status === 'in_progress') {
    description = step.activeTitle;
    const ms = since(step.startedAt, now);
    if (ms !== undefined) meta = pick(workText).time.forSpan(spanText(ms));
  } else if (step.status === 'blocked' && step.block) {
    // 상태 칩이 이미 사유를 말하면(사용자 확인 기다림·실패) 자세한 글만 덧붙인다
    const head = blockText({...step.block, detail: undefined});
    const same = head === view.label || (step.block.reason === 'run_failed' && view.label === pick(workText).failed);
    description = same ? step.block.detail : blockText(step.block);
    const ms = since(step.block.since, now);
    if (ms !== undefined) meta = pick(workText).time.sinceAgo(spanText(ms));
  }

  const assignee = assigneeText(step.assignee);
  return {
    id: step.id,
    column: step.wave,
    title: step.title,
    description,
    status: {label: view.label, tone: view.tone},
    chips: assignee ? [assignee] : [],
    badges,
    meta,
    emphasis: step.status === 'in_progress' ? 'current' : step.status === 'cancelled' || !view.known ? 'muted' : undefined,
  };
}

/** 작업 → FlowGraph props. 카드는 `order` 순(목록 보기가 order 순), 열은 `wave`, 선은 `after` */
export function toFlowGraph(work: Pick<Work, 'steps' | 'runs' | 'assumptions'>, now: number): GraphData {
  const steps = [...work.steps].sort((a, b) => a.order - b.order);
  const nodes = steps.map(s => node(s, work, now));
  const edges: FlowGraphEdge[] = steps.flatMap(s =>
    s.after.map((a): FlowGraphEdge =>
      a.source === 'native'
        ? {from: a.stepId, to: s.id, variant: 'solid'}
        : {from: a.stepId, to: s.id, variant: 'dashed', label: a.source === 'order' ? pick(workText).graph.inferredPredecessor : pick(workText).unknown},
    ),
  );
  const waves = [...new Set(steps.map(s => s.wave))].sort((a, b) => a - b);
  const columns = waves.map(wave => {
    const counted = steps.filter(s => s.wave === wave && s.status !== 'cancelled');
    const g = pick(workText).graph;
    return {column: wave, title: g.batch(wave + 1), summary: g.batchSummary(counted.filter(s => s.status === 'done').length, counted.length)};
  });
  return {nodes, edges, columns, activeColumn: activeColumnOf(steps)};
}

export interface Neighbor {
  stepId: string;
  /** 목록에 없는 단계면 undefined */
  title?: string;
  source: string;
}

/** 선행(after 순서대로)과 후행(이 단계를 after로 둔 단계) */
export function neighbors(steps: readonly Step[], stepId: string): {before: Neighbor[]; after: Neighbor[]} {
  const byId = new Map(steps.map(s => [s.id, s]));
  const me = byId.get(stepId);
  const before = (me?.after ?? []).map(a => ({stepId: a.stepId, title: byId.get(a.stepId)?.title, source: a.source}));
  const after = steps.flatMap(s => s.after.filter(a => a.stepId === stepId).map(a => ({stepId: s.id, title: s.title, source: a.source})));
  return {before, after};
}

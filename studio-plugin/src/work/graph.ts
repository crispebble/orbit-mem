// 작업 하나를 Forma `FlowGraph` props로 바꾼다(순수 함수). 열·선은 계약의 `wave`·`after`를 그대로 쓰고 다시 계산하지 않는다.
// 이 대응은 Orbit 계약을 아는 플러그인의 몫이라 Forma 부품에 넣지 않는다(설계 「Forma 2차」 묶음 5).
import type {FlowGraphBadge, FlowGraphColumn, FlowGraphEdge, FlowGraphNode} from '@onidot-labs/studio-plugin/ai';
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
  return (host && HOST[host]) || '알 수 없음';
}

/** 단계 담당: 메인·서브에이전트(역할)·별도 세션. 모델이 있으면 덧붙인다. */
export function assigneeText(a: Step['assignee']): string | undefined {
  if (!a) return undefined;
  let text: string;
  if (a.kind === 'main') text = `메인 · ${hostLabel(a.host)}`;
  else if (a.kind === 'subagent') text = a.role || '서브에이전트 · 종류 미확인';
  else if (a.kind === 'session') text = `별도 세션 · ${hostLabel(a.host)}`;
  else return '알 수 없음';
  return a.model ? `${text} · ${a.model}` : text;
}

/** 걸린 시간 글: 「1분 미만」「40분」「1시간 29분」「1일 2시간」 */
export function spanText(ms: number): string {
  const minutes = Math.floor(Math.max(0, ms) / 60_000);
  if (minutes < 1) return '1분 미만';
  if (minutes < 60) return `${minutes}분`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return minutes % 60 ? `${hours}시간 ${minutes % 60}분` : `${hours}시간`;
  const days = Math.floor(hours / 24);
  return hours % 24 ? `${days}일 ${hours % 24}시간` : `${days}일`;
}

/** 「3분 전」. 읽을 수 없는 시각이면 undefined */
export function agoText(iso: string | undefined, now: number): string | undefined {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return undefined;
  return now - t < 60_000 ? '방금' : `${spanText(now - t)} 전`;
}

function since(iso: string | undefined, now: number): number | undefined {
  const t = iso ? Date.parse(iso) : NaN;
  return Number.isNaN(t) ? undefined : now - t;
}

const EDGE_LABEL: Record<string, string> = {order: '순서로 추정한 선행'};

/** 진행 중 열: 진행 중 단계가 있는 첫 열, 없으면 끝나지 않은(끝·취소가 아닌) 단계가 있는 첫 열 */
export function activeColumnOf(steps: readonly Step[]): number | undefined {
  const first = (pick: (s: Step) => boolean) => {
    const waves = steps.filter(pick).map(s => s.wave);
    return waves.length ? Math.min(...waves) : undefined;
  };
  return first(s => s.status === 'in_progress') ?? first(s => s.status !== 'done' && s.status !== 'cancelled');
}

function node(step: Step, work: Pick<Work, 'runs'>, now: number): FlowGraphNode {
  const view = stepStatusView(step.status, step.block);
  const badges: FlowGraphBadge[] = [];
  const v = verificationBadge(step.verification, step.status);
  if (v) badges.push({label: v.label, tone: v.tone});
  if (step.status === 'in_progress') {
    const runs = stepRuns(work, step.id, now);
    if (runs.active.length === 0 && runs.observationOld.length > 0) badges.push({label: '관측 오래됨', tone: 'warning'});
  }
  if (step.childWorkIds.length) badges.push({label: `하위 작업 ${step.childWorkIds.length}`, tone: 'violet'});

  let description: string | undefined;
  let meta: string | undefined;
  if (step.status === 'in_progress') {
    description = step.activeTitle;
    const ms = since(step.startedAt, now);
    if (ms !== undefined) meta = `${spanText(ms)}째`;
  } else if (step.status === 'blocked' && step.block) {
    description = blockText(step.block);
    const ms = since(step.block.since, now);
    if (ms !== undefined) meta = `${spanText(ms)} 전부터`;
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
export function toFlowGraph(work: Pick<Work, 'steps' | 'runs'>, now: number): GraphData {
  const steps = [...work.steps].sort((a, b) => a.order - b.order);
  const nodes = steps.map(s => node(s, work, now));
  const edges: FlowGraphEdge[] = steps.flatMap(s =>
    s.after.map((a): FlowGraphEdge =>
      a.source === 'native'
        ? {from: a.stepId, to: s.id, variant: 'solid'}
        : {from: a.stepId, to: s.id, variant: 'dashed', label: EDGE_LABEL[a.source] ?? '알 수 없음'},
    ),
  );
  const waves = [...new Set(steps.map(s => s.wave))].sort((a, b) => a - b);
  const columns = waves.map(wave => {
    const counted = steps.filter(s => s.wave === wave && s.status !== 'cancelled');
    return {column: wave, title: `묶음 ${wave + 1}`, summary: `끝 ${counted.filter(s => s.status === 'done').length}/${counted.length}`};
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

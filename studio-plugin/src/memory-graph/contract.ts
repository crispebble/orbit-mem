// 기억 계약(orbit.memory) memory_graph 응답 검사. 판 규칙은 작업 계약과 같아 checkContract·오류 형식을 함께 쓴다.
// response.schema.json으로 거르지 않는다. 필요한 칸만 확인하고, 이 판이 모르는 선·묶음 종류는 그대로 넘겨 그리는 쪽이 센다.
import {checkContract, throwIfError, WorkShapeError} from '../work/contract.ts';

export const GRAPH_TOOL = 'memory_graph';

export interface GraphNode {
  id: string;
  /** rule·fact·decision·lesson·journal. 모르는 값도 그대로 둔다(「알 수 없음」) */
  kind: string;
  state: string;
  scope: string;
  project?: string;
  title: string;
  tags: string[];
  createdAt: string;
  updatedAt: string;
  supersededBy?: string;
}

/** link(명시 연결)·supersede(대체). 이 판이 모르는 kind도 그대로 둔다 */
export interface GraphEdge {
  id: string;
  kind: string;
  from: string;
  to: string;
  relation?: string;
  note?: string;
  origin?: string;
  at?: string;
}

/** tag·session·file. 이 판이 모르는 kind도 그대로 둔다 */
export interface GraphGroup {
  kind: string;
  key: string;
  label: string;
  host?: string;
  members: string[];
}

export interface MemoryGraph {
  focus?: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  groups: GraphGroup[];
  total: number;
  truncated: boolean;
  limitMax: number;
  depthMax: number;
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown): v is string => typeof v === 'string';
const optStr = (v: unknown): string | undefined => (str(v) ? v : undefined);
const strList = (v: unknown): string[] => (Array.isArray(v) ? v.filter(str) : []);
const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v);

function need(cond: boolean, why: string): void {
  if (!cond) throw new WorkShapeError(GRAPH_TOOL, why);
}

/** 판을 확인하고(WorkPaused), 오류 응답은 WorkToolError, 모양이 틀리면 WorkShapeError로 던진다. */
export function parseMemoryGraph(out: unknown): MemoryGraph {
  checkContract(out, GRAPH_TOOL);
  throwIfError(out, GRAPH_TOOL);
  need(isObj(out) && Array.isArray(out.nodes) && Array.isArray(out.edges) && Array.isArray(out.groups), 'nodes·edges·groups');
  const o = out as Record<string, unknown>;
  const nodes = (o.nodes as unknown[]).map((n): GraphNode => {
    need(isObj(n) && str(n.id) && str(n.kind) && str(n.state) && str(n.scope) && str(n.title) && str(n.updatedAt), 'memory fields');
    const m = n as Record<string, unknown>;
    return {
      id: m.id as string, kind: m.kind as string, state: m.state as string, scope: m.scope as string, project: optStr(m.project),
      title: m.title as string, tags: strList(m.tags), createdAt: optStr(m.createdAt) ?? (m.updatedAt as string), updatedAt: m.updatedAt as string,
      supersededBy: optStr(m.supersededBy),
    };
  });
  const edges = (o.edges as unknown[]).map((e): GraphEdge => {
    need(isObj(e) && str(e.id) && str(e.kind) && str(e.from) && str(e.to), 'edge fields');
    const m = e as Record<string, unknown>;
    return {id: m.id as string, kind: m.kind as string, from: m.from as string, to: m.to as string, relation: optStr(m.relation), note: optStr(m.note), origin: optStr(m.origin), at: optStr(m.at)};
  });
  const groups = (o.groups as unknown[]).map((g): GraphGroup => {
    need(isObj(g) && str(g.kind) && str(g.key) && Array.isArray(g.members), 'group fields');
    const m = g as Record<string, unknown>;
    return {kind: m.kind as string, key: m.key as string, label: optStr(m.label) ?? (m.key as string), host: optStr(m.host), members: strList(m.members)};
  });
  return {
    focus: optStr(o.focus),
    nodes, edges, groups,
    total: isInt(o.total) ? o.total : nodes.length,
    truncated: o.truncated === true,
    limitMax: isInt(o.limitMax) ? o.limitMax : nodes.length,
    depthMax: isInt(o.depthMax) ? o.depthMax : 1,
  };
}

// memory_graph 응답 → Forma 관계망 그래프(NetworkGraph·NetworkPanel) 입력. DOM·React를 쓰지 않는 순수 함수다.
// 규칙은 Atlas 「설계 · Orbit 2차 — 기억 그래프」 「Studio 보기」(rev 3): 클러스터는 대표 태그(덮기 순서 7개 + 그 밖),
// 옛 Knowledge 표식 태그는 클러스터·같은 태그 선에서 뺀다, 종류는 모양이 아니라 설명(말풍선)으로, 색은 클러스터.
import {memoryKindLabels, memoryStateLabels, type MemoryKind, type MemoryState} from '../parts/vocabulary.ts';
import type {GraphEdge, GraphGroup, GraphNode, MemoryGraph} from './contract.ts';

/* ---------- 부품 입력(Forma 0.10.4 forma-ui `/graph`의 같은 이름 타입과 같은 모양) ---------- */
// 플러그인은 Forma를 직접 가져오지 않는다. Studio 플러그인 API가 그래프 부품을 다시 내보내면 그 타입으로 바꾼다.
// 겹쳐보기 icon만 다르다(여기는 이름이고, 화면이 아이콘으로 바꾼다).
export interface NetworkNode { id: string; label: string; cluster: string; weight?: number; description?: string; muted?: boolean }
export interface NetworkEdge { id: string; from: string; to: string; relation: string; arrow?: boolean }
export interface NetworkCluster { id: string; label: string; tone?: string }
export type EdgeLine = 'solid' | 'dashed' | 'dotted';
export interface NetworkRelation { id: string; label: string; tone: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'violet'; line?: EdgeLine; arrow?: boolean; emphasis?: number }
export type OverlayIcon = 'chain' | 'time' | 'retro' | 'session';
export interface MemoryOverlay { id: OverlayIcon; label: string; icon: OverlayIcon; description: string; count: number; paths: string[][]; highlight?: string[]; numbered?: boolean }

export interface MemoryNetwork {
  nodes: NetworkNode[];
  edges: NetworkEdge[];
  clusters: NetworkCluster[];
  relations: NetworkRelation[];
  overlays: MemoryOverlay[];
  /** 이 판이 모르는 선·묶음 종류의 수(그리지 않았다) */
  unknownRelations: number;
}

export const UNKNOWN = '알 수 없음';
export const OTHER_CLUSTER = 'other';
export const OTHER_LABEL = '그 밖';
/** Forma 클러스터 팔레트 칸 수(rev 17 「색 토큰」). 여덟째부터 색을 되풀이하면 색이 클러스터를 뜻하지 않게 된다 */
export const CLUSTER_MAX = 7;
const PALETTE = ['c1', 'c2', 'c3', 'c4', 'c5', 'c6', 'c7'] as const;

/** 시안 3판 어댑터의 구현 값. 실제 화면에서 고친다 */
export const TAG_ALL_PAIRS_MAX = 5;
export const TAG_PICKS = 4;
export const TAG_GROUP_MAX = 40;
export const FILE_ALL_PAIRS_MAX = 4;
export const RECENT_OVERLAY = 14;

/** 관계 종류(범례). 선 모양은 사용자 결정 O19, 색·진하기는 시안 3판 */
export const MEMORY_RELATIONS: NetworkRelation[] = [
  {id: 'tag', label: '같은 태그', tone: 'neutral', line: 'solid', emphasis: 0.8},
  {id: 'link', label: '연결', tone: 'accent', line: 'solid', emphasis: 1.1},
  {id: 'supersede', label: '대체', tone: 'danger', line: 'solid', arrow: true, emphasis: 2.2},
  {id: 'retro', label: '회고 연결', tone: 'warning', line: 'solid', emphasis: 1.1},
  {id: 'session', label: '같은 세션', tone: 'violet', line: 'dashed', emphasis: 0.9},
  {id: 'file', label: '같은 파일', tone: 'success', line: 'dotted', emphasis: 0.9},
];

/* ---------- 제외 태그 ---------- */
/** 옛 Knowledge·Claude 메모리에서 옮길 때 붙은 표식 */
const MARKER_TAG = /^(imported|space|since|type|옛|superseded-by):/;
/** 종류 이름 태그. 종류는 배지로 보이므로 클러스터 기준에서 뺀다(제안) */
const KIND_TAGS = new Set(['규칙', '사실', '결정', '교훈', '일지', '회고', 'rule', 'fact', 'decision', 'lesson', 'journal']);

const tagKey = (tag: string) => tag.trim().toLowerCase();

/** 클러스터 기준과 「같은 태그」 선에서 빼는 태그 */
export function isExcludedTag(tag: string): boolean {
  const k = tagKey(tag);
  return k === '' || MARKER_TAG.test(k) || KIND_TAGS.has(k);
}

/** 기억 하나의 주제 태그 키(대소문자 무시, 제외 태그 뺌) */
export function topicKeys(tags: readonly string[]): Set<string> {
  const out = new Set<string>();
  for (const t of tags) if (!isExcludedTag(t)) out.add(tagKey(t));
  return out;
}

/* ---------- 대표 태그(클러스터) ---------- */
export interface Clustering {
  /** 기억 id → 클러스터 id(태그 키 또는 other) */
  clusterOf: Map<string, string>;
  /** 고른 순서. tone은 c1~c7, 그 밖은 other */
  clusters: NetworkCluster[];
}

/**
 * 덮기 순서: 아직 클러스터에 들지 않은 기억이 가장 많이 가진 주제 태그를 하나씩 고른다(같으면 키 순). 7개까지.
 * 기억은 자기가 가진 고른 태그 중 먼저 고른 것에 든다. 하나도 없으면 그 밖. 같은 입력이면 같은 결과다.
 */
export function clusterByRepresentativeTag(nodes: readonly Pick<GraphNode, 'id' | 'tags'>[]): Clustering {
  const keys = nodes.map(n => topicKeys(n.tags));
  const label = new Map<string, string>();
  for (const n of nodes) for (const t of n.tags) if (!isExcludedTag(t) && !label.has(tagKey(t))) label.set(tagKey(t), t.trim());
  let uncovered = nodes.map((_, i) => i).filter(i => keys[i].size > 0);
  const picked: string[] = [];
  while (picked.length < CLUSTER_MAX && uncovered.length > 0) {
    const count = new Map<string, number>();
    for (const i of uncovered) for (const k of keys[i]) count.set(k, (count.get(k) ?? 0) + 1);
    let best: string | undefined, bestN = 0;
    for (const [k, c] of count) if (c > bestN || (c === bestN && best !== undefined && k < best)) { best = k; bestN = c; }
    if (best === undefined) break;
    picked.push(best);
    const chosen = best;
    uncovered = uncovered.filter(i => !keys[i].has(chosen));
  }
  const clusterOf = new Map<string, string>();
  let others = 0;
  nodes.forEach((n, i) => {
    const k = picked.find(p => keys[i].has(p));
    clusterOf.set(n.id, k ?? OTHER_CLUSTER);
    if (!k) others++;
  });
  const clusters: NetworkCluster[] = picked.map((k, i) => ({id: k, label: label.get(k) ?? k, tone: PALETTE[i]}));
  if (others) clusters.push({id: OTHER_CLUSTER, label: OTHER_LABEL, tone: 'other'});
  return {clusterOf, clusters};
}

/* ---------- 글 ---------- */
export const kindLabel = (kind: string) => memoryKindLabels[kind as MemoryKind] ?? UNKNOWN;
export const stateLabel = (state: string) => memoryStateLabels[state as MemoryState] ?? UNKNOWN;
export function scopeLabel(scope: string, project?: string): string {
  if (scope === 'global') return '전역';
  const key = scope.replace(/^project:/, '');
  return `프로젝트 · ${project || key.split('/').filter(Boolean).pop() || key}`;
}
export const memoryDescription = (n: Pick<GraphNode, 'kind' | 'state' | 'scope' | 'project'>) =>
  `${kindLabel(n.kind)} · ${stateLabel(n.state)} · ${scopeLabel(n.scope, n.project)}`;

/* ---------- 변환 ---------- */
export interface NetworkOptions {
  /** 이 종류만 보인다. 클러스터는 거르기 전 전체로 정해 색이 바뀌지 않는다 */
  kinds?: ReadonlySet<string>;
}

/** 같은 묶음 안에서 짝을 고르는 결정적 해시(0~1) */
function hash01(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) { h ^= text.charCodeAt(i); h = Math.imul(h, 16777619); }
  h ^= h >>> 15; h = Math.imul(h, 2246822519); h ^= h >>> 13;
  return (h >>> 0) / 4294967296;
}

const byUpdated = (byId: Map<string, GraphNode>) => (a: string, b: string) => {
  const x = byId.get(a)!.updatedAt, y = byId.get(b)!.updatedAt;
  return x < y ? -1 : x > y ? 1 : a < b ? -1 : a > b ? 1 : 0;
};

export function toMemoryNetwork(graph: MemoryGraph, opts: NetworkOptions = {}): MemoryNetwork {
  const {clusterOf, clusters: allClusters} = clusterByRepresentativeTag(graph.nodes);
  const visible = opts.kinds ? graph.nodes.filter(n => opts.kinds!.has(n.kind)) : graph.nodes;
  const byId = new Map(visible.map(n => [n.id, n] as const));
  const nodes: NetworkNode[] = visible.map(n => ({
    id: n.id, label: n.title, cluster: clusterOf.get(n.id) ?? OTHER_CLUSTER,
    description: memoryDescription(n), muted: n.state === 'stale' || n.state === 'hidden',
  }));
  const used = new Set(nodes.map(n => n.cluster));
  const clusters = allClusters.filter(c => used.has(c.id));

  let unknownRelations = 0;
  const edges: NetworkEdge[] = [];
  for (const e of graph.edges) {
    if (e.kind !== 'link' && e.kind !== 'supersede') { unknownRelations++; continue; }
    if (!byId.has(e.from) || !byId.has(e.to)) continue;
    if (e.kind === 'supersede') edges.push({id: e.id, from: e.from, to: e.to, relation: 'supersede', arrow: true});
    else edges.push({id: e.id, from: e.from, to: e.to, relation: e.origin === 'retro' ? 'retro' : 'link'});
  }
  const pair = (g: GraphGroup, a: string, b: string, relation: string) => {
    const [x, y] = a < b ? [a, b] : [b, a];
    edges.push({id: `${relation}:${g.key}:${x}:${y}`, from: x, to: y, relation});
  };
  for (const g of graph.groups) {
    if (g.kind !== 'tag' && g.kind !== 'session' && g.kind !== 'file') { unknownRelations++; continue; }
    const members = [...new Set(g.members.filter(m => byId.has(m)))];
    if (members.length < 2) continue;
    if (g.kind === 'tag') {
      if (isExcludedTag(g.key) || members.length > TAG_GROUP_MAX) continue;
      const sorted = members.sort();
      if (sorted.length <= TAG_ALL_PAIRS_MAX) {
        for (let i = 0; i < sorted.length; i++) for (let j = i + 1; j < sorted.length; j++) pair(g, sorted[i], sorted[j], 'tag');
        continue;
      }
      for (const m of sorted) for (let k = 0; k < TAG_PICKS; k++) {
        const other = sorted[Math.floor(Math.pow(hash01(`${g.key}|${m}|${k}`), 1.7) * sorted.length)];
        if (other !== m) pair(g, m, other, 'tag');
      }
    } else if (g.kind === 'session') {
      const timeline = members.sort(byUpdated(byId));
      for (let i = 0; i + 1 < timeline.length; i++) pair(g, timeline[i], timeline[i + 1], 'session');
    } else if (members.length <= FILE_ALL_PAIRS_MAX) {
      for (let i = 0; i < members.length; i++) for (let j = i + 1; j < members.length; j++) pair(g, members[i], members[j], 'file');
    }
  }
  return {nodes, edges: dedupe(edges), clusters, relations: MEMORY_RELATIONS, overlays: buildOverlays(visible, graph.edges, graph.groups), unknownRelations};
}

/** 같은 관계·같은 두 기억의 선은 하나(대소문자만 다른 태그 묶음 둘이 같은 짝을 낼 수 있다) */
function dedupe(edges: NetworkEdge[]): NetworkEdge[] {
  const seen = new Set<string>();
  return edges.filter(e => {
    const key = e.arrow ? `${e.relation}>${e.from}>${e.to}` : `${e.relation}-${e.from < e.to ? e.from : e.to}-${e.from < e.to ? e.to : e.from}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

/* ---------- 주제 겹쳐보기(시안 3판의 넷) ---------- */
/** 옛 → 새로 이어지는 대체 사슬(둘 이상). 긴 것부터 */
export function supersedeChains(edges: readonly GraphEdge[], has: (id: string) => boolean): string[][] {
  const next = new Map<string, string>(), hasPrev = new Set<string>();
  for (const e of edges) if (e.kind === 'supersede' && has(e.from) && has(e.to)) { next.set(e.from, e.to); hasPrev.add(e.to); }
  const chains: string[][] = [];
  for (const start of next.keys()) {
    if (hasPrev.has(start)) continue;
    const chain = [start];
    const seen = new Set(chain);
    for (let cur = next.get(start); cur && !seen.has(cur); cur = next.get(cur)) { chain.push(cur); seen.add(cur); }
    if (chain.length >= 2) chains.push(chain);
  }
  return chains.sort((a, b) => b.length - a.length || (a[0] < b[0] ? -1 : 1));
}

function buildOverlays(visible: readonly GraphNode[], edges: readonly GraphEdge[], groups: readonly GraphGroup[]): MemoryOverlay[] {
  const byId = new Map(visible.map(n => [n.id, n] as const));
  const has = (id: string) => byId.has(id);
  const chains = supersedeChains(edges, has);
  const recent = [...visible].sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : a.updatedAt > b.updatedAt ? -1 : a.id < b.id ? -1 : 1)).slice(0, RECENT_OVERLAY).reverse().map(n => n.id);
  const retro = edges.filter(e => e.kind === 'link' && e.origin === 'retro' && has(e.from) && has(e.to));
  const sessions = groups.filter(g => g.kind === 'session').map(g => [...new Set(g.members.filter(has))].sort(byUpdated(byId))).filter(s => s.length >= 2);
  return [
    {id: 'chain', label: '대체 사슬', icon: 'chain', description: '옛 기억이 새 기억으로 이어지는 순서를 따라 겹쳐 그립니다.', count: chains.length, paths: chains},
    {id: 'time', label: '시간순(고친 때)', icon: 'time', description: `최근에 고친 기억 ${recent.length}개를 고친 때 순서대로 이어 번호를 붙입니다.`, count: recent.length, paths: recent.length >= 2 ? [recent] : [], numbered: true},
    {id: 'retro', label: '회고 연결', icon: 'retro', description: '회고가 이어 둔 연결을 짝으로 겹쳐 그립니다.', count: retro.length, paths: retro.map(e => [e.from, e.to]), highlight: [...new Set(retro.flatMap(e => [e.from, e.to]))]},
    {id: 'session', label: '같은 세션', icon: 'session', description: '한 작업 세션에서 만든 기억을 고친 때 순서대로 이어 그립니다.', count: sessions.length, paths: sessions},
  ];
}

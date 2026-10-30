// oni MCP 도구 결과(구조화 결과)를 화면 모델로 바꾸는 순수 함수들.
// 도구 계약 원본: orbit/internal/mcpserver/server.go, orbit/internal/memory/{model,proposal}.go
import type {InboxItem} from '@onidot-labs/studio-plugin';
import type {
  MemoryKind,
  MemorySource,
  MemorySourceKind,
  MemoryState,
  MemoryVersion,
  OrbitMemory,
  Proposal,
  ProposalEvidence,
  ProposalKind,
} from './parts/index.ts';

// ---------- oni가 주는 모양(Go JSON). 빈 목록은 null로 올 수 있다. ----------

export interface OniSummary {
  id: string;
  kind: string;
  state: string;
  scope: string;
  title: string;
  summary?: string | null;
  tags?: string[] | null;
  version: number;
  updatedAt: number;
}

export interface OniSource {
  id?: string;
  kind: string;
  ref?: string;
  excerpt?: string;
  hash?: string;
  at?: number;
}

export interface OniMemory {
  id: string;
  kind: string;
  scope: string;
  project?: string;
  state: string;
  title: string;
  body: string;
  tags?: string[] | null;
  version: number;
  createdAt: number;
  updatedAt: number;
  supersededBy?: string;
  staleReason?: string;
  days?: string[] | null;
  sources?: OniSource[] | null;
  links?: OniLink[] | null;
}

/** memory_get의 살아 있는 명시 연결(orbit 0.3.6~) */
export interface OniLink {
  id: string;
  /** 상대 기억 */
  memoryId: string;
  kind?: string;
  note?: string;
  origin?: string;
  at?: number;
}

export interface OniVersion {
  version: number;
  action: string;
  reason?: string;
  at: number;
  memory: OniMemory;
}

export interface OniPayload {
  kind?: string;
  mergedTitle?: string;
  mergedBody?: string;
  path?: string;
  baseHash?: string;
  content?: string;
  scope?: string;
  /** link: 잇는 근거 */
  note?: string;
  /** retag: 뺄 태그(표기 차이 무시) */
  fromTags?: string[];
  /** retag: 넣을 태그 */
  toTags?: string[];
}

export interface OniProposal {
  id: string;
  kind: string;
  title: string;
  body?: string;
  memoryIds?: string[] | null;
  payload: OniPayload | null;
  status: string;
  createdAt: number;
  decidedAt?: number;
  retroId?: string;
  note?: string;
  memories?: OniSummary[] | null;
}

// ---------- 글 ----------

const KINDS: readonly MemoryKind[] = ['rule', 'fact', 'decision', 'lesson', 'journal'];
const STATES: readonly MemoryState[] = ['candidate', 'confirmed', 'stale', 'hidden'];
const SOURCE_KINDS: readonly MemorySourceKind[] = ['quote', 'file', 'command', 'session', 'host_memory', 'legacy'];

const sourceLabels: Record<MemorySourceKind, string> = {
  quote: '내 말', file: '파일', command: '명령', session: '대화 기록', host_memory: '호스트 기억', legacy: '옛 Knowledge',
};

const proposalKindLabels: Record<string, string> = {
  confirm: '후보 확정', stale: '낡은 기억 정리', merge: '기억 합치기', reclassify: '종류 다시 분류', apply_file: '파일 반영', direction: '방향 제안',
  edit: '기억 고치기', revert: '변경 되돌리기', link: '관련 기억 잇기', retag: '태그 정리',
};

const actionLabels: Record<string, string> = {
  create: '', seen: '다시 나옴', confirm: '확정', update: '고침', edit: '고침', stale: '낡음으로 표시', restore: '후보로 되돌림', supersede: '대체됨', reclassify: '종류 바꿈', retag: '태그 바꿈', revert: '되돌림',
};

export const proposalStatusLabels: Record<string, string> = {pending: '대기', approved: '승인함', rejected: '거절함', settled: '다른 길로 처리됨'};

export function proposalKindLabel(kind: string): string {
  return proposalKindLabels[kind] ?? kind;
}

/** 월·일·시각. 올해가 아니면 시각 없이 연도를 붙인다. 시각이 없으면 빈 글. */
export function formatDate(ms: number | undefined, now: Date = new Date()): string {
  if (!ms) return '';
  const d = new Date(ms);
  if (d.getFullYear() !== now.getFullYear()) return `${d.getFullYear()}년 ${d.getMonth() + 1}월 ${d.getDate()}일`;
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return `${d.getMonth() + 1}월 ${d.getDate()}일 ${hh}:${mm}`;
}

/** 범위(global·project:<키>)를 사람이 읽는 이름으로 */
export function scopeLabel(scope: string, project?: string): string {
  if (!scope || scope === 'global') return '전역';
  if (project) return project;
  return scope.startsWith('project:') ? scope.slice('project:'.length) : scope;
}

// ---------- 기억 ----------

const asKind = (v: string): MemoryKind => (KINDS as readonly string[]).includes(v) ? (v as MemoryKind) : 'fact';
const asState = (v: string): MemoryState => (STATES as readonly string[]).includes(v) ? (v as MemoryState) : 'candidate';

export function toMemoryRecord(s: OniSummary, now: Date = new Date()): OrbitMemory {
  return {
    id: s.id,
    title: s.title,
    content: s.summary ?? '',
    kind: asKind(s.kind),
    state: asState(s.state),
    scope: scopeLabel(s.scope),
    tags: s.tags ?? [],
    version: s.version,
    updatedAt: formatDate(s.updatedAt, now),
  };
}

function toSource(s: OniSource, now: Date): MemorySource {
  const kind: MemorySourceKind = (SOURCE_KINDS as readonly string[]).includes(s.kind) ? (s.kind as MemorySourceKind) : 'session';
  const when = formatDate(s.at, now);
  const out: MemorySource = {kind, label: when ? `${sourceLabels[kind]} · ${when}` : sourceLabels[kind]};
  if (s.excerpt) out.text = s.excerpt;
  if (s.ref) out.path = s.ref;
  return out;
}

function toVersions(history: readonly OniVersion[], now: Date): MemoryVersion[] {
  const asc = [...history].sort((a, b) => a.version - b.version);
  const out = asc.map((h, i): MemoryVersion => {
    const prev = asc[i - 1];
    const label = h.action === 'create'
      ? (h.memory.state === 'confirmed' ? '확정으로 남김' : '후보로 남김')
      : actionLabels[h.action] || h.action;
    const v: MemoryVersion = {version: h.version, label, at: formatDate(h.at, now)};
    if (h.reason) v.by = h.reason;
    if (h.action === 'confirm') v.tone = 'ok';
    if (prev && prev.memory.body !== h.memory.body) {
      v.before = prev.memory.body;
      v.after = h.memory.body;
    }
    return v;
  });
  return out.reverse();
}

export function toMemoryDetail(m: OniMemory, history: readonly OniVersion[] = [], now: Date = new Date()): OrbitMemory {
  const rec: OrbitMemory = {
    id: m.id,
    title: m.title,
    content: m.body,
    kind: asKind(m.kind),
    state: asState(m.state),
    scope: scopeLabel(m.scope, m.project),
    tags: m.tags ?? [],
    version: m.version,
    updatedAt: formatDate(m.updatedAt, now),
    sources: (m.sources ?? []).map(s => toSource(s, now)),
  };
  if (history.length) rec.history = toVersions(history, now);
  if (m.supersededBy) rec.supersededBy = m.supersededBy;
  return rec;
}

// ---------- 제안 ----------

export interface ProposalRow {
  id: string;
  title: string;
  kind: string;
  kindLabel: string;
  date: string;
  status: string;
  pending: boolean;
}

export function toProposalRow(p: OniProposal, now: Date = new Date()): ProposalRow {
  return {id: p.id, title: p.title, kind: p.kind, kindLabel: proposalKindLabel(p.kind), date: formatDate(p.createdAt, now), status: p.status, pending: p.status === 'pending'};
}

export interface ProposalGroup {
  kind: string;
  label: string;
  rows: ProposalRow[];
}

/** 제안을 종류별로 묶는다. 건수가 많은 묶음부터, 같으면 목록에 먼저 나온 종류부터. 묶음 안은 받은 순서 그대로. */
export function groupProposalRows(rows: readonly ProposalRow[]): ProposalGroup[] {
  const groups = new Map<string, ProposalGroup>();
  for (const r of rows) {
    let g = groups.get(r.kind);
    if (!g) groups.set(r.kind, g = {kind: r.kind, label: r.kindLabel, rows: []});
    g.rows.push(r);
  }
  const order = [...groups.values()];
  return order.map((g, i) => ({g, i})).sort((a, b) => b.g.rows.length - a.g.rows.length || a.i - b.i).map(x => x.g);
}

const PROPOSAL_KINDS: readonly ProposalKind[] = ['confirm', 'stale', 'merge', 'reclassify', 'apply_file', 'direction', 'link'];

/**
 * Forma 0.9.0에 없는 Orbit 종류: 기억 고치기·변경 되돌리기·태그 정리는 가장 가까운 「기억 정리」로 그리고
 * 바뀔 내용(diff, 바뀔 태그)은 설명으로 보인다. 그 밖에 모르는 종류는 확정으로 본다.
 */
const asProposalKind = (kind: string): ProposalKind => {
  if (kind === 'edit' || kind === 'revert' || kind === 'retag') return 'cleanup';
  return (PROPOSAL_KINDS as readonly string[]).includes(kind) ? (kind as ProposalKind) : 'confirm';
};

function addLines(text: string) {
  return text.split('\n').map((t, i) => ({no: i + 1, kind: 'add' as const, text: t}));
}

const MAX_EVIDENCE = 8;

/** 근거: 대상 기억의 출처들. 출처가 없는 기억은 제목을 보인다. */
function evidenceOf(memories: readonly OniMemory[], now: Date): ProposalEvidence[] {
  const out: ProposalEvidence[] = [];
  for (const m of memories) {
    const sources = m.sources ?? [];
    if (!sources.length) {
      out.push({id: `${m.id}`, label: '기억', text: m.title});
      continue;
    }
    sources.forEach((s, i) => {
      const src = toSource(s, now);
      out.push({id: `${m.id}-${i}`, label: src.label, text: s.excerpt || s.ref || m.title});
    });
  }
  return out.slice(0, MAX_EVIDENCE);
}

export function toProposal(p: OniProposal, related: readonly OniMemory[] = [], now: Date = new Date()): Proposal {
  const payload = p.payload ?? {};
  const date = formatDate(p.createdAt, now);
  const out: Proposal = {id: p.id, kind: asProposalKind(p.kind), title: p.title, source: p.retroId ? `${date} 회고에서` : date};
  const evidence = evidenceOf(related, now);
  if (evidence.length) out.evidence = evidence;
  if (p.kind === 'apply_file') {
    const content = payload.content ?? '';
    out.diff = {path: payload.path ?? '', lines: addLines(content), text: content};
    if (p.body) out.explanation = p.body;
  } else if (p.kind === 'merge') {
    const text = payload.mergedBody ?? '';
    out.diff = {path: `합친 기억 · ${payload.mergedTitle ?? ''}`, lines: addLines(text), text};
    // 고친 제목은 합칠 기억의 제목이 된다(oni merge: title이 mergedTitle을 바꾼다).
    if (payload.mergedTitle) out.title = payload.mergedTitle;
    if (p.body) out.explanation = p.body;
  } else if (p.kind === 'direction') {
    out.diff = {path: `새 결정 기억 · ${p.title}`, lines: addLines(p.body ?? ''), text: p.body ?? ''};
  } else if (p.kind === 'link') {
    // 잇기는 기억 내용을 바꾸지 않아 고쳐서 승인이 없다(oni가 거부한다). 두 기억은 근거에 보인다.
    const why = [p.body, payload.note && `잇는 근거: ${payload.note}`].filter(Boolean).join('\n\n');
    if (why) out.explanation = why;
  } else {
    if (p.body) out.explanation = p.body;
    // 확정·재분류는 승인할 때 받은 제목·본문을 대상 기억(하나일 때)에 쓴다. 고칠 칸에는 그 기억의 제목·본문을 채운다.
    // 낡음 정리는 본문을 쓰지 않고, 대상이 여럿이거나 아직 못 읽었으면 고쳐서 승인을 내지 않는다.
    const ids = p.memoryIds ?? [];
    const target = ids.length === 1 ? related.find(m => m.id === ids[0]) : undefined;
    if ((p.kind === 'confirm' || p.kind === 'reclassify') && target) {
      out.title = target.title;
      out.body = target.body;
    }
  }
  return out;
}

// ---------- 받은 일 ----------

export function toInboxItems(proposals: readonly OniProposal[], now: Date = new Date()): InboxItem[] {
  return proposals
    .filter(p => p.status === 'pending')
    .map(p => ({
      id: p.id,
      kind: 'proposal' as const,
      title: p.title,
      subtitle: `${proposalKindLabel(p.kind)} · ${formatDate(p.createdAt, now)}`,
      group: 'Orbit',
      unread: true,
      createdAt: p.createdAt,
      actions: [
        {id: 'approve', label: '승인', tone: 'primary' as const},
        {id: 'reject', label: '거절', tone: 'danger' as const},
      ],
    }));
}

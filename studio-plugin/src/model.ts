// orb MCP 도구 결과(구조화 결과)를 화면 모델로 바꾸는 순수 함수들.
// 도구 계약 원본: orbit/internal/mcpserver/server.go, orbit/internal/memory/{model,proposal}.go
import {pick} from './i18n/index.ts';
import {memoryText} from './i18n/messages/memory.ts';
import {proposalText} from './i18n/messages/proposals.ts';
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

// ---------- orb가 주는 모양(Go JSON). 빈 목록은 null로 올 수 있다. ----------

export interface OrbSummary {
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

export interface OrbSource {
  id?: string;
  kind: string;
  ref?: string;
  excerpt?: string;
  hash?: string;
  at?: number;
}

export interface OrbMemory {
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
  sources?: OrbSource[] | null;
  links?: OrbLink[] | null;
}

/** memory_get의 살아 있는 명시 연결(orbit 0.3.6~) */
export interface OrbLink {
  id: string;
  /** 상대 기억 */
  memoryId: string;
  kind?: string;
  note?: string;
  origin?: string;
  at?: number;
}

export interface OrbVersion {
  version: number;
  action: string;
  reason?: string;
  at: number;
  memory: OrbMemory;
}

export interface OrbPayload {
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

export interface OrbProposal {
  id: string;
  kind: string;
  title: string;
  body?: string;
  memoryIds?: string[] | null;
  payload: OrbPayload | null;
  status: string;
  createdAt: number;
  decidedAt?: number;
  retroId?: string;
  note?: string;
  memories?: OrbSummary[] | null;
}

// ---------- 감사 목록(proposals {status:'audit'}) ----------

export type AuditVerdict = 'pending' | 'helped' | 'harmful' | 'no_effect' | 'mixed';
export type AuditEvidence = 'seen_days' | 'followed_verified' | 'verified_fact';

export interface OrbAuditChange {
  ledgerId: string;
  proposalId: string;
  kind: string;
  title: string;
  evidence?: string;
  actor?: string;
  at: number;
  targets?: string[] | null;
  change?: string;
  verdict: string;
  revertedBy?: string;
  revertOf?: string;
  autoReverted?: boolean;
}

export interface OrbAuditConflict {
  id: string;
  kind: 'conflict';
  memoryId: string;
  otherId: string;
  note?: string;
  actor?: string;
  at: number;
  title: string;
  state: string;
  otherTitle?: string;
}

export interface OrbRepeatedCorrection {
  key: string;
  targetKind: string;
  target: string;
  title?: string;
  sessions: number;
  corrections: number;
  threshold: number;
  over: boolean;
  harness: boolean;
  first: number;
  last: number;
  quotes?: string[] | null;
  ackedAt?: number;
}

export interface OrbAudit {
  changes: OrbAuditChange[] | null;
  conflicts: OrbAuditConflict[] | null;
  corrections: OrbRepeatedCorrection[] | null;
  pending: number;
  policy?: {confirmSeenDays: number; confirmFollowedVerified: number; correctionRepeatSessions: number};
  policyPending?: string[] | null;
}

/** 빈 목록이 null로 와도 화면은 배열만 본다 */
export interface AuditView {
  changes: OrbAuditChange[];
  conflicts: OrbAuditConflict[];
  corrections: OrbRepeatedCorrection[];
}

export function toAuditView(a: OrbAudit | undefined): AuditView {
  return {changes: a?.changes ?? [], conflicts: a?.conflicts ?? [], corrections: a?.corrections ?? []};
}

/** 사람이 볼 것: 대기 제안 + 어긋남 + 아직 확인하지 않은 반복 교정 */
export function auditAttention(pending: number, a: OrbAudit | undefined): number {
  const v = toAuditView(a);
  return pending + v.conflicts.length + v.corrections.filter(c => !c.ackedAt).length;
}

// ---------- 글 ----------

const KINDS: readonly MemoryKind[] = ['rule', 'fact', 'decision', 'lesson', 'journal'];
const STATES: readonly MemoryState[] = ['candidate', 'confirmed', 'stale', 'hidden'];
const SOURCE_KINDS: readonly MemorySourceKind[] = ['quote', 'file', 'command', 'session', 'host_memory', 'legacy'];

/** 출처 이름: 지금 언어로 */
const sourceLabel = (kind: MemorySourceKind): string => pick(memoryText).sources[kind];

export const proposalStatusLabel = (status: string): string => (pick(proposalText).statuses as Record<string, string>)[status] ?? status;

export function proposalKindLabel(kind: string): string {
  return (pick(proposalText).rowKinds as Record<string, string>)[kind] ?? kind;
}

/** 월·일·시각. 올해가 아니면 시각 없이 연도를 붙인다. 시각이 없으면 빈 글. */
export function formatDate(ms: number | undefined, now: Date = new Date()): string {
  if (!ms) return '';
  const d = new Date(ms);
  const t = pick(memoryText).date;
  if (d.getFullYear() !== now.getFullYear()) return t.withYear(d.getFullYear(), d.getMonth() + 1, d.getDate());
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return t.withTime(d.getMonth() + 1, d.getDate(), `${hh}:${mm}`);
}

/** 범위(global·project:<키>)를 사람이 읽는 이름으로 */
export function scopeLabel(scope: string, project?: string): string {
  if (!scope || scope === 'global') return pick(memoryText).scope.global;
  if (project) return project;
  return scope.startsWith('project:') ? scope.slice('project:'.length) : scope;
}

// ---------- 기억 ----------

const asKind = (v: string): MemoryKind => (KINDS as readonly string[]).includes(v) ? (v as MemoryKind) : 'fact';
const asState = (v: string): MemoryState => (STATES as readonly string[]).includes(v) ? (v as MemoryState) : 'candidate';

export function toMemoryRecord(s: OrbSummary, now: Date = new Date()): OrbitMemory {
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

function toSource(s: OrbSource, now: Date): MemorySource {
  const kind: MemorySourceKind = (SOURCE_KINDS as readonly string[]).includes(s.kind) ? (s.kind as MemorySourceKind) : 'session';
  const when = formatDate(s.at, now);
  const out: MemorySource = {kind, label: when ? `${sourceLabel(kind)} · ${when}` : sourceLabel(kind)};
  if (s.excerpt) out.text = s.excerpt;
  if (s.ref) out.path = s.ref;
  return out;
}

function toVersions(history: readonly OrbVersion[], now: Date): MemoryVersion[] {
  const asc = [...history].sort((a, b) => a.version - b.version);
  const out = asc.map((h, i): MemoryVersion => {
    const prev = asc[i - 1];
    const actions = pick(memoryText).versionActions as Record<string, string>;
    const label = h.action === 'create'
      ? (h.memory.state === 'confirmed' ? actions.createdConfirmed : actions.createdCandidate)
      : actions[h.action] || h.action;
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

export function toMemoryDetail(m: OrbMemory, history: readonly OrbVersion[] = [], now: Date = new Date()): OrbitMemory {
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

export function toProposalRow(p: OrbProposal, now: Date = new Date()): ProposalRow {
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
 * Forma 0.9.0에 없는 OpenOrbit 종류: 기억 고치기·변경 되돌리기·태그 정리는 가장 가까운 「기억 정리」로 그리고
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
function evidenceOf(memories: readonly OrbMemory[], now: Date): ProposalEvidence[] {
  const out: ProposalEvidence[] = [];
  for (const m of memories) {
    const sources = m.sources ?? [];
    if (!sources.length) {
      out.push({id: `${m.id}`, label: pick(memoryText).evidenceMemory, text: m.title});
      continue;
    }
    sources.forEach((s, i) => {
      const src = toSource(s, now);
      out.push({id: `${m.id}-${i}`, label: src.label, text: s.excerpt || s.ref || m.title});
    });
  }
  return out.slice(0, MAX_EVIDENCE);
}

export function toProposal(p: OrbProposal, related: readonly OrbMemory[] = [], now: Date = new Date()): Proposal {
  const payload = p.payload ?? {};
  const date = formatDate(p.createdAt, now);
  const out: Proposal = {id: p.id, kind: asProposalKind(p.kind), title: p.title, source: p.retroId ? pick(proposalText).fromRetro(date) : date};
  const evidence = evidenceOf(related, now);
  if (evidence.length) out.evidence = evidence;
  if (p.kind === 'apply_file') {
    const content = payload.content ?? '';
    out.diff = {path: payload.path ?? '', lines: addLines(content), text: content};
    if (p.body) out.explanation = p.body;
  } else if (p.kind === 'merge') {
    const text = payload.mergedBody ?? '';
    out.diff = {path: pick(proposalText).mergedPath(payload.mergedTitle ?? ''), lines: addLines(text), text};
    // 고친 제목은 합칠 기억의 제목이 된다(orb merge: title이 mergedTitle을 바꾼다).
    if (payload.mergedTitle) out.title = payload.mergedTitle;
    if (p.body) out.explanation = p.body;
  } else if (p.kind === 'direction') {
    out.diff = {path: pick(proposalText).directionPath(p.title), lines: addLines(p.body ?? ''), text: p.body ?? ''};
  } else if (p.kind === 'link') {
    // 잇기는 기억 내용을 바꾸지 않아 고쳐서 승인이 없다(orb가 거부한다). 두 기억은 근거에 보인다.
    const why = [p.body, payload.note && pick(proposalText).linkReason(payload.note)].filter(Boolean).join('\n\n');
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


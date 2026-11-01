// 도구 호출 인자를 만드는 순수 함수들.
import type {MemoryFormValue, MemoryKind, MemoryListState, ProposalApproval} from './parts/index.ts';
import type {OrbMemory, OrbProposal} from './model.ts';
import {isGlobalScope} from './parts/vocabulary.ts';

export interface FindParams {
  query: string;
  kind: MemoryKind | 'all';
  state: MemoryListState;
}

const MAX = 50;

/** memory_find 인자. 범위는 모든 프로젝트(Studio는 세션 프로젝트가 없다). */
export function findArgs(p: FindParams): Record<string, unknown> {
  const args: Record<string, unknown> = {scope: 'all', limit: MAX};
  const query = p.query.trim();
  if (query) args.query = query;
  if (p.kind !== 'all') args.kinds = [p.kind];
  args.states = p.state === 'all' ? ['candidate', 'confirmed', 'stale']
    : p.state === 'active' ? ['candidate', 'confirmed']
    : [p.state];
  return args;
}

export interface DecideOptions {
  title?: string;
  body?: string;
  note?: string;
}

export interface DecideArgs {
  id: string;
  decision: 'approve' | 'reject';
  title?: string;
  body?: string;
  note?: string;
}

export function decideArgs(id: string, decision: 'approve' | 'reject', o: DecideOptions = {}): DecideArgs {
  const args: DecideArgs = {id, decision};
  if (o.title !== undefined) args.title = o.title;
  if (o.body !== undefined) args.body = o.body;
  const note = o.note?.trim();
  if (note) args.note = note;
  return args;
}

export interface RevertArgs {
  id: string;
  decision: 'revert';
  note?: string;
}

/** 자동 변경 되돌리기(id는 proposalId 또는 ledgerId) */
export function revertArgs(id: string, note?: string): RevertArgs {
  const args: RevertArgs = {id, decision: 'revert'};
  const n = note?.trim();
  if (n) args.note = n;
  return args;
}

/** 어긋남(f-…)·반복 교정(correction:…)을 확인함으로 닫기 */
export function dismissArgs(id: string): {id: string; decision: 'dismiss'} {
  return {id, decision: 'dismiss'};
}

export interface DecideManyArgs {
  ids: string[];
  decision: 'approve' | 'reject';
  note?: string;
}

/** 같은 답으로 여럿을 결정하는 proposal_decide 인자(orbit 0.3.9~). 고쳐서 승인은 하나씩 decideArgs로 한다. */
export function decideManyArgs(ids: readonly string[], decision: 'approve' | 'reject', note?: string): DecideManyArgs {
  const args: DecideManyArgs = {ids: [...ids], decision};
  const n = note?.trim();
  if (n) args.note = n;
  return args;
}

/** ProposalDetail의 승인(고쳐서 승인 포함)을 proposal_decide 인자로. 고친 제목은 title, 고친 내용은 body로 보낸다(고친 것만). */
export function approvalToDecideArgs(p: OrbProposal, a: ProposalApproval): DecideArgs {
  const o: DecideOptions = {};
  if (a.titleEdited) o.title = a.title;
  if (a.textEdited) o.body = a.text;
  return decideArgs(p.id, 'approve', o);
}

export type UpdateAction = 'edit' | 'confirm' | 'stale' | 'restore' | 'supersede';

export interface UpdateArgs {
  id: string;
  expectedVersion?: number;
  action: UpdateAction;
  kind?: string;
  title?: string;
  body?: string;
  tags?: string[];
  scope?: string;
  supersededBy?: string;
  reason?: string;
}

const sameTags = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((t, i) => t === b[i]);

/**
 * MemoryForm 저장값을 memory_update 단계들로 바꾼다. 바뀐 칸만 보낸다.
 * 상태 바꾸기는 편집과 같은 호출에 action으로 실어 보낸다. 대체는 편집 뒤 별도 호출이며,
 * 그 호출의 expectedVersion은 앞 호출 결과로 호출하는 쪽이 채운다.
 */
export function memoryUpdateSteps(m: OrbMemory, v: MemoryFormValue): UpdateArgs[] {
  const step: UpdateArgs = {id: m.id, expectedVersion: m.version, action: 'edit'};
  let changed = false;
  if (v.title !== m.title) { step.title = v.title; changed = true; }
  if (v.content !== m.body) { step.body = v.content; changed = true; }
  if (v.kind !== m.kind) { step.kind = v.kind; changed = true; }
  if (!sameTags(v.tags, m.tags ?? [])) { step.tags = v.tags; changed = true; }
  if (isGlobalScope(v.scope) && m.scope !== 'global') { step.scope = 'global'; changed = true; }

  const target = v.supersededBy ? m.state : v.stale ? 'stale' : v.state;
  if (target !== m.state) {
    step.action = target === 'stale' ? 'stale' : target === 'confirmed' ? 'confirm' : 'restore';
    changed = true;
  }
  const steps: UpdateArgs[] = changed ? [step] : [];
  if (v.supersededBy) steps.push({id: m.id, action: 'supersede', supersededBy: v.supersededBy, ...(changed ? {} : {expectedVersion: m.version})});
  return steps;
}

export function statusArgs(m: OrbMemory, action: 'confirm' | 'stale' | 'restore', reason?: string): UpdateArgs {
  const args: UpdateArgs = {id: m.id, expectedVersion: m.version, action};
  if (reason) args.reason = reason;
  return args;
}

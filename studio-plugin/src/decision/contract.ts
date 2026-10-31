// orbit.decision 1.0 응답 읽기. response.schema.json으로 거르지 않는다(계약 README 「판」): 필요한 칸만 확인하고 모르는 칸은 버린다.
import {WorkShapeError} from '../work/contract.ts';
import type {AnswerPart, Decision, DecisionChanges, DecisionPage, DecisionQuestion} from './types.ts';

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown) => (typeof v === 'string' && v.length ? v : undefined);
const strs = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);

function parseQuestion(v: unknown): DecisionQuestion | null {
  if (!isObj(v) || typeof v.text !== 'string') return null;
  const options = (Array.isArray(v.options) ? v.options : []).flatMap(o =>
    isObj(o) && typeof o.id === 'string' && typeof o.label === 'string' ? [{id: o.id, label: o.label, ...(str(o.detail) ? {detail: str(o.detail)} : {})}] : []);
  return {
    text: v.text, ...(str(v.header) ? {header: str(v.header)} : {}), multiSelect: v.multiSelect === true, options,
    ...(str(v.recommended) ? {recommended: str(v.recommended)} : {}), ...(str(v.reason) ? {reason: str(v.reason)} : {}),
  };
}

export function parseAnswers(v: unknown): AnswerPart[] {
  return (Array.isArray(v) ? v : []).map(a => {
    const o = isObj(a) ? a : {};
    const ids = strs(o.optionIds);
    return {...(ids.length ? {optionIds: ids} : {}), ...(str(o.text) ? {text: str(o.text)} : {})};
  });
}

/** 카드 하나. id나 질문이 없으면 null */
export function parseDecision(v: unknown): Decision | null {
  if (!isObj(v) || typeof v.id !== 'string' || !v.id) return null;
  const questions = (Array.isArray(v.questions) ? v.questions : []).map(parseQuestion).filter((q): q is DecisionQuestion => !!q);
  if (!questions.length) return null;
  const w = isObj(v.work) ? v.work : undefined;
  const a = isObj(v.answer) ? v.answer : undefined;
  return {
    id: v.id, state: str(v.state) ?? 'unknown', source: str(v.source) ?? 'unknown', owner: str(v.owner) ?? 'unknown',
    createdAt: str(v.createdAt) ?? '', ...(str(v.updatedAt) ? {updatedAt: str(v.updatedAt)} : {}), questions,
    ...(str(v.context) ? {context: str(v.context)} : {}),
    links: (Array.isArray(v.links) ? v.links : []).flatMap(l => isObj(l) && typeof l.kind === 'string'
      ? [{kind: l.kind, ...(str(l.label) ? {label: str(l.label)} : {}), ...(str(l.ref) ? {ref: str(l.ref)} : {})}] : []),
    ...(w ? {work: Object.fromEntries(['host', 'sessionId', 'workId', 'stepId', 'waitKey'].flatMap(k => (str(w[k]) ? [[k, str(w[k])]] : [])))} : {}),
    ...(str(v.expiresAt) ? {expiresAt: str(v.expiresAt)} : {}),
    ...(str(v.closedAt) ? {closedAt: str(v.closedAt)} : {}), ...(str(v.closeReason) ? {closeReason: str(v.closeReason)} : {}),
    ...(a && str(a.by) && str(a.at) ? {answer: {answers: parseAnswers(a.answers), by: str(a.by)!, at: str(a.at)!}} : {}),
  };
}

export function parseDecisionPage(out: unknown): DecisionPage {
  if (!isObj(out) || !Array.isArray(out.items)) throw new WorkShapeError('decision_list', 'items');
  return {items: out.items.map(parseDecision).filter((d): d is Decision => !!d), seq: str(out.seq) ?? '', truncated: out.truncated === true};
}

export function parseDecisionChanges(out: unknown): DecisionChanges {
  if (!isObj(out) || typeof out.seq !== 'string') throw new WorkShapeError('decision_changes', 'seq');
  return {
    seq: out.seq, decisionIds: strs(out.decisionIds), reset: out.reset === true, more: out.more === true,
    waitMax: typeof out.waitMax === 'number' && out.waitMax >= 0 ? out.waitMax : 0,
  };
}

/** 응답의 오류 칸: 코드·글·지금의 카드. 오류가 아니면 undefined */
export function decisionError(out: unknown): {code: string; message?: string; decision?: Decision} | undefined {
  if (!isObj(out) || !isObj(out.error)) return undefined;
  const e = out.error;
  const decision = parseDecision(e.decision);
  return {code: str(e.code) ?? 'UNKNOWN', ...(str(e.message) ? {message: str(e.message)} : {}), ...(decision ? {decision} : {})};
}

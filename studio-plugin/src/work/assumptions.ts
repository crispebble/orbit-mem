// 조사(AU-2)·가정(AU-3)의 표시 모델(순수 함수). 계약이 준 값을 다시 계산하지 않고 문구·색조로 옮긴다.
// 모르는 열거값은 「알 수 없음」으로 두고, 지도 배지는 만들지 않는다.
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import type {Assumption, Step, Work} from './types.ts';
import type {Tone} from './display.ts';

/** 지도 단계에 붙는 표시. assume=열린 가정에 기대 진행 중, ask=답을 기다림, reopened=뒤집혀 다시 열림 */
export type StepMark = 'assume' | 'ask' | 'reopened';

/** 가정이 이 단계를 가리키는가. 가정의 `steps`는 단계 `id`나 호스트 이름표(`native.id`, 계획 id)로 적힌다. */
export function leansOn(a: Assumption, step: Pick<Step, 'id' | 'native'>): boolean {
  return a.steps.includes(step.id) || (!!step.native?.id && a.steps.includes(step.native.id));
}

/** 이 단계가 기댄 가정과 그 표시(같은 표시는 한 번). 열린 `assume`·`ask`와 뒤집힌 것만 표시가 된다. */
export function stepMarks(work: Pick<Work, 'assumptions'>, step: Pick<Step, 'id' | 'native'>): StepMark[] {
  const marks = new Set<StepMark>();
  for (const a of work.assumptions ?? []) {
    if (!leansOn(a, step)) continue;
    if (a.state === 'overturned') marks.add('reopened');
    else if (a.state === 'open' && a.rung === 'assume') marks.add('assume');
    else if (a.state === 'open' && a.rung === 'ask') marks.add('ask');
  }
  return (['ask', 'assume', 'reopened'] as const).filter(m => marks.has(m));
}

export function stepMarkView(mark: StepMark): {label: string; tone: Tone} {
  const m = pick(workText).assumption.mark;
  if (mark === 'ask') return {label: m.ask, tone: 'warning'};
  if (mark === 'reopened') return {label: m.reopened, tone: 'violet'};
  return {label: m.assume, tone: 'accent'};
}

/** 이 단계가 기댄 가정 전부(표시와 무관하게). 단계 상세용 */
export function assumptionsOfStep(work: Pick<Work, 'assumptions'>, step: Pick<Step, 'id' | 'native'>): Assumption[] {
  return (work.assumptions ?? []).filter(a => leansOn(a, step));
}

export interface ViewLabel {
  label: string;
  tone: Tone;
  known: boolean;
}

const unknown = (): ViewLabel => ({label: pick(workText).unknown, tone: 'neutral', known: false});

export function rungView(rung: string): ViewLabel {
  const r = pick(workText).assumption.rung;
  if (rung === 'evidence') return {label: r.evidence, tone: 'success', known: true};
  if (rung === 'assume') return {label: r.assume, tone: 'accent', known: true};
  if (rung === 'ask') return {label: r.ask, tone: 'warning', known: true};
  return unknown();
}

export function assumptionStateView(state: string): ViewLabel {
  const s = pick(workText).assumption.state;
  if (state === 'settled') return {label: s.settled, tone: 'neutral', known: true};
  if (state === 'open') return {label: s.open, tone: 'accent', known: true};
  if (state === 'confirmed') return {label: s.confirmed, tone: 'success', known: true};
  if (state === 'overturned') return {label: s.overturned, tone: 'violet', known: true};
  if (state === 'answered') return {label: s.answered, tone: 'success', known: true};
  // 사람이 답하지 않고 카드를 닫음: 가정은 그대로 둔다
  if (state === 'closed') return {label: s.closed, tone: 'neutral', known: true};
  return unknown();
}

export function confidenceLabel(c: string): string {
  const m = pick(workText).assumption.confidence;
  return c === 'low' ? m.low : c === 'medium' ? m.medium : c === 'high' ? m.high : pick(workText).unknown;
}

export function sizeLabel(size: string | undefined): string | undefined {
  if (!size) return undefined;
  const m = pick(workText).assumption.size;
  return size === 'small' ? m.small : size === 'medium' ? m.medium : size === 'large' ? m.large : pick(workText).unknown;
}

/** 조사 칸이 길면 처음엔 접어 둔다(결정 지점 4개 초과, 모르는 것·출처 합 6개 초과, 문제 글 240자 초과) */
export function isLongDiscovery(d: {problem: string; decisions: unknown[]; unknowns: unknown[]; sources: unknown[]}): boolean {
  return d.decisions.length > 4 || d.unknowns.length + d.sources.length > 6 || d.problem.length > 240;
}

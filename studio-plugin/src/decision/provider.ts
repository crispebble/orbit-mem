// Orbit Studio Needs you에 주는 결정 공급자(플러그인 API 0.1.26 `ctx.decisions.provide`, Studio 4차 S4-3).
// 저장소의 카드를 Studio가 아는 모양으로만 옮긴다. 옛 Studio(ctx.decisions 없음)에서는 등록하지 않는다.
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import type {DecisionStore} from './store.ts';
import type {AnswerOutcome, AnswerPart, Decision, DecisionSource} from './types.ts';

/** Studio `DecisionCard`(0.1.26)와 같은 모양. 옛 패키지 타입에는 없어 좁게 둔다. */
export interface StudioCard {
  id: string;
  state: string;
  source: string;
  relay: boolean;
  createdAt: number;
  updatedAt?: number;
  questions: Decision['questions'];
  context?: string;
  links?: {kind: string; label: string; url?: string}[];
  workItemId?: string;
  meta?: string;
  answer?: {answers: AnswerPart[]; by: string; at: number};
  closedAt?: number;
  closeReason?: string;
}

type StudioOutcome = {ok: true; card?: StudioCard} | {ok: false; code: 'CONFLICT' | 'CANNOT_RELAY' | 'FAILED'; message?: string; card?: StudioCard};

interface DecisionProviderLike {
  id: string;
  list(query: {since: number}, signal: AbortSignal): Promise<readonly StudioCard[]>;
  subscribe?(onChange: () => void): () => void;
  answer(id: string, answer: {answers: AnswerPart[]}, signal: AbortSignal): Promise<StudioOutcome>;
}

/** Studio의 답이 기다리는 쪽에 닿는 owner. 사람이 직접 연 세션(human)과 모르는 값에는 넘기지 않는다 */
const RELAY_OWNERS = new Set(['orbit-pi', 'studio']);
const ms = (s: string | undefined) => (s ? Date.parse(s) || undefined : undefined);

/** Needs you 줄의 보조 글. 가정 카드(AU-3)는 「가정으로 진행 중」을 맨 앞에 붙인다. 호스트 Needs you는 출처를 question|permission|ops|plugin만 알아서 assumption은 question으로 옮긴다(일은 막히지 않았고, 답하면 이어진 단계만 다시 열린다). */
function metaOf(d: Decision): string | undefined {
  const host = d.work?.host;
  if (d.source !== 'assumption') return host;
  const head = pick(workText).assumption.rung.assume;
  return host ? `${head} · ${host}` : head;
}

export function toStudioCard(d: Decision): StudioCard {
  const links = d.links.flatMap(l => {
    const url = l.ref && /^https?:\/\//.test(l.ref) ? l.ref : undefined;
    const label = l.label ?? l.ref;
    return label ? [{kind: l.kind, label, ...(url ? {url} : {})}] : [];
  });
  const meta = metaOf(d);
  return {
    id: d.id, state: d.state, source: d.source === 'assumption' ? 'question' : d.source, relay: RELAY_OWNERS.has(d.owner),
    createdAt: ms(d.createdAt) ?? 0, ...(ms(d.updatedAt) ? {updatedAt: ms(d.updatedAt)} : {}),
    questions: d.questions, ...(d.context ? {context: d.context} : {}), ...(links.length ? {links} : {}),
    ...(d.work?.workId ? {workItemId: d.work.workId} : {}), ...(meta ? {meta} : {}),
    ...(d.answer && ms(d.answer.at) ? {answer: {answers: d.answer.answers, by: d.answer.by, at: ms(d.answer.at)!}} : {}),
    ...(ms(d.closedAt) ? {closedAt: ms(d.closedAt)} : {}), ...(d.closeReason ? {closeReason: d.closeReason} : {}),
  };
}

function outcome(o: AnswerOutcome): StudioOutcome {
  const card = o.decision ? toStudioCard(o.decision) : undefined;
  if (o.ok) return {ok: true, ...(card ? {card} : {})};
  const code = o.code === 'CONFLICT' || o.code === 'CANNOT_RELAY' ? o.code : 'FAILED';
  return {ok: false, code, ...(o.message ? {message: o.message} : {}), ...(card ? {card} : {})};
}

/** Studio에 결정 공급자를 등록한다. ctx.decisions가 없는 옛 Studio에서는 아무것도 하지 않는다. 돌려받은 함수로 뺀다. */
export function registerDecisionProvider(ctx: {decisions?: {provide(provider: DecisionProviderLike): () => void}}, store: DecisionStore, source: DecisionSource): () => void {
  const decisions = ctx.decisions;
  if (!decisions || typeof decisions.provide !== 'function') return () => {};
  return decisions.provide({
    id: 'orbit',
    async list({since}) {
      const state = store.getState();
      if ((state.status === 'paused' || state.status === 'error') && !state.open.length && !state.closed.length) throw new Error(state.error ?? 'orbit-code decision cards could not be read');
      const closed = state.closed.filter(d => (ms(d.answer?.at) ?? ms(d.closedAt) ?? ms(d.updatedAt) ?? 0) >= since);
      return [...state.open, ...closed].map(toStudioCard);
    },
    subscribe: onChange => store.subscribe(onChange),
    async answer(id, answer, signal) {
      const out = outcome(await source.answer(id, answer.answers, signal));
      void store.refresh();
      return out;
    },
  });
}

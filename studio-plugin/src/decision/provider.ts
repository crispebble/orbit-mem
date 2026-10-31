// Orbit Studio Needs you에 주는 결정 공급자(플러그인 API 0.1.26 `ctx.decisions.provide`, Studio 4차 S4-3).
// 저장소의 카드를 Studio가 아는 모양으로만 옮긴다. 옛 Studio(ctx.decisions 없음)에서는 등록하지 않는다.
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

export function toStudioCard(d: Decision): StudioCard {
  const links = d.links.flatMap(l => {
    const url = l.ref && /^https?:\/\//.test(l.ref) ? l.ref : undefined;
    const label = l.label ?? l.ref;
    return label ? [{kind: l.kind, label, ...(url ? {url} : {})}] : [];
  });
  return {
    id: d.id, state: d.state, source: d.source, relay: RELAY_OWNERS.has(d.owner),
    createdAt: ms(d.createdAt) ?? 0, ...(ms(d.updatedAt) ? {updatedAt: ms(d.updatedAt)} : {}),
    questions: d.questions, ...(d.context ? {context: d.context} : {}), ...(links.length ? {links} : {}),
    ...(d.work?.workId ? {workItemId: d.work.workId} : {}), ...(d.work?.host ? {meta: d.work.host} : {}),
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
      if ((state.status === 'paused' || state.status === 'error') && !state.open.length && !state.closed.length) throw new Error(state.error ?? 'OpenOrbit decision cards could not be read');
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

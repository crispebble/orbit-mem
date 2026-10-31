// 지금의 카드 출처: 이 플러그인의 읽는 쪽 연결(`orb mcp --reader`)의 decision_* 도구.
// R4-4 orbit-pi 데몬이 생기면 데몬 이벤트 구독으로 같은 DecisionSource를 만들어 index.tsx에서 바꿔 끼운다.
import {errorMessage} from '../orb.ts';
import {readerCall, type CallOptions} from '../work/client.ts';
import {WorkToolError} from '../work/contract.ts';
import type {WorkCtx} from '../work/types.ts';
import {decisionError, parseDecision, parseDecisionChanges, parseDecisionPage} from './contract.ts';
import type {DecisionSource} from './types.ts';

/** 계약의 limitMax. 열린 카드와 하루치 닫힌 카드를 한 번에 받는다 */
export const LIST_LIMIT = 200;
/** 호출 시간 한도 = wait + 이 값(work_changes와 같다) */
const TIMEOUT_GRACE_MS = 10_000;

function throwIfError(out: unknown): void {
  const e = decisionError(out);
  if (e) throw new WorkToolError(e.code, e.message ?? e.code);
}

export function orbDecisionSource(ctx: WorkCtx): DecisionSource {
  const call = (tool: string, args: Record<string, unknown>, options?: CallOptions) => readerCall(ctx, tool, args, options);
  return {
    async list(q, signal) {
      const args: Record<string, unknown> = {state: q.state, limit: LIST_LIMIT};
      if (q.updatedSince) args.updatedSince = q.updatedSince;
      const out = await call('decision_list', args, signal ? {signal} : undefined);
      throwIfError(out);
      return parseDecisionPage(out);
    },
    async changes(q, signal) {
      const args: Record<string, unknown> = {wait: q.wait};
      if (q.after) args.after = q.after;
      const out = await call('decision_changes', args, {signal, timeoutMs: q.wait + TIMEOUT_GRACE_MS});
      throwIfError(out);
      return parseDecisionChanges(out);
    },
    async answer(id, answers, signal) {
      let out: unknown;
      try {
        out = await call('decision_answer', {id, answers, by: 'studio'}, signal ? {signal} : undefined);
      } catch (e) {
        return {ok: false, code: 'FAILED', message: errorMessage(e)};
      }
      const e = decisionError(out);
      if (!e) {
        const decision = parseDecision((out as {decision?: unknown}).decision);
        return {ok: true, ...(decision ? {decision} : {})};
      }
      const code = e.code === 'CONFLICT' || e.code === 'CANNOT_RELAY' || e.code === 'NOT_FOUND' ? e.code : 'FAILED';
      return {ok: false, code, ...(e.message ? {message: e.message} : {}), ...(e.decision ? {decision: e.decision} : {})};
    },
  };
}

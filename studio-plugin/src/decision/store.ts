// 결정 카드 저장소: 열린 카드와 오늘 닫힌 카드를 들고, 출처의 변경 기다리기로 계속 따라간다.
// 출처(DecisionSource)만 알므로 orb 연결이든 데몬 구독이든 그대로 쓴다.
import {errorMessage} from '../orb.ts';
import {WorkPaused} from '../work/contract.ts';
import {abortableSleep, NO_WAIT_POLL_MS, RETRY_MS} from '../work/watch.ts';
import type {Decision, DecisionSource} from './types.ts';

export interface DecisionState {
  status: 'idle' | 'loading' | 'ready' | 'error' | 'paused';
  open: Decision[];
  /** 오늘(로컬 자정 뒤) 답하거나 거두거나 만료된 카드 */
  closed: Decision[];
  error?: string;
}

const CLOSED_STATES = ['answered', 'withdrawn', 'expired'];
const startOfDay = (now: number) => {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};

export interface DecisionStoreOptions {
  now?: () => number;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  log?: {warn(message: string): void};
}

export function createDecisionStore(source: DecisionSource, options: DecisionStoreOptions = {}) {
  const now = options.now ?? Date.now;
  const sleep = options.sleep ?? abortableSleep;
  let state: DecisionState = {status: 'idle', open: [], closed: []};
  const listeners = new Set<() => void>();
  const ctrl = new AbortController();
  let seq: string | undefined;
  let inflight: Promise<void> | undefined;

  const set = (next: Partial<DecisionState>) => {
    state = {...state, ...next};
    for (const l of listeners) l();
  };

  async function load(): Promise<void> {
    if (state.status === 'idle') set({status: 'loading'});
    try {
      const since = new Date(startOfDay(now())).toISOString();
      const [open, closed] = await Promise.all([
        source.list({state: ['open']}, ctrl.signal),
        source.list({state: CLOSED_STATES, updatedSince: since}, ctrl.signal),
      ]);
      seq ??= open.seq || closed.seq || undefined;
      set({status: 'ready', open: open.items, closed: closed.items, error: undefined});
    } catch (e) {
      if (e instanceof WorkPaused) {
        set({status: 'paused', error: e.message});
        throw e;
      }
      options.log?.warn(`Could not read decision cards: ${errorMessage(e)}`);
      set({status: 'error', error: errorMessage(e)});
    }
  }

  function refresh(): Promise<void> {
    inflight ??= load().catch(() => {}).finally(() => { inflight = undefined; });
    return inflight;
  }

  /** 변경 기다리기 루프. 바뀐 카드가 있으면 목록을 다시 읽는다(열린 카드는 많아야 수십 개다). */
  async function watch(): Promise<void> {
    let waitMax = 0;
    while (!ctrl.signal.aborted && state.status !== 'paused') {
      try {
        const res = await source.changes({...(seq ? {after: seq} : {}), wait: waitMax}, ctrl.signal);
        if (ctrl.signal.aborted) return;
        waitMax = res.waitMax;
        const changed = res.reset || res.decisionIds.length > 0;
        seq = res.seq;
        if (changed) await refresh();
        if (!res.more && !changed && waitMax === 0) await sleep(NO_WAIT_POLL_MS, ctrl.signal);
      } catch (e) {
        if (ctrl.signal.aborted) return;
        if (e instanceof WorkPaused) {
          set({status: 'paused', error: e.message});
          return;
        }
        await sleep(RETRY_MS, ctrl.signal);
      }
    }
  }

  return {
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
    /** 처음 읽고 변경 기다리기를 건다. 읽기가 실패해도 끝난다(이유는 state.error) */
    async start(): Promise<void> {
      await refresh();
      void watch();
    },
    refresh,
    /** 열린 카드가 묶인 작업·세션(홈 지도의 「답 기다림」) */
    openRefs() {
      const workIds = new Set<string>();
      const sessionIds = new Set<string>();
      for (const d of state.open) {
        // 가정 카드(AU-3)는 일을 막지 않는다: 지도의 「답 기다림」에 세지 않는다(목록에는 그대로 있다)
        if (d.source === 'assumption') continue;
        if (d.work?.workId) workIds.add(d.work.workId);
        if (d.work?.sessionId) sessionIds.add(d.work.sessionId);
      }
      return {workIds, sessionIds};
    },
    dispose(): void {
      ctrl.abort();
      listeners.clear();
    },
  };
}

export type DecisionStore = ReturnType<typeof createDecisionStore>;

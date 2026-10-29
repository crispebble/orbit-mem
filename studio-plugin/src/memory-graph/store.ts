// 기억 그래프 탭의 데이터 상태: memory_graph 읽기·다시 읽기·숨김 포함. React에 기대지 않는다(화면은 subscribe/getState로 읽는다).
import {errorMessage} from '../oni.ts';
import {PAUSE_MESSAGE, WorkPaused, WorkToolError, type PauseKind} from '../work/contract.ts';
import type {WorkCtx} from '../work/types.ts';
import {memoryGraph} from './client.ts';
import type {MemoryGraph} from './contract.ts';

export interface GraphState {
  /** loading: 첫 읽기 중, ready: 그래프 있음, error: 그래프 없이 실패, paused: 판이 맞지 않음 */
  status: 'loading' | 'ready' | 'error' | 'paused';
  graph?: MemoryGraph;
  includeHidden: boolean;
  /** 다시 읽는 중(그래프는 그대로 보인다) */
  reloading: boolean;
  error?: string;
  /** 계약 오류 코드(UNAVAILABLE 등) */
  code?: string;
  pause?: {kind: PauseKind; message: string};
  /** 그래프가 있는데 다시 읽기가 실패했다. 그래프는 앞의 것이다 */
  staleError?: string;
}

export function createGraphStore(ctx: WorkCtx & {log: {error(m: string): void}}) {
  let state: GraphState = {status: 'loading', includeHidden: false, reloading: false};
  const listeners = new Set<() => void>();
  let seq = 0;
  let disposed = false;

  const set = (next: Partial<GraphState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  async function load(): Promise<void> {
    const mine = ++seq;
    const includeHidden = state.includeHidden;
    if (state.graph) set({reloading: true});
    else set({status: 'loading', error: undefined, code: undefined, pause: undefined});
    try {
      const graph = await memoryGraph(ctx, {includeHidden});
      if (mine !== seq || disposed) return; // 더 새 요청이 있다
      set({status: 'ready', graph, reloading: false, error: undefined, code: undefined, pause: undefined, staleError: undefined});
    } catch (e) {
      if (mine !== seq || disposed) return;
      if (e instanceof WorkPaused) {
        set({status: 'paused', graph: undefined, reloading: false, pause: {kind: e.kind, message: PAUSE_MESSAGE[e.kind]}});
        return;
      }
      const message = errorMessage(e);
      ctx.log.error(`기억 그래프를 읽지 못했습니다: ${message}`);
      const code = e instanceof WorkToolError ? e.code : undefined;
      if (state.graph) set({reloading: false, staleError: message, code});
      else set({status: 'error', reloading: false, error: message, code});
    }
  }

  return {
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
    load,
    /** 숨김 포함을 바꾸면 그 상태로 다시 읽는다 */
    setIncludeHidden(includeHidden: boolean): Promise<void> {
      if (includeHidden === state.includeHidden) return Promise.resolve();
      set({includeHidden});
      return load();
    },
    dispose() {
      disposed = true;
      listeners.clear();
    },
  };
}

export type GraphStore = ReturnType<typeof createGraphStore>;

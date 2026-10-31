// 기억 그래프 탭의 데이터 상태: memory_graph 읽기·다시 읽기·숨김 포함. React에 기대지 않는다(화면은 subscribe/getState로 읽는다).
import {errorMessage} from '../orb.ts';
import {pauseMessage, WorkPaused, WorkToolError, type PauseKind} from '../work/contract.ts';
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
  /** 지금 그래프를 읽을 때의 숨김 포함 값. 다시 읽기가 실패하면 스위치를 이 값으로 되돌린다 */
  let graphHidden = false;
  /** 같은 오류를 주기 갱신마다 되풀이해 적지 않는다 */
  let lastLogged: string | undefined;

  const set = (next: Partial<GraphState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  /** quiet: 그래프가 없을 때(오류) 「불러오는 중」으로 바꾸지 않고 조용히 다시 읽는다(주기 갱신) */
  async function load(opts: {quiet?: boolean} = {}): Promise<void> {
    const mine = ++seq;
    const includeHidden = state.includeHidden;
    if (state.graph) set({reloading: true});
    else if (!opts.quiet) set({status: 'loading', error: undefined, code: undefined, pause: undefined});
    try {
      const graph = await memoryGraph(ctx, {includeHidden});
      if (mine !== seq) return; // 더 새 요청이 있거나 탭이 닫혔다
      graphHidden = includeHidden;
      lastLogged = undefined;
      set({status: 'ready', graph, reloading: false, error: undefined, code: undefined, pause: undefined, staleError: undefined});
    } catch (e) {
      if (mine !== seq) return;
      if (e instanceof WorkPaused) {
        set({status: 'paused', graph: undefined, reloading: false, pause: {kind: e.kind, message: pauseMessage(e.kind)}});
        return;
      }
      const message = errorMessage(e);
      if (message !== lastLogged) ctx.log.error(`Could not load the memory graph: ${message}`);
      lastLogged = message;
      const code = e instanceof WorkToolError ? e.code : undefined;
      if (state.graph) set({reloading: false, staleError: message, code, includeHidden: graphHidden});
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
    /** 진행 중인 읽기의 응답을 버린다. 다시 load하면 다시 쓸 수 있다(개발 모드의 다시 마운트) */
    dispose() {
      seq++;
      listeners.clear();
    },
  };
}

export type GraphStore = ReturnType<typeof createGraphStore>;

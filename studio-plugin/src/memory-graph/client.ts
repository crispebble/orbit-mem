// 기억 연결(`mem` = `orbit-mem mcp --reader`)로 memory_graph를 부른다. 판·오류 처리는 작업 계약과 같은 경계(memCall)를 쓴다.
import {OrbitError} from '../orb.ts';
import {memCall, type CallOptions} from '../work/client.ts';
import {WorkPaused} from '../work/contract.ts';
import type {WorkCtx} from '../work/types.ts';
import {GRAPH_TOOL, parseMemoryGraph, type MemoryGraph} from './contract.ts';

export const DEFAULT_STATES = ['candidate', 'confirmed', 'stale'] as const;

export interface GraphParams {
  /** 숨김 기억도 받는다 */
  includeHidden?: boolean;
}

/** memory_graph가 없는 옛 판은 MCP가 「unknown tool」 오류로 답한다 */
const UNKNOWN_TOOL = /unknown tool/i;

export async function memoryGraph(ctx: WorkCtx, p: GraphParams = {}, options?: CallOptions): Promise<MemoryGraph> {
  const args: Record<string, unknown> = p.includeHidden ? {states: [...DEFAULT_STATES, 'hidden']} : {};
  try {
    return parseMemoryGraph(await memCall(ctx, GRAPH_TOOL, args, options));
  } catch (e) {
    if (e instanceof OrbitError && UNKNOWN_TOOL.test(e.message)) throw new WorkPaused('orbit-older', 'orbit-mem');
    // 판 검사는 작업 계약과 같은 코드다: 기억 그래프를 주는 쪽은 orbit-mem이라 그 이름으로 다시 던진다.
    if (e instanceof WorkPaused && e.source !== 'orbit-mem') throw new WorkPaused(e.kind, 'orbit-mem');
    throw e;
  }
}

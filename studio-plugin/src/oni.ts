// oni MCP 호출 경계: 연결 실패·도구 오류·응답 모양 오류를 한 가지 오류(OrbitError)로 모은다.
import type {PluginContext} from '@onidot-labs/studio-plugin';
import {pick} from './i18n/index.ts';
import {commonText} from './i18n/messages/common.ts';

export const CONNECTION_ID = 'oni';

export class OrbitError extends Error {
  /** 판본이 어긋났다(다시 읽고 고쳐야 한다) */
  conflict = false;
  /** oni에 닿지 못했다 */
  connection = false;
  constructor(message: string, flags: {conflict?: boolean; connection?: boolean} = {}) {
    super(message);
    this.name = 'OrbitError';
    this.conflict = flags.conflict ?? false;
    this.connection = flags.connection ?? false;
  }
}

/** 어떤 오류든 사용자에게 보일 글로 */
export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// oni가 판본이 어긋났을 때 돌려주는 글(한국어)을 알아본다
const CONFLICT = /다른 곳에서 먼저 고쳤|다시 읽고/;

export async function callOni<T>(ctx: PluginContext, tool: string, args?: Record<string, unknown>): Promise<T> {
  let result;
  const t = pick(commonText).oni;
  try {
    result = await ctx.mcp.call<T>(CONNECTION_ID, tool, args);
  } catch (e) {
    throw new OrbitError(t.connectFailed(errorMessage(e)), {connection: true});
  }
  if (result.isError) {
    const text = result.text.trim() || t.callFailed(tool);
    throw new OrbitError(text, {conflict: CONFLICT.test(text)});
  }
  if (result.structured === undefined || result.structured === null) {
    throw new OrbitError(t.noStructured(tool));
  }
  return result.structured;
}

/** 응답의 목록 칸을 확인한다. 모양이 다르면 오류. */
export function itemsOf<T>(out: unknown, tool: string): T[] {
  const items = (out as {items?: unknown} | null)?.items;
  if (!Array.isArray(items)) throw new OrbitError(pick(commonText).oni.badShape(tool));
  return items as T[];
}

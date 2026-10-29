// 읽는 쪽 연결(`work`)로 작업 도구를 부르고 응답을 검사해 돌려준다. oni 호출 경계 하나.
import {errorMessage, OrbitError} from '../oni.ts';
import {
  checkContract, parseChanges, parseGet, parseList, parseSession, parseSessions, WorkPaused, WorkToolError,
} from './contract.ts';
import type {
  ChangesResult, GetResult, ListPage, SessionPage, SessionsPage, StudioScope, WorkCtx,
} from './types.ts';

export const WORK_CONNECTION_ID = 'work';
export const READ_MAJOR_ARG = 1;

export interface CallOptions {
  timeoutMs?: number;
  signal?: AbortSignal;
}

/** 도구 하나를 부르고 계약 판을 확인한 응답 본문을 돌려준다. 연결 실패는 OrbitError(connection), 판 문제는 WorkPaused. */
async function call(ctx: WorkCtx, tool: string, args: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
  let result;
  try {
    result = await ctx.mcp.call<unknown>(WORK_CONNECTION_ID, tool, {...args, major: READ_MAJOR_ARG}, options);
  } catch (e) {
    throw new OrbitError(`oni에 연결하지 못했습니다: ${errorMessage(e)}`, {connection: true});
  }
  const out = result.structured;
  if (out === undefined || out === null) {
    if (result.isError) throw new WorkToolError('UNKNOWN', result.text.trim() || `${tool} 호출이 실패했습니다.`);
    throw new OrbitError(`oni의 ${tool} 응답에 구조화 결과가 없습니다. Orbit을 올려 주세요.`);
  }
  checkContract(out, tool);
  return out;
}

/** 범위가 프로젝트면 디렉토리들로 거른다(계약 `cwdPrefix`). 디렉토리가 없는 프로젝트는 빈 목록이 맞다. */
export function scopeArgs(scope: StudioScope): Record<string, unknown> {
  return scope.kind === 'project' ? {cwdPrefix: scope.directories} : {};
}

export interface ListParams {
  scope: StudioScope;
  state?: 'open' | 'done' | 'any';
  cursor?: string;
  limit?: number;
}

export async function workList(ctx: WorkCtx, p: ListParams): Promise<ListPage> {
  const args: Record<string, unknown> = {...scopeArgs(p.scope), state: p.state ?? 'any'};
  if (p.cursor) args.cursor = p.cursor;
  if (p.limit) args.limit = p.limit;
  return parseList(await call(ctx, 'work_list', args));
}

export async function workGet(ctx: WorkCtx, workId: string, includeChildren = false): Promise<GetResult> {
  return parseGet(await call(ctx, 'work_get', {workId, includeChildren}));
}

export interface SessionsParams {
  scope: StudioScope;
  state?: string[];
  cursor?: string;
  limit?: number;
}

export async function sessionsList(ctx: WorkCtx, p: SessionsParams): Promise<SessionsPage> {
  const args: Record<string, unknown> = {...scopeArgs(p.scope)};
  if (p.state?.length) args.state = p.state;
  if (p.cursor) args.cursor = p.cursor;
  if (p.limit) args.limit = p.limit;
  return parseSessions(await call(ctx, 'work_sessions', args));
}

/** 한 세션의 사건 쪽(첫 쪽에 runs·evidence가 모두 있다) */
export async function sessionGet(ctx: WorkCtx, sessionId: string, cursor?: string, limit?: number): Promise<SessionPage> {
  const args: Record<string, unknown> = {sessionId};
  if (cursor) args.cursor = cursor;
  if (limit) args.limit = limit;
  return parseSession(await call(ctx, 'work_sessions', args));
}

export interface ChangesParams {
  after?: string;
  wait?: number;
}

export async function workChanges(ctx: WorkCtx, p: ChangesParams, options?: CallOptions): Promise<ChangesResult> {
  const args: Record<string, unknown> = {};
  if (p.after) args.after = p.after;
  if (p.wait !== undefined) args.wait = p.wait;
  return parseChanges(await call(ctx, 'work_changes', args, options));
}

export {WorkPaused, WorkToolError};

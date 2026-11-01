// 작업 연결(`work` = `orb work mcp`)로 작업 도구를 부르고 응답을 검사해 돌려준다. orb 호출 경계 하나.
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import {errorMessage, OrbitError} from '../orb.ts';
import {
  checkContract, parseChanges, parseGet, parseList, parseSession, parseSessions, WorkPaused, WorkToolError,
} from './contract.ts';
import type {
  ChangesResult, GetResult, ListPage, SessionPage, SessionsPage, StudioScope, WorkCtx,
} from './types.ts';

export const WORK_CONNECTION_ID = 'work';
/** 기억 그래프 연결(`mem` = `orb mcp --reader`). memory_graph만 낸다. */
export const MEM_CONNECTION_ID = 'mem';
export const READ_MAJOR_ARG = 1;

export interface CallOptions {
  timeoutMs?: number;
  signal?: AbortSignal;
}

/** 도구 하나를 `connectionId` 연결로 부르고 계약 판을 확인한 응답 본문을 돌려준다. 연결 실패는 OrbitError(connection), 판 문제는 WorkPaused. */
async function callOn(connectionId: string, ctx: WorkCtx, tool: string, args: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
  let result;
  try {
    result = await ctx.mcp.call<unknown>(connectionId, tool, {...args, major: READ_MAJOR_ARG}, options);
  } catch (e) {
    throw new OrbitError(pick(workText).error.connect(errorMessage(e)), {connection: true});
  }
  const out = result.structured;
  if (out === undefined || out === null) {
    if (result.isError) throw new WorkToolError('UNKNOWN', result.text.trim() || pick(workText).error.callFailed(tool));
    throw new OrbitError(pick(workText).error.noStructured(tool));
  }
  checkContract(out, tool);
  return out;
}

const call = (ctx: WorkCtx, tool: string, args: Record<string, unknown>, options?: CallOptions) => callOn(WORK_CONNECTION_ID, ctx, tool, args, options);

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

/** 작업 연결의 다른 계약 도구(decision_*)도 같은 경계로 부른다 */
export {call as readerCall, WorkPaused, WorkToolError};

/** 기억 연결(`mem`)의 memory_graph도 같은 판·오류 경계로 부른다 */
export const memCall = (ctx: WorkCtx, tool: string, args: Record<string, unknown>, options?: CallOptions) => callOn(MEM_CONNECTION_ID, ctx, tool, args, options);

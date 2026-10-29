// 통합 시험: 임시 ORBIT_HOME과 실제 `oni mcp --reader`(stdio MCP)로 계약 호출을 확인한다.
// oni 실행 파일은 ONI_BIN(없으면 PATH의 oni). 없거나 --reader를 모르면 건너뛴다.
import assert from 'node:assert/strict';
import {spawn, spawnSync, type ChildProcessWithoutNullStreams} from 'node:child_process';
import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {after, describe, it} from 'node:test';
import {sessionGet, sessionsList, workChanges, workGet, workList} from '../src/work/client.ts';
import {WorkToolError} from '../src/work/contract.ts';
import type {McpResultLike, WorkCtx} from '../src/work/types.ts';

const ONI = process.env.ONI_BIN ?? 'oni';
const help = spawnSync(ONI, ['mcp', '--help'], {encoding: 'utf8'});
const available = !help.error && `${help.stdout}${help.stderr}`.includes('-reader');

/** 최소 MCP stdio 클라이언트(JSON 줄 단위) */
function startReader(home: string) {
  const child: ChildProcessWithoutNullStreams = spawn(ONI, ['mcp', '--reader'], {env: {...process.env, ORBIT_HOME: home}, cwd: home});
  let id = 0;
  const pending = new Map<number, (v: {result?: Record<string, unknown>; error?: {message: string}}) => void>();
  let buf = '';
  child.stdout.on('data', (d: Buffer) => {
    buf += d.toString();
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      const msg = JSON.parse(line) as {id?: number; result?: Record<string, unknown>; error?: {message: string}};
      if (msg.id !== undefined) pending.get(msg.id)?.(msg);
    }
  });
  const rpc = (method: string, params: unknown) => new Promise<{result?: Record<string, unknown>; error?: {message: string}}>(resolve => {
    const n = ++id;
    pending.set(n, resolve);
    child.stdin.write(`${JSON.stringify({jsonrpc: '2.0', id: n, method, params})}\n`);
  });
  const ready = (async () => {
    await rpc('initialize', {protocolVersion: '2025-06-18', capabilities: {}, clientInfo: {name: 'orbit-plugin-test', version: '0'}});
    child.stdin.write(`${JSON.stringify({jsonrpc: '2.0', method: 'notifications/initialized'})}\n`);
  })();
  const ctx = {
    mcp: {
      async call(_c: string, tool: string, args?: Record<string, unknown>): Promise<McpResultLike> {
        await ready;
        const r = await rpc('tools/call', {name: tool, arguments: args ?? {}});
        if (r.error) throw new Error(r.error.message);
        const res = r.result as {isError?: boolean; structuredContent?: unknown; content?: {text?: string}[]};
        return {isError: res.isError === true, text: res.content?.[0]?.text ?? '', structured: res.structuredContent};
      },
    },
  } as unknown as WorkCtx;
  return {ctx, stop: () => child.kill()};
}

describe('실제 oni mcp --reader', {skip: available ? false : 'oni 실행 파일이 없거나 --reader를 모른다(ONI_BIN으로 지정)'}, () => {
  const home = mkdtempSync(join(tmpdir(), 'orbit-home-'));
  const reader = startReader(home);
  after(() => {
    reader.stop();
    rmSync(home, {recursive: true, force: true});
  });

  it('빈 기록에서 목록·세션 쪽이 계약대로 비어 온다', async () => {
    const list = await workList(reader.ctx, {scope: {kind: 'all'}});
    assert.deepEqual([list.items, list.nextCursor], [[], undefined]);
    assert.ok(list.seq && list.limitMax >= 1);
    const sessions = await sessionsList(reader.ctx, {scope: {kind: 'all'}});
    assert.deepEqual(sessions.sessions, []);
  });

  it('없는 작업·세션은 NOT_FOUND', async () => {
    await assert.rejects(workGet(reader.ctx, 'w-없음'), (e: unknown) => e instanceof WorkToolError && e.code === 'NOT_FOUND');
    await assert.rejects(sessionGet(reader.ctx, 's-없음'), (e: unknown) => e instanceof WorkToolError && e.code === 'NOT_FOUND');
  });

  it('프로젝트 범위(cwdPrefix)와 work_changes: after 없으면 reset, 그 seq로 짧게 기다리면 빈 응답', async () => {
    const scoped = await workList(reader.ctx, {scope: {kind: 'project', id: 'p', name: 'P', directories: ['/nowhere']}});
    assert.deepEqual(scoped.items, []);
    const first = await workChanges(reader.ctx, {});
    assert.equal(first.reset, true);
    assert.ok(first.waitMax > 0);
    const t0 = Date.now();
    const idle = await workChanges(reader.ctx, {after: first.seq, wait: 300});
    assert.equal(idle.reset, false);
    assert.deepEqual([idle.workIds, idle.sessionIds, idle.more], [[], [], false]);
    assert.ok(Date.now() - t0 >= 250, '바뀐 것이 없으면 wait만큼 기다린다');
  });
});

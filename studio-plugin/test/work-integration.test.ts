// 통합 시험: 임시 ORBIT_HOME과 실제 `orb work mcp`(stdio MCP)로 계약 호출을 확인한다.
// orb 실행 파일은 ORB_BIN(없으면 PATH의 orb). 없거나 `work mcp`를 모르면 건너뛴다.
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {after, describe, it} from 'node:test';
import {sessionGet, sessionsList, workChanges, workGet, workList} from '../src/work/client.ts';
import {WorkToolError} from '../src/work/contract.ts';
import {startMcp} from './mcp-stdio.ts';

const ORB = process.env.ORB_BIN ?? 'orb';
const probe = spawnSync(ORB, ['work', 'mcp'], {
  encoding: 'utf8', timeout: 10_000, env: {...process.env, HOME: tmpdir(), ORBIT_HOME: tmpdir()},
  input: `${JSON.stringify({jsonrpc: '2.0', id: 1, method: 'initialize', params: {protocolVersion: '2025-06-18', capabilities: {}, clientInfo: {name: 'probe', version: '0'}}})}\n`,
});
const available = !probe.error && probe.stdout.includes('orbit-work');

describe('실제 orb work mcp', {skip: available ? false : 'orb 실행 파일이 없거나 work mcp를 모른다(ORB_BIN으로 지정)'}, () => {
  const home = mkdtempSync(join(tmpdir(), 'orbit-home-'));
  const reader = startMcp(ORB, ['work', 'mcp'], home);
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

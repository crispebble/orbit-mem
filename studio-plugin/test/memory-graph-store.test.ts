// 기억 그래프 탭의 데이터 층: memory_graph 호출·판·오류·다시 읽기와 탭 열기. 「Studio 보기 › 상태·오류」·「자리」에 해당한다.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {describe, it} from 'node:test';
import {memoryGraph} from '../src/memory-graph/client.ts';
import {GRAPH_TAB, onSelectRequest, openMemoryGraph, takePendingSelect} from '../src/memory-graph/open.ts';
import {createGraphStore} from '../src/memory-graph/store.ts';
import {WorkPaused} from '../src/work/contract.ts';
import type {McpResultLike} from '../src/work/types.ts';
import {fakeWorkCtx, until} from './work-fake.ts';

const example = (name: string) => JSON.parse(readFileSync(new URL(`./memory-examples/${name}.json`, import.meta.url), 'utf8')) as {result: Record<string, unknown>; isError?: boolean};
const ok = (name: string): McpResultLike => ({isError: false, text: '', structured: example(name).result});
const err = (name: string): McpResultLike => ({isError: true, text: '', structured: example(name).result});

describe('memory_graph 호출', () => {
  it('읽는 쪽 연결로 major 1을 붙여 부르고, 숨김 포함이면 states에 hidden을 더한다', async () => {
    const f = fakeWorkCtx({memory_graph: () => ok('memory_graph.all')});
    const g = await memoryGraph(f.ctx);
    assert.equal(g.nodes.length, 8);
    await memoryGraph(f.ctx, {includeHidden: true});
    assert.deepEqual(f.calls.map(c => c.args), [{major: 1}, {states: ['candidate', 'confirmed', 'stale', 'hidden'], major: 1}]);
  });

  it('memory_graph가 없는 oni(0.3.5 이하)는 「Orbit을 올려 주세요」 멈춤', async () => {
    const f = fakeWorkCtx({memory_graph: () => { throw new Error('MCP error -32602: unknown tool "memory_graph"'); }});
    await assert.rejects(memoryGraph(f.ctx), (e: unknown) => e instanceof WorkPaused && e.kind === 'orbit-older');
  });
});

describe('그래프 상태', () => {
  it('처음 읽기: loading → ready', async () => {
    const f = fakeWorkCtx({memory_graph: () => ok('memory_graph.all')});
    const s = createGraphStore(f.ctx);
    assert.equal(s.getState().status, 'loading');
    await s.load();
    assert.equal(s.getState().status, 'ready');
    assert.equal(s.getState().graph?.nodes.length, 8);
  });

  it('UNAVAILABLE이면 오류와 코드, 로그를 남긴다', async () => {
    const f = fakeWorkCtx({memory_graph: () => err('error.unavailable')});
    const s = createGraphStore(f.ctx);
    await s.load();
    const st = s.getState();
    assert.deepEqual([st.status, st.code], ['error', 'UNAVAILABLE']);
    assert.match(st.error ?? '', /database is locked/);
    assert.equal(f.logs.filter(l => l.level === 'error').length, 1);
  });

  it('연결 실패도 오류(다시 시도할 수 있다)', async () => {
    const f = fakeWorkCtx({});
    const s = createGraphStore(f.ctx);
    await s.load();
    assert.equal(s.getState().status, 'error');
    assert.match(s.getState().error ?? '', /oni에 연결하지 못했습니다/);
  });

  it('Orbit이 새 판이면 멈춤과 문구', async () => {
    const newer = {contract: {name: 'orbit.memory', major: 2, minor: 0, supportedMajors: [2]}, error: {code: 'UNSUPPORTED_MAJOR', message: 'x', supportedMajors: [2]}};
    const f = fakeWorkCtx({memory_graph: () => ({isError: true, text: '', structured: newer})});
    const s = createGraphStore(f.ctx);
    await s.load();
    assert.equal(s.getState().status, 'paused');
    assert.match(s.getState().pause?.message ?? '', /Orbit 앱을 올려 주세요/);
  });

  it('다시 읽기가 실패해도 앞 그래프를 두고 알린다', async () => {
    let fail = false;
    const f = fakeWorkCtx({memory_graph: () => (fail ? err('error.unavailable') : ok('memory_graph.all'))});
    const s = createGraphStore(f.ctx);
    await s.load();
    fail = true;
    const p = s.load();
    assert.equal(s.getState().reloading, true);
    await p;
    const st = s.getState();
    assert.deepEqual([st.status, st.graph?.nodes.length, st.reloading], ['ready', 8, false]);
    assert.match(st.staleError ?? '', /database is locked/);
    fail = false;
    await s.load();
    assert.equal(s.getState().staleError, undefined);
  });

  it('숨김 포함을 바꾸면 그 상태로 다시 읽는다. 늦게 온 앞 응답은 버린다', async () => {
    const pending: ((r: McpResultLike) => void)[] = [];
    const f = fakeWorkCtx({memory_graph: () => new Promise<McpResultLike>(r => pending.push(r))});
    const s = createGraphStore(f.ctx);
    const first = s.load();
    const second = s.setIncludeHidden(true);
    await until(() => pending.length === 2, '두 호출');
    pending[1](ok('memory_graph.hidden'));
    await second;
    pending[0](ok('memory_graph.all'));
    await first;
    assert.equal(s.getState().includeHidden, true);
    assert.equal(s.getState().graph?.nodes.length, 2); // hidden 예시
    assert.deepEqual(f.calls[1].args?.states, ['candidate', 'confirmed', 'stale', 'hidden']);
  });

  it('dispose 뒤 온 응답은 상태를 바꾸지 않는다', async () => {
    let resolve: (r: McpResultLike) => void = () => {};
    const f = fakeWorkCtx({memory_graph: () => new Promise<McpResultLike>(r => { resolve = r; })});
    const s = createGraphStore(f.ctx);
    const p = s.load();
    s.dispose();
    resolve(ok('memory_graph.all'));
    await p;
    assert.equal(s.getState().status, 'loading');
  });
});

describe('기억 그래프 탭 열기', () => {
  it('탭은 하나(같은 id)이고, 고를 기억은 남겨 두었다가 탭이 받아 간다', () => {
    const opened: unknown[] = [];
    const ctx = {tabs: {open: (spec: unknown) => opened.push(spec)}} as unknown as Parameters<typeof openMemoryGraph>[0];
    openMemoryGraph(ctx);
    assert.equal(takePendingSelect(), undefined);
    openMemoryGraph(ctx, 'm-1');
    assert.deepEqual(opened, [{...GRAPH_TAB, params: {}}, {...GRAPH_TAB, params: {select: 'm-1'}}]);
    assert.equal(takePendingSelect(), 'm-1');
    assert.equal(takePendingSelect(), undefined);
  });

  it('열린 탭은 이후 요청을 바로 받고, 받은 요청은 남지 않는다', () => {
    const ctx = {tabs: {open: () => {}}} as unknown as Parameters<typeof openMemoryGraph>[0];
    const got: string[] = [];
    const off = onSelectRequest(id => got.push(id));
    openMemoryGraph(ctx, 'm-2');
    off();
    assert.deepEqual(got, ['m-2']);
    assert.equal(takePendingSelect(), undefined);
  });
});

describe('그래프 상태(검토 반영)', () => {
  it('조용히 다시 읽기는 오류 화면을 「불러오는 중」으로 바꾸지 않고, 같은 오류는 한 번만 적는다', async () => {
    const f = fakeWorkCtx({memory_graph: () => err('error.unavailable')});
    const s = createGraphStore(f.ctx);
    await s.load();
    const seen: string[] = [];
    s.subscribe(() => seen.push(s.getState().status));
    await s.load({quiet: true});
    assert.ok(!seen.includes('loading'));
    assert.equal(s.getState().status, 'error');
    assert.equal(f.logs.filter(l => l.level === 'error').length, 1);
  });

  it('숨김 포함으로 다시 읽기가 실패하면 스위치를 앞 그래프의 값으로 되돌린다', async () => {
    let fail = false;
    const f = fakeWorkCtx({memory_graph: () => (fail ? err('error.unavailable') : ok('memory_graph.all'))});
    const s = createGraphStore(f.ctx);
    await s.load();
    fail = true;
    await s.setIncludeHidden(true);
    assert.deepEqual([s.getState().includeHidden, s.getState().graph?.nodes.length], [false, 8]);
    assert.match(s.getState().staleError ?? '', /database is locked/);
  });

  it('dispose 뒤에도 다시 load하면 쓸 수 있다(개발 모드의 다시 마운트)', async () => {
    const f = fakeWorkCtx({memory_graph: () => ok('memory_graph.all')});
    const s = createGraphStore(f.ctx);
    s.dispose();
    await s.load();
    assert.equal(s.getState().status, 'ready');
  });
});

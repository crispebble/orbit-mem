import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {createStore} from '../src/store.ts';
import type {OniProposal} from '../src/model.ts';
import {fail, fakeCtx, ok} from './fake-ctx.ts';

const prop = (id: string, status = 'pending'): OniProposal => ({id, kind: 'confirm', title: `제안 ${id}`, memoryIds: [], payload: {}, status, createdAt: 1_780_000_000_000});

function timers() {
  const t = {fn: undefined as undefined | (() => void), ms: 0, cleared: false};
  return {
    t,
    api: {
      setInterval: (fn: () => void, ms: number) => { t.fn = fn; t.ms = ms; return 1 as unknown as ReturnType<typeof setInterval>; },
      clearInterval: () => { t.cleared = true; },
    },
  };
}

describe('store.refresh', () => {
  it('제안을 불러와 받은 일과 배지를 채운다', async () => {
    const f = fakeCtx({proposals: () => ok({items: [prop('a'), prop('b', 'approved'), prop('c')]})});
    const store = createStore(f.ctx);
    await store.refresh();
    assert.deepEqual(f.calls, [{tool: 'proposals', args: {status: 'all'}}]);
    assert.equal(store.getState().status, 'ready');
    assert.equal(store.getState().proposals.length, 3);
    assert.deepEqual(f.inbox.at(-1)?.map(i => i.id), ['a', 'c']);
    assert.equal(f.badges.at(-1), 2);
  });

  it('대기 제안이 없으면 배지를 지운다', async () => {
    const f = fakeCtx({proposals: () => ok({items: []})});
    const store = createStore(f.ctx);
    await store.refresh();
    assert.equal(f.badges.at(-1), null);
    assert.deepEqual(f.inbox.at(-1), []);
  });

  it('연결 실패는 오류 상태와 이유를 남기고 log.error에 적는다', async () => {
    const f = fakeCtx({});
    const store = createStore(f.ctx);
    await store.refresh();
    const s = store.getState();
    assert.equal(s.status, 'error');
    assert.match(s.error ?? '', /oni에 연결하지 못했습니다/);
    assert.equal(f.logs.filter(l => l.level === 'error').length, 1);
  });

  it('도구 오류 결과는 도구가 준 글을 이유로 쓴다', async () => {
    const f = fakeCtx({proposals: () => fail('저장소가 잠겨 있습니다')});
    const store = createStore(f.ctx);
    await store.refresh();
    assert.equal(store.getState().error, '저장소가 잠겨 있습니다');
  });

  it('실패 뒤 다시 불러오면 회복한다', async () => {
    let n = 0;
    const f = fakeCtx({proposals: () => (n++ === 0 ? fail('일시 오류') : ok({items: [prop('a')]}))});
    const store = createStore(f.ctx);
    await store.refresh();
    await store.refresh();
    assert.equal(store.getState().status, 'ready');
    assert.equal(store.getState().error, undefined);
  });

  it('구독자에게 바뀐 것을 알린다', async () => {
    const f = fakeCtx({proposals: () => ok({items: [prop('a')]})});
    const store = createStore(f.ctx);
    let n = 0;
    const off = store.subscribe(() => n++);
    await store.refresh();
    assert.ok(n >= 2); // 불러오는 중 → 준비
    off();
    const before = n;
    await store.refresh();
    assert.equal(n, before);
  });
});

describe('store.decide', () => {
  it('결정을 보내고 목록을 다시 불러온다', async () => {
    let decided = false;
    const f = fakeCtx({
      proposal_decide: () => { decided = true; return ok({proposal: prop('a', 'approved')}); },
      proposals: () => ok({items: [prop('a', decided ? 'approved' : 'pending')]}),
    });
    const store = createStore(f.ctx);
    await store.refresh();
    await store.decide({id: 'a', decision: 'approve'});
    assert.deepEqual(f.calls.map(c => c.tool), ['proposals', 'proposal_decide', 'proposals']);
    assert.equal(f.badges.at(-1), null);
  });

  it('결정이 실패하면 이유를 던지고 목록을 건드리지 않는다', async () => {
    const f = fakeCtx({proposal_decide: () => fail('이미 결정한 제안입니다'), proposals: () => ok({items: [prop('a')]})});
    const store = createStore(f.ctx);
    await store.refresh();
    await assert.rejects(store.decide({id: 'a', decision: 'reject'}), /이미 결정한 제안입니다/);
    assert.deepEqual(f.calls.map(c => c.tool), ['proposals', 'proposal_decide']);
  });
});

describe('store.start', () => {
  it('처음 불러오고 60초마다 갱신하며 dispose에서 정리한다', async () => {
    const f = fakeCtx({proposals: () => ok({items: [prop('a')]})});
    const tm = timers();
    const store = createStore(f.ctx, tm.api);
    await store.start();
    assert.equal(tm.t.ms, 60_000);
    assert.equal(f.calls.length, 1);
    tm.t.fn?.();
    await new Promise(r => setImmediate(r));
    assert.equal(f.calls.length, 2);
    store.dispose();
    assert.equal(tm.t.cleared, true);
    assert.equal(f.actionDisposed, true);
  });

  it('받은 일의 승인·거절을 처리하고 결과를 알린다', async () => {
    const calls: unknown[] = [];
    const f = fakeCtx({
      proposals: () => ok({items: [prop('a')]}),
      proposal_decide: a => { calls.push(a); return ok({proposal: prop('a', 'approved')}); },
    });
    const store = createStore(f.ctx, timers().api);
    await store.start();
    const item = f.inbox.at(-1)![0];
    await f.fireAction(item, 'approve');
    await f.fireAction(item, 'reject');
    assert.deepEqual(calls, [{id: 'a', decision: 'approve'}, {id: 'a', decision: 'reject'}]);
    assert.equal(f.notices.length, 2);
    assert.equal(f.notices[0].tone, 'success');
  });

  it('받은 일 동작이 실패하면 위험 알림을 낸다', async () => {
    const f = fakeCtx({proposals: () => ok({items: [prop('a')]}), proposal_decide: () => fail('파일이 바뀌었습니다')});
    const store = createStore(f.ctx, timers().api);
    await store.start();
    await f.fireAction(f.inbox.at(-1)![0], 'approve');
    assert.equal(f.notices[0].tone, 'danger');
    assert.match(f.notices[0].body ?? '', /파일이 바뀌었습니다/);
  });

  it('첫 불러오기가 실패해도 start는 던지지 않는다', async () => {
    const f = fakeCtx({});
    const store = createStore(f.ctx, timers().api);
    await store.start();
    assert.equal(store.getState().status, 'error');
  });
});

describe('store 기억 읽기·고치기', () => {
  const m = {id: 'm1', kind: 'rule', scope: 'global', state: 'candidate', title: 't', body: 'b', tags: [], version: 2, createdAt: 1, updatedAt: 2};

  it('findMemories는 인자를 변환해 부른다', async () => {
    const f = fakeCtx({memory_find: () => ok({items: [{id: 'm1', kind: 'rule', state: 'confirmed', scope: 'global', title: 't', version: 1, updatedAt: 1}]})});
    const store = createStore(f.ctx);
    const items = await store.findMemories({query: 'x', kind: 'all', state: 'all'});
    assert.equal(items.length, 1);
    assert.equal(f.calls[0].args?.query, 'x');
  });

  it('getMemory는 이력을 함께 받는다', async () => {
    const f = fakeCtx({memory_get: () => ok({items: [m], history: {m1: [{version: 1, action: 'create', at: 1, memory: m}]}})});
    const store = createStore(f.ctx);
    const r = await store.getMemory('m1');
    assert.equal(r.memory.id, 'm1');
    assert.equal(r.history.length, 1);
    assert.deepEqual(f.calls[0].args, {ids: ['m1'], history: true});
  });

  it('updateMemory는 단계를 차례로 부르고 대체 단계에는 새 판본을 채운다', async () => {
    let v = 2;
    const f = fakeCtx({memory_update: () => { v += 1; return ok({memory: {...m, version: v}}); }});
    const store = createStore(f.ctx);
    const last = await store.updateMemory([
      {id: 'm1', expectedVersion: 2, action: 'edit', title: 'n'},
      {id: 'm1', action: 'supersede', supersededBy: 'm9'},
    ]);
    assert.equal(last.version, 4);
    assert.equal(f.calls[1].args?.expectedVersion, 3);
  });

  it('판본이 어긋나면 다시 읽으라는 안내를 붙여 던진다', async () => {
    const f = fakeCtx({memory_update: () => fail('다른 곳에서 먼저 고쳤습니다. 다시 읽고 고쳐 주세요')});
    const store = createStore(f.ctx);
    await assert.rejects(store.updateMemory([{id: 'm1', expectedVersion: 1, action: 'edit', title: 'n'}]), (e: Error) => {
      assert.match(e.message, /다시 읽고/);
      assert.equal((e as {conflict?: boolean}).conflict, true);
      return true;
    });
  });
});

it('기본 타이머는 this 없이 불러도 동작한다(브라우저 setInterval Illegal invocation 회귀)', async () => {
  const realSet = globalThis.setInterval, realClear = globalThis.clearInterval;
  const calls: string[] = [];
  // 브라우저처럼 this가 globalThis가 아니면 던지는 타이머로 바꿔 끼운다.
  globalThis.setInterval = function (this: unknown, fn: () => void, ms?: number) { if (this !== undefined && this !== globalThis) throw new TypeError('Illegal invocation'); calls.push('set'); return realSet(fn, ms); } as typeof setInterval;
  globalThis.clearInterval = function (this: unknown, id?: ReturnType<typeof setInterval>) { if (this !== undefined && this !== globalThis) throw new TypeError('Illegal invocation'); calls.push('clear'); realClear(id); } as typeof clearInterval;
  try {
    const store = createStore(fakeCtx({proposals: () => ok({items: []})}).ctx);
    await store.start();
    store.dispose();
    assert.deepEqual(calls, ['set', 'clear']);
  } finally { globalThis.setInterval = realSet; globalThis.clearInterval = realClear; }
});

describe('store.getMemories', () => {
  it('함께 부르고, 못 읽은 것은 빼고 순서대로 돌려준다', async () => {
    let inflight = 0, peak = 0;
    const f = fakeCtx({
      memory_get: async args => {
        inflight++; peak = Math.max(peak, inflight);
        await new Promise(r => setImmediate(r));
        inflight--;
        const id = (args?.ids as string[])[0];
        return id === 'm-x' ? fail('없음') : ok({items: [{id, title: id}]});
      },
    });
    const got = await createStore(f.ctx).getMemories(['m-1', 'm-x', 'm-2']);
    assert.deepEqual(got.map(m => m.id), ['m-1', 'm-2']);
    assert.equal(peak, 3);
  });
});

// 장기 대기 루프: more·reset·실패 후 재개·중단 시 대기 끊김·timeoutMs>wait.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {RETRY_MS, TIMEOUT_GRACE_MS, watchChanges, type WatchHandlers} from '../src/work/watch.ts';
import {asResult, example, fakeWorkCtx, until, type Handler} from './work-fake.ts';
import type {McpResultLike} from '../src/work/types.ts';

const changes = (over: Record<string, unknown>): McpResultLike => ({
  isError: false, text: '',
  structured: {...example('work_changes.empty').result, ...over},
});

/** 줄 세운 응답을 차례로 준다. 줄이 끝나면 중단될 때까지 걸어 둔다(실제 장기 대기처럼). */
function queue(items: (McpResultLike | Error)[]): Handler {
  let i = 0;
  return (_args, options) => {
    const next = items[i++];
    if (next instanceof Error) throw next;
    if (next) return next;
    return new Promise((_, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new Error('중단됨')), {once: true});
    });
  };
}

function recorder() {
  const log: string[] = [];
  const handlers: WatchHandlers = {
    onChanges: async c => void log.push(`changes:${c.workIds.join(',')}|${c.sessionIds.join(',')}`),
    onReset: async () => void log.push('reset'),
    onPaused: k => void log.push(`paused:${k}`),
    onError: m => void log.push(`error:${m}`),
  };
  return {log, handlers};
}

describe('watchChanges', () => {
  it('첫 응답의 waitMax를 읽어 wait로 쓰고 timeoutMs는 wait보다 넉넉하다', async () => {
    const f = fakeWorkCtx({work_changes: queue([changes({waitMax: 30000}), changes({waitMax: 30000})])});
    const ac = new AbortController();
    const r = recorder();
    const done = watchChanges(f.ctx, {after: 's0', handlers: r.handlers, signal: ac.signal});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.equal(f.calls[0].args?.wait, 0); // waitMax를 알기 전에는 기다리지 않는다
    assert.equal(f.calls[1].args?.wait, 30000);
    assert.equal(f.calls[1].options?.timeoutMs, 30000 + TIMEOUT_GRACE_MS);
    assert.ok((f.calls[1].options?.timeoutMs ?? 0) > (f.calls[1].args?.wait as number));
    assert.ok(f.calls[1].options?.signal);
  });

  it('waitMax가 작으면 wait도 그 안(서버가 밝힌 값 이하)이다', async () => {
    const f = fakeWorkCtx({work_changes: queue([changes({waitMax: 2000}), changes({waitMax: 2000})])});
    const ac = new AbortController();
    const done = watchChanges(f.ctx, {after: 's0', handlers: recorder().handlers, signal: ac.signal});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.equal(f.calls[2].args?.wait, 2000);
  });

  it('more면 기다리지 않고 바로 다시 걸고, seq를 이어 간다', async () => {
    const f = fakeWorkCtx({
      work_changes: queue([
        changes({waitMax: 30000, more: true, seq: 's1', workIds: ['w1'], sessionIds: []}),
        changes({waitMax: 30000, seq: 's2', workIds: ['w2'], sessionIds: ['x']}),
      ]),
    });
    const ac = new AbortController();
    const r = recorder();
    const slept: number[] = [];
    const done = watchChanges(f.ctx, {after: 's0', handlers: r.handlers, signal: ac.signal, sleep: async ms => void slept.push(ms)});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.deepEqual(r.log, ['changes:w1|', 'changes:w2|x']);
    assert.deepEqual(f.calls.slice(0, 3).map(c => c.args?.after), ['s0', 's1', 's2']);
    assert.deepEqual(slept, []);
  });

  it('reset이면 onReset을 부르고 새 seq부터 이어 건다', async () => {
    const f = fakeWorkCtx({work_changes: queue([asResult(example('work_changes.reset')), changes({waitMax: 30000})])});
    const ac = new AbortController();
    const r = recorder();
    const done = watchChanges(f.ctx, {after: 'old', handlers: r.handlers, signal: ac.signal});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.equal(r.log[0], 'reset');
    assert.equal(f.calls[1].args?.after, (example('work_changes.reset').result as {seq: string}).seq);
  });

  it('호출이 실패하면(oni가 죽음) 짧게 쉬었다가 마지막 seq부터 다시 건다', async () => {
    const f = fakeWorkCtx({
      work_changes: queue([changes({waitMax: 30000, seq: 's1', workIds: ['w1'], sessionIds: []}), new Error('EPIPE')]),
    });
    const ac = new AbortController();
    const r = recorder();
    const slept: number[] = [];
    const done = watchChanges(f.ctx, {after: 's0', handlers: r.handlers, signal: ac.signal, sleep: async ms => void slept.push(ms)});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.ok(r.log.some(l => l.startsWith('error:oni에 연결하지 못했습니다')));
    assert.deepEqual(slept, [RETRY_MS]);
    assert.deepEqual(f.calls.slice(1, 3).map(c => c.args?.after), ['s1', 's1']);
  });

  it('다시 읽기 처리가 실패하면 seq를 올리지 않고 같은 곳부터 다시 건다', async () => {
    const f = fakeWorkCtx({
      work_changes: queue([
        changes({waitMax: 30000, seq: 's1', workIds: ['w1'], sessionIds: []}),
        changes({waitMax: 30000, seq: 's1', workIds: ['w1'], sessionIds: []}),
      ]),
    });
    const ac = new AbortController();
    let n = 0;
    const handlers: WatchHandlers = {...recorder().handlers, onChanges: async () => { if (n++ === 0) throw new Error('읽기 실패'); }};
    const done = watchChanges(f.ctx, {after: 's0', handlers, signal: ac.signal, sleep: async () => {}});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.deepEqual(f.calls.slice(0, 2).map(c => c.args?.after), ['s0', 's0']);
  });

  it('중단하면 걸려 있던 대기 호출이 끊기고 루프가 끝난다', async () => {
    const f = fakeWorkCtx({work_changes: queue([changes({waitMax: 30000})])});
    const ac = new AbortController();
    const r = recorder();
    const done = watchChanges(f.ctx, {after: 's0', handlers: r.handlers, signal: ac.signal});
    await until(() => f.calls.length >= 2); // 두 번째 호출이 대기에 걸림
    const signal = f.calls[1].options?.signal;
    assert.equal(signal?.aborted, false);
    ac.abort();
    await done; // 끝나지 않으면 시험 시간 초과
    assert.equal(signal?.aborted, true);
    assert.deepEqual(r.log.filter(l => l.startsWith('error')), []);
  });

  it('중단 신호가 선 채 시작하면 호출하지 않는다', async () => {
    const f = fakeWorkCtx({work_changes: queue([])});
    const ac = new AbortController();
    ac.abort();
    await watchChanges(f.ctx, {after: 's0', handlers: recorder().handlers, signal: ac.signal});
    assert.equal(f.calls.length, 0);
  });

  it('판이 맞지 않으면 onPaused를 부르고 루프를 끝낸다(다시 걸지 않음)', async () => {
    const newer = {...example('work_changes.empty').result, contract: {name: 'orbit.work', major: 2, minor: 0, supportedMajors: [2]}};
    const f = fakeWorkCtx({work_changes: () => ({isError: false, text: '', structured: newer})});
    const r = recorder();
    await watchChanges(f.ctx, {after: 's0', handlers: r.handlers, signal: new AbortController().signal});
    assert.deepEqual(r.log, ['paused:oni-newer']);
    assert.equal(f.calls.length, 1);
  });

  it('waitMax가 0이면 바쁜 반복을 피해 쉰다', async () => {
    const f = fakeWorkCtx({work_changes: queue([changes({waitMax: 0}), changes({waitMax: 0})])});
    const ac = new AbortController();
    const slept: number[] = [];
    const done = watchChanges(f.ctx, {after: 's0', handlers: recorder().handlers, signal: ac.signal, sleep: async ms => void slept.push(ms)});
    await until(() => f.calls.length >= 3);
    ac.abort();
    await done;
    assert.ok(slept.length >= 1);
  });
});

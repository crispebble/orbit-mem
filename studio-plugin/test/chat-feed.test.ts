// 실행 기록 모으기: history(과거) + onEvent(실시간)가 겹치거나 순서가 뒤섞여도, 스트림이 끊겼다 이어져도 빠짐·겹침이 없다.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import type {DaemonEvent} from '../src/daemon/api.ts';
import {createRunFeed} from '../src/chat/feed.ts';
import {fakeClient, sampleRun, upStatus} from './daemon-fake.ts';

const ev = (seq: number, runId = 'r1'): DaemonEvent => ({seq, runId, type: 'warning', at: '2026-11-01T00:00:00Z', event: {type: 'warning', at: '2026-11-01T00:00:00Z', message: `m${seq}`}});
const tick = () => new Promise<void>(r => setTimeout(r, 5));

describe('createRunFeed', () => {
  it('history보다 실시간이 먼저 와도 겹치지 않고 seq 순서로 합친다', async () => {
    let release!: () => void;
    const gate = new Promise<void>(r => (release = r));
    const f = fakeClient((method) => {
      if (method === 'daemon.history') return gate.then(() => ({events: [ev(1), ev(2), ev(3)]}));
      if (method === 'daemon.getRun') return {run: sampleRun()};
      return {};
    });
    const feed = createRunFeed(f.client, 'r1');
    f.emitEvent(ev(3));
    f.emitEvent(ev(4));
    release();
    await tick();
    assert.deepEqual(feed.getState().events.map(e => e.seq), [1, 2, 3, 4]);
    assert.equal(feed.getState().loading, false);
    assert.equal(feed.getState().summary?.runId, 'r1');
    feed.dispose();
  });

  it('다른 실행의 이벤트는 받지 않는다', async () => {
    const f = fakeClient(method => (method === 'daemon.history' ? {events: []} : {run: sampleRun()}));
    const feed = createRunFeed(f.client, 'r1');
    await tick();
    f.emitEvent(ev(9, 'other'));
    assert.equal(feed.getState().events.length, 0);
    feed.dispose();
  });

  it('스트림이 끊겼다 다시 이어지면 마지막 seq 뒤를 history로 채운다', async () => {
    let historyCalls: unknown[] = [];
    const f = fakeClient((method, params) => {
      if (method === 'daemon.history') {
        historyCalls.push(params);
        const after = (params as {afterSeq?: number}).afterSeq ?? 0;
        return {events: [1, 2, 3, 4, 5].filter(s => s > after && s <= (after === 0 ? 2 : 5)).map(s => ev(s))};
      }
      return {run: sampleRun()};
    });
    f.emitStatus(upStatus);
    const feed = createRunFeed(f.client, 'r1');
    await tick();
    assert.deepEqual(feed.getState().events.map(e => e.seq), [1, 2]);
    f.emitStatus({...upStatus, streaming: false});
    f.emitEvent(ev(2)); // 재전송된 겹침
    f.emitStatus(upStatus);
    await tick();
    assert.deepEqual(feed.getState().events.map(e => e.seq), [1, 2, 3, 4, 5]);
    assert.deepEqual(historyCalls[1], {runId: 'r1', afterSeq: 2});
    feed.dispose();
  });

  it('history가 실패하면 error를 두고 reload로 다시 읽는다', async () => {
    let fail = true;
    const f = fakeClient(method => {
      if (method === 'daemon.history') {
        if (fail) throw Object.assign(new Error('down'), {code: 'DAEMON_DOWN'});
        return {events: [ev(1)]};
      }
      return {run: sampleRun()};
    });
    const feed = createRunFeed(f.client, 'r1');
    await tick();
    assert.ok(feed.getState().error);
    assert.equal(feed.getState().loading, false);
    fail = false;
    await feed.reload();
    assert.equal(feed.getState().error, undefined);
    assert.equal(feed.getState().events.length, 1);
    feed.dispose();
  });

  it('dispose 뒤에는 더 받지 않는다', async () => {
    const f = fakeClient(method => (method === 'daemon.history' ? {events: []} : {run: sampleRun()}));
    const feed = createRunFeed(f.client, 'r1');
    await tick();
    feed.dispose();
    f.emitEvent(ev(1));
    assert.equal(feed.getState().events.length, 0);
  });
});

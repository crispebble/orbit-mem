// 작업 store: 처음 읽기, 변경 반영, 확인 전, reset, 판 멈춤, 범위 바뀜.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {createWorkStore} from '../src/work/store.ts';
import type {Work, WorkSummary} from '../src/work/types.ts';
import {example, fakeWorkCtx, until, type Handler} from './work-fake.ts';

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T;
const contract = {name: 'orbit.work', major: 1, minor: 0, supportedMajors: [1]};
const res = (structured: unknown) => ({isError: false, text: '', structured});

const baseWork = (example('work_get.claude-task').result as {work: Work}).work;
const mkWork = (id: string, state: 'open' | 'done', cwd = '/Users/dev/repo/orbit'): Work => {
  const w = clone(baseWork);
  w.id = id; w.state = state; w.cwd = cwd;
  if (state === 'done') {
    w.steps = w.steps.map(s => ({...s, status: 'done', block: undefined}));
    w.counts = {pending: 0, ready: 0, in_progress: 0, blocked: 0, done: w.steps.length, cancelled: 0};
  }
  return w;
};

/** 환경: 서버 쪽 기록을 흉내 낸다 */
function server(works: Work[]) {
  const table = new Map(works.map(w => [w.id, w]));
  const sessionIds: string[] = [];
  const changes: unknown[] = [];
  const api = {
    table, changes, seq: 'q1',
    wake: undefined as undefined | ((c: unknown) => void),
    /** 변경을 낸다. 걸려 있는 대기가 있으면 깨운다 */
    push(c: unknown) {
      if (api.wake) { const w = api.wake; api.wake = undefined; w(c); } else changes.push(c);
    },
    summaries: (): WorkSummary[] => [...table.values()].map(w => {
      const {steps, runs: _r, evidence: _e, children: _c, ...head} = w;
      return {...head, stepCount: steps.length, verification: {none: 0, pass: 0, fail: 0, needs_user: 0, outdated: 0}} as WorkSummary;
    }),
  };
  const handlers: Record<string, Handler> = {
    work_list: () => res({contract, seq: api.seq, items: api.summaries(), limitMax: 50}),
    work_sessions: args => {
      if (args?.sessionId) {
        const ex = example('work_sessions.session.page1').result;
        return res({...ex, session: {...(ex.session as object), sessionId: args.sessionId}});
      }
      void sessionIds;
      return res({contract, seq: api.seq, sessions: [], limitMax: 50});
    },
    work_get: args => {
      const w = table.get(String(args?.workId));
      if (!w) return {isError: true, text: 'NOT_FOUND', structured: {contract, error: {code: 'NOT_FOUND', message: 'x', field: 'workId'}}};
      return res({contract, seq: api.seq, work: w});
    },
    // 실제 서버처럼: wait가 0이면 곧바로 waitMax만 밝히고, 아니면 바뀐 것이 생길 때까지(또는 중단까지) 걸어 둔다.
    work_changes: (args, options) => {
      const next = changes.shift();
      if (next) return res(next);
      if (!args?.wait) return res({contract, seq: api.seq, reset: false, more: false, waitMax: 30000, workIds: [], sessionIds: []});
      return new Promise((resolve, reject) => {
        api.wake = c => resolve(res(c));
        options?.signal?.addEventListener('abort', () => reject(new Error('중단됨')), {once: true});
      });
    },
  };
  return {api, handlers};
}

const noSleep = async () => {};

describe('work store', () => {
  it('처음 읽으면 목록·세션을 채우고, 확인 기다림이 있는 열린 작업의 단계만 미리 읽는다', async () => {
    const s = server([mkWork('w1', 'open'), mkWork('w0', 'done')]);
    const f = fakeWorkCtx(s.handlers);
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    assert.deepEqual(store.getState().works.map(w => w.id).sort(), ['w0', 'w1']);
    assert.deepEqual(Object.keys(store.getState().details), ['w1']);
    // 처음 연결할 때 이미 끝난 옛 작업은 확인 전으로 올리지 않는다
    assert.deepEqual(store.getState().pendingReview, []);
    assert.deepEqual(store.visibleWorks().map(w => w.id), ['w1']);
    await until(() => f.calls.some(c => c.tool === 'work_changes'));
    assert.equal(f.calls.find(c => c.tool === 'work_changes')?.args?.after, 'q1');
    store.dispose();
  });

  it('바뀐 작업만 다시 읽고, 보던 중 끝난 작업은 확인 전으로 남아 정리할 수 있다', async () => {
    const s = server([mkWork('w1', 'open'), mkWork('w2', 'open')]);
    const f = fakeWorkCtx(s.handlers);
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    await until(() => f.calls.filter(c => c.tool === 'work_changes').length >= 2);
    s.api.table.set('w1', mkWork('w1', 'done'));
    s.api.push({contract, seq: 'q2', reset: false, more: false, waitMax: 30000, workIds: ['w1'], sessionIds: ['sess-1']});
    await until(() => store.getState().pendingReview.length === 1);
    const getCalls = f.calls.filter(c => c.tool === 'work_get').map(c => c.args?.workId);
    assert.deepEqual(getCalls.slice(-1), ['w1']); // w2는 다시 읽지 않았다
    assert.equal(store.getState().works.find(w => w.id === 'w1')?.state, 'done');
    assert.deepEqual(store.visibleWorks().map(w => w.id).sort(), ['w1', 'w2']); // 끝났어도 목록에 남는다
    assert.deepEqual(store.getState().sessions.map(x => x.sessionId), ['sess-1']);
    assert.deepEqual(f.storage.get('work.review.v1'), {pending: ['w1']});
    await store.dismiss('w1');
    assert.deepEqual(store.visibleWorks().map(w => w.id), ['w2']);
    assert.deepEqual(f.storage.get('work.review.v1'), {pending: []});
    store.dispose();
  });

  it('저장된 확인 전을 다시 켤 때 이어받는다', async () => {
    const s = server([mkWork('w9', 'done')]);
    const f = fakeWorkCtx(s.handlers);
    f.storage.set('work.review.v1', {pending: ['w9']});
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    assert.deepEqual(store.visibleWorks().map(w => w.id), ['w9']);
    store.dispose();
  });

  it('reset이면 목록을 다시 읽되 확인 전 표시는 유지하고 오류·경고 상태를 만들지 않는다', async () => {
    const s = server([mkWork('w1', 'open')]);
    const f = fakeWorkCtx(s.handlers);
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    await until(() => f.calls.filter(c => c.tool === 'work_changes').length >= 2);
    s.api.table.set('w1', mkWork('w1', 'done'));
    s.api.push({contract, seq: 'q2', reset: false, more: false, waitMax: 30000, workIds: ['w1'], sessionIds: []});
    await until(() => store.getState().pendingReview.length === 1);
    const listsBefore = f.calls.filter(c => c.tool === 'work_list').length;
    s.api.table.set('w3', mkWork('w3', 'open'));
    s.api.push({contract, seq: 'q9', reset: true, more: false, waitMax: 30000});
    await until(() => f.calls.filter(c => c.tool === 'work_list').length > listsBefore);
    await until(() => store.getState().works.some(w => w.id === 'w3'));
    assert.deepEqual(store.getState().pendingReview, ['w1']);
    assert.equal(store.getState().status, 'ready');
    assert.deepEqual(f.logs.filter(l => l.level !== 'info'), []);
    store.dispose();
  });

  it('지워진 작업(NOT_FOUND)은 목록에서 뺀다', async () => {
    const s = server([mkWork('w1', 'open')]);
    const f = fakeWorkCtx(s.handlers);
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => f.calls.filter(c => c.tool === 'work_changes').length >= 2);
    s.api.table.delete('w1');
    s.api.push({contract, seq: 'q2', reset: false, more: false, waitMax: 30000, workIds: ['w1'], sessionIds: []});
    await until(() => store.getState().works.length === 0);
    assert.deepEqual(store.getState().details, {});
    store.dispose();
  });

  it('판이 맞지 않으면 이 데이터만 멈추고 안내 문구를 둔다', async () => {
    const newer = {contract: {...contract, major: 2, supportedMajors: [2]}, error: {code: 'UNSUPPORTED_MAJOR', message: 'x', supportedMajors: [2]}};
    const f = fakeWorkCtx({work_list: () => ({isError: true, text: 'x', structured: newer})});
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'paused');
    assert.equal(store.getState().pause?.message, 'Orbit이 이 Orbit 앱보다 새 판입니다. Orbit 앱을 올려 주세요.');
    assert.equal(f.calls.length, 1);
    store.dispose();
  });

  it('연결 실패는 오류 상태를 보이고 쉬었다가 다시 읽는다', async () => {
    const s = server([mkWork('w1', 'open')]);
    let fail = true;
    const f = fakeWorkCtx({...s.handlers, work_list: (a, o) => { if (fail) { fail = false; throw new Error('끊김'); } return s.handlers.work_list(a, o); }});
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    assert.equal(f.logs.filter(l => l.level === 'error').length, 1);
    assert.equal(store.getState().works.length, 1);
    store.dispose();
  });

  it('프로젝트 범위면 cwdPrefix로 읽고, 범위가 바뀌면 다시 읽는다', async () => {
    const s = server([mkWork('w1', 'open')]);
    const f = fakeWorkCtx(s.handlers, {kind: 'project', id: 'p', name: 'P', directories: ['/Users/dev/repo/orbit']});
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => store.getState().status === 'ready');
    assert.deepEqual(f.calls.find(c => c.tool === 'work_list')?.args?.cwdPrefix, ['/Users/dev/repo/orbit']);
    f.setScope({kind: 'all'});
    await until(() => f.calls.filter(c => c.tool === 'work_list').length >= 2);
    assert.equal(f.calls.filter(c => c.tool === 'work_list')[1].args?.cwdPrefix, undefined);
    await until(() => store.getState().scope.kind === 'all');
    store.dispose();
  });

  it('dispose하면 걸려 있던 변경 대기가 끊긴다', async () => {
    const s = server([mkWork('w1', 'open')]);
    const f = fakeWorkCtx(s.handlers);
    const store = createWorkStore(f.ctx, {sleep: noSleep});
    await store.start();
    await until(() => f.calls.filter(c => c.tool === 'work_changes').length >= 2);
    const signal = f.calls.filter(c => c.tool === 'work_changes')[1].options?.signal;
    store.dispose();
    assert.equal(signal?.aborted, true);
  });
});

// 홈 작업 지도의 열기·새 일 시키기(플러그인 API 0.1.27, S4-5 C1): 데몬 실행이 있으면 그 대화, 없으면 작업을 이어 하는 대화.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet} from '../src/work/contract.ts';
import {registerWorkProvider, type WorkChatHooks} from '../src/work/provider.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const state: WorkState = {status: 'ready', scope: {kind: 'all'}, works: [summaryOf({...task, cwd: '/repo/x'})], details: {}, sessions: [], pendingReview: []};
const store = {getState: () => state, subscribe: () => () => {}, ensureDetails: () => {}, loadDetail: async () => task};

function provide(chat?: WorkChatHooks) {
  let provided: any;
  registerWorkProvider({work: {provide: p => { provided = p; return () => {}; }}}, store as never, undefined, chat);
  return provided;
}

describe('작업 공급자 열기·새 일 시키기', () => {
  it('작업 대화를 열 수 없으면(훅 없음) open·compose를 주지 않는다', () => {
    const p = provide();
    assert.equal(p.open, undefined);
    assert.equal(p.compose, undefined);
  });

  it('작업에 해당하는 데몬 실행이 있으면 그 실행의 대화를 연다', async () => {
    const opened: unknown[] = [];
    const asked: unknown[] = [];
    const p = provide({runFor: async t => { asked.push(t); return 'run-7'; }, open: t => void opened.push(t)});
    await p.open(task.id);
    assert.deepEqual(asked, [{workId: task.id}]);
    assert.deepEqual(opened, [{kind: 'run', runId: 'run-7'}]);
  });

  it('없거나 데몬이 답하지 않으면 작업을 이어 하는 대화(작업 폴더 포함)를 연다', async () => {
    const opened: unknown[] = [];
    const p = provide({runFor: async () => undefined, open: t => void opened.push(t)});
    await p.open(task.id);
    const failing = provide({runFor: async () => { throw new Error('daemon down'); }, open: t => void opened.push(t)});
    await failing.open(task.id);
    assert.deepEqual(opened, [{kind: 'work', workId: task.id, cwd: '/repo/x'}, {kind: 'work', workId: task.id, cwd: '/repo/x'}]);
  });

  it('세션 노드는 세션 id로 실행을 찾고, 없으면 아무것도 열지 않는다', async () => {
    const opened: unknown[] = [];
    const asked: unknown[] = [];
    let runId: string | undefined = 'run-3';
    const p = provide({runFor: async t => { asked.push(t); return runId; }, open: t => void opened.push(t)});
    await p.open('session:S-9');
    runId = undefined;
    await p.open('session:S-9');
    assert.deepEqual(asked, [{sessionId: 'S-9'}, {sessionId: 'S-9'}]);
    assert.deepEqual(opened, [{kind: 'run', runId: 'run-3'}]);
  });

  it('새 일 시키기는 쓰던 글과 폴더로 작성 대화를 연다', async () => {
    const opened: unknown[] = [];
    const p = provide({runFor: async () => undefined, open: t => void opened.push(t)});
    await p.compose({text: 'fix the build', cwd: '/repo/y'});
    await p.compose({text: 'hello'});
    assert.deepEqual(opened, [{kind: 'compose', text: 'fix the build', cwd: '/repo/y'}, {kind: 'compose', text: 'hello'}]);
  });
});

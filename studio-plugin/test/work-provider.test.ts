// 홈 작업 지도에 주는 작업 공급자(Studio 플러그인 API 0.1.25, Studio 4차 S4-2).
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet, parseSessions} from '../src/work/contract.ts';
import {homeItems, providerDetail, registerWorkProvider} from '../src/work/provider.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import type {Session, Work} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const sessions = parseSessions(example('work_sessions.list.page1').result).sessions;
const NOW = Date.parse('2026-10-28T07:00:00.000Z');

function state(works: Work[] = [task], ss: Session[] = sessions, over: Partial<WorkState> = {}): WorkState {
  return {status: 'ready', scope: {kind: 'all'}, works: works.map(summaryOf), details: Object.fromEntries(works.map(w => [w.id, w])), sessions: ss, pendingReview: [], ...over};
}

describe('홈 작업 항목', () => {
  it('작업은 사실(끝·답 기다림·도는 중·마지막 움직임)과 프로젝트·최근 단계 두 줄을 준다', () => {
    const items = homeItems(state(), NOW);
    const w = items.find(i => i.id === task.id)!;
    assert.equal(w.kind, 'work');
    assert.equal(w.title, task.title);
    assert.deepEqual(w.project, {id: task.project!.scope, label: task.project!.label});
    assert.equal(w.done, false);
    assert.equal(w.waiting, true, '사용자 응답 대기 단계가 있다');
    assert.equal(w.activeAt, Date.parse(task.updatedAt));
    assert.ok(w.recent && w.recent.length >= 1 && w.recent.length <= 2, String(w.recent));
  });

  it('끝난 작업은 done과 끝난 시각, 열린 작업이 없는 세션은 세션 노드가 된다', () => {
    const done: Work = {...task, id: 'w-done', state: 'done', closedAt: '2026-10-28T06:00:00.000Z', steps: task.steps.map(s => ({...s, status: 'done', block: undefined}))};
    const lone: Session = {...sessions[0], sessionId: 's-lone', openWorkIds: [], activeStepIds: [], state: 'running', lastSeenAt: '2026-10-28T06:59:00.000Z', branch: 'feat/x'};
    const items = homeItems(state([task, done], [...sessions, lone]), NOW);
    const d = items.find(i => i.id === 'w-done')!;
    assert.equal(d.done, true);
    assert.equal(d.doneAt, Date.parse('2026-10-28T06:00:00.000Z'));
    const s = items.find(i => i.id === 'session:s-lone')!;
    assert.equal(s.kind, 'session');
    assert.equal(s.running, true);
    assert.ok(s.title.length > 0);
    // 작업을 연 세션은 따로 노드가 되지 않는다
    assert.ok(!items.some(i => i.id === `session:${task.origin!.sessionId}`));
  });

  it('오래전에 끝난 세션은 넣지 않는다', () => {
    const old: Session = {...sessions[0], sessionId: 's-old', openWorkIds: [], state: 'ended', endReason: 'session_end', endedAt: '2026-10-20T00:00:00.000Z', lastSeenAt: '2026-10-20T00:00:00.000Z'};
    assert.ok(!homeItems(state([task], [old]), NOW).some(i => i.id === 'session:s-old'));
  });
});

describe('작업 공급자 등록', () => {
  it('ctx.work가 있는 Studio에만 등록하고, 바뀌면 알린다', async () => {
    let listener: (() => void) | undefined;
    const store = {getState: () => state(), subscribe: (l: () => void) => { listener = l; return () => { listener = undefined; }; }, ensureDetails: () => {}, loadDetail: async () => task};
    let provided: {id: string; list(s: AbortSignal): Promise<readonly unknown[]>; subscribe?(f: () => void): () => void} | undefined;
    const off = registerWorkProvider({work: {provide: p => { provided = p as never; return () => { provided = undefined; }; }}}, store as never);
    assert.ok(provided);
    assert.ok((await provided!.list(new AbortController().signal)).length > 0);
    let changed = 0;
    const unsub = provided!.subscribe!(() => changed++);
    listener!();
    assert.equal(changed, 1);
    unsub();
    off();
    assert.equal(provided, undefined);
    // 옛 Studio(ctx.work 없음)는 아무것도 하지 않는다
    assert.doesNotThrow(() => registerWorkProvider({}, store as never)());
  });

  it('상세는 단계·검증·세션을 준다', async () => {
    const store = {getState: () => state(), loadDetail: async () => task};
    const d = await providerDetail(store as never, task.id);
    assert.ok(d);
    assert.equal(d!.steps!.length, task.steps.length);
    assert.ok(d!.steps!.some(s => s.status === 'blocked'));
    assert.ok(d!.sessions!.some(s => s.id === task.origin!.sessionId));
  });
});

describe('프로젝트 이름', () => {
  it('절대경로 이름은 마지막 폴더 이름으로 보인다', () => {
    const w: Work = {...task, project: {scope: 'project:/private/tmp/alpha-x', label: '/private/tmp/alpha-x'}};
    assert.deepEqual(homeItems(state([w]), NOW).find(i => i.id === w.id)!.project, {id: 'project:/private/tmp/alpha-x', label: 'alpha-x'});
  });
});

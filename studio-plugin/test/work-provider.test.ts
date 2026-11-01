// 홈 작업 지도에 주는 작업 공급자(Studio 플러그인 API 0.1.25, Studio 4차 S4-2).
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet, parseSessions} from '../src/work/contract.ts';
import {homeItems, isOpenOrbitWork, providerDetail, registerWorkProvider} from '../src/work/provider.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import type {Session, Work} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const claudeTask = parseGet(example('work_get.claude-task').result).work;
// 지도에 오르는 것은 OpenOrbit이 만든 작업뿐이다: 예시 작업의 출처를 orbit-pi로 바꿔 쓴다
const task: Work = {...claudeTask, origin: {...claudeTask.origin!, host: 'orbit-pi', surface: 'orbit-pi'}};
const sessions = parseSessions(example('work_sessions.list.page1').result).sessions;
const NOW = Date.parse('2026-10-28T07:00:00.000Z');

function state(works: Work[] = [task], ss: Session[] = sessions, over: Partial<WorkState> = {}): WorkState {
  return {status: 'ready', scope: {kind: 'all'}, works: works.map(summaryOf), details: Object.fromEntries(works.map(w => [w.id, w])), sessions: ss, pendingReview: [], ...over};
}

const orbitTask = task;

describe('홈 작업 판단(OpenOrbit 작업만)', () => {
  it('origin.host가 orbit-pi이거나 목록 종류가 orbit-pi-plan이면 OpenOrbit 작업이다', () => {
    assert.equal(isOpenOrbitWork(orbitTask), true);
    assert.equal(isOpenOrbitWork({...task, origin: {...task.origin!, host: 'claude', list: {kind: 'orbit-pi-plan'}}}), true);
    assert.equal(isOpenOrbitWork(claudeTask), false);
    assert.equal(isOpenOrbitWork({...claudeTask, origin: undefined}), false);
  });

  it('순수 Claude·Codex 세션이 만든 작업은 지도에 없고, OpenOrbit 작업만 남는다', () => {
    const other: Work = {...claudeTask, id: 'w-claude'};
    const ids = homeItems(state([orbitTask, other]), NOW).map(i => i.id);
    assert.deepEqual(ids, [orbitTask.id]);
  });

  it('작업 없는 세션은 세션 노드가 되지 않는다(열린 결정 카드가 묶여 있어도)', () => {
    const lone: Session = {...sessions[0], sessionId: 's-lone', openWorkIds: [], activeStepIds: [], state: 'running', lastSeenAt: '2026-10-28T06:59:00.000Z'};
    const cards = {workIds: new Set<string>(), sessionIds: new Set(['s-lone'])};
    assert.deepEqual(homeItems(state([orbitTask], [...sessions, lone]), NOW, cards).filter(i => i.kind === 'session'), []);
  });

  it('옛 작업도 id로 여는 상세는 그대로 된다', async () => {
    const d = await providerDetail({getState: () => state([claudeTask]), loadDetail: async () => claudeTask} as never, claudeTask.id);
    assert.equal(d!.title, claudeTask.title);
  });
});

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

  it('끝난 작업은 done과 끝난 시각을 준다', () => {
    const done: Work = {...task, id: 'w-done', state: 'done', closedAt: '2026-10-28T06:00:00.000Z', steps: task.steps.map(s => ({...s, status: 'done', block: undefined}))};
    const items = homeItems(state([task, done]), NOW);
    const d = items.find(i => i.id === 'w-done')!;
    assert.equal(d.done, true);
    assert.equal(d.doneAt, Date.parse('2026-10-28T06:00:00.000Z'));
    // 작업을 연 세션은 따로 노드가 되지 않는다
    assert.ok(!items.some(i => i.id === `session:${task.origin!.sessionId}`));
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

describe('검토 지적', () => {
  it('읽는 연결이 멈췄거나 실패해 보일 것이 없으면 list가 이유와 함께 던진다(Studio가 「답하지 않음」을 보인다)', async () => {
    const make = (s: WorkState) => {
      let provided: {list(sig: AbortSignal): Promise<readonly unknown[]>} | undefined;
      registerWorkProvider({work: {provide: p => { provided = p as never; return () => {}; }}}, {getState: () => s, subscribe: () => () => {}, ensureDetails: () => {}, loadDetail: async () => task} as never);
      return provided!;
    };
    await assert.rejects(make(state([], [], {status: 'paused', pause: {kind: 'orb-newer', message: '올려 주세요'}})).list(new AbortController().signal), /올려 주세요/);
    await assert.rejects(make(state([], [], {status: 'error', error: '잠김'})).list(new AbortController().signal), /잠김/);
    assert.ok((await make(state([task], sessions, {status: 'error', error: '잠김'})).list(new AbortController().signal)).length > 0, '읽어 둔 것이 있으면 보인다');
  });

  it('다른 세션이 이어 돌리는 작업도 도는 중이다(상세의 세션과 같은 묶음)', () => {
    const other: Session = {...sessions[0], sessionId: 's-other', state: 'running', openWorkIds: [task.id]};
    const quiet = sessions.map(s => (s.sessionId === task.origin!.sessionId ? {...s, state: 'idle'} : s));
    const w: Work = {...task, steps: task.steps.map(s => ({...s, status: s.status === 'blocked' ? 'in_progress' : s.status, block: undefined}))};
    assert.equal(homeItems(state([w], [...quiet, other]), NOW).find(i => i.id === task.id)!.running, true);
  });
});

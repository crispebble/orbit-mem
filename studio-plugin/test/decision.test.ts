// 결정 카드(orbit.decision 1.0)를 Orbit Studio Needs you에 주는 길(Studio 4차 S4-3, Atlas 0s39ap1v5b0qw).
// 계약 읽기 → 카드 출처(DecisionSource: 지금은 orbit-mem mcp --reader, 뒤에 데몬 구독) → 저장소 → Studio 결정 공급자.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {describe, it} from 'node:test';
import {parseDecision, parseDecisionChanges, parseDecisionPage} from '../src/decision/contract.ts';
import {orbDecisionSource} from '../src/decision/source.ts';
import {createDecisionStore} from '../src/decision/store.ts';
import {registerDecisionProvider, toStudioCard} from '../src/decision/provider.ts';
import type {Decision, DecisionSource} from '../src/decision/types.ts';
import {homeItems} from '../src/work/provider.ts';
import {parseGet, parseSessions} from '../src/work/contract.ts';
import {summaryOf} from '../src/work/store.ts';
import {example as workExample, fakeWorkCtx, until} from './work-fake.ts';

const contractExample = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../internal/decision/contract/v1/examples/${name}.json`, import.meta.url), 'utf8')) as {args: Record<string, unknown>; isError?: boolean; result: Record<string, unknown>};
const result = (name: string) => ({isError: contractExample(name).isError === true, text: '', structured: contractExample(name).result});

const card = (over: Partial<Decision> = {}): Decision => ({
  id: 'd-1', state: 'open', source: 'question', owner: 'orbit-pi', createdAt: '2026-11-01T01:00:00.000Z', updatedAt: '2026-11-01T01:00:00.000Z',
  questions: [{text: 'Proceed?', header: 'Plan approval', multiSelect: false, options: [{id: 'approve', label: 'Approve'}, {id: 'revise', label: 'Revise'}, {id: 'cancel', label: 'Cancel'}]}],
  links: [], ...over,
});

describe('계약 읽기', () => {
  it('목록·변경·카드를 읽고 모르는 칸은 버린다', () => {
    const page = parseDecisionPage(contractExample('decision_list.open').result);
    assert.equal(page.items.length, 1);
    const d = page.items[0];
    assert.equal(d.source, 'ops');
    assert.equal(d.owner, 'orbit-pi');
    assert.equal(d.questions[0].recommended, 'proceed');
    assert.equal(d.links[0].ref, 'https://github.com/onidot-labs/atlas/pull/12');
    const changes = parseDecisionChanges(contractExample('decision_changes.changes').result);
    assert.deepEqual(changes.decisionIds, ['d-13f37b481eac']);
    assert.equal(changes.waitMax, 30000);
    assert.equal(parseDecision({id: 'x'}), null, '질문이 없으면 카드가 아니다');
  });
});

describe('orb 카드 출처', () => {
  it('목록은 major 1과 상태·updatedSince를 넘기고, 답은 by=studio로 보낸다', async () => {
    const f = fakeWorkCtx({
      decision_list: () => result('decision_list.open'),
      decision_answer: () => result('decision_answer.ok'),
    });
    const source = orbDecisionSource(f.ctx);
    const page = await source.list({state: ['answered'], updatedSince: '2026-11-01T00:00:00.000Z'});
    assert.equal(page.items.length, 1);
    assert.deepEqual(f.calls[0].args, {state: ['answered'], updatedSince: '2026-11-01T00:00:00.000Z', limit: 200, major: 1});
    const out = await source.answer('d-13f37b481eac', [{optionIds: ['proceed']}]);
    assert.equal(out.ok, true);
    assert.deepEqual(f.calls[1].args, {id: 'd-13f37b481eac', answers: [{optionIds: ['proceed']}], by: 'studio', major: 1});
  });

  it('먼저 답한 카드는 CONFLICT와 지금의 카드, 사람이 연 카드는 CANNOT_RELAY', async () => {
    const f = fakeWorkCtx({decision_answer: args => result(args?.id === 'd-13f37b481eac' ? 'error.conflict' : 'error.cannot-relay')});
    const source = orbDecisionSource(f.ctx);
    const conflict = await source.answer('d-13f37b481eac', [{optionIds: ['record']}]);
    assert.equal(conflict.ok, false);
    assert.equal(!conflict.ok && conflict.code, 'CONFLICT');
    assert.equal(!conflict.ok && conflict.decision?.answer?.by, 'studio');
    const human = await source.answer('d-3f2a9c1e7b04', [{optionIds: ['1']}]);
    assert.equal(!human.ok && human.code, 'CANNOT_RELAY');
  });
});

/** 줄 세운 변경 응답을 차례로 준다. 줄이 끝나면 중단될 때까지 걸어 둔다. */
function fakeSource(pages: {open: Decision[]; closed: Decision[]}, changes: (Promise<{decisionIds: string[]; reset?: boolean}> | {decisionIds: string[]; reset?: boolean})[] = []) {
  const calls: string[] = [];
  let i = 0;
  const source: DecisionSource = {
    async list(q) {
      calls.push(`list:${q.state.join(',')}${q.updatedSince ? '@since' : ''}`);
      return {items: q.state.includes('open') ? pages.open : pages.closed, seq: 's0', truncated: false};
    },
    changes(q, signal) {
      calls.push(`changes:${q.after ?? ''}:${q.wait}`);
      const next = changes[i++];
      const n = i;
      if (next) return Promise.resolve(next).then(c => ({seq: `s${n}`, decisionIds: c.decisionIds, reset: c.reset ?? false, more: false, waitMax: 30000}));
      return new Promise((_, reject) => signal.addEventListener('abort', () => reject(new Error('stopped')), {once: true}));
    },
    async answer(id, answers) {
      calls.push(`answer:${id}:${JSON.stringify(answers)}`);
      return {ok: true, decision: {...card({id}), state: 'answered', answer: {answers, by: 'studio', at: '2026-11-01T02:00:00.000Z'}}};
    },
  };
  return {source, calls};
}

describe('카드 저장소', () => {
  it('열린 카드와 오늘 닫힌 카드를 읽고, 변경 알림마다 다시 읽어 알린다', async () => {
    const pages = {open: [card()], closed: [card({id: 'd-0', state: 'answered', answer: {answers: [{optionIds: ['approve']}], by: 'terminal', at: '2026-11-01T00:30:00.000Z'}})]};
    let fire!: (c: {decisionIds: string[]}) => void;
    const {source, calls} = fakeSource(pages, [new Promise(resolve => { fire = resolve; })]);
    const store = createDecisionStore(source, {now: () => Date.parse('2026-11-01T03:00:00.000Z')});
    let notices = 0;
    store.subscribe(() => notices++);
    await store.start();
    assert.equal(store.getState().status, 'ready');
    assert.deepEqual(store.getState().open.map(d => d.id), ['d-1']);
    assert.deepEqual(store.getState().closed.map(d => d.id), ['d-0']);
    pages.open = [card(), card({id: 'd-2'})];
    fire({decisionIds: ['d-2']});
    await until(() => store.getState().open.length === 2);
    assert.ok(notices >= 2);
    assert.ok(calls.includes('list:open'), calls.join(' '));
    assert.ok(calls.includes('list:answered,withdrawn,expired@since'), calls.join(' '));
    assert.ok(calls.some(c => c.startsWith('changes:s0:')), '첫 목록의 seq부터 기다린다');
    assert.deepEqual([...store.openRefs().workIds], []);
    store.dispose();
  });

  it('열린 카드의 작업·세션을 지도의 「답 기다림」에 넘긴다', async () => {
    const {source} = fakeSource({open: [card({work: {host: 'orbit-pi', sessionId: 'S-1', workId: 'w-1'}})], closed: []});
    const store = createDecisionStore(source);
    await store.start();
    assert.deepEqual([...store.openRefs().workIds], ['w-1']);
    assert.deepEqual([...store.openRefs().sessionIds], ['S-1']);
    store.dispose();
  });
});

describe('Studio 결정 공급자', () => {
  it('카드를 Studio 모양으로 옮긴다: 시각은 ms, 사람이 연 카드는 relay=false, 링크 ref가 http면 url', () => {
    const d = parseDecisionPage(contractExample('decision_list.open').result).items[0];
    const c = toStudioCard({...d, work: {host: 'orbit-pi', workId: 'w-9', sessionId: 'S'}});
    assert.equal(c.relay, true);
    assert.equal(c.createdAt, Date.parse('2026-10-31T15:24:28.258Z'));
    assert.equal(c.workItemId, 'w-9');
    assert.deepEqual(c.links, [{kind: 'pr', label: 'atlas#12', url: 'https://github.com/onidot-labs/atlas/pull/12'}]);
    assert.equal(toStudioCard({...d, owner: 'human'}).relay, false);
    assert.equal(toStudioCard({...d, owner: 'someone-new'}).relay, false, '모르는 owner에는 답을 넘기지 않는다');
    const answered = toStudioCard(card({state: 'answered', closedAt: '2026-11-01T02:00:00.000Z', answer: {answers: [{text: 'x'}], by: 'telegram', at: '2026-11-01T02:00:00.000Z'}}));
    assert.deepEqual(answered.answer, {answers: [{text: 'x'}], by: 'telegram', at: Date.parse('2026-11-01T02:00:00.000Z')});
  });

  it('ctx.decisions가 있으면 등록하고, 목록·답하기가 저장소와 출처로 간다. 옛 Studio에서는 아무것도 하지 않는다', async () => {
    const {source, calls} = fakeSource({open: [card()], closed: [card({id: 'd-old', state: 'answered', answer: {answers: [{optionIds: ['approve']}], by: 'studio', at: '2026-10-31T10:00:00.000Z'}})]});
    const store = createDecisionStore(source);
    await store.start();
    let provided: any;
    const off = registerDecisionProvider({decisions: {provide: p => { provided = p; return () => { provided = undefined; }; }}}, store, source);
    assert.equal(provided.id, 'orbit');
    const listed = await provided.list({since: Date.parse('2026-11-01T00:00:00.000Z')}, new AbortController().signal);
    assert.deepEqual(listed.map((c: {id: string}) => c.id), ['d-1'], '어제 닫힌 카드는 빠진다');
    const out = await provided.answer('d-1', {answers: [{optionIds: ['approve']}]}, new AbortController().signal);
    assert.equal(out.ok, true);
    assert.equal(out.card.answer.by, 'studio');
    assert.ok(calls.includes('answer:d-1:[{"optionIds":["approve"]}]'));
    off();
    assert.equal(provided, undefined);
    assert.doesNotThrow(() => registerDecisionProvider({}, store, source)());
    store.dispose();
  });

  it('홈 지도: 열린 카드가 묶인 작업은 「답 기다림」이다', () => {
    const task = parseGet(workExample('work_get.claude-task').result).work;
    const calm = {...task, origin: {...task.origin!, host: 'orbit-pi'}, id: 'w-calm', steps: task.steps.map(s => ({...s, status: 'done' as const, block: undefined}))};
    const sessions = parseSessions(workExample('work_sessions.list.page1').result).sessions.map(s => ({...s, state: 'running'}));
    const state = {status: 'ready' as const, scope: {kind: 'all' as const}, works: [summaryOf(calm)], details: {[calm.id]: calm}, sessions, pendingReview: []};
    const now = Date.parse('2026-10-28T07:00:00.000Z');
    assert.equal(homeItems(state, now).find(i => i.id === 'w-calm')!.waiting, false);
    assert.equal(homeItems(state, now, {workIds: new Set(['w-calm']), sessionIds: new Set()}).find(i => i.id === 'w-calm')!.waiting, true);
  });
});

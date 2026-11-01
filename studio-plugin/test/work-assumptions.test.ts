// AU-2 조사·AU-3 가정: 계약 읽기(없는 칸·모르는 값), 지도 배지, 작업 상세 「조사」「결정·가정」, 결정 카드 「가정으로 진행 중」.
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {setLocale} from '../src/i18n/index.ts';
import {assumptionStateView, isLongDiscovery, rungView, stepMarks} from '../src/work/assumptions.ts';
import {parseDecision} from '../src/decision/contract.ts';
import {parseGet} from '../src/work/contract.ts';
import {toFlowGraph} from '../src/work/graph.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import {toStudioCard} from '../src/decision/provider.ts';
import {createDecisionStore} from '../src/decision/store.ts';
import type {Work} from '../src/work/types.ts';
import {auResult, STEP_NATIVE, STEP_NATIVE_ID, STEP_REOPENED, STEP_WAITING} from './au23-fixture.ts';
import {example} from './work-fake.ts';

type Harness = typeof import('./work-render-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-au23-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const opts = {bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent', packages: 'external', loader: {'.css': 'empty'}} as const;
  await build({entryPoints: [new URL('./work-render-harness.tsx', import.meta.url).pathname], outfile: join(dir, 'work.mjs'), ...opts});
  h = await import(join(dir, 'work.mjs'));
});
after(() => rmSync(dir, {recursive: true, force: true}));

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');
const au = parseGet(auResult()).work;
const sessions: WorkState['sessions'] = [];
const state = (w: Work): WorkState => ({status: 'ready', scope: {kind: 'all'}, works: [summaryOf(w)], details: {[w.id]: w}, sessions, pendingReview: []});
const lean = (w: Work, stateOf: string, rung = 'assume') =>
  ({...w, assumptions: w.assumptions!.map(a => (a.id === 'as-overturned' ? {...a, rung, state: stateOf} : a))});

describe('계약 읽기', () => {
  it('조사와 가정 셋을 읽는다', () => {
    assert.equal(au.discovery?.decisions.length, 2);
    assert.equal(au.discovery?.decisions[0].settled, '작업 머리 아래');
    assert.equal(au.discovery?.size, 'medium');
    assert.deepEqual(au.assumptions?.map(a => [a.rung, a.state]), [['evidence', 'settled'], ['assume', 'overturned'], ['ask', 'open']]);
    assert.equal(au.assumptions?.[1].answeredAt, Date.parse('2026-10-28T06:00:00.000Z'));
  });
  it('1.2 이하 작업(두 칸 없음)도 그대로 읽는다', () => {
    const w = parseGet(example('work_get.claude-task').result).work;
    assert.equal(w.discovery, undefined);
    assert.equal(w.assumptions, undefined);
  });
  it('모르는 열거값·모양이 틀린 줄은 깨지지 않는다', () => {
    const w = parseGet(auResult({
      assumptions: [{id: 'x', point: 'p', chosen: 'c', basis: 'b', confidence: 'weird', rung: 'new-rung', state: 'new-state', steps: [STEP_REOPENED]}, {nope: 1}, 'x'],
      discovery: {problem: 'p', decisions: [{point: 'q'}, 5], unknowns: 'bad', size: 'huge'},
    })).work;
    assert.equal(w.assumptions?.length, 1);
    assert.equal(rungView('new-rung').known, false);
    assert.equal(assumptionStateView('new-state').known, false);
    assert.deepEqual(w.discovery?.decisions, [{point: 'q', evidence: []}]);
    assert.deepEqual(w.discovery?.unknowns, []);
    assert.deepEqual(stepMarks(w, w.steps.find(s => s.id === STEP_REOPENED)!), [], '모르는 상태는 배지를 만들지 않는다');
  });
  it('같은 가정 id가 겹쳐 오면 하나로 합친다(나중 줄이 이기고 자리는 처음 자리)', () => {
    const dup = (id: string, chosen: string) => ({id, point: 'p', chosen, basis: 'b', confidence: 'low', rung: 'assume', state: 'open', steps: []});
    const w = parseGet(auResult({assumptions: [dup('a', '첫째'), dup('b', 'B'), dup('a', '둘째')]})).work;
    assert.deepEqual(w.assumptions?.map(a => [a.id, a.chosen]), [['a', '둘째'], ['b', 'B']]);
  });
  it('problem이 없거나 틀려도 나머지 조사는 살리고, 문제 줄은 그리지 않는다', () => {
    const w = parseGet(auResult({discovery: {problem: 7, decisions: [{point: 'q', evidence: ['e']}], unknowns: ['u'], sources: ['s'], at: 't'}})).work;
    assert.equal(w.discovery?.problem, '');
    assert.equal(w.discovery?.decisions.length, 1);
    h.setLocale('en');
    h.useState(state(w));
    const t = text(h.renderWorkTab(w.id));
    assert.ok(t.includes('Decision points') && t.includes('Still unknown'));
    assert.ok(!t.includes('Problem'));
  });
  it('같은 글이 겹친 증거·출처도 그려진다(키 충돌 없음)', () => {
    const w = parseGet(auResult({discovery: {problem: 'p', decisions: [{point: '같음', evidence: ['x', 'x']}, {point: '같음', evidence: []}], unknowns: ['u', 'u'], sources: ['s', 's'], at: 't'}})).work;
    h.useState(state(w));
    const errors: unknown[] = [];
    const orig = console.error;
    console.error = (...a: unknown[]) => { errors.push(a); };
    try { h.renderWorkTab(w.id); } finally { console.error = orig; }
    assert.deepEqual(errors, []);
  });
  it('「닫힘」 상태(답 없이 카드를 닫음)는 배지가 없고 라벨이 있다', () => {
    const w = lean(au, 'closed');
    assert.deepEqual(stepMarks(w, w.steps.find(s => s.id === STEP_REOPENED)!), []);
    assert.equal(assumptionStateView('closed').label, 'Closed');
    setLocale('ko');
    try { assert.equal(assumptionStateView('closed').label, '닫힘'); } finally { setLocale('en'); }
  });
  it('단계 상세의 기댄 결정에 상태 배지가 함께 나온다', () => {
    h.setLocale('en');
    h.useState(state(au));
    const t = text(h.renderWorkTab(au.id, {step: STEP_REOPENED}));
    assert.match(t, /요약 위치 Running on an assumption Overturned · steps reopened/);
  });
  it('조사 칸이 깨져도 작업 전체는 읽힌다', () => {
    const w = parseGet(auResult({discovery: 'oops', assumptions: 'oops'})).work;
    assert.equal(w.discovery, undefined);
    assert.equal(w.assumptions, undefined);
    assert.equal(w.steps.length, 9);
  });
});

describe('지도 배지', () => {
  const step = (w: Work, id: string) => w.steps.find(s => s.id === id)!;
  it('뒤집힌 가정은 「다시 엶」, 답 기다림은 「답 기다림」(단계 id와 계획 id 둘 다로 찾는다)', () => {
    assert.deepEqual(stepMarks(au, step(au, STEP_REOPENED)), ['reopened']);
    assert.deepEqual(stepMarks(au, step(au, STEP_WAITING)), ['ask']);
    assert.deepEqual(stepMarks(au, step(au, STEP_NATIVE_ID)), ['ask'], `native.id=${STEP_NATIVE}`);
    assert.deepEqual(stepMarks(au, step(au, 'st-1a2b3c4d5e6f')), []);
  });
  it('열린 assume 가정에 기댄 단계는 「가정」', () => {
    const w = lean(au, 'open');
    assert.deepEqual(stepMarks(w, step(w, STEP_REOPENED)), ['assume']);
  });
  it('끝난 가정(확인·답함·기록)은 배지가 없다', () => {
    for (const st of ['confirmed', 'answered', 'settled']) {
      const w = lean(au, st);
      assert.deepEqual(stepMarks(w, step(w, STEP_REOPENED)), [], st);
    }
  });
  it('FlowGraph 노드 배지로 나온다(en)', () => {
    setLocale('en');
    const g = toFlowGraph(lean(au, 'open'), Date.now());
    const labels = (id: string) => g.nodes.find(n => n.id === id)!.badges!.map(b => b.label);
    assert.ok(labels(STEP_REOPENED).includes('Assumption'));
    assert.ok(labels(STEP_WAITING).includes('Waiting'));
    const g2 = toFlowGraph(au, Date.now());
    assert.ok(g2.nodes.find(n => n.id === STEP_REOPENED)!.badges!.some(b => b.label === 'Reopened'));
  });
  it('FlowGraph 노드 배지(ko)', () => {
    setLocale('ko');
    try {
      const g = toFlowGraph(au, Date.now());
      assert.ok(g.nodes.find(n => n.id === STEP_REOPENED)!.badges!.some(b => b.label === '다시 엶'));
      assert.ok(g.nodes.find(n => n.id === STEP_WAITING)!.badges!.some(b => b.label === '답 기다림'));
      assert.ok(toFlowGraph(lean(au, 'open'), Date.now()).nodes.find(n => n.id === STEP_REOPENED)!.badges!.some(b => b.label === '가정'));
    } finally {
      setLocale('en');
    }
  });
});

describe('작업 상세 화면', () => {
  it('en: 조사와 결정·가정이 그려진다', () => {
    h.setLocale('en');
    h.useState(state(au));
    const html = h.renderWorkTab(au.id);
    const t = text(html);
    for (const w of ['Discovery', 'Problem', 'Decision points', '가정을 어디에 보일까', 'src/work-ui/work-tab.tsx', 'Settled: 작업 머리 아래', 'Intended end state', 'Still unknown', 'Where it looked', 'mem-91ab',
      'Size: Medium', '2 rounds', 'Decisions', '3 decisions', 'Settled by evidence', 'Running on an assumption', 'Waiting for an answer', 'Overturned · steps reopened', 'Recorded', 'Open',
      'Chosen', 'Basis', 'Confidence', 'Low', '머리 아래', 'studio', 'Steps relying on it', '그래프 계산', '훅 연결', 'MCP 도구']) assert.ok(t.includes(w), w);
    for (const w of ['Reopened', 'Waiting']) assert.ok(t.includes(w), `지도 배지 ${w}`);
  });
  it('ko', () => {
    h.setLocale('ko');
    try {
      h.useState(state(au));
      const html = h.renderWorkTab(au.id);
      const t = text(html);
      for (const w of ['조사', '문제', '결정 지점', '바라는 끝 모습', '아직 모르는 것', '살펴본 곳', '결정·가정', '결정 3개', '근거로 정함', '가정으로 진행 중', '답을 기다림', '뒤집혀 다시 엶', '기록됨', '열려 있음',
        '고른 것', '근거', '확신', '낮음', '기댄 단계', '다시 엶', '답 기다림']) assert.ok(t.includes(w), w);
    } finally {
      h.setLocale('en');
    }
  });
  it('고른 단계 상세에 기댄 결정이 나온다', () => {
    h.setLocale('en');
    h.useState(state(au));
    const t = text(h.renderWorkTab(au.id, {step: STEP_WAITING}));
    assert.match(t, /Decisions it relies on/);
    assert.ok(t.includes('운영 데이터를 지울까'));
  });
  it('두 칸이 없는 작업에는 조사·결정 구역이 없다', () => {
    const old = parseGet(example('work_get.claude-task').result).work;
    h.setLocale('en');
    h.useState(state(old));
    const html = h.renderWorkTab(old.id);
    assert.doesNotMatch(html, /orbit-work-brief/);
    assert.doesNotMatch(text(html), /Discovery|Running on an assumption/);
  });
  it('모든 가정이 정리되면 「결정·가정」은 접혀 있고, 긴 조사는 접혀 있다', () => {
    assert.equal(isLongDiscovery({problem: 'x', decisions: new Array(5), unknowns: [], sources: []}), true);
    assert.equal(isLongDiscovery({problem: 'x', decisions: [], unknowns: [], sources: []}), false);
    const long = {...au.discovery!, decisions: Array.from({length: 6}, (_, i) => ({point: `지점${i}`, evidence: []}))};
    const closed = {...au, discovery: long, assumptions: au.assumptions!.map(a => ({...a, state: 'confirmed'}))};
    h.useState(state(closed));
    const html = h.renderWorkTab(closed.id);
    // Disclosure는 안쪽을 그려 두고 머리 단추의 aria-expanded로 접힘을 알린다
    const states = (x: string) => [...x.matchAll(/<button[^>]*aria-expanded="(true|false)"[^>]*>(.*?)<\/button>/g)].map(m => [text(m[2]).trim().split(' ')[0], m[1]]);
    const got = Object.fromEntries(states(html).filter(([n]) => n === 'Discovery' || n === 'Decisions'));
    assert.deepEqual(got, {Discovery: 'false', Decisions: 'false'});
    h.useState(state(au));
    const open = Object.fromEntries(states(h.renderWorkTab(au.id)).filter(([n]) => n === 'Discovery' || n === 'Decisions'));
    assert.deepEqual(open, {Discovery: 'true', Decisions: 'true'});
  });
});

describe('결정 카드 목록(Needs you)', () => {
  const card = (id: string, source: string, workId = 'w-1') => parseDecision({
    id, state: 'open', source, owner: 'orbit-pi', createdAt: '2026-10-28T05:00:00Z',
    questions: [{text: 'q?', multiSelect: false, options: [{id: 'a', label: 'A'}, {id: 'b', label: 'B'}], recommended: 'a', reason: '근거'}],
    work: {workId, sessionId: `s-${id}`, host: 'claude'},
  })!;
  it('assumption 출처를 읽고 recommended·reason을 지킨다', () => {
    const d = card('1', 'assumption');
    assert.equal(d.source, 'assumption');
    assert.equal(d.questions[0].recommended, 'a');
    assert.equal(d.questions[0].reason, '근거');
  });
  it('Needs you 줄에 「가정으로 진행 중」이 붙고 답은 그대로 받는다', () => {
    setLocale('ko');
    try {
      const s = toStudioCard(card('1', 'assumption'));
      assert.equal(s.source, 'question');
      assert.equal(s.meta, '가정으로 진행 중 · claude');
      assert.equal(s.questions[0].recommended, 'a');
      assert.equal(s.questions[0].reason, '근거');
    } finally {
      setLocale('en');
    }
    assert.equal(toStudioCard(card('1', 'assumption')).meta, 'Running on an assumption · claude');
    assert.equal(toStudioCard(card('1', 'assumption')).source, 'question', '호스트 Needs you는 assumption 출처를 모른다');
    for (const src of ['question', 'permission', 'ops', 'plugin']) assert.equal(toStudioCard(card('x', src)).source, src);
    assert.equal(toStudioCard(card('2', 'question')).meta, 'claude');
  });
  it('가정 카드는 「답 기다림」 연결(openRefs)에 세지 않지만 목록에는 남는다', async () => {
    const open = [card('1', 'assumption', 'w-assume'), card('2', 'question', 'w-ask')];
    const store = createDecisionStore({
      list: async (q: {state: string[]}) => ({items: q.state.includes('open') ? open : [], seq: '1', truncated: false}),
      changes: async () => new Promise(() => {}),
      answer: async () => ({ok: true}),
    } as never);
    await store.refresh();
    const refs = store.openRefs();
    assert.deepEqual([...refs.workIds], ['w-ask']);
    assert.deepEqual([...refs.sessionIds], ['s-2']);
    assert.equal(store.getState().open.length, 2);
    store.dispose();
  });
});

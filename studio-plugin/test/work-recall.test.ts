// AU-4 단계별 회상(orbit.work 1.4 steps[].recall): 계약 읽기(없는 칸·틀린 모양)와 단계 상세 「이 단계에 붙인 기억」.
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {normalizeRecall, parseGet} from '../src/work/contract.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import type {Work} from '../src/work/types.ts';
import {STEP_REOPENED, STEP_WAITING} from './au23-fixture.ts';
import {example} from './work-fake.ts';

type Harness = typeof import('./work-render-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-au4-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const opts = {bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent', packages: 'external', loader: {'.css': 'empty'}} as const;
  await build({entryPoints: [new URL('./work-render-harness.tsx', import.meta.url).pathname], outfile: join(dir, 'work.mjs'), ...opts});
  h = await import(join(dir, 'work.mjs'));
});
after(() => rmSync(dir, {recursive: true, force: true}));

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');

const LESSON = {id: 'm-0cb102be99a8', kind: '교훈 후보', text: 'format.mjs에 export를 더하면 index.mjs도 고친다 (m-0cb102be99a8 · 교훈 후보)'};

/** claude-task 예시에 1.4 회상 칸을 단다: STEP_REOPENED에는 기억 둘, 나머지는 없음. */
function recallResult(recall: unknown = {attempt: 2, query: 'format.mjs에 slugify\nfiles: format.mjs', memories: [LESSON, {id: 'm-fact', text: '시험은 node --test'}], at: '2026-10-28T05:10:00.000Z'}) {
  const base = example('work_get.claude-task').result as Record<string, any>;
  const work = JSON.parse(JSON.stringify(base.work));
  work.steps = work.steps.map((s: any) => (s.id === STEP_REOPENED ? {...s, recall} : s));
  return {...base, contract: {...(base.contract as object), minor: 4}, work};
}

const state = (w: Work): WorkState => ({status: 'ready', scope: {kind: 'all'}, works: [summaryOf(w)], details: {[w.id]: w}, sessions: [], pendingReview: []});

describe('계약 읽기', () => {
  it('단계의 recall을 읽는다(시도·질의·기억)', () => {
    const w = parseGet(recallResult()).work;
    const s = w.steps.find(x => x.id === STEP_REOPENED)!;
    assert.equal(s.recall?.attempt, 2);
    assert.deepEqual(s.recall?.memories.map(m => m.id), ['m-0cb102be99a8', 'm-fact']);
    assert.equal(s.recall?.memories[0].kind, '교훈 후보');
    assert.equal(w.steps.find(x => x.id === STEP_WAITING)?.recall, undefined);
  });
  it('1.3 이하 작업(칸 없음)·틀린 모양은 칸이 없다', () => {
    const old = parseGet(example('work_get.claude-task').result).work;
    assert.ok(old.steps.every(s => s.recall === undefined));
    assert.equal(normalizeRecall('x'), undefined);
    assert.equal(normalizeRecall({memories: [{kind: 'k'}, 5, {id: ''}]}), undefined);
    assert.deepEqual(normalizeRecall({memories: [{id: 'm-1', kind: 7}], attempt: 1.5})?.memories, [{id: 'm-1'}]);
    const w = parseGet(recallResult({memories: 'bad'})).work;
    assert.equal(w.steps.find(x => x.id === STEP_REOPENED)?.recall, undefined);
  });
});

describe('단계 상세', () => {
  it('고른 단계에 붙인 기억이 종류·글과 함께 나온다(글에 id가 없으면 id도)', () => {
    const w = parseGet(recallResult()).work;
    h.setLocale('en');
    h.useState(state(w));
    const t = text(h.renderWorkTab(w.id, {step: STEP_REOPENED}));
    assert.match(t, /Memories given to this step/);
    assert.ok(t.includes('format.mjs에 export를 더하면 index.mjs도 고친다 (m-0cb102be99a8 · 교훈 후보)'));
    assert.ok(t.includes('교훈 후보'));
    assert.ok(t.includes('시험은 node --test') && t.includes('m-fact'));
  });
  it('기억이 없는 단계에는 그 줄이 없고, 한국어 화면은 한국어 이름표다', () => {
    const w = parseGet(recallResult()).work;
    h.setLocale('ko');
    h.useState(state(w));
    assert.ok(!text(h.renderWorkTab(w.id, {step: STEP_WAITING})).includes('이 단계에 붙인 기억'));
    assert.ok(text(h.renderWorkTab(w.id, {step: STEP_REOPENED})).includes('이 단계에 붙인 기억'));
    h.setLocale('en');
  });
});

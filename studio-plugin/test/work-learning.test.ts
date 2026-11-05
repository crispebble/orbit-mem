// AU-7 능숙해짐 지표(orbit.work 1.5 work_metrics): 계약 읽기와 작업 탭 「Learning / 능숙해짐」 구역.
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {parseMetrics} from '../src/work/contract.ts';
import {isOrbitCodeWork} from '../src/work/provider.ts';

type Harness = typeof import('./work-render-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-au7-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const opts = {bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent', packages: 'external', loader: {'.css': 'empty'}} as const;
  await build({entryPoints: [new URL('./work-render-harness.tsx', import.meta.url).pathname], outfile: join(dir, 'work.mjs'), ...opts});
  h = await import(join(dir, 'work.mjs'));
});
after(() => rmSync(dir, {recursive: true, force: true}));

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');

const doc = (workId: string, title: string, endedAt: number, m: Record<string, unknown>) => ({
  workId, title, state: 'done', project: 'project:textkit', kind: 'add', size: 'small', openedAt: '2026-11-01T00:00:00.000Z',
  endedAt, sessions: [{session: 'S', from: 1}],
  metrics: {asked: 0, assumed: 0, overturned: 0, corrections: 0, repeats: 0, replans: 0, firstPass: {passed: 1, steps: 1}, costUsd: 0.02, turns: 3, minutes: 1.5, ...m},
});

const side = (over: Record<string, unknown>) => ({
  works: 1, asked: 0, assumed: 0, overturned: 0, corrections: 0, repeats: 0, replans: 0, firstPassRate: 1, costUsd: 0.02, turns: 3, minutes: 1.5, ...over,
});

function result() {
  const a = doc('w-a', 'slugify 추가', Date.UTC(2026, 10, 1, 1), {repeats: 1, corrections: 1, firstPass: {passed: 0, steps: 1}, costUsd: 0.12, turns: 9});
  const b = doc('w-b', 'titleCase 추가', Date.UTC(2026, 10, 1, 2), {asked: 1, assumed: 2});
  return {
    contract: {name: 'orbit.work', major: 1, minor: 5, supportedMajors: [1]},
    seq: 's-1',
    work: b,
    similar: {
      works: [a, b],
      trend: {
        earlier: side({repeats: 1, corrections: 1, firstPassRate: 0, costUsd: 0.12, turns: 9}),
        later: side({asked: 1, assumed: 2}),
        better: ['corrections', 'repeats', 'firstPass', 'cost', 'turns'],
        worse: ['asked'],
      },
    },
  };
}

describe('계약 읽기', () => {
  it('이 작업 지표와 비슷한 일·추이를 읽는다', () => {
    const r = parseMetrics(result());
    assert.equal(r.work.workId, 'w-b');
    assert.equal(r.work.metrics.asked, 1);
    assert.deepEqual(r.similar.works.map(w => w.workId), ['w-a', 'w-b']);
    assert.deepEqual(r.similar.trend.worse, ['asked']);
  });
  it('교정을 모르면 null, 모르는 추이 열쇠는 버린다', () => {
    const raw = result() as any;
    raw.work.metrics.corrections = null;
    raw.similar.trend.better.push('bogus');
    const r = parseMetrics(raw);
    assert.equal(r.work.metrics.corrections, null);
    assert.ok(!r.similar.trend.better.includes('bogus' as never));
  });
  it('모양이 틀리면 오류', () => {
    assert.throws(() => parseMetrics({contract: {major: 1}, seq: 's', work: {}}));
  });
});

describe('작업 탭 「Learning」 구역', () => {
  it('이 작업의 지표, 비슷한 일 목록과 추이(나아짐·나빠짐)를 보인다', () => {
    h.setLocale('en');
    const t = text(h.renderLearning(parseMetrics(result())));
    assert.match(t, /Learning/);
    assert.match(t, /1 stopped · 2 assumed Questions/); // StatRow: 값 다음 이름
    assert.match(t, /1\/1 \(100%\) First-try verification/);
    assert.match(t, /Similar works · textkit · add · small/);
    assert.ok(t.includes('slugify 추가') && t.includes('titleCase 추가'));
    assert.match(t, /This work/);
    assert.match(t, /Better: corrections 1 → 0/);
    assert.match(t, /Worse: questions 0 → 1/);
  });
  it('한 개뿐이면 추이 대신 안내, 한국어 이름표', () => {
    h.setLocale('ko');
    const raw = result();
    raw.similar.works = [raw.work];
    raw.similar.trend = {earlier: side({works: 0}), later: side({works: 0}), better: [], worse: []};
    const t = text(h.renderLearning(parseMetrics(raw)));
    assert.match(t, /능숙해짐/);
    assert.match(t, /멈춤 1 · 가정 2 질문/);
    assert.match(t, /비슷한 일이 더 끝나면 추이가 보입니다/);
    h.setLocale('en');
  });
});

describe('구역을 그리는 작업', () => {
  it('작업 탭은 orbit-code 작업(홈 지도와 같은 판단)에만 구역을 그린다', () => {
    h.setLocale('en');
    const base = {id: 'w-x', title: 't', state: 'done', steps: [], runs: [], evidence: [], counts: {pending: 0, ready: 0, in_progress: 0, blocked: 0, done: 0, cancelled: 0},
      openedAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', titleSource: 'host'};
    assert.equal(isOrbitCodeWork({...base, origin: {host: 'orbit-pi', surface: 'cli', sessionId: 's'}} as never), true);
    assert.equal(isOrbitCodeWork({...base, origin: {host: 'claude', surface: 'cli', sessionId: 's', list: {kind: 'claude-task'}}} as never), false);
  });
});

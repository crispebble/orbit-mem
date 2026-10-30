// 작업·세션 화면 렌더 시험(서버 렌더). 끝 기준 23(그래프·상세·세션 탭이 그려짐)·27(모르는 값)·28(상태 대응)·31(확인 동작 없음)·34(「Work」 글자 없음).
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {parseGet, parseSession, parseSessions} from '../src/work/contract.ts';
import {summaryOf, type WorkState} from '../src/work/store.ts';
import type {Work} from '../src/work/types.ts';
import {example} from './work-fake.ts';

type Harness = typeof import('./work-render-harness.tsx');
let h: Harness;
// 묶은 파일이 node_modules의 React·Forma를 찾도록 플러그인 폴더 안 node_modules/.cache에 둔다
const dir = new URL(`../node_modules/.cache/orbit-render-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const outfile = join(dir, 'harness.mjs');
  await build({
    entryPoints: [new URL('./work-render-harness.tsx', import.meta.url).pathname],
    outfile, bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent',
    // Forma·React는 node_modules에서 그대로 읽는다(묶으면 React가 두 벌이 된다)
    packages: 'external', loader: {'.css': 'empty'},
  });
  h = await import(outfile);
});
after(() => rmSync(dir, {recursive: true, force: true}));

const task = parseGet(example('work_get.claude-task').result).work;
const sessions = parseSessions(example('work_sessions.list.page1').result).sessions;
const NOW = Date.parse('2026-10-28T06:41:00.000Z');

function state(over: Partial<WorkState> = {}, works: Work[] = [task]): WorkState {
  return {
    status: 'ready', scope: {kind: 'all'}, works: works.map(summaryOf), details: Object.fromEntries(works.map(w => [w.id, w])),
    sessions, pendingReview: [], ...over,
  };
}

/** 태그를 지운 글 */
const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');

describe('작업 탭', () => {
  it('머리·열 머리·카드·상태 글이 그려진다', () => {
    h.useState(state());
    const t = text(h.renderWorkTab(task.id));
    assert.match(t, new RegExp(task.title));
    for (const s of task.steps) assert.ok(t.includes(s.title), s.title);
    assert.match(t, /Batch 1/);
    assert.match(t, /Done 2\/3/);
    assert.match(t, /Waiting for user confirmation/);
    assert.match(t, /Failed/);
    assert.match(t, /Flow/);
    assert.match(t, /By owner/);
  });

  it('고른 단계의 상세가 함께 그려진다(선행·실행·증거)', () => {
    h.useState(state());
    const t = text(h.renderWorkTab(task.id, {step: 'st-2b3c4d5e6f70'}));
    assert.match(t, /Before/);
    assert.match(t, /After/);
    assert.match(t, /Runs \(2\)/);
    assert.match(t, /Evidence \(3\)/);
    assert.match(t, /Open commit/);
  });

  it('담당별 보기', () => {
    h.useState(state());
    const t = text(h.renderWorkTab(task.id, {mode: 'owner'}));
    assert.match(t, /orbit:worker/);
  });

  it('모르는 상태·사유·검증·선행 출처도 깨지지 않고 「알 수 없음」으로', () => {
    const odd: Work = {...task, steps: task.steps.map((s, i) => i === 0 ? {...s, status: 'brand_new', verification: {state: 'weird', outdated: false}, after: [{stepId: task.steps[1].id, source: 'guess'}]} : s)};
    h.useState(state({}, [odd]));
    const t = text(h.renderWorkTab(odd.id, {step: odd.steps[0].id}));
    assert.match(t, /Unknown/);
  });

  it('판이 맞지 않으면 작업 화면만 멈추고 안내를 보인다', () => {
    h.useState(state({status: 'paused', pause: {kind: 'oni-newer', message: 'Orbit이 이 Orbit 앱보다 새 판입니다. Orbit 앱을 올려 주세요.'}}));
    assert.match(text(h.renderWorkTab(task.id)), /Please update the Orbit app/);
    assert.match(text(h.renderWorks()), /Please update the Orbit app/);
    assert.match(text(h.renderSessions()), /Please update the Orbit app/);
    assert.match(text(h.renderWorks()), /Check again/);
  });
});

describe('빈·로딩·오류·멈춤 화면', () => {
  /** StateView 한 개를 찾아 변형 단서(역할·제목 태그·compact)를 돌려준다 */
  function stateView(html: string) {
    const m = html.match(/<div[^>]*class="forma-state-view[^"]*"[^>]*>[\s\S]*?<(h\d|p) class="forma-state-view-title"/);
    assert.ok(m, `StateView가 없습니다: ${html.slice(0, 300)}`);
    return {head: m[0], compact: /data-compact/.test(m[0]), level: m[1]};
  }

  it('사이드바 보기는 Forma StateView를 compact·h3로 그린다', () => {
    h.useState(state({status: 'loading', works: [], sessions: []}));
    for (const html of [h.renderWorks(), h.renderSessions()]) {
      const v = stateView(html);
      assert.ok(v.compact, v.head);
      assert.equal(v.level, 'h3');
      assert.match(v.head, /role="status"/);
    }
    h.useState(state({status: 'error', error: '잠김', works: [], sessions: []}));
    const err = stateView(h.renderWorks());
    assert.match(err.head, /role="alert"/);
    assert.match(text(h.renderWorks()), /Could not read work history/);
    h.useState(state({works: [], sessions: []}));
    assert.match(text(h.renderWorks()), /No active work/);
    assert.equal(stateView(h.renderWorks()).level, 'h3');
  });

  it('멈춤 안내도 StateView이고 「다시 확인」은 동작 자리에 있다', () => {
    h.useState(state({status: 'paused', pause: {kind: 'oni-newer', message: '올려 주세요'}}));
    const html = h.renderWorks();
    stateView(html);
    assert.match(html, /forma-state-view-actions[\s\S]*Check again/);
  });

  it('작업 탭의 없음·로딩은 탭 본문 크기의 StateView다', () => {
    h.useState(state({works: [], details: {}}));
    const v = stateView(h.renderWorkTab('w-없음'));
    assert.ok(!v.compact);
    assert.match(v.head, /role="status"/);
  });
});

describe('받은 일의 사용자 확인 기다림', () => {
  it('세션으로 가는 길만 있고 확인·거절 동작은 없다', () => {
    h.useState(state());
    const html = h.renderWaiting(task.id, 'st-4d5e6f708192');
    const t = text(html);
    assert.match(t, /Copy session id/);
    assert.match(t, /Resume in terminal/);
    assert.ok(t.includes(task.origin!.sessionId));
    assert.ok(t.includes(task.cwd!));
    assert.doesNotMatch(t, /approve|reject|승인|거절/i);
  });
});

describe('사이드바 보기', () => {
  it('작업 목록: 열린 작업과 끝남 · 확인 전', () => {
    const done: Work = {...task, id: 'w-done', title: '끝난 작업', state: 'done'};
    h.useState(state({pendingReview: ['w-done']}, [task, done]));
    const t = text(h.renderWorks());
    assert.match(t, /Done · not reviewed/);
    assert.match(t, /Clear from list/);
    assert.ok(t.includes(task.title));
  });

  it('세션 목록: 세션 칸의 레포·브랜치와 상태', () => {
    h.useState(state());
    const t = text(h.renderSessions());
    for (const s of sessions.filter(x => x.state !== 'ended' && !x.parentSessionId)) {
      if (s.branch) assert.ok(t.includes(s.branch), s.branch);
    }
    assert.match(t, /Running|Waiting for user confirmation|Idle|Stale observation/);
  });
});

describe('세션 탭', () => {
  it('호출 계보·연결된 작업·증거·사건 기록', () => {
    const page = parseSession(example('work_sessions.session.page1').result);
    const loaded = {session: page.session, runs: page.runs ?? [], evidence: page.evidence ?? [], pages: [{events: page.events, nextCursor: page.nextCursor}]};
    const t = text(h.renderSession(loaded, state(), NOW));
    assert.match(t, /Call lineage/);
    assert.match(t, /Main session · Claude Code/);
    assert.match(t, /Event log/);
    assert.match(t, /Session started/);
    assert.match(t, /Session ended · Exited by input/);
    if (page.nextCursor) assert.match(t, /Show more events/);
  });
});

describe('화면 문구', () => {
  it('한국어 화면에는 「Work」 글자가 없다(데이터 값은 바꿔 두고 화면 문구만 본다)', () => {
    h.setLocale('ko');
    try {
    // 제목·브랜치·파일 이름 같은 데이터 값에는 work가 들어 있으므로 글 값만 바꾼다(칸 이름은 그대로)
    const scrub = <T,>(v: T): T => JSON.parse(JSON.stringify(v, (_k, x) => (typeof x === 'string' ? x.replace(/work/gi, 'job') : x)));
    const w = scrub(task);
    h.useState(scrub(state({pendingReview: []}, [w])));
    const page = scrub(parseSession(example('work_sessions.session.page1').result));
    const loaded = {session: page.session, runs: page.runs ?? [], evidence: page.evidence ?? [], pages: [{events: page.events}]};
    const all = [
      h.renderWorkTab(w.id, {step: 'st-2b3c4d5e6f70'}), h.renderWorkTab(w.id, {mode: 'owner'}), h.renderWaiting(w.id, 'st-4d5e6f708192'),
      h.renderWorks(), h.renderSessions(), h.renderSession(loaded, scrub(state({}, [w])), NOW),
    ].map(text).join(' ');
    assert.doesNotMatch(all, /work/i);
    } finally {
      h.setLocale('en');
    }
  });

  it('한국어로 바꾸면 화면 글이 한국어로 나온다', () => {
    h.setLocale('ko');
    try {
      h.useState(state());
      const t = text(h.renderWorkTab(task.id, {step: 'st-2b3c4d5e6f70'}));
      for (const word of ['묶음 1', '사용자 확인 기다림', '흐름', '담당별', '선행', '실행 2건', '증거 3건', '커밋 열기']) assert.ok(t.includes(word), word);
      assert.match(text(h.renderWorks()), /진행 \d+/);
    } finally {
      h.setLocale('en');
    }
  });
});

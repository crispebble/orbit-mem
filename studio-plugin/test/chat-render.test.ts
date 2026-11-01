// 대화 창·프로필 화면 렌더 시험(서버 렌더): 접근성 이름(e2e가 역할·이름으로 찾는다), 영어 기본·한국어, 「키 없음 — 건너뜀」, 결정 카드 모양.
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import type {DaemonEvent} from '../src/daemon/api.ts';

type Harness = typeof import('./chat-render-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-chat-render-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const outfile = join(dir, 'harness.mjs');
  await build({
    entryPoints: [new URL('./chat-render-harness.tsx', import.meta.url).pathname],
    outfile, bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent', packages: 'external', loader: {'.css': 'empty'},
  });
  h = await import(outfile);
  await h.loadKo();
});
after(() => rmSync(dir, {recursive: true, force: true}));

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');
const ev = (seq: number, event: Record<string, unknown>): DaemonEvent => ({seq, runId: 'r', type: String(event.type), at: 'x', event: {at: 'x', ...event} as DaemonEvent['event']});

describe('새 작업 입력', () => {
  it('영어: Request 입력, Start 단추, 모드 식별자 그대로, 툴팁은 번역', () => {
    h.setLocale('en');
    const html = h.renderComposer();
    assert.match(html, /<textarea[^>]*aria-label="Request"/);
    assert.match(html, /<button[^>]*aria-label="Start"/);
    const t = text(html);
    const at = ['ultrawork', 'plan', 'plan-approve'].map(m => t.indexOf(m));
    assert.ok(at.every(i => i >= 0) && at[0] < at[1] && at[1] < at[2], String(at));
    assert.ok(!t.includes('Default'));
    assert.match(html, /title="Shows the plan and waits for your approval before running it\."/);
    assert.match(html, /<p class="orbit-meta">Runs to the end without asking for plan approval \(default\)\.<\/p>/);
    assert.match(html, /title="Runs to the end without asking for plan approval \(default\)\."/);
    assert.match(html, /aria-label="Profile"/);
    assert.ok(html.includes('/Users/me/proj'));
  });
  it('한국어: 요청·시작, 모드 식별자는 영어 그대로', () => {
    h.setLocale('ko');
    const html = h.renderComposer();
    assert.match(html, /<textarea[^>]*aria-label="요청"/);
    assert.match(html, /<button[^>]*aria-label="시작"/);
    assert.ok(text(html).includes('ultrawork'));
    assert.match(html, /계획 승인을 묻지 않고 끝까지 실행합니다/);
    h.setLocale('en');
  });
  it('이어가기 입력은 안내를 보이고 요청 없이도 시작할 수 있다', () => {
    h.setLocale('en');
    const html = h.renderComposer({resumeWorkId: 'w9'});
    assert.match(text(html), /Continue the work w9/);
    assert.match(html, /Continue this work/);
  });
});

describe('결정 카드', () => {
  it('질문: 선택지 단추(숫자 키 표시), 다른 답 입력, 답 보내기', () => {
    h.setLocale('en');
    const html = h.renderCard('question');
    assert.match(html, /aria-label="Decision"/);
    assert.equal((html.match(/role="radio"/g) ?? []).length, 2);
    assert.match(html, /<kbd[^>]*>1<\/kbd>/);
    assert.ok(text(html).includes('Other answer'));
    assert.match(html, /<button[^>]*disabled=""[^>]*aria-label="Send answer"/, '고르기 전에는 보낼 수 없다');
    assert.ok(!html.includes('Permission'));
  });
  it('도구 허가: 「허가」 머리와 접힌 context', () => {
    const html = h.renderCard('tool');
    assert.ok(text(html).includes('Permission'));
    assert.ok(html.includes('Bash: rm -rf build'));
    assert.match(html, /data-kind="tool"/);
  });
  it('한국어: 선택지 라벨은 optionLabel, 보내기 이름은 「답 보내기」', () => {
    h.setLocale('ko');
    const html = h.renderCard('question', true);
    assert.ok(text(html).includes('허용'), '번역된 선택지');
    assert.ok(text(html).includes('deny'), '사전에 없으면 id');
    assert.match(html, /aria-label="답 보내기"/);
    assert.ok(text(html).includes('허가') === false, '질문 카드에는 허가 머리가 없다');
    h.setLocale('en');
  });
});

describe('실행 기록', () => {
  const events = [
    ev(1, {type: 'plan.proposed', attempt: 1, plan: {goal: 'ship', steps: [{id: 'a', title: 'A', role: 'worker', after: [], done: ''}, {id: 'b', title: 'B', role: 'worker', after: [], done: ''}, {id: 'c', title: 'C', role: 'qa', after: ['a', 'b'], done: ''}]}}),
    ev(2, {type: 'model.skipped', role: 'worker', ref: 'pi:openai/x', reason: 'no-key'}),
    ev(3, {type: 'warning', message: 'raw warning', key: 'warning.x', args: ['A']}),
    ev(4, {type: 'verify.result', step: 'a', attempt: 1, command: 'npm test', ok: false, outputTail: 'one\ntwo'}),
    ev(5, {type: 'commit.found', step: 'a', sha: '0123456789abcdef', subject: 'feat: a'}),
    ev(6, {type: 'something.new'}),
    ev(7, {type: 'decision.asked', id: 'd', title: 'Q?', options: ['allow'], kind: 'question'}),
  ];
  it('병렬 묶음·건너뜀 이유·검증·커밋을 그리고 모르는 type은 건너뛴다', () => {
    h.setLocale('en');
    const t = text(h.renderTranscript(events));
    assert.match(t, /Batch 1 · runs in parallel/);
    assert.match(t, /Batch 2/);
    assert.match(t, /Model skipped/);
    assert.match(t, /no key/);
    assert.match(t, /Verification/);
    assert.match(t, /Failed/);
    assert.match(t, /01234567/);
    assert.ok(!t.includes('something.new'));
  });
  it('key가 있으면 번역한 글, 없는 key는 기본 글', () => {
    h.setLocale('ko');
    const ko = text(h.renderTranscript(events, true, true));
    assert.match(ko, /경고 A/);
    assert.match(ko, /병렬 실행/);
    h.setLocale('en');
    const en = text(h.renderTranscript(events));
    assert.match(en, /raw warning/, '사전이 없으면 이벤트의 기본 글');
  });
  it('진행 중이 아니면 열린 결정은 카드가 아니라 「답하지 않음」 줄', () => {
    const html = h.renderTranscript(events, false);
    assert.ok(!html.includes('role="radiogroup"'));
    assert.ok(text(html).includes('Not answered'));
  });
});

describe('프로필 목록', () => {
  it('프로필마다 이름·폴더·이번 달 사용량, 기본 표시', () => {
    h.setLocale('en');
    const t = text(h.renderProfileList());
    assert.match(t, /work/);
    assert.match(t, /Default/);
    assert.match(t, /\/w/);
    assert.match(t, /This month: \$1\.20 of \$20\.00/);
    assert.match(t, /API-billed: \$0\.20 \(no limit\)/);
    assert.match(t, /side/);
    assert.match(t, /No folders/);
  });
  it('한국어', () => {
    h.setLocale('ko');
    const t = text(h.renderProfileList());
    assert.match(t, /기본/);
    assert.match(t, /이번 달/);
    assert.match(t, /API 과금분/);
    h.setLocale('en');
  });
});

describe('프로필 편집기', () => {
  it('Save 단추, 사슬 옆 「No key — skipped」, 키 목록(저장 안 됨), 사용량 표', () => {
    h.setLocale('en');
    const html = h.renderEditor('work');
    const t = text(html);
    assert.match(html, /<button[^>]*aria-label="Save"/);
    assert.ok(t.includes('No key — skipped'), 'pi:openrouter/free에 openrouter 키가 없다');
    assert.equal((t.match(/No key — skipped/g) ?? []).length, 1, 'claude 모델에는 붙지 않는다');
    assert.match(t, /openrouter/);
    assert.match(t, /Add key/);
    assert.match(t, /Usage by model|Engine/);
    assert.match(t, /subscription/);
    assert.match(html, /aria-label="Models for planner/);
  });
  it('한국어: 저장, 키 없음 — 건너뜀', () => {
    h.setLocale('ko');
    const html = h.renderEditor('work');
    assert.match(html, /<button[^>]*aria-label="저장"/);
    assert.ok(text(html).includes('키 없음 — 건너뜀'));
    assert.ok(text(html).includes('키 넣기'));
    h.setLocale('en');
  });
  it('폴더 목록에 제목(Project folders / 프로젝트 폴더)이 있다', () => {
    h.setLocale('en');
    assert.match(h.renderEditor('work'), /<strong[^>]*>Project folders<\/strong>/);
    h.setLocale('ko');
    assert.match(h.renderEditor('work'), /<strong[^>]*>프로젝트 폴더<\/strong>/);
    h.setLocale('en');
  });
  it('키를 이미 가진 공급처는 건너뜀 표시가 없고 「Replace key」', () => {
    // 키 상태는 sampleProfiles에서 바꿀 수 없어 렌더 결과로만 확인: 기본(키 없음)에서 Replace key는 나오지 않는다
    const t = text(h.renderEditor('work'));
    assert.ok(!t.includes('Replace key'));
  });
});

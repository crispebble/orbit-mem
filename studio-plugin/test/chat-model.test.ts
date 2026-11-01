// 대화 창의 나머지 순수 로직: 결정 카드 고르기, 새 작업 입력, 오류 글, key 문구 번역·대체, openChat 대상.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {answerFor, emptyPicks, pickByDigit, pickChoice, writeText} from '../src/chat/card.ts';
import {buildStartInput, emptyForm, folderCandidates, resumeInput} from '../src/chat/composer.ts';
import {daemonErrorText} from '../src/chat/errors.ts';
import {openChat} from '../src/chat/open.ts';
import {pendingChat, requestChat, takeChat} from '../src/chat/store.ts';
import {latestRunOfWork, viewForTarget} from '../src/chat/target.ts';
import {daemonText} from '../src/i18n/messages/daemon.ts';
import {fakeClient, sampleRun} from './daemon-fake.ts';

describe('결정 카드 고르기', () => {
  it('숫자 키로 고르고 같은 키를 다시 누르면 풀린다', () => {
    const p1 = pickByDigit(['yes', 'no'], emptyPicks, '2');
    assert.equal(p1.choice, 'no');
    assert.equal(answerFor(p1), 'no');
    assert.equal(pickByDigit(['yes', 'no'], p1, '2').choice, undefined);
  });
  it('선택지 밖·숫자가 아닌 키는 그대로(같은 객체)', () => {
    assert.equal(pickByDigit(['yes'], emptyPicks, '5'), emptyPicks);
    assert.equal(pickByDigit(['yes'], emptyPicks, 'a'), emptyPicks);
    assert.equal(pickByDigit(['yes'], emptyPicks, '0'), emptyPicks);
  });
  it('「다른 답」을 쓰면 그 글이 답이고, 고르면 쓴 글은 비운다', () => {
    const typed = writeText(pickChoice(emptyPicks, 'yes'), 'later please');
    assert.equal(typed.choice, undefined);
    assert.equal(answerFor(typed), 'later please');
    assert.equal(pickChoice(typed, 'no').text, '');
    assert.equal(answerFor(writeText(emptyPicks, '   ')), undefined);
  });
});

describe('새 작업 입력', () => {
  it('요청·절대 폴더가 있으면 startRun 입력을 만든다', () => {
    const r = buildStartInput(emptyForm({request: ' hi ', cwd: '/w', mode: 'ultrawork', profile: 'work', maxCostUsd: '2.5', maxTurns: '30'}));
    assert.ok(r.ok);
    if (r.ok) assert.deepEqual(r.input, {request: 'hi', cwd: '/w', mode: 'ultrawork', profile: 'work', limits: {maxCostUsd: 2.5, maxTurns: 30}});
  });
  it('상한을 비우면 limits를 보내지 않고, 프로필 자동이면 profile도 없다', () => {
    const r = buildStartInput(emptyForm({request: 'x', cwd: '/w'}));
    assert.ok(r.ok);
    if (r.ok) {
      assert.equal('limits' in r.input, false);
      assert.equal('profile' in r.input, false);
    }
  });
  it('요청 없음·상대 경로·나쁜 상한은 막는다', () => {
    const r = buildStartInput(emptyForm({request: '', cwd: 'rel', maxCostUsd: '-1', maxTurns: '2.5', maxMinutes: 'abc'}));
    assert.ok(!r.ok);
    if (!r.ok) assert.deepEqual(r.issues, ['request', 'cwd', 'cost', 'turns', 'minutes']);
  });
  it('이어가기는 요청이 비어도 된다', () => {
    const r = buildStartInput(emptyForm({cwd: '/w', resumeWorkId: 'w9'}));
    assert.ok(r.ok);
    if (r.ok) assert.deepEqual(r.input.resume, {workId: 'w9'});
  });
  it('「이어가기」 입력: 같은 폴더·프로필, 모드 default, 그 workId', () => {
    assert.deepEqual(resumeInput(sampleRun({mode: 'ultrawork', workId: 'w1'})), {request: '', cwd: '/w', mode: 'ultrawork', resume: {workId: 'w1'}, profile: 'p'});
  });
  it('새 입력의 기본 모드는 ultrawork', () => {
    assert.equal(emptyForm().mode, 'ultrawork');
    const r = buildStartInput(emptyForm({request: 'x', cwd: '/w'}));
    assert.ok(r.ok);
    if (r.ok) assert.equal(r.input.mode, 'ultrawork');
  });
  it('폴더 후보는 프로젝트 범위의 디렉터리들, 전체 범위는 없음', () => {
    assert.deepEqual(folderCandidates({kind: 'project', directories: ['/a', '/b']}), ['/a', '/b']);
    assert.deepEqual(folderCandidates({kind: 'all'}), []);
  });
});

describe('오류 글', () => {
  const t = daemonText.en.errors;
  it('알려진 코드는 사람이 읽을 글', () => {
    assert.equal(daemonErrorText({code: 'ORB_MISSING', message: 'spawn orb ENOENT'}, t), t.ORB_MISSING);
    assert.equal(daemonErrorText({code: 'STUDIO_TOO_OLD', message: 'x'}, t), t.STUDIO_TOO_OLD);
    assert.equal(daemonErrorText({code: 'PAIR_FAILED', message: 'device add failed'}, t), `${t.PAIR_FAILED} device add failed`);
    assert.match(daemonErrorText({code: 'DAEMON_START_FAILED', message: 'port in use'}, t), /could not start.*port in use/);
  });
  it('모르는 코드·이상한 값은 일반 글', () => {
    assert.match(daemonErrorText({code: 'WEIRD', message: 'boom'}, t), /Something went wrong\. boom/);
    assert.equal(daemonErrorText(undefined, t), t.unknown);
    assert.equal(daemonErrorText('x', t), t.unknown);
  });
  it('한국어 사전도 같은 코드를 가진다', () => {
    assert.deepEqual(Object.keys(daemonText.ko.errors).sort(), Object.keys(daemonText.en.errors).sort());
  });
});

describe('key 문구 번역·대체', () => {
  it('사전에 key가 있으면 args를 채워 번역하고 없으면 기본 글', async () => {
    const f = fakeClient(method => (method === 'daemon.messages' ? {lang: 'ko', messages: {'warning.x': '경고 %s (%d회)', 'option.allow': '허용'}} : {}));
    assert.equal(f.client.text('warning.x', ['a'], 'fallback'), 'fallback', '불러오기 전에는 기본 글');
    await f.client.loadMessages('ko');
    assert.equal(f.client.text('warning.x', ['a', 3], 'fallback'), '경고 a (3회)');
    assert.equal(f.client.text('nope', [], 'fallback'), 'fallback');
    assert.equal(f.client.text(undefined, undefined, 'fallback'), 'fallback');
    assert.equal(f.client.optionLabel('allow'), '허용');
    assert.equal(f.client.optionLabel('deny'), 'deny');
  });
});

describe('openChat', () => {
  const ctxWith = (shown: string[], present = true) => ({toolWindows: present ? {show: (id: string) => (shown.push(id), true)} : undefined}) as unknown as PluginContext;
  it('대상을 남기고 chat 도구 창을 연다', () => {
    const shown: string[] = [];
    const target = {kind: 'run', runId: 'r7'} as const;
    assert.equal(openChat(ctxWith(shown), target), true);
    assert.deepEqual(shown, ['chat']);
    assert.equal(pendingChat(), target);
    takeChat(target);
    assert.equal(pendingChat(), undefined);
  });
  it('옛 Studio(toolWindows 없음)에서도 던지지 않고 false', () => {
    assert.equal(openChat(ctxWith([], false), {kind: 'compose', text: 'hi'}), false);
    requestChat({kind: 'run', runId: 'x'});
  });
  it('run은 그 실행, compose는 글·폴더를 채운 입력, work는 최근 실행을 찾고 없으면 이어가기 입력', async () => {
    const runs = [sampleRun({runId: 'old', workId: 'w1', startedAt: '2026-10-01T00:00:00Z'}), sampleRun({runId: 'new', workId: 'w1', startedAt: '2026-11-01T00:00:00Z'}), sampleRun({runId: 'other', workId: 'w2'})];
    const f = fakeClient(method => (method === 'daemon.listRuns' ? {runs} : {}));
    assert.equal(latestRunOfWork(runs, 'w1')?.runId, 'new');
    assert.deepEqual(await viewForTarget(f.client, {kind: 'run', runId: 'r1'}), {kind: 'run', runId: 'r1'});
    const compose = await viewForTarget(f.client, {kind: 'compose', text: 'fix it', cwd: '/p'});
    assert.ok(compose.kind === 'compose' && compose.form.request === 'fix it' && compose.form.cwd === '/p');
    assert.deepEqual(await viewForTarget(f.client, {kind: 'work', workId: 'w1'}), {kind: 'run', runId: 'new'});
    const missing = await viewForTarget(f.client, {kind: 'work', workId: 'w404', cwd: '/p'});
    assert.ok(missing.kind === 'compose' && missing.form.resumeWorkId === 'w404' && missing.form.cwd === '/p');
  });
});

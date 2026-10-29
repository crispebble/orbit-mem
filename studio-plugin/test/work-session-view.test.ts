// 세션 줄·호출 계보·사건 기록·이어 가기 안내 시험. 끝 기준 28(세션 대응·세션 칸 값)·31(확인 기다림 안내)·32(계보·관측 오래됨).
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet, parseSession} from '../src/work/contract.ts';
import {lineageOf, resumeGuide, sessionRowProps, timelineOf} from '../src/work/session-view.ts';
import type {Run, Session} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const NOW = Date.parse('2026-10-28T06:41:00.000Z');
const ended = parseSession(example('work_sessions.session.page1').result);
const task = parseGet(example('work_get.claude-task').result).work;
const session = (over: Partial<Session>): Session => ({
  sessionId: 'sess-1', host: 'claude', surface: 'cli', state: 'running', startedAt: '2026-10-28T05:12:00.000Z',
  lastSeenAt: '2026-10-28T06:40:30.000Z', openWorkIds: [], activeStepIds: [], ...over,
});

describe('sessionRowProps', () => {
  it('세션 상태 다섯 가지와 모르는 값', () => {
    const labels = ['running', 'waiting_user', 'idle', 'stale', 'ended', 'brand_new'].map(state => sessionRowProps(session({state}), new Map(), NOW).status);
    assert.deepEqual(labels, [
      {label: '도는 중', tone: 'accent'}, {label: '사용자 확인 기다림', tone: 'warning'}, {label: '쉬는 중', tone: 'neutral'},
      {label: '관측 오래됨', tone: 'warning'}, {label: '끝남', tone: 'neutral'}, {label: '알 수 없음', tone: 'neutral'},
    ]);
  });

  it('레포·브랜치·끝 사유는 세션 칸 값 그대로', () => {
    const p = sessionRowProps(ended.session, new Map(), NOW);
    assert.equal(p.host, 'Claude Code · 데스크톱');
    assert.deepEqual(p.place, {kind: 'repo', name: 'orbit', branch: 'main'});
    assert.equal(p.endReason, '세션 종료 · 입력으로 종료');
    assert.equal(p.muted, true);
    assert.equal(p.elapsed, '40분');
  });

  it('레포가 아니면 폴더 이름, 폴더도 모르면 세션 id 앞자리', () => {
    assert.deepEqual(sessionRowProps(session({cwd: '/Users/dev/notes'}), new Map(), NOW).place, {kind: 'folder', name: 'notes'});
    assert.deepEqual(sessionRowProps(session({}), new Map(), NOW).place, {kind: 'folder', name: 'sess-1'});
  });

  it('모르는 끝 사유·끝 자세한 글은 알 수 없음·원문', () => {
    assert.equal(sessionRowProps(session({state: 'ended', endReason: 'weird', endDetail: 'custom_exit'}), new Map(), NOW).endReason, '알 수 없음 · custom_exit');
  });

  it('지금 하는 단계: 첫 단계 제목과 나머지 수, 제목을 모르면 개수', () => {
    const titles = new Map([['s1', '그래프 계산']]);
    assert.equal(sessionRowProps(session({activeStepIds: ['s1', 's2', 's3']}), titles, NOW).step, '그래프 계산 외 2');
    assert.equal(sessionRowProps(session({activeStepIds: ['x', 'y']}), titles, NOW).step, '단계 2개');
    assert.equal(sessionRowProps(session({}), titles, NOW).step, undefined);
  });

  it('관측 오래됨은 Orbit이 stale로 판정했을 때만 마지막 관측 시각을 보인다', () => {
    assert.equal(sessionRowProps(session({state: 'running', lastSeenAt: '2026-10-28T05:00:00.000Z'}), new Map(), NOW).lastSeen, undefined);
    assert.equal(sessionRowProps(session({state: 'stale', lastSeenAt: '2026-10-28T06:02:00.000Z'}), new Map(), NOW).lastSeen, '마지막 관측 39분 전');
  });
});

describe('lineageOf', () => {
  const root = session({sessionId: task.origin!.sessionId, host: 'claude', surface: 'cli'});
  const tree = lineageOf(root, task.runs, NOW);

  it('메인 세션 아래에 parentSessionId로 이은 실행, 시작 순', () => {
    assert.equal(tree.label, '메인 세션 · Claude Code');
    assert.deepEqual(tree.children.map(c => c.label), ['orbit:worker', 'orbit:gate', 'orbit:reviewer', '별도 세션 · Codex', 'orbit:worker', 'orbit:gate']);
  });

  it('실행 상태 글, 뒤에서 도는 실행 표시, 끝난 것 흐리게', () => {
    const codex = tree.children[3];
    assert.deepEqual([codex.detail, codex.background, codex.ended, codex.sessionId], ['도는 중', true, false, '019a2b7c-55e1-7c30-9d42-6f1e0a8b3c27']);
    const failed = tree.children[4];
    assert.deepEqual([failed.detail, failed.background, failed.ended], ['실패', true, true]);
  });

  it('역할이 없는 서브에이전트는 종류 미확인, 모르는 실행 종류는 알 수 없음', () => {
    const runs: Run[] = [
      {id: 'r1', kind: 'subagent', host: 'claude', link: 'owner', state: 'finished', startedAt: '2026-10-28T06:00:00.000Z', parentSessionId: 'sess-1'},
      {id: 'r2', kind: 'robot', host: 'claude', link: 'owner', state: 'odd', startedAt: '2026-10-28T06:00:00.000Z', parentSessionId: 'sess-1'},
    ];
    const t = lineageOf(session({}), runs, NOW);
    assert.deepEqual(t.children.map(c => [c.label, c.detail]), [['서브에이전트 · 종류 미확인', '끝남'], ['알 수 없음', '알 수 없음']]);
  });

  it('별도 세션 아래의 실행도 이어 그린다', () => {
    const runs: Run[] = [
      {id: 'child', kind: 'session', host: 'codex', sessionId: 'sess-2', parentSessionId: 'sess-1', link: 'parent_env', state: 'finished', startedAt: '2026-10-28T06:00:00.000Z'},
      {id: 'grand', kind: 'subagent', host: 'codex', role: 'explorer', sessionId: 'sess-2', parentSessionId: 'sess-2', link: 'owner', state: 'finished', startedAt: '2026-10-28T06:00:00.000Z'},
    ];
    const t = lineageOf(session({}), runs, NOW);
    assert.deepEqual(t.children.map(c => [c.label, c.children.map(g => g.label)]), [['별도 세션 · Codex', ['explorer']]]);
  });
});

describe('timelineOf', () => {
  it('사건 종류를 글로, 모르는 종류는 알 수 없음, 시각은 원문을 dateTime에', () => {
    const items = timelineOf([...ended.events, {seq: '9', at: '2026-10-28T03:39:00.000Z', kind: 'brand.new'}], new Map(), 'Asia/Seoul');
    assert.equal(items[0].title, '세션 시작');
    assert.equal(items[0].time, '12:00');
    assert.equal(items[0].dateTime, ended.events[0].at);
    assert.equal(items.at(-1)!.title, '알 수 없음');
  });
});

describe('resumeGuide', () => {
  it('CLI 세션만 터미널 이어 가기 명령을 준다', () => {
    assert.equal(resumeGuide({host: 'claude', surface: 'cli', sessionId: 'abc'}).command, 'claude --resume abc');
    assert.equal(resumeGuide({host: 'codex', surface: 'cli', sessionId: 'abc'}).command, 'codex resume abc');
    assert.equal(resumeGuide({host: 'claude', surface: 'desktop', sessionId: 'abc'}).command, undefined);
    assert.equal(resumeGuide({host: 'gemini', surface: 'cli', sessionId: 'abc'}).command, undefined);
  });
  it('세션 id가 명령 글자로 안전하지 않으면 명령을 주지 않는다', () => {
    assert.equal(resumeGuide({host: 'claude', surface: 'cli', sessionId: 'a; rm -rf ~'}).command, undefined);
    // 옵션으로 읽힐 수 있는 id(-로 시작)도 받지 않는다
    assert.equal(resumeGuide({host: 'claude', surface: 'cli', sessionId: '-x'}).command, undefined);
  });
  it('디렉토리를 모르면 명령을 주지 않는다(다른 디렉토리에서는 이어 갈 대화를 찾지 못한다)', () => {
    assert.equal(resumeGuide({host: 'claude', surface: 'cli', sessionId: 'abc'}, {needCwd: true}).command, undefined);
    assert.equal(resumeGuide({host: 'claude', surface: 'cli', sessionId: 'abc', cwd: '/repo'}, {needCwd: true}).command, 'claude --resume abc');
  });
  it('화면 종류 글', () => {
    assert.deepEqual(['cli', 'desktop', 'headless', 'unknown', 'x'].map(s => resumeGuide({host: 'claude', surface: s, sessionId: 'a'}).surface), ['CLI', '데스크톱', '백그라운드', '알 수 없음', '알 수 없음']);
  });
});

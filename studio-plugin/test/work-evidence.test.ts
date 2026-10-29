// 단계 상세의 증거·실행·세션으로 가는 길 시험. 끝 기준 28(검증 배지)·31(확인 기다림 세션)·32(stepId로만 잇기).
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet} from '../src/work/contract.ts';
import {commitsOf, evidenceKindLabel, evidenceOfStep, evidenceResultView, runLabelOf, waitingSessionOf} from '../src/work/evidence.ts';
import type {Session} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const id = (title: string) => task.steps.find(s => s.title === title)!.id;

describe('증거', () => {
  it('종류·결과 글과 톤, 모르는 값은 알 수 없음', () => {
    assert.deepEqual(['command', 'commit', 'verdict', 'x'].map(evidenceKindLabel), ['명령', '커밋', '판정', '알 수 없음']);
    assert.deepEqual(['ok', 'fail', 'pass', 'needs_fix', 'needs_user', 'x'].map(evidenceResultView), [
      {label: '성공', tone: 'success'}, {label: '실패', tone: 'danger'}, {label: '통과', tone: 'success'},
      {label: '수정 요구', tone: 'danger'}, {label: '사용자 확인 필요', tone: 'warning'}, {label: '알 수 없음', tone: 'neutral'},
    ]);
  });

  it('단계의 증거는 그 단계에 묶인 것만, 시각 순', () => {
    const list = evidenceOfStep(task, id('기록 DB 원장 만들기'));
    assert.deepEqual(list.map(e => e.kind), ['command', 'verdict', 'commit']);
    assert.ok(list.every(e => e.stepId === id('기록 DB 원장 만들기')));
  });

  it('커밋 산출물은 repo·sha가 있는 커밋 증거', () => {
    const commits = commitsOf(evidenceOfStep(task, id('기록 DB 원장 만들기')));
    assert.deepEqual(commits.map(c => [c.repo, c.sha.slice(0, 7), c.files.length]), [['/Users/dev/repo/orbit', '3b9e0c1', 2]]);
  });

  it('실행 이름: 역할·별도 세션·종류 미확인', () => {
    assert.equal(runLabelOf(task.runs[0]), 'orbit:gate');
    assert.equal(runLabelOf(task.runs.at(-1)!), '별도 세션 · Codex');
    assert.equal(runLabelOf({...task.runs[0], role: undefined}), '서브에이전트 · 종류 미확인');
  });
});

describe('waitingSessionOf', () => {
  it('별도 세션 실행이 그 단계를 맡았으면 그 세션, 화면 종류는 세션 칸에서', () => {
    const sessions: Session[] = [{
      sessionId: '019a2b7c-55e1-7c30-9d42-6f1e0a8b3c27', host: 'codex', surface: 'cli', state: 'waiting_user', cwd: '/x',
      startedAt: '2026-10-28T06:00:00.000Z', lastSeenAt: '2026-10-28T06:40:00.000Z', openWorkIds: [], activeStepIds: [],
    }];
    const w = waitingSessionOf(task, id('그래프 계산'), sessions);
    assert.deepEqual(w, {host: 'codex', surface: 'cli', sessionId: '019a2b7c-55e1-7c30-9d42-6f1e0a8b3c27', cwd: '/Users/dev/repo/orbit/.claude/worktrees/graph'});
  });

  it('아니면 작업을 연 세션(origin), 디렉토리는 그 세션 칸 값이 먼저(이어 가기는 세션을 만든 디렉토리에서)', () => {
    assert.deepEqual(waitingSessionOf(task, id('훅 연결'), []), {host: 'claude', surface: 'cli', sessionId: task.origin!.sessionId, cwd: task.cwd});
    const own: Session = {
      sessionId: task.origin!.sessionId, host: 'claude', surface: 'cli', state: 'waiting_user', cwd: '/Users/dev/repo',
      startedAt: '2026-10-28T06:00:00.000Z', lastSeenAt: '2026-10-28T06:40:00.000Z', openWorkIds: [], activeStepIds: [],
    };
    assert.equal(waitingSessionOf(task, id('훅 연결'), [own])?.cwd, '/Users/dev/repo');
  });

  it('세션 칸이 없고 실행에도 화면 종류가 없으면 알 수 없음', () => {
    assert.equal(waitingSessionOf(task, id('그래프 계산'), [])?.surface, 'unknown');
  });

  it('origin도 없으면 없음', () => {
    assert.equal(waitingSessionOf({...task, origin: undefined}, id('훅 연결'), []), undefined);
  });
});

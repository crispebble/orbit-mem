// 대화 창의 순수 로직: transcript 축약(seq 정렬·중복 제거·끊긴 뒤 이어 붙임), 계획 병렬 묶음, 열린 카드, 합계, 조작 조건.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import type {DaemonEvent, RunSummary} from '../src/daemon/api.ts';
import {planBatches} from '../src/chat/plan.ts';
import {buildTimeline, canResume, canStop, lastSeq, mergeEvents, openDecisions, runState, runTotals, sortRuns} from '../src/chat/timeline.ts';

let n = 0;
const ev = (seq: number, event: Record<string, unknown>, runId = 'r1'): DaemonEvent => ({seq, runId, type: String(event.type), at: `2026-11-01T00:00:${String(seq).padStart(2, '0')}Z`, event: {at: `2026-11-01T00:00:${String(seq).padStart(2, '0')}Z`, ...event} as DaemonEvent['event']});

describe('mergeEvents', () => {
  it('seq로 정렬하고 같은 seq는 한 번만 센다', () => {
    const a = [ev(1, {type: 'run.started'}), ev(3, {type: 'warning', message: 'c'})];
    const merged = mergeEvents(a, [ev(2, {type: 'warning', message: 'b'}), ev(3, {type: 'warning', message: 'c'}), ev(1, {type: 'run.started'})]);
    assert.deepEqual(merged.map(e => e.seq), [1, 2, 3]);
  });
  it('끊겼다 다시 와도 빠짐·겹침이 없다(앞 조각 + 겹치는 뒤 조각)', () => {
    const first = mergeEvents([], [ev(1, {type: 'run.started'}), ev(2, {type: 'warning', message: 'x'})]);
    const second = mergeEvents(first, [ev(2, {type: 'warning', message: 'x'}), ev(3, {type: 'warning', message: 'y'}), ev(4, {type: 'warning', message: 'z'})]);
    assert.deepEqual(second.map(e => e.seq), [1, 2, 3, 4]);
    assert.equal(lastSeq(second), 4);
  });
  it('바뀐 게 없으면 같은 배열을 돌려준다(다시 그리지 않게)', () => {
    const a = [ev(1, {type: 'run.started'})];
    assert.equal(mergeEvents(a, [a[0]]), a);
  });
  it('다른 실행의 이벤트는 버린다', () => {
    const merged = mergeEvents([], [ev(1, {type: 'warning', message: 'a'}, 'other'), ev(2, {type: 'warning', message: 'b'})], 'r1');
    assert.deepEqual(merged.map(e => e.seq), [2]);
  });
});

describe('planBatches', () => {
  it('선행 깊이로 묶고 같은 묶음은 계획 순서를 지킨다', () => {
    const steps = [{id: 'a', after: []}, {id: 'b', after: []}, {id: 'c', after: ['a', 'b']}, {id: 'd', after: ['c']}, {id: 'e', after: ['a']}];
    assert.deepEqual(planBatches(steps), [['a', 'b'], ['c', 'e'], ['d']]);
  });
  it('모르는 선행은 무시하고 순환은 끊는다', () => {
    assert.deepEqual(planBatches([{id: 'a', after: ['zzz']}]), [['a']]);
    const cyc = planBatches([{id: 'a', after: ['b']}, {id: 'b', after: ['a']}]);
    assert.equal(cyc.flat().length, 2);
  });
  it('빈 계획', () => assert.deepEqual(planBatches([]), []));
});

describe('buildTimeline', () => {
  const events = [
    ev(1, {type: 'run.started', request: 'do it', cwd: '/w', profile: 'p', mode: 'ultrawork', resume: false, limits: {}, listId: 'l'}),
    ev(2, {type: 'plan.proposed', attempt: 1, plan: {goal: 'g', steps: [{id: 's1', title: 'One', role: 'worker', after: [], done: 'd'}, {id: 's2', title: 'Two', role: 'qa', after: ['s1'], done: 'd', verify: 'npm test'}]}}),
    ev(3, {type: 'step.started', step: 's1', title: 'One', role: 'worker', engine: 'claude', model: 'haiku', attempt: 1}),
    ev(4, {type: 'step.output', step: 's1', attempt: 1, text: 'hel'}),
    ev(5, {type: 'step.output', step: 's1', attempt: 1, text: 'lo'}),
    ev(6, {type: 'step.finished', step: 's1', role: 'worker', engine: 'claude', model: 'haiku', attempt: 1, status: 'done', inputTokens: 10, outputTokens: 5, costUsd: 0.5, turns: 2}),
    ev(7, {type: 'model.skipped', role: 'worker', ref: 'pi:openai/x', reason: 'no-key'}),
    ev(8, {type: 'verify.result', step: 's1', attempt: 1, command: 'npm test', ok: false, outputTail: 'a\nb'}),
    ev(9, {type: 'commit.found', step: 's1', sha: 'abcdef1234', subject: 'feat: x'}),
    ev(10, {type: 'future.unknown', foo: 1}),
    ev(11, {type: 'decision.asked', id: 'd1', title: 'Approve?', options: ['yes', 'no'], kind: 'question', key: 'decision.plan-approval', args: ['g']}),
    ev(12, {type: 'decision.asked', id: 'd2', title: 'Run rm?', options: ['allow', 'deny'], kind: 'tool', context: 'Bash: rm'}),
    ev(13, {type: 'decision.answered', id: 'd1', answer: 'yes'}),
  ];
  const entries = buildTimeline(events);
  it('단계 시작·출력·끝을 한 항목으로 합치고 모르는 type은 건너뛴다', () => {
    const kinds = entries.map(e => e.kind);
    assert.deepEqual(kinds, ['started', 'plan', 'step', 'skipped', 'verify', 'commit', 'decision', 'decision']);
    const step = entries.find(e => e.kind === 'step');
    assert.ok(step && step.kind === 'step');
    assert.equal(step.output, 'hello');
    assert.equal(step.status, 'done');
    assert.equal(step.costUsd, 0.5);
    assert.equal(step.seq, 3);
  });
  it('계획 항목에 병렬 묶음이 들어 있다', () => {
    const plan = entries.find(e => e.kind === 'plan');
    assert.ok(plan && plan.kind === 'plan');
    assert.deepEqual(plan.batches, [['s1'], ['s2']]);
  });
  it('열린 카드는 decision.answered가 없는 것뿐이다', () => {
    const open = openDecisions(entries);
    assert.deepEqual(open.map(d => d.id), ['d2']);
    assert.equal(open[0].decisionKind, 'tool');
    assert.equal(open[0].context, 'Bash: rm');
    const answered = entries.find(e => e.kind === 'decision' && e.id === 'd1');
    assert.ok(answered && answered.kind === 'decision' && answered.answered && answered.answer === 'yes');
  });
  it('같은 이벤트를 두 번 넣어도(seq 중복) 같은 결과', () => {
    const doubled = mergeEvents(mergeEvents([], events), events);
    assert.deepEqual(buildTimeline(doubled).map(e => e.kind), entries.map(e => e.kind));
  });
  it('시작 이벤트 없이 끝만 와도 단계 항목이 생긴다', () => {
    const e = buildTimeline([ev(1, {type: 'step.finished', step: 'x', role: 'worker', engine: 'codex', attempt: 1, status: 'failed', inputTokens: 1, outputTokens: 1, turns: 1})]);
    assert.equal(e.length, 1);
    assert.equal(e[0].kind, 'step');
  });
});

const summary = (over: Partial<RunSummary> = {}): RunSummary => ({
  runId: 'r1', request: 'q', cwd: '/w', profile: 'p', mode: 'default', state: 'running', reason: '', exitCode: 0, workId: 'w1', message: '', startedAt: '2026-11-01T00:00:00Z', finishedAt: '',
  totals: {costUsd: 0, subscription: false, turns: 0, minutes: 0, inputTokens: 0, outputTokens: 0, monthSpentUsd: 3, monthLimitUsd: 10}, openDecisions: [], source: 'studio', device: '', lastSeq: 0, sessionId: '', ...over,
});

describe('상태·합계·조작', () => {
  it('끝 이벤트가 있으면 그 상태, 없으면 요약(interrupted), 둘 다 없으면 running', () => {
    const fin = buildTimeline([ev(1, {type: 'run.finished', status: 'failed', exitCode: 1, message: 'm', totals: {costUsd: 2, subscription: false, turns: 4, minutes: 3, inputTokens: 7, outputTokens: 8}})]);
    assert.equal(runState(fin, summary()), 'failed');
    assert.equal(runState([], summary({state: 'interrupted'})), 'interrupted');
    assert.equal(runState([], undefined), 'running');
    assert.deepEqual({c: runTotals(fin).costUsd, t: runTotals(fin).turns, i: runTotals(fin).inputTokens}, {c: 2, t: 4, i: 7});
  });
  it('끝 전에는 단계를 합산하고 월 사용량은 요약에서', () => {
    const entries = buildTimeline([
      ev(1, {type: 'step.finished', step: 'a', role: 'w', engine: 'claude', attempt: 1, status: 'done', inputTokens: 10, outputTokens: 5, costUsd: 0.25, turns: 2}),
      ev(2, {type: 'step.finished', step: 'b', role: 'w', engine: 'claude', attempt: 1, status: 'done', inputTokens: 1, outputTokens: 1, costUsd: 0.25, turns: 3}),
    ]);
    const t = runTotals(entries, summary());
    assert.equal(t.costUsd, 0.5);
    assert.equal(t.turns, 5);
    assert.equal(t.inputTokens, 11);
    assert.equal(t.monthSpentUsd, 3);
    assert.equal(t.monthLimitUsd, 10);
  });
  it('멈춤은 진행 중에만, 이어가기는 멈춤·끊김·실패이고 workId가 있을 때만', () => {
    assert.ok(canStop('running'));
    assert.ok(!canStop('completed'));
    for (const s of ['stopped', 'interrupted', 'failed']) assert.ok(canResume(s, 'w1'), s);
    for (const s of ['running', 'completed', 'planned']) assert.ok(!canResume(s, 'w1'), s);
    assert.ok(!canResume('failed', ''));
    assert.ok(!canResume('failed', undefined));
  });
  it('실행 목록은 진행 중이 먼저, 그다음 최근 순', () => {
    const runs = [summary({runId: 'old', state: 'completed', startedAt: '2026-10-01T00:00:00Z'}), summary({runId: 'new', state: 'completed', startedAt: '2026-11-01T00:00:00Z'}), summary({runId: 'live', state: 'running', startedAt: '2026-09-01T00:00:00Z'})];
    assert.deepEqual(sortRuns(runs).map(r => r.runId), ['live', 'new', 'old']);
  });
});

void n;

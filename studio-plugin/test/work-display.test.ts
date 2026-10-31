// 표시 모델 단위 시험. 끝 기준 29(상태 표시 대응)·32(지금 수행·관측 오래됨)·33(하위 작업 묶기·시각)에 해당한다.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {
  blockText, formatTime, groupWorks, isRunningNow, layoutStatus, progressOf, RUN_FRESH_MS, runStateView,
  sessionStateView, stepRuns, stepStatusView, verificationBadge, workStateView,
} from '../src/work/display.ts';
import {setLocale} from '../src/i18n/index.ts';
import {parseGet, parseList} from '../src/work/contract.ts';
import {defaultList, dismiss, PENDING_MAX, updatePending} from '../src/work/review.ts';
import {runningWorksData} from '../src/work/widgets.ts';
import {inScope, summaryOf} from '../src/work/store.ts';
import type {Run, WorkSummary} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const NOW = Date.parse('2026-10-28T06:41:00.000Z');
const run = (over: Partial<Run>): Run => ({id: 'r', kind: 'subagent', host: 'claude', link: 'owner', state: 'running', startedAt: '2026-10-28T06:00:00.000Z', ...over});

describe('상태 표시 대응', () => {
  it('색조는 Forma Badge·FlowGraph 톤 이름만 쓴다(info→accent, muted→neutral)', () => {
    const forma = new Set(['neutral', 'accent', 'success', 'warning', 'danger', 'violet']);
    const tones = [
      ...['pending', 'ready', 'in_progress', 'blocked', 'done', 'cancelled', 'x'].map(s => stepStatusView(s).tone),
      ...['waiting_user', 'run_failed', 'run_lost', 'x'].map(r => stepStatusView('blocked', {reason: r, since: ''}).tone),
      ...['running', 'waiting_user', 'idle', 'stale', 'ended', 'x'].map(s => sessionStateView(s).tone),
      ...['running', 'finished', 'failed', 'lost', 'x'].map(s => runStateView(run({state: s, lastSeenAt: '2026-10-28T06:40:00.000Z'}), NOW).tone),
      workStateView({state: 'open'}, false).tone, workStateView({state: 'done'}, true).tone, workStateView({state: 'done'}, false).tone,
    ];
    assert.deepEqual(tones.filter(t => !forma.has(t)), []);
    assert.equal(stepStatusView('in_progress').tone, 'accent');
    assert.equal(stepStatusView('pending').tone, 'neutral');
  });

  it('단계 상태 여섯 가지와 모르는 값(설계 「상태 표시 대응」)', () => {
    assert.deepEqual(['pending', 'ready', 'in_progress', 'blocked', 'done', 'cancelled'].map(s => stepStatusView(s).label), ['Pending', 'Ready', 'In progress', 'Blocked', 'Done', 'Cancelled']);
    assert.equal(stepStatusView('brand_new').label, 'Unknown');
    assert.equal(stepStatusView('brand_new').known, false);
    assert.equal(layoutStatus('brand_new'), 'pending');
    assert.equal(layoutStatus('done'), 'done');
  });

  it('막힌 단계는 사유로 가른다: 사용자 확인 기다림, 실패(run_failed만), 그 밖은 막힘, 모르는 사유는 알 수 없음', () => {
    const view = (reason: string) => stepStatusView('blocked', {reason, since: ''});
    assert.deepEqual([view('waiting_user').label, view('waiting_user').tone], ['Waiting for user confirmation', 'warning']);
    assert.deepEqual([view('run_failed').label, view('run_failed').tone], ['Failed', 'danger']);
    assert.deepEqual(['run_lost', 'interrupted', 'stale', 'declared'].map(r => view(r).label), ['Blocked', 'Blocked', 'Blocked', 'Blocked']);
    assert.deepEqual([view('brand_new').label, view('brand_new').known], ['Unknown', false]);
    assert.equal(stepStatusView('blocked').label, 'Blocked');
  });

  it('세션 상태 다섯 가지와 모르는 값', () => {
    assert.deepEqual(['running', 'waiting_user', 'idle', 'stale', 'ended'].map(s => sessionStateView(s).label), ['Running', 'Waiting for user confirmation', 'Idle', 'Stale observation', 'Ended']);
    assert.equal(sessionStateView('x').label, 'Unknown');
  });

  it('실행 상태 네 가지, 오래된 running은 관측 오래됨', () => {
    assert.equal(runStateView(run({state: 'failed'}), NOW).label, 'Failed');
    assert.equal(runStateView(run({state: 'lost'}), NOW).label, 'Disconnected');
    assert.equal(runStateView(run({state: 'finished'}), NOW).label, 'Finished');
    assert.equal(runStateView(run({lastSeenAt: new Date(NOW - 1000).toISOString()}), NOW).label, 'Running');
    assert.equal(runStateView(run({lastSeenAt: new Date(NOW - RUN_FRESH_MS - 1).toISOString()}), NOW).label, 'Stale observation');
  });

  it('작업 상태: 끝남 · 확인 전', () => {
    assert.equal(workStateView({state: 'open'}, false).label, 'Active');
    assert.equal(workStateView({state: 'done'}, true).label, 'Done · not reviewed');
    assert.equal(workStateView({state: 'done'}, false).label, 'Done');
    assert.equal(workStateView({state: 'zzz'}, false).label, 'Unknown');
  });

  it('화면 문구에 Work를 쓰지 않는다', () => {
    const all = [
      ...['pending', 'ready', 'in_progress', 'blocked', 'done', 'cancelled', 'x'].map(s => stepStatusView(s).label),
      ...['running', 'waiting_user', 'idle', 'stale', 'ended'].map(s => sessionStateView(s).label),
      ...['waiting_user', 'run_failed', 'run_lost', 'declared', 'interrupted', 'stale'].map(r => blockText({reason: r, since: ''})),
      workStateView({state: 'done'}, true).label,
    ];
    assert.ok(all.every(t => !/work/i.test(t)));
  });
});

describe('한국어로 바꾸면', () => {
  it('상태 글과 시각이 한국어로 나온다', () => {
    setLocale('ko');
    try {
      assert.equal(stepStatusView('in_progress').label, '진행 중');
      assert.equal(stepStatusView('blocked', {reason: 'waiting_user', since: ''}).label, '사용자 확인 기다림');
      assert.equal(workStateView({state: 'done'}, true).label, '끝남 · 확인 전');
      assert.equal(formatTime('2026-10-28T05:12:30.123Z', 'Asia/Seoul'), '10월 28일 14:12');
      assert.equal(blockText({reason: 'run_lost', since: ''}), '실행 끊김');
    } finally {
      setLocale('en');
    }
  });
});

describe('검증 배지와 막힘 사유', () => {
  it('검증 결과는 배지로만 붙는다: 끝난 단계의 실패는 「끝남 · 검증 실패」, 끝나지 않은 단계는 「검증 실패」, 통과·없음은 배지 없음', () => {
    assert.equal(verificationBadge({state: 'none', outdated: false}, 'done'), undefined);
    assert.equal(verificationBadge({state: 'pass', outdated: false}, 'done'), undefined);
    assert.deepEqual([verificationBadge({state: 'fail', outdated: false}, 'done')?.label, verificationBadge({state: 'fail', outdated: false}, 'done')?.tone], ['Done · verification failed', 'danger']);
    assert.equal(verificationBadge({state: 'fail', outdated: false}, 'in_progress')?.label, 'Verification failed');
    assert.equal(verificationBadge({state: 'needs_user', outdated: false}, 'done')?.label, 'Needs user confirmation');
    assert.deepEqual([verificationBadge({state: 'brand_new', outdated: false}, 'done')?.label], ['Unknown']);
  });

  it('지난 검증(outdated)은 「오래됨」으로 흐리게 보인다', () => {
    const b = verificationBadge({state: 'fail', outdated: true}, 'done');
    assert.deepEqual([b?.label, b?.tone, b?.outdated], ['Outdated', 'neutral', true]);
    // 통과도 뒤에 커밋이 오면 지난 검증이다
    assert.equal(verificationBadge({state: 'pass', outdated: true}, 'done')?.label, 'Outdated');
    assert.equal(verificationBadge({state: 'none', outdated: true}, 'done'), undefined);
  });

  it('예시: done 단계에 fail 검증이 붙어도 단계 상태는 done 그대로', () => {
    const s = task.steps.find(x => x.id === 'st-2b3c4d5e6f70');
    assert.equal(stepStatusView(s?.status ?? '').label, 'Done');
    assert.equal(verificationBadge(s?.verification, s?.status ?? '')?.outdated, true);
  });

  it('막힘 사유 문구와 모르는 사유', () => {
    assert.equal(blockText({reason: 'waiting_user', detail: '배포해도 될까요?', since: ''}), 'Waiting for user confirmation: 배포해도 될까요?');
    assert.deepEqual(['run_failed', 'run_lost', 'interrupted', 'stale', 'declared'].map(reason => blockText({reason, since: ''})), ['Failed while running', 'Run disconnected', 'Interrupted', 'Stalled', 'Reported as blocked']);
    assert.equal(blockText({reason: 'new_reason', since: ''}), 'Unknown');
  });
});

describe('지금 수행', () => {
  it('running이어도 lastSeenAt이 오래되면 지금 수행이 아니다', () => {
    assert.equal(isRunningNow(run({lastSeenAt: new Date(NOW - 60_000).toISOString()}), NOW), true);
    assert.equal(isRunningNow(run({lastSeenAt: new Date(NOW - RUN_FRESH_MS - 1000).toISOString()}), NOW), false);
    assert.equal(isRunningNow(run({state: 'finished', lastSeenAt: new Date(NOW).toISOString()}), NOW), false);
  });

  it('단계 수행은 실행의 stepId로만 잇는다(부모 세션 실행·다른 단계 실행은 세지 않음)', () => {
    const fresh = new Date(NOW - 1000).toISOString();
    const work = {runs: [
      run({id: 'mine', stepId: 's1', lastSeenAt: fresh}),
      run({id: 'parent', kind: 'session', lastSeenAt: fresh}), // stepId 없음: 부모 세션
      run({id: 'other', stepId: 's2', lastSeenAt: fresh}),
      run({id: 'old', stepId: 's1', lastSeenAt: new Date(NOW - RUN_FRESH_MS - 1).toISOString()}),
    ]};
    const r = stepRuns(work, 's1', NOW);
    assert.deepEqual(r.active.map(x => x.id), ['mine']);
    assert.deepEqual(r.observationOld.map(x => x.id), ['old']);
    assert.deepEqual(stepRuns(work, 's9', NOW), {active: [], observationOld: []});
  });

  it('예시: 진행 중 단계는 session 실행(stepId 있음)이 잇는다', () => {
    const t = Date.parse('2026-10-28T06:40:30.000Z');
    assert.equal(stepRuns(task, 'st-3c4d5e6f7081', t).active.length, 1);
    assert.equal(stepRuns(task, 'st-5e6f708192a3', t).active.length, 0);
  });
});

describe('하위 작업 묶기·진행·시각', () => {
  it('하위 작업은 상위 아래로, 상위가 없으면 최상위', () => {
    const items = parseList(example('work_list.page1').result).items;
    const tree = groupWorks(items);
    const parent = tree.find(n => n.work.id === 'w-7d2a91c04e5b');
    assert.deepEqual(parent?.children.map(c => c.work.id), ['w-0c5e8f2a9d14']);
    assert.equal(tree.some(n => n.work.id === 'w-0c5e8f2a9d14'), false);
    const orphan = groupWorks(items.filter(i => i.id !== 'w-7d2a91c04e5b'));
    assert.ok(orphan.some(n => n.work.id === 'w-0c5e8f2a9d14'));
  });

  it('순환이 와도 무한히 돌지 않는다', () => {
    const a = {id: 'a', updatedAt: '2026-01-01T00:00:00.000Z', parent: {workId: 'b'}};
    const b = {id: 'b', updatedAt: '2026-01-02T00:00:00.000Z', parent: {workId: 'a'}};
    const tree = groupWorks([a, b]);
    assert.equal(tree.length >= 1, true);
  });

  it('진행 끝난 단계/전체(취소 제외)', () => {
    const w = parseList(example('work_list.page1').result).items[0];
    const p = progressOf(w);
    assert.equal(p.total, w.stepCount - w.counts.cancelled);
    assert.equal(p.done, w.counts.done);
  });

  it('시각은 시간대에 맞춰 보인다', () => {
    assert.equal(formatTime('2026-10-28T05:12:30.123Z', 'Asia/Seoul'), 'Oct 28, 14:12');
    assert.equal(formatTime('2026-10-28T05:12:30.123Z', 'UTC'), 'Oct 28, 05:12');
    assert.equal(formatTime('이상한 값'), '이상한 값');
    assert.equal(typeof formatTime('2026-10-28T05:12:30.123Z'), 'string'); // PC 시간대
  });
});

describe('확인 전·정리', () => {
  const open = (id: string) => ({id, state: 'open'}) as WorkSummary;
  const done = (id: string) => ({id, state: 'done'}) as WorkSummary;

  it('보던 중 끝난 작업은 확인 전으로 남고, 처음부터 끝나 있던 작업은 올리지 않는다', () => {
    const known = new Map([['a', 'open']]);
    assert.deepEqual(updatePending([], known, [done('a'), done('old')]), ['a']);
  });

  it('다시 열리면 내린다. 이번에 읽지 않은 것은 유지한다(reset 뒤에도 표시 유지)', () => {
    assert.deepEqual(updatePending(['a', 'b'], new Map(), [open('a')]), ['b']);
    assert.deepEqual(updatePending(['a', 'b'], new Map(), []), ['a', 'b']);
  });

  it('목록에서 정리하면 기본 목록에서 빠지고, 기본 목록은 열린 것과 확인 전이다', () => {
    const works = [open('a'), done('b'), done('c')];
    assert.deepEqual(defaultList(works, ['b']).map(w => w.id), ['a', 'b']);
    assert.deepEqual(defaultList(works, dismiss(['b'], 'b')).map(w => w.id), ['a']);
  });

  it('쌓이는 양에 상한이 있다', () => {
    const many = Array.from({length: PENDING_MAX + 5}, (_, i) => `w${i}`);
    assert.equal(updatePending(many, new Map(), []).length, PENDING_MAX);
  });
});

describe('홈 위젯·범위', () => {

  it('진행 중 작업 데이터: 열린 작업 수·진행 끝난 단계/전체·확인 기다림', () => {
    const w = summaryOf(task);
    const d = runningWorksData([w, {...w, id: 'x', state: 'done'}], {[w.id]: task});
    assert.equal(d.open, 1);
    assert.equal(d.stepsDone, task.counts.done);
    assert.equal(d.stepsTotal, task.steps.length - task.counts.cancelled);
    assert.equal(d.waiting, 1);
  });

  it('범위: 경로 구분자 단위로 비교한다', () => {
    const p = {kind: 'project' as const, id: 'p', name: 'P', directories: ['/a/b']};
    assert.equal(inScope(p, '/a/b'), true);
    assert.equal(inScope(p, '/a/b/c'), true);
    assert.equal(inScope(p, '/a/bc'), false);
    assert.equal(inScope(p, undefined), false);
    assert.equal(inScope({kind: 'all'}, undefined), true);
  });

  it('summaryOf는 단계에서 목록 줄을 만든다', () => {
    const s = summaryOf(task);
    assert.equal(s.stepCount, 9);
    assert.equal(s.verification.pass + s.verification.fail + s.verification.needs_user + s.verification.none, 9);
    assert.equal(s.verification.outdated, 1);
  });
});

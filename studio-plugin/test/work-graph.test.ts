// 작업 → FlowGraph props 변환 시험. 끝 기준 23(wave 열·after 선·order 다른 모양)·27(모르는 값)·28(상태 대응).
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet} from '../src/work/contract.ts';
import {RUN_FRESH_MS} from '../src/work/display.ts';
import {activeColumnOf, assigneeText, hostLabel, neighbors, spanText, toFlowGraph} from '../src/work/graph.ts';
import type {Step, Work} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const NOW = Date.parse('2026-10-28T06:41:00.000Z');
const step = (over: Partial<Step>): Step => ({
  id: 's', title: '단계', order: 0, wave: 0, after: [], status: 'pending', verification: {state: 'none', outdated: false},
  evidenceIds: [], childWorkIds: [], ...over,
});
const work = (steps: Step[], runs: Work['runs'] = []): Pick<Work, 'steps' | 'runs'> => ({steps, runs});

describe('toFlowGraph', () => {
  const g = toFlowGraph(task, NOW);

  it('단계마다 카드 하나, 열은 계약의 wave 그대로', () => {
    assert.equal(g.nodes.length, task.steps.length);
    for (const s of task.steps) assert.equal(g.nodes.find(n => n.id === s.id)?.column, s.wave);
  });

  it('카드는 order 순이다(목록 보기가 order 순)', () => {
    const orders = g.nodes.map(n => task.steps.find(s => s.id === n.id)!.order);
    assert.deepEqual(orders, [...orders].sort((a, b) => a - b));
  });

  it('after: native는 실선, order는 점선에 「순서로 추정한 선행」', () => {
    const w = work([step({id: 'a'}), step({id: 'b', wave: 1, after: [{stepId: 'a', source: 'native'}]}), step({id: 'c', wave: 1, after: [{stepId: 'a', source: 'order'}]})]);
    const {edges} = toFlowGraph(w, NOW);
    assert.deepEqual(edges, [
      {from: 'a', to: 'b', variant: 'solid'},
      {from: 'a', to: 'c', variant: 'dashed', label: '순서로 추정한 선행'},
    ]);
  });

  it('모르는 선행 출처는 점선에 「알 수 없음」', () => {
    const {edges} = toFlowGraph(work([step({id: 'a'}), step({id: 'b', wave: 1, after: [{stepId: 'a', source: 'guess'}]})]), NOW);
    assert.deepEqual(edges, [{from: 'a', to: 'b', variant: 'dashed', label: '알 수 없음'}]);
  });

  it('열 머리는 「묶음 N」과 「끝 a/b」(취소 단계 빼고)', () => {
    const w = work([
      step({id: 'a', status: 'done'}), step({id: 'b', status: 'in_progress'}), step({id: 'x', status: 'cancelled'}),
      step({id: 'c', wave: 2, status: 'pending'}),
    ]);
    assert.deepEqual(toFlowGraph(w, NOW).columns, [
      {column: 0, title: '묶음 1', summary: '끝 1/2'},
      {column: 2, title: '묶음 3', summary: '끝 0/1'},
    ]);
  });

  it('상태는 표시 대응대로 글과 Forma 톤으로', () => {
    const byId = new Map(g.nodes.map(n => [n.id, n]));
    const of = (title: string) => byId.get(task.steps.find(s => s.title === title)!.id)!;
    assert.deepEqual(of('그래프 계산').status, {label: '진행 중', tone: 'accent'});
    assert.deepEqual(of('훅 연결').status, {label: '사용자 확인 기다림', tone: 'warning'});
    assert.deepEqual(of('MCP 도구').status, {label: '실패', tone: 'danger'});
    assert.deepEqual(of('옛 표식 방식 정리').status, {label: '취소', tone: 'neutral'});
  });

  it('진행 중이면 current, 취소·알 수 없음이면 muted, 그 밖은 강조 없음', () => {
    const w = work([
      step({id: 'p', status: 'in_progress'}), step({id: 'c', status: 'cancelled'}), step({id: 'u', status: 'brand_new'}),
      step({id: 'b', status: 'blocked', block: {reason: 'new_reason', since: '2026-10-28T06:00:00.000Z'}}), step({id: 'd', status: 'done'}),
    ]);
    const e = Object.fromEntries(toFlowGraph(w, NOW).nodes.map(n => [n.id, n.emphasis]));
    assert.deepEqual(e, {p: 'current', c: 'muted', u: 'muted', b: 'muted', d: undefined});
    assert.equal(toFlowGraph(w, NOW).nodes.find(n => n.id === 'u')?.status.label, '알 수 없음');
  });

  it('진행 중이면 진행 문구와 경과, 막힘이면 사유와 언제부터(칩과 같은 사유는 되풀이하지 않는다)', () => {
    const w = work([
      step({id: 'p', status: 'in_progress', activeTitle: '시험 고치는 중', startedAt: '2026-10-28T06:01:00.000Z'}),
      step({id: 'b', status: 'blocked', block: {reason: 'waiting_user', detail: 'Bash 권한 요청', since: '2026-10-28T06:38:00.000Z'}}),
    ]);
    const [p, b] = toFlowGraph(w, NOW).nodes;
    assert.deepEqual([p.description, p.meta], ['시험 고치는 중', '40분째']);
    assert.deepEqual([b.description, b.meta], ['Bash 권한 요청', '3분 전부터']);
    const [lost, failed] = toFlowGraph(work([
      step({id: 'l', status: 'blocked', block: {reason: 'run_lost', detail: '세션이 끊겼습니다', since: '2026-10-28T06:38:00.000Z'}}),
      step({id: 'f', status: 'blocked', block: {reason: 'run_failed', detail: '시험 실패', since: '2026-10-28T06:38:00.000Z'}}),
    ]), NOW).nodes;
    assert.deepEqual([lost.status.label, lost.description], ['막힘', '실행 끊김: 세션이 끊겼습니다']);
    assert.deepEqual([failed.status.label, failed.description], ['실패', '시험 실패']);
  });

  it('검증은 상태를 바꾸지 않고 배지로, 하위 작업은 보조 배지로', () => {
    const w = work([
      step({id: 'd', status: 'done', verification: {state: 'fail', outdated: false}, childWorkIds: ['w-1', 'w-2']}),
      step({id: 'n', status: 'in_progress', verification: {state: 'needs_user', outdated: false}}),
    ]);
    const [d, n] = toFlowGraph(w, NOW).nodes;
    assert.deepEqual(d.status, {label: '끝', tone: 'success'});
    assert.deepEqual(d.badges, [{label: '끝남 · 검증 실패', tone: 'danger'}, {label: '하위 작업 2', tone: 'violet'}]);
    assert.deepEqual(n.badges, [{label: '사용자 확인 필요', tone: 'warning'}]);
  });

  it('진행 중 단계의 실행이 모두 관측 오래됨이면 배지를 단다(부모 세션 실행은 세지 않는다)', () => {
    const old = new Date(NOW - RUN_FRESH_MS - 60_000).toISOString();
    const fresh = new Date(NOW - 60_000).toISOString();
    const base = {kind: 'subagent', host: 'claude', link: 'owner', state: 'running', startedAt: old};
    const w = work(
      [step({id: 'a', status: 'in_progress'}), step({id: 'b', status: 'in_progress'})],
      [
        {...base, id: 'r1', stepId: 'a', lastSeenAt: old},
        {...base, id: 'r2', stepId: 'b', lastSeenAt: old}, {...base, id: 'r3', stepId: 'b', lastSeenAt: fresh},
        {...base, id: 'parent', lastSeenAt: fresh}, // stepId 없음: 부모 세션 실행
      ],
    );
    const [a, b] = toFlowGraph(w, NOW).nodes;
    assert.deepEqual(a.badges, [{label: '관측 오래됨', tone: 'warning'}]);
    assert.deepEqual(b.badges, []);
  });

  it('담당은 칩으로: 종류·역할·호스트·모델', () => {
    const w = work([
      step({id: 'm', assignee: {kind: 'main', host: 'claude', model: 'opus'}}),
      step({id: 's', assignee: {kind: 'subagent', role: 'orbit:worker'}}),
      step({id: 'x', assignee: {kind: 'subagent'}}),
      step({id: 'n'}),
    ]);
    assert.deepEqual(toFlowGraph(w, NOW).nodes.map(n => n.chips), [['메인 · Claude Code · opus'], ['orbit:worker'], ['서브에이전트 · 종류 미확인'], []]);
  });

  it('진행 중 열: 진행 중 단계가 있는 첫 열, 없으면 끝나지 않은 단계가 있는 첫 열, 다 끝났으면 없음', () => {
    assert.equal(g.activeColumn, 1);
    assert.equal(activeColumnOf(work([step({id: 'a', status: 'done'}), step({id: 'b', wave: 1, status: 'ready'}), step({id: 'c', wave: 2, status: 'in_progress'})]).steps), 2);
    assert.equal(activeColumnOf(work([step({id: 'a', status: 'done'}), step({id: 'b', wave: 3, status: 'blocked'})]).steps), 3);
    assert.equal(activeColumnOf(work([step({id: 'a', status: 'done'}), step({id: 'b', status: 'cancelled'})]).steps), undefined);
  });
});

describe('표시 도움', () => {
  it('경과 글', () => {
    assert.deepEqual([spanText(0), spanText(59 * 60_000), spanText(60 * 60_000), spanText(89 * 60_000), spanText(26 * 3_600_000)], ['1분 미만', '59분', '1시간', '1시간 29분', '1일 2시간']);
  });
  it('호스트 이름: 계약의 claude·codex, 모르는 값은 알 수 없음', () => {
    assert.deepEqual([hostLabel('claude'), hostLabel('codex'), hostLabel('gemini'), hostLabel(undefined)], ['Claude Code', 'Codex', '알 수 없음', '알 수 없음']);
  });
  it('담당 글', () => {
    assert.equal(assigneeText({kind: 'session', host: 'codex'}), '별도 세션 · Codex');
    assert.equal(assigneeText({kind: 'robot'}), '알 수 없음');
    assert.equal(assigneeText(undefined), undefined);
  });
  it('선행·후행: 선행은 after 순서대로 출처와 함께, 후행은 이 단계를 after로 둔 단계', () => {
    const w = work([step({id: 'a', title: 'A'}), step({id: 'b', title: 'B', after: [{stepId: 'a', source: 'order'}, {stepId: 'gone', source: 'native'}]})]);
    const nb = neighbors(w.steps, 'b');
    assert.deepEqual(nb.before.map(x => [x.stepId, x.title, x.source]), [['a', 'A', 'order'], ['gone', undefined, 'native']]);
    assert.deepEqual(neighbors(w.steps, 'a').after.map(x => [x.stepId, x.title, x.source]), [['b', 'B', 'order']]);
  });
});

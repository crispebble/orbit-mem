// 「담당별」 보조 보기 시험: 단계를 assignee로 묶어 AgentOrchestrator workers로.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {parseGet} from '../src/work/contract.ts';
import {ownerWorkers, stepRunStatus} from '../src/work/owners.ts';
import type {Step} from '../src/work/types.ts';
import {example} from './work-fake.ts';

const task = parseGet(example('work_get.claude-task').result).work;
const step = (over: Partial<Step>): Step => ({
  id: 's', title: '단계', order: 0, wave: 0, after: [], status: 'pending', verification: {state: 'none', outdated: false},
  evidenceIds: [], childWorkIds: [], ...over,
});

describe('ownerWorkers', () => {
  it('같은 종류·역할·호스트는 한 묶음, 처음 나온 order 순', () => {
    const workers = ownerWorkers(task.steps);
    const total = workers.reduce((n, w) => n + (w.subtasks?.length ?? 0), 0);
    assert.equal(total, task.steps.length);
    const ids = workers.map(w => w.id);
    assert.equal(new Set(ids).size, ids.length);
  });

  it('이름은 담당 종류, 역할·호스트는 칩으로(이름에 겹쳐 쓰지 않는다)', () => {
    const workers = ownerWorkers([
      step({id: 'm', assignee: {kind: 'main', host: 'claude'}}),
      step({id: 's', assignee: {kind: 'subagent', role: 'orbit:reviewer', host: 'claude'}}),
      step({id: 'x', assignee: {kind: 'subagent'}}),
      step({id: 'c', assignee: {kind: 'session', host: 'codex'}}),
    ]);
    assert.deepEqual(workers.map(w => [w.name, w.role, w.host]), [
      ['Main session', undefined, 'Claude Code'], ['Subagent', 'orbit:reviewer', 'Claude Code'],
      ['Subagent · unknown type', undefined, undefined], ['Separate session', undefined, 'Codex'],
    ]);
  });

  it('진행률은 끝난 단계 / 취소 뺀 단계', () => {
    const [w] = ownerWorkers([
      step({id: 'a', status: 'done', assignee: {kind: 'subagent', role: 'orbit:worker'}}),
      step({id: 'b', status: 'in_progress', assignee: {kind: 'subagent', role: 'orbit:worker'}}),
      step({id: 'c', status: 'cancelled', assignee: {kind: 'subagent', role: 'orbit:worker'}}),
    ]);
    assert.equal(w.progress, 50);
    assert.equal(w.status, 'running');
    assert.equal(w.role, 'orbit:worker');
  });

  it('담당이 없는 단계는 「담당 정해지지 않음」', () => {
    assert.equal(ownerWorkers([step({})])[0].name, 'No owner assigned');
  });

  it('사용자 확인 기다림이 있으면 waiting 상태와 안내(승인·거절 동작 없음)', () => {
    const [w] = ownerWorkers([step({status: 'blocked', block: {reason: 'waiting_user', detail: 'Bash 권한', since: ''}, assignee: {kind: 'main', host: 'claude'}})]);
    assert.equal(w.status, 'waiting');
    assert.equal(w.waiting?.message, 'Waiting for user confirmation: Bash 권한');
    assert.equal(w.waiting?.onApprove, undefined);
    assert.equal(w.waiting?.onDeny, undefined);
  });
});

describe('stepRunStatus', () => {
  it('단계 상태를 RunStatus로', () => {
    const b = (reason: string) => step({status: 'blocked', block: {reason, since: ''}});
    assert.deepEqual(
      [step({status: 'pending'}), step({status: 'ready'}), step({status: 'in_progress'}), b('waiting_user'), b('run_failed'), b('stale'), step({status: 'done'}), step({status: 'cancelled'}), step({status: 'x'})].map(stepRunStatus),
      ['queued', 'queued', 'running', 'waiting', 'failed', 'paused', 'succeeded', 'cancelled', 'idle'],
    );
  });
});

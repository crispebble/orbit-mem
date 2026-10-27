import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {approvalToDecideArgs, decideArgs, findArgs, memoryUpdateSteps, statusArgs} from '../src/requests.ts';
import type {OniMemory, OniProposal} from '../src/model.ts';

const mem = (over: Partial<OniMemory> = {}): OniMemory => ({
  id: 'm1', kind: 'rule', scope: 'global', state: 'candidate', title: '제목', body: '본문', tags: ['a'], version: 4, createdAt: 1, updatedAt: 2, ...over,
});
const value = (over = {}) => ({title: '제목', content: '본문', kind: 'rule' as const, scope: '전역', state: 'candidate' as const, tags: ['a'], stale: false, ...over});

describe('findArgs', () => {
  it('질의·종류·상태를 도구 인자로 바꾼다', () => {
    assert.deepEqual(findArgs({query: ' 커밋 ', kind: 'rule', state: 'confirmed'}), {query: '커밋', kinds: ['rule'], states: ['confirmed'], scope: 'all', limit: 50});
  });
  it('전체·낡음 제외는 거르지 않거나 기본 상태를 쓴다', () => {
    assert.deepEqual(findArgs({query: '', kind: 'all', state: 'all'}), {scope: 'all', limit: 50, states: ['candidate', 'confirmed', 'stale']});
    assert.deepEqual(findArgs({query: '', kind: 'all', state: 'active'}), {scope: 'all', limit: 50, states: ['candidate', 'confirmed']});
  });
});

describe('decideArgs', () => {
  it('승인·거절 인자를 만든다', () => {
    assert.deepEqual(decideArgs('p1', 'approve'), {id: 'p1', decision: 'approve'});
    assert.deepEqual(decideArgs('p1', 'reject', {note: '필요 없음'}), {id: 'p1', decision: 'reject', note: '필요 없음'});
    assert.deepEqual(decideArgs('p1', 'approve', {title: '새 제목', body: '새 본문'}), {id: 'p1', decision: 'approve', title: '새 제목', body: '새 본문'});
  });
  it('빈 메모는 보내지 않는다', () => {
    assert.deepEqual(decideArgs('p1', 'reject', {note: '  '}), {id: 'p1', decision: 'reject'});
  });
});

describe('approvalToDecideArgs', () => {
  const p = (kind: string): OniProposal => ({id: 'p1', kind, title: 't', memoryIds: [], payload: {}, status: 'pending', createdAt: 1});
  it('고치지 않았으면 그대로 승인한다', () => {
    assert.deepEqual(approvalToDecideArgs(p('apply_file'), {text: 'x', edited: false}), {id: 'p1', decision: 'approve'});
  });
  it('고쳤으면 본문으로 보낸다', () => {
    assert.deepEqual(approvalToDecideArgs(p('apply_file'), {text: '새 내용', edited: true}), {id: 'p1', decision: 'approve', body: '새 내용'});
  });
});

describe('memoryUpdateSteps', () => {
  it('바뀐 게 없으면 호출하지 않는다', () => {
    assert.deepEqual(memoryUpdateSteps(mem(), value()), []);
  });
  it('바뀐 칸만 expectedVersion과 함께 보낸다', () => {
    assert.deepEqual(memoryUpdateSteps(mem(), value({title: '새 제목', tags: ['a', 'b']})), [
      {id: 'm1', expectedVersion: 4, action: 'edit', title: '새 제목', tags: ['a', 'b']},
    ]);
  });
  it('후보를 확정으로 바꾸면 confirm 동작을 쓴다', () => {
    assert.deepEqual(memoryUpdateSteps(mem(), value({state: 'confirmed', content: '고침'})), [
      {id: 'm1', expectedVersion: 4, action: 'confirm', body: '고침'},
    ]);
  });
  it('낡음 표시는 stale, 낡음을 풀면 확정으로 되돌린다', () => {
    assert.equal(memoryUpdateSteps(mem(), value({stale: true, state: 'stale'}))[0].action, 'stale');
    const back = memoryUpdateSteps(mem({state: 'stale'}), value({state: 'confirmed', stale: false}));
    assert.equal(back[0].action, 'confirm');
  });
  it('이미 낡음인 기억을 낡음으로 두면 상태 동작을 보내지 않는다', () => {
    assert.deepEqual(memoryUpdateSteps(mem({state: 'stale'}), value({state: 'stale', stale: true})), []);
  });
  it('전역으로 옮기면 scope=global을 보낸다', () => {
    const steps = memoryUpdateSteps(mem({scope: 'project:x'}), value({scope: '전역'}));
    assert.equal(steps[0].scope, 'global');
  });
  it('대체는 편집 뒤 supersede를 한 번 더 부른다(판본은 호출 때 채운다)', () => {
    const steps = memoryUpdateSteps(mem(), value({title: '새', supersededBy: 'm9', stale: true, state: 'stale'}));
    assert.deepEqual(steps.map(s => s.action), ['edit', 'supersede']);
    assert.equal(steps[1].supersededBy, 'm9');
    assert.equal(steps[1].expectedVersion, undefined);
  });
});

describe('statusArgs', () => {
  it('확정·낡음·되돌리기', () => {
    assert.deepEqual(statusArgs(mem(), 'confirm'), {id: 'm1', expectedVersion: 4, action: 'confirm'});
    assert.deepEqual(statusArgs(mem(), 'stale', '낡음'), {id: 'm1', expectedVersion: 4, action: 'stale', reason: '낡음'});
    assert.deepEqual(statusArgs(mem({state: 'stale'}), 'restore'), {id: 'm1', expectedVersion: 4, action: 'restore'});
  });
});

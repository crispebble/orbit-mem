import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {createActionGate} from '../src/parts/action.ts';
import {changedLines} from '../src/parts/proposal-text.ts';
import {
  GLOBAL_SCOPE, KINDS, kindOf, memoryKindLabels, memorySourceLabels, memoryStateLabels, proposalApproveHints, proposalKindLabels, scopeOf, stateOf,
  type OrbitMemory,
} from '../src/parts/vocabulary.ts';

const mem = (over: Partial<OrbitMemory> = {}): OrbitMemory => ({id: 'm1', title: '제목', content: '본문', ...over});

describe('어휘', () => {
  it('종류·상태·출처·제안 종류의 이름이 빠짐없이 있다', () => {
    assert.deepEqual(Object.keys(memoryKindLabels), [...KINDS]);
    assert.deepEqual(Object.keys(memoryStateLabels), ['candidate', 'confirmed', 'stale', 'hidden']);
    assert.deepEqual(Object.keys(memorySourceLabels), ['quote', 'file', 'command', 'session', 'host_memory', 'legacy']);
    assert.deepEqual(Object.keys(proposalKindLabels), ['confirm', 'stale', 'merge', 'reclassify', 'apply_file', 'direction', 'link', 'cleanup', 'harness']);
    assert.deepEqual(Object.keys(proposalApproveHints), Object.keys(proposalKindLabels));
  });
  it('승인 안내 문구가 Forma 0.9.x와 같다', () => {
    assert.equal(proposalApproveHints.confirm, '승인하면 이 후보를 확정 기억으로 바꿉니다.');
    assert.equal(proposalKindLabels.cleanup, '기억 정리·합치기');
    assert.equal(memoryKindLabels.journal, '일지');
  });
  it('칸이 없는 기억은 사실·확정·전역으로 본다', () => {
    const m = mem();
    assert.equal(kindOf(m), 'fact');
    assert.equal(stateOf(m), 'confirmed');
    assert.equal(scopeOf(m), GLOBAL_SCOPE);
    assert.equal(scopeOf(mem({scope: ''})), '전역');
  });
  it('있는 칸은 그대로 쓴다', () => {
    const m = mem({kind: 'rule', state: 'stale', scope: 'orbit'});
    assert.deepEqual([kindOf(m), stateOf(m), scopeOf(m)], ['rule', 'stale', 'orbit']);
  });
});

describe('createActionGate', () => {
  it('동작이 끝나면 ok를 돌려준다', async () => {
    const gate = createActionGate();
    assert.deepEqual(await gate.run(() => {}), {ok: true});
    assert.equal(gate.pending, false);
  });
  it('실행 중에 또 부르면 pending으로 막는다', async () => {
    const gate = createActionGate();
    let release!: () => void;
    const first = gate.run(() => new Promise<void>(resolve => { release = resolve; }));
    assert.equal(gate.pending, true);
    assert.deepEqual(await gate.run(() => {}), {ok: false, reason: 'pending'});
    release();
    assert.deepEqual(await first, {ok: true});
    assert.equal(gate.pending, false);
  });
  it('던진 오류는 메시지로 바꾸고 문을 연다', async () => {
    const gate = createActionGate();
    assert.deepEqual(await gate.run(() => { throw new Error('실패'); }), {ok: false, reason: 'error', error: '실패'});
    assert.deepEqual(await gate.run(() => Promise.reject('문자열')), {ok: false, reason: 'error', error: '요청을 처리하지 못했습니다.'});
    assert.equal(gate.pending, false);
  });
});

describe('changedLines', () => {
  it('같으면 0이다', () => assert.equal(changedLines('a\nb', 'a\nb'), 0));
  it('바뀐 줄을 센다', () => assert.equal(changedLines('a\nb\nc', 'a\nx\nc'), 1));
  it('줄 수 차이도 더한다', () => assert.equal(changedLines('a', 'a\nb\nc'), 2));
});

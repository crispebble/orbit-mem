// 「그래프에서 보기」 요청 처리. 「Studio 보기 › 노드와 고른 기억」에 해당한다.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {resolveRequest, type RequestContext} from '../src/memory-graph/select.ts';

const ctx = (over: Partial<RequestContext> = {}): RequestContext => ({
  loaded: true, reloading: false, graphIds: new Set(['a', 'b']), visibleIds: new Set(['a']), reloaded: false, ...over,
});

describe('그래프에서 보기 요청', () => {
  it('보이는 기억이면 고른다', () => {
    assert.deepEqual(resolveRequest('a', ctx()), {kind: 'select', clearKind: false});
  });
  it('종류 거르기에 가려진 기억이면 「모두」로 되돌리고 고른다', () => {
    assert.deepEqual(resolveRequest('b', ctx()), {kind: 'select', clearKind: true});
  });
  it('받은 그래프에 없으면 한 번 다시 읽고, 그래도 없으면 없음', () => {
    assert.deepEqual(resolveRequest('new', ctx()), {kind: 'reload'});
    assert.deepEqual(resolveRequest('new', ctx({reloaded: true})), {kind: 'missing'});
  });
  it('그래프가 없거나 읽는 중이면 기다린다', () => {
    assert.deepEqual(resolveRequest('a', ctx({loaded: false})), {kind: 'wait'});
    assert.deepEqual(resolveRequest('new', ctx({reloading: true, reloaded: true})), {kind: 'wait'});
  });
});

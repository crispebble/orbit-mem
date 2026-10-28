import {describe, it} from 'node:test';
import assert from 'node:assert/strict';
import {createInboxParts} from '../src/inbox-parts.ts';

type Item = {id: string; title: string};

describe('받은 일 갈래 합치기', () => {
  it('제안과 확인 기다림을 각자 바꿔도 서로를 지우지 않고 한 번에 set한다', () => {
    const sets: string[][] = [];
    const parts = createInboxParts<Item>({set: items => { sets.push(items.map(i => i.id)); }});
    parts.set('proposals', [{id: 'p1', title: '제안'}]);
    parts.set('waiting', [{id: 'w1', title: '확인'}]);
    parts.set('proposals', []);
    assert.deepEqual(sets, [['p1'], ['p1', 'w1'], ['w1']]);
  });

  it('같은 내용이면 다시 set하지 않고, 갈래를 지우면 그 항목만 빠진다', () => {
    const sets: string[][] = [];
    const parts = createInboxParts<Item>({set: items => { sets.push(items.map(i => i.id)); }});
    parts.set('waiting', [{id: 'w1', title: '확인'}]);
    parts.set('waiting', [{id: 'w1', title: '확인'}]);
    parts.set('proposals', [{id: 'p1', title: '제안'}]);
    parts.clear('waiting');
    assert.deepEqual(sets, [['w1'], ['w1', 'p1'], ['p1']]);
  });
});

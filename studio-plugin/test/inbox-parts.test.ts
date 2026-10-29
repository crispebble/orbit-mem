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

describe('작업 확인 기다림 → 받은 일', () => {
  it('받은 일 id에서 작업·단계 id를 되찾는다, 다른 id는 undefined', async () => {
    const {parseWaitingId, waitingInboxItems} = await import('../src/work/inbox.ts');
    const {parseGet} = await import('../src/work/contract.ts');
    const {example} = await import('./work-fake.ts');
    const task = parseGet(example('work_get.claude-task').result).work;
    const [item] = waitingInboxItems([task]);
    assert.deepEqual(parseWaitingId(item.id), {workId: task.id, stepId: 'st-4d5e6f708192'});
    assert.equal(parseWaitingId('p-123'), undefined);
    assert.equal(parseWaitingId('work-waiting:only'), undefined);
    // 계약 id는 불투명 문자열이라 ':'가 들어 있어도 되찾는다
    const [odd] = waitingInboxItems([{...task, id: 'w:1/2', steps: task.steps.map(s => s.id === 'st-4d5e6f708192' ? {...s, id: 'st:x'} : s)}]);
    assert.deepEqual(parseWaitingId(odd.id), {workId: 'w:1/2', stepId: 'st:x'});
  });

  it('store가 바뀔 때마다 읽은 작업의 확인 기다림을 waiting 갈래로 넣고, 끊으면 갈래를 지운다', async () => {
    const {syncWaitingInbox} = await import('../src/work/inbox.ts');
    const {parseGet} = await import('../src/work/contract.ts');
    const {example} = await import('./work-fake.ts');
    const task = parseGet(example('work_get.claude-task').result).work;
    let details: Record<string, typeof task> = {};
    const listeners = new Set<() => void>();
    const store = {getState: () => ({details}), subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l); }; }};
    const sets: string[][] = [];
    const parts = createInboxParts<{id: string}>({set: items => { sets.push(items.map(i => i.id)); }});
    const off = syncWaitingInbox(store, parts);
    details = {[task.id]: task};
    listeners.forEach(l => l());
    off();
    assert.deepEqual(sets, [[], [`work-waiting:${task.id}:st-4d5e6f708192`], []]);
    assert.equal(listeners.size, 0);
  });
});

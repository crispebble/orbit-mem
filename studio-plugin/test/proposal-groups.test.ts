// 제안 묶어 보기와 묶음 결정(설계 · Orbit 3차 O3-1 「묶어 보기와 일괄 결정」 Orbit Studio의 orbit-mem 앱).
import assert from 'node:assert/strict';
import {mkdirSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {setLocale} from '../src/i18n/index.ts';
import {groupProposalRows, toProposalRow, type OrbProposal} from '../src/model.ts';
import {decideManyArgs} from '../src/requests.ts';
import {createStore} from '../src/store.ts';
import {decideGroup} from '../src/group-decision.ts';
import {fail, fakeCtx, ok} from './fake-ctx.ts';

const prop = (id: string, kind: string, status = 'pending', createdAt = 1_780_000_000_000): OrbProposal =>
  ({id, kind, title: `제안 ${id}`, memoryIds: [], payload: {}, status, createdAt});

describe('groupProposalRows', () => {
  it('종류별로 묶고 건수가 많은 묶음부터, 같으면 먼저 나온 종류부터 둔다', () => {
    const rows = [prop('a', 'confirm'), prop('b', 'stale'), prop('c', 'stale'), prop('d', 'link'), prop('e', 'confirm'), prop('f', 'stale')]
      .map(p => toProposalRow(p));
    const groups = groupProposalRows(rows);
    assert.deepEqual(groups.map(g => [g.kind, g.label, g.rows.map(r => r.id)]), [
      ['stale', 'Clean up stale memory', ['b', 'c', 'f']],
      ['confirm', 'Confirm candidate', ['a', 'e']],
      ['link', 'Link related memories', ['d']],
    ]);
  });

  it('빈 목록은 빈 묶음', () => {
    assert.deepEqual(groupProposalRows([]), []);
  });
});

describe('decideManyArgs', () => {
  it('ids와 결정, 빈 사유는 빼고 보낸다', () => {
    assert.deepEqual(decideManyArgs(['a', 'b'], 'approve'), {ids: ['a', 'b'], decision: 'approve'});
    assert.deepEqual(decideManyArgs(['a'], 'reject', '  '), {ids: ['a'], decision: 'reject'});
    assert.deepEqual(decideManyArgs(['a'], 'reject', ' 묶음 거절 '), {ids: ['a'], decision: 'reject', note: '묶음 거절'});
  });
});

describe('store.decideMany', () => {
  it('proposal_decide에 ids로 한 번 보내고 다시 불러온 뒤 결정 수와 실패를 돌려준다', async () => {
    let decided = false;
    const f = fakeCtx({
      proposals: () => ok({items: decided ? [prop('a', 'stale', 'approved'), prop('b', 'stale')] : [prop('a', 'stale'), prop('b', 'stale')]}),
      proposal_decide: () => {
        decided = true;
        return ok({decided: [{proposal: prop('a', 'stale', 'approved')}], failed: [{id: 'b', error: '이미 결정한 제안입니다'}]});
      },
    });
    const store = createStore(f.ctx);
    await store.refresh();
    const out = await store.decideMany(decideManyArgs(['a', 'b'], 'approve'));
    assert.deepEqual(out, {decided: 1, failed: [{id: 'b', error: '이미 결정한 제안입니다'}]});
    assert.deepEqual(f.calls.filter(c => c.tool === 'proposal_decide'), [{tool: 'proposal_decide', args: {ids: ['a', 'b'], decision: 'approve'}}]);
    assert.equal(f.calls.at(-1)?.tool, 'proposals');
    assert.equal(store.getState().proposals.filter(p => p.status === 'pending').length, 1);
  });

  it('실패 칸이 비어 오면 빈 목록으로 본다', async () => {
    const f = fakeCtx({proposals: () => ok({items: []}), proposal_decide: () => ok({decided: [{}, {}]})});
    const store = createStore(f.ctx);
    assert.deepEqual(await store.decideMany(decideManyArgs(['a', 'b'], 'reject')), {decided: 2, failed: []});
  });

  it('도구 오류는 던진다', async () => {
    const f = fakeCtx({proposals: () => ok({items: []}), proposal_decide: () => fail('입력이 올바르지 않습니다')});
    const store = createStore(f.ctx);
    await assert.rejects(store.decideMany(decideManyArgs(['a'], 'approve')), /입력이 올바르지 않습니다/);
  });
});

describe('decideGroup', () => {
  const group = {kind: 'stale', label: 'Clean up stale memory', rows: [{id: 'a', title: '제안 a'}, {id: 'b', title: '제안 b'}]};
  const handlers = (decide: () => ReturnType<typeof ok>) => ({proposals: () => ok({items: []}), proposal_decide: decide});

  it('확인 창에서 고르지 않으면 아무것도 보내지 않는다', async () => {
    const f = fakeCtx(handlers(() => ok({decided: []})), {ask: 'cancel'});
    assert.equal(await decideGroup(f.ctx, createStore(f.ctx), group, 'approve'), 'cancelled');
    assert.equal(f.asks[0]?.title, 'Approve all 2 proposals in "Clean up stale memory"?');
    assert.equal(f.calls.length, 0);
  });

  it('모두 결정하면 성공 알림', async () => {
    const f = fakeCtx(handlers(() => ok({decided: [{}, {}]})), {ask: 'reject'});
    assert.equal(await decideGroup(f.ctx, createStore(f.ctx), group, 'reject'), 'done');
    assert.deepEqual(f.calls[0], {tool: 'proposal_decide', args: {ids: ['a', 'b'], decision: 'reject'}});
    assert.deepEqual(f.notices.at(-1), {title: 'Rejected 2 proposals in "Clean up stale memory"', tone: 'success'});
  });

  it('일부 실패는 제목과 이유를 경고로 알린다', async () => {
    const f = fakeCtx(handlers(() => ok({decided: [{}], failed: [{id: 'b', error: '이미 결정한 제안입니다'}]})), {ask: 'approve'});
    await decideGroup(f.ctx, createStore(f.ctx), group, 'approve');
    assert.deepEqual(f.notices.at(-1), {title: '1 approved, 1 could not be decided', body: '제안 b: 이미 결정한 제안입니다', tone: 'warning'});
  });

  it('호출이 실패하면 알리고 목록을 다시 읽는다', async () => {
    const f = fakeCtx(handlers(() => fail('시간 초과')), {ask: 'approve'});
    assert.equal(await decideGroup(f.ctx, createStore(f.ctx), group, 'approve'), 'failed');
    assert.equal(f.notices.at(-1)?.tone, 'danger');
    assert.equal(f.calls.at(-1)?.tool, 'proposals');
  });
});

type Harness = typeof import('./proposal-groups-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-groups-render-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const outfile = join(dir, 'harness.mjs');
  await build({
    entryPoints: [new URL('./proposal-groups-harness.tsx', import.meta.url).pathname],
    outfile, bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent',
    packages: 'external', loader: {'.css': 'empty'},
  });
  h = await import(outfile);
});
after(() => rmSync(dir, {recursive: true, force: true}));

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');

describe('ProposalGroupList', () => {
  const groups = [
    {kind: 'stale', label: 'Clean up stale memory', items: [{id: 'b', title: '제안 b', subtitle: '오늘'}, {id: 'c', title: '제안 c', subtitle: '어제'}]},
    {kind: 'confirm', label: 'Confirm candidate', items: [{id: 'a', title: '제안 a', subtitle: '오늘'}]},
  ];

  it('묶음마다 이름·건수·묶음 승인·거절과 제안을 그린다', () => {
    const html = h.render({groups});
    const t = text(html);
    for (const s of ['Clean up stale memory', '2', 'Confirm candidate', '1', '제안 b', '제안 c', '제안 a']) assert.ok(t.includes(s), s);
    assert.equal(html.match(/>Approve all</g)?.length, 2);
    assert.equal(html.match(/>Reject all</g)?.length, 2);
    assert.match(html, /aria-label="Approve all 2 proposals in &quot;Clean up stale memory&quot;"/);
  });

  it('한 묶음을 결정하는 동안 결정이 겹치지 않게 모든 묶음 단추를 끈다', () => {
    const idle = h.render({groups});
    assert.doesNotMatch(idle.match(/<button[^>]*aria-label="Approve all 1 proposal in &quot;Confirm candidate&quot;"[^>]*>/)?.[0] ?? '', /disabled/);
    const busy = h.render({groups, busyKind: 'stale'});
    const buttons = busy.match(/<button[^>]*aria-label="(Approve|Reject) all[^"]*"[^>]*>/g) ?? [];
    assert.equal(buttons.length, 4);
    for (const b of buttons) assert.match(b, /disabled/);
  });

  it('결정할 수 없으면(연결 끊김) 묶음 단추를 그리지 않는다', () => {
    assert.doesNotMatch(h.render({groups, decidable: false}), /Approve all/);
  });
});

describe('한국어', () => {
  it('언어를 한국어로 바꾸면 확인 창과 묶음 단추가 한국어다', async () => {
    setLocale('ko');
    h.setLocale('ko');
    try {
      const group = {kind: 'stale', label: '낡은 기억 정리', rows: [{id: 'a', title: '제안 a'}, {id: 'b', title: '제안 b'}]};
      const f = fakeCtx({proposals: () => ok({items: []}), proposal_decide: () => ok({decided: []})}, {ask: 'cancel'});
      await decideGroup(f.ctx, createStore(f.ctx), group, 'approve');
      assert.equal(f.asks[0]?.title, '낡은 기억 정리 2건을 모두 승인할까요?');
      assert.deepEqual(f.asks[0]?.actions.map(a => a.label), ['모두 승인', '취소']);
      const html = h.render({groups: [{kind: 'stale', label: '낡은 기억 정리', items: [{id: 'b', title: '제안 b'}]}]});
      assert.match(html, /aria-label="낡은 기억 정리 1건 모두 승인"/);
      assert.match(html, />모두 거절</);
    } finally {
      setLocale('en');
      h.setLocale('en');
    }
  });
});

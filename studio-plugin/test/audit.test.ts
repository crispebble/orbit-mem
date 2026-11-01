// 감사 목록(설계 A2 · AU-6): store 읽기·되돌리기·무시, 단추 흐름, 화면 글(영어·한국어).
import assert from 'node:assert/strict';
import {mkdirSync, readFileSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {dismissItem, resolveConflict, revertChange} from '../src/audit-actions.ts';
import {auditAttention, toAuditView, type OrbAudit} from '../src/model.ts';
import {dismissArgs, revertArgs} from '../src/requests.ts';
import {createStore} from '../src/store.ts';
import {audit} from './audit-fixture.ts';
import {fail, fakeCtx, ok} from './fake-ctx.ts';

const pendingProposal = {id: 'q-1', kind: 'stale', title: '낡은 메모 정리', memoryIds: [], payload: {}, status: 'pending', createdAt: 1_780_000_000_000};
const handlers = (over: Parameters<typeof fakeCtx>[0] = {}) => ({
  proposals: (a?: Record<string, unknown>) => ok(a?.status === 'audit' ? {items: [], audit} : {items: [pendingProposal]}),
  proposal_decide: () => ok({}),
  memory_update: () => ok({memory: {id: 'm-9', version: 2}}),
  ...over,
});

describe('요청 인자', () => {
  it('revert·dismiss 인자', () => {
    assert.deepEqual(revertArgs('p-1'), {id: 'p-1', decision: 'revert'});
    assert.deepEqual(revertArgs('p-1', ' 틀림 '), {id: 'p-1', decision: 'revert', note: '틀림'});
    assert.deepEqual(dismissArgs('f-1'), {id: 'f-1', decision: 'dismiss'});
  });
});

describe('store 감사 목록', () => {
  it('새로 고칠 때 proposals를 all과 audit로 읽고 감사 목록을 상태에 둔다', async () => {
    const f = fakeCtx(handlers());
    const store = createStore(f.ctx);
    await store.refresh();
    assert.deepEqual(f.calls.map(c => c.args), [{status: 'all'}, {status: 'audit'}]);
    assert.equal(store.getState().audit?.changes?.length, 3);
    assert.equal(store.getState().auditError, undefined);
  });

  it('배지는 대기 제안 + 어긋남 + 확인 전 반복 교정', async () => {
    const f = fakeCtx(handlers());
    await createStore(f.ctx).refresh();
    assert.equal(f.badges.at(-1), 1 + 1 + 2);
    const acked: OrbAudit = {...audit, corrections: [{...audit.corrections![0], ackedAt: 1}]};
    assert.equal(auditAttention(0, acked), 1);
    assert.equal(auditAttention(0, undefined), 0);
    assert.deepEqual(toAuditView({...audit, changes: null, conflicts: null, corrections: null}), {changes: [], conflicts: [], corrections: []});
  });

  it('감사 목록만 실패하면 제안 목록은 보이고 이유를 둔다', async () => {
    const f = fakeCtx(handlers({proposals: a => (a?.status === 'audit' ? fail('알 수 없는 상태') : ok({items: [pendingProposal]}))}));
    const store = createStore(f.ctx);
    await store.refresh();
    const s = store.getState();
    assert.equal(s.status, 'ready');
    assert.equal(s.proposals.length, 1);
    assert.match(s.auditError ?? '', /알 수 없는 상태/);
  });

  it('revert는 proposal_decide revert로 보내고 다시 읽는다', async () => {
    const f = fakeCtx(handlers());
    const store = createStore(f.ctx);
    await store.revert('p-auto', '틀림');
    assert.deepEqual(f.calls[0], {tool: 'proposal_decide', args: {id: 'p-auto', decision: 'revert', note: '틀림'}});
    assert.deepEqual(f.calls.slice(1).map(c => c.args), [{status: 'all'}, {status: 'audit'}]);
  });

  it('dismiss는 proposal_decide dismiss로 보낸다', async () => {
    const f = fakeCtx(handlers());
    await createStore(f.ctx).dismiss('correction:skill:tdd');
    assert.deepEqual(f.calls[0], {tool: 'proposal_decide', args: {id: 'correction:skill:tdd', decision: 'dismiss'}});
  });
});

describe('감사 단추 흐름', () => {
  it('되돌리기는 확인 창에서 고르지 않으면 보내지 않는다', async () => {
    const f = fakeCtx(handlers(), {ask: 'cancel'});
    assert.equal(await revertChange(f.ctx, createStore(f.ctx), audit.changes![0]), 'cancelled');
    assert.equal(f.calls.length, 0);
    assert.equal(f.asks[0]?.title, 'Revert "테스트는 mock 없이 실제 DB로"?');
  });

  it('되돌리기를 확인하면 proposalId로 보내고 알린다', async () => {
    const f = fakeCtx(handlers(), {ask: 'revert'});
    assert.equal(await revertChange(f.ctx, createStore(f.ctx), audit.changes![0]), 'done');
    assert.deepEqual(f.calls[0], {tool: 'proposal_decide', args: {id: 'p-auto', decision: 'revert'}});
    assert.equal(f.notices.at(-1)?.tone, 'success');
  });

  it('되돌리기가 실패하면 위험 알림', async () => {
    const f = fakeCtx(handlers({proposal_decide: () => fail('이미 되돌렸습니다')}), {ask: 'revert'});
    assert.equal(await revertChange(f.ctx, createStore(f.ctx), audit.changes![0]), 'failed');
    assert.deepEqual([f.notices.at(-1)?.tone, f.notices.at(-1)?.body], ['danger', '이미 되돌렸습니다']);
  });

  it('그래도 확정은 memory_update confirm, 낡음은 stale', async () => {
    const c = audit.conflicts![0];
    const a = fakeCtx(handlers(), {ask: 'confirm'});
    assert.equal(await resolveConflict(a.ctx, createStore(a.ctx), c, 'confirm'), 'done');
    assert.deepEqual(a.calls[0], {tool: 'memory_update', args: {id: 'm-9', action: 'confirm'}});
    const b = fakeCtx(handlers(), {ask: 'stale'});
    assert.equal(await resolveConflict(b.ctx, createStore(b.ctx), c, 'stale'), 'done');
    assert.deepEqual(b.calls[0], {tool: 'memory_update', args: {id: 'm-9', action: 'stale'}});
    const n = fakeCtx(handlers(), {ask: 'cancel'});
    assert.equal(await resolveConflict(n.ctx, createStore(n.ctx), c, 'stale'), 'cancelled');
    assert.equal(n.calls.length, 0);
  });

  it('무시는 확인 창 없이 dismiss', async () => {
    const f = fakeCtx(handlers());
    assert.equal(await dismissItem(f.ctx, createStore(f.ctx), 'f-1'), 'done');
    assert.deepEqual(f.calls[0], {tool: 'proposal_decide', args: {id: 'f-1', decision: 'dismiss'}});
    assert.equal(f.asks.length, 0);
  });
});

type Harness = typeof import('./audit-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-audit-render-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});
before(async () => {
  const outfile = join(dir, 'harness.mjs');
  await build({
    entryPoints: [new URL('./audit-harness.tsx', import.meta.url).pathname],
    outfile, bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent',
    packages: 'external', loader: {'.css': 'empty'},
  });
  h = await import(outfile);
});
after(() => { h.setLocale('en'); rmSync(dir, {recursive: true, force: true}); });

const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');

describe('감사 목록 화면', () => {
  it('영어: 섹션 넷, 증거·판정, 자동 되돌림, 하네스 표식, 단추', () => {
    h.setLocale('en');
    const html = h.render();
    const t = text(html);
    for (const s of ['Automatic changes', 'Conflicts', 'Repeated corrections', 'Pending proposals',
      'Seen on different days', 'Helped', 'Harmful', 'Auto-reverted (harmful)', 'Merge memories',
      '커밋 메시지는 영어로 ↔ 커밋 메시지는 한글로', 'Harness candidate', '4/3 sessions', '테스트부터 쓰라고 했잖아', 'Mark reviewed',
      'Clean up stale memory', '낡은 메모 정리']) assert.ok(t.includes(s), s);
    assert.ok(!t.includes('처음 인용'), '최근 인용 하나만');
    assert.equal(html.match(/>Harness candidate</g)?.length, 1);
    assert.equal(html.match(/>Revert</g)?.length, 1, '되돌린 것과 되돌림 기록에는 단추가 없다');
    assert.equal(html.match(/>Confirm anyway</g)?.length, 1);
    assert.equal(html.match(/>Mark stale</g)?.length, 1);
    assert.equal(html.match(/>Dismiss</g)?.length, 1);
    assert.equal(html.match(/>Mark reviewed</g)?.length, 2);
  });

  it('한국어: 같은 구조', () => {
    h.setLocale('ko');
    const html = h.render();
    const t = text(html);
    for (const s of ['자동 변경', '충돌', '반복 교정', '대기 중인 제안', '다른 날 다시 나옴', '도움됨', '해로움', '자동 되돌림(해로움)', '하네스 개선 후보', '4/3 세션', '검토 완료', '그래도 확정', '낡음으로 표시', '무시', '되돌리기']) {
      assert.ok(t.includes(s), s);
    }
    assert.equal(html.match(/>되돌리기</g)?.length, 1);
    h.setLocale('en');
  });

  it('비어 있으면 섹션마다 빈 글', () => {
    h.setLocale('en');
    const t = text(h.render({audit: {changes: [], conflicts: [], corrections: []}, groups: []}));
    for (const s of ['No automatic changes yet.', 'No conflicts with your rules.', 'No repeated corrections.', 'No pending proposals.']) assert.ok(t.includes(s), s);
  });

  it('연결이 끊기면(actionable false) 결정 단추를 그리지 않는다', () => {
    h.setLocale('en');
    const html = h.render({actionable: false});
    for (const s of ['>Revert<', '>Confirm anyway<', '>Mark reviewed<', '>Approve all<']) assert.ok(!html.includes(s), s);
  });
});

describe('plugin.json 감사 이름', () => {
  const m = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
  it('보기 id는 그대로, 제목은 Audit/감사, 버전 0.3.0', () => {
    assert.equal(m.version, '0.3.0');
    assert.deepEqual(m.apps[0].views[0], {id: 'proposals', title: 'Audit'});
    assert.equal(m.locales.ko.apps.orbit.views.proposals, '감사');
    assert.equal(m.commands.find((c: {id: string}) => c.id === 'orbit.proposals').title, 'Show audit list');
    assert.equal(m.locales.ko.commands['orbit.proposals'], '감사 목록 보기');
  });
});

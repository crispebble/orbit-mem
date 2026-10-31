// 처음부터 끝까지: 가짜 데몬 ← 뒤쪽(service) ← 창쪽 DaemonClient ← 합성 카드 출처. 사이는 ctx.backend.call/on 흉내뿐이다.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {createDaemonClient} from '../src/daemon/client.ts';
import {daemonDecisionSource} from '../src/decision/daemon.ts';
import type {DecisionSource} from '../src/decision/types.ts';
import {rig, until} from './backend-rig.ts';

const emptyOrb: DecisionSource = {
  list: async () => ({items: [], seq: 'o1', truncated: false}),
  changes: (_q, signal) => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), {once: true})),
  answer: async () => ({ok: true}),
};

describe('데몬 처음부터 끝까지', () => {
  it('ensure 뒤 실행을 시작하면 질문 카드가 합성 출처에 나타나고, 답하면 데몬에 닿는다', async () => {
    const r = await rig({published: false});
    const handlers = new Map<string, Set<(p: unknown) => void>>();
    const client = createDaemonClient({
      call: async (method, params) => structuredClone(await r.backend.methods[method](params, {signal: new AbortController().signal})) as never,
      on: (event, h) => {
        const set = handlers.get(event) ?? handlers.set(event, new Set()).get(event)!;
        set.add(h);
        return () => set.delete(h);
      },
    });
    // 뒤쪽이 보낸 사건을 창쪽 구독자에게 이어 준다(ctx.backend.on 흉내)
    const emitted = r.emitted.push.bind(r.emitted);
    r.emitted.push = (...items) => {
      for (const i of items) handlers.get(i.event)?.forEach(h => h(structuredClone(i.payload)));
      return emitted(...items);
    };

    assert.equal((await client.refresh()).daemon, 'down');
    assert.equal((await client.ensure()).paired, true);
    assert.equal(client.status().daemon, 'up');
    await r.backend.start();
    await client.loadMessages('ko');
    assert.equal(client.optionLabel('approve'), '승인');

    const started = await client.api.startRun({request: 'build it', cwd: '/w', mode: 'default'});
    const live = r.daemon.runs.get(started.runId)!;
    live.sessionId = 'S-1';
    live.openDecisions = [{id: 'd1', title: 'Run the plan?', options: ['approve', 'cancel'], askedAt: '2026-11-01T01:00:00.000Z'}];
    r.daemon.push(started.runId, 'run.started');
    r.daemon.push(started.runId, 'decision.asked', {id: 'd1', title: 'Run the plan?', options: ['approve', 'cancel'], kind: 'question', name: 'plan-approval', key: 'decision.plan', args: ['3 steps']});
    const source = daemonDecisionSource(client, emptyOrb);
    await until(() => r.events().length === 2, '전역 스트림으로 사건 둘');

    const cards = (await source.list({state: ['open']})).items;
    assert.deepEqual(cards.map(c => c.id), [`daemon:${started.runId}:d1`]);
    assert.equal(cards[0].questions[0].text, 'Run the plan 3 steps?');
    assert.deepEqual(cards[0].questions[0].options.map(o => o.label), ['승인', 'cancel']);
    assert.equal(cards[0].work?.sessionId, 'S-1');

    const out = await source.answer(cards[0].id, [{optionIds: ['approve']}]);
    assert.equal(out.ok, true);
    assert.deepEqual(r.daemon.answers, [{runId: started.runId, decisionId: 'd1', answer: 'approve'}]);
    source.dispose();
    client.dispose();
  });
});

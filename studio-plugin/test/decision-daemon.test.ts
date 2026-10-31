// 결정 카드 출처 합성(S4-5, C5): 데몬 카드 + orb 카드, 중복 제거, 답하기, 변경 깨우기.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {formatMessage, type DaemonEvent, type DaemonStatus, type RunSummary} from '../src/daemon/api.ts';
import type {DaemonClient} from '../src/daemon/client.ts';
import {RECORDS_MAX, cardId, daemonDecisionSource, parseCardId} from '../src/decision/daemon.ts';
import {toStudioCard} from '../src/decision/provider.ts';
import {createDecisionStore} from '../src/decision/store.ts';
import type {AnswerOutcome, Decision, DecisionSource} from '../src/decision/types.ts';

const NOW = Date.parse('2026-11-01T03:00:00.000Z');
const SINCE = '2026-11-01T00:00:00.000Z';

const ev = (seq: number, runId: string, type: string, body: Record<string, unknown> = {}, at = '2026-11-01T01:00:00.000Z'): DaemonEvent =>
  ({seq, runId, type, at, event: {type, at, ...body}});

const run = (over: Partial<RunSummary> = {}): RunSummary => ({
  runId: 'r1', request: 'do it', cwd: '/w', profile: 'main', mode: 'default', state: 'running', reason: '', exitCode: 0, workId: 'w-1',
  message: '', startedAt: '2026-11-01T01:00:00.000Z', finishedAt: '', source: 'studio', device: 'orbit-studio', lastSeq: 10, sessionId: 'S-1',
  totals: {costUsd: 0, subscription: false, turns: 0, minutes: 0, inputTokens: 0, outputTokens: 0, monthSpentUsd: 0, monthLimitUsd: 0},
  openDecisions: [], ...over,
});

const asked = (runId: string, id: string, body: Record<string, unknown> = {}, seq = 2) =>
  ev(seq, runId, 'decision.asked', {id, title: 'Run the plan?', options: ['approve', 'revise'], key: 'decision.plan', args: ['3 steps'], kind: 'question', name: 'plan-approval', ...body});

function stubClient(init: {status?: Partial<DaemonStatus>; runs?: RunSummary[]; history?: Record<string, DaemonEvent[]>} = {}) {
  let status: DaemonStatus = {daemon: 'up', streaming: true, paired: true, lastSeq: 0, ...init.status};
  const eventListeners = new Set<(e: DaemonEvent) => void>();
  const statusListeners = new Set<(s: DaemonStatus) => void>();
  const messages: Record<string, string> = {'decision.plan': 'Run the plan (%s)?', 'option.approve': 'Approve', 'option.revise': 'Revise'};
  const s = {
    runs: init.runs ?? [],
    history: init.history ?? {},
    answers: [] as {runId: string; decisionId: string; answer: string}[],
    accepted: true,
    historyCalls: [] as string[],
    getRunCalls: [] as string[],
    /** ListRuns(최근 50개)에는 없지만 GetRun으로는 아는 실행 */
    extraRuns: {} as Record<string, RunSummary>,
    listRunsFails: false,
    setStatus(next: Partial<DaemonStatus>) {
      status = {...status, ...next};
      statusListeners.forEach(l => l(status));
    },
    fire: (e: DaemonEvent) => eventListeners.forEach(l => l(e)),
  };
  const client = {
    api: {
      listRuns: async () => {
        if (s.listRunsFails) throw new Error('daemon exploded');
        return {runs: s.runs};
      },
      history: async ({runId}: {runId: string}) => {
        s.historyCalls.push(runId);
        return {events: s.history[runId] ?? []};
      },
      getRun: async ({runId}: {runId: string}) => {
        s.getRunCalls.push(runId);
        const found = s.extraRuns[runId];
        if (!found) throw Object.assign(new Error('run not found'), {code: 'RPC_NOT_FOUND'});
        return {run: found};
      },
      answer: async (p: {runId: string; decisionId: string; answer: string}) => {
        s.answers.push(p);
        return {accepted: s.accepted};
      },
    },
    status: () => status,
    onStatus: (l: (x: DaemonStatus) => void) => { statusListeners.add(l); return () => { statusListeners.delete(l); }; },
    onEvent: (l: (e: DaemonEvent) => void) => { eventListeners.add(l); return () => { eventListeners.delete(l); }; },
    text: (key: unknown, args: unknown, fallback: string) => (typeof key === 'string' && messages[key] ? formatMessage(messages[key], Array.isArray(args) ? args : []) : fallback),
    optionLabel: (id: string) => messages[`option.${id}`] ?? id,
  } as unknown as DaemonClient;
  return {client, ...s, state: s};
}

const orbCard = (over: Partial<Decision> = {}): Decision => ({
  id: 'd-1', state: 'open', source: 'question', owner: 'orbit-pi', createdAt: '2026-11-01T01:30:00.000Z',
  questions: [{text: 'Proceed?', multiSelect: false, options: [{id: 'approve', label: 'Approve'}]}], links: [], ...over,
});

function fakeOrb(open: Decision[] = [], closed: Decision[] = []) {
  const calls: string[] = [];
  const orb: DecisionSource & {fail?: Error; resolveChanges?: (v: {seq: string; decisionIds: string[]}) => void} = {
    async list(q) {
      calls.push(`list:${q.state.join(',')}`);
      if (orb.fail) throw orb.fail;
      return {items: q.state.includes('open') ? open : closed, seq: 'o1', truncated: false};
    },
    changes(q, signal) {
      calls.push(`changes:${q.after ?? ''}:${q.wait}`);
      if (orb.fail) return Promise.reject(orb.fail);
      return new Promise((resolve, reject) => {
        orb.resolveChanges = v => resolve({...v, reset: false, more: false, waitMax: 30000});
        signal.addEventListener('abort', () => reject(new Error('aborted')), {once: true});
      });
    },
    async answer(id): Promise<AnswerOutcome> {
      calls.push(`answer:${id}`);
      return {ok: true};
    },
  };
  return {orb, calls};
}

const OPEN_RUN = () => run({openDecisions: [{id: 'd1', title: 'Run the plan?', options: ['approve', 'revise'], askedAt: '2026-11-01T01:00:00.000Z'}]});

describe('카드 id', () => {
  it('daemon:<runId>:<decisionId>이고 되돌려 읽는다', () => {
    assert.equal(cardId('r1', 'd2'), 'daemon:r1:d2');
    assert.deepEqual(parseCardId('daemon:run:with:colon:d2'), {runId: 'run:with:colon', decisionId: 'd2'});
    assert.equal(parseCardId('d-13f37'), undefined);
    assert.equal(parseCardId('daemon:onlyone'), undefined);
  });
});

describe('데몬 카드', () => {
  it('열린 카드는 ListRuns와 decision.asked 사건으로 만든다: 질문은 key/args, 선택지는 option.<id>, 세션·작업 연결', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [ev(1, 'r1', 'run.started'), asked('r1', 'd1', {context: 'bash: rm x'})]}});
    const {orb} = fakeOrb();
    const source = daemonDecisionSource(t.client, orb, {now: () => NOW});
    const page = await source.list({state: ['open']});
    assert.equal(page.items.length, 1);
    const d = page.items[0];
    assert.equal(d.id, 'daemon:r1:d1');
    assert.equal(d.state, 'open');
    assert.equal(d.owner, 'orbit-pi');
    assert.equal(d.source, 'question');
    assert.equal(d.questions[0].text, 'Run the plan (3 steps)?');
    assert.deepEqual(d.questions[0].options, [{id: 'approve', label: 'Approve'}, {id: 'revise', label: 'Revise'}]);
    assert.equal(d.context, 'bash: rm x');
    assert.deepEqual(d.work, {host: 'orbit-pi', sessionId: 'S-1', workId: 'w-1'});
    assert.equal(toStudioCard(d).relay, true);
    assert.equal(toStudioCard(d).workItemId, 'w-1');
  });

  it('kind가 tool이면 permission이고, 사건 없이 ListRuns뿐이면 title과 option id 그대로', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1', {kind: 'tool', name: undefined, key: undefined, args: undefined})]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    assert.equal((await source.list({state: ['open']})).items[0].source, 'permission');
    const bare = stubClient({runs: [OPEN_RUN()]});
    const d = (await daemonDecisionSource(bare.client, fakeOrb().orb).list({state: ['open']})).items[0];
    assert.equal(d.questions[0].text, 'Run the plan?');
    assert.equal(d.source, 'question');
    assert.deepEqual(d.questions[0].options.map(o => o.id), ['approve', 'revise']);
  });

  it('데몬 카드는 orb 카드 앞에 오고, 데몬이 아는 세션이 연 orbit-pi orb 카드만 뺀다', async () => {
    const t = stubClient({runs: [OPEN_RUN(), run({runId: 'r0', sessionId: 'S-0', state: 'completed'})], history: {r1: [asked('r1', 'd1')]}});
    const {orb} = fakeOrb([
      orbCard({id: 'dup', work: {host: 'orbit-pi', sessionId: 'S-1'}}),
      orbCard({id: 'dup-finished-run', work: {host: 'orbit-pi', sessionId: 'S-0'}}),
      orbCard({id: 'other-session', work: {host: 'orbit-pi', sessionId: 'S-9'}}),
      orbCard({id: 'claude-code', work: {host: 'claude', sessionId: 'S-1'}}),
      orbCard({id: 'no-work'}),
    ]);
    const source = daemonDecisionSource(t.client, orb);
    const ids = (await source.list({state: ['open']})).items.map(d => d.id);
    assert.deepEqual(ids, ['daemon:r1:d1', 'other-session', 'claude-code', 'no-work']);
  });

  it('데몬이 내려가 있거나 짝이 없으면 orb 카드만, 가리지도 않는다', async () => {
    for (const status of [{daemon: 'down' as const}, {paired: false}]) {
      const t = stubClient({status, runs: [OPEN_RUN()]});
      const {orb} = fakeOrb([orbCard({id: 'dup', work: {host: 'orbit-pi', sessionId: 'S-1'}})]);
      const ids = (await daemonDecisionSource(t.client, orb).list({state: ['open']})).items.map(d => d.id);
      assert.deepEqual(ids, ['dup']);
    }
  });

  it('데몬 읽기가 실패하면 orb 카드만 보인다', async () => {
    const t = stubClient({runs: [OPEN_RUN()]});
    t.state.listRunsFails = true;
    const warns: string[] = [];
    const {orb} = fakeOrb([orbCard({id: 'dup', work: {host: 'orbit-pi', sessionId: 'S-1'}})]);
    const ids = (await daemonDecisionSource(t.client, orb, {log: {warn: m => void warns.push(m)}}).list({state: ['open']})).items.map(d => d.id);
    assert.deepEqual(ids, ['dup']);
    assert.ok(warns.some(w => /daemon exploded/.test(w)));
  });

  it('데몬이 되어 있으면 orb 쪽 오류는 로그만 남기고 데몬 카드를 보인다. 데몬이 없으면 그대로 던진다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const {orb} = fakeOrb();
    orb.fail = new Error('orb version mismatch');
    const warns: string[] = [];
    const source = daemonDecisionSource(t.client, orb, {log: {warn: m => void warns.push(m)}});
    assert.deepEqual((await source.list({state: ['open']})).items.map(d => d.id), ['daemon:r1:d1']);
    assert.ok(warns.some(w => /version mismatch/.test(w)));
    t.setStatus({daemon: 'down'});
    await assert.rejects(source.list({state: ['open']}), /version mismatch/);
  });
});

describe('닫힌 카드', () => {
  it('decision.answered는 답한 카드로, 선택지 id면 optionIds, 아니면 글로 오늘 목록에 든다', async () => {
    const openTwo = run({openDecisions: ['d1', 'd2'].map(id => ({id, title: 'Run the plan?', options: ['approve', 'revise'], askedAt: '2026-11-01T01:00:00.000Z'}))});
    const t = stubClient({runs: [openTwo], history: {r1: [asked('r1', 'd1'), asked('r1', 'd2', {}, 3)]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb, {now: () => NOW});
    assert.equal((await source.list({state: ['open']})).items.length, 2);
    t.state.runs = [run()];
    t.fire(ev(11, 'r1', 'decision.answered', {id: 'd1', answer: 'approve'}, '2026-11-01T02:00:00.000Z'));
    t.fire(ev(12, 'r1', 'decision.answered', {id: 'd2', answer: 'use the other plan'}, '2026-11-01T02:05:00.000Z'));
    const open = await source.list({state: ['open']});
    assert.deepEqual(open.items.map(d => d.id), []);
    const closed = (await source.list({state: ['answered', 'withdrawn', 'expired'], updatedSince: SINCE})).items;
    assert.deepEqual(closed.map(d => [d.id, d.state]), [['daemon:r1:d1', 'answered'], ['daemon:r1:d2', 'answered']]);
    assert.deepEqual(closed[0].answer, {answers: [{optionIds: ['approve']}], by: 'daemon', at: '2026-11-01T02:00:00.000Z'});
    assert.deepEqual(closed[1].answer?.answers, [{text: 'use the other plan'}]);
    // 어제 닫힌 것은 오늘 목록에 없다
    const later = await source.list({state: ['answered'], updatedSince: '2026-11-01T02:03:00.000Z'});
    assert.deepEqual(later.items.map(d => d.id), ['daemon:r1:d2']);
  });

  it('실행이 끝나면 남은 열린 카드는 거둔 것으로, ListRuns에서 사라진 카드도 거둔다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb, {now: () => NOW});
    assert.equal((await source.list({state: ['open']})).items.length, 1);
    t.fire(ev(11, 'r1', 'run.finished', {status: 'stopped'}, '2026-11-01T02:00:00.000Z'));
    assert.equal((await source.list({state: ['open']})).items.length, 0);
    const closed = (await source.list({state: ['withdrawn'], updatedSince: SINCE})).items;
    assert.deepEqual(closed.map(d => [d.state, d.closeReason]), [['withdrawn', 'run_finished']]);

    // 데몬이 사건 없이 끝난 실행(interrupted)은 ListRuns로 알아챈다
    const t2 = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const s2 = daemonDecisionSource(t2.client, fakeOrb().orb, {now: () => NOW});
    assert.equal((await s2.list({state: ['open']})).items.length, 1);
    t2.state.runs = [run({state: 'interrupted', finishedAt: '2026-11-01T02:30:00.000Z'})];
    assert.equal((await s2.list({state: ['open']})).items.length, 0);
    assert.equal((await s2.list({state: ['withdrawn'], updatedSince: SINCE})).items[0].closeReason, 'run_ended');
  });

  it('재시작 때 오늘 실행의 history로 닫힌 카드를 복원한다(한 번만 읽는다)', async () => {
    const t = stubClient({
      runs: [run({runId: 'r5', state: 'completed', finishedAt: '2026-11-01T02:10:00.000Z'}), run({runId: 'old', state: 'completed', startedAt: '2026-10-20T01:00:00.000Z', finishedAt: '2026-10-20T02:00:00.000Z'})],
      history: {r5: [asked('r5', 'd1'), ev(5, 'r5', 'decision.answered', {id: 'd1', answer: 'revise'}, '2026-11-01T02:00:00.000Z')], old: [asked('old', 'd1')]},
    });
    const source = daemonDecisionSource(t.client, fakeOrb().orb, {now: () => NOW});
    const closed = (await source.list({state: ['answered'], updatedSince: SINCE})).items;
    assert.deepEqual(closed.map(d => d.id), ['daemon:r5:d1']);
    await source.list({state: ['answered'], updatedSince: SINCE});
    assert.deepEqual(t.historyCalls, ['r5']);
  });
});

describe('최근 50개 밖의 실행', () => {
  const pushedOut = (state: string, id = 'old') => run({runId: id, state, sessionId: 'S-old', lastSeq: 10, openDecisions: state === 'running' ? [{id: 'd1', title: 'x', options: ['approve'], askedAt: '2026-11-01T01:00:00.000Z'}] : []});

  it('밀려난 실행이 아직 도는 중이고 그 결정이 열려 있으면 열린 채 두고, 그 세션의 orb 카드도 가린다', async () => {
    const t = stubClient({runs: [run({runId: 'new', sessionId: 'S-new'})]});
    t.state.extraRuns.old = pushedOut('running');
    const {orb} = fakeOrb([orbCard({id: 'dup', work: {host: 'orbit-pi', sessionId: 'S-old'}})]);
    const source = daemonDecisionSource(t.client, orb, {now: () => NOW});
    t.fire(asked('old', 'd1'));
    const items = (await source.list({state: ['open']})).items;
    assert.deepEqual(items.map(d => d.id), ['daemon:old:d1']);
    assert.deepEqual(t.state.getRunCalls, ['old']);
  });

  it('끝났거나 데몬이 모르는 실행의 열린 카드는 거둔다', async () => {
    const t = stubClient({runs: [run({runId: 'new'})]});
    t.state.extraRuns.done = pushedOut('completed', 'done');
    const source = daemonDecisionSource(t.client, fakeOrb().orb, {now: () => NOW});
    t.fire(asked('done', 'd1'));
    t.fire(asked('ghost', 'd1', {}, 3));
    assert.deepEqual((await source.list({state: ['open']})).items, []);
    const closed = (await source.list({state: ['withdrawn'], updatedSince: SINCE})).items;
    assert.deepEqual(closed.map(d => [d.id, d.closeReason]).sort(), [['daemon:done:d1', 'run_ended'], ['daemon:ghost:d1', 'run_ended']]);
  });

  it('기록이 한도를 넘으면 오래전에 닫힌 카드부터 버리고 열린 카드는 남긴다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb, {now: () => NOW});
    for (let i = 0; i < RECORDS_MAX + 20; i++) {
      t.fire(asked('c', `x${i}`, {}, 100 + i));
      t.fire(ev(1000 + i, 'c', 'decision.answered', {id: `x${i}`, answer: 'approve'}, new Date(Date.UTC(2026, 10, 1, 1, 0, 0) + i * 1000).toISOString()));
    }
    const open = (await source.list({state: ['open']})).items;
    assert.deepEqual(open.map(d => d.id), ['daemon:r1:d1']);
    const closed = (await source.list({state: ['answered'], updatedSince: '2000-01-01T00:00:00.000Z'})).items;
    assert.equal(closed.length, RECORDS_MAX - 1);
    assert.ok(!closed.some(d => d.id === 'daemon:c:x0'));
    assert.ok(closed.some(d => d.id === `daemon:c:x${RECORDS_MAX + 19}`));
  });
});

describe('답하기', () => {
  it('데몬 카드는 AnswerDecision으로 가고 곧바로 답한 카드가 된다. orb 카드는 orb로 간다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const {orb, calls} = fakeOrb();
    const source = daemonDecisionSource(t.client, orb, {now: () => NOW});
    await source.list({state: ['open']});
    const out = await source.answer('daemon:r1:d1', [{optionIds: ['approve']}]);
    assert.deepEqual(t.answers, [{runId: 'r1', decisionId: 'd1', answer: 'approve'}]);
    assert.equal(out.ok, true);
    assert.equal(out.ok && out.decision?.state, 'answered');
    assert.equal(out.ok && out.decision?.answer?.by, 'studio');
    assert.equal((await source.list({state: ['open']})).items.length, 0);
    await source.answer('some-orb-id', [{optionIds: ['x']}]);
    assert.deepEqual(calls.filter(c => c.startsWith('answer')), ['answer:some-orb-id']);
  });

  it('글로 답하면 그 글이 간다. 비었으면 보내지 않는다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    await source.list({state: ['open']});
    await source.answer('daemon:r1:d1', [{text: 'use plan B'}]);
    assert.equal(t.answers[0].answer, 'use plan B');
    const empty = await source.answer('daemon:r1:d1', [{}]);
    assert.equal(!empty.ok && empty.code, 'FAILED');
    assert.equal(t.answers.length, 1);
  });

  it('accepted=false는 CONFLICT이고 카드는 열린 채 남아 목록을 다시 읽게 한다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    await source.list({state: ['open']});
    t.state.accepted = false;
    const out = await source.answer('daemon:r1:d1', [{optionIds: ['approve']}]);
    assert.equal(!out.ok && out.code, 'CONFLICT');
  });

  it('데몬 오류는 FAILED, 없는 실행(RPC_NOT_FOUND)은 NOT_FOUND', async () => {
    const t = stubClient();
    t.client.api.answer = async () => { throw Object.assign(new Error('run gone'), {code: 'RPC_NOT_FOUND'}); };
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    const out = await source.answer('daemon:zz:d1', [{optionIds: ['approve']}]);
    assert.deepEqual(!out.ok && [out.code, out.message], ['NOT_FOUND', 'run gone']);
    t.client.api.answer = async () => { throw new Error('boom'); };
    assert.equal((await source.answer('daemon:zz:d1', [{optionIds: ['approve']}]) as {code: string}).code, 'FAILED');
  });
});

describe('변경 알림', () => {
  it('데몬 사건이 orb 장기 대기를 깨운다: orb 호출은 끊고, 같은 orb seq로 다시 걸게 한다', async () => {
    const t = stubClient({runs: [OPEN_RUN()], history: {r1: [asked('r1', 'd1')]}});
    const {orb, calls} = fakeOrb();
    const source = daemonDecisionSource(t.client, orb);
    const page = await source.list({state: ['open']});
    assert.equal(page.seq, 'o1|0');
    const waiting = source.changes({after: page.seq, wait: 30000}, new AbortController().signal);
    await new Promise(r => setTimeout(r, 10));
    t.fire(ev(11, 'r1', 'decision.answered', {id: 'd1', answer: 'approve'}));
    const res = await waiting;
    assert.deepEqual(res.decisionIds, ['daemon:r1:d1']);
    assert.equal(res.seq, 'o1|1');
    assert.equal(res.reset, false);
    assert.ok(calls.includes('changes:o1:30000'));
    // 이미 쌓인 변경은 기다리지 않고 돌려준다
    t.fire(ev(12, 'r1', 'run.finished'));
    t.fire(ev(13, 'other', 'decision.asked', {id: 'd9', title: 'x', options: []}, '2026-11-01T02:00:00.000Z'));
    const quick = await source.changes({after: res.seq, wait: 30000}, new AbortController().signal);
    assert.deepEqual(quick.decisionIds, ['daemon:other:d9']);
  });

  it('orb 쪽 변경은 그대로 전해 주고 seq에 데몬 번호를 붙인다', async () => {
    const t = stubClient();
    const {orb} = fakeOrb();
    const source = daemonDecisionSource(t.client, orb);
    const waiting = source.changes({after: 'o1|0', wait: 30000}, new AbortController().signal);
    await new Promise(r => setTimeout(r, 5));
    orb.resolveChanges!({seq: 'o2', decisionIds: ['d-7']});
    const res = await waiting;
    assert.deepEqual([res.seq, res.decisionIds, res.waitMax], ['o2|0', ['d-7'], 30000]);
  });

  it('데몬이 올라오거나 내려가면 목록 전체를 다시 읽게 한다(reset)', async () => {
    const t = stubClient({status: {daemon: 'down'}});
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    const waiting = source.changes({after: 'o1|0', wait: 30000}, new AbortController().signal);
    await new Promise(r => setTimeout(r, 5));
    t.setStatus({daemon: 'up', paired: true});
    assert.equal((await waiting).reset, true);
  });

  it('중단하면 끝난다', async () => {
    const t = stubClient();
    const source = daemonDecisionSource(t.client, fakeOrb().orb);
    const ac = new AbortController();
    const waiting = source.changes({after: 'o1|0', wait: 30000}, ac.signal);
    ac.abort();
    await assert.rejects(waiting);
  });
});

describe('저장소와 함께', () => {
  it('데몬 사건이 오면 저장소가 다시 읽어 열린 카드가 늘고 줄어든다', async () => {
    const t = stubClient({runs: [run()], history: {r1: []}});
    const {orb} = fakeOrb();
    const source = daemonDecisionSource(t.client, orb, {now: () => NOW});
    const store = createDecisionStore(source, {now: () => NOW});
    await store.start();
    assert.deepEqual(store.getState().open, []);
    t.state.runs = [OPEN_RUN()];
    t.fire(asked('r1', 'd1'));
    const end = Date.now() + 2000;
    while (store.getState().open.length !== 1 && Date.now() < end) await new Promise(r => setTimeout(r, 10));
    assert.deepEqual(store.getState().open.map(d => d.id), ['daemon:r1:d1']);
    assert.deepEqual([...store.openRefs().sessionIds], ['S-1']);
    t.state.runs = [run()];
    t.fire(ev(11, 'r1', 'decision.answered', {id: 'd1', answer: 'approve'}));
    const end2 = Date.now() + 2000;
    while (store.getState().open.length !== 0 && Date.now() < end2) await new Promise(r => setTimeout(r, 10));
    assert.equal(store.getState().open.length, 0);
    assert.equal(store.getState().closed.length, 1);
    store.dispose();
    source.dispose();
  });
});

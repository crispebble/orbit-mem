// 뒤쪽 프로세스(데몬 클라이언트): 가짜 데몬(진짜 Connect 서버)과 가짜 orb(셸 스크립트), 임시 ORBIT_HOME으로 시험한다.
import assert from 'node:assert/strict';
import {afterEach, describe, it} from 'node:test';
import {Code, ConnectError} from '@connectrpc/connect';
import {DAEMON_METHODS, type DaemonEvent, type DaemonStatus} from '../src/daemon/api.ts';
import {TOKEN_SECRET, createDaemonBackend} from '../src/backend/service.ts';
import {fakeDaemon, fakeHome, type FakeDaemon, type FakeHome} from './fake-daemon.ts';

const TOKEN = 'tok-secret-0123456789';
const call = {signal: new AbortController().signal};

async function until(check: () => boolean, what = 'condition', ms = 4000) {
  const end = Date.now() + ms;
  while (!check()) {
    if (Date.now() > end) assert.fail(`timed out: ${what}`);
    await new Promise(r => setTimeout(r, 10));
  }
}

const cleanups: (() => Promise<void> | void)[] = [];
afterEach(async () => {
  while (cleanups.length) await cleanups.pop()!();
});

interface Rig {
  home: FakeHome;
  daemon: FakeDaemon;
  secrets: Map<string, string>;
  emitted: {event: string; payload: unknown}[];
  logs: string[];
  statuses(): DaemonStatus[];
  events(): DaemonEvent[];
  backend: ReturnType<typeof createDaemonBackend>;
  m<T = any>(name: string, params?: unknown): Promise<T>;
  everything(): string;
}

async function rig(opts: {paired?: boolean; published?: boolean; secretsSet?: boolean; lastSeq?: number; pollMs?: number} = {}): Promise<Rig> {
  const home = fakeHome();
  const daemon = await fakeDaemon({lastSeq: opts.lastSeq ?? 0});
  daemon.tokens.add(TOKEN);
  home.issue(TOKEN);
  home.startsAt(daemon.url);
  if (opts.published ?? true) home.publish(daemon.url);
  const secrets = new Map<string, string>();
  if (opts.paired) secrets.set(TOKEN_SECRET, TOKEN);
  const emitted: Rig['emitted'] = [];
  const logs: string[] = [];
  const backend = createDaemonBackend({
    secrets: {
      get: async n => secrets.get(n) ?? null,
      ...(opts.secretsSet === false ? {} : {set: async (n: string, v: string) => void secrets.set(n, v), delete: async (n: string) => void secrets.delete(n)}),
    },
    emit: (event, payload) => void emitted.push({event, payload}),
    log: {info: m => void logs.push(m), warn: m => void logs.push(m), error: m => void logs.push(m)},
    env: home.env,
    pollMs: opts.pollMs ?? 30,
    backoffMs: {min: 20, max: 80},
    startTimeoutMs: 10_000,
  });
  cleanups.push(async () => {
    await backend.stop();
    await daemon.close();
    home.cleanup();
  });
  const r: Rig = {
    home, daemon, secrets, emitted, logs, backend,
    statuses: () => emitted.filter(e => e.event === 'daemon.status').map(e => e.payload as DaemonStatus),
    events: () => emitted.filter(e => e.event === 'daemon.event').map(e => e.payload as DaemonEvent),
    m: (name, params) => Promise.resolve(backend.methods[name](params, call)) as never,
    // 창·로그·사건·결과로 나간 모든 글
    everything: () => JSON.stringify([emitted, logs]),
  };
  return r;
}

describe('뒤쪽: 찾기와 상태', () => {
  it('status는 데몬을 띄우지 않는다: daemon.json이 없으면 down이고 orb를 부르지 않는다', async () => {
    const r = await rig({published: false});
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.status);
    assert.deepEqual(s, {daemon: 'down', streaming: false, paired: false, lastSeq: 0});
    assert.deepEqual(r.home.orbCalls(), []);
  });

  it('start만으로는 데몬을 띄우지 않고, 토큰이 있으면 up이 되어 status 사건을 보낸다', async () => {
    const r = await rig({paired: true, published: false});
    await r.backend.start();
    await new Promise(res => setTimeout(res, 100));
    assert.deepEqual(r.home.orbCalls(), []);
    r.home.publish(r.daemon.url);
    await until(() => r.statuses().some(s => s.daemon === 'up' && s.streaming), '올라온 데몬에 붙음');
    const up = r.statuses().find(s => s.daemon === 'up' && s.paired)!;
    assert.equal(up.url, r.daemon.url);
    assert.equal(up.version, '9.9.9');
  });

  it('토큰이 거절되면 데몬은 up이지만 paired는 false다', async () => {
    const r = await rig({paired: true});
    r.daemon.tokens.delete(TOKEN);
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.status);
    assert.equal(s.daemon, 'up');
    assert.equal(s.paired, false);
  });
});

describe('뒤쪽: ensure와 짝짓기', () => {
  it('데몬이 내려가 있으면 orb daemon start 뒤 기기를 거두고 더해 토큰을 비밀값에 둔다', async () => {
    const r = await rig({published: false});
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.ensure);
    assert.equal(s.daemon, 'up');
    assert.equal(s.paired, true);
    assert.deepEqual(r.home.orbCalls(), [
      'daemon start',
      'daemon device revoke orbit-studio',
      'daemon device add orbit-studio --scope read,answer,run,admin',
    ]);
    assert.equal(r.secrets.get(TOKEN_SECRET), TOKEN);
    assert.ok(r.statuses().some(x => x.daemon === 'starting'));
  });

  it('토큰은 로그·사건·결과에 실리지 않는다', async () => {
    const r = await rig({published: false});
    const result = await r.m(DAEMON_METHODS.ensure);
    await r.backend.start();
    await r.m(DAEMON_METHODS.listRuns, {});
    r.daemon.push('run-x', 'run.started');
    await until(() => r.events().length === 1, '사건 하나');
    assert.ok(!JSON.stringify(result).includes(TOKEN));
    assert.ok(!r.everything().includes(TOKEN), r.everything());
  });

  it('이미 짝지어져 있고 떠 있으면 orb를 부르지 않는다', async () => {
    const r = await rig({paired: true});
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.ensure);
    assert.equal(s.paired, true);
    assert.deepEqual(r.home.orbCalls(), []);
  });

  it('떠 있지만 토큰이 없으면 시작하지 않고 짝짓기만 한다', async () => {
    const r = await rig();
    await r.m(DAEMON_METHODS.ensure);
    assert.deepEqual(r.home.orbCalls().map(c => c.split(' ').slice(0, 3).join(' ')), ['daemon device revoke', 'daemon device add']);
    assert.equal(r.secrets.get(TOKEN_SECRET), TOKEN);
  });

  it('거절된 옛 토큰은 새로 받는다', async () => {
    const r = await rig({paired: true});
    r.secrets.set(TOKEN_SECRET, 'stale-token-000000');
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.ensure);
    assert.equal(s.paired, true);
    assert.equal(r.secrets.get(TOKEN_SECRET), TOKEN);
  });

  it('start가 실패하면 DAEMON_START_FAILED와 출력 끝줄', async () => {
    const r = await rig({published: false});
    r.home.failStart(true);
    await assert.rejects(r.m(DAEMON_METHODS.ensure), (e: any) => e.code === 'DAEMON_START_FAILED' && /cannot bind/.test(e.message));
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.status);
    assert.equal(s.daemon, 'down');
    assert.equal(s.error?.code, 'DAEMON_START_FAILED');
  });

  it('토큰을 받지 못하면 한 번 다시 해 보고 PAIR_FAILED, 비밀값은 두지 않는다', async () => {
    const r = await rig();
    r.home.failAdd(true);
    await assert.rejects(r.m(DAEMON_METHODS.ensure), (e: any) => e.code === 'PAIR_FAILED' && /device add failed/.test(e.message));
    assert.equal(r.home.orbCalls().filter(c => c.startsWith('daemon device add')).length, 2);
    assert.equal(r.secrets.has(TOKEN_SECRET), false);
  });

  it('새 토큰도 거절되면 PAIR_FAILED이고 토큰 글은 오류에 없다', async () => {
    const r = await rig();
    r.home.issue('tok-not-accepted-123456');
    await assert.rejects(r.m(DAEMON_METHODS.ensure), (e: any) => e.code === 'PAIR_FAILED' && !e.message.includes('tok-not-accepted-123456'));
  });

  it('비밀값을 둘 수 없는 옛 Studio는 STUDIO_TOO_OLD이고 orb로 기기를 만들지 않는다', async () => {
    const r = await rig({secretsSet: false});
    await assert.rejects(r.m(DAEMON_METHODS.ensure), (e: any) => e.code === 'STUDIO_TOO_OLD');
    assert.deepEqual(r.home.orbCalls(), []);
  });

  it('orb가 없으면 ORB_MISSING', async () => {
    const r = await rig({published: false});
    r.home.env.ORBIT_ORB = '/nonexistent/orb';
    await assert.rejects(r.m(DAEMON_METHODS.ensure), (e: any) => e.code === 'ORB_MISSING');
  });

  it('ensure가 겹쳐도 짝짓기는 한 번이다', async () => {
    const r = await rig();
    await Promise.all([r.m(DAEMON_METHODS.ensure), r.m(DAEMON_METHODS.ensure)]);
    assert.equal(r.home.orbCalls().filter(c => c.startsWith('daemon device add')).length, 1);
  });
});

describe('뒤쪽: 전역 사건 스트림', () => {
  it('처음 붙을 때는 과거를 다시 보내지 않고 Health.last_seq부터 사건을 보낸다(json은 파싱)', async () => {
    const r = await rig({paired: true});
    r.daemon.push('old', 'run.started');
    r.daemon.push('old', 'run.finished', {status: 'completed'});
    await r.backend.start();
    await until(() => r.daemon.openStreams() === 1, '스트림 열림');
    assert.deepEqual(r.daemon.watches[0], {runId: '', afterSeq: 2});
    r.daemon.push('run-1', 'decision.asked', {id: 'd1', title: 'Go?', options: ['approve'], key: 'decision.plan', args: ['x']});
    await until(() => r.events().length === 1, '사건');
    const e = r.events()[0];
    assert.equal(e.seq, 3);
    assert.equal(e.runId, 'run-1');
    assert.equal(e.event.type, 'decision.asked');
    assert.deepEqual(e.event.args, ['x']);
    const s = await r.m<DaemonStatus>(DAEMON_METHODS.status);
    assert.equal(s.lastSeq, 3);
    assert.equal(s.streaming, true);
  });

  it('끊기면 마지막 seq부터 다시 열어 빠짐도 겹침도 없다', async () => {
    const r = await rig({paired: true});
    await r.backend.start();
    await until(() => r.daemon.openStreams() === 1, '스트림 열림');
    r.daemon.push('a', 'run.started');
    r.daemon.push('a', 'step.started');
    await until(() => r.events().length === 2, '두 사건');
    r.daemon.dropStreams();
    r.daemon.push('a', 'step.finished');
    r.daemon.push('a', 'run.finished', {status: 'completed'});
    await until(() => r.events().length === 4, '끊긴 동안의 사건');
    r.daemon.push('b', 'run.started');
    await until(() => r.events().length === 5, '다시 붙은 뒤 사건');
    assert.deepEqual(r.events().map(e => e.seq), [1, 2, 3, 4, 5]);
    // 다시 연 요청은 끊기기 전 마지막 seq(2)부터였다
    assert.deepEqual(r.daemon.watches.map(w => w.afterSeq).slice(0, 2), [0, 2]);
  });

  it('데몬이 죽었다 돌아오면 확인해 다시 붙는다', async () => {
    const r = await rig({paired: true});
    await r.backend.start();
    await until(() => r.daemon.openStreams() === 1);
    r.home.unpublish();
    r.daemon.dropStreams();
    await until(() => r.statuses().at(-1)?.daemon === 'down', 'down 보고');
    r.home.publish(r.daemon.url);
    await until(() => r.statuses().at(-1)?.streaming === true, '다시 붙음');
    r.daemon.push('c', 'run.started');
    await until(() => r.events().length === 1);
  });
});

describe('뒤쪽: history', () => {
  it('실행의 사건을 GetRun.last_seq까지 모아 돌려주고 스트림을 끊는다', async () => {
    const r = await rig({paired: true});
    r.daemon.runs.set('r1', {runId: 'r1', sessionId: 's1'});
    r.daemon.push('other', 'run.started');
    for (const t of ['run.started', 'plan.proposed', 'step.started', 'step.finished']) r.daemon.push('r1', t);
    const out = await r.m<{events: DaemonEvent[]}>(DAEMON_METHODS.history, {runId: 'r1', afterSeq: 2});
    assert.deepEqual(out.events.map(e => [e.seq, e.type]), [[3, 'plan.proposed'], [4, 'step.started'], [5, 'step.finished']]);
    assert.deepEqual(r.daemon.watches.at(-1), {runId: 'r1', afterSeq: 2});
    await until(() => r.daemon.openStreams() === 0, '스트림 끊김');
  });

  it('이미 다 받았으면 스트림을 열지 않는다', async () => {
    const r = await rig({paired: true});
    r.daemon.runs.set('r1', {runId: 'r1'});
    r.daemon.push('r1', 'run.started');
    const out = await r.m<{events: DaemonEvent[]}>(DAEMON_METHODS.history, {runId: 'r1', afterSeq: 1});
    assert.deepEqual(out.events, []);
    assert.equal(r.daemon.watches.length, 0);
  });
});

describe('뒤쪽: 메서드', () => {
  it('startRun은 source=studio, mode를 플래그로, 0인 한도는 비운다', async () => {
    const r = await rig({paired: true});
    const out = await r.m(DAEMON_METHODS.startRun, {request: 'fix it', cwd: '/w', mode: 'ultrawork', limits: {maxTurns: 0, maxCostUsd: 2}, resume: {}});
    assert.deepEqual(out, {runId: 'run-1', watchAfterSeq: 0});
    const req = r.daemon.startRuns[0];
    assert.equal(req.source, 'studio');
    assert.equal(req.ultrawork, true);
    assert.equal(req.plan, false);
    assert.equal(req.limits?.maxCostUsd, 2);
    assert.equal(req.limits?.maxTurns, 0);
    assert.equal(req.resume?.workId, '');
    await r.m(DAEMON_METHODS.startRun, {request: 'x', cwd: '/w', mode: 'plan'});
    const plan = r.daemon.startRuns[1];
    assert.equal(plan.plan, true);
    assert.equal(plan.limits, undefined);
    assert.equal(plan.resume, undefined);
  });

  it('잘못된 입력은 RPC_INVALID_ARGUMENT', async () => {
    const r = await rig({paired: true});
    await assert.rejects(r.m(DAEMON_METHODS.startRun, {request: 'x', cwd: '/w', mode: 'wild'}), (e: any) => e.code === 'RPC_INVALID_ARGUMENT');
    await assert.rejects(r.m(DAEMON_METHODS.startRun, {request: 'x'}), (e: any) => e.code === 'RPC_INVALID_ARGUMENT');
  });

  it('Connect 오류는 RPC_<CODE>, 데몬이 없으면 DAEMON_DOWN, 짝이 없으면 RPC_UNAUTHENTICATED', async () => {
    const r = await rig({paired: true});
    r.daemon.fail.set('saveProfiles', new ConnectError('profiles changed', Code.Aborted));
    await assert.rejects(r.m(DAEMON_METHODS.saveProfiles, {file: {version: 1, profiles: []}, expectedRevision: 'r1'}), (e: any) => e.code === 'RPC_ABORTED' && /profiles changed/.test(e.message));
    await assert.rejects(r.m(DAEMON_METHODS.getRun, {runId: 'nope'}), (e: any) => e.code === 'RPC_NOT_FOUND');
    r.home.unpublish();
    await assert.rejects(r.m(DAEMON_METHODS.listRuns, {}), (e: any) => e.code === 'DAEMON_DOWN');
    const unpaired = await rig();
    await assert.rejects(unpaired.m(DAEMON_METHODS.listRuns, {}), (e: any) => e.code === 'RPC_UNAUTHENTICATED');
  });

  it('answer, stopRun, listRuns(숫자는 Number), getUsage, getProfiles, messages', async () => {
    const r = await rig({paired: true});
    r.daemon.runs.set('r1', {runId: 'r1', sessionId: 's1', workId: 'w1', openDecisions: [{id: 'd1', title: 'Go?', options: ['approve', 'cancel'], askedAt: '2026-11-01T01:00:00.000Z'}]});
    r.daemon.push('r1', 'run.started');
    assert.deepEqual(await r.m(DAEMON_METHODS.answer, {runId: 'r1', decisionId: 'd1', answer: 'approve'}), {accepted: true});
    r.daemon.accept = false;
    assert.deepEqual(await r.m(DAEMON_METHODS.answer, {runId: 'r1', decisionId: 'd1', answer: 'approve'}), {accepted: false});
    assert.deepEqual(await r.m(DAEMON_METHODS.stopRun, {runId: 'r1'}), {stopping: true});
    const {runs} = await r.m(DAEMON_METHODS.listRuns, {activeOnly: true});
    assert.equal(runs[0].sessionId, 's1');
    assert.equal(runs[0].lastSeq, 1);
    assert.equal(typeof runs[0].totals.inputTokens, 'number');
    assert.equal(runs[0].totals.inputTokens, 1000);
    assert.deepEqual(runs[0].openDecisions[0].options, ['approve', 'cancel']);
    const usage = await r.m(DAEMON_METHODS.getUsage, {});
    assert.equal(usage.profiles[0].models[0].inputTokens, 5);
    const view = await r.m(DAEMON_METHODS.getProfiles);
    assert.equal(view.file.profiles[0].name, 'main');
    assert.equal(view.keys[0].keys[0].set, true);
    const ko = await r.m(DAEMON_METHODS.messages, {lang: 'ko'});
    assert.equal(ko.messages['option.approve'], '승인');
  });

  it('SetApiKey 값은 데몬에만 가고 로그·사건·오류에 남지 않는다', async () => {
    const r = await rig({paired: true});
    const KEY = 'sk-or-very-secret-value';
    await r.backend.start();
    assert.deepEqual(await r.m(DAEMON_METHODS.setApiKey, {profile: 'main', provider: 'openrouter', value: KEY}), {revision: 'r3'});
    assert.deepEqual(r.daemon.setKeys, [{profile: 'main', provider: 'openrouter', value: KEY}]);
    r.daemon.fail.set('setApiKey', new ConnectError(`bad key ${KEY}`, Code.InvalidArgument));
    await assert.rejects(r.m(DAEMON_METHODS.setApiKey, {profile: 'main', provider: 'openrouter', value: KEY}), (e: any) => e.code === 'RPC_INVALID_ARGUMENT' && !e.message.includes(KEY));
    assert.ok(!r.everything().includes(KEY));
  });

  it('데몬이 토큰을 거절하면 paired가 false가 되고 RPC_UNAUTHENTICATED', async () => {
    const r = await rig({paired: true});
    await r.m(DAEMON_METHODS.status);
    r.daemon.tokens.delete(TOKEN);
    await assert.rejects(r.m(DAEMON_METHODS.listRuns, {}), (e: any) => e.code === 'RPC_UNAUTHENTICATED');
    assert.equal(r.statuses().at(-1)?.paired, false);
  });
});

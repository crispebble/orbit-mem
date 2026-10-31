// 뒤쪽·결정 출처 시험용 가짜 데몬: 진짜 Connect 서버(HostService)를 시험 안에서 띄우고, 가짜 orb(셸 스크립트)와 임시 ORBIT_HOME을 만든다.
// 실제 ~/Library/Application Support/orbit과 실제 키체인은 열지 않는다.
import {create} from '@bufbuild/protobuf';
import {Code, ConnectError, type ConnectRouter} from '@connectrpc/connect';
import {connectNodeAdapter} from '@connectrpc/connect-node';
import {chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {createServer, type Server} from 'node:http';
import type {AddressInfo} from 'node:net';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {
  EventSchema, HealthResponseSchema, HostService, RunSummarySchema,
  type Event, type StartRunRequest,
} from '../src/daemon/gen/host_pb.ts';

export interface FakeRun {
  runId: string;
  sessionId?: string;
  workId?: string;
  state?: string;
  request?: string;
  cwd?: string;
  openDecisions?: {id: string; title: string; options: string[]; askedAt: string}[];
}

export interface FakeDaemon {
  url: string;
  /** 이 토큰들만 받는다 */
  tokens: Set<string>;
  /** 사건을 기록하고 열린 스트림에 보낸다. seq는 1씩 늘어난다 */
  push(runId: string, type: string, body?: Record<string, unknown>): Event;
  lastSeq(): number;
  runs: Map<string, FakeRun>;
  startRuns: StartRunRequest[];
  answers: {runId: string; decisionId: string; answer: string}[];
  setKeys: {profile: string; provider: string; value: string}[];
  /** answerDecision의 accepted 값 */
  accept: boolean;
  /** GetProfiles가 돌려줄 json(기본: 정상 파일) */
  profilesJson?: string;
  /** 다음 호출들에 오류를 낸다(메서드 이름 → 오류) */
  fail: Map<string, ConnectError>;
  /** 열린 WatchEvents를 모두 끊는다 */
  dropStreams(): void;
  openStreams(): number;
  /** WatchEvents 요청들(run_id, after_seq) */
  watches: {runId: string; afterSeq: number}[];
  close(): Promise<void>;
}

export async function fakeDaemon(init: {lastSeq?: number} = {}): Promise<FakeDaemon> {
  const events: Event[] = [];
  let seq = init.lastSeq ?? 0;
  const subs = new Set<(e: Event) => void>();
  const aborts = new Set<() => void>();
  const d: FakeDaemon = {
    url: '', tokens: new Set(), runs: new Map(), startRuns: [], answers: [], setKeys: [], accept: true, fail: new Map(), watches: [],
    push(runId, type, body = {}) {
      const at = new Date(Date.UTC(2026, 10, 1, 1, 0, seq)).toISOString();
      const e = create(EventSchema, {seq: BigInt(++seq), runId, type, at, json: JSON.stringify({type, at, ...body})});
      events.push(e);
      subs.forEach(s => s(e));
      return e;
    },
    lastSeq: () => seq,
    dropStreams: () => aborts.forEach(a => a()),
    openStreams: () => aborts.size,
    close: async () => {
      d.dropStreams();
      server.closeAllConnections();
      await new Promise<void>(r => server.close(() => r()));
    },
  };
  const check = (name: string) => {
    const e = d.fail.get(name);
    if (e) throw e;
  };
  const summary = (r: FakeRun) => create(RunSummarySchema, {
    runId: r.runId, request: r.request ?? 'do it', cwd: r.cwd ?? '/work', state: r.state ?? 'running', mode: 'default', sessionId: r.sessionId ?? '', workId: r.workId ?? '',
    startedAt: '2026-11-01T01:00:00.000Z', source: 'studio', device: 'orbit-studio',
    lastSeq: BigInt(events.filter(e => e.runId === r.runId).at(-1)?.seq ?? 0n),
    totals: {costUsd: 0.5, turns: 3, minutes: 1.5, inputTokens: 1000n, outputTokens: 200n},
    openDecisions: r.openDecisions ?? [],
  });
  const routes = (router: ConnectRouter) => router.service(HostService, {
    async health() {
      check('health');
      return create(HealthResponseSchema, {version: '9.9.9', pid: process.pid, device: 'orbit-studio', lastSeq: BigInt(seq), listen: d.url.replace('http://', '')});
    },
    async startRun(req) {
      check('startRun');
      d.startRuns.push(req);
      const runId = `run-${d.startRuns.length}`;
      d.runs.set(runId, {runId, request: req.request, cwd: req.cwd});
      return {runId, watchAfterSeq: BigInt(seq)};
    },
    async stopRun() {
      check('stopRun');
      return {stopping: true};
    },
    async answerDecision(req) {
      check('answerDecision');
      d.answers.push({runId: req.runId, decisionId: req.decisionId, answer: req.answer});
      return {accepted: d.accept};
    },
    async *watchEvents(req, ctx) {
      check('watchEvents');
      const after = Number(req.afterSeq);
      d.watches.push({runId: req.runId, afterSeq: after});
      const queue: Event[] = events.filter(e => Number(e.seq) > after && (!req.runId || e.runId === req.runId));
      let wake: (() => void) | undefined;
      let dropped = false;
      const sub = (e: Event) => {
        if (!req.runId || e.runId === req.runId) queue.push(e);
        wake?.();
      };
      const drop = () => { dropped = true; wake?.(); };
      subs.add(sub);
      aborts.add(drop);
      ctx.signal.addEventListener('abort', drop);
      try {
        while (!dropped) {
          const e = queue.shift();
          if (e) {
            yield {event: e};
            if (req.runId && e.type === 'run.finished') return;
            continue;
          }
          await new Promise<void>(r => { wake = r; });
        }
        if (dropped && !ctx.signal.aborted) throw new ConnectError('stream dropped', Code.Unavailable);
      } finally {
        subs.delete(sub);
        aborts.delete(drop);
      }
    },
    async listRuns(req) {
      check('listRuns');
      const all = [...d.runs.values()].filter(r => !req.activeOnly || (r.state ?? 'running') === 'running');
      return {runs: all.map(summary)};
    },
    async getRun(req) {
      check('getRun');
      const r = d.runs.get(req.runId);
      if (!r) throw new ConnectError(`run ${req.runId} not found`, Code.NotFound);
      return {run: summary(r)};
    },
    async getUsage() {
      check('getUsage');
      return {month: '2026-11', profiles: [{profile: 'main', spentUsd: 1.5, limitUsd: 10, models: [{engine: 'pi', model: 'x', billing: 'api', costUsd: 1, inputTokens: 5n, outputTokens: 6n, runs: 2}]}]};
    },
    async getProfiles() {
      check('getProfiles');
      return {json: d.profilesJson ?? JSON.stringify({version: 1, profiles: [{name: 'main'}]}), revision: 'r1', path: '/p/profiles.json', keys: [{profile: 'main', keys: [{provider: 'openrouter', set: true}]}], keyStore: 'file'};
    },
    async saveProfiles() {
      check('saveProfiles');
      return {revision: 'r2'};
    },
    async setApiKey(req) {
      check('setApiKey');
      d.setKeys.push({profile: req.profile, provider: req.provider, value: req.value});
      return {revision: 'r3'};
    },
    async deleteApiKey() {
      check('deleteApiKey');
      return {deleted: true, revision: 'r4'};
    },
    async getMessages(req) {
      check('getMessages');
      return {lang: req.lang === 'ko' ? 'ko' : 'en', messages: {'option.approve': req.lang === 'ko' ? '승인' : 'Approve', 'decision.plan': 'Run the plan %s?'}};
    },
  });
  const handler = connectNodeAdapter({
    routes,
    interceptors: [next => async req => {
      const auth = req.header.get('authorization') ?? '';
      const token = auth.startsWith('Bearer ') ? auth.slice(7) : '';
      if (!d.tokens.has(token)) throw new ConnectError('unauthenticated', Code.Unauthenticated);
      return next(req);
    }],
  });
  const server: Server = createServer(handler);
  await new Promise<void>(r => server.listen(0, '127.0.0.1', () => r()));
  d.url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return d;
}

export interface FakeHome {
  /** 시험 전용 ORBIT_HOME */
  home: string;
  dir: string;
  env: Record<string, string | undefined>;
  /** daemon.json을 둔다(데몬이 떠 있는 것처럼) */
  publish(url: string): void;
  unpublish(): void;
  /** 가짜 orb가 받은 인자 줄들 */
  orbCalls(): string[];
  /** 가짜 orb `daemon device add`가 찍을 토큰 */
  issue(token: string): void;
  /** 가짜 orb의 `daemon start`가 daemon.json을 둘 대상 */
  startsAt(url: string): void;
  failStart(on: boolean): void;
  failAdd(on: boolean): void;
  cleanup(): void;
}

const ORB_SCRIPT = `#!/bin/sh
echo "$@" >> "$FAKE_ORB_DIR/calls.log"
case "$1 $2 $3" in
  "daemon start"*)
    if [ -f "$FAKE_ORB_DIR/start-fail" ]; then echo "starting" >&2; echo "boom: cannot bind 127.0.0.1" >&2; exit 1; fi
    mkdir -p "$ORBIT_HOME/runtime/daemon"
    cp "$FAKE_ORB_DIR/daemon.json" "$ORBIT_HOME/runtime/daemon/daemon.json"
    echo "started at $(cat "$FAKE_ORB_DIR/daemon.json")"
    exit 0;;
  "daemon device revoke")
    if [ -f "$FAKE_ORB_DIR/token" ]; then echo "revoked $4"; exit 0; fi
    echo "no device $4" >&2; exit 1;;
  "daemon device add")
    if [ -f "$FAKE_ORB_DIR/add-fail" ]; then echo "device add failed" >&2; exit 1; fi
    cat "$FAKE_ORB_DIR/token"
    exit 0;;
esac
exit 2
`;

export function fakeHome(): FakeHome {
  const dir = mkdtempSync(join(tmpdir(), 'orbit-plugin-test-'));
  const home = join(dir, 'orbit-home');
  const bin = join(dir, 'orb');
  mkdirSync(home, {recursive: true});
  writeFileSync(bin, ORB_SCRIPT);
  chmodSync(bin, 0o755);
  const info = (url: string) => JSON.stringify({pid: process.pid, url, listen: url.replace('http://', ''), version: '9.9.9', startedAt: '2026-11-01T00:00:00.000Z'});
  const infoPath = join(home, 'runtime', 'daemon', 'daemon.json');
  const flag = (name: string, on: boolean) => (on ? writeFileSync(join(dir, name), '') : rmSync(join(dir, name), {force: true}));
  return {
    home, dir,
    env: {ORBIT_HOME: home, ORBIT_ORB: bin, FAKE_ORB_DIR: dir, PATH: process.env.PATH, HOME: dir},
    publish(url) {
      mkdirSync(join(home, 'runtime', 'daemon'), {recursive: true});
      writeFileSync(infoPath, info(url));
    },
    unpublish: () => rmSync(infoPath, {force: true}),
    orbCalls: () => (existsSync(join(dir, 'calls.log')) ? readFileSync(join(dir, 'calls.log'), 'utf8').split('\n').filter(Boolean) : []),
    issue: token => writeFileSync(join(dir, 'token'), `${token}\n`),
    startsAt: url => writeFileSync(join(dir, 'daemon.json'), info(url)),
    failStart: on => flag('start-fail', on),
    failAdd: on => flag('add-fail', on),
    cleanup: () => rmSync(dir, {recursive: true, force: true}),
  };
}

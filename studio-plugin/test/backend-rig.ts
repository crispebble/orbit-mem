// 뒤쪽 시험들이 함께 쓰는 장치: 가짜 데몬 + 가짜 orb + 임시 ORBIT_HOME + 가짜 비밀값 저장소로 만든 뒤쪽.
import assert from 'node:assert/strict';
import {afterEach} from 'node:test';
import type {DaemonEvent, DaemonStatus} from '../src/daemon/api.ts';
import {TOKEN_SECRET, createDaemonBackend} from '../src/backend/service.ts';
import {fakeDaemon, fakeHome, type FakeDaemon, type FakeHome} from './fake-daemon.ts';

export const TOKEN = 'tok-secret-0123456789';
const call = {signal: new AbortController().signal};

export async function until(check: () => boolean, what = 'condition', ms = 4000) {
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

export interface Rig {
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

export async function rig(opts: {paired?: boolean; published?: boolean; secretsSet?: boolean; lastSeq?: number; pollMs?: number} = {}): Promise<Rig> {
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


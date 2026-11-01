// 화면 시험용 가짜 DaemonClient. 실제 데몬·ORBIT_HOME·키체인을 쓰지 않는다.
import type {DaemonApi, DaemonEvent, DaemonStatus, ProfilesView, RunSummary, UsageReport} from '../src/daemon/api.ts';
import {createDaemonClient, type DaemonClient} from '../src/daemon/client.ts';

export type Handler = (method: string, params: unknown) => unknown;

/** createDaemonClient가 쓰는 backend 자리에 꽂는 가짜. 호출은 calls에 쌓이고, 응답은 handler가 정한다. */
export function fakeBackend(handler: Handler) {
  const calls: {method: string; params: unknown}[] = [];
  const listeners = new Map<string, Set<(payload: unknown) => void>>();
  const backend = {
    async call<T>(method: string, params?: unknown): Promise<T> {
      calls.push({method, params});
      return handler(method, params) as T;
    },
    on(event: string, h: (payload: unknown) => void) {
      if (!listeners.has(event)) listeners.set(event, new Set());
      listeners.get(event)!.add(h);
      return () => void listeners.get(event)?.delete(h);
    },
  };
  return {
    backend,
    calls,
    emit: (event: string, payload: unknown) => listeners.get(event)?.forEach(h => h(payload)),
    emitEvent: (e: DaemonEvent) => listeners.get('daemon.event')?.forEach(h => h(e)),
    emitStatus: (s: DaemonStatus) => listeners.get('daemon.status')?.forEach(h => h(s)),
  };
}

export const upStatus: DaemonStatus = {daemon: 'up', streaming: true, paired: true, lastSeq: 0};

export function fakeClient(handler: Handler): {client: DaemonClient} & ReturnType<typeof fakeBackend> {
  const fb = fakeBackend(handler);
  return {...fb, client: createDaemonClient(fb.backend)};
}

export const sampleRun = (over: Partial<RunSummary> = {}): RunSummary => ({
  runId: 'r1', request: 'make it', cwd: '/w', profile: 'p', mode: 'ultrawork', state: 'running', reason: '', exitCode: 0, workId: 'w1', message: '', startedAt: '2026-11-01T00:00:00Z', finishedAt: '',
  totals: {costUsd: 0, subscription: false, turns: 0, minutes: 0, inputTokens: 0, outputTokens: 0, monthSpentUsd: 0, monthLimitUsd: 0}, openDecisions: [], source: 'studio', device: '', lastSeq: 0, sessionId: '', ...over,
});

export const sampleProfiles = (): ProfilesView => ({
  file: {
    version: 1,
    default: 'work',
    profiles: [
      {
        name: 'work', folders: ['/w'], apiKeys: ['openrouter'], allowedModels: ['claude:*', 'pi:openrouter/*'],
        roles: {default: 'claude:haiku', planner: ['pi:openrouter/free', 'claude:haiku']}, cheap: 'claude:haiku',
        limits: {turns: 60, costUsd: 5, minutes: 60}, keywords: {ultrawork: ['ulw'], plan: ['ulw plan']}, monthlyLimitUsd: 20,
      },
      {name: 'side', folders: [], apiKeys: [], allowedModels: ['claude:*'], roles: {default: 'claude:haiku'}, limits: {turns: 10, costUsd: 1, minutes: 10}, keywords: {ultrawork: [], plan: []}},
    ],
  },
  revision: 'rev1', path: '/home/x/profiles.json', keys: [{profile: 'work', keys: [{provider: 'openrouter', set: false}]}], keyStore: 'keyring',
});

export const sampleUsage = (): UsageReport => ({
  month: '2026-11',
  profiles: [{profile: 'work', spentUsd: 1.2, limitUsd: 20, reached: false, apiSpentUsd: 0.2, apiLimitUsd: 0, apiReached: false, models: [{engine: 'claude', model: 'haiku', billing: 'subscription', costUsd: 1, inputTokens: 1000, outputTokens: 500, runs: 3}]}],
});

export type { DaemonApi };

// host.proto 메시지 ↔ 창이 보는 JSON 모양(src/daemon/api.ts). uint64·int64는 Number로 옮긴다(2^53보다 훨씬 작다).
import {BackendError} from '@onidot-labs/orbit-studio-plugin/backend';
import type {
  Event, GetProfilesResponse, GetUsageResponse, RunSummary as PbRunSummary,
} from '../daemon/gen/host_pb.ts';
import type {
  DaemonEvent, ProfileFileJson, ProfilesView, RunMode, RunSummary, UsageReport,
} from '../daemon/api.ts';

const num = (v: bigint | number): number => Number(v);

const MODES = new Set<string>(['default', 'ultrawork', 'plan']);

export function toRunSummary(r: PbRunSummary): RunSummary {
  const t = r.totals;
  return {
    runId: r.runId, request: r.request, cwd: r.cwd, profile: r.profile,
    mode: (MODES.has(r.mode) ? r.mode : 'default') as RunMode,
    state: r.state, reason: r.reason, exitCode: r.exitCode, workId: r.workId, message: r.message,
    startedAt: r.startedAt, finishedAt: r.finishedAt,
    totals: {
      costUsd: t?.costUsd ?? 0, subscription: t?.subscription ?? false, turns: t?.turns ?? 0, minutes: t?.minutes ?? 0,
      inputTokens: num(t?.inputTokens ?? 0n), outputTokens: num(t?.outputTokens ?? 0n),
      monthSpentUsd: t?.monthSpentUsd ?? 0, monthLimitUsd: t?.monthLimitUsd ?? 0,
    },
    openDecisions: r.openDecisions.map(d => ({id: d.id, title: d.title, options: [...d.options], askedAt: d.askedAt})),
    source: r.source, device: r.device, lastSeq: num(r.lastSeq), sessionId: r.sessionId,
  };
}

/** `json`이 읽히지 않으면 type·at만 있는 사건으로 둔다(사건은 건너뛰지 않고 seq를 지킨다) */
export function toDaemonEvent(e: Event): DaemonEvent {
  let event: DaemonEvent['event'] = {type: e.type, at: e.at};
  try {
    const parsed: unknown = JSON.parse(e.json);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      event = {...(parsed as Record<string, unknown>), type: typeof (parsed as {type?: unknown}).type === 'string' ? (parsed as {type: string}).type : e.type, at: typeof (parsed as {at?: unknown}).at === 'string' ? (parsed as {at: string}).at : e.at};
    }
  } catch {
    // 아래 기본값
  }
  return {seq: num(e.seq), runId: e.runId, type: e.type, at: e.at, event};
}

export function toUsage(r: GetUsageResponse): UsageReport {
  return {
    month: r.month,
    profiles: r.profiles.map(p => ({
      profile: p.profile, spentUsd: p.spentUsd, limitUsd: p.limitUsd, reached: p.reached,
      apiSpentUsd: p.apiSpentUsd, apiLimitUsd: p.apiLimitUsd, apiReached: p.apiReached,
      models: p.models.map(m => ({
        engine: m.engine, model: m.model, billing: m.billing, costUsd: m.costUsd,
        inputTokens: num(m.inputTokens), outputTokens: num(m.outputTokens), runs: m.runs,
      })),
    })),
  };
}

export function toProfilesView(r: GetProfilesResponse): ProfilesView {
  let file: ProfileFileJson;
  try {
    file = JSON.parse(r.json) as ProfileFileJson;
    if (!file || typeof file !== 'object' || Array.isArray(file)) throw new Error('not a JSON object');
  } catch (e) {
    throw new BackendError('PROFILES_UNREADABLE', `The profile file ${r.path || '(unknown path)'} could not be read: ${e instanceof Error ? e.message : String(e)}`);
  }
  return {
    file,
    revision: r.revision,
    path: r.path,
    keys: r.keys.map(k => ({profile: k.profile, keys: k.keys.map(s => ({provider: s.provider, set: s.set}))})),
    keyStore: r.keyStore,
  };
}

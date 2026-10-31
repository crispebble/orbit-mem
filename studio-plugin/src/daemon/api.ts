// The window ↔ backend contract of the orbit-pi daemon client (Studio 4차 S4-5·S4-8, ONID-86·85).
// The backend (src/backend/) holds the device token and speaks Connect RPC to the daemon; the window only sees these
// plain JSON shapes through `ctx.backend.call(method, params)` and the `daemon.*` events of `ctx.backend.on`.
// Contract of the daemon itself: orbit-pi packages/orbit-host/proto/orbit/host/v1/host.proto (vendored in proto/).
// Numbers that are uint64/int64 in the proto (seq, tokens) are plain numbers here; they stay far below 2^53.

/** Backend method names. */
export const DAEMON_METHODS = {
  status: 'daemon.status',
  ensure: 'daemon.ensure',
  startRun: 'daemon.startRun',
  stopRun: 'daemon.stopRun',
  answer: 'daemon.answer',
  listRuns: 'daemon.listRuns',
  getRun: 'daemon.getRun',
  history: 'daemon.history',
  getUsage: 'daemon.getUsage',
  getProfiles: 'daemon.getProfiles',
  saveProfiles: 'daemon.saveProfiles',
  setApiKey: 'daemon.setApiKey',
  deleteApiKey: 'daemon.deleteApiKey',
  messages: 'daemon.messages',
} as const;

/** Backend events (`ctx.backend.on(name, handler)`). */
export const DAEMON_EVENTS = {
  /** payload: DaemonEvent. Every run, in seq order, without gaps or repeats across reconnects. */
  event: 'daemon.event',
  /** payload: DaemonStatus. Sent when it changes (stream up/down, daemon started, pairing). */
  status: 'daemon.status',
} as const;

/** Error codes a backend method fails with (`BackendError.code`, seen as `error.code` in the window). */
export type DaemonErrorCode =
  /** No `orb` on PATH (or ORBIT_ORB). */
  | 'ORB_MISSING'
  /** The daemon is not running and the call does not start it. */
  | 'DAEMON_DOWN'
  /** `orb daemon start` failed; message has its output tail. */
  | 'DAEMON_START_FAILED'
  /** Pairing (`orb daemon device add`) failed, or the token was refused twice. */
  | 'PAIR_FAILED'
  /** This Orbit Studio cannot keep the token (plugin API before 0.1.27: no backend secrets.set). */
  | 'STUDIO_TOO_OLD'
  /** A Connect error from the daemon: code is `RPC_<connect code name>`, e.g. RPC_INVALID_ARGUMENT, RPC_ABORTED. */
  | `RPC_${string}`;

export interface DaemonStatus {
  /** down: no daemon; starting: `orb daemon start` in progress; up: Health answered with our token. */
  daemon: 'down' | 'starting' | 'up';
  /** The global event stream is connected. */
  streaming: boolean;
  /** A device token is kept (plugin secret `daemon-token`). */
  paired: boolean;
  url?: string;
  version?: string;
  /** Highest seq the window has been sent. */
  lastSeq: number;
  /** Last failure, for the window to show (never a token). */
  error?: {code: string; message: string};
}

export type RunMode = 'default' | 'ultrawork' | 'plan';

export interface StartRunInput {
  request: string;
  /** Absolute work folder. */
  cwd: string;
  /** Empty: the profile whose folder matches cwd. */
  profile?: string;
  mode: RunMode;
  /** Continue an earlier work (`--resume`). Empty workId: the latest open orbit-pi work of cwd. */
  resume?: {workId?: string};
  limits?: {maxTurns?: number; maxCostUsd?: number; maxMinutes?: number};
}

export interface OpenDecision {
  id: string;
  title: string;
  options: string[];
  askedAt: string;
}

export interface RunTotals {
  costUsd: number;
  subscription: boolean;
  turns: number;
  minutes: number;
  inputTokens: number;
  outputTokens: number;
  monthSpentUsd: number;
  monthLimitUsd: number;
}

/** host.proto RunSummary. */
export interface RunSummary {
  runId: string;
  request: string;
  cwd: string;
  profile: string;
  mode: RunMode;
  /** running, completed, failed, stopped, planned, interrupted. */
  state: string;
  reason: string;
  exitCode: number;
  workId: string;
  message: string;
  startedAt: string;
  finishedAt: string;
  totals: RunTotals;
  openDecisions: OpenDecision[];
  source: string;
  device: string;
  lastSeq: number;
  sessionId: string;
}

/** host.proto Event with `json` parsed: `event` is an orbit-orchestra OrchestraEvent (unknown types are kept). */
export interface DaemonEvent {
  seq: number;
  runId: string;
  type: string;
  at: string;
  event: {type: string; at: string; [field: string]: unknown};
}

export interface ModelUsage {
  engine: string;
  model: string;
  billing: 'subscription' | 'api' | string;
  costUsd: number;
  inputTokens: number;
  outputTokens: number;
  runs: number;
}

export interface ProfileUsage {
  profile: string;
  spentUsd: number;
  limitUsd: number;
  reached: boolean;
  apiSpentUsd: number;
  apiLimitUsd: number;
  apiReached: boolean;
  models: ModelUsage[];
}

export interface UsageReport {
  month: string;
  profiles: ProfileUsage[];
}

export interface ProfilesView {
  /** Parsed profile file (orbit-profiles ProfileFile version 1). */
  file: ProfileFileJson;
  revision: string;
  path: string;
  keys: {profile: string; keys: {provider: string; set: boolean}[]}[];
  keyStore: 'keyring' | 'file' | string;
}

/** orbit-profiles ProfileFile (types.ts there is the source; this mirrors it for the window). */
export interface ProfileFileJson {
  version: 1;
  default?: string;
  profiles: ProfileJson[];
}

export interface ProfileJson {
  name: string;
  folders: string[];
  claudeConfigDir?: string;
  codexHome?: string;
  apiKeys: string[];
  allowedModels: string[];
  roles: Record<string, string | string[]>;
  cheap?: string | string[];
  monthlyLimitUsd?: number;
  monthlyApiLimitUsd?: number;
  runRules?: Partial<Record<'terminal' | 'studio' | 'workflow' | 'schedule', 'subscription' | 'api' | 'subscription-first' | 'api-first'>>;
  folderRules?: {models: string[]; allow?: string[]; deny?: string[]}[];
  limits: {turns: number; costUsd: number; minutes: number};
  keywords: {ultrawork: string[]; plan: string[]};
  [field: string]: unknown;
}

/** `pi:<provider>/<model>` needs that provider's key; claude and codex models need none (orbit-profiles requiredKey). */
export function requiredKey(ref: string): string | undefined {
  if (!ref.startsWith('pi:')) return undefined;
  const model = ref.slice(3);
  const slash = model.indexOf('/');
  return slash > 0 ? model.slice(0, slash) : undefined;
}

/** Params and results of each backend method. */
export interface DaemonApi {
  /** Never starts the daemon. */
  status(): Promise<DaemonStatus>;
  /** Starts the daemon when it is down (`orb daemon start`) and pairs when there is no token. */
  ensure(): Promise<DaemonStatus>;
  startRun(input: StartRunInput): Promise<{runId: string; watchAfterSeq: number}>;
  stopRun(params: {runId: string}): Promise<{stopping: boolean}>;
  answer(params: {runId: string; decisionId: string; answer: string}): Promise<{accepted: boolean}>;
  listRuns(params: {limit?: number; activeOnly?: boolean}): Promise<{runs: RunSummary[]}>;
  getRun(params: {runId: string}): Promise<{run: RunSummary}>;
  /** Stored events of one run with seq > afterSeq, up to the run's last seq when called (live ones arrive as events). */
  history(params: {runId: string; afterSeq?: number}): Promise<{events: DaemonEvent[]}>;
  getUsage(params: {profile?: string; month?: string}): Promise<UsageReport>;
  getProfiles(): Promise<ProfilesView>;
  /** Fails with RPC_INVALID_ARGUMENT (message names the field) or RPC_ABORTED (someone else saved first). */
  saveProfiles(params: {file: ProfileFileJson; expectedRevision: string}): Promise<{revision: string}>;
  /** The value goes to the daemon once and is never sent back. */
  setApiKey(params: {profile: string; provider: string; value: string}): Promise<{revision: string}>;
  deleteApiKey(params: {profile: string; provider: string}): Promise<{deleted: boolean; revision: string}>;
  /** Templates for event `key`s and `option.<id>` labels (GetMessages). */
  messages(params: {lang: 'en' | 'ko'}): Promise<{lang: string; messages: Record<string, string>}>;
}

interface BackendLike {
  call<T = unknown>(method: string, params?: unknown, options?: {signal?: AbortSignal; timeoutMs?: number}): Promise<T>;
}

/** The window's typed client over `ctx.backend`. */
export function daemonApi(backend: BackendLike): DaemonApi {
  const call = <T>(method: string, params?: unknown, timeoutMs?: number) =>
    backend.call<T>(method, params, timeoutMs ? {timeoutMs} : undefined);
  return {
    status: () => call(DAEMON_METHODS.status),
    // Starting the daemon and pairing can take a while on a cold machine.
    ensure: () => call(DAEMON_METHODS.ensure, undefined, 60_000),
    startRun: input => call(DAEMON_METHODS.startRun, input),
    stopRun: p => call(DAEMON_METHODS.stopRun, p),
    answer: p => call(DAEMON_METHODS.answer, p),
    listRuns: p => call(DAEMON_METHODS.listRuns, p),
    getRun: p => call(DAEMON_METHODS.getRun, p),
    history: p => call(DAEMON_METHODS.history, p, 60_000),
    getUsage: p => call(DAEMON_METHODS.getUsage, p),
    getProfiles: () => call(DAEMON_METHODS.getProfiles),
    saveProfiles: p => call(DAEMON_METHODS.saveProfiles, p),
    setApiKey: p => call(DAEMON_METHODS.setApiKey, p),
    deleteApiKey: p => call(DAEMON_METHODS.deleteApiKey, p),
    messages: p => call(DAEMON_METHODS.messages, p),
  };
}

/** Fills `%s`/`%d` in order and `%%` (orbit-orchestra i18n format). */
export function formatMessage(template: string, args: readonly unknown[] = []): string {
  let i = 0;
  return template.replace(/%([sd%])/g, (_m, c: string) => (c === '%' ? '%' : String(args[i++] ?? '')));
}

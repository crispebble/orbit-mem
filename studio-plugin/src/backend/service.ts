// orbit-pi 데몬 클라이언트의 뒤쪽 본체(Studio 4차 S4-5·S4-8). 창은 `ctx.backend.call`과 `daemon.*` 사건으로만 이것을 본다.
// - 데몬 찾기: `$ORBIT_HOME/runtime/daemon/daemon.json`의 url + Health(기기 토큰).
// - 토큰: 플러그인 비밀값 `daemon-token`. 없거나 거절되면 `orb daemon device add`로 새로 받아 둔다. 토큰은 로그·사건·오류·결과에 싣지 않는다.
// - 전역 사건 스트림: 데몬이 올라와 있고 짝지어졌으면 WatchEvents(run_id 비움)를 열어 사건마다 `daemon.event`로 보낸다.
//   끊기면 지수 백오프로 마지막 seq부터 다시 열어 빠짐도 겹침도 없다. 데몬이 내려가 있으면 주기적으로 확인만 하고 띄우지는 않는다.
import {create} from '@bufbuild/protobuf';
import {Code, ConnectError, createClient, type Client} from '@connectrpc/connect';
import {createConnectTransport} from '@connectrpc/connect-node';
import {BackendError, type BackendCall} from '@onidot-labs/orbit-studio-plugin/backend';
import {DAEMON_EVENTS, DAEMON_METHODS, type DaemonEvent, type DaemonStatus, type StartRunInput} from '../daemon/api.ts';
import {
  HostService, StartRunRequestSchema, type HealthResponse,
} from '../daemon/gen/host_pb.ts';
import {toDaemonEvent, toProfilesView, toRunSummary, toUsage} from './convert.ts';
import {orbitHome, readDaemonInfo, resolveOrb, runOrb, tail, type DaemonInfo, type Env} from './orb.ts';

export const TOKEN_SECRET = 'daemon-token';
export const DEVICE_NAME = 'orbit-studio';
export const DEVICE_SCOPES = 'read,answer,run,admin';

type HostClient = Client<typeof HostService>;
type BackendMethod = (params: any, call: BackendCall) => unknown;

export interface ServiceDeps {
  secrets: {get(name: string): Promise<string | null>; set?(name: string, value: string): Promise<void>; delete?(name: string): Promise<void>};
  emit(event: string, payload?: unknown): void;
  log: {info(message: string): void; warn(message: string): void; error(message: string): void};
  env?: Env;
  /** 데몬이 내려가 있을 때 올라왔는지 보는 간격(기본 5초) */
  pollMs?: number;
  /** 스트림이 끊긴 뒤 다시 여는 간격: 처음 값에서 두 배씩, 최대까지(기본 0.5초~5초) */
  backoffMs?: {min: number; max: number};
  /** `orb daemon start` 시간 한도(기본 60초) */
  startTimeoutMs?: number;
}

const HEALTH_TIMEOUT_MS = 3000;
const CALL_TIMEOUT_MS = 30_000;
const ORB_TIMEOUT_MS = 20_000;

/** 토큰은 loopback 데몬에만 보낸다: daemon.json이 바깥 주소를 가리켜도 따라가지 않는다 */
export function isLoopbackUrl(url: string): boolean {
  try {
    const host = new URL(url).hostname;
    return host === '127.0.0.1' || host === 'localhost' || host === '[::1]' || host === '::1';
  } catch {
    return false;
  }
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const invalid = (message: string) => new BackendError('RPC_INVALID_ARGUMENT', message);

/** `InvalidArgument` → `INVALID_ARGUMENT` */
function codeName(code: Code): string {
  return (Code[code] ?? 'Unknown').replace(/([a-z0-9])([A-Z])/g, '$1_$2').toUpperCase();
}

export function createDaemonBackend(deps: ServiceDeps) {
  const env = deps.env ?? process.env;
  const home = orbitHome(env);
  const pollMs = deps.pollMs ?? 5000;
  const backoff = deps.backoffMs ?? {min: 500, max: 5000};
  const startTimeoutMs = deps.startTimeoutMs ?? 60_000;

  // undefined: 아직 읽지 않음, null: 없음 또는 거절됨
  let token: string | null | undefined;
  let state: Omit<DaemonStatus, 'lastSeq'> = {daemon: 'down', streaming: false, paired: false};
  let lastSeq: number | undefined;
  let starting = false;
  let stopped = false;
  let streamAbort: AbortController | undefined;
  let wake: (() => void) | undefined;
  /** 기다리는 중이 아닐 때 온 깨우기: 다음 기다림을 건너뛴다 */
  let kicked = false;
  let queue: Promise<unknown> = Promise.resolve();
  const clients = new Map<string, HostClient>();

  // ---- 상태 ----
  const snapshot = (): DaemonStatus => ({...state, lastSeq: lastSeq ?? 0});
  function setState(patch: Partial<Omit<DaemonStatus, 'lastSeq'>>) {
    const next = {...state, ...patch};
    if (JSON.stringify(next) === JSON.stringify(state)) return;
    state = next;
    deps.emit(DAEMON_EVENTS.status, snapshot());
  }
  /** 토큰·비밀값이 들어간 글을 밖으로 내지 않는다 */
  const redact = (text: string, extra: string[] = []) => {
    let out = text;
    for (const s of [token, ...extra]) if (s && s.length >= 6) out = out.split(s).join('***');
    return out;
  };
  const failure = (e: unknown, extra: string[] = []) => ({
    code: e instanceof BackendError ? e.code : 'ERROR',
    message: redact(e instanceof Error ? e.message : String(e), extra),
  });

  async function loadToken(): Promise<string | null> {
    if (token === undefined) {
      try {
        token = (await deps.secrets.get(TOKEN_SECRET)) || null;
      } catch {
        token = null;
      }
    }
    return token;
  }

  function clientFor(url: string, tok: string): HostClient {
    const key = `${url}\n${tok}`;
    let client = clients.get(key);
    if (!client) {
      clients.clear();
      client = createClient(HostService, createConnectTransport({
        baseUrl: url,
        httpVersion: '1.1',
        interceptors: [next => req => {
          if (tok) req.header.set('authorization', `Bearer ${tok}`);
          return next(req);
        }],
      }));
      clients.set(key, client);
    }
    return client;
  }

  const isUnauth = (e: unknown) => ConnectError.from(e).code === Code.Unauthenticated;

  // ---- 찾기 ----
  type Probe = {kind: 'down'} | {kind: 'unauth'; info: DaemonInfo} | {kind: 'ok'; info: DaemonInfo; health: HealthResponse};

  /** daemon.json을 읽고 Health로 확인한다. 데몬을 띄우지 않는다. */
  async function probe(): Promise<Probe> {
    const info = readDaemonInfo(home);
    if (!info) return {kind: 'down'};
    if (!isLoopbackUrl(info.url)) {
      setState({error: {code: 'DAEMON_URL_REFUSED', message: 'daemon.json does not point to a loopback address; the token is not sent'}});
      return {kind: 'down'};
    }
    const tok = await loadToken();
    try {
      const health = await clientFor(info.url, tok ?? '').health({}, {timeoutMs: HEALTH_TIMEOUT_MS});
      return tok ? {kind: 'ok', info, health} : {kind: 'unauth', info};
    } catch (e) {
      if (!isUnauth(e)) return {kind: 'down'};
      // 다른 곳에서 새 토큰이 이미 놓였으면 그것은 지우지 않는다
      if (token === tok) token = null;
      return {kind: 'unauth', info};
    }
  }

  function applyProbe(p: Probe) {
    if (p.kind === 'down') setState({daemon: starting ? 'starting' : 'down', streaming: false, paired: !!token, url: undefined, version: undefined});
    else if (p.kind === 'unauth') setState({daemon: 'up', streaming: false, paired: false, url: p.info.url, version: p.info.version});
    else setState({daemon: 'up', paired: true, url: p.info.url, version: p.health.version || p.info.version});
  }

  async function refresh(): Promise<Probe> {
    const p = await probe();
    applyProbe(p);
    return p;
  }

  // ---- 짝짓기·띄우기 ----
  const needOrb = () => {
    const orb = resolveOrb(env);
    if (!orb) throw new BackendError('ORB_MISSING', 'orb was not found (set ORBIT_ORB or put orb on PATH)');
    return orb;
  };

  /** 기기 토큰을 새로 받는다: 같은 이름의 옛 기기를 거두고 새로 더한다. 토큰은 stdout 마지막 줄이다. */
  async function pairOnce(orb: string): Promise<string> {
    try {
      await runOrb(orb, ['daemon', 'device', 'revoke', DEVICE_NAME], env, ORB_TIMEOUT_MS);
    } catch {
      // 없으면 무시한다
    }
    const r = await runOrb(orb, ['daemon', 'device', 'add', DEVICE_NAME, '--scope', DEVICE_SCOPES], env, ORB_TIMEOUT_MS);
    // 실패 글에는 stderr만 쓴다: stdout은 토큰이다
    if (r.code !== 0) throw new Error(tail(r.stderr) || `orb daemon device add exited with ${r.code}`);
    const issued = r.stdout.trim().split(/\r?\n/).filter(Boolean).pop()?.trim();
    if (!issued) throw new Error('orb daemon device add printed no token');
    return issued;
  }

  async function pair(): Promise<void> {
    if (typeof deps.secrets.set !== 'function') {
      throw new BackendError('STUDIO_TOO_OLD', 'This Orbit Studio cannot keep the daemon token (needs plugin API 0.1.27).');
    }
    const orb = needOrb();
    let last = 'unknown';
    for (let round = 0; round < 2; round++) {
      try {
        const issued = await pairOnce(orb);
        await deps.secrets.set(TOKEN_SECRET, issued);
        token = issued;
        const p = await refresh();
        if (p.kind === 'ok') {
          restartStream();
          return;
        }
        last = p.kind === 'unauth' ? 'the daemon refused the new device token' : 'the daemon stopped answering while pairing';
        token = null;
      } catch (e) {
        last = failure(e).message;
        token = null;
      }
    }
    throw new BackendError('PAIR_FAILED', last);
  }

  /** 옛 토큰으로 열린 스트림을 끊고, 감시 루프가 새 토큰으로 곧바로 다시 열게 한다 */
  function restartStream() {
    const old = streamAbort;
    streamAbort = undefined;
    old?.abort();
    kicked = true;
    wake?.();
  }

  /** 한 번에 하나씩: 창이 둘이어도 띄우기·짝짓기는 겹치지 않는다 */
  function serialize<T>(job: () => Promise<T>): Promise<T> {
    const run = queue.then(job, job);
    queue = run.catch(() => {});
    return run;
  }

  function ensure(): Promise<DaemonStatus> {
    return serialize(async () => {
      try {
        let p = await refresh();
        if (p.kind === 'down') {
          const orb = needOrb();
          starting = true;
          setState({daemon: 'starting'});
          let r;
          try {
            r = await runOrb(orb, ['daemon', 'start'], env, startTimeoutMs);
          } catch (e) {
            throw new BackendError('DAEMON_START_FAILED', failure(e).message);
          } finally {
            starting = false;
          }
          if (r.code !== 0) throw new BackendError('DAEMON_START_FAILED', tail(r.stderr) || tail(r.stdout) || `orb daemon start exited with ${r.code}`);
          p = await refresh();
          if (p.kind === 'down') throw new BackendError('DAEMON_START_FAILED', tail(r.stdout) || 'the daemon did not answer after start');
        }
        if (p.kind === 'unauth' || !token) await pair();
        setState({error: undefined});
        kicked = true;
        wake?.();
        return snapshot();
      } catch (e) {
        starting = false;
        applyProbe(await probe().catch((): Probe => ({kind: 'down'})));
        setState({error: failure(e)});
        throw e;
      }
    });
  }

  // ---- 전역 스트림 ----
  /** 스트림 하나를 끝날 때까지 읽는다. 사건을 하나라도 받았으면 true */
  async function stream(p: Extract<Probe, {kind: 'ok'}>): Promise<boolean> {
    const current = Number(p.health.lastSeq);
    // 처음 붙을 때는 과거를 다시 보내지 않는다(과거는 history가 준다). 사건 기록이 줄었으면 그 끝으로 맞춘다.
    if (lastSeq === undefined || current < lastSeq) lastSeq = current;
    const ac = new AbortController();
    streamAbort = ac;
    // 이 스트림이 쓴 토큰: 끝났을 때 거절이면 그 토큰일 때만 지운다(그 사이 새로 짝지어졌을 수 있다)
    const used = token ?? '';
    let got = false;
    setState({streaming: true});
    try {
      for await (const res of clientFor(p.info.url, used).watchEvents({runId: '', afterSeq: BigInt(lastSeq)}, {signal: ac.signal})) {
        const ev = res.event;
        if (!ev) continue;
        const seq = Number(ev.seq);
        if (seq <= lastSeq) continue;
        lastSeq = seq;
        got = true;
        deps.emit(DAEMON_EVENTS.event, toDaemonEvent(ev));
      }
    } catch (e) {
      if (!ac.signal.aborted) {
        const message = failure(e).message;
        if (isUnauth(e) && token === used) token = null;
        deps.log.warn(`daemon event stream ended: ${message}`);
      }
    } finally {
      // 다시 짝지어 새 스트림이 이미 열렸으면 건드리지 않는다
      if (streamAbort === ac) {
        streamAbort = undefined;
        setState({streaming: false});
      }
    }
    return got;
  }

  const wait = (ms: number) => new Promise<void>(resolve => {
    if (kicked) {
      kicked = false;
      return resolve();
    }
    const timer = setTimeout(done, ms);
    function done() {
      kicked = false;
      clearTimeout(timer);
      if (wake === done) wake = undefined;
      resolve();
    }
    wake = done;
  });

  async function supervise(): Promise<void> {
    let delay = backoff.min;
    while (!stopped) {
      let attached = false;
      try {
        const p = await refresh();
        if (p.kind === 'ok' && !stopped) {
          attached = true;
          delay = (await stream(p)) ? backoff.min : Math.min(delay * 2, backoff.max);
        }
      } catch (e) {
        deps.log.warn(`daemon watch failed: ${failure(e).message}`);
      }
      if (stopped) break;
      await wait(attached ? delay : pollMs);
    }
  }

  // ---- RPC ----
  async function rpcClient(): Promise<HostClient> {
    const info = readDaemonInfo(home);
    if (!info) throw new BackendError('DAEMON_DOWN', 'The orbit-pi daemon is not running.');
    if (!isLoopbackUrl(info.url)) throw new BackendError('DAEMON_DOWN', 'daemon.json does not point to a loopback address; the token is not sent.');
    const tok = await loadToken();
    if (!tok) throw new BackendError('RPC_UNAUTHENTICATED', 'Not paired with the daemon yet; call ensure first.');
    return clientFor(info.url, tok);
  }

  function mapError(e: unknown, extra: string[], used: string | null | undefined = token): BackendError {
    if (e instanceof BackendError) return e;
    const c = ConnectError.from(e);
    // 오류 글은 토큰을 지우기 전에 만든다(가리기에 현재 토큰이 필요하다)
    const message = redact(c.rawMessage, extra);
    if (c.code === Code.Unauthenticated) {
      // 부른 사이 새로 짝지어졌으면 새 토큰은 지우지 않는다
      if (token === used) {
        token = null;
        setState({paired: false});
      }
    }
    // 데몬에 닿지 못한 것은 DAEMON_DOWN, 데몬이 돌려준 오류는 RPC_<코드>
    if (c.code === Code.Unavailable) {
      kicked = true;
      wake?.();
      return new BackendError('DAEMON_DOWN', message);
    }
    return new BackendError(`RPC_${codeName(c.code)}`, message);
  }

  async function call<T>(fn: (client: HostClient, timeoutMs: number) => Promise<T>, extra: string[] = []): Promise<T> {
    const client = await rpcClient();
    const used = token;
    try {
      return await fn(client, CALL_TIMEOUT_MS);
    } catch (e) {
      throw mapError(e, extra, used);
    }
  }

  const params = (v: unknown): Record<string, unknown> => {
    if (!isObj(v)) throw invalid('params must be an object');
    return v;
  };
  const text = (o: Record<string, unknown>, key: string, required = true): string => {
    const v = o[key];
    if (typeof v === 'string' && (v || !required)) return v;
    if (v === undefined && !required) return '';
    throw invalid(`${key} must be ${required ? 'a non-empty ' : 'a '}string`);
  };
  const positive = (v: unknown) => (typeof v === 'number' && v > 0 ? v : 0);

  /** 한 실행의 사건을 `GetRun.last_seq`까지 모아 돌려주고 스트림을 끊는다. 그 뒤는 전역 스트림이 준다. */
  async function history(runId: string, afterSeq: number, signal: AbortSignal): Promise<{events: DaemonEvent[]}> {
    const client = await rpcClient();
    const used = token;
    const events: DaemonEvent[] = [];
    const ac = new AbortController();
    const onAbort = () => ac.abort();
    signal.addEventListener('abort', onAbort);
    try {
      const upTo = Number((await client.getRun({runId}, {timeoutMs: CALL_TIMEOUT_MS})).run?.lastSeq ?? 0n);
      if (upTo <= afterSeq) return {events};
      for await (const res of client.watchEvents({runId, afterSeq: BigInt(afterSeq)}, {signal: ac.signal})) {
        const ev = res.event;
        if (!ev) continue;
        const seq = Number(ev.seq);
        if (seq <= afterSeq) continue;
        events.push(toDaemonEvent(ev));
        if (seq >= upTo) break;
      }
      return {events};
    } catch (e) {
      throw mapError(signal.aborted ? new ConnectError('canceled', Code.Canceled) : e, [], used);
    } finally {
      signal.removeEventListener('abort', onAbort);
      ac.abort();
    }
  }

  const methods: Record<string, BackendMethod> = {
    [DAEMON_METHODS.status]: async () => {
      await refresh();
      return snapshot();
    },
    [DAEMON_METHODS.ensure]: () => ensure(),
    [DAEMON_METHODS.startRun]: async (raw: unknown) => {
      const p = params(raw) as Partial<StartRunInput> & Record<string, unknown>;
      const request = text(p, 'request', false);
      const cwd = text(p, 'cwd');
      const mode = p.mode ?? 'ultrawork';
      if (mode !== 'ultrawork' && mode !== 'plan' && mode !== 'plan-approve') throw invalid('mode must be ultrawork, plan or plan-approve');
      const limits = isObj(p.limits) ? p.limits : {};
      const [turns, cost, minutes] = [positive(limits.maxTurns), positive(limits.maxCostUsd), positive(limits.maxMinutes)];
      const req = create(StartRunRequestSchema, {
        request, cwd, profile: typeof p.profile === 'string' ? p.profile : '',
        ultrawork: mode === 'ultrawork', plan: mode === 'plan', planApprove: mode === 'plan-approve',
        ...(isObj(p.resume) ? {resume: {workId: typeof p.resume.workId === 'string' ? p.resume.workId : ''}} : {}),
        ...(turns || cost || minutes ? {limits: {maxTurns: turns, maxCostUsd: cost, maxMinutes: minutes}} : {}),
        source: 'studio',
      });
      const res = await call((c, timeoutMs) => c.startRun(req, {timeoutMs}));
      return {runId: res.runId, watchAfterSeq: Number(res.watchAfterSeq)};
    },
    [DAEMON_METHODS.stopRun]: async (raw: unknown) => {
      const runId = text(params(raw), 'runId');
      return {stopping: (await call((c, timeoutMs) => c.stopRun({runId}, {timeoutMs}))).stopping};
    },
    [DAEMON_METHODS.answer]: async (raw: unknown) => {
      const p = params(raw);
      const [runId, decisionId, answer] = [text(p, 'runId'), text(p, 'decisionId'), text(p, 'answer', false)];
      return {accepted: (await call((c, timeoutMs) => c.answerDecision({runId, decisionId, answer}, {timeoutMs}))).accepted};
    },
    [DAEMON_METHODS.listRuns]: async (raw: unknown) => {
      const p = isObj(raw) ? raw : {};
      const res = await call((c, timeoutMs) => c.listRuns({limit: typeof p.limit === 'number' ? p.limit : 0, activeOnly: p.activeOnly === true}, {timeoutMs}));
      return {runs: res.runs.map(toRunSummary)};
    },
    [DAEMON_METHODS.getRun]: async (raw: unknown) => {
      const runId = text(params(raw), 'runId');
      const res = await call((c, timeoutMs) => c.getRun({runId}, {timeoutMs}));
      if (!res.run) throw new BackendError('RPC_NOT_FOUND', `run ${runId} not found`);
      return {run: toRunSummary(res.run)};
    },
    [DAEMON_METHODS.history]: (raw: unknown, c: BackendCall) => {
      const p = params(raw);
      const after = typeof p.afterSeq === 'number' && p.afterSeq > 0 ? p.afterSeq : 0;
      return history(text(p, 'runId'), after, c.signal);
    },
    [DAEMON_METHODS.getUsage]: async (raw: unknown) => {
      const p = isObj(raw) ? raw : {};
      return toUsage(await call((c, timeoutMs) => c.getUsage({profile: typeof p.profile === 'string' ? p.profile : '', month: typeof p.month === 'string' ? p.month : ''}, {timeoutMs})));
    },
    [DAEMON_METHODS.getProfiles]: async () => toProfilesView(await call((c, timeoutMs) => c.getProfiles({}, {timeoutMs}))),
    [DAEMON_METHODS.saveProfiles]: async (raw: unknown) => {
      const p = params(raw);
      if (!isObj(p.file)) throw invalid('file must be an object');
      const json = JSON.stringify(p.file);
      const expectedRevision = typeof p.expectedRevision === 'string' ? p.expectedRevision : '';
      return {revision: (await call((c, timeoutMs) => c.saveProfiles({json, expectedRevision}, {timeoutMs}))).revision};
    },
    [DAEMON_METHODS.setApiKey]: async (raw: unknown) => {
      const p = params(raw);
      const [profile, provider, value] = [text(p, 'profile'), text(p, 'provider'), text(p, 'value')];
      // 값은 데몬에만 보낸다. 오류 글에 되비쳐 와도 가린다.
      return {revision: (await call((c, timeoutMs) => c.setApiKey({profile, provider, value}, {timeoutMs}), [value])).revision};
    },
    [DAEMON_METHODS.deleteApiKey]: async (raw: unknown) => {
      const p = params(raw);
      const [profile, provider] = [text(p, 'profile'), text(p, 'provider')];
      const res = await call((c, timeoutMs) => c.deleteApiKey({profile, provider}, {timeoutMs}));
      return {deleted: res.deleted, revision: res.revision};
    },
    [DAEMON_METHODS.messages]: async (raw: unknown) => {
      const lang = isObj(raw) && raw.lang === 'ko' ? 'ko' : 'en';
      const res = await call((c, timeoutMs) => c.getMessages({lang}, {timeoutMs}));
      return {lang: res.lang || lang, messages: {...res.messages}};
    },
  };

  return {
    methods,
    /** 프로세스가 뜬 뒤 한 번: 토큰을 읽고 전역 스트림 감시를 시작한다. 데몬을 띄우지 않는다. */
    async start(): Promise<void> {
      stopped = false;
      await loadToken();
      void supervise();
    },
    async stop(): Promise<void> {
      stopped = true;
      streamAbort?.abort();
      wake?.();
    },
  };
}


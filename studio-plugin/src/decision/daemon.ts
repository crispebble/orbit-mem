// 결정 카드 출처 합성(Studio 4차 S4-5, C5): orbit-pi 데몬의 열린 질문·권한 카드 + `orb work mcp`의 카드.
// - 데몬 카드의 정본은 데몬 실행이다. 열린 카드는 ListRuns(open_decisions), 글(질문·선택지)은 decision.asked 사건의 key/args로 만든다.
//   닫힌 카드는 전역 스트림의 decision.answered·run.finished로 따라가고, 재시작 때는 오늘 실행들의 history로 복원한다(최선).
// - 카드 id는 `daemon:<runId>:<decisionId>`. 답하기는 AnswerDecision이고 accepted=false면 CONFLICT.
// - 같은 질문이 orb 쪽에도 열려 있으면(데몬이 orb 카드도 연다) work.host==='orbit-pi'이고 work.sessionId가 데몬이 아는
//   실행의 session_id인 orb 카드는 목록에서 뺀다: 한 번만 보인다.
// - 데몬이 내려가 있거나 짝지어지지 않았으면 orb 쪽만 쓴다. 데몬이 되면 orb 쪽 오류(판 불일치 등)는 로그만 남기고 데몬 카드를 보인다.
import {errorMessage} from '../orb.ts';
import type {DaemonEvent, RunSummary} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import type {AnswerOutcome, AnswerPart, Decision, DecisionChanges, DecisionPage, DecisionSource} from './types.ts';

export const DAEMON_CARD_PREFIX = 'daemon:';
/** 한 번의 복원에서 history를 읽는 실행 수 한도 */
const RESTORE_MAX = 20;
const RUNS_LIMIT = 50;
/** 기록(records) 수 한도: 넘으면 오래전에 닫힌 카드부터 버린다. 열린 카드는 버리지 않는다 */
export const RECORDS_MAX = 500;
/** orb 쪽이 실패해도 데몬이 되어 있을 때 다시 걸기 전에 쉬는 시간 */
const RETRY_MS = 5_000;

export const cardId = (runId: string, decisionId: string) => `${DAEMON_CARD_PREFIX}${runId}:${decisionId}`;

/** 카드 id → 실행·결정 id. 데몬 카드가 아니면 undefined */
export function parseCardId(id: string): {runId: string; decisionId: string} | undefined {
  if (!id.startsWith(DAEMON_CARD_PREFIX)) return undefined;
  const rest = id.slice(DAEMON_CARD_PREFIX.length);
  const cut = rest.lastIndexOf(':');
  return cut > 0 && cut < rest.length - 1 ? {runId: rest.slice(0, cut), decisionId: rest.slice(cut + 1)} : undefined;
}

interface Record_ {
  runId: string;
  decisionId: string;
  /** decision.asked 사건의 seq: 이 값이 ListRuns 시점보다 뒤면 아직 반영되지 않았을 뿐이다 */
  askedSeq: number;
  askedAt: string;
  title: string;
  options: string[];
  key?: unknown;
  args?: unknown;
  kind?: string;
  name?: string;
  context?: string;
  state: 'open' | 'answered' | 'withdrawn';
  answer?: {text: string; at: string; by: string};
  closedAt?: string;
  closeReason?: string;
}

export interface DaemonDecisionOptions {
  now?: () => number;
  log?: {warn(message: string): void};
}

const startOfDay = (now: number) => {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};
const ms = (s: string | undefined) => (s ? Date.parse(s) || 0 : 0);
const str = (v: unknown): string | undefined => (typeof v === 'string' && v ? v : undefined);

/** 데몬 카드와 orb 카드를 합치는 DecisionSource */
export function daemonDecisionSource(client: DaemonClient, orb: DecisionSource, options: DaemonDecisionOptions = {}): DecisionSource & {dispose(): void} {
  const now = options.now ?? Date.now;
  const records = new Map<string, Record_>();
  const runs = new Map<string, RunSummary>();
  const restored = new Set<string>();
  const dirty = new Set<string>();
  let reset = false;
  let counter = 0;
  let lastWaitMax = 0;
  let sessionIds = new Set<string>();
  const waiters = new Set<() => void>();
  let syncing: Promise<void> | undefined;

  const bump = (id?: string, full = false) => {
    counter++;
    if (id) dirty.add(id);
    if (full) reset = true;
    [...waiters].forEach(w => w());
  };
  const available = () => {
    const s = client.status();
    return s.daemon === 'up' && s.paired;
  };

  // ---- 사건 → 기록 ----
  /** 사건을 기록에 반영하고, 바뀐 카드 id들을 돌려준다 */
  function apply(e: DaemonEvent): string[] {
    const ev = e.event;
    if (e.type === 'decision.asked') {
      const decisionId = str(ev.id);
      if (!decisionId) return [];
      const id = cardId(e.runId, decisionId);
      if (records.has(id)) return [];
      records.set(id, {
        runId: e.runId, decisionId, askedSeq: e.seq, askedAt: e.at, title: str(ev.title) ?? decisionId,
        options: Array.isArray(ev.options) ? ev.options.filter((o): o is string => typeof o === 'string') : [],
        key: ev.key, args: ev.args, kind: str(ev.kind), name: str(ev.name), context: str(ev.context), state: 'open',
      });
      return [id];
    }
    if (e.type === 'decision.answered') {
      const decisionId = str(ev.id);
      const id = decisionId ? cardId(e.runId, decisionId) : '';
      const r = records.get(id);
      if (!r || r.state !== 'open') return [];
      r.state = 'answered';
      r.answer = {text: str(ev.answer) ?? '', at: e.at, by: r.answer?.by ?? 'daemon'};
      r.closedAt = e.at;
      return [id];
    }
    if (e.type === 'run.finished') {
      const changed: string[] = [];
      for (const [id, r] of records) {
        if (r.runId === e.runId && r.state === 'open') {
          r.state = 'withdrawn';
          r.closedAt = e.at;
          r.closeReason = 'run_finished';
          changed.push(id);
        }
      }
      return changed;
    }
    return [];
  }

  const offEvent = client.onEvent(e => {
    if (!e.type.startsWith('decision.') && e.type !== 'run.finished') return;
    const changed = apply(e);
    if (changed.length) {
      changed.forEach(id => dirty.add(id));
      bump();
    }
  });
  let lastAvail = available();
  const offStatus = client.onStatus(() => {
    const now_ = available();
    if (now_ !== lastAvail) {
      lastAvail = now_;
      // 데몬이 올라오거나 내려가면 목록 전체가 바뀐다(orb 카드 가림도 달라진다)
      bump(undefined, true);
    }
  });

  // ---- 동기화: 실행 목록, 복원, 어긋난 열린 카드 정리 ----
  async function restore(runId: string): Promise<void> {
    try {
      const {events} = await client.api.history({runId, afterSeq: 0});
      for (const e of events) if (e.type.startsWith('decision.') || e.type === 'run.finished') apply(e);
      restored.add(runId);
    } catch (e) {
      options.log?.warn(`Could not restore decision cards of run ${runId}: ${errorMessage(e)}`);
    }
  }

  async function doSync(): Promise<void> {
    const {runs: list} = await client.api.listRuns({limit: RUNS_LIMIT});
    runs.clear();
    for (const r of list) runs.set(r.runId, r);
    const gone = new Set<string>();
    sessionIds = new Set(list.map(r => r.sessionId).filter(Boolean));
    const dayStart = startOfDay(now());
    const need = list.filter(r => {
      if (restored.has(r.runId)) return false;
      const openUnknown = r.state === 'running' && r.openDecisions.some(d => !records.has(cardId(r.runId, d.id)));
      return openUnknown || ms(r.startedAt) >= dayStart || ms(r.finishedAt) >= dayStart || r.state === 'running';
    }).slice(0, RESTORE_MAX);
    for (const r of need) await restore(r.runId);
    // 최근 RUNS_LIMIT개 밖으로 밀린 실행의 열린 기록은 GetRun으로 직접 확인한다(없거나 끝났으면 아래에서 거둔다)
    const missing = new Set([...records.values()].filter(r => r.state === 'open' && !runs.has(r.runId)).map(r => r.runId));
    for (const runId of missing) {
      try {
        const {run: found} = await client.api.getRun({runId});
        runs.set(runId, found);
        if (found.sessionId) sessionIds.add(found.sessionId);
      } catch (e) {
        // 데몬이 모르는 실행이면 그 실행의 열린 카드는 거둔다. 그 밖의 오류는 다음에 다시 본다
        if ((e as {code?: unknown}).code === 'RPC_NOT_FOUND') gone.add(runId);
        else options.log?.warn(`Could not check run ${runId}: ${errorMessage(e)}`);
      }
    }
    // 열린 카드를 ListRuns와 맞춘다: 실행이 끝났거나 그 결정이 더는 열려 있지 않으면 거둔다.
    // askedSeq가 ListRuns 시점의 lastSeq보다 뒤인 카드는 목록이 읽힌 뒤에 생긴 것이라 건드리지 않는다.
    for (const [id, rec] of records) {
      if (rec.state !== 'open') continue;
      const run = runs.get(rec.runId);
      if (!run && gone.has(rec.runId)) {
        rec.state = 'withdrawn';
        rec.closedAt = new Date(now()).toISOString();
        rec.closeReason = 'run_ended';
        continue;
      }
      if (!run || rec.askedSeq > run.lastSeq) continue;
      if (run.state !== 'running' || !run.openDecisions.some(d => d.id === rec.decisionId)) {
        rec.state = 'withdrawn';
        rec.closedAt = run.finishedAt || new Date(now()).toISOString();
        rec.closeReason = 'run_ended';
      }
    }
    if (records.size > RECORDS_MAX) {
      const closed = [...records].filter(([, r]) => r.state !== 'open').sort((a, b) => ms(a[1].closedAt) - ms(b[1].closedAt));
      for (const [id] of closed.slice(0, records.size - RECORDS_MAX)) records.delete(id);
    }
    // ListRuns에는 열려 있는데 기록이 없는 카드(복원 실패)는 제목과 선택지만으로 만든다
    for (const run of list) {
      if (run.state !== 'running') continue;
      for (const d of run.openDecisions) {
        const id = cardId(run.runId, d.id);
        if (!records.has(id)) records.set(id, {runId: run.runId, decisionId: d.id, askedSeq: 0, askedAt: d.askedAt, title: d.title, options: d.options, state: 'open'});
      }
    }
  }

  function sync(): Promise<void> {
    syncing ??= doSync().finally(() => { syncing = undefined; });
    return syncing;
  }

  // ---- 기록 → 카드 ----
  function render(rec: Record_): Decision {
    const run = runs.get(rec.runId);
    const id = cardId(rec.runId, rec.decisionId);
    const text = client.text(rec.key, rec.args, rec.title);
    const known = rec.options.includes(rec.answer?.text ?? '');
    return {
      id, state: rec.state, source: rec.kind === 'tool' ? 'permission' : 'question', owner: 'orbit-pi',
      createdAt: rec.askedAt, updatedAt: rec.closedAt ?? rec.askedAt,
      questions: [{text, multiSelect: false, options: rec.options.map(o => ({id: o, label: client.optionLabel(o)}))}],
      ...(rec.context ? {context: rec.context} : {}),
      links: [],
      work: {host: 'orbit-pi', ...(run?.sessionId ? {sessionId: run.sessionId} : {}), ...(run?.workId ? {workId: run.workId} : {})},
      ...(rec.closedAt ? {closedAt: rec.closedAt} : {}),
      ...(rec.closeReason ? {closeReason: rec.closeReason} : {}),
      ...(rec.answer ? {answer: {answers: [known ? {optionIds: [rec.answer.text]} : {text: rec.answer.text}], by: rec.answer.by, at: rec.answer.at}} : {}),
    };
  }

  /**
   * orb 카드 가림: 데몬이 아는 실행의 세션이 연 orbit-pi 카드는 데몬 카드로 이미 보인다.
   * 가정 카드(AU-3)는 데몬 이벤트에 없으므로 가리지 않는다.
   */
  const hiddenOrb = (d: Decision) => d.source !== 'assumption' && d.work?.host === 'orbit-pi' && !!d.work.sessionId && sessionIds.has(d.work.sessionId);
  const seqOf = (orbSeq: string, c: number) => `${orbSeq}|${c}`;
  const splitSeq = (after: string | undefined) => {
    const i = (after ?? '').lastIndexOf('|');
    return i < 0 ? {orb: after || undefined, n: undefined} : {orb: after!.slice(0, i) || undefined, n: Number(after!.slice(i + 1))};
  };

  async function orbPage(q: Parameters<DecisionSource['list']>[0], signal: AbortSignal | undefined, tolerate: boolean): Promise<DecisionPage> {
    try {
      return await orb.list(q, signal);
    } catch (e) {
      if (!tolerate) throw e;
      options.log?.warn(`orb decision cards could not be read; showing daemon cards only: ${errorMessage(e)}`);
      return {items: [], seq: '', truncated: false};
    }
  }

  return {
    async list(q, signal) {
      const at = counter;
      let daemonCards: Decision[] = [];
      let up = available();
      if (up) {
        try {
          await sync();
          const wantOpen = q.state.includes('open');
          const since = q.updatedSince ? ms(q.updatedSince) : 0;
          daemonCards = [...records.values()]
            .filter(r => (r.state === 'open' ? wantOpen : q.state.includes(r.state) && ms(r.closedAt) >= since))
            .map(render);
        } catch (e) {
          options.log?.warn(`Could not read daemon decision cards: ${errorMessage(e)}`);
          up = false;
        }
      }
      const page = await orbPage(q, signal, up);
      const items = up ? [...daemonCards.sort((a, b) => ms(a.createdAt) - ms(b.createdAt)), ...page.items.filter(d => !hiddenOrb(d))] : page.items;
      return {items, seq: seqOf(page.seq, at), truncated: page.truncated};
    },

    async changes(q, signal): Promise<DecisionChanges> {
      const {orb: orbAfter, n} = splitSeq(q.after);
      let seen = Number.isFinite(n) && n !== undefined ? n : counter;
      const take = (orbIds: string[] = [], orbReset = false) => {
        const ids = [...new Set([...orbIds, ...dirty])];
        const full = orbReset || reset;
        dirty.clear();
        reset = false;
        seen = counter;
        return {ids, full};
      };
      if (counter !== seen) {
        const t = take();
        return {seq: seqOf(orbAfter ?? '', seen), decisionIds: t.ids, reset: t.full, more: false, waitMax: lastWaitMax};
      }
      const ac = new AbortController();
      const onAbort = () => ac.abort();
      signal.addEventListener('abort', onAbort);
      let wakeUp!: () => void;
      const woken = new Promise<'daemon'>(resolve => {
        wakeUp = () => resolve('daemon');
        waiters.add(wakeUp);
      });
      const orbCall = orb.changes({...(orbAfter ? {after: orbAfter} : {}), wait: q.wait}, ac.signal).then(
        value => ({value}) as const,
        error => ({error}) as const,
      );
      try {
        const first = await Promise.race([woken, orbCall]);
        if (first === 'daemon') {
          ac.abort();
          await orbCall;
          const t = take();
          return {seq: seqOf(orbAfter ?? '', seen), decisionIds: t.ids, reset: t.full, more: false, waitMax: lastWaitMax};
        }
        if ('value' in first) {
          lastWaitMax = first.value.waitMax;
          const t = take(first.value.decisionIds, first.value.reset);
          return {seq: seqOf(first.value.seq, seen), decisionIds: t.ids, reset: t.full, more: first.value.more, waitMax: first.value.waitMax};
        }
        if (signal.aborted || !available()) throw first.error;
        // orb 쪽이 안 되어도 데몬이 되어 있으면 데몬 알림만 기다린다(바쁜 반복을 피하려고 쉰다)
        options.log?.warn(`orb decision changes failed; following the daemon only: ${errorMessage(first.error)}`);
        await Promise.race([woken, new Promise<void>(resolve => {
          const timer = setTimeout(resolve, RETRY_MS);
          signal.addEventListener('abort', () => { clearTimeout(timer); resolve(); }, {once: true});
        })]);
        const t = take();
        return {seq: seqOf(orbAfter ?? '', seen), decisionIds: t.ids, reset: t.full, more: false, waitMax: lastWaitMax};
      } finally {
        waiters.delete(wakeUp);
        signal.removeEventListener('abort', onAbort);
        ac.abort();
      }
    },

    async answer(id, answers: AnswerPart[], signal): Promise<AnswerOutcome> {
      const parsed = parseCardId(id);
      if (!parsed) return orb.answer(id, answers, signal);
      const part = answers[0];
      const answer = part?.optionIds?.[0] ?? part?.text ?? '';
      if (!answer) return {ok: false, code: 'FAILED', message: 'The answer is empty'};
      try {
        const {accepted} = await client.api.answer({runId: parsed.runId, decisionId: parsed.decisionId, answer});
        const rec = records.get(id);
        if (!accepted) {
          // 다른 곳(터미널·orb 카드)에서 먼저 답했거나 거두어졌다: 목록을 다시 읽게 깨운다
          bump(id, true);
          return {ok: false, code: 'CONFLICT', message: 'The card was already answered elsewhere or withdrawn'};
        }
        if (rec && rec.state === 'open') {
          rec.state = 'answered';
          rec.answer = {text: answer, at: new Date(now()).toISOString(), by: 'studio'};
          rec.closedAt = rec.answer.at;
          bump(id);
        }
        return {ok: true, ...(rec ? {decision: render(rec)} : {})};
      } catch (e) {
        const code = (e as {code?: unknown}).code;
        return {ok: false, code: code === 'RPC_NOT_FOUND' ? 'NOT_FOUND' : 'FAILED', message: errorMessage(e)};
      }
    },

    dispose() {
      offEvent();
      offStatus();
      waiters.clear();
    },
  };
}

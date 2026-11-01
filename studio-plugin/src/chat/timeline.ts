// 실행 한 건의 이벤트(DaemonEvent)를 화면에 그릴 항목(Entry)으로 줄인다. 순수 함수만 있다.
// seq로 줄 세우고 같은 seq는 한 번만 센다: 끊겼다 다시 와도 빠짐·겹침이 없다. 모르는 type은 건너뛴다.
import type {DaemonEvent, RunSummary} from '../daemon/api.ts';
import {planBatches} from './plan.ts';

type Fields = Record<string, unknown>;

/** seq 순서로 합친다(같은 seq는 뒤에 온 것으로 바꾼다). runId를 주면 다른 실행의 이벤트는 버린다. */
export function mergeEvents(current: readonly DaemonEvent[], incoming: readonly DaemonEvent[], runId?: string): DaemonEvent[] {
  const bySeq = new Map<number, DaemonEvent>();
  for (const e of current) bySeq.set(e.seq, e);
  let changed = false;
  for (const e of incoming) {
    if (runId !== undefined && e.runId !== runId) continue;
    if (typeof e.seq !== 'number') continue;
    if (bySeq.get(e.seq) !== e) changed = true;
    bySeq.set(e.seq, e);
  }
  if (!changed) return current as DaemonEvent[];
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq);
}

export function lastSeq(events: readonly DaemonEvent[]): number {
  return events.length ? events[events.length - 1].seq : 0;
}

const str = (f: Fields, k: string): string => (typeof f[k] === 'string' ? (f[k] as string) : '');
const optStr = (f: Fields, k: string): string | undefined => (typeof f[k] === 'string' && f[k] !== '' ? (f[k] as string) : undefined);
const num = (f: Fields, k: string): number => (typeof f[k] === 'number' && Number.isFinite(f[k]) ? (f[k] as number) : 0);
const strList = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
const keyArgs = (f: Fields): {key?: string; args?: (string | number)[]} => ({
  key: optStr(f, 'key'),
  args: Array.isArray(f.args) ? (f.args as (string | number)[]) : undefined,
});

export interface PlanStepView {
  id: string;
  title: string;
  role: string;
  after: string[];
  done: string;
  verify?: string;
  commit?: boolean;
}

export type StepStatus = 'running' | 'done' | 'retry' | 'failed' | 'aborted';

interface Base {
  /** 이 항목이 처음 생긴 seq. React key에 쓴다. */
  seq: number;
  at: string;
}

export type Entry =
  | (Base & {kind: 'started'; request: string; cwd: string; profile: string; mode: string; resume: boolean})
  | (Base & {kind: 'work'; workId: string})
  | (Base & {kind: 'plan'; goal: string; steps: PlanStepView[]; batches: string[][]; restored: boolean; done: string[]})
  | (Base & {kind: 'planRejected'; errors: string[]})
  | (Base & {kind: 'planApproved'; by: string})
  | (Base & {
      kind: 'step';
      step: string;
      attempt: number;
      title: string;
      role: string;
      engine: string;
      model?: string;
      status: StepStatus;
      inputTokens: number;
      outputTokens: number;
      costUsd?: number;
      turns: number;
      reason?: string;
      output: string;
    })
  | (Base & {kind: 'skipped'; role: string; ref: string; reason: string; detail?: string})
  | (Base & {kind: 'fallback'; role: string; from: string; to?: string; error: string})
  | (Base & {kind: 'verify'; step: string; attempt: number; command: string; ok: boolean; outputTail: string})
  | (Base & {kind: 'commit'; step: string; sha: string; subject?: string})
  | (Base & {kind: 'review'; verdict: string; reason: string})
  | (Base & {kind: 'warning'; message: string; key?: string; args?: (string | number)[]})
  | (Base & {kind: 'limit'; limit: string; detail: string; key?: string; args?: (string | number)[]})
  | (Base & {
      kind: 'decision';
      id: string;
      title: string;
      options: string[];
      decisionKind: 'tool' | 'question';
      name?: string;
      context?: string;
      key?: string;
      args?: (string | number)[];
      answered: boolean;
      answer?: string;
    })
  | (Base & {
      kind: 'finished';
      status: string;
      reason?: string;
      exitCode: number;
      workId?: string;
      message: string;
      key?: string;
      args?: (string | number)[];
      totals?: Fields;
    });

function planView(raw: unknown): PlanStepView[] {
  const steps = (raw as {steps?: unknown} | undefined)?.steps;
  if (!Array.isArray(steps)) return [];
  return steps
    .filter((s): s is Fields => !!s && typeof s === 'object')
    .map(s => ({
      id: str(s, 'id'),
      title: str(s, 'title'),
      role: str(s, 'role'),
      after: strList(s.after),
      done: str(s, 'done'),
      verify: optStr(s, 'verify'),
      commit: s.commit === true ? true : undefined,
    }));
}

export function buildTimeline(events: readonly DaemonEvent[]): Entry[] {
  const out: Entry[] = [];
  const steps = new Map<string, Extract<Entry, {kind: 'step'}>>();
  const decisions = new Map<string, Extract<Entry, {kind: 'decision'}>>();
  const stepOf = (f: Fields, seq: number, at: string) => {
    const id = `${str(f, 'step')}#${num(f, 'attempt')}`;
    let e = steps.get(id);
    if (!e) {
      // 시작 이벤트가 없어도(기록이 잘렸을 때) 항목은 만든다
      e = {
        kind: 'step', seq, at, step: str(f, 'step'), attempt: num(f, 'attempt'), title: str(f, 'title'), role: str(f, 'role'), engine: str(f, 'engine'),
        model: optStr(f, 'model'), status: 'running', inputTokens: 0, outputTokens: 0, turns: 0, output: '',
      };
      steps.set(id, e);
      out.push(e);
    }
    return e;
  };
  for (const ev of events) {
    const f = ev.event as Fields;
    const base = {seq: ev.seq, at: ev.at};
    switch (f.type) {
      case 'run.started':
        out.push({...base, kind: 'started', request: str(f, 'request'), cwd: str(f, 'cwd'), profile: str(f, 'profile'), mode: str(f, 'mode') === 'default' ? 'plan-approve' : str(f, 'mode') || 'ultrawork', resume: f.resume === true});
        break;
      case 'work.recorded':
        out.push({...base, kind: 'work', workId: str(f, 'workId')});
        break;
      case 'plan.proposed':
      case 'plan.restored': {
        const plan = planView(f.plan);
        out.push({...base, kind: 'plan', goal: str((f.plan ?? {}) as Fields, 'goal'), steps: plan, batches: planBatches(plan), restored: f.type === 'plan.restored', done: strList(f.done)});
        break;
      }
      case 'plan.rejected':
        out.push({...base, kind: 'planRejected', errors: strList(f.errors)});
        break;
      case 'plan.approved':
        out.push({...base, kind: 'planApproved', by: str(f, 'by')});
        break;
      case 'step.started': {
        const e = stepOf(f, ev.seq, ev.at);
        e.title = str(f, 'title') || e.title;
        e.role = str(f, 'role') || e.role;
        e.engine = str(f, 'engine') || e.engine;
        e.model = optStr(f, 'model') ?? e.model;
        break;
      }
      case 'step.output': {
        const e = stepOf(f, ev.seq, ev.at);
        e.output += str(f, 'text');
        break;
      }
      case 'step.finished': {
        const e = stepOf(f, ev.seq, ev.at);
        const status = str(f, 'status');
        e.status = status === 'done' || status === 'retry' || status === 'failed' || status === 'aborted' ? status : 'failed';
        e.role = str(f, 'role') || e.role;
        e.engine = str(f, 'engine') || e.engine;
        e.model = optStr(f, 'model') ?? e.model;
        e.inputTokens = num(f, 'inputTokens');
        e.outputTokens = num(f, 'outputTokens');
        e.costUsd = typeof f.costUsd === 'number' ? f.costUsd : undefined;
        e.turns = num(f, 'turns');
        e.reason = optStr(f, 'reason');
        break;
      }
      case 'model.skipped':
        out.push({...base, kind: 'skipped', role: str(f, 'role'), ref: str(f, 'ref'), reason: str(f, 'reason'), detail: optStr(f, 'detail')});
        break;
      case 'model.fallback':
        out.push({...base, kind: 'fallback', role: str(f, 'role'), from: str(f, 'from'), to: optStr(f, 'to'), error: str(f, 'error')});
        break;
      case 'verify.result':
        out.push({...base, kind: 'verify', step: str(f, 'step'), attempt: num(f, 'attempt'), command: str(f, 'command'), ok: f.ok === true, outputTail: str(f, 'outputTail')});
        break;
      case 'commit.found':
        out.push({...base, kind: 'commit', step: str(f, 'step'), sha: str(f, 'sha'), subject: optStr(f, 'subject')});
        break;
      case 'review.result':
        out.push({...base, kind: 'review', verdict: str(f, 'verdict'), reason: str(f, 'reason')});
        break;
      case 'warning':
        out.push({...base, kind: 'warning', message: str(f, 'message'), ...keyArgs(f)});
        break;
      case 'limit.reached':
        out.push({...base, kind: 'limit', limit: str(f, 'limit'), detail: str(f, 'detail'), ...keyArgs(f)});
        break;
      case 'decision.asked': {
        const id = str(f, 'id');
        const d: Extract<Entry, {kind: 'decision'}> = {
          ...base, kind: 'decision', id, title: str(f, 'title'), options: strList(f.options), decisionKind: f.kind === 'tool' ? 'tool' : 'question',
          name: optStr(f, 'name'), context: optStr(f, 'context'), answered: false, ...keyArgs(f),
        };
        decisions.set(id, d);
        out.push(d);
        break;
      }
      case 'decision.answered': {
        const d = decisions.get(str(f, 'id'));
        if (d) {
          d.answered = true;
          d.answer = optStr(f, 'answer');
        }
        break;
      }
      case 'run.finished':
        out.push({
          ...base, kind: 'finished', status: str(f, 'status'), reason: optStr(f, 'reason'), exitCode: num(f, 'exitCode'), workId: optStr(f, 'workId'),
          message: str(f, 'message'), totals: f.totals && typeof f.totals === 'object' ? (f.totals as Fields) : undefined, ...keyArgs(f),
        });
        break;
      default:
        break; // 모르는 type은 건너뛴다
    }
  }
  return out;
}

/** 아직 decision.answered가 없는 결정 카드(질문 순서대로). */
export function openDecisions(entries: readonly Entry[]): Extract<Entry, {kind: 'decision'}>[] {
  return entries.filter((e): e is Extract<Entry, {kind: 'decision'}> => e.kind === 'decision' && !e.answered);
}

/** 실행 상태: 끝 이벤트가 있으면 그 상태, 없으면 daemon 요약(interrupted는 요약에만 있다), 둘 다 없으면 running. */
export function runState(entries: readonly Entry[], summary?: RunSummary): string {
  const fin = entries.find((e): e is Extract<Entry, {kind: 'finished'}> => e.kind === 'finished');
  if (fin) return fin.status;
  return summary?.state || 'running';
}

export interface ShownTotals {
  costUsd: number;
  subscription: boolean;
  turns: number;
  minutes: number;
  inputTokens: number;
  outputTokens: number;
  monthSpentUsd?: number;
  monthLimitUsd?: number;
}

/** 머리의 합계: 끝 이벤트의 totals > daemon 요약 > 단계 합산 순. */
export function runTotals(entries: readonly Entry[], summary?: RunSummary): ShownTotals {
  const fin = entries.find((e): e is Extract<Entry, {kind: 'finished'}> => e.kind === 'finished');
  const t = fin?.totals;
  if (t) {
    return {
      costUsd: num(t, 'costUsd'), subscription: t.subscription === true, turns: num(t, 'turns'), minutes: num(t, 'minutes'),
      inputTokens: num(t, 'inputTokens'), outputTokens: num(t, 'outputTokens'),
      monthSpentUsd: typeof t.monthSpentUsd === 'number' ? t.monthSpentUsd : undefined,
      monthLimitUsd: typeof t.monthLimitUsd === 'number' ? t.monthLimitUsd : undefined,
    };
  }
  const sum = {costUsd: 0, subscription: false, turns: 0, minutes: 0, inputTokens: 0, outputTokens: 0};
  for (const e of entries) {
    if (e.kind !== 'step' || e.status === 'running') continue;
    sum.costUsd += e.costUsd ?? 0;
    if (e.costUsd === undefined) sum.subscription = true;
    sum.turns += e.turns;
    sum.inputTokens += e.inputTokens;
    sum.outputTokens += e.outputTokens;
  }
  const s = summary?.totals;
  if (s && (s.turns > 0 || s.costUsd > 0 || s.minutes > 0)) {
    return {
      ...sum, costUsd: s.costUsd, subscription: s.subscription, turns: s.turns, minutes: s.minutes, inputTokens: s.inputTokens || sum.inputTokens,
      outputTokens: s.outputTokens || sum.outputTokens, monthSpentUsd: s.monthSpentUsd, monthLimitUsd: s.monthLimitUsd || undefined,
    };
  }
  return {...sum, monthSpentUsd: s?.monthSpentUsd, monthLimitUsd: s?.monthLimitUsd || undefined};
}

/** 실행 목록 정렬: 진행 중이 먼저, 그다음 시작이 늦은 것부터. */
export function sortRuns<T extends Pick<RunSummary, 'state' | 'startedAt'>>(runs: readonly T[]): T[] {
  const rank = (r: T) => (r.state === 'running' ? 0 : 1);
  return [...runs].sort((a, b) => rank(a) - rank(b) || (a.startedAt < b.startedAt ? 1 : a.startedAt > b.startedAt ? -1 : 0));
}

/** 멈춤은 진행 중일 때만. */
export const canStop = (state: string) => state === 'running';
/** 이어가기는 멈춤·끊김·실패이고 workId가 있을 때만. */
export const canResume = (state: string, workId: string | undefined) => !!workId && (state === 'stopped' || state === 'interrupted' || state === 'failed');

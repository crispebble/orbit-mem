// 작업 계약(orbit.work) 응답 검사: 판(contract) 확인과 모양 확인.
// response.schema.json으로 거르지 않는다(계약 README 「판」). 필요한 칸만 확인하고 모르는 칸은 무시한다.
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import type {
  Assumption, ChangesResult, ContractInfo, Discovery, GetResult, ListPage, MetricsResult, SessionPage, SessionsPage, StepRecall,
  TrendKey, TrendSide, WorkMetricsDoc,
} from './types.ts';

export const READ_MAJOR = 1;

/** 이 데이터만 멈추는 이유. 기억·제안은 영향 없다. */
export type PauseKind = 'orb-newer' | 'orbit-older';

/** 판이 맞지 않는 데이터를 주는 쪽: 작업·세션은 orbit-code(`orb work mcp`), 기억 그래프는 orbit-mem(`orbit-mem mcp --reader`) */
export type PauseSource = 'orbit-code' | 'orbit-mem';

export function pauseMessage(kind: PauseKind, source: PauseSource = 'orbit-code'): string {
  const e = pick(workText).error;
  return kind === 'orb-newer' ? e.orbNewer(source) : e.orbitOlder(source);
}

/** 판 때문에 읽지 못함 */
export class WorkPaused extends Error {
  kind: PauseKind;
  source: PauseSource;
  constructor(kind: PauseKind, source: PauseSource = 'orbit-code') {
    super(pauseMessage(kind, source));
    this.name = 'WorkPaused';
    this.kind = kind;
    this.source = source;
  }
}

/** 계약이 밝힌 오류(NOT_FOUND 등). 판 문제는 WorkPaused. */
export class WorkToolError extends Error {
  code: string;
  field?: string;
  constructor(code: string, message: string, field?: string) {
    super(message);
    this.name = 'WorkToolError';
    this.code = code;
    this.field = field;
  }
}

/** 응답 모양이 맞지 않음 */
export class WorkShapeError extends Error {
  constructor(tool: string, why: string) {
    super(pick(workText).error.shape(tool, why));
    this.name = 'WorkShapeError';
  }
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v);

/** 응답 맨 위 contract를 읽는다. 읽을 수 없는 판이면 WorkPaused. */
export function checkContract(out: unknown, tool: string): ContractInfo {
  const c = isObj(out) ? out.contract : undefined;
  if (!isObj(c) || !isInt(c.major)) throw new WorkShapeError(tool, pick(workText).error.whyNoContract);
  const supported = Array.isArray(c.supportedMajors) ? c.supportedMajors.filter(isInt) : [];
  const info: ContractInfo = {
    name: typeof c.name === 'string' ? c.name : 'orbit.work',
    major: c.major,
    minor: isInt(c.minor) ? c.minor : 0,
    supportedMajors: supported,
  };
  const err = isObj(out) && isObj((out as Record<string, unknown>).error) ? ((out as Record<string, unknown>).error as Record<string, unknown>) : undefined;
  if (err?.code === 'UNSUPPORTED_MAJOR') {
    // orb가 우리 판(1)을 받지 못한다 = orb가 더 새 판. 지원 목록이 1을 품으면 이상한 응답이므로 모양 오류로 둔다.
    const list = Array.isArray(err.supportedMajors) ? err.supportedMajors.filter(isInt) : supported;
    if (list.includes(READ_MAJOR)) throw new WorkShapeError(tool, pick(workText).error.whyUnsupportedMajor);
    throw new WorkPaused(info.major < READ_MAJOR ? 'orbit-older' : 'orb-newer');
  }
  if (info.major > READ_MAJOR) {
    if (supported.includes(READ_MAJOR)) return info; // 1도 읽어 줄 수 있다고 밝힘(우리는 major:1을 요청한다)
    throw new WorkPaused('orb-newer');
  }
  if (info.major < READ_MAJOR) throw new WorkPaused('orbit-older');
  return info;
}

/** 오류 응답이면 던진다. 아니면 그대로 */
export function throwIfError(out: unknown, tool: string): void {
  if (!isObj(out) || !isObj(out.error)) return;
  const e = out.error;
  throw new WorkToolError(
    typeof e.code === 'string' ? e.code : 'UNKNOWN',
    typeof e.message === 'string' ? e.message : pick(workText).error.callFailed(tool),
    typeof e.field === 'string' ? e.field : undefined,
  );
}

const str = (v: unknown): v is string => typeof v === 'string';
const strList = (v: unknown): string[] => (Array.isArray(v) ? v.filter(str) : []);
const objList = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

function need(cond: boolean, tool: string, why: string): void {
  if (!cond) throw new WorkShapeError(tool, why);
}

function checkWorkHead(w: unknown, tool: string): void {
  need(isObj(w) && str(w.id) && str(w.title) && str(w.state) && str(w.updatedAt) && str(w.openedAt) && isObj(w.counts), tool, pick(workText).error.whyWork);
}

export function parseList(out: unknown): ListPage {
  const tool = 'work_list';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && Array.isArray(out.items), tool, 'seq·items');
  const o = out as Record<string, unknown>;
  const items = objList(o.items);
  items.forEach(i => {
    checkWorkHead(i, tool);
    need(isObj(i) && isInt(i.stepCount) && isObj(i.verification), tool, 'stepCount·verification');
  });
  return {
    seq: o.seq as string,
    items: items as ListPage['items'],
    nextCursor: str(o.nextCursor) ? o.nextCursor : undefined,
    limitMax: isInt(o.limitMax) ? o.limitMax : 20,
  };
}

const optStr = (v: unknown): string | undefined => (str(v) && v !== '' ? v : undefined);

/** 조사(1.3). 객체가 아니면 없는 것으로 본다(`problem`이 없거나 틀리면 빈 글로 두고 나머지는 살린다)(조사 칸 때문에 작업 전체를 못 읽게 하지 않는다). */
export function normalizeDiscovery(v: unknown): Discovery | undefined {
  if (!isObj(v)) return undefined;
  const decisions = objList(v.decisions).flatMap(d =>
    isObj(d) && str(d.point) ? [{point: d.point, evidence: strList(d.evidence), ...(optStr(d.settled) ? {settled: optStr(d.settled)} : {})}] : []);
  return {
    problem: str(v.problem) ? v.problem : '',
    decisions,
    ...(optStr(v.idealEnd) ? {idealEnd: optStr(v.idealEnd)} : {}),
    unknowns: strList(v.unknowns),
    sources: strList(v.sources),
    ...(optStr(v.size) ? {size: optStr(v.size)} : {}),
    ...(isInt(v.rounds) ? {rounds: v.rounds} : {}),
    ...(optStr(v.by) ? {by: optStr(v.by)} : {}),
    at: str(v.at) ? v.at : '',
  };
}

/** 가정 목록(1.3). id·point가 없는 줄은 버리고, 모르는 열거값은 그대로 둔다. */
export function normalizeAssumptions(v: unknown): Assumption[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const byId = new Map<string, Assumption>();
  v.forEach(a => {
    for (const x of one(a)) byId.set(x.id, x); // 같은 id는 나중 줄이 이긴다(생산 쪽 upsert와 같다). 자리는 처음 나온 자리
  });
  const list = [...byId.values()];
  return list.length ? list : undefined;

  function one(a: unknown): Assumption[] {
    if (!isObj(a) || !str(a.id) || a.id === '' || !str(a.point)) return [];
    const options = strList(a.options);
    return [{
      id: a.id, point: a.point, chosen: str(a.chosen) ? a.chosen : '', basis: str(a.basis) ? a.basis : '',
      confidence: str(a.confidence) ? a.confidence : '', rung: str(a.rung) ? a.rung : '', state: str(a.state) ? a.state : '',
      ...(options.length ? {options} : {}),
      ...(optStr(a.cardId) ? {cardId: optStr(a.cardId)} : {}),
      steps: strList(a.steps),
      ...(optStr(a.answer) ? {answer: optStr(a.answer)} : {}),
      ...(optStr(a.answeredBy) ? {answeredBy: optStr(a.answeredBy)} : {}),
      ...(typeof a.answeredAt === 'number' && Number.isFinite(a.answeredAt) ? {answeredAt: a.answeredAt} : {}),
      at: str(a.at) ? a.at : '',
    }];
  }
}

/** 단계별 회상(1.4). 객체가 아니거나 id 있는 기억이 하나도 없으면 없는 것으로 본다(회상 칸 때문에 작업을 못 읽게 하지 않는다). */
export function normalizeRecall(v: unknown): StepRecall | undefined {
  if (!isObj(v)) return undefined;
  const memories = objList(v.memories).flatMap(m =>
    isObj(m) && str(m.id) && m.id !== ''
      ? [{id: m.id, ...(optStr(m.kind) ? {kind: optStr(m.kind)} : {}), ...(optStr(m.text) ? {text: optStr(m.text)} : {})}]
      : []);
  if (memories.length === 0) return undefined;
  return {
    ...(isInt(v.attempt) ? {attempt: v.attempt} : {}),
    ...(optStr(v.query) ? {query: optStr(v.query)} : {}),
    memories,
    at: str(v.at) ? v.at : '',
  };
}

function normalizeWork(w: unknown, tool: string): GetResult['work'] {
  checkWorkHead(w, tool);
  const o = w as Record<string, unknown>;
  need(Array.isArray(o.steps), tool, 'steps');
  for (const s of o.steps as unknown[]) {
    need(isObj(s) && str(s.id) && str(s.title) && str(s.status) && isObj(s.verification), tool, pick(workText).error.whyStep);
  }
  const children = Array.isArray(o.children) ? o.children.map(c => normalizeWork(c, tool)) : undefined;
  const discovery = normalizeDiscovery(o.discovery);
  const assumptions = normalizeAssumptions(o.assumptions);
  const {discovery: _d, assumptions: _a, ...rest} = o;
  return {
    ...(rest as unknown as GetResult['work']),
    ...(discovery ? {discovery} : {}),
    ...(assumptions ? {assumptions} : {}),
    steps: (o.steps as GetResult['work']['steps']).map(({recall: rawRecall, ...s}) => {
      const recall = normalizeRecall(rawRecall);
      return {
        ...s,
        after: objList(s.after) as GetResult['work']['steps'][number]['after'],
        evidenceIds: strList(s.evidenceIds),
        childWorkIds: strList(s.childWorkIds),
        ...(recall ? {recall} : {}),
      };
    }),
    runs: objList(o.runs) as GetResult['work']['runs'],
    evidence: objList(o.evidence) as GetResult['work']['evidence'],
    children,
  };
}

export function parseGet(out: unknown): GetResult {
  const tool = 'work_get';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && isObj(out.work), tool, 'seq·work');
  const o = out as Record<string, unknown>;
  return {seq: o.seq as string, work: normalizeWork(o.work, tool)};
}

function checkSession(s: unknown, tool: string): void {
  need(isObj(s) && str(s.sessionId) && str(s.state) && str(s.startedAt) && str(s.lastSeenAt), tool, pick(workText).error.whySession);
}

function normalizeSession(s: unknown): SessionsPage['sessions'][number] {
  const o = s as Record<string, unknown>;
  return {...(o as unknown as SessionsPage['sessions'][number]), openWorkIds: strList(o.openWorkIds), activeStepIds: strList(o.activeStepIds)};
}

export function parseSessions(out: unknown): SessionsPage {
  const tool = 'work_sessions';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && Array.isArray(out.sessions), tool, 'seq·sessions');
  const o = out as Record<string, unknown>;
  const sessions = objList(o.sessions);
  sessions.forEach(s => checkSession(s, tool));
  return {
    seq: o.seq as string,
    sessions: sessions.map(normalizeSession),
    nextCursor: str(o.nextCursor) ? o.nextCursor : undefined,
    limitMax: isInt(o.limitMax) ? o.limitMax : 20,
  };
}

export function parseSession(out: unknown): SessionPage {
  const tool = 'work_sessions';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && isObj(out.session) && Array.isArray(out.events), tool, 'seq·session·events');
  const o = out as Record<string, unknown>;
  checkSession(o.session, tool);
  return {
    seq: o.seq as string,
    session: normalizeSession(o.session),
    events: objList(o.events) as SessionPage['events'],
    runs: Array.isArray(o.runs) ? (o.runs as SessionPage['runs']) : undefined,
    evidence: Array.isArray(o.evidence) ? (o.evidence as SessionPage['evidence']) : undefined,
    nextCursor: str(o.nextCursor) ? o.nextCursor : undefined,
    limitMax: isInt(o.limitMax) ? o.limitMax : 100,
  };
}

export function parseChanges(out: unknown): ChangesResult {
  const tool = 'work_changes';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && typeof out.reset === 'boolean', tool, 'seq·reset');
  const o = out as Record<string, unknown>;
  return {
    seq: o.seq as string,
    reset: o.reset as boolean,
    more: o.more === true,
    waitMax: isInt(o.waitMax) && o.waitMax >= 0 ? o.waitMax : 0,
    workIds: strList(o.workIds),
    sessionIds: strList(o.sessionIds),
  };
}

// ---- AU-7 work_metrics(orbit.work 1.5) ----

const TREND_KEYS: readonly TrendKey[] = ['asked', 'overturned', 'corrections', 'repeats', 'replans', 'firstPass', 'cost', 'turns', 'minutes'];
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
const numOrNull = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null);
const keys = (v: unknown): TrendKey[] => (Array.isArray(v) ? v.filter((k): k is TrendKey => TREND_KEYS.includes(k as TrendKey)) : []);

function metricsDoc(v: unknown, tool: string): WorkMetricsDoc {
  need(isObj(v) && str(v.workId) && isObj(v.metrics), tool, 'workId·metrics');
  const o = v as Record<string, unknown>;
  const m = o.metrics as Record<string, unknown>;
  const fp = isObj(m.firstPass) ? m.firstPass : {};
  return {
    workId: o.workId as string,
    title: str(o.title) ? o.title : '',
    state: str(o.state) ? o.state : '',
    project: str(o.project) ? o.project : '',
    kind: str(o.kind) ? o.kind : 'other',
    size: str(o.size) ? o.size : '',
    endedAt: num(o.endedAt),
    metrics: {
      asked: num(m.asked), assumed: num(m.assumed), overturned: num(m.overturned), corrections: numOrNull(m.corrections),
      repeats: num(m.repeats), replans: num(m.replans), firstPass: {passed: num(fp.passed), steps: num(fp.steps)},
      costUsd: num(m.costUsd), turns: num(m.turns), minutes: num(m.minutes),
    },
  };
}

function trendSide(v: unknown): TrendSide {
  const o = isObj(v) ? v : {};
  return {
    works: num(o.works), asked: num(o.asked), assumed: num(o.assumed), overturned: num(o.overturned), corrections: numOrNull(o.corrections),
    repeats: num(o.repeats), replans: num(o.replans), firstPassRate: numOrNull(o.firstPassRate), costUsd: num(o.costUsd), turns: num(o.turns), minutes: num(o.minutes),
  };
}

export function parseMetrics(out: unknown): MetricsResult {
  const tool = 'work_metrics';
  throwIfError(out, tool);
  need(isObj(out) && str(out.seq) && isObj(out.work), tool, 'seq·work');
  const o = out as Record<string, unknown>;
  const similar = isObj(o.similar) ? o.similar : {};
  const trend = isObj(similar.trend) ? similar.trend : {};
  return {
    seq: o.seq as string,
    work: metricsDoc(o.work, tool),
    similar: {
      works: Array.isArray(similar.works) ? similar.works.map(w => metricsDoc(w, tool)) : [],
      trend: {earlier: trendSide(trend.earlier), later: trendSide(trend.later), better: keys(trend.better), worse: keys(trend.worse)},
    },
  };
}

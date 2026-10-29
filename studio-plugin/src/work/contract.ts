// 작업 계약(orbit.work) 응답 검사: 판(contract) 확인과 모양 확인.
// response.schema.json으로 거르지 않는다(계약 README 「판」). 필요한 칸만 확인하고 모르는 칸은 무시한다.
import type {
  ChangesResult, ContractInfo, GetResult, ListPage, SessionPage, SessionsPage,
} from './types.ts';

export const READ_MAJOR = 1;

/** 이 데이터만 멈추는 이유. 기억·제안은 영향 없다. */
export type PauseKind = 'oni-newer' | 'orbit-older';

export const PAUSE_MESSAGE: Record<PauseKind, string> = {
  'oni-newer': 'Orbit이 이 Orbit 앱보다 새 판입니다. Orbit 앱을 올려 주세요.',
  'orbit-older': 'Orbit을 올려 주세요.',
};

/** 판 때문에 읽지 못함 */
export class WorkPaused extends Error {
  kind: PauseKind;
  constructor(kind: PauseKind) {
    super(PAUSE_MESSAGE[kind]);
    this.name = 'WorkPaused';
    this.kind = kind;
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
    super(`oni의 ${tool} 응답 모양이 맞지 않습니다(${why}).`);
    this.name = 'WorkShapeError';
  }
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v);

/** 응답 맨 위 contract를 읽는다. 읽을 수 없는 판이면 WorkPaused. */
export function checkContract(out: unknown, tool: string): ContractInfo {
  const c = isObj(out) ? out.contract : undefined;
  if (!isObj(c) || !isInt(c.major)) throw new WorkShapeError(tool, 'contract 없음');
  const supported = Array.isArray(c.supportedMajors) ? c.supportedMajors.filter(isInt) : [];
  const info: ContractInfo = {
    name: typeof c.name === 'string' ? c.name : 'orbit.work',
    major: c.major,
    minor: isInt(c.minor) ? c.minor : 0,
    supportedMajors: supported,
  };
  const err = isObj(out) && isObj((out as Record<string, unknown>).error) ? ((out as Record<string, unknown>).error as Record<string, unknown>) : undefined;
  if (err?.code === 'UNSUPPORTED_MAJOR') {
    // oni가 우리 판(1)을 받지 못한다 = oni가 더 새 판. 지원 목록이 1을 품으면 이상한 응답이므로 모양 오류로 둔다.
    const list = Array.isArray(err.supportedMajors) ? err.supportedMajors.filter(isInt) : supported;
    if (list.includes(READ_MAJOR)) throw new WorkShapeError(tool, 'UNSUPPORTED_MAJOR인데 supportedMajors에 1이 있음');
    throw new WorkPaused(info.major < READ_MAJOR ? 'orbit-older' : 'oni-newer');
  }
  if (info.major > READ_MAJOR) {
    if (supported.includes(READ_MAJOR)) return info; // 1도 읽어 줄 수 있다고 밝힘(우리는 major:1을 요청한다)
    throw new WorkPaused('oni-newer');
  }
  if (info.major < READ_MAJOR) throw new WorkPaused('orbit-older');
  return info;
}

/** 오류 응답이면 던진다. 아니면 그대로 */
function throwIfError(out: unknown, tool: string): void {
  if (!isObj(out) || !isObj(out.error)) return;
  const e = out.error;
  throw new WorkToolError(
    typeof e.code === 'string' ? e.code : 'UNKNOWN',
    typeof e.message === 'string' ? e.message : `${tool} 호출이 실패했습니다.`,
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
  need(isObj(w) && str(w.id) && str(w.title) && str(w.state) && str(w.updatedAt) && str(w.openedAt) && isObj(w.counts), tool, '작업 칸');
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

function normalizeWork(w: unknown, tool: string): GetResult['work'] {
  checkWorkHead(w, tool);
  const o = w as Record<string, unknown>;
  need(Array.isArray(o.steps), tool, 'steps');
  for (const s of o.steps as unknown[]) {
    need(isObj(s) && str(s.id) && str(s.title) && str(s.status) && isObj(s.verification), tool, '단계 칸');
  }
  const children = Array.isArray(o.children) ? o.children.map(c => normalizeWork(c, tool)) : undefined;
  return {
    ...(o as unknown as GetResult['work']),
    steps: (o.steps as GetResult['work']['steps']).map(s => ({
      ...s,
      after: objList(s.after) as GetResult['work']['steps'][number]['after'],
      evidenceIds: strList(s.evidenceIds),
      childWorkIds: strList(s.childWorkIds),
    })),
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
  need(isObj(s) && str(s.sessionId) && str(s.state) && str(s.startedAt) && str(s.lastSeenAt), tool, '세션 칸');
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

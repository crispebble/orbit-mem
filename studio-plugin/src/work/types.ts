// 작업 그래프·세션 보기의 데이터 형(orbit.work 1판 읽는 쪽). 좁게 선언하고 경계(contract.ts)에서 한 번 거른다.
// 모르는 칸은 무시하고, 모르는 열거값은 string으로 받아 표시 모델이 「알 수 없음」으로 바꾼다(계약 README 「판」).

export interface ContractInfo {
  name: string;
  major: number;
  minor: number;
  supportedMajors: number[];
}

export interface ProjectRef {scope: string; label: string}

export interface StepCounts {
  pending: number; ready: number; in_progress: number; blocked: number; done: number; cancelled: number;
}

export interface VerificationCounts {none: number; pass: number; fail: number; needs_user: number; outdated: number}

export interface WorkHead {
  id: string;
  title: string;
  state: string; // open | done | (모르는 값)
  cwd?: string;
  project?: ProjectRef;
  parent?: {workId: string; stepId?: string};
  openedAt: string;
  updatedAt: string;
  closedAt?: string;
  counts: StepCounts;
  origin?: {host: string; surface: string; sessionId: string};
}

export interface WorkSummary extends WorkHead {
  stepCount: number;
  verification: VerificationCounts;
}

export interface StepBlock {reason: string; detail?: string; since: string}

export interface Step {
  id: string;
  title: string;
  activeTitle?: string;
  order: number;
  wave: number;
  after: {stepId: string; source: string}[];
  status: string; // 모르는 값이면 pending처럼 배치
  block?: StepBlock;
  assignee?: {kind: string; role?: string; host?: string; model?: string};
  startedAt?: string;
  finishedAt?: string;
  verification: {state: string; outdated: boolean; evidenceId?: string};
  evidenceIds: string[];
  childWorkIds: string[];
}

export interface Run {
  id: string;
  kind: string;
  host: string;
  role?: string;
  agentId?: string;
  sessionId?: string;
  parentSessionId?: string;
  stepId?: string;
  link: string;
  background?: boolean;
  cwd?: string;
  state: string;
  endReason?: string;
  startedAt: string;
  lastSeenAt?: string;
  finishedAt?: string;
}

export interface Evidence {
  id: string;
  kind: string;
  stepId?: string;
  runId?: string;
  at: string;
  result: string;
  summary?: string;
}

export interface Work extends WorkHead {
  steps: Step[];
  runs: Run[];
  evidence: Evidence[];
  children?: Work[];
}

export interface Session {
  sessionId: string;
  host: string;
  surface: string;
  cwd?: string;
  repo?: string;
  branch?: string;
  project?: ProjectRef;
  parentSessionId?: string;
  model?: string;
  state: string;
  endReason?: string;
  endDetail?: string;
  startedAt: string;
  lastSeenAt: string;
  endedAt?: string;
  openWorkIds: string[];
  activeStepIds: string[];
}

export interface SessionEvent {
  seq: string;
  at: string;
  kind: string;
  workId?: string;
  stepId?: string;
  runId?: string;
  summary?: string;
}

export interface ListPage {seq: string; items: WorkSummary[]; nextCursor?: string; limitMax: number}
export interface GetResult {seq: string; work: Work}
export interface SessionsPage {seq: string; sessions: Session[]; nextCursor?: string; limitMax: number}
export interface SessionPage {
  seq: string;
  session: Session;
  events: SessionEvent[];
  runs?: Run[];
  evidence?: Evidence[];
  nextCursor?: string;
  limitMax: number;
}
export interface ChangesResult {
  seq: string;
  reset: boolean;
  more: boolean;
  waitMax: number;
  workIds: string[];
  sessionIds: string[];
}

// ---- Studio 플러그인 API 0.1.1에 더해진 것 중 이 폴더가 쓰는 좁은 모양 ----
// 0.3.0 배포 후 지움: devDependency를 올리면 `@onidot-labs/studio-plugin`의 PluginContext를 그대로 쓴다.
export type StudioScope = {kind: 'all'} | {kind: 'project'; id: string; name: string; directories: string[]};

export interface McpResultLike<T = unknown> {
  isError: boolean;
  text: string;
  structured?: T;
}

export interface WorkCtx {
  mcp: {
    call<T = unknown>(
      connectionId: string,
      tool: string,
      args?: Record<string, unknown>,
      options?: {timeoutMs?: number; signal?: AbortSignal},
    ): Promise<McpResultLike<T>>;
  };
  scope: {current(): StudioScope; onChange(handler: (scope: StudioScope) => void): () => void};
  storage: {
    get<T = unknown>(key: string): Promise<T | undefined>;
    set(key: string, value: unknown): Promise<void>;
  };
  log: {info(message: string): void; warn(message: string): void; error(message: string): void};
}

/** InboxItem(0.1.1)의 이 폴더가 쓰는 부분. kind 'waiting'은 0.1.1부터. */
export interface WorkInboxItem {
  id: string;
  kind: 'waiting';
  title: string;
  subtitle?: string;
  group?: string;
  unread?: boolean;
  createdAt?: number;
  path?: string;
}

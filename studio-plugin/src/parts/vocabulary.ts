// Orbit 기억·제안 어휘. Forma(forma-ai 0.9.x)의 management-model.ts에서 옮겨 왔다.
// Forma 0.10.0에서 이 어휘가 빠지므로 플러그인이 갖는다.

export type MemoryKind = 'rule' | 'fact' | 'decision' | 'lesson' | 'journal';
export type MemoryState = 'candidate' | 'confirmed' | 'stale' | 'hidden';
export type MemorySourceKind = 'quote' | 'file' | 'command' | 'session' | 'host_memory' | 'legacy';

export interface MemorySource {
  kind: MemorySourceKind;
  label: string;
  text?: string;
  path?: string;
  at?: string;
  /** 출처 파일이 기억을 쓴 뒤에 바뀌었다. */
  changed?: boolean;
}

export interface MemoryVersion {
  version: number;
  label: string;
  by?: string;
  at: string;
  before?: string;
  after?: string;
  tone?: 'ok';
}

/** Orbit 제안 종류. cleanup·harness는 0.8.0까지의 묶음 이름이며 그대로 받는다. */
export type ProposalKind = 'confirm' | 'stale' | 'merge' | 'reclassify' | 'apply_file' | 'direction' | 'link' | 'cleanup' | 'harness';

/**
 * 부품이 그리는 기억 한 건. forma-ai의 MemoryRecord에서 Orbit 칸을 그대로 가져온 플러그인 자기 형식이다.
 * kind·state 같은 Orbit 칸은 모두 선택이며, 없으면 kind는 fact, state는 confirmed로 본다.
 */
export interface OrbitMemory {
  id: string;
  title: string;
  content: string;
  scope?: string;
  updatedAt?: string;
  source?: string;
  kind?: MemoryKind;
  state?: MemoryState;
  tags?: string[];
  sources?: MemorySource[];
  history?: MemoryVersion[];
  supersedes?: string[];
  supersededBy?: string;
  version?: number;
  lastUsed?: string;
}

export const memoryKindLabels: Record<MemoryKind, string> = {
  rule: '규칙',
  fact: '사실',
  decision: '결정',
  lesson: '교훈',
  journal: '일지',
};

export const memoryStateLabels: Record<MemoryState, string> = {
  candidate: '후보',
  confirmed: '확정',
  stale: '낡음',
  hidden: '숨김',
};

export const memorySourceLabels: Record<MemorySourceKind, string> = {
  quote: '내 말',
  file: '파일',
  command: '명령',
  session: '대화 기록',
  host_memory: '호스트 기억',
  legacy: '옛 Knowledge',
};

export const proposalKindLabels: Record<ProposalKind, string> = {
  confirm: '후보 확정',
  stale: '낡은 기억 정리',
  merge: '기억 합치기',
  reclassify: '종류 재분류',
  apply_file: '파일 반영',
  direction: '방향 제안',
  link: '관련 기억 잇기',
  cleanup: '기억 정리·합치기',
  harness: '하네스 반영',
};

/** 승인 단추 옆에 보이는 안내: 승인하면 무엇이 바뀌는지 */
export const proposalApproveHints: Record<ProposalKind, string> = {
  confirm: '승인하면 이 후보를 확정 기억으로 바꿉니다.',
  stale: '승인하면 대상 기억을 낡음으로 표시합니다. 이력은 남습니다.',
  merge: '승인하면 합친 기억을 새로 만들고 원래 기억은 낡음이 됩니다.',
  reclassify: '승인하면 기억의 종류를 바꿉니다.',
  apply_file: '승인하면 파일 변경만 만들어 둡니다. 커밋은 직접 합니다.',
  direction: '승인하면 결정 기억으로 남깁니다.',
  link: '승인하면 두 기억을 잇습니다. 기억 내용은 바뀌지 않습니다.',
  cleanup: '승인하면 결정을 기록합니다.',
  harness: '승인하면 파일 변경만 만들어 둡니다. 커밋은 직접 합니다.',
};

export const KINDS: readonly MemoryKind[] = ['rule', 'fact', 'decision', 'lesson', 'journal'];
export const GLOBAL_SCOPE = '전역';

export const kindOf = (memory: OrbitMemory): MemoryKind => memory.kind ?? 'fact';
export const stateOf = (memory: OrbitMemory): MemoryState => memory.state ?? 'confirmed';
export const scopeOf = (memory: OrbitMemory): string => memory.scope || GLOBAL_SCOPE;

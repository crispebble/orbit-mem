// orbit-mem 기억·제안 어휘. Forma(forma-ai 0.9.x)의 management-model.ts에서 옮겨 왔다.
// Forma 0.10.0에서 이 어휘가 빠지므로 플러그인이 갖는다.
import {pick} from '../i18n/index.ts';
import {memoryText} from '../i18n/messages/memory.ts';
import {proposalText} from '../i18n/messages/proposals.ts';

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

/** orbit-mem 제안 종류. cleanup·harness는 0.8.0까지의 묶음 이름이며 그대로 받는다. */
export type ProposalKind = 'confirm' | 'stale' | 'merge' | 'reclassify' | 'apply_file' | 'direction' | 'link' | 'cleanup' | 'harness';

/**
 * 부품이 그리는 기억 한 건. forma-ai의 MemoryRecord에서 orbit-mem 칸을 그대로 가져온 플러그인 자기 형식이다.
 * kind·state 같은 orbit-mem 칸은 모두 선택이며, 없으면 kind는 fact, state는 confirmed로 본다.
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

// 이름과 안내는 지금 언어로 읽는다(그릴 때·부를 때). 화면은 useLocale·useMessages로 언어 바뀜을 따라간다.
export const memoryKindLabels = (): Record<MemoryKind, string> => pick(memoryText).kinds;
export const memoryStateLabels = (): Record<MemoryState, string> => pick(memoryText).states;
export const memorySourceLabels = (): Record<MemorySourceKind, string> => pick(memoryText).sources;
export const proposalKindLabels = (): Record<ProposalKind, string> => pick(proposalText).kinds;

/** 승인 단추 옆에 보이는 안내: 승인하면 무엇이 바뀌는지 */
export const proposalApproveHints = (): Record<ProposalKind, string> => pick(proposalText).approveHints;

export const KINDS: readonly MemoryKind[] = ['rule', 'fact', 'decision', 'lesson', 'journal'];

/** 전역 범위의 이름(지금 언어). 기억의 scope 칸에는 이 이름이 그대로 들어간다. */
export const globalScope = (): string => pick(memoryText).scope.global;
/** 어느 언어의 전역 이름이든 전역으로 본다(언어가 바뀌어도 고치던 값이 어긋나지 않게) */
export const isGlobalScope = (scope: string): boolean => scope === memoryText.en.scope.global || scope === memoryText.ko.scope.global;

export const kindOf = (memory: OrbitMemory): MemoryKind => memory.kind ?? 'fact';
export const stateOf = (memory: OrbitMemory): MemoryState => memory.state ?? 'confirmed';
export const scopeOf = (memory: OrbitMemory): string => memory.scope || globalScope();

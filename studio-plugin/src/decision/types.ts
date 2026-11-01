// orbit.decision 1.0 카드(계약 정본: orbit-mem internal/decision/contract/v1/README.md)와 카드 출처 경계.

export interface DecisionOption {
  id: string;
  label: string;
  detail?: string;
}

export interface DecisionQuestion {
  text: string;
  header?: string;
  multiSelect: boolean;
  options: DecisionOption[];
  recommended?: string;
  reason?: string;
}

/** 질문 하나의 답: 고른 선택지 id 또는 직접 쓴 글 */
export interface AnswerPart {
  optionIds?: string[];
  text?: string;
}

export interface Decision {
  id: string;
  /** open·answered·withdrawn·expired. 모르는 값도 그대로 둔다 */
  state: string;
  /** question·permission·ops·plugin·assumption */
  source: string;
  /** orbit-pi·human·studio */
  owner: string;
  createdAt: string;
  updatedAt?: string;
  questions: DecisionQuestion[];
  context?: string;
  links: {kind: string; label?: string; ref?: string}[];
  work?: {host?: string; sessionId?: string; workId?: string; stepId?: string; waitKey?: string};
  expiresAt?: string;
  closedAt?: string;
  closeReason?: string;
  answer?: {answers: AnswerPart[]; by: string; at: string};
}

export interface DecisionPage {
  items: Decision[];
  seq: string;
  truncated: boolean;
}

export interface DecisionChanges {
  seq: string;
  decisionIds: string[];
  reset: boolean;
  more: boolean;
  waitMax: number;
}

/** 답한 결과. CONFLICT면 `decision`이 지금의 카드(먼저 답한 곳)다. */
export type AnswerOutcome =
  | {ok: true; decision?: Decision}
  | {ok: false; code: 'CONFLICT' | 'CANNOT_RELAY' | 'NOT_FOUND' | 'FAILED'; message?: string; decision?: Decision};

/**
 * 카드 출처. 저장소·Studio 공급자는 이것만 안다.
 * 지금 구현은 `orb work mcp`(source.ts)이고, R4-4 orbit-pi 데몬이 생기면 데몬 이벤트 구독으로 같은 모양을 하나 더 만들어 바꿔 끼운다.
 */
export interface DecisionSource {
  /** 카드 목록. `updatedSince`(RFC 3339) 뒤에 바뀐 것만 */
  list(query: {state: string[]; updatedSince?: string}, signal?: AbortSignal): Promise<DecisionPage>;
  /** `after` 뒤에 바뀐 카드 id. `wait` 밀리초까지 기다린다(장기 대기) */
  changes(query: {after?: string; wait: number}, signal: AbortSignal): Promise<DecisionChanges>;
  /** Studio에서 답한다(`by=studio`) */
  answer(id: string, answers: AnswerPart[], signal?: AbortSignal): Promise<AnswerOutcome>;
}

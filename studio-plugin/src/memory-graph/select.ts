// 「그래프에서 보기」 요청을 어떻게 처리할지 정한다. 순수 함수다(graph-tab이 결과대로 상태를 바꾼다).

export type RequestOutcome =
  /** 고른다. clearKind면 종류 거르기에 가려져 있으니 「모두」로 되돌린다 */
  | {kind: 'select'; clearKind: boolean}
  /** 받은 그래프에 없다. 그 사이 새로 생긴 기억일 수 있으니 한 번 다시 읽는다 */
  | {kind: 'reload'}
  /** 그래프를 읽는 중이다. 읽고 나서 다시 정한다 */
  | {kind: 'wait'}
  /** 다시 읽어도 없다(숨김·잘림) */
  | {kind: 'missing'};

export interface RequestContext {
  /** 받은 그래프가 있다 */
  loaded: boolean;
  /** 다시 읽는 중이다 */
  reloading: boolean;
  /** 받은 그래프의 기억 */
  graphIds: ReadonlySet<string>;
  /** 종류 거르기 뒤 보이는 기억 */
  visibleIds: ReadonlySet<string>;
  /** 이 요청 때문에 이미 다시 읽었다 */
  reloaded: boolean;
}

export function resolveRequest(id: string, c: RequestContext): RequestOutcome {
  if (!c.loaded || c.reloading) return {kind: 'wait'};
  if (c.visibleIds.has(id)) return {kind: 'select', clearKind: false};
  if (c.graphIds.has(id)) return {kind: 'select', clearKind: true};
  return c.reloaded ? {kind: 'missing'} : {kind: 'reload'};
}

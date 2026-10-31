// openChat이 도구 창 컴포넌트에 대상을 건네는 모듈 수준 store. 창이 아직 안 떠 있어도 대상은 남아 있다가 창이 가져간다.
export type ChatTarget =
  | {kind: 'run'; runId: string}
  | {kind: 'work'; workId: string; cwd?: string}
  | {kind: 'compose'; text: string; cwd?: string};

let pending: ChatTarget | undefined;
const waiting = new Set<() => void>();

export function requestChat(target: ChatTarget): void {
  pending = target;
  waiting.forEach(w => w());
}

export function pendingChat(): ChatTarget | undefined {
  return pending;
}

/** 창이 대상을 가져가면 비운다(창을 다시 그려도 같은 대상을 또 적용하지 않게). */
export function takeChat(target: ChatTarget): void {
  if (pending === target) pending = undefined;
}

export function subscribeChat(listener: () => void): () => void {
  waiting.add(listener);
  return () => {
    waiting.delete(listener);
  };
}

// 받은 일 갈래 합치기. ctx.inbox.set은 플러그인의 받은 일 전체를 바꾸므로, 제안(store.ts)과
// 작업의 확인 기다림(work/inbox.ts)이 따로 set하면 서로 지운다. 갈래마다 항목을 들고 있다가 합쳐서 한 번에 set한다.

export function createInboxParts<T>(inbox: {set(items: T[]): void}) {
  const parts = new Map<string, T[]>();
  let last = '';
  const publish = () => {
    const all = [...parts.values()].flat();
    const key = JSON.stringify(all);
    if (key === last) return;
    last = key;
    inbox.set(all);
  };
  return {
    set(part: string, items: T[]) { parts.set(part, items); publish(); },
    clear(part: string) { parts.delete(part); publish(); },
  };
}

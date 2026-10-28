// 받은 일: `blocked` + `waiting_user` 단계를 kind 'waiting' 항목으로. 확인·거절 동작은 없다(계약 1.0은 읽기 전용).
// 주의: ctx.inbox.set은 플러그인의 받은 일 전체를 바꾼다. 제안 항목(store.ts)과 합쳐서 한 번에 set해야 하므로
// 여기서는 항목만 만들고 set은 부르지 않는다.
import {blockText, waitingSteps} from './display.ts';
import type {Work, WorkInboxItem} from './types.ts';

export function waitingInboxItems(works: readonly Work[]): WorkInboxItem[] {
  const items: WorkInboxItem[] = [];
  for (const w of works) {
    if (w.state !== 'open') continue;
    for (const s of waitingSteps(w)) {
      const since = s.block ? Date.parse(s.block.since) : NaN;
      items.push({
        id: `work-waiting:${w.id}:${s.id}`,
        kind: 'waiting',
        title: s.title,
        subtitle: s.block ? blockText(s.block) : undefined,
        group: w.title,
        unread: true,
        createdAt: Number.isNaN(since) ? undefined : since,
        path: w.cwd,
      });
    }
  }
  return items;
}

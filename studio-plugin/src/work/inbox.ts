// 받은 일: `blocked` + `waiting_user` 단계를 kind 'waiting' 항목으로. 확인·거절 동작은 없다(계약 1.0은 읽기 전용).
// ctx.inbox.set은 플러그인의 받은 일 전체를 바꾸므로 제안(store.ts)과 같은 갈래 합치기(inbox-parts.ts)에 'waiting' 갈래로 넣는다.
import {onLocale} from '../i18n/index.ts';
import {blockText, waitingSteps} from './display.ts';
import type {Work, WorkInboxItem} from './types.ts';

const PREFIX = 'work-waiting:';

export function waitingInboxItems(works: readonly Work[]): WorkInboxItem[] {
  const items: WorkInboxItem[] = [];
  for (const w of works) {
    if (w.state !== 'open') continue;
    for (const s of waitingSteps(w)) {
      const since = s.block ? Date.parse(s.block.since) : NaN;
      items.push({
        // 계약 id는 불투명 문자열이라 ':'가 들어 있을 수 있어 나눠 읽을 수 있게 인코딩한다
        id: `${PREFIX}${encodeURIComponent(w.id)}:${encodeURIComponent(s.id)}`,
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

/** 받은 일 항목 id → 작업·단계 id. 확인 기다림 항목이 아니면 undefined */
export function parseWaitingId(id: string): {workId: string; stepId: string} | undefined {
  if (!id.startsWith(PREFIX)) return undefined;
  const [workId, stepId, ...rest] = id.slice(PREFIX.length).split(':');
  if (!workId || !stepId || rest.length > 0) return undefined;
  try {
    return {workId: decodeURIComponent(workId), stepId: decodeURIComponent(stepId)};
  } catch {
    return undefined;
  }
}

interface DetailSource {
  getState(): {details: Readonly<Record<string, Work>>};
  subscribe(listener: () => void): () => void;
}

/** 작업 store가 바뀔 때마다 확인 기다림 항목을 받은 일 'waiting' 갈래로 넣는다. 돌려준 함수로 끊으면 갈래를 지운다. */
export function syncWaitingInbox(store: DetailSource, parts: {set(part: string, items: WorkInboxItem[]): void; clear(part: string): void}): () => void {
  let last: Readonly<Record<string, Work>> | undefined;
  const publish = () => {
    const {details} = store.getState();
    if (details === last) return;
    last = details;
    parts.set('waiting', waitingInboxItems(Object.values(details)));
  };
  const off = store.subscribe(publish);
  // 언어가 바뀌면 부제(막힘 사유)를 새 언어로 다시 올린다
  const offLocale = onLocale(() => {
    last = undefined;
    publish();
  });
  publish();
  return () => {
    off();
    offLocale();
    parts.clear('waiting');
  };
}

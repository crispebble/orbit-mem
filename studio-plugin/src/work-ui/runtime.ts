// activate에서 만든 작업 store를 화면이 쓰도록 두는 자리(runtime.ts와 같은 방식). 켜기 전·끈 뒤에는 store가 없다.
import {useEffect, useState, useSyncExternalStore} from 'react';
import type {WorkState, WorkStore} from '../work/store.ts';

let current: WorkStore | undefined;
const waiting = new Set<() => void>();

export function setWorkStore(store: WorkStore | undefined): void {
  current = store;
  waiting.forEach(w => w());
}

const idle: WorkState = {status: 'idle', scope: {kind: 'all'}, works: [], details: {}, sessions: [], pendingReview: []};

export function useWork(): {store: WorkStore | undefined; state: WorkState} {
  const store = useSyncExternalStore(
    cb => { waiting.add(cb); return () => { waiting.delete(cb); }; },
    () => current,
    () => current,
  );
  const state = useSyncExternalStore(
    cb => store?.subscribe(cb) ?? (() => {}),
    () => store?.getState() ?? idle,
    () => store?.getState() ?? idle,
  );
  return {store, state};
}

/**
 * 경과 글을 맞게 두려고 「지금」을 주기적으로 바꾼다. 화면이 보일 때만 돈다(그린 화면이 없으면 타이머도 없다).
 * 30초: 분 단위 글이 한 박자 이상 늦지 않게 하는 구현 값이다.
 */
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

/** 작업 안 단계 id → 제목. 세션 줄·사건 기록에 쓴다(읽은 작업만 안다). */
export function stepTitleMap(state: WorkState): Map<string, string> {
  const map = new Map<string, string>();
  for (const w of Object.values(state.details)) for (const s of w.steps) map.set(s.id, s.title);
  return map;
}

const asked = new Set<string>();

/**
 * 세션 줄·세션 탭이 단계 제목을 쓰도록, 세션이 연 작업 중 아직 읽지 않은 것을 읽어 둔다(store.details에 들어간다).
 * 한 번 물은 작업은 다시 묻지 않는다(실시간 갱신은 store가 한다). 실패하면 제목 대신 개수를 보인다.
 */
export function useLoadWorks(store: WorkStore | undefined, ids: readonly string[], details: Readonly<Record<string, unknown>>): void {
  const missing = ids.filter(id => !details[id] && !asked.has(id));
  const key = missing.join(',');
  useEffect(() => {
    if (!store || !key) return;
    for (const id of key.split(',')) {
      asked.add(id);
      store.loadDetail(id).catch(() => {});
    }
  }, [store, key]);
}

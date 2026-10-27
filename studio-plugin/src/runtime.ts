// activate에서 만든 store를 화면이 쓰도록 두는 자리. 화면은 store가 없으면(켜기 전·끈 뒤) 빈 상태를 보인다.
import {useSyncExternalStore} from 'react';
import type {OrbitState, OrbitStore} from './store.ts';

let current: OrbitStore | undefined;
const waiting = new Set<() => void>();

export function setStore(store: OrbitStore | undefined): void {
  current = store;
  waiting.forEach(w => w());
}

export function getStore(): OrbitStore | undefined {
  return current;
}

const idle: OrbitState = {status: 'idle', proposals: []};

/** store 상태를 읽는다. store가 바뀌거나 상태가 바뀌면 다시 그린다. */
export function useOrbit(): {store: OrbitStore | undefined; state: OrbitState} {
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

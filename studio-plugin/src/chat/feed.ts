// 실행 하나의 이벤트를 모으는 곳: history로 과거를 채우고 onEvent로 실시간을 붙인다.
// 합치는 규칙은 timeline.mergeEvents(seq 정렬·같은 seq 한 번). 실시간이 history보다 먼저 와도 겹치거나 빠지지 않는다.
// 스트림이 끊겼다 다시 이어지면 마지막 seq 뒤를 history로 다시 받아 채운다.
import type {DaemonEvent, RunSummary} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {lastSeq, mergeEvents} from './timeline.ts';

export type FeedClient = Pick<DaemonClient, 'api' | 'onEvent' | 'onStatus' | 'status'>;

export interface FeedState {
  events: DaemonEvent[];
  summary?: RunSummary;
  loading: boolean;
  /** 불러오기 실패(오류 객체 그대로: 화면이 글로 바꾼다) */
  error?: unknown;
}

export interface RunFeed {
  getState(): FeedState;
  subscribe(listener: () => void): () => void;
  /** 다시 불러온다(실패 뒤 「다시 시도」). */
  reload(): Promise<void>;
  dispose(): void;
}

export function createRunFeed(client: FeedClient, runId: string): RunFeed {
  let state: FeedState = {events: [], loading: true};
  let disposed = false;
  const listeners = new Set<() => void>();
  const set = (next: Partial<FeedState>) => {
    if (disposed) return;
    state = {...state, ...next};
    listeners.forEach(l => l());
  };
  const take = (events: readonly DaemonEvent[]) => {
    const merged = mergeEvents(state.events, events, runId);
    if (merged !== state.events) set({events: merged});
  };
  const loadSummary = async () => {
    try {
      set({summary: (await client.api.getRun({runId})).run});
    } catch {
      // 요약은 머리의 보조 정보다: 못 받아도 이벤트로 그린다
    }
  };

  const offEvent = client.onEvent(event => {
    if (event.runId !== runId) return;
    take([event]);
    // 끝·결정·시작은 요약(상태·월 사용량)이 바뀐다
    const type = event.event?.type;
    if (type === 'run.finished' || type === 'run.started' || type === 'decision.answered') void loadSummary();
  });
  let wasStreaming = client.status().streaming;
  const offStatus = client.onStatus(status => {
    const back = status.streaming && !wasStreaming;
    wasStreaming = status.streaming;
    if (back && !state.loading) void catchUp();
  });

  async function catchUp() {
    try {
      take((await client.api.history({runId, afterSeq: lastSeq(state.events)})).events);
      void loadSummary();
    } catch {
      // 다음 이벤트·재연결 때 다시 채운다
    }
  }

  async function load() {
    set({loading: true, error: undefined});
    try {
      take((await client.api.history({runId, afterSeq: 0})).events);
      set({loading: false});
      await loadSummary();
    } catch (error) {
      set({loading: false, error});
    }
  }
  void load();

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    reload: load,
    dispose() {
      disposed = true;
      offEvent();
      offStatus();
      listeners.clear();
    },
  };
}

// API 키 넣기·지우기 흐름(P3). 키 값은 어디에도 남기지 않는다: 상태·로그·localStorage·ctx.storage 모두 아니다.
// 값은 입력 칸에서 한 번 읽어 setApiKey 호출에만 넘기고, 칸은 보내기 전에 비운다. 여기엔 공급처별 진행 상태만 있다.
import type {DaemonApi} from '../daemon/api.ts';

export type KeyStatus = 'idle' | 'saving' | 'saved' | 'deleting' | 'deleted' | 'failed';

export interface KeyEntryState {
  /** 공급처 → 상태 */
  status: Record<string, KeyStatus>;
  /** 마지막 실패(오류 객체: 화면이 글로 바꾼다. 키 값은 들어 있지 않다) */
  error?: {provider: string; error: unknown};
}

export interface KeyEntry {
  getState(): KeyEntryState;
  subscribe(listener: () => void): () => void;
  /** 값은 이 호출 안에서만 쓰고 남기지 않는다. 데몬이 받았으면 새 revision. */
  submit(profile: string, provider: string, value: string): Promise<string | undefined>;
  remove(profile: string, provider: string): Promise<string | undefined>;
}

export function createKeyEntry(api: Pick<DaemonApi, 'setApiKey' | 'deleteApiKey'>): KeyEntry {
  let state: KeyEntryState = {status: {}};
  const listeners = new Set<() => void>();
  const set = (provider: string, status: KeyStatus, error?: unknown) => {
    state = {status: {...state.status, [provider]: status}, error: status === 'failed' ? {provider, error} : undefined};
    listeners.forEach(l => l());
  };
  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    async submit(profile, provider, value) {
      if (value === '') return undefined;
      set(provider, 'saving');
      try {
        const r = await api.setApiKey({profile, provider, value});
        set(provider, 'saved');
        return r.revision;
      } catch (error) {
        set(provider, 'failed', error);
        return undefined;
      }
    },
    async remove(profile, provider) {
      set(provider, 'deleting');
      try {
        const r = await api.deleteApiKey({profile, provider});
        set(provider, 'deleted');
        return r.revision;
      } catch (error) {
        set(provider, 'failed', error);
        return undefined;
      }
    },
  };
}

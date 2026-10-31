// 프로필 화면(사이드바 보기·편집 탭)이 함께 쓰는 데이터: 프로필 파일(+키 상태)과 이번 달 사용량.
// 저장·키 변경 뒤 notifyProfilesChanged()로 열린 화면이 다시 읽게 한다. 데몬이 꺼져 있으면 읽지 않는다(여기서 띄우지 않는다).
import {useCallback, useEffect, useState} from 'react';
import type {ProfilesView, UsageReport} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {normalizeView} from './edit-model.ts';

const waiting = new Set<() => void>();

export function notifyProfilesChanged(): void {
  waiting.forEach(w => w());
}

export interface ProfilesData {
  view?: ProfilesView;
  usage?: UsageReport;
  loading: boolean;
  error?: unknown;
}

export function useProfilesData(client: DaemonClient | undefined, up: boolean): {data: ProfilesData; reload: () => Promise<void>} {
  const [data, setData] = useState<ProfilesData>({loading: true});
  const reload = useCallback(async () => {
    if (!client || !up) {
      setData({loading: false});
      return;
    }
    setData(d => ({...d, loading: true, error: undefined}));
    try {
      const [view, usage] = await Promise.all([client.api.getProfiles(), client.api.getUsage({})]);
      setData({view: normalizeView(view), usage, loading: false});
    } catch (error) {
      setData(d => ({...d, loading: false, error}));
    }
  }, [client, up]);
  useEffect(() => {
    void reload();
    waiting.add(reload as () => void);
    return () => {
      waiting.delete(reload as () => void);
    };
  }, [reload]);
  return {data, reload};
}

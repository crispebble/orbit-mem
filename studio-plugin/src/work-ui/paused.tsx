// 판이 맞지 않아 작업·세션 화면만 멈췄을 때의 안내. Orbit이나 Orbit 앱을 올린 뒤 「다시 확인」으로 이어 읽는다.
import {Button, EmptyState} from '@onidot-labs/studio-plugin/ui';
import type {WorkState, WorkStore} from '../work/store.ts';

export function Paused({store, state, title}: {store: WorkStore; state: WorkState; title: string}) {
  return (
    <EmptyState title={title} description={state.pause?.message}>
      <Button variant="secondary" size="sm" onClick={() => store.recheck()}>다시 확인</Button>
    </EmptyState>
  );
}

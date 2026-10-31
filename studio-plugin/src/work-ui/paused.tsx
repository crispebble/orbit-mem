// 판이 맞지 않아 작업·세션 화면만 멈췄을 때의 안내. OpenOrbit이나 OpenOrbit 앱을 올린 뒤 「다시 확인」으로 이어 읽는다.
import {Button, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {workUiText} from '../i18n/messages/work.ts';
import {pauseMessage} from '../work/contract.ts';
import type {WorkState, WorkStore} from '../work/store.ts';

export function Paused({store, state, title, compact}: {store: WorkStore; state: WorkState; title: string; compact?: boolean}) {
  const t = useMessages(workUiText);
  // 언어가 바뀌어도 맞게 보이도록 저장된 글이 아니라 사유로 다시 만든다
  const description = state.pause ? pauseMessage(state.pause.kind) : undefined;
  return (
    <StateView title={title} description={description} compact={compact} headingLevel={compact ? 3 : undefined}
      action={<Button variant="secondary" size="sm" onClick={() => store.recheck()}>{t.paused.recheck}</Button>} />
  );
}

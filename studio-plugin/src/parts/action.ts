import {pick} from '../i18n/index.ts';
import {commonText} from '../i18n/messages/common.ts';

// 부품의 비동기 동작이 겹쳐 실행되지 않게 막는 문. forma-ai의 action.ts에서 옮겨 왔다.

export type ActionResult = {ok: true} | {ok: false; reason: 'pending'} | {ok: false; reason: 'error'; error: string};

export function createActionGate() {
  let pending = false;
  return {
    get pending() {
      return pending;
    },
    async run(action: () => void | Promise<void>): Promise<ActionResult> {
      if (pending) return {ok: false, reason: 'pending'};
      pending = true;
      try {
        await action();
        return {ok: true};
      } catch (error) {
        return {ok: false, reason: 'error', error: error instanceof Error ? error.message : pick(commonText).requestFailed};
      } finally {
        pending = false;
      }
    },
  };
}

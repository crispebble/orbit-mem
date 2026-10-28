// 실시간 갱신: work_changes 장기 대기를 계속 건다. 이 플러그인에서 작업 쪽 타이머는 이 파일의 sleep 하나뿐이다.
import {errorMessage} from '../oni.ts';
import {WorkPaused, type PauseKind} from './contract.ts';
import {workChanges} from './client.ts';
import type {WorkCtx} from './types.ts';

/** 호출 실패(oni가 죽음) 뒤 다시 걸기 전에 쉬는 시간 */
export const RETRY_MS = 3_000;
/** waitMax가 0이라 서버가 기다려 주지 않을 때의 확인 간격(바쁜 반복 방지) */
export const NO_WAIT_POLL_MS = 5_000;
/**
 * 호출 시간 한도 = wait + 이 값. 응답이 wait 끝까지 기다린 뒤 돌아오는 시간(프로세스 띄우기·DB 읽기)을 넉넉히 둔다.
 * 근거 없는 구현 값: 계약은 숫자를 주지 않는다. Studio 기본 한도(60초)가 waitMax(30초)의 두 배이므로 그 사이 10초를 둔다.
 */
export const TIMEOUT_GRACE_MS = 10_000;

export interface WatchHandlers {
  /** 바뀐 작업·세션을 다시 읽는다. 던지면 seq를 올리지 않고 쉰 뒤 같은 곳부터 다시 건다. */
  onChanges(changes: {workIds: string[]; sessionIds: string[]}): Promise<void>;
  /** 커서를 이어 줄 수 없음. 목록을 처음부터 다시 읽는다(경고 상태 없음). 던지면 onChanges와 같다. */
  onReset(): Promise<void>;
  /** 판이 맞지 않아 이 데이터만 멈춘다. 루프도 끝난다. */
  onPaused(kind: PauseKind): void;
  /** 호출 실패(다시 걸린다) */
  onError(message: string): void;
  /** 한 번 성공하면 불린다(오류 표시를 지우는 데 쓴다) */
  onOk?(): void;
}

export interface WatchOptions {
  after: string;
  handlers: WatchHandlers;
  signal: AbortSignal;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
}

/** 중단되면 곧바로 깬다. 브라우저 setTimeout은 window에 묶여 있어 감싸서 부른다. */
export function abortableSleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    if (signal.aborted) return resolve();
    const id = setTimeout(done, ms);
    function done() {
      clearTimeout(id);
      signal.removeEventListener('abort', done);
      resolve();
    }
    signal.addEventListener('abort', done, {once: true});
  });
}

/** 루프를 돈다. 중단 신호가 서면 진행 중 대기 호출도 끊고 끝난다. */
export async function watchChanges(ctx: WorkCtx, opts: WatchOptions): Promise<void> {
  const {signal, handlers} = opts;
  const sleep = opts.sleep ?? abortableSleep;
  let after = opts.after;
  let waitMax: number | undefined;
  while (!signal.aborted) {
    // 첫 응답이 waitMax를 밝히기 전에는 기다리지 않는다.
    const wait = waitMax ?? 0;
    try {
      const res = await workChanges(ctx, {after, wait}, {timeoutMs: wait + TIMEOUT_GRACE_MS, signal});
      if (signal.aborted) return;
      waitMax = res.waitMax;
      handlers.onOk?.();
      const changed = res.workIds.length > 0 || res.sessionIds.length > 0;
      if (res.reset) await handlers.onReset();
      else if (changed) await handlers.onChanges({workIds: res.workIds, sessionIds: res.sessionIds});
      after = res.seq;
      // more면 기다리지 않고 바로 다시. 서버가 기다려 주지 않는데 바뀐 것도 없으면 쉰다.
      if (!res.more && waitMax === 0 && !changed && !res.reset) await sleep(NO_WAIT_POLL_MS, signal);
    } catch (e) {
      if (signal.aborted) return;
      if (e instanceof WorkPaused) {
        handlers.onPaused(e.kind);
        return;
      }
      handlers.onError(errorMessage(e));
      await sleep(RETRY_MS, signal);
    }
  }
}

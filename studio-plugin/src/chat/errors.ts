// 백엔드·데몬 오류 → 사람이 읽을 글. 코드는 src/daemon/api.ts의 DaemonErrorCode.
import type {daemonText} from '../i18n/messages/daemon.ts';

type Errors = (typeof daemonText)['en']['errors'];

/** `{code, message}` 모양이 아니면 null. */
export function errorParts(error: unknown): {code: string; message: string} | null {
  if (!error || typeof error !== 'object') return null;
  const e = error as {code?: unknown; message?: unknown};
  return {code: typeof e.code === 'string' ? e.code : '', message: typeof e.message === 'string' ? e.message : ''};
}

/** 알려진 코드는 번역한 글, 데몬이 준 상세 글(message)이 있으면 뒤에 덧붙인다. 모르는 코드는 일반 글 + 상세. */
export function daemonErrorText(error: unknown, t: Errors): string {
  const p = errorParts(error);
  if (!p) return t.unknown;
  const known = (t as Record<string, string>)[p.code];
  const head = known ?? t.unknown;
  // ORB_MISSING·STUDIO_TOO_OLD·DAEMON_DOWN은 글만으로 충분하다. 나머지는 데몬이 준 상세를 보인다.
  const plain = p.code === 'ORB_MISSING' || p.code === 'STUDIO_TOO_OLD' || p.code === 'DAEMON_DOWN';
  return !plain && p.message ? `${head} ${p.message}` : head;
}

// 「끝남 · 확인 전」: 보고 있는 중에 끝난 작업은 목록에서 사라지지 않고 이 상태로 남고, 「목록에서 정리」로 기본 목록에서 뺀다.
// 정리 상태는 플러그인 저장소(ctx.storage)에 둔다. 처음 연결할 때 이미 끝난 옛 작업은 올리지 않는다.
import type {WorkCtx, WorkSummary} from './types.ts';

const KEY = 'work.review.v1';
/** 저장하는 「확인 전」 id의 상한. 넘으면 오래된 것부터 버린다(근거 없는 구현 값: 화면에 쌓일 수 있는 양의 상식적 상한) */
export const PENDING_MAX = 200;

export interface ReviewState {
  /** 확인 전인 작업 id(오래된 것이 앞) */
  pending: string[];
}

export async function loadReview(ctx: WorkCtx): Promise<ReviewState> {
  try {
    const saved = await ctx.storage.get<{pending?: unknown}>(KEY);
    const pending = Array.isArray(saved?.pending) ? saved.pending.filter((x): x is string => typeof x === 'string') : [];
    return {pending};
  } catch (e) {
    ctx.log.warn(`Could not read the not-reviewed list: ${e instanceof Error ? e.message : String(e)}`);
    return {pending: []};
  }
}

export async function saveReview(ctx: WorkCtx, review: ReviewState): Promise<void> {
  try {
    await ctx.storage.set(KEY, {pending: review.pending});
  } catch (e) {
    ctx.log.warn(`Could not save the not-reviewed list: ${e instanceof Error ? e.message : String(e)}`);
  }
}

/**
 * 새로 읽은 작업들로 「확인 전」을 갱신한다.
 * - 지난번에 열려 있던 것이 지금 끝났으면 올린다. `known`에 없던(처음 보는) 작업은 올리지 않는다.
 * - 다시 열렸으면 내린다.
 * - 이번에 읽지 않은 것(쪽 밖·범위 밖)은 그대로 둔다. reset으로 목록을 다시 읽어도 표시가 유지된다.
 */
export function updatePending(pending: string[], known: ReadonlyMap<string, string>, works: readonly WorkSummary[]): string[] {
  const set = new Set(pending);
  for (const w of works) {
    if (w.state === 'open') set.delete(w.id);
    else if (w.state === 'done' && known.get(w.id) === 'open') {
      set.delete(w.id); // 뒤로 보내 최근 것이 뒤에 오게 한다
      set.add(w.id);
    }
  }
  const list = [...set];
  return list.length > PENDING_MAX ? list.slice(list.length - PENDING_MAX) : list;
}

/** 「목록에서 정리」 */
export function dismiss(pending: string[], workId: string): string[] {
  return pending.filter(id => id !== workId);
}

/** 기본 목록: 열린 작업과 「끝남 · 확인 전」. 정리한 끝난 작업은 빠진다. */
export function defaultList<T extends Pick<WorkSummary, 'id' | 'state'>>(works: readonly T[], pending: readonly string[]): T[] {
  const p = new Set(pending);
  return works.filter(w => w.state !== 'done' || p.has(w.id));
}

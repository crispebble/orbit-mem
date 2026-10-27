// Orbit 앱의 공유 상태: 제안 목록, 받은 일·배지 동기화, 결정·기억 호출.
// React에 기대지 않는다(화면은 subscribe/getState로 읽는다). ctx만 있으면 시험할 수 있다.
import type {InboxItem, PluginContext} from '@onidot-labs/studio-plugin';
import {callOni, errorMessage, itemsOf, OrbitError} from './oni.ts';
import {
  toInboxItems,
  toMemoryRecord,
  type OniMemory,
  type OniProposal,
  type OniSummary,
  type OniVersion,
} from './model.ts';
import {decideArgs, findArgs, type DecideArgs, type FindParams, type UpdateArgs} from './requests.ts';
import type {MemoryRecord} from '@onidot-labs/studio-plugin/ai';

export interface OrbitState {
  status: 'idle' | 'loading' | 'ready' | 'error';
  proposals: OniProposal[];
  error?: string;
}

export interface Timers {
  setInterval(fn: () => void, ms: number): ReturnType<typeof setInterval>;
  clearInterval(id: ReturnType<typeof setInterval>): void;
}

export const REFRESH_MS = 60_000;

export interface DecideResult {
  proposal: OniProposal;
  changed?: string[];
  created?: string;
  file?: string;
}

/** 브라우저의 setInterval은 window에 묶여 있어 객체 메서드로 부르면 Illegal invocation이 난다. 감싸서 넘긴다. */
const browserTimers: Timers = {
  setInterval: (fn, ms) => setInterval(fn, ms),
  clearInterval: id => clearInterval(id),
};

export function createStore(ctx: PluginContext, timers: Timers = browserTimers) {
  let state: OrbitState = {status: 'idle', proposals: []};
  const listeners = new Set<() => void>();
  let inflight: Promise<void> | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  let offAction: (() => void) | undefined;
  let lastLogged: string | undefined;

  const set = (next: Partial<OrbitState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  async function load(): Promise<void> {
    if (state.status !== 'ready') set({status: 'loading', error: undefined});
    try {
      const out = await callOni<unknown>(ctx, 'proposals', {status: 'all'});
      const proposals = itemsOf<OniProposal>(out, 'proposals');
      lastLogged = undefined;
      set({status: 'ready', proposals, error: undefined});
      const items: InboxItem[] = toInboxItems(proposals);
      ctx.inbox.set(items);
      ctx.app.setBadge('orbit', items.length > 0 ? items.length : null);
    } catch (e) {
      const message = errorMessage(e);
      // 같은 오류를 60초마다 되풀이해 적지 않는다
      if (message !== lastLogged) ctx.log.error(`제안을 불러오지 못했습니다: ${message}`);
      lastLogged = message;
      set({status: 'error', error: message});
    }
  }

  function refresh(): Promise<void> {
    inflight ??= load().finally(() => { inflight = undefined; });
    return inflight;
  }

  async function decide(args: DecideArgs): Promise<DecideResult> {
    const out = await callOni<DecideResult>(ctx, 'proposal_decide', {...args});
    await refresh();
    return out;
  }

  async function handleAction(item: InboxItem, actionId: string): Promise<void> {
    if (actionId !== 'approve' && actionId !== 'reject') return;
    const approve = actionId === 'approve';
    try {
      const out = await decide(decideArgs(item.id, approve ? 'approve' : 'reject'));
      ctx.notify({title: approve ? '제안을 승인했습니다' : '제안을 거절했습니다', body: out.file ? `${item.title} · 파일 반영: ${out.file}` : item.title, tone: 'success'});
    } catch (e) {
      ctx.log.error(`제안 결정 실패(${item.id}): ${errorMessage(e)}`);
      ctx.notify({title: '결정하지 못했습니다', body: `${item.title} · ${errorMessage(e)}`, tone: 'danger'});
    }
  }

  async function findMemories(p: FindParams): Promise<MemoryRecord[]> {
    const out = await callOni<unknown>(ctx, 'memory_find', findArgs(p));
    const now = new Date();
    return itemsOf<OniSummary>(out, 'memory_find').map(s => toMemoryRecord(s, now));
  }

  async function getMemory(id: string): Promise<{memory: OniMemory; history: OniVersion[]}> {
    const out = await callOni<{items?: OniMemory[]; history?: Record<string, OniVersion[]>}>(ctx, 'memory_get', {ids: [id], history: true});
    const memory = itemsOf<OniMemory>(out, 'memory_get')[0];
    if (!memory) throw new OrbitError('기억을 찾지 못했습니다.');
    return {memory, history: out.history?.[id] ?? []};
  }

  /** 제안 근거용. 일부가 없어도 있는 것만 돌려준다. */
  async function getMemories(ids: readonly string[]): Promise<OniMemory[]> {
    const found: OniMemory[] = [];
    for (const id of ids.slice(0, 20)) {
      try {
        const out = await callOni<unknown>(ctx, 'memory_get', {ids: [id]});
        found.push(...itemsOf<OniMemory>(out, 'memory_get'));
      } catch (e) {
        ctx.log.warn(`근거 기억을 읽지 못했습니다(${id}): ${errorMessage(e)}`);
      }
    }
    return found;
  }

  /** 단계를 차례로 부른다. 앞 호출의 결과 판본을 expectedVersion이 비어 있는 다음 단계에 채운다. */
  async function updateMemory(steps: readonly UpdateArgs[]): Promise<OniMemory> {
    let last: OniMemory | undefined;
    for (const step of steps) {
      const args: Record<string, unknown> = {...step};
      if (args.expectedVersion === undefined && last) args.expectedVersion = last.version;
      const out = await callOni<{memory?: OniMemory}>(ctx, 'memory_update', args);
      if (!out.memory) throw new OrbitError('oni의 memory_update 응답 모양이 맞지 않습니다.');
      last = out.memory;
    }
    if (!last) throw new OrbitError('바뀐 내용이 없습니다.');
    return last;
  }

  async function start(): Promise<void> {
    offAction = ctx.inbox.onAction(handleAction);
    await refresh();
    timer = timers.setInterval(() => { void refresh(); }, REFRESH_MS);
  }

  function dispose(): void {
    if (timer !== undefined) timers.clearInterval(timer);
    timer = undefined;
    offAction?.();
    offAction = undefined;
    listeners.clear();
  }

  return {
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
    refresh, decide, findMemories, getMemory, getMemories, updateMemory, start, dispose,
  };
}

export type OrbitStore = ReturnType<typeof createStore>;

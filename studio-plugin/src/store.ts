// OpenOrbit 앱의 공유 상태: 제안 목록, 받은 일·배지 동기화, 결정·기억 호출.
// React에 기대지 않는다(화면은 subscribe/getState로 읽는다). ctx만 있으면 시험할 수 있다.
import type {InboxItem, PluginContext} from '@onidot-labs/studio-plugin';
import {onLocale, pick} from './i18n/index.ts';
import {commonText} from './i18n/messages/common.ts';
import {proposalText} from './i18n/messages/proposals.ts';
import {callOrb, errorMessage, itemsOf, OrbitError} from './orb.ts';
import {
  toInboxItems,
  toMemoryRecord,
  type OrbMemory,
  type OrbProposal,
  type OrbSummary,
  type OrbVersion,
} from './model.ts';
import {decideArgs, findArgs, type DecideArgs, type DecideManyArgs, type FindParams, type UpdateArgs} from './requests.ts';
import type {OrbitMemory} from './parts/index.ts';
import {createInboxParts} from './inbox-parts.ts';

export interface OrbitState {
  status: 'idle' | 'loading' | 'ready' | 'error';
  proposals: OrbProposal[];
  error?: string;
}

export interface Timers {
  setInterval(fn: () => void, ms: number): ReturnType<typeof setInterval>;
  clearInterval(id: ReturnType<typeof setInterval>): void;
}

export const REFRESH_MS = 60_000;

export interface DecideResult {
  proposal: OrbProposal;
  changed?: string[];
  created?: string;
  file?: string;
}

/** 묶음 결정 결과: 결정한 수와, 결정하지 못한 제안과 그 이유 */
export interface BatchOutcome {
  decided: number;
  failed: {id: string; error: string}[];
}

/** 브라우저의 setInterval은 window에 묶여 있어 객체 메서드로 부르면 Illegal invocation이 난다. 감싸서 넘긴다. */
const browserTimers: Timers = {
  setInterval: (fn, ms) => setInterval(fn, ms),
  clearInterval: id => clearInterval(id),
};

/** `inbox`는 받은 일 갈래 합치기. 작업의 확인 기다림 항목도 같은 것에 'waiting' 갈래로 넣어야 서로 지우지 않는다. */
export function createStore(ctx: PluginContext, timers: Timers = browserTimers, inbox = createInboxParts<InboxItem>(ctx.inbox)) {
  let state: OrbitState = {status: 'idle', proposals: []};
  const listeners = new Set<() => void>();
  let inflight: Promise<void> | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  let offAction: (() => void) | undefined;
  let offLocale: (() => void) | undefined;
  let lastLogged: string | undefined;

  const set = (next: Partial<OrbitState>) => {
    state = {...state, ...next};
    listeners.forEach(l => l());
  };

  /** 받은 일·배지를 지금 제안과 언어로 다시 만든다 */
  function publishInbox(): void {
    const items: InboxItem[] = toInboxItems(state.proposals);
    inbox.set('proposals', items);
    ctx.app.setBadge('orbit', items.length > 0 ? items.length : null);
  }

  async function load(): Promise<void> {
    if (state.status !== 'ready') set({status: 'loading', error: undefined});
    try {
      const out = await callOrb<unknown>(ctx, 'proposals', {status: 'all'});
      const proposals = itemsOf<OrbProposal>(out, 'proposals');
      lastLogged = undefined;
      set({status: 'ready', proposals, error: undefined});
      publishInbox();
    } catch (e) {
      const message = errorMessage(e);
      // 같은 오류를 60초마다 되풀이해 적지 않는다
      if (message !== lastLogged) ctx.log.error(`Could not load proposals: ${message}`);
      lastLogged = message;
      set({status: 'error', error: message});
    }
  }

  function refresh(): Promise<void> {
    inflight ??= load().finally(() => { inflight = undefined; });
    return inflight;
  }

  async function decide(args: DecideArgs): Promise<DecideResult> {
    const out = await callOrb<DecideResult>(ctx, 'proposal_decide', {...args});
    await refresh();
    return out;
  }

  /** 여럿을 한 번에 결정한다. 하나가 실패해도 나머지는 orb가 결정하고, 실패한 것은 이유와 함께 돌아온다. */
  async function decideMany(args: DecideManyArgs): Promise<BatchOutcome> {
    const out = await callOrb<{decided?: unknown[] | null; failed?: BatchOutcome['failed'] | null}>(ctx, 'proposal_decide', {...args});
    await refresh();
    return {decided: out.decided?.length ?? 0, failed: out.failed ?? []};
  }

  async function handleAction(item: InboxItem, actionId: string): Promise<void> {
    if (actionId !== 'approve' && actionId !== 'reject') return;
    const approve = actionId === 'approve';
    try {
      const out = await decide(decideArgs(item.id, approve ? 'approve' : 'reject'));
      const t = pick(proposalText).notify;
      ctx.notify({title: approve ? t.approved : t.rejected, body: out.file ? t.appliedToFile(item.title, out.file) : item.title, tone: 'success'});
    } catch (e) {
      ctx.log.error(`Proposal decision failed (${item.id}): ${errorMessage(e)}`);
      ctx.notify({title: pick(proposalText).notify.failed, body: `${item.title} · ${errorMessage(e)}`, tone: 'danger'});
    }
  }

  async function findMemories(p: FindParams): Promise<OrbitMemory[]> {
    const out = await callOrb<unknown>(ctx, 'memory_find', findArgs(p));
    const now = new Date();
    return itemsOf<OrbSummary>(out, 'memory_find').map(s => toMemoryRecord(s, now));
  }

  async function getMemory(id: string): Promise<{memory: OrbMemory; history: OrbVersion[]}> {
    const out = await callOrb<{items?: OrbMemory[]; history?: Record<string, OrbVersion[]>}>(ctx, 'memory_get', {ids: [id], history: true});
    const memory = itemsOf<OrbMemory>(out, 'memory_get')[0];
    if (!memory) throw new OrbitError(pick(commonText).orb.memoryNotFound);
    return {memory, history: out.history?.[id] ?? []};
  }

  /** 제안 근거·관련 기억용(20개까지). 함께 부르고, 일부가 없어도 있는 것만 순서대로 돌려준다. */
  async function getMemories(ids: readonly string[]): Promise<OrbMemory[]> {
    const each = await Promise.all(ids.slice(0, 20).map(async id => {
      try {
        return itemsOf<OrbMemory>(await callOrb<unknown>(ctx, 'memory_get', {ids: [id]}), 'memory_get');
      } catch (e) {
        ctx.log.warn(`Could not read memory (${id}): ${errorMessage(e)}`);
        return [];
      }
    }));
    return each.flat();
  }

  /** 단계를 차례로 부른다. 앞 호출의 결과 판본을 expectedVersion이 비어 있는 다음 단계에 채운다. */
  async function updateMemory(steps: readonly UpdateArgs[]): Promise<OrbMemory> {
    let last: OrbMemory | undefined;
    for (const step of steps) {
      const args: Record<string, unknown> = {...step};
      if (args.expectedVersion === undefined && last) args.expectedVersion = last.version;
      const out = await callOrb<{memory?: OrbMemory}>(ctx, 'memory_update', args);
      if (!out.memory) throw new OrbitError(pick(commonText).orb.badShape('memory_update'));
      last = out.memory;
    }
    if (!last) throw new OrbitError(pick(commonText).orb.nothingChanged);
    return last;
  }

  async function start(): Promise<void> {
    offAction = ctx.inbox.onAction(handleAction);
    // 언어가 바뀌면 이미 올린 받은 일 글도 새 언어로 다시 올린다
    offLocale = onLocale(() => { if (state.status === 'ready') publishInbox(); });
    await refresh();
    timer = timers.setInterval(() => { void refresh(); }, REFRESH_MS);
  }

  function dispose(): void {
    if (timer !== undefined) timers.clearInterval(timer);
    timer = undefined;
    offAction?.();
    offAction = undefined;
    offLocale?.();
    offLocale = undefined;
    listeners.clear();
  }

  return {
    getState: () => state,
    subscribe(l: () => void) {
      listeners.add(l);
      return () => { listeners.delete(l); };
    },
    refresh, decide, decideMany, findMemories, getMemory, getMemories, updateMemory, start, dispose,
  };
}

export type OrbitStore = ReturnType<typeof createStore>;

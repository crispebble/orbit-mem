// 기억 그래프 탭 열기. 탭은 하나만 연다(같은 id면 이미 열린 탭으로 간다).
// 이미 열린 탭은 open의 params를 다시 받지 않으므로, 고를 기억은 여기 남겨 두고 탭이 받아 간다.
import type {PluginContext} from '@onidot-labs/studio-plugin';

export const GRAPH_TAB = {type: 'memory-graph', id: 'graph', title: '기억 그래프'} as const;

let pending: string | undefined;
const listeners = new Set<(id: string) => void>();

/** `select`를 주면 그 기억을 고른 채로 연다 */
export function openMemoryGraph(ctx: Pick<PluginContext, 'tabs'>, select?: string): void {
  if (select) {
    pending = select;
    listeners.forEach(l => l(select));
  }
  ctx.tabs.open({...GRAPH_TAB, params: select ? {select} : {}});
}

/** 탭이 처음 그릴 때 남아 있는 요청을 받아 간다 */
export function takePendingSelect(): string | undefined {
  const id = pending;
  pending = undefined;
  return id;
}

/** 열린 탭이 이후 요청을 받는다. 받은 요청은 지운다 */
export function onSelectRequest(listener: (id: string) => void): () => void {
  const wrapped = (id: string) => { pending = undefined; listener(id); };
  listeners.add(wrapped);
  return () => { listeners.delete(wrapped); };
}

// 기억 그래프 탭·관련 기억을 서버 렌더로 그려 글을 돌려준다(memory-graph-render.test.ts가 esbuild로 묶어 부른다). 효과(useEffect)는 돌지 않는다.
import {renderToStaticMarkup} from 'react-dom/server';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {describeNetwork} from '@onidot-labs/orbit-studio-plugin/graph';
import {GraphBody, SelectedMemoryCard} from '../src/memory-graph/graph-tab.tsx';
import {toMemoryNetwork} from '../src/memory-graph/network.ts';
import type {GraphNode, MemoryGraph} from '../src/memory-graph/contract.ts';
import type {GraphState} from '../src/memory-graph/store.ts';
import {MemoryDetail, type MemoryLinkView} from '../src/parts/index.ts';

export {setLocale} from '../src/i18n/index.ts';

const noop = () => {};
const ctx = {tabs: {open: noop}, app: {show: noop}, log: {error: noop}} as unknown as PluginContext;
const store = {load: async () => {}, setIncludeHidden: async () => {}};

export const renderBody = (state: GraphState) => renderToStaticMarkup(<GraphBody ctx={ctx} store={store} state={state} />);

export function renderCard(graph: MemoryGraph, node: GraphNode) {
  const info = describeNetwork(toMemoryNetwork(graph));
  return renderToStaticMarkup(<SelectedMemoryCard node={node} info={info} centered={false} onOpen={noop} onCenter={noop} onClear={noop} />);
}

export const renderLinks = (links: MemoryLinkView[], open = true) =>
  renderToStaticMarkup(<MemoryDetail memory={{id: 'm-1', title: '제목', content: '본문'}} links={links} onOpenMemory={open ? noop : undefined} />);

import {definePlugin} from '@onidot-labs/studio-plugin';
import type {InboxDetailProps, TabProps} from '@onidot-labs/studio-plugin';
import './index.css';
import './parts/parts.css';
import {MemoriesView} from './memories-view.tsx';
import {MemoryPane} from './memory-pane.tsx';
import {ProposalPane} from './proposal-pane.tsx';
import {ProposalsView} from './proposals-view.tsx';
import {setStore} from './runtime.ts';
import {createStore, type OrbitStore} from './store.ts';

let store: OrbitStore | undefined;

function ProposalTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  return <ProposalPane ctx={ctx} proposalId={id} />;
}

function OrbitInboxDetail({ctx, item}: InboxDetailProps) {
  return <ProposalPane ctx={ctx} proposalId={item.id} />;
}

export default definePlugin({
  async activate(ctx) {
    store?.dispose();
    store = createStore(ctx);
    setStore(store);
    // 첫 불러오기가 실패해도 켜기는 성공한다. 실패 이유는 화면(다시 시도)과 log.error에 남는다.
    await store.start();
  },
  deactivate() {
    store?.dispose();
    store = undefined;
    setStore(undefined);
  },
  views: {
    'orbit/proposals': ProposalsView,
    'orbit/memories': MemoriesView,
  },
  tabs: {
    proposal: ProposalTab,
    memory: MemoryPane,
  },
  inboxDetail: OrbitInboxDetail,
  commands: {
    'orbit.find': ctx => ctx.app.show('orbit', 'memories'),
    'orbit.proposals': ctx => ctx.app.show('orbit', 'proposals'),
  },
});

import {definePlugin} from '@onidot-labs/studio-plugin';
import type {InboxDetailProps, InboxItem, TabProps} from '@onidot-labs/studio-plugin';
import './index.css';
import './parts/parts.css';
import './work-ui/work.css';
import {createInboxParts} from './inbox-parts.ts';
import {MemoriesView} from './memories-view.tsx';
import {MemoryPane} from './memory-pane.tsx';
import {ProposalPane} from './proposal-pane.tsx';
import {ProposalsView} from './proposals-view.tsx';
import {setStore} from './runtime.ts';
import {createStore, type OrbitStore} from './store.ts';
import {parseWaitingId, syncWaitingInbox} from './work/inbox.ts';
import {createWorkStore, type WorkStore} from './work/store.ts';
import {setWorkStore} from './work-ui/runtime.ts';
import {SessionTab} from './work-ui/session-tab.tsx';
import {SessionsView} from './work-ui/sessions-view.tsx';
import {WaitingStepDetail, WorkTab} from './work-ui/work-tab.tsx';
import {WorksView} from './work-ui/works-view.tsx';

let store: OrbitStore | undefined;
let workStore: WorkStore | undefined;
let offWaiting: (() => void) | undefined;

function ProposalTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  return <ProposalPane ctx={ctx} proposalId={id} />;
}

function OrbitInboxDetail({ctx, item}: InboxDetailProps) {
  const waiting = parseWaitingId(item.id);
  if (waiting) return <WaitingStepDetail ctx={ctx} workId={waiting.workId} stepId={waiting.stepId} />;
  return <ProposalPane ctx={ctx} proposalId={item.id} />;
}

function stop() {
  offWaiting?.();
  offWaiting = undefined;
  workStore?.dispose();
  workStore = undefined;
  setWorkStore(undefined);
  store?.dispose();
  store = undefined;
  setStore(undefined);
}

export default definePlugin({
  async activate(ctx) {
    stop();
    // 제안과 작업의 확인 기다림이 받은 일 전체를 서로 지우지 않게 한 갈래 합치기를 함께 쓴다
    const inbox = createInboxParts<InboxItem>(ctx.inbox);
    store = createStore(ctx, undefined, inbox);
    setStore(store);
    // 작업 기록은 읽는 쪽 연결(`oni mcp --reader`)로 따로 읽는다. 판이 맞지 않으면 작업·세션 화면만 멈춘다.
    workStore = createWorkStore(ctx);
    setWorkStore(workStore);
    offWaiting = syncWaitingInbox(workStore, inbox);
    // 첫 불러오기가 실패해도 켜기는 성공한다. 실패 이유는 화면(다시 시도)과 log.error에 남는다.
    await Promise.all([store.start(), workStore.start()]);
  },
  deactivate() {
    stop();
  },
  views: {
    'orbit/proposals': ProposalsView,
    'orbit/memories': MemoriesView,
    'orbit/works': WorksView,
    'orbit/sessions': SessionsView,
  },
  tabs: {
    proposal: ProposalTab,
    memory: MemoryPane,
    work: WorkTab,
    session: SessionTab,
  },
  inboxDetail: OrbitInboxDetail,
  commands: {
    'orbit.find': ctx => ctx.app.show('orbit', 'memories'),
    'orbit.proposals': ctx => ctx.app.show('orbit', 'proposals'),
    'orbit.works': ctx => ctx.app.show('orbit', 'works'),
    'orbit.sessions': ctx => ctx.app.show('orbit', 'sessions'),
  },
});

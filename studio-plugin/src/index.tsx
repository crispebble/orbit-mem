import {definePlugin} from '@onidot-labs/orbit-studio-plugin';
import type {PluginContext, TabProps} from '@onidot-labs/orbit-studio-plugin';
import './index.css';
import './parts/parts.css';
import './work-ui/work.css';
import './chat/chat.css';
import './profiles/profiles.css';
import {followStudio} from './i18n/index.ts';
import {MemoryGraphTab} from './memory-graph/graph-tab.tsx';
import {openMemoryGraph} from './memory-graph/open.ts';
import {MemoriesView} from './memories-view.tsx';
import {MemoryPane} from './memory-pane.tsx';
import {ProposalPane} from './proposal-pane.tsx';
import {ProposalsView} from './proposals-view.tsx';
import {setStore} from './runtime.ts';
import {createStore, type OrbitStore} from './store.ts';
import {ChatToolWindow} from './chat/tool-window.tsx';
import {openChat} from './chat/open.ts';
import {createDaemonClient, type DaemonClient} from './daemon/client.ts';
import {setDaemonClient} from './daemon/runtime.ts';
import {daemonDecisionSource} from './decision/daemon.ts';
import {ProfilesTab} from './profiles/tab.tsx';
import {ProfilesView} from './profiles/view.tsx';
import {registerDecisionProvider} from './decision/provider.ts';
import {orbDecisionSource} from './decision/source.ts';
import {createDecisionStore, type DecisionStore} from './decision/store.ts';
import {registerWorkProvider, type WorkChatHooks} from './work/provider.ts';
import {createWorkStore, type WorkStore} from './work/store.ts';
import {setWorkStore} from './work-ui/runtime.ts';
import {SessionTab} from './work-ui/session-tab.tsx';
import {WorkTab} from './work-ui/work-tab.tsx';

let store: OrbitStore | undefined;
let workStore: WorkStore | undefined;
let decisionStore: DecisionStore | undefined;
let offProvider: (() => void) | undefined;
let offDecisions: (() => void) | undefined;
let offLocale: (() => void) | undefined;
let daemonClient: DaemonClient | undefined;
let offDaemon: (() => void) | undefined;
let disposeDecisionSource: (() => void) | undefined;

function ProposalTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  return <ProposalPane ctx={ctx} proposalId={id} />;
}

/** 데몬 클라이언트(S4-5): 뒤쪽 연결이 있으면 만들고, 번역 글(GetMessages)을 Studio 언어로 맞춰 둔다. 글을 못 읽어도 켜기는 성공한다. */
function startDaemon(ctx: PluginContext): DaemonClient | undefined {
  const backend = (ctx as {backend?: Parameters<typeof createDaemonClient>[0]}).backend;
  if (!backend || typeof backend.call !== 'function') return undefined;
  const client = createDaemonClient(backend);
  setDaemonClient(client);
  let loaded: string | undefined;
  let loading: string | undefined;
  const wanted = () => (ctx.locale?.current() === 'ko' ? 'ko' : 'en');
  // 데몬이 내려가 있으면 글을 받을 수 없다: 올라온 뒤(상태 사건)에 다시 시도한다
  const syncMessages = () => {
    const lang = wanted();
    const s = client.status();
    if (s.daemon !== 'up' || !s.paired || loaded === lang || loading === lang) return;
    loading = lang;
    client.loadMessages(lang).then(
      () => { loaded = lang; },
      e => ctx.log.warn(`Could not load daemon messages: ${e instanceof Error ? e.message : String(e)}`),
    ).finally(() => { if (loading === lang) loading = undefined; });
  };
  const offStatus = client.onStatus(syncMessages);
  const offLang = ctx.locale?.onChange(() => { loaded = undefined; syncMessages(); });
  offDaemon = () => {
    offStatus();
    offLang?.();
    client.dispose();
    setDaemonClient(undefined);
  };
  return client;
}

function stop() {
  disposeDecisionSource?.();
  disposeDecisionSource = undefined;
  offDaemon?.();
  offDaemon = undefined;
  daemonClient = undefined;
  offProvider?.();
  offProvider = undefined;
  offDecisions?.();
  offDecisions = undefined;
  decisionStore?.dispose();
  decisionStore = undefined;
  offLocale?.();
  offLocale = undefined;
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
    // 보이는 글은 Studio 언어를 따른다(옛 Studio에는 ctx.locale이 없어 영어).
    offLocale = followStudio(ctx.locale);
    store = createStore(ctx);
    setStore(store);
    // 작업 기록은 읽는 쪽 연결(`orb mcp --reader`)로 따로 읽는다. 판이 맞지 않으면 작업·세션 화면만 멈춘다.
    workStore = createWorkStore(ctx);
    setWorkStore(workStore);
    // 데몬 클라이언트(S4-5). 데몬은 여기서 띄우지 않는다: 상태를 한 번 읽을 뿐이고, 띄우기는 작업 대화의 「시작」이 ensure로 한다.
    daemonClient = startDaemon(ctx);
    const client = daemonClient;
    if (client) await client.refresh().catch(e => ctx.log.warn(`Could not read the daemon status: ${e instanceof Error ? e.message : String(e)}`));
    // 결정 카드(orbit.decision)를 Orbit Studio Needs you에 준다(Studio 4차 S4-3). 카드 출처는 orb 연결에 데몬 카드를 합친 것이다(S4-5).
    const orbSource = orbDecisionSource(ctx);
    const composite = client ? daemonDecisionSource(client, orbSource, {log: ctx.log}) : undefined;
    disposeDecisionSource = composite?.dispose;
    const decisionSource = composite ?? orbSource;
    decisionStore = createDecisionStore(decisionSource, {log: ctx.log});
    offDecisions = registerDecisionProvider(ctx as Parameters<typeof registerDecisionProvider>[0], decisionStore, decisionSource);
    // 작업 목록·세션 보기는 Orbit Studio 홈 작업 지도로 옮겼다(Studio 4차 S4-2). 홈에 작업을 공급한다. 열린 카드도 「답 기다림」에 센다.
    // 지도의 열기·새 일 시키기는 작업 대화 창을 열 수 있는 Studio(API 0.1.27, ctx.toolWindows)와 데몬 클라이언트가 있을 때만 건다.
    const canChat = !!client && typeof (ctx as {toolWindows?: {show?: unknown}}).toolWindows?.show === 'function';
    const chat: WorkChatHooks | undefined = canChat ? {
      runFor: async ({workId, sessionId}) => {
        const {runs} = await client.api.listRuns({limit: 50});
        return runs.find(r => (workId ? r.workId === workId : r.sessionId === sessionId))?.runId;
      },
      open: target => void openChat(ctx, target),
    } : undefined;
    offProvider = registerWorkProvider(ctx as Parameters<typeof registerWorkProvider>[0], workStore, decisionStore, chat);
    // 첫 불러오기가 실패해도 켜기는 성공한다. 실패 이유는 화면(다시 시도)과 log.error에 남는다.
    await Promise.all([store.start(), workStore.start(), decisionStore.start()]);
  },
  deactivate() {
    stop();
  },
  views: {
    'orbit/proposals': ProposalsView,
    'orbit/memories': MemoriesView,
    'orbit/profiles': ProfilesView,
  },
  toolWindows: {
    chat: ChatToolWindow,
  },
  tabs: {
    proposal: ProposalTab,
    memory: MemoryPane,
    work: WorkTab,
    session: SessionTab,
    'memory-graph': MemoryGraphTab,
    profiles: ProfilesTab,
  },
  commands: {
    'orbit.find': ctx => ctx.app.show('orbit', 'memories'),
    'orbit.memory-graph': ctx => openMemoryGraph(ctx),
    'orbit.proposals': ctx => ctx.app.show('orbit', 'proposals'),
  },
});

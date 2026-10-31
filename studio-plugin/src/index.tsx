import {definePlugin} from '@onidot-labs/orbit-studio-plugin';
import type {TabProps} from '@onidot-labs/orbit-studio-plugin';
import './index.css';
import './parts/parts.css';
import './work-ui/work.css';
import {followStudio} from './i18n/index.ts';
import {MemoryGraphTab} from './memory-graph/graph-tab.tsx';
import {openMemoryGraph} from './memory-graph/open.ts';
import {MemoriesView} from './memories-view.tsx';
import {MemoryPane} from './memory-pane.tsx';
import {ProposalPane} from './proposal-pane.tsx';
import {ProposalsView} from './proposals-view.tsx';
import {setStore} from './runtime.ts';
import {createStore, type OrbitStore} from './store.ts';
import {registerDecisionProvider} from './decision/provider.ts';
import {orbDecisionSource} from './decision/source.ts';
import {createDecisionStore, type DecisionStore} from './decision/store.ts';
import {registerWorkProvider} from './work/provider.ts';
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

function ProposalTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  return <ProposalPane ctx={ctx} proposalId={id} />;
}

function stop() {
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
    // 결정 카드(orbit.decision)를 Orbit Studio Needs you에 준다(Studio 4차 S4-3). 카드 출처는 지금 orb 연결이고,
    // R4-4 orbit-pi 데몬이 생기면 같은 DecisionSource 모양의 데몬 구독으로 바꿔 끼운다.
    const decisionSource = orbDecisionSource(ctx);
    decisionStore = createDecisionStore(decisionSource, {log: ctx.log});
    offDecisions = registerDecisionProvider(ctx as Parameters<typeof registerDecisionProvider>[0], decisionStore, decisionSource);
    // 작업 목록·세션 보기는 Orbit Studio 홈 작업 지도로 옮겼다(Studio 4차 S4-2). 홈에 작업을 공급한다. 열린 카드도 「답 기다림」에 센다.
    offProvider = registerWorkProvider(ctx as Parameters<typeof registerWorkProvider>[0], workStore, decisionStore);
    // 첫 불러오기가 실패해도 켜기는 성공한다. 실패 이유는 화면(다시 시도)과 log.error에 남는다.
    await Promise.all([store.start(), workStore.start(), decisionStore.start()]);
  },
  deactivate() {
    stop();
  },
  views: {
    'orbit/proposals': ProposalsView,
    'orbit/memories': MemoriesView,
  },
  tabs: {
    proposal: ProposalTab,
    memory: MemoryPane,
    work: WorkTab,
    session: SessionTab,
    'memory-graph': MemoryGraphTab,
  },
  commands: {
    'orbit.find': ctx => ctx.app.show('orbit', 'memories'),
    'orbit.memory-graph': ctx => openMemoryGraph(ctx),
    'orbit.proposals': ctx => ctx.app.show('orbit', 'proposals'),
  },
});

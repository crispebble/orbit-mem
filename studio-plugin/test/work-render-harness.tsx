// 작업·세션 화면을 서버 렌더로 그려 글을 돌려준다(work-render.test.ts가 esbuild로 묶어 부른다). 효과(useEffect)는 돌지 않는다.
import {renderToStaticMarkup} from 'react-dom/server';
import type {PluginContext} from '@onidot-labs/studio-plugin';
// 묶은 이 파일 안의 언어 상태(시험 파일의 것과 다른 사본)를 바꿀 수 있게 내보낸다
export {setLocale} from '../src/i18n/index.ts';
import {setWorkStore, stepTitleMap} from '../src/work-ui/runtime.ts';
import {SessionBody, type Loaded} from '../src/work-ui/session-tab.tsx';
import {SessionsView} from '../src/work-ui/sessions-view.tsx';
import {WaitingStepDetail, WorkTab, workNav} from '../src/work-ui/work-tab.tsx';
import {WorksView} from '../src/work-ui/works-view.tsx';
import type {WorkState, WorkStore} from '../src/work/store.ts';

const noop = () => {};
export const ctx = {
  tabs: {open: noop, setTitle: noop, setParams: noop, setModified: noop, close: noop},
  notify: noop,
  studio: {openCommit: async () => true, openFile: async () => true},
  terminal: {open: async () => true},
  app: {show: noop, setBadge: noop},
} as unknown as PluginContext;

export function useState(state: WorkState) {
  const store = {
    getState: () => state,
    subscribe: () => noop,
    visibleWorks: () => state.works.filter(w => w.state !== 'done' || state.pendingReview.includes(w.id)),
    dismiss: async () => {},
    loadDetail: async (id: string) => state.details[id],
    loadSession: async () => { throw new Error('시험에서 읽지 않음'); },
  } as unknown as WorkStore;
  setWorkStore(store);
}

const tab = (type: string, id: string, params: Record<string, unknown> = {}) => ({key: `${type}:${id}`, type, id, title: id, params: {id, ...params}});

export const renderWorkTab = (id: string, params: Record<string, unknown> = {}) => renderToStaticMarkup(<WorkTab ctx={ctx} tab={tab('work', id, params)} />);
export const renderWaiting = (workId: string, stepId: string) => renderToStaticMarkup(<WaitingStepDetail ctx={ctx} workId={workId} stepId={stepId} />);
export const renderWorks = () => renderToStaticMarkup(<WorksView ctx={ctx} appId="orbit" viewId="works" />);
export const renderSessions = () => renderToStaticMarkup(<SessionsView ctx={ctx} appId="orbit" viewId="sessions" />);
export const renderSession = (loaded: Loaded, state: WorkState, now: number) =>
  renderToStaticMarkup(<SessionBody ctx={ctx} loaded={loaded} now={now} titles={stepTitleMap(state)} state={state} nav={workNav(ctx, noop)} onReload={noop} onMore={noop} />);

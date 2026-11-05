// 작업 탭: 머리(제목·상태·진행·프로젝트·마지막 갱신·상위 작업 길)와 본문(그래프 + 단계 상세 / 담당별).
// 그래프는 forma-ai FlowGraph, 담당별은 forma-ai AgentOrchestrator, 나머지는 forma-ui 부품으로 조립한다.
import {useCallback, type ComponentProps, useEffect, useMemo, useState} from 'react';
import type {PluginContext, TabProps} from '@onidot-labs/orbit-studio-plugin';
import {AgentOrchestrator, FlowGraph, type FlowGraphView} from '@onidot-labs/orbit-studio-plugin/ai';
import {Badge, Breadcrumbs, Button, StateView, Progress, SegmentedControl} from '@onidot-labs/orbit-studio-plugin/ui';
import {pick, useMessages} from '../i18n/index.ts';
import {workKitText, workUiText} from '../i18n/messages/work.ts';
import {errorMessage} from '../orb.ts';
import {WorkToolError} from '../work/contract.ts';
import {progressOf, workStateView} from '../work/display.ts';
import {agoText, toFlowGraph} from '../work/graph.ts';
import {ownerWorkers} from '../work/owners.ts';
import {isOrbitCodeWork} from '../work/provider.ts';
import {summaryOf} from '../work/store.ts';
import type {Work} from '../work/types.ts';
import {WorkBrief} from './brief.tsx';
import {WorkLearning} from './learning.tsx';
import {StageDetail, type StageNav} from './stage-detail.tsx';
import {useNow, useWork} from './runtime.ts';
import {Paused} from './paused.tsx';

type Mode = 'flow' | 'owner';

// 부품의 FlowGraphText 타입이 한국어 글자 그대로의 리터럴이라 영어 글을 그대로는 넘길 수 없다(부품 쪽 타입 문제). 값은 부품이 문자열로 쓴다.
type FlowGraphTextProp = ComponentProps<typeof FlowGraph>['text'];

export function workNav(ctx: PluginContext, selectStep: (id: string) => void): StageNav {
  return {
    selectStep,
    openWork: (id, title) => ctx.tabs.open({type: 'work', id, title: title ?? pick(workUiText).workTab.work, params: {id}}),
    openSession: (id, title) => ctx.tabs.open({type: 'session', id, title: title ?? pick(workUiText).workTab.session, params: {id}, beside: true}),
  };
}

function useWorkDetail(id: string): {work?: Work; error?: string; missing: boolean; reload: () => void} {
  const {store, state} = useWork();
  const [loaded, setLoaded] = useState<Work>();
  const [error, setError] = useState<string>();
  const [missing, setMissing] = useState(false);
  const [tick, setTick] = useState(0);
  const live = state.details[id];

  useEffect(() => {
    if (!store || state.status === 'paused') return;
    let alive = true;
    setError(undefined);
    store.loadDetail(id, tick > 0).then(
      w => { if (alive) { setLoaded(w); setMissing(false); } },
      (e: unknown) => {
        if (!alive) return;
        if (e instanceof WorkToolError && e.code === 'NOT_FOUND') setMissing(true);
        else setError(errorMessage(e));
      },
    );
    return () => { alive = false; };
  }, [store, id, tick, state.status]);

  // 실시간 갱신은 store.details에 들어온다. 범위 밖으로 빠져 store가 지워도 탭은 마지막으로 읽은 것을 보인다.
  return {work: live ?? loaded, error, missing, reload: () => setTick(t => t + 1)};
}

export function WorkTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  const {store, state} = useWork();
  const u = useMessages(workUiText);
  const t = u.workTab;
  const kit = useMessages(workKitText);
  const {work, error, missing, reload} = useWorkDetail(id);
  const now = useNow();
  const [selected, setSelected] = useState<string | null>(typeof tab.params.step === 'string' ? tab.params.step : null);
  const [mode, setMode] = useState<Mode>(tab.params.mode === 'owner' ? 'owner' : 'flow');
  const [view, setView] = useState<FlowGraphView | 'auto'>('auto');

  // 고른 단계·보기를 탭에 남겨 다시 켰을 때 돌아오게 한다
  useEffect(() => {
    ctx.tabs.setParams('work', tab.id, {id, ...(selected ? {step: selected} : {}), ...(mode === 'owner' ? {mode} : {})});
  }, [ctx, tab.id, id, selected, mode]);

  useEffect(() => {
    if (work) ctx.tabs.setTitle('work', tab.id, work.title);
  }, [ctx, tab.id, work?.title]);

  const select = useCallback((stepId: string | null) => setSelected(stepId), []);
  const nav = useMemo(() => workNav(ctx, select), [ctx, select]);
  const graph = useMemo(() => (work ? toFlowGraph(work, now) : undefined), [work, now]);

  if (!store) return <StateView variant="loading" title={u.starting} />;
  if (state.status === 'paused') return <Paused store={store} state={state} title={u.paused.workTitle} />;
  if (missing) return <StateView title={t.notFound} description={t.notFoundHint} />;
  if (!work) {
    if (error) return <StateView variant="error" title={t.readFailed} description={error} onRetry={reload} retryLabel={kit.retry} />;
    return <StateView variant="loading" title={t.loading} />;
  }

  const summary = summaryOf(work);
  const pending = state.pendingReview.includes(work.id);
  const status = workStateView(work, pending);
  const progress = progressOf(summary);
  const parent = work.parent ? state.works.find(w => w.id === work.parent!.workId) : undefined;
  const step = selected ? work.steps.find(s => s.id === selected) : undefined;
  const noLines = work.steps.length >= 2 && graph!.edges.length === 0;

  return (
    <div className="orbit-work-tab">
      <header className="orbit-work-head">
        {work.parent && (
          <Breadcrumbs label={t.parentPath} items={[
            {label: parent?.title ?? t.parentWork, onClick: () => nav.openWork(work.parent!.workId, parent?.title)},
            {label: work.title},
          ]} />
        )}
        <div className="orbit-row orbit-work-head-row">
          <h2 className="orbit-work-title" title={work.title}>{work.title}</h2>
          <Badge tone={status.tone}>{status.label}</Badge>
          <Progress label={t.stepProgress} done={progress.done} total={progress.total} size="sm" />
          {work.project && <span className="orbit-meta">{t.project} <b>{work.project.label}</b></span>}
          <span className="orbit-meta" title={work.updatedAt}>{t.updated(agoText(work.updatedAt, now) ?? '')}</span>
          <span className="orbit-grow" />
          {pending && <Button variant="secondary" size="sm" onClick={() => void store.dismiss(work.id)}>{t.dismiss}</Button>}
          <SegmentedControl label={t.viewLabel} value={mode} onChange={v => setMode(v === 'owner' ? 'owner' : 'flow')}
            options={[{value: 'flow', label: t.flow}, {value: 'owner', label: t.byOwner}]} />
        </div>
      </header>
      <WorkBrief work={work} onSelectStep={id => { setMode('flow'); select(id); }} />
      {isOrbitCodeWork(work) && <WorkLearning workId={work.id} state={work.state} />}
      {mode === 'flow' ? (
        <div className="orbit-work-graph">
          <FlowGraph
            label={t.graphLabel}
            nodes={graph!.nodes}
            edges={graph!.edges}
            columns={graph!.columns}
            activeColumn={graph!.activeColumn}
            selectedId={selected}
            onSelect={select}
            view={view}
            onViewChange={setView}
            notice={noLines ? t.noEdges : undefined}
            asideLabel={t.detailLabel}
            text={kit.flowGraph as FlowGraphTextProp}
            aside={step ? (
              <StageDetail ctx={ctx} work={work} step={step} now={now} works={state.works} sessions={state.sessions} nav={nav} onClose={() => select(null)} />
            ) : null}
          />
        </div>
      ) : (
        <div className="orbit-work-owners">
          <AgentOrchestrator
            header={false}
            title={work.title}
            status={work.state === 'done' ? 'succeeded' : 'running'}
            workers={ownerWorkers(work.steps)}
            variant="lanes"
            surface="plain"
          />
        </div>
      )}
    </div>
  );
}

/** 받은 일에서 「사용자 확인 기다림」 단계를 골랐을 때 메인에 그리는 상세 */
export function WaitingStepDetail({ctx, workId, stepId}: {ctx: PluginContext; workId: string; stepId: string}) {
  const {store, state} = useWork();
  const u = useMessages(workUiText);
  const t = u.workTab;
  const kit = useMessages(workKitText);
  const {work, error, missing, reload} = useWorkDetail(workId);
  const now = useNow();
  const nav = useMemo(() => workNav(ctx, sid => ctx.tabs.open({type: 'work', id: workId, title: work?.title ?? t.work, params: {id: workId, step: sid}})), [ctx, workId, work?.title, t.work]);
  if (store && state.status === 'paused') return <Paused store={store} state={state} title={u.paused.workTitle} />;
  if (missing) return <StateView title={t.notFound} />;
  if (!work) {
    if (error) return <StateView variant="error" title={t.readFailed} description={error} onRetry={reload} retryLabel={kit.retry} />;
    return <StateView variant="loading" title={t.loading} />;
  }
  const step = work.steps.find(s => s.id === stepId);
  if (!step) return <StateView title={t.stepNotFound} />;
  return (
    <div className="orbit-pane">
      <div className="orbit-row">
        <span className="orbit-meta">{work.title}</span>
        <Button variant="secondary" size="sm" onClick={() => nav.selectStep(step.id)}>{t.showInGraph}</Button>
      </div>
      <StageDetail ctx={ctx} work={work} step={step} now={now} works={state.works} sessions={state.sessions} nav={nav} />
    </div>
  );
}

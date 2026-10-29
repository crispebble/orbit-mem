// 작업 탭: 머리(제목·상태·진행·프로젝트·마지막 갱신·상위 작업 길)와 본문(그래프 + 단계 상세 / 담당별).
// 그래프는 forma-ai FlowGraph, 담당별은 forma-ai AgentOrchestrator, 나머지는 forma-ui 부품으로 조립한다.
import {useCallback, useEffect, useMemo, useState} from 'react';
import type {PluginContext, TabProps} from '@onidot-labs/studio-plugin';
import {AgentOrchestrator, FlowGraph, type FlowGraphView} from '@onidot-labs/studio-plugin/ai';
import {Badge, Breadcrumbs, Button, EmptyState, Progress, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {errorMessage} from '../oni.ts';
import {WorkToolError} from '../work/contract.ts';
import {progressOf, workStateView} from '../work/display.ts';
import {agoText, toFlowGraph} from '../work/graph.ts';
import {ownerWorkers} from '../work/owners.ts';
import {summaryOf} from '../work/store.ts';
import type {Work} from '../work/types.ts';
import {StageDetail, type StageNav} from './stage-detail.tsx';
import {useNow, useWork} from './runtime.ts';

type Mode = 'flow' | 'owner';

export function workNav(ctx: PluginContext, selectStep: (id: string) => void): StageNav {
  return {
    selectStep,
    openWork: (id, title) => ctx.tabs.open({type: 'work', id, title: title ?? '작업', params: {id}}),
    openSession: (id, title) => ctx.tabs.open({type: 'session', id, title: title ?? '세션', params: {id}, beside: true}),
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

  if (!store) return <EmptyState title="Orbit을 켜는 중입니다" />;
  if (state.status === 'paused') return <EmptyState title="작업 기록을 읽지 않습니다" description={state.pause?.message} />;
  if (missing) return <EmptyState title="작업을 찾지 못했습니다" description="Orbit 기록에서 지워졌거나 다른 PC의 작업입니다." />;
  if (!work) {
    if (error) return <EmptyState title="작업을 읽지 못했습니다" description={error}><Button variant="secondary" size="sm" onClick={reload}>다시 시도</Button></EmptyState>;
    return <EmptyState title="작업을 불러오는 중입니다" />;
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
          <Breadcrumbs label="상위 작업 길" items={[
            {label: parent?.title ?? '상위 작업', onClick: () => nav.openWork(work.parent!.workId, parent?.title)},
            {label: work.title},
          ]} />
        )}
        <div className="orbit-row orbit-work-head-row">
          <h2 className="orbit-work-title" title={work.title}>{work.title}</h2>
          <Badge tone={status.tone}>{status.label}</Badge>
          <Progress label="단계 진행" done={progress.done} total={progress.total} size="sm" />
          {work.project && <span className="orbit-meta">프로젝트 <b>{work.project.label}</b></span>}
          <span className="orbit-meta" title={work.updatedAt}>마지막 갱신 {agoText(work.updatedAt, now)}</span>
          <span className="orbit-grow" />
          {pending && <Button variant="secondary" size="sm" onClick={() => void store.dismiss(work.id)}>목록에서 정리</Button>}
          <SegmentedControl label="보기" value={mode} onChange={v => setMode(v === 'owner' ? 'owner' : 'flow')}
            options={[{value: 'flow', label: '흐름'}, {value: 'owner', label: '담당별'}]} />
        </div>
      </header>
      {mode === 'flow' ? (
        <div className="orbit-work-graph">
          <FlowGraph
            label="작업 흐름"
            nodes={graph!.nodes}
            edges={graph!.edges}
            columns={graph!.columns}
            activeColumn={graph!.activeColumn}
            selectedId={selected}
            onSelect={select}
            view={view}
            onViewChange={setView}
            notice={noLines ? '선행이 하나도 기록되지 않았습니다. 같은 열은 함께 시작할 수 있는 단계입니다.' : undefined}
            asideLabel="단계 상세"
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
  const {state} = useWork();
  const {work, error, missing, reload} = useWorkDetail(workId);
  const now = useNow();
  const nav = useMemo(() => workNav(ctx, sid => ctx.tabs.open({type: 'work', id: workId, title: work?.title ?? '작업', params: {id: workId, step: sid}})), [ctx, workId, work?.title]);
  if (state.status === 'paused') return <EmptyState title="작업 기록을 읽지 않습니다" description={state.pause?.message} />;
  if (missing) return <EmptyState title="작업을 찾지 못했습니다" />;
  if (!work) {
    if (error) return <EmptyState title="작업을 읽지 못했습니다" description={error}><Button variant="secondary" size="sm" onClick={reload}>다시 시도</Button></EmptyState>;
    return <EmptyState title="작업을 불러오는 중입니다" />;
  }
  const step = work.steps.find(s => s.id === stepId);
  if (!step) return <EmptyState title="단계를 찾지 못했습니다" />;
  return (
    <div className="orbit-pane">
      <div className="orbit-row">
        <span className="orbit-meta">{work.title}</span>
        <Button variant="secondary" size="sm" onClick={() => nav.selectStep(step.id)}>작업 그래프에서 보기</Button>
      </div>
      <StageDetail ctx={ctx} work={work} step={step} now={now} works={state.works} sessions={state.sessions} nav={nav} />
    </div>
  );
}

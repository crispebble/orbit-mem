// 사이드바 「작업」 보기: 한 줄에 제목·진행·상태 칩·시작한 호스트. 기본은 열린 작업과 「끝남 · 확인 전」, 하위 작업은 상위 아래에 묶는다.
import {useState, type ReactNode} from 'react';
import {ListChecks} from 'lucide-react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {Badge, Button, StateView, InboxItem, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {groupWorks, progressOf, workStateView, type WorkNode} from '../work/display.ts';
import {hostLabel} from '../work/graph.ts';
import type {WorkSummary} from '../work/types.ts';
import {useWork} from './runtime.ts';
import {Paused} from './paused.tsx';

type Filter = 'default' | 'all';

export function WorksView({ctx}: ViewProps) {
  const {store, state} = useWork();
  const [filter, setFilter] = useState<Filter>('default');
  const [selected, setSelected] = useState<string>();

  if (!store) return <StateView variant="loading" title="Orbit을 켜는 중입니다" compact headingLevel={3} />;
  if (state.status === 'paused') {
    return <div className="orbit-side"><Paused store={store} state={state} title="작업 기록을 읽지 않습니다" compact /></div>;
  }
  if (state.status === 'error' && state.works.length === 0) {
    return <div className="orbit-side"><StateView variant="error" title="작업 기록을 읽지 못했습니다" description={`${state.error ?? ''} 잠시 뒤 다시 읽습니다.`} compact headingLevel={3} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.works.length === 0)) {
    return <div className="orbit-side"><StateView variant="loading" title="작업을 불러오는 중입니다" compact headingLevel={3} /></div>;
  }

  const visible = filter === 'default' ? store.visibleWorks() : state.works;
  const pending = new Set(state.pendingReview);
  const open = (w: WorkSummary) => {
    setSelected(w.id);
    ctx.tabs.open({type: 'work', id: w.id, title: w.title, params: {id: w.id}});
  };

  const row = (node: WorkNode<WorkSummary>, depth: number): ReactNode => {
    const w = node.work;
    const status = workStateView(w, pending.has(w.id));
    const p = progressOf(w);
    const blocked = w.state === 'open' && w.counts.blocked > 0;
    return (
      <div role="listitem" key={w.id} className="orbit-work-item" data-depth={depth > 0 ? Math.min(depth, 3) : undefined}>
        <InboxItem
          icon={<ListChecks size={14} aria-hidden="true" />}
          title={<span className="orbit-work-row-title"><span className="orbit-work-row-text">{w.title}</span><Badge tone={status.tone}>{status.label}</Badge></span>}
          subtitle={[
            p.total > 0 ? `${p.done}/${p.total}` : '단계 없음',
            blocked ? `막힘 ${w.counts.blocked}` : undefined,
            w.origin ? hostLabel(w.origin.host) : undefined,
            w.project?.label.split('/').pop(),
          ].filter(Boolean).join(' · ')}
          unread={pending.has(w.id)}
          current={selected === w.id}
          onOpen={() => open(w)}
          actions={pending.has(w.id) ? (
            <Button variant="ghost" size="sm" onClick={e => { e.stopPropagation(); void store.dismiss(w.id); }}>목록에서 정리</Button>
          ) : undefined}
        />
        {node.children.length > 0 && <div role="list" aria-label={`${w.title}의 하위 작업`}>{node.children.map(c => row(c, depth + 1))}</div>}
      </div>
    );
  };

  const tree = groupWorks(visible);
  const openCount = state.works.filter(w => w.state === 'open').length;
  return (
    <div className="orbit-side">
      <SegmentedControl
        label="작업 보기"
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'default')}
        options={[{value: 'default', label: `진행 ${openCount}`}, {value: 'all', label: '전체'}]}
      />
      {state.status === 'error' && <p className="orbit-alert" role="alert">갱신하지 못했습니다. 마지막으로 읽은 목록입니다. {state.error}</p>}
      {tree.length === 0 ? (
        <StateView
          title={filter === 'default' ? '진행 중인 작업이 없습니다' : '작업이 없습니다'}
          description={state.scope.kind === 'project' ? `${state.scope.name} 프로젝트 안의 작업만 보입니다.` : 'AI 호스트가 작업 목록을 만들면 여기에 나타납니다.'} compact headingLevel={3} />
      ) : (
        <div className="orbit-side-list" role="list" aria-label="작업 목록">{tree.map(n => row(n, 0))}</div>
      )}
    </div>
  );
}

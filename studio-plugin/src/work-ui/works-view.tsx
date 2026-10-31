// 사이드바 「작업」 보기: 한 줄에 제목·진행·상태 칩·시작한 호스트. 기본은 열린 작업과 「끝남 · 확인 전」, 하위 작업은 상위 아래에 묶는다.
import {useState, type ReactNode} from 'react';
import {ListChecks} from 'lucide-react';
import type {ViewProps} from '@onidot-labs/orbit-studio-plugin';
import {Badge, Button, StateView, InboxItem, SegmentedControl} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {workUiText} from '../i18n/messages/work.ts';
import {groupWorks, progressOf, workStateView, type WorkNode} from '../work/display.ts';
import {hostLabel} from '../work/graph.ts';
import type {WorkSummary} from '../work/types.ts';
import {useWork} from './runtime.ts';
import {Paused} from './paused.tsx';

type Filter = 'default' | 'all';

export function WorksView({ctx}: ViewProps) {
  const {store, state} = useWork();
  const u = useMessages(workUiText);
  const t = u.worksView;
  const [filter, setFilter] = useState<Filter>('default');
  const [selected, setSelected] = useState<string>();

  if (!store) return <StateView variant="loading" title={u.starting} compact headingLevel={3} />;
  if (state.status === 'paused') {
    return <div className="orbit-side"><Paused store={store} state={state} title={u.paused.workTitle} compact /></div>;
  }
  if (state.status === 'error' && state.works.length === 0) {
    return <div className="orbit-side"><StateView variant="error" title={t.readFailed} description={`${state.error ?? ''} ${t.retrySoon}`} compact headingLevel={3} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.works.length === 0)) {
    return <div className="orbit-side"><StateView variant="loading" title={t.loading} compact headingLevel={3} /></div>;
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
            p.total > 0 ? `${p.done}/${p.total}` : t.noSteps,
            blocked ? t.blocked(w.counts.blocked) : undefined,
            w.origin ? hostLabel(w.origin.host) : undefined,
            w.project?.label.split('/').pop(),
          ].filter(Boolean).join(' · ')}
          unread={pending.has(w.id)}
          current={selected === w.id}
          onOpen={() => open(w)}
          actions={pending.has(w.id) ? (
            <Button variant="ghost" size="sm" onClick={e => { e.stopPropagation(); void store.dismiss(w.id); }}>{t.dismiss}</Button>
          ) : undefined}
        />
        {node.children.length > 0 && <div role="list" aria-label={t.childrenOf(w.title)}>{node.children.map(c => row(c, depth + 1))}</div>}
      </div>
    );
  };

  const tree = groupWorks(visible);
  const openCount = state.works.filter(w => w.state === 'open').length;
  return (
    <div className="orbit-side">
      <SegmentedControl
        label={t.viewLabel}
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'default')}
        options={[{value: 'default', label: t.active(openCount)}, {value: 'all', label: t.all}]}
      />
      {state.status === 'error' && <p className="orbit-alert" role="alert">{t.refreshFailed} {state.error}</p>}
      {tree.length === 0 ? (
        <StateView
          title={filter === 'default' ? t.noActive : t.none}
          description={state.scope.kind === 'project' ? t.projectOnly(state.scope.name) : t.allHint} compact headingLevel={3} />
      ) : (
        <div className="orbit-side-list" role="list" aria-label={t.listLabel}>{tree.map(n => row(n, 0))}</div>
      )}
    </div>
  );
}

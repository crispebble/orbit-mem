// 사이드바 「제안」 보기: 대기 제안은 종류별 묶음과 묶음 승인·거절, 전체는 최근 것부터 한 줄씩.
import {useRef, useState} from 'react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {Button, StateView, InboxItem, Mark, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {groupProposalRows, toProposalRow, type ProposalGroup} from './model.ts';
import {decideGroup} from './group-decision.ts';
import {ProposalGroupList} from './parts/index.ts';
import {useOrbit} from './runtime.ts';

type Filter = 'pending' | 'all';

export function ProposalsView({ctx}: ViewProps) {
  const {store, state} = useOrbit();
  const [filter, setFilter] = useState<Filter>('pending');
  const [selected, setSelected] = useState<string>();
  const [busyKind, setBusyKind] = useState<string>();
  const deciding = useRef(false);

  if (!store) return <StateView variant="loading" title="Orbit을 켜는 중입니다" compact headingLevel={3} />;

  const retry = <Button variant="secondary" size="sm" onClick={() => void store.refresh()}>다시 시도</Button>;
  if (state.status === 'error' && state.proposals.length === 0) {
    return <div className="orbit-side"><StateView variant="error" title="Orbit에 연결하지 못했습니다" description={state.error} onRetry={() => void store.refresh()} compact headingLevel={3} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.proposals.length === 0)) {
    return <div className="orbit-side"><StateView variant="loading" title="제안을 불러오는 중입니다" compact headingLevel={3} /></div>;
  }

  const now = new Date();
  const all = [...state.proposals].sort((a, b) => b.createdAt - a.createdAt).map(p => toProposalRow(p, now));
  const pending = all.filter(r => r.pending);
  const rows = filter === 'pending' ? pending : all;
  const groups = groupProposalRows(pending);

  const open = (id: string, title: string) => {
    setSelected(id);
    ctx.tabs.open({type: 'proposal', id, title, params: {id}});
  };

  // 확인 창이 떠 있는 동안에도 다른 묶음 결정을 받지 않는다(state는 다음 그리기까지 늦다)
  async function runGroup(g: ProposalGroup, decision: 'approve' | 'reject') {
    if (!store || deciding.current) return;
    deciding.current = true;
    setBusyKind(g.kind);
    try {
      await decideGroup(ctx, store, g, decision);
    } finally {
      deciding.current = false;
      setBusyKind(undefined);
    }
  }

  return (
    <div className="orbit-side">
      <SegmentedControl
        label="제안 보기"
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'pending')}
        options={[{value: 'pending', label: `대기 ${pending.length}`}, {value: 'all', label: '전체'}]}
      />
      {state.status === 'error' && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">갱신하지 못했습니다. 마지막으로 불러온 목록입니다. {state.error}</p>
          {retry}
        </div>
      )}
      {rows.length === 0 ? (
        <StateView title={filter === 'pending' ? '대기 중인 제안이 없습니다' : '제안이 없습니다'} description="회고가 새 제안을 만들면 여기에 나타납니다." compact headingLevel={3} />
      ) : filter === 'pending' ? (
        <ProposalGroupList
          groups={groups.map(g => ({kind: g.kind, label: g.label, items: g.rows.map(r => ({id: r.id, title: r.title, subtitle: r.date, unread: true}))}))}
          selectedId={selected}
          busyKind={busyKind}
          decidable={state.status !== 'error'}
          onOpen={id => open(id, pending.find(r => r.id === id)?.title ?? id)}
          onDecideGroup={(kind, decision) => {
            const g = groups.find(x => x.kind === kind);
            if (g) void runGroup(g, decision);
          }}
        />
      ) : (
        <div className="orbit-side-list" role="list" aria-label="제안 목록">
          {rows.map(r => (
            <div role="listitem" key={r.id}>
              <InboxItem
                icon={<Mark size="sm">{r.kindLabel.slice(0, 1)}</Mark>}
                title={r.title}
                subtitle={`${r.kindLabel} · ${r.date}${r.pending ? '' : ` · ${r.status === 'approved' ? '승인함' : r.status === 'rejected' ? '거절함' : '처리됨'}`}`}
                unread={r.pending}
                current={selected === r.id}
                onOpen={() => open(r.id, r.title)}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

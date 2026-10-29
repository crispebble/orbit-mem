// 사이드바 「제안」 보기
import {useState} from 'react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {Button, StateView, InboxItem, Mark, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {toProposalRow} from './model.ts';
import {useOrbit} from './runtime.ts';

type Filter = 'pending' | 'all';

export function ProposalsView({ctx}: ViewProps) {
  const {store, state} = useOrbit();
  const [filter, setFilter] = useState<Filter>('pending');
  const [selected, setSelected] = useState<string>();

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
  const pendingCount = all.filter(r => r.pending).length;
  const rows = filter === 'pending' ? all.filter(r => r.pending) : all;

  return (
    <div className="orbit-side">
      <SegmentedControl
        label="제안 보기"
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'pending')}
        options={[{value: 'pending', label: `대기 ${pendingCount}`}, {value: 'all', label: '전체'}]}
      />
      {state.status === 'error' && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">갱신하지 못했습니다. 마지막으로 불러온 목록입니다. {state.error}</p>
          {retry}
        </div>
      )}
      {rows.length === 0 ? (
        <StateView title={filter === 'pending' ? '대기 중인 제안이 없습니다' : '제안이 없습니다'} description="회고가 새 제안을 만들면 여기에 나타납니다." compact headingLevel={3} />
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
                onOpen={() => {
                  setSelected(r.id);
                  ctx.tabs.open({type: 'proposal', id: r.id, title: r.title, params: {id: r.id}});
                }}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

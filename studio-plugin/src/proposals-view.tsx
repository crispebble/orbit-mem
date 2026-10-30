// 사이드바 「제안」 보기: 대기 제안은 종류별 묶음과 묶음 승인·거절, 전체는 최근 것부터 한 줄씩.
import {useState} from 'react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {Button, StateView, InboxItem, Mark, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {groupProposalRows, toProposalRow, type ProposalGroup} from './model.ts';
import {errorMessage} from './oni.ts';
import {ProposalGroupList} from './parts/index.ts';
import {decideManyArgs} from './requests.ts';
import {useOrbit} from './runtime.ts';

type Filter = 'pending' | 'all';

const decisionWord = {approve: '승인', reject: '거절'} as const;

export function ProposalsView({ctx}: ViewProps) {
  const {store, state} = useOrbit();
  const [filter, setFilter] = useState<Filter>('pending');
  const [selected, setSelected] = useState<string>();
  const [busyKind, setBusyKind] = useState<string>();

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

  async function decideGroup(g: ProposalGroup, decision: 'approve' | 'reject') {
    if (!store) return;
    const word = decisionWord[decision];
    const what = `${g.label} ${g.rows.length}건`;
    const body = decision === 'approve'
      ? `제안마다 승인한 것과 같습니다.${g.kind === 'apply_file' || g.kind === 'revert' ? ' 파일 반영·되돌리기는 파일을 바꿉니다.' : ''} 하나가 실패해도 나머지는 결정하고, 실패한 것은 이유와 함께 알려 드립니다.`
      : '제안마다 거절한 것과 같습니다. 후보 확정을 거절하면 그 후보는 숨겨집니다. 하나가 실패해도 나머지는 결정합니다.';
    const answer = await ctx.ui.ask({
      title: `${what}을 모두 ${word}할까요?`,
      body,
      actions: [{id: decision, label: `모두 ${word}`, variant: decision === 'approve' ? 'primary' : 'danger'}, {id: 'cancel', label: '취소', variant: 'ghost'}],
    });
    if (answer !== decision) return;
    setBusyKind(g.kind);
    try {
      const out = await store.decideMany(decideManyArgs(g.rows.map(r => r.id), decision));
      if (out.failed.length === 0) {
        ctx.notify({title: `${g.label} ${out.decided}건을 ${word}했습니다`, tone: 'success'});
      } else {
        for (const f of out.failed) ctx.log.warn(`묶음 ${word} 실패(${f.id}): ${f.error}`);
        ctx.notify({
          title: `${out.decided}건 ${word}, ${out.failed.length}건은 결정하지 못했습니다`,
          body: out.failed.map(f => `${g.rows.find(r => r.id === f.id)?.title ?? f.id}: ${f.error}`).join('\n'),
          tone: out.decided > 0 ? 'warning' : 'danger',
        });
      }
    } catch (e) {
      ctx.log.error(`묶음 ${word} 실패(${g.kind}): ${errorMessage(e)}`);
      ctx.notify({title: `${what}을 ${word}하지 못했습니다`, body: errorMessage(e), tone: 'danger'});
    } finally {
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
            if (g) void decideGroup(g, decision);
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

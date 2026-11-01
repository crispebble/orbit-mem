// 사이드바 「감사」 보기(옛 제안 보기): 자동 변경은 되돌리고, 충돌·반복 교정은 확인하고, 남은 대기 제안은 종류별로 결정한다.
import {useRef, useState} from 'react';
import type {ViewProps} from '@onidot-labs/orbit-studio-plugin';
import {Button, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from './i18n/index.ts';
import {auditText} from './i18n/messages/audit.ts';
import {commonText} from './i18n/messages/common.ts';
import {proposalText} from './i18n/messages/proposals.ts';
import {groupProposalRows, toAuditView, toProposalRow, type ProposalGroup} from './model.ts';
import {decideGroup} from './group-decision.ts';
import {dismissItem, resolveConflict, revertChange} from './audit-actions.ts';
import {AuditList} from './audit-list.tsx';
import {useOrbit} from './runtime.ts';

export function ProposalsView({ctx}: ViewProps) {
  const {store, state} = useOrbit();
  const [selected, setSelected] = useState<string>();
  const [busyKind, setBusyKind] = useState<string>();
  const [busyId, setBusyId] = useState<string>();
  const working = useRef(false);
  const t = useMessages(proposalText);
  const a = useMessages(auditText);
  const common = useMessages(commonText);

  if (!store) return <StateView variant="loading" title={common.startingOrbit} compact headingLevel={3} />;

  const retry = <Button variant="secondary" size="sm" onClick={() => void store.refresh()}>{common.retry}</Button>;
  if (state.status === 'error' && state.proposals.length === 0) {
    return <div className="orbit-side"><StateView variant="error" title={t.view.connectFailed} description={state.error} onRetry={() => void store.refresh()} retryLabel={common.retry} compact headingLevel={3} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.proposals.length === 0)) {
    return <div className="orbit-side"><StateView variant="loading" title={a.view.loading} compact headingLevel={3} /></div>;
  }

  const now = new Date();
  const pending = [...state.proposals].filter(p => p.status === 'pending').sort((x, y) => y.createdAt - x.createdAt).map(p => toProposalRow(p, now));
  const groups = groupProposalRows(pending);

  const openProposal = (id: string, title?: string) => {
    setSelected(id);
    ctx.tabs.open({type: 'proposal', id, title: title ?? pending.find(r => r.id === id)?.title ?? id, params: {id}});
  };
  const openMemory = (id: string, title: string) => ctx.tabs.open({type: 'memory', id, title, params: {id}});

  // 확인 창이 떠 있는 동안에도 다른 결정을 받지 않는다(state는 다음 그리기까지 늦다)
  async function exclusive(mark: () => void, unmark: () => void, run: () => Promise<unknown>) {
    if (working.current) return;
    working.current = true;
    mark();
    try { await run(); } finally { working.current = false; unmark(); }
  }
  const byId = (id: string, run: () => Promise<unknown>) => exclusive(() => setBusyId(id), () => setBusyId(undefined), run);
  const runGroup = (g: ProposalGroup, decision: 'approve' | 'reject') =>
    exclusive(() => setBusyKind(g.kind), () => setBusyKind(undefined), () => decideGroup(ctx, store, g, decision));

  return (
    <div className="orbit-side" aria-label={a.view.listLabel}>
      {state.status === 'error' && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">{a.view.refreshFailed} {state.error}</p>
          {retry}
        </div>
      )}
      {state.auditError && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">{a.view.auditFailed} {state.auditError}</p>
          {retry}
        </div>
      )}
      <AuditList
        audit={toAuditView(state.audit)}
        groups={groups.map(g => ({kind: g.kind, label: g.label, items: g.rows.map(r => ({id: r.id, title: r.title, subtitle: r.date, unread: true}))}))}
        selectedId={selected}
        busyKind={busyKind}
        busyId={busyId}
        actionable={state.status !== 'error'}
        now={now}
        onOpenChange={c => {
          const target = c.targets?.[0];
          if (target) openMemory(target, c.title);
          else openProposal(c.proposalId || c.ledgerId, c.title);
        }}
        onOpenConflict={c => openMemory(c.memoryId, c.title)}
        onOpenProposal={id => openProposal(id)}
        onOpenMemory={openMemory}
        onRevert={c => void byId(c.ledgerId, () => revertChange(ctx, store, c))}
        onConfirm={c => void byId(c.id, () => resolveConflict(ctx, store, c, 'confirm'))}
        onStale={c => void byId(c.id, () => resolveConflict(ctx, store, c, 'stale'))}
        onDismiss={id => void byId(id, () => dismissItem(ctx, store, id))}
        onDecideGroup={(kind, decision) => {
          const g = groups.find(x => x.kind === kind);
          if (g) void runGroup(g, decision);
        }}
      />
    </div>
  );
}

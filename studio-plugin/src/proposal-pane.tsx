// 제안 상세: proposal 탭과 받은 일 상세가 함께 쓴다.
import {useEffect, useMemo, useState} from 'react';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {StateView, Label} from '@onidot-labs/orbit-studio-plugin/ui';
import {useLocale, useMessages} from './i18n/index.ts';
import {commonText} from './i18n/messages/common.ts';
import {proposalText} from './i18n/messages/proposals.ts';
import {ProposalDetail} from './parts/index.ts';
import {errorMessage} from './orb.ts';
import {formatDate, proposalStatusLabel, toProposal, type OrbMemory} from './model.ts';
import {approvalToDecideArgs, decideArgs} from './requests.ts';
import {useOrbit} from './runtime.ts';

export function ProposalPane({ctx, proposalId}: {ctx: PluginContext; proposalId: string}) {
  const {store, state} = useOrbit();
  const locale = useLocale();
  const t = useMessages(proposalText);
  const common = useMessages(commonText);
  const [related, setRelated] = useState<OrbMemory[]>([]);
  const proposal = state.proposals.find(p => p.id === proposalId);
  const memoryKey = proposal?.memoryIds?.join(',') ?? '';

  // 근거(대상 기억의 출처)는 제안이 바뀔 때만 읽는다. 60초 갱신마다 다시 읽지 않는다.
  useEffect(() => {
    if (!store || !memoryKey) { setRelated([]); return; }
    let live = true;
    void store.getMemories(memoryKey.split(',')).then(m => { if (live) setRelated(m); });
    return () => { live = false; };
  }, [store, memoryKey]);

  const view = useMemo(
    // 날짜·근거 이름이 언어를 따르므로 언어가 바뀌면 다시 만든다
    () => (locale && proposal ? toProposal(proposal, related) : undefined),
    [proposal, related, locale],
  );

  if (!store) return <StateView variant="loading" title={common.startingOrbit} />;
  if (!proposal || !view) {
    if (state.status === 'error') {
      return <StateView variant="error" title={t.view.connectFailed} description={state.error} onRetry={() => void store.refresh()} retryLabel={common.retry} />;
    }
    if (state.status === 'idle' || state.status === 'loading') return <StateView variant="loading" title={t.view.loading} />;
    return <StateView title={t.pane.notFound} description={t.pane.notFoundHint} />;
  }

  const pending = proposal.status === 'pending';
  const fail = (what: string, e: unknown): never => {
    ctx.log.error(`${what} failed (${proposal.id}): ${errorMessage(e)}`);
    throw e;
  };

  return (
    <div className="orbit-pane">
      {!pending && (
        <div className="orbit-row" role="status">
          <Label tone={proposal.status === 'approved' ? 'success' : undefined}>{proposalStatusLabel(proposal.status)}</Label>
          {proposal.decidedAt ? <span>{formatDate(proposal.decidedAt)}</span> : null}
          {proposal.note ? <span>{t.pane.reason} {proposal.note}</span> : null}
        </div>
      )}
      <ProposalDetail
        proposal={view}
        onApprove={pending ? async (_id, change) => {
          try { await store.decide(approvalToDecideArgs(proposal, change)); } catch (e) { fail('Approve', e); }
        } : undefined}
        onDeny={pending ? async (_id, denial) => {
          try { await store.decide(decideArgs(proposal.id, 'reject', {note: denial.note})); } catch (e) { fail('Reject', e); }
        } : undefined}
        denyReason="optional"
        availability={state.status === 'error' ? 'offline' : 'ready'}
        error={state.error}
      />
    </div>
  );
}

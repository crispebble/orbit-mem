// 제안 상세: proposal 탭과 받은 일 상세가 함께 쓴다.
import {useEffect, useMemo, useState} from 'react';
import type {PluginContext} from '@onidot-labs/studio-plugin';
import {Button, StateView, Label} from '@onidot-labs/studio-plugin/ui';
import {ProposalDetail} from './parts/index.ts';
import {errorMessage} from './oni.ts';
import {formatDate, proposalStatusLabels, toProposal, type OniMemory} from './model.ts';
import {approvalToDecideArgs, decideArgs} from './requests.ts';
import {useOrbit} from './runtime.ts';

export function ProposalPane({ctx, proposalId}: {ctx: PluginContext; proposalId: string}) {
  const {store, state} = useOrbit();
  const [related, setRelated] = useState<OniMemory[]>([]);
  const proposal = state.proposals.find(p => p.id === proposalId);
  const memoryKey = proposal?.memoryIds?.join(',') ?? '';

  // 근거(대상 기억의 출처)는 제안이 바뀔 때만 읽는다. 60초 갱신마다 다시 읽지 않는다.
  useEffect(() => {
    if (!store || !memoryKey) { setRelated([]); return; }
    let live = true;
    void store.getMemories(memoryKey.split(',')).then(m => { if (live) setRelated(m); });
    return () => { live = false; };
  }, [store, memoryKey]);

  const view = useMemo(() => (proposal ? toProposal(proposal, related) : undefined), [proposal, related]);

  if (!store) return <StateView variant="loading" title="Orbit을 켜는 중입니다" />;
  if (!proposal || !view) {
    if (state.status === 'error') {
      return <StateView variant="error" title="Orbit에 연결하지 못했습니다" description={state.error} onRetry={() => void store.refresh()} />;
    }
    if (state.status === 'idle' || state.status === 'loading') return <StateView variant="loading" title="제안을 불러오는 중입니다" />;
    return <StateView title="제안을 찾지 못했습니다" description="이미 정리됐거나 oni에 없는 제안입니다." />;
  }

  const pending = proposal.status === 'pending';
  const fail = (what: string, e: unknown): never => {
    ctx.log.error(`${what} 실패(${proposal.id}): ${errorMessage(e)}`);
    throw e;
  };

  return (
    <div className="orbit-pane">
      {!pending && (
        <div className="orbit-row" role="status">
          <Label tone={proposal.status === 'approved' ? 'success' : undefined}>{proposalStatusLabels[proposal.status] ?? proposal.status}</Label>
          {proposal.decidedAt ? <span>{formatDate(proposal.decidedAt)}</span> : null}
          {proposal.note ? <span>사유: {proposal.note}</span> : null}
        </div>
      )}
      <ProposalDetail
        proposal={view}
        onApprove={pending ? async (_id, change) => {
          try { await store.decide(approvalToDecideArgs(proposal, change)); } catch (e) { fail('승인', e); }
        } : undefined}
        onDeny={pending ? async (_id, denial) => {
          try { await store.decide(decideArgs(proposal.id, 'reject', {note: denial.note})); } catch (e) { fail('거절', e); }
        } : undefined}
        denyReason="optional"
        availability={state.status === 'error' ? 'offline' : 'ready'}
        error={state.error}
      />
    </div>
  );
}

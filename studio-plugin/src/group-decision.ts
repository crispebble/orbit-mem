// 묶음 결정 흐름: 확인 창 → proposal_decide ids → 결과 알림. React에 기대지 않아 ctx만으로 시험한다.
import type {PluginContext} from '@onidot-labs/studio-plugin';
import {pick} from './i18n/index.ts';
import {proposalText} from './i18n/messages/proposals.ts';
import {errorMessage} from './orb.ts';
import {decideManyArgs} from './requests.ts';
import type {OrbitStore} from './store.ts';

export interface DecidableGroup {
  kind: string;
  label: string;
  rows: readonly {id: string; title: string}[];
}

/** 묶음의 제안을 모두 승인·거절한다. 사용자가 확인 창에서 고르지 않으면 'cancelled'. */
export async function decideGroup(
  ctx: PluginContext,
  store: Pick<OrbitStore, 'decideMany' | 'refresh'>,
  g: DecidableGroup,
  decision: 'approve' | 'reject',
): Promise<'cancelled' | 'done' | 'failed'> {
  const approve = decision === 'approve';
  const t = pick(proposalText).decision;
  const count = g.rows.length;
  const body = approve ? t.approveBody(g.kind === 'apply_file' || g.kind === 'revert') : t.rejectBody;
  const answer = await ctx.ui.ask({
    title: approve ? t.approveTitle(g.label, count) : t.rejectTitle(g.label, count),
    body,
    actions: [
      {id: decision, label: approve ? pick(proposalText).groups.approveAll : pick(proposalText).groups.rejectAll, variant: approve ? 'primary' : 'danger'},
      {id: 'cancel', label: t.cancel, variant: 'ghost'},
    ],
  });
  if (answer !== decision) return 'cancelled';
  try {
    const out = await store.decideMany(decideManyArgs(g.rows.map(r => r.id), decision));
    // 결정을 기다리는 사이 언어가 바뀔 수 있어 알림 글은 지금 언어로 다시 읽는다
    const after = pick(proposalText).decision;
    if (out.failed.length === 0) {
      ctx.notify({title: approve ? after.approved(g.label, out.decided) : after.rejected(g.label, out.decided), tone: 'success'});
      return 'done';
    }
    for (const f of out.failed) ctx.log.warn(`Group ${decision} failed (${f.id}): ${f.error}`);
    ctx.notify({
      title: approve ? after.partialApproved(out.decided, out.failed.length) : after.partialRejected(out.decided, out.failed.length),
      body: out.failed.map(f => `${g.rows.find(r => r.id === f.id)?.title ?? f.id}: ${f.error}`).join('\n'),
      tone: out.decided > 0 ? 'warning' : 'danger',
    });
    return 'done';
  } catch (e) {
    ctx.log.error(`Group ${decision} failed (${g.kind}): ${errorMessage(e)}`);
    const after = pick(proposalText).decision;
    ctx.notify({title: approve ? after.approveFailed(g.label, count) : after.rejectFailed(g.label, count), body: errorMessage(e), tone: 'danger'});
    // orb가 결정한 뒤 응답만 늦었을 수 있다: 목록을 다시 읽는다
    await store.refresh();
    return 'failed';
  }
}

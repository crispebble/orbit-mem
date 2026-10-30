// 묶음 결정 흐름: 확인 창 → proposal_decide ids → 결과 알림. React에 기대지 않아 ctx만으로 시험한다.
import type {PluginContext} from '@onidot-labs/studio-plugin';
import {errorMessage} from './oni.ts';
import {decideManyArgs} from './requests.ts';
import type {OrbitStore} from './store.ts';

export interface DecidableGroup {
  kind: string;
  label: string;
  rows: readonly {id: string; title: string}[];
}

const decisionWord = {approve: '승인', reject: '거절'} as const;

/** 묶음의 제안을 모두 승인·거절한다. 사용자가 확인 창에서 고르지 않으면 'cancelled'. */
export async function decideGroup(
  ctx: PluginContext,
  store: Pick<OrbitStore, 'decideMany' | 'refresh'>,
  g: DecidableGroup,
  decision: 'approve' | 'reject',
): Promise<'cancelled' | 'done' | 'failed'> {
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
  if (answer !== decision) return 'cancelled';
  try {
    const out = await store.decideMany(decideManyArgs(g.rows.map(r => r.id), decision));
    if (out.failed.length === 0) {
      ctx.notify({title: `${g.label} ${out.decided}건을 ${word}했습니다`, tone: 'success'});
      return 'done';
    }
    for (const f of out.failed) ctx.log.warn(`묶음 ${word} 실패(${f.id}): ${f.error}`);
    ctx.notify({
      title: `${out.decided}건 ${word}, ${out.failed.length}건은 결정하지 못했습니다`,
      body: out.failed.map(f => `${g.rows.find(r => r.id === f.id)?.title ?? f.id}: ${f.error}`).join('\n'),
      tone: out.decided > 0 ? 'warning' : 'danger',
    });
    return 'done';
  } catch (e) {
    ctx.log.error(`묶음 ${word} 실패(${g.kind}): ${errorMessage(e)}`);
    ctx.notify({title: `${what}을 ${word}하지 못했습니다`, body: errorMessage(e), tone: 'danger'});
    // oni가 결정한 뒤 응답만 늦었을 수 있다: 목록을 다시 읽는다
    await store.refresh();
    return 'failed';
  }
}

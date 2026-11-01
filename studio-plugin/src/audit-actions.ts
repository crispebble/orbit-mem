// 감사 목록 단추의 흐름: 확인 창 → 도구 호출 → 알림. React에 기대지 않아 ctx만으로 시험한다.
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {pick} from './i18n/index.ts';
import {auditText} from './i18n/messages/audit.ts';
import type {OrbAuditChange, OrbAuditConflict} from './model.ts';
import {errorMessage} from './orb.ts';
import type {OrbitStore} from './store.ts';

export type ActionResult = 'cancelled' | 'done' | 'failed';

async function ask(ctx: PluginContext, title: string, body: string, id: string, label: string, variant: 'primary' | 'danger'): Promise<boolean> {
  const t = pick(auditText).dialog;
  const answer = await ctx.ui.ask({
    title,
    body,
    actions: [{id, label, variant}, {id: 'cancel', label: t.cancel, variant: 'ghost'}],
  });
  return answer === id;
}

/** 자동 변경을 되돌린다. 확인 창에서 고르지 않으면 'cancelled'. */
export async function revertChange(ctx: PluginContext, store: Pick<OrbitStore, 'revert'>, c: OrbAuditChange): Promise<ActionResult> {
  const t = pick(auditText);
  if (!(await ask(ctx, t.dialog.revertTitle(c.title), t.dialog.revertBody, 'revert', t.actions.revert, 'danger'))) return 'cancelled';
  try {
    await store.revert(c.proposalId || c.ledgerId);
    ctx.notify({title: pick(auditText).notify.reverted(c.title), tone: 'success'});
    return 'done';
  } catch (e) {
    ctx.log.error(`Revert failed (${c.proposalId || c.ledgerId}): ${errorMessage(e)}`);
    ctx.notify({title: pick(auditText).notify.revertFailed(c.title), body: errorMessage(e), tone: 'danger'});
    return 'failed';
  }
}

/** 어긋난 후보를 그래도 확정하거나 낡음으로 표시한다(사람 actor 연결이라 허용된다). */
export async function resolveConflict(
  ctx: PluginContext,
  store: Pick<OrbitStore, 'updateMemory' | 'refresh'>,
  c: OrbAuditConflict,
  action: 'confirm' | 'stale',
): Promise<ActionResult> {
  const t = pick(auditText);
  const confirm = action === 'confirm';
  const okAsk = confirm
    ? await ask(ctx, t.dialog.confirmTitle(c.title), t.dialog.confirmBody, 'confirm', t.actions.confirmAnyway, 'primary')
    : await ask(ctx, t.dialog.staleTitle(c.title), t.dialog.staleBody, 'stale', t.actions.markStale, 'danger');
  if (!okAsk) return 'cancelled';
  try {
    await store.updateMemory([{id: c.memoryId, action}]);
    await store.refresh();
    const after = pick(auditText).notify;
    ctx.notify({title: confirm ? after.confirmed(c.title) : after.staled(c.title), tone: 'success'});
    return 'done';
  } catch (e) {
    ctx.log.error(`Conflict ${action} failed (${c.memoryId}): ${errorMessage(e)}`);
    ctx.notify({title: pick(auditText).notify.actionFailed, body: errorMessage(e), tone: 'danger'});
    await store.refresh();
    return 'failed';
  }
}

/** 어긋남·반복 교정을 확인함으로 닫는다(확인 창 없음: 기억은 바뀌지 않는다). */
export async function dismissItem(ctx: PluginContext, store: Pick<OrbitStore, 'dismiss'>, id: string): Promise<ActionResult> {
  try {
    await store.dismiss(id);
    return 'done';
  } catch (e) {
    ctx.log.error(`Dismiss failed (${id}): ${errorMessage(e)}`);
    ctx.notify({title: pick(auditText).notify.actionFailed, body: errorMessage(e), tone: 'danger'});
    return 'failed';
  }
}

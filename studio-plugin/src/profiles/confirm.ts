// ctx.ui.ask로 하는 확인 창(키 지우기·프로필 삭제).
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';

export async function askConfirm(ctx: PluginContext, title: string, action: string, cancel: string): Promise<boolean> {
  const id = await ctx.ui.ask({title, actions: [{id: 'cancel', label: cancel, variant: 'secondary'}, {id: 'ok', label: action, variant: 'danger'}]});
  return id === 'ok';
}

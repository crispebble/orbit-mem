// 다른 화면(작업 상세의 「대화 열기」, 홈의 「새 일 시키기」, 결정 카드)에서 작업 대화 창을 연다.
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {requestChat, type ChatTarget} from './store.ts';

export type {ChatTarget};

/** 옛 Studio(API 0.1.27 전)에는 ctx.toolWindows가 없다: 대상만 남기고 열지 못한다. 열었으면 true. */
export function openChat(ctx: PluginContext, target: ChatTarget): boolean {
  requestChat(target);
  const windows = (ctx as PluginContext & {toolWindows?: {show(toolId: string): boolean}}).toolWindows;
  return windows?.show('chat') ?? false;
}

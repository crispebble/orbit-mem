import type {InboxItem, McpResult, PluginContext} from '@onidot-labs/studio-plugin';

export interface Call {tool: string; args?: Record<string, unknown>}

/** 시험용 가짜 ctx. mcp 응답은 도구 이름별 함수로 정한다. */
export function fakeCtx(handlers: Record<string, (args?: Record<string, unknown>) => McpResult | Promise<McpResult>>) {
  const calls: Call[] = [];
  const badges: (number | string | null)[] = [];
  const inbox: InboxItem[][] = [];
  const notices: {title: string; body?: string; tone?: string}[] = [];
  const logs: {level: string; message: string}[] = [];
  let actionHandler: ((item: InboxItem, actionId: string) => void | Promise<void>) | undefined;
  let actionDisposed = false;
  const ctx = {
    pluginId: 'orbit',
    apiVersion: '0.1.0',
    app: {setBadge: (_: string, b: number | string | null) => void badges.push(b), show: () => {}},
    tabs: {open: () => {}, setTitle: () => {}, setModified: () => {}, close: () => {}},
    storage: {get: async () => undefined, set: async () => {}, delete: async () => {}},
    inbox: {
      set: (items: InboxItem[]) => void inbox.push(items),
      onAction: (h: typeof actionHandler) => {
        actionHandler = h;
        return () => {
          actionDisposed = true;
        };
      },
    },
    notify: (n: {title: string; body?: string; tone?: string}) => void notices.push(n),
    mcp: {
      async call(_id: string, tool: string, args?: Record<string, unknown>) {
        calls.push({tool, args});
        const h = handlers[tool];
        if (!h) throw new Error(`연결 없음: ${tool}`);
        return h(args);
      },
    },
    log: {
      info: (message: string) => void logs.push({level: 'info', message}),
      warn: (message: string) => void logs.push({level: 'warn', message}),
      error: (message: string) => void logs.push({level: 'error', message}),
    },
  } as unknown as PluginContext;
  return {
    ctx, calls, badges, inbox, notices, logs,
    fireAction: (item: InboxItem, id: string) => actionHandler?.(item, id),
    get actionDisposed() { return actionDisposed; },
  };
}

export const ok = (structured: unknown): McpResult => ({isError: false, structured, text: ''});
export const fail = (text: string): McpResult => ({isError: true, text});

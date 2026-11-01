// 시험용 최소 MCP stdio 클라이언트: 실제 orb를 자식으로 띄워 WorkCtx.mcp.call처럼 부른다.
import {spawn, type ChildProcessWithoutNullStreams} from 'node:child_process';
import type {McpResultLike, WorkCtx} from '../src/work/types.ts';

/** 최소 MCP stdio 클라이언트(JSON 줄 단위). 자식은 임시 HOME·ORBIT_HOME으로만 돈다. */
export function startMcp(bin: string, args: string[], home: string) {
  const child: ChildProcessWithoutNullStreams = spawn(bin, args, {env: {...process.env, HOME: home, ORBIT_HOME: home}, cwd: home});
  let id = 0;
  const pending = new Map<number, (v: {result?: Record<string, unknown>; error?: {message: string}}) => void>();
  let buf = '';
  child.stdout.on('data', (d: Buffer) => {
    buf += d.toString();
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      const msg = JSON.parse(line) as {id?: number; result?: Record<string, unknown>; error?: {message: string}};
      if (msg.id !== undefined) pending.get(msg.id)?.(msg);
    }
  });
  const rpc = (method: string, params: unknown) => new Promise<{result?: Record<string, unknown>; error?: {message: string}}>(resolve => {
    const n = ++id;
    pending.set(n, resolve);
    child.stdin.write(`${JSON.stringify({jsonrpc: '2.0', id: n, method, params})}\n`);
  });
  const ready = (async () => {
    await rpc('initialize', {protocolVersion: '2025-06-18', capabilities: {}, clientInfo: {name: 'orbit-plugin-test', version: '0'}});
    child.stdin.write(`${JSON.stringify({jsonrpc: '2.0', method: 'notifications/initialized'})}\n`);
  })();
  const ctx = {
    mcp: {
      async call(_c: string, tool: string, args?: Record<string, unknown>): Promise<McpResultLike> {
        await ready;
        const r = await rpc('tools/call', {name: tool, arguments: args ?? {}});
        if (r.error) throw new Error(r.error.message);
        const res = r.result as {isError?: boolean; structuredContent?: unknown; content?: {text?: string}[]};
        return {isError: res.isError === true, text: res.content?.[0]?.text ?? '', structured: res.structuredContent};
      },
    },
  } as unknown as WorkCtx;
  return {ctx, stop: () => child.kill()};
}

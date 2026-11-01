// 작업 데이터 층 시험용 가짜 ctx와 예시 읽기. 예시는 test/work-examples(계약 v1 examples의 복사본)에서 읽는다.
import {readFileSync} from 'node:fs';
import type {McpResultLike, StudioScope, WorkCtx} from '../src/work/types.ts';

export interface Example {
  tool: string;
  args: Record<string, unknown>;
  isError?: boolean;
  result: Record<string, unknown>;
}

export function example(name: string): Example {
  return JSON.parse(readFileSync(new URL(`./work-examples/${name}.json`, import.meta.url), 'utf8')) as Example;
}

/** 예시를 도구 결과로 */
export function asResult(ex: Example): McpResultLike {
  return {isError: ex.isError === true, text: '', structured: ex.result};
}

export interface CallRecord {
  /** 부른 연결 id */
  connection?: string;
  tool: string;
  args?: Record<string, unknown>;
  options?: {timeoutMs?: number; signal?: AbortSignal};
}

export type Handler = (args: Record<string, unknown> | undefined, options: CallRecord['options']) => McpResultLike | Promise<McpResultLike>;

export function fakeWorkCtx(handlers: Record<string, Handler>, initialScope: StudioScope = {kind: 'all'}) {
  const calls: CallRecord[] = [];
  const logs: {level: string; message: string}[] = [];
  const storage = new Map<string, unknown>();
  let scope = initialScope;
  const scopeListeners = new Set<(s: StudioScope) => void>();
  const ctx: WorkCtx = {
    mcp: {
      async call<T>(id: string, tool: string, args?: Record<string, unknown>, options?: CallRecord['options']) {
        calls.push({connection: id, tool, args, options});
        const h = handlers[tool];
        if (!h) throw new Error(`연결 없음: ${tool}`);
        return (await h(args, options)) as McpResultLike<T>;
      },
    },
    scope: {
      current: () => scope,
      onChange: h => {
        scopeListeners.add(h);
        return () => scopeListeners.delete(h);
      },
    },
    storage: {
      get: async <T>(key: string) => storage.get(key) as T | undefined,
      set: async (key: string, value: unknown) => void storage.set(key, value),
    },
    log: {
      info: m => void logs.push({level: 'info', message: m}),
      warn: m => void logs.push({level: 'warn', message: m}),
      error: m => void logs.push({level: 'error', message: m}),
    },
  };
  return {
    ctx, calls, logs, storage,
    setScope(s: StudioScope) {
      scope = s;
      scopeListeners.forEach(l => l(s));
    },
  };
}

/** 조건이 될 때까지 마이크로태스크를 돌린다 */
export async function until(cond: () => boolean, label = '조건'): Promise<void> {
  for (let i = 0; i < 500; i++) {
    if (cond()) return;
    await new Promise(r => setImmediate(r));
  }
  throw new Error(`${label}이(가) 되지 않았습니다`);
}

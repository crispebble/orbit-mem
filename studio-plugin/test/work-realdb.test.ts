// 실제 DB 사본 시험: 같은 work.db 사본을 새 연결(`ORB_BIN work mcp`)과 옛 연결(`ORB_OLD mcp --reader`)로 읽어
// 홈 작업 지도 항목(homeItems)·작업 상세 요약·결정 카드 목록이 같고 비어 있지 않은지 본다.
// WORK_DB_SAMPLE(work.db 경로; 같은 폴더의 -wal·-shm도 함께 복사한다)·ORB_BIN·ORB_OLD가 모두 있을 때만 돈다.
// 사본은 항상 임시 ORBIT_HOME으로 복사해서 쓰고, 원본은 열지 않는다. 자식에도 임시 HOME·ORBIT_HOME을 준다.
import assert from 'node:assert/strict';
import {copyFileSync, existsSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {describe, it} from 'node:test';
import {orbDecisionSource} from '../src/decision/source.ts';
import {homeItems} from '../src/work/provider.ts';
import {createWorkStore} from '../src/work/store.ts';
import type {WorkCtx} from '../src/work/types.ts';
import {startMcp} from './mcp-stdio.ts';

const {WORK_DB_SAMPLE, ORB_BIN, ORB_OLD} = process.env;
const enabled = !!(WORK_DB_SAMPLE && ORB_BIN && ORB_OLD && existsSync(WORK_DB_SAMPLE));
const NOW = Date.parse('2026-11-01T00:00:00Z');

function homeWithSample(): string {
  const home = mkdtempSync(join(tmpdir(), 'orbit-realdb-'));
  for (const suffix of ['', '-wal', '-shm']) {
    if (existsSync(`${WORK_DB_SAMPLE}${suffix}`)) copyFileSync(`${WORK_DB_SAMPLE}${suffix}`, join(home, `work.db${suffix}`));
  }
  return home;
}

async function snapshot(bin: string, args: string[]) {
  const home = homeWithSample();
  const mcp = startMcp(bin, args, home);
  const ctx: WorkCtx = {
    mcp: mcp.ctx.mcp,
    scope: {current: () => ({kind: 'all'}), onChange: () => () => {}},
    storage: {get: async () => undefined, set: async () => {}},
    log: {info: () => {}, warn: () => {}, error: () => {}},
  };
  const store = createWorkStore(ctx);
  try {
    await store.start();
    for (let i = 0; i < 600 && store.getState().status === 'loading'; i++) await new Promise(r => setTimeout(r, 50));
    const state = store.getState();
    assert.equal(state.status, 'ready', state.error);
    // 지도가 여는 모든 작업의 단계를 읽는다(모양이 같은지 본다)
    await Promise.all(state.works.map(w => store.loadDetail(w.id)));
    const full = store.getState();
    const source = orbDecisionSource(ctx);
    const open = await source.list({state: ['open']});
    const closed = await source.list({state: ['answered', 'withdrawn', 'expired']});
    return {
      works: full.works,
      sessions: full.sessions,
      items: homeItems(full, NOW),
      details: full.details,
      decisions: {open, closed},
    };
  } finally {
    store.dispose();
    mcp.stop();
    rmSync(home, {recursive: true, force: true});
  }
}

describe('실제 DB 사본: 새 연결과 옛 연결의 작업 지도·결정 카드', {skip: enabled ? false : 'WORK_DB_SAMPLE·ORB_BIN·ORB_OLD가 있어야 돈다'}, () => {
  it('작업 지도 항목과 결정 카드 목록이 같고 비어 있지 않다', async () => {
    const next = await snapshot(ORB_BIN!, ['work', 'mcp']);
    const old = await snapshot(ORB_OLD!, ['mcp', '--reader']);
    assert.ok(next.works.length > 0, '작업이 비어 있지 않다');
    assert.ok(next.items.length > 0, '지도 항목이 비어 있지 않다');
    assert.deepEqual(next.items, old.items);
    assert.deepEqual(next.works, old.works);
    assert.deepEqual(next.sessions, old.sessions);
    assert.deepEqual(next.details, old.details);
    assert.deepEqual(next.decisions, old.decisions);
    const steps = Object.values(next.details).reduce((n, w) => n + w.steps.length, 0);
    console.log(`비교: 작업 ${next.works.length}, 지도 항목 ${next.items.length}, 세션 ${next.sessions.length}, 상세 ${Object.keys(next.details).length}(단계 ${steps}), 결정 카드 열림 ${next.decisions.open.items?.length ?? '?'}·닫힘 ${next.decisions.closed.items?.length ?? '?'}`);
  });
});

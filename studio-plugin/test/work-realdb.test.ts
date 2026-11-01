// 실제 DB 사본 시험: work.db 사본을 `work` 연결(`ORB_BIN work mcp`)로 읽어 홈 작업 지도(homeItems)가
// OpenOrbit 작업(work.db에서 host가 orbit-pi인 작업, 또는 목록 종류 orbit-pi-plan)만 올리는지 센다.
// MEM_BIN(선택)이 있으면 memory.db 사본(MEMORY_DB_SAMPLE)을 그 실행 파일의 `mcp --reader`(`mem` 연결)로 읽어 memory_graph가 오는지 본다.
// 실행 파일 바꾸기: ORB_BIN = `work` 연결 명령(새 `orb` 또는 `orb-dev` 셸 스크립트; 인자는 `work mcp`), MEM_BIN = `mem` 연결 명령(`orbit-mem`; 인자는 `mcp --reader`).
// WORK_DB_SAMPLE(work.db 경로; 같은 폴더의 -wal·-shm도 함께 복사한다)·ORB_BIN이 있을 때만 돈다.
// 사본은 항상 임시 ORBIT_HOME으로 복사해서 쓰고, 원본은 열지 않는다. 자식에도 임시 HOME·ORBIT_HOME을 준다.
import assert from 'node:assert/strict';
import {DatabaseSync} from 'node:sqlite';
import {copyFileSync, existsSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {describe, it} from 'node:test';
import {memoryGraph} from '../src/memory-graph/client.ts';
import {orbDecisionSource} from '../src/decision/source.ts';
import {homeItems} from '../src/work/provider.ts';
import {createWorkStore} from '../src/work/store.ts';
import type {WorkCtx} from '../src/work/types.ts';
import {startMcp} from './mcp-stdio.ts';

const {WORK_DB_SAMPLE, ORB_BIN, MEM_BIN, MEMORY_DB_SAMPLE} = process.env;
const enabled = !!(WORK_DB_SAMPLE && ORB_BIN && existsSync(WORK_DB_SAMPLE));
const NOW = Date.parse('2026-11-01T00:00:00Z');

function homeWithSample(): string {
  const home = mkdtempSync(join(tmpdir(), 'orbit-realdb-'));
  for (const suffix of ['', '-wal', '-shm']) {
    if (existsSync(`${WORK_DB_SAMPLE}${suffix}`)) copyFileSync(`${WORK_DB_SAMPLE}${suffix}`, join(home, `work.db${suffix}`));
  }
  return home;
}

/** work.db 사본에서 OpenOrbit 작업 수와 전체 작업 수를 센다(MCP를 거치지 않는 독립 계산) */
function countOpenOrbit(): {all: number; openOrbit: number} {
  const home = homeWithSample();
  try {
    const db = new DatabaseSync(join(home, 'work.db'));
    const one = (sql: string) => Number((db.prepare(sql).get() as {n: number}).n);
    const out = {all: one('select count(*) n from works'), openOrbit: one("select count(*) n from works where host = 'orbit-pi' or list_kind = 'orbit-pi-plan'")};
    db.close();
    return out;
  } finally {
    rmSync(home, {recursive: true, force: true});
  }
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

describe('실제 DB 사본: 홈 작업 지도는 OpenOrbit 작업만', {skip: enabled ? false : 'WORK_DB_SAMPLE·ORB_BIN이 있어야 돈다'}, () => {
  it('지도 항목 수가 work.db의 OpenOrbit 작업 수와 같고 세션 노드가 없다', async () => {
    const count = countOpenOrbit();
    const next = await snapshot(ORB_BIN!, ['work', 'mcp']);
    assert.equal(next.items.length, count.openOrbit, '지도 항목 수 = OpenOrbit 작업 수');
    assert.ok(next.items.every(i => i.kind === 'work'));
    const mapped = new Set(next.items.map(i => i.id));
    assert.ok(next.works.filter(w => w.origin?.host !== 'orbit-pi' && w.origin?.list?.kind !== 'orbit-pi-plan').every(w => !mapped.has(w.id)));
    console.log(`지도: 전체 작업 ${count.all}, OpenOrbit 작업 ${count.openOrbit}, 지도 항목 ${next.items.length}, 세션 ${next.sessions.length}, 결정 카드 열림 ${next.decisions.open.items?.length ?? '?'}·닫힘 ${next.decisions.closed.items?.length ?? '?'}`);
  });

  it('mem 연결(`mcp --reader`)은 memory.db 사본에서 memory_graph를 준다', {skip: MEM_BIN && MEMORY_DB_SAMPLE && existsSync(MEMORY_DB_SAMPLE) ? false : 'MEM_BIN·MEMORY_DB_SAMPLE이 없다'}, async () => {
    const home = mkdtempSync(join(tmpdir(), 'orbit-realdb-mem-'));
    for (const suffix of ['', '-wal', '-shm']) if (existsSync(`${MEMORY_DB_SAMPLE}${suffix}`)) copyFileSync(`${MEMORY_DB_SAMPLE}${suffix}`, join(home, `memory.db${suffix}`));
    const mcp = startMcp(MEM_BIN!, ['mcp', '--reader'], home);
    try {
      const graph = await memoryGraph(mcp.ctx, {});
      assert.ok(graph.nodes.length > 0, '기억 노드가 비어 있지 않다');
      console.log(`기억 그래프: 노드 ${graph.nodes.length}`);
    } finally {
      mcp.stop();
      rmSync(home, {recursive: true, force: true});
    }
  });
});

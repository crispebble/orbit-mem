// 기억 그래프 탭 렌더 시험(서버 렌더). 「Studio 보기 › 끝 기준」 2: 로딩·빈·오류·멈춤·잘림 알림, 고른 기억 카드, 관련 기억.
import assert from 'node:assert/strict';
import {mkdirSync, readFileSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {after, before, describe, it} from 'node:test';
import {build} from 'esbuild';
import {parseMemoryGraph} from '../src/memory-graph/contract.ts';
import type {GraphState} from '../src/memory-graph/store.ts';

type Harness = typeof import('./memory-graph-render-harness.tsx');
let h: Harness;
const dir = new URL(`../node_modules/.cache/orbit-graph-render-${process.pid}`, import.meta.url).pathname;
mkdirSync(dir, {recursive: true});

before(async () => {
  const outfile = join(dir, 'harness.mjs');
  await build({
    entryPoints: [new URL('./memory-graph-render-harness.tsx', import.meta.url).pathname],
    outfile, bundle: true, platform: 'node', format: 'esm', jsx: 'automatic', logLevel: 'silent',
    packages: 'external', loader: {'.css': 'empty'},
  });
  h = await import(outfile);
});
after(() => rmSync(dir, {recursive: true, force: true}));

const example = (name: string) => (JSON.parse(readFileSync(new URL(`./memory-examples/${name}.json`, import.meta.url), 'utf8')) as {result: unknown}).result;
const all = parseMemoryGraph(example('memory_graph.all'));
const text = (html: string) => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');
const st = (over: Partial<GraphState>): GraphState => ({status: 'ready', includeHidden: false, reloading: false, ...over});

describe('기억 그래프 탭', () => {
  it('그래프가 있으면 패널(클러스터·관계)·거르기·도구를 그린다', () => {
    const t = text(h.renderBody(st({graph: all})));
    for (const s of ['클러스터 지도', '주제 겹쳐보기', '연결 관계', 'orbit', '브랜드', '그 밖', '같은 세션', '회고 연결', '대체 사슬', '숨김 포함', '다시 읽기', '목록으로', '일지']) {
      assert.ok(t.includes(s), s);
    }
  });

  it('불러오는 중', () => {
    assert.match(text(h.renderBody(st({status: 'loading'}))), /그래프를 불러오는 중/);
  });

  it('오류: 제목·메시지·코드·다시 시도', () => {
    const t = text(h.renderBody(st({status: 'error', error: '기억 기록을 읽지 못했습니다: database is locked', code: 'UNAVAILABLE'})));
    for (const s of ['기억 그래프를 읽지 못했습니다', 'database is locked', 'UNAVAILABLE', '다시 시도']) assert.ok(t.includes(s), s);
  });

  it('멈춤: 판 문구와 다시 확인. 그래프는 그리지 않는다', () => {
    const t = text(h.renderBody(st({status: 'paused', pause: {kind: 'orbit-older', message: 'Orbit을 올려 주세요.'}})));
    assert.match(t, /Orbit을 올려 주세요/);
    assert.match(t, /다시 확인/);
    assert.ok(!t.includes('클러스터 지도'));
  });

  it('빈 그래프', () => {
    assert.match(text(h.renderBody(st({graph: {...all, nodes: [], edges: [], groups: [], total: 0}}))), /표시할 항목이 없습니다/);
  });

  it('잘림·다시 읽기 실패를 알린다', () => {
    const t = text(h.renderBody(st({graph: parseMemoryGraph(example('memory_graph.truncated')), staleError: 'database is locked'})));
    assert.match(t, /최근에 고친 2개만 보입니다\(전체 3개\)/);
    assert.match(t, /다시 읽지 못했습니다: database is locked/);
  });
});

describe('고른 기억 카드', () => {
  it('종류·상태 배지, 범위, 클러스터, 연결 수, 주제 태그(제외 태그 뺌), 단추', () => {
    const node = all.nodes.find(n => n.id === 'm-1b2c3d4e5f60')!;
    const t = text(h.renderCard(all, node));
    for (const s of [node.title, '결정', '확정', '프로젝트 · orbit', 'orbit', '연결 4개', '#orbit', '#기억-그래프', '기억 열기', '주변 보기', '선택 풀기']) {
      assert.ok(t.includes(s), s);
    }
    assert.ok(!t.includes('#결정'), '종류 이름 태그는 뺀다');
  });
});

describe('관련 기억', () => {
  it('제목·근거·만든 쪽을 보이고, 제목을 못 읽었으면 id', () => {
    const html = h.renderLinks([
      {id: 'l-1', memoryId: 'm-2', title: '이어진 기억', kind: 'decision', note: '같은 결정의 근거', origin: 'retro'},
      {id: 'l-2', memoryId: 'm-3', origin: 'ai'},
    ]);
    const t = text(html);
    for (const s of ['관련 기억', '이어진 기억', '같은 결정의 근거 · 회고가 이음', 'm-3', 'AI가 이음']) assert.ok(t.includes(s), s);
    assert.equal((html.match(/orbit-link-button/g) ?? []).length, 2);
  });

  it('연결이 없으면 그리지 않는다', () => {
    assert.ok(!text(h.renderLinks([])).includes('관련 기억'));
  });
});

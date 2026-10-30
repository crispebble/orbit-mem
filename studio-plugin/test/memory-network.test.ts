// 기억 그래프 응답 검사와 그래프 부품 입력 변환. Atlas 「설계 · Orbit 2차 — 기억 그래프」 「Studio 보기 › 끝 기준」 1에 해당한다.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {describe, it} from 'node:test';
import {parseMemoryGraph, type GraphNode, type MemoryGraph} from '../src/memory-graph/contract.ts';
import {
  clusterByRepresentativeTag, clusterMinNew, isExcludedTag, memoryDescription, OTHER_CLUSTER, TAG_GROUP_MAX, toMemoryNetwork, UNKNOWN,
} from '../src/memory-graph/network.ts';
import {WorkPaused, WorkShapeError, WorkToolError} from '../src/work/contract.ts';

const example = (name: string) => (JSON.parse(readFileSync(new URL(`./memory-examples/${name}.json`, import.meta.url), 'utf8')) as {result: unknown}).result;
const contract = {name: 'orbit.memory', major: 1, minor: 1, supportedMajors: [1]};

let seq = 0;
function node(tags: string[], extra: Partial<GraphNode> = {}): GraphNode {
  const id = extra.id ?? `m-${String(++seq).padStart(4, '0')}`;
  return {id, kind: 'fact', state: 'confirmed', scope: 'global', title: `기억 ${id}`, tags, createdAt: '2026-10-29T00:00:00.000Z', updatedAt: '2026-10-29T00:00:00.000Z', ...extra};
}
const graph = (nodes: GraphNode[], rest: Partial<MemoryGraph> = {}): MemoryGraph => ({nodes, edges: [], groups: [], total: nodes.length, truncated: false, limitMax: 1000, depthMax: 3, ...rest});

describe('memory_graph 응답 읽기', () => {
  it('예시 응답 다섯을 모두 읽는다', () => {
    for (const name of ['all', 'filtered', 'focus', 'hidden', 'truncated']) {
      const g = parseMemoryGraph(example(`memory_graph.${name}`));
      assert.ok(g.nodes.length > 0, name);
    }
    const t = parseMemoryGraph(example('memory_graph.truncated'));
    assert.deepEqual([t.truncated, t.total, t.nodes.length], [true, 3, 2]);
    assert.equal(parseMemoryGraph(example('memory_graph.focus')).focus, 'm-2c3d4e5f6071');
  });

  it('오류 예시는 WorkToolError로 던진다(UNAVAILABLE·NOT_FOUND·INVALID_ARGUMENT)', () => {
    for (const [name, code] of [['error.unavailable', 'UNAVAILABLE'], ['error.not-found', 'NOT_FOUND'], ['error.invalid-argument', 'INVALID_ARGUMENT']]) {
      assert.throws(() => parseMemoryGraph(example(name)), (e: unknown) => e instanceof WorkToolError && e.code === code, name);
    }
  });

  it('Orbit이 2판만 읽으면 멈춤(Orbit 앱을 올려 주세요), 0판이면 Orbit을 올려 주세요', () => {
    const newer = {contract: {...contract, major: 2, supportedMajors: [2]}, error: {code: 'UNSUPPORTED_MAJOR', message: 'x', supportedMajors: [2]}};
    assert.throws(() => parseMemoryGraph(newer), (e: unknown) => e instanceof WorkPaused && e.kind === 'oni-newer');
    assert.throws(() => parseMemoryGraph({...(example('memory_graph.all') as object), contract: {...contract, major: 0}}), (e: unknown) => e instanceof WorkPaused && e.kind === 'orbit-older');
  });

  it('모양이 틀리면 WorkShapeError', () => {
    assert.throws(() => parseMemoryGraph({contract, nodes: []}), WorkShapeError);
    assert.throws(() => parseMemoryGraph({contract, nodes: [{id: 'm-1'}], edges: [], groups: []}), WorkShapeError);
    assert.throws(() => parseMemoryGraph({nodes: [], edges: [], groups: []}), WorkShapeError);
  });
});

describe('제외 태그', () => {
  it('옛 표식 태그와 종류 이름 태그를 뺀다', () => {
    for (const t of ['imported:server-knowledge', 'space:onidot', 'since:2026-10-11', 'type:project', '옛:ISSUE', 'superseded-by:47e52bb4', '결정', 'Lesson', ' 회고 ', '']) {
      assert.ok(isExcludedTag(t), t);
    }
    for (const t of ['onidot', 'ui', 'kvti', '기억-그래프', 'imported', 'spaces:x']) assert.ok(!isExcludedTag(t), t);
  });

  it('제외 태그는 클러스터와 같은 태그 선에 쓰이지 않는다', () => {
    const marked = ['imported:server-knowledge', 'space:onidot', 'since:2026-10-11', '결정'];
    const nodes = [node([...marked, 'orbit']), node(marked), node(marked), node([...marked, 'orbit'])];
    const groups = marked.map(key => ({kind: 'tag', key, label: key, members: nodes.map(n => n.id)}));
    const net = toMemoryNetwork(graph(nodes, {groups}));
    assert.deepEqual(net.clusters.map(c => c.id), ['orbit', OTHER_CLUSTER]);
    assert.equal(net.edges.filter(e => e.relation === 'tag').length, 0);
  });
});

describe('대표 태그(클러스터)', () => {
  it('덮기 순서로 고른다: 이미 덮인 기억만 가진 태그는 크더라도 고르지 않는다', () => {
    // a는 1~5, b는 1~4(크기 2위지만 a에 모두 덮임), c는 6~8
    const nodes = [node(['a', 'b']), node(['a', 'b']), node(['a', 'b']), node(['a', 'b']), node(['a']), node(['c']), node(['c']), node(['c'])];
    const {clusters, clusterOf} = clusterByRepresentativeTag(nodes);
    assert.deepEqual(clusters.map(c => c.id), ['a', 'c']);
    assert.equal(clusterOf.get(nodes[0].id), 'a');
    assert.equal(clusterOf.get(nodes[7].id), 'c');
  });

  it('대소문자를 무시하고 이름은 처음 본 표기다', () => {
    const nodes = [node(['UI']), node(['ui']), node(['Ui', 'x'])];
    const {clusters, clusterOf} = clusterByRepresentativeTag(nodes);
    assert.deepEqual(clusters, [{id: 'ui', label: 'UI', tone: 'c1'}]);
    assert.ok(nodes.every(n => clusterOf.get(n.id) === 'ui'));
  });

  it('앞뒤 공백·-와 _·전각 차이도 같은 태그다(oni TagKey와 같은 규칙)', () => {
    const nodes = [node(['claude_code']), node(['Claude-Code ']), node(['ＣＬＡＵＤＥ-code'])];
    const {clusters, clusterOf} = clusterByRepresentativeTag(nodes);
    assert.deepEqual(clusters.map(c => c.id), ['claude-code']);
    assert.equal(clusters[0].label, 'claude_code');
    assert.ok(nodes.every(n => clusterOf.get(n.id) === 'claude-code'));
  });

  it('개수 상한 없이 새로 묶는 기억이 전체의 1%(최소 2개) 이상인 태그까지 고르고, 색 칸은 고른 순서대로 c1, c2, …', () => {
    // t0~t9가 10, 9, …, 1개. 기억 56개라 기준은 2개: t0~t8(9개)을 고르고 t9(1개)와 태그 없는 기억은 그 밖
    const nodes: GraphNode[] = [];
    for (let t = 0; t < 10; t++) for (let k = 0; k < 10 - t; k++) nodes.push(node([`t${t}`]));
    nodes.push(node([]));
    const {clusters} = clusterByRepresentativeTag(nodes);
    assert.equal(clusters.length, 9 + 1);
    assert.deepEqual(clusters.map(c => c.tone), ['c1', 'c2', 'c3', 'c4', 'c5', 'c6', 'c7', 'c8', 'c9', 'other']);
    assert.deepEqual(clusters.slice(0, 9).map(c => c.id), ['t0', 't1', 't2', 't3', 't4', 't5', 't6', 't7', 't8']);
    assert.deepEqual(clusters[9], {id: OTHER_CLUSTER, label: '그 밖', tone: 'other'});
  });

  it('1%는 받은 노드가 아니라 응답의 total(잘리기 전 기억 수)로 센다(oni stats와 같은 기준)', () => {
    const nodes = [node(['a']), node(['a']), node(['a'])];
    assert.deepEqual(clusterByRepresentativeTag(nodes).clusters.map(c => c.id), ['a']);
    assert.deepEqual(clusterByRepresentativeTag(nodes, 1000).clusters.map(c => c.id), [OTHER_CLUSTER]);
    assert.equal(toMemoryNetwork(graph(nodes, {total: 1000, truncated: true})).clusters[0].id, OTHER_CLUSTER);
  });

  it('클러스터 기준은 전체의 1%, 최소 2개(oni ClusterMinNew와 같다)', () => {
    for (const [n, want] of [[0, 2], [10, 2], [249, 2], [250, 3], [558, 6], [1000, 10]] as const) assert.equal(clusterMinNew(n), want);
  });

  it('같으면 키 순이고, 같은 입력이면 같은 결과다', () => {
    const nodes = [node(['나']), node(['가']), node(['b']), node(['a'])].flatMap(n => [n, node(n.tags)]);
    assert.deepEqual(clusterByRepresentativeTag(nodes).clusters.map(c => c.id), ['a', 'b', '가', '나']);
    const g = parseMemoryGraph(example('memory_graph.all'));
    assert.deepEqual(toMemoryNetwork(g), toMemoryNetwork(structuredClone(g)));
  });
});

describe('예시 응답 → 부품 입력', () => {
  const g = parseMemoryGraph(example('memory_graph.all'));
  const net = toMemoryNetwork(g);

  it('기억마다 점 하나, 클러스터는 대표 태그(결정·회고 태그는 빼고, 기억 하나만 묶는 태그는 그 밖)', () => {
    assert.equal(net.nodes.length, g.nodes.length);
    assert.deepEqual(net.clusters.map(c => [c.id, c.tone]), [['orbit', 'c1'], ['브랜드', 'c2'], [OTHER_CLUSTER, 'other']]);
    const journal = net.nodes.find(n => n.id === 'm-60718293a4b5');
    assert.equal(journal?.cluster, OTHER_CLUSTER); // 태그가 「회고」뿐이다
  });

  it('설명은 종류 · 상태 · 범위, 낡음은 흐리게', () => {
    const stale = net.nodes.find(n => n.id === 'm-4e5f60718293');
    assert.deepEqual([stale?.description, stale?.muted], ['규칙 · 낡음 · 전역', true]);
    assert.equal(net.nodes.find(n => n.id === 'm-2c3d4e5f6071')?.description, '교훈 · 후보 · 프로젝트 · orbit');
  });

  it('선: 연결·회고 연결·대체(화살표)·같은 태그·같은 세션(고친 때 순 사슬)·같은 파일', () => {
    const by = (r: string) => net.edges.filter(e => e.relation === r);
    assert.deepEqual(by('link').map(e => e.id), ['l-9a8b7c6d5e4f']);
    assert.deepEqual(by('retro').map(e => e.id), ['l-8b7c6d5e4f30']);
    assert.deepEqual(by('supersede').map(e => [e.from, e.to, e.arrow]), [['m-4e5f60718293', 'm-5f60718293a4', true]]);
    assert.equal(by('tag').length, 4); // orbit 3짝 + 브랜드 1짝. 기억-그래프·측정의 짝은 orbit 짝과 같아 하나로 합친다
    assert.deepEqual(by('session').map(e => [e.from, e.to]), [['m-1b2c3d4e5f60', 'm-718293a4b5c6'], ['m-2c3d4e5f6071', 'm-718293a4b5c6']]);
    assert.equal(by('file').length, 1);
    assert.equal(net.unknownRelations, 0);
    const ids = new Set(net.nodes.map(n => n.id));
    assert.ok(net.edges.every(e => ids.has(e.from) && ids.has(e.to)));
    assert.deepEqual(net.relations.map(r => [r.id, r.line]), [['tag', 'solid'], ['link', 'solid'], ['supersede', 'solid'], ['retro', 'solid'], ['session', 'dashed'], ['file', 'dotted']]);
  });

  it('겹쳐보기: 대체 사슬·시간순(번호)·회고 연결·같은 세션', () => {
    const o = Object.fromEntries(net.overlays.map(x => [x.id, x]));
    assert.deepEqual(o.chain.paths, [['m-4e5f60718293', 'm-5f60718293a4']]);
    assert.equal(o.time.numbered, true);
    assert.equal(o.time.paths[0].at(-1), 'm-2c3d4e5f6071'); // 가장 최근에 고친 것이 끝
    assert.deepEqual(o.retro.highlight, ['m-0a1b2c3d4e5f', 'm-2c3d4e5f6071']);
    assert.deepEqual(o.session.paths, [['m-1b2c3d4e5f60', 'm-718293a4b5c6', 'm-2c3d4e5f6071']]);
  });

  it('종류로 걸러도 클러스터 색이 바뀌지 않고, 선은 남은 기억끼리만', () => {
    const rules = toMemoryNetwork(g, {kinds: new Set(['rule'])});
    assert.ok(rules.nodes.every(n => g.nodes.find(m => m.id === n.id)?.kind === 'rule'));
    assert.deepEqual(rules.clusters.map(c => [c.id, c.tone]), [['브랜드', 'c2'], [OTHER_CLUSTER, 'other']]);
    const ids = new Set(rules.nodes.map(n => n.id));
    assert.ok(rules.edges.every(e => ids.has(e.from) && ids.has(e.to)));
    assert.equal(rules.edges.filter(e => e.relation === 'supersede').length, 1);
  });
});

describe('그 밖의 규칙', () => {
  it('모르는 종류·상태는 「알 수 없음」, 모르는 선·묶음 종류는 그리지 않고 센다', () => {
    const a = node(['x'], {kind: 'memo', state: 'archived'}), b = node(['x']);
    assert.equal(memoryDescription(a), `${UNKNOWN} · ${UNKNOWN} · 전역`);
    const net = toMemoryNetwork(graph([a, b], {
      edges: [{id: 'z-1', kind: 'similar', from: a.id, to: b.id}],
      groups: [{kind: 'project', key: 'p', label: 'p', members: [a.id, b.id]}, {kind: 'tag', key: 'x', label: 'x', members: [a.id, b.id]}],
    }));
    assert.equal(net.unknownRelations, 2);
    assert.deepEqual(net.edges.map(e => e.relation), ['tag']);
  });

  it('응답 밖 기억을 가리키는 선·묶음 소속은 버린다', () => {
    const a = node(['x']), b = node(['x']);
    const net = toMemoryNetwork(graph([a, b], {
      edges: [{id: 'l-1', kind: 'link', from: a.id, to: 'm-없음', origin: 'ai'}],
      groups: [{kind: 'file', key: '/f', label: 'f', members: [a.id, 'm-없음']}],
    }));
    assert.equal(net.edges.filter(e => e.relation !== 'tag').length, 0);
  });

  it(`같은 태그 묶음이 ${TAG_GROUP_MAX}개를 넘으면 선을 긋지 않고, 6~${TAG_GROUP_MAX}개면 기억마다 짝 몇 개만`, () => {
    const big = Array.from({length: TAG_GROUP_MAX + 1}, () => node(['big']));
    const mid = Array.from({length: 20}, () => node(['mid']));
    const g = graph([...big, ...mid], {groups: [
      {kind: 'tag', key: 'big', label: 'big', members: big.map(n => n.id)},
      {kind: 'tag', key: 'mid', label: 'mid', members: mid.map(n => n.id)},
    ]});
    const net = toMemoryNetwork(g);
    const midIds = new Set(mid.map(n => n.id));
    const tag = net.edges.filter(e => e.relation === 'tag');
    assert.ok(tag.length > 0 && tag.every(e => midIds.has(e.from) && midIds.has(e.to)));
    assert.ok(tag.length <= 20 * 4 && tag.length < (20 * 19) / 2);
  });

  it('파일 묶음은 4개까지만 모든 짝을 잇는다', () => {
    const five = Array.from({length: 5}, () => node([]));
    const net = toMemoryNetwork(graph(five, {groups: [{kind: 'file', key: '/a/AGENTS.md', label: 'AGENTS.md', members: five.map(n => n.id)}]}));
    assert.equal(net.edges.length, 0);
    const four = five.slice(0, 4);
    assert.equal(toMemoryNetwork(graph(four, {groups: [{kind: 'file', key: '/a/AGENTS.md', label: 'AGENTS.md', members: four.map(n => n.id)}]})).edges.length, 6);
  });

  it('대소문자만 다른 태그 묶음 둘이 같은 짝을 내도 선은 하나', () => {
    const a = node(['UI']), b = node(['UI', 'ui']);
    const c = {...a, tags: ['UI', 'ui']};
    const net = toMemoryNetwork(graph([c, b], {groups: [
      {kind: 'tag', key: 'UI', label: 'UI', members: [c.id, b.id]},
      {kind: 'tag', key: 'ui', label: 'ui', members: [c.id, b.id]},
    ]}));
    assert.equal(net.edges.length, 1);
  });
});

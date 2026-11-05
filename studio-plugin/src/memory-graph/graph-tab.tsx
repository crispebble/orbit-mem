// 「기억 그래프」 탭: memory_graph를 Forma 관계망 그래프(NetworkGraph·NetworkPanel)로 그린다.
// 규칙은 Atlas 「설계 · Orbit 2차 — 기억 그래프」 「Studio 보기」. 부품은 노드·선·클러스터만 알고, 거르기·고른 기억 카드·탭 연결은 여기서 한다.
import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {Clock, History, Link2, MessagesSquare} from 'lucide-react';
import type {PluginContext, TabProps} from '@onidot-labs/orbit-studio-plugin';
import {describeNetwork, NetworkGraph, NetworkPanel, searchNetwork, type NetworkDescription, type NetworkOverlay} from '@onidot-labs/orbit-studio-plugin/graph';
import {Badge, Button, SegmentedControl, StateView, Switch} from '@onidot-labs/orbit-studio-plugin/ui';
import {localeTag, useLocale, useMessages} from '../i18n/index.ts';
import {graphText} from '../i18n/messages/graph.ts';
import {memoryText} from '../i18n/messages/memory.ts';
import {KINDS} from '../parts/vocabulary.ts';
import {pauseMessage} from '../work/contract.ts';
import {useOrbit} from '../runtime.ts';
import type {GraphNode} from './contract.ts';
import {
  isExcludedTag, kindLabel, scopeLabel, stateLabel, toMemoryNetwork, type MemoryNetwork, type OverlayIcon,
} from './network.ts';
import {onSelectRequest, takePendingSelect} from './open.ts';
import {resolveRequest} from './select.ts';
import {createGraphStore, type GraphState, type GraphStore} from './store.ts';

const OVERLAY_ICONS: Record<OverlayIcon, ReactNode> = {
  chain: <Link2 aria-hidden="true" />, time: <Clock aria-hidden="true" />, retro: <History aria-hidden="true" />, session: <MessagesSquare aria-hidden="true" />,
};

export function openMemory(ctx: Pick<PluginContext, 'tabs'>, node: Pick<GraphNode, 'id' | 'title'>): void {
  ctx.tabs.open({type: 'memory', id: node.id, title: node.title, params: {id: node.id}});
}

export function MemoryGraphTab({ctx, tab}: TabProps) {
  const store = useMemo(() => createGraphStore(ctx), [ctx]);
  const state = useSyncExternalStore(store.subscribe, store.getState, store.getState);
  const {state: orbit} = useOrbit();
  const [request, setRequest] = useState<string | undefined>(() => takePendingSelect() ?? (typeof tab.params.select === 'string' ? tab.params.select : undefined));

  useEffect(() => {
    void store.load();
    const off = onSelectRequest(setRequest);
    return () => { off(); store.dispose(); };
  }, [store]);

  // 제안 목록이 새로 읽히면(60초마다·결정 뒤) 그래프도 조용히 다시 읽는다. 「기억」 목록과 같은 신호다.
  // 멈춤은 사용자가 「다시 확인」할 때까지 둔다
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    if (store.getState().status !== 'paused') void store.load({quiet: true});
  }, [store, orbit.proposals]);

  const done = useCallback(() => setRequest(undefined), []);
  return <GraphBody ctx={ctx} store={store} state={state} request={request} onRequestDone={done} />;
}

export interface SelectedMemoryCardProps {
  node: GraphNode;
  info: NetworkDescription;
  /** 이 기억이 주변 보기의 가운데다 */
  centered: boolean;
  onOpen: () => void;
  onCenter: () => void;
  onClear: () => void;
}

/** 왼쪽 패널의 고른 기억 카드: 종류·상태 배지, 범위, 클러스터, 연결 수, 주제 태그, 단추 */
export function SelectedMemoryCard({node, info, centered, onOpen, onCenter, onClear}: SelectedMemoryCardProps) {
  const t = useMessages(graphText).card;
  const cluster = info.clusterOf(node.id);
  const tags = node.tags.filter(t => !isExcludedTag(t));
  return (
    <section className="forma-netpanel-card" aria-label={t.label}>
      <h3>{node.title}</h3>
      <div className="forma-netpanel-card-cluster">
        <Badge>{kindLabel(node.kind)}</Badge>
        <Badge tone={node.state === 'confirmed' ? 'success' : node.state === 'candidate' ? 'accent' : 'neutral'}>{stateLabel(node.state)}</Badge>
      </div>
      <p>{scopeLabel(node.scope, node.project)}</p>
      <div className="forma-netpanel-card-cluster">
        <i className="forma-net-dot" data-tone={info.toneOf(cluster ?? '')} />
        {info.clusters.find(c => c.id === cluster)?.label}
        <Badge>{t.links(info.degreeOf(node.id).toLocaleString(localeTag()))}</Badge>
      </div>
      {tags.length > 0 && <p>{tags.map(t => `#${t}`).join(' ')}</p>}
      <div className="forma-netpanel-card-actions">
        <Button size="sm" variant="primary" onClick={onOpen}>{t.open}</Button>
        <Button size="sm" variant="secondary" aria-pressed={centered} onClick={onCenter}>{t.center}</Button>
        <Button size="sm" variant="ghost" onClick={onClear}>{t.clear}</Button>
      </div>
    </section>
  );
}

export interface GraphBodyProps {
  ctx: PluginContext;
  store: Pick<GraphStore, 'load' | 'setIncludeHidden'>;
  state: GraphState;
  /** 고르라는 요청(기억 탭의 「그래프에서 보기」) */
  request?: string;
  onRequestDone?: () => void;
}

/** 탭 본문. 데이터 상태를 받아 그린다(렌더 시험이 이것을 그린다) */
export function GraphBody({ctx, store, state, request, onRequestDone}: GraphBodyProps) {
  const locale = useLocale();
  const g = useMessages(graphText);
  const mem = useMessages(memoryText);
  const kindOptions = [{value: 'all', label: g.tab.all}, ...KINDS.map(k => ({value: k, label: mem.kinds[k]}))];
  const [kind, setKind] = useState('all');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [focus, setFocus] = useState<Set<string>>(() => new Set());
  const [mode, setMode] = useState<'highlight' | 'filter'>('highlight');
  const [overlayId, setOverlayId] = useState<string | null>(null);
  const [hiddenRelations, setHiddenRelations] = useState<Set<string>>(() => new Set());
  const [center, setCenter] = useState<string | null>(null);
  const [depth, setDepth] = useState<1 | 2 | 3>(2);
  const [missing, setMissing] = useState(false);

  const graph = state.graph;
  // nodes·edges 참조를 고정한다(부품이 같은 입력의 배치를 다시 계산하지 않는다)
  const network: MemoryNetwork | undefined = useMemo(
    () => (graph ? toMemoryNetwork(graph, {kinds: kind === 'all' ? undefined : new Set([kind])}) : undefined),
    // 이름·설명이 언어를 따르므로 언어가 바뀌면 다시 만든다
    [graph, kind, locale],
  );
  const info = useMemo(() => (network ? describeNetwork(network) : undefined), [network]);
  const overlays: NetworkOverlay[] = useMemo(() => network?.overlays.map(o => ({...o, icon: OVERLAY_ICONS[o.icon]})) ?? [], [network]);
  const byId = useMemo(() => new Map(graph?.nodes.map(n => [n.id, n] as const) ?? []), [graph]);
  const visible = useMemo(() => new Set(network?.nodes.map(n => n.id) ?? []), [network]);
  const hitIds = useMemo(() => (query && network ? searchNetwork(network.nodes, query) : null), [network, query]);
  const hits = useMemo(() => (hitIds?.length ? new Set(hitIds) : null), [hitIds]);

  // 고른 기억이 거르기로 사라지면 고름·주변 보기를 풀고, 없어진 클러스터는 강조에서 뺀다
  useEffect(() => {
    if (selectedId && !visible.has(selectedId)) setSelectedId(null);
    if (center && !visible.has(center)) setCenter(null);
  }, [visible, selectedId, center]);
  useEffect(() => {
    const ids = new Set(network?.clusters.map(c => c.id) ?? []);
    setFocus(prev => ([...prev].every(id => ids.has(id)) ? prev : new Set([...prev].filter(id => ids.has(id)))));
  }, [network]);
  // 다른 기억을 고르거나 그래프를 새로 읽으면 「없음」 안내를 지운다(요청 처리보다 먼저 둔다)
  useEffect(() => { if (selectedId) setMissing(false); }, [selectedId]);
  useEffect(() => { setMissing(false); }, [graph]);

  // 「그래프에서 보기」 요청: 있으면 고르고(종류 거르기·주변 보기·강조·찾기를 풀어 보이게), 없으면 한 번 다시 읽어 본다
  const reloadedFor = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!request) return;
    const outcome = resolveRequest(request, {
      loaded: !!graph, reloading: state.reloading, graphIds: new Set(byId.keys()), visibleIds: visible, reloaded: reloadedFor.current === request,
    });
    if (outcome.kind === 'wait') return;
    if (outcome.kind === 'reload') { reloadedFor.current = request; void store.load(); return; }
    reloadedFor.current = undefined;
    if (outcome.kind === 'select') {
      if (outcome.clearKind) setKind('all');
      setCenter(null);
      setFocus(new Set());
      setQuery('');
      setSelectedId(request);
    } else setMissing(true);
    onRequestDone?.();
  }, [request, graph, state.reloading, byId, visible, store, onRequestDone]);

  if (state.status === 'paused' && state.pause) {
    return (
      <div className="orbit-pane">
        <StateView title={g.tab.pausedTitle} description={pauseMessage(state.pause.kind, 'orbit-mem')}
          action={<Button variant="secondary" size="sm" onClick={() => void store.load()}>{g.tab.recheck}</Button>} />
      </div>
    );
  }

  const selected = selectedId ? byId.get(selectedId) : undefined;
  const notices: string[] = [];
  if (graph?.truncated) notices.push(g.tab.truncated(graph.nodes.length.toLocaleString(localeTag(locale)), graph.total.toLocaleString(localeTag(locale))));
  if (network?.unknownRelations) notices.push(g.tab.unknownRelations(network.unknownRelations));
  if (missing) notices.push(g.tab.missing);
  if (state.staleError) notices.push(`${g.tab.reloadFailed} ${state.staleError}`);

  const card = selected && info ? (
    <SelectedMemoryCard node={selected} info={info} centered={center === selected.id}
      onOpen={() => openMemory(ctx, selected)} onCenter={() => setCenter(selected.id)} onClear={() => setSelectedId(null)} />
  ) : null;

  const nodes = network?.nodes ?? [];
  const edges = network?.edges ?? [];
  const clusters = network?.clusters ?? [];
  const relations = network?.relations ?? [];
  const side = (
    <NetworkPanel title={g.tab.label} text={g.panel} nodes={nodes} edges={edges} clusters={clusters} relations={relations} overlays={overlays}
      query={query} onQueryChange={q => { setQuery(q); const found = q && network ? searchNetwork(network.nodes, q) : []; if (found.length === 1) setSelectedId(found[0]); }}
      hitCount={hitIds ? hitIds.length : null}
      focusClusters={focus} onFocusClusters={setFocus} clusterMode={mode} onClusterMode={setMode}
      overlayId={overlayId} onOverlay={setOverlayId} hiddenRelations={hiddenRelations} onHiddenRelations={setHiddenRelations}>
      {card}
    </NetworkPanel>
  );
  const filters = (
    <div className="orbit-graph-filters">
      <SegmentedControl label={g.tab.kind} value={kind} options={kindOptions} onChange={setKind} />
      <Switch label={g.tab.includeHidden} checked={state.includeHidden} onCheckedChange={v => void store.setIncludeHidden(v)} />
    </div>
  );
  const tools = (
    <>
      <Button size="sm" variant="secondary" disabled={state.reloading || state.status === 'loading'} onClick={() => void store.load()}>{state.reloading ? g.tab.reloading : g.tab.reload}</Button>
      <Button size="sm" variant="ghost" onClick={() => ctx.app.show('orbit', 'memories')}>{g.tab.toList}</Button>
    </>
  );
  return (
    <div className="orbit-graph">
      <NetworkGraph label={g.tab.label} text={g.network} nodes={nodes} edges={edges} clusters={clusters} relations={relations}
        state={state.status === 'loading' ? 'loading' : state.status === 'error' ? 'error' : 'ready'}
        errorText={g.tab.errorTitle}
        errorDetail={state.status === 'error' ? <p className="orbit-graph-error">{state.error}{state.code && <> <code>{state.code}</code></>}</p> : undefined}
        onRetry={() => void store.load()}
        selectedId={selectedId} onSelect={setSelectedId}
        focusClusters={focus} clusterMode={mode} hiddenRelations={hiddenRelations} hits={hits}
        overlay={overlays.find(o => o.id === overlayId) ?? null}
        center={center} onCenterChange={id => { setCenter(id); if (id) setSelectedId(id); }} depth={depth} onDepthChange={setDepth}
        side={side} sideLabel={g.tab.sideLabel} tools={tools} filters={filters}
        notice={notices.length ? <span className="orbit-graph-notice">{notices.join(' ')}</span> : undefined} />
    </div>
  );
}

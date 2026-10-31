// 사이드바 「기억」 보기: 찾기 칸과 결과 목록
import {useEffect, useRef, useState} from 'react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {Button} from '@onidot-labs/studio-plugin/ui';
import {useLocale, useMessages} from './i18n/index.ts';
import {commonText} from './i18n/messages/common.ts';
import {memoryText} from './i18n/messages/memory.ts';
import {openMemoryGraph} from './memory-graph/open.ts';
import {MemoryList, type MemoryKind, type MemoryListState, type OrbitMemory} from './parts/index.ts';
import {errorMessage} from './orb.ts';
import {useOrbit} from './runtime.ts';

const DEBOUNCE_MS = 250;

export function MemoriesView({ctx}: ViewProps) {
  const {store, state} = useOrbit();
  const t = useMessages(memoryText);
  const common = useMessages(commonText);
  const locale = useLocale();
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState<MemoryKind | 'all'>('all');
  const [filterState, setFilterState] = useState<MemoryListState>('all');
  const [memories, setMemories] = useState<OrbitMemory[]>([]);
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [error, setError] = useState<string>();
  const [selected, setSelected] = useState<string>();
  const [reload, setReload] = useState(0);
  const seq = useRef(0);
  // 제안 목록이 새로 불러와지면(60초마다·결정 뒤) 기억도 다시 읽는다.
  const proposalsVersion = state.proposals;

  useEffect(() => {
    if (!store) return;
    const mine = ++seq.current;
    const timer = setTimeout(() => {
      store.findMemories({query, kind, state: filterState}).then(
        items => {
          if (mine !== seq.current) return; // 더 새 요청이 있다
          setMemories(items);
          setStatus('ready');
          setError(undefined);
        },
        (e: unknown) => {
          if (mine !== seq.current) return;
          ctx.log.error(`Could not find memories: ${errorMessage(e)}`);
          setStatus('error');
          setError(errorMessage(e));
        },
      );
    }, DEBOUNCE_MS);
    return () => clearTimeout(timer);
  // 날짜·범위 이름이 언어를 따르므로 언어가 바뀌면 다시 읽는다
  }, [store, ctx, query, kind, filterState, reload, proposalsVersion, locale]);

  return (
    <div className="orbit-side">
      <div className="orbit-row">
        <Button variant="secondary" size="sm" onClick={() => openMemoryGraph(ctx)}>{t.list.showGraph}</Button>
      </div>
      <MemoryList
        memories={memories}
        filterMode="server"
        emptyText={query.trim() ? t.list.noMatch : t.list.noneYet}
        selectedId={selected}
        onSelect={id => {
          setSelected(id);
          const m = memories.find(x => x.id === id);
          ctx.tabs.open({type: 'memory', id, title: m?.title ?? id, params: {id}});
        }}
        query={query}
        onQueryChange={setQuery}
        kind={kind}
        onKindChange={setKind}
        state={filterState}
        onStateChange={setFilterState}
        availability={!store ? 'loading' : status === 'error' ? (memories.length ? 'offline' : 'error') : status === 'loading' && memories.length === 0 ? 'loading' : 'ready'}
        error={error}
      />
      {status === 'error' && <div className="orbit-row"><Button variant="secondary" size="sm" onClick={() => { setStatus('loading'); setReload(n => n + 1); }}>{common.retry}</Button></div>}
    </div>
  );
}

// 기억 목록: 찾기·종류·범위·상태로 거르고 한 건을 고른다.
import {useState} from 'react';
import {Search} from 'lucide-react';
import {AvailabilityNotice, EmptyState, Frame, type Availability} from './shared.tsx';
import {KindTile, Select, StateMark} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import {GLOBAL_SCOPE, KINDS, kindOf, memoryKindLabels, scopeOf, stateOf, type MemoryKind, type OrbitMemory} from './vocabulary.ts';

export type MemoryListState = 'all' | 'active' | 'candidate' | 'confirmed' | 'stale';

const listStateOptions: readonly {value: MemoryListState; label: string}[] = [
  {value: 'all', label: '전체'},
  {value: 'active', label: '낡음 제외'},
  {value: 'candidate', label: '후보'},
  {value: 'confirmed', label: '확정'},
  {value: 'stale', label: '낡음'},
];

export interface MemoryListProps extends OrbitProps {
  memories: readonly OrbitMemory[];
  selectedId?: string;
  onSelect?: (id: string) => void;
  query?: string;
  onQueryChange?: (query: string) => void;
  kind?: MemoryKind | 'all';
  onKindChange?: (kind: MemoryKind | 'all') => void;
  scope?: string;
  onScopeChange?: (scope: string) => void;
  state?: MemoryListState;
  onStateChange?: (state: MemoryListState) => void;
  /**
   * client(기본): 받은 기억을 안에서 찾기·종류·범위·상태로 거른다.
   * server: 입력값은 on…Change로만 알리고 받은 기억을 거르지 않는다(서버가 이미 거른 결과를 그대로 보인다). 숨김 상태만 빼고 보인다.
   */
  filterMode?: 'client' | 'server';
  /** false면 안쪽 검색칸·종류 칩·범위·상태 선택을 그리지 않는다(밖에서 필터를 그릴 때). 기본 true */
  controls?: boolean;
  /** 범위 선택의 목록. 없으면 받은 기억에서 모은다(server 모드에서는 결과가 줄면 목록도 줄므로 넘기는 편이 좋다). */
  scopes?: readonly string[];
  /** 종류 칩에 보일 개수. client 모드에서는 없으면 직접 센다. server 모드에서는 넘긴 것만 보인다. */
  kindCounts?: Partial<Record<MemoryKind | 'all', number>>;
  /** 결과가 없을 때 문구. 기본은 받은 기억이 하나도 없으면(client) "저장된 기억이 없습니다.", 그 밖에는 "조건에 맞는 기억이 없습니다." */
  emptyText?: string;
  availability?: Availability;
  error?: string;
}

/** 값을 밖에서 주면 그 값을, 아니면 안쪽 상태를 쓴다. 바꿀 때는 둘 다 갱신하고 알린다. */
function useControlled<T>(value: T | undefined, initial: T, onChange?: (next: T) => void): [T, (next: T) => void] {
  const [inner, setInner] = useState(initial);
  return [
    value ?? inner,
    (next: T) => {
      setInner(next);
      onChange?.(next);
    },
  ];
}

export function MemoryList({
  memories,
  selectedId,
  onSelect,
  query,
  onQueryChange,
  kind,
  onKindChange,
  scope,
  onScopeChange,
  state,
  onStateChange,
  filterMode = 'client',
  controls = true,
  scopes: givenScopes,
  kindCounts,
  emptyText,
  availability = 'ready',
  error,
  className = '',
  surface = 'plain',
}: MemoryListProps) {
  const [q, setQ] = useControlled(query, '', onQueryChange);
  const [k, setK] = useControlled(kind, 'all' as MemoryKind | 'all', onKindChange);
  const [sc, setSc] = useControlled(scope, 'all', onScopeChange);
  const [st, setSt] = useControlled(state, 'all' as MemoryListState, onStateChange);
  const visible = memories.filter(memory => stateOf(memory) !== 'hidden');
  const needle = q.trim().toLocaleLowerCase();
  const server = filterMode === 'server';
  const scopes = givenScopes
    ? [...givenScopes]
    : [...new Set(visible.map(scopeOf))].sort((a, b) => (a === GLOBAL_SCOPE ? -1 : b === GLOBAL_SCOPE ? 1 : a.localeCompare(b)));
  const base = server
    ? visible
    : visible.filter(memory => {
        const current = stateOf(memory);
        if (st === 'active' ? current === 'stale' : st !== 'all' && current !== st) return false;
        if (sc !== 'all' && scopeOf(memory) !== sc) return false;
        return !needle || `${memory.title} ${memory.content} ${(memory.tags ?? []).join(' ')}`.toLocaleLowerCase().includes(needle);
      });
  const rows = server ? visible : base.filter(memory => k === 'all' || kindOf(memory) === k);
  const count = (value: MemoryKind | 'all') =>
    kindCounts?.[value] ?? (server ? undefined : value === 'all' ? base.length : base.filter(memory => kindOf(memory) === value).length);

  return (
    <Frame label="기억 목록" surface={surface} className={`orbit-mem-list ${className}`}>
      <AvailabilityNotice availability={availability} error={error} />
      {controls && (
        <div className="orbit-mem-filter">
          <div className="orbit-mem-search">
            <Search size={13} aria-hidden="true" />
            <input type="search" aria-label="기억 검색" placeholder="기억 찾기" value={q} onChange={event => setQ(event.target.value)} />
            <span className="orbit-meta" role="status">{kindCounts?.all ?? rows.length}건</span>
          </div>
          <div className="orbit-mem-chips" role="group" aria-label="종류">
            {(['all', ...KINDS] as const).map(value => {
              const n = count(value);
              const name = value === 'all' ? '전체' : memoryKindLabels[value];
              return (
                <button type="button" key={value} className="orbit-chip" aria-pressed={k === value} aria-label={n === undefined ? name : `${name} ${n}`} onClick={() => setK(value)}>
                  {name}
                  {n !== undefined && (n > 0 || value === 'all') && <> <span className="orbit-chip-n">{n}</span></>}
                </button>
              );
            })}
          </div>
          <div className="orbit-mem-selects">
            <Select label="범위" value={sc} onChange={setSc} options={[{value: 'all', label: '전체'}, ...scopes.map(value => ({value, label: value}))]} />
            <Select label="상태" value={st} onChange={value => setSt(value as MemoryListState)} options={listStateOptions} />
          </div>
        </div>
      )}
      {rows.length === 0 ? (
        availability === 'ready' ? <EmptyState title={emptyText ?? (server || visible.length > 0 ? '조건에 맞는 기억이 없습니다.' : '저장된 기억이 없습니다.')} /> : null
      ) : (
        <ul className="orbit-list orbit-mem-rows" aria-label="기억">
          {rows.map(memory => {
            const current = stateOf(memory);
            const extra = current === 'stale' && memory.supersededBy ? '대체됨' : (memory.version ?? 1) > 1 ? `v${memory.version}` : memory.updatedAt;
            return (
              <li key={memory.id}>
                <button type="button" className="orbit-mem-row" data-state={current} data-kind={kindOf(memory)} aria-current={selectedId === memory.id ? 'true' : undefined} onClick={() => onSelect?.(memory.id)}>
                  <KindTile kind={kindOf(memory)} />
                  <span className="orbit-mem-text">
                    <span className="orbit-mem-title">{memory.title}</span>
                    <span className="orbit-mem-sub">
                      <StateMark state={current} />
                      <span aria-hidden="true">·</span>
                      <span>{scopeOf(memory)}</span>
                      {extra && <><span aria-hidden="true">·</span><span>{extra}</span></>}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </Frame>
  );
}

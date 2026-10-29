// 사이드바 「세션」 보기: 메인 세션 단위 한 줄(forma-ai SessionRow). 기본은 끝나지 않은 세션, 「전체」는 끝난 세션도.
import {useMemo, useState, type KeyboardEvent} from 'react';
import type {ViewProps} from '@onidot-labs/studio-plugin';
import {SessionRow} from '@onidot-labs/studio-plugin/ai';
import {EmptyState, SegmentedControl} from '@onidot-labs/studio-plugin/ui';
import {sessionRowProps} from '../work/session-view.ts';
import {stepTitleMap, useLoadWorks, useNow, useWork} from './runtime.ts';
import {Paused} from './paused.tsx';

type Filter = 'live' | 'all';

/** ↑↓로 행 사이를 옮긴다(행은 단추 하나씩) */
function moveFocus(e: KeyboardEvent<HTMLUListElement>) {
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
  const rows = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('li button'));
  const at = rows.indexOf(e.target as HTMLElement);
  if (at < 0) return;
  e.preventDefault();
  rows[at + (e.key === 'ArrowDown' ? 1 : -1)]?.focus();
}

export function SessionsView({ctx}: ViewProps) {
  const {store, state} = useWork();
  const now = useNow();
  const [filter, setFilter] = useState<Filter>('live');
  const [selected, setSelected] = useState<string>();
  const titles = useMemo(() => stepTitleMap(state), [state.details]);
  useLoadWorks(store, state.sessions.filter(s => s.state !== 'ended' && s.activeStepIds.length > 0).flatMap(s => s.openWorkIds), now);

  if (!store) return <EmptyState title="Orbit을 켜는 중입니다" />;
  if (state.status === 'paused') {
    return <div className="orbit-side"><Paused store={store} state={state} title="세션 기록을 읽지 않습니다" /></div>;
  }
  if (state.status === 'error' && state.sessions.length === 0) {
    return <div className="orbit-side"><EmptyState title="세션 기록을 읽지 못했습니다" description={`${state.error ?? ''} 잠시 뒤 다시 읽습니다.`} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.sessions.length === 0)) {
    return <div className="orbit-side"><EmptyState title="세션을 불러오는 중입니다" /></div>;
  }

  // 부모가 있는 세션(다른 세션이 띄운 세션)은 부모 세션 탭의 호출 계보에서 보인다
  const mains = state.sessions.filter(s => !s.parentSessionId || !state.sessions.some(p => p.sessionId === s.parentSessionId));
  const live = mains.filter(s => s.state !== 'ended');
  const rows = filter === 'live' ? live : mains;

  return (
    <div className="orbit-side">
      <SegmentedControl
        label="세션 보기"
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'live')}
        options={[{value: 'live', label: `진행 ${live.length}`}, {value: 'all', label: '전체'}]}
      />
      {state.status === 'error' && <p className="orbit-alert" role="alert">갱신하지 못했습니다. 마지막으로 읽은 목록입니다. {state.error}</p>}
      {rows.length === 0 ? (
        <EmptyState title={filter === 'live' ? '진행 중인 세션이 없습니다' : '세션이 없습니다'} description="Orbit 훅이 켜진 AI 세션이 여기에 나타납니다." />
      ) : (
        <ul className="orbit-session-list" aria-label="세션 목록" onKeyDown={moveFocus}>
          {rows.map(s => {
            const props = sessionRowProps(s, titles, now);
            return (
              <SessionRow
                key={s.sessionId}
                {...props}
                selected={selected === s.sessionId}
                onSelect={() => {
                  setSelected(s.sessionId);
                  ctx.tabs.open({type: 'session', id: s.sessionId, title: `${props.host} · ${props.place.name}`, params: {id: s.sessionId}});
                }}
              />
            );
          })}
        </ul>
      )}
    </div>
  );
}

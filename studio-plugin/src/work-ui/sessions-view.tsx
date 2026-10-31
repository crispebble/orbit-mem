// 사이드바 「세션」 보기: 메인 세션 단위 한 줄(forma-ai SessionRow). 기본은 끝나지 않은 세션, 「전체」는 끝난 세션도.
import {useMemo, useState, type KeyboardEvent} from 'react';
import type {ViewProps} from '@onidot-labs/orbit-studio-plugin';
import {SessionRow} from '@onidot-labs/orbit-studio-plugin/ai';
import {StateView, SegmentedControl} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {workUiText} from '../i18n/messages/work.ts';
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
  const u = useMessages(workUiText);
  const t = u.sessionsView;
  const now = useNow();
  const [filter, setFilter] = useState<Filter>('live');
  const [selected, setSelected] = useState<string>();
  const titles = useMemo(() => stepTitleMap(state), [state.details]);
  useLoadWorks(store, state.sessions.filter(s => s.state !== 'ended' && s.activeStepIds.length > 0).flatMap(s => s.openWorkIds), now);

  if (!store) return <StateView variant="loading" title={u.starting} compact headingLevel={3} />;
  if (state.status === 'paused') {
    return <div className="orbit-side"><Paused store={store} state={state} title={u.paused.sessionTitle} compact /></div>;
  }
  if (state.status === 'error' && state.sessions.length === 0) {
    return <div className="orbit-side"><StateView variant="error" title={t.readFailed} description={`${state.error ?? ''} ${t.retrySoon}`} compact headingLevel={3} /></div>;
  }
  if (state.status === 'idle' || (state.status === 'loading' && state.sessions.length === 0)) {
    return <div className="orbit-side"><StateView variant="loading" title={t.loading} compact headingLevel={3} /></div>;
  }

  // 부모가 있는 세션(다른 세션이 띄운 세션)은 부모 세션 탭의 호출 계보에서 보인다
  const mains = state.sessions.filter(s => !s.parentSessionId || !state.sessions.some(p => p.sessionId === s.parentSessionId));
  const live = mains.filter(s => s.state !== 'ended');
  const rows = filter === 'live' ? live : mains;

  return (
    <div className="orbit-side">
      <SegmentedControl
        label={t.viewLabel}
        value={filter}
        onChange={v => setFilter(v === 'all' ? 'all' : 'live')}
        options={[{value: 'live', label: t.active(live.length)}, {value: 'all', label: t.all}]}
      />
      {state.status === 'error' && <p className="orbit-alert" role="alert">{t.refreshFailed} {state.error}</p>}
      {rows.length === 0 ? (
        <StateView title={filter === 'live' ? t.noActive : t.none} description={t.noneHint} compact headingLevel={3} />
      ) : (
        <ul className="orbit-session-list" aria-label={t.listLabel} onKeyDown={moveFocus}>
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

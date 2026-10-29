// 세션 탭: work_sessions({sessionId}) 한 번으로 머리·호출 계보·연결된 작업·증거·바뀐 파일·대화 기록 위치·사건 기록을 그린다.
// 호출 계보는 forma-ui Tree로, 사건 기록은 forma-ai AgentTimeline으로 조립한다(계보 부품은 Forma에 두지 않기로 함, ONID-26).
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import type {PluginContext, TabProps} from '@onidot-labs/studio-plugin';
import {AgentTimeline} from '@onidot-labs/studio-plugin/ai';
import {Badge, Button, EmptyState, PropertyList, TextLink, Tree, type TreeNode} from '@onidot-labs/studio-plugin/ui';
import {errorMessage} from '../oni.ts';
import {WorkToolError} from '../work/contract.ts';
import {formatTime} from '../work/display.ts';
import {commitsOf} from '../work/evidence.ts';
import {lineageOf, sessionRowProps, surfaceLabel, timelineOf, type LineageItem} from '../work/session-view.ts';
import {hostLabel} from '../work/graph.ts';
import type {WorkState} from '../work/store.ts';
import type {Evidence, Run, Session, SessionEvent} from '../work/types.ts';
import {VerificationItem, type StageNav} from './stage-detail.tsx';
import {stepTitleMap, useLoadWorks, useNow, useWork} from './runtime.ts';
import {workNav} from './work-tab.tsx';

export interface Page {
  cursor?: string;
  events: SessionEvent[];
  nextCursor?: string;
}

export interface Loaded {
  session: Session;
  runs: Run[];
  evidence: Evidence[];
  pages: Page[];
}

function lineageNode(item: LineageItem): TreeNode {
  return {
    id: item.id,
    label: item.label,
    icon: <span className="orbit-lin-dot" data-ended={item.ended || undefined} aria-hidden="true" />,
    meta: <span className={item.ended ? 'orbit-lin-meta orbit-lin-ended' : 'orbit-lin-meta'}>{item.detail}{item.background ? ' · 뒤에서' : ''}</span>,
    children: item.children.map(lineageNode),
  };
}

function findItem(item: LineageItem, id: string): LineageItem | undefined {
  if (item.id === id) return item;
  for (const c of item.children) {
    const hit = findItem(c, id);
    if (hit) return hit;
  }
  return undefined;
}

async function copy(ctx: PluginContext, text: string) {
  try {
    await navigator.clipboard.writeText(text);
    ctx.notify({title: '대화 기록 위치를 복사했습니다', body: text, tone: 'info'});
  } catch (e) {
    ctx.notify({title: '복사하지 못했습니다', body: e instanceof Error ? e.message : String(e), tone: 'warning'});
  }
}

export function SessionTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  const {store, state} = useWork();
  const now = useNow();
  const [loaded, setLoaded] = useState<Loaded>();
  const [error, setError] = useState<string>();
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const loadedRef = useRef<Loaded>(undefined);
  loadedRef.current = loaded;

  /** 쪽 하나를 읽는다. replaceFrom이 있으면 그 쪽부터 갈아 끼운다(실시간 갱신은 마지막 쪽만 다시 읽는다). */
  const fetchPage = useCallback(async (cursor: string | undefined, replaceFrom: number) => {
    if (!store) return;
    setBusy(true);
    try {
      const page = await store.loadSession(id, cursor);
      const prev = loadedRef.current;
      const pages = [...(prev?.pages.slice(0, replaceFrom) ?? []), {cursor, events: page.events, nextCursor: page.nextCursor}];
      setLoaded({
        session: page.session,
        runs: page.runs ?? prev?.runs ?? [],
        evidence: page.evidence ?? prev?.evidence ?? [],
        pages,
      });
      setError(undefined);
      setMissing(false);
    } catch (e) {
      if (e instanceof WorkToolError && e.code === 'NOT_FOUND') setMissing(true);
      else setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }, [store, id]);

  // 처음 읽기
  useEffect(() => { void fetchPage(undefined, 0); }, [fetchPage]);

  // 이 세션이 바뀌었다는 갱신(store.sessions의 lastSeenAt·state)이 오면 마지막 쪽을 다시 읽는다
  const live = state.sessions.find(s => s.sessionId === id);
  const liveKey = live ? `${live.lastSeenAt}|${live.state}|${live.activeStepIds.join(',')}` : '';
  useEffect(() => {
    const cur = loadedRef.current;
    if (!liveKey || !cur) return;
    if (cur.session.lastSeenAt === live?.lastSeenAt && cur.session.state === live?.state) return;
    const last = cur.pages.length - 1;
    void fetchPage(cur.pages[last]?.cursor, last);
  }, [liveKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!loaded) return;
    const p = sessionRowProps(loaded.session, new Map(), Date.now());
    ctx.tabs.setTitle('session', tab.id, `${p.host} · ${p.place.name}`);
  }, [ctx, tab.id, loaded?.session.sessionId, loaded?.session.repo, loaded?.session.cwd]);

  const titles = useMemo(() => stepTitleMap(state), [state.details]);
  const nav = useMemo(() => workNav(ctx, () => {}), [ctx]);
  useLoadWorks(store, loaded?.session.openWorkIds ?? [], state.details);

  if (!store) return <EmptyState title="Orbit을 켜는 중입니다" />;
  if (state.status === 'paused') return <EmptyState title="세션 기록을 읽지 않습니다" description={state.pause?.message} />;
  if (missing) return <EmptyState title="세션을 찾지 못했습니다" description="Orbit 기록에 없는 세션입니다." />;
  if (!loaded) {
    if (error) return <EmptyState title="세션을 읽지 못했습니다" description={error}><Button variant="secondary" size="sm" onClick={() => void fetchPage(undefined, 0)}>다시 시도</Button></EmptyState>;
    return <EmptyState title="세션을 불러오는 중입니다" />;
  }

  return (
    <SessionBody ctx={ctx} loaded={loaded} now={now} titles={titles} state={state} nav={nav} busy={busy} error={error}
      onReload={() => void fetchPage(undefined, 0)}
      onMore={next => void fetchPage(next, loaded.pages.length)} />
  );
}

export interface SessionBodyProps {
  ctx: PluginContext;
  loaded: Loaded;
  now: number;
  titles: ReadonlyMap<string, string>;
  state: Pick<WorkState, 'works' | 'details'>;
  nav: StageNav;
  busy?: boolean;
  error?: string;
  onReload(): void;
  onMore(cursor: string): void;
}

/** 세션 탭 본문(읽기와 나눠 둔 그리기) */
export function SessionBody({ctx, loaded, now, titles, state, nav, busy, error, onReload, onMore}: SessionBodyProps) {
  const s = loaded.session;
  const row = sessionRowProps(s, titles, now);
  const tree = lineageOf(s, loaded.runs, now);
  const events = loaded.pages.flatMap(p => p.events);
  const next = loaded.pages.at(-1)?.nextCursor;
  const commits = commitsOf(loaded.evidence);
  const transcripts = [...new Set(events.flatMap(e => [e.ref?.transcript, e.ref?.agentTranscript]).filter((x): x is string => !!x))];
  const runById = new Map(loaded.runs.map(r => [r.id, r]));
  const workTitle = (wid: string) => state.works.find(w => w.id === wid)?.title ?? state.details[wid]?.title;

  return (
    <div className="orbit-pane orbit-session-tab">
      <header className="orbit-row orbit-work-head-row">
        <h2 className="orbit-work-title">{hostLabel(s.host)} · {surfaceLabel(s.surface)}</h2>
        <Badge tone={row.status.tone}>{row.status.label}</Badge>
        {row.endReason && <span className="orbit-meta">{row.endReason}</span>}
        {row.lastSeen && <span className="orbit-work-warn">{row.lastSeen}</span>}
      </header>
      {error && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">갱신하지 못했습니다. 마지막으로 읽은 내용입니다. {error}</p>
          <Button variant="secondary" size="sm" onClick={onReload}>다시 읽기</Button>
        </div>
      )}
      <PropertyList items={[
        {label: row.place.kind === 'repo' ? '레포' : '폴더', value: <span>{row.place.name}{row.place.kind === 'repo' && row.place.branch ? <span className="orbit-mono"> · {row.place.branch}</span> : null}</span>},
        {label: '디렉토리', value: <span className="orbit-mono orbit-work-break">{s.cwd ?? '알 수 없음'}</span>},
        {label: '시작', value: formatTime(s.startedAt)},
        {label: '경과', value: row.elapsed ?? '알 수 없음'},
        ...(s.model ? [{label: '모델', value: s.model}] : []),
        {label: '세션 id', value: <span className="orbit-mono orbit-work-break">{s.sessionId}</span>},
      ]} />

      <section aria-labelledby="orbit-lin-h">
        <h3 id="orbit-lin-h" className="orbit-work-sec">호출 계보</h3>
        <Tree
          label="호출 계보"
          nodes={[lineageNode(tree)]}
          defaultExpandedIds={[tree.id]}
          autoHeight
          onSelect={nid => {
            const item = findItem(tree, nid);
            if (item?.sessionId && item.sessionId !== s.sessionId) nav.openSession(item.sessionId, item.label);
          }}
        />
      </section>

      <section aria-labelledby="orbit-links-h">
        <h3 id="orbit-links-h" className="orbit-work-sec">연결된 작업</h3>
        {s.openWorkIds.length === 0 ? <p className="orbit-meta">이 세션이 연 작업이 없습니다.</p> : (
          <ul className="orbit-work-runs">
            {s.openWorkIds.map(wid => (
              <li key={wid} className="orbit-row">
                <TextLink onClick={() => nav.openWork(wid, workTitle(wid))}>{workTitle(wid) ?? '작업 열기'}</TextLink>
              </li>
            ))}
          </ul>
        )}
        {row.step && <p className="orbit-meta">지금 하는 단계: {row.step}</p>}
      </section>

      <section aria-labelledby="orbit-ev-h">
        <h3 id="orbit-ev-h" className="orbit-work-sec">증거 {loaded.evidence.length}건</h3>
        {loaded.evidence.length === 0 ? <p className="orbit-meta">남은 증거가 없습니다.</p> : (
          <ul className="orbit-work-evidence-list">
            {loaded.evidence.map(e => <VerificationItem key={e.id} ctx={ctx} evidence={e} run={e.runId ? runById.get(e.runId) : undefined} now={now} nav={nav} />)}
          </ul>
        )}
      </section>

      {commits.some(c => c.files.length > 0) && (
        <section aria-labelledby="orbit-files-h">
          <h3 id="orbit-files-h" className="orbit-work-sec">바뀐 파일</h3>
          {commits.filter(c => c.files.length > 0).map(c => (
            <div key={c.id} className="orbit-work-files">
              <div className="orbit-row">
                <span className="orbit-mono">{c.sha.slice(0, 7)}</span>
                {c.summary && <span className="orbit-work-text">{c.summary}</span>}
                <TextLink onClick={() => void ctx.studio.openCommit(c.repo, c.sha).then(ok => {
                  if (!ok) ctx.notify({title: '커밋을 열지 못했습니다', body: c.repo, tone: 'warning'});
                })}>diff 열기</TextLink>
              </div>
              <ul className="orbit-work-file-list">{c.files.map(f => <li key={f} className="orbit-mono">{f}</li>)}</ul>
            </div>
          ))}
        </section>
      )}

      {transcripts.length > 0 && (
        <section aria-labelledby="orbit-tr-h">
          <h3 id="orbit-tr-h" className="orbit-work-sec">대화 기록 위치</h3>
          <ul className="orbit-work-runs">
            {transcripts.map(t => (
              <li key={t} className="orbit-row">
                <span className="orbit-mono orbit-work-break">{t}</span>
                <TextLink tone="muted" onClick={() => void copy(ctx, t)}>복사</TextLink>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section aria-labelledby="orbit-tl-h">
        <h3 id="orbit-tl-h" className="orbit-work-sec">사건 기록</h3>
        {events.length === 0 ? <p className="orbit-meta">남은 사건이 없습니다.</p> : <AgentTimeline title="" events={timelineOf(events, titles)} surface="plain" />}
        {next && <Button variant="secondary" size="sm" disabled={busy} onClick={() => onMore(next)}>사건 더 보기</Button>}
      </section>
    </div>
  );
}

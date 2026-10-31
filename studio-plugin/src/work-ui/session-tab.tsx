// 세션 탭: work_sessions({sessionId}) 한 번으로 머리·호출 계보·연결된 작업·증거·바뀐 파일·대화 기록 위치·사건 기록을 그린다.
// 호출 계보는 forma-ui Tree로, 사건 기록은 forma-ai AgentTimeline으로 조립한다(계보 부품은 Forma에 두지 않기로 함, ONID-26).
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import type {PluginContext, TabProps} from '@onidot-labs/studio-plugin';
import {AgentTimeline} from '@onidot-labs/studio-plugin/ai';
import {Badge, Button, StateView, PropertyList, TextLink, Tree, type TreeNode} from '@onidot-labs/studio-plugin/ui';
import {pick, useMessages} from '../i18n/index.ts';
import {workKitText, workText, workUiText} from '../i18n/messages/work.ts';
import {errorMessage} from '../orb.ts';
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
import {Paused} from './paused.tsx';

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

function lineageNode(item: LineageItem, backgroundText: string): TreeNode {
  return {
    id: item.id,
    label: item.label,
    icon: <span className="orbit-lin-dot" data-ended={item.ended || undefined} aria-hidden="true" />,
    meta: <span className={item.ended ? 'orbit-lin-meta orbit-lin-ended' : 'orbit-lin-meta'}>{item.detail}{item.background ? backgroundText : ''}</span>,
    children: item.children.map(c => lineageNode(c, backgroundText)),
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
  const t = pick(workUiText).copy;
  try {
    await navigator.clipboard.writeText(text);
    ctx.notify({title: t.copied(t.transcriptLocation), body: text, tone: 'info'});
  } catch (e) {
    ctx.notify({title: t.failed(t.transcriptLocation), body: e instanceof Error ? e.message : String(e), tone: 'warning'});
  }
}

export function SessionTab({ctx, tab}: TabProps) {
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  const {store, state} = useWork();
  const u = useMessages(workUiText);
  const t = u.sessionTab;
  const kit = useMessages(workKitText);
  const now = useNow();
  const [loaded, setLoaded] = useState<Loaded>();
  const [error, setError] = useState<string>();
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  // 읽기 줄이 마지막으로 정한 값. 화면 상태(loaded)보다 먼저 바뀌므로 다음 읽기가 이것을 기준으로 한다.
  const loadedRef = useRef<Loaded>(undefined);

  const queue = useRef<Promise<void>>(Promise.resolve());
  const pending = useRef(0);
  /** 읽기를 한 줄로 세운다: 실시간 갱신과 「사건 더 보기」가 겹쳐 서로의 쪽을 덮어쓰지 않게 한다 */
  const enqueue = useCallback((job: () => Promise<void>) => {
    pending.current += 1;
    setBusy(true);
    queue.current = queue.current.then(job).catch((e: unknown) => {
      if (e instanceof WorkToolError && e.code === 'NOT_FOUND') setMissing(true);
      else setError(errorMessage(e));
    }).finally(() => {
      pending.current -= 1;
      if (pending.current === 0) setBusy(false);
    });
  }, []);

  const keep = (next: Loaded) => {
    loadedRef.current = next;
    setLoaded(next);
    setError(undefined);
    setMissing(false);
  };

  /** 첫 쪽부터 지금까지 읽은 쪽 수만큼 다시 읽는다. runs·evidence는 첫 쪽에 모두 있어 계보·증거도 새로 된다. */
  const reload = useCallback(() => enqueue(async () => {
    if (!store) return;
    const want = Math.max(1, loadedRef.current?.pages.length ?? 1);
    const first = await store.loadSession(id);
    const pages: Page[] = [{events: first.events, nextCursor: first.nextCursor}];
    for (let cursor = first.nextCursor; cursor && pages.length < want; cursor = pages.at(-1)!.nextCursor) {
      const page = await store.loadSession(id, cursor);
      pages.push({cursor, events: page.events, nextCursor: page.nextCursor});
    }
    keep({session: first.session, runs: first.runs ?? [], evidence: first.evidence ?? [], pages});
  }), [enqueue, store, id]);

  /** 다음 사건 쪽을 덧붙인다 */
  const more = useCallback(() => enqueue(async () => {
    const cur = loadedRef.current;
    const cursor = cur?.pages.at(-1)?.nextCursor;
    if (!store || !cur || !cursor) return;
    const page = await store.loadSession(id, cursor);
    keep({...cur, pages: [...cur.pages, {cursor, events: page.events, nextCursor: page.nextCursor}]});
  }), [enqueue, store, id]);

  // 처음 읽기
  useEffect(() => { reload(); }, [reload]);

  // 이 세션이 바뀌었다는 갱신(store.sessions의 lastSeenAt·state)이 오면 다시 읽는다
  const live = state.sessions.find(s => s.sessionId === id);
  const liveKey = live ? `${live.lastSeenAt}|${live.state}|${live.activeStepIds.join(',')}` : '';
  useEffect(() => {
    const cur = loadedRef.current;
    if (!liveKey || !cur) return;
    if (cur.session.lastSeenAt === live?.lastSeenAt && cur.session.state === live?.state) return;
    reload();
  }, [liveKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!loaded) return;
    const p = sessionRowProps(loaded.session, new Map(), Date.now());
    ctx.tabs.setTitle('session', tab.id, `${p.host} · ${p.place.name}`);
  }, [ctx, tab.id, loaded?.session.sessionId, loaded?.session.repo, loaded?.session.cwd]);

  const titles = useMemo(() => stepTitleMap(state), [state.details]);
  const nav = useMemo(() => workNav(ctx, () => {}), [ctx]);
  useLoadWorks(store, loaded?.session.openWorkIds ?? [], now);

  if (!store) return <StateView variant="loading" title={u.starting} />;
  if (state.status === 'paused') return <Paused store={store} state={state} title={u.paused.sessionTitle} />;
  if (missing) return <StateView title={t.notFound} description={t.notFoundHint} />;
  if (!loaded) {
    if (error) return <StateView variant="error" title={t.readFailed} description={error} onRetry={reload} retryLabel={kit.retry} />;
    return <StateView variant="loading" title={t.loading} />;
  }

  return (
    <SessionBody ctx={ctx} loaded={loaded} now={now} titles={titles} state={state} nav={nav} busy={busy} error={error}
      onReload={reload}
      onMore={more} />
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
  onMore(): void;
}

/** 세션 탭 본문(읽기와 나눠 둔 그리기) */
export function SessionBody({ctx, loaded, now, titles, state, nav, busy, error, onReload, onMore}: SessionBodyProps) {
  const u = useMessages(workUiText);
  const t = u.sessionTab;
  const unknown = useMessages(workText).unknown;
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
          <p className="orbit-alert" role="alert">{t.refreshFailed} {error}</p>
          <Button variant="secondary" size="sm" onClick={onReload}>{t.reread}</Button>
        </div>
      )}
      <PropertyList items={[
        {label: row.place.kind === 'repo' ? t.repo : t.folder, value: <span>{row.place.name}{row.place.kind === 'repo' && row.place.branch ? <span className="orbit-mono"> · {row.place.branch}</span> : null}</span>},
        {label: t.directory, value: <span className="orbit-mono orbit-work-break">{s.cwd ?? unknown}</span>},
        {label: t.started, value: formatTime(s.startedAt)},
        {label: t.elapsed, value: row.elapsed ?? unknown},
        ...(s.model ? [{label: t.model, value: s.model}] : []),
        {label: t.sessionId, value: <span className="orbit-mono orbit-work-break">{s.sessionId}</span>},
      ]} />

      <section aria-labelledby="orbit-lin-h">
        <h3 id="orbit-lin-h" className="orbit-work-sec">{t.lineage}</h3>
        <Tree
          label={t.lineage}
          nodes={[lineageNode(tree, pick(workText).sessionRow.background)]}
          defaultExpandedIds={[tree.id]}
          autoHeight
          onSelect={nid => {
            const item = findItem(tree, nid);
            if (item?.sessionId && item.sessionId !== s.sessionId) nav.openSession(item.sessionId, item.label);
          }}
        />
      </section>

      <section aria-labelledby="orbit-links-h">
        <h3 id="orbit-links-h" className="orbit-work-sec">{t.linkedWorks}</h3>
        {s.openWorkIds.length === 0 ? <p className="orbit-meta">{t.noLinkedWorks}</p> : (
          <ul className="orbit-work-runs">
            {s.openWorkIds.map(wid => (
              <li key={wid} className="orbit-row">
                <TextLink onClick={() => nav.openWork(wid, workTitle(wid))}>{workTitle(wid) ?? t.openWork}</TextLink>
              </li>
            ))}
          </ul>
        )}
        {row.step && <p className="orbit-meta">{t.currentStep(row.step)}</p>}
      </section>

      <section aria-labelledby="orbit-ev-h">
        <h3 id="orbit-ev-h" className="orbit-work-sec">{t.evidence(loaded.evidence.length)}</h3>
        {loaded.evidence.length === 0 ? <p className="orbit-meta">{t.noEvidence}</p> : (
          <ul className="orbit-work-evidence-list">
            {loaded.evidence.map(e => <VerificationItem key={e.id} ctx={ctx} evidence={e} run={e.runId ? runById.get(e.runId) : undefined} now={now} nav={nav} />)}
          </ul>
        )}
      </section>

      {commits.some(c => c.files.length > 0) && (
        <section aria-labelledby="orbit-files-h">
          <h3 id="orbit-files-h" className="orbit-work-sec">{t.changedFilesHeading}</h3>
          {commits.filter(c => c.files.length > 0).map(c => (
            <div key={c.id} className="orbit-work-files">
              <div className="orbit-row">
                <span className="orbit-mono">{c.sha.slice(0, 7)}</span>
                {c.summary && <span className="orbit-work-text">{c.summary}</span>}
                <TextLink onClick={() => void ctx.studio.openCommit(c.repo, c.sha).then(ok => {
                  if (!ok) ctx.notify({title: u.openCommitFailed, body: c.repo, tone: 'warning'});
                })}>{t.openDiff}</TextLink>
              </div>
              <ul className="orbit-work-file-list">{c.files.map(f => <li key={f} className="orbit-mono">{f}</li>)}</ul>
            </div>
          ))}
        </section>
      )}

      {transcripts.length > 0 && (
        <section aria-labelledby="orbit-tr-h">
          <h3 id="orbit-tr-h" className="orbit-work-sec">{t.transcripts}</h3>
          <ul className="orbit-work-runs">
            {transcripts.map(path => (
              <li key={path} className="orbit-row">
                <span className="orbit-mono orbit-work-break">{path}</span>
                <TextLink tone="muted" onClick={() => void copy(ctx, path)}>{t.copy}</TextLink>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section aria-labelledby="orbit-tl-h">
        <h3 id="orbit-tl-h" className="orbit-work-sec">{t.timeline}</h3>
        {events.length === 0 ? <p className="orbit-meta">{t.noEvents}</p> : <AgentTimeline title="" events={timelineOf(events, titles)} surface="plain" />}
        {next && <Button variant="secondary" size="sm" disabled={busy} onClick={onMore}>{t.moreEvents}</Button>}
      </section>
    </div>
  );
}

// 실행 하나를 보이는 화면: 머리(요청·모드·프로필·상태·합계), 조작(멈춤·이어가기), 기록, 열린 결정 카드.
import {useCallback, useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Badge, Button, Callout, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import type {RunSummary} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {useMessages} from '../i18n/index.ts';
import {chatText} from '../i18n/messages/chat.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {resumeInput} from './composer.ts';
import {daemonErrorText} from './errors.ts';
import {createRunFeed, type FeedState, type RunFeed} from './feed.ts';
import {formatCount, formatMinutes, formatUsd} from './format.ts';
import {buildTimeline, canResume, canStop, runState, runTotals} from './timeline.ts';
import {Transcript, type TranscriptActions} from './transcript.tsx';

const LOADING: FeedState = {events: [], loading: true};
const noop = () => {};

function useRunFeed(client: DaemonClient, runId: string): {state: FeedState; reload: () => void} {
  const [feed, setFeed] = useState<RunFeed>();
  useEffect(() => {
    const next = createRunFeed(client, runId);
    setFeed(next);
    return () => next.dispose();
  }, [client, runId]);
  const state = useSyncExternalStore(
    cb => feed?.subscribe(cb) ?? noop,
    () => feed?.getState() ?? LOADING,
    () => feed?.getState() ?? LOADING,
  );
  return {state, reload: () => void feed?.reload()};
}

const stateTone = (s: string) => (s === 'completed' ? 'success' : s === 'failed' ? 'danger' : s === 'running' ? 'accent' : 'warning');

export function RunView({client, runId, onStarted}: {client: DaemonClient; runId: string; onStarted(runId: string): void}) {
  const t = useMessages(chatText);
  const d = useMessages(daemonText);
  const {state, reload} = useRunFeed(client, runId);
  const [busy, setBusy] = useState<'stop' | 'resume'>();
  const [problem, setProblem] = useState<string>();
  const entries = useMemo(() => buildTimeline(state.events), [state.events]);
  const run: RunSummary | undefined = state.summary;
  const status = runState(entries, run);
  const totals = runTotals(entries, run);
  const started = entries.find(e => e.kind === 'started');
  const request = run?.request || (started?.kind === 'started' ? started.request : '');
  const mode = run?.mode || (started?.kind === 'started' ? started.mode : '');
  const profile = run?.profile || (started?.kind === 'started' ? started.profile : '');
  const cwd = run?.cwd || (started?.kind === 'started' ? started.cwd : '');
  const finished = entries.find(e => e.kind === 'finished');
  const workId = (finished?.kind === 'finished' ? finished.workId : undefined) || run?.workId || '';
  const answerable = status === 'running';

  const actions: TranscriptActions = useMemo(
    () => ({
      text: (key, args, fallback) => client.text(key, args, fallback),
      optionLabel: id => client.optionLabel(id),
      answer: (decisionId, answer) => client.api.answer({runId, decisionId, answer}),
      answerable,
    }),
    [client, runId, answerable],
  );

  const stop = useCallback(async () => {
    setBusy('stop');
    setProblem(undefined);
    try {
      await client.api.stopRun({runId});
    } catch (error) {
      setProblem(daemonErrorText(error, d.errors));
    } finally {
      setBusy(undefined);
    }
  }, [client, runId, d]);

  const resume = useCallback(async () => {
    setBusy('resume');
    setProblem(undefined);
    try {
      await client.ensure();
      const r = await client.api.startRun(resumeInput({cwd, profile, workId, request}));
      onStarted(r.runId);
    } catch (error) {
      setProblem(daemonErrorText(error, d.errors));
    } finally {
      setBusy(undefined);
    }
  }, [client, cwd, profile, workId, request, onStarted, d]);

  if (state.loading && state.events.length === 0) return <StateView variant="loading" title={t.run.loadingHistory} compact headingLevel={3} />;
  if (state.error && state.events.length === 0) {
    return <StateView variant="error" title={t.run.loadFailed} description={daemonErrorText(state.error, d.errors)} onRetry={reload} retryLabel={t.run.retry} compact headingLevel={3} />;
  }

  return (
    <div className="orbit-chat-run">
      <header className="orbit-chat-run-head">
        <p className="orbit-chat-request orbit-work-text">{request}</p>
        <div className="orbit-row">
          <Badge tone={stateTone(status)}>{t.run.state[status] ?? status}</Badge>
          {mode && <Badge tone="neutral">{mode}</Badge>}
          {profile && <span className="orbit-meta">{t.run.profile}: {profile}</span>}
          {cwd && <span className="orbit-meta orbit-chat-cwd" title={cwd}>{cwd}</span>}
        </div>
        <dl className="orbit-chat-totals" aria-label={t.run.totals}>
          <div><dt>{t.run.cost}</dt><dd>{totals.subscription && totals.costUsd === 0 ? t.run.subscription : formatUsd(totals.costUsd)}</dd></div>
          <div><dt>{t.run.turns}</dt><dd>{formatCount(totals.turns)}</dd></div>
          <div><dt>{t.run.minutes}</dt><dd>{formatMinutes(totals.minutes)}</dd></div>
          <div><dt>{t.run.tokens}</dt><dd>{formatCount(totals.inputTokens)} / {formatCount(totals.outputTokens)}</dd></div>
          {totals.monthSpentUsd !== undefined && (
            <div><dt>{t.run.month}</dt><dd>{formatUsd(totals.monthSpentUsd)}{totals.monthLimitUsd ? ` / ${formatUsd(totals.monthLimitUsd)}` : ''}</dd></div>
          )}
        </dl>
        <div className="orbit-row">
          {canStop(status) && <Button variant="danger" size="sm" disabled={busy !== undefined} onClick={() => void stop()} aria-label={t.run.stop}>{busy === 'stop' ? t.run.stopping : t.run.stop}</Button>}
          {canResume(status, workId) && <Button variant="primary" size="sm" disabled={busy !== undefined} onClick={() => void resume()} aria-label={t.run.resume}>{t.run.resume}</Button>}
        </div>
        {problem && <Callout tone="danger" title={problem} />}
      </header>
      {entries.length === 0 ? <p className="orbit-meta">{t.run.empty}</p> : <Transcript entries={entries} actions={actions} />}
    </div>
  );
}

// 오른쪽 도구 창 「작업 대화」(plugin.json toolWindows `chat`): 머리(데몬 상태·실행 고르기·새 작업) + 새 작업 입력 또는 실행 기록.
import {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {PanelProps} from '@onidot-labs/orbit-studio-plugin';
import {Badge, Button, Callout, Select, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import type {RunSummary} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {useDaemon} from '../daemon/runtime.ts';
import {useMessages} from '../i18n/index.ts';
import {chatText} from '../i18n/messages/chat.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {emptyForm} from './composer.ts';
import {ComposerView} from './composer-view.tsx';
import {daemonErrorText} from './errors.ts';
import {oneLine} from './format.ts';
import {RunView} from './run-view.tsx';
import {pendingChat, subscribeChat, takeChat} from './store.ts';
import {viewForTarget, type View} from './target.ts';
import {sortRuns} from './timeline.ts';

function useRuns(client: DaemonClient | undefined, up: boolean) {
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const load = useCallback(() => {
    if (!client || !up) return;
    client.api.listRuns({limit: 50}).then(r => setRuns(sortRuns(r.runs)), () => {});
  }, [client, up]);
  useEffect(() => {
    load();
    if (!client) return;
    // 실행이 시작·끝나면 목록을 다시 읽는다(이벤트가 몰릴 때를 위해 잠깐 모은다)
    const off = client.onEvent(e => {
      const type = e.event?.type;
      if (type !== 'run.started' && type !== 'run.finished') return;
      clearTimeout(timer.current);
      timer.current = setTimeout(load, 300);
    });
    return () => {
      off();
      clearTimeout(timer.current);
    };
  }, [client, load]);
  return {runs, reload: load};
}

export function ChatToolWindow({ctx}: PanelProps) {
  const t = useMessages(chatText);
  const d = useMessages(daemonText);
  const {client, status} = useDaemon();
  const [view, setView] = useState<View>({kind: 'compose', form: emptyForm()});
  const [starting, setStarting] = useState(false);
  const [problem, setProblem] = useState<string>();
  const {runs, reload} = useRuns(client, status.daemon === 'up' && status.paired);
  const request = useSyncExternalStore(subscribeChat, pendingChat, pendingChat);

  // openChat이 남긴 대상을 화면에 맞춘다. 대상을 가져가면 request가 비어 이 효과가 다시 도므로, 늦게 온 결과는 번호로 가린다.
  const applied = useRef(0);
  useEffect(() => {
    if (!request || !client) return;
    const n = ++applied.current;
    takeChat(request);
    void viewForTarget(client, request).then(next => {
      if (n === applied.current) setView(next);
    });
  }, [request, client]);

  if (!client) return <StateView variant="loading" title={t.loading} compact headingLevel={3} />;

  const startedRun = (runId: string) => {
    setView({kind: 'run', runId});
    reload();
  };
  const currentRunId = view.kind === 'run' ? view.runId : '';
  const options = runs.map(r => ({value: r.runId, label: `${r.state === 'running' ? '● ' : ''}${oneLine(r.request || r.workId || r.runId, 36)} · ${t.run.state[r.state] ?? r.state}`}));
  // 목록에 아직 없는 실행(방금 시작)도 고른 값으로 보이게 한다
  if (currentRunId && !options.some(o => o.value === currentRunId)) options.unshift({value: currentRunId, label: currentRunId});

  const startDaemon = async () => {
    setStarting(true);
    setProblem(undefined);
    try {
      await client.ensure();
    } catch (error) {
      setProblem(daemonErrorText(error, d.errors));
    } finally {
      setStarting(false);
    }
  };

  const tone = status.daemon === 'up' ? 'success' : status.daemon === 'starting' ? 'warning' : 'danger';
  return (
    <div className="orbit-chat" aria-label={t.title}>
      <header className="orbit-chat-head">
        <div className="orbit-row">
          <Badge tone={tone}>{d.status[status.daemon]}</Badge>
          {status.daemon === 'down' && (
            <Button size="sm" variant="secondary" loading={starting} onClick={() => void startDaemon()}>{d.status.start}</Button>
          )}
          <span className="orbit-grow" />
          <Button size="sm" variant={view.kind === 'compose' ? 'soft' : 'secondary'} onClick={() => setView({kind: 'compose', form: emptyForm()})}>{t.head.newTask}</Button>
        </div>
        <Select
          label={t.head.runs}
          value={currentRunId}
          onValueChange={runId => setView({kind: 'run', runId})}
          options={options}
          placeholder={runs.length === 0 ? t.head.noRuns : t.head.pickRun}
          disabled={options.length === 0}
        />
        {status.daemon === 'up' && !status.streaming && <Callout tone="warning" title={d.status.disconnected} />}
        {(problem || status.error) && <Callout tone="danger" title={problem ?? daemonErrorText(status.error, d.errors)} />}
      </header>
      <div className="orbit-chat-body">
        {view.kind === 'compose' ? (
          <ComposerView ctx={ctx} client={client} form={view.form} setForm={form => setView({kind: 'compose', form})} onStarted={startedRun} />
        ) : (
          <RunView key={view.runId} client={client} runId={view.runId} onStarted={startedRun} />
        )}
      </div>
    </div>
  );
}

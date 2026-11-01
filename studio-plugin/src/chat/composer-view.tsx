// 새 작업 입력 화면: 요청·모드·프로필·작업 폴더·상한을 받아 시작한다. 시작 전에 데몬을 띄우고 짝짓는다(client.ensure).
import {useEffect, useState} from 'react';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {Button, Callout, Input, Select, SegmentedControl} from '@onidot-labs/orbit-studio-plugin/ui';
import type {RunMode} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {useMessages} from '../i18n/index.ts';
import {chatText} from '../i18n/messages/chat.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {buildStartInput, folderCandidates, type ComposerForm, type ComposerIssue} from './composer.ts';
import {daemonErrorText} from './errors.ts';

const AUTO = '__auto__';

export function ComposerView({ctx, client, form, setForm, onStarted}: {ctx: PluginContext; client: DaemonClient; form: ComposerForm; setForm(next: ComposerForm): void; onStarted(runId: string): void}) {
  const t = useMessages(chatText);
  const d = useMessages(daemonText);
  const c = t.composer;
  const [profiles, setProfiles] = useState<string[]>([]);
  const [issues, setIssues] = useState<ComposerIssue[]>([]);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const set = (patch: Partial<ComposerForm>) => setForm({...form, ...patch});
  const candidates = folderCandidates(ctx.scope.current());
  const daemonUp = client.status().daemon;

  // 프로필 이름: 데몬이 떠 있을 때만 읽는다(여기서 데몬을 띄우지 않는다)
  useEffect(() => {
    if (daemonUp !== 'up') return;
    let alive = true;
    client.api.getProfiles().then(v => alive && setProfiles(v.file.profiles.map(p => p.name)), () => {});
    return () => {
      alive = false;
    };
  }, [client, daemonUp]);

  // 폴더가 비어 있으면 범위의 첫 프로젝트 폴더로 시작한다
  useEffect(() => {
    if (!form.cwd && candidates[0]) setForm({...form, cwd: candidates[0]});
    // 처음 한 번만 채운다: 사용자가 지운 칸을 다시 채우지 않는다
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const start = async () => {
    if (busy) return;
    const built = buildStartInput(form);
    if (!built.ok) {
      setIssues(built.issues);
      return;
    }
    setIssues([]);
    setProblem(undefined);
    setBusy(true);
    try {
      await client.ensure();
      const r = await client.api.startRun(built.input);
      onStarted(r.runId);
    } catch (error) {
      setProblem(daemonErrorText(error, d.errors));
    } finally {
      setBusy(false);
    }
  };

  const err = (issue: ComposerIssue) => (issues.includes(issue) ? c.issue[issue] : undefined);
  const modeOptions: {value: string; label: React.ReactNode}[] = [
    {value: 'ultrawork', label: <span title={c.modeTip.ultrawork}>{c.modeUltrawork}</span>},
    {value: 'plan', label: <span title={c.modeTip.plan}>{c.modePlan}</span>},
    {value: 'plan-approve', label: <span title={c.modeTip['plan-approve']}>{c.modePlanApprove}</span>},
  ];

  return (
    <form className="orbit-chat-composer" onSubmit={event => { event.preventDefault(); void start(); }}>
      {form.resumeWorkId && <Callout tone="info" title={c.resumeBanner(form.resumeWorkId)} />}
      <div className="orbit-field">
        <label className="orbit-field-label" htmlFor="orbit-chat-request">{c.request}</label>
        <textarea
          id="orbit-chat-request"
          className="orbit-textarea"
          aria-label={c.request}
          aria-invalid={issues.includes('request') || undefined}
          rows={5}
          placeholder={form.resumeWorkId ? c.requestResume : c.requestHint}
          value={form.request}
          onChange={event => set({request: event.currentTarget.value})}
          onKeyDown={event => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
              event.preventDefault();
              void start();
            }
          }}
        />
        {err('request') && <p className="orbit-alert" role="alert">{err('request')}</p>}
      </div>
      <SegmentedControl label={c.mode} value={form.mode} options={modeOptions} onValueChange={v => set({mode: (v === 'plan' || v === 'plan-approve' ? v : 'ultrawork') as RunMode})} />
      <p className="orbit-meta">{c.modeTip[form.mode]}</p>
      <Select
        label={c.profile}
        showLabel
        value={form.profile || AUTO}
        onValueChange={v => set({profile: v === AUTO ? '' : v})}
        options={[{value: AUTO, label: c.profileAuto}, ...profiles.map(p => ({value: p, label: p}))]}
      />
      <Input label={c.folder} placeholder={c.folderHint} value={form.cwd} error={err('cwd')} onChange={event => set({cwd: event.currentTarget.value})} />
      {candidates.length > 0 && (
        <div className="orbit-row" role="group" aria-label={c.folder}>
          {candidates.map(dir => (
            <Button key={dir} type="button" variant={form.cwd === dir ? 'soft' : 'ghost'} size="sm" onClick={() => set({cwd: dir})} title={dir}>
              {dir.split(/[\\/]/).filter(Boolean).pop() ?? dir}
            </Button>
          ))}
        </div>
      )}
      <fieldset className="orbit-fieldset">
        <legend className="orbit-meta">{c.limits}</legend>
        <div className="orbit-chat-limits">
          <Input label={c.maxCost} inputMode="decimal" value={form.maxCostUsd} error={err('cost')} onChange={event => set({maxCostUsd: event.currentTarget.value})} />
          <Input label={c.maxTurns} inputMode="numeric" value={form.maxTurns} error={err('turns')} onChange={event => set({maxTurns: event.currentTarget.value})} />
          <Input label={c.maxMinutes} inputMode="decimal" value={form.maxMinutes} error={err('minutes')} onChange={event => set({maxMinutes: event.currentTarget.value})} />
        </div>
      </fieldset>
      {problem && <Callout tone="danger" title={problem} />}
      <div className="orbit-row">
        <Button type="submit" variant="primary" loading={busy} aria-label={form.resumeWorkId ? undefined : c.start}>{busy ? c.starting : form.resumeWorkId ? c.resumeStart : c.start}</Button>
      </div>
    </form>
  );
}

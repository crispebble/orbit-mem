// 사이드바 보기 `orbit/profiles`: 프로필 목록과 이번 달 사용량. 누르면 편집 탭(`profiles`, params {profile})을 연다.
import {useState} from 'react';
import type {ViewProps} from '@onidot-labs/orbit-studio-plugin';
import {Badge, Button, Callout, Input, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import {Plus} from 'lucide-react';
import type {DaemonApi, ProfileUsage, ProfilesView as ProfilesData} from '../daemon/api.ts';
import {useDaemon} from '../daemon/runtime.ts';
import {daemonErrorText} from '../chat/errors.ts';
import {formatUsd} from '../chat/format.ts';
import {useMessages} from '../i18n/index.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {profilesText} from '../i18n/messages/profiles.ts';
import {notifyProfilesChanged, useProfilesData} from './data.ts';
import {addProfile, defaultName} from './edit-model.ts';

/** 한도 대비 사용량 한 줄: 「$1.20 / $5.00」, 한도가 없으면 「$1.20 (한도 없음)」. */
export function usageLine(spent: number, limit: number, t: {of: string; noLimit: string}): string {
  return limit > 0 ? `${formatUsd(spent)} ${t.of} ${formatUsd(limit)}` : `${formatUsd(spent)} (${t.noLimit})`;
}

export function ProfilesView({ctx}: ViewProps) {
  const t = useMessages(profilesText);
  const d = useMessages(daemonText);
  const {client, status} = useDaemon();
  const up = status.daemon === 'up' && status.paired;
  const {data, reload} = useProfilesData(client, up);
  const [starting, setStarting] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [adding, setAdding] = useState(false);
  const [newName, setNewName] = useState('');

  if (!client) return <div className="orbit-side"><StateView variant="loading" title={t.view.loading} compact headingLevel={3} /></div>;

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

  if (!up) {
    return (
      <div className="orbit-side">
        <StateView compact headingLevel={3} title={t.view.daemonDown} action={
          <Button size="sm" variant="secondary" loading={starting || status.daemon === 'starting'} onClick={() => void startDaemon()}>{d.status.start}</Button>
        } />
        {problem && <Callout tone="danger" title={problem} />}
      </div>
    );
  }
  if (data.loading && !data.view) return <div className="orbit-side"><StateView variant="loading" title={t.view.loading} compact headingLevel={3} /></div>;
  if (!data.view) {
    return <div className="orbit-side"><StateView variant="error" title={t.view.loadFailed} description={data.error ? daemonErrorText(data.error, d.errors) : undefined} onRetry={() => void reload()} retryLabel={t.view.retry} compact headingLevel={3} /></div>;
  }

  return (
    <ProfileList
      ctx={ctx} view={data.view} usage={data.usage?.profiles ?? []} api={client.api} adding={adding} setAdding={setAdding} newName={newName} setNewName={setNewName}
    />
  );
}

export function ProfileList({ctx, view, usage, api, adding, setAdding, newName, setNewName}: {
  ctx: ViewProps['ctx']; view: ProfilesData; usage: ProfileUsage[]; api: DaemonApi; adding: boolean; setAdding(v: boolean): void; newName: string; setNewName(v: string): void;
}) {
  const t = useMessages(profilesText);
  const d = useMessages(daemonText);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const def = defaultName(view.file);
  const open = (name: string) => ctx.tabs.open({type: 'profiles', id: name, title: t.tab.title(name), params: {profile: name}});
  const names = view.file.profiles.map(p => p.name);
  const nameTaken = names.includes(newName.trim());

  const add = async () => {
    const name = newName.trim();
    if (!name || nameTaken) return;
    setBusy(true);
    setProblem(undefined);
    try {
      await api.saveProfiles({file: addProfile(view.file, name), expectedRevision: view.revision});
      notifyProfilesChanged();
      setAdding(false);
      setNewName('');
      open(name);
    } catch (error) {
      setProblem(daemonErrorText(error, d.errors));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="orbit-side">
      {view.keyStore === 'file' && <p className="orbit-meta">{t.view.fileStore}</p>}
      <ul className="orbit-prof-items" aria-label={t.view.listLabel}>
        {view.file.profiles.map(p => {
          const u = usage.find(x => x.profile === p.name);
          return (
            <li key={p.name}>
              <button type="button" className="orbit-prof-item" onClick={() => open(p.name)}>
                <span className="orbit-row">
                  <span className="orbit-prof-name">{p.name}</span>
                  {p.name === def && <Badge tone="accent">{t.view.default}</Badge>}
                  {u && (u.reached || u.apiReached) && <Badge tone="danger">{t.view.reached}</Badge>}
                </span>
                <span className="orbit-meta">{p.folders.length === 0 ? t.view.noFolders : p.folders.length === 1 ? p.folders[0] : t.view.folders(p.folders.length)}</span>
                {u && (
                  <span className="orbit-meta orbit-prof-usage">
                    {t.view.monthSpent}: {usageLine(u.spentUsd, u.limitUsd, t.view)}
                    {(u.apiLimitUsd > 0 || u.apiSpentUsd > 0) && <> · {t.view.apiSpent}: {usageLine(u.apiSpentUsd, u.apiLimitUsd, t.view)}</>}
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
      {adding ? (
        <form className="orbit-prof-add" onSubmit={event => { event.preventDefault(); void add(); }}>
          <Input label={t.view.addName} value={newName} autoFocus error={nameTaken ? t.issue.duplicate : undefined} onChange={event => setNewName(event.currentTarget.value)} />
          <div className="orbit-row">
            <Button type="submit" size="sm" loading={busy} disabled={newName.trim() === '' || nameTaken}>{busy ? t.view.adding : t.view.addConfirm}</Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => { setAdding(false); setNewName(''); }}>{t.view.cancel}</Button>
          </div>
        </form>
      ) : (
        <Button size="sm" variant="secondary" leadingIcon={<Plus />} onClick={() => setAdding(true)}>{t.view.add}</Button>
      )}
      {problem && <Callout tone="danger" title={problem} />}
    </div>
  );
}

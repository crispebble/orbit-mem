// 프로필 편집 탭(`profiles`, params {profile}): 한 프로필의 모든 설정, 키(P3), 이번 달 사용량(P5).
import {useEffect, useMemo, useState} from 'react';
import type {PluginContext, TabProps} from '@onidot-labs/orbit-studio-plugin';
import {Badge, Button, Callout, Input, Select, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import {Plus, X} from 'lucide-react';
import type {ProfileFileJson, ProfileJson, ProfilesView, ProfileUsage} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {useDaemon} from '../daemon/runtime.ts';
import {daemonErrorText} from '../chat/errors.ts';
import {useMessages} from '../i18n/index.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {profilesText} from '../i18n/messages/profiles.ts';
import {askConfirm} from './confirm.ts';
import {notifyProfilesChanged, useProfilesData} from './data.ts';
import {ChainEditor, FieldError, StringList} from './editor-parts.tsx';
import {
  RUN_RULES, RUN_SOURCES, ROLE_NAMES, chainOf, defaultName, fieldWithin, keysSet, neededProviders, parseFieldError, removeProfile, setCheap, setDefaultProfile, setRole,
  toSaveFile, validateProfile, classifySaveError, type Issue, type RunRule, type RunSource, type SaveProblem,
} from './edit-model.ts';
import {KeySection} from './key-section.tsx';
import {createKeyEntry} from './keys.ts';
import {NumberField} from './number-field.tsx';
import {UsageTable} from './usage-table.tsx';

const NONE = '__none__';

type Server = SaveProblem;

export function ProfilesTab({ctx, tab}: TabProps) {
  const t = useMessages(profilesText);
  const d = useMessages(daemonText);
  const {client, status} = useDaemon();
  const [name, setName] = useState(typeof tab.params.profile === 'string' ? tab.params.profile : tab.id);
  // 「다시 불러오기」를 누르면 편집기를 새로 세워 초안을 서버 값으로 돌린다
  const [epoch, setEpoch] = useState(0);
  const up = status.daemon === 'up';
  const {data, reload} = useProfilesData(client, up);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string>();

  if (!client) return <StateView variant="loading" title={t.tab.loading} headingLevel={3} />;
  if (!up) {
    return (
      <div className="orbit-pane">
        <StateView title={t.view.daemonDown} action={
          <Button variant="secondary" size="sm" loading={starting || status.daemon === 'starting'} onClick={async () => {
            setStarting(true);
            setStartError(undefined);
            try { await client.ensure(); } catch (error) { setStartError(daemonErrorText(error, d.errors)); } finally { setStarting(false); }
          }}>{d.status.start}</Button>} />
        {startError && <Callout tone="danger" title={startError} />}
      </div>
    );
  }
  if (data.loading && !data.view) return <StateView variant="loading" title={t.tab.loading} headingLevel={3} />;
  if (!data.view) return <StateView variant="error" title={t.view.loadFailed} description={data.error ? daemonErrorText(data.error, d.errors) : undefined} onRetry={() => void reload()} retryLabel={t.view.retry} headingLevel={3} />;
  const profile = data.view.file.profiles.find(p => p.name === name);
  if (!profile) return data.loading ? <StateView variant="loading" title={t.tab.loading} headingLevel={3} /> : <StateView title={t.tab.notFound(name)} headingLevel={3} />;
  return (
    <ProfileEditor
      key={epoch} ctx={ctx} tabId={tab.id} client={client} view={data.view} profile={profile}
      usage={data.usage?.profiles.find(u => u.profile === name)} onRenamed={setName}
      reload={async () => { await reload(); setEpoch(e => e + 1); }}
    />
  );
}

export function ProfileEditor({ctx, tabId, client, view, profile, usage, reload, onRenamed}: {
  ctx: PluginContext; tabId: string; client: DaemonClient; view: ProfilesView; profile: ProfileJson; usage: ProfileUsage | undefined; onRenamed(name: string): void; reload: () => Promise<void>;
}) {
  const t = useMessages(profilesText);
  const d = useMessages(daemonText);
  const [savedName, setSavedName] = useState(profile.name);
  const [draft, setDraft] = useState<ProfileJson>(profile);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [showIssues, setShowIssues] = useState(false);
  const [server, setServer] = useState<Server>();
  const [done, setDone] = useState(false);
  const entry = useMemo(() => createKeyEntry(client.api), [client]);
  const keys = view.keys.find(k => k.profile === savedName)?.keys;
  const have = useMemo(() => keysSet(keys), [keys]);
  const others = view.file.profiles.map(p => p.name).filter(n => n !== savedName);
  const issues = useMemo(() => validateProfile(draft, others), [draft, others.join('\n')]);
  const isDefault = defaultName(view.file) === savedName;

  useEffect(() => {
    ctx.tabs.setModified('profiles', tabId, dirty);
  }, [ctx, tabId, dirty]);

  const edit = (next: ProfileJson) => {
    setDraft(next);
    setDirty(true);
    setDone(false);
    setServer(undefined);
  };
  const text = (issue: Issue) => {
    const i = t.issue;
    switch (issue.code) {
      case 'name': return i.name;
      case 'duplicate': return i.duplicate;
      case 'absolute': return i.absolute;
      case 'notAllowed': return i.notAllowed(issue.ref ?? '');
      case 'positive': return i.positive;
      case 'empty': return i.empty;
    }
  };
  /** 칸(또는 그 아래 칸)의 오류 글: 저장을 눌러 본 뒤의 로컬 검증 + 데몬이 알려 준 칸 */
  const errorOf = (field: string): string | undefined => {
    const local = showIssues ? issues.find(x => x.field === field) : undefined;
    if (local) return text(local);
    return server?.kind === 'field' && server.field === field ? server.text : undefined;
  };
  /** 목록 칸(`base[i]`)별 오류 */
  const listErrors = (base: string, count: number): Record<number, string> => {
    const out: Record<number, string> = {};
    for (let i = 0; i < count; i++) {
      const e = errorOf(`${base}[${i}]`) ?? (count === 1 ? errorOf(base) : undefined);
      if (e) out[i] = e;
    }
    return out;
  };
  const serverNear = (...fields: string[]) => (server?.kind === 'field' && fields.some(f => fieldWithin(server.field, f)) && !errorOf(server.field) ? server.text : undefined);

  const save = async () => {
    setShowIssues(true);
    setServer(undefined);
    setDone(false);
    if (issues.length > 0) return;
    setBusy(true);
    try {
      const file = toSaveFile(view.file, savedName, draft);
      await client.api.saveProfiles({file, expectedRevision: view.revision});
      const newName = draft.name.trim();
      if (newName !== savedName) {
        ctx.tabs.setParams('profiles', tabId, {profile: newName});
        ctx.tabs.setTitle('profiles', tabId, t.tab.title(newName));
        setSavedName(newName);
        onRenamed(newName);
      }
      setDirty(false);
      setShowIssues(false);
      setDone(true);
      notifyProfilesChanged();
    } catch (error) {
      setServer(classifySaveError(error, daemonErrorText(error, d.errors)));
    } finally {
      setBusy(false);
    }
  };

  const reloadFromDaemon = () => reload();

  const mutateFile = async (next: ProfileFileJson, after?: () => void) => {
    setBusy(true);
    setServer(undefined);
    try {
      await client.api.saveProfiles({file: next, expectedRevision: view.revision});
      notifyProfilesChanged();
      after?.();
    } catch (error) {
      setServer(classifySaveError(error, daemonErrorText(error, d.errors)));
    } finally {
      setBusy(false);
    }
  };

  const roleNames = [...ROLE_NAMES.filter(r => r in draft.roles), ...Object.keys(draft.roles).filter(r => !(ROLE_NAMES as readonly string[]).includes(r))];
  const missingStandard = ROLE_NAMES.filter(r => !(r in draft.roles));
  const [newRole, setNewRole] = useState('');
  const rules = draft.runRules ?? {};

  return (
    <div className="orbit-pane orbit-prof-tab">
      <header className="orbit-prof-head">
        <div className="orbit-row">
          <h2 className="orbit-prof-title">{savedName}</h2>
          {isDefault && <Badge tone="accent">{t.tab.isDefault}</Badge>}
          {dirty && <Badge tone="warning">{t.tab.unsaved}</Badge>}
          <span className="orbit-grow" />
          {!isDefault && <Button size="sm" variant="secondary" disabled={busy || dirty} onClick={() => void mutateFile(setDefaultProfile(view.file, savedName))}>{t.tab.setDefault}</Button>}
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void reloadFromDaemon()} aria-label={t.tab.reloadLabel}>{t.tab.reload}</Button>
          <Button size="sm" variant="danger" disabled={busy || view.file.profiles.length <= 1} title={view.file.profiles.length <= 1 ? t.tab.deleteLast : undefined} onClick={async () => {
            if (await askConfirm(ctx, t.tab.deleteConfirm(savedName), t.tab.deleteAction, t.tab.cancel)) await mutateFile(removeProfile(view.file, savedName), () => ctx.tabs.close('profiles', tabId));
          }}>{t.tab.delete}</Button>
        </div>
        {view.keyStore === 'file' && <p className="orbit-meta">{t.view.fileStore}</p>}
        {server?.kind === 'changed' && (
          <Callout tone="warning" title={t.tab.reloadChanged}>
            <Button size="sm" variant="secondary" onClick={() => void reloadFromDaemon()}>{t.tab.reload}</Button>
          </Callout>
        )}
        {server?.kind === 'other' && <Callout tone="danger" title={server.text} />}
        {server?.kind === 'field' && !errorOf(server.field) && <Callout tone="danger" title={`${t.tab.serverField}: ${server.field}: ${server.text}`} />}
        {showIssues && issues.length > 0 && <Callout tone="danger" title={t.tab.invalidSummary} />}
        {done && <p className="orbit-meta" role="status">{t.tab.saved}</p>}
      </header>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-basics">
        <h3 id="orbit-prof-basics" className="orbit-prof-h">{t.section.basics}</h3>
        <InputLine label={t.section.name} value={draft.name} error={errorOf('name')} onChange={v => edit({...draft, name: v})} />
        <p className="orbit-meta">{t.section.foldersHint}</p>
        <StringList label={t.section.folders} values={draft.folders} onChange={v => edit({...draft, folders: v})} errors={listErrors('folders', draft.folders.length)} />
        <InputLine label={t.section.claudeConfigDir} value={draft.claudeConfigDir ?? ''} error={errorOf('claudeConfigDir')} onChange={v => edit({...draft, claudeConfigDir: v})} />
        <InputLine label={t.section.codexHome} value={draft.codexHome ?? ''} error={errorOf('codexHome')} onChange={v => edit({...draft, codexHome: v})} />
        <FieldError text={serverNear('folders', 'name')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-allowed">
        <h3 id="orbit-prof-allowed" className="orbit-prof-h">{t.section.allowedModels}</h3>
        <p className="orbit-meta">{t.section.allowedHint}</p>
        <StringList label={t.section.allowedModels} values={draft.allowedModels} onChange={v => edit({...draft, allowedModels: v})} placeholder="claude:*" />
        <FieldError text={serverNear('allowedModels')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-roles">
        <h3 id="orbit-prof-roles" className="orbit-prof-h">{t.section.roles}</h3>
        <p className="orbit-meta">{t.section.rolesHint}</p>
        {roleNames.map(role => {
          const chain = chainOf(draft.roles[role]);
          return (
            <div key={role} className="orbit-prof-role">
              <div className="orbit-row">
                <strong>{role}</strong>
                <span className="orbit-grow" />
                <Button type="button" size="sm" variant="ghost" leadingIcon={<X />} onClick={() => edit(setRole(draft, role, []))} aria-label={`${t.field.removeRole}: ${role}`}>{t.field.removeRole}</Button>
              </div>
              <ChainEditor label={t.field.roleChain(role)} chain={chain} haveKeys={have} onChange={v => edit(setRole(draft, role, v))} errors={listErrors(`roles.${role}`, chain.length)} />
              <FieldError text={serverNear(`roles.${role}`)} />
            </div>
          );
        })}
        <div className="orbit-row">
          {missingStandard.map(r => (
            <Button key={r} type="button" size="sm" variant="ghost" leadingIcon={<Plus />} onClick={() => edit({...draft, roles: {...draft.roles, [r]: draft.roles.default ?? ''}})}>{r}</Button>
          ))}
        </div>
        <form className="orbit-prof-line" onSubmit={event => {
          event.preventDefault();
          const r = newRole.trim();
          if (r && !(r in draft.roles)) edit({...draft, roles: {...draft.roles, [r]: ''}});
          setNewRole('');
        }}>
          <InputLine label={t.field.newRole} value={newRole} onChange={setNewRole} />
          <Button type="submit" size="sm" variant="secondary" disabled={newRole.trim() === ''}>{t.field.addRole}</Button>
        </form>
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-cheap">
        <h3 id="orbit-prof-cheap" className="orbit-prof-h">{t.section.cheap}</h3>
        <ChainEditor label={t.section.cheap} chain={chainOf(draft.cheap)} haveKeys={have} onChange={v => edit(setCheap(draft, v))} errors={listErrors('cheap', chainOf(draft.cheap).length)} />
        <FieldError text={serverNear('cheap')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-rules">
        <h3 id="orbit-prof-rules" className="orbit-prof-h">{t.section.runRules}</h3>
        <p className="orbit-meta">{t.section.runRulesHint}</p>
        <div className="orbit-prof-grid">
          {RUN_SOURCES.map((source: RunSource) => (
            <Select
              key={source}
              label={t.field.ruleFor(source)}
              showLabel
              value={rules[source] ?? NONE}
              onValueChange={v => {
                const next = {...rules};
                if (v === NONE) delete next[source];
                else next[source] = v as RunRule;
                edit({...draft, runRules: next});
              }}
              options={[{value: NONE, label: t.field.rule['']}, ...RUN_RULES.map(r => ({value: r, label: t.field.rule[r]}))]}
            />
          ))}
        </div>
        <FieldError text={serverNear('runRules')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-frules">
        <h3 id="orbit-prof-frules" className="orbit-prof-h">{t.section.folderRules}</h3>
        <p className="orbit-meta">{t.section.folderRulesHint}</p>
        {(draft.folderRules ?? []).map((rule, i) => {
          const set = (patch: Partial<typeof rule>) => edit({...draft, folderRules: (draft.folderRules ?? []).map((r, j) => (j === i ? {...r, ...patch} : r))});
          return (
            <div key={i} className="orbit-prof-role">
              <div className="orbit-row">
                <strong>{`${t.section.folderRules} ${i + 1}`}</strong>
                <span className="orbit-grow" />
                <Button type="button" size="sm" variant="ghost" leadingIcon={<X />} onClick={() => edit({...draft, folderRules: (draft.folderRules ?? []).filter((_, j) => j !== i)})} aria-label={`${t.field.removeRule} ${i + 1}`}>{t.field.removeRule}</Button>
              </div>
              <StringList label={`${t.field.ruleModels} ${i + 1}`} values={rule.models} onChange={v => set({models: v})} errors={errorOf(`folderRules[${i}].models`) ? {0: errorOf(`folderRules[${i}].models`)!} : undefined} />
              <StringList label={`${t.field.ruleAllow} ${i + 1}`} values={rule.allow ?? []} onChange={v => set({allow: v})} errors={listErrors(`folderRules[${i}].allow`, (rule.allow ?? []).length)} />
              <StringList label={`${t.field.ruleDeny} ${i + 1}`} values={rule.deny ?? []} onChange={v => set({deny: v})} errors={listErrors(`folderRules[${i}].deny`, (rule.deny ?? []).length)} />
              <FieldError text={serverNear(`folderRules[${i}]`)} />
            </div>
          );
        })}
        <div className="orbit-row">
          <Button type="button" size="sm" variant="secondary" leadingIcon={<Plus />} onClick={() => edit({...draft, folderRules: [...(draft.folderRules ?? []), {models: ['']}]})}>{t.field.addRule}</Button>
        </div>
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-limits">
        <h3 id="orbit-prof-limits" className="orbit-prof-h">{t.section.limits}</h3>
        <div className="orbit-prof-grid">
          <NumberField label={t.field.turns} value={draft.limits.turns} error={errorOf('limits.turns')} onChange={v => edit({...draft, limits: {...draft.limits, turns: v ?? Number.NaN}})} />
          <NumberField label={t.field.costUsd} value={draft.limits.costUsd} error={errorOf('limits.costUsd')} onChange={v => edit({...draft, limits: {...draft.limits, costUsd: v ?? Number.NaN}})} />
          <NumberField label={t.field.minutes} value={draft.limits.minutes} error={errorOf('limits.minutes')} onChange={v => edit({...draft, limits: {...draft.limits, minutes: v ?? Number.NaN}})} />
        </div>
        <h3 className="orbit-prof-h">{t.section.monthly}</h3>
        <div className="orbit-prof-grid">
          <NumberField optional label={t.field.monthlyLimitUsd} value={draft.monthlyLimitUsd} error={errorOf('monthlyLimitUsd')} onChange={v => edit({...draft, monthlyLimitUsd: v})} />
          <NumberField optional label={t.field.monthlyApiLimitUsd} value={draft.monthlyApiLimitUsd} error={errorOf('monthlyApiLimitUsd')} onChange={v => edit({...draft, monthlyApiLimitUsd: v})} />
        </div>
        <FieldError text={serverNear('limits', 'monthlyLimitUsd', 'monthlyApiLimitUsd')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-kw">
        <h3 id="orbit-prof-kw" className="orbit-prof-h">{t.section.keywords}</h3>
        <p className="orbit-meta">{t.section.keywordsHint}</p>
        <StringList label={t.field.ultrawork} values={draft.keywords.ultrawork} onChange={v => edit({...draft, keywords: {...draft.keywords, ultrawork: v}})} />
        <StringList label={t.field.plan} values={draft.keywords.plan} onChange={v => edit({...draft, keywords: {...draft.keywords, plan: v}})} />
        <FieldError text={serverNear('keywords')} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-keys">
        <h3 id="orbit-prof-keys" className="orbit-prof-h">{t.section.keys}</h3>
        <KeySection profile={savedName} providers={neededProviders(draft, keys)} keys={keys} entry={entry} confirm={(q, a, c) => askConfirm(ctx, q, a, c)} onChanged={notifyProfilesChanged} />
      </section>

      <section className="orbit-prof-sec" aria-labelledby="orbit-prof-usage">
        <h3 id="orbit-prof-usage" className="orbit-prof-h">{t.section.usage}</h3>
        <UsageTable usage={usage} />
      </section>

      <footer className="orbit-prof-foot">
        <Button variant="primary" loading={busy} disabled={!dirty} onClick={() => void save()} aria-label={t.tab.save}>{busy ? t.tab.saving : t.tab.save}</Button>
      </footer>
    </div>
  );
}

function InputLine({label, value, onChange, error}: {label: string; value: string; onChange(v: string): void; error?: string}) {
  return <Input label={label} value={value} error={error} onChange={event => onChange(event.currentTarget.value)} />;
}

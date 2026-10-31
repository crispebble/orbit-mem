// 프로필의 API 키(P3): 공급처마다 저장 여부, 「키 넣기/바꾸기」, 「지우기」. 키 값은 상태에 두지 않는다.
// 입력 칸은 제어되지 않는 password 칸이다: 값은 보내는 순간 칸에서 읽고 곧바로 비운다(다시 보이지 않고 「저장됨」만 남는다).
import {useRef, useState, useSyncExternalStore} from 'react';
import {Badge, Button, Input} from '@onidot-labs/orbit-studio-plugin/ui';
import type {ProfilesView} from '../daemon/api.ts';
import {daemonErrorText} from '../chat/errors.ts';
import {useMessages} from '../i18n/index.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {profilesText} from '../i18n/messages/profiles.ts';
import type {KeyEntry} from './keys.ts';

export function KeySection({profile, providers, keys, entry, confirm, onChanged}: {
  profile: string;
  providers: readonly string[];
  keys: ProfilesView['keys'][number]['keys'] | undefined;
  entry: KeyEntry;
  /** 「지우기」 확인 창(ctx.ui.ask) */
  confirm(text: string, action: string, cancel: string): Promise<boolean>;
  onChanged(): void;
}) {
  const t = useMessages(profilesText);
  const d = useMessages(daemonText);
  const state = useSyncExternalStore(entry.subscribe, entry.getState, entry.getState);
  const [open, setOpen] = useState<string>();
  const input = useRef<HTMLInputElement>(null);
  const isSet = (p: string) => keys?.find(k => k.provider === p)?.set ?? false;

  const submit = async (provider: string) => {
    const el = input.current;
    if (!el) return;
    const value = el.value;
    el.value = '';
    const revision = await entry.submit(profile, provider, value);
    if (revision !== undefined) {
      setOpen(undefined);
      onChanged();
    }
  };
  const remove = async (provider: string) => {
    if (!(await confirm(t.keys.removeConfirm(provider), t.keys.removeAction, t.keys.cancel))) return;
    if ((await entry.remove(profile, provider)) !== undefined) onChanged();
  };

  if (providers.length === 0) return <p className="orbit-meta">{t.keys.none}</p>;
  return (
    <ul className="orbit-prof-keys">
      {providers.map(provider => {
        const set = isSet(provider);
        const status = state.status[provider];
        const failed = state.error?.provider === provider ? state.error.error : undefined;
        return (
          <li key={provider} className="orbit-prof-key">
            <div className="orbit-row">
              <strong>{provider}</strong>
              <Badge tone={set ? 'success' : 'warning'}>{set ? t.keys.set : t.keys.missing}</Badge>
              <span className="orbit-grow" />
              {open !== provider && <Button size="sm" variant="secondary" onClick={() => setOpen(provider)}>{set ? t.keys.change : t.keys.put}</Button>}
              {set && <Button size="sm" variant="ghost" disabled={status === 'deleting'} onClick={() => void remove(provider)} aria-label={`${t.keys.remove}: ${provider}`}>{t.keys.remove}</Button>}
            </div>
            {open === provider && (
              <form className="orbit-prof-line" onSubmit={event => { event.preventDefault(); void submit(provider); }}>
                <Input ref={input} type="password" autoComplete="off" autoFocus spellCheck={false} label={t.keys.apiKeyFor(provider)} hideLabel placeholder={t.keys.apiKeyFor(provider)} aria-label={t.keys.apiKeyFor(provider)} />
                <Button type="submit" size="sm" loading={status === 'saving'}>{status === 'saving' ? t.keys.saving : t.keys.saveKey}</Button>
                <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(undefined)}>{t.keys.cancel}</Button>
              </form>
            )}
            {open === provider && <p className="orbit-meta">{t.keys.neverShown}</p>}
            {status === 'saved' && open !== provider && <p className="orbit-meta" role="status">{t.keys.saved}</p>}
            {failed !== undefined && <p className="orbit-alert" role="alert">{t.keys.failed} {daemonErrorText(failed, d.errors)}</p>}
          </li>
        );
      })}
    </ul>
  );
}

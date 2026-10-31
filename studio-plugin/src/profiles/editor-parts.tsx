// 프로필 편집기의 작은 부품: 글 목록, 모델 사슬(순서 바꾸기·더하기·빼기·「키 없음 — 건너뜀」), 오류 줄.
import {useState} from 'react';
import {ArrowDown, ArrowUp, Plus, X} from 'lucide-react';
import {Badge, Button, IconButton, Input} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {profilesText} from '../i18n/messages/profiles.ts';
import {addRef, lacksKey, moveRef, removeRef} from './edit-model.ts';

export function FieldError({text}: {text?: string}) {
  return text ? <p className="orbit-alert" role="alert">{text}</p> : null;
}

/** 글 한 줄씩 고치는 목록(폴더·허용 모델·키워드…). 빈 줄은 저장 때 걷어 낸다. */
export function StringList({label, values, onChange, placeholder, errors}: {label: string; values: readonly string[]; onChange(next: string[]): void; placeholder?: string; errors?: Record<number, string>}) {
  const t = useMessages(profilesText);
  return (
    <div className="orbit-prof-list" role="group" aria-label={label}>
      {values.map((v, i) => (
        <div key={i} className="orbit-prof-line">
          <Input label={`${label} ${i + 1}`} hideLabel placeholder={placeholder} value={v} error={errors?.[i]} onChange={event => onChange(values.map((x, j) => (j === i ? event.currentTarget.value : x)))} />
          <IconButton label={`${t.field.remove}: ${label} ${i + 1}`} size="sm" onClick={() => onChange(removeRef(values, i))}><X /></IconButton>
        </div>
      ))}
      <div className="orbit-row">
        <Button type="button" variant="ghost" size="sm" leadingIcon={<Plus />} onClick={() => onChange([...values, ''])} aria-label={`${t.field.add}: ${label}`}>{t.field.add}</Button>
      </div>
    </div>
  );
}

/** 모델 사슬. 위에서부터 시도한다. 필요한 키가 없는 모델 옆에 「키 없음 — 건너뜀」. */
export function ChainEditor({label, chain, onChange, haveKeys, errors}: {label: string; chain: readonly string[]; onChange(next: string[]): void; haveKeys: ReadonlySet<string>; errors?: Record<number, string>}) {
  const t = useMessages(profilesText);
  const [draft, setDraft] = useState('');
  const add = () => {
    const next = addRef(chain, draft);
    if (next !== chain) onChange(next);
    setDraft('');
  };
  return (
    <div className="orbit-prof-list" role="group" aria-label={label}>
      <ol className="orbit-prof-chain">
        {chain.map((ref, i) => (
          <li key={i} className="orbit-prof-line">
            <span className="orbit-meta orbit-prof-index">{i + 1}</span>
            <Input label={`${label} ${i + 1}`} hideLabel value={ref} error={errors?.[i]} onChange={event => onChange(chain.map((x, j) => (j === i ? event.currentTarget.value : x)))} />
            {lacksKey(ref, haveKeys) && <Badge tone="warning">{t.field.noKeySkipped}</Badge>}
            <IconButton label={`${t.field.moveUp}: ${label} ${i + 1}`} size="sm" disabled={i === 0} onClick={() => onChange(moveRef(chain, i, -1))}><ArrowUp /></IconButton>
            <IconButton label={`${t.field.moveDown}: ${label} ${i + 1}`} size="sm" disabled={i === chain.length - 1} onClick={() => onChange(moveRef(chain, i, 1))}><ArrowDown /></IconButton>
            <IconButton label={`${t.field.remove}: ${label} ${i + 1}`} size="sm" onClick={() => onChange(removeRef(chain, i))}><X /></IconButton>
          </li>
        ))}
      </ol>
      <div className="orbit-prof-line">
        <Input label={`${t.field.modelRef} — ${label}`} hideLabel placeholder={t.field.modelRef} value={draft} onChange={event => setDraft(event.currentTarget.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); add(); } }} />
        <Button type="button" variant="ghost" size="sm" leadingIcon={<Plus />} disabled={draft.trim() === ''} onClick={add} aria-label={`${t.field.add}: ${label}`}>{t.field.add}</Button>
      </div>
    </div>
  );
}

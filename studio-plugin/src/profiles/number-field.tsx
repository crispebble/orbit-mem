// 숫자 칸: 쓰는 도중의 글("1.")을 그대로 두고, 읽을 수 있으면 숫자를 위로 올린다. 비우면 optional은 undefined, 아니면 NaN(검증이 잡는다).
import {useState} from 'react';
import {Input} from '@onidot-labs/orbit-studio-plugin/ui';

export function NumberField({label, value, onChange, error, optional}: {label: string; value: number | undefined; onChange(next: number | undefined): void; error?: string; optional?: boolean}) {
  const [text, setText] = useState(value === undefined || Number.isNaN(value) ? '' : String(value));
  return (
    <Input
      label={label}
      inputMode="decimal"
      value={text}
      error={error}
      onChange={event => {
        const v = event.currentTarget.value;
        setText(v);
        onChange(v.trim() === '' ? (optional ? undefined : Number.NaN) : Number(v));
      }}
    />
  );
}

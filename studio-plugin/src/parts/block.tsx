// 기억 목록·상세·폼·제안·회고가 함께 쓰는 작은 부품.
import {useId, type ReactNode} from 'react';
import {memoryKindLabels, memoryStateLabels, type MemoryKind, type MemoryState} from './vocabulary.ts';

export function KindTile({kind}: {kind: MemoryKind}) {
  return <span className="orbit-mem-kind" data-kind={kind} aria-hidden="true">{memoryKindLabels[kind][0]}</span>;
}

export function KindPill({kind}: {kind: MemoryKind}) {
  return <span className="orbit-mem-pill" data-kind={kind}>{memoryKindLabels[kind]}</span>;
}

export function StateMark({state}: {state: MemoryState}) {
  return <span className="orbit-mem-state" data-state={state}><i aria-hidden="true" />{memoryStateLabels[state]}</span>;
}

export function Block({icon, title, count, actions, children}: {icon: ReactNode; title: string; count?: ReactNode; actions?: ReactNode; children: ReactNode}) {
  const id = useId();
  return (
    <section className="orbit-block" aria-labelledby={id}>
      <header className="orbit-block-head">
        {icon}
        <h3 id={id}>{title}</h3>
        {count !== undefined && <span className="orbit-meta">{count}</span>}
        <span className="orbit-block-sp" />
        {actions}
      </header>
      {children}
    </section>
  );
}

/** 접두어가 보이는 작은 선택 상자("범위 전체"). 접근 가능한 이름은 접두어 글이다. */
export function Select({label, value, onChange, options, disabled}: {label: string; value: string; onChange: (value: string) => void; options: readonly {value: string; label: string}[]; disabled?: boolean}) {
  return (
    <span className="orbit-mem-select">
      <span aria-hidden="true">{label}</span>
      <select aria-label={label} value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>
        {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </span>
  );
}

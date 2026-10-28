// 회고 하루(또는 한 주): 한 일·막힌 것·이어 갈 것, 짚을 점, 나온 기억·제안.
// 지금 플러그인 화면은 쓰지 않는다. Forma 0.9.x에서 함께 옮겨 왔다.
import {Brain, Clock, Cpu, FileSearch, Flag, MessagesSquare, OctagonAlert, Repeat, Sparkles} from 'lucide-react';
import {ActionFeedback, Frame, useAction} from './shared.tsx';
import {Block, KindTile} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import type {MemoryKind} from './vocabulary.ts';

export interface RetroFlag {
  id: string;
  text: string;
  evidence?: string;
  type?: 'repeat' | 'direction';
}

export interface RetroOutput {
  id: string;
  type: 'memory' | 'proposal';
  kind?: MemoryKind;
  title: string;
  meta?: string;
}

export interface RetroMaterial {
  label: string;
  count: number;
}

export interface RetroDayData {
  id: string;
  kind?: 'daily' | 'weekly';
  date: string;
  title: string;
  ranAt?: string;
  duration?: string;
  runner?: string;
  material?: readonly RetroMaterial[];
  hostMemoryChanges?: number;
  done: readonly string[];
  blocked: readonly string[];
  next: readonly string[];
  flags?: readonly RetroFlag[];
  outputs?: readonly RetroOutput[];
}

export interface RetroDayProps extends OrbitProps {
  day: RetroDayData;
  onConfirm?: (id: string) => void | Promise<void>;
  onOpen?: (id: string) => void | Promise<void>;
  onOpenMemories?: () => void | Promise<void>;
}

function RetroColumn({title, items}: {title: string; items: readonly string[]}) {
  return (
    <div className="orbit-retro-col">
      <h4>{title}</h4>
      {items.length ? <ul>{items.map(item => <li key={item}>{item}</li>)}</ul> : <p className="orbit-meta">없음</p>}
    </div>
  );
}

export function RetroDay({day, onConfirm, onOpen, onOpenMemories, className = '', surface = 'plain'}: RetroDayProps) {
  const action = useAction();
  const outputs = day.outputs ?? [];
  const memories = outputs.filter(item => item.type === 'memory').length;
  const proposals = outputs.length - memories;
  return (
    <Frame label={`회고 ${day.date}`} surface={surface} className={`orbit-retro ${className}`}>
      <div className="orbit-prop-src">
        <span className="orbit-badge">{day.kind === 'weekly' ? '주간 회고' : '하루 회고'}</span>
        <span>{day.date}</span>
      </div>
      <h2 className="orbit-title">{day.title}</h2>
      <div className="orbit-retro-meta">
        {(day.ranAt || day.duration) && <span><Clock size={12} aria-hidden="true" />{[day.ranAt, day.duration].filter(Boolean).join(' · ')}</span>}
        {day.runner && <span><Cpu size={12} aria-hidden="true" />{day.runner}</span>}
        {day.material?.length ? <span><MessagesSquare size={12} aria-hidden="true" />재료: {day.material.map(item => `${item.label} ${item.count}`).join(' · ')}</span> : null}
        {day.hostMemoryChanges !== undefined && <span><FileSearch size={12} aria-hidden="true" />호스트 기억 변경 {day.hostMemoryChanges}</span>}
      </div>
      <div className="orbit-retro-grid">
        <RetroColumn title="한 일" items={day.done} />
        <RetroColumn title="막힌 것" items={day.blocked} />
        <RetroColumn title="이어 갈 것" items={day.next} />
      </div>
      {day.flags?.length ? (
        <Block icon={<Flag size={14} aria-hidden="true" />} title="짚을 점" count={day.flags.length}>
          <ul className="orbit-list">
            {day.flags.map(flag => (
              <li className="orbit-flag" key={flag.id}>
                <span className="orbit-flag-ic">{flag.type === 'direction' ? <OctagonAlert size={13} aria-hidden="true" /> : <Repeat size={13} aria-hidden="true" />}</span>
                <div className="orbit-copy">{flag.text}{flag.evidence && <div className="orbit-meta">{flag.evidence}</div>}</div>
              </li>
            ))}
          </ul>
        </Block>
      ) : null}
      {outputs.length > 0 && (
        <Block
          icon={<Brain size={14} aria-hidden="true" />}
          title="나온 것"
          count={`후보 기억 ${memories} · 제안 ${proposals}`}
          actions={onOpenMemories && <button type="button" className="orbit-button" data-variant="ghost" onClick={() => void action.run(onOpenMemories)}>기억에서 보기</button>}
        >
          <ul className="orbit-list">
            {outputs.map(item => (
              <li className="orbit-out-row" key={item.id}>
                {item.type === 'memory' ? <KindTile kind={item.kind ?? 'lesson'} /> : <span className="orbit-prop-tile"><Sparkles size={12} aria-hidden="true" /></span>}
                <div className="orbit-copy">{item.title}{item.meta && <div className="orbit-meta">{item.meta}</div>}</div>
                {item.type === 'memory' && onConfirm && (
                  <button type="button" className="orbit-button" data-variant="ghost" aria-label={`${item.title} 확정`} disabled={action.pending} onClick={() => void action.run(() => onConfirm(item.id))}>확정</button>
                )}
                {item.type === 'proposal' && onOpen && (
                  <button type="button" className="orbit-button" aria-label={`${item.title} 열기`} disabled={action.pending} onClick={() => void action.run(() => onOpen(item.id))}>열기</button>
                )}
              </li>
            ))}
          </ul>
        </Block>
      )}
      <ActionFeedback action={action} />
    </Frame>
  );
}

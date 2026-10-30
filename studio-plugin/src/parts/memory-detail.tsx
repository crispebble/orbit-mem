// 기억 상세: 머리줄, 본문, 태그, 출처, 버전 이력, 대체 관계, 관련 기억(명시 연결).
import type {ReactNode} from 'react';
import {Archive, Brain, FileText, History, Link, Link2, Merge, MessageSquareQuote, Pencil, Quote, Sparkles, Terminal, TriangleAlert} from 'lucide-react';
import {AgentTimeline} from '@onidot-labs/studio-plugin/ai';
import {useMessages} from '../i18n/index.ts';
import {memoryText} from '../i18n/messages/memory.ts';
import {ActionFeedback, Frame, useAction} from './shared.tsx';
import {Block, KindPill, KindTile, StateMark} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import {kindOf, scopeOf, stateOf, type MemoryKind, type MemorySource, type MemorySourceKind, type OrbitMemory} from './vocabulary.ts';

const sourceIcon: Record<MemorySourceKind, typeof FileText> = {
  quote: MessageSquareQuote,
  file: FileText,
  command: Terminal,
  session: Sparkles,
  host_memory: Brain,
  legacy: Archive,
};

function SourceRow({source}: {source: MemorySource}) {
  const t = useMessages(memoryText);
  const Icon = sourceIcon[source.kind];
  return (
    <li className="orbit-src-row">
      <span className="orbit-src-ic" data-kind={source.kind}><Icon size={12} aria-hidden="true" /></span>
      <div className="orbit-copy">
        <span className="orbit-meta">{source.label || t.sources[source.kind]}{source.at && ` · ${source.at}`}</span>
        {source.text && <q>{source.text}</q>}
        {source.path && <div className="orbit-src-path">{source.path}</div>}
        {source.changed && (
          <div className="orbit-src-warn"><TriangleAlert size={12} aria-hidden="true" />{t.detail.sourceChanged}</div>
        )}
      </div>
    </li>
  );
}

/** 관련 기억 한 줄: 명시 연결 하나와 상대 기억 */
export interface MemoryLinkView {
  id: string;
  memoryId: string;
  /** 상대 기억 제목. 못 읽었으면 없다(id를 보인다) */
  title?: string;
  kind?: MemoryKind;
  note?: string;
  origin?: string;
}

export interface MemoryDetailProps extends OrbitProps {
  memory: OrbitMemory;
  related?: readonly OrbitMemory[];
  /** 함께 읽을 관련 기억(memory_get의 links) */
  links?: readonly MemoryLinkView[];
  onOpenMemory?: (id: string) => void;
  usedIn?: ReactNode;
  onEdit?: (id: string) => void | Promise<void>;
  onCopyId?: (id: string) => void | Promise<void>;
}

export function MemoryDetail({memory, related = [], links = [], onOpenMemory, usedIn, onEdit, onCopyId, className = '', surface = 'plain'}: MemoryDetailProps) {
  const t = useMessages(memoryText);
  const action = useAction();
  const kind = kindOf(memory);
  const find = (id: string) => related.find(item => item.id === id);
  return (
    <Frame label={t.detail.frame(memory.title)} surface={surface} className={`orbit-mem-detail ${className}`}>
      <div className="orbit-mem-head">
        <KindPill kind={kind} />
        <StateMark state={stateOf(memory)} />
        <span aria-hidden="true">·</span>
        <span>{scopeOf(memory)}</span>
        <span aria-hidden="true">·</span>
        <span className="orbit-mono">{memory.id}</span>
        <span className="orbit-block-sp" />
        {onCopyId && (
          <button type="button" className="orbit-button" data-variant="ghost" disabled={action.pending} onClick={() => void action.run(() => onCopyId(memory.id))}>
            <Link size={13} aria-hidden="true" />{t.detail.copyId}
          </button>
        )}
        {onEdit && (
          <button type="button" className="orbit-button" disabled={action.pending} onClick={() => void action.run(() => onEdit(memory.id))}>
            <Pencil size={13} aria-hidden="true" />{t.detail.edit}
          </button>
        )}
      </div>
      <h2 className="orbit-title">{memory.title}</h2>
      <p className="orbit-mem-body">{memory.content}</p>
      {(memory.tags?.length || usedIn || memory.lastUsed) && (
        <dl className="orbit-mem-props">
          {memory.tags?.length ? <><dt>{t.detail.tags}</dt><dd>{memory.tags.map(tag => <span className="orbit-tag" key={tag}>{tag}</span>)}</dd></> : null}
          {usedIn ? <><dt>{t.detail.usedIn}</dt><dd>{usedIn}</dd></> : null}
          {memory.lastUsed ? <><dt>{t.detail.lastUsed}</dt><dd>{memory.lastUsed}</dd></> : null}
        </dl>
      )}
      {memory.sources?.length ? (
        <Block icon={<Quote size={14} aria-hidden="true" />} title={t.detail.sources} count={t.count(memory.sources.length)}>
          <ul className="orbit-list">
            {memory.sources.map((source, index) => <SourceRow key={`${source.kind}-${index}`} source={source} />)}
          </ul>
        </Block>
      ) : null}
      {memory.history?.length ? (
        <Block icon={<History size={14} aria-hidden="true" />} title={t.detail.history} count={memory.history.length}>
          <AgentTimeline variant="history" title="" history={memory.history} surface="plain" />
        </Block>
      ) : null}
      {memory.supersedes?.length || memory.supersededBy ? (
        <Block icon={<Merge size={14} aria-hidden="true" />} title={t.detail.relations}>
          <ul className="orbit-list">
            {memory.supersedes?.map(id => {
              const old = find(id);
              return (
                <li className="orbit-out-row" key={`old-${id}`}>
                  {old ? <KindTile kind={kindOf(old)} /> : <KindTile kind="fact" />}
                  <div className="orbit-copy">
                    <div className="orbit-struck">{old?.title ?? id}</div>
                    <div className="orbit-meta">{t.detail.replaces}</div>
                  </div>
                </li>
              );
            })}
            {memory.supersededBy && (() => {
              const next = find(memory.supersededBy);
              return (
                <li className="orbit-out-row">
                  {next ? <KindTile kind={kindOf(next)} /> : <KindTile kind="fact" />}
                  <div className="orbit-copy">
                    <div>{next?.title ?? memory.supersededBy}</div>
                    <div className="orbit-meta">{t.detail.replacedBy}</div>
                  </div>
                </li>
              );
            })()}
          </ul>
        </Block>
      ) : null}
      {links.length ? (
        <Block icon={<Link2 size={14} aria-hidden="true" />} title={t.detail.related} count={links.length}>
          <ul className="orbit-list">
            {links.map(link => (
              <li className="orbit-out-row" key={link.id}>
                <KindTile kind={link.kind ?? 'fact'} />
                <div className="orbit-copy">
                  {onOpenMemory
                    ? <button type="button" className="orbit-link-button" onClick={() => onOpenMemory(link.memoryId)}>{link.title ?? link.memoryId}</button>
                    : <div>{link.title ?? link.memoryId}</div>}
                  {(link.note || link.origin) && <div className="orbit-meta">{[link.note, link.origin ? (t.linkOrigins as Record<string, string>)[link.origin] ?? link.origin : undefined].filter(Boolean).join(' · ')}</div>}
                </div>
              </li>
            ))}
          </ul>
        </Block>
      ) : null}
      <ActionFeedback action={action} />
    </Frame>
  );
}

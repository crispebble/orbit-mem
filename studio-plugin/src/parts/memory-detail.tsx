// 기억 상세: 머리줄, 본문, 태그, 출처, 버전 이력, 대체 관계.
import type {ReactNode} from 'react';
import {Archive, Brain, FileText, History, Link, Merge, MessageSquareQuote, Pencil, Quote, Sparkles, Terminal, TriangleAlert} from 'lucide-react';
import {AgentTimeline} from '@onidot-labs/studio-plugin/ai';
import {ActionFeedback, Frame, useAction} from './shared.tsx';
import {Block, KindPill, KindTile, StateMark} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import {kindOf, memorySourceLabels, scopeOf, stateOf, type MemorySource, type MemorySourceKind, type OrbitMemory} from './vocabulary.ts';

const sourceIcon: Record<MemorySourceKind, typeof FileText> = {
  quote: MessageSquareQuote,
  file: FileText,
  command: Terminal,
  session: Sparkles,
  host_memory: Brain,
  legacy: Archive,
};

function SourceRow({source}: {source: MemorySource}) {
  const Icon = sourceIcon[source.kind];
  return (
    <li className="orbit-src-row">
      <span className="orbit-src-ic" data-kind={source.kind}><Icon size={12} aria-hidden="true" /></span>
      <div className="orbit-copy">
        <span className="orbit-meta">{source.label || memorySourceLabels[source.kind]}{source.at && ` · ${source.at}`}</span>
        {source.text && <q>{source.text}</q>}
        {source.path && <div className="orbit-src-path">{source.path}</div>}
        {source.changed && (
          <div className="orbit-src-warn"><TriangleAlert size={12} aria-hidden="true" />출처 파일이 이 기억 뒤에 바뀌었습니다 · 다음 회고에서 확인</div>
        )}
      </div>
    </li>
  );
}

export interface MemoryDetailProps extends OrbitProps {
  memory: OrbitMemory;
  related?: readonly OrbitMemory[];
  usedIn?: ReactNode;
  onEdit?: (id: string) => void | Promise<void>;
  onCopyId?: (id: string) => void | Promise<void>;
}

export function MemoryDetail({memory, related = [], usedIn, onEdit, onCopyId, className = '', surface = 'plain'}: MemoryDetailProps) {
  const action = useAction();
  const kind = kindOf(memory);
  const find = (id: string) => related.find(item => item.id === id);
  return (
    <Frame label={`기억 ${memory.title}`} surface={surface} className={`orbit-mem-detail ${className}`}>
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
            <Link size={13} aria-hidden="true" />id 복사
          </button>
        )}
        {onEdit && (
          <button type="button" className="orbit-button" disabled={action.pending} onClick={() => void action.run(() => onEdit(memory.id))}>
            <Pencil size={13} aria-hidden="true" />고치기
          </button>
        )}
      </div>
      <h2 className="orbit-title">{memory.title}</h2>
      <p className="orbit-mem-body">{memory.content}</p>
      {(memory.tags?.length || usedIn || memory.lastUsed) && (
        <dl className="orbit-mem-props">
          {memory.tags?.length ? <><dt>태그</dt><dd>{memory.tags.map(tag => <span className="orbit-tag" key={tag}>{tag}</span>)}</dd></> : null}
          {usedIn ? <><dt>쓰이는 곳</dt><dd>{usedIn}</dd></> : null}
          {memory.lastUsed ? <><dt>마지막 사용</dt><dd>{memory.lastUsed}</dd></> : null}
        </dl>
      )}
      {memory.sources?.length ? (
        <Block icon={<Quote size={14} aria-hidden="true" />} title="출처" count={`${memory.sources.length}건`}>
          <ul className="orbit-list">
            {memory.sources.map((source, index) => <SourceRow key={`${source.kind}-${index}`} source={source} />)}
          </ul>
        </Block>
      ) : null}
      {memory.history?.length ? (
        <Block icon={<History size={14} aria-hidden="true" />} title="버전 이력" count={memory.history.length}>
          <AgentTimeline variant="history" title="" history={memory.history} surface="plain" />
        </Block>
      ) : null}
      {memory.supersedes?.length || memory.supersededBy ? (
        <Block icon={<Merge size={14} aria-hidden="true" />} title="관계">
          <ul className="orbit-list">
            {memory.supersedes?.map(id => {
              const old = find(id);
              return (
                <li className="orbit-out-row" key={`old-${id}`}>
                  {old ? <KindTile kind={kindOf(old)} /> : <KindTile kind="fact" />}
                  <div className="orbit-copy">
                    <div className="orbit-struck">{old?.title ?? id}</div>
                    <div className="orbit-meta">이 기억이 대체함 · 낡음</div>
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
                    <div className="orbit-meta">이 기억을 대체한 기억</div>
                  </div>
                </li>
              );
            })()}
          </ul>
        </Block>
      ) : null}
      <ActionFeedback action={action} />
    </Frame>
  );
}

// 제안 상세: 근거, 바뀔 내용(diff 또는 본문), 고쳐서 승인, 거절 사유. 단축키 A(승인)·⌘↵·Esc.
import {useEffect, useId, useRef, useState} from 'react';
import {FileDiff, FileText, Quote, Sparkles, Terminal} from 'lucide-react';
import {AvailabilityNotice, Frame, ActionFeedback, useAction, type Availability} from './shared.tsx';
import {Block} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import {changedLines} from './proposal-text.ts';
import {proposalApproveHints, proposalKindLabels, type ProposalKind} from './vocabulary.ts';

export interface ProposalEvidence {
  id?: string;
  label: string;
  text: string;
}

export interface ProposalDiffLine {
  no?: number;
  kind: 'add' | 'del' | 'ctx';
  text: string;
}

export interface ProposalDiff {
  path: string;
  lines: readonly ProposalDiffLine[];
  /** 제안된 글. 고치기는 이 글을 고친다. */
  text: string;
}

export interface ProposalOption {
  value: string;
  label: string;
}

export interface Proposal {
  id: string;
  kind: ProposalKind;
  title: string;
  explanation?: string;
  source?: string;
  evidence?: readonly ProposalEvidence[];
  diff?: ProposalDiff;
  target?: string;
  scope?: string;
  /** diff가 없는 제안(확정·낡음·재분류 등)의 내용. 미리보기에 보이고, 고쳐서 승인에서 이 글을 고친다. diff도 body도 없으면 0.8.0처럼 고쳐서 승인이 없다. */
  body?: string;
}

export interface ProposalApproval {
  /** 승인할 내용: diff가 있으면 diff.text, 없으면 body(고쳤으면 고친 글) */
  text: string;
  /** 제목이나 내용 중 하나라도 고쳤는지 */
  edited: boolean;
  /** 승인할 제목(고쳤으면 고친 제목) */
  title: string;
  titleEdited: boolean;
  textEdited: boolean;
  target?: string;
  scope?: string;
}

export interface ProposalDenial {
  /** 거절 사유. 비웠거나 사유를 받지 않으면 없다. */
  note?: string;
}

export interface ProposalDetailProps extends OrbitProps {
  /** true면 처음부터 고치는 중(고치기 모드)으로 연다 */
  defaultEditing?: boolean;
  proposal: Proposal;
  targets?: readonly ProposalOption[];
  scopes?: readonly ProposalOption[];
  onApprove?: (id: string, change: ProposalApproval) => void | Promise<void>;
  /** 거절. 사유를 받으면 두 번째 인자 note로 넘긴다. */
  onDeny?: (id: string, denial: ProposalDenial) => void | Promise<void>;
  /** 거절 사유 입력: none(기본, 바로 거절)·optional(사유 칸을 열고 비워도 거절)·required(사유가 있어야 거절) */
  denyReason?: 'none' | 'optional' | 'required';
  availability?: Availability;
  error?: string;
}

const isEditable = (target: EventTarget | null) =>
  target instanceof HTMLElement && (['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName) || target.isContentEditable);

type ProposalMode = 'view' | 'edit' | 'deny';

export function ProposalDetail({
  proposal,
  targets = [],
  scopes = [],
  defaultEditing = false,
  onApprove,
  onDeny,
  denyReason = 'none',
  availability = 'ready',
  error,
  className = '',
  surface = 'plain',
}: ProposalDetailProps) {
  const ids = useId();
  const action = useAction();
  const diff = proposal.diff;
  const original = diff ? diff.text : proposal.body ?? '';
  const [mode, setMode] = useState<ProposalMode>(defaultEditing ? 'edit' : 'view');
  const [text, setText] = useState(original);
  const [title, setTitle] = useState(proposal.title);
  const [note, setNote] = useState('');
  const [target, setTarget] = useState(proposal.target ?? targets[0]?.value);
  const [scope, setScope] = useState(proposal.scope ?? scopes[0]?.value);
  const editing = mode === 'edit';
  const denying = mode === 'deny';
  // 다른 제안(또는 새 제안 글)이 올 때만 되돌린다. targets/scopes는 보통 인라인 배열이라 다시 걸리면 안 된다.
  const firstTarget = targets[0]?.value;
  const firstScope = scopes[0]?.value;
  useEffect(() => {
    setMode(defaultEditing ? 'edit' : 'view');
    setText(original);
    setTitle(proposal.title);
    setNote('');
    setTarget(proposal.target ?? firstTarget);
    setScope(proposal.scope ?? firstScope);
  }, [proposal.id, original, proposal.title, proposal.target, proposal.scope, firstTarget, firstScope, defaultEditing]);
  const area = useRef<HTMLTextAreaElement>(null);
  const noteArea = useRef<HTMLTextAreaElement>(null);
  const ready = availability === 'ready';

  function approve() {
    if (!onApprove || !ready || action.pending || denying) return;
    const finalTitle = editing ? title.trim() || proposal.title : proposal.title;
    const finalText = editing ? text : original;
    const titleEdited = finalTitle !== proposal.title;
    const textEdited = finalText !== original;
    const change: ProposalApproval = {text: finalText, title: finalTitle, titleEdited, textEdited, edited: titleEdited || textEdited, target, scope};
    void action.run(() => onApprove(proposal.id, change));
  }

  const noteText = note.trim();
  const canDeny = !!onDeny && ready && !action.pending && (denyReason !== 'required' || !!noteText);

  function deny() {
    if (!onDeny || !ready || action.pending) return;
    if (denyReason !== 'none' && !denying) {
      setMode('deny');
      requestAnimationFrame(() => noteArea.current?.focus());
      return;
    }
    if (!canDeny) return;
    void action.run(() => onDeny(proposal.id, denyReason !== 'none' && noteText ? {note: noteText} : {}));
  }

  function startEdit() {
    setMode('edit');
    requestAnimationFrame(() => area.current?.focus());
  }

  function cancel() {
    setMode('view');
    setText(original);
    setTitle(proposal.title);
    setNote('');
  }

  const latest = useRef({approve, mode});
  latest.current = {approve, mode};
  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key.toLowerCase() !== 'a' || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.repeat || event.isComposing || isEditable(event.target) || latest.current.mode !== 'view') return;
      event.preventDefault();
      latest.current.approve();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // 최신 approve·mode는 ref로 읽으므로 한 번만 등록해도 낡지 않는다.
  }, []);

  const message = editing
    ? `고친 내용으로 승인합니다 · 원래 제안과의 차이 ${changedLines(original, text)}줄${title.trim() && title.trim() !== proposal.title ? ' · 제목 고침' : ''}`
    : denying
      ? denyReason === 'required' ? '거절 사유를 적어야 거절합니다.' : '거절 사유는 비워도 됩니다.'
      : proposalApproveHints[proposal.kind] ?? '승인하면 결정을 기록합니다.';

  const picks = (targets.length > 0 || scopes.length > 0) && (
    <div className="orbit-scope-row">
      {targets.length > 0 && (
        <>
          <label htmlFor={`${ids}-target`}>반영 대상</label>
          <span className="orbit-pick">
            <select id={`${ids}-target`} className="orbit-input orbit-select" value={target} onChange={event => setTarget(event.target.value)}>
              {targets.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </span>
        </>
      )}
      {scopes.length > 0 && (
        <>
          <label htmlFor={`${ids}-scope`}>범위</label>
          <span className="orbit-pick">
            <select id={`${ids}-scope`} className="orbit-input orbit-select" value={scope} onChange={event => setScope(event.target.value)}>
              {scopes.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </span>
        </>
      )}
    </div>
  );

  return (
    <Frame label="제안" surface={surface} className={`orbit-prop ${className}`}>
      <div
        onKeyDown={event => {
          if (mode === 'view') return;
          if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
            event.preventDefault();
            if (editing) approve();
            else deny();
          } else if (event.key === 'Escape') {
            event.preventDefault();
            cancel();
          }
        }}
      >
        <AvailabilityNotice availability={availability} error={error} />
        <div className="orbit-prop-src">
          <span className="orbit-prop-source"><Sparkles size={11} aria-hidden="true" />{proposalKindLabels[proposal.kind] ?? proposal.kind}</span>
          {proposal.source && <span>{proposal.source}</span>}
          <span className="orbit-block-sp" />
          {editing && <span className="orbit-prop-editing">고치는 중</span>}
        </div>
        {editing
          ? <input className="orbit-input orbit-prop-title-input" aria-label="제목 고치기" value={title} onChange={event => setTitle(event.target.value)} disabled={action.pending} />
          : <h2 className="orbit-title">{proposal.title}</h2>}
        {proposal.explanation && <p className="orbit-prop-sub">{proposal.explanation}</p>}
        {proposal.evidence?.length ? (
          <Block icon={<Quote size={14} aria-hidden="true" />} title="근거" count={`${proposal.evidence.length}건`}>
            <ul className="orbit-list">
              {proposal.evidence.map((item, index) => (
                <li className="orbit-evd" key={item.id ?? index}>
                  <span className="orbit-evd-who"><Terminal size={13} aria-hidden="true" /></span>
                  <div className="orbit-copy"><span className="orbit-meta">{item.label}</span><q>{item.text}</q></div>
                </li>
              ))}
            </ul>
          </Block>
        ) : null}
        {diff ? (
          <Block
            icon={<FileDiff size={14} aria-hidden="true" />}
            title="바뀔 내용"
            count={<span className="orbit-prop-path">{diff.path}</span>}
            actions={(
              <div className="orbit-seg" role="group" aria-label="보기 방식">
                <button type="button" aria-pressed={!editing} onClick={cancel}>미리보기</button>
                <button type="button" aria-pressed={editing} onClick={startEdit}>고치기</button>
              </div>
            )}
          >
            <div className="orbit-diff">
              {diff.lines.map((line, index) => (
                <div className="orbit-diff-line" data-kind={line.kind} key={index}>
                  <span className="orbit-diff-g">{line.no}</span>
                  <span className="orbit-diff-m">{line.kind === 'add' ? '+' : line.kind === 'del' ? '−' : ''}</span>
                  <span className="orbit-diff-t">{line.text}</span>
                </div>
              ))}
            </div>
            {editing && <textarea ref={area} className="orbit-edittext" aria-label="바뀔 내용 고치기" value={text} rows={Math.max(1, text.split('\n').length)} onChange={event => setText(event.target.value)} disabled={action.pending} />}
            {picks}
          </Block>
        ) : proposal.body !== undefined ? (
          <Block icon={<FileText size={14} aria-hidden="true" />} title="내용">
            {editing
              ? <textarea ref={area} className="orbit-edittext" aria-label="내용 고치기" value={text} rows={Math.max(2, text.split('\n').length)} onChange={event => setText(event.target.value)} disabled={action.pending} />
              : <p className="orbit-mem-body">{proposal.body}</p>}
            {picks}
          </Block>
        ) : null}
        {denying && (
          <div className="orbit-fld orbit-prop-deny">
            <label htmlFor={`${ids}-deny`}>거절 사유{denyReason === 'optional' && <span className="orbit-meta"> · 선택</span>}</label>
            <textarea ref={noteArea} id={`${ids}-deny`} className="orbit-input" rows={2} value={note} onChange={event => setNote(event.target.value)} disabled={action.pending} />
          </div>
        )}
        <ActionFeedback action={action} />
        <div className="orbit-dock">
          <div className="orbit-dock-inner">
            <span className="orbit-dock-msg">{message}</span>
            {editing ? (
              <>
                <button type="button" className="orbit-button" data-variant="ghost" disabled={action.pending} onClick={cancel}>취소</button>
                <button type="button" className="orbit-button" data-variant="primary" disabled={!ready || !onApprove || action.pending} onClick={approve}>고친 대로 승인<kbd className="orbit-kbd">⌘↵</kbd></button>
              </>
            ) : denying ? (
              <>
                <button type="button" className="orbit-button" data-variant="ghost" disabled={action.pending} onClick={cancel}>취소</button>
                <button type="button" className="orbit-button" data-variant="danger" disabled={!canDeny} onClick={deny}>거절하기<kbd className="orbit-kbd">⌘↵</kbd></button>
              </>
            ) : (
              <>
                {onDeny && <button type="button" className="orbit-button" data-variant="ghost" disabled={!ready || action.pending} onClick={deny}>거절</button>}
                {onApprove && (diff || proposal.body !== undefined) && <button type="button" className="orbit-button" disabled={!ready || action.pending} onClick={startEdit}>고쳐서 승인</button>}
                {onApprove && <button type="button" className="orbit-button" data-variant="primary" disabled={!ready || action.pending} onClick={approve}>승인<kbd className="orbit-kbd">A</kbd></button>}
              </>
            )}
          </div>
        </div>
      </div>
    </Frame>
  );
}

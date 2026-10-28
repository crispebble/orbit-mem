// 기억 고치기·새로 쓰기 폼. 저장은 ⌘S 또는 저장 단추, 아직 저장하지 않은 변경은 onDirtyChange로 알린다.
import {useEffect, useId, useRef, useState, type KeyboardEvent} from 'react';
import {Archive} from 'lucide-react';
import {ActionFeedback, Frame, useAction} from './shared.tsx';
import {Block, KindPill, StateMark} from './block.tsx';
import type {OrbitProps} from './orbit-props.ts';
import {GLOBAL_SCOPE, KINDS, memoryKindLabels, type MemoryKind, type MemoryState, type OrbitMemory} from './vocabulary.ts';

export interface MemoryFormValue {
  title: string;
  content: string;
  kind: MemoryKind;
  scope: string;
  state: MemoryState;
  tags: string[];
  stale: boolean;
  supersededBy?: string;
}

export interface MemoryFormProps extends OrbitProps {
  /** 고치는 기억. 없으면 새로 쓴다. */
  memory?: OrbitMemory;
  candidates?: readonly OrbitMemory[];
  scopes?: readonly string[];
  onSave: (value: MemoryFormValue) => void | Promise<void>;
  onCancel?: () => void;
  /** 처음 값(저장한 뒤에는 저장한 값)과 달라졌는지가 바뀔 때만 불린다(탭의 수정 표시 등). 아직 넣지 않은 태그 입력도 고친 것으로 본다. 다른 기억을 열 때는 key를 바꿔 새로 그린다. */
  onDirtyChange?: (dirty: boolean) => void;
  /** 칸을 고칠 때마다 지금 값으로 불린다(초안 저장 등). 처음 그릴 때는 부르지 않는다. */
  onChange?: (value: MemoryFormValue) => void;
}

export function MemoryForm({memory, candidates = [], scopes = [], onSave, onCancel, onDirtyChange, onChange, className = '', surface = 'plain'}: MemoryFormProps) {
  const ids = useId();
  const action = useAction();
  const [title, setTitle] = useState(memory?.title ?? '');
  const [content, setContent] = useState(memory?.content ?? '');
  const [kind, setKind] = useState<MemoryKind>(memory?.kind ?? 'fact');
  const [scope, setScope] = useState(memory?.scope || GLOBAL_SCOPE);
  const initialState: MemoryState =
    memory?.state === 'candidate' || memory?.state === 'confirmed' ? memory.state : memory?.state === 'stale' || memory?.state === 'hidden' ? 'confirmed' : 'candidate';
  const [state, setState] = useState<MemoryState>(initialState);
  const [tags, setTags] = useState<string[]>(memory?.tags ?? []);
  const [tagDraft, setTagDraft] = useState('');
  const [stale, setStale] = useState(memory?.state === 'stale');
  const [replace, setReplace] = useState(!!memory?.supersededBy);
  const [supersededBy, setSupersededBy] = useState(memory?.supersededBy ?? '');
  const scopeOptions = [...new Set([GLOBAL_SCOPE, ...scopes, scope])];
  const version = memory?.version ?? 1;

  function addTags(raw: string) {
    const added = raw.split(',').map(tag => tag.trim()).filter(tag => tag && !tags.includes(tag));
    if (added.length) setTags([...tags, ...added]);
    setTagDraft('');
    return added.length ? [...tags, ...added] : tags;
  }

  const canSave = title.trim().length > 0 && !action.pending;
  const current = (): MemoryFormValue => {
    const replacedBy = replace && supersededBy ? supersededBy : undefined;
    const isStale = stale || !!replacedBy;
    return {title: title.trim(), content: content.trim(), kind, scope, state: isStale ? 'stale' : state, tags, stale: isStale, ...(replacedBy ? {supersededBy: replacedBy} : {})};
  };
  const snapshot = JSON.stringify([title, content, kind, scope, state, tags, stale, replace, supersededBy]);
  const [baseline, setBaseline] = useState(snapshot);
  const dirty = snapshot !== baseline || tagDraft.trim() !== '';
  const reported = useRef({dirty: false, snapshot});
  const callbacks = useRef({onDirtyChange, onChange, current});
  callbacks.current = {onDirtyChange, onChange, current};
  useEffect(() => {
    const last = reported.current;
    if (last.snapshot !== snapshot) {
      last.snapshot = snapshot;
      callbacks.current.onChange?.(callbacks.current.current());
    }
    if (last.dirty !== dirty) {
      last.dirty = dirty;
      callbacks.current.onDirtyChange?.(dirty);
    }
  }, [snapshot, dirty]);

  function save() {
    if (!canSave) return;
    const finalTags = tagDraft.trim() ? addTags(tagDraft) : tags;
    const replacedBy = replace && supersededBy ? supersededBy : undefined;
    const isStale = stale || !!replacedBy;
    const value: MemoryFormValue = {title: title.trim(), content: content.trim(), kind, scope, state: isStale ? 'stale' : state, tags: finalTags, stale: isStale, ...(replacedBy ? {supersededBy: replacedBy} : {})};
    const saved = JSON.stringify([title, content, kind, scope, state, finalTags, stale, replace, supersededBy]);
    // 저장이 끝나면 그 값이 새 기준이다(같은 폼으로 이어서 고칠 때 dirty를 다시 알린다).
    void action.run(async () => {
      await onSave(value);
      setBaseline(saved);
    });
  }

  function onKeyDown(event: KeyboardEvent<HTMLFormElement>) {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      save();
    }
  }

  return (
    <Frame label="기억 고치기" surface={surface} className={`orbit-mem-form ${className}`}>
      <form className="orbit-mem-detail" onSubmit={event => { event.preventDefault(); save(); }} onKeyDown={onKeyDown}>
        <div className="orbit-mem-head">
          <KindPill kind={kind} />
          <StateMark state={stale || supersededBy ? 'stale' : state} />
          <span aria-hidden="true">·</span>
          <span>{scope}</span>
          <span aria-hidden="true">·</span>
          <span>{memory ? `고치는 중 · v${version} → v${version + 1}` : '새 기억 · v1'}</span>
        </div>
        <div className="orbit-fld">
          <label htmlFor={`${ids}-title`}>제목</label>
          <input id={`${ids}-title`} className="orbit-input" value={title} onChange={event => setTitle(event.target.value)} disabled={action.pending} />
        </div>
        <div className="orbit-fld">
          <label htmlFor={`${ids}-body`}>본문</label>
          <textarea id={`${ids}-body`} className="orbit-input" rows={4} value={content} onChange={event => setContent(event.target.value)} disabled={action.pending} />
          <span className="orbit-meta">짧게 씁니다. 세션에는 제목과 id만 들어가고, 본문은 AI가 필요할 때 읽습니다.</span>
        </div>
        <div className="orbit-fgrid">
          <div className="orbit-fld">
            <label htmlFor={`${ids}-kind`}>종류</label>
            <span className="orbit-pick">
              <select id={`${ids}-kind`} className="orbit-input orbit-select" value={kind} onChange={event => setKind(event.target.value as MemoryKind)} disabled={action.pending}>
                {KINDS.map(value => <option key={value} value={value}>{memoryKindLabels[value]}</option>)}
              </select>
            </span>
          </div>
          <div className="orbit-fld">
            <label htmlFor={`${ids}-scope`}>범위</label>
            <span className="orbit-pick">
              <select id={`${ids}-scope`} className="orbit-input orbit-select" value={scope} onChange={event => setScope(event.target.value)} disabled={action.pending}>
                {scopeOptions.map(value => <option key={value} value={value}>{value}</option>)}
              </select>
            </span>
          </div>
          <div className="orbit-fld">
            <label htmlFor={`${ids}-state`}>상태</label>
            <span className="orbit-pick">
              <select id={`${ids}-state`} className="orbit-input orbit-select" value={state} onChange={event => setState(event.target.value as MemoryState)} disabled={action.pending}>
                <option value="candidate">후보</option>
                <option value="confirmed">확정</option>
              </select>
            </span>
          </div>
        </div>
        <div className="orbit-fld">
          <label htmlFor={`${ids}-tag`}>태그</label>
          <div className="orbit-tag-input">
            {tags.map(tag => (
              <span className="orbit-tag" key={tag}>
                {tag}
                <button type="button" aria-label={`태그 ${tag} 제거`} onClick={() => setTags(tags.filter(item => item !== tag))}>×</button>
              </span>
            ))}
            <input
              id={`${ids}-tag`}
              aria-label="태그 추가"
              placeholder="태그 추가"
              value={tagDraft}
              disabled={action.pending}
              onChange={event => setTagDraft(event.target.value)}
              onBlur={() => { if (tagDraft.trim()) addTags(tagDraft); }}
              onKeyDown={event => {
                if ((event.key === 'Enter' || event.key === ',') && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  event.stopPropagation();
                  if (tagDraft.trim()) addTags(tagDraft);
                } else if (event.key === 'Backspace' && !tagDraft && tags.length) {
                  setTags(tags.slice(0, -1));
                }
              }}
            />
          </div>
        </div>
        <Block icon={<Archive size={14} aria-hidden="true" />} title="정리">
          <div className="orbit-clean-row">
            <input id={`${ids}-stale`} type="checkbox" checked={stale} onChange={event => setStale(event.target.checked)} disabled={action.pending} />
            <label htmlFor={`${ids}-stale`}>낡음으로 표시<span className="orbit-meta">다시 주입하지 않고 이력으로만 남깁니다</span></label>
          </div>
          <div className="orbit-clean-row">
            <input
              id={`${ids}-replace`}
              type="checkbox"
              checked={replace}
              onChange={event => {
                setReplace(event.target.checked);
                if (!event.target.checked) setSupersededBy('');
              }}
              disabled={action.pending}
            />
            <label htmlFor={`${ids}-replace`}>다른 기억으로 대체<span className="orbit-meta">대체할 기억을 고르면 이 기억은 낡음이 됩니다</span></label>
            <span className="orbit-pick">
              <select className="orbit-input orbit-select orbit-clean-pick" aria-label="대체할 기억" value={supersededBy} disabled={!replace || action.pending} onChange={event => setSupersededBy(event.target.value)}>
                <option value="">기억 고르기</option>
                {candidates.filter(item => item.id !== memory?.id).map(item => <option key={item.id} value={item.id}>{item.title}</option>)}
              </select>
            </span>
          </div>
        </Block>
        <ActionFeedback action={action} />
        <div className="orbit-editbar">
          <span className="orbit-meta">{memory ? `저장하면 v${version + 1}로 이력에 남습니다` : '저장하면 v1로 이력에 남습니다'}</span>
          <span className="orbit-block-sp" />
          {onCancel && <button type="button" className="orbit-button" data-variant="ghost" disabled={action.pending} onClick={onCancel}>취소</button>}
          <button type="submit" className="orbit-button" data-variant="primary" disabled={!canSave}>저장<kbd className="orbit-kbd">⌘S</kbd></button>
        </div>
      </form>
    </Frame>
  );
}

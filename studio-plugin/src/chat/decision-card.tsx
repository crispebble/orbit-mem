// 대화 창 안 결정 카드. Needs you 결정 카드(DecisionsView)와 같은 모양·동작:
// 선택지 단추(숫자 키 1-9), 「다른 답」 직접 쓰기, ⌘Enter로 보내기. 도구 허가(kind==='tool')는 「허가」 머리와 접힌 context.
import {useEffect, useRef, useState} from 'react';
import {Badge, Button, Callout, Disclosure, Input, Kbd} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {chatText} from '../i18n/messages/chat.ts';
import {daemonText} from '../i18n/messages/daemon.ts';
import {answerFor, emptyPicks, pickByDigit, pickChoice, writeText, type CardPicks} from './card.ts';
import {daemonErrorText} from './errors.ts';
import type {Entry} from './timeline.ts';

export interface DecisionCardProps {
  entry: Extract<Entry, {kind: 'decision'}>;
  /** 질문 글(Studio 언어, 없으면 title) */
  question: string;
  optionLabel(id: string): string;
  /** 보낸 답을 데몬이 받았는지. 던지면 오류 글을 보인다. */
  onAnswer(answer: string): Promise<{accepted: boolean}>;
}

type Notice = {tone: 'warning' | 'danger' | 'info'; text: string} | null;

export function DecisionCard({entry, question, optionLabel, onAnswer}: DecisionCardProps) {
  const t = useMessages(chatText);
  const d = useMessages(daemonText);
  const [picks, setPicks] = useState<CardPicks>(emptyPicks);
  const [sending, setSending] = useState(false);
  const [notice, setNotice] = useState<Notice>(null);
  const [done, setDone] = useState(false);
  const ref = useRef<HTMLElement>(null);
  const answer = answerFor(picks);
  const inert = done || entry.answered;

  const send = async () => {
    if (!answer || sending || inert) return;
    setSending(true);
    setNotice(null);
    try {
      const r = await onAnswer(answer);
      if (r.accepted) setDone(true);
      else {
        setDone(true);
        setNotice({tone: 'warning', text: t.card.taken});
      }
    } catch (error) {
      setNotice({tone: 'danger', text: `${t.card.failed} ${daemonErrorText(error, d.errors)}`});
    } finally {
      setSending(false);
    }
  };

  // 카드가 새로 나타나면 키가 바로 먹도록 포커스를 준다(다른 곳에 입력 중이면 가로채지 않는다)
  useEffect(() => {
    const active = document.activeElement;
    if (!active || active === document.body) ref.current?.focus({preventScroll: true});
  }, []);

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (inert) return;
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault();
      void send();
      return;
    }
    const target = event.target as HTMLElement;
    if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return;
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    const next = pickByDigit(entry.options, picks, event.key);
    if (next !== picks) {
      event.preventDefault();
      setPicks(next);
    }
  };

  const tool = entry.decisionKind === 'tool';
  const nameId = `orbit-card-${entry.seq}`;
  return (
    <article ref={ref} tabIndex={-1} className="orbit-card" aria-label={t.card.label} data-kind={entry.decisionKind} data-state={inert ? 'answered' : 'open'} onKeyDown={onKeyDown}>
      <header className="orbit-card-head">
        <Badge tone={tool ? 'warning' : 'accent'}>{tool ? t.card.permission : t.card.question}</Badge>
      </header>
      <h3 id={nameId} className="orbit-card-text">{question}</h3>
      {tool && entry.context && (
        <Disclosure title={t.card.context} size="sm">
          <pre className="orbit-card-context">{entry.context}</pre>
        </Disclosure>
      )}
      {notice && <Callout tone={notice.tone} title={notice.text} />}
      {entry.options.length > 0 && (
        <div className="orbit-card-options" role="radiogroup" aria-labelledby={nameId}>
          {entry.options.map((id, i) => (
            <button
              key={id}
              type="button"
              role="radio"
              aria-checked={picks.choice === id}
              disabled={inert}
              className="orbit-card-option"
              data-on={picks.choice === id || undefined}
              onClick={() => setPicks(p => pickChoice(p, id))}
            >
              {i < 9 && <Kbd>{i + 1}</Kbd>}
              <span className="orbit-card-option-label">{optionLabel(id)}</span>
            </button>
          ))}
        </div>
      )}
      {!inert && (
        <Input
          label={t.card.other}
          placeholder={t.card.otherPlaceholder}
          value={picks.text}
          onChange={event => setPicks(p => writeText(p, event.currentTarget.value))}
        />
      )}
      {!inert && (
        <footer className="orbit-card-foot">
          <span className="orbit-meta">{t.card.keys}</span>
          <Button variant="primary" size="sm" disabled={!answer || sending} onClick={() => void send()} aria-label={t.card.send}>
            {sending ? t.card.sending : t.card.send}
          </Button>
        </footer>
      )}
    </article>
  );
}

// 실행 기록(transcript): 이벤트 항목을 위에서 아래로 그린다. 고정 문구는 text(key, args, 기본 글)로 Studio 언어에 맞춘다.
import {Badge, Disclosure} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {chatText} from '../i18n/messages/chat.ts';
import {DecisionCard} from './decision-card.tsx';
import {formatCount, formatUsd, shortSha, tailLines} from './format.ts';
import type {Entry} from './timeline.ts';

export interface TranscriptActions {
  /** client.text */
  text(key: unknown, args: unknown, fallback: string): string;
  optionLabel(id: string): string;
  answer(decisionId: string, answer: string): Promise<{accepted: boolean}>;
  /** 결정을 답할 수 있는 상태인지(진행 중) */
  answerable: boolean;
}

type Tone = 'success' | 'danger' | 'warning' | 'neutral' | 'accent';
const STEP_TONE: Record<string, Tone> = {running: 'accent', done: 'success', retry: 'warning', failed: 'danger', aborted: 'neutral'};

function Row({title, tone, badge, children, meta}: {title: string; tone?: Tone; badge?: string; meta?: React.ReactNode; children?: React.ReactNode}) {
  return (
    <li className="orbit-chat-entry">
      <div className="orbit-row orbit-chat-entry-head">
        <span className="orbit-chat-entry-title">{title}</span>
        {badge && <Badge tone={tone ?? 'neutral'}>{badge}</Badge>}
        {meta && <span className="orbit-meta">{meta}</span>}
      </div>
      {children}
    </li>
  );
}

export function EntryView({entry, actions}: {entry: Entry; actions: TranscriptActions}) {
  const t = useMessages(chatText);
  const e = t.entry;
  switch (entry.kind) {
    case 'started':
      return <Row title={entry.resume ? e.resumed : e.started} meta={`${entry.profile} · ${entry.mode}`} />;
    case 'work':
      return <Row title={e.work(entry.workId)} />;
    case 'plan': {
      const byId = new Map(entry.steps.map(s => [s.id, s]));
      return (
        <Row title={entry.restored ? e.planRestored : e.plan}>
          {entry.goal && <p className="orbit-work-text"><strong>{e.planGoal}</strong> {entry.goal}</p>}
          <ol className="orbit-chat-plan" aria-label={e.plan}>
            {entry.batches.map((batch, bi) => (
              <li key={bi} className="orbit-chat-batch" data-parallel={batch.length > 1 || undefined}>
                <span className="orbit-meta">{e.batch(bi + 1)}{batch.length > 1 ? ` · ${e.batchParallel}` : ''}</span>
                <ul className="orbit-chat-batch-steps">
                  {batch.map(id => {
                    const s = byId.get(id);
                    if (!s) return null;
                    return (
                      <li key={id} className="orbit-chat-plan-step">
                        <span>{s.title || s.id}</span>
                        <Badge tone="neutral">{s.role}</Badge>
                        {entry.done.includes(id) && <Badge tone="success">{e.stepStatus.done}</Badge>}
                        {s.after.length > 0 && <span className="orbit-meta">{e.after}: {s.after.join(', ')}</span>}
                      </li>
                    );
                  })}
                </ul>
              </li>
            ))}
          </ol>
        </Row>
      );
    }
    case 'planRejected':
      return (
        <Row title={e.planRejected} tone="warning" badge={e.planRejected}>
          <ul className="orbit-chat-list">{entry.errors.map((x, i) => <li key={i} className="orbit-work-text">{x}</li>)}</ul>
        </Row>
      );
    case 'planApproved':
      return <Row title={e.planApproved[entry.by] ?? e.planApproved.user} />;
    case 'step': {
      const model = [entry.engine, entry.model].filter(Boolean).join(':');
      return (
        <Row
          title={`${e.step}: ${entry.title || entry.step}`}
          tone={STEP_TONE[entry.status]}
          badge={e.stepStatus[entry.status] ?? entry.status}
          meta={[entry.role, model, entry.attempt > 1 ? e.attempt(entry.attempt) : ''].filter(Boolean).join(' · ')}
        >
          {entry.status !== 'running' && (
            <p className="orbit-meta">
              {formatCount(entry.inputTokens)} {e.tokensIn} / {formatCount(entry.outputTokens)} {e.tokensOut}
              {entry.costUsd !== undefined ? ` · ${formatUsd(entry.costUsd)}` : ''}
              {entry.reason ? ` · ${entry.reason}` : ''}
            </p>
          )}
          {entry.output && (
            <Disclosure title={e.output} size="sm">
              <pre className="orbit-chat-pre">{entry.output}</pre>
            </Disclosure>
          )}
        </Row>
      );
    }
    case 'skipped':
      return <Row title={e.skipped} tone="warning" badge={e.skipReason[entry.reason] ?? entry.reason} meta={`${entry.role} · ${entry.ref}`}>{entry.detail && <p className="orbit-meta orbit-work-text">{entry.detail}</p>}</Row>;
    case 'fallback':
      return (
        <Row title={e.fallback} tone="warning" meta={`${entry.role} · ${entry.from} → ${entry.to ?? e.fallbackNone}`}>
          {entry.error && <p className="orbit-meta orbit-work-text">{entry.error}</p>}
        </Row>
      );
    case 'verify':
      return (
        <Row title={`${e.verify}: ${entry.step}`} tone={entry.ok ? 'success' : 'danger'} badge={entry.ok ? e.verifyPass : e.verifyFail}>
          <code className="orbit-chat-code">{entry.command}</code>
          {entry.outputTail && (
            <Disclosure title={e.outputTail} size="sm">
              <pre className="orbit-chat-pre">{tailLines(entry.outputTail)}</pre>
            </Disclosure>
          )}
        </Row>
      );
    case 'commit':
      return <Row title={e.commit} meta={entry.step}><p className="orbit-work-text"><code>{shortSha(entry.sha)}</code> {entry.subject}</p></Row>;
    case 'review':
      return (
        <Row title={e.review} tone={entry.verdict === 'pass' ? 'success' : entry.verdict === 'needs_fix' ? 'warning' : 'accent'} badge={e.verdict[entry.verdict] ?? entry.verdict}>
          {entry.reason && <p className="orbit-work-text">{entry.reason}</p>}
        </Row>
      );
    case 'warning':
      return <Row title={e.warning} tone="warning" badge={e.warning}><p className="orbit-work-text">{actions.text(entry.key, entry.args, entry.message)}</p></Row>;
    case 'limit':
      return <Row title={e.limit} tone="danger" badge={entry.limit}><p className="orbit-work-text">{actions.text(entry.key, entry.args, entry.detail)}</p></Row>;
    case 'decision': {
      const question = actions.text(entry.key, entry.args, entry.title);
      if (entry.answered || !actions.answerable) {
        return (
          <Row title={question} tone={entry.answered ? 'success' : 'neutral'} badge={entry.answered ? e.answered : e.unanswered}>
            {entry.answered && entry.answer && <p className="orbit-meta">{actions.optionLabel(entry.answer)}</p>}
          </Row>
        );
      }
      return (
        <li className="orbit-chat-entry">
          <DecisionCard entry={entry} question={question} optionLabel={actions.optionLabel} onAnswer={answer => actions.answer(entry.id, answer)} />
        </li>
      );
    }
    case 'finished': {
      const tone: Tone = entry.status === 'completed' ? 'success' : entry.status === 'failed' ? 'danger' : 'warning';
      return (
        <Row title={e.finished} tone={tone} badge={t.run.state[entry.status] ?? entry.status}>
          {entry.reason && <p className="orbit-meta">{e.reason}: {entry.reason}</p>}
          {entry.message && <p className="orbit-work-text">{actions.text(entry.key, entry.args, entry.message)}</p>}
        </Row>
      );
    }
  }
}

export function Transcript({entries, actions}: {entries: readonly Entry[]; actions: TranscriptActions}) {
  const t = useMessages(chatText);
  return (
    <ol className="orbit-chat-transcript" aria-label={t.run.transcript}>
      {entries.map(entry => <EntryView key={`${entry.kind}:${entry.seq}`} entry={entry} actions={actions} />)}
    </ol>
  );
}

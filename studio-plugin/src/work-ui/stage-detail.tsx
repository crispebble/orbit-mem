// 단계 상세(StageDetail)와 증거 한 건(VerificationItem). Forma에 넣지 않고 forma-ui PropertyList·Badge·TextLink로 조립한다.
// 이유: 칸 하나하나가 orbit.work 계약의 뜻(막힘 사유·검증·실행·커밋)이라 OpenOrbit 도메인이다(설계 「Forma 2차」 묶음 5).
import type {ReactNode} from 'react';
import {X} from 'lucide-react';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
import {Badge, Button, IconButton, PropertyList, TextLink} from '@onidot-labs/orbit-studio-plugin/ui';
import {pick, useMessages} from '../i18n/index.ts';
import {workText, workUiText} from '../i18n/messages/work.ts';
import {blockText, formatTime, runStateView, stepStatusView, verificationBadge} from '../work/display.ts';
import {commitsOf, evidenceKindLabel, evidenceOfStep, evidenceResultView, runLabelOf, waitingSessionOf, type CommitRef} from '../work/evidence.ts';
import {agoText, assigneeText, neighbors, type Neighbor} from '../work/graph.ts';
import {resumeGuide} from '../work/session-view.ts';
import type {Evidence, Run, Session, Step, Work, WorkSummary} from '../work/types.ts';

export interface StageNav {
  selectStep(stepId: string): void;
  openWork(workId: string, title?: string): void;
  openSession(sessionId: string, title?: string): void;
}

async function copy(ctx: PluginContext, text: string, what: string) {
  const t = pick(workUiText).copy;
  try {
    await navigator.clipboard.writeText(text);
    ctx.notify({title: t.copied(what), body: text, tone: 'info'});
  } catch (e) {
    ctx.notify({title: t.failed(what), body: e instanceof Error ? e.message : String(e), tone: 'warning'});
  }
}

async function openCommit(ctx: PluginContext, c: CommitRef) {
  const ok = await ctx.studio.openCommit(c.repo, c.sha);
  const t = pick(workUiText);
  if (!ok) ctx.notify({title: t.openCommitFailed, body: t.openCommitFailedBody(c.repo), tone: 'warning'});
}

const when = (iso: string | undefined, now: number) => (iso ? `${formatTime(iso)} (${agoText(iso, now)})` : undefined);

function Links({items, nav, empty}: {items: Neighbor[]; nav: StageNav; empty: string}) {
  const g = useMessages(workText).graph;
  if (items.length === 0) return <span className="orbit-meta">{empty}</span>;
  return (
    <span className="orbit-work-links">
      {items.map(n => (
        <span key={n.stepId}>
          {n.title ? <TextLink onClick={() => nav.selectStep(n.stepId)}>{n.title}</TextLink> : <span className="orbit-meta">{g.stepNotListed}</span>}
          {n.source === 'order' && <span className="orbit-meta"> · {g.inferredPredecessor}</span>}
          {n.source !== 'order' && n.source !== 'native' && <span className="orbit-meta"> · {g.unknownSource}</span>}
        </span>
      ))}
    </span>
  );
}

/** 증거 한 건: 종류·결과·요약·시각·수행한 실행, 커밋이면 sha·바뀐 파일 수와 열기, 판정이면 기록 위치 복사 */
export function VerificationItem({ctx, evidence, run, now, nav}: {ctx: PluginContext; evidence: Evidence; run?: Run; now: number; nav: StageNav}) {
  const t = useMessages(workUiText);
  const result = evidenceResultView(evidence.result);
  const commit = commitsOf([evidence])[0];
  return (
    <li className="orbit-work-evidence">
      <div className="orbit-row">
        <span className="orbit-work-kind">{evidenceKindLabel(evidence.kind)}</span>
        <Badge tone={result.tone}>{result.label}</Badge>
        <span className="orbit-meta" title={formatTime(evidence.at)}>{agoText(evidence.at, now)}</span>
      </div>
      {evidence.summary && <p className="orbit-work-text" title={evidence.summary}>{evidence.summary}</p>}
      <div className="orbit-row orbit-meta">
        {run && (run.sessionId
          ? <TextLink tone="muted" onClick={() => nav.openSession(run.sessionId!, runLabelOf(run))}>{runLabelOf(run)}</TextLink>
          : <span>{runLabelOf(run)}</span>)}
        {commit && (
          <>
            <span className="orbit-mono">{commit.sha.slice(0, 7)}</span>
            <span>{t.stage.changedFiles(commit.files.length)}</span>
            <TextLink onClick={() => void openCommit(ctx, commit)}>{t.stage.openCommit}</TextLink>
          </>
        )}
        {evidence.ref?.transcript && <TextLink tone="muted" onClick={() => void copy(ctx, evidence.ref!.transcript!, t.copy.transcriptLocation)}>{t.stage.copyTranscript}</TextLink>}
      </div>
    </li>
  );
}

/** 사용자 확인 기다림: Studio는 확인을 반영하지 않고(계약 1.0은 읽기 전용) 그 일을 하는 세션으로 가는 길만 준다. */
function WaitingGuide({ctx, work, step, sessions}: {ctx: PluginContext; work: Work; step: Step; sessions: readonly Session[]}) {
  const t = useMessages(workUiText).stage;
  const target = waitingSessionOf(work, step.id, sessions);
  return (
    <section className="orbit-work-waiting" aria-label={t.waitingGuideLabel}>
      <b>{t.waitingGuideLabel}</b>
      <p className="orbit-work-text">{step.block?.detail ?? t.waitingDefault} {t.waitingHow}</p>
      {target ? <WaitingTarget ctx={ctx} target={target} /> : <p className="orbit-meta">{t.originUnknown}</p>}
    </section>
  );
}

function WaitingTarget({ctx, target}: {ctx: PluginContext; target: NonNullable<ReturnType<typeof waitingSessionOf>>}) {
  const u = useMessages(workUiText);
  const t = u.stage;
  const guide = resumeGuide(target, {needCwd: true});
  return (
    <>
      <PropertyList items={[
        {label: t.host, value: guide.host},
        {label: t.surface, value: guide.surface},
        {label: t.directory, value: <span className="orbit-mono orbit-work-break">{target.cwd ?? pick(workText).unknown}</span>},
        {label: t.sessionId, value: <span className="orbit-mono orbit-work-break">{guide.sessionId}</span>},
      ]} />
      <div className="orbit-row">
        <Button variant="secondary" size="sm" onClick={() => void copy(ctx, guide.sessionId, u.copy.sessionId)}>{t.copySessionId}</Button>
        {guide.command && (
          <Button variant="secondary" size="sm" onClick={() => {
            // 명령은 입력만 해 두고 Enter는 사용자가 누른다(ctx.terminal.open)
            void ctx.terminal.open({cwd: target.cwd, input: guide.command}).then(ok => {
              if (!ok) ctx.notify({title: u.openTerminalFailed, body: target.cwd, tone: 'warning'});
            });
          }}>{t.resumeInTerminal}</Button>
        )}
      </div>
    </>
  );
}

export interface StageDetailProps {
  ctx: PluginContext;
  work: Work;
  step: Step;
  now: number;
  /** 하위 작업 제목을 찾는 데 쓴다 */
  works: readonly WorkSummary[];
  sessions: readonly Session[];
  nav: StageNav;
  onClose?: () => void;
}

export function StageDetail({ctx, work, step, now, works, sessions, nav, onClose}: StageDetailProps) {
  const t = useMessages(workUiText).stage;
  const unknown = useMessages(workText).unknown;
  const status = stepStatusView(step.status, step.block);
  const badge = verificationBadge(step.verification, step.status);
  const nb = neighbors(work.steps, step.id);
  const runs = work.runs.filter(r => r.stepId === step.id);
  const evidence = evidenceOfStep(work, step.id);
  const runById = new Map(work.runs.map(r => [r.id, r]));
  const items: {label: ReactNode; value: ReactNode}[] = [
    {label: t.status, value: (
      <span className="orbit-row">
        <Badge tone={status.tone}>{status.label}</Badge>
        {step.status === 'in_progress' && step.activeTitle && <span className="orbit-work-text">{step.activeTitle}</span>}
      </span>
    )},
  ];
  if (step.block) {
    items.push({label: t.blocked, value: <span className="orbit-work-text">{blockText(step.block)}</span>});
    items.push({label: t.since, value: when(step.block.since, now) ?? unknown});
  }
  items.push(
    {label: t.owner, value: assigneeText(step.assignee) ?? <span className="orbit-meta">{t.ownerNone}</span>},
    {label: t.before, value: <Links items={nb.before} nav={nav} empty={t.none} />},
    {label: t.after, value: <Links items={nb.after} nav={nav} empty={t.none} />},
    {label: t.verification, value: badge
      ? <Badge tone={badge.tone}>{badge.label}</Badge>
      : <span className="orbit-meta">{step.verification.state === 'pass' ? t.passed : t.noVerification}</span>},
    {label: t.started, value: when(step.startedAt, now) ?? <span className="orbit-meta">{t.notStarted}</span>},
  );
  if (step.finishedAt) items.push({label: t.finished, value: when(step.finishedAt, now)});
  if (step.childWorkIds.length) {
    items.push({label: t.childWorks, value: (
      <span className="orbit-work-links">
        {step.childWorkIds.map(id => {
          const title = works.find(w => w.id === id)?.title ?? work.children?.find(c => c.id === id)?.title;
          return <TextLink key={id} onClick={() => nav.openWork(id, title)}>{title ?? t.openChildWork}</TextLink>;
        })}
      </span>
    )});
  }

  return (
    <div className="orbit-work-detail">
      <div className="orbit-work-detail-head">
        <h3 className="orbit-work-detail-title">{step.title}</h3>
        {onClose && <IconButton label={t.closeDetail} size="sm" onClick={onClose}><X size={14} /></IconButton>}
      </div>
      {step.status === 'blocked' && step.block?.reason === 'waiting_user' && <WaitingGuide ctx={ctx} work={work} step={step} sessions={sessions} />}
      <PropertyList items={items} />
      <section aria-label={t.runsLabel}>
        <h4 className="orbit-work-sec">{t.runsHeading(runs.length)}</h4>
        {runs.length === 0 ? <p className="orbit-meta">{t.noRuns}</p> : (
          <ul className="orbit-work-runs">
            {runs.map(r => {
              const v = runStateView(r, now);
              return (
                <li key={r.id} className="orbit-row">
                  {r.sessionId
                    ? <TextLink onClick={() => nav.openSession(r.sessionId!, runLabelOf(r))}>{runLabelOf(r)}</TextLink>
                    : <span>{runLabelOf(r)}</span>}
                  {r.background && <span className="orbit-meta">{t.background}</span>}
                  <Badge tone={v.tone}>{v.label}</Badge>
                </li>
              );
            })}
          </ul>
        )}
      </section>
      <section aria-label={t.evidenceLabel}>
        <h4 className="orbit-work-sec">{t.evidenceHeading(evidence.length)}</h4>
        {evidence.length === 0 ? <p className="orbit-meta">{t.noEvidence}</p> : (
          <ul className="orbit-work-evidence-list">
            {evidence.map(e => <VerificationItem key={e.id} ctx={ctx} evidence={e} run={e.runId ? runById.get(e.runId) : undefined} now={now} nav={nav} />)}
          </ul>
        )}
      </section>
    </div>
  );
}

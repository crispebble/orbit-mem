// 단계 상세(StageDetail)와 증거 한 건(VerificationItem). Forma에 넣지 않고 forma-ui PropertyList·Badge·TextLink로 조립한다.
// 이유: 칸 하나하나가 orbit.work 계약의 뜻(막힘 사유·검증·실행·커밋)이라 Orbit 도메인이다(설계 「Forma 2차」 묶음 5).
import type {ReactNode} from 'react';
import {X} from 'lucide-react';
import type {PluginContext} from '@onidot-labs/studio-plugin';
import {Badge, Button, IconButton, PropertyList, TextLink} from '@onidot-labs/studio-plugin/ui';
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
  try {
    await navigator.clipboard.writeText(text);
    ctx.notify({title: `${what}를 복사했습니다`, body: text, tone: 'info'});
  } catch (e) {
    ctx.notify({title: `${what}를 복사하지 못했습니다`, body: e instanceof Error ? e.message : String(e), tone: 'warning'});
  }
}

async function openCommit(ctx: PluginContext, c: CommitRef) {
  const ok = await ctx.studio.openCommit(c.repo, c.sha);
  if (!ok) ctx.notify({title: '커밋을 열지 못했습니다', body: `Studio가 보고 있는 저장소가 아니거나 커밋이 없습니다: ${c.repo}`, tone: 'warning'});
}

const when = (iso: string | undefined, now: number) => (iso ? `${formatTime(iso)} (${agoText(iso, now)})` : undefined);

function Links({items, nav, empty}: {items: Neighbor[]; nav: StageNav; empty: string}) {
  if (items.length === 0) return <span className="orbit-meta">{empty}</span>;
  return (
    <span className="orbit-work-links">
      {items.map(n => (
        <span key={n.stepId}>
          {n.title ? <TextLink onClick={() => nav.selectStep(n.stepId)}>{n.title}</TextLink> : <span className="orbit-meta">목록에 없는 단계</span>}
          {n.source === 'order' && <span className="orbit-meta"> · 순서로 추정한 선행</span>}
          {n.source !== 'order' && n.source !== 'native' && <span className="orbit-meta"> · 출처 알 수 없음</span>}
        </span>
      ))}
    </span>
  );
}

/** 증거 한 건: 종류·결과·요약·시각·수행한 실행, 커밋이면 sha·바뀐 파일 수와 열기, 판정이면 기록 위치 복사 */
export function VerificationItem({ctx, evidence, run, now, nav}: {ctx: PluginContext; evidence: Evidence; run?: Run; now: number; nav: StageNav}) {
  const result = evidenceResultView(evidence.result);
  const commit = commitsOf([evidence])[0];
  return (
    <li className="orbit-work-evidence">
      <div className="orbit-row">
        <span className="orbit-work-kind">{evidenceKindLabel(evidence.kind)}</span>
        <Badge tone={result.tone}>{result.label}</Badge>
        <span className="orbit-meta" title={formatTime(evidence.at)}>{agoText(evidence.at, now)}</span>
      </div>
      {evidence.summary && <p className="orbit-work-text">{evidence.summary}</p>}
      <div className="orbit-row orbit-meta">
        {run && (run.sessionId
          ? <TextLink tone="muted" onClick={() => nav.openSession(run.sessionId!, runLabelOf(run))}>{runLabelOf(run)}</TextLink>
          : <span>{runLabelOf(run)}</span>)}
        {commit && (
          <>
            <span className="orbit-mono">{commit.sha.slice(0, 7)}</span>
            <span>바뀐 파일 {commit.files.length}개</span>
            <TextLink onClick={() => void openCommit(ctx, commit)}>커밋 열기</TextLink>
          </>
        )}
        {evidence.ref?.transcript && <TextLink tone="muted" onClick={() => void copy(ctx, evidence.ref!.transcript!, '대화 기록 위치')}>기록 위치 복사</TextLink>}
      </div>
    </li>
  );
}

/** 사용자 확인 기다림: Studio는 확인을 반영하지 않고(계약 1.0은 읽기 전용) 그 일을 하는 세션으로 가는 길만 준다. */
function WaitingGuide({ctx, work, step, sessions}: {ctx: PluginContext; work: Work; step: Step; sessions: readonly Session[]}) {
  const target = waitingSessionOf(work, step.id, sessions);
  return (
    <section className="orbit-work-waiting" aria-label="사용자 확인 기다림">
      <b>사용자 확인 기다림</b>
      <p className="orbit-work-text">{step.block?.detail ?? '세션에서 사용자 확인을 기다립니다.'} 확인은 그 세션 대화에서 합니다.</p>
      {target ? <WaitingTarget ctx={ctx} target={target} /> : <p className="orbit-meta">이 작업을 연 세션을 알 수 없습니다.</p>}
    </section>
  );
}

function WaitingTarget({ctx, target}: {ctx: PluginContext; target: NonNullable<ReturnType<typeof waitingSessionOf>>}) {
  const guide = resumeGuide(target);
  return (
    <>
      <PropertyList items={[
        {label: '호스트', value: guide.host},
        {label: '화면', value: guide.surface},
        {label: '디렉토리', value: <span className="orbit-mono orbit-work-break">{target.cwd ?? '알 수 없음'}</span>},
        {label: '세션 id', value: <span className="orbit-mono orbit-work-break">{guide.sessionId}</span>},
      ]} />
      <div className="orbit-row">
        <Button variant="secondary" size="sm" onClick={() => void copy(ctx, guide.sessionId, '세션 id')}>세션 id 복사</Button>
        {guide.command && (
          <Button variant="secondary" size="sm" onClick={() => {
            // 명령은 입력만 해 두고 Enter는 사용자가 누른다(ctx.terminal.open)
            void ctx.terminal.open({cwd: target.cwd, input: guide.command}).then(ok => {
              if (!ok) ctx.notify({title: '터미널을 열지 못했습니다', body: target.cwd, tone: 'warning'});
            });
          }}>터미널에서 이어 가기</Button>
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
  const status = stepStatusView(step.status, step.block);
  const badge = verificationBadge(step.verification, step.status);
  const nb = neighbors(work.steps, step.id);
  const runs = work.runs.filter(r => r.stepId === step.id);
  const evidence = evidenceOfStep(work, step.id);
  const runById = new Map(work.runs.map(r => [r.id, r]));
  const items: {label: ReactNode; value: ReactNode}[] = [
    {label: '상태', value: (
      <span className="orbit-row">
        <Badge tone={status.tone}>{status.label}</Badge>
        {step.status === 'in_progress' && step.activeTitle && <span className="orbit-work-text">{step.activeTitle}</span>}
      </span>
    )},
  ];
  if (step.block) {
    items.push({label: '막힘', value: <span className="orbit-work-text">{blockText(step.block)}</span>});
    items.push({label: '언제부터', value: when(step.block.since, now) ?? '알 수 없음'});
  }
  items.push(
    {label: '담당', value: assigneeText(step.assignee) ?? <span className="orbit-meta">정해지지 않음</span>},
    {label: '선행', value: <Links items={nb.before} nav={nav} empty="없음" />},
    {label: '후행', value: <Links items={nb.after} nav={nav} empty="없음" />},
    {label: '검증', value: badge
      ? <Badge tone={badge.tone}>{badge.label}</Badge>
      : <span className="orbit-meta">{step.verification.state === 'pass' ? '통과' : '검증 결과 없음'}</span>},
    {label: '시작', value: when(step.startedAt, now) ?? <span className="orbit-meta">아직 시작 전</span>},
  );
  if (step.finishedAt) items.push({label: '끝', value: when(step.finishedAt, now)});
  if (step.childWorkIds.length) {
    items.push({label: '하위 작업', value: (
      <span className="orbit-work-links">
        {step.childWorkIds.map(id => {
          const title = works.find(w => w.id === id)?.title ?? work.children?.find(c => c.id === id)?.title;
          return <TextLink key={id} onClick={() => nav.openWork(id, title)}>{title ?? '하위 작업 열기'}</TextLink>;
        })}
      </span>
    )});
  }

  return (
    <div className="orbit-work-detail">
      <div className="orbit-work-detail-head">
        <h3 className="orbit-work-detail-title">{step.title}</h3>
        {onClose && <IconButton label="상세 닫기" size="sm" onClick={onClose}><X size={14} /></IconButton>}
      </div>
      {step.status === 'blocked' && step.block?.reason === 'waiting_user' && <WaitingGuide ctx={ctx} work={work} step={step} sessions={sessions} />}
      <PropertyList items={items} />
      <section aria-label="실행">
        <h4 className="orbit-work-sec">실행 {runs.length}건</h4>
        {runs.length === 0 ? <p className="orbit-meta">이 단계에 묶인 실행이 없습니다.</p> : (
          <ul className="orbit-work-runs">
            {runs.map(r => {
              const v = runStateView(r, now);
              return (
                <li key={r.id} className="orbit-row">
                  {r.sessionId
                    ? <TextLink onClick={() => nav.openSession(r.sessionId!, runLabelOf(r))}>{runLabelOf(r)}</TextLink>
                    : <span>{runLabelOf(r)}</span>}
                  {r.background && <span className="orbit-meta">뒤에서</span>}
                  <Badge tone={v.tone}>{v.label}</Badge>
                </li>
              );
            })}
          </ul>
        )}
      </section>
      <section aria-label="증거">
        <h4 className="orbit-work-sec">증거 {evidence.length}건</h4>
        {evidence.length === 0 ? <p className="orbit-meta">남은 증거가 없습니다.</p> : (
          <ul className="orbit-work-evidence-list">
            {evidence.map(e => <VerificationItem key={e.id} ctx={ctx} evidence={e} run={e.runId ? runById.get(e.runId) : undefined} now={now} nav={nav} />)}
          </ul>
        )}
      </section>
    </div>
  );
}

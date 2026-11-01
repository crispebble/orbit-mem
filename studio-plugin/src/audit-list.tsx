// 감사 목록 본문: 자동 변경·충돌·반복 교정·대기 제안 네 섹션. 상태와 호출은 props로 받아 서버 렌더로도 그릴 수 있다.
import type {ReactNode} from 'react';
import {Button, Count, Disclosure, InboxItem, Label, Mark} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from './i18n/index.ts';
import {auditText} from './i18n/messages/audit.ts';
import {formatDate, proposalKindLabel, type AuditView, type OrbAuditChange, type OrbAuditConflict, type OrbRepeatedCorrection} from './model.ts';
import {ProposalGroupList, type ProposalGroupListProps} from './parts/index.ts';

type Tone = 'neutral' | 'success' | 'warning' | 'danger';
const VERDICT_TONE: Record<string, Tone> = {pending: 'neutral', helped: 'success', harmful: 'danger', no_effect: 'neutral', mixed: 'warning'};

export interface AuditListProps {
  audit: AuditView;
  groups: ProposalGroupListProps['groups'];
  selectedId?: string;
  busyKind?: string;
  /** 처리 중인 항목 id(그 항목의 단추를 끈다) */
  busyId?: string;
  /** false면 결정 단추를 그리지 않는다(연결 끊김 등) */
  actionable?: boolean;
  now?: Date;
  onOpenChange: (c: OrbAuditChange) => void;
  onOpenConflict: (c: OrbAuditConflict) => void;
  onOpenProposal: (id: string) => void;
  onOpenMemory: (id: string, title: string) => void;
  onRevert: (c: OrbAuditChange) => void;
  onConfirm: (c: OrbAuditConflict) => void;
  onStale: (c: OrbAuditConflict) => void;
  onDismiss: (id: string) => void;
  onDecideGroup: ProposalGroupListProps['onDecideGroup'];
}

function Section({title, count, empty, children}: {title: string; count: number; empty: string; children: ReactNode}) {
  return (
    <Disclosure title={title} meta={<Count>{count}</Count>} defaultOpen size="sm">
      {count === 0 ? <p className="orbit-audit-empty">{empty}</p> : <div className="orbit-side-list" role="list" aria-label={title}>{children}</div>}
    </Disclosure>
  );
}

export function AuditList(p: AuditListProps) {
  const t = useMessages(auditText);
  const now = p.now ?? new Date();
  const busy = p.busyId !== undefined;
  const evidence = t.evidence as Record<string, string>;
  const verdicts = t.verdicts as Record<string, string>;
  const targetKinds = t.targetKinds as Record<string, string>;

  return (
    <div className="orbit-groups">
      <Section title={t.sections.changes} count={p.audit.changes.length} empty={t.empty.changes}>
        {p.audit.changes.map(c => {
          const canRevert = p.actionable !== false && !c.revertedBy && c.kind !== 'revert';
          const sub = [proposalKindLabel(c.kind), c.evidence ? (evidence[c.evidence] ?? c.evidence) : '', formatDate(c.at, now)].filter(Boolean).join(' · ');
          return (
            <div role="listitem" key={c.ledgerId} className="orbit-audit-item">
              <InboxItem
                icon={<Mark size="sm">{proposalKindLabel(c.kind).slice(0, 1)}</Mark>}
                title={c.title}
                subtitle={sub}
                current={p.selectedId === c.proposalId}
                onOpen={() => p.onOpenChange(c)}
              />
              <div className="orbit-row orbit-audit-meta">
                <Label tone={VERDICT_TONE[c.verdict] ?? 'neutral'}>{verdicts[c.verdict] ?? c.verdict}</Label>
                {c.autoReverted && <Label tone="danger">{t.autoReverted}</Label>}
                {c.revertedBy && !c.autoReverted && <Label>{t.reverted}</Label>}
                {c.kind === 'revert' && c.revertOf && <Label>{t.revertOf}</Label>}
                {canRevert && (
                  <Button size="sm" variant="secondary" aria-label={t.actions.revertLabel(c.title)} disabled={busy} loading={p.busyId === c.ledgerId}
                    onClick={() => p.onRevert(c)}>{t.actions.revert}</Button>
                )}
              </div>
            </div>
          );
        })}
      </Section>

      <Section title={t.sections.conflicts} count={p.audit.conflicts.length} empty={t.empty.conflicts}>
        {p.audit.conflicts.map(c => (
          <div role="listitem" key={c.id} className="orbit-audit-item">
            <InboxItem
              icon={<Mark size="sm">!</Mark>}
              title={t.candidateVsRule(c.title, c.otherTitle ?? t.unknownRule)}
              subtitle={[c.note, formatDate(c.at, now)].filter(Boolean).join(' · ')}
              onOpen={() => p.onOpenConflict(c)}
            />
            {p.actionable !== false && (
              <div className="orbit-row orbit-audit-meta">
                <Button size="sm" variant="secondary" aria-label={t.actions.confirmAnywayLabel(c.title)} disabled={busy} loading={p.busyId === c.id}
                  onClick={() => p.onConfirm(c)}>{t.actions.confirmAnyway}</Button>
                <Button size="sm" variant="secondary" aria-label={t.actions.markStaleLabel(c.title)} disabled={busy}
                  onClick={() => p.onStale(c)}>{t.actions.markStale}</Button>
                <Button size="sm" variant="ghost" aria-label={t.actions.dismissLabel(c.title)} disabled={busy}
                  onClick={() => p.onDismiss(c.id)}>{t.actions.dismiss}</Button>
              </div>
            )}
          </div>
        ))}
      </Section>

      <Section title={t.sections.corrections} count={p.audit.corrections.length} empty={t.empty.corrections}>
        {p.audit.corrections.map((c: OrbRepeatedCorrection) => {
          const name = c.title || c.target;
          const harness = c.harness || c.targetKind === 'skill' || c.targetKind === 'role';
          const quote = c.quotes?.at(-1);
          return (
            <div role="listitem" key={c.key} className="orbit-audit-item">
              <InboxItem
                icon={<Mark size="sm">{(targetKinds[c.targetKind] ?? c.targetKind).slice(0, 1)}</Mark>}
                title={name}
                subtitle={[targetKinds[c.targetKind] ?? c.targetKind, t.sessions(c.sessions, c.threshold), formatDate(c.last, now)].filter(Boolean).join(' · ')}
                onOpen={() => { if (c.targetKind === 'memory') p.onOpenMemory(c.target, name); }}
              />
              {quote && <p className="orbit-audit-quote">{quote}</p>}
              <div className="orbit-row orbit-audit-meta">
                {harness && <Label tone="accent">{t.harnessCandidate}</Label>}
                {c.ackedAt ? (
                  <Label>{t.reviewed}</Label>
                ) : p.actionable !== false && (
                  <Button size="sm" variant="secondary" aria-label={t.actions.markReviewedLabel(name)} disabled={busy} loading={p.busyId === c.key}
                    onClick={() => p.onDismiss(c.key)}>{t.actions.markReviewed}</Button>
                )}
              </div>
            </div>
          );
        })}
      </Section>

      <Disclosure title={t.sections.pending} meta={<Count>{p.groups.reduce((n, g) => n + g.items.length, 0)}</Count>} defaultOpen size="sm">
        {p.groups.length === 0 ? (
          <p className="orbit-audit-empty">{t.empty.pending}</p>
        ) : (
          <ProposalGroupList
            groups={p.groups}
            selectedId={p.selectedId}
            busyKind={p.busyKind}
            decidable={p.actionable !== false}
            onOpen={p.onOpenProposal}
            onDecideGroup={p.onDecideGroup}
          />
        )}
      </Disclosure>
    </div>
  );
}

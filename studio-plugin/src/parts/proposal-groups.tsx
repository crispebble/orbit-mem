// 제안 묶음 목록: 종류별 묶음마다 이름·건수와 묶음 승인·거절, 그 아래 제안들(설계 · Orbit 3차 O3-1).
// Studio 키트 부품(Disclosure·Count·Button·InboxItem·Mark)을 엮기만 한다. 크기는 키트 토큰을 따른다.
import {useMessages} from '../i18n/index.ts';
import {proposalText} from '../i18n/messages/proposals.ts';
import {Button, Count, Disclosure, InboxItem, Mark} from '@onidot-labs/studio-plugin/ui';

export interface ProposalGroupItem {
  id: string;
  title: string;
  subtitle?: string;
  unread?: boolean;
}

export interface ProposalGroupView {
  kind: string;
  label: string;
  items: readonly ProposalGroupItem[];
}

export interface ProposalGroupListProps {
  groups: readonly ProposalGroupView[];
  selectedId?: string;
  /** 결정 중인 묶음의 종류. 그 묶음의 단추를 끈다 */
  busyKind?: string;
  /** false면 묶음 단추를 그리지 않는다(연결 끊김 등) */
  decidable?: boolean;
  onOpen: (id: string) => void;
  onDecideGroup: (kind: string, decision: 'approve' | 'reject') => void;
}

export function ProposalGroupList({groups, selectedId, busyKind, decidable = true, onOpen, onDecideGroup}: ProposalGroupListProps) {
  const t = useMessages(proposalText).groups;
  return (
    <div className="orbit-groups">
      {groups.map(g => {
        const busy = busyKind !== undefined;
        return (
          <Disclosure key={g.kind} title={g.label} meta={<Count>{g.items.length}</Count>} defaultOpen size="sm">
            {decidable && (
              <div className="orbit-group-actions">
                <Button size="sm" variant="secondary" aria-label={t.approveAllLabel(g.label, g.items.length)} disabled={busy} loading={busyKind === g.kind}
                  onClick={() => onDecideGroup(g.kind, 'approve')}>{t.approveAll}</Button>
                <Button size="sm" variant="ghost" aria-label={t.rejectAllLabel(g.label, g.items.length)} disabled={busy}
                  onClick={() => onDecideGroup(g.kind, 'reject')}>{t.rejectAll}</Button>
              </div>
            )}
            <div className="orbit-side-list" role="list" aria-label={t.listLabel(g.label)}>
              {g.items.map(it => (
                <div role="listitem" key={it.id}>
                  <InboxItem
                    icon={<Mark size="sm">{g.label.slice(0, 1)}</Mark>}
                    title={it.title}
                    subtitle={it.subtitle}
                    unread={it.unread}
                    current={selectedId === it.id}
                    onOpen={() => onOpen(it.id)}
                  />
                </div>
              ))}
            </div>
          </Disclosure>
        );
      })}
    </div>
  );
}

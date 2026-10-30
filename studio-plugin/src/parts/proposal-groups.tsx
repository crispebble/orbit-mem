// 제안 묶음 목록: 종류별 묶음마다 이름·건수와 묶음 승인·거절, 그 아래 제안들(설계 · Orbit 3차 O3-1).
// Studio 키트 부품(Disclosure·Count·Button·InboxItem·Mark)을 엮기만 한다. 크기는 키트 토큰을 따른다.
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
  return (
    <div className="orbit-groups">
      {groups.map(g => {
        const what = `${g.label} ${g.items.length}건`;
        const busy = busyKind !== undefined;
        return (
          <Disclosure key={g.kind} title={g.label} meta={<Count>{g.items.length}</Count>} defaultOpen size="sm">
            {decidable && (
              <div className="orbit-group-actions">
                <Button size="sm" variant="secondary" aria-label={`${what} 모두 승인`} disabled={busy} loading={busyKind === g.kind}
                  onClick={() => onDecideGroup(g.kind, 'approve')}>모두 승인</Button>
                <Button size="sm" variant="ghost" aria-label={`${what} 모두 거절`} disabled={busy}
                  onClick={() => onDecideGroup(g.kind, 'reject')}>모두 거절</Button>
              </div>
            )}
            <div className="orbit-side-list" role="list" aria-label={`${g.label} 제안`}>
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

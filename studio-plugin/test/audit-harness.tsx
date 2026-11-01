// 감사 목록을 서버 렌더로 그려 글을 돌려준다(audit-render.test.ts와 미리보기 스크립트가 esbuild로 묶어 부른다).
import {renderToStaticMarkup} from 'react-dom/server';
import {AuditList, type AuditListProps} from '../src/audit-list.tsx';
import {toAuditView} from '../src/model.ts';
import {audit, T0} from './audit-fixture.ts';

export {setLocale} from '../src/i18n/index.ts';
export {audit, T0};

const noop = () => {};
export const groups = [
  {kind: 'stale', label: 'Clean up stale memory', items: [{id: 'q-1', title: '낡은 메모 정리', subtitle: '오늘', unread: true}, {id: 'q-2', title: '옛 배포 메모', subtitle: '어제', unread: true}]},
];

export const render = (p: Partial<AuditListProps> = {}) =>
  renderToStaticMarkup(
    <AuditList
      audit={toAuditView(audit)} groups={groups} now={new Date(T0)}
      onOpenChange={noop} onOpenConflict={noop} onOpenProposal={noop} onOpenMemory={noop}
      onRevert={noop} onConfirm={noop} onStale={noop} onDismiss={noop} onDecideGroup={noop}
      {...p}
    />,
  );

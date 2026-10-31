// P5: 프로필의 이번 달 모델별 내역(엔진·모델·과금·비용·토큰·실행 수).
import type {ProfileUsage} from '../daemon/api.ts';
import {formatCount, formatUsd} from '../chat/format.ts';
import {useMessages} from '../i18n/index.ts';
import {profilesText} from '../i18n/messages/profiles.ts';

export function UsageTable({usage}: {usage: ProfileUsage | undefined}) {
  const t = useMessages(profilesText);
  if (!usage || usage.models.length === 0) return <p className="orbit-meta">{t.usage.empty}</p>;
  return (
    <table className="orbit-prof-table" aria-label={t.usage.table}>
      <thead>
        <tr>
          <th scope="col">{t.usage.engine}</th>
          <th scope="col">{t.usage.model}</th>
          <th scope="col">{t.usage.billing}</th>
          <th scope="col" className="orbit-num">{t.usage.cost}</th>
          <th scope="col" className="orbit-num">{t.usage.tokens}</th>
          <th scope="col" className="orbit-num">{t.usage.runs}</th>
        </tr>
      </thead>
      <tbody>
        {usage.models.map(m => (
          <tr key={`${m.engine}:${m.model}:${m.billing}`}>
            <td>{m.engine}</td>
            <td>{m.model}</td>
            <td>{t.usage.billingKind[m.billing] ?? m.billing}</td>
            <td className="orbit-num">{formatUsd(m.costUsd)}</td>
            <td className="orbit-num">{formatCount(m.inputTokens)} / {formatCount(m.outputTokens)}</td>
            <td className="orbit-num">{formatCount(m.runs)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

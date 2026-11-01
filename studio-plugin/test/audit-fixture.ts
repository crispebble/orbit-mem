// 감사 목록 시험·미리보기용 가짜 데이터: 자동 확정 1, 자동 되돌림 1, 어긋남 1, 하네스 후보 1(+기억 교정 1), 대기 제안 2
import type {OrbAudit} from '../src/model.ts';

const day = 86_400_000;
export const T0 = new Date('2026-11-01T12:00:00+09:00').getTime();

export const audit: OrbAudit = {
  changes: [
    {ledgerId: 'l-1', proposalId: 'p-auto', kind: 'confirm', title: '테스트는 mock 없이 실제 DB로', evidence: 'seen_days', actor: 'auto', at: T0 - day, targets: ['m-1'], change: '후보 → 확정', verdict: 'helped'},
    {ledgerId: 'l-2', proposalId: 'p-bad', kind: 'merge', title: '배포 전 확인 절차 합치기', evidence: 'followed_verified', actor: 'auto', at: T0 - 2 * day, targets: ['m-2', 'm-3'], verdict: 'harmful', revertedBy: 'l-3', autoReverted: true},
    {ledgerId: 'l-3', proposalId: 'p-rev', kind: 'revert', title: '배포 전 확인 절차 합치기', actor: 'auto', at: T0 - 2 * day + 1000, revertOf: 'l-2', verdict: 'pending'},
  ],
  conflicts: [
    {id: 'f-1', kind: 'conflict', memoryId: 'm-9', otherId: 'm-8', note: '반대 방향', at: T0 - day, title: '커밋 메시지는 영어로', state: 'open', otherTitle: '커밋 메시지는 한글로'},
  ],
  corrections: [
    {key: 'correction:skill:tdd', targetKind: 'skill', target: 'tdd', title: 'tdd 스킬', sessions: 4, corrections: 6, threshold: 3, over: true, harness: true, first: T0 - 5 * day, last: T0 - day, quotes: ['처음 인용', '테스트부터 쓰라고 했잖아']},
    {key: 'correction:memory:m-1', targetKind: 'memory', target: 'm-1', title: '테스트는 mock 없이 실제 DB로', sessions: 3, corrections: 3, threshold: 3, over: true, harness: false, first: T0 - 4 * day, last: T0 - day, quotes: ['mock 쓰지 마']},
  ],
  pending: 2,
  policy: {confirmSeenDays: 3, confirmFollowedVerified: 2, correctionRepeatSessions: 3},
};

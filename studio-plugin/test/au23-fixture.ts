// AU-2·AU-3 시험 자료: orbit.work 1.3 `work_get` 응답(조사 + 가정 셋). claude-task 예시를 바탕으로 칸만 더한다.
// 가정의 steps는 단계 id(`st-…`)나 호스트 이름표(`native.id`, 계획 id)로 적힌다.
import {example} from './work-fake.ts';

export const STEP_REOPENED = 'st-3c4d5e6f7081';
export const STEP_WAITING = 'st-4d5e6f708192';
export const STEP_NATIVE = 'impl';
export const STEP_NATIVE_ID = 'st-5e6f708192a3';

export function auResult(over: {assumptions?: unknown; discovery?: unknown} = {}): Record<string, unknown> {
  const base = example('work_get.claude-task').result as Record<string, any>;
  const work = JSON.parse(JSON.stringify(base.work));
  work.steps = work.steps.map((s: any) => (s.id === STEP_NATIVE_ID ? {...s, native: {kind: 'orbit-pi-plan', id: STEP_NATIVE}} : s));
  work.discovery = 'discovery' in over ? over.discovery : {
    problem: 'Studio에서 조사 결과와 가정을 볼 수 없다',
    decisions: [
      {point: '가정을 어디에 보일까', evidence: ['src/work-ui/work-tab.tsx', 'w-7d2a91c04e5b'], settled: '작업 머리 아래'},
      {point: '카드 출처 이름', evidence: []},
    ],
    idealEnd: '작업을 열면 조사와 가정이 한눈에 보인다',
    unknowns: ['오래된 작업에 칸이 없을 때의 모양'],
    sources: ['src/work/graph.ts', 'mem-91ab', ''],
    size: 'medium',
    rounds: 2,
    by: 'orbit-pi',
    at: '2026-10-28T05:00:00.000Z',
  };
  work.assumptions = 'assumptions' in over ? over.assumptions : [
    {
      id: 'as-evidence', point: '배지 색', chosen: '기존 칩 색', basis: 'FlowGraph 배지 톤을 그대로 쓴다', confidence: 'high',
      rung: 'evidence', state: 'settled', steps: [], at: '2026-10-28T05:01:00.000Z',
    },
    {
      id: 'as-overturned', point: '요약 위치', chosen: '머리 위', basis: '되돌리기 쉬워 먼저 진행', confidence: 'medium',
      rung: 'assume', state: 'overturned', options: ['머리 위', '머리 아래'], cardId: 'dc-1', steps: [STEP_REOPENED],
      answer: '머리 아래', answeredBy: 'studio', answeredAt: Date.parse('2026-10-28T06:00:00.000Z'), at: '2026-10-28T05:02:00.000Z',
    },
    {
      id: 'as-ask', point: '운영 데이터를 지울까', chosen: '', basis: '되돌릴 수 없어 사람이 정해야 한다', confidence: 'low',
      rung: 'ask', state: 'open', options: ['지운다', '둔다'], cardId: 'dc-2', steps: [STEP_NATIVE, STEP_WAITING], at: '2026-10-28T05:03:00.000Z',
    },
  ];
  return {...base, contract: {...(base.contract as object), minor: 3}, work};
}

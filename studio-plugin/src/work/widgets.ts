// 홈 위젯 「진행 중 작업」용 데이터(화면은 나중).
import {progressOf, waitingSteps} from './display.ts';
import type {Work, WorkSummary} from './types.ts';

export interface RunningWorksData {
  /** 열린 작업 수 */
  open: number;
  /** 열린 작업들의 진행 끝난 단계 수 */
  stepsDone: number;
  /** 열린 작업들의 전체 단계 수(취소 제외) */
  stepsTotal: number;
  /** 사용자 확인을 기다리는 단계 수. 단계를 읽은 작업만 센다 */
  waiting: number;
}

export function runningWorksData(works: readonly WorkSummary[], details: Readonly<Record<string, Work>>): RunningWorksData {
  const open = works.filter(w => w.state === 'open');
  let stepsDone = 0;
  let stepsTotal = 0;
  let waiting = 0;
  for (const w of open) {
    const p = progressOf(w);
    stepsDone += p.done;
    stepsTotal += p.total;
    const d = details[w.id];
    if (d) waiting += waitingSteps(d).length;
  }
  return {open: open.length, stepsDone, stepsTotal, waiting};
}

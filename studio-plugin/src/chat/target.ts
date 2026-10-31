// openChat 대상 → 대화 창이 보일 화면.
import type {RunSummary} from '../daemon/api.ts';
import type {DaemonClient} from '../daemon/client.ts';
import {emptyForm, type ComposerForm} from './composer.ts';
import type {ChatTarget} from './store.ts';

export type View = {kind: 'compose'; form: ComposerForm} | {kind: 'run'; runId: string};

/** 한 작업(workId)의 실행 중 가장 최근 것. 없으면 undefined. */
export function latestRunOfWork(runs: readonly RunSummary[], workId: string): RunSummary | undefined {
  return [...runs].filter(r => r.workId === workId).sort((a, b) => (a.startedAt < b.startedAt ? 1 : -1))[0];
}

/** openChat 대상 → 화면. work는 실행 목록에서 찾아야 해서 비동기다. */
export async function viewForTarget(client: Pick<DaemonClient, 'api'>, target: ChatTarget): Promise<View> {
  if (target.kind === 'run') return {kind: 'run', runId: target.runId};
  if (target.kind === 'compose') return {kind: 'compose', form: emptyForm({request: target.text, cwd: target.cwd ?? ''})};
  try {
    const {runs} = await client.api.listRuns({limit: 200});
    const found = latestRunOfWork(runs, target.workId);
    if (found) return {kind: 'run', runId: found.runId};
  } catch {
    // 목록을 못 읽으면 이어가기 입력으로 간다(workId만 있으면 이어갈 수 있다)
  }
  return {kind: 'compose', form: emptyForm({cwd: target.cwd ?? '', resumeWorkId: target.workId})};
}

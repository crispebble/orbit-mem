// 새 작업 입력: 폼 값 → startRun 입력. 순수 함수만 있다.
import type {RunMode, RunSummary, StartRunInput} from '../daemon/api.ts';

export interface ComposerForm {
  request: string;
  mode: RunMode;
  /** 빈 글: 폴더로 자동 */
  profile: string;
  cwd: string;
  maxCostUsd: string;
  maxTurns: string;
  maxMinutes: string;
  /** 있으면 그 작업을 이어간다 */
  resumeWorkId?: string;
}

export type ComposerIssue = 'request' | 'cwd' | 'cost' | 'turns' | 'minutes';

export const emptyForm = (over: Partial<ComposerForm> = {}): ComposerForm => ({
  request: '', mode: 'ultrawork', profile: '', cwd: '', maxCostUsd: '', maxTurns: '', maxMinutes: '', ...over,
});

export const isAbsolutePath = (p: string) => p.startsWith('/') || /^[A-Za-z]:[\\/]/.test(p);

function positive(text: string, integer: boolean): {value?: number; bad: boolean} {
  const t = text.trim();
  if (t === '') return {bad: false};
  const n = Number(t);
  if (!Number.isFinite(n) || n <= 0 || (integer && !Number.isInteger(n))) return {bad: true};
  return {value: n, bad: false};
}

export function buildStartInput(form: ComposerForm): {ok: true; input: StartRunInput} | {ok: false; issues: ComposerIssue[]} {
  const issues: ComposerIssue[] = [];
  const request = form.request.trim();
  const cwd = form.cwd.trim();
  if (!request && !form.resumeWorkId) issues.push('request');
  if (!isAbsolutePath(cwd)) issues.push('cwd');
  const cost = positive(form.maxCostUsd, false);
  const turns = positive(form.maxTurns, true);
  const minutes = positive(form.maxMinutes, false);
  if (cost.bad) issues.push('cost');
  if (turns.bad) issues.push('turns');
  if (minutes.bad) issues.push('minutes');
  if (issues.length) return {ok: false, issues};
  const limits = {maxCostUsd: cost.value, maxTurns: turns.value, maxMinutes: minutes.value};
  const input: StartRunInput = {request, cwd, mode: form.mode};
  if (form.profile) input.profile = form.profile;
  if (form.resumeWorkId) input.resume = {workId: form.resumeWorkId};
  if (limits.maxCostUsd !== undefined || limits.maxTurns !== undefined || limits.maxMinutes !== undefined) {
    input.limits = Object.fromEntries(Object.entries(limits).filter(([, v]) => v !== undefined));
  }
  return {ok: true, input};
}

/** 「이어가기」: 같은 폴더·프로필, 모드는 ultrawork(기록된 계획을 그대로 실행), 그 작업(workId)을 이어간다. */
export function resumeInput(run: Pick<RunSummary, 'cwd' | 'profile' | 'workId' | 'request'>): StartRunInput {
  const input: StartRunInput = {request: '', cwd: run.cwd, mode: 'ultrawork', resume: {workId: run.workId}};
  if (run.profile) input.profile = run.profile;
  return input;
}

/** 작업 폴더 후보: 프로젝트 범위면 그 디렉터리들, 전체 범위면 없음(직접 입력). */
export function folderCandidates(scope: {kind: 'all'} | {kind: 'project'; directories: string[]}): string[] {
  return scope.kind === 'project' ? [...scope.directories] : [];
}

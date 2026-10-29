// 단계 상세의 증거·실행 표시와 사용자 확인 기다림 때 이어 갈 세션 찾기(순수 함수).
import type {FlowGraphTone} from '@onidot-labs/studio-plugin/ai';
import {hostLabel} from './graph.ts';
import type {Evidence, Run, Session, Work} from './types.ts';

const KIND: Record<string, string> = {command: '명령', commit: '커밋', verdict: '판정'};
export const evidenceKindLabel = (kind: string) => KIND[kind] ?? '알 수 없음';

const RESULT: Record<string, {label: string; tone: FlowGraphTone}> = {
  ok: {label: '성공', tone: 'success'},
  fail: {label: '실패', tone: 'danger'},
  pass: {label: '통과', tone: 'success'},
  needs_fix: {label: '수정 요구', tone: 'danger'},
  needs_user: {label: '사용자 확인 필요', tone: 'warning'},
};
export const evidenceResultView = (result: string) => RESULT[result] ?? {label: '알 수 없음', tone: 'neutral' as const};

/** 그 단계에 묶인 증거(시각 순) */
export function evidenceOfStep(work: Pick<Work, 'evidence'>, stepId: string): Evidence[] {
  return work.evidence.filter(e => e.stepId === stepId).sort((a, b) => a.at.localeCompare(b.at) || a.id.localeCompare(b.id));
}

export interface CommitRef {
  id: string;
  repo: string;
  sha: string;
  summary?: string;
  files: string[];
}

/** 커밋 산출물(→ 코드 앱 diff) */
export function commitsOf(evidence: readonly Evidence[]): CommitRef[] {
  return evidence.flatMap(e =>
    e.kind === 'commit' && e.ref?.repo && e.ref.sha ? [{id: e.id, repo: e.ref.repo, sha: e.ref.sha, summary: e.summary, files: e.files ?? []}] : [],
  );
}

export function runLabelOf(run: Run): string {
  if (run.kind === 'subagent') return run.role || '서브에이전트 · 종류 미확인';
  if (run.kind === 'session') return `별도 세션 · ${hostLabel(run.host)}`;
  return '알 수 없음';
}

export interface WaitingSession {
  host: string;
  surface: string;
  sessionId: string;
  cwd?: string;
}

/**
 * 사용자 확인을 기다리는 단계를 이어 갈 세션. 별도 세션 실행이 그 단계를 맡았으면 그 세션(가장 늦게 시작한 것),
 * 아니면 작업을 연 세션. 서브에이전트의 확인 요청은 메인 세션 대화에 뜨므로 메인 세션으로 간다.
 */
export function waitingSessionOf(work: Pick<Work, 'runs' | 'origin' | 'cwd'>, stepId: string, sessions: readonly Session[]): WaitingSession | undefined {
  const own = work.runs
    .filter(r => r.stepId === stepId && r.kind === 'session' && r.sessionId)
    .sort((a, b) => b.startedAt.localeCompare(a.startedAt))[0];
  if (own) {
    const s = sessions.find(x => x.sessionId === own.sessionId);
    return {host: own.host, surface: s?.surface ?? 'unknown', sessionId: own.sessionId!, cwd: own.cwd ?? s?.cwd};
  }
  if (!work.origin) return undefined;
  // 이어 가기는 세션을 만든 디렉토리에서 해야 대화를 찾는다: 세션 칸의 cwd가 먼저다
  const s = sessions.find(x => x.sessionId === work.origin!.sessionId);
  return {host: work.origin.host, surface: work.origin.surface, sessionId: work.origin.sessionId, cwd: s?.cwd ?? work.cwd};
}

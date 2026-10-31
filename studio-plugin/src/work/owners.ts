// 「담당별」 보조 보기: 단계를 담당(assignee)으로 묶어 forma-ai `AgentOrchestrator`의 workers로 바꾼다.
// 기본 보기는 선행 관계를 그리는 그래프이고, 이 보기는 누가 무엇을 맡았는지만 보인다(설계 S4-2).
import type {AgentWorker} from '@onidot-labs/orbit-studio-plugin/ai';
import {pick} from '../i18n/index.ts';
import {workText} from '../i18n/messages/work.ts';
import {blockText} from './display.ts';
import {hostLabel} from './graph.ts';
import type {Step} from './types.ts';

type RunStatus = AgentWorker['status'];

const STEP_RUN: Record<string, RunStatus> = {
  pending: 'queued', ready: 'queued', in_progress: 'running', done: 'succeeded', cancelled: 'cancelled',
};

/** 단계 상태 → forma-ai RunStatus. 막힘은 사유로 가른다. 모르는 값은 idle */
export function stepRunStatus(step: Pick<Step, 'status' | 'block'>): RunStatus {
  if (step.status === 'blocked') {
    if (step.block?.reason === 'waiting_user') return 'waiting';
    if (step.block?.reason === 'run_failed') return 'failed';
    return 'paused';
  }
  return STEP_RUN[step.status] ?? 'idle';
}

/** 묶음 상태: 진행 중 > 확인 기다림 > 실패 > 막힘 > 대기 > 다 끝남 */
function groupStatus(statuses: RunStatus[]): RunStatus {
  for (const s of ['running', 'waiting', 'failed', 'paused', 'queued', 'idle'] as const) if (statuses.includes(s)) return s;
  return statuses.every(s => s === 'cancelled') ? 'cancelled' : 'succeeded';
}

/** 담당 묶음 이름. 역할은 역할 칩(role)으로, 호스트는 호스트 칩(host)으로 따로 보이므로 이름에 넣지 않는다. */
function ownerName(a: NonNullable<Step['assignee']>): string {
  const m = pick(workText);
  if (a.kind === 'main') return m.who.mainSession;
  if (a.kind === 'subagent') return a.role ? m.who.subagent : m.who.subagentUnknown;
  if (a.kind === 'session') return m.who.separateSessionPlain;
  return m.unknown;
}

const keyOf = (a: Step['assignee']) => (a ? `${a.kind}|${a.role ?? ''}|${a.host ?? ''}` : '');

export function ownerWorkers(steps: readonly Step[]): AgentWorker[] {
  const groups = new Map<string, Step[]>();
  for (const s of [...steps].sort((a, b) => a.order - b.order)) {
    const k = keyOf(s.assignee);
    groups.set(k, [...(groups.get(k) ?? []), s]);
  }
  return [...groups.entries()].map(([key, list]) => {
    const a = list[0].assignee;
    const counted = list.filter(s => s.status !== 'cancelled');
    const done = counted.filter(s => s.status === 'done').length;
    const waiting = list.find(s => s.status === 'blocked' && s.block?.reason === 'waiting_user');
    const models = [...new Set(list.map(s => s.assignee?.model).filter((m): m is string => !!m))];
    return {
      id: key || 'unassigned',
      name: a ? ownerName(a) : pick(workText).who.unassigned,
      status: groupStatus(list.map(stepRunStatus)),
      role: a?.kind === 'subagent' ? a.role : undefined,
      host: a?.host ? hostLabel(a.host) : undefined,
      model: models.length === 1 ? models[0] : undefined,
      progress: counted.length ? Math.round((done / counted.length) * 100) : 0,
      subtasks: list.map(s => ({id: s.id, label: s.title, status: stepRunStatus(s)})),
      waiting: waiting?.block ? {message: blockText(waiting.block)} : undefined,
    };
  });
}

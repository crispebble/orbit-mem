// 세션 보기의 표시 모델(순수 함수): 세션 줄(Forma `SessionRow` props), 호출 계보(forma-ui `Tree`에 넣을 나무),
// 사건 기록(forma-ai `AgentTimeline` events), 사용자 확인 기다림 때 세션으로 가는 길.
import type {AgentTimelineEvent, SessionRowProps} from '@onidot-labs/orbit-studio-plugin/ai';
import {localeTag, pick} from '../i18n/index.ts';
import {workKitText, workText} from '../i18n/messages/work.ts';
import {runStateView, sessionStateView} from './display.ts';
import {agoText, hostLabel, spanText} from './graph.ts';
import type {Run, Session, SessionEvent} from './types.ts';

const SURFACE_KEYS = ['cli', 'desktop', 'headless'];

/** 화면 종류 글. 모르는 값·`unknown`은 「알 수 없음」 */
export function surfaceLabel(surface: string | undefined): string {
  const m = pick(workText);
  if (surface === 'cli') return 'CLI';
  if (surface && SURFACE_KEYS.includes(surface)) return (m.surface as Record<string, string>)[surface];
  return m.unknown;
}

/** 호스트가 준 끝 이유(Claude Code SessionEnd reason). 모르는 글은 원문 그대로 */
export function endText(s: Pick<Session, 'state' | 'endReason' | 'endDetail'>): string | undefined {
  if (s.state !== 'ended') return undefined;
  const m = pick(workText);
  const head = s.endReason === 'session_end' ? m.endReason.session_end : m.unknown;
  if (!s.endDetail) return head;
  const details = ['prompt_input_exit', 'clear', 'logout', 'other'];
  return `${head} · ${details.includes(s.endDetail) ? (m.endReason as Record<string, string>)[s.endDetail] : s.endDetail}`;
}

const tail = (path: string) => path.split('/').filter(Boolean).pop() ?? path;

export function placeOf(s: Pick<Session, 'repo' | 'branch' | 'cwd' | 'sessionId'>): SessionRowProps['place'] {
  if (s.repo) return {kind: 'repo', name: tail(s.repo), branch: s.branch};
  return {kind: 'folder', name: s.cwd ? tail(s.cwd) : s.sessionId.slice(0, 8)};
}

function elapsedOf(s: Pick<Session, 'startedAt' | 'endedAt'>, now: number): string | undefined {
  const start = Date.parse(s.startedAt);
  if (Number.isNaN(start)) return undefined;
  const end = s.endedAt ? Date.parse(s.endedAt) : now;
  return spanText((Number.isNaN(end) ? now : end) - start);
}

/** 세션 한 줄. 레포·브랜치·끝 사유는 세션 칸에서 읽는다(실행 칸이 아니다). 선택·누르기는 화면이 붙인다. */
export function sessionRowProps(s: Session, stepTitles: ReadonlyMap<string, string>, now: number): Omit<SessionRowProps, 'onSelect' | 'selected'> {
  const view = sessionStateView(s.state);
  const surface = s.surface && SURFACE_KEYS.includes(s.surface) ? surfaceLabel(s.surface) : undefined;
  const m = pick(workText).sessionRow;
  const ids = s.activeStepIds;
  const first = ids.map(id => stepTitles.get(id)).find((t): t is string => !!t);
  const step = ids.length === 0 ? undefined : first ? (ids.length > 1 ? m.moreSteps(first, ids.length - 1) : first) : m.stepCount(ids.length);
  // 관측 오래됨 판정은 OpenOrbit이 한다(세션 state `stale`). 플러그인이 따로 기준 시간을 두지 않는다.
  const seen = s.state === 'stale' ? agoText(s.lastSeenAt, now) : undefined;
  return {
    host: surface ? `${hostLabel(s.host)} · ${surface}` : hostLabel(s.host),
    place: placeOf(s),
    step,
    elapsed: elapsedOf(s, now),
    status: {label: view.label, tone: view.tone},
    lastSeen: seen ? m.lastSeen(seen) : undefined,
    endReason: endText(s),
    muted: s.state === 'ended',
    text: pick(workKitText).sessionRow,
  };
}

export interface LineageItem {
  id: string;
  label: string;
  detail: string;
  /** 끝난 실행(흐리게) */
  ended: boolean;
  /** 뒤에서 도는 실행 */
  background: boolean;
  /** 이 실행이 별도 세션이면 그 세션 id(누르면 세션 탭) */
  sessionId?: string;
  children: LineageItem[];
}

const ENDED = new Set(['finished', 'failed', 'lost']);

function runLabel(run: Run): string {
  const m = pick(workText);
  if (run.kind === 'subagent') return run.role || m.who.subagentUnknown;
  if (run.kind === 'session') return m.who.separateSession(hostLabel(run.host));
  return m.unknown;
}

/** 호출 계보: 메인 세션 → `parentSessionId`로 이은 실행. 별도 세션 실행은 그 세션 아래의 실행을 이어 그린다. */
export function lineageOf(root: Pick<Session, 'sessionId' | 'host' | 'surface' | 'state'>, runs: readonly Run[], now: number): LineageItem {
  const seen = new Set<string>([root.sessionId]);
  const childrenOf = (sessionId: string): LineageItem[] =>
    runs
      .filter(r => r.parentSessionId === sessionId)
      .sort((a, b) => a.startedAt.localeCompare(b.startedAt) || a.id.localeCompare(b.id))
      .map(r => {
        const nested = r.kind === 'session' && r.sessionId && !seen.has(r.sessionId);
        if (nested) seen.add(r.sessionId!);
        return {
          id: r.id,
          label: runLabel(r),
          detail: runStateView(r, now).label,
          ended: ENDED.has(r.state),
          background: r.background === true,
          sessionId: r.kind === 'session' ? r.sessionId : undefined,
          children: nested ? childrenOf(r.sessionId!) : [],
        };
      });
  return {
    id: root.sessionId,
    label: pick(workText).who.mainSessionBy(hostLabel(root.host)),
    detail: sessionStateView(root.state).label,
    ended: root.state === 'ended',
    background: false,
    children: childrenOf(root.sessionId),
  };
}

const clock = (timeZone?: string) => new Intl.DateTimeFormat(localeTag(), {hour: '2-digit', minute: '2-digit', hour12: false, timeZone});

/** 세션 사건 → AgentTimeline events. 시각은 PC 시간대(timeZone은 시험용). 단계 제목을 알면 덧붙인다. */
export function timelineOf(events: readonly SessionEvent[], stepTitles: ReadonlyMap<string, string>, timeZone?: string): AgentTimelineEvent[] {
  const fmt = clock(timeZone);
  const ev = pick(workText).events as Record<string, unknown>;
  const name = (kind: string): string => (typeof ev[kind] === 'string' && kind.includes('.') ? (ev[kind] as string) : pick(workText).unknown);
  return events.map(e => {
    const t = Date.parse(e.at);
    const parts = Number.isNaN(t) ? [] : fmt.formatToParts(new Date(t));
    const hour = parts.find(p => p.type === 'hour')?.value;
    const minute = parts.find(p => p.type === 'minute')?.value;
    const step = e.stepId ? stepTitles.get(e.stepId) : undefined;
    const detail = [e.summary, step && !e.summary?.includes(step) ? pick(workText).events.stepPrefix(step) : undefined].filter(Boolean).join(' · ');
    return {
      id: e.seq,
      time: hour && minute ? `${hour === '24' ? '00' : hour}:${minute}` : undefined,
      dateTime: e.at,
      agent: e.runId ? pick(workText).events.agentRun : pick(workText).who.mainSession,
      title: name(e.kind),
      detail: detail || undefined,
    };
  });
}

export interface ResumeGuide {
  host: string;
  surface: string;
  sessionId: string;
  /** 터미널에서 이어 가는 명령. 이어 갈 수 없는 화면 종류·호스트면 없다(세션 id 복사만). */
  command?: string;
}

const RESUME: Partial<Record<string, (id: string) => string>> = {
  claude: id => `claude --resume ${id}`,
  codex: id => `codex resume ${id}`,
};

/**
 * 사용자 확인 기다림 때 그 세션으로 가는 길. 계약 1.0은 읽기 전용이라 Studio는 확인을 반영하지 않는다(설계 S4-6).
 * 터미널 이어 가기는 CLI 세션만 준다: 데스크톱 앱·백그라운드 세션은 같은 대화를 터미널에서 이어 가면 갈라질 수 있다.
 * needCwd면 디렉토리를 모를 때 명령을 주지 않는다(다른 디렉토리에서는 이어 갈 대화를 찾지 못한다).
 */
export function resumeGuide(o: {host: string; surface: string; sessionId: string; cwd?: string}, opts: {needCwd?: boolean} = {}): ResumeGuide {
  const make = RESUME[o.host];
  // 첫 글자는 영숫자: '-'로 시작하면 명령의 옵션으로 읽힌다
  const safe = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(o.sessionId);
  const where = !opts.needCwd || !!o.cwd;
  return {
    host: hostLabel(o.host),
    surface: surfaceLabel(o.surface),
    sessionId: o.sessionId,
    command: o.surface === 'cli' && make && safe && where ? make(o.sessionId) : undefined,
  };
}

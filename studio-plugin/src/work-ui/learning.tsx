// 작업 탭 「Learning / 능숙해짐」(AU-7): 이 작업의 지표와 같은 저장소·종류·크기의 끝난 작업들, 앞 절반 대 뒤 절반의 추이.
// orbit.work 1.5 work_metrics를 그때그때 읽는다. 옛 orb(도구 없음)나 읽기 실패면 구역을 그리지 않는다.
// Forma Disclosure·StatRow·Badge로 조립하고 새 시각 언어는 만들지 않는다.
import {useEffect, useState} from 'react';
import {Badge, Disclosure, StatRow} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {workUiText} from '../i18n/messages/work.ts';
import type {MetricsResult, TrendKey, TrendSide, WorkMetrics} from '../work/types.ts';
import {useWork} from './runtime.ts';

const usd = (x: number) => `$${x.toFixed(2)}`;
const num = (x: number) => (Number.isInteger(x) ? String(x) : x.toFixed(1));
const pct = (x: number | null) => (x === null ? '-' : `${Math.round(x * 100)}%`);
const ORDER: TrendKey[] = ['asked', 'overturned', 'corrections', 'repeats', 'replans', 'firstPass', 'cost', 'turns', 'minutes'];

/** 저장소의 짧은 이름: 프로젝트 범위나 폴더의 마지막 조각 */
export function repoName(project: string): string {
  const tail = project.replace(/^[a-z]+:/, '').replace(/\/+$/, '');
  return tail.split('/').filter(Boolean).at(-1) ?? project;
}

function trendValue(k: TrendKey, s: TrendSide): string {
  switch (k) {
    case 'firstPass': return pct(s.firstPassRate);
    case 'cost': return usd(s.costUsd);
    case 'corrections': return s.corrections === null ? '-' : num(s.corrections);
    default: return num(s[k]);
  }
}

function firstPass(m: WorkMetrics): string {
  const {passed, steps} = m.firstPass;
  return steps > 0 ? `${passed}/${steps} (${Math.round((passed / steps) * 100)}%)` : '-';
}

export function LearningSection({result}: {result: MetricsResult}) {
  const t = useMessages(workUiText).learning;
  const {work, similar} = result;
  const m = work.metrics;
  const corrections = m.corrections === null ? '-' : String(m.corrections);
  const {trend} = similar;
  const changed = (list: TrendKey[]) =>
    ORDER.filter(k => list.includes(k)).map(k => `${t.key[k]} ${trendValue(k, trend.earlier)} → ${trendValue(k, trend.later)}`);
  const better = changed(trend.better);
  const worse = changed(trend.worse);
  return (
    <Disclosure title={t.title} meta={t.meta(similar.works.length)} size="sm" defaultOpen={false}>
      <div className="orbit-work-brief-body orbit-work-learning">
        <StatRow label={t.title} items={[
          {id: 'questions', label: t.questions, value: t.questionsValue(m.asked, m.assumed)},
          {id: 'overturned', label: t.overturned, value: String(m.overturned)},
          {id: 'corrections', label: t.corrections, value: corrections},
          {id: 'repeats', label: t.repeats, value: String(m.repeats)},
          {id: 'replans', label: t.replans, value: String(m.replans)},
          {id: 'firstPass', label: t.firstPass, value: firstPass(m)},
          {id: 'cost', label: t.cost, value: t.costValue(usd(m.costUsd), m.turns, num(m.minutes))},
        ]} />
        <section className="orbit-work-brief-part">
          <h4 className="orbit-work-sec">{t.similar(repoName(work.project), t.kind[work.kind as keyof typeof t.kind] ?? work.kind, t.size[work.size as keyof typeof t.size] ?? work.size)}</h4>
          <ul className="orbit-work-brief-list">
            {similar.works.map(d => {
              const x = d.metrics;
              return (
                <li key={d.workId} className="orbit-row orbit-work-learning-row" data-current={d.workId === work.workId || undefined}>
                  <span className="orbit-work-text">{d.title || d.workId}</span>
                  {d.workId === work.workId && <Badge tone="accent">{t.thisWork}</Badge>}
                  <span className="orbit-meta">{t.row(x.asked, x.corrections === null ? '-' : String(x.corrections), x.repeats, firstPass(x), usd(x.costUsd))}</span>
                </li>
              );
            })}
          </ul>
          {similar.works.length < 2 ? (
            <p className="orbit-meta">{t.needMore}</p>
          ) : (
            <div className="orbit-row orbit-work-learning-trend">
              <span className="orbit-meta">{t.trend}</span>
              {better.length > 0 && <Badge tone="success">{t.better(better.join(' · '))}</Badge>}
              {worse.length > 0 && <Badge tone="warning">{t.worse(worse.join(' · '))}</Badge>}
              {better.length === 0 && worse.length === 0 && <span className="orbit-meta">{t.noChange}</span>}
            </div>
          )}
        </section>
      </div>
    </Disclosure>
  );
}

/** 작업 탭이 그린다: 지표를 읽어 구역을 보이고, 작업이 바뀌면 다시 읽는다 */
export function WorkLearning({workId, updatedAt}: {workId: string; updatedAt: string}) {
  const {store} = useWork();
  const [result, setResult] = useState<MetricsResult>();
  useEffect(() => {
    if (!store) return;
    let alive = true;
    store.loadMetrics(workId).then(
      r => { if (alive) setResult(r); },
      () => { if (alive) setResult(undefined); },
    );
    return () => { alive = false; };
  }, [store, workId, updatedAt]);
  return result ? <LearningSection result={result} /> : null;
}

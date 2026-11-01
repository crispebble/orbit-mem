// 작업 머리 아래의 「조사」(AU-2)와 「결정·가정」(AU-3). 오래된 작업(1.3 이전)에는 두 칸이 없어 아무것도 그리지 않는다.
// Forma Disclosure·Badge·PropertyList·TextLink로 조립하고 새 시각 언어는 만들지 않는다.
import type {ReactNode} from 'react';
import {Badge, Disclosure, PropertyList, TextLink} from '@onidot-labs/orbit-studio-plugin/ui';
import {useMessages} from '../i18n/index.ts';
import {workUiText} from '../i18n/messages/work.ts';
import {assumptionStateView, confidenceLabel, isLongDiscovery, leansOn, rungView, sizeLabel} from '../work/assumptions.ts';
import {formatTime} from '../work/display.ts';
import type {Assumption, Discovery, Work} from '../work/types.ts';

function List({items, mono}: {items: readonly string[]; mono?: boolean}) {
  return (
    <ul className="orbit-work-brief-list">
      {items.map((x, i) => <li key={`${i}:${x}`} className={mono ? 'orbit-mono orbit-work-break' : 'orbit-work-text'}>{x}</li>)}
    </ul>
  );
}

function Part({title, children}: {title: string; children: ReactNode}) {
  return (
    <section className="orbit-work-brief-part">
      <h4 className="orbit-work-sec">{title}</h4>
      {children}
    </section>
  );
}

export function DiscoverySection({discovery}: {discovery: Discovery}) {
  const t = useMessages(workUiText).brief;
  const meta = t.meta(sizeLabel(discovery.size), discovery.rounds, discovery.by);
  return (
    <Disclosure title={t.discovery} meta={meta || undefined} size="sm" defaultOpen={!isLongDiscovery(discovery)}>
      <div className="orbit-work-brief-body">
        {discovery.problem && <Part title={t.problem}><p className="orbit-work-text">{discovery.problem}</p></Part>}
        {discovery.decisions.length > 0 && (
          <Part title={t.points}>
            <ul className="orbit-work-brief-list">
              {discovery.decisions.map((d, i) => (
                <li key={`${i}:${d.point}`} className="orbit-work-brief-point">
                  <span className="orbit-row">
                    <span className="orbit-work-text">{d.point}</span>
                    {d.settled && <Badge tone="success">{t.settled}: {d.settled}</Badge>}
                  </span>
                  {d.evidence.length > 0
                    ? <List items={d.evidence} mono />
                    : <span className="orbit-meta">{t.noEvidence}</span>}
                </li>
              ))}
            </ul>
          </Part>
        )}
        {discovery.idealEnd && <Part title={t.idealEnd}><p className="orbit-work-text">{discovery.idealEnd}</p></Part>}
        {discovery.unknowns.length > 0 && <Part title={t.unknowns}><List items={discovery.unknowns} /></Part>}
        {discovery.sources.length > 0 && <Part title={t.sources}><List items={discovery.sources} mono /></Part>}
      </div>
    </Disclosure>
  );
}

function AssumptionItem({assumption: a, work, onSelectStep}: {assumption: Assumption; work: Work; onSelectStep(stepId: string): void}) {
  const t = useMessages(workUiText).brief;
  const rung = rungView(a.rung);
  const state = assumptionStateView(a.state);
  const steps = work.steps.filter(s => leansOn(a, s));
  const items: {label: ReactNode; value: ReactNode}[] = [];
  if (a.chosen) items.push({label: t.chosen, value: <span className="orbit-work-text">{a.chosen}</span>});
  if (a.basis) items.push({label: t.basis, value: <span className="orbit-work-text">{a.basis}</span>});
  items.push({label: t.confidence, value: confidenceLabel(a.confidence)});
  if (a.answer) {
    const when = a.answeredAt !== undefined ? formatTime(new Date(a.answeredAt).toISOString()) : '';
    items.push({label: t.answer, value: (
      <span className="orbit-work-text">
        {a.answer}
        {(a.answeredBy || when) && <span className="orbit-meta"> · {t.answeredBy(a.answeredBy ?? '', when)}</span>}
      </span>
    )});
  }
  if (steps.length > 0) {
    items.push({label: t.leaning, value: (
      <span className="orbit-work-links">
        {steps.map(s => <TextLink key={s.id} onClick={() => onSelectStep(s.id)}>{s.title}</TextLink>)}
      </span>
    )});
  } else if (a.steps.length > 0) {
    items.push({label: t.leaning, value: <span className="orbit-meta">{t.stepNotListed}</span>});
  }
  return (
    <li className="orbit-work-brief-item" data-rung={a.rung} data-state={a.state}>
      <div className="orbit-row">
        <b className="orbit-work-text">{a.point}</b>
        <Badge tone={rung.tone}>{rung.label}</Badge>
        <Badge tone={state.tone}>{state.label}</Badge>
      </div>
      <PropertyList items={items} />
    </li>
  );
}

export function DecisionsSection({work, onSelectStep}: {work: Work; onSelectStep(stepId: string): void}) {
  const t = useMessages(workUiText).brief;
  const list = work.assumptions ?? [];
  // 열려 있거나 뒤집힌 것이 있으면 펼쳐 둔다. 모두 정리됐으면 접어 둔다.
  const unresolved = list.some(a => a.state === 'open' || a.state === 'overturned');
  return (
    <Disclosure title={t.decisions} meta={t.decisionsCount(list.length)} size="sm" defaultOpen={unresolved}>
      <ul className="orbit-work-brief-items">
        {list.map(a => <AssumptionItem key={a.id} assumption={a} work={work} onSelectStep={onSelectStep} />)}
      </ul>
    </Disclosure>
  );
}

/** 조사와 결정·가정. 둘 다 없으면 null */
export function WorkBrief({work, onSelectStep}: {work: Work; onSelectStep(stepId: string): void}) {
  const hasAssumptions = !!work.assumptions?.length;
  if (!work.discovery && !hasAssumptions) return null;
  return (
    <div className="orbit-work-brief">
      {work.discovery && <DiscoverySection discovery={work.discovery} />}
      {hasAssumptions && <DecisionsSection work={work} onSelectStep={onSelectStep} />}
    </div>
  );
}

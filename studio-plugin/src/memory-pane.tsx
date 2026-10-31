// memory 탭: 기억 보기, 고치기, 상태 바꾸기
import {useCallback, useEffect, useMemo, useState} from 'react';
import type {TabProps} from '@onidot-labs/orbit-studio-plugin';
import {Button, StateView} from '@onidot-labs/orbit-studio-plugin/ui';
import {pick, useLocale, useMessages} from './i18n/index.ts';
import {commonText} from './i18n/messages/common.ts';
import {memoryText} from './i18n/messages/memory.ts';
import {openMemoryGraph} from './memory-graph/open.ts';
import {globalScope, MemoryDetail, MemoryForm, type MemoryFormValue, type MemoryKind, type MemoryLinkView, type OrbitMemory} from './parts/index.ts';
import {errorMessage, OrbitError} from './orb.ts';
import {toMemoryDetail, toMemoryRecord, type OrbMemory, type OrbVersion} from './model.ts';
import {memoryUpdateSteps, statusArgs, type UpdateArgs} from './requests.ts';
import {useOrbit} from './runtime.ts';

interface Loaded {
  memory: OrbMemory;
  history: OrbVersion[];
  related: OrbitMemory[];
  links: MemoryLinkView[];
}

export function MemoryPane({ctx, tab}: TabProps) {
  const {store} = useOrbit();
  const t = useMessages(memoryText);
  const common = useMessages(commonText);
  const locale = useLocale();
  const CONFLICT_HINT = t.pane.conflict;
  const id = typeof tab.params.id === 'string' ? tab.params.id : tab.id;
  const [loaded, setLoaded] = useState<Loaded>();
  const [error, setError] = useState<string>();
  const [mode, setMode] = useState<'view' | 'edit'>('view');
  const [candidates, setCandidates] = useState<OrbitMemory[]>([]);
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string>();

  const load = useCallback(async () => {
    if (!store) return;
    setError(undefined);
    try {
      const {memory, history} = await store.getMemory(id);
      const related: OrbitMemory[] = [];
      if (memory.supersededBy) {
        try {
          related.push(toMemoryRecord({...(await store.getMemory(memory.supersededBy)).memory, summary: '', tags: null}));
        } catch {
          // 대체한 기억을 못 읽어도 id로 보여 준다
        }
      }
      const linked = memory.links ?? [];
      const links: MemoryLinkView[] = linked.map(l => ({id: l.id, memoryId: l.memoryId, note: l.note, origin: l.origin}));
      setLoaded({memory, history, related, links});
      setConflict(false);
      ctx.tabs.setTitle('memory', tab.id, memory.title);
      // 관련 기억(명시 연결)의 제목은 본문을 보인 뒤 채운다. 못 읽은 것은 id로 남는다
      if (linked.length) {
        void store.getMemories(linked.map(l => l.memoryId)).then(others => {
          const byId = new Map(others.map(o => [o.id, o] as const));
          setLoaded(prev => (prev && prev.memory.id === memory.id && prev.memory.version === memory.version
            ? {...prev, links: prev.links.map(l => ({...l, title: byId.get(l.memoryId)?.title, kind: byId.get(l.memoryId)?.kind as MemoryKind | undefined}))}
            : prev));
        }, (e: unknown) => ctx.log.warn(`Could not read related memory titles (${id}): ${errorMessage(e)}`));
      }
    } catch (e) {
      ctx.log.error(`Could not read memory (${id}): ${errorMessage(e)}`);
      setError(errorMessage(e));
    }
  }, [store, ctx, id, tab.id]);

  useEffect(() => { void load(); }, [load]);

  // 고치기를 벗어나거나 탭이 닫히면 저장 안 한 표시를 지운다.
  useEffect(() => {
    if (mode !== 'edit') return;
    return () => ctx.tabs.setModified('memory', tab.id, false);
  }, [mode, ctx, tab.id]);

  // 고치기에 들어갈 때 대체 후보로 쓸 기억 목록을 읽는다
  useEffect(() => {
    if (mode !== 'edit' || !store) return;
    let live = true;
    store.findMemories({query: '', kind: 'all', state: 'active'}).then(
      items => { if (live) setCandidates(items); },
      (e: unknown) => ctx.log.warn(`Could not read replacement candidates: ${errorMessage(e)}`),
    );
    return () => { live = false; };
  }, [mode, store, ctx]);

  // 날짜·범위 이름이 언어를 따르므로 그릴 때 만든다
  const record = useMemo(() => (loaded ? toMemoryDetail(loaded.memory, loaded.history) : undefined), [loaded, locale]);

  if (!store) return <StateView variant="loading" title={common.startingOrbit} />;
  if (error && !loaded) {
    return <StateView variant="error" title={t.pane.readFailed} description={error} onRetry={() => void load()} retryLabel={common.retry} />;
  }
  if (!loaded || !record) return <StateView variant="loading" title={t.pane.loading} />;

  const {memory} = loaded;

  async function save(value: MemoryFormValue) {
    const steps = memoryUpdateSteps(memory, value);
    if (steps.length === 0) { setMode('view'); return; }
    try {
      await store!.updateMemory(steps);
    } catch (e) {
      ctx.log.error(`Memory edit failed (${id}): ${errorMessage(e)}`);
      if (e instanceof OrbitError && e.conflict) setConflict(true);
      throw e;
    }
    await load();
    void store!.refresh();
    setMode('view');
    ctx.notify({title: pick(memoryText).pane.saved, body: value.title, tone: 'success'});
  }

  async function changeState(args: UpdateArgs, done: string) {
    setBusy(true);
    setNotice(undefined);
    try {
      await store!.updateMemory([args]);
      await load();
      void store!.refresh();
      setNotice(done);
    } catch (e) {
      ctx.log.error(`Memory state change failed (${id}): ${errorMessage(e)}`);
      setError(e instanceof OrbitError && e.conflict ? CONFLICT_HINT : errorMessage(e));
      if (e instanceof OrbitError && e.conflict) setConflict(true);
    } finally {
      setBusy(false);
    }
  }

  if (mode === 'edit') {
    return (
      <div className="orbit-pane">
        {conflict && (
          <div className="orbit-row">
            <p className="orbit-alert" role="alert">{CONFLICT_HINT}</p>
            <Button variant="secondary" size="sm" onClick={() => void load()}>{t.pane.reload}</Button>
          </div>
        )}
        <MemoryForm
          key={memory.version}
          memory={record}
          candidates={candidates}
          scopes={[record.scope ?? globalScope()]}
          onSave={save}
          onDirtyChange={dirty => ctx.tabs.setModified('memory', tab.id, dirty)}
          onCancel={() => { setMode('view'); setConflict(false); }}
        />
      </div>
    );
  }

  const state = memory.state;
  return (
    <div className="orbit-pane">
      <div className="orbit-row" role="group" aria-label={t.pane.changeState}>
        {state === 'candidate' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'confirm'), t.pane.confirmed)}>{t.pane.confirm}</Button>}
        {state !== 'stale' && state !== 'hidden' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'stale'), t.pane.markedStale)}>{t.pane.markStale}</Button>}
        {(state === 'stale' || state === 'hidden') && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'restore'), t.pane.restored)}>{t.pane.restore}</Button>}
        <Button variant="ghost" size="sm" onClick={() => openMemoryGraph(ctx, memory.id)}>{t.pane.showInGraph}</Button>
      </div>
      {error && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">{error}</p>
          <Button variant="secondary" size="sm" onClick={() => void load()}>{t.pane.reload}</Button>
        </div>
      )}
      {notice && <p className="orbit-alert" role="status">{notice}</p>}
      <MemoryDetail
        memory={record}
        related={loaded.related}
        links={loaded.links}
        onOpenMemory={mid => {
          const title = loaded.links.find(l => l.memoryId === mid)?.title ?? mid;
          ctx.tabs.open({type: 'memory', id: mid, title, params: {id: mid}});
        }}
        onEdit={() => { setNotice(undefined); setMode('edit'); }}
        onCopyId={async mid => { await navigator.clipboard.writeText(mid); ctx.notify({title: pick(memoryText).pane.copiedId, body: mid, tone: 'info'}); }}
      />
    </div>
  );
}

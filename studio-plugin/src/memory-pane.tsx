// memory 탭: 기억 보기, 고치기, 상태 바꾸기
import {useCallback, useEffect, useState} from 'react';
import type {TabProps} from '@onidot-labs/studio-plugin';
import {Button, StateView} from '@onidot-labs/studio-plugin/ui';
import {openMemoryGraph} from './memory-graph/open.ts';
import {MemoryDetail, MemoryForm, type MemoryFormValue, type MemoryKind, type MemoryLinkView, type OrbitMemory} from './parts/index.ts';
import {errorMessage, OrbitError} from './oni.ts';
import {toMemoryDetail, toMemoryRecord, type OniMemory, type OniVersion} from './model.ts';
import {memoryUpdateSteps, statusArgs, type UpdateArgs} from './requests.ts';
import {useOrbit} from './runtime.ts';

interface Loaded {
  memory: OniMemory;
  history: OniVersion[];
  record: OrbitMemory;
  related: OrbitMemory[];
  links: MemoryLinkView[];
}

const CONFLICT_HINT = '다른 곳에서 먼저 고쳤습니다. 다시 읽고 고쳐 주세요.';

export function MemoryPane({ctx, tab}: TabProps) {
  const {store} = useOrbit();
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
      // 관련 기억(명시 연결)의 제목을 읽는다. 못 읽은 것은 id로 보인다
      const linked = memory.links ?? [];
      const others = linked.length ? await store.getMemories(linked.map(l => l.memoryId)) : [];
      const links: MemoryLinkView[] = linked.map(l => {
        const other = others.find(o => o.id === l.memoryId);
        return {id: l.id, memoryId: l.memoryId, title: other?.title, kind: other?.kind as MemoryKind | undefined, note: l.note, origin: l.origin};
      });
      setLoaded({memory, history, record: toMemoryDetail(memory, history), related, links});
      setConflict(false);
      ctx.tabs.setTitle('memory', tab.id, memory.title);
    } catch (e) {
      ctx.log.error(`기억을 읽지 못했습니다(${id}): ${errorMessage(e)}`);
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
      (e: unknown) => ctx.log.warn(`대체 후보를 읽지 못했습니다: ${errorMessage(e)}`),
    );
    return () => { live = false; };
  }, [mode, store, ctx]);

  if (!store) return <StateView variant="loading" title="Orbit을 켜는 중입니다" />;
  if (error && !loaded) {
    return <StateView variant="error" title="기억을 읽지 못했습니다" description={error} onRetry={() => void load()} />;
  }
  if (!loaded) return <StateView variant="loading" title="기억을 불러오는 중입니다" />;

  const {memory, record} = loaded;

  async function save(value: MemoryFormValue) {
    const steps = memoryUpdateSteps(memory, value);
    if (steps.length === 0) { setMode('view'); return; }
    try {
      await store!.updateMemory(steps);
    } catch (e) {
      ctx.log.error(`기억 고치기 실패(${id}): ${errorMessage(e)}`);
      if (e instanceof OrbitError && e.conflict) setConflict(true);
      throw e;
    }
    await load();
    void store!.refresh();
    setMode('view');
    ctx.notify({title: '기억을 고쳤습니다', body: value.title, tone: 'success'});
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
      ctx.log.error(`기억 상태 바꾸기 실패(${id}): ${errorMessage(e)}`);
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
            <Button variant="secondary" size="sm" onClick={() => void load()}>다시 읽기</Button>
          </div>
        )}
        <MemoryForm
          key={memory.version}
          memory={record}
          candidates={candidates}
          scopes={[record.scope ?? '전역']}
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
      <div className="orbit-row" role="group" aria-label="상태 바꾸기">
        {state === 'candidate' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'confirm'), '확정했습니다.')}>확정</Button>}
        {state !== 'stale' && state !== 'hidden' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'stale'), '낡음으로 표시했습니다.')}>낡음으로 표시</Button>}
        {(state === 'stale' || state === 'hidden') && <Button variant="secondary" size="sm" disabled={busy} onClick={() => void changeState(statusArgs(memory, 'restore'), '후보로 되돌렸습니다.')}>후보로 되돌리기</Button>}
        <Button variant="ghost" size="sm" onClick={() => openMemoryGraph(ctx, memory.id)}>그래프에서 보기</Button>
      </div>
      {error && (
        <div className="orbit-row">
          <p className="orbit-alert" role="alert">{error}</p>
          <Button variant="secondary" size="sm" onClick={() => void load()}>다시 읽기</Button>
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
        onCopyId={async mid => { await navigator.clipboard.writeText(mid); ctx.notify({title: 'id를 복사했습니다', body: mid, tone: 'info'}); }}
      />
    </div>
  );
}

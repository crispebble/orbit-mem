// 계약 응답 검사: 판 확인과 예시 읽기. 끝 기준 27(판 호환)·28(모르는 값)에 해당한다.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {checkContract, parseChanges, parseGet, parseList, parseSession, parseSessions, WorkPaused, WorkShapeError, WorkToolError} from '../src/work/contract.ts';
import {sessionGet, workChanges, workGet, workList, scopeArgs} from '../src/work/client.ts';
import {asResult, example, fakeWorkCtx} from './work-fake.ts';

const withContract = (r: Record<string, unknown>, c: Record<string, unknown>): Record<string, unknown> => ({...r, contract: {name: 'orbit.work', major: 1, minor: 0, supportedMajors: [1], ...c}});

describe('예시 응답 읽기', () => {
  it('work_list 쪽과 nextCursor를 읽는다', () => {
    const p = parseList(example('work_list.page1').result);
    assert.equal(p.items.length, 3);
    assert.ok(p.nextCursor);
    assert.equal(p.limitMax, 50);
    assert.equal(parseList(example('work_list.page2').result).nextCursor, undefined);
  });

  it('work_get 단계·실행·증거와 자식 작업을 읽는다', () => {
    const g = parseGet(example('work_get.claude-task').result);
    assert.equal(g.work.steps.length, 9);
    assert.ok(g.work.runs.length > 0);
    const c = parseGet(example('work_get.with-children').result);
    assert.equal(c.work.children?.length, 1);
  });

  it('work_sessions 세션 쪽과 한 세션의 사건 쪽을 읽는다', () => {
    assert.ok(parseSessions(example('work_sessions.list.page1').result).sessions.length > 0);
    const s = parseSession(example('work_sessions.session.page1').result);
    assert.equal(s.session.state, 'ended');
    assert.ok(s.events.length > 0);
  });

  it('work_changes 네 경우(변경·빈 응답·more·reset)를 읽는다', () => {
    const c = parseChanges(example('work_changes.changes').result);
    assert.ok(c.workIds.length + c.sessionIds.length > 0);
    assert.equal(parseChanges(example('work_changes.empty').result).workIds.length, 0);
    assert.equal(parseChanges(example('work_changes.more').result).more, true);
    const r = parseChanges(example('work_changes.reset').result);
    assert.deepEqual([r.reset, r.workIds], [true, []]);
    assert.equal(parseChanges(example('work_changes.reset-restored').result).reset, true);
  });

  it('오류 예시는 WorkToolError로 던진다(NOT_FOUND·INVALID_ARGUMENT)', () => {
    assert.throws(() => parseGet(example('error.not-found').result), (e: unknown) => e instanceof WorkToolError && e.code === 'NOT_FOUND');
    assert.throws(() => parseList(example('error.invalid-argument').result), (e: unknown) => e instanceof WorkToolError && e.code === 'INVALID_ARGUMENT');
  });

  it('모양이 틀리면 WorkShapeError', () => {
    assert.throws(() => parseList({contract: {major: 1}, seq: 1}), WorkShapeError);
    assert.throws(() => parseGet({seq: 'a', work: {id: 'x'}}), WorkShapeError);
  });
});

describe('판 확인', () => {
  it('UNSUPPORTED_MAJOR이고 supportedMajors에 1이 없으면 OpenOrbit이 새 판', () => {
    const ex = example('error.unsupported-major').result;
    const newer = withContract(ex, {major: 2, supportedMajors: [2]});
    ((newer as {error: Record<string, unknown>}).error).supportedMajors = [2];
    assert.throws(() => checkContract(newer, 'work_list'), (e: unknown) => e instanceof WorkPaused && e.kind === 'orb-newer' && /Please update the OpenOrbit app/.test(e.message));
  });

  it('major가 1보다 높고 supportedMajors에 1이 없으면 OpenOrbit이 새 판', () => {
    assert.throws(() => checkContract(withContract(example('work_list.page1').result, {major: 2, supportedMajors: [2, 3]}), 'work_list'), (e: unknown) => e instanceof WorkPaused && e.kind === 'orb-newer');
  });

  it('major가 1보다 낮으면 OpenOrbit을 올려 주세요', () => {
    assert.throws(() => checkContract(withContract(example('work_list.page1').result, {major: 0, supportedMajors: [0]}), 'work_list'), (e: unknown) => e instanceof WorkPaused && e.kind === 'orbit-older' && e.message === 'Please update OpenOrbit.');
  });

  it('minor가 높으면 그대로 읽고 모르는 칸은 무시한다', () => {
    const r = withContract(example('work_list.page1').result, {minor: 7});
    ((r as {items: Record<string, unknown>[]}).items)[0].brandNewField = {x: 1};
    assert.equal(checkContract(r, 'work_list').minor, 7);
    assert.equal(parseList(r).items.length, 3);
  });

  it('모르는 열거값(단계 상태·세션 상태)은 던지지 않고 그대로 통과한다', () => {
    const r = JSON.parse(JSON.stringify(example('work_get.claude-task').result));
    r.work.steps[0].status = 'brand_new';
    r.work.steps[0].block = undefined;
    const g = parseGet(r);
    assert.equal(g.work.steps[0].status, 'brand_new');
    const s = JSON.parse(JSON.stringify(example('work_sessions.list.page1').result));
    s.sessions[0].state = 'hibernating';
    assert.equal(parseSessions(s).sessions[0].state, 'hibernating');
  });

  it('contract가 없으면 모양 오류', () => {
    assert.throws(() => checkContract({seq: 'a'}, 'work_list'), WorkShapeError);
  });
});

describe('client 호출', () => {
  it('모든 호출에 major:1을 싣고, 프로젝트 범위는 cwdPrefix에 디렉토리들을 싣는다', async () => {
    const f = fakeWorkCtx({work_list: () => asResult(example('work_list.page2'))});
    await workList(f.ctx, {scope: {kind: 'project', id: 'p', name: 'P', directories: ['/a', '/b']}});
    assert.deepEqual(f.calls[0].args, {cwdPrefix: ['/a', '/b'], state: 'any', major: 1});
    assert.deepEqual(scopeArgs({kind: 'all'}), {});
  });

  it('work_get·work_sessions·work_changes 인자', async () => {
    const f = fakeWorkCtx({
      work_get: () => asResult(example('work_get.claude-task')),
      work_sessions: () => asResult(example('work_sessions.session.page1')),
      work_changes: () => asResult(example('work_changes.empty')),
    });
    await workGet(f.ctx, 'w1');
    await sessionGet(f.ctx, 's1', 'cur', 5);
    await workChanges(f.ctx, {after: 'x', wait: 100}, {timeoutMs: 200});
    assert.deepEqual(f.calls.map(c => c.args), [
      {workId: 'w1', includeChildren: false, major: 1},
      {sessionId: 's1', cursor: 'cur', limit: 5, major: 1},
      {after: 'x', wait: 100, major: 1},
    ]);
    assert.equal(f.calls[2].options?.timeoutMs, 200);
  });

  it('UNSUPPORTED_MAJOR 오류 응답은 WorkPaused', async () => {
    const ex = example('error.unsupported-major');
    const r = JSON.parse(JSON.stringify(ex.result));
    r.error.supportedMajors = [2];
    r.contract.supportedMajors = [2];
    r.contract.major = 2;
    const f = fakeWorkCtx({work_list: () => ({isError: true, text: 'x', structured: r})});
    await assert.rejects(workList(f.ctx, {scope: {kind: 'all'}}), (e: unknown) => e instanceof WorkPaused);
  });

  it('연결 실패는 connection 오류', async () => {
    const f = fakeWorkCtx({work_list: () => { throw new Error('끊김'); }});
    await assert.rejects(workList(f.ctx, {scope: {kind: 'all'}}), (e: unknown) => (e as {connection?: boolean}).connection === true);
  });
});

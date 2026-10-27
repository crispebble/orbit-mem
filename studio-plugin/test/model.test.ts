import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import {
  formatDate,
  scopeLabel,
  toInboxItems,
  toMemoryDetail,
  toMemoryRecord,
  toProposal,
  toProposalRow,
  type OniMemory,
  type OniProposal,
  type OniSummary,
} from '../src/model.ts';

const at = (m: number, d: number, h = 9, min = 5) => new Date(2026, m - 1, d, h, min).getTime();
const now = new Date(2026, 9, 27, 12, 0);

const summary: OniSummary = {id: 'm1', kind: 'rule', state: 'confirmed', scope: 'global', title: '경로를 명시한다', summary: '요약', tags: ['git'], version: 3, updatedAt: at(10, 26)};
const memory: OniMemory = {
  id: 'm1', kind: 'rule', scope: 'project:github.com/onidot-labs/orbit', project: 'onidot-labs/orbit', state: 'confirmed', title: '경로를 명시한다', body: '본문', tags: ['git'],
  version: 3, createdAt: at(10, 20), updatedAt: at(10, 26), supersededBy: 'm9',
  sources: [{kind: 'quote', excerpt: '경로를 적어 주세요', at: at(10, 20)}, {kind: 'file', ref: '/a/b.md'}],
};
const pending = (over: Partial<OniProposal> = {}): OniProposal => ({
  id: 'p1', kind: 'confirm', title: '확정할까요?', body: '두 번 나왔습니다', memoryIds: ['m1'], payload: {}, status: 'pending', createdAt: at(10, 27, 7, 10), ...over,
});

describe('formatDate', () => {
  it('올해는 월·일·시각, 다른 해는 연도를 붙인다', () => {
    assert.equal(formatDate(at(10, 26, 9, 5), now), '10월 26일 09:05');
    assert.equal(formatDate(new Date(2025, 11, 3, 14, 0).getTime(), now), '2025년 12월 3일');
    assert.equal(formatDate(undefined, now), '');
  });
});

describe('scopeLabel', () => {
  it('전역과 프로젝트를 사람이 읽는 이름으로 바꾼다', () => {
    assert.equal(scopeLabel('global'), '전역');
    assert.equal(scopeLabel('project:abc'), 'abc');
    assert.equal(scopeLabel('project:abc', 'onidot/orbit'), 'onidot/orbit');
    assert.equal(scopeLabel(''), '전역');
  });
});

describe('기억 변환', () => {
  it('목록 요약을 MemoryRecord로 바꾼다', () => {
    const r = toMemoryRecord(summary, now);
    assert.deepEqual([r.id, r.kind, r.state, r.scope, r.title, r.content, r.version], ['m1', 'rule', 'confirmed', '전역', '경로를 명시한다', '요약', 3]);
    assert.deepEqual(r.tags, ['git']);
    assert.equal(r.updatedAt, '10월 26일 09:05');
  });

  it('알 수 없는 종류·상태는 사실·후보로 둔다', () => {
    const r = toMemoryRecord({...summary, kind: 'weird', state: 'odd'}, now);
    assert.equal(r.kind, 'fact');
    assert.equal(r.state, 'candidate');
  });

  it('상세에 출처와 이력을 싣고 이력은 본문 변화를 before/after로 보인다', () => {
    const history = [
      {version: 1, action: 'create', at: at(10, 20), memory: {...memory, body: '처음', version: 1, state: 'candidate'}},
      {version: 2, action: 'update', reason: '다듬음', at: at(10, 22), memory: {...memory, body: '고침', version: 2}},
      {version: 3, action: 'confirm', reason: '제안 승인 p1', at: at(10, 26), memory: {...memory, body: '고침', version: 3}},
    ];
    const r = toMemoryDetail(memory, history, now);
    assert.equal(r.content, '본문');
    assert.equal(r.scope, 'onidot-labs/orbit');
    assert.equal(r.supersededBy, 'm9');
    assert.deepEqual(r.sources?.map(s => [s.kind, s.label, s.text, s.path]), [
      ['quote', '내 말 · 10월 20일 09:05', '경로를 적어 주세요', undefined],
      ['file', '파일', undefined, '/a/b.md'],
    ]);
    // 최신 판본이 먼저
    assert.deepEqual(r.history?.map(h => h.version), [3, 2, 1]);
    assert.equal(r.history?.[0].label, '확정');
    assert.equal(r.history?.[0].tone, 'ok');
    assert.equal(r.history?.[0].before, undefined);
    assert.deepEqual([r.history?.[1].before, r.history?.[1].after], ['처음', '고침']);
    assert.equal(r.history?.[2].label, '후보로 남김');
  });
});

describe('제안 변환', () => {
  it('목록 줄은 종류·제목·날짜를 준다', () => {
    const row = toProposalRow(pending({kind: 'stale'}), now);
    assert.deepEqual([row.id, row.kindLabel, row.title, row.date, row.pending], ['p1', '낡은 기억 정리', '확정할까요?', '10월 27일 07:10', true]);
    assert.equal(toProposalRow(pending({status: 'rejected'}), now).pending, false);
  });

  it('확정 제안은 설명과 근거(기억 출처)를 준다', () => {
    const p = toProposal(pending(), [memory], now);
    assert.equal(p.kind, 'confirm');
    assert.equal(p.explanation, '두 번 나왔습니다');
    assert.equal(p.diff, undefined);
    assert.equal(p.evidence?.[0].text, '경로를 적어 주세요');
  });

  it('출처가 없는 기억은 제목을 근거로 보인다', () => {
    const p = toProposal(pending(), [{...memory, sources: undefined}], now);
    assert.equal(p.evidence?.[0].text, '경로를 명시한다');
  });

  it('파일 반영은 새 내용을 추가 줄 diff로 보이고 고칠 본문이 된다', () => {
    const p = toProposal(pending({kind: 'apply_file', payload: {path: '/x/SKILL.md', content: 'a\nb', baseHash: 'h'}}), [], now);
    assert.equal(p.kind, 'harness');
    assert.equal(p.diff?.path, '/x/SKILL.md');
    assert.deepEqual(p.diff?.lines.map(l => [l.no, l.kind, l.text]), [[1, 'add', 'a'], [2, 'add', 'b']]);
    assert.equal(p.diff?.text, 'a\nb');
  });

  it('합치기는 합친 본문을, 방향은 제안 본문을 고칠 수 있게 한다', () => {
    const m = toProposal(pending({kind: 'merge', payload: {mergedTitle: '합침', mergedBody: '합친 글'}}), [], now);
    assert.equal(m.diff?.text, '합친 글');
    assert.equal(m.kind, 'cleanup');
    const d = toProposal(pending({kind: 'direction', body: '이 방향으로'}), [], now);
    assert.equal(d.diff?.text, '이 방향으로');
    assert.equal(d.explanation, undefined);
  });
});

describe('받은 일 항목', () => {
  it('대기 제안만 승인·거절 동작이 있는 항목으로 바꾼다', () => {
    const items = toInboxItems([pending(), pending({id: 'p2', status: 'approved'})], now);
    assert.equal(items.length, 1);
    const [i] = items;
    assert.deepEqual([i.id, i.kind, i.group, i.title, i.unread, i.createdAt], ['p1', 'proposal', 'Orbit', '확정할까요?', true, at(10, 27, 7, 10)]);
    assert.equal(i.subtitle, '후보 확정 · 10월 27일 07:10');
    assert.deepEqual(i.actions?.map(a => [a.id, a.tone]), [['approve', 'primary'], ['reject', 'danger']]);
  });
});

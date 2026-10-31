// 프로필 편집 모델: 사슬 이동·검증·저장 페이로드·「키 없음 — 건너뜀」 판정·저장 실패 분류.
import assert from 'node:assert/strict';
import {describe, it} from 'node:test';
import type {ProfileJson} from '../src/daemon/api.ts';
import {
  addProfile, addRef, chainOf, chainValue, classifySaveError, cleanProfile, defaultName, fieldWithin, isAllowedRef, keysSet, lacksKey, moveRef, neededProviders, newProfile,
  parseFieldError, removeProfile, removeRef, setCheap, setDefaultProfile, setRole, skippedInChain, toSaveFile, validateProfile,
} from '../src/profiles/edit-model.ts';
import {sampleProfiles} from './daemon-fake.ts';

const base = (): ProfileJson => sampleProfiles().file.profiles[0];

describe('사슬 편집', () => {
  it('하나면 문자열, 여럿이면 배열, 없으면 비움', () => {
    assert.equal(chainValue([]), undefined);
    assert.equal(chainValue(['a']), 'a');
    assert.deepEqual(chainValue(['a', 'b']), ['a', 'b']);
    assert.deepEqual(chainOf('a'), ['a']);
    assert.deepEqual(chainOf(undefined), []);
  });
  it('위·아래로 옮기고 끝에서는 그대로(같은 배열)', () => {
    const l = ['a', 'b', 'c'];
    assert.deepEqual(moveRef(l, 2, -1), ['a', 'c', 'b']);
    assert.deepEqual(moveRef(l, 0, 1), ['b', 'a', 'c']);
    assert.equal(moveRef(l, 0, -1), l);
    assert.equal(moveRef(l, 2, 1), l);
    assert.deepEqual(l, ['a', 'b', 'c'], '원본은 바뀌지 않는다');
  });
  it('더하기는 빈 글·중복을 막고, 빼기는 그 위치만', () => {
    const l = ['a'];
    assert.equal(addRef(l, '  '), l);
    assert.equal(addRef(l, 'a'), l);
    assert.deepEqual(addRef(l, ' b '), ['a', 'b']);
    assert.deepEqual(removeRef(['a', 'b', 'c'], 1), ['a', 'c']);
  });
  it('역할 사슬을 바꾸면 형식이 맞게 저장된다(마지막을 빼면 역할이 사라진다)', () => {
    const p = setRole(base(), 'planner', ['claude:haiku']);
    assert.equal(p.roles.planner, 'claude:haiku');
    assert.equal('planner' in setRole(p, 'planner', []).roles, false);
    assert.deepEqual(setRole(p, 'worker', ['a', 'b']).roles.worker, ['a', 'b']);
    assert.equal('cheap' in setCheap(p, []), false);
    assert.equal(setCheap(p, ['x']).cheap, 'x');
    assert.deepEqual(base().roles.planner, ['pi:openrouter/free', 'claude:haiku'], '원본 프로필은 그대로');
  });
});

describe('검증', () => {
  it('정상 프로필은 문제가 없다', () => assert.deepEqual(validateProfile(base()), []));
  it('이름·폴더·허용 모델·한도를 잡는다', () => {
    const p: ProfileJson = {...base(), name: ' ', folders: ['rel', '/ok'], codexHome: 'x', roles: {planner: ['claude:haiku', 'codex:gpt']}, cheap: 'pi:other/m', limits: {turns: 0, costUsd: 5, minutes: Number.NaN}, monthlyLimitUsd: -1};
    const issues = validateProfile(p, []);
    const has = (field: string, code: string) => issues.some(i => i.field === field && i.code === code);
    assert.ok(has('name', 'name'));
    assert.ok(has('folders[0]', 'absolute'));
    assert.ok(!issues.some(i => i.field === 'folders[1]'));
    assert.ok(has('codexHome', 'absolute'));
    assert.ok(has('roles.planner[1]', 'notAllowed'));
    assert.ok(has('cheap', 'notAllowed'));
    assert.ok(has('limits.turns', 'positive'));
    assert.ok(has('limits.minutes', 'positive'));
    assert.ok(has('monthlyLimitUsd', 'positive'));
  });
  it('같은 이름은 중복', () => assert.ok(validateProfile(base(), ['work']).some(i => i.code === 'duplicate')));
  it('허용 모델 일치: 정확히·engine:*·접두 *', () => {
    assert.ok(isAllowedRef(['claude:*'], 'claude:haiku'));
    assert.ok(isAllowedRef(['pi:openrouter/*'], 'pi:openrouter/free'));
    assert.ok(!isAllowedRef(['pi:openrouter/*'], 'pi:other/x'));
    assert.ok(isAllowedRef(['codex:gpt'], 'codex:gpt'));
    assert.ok(!isAllowedRef([], 'claude:haiku'));
  });
  it('폴더 규칙의 빈 모델 목록·상대 경로', () => {
    const p: ProfileJson = {...base(), folderRules: [{models: [], allow: ['rel']}]};
    const issues = validateProfile(p);
    assert.ok(issues.some(i => i.field === 'folderRules[0].models' && i.code === 'empty'));
    assert.ok(issues.some(i => i.field === 'folderRules[0].allow[0]' && i.code === 'absolute'));
  });
});

describe('저장 페이로드', () => {
  it('빈 칸을 걷어 내고 모르는 칸은 남긴다', () => {
    const p: ProfileJson = {
      ...base(), name: ' work2 ', folders: ['/w', ' ', ''], claudeConfigDir: '  ', codexHome: ' /h ', allowedModels: ['claude:*', ''],
      roles: {default: 'claude:haiku', empty: '', x: ['', 'claude:haiku']}, cheap: [''], runRules: {studio: 'api', terminal: undefined},
      folderRules: [{models: ['claude:*'], allow: [''], deny: ['/secret']}, {models: ['']}], keywords: {ultrawork: ['ulw', ' '], plan: []}, extra: 1,
    };
    const c = cleanProfile(p);
    assert.equal(c.name, 'work2');
    assert.deepEqual(c.folders, ['/w']);
    assert.equal('claudeConfigDir' in c, false);
    assert.equal(c.codexHome, '/h');
    assert.deepEqual(c.allowedModels, ['claude:*']);
    assert.deepEqual(c.roles, {default: 'claude:haiku', x: 'claude:haiku'});
    assert.equal('cheap' in c, false);
    assert.deepEqual(c.runRules, {studio: 'api'});
    assert.deepEqual(c.folderRules, [{models: ['claude:*'], deny: ['/secret']}]);
    assert.deepEqual(c.keywords, {ultrawork: ['ulw'], plan: []});
    assert.equal(c.extra, 1);
  });
  it('같은 이름의 프로필만 바꾸고, 이름을 바꾸면 default도 따라간다', () => {
    const file = sampleProfiles().file;
    const next = toSaveFile(file, 'work', {...base(), name: 'job'});
    assert.deepEqual(next.profiles.map(p => p.name), ['job', 'side']);
    assert.equal(next.default, 'job');
    assert.equal(file.default, 'work', '원본 파일은 그대로');
    assert.equal(toSaveFile(file, 'side', {...file.profiles[1], folders: ['/s']}).default, 'work');
  });
  it('추가·삭제·기본 지정', () => {
    const file = sampleProfiles().file;
    const added = addProfile(file, ' new ');
    assert.equal(added.profiles.at(-1)?.name, 'new');
    assert.deepEqual(validateProfile(newProfile('new')), []);
    assert.deepEqual(removeProfile(added, 'new').profiles.map(p => p.name), ['work', 'side']);
    assert.equal('default' in removeProfile(file, 'work'), false);
    assert.equal(defaultName(removeProfile(file, 'work')), 'side');
    const one = {...file, profiles: [file.profiles[0]]};
    assert.equal(removeProfile(one, 'work'), one, '마지막 하나는 지우지 않는다');
    assert.equal(setDefaultProfile(file, 'side').default, 'side');
  });
});

describe('키 판정 (P3·P4)', () => {
  it('pi 모델만 키가 필요하고, 가진 공급처면 건너뛰지 않는다', () => {
    const have = keysSet([{provider: 'openrouter', set: false}, {provider: 'openai', set: true}]);
    assert.ok(lacksKey('pi:openrouter/free', have));
    assert.ok(!lacksKey('pi:openai/gpt', have));
    assert.ok(!lacksKey('claude:haiku', have));
    assert.ok(!lacksKey('codex:gpt', have));
  });
  it('사슬에서 건너뛰는 위치', () => {
    assert.deepEqual(skippedInChain(chainOf(base().roles.planner), keysSet([])), [0]);
    assert.deepEqual(skippedInChain(chainOf(base().roles.planner), keysSet([{provider: 'openrouter', set: true}])), []);
  });
  it('필요한 공급처: apiKeys + 사슬·cheap·허용 모델에 나온 것 + 키 목록', () => {
    const p: ProfileJson = {...base(), apiKeys: ['zeta'], cheap: 'pi:groq/m', allowedModels: ['claude:*', 'pi:openrouter/*']};
    assert.deepEqual(neededProviders(p, [{provider: 'extra'}]), ['extra', 'groq', 'openrouter', 'zeta']);
  });
});

describe('저장 실패 분류', () => {
  it('칸 이름이 든 메시지에서 칸과 글을 뗀다', () => {
    assert.deepEqual(parseFieldError('profiles[0].roles.planner: pi:x/y is not in allowedModels'), {field: 'roles.planner', text: 'pi:x/y is not in allowedModels'});
    assert.deepEqual(parseFieldError('[invalid_argument] profiles[2].limits.turns: must be a positive number'), {field: 'limits.turns', text: 'must be a positive number'});
    assert.deepEqual(parseFieldError('default: no profile named "x"'), {field: 'default', text: 'no profile named "x"'});
    assert.deepEqual(parseFieldError('something else'), {field: '', text: 'something else'});
  });
  it('RPC_INVALID_ARGUMENT는 칸 근처, RPC_ABORTED는 다시 불러오기, 나머지는 일반', () => {
    assert.deepEqual(classifySaveError({code: 'RPC_INVALID_ARGUMENT', message: 'profiles[0].cheap: chain must not be empty'}, 'fb'), {kind: 'field', field: 'cheap', text: 'chain must not be empty'});
    assert.deepEqual(classifySaveError({code: 'RPC_INVALID_ARGUMENT', message: 'bad'}, 'fb'), {kind: 'other', text: 'bad'});
    assert.deepEqual(classifySaveError({code: 'RPC_ABORTED', message: 'revision'}, 'fb'), {kind: 'changed'});
    assert.deepEqual(classifySaveError({code: 'RPC_UNAVAILABLE', message: 'x'}, 'fb'), {kind: 'other', text: 'fb'});
  });
  it('칸 소속 판정', () => {
    assert.ok(fieldWithin('roles.planner[1]', 'roles'));
    assert.ok(fieldWithin('roles.planner', 'roles.planner'));
    assert.ok(!fieldWithin('rolesX', 'roles'));
  });
});

describe('리뷰 수정: apiKeys·이름 바꾸기·삭제·빠진 칸', () => {
  it('저장 페이로드의 apiKeys는 draft가 아니라 최신 파일 값이다(키 저장 뒤에도)', () => {
    const stale = base(); // 편집기를 연 시점: apiKeys ['openrouter']
    const draft = {...stale, folders: ['/x'], apiKeys: ['openrouter']};
    const latest = sampleProfiles().file;
    latest.profiles[0] = {...latest.profiles[0], apiKeys: ['openrouter', 'groq']}; // 그 사이 키 저장으로 데몬이 바꿈
    const out = toSaveFile(latest, 'work', draft);
    assert.deepEqual(out.profiles[0].apiKeys, ['openrouter', 'groq']);
    assert.deepEqual(out.profiles[0].folders, ['/x'], '다른 편집은 그대로');
  });
  it('저장된 키가 있으면 이름 바꾸기를 막는다', async () => {
    const { renameBlocked } = await import('../src/profiles/edit-model.ts');
    assert.ok(renameBlocked([{set: true}], 'work', 'job'));
    assert.ok(!renameBlocked([{set: true}], 'work', ' work '));
    assert.ok(!renameBlocked([{set: false}], 'work', 'job'));
    assert.ok(!renameBlocked(undefined, 'work', 'job'));
  });
  it('삭제는 set=true 키를 먼저 지우고, 최신 파일·revision으로 프로필을 뺀다', async () => {
    const { deleteProfileWithKeys } = await import('../src/profiles/edit-model.ts');
    const order: string[] = [];
    const file = sampleProfiles();
    const api = {
      deleteApiKey: async (p: {profile: string; provider: string}) => { order.push(`del:${p.provider}`); return {deleted: true, revision: 'r2'}; },
      getProfiles: async () => { order.push('get'); return {...file, revision: 'r2'}; },
      saveProfiles: async (p: {file: {profiles: {name: string}[]}; expectedRevision: string}) => { order.push(`save:${p.expectedRevision}:${p.file.profiles.map(x => x.name)}`); return {revision: 'r3'}; },
    };
    await deleteProfileWithKeys(api, 'work', [{provider: 'openrouter', set: true}, {provider: 'groq', set: false}, {provider: 'openai', set: true}]);
    assert.deepEqual(order, ['del:openrouter', 'del:openai', 'get', 'save:r2:side']);
  });
  it('키 지우기가 실패하면 프로필을 지우지 않는다', async () => {
    const { deleteProfileWithKeys } = await import('../src/profiles/edit-model.ts');
    let saved = false;
    const api = {deleteApiKey: async () => { throw new Error('x'); }, getProfiles: async () => sampleProfiles(), saveProfiles: async () => { saved = true; return {revision: 'r'}; }};
    await assert.rejects(deleteProfileWithKeys(api, 'work', [{provider: 'a', set: true}]));
    assert.equal(saved, false);
  });
  it('필수 칸이 빠진 파일도 기본값으로 채워 연다', async () => {
    const { normalizeProfile, normalizeView } = await import('../src/profiles/edit-model.ts');
    const p = normalizeProfile({name: 'broken'});
    assert.deepEqual(p.keywords, {ultrawork: [], plan: []});
    assert.deepEqual(p.roles, {});
    assert.deepEqual(p.limits, {turns: 60, costUsd: 5, minutes: 60});
    assert.deepEqual(p.folders, []);
    assert.deepEqual(validateProfile(p), []);
    assert.equal(normalizeProfile({name: 'x', limits: {turns: 3} as never}).limits.turns, 3);
    const v = normalizeView({file: {version: 1} as never, revision: 'r', path: '', keys: undefined as never, keyStore: 'file'});
    assert.deepEqual(v.file.profiles, []);
    assert.deepEqual(v.keys, []);
  });
});

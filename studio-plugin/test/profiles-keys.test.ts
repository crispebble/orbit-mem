// 키 값은 어디에도 남지 않는다(P3): 진행 상태·오류·ctx.storage·로그·소스에 남을 길이 없다.
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join} from 'node:path';
import {describe, it} from 'node:test';
import {createKeyEntry} from '../src/profiles/keys.ts';

const SECRET = 'sk-test-SECRET-0123456789';
const tick = () => new Promise<void>(r => setTimeout(r, 1));

describe('createKeyEntry', () => {
  it('값은 setApiKey로 한 번만 가고, 상태·오류 어디에도 값이 없다', async () => {
    const sent: {profile: string; provider: string; value: string}[] = [];
    const entry = createKeyEntry({
      setApiKey: async p => { sent.push(p); return {revision: 'r2'}; },
      deleteApiKey: async () => ({deleted: true, revision: 'r3'}),
    });
    const seen: string[] = [];
    entry.subscribe(() => seen.push(JSON.stringify(entry.getState())));
    const revision = await entry.submit('work', 'openrouter', SECRET);
    assert.equal(revision, 'r2');
    assert.deepEqual(sent, [{profile: 'work', provider: 'openrouter', value: SECRET}]);
    assert.equal(entry.getState().status.openrouter, 'saved');
    for (const s of [...seen, JSON.stringify(entry.getState())]) assert.ok(!s.includes(SECRET), s);
  });

  it('실패해도 오류 객체에 값이 없고 상태가 failed', async () => {
    const entry = createKeyEntry({
      setApiKey: async () => { throw Object.assign(new Error('RPC failed'), {code: 'RPC_UNAVAILABLE'}); },
      deleteApiKey: async () => ({deleted: false, revision: 'r'}),
    });
    assert.equal(await entry.submit('work', 'openai', SECRET), undefined);
    assert.equal(entry.getState().status.openai, 'failed');
    assert.ok(!JSON.stringify(entry.getState(), Object.getOwnPropertyNames(Object(entry.getState().error?.error))).includes(SECRET));
  });

  it('빈 값은 보내지 않는다', async () => {
    let calls = 0;
    const entry = createKeyEntry({setApiKey: async () => { calls++; return {revision: 'r'}; }, deleteApiKey: async () => ({deleted: true, revision: 'r'})});
    assert.equal(await entry.submit('work', 'openai', ''), undefined);
    assert.equal(calls, 0);
  });

  it('지우기는 deleted 상태', async () => {
    const entry = createKeyEntry({setApiKey: async () => ({revision: 'r'}), deleteApiKey: async () => ({deleted: true, revision: 'r9'})});
    assert.equal(await entry.remove('work', 'openai'), 'r9');
    assert.equal(entry.getState().status.openai, 'deleted');
    await tick();
  });
});

describe('소스 점검', () => {
  const dir = new URL('../src/profiles/', import.meta.url).pathname;
  const files = readdirSync(dir).filter(n => /\.(ts|tsx)$/.test(n));
  it('프로필 화면은 localStorage·sessionStorage·ctx.storage·ctx.log를 쓰지 않는다', () => {
    for (const name of files) {
      const text = readFileSync(join(dir, name), 'utf8').replace(/\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '');
      assert.ok(!/localStorage|sessionStorage|\.storage\.|\.log\.(info|warn|error)|console\./.test(text), `${name}`);
    }
  });
  it('키 입력 칸은 password이고 값을 React 상태로 들고 있지 않다', () => {
    const text = readFileSync(join(dir, 'key-section.tsx'), 'utf8');
    assert.match(text, /type="password"/);
    assert.ok(!/useState<string>\(''\).*key|setValue|setKey|setSecret/i.test(text));
    assert.match(text, /el\.value = ''/, '보내기 전에 칸을 비운다');
  });
});

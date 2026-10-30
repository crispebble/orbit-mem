// plugin.json이 Studio의 매니페스트 검사를 통과하는지. 한 칸만 틀려도 Studio가 플러그인 전체를 켜지 않는다(state invalid).
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {describe, it} from 'node:test';

// Studio apps/desktop/src/main/plugins/manifest.ts(0.2.7)의 규칙을 옮겼다
const ITEM_ID = /^[a-z][a-z0-9-]{0,39}$/;
const COMMAND_ID = /^[a-z][a-z0-9.-]{1,63}$/;

const manifest = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8')) as {
  apps: {id: string; views: {id: string}[]}[];
  tabTypes: {id: string; app: string}[];
  commands: {id: string}[];
};
const index = readFileSync(new URL('../src/index.tsx', import.meta.url), 'utf8');

describe('plugin.json', () => {
  it('앱·보기·탭 종류 id는 소문자·숫자·붙임표, 명령 id는 점도 쓸 수 있다', () => {
    const bad = [
      ...manifest.apps.flatMap(a => [a.id, ...a.views.map(v => v.id)]).filter(id => !ITEM_ID.test(id)),
      ...manifest.tabTypes.map(t => t.id).filter(id => !ITEM_ID.test(id)),
      ...manifest.commands.map(c => c.id).filter(id => !COMMAND_ID.test(id)),
    ];
    assert.deepEqual(bad, []);
  });

  it('탭 종류·명령마다 index.tsx에 처리가 있다', () => {
    for (const t of manifest.tabTypes) assert.ok(new RegExp(`(^|\\s|')${t.id}'?:`, 'm').test(index), t.id);
    for (const c of manifest.commands) assert.ok(index.includes(`'${c.id}':`), c.id);
  });
});

describe('plugin.json 언어', () => {
  const full = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
  const hangul = /[가-힣]/;

  it('기본 칸은 영어이고 언어 API(0.1.20)를 적는다', () => {
    const {locales, ...base} = full;
    assert.equal(full.studioApi, '^0.1.20');
    assert.ok(!hangul.test(JSON.stringify(base)), JSON.stringify(base));
    assert.ok(locales?.ko);
  });

  it('한국어는 보기·명령마다 있다', () => {
    const ko = full.locales.ko;
    for (const app of full.apps) for (const v of app.views) assert.ok(hangul.test(ko.apps[app.id].views[v.id]), v.id);
    for (const c of full.commands) assert.ok(hangul.test(ko.commands[c.id]), c.id);
    assert.ok(hangul.test(ko.description));
  });
});

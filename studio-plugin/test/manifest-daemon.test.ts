// 데몬 클라이언트가 plugin.json에 선언한 것들(S4-5): 뒤쪽, 비밀값, 작업 대화 도구 창, 실행 프로필 보기·탭.
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join} from 'node:path';
import {describe, it} from 'node:test';
import {DAEMON_METHODS} from '../src/daemon/api.ts';

const manifest = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
const index = readFileSync(new URL('../src/index.tsx', import.meta.url), 'utf8');

describe('plugin.json 데몬 선언', () => {
  it('뒤쪽 프로세스·비밀값·작업 대화 도구 창·실행 프로필 보기와 탭', () => {
    assert.deepEqual(manifest.backend, {main: 'dist/backend.js'});
    assert.equal(manifest.secrets, true);
    assert.deepEqual(manifest.toolWindows, [{id: 'chat', title: 'Work chat'}]);
    assert.ok(manifest.apps[0].views.some((v: {id: string; title: string}) => v.id === 'profiles' && v.title === 'Run profiles'));
    assert.ok(manifest.tabTypes.some((t: {id: string}) => t.id === 'profiles'));
    assert.match(manifest.locales.ko.apps.orbit.views.profiles, /[가-힣]/);
    assert.match(manifest.locales.ko.toolWindows.chat, /[가-힣]/);
  });

  it('새 기능은 기능 감지로 쓰므로 studioApi 하한은 올리지 않는다', () => {
    assert.equal(manifest.studioApi, '^0.1.20');
  });

  it('index.tsx가 도구 창·보기·탭을 내보낸다', () => {
    assert.match(index, /toolWindows:\s*\{\s*chat: ChatToolWindow/);
    assert.ok(index.includes("'orbit/profiles': ProfilesView"));
    assert.match(index, /profiles: ProfilesTab/);
  });

  it('빌드가 뒤쪽 번들을 dist/backend.js로 만든다', () => {
    const build = readFileSync(new URL('../scripts/build.mjs', import.meta.url), 'utf8');
    assert.match(build, /backend: 'src\/backend\/index\.ts'/);
    assert.match(build, /platform: 'node'/);
  });
});

describe('뒤쪽 소스', () => {
  const dir = new URL('../src/backend', import.meta.url).pathname;
  const files = readdirSync(dir).filter(f => f.endsWith('.ts'));

  it('화면 라이브러리와 네이티브 모듈을 쓰지 않는다', () => {
    for (const f of files) {
      const src = readFileSync(join(dir, f), 'utf8');
      assert.ok(!/from '(react|react-dom|lucide-react|@onidot-labs\/forma[^']*)'/.test(src), f);
      assert.ok(!/\.node'|require\(/.test(src), f);
    }
  });

  it('창이 부르는 메서드 이름마다 뒤쪽 본체에 처리가 있다', () => {
    const service = readFileSync(join(dir, 'service.ts'), 'utf8');
    for (const key of Object.keys(DAEMON_METHODS)) assert.ok(service.includes(`DAEMON_METHODS.${key}]`), key);
  });
});

import assert from 'node:assert/strict';
import {readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative} from 'node:path';
import {test} from 'node:test';
import ts from 'typescript';
import {currentLocale, followStudio, localeTag, onLocale, setLocale} from '../src/i18n/index.ts';

const src = new URL('../src/', import.meta.url).pathname;

test('Studio 언어를 따르고 바뀜을 듣는다', () => {
  let handler: ((l: 'en' | 'ko') => void) | undefined;
  let off = false;
  const stop = followStudio({current: () => 'ko', onChange: h => { handler = h; return () => { off = true; }; }});
  assert.equal(currentLocale(), 'ko');
  assert.equal(localeTag(), 'ko-KR');
  let heard = 0;
  const unlisten = onLocale(() => heard++);
  handler?.('en');
  assert.equal(currentLocale(), 'en');
  assert.equal(heard, 1);
  handler?.('en');
  assert.equal(heard, 1, '같은 언어면 다시 알리지 않는다');
  unlisten();
  stop();
  assert.ok(off);
  followStudio(undefined);
  assert.equal(currentLocale(), 'en', 'ctx.locale이 없는 옛 Studio는 영어');
  setLocale('en');
});

function files(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return files(p);
    return /\.(ts|tsx)$/.test(name) ? [p] : [];
  });
}

function keyShape(v: unknown, path = ''): string[] {
  if (typeof v === 'function') return [`${path}()${v.length}`];
  if (v && typeof v === 'object') return Object.keys(v).sort().flatMap(k => keyShape((v as Record<string, unknown>)[k], `${path}.${k}`));
  return [path];
}

test('영역 사전마다 en·ko 키와 함수 인자 수가 같다', async () => {
  const dir = join(src, 'i18n/messages');
  const names = readdirSync(dir).filter(n => n.endsWith('.ts'));
  assert.ok(names.length > 0);
  for (const name of names) {
    const mod = await import(join(dir, name));
    for (const [exp, value] of Object.entries(mod)) {
      const c = value as {en?: unknown; ko?: unknown};
      if (!c || typeof c !== 'object' || !('en' in c) || !('ko' in c)) continue;
      assert.deepEqual(keyShape(c.ko), keyShape(c.en), `${name} ${exp}`);
    }
  }
});

const hangul = /[가-힣]/;

test('코드의 문자열에 한국어가 없다(사람이 읽는 글은 i18n/messages에)', () => {
  const found: string[] = [];
  for (const file of files(src)) {
    const rel = relative(src, file);
    if (rel.startsWith('i18n/messages/')) continue;
    const text = readFileSync(file, 'utf8');
    const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
    const lines = text.split('\n');
    const visit = (node: ts.Node) => {
      const isText = ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node);
      if (isText && hangul.test(node.getText(sf))) {
        const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line;
        if (!lines[line].includes('i18n:keep')) found.push(`${rel}:${line + 1} ${node.getText(sf).trim().slice(0, 60)}`);
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  assert.deepEqual(found, []);
});

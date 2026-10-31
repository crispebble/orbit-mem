import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join} from 'node:path';
import {describe, it} from 'node:test';

// OpenOrbit 전용 부품·어휘는 플러그인 안(src/parts)에 있다. Forma 0.10.0에서 빠지므로 `/ai`에서 받으면 안 된다.
const MOVED = [
  'MemoryList', 'MemoryDetail', 'MemoryForm', 'ProposalDetail', 'RetroDay',
  'MemoryListProps', 'MemoryListState', 'MemoryDetailProps', 'MemoryFormProps', 'MemoryFormValue',
  'ProposalDetailProps', 'Proposal', 'ProposalApproval', 'ProposalDenial', 'ProposalDiff', 'ProposalDiffLine', 'ProposalEvidence', 'ProposalOption',
  'RetroDayProps', 'RetroDayData', 'RetroFlag', 'RetroOutput', 'RetroMaterial',
  'MemoryRecord', 'MemoryKind', 'MemoryState', 'MemorySourceKind', 'MemorySource', 'MemoryVersion', 'ProposalKind',
  'memoryKindLabels', 'memoryStateLabels', 'memorySourceLabels', 'proposalKindLabels', 'proposalApproveHints',
];

const srcDir = new URL('../src', import.meta.url).pathname;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, {withFileTypes: true}).flatMap(entry => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(ts|tsx)$/.test(entry.name) ? [path] : [];
  });
}

/** `import`·`export ... from` 문에서 모듈 이름과 가져온 이름(원래 이름)을 뽑는다. */
function importsOf(source: string): {from: string; names: string[]}[] {
  const out: {from: string; names: string[]}[] = [];
  const re = /(?:import|export)\s+(?:type\s+)?([^;'"]*?)\s*from\s*'([^']+)'/g;
  for (const m of source.matchAll(re)) {
    const clause = m[1];
    const braces = clause.match(/\{([^}]*)\}/)?.[1] ?? '';
    const names = braces.split(',').map(s => s.trim().replace(/^type\s+/, '').split(/\s+as\s+/)[0]).filter(Boolean);
    if (clause.includes('*')) names.push('*');
    out.push({from: m[2], names});
  }
  return out;
}

describe('OpenOrbit 부품은 플러그인 안에 있다', () => {
  const files = sourceFiles(srcDir);

  it('src를 찾는다', () => {
    assert.ok(files.length > 0);
  });

  it('옮긴 이름을 @onidot-labs/studio-plugin/ai에서 가져오지 않는다', () => {
    const bad: string[] = [];
    for (const file of files) {
      for (const imp of importsOf(readFileSync(file, 'utf8'))) {
        if (imp.from !== '@onidot-labs/studio-plugin/ai') continue;
        for (const name of imp.names) {
          if (name === '*' || MOVED.includes(name)) bad.push(`${file.slice(srcDir.length + 1)}: ${name}`);
        }
      }
    }
    assert.deepEqual(bad, []);
  });

  // 옛 Forma 0.10.x 여섯 패키지는 더 나오지 않는다(F3-5). Studio 키트(forma-ui-ide)도 studio-plugin을 거쳐 받는다.
  const OLD_FORMA = /^@onidot-labs\/(forma-(ui|ide|docs|work|ai|site)|forma-ui-ide)(\/|$)/;

  it('옛 Forma 패키지와 Studio 키트를 직접 가져오지 않는다', () => {
    const bad: string[] = [];
    for (const file of files) {
      for (const imp of importsOf(readFileSync(file, 'utf8'))) {
        if (OLD_FORMA.test(imp.from)) bad.push(`${file.slice(srcDir.length + 1)}: ${imp.from}`);
      }
    }
    assert.deepEqual(bad, []);
  });

  it('옛 Forma 패키지를 의존으로 두지 않는다', () => {
    const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
    const deps = Object.keys({...pkg.dependencies, ...pkg.devDependencies, ...pkg.peerDependencies});
    assert.deepEqual(deps.filter(d => OLD_FORMA.test(d)), []);
  });

  it('여섯 옛 이름을 모두 막는다', () => {
    for (const name of ['forma-ui', 'forma-ide', 'forma-docs', 'forma-work', 'forma-ai', 'forma-site']) {
      assert.ok(OLD_FORMA.test(`@onidot-labs/${name}`), name);
      assert.ok(OLD_FORMA.test(`@onidot-labs/${name}/x`), name);
    }
    assert.ok(!OLD_FORMA.test('@onidot-labs/forma-brand'));
    assert.ok(!OLD_FORMA.test('@onidot-labs/studio-plugin/ui'));
  });
});

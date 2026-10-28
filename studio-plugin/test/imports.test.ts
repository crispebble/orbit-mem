import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join} from 'node:path';
import {describe, it} from 'node:test';

// Orbit 전용 부품·어휘는 플러그인 안(src/parts)에 있다. Forma 0.10.0에서 빠지므로 `/ai`에서 받으면 안 된다.
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

describe('Orbit 부품은 플러그인 안에 있다', () => {
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

  it('Forma 내부 모듈(@onidot-labs/forma-ai, forma-ui)을 직접 가져오지 않는다', () => {
    const bad: string[] = [];
    for (const file of files) {
      for (const imp of importsOf(readFileSync(file, 'utf8'))) {
        if (/^@onidot-labs\/forma-(ai|ui)/.test(imp.from)) bad.push(`${file.slice(srcDir.length + 1)}: ${imp.from}`);
      }
    }
    assert.deepEqual(bad, []);
  });
});

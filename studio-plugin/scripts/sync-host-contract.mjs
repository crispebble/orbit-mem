// orbit-pi 데몬 계약(host.proto)과 생성된 TS를 이 플러그인으로 다시 복사한다.
// 사용: node scripts/sync-host-contract.mjs <orbit-pi 체크아웃 경로>   (또는 ORBIT_PI_DIR)
// 계약 정본은 orbit-pi packages/orbit-host/proto/orbit/host/v1/host.proto 이고, 여기 복사본을 손으로 고치지 않는다.
import {execFileSync} from 'node:child_process';
import {mkdirSync, readFileSync, writeFileSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = process.argv[2] ?? process.env.ORBIT_PI_DIR;
if (!source) {
  console.error('usage: node scripts/sync-host-contract.mjs <orbit-pi checkout> (or set ORBIT_PI_DIR)');
  process.exit(2);
}
const host = join(resolve(source), 'packages', 'orbit-host');
const files = [
  {from: join(host, 'proto/orbit/host/v1/host.proto'), to: join(root, 'proto/orbit/host/v1/host.proto')},
  {from: join(host, 'src/gen/orbit/host/v1/host_pb.ts'), to: join(root, 'src/daemon/gen/host_pb.ts')},
];
let commit = 'unknown';
try {
  commit = execFileSync('git', ['-C', resolve(source), 'log', '-1', '--format=%h', '--', 'packages/orbit-host/proto'], {encoding: 'utf8'}).trim() || commit;
} catch {
  // 체크아웃이 git이 아니면 커밋을 모른다고 적는다
}
const header = `// Vendored from orbit-pi packages/orbit-host (contract commit ${commit}, branch feat/s4-5-studio-daemon or later).\n// Do not edit: run scripts/sync-host-contract.mjs to copy it again.\n`;
for (const {from, to} of files) {
  mkdirSync(dirname(to), {recursive: true});
  writeFileSync(to, header + readFileSync(from, 'utf8'));
  console.log(`copied ${from} -> ${to}`);
}

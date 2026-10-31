// 플러그인 번들을 만든다. React·Forma·플러그인 API는 Studio가 한 벌 싣고 import map으로 이어 주므로 번들에 넣지 않는다.
import {build} from 'esbuild';
// 외부로 둘 모듈 목록은 Studio가 내보내는 `sharedModules`를 그대로 쓴다.
import {sharedModules} from '@onidot-labs/orbit-studio-plugin/shared';

await build({
  entryPoints: {index: 'src/index.tsx'},
  outdir: 'dist',
  bundle: true,
  format: 'esm',
  platform: 'browser',
  target: 'es2022',
  jsx: 'automatic',
  external: sharedModules,
  logLevel: 'info',
});

// 뒤쪽 프로세스: Studio가 플러그인마다 Node 프로세스(utilityProcess)로 띄운다. 의존성을 모두 묶고 네이티브 모듈은 쓰지 않는다.
await build({
  entryPoints: {backend: 'src/backend/index.ts'},
  outdir: 'dist',
  bundle: true,
  format: 'esm',
  platform: 'node',
  target: 'node22',
  // ESM 번들 안의 CJS 의존성(require)을 위해
  banner: {js: "import {createRequire as __createRequire} from 'node:module';const require = __createRequire(import.meta.url);"},
  logLevel: 'info',
});

// 플러그인 번들을 만든다. React·Forma·플러그인 API는 Studio가 한 벌 싣고 import map으로 이어 주므로 번들에 넣지 않는다.
import {build} from 'esbuild';
// 외부로 둘 모듈 목록은 Studio가 내보내는 `sharedModules`를 그대로 쓴다.
import {sharedModules} from '@onidot-labs/studio-plugin/shared';

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

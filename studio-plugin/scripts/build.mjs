// 플러그인 번들을 만든다. React·Forma·플러그인 API는 Studio가 한 벌 싣고 import map으로 이어 주므로 번들에 넣지 않는다.
import {build} from 'esbuild';

// Studio의 `sharedModules`(studio/packages/studio-plugin/src/shared.ts)와 같은 목록. 바뀌면 함께 고친다.
const sharedModules = [
  'react',
  'react/jsx-runtime',
  'react-dom',
  'react-dom/client',
  '@onidot-labs/studio-plugin',
  '@onidot-labs/studio-plugin/ui',
  '@onidot-labs/studio-plugin/code',
  '@onidot-labs/studio-plugin/editor',
  '@onidot-labs/studio-plugin/ai',
];

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

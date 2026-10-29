// 임시 타입: 지금 devDependency인 @onidot-labs/studio-plugin 0.10.0에는 `/graph` 진입점이 없다.
// Studio가 Forma 0.10.4를 실은 판(API 0.1.12, `/graph`가 forma-ui `/graph`를 그대로 다시 내보냄)을 내면 devDependency를 올리고 이 파일을 지운다.
// 모양은 Forma `feat/forma-0104-graph`의 forma-ui `src/graph/net-model.ts`를 그대로 옮겼다. 변환 함수가 쓰는 타입만 둔다.
declare module '@onidot-labs/studio-plugin/graph' {
  import type {ReactNode} from 'react';
  export interface NetworkNode { id: string; label: string; cluster: string; weight?: number; description?: string; muted?: boolean }
  export interface NetworkEdge { id: string; from: string; to: string; relation: string; arrow?: boolean }
  export interface NetworkCluster { id: string; label: string; tone?: string }
  export type EdgeLine = 'solid' | 'dashed' | 'dotted';
  export interface NetworkRelation { id: string; label: string; tone: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'violet'; line?: EdgeLine; arrow?: boolean; emphasis?: number }
  export interface NetworkOverlay { id: string; label: string; icon?: ReactNode; description?: string; count?: number; paths?: readonly (readonly string[])[]; highlight?: readonly string[]; numbered?: boolean }
}

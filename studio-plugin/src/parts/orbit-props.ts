import type {Surface} from './shared.tsx';

/** 모든 Orbit 부품이 받는 공통 props. */
export interface OrbitProps {
  className?: string;
  surface?: Surface;
}

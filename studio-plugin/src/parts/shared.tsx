// 부품이 함께 쓰는 틀·빈 상태·알림·동작 훅. forma-ai의 shared.tsx에서 Orbit 부품이 쓰는 것만 옮겨 왔다.
import {useEffect, useRef, useState, type ReactNode} from 'react';
import {createActionGate} from './action.ts';

export type Availability = 'ready' | 'loading' | 'offline' | 'error';
export type Surface = 'plain' | 'outlined' | 'raised';

export function Frame({children, label, surface = 'outlined', className = ''}: {children: ReactNode; label: string; surface?: Surface; className?: string}) {
  return (
    <section className={`orbit-part orbit-surface ${className}`} data-surface={surface} aria-label={label}>
      {children}
    </section>
  );
}

export function EmptyState({title = '표시할 항목이 없습니다.', detail}: {title?: string; detail?: string}) {
  return (
    <div className="orbit-empty">
      <span>{title}</span>
      {detail && <p className="orbit-meta">{detail}</p>}
    </div>
  );
}

export function AvailabilityNotice({availability = 'ready', error}: {availability?: Availability; error?: string}) {
  if (availability === 'ready') return null;
  return (
    <p className="orbit-notice" role={availability === 'error' ? 'alert' : 'status'}>
      {availability === 'loading' ? '불러오는 중입니다.' : availability === 'offline' ? '연결이 끊겼습니다. 마지막 데이터를 표시합니다.' : error || '데이터를 불러오지 못했습니다.'}
    </p>
  );
}

export function useAction() {
  const gate = useRef(createActionGate());
  const mounted = useRef(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  async function run(action: () => void | Promise<void>) {
    if (gate.current.pending) return false;
    setPending(true);
    setError('');
    const result = await gate.current.run(action);
    if (mounted.current) {
      setPending(false);
      if (!result.ok && result.reason === 'error') setError(result.error);
    }
    return result.ok;
  }
  return {pending, error, run, clearError: () => setError('')};
}

export function ActionFeedback({action}: {action: {pending: boolean; error: string}}) {
  if (action.error) return <p className="orbit-error" role="alert">{action.error}</p>;
  if (action.pending) return <p className="orbit-sr-only" role="status">요청을 전달하고 있습니다.</p>;
  return null;
}

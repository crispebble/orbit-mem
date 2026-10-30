// 부품이 함께 쓰는 틀·알림·동작 훅. 빈 상태는 Forma StateView를 쓴다. forma-ai의 shared.tsx에서 Orbit 부품이 쓰는 것만 옮겨 왔다.
import {useEffect, useRef, useState, type ReactNode} from 'react';
import {useMessages} from '../i18n/index.ts';
import {commonText} from '../i18n/messages/common.ts';
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

export function AvailabilityNotice({availability = 'ready', error}: {availability?: Availability; error?: string}) {
  const t = useMessages(commonText);
  if (availability === 'ready') return null;
  return (
    <p className="orbit-notice" role={availability === 'error' ? 'alert' : 'status'}>
      {availability === 'loading' ? t.availability.loading : availability === 'offline' ? t.availability.offline : error || t.availability.failed}
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
  const t = useMessages(commonText);
  if (action.error) return <p className="orbit-error" role="alert">{action.error}</p>;
  if (action.pending) return <p className="orbit-sr-only" role="status">{t.sending}</p>;
  return null;
}

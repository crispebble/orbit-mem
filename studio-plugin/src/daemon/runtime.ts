// Where the activation's daemon client lives for the screens (same way as work-ui/runtime.ts). None before activate
// and after deactivate.
import {useSyncExternalStore} from 'react';
import type {DaemonStatus} from './api.ts';
import type {DaemonClient} from './client.ts';

let current: DaemonClient | undefined;
const waiting = new Set<() => void>();

export function setDaemonClient(client: DaemonClient | undefined): void {
  current = client;
  waiting.forEach(w => w());
}

export function getDaemonClient(): DaemonClient | undefined {
  return current;
}

const DOWN: DaemonStatus = {daemon: 'down', streaming: false, paired: false, lastSeq: 0};

/** The client and its latest status; the screen redraws when either changes. */
export function useDaemon(): {client: DaemonClient | undefined; status: DaemonStatus} {
  const client = useSyncExternalStore(
    cb => {
      waiting.add(cb);
      return () => {
        waiting.delete(cb);
      };
    },
    () => current,
    () => current,
  );
  const status = useSyncExternalStore(
    cb => client?.onStatus(cb) ?? (() => {}),
    () => client?.status() ?? DOWN,
    () => client?.status() ?? DOWN,
  );
  return {client, status};
}

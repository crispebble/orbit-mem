// The window side of the daemon connection: one per activation. It follows the backend's `daemon.*` events and keeps
// the latest status; the chat, the profiles screen and the decision source all read it (Studio 4차 S4-5·S4-8).
import {
  DAEMON_EVENTS,
  daemonApi,
  formatMessage,
  type DaemonApi,
  type DaemonEvent,
  type DaemonStatus,
} from './api.ts';

interface BackendLike {
  call<T = unknown>(method: string, params?: unknown, options?: {signal?: AbortSignal; timeoutMs?: number}): Promise<T>;
  on(event: string, handler: (payload: unknown) => void): () => void;
}

export interface DaemonClient {
  readonly api: DaemonApi;
  /** Latest status the backend reported (down until the first report). */
  status(): DaemonStatus;
  onStatus(listener: (status: DaemonStatus) => void): () => void;
  /** Live events of every run, in seq order. */
  onEvent(listener: (event: DaemonEvent) => void): () => void;
  /** Reads the backend status once (does not start the daemon). */
  refresh(): Promise<DaemonStatus>;
  /** Starts the daemon and pairs when needed. */
  ensure(): Promise<DaemonStatus>;
  /** Event text in the window's language: the event `key` through GetMessages, else the given fallback text. */
  text(key: unknown, args: unknown, fallback: string): string;
  /** Label of a card option id (`option.<id>`), else the id. */
  optionLabel(id: string): string;
  /** Loads the message templates of a language (call on activate and when Studio's language changes). */
  loadMessages(lang: 'en' | 'ko'): Promise<void>;
  dispose(): void;
}

const DOWN: DaemonStatus = {daemon: 'down', streaming: false, paired: false, lastSeq: 0};

export function createDaemonClient(backend: BackendLike): DaemonClient {
  const api = daemonApi(backend);
  let current: DaemonStatus = DOWN;
  let messages: Record<string, string> = {};
  const statusListeners = new Set<(status: DaemonStatus) => void>();
  const eventListeners = new Set<(event: DaemonEvent) => void>();
  const setStatus = (next: DaemonStatus) => {
    current = next;
    statusListeners.forEach(l => l(next));
  };
  const offStatus = backend.on(DAEMON_EVENTS.status, payload => {
    if (payload && typeof payload === 'object') setStatus(payload as DaemonStatus);
  });
  const offEvent = backend.on(DAEMON_EVENTS.event, payload => {
    if (!payload || typeof payload !== 'object') return;
    const event = payload as DaemonEvent;
    eventListeners.forEach(l => l(event));
  });
  return {
    api,
    status: () => current,
    onStatus(listener) {
      statusListeners.add(listener);
      return () => {
        statusListeners.delete(listener);
      };
    },
    onEvent(listener) {
      eventListeners.add(listener);
      return () => {
        eventListeners.delete(listener);
      };
    },
    async refresh() {
      const next = await api.status();
      setStatus(next);
      return next;
    },
    async ensure() {
      const next = await api.ensure();
      setStatus(next);
      return next;
    },
    text(key, args, fallback) {
      if (typeof key !== 'string') return fallback;
      const template = messages[key];
      return template === undefined ? fallback : formatMessage(template, Array.isArray(args) ? args : []);
    },
    optionLabel(id) {
      return messages[`option.${id}`] ?? id;
    },
    async loadMessages(lang) {
      messages = (await api.messages({lang})).messages;
    },
    dispose() {
      offStatus();
      offEvent();
      statusListeners.clear();
      eventListeners.clear();
    },
  };
}

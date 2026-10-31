// Orbit Studio 뒤쪽 프로세스(plugin.json `backend.main` → dist/backend.js). 메서드와 사건은 src/daemon/api.ts의 계약이다.
import {defineBackend, type BackendCall, type BackendContext} from '@onidot-labs/orbit-studio-plugin/backend';
import {DAEMON_METHODS} from '../daemon/api.ts';
import {createDaemonBackend} from './service.ts';

let service: ReturnType<typeof createDaemonBackend> | undefined;

// 메서드 이름은 start 전에 정해져 있어야 하므로 이름마다 껍데기를 두고 start 뒤의 본체로 넘긴다
const methods = Object.fromEntries(
  Object.values(DAEMON_METHODS).map(name => [
    name,
    (params: unknown, call: BackendCall) => {
      if (!service) throw new Error('backend not started');
      return service.methods[name](params, call);
    },
  ]),
);

export default defineBackend({
  async start(ctx: BackendContext) {
    // secrets.set/delete는 Studio 0.1.27부터 있다. 없는 Studio에서는 짝짓기가 STUDIO_TOO_OLD로 거절된다.
    service = createDaemonBackend({secrets: ctx.secrets, emit: (event, payload) => ctx.emit(event, payload), log: ctx.log});
    await service.start();
  },
  async stop() {
    await service?.stop();
    service = undefined;
  },
  methods,
});

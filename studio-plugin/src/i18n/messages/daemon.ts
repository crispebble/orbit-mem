// 데몬 연결 상태·오류의 글(작업 대화 창과 실행 프로필 화면이 함께 쓴다). en이 기준이고 ko는 같은 모양이다.
// 오류 코드는 src/daemon/api.ts의 DaemonErrorCode. 데몬·orb가 준 오류 글(message)은 번역하지 않고 뒤에 덧붙인다.
import {defineMessages} from '../index.ts';

export const daemonText = defineMessages({
  en: {
    status: {
      down: 'Daemon stopped',
      starting: 'Daemon starting…',
      up: 'Daemon running',
      disconnected: 'Live updates disconnected. Reconnecting…',
      start: 'Start daemon',
      starting_: 'Starting…',
    },
    errors: {
      ORB_MISSING: 'The orb command was not found. Install orbit-code and try again.',
      DAEMON_DOWN: 'The daemon is not running. Start it first.',
      DAEMON_START_FAILED: 'The daemon could not start.',
      PAIR_FAILED: 'Pairing with the daemon failed.',
      STUDIO_TOO_OLD: 'This Orbit Studio is too old to keep the daemon token. Update Orbit Studio.',
      RPC_INVALID_ARGUMENT: 'The daemon rejected the request.',
      RPC_ABORTED: 'Changed somewhere else. Reload and try again.',
      RPC_NOT_FOUND: 'The daemon could not find it.',
      RPC_UNAVAILABLE: 'The daemon is unreachable.',
      unknown: 'Something went wrong.',
    },
  },
  ko: {
    status: {
      down: '데몬 중지됨',
      starting: '데몬 시작 중…',
      up: '데몬 실행 중',
      disconnected: '실시간 연결이 끊겼습니다. 다시 연결하는 중…',
      start: '데몬 시작',
      starting_: '시작 중…',
    },
    errors: {
      ORB_MISSING: 'orb 명령을 찾지 못했습니다. orbit-code를 설치한 뒤 다시 시도하세요.',
      DAEMON_DOWN: '데몬이 실행 중이 아닙니다. 먼저 시작하세요.',
      DAEMON_START_FAILED: '데몬을 시작하지 못했습니다.',
      PAIR_FAILED: '데몬과 페어링하지 못했습니다.',
      STUDIO_TOO_OLD: '이 Orbit Studio는 데몬 토큰을 보관할 수 없는 옛 버전입니다. Orbit Studio를 업데이트하세요.',
      RPC_INVALID_ARGUMENT: '데몬이 요청을 거부했습니다.',
      RPC_ABORTED: '다른 곳에서 바뀌었습니다. 다시 불러온 뒤 시도하세요.',
      RPC_NOT_FOUND: '데몬에서 찾지 못했습니다.',
      RPC_UNAVAILABLE: '데몬에 연결할 수 없습니다.',
      unknown: '문제가 생겼습니다.',
    },
  },
});

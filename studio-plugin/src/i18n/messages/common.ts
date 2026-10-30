// 여러 화면이 함께 쓰는 글: 상태 안내, oni 호출 오류의 첫 줄, 부품의 동작 안내.
// oni가 돌려준 오류 글(한국어)은 번역하지 않고 자세한 내용으로 뒤에 붙인다.
import {defineMessages} from '../index.ts';

export const commonText = defineMessages({
  en: {
    retry: 'Try again',
    startingOrbit: 'Starting Orbit…',
    availability: {
      loading: 'Loading…',
      offline: 'Disconnected. Showing the last data.',
      failed: 'Could not load the data.',
    },
    sending: 'Sending the request…',
    requestFailed: 'Could not process the request.',
    oni: {
      connectFailed: (detail: string) => `Could not connect to oni: ${detail}`,
      callFailed: (tool: string) => `The ${tool} call failed.`,
      noStructured: (tool: string) => `oni's ${tool} reply has no structured result. Update oni to the latest version.`,
      badShape: (tool: string) => `oni's ${tool} reply has an unexpected shape.`,
      memoryNotFound: 'Could not find the memory.',
      nothingChanged: 'Nothing changed.',
    },
  },
  ko: {
    retry: '다시 시도',
    startingOrbit: 'Orbit을 켜는 중입니다',
    availability: {
      loading: '불러오는 중입니다.',
      offline: '연결이 끊겼습니다. 마지막 데이터를 표시합니다.',
      failed: '데이터를 불러오지 못했습니다.',
    },
    sending: '요청을 전달하고 있습니다.',
    requestFailed: '요청을 처리하지 못했습니다.',
    oni: {
      connectFailed: (detail: string) => `oni에 연결하지 못했습니다: ${detail}`,
      callFailed: (tool: string) => `${tool} 호출이 실패했습니다.`,
      noStructured: (tool: string) => `oni의 ${tool} 응답에 구조화 결과가 없습니다. oni를 최신 버전으로 올려 주세요.`,
      badShape: (tool: string) => `oni의 ${tool} 응답 모양이 맞지 않습니다.`,
      memoryNotFound: '기억을 찾지 못했습니다.',
      nothingChanged: '바뀐 내용이 없습니다.',
    },
  },
});

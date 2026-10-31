# orbit.decision 1판

결정 카드 계약이다. 에이전트가 사용자에게 묻는 것(AI 질문 도구, 도구 실행 허가, 운영 감시 승인, 플러그인 결정)을 카드 하나로 남기고, Orbit Studio의 Needs you와 orbit-pi(Telegram 채널 포함)가 같은 카드를 읽고 답한다. 이 디렉터리의 세 가지가 함께 계약이다.

| 파일 | 내용 |
| --- | --- |
| 이 문서 | 도구와 입력 칸 표, 생명주기, 오류, orb가 정한 값 |
| `inputs.schema.json` | 도구별 입력 JSON Schema(`$defs`의 도구 이름) |
| `response.schema.json` | 도구별 응답 JSON Schema(`$defs`의 도구 이름과 부품) |
| `examples/*.json` | 예시 응답(orb 시험이 실제 응답으로 만든 것). 파일마다 `tool`·`args`·`result`. 오류 예시는 `isError: true` |

기준 설계: Atlas 「설계 · Studio 4차」(`0s33y629n5qxy`) 「결정 카드 계약 `orbit.decision`」, 「Claude 엔진 중복 규칙」, 「설계 · OpenOrbit 4차」(`0s36w3z1cgznq`). 저장은 `work.db`의 원장 사건(`decision.open`·`decision.answer`·`decision.withdraw`)이라 다시 만들어도 같은 카드가 나오고, 변경 커서는 `orbit.work`의 `seq`와 같다.

## 판

- 모든 응답(오류 포함)의 맨 위에 `contract: {name: "orbit.decision", major: 1, minor: 0, supportedMajors: [1]}`가 있다.
- 선택 칸·열거값·도구 추가는 `minor`, 칸 삭제·이름·뜻 변경은 `major`를 올린다.
- 읽는 쪽은 모르는 칸을 무시한다. 모르는 상태·출처·owner·`by`·연결 종류는 「알 수 없음」으로 보인다.
- `response.schema.json`은 orb가 내는 1.0 응답을 엄격하게 검사한다. **읽는 쪽은 이 Schema로 응답을 거르지 않는다.**

## 도구

모두 `orb mcp --reader`(Orbit Studio·orbit-pi 같은 앱)에 있다. AI 호스트(`orb mcp`)에는 없다. 에이전트는 질문 도구로 묻고 훅이 카드를 연다.

| 도구 | 하는 일 |
| --- | --- |
| `decision_list` | 카드 목록. 최근에 연 것부터 |
| `decision_get` | 카드 하나 |
| `decision_changes` | 커서(`after`) 뒤에 바뀐 카드 id. 장기 대기 |
| `decision_open` | 카드를 연다 |
| `decision_answer` | 열린 카드에 답한다(비교 후 교체) |
| `decision_withdraw` | 열린 카드를 거둔다 |

## 입력 칸

모르는 입력 칸은 무시하고 응답 `ignoredArgs`에 이름을 적는다. 형이 틀린 칸은 `INVALID_ARGUMENT`다. 모든 도구에 `major`(기본 1)가 있다.

| 도구 | 칸 | 형 | 기본값 | 뜻 |
| --- | --- | --- | --- | --- |
| `decision_list` | `state` | `open`·`answered`·`withdrawn`·`expired` 목록 | `open` | 카드 상태 |
| | `source` | `question`·`permission`·`ops`·`plugin` 목록 | 모두 | 출처 |
| | `sessionId`, `workId` | 문자열 | 없음 | 이 세션·작업의 카드만 |
| | `updatedSince` | RFC 3339 | 없음 | 이 뒤에 바뀐 카드만(「오늘 내린 결정」) |
| | `limit` | 정수 | 50 | `limitMax`보다 크면 줄인다. 넘친 것은 `truncated` |
| `decision_get` | `id` | 문자열 | (필수) | |
| `decision_changes` | `after` | 문자열 | 없음 | 앞 응답의 `seq`(`work_changes`의 `seq`도 된다). 없으면 `reset` |
| | `wait` | 밀리초 | 0 | `waitMax`까지 기다린다 |
| `decision_open` | `source`, `owner`, `questions` | | (필수) | 아래 「카드」 |
| | `id` | 문자열 | 새로 붙임 | 이미 있는 id면 `CONFLICT` |
| | `context`, `links`, `work`, `binding`, `expiresAt` | | 없음 | 아래 「카드」. `work`에 `host`·`sessionId`만 주면 그때 진행 중인 단계에 묶는다 |
| `decision_answer` | `id`, `answers`, `by` | | (필수) | `answers`는 질문마다 하나, 같은 순서로 `{optionIds?, text?}`. 단일 선택은 선택지 하나 |
| `decision_withdraw` | `id` | 문자열 | (필수) | |
| | `reason` | 문자열 | 없음 | 예: `binding_changed`, `runtime_stopped` |

## 카드

| 칸 | 뜻 |
| --- | --- |
| `id`, `createdAt`, `updatedAt` | |
| `state` | `open`·`answered`·`withdrawn`·`expired`. `expired`는 읽을 때 정한다(`expiresAt`이 지난 열린 카드). 시간이 지나 생긴 `expired`는 `decision_changes`에 나오지 않는다 |
| `source` | `question`(AI 질문 도구), `permission`(도구 실행 허가), `ops`(운영 감시 승인), `plugin` |
| `owner` | 답을 세션에 넘길 쪽. `orbit-pi`(orbit-pi가 띄운 세션·워크플로·오케스트레이션 단계), `human`(사람이 직접 연 Claude 데스크톱·대화형 터미널 세션. 답을 넘길 수 없다), `studio`(Orbit Studio 플러그인) |
| `questions[]` | `text`, `header`, `multiSelect`, `options[]`(`id`, `label`, `detail`), 선택 `recommended`(선택지 id)·`reason`. 선택지 id를 주지 않으면 1부터 센 위치다. 질문 1~10개, 선택지 20개까지 |
| `context`, `links[]` | 근거·배경과 출처. 연결 `kind`는 `work`·`session`·`step`·`atlas`·`pr`·`memory`·`url` |
| `work` | `host`, `sessionId`, `workId`, `stepId`, `waitKey`(orbit.work 기다림 키). 지도의 「답 기다림」과 훅의 닫기를 잇는다 |
| `binding` | 카드가 묶인 대상 `{kind, ref}`. 대상이 바뀌면 연 쪽이 거둔다 |
| `expiresAt`, `closedAt`, `closeReason` | `closeReason`은 거둔 이유(`turn_ended`, `session_ended`, `declined`, 연 쪽이 준 글) |
| `answer` | `answered`일 때만: `answers[]`(`optionIds`·`text`), `by`(`studio`·`telegram`·`terminal`·`host`·`orbit-pi`), `at` |

글 칸은 비밀값을 가리고 길이를 자른다(질문 2000자, 설명·이유 1000자, 근거 4000자, 이름표 200자).

## 생명주기

| 단계 | 규칙 |
| --- | --- |
| 열기 | 사람이 직접 연 세션: OpenOrbit 플러그인 훅이 질문 도구(Claude Code `AskUserQuestion`, Codex `request_user_input`)의 사전 훅 입력에서 연다(`owner=human`, 추천 없음). orbit-pi가 띄운 세션: orbit-pi가 `decision_open`으로 연다(`owner=orbit-pi`). 운영 감시·플러그인도 `decision_open` |
| 답하기 | `decision_answer`는 비교 후 교체다. 먼저 답한 쪽이 이기고 늦은 쪽은 `CONFLICT`와 지금의 카드(`answer.by`·`at`)를 받는다. `owner=human` 카드는 `CANNOT_RELAY`: 카드에 「그 앱에서 답하기」를 보이고, 그 앱에서 답하면 사후 훅이 카드를 `answered`(`by`는 터미널이면 `terminal`, 아니면 `host`)로 닫는다. 질문 도구가 실패하면(거절·중단) `withdrawn`(`declined`) |
| 답 전달 | `owner`가 `decision_changes`로 `answered`를 보고 살아 있는 세션에 넘긴다 |
| 정리 | 세션의 턴이 끝나거나 세션이 끝나면 그 세션의 열린 카드는 `withdrawn`(`turn_ended`·`session_ended`). 끝난 턴·세션의 늦은 열기는 거둔 채로 남는다. orbit-pi는 멈출 때 자기 카드를 `decision_withdraw`로 거둔다 |
| 순서 | 비동기 훅이라 사후(답·거절)가 열기보다 먼저 올 수 있다. 그때 카드는 그 상태로 자리만 생기고, 늦은 열기가 질문을 채운다 |

## Claude 엔진 중복 규칙

orbit-pi가 띄운 `claude` 세션에도 OpenOrbit 플러그인 훅이 돈다. orbit-pi는 실행 환경에 `ORBIT_RUNTIME=orbit-pi`를 넣는다.

| 부른 쪽 | 하는 일 |
| --- | --- |
| 플러그인 훅(`orb hook …`, `ORBIT_RUNTIME=orbit-pi`) | 아무것도 하지 않는다(기억 주입·작업 기록·카드 모두 끔) |
| orbit-pi가 같은 훅 입력으로 부름(`orb hook … --runtime orbit-pi [--launcher studio\|telegram\|workflow]`) | 기억 문맥을 돌려주고 작업을 기록한다(`origin.launcher`). 카드는 열지 않는다(orbit-pi가 `decision_open`) |
| 사람이 직접 연 세션(표식 없음) | 지금처럼 플러그인 훅이 모두 한다(`origin.launcher=human`) |

`--launcher`가 없으면 `ORBIT_LAUNCHER`를 보고, 모르는 값이면 `orbit-pi`로 남긴다.

## 오류

`{contract, error: {code, message, field?, supportedMajors?, decision?}}`이고 MCP 결과의 `isError`가 `true`다.

| 코드 | 언제 | 더 주는 칸 |
| --- | --- | --- |
| `UNSUPPORTED_MAJOR` | 지원하지 않는 판 | `supportedMajors` |
| `INVALID_ARGUMENT` | 형이 틀림, 필수 칸 없음, 질문 수·선택지 수·추천 id가 틀림, 답이 질문과 맞지 않음 | `field` |
| `NOT_FOUND` | 없는 카드 | `field` |
| `CONFLICT` | 카드가 열려 있지 않음(먼저 답함·거둠·만료), 이미 있는 id로 열기 | `decision`(지금의 카드) |
| `CANNOT_RELAY` | `owner=human` 카드에 답함 | `field` |
| `UNAVAILABLE` | 작업 기록을 열지 못함. 다시 부를 수 있다 | |

## orb가 정한 값

| 값 | 1.0의 값 | 근거 |
| --- | --- | --- |
| `limitMax` | 200 | 열린 카드는 많아야 수십 개다. 「오늘 내린 결정」 하루치를 한 번에 받게 둔다 |
| `waitMax` | 30000밀리초 | `orbit.work`와 같다 |

## 실측(S4-0, 2026-11-01, Claude Code 2.1.303)

- 사전 훅 `tool_input`: `{questions:[{question, header, multiSelect, options:[{label, description}]}]}`. 선택지 id는 없다.
- 사후 훅 `tool_response`(대화 기록의 `toolUseResult`, 이 PC 대화 40개): `{questions, answers: {<질문 글>: 답}, annotations}`. 단일 선택은 고른 라벨이나 직접 쓴 글, 다중 선택은 라벨 배열(직접 쓴 글이 섞일 수 있음). 옛 판은 다중 선택을 `", "`로 이은 글로 준다.
- 답을 기다리는 동안 세션은 orbit.work에서 `waiting_user`(기다림 키 = 도구·입력 해시)다.
- `claude -p`(비대화형)에는 `AskUserQuestion`이 없다(도구 목록에도 지연 도구에도 없음). orbit-pi는 Agent SDK 권한 콜백에서 질문을 받는다(R4-2).
- Codex `request_user_input`은 질문마다 `id`를 주고 답을 그 `id`로 준다(`{answers: {<id>: {answers: [...]}}}`, 시험 데이터 기준이며 실제 Codex 세션 실측은 아직 없음).

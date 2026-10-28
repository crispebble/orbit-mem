# orbit.work 1판

`oni mcp`의 작업 도구가 내는 읽기 계약이다. 이 디렉터리의 세 가지가 함께 계약이다.

| 파일 | 내용 |
| --- | --- |
| 이 문서 | 도구와 입력 칸 표, 오류, oni가 정한 값 |
| `inputs.schema.json` | 도구별 입력 JSON Schema(`$defs`의 도구 이름) |
| `response.schema.json` | 도구별 응답 JSON Schema(`$defs`의 도구 이름과 부품) |
| `examples/*.json` | 예시 응답. 파일마다 `tool`·`args`·`result`(구조화 결과)를 둔다. 오류 예시는 `isError: true` |

기준 설계: Atlas 「설계 · Orbit 2차 — 작업 그래프 오케스트레이션」(`0s25925ehxa4n`) 「기록 형식 계약」. 이 문서에서 설계에 없던 이름은 **제안**으로 표시했다.

## 판

- 모든 응답(오류 포함)의 맨 위에 `contract: {name: "orbit.work", major: 1, minor: 0, supportedMajors: [1]}`가 있다.
- 선택 칸·열거값·도구 추가는 `minor`를 올린다. 칸 삭제·이름 변경·뜻 변경·id 규칙 변경은 `major`를 올린다.
- 읽는 쪽은 모르는 칸을 무시한다. 모르는 단계 상태는 "알 수 없음"으로 보이고 배치는 `pending`처럼 한다. 모르는 막힘 사유·증거 종류·사건 종류는 이름만 보인다.
- `response.schema.json`은 oni가 내는 1.0 응답을 엄격하게 검사한다(모르는 칸·값을 거부). oni의 작업 도구 시험은 실제 응답을 이 Schema로 검사한다. **읽는 쪽은 이 Schema로 응답을 거르지 않는다.** 위 판 규칙대로 모르는 칸과 값을 견딘다.
- 예시 값의 모양(id·`seq`·커서 글자)에는 뜻이 없다. 예시끼리는 같은 시점(`seq`)의 한 기록을 보이도록 맞췄다. `work_list.page1`의 `fliter`는 `ignoredArgs`를 보이려고 일부러 틀린 칸 이름이다.

## 도구

| 도구 | 하는 일 | 보이는 곳 |
| --- | --- | --- |
| `work_list` | 작업 목록 쪽 | AI 호스트(`oni mcp`), 읽는 쪽(`oni mcp --reader`) |
| `work_get` | 작업 하나의 전체 그래프 | AI 호스트, 읽는 쪽 |
| `work_title` | 작업 제목을 정하거나 고친다. 유일한 쓰기 도구 | AI 호스트만 |
| `work_sessions` | 세션 쪽, 또는 세션 하나의 사건 쪽 | 읽는 쪽만 |
| `work_changes` | 커서 뒤에 바뀐 작업·세션 id. 장기 대기 | 읽는 쪽만 |

## 입력 칸

모든 칸은 선택이다(`work_get.workId`, `work_title.title`만 필수). 모르는 입력 칸은 무시하고 응답 `ignoredArgs`에 이름을 적는다. 형이 틀린 칸은 `INVALID_ARGUMENT` 오류다.

| 도구 | 칸 | 형 | 기본값 | 뜻 |
| --- | --- | --- | --- | --- |
| 공통(다섯 모두) | `major` | 정수 | `1` | 읽는 쪽이 읽을 수 있는 주판. 지원하지 않는 판이면 `UNSUPPORTED_MAJOR` |
| `work_list` | `scope` | 문자열 `all` · `project` | `all` | `project`면 `project` 칸 값으로 거른다 |
| | `project` | 문자열 | 없음 | 프로젝트 범위(`project:…`). `scope=project`일 때만 쓴다. 비우면 AI 호스트는 그 세션의 프로젝트, 읽는 쪽은 `INVALID_ARGUMENT` |
| | `cwdPrefix` | 문자열 배열 | 없음 | 작업 `cwd`가 이 절대경로들 중 하나이거나 그 아래인 것만. 경로 구분자 단위로 비교한다(`/a/b`는 `/a/bc`에 맞지 않는다) |
| | `state` | 문자열 `open` · `done` · `any` | `any` | 작업 상태 |
| | `updatedSince` | RFC 3339 문자열 | 없음 | 이 시각 뒤에 갱신된 것만 |
| | `cursor` | 문자열 | 없음 | 앞 응답의 `nextCursor` |
| | `limit` | 정수 | `20` | 쪽 크기. `limitMax`보다 크면 `limitMax`로 줄인다. 1보다 작으면 `INVALID_ARGUMENT` |
| `work_get` | `workId` | 문자열 | (필수) | 읽을 작업. 없으면 `NOT_FOUND` |
| | `includeChildren` | 불 | `false` | `true`면 자식 작업 전체를 `work.children`(제안)에 넣는다. 손자도 같은 방식으로 들어간다 |
| `work_title` | `workId` | 문자열 | 없음 | 정할 작업. 비우면 MCP 연결의 작업 디렉토리와 `cwd`가 같은 열린 작업이 하나일 때만 그 작업. 아니면 `AMBIGUOUS_WORK` |
| | `title` | 문자열 | (필수) | 한 줄 제목. 줄바꿈은 공백으로 바꾸고 비밀값 모양 문자열을 가리고 `titleMax` 글자로 자른다 |
| `work_sessions` | `sessionId` | 문자열 | 없음 | 넘기면 그 세션의 사건 쪽, 안 넘기면 세션 쪽. 없는 세션이면 `NOT_FOUND` |
| | `scope`·`project`·`cwdPrefix` | 위와 같다 | 위와 같다 | 세션 쪽을 거른다 |
| | `state` | 문자열 배열 | 모두 | 세션 상태(`running`·`waiting_user`·`idle`·`stale`·`ended`)로 거른다 |
| | `cursor` | 문자열 | 없음 | 앞 응답의 `nextCursor` |
| | `limit` | 정수 | 세션 쪽 `20`, 사건 쪽 `100` | 쪽 크기. `limitMax`보다 크면 줄인다 |
| `work_changes` | `after` | 문자열 | 없음 | 앞 응답의 `seq`. 없으면 `reset: true` |
| | `wait` | 정수(밀리초) | `0` | 바뀐 것이 없을 때 기다릴 시간. `waitMax`보다 크면 `waitMax`로 줄인다. 음수면 `INVALID_ARGUMENT` |

## 파생 규칙

호스트가 아는 것은 단계의 대기·진행·완료·삭제뿐이다. 아래 값은 oni가 기록에서 계산한다(설계 「모델」). 읽는 쪽은 다시 계산하지 않고 받은 값을 쓴다.

| 칸 | 규칙 |
| --- | --- |
| `status` | 호스트 대기 + 끝나지 않은 선행 있음 → `pending`. 호스트 대기 + 선행이 모두 `done`·`cancelled` → `ready`. 호스트 진행 → `in_progress`, 막힘 사유가 열려 있으면 `blocked`. 성공한 완료 갱신 → `done`. 호스트 삭제·스냅숏 목록에서 빠짐 → `cancelled` |
| `block.reason` | 사유가 여럿 열려 있으면 `waiting_user` → `run_failed` → `run_lost` → `declared` → `interrupted` → `stale` 순으로 앞의 하나를 보인다(제안) |
| `after` | Claude Code Task의 `blockedBy`·`blocks`에서 온 간선은 `native`. 선행을 담지 못하는 목록(스냅숏)은 목록 순서대로 앞 단계 하나를 선행으로 두고 `order`. 빠진(`cancelled`) 단계는 순서 사슬에 넣지 않는다(제안). 순환이 생기는 간선은 받지 않는다 |
| `wave` | 가장 긴 선행 경로의 길이. 선행이 없으면 0 |
| `verification.state` | 1) 판정 증거가 있으면 가장 최근 판정: 통과·승인 `pass`, 수정 필요·수정 요구 `fail`, 사용자 확인 필요 `needs_user`. 2) 판정이 없고 시험 명령 증거가 있으면 가장 최근 시험 명령의 결과(`ok` → `pass`, `fail` → `fail`). 3) 둘 다 없으면 `none`. `evidenceId`는 1)·2)에서 고른 증거 |
| `verification.outdated` | 고른 증거보다 뒤에 같은 단계의 `commit` 증거가 있으면 `true` |
| `done` | 검증 결과에 묶이지 않는다. 검증 결과는 상태를 바꾸지 않고 배지로 붙는다 |
| 증거 `result` | `command`는 `ok`·`fail`, `verdict`는 `pass`·`needs_fix`·`needs_user`, `commit`은 `ok`(제안)와 `ref.repo`·`ref.sha` |
| 작업 요약 | `stepCount`(단계 수)와 `verification`(검증 결과별 단계 수, `outdated`는 지난 검증이 붙은 단계 수)은 `work_list`·`work_title`에만 있다(제안) |
| 세션 `activeStepIds` | 그 세션이 마지막으로 갱신한 단계 중 호스트 진행인 것(`in_progress`·`blocked`) |
| 세션 `openWorkIds` | 그 세션이 연 작업 중 열린 것. 끝난(`ended`) 세션도 열린 작업을 가질 수 있다 |

- `stale`(세션 상태와 막힘 사유)은 읽을 때 PC 설정 시간으로 계산한다. 시간이 지나 생긴 `stale`은 사건이 아니므로 `work_changes`에 나오지 않는다. PC 설정 시간이 없으면 `stale`은 나오지 않는다.
- 단계에 잇지 못한 실행(`stepId` 없음)과 세션 수준 증거는 `work_get`에 없고 `work_sessions({sessionId})`에만 있다.

## 쪽 넘김

- `work_list`·`work_sessions` 세션 쪽은 마지막으로 바뀐 사건 위치가 최근인 것부터, 같으면 id 순이다.
- 첫 쪽의 `seq`가 그 목록의 기준점이다. 뒤 쪽도 같은 `seq`를 돌려주고, 그 `seq` 뒤에 바뀐 항목은 뒤 쪽에서 뺀다. 그래서 넘기는 동안 바뀐 항목이 빠지거나 두 번 나오지 않는다. 그동안 바뀐 항목은 첫 쪽 `seq`로 `work_changes`를 불러 받는다.
- 세션 사건 쪽은 오래된 것부터다. `runs`·`evidence`는 첫 쪽에만 있고 `limit`과 상관없이 그 세션의 것이 모두 들어간다(설계). 그래서 `limitMax`는 사건 수의 상한이며 첫 쪽 응답 크기의 상한은 아니다.
- `work_sessions` 세션 쪽을 `state`로 거르면 한 번에 훑는 세션 수에 상한(5000)이 있다. 상한에 걸리면 쪽이 `limit`보다 짧거나 비어 있어도 `nextCursor`를 준다. 다음 쪽은 훑다 멈춘 자리부터 읽는다.
- `nextCursor`가 없으면 마지막 쪽이다. 커서는 불투명한 문자열이다. 모양이 틀리거나 다른 기록 DB에서 온 커서는 `INVALID_ARGUMENT`다.

## 변경 대기

`work_changes`는 커서(`after`) 뒤에 바뀐 작업·세션 id를 준다.

| 경우 | 응답 |
| --- | --- |
| 바뀐 것이 있음 | `workIds`·`sessionIds`와 새 `seq` |
| 한 번에 담지 못함 | `more: true`와 담은 데까지의 `seq`. 곧바로 그 `seq`로 다시 부른다 |
| 대기 시간 안에 바뀐 것이 없음 | 빈 `workIds`·`sessionIds`와 같은 `seq` |
| 커서를 이어 줄 수 없음(정리됨, 기록 DB를 다시 만들었거나 백업에서 되살림, 모양이 틀림), `after` 없음 | `reset: true`, id 목록 없음, 현재 `seq`. 보던 목록을 처음부터 다시 읽는다 |

`reset`은 오류가 아니다. 백업 사본은 만들 때 새 정체를 받으므로 되살린 DB에는 옛 커서가 이어지지 않는다.

## 오류

오류는 MCP 도구 결과 `isError: true`로 온다. 글 내용은 `<코드>: <설명>`이고, 구조화 결과는 `{contract, error: {code, message, …}}`다.

| 코드 | 언제 | 더 있는 칸 |
| --- | --- | --- |
| `UNSUPPORTED_MAJOR` | `major`가 지원하지 않는 판 | `supportedMajors` |
| `INVALID_ARGUMENT` | 칸의 형이 틀림, 필수 칸 없음, 읽는 쪽이 `scope=project`에 `project`를 비움, 모양이 틀린 쪽 커서 | `field`(제안) |
| `AMBIGUOUS_WORK` | `work_title`에 `workId`가 없고 작업을 하나로 정할 수 없음 | `candidates`(후보 작업 id. 없으면 빈 배열) |
| `NOT_FOUND`(제안) | 없는 `workId`·`sessionId` | `field`(제안) |

## oni가 정한 값

응답이 밝히는 값이다. 읽는 쪽은 응답의 값을 쓰고 아래 숫자를 고정하지 않는다.

| 값 | 1.0의 값 | 근거 |
| --- | --- | --- |
| `titleMax` | 120자 | 2026-10-28 이 PC의 Claude Code 작업 항목 제목 16건: 중앙값 52자, 최대 65자. 한 줄 제목에 두 배 가까운 여유를 둔다 |
| `waitMax` | 30000밀리초 | Studio `McpHub` 기본 `callTimeoutMs` 60초보다 짧아야 한다. 응답이 늦어도 제한에 걸리지 않게 절반으로 둔다 |
| `limitMax` | 작업·세션 쪽 50, 사건 쪽 200 | 쪽 응답이 Claude Code MCP 출력 경고(기본 25,000토큰)보다 충분히 작게. 시험에서 잰 크기는 설계 문서 「구현에서 정한 것」에 적는다 |

## 시각과 id

- 시각은 RFC 3339 UTC 밀리초(`2026-10-28T05:12:30.123Z`)다. 끝나지 않은 칸은 생략한다.
- id·`seq`·커서는 불투명한 문자열이다. 읽는 쪽은 해석·비교·계산하지 않고 받은 값을 그대로 돌려준다. `native.id`는 호스트가 붙인 번호이며 작업 안에서만 뜻이 있다.
- `sessionId`는 호스트가 붙인 세션 id 그대로다.
- 세션의 `endDetail`(제안)은 호스트가 세션 끝 사건에 준 이유(Claude Code `SessionEnd`의 `reason`)를 그대로 옮긴 글이다.
- 글 칸은 일반 텍스트이고 비밀값을 가린다. `ref.transcript`·`ref.agentTranscript`는 이 PC의 로컬 경로다.

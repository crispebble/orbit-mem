# orbit.memory 1판

`oni mcp --reader`의 기억 그래프 도구가 내는 읽기 계약이다. 이 디렉터리의 세 가지가 함께 계약이다.

| 파일 | 내용 |
| --- | --- |
| 이 문서 | 도구와 입력 칸 표, 응답 규칙, 오류, oni가 정한 값 |
| `inputs.schema.json` | 도구 입력 JSON Schema(`$defs`의 도구 이름) |
| `response.schema.json` | 응답 JSON Schema(`$defs`의 도구 이름과 부품) |
| `examples/*.json` | 예시 응답. 파일마다 `tool`·`args`·`result`(구조화 결과)를 둔다. 오류 예시는 `isError: true` |

기준 설계: Atlas 「설계 · Orbit 2차 — 기억 그래프」(`0s2gs9nd7tdt8`). 이 문서의 이름(`orbit.memory`, `memory_graph`, `link`·`related` 등)은 모두 **제안**이다.

## 판

- 모든 응답(오류 포함)의 맨 위에 `contract: {name: "orbit.memory", major: 1, minor: 0, supportedMajors: [1]}`가 있다.
- 선택 칸·열거값·도구 추가는 `minor`를 올린다. 칸 삭제·이름 변경·뜻 변경·id 규칙 변경은 `major`를 올린다.
- 읽는 쪽은 모르는 칸을 무시한다. 모르는 기억 종류·상태, 선·묶음 종류, 연결 종류(`relation`), 만든 쪽(`origin`)은 「알 수 없음」으로 보인다.
- `response.schema.json`은 oni가 내는 1.0 응답을 엄격하게 검사한다(모르는 칸·값을 거부). oni 시험은 실제 응답을 이 Schema로 검사한다. **읽는 쪽은 이 Schema로 응답을 거르지 않는다.** 위 판 규칙대로 모르는 칸과 값을 견딘다.
- 예시 값의 모양(id 글자)에는 뜻이 없다. 예시끼리는 같은 시점의 한 기록을 보이도록 맞췄다(같은 기억은 예시마다 같은 모양). `memory_graph.filtered`의 `fliter`는 `ignoredArgs`를 보이려고 일부러 틀린 칸 이름이다.

## 도구

| 도구 | 하는 일 | 보이는 곳 |
| --- | --- | --- |
| `memory_graph` | 기억 노드·관계 선·파생 묶음을 거르기에 맞게 준다. 한 기억을 주면 그 주변만 준다 | 읽는 쪽(`oni mcp --reader`)만 |

AI 호스트(`oni mcp`)에는 열지 않는다. AI는 `memory_get` 응답의 `links`로 관련 기억을 따라간다(이 계약 밖, 기억 도구).

## 입력 칸

모든 칸은 선택이다. 모르는 입력 칸은 무시하고 응답 `ignoredArgs`에 이름을 적는다. 형이 틀린 칸은 `INVALID_ARGUMENT` 오류다.

| 칸 | 형 | 기본값 | 뜻 |
| --- | --- | --- | --- |
| `major` | 정수 | `1` | 읽는 쪽이 읽을 수 있는 주판. 지원하지 않는 판이면 `UNSUPPORTED_MAJOR` |
| `focus` | 기억 id | 없음 | 주면 이 기억에서 `depth` 단계 안의 기억만 준다. 이 기억은 거르기와 상관없이 늘 들어간다. 없는 기억이면 `NOT_FOUND` |
| `depth` | 정수(1 이상) | `1` | `focus`에서 따라갈 단계 수. `depthMax`보다 크면 `depthMax` |
| `via` | `link`·`supersede`·`session`·`file`·`tag` 목록 | `link`·`supersede`·`session`·`file` | 주변을 따라갈 때 건너는 관계. 묶음은 같은 묶음의 다른 기억으로 한 단계 건넌다 |
| `kinds` | `rule`·`fact`·`decision`·`lesson`·`journal` 목록 | 모두 | 기억 종류 |
| `states` | `candidate`·`confirmed`·`stale`·`hidden` 목록 | `candidate`·`confirmed`·`stale` | 기억 상태 |
| `scopes` | `global`·`project:<키>` 목록 | 모두 | 범위 |
| `tags` | 문자열 목록 | 없음 | 이 태그 중 하나라도 가진 기억만 |
| `updatedFrom`, `updatedTo` | RFC 3339 시각 | 없음 | 마지막으로 고친 때가 이 사이인 기억만(양 끝 포함) |
| `relations` | `link`·`supersede`·`session`·`file`·`tag` 목록 | 모두 | 응답에 담을 선·묶음 종류 |
| `limit` | 정수(1 이상) | `limitMax` | 줄 기억 수 상한. `limitMax`보다 크면 `limitMax`. 넘으면 최근에 고친 것부터 남기고 `truncated: true`. `focus`는 오래됐어도 남긴다 |

거르기(`kinds`·`states`·`scopes`·`tags`·기간)는 `focus`가 있을 때 주변을 따라가며 만난 기억에도 같이 적용한다. 걸러진 기억은 건너지 않는다.

## 응답

| 칸 | 뜻 |
| --- | --- |
| `nodes` | 기억. 최근에 고친 순. 본문은 없다(고를 때 `memory_get`) |
| `edges` | 선. `link`(명시 연결, 방향 없음, `from < to`로 id 순)와 `supersede`(대체, 옛 기억 → 새 기억) |
| `groups` | 파생 묶음. `tag`(같은 태그), `session`(같은 세션 출처, `host` 있음), `file`(같은 파일 출처, `key`는 절대경로). 소속은 둘 이상 |
| `total` | 거르기를 맞춘 기억 수(`limit`으로 자르기 전) |
| `truncated` | `limit`으로 잘렸으면 `true` |
| `focus` | 입력 `focus`를 그대로 돌려준다 |
| `limitMax`, `depthMax` | oni가 받는 상한 |
| `ignoredArgs` | 무시한 입력 칸 이름 |

- 선과 묶음은 응답 `nodes` 안의 기억끼리만 준다. 한쪽이 빠진 선은 주지 않고, 남은 소속이 둘보다 적은 묶음도 주지 않는다.
- 시각은 RFC 3339 UTC(밀리초), id는 불투명 문자열이다. 글 칸(`title`·`note`·`label`)은 비밀값을 가린다.
- 연결 id는 `l-`, 대체 선 id는 `sup-` + 대체된 기억 id다(뜻 없는 글자로 다뤄도 된다).

## 오류

오류 응답은 `{contract, error: {code, message, field?, supportedMajors?}}`이고 MCP 결과의 `isError`가 `true`다.

| 코드 | 언제 | 더 주는 칸 |
| --- | --- | --- |
| `UNSUPPORTED_MAJOR` | `major`가 지원 판이 아님 | `supportedMajors` |
| `INVALID_ARGUMENT` | 칸의 형이 틀림 | `field` |
| `NOT_FOUND` | 없는 `focus` | `field` |

## oni가 정한 값

| 값 | 정한 것 | 근거 |
| --- | --- | --- |
| `limitMax` | 1000(제안) | 2026-10-29 집 PC 기억 515개 전체 응답이 261KB, oni 안에서 13~15ms였다(태그 묶음을 빼면 197KB). 그리기 시간을 재고 고친다 |
| `depthMax` | 3(제안) | 명시 연결이 아직 없어 재지 못했다. 연결이 쌓인 뒤 단계별 노드 수를 재고 고친다 |

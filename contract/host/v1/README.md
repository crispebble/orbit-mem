# host 계약 v1 짝 파일

이 폴더의 `*.json` 하나는 host 계약 op 하나의 입력 하나와 그 입력이 내야 할 출력·종료 코드를 고정한다(설계 · 지연 회상·호스트 계약 10.2). Go 시험(`cmd/orbit-mem/host_contract_test.go`)과 TypeScript 어댑터 시험이 같은 파일을 읽는다. 아래 규칙만으로 Go 없이 짝을 재현할 수 있어야 한다. 규칙을 바꾸면 이 문서와 `contract/embed.go`를 함께 고친다.

## 짝 꼴

```json
{"name": "...", "op": "...", "setup": {...}, "input": {...}, "exit": 0, "output": {...}}
```

| 칸 | 뜻 |
| --- | --- |
| `name` | 짝 이름. 파일 이름은 `<name>.json`이고 겹치지 않는다. |
| `op` | 돌릴 op. `orbit-mem host <op>`로 부른다. 모르는 op(사용법 오류 짝)일 수도 있다. |
| `setup` | 돌리기 전에 만들 상태: `db`, `calls`, `meta`, `config`(아래). 없으면 `{}`. |
| `input` | op의 stdin. JSON 값이면 그 JSON 글을, JSON 문자열이면 그 문자열 자체(JSON이 아닌 입력)를 준다. 없으면 빈 stdin. |
| `exit` | `orbit-mem host <op>`의 종료 코드. |
| `output` | stdout에 나와야 할 JSON 값 하나. 자리표를 빼면 정확히 대조한다. |

## 돌리는 차례

1. 새 빈 폴더를 `ORBIT_HOME`으로 준다. 짝마다 따로 만든다. 그 밖의 홈 파일은 없다.
2. `setup.config`가 있으면 그 객체를 JSON으로 `$ORBIT_HOME/config.json`에 쓴다(예 `{"hostTimeouts": {"context": "1ns"}}`).
3. `setup.db`대로 기억 DB(`$ORBIT_HOME/memory.db`)를 준비한다.
   - `"none"` 또는 없음: 아무것도 하지 않는다. DB 파일이 없다.
   - `"empty"`: 인자 없는 `orbit-mem migrate`를 한 번 돌린다(종료 `0`). 지금 판 스키마의 빈 DB가 생긴다. host op로는 빈 DB를 만들 수 없다(읽기 op는 파일을 만들지 않는다).
   - `"memories"`: `"empty"`와 같이 만든 뒤 `setup.calls`로 기억을 넣는다.
4. `setup.calls`를 차례로 돌린다. 각 `{op, input}`을 `orbit-mem host <op>`에 위 `input` 규칙으로 주고, 종료 `0`이어야 한다. 각 stdout의 JSON을 차례대로 모아 둔다(`$calls[N]`이 가리킨다). `memory_save`는 DB가 없으면 만든다.
5. `setup.meta`가 있으면 calls 뒤에 DB의 `meta(key, value)` 표에 직접 쓴다. 키가 있으면 값을 바꾼다. 예 `{"schema.version": "99"}`는 이 실행 파일보다 새 판의 DB가 된다.

   ```sql
   INSERT INTO meta(key, value) VALUES('schema.version', '99')
     ON CONFLICT(key) DO UPDATE SET value = excluded.value;
   ```
6. `input`을 `orbit-mem host <op>`의 stdin으로 주고 끝날 때까지 기다린다.
7. 종료 코드가 `exit`와 같고, stdout이 JSON 값 하나이며 `output`과 맞아야 한다.

`cwd`의 폴더(`/tmp/orbit-contract`)는 없어도 된다. 오류의 `error` 글은 언어 설정을 따르므로 짝은 늘 `"$string"`으로 둔다.

## 앞선 호출 가리키기

`input`이나 뒤 `setup.calls`의 입력 안에서 문자열 값 `"$calls[N].경로"`는 `setup.calls[N]` 출력 JSON의 그 경로 값으로 바꾼다(예 `"$calls[0].result.memory.id"`). 경로는 `.키`와 `[번호]`만 쓴다. 가리킨 곳이 없으면 짝이 실패한다. 그 입력보다 앞선 호출만 가리킬 수 있다.

## 대조와 자리표

객체는 키가 정확히 같아야 한다(빠진 키도 더 있는 키도 다르다). 배열은 길이와 차례가 같아야 한다. 그 밖의 값은 JSON 값으로 같아야 한다. `output` 안의 문자열 중 아래 자리표만 값 하나를 대신한다.

| 자리표 | 맞는 값 |
| --- | --- |
| `"$string"` | 아무 문자열(빈 문자열 포함) |
| `"$nonEmptyString"` | 공백만이 아닌 문자열 |
| `"$number"` | 아무 숫자 |
| `"$array"` | 아무 배열(빈 배열 포함) |
| `"$nonEmptyArray"` | 원소가 하나 이상인 배열 |
| `"$any"` | 아무 값. 그 키는 있어야 한다 |

그 밖의 `$`로 시작하는 문자열은 자리표가 아니라 그 글자 그대로 대조한다.

기억이 있는 짝(`setup.db`가 `"memories"`)의 `context`·`recall` 성공 결과는 건너뛴 까닭(`skipped`)이 없으면 `text`를 `"$nonEmptyString"`, `memoryIds`를 `"$nonEmptyArray"`로 둔다. 빈 결과로 통과하지 않게 하려는 것이다. `call` 짝은 결과의 봉투(성공·오류·도구 이름)만 대조하고 도구 결과는 `"$any"`로 둔다.

공통 칸 `idempotencyKey`는 `call`의 `memory_save`에서 `args`에 `idempotencyKey`가 없을 때만 `args`에 합친다(`args`가 이긴다). 같은 키·같은 인자로 다시 부르면 한 번만 적용하고 그 기억을 지금 상태로 다시 돌려준다(`call-memory_save-idempotent`). 같은 키에 다른 인자는 도구 오류(`tool`)다. 저장할 기억과 비슷한 기억이 있으면 `memory_save`는 저장하지 않고 성공(종료 `0`)으로 고르기를 돌려주고(`call-memory_save-similar`) (`call memory_save`는 종료 `0`이어도 `result.saved`가 `false`면 저장되지 않았으므로, 호출자는 `saved`를 확인하고 `false`면 `similar`·`choices`를 보고 `resolve`를 넣어 다시 부른다), `args.resolve`로 고른 것을 적용한다(`call-memory_save-resolve`). 결과 꼴은 MCP `memory_save`와 같고 짝은 봉투만 대조한다.

공통 칸 `trust`는 글 하나(`owner`·`agent`·`untrusted`·`system`)다. `call`의 저장이 남기는 출처 등급을 이번 호출만 그 값까지 낮춘다(`call-memory_save-trust-lowered`). 통로의 상한(host call은 `agent`)보다 올리지는 못한다. 다른 꼴(객체·숫자·배열)이나 모르는 값은 어느 op에서든 사용법 오류(종료 `2`)다(`usage-bad-trust`).

## 종료 코드와 오류 꼴

| 종료 코드 | 뜻 |
| --- | --- |
| `0` | 성공. 회상 op의 빈 결과(`skipped`: `no-db`·`schema-newer`·`schema-older`·`origin`·`not-person`)도 성공이다. |
| `1` | 사용법이 아닌 오류: `contract`, `timeout`, `no-db`, `schema-newer`, `schema-older`, `unknown-tool`, `tool`, `internal`. |
| `2` | 사용법 오류(`usage`): JSON 객체가 아닌 입력, 모르는 op, 모르는 값, 빠진 칸. |

오류는 `{"error": 글, "code": 코드}`이고 다음 칸이 더 붙을 수 있다.

- `name`: `call`의 도구 오류(`tool`)·`unknown-tool`·DB 상태 오류(`no-db`·`schema-newer`·`schema-older`)에서 그 도구 이름.
- `mayHaveApplied`: 쓰기 도구(`memory_save`·`memory_update`)의 `timeout`에만 `true`로 붙는다. 한도를 넘긴 계산도 끝까지 돌아 쓰기가 커밋될 수 있다는 뜻이다. 호출자는 다시 쓰기 전에 `memory_find`·`memory_get`으로 반영됐는지 본다. 읽기 도구와 다른 op의 `timeout`에는 이 칸이 없다. 한도를 넘긴 뒤 나온 결과는 늘 `timeout`이다(결정적).

## 위임 글

서브에이전트에게 일을 맡기는 도구 직전의 `tool-context`에 `task`(위임 글)와 `agentKind`(서브에이전트 종류)를 실으면, 결과는 `task`가 없을 때와 같고(그 도구에 걸린 기억) 위임 글의 앞 2,000자를 세션 상태에 적는다(`tool-context-delegation`). 서브에이전트가 시작할 때의 `context`(`audience` `subagent`)는 `task`가 없으면 같은 `session`·같은 `agentKind`에서 먼저 적힌, 아직 다른 `agent`가 가져가지 않은 위임 글을 가져가 그 글로 묶음을 만든다(`context-subagent-delegated`). 같은 `agent`가 다시 부르면 가져간 글을 다시 쓴다. `task`가 있으면 그 글을 쓰고 적힌 위임 글은 건드리지 않는다(`context-subagent-task`). 짝이 없으면 상시 핵심 제목 줄만이다(`context-subagent`). `reset`·`session-end`는 그 세션의 위임 글을 지운다. 묶음은 위임 회상(2,400자 이하) 뒤 상시 핵심 제목 줄(600자 이하)이고 `budget.limit`은 3,000이다.

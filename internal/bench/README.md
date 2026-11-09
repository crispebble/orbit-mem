# internal/bench

회상이 「넣어야 할 기억은 넣고, 넣지 말아야 할 것은 넣지 않는지」를 숫자로 재는 평가 묶음이다. 모델을 부르지 않고, 같은 사례 파일을 같은 코드에 돌리면 같은 숫자가 나온다. 회상 규칙을 바꾸면 이 숫자가 어디서 움직였는지 먼저 본다.

```mermaid
flowchart LR
    A[사례 파일<br/>기억 + 사례] --> B[임시 DB<br/>새로 만든 사본]
    B --> C[회상 경로<br/>지금 · 옛]
    C --> D[사례마다<br/>보인 id · 글자]
    D --> E[지표<br/>학습 · 판정 · 전체]
```

위 그림은 한 번의 실행이다. 회상 경로마다 새 DB 사본에서 따로 돈다. 보인 표시와 활동 기록이 다른 실행에 섞이지 않게 하려는 것이다. 도메인(`internal/bench`)은 회상 구현을 모른다. 지금 경로와 옛 경로는 하위 패키지의 어댑터가 포트(`Recaller`·`Finder`·`Similar`)에 맞춰 붙이고, 조립은 `internal/bench/paths`의 `Factories`가 한다. 옛 회상은 Finder·Similar도 87ab0fb의 `memory_find`(와 그것을 빌린 `find-proxy`)를 얼린 사본을 쓰고, 지금 경로를 열지 않는다(`internal/bench/legacy/find.go`). 지금 찾기가 바뀌어도 옛 기준선의 놓친 것 되찾기·중복 지표가 움직이지 않게 하려는 것이다. 그래서 두 회상의 되찾기 지표 차이에는 회상 차이와 찾기 차이가 함께 든다.

## 두 묶음

| 묶음 | 자료 | 용도 |
| --- | --- | --- |
| 공개 고정 | `internal/bench/testdata/public_set.json`(저장소 안) | 회귀. 가짜 기억과 사례만 있고 개인 내용이 없다. 시험이 돌린다. |
| 이 PC 재생 | `$ORBIT_HOME/bench/replay/`(저장소 밖) | 이 PC의 실제 기억 DB 사본과 대화 기록에서 뽑은 사례. 저장소에 넣지 않는다. |

공개 고정 묶음의 정답은 현재 회상 코드가 맞히는지가 아니라 참을 기준으로 적는다. 그래서 지금 판은 일부 사례에서 틀리고, 그 점수가 기준선으로 기록된다.

## 사례 파일 꼴

JSON 하나다. 맨 위 칸은 다음과 같다.

| 칸 | 뜻 |
| --- | --- |
| `about` | 이 파일이 무엇인지 한 줄 |
| `now` | 고정 시각(RFC3339, 예 `2026-11-03T09:00:00+09:00`). 기억의 나이와 기한은 이 시각에서 센다 |
| `cwd` | 사례가 `cwd`를 주지 않을 때 쓰는 작업 폴더(예 `/bench/app`) |
| `seed` | 70/30 나누기를 고정하는 문자열 또는 숫자 |
| `memories` | 가짜 기억 목록. 있으면 임시 DB에 넣는다. 없으면 `--db`로 받은 DB를 쓴다 |
| `cases` | 사례 목록 |

작업 폴더는 존재하지 않는 경로를 쓴다. 없는 경로의 범위는 `project:<경로>` 꼴이라(`internal/project/project.go:116-120`) 이 PC의 폴더와 섞이지 않는다. 하위 폴더(`/bench/app/web`)는 상위 저장소(`/bench/app`) 기억을 본다.

### 기억 칸

| 칸 | 뜻 |
| --- | --- |
| `key` | 사례가 가리키는 이름. 묶음 안에서 하나뿐이다 |
| `kind` | `rule`·`fact`·`decision`·`lesson`·`journal` |
| `scope` | `global`, `project:/bench/app` 꼴 |
| `quote` | `true`면 사용자 말을 그대로 옮긴 출처라 확정으로 저장한다. 아니면 후보다 |
| `title`·`body`·`tags` | 글. 태그 `분야:<이름>`은 분야 기억으로 만든다(`memory.DomainOf`) |
| `topic` | 규칙·결정은 필수. 같은 범위·같은 주제의 옛 판은 새 판에 밀려 낡는다 |
| `retention`·`ttlDays`·`reviewAfterDays` | 휘발 기한, 재검토 기한 |
| `ageDays` | 저장한 지 며칠 됐는지 |
| `followed` | 회고가 「따름」으로 판정한 횟수 |
| `pinned` | 상시 핵심(세션 시작에 늘 들어가는 확정 사용자 기억) |
| `tools` | 도구 직전에 넣을 도구 이름. `mcp__docs__*`처럼 `*`로 끝나면 접두 |
| `state` | `candidate`·`confirmed`·`stale`·`hidden`. 비우면 `quote`로 정해진다 |

같은 `kind`·`scope`·`title`은 둘 이상 넣을 수 없다(불러올 때 오류).

### 사례 칸

| 칸 | 뜻 |
| --- | --- |
| `id` | 사례 이름. 하나뿐이다 |
| `session` | 같은 세션 이름. 같은 세션은 파일 순서대로 돌고, 세션마다 세션 시작 회상을 먼저 한다. 한 세션에서 이미 넣은 기억은 다시 넣지 않는 것이 맞다 |
| `cwd` | 이 사례의 작업 폴더. 한 세션의 사례는 같은 폴더를 쓴다 |
| `origin` | 정답 칸. 이 요청을 사람이 썼는가: `user`(기본)·`agent`·`system`. 어댑터는 훅처럼 늘 `user`로 넘기고, 사람 요청인지는 회상 경로가 글로 판정한다. 요청 글(`prompt`)이 있는 사례에만 뜻이 있다(도구만·위임·중복 사례는 보지 않는다) |
| `kind` | `request`(기본)·`subagent`(위임)·`duplicate`(중복 저장) |
| `prompt` | 요청 글. 도구 사례에는 없을 수 있다 |
| `tool` | 도구 직전 사례의 도구 이름 |
| `task` | 위임 글(`kind` `subagent`) |
| `save` | 저장하려는 초안 `{kind, title, body, topic, scope}`(`kind` `duplicate`) |
| `expect` | 넣어야 할 기억 키, 최대 3개. 비면 「아무것도 넣지 않음」이 정답이다 |
| `allow` | 넣어도 틀리지 않는 것(같은 분야의 다른 기억, 상시 핵심 등) |
| `forbid` | 넣으면 안 되는 것. 제목이 비슷한 짝, 옛 판, 기한 지난 것, 다른 프로젝트·일지 |
| `note` | 왜 그 정답인지 한 줄 |

불러올 때 이런 사례는 오류다. 없는 기억 키, `expect` 4개 이상, `prompt` 사례에서 `origin`이 `user`가 아닌데 `expect`가 있는 사례, 같은 `id`.

사람 요청 판정은 이렇게 본다. 서브에이전트 보고 머리말, `<task-notification>`, 시스템 알림만 있는 메시지는 사람 요청이 아니다(`origin` `agent`·`system`, `expect` 비움). 사람이 그런 글을 붙여 넣고 질문을 덧붙이면 사람 요청이다. 인사, 「ㅇㅇ」 같은 짧은 답, 인자 없는 슬래시 명령(`/clear`)은 사소한 요청이라 `expect`가 비고, 인자 있는 슬래시 명령(`/review …`)은 인자를 요청으로 본다.

### 공개 고정 묶음에 일부러 넣은 것

가상의 제품 셋과 저장소 하나(`/bench/app`·`/bench/api`·`/bench/docs`·`/bench/other`)에 기억 222개, 사례 195개가 있다. 기억 수나 사례 수는 늘 바뀔 수 있으니 세려면 파일을 본다. 다음 함정은 사례마다 `forbid`·`note`에 적혀 있다.

- 같은 주제의 옛 판과 새 판(`model-old`·`model-new` 등 일곱 쌍), 기한 지난 휘발 기억, 재검토일이 지난 기억, 낡음·숨김 상태 기억
- 다른 프로젝트 기억, 일지, 제목이 비슷한 짝(로그인 분당 제한과 로그인 실패 잠금, 운영 배포 「전」과 「뒤」)
- 분야 기억(접근성·결제·번역), 도구 트리거 기억(가상의 `mcp__docs__write_page` 등), 상시 핵심(`pin-*`)
- 사람이 아닌 글, 붙여 넣고 묻는 요청, 인자 있는·없는 슬래시 명령, 한 세션에서 같은 기억을 두 번 묻는 사례
- 위임 사례: 계획 항목·검토 요청·조사 요청 꼴의 위임 글(정답은 일에 꼭 필요한 기억 최대 3개), 옛 판·다른 프로젝트·제목만 비슷한 기억을 `forbid`로 건 사례, 정답이 빈 사소한 위임. 위임 사례는 세션 하나에 둘에서 다섯 건씩 묶었다.
- 중복 사례: L-16 시험 자료(`internal/mcpserver/testdata/save_duplicates.json`)의 원래 기억 여섯과 변형을 옮겼다(`dup-*`). 변형은 그대로·어순·낱말·짧은 제목처럼 같은 내용을 다르게 쓴 초안이다. 같은 내용의 기억이 세션 상시 핵심(`pin-polite`)이거나 한국어·영어 짝일 때는 `allow`로 둔다. 새 내용이라 아무것도 내밀면 안 되는 초안(`new-*`)도 섞었다. 원본 자료는 mcpserver 시험이 계속 쓰므로 그대로 있다.
- 말을 바꿔 묻는 요청(`rq-*`): 정답 기억의 제목 낱말과 다른 낱말로 묻되(「토큰」↔「비밀값」, 「에러」↔「오류」) 원문 검색으로는 찾을 만한 요청. 회상 문턱이 높아져 아무것도 넣지 않는 때의 놓침 복구를 재려는 것이다.
- 넣지 않음이 정답인 일반 요청(`ch-*`): 기억과 낱말이 조금 겹치지만(「운영 배포」 뒤 케이크, 철새 「마이그레이션」) 관련 없는 질문
- 세션은 위임·중복·질문 묶음마다 여러 개로 나눠 판정용에 각 묶음이 들게 했다. 세션 이름의 숫자(`s1-plan-api`)는 나누기 해시를 맞추려고 붙였다. 세션을 더하거나 이름을 바꾸면 70/30 나누기가 통째로 움직이니 `bench recall`의 머리(test part 줄)로 판정용 크기를 다시 본다.

## 지표

요청 사례(`kind` `request`)의 정의다. 세션 시작에 들어간 기억을 A층, 요청마다 들어가는 회상을 B1, 도구 직전을 B2라 부른다.

- 보인 것: 그 세션의 A층 ∪ B1 앞 3개 ∪ B2 결과.
- 적중: `expect`가 있는 사례 중, `expect` 전부가 보인 것에 든 비율. A층 덕에 맞은 수는 `hitByStart`로 따로 센다.
- 틀린 회상: B1·B2가 넣은 id 중 `expect`·`allow`에 없는 것의 비율. `forbid`를 넣은 수는 따로 센다.
- 놓침 복구: `expect`가 있는데 B1·B2가 아무것도 넣지 않은 사례 중, `Find(요청 원문, 8)`에 `expect` 전부가 든 비율. 적중에 실패한 사례 전부로 잰 값도 함께 낸다. `prompt`가 없는 도구 사례는 뺀다.
- 넣지 않음 정확도: `expect`가 빈 사례 중 B1·B2가 0자인 비율.
- 글자: 요청당 B1(평균·중앙값·최댓값), 도구 호출당 B2, 세션당(A층 1회 + 그 세션 B1 합 + B2 합, 중앙값·최댓값), A층 따로.
- 빈 분모: 넣은 id가 0이면 틀린 회상은 0이고 `zeroDen`을 함께 낸다. 정답 있는 사례가 0이면 적중은 `null`이다.

위임 사례(`subagent`)는 `Subagent` 결과로 따로 묶는다. 위임 묶음은 두 몫이다. 늘 들어가는 핵심 **제목 줄**(역할 지침, `Shown.Lead`·`LeadChars`)과 위임 글에 맞춰 고른 **위임 회상**(나머지 id)이다. 어댑터가 두 몫을 가르고, 도메인은 회상 구현을 모른 채 `Lead`만 본다. 요청 지표가 A층을 `hitByStart`로 따로 세듯 위임 지표도 나눠 센다.

- 적중: `expect` 전부가 (제목 줄 ∪ 위임 회상)에 든 비율. 제목 줄 덕에 맞은 수는 `hitByLead`로 따로 센다.
- 틀린 회상·`forbid`·넣지 않음·글자(`chars`): **위임 회상 몫으로만** 잰다. 제목 줄 id는 틀린 회상이 아니고, 회상 몫이 0자일 때 「넣지 않음」이 맞다. 제목 줄 글자는 `leadChars`로 따로 낸다.
- 어댑터가 가르지 않으면(`Lead` 비움) 묶음 전체가 회상 몫이다. 옛 회상은 핵심 전문만 내므로 묶음 전체가 `Lead`다. 지금 경로(`hostpath`)는 훅과 같은 계약 길을 탄다: 위임 도구의 `tool-context`에 `task`·`agentKind`를 실어 위임 글을 적고, 바로 뒤 같은 종류의 `context`(`audience` `subagent`, 서브에이전트마다 다른 `agent`)가 그 글로 묶음을 만든다. 묶음의 역할 지침 몫(`inject.RoleHead`부터 끝까지)이 `LeadChars`이고, 그 몫에 든 id가 `Lead`다.

중복 사례(`duplicate`)는 비슷한 기억 상위 3의 적중과, 비슷한 것이 없어야 할 때 무언가를 내민 비율을 따로 낸다. 위임·중복 모두 요청 지표에 섞지 않는다. 지금 경로의 중복 후보는 저장 때 실제로 보는 판정이다(`memory.Store.SaveCheck`, `save-check`). 같은 제목·같은 주제로 걸린 `Existing`도 후보로 센다: 저장하면 그 기억에 출처가 더해지거나 대체되어 같은 내용 기억이 둘 생기지 않고(설계 L-16), 틀린 `Existing`은 다른 기억을 말없이 합치거나 갈아 끼우기 때문이다. 문턱은 설정 `saveSimilarMin`이고 `--set`으로 바꿔 잰다. 옛 회상의 후보는 87ab0fb `memory_find` 위의 `find-proxy`를 얼린 그대로다.

우선순위는 적중, 틀린 회상, 글자 수 순이다. 문턱을 정하는 일은 이 묶음이 아니라 회상을 고치는 작업이 한다.

### 판정용의 놓침 복구 분모

판정용(30%)에서 「아무것도 넣지 않은 사례만」 놓침 복구 분모는 지금 경로·옛 회상 모두 6이다(`rq-*`가 대부분이다). 사례 하나가 16.7%포인트라 작은 차이는 숫자로 말하지 못한다. 「적중 실패 전부」 값은 분모가 8~9다. 회상 문턱이 높아지면 분모가 늘 수 있으니 문턱을 바꾼 뒤에는 두 분모를 함께 보고, 이 값 하나로 문턱을 정하지 않는다. 원문 검색으로 못 찾는 요청은 일부러 넣지 않았다(놓침 복구의 정답은 「원문 검색으로 찾을 만하다」다).

### 판정용 수용 시험

`acceptance_test.go`는 판정용 값으로 수용 기준을 단언한다. 문턱은 학습용으로만 고르고 판정용은 고른 뒤 한 번만 잰다. 기준에 못 미친 지표는 시험으로 넣지 않고(실패를 덮지 않는다) 계획 보고에 미달로 적는다. 지금은 L-7(놓침 복구 ≥90%)만 단언하고, L-5(적중 ≥90%·틀린 회상 ≤10%·넣지 않음 ≥90%)는 1-6 b6 측정에서 미달이라 시험이 없다.

## 70/30 나누기

세션 단위로 나눈다(`session`이 없으면 사례 `id`가 한 단위). 단위마다 `seed`와 키를 이은 글의 FNV-64a 해시를 구해 그 순서로 줄을 세우고, 앞 70%(내림)는 학습용, 나머지는 판정용이다. 같은 `seed`는 늘 같은 나누기를 낸다. 회상을 고칠 때는 학습용으로 조정하고 판정용으로 확인한다. 보고는 학습·판정·전체를 다 보이고, 판정용 크기와 사례 하나가 몇 %포인트인지를 함께 적는다. 판정용에는 묶음(정답 있는 요청·넣지 않음·위임·중복)마다 사례가 1개 이상 있어야 하고, 시험이 이를 확인한다.

## 기준선 파일

| 파일 | 뜻 | 갱신 |
| --- | --- | --- |
| `internal/bench/testdata/baseline-current.json` | 지금 경로(호스트 계약)의 점수와 사례별 보인 id·글자 | 갱신 명령으로만 다시 쓴다 |
| `internal/bench/testdata/baseline-legacy.json` | 옛 회상(87ab0fb 시점을 얼린 사본)의 점수 | 없으면 시험이 실패한다. 지표 정의나 사례가 바뀔 때만 파일을 지우고 `-write-legacy-baseline`으로 다시 쓴다(파일이 없을 때만 쓰고 있으면 덮어쓰지 않는다). 옛 회상 코드는 그대로 둔다. 그 밖에는 엄격 비교만 한다 |

지금 경로의 기준선을 다시 쓰는 명령은 저장소 루트에서 실행한다.

```sh
go test ./internal/bench -run TestPublicSet -update-golden
```

`-update-golden` 깃발은 `internal/bench` 시험 패키지에만 있다. 다시 쓴 뒤 `git diff`로 어느 사례의 값이 움직였는지 눈으로 본다. 회상을 일부러 바꾼 때가 아니면 이 파일이 바뀌어서는 안 된다. 옛 회상 기준선은 회상을 고쳐 나가며 비교하는 출발점이라 옛 회상 코드가 바뀌었다는 이유로는 고치지 않는다. 지표 정의나 사례가 바뀐 때만, 옛 회상 코드는 그대로 두고 파일을 지운 뒤 아래로 다시 쓴다. 다시 쓴 뒤 `git diff`로 옛 회상의 값 가운데 정의 변경 밖의 것이 움직이지 않았는지 본다.

```sh
go test ./internal/bench -run TestPublicSet -write-legacy-baseline
```

## 명령

두 명령은 `orbit-mem bench` 아래에 있다.

```sh
# 사례 파일을 DB 사본에 돌려 지표를 낸다. --db 파일은 열지 않고, bench.CopyDB(internal/bench/copy.go)가 원본 .db·-wal·-journal을 임시 폴더로 바이트 복사한 사본에서 돈다(-shm은 복사하지 않음). 복사 중 원본이 바뀌면 다시 뜨고 5번 실패하면 오류.
orbit-mem bench recall --db <기억 DB 파일> --cases <사례 파일>

# 지금 회상 경로의 설정을 덮어써 잰다(--set key=value, 여러 번). 키와 값은 설정 파일이 거르는 검사(config.Config.Validate)로 실행 전에 거른다. 얼린 옛 회상은 설정을 읽지 않는다.
orbit-mem bench recall --cases <사례 파일> --set saveSimilarMin=0.7 --set recallChars=800

# 이 PC의 대화 기록에서 재생 자료를 뽑는다. 쓰기는 --out에만 하고, git 작업 트리 안은 거절한다.
orbit-mem bench extract --projects <대화 기록 폴더> --db <기억 DB 파일> --out <폴더>
```

- `bench recall`은 `--cases`에 `memories`가 없을 때 `--db`가 필요하고, 사례는 키 대신 DB의 실제 기억 id로 정답을 가리킨다.
- `bench extract`의 기본값은 `--projects`가 Claude 설정 폴더의 `projects`, `--out`이 `$ORBIT_HOME/bench/replay`다. 결과는 DB 사본 `memory-copy.db`, `sessions.json`, 정답 칸이 빈 `cases.json`이다. 정답표는 사람이나 AI가 `cases.json`을 고쳐 `answers-draft.json`으로 만든다.
- 재생 자료에는 이 PC의 기억과 대화가 들어 있다. 저장소에 넣거나 공개하지 않는다.

## 1-3·1-4를 합친 뒤

`internal/memory`(1-3)와 `internal/inject`·`internal/host`(1-4)를 이 브랜치에 합친 뒤 한 일과, 앞으로 합칠 때 하는 일이다.

1. `go test ./internal/bench -run TestPublicSet -update-golden`으로 `baseline-current.json`을 다시 재고, 바뀐 지표를 이슈에 남긴다. 조립은 `internal/bench/paths`의 `Factories`(`current`·`legacy`) 한 곳이고, 깃발은 `-update-golden`이다.
2. `baseline-legacy.json`은 옛 회상 코드가 바뀌면 안 된다. 깨지면 legacy 사본이 기대는 `memory`·`place`·`project` 심볼(목록은 `internal/bench/legacy/symbols_test.go`)이 바뀐 것이니 사본을 고친다. 0.17 통합 때 1-3이 `Store.Find`를 FTS5 색인으로 바꿔(`272b9ca`) 지금 경로의 찾기를 빌려 쓰던 옛 기준선이 움직였고, 그 뒤로 찾기도 사본에 얼렸다. 지표 정의나 사례가 바뀌면 위 「기준선 파일」 규칙대로 다시 쓴다.
3. `-tags benchequiv` 대조 시험(`hostpath`·`legacy` 둘 다)은 지웠다. 「87ab0fb 판과 같다」는 일회성 증거였고 증거 시점은 87ab0fb다. 1-4가 inject 시그니처와 사람 요청 판정을 바꿔 컴파일이 깨지고 값이 달라지는 것이 정상이었다. 옛 회상이 그대로인지는 기본 시험의 `baseline-legacy.json` 엄격 비교가 지킨다. 지금 코드끼리의 어댑터 대조(호스트 계약 `Request`가 `inject`를 직접 부른 것과 같은 id를 낸다, 사람이 아닌 글은 회상하지 않는다)는 태그 없이 기본 시험(`hostpath/hostpath_test.go`)으로 살렸다. `go vet -tags benchequiv ./internal/bench/...`는 태그를 쓰는 파일이 없어 그대로 통과한다.
4. 기대는 바깥 모양이 바뀌었는지 본다. `internal/bench/load.go`는 `memory.Import`·`Pin`·`RecordEvent`를, `internal/bench/hostpath`는 `host.Request`·`host.Setup`·op 이름·`memory_find` 결과 id·`memory.Store.SaveCheck`를 기대한다. 바뀌면 여기를 고친다.
5. 지금 경로의 Similar는 저장 때 판정(`SaveCheck`)이다(`save-check`). 옛 회상의 Similar는 87ab0fb 찾기 위의 `find-proxy`를 얼린 것이라 중복 묶음의 두 회상 비교에는 판정 차이와 찾기 차이가 함께 든다.

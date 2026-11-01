# orbit-mem

저장소 `onidot-labs/orbit-mem`이다. 이 PC의 AI 호스트(Claude Code·Codex)가 함께 쓰는 공유 기억 시스템 `orbit-mem`(Go 실행 파일 하나)을 담는다. 기억과 그 출처, 대화 기록으로 배운 것을 제안하는 회고, 호스트 세션의 활동 기록, 그리고 이것들을 호스트에 붙이는 MCP 서버와 훅이다. 서버와 상주 프로세스는 없다. 개인 내용 없이 공개할 수 있게 만든다. 버전은 0.x를 쓴다.

OpenOrbit의 명령 `orb`(플러그인 원본·빌드·호스트 등록, 오케스트라, 작업 현황)는 `onidot-labs/openorbit`(TS)에 있다. OpenOrbit 플러그인은 그 원본에 이 저장소의 기억 조각(MCP 서버 `orbit-mem`과 훅 셋)을 끼워 빌드한다. 구조: Atlas 설계 · OpenOrbit 4차 「구조 (2026-11-01 확정)」.

이력: 2026-11-01까지 이 저장소의 실행 파일은 OpenOrbit 명령 `orb`(4차 동안 별칭 `oni`)였고, 작업 기록(`work.db`, 훅 번역·결정 카드), orbit-pi 런타임·데몬·TUI 실행, OpenOrbit TS CLI 다리를 함께 담았다. N4-2 차례 5(ONID-121)에서 그것들을 걷어 내고 `orbit-mem` 0.4.0이 됐다. 옛 `work.db`는 지우지 않고 읽기 전용으로 활동 기록 찾기에 보이며 하루 백업도 이어 간다.

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `orbit-mem install` | `orbit-mem`을 `~/.local/bin`(Windows는 `%LOCALAPPDATA%\Programs\orbit`)에 두고, 설정(`claudePath`·`codexPath`, `--plugin-source <원본>`, `--apply-target <경로>`)과 기억 DB를 만들고, 회고 예약(launchd 라벨 `dev.onidot.orbit.retro`·작업 스케줄러, `orbit-mem retro run`)을 건다. 플러그인 빌드와 호스트 등록은 OpenOrbit `orb install`이 한다(끝에 안내) |
| `orbit-mem uninstall` | 회고 예약만 지운다. 기억 DB·활동 기록·백업은 남긴다. 플러그인을 빼는 것은 `orb uninstall` |
| `orbit-mem doctor [--json]` | 기억 수, 대기 제안, 회고·예약, 활동 기록 세션 수(옛 `work.db` 세션 수 따로), 호스트와 플러그인 등록, 최근 회고·백업 |
| `orbit-mem search <질의> [--kind K]… [--all] [--stale] [-n N]` | 기억 찾기(`memory_find`와 같은 함수) |
| `orbit-mem show <id> [--history]` | 기억 하나 읽기(`memory_get`) |
| `orbit-mem list [--kind K]… [--state S]… [--tag T]… [--all] [-n N]` | 최근 기억 목록(질의 없는 `memory_find`, 기본은 후보·확정) |
| `orbit-mem edit <id> [--title] [--body \| --body-file F] [--tag T]… [--link ID]… [--unlink ID]… [--note]` | 기억 고치기(`memory_update`, 판이 오르고 이력이 남는다) |
| `orbit-mem stale <id> [--note]` | 낡음으로 표시(`memory_update` action=stale, 지우지 않는다) |
| `orbit-mem export [--format jsonl\|md] [--out 경로] [--all]` | 사람이 읽고 옮길 수 있게 내보낸다. jsonl은 한 줄에 기억 하나(출처·나온 날·연결 포함), `--out`이 없으면 stdout. md는 폴더에 `<id>.md`와 `index.md`. 기본은 숨김 빼고 전부, `--all`은 숨김(거절한 후보)까지 |
| `orbit-mem import <jsonl> [--dry-run]` | export한 jsonl을 들여온다. 아래 「들여오기 규칙」 |
| `orbit-mem activity [질의] [--host H] [--project all\|DIR] [--since 날짜] [-n N]` | 활동 기록 찾기(`activity_find`) |
| `orbit-mem proposals` | 회고 제안 보기·하나씩 결정(`review`)·승인·거절·고쳐서 승인 |
| `orbit-mem retro run` | 세션 끝 회고 대기열을 비우고, 밀린 하루 회고(최대 7일, 근거 파일이 바뀐 기억 판정 포함), 효과 판정, 지난주 주간 회고를 헤드리스 호스트로 돌린다. 예약이 부른다 |
| `orbit-mem retro sessions\|tidy\|legacy\|status\|material` | 세션 끝 회고 대기열 비우기(세션 끝 훅이 띄운다), 기억 그래프 정리, 옛 기억 한 묶음 판정(`legacyMemories`·`legacyBatches`·`retiredNames`), 상태, 재료 보기 |
| `orbit-mem mcp [--retro ID \| --reader]` | 호스트가 띄우는 MCP 서버(stdio). 아래 「MCP 도구」 |
| `orbit-mem hook session-start\|prompt\|session-end --host claude\|codex` | 호스트 훅(stdin JSON). 세션 시작에 고정 묶음(확정 규칙 전문·최근 결정 제목·회고 한 줄, 세션 안에서는 바뀌지 않음), 요청마다 강하게 맞는 기억을 `recallChars` 안에서, 세션이 끝나면 세션 끝 회고 대기열에 넣고 `orbit-mem retro sessions`를 뒤에서 띄운다. 셋 다 활동 기록을 남긴다. 어떤 경우에도 호스트를 실패시키지 않는다(종료 0). `hook work`(옛 플러그인의 작업 기록 훅)는 입력을 버리고 아무것도 하지 않는다 |
| `orbit-mem inject session-start\|prompt [--cwd D] [--session S]` | orbit-pi에 훅과 같은 기억 문맥을 JSON(`text`·`memoryIds`)으로 준다. 요청 글은 stdin |
| `orbit-mem plugin fragment [--path P]` | 기억 조각(MCP 서버 `orbit-mem`과 훅 셋)을 그 경로(기본: 지금 `orbit-mem`)로 채운 JSON으로 낸다. `{{host}}`만 남는다. OpenOrbit `orb plugin build <원본> --fragment <조각>`이 받는다 |
| `orbit-mem migrate --from <사본>` | 옛 Knowledge 백업 사본을 한 번 옮긴다 |
| `orbit-mem stats [--days N \| --week 월요일]` | 지표: 반복 교정, 요청 적중, 사용 판정, 빈 검색, 저장, 쓰기 판정, 제안 결정, 근거 파일 판정, 효과 원장, 회고 비용, 기억 그래프, 옛 기억 정리 |
| `orbit-mem ledger [--all]` | 승인한 기억·하네스·지침 변경과 효과 판정 |
| `orbit-mem lab` | 하네스 실험실. 지금 플러그인과 고친 플러그인을 `labRuns`번씩 실험 전용 `ORBIT_HOME`에서 돌리고 결과물을 익명으로 고른다. 후보 플러그인은 OpenOrbit `orb plugin build <원본> --out <폴더> --fragment <조각>`으로 빌드한다(`orb`는 `ORBIT_ORB`, PATH, `~/.local/bin/orb` 순으로 찾는다). 플러그인 원본(`pluginSource`)을 쓰는 PC에서만 돈다 |
| `orbit-mem config`, `orbit-mem backup`, `orbit-mem version` | 설정 보기·바꾸기, 즉시 백업(기억 DB와 옛 `work.db`), 판 |

## 얇은 CLI의 출력

`search`·`show`·`list`·`edit`·`stale`·`activity`·`import`는 MCP 도구와 같은 함수를 부른다. stdout이 터미널이면 사람이 읽는 표·글을, 파이프나 파일이면 JSON(도구 응답과 같은 구조)을 낸다. `--json`이면 언제나 JSON이다. 이 명령은 AI 도구 사용 사건(`tool.*`)으로 세지 않는다.

들여오기 규칙(`import`): 줄마다 기억 하나를 읽어 같은 id·종류·범위·상태·제목·본문·태그·시각·출처·나온 날로 넣고, 판 이력에 `import`(사유 `import: <파일 이름>`) 한 줄을 남긴다. 이미 있는 id(옛 Knowledge id가 같은 것 포함)는 그대로 두고 `exists`로, 같은 종류·범위·제목의 숨기지 않은 기억이 있으면 `duplicate`로, 깨진 줄이나 틀린 값은 `invalid`로 건너뛰고 줄 번호와 함께 알린다. 양쪽 기억이 다 있게 된 연결은 다시 잇는다. 쓰기 전에 `backups/before-import-*.sqlite` 사본을 만든다. `--dry-run`은 쓰지 않고 결과만 보인다.

## 활동 기록

순수 Claude Code·Codex 세션은 작업 현황 없이 활동 기록만 남긴다: 언제·어느 호스트·어느 프로젝트에서 무엇을 요청했는지다. `memory.db`의 `activity` 표(스키마 판 `user_version` 3)에 세션 하나가 한 줄이다.

| 칸 | 뜻 |
| --- | --- |
| `host`, `session` | 기본키. 호스트(`claude`·`codex`)와 세션 id |
| `project_scope`, `project_label`, `cwd` | 프로젝트 범위(`project:<키>`)·이름·작업 디렉터리 |
| `launcher` | `human`(사람이 연 세션), orbit-pi가 띄운 세션은 `orbit-pi`·`studio`·`telegram`·`workflow` |
| `parent` | 세션을 띄운 쪽이 준 `ORBIT_PARENT` |
| `started_at`, `last_at`, `ended_at` | 시작·마지막 활동·끝 시각(Unix ms). 다시 시작(resume·clear·compact)하면 끝 시각이 지워진다 |
| `prompts`, `first_prompt`, `last_prompt` | 요청 수, 첫·마지막 요청 글(비밀값을 가리고 공백을 접어 300자까지) |

- `session-start`가 줄을 만들고(있으면 다시 연다), `prompt`가 요청 수·시각·글을, `session-end`가 끝 시각을 남긴다. 시작 훅을 놓쳐도 첫 요청이나 끝이 줄을 만든다.
- 회고 실행 안(`ORBIT_RETRO_ID`)의 세션은 남기지 않는다.
- orbit-pi가 띄운 세션(`ORBIT_RUNTIME=orbit-pi`)에서는 플러그인 훅이 아무것도 하지 않고, orbit-pi가 `--runtime orbit-pi --launcher <studio·telegram·workflow>`로 부른 훅만 기억을 주입하고 기록한다(모르는 launcher는 `orbit-pi`).
- 기억 DB가 없으면(설치 전) 훅은 아무것도 만들지 않는다.

찾기(`activity_find`·`orbit-mem activity`)는 이 표와 옛 작업 기록 `work.db`의 세션(그 세션이 연 작업 제목 포함)을 함께, 출처(`source`: `activity`·`work.db`)를 붙여 마지막 활동 최신순으로 준다. 같은 호스트+세션이 둘에 다 있으면 활동 기록 한 줄에 옛 작업 제목을 붙인다. `work.db`는 `mode=ro`로만 열고 쓰지 않으며, 없으면 건너뛰고, 읽지 못하면 `legacyError`에 이유를 적고 활동 기록만 준다.

## MCP 도구

`orbit-mem mcp`(서버 이름 `orbit-mem`, 응답 글은 AI가 읽으므로 한국어):

- 기억 6개: `memory_find`, `memory_get`, `memory_save`, `memory_update`, `proposals`, `proposal_decide`. `memory_save`·`memory_update`는 관련 기억 연결 `links`·`link`·`unlink`를 받고 `memory_get`은 `links`를 준다.
- 활동 기록: `activity_find` — 입력 `query`·`host`·`project`(비우면 이 프로젝트, `all`이면 전부, 아니면 프로젝트 디렉터리 절대경로)·`since`(`YYYY-MM-DD`, RFC 3339, `7d`·`12h`)·`limit`(기본 20, 최대 100). 출력 `{"items":[{source, host, session, project, projectScope, cwd, launcher, parent, startedAt, lastAt, endedAt, prompts, firstPrompt, lastPrompt, works}], "legacyError"?}`.
- `--reader`: Orbit Studio 같은 앱 전용으로 기억 그래프 `memory_graph`만 연다(계약 `orbit.memory`, `internal/memory/contract/v1`).
- `--retro ID`: 그 회고 실행의 도구만 연다.

## 언어

`orbit-mem`이 사람에게 보이는 출력·오류·도움말은 영어가 기본이고 한국어로 바꿀 수 있다(`en`·`ko`). `orbit-mem config set language ko`로 바꾸고, `ORBIT_LANG=en|ko`가 있으면 설정보다 먼저 따른다. JSON 출력과 MCP 응답 구조, AI가 읽는 글(MCP 도구 설명, 세션 주입, 회고 프롬프트), 기억·제안 본문은 언어 설정과 무관하다. 문구는 `internal/i18n/locales/<언어>/*.json`에 키로 두고, 언어마다 같은 키와 같은 서식 자리를 갖는다(시험으로 확인).

Codex는 플러그인 훅을 사용자가 `/hooks`에서 신뢰해야 돌리고, 훅이 바뀌면 신뢰가 풀린다. 새 플러그인을 깐 뒤에는 Codex에서 `/hooks`를 열어 다시 신뢰한다.

## 데이터

- 위치: `ORBIT_HOME`, 없으면 Mac `~/Library/Application Support/orbit`, Windows `%AppData%\orbit`.
- `memory.db`(SQLite: 기억·제안·회고·지표용 사건·쓰기 판정·효과 원장·세션 끝 회고 대기열·활동 기록), `config.json`, `backups/`(매일 사본 기본 14개: `memory-*`와 옛 `work-*`, 옮기기·들여오기 전 사본은 따로), `retro/`(진행 중 회고 재료), `logs/`, `lab/`(실험실).
- `work.db`: 옛 작업 기록. 읽기 전용으로 활동 기록 찾기에 보이고 하루 백업만 이어 간다. 새로 쓰지 않는다.
- 옛 설정 키 `work`·`workStaleAfter`·`workTestCommands`는 읽을 때 무시하고 다시 저장하면 빠진다.
- PC끼리 공유하지 않는다.

## 개발

```bash
go test ./...
go vet ./...
go build -o dist/orbit-mem ./cmd/orbit-mem
GOOS=windows GOARCH=amd64 go build -o dist/orbit-mem.exe ./cmd/orbit-mem
```

| 패키지 | 내용 |
| --- | --- |
| `cmd/orbit-mem` | 명령 |
| `internal/memory` | 기억 저장소(SQLite), 찾기·연결·태그·제안·지표 사건·원장·활동 기록·내보내기·들여오기 |
| `internal/activity` | 활동 기록 찾기(새 기록 + 옛 `work.db` 읽기 전용) |
| `internal/mcpserver` | MCP 서버(기억·활동 기록 도구, `memory_graph`, 회고용 도구)와 얇은 CLI가 부르는 `Tools` |
| `internal/inject` | 세션 시작 묶음·요청 회상 |
| `internal/retro`, `internal/transcript` | 회고와 그 재료(호스트 대화 기록) |
| `internal/memplugin` | 기억 조각(`fragment.json`)과 OpenOrbit `orb plugin build` 호출 |
| `internal/lab` | 하네스 실험실 |
| `internal/install` | 실행 파일 복사와 회고 예약 |
| `internal/config`, `internal/i18n`, `internal/sqlitedb`, `internal/project` | 설정, 문구, SQLite 열기·하루 백업, 프로젝트 범위 |
| `internal/memory/contract`, `internal/decision/contract` | 읽기 계약 `orbit.memory`와 옛 `orbit.decision` 예시(Orbit Studio 플러그인 시험이 읽는다) |

## 기억 조각 고치기

기억 MCP 서버와 훅의 정본은 `internal/memplugin/fragment.json`이다(`{{mem}}`은 MCP 명령, `{{memcmd}}`는 훅 명령 줄, `{{host}}`는 빌드가 채운다). 스킬·역할·분야 묶음은 OpenOrbit 원본(`onidot-labs/openorbit`의 `packages/harness/source`)이 정본이다. 회고가 낸 원본 변경은 실험실에서 도움됨으로 판정된 것만 사용자 승인으로 간다. 개인·회사 분야 지식은 원본이 아니라 그 PC 기억에 `분야:<이름>` 태그로 둔다.

## 릴리스

`scripts/release.sh`가 `dist/<버전>/`에 darwin arm64·amd64, windows amd64(빌드만) 자산(`orbit-mem-<버전>-<os>-<arch>`)과 `SHA256SUMS`를 만든다. `--publish`는 깨끗한 `origin/main` 끝에서 태그 `v<버전>`으로 GitHub Release를 올린다(노트는 `RELEASE_NOTES` 파일). 서명이 없으므로 내려받은 바이너리는 `xattr -d com.apple.quarantine orbit-mem`으로 격리 속성을 풀고 `orbit-mem install`을 실행한다.

# OpenOrbit

PC마다 깔리는 Go 바이너리 하나(`orb`)다. AI 호스트(Claude Code·Codex)에 이 PC의 기억과 일하는 방법(개발 스킬·역할·스택 에이전트·git 규칙)을 붙이고, 대화 기록으로 회고해 배운 것을 제안하고, 플러그인을 Claude·Codex 두 형식으로 빌드한다. 서버와 상주 프로세스는 없다. 개인 내용 없이 공개할 수 있게 만든다. 버전은 0.x를 쓴다.

기준 설계: Atlas 「설계 · Orbit 1차 — 기억·회고·하네스」, 「설계 · Orbit 개선 — 기억 품질과 자가 개선」(2차 구현 명세는 그 하위).

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `orb install` | `orb`를 `~/.local/bin`(Windows는 `%LOCALAPPDATA%\Programs\orbit`)에 두고(옛 이름 `oni`는 당분간 같은 자리에 `orb`의 별칭으로 둔다), OpenOrbit 플러그인을 이 PC의 경로로 빌드해 두 호스트의 로컬 마켓에 등록하고(Codex 역할 파일은 `~/.codex/agents/orbit-*.toml`로 복사), 회고 예약(launchd·작업 스케줄러)을 건다. `--plugin-source <원본>`을 주면 내장 원본 대신 그 디렉터리로 빌드한다 |
| `orb doctor` | 기억 수, 대기 제안, 호스트 등록, 예약, 최근 회고·백업 |
| `orb mcp [--reader]` | 호스트가 띄우는 MCP 서버. 기억 도구 6개 `memory_find`, `memory_get`, `memory_save`, `memory_update`, `proposals`, `proposal_decide`(`memory_save`·`memory_update`는 관련 기억 연결 `links`·`link`·`unlink`를 받고 `memory_get`은 `links`를 준다)와 작업 도구 `work_list`, `work_get`, `work_title`. `--reader`는 Orbit Studio·orbit-pi 같은 앱 전용으로 `work_list`·`work_get`·`work_sessions`·`work_changes`, 결정 카드 `decision_list`·`decision_get`·`decision_changes`·`decision_open`·`decision_answer`·`decision_withdraw`, 기억 그래프 `memory_graph`를 연다(계약 `internal/work/contract/v1`, `internal/decision/contract/v1`, `internal/memory/contract/v1`) |
| `orb hook session-start\|prompt\|session-end\|work` | 질문 도구(AskUserQuestion·request_user_input)는 결정 카드(`orbit.decision`)로 열고 닫는다. orbit-pi가 띄운 세션(`ORBIT_RUNTIME=orbit-pi`)에서는 플러그인 훅이 아무것도 하지 않고, orbit-pi가 `--runtime orbit-pi --launcher <studio·telegram·workflow>`로 부른 것만 기록한다. 세션 시작에 고정 묶음(확정 규칙 전문·최근 결정 제목·회고 한 줄, 세션 안에서는 바뀌지 않음), 요청마다 강하게 맞는 기억을 `recallChars` 안에서 순위대로(작업 회상), 세션이 끝나면 세션 끝 회고 대기열에 넣고 `orb retro sessions`를 뒤에서 띄운다 |
| `orb retro run` | 세션 끝 회고 대기열을 비우고, 밀린 하루 회고(최대 7일, 근거 파일이 바뀐 기억 판정 포함), 효과 판정, 지난주 주간 회고를 헤드리스 호스트로 돌린다. 예약이 부른다 |
| `orb retro sessions` | 세션 끝 회고 대기열을 비운다(세션 종료 훅이 부른다). 다른 회고가 돌고 있으면 바로 끝난다. 호스트가 로그인되어 있지 않으면(macOS 로그아웃·재시동 중 등) 남은 세션을 대기열에 두고 멈추며, 다음 세션 끝이나 예약 회고가 다시 돌린다 |
| `orb retro legacy` | 옛 Knowledge·Claude 메모리에서 옮겨 온 기억(`imported:*`) 한 묶음(`legacyMemories`, 기본 30)을 대표 태그 클러스터별로 판정한다. 확정·합치기·낡음은 근거와 함께 제안으로, 그대로 둠은 사건으로 남는다. 예약 회고는 하루에 `legacyBatches` 묶음까지 돌리고, 모두 판정하면 멈춘다. 낡음 판정에 쓸 보관한 저장소·옛 이름은 `retiredNames`(쉼표로 구분)에 둔다 |
| `orb lab` | 하네스 실험실. `task new`(작업 지시·판정 기준·시작 상태, `--from-corrections`로 교정 인용을 기준에 붙임)·`task list`, `run --task T --change DIR\|--proposal ID`(지금 플러그인과 고친 플러그인을 `labRuns`번씩 실험 전용 `ORBIT_HOME`에서 `claude -p --restricted`로 돌리고, 결과물을 A·B로 섞어 `labJudgeModel`이 고름. 판정은 원장에 실험 줄로, 도움됨이면 파일 반영 제안으로), `show`·`list`, `candidates`(회고가 낸 하네스 변경 중 실험실 대기). 플러그인 원본(`pluginSource`)을 쓰는 PC에서만 돈다. 과제·결과물은 `ORBIT_HOME/lab/`에만 둔다 |
| `orb proposals` | 회고 제안 보기·하나씩 결정(`review`)·승인·거절·고쳐서 승인 |
| `orb memory find\|show` | 기억 찾기·읽기 |
| `orb migrate --from <사본>` | 옛 Knowledge 백업 사본을 한 번 옮긴다 |
| `orb plugin build <원본>` | 공통 원본에서 Claude·Codex 플러그인과 마켓 파일을 만든다 |
| `orb plugin install <마켓>` | 빌드한 마켓을 두 호스트에 등록하고 Codex 역할 파일을 복사한다 |
| `orb stats [--days N \| --week 월요일] [--json]` | 지표: 반복 교정(넣었는데 어김·넣지 못함), 요청 적중과 평균 글자 수, 사용 판정(따름·어김·상관없음), 빈 검색, 저장, 쓰기 판정, 제안 결정, 근거 파일 판정(감지 수·제안으로 이어진 수), 효과 원장, 회고 종류별 수와 비용, 기억 그래프, 상태별 기억 수, 옛 기억 정리(판정한 수·남은 수) |
| `orb ledger [--all] [--json]` | 승인한 기억·하네스·지침 변경과 효과 판정(도움됨·해로움·효과 없음·섞임) |
| `orb config`, `orb backup`, `orb uninstall` | 설정, 즉시 백업, 등록·예약 해제(데이터는 남김) |

## 언어

`orb`가 사람에게 보이는 출력·오류·도움말은 영어가 기본이고 한국어로 바꿀 수 있다(지원 언어 `en`·`ko`). `orb config set language ko`로 바꾸고, 환경 변수 `ORBIT_LANG=en|ko`가 있으면 설정보다 먼저 따른다. `orb doctor`가 지금 언어를 보인다. JSON 출력(`--json`)과 MCP 응답 구조, AI가 읽는 글(MCP 도구 설명, 세션 주입, 회고 프롬프트·재료, 하네스 스킬·역할)과 기억·제안 본문은 언어 설정과 무관하다. 문구는 `internal/i18n/locales/<언어>/*.json`에 키로 두고, 언어마다 같은 키와 같은 서식 자리를 갖는다(시험으로 확인).

Codex는 플러그인 훅을 사용자가 `/hooks`에서 신뢰해야 돌리고, 훅이 바뀌면 신뢰가 풀린다. 새 판을 `orb install`로 깐 뒤에는 Codex에서 `/hooks`를 열어 OpenOrbit 훅을 다시 신뢰한다. 신뢰가 없으면 기억 문맥과 작업 기록이 Codex에서 돌지 않는다.

## 데이터

- 위치: `ORBIT_HOME`, 없으면 Mac `~/Library/Application Support/orbit`, Windows `%AppData%\orbit`.
- `memory.db`(SQLite, 기억·제안·회고·지표용 사건·쓰기 판정·효과 원장·세션 끝 회고 대기열), `config.json`, `backups/`(매일 사본, 기본 14개, 옮기기 전 사본은 따로), `plugin/`(빌드한 OpenOrbit 플러그인 마켓), `retro/`(진행 중 회고 재료), `logs/`.
- PC끼리 공유하지 않는다.

## 개발

```bash
go test ./...
go vet ./...
GOOS=windows GOARCH=amd64 go build -o dist/orb.exe ./cmd/orb
go build -o dist/orb ./cmd/orb
```

## 구조

| 패키지 | 내용 |
| --- | --- |
| `internal/memory` | 기억 DB, 한글 검색 점수(옛 Knowledge 규칙), 후보·확정·낡음·숨김, 출처, 버전 이력, 제안, 쓰기 판정, 효과 원장(기록·판정·되돌리기) |
| `internal/inject` | 훅이 넣는 세션 고정 묶음·작업 회상과 넣은 기억 id |
| `internal/metrics` | 사건(`events` 표)·회고·제안으로 지표 계산. 새 지표는 사건 종류를 더한다 |
| `internal/mcpserver` | MCP 도구 6개, 작업·결정 카드·기억 그래프 계약 도구, 회고 전용 도구 |
| `internal/work` | 작업 기록(`work.db`): 훅 사건 원장과 그래프, 결정 카드(`orbit.decision`) |
| `internal/decision/contract` | 결정 카드 계약 |
| `internal/transcript` | Claude·Codex 대화 기록에서 사용자 말과 AI 답만 추리기 |
| `internal/retro` | 세션 끝·하루·주간 회고, 대기열과 읽은 발화 구간, 밀린 회고 따라잡기, 흡수, 주간 정리 재료, 제출 반영(쓰기·사용 판정, 교훈) |
| `internal/migrate` | 옛 Knowledge 이전 |
| `internal/lab` | 하네스 실험실: 과제, 후보 빌드·실행, 익명 고르기, 원장 실험 줄과 제안 |
| `internal/pluginbuild` | 두 호스트 플러그인 빌드 |
| `internal/orbitplugin/source` | OpenOrbit 플러그인 원본: MCP·훅, 스킬 15개(memory, proposals, tdd, spec-first, debugging, plan, verify, frontend, plain-writing, explore-domain, research, review, delegate, git-commit, pull-request), 역할 17개와 스택 에이전트 6개(`agents/`), 공통 규칙(`agents-common.md`), 분야 묶음(`knowledge/<분야>/`: 한 줄 신호 목록 `SIGNALS.md`와 항목. 에이전트에는 일하는 방법만 두고 분야 사실은 여기에) |
| `internal/install` | 바이너리·플러그인 등록·Codex 역할 복사·OS 예약 |
| `internal/i18n` | 사람이 읽는 문구 카탈로그(en·ko)와 언어 고르기(`language` 설정, `ORBIT_LANG`) |

## 플러그인 원본 고치기

스킬·역할·분야 묶음은 `internal/orbitplugin/source`가 정본이다. 회고가 낸 원본 변경은 실험실(`orb lab`)에서 도움됨으로 판정된 것만 사용자 승인으로 간다. 개인·회사 분야 지식은 원본이 아니라 그 PC 기억에 `분야:<이름>` 태그로 둔다(요청과 맞으면 신호만 붙는다). 고친 뒤 `go build`로 바이너리를 다시 만들거나, 이 PC에서는 `orb install --plugin-source internal/orbitplugin/source`로 내장 원본 대신 이 디렉터리를 쓰게 한다. 원본에 특정 서비스·개인 프로젝트·개인 경로를 넣지 않는다. 플러그인 버전은 `internal/version`의 orb 버전과 같다.

## 릴리스

`scripts/release.sh`가 `dist/<버전>/`에 darwin arm64·amd64, windows amd64(빌드만) 자산과 `SHA256SUMS`를 만든다. `--publish`는 깨끗한 `origin/main` 끝에서 태그 `v<버전>`으로 GitHub Release를 올린다(노트는 `RELEASE_NOTES` 파일). 서명이 없으므로 내려받은 바이너리는 `xattr -d com.apple.quarantine orb`로 격리 속성을 풀고 `orb install`을 실행한다. 버전은 0.x를 쓴다.

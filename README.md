# Orbit

PC마다 깔리는 Go 바이너리 하나(`oni`)다. AI 호스트(Claude Code·Codex)에 이 PC의 기억과 일하는 방법(개발 스킬·역할·스택 에이전트·git 규칙)을 붙이고, 대화 기록으로 회고해 배운 것을 제안하고, 플러그인을 Claude·Codex 두 형식으로 빌드한다. 서버와 상주 프로세스는 없다. 개인 내용 없이 공개할 수 있게 만든다. 버전은 0.x를 쓴다.

기준 설계: Wiki 「설계 · Orbit 1차 — 기억·회고·하네스」.

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `oni install` | `oni`를 `~/.local/bin`(Windows는 `%LOCALAPPDATA%\Programs\orbit`)에 두고, Orbit 플러그인을 이 PC의 경로로 빌드해 두 호스트의 로컬 마켓에 등록하고(Codex 역할 파일은 `~/.codex/agents/orbit-*.toml`로 복사), 회고 예약(launchd·작업 스케줄러)을 건다. `--plugin-source <원본>`을 주면 내장 원본 대신 그 디렉터리로 빌드한다 |
| `oni doctor` | 기억 수, 대기 제안, 호스트 등록, 예약, 최근 회고·백업 |
| `oni mcp` | 호스트가 띄우는 MCP 서버. 도구 6개: `memory_find`, `memory_get`, `memory_save`, `memory_update`, `proposals`, `proposal_decide` |
| `oni hook session-start\|prompt` | 세션 시작에 확정 규칙 전문·최근 결정 제목·회고 한 줄, 요청마다 강하게 맞는 기억 최대 3건 |
| `oni retro run` | 밀린 하루 회고(최대 7일)와 지난주 주간 회고를 헤드리스 호스트로 돌린다. 예약이 부른다 |
| `oni proposals` | 회고 제안 보기·하나씩 결정(`review`)·승인·거절·고쳐서 승인 |
| `oni memory find\|show` | 기억 찾기·읽기 |
| `oni migrate --from <사본>` | 옛 Knowledge 백업 사본을 한 번 옮긴다 |
| `oni plugin build <원본>` | 공통 원본에서 Claude·Codex 플러그인과 마켓 파일을 만든다 |
| `oni plugin install <마켓>` | 빌드한 마켓을 두 호스트에 등록하고 Codex 역할 파일을 복사한다 |
| `oni stats [--days N \| --week 월요일] [--json]` | 지표: 반복 교정(넣었는데 어김·넣지 못함), 요청 적중, 빈 검색, 저장, 제안 결정, 회고 |
| `oni config`, `oni backup`, `oni uninstall` | 설정, 즉시 백업, 등록·예약 해제(데이터는 남김) |

## 데이터

- 위치: `ORBIT_HOME`, 없으면 Mac `~/Library/Application Support/orbit`, Windows `%AppData%\orbit`.
- `memory.db`(SQLite, 기억·제안·회고·지표용 사건), `config.json`, `backups/`(매일 사본, 기본 14개, 옮기기 전 사본은 따로), `plugin/`(빌드한 Orbit 플러그인 마켓), `retro/`(진행 중 회고 재료), `logs/`.
- PC끼리 공유하지 않는다.

## 개발

```bash
go test ./...
go vet ./...
GOOS=windows GOARCH=amd64 go build -o dist/oni.exe ./cmd/oni
go build -o dist/oni ./cmd/oni
```

## 구조

| 패키지 | 내용 |
| --- | --- |
| `internal/memory` | 기억 DB, 한글 검색 점수(옛 Knowledge 규칙), 후보·확정·낡음·숨김, 출처, 버전 이력, 제안 |
| `internal/inject` | 훅이 넣는 세션 시작·요청 문맥과 넣은 기억 id |
| `internal/metrics` | 사건(`events` 표)·회고·제안으로 지표 계산. 새 지표는 사건 종류를 더한다 |
| `internal/mcpserver` | MCP 도구 6개와 회고 전용 도구 |
| `internal/transcript` | Claude·Codex 대화 기록에서 사용자 말과 AI 답만 추리기 |
| `internal/retro` | 하루·주간 회고, 밀린 회고 따라잡기, 흡수, 제출 반영 |
| `internal/migrate` | 옛 Knowledge 이전 |
| `internal/pluginbuild` | 두 호스트 플러그인 빌드 |
| `internal/orbitplugin/source` | Orbit 플러그인 원본: MCP·훅, 스킬 15개(memory, proposals, tdd, spec-first, debugging, plan, verify, frontend, plain-writing, explore-domain, research, review, delegate, git-commit, pull-request), 역할 17개와 스택 에이전트 6개(`agents/`), 공통 규칙(`agents-common.md`) |
| `internal/install` | 바이너리·플러그인 등록·Codex 역할 복사·OS 예약 |

## 플러그인 원본 고치기

스킬·역할은 `internal/orbitplugin/source`가 정본이다. 고친 뒤 `go build`로 바이너리를 다시 만들거나, 이 PC에서는 `oni install --plugin-source internal/orbitplugin/source`로 내장 원본 대신 이 디렉터리를 쓰게 한다. 원본에 특정 서비스·개인 프로젝트·개인 경로를 넣지 않는다. 플러그인 버전은 `internal/version`의 oni 버전과 같다.

## 릴리스

`scripts/release.sh`가 `dist/<버전>/`에 darwin arm64·amd64, windows amd64(빌드만) 자산과 `SHA256SUMS`를 만든다. `--publish`는 깨끗한 `origin/main` 끝에서 태그 `v<버전>`으로 GitHub Release를 올린다(노트는 `RELEASE_NOTES` 파일). 서명이 없으므로 내려받은 바이너리는 `xattr -d com.apple.quarantine oni`로 격리 속성을 풀고 `oni install`을 실행한다. 버전은 0.x를 쓴다.

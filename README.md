# orbit-mem

저장소 `onidot-labs/orbit-mem`이다. 이 PC의 AI 호스트(Claude Code·Codex)가 함께 쓰는 공유 기억 시스템 `orbit-mem`(Go 실행 파일 하나)을 담는다. 기억과 그 출처, 회고가 남긴 일지·후보·제안을 받아 두는 저장 API, 호스트 세션의 활동 기록, 그리고 이것들을 호스트에 붙이는 MCP 서버와 훅이다. 서버와 상주 프로세스는 없다.

회고(하루·주간·세션 끝·작업·그래프 정리·옛 기억 판정)는 OpenOrbit이 돌린다: `orb retro …`, 예약은 `orb install`이 등록한다. `orbit-mem`은 모델이나 헤드리스 호스트를 부르지 않고(예외는 실험실 `orbit-mem lab`, ONID-124에서 빠진다), 회고의 저장 쪽만 맡는다: 기계용 명령 `orbit-mem retro-store`(재료 준비·마침·대기열·상태)와 회고용 MCP 도구(`orbit-mem mcp --retro ID`). 그래서 `orbit-mem`만 깐 PC에서는 회고가 돌지 않지만, 일하는 동안 `memory_save` 등으로 배운 것을 남기고 회상하는 일은 그대로 된다. 결정: 2026-11-01 「orbit-mem은 기억 기능을 제공하는 시스템, 회고 기능은 openorbit」(Atlas 계획 · AU-0). 개인 내용 없이 공개할 수 있게 만든다. 버전은 0.x를 쓴다.

`orbit-mem`은 자기 Claude Code·Codex 플러그인 `orbit-mem`(기억 MCP 서버, 훅 셋, 스킬 `memory`·`proposals`)을 Go로 직접 빌드하고 호스트에 등록한다. OpenOrbit 없이 혼자 설치된다. 이 저장소 자체가 Claude Code 플러그인 마켓이다(아래 「설치」). 구조: Atlas 설계 · OpenOrbit 4차 「플러그인과 설치 — 역할과 의존」.

## 세 플러그인 중 이 저장소의 자리

| 플러그인 | 저장소 | 담는 것 | 필요한 것 |
| --- | --- | --- | --- |
| 하네스 `orbit` | `onidot-labs/openorbit` | 역할·스킬(일하는 방법). MCP·훅 없음 | 없음 |
| **`orbit-mem`** | **이 저장소** | 기억 MCP 서버 `orbit-mem`, 세션 시작·요청·세션 끝 훅(기억 주입·활동 기록), 스킬 `memory`·`proposals`, 회고 저장 API `retro-store` | PATH의 `orbit-mem` 실행 파일 |
| OpenOrbit 확장 | `onidot-labs/openorbit` | 오케스트라·작업 현황, 회고 실행(`orb retro`)과 그 예약(`orb install`) | OpenOrbit `orb` |

`orbit-mem`의 기억 플러그인·MCP·훅은 OpenOrbit 없이 돈다. 예외는 둘이다. 실험실(`orbit-mem lab`)은 후보 하네스를 빌드할 때 OpenOrbit의 `orb plugin build`를 부른다(실험실을 어디 둘지는 다음에 정한다). 훅은 orbit-pi가 부를 때 붙는 `--runtime orbit-pi` 표시를 안다. 하네스와는 서로를 모른다. 하네스 빌드에 기억 조각을 끼우던 방식(`orbit-mem plugin fragment`, `orb plugin build --fragment`)은 걷어 냈다.

## 설치

단독 설치: 릴리스에서 실행 파일을 받아(아래 「릴리스」) 한 번 실행한다.

```bash
./orbit-mem install            # ~/.local/bin/orbit-mem, 설정·기억 DB, 호스트 플러그인 등록(예약은 하지 않음)
orbit-mem doctor               # 상태와 문제마다 고칠 명령 한 줄
```

`install`이 호스트에 하는 일(호스트가 PATH에 있을 때만):

1. Codex가 있거나 `--local-plugin`이면 내장 원본을 `$ORBIT_HOME/plugins/orbit-mem`(로컬 마켓 폴더: Claude·Codex 꼴과 두 마켓 파일)에 빌드한다.
2. Claude Code: `claude plugin marketplace list --json`으로 마켓 `orbit-mem`을 보고, 없으면 `claude plugin marketplace add onidot-labs/orbit-mem`(`--local-plugin`이면 로컬 폴더), 같은 출처로 있으면 `claude plugin marketplace update orbit-mem`, 다른 출처면 `remove` 뒤 `add`. 이어서 `claude plugin list --json`에 없으면 `claude plugin install orbit-mem@orbit-mem --scope user`, 있으면 `claude plugin update orbit-mem@orbit-mem`.
3. Codex: 언제나 로컬 폴더 마켓이다. `codex plugin marketplace add $ORBIT_HOME/plugins/orbit-mem` → `codex plugin add orbit-mem@orbit-mem`(다시 해도 된다). Codex는 신뢰한 훅만 돌리므로 Codex에서 `/hooks`를 열어 `orbit-mem` 훅을 신뢰한다(훅이 바뀌면 다시).

`install`은 아무것도 예약하지 않는다. 0.6.0 전 `orbit-mem install`이 등록한 회고 예약(launchd `dev.onidot.orbit.retro`, Windows 작업 `Orbit Retro`)이 이 `orbit-mem`(같은 `ORBIT_HOME`, 없으면 기본 위치)의 것이면 지운다. 실행 파일이 `orbit-mem`이 아닌 예약(OpenOrbit `orb`가 등록한 것)과 다른 `ORBIT_HOME`의 예약은 그대로 둔다. `--no-schedule`이면 OS 예약을 아예 건드리지 않는다. `--no-hosts`는 호스트를 건드리지 않고, `--dry-run`은 무엇을 할지만 보인다. `orbit-mem uninstall`은 같은 옛 회고 예약과 두 호스트의 `orbit-mem@orbit-mem`·마켓 `orbit-mem`을 뺀다(있는 것만). 기억 DB·활동 기록·백업·로컬 플러그인 폴더는 남긴다.

Claude Code의 `/plugin`에서 GitHub 마켓 `onidot-labs/orbit-mem`을 더해 플러그인만 깔 수도 있다. 이때 실행 파일이 PATH에 없으면 세션 시작 훅이 설치 안내(「orbit-mem 실행 파일이 PATH에 없습니다. … `orbit-mem install`을 실행하세요.」)를 문맥으로 내고, 요청·세션 끝 훅은 조용히 넘어간다. 실행 파일과 플러그인의 판이 다르면 세션 시작 문맥 끝에 `orbit-mem install`로 맞추라는 한 줄이 붙는다(훅이 `--plugin-version`을 넘긴다).

훅과 MCP 서버는 절대경로를 박지 않고 PATH의 `orbit-mem`을 부른다. 훅 명령은 sh 꼴이다(`if command -v orbit-mem >/dev/null 2>&1; then orbit-mem hook … --host … --plugin-version …; else …; fi`). 맥·리눅스에서 시험했고 Windows는 확인하지 않았다. 호스트를 Dock·Finder에서 띄우면 셸의 PATH를 받지 못할 수 있으니 `orbit-mem doctor`의 `orbit-mem on PATH`로 확인한다.

이력: 2026-11-01까지 이 저장소의 실행 파일은 OpenOrbit 명령 `orb`(4차 동안 별칭 `oni`)였고, 작업 기록(`work.db`, 훅 번역·결정 카드), orbit-pi 런타임·데몬·TUI 실행, OpenOrbit TS CLI 다리를 함께 담았다. N4-2 차례 5(ONID-121)에서 그것들을 걷어 내고 `orbit-mem` 0.4.0이 됐다. 옛 `work.db`는 지우지 않고 읽기 전용으로 활동 기록 찾기에 보이며 하루 백업도 이어 간다.

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `orbit-mem install [--bin-dir D] [--local-plugin] [--no-hosts] [--no-schedule] [--dry-run]` | `orbit-mem`을 `~/.local/bin`(Windows는 `%LOCALAPPDATA%\Programs\orbit`)에 두고, 설정(`claudePath`·`codexPath`, `--plugin-source <원본>`, `--apply-target <경로>`)과 기억 DB를 만들고, 예전 `orbit-mem`이 등록한 회고 예약이 있으면 지운 뒤(`--no-schedule`이면 건드리지 않음) 플러그인을 호스트에 등록한다(위 「설치」). 예약은 하지 않는다 |
| `orbit-mem uninstall` | 예전 `orbit-mem`이 등록한 회고 예약과 호스트의 플러그인·마켓을 뺀다. 기억 DB·활동 기록·백업·로컬 플러그인 폴더는 남긴다 |
| `orbit-mem doctor [--json]` | 실행 파일 경로·판, PATH의 `orbit-mem`, 데이터 경로·기억 수·대기 제안·세션 끝 회고 대기열·활동 기록 수, Claude Code의 `orbit-mem@orbit-mem` 설치·판·마켓, Codex 등록·훅 신뢰, 문제마다 고칠 명령 한 줄. `--json` 키는 아래 「doctor --json」 |
| `orbit-mem search <질의> [--kind K]… [--all] [--stale] [-n N]` | 기억 찾기(`memory_find`와 같은 함수) |
| `orbit-mem show <id> [--history]` | 기억 하나 읽기(`memory_get`) |
| `orbit-mem list [--kind K]… [--state S]… [--tag T]… [--all] [-n N]` | 최근 기억 목록(질의 없는 `memory_find`, 기본은 후보·확정) |
| `orbit-mem edit <id> [--title] [--body \| --body-file F] [--tag T]… [--link ID]… [--unlink ID]… [--note]` | 기억 고치기(`memory_update`, 판이 오르고 이력이 남는다) |
| `orbit-mem stale <id> [--note]` | 낡음으로 표시(`memory_update` action=stale, 지우지 않는다) |
| `orbit-mem export [--format jsonl\|md] [--out 경로] [--all]` | 사람이 읽고 옮길 수 있게 내보낸다. jsonl은 한 줄에 기억 하나(출처·나온 날·연결 포함), `--out`이 없으면 stdout. md는 폴더에 `<id>.md`와 `index.md`. 기본은 숨김 빼고 전부, `--all`은 숨김(거절한 후보)까지 |
| `orbit-mem import <jsonl> [--dry-run]` | export한 jsonl을 들여온다. 아래 「들여오기 규칙」 |
| `orbit-mem activity [질의] [--host H] [--project all\|DIR] [--since 날짜] [-n N]` | 활동 기록 찾기(`activity_find`) |
| `orbit-mem proposals` | 사람의 결정을 기다리는 제안 보기·하나씩 결정(`review`)·승인·거절·고쳐서 승인 |
| `orbit-mem audit [--days N] [--json]`, `audit revert <원장·제안 id> [--note]`, `audit dismiss <f-…\|correction:…>`, `audit harness [--json]` | 감사 목록(아래 「확정 규칙과 감사 목록」): 최근 자동 확정·자동 정리, 어긋남, 기준을 넘은 반복 교정. 틀린 것만 되돌리거나 확인함으로 닫는다. `harness --json`은 하네스 개선 후보(스킬·역할 반복 교정, 서로 다른 작업 여럿에서 나온 절차 교훈)를 openorbit 실험실(개발 도구)이 읽기만 하는 꼴로 준다 |
| `orbit-mem retro-store <op>` | OpenOrbit `orb retro`가 부르는 회고 저장 API(기계용). 결과는 stdout에 JSON 객체 하나, 실패는 종료 1과 stderr 한 줄, 기억 DB가 없으면 만들지 않는다. `prepare --kind daily\|weekly --period YYYY-MM-DD`·`--kind tidy\|legacy`·`--kind session --session-host H --session S --ended-at MS [--daily-max N]`·`--kind work`(stdin의 끝난 작업 기록 JSON 하나 `{ref,cwd,title,status,endedAt,text,userWords,evidence,recalled,verified}`, 4MB까지. 같은 작업의 같은 `endedAt`이 실행 중이거나 끝났으면 `skipped`(실행·제출 중으로 45분이 지난 줄은 실패로 닫고 다시 돈다), `text`가 비면 `empty`)(재료를 `retro/<id>/material.json`에 두고 실행 줄을 연다, `--host` 기본 `retroHost`), `finish --id ID`(stdin의 호스트 결과 JSON으로 재료를 지우고 사건 `retro.run`을 남기며, 제출이 없으면 실패로 닫는다), `queue list\|add\|mark`(세션 끝 회고 대기열), `state`, `list [--n N]`, `sessions --day D`, `backup`(세션 시작 훅과 같은 하루 규칙: 마지막 기억 사본이 24시간 안이면 새로 만들지 않고 그 사본 경로를 준다), `judge`(효과 판정·해로운 자동 변경 되돌리기), `settle`(증거 확정·정착한 제안 닫기·오래된 세션 상태 정리) |
| `orbit-mem mcp [--actor ai\|user\|automation] [--retro ID \| --reader]` | 호스트가 띄우는 MCP 서버(stdio). 아래 「MCP 도구」. `--actor`는 이 서버로 쓰는 쪽(기본 ai, Orbit Studio는 user) |
| `orbit-mem hook session-start\|prompt\|session-end --host claude\|codex [--plugin-version V]` | 호스트 훅(stdin JSON). 세션 시작에 고정 묶음(확정 규칙 전문·최근 결정 제목·회고 한 줄, 세션 안에서는 바뀌지 않음), 요청마다 강하게 맞는 기억을 `recallChars` 안에서 준다. 셋 다 활동 기록을 남기고, 세션 끝 훅은 활동 기록의 끝 시각만 남긴다(세션 끝 회고 대기열은 OpenOrbit이 채운다). 세션 시작 훅은 마지막 기억 사본이 24시간보다 오래됐으면 `orbit-mem backup auto`를 따로 띄우고 기다리지 않는다(아래 「백업」). 어떤 경우에도 호스트를 실패시키지 않는다(종료 0). `--plugin-version`이 자기 판과 다르면 세션 시작 문맥 끝에 맞추라는 한 줄을 더한다(다른 훅은 무시). `hook work`(옛 플러그인의 작업 기록 훅)는 입력을 버리고 아무것도 하지 않는다 |
| `orbit-mem inject session-start\|prompt [--cwd D] [--session S]` | orbit-pi에 훅과 같은 기억 문맥을 JSON(`text`·`memoryIds`)으로 준다. 요청 글은 stdin |
| `orbit-mem plugin build [--out D \| --repo R]` | 내장 원본으로 플러그인을 만든다. `--out`(기본 `$ORBIT_HOME/plugins/orbit-mem`)에는 Claude·Codex 꼴과 두 로컬 마켓을, `--repo`에는 GitHub 마켓(`.claude-plugin/marketplace.json`과 `plugin/`, Claude 꼴만)을 쓴다. `plugin/` 안의 남은 파일은 지운다 |
| `orbit-mem migrate --from <사본>` | 옛 Knowledge 백업 사본을 한 번 옮긴다 |
| `orbit-mem stats [--days N \| --week 월요일]` | 지표: 반복 교정, 요청 적중, 사용 판정, 빈 검색, 저장, 쓰기 판정, 제안 결정, 근거 파일 판정, 효과 원장, 회고 비용, 기억 그래프, 옛 기억 정리 |
| `orbit-mem ledger [--all]` | 승인한 변경과 자동 변경(기억·하네스·지침)의 효과 판정 |
| `orbit-mem lab` | 하네스 실험실. 지금 플러그인과 고친 플러그인을 `labRuns`번씩 실험 전용 `ORBIT_HOME`에서 돌리고 결과물을 익명으로 고른다. 후보 플러그인은 OpenOrbit `orb plugin build <원본> --out <폴더>`로 하네스 플러그인만 빌드한다(`orb`는 `ORBIT_ORB`, PATH, `~/.local/bin/orb` 순으로 찾는다). 실행은 실험 전용 빈 `ORBIT_HOME`과 `--setting-sources project`로 돌아 이 PC에 깔린 플러그인(`orbit-mem` 포함)을 싣지 않으므로 기억은 비교에 끼지 않는다. 플러그인 원본(`pluginSource`)을 쓰는 PC에서만 돈다 |
| `orbit-mem backup [list\|status\|restore <사본> [--yes]]` | 지금 사본 만들기(하루 규칙과 관계없이, 기억 DB와 옛 `work.db`), 사본 목록, 상태, 복구. 아래 「백업」 |
| `orbit-mem config`, `orbit-mem version` | 설정 보기·바꾸기, 판 |

## 얇은 CLI의 출력

`search`·`show`·`list`·`edit`·`stale`·`activity`·`import`는 MCP 도구와 같은 함수를 부른다. stdout이 터미널이면 사람이 읽는 표·글을, 파이프나 파일이면 JSON(도구 응답과 같은 구조)을 낸다. `--json`이면 언제나 JSON이다. 이 명령은 AI 도구 사용 사건(`tool.*`)으로 세지 않는다.

들여오기 규칙(`import`): 줄마다 기억 하나를 읽어 같은 id·종류·범위·상태·제목·본문·태그·시각·출처·나온 날로 넣고, 판 이력에 `import`(사유 `import: <파일 이름>`) 한 줄을 남긴다. 이미 있는 id(옛 Knowledge id가 같은 것 포함)는 그대로 두고 `exists`로, 같은 종류·범위·제목의 숨기지 않은 기억이 있으면 `duplicate`로, 깨진 줄이나 틀린 값은 `invalid`로 건너뛰고 줄 번호와 함께 알린다. 양쪽 기억이 다 있게 된 연결은 다시 잇는다. 쓰기 전에 `backups/before-import-*.sqlite` 사본을 만든다. `--dry-run`은 쓰지 않고 결과만 보인다.

## 활동 기록

순수 Claude Code·Codex 세션은 작업 현황 없이 활동 기록만 남긴다: 언제·어느 호스트·어느 프로젝트에서 무엇을 요청했는지다. `memory.db`의 `activity` 표(스키마 판 `user_version` 3부터, 지금 4)에 세션 하나가 한 줄이다.

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

- 기억 6개: `memory_find`, `memory_get`, `memory_save`, `memory_update`, `proposals`, `proposal_decide`. `memory_save`·`memory_update`는 관련 기억 연결 `links`·`link`·`unlink`를 받고 `memory_get`은 `links`를 준다. `memory_save`는 `conflictsWith`(어긋나는 사용자 규칙 id)와 출처 `verification`을, `memory_update`는 `userQuote`(사용자 인용 기억을 바꾸라는 사용자 말)를 받는다. `proposals`는 감사·교정용이다: `status=audit`이면 감사 목록(`audit`)을, 기본은 대기 제안을 준다. `proposal_decide`는 `approve`·`reject` 말고도 `revert`(자동 변경 되돌리기)·`dismiss`(어긋남·반복 교정 확인함)를 받는다.
- 쓰는 쪽(actor)은 도구 인자가 아니라 서버가 정한다: `--actor`, 없으면 환경 `ORBIT_ACTOR`, 회고 실행 안(`ORBIT_RETRO_ID`)이면 언제나 `automation`, 기본 `ai`. Orbit Studio 플러그인은 `--actor user`로 띄운다.
- 활동 기록: `activity_find` — 입력 `query`·`host`·`project`(비우면 이 프로젝트, `all`이면 전부, 아니면 프로젝트 디렉터리 절대경로)·`since`(`YYYY-MM-DD`, RFC 3339, `7d`·`12h`)·`limit`(기본 20, 최대 100). 출력 `{"items":[{source, host, session, project, projectScope, cwd, launcher, parent, startedAt, lastAt, endedAt, prompts, firstPrompt, lastPrompt, works}], "legacyError"?}`.
- `--reader`: Orbit Studio 같은 앱 전용으로 기억 그래프 `memory_graph`만 연다(계약 `orbit.memory`, `internal/memory/contract/v1`).
- `--retro ID`: 그 회고 실행의 도구만 연다.

## 확정 규칙과 감사 목록

2026-11-01 사용자 결정 A2(Atlas 설계 · OpenOrbit 자율 완수와 학습 「확정 규칙」, 계획 · AU-6). 규칙은 저장 계층(`internal/memory`, MCP, CLI)에 있어, 회고든 다른 자동화든 이 API를 거치면 같은 규칙을 지킨다. 옛 「승인 없는 자동 쓰기 금지」·「확정은 인용이나 승인으로만」을 대신한다.

- 사용자 인용 보호: 출처에 사용자 말(`quote`)이 있는 기억은 사람(`user`)이 아니면 고치기·확정·낡음·대체·합치기·(자동화의) 연결·태그를 하지 못한다(`ErrProtected`). 사용자 말(`userQuote`)을 주면 되고, 그 말이 새 출처로 남는다.
- 증거 확정: 후보는 회상에 쓰되 확정 뒤에 두고 「후보」로 표시한다. 증거가 하나라도 있으면 스스로 확정하고 원장에 남긴다. ① 서로 다른 날 다시 나옴(`confirmSeenDays`, 기본 2) ② 회상돼 따랐고 그 일이 검증을 통과했으며 그 세션에 교정·어김이 없는 세션 수(`confirmFollowedVerified`, 기본 2) ③ 작업 검증이 직접 확인한 프로젝트 사실(출처 `verification`, 종류 `fact`, 범위 프로젝트). 열린 어긋남 표시가 있거나 사람이 자동 확정을 되돌린 기억은 다시 자동 확정하지 않는다.
- 자동 변경: 자동화의 확정·합치기·낡음·대체(`supersede`)·연결·태그는 승인 없이 적용되고, 제안 표에 상태 `auto`로, 효과 원장에 한 줄로 남는다(`SubmitChange`·`AutoApply`). 고치기·재분류·파일 반영·방향, 사용자 인용 기억에 닿는 정리, 증거 없는 확정은 대기 제안이다. 사람이 거절한 제안과 되돌린 자동 변경은 자동화가 다시 하지 않는다. 효과 판정이 「해로움」인 자동 변경은 회고 따라잡기(`retro-store judge`)가 되돌리고(`RevertHarmfulAuto`) 사건 `audit.auto_reverted`와 세션 시작 한 줄로 알린다.
- 반복 교정: 교정 사건에 대상(`memory`·`skill`·`role`)을 둔다. 대상마다 교정이 나온 세션 수가 `correctionRepeatSessions`(기본 3) 이상이면 감사 목록에 보이고, 스킬·역할이면 하네스 개선 후보다. 확인함을 누르면 그 뒤 교정만 다시 센다.
- 작업 회고(AU-5): OpenOrbit이 끝난 작업의 기록을 넘기면(`retro-store prepare --kind work`) 그 기록 하나를 회고한다. 남는 기억의 출처는 `work`(ref = 작업 id)이고, 사용자 말에 글자 그대로 있는 인용은 `quote`로 확정된다. 그 작업 프로젝트의 사실(`fact`)의 `evidence`가 그 작업의 검증 증거에 그대로 있으면 출처 `verification`이 붙어 증거 ③으로 확정된다. `links`는 자동 연결로 원장에 남는다. 태그 `procedure`(「절차」)인 교훈이 서로 다른 작업 `correctionRepeatSessions`개 이상에서 나오면(출처 `work`의 ref 수, 확인함 뒤만) 하네스 개선 후보(`correction:procedure:<기억 id>`)다.
- 기준 횟수 `confirmFollowedVerified`·`correctionRepeatSessions`는 E2로 이 PC 기억 DB 사본(2026-10-26~11-01)을 재서 정한 추천값이며 사용자 결정 대기다(근거는 계획 · AU-6). `orbit-mem config set <키> <1-30>`으로 바꾼다.
- 사람의 결정: 사람이 아닌 쪽(AI)이 사용자 인용 기억에 닿는 대기 제안을 승인·거절하거나 어긋남 표시를 닫으려면 `userQuote`가 있어야 한다. 사람이 확정 기억을 후보로 내리면 보류(`hold`)로 남아 자동화가 다시 확정하지 않는다(사람이 확정하면 풀린다).
- 표: `audit_flags`(어긋남 `conflict`·보류 `hold`), `audit_acks`(반복 교정 확인함), 색인 `events_kind`. 스키마 판 4.
- 한계: actor는 서버·명령을 띄운 쪽이 정한다. 호스트 AI가 셸로 `orbit-mem edit`를 직접 부르면 사람(user)으로 쓰인다. 대화 중 AI가 새 기억을 저장하며 사용자 인용 기억에 잇는 연결은 기억 내용을 바꾸지 않아 막지 않는다. 출처 `quote`·`verification`은 쓰는 쪽이 붙이는 표시이므로, 스킬·MCP 안내가 「사용자 말 그대로」「실제로 돌린 검증」에만 쓰라고 한다(`verification`은 `ref`·`excerpt`가 있어야 증거다). Studio 플러그인 0.2.0은 `--actor`를 아는 `orbit-mem` 0.5.0 이상이 필요하다(`orbit-mem install`이 함께 올린다).

## 언어

`orbit-mem`이 사람에게 보이는 출력·오류·도움말은 영어가 기본이고 한국어로 바꿀 수 있다(`en`·`ko`). `orbit-mem config set language ko`로 바꾸고, `ORBIT_LANG=en|ko`가 있으면 설정보다 먼저 따른다. JSON 출력과 MCP 응답 구조, AI가 읽는 글(MCP 도구 설명, 세션 주입), 기억·제안 본문은 언어 설정과 무관하다. 문구는 `internal/i18n/locales/<언어>/*.json`에 키로 두고, 언어마다 같은 키와 같은 서식 자리를 갖는다(시험으로 확인).

Codex는 플러그인 훅을 사용자가 `/hooks`에서 신뢰해야 돌리고, 훅이 바뀌면 신뢰가 풀린다. 새 플러그인을 깐 뒤에는 Codex에서 `/hooks`를 열어 다시 신뢰한다.

## 데이터

- 위치: `ORBIT_HOME`, 없으면 Mac `~/Library/Application Support/orbit`, Windows `%AppData%\orbit`.
- `memory.db`(SQLite: 기억·제안(자동 변경 포함)·회고·지표용 사건·쓰기 판정·효과 원장·세션 끝 회고 대기열·활동 기록·감사 표시), `config.json`, `backups/`(아래 「백업」), `retro/`(진행 중 회고 재료: `prepare`가 만들고 `finish`가 지운다), `logs/`, `lab/`(실험실).
- `work.db`: 옛 작업 기록. 읽기 전용으로 활동 기록 찾기에 보이고 하루 백업만 이어 간다. 새로 쓰지 않는다.
- 옛 설정 키 `work`·`workStaleAfter`·`workTestCommands`는 읽을 때 무시하고 다시 저장하면 빠진다.
- PC끼리 공유하지 않는다.

## 백업

`backups/`에 기억 DB 사본을 둔다. 사본은 SQLite `VACUUM INTO`로 만든다: 읽기 트랜잭션 하나의 일관된 모습을 임시 이름(`*.tmp`)에 쓰고 다 쓴 뒤에 이름을 바꾸므로, 쓰는 쪽(훅·MCP)을 막지 않고 덜 쓴 사본이 이름을 얻지 않는다.

| 사본 | 언제 | 정리 |
| --- | --- | --- |
| `memory-YYYYMMDD.sqlite`(하루 사본)와 옛 `work-YYYYMMDD.sqlite` | 세션 시작 훅이 마지막 하루 기억 사본이 24시간보다 오래됐을 때(사본이 없을 때 포함) 뒤에서, OpenOrbit `orb retro` 앞(`retro-store backup`, 같은 규칙), 사람이 `orbit-mem backup`을 실행할 때(규칙과 관계없이) | 이름 꼴마다 `backupKeep`(기본 14)개를 넘는 가장 오래된 것부터 지운다. 정확히 이 이름 꼴인 파일만 지운다 |
| `before-schema-*`·`before-restore-*`·`before-import-*`·`before-migrate-*.sqlite` | 스키마 이전(새 판이 기억 DB의 표·열·`user_version`을 바꿀 때)·복구·들여오기·옮기기 바로 전, 하루 규칙과 관계없이 | 지우지 않는다 |

- 세션 시작 훅은 폴더를 한 번 읽어 24시간 규칙만 보고, 사본이 필요하면 `orbit-mem backup auto`를 따로(새 세션, 입출력 없이) 띄운 뒤 기다리지 않는다. `backup auto`는 `backups/.backup.lock`(OS 파일 잠금)을 잡은 뒤 규칙을 다시 보고, 다른 프로세스가 잠금을 쥐고 있으면 건너뛴다. 실패는 `logs/backup.log`에 남기고 훅은 언제나 종료 0이다. `orbit-mem config set autoBackup false`로 끈다(기본 켜짐).
- 스키마 이전 전 사본을 만들지 못하면 스키마를 바꾸지 않고 기억 DB 열기가 실패한다.
- `orbit-mem backup list`: 사본(이름·종류·시각·크기)을 최신순으로. `orbit-mem backup status`: 폴더, 자동 켜짐, 보관 개수, 마지막 하루 사본 시각·개수, 다음 자동 사본 시각. 둘 다 파이프면 JSON이다.
- `orbit-mem backup restore <사본 이름 또는 경로> [--yes]`: 먼저 무엇을 무엇으로 바꾸는지(기억 수와 함께) 출력한다. `--yes`가 없으면 터미널에서는 묻고, 터미널이 아니면 바꾸지 않고 종료 1이다. 사본의 무결성(`PRAGMA integrity_check`)과 기억 표를 확인한 뒤, 지금 DB를 `before-restore-*.sqlite`로 남기고, SQLite 온라인 백업 API로 사본을 한 트랜잭션에 들여온다. 파일을 갈아 끼우지 않으므로 열려 있는 MCP 서버도 바뀐 내용을 본다. 들여온 뒤 다시 검사해 실패하면 남긴 사본으로 되돌린다.

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
| `internal/memory` | 기억 저장소(SQLite), 찾기·연결·태그·제안·지표 사건·원장·활동 기록·내보내기·들여오기·사본(하루 사본·스키마 이전 전 사본)·복구 |
| `internal/activity` | 활동 기록 찾기(새 기록 + 옛 `work.db` 읽기 전용) |
| `internal/mcpserver` | MCP 서버(기억·활동 기록 도구, `memory_graph`, 회고용 도구)와 얇은 CLI가 부르는 `Tools` |
| `internal/inject` | 세션 시작 묶음·요청 회상 |
| `internal/retro`, `internal/transcript` | 회고 저장: 재료(호스트 대화 기록) 만들기·제출 받기·실행 기록·세션 끝 대기열. 모델은 부르지 않는다(실행은 OpenOrbit) |
| `internal/memplugin` | 플러그인 원본(`source/`, go:embed)과 Claude·Codex 꼴·마켓 빌드, OpenOrbit `orb` 찾기(실험실) |
| `internal/lab` | 하네스 실험실. `orbit-mem`에서 헤드리스 호스트를 부르는 유일한 곳(ONID-124에서 빠진다) |
| `internal/install` | 실행 파일 복사, 예전 `orbit-mem` 회고 예약 지우기(`legacy.go`), 호스트 플러그인 등록·해제·상태(`hosts.go`) |
| `internal/config`, `internal/i18n`, `internal/sqlitedb`, `internal/project` | 설정, 문구, SQLite 열기·옛 `work.db` 하루 사본, 프로젝트 범위 |
| `internal/backup` | 백업 폴더의 사본 이름·목록·보관 정리·24시간 규칙·잠금 |
| `internal/memory/contract`, `internal/decision/contract` | 읽기 계약 `orbit.memory`와 옛 `orbit.decision` 예시(Orbit Studio 플러그인 시험이 읽는다) |

## 플러그인 고치기·다시 만들기

플러그인의 정본은 `internal/memplugin/source/`다: `plugin.json`(이름·설명·스킬 목록·MCP 서버·훅; 판은 `internal/version`), `skills/memory`·`skills/proposals`. Go가 두 꼴을 만든다(Node 불필요).

| 꼴 | 파일 |
| --- | --- |
| Claude Code | `.claude-plugin/plugin.json`, `.mcp.json`(서버 `orbit-mem`: `orbit-mem mcp`), `hooks/hooks.json`(Claude가 스스로 읽으므로 `plugin.json`에 다시 적지 않는다), `skills/` |
| Codex | `.codex-plugin/plugin.json`(`skills`·`hooks`·`mcpServers`·`interface`), `codex/mcp.json`(`env_vars: ["ORBIT_HOME"]`), `hooks/codex.json`, `skills/` |
| 마켓 | Claude `.claude-plugin/marketplace.json`(source `./plugin`), Codex `.agents/plugins/marketplace.json`(source local `./plugin`) |

검사: 이름·판 꼴, 스킬 머리말의 `name:`, 아는 훅 사건, Codex가 SessionEnd 훅을 3초로 자르므로 3초 넘는 SessionEnd는 빌드가 멈춘다. 원본을 고치거나 판을 올리면 저장소의 GitHub 마켓을 다시 만든다.

```bash
go run ./cmd/orbit-mem plugin build --repo .
```

커밋한 `.claude-plugin/marketplace.json`·`plugin/`이 원본과 어긋나면 `go test ./internal/memplugin`(`TestRepo_커밋한_생성물이_원본과_같다`)이 실패한다. Codex 꼴은 저장소에 두지 않는다: Codex는 언제나 `install`이 이 PC에 만든 로컬 폴더를 마켓으로 쓰므로 실행 파일과 같은 판이 붙는다.

## doctor --json

OpenOrbit `orb doctor`가 읽는다. 키를 바꾸지 않는다.

| 키 | 뜻 |
| --- | --- |
| `version`, `binary` | 이 `orbit-mem`의 판과 경로 |
| `pathBinary` | 훅·MCP가 부를 PATH의 `orbit-mem`(없으면 빈 문자열) |
| `home`, `db`, `config` | 데이터 경로, `memory.db`가 있는지, 설정 |
| `memories`, `pendingProposals`, `sessionQueue`, `activity` | 상태별 기억 수, 대기 제안, 세션 끝 회고 대기열, 활동 기록 세션 수(`sessions`·`legacySessions`) |
| `lastRetros`, `lastBackup` | 최근 회고, 마지막 기억 하루 사본 경로(0.6.0에서 `schedule`은 뺐다: 회고 예약은 OpenOrbit이 맡는다) |
| `backups` | `dir`, `auto`, `keep`, `count`(기억 하루 사본 수), `last`·`lastPath`·`lastAt`, `due`, `nextAfter`, `workCopies`, `snapshots` |
| `claude` | `path`, `plugin`(`installed`·`missing`·`unknown`·`no-host`), `version`, `enabled`, `marketplace`(`github:<owner/repo>`·`directory:<경로>`) |
| `codex` | `path`, `config`(읽은 `config.toml`), `marketplace`, `plugin`(`installed`·`missing`), `enabled`, `hooksTrusted` |
| `problems` | `[{id, message, fix}]`. `id`: `not-on-path`, `db-missing`, `claude-plugin-missing`, `claude-plugin-disabled`, `claude-plugin-version`, `codex-plugin-missing`, `codex-plugin-disabled`, `codex-hooks-untrusted` |

## 릴리스

`scripts/release.sh`가 `dist/<버전>/`에 darwin arm64·amd64, windows amd64(빌드만) 자산(`orbit-mem-<버전>-<os>-<arch>`)과 `SHA256SUMS`를 만든다. `--publish`는 깨끗한 `origin/main` 끝에서 태그 `v<버전>`으로 GitHub Release를 올린다(노트는 `RELEASE_NOTES` 파일). 서명이 없으므로 내려받은 바이너리는 `xattr -d com.apple.quarantine orbit-mem`으로 격리 속성을 풀고 `orbit-mem install`을 실행한다.

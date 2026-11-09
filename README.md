# orbit-mem

저장소 `crispebble/orbit-mem`이다(2026-11-08 `onidot-labs`에서 옮겨 왔다). 이 PC의 AI 호스트(Claude Code·Codex, 그리고 훅·MCP 없이 `orbit-mem inject`·`orbit-mem tool`로 붙는 호스트)가 함께 쓰는 공유 기억 시스템 `orbit-mem`(Go 실행 파일 하나)을 담는다. 기억과 그 출처, 호스트 세션의 활동 기록, 그리고 이것들을 호스트에 붙이는 MCP 서버와 훅이다. 서버와 상주 프로세스는 없다.

`orbit-mem`은 모델이나 헤드리스 호스트를 부르지 않는다(`TestNoHostCalls`가 지킨다). 다른 제품(하네스·Atlas 등)의 이름도 판정에 쓰지 않는다. 판단이 필요한 저장은 일하는 중인 호스트 AI가 `memory_save`로 한다. 모델 없이 되는 정리(증거 확정, 기한 지난 기억 낡음, 효과 판정과 해로운 자동 변경 되돌리기 등)는 세션 시작 훅이 하루 한 번 뒤에서 돌린다(아래 「하루 정리」). 모델 회고(하루·주간·세션 끝 회고, 회고 저장 API `retro-store`)는 2026-11-08 사용자 결정으로 걷어냈다(보관 태그 `archive/retro-2026-11-08`). 개인 내용 없이 공개할 수 있게 만든다. 버전은 0.x를 쓴다.

`orbit-mem`은 자기 Claude Code·Codex 플러그인 `orbit-mem`(기억 MCP 서버, 훅 다섯, 스킬 `memory`·`proposals`)을 Go로 직접 빌드하고 호스트에 등록한다. 다른 플러그인 없이 혼자 설치된다. 이 저장소 자체가 Claude Code 플러그인 마켓이다(아래 「설치」).

## AI 에이전트가 설치할 때

사용자가 AI 에이전트(Claude Code·Codex 등)에게 「orbit-mem 깔아 줘」라고 하면 에이전트는 이 순서를 따른다. **무엇을 끌지는 사용자가 정한다.** 에이전트는 묻기만 하고, 동의한 것만 명령으로 적용한다. 설정 파일을 손으로 고치지 않는다.

1. 실행 파일을 받는다(아래 「릴리스」).
2. `./orbit-mem install --json`을 실행한다. stdout은 성공이든 실패든 JSON 하나다(진행 줄은 stderr): `{ok, error?, dryRun, binary, home, steps, askUser, warnings}`. 종료 코드는 0(설치함·`--dry-run`이면 보이기만 함, `askUser`가 있어도 0), 1(설치 실패, `error`에 이유), 2(인자가 틀림)다. `warnings`는 겹침 판정에서 읽지 못한 파일과 그 이유이고, 판정은 나머지 파일로 이어 간다.
3. `askUser`의 항목마다 `question`(필요하면 `reason`)을 사용자에게 그대로 묻는다. 항목은 orbit-mem과 겹치는 기억 시스템이다(아래 「겹치는 기억」). 둘이 함께 돌면 같은 것을 따로 저장·회상해 규칙이 두 번 들어가거나 바꾼 결정의 옛 판이 다시 나온다.
4. 사용자가 동의한 항목만 그 `apply` 명령을 실행한다(대개 `orbit-mem host-memory off --only <id>`, Claude Code MCP 서버는 `claude mcp remove …`). `blockedBy`가 있는 항목은 프로젝트·관리 설정이 켠 것이라 `apply`가 비어 있다: 그 파일의 주인이 정한다고 사용자에게 알린다. 모두 유지하겠다고 하면 `keep` 명령(`orbit-mem config set hostMemory keep`)을 실행해 다시 묻지 않게 한다. orbit-mem이 끈 것은 `orbit-mem host-memory on`이 되돌리고, 기억 파일은 어느 쪽에서도 지우지 않는다.
5. `orbit-mem doctor`로 확인한다.

`install --json`에 `askUser`가 비어 있으면 물을 것이 없다. 설치 뒤에도 겹치는 기억이 켜져 있으면(사용자가 아직 정하지 않음) 사람이 있는 세션의 시작 문맥 끝에 같은 질문을 하라는 한 줄이 붙는다. 명세: Atlas 설계 · orbit-mem 2판 17절.

### 겹치는 기억

`orbit-mem host-memory status`·`doctor`·`install`·세션 시작 훅은 같은 판정을 쓴다.

| id | 무엇 | 켜짐 판정 | `off` |
| --- | --- | --- | --- |
| `claude-auto-memory` | Claude Code 자체 기억 | `autoMemoryEnabled`를 Claude Code 설정 우선순위대로 읽는다: 관리(`managed-settings.json` 뒤 `managed-settings.d/*.json` 알파벳 순) > 프로젝트 로컬(git 루트의 `.claude/settings.local.json`) > 공유 프로젝트(지금 폴더의 `.claude/settings.json`) > 사용자(`~/.claude/settings.json`). 아무도 정하지 않으면 켜짐, `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`(환경 또는 설정의 `env` 블록)이면 꺼짐 | 사용자 설정에 `autoMemoryEnabled`·`autoDreamEnabled` false |
| `claude-plugin:<id>` | Claude Code의 기억 플러그인 | `enabledPlugins.<id>`를 같은 우선순위로 | 사용자 설정에 `enabledPlugins.<id>` false |
| `claude-mcp:<이름>` | Claude Code의 기억 MCP 서버 | `~/.claude.json`의 `mcpServers`(user)·`projects[<폴더>].mcpServers`(local), 프로젝트 `.mcp.json`(project). user·local 서버는 이 프로젝트의 `disabledMcpServers`에 있으면 꺼짐, `.mcp.json` 서버는 승인(`enabledMcpjsonServers`·`enableAllProjectMcpServers`)된 것만 켜짐이고 `disabledMcpjsonServers`면 꺼짐 | orbit-mem은 끄지 않는다(Claude Code가 그 파일을 스스로 다시 쓴다). `apply`는 `claude mcp remove <이름> --scope <범위>` |
| `codex-memories` | Codex 자체 기억 | `config.toml`의 `features.memories`·`memories.generate_memories`·`memories.use_memories` 중 하나가 true | 세 값 false |
| `codex-plugin:<id>`, `codex-mcp:<이름>` | Codex의 기억 플러그인·MCP 서버 | `config.toml`의 `[plugins."<id>"]`, `[mcp_servers.<이름>]`. `enabled = false`가 아니면 켜짐 | 그 표에 `enabled = false` |

- 관리·프로젝트 설정이 켜 둔 것은 `lockedBy`에 그 파일이 나오고, `off`는 사용자 설정을 고쳐 봐야 소용없으므로 아무것도 바꾸지 않고 알린다. 명령줄 `--settings`와 MDM·서버 관리 설정(plist·레지스트리·claude.ai 콘솔)은 파일로 볼 수 없어 판정에 넣지 않는다.
- 아는 기억 시스템은 코드 상수 `knownMemories` 한 곳에 있다(`cmd/orbit-mem/hostmem_detect.go`, 확인한 출처를 주석으로): claude-mem(플러그인 `claude-mem`·`claude-mem-cowork`), supermemory(플러그인 `supermemory`·`claude-supermemory`, MCP `mcp.supermemory.ai`), mem0(플러그인 `mem0`, MCP `mem0-mcp-server`), basic-memory(MCP `basic-memory`), MCP 참조 서버 `@modelcontextprotocol/server-memory`. 플러그인은 이름(@ 앞)으로 찾는다. MCP 서버는 항목 이름이 아니라 실행하는 것으로 찾는다: `command`·`args`의 패키지·명령 이름(뒤에 판이 붙어도 되고, 경로 인자는 보지 않는다)이나 `url`의 호스트다. orbit-mem 자신은 빠진다.

## 다른 도구와의 관계

`orbit-mem`은 다른 도구를 모른다. 하네스 플러그인 `orbit-code`(`crispebble/orbit-code`, 역할·스킬)와는 서로를 부르지 않고, 함께 깔면 각자 따로 돈다. 기억 주입은 `orbit-mem` 자기 플러그인의 훅이 한다. 훅이 없는 호스트는 `orbit-mem inject`(세션 시작·요청 회상)와 `orbit-mem tool`(기억 도구)을 부르고 `--host`로 자기 이름을 준다.

보관·폐기한 제품용 부분(Orbit Studio 플러그인 `studio-plugin/`, 결정 카드 계약, 기억 그래프 읽기 서버 `mcp --reader`, 하네스 실험실 저장 API, 옛 작업 기록 `work.db` 지표·읽기·복사, orbit-pi 전용 훅 인자)은 2026-11-08 걷어냈다(같은 보관 태그). 기억 DB의 표는 바꾸지 않았다.

## 설치

한 줄 설치(macOS·Linux, 공개 저장소라 로그인이 필요 없다). 스크립트는 OS·CPU에 맞는 릴리스를 받아 `SHA256SUMS`로 검증한 뒤 `orbit-mem install`을 실행한다. 스크립트 자신은 홈 디렉터리에 아무것도 쓰지 않는다.

```bash
curl -fsSL https://raw.githubusercontent.com/crispebble/orbit-mem/main/scripts/install.sh | sh
```

`curl | sh`는 내용을 보기 전에 실행되므로 먼저 읽고 싶으면 내려받아 확인한 뒤 실행한다. 특정 판은 `ORBIT_MEM_VERSION=0.13.0` 환경 변수나 `--version 0.13.0`, `install`에 넘길 인자는 `sh -s -- --no-hosts`처럼 뒤에 붙인다.

```bash
curl -fsSLO https://raw.githubusercontent.com/crispebble/orbit-mem/main/scripts/install.sh
less install.sh && sh install.sh
```

업데이트는 한 줄이다. 최신 릴리스를 받아 검증하고 실행 파일을 바꾼 뒤 Claude Code·Codex 플러그인을 같은 판으로 맞춘다. 기억 DB·설정·활동 기록은 건드리지 않는다. 세션 시작 훅은 하루에 한 번 이하로 새 판을 확인해 있으면 한 줄로 알린다(끄려면 `orbit-mem config set updateCheck false`).

```bash
orbit-mem update            # --check: 새 판이 있는지만, --dry-run: 하려는 일만, --json
```

수동 설치: 릴리스에서 실행 파일을 받아(아래 「릴리스」) 한 번 실행한다.

```bash
./orbit-mem install            # ~/.local/bin/orbit-mem, 설정·기억 DB, 호스트 플러그인 등록(예약은 하지 않음)
orbit-mem doctor               # 상태와 문제마다 고칠 명령 한 줄
```

`install`이 호스트에 하는 일(호스트가 PATH에 있을 때만):

1. Codex가 있거나 `--local-plugin`이면 내장 원본을 `$ORBIT_HOME/plugins/orbit-mem`(로컬 마켓 폴더: Claude·Codex 꼴과 두 마켓 파일)에 빌드한다.
2. Claude Code: `claude plugin marketplace list --json`으로 마켓 `orbit-mem`을 보고, 없으면 `claude plugin marketplace add crispebble/orbit-mem`(`--local-plugin`이면 로컬 폴더), 같은 출처로 있으면 `claude plugin marketplace update orbit-mem`, 다른 출처면 `remove` 뒤 `add`. 이어서 `claude plugin list --json`에 없으면 `claude plugin install orbit-mem@orbit-mem --scope user`, 있으면 `claude plugin update orbit-mem@orbit-mem`.
3. Codex: 언제나 로컬 폴더 마켓이다. `codex plugin marketplace add $ORBIT_HOME/plugins/orbit-mem` → `codex plugin add orbit-mem@orbit-mem`(다시 해도 된다). Codex는 신뢰한 훅만 돌리므로 Codex에서 `/hooks`를 열어 `orbit-mem` 훅을 신뢰한다(훅이 바뀌면 다시).

`install`은 아무것도 예약하지 않는다. 0.6.0 전 `orbit-mem install`이 등록한 회고 예약(launchd `dev.onidot.orbit.retro`, Windows 작업 `Orbit Retro`)을 지우던 일도 0.17.0에서 걷어냈다: 그런 예약이 남아 있으면 손으로 지운다(`launchctl bootout gui/$(id -u)/dev.onidot.orbit.retro`). `--no-hosts`는 호스트를 건드리지 않고, `--dry-run`은 무엇을 할지만 보인다. `orbit-mem uninstall`은 두 호스트의 `orbit-mem@orbit-mem`·마켓 `orbit-mem`을 뺀다(있는 것만). 기억 DB·활동 기록·백업·로컬 플러그인 폴더는 남긴다.

Claude Code의 `/plugin`에서 GitHub 마켓 `crispebble/orbit-mem`을 더해 플러그인만 깔 수도 있다. 이때 실행 파일이 PATH에 없으면 세션 시작 훅이 설치 안내(「orbit-mem 실행 파일이 PATH에 없습니다. … `orbit-mem install`을 실행하세요.」)를 문맥으로 내고, 요청·세션 끝 훅은 조용히 넘어간다. 실행 파일과 플러그인의 판이 다르면 세션 시작 문맥 끝에 `orbit-mem install`로 맞추라는 한 줄이 붙는다(훅이 `--plugin-version`을 넘긴다).

훅과 MCP 서버는 절대경로를 박지 않고 PATH의 `orbit-mem`을 부른다. 훅 명령은 sh 꼴이다(`if command -v orbit-mem >/dev/null 2>&1; then orbit-mem hook … --host … --plugin-version …; else …; fi`). 맥·리눅스에서 시험했고 Windows는 확인하지 않았다. 호스트를 Dock·Finder에서 띄우면 셸의 PATH를 받지 못할 수 있으니 `orbit-mem doctor`의 `orbit-mem on PATH`로 확인한다.

이력: 2026-11-01까지 이 저장소의 실행 파일은 지금 orbit-code의 명령인 `orb`(4차 동안 별칭 `oni`)였고, 작업 기록(`work.db`, 훅 번역·결정 카드), orbit-pi 런타임·데몬·TUI 실행, orbit-code TS CLI 다리를 함께 담았다. N4-2 차례 5(ONID-121)에서 그것들을 걷어 내고 `orbit-mem` 0.4.0이 됐다. 옛 `work.db`는 0.17.0부터 읽지도 복사하지도 않는다(파일은 지우지 않는다).

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `orbit-mem install [--bin-dir D] [--local-plugin] [--no-hosts] [--dry-run] [--json]` | `orbit-mem`을 `~/.local/bin`(Windows는 `%LOCALAPPDATA%\Programs\orbit`)에 두고, 설정과 기억 DB를 만든 뒤 플러그인을 호스트에 등록한다(위 「설치」). 예약은 하지 않는다. 끝에 「사용자에게 확인할 것」(겹치는 기억마다 질문과 끄는 명령)을 보이고, 끄지는 않는다. `--json`은 성공이든 실패든 `{ok, error?, dryRun, binary, home, steps, askUser:[{id, question, reason, apply, blockedBy?, keep}], warnings:[{path, reason}]}` 하나를 stdout에 낸다. 종료 코드는 0 성공, 1 실패, 2 인자 오류(위 「AI 에이전트가 설치할 때」) |
| `orbit-mem host-memory [status\|off\|on] [--host claude\|codex\|all] [--only ID,…] [--json]` | orbit-mem과 겹치는 기억 시스템(위 「겹치는 기억」). `status`(기본, 읽기만)는 켜짐, 적용되는 값과 그 출처 파일, `lockedBy`, 끄는 명령, 기억 저장소의 위치·파일 수·크기, 읽지 못한 파일(`warnings`)과 `hostMemory` 설정을 보인다. `off`는 켜진 것 중 orbit-mem이 끌 수 있는 것만 끈다. 바꾸기 전에 `<파일>.bak-orbit-mem-<날짜>`로 백업하고, 다른 키·줄과 키 순서·들여쓰기는 그대로 두며(JSON 값은 다시 들여 쓴다), 임시 파일에 쓴 뒤 바꿔 넣고, 바꾼 값과 그 전 값을 `$ORBIT_HOME/host-memory.json`에 남긴다. 안전하게 바꿀 수 없는 TOML(인라인 표 `features = { … }`)은 거절하고 건드리지 않는다. `on`은 그 기록에 있는 값, 곧 orbit-mem이 끈 값만 되돌린다(그 뒤 사람이 바꾼 값은 둔다). 기억 파일은 지우지 않는다. 사용자가 동의한 뒤에만 쓴다 |
| `orbit-mem update [--check] [--dry-run] [--version X] [--force] [--json]` | 최신 릴리스(공개 GitHub Releases)를 받아 `SHA256SUMS`로 검증하고, 실행 파일을 원자적으로 바꾼 뒤 새 판의 `install`을 다시 실행해 호스트 플러그인을 같은 판으로 맞춘다. 같거나 낮은 판이면 아무것도 바꾸지 않는다(`--version`으로 현재보다 낮은 판을 지정하면 거절하고, 낮추려면 `--force`를 준다). `--check`는 새 판이 있는지만 알리고(종료 코드 0), `--dry-run`은 할 일만 보이고, `--json`은 `{ok, error?, current, latest, updated, binary, steps}` 하나를 stdout에 낸다. 기억 DB·설정·활동 기록은 건드리지 않는다. 설정 `updateCheck`(기본 true)가 켜져 있으면 세션 시작 훅은 캐시(`update-check.json`)만 읽어 새 판이 있으면 문맥 끝에 한 줄로 알리고, 네트워크는 기다리지 않는다. 캐시가 없거나 24시간이 지났으면 `backup auto`처럼 떼어 낸 프로세스(숨은 `update --refresh-cache`)가 하루 한 번 이하로 갱신한다(실패는 조용히 넘기고 24시간 안에는 다시 하지 않는다). 그래서 새 판 안내는 갱신 뒤 다음 세션부터 나온다. `ORBIT_MEM_BASE_URL`로 조회·내려받기 주소를 바꿀 수 있다(시험용; `install.sh`는 뜻이 달라 `ORBIT_MEM_RELEASES_URL`·`ORBIT_MEM_LATEST_URL`을 쓴다) |
| `orbit-mem uninstall` | 호스트의 플러그인·마켓을 뺀다. 기억 DB·활동 기록·백업·로컬 플러그인 폴더는 남긴다 |
| `orbit-mem doctor [--json]` | 실행 파일 경로·판, PATH의 `orbit-mem`, 데이터 경로·기억 수·남은 옛 대기 제안·하루 정리(켜짐·마지막 날)·활동 기록 수, Claude Code의 `orbit-mem@orbit-mem` 설치·판·마켓, Codex 등록·훅 신뢰, 문제마다 고칠 명령 한 줄. `--json` 키는 아래 「doctor --json」 |
| `orbit-mem search <질의> [--kind K]… [--all] [--stale] [-n N]` | 기억 찾기(`memory_find`와 같은 함수). 한 건마다 id·종류·상태·적용 대상·제목 한 줄(100자까지)을 보인다. `-n` 기본 20, 최대 30(넘으면 30). `--json`은 `{"items":[{id, kind, state, appliesTo, title}]}`(제목 100자, 적용 대상 60자에서 자름) |
| `orbit-mem show <id> [--history]` | 기억 하나 읽기(`memory_get`을 `full: true`로 불러 늘 본문·출처·연결 전문을 보인다). `--json`은 그 보기 그대로다: `scope`·`project`·`createdAt`·`days`·`legacyId`는 없고, `links`는 「제목 — id · note」 문자열 줄이며, 출처는 전부 온다. 기억을 빠짐없이 옮기려면 `export`를 쓴다 |
| `orbit-mem list [--kind K]… [--state S]… [--tag T]… [--all] [-n N]` | 최근 기억 목록(질의 없는 `memory_find`, 기본은 후보·확정, 한 건마다 색인 한 줄, `-n` 기본 20·최대 30, 제목 100자. `--json`은 `search`와 같은 꼴) |
| `orbit-mem edit <id> [--title] [--body \| --body-file F] [--tag T]… [--link ID]… [--unlink ID]… [--note]` | 기억 고치기(`memory_update`, 판이 오르고 이력이 남는다) |
| `orbit-mem stale <id> [--note]` | 낡음으로 표시(`memory_update` action=stale, 지우지 않는다) |
| `orbit-mem export [--format jsonl\|md] [--out 경로] [--all]` | 사람이 읽고 옮길 수 있게 내보낸다. jsonl은 한 줄에 기억 하나(출처·나온 날·연결 포함), `--out`이 없으면 stdout. md는 폴더에 `<id>.md`와 `index.md`. 기본은 숨김 빼고 전부, `--all`은 숨김(거절한 후보)까지 |
| `orbit-mem import <jsonl> [--dry-run]` | export한 jsonl을 들여온다. 아래 「들여오기 규칙」 |
| `orbit-mem activity [질의] [--host H] [--project all\|DIR] [--since 날짜] [-n N]` | 활동 기록 찾기(`activity_find`) |
| `orbit-mem proposals [drain [--dry-run]]` | 사람 승인 대기열은 0.13.0에서 없앴다. `proposals`는 대기열을 없애기 전에 남은 옛 제안을 보이고, `drain`은 그것을 모두 닫는다: 자동 규칙에 맞으면 적용(`settled`, 새 자동 변경을 가리킴), 아니면 까닭을 메모로 남기고 닫음(`dropped`). 행은 지우지 않는다. `--dry-run`은 DB 사본에서 돌려 결과만 보인다. 실제로 돌리기 전에 `orbit-mem backup`. 옛 제안 하나씩 결정(`review`·`approve`·`reject`)도 남아 있다 |
| `orbit-mem audit [--days N] [--json]`, `audit revert <원장·제안 id> [--note]`, `audit dismiss <f-…\|correction:…>` | 감사 목록(아래 「확정 규칙과 감사 목록」): 최근 자동 확정·자동 정리, 어긋남, 기준을 넘은 반복 교정(스킬·역할 교정과 서로 다른 작업 여럿에서 나온 절차 교훈은 「하네스 개선 후보」로 표시). 틀린 것만 되돌리거나 확인함으로 닫는다 |
| `orbit-mem mcp [--actor ai\|user\|automation]` | 호스트가 띄우는 MCP 서버(stdio). 아래 「MCP 도구」. `--actor`는 이 서버로 쓰는 쪽(기본 ai) |
| `orbit-mem hook session-start\|prompt\|pre-tool\|subagent-start\|session-end --host claude\|codex [--plugin-version V]` | 호스트 훅(stdin JSON). 세션 시작에 고정 묶음(상시 핵심·「먼저 찾는 곳」 한 줄(이 폴더에서 보이는 확정 규칙·결정 수, `memory_find` 먼저)·최근 결정 제목·해로운 자동 변경 되돌림 한 줄, 합계 6,000자 이하. 핵심 밖 확정 규칙은 넣지 않고 요청 회상 후보로 남는다. `inject rules`만 예전처럼 남는 자리를 확정 규칙으로 채운다. resume에서는 바뀌지 않고, 입력 `source`가 `compact`·`clear`면 묶음을 다시 만든다), 요청마다 강하게 맞는 기억을 `recallChars` 안에서 준다(어떤 훅 문맥도 9,000자 이하). 요청 훅은 세션 도중 대체된 기억을 「정정」 줄로, 세션 시작 뒤 다른 세션에서 확정된 기억을 「새 기억」 줄(최대 3개)로 세션마다 한 번 더한다. `pre-tool`(PreToolUse)은 부르려는 도구(`tool_name`)에 도구 트리거(`tools`)가 걸린 현재값을 3개·1,000자 안에서 세션마다 한 번 넣는다. `subagent-start`(Claude Code SubagentStart, 플러그인은 Claude 꼴에만 싣는다)는 user 상시 핵심만 3,000자 안에서 넣는다. 서브에이전트 안에서 부른 훅(`agent_id`가 있음)은 「세션 id/agent_id」를 세션으로 쳐서, 주 세션이 받은 도구 기억도 서브에이전트가 한 번 더 받는다. 시작·요청·끝 훅은 활동 기록을 남기고, 세션 끝 훅은 활동 기록의 끝 시각만 남긴다. 세션 시작 훅은 마지막 기억 사본이 24시간보다 오래됐으면 `orbit-mem backup auto`를, 그날 정리가 아직이면 `orbit-mem tidy auto`를 따로 띄우고 기다리지 않는다(아래 「백업」·「하루 정리」). 어떤 경우에도 호스트를 실패시키지 않는다(종료 0). `--plugin-version`이 자기 판과 다르면 세션 시작 문맥 끝에 맞추라는 한 줄을 더한다(다른 훅은 무시). 옛 인자(`hook work`, `--runtime`·`--launcher`·`--record-only`)는 0.17.0에서 걷어냈고, 불러도 인자 오류로 아무것도 하지 않는다(종료 0) |
| `orbit-mem inject session-start\|prompt --host H [--cwd D] [--session S] [--rules brief\|full] [--source compact\|clear]` | 훅이 없는 호스트에 훅과 같은 기억 문맥을 JSON(`text`·`memoryIds`)으로 준다. 길이 한도·정정·새 기억 줄도 훅과 같다. 요청 글은 stdin. `--host`는 꼭 준다(사용 사건과 「왜 안 나왔나」 진단이 호스트로 나뉜다). `--rules brief`면 확정 규칙을 짧은 꼴(규칙마다 제목 한 줄과 id, 본문은 `memory_get`)로 주고, 요청 회상이 그 요청과 강하게 맞는 확정 규칙을 본문과 함께 넣는다. 비우거나 `full`이면 전문 꼴이다. `inject delivered --host H --session S --ids A,B`는 호스트가 회상을 모델 요청에 실었다는 사건(`inject.delivered`)을 남긴다. Claude Code·Codex(`claude`·`codex`) 말고는 이 사건이 있어야 「전달됨」으로 본다 |
| `orbit-mem tool memory_find\|memory_get\|memory_save\|memory_update --host H [--cwd D] [--session S] [--actor ai]` | MCP 클라이언트가 없는 호스트의 기억 도구. `--host`는 꼭 준다. 입력은 MCP 도구와 같은 JSON(stdin), 결과도 같은 값의 JSON(stdout, 들여쓰기와 `<>&` 이스케이프만 다르다), 도구 오류는 `{"error": …}`와 종료 1. 쓰는 쪽은 기본 AI이고, 호출은 그 호스트·세션의 `tool.<이름>` 사건으로 남는다 |
| `orbit-mem why <기억 id> [요청 글…] [--cwd D] [--host H] [--session S] [--rules brief\|full] [--json]` | 「왜 안 나왔나」 진단. 저장됨·적격·선택됨·주입됨·전달됨·사용됨을 각자의 증거(그 세션의 `inject.*`·`inject.delivered`·`tool.memory_get`·`memory.used` 사건, 세션이 없으면 지금 요청으로 묶음을 다시 만들어 봄)로 따로 판정하고 멈춘 단계를 보인다. 읽기만 한다 |
| `orbit-mem place [--cwd D] [--json]` | 제품 지도(`config.json`의 `products`, 아래 「적용 대상·제품 지도·상시 핵심」)가 폴더를 어느 제품으로 보는지: 범위 키, 제품(여럿이면 여러 제품 폴더), 여기서 `appliesTo` 없이 저장한 기억의 적용 대상. 읽기만 한다 |
| `orbit-mem place --suggest [--root D] [--json]` | 이 PC의 저장소 원격과 작업 폴더(기본 `~/_workspace`)로 제품 지도 첫 표를 제안한다. 설정은 쓰지 않는다: `products`에 넣고 틀린 줄만 고친다 |
| `orbit-mem core --suggest [--scope S] [--json]` · `core --pin <id,…> [--quote Q]` | 상시 핵심 처음 뽑기 제안(읽기만)과 고른 기억 승격(아래 「적용 대상·제품 지도·상시 핵심」) |
| `orbit-mem product rename <옛> <새> [--dry-run] [--json]` | 제품 지도(`config.json`의 `products`)의 제품 이름을 바꾸고 그 제품에 묶인 기억(`product:<옛>`, 상태·상시 핵심과 관계없이 모두)을 `product:<새>`로 함께 옮긴다. 옛 이름이 지도에 없거나, 새 이름이 이미 있거나, `product:<새>`에 기억이 있으면 거절한다. 시작 전에 `backups/`에 기억 DB 사본(`memory-before-product-rename-…sqlite`)과 설정 사본(`config-before-product-rename-….json`)을 남기고, 기억 옮기기와 설정 쓰기를 한 트랜잭션 안에서 한다: 설정은 처음 읽은 내용 그대로일 때만 쓰고, 쓴 뒤 실패하면 원래 내용으로 되돌린다. 내용·주제·승격은 바꾸지 않고 기억마다 판을 올려 이력(`rename`)에 남긴다(자동 변경 원장에는 남기지 않는다). `--dry-run`은 무엇을 옮길지만 보이고 사본도 만들지 않는다 |
| `orbit-mem place --move [--json]` | 제품 지도에 맞는 `product:?` 기억을 그 제품으로 옮긴다(범위만, 자동 변경으로 원장에 남고 `orbit-mem audit revert`로 되돌린다). 하루 정리도 같은 일을 한다 |
| `orbit-mem plugin build [--out D \| --repo R]` | 내장 원본으로 플러그인을 만든다. `--out`(기본 `$ORBIT_HOME/plugins/orbit-mem`)에는 Claude·Codex 꼴과 두 로컬 마켓을, `--repo`에는 GitHub 마켓(`.claude-plugin/marketplace.json`과 `plugin/`, Claude 꼴만)을 쓴다. `plugin/` 안의 남은 파일은 지운다 |
| `orbit-mem migrate [--json]` | 기억 DB의 스키마를 이 실행 파일의 판으로 올리고(바꾸기 전 사본을 남긴다), 옛 실행 파일이 쓴 늦은 검색 색인 줄을 다 채운다. 채운 수를 알린다 |
| `orbit-mem migrate --reindex [--json]` | 검색 색인(`memory_index`·`memory_fts`)을 비우고 모든 기억에서 다시 만든다(멱등). 다시 만든 줄 수를 알린다. 색인이 깨졌다고 의심될 때 쓴다 |
| `orbit-mem migrate --from <사본>` | 옛 Knowledge 백업 사본을 한 번 옮긴다 |
| `orbit-mem stats [--days N \| --week 월요일]` | 지표: 반복 교정, 요청 적중, 사용 판정, 빈 검색, 저장, 쓰기 판정, 제안 결정, 근거 파일 판정, 효과 원장, 기억 그래프(태그 묶음), 옛 기억 정리. 교정·사용 판정·쓰기 판정·근거 파일 판정은 걷어낸 회고가 남긴 옛 사건을 센다 |
| `orbit-mem ledger [--all]` | 승인한 변경과 자동 변경(기억·하네스·지침)의 효과 판정 |
| `orbit-mem backup [list\|status\|restore <사본> [--yes]]` | 지금 사본 만들기(하루 규칙과 관계없이), 사본 목록, 상태, 복구. 아래 「백업」 |
| `orbit-mem tidy [--json]` | 하루 정리를 지금 돌리고 한 일을 센다(날과 관계없이, 그날 표시도 남긴다). 아래 「하루 정리」 |
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
| `launcher` | `human`(훅: 사람이 연 세션)·`host`(`host`·`inject`·`tool`로 계약을 거친 호스트: 누가 띄웠는지는 모른다). 0.17.0 전 orbit-pi가 띄운 세션의 옛 줄에는 `orbit-pi`·`studio`·`telegram`·`workflow`가 있다 |
| `parent` | 세션을 띄운 쪽이 준 `ORBIT_PARENT` |
| `started_at`, `last_at`, `ended_at` | 시작·마지막 활동·끝 시각(Unix ms). 다시 시작(resume·clear·compact)하면 끝 시각이 지워진다 |
| `prompts`, `first_prompt`, `last_prompt` | 요청 수, 첫·마지막 요청 글(비밀값을 가리고 공백을 접어 300자까지) |

- `session-start`가 줄을 만들고(있으면 다시 연다), `prompt`가 요청 수·시각·글을, `session-end`가 끝 시각을 남긴다. 시작 훅을 놓쳐도 첫 요청이나 끝이 줄을 만든다.
- 기억 DB가 없으면(설치 전) 훅은 아무것도 만들지 않는다.

찾기(`activity_find`·`orbit-mem activity`)는 이 표를 출처(`source`: `activity`)를 붙여 마지막 활동 최신순으로 준다. 옛 작업 기록 `work.db`는 0.17.0부터 읽지 않는다.

## MCP 도구

`orbit-mem mcp`(서버 이름 `orbit-mem`, 응답 글은 AI가 읽으므로 한국어):

- 기억 6개: `memory_find`, `memory_get`, `memory_save`, `memory_update`, `proposals`, `proposal_decide`. `memory_save`·`memory_update`는 관련 기억 연결 `links`·`link`·`unlink`를 받고 `memory_get`은 `links`를 준다. `memory_find`는 기본으로 색인 꼴(`detail: "index"`: 건마다 `id`·`kind`·`state`·`appliesTo`·`title`)로 `limit` 기본 8건(최대 30)을 주고, `detail: "summary"`면 본문 앞 200자(`summary`)·`tags`·`pinned`가 더해진다. `memory_get`은 본문을 기본 2,000자까지(자르면 끝에 「… (전문은 full: true)」), 출처는 최근 2개, `links`는 「제목 — id」 줄 최대 5개(더 있으면 「외 N개」)로 줄여 주고, `full: true`면 본문 전문과 출처·`links` 전부를 준다. `memory_save`는 `conflictsWith`(어긋나는 사용자 규칙 id)와 출처 `verification`을, `memory_update`는 `userQuote`(사용자 인용 기억을 바꾸라는 사용자 말)를 받는다. `proposals`는 감사·교정용이다: 기본(`status=audit`)은 감사 목록(`audit`)이고, `status=pending`은 남은 옛 대기 제안이다. `proposal_decide`는 `revert`(자동 변경 되돌리기)·`dismiss`(어긋남·반복 교정 확인함)와, 남은 옛 대기 제안에 쓰는 `approve`·`reject`를 받는다.
- 쓰는 쪽(actor)은 도구 인자가 아니라 서버가 정한다: `--actor`, 없으면 환경 `ORBIT_ACTOR`, 기본 `ai`.
- 활동 기록: `activity_find` — 입력 `query`·`host`·`project`(비우면 이 프로젝트, `all`이면 전부, 아니면 프로젝트 디렉터리 절대경로)·`since`(`YYYY-MM-DD`, RFC 3339, `7d`·`12h`)·`limit`(기본 20, 최대 100). 출력 `{"items":[{source, host, session, project, projectScope, cwd, launcher, parent, startedAt, lastAt, endedAt, prompts, firstPrompt, lastPrompt, summary?}]}`.

## 적용 대상·제품 지도·상시 핵심

설계 · orbit-mem 2판 4.1·4.4, S3 명세(Atlas 명세 · orbit-mem 2판 S3).

- 적용 대상(`appliesTo`): `user`(모든 일), `product:<이름>`(한 제품), `product:?`(폴더에 묶음: 그 폴더와 그 아래에서 회상), `repo:<키>`(한 저장소). DB의 `scope` 열은 그대로 `global`(= user)·`project:<키>`(= repo)이고 `product:<이름>`·`product:?`가 더해졌다(`product:?`의 폴더는 `project` 열). 도구의 기억 JSON에는 `appliesTo`가 붙는다.
- 기준 폴더는 세션을 시작한 폴더다(훅 입력 `cwd`, MCP 서버 폴더). 범위 키는 원격이 있으면 원격 키, 원격이 없는 저장소는 주 저장소 경로(worktree가 함께 쓴다), git 밖이면 그 폴더다.
- 규칙(`kind=rule`)은 `appliesTo`가 있어야 저장한다(사람의 일하는 방식은 대개 `user`). 그 밖에는 `appliesTo`를 비우면 그 폴더의 제품 → 여러 제품 폴더면 제목·본문에 이름이 하나 나온 제품 → 그 저장소 → git 밖이면 그 폴더(`product:?`, `warnings`에 「폴더에 묶음」) 순이다. 임시 폴더(`/tmp`·`$TMPDIR` 아래)는 `product:?`와 「임시 폴더」 경고다. `user`면 「모든 일에 적용」 경고와 지금 user 핵심 길이를, 제품 지도에 없는 제품이면 그 경고를 싣는다. 옛 입력 `scope`(`global`·`project`)는 0.14.x 동안 별칭으로 받고 경고한다(`appliesTo`와 다르면 거절).
- 제품 지도: `config.json`의 `products`(손으로 고친다).

  ```json
  "products": {
    "orbit-code-desktop": {
      "match": ["github.com/onidot-labs/orbit-studio", "~/_workspace/onidot/prototypes/orbit-desktop-*", "~/_workspace/onidot"],
      "names": ["Orbit 데스크탑"]
    }
  }
  ```

  `match` 한 줄은 git 원격 키(정규화한 `host/owner/repo`)이거나 `/`·`~`로 시작하는 폴더(글롭 `*`·`?`·`[…]` 가능, 그 폴더와 그 아래)다. git 원격이 있는 저장소는 원격 키로 찾는다. 맞은 줄 중 가장 구체적인 것(원격 키, 그다음 경로 마디가 많은 폴더)이 이기고, 같은 구체성에 제품이 둘 이상이면 그 폴더는 여러 제품 폴더다(상위 폴더 `~/_workspace/onidot`을 여러 제품에 적는 식). `names`는 요청 글에서 그 제품을 가리키는 말이다. `orbit-mem place`로 확인한다.
- 보이는 곳: `user`는 어디서나, `product:<이름>`은 그 제품의 폴더와 그 제품을 포함한 여러 제품 폴더, `repo:<키>`와 `product:?`는 그 저장소·폴더와 그 아래 모든 폴더·저장소(폴더 계층 상속: 세션 폴더에서 홈 바로 아래까지 올라가며 상위 폴더의 기억을 모두 가져온다). 세션 시작·요청 회상·도구 직전·`memory_find`(`here`)가 같은 판정을 쓴다. 요청 회상은 이 폴더의 제품(여러 제품 폴더면 요청 글에 이름이 나온 제품)의 기억을 사용자 공통보다 앞에 둔다.
- 상시 핵심(`pinned` 열): 확정된 `user`·`product:<이름>` 기억만 승격된다(`memory_save` `pin`, `memory_update` action `pin`·`unpin`, 판 이력 `pin`·`unpin`). 한도는 세션 시작에 들어가는 꼴(`- 제목 (id)` 줄과 본문)로 user 3,000자, 제품마다 2,000자다. 넘으면 자르지 않고 거절하며 그 층의 지금 핵심(id·제목·글자 수)과 합계를 돌려준다. `memory_save pin`이 넘치면 아무것도 저장하지 않는다. 승격된 기억을 대체한 새 판은 한도 안이면 승격을 이어받고, 넘치면 `warnings`에 「상시 핵심에서 빠짐」을 싣는다.
- 세션 시작 순서: user 핵심 → 이 폴더가 한 제품이면 그 제품 핵심(여러 제품 폴더는 넣지 않는다) → 「먼저 찾는 곳」 한 줄(핵심 밖 확정 규칙 N·결정 M개, 일이 닿으면 `memory_find`를 가장 먼저) → 최근 결정 제목(승격된 것 빼고)·해로운 자동 변경 되돌림 줄. 합계 6,000자 이하. 핵심 밖 기억(상위 폴더에서 물려받은 것 포함)은 요청 회상·도구 직전·`memory_find`로만 들어온다.
- 처음 핵심 뽑기: `orbit-mem core --suggest [--scope user|product:<이름>] [--json]`이 층마다 한도 안에서 늘 지킬 확정 기억을 고른다(교정×3 + 어김×3 + 따름 + 사용자 말 2 순, 분야·도구 트리거·한도의 3분의 1을 넘는 것 제외). 아무것도 바꾸지 않는다. 사람이 틀린 것을 빼고 `orbit-mem core --pin <id,…>`로 승격한다(한도 거절·판 이력 `pin`). 사람이 직접 실행하면 그 실행이 근거다. AI가 대신 부르면(`ORBIT_ACTOR=ai`) 사용자 말 기억은 `--quote "사용자 말"`이 있어야 승격되고 그 말이 출처로 남는다. 내리기는 `memory_update action=unpin`.

## 보존 등급·재검토일·주제·저장 거부

기획 · Orbit Code K15.

- 보존 등급(`retention`): `volatile`(1~7일, 기본 7일 뒤 회상에서 빠지고 하루 정리가 낡음으로 바꾼다, 사용자 말을 담은 것은 그대로 둔다), 보통(기본, 만료·재검토일 없음), `durable`(재검토일 기본 90일). `reviewAfterDays`를 주면 어느 등급이든 재검토일을 둔다. 재검토일이 지난 기억은 회상에 「재검토일 지남」으로 표시된다(대기 제안은 내지 않는다). 다시 봤고 아직 맞으면 `memory_update` action `reviewed`가 재검토일을 민다.
- 후보 기한(0.13.0): `candidateExpiryDays`(기본 28, 0이면 끔)일 동안 만들지도 고치지도, 세션·요청에 넣지도, `memory_find`·`memory_get`으로 찾거나 읽지도, 사용 판정을 받지도 않은 후보는 하루 정리가 자동 낡음으로 돌리고 원장에 남긴다. 회상을 넣지 않는 설정(`inject` `rules`·`off`)에서는 후보가 쓰일 기회가 없으니 하지 않는다. 사용자 말 출처 기억은 대상이 아니고, 사건 기록이 기한보다 짧으면(새 설치) 하지 않는다. 되돌리면 후보로 돌아오고 다시 낡음으로 하지 않는다.
- 주제(`topic`): 같은 범위·주제의 기억은 하나만 회상한다(세션 시작 규칙 포함): 확정된 것 중 가장 최근 것, 확정이 없으면 후보·낡음 중 가장 최근 것. 후보는 확정(사용자 말)을 가리지 못하고, 최신 판이 낡아도 옛 판은 돌아오지 않는다. 날이 지난 휘발 기억은 현재값이 아니다.
- 저장 때 대체(설계 · orbit-mem 2판 4.2·4.3, 0.11.0): `memory_save`의 규칙·결정은 `topic`이 필수다(없으면 거절하고 이 범위의 주제 목록과 비슷한 현재값 3개를 보인다). 같은 범위·주제로 저장하면 옛 현재값을 낡음 + `superseded_by`로 돌리고, `supersedes: [id…]`는 범위·주제가 다른 옛 판도 같은 트랜잭션에서 대체한다. 사용자 말(출처 quote)은 옛 사용자 말을 `userQuote` 없이 대체한다(D1). AI가 배운 것·자동화는 사용자 말을 대체하지 않고 어긋남 표시를 남긴다. `volatile`(임시라고 밝힌 말)은 같은 주제를 대체하지 않는다. 응답의 `superseded`·`conflicts`가 한 일을 알린다.
- 검색: `memory_find`·`orbit-mem search`의 질의는 SQLite FTS5 색인(`memory_index`·`memory_fts`, 스키마 판 5)으로 찾고 점수(0~1)를 매긴다. 한·중·일 글은 두 글자 바이그램으로 쪼개 색인한다. 색인은 기억을 쓰는 같은 트랜잭션에서 맞춘다. 옛 실행 파일이 판 5 DB에 쓴 기억은 색인에 없는 「늦은 줄」이라 검색이 훑어서 함께 찾고, 쓰기 호출이 한 번에 최대 200개씩, `orbit-mem migrate`·`install`·`backup restore`가 전부 채운다. 판 4 → 5 이전은 표를 만들고 전부 채운다. 훅·읽기 op·`doctor`는 채우지 않는다(`doctor`는 `index-stale`로 알릴 뿐이다).
- 저장 때 정리(`memory_save`): 같은 범위에 비슷한 기억(제목·본문 점수 `saveSimilarMin` 이상, 기본 0.68, `orbit-mem config set saveSimilarMin 0.7`)이 있으면 저장하지 않고 `saved: false`와 `similar`(최대 3개: id·제목·요약·점수)를 돌려준다. 호출한 AI가 `resolve`로 다시 부른다: `add`(그래도 새로 저장)·`update:<id>`(그 기억에 합침)·`supersede:<id>`(그 기억을 대체)·`skip`(저장하지 않음). 같은 제목이면 출처를 그 기억에 더하고, 같은 범위·주제의 현재값은 대체한다(위). `idempotencyKey`를 주면 같은 키로 다시 불러도 한 번만 적용한다(재시도 안전).
- 출처 등급(`sources[].trust`): `owner`(사용자 본인 말)·`agent`(호스트 AI가 배운 것)·`untrusted`(웹·도구 출력·남의 글·로그)·`system`(자동화). 통로가 상한을 정한다: MCP·호스트(`orbit-mem tool`)로 저장한 출처는 `agent`까지이고, `sources[].trust`로 낮출 수만 있다(올리지 못한다). 기억의 등급은 출처 가운데 가장 높은 것이다. 출처가 `untrusted`·`system`뿐인 기억은 확정·상시 핵심 승격이 안 되고 `owner`·`agent` 기억을 대체하지 못하며, 회상에 「믿을 수 없음」 표시가 붙는다.
- 주장 하나: `memory_save`·`memory_update`에서 규칙·결정 본문이 1,000자를 넘으면 나눠 저장하라고 거절한다.
- 도구 트리거(`tools`): 도구 이름(`mcp__atlas__write_page`)이나 `*`로 끝나는 접두(`mcp__atlas__*`), 10개까지. `memory_save`·`memory_update`가 받는다.
- 나이 감쇠: 요청 회상에서 관련도가 같으면 마지막으로 바뀌거나 다시 나온 지 30일 안 → 180일 안 → 그 뒤 순으로 앞에 둔다. 관련도가 먼저다.
- 회상 품질 고정 시험: `internal/inject/testdata/recall_fixtures.json`(모델 호출 없음). 회상 규칙을 바꾸면 이 묶음이 같은 답을 내는지 먼저 본다.
- 저장 거부(`memory_save`·`orbit-mem tool memory_save`): 비밀값, 원본 로그·스택 추적(세 줄 이상), 대화 전사(역할 표시 두 줄 이상), 모델을 향한 지시문(이전 지시 무시·시스템 프롬프트), 지금 일의 진행 상태(사용자 말 인용이 아닐 때)는 저장하지 않고 까닭을 돌려준다. 「3주 뒤」·「내일」·「in 3 weeks」 같은 상대 시간은 제목·본문에서 날짜로 바꾸고 `rewrites`로 알린다. 규칙(일하는 방식)은 바꾸지 않는다(「배포 3일 전」은 사건 기준이고, 제목이 바뀌면 다른 날 다시 저장한 같은 규칙이 모이지 않는다). 출처 발췌는 그대로 둔다. 들여오기의 저장에는 적용하지 않는다.
- 스키마: `memories`에 `retention`·`review_days`·`review_at`·`expires_at`·`topic`·`tools`·`pinned`(0.15.0) 열과 색인 `memories_topic`, `activity`에 `summary`(0.16.0, 활동 기록으로 옮긴 일지의 본문) 열, 표 `session_marks`(세션에 한 번만 알린 정정·새 기억·도구 회상)가 더해진다(`user_version`은 4 그대로). 새 판이 처음 열 때 `before-schema-*.sqlite` 사본을 남기고 열을 더한다. 옛 판 실행 파일은 열 이름을 대어 읽고 쓰므로 같은 DB를 함께 써도 깨지지 않고, 옛 판이 넣은 줄은 기본값(보통·날짜 없음·주제 없음)이 된다.

## 확정 규칙과 감사 목록

2026-11-01 사용자 결정 A2(Atlas 설계 · orbit-code 자율 완수와 학습 「확정 규칙」, 계획 · AU-6). 규칙은 저장 계층(`internal/memory`, MCP, CLI)에 있어, 하루 정리든 다른 자동화든 이 API를 거치면 같은 규칙을 지킨다. 옛 「승인 없는 자동 쓰기 금지」·「확정은 인용이나 승인으로만」을 대신한다.

- 사용자 인용 보호: 출처에 사용자 말(`quote`)이 있는 기억은 사람(`user`)이 아니면 고치기·확정·낡음·대체·합치기·(자동화의) 연결·태그를 하지 못한다(`ErrProtected`). 사용자 말(`userQuote`)을 주면 되고, 그 말이 새 출처로 남는다.
- 증거 확정: 후보는 회상에 쓰되 확정 뒤에 두고 「후보」로 표시한다. 증거가 하나라도 있으면 스스로 확정하고 원장에 남긴다. ① 서로 다른 날 다시 나옴(`confirmSeenDays`, 기본 2) ② 회상돼 따랐고 그 일이 검증을 통과했으며 그 세션에 교정·어김이 없는 세션 수(`confirmFollowedVerified`, 기본 2) ③ 작업 검증이 직접 확인한 프로젝트 사실(출처 `verification`, 종류 `fact`, 적용 대상 user가 아닌 것: 저장소·제품·제품 미정). ②의 사용 판정은 걷어낸 세션 끝 회고만 남겼으므로 0.17.0부터 새로 쌓이지 않는다(옛 판정은 계속 센다). 열린 어긋남 표시가 있거나 사람이 자동 확정을 되돌린 기억은 다시 자동 확정하지 않는다.
- 자동 변경: 자동화의 확정·합치기·낡음·대체(`supersede`)·연결·태그·고치기(`edit`)·재분류(`reclassify`)와 자동 변경의 되돌리기는 승인 없이 적용되고, 제안 표에 상태 `auto`로, 효과 원장에 한 줄로 남는다(`SubmitChange`·`AutoApply`). 사람 승인 대기열은 없다(0.13.0, Atlas 설계 · orbit-mem 2판 18절). 적용하지 않는 것은 결과에 까닭(`reason`)을 돌려준다: 증거 없는 확정(후보로 남음), 사용자 인용 기억·사람이 한 변경·지침 파일·플러그인 원본(하네스) 파일에 닿는 변경(`askUser`: 일하는 중에 사용자에게 묻고 `userQuote`로 다시), 방향(`direction`). 사람이 거절한 제안과 되돌린 자동 변경은 자동화가 다시 하지 않는다. 효과 판정은 교정·어김 → 상관없음 차례로 견준다. 효과 판정이 「해로움」인 자동 변경은 하루 정리가 되돌리고(`RevertHarmfulAuto`) 사건 `audit.auto_reverted`와 세션 시작 한 줄로 알린다.
- 반복 교정: 교정 사건에 대상(`memory`·`skill`·`role`)을 둔다. 대상마다 교정이 나온 세션 수가 `correctionRepeatSessions`(기본 3) 이상이면 감사 목록에 보이고, 스킬·역할이면 하네스 개선 후보다. 확인함을 누르면 그 뒤 교정만 다시 센다.
- 작업 회고(AU-5)는 0.17.0에서 걷어냈다. 그 회고가 남긴 기억(출처 `work`, ref = 작업 id)은 그대로다. 태그 `procedure`(「절차」)인 교훈이 서로 다른 작업 `correctionRepeatSessions`개 이상에서 나오면(출처 `work`의 ref 수, 확인함 뒤만) 하네스 개선 후보(`correction:procedure:<기억 id>`)로 감사 목록에 보인다.
- 기준 횟수 `confirmFollowedVerified`·`correctionRepeatSessions`는 E2로 이 PC 기억 DB 사본(2026-10-26~11-01)을 재서 정한 추천값이며 사용자 결정 대기다(근거는 계획 · AU-6). `orbit-mem config set <키> <1-30>`으로 바꾼다.
- 사람의 결정: 사람이 아닌 쪽(AI)이 사용자 인용 기억에 닿는 남은 옛 대기 제안을 승인·거절하거나 어긋남 표시를 닫으려면 `userQuote`가 있어야 한다. 사람이 확정 기억을 후보로 내리면 보류(`hold`)로 남아 자동화가 다시 확정하지 않는다(사람이 확정하면 풀린다).
- 표: `audit_flags`(어긋남 `conflict`·보류 `hold`), `audit_acks`(반복 교정 확인함), 색인 `events_kind`. 스키마 판 4.
- 한계: actor는 서버·명령을 띄운 쪽이 정한다. 호스트 AI가 셸로 `orbit-mem edit`를 직접 부르면 사람(user)으로 쓰인다. 대화 중 AI가 새 기억을 저장하며 사용자 인용 기억에 잇는 연결은 기억 내용을 바꾸지 않아 막지 않는다. 출처 `quote`·`verification`은 쓰는 쪽이 붙이는 표시이므로, 스킬·MCP 안내가 「사용자 말 그대로」「실제로 돌린 검증」에만 쓰라고 한다(`verification`은 `ref`·`excerpt`가 있어야 증거다).

## 언어

`orbit-mem`이 사람에게 보이는 출력·오류·도움말은 영어가 기본이고 한국어로 바꿀 수 있다(`en`·`ko`). `orbit-mem config set language ko`로 바꾸고, `ORBIT_LANG=en|ko`가 있으면 설정보다 먼저 따른다. JSON 출력과 MCP 응답 구조, AI가 읽는 글(MCP 도구 설명, 세션 주입), 기억·제안 본문은 언어 설정과 무관하다. 문구는 `internal/i18n/locales/<언어>/*.json`에 키로 두고, 언어마다 같은 키와 같은 서식 자리를 갖는다(시험으로 확인).

Codex는 플러그인 훅을 사용자가 `/hooks`에서 신뢰해야 돌리고, 훅이 바뀌면 신뢰가 풀린다. 새 플러그인을 깐 뒤에는 Codex에서 `/hooks`를 열어 다시 신뢰한다.

## 데이터

- 위치: `ORBIT_HOME`, 없으면 Mac `~/Library/Application Support/orbit`, Windows `%AppData%\orbit`.
- `memory.db`(SQLite: 기억·제안(자동 변경 포함)·지표용 사건·효과 원장·활동 기록·감사 표시·`meta`. 걷어낸 회고·실험실의 표 `retros`·`session_ends`·`retro_coverage`·`judgments`·`lab_experiments`·`lab_runs`는 형식을 바꾸지 않으려고 그대로 둔다), `config.json`, `backups/`(아래 「백업」), `logs/`(`backup.log`·`tidy.log`).
- 옛 `retro/`·`lab/` 폴더와 `work.db`는 0.17.0부터 쓰지도 읽지도 않는다. 지우지 않는다.
- `config.json`에서 이 판이 모르는 키(걷어낸 회고 설정 `retroHost`·`retroModel`·`retroHour`·`sessionRetro*`·`graphTidy*`·`legacy*`·`claudePath`·`codexPath`·`applyTargets`·`pluginSource`·`retiredNames`, 옛 작업 기록 설정 `work`·`workStaleAfter`·`workTestCommands` 등)는 읽을 때 무시하고, 다시 저장할 때 파일에 있던 그대로 남긴다.
- PC끼리 공유하지 않는다.

## 백업

`backups/`에 기억 DB 사본을 둔다. 사본은 SQLite `VACUUM INTO`로 만든다: 읽기 트랜잭션 하나의 일관된 모습을 임시 이름(`*.tmp`)에 쓰고 다 쓴 뒤에 이름을 바꾸므로, 쓰는 쪽(훅·MCP)을 막지 않고 덜 쓴 사본이 이름을 얻지 않는다.

| 사본 | 언제 | 정리 |
| --- | --- | --- |
| `memory-YYYYMMDD.sqlite`(하루 사본) | 세션 시작 훅이 마지막 하루 기억 사본이 24시간보다 오래됐을 때(사본이 없을 때 포함) 뒤에서, 사람이 `orbit-mem backup`을 실행할 때(규칙과 관계없이). 옛 `work-YYYYMMDD.sqlite`는 0.17.0부터 만들지 않는다 | 이름 꼴마다 `backupKeep`(기본 14)개를 넘는 가장 오래된 것부터 지운다. 정확히 이 이름 꼴인 파일만 지운다 |
| `before-schema-*`·`before-restore-*`·`before-import-*`·`before-migrate-*.sqlite` | 스키마 이전(새 판이 기억 DB의 표·열·`user_version`을 바꿀 때)·복구·들여오기·옮기기 바로 전, 하루 규칙과 관계없이 | 지우지 않는다 |

- 세션 시작 훅은 폴더를 한 번 읽어 24시간 규칙만 보고, 사본이 필요하면 `orbit-mem backup auto`를 따로(새 세션, 입출력 없이) 띄운 뒤 기다리지 않는다. `backup auto`는 `backups/.backup.lock`(OS 파일 잠금)을 잡은 뒤 규칙을 다시 보고, 다른 프로세스가 잠금을 쥐고 있으면 건너뛴다. 실패는 `logs/backup.log`에 남기고 훅은 언제나 종료 0이다. `orbit-mem config set autoBackup false`로 끈다(기본 켜짐).
- 스키마 이전 전 사본을 만들지 못하면 스키마를 바꾸지 않고 기억 DB 열기가 실패한다.
- `orbit-mem backup list`: 사본(이름·종류·시각·크기)을 최신순으로. `orbit-mem backup status`: 폴더, 자동 켜짐, 보관 개수, 마지막 하루 사본 시각·개수, 다음 자동 사본 시각. 둘 다 파이프면 JSON이다.
- `orbit-mem backup restore <사본 이름 또는 경로> [--yes]`: 먼저 무엇을 무엇으로 바꾸는지(기억 수와 함께) 출력한다. `--yes`가 없으면 터미널에서는 묻고, 터미널이 아니면 바꾸지 않고 종료 1이다. 사본의 무결성(`PRAGMA integrity_check`)과 기억 표를 확인한 뒤, 지금 DB를 `before-restore-*.sqlite`로 남기고, SQLite 온라인 백업 API로 사본을 한 트랜잭션에 들여온다. 파일을 갈아 끼우지 않으므로 열려 있는 MCP 서버도 바뀐 내용을 본다. 들여온 뒤 다시 검사해 실패하면 남긴 사본으로 되돌린다.

## 하루 정리

기획 · orbit-mem 4.3(H1~H4). 모델이 필요 없는 정리를 하루 한 번 돌린다. 예전에는 회고 마무리가 돌렸다.

- 세션 시작 훅은 기억 DB `meta`의 `tidy.day`가 오늘이 아니면 `orbit-mem tidy auto`를 따로(새 세션, 입출력 없이) 띄운 뒤 기다리지 않는다. `tidy auto`는 `meta`에 오늘을 먼저 쓴 프로세스만 정리한다(한 문장의 조건부 쓰기): 두 세션이 함께 열려도 하루 한 번만 돈다. 실패는 `logs/tidy.log`에 남기고 다음 날 다시 한다. 훅은 언제나 종료 0이다.
- 차례: 관찰 기간이 끝난 변경의 효과 판정(`JudgeLedger`) → 해롭다고 판정된 자동 변경 되돌리기(`RevertHarmfulAuto`) → 증거가 생긴 후보 확정 → 끝난 제안 닫기 → 기한 지난 휘발 기억 낡음 → `candidateExpiryDays`일 쓰지 않은 후보 낡음(`inject`가 `full`일 때만) → 제품 지도에 맞는 `product:?` 기억 옮기기 → 30일 지난 세션 묶음 지우기. 바꾼 것은 모두 자동 변경으로 원장에 남고 감사 목록에서 되돌린다.
- `orbit-mem config set autoTidy false`로 끈다(기본 켜짐). 사람은 `orbit-mem tidy`로 지금 돌린다. `doctor`가 켜짐과 마지막 날을 보인다.

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
| `internal/activity` | 활동 기록 찾기 |
| `internal/mcpserver` | MCP 서버(기억·활동 기록 도구)와 얇은 CLI가 부르는 `Tools` |
| `internal/inject` | 세션 시작 묶음·요청 회상 |
| `internal/transcript` | 호스트 데이터 폴더와 호스트 자체 기억 파일 찾기(겹치는 기억 판정) |
| `internal/memplugin` | 플러그인 원본(`source/`, go:embed)과 Claude·Codex 꼴·마켓 빌드 |
| `internal/install` | 실행 파일 복사, 호스트 플러그인 등록·해제·상태(`hosts.go`) |
| `internal/config`, `internal/i18n`, `internal/sqlitedb`, `internal/project` | 설정, 문구, SQLite 열기, 프로젝트 범위 |
| `internal/backup` | 백업 폴더의 사본 이름·목록·보관 정리·24시간 규칙·잠금 |

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

기계가 읽는 꼴이다. 키는 함부로 바꾸지 않는다(0.17.0에서 회고 키 `sessionQueue`·`lastRetros`를 빼고 `tidy`를 더했다).

| 키 | 뜻 |
| --- | --- |
| `version`, `binary` | 이 `orbit-mem`의 판과 경로 |
| `pathBinary` | 훅·MCP가 부를 PATH의 `orbit-mem`(없으면 빈 문자열) |
| `home`, `db`, `config` | 데이터 경로, `memory.db`가 있는지, 설정 |
| `memories`, `pendingProposals`, `activity` | 상태별 기억 수, 남은 옛 대기 제안(0.13.0부터 새로 생기지 않는다), 활동 기록 세션 수(`sessions`) |
| `tidy` | 하루 정리: `auto`(설정 `autoTidy`), `lastDay`(마지막으로 정리한 날) |
| `lastBackup` | 마지막 기억 하루 사본 경로 |
| `backups` | `dir`, `auto`, `keep`, `count`(기억 하루 사본 수), `last`·`lastPath`·`lastAt`, `due`, `nextAfter`, `workCopies`, `snapshots` |
| `update` | `current`, `latest`(하루 한 번 세션 시작 훅이 남긴 캐시 `update-check.json`의 최신 판, 없으면 빈 문자열), `checkedAt`, `available`(새 판이 있는가), `enabled`(설정 `updateCheck`). doctor는 네트워크를 하지 않고 캐시만 읽는다 |
| `claude` | `path`, `plugin`(`installed`·`missing`·`unknown`·`no-host`), `version`, `enabled`, `marketplace`(`github:<owner/repo>`·`directory:<경로>`) |
| `codex` | `path`, `config`(읽은 `config.toml`), `marketplace`, `plugin`(`installed`·`missing`), `enabled`, `hooksTrusted` |
| `problems` | `[{id, message, fix}]`. `id`: `not-on-path`, `db-missing`, `claude-plugin-missing`, `claude-plugin-disabled`, `claude-plugin-version`, `codex-plugin-missing`, `codex-plugin-disabled`, `codex-hooks-untrusted`, `claude-auto-memory`(Claude Code 자체 기억이 켜짐: `autoMemoryEnabled`가 false가 아니고 `CLAUDE_CODE_DISABLE_AUTO_MEMORY`도 없음), `codex-memories`(Codex `features.memories`·`memories.generate_memories`·`memories.use_memories` 중 하나가 true), `claude-memory-plugin`·`claude-memory-mcp`·`codex-memory-plugin`·`codex-memory-mcp`(알려진 기억 플러그인·MCP 서버가 켜짐), `host-settings-unreadable`(겹침 판정에서 읽지 못한 설정 파일), `old-proposals`(0.13.0에서 없앤 승인 대기열에 남은 옛 제안: `orbit-mem backup && orbit-mem proposals drain`), `binary-older-than-db`(`orbit-mem update`)·`migration-needed`(`orbit-mem migrate`), `index-stale`(옛 orbit-mem이 쓴 기억이 검색 색인에 아직 없음, 메시지에 건수: `orbit-mem migrate`가 채운다. 판이 맞는 DB에서만 낸다). 겹치는 기억의 `fix`는 `orbit-mem host-memory off --only <id>`(Claude MCP는 `claude mcp remove …`, 관리·프로젝트 설정이 켠 것은 그 파일)이고, 사용자가 유지하기로 했으면(`hostMemory` `keep`) 문제로 세지 않는다 |

## 릴리스

`scripts/release.sh`가 `dist/<버전>/`에 darwin·linux arm64·amd64, windows amd64(빌드만) 자산(`orbit-mem-<버전>-<os>-<arch>`)과 `SHA256SUMS`를 만든다. `--publish`는 깨끗한 `origin/main` 끝에서 태그 `v<버전>`으로 GitHub Release를 올린다(노트는 `RELEASE_NOTES` 파일). 서명이 없으므로 내려받은 바이너리는 `xattr -d com.apple.quarantine orbit-mem`으로 격리 속성을 풀고 `orbit-mem install`을 실행한다.

릴리스 전에 `internal/version`의 `Version`, `plugin/.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`의 판이 모두 같아야 한다(`scripts/release.sh`와 `go test`가 어긋남을 잡는다). Claude Code는 플러그인의 `version`을 업데이트 판단의 기준으로 쓰므로 판을 올리지 않으면 플러그인이 갱신되지 않는다. `curl`로 받은 파일에는 macOS 격리 속성이 붙지 않지만 브라우저로 받은 파일에는 붙으니 그때는 `xattr -d com.apple.quarantine orbit-mem`을 쓴다.

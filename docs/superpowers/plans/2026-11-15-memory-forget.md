# 기억 완전 삭제 (`memory_forget`) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**목표:** AI가 사용자 말을 받아서, 사람은 명령줄로 기억 1~20개를 DB에서 완전히 지운다. 지운 기억은 다시 들어오지 못한다.

**구조:**
- 삭제 규칙과 표 정리는 `internal/memory`의 `Store.Forget` 한 트랜잭션에 둔다.
- 원장·제안 글 비우기와 되살아남 막기는 그 둘레에 붙인다.
- MCP 도구는 얇은 처리기로 둔다. 명령줄은 `corePin`처럼 저장소를 직접 부른다.
- 변경 축은 「지울 때 같이 정리할 표」다. 그래서 그 목록을 한 곳(`forgetTables`)에 모으고, 새 표가 목록에서 빠지면 실패하는 시험으로 지킨다.
- 새 포트나 인터페이스는 만들지 않는다. 외부 경계가 없기 때문이다.

**기술:** Go 1.27.1, SQLite(modernc), MCP go-sdk. 새 의존성은 없다.

**명세:** `docs/superpowers/specs/2026-11-15-memory-forget-design.md` (승인 2026-11-15, 계획 검토 뒤 5.1·5.3·5.4·7절을 다듬음). 실행자는 명세와 이 계획을 함께 읽는다.

## 전역 제약

- 스키마 판(`user_version`)은 올리지 않는다. 새 표는 `CREATE TABLE IF NOT EXISTS`로 더한다(README.md:256 방식).
- `HostTools`(internal/mcpserver/tools.go:123)와 `contract/host/v1/*`는 바꾸지 않는다.
- `memory_forget` 표시는 `writeHints(true, false)`다.
- 한 번에 지울 수 있는 id는 1~20개다. 이 검사는 저장 계층(`Store.Forget`)에만 두고 MCP 처리기에서 다시 하지 않는다.
- `userQuote`·`reason`은 각 500자 이하다. `MaskSecrets(s) != s`면 `&RejectError{Reason: RejectSecret}`로 거절한다. 비밀값만 거른다(명세 5.1).
- 쓰는 쪽 검사는 닫힌 쪽으로 한다.
  - `ActorAutomation`이면 늘 거절한다.
  - 사람이 아니면(`!who.human()`) 말을 요구한다.
  - 알 수 없는 actor도 말을 요구한다.
- DB에 저장하는 글은 memory 패키지의 한국어 상수로 둔다. 저장소 관례가 그렇다(ledger.go:450, supersede.go:75). 대상은 낡음 사유, 원장 변경 글, 제안 제목이다. `i18n.T`는 돌려주는 메시지에만 쓴다.
- `i18n.Err` 키는 형식 인자를 받지 않는다(i18n_test.go:217-221). id를 붙일 때는 `fmt.Errorf("%w: %s", ErrX, id)`로 한다(ledger.go:419 선례).
- 카탈로그에 넣은 키는 같은 Task의 코드가 써야 한다. `TestCodeKeysExist`(i18n_test.go:222-226)는 쓰지 않는 키를 실패로 본다.
- `plugin/`은 손으로 고치지 않는다. `go run ./cmd/orbit-mem plugin build --repo .`로 다시 만든다.
- 시험 이름은 `Test대상_한글_설명`으로 짓는다(asof_test.go:94).
- 시험은 임시 DB와 임시 `ORBIT_HOME`만 쓴다.
- 사용자에게 보이는 글에 개인 작업 환경 이야기를 쓰지 않는다.

## 검토 초점

- 같은 id가 `ids`에 두 번 들어오면 하나로 보고 한 번만 지운다(Task 1).
- 대체 사슬 A→B→C에서 B를 지우면 A의 `superseded_by`만 비고 C는 그대로다. B와 A를 함께 지우면 A는 고치지 않고 `Touched`에도 넣지 않는다(Task 1).
- 서로 연결된 두 기억을 한 번에 지우면 오류가 없다(Task 1).
- `userQuote`가 공백뿐이면 빈 말로 보고 거절한다(Task 1).
- 지운 직후 같은 연결에서 다른 기억의 `memory_get`과 `memory_find`가 오류 없이 돌고, links에서 지운 기억이 빠진다(Task 4).

---

## 진행 순서

| 묶음 | Task | 선후 | 역할 | 검토 |
| --- | --- | --- | --- | --- |
| 1 | Task 1 `Store.Forget` 핵심 | 먼저 | `worker-deep` | `reviewer` |
| 2 (동시, worktree) | Task 2 원장·제안 글 비우기 | Task 1 뒤 | `worker` | `reviewer` |
| 2 (동시, worktree) | Task 3 되살아남 막기 | Task 1 뒤 | `worker` | `reviewer` |
| 2 (동시, worktree) | Task 4 MCP 도구 | Task 1 뒤 | `worker` | `reviewer` |
| 3 | Task 5 명령줄·복구 안내·문서 | Task 2~4를 합친 뒤 | `worker` | `reviewer` |
| 끝 | 전체 시험·전체 검토 | Task 5 뒤 | `qa` → `architect` | — |

- 묶음 2에서 쓰는 파일은 이렇다.
  - Task 2: `forget_audit*.go`, `ledger.go`, `auto.go`, `proposal.go`, 그리고 `forget.go` 한 줄
  - Task 3: `transfer.go`, `idem.go`, `migrate.go`, 각 시험
  - Task 4: `internal/mcpserver`
- 세 Task 모두 locale 파일을 고치지 않는다.
- 합치는 순서는 Task 2 → 3 → 4다. 하나 합칠 때마다 그 Task의 시험을 다시 돌리고, 셋을 다 합친 뒤 `go test ./internal/... -count=1`을 돌린다.

## 허용 위험 단계

| 단계 | 허용 | 비고 |
| --- | --- | --- |
| 로컬 브랜치 `feat/memory-forget` 만들기 | 허용 | `main`에서 갈라진다 |
| 그 브랜치와 Task worktree 브랜치에 커밋 | 허용 | 경로를 명시해 `git add`. 작성자 `crispebble` 확인 |
| Task worktree 만들기·정리 | 허용 | `.claude/worktrees/` 아래 |
| worktree 브랜치를 `feat/memory-forget`에 로컬 병합 | 허용 | |
| `go test`·`go vet`·`plugin build --repo .` 실행 | 허용 | |
| 명세·계획 문서 커밋 | 허용 | 첫 커밋 |
| 푸시, 원격 브랜치, PR | 금지 | 끝나면 묻는다 |
| 버전 올리기, 릴리스, 태그 | 금지 | |
| 이 PC에 orbit-mem 설치·`update`·`install` | 금지 | |
| 이 PC의 실제 기억 DB 쓰기(낡은 orbit-cc 기억 정리 포함) | 금지 | 릴리스 뒤 따로 한다 |
| `main` 직접 커밋 | 금지 | |

---

### Task 1: `Store.Forget` 핵심

**파일:**
- 만들기:
  - `internal/memory/forget.go`
  - `internal/memory/forget_test.go`
  - `internal/memory/forget_guard_test.go`
  - `internal/inject/forget_test.go`
- 고치기:
  - `internal/memory/store.go`: 스키마 묶음(store.go:186 근처)에 `forgottenSchema`를 더한다.
  - `internal/memory/rules.go`: 오류 변수를 더한다.
  - `internal/i18n/locales/{en,ko}/memory.json`: 이 Task가 쓰는 키만 넣는다.

**인터페이스:**
- 쓰는 것:
  - `Who`, `human()`, `quote()`, `ActorAutomation`(rules.go:22-45)
  - `unindexTx(tx, id)`(asof.go:450)
  - `indexTx`·`syncIndexTx`(index.go:97·209)
  - `MaskSecrets`, `RejectError`, `RejectSecret`
  - `ErrNotFound`, `ErrInvalid`, `ErrProtected`
  - `Core(scope)`(core.go:58)
  - `JournalHost`·`JournalSession(id)`(journals.go:18-25)
- 내놓는 것:
  - `const forgottenSchema`
    - 표: `forgotten(id TEXT PRIMARY KEY, legacy_id TEXT, at INTEGER NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', quote TEXT NOT NULL DEFAULT '')`
    - 색인: `forgotten_legacy ON forgotten(legacy_id) WHERE legacy_id IS NOT NULL`
  - 오류 변수(rules.go). 선언 자체가 키를 쓰는 것으로 잡힌다.
    - `ErrForgetNeedsWords = i18n.Err("memory.err.forget_words")`
    - `ErrForgetAutomation = i18n.Err("memory.err.forget_automation")`
    - `ErrForgotten = i18n.Err("memory.err.forgotten")`: Task 3이 쓴다.
    - `ErrLedgerForgotten = i18n.Err("memory.err.ledger_forgotten")`: Task 2가 쓴다.
  - 메시지 키 `memory.forget.missing`(`%s`, 없는 id 목록), `memory.forget.count`(1~20개), `memory.forget.too_long`(500자). 모두 forget.go가 쓴다.
  - 한국어 상수(forget.go):
    - `forgetSuccessorGone = "대체한 기억을 지웠다"`
    - `forgetLedgerChange = "지운 기억 %s에 대한 변경"`: Task 2가 쓴다.
    - `forgetProposalTitle = "지운 기억 %s에 대한 제안"`: Task 2가 쓴다.
  - `type ForgetInput struct { IDs []string; Reason string; Who Who }`
  - `type ForgetResult struct { Forgotten []string; Touched []string }`
  - `type ForgetMissingError struct{ IDs []string }`
    - `Error()`는 `i18n.T("memory.forget.missing", strings.Join(IDs, ", "))`를 돌려준다.
    - `Unwrap() error`는 `ErrNotFound`를 돌려준다.
  - `func (s *Store) Forget(in ForgetInput) (ForgetResult, error)`
  - `func (s *Store) IsForgotten(id, legacyID string) (bool, error)`
  - `func forgottenTx(q querier, id, legacyID string) (bool, error)`
    - `forgotten` 표가 없으면(no such table) 거짓을 돌려준다.
    - `noSaveRequests`(idem.go:66-69) 꼴의 `noForgottenTable(err)`을 쓴다.
  - `var forgetTables`: 지울 `(표, 열, 방식)` 목록. 방식은 `delete`(그 열이 id인 행 삭제)나 `custom`(Forget 안에서 따로 처리)이다.
  - `var keepTables`: id를 남기는 `(표, 열)` 목록. `save_requests.memory_id`, `session_bundles.memory_ids`, `forgotten.id`, 원장·제안(Task 2) 등이다.

- [ ] **1단계: 실패하는 시험 쓰기** (`newTestStore(t)`, store_test.go:17)
  - `TestForget_사용자_말과_함께_지우면_기억과_딸린_행이_모두_없다`:
    - 출처·연결·판 이력·판단 기록·감사 표시·세션 표시가 있고, 일지로 옮겨진 활동 줄이 있는 기억을 준비한다. 지우기 전에 `memory_fts` rowid를 잡아 둔다.
    - `Who{Actor: ActorAI, UserQuote: "지워"}`로 지운다.
    - `Get`이 `ErrNotFound`다.
    - `forgetTables`의 모든 `(표, 열)`에서 그 id로 세면 0이다. 잡아 둔 FTS rowid도 0행이다.
    - correction·memory.used 사건이 0이다. LIKE 꼴은 rules.go:223-224를 따른다.
    - `Find`와 `Export`의 결과에 그 id가 없다.
    - `forgotten`에 1행이 있고, `quote`는 "지워", `legacy_id`는 그 기억의 `LegacyID`다.
  - `TestForget_AI가_말_없이_부르면_거절하고_아무것도_안_지운다`:
    - `UserQuote`가 ""일 때와 "  "일 때 둘 다 거절한다.
    - `errors.Is(err, ErrForgetNeedsWords)`와 `errors.Is(err, ErrProtected)`가 모두 참이다.
    - 기억은 그대로 있다.
  - `TestForget_자동화는_말이_있어도_거절한다`: `ErrForgetAutomation`이다.
  - `TestForget_알_수_없는_actor는_말을_요구한다`: `Actor: "x"`이고 말이 없으면 거절한다.
  - `TestForget_사람은_말_없이_지운다`: `Who{}`면 성공한다.
  - `TestForget_없는_id가_섞이면_아무것도_안_지우고_그_id를_알린다`: `errors.As`로 `*ForgetMissingError`를 꺼내면 `IDs`가 `["m-없음"]`이다. 함께 준 기억은 남아 있다.
  - `TestForget_id가_0개나_21개면_거절한다`: `ErrInvalid`.
  - `TestForget_같은_id를_두_번_주면_한_번만_지운다`: `len(Forgotten) == 1`.
  - `TestForget_새_판을_지우면_옛_판의_대체_표시가_비고_낡음은_그대로다`:
    - A를 B가 대체한 상태에서 B를 지운다.
    - A의 `SupersededBy`가 "", `State`가 낡음, `StaleReason`이 `forgetSuccessorGone`이다.
    - `Touched`가 `[A]`다.
    - A의 `memory_versions` 줄 수는 그대로다.
    - `Find`로 A의 주제를 찾아도 A가 회상되지 않는다.
  - `TestForget_대체_사슬_가운데를_지우면_앞쪽만_고친다`: A→B→C에서 B를 지운다. A의 `SupersededBy`는 "", C는 바뀌지 않는다.
  - `TestForget_대체한_쪽과_된_쪽을_함께_지우면_Touched가_비어_있다`.
  - `TestForget_서로_이은_두_기억을_함께_지운다`: 오류가 없고 `memory_links`가 0행이다.
  - `TestForget_상시_핵심_기억을_지우면_핵심_목록에서_빠진다`: `Core(scope)` 결과에 그 id가 없다.
  - `TestForget_말이나_이유에_비밀값이_있으면_거절한다`:
    - `MaskSecrets`가 가리는 토큰을 넣으면 `RejectError`이고 `Reason`이 `RejectSecret`이다.
    - 501자 `Reason`은 `ErrInvalid`다.
  - `TestForget_이미_forgotten에_있는_id를_다시_지워도_성공한다`: 옛 판으로 들여온 경우를 흉내 낸다. `forgotten`에 행을 미리 넣고 같은 id의 기억을 만든 뒤 지운다.
  - `TestForgetGuard_기억_id_열을_가진_표는_지움이나_남김_목록에_있다`(`forget_guard_test.go`):
    - `sqlite_master`와 `pragma_table_info`를 훑어, `memory_id`·`other_id`·`memory_ids` 열을 가진 표와 `memory_links`를 찾는다.
    - 그런 표가 `forgetTables`에도 `keepTables`에도 없으면 실패한다.
    - 선례는 `index_guard_test.go`다.
  - `TestForget_다음_세션_시작_묶음에서_지운_핵심_기억이_빠진다`(`internal/inject/forget_test.go`): 핀으로 올린 기억을 지운 뒤 `SessionStart(s, cfg, scope)`(inject.go:210)의 결과에 그 제목이 없다.
- [ ] **2단계: 실패 확인** — `go test ./internal/memory ./internal/inject -run 'TestForget' -count=1` → 컴파일 실패 또는 FAIL
- [ ] **3단계: 구현** — `forget.go`
  - 순서는 이렇다.
    1. 개수 검사, 중복 제거, actor·말 검사, 길이·비밀값 검사.
    2. 트랜잭션을 시작한다.
    3. 없는 id를 모은다. 하나라도 있으면 롤백하고 `ForgetMissingError`를 돌려준다.
    4. 각 기억의 `legacy_id`를 읽어 둔다.
    5. 삭제 묶음 밖에서 `superseded_by`가 지울 id인 기억을 고친다. 행만 고치고, 같은 tx에서 `indexTx`를 부른다(index.go:33-46 주석).
    6. `forgetTables`를 돌며 행을 지운다(`unindexTx` 포함).
    7. `forgotten`을 upsert한다.
    8. 커밋한다.
  - 모든 읽기와 쓰기는 `tx`로 한다. `s.db`를 쓰는 도우미(`LedgerEntry`, `GetProposal`, `isAuto`)는 부르지 않는다.
  - 옛 판의 판 이력은 더하지 않는다(명세 7절).
- [ ] **4단계: 통과 확인** — `go test ./internal/memory ./internal/inject ./internal/i18n -count=1` → PASS
- [ ] **5단계: 커밋** — `git add` 위 파일들 → `feat: Forget memories for good in one transaction`

### Task 2: 원장·제안 글 비우기와 되돌리기 거절

**파일:**
- 만들기: `internal/memory/forget_audit.go`, `internal/memory/forget_audit_test.go`
- 고치기:
  - `internal/memory/forget.go`: `Forget` tx 안에서 `scrubAuditTx(tx, ids)`를 부르는 한 줄. 행 삭제 앞에 둔다.
  - `internal/memory/ledger.go`:
    - `LedgerBefore`(ledger.go:41)에 `Forgotten []string \`json:"forgotten,omitempty"\``를 더한다.
    - `snapshotFor`의 되돌림 분기 첫 줄(ledger.go:247)에서 `revertableErr`를 부른다.
  - `internal/memory/auto.go`: `RevertChange`(auto.go:271)에서 `ledgerFor` 바로 뒤(auto.go:272), 되돌림 제안 줄을 넣기 전에 `revertableErr`를 부른다.
  - `internal/memory/forget_guard_test.go`: `keepTables`에 원장·제안을 적는다.

**인터페이스:**
- 쓰는 것: Task 1의 `Forget`, `forgetLedgerChange`, `forgetProposalTitle`, `ErrLedgerForgotten`, `keepTables`, `StatusDropped`(queue.go:14).
- 내놓는 것:
  - `func scrubAuditTx(tx *sql.Tx, ids []string) error`
  - `func revertableErr(b LedgerBefore) error`: `b.Forgotten`이 비어 있지 않으면 `fmt.Errorf("%w: %s", ErrLedgerForgotten, strings.Join(b.Forgotten, ", "))`를 돌려준다.

- [ ] **1단계: 실패하는 시험 쓰기**
  - 준비 선례는 evidence_test.go:337-346이다. `s.AutoApply(auto(AutoInput{Kind: ProposalStale…}))`, `UPDATE ledger SET verdict=?`, `s.RevertHarmfulAuto()`가 있다. 합치기 제안은 같은 파일 386-390 꼴이다.
  - `TestForgetAudit_지운_기억의_원장_줄은_남고_글이_비워진다`:
    - 원장 줄 수가 그대로다.
    - `change`와 `basis`가 `fmt.Sprintf(forgetLedgerChange, id)`다.
    - `before.Memories`에 그 id의 스냅숏이 없다.
    - `before.Created`와 `targets`의 id는 남아 있다.
    - `before.Forgotten`이 `[id]`다.
    - 짝인 제안의 `title`이 `fmt.Sprintf(forgetProposalTitle, id)`이고, `body`와 `payload`는 빈 값이다.
  - `TestForgetAudit_대체_표시만_담긴_원장_줄도_찾는다`:
    - A를 B가 대체한 뒤 A를 고친 원장 줄은 `targets`가 `[A]`다.
    - B를 지우면 그 줄의 `before`에서 `"supersededBy":"B"`를 담은 A 스냅숏이 빠지고, `Forgotten`에 B가 들어간다.
    - 대상 줄은 `targets`·`observe`·`before` 글 전체를 LIKE로 찾는다. `json_each`로 필드만 훑지 않는다.
  - `TestForgetAudit_되돌림_제안과_이어진_원장_줄도_비운다`: `revert_of`·`reverted_by`로 이어진 줄과 `memory_ids`가 빈 되돌림 제안(auto.go:283)에 지운 기억의 제목 문자열이 남지 않는다.
  - `TestForgetAudit_감사_목록에_지운_기억의_제목과_본문이_없다`: `Audit`(audit.go:518) 출력 전체에서 제목·본문 문자열이 0개이고, 항목 수는 그대로다.
  - `TestForgetAudit_자동_되돌림_사건의_제목을_비우고_ledgerId는_둔다`: `audit.auto_reverted`(auto.go:26) 사건의 `title`에서 지운 기억의 제목이 빠지고 `ledgerId`는 그대로다.
  - `TestForgetAudit_지운_기억이_든_원장_줄을_되돌리면_까닭을_알리고_제안_줄이_안_남는다`:
    - `RevertChange`가 `errors.Is(err, ErrLedgerForgotten)`이고, 오류 글에 id가 있다.
    - 호출 전후의 `proposals` 행 수가 같다.
  - `TestForgetAudit_관찰_대상으로만_담긴_원장_줄은_되돌릴_수_있다`: 지침 파일 변경 원장 줄(ledger.go:361-363 꼴, `observe`에만 id)은 글만 비고 `Forgotten`이 비어 있다. `RevertChange`가 성공한다.
  - `TestForgetAudit_하루_정리는_끝까지_돈다`: 해로움 판정 줄이 있어도 `Tidy`가 오류 없이 돌아오고 뒤 단계가 실행된다. `Errors`에 그 줄 오류가 한 번 있는 것은 정상이다(tidy.go:52-56).
  - `TestForgetAudit_여러_기억에_걸친_제안은_남은_기억의_감사_항목이_남는다`.
  - `TestForgetAudit_대기_중인_제안은_닫는다`: 지운 id를 담은 `pending` 제안이 `StatusDropped`가 된다.
- [ ] **2단계: 실패 확인** — `go test ./internal/memory -run 'TestForgetAudit_' -count=1` → FAIL
- [ ] **3단계: 구현** — `scrubAuditTx`
  - 대상 원장 줄은 둘이다.
    - `targets`·`observe`·`before` 글에 id가 LIKE로 걸리는 줄
    - 그 줄과 `revert_of`·`reverted_by`로 이어진 줄
  - 대상 제안 줄은 둘이다.
    - `memory_ids`에 id가 든 줄
    - 대상 원장 줄의 `proposal_id`
  - `Forgotten` 표시는 그 id가 `before.Memories`나 `Created`에 있던 줄에만 단다.
  - 모든 작업은 `tx`로만 한다.
- [ ] **4단계: 통과 확인** — `go test ./internal/memory -count=1` → PASS
- [ ] **5단계: 커밋** — `feat: Blank forgotten memories out of the ledger and proposals`

### Task 3: 되살아남 막기

**파일:**
- 고치기:
  - `internal/memory/transfer.go`: `Import`(transfer.go:57-110)
  - `internal/memory/idem.go`: `replayTx`(idem.go:85-147)
  - `internal/migrate/migrate.go`: migrate.go:216-220
- 만들기:
  - `internal/memory/forget_revive_test.go`
  - `internal/migrate/forget_test.go`: 같은 패키지의 기존 시험 꼴을 따른다.

**인터페이스:**
- 쓰는 것: Task 1의 `forgottenTx`, `Store.IsForgotten`, `Forget`, `ErrForgotten`.
- 내놓는 것: `const ImportForgotten = "forgotten"`(transfer.go:13-19 묶음). 명령줄은 기존 `cli.import.skipped` 줄에 이 코드를 그대로 찍는다(thin_cmds.go:405). 새 문구 키는 만들지 않는다.

- [ ] **1단계: 실패하는 시험 쓰기**
  - `TestImport_지운_기억은_forgotten으로_건너뛴다`: export한 기억을 지운 뒤 `Import`하면 `ImportForgotten`이고 `Get`은 없음이다. `legacy_id`로만 같은 경우도 같다.
  - `TestImport_지운_기억을_가리키는_대체_표시는_비우고_들여온다`.
  - `TestIdem_지운_기억을_만든_저장_요청을_재시도하면_거절한다`: 키 "k1"로 저장 → 지움 → 같은 키·같은 인자로 `Save`하면 `errors.Is(err, ErrForgotten)`이고 기억 수가 그대로다. `update`·`supersede`로 기록된 키도 같다.
  - `TestIdem_같은_내용도_새_키로는_저장된다`.
  - `TestIdem_forgotten_표가_없는_DB에서도_재시도가_돈다`: `DROP TABLE forgotten` 뒤 재시도가 기존처럼 동작한다.
  - `TestMigrate_지운_legacy_id는_다시_들여오지_않는다`: 다시 돌리면 `Skipped`가 늘고 기억이 생기지 않는다.
- [ ] **2단계: 실패 확인** — `go test ./internal/memory ./internal/migrate -run 'TestImport_지운|TestIdem_|TestMigrate_지운' -count=1` → FAIL
- [ ] **3단계: 구현**
  - `Import`는 `ImportExists` 검사 앞에서 `forgottenTx(tx, m.ID, m.LegacyID)`를 본다.
  - `replayTx`는 기록된 `MemoryID`가 `forgotten`에 있으면 `ErrForgotten`을 돌려준다.
  - `migrate`는 `s.IsForgotten("", r.ID)`(legacy id)를 본다. 다른 패키지라 소문자 함수는 부를 수 없다.
- [ ] **4단계: 통과 확인** — `go test ./internal/memory ./internal/migrate -count=1` → PASS. `./internal/i18n`은 묶음 2를 다 합친 뒤 돌린다.
- [ ] **5단계: 커밋** — `feat: Keep forgotten memories from coming back through import, migrate and retries`

### Task 4: MCP 도구 `memory_forget`

**파일:**
- 고치기:
  - `internal/mcpserver/server.go`:
    - `addPlain` 등록(server.go:119-158 사이)
    - `ForgetIn`·`ForgetOut`, 처리기
    - MCP 안내 문자열(server.go:109-114)에 한 줄
    - 주석(server.go:118-121)의 「only add」 고침
  - `internal/mcpserver/tools.go`: `Tools.Forget`. `HostTools`는 그대로 둔다.
  - `internal/mcpserver/server_test.go`: :73 도구 이름 목록, `TestToolAnnotations`(:292)
  - `internal/mcpserver/surface_test.go`: :429 개수를 7에서 8로
- 만들기: `internal/mcpserver/forget_test.go`

**인터페이스:**
- 쓰는 것: Task 1의 `Store.Forget`, `ForgetInput`, `ForgetResult`, `ForgetMissingError`.
- 내놓는 것:
  - `type ForgetIn struct { IDs []string \`json:"ids"\`; UserQuote string \`json:"userQuote,omitempty"\`; Reason string \`json:"reason,omitempty"\` }`
  - `type ForgetOut struct { Forgotten []string \`json:"forgotten"\`; Touched []string \`json:"touched,omitempty"\`; Note string \`json:"note"\` }`
  - `func (h handlers) forget(ctx context.Context, req *mcp.CallToolRequest, in ForgetIn) (*mcp.CallToolResult, ForgetOut, error)`
  - `func (t Tools) Forget(in ForgetIn) (ForgetOut, error)`
- 도구 설명은 명세 5.1의 문장이다.
- `Note`는 mcpserver 안의 한국어 상수 `forgetNote = "백업 사본과, 다른 기억에 나눠지거나 복사된 글은 지우지 않았다."`다. 기존 도구 설명처럼 AI가 읽는 글이라 locale에 넣지 않는다.
- `track`의 데이터 함수는 `{"count": len(in.IDs)}`만 넣는다.
- tools/list 상한은 8,000자이고 지금 7,392자라 여유가 608자다. 넘으면 이 순서로 줄인다.
  1. jsonschema 설명
  2. 도구 설명의 「supermemory…」 같은 부연
  3. 「틀리거나 낡은 기억은 먼저…」 문장
  - 「되돌릴 수 없다」와 「사용자 말을 userQuote로 줄 때만」은 남긴다.

- [ ] **1단계: 실패하는 시험 쓰기** (`connect(t)`·`call(...)`, server_test.go:16·37)
  - `TestForgetTool_말과_함께_부르면_지우고_안내를_돌려준다`: `forgotten`이 그 id들이고 `note`가 `forgetNote`다.
  - `TestForgetTool_AI가_말_없이_부르면_도구_오류다`: `IsError`이고 기억이 남아 있다.
  - `TestForgetTool_ids가_0개나_21개면_도구_오류다`.
  - `TestForgetTool_없는_id는_도구_오류에_그_id를_담는다`.
  - `TestForgetTool_지운_뒤_다른_기억의_get과_find가_오류_없이_돈다`: 연결돼 있던 기억 C의 `memory_get` links에 지운 id가 없다. `memory_find`도 오류가 없다.
  - `TestForgetTool_호출_사건에_말과_이유를_남기지_않는다`: `tool.memory_forget` 사건 data에 `userQuote`·`reason`의 글이 없다.
  - 기존 시험 고침:
    - 도구 목록(server_test.go:73)에 `memory_forget`을 더한다.
    - `TestToolAnnotations`에 `memory_forget: destructive=true, idempotent=false`를 더한다.
    - surface_test.go:429 개수를 8로 바꾼다.
    - 8,000자·1,200자 상한 시험은 고치지 않고 통과시킨다.
    - `tools_test.go:16`은 고치지 않는다.
- [ ] **2단계: 실패 확인** — `go test ./internal/mcpserver -count=1` → FAIL
- [ ] **3단계: 구현** — 처리기는 `ForgetIn`을 `ForgetInput`으로 옮기고, 저장소 오류를 도구 오류로 바꾼다. ids 개수는 다시 검사하지 않는다.
- [ ] **4단계: 통과 확인** — `go test ./internal/mcpserver ./contract/... -count=1` → PASS
- [ ] **5단계: 커밋** — `feat: Add the memory_forget MCP tool`

### Task 5: 명령줄 `orbit-mem forget`, 복구 안내, 문서·생성물

**파일:**
- 고치기:
  - `cmd/orbit-mem/thin_cmds.go`: `cmdForget`
  - `cmd/orbit-mem/main.go`: `commands()`에 `{"forget", "usage.forget", cmdForget}`
  - `cmd/orbit-mem/backup_cmd.go`: restore 미리 보기(backup_cmd.go:213-224)
  - `internal/memory/backup.go`: `ForgottenInCopy`
  - `internal/i18n/locales/{en,ko}/cli.json`:
    - `usage.forget`, `flag.forget.reason|quote|yes`, 확인 질문, `cli.backup.restore_forgotten`을 더한다.
    - `usage.stale`의 「지우지 않는다」를 「완전히 지우려면 forget」으로 고친다.
  - `internal/i18n/locales/{en,ko}/memory.json`: `Store.ForgottenInCopy` 오류 문구가 필요하면 넣는다.
  - `README.md`:
    - 명령 표(104-130), MCP 도구 목록(207-211), 185·247행
    - 백업 절(286-299): 「지운 기억은 하루 사본·이름 붙은 사본에 남고 restore하면 돌아온다」
    - 스키마 절: 「`forgotten` 표가 생기면 기존 DB를 처음 쓰기로 열 때 `before-schema-*` 사본이 하나 생긴다」(store.go Open의 SchemaOlder 분기, backup.go:209-216)
  - `internal/memplugin/source/skills/memory/SKILL.md`: 고치기 표(112-120)에 규칙을 더한다. 잊기는 stale, 사용자가 지우라고 할 때만 `memory_forget`, userQuote 필수.
  - `plugin/skills/memory/SKILL.md`: `plugin build --repo .`로 다시 만든다.
- 만들기: `cmd/orbit-mem/forget_cmd_test.go`

**인터페이스:**
- 쓰는 것: Task 1의 `Store.Forget`, `ForgetInput`, `ForgetMissingError`. `actorOf`(memory_cmds.go:58), `isTerminal(a.stdin)`(tty.go:11, backup_cmd.go:226 선례). Task 4에는 기대지 않는다.
- 내놓는 것:
  - `func cmdForget(a *app, args []string) error`: 플래그 `--reason`, `--quote`, `--yes`, `--json`
  - `func (s *Store) ForgottenInCopy(path string) (int, error)`: 사본의 `memories.id` 가운데 지금 `forgotten`에 있는 개수. 사본은 `ReadCopyInfo`(backup.go:232)처럼 읽기 전용으로 연다.

- [ ] **1단계: 실패하는 시험 쓰기** (`run(t, stdin, args...)`·`home(t)`·`seed(t)`, main_test.go:17·25·41)
  - `TestForgetCmd_터미널이_아니고_yes가_없으면_지우지_않고_종료_1`.
  - `TestForgetCmd_yes면_지우고_id를_보인다`.
  - `TestForgetCmd_ORBIT_ACTOR가_ai면_quote_없이는_yes라도_거절한다`: `t.Setenv("ORBIT_ACTOR", "ai")`. `--quote "지워"`를 주면 성공한다.
  - `TestForgetCmd_지우기_전에_id와_제목을_보인다`.
  - `TestBackupRestore_지운_기억이_사본에_있으면_돌아온다고_알린다`: 사본 → 지움 → `backup restore <사본>` 미리 보기 출력에 「1」과 `cli.backup.restore_forgotten` 글이 있다. 선례는 backup_test.go:329-355다.
  - `TestRepo_커밋한_생성물이_원본과_같다`(memplugin/repo_test.go:12)는 다시 만든 뒤 통과한다.
- [ ] **2단계: 실패 확인** — `go test ./cmd/orbit-mem -run 'TestForgetCmd_|TestBackupRestore_지운' -count=1` → FAIL
- [ ] **3단계: 구현**
  - 확인 흐름은 `backupRestore`(backup_cmd.go:199-258)를 따른다.
  - actor는 `actorOf("", memory.ActorUser)`로 정하고, `--quote`와 함께 `memory.Who`를 만든다. `corePin`(core_cmd.go:120)과 같다.
  - 문서와 SKILL을 고친 뒤 `go run ./cmd/orbit-mem plugin build --repo .`를 돌린다.
  - 명세 8절의 빌더 몫도 여기서 한다. `retros`·`session_ends`·`retro_coverage`·`lab_*` 표에 기억 id가 글로 남는지 SQL로 확인하고, 남으면 보고한다(지우지는 않는다).
- [ ] **4단계: 통과 확인** — `go test ./cmd/orbit-mem ./internal/memplugin ./internal/i18n -count=1` → PASS
- [ ] **5단계: 커밋** — 고친 파일과 다시 만든 `plugin/skills/memory/SKILL.md` → `feat: Add orbit-mem forget and document what forgetting keeps`

### 마무리 (Task 아님)

- [ ] `go vet ./... && go test ./... -count=1` → 실패 0개. 건너뜀은 이유를 적는다(`qa`).
- [ ] 명세 6절 수용 기준 1~15를 시험 이름과 하나씩 대조한다.
- [ ] `architect` 전체 검토: `main...feat/memory-forget` 차이, 명세, 이 계획.
- [ ] 보고:
  - 완료 기준 대조
  - 사전 조사에서 놓친 것
  - 남은 일: 푸시·PR·릴리스, 실제 DB의 낡은 orbit-cc 기억 정리
  - 위험: 옛 판 실행 파일의 `audit revert`

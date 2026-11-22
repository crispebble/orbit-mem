# orbit-mem 기억 정확성과 회상 품질 개선 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 구현 일꾼은 superpowers:test-driven-development를 따른다.

**Goal:** 폐기·만료·대체된 기억이 어느 경로로도 현재 값처럼 들어오지 않게 하고, 저장·대체·확정·수명·검색·도구 직전 회상의 틈을 0.20.0 릴리스 하나로 고친다.

**Architecture:** 변경 축 다섯을 저장소 안 한 곳씩에 모은다. 보임 판정은 `memory.Viewer`, 대체 정책은 표 `replaceTable`, 순위는 `inject.rankHits`, 낱말 나누기는 기존 `textindex.go`·`stem.go` 확장, 호스트 계약은 선택 칸 `toolInput` 하나다. 새 근거 자료는 스키마 6에서 열·표 추가로만 둔다. 새 포트·인터페이스는 만들지 않는다.

**Tech Stack:** Go, SQLite(FTS5), MCP 서버, Claude Code·Codex 훅. 새 의존성 없음.

**Spec:** Atlas 「명세 · orbit-mem — 기억 정확성과 회상 품질 개선」 https://atlas.onidot.dev/w/0s471qc3pfgg2/pages/0sa97zdgbmq4t (요구 번호 F·S·C·R·E·L·Q·T·D). 일꾼은 맡은 Task의 요구 번호 절을 원문으로 읽는다.

## Global Constraints

- 작업 브랜치는 `feat/memory-accuracy-recall`이다. `main`에 직접 커밋하지 않는다. 일꾼은 커밋까지만 하고 푸시·PR·태그는 하지 않는다.
- `git add`는 경로를 적는다. `git add -A`·`git add .`·`git commit -a`를 쓰지 않는다. 커밋 전에 `git config user.name`이 `crispebble`인지 본다.
- 커밋 메시지는 `<type>: <Subject>`(영어 명령형, 50자 안팎), 본문은 한글. 끝에 `Co-Authored-By: Claude Fable 5.2 <noreply@anthropic.com>`.
- 시험 이름은 저장소 관례 `TestX_한글_설명`을 따른다. 실패하는 시험을 먼저 쓰고 실패를 확인한 뒤 구현한다.
- 새 의존성을 더하지 않는다. 저장할 때 모델을 추가로 호출하지 않는다.
- 스키마는 열·표 추가만 한다. `SchemaVersion`은 5에서 6으로 한 번만 올린다(Task 03).
- 실제 기억 DB(`~/Library/Application Support/orbit/memory.db`)에 쓰지 않는다. 읽어서 사본을 만드는 것만 된다. 사본과 측정 결과는 저장소 밖 `$ORBIT_HOME/bench/replay/`에 둔다. 개인 기억 내용을 커밋하지 않는다.
- 소스·주석·README·문구에 개인 작업 환경 이야기를 쓰지 않는다. 제품 동작으로만 쓴다.
- `tools/list` 전체는 8,000룬 이하다(지금 7,978룬). Task 06이 기존 설명을 줄이기 전에는 어떤 Task도 도구 설명·인자 설명을 늘리지 않는다.
- 평가 기준선 `internal/bench/testdata/baseline-current.json`은 Task 01과 18, 그리고 묶음을 합친 뒤의 진행자만 `-update-golden`으로 다시 쓴다. 다른 Task는 이 파일을 고치지 않는다. 자기 변경으로 `TestPublicSet`이 달라지면 고치지 말고 달라진 지표를 보고에 적는다. 진행자는 다시 쓸 때마다 지표 차이를 이슈에 적는다.
- 측정에 쓰는 실행 파일은 해당 커밋에서 `go build -o <저장소 밖 임시 경로>`로 만든 것만 쓴다. PATH의 설치본으로 재지 않는다. `migrate`·`doctor`처럼 DB를 여는 명령은 반드시 `ORBIT_HOME=<사본 폴더>`를 주고 돌린다. `ORBIT_HOME` 없이 돌리면 실제 기억 DB가 이행된다.
- 이 문서의 `$ORBIT_HOME/bench/replay/`는 기본 홈 아래 `~/Library/Application Support/orbit/bench/replay/`를 뜻한다. 셸에서는 변수가 비어 있을 수 있으므로 실제 절대경로로 적어 쓴다.
- 스키마 6 뒤에도 옛 표 `sightings`에는 지금처럼 계속 쓴다.
- 「판」 대신 「버전」을 쓴다.
- Task 끝 시험 범위: 그 Task의 「명령」(바꾼 패키지와 직접 소비자)과 바꾼 패키지의 `go vet`. 전체 `go test ./... && go vet ./...`는 묶음을 합친 뒤 진행자가 한 번, 그리고 Task 03·19·21에서 돈다.
- 막히면 멈추고 보고한다. 명세와 코드가 어긋나면 명세를 바꾸지 말고 보고한다.

## 작성자가 정한 것 (설계 검토에서 나온 선택)

| 항목 | 정한 것 | 까닭 |
| --- | --- | --- |
| 블라인드 판정 묶음 | 구현을 보지 않는 일꾼이 새 묶음 `holdout6`을 쓴다(Task 02b) | 기존 묶음은 모두 한 번씩 써서 개발 자료가 됐다 |
| T-2 입력 조건을 둘 곳 | 기존 tools 칸 문법 `Bash(git commit)` | 열과 도구 목록 글자가 늘지 않는다 |
| S-4 대조 범위 | `memory_save`의 사용자 말 출처만 | 명세 범위다. `memory_update`·`memory_forget`의 userQuote는 다음 릴리스 후보 |
| 자동 모드 설정 키 | `autoMode`(기본 false) | 명세의 이름을 그대로 옮김 |
| E-2의 거절 기록 | 같은 범위·주제의 hidden 기억 | 원장 줄보다 판정이 단순하고 시험할 수 있다 |
| 세션 구분 | MCP 프로세스 환경의 `CLAUDE_CODE_SESSION_ID`, 없으면 서버 실행 단위 id | 실행 중인 프로세스에서 값이 대화 기록 id와 같음을 확인. 공식 문서는 확인 못 함 |

명세 사실 정정(구현은 막히지 않는다): 한글 두 글자 색인과 요청 회상의 조사 떼기는 스키마 5에 이미 있다. Q-2에서 새로 하는 것은 `memory_find` 질의의 조사 떼기뿐이고, Q-2 스위치도 그 범위다. 호스트 계약의 세션 칸도 이미 있어, 새 선택 칸은 `toolInput` 하나다.

## Review Focus

1. **재개가 여러 번인 세션**: 묶음을 다시 만든 뒤 또 재개하면, 두 번째 다시 만들기가 첫 번째에서 뺀 기억을 되살리지 않는다. → Task 07 `TestSessionBundle_두번_다시_만들어도_뺀_기억이_돌아오지_않는다`
2. **재확인할 파일이 없거나 읽을 수 없음**: `RecheckExcerpt`는 없는 경로·디렉터리·권한 없음·상대경로에서 false를 돌려주고 저장은 실패하지 않는다(어긋남으로 남는다). → Task 14 `TestRecheckExcerpt_없는_파일과_상대경로는_false다`
3. **아주 짧은 발췌의 사용자 말 대조**: 낱말 한두 개짜리 발췌(「응」「그래」)가 아무 발언과나 맞아 통과하지 않는다. → Task 12 `TestMatchQuote_조각보다_짧은_발췌는_통째로_일치할_때만_matched다`
4. **스키마 이행 도중 실패**: 이행 트랜잭션이 중간에 실패하면 DB가 스키마 5 그대로 남고 다시 열면 이행을 다시 시도한다. → Task 03 `TestOpen_이행이_실패하면_스키마5_그대로_남는다`
5. **자동 모드를 켰다 끈 뒤**: 켰을 때 자동 대체된 사실은 끈 뒤에도 그대로이고, 끈 뒤 새로 생긴 같은 꼴의 어긋남은 확인 지시가 붙는다. → Task 15 `TestAutoMode_끈_뒤에도_이미_대체된_것은_그대로이고_새_어긋남은_묻는다`

---

## 구조 (모든 Task가 공유)

경로는 `internal/` 기준이다.

### 보임 판정 `memory/visible.go` (Task 05가 만든다)

```go
type Why string
const (
    WhyCurrent  Why = ""
    WhyStale    Why = "stale"
    WhyRejected Why = "rejected"
    WhyGone     Why = "gone"
    WhyExpired  Why = "expired"             // 글은 inject.ExcludedExpired와 같게
    WhyOlder    Why = "superseded_by_topic" // 글은 inject.ExcludedSuperseded와 같게
)
type Visibility struct {
    Why       Why
    Successor string   // 살아 있는 후계 id, 없으면 ""
    ReviewDue bool     // L-1
    Conflicts []string // C-5: 열린 어긋남 쌍의 새 쪽 id. Task 15가 채운다
}
func (v Visibility) Current() bool
func (s *Store) Viewer() (Viewer, error)
func (v Viewer) Of(m Memory) Visibility
func (v Viewer) OfID(id string) (Memory, Visibility) // 없는 id는 WhyGone
func (v Viewer) Successor(m Memory) (Memory, bool)
```

사용자 말 출처의 휘발 기억은 만료돼도 상태를 바꾸지 않고 `WhyExpired`로만 보인다.

### 대체 정책 `memory/replace.go` (Task 14가 만든다)

```go
type Outcome int
const (OutcomeSupersede Outcome = iota; OutcomeAutoSupersede; OutcomeConflict; OutcomeRefuse)
type NewGround int
const (GroundQuote NewGround = iota; GroundRechecked; GroundStated; GroundTalk; GroundUntrusted)
type OldGround int
const (OldQuote OldGround = iota; OldQuoteUserScope; OldOther)
type replaceRow struct { Kinds []string; New []NewGround; Old OldGround; Off, On Outcome }
func decideReplace(kind string, nw NewGround, old OldGround, autoMode, revertedPair bool) Outcome
```

표(첫 줄 일치, 위에서 아래로):

| 종류 | 새 근거 | 옛 근거 | 자동 모드 끔 | 켬 |
| --- | --- | --- | --- | --- |
| 모두 | 사용자 말 | 사용자 말(어느 범위든) | 대체 | 대체 |
| 모두 | 사용자 말 아님 | 사용자 말(user 범위) | 어긋남 | 어긋남 |
| 규칙·결정 | 사용자 말 아님 | 사용자 말 | 어긋남 | 어긋남 |
| 사실 | 서버가 다시 확인함 | 사용자 말 | 어긋남(2026-11-23 사용자 결정으로 고침. 이전: 자동 대체) | 자동 대체 |
| 사실 | 근거 적힘(다시 확인 못 함) | 사용자 말 | 어긋남 | 자동 대체 |
| 사실 | 대화·믿을 수 없음 | 사용자 말 | 어긋남 | 어긋남 |
| 모두 | 사용자 말 아님 | 사용자 말 | 어긋남 | 어긋남 |
| 모두 | 믿을 수 없음 | 그 밖 | 어긋남 | 어긋남 |
| 모두 | 믿을 수 있음 | 그 밖 | 대체 | 대체 |

`revertedPair`면 늘 어긋남이다. 명시적 `Replaces`에서 어긋남은 거절로 바꾼다(지금 동작).

표를 쓸 때 지킬 것:

- **새 근거는 이번 저장의 `in.Source`로만 정한다**(사용자 말 대조와 재확인을 거친 뒤). 기억에 예전에 붙은 출처로 정하지 않는다. 예전에 사용자 말이 붙은 기억을 AI가 다시 저장하면서 사용자 규칙을 가리켜도 「사용자 말」 줄에 걸리면 안 된다.
- 믿음 등급 축은 기존 `mayOverride`가 먼저 판정하고, 표는 출처·종류 축만 맡는다. 두 판정이 어긋나면 더 보수적인 쪽(어긋남·거절)을 따른다.
- `replaceRow.Old`는 값 하나가 아니라 집합 `[]OldGround`로 둔다(첫 줄의 「어느 범위든」).
- 휘발 기억을 주제 대체에서 빼는 선판정(`supersede.go:26`)과 `Replaces`를 먼저 거절하는 `guardTx`(`store.go:510-513`)는 그대로 둔다. 그래서 같은 쌍이 `Replaces`로 오면 거절, 주제 대체로 오면 자동 대체로 갈릴 수 있다. 의도한 동작이다.
- **자동 대체의 원장 줄은 Before·After에 옛 기억만 넣고 새 id는 payload에 둔다.** 새 기억은 즉시 확정이나 재저장으로 버전이 바뀌므로, 원장의 버전 검사(`ledger.go:432`)에 새 기억이 들어가면 되돌리기가 `ErrMemoryChanged`로 막힌다. 되돌리기 갈래는 새 기억의 버전과 관계없이 그것을 후보로 내리고 `supersede_reverts`에 쌍을 남긴다.

### 순위 `inject/rank.go` (Task 13이 만든다)

```go
type rankPolicy struct{ byScore bool; margin float64 }
func rankPolicyOf(cfg config.Config) rankPolicy
func rankHits(s *memory.Store, cfg config.Config, v memory.Viewer, pl place.Place, text string, hits []memory.RecallHit) []memory.RecallHit
```

순서: ① `byScore`이고 점수 차가 `margin`보다 크면 점수 큰 쪽 ② 확정이 후보보다 앞 ③ 점수·확정이 같으면 `ReviewDue`가 뒤 ④ 지금 순서(점수, 맞은 비율, 이 장소, 나이, 사용 판정, 고친 시각, id).

### 스위치 (`config/config.go`의 `Config`, 기본값은 `Default()`)

| 스위치 | 칸 | 기본값 | 끄면 |
| --- | --- | --- | --- |
| R-1 | `RankByScore bool`, `RankScoreMargin float64` | Task 18이 정함. 맞는 값이 없으면 false | 지금 순위 |
| Q-2 | `FindStripParticles bool` | true | `memory_find` 질의에서 조사를 떼지 않음 |
| T-1 | `ToolInputGate bool`, `ToolInputMinScore float64` | Task 18 전까지 false. Task 18이 문턱과 함께 true로 정함(못 넘으면 false 유지) | 도구 이름만으로 판정 |
| C-9 | `AutoMode bool` (`json:"autoMode"`) | false | 묻는다 |

평가 때는 `orbit-mem bench recall --set key=value`로 덮어쓴다.

### 스키마 6 (Task 03이 만든다)

```go
// memory/store.go addColumns 표에 더함
{"memories", "aliases",   "TEXT NOT NULL DEFAULT '[]'"},
{"sources",  "rechecked", "INTEGER NOT NULL DEFAULT 0"},
```

```sql
-- memory/schema6.go
CREATE TABLE IF NOT EXISTS sighting_sessions (memory_id TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY(memory_id, session, day)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS utterances (id TEXT PRIMARY KEY, host TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS utterances_at ON utterances(at);
CREATE TABLE IF NOT EXISTS utterance_shingles (hash TEXT NOT NULL, utterance_id TEXT NOT NULL,
  PRIMARY KEY(hash, utterance_id)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS utterance_shingles_u ON utterance_shingles(utterance_id);
CREATE TABLE IF NOT EXISTS supersede_reverts (old_id TEXT NOT NULL, new_id TEXT NOT NULL, ledger_id TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, PRIMARY KEY(old_id, new_id)) WITHOUT ROWID;
```

`migrate`에서 `v < 6`일 때 같은 트랜잭션으로 `backfill6Tx(tx, now)`. `v`는 잠금 안에서 다시 읽은 값(`store.go:172`)으로 건다. 두 프로세스가 동시에 열어도 채우기는 한 번만 돈다.
1. `INSERT OR IGNORE INTO sighting_sessions SELECT memory_id, 'legacy', day, 0 FROM sightings`. 옛 기록은 세션 하나 `legacy`로 묶는다. 옛 날짜 둘만으로는 「다른 세션」이 되지 않는다.
2. `review_at`이 0이고 휘발이 아니며 후보·확정인 기억에 `max(created_at, updated_at, 마지막 sightings 날) + 90일`을 채운다
3. 모든 기억을 먼저 「늦은 줄」로 표시한 뒤 채워서 다시 색인한다. 2단계의 UPDATE 트리거에 기대지 않는다(트리거는 살아 있는 기억만 표시한다).

E-1의 판정은 「`sighting_sessions`에 세션과 날이 둘 다 다른 두 줄이 있다」이다.

### 그 밖의 공유 이름

| 이름 | 만드는 Task | 뜻 |
| --- | --- | --- |
| `host.ToolInputText(tool string, raw json.RawMessage) string` | 01 | Bash면 `command`, Edit·Read·Write면 `file_path`, 그 밖은 문자열 값 이어 붙임. 2,000룬에서 자름 |
| `bench.Shown.Dead []string`, 사례 종류 `start`, `Case.Input`, `Recaller.Tool(session, cwd, tool, input string)` | 01 | 재생 측정 |
| `(*memory.History).StateAt(id string, at int64) (Memory, bool)`, `bench.AuditInjections` | 01 | 기록된 주입의 폐기 여부 감사 |
| `SaveReport.BodyKept bool` | 06 | 같은 제목·다른 본문 |
| `(*Store).ReplaceSessionBundle(host, session, text string, ids, dropped []string) error`, `memory.MarkBundle` | 07 | 묶음 다시 만들기 |
| `FindInput.Current bool`, `FindItem.Note string`, `MemoryView.Notice string` | 08 | 조회 경로 표시 |
| `SaveInput.Session string`, `mcpserver.Options.Session string`, `claudecode.MCPSession(getenv func(string) string) string`, `memory.SessionKey` | 10 | 세션 구분 |
| `host.Request.ToolInput string` (`json:"toolInput,omitempty"`), `ToolInjection(s, cfg, scope, host, session, tool, input string) Injection`, `(Memory).TriggeredByInput(tool, input string) (exact, ok bool)` | 11 | 도구 입력 |
| `RecordUtterance(host, text string) error`, `MatchQuote(excerpt string) (QuoteMatch, error)`, `QuoteMatched`·`QuoteNotFound`·`QuoteUnchecked`, `PruneUtterances(days int) (int, error)`, `SaveReport.Rejected` | 12 | 발언 조각 해시(HMAC-SHA256 앞 16바이트 hex, salt는 `meta`의 `utterance.salt`), 30일 보관 |
| `Policy.AutoMode bool`, `RecheckExcerpt(path, excerpt string) bool`, `mcpserver.Options.LoadPolicy func() memory.Policy` | 14 | 자동 모드·재확인 |
| `OpenConflictsOf(ids []string) (map[string][]string, error)`, `AutoSupersedesSince(at int64)` | 15 | 어긋남·알림 표시 |
| `Memory.Aliases []string`, `StripParticlesQuery` | 16 | 별칭·질의 조사 떼기 |

---

## Tasks

역할: 어려움 = `worker-deep`, 보통 = `worker`, 작음 = `worker-quick`, 측정 = `qa`, 문서 = `writer`. 모든 구현 Task는 끝나면 `reviewer`가 그 Task의 diff와 맡은 요구 번호 절만 보고 검토한다.

각 Task의 걸음은 같다.

- [ ] 맡은 요구 번호의 명세 절과 「구조」의 해당 부분을 읽는다
- [ ] 아래 실패 시험을 쓰고, 「명령」을 돌려 실패를 확인한다
- [ ] 시험이 통과하는 최소 구현을 한다
- [ ] 「명령」과 바꾼 패키지의 `go vet`을 돌려 통과를 확인한다
- [ ] 적힌 파일만 경로를 적어 커밋한다

### Task 01: 재생 측정 도구 — worker-deep

**끝:** 같은 사본과 사례로 경로별(start·request·tool·subagent) 호출 수·평균 글자·폐기 주입 수가 결정적으로 나오고, `bench audit-injections`가 기록된 폐기 주입을 센다. 요구: 7절 측정, F-6 측정 쪽.

**Files:** `bench/ports.go`, `bench/cases.go`, `bench/run.go:95-135`, `bench/metrics.go`, `bench/report.go`, 새 `bench/injaudit.go`, `memory/asof.go`, `bench/claudelog/claudelog.go:48-58`, `cmd/orbit-mem/bench_cmd.go`, `bench/hostpath/hostpath.go`, `bench/legacy/recall.go`(시그니처만), 새 `host/toolinput.go`, `bench/README.md`

**Interfaces:** Produces 위 표의 01 줄 전부. 재생 사본은 실행 전에 `session_bundles`·`session_marks`·위임 표를 비워, 사본을 뜬 PC에 남아 있던 세션 상태가 섞이지 않게 한다. 비우는 것은 실행 전 한 번이고, 재생이 스스로 만든 세션 상태는 그 세션의 뒤 사례까지 이어진다(Task 01b).

**실패 시험:**
- `TestRun_같은_세션의_두번째_시작_사례는_시작_회상을_다시_부른다` — Start가 2회 불린다
- `TestHostpath_재개_시작이_얼린_묶음의_낡은_기억을_Dead로_낸다` — 지금 동작에서 Dead가 1
- `TestAuditInjections_주입_시각에_낡은_기억을_센다`
- `TestMetrics_경로별_평균_글자와_폐기_주입을_낸다`
- `TestToolInputText_Bash는_command만_Edit는_file_path를_돌려준다`

**명령:** `go test ./internal/bench/... ./internal/host ./internal/memory -run 'TestRun|TestHostpath|TestAudit|TestMetrics|TestToolInputText|TestHistory' -count=1`

지표 정의가 바뀌므로 `baseline-current`는 `-update-golden`으로, `baseline-legacy`는 지운 뒤 `-write-legacy-baseline`으로 다시 쓴다. 정의 밖 값이 움직이지 않았는지 diff로 확인해 보고에 적는다.

### Task 02: 기준값 측정 — qa

Task 01 커밋에서만 돈다(0.19.0 동작). 소스를 쓰지 않는다.

**끝:** 기준값 표가 나오고, Task 19가 다시 쓸 재료가 `$ORBIT_HOME/bench/replay/0.19.0/`에 남는다: 실행 파일 `bin/orbit-mem`(Task 01 커밋 빌드), `memory-copy.db`, `cases.json`.

기준값 표에 적을 것과 판정법:

- 경로 넷(start·request·tool·subagent)의 호출 수와 주입 한 번의 평균 글자
- 공개 묶음의 찾아지는 비율·틀린 회상
- **폐기 주입 재현**: `audit-injections`가 센 (세션, 기억) 쌍의 수 A, 그 가운데 재생의 start 경로 `Dead`에 다시 나오는 쌍의 수 B. B/A를 적는다. B = A면 「묶음 재전달이 원인」으로 확정하고, B < A면 재현되지 않은 쌍의 경로를 사건 종류별로 나눠 적고 진행자에게 보고한다(다른 유입 경로가 있다는 뜻이다).
- **무관 반복 사례 둘**: 기억 `m-5d1c0ab2276b`와 `m-1bc7d455c05c`가 재생의 tool 경로에 실린 횟수

**명령:** `<bin> bench extract --projects <대화 기록 폴더> --db <실제 DB> --out $ORBIT_HOME/bench/replay/0.19.0/`, `<bin> bench audit-injections --db …/memory-copy.db`, `<bin> bench recall --db …/memory-copy.db --cases …/cases.json --recall current,legacy --split all`, `go test ./internal/bench -run TestPublicSet -v`. `extract`는 원본 DB를 열지 않고 바이트 사본만 만든다. 설치본을 바꾸지 않는다.

진행자가 결과 표를 이 문서 끝 「기준값」 절과 이슈에 옮긴다.

### Task 01b: 재생이 묶음 재전달을 재현 — worker-deep

기준값 측정에서 더한 작업이다. 0.19.0의 폐기 주입은 얼린 세션 묶음이 같은 세션의 뒤 시작(압축·이어받기)에 다시 전달되어 생긴다(주입 기록으로 낡음 쌍 55 가운데 54 확인). 재생이 이 경로를 재현하지 못해 B/A가 0/55로 나왔다.

**끝:** 한 세션의 시작 사례가 만든 묶음이 그 세션의 뒤 시작 사례에 재전달되고, 그 사이 낡은 기억이 start 경로의 `Dead`로 세어진다. 사본을 뜬 PC의 세션 상태는 섞이지 않는다. 같은 고침을 0.19.0 측정 빌드(`3258f97`)에 얹어 기준값의 폐기 주입 재현 칸을 다시 잰다.

**Files:** `internal/bench/**`만(재생·되감기·사례), `internal/bench/README.md`. 제품 코드를 고치지 않는다.

**실패 시험:**
- `TestReplay_세션의_묶음은_뒤_시작_사례에_재전달된다`
- `TestReplay_묶음에_얼린_기억이_낡으면_재전달이_Dead로_세어진다`
- `TestReplay_실행_전_세션_상태는_섞이지_않는다`
- `TestReplay_같은_입력이면_결과가_같다`

Task 19는 같은 사본과 사례로 「0.19.0 측정 빌드에서 재현됨 → 작업 브랜치 끝에서 0회」를 판정한다. 다시 재도 재현되지 않으면 고치지 않고 멈춰 보고한다.

### Task 02b: 블라인드 판정 묶음 holdout6 — worker

구현 diff와 Task 03 이후의 브랜치를 읽지 않는다. 진행자가 알려 주는 Task 01 커밋 해시에서 만든 worktree에서 쓴다(묶음 1을 합친 브랜치에는 Task 03이 이미 들어 있다). 묶음은 기존 holdout 묶음처럼 기억과 사례를 함께 담아 실제 DB 없이 판정할 수 있어야 한다.

**끝:** `internal/bench/testdata/holdout6_set.json`이 기존 블라인드 묶음과 같은 형식으로 있고, `bench/README.md`의 묶음 표에 「0.20.0 판정용, 한 번만 씀」으로 적힌다. 사례는 명세 1절의 네 증상을 고르게 담는다: 폐기·만료 뒤 회상, 동의어·한영 표기 차이, 약하게 걸린 확정과 딱 맞는 후보, 도구 입력과 무관한 기억.

**명령:** `go test ./internal/bench -run 'TestHoldout|TestCases' -count=1`(형식 검증 시험이 없으면 형식만 읽는 시험 하나를 더한다)

### Task 03: 스키마 6 이행 — worker-deep

**끝:** 5→6 이행이 표·열·채우기·다시 색인을 한 트랜잭션으로 하고 기억 수가 같다. 요구: 6.3, L-1 기본값, Q-3, E-3.

**Files:** `memory/store.go:115-146,169-207`, 새 `memory/schema6.go`, `memory/backup.go:28`, `contract/host/v1/version-*.json`·`context-schema-*.json`의 스키마 숫자, `cmd/orbit-mem/doctor_schema_test.go` 등 숫자를 박아 둔 시험

**실패 시험:**
- `TestOpen_스키마5_DB를_6으로_올리면_기억_수가_같고_새_표가_있다`
- `TestOpen_재검토일이_빈_기억에_마지막_확인_더하기_90일을_채운다` — 휘발은 제외
- `TestOpen_옛_sightings를_legacy_세션으로_옮긴다`
- `TestOpen_올린_뒤_모든_기억이_다시_색인된다` — `StaleIndexCount==0`
- `TestOpen_스키마6으로_올리기_전에_사본을_남긴다`
- `TestOpen_업그레이드_뒤_확정_기억의_상태는_그대로다`
- `TestOpen_이행이_실패하면_스키마5_그대로_남는다` (Review Focus 4) — 시험 DB에 `RAISE(ABORT)` 트리거를 걸어 실패를 넣는다. 제품 코드에 시험용 이음새를 만들지 않는다
- `TestOpen_두_번_열어도_채우기는_한_번만_돈다`

`memory/asof.go`의 시각 되감기가 새 표 `sighting_sessions`와 새 열을 만나도 깨지지 않게 한다(스냅숏에 없는 칸은 지금 값을 쓰는 기존 규칙을 따른다).

**명령:** `go test ./internal/memory ./cmd/orbit-mem ./contract/... -run 'TestOpen|TestMigrate|Schema|Contract' -count=1`, 이어서 `go test ./... && go vet ./...`

### Task 04: 28일 후보 정리 시험 고정 — worker-quick

**끝:** 기록이 28일을 넘는 DB에서 정리 작업이 안 쓰인 AI 후보를 낡게 하고 사용자 말 후보는 둔다. 요구: L-4.

**Files:** `memory/lifecycle_test.go`. 실패하면 `memory/lifecycle.go:227-` 수정까지.

**실패 시험:** `TestExpireCandidates_기록이_28일을_넘으면_안_쓰인_AI_후보를_낡게_하고_사용자_말_후보는_둔다`

**명령:** `go test ./internal/memory -run TestExpireCandidates -count=1`

### Task 05: 보임 판정 한 곳 — worker

**끝:** inject의 판정 호출부가 모두 `Viewer`를 쓰고, inject 기존 시험과 공개 묶음 기준선이 그대로다. 요구: F의 토대.

**Files:** 새 `memory/visible.go`, `inject/inject.go`(:86-96 `excluded` 지움, :244-256, :289, :416-446, :619-634 `liveSuccessor` 옮김), `inject/tool.go:76-87`, `inject/subagent.go:48-71,186-194`, `inject/diagnose.go:105-130`(출력 글 그대로), `bench/hostpath/hostpath.go`(`Dead`를 `Viewer`로)

**Interfaces:** Consumes 01의 `Shown.Dead`. Produces 「보임 판정」 전부.

**실패 시험:**
- `TestViewer_폐기_거절_지움_만료_옛버전을_가른다`
- `TestViewer_사용자_말_휘발은_만료여도_상태는_그대로이고_만료로_보인다`
- `TestViewer_후계를_따라가_살아있는_것을_준다`

**명령:** `go test ./internal/memory ./internal/inject ./internal/bench/... -count=1`

### Task 06: 같은 제목 재저장과 상대 시간 — worker

**끝:** 같은 제목에 다른 본문이면 알리고, 상대 시간 치환이 지금 기준 표현만 바꾼다. 요구: S-1, S-3.

**Files:** `memory/store.go:409,504-535`, `memory/screen.go:119-150`, `mcpserver/server.go:125-162,795-817`, `mcpserver/tools.go`

먼저 `mcpserver/server.go:125-162`의 도구 설명과 `mcpserver/tools.go`의 인자 설명(jsonschema 태그)을 뜻을 잃지 않게 합쳐 300룬 이상 줄인다. 뒤 Task(08·10·11·16)가 인자와 설명을 더할 자리를 만드는 일이다. `TestSurface_tools_list는_8000룬_이하이고_출력_스키마가_없다`를 `-v`로 돌려 줄인 뒤의 룬 수를 보고에 적는다.

**실패 시험:**
- `TestSave_같은_제목_다른_본문이면_BodyKept를_알린다`
- `TestMemorySave_같은_제목_다른_본문은_memory_update_안내가_나온다`
- `TestScreen_사건_기준_표현은_바꾸지_않는다` — 「배포 3일 전」「3개월 전체」「2주 후반」
- `TestScreen_1개월_뒤는_달을_넘치지_않는다` — 1월 31일 기준 2월 말
- `TestScreen_사용자_말_발췌는_치환하지_않는다`
- `TestScreen_바꾸지_않은_표현은_rewrites에_바꾸지_않음으로_알린다`

**명령:** `go test ./internal/memory ./internal/mcpserver -run 'TestSave_|TestScreen|TestMemorySave' -count=1`

### Task 07: 묶음 재전달과 정정 줄 — worker-deep

**끝:** 묶음 안 기억의 보임이 바뀌면 다시 만들고, 안 바뀌면 같은 바이트를 돌려준다. 세션 시작 시각과 표시는 지킨다. 무효 줄은 한 번 나온다. 요구: F-1, F-2.

**Files:** `memory/bundle.go`, `memory/supersede.go`(표시 종류 `MarkBundle`), `inject/inject.go:148-183,517-598`

**Interfaces:** Consumes `Viewer`. Produces `ReplaceSessionBundle`, `MarkBundle`.

- `ReplaceSessionBundle`은 시작 시각(`at`)을 건드리지 않고 `WHERE text=<옛 글>` 조건으로 고친다. `MarkBundle`은 `INSERT OR IGNORE`로 남긴다. 두 훅이 동시에 다시 만들어도 한쪽만 적용되고 결과가 같다.
- 정정 줄의 「이미 알린 것」 집합(`inject.go:519-528`)에 `MarkBundle`을 더해, 묶음에서 뺀 기억의 정정·무효 줄이 한 번 나오게 한다.
- 만료는 고친 시각이 바뀌지 않아 `old.UpdatedAt <= at`(`inject.go:548`)에 걸린다. 만료는 만료 시각으로 판정한다.
- 뺀 기억이 되돌려져 다시 현재 값이 되면 다음 다시 만들기에 돌아와도 된다. 막는 것은 「여전히 현재 값이 아닌 기억이 돌아오는 것」이다.

**실패 시험:**
- `TestSessionBundle_묶음_뒤_폐기된_기억은_다음_시작_주입에_없다` — 폐기·만료·대체·삭제 네 경우
- `TestSessionBundle_바뀐_기억이_없으면_같은_글을_돌려준다` — 바이트 동일
- `TestSessionBundle_다시_만들어도_세션_시작_시각과_표시를_지킨다`
- `TestSessionBundle_두번_다시_만들어도_여전히_폐기된_기억은_돌아오지_않는다` (Review Focus 1)
- `TestSessionBundle_동시에_두_번_다시_만들어도_결과가_같다`
- `TestPromptNews_후계_없는_폐기_만료_삭제는_무효_줄을_한_번_낸다`
- `TestPromptNews_같은_기억의_정정은_세션에_한_번이다`

**명령:** `go test ./internal/inject ./internal/memory -run 'TestSessionBundle|TestPromptNews' -count=1`

### Task 08: 조회 경로 표시 — worker

**끝:** `memory_find` 기본 결과에 만료 휘발과 옛 버전이 없고, `memory_get`은 경고 머리를 붙이고, 연결은 상대의 상태와 후계를 보인다. 요구: F-3, F-4, F-5.

**Files:** `memory/store.go`(FindInput, :1133 readFind), `mcpserver/server.go:275-345`, `mcpserver/view.go:104-115,173-198,208-246`, `mcpserver/tools.go`

**실패 시험:**
- `TestMemoryFind_기본은_만료_휘발과_주제의_옛_버전을_뺀다`
- `TestMemoryFind_states_stale이면_폐기됨_만료됨_표시가_붙는다`
- `TestMemoryGet_폐기된_기억은_상태_사유_후계를_머리에_보인다`
- `TestMemoryGet_연결의_폐기된_상대는_폐기됨과_후계를_보인다`

**명령:** `go test ./internal/mcpserver ./internal/memory -run 'TestMemoryFind|TestMemoryGet|TestFind' -count=1`

### Task 09: 주입 시점 상태 기록 — worker-quick

**끝:** 주입 사건에 id마다 상태가 남고, 정리 보고에 죽은 연결 수가 나온다. 요구: F-6.

**Files:** `host/ops.go:226-239`, `memory/links.go`(새 `DeadLinkCount() (int, error)`), `memory/tidy.go`(`TidyResult.DeadLinks`), `cmd/orbit-mem/tidy_cmd.go`

**실패 시험:**
- `TestInjected_주입_기록에_기억_상태가_남는다`
- `TestTidy_살아있는_기억이_폐기된_기억을_가리키는_연결_수를_보고한다`

**명령:** `go test ./internal/host ./internal/memory ./cmd/orbit-mem -run 'TestInjected|TestTidy' -count=1`

### Task 10: 세션 구분과 확정 근거 — worker-deep

**끝:** 같은 세션에서 날만 넘긴 두 번은 확정되지 않고, 다른 세션·다른 날 두 번은 확정된다. 요구: E-1, E-2, E-3.

**Files:** 새 `hook/claudecode/session.go`, `cmd/orbit-mem/memory_cmds.go:104`, `mcpserver/server.go`(Options, save), `mcpserver/tools.go:138`, `memory/store.go`(SaveInput.Session, :597-601, :631-655 sightTx), `memory/rules.go:159-233`

**Interfaces:** Consumes `sighting_sessions`. Produces 표의 10 줄. `const SessionEnv = "CLAUDE_CODE_SESSION_ID"`. 조립: `Session: firstNonEmpty(claudecode.MCPSession(os.Getenv), "run-"+memory.NewID(""))`. `firstNonEmpty`는 저장소에 없으므로 이 Task가 조립 파일 안에 만든다. 「세션/agent」 꼴 키는 앞 세션으로 접는다. E-2의 거절 기록은 「후보가 생긴 뒤에 거절된, 같은 범위·주제의 hidden 기억」이다. 후보가 생기기 전의 거절은 세지 않는다. 주제가 없는 기억에는 E-2의 거절 조건을 적용하지 않는다(어긋남 쌍 조건만 본다).

**실패 시험:**
- `TestEvidence_같은_세션에서_날만_넘겨_두번_저장하면_확정되지_않는다`
- `TestEvidence_다른_세션_다른_날_두번이면_확정된다`
- `TestEvidence_같은_주제에_거절된_기억이나_어긋남이_있으면_확정되지_않는다`
- `TestEvidence_후보가_생기기_전의_거절은_확정을_막지_않는다`
- `TestEvidence_옛_기록만_있는_후보는_legacy_세션_하나로_세어_확정되지_않는다`
- `TestMCPSession_환경_변수가_없으면_빈_값을_돌려준다`
- `TestCall_memory_save는_요청_세션을_다시_나온_기록에_남긴다`

**명령:** `go test ./internal/memory ./internal/mcpserver ./internal/hook/... ./cmd/orbit-mem -run 'TestEvidence|TestMCPSession|TestCall' -count=1`

### Task 11: 도구 입력 기준 회상 — worker-deep

**끝:** 규칙이 아닌 기억은 도구 입력과 관련이 있을 때만 붙고, 입력 조건이 달린 규칙은 조건 낱말이 있을 때만 붙고, 입력이 없으면 지금 동작이다. 요구: T-1, T-2, T-4.

**Files:** `host/contract.go`, `host/ops.go:347-355`, `hook/claudecode/claudecode.go:119-123`, `hook/codex/codex.go`, 새 `memory/toolcond.go`, `memory/supersede.go:91-135`(cleanTools, TriggeredBy), `inject/tool.go`, `config/config.go`, 새 `contract/host/v1/tool-context-input.json`, `contract/host/v1/README.md`, `bench/hostpath/hostpath.go`

**Interfaces:** Consumes `host.ToolInputText`, `Viewer`. Produces 표의 11 줄. 입력 조건 문법은 tools 칸의 `Bash(git commit)`이다. 계약 버전은 1 그대로다. `ToolInputGate`의 기본값은 이 Task에서 false로 둔다(Task 18이 정한다). 시험은 설정을 켜서 돌린다.

**실패 시험:**
- `TestToolInjection_교훈은_도구_입력과_관련이_없으면_붙지_않는다`
- `TestToolInjection_확정_규칙은_도구_이름만으로_붙는다`
- `TestToolInjection_입력_조건이_달린_규칙은_ls에_붙지_않고_git_commit에_붙는다`
- `TestToolInjection_입력이_없으면_이름으로만_판정한다`
- `TestToolInjection_toolInputGate를_끄면_지금_동작이다`
- `TestClaudeAdapter_PreToolUse의_tool_input을_toolInput으로_싣는다`
- `TestContract_tool_context_input_짝`

**명령:** `go test ./internal/inject ./internal/host ./internal/hook/... ./contract/... ./internal/memory -run 'TestToolInjection|TestClaudeAdapter|TestContract|TestTrigger' -count=1`

### Task 12: 발언 기록과 사용자 말 대조 — worker-deep

**끝:** 사람 발언의 조각 해시만 30일 보관하고, 사용자 말 출처 대조와 거절된 제목의 처리 결과를 정직하게 답한다. 요구: S-4, S-2.

**Files:** 새 `memory/utterance.go`, `host/ops.go:325-343`(`PersonWords`가 가른 사람 발언만 기록), `mcpserver/server.go:593-649`, `memory/store.go:504-535`(hidden 갈래), `memory/tidy.go`

**Interfaces:** Consumes utterances 표, 06의 `SaveReport`. Produces 표의 12 줄. 대조: 발췌 조각 E에 대해 `max_u |E∩U|/|E| ≥ 문턱`. 조각 크기(제안 2~3 낱말)와 문턱(제안 0.8)은 시험으로 정해 상수로 둔다. 통로가 사람(TrustLimit owner)이면 대조하지 않는다. `not_found`면 출처를 session 후보로 낮추고 경고, `unchecked`면 지금 동작에 「대조 못 함」 경고.

**실패 시험:**
- `TestRecordUtterance_원문은_남기지_않고_조각_해시만_남긴다` — DB 덤프에 원문 낱말이 없다
- `TestMatchQuote_최근_30일_발언의_발췌면_matched다`
- `TestMatchQuote_없는_발췌는_not_found다`
- `TestMatchQuote_발언_기록이_없으면_unchecked다`
- `TestMatchQuote_조각보다_짧은_발췌는_통째로_일치할_때만_matched다` (Review Focus 3)
- `TestRecall_사람_발언이_아니면_기록하지_않는다`
- `TestMemorySave_대조에_없는_quote는_AI_출처_후보로_저장하고_알린다`
- `TestMemorySave_거절된_제목을_대조_통과_quote로_다시_저장하면_되살려_확정한다`
- `TestMemorySave_거절된_제목의_그밖_저장은_저장하지_않았다고_사유와_답한다` — `saved=false`
- `TestTidy_30일_지난_발언_기록을_지운다`

**명령:** `go test ./internal/memory ./internal/mcpserver ./internal/host -run 'Utterance|MatchQuote|TestRecall|TestMemorySave|TestTidy' -count=1`

### Task 13: 순위 함수 — worker

**끝:** 순위 함수 하나를 요청·위임·도구 직전 회상이 쓰고, 스위치를 끄면 공개 묶음 기준선이 지금과 같다. 요구: R-1, R-3.

**Files:** 새 `inject/rank.go`, `inject/inject.go:798-836`(지움), `inject/tool.go`(정렬), `config/config.go`

**실패 시험:**
- `TestRank_점수_차가_문턱보다_크면_후보가_약한_확정보다_앞이다`
- `TestRank_문턱_안이면_확정이_앞이다`
- `TestRank_점수와_확정이_같으면_재검토_후보가_뒤다`
- `TestRank_rankByScore를_끄면_지금_순위와_같다`

**명령:** `go test ./internal/inject ./internal/bench -run 'TestRank|TestPublicSet' -count=1`

### Task 14: 대체 정책 표와 자동 모드 — worker-deep

**끝:** 표대로 저장하고, 자동 대체는 원장에 남아 되돌릴 수 있고, 되돌린 쌍은 다시 대체되지 않는다. 요구: C-1, C-3, C-4, C-7, C-8 저장 쪽, C-9, E-4.

**Files:** 새 `memory/replace.go`, `memory/sourcecheck.go`(이미 있는 파일에 더한다), `memory/supersede.go:17-89`, `memory/store.go:427-460`, `memory/ledger.go:401-`, `memory/rules.go`(Policy, :159-233), `mcpserver/server.go:617-625`(재확인), `config/config.go`, `cmd/orbit-mem/memory_cmds.go`(policyOf, LoadPolicy), `host/ops.go:89`

**Interfaces:** Consumes `sources.rechecked`, `supersede_reverts`. Produces 「대체 정책」 전부와 표의 14 줄. 자동 대체는 `supersedeTx` 안에서 기존 `insertProposalTx`·`recordLedgerTx`로 원장 줄을 남겨 기존 `RevertChange`로 되돌린다. 원장 줄의 꼴과 새 근거를 정하는 법은 「대체 정책」의 「표를 쓸 때 지킬 것」을 따른다. 되돌리면 새 기억을 후보로 내리고 `supersede_reverts`에 쌍을 넣는다. `confirmBlocked`는 그 새 id를 막는다. `EvidenceFor`는 `rechecked=1`인 검증 사실만 즉시 확정한다.

**실패 시험:**
- `TestReplace_표의_각_줄을_자동_모드_켬과_끔으로_가른다` — 표 기반
- `TestSave_서버가_다시_확인한_검증_사실은_사용자_말_사실을_자동_대체한다`
- `TestSave_자동_모드_끔이면_명령_출력_근거의_검증_사실은_어긋남만_남긴다`
- `TestSave_자동_모드_켬이면_근거_적힌_검증_사실이_대체한다`
- `TestSave_사용자_범위_사실은_자동_대체하지_않는다`
- `TestSave_규칙_결정은_검증_출처여도_상태가_바뀌지_않는다`
- `TestSave_세션_출처_사실은_사용자_말_사실을_대체하지_않는다`
- `TestRevert_자동_사실_대체를_되돌리면_옛_것은_확정_새_것은_후보이고_다시_대체되지_않는다`
- `TestEvidence_서버가_다시_확인_못한_검증_사실은_즉시_확정하지_않는다`
- `TestRecheckExcerpt_없는_파일과_상대경로는_false다` (Review Focus 2)
- `TestRevert_즉시_확정과_같은_제목_재저장_뒤에도_되돌리기가_성공한다` — 대체 → 새 기억 즉시 확정 → 같은 제목 재저장 → 되돌리기 성공
- `TestSave_예전에_사용자_말이_붙은_기억을_AI가_다시_저장해도_사용자_규칙을_대체하지_않는다` — 새 근거는 이번 저장의 출처로만 정한다

**명령:** `go test ./internal/memory ./internal/mcpserver ./cmd/orbit-mem -run 'TestReplace|TestSave_|TestRevert|TestEvidence|TestRecheck' -count=1`

### Task 15: 어긋남·알림 표시 — worker

**끝:** 어긋남이 있는 기억 줄에만 새 사실을 나란히 보이고, 확인 지시는 자동 모드가 꺼져 있을 때 세션마다 쌍마다 한 번이고, 알림 줄과 개수 줄은 0건이면 없다. 요구: C-2, C-5, C-6, C-8 표시.

**Files:** `memory/visible.go`(Conflicts 채우기), `memory/audit.go`, `memory/replace.go`(`AutoSupersedesSince`와 알림 표시점 `meta`), `inject/inject.go:232-363,838-855`, `inject/tool.go`(toolLine), `mcpserver/view.go`

**실패 시험:**
- `TestRecallLine_어긋남이_있으면_새_사실을_나란히_보인다`
- `TestRecallLine_자동_모드_끔이면_확인_지시가_세션에_쌍마다_한_번이다`
- `TestRecallLine_자동_모드_켬이면_확인_지시가_없다`
- `TestRecallLine_어긋남_없는_기억에는_아무것도_붙지_않는다`
- `TestSessionStart_자동_대체_알림은_한_번만_나온다`
- `TestSessionStart_넘어간_어긋남_개수_줄은_0건이면_없다`
- `TestMemoryGet_어긋나는_새_사실을_보인다`
- `TestMemoryFind_어긋나는_새_사실을_보인다`
- `TestConflict_사용자_말로_저장하면_어긋남이_풀린다`
- `TestAutoMode_끈_뒤에도_이미_대체된_것은_그대로이고_새_어긋남은_묻는다` (Review Focus 5)

**명령:** `go test ./internal/inject ./internal/mcpserver ./internal/memory -run 'TestRecallLine|TestSessionStart|TestMemoryGet|TestConflict|TestAutoMode' -count=1`

### Task 16: 별칭과 찾기 질의 — worker

**끝:** 별칭 낱말로 찾아지고, 별칭은 본문 한도와 주입 줄 밖이고, `memory_find` 질의의 조사를 떼고, `tools/list`가 8,000룬 이하다. 요구: Q-1~Q-4.

**Files:** `memory/model.go`, `memory/store.go`(SaveInput·UpdateInput `Aliases`), `memory/index.go:94-101`, `memory/rank.go:94`, `memory/recallmatch.go`, `memory/stem.go`, `mcpserver/server.go`(SaveIn·UpdateIn `aliases`, find 질의), `mcpserver/tools.go`(별칭 칸 설명. 줄이기는 Task 06이 이미 했다), `config/config.go`

**실패 시험:**
- `TestFind_별칭에만_있는_낱말로_찾으면_나온다`
- `TestUpdate_별칭을_고치면_새_별칭으로_찾아지고_옛_별칭으로는_안_찾아진다`
- `TestSave_별칭은_본문_한도에_세지_않는다`
- `TestRecallLine_별칭은_주입_줄에_없다`
- `TestMemoryFind_배포를로_찾으면_배포가_든_기억이_나온다` — 지금 코드로 이미 통과할 수 있다. 먼저 돌려 결과를 보고에 적는다
- `TestMemoryFind_붙여_쓴_한글_낱말의_일부로_찾아진다`
- 기존 `TestSurface_tools_list는_8000룬_이하이고_출력_스키마가_없다`

**명령:** `go test ./internal/memory ./internal/mcpserver -run 'TestFind|TestSave_|TestMemoryFind|TestSurface|TestRecallLine' -count=1 -v`

### Task 17: 90일 재검토 — worker

**끝:** 마지막 확인 뒤 90일이면 normal 기억도 재검토 후보이고, 같은 세션 재등장·조회·주입은 확인이 아니고, 세션 시작에 개수 한 줄이 나온다. 요구: L-1, L-2, L-3.

**Files:** `memory/lifecycle.go`(Freshness 기본값: `ReviewAt==0`이고 휘발이 아니면 `UpdatedAt+90일`, 새 `confirmCheckTx`), `memory/store.go`(update :876-, sightTx는 다른 세션일 때만, create), `inject/inject.go`(「재검토 후보」 표시, 개수 줄)

**실패 시험:**
- `TestFreshness_마지막_확인_90일_뒤면_보통_기억도_재검토_후보다`
- `TestSight_같은_세션에서_다시_나온_것은_확인이_아니다`
- `TestSight_다른_세션에서_다시_나오면_재검토일이_밀린다`
- `TestUpdate_사용자_말로_고치면_재검토일이_밀린다`
- `TestProposal_감사_목록에서_유지로_정하면_재검토일이_밀린다`
- `TestSessionStart_재검토_후보_개수_줄은_0건이면_없다`
- `TestRecallLine_재검토_후보는_빼지_않고_표시만_한다`

**명령:** `go test ./internal/memory ./internal/inject -run 'TestFreshness|TestSight|TestSessionStart|TestRecallLine' -count=1`

### Task 18: 문턱 정하기와 스위치 판정 — worker

**끝:** 명세 7절 지표가 기준값 이상인 문턱만 기본값이 되고, 못 넘는 항목은 스위치를 끄고, 결과가 `bench/README.md` 판정 기록에 적힌다. 요구: R-2, T-3.

**Files:** `config/config.go` Default, `bench/testdata/public_set.json`(폐기 주입·재개·도구 입력 사례 추가), `baseline-current.json`, `bench/README.md`

**실패 시험:** `TestPublicSet_폐기된_기억_주입_사례가_0건이다`

**명령:** 작업 브랜치 끝에서 빌드한 임시 실행 파일로 `<bin> bench recall --db $ORBIT_HOME/bench/replay/0.19.0/memory-copy.db --cases $ORBIT_HOME/bench/replay/0.19.0/cases.json --set rankByScore=true --set rankScoreMargin=<값>`과 `--set toolInputGate=true --set toolInputMinScore=<값>`으로 값을 훑는다. 재생 사본은 실행 파일이 이행한 별도 사본을 쓰고 0.19.0 폴더의 원본 사본은 고치지 않는다. 이어서 `go test ./internal/bench -run TestPublicSet -update-golden`. holdout6은 여기서 쓰지 않는다(Task 19에서 한 번만). 훑은 값과 지표를 표로 보고에 적는다.

### Task 19: 최종 검증과 업그레이드 리허설 — qa

**끝:** 명세 7절 끝 기준 3~8이 기준값과 비교해 모두 판정된다. 이 PC DB 사본을 6으로 올리면 열리고 기억 수가 같다. 다시 색인에 걸린 시간을 잰다. holdout6 판정을 한 번 돈다.

실행 파일은 둘이다. 옛 것은 Task 02가 남긴 `$ORBIT_HOME/bench/replay/0.19.0/bin/orbit-mem`, 새 것은 작업 브랜치 끝에서 저장소 밖 임시 경로에 빌드한 것이다.

**명령과 판정:**
1. **재생**: 0.19.0 폴더의 `memory-copy.db`를 새 폴더에 복사하고, 같은 `cases.json`으로 새 실행 파일의 `bench recall --recall current,legacy --split all`을 돌린다. 경로별 평균 글자가 기준값 이하인지, start 경로 `Dead`가 0인지, 기억 `m-5d1c0ab2276b`·`m-1bc7d455c05c`가 tool 경로에 0회인지 판정한다.
2. **블라인드**: holdout6을 옛 실행 파일과 새 실행 파일로 같은 조건에서 각 한 번 돌려 찾아지는 비율·틀린 회상을 견준다. 최종 공개 묶음도 같은 방식으로 견준다(Task 18이 사례를 더했으므로 Task 02의 공개 묶음 값과 직접 견주지 않는다).
3. **업그레이드 리허설**: 실제 DB의 새 바이트 사본을 만든 폴더에서 `ORBIT_HOME=<사본 폴더> <새 bin> migrate`, 이어서 `ORBIT_HOME=<사본 폴더> <새 bin> doctor`. 기억 수가 이행 전과 같은지, 이행에 걸린 시간이 5초 미만인지(쓰기 대기 시간 `open.go:24`) 잰다. `ORBIT_HOME`을 빠뜨리지 않는다.
4. `go test ./... && go vet ./...`

결과는 저장소 밖에만 쓴다. 못 넘는 항목은 고치지 말고 보고한다.

### Task 20: 문서 정합 — writer

**Files:** `README.md`, `contract/host/v1/README.md`, `internal/bench/README.md`. 세션 시작에 넣는 것(상시 핵심 + 최근 결정 제목 줄), 바뀐 결정 넷(확정 우대, 확정 근거, 사실의 자동 대체와 자동 모드, 90일 재검토), 새 설정 키를 반영한다. 시험 없음. Atlas 설계 정본과 기억 정리는 진행자가 한다.

### Task 21: 버전 올림 — worker-quick

**끝:** 0.20.0이고 전체 시험과 vet가 통과한다.

**Files:** 커밋 72c3dc3의 꼴을 따른다: `internal/version/version.go`, `plugin/.claude-plugin/plugin.json`, `plugin/hooks/hooks.json`(`--plugin-version`), `.claude-plugin/marketplace.json`, `cmd/orbit-mem/install_test.go`, `cmd/orbit-mem/main_test.go`

**명령:** `go test ./... && go vet ./...`

---

## 선후와 병렬 묶음

동시에 셋까지. 같은 묶음의 구현 일꾼은 각자 worktree에서 일한다. 묶음이 끝나면 진행자가 표의 순서대로 `feat/memory-accuracy-recall`에 합치고 전체 시험을 한 번 돌린 뒤 그 일꾼의 worktree만 지운다. 일꾼은 시작할 때 작업 브랜치의 최신을 먼저 받는다.

| 묶음 | Task | 메모 |
| --- | --- | --- |
| 1 | 01 · 03 · 04 | |
| 2a | 02 · 02b · 05 | 02와 02b는 01 커밋 기준, 소스 겹침 없음 |
| 2b | 06 | 02나 02b가 끝나 자리가 나면 시작. 합치는 순서는 05 → 06 |
| 3 | 07 · 08 · 09 | |
| 4 | 10 · 11 | |
| 5 | 12 · 13 · 01b | 01b는 `internal/bench`만 고쳐 다른 Task와 겹치지 않는다. 끝나면 폐기 주입 재현 칸을 다시 잰다 |
| 6 | 14 | |
| 7 | 15 · 16 | |
| 8 | 17 | |
| 9 | 18 | |
| 10 | 19 · 20 | |
| 11 | 21 | 뒤에 `architect` 전체 검토 |

겹치는 파일의 순서: `memory/store.go` 03→06→08→10→12→14→16→17, `mcpserver/server.go` 06→08→10→12→14→16, `inject/inject.go` 05→07→13→15→17, `inject/tool.go` 05→11→13→15, `host/ops.go` 09→11→12→14, `config/config.go` 11→13→14→16→18, `memory/rules.go` 10→14, `memory/supersede.go` 07→11→14, `memory/tidy.go` 09→12, `mcpserver/tools.go` 06→08→10→16. 새 문구는 그 Task 파일 안의 한글 문자열 꼴을 따르고, i18n 키를 더하는 Task는 묶음에 하나만 둔다.

## 허용 위험 단계

| 단계 | 허용 여부 |
| --- | --- |
| `main`에서 작업 브랜치 `feat/memory-accuracy-recall` 만들기, 이 계획 파일 커밋 | 허용 |
| 일꾼 worktree에서 커밋, 작업 브랜치로 로컬 병합, 그 일꾼 worktree 지우기 | 허용 |
| 실제 기억 DB를 읽어 저장소 밖에 사본 만들기, 사본에서 측정·이행 리허설 | 허용. 사본 이행은 `ORBIT_HOME=<사본 폴더>` 지정이 필수이고, 임시로 빌드한 실행 파일만 쓴다 |
| 이슈 프로젝트 ORBIT에 에픽과 작업 이슈 만들기, 진행 기록 | 허용 |
| Atlas 명세의 사실 정정, 설계 정본 결정 표 반영(Task 20 때) | 허용 |
| 실제 기억 DB에 쓰기·스키마 이행 | 금지. 설치본 반영 때 사람이 정한다 |
| 이 PC 설치본·플러그인 갱신(`orbit-mem update`·`install`) | 금지. 끝난 뒤 묻는다 |
| 푸시, 원격 브랜치, PR, 태그, 릴리스 발행 | 금지. 끝난 뒤 묻는다 |
| 새 의존성·프로그램 설치 | 금지 |
| 이 PC 설정에서 자동 모드 켜기 | 금지. 사용자 몫 |
| 기억 m-5db1ee1f3c51 수정, m-0b6dbf87cc48 낡음 처리 | 금지. 보고 때 사용자 말을 받아 한다 |
| 다른 세션의 worktree·브랜치 정리 | 금지 |

## 위험과 빠져나갈 길

- **스키마 이행은 되돌리기 어렵다.** 6으로 올린 DB는 0.19.0이 열지 못한다. 되돌릴 길은 열기 전 사본뿐이고, 업그레이드 뒤 쓴 기억은 잃는다. 실제 DB 이행은 Task 19 리허설 뒤, 설치본 반영 때 한 번만 일어난다.
- **지표를 못 넘으면** `rankByScore=false`, `findStripParticles=false`, `toolInputGate=false`로 그 항목만 끄고 릴리스하며 보고에 적는다. F-1은 스위치가 없다.
- **세션 시작 글자 수가 기준을 넘으면**(C-2 알림 줄, 개수 줄들 때문에) 고치지 말고 사용자에게 묻는다: 새 줄을 감사 목록으로만 돌릴지, 실패 조건을 고칠지.
- **`CLAUDE_CODE_SESSION_ID`가 없거나 이름이 바뀌면** 서버 실행 단위 id로 떨어진다. 확정이 늦어질 뿐 잘못 확정하지 않는다.

## 기준값 (Task 02가 채운다)

글자 수 판정(2026-11-23 사용자 결정으로 고침. 이전: 경로별 평균을 그대로 기준값과 비교): 경로별 평균 글자 수는 재검토 후보·어긋남·자동 대체 알림 줄이 없는 주입끼리 비교해 판정한다. 알림 줄이 붙은 주입의 평균은 참고값으로 따로 재서 함께 보고한다. 실제 기록 재생과 공개 묶음 모두 이렇게 나눈다. 알림 줄은 해당 건이 0건이면 붙지 않는다.

| 지표 | 0.19.0 값 |
| --- | --- |
| 경로별 평균 글자(start·request·tool·subagent) | 호출당 / 넣은 호출당. start 1427.6 / 2057.3 (호출 1,369, 넣음 950) · request 39.7 / 358.1 (19,493, 2,161) · tool 2.98 / 325.7 (143,776, 1,317) · subagent 406.0 / 715.9 (6,196, 3,514) |
| 폐기 주입 수(기록 감사 / 재생) | 감사: 주입 사건 21,650, 실린 id 130,155, 현재 값이 아니던 id 92회(사건 53, 세션·기억 쌍 63, 기억 32, 세션 34). 까닭 낡음 80·지움 12, 경로 세션 시작 91·위임 1. 재생: start 경로 5 id(지움, 호출 1), 나머지 경로 0 |
| 80회 재현 여부 | 첫 측정은 B/A = 0/63(지움 제외 0/55)이었다. 원인은 사례 파일에 제품 지도가 없어 재생 묶음에 제품 핵심·제품 결정이 빠진 것이다(재생이 세션 묶음을 비워서가 아니다. 비우는 것은 실행 전 한 번뿐이다). Task 01b 뒤, 제품 지도를 넣은 0.19.0 측정 빌드(`3258f97` + `a17aec8`): 대화 기록의 시작 사례만으로 낡음 3쌍, 대화 기록에 없는 시작을 주입 사건에서 보태면 0.11.0 이후의 낡음 쌍 26개가 모두 재현된다. 같은 사례로 작업 브랜치(`a17aec8`)는 0. 재현되지 않는 것: 0.11.0 이전 버전의 동작으로 생긴 28쌍, 지운 기억 8쌍, 위임 1쌍(낡음 처리 186ms 뒤, 동시 실행 경합으로 추정) |
| 무관 반복 사례 둘(도구 직전에 실린 횟수) | 재생 154회·497회, 사건 기록 2,025회·4,960회 |
| 공개 묶음 적중·틀린 회상 | 요청 적중 82/91 = 0.9011, 틀린 회상 6/98 = 0.0612, 위임 적중 5/21, 위임 틀린 회상 0/19, 중복 적중 22/25 |

위 경로별 글자 수와 무관 반복 횟수는 제품 지도 없이 잰 값이다. 제품 지도는 모든 경로의 값을 바꾸므로 최종 검증은 아래 값(같은 제품 지도를 준 0.19.0 측정 빌드, 지금 회상 경로)과 견준다.

| 경로 | 호출 수(넣은 수) | 평균 글자: 호출당 / 넣은 호출당 | 현재 값이 아닌 id |
| --- | --- | --- | --- |
| start | 1,369 (950) | 1654.8758 / 2384.7632 | 8(낡음 3, 지움 5, 호출 3) |
| request | 19,493 (2,541) | 45.8372 / 351.6348 | 0 |
| tool | 143,776 (1,361) | 3.1643 / 334.2799 | 0 |
| subagent | 6,196 (3,514) | 580.8925 / 1024.2487 | 0 |

무관 반복 사례 둘이 도구 직전에 실린 사례 수는 191과 529다. 전체 재생은 43.8분, 메모리 최대 약 1.85GB였다. start만 재생한 값(2.3분)은 전체 재생의 start 값과 같았다. 재생 재료는 `$ORBIT_HOME/bench/replay/0.19.0/`에 있다: `bin/orbit-mem-0.19.0-replay`, `products/config-fixed.json`(고정한 제품 지도), `synth/`(주입 사건에서 만든 시작 사례), `tools/`(분석 스크립트).

표본: 세션 858(합친 28, 이어받은 22), 시작 사례 1,323, 요청 19,493, 위임 6,196, 도구 사례 143,776. 측정 빌드는 `3258f97`(0.19.0 동작에 측정 도구 고침만 더한 것). 전체 재생 한 번에 약 61분, 메모리 최대 약 2.1GB. 재생 결과가 기준값 이하인지 견줄 때는 호출당 값과 넣은 호출당 값을 둘 다 본다.

package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// Store is this PC's memory database (one SQLite file).
type Store struct {
	db   *sql.DB
	path string
	Now  func() time.Time
	// Policy holds the counts of the confirmation rules (A2). Open sets the
	// defaults; the command line sets the configured ones.
	Policy Policy
	// findStep, when set, runs inside FindScored after its first read (tests put another connection's
	// write there).
	findStep func()
	// rankStep, when set, runs in FindScored after its read transaction ended, before it filters and orders.
	rankStep func()
}

const schema = `
CREATE TABLE IF NOT EXISTS memories (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  scope TEXT NOT NULL,
  project TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  title TEXT NOT NULL,
  title_key TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  version INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  superseded_by TEXT NOT NULL DEFAULT '',
  stale_reason TEXT NOT NULL DEFAULT '',
  legacy_id TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS memories_legacy ON memories(legacy_id) WHERE legacy_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS memories_key ON memories(kind, scope, title_key);
CREATE TABLE IF NOT EXISTS memory_versions (
  memory_id TEXT NOT NULL, version INTEGER NOT NULL, action TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, snapshot TEXT NOT NULL, PRIMARY KEY(memory_id, version));
CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY, memory_id TEXT NOT NULL, kind TEXT NOT NULL, ref TEXT NOT NULL DEFAULT '',
  excerpt TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS sources_memory ON sources(memory_id);
CREATE TABLE IF NOT EXISTS sightings (memory_id TEXT NOT NULL, day TEXT NOT NULL, PRIMARY KEY(memory_id, day));
CREATE TABLE IF NOT EXISTS proposals (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '',
  memory_ids TEXT NOT NULL DEFAULT '[]', payload TEXT NOT NULL DEFAULT '{}', fingerprint TEXT NOT NULL,
  status TEXT NOT NULL, created_at INTEGER NOT NULL, decided_at INTEGER NOT NULL DEFAULT 0,
  retro_id TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS proposals_fp ON proposals(fingerprint);
CREATE TABLE IF NOT EXISTS retros (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, period TEXT NOT NULL, status TEXT NOT NULL, host TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '',
  flags TEXT NOT NULL DEFAULT '[]', journal_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS retros_period ON retros(kind, period);
CREATE TABLE IF NOT EXISTS absorbed (path TEXT PRIMARY KEY, hash TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS session_ends (
  host TEXT NOT NULL, session TEXT NOT NULL, transcript TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
  ended_at INTEGER NOT NULL, retro_id TEXT NOT NULL DEFAULT '', PRIMARY KEY(host, session));
CREATE TABLE IF NOT EXISTS retro_coverage (
  session_key TEXT NOT NULL, from_at INTEGER NOT NULL, to_at INTEGER NOT NULL, retro_id TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS retro_coverage_key ON retro_coverage(session_key);
CREATE TABLE IF NOT EXISTS session_bundles (
  host TEXT NOT NULL, session TEXT NOT NULL, text TEXT NOT NULL, memory_ids TEXT NOT NULL DEFAULT '[]', at INTEGER NOT NULL,
  PRIMARY KEY(host, session));
CREATE TABLE IF NOT EXISTS judgments (
  id TEXT PRIMARY KEY, at INTEGER NOT NULL, retro_id TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  memory_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', excerpt TEXT NOT NULL DEFAULT '',
  verdict TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', proposal_id TEXT NOT NULL DEFAULT '',
  user_decision TEXT NOT NULL DEFAULT '', user_note TEXT NOT NULL DEFAULT '', decided_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS judgments_memory ON judgments(memory_id) WHERE memory_id <> '';
CREATE TABLE IF NOT EXISTS ledger (
  id TEXT PRIMARY KEY, at INTEGER NOT NULL, day TEXT NOT NULL, proposal_id TEXT NOT NULL, proposal_kind TEXT NOT NULL,
  target_kind TEXT NOT NULL, targets TEXT NOT NULL DEFAULT '[]', observe TEXT NOT NULL DEFAULT '[]',
  change TEXT NOT NULL DEFAULT '', basis TEXT NOT NULL DEFAULT '', before TEXT NOT NULL DEFAULT '{}',
  verdict TEXT NOT NULL, evidence INTEGER NOT NULL DEFAULT 0, observation TEXT NOT NULL DEFAULT '{}', judged_at INTEGER NOT NULL DEFAULT 0,
  lesson_id TEXT NOT NULL DEFAULT '', revert_of TEXT NOT NULL DEFAULT '', reverted_by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ledger_at ON ledger(at);
CREATE TABLE IF NOT EXISTS session_marks (
  host TEXT NOT NULL, session TEXT NOT NULL, memory_id TEXT NOT NULL, kind TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY(host, session, kind, memory_id));
PRAGMA user_version=3;
`

// addColumns adds the columns later versions put on tables an older version made.
func addColumns(db *sql.Tx) error {
	for _, c := range []struct{ table, column, def string }{
		{"retros", "session", "TEXT NOT NULL DEFAULT ''"},
		// 보존 등급·재검토일·주제(기획 · Orbit Code K15). 옛 판 실행 파일은 열 이름을 대어 읽고 쓰므로
		// 이 열을 모른 채 같은 DB를 써도 깨지지 않고, 옛 판이 넣은 줄은 기본값(보통·날짜 없음·주제 없음)이 된다.
		{"memories", "retention", "TEXT NOT NULL DEFAULT ''"},
		{"memories", "review_days", "INTEGER NOT NULL DEFAULT 0"},
		{"memories", "review_at", "INTEGER NOT NULL DEFAULT 0"},
		{"memories", "expires_at", "INTEGER NOT NULL DEFAULT 0"},
		{"memories", "topic", "TEXT NOT NULL DEFAULT ''"},
		// 도구 트리거(설계 · orbit-mem 2판 6절). 옛 판은 이 열을 모르고 쓰므로 그 줄은 트리거가 없다.
		{"memories", "tools", "TEXT NOT NULL DEFAULT '[]'"},
		// 상시 핵심 승격(설계 · orbit-mem 2판 4.4, S3). 옛 판은 이 열을 모르고 쓰므로 승격을 건드리지 않는다.
		{"memories", "pinned", "INTEGER NOT NULL DEFAULT 0"},
		// 활동 기록의 하루 요약(S4-8: 일지 기억을 옮겨 온 것). 옛 판 훅은 이 열을 모르고 넣으므로 그 줄은 빈 요약이다.
		{"activity", "summary", "TEXT NOT NULL DEFAULT ''"},
		// 출처 등급(판 5, 설계 9절). 옛 판은 이 열을 모르고 넣으므로 그 줄은 빈 등급이고, 읽을 때 kind로 정한다.
		{"sources", "trust", "TEXT NOT NULL DEFAULT ''"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			// 다른 프로세스(훅·MCP)가 같은 순간에 먼저 더했으면 이미 있는 열이다.
			if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.def); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS retros_session ON retros(session) WHERE session <> ''`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS memories_topic ON memories(scope, topic) WHERE topic <> ''`)
	return err
}

// labSchema is the harness lab's tables. The lab was removed (2026-11-08, ORBIT-13); the tables
// stay so the database format does not change (기획 · orbit-mem 원칙 5).
const labSchema = `
CREATE TABLE IF NOT EXISTS lab_experiments (
  id TEXT PRIMARY KEY, task_id TEXT NOT NULL, status TEXT NOT NULL, verdict TEXT NOT NULL DEFAULT '',
  runs INTEGER NOT NULL, files TEXT NOT NULL DEFAULT '[]', hashes TEXT NOT NULL DEFAULT '{}', picks TEXT NOT NULL DEFAULT '[]',
  cost_usd REAL NOT NULL DEFAULT 0, ledger_id TEXT NOT NULL DEFAULT '', proposal_ids TEXT NOT NULL DEFAULT '[]',
  note TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS lab_runs (
  experiment TEXT NOT NULL, candidate TEXT NOT NULL, round INTEGER NOT NULL, cost_usd REAL NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, ms INTEGER NOT NULL DEFAULT 0,
  ok INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(experiment, candidate, round));
`

// migrate creates and upgrades the tables in one immediate transaction. Taking the
// write lock first lets the busy timeout apply when several processes open a fresh
// or old DB together; a statement that reads first and then writes would get
// SQLITE_BUSY at once instead of waiting. Under that lock it reads the schema version
// again: a DB a newer orbit-mem migrated meanwhile is left alone (ErrSchemaNewer), so
// the meta schema.version it writes never goes down.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return i18n.Errorf("memory.err.db_open", err)
	}
	defer tx.Rollback()
	v, err := dbVersion(tx)
	if err != nil {
		return i18n.Errorf("memory.err.db_open", err)
	}
	if v > SchemaVersion {
		return schemaNewerError(v)
	}
	var indexed, marks int // 색인 표가 이미 있으면 채우지 않는다: 열기는 늦은 줄을 채우지 않는다(채우기는 쓰기 트랜잭션 끝에서)
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memory_index'`).Scan(&indexed); err != nil {
		return i18n.Errorf("memory.err.db_open", err)
	}
	if err := tx.QueryRow(lateObjectsSQL).Scan(&marks); err != nil {
		return i18n.Errorf("memory.err.db_open", err)
	}
	if _, err := tx.Exec(schema + eventsSchema + linksSchema + labSchema + activitySchema + auditSchema + indexSchema); err != nil {
		return i18n.Errorf("memory.err.db_open", err)
	}
	if err := addColumns(tx); err != nil {
		return i18n.Errorf("memory.err.db_upgrade", err)
	}
	switch {
	case indexed == 0: // 판 4 → 5: 같은 트랜잭션에서 기억 전부를 색인한다
		if _, err := fillStaleTx(tx, 0); err != nil {
			return i18n.Errorf("memory.err.db_upgrade", err)
		}
	case marks < 3: // 늦은 표시가 없던 판 5 DB: 그동안의 쓰기는 표시가 없으니 전체 검사로 표시한다(채우지는 않는다)
		if err := markStaleTx(tx); err != nil {
			return i18n.Errorf("memory.err.db_upgrade", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		metaSchemaVersion, strconv.Itoa(SchemaVersion)); err != nil {
		return i18n.Errorf("memory.err.db_upgrade", err)
	}
	return tx.Commit()
}

// Open opens (creating if needed) the memory database at path for writing and
// migrates it. The directory is private to the user. A DB a newer orbit-mem made is
// not migrated: Open returns ErrSchemaNewer. Read operations use OpenRead instead.
func Open(path string) (*Store, error) {
	db, err := sqlitedb.Open(path, writeBusy, storePragmas...)
	if err != nil {
		return nil, i18n.Errorf("memory.err.db_open", err)
	}
	info, err := schemaInfo(db)
	if err != nil {
		db.Close()
		return nil, i18n.Errorf("memory.err.db_open", err)
	}
	if info.State == SchemaNewer {
		db.Close()
		return nil, schemaNewerError(info.DB)
	}
	// 스키마를 바꾸기 전에는 하루 한 번 규칙과 관계없이 사본을 남긴다(ONID-136). 사본을 못 만들면 바꾸지 않는다.
	if info.State == SchemaOlder {
		if _, err := snapshot(db, schemaBackupDir(path), "before-schema", time.Now()); err != nil {
			db.Close()
			return nil, i18n.Errorf("memory.err.schema_backup", err)
		}
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path, Now: time.Now, Policy: DefaultPolicy()}, nil
}

// MigrateCopy brings a private throwaway copy of a memory DB (a bench copy no other process knows)
// to this binary's schema with the same migrate Open runs, and gives the schema it had before.
// Unlike Open it takes no "before-schema" backup: the rule to back up before a schema migration
// guards the user's DB, and here the user's DB is never opened (the copy was made from its files)
// and the copy is deleted after the run, so a backup of it would only be a second copy of a copy.
// The rule that read operations never migrate (they answer empty on an old DB) is about the
// host's DB as well; a copy is not that DB. A DB a newer orbit-mem made is left alone
// (ErrSchemaNewer), as Open does.
func MigrateCopy(path string) (SchemaInfo, error) {
	db, err := sqlitedb.OpenWith(path, sqlitedb.Options{Busy: writeBusy, Pragmas: storePragmas, Existing: true})
	if err != nil {
		return SchemaInfo{Binary: SchemaVersion}, i18n.Errorf("memory.err.db_open", err)
	}
	defer db.Close()
	info, err := schemaInfo(db)
	if err != nil {
		return info, i18n.Errorf("memory.err.db_open", err)
	}
	switch info.State {
	case SchemaNewer:
		return info, schemaNewerError(info.DB)
	case SchemaOlder:
		return info, migrate(db)
	}
	return info, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for packages that keep their own tables in the same file.
func (s *Store) DB() *sql.DB { return s.db }

// Path is the database file.
func (s *Store) Path() string { return s.path }

func (s *Store) nowMs() int64 { return s.Now().UnixMilli() }

// Day is the local calendar day of t.
func Day(t time.Time) string { return t.Local().Format("2006-01-02") }

// NewID returns a short random id with a prefix.
func NewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// TitleKey is the duplicate key of a title: normalized, spaces collapsed.
func TitleKey(title string) string { return strings.Join(strings.Fields(Normalize(title)), " ") }

// SaveInput is a new memory with its source.
type SaveInput struct {
	Kind     string
	Title    string
	Body     string
	Tags     []string
	Scope    string
	Project  string
	Source   Source
	State    string // 비우면 출처로 정한다(인용이면 확정, 아니면 후보)
	LegacyID string
	Day      string   // 후보가 나온 날. 비우면 오늘
	At       int64    // 만든 시각. 비우면 지금
	Extra    []Source // 같은 트랜잭션에 함께 붙일 출처
	// ConflictsWith marks the candidate as going against these rules of the
	// user before its evidence is checked (A2): it is then not confirmed automatically.
	ConflictsWith []string
	Who           Who // 어긋남 표시를 남기는 쪽
	// Retention is the retention class: volatile, normal ("") or durable (lifecycle.go).
	Retention string
	// TTLDays is how long a volatile memory lasts (1-7, 7 when 0).
	TTLDays int
	// ReviewAfterDays is the review cadence (durable 90 when 0, normal none when 0).
	ReviewAfterDays int
	// Topic groups the values of one thing: recall serves only the newest of a topic, and
	// saving a new value supersedes the current one of the same scope and topic (SaveWith).
	Topic string
	// Supersedes are older memories (any scope) the new one replaces in the same transaction.
	Supersedes []string
	// Tools are the tool names or prefixes (ending in *) right before whose call the memory is recalled.
	Tools []string
	// Pin puts the memory into the core of its layer (설계 · orbit-mem 2판 4.4). A save that cannot get it
	// (not confirmed, not a user or product memory, past the limit) is refused before anything is written.
	Pin bool
	// TrustLimit is the highest trust the sources of this save may get: the channel it came by, as the
	// assembly point says (a person's own command owner, an AI host agent). Empty is agent. A source
	// asks for a lower one with Source.Trust; a quote is always owner (ResolveTrust).
	TrustLimit string
	// Idem makes the save idempotent: a key already applied with the same argument hash returns the memory it
	// recorded without writing; the same key with another hash is ErrIdempotencyConflict (idem.go).
	Idem *Idem
	// Replaces is an older memory the save puts out of date by the rules of SupersedeAs, in the same
	// transaction: the save is refused whole when the writer may not change it (the user's words without a
	// quote) or when the new sources are all untrusted and the old ones are not.
	Replaces string

	noEvidence bool // 저장 뒤 증거 확정을 하지 않는다(승인 적용 중의 저장)
}

func validKind(k string) bool { return contains(Kinds, k) }

func validScope(s string) bool { return place.ValidScope(s) }

// maxTags is how many tags a memory keeps.
const maxTags = 10

func cleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] || utf8.RuneCountInString(t) > 50 {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxTags {
			break
		}
	}
	return out
}

func (in *SaveInput) validate() error {
	in.Title = strings.TrimSpace(in.Title)
	if !validKind(in.Kind) || in.Title == "" || strings.ContainsAny(in.Title, "\r\n") || utf8.RuneCountInString(in.Title) > 200 {
		return i18n.Errorf("memory.err.check_kind_title", ErrInvalid, strings.Join(Kinds, "|"))
	}
	if len(in.Body) > 32<<10 {
		return i18n.Errorf("memory.err.body_size", ErrInvalid)
	}
	if in.Scope == "" {
		in.Scope = ScopeGlobal
	}
	if !validScope(in.Scope) {
		return i18n.Errorf("memory.err.scope_form", ErrInvalid)
	}
	if !contains(SourceKinds, in.Source.Kind) {
		return i18n.Errorf("memory.err.source_kind", ErrInvalid, strings.Join(SourceKinds, "|"))
	}
	if in.State != "" && !contains(States, in.State) {
		return ErrInvalid
	}
	in.Tags = cleanTags(in.Tags)
	tools, err := cleanTools(in.Tools)
	if err != nil {
		return err
	}
	in.Tools = tools
	if err := in.Idem.validate(); err != nil {
		return err
	}
	return in.validateLifecycle()
}

// Save stores a memory with its source. A direct quote of the user is
// confirmed at once; anything else starts as a candidate. Saving the same
// title (same kind and scope) again adds the source and the day. A candidate
// with evidence (seen on different days, followed in verified work, a project
// fact the work's verification checked) is then confirmed automatically and
// the confirmation kept in the ledger (A2). A hidden (rejected) candidate
// stays hidden unless the new source is the user's own quote.
func (s *Store) Save(in SaveInput) (Memory, error) {
	m, _, err := s.SaveWith(in)
	return m, err
}

// SaveReport is what a save did besides storing the memory (설계 · orbit-mem 2판 5절).
type SaveReport struct {
	Superseded []string `json:"superseded,omitempty"` // 이 저장이 대체한 옛 판
	Conflicts  []string `json:"conflicts,omitempty"`  // 대체하지 않고 어긋남으로 둔 사용자 말(또는 믿을 수 없는 새 판이 둔 owner·agent 기억)
	// Unpinned is a pinned memory this save superseded whose core place the new value could not take (past the limit).
	Unpinned string `json:"unpinned,omitempty"`

	// Replayed is the earlier application of the save's idempotency key: nothing was written now.
	Replayed *SaveRequest `json:"-"`

	untrusted []string // Conflicts 가운데 새 판의 출처가 믿을 수 없는 것뿐이라 남긴 것
}

// SaveWith is Save that also supersedes, in the same transaction, the current value of
// the same scope and topic and the memories in in.Supersedes, by the source rules of
// 설계 · orbit-mem 2판 4.2: the user's words replace anything (D1, no userQuote needed:
// the new words are the ground); what an AI learned or an automation wrote does not
// replace the user's words and is marked as going against them instead. A volatile
// memory (the user said it is for now) does not replace its topic's value.
func (s *Store) SaveWith(in SaveInput) (Memory, SaveReport, error) {
	m, rep, err := s.save(in)
	if err != nil || rep.Replayed != nil { // 다시 온 키: 이번에는 아무것도 쓰지 않았다
		return m, rep, err
	}
	for _, id := range rep.Superseded {
		s.closeFlagsOf(id, StateStale)
	}
	// 상시 핵심: 요청한 승격은 저장과 같은 트랜잭션에서 했다. 아니면 대체한 핵심 기억의 자리를 새 판이 이어받는다(S3 명세 2.6).
	if !in.Pin {
		m, rep.Unpinned = s.inheritPin(m, rep.Superseded, in.Who)
	}
	if m.State != StateCandidate {
		return m, rep, nil
	}
	// 기억은 이미 저장됐다: 표시는 할 수 있는 만큼 단다(없는 규칙 id는 부르는 쪽이 먼저 거른다).
	for _, rule := range in.ConflictsWith {
		_, _ = s.FlagConflict(m.ID, rule, "사용자 규칙과 어긋남", in.Who)
	}
	for _, id := range rep.Conflicts {
		note := "같은 주제의 사용자 말과 어긋남(대체하지 않음)"
		if slices.Contains(rep.untrusted, id) {
			note = i18n.TIn(i18n.KO, "memory.conflict.untrusted_topic")
		}
		_, _ = s.FlagConflict(m.ID, id, note, in.Who)
	}
	if in.noEvidence {
		return m, rep, nil
	}
	if _, ok, err := s.ConfirmByEvidence(m.ID); err != nil || !ok {
		return m, rep, nil // 확정하지 못해도 저장은 됐다
	}
	m, err = s.Get(m.ID)
	return m, rep, err
}

func (s *Store) save(in SaveInput) (Memory, SaveReport, error) {
	var rep SaveReport
	if err := in.validate(); err != nil {
		return Memory{}, rep, err
	}
	if err := in.resolveSources(); err != nil {
		return Memory{}, rep, err
	}
	now := s.nowMs()
	at := in.At
	if at == 0 {
		at = now
	}
	day := in.Day
	if day == "" {
		day = Day(s.Now())
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, rep, err
	}
	defer tx.Rollback()
	action := ApplySave
	if in.Replaces != "" {
		action = ApplySupersede
	}
	// 멱등 키: 같은 트랜잭션에서 먼저 본다. 적용된 키면 쓰지 않고 기록한 기억을 지금 상태로 돌려준다.
	if rep.Replayed, err = replayTx(tx, in.Idem, action); err != nil {
		return Memory{}, rep, err
	}
	if rep.Replayed != nil {
		if err := tx.Commit(); err != nil {
			return Memory{}, rep, err
		}
		m, err := s.Get(rep.Replayed.MemoryID)
		return m, rep, err
	}
	for _, id := range append(slices.Clone(in.Supersedes), in.Replaces) { // 없는 기억을 지목하면 아무것도 쓰지 않는다
		if id == "" {
			continue
		}
		if _, err := getTx(tx, id); err != nil {
			return Memory{}, rep, err
		}
	}
	// Replaces는 SupersedeAs의 보호를 거친다: 사람이 아니면 사용자 말 기억은 사용자 말(userQuote)이 있어야 낡게 한다.
	// 새 출처가 사용자 말이면 그 말이 근거다(D1, supersedeTx와 같다).
	if in.Replaces != "" && in.Source.Kind != SourceQuote {
		if err := guardTx(tx, in.Who, now, in.Replaces); err != nil {
			return Memory{}, rep, err
		}
	}

	key := TitleKey(in.Title)
	var existingID string
	if in.LegacyID == "" {
		_ = tx.QueryRow(`SELECT id FROM memories WHERE kind=? AND scope=? AND title_key=? AND state IN ('candidate','confirmed','hidden') ORDER BY created_at LIMIT 1`,
			in.Kind, in.Scope, key).Scan(&existingID)
	}
	if existingID != "" {
		if err := sightTx(tx, existingID, in.Source, day, now); err != nil {
			return Memory{}, rep, err
		}
		cur, err := getTx(tx, existingID)
		if err != nil {
			return Memory{}, rep, err
		}
		if cur.State == StateCandidate || cur.State == StateConfirmed { // 거절된(hidden) 판은 아무것도 대체하지 않는다
			if rep, err = supersedeTx(tx, cur, in, false, now); err != nil {
				return Memory{}, rep, err
			}
		}
		if err := savePinTx(tx, in, existingID, now); err != nil {
			return Memory{}, rep, err
		}
		if err := recordTx(tx, in.Idem, existingID, action, now); err != nil {
			return Memory{}, rep, err
		}
		if err := tx.Commit(); err != nil {
			return Memory{}, rep, err
		}
		m, err := s.Get(existingID)
		return m, rep, err
	}

	state := in.State
	if state == "" {
		state = StateCandidate
		if in.Source.Kind == SourceQuote {
			state = StateConfirmed
		}
	}
	m := Memory{ID: NewID("m-"), Kind: in.Kind, Scope: in.Scope, Project: in.Project, State: state, Title: in.Title,
		Body: in.Body, Tags: in.Tags, Version: 1, CreatedAt: at, UpdatedAt: now, LegacyID: in.LegacyID, Tools: in.Tools}
	in.lifecycle(&m, at)
	var legacy any
	if in.LegacyID != "" {
		legacy = in.LegacyID
	}
	tags, _ := json.Marshal(m.Tags)
	if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at,legacy_id,
		retention,review_days,review_at,expires_at,topic,tools) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Kind, m.Scope, m.Project, m.State, m.Title, key, m.Body, string(tags), 1, m.CreatedAt, m.UpdatedAt, legacy,
		m.Retention, m.ReviewDays, m.ReviewAt, m.ExpiresAt, m.Topic, toolsJSON(m.Tools)); err != nil {
		return Memory{}, rep, err
	}
	if err := addSourceTx(tx, m.ID, in.Source, now); err != nil {
		return Memory{}, rep, err
	}
	for _, src := range in.Extra {
		if !contains(SourceKinds, src.Kind) {
			return Memory{}, rep, ErrInvalid
		}
		if err := addSourceTx(tx, m.ID, src, now); err != nil {
			return Memory{}, rep, err
		}
	}
	if state == StateConfirmed {
		// 출처가 믿을 수 없는 것뿐이면 확정으로 저장하지 않는다(D7, 확정 길 ③). 사용자 말(quote)은 늘 owner다.
		if t, err := memoryTrustTx(tx, m.ID); err != nil {
			return Memory{}, rep, err
		} else if Untrusted(t) {
			return Memory{}, rep, fmt.Errorf("%w: %s", ErrUntrusted, in.Title)
		}
	}
	if state == StateCandidate {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO sightings(memory_id, day) VALUES(?,?)`, m.ID, day); err != nil {
			return Memory{}, rep, err
		}
	}
	if err := versionTx(tx, m, "create", "", now); err != nil {
		return Memory{}, rep, err
	}
	if err := syncIndexTx(tx, m.ID); err != nil { // 색인: 새 기억의 줄과 늦은 줄(쓰기 트랜잭션 끝)
		return Memory{}, rep, err
	}
	if rep, err = supersedeTx(tx, m, in, true, now); err != nil {
		return Memory{}, rep, err
	}
	if err := savePinTx(tx, in, m.ID, now); err != nil {
		return Memory{}, rep, err
	}
	if err := recordTx(tx, in.Idem, m.ID, action, now); err != nil {
		return Memory{}, rep, err
	}
	if err := tx.Commit(); err != nil {
		return Memory{}, rep, err
	}
	m, err = s.Get(m.ID)
	return m, rep, err
}

// savePinTx pins the saved memory in the save's transaction when the save asks for it: a save that cannot
// get the core (not confirmed, not a user or product memory, past the limit) is refused whole (S3 명세 2.6).
// The save is the user's words or an explicit ask, so the memory's own protection is not checked again.
func savePinTx(tx *sql.Tx, in SaveInput, id string, now int64) error {
	if !in.Pin {
		return nil
	}
	m, err := getTx(tx, id)
	if err != nil || m.Pinned {
		return err
	}
	return pinTx(tx, &m, true, in.Who, now)
}

// sightTx records that memory id came up again on day with src. A quote of
// the user confirms it; the evidence of the other days is checked after the
// transaction (ConfirmByEvidence), so the confirmation is in the ledger.
func sightTx(tx *sql.Tx, id string, src Source, day string, now int64) error {
	m, err := getTx(tx, id)
	if err != nil {
		return err
	}
	if err := addSourceTx(tx, m.ID, src, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO sightings(memory_id, day) VALUES(?,?)`, m.ID, day); err != nil {
		return err
	}
	next := m.State
	if src.Kind == SourceQuote && m.State == StateCandidate {
		next = StateConfirmed
	}
	action := "seen"
	if next != m.State {
		m.State = next
		action = "confirm"
	}
	m.Version++
	m.UpdatedAt = now
	return writeTx(tx, m, action, "다시 나옴: "+day, now)
}

// AddSource attaches another source to an existing memory without a new version.
func (s *Store) AddSource(id string, src Source) error {
	if !contains(SourceKinds, src.Kind) {
		return ErrInvalid
	}
	t, err := ResolveTrust("", src.Kind, src.Trust) // 통로를 모르는 붙이기는 agent까지
	if err != nil {
		return err
	}
	src.Trust, src.Excerpt = t, truncateBytes(MaskSecrets(src.Excerpt), 8<<10)
	return insertSourceTx(s.db, NewID("s-"), id, src, s.atOr(src.At))
}

func (s *Store) atOr(at int64) int64 {
	if at != 0 {
		return at
	}
	return s.nowMs()
}

// addSourceTx attaches src to memory id. A source with nothing to point at is left out, unless it carries
// a trust other than its kind's: dropping a lowered one would make the memory read as trusted.
func addSourceTx(tx *sql.Tx, id string, src Source, now int64) error {
	if src.Ref == "" && src.Excerpt == "" && src.Kind != SourceSession && storedTrust(src) == "" {
		return nil
	}
	at := src.At
	if at == 0 {
		at = now
	}
	excerpt := MaskSecrets(src.Excerpt)
	if len(excerpt) > 8<<10 {
		excerpt = truncateBytes(excerpt, 8<<10)
	}
	src.Excerpt = excerpt
	return insertSourceTx(tx, NewID("s-"), id, src, at)
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func writeTx(tx *sql.Tx, m Memory, action, reason string, now int64) error {
	// 상시 핵심은 확정된 user·제품 기억이 한도 안에 있을 때만이다: 옮기거나 늘리거나 후보로 내리면 핵심에서 빠진다(S3).
	// 낡음(대체·낡음 표시)은 핵심 층이 확정만 읽어 빠지고, 표시는 남겨 대체한 새 판이 그 자리를 잇게 한다.
	if m.Pinned && action != "pin" && m.State != StateStale && m.State != StateHidden && !stillPinned(tx, m) {
		m.Pinned = false
	}
	tags, _ := json.Marshal(m.Tags)
	if _, err := tx.Exec(`UPDATE memories SET kind=?,scope=?,project=?,state=?,title=?,title_key=?,body=?,tags=?,version=?,updated_at=?,superseded_by=?,stale_reason=?,
		retention=?,review_days=?,review_at=?,expires_at=?,topic=?,tools=?,pinned=? WHERE id=?`,
		m.Kind, m.Scope, m.Project, m.State, m.Title, TitleKey(m.Title), m.Body, string(tags), m.Version, m.UpdatedAt, m.SupersededBy, m.StaleReason,
		m.Retention, m.ReviewDays, m.ReviewAt, m.ExpiresAt, m.Topic, toolsJSON(m.Tools), m.Pinned, m.ID); err != nil {
		return err
	}
	if err := syncIndexTx(tx, m.ID); err != nil { // 색인: 고친 기억의 줄과 늦은 줄(쓰기 트랜잭션 끝)
		return err
	}
	return versionTx(tx, m, action, reason, now)
}

func versionTx(tx *sql.Tx, m Memory, action, reason string, now int64) error {
	m.Sources, m.Days, m.Links = nil, nil, nil
	snap, _ := json.Marshal(m)
	_, err := tx.Exec(`INSERT INTO memory_versions(memory_id,version,action,reason,at,snapshot) VALUES(?,?,?,?,?,?)`, m.ID, m.Version, action, reason, now, string(snap))
	return err
}

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

const memoryCols = `id,kind,scope,project,state,title,body,tags,version,created_at,updated_at,superseded_by,stale_reason,COALESCE(legacy_id,''),
	retention,review_days,review_at,expires_at,topic,tools,pinned`

func scanMemory(row interface{ Scan(...any) error }) (Memory, error) {
	var m Memory
	var tags, tools string
	if err := row.Scan(&m.ID, &m.Kind, &m.Scope, &m.Project, &m.State, &m.Title, &m.Body, &tags, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.SupersededBy, &m.StaleReason, &m.LegacyID,
		&m.Retention, &m.ReviewDays, &m.ReviewAt, &m.ExpiresAt, &m.Topic, &tools, &m.Pinned); err != nil {
		return m, err
	}
	m.AppliesTo = place.AppliesTo(m.Scope)
	_ = json.Unmarshal([]byte(tools), &m.Tools)
	if len(m.Tools) == 0 {
		m.Tools = nil
	}
	_ = json.Unmarshal([]byte(tags), &m.Tags)
	if m.Tags == nil {
		m.Tags = []string{}
	}
	return m, nil
}

func getTx(q querier, id string) (Memory, error) {
	m, err := scanMemory(q.QueryRow(`SELECT `+memoryCols+` FROM memories WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return m, err
	}
	m.Trust, err = memoryTrustTx(q, id)
	return m, err
}

// Get reads one memory with its sources and sighting days.
func (s *Store) Get(id string) (Memory, error) {
	m, err := getTx(s.db, id)
	if err != nil {
		return m, err
	}
	rows, err := s.db.Query(`SELECT id,kind,ref,excerpt,hash,at,trust FROM sources WHERE memory_id=? ORDER BY at, id`, id)
	if noTrustColumn(err) { // 판 4 사본으로 복구된 DB: 등급은 kind로 읽는다
		rows, err = s.db.Query(`SELECT id,kind,ref,excerpt,hash,at,'' FROM sources WHERE memory_id=? ORDER BY at, id`, id)
	}
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var src Source
		if err := rows.Scan(&src.ID, &src.Kind, &src.Ref, &src.Excerpt, &src.Hash, &src.At, &src.Trust); err != nil {
			return m, err
		}
		m.Sources = append(m.Sources, src)
	}
	drows, err := s.db.Query(`SELECT day FROM sightings WHERE memory_id=? ORDER BY day`, id)
	if err != nil {
		return m, err
	}
	defer drows.Close()
	for drows.Next() {
		var d string
		_ = drows.Scan(&d)
		m.Days = append(m.Days, d)
	}
	if err := rows.Err(); err != nil {
		return m, err
	}
	links, err := s.LinksOf(id)
	if err != nil {
		return m, err
	}
	if len(links) > 0 {
		m.Links = links
	}
	return m, nil
}

// UpdateInput changes a memory. Nil fields stay.
type UpdateInput struct {
	ID              string
	ExpectedVersion int // 0이면 판본을 확인하지 않는다
	Kind            *string
	Title           *string
	Body            *string
	Tags            *[]string
	Tools           *[]string // 도구 트리거를 바꾼다(빈 목록이면 없앤다)
	Scope           *string
	Project         *string
	Topic           *string // 주제를 바꾼다(이전 계획, S4)
	State           *string
	Reason          string
	Action          string
	Who             Who    // 비우면 사람. 사람이 아니면 사용자 인용 기억은 사용자 말이 있어야 고친다
	ExpectState     string // 비우지 않으면 지금 상태가 이것일 때만 고친다(자동 확정이 겹치지 않게)
	Idem            *Idem  // 멱등 키: 같은 키·같은 해시로 다시 오면 쓰지 않고 지금 기억을 돌려준다(idem.go)
	// Sources are new grounds of the change, attached in its transaction. Their trust is set under
	// TrustLimit (the channel's highest, empty is agent) as a save's is.
	Sources    []Source
	TrustLimit string
}

// Update edits a memory and records a new version. A writer other than the
// person changes a memory with the user's words only with the user's words,
// and confirms a candidate only with them (automatic confirmation goes through
// ConfirmByEvidence).
func (s *Store) Update(in UpdateInput) (Memory, error) {
	m, _, err := s.UpdateWith(in)
	return m, err
}

// UpdateWith is Update that also says, for an idempotency key that came before (in.Idem), the earlier
// application: then nothing was written and the memory comes back as it is now. The key is checked
// before anything else (a stale ExpectedVersion of the retried call does not matter) and recorded in
// the update's transaction.
func (s *Store) UpdateWith(in UpdateInput) (Memory, *SaveRequest, error) {
	if err := in.Idem.validate(); err != nil {
		return Memory{}, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, nil, err
	}
	defer tx.Rollback()
	replay, err := replayTx(tx, in.Idem, ApplyUpdate)
	if err != nil {
		return Memory{}, nil, err
	}
	if replay != nil {
		if err := tx.Commit(); err != nil {
			return Memory{}, nil, err
		}
		m, err := s.Get(replay.MemoryID)
		return m, replay, err
	}
	m, err := s.update(tx, in)
	return m, nil, err
}

// update is the body of Update in its transaction tx, which it commits.

func (s *Store) update(tx *sql.Tx, in UpdateInput) (Memory, error) {
	m, err := getTx(tx, in.ID)
	if err != nil {
		return m, err
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != m.Version {
		return m, ErrVersionConflict
	}
	if !in.Who.human() && in.State != nil && *in.State == StateConfirmed && m.State != StateConfirmed && in.Who.quote() == "" {
		return m, fmt.Errorf("%w: %s", ErrConfirmNeedsWords, in.ID)
	}
	// 출처가 믿을 수 없는 것뿐인 기억은 누가 하든 사용자 말 없이 확정하지 않는다(D7, 확정 길 ②·④: 제안 승인도
	// 이 길이다). 사용자 말이 오면 quote(owner) 출처가 붙어 풀린다.
	if in.State != nil && *in.State == StateConfirmed && m.State != StateConfirmed && in.Who.quote() == "" && Untrusted(m.Trust) {
		return m, fmt.Errorf("%w: %s", ErrUntrusted, in.ID)
	}
	if in.ExpectState != "" && m.State != in.ExpectState {
		return m, fmt.Errorf("%w: %s %s", ErrAutoSkip, in.ID, m.State)
	}
	prevState := m.State
	// 고침의 새 출처: 출처가 믿을 수 없는 것뿐인 고침은 사용자 말 없이 owner·agent 기억을 고쳐 쓰지 못한다(D7,
	// 대체와 같은 규칙). 출처는 고침과 같은 트랜잭션에 붙어, 붙이지 못하면 고침도 남지 않는다.
	srcs, srcTrust, err := resolveTrusts(in.TrustLimit, in.Sources)
	if err != nil {
		return m, err
	}
	if len(srcs) > 0 && !mayOverride(srcTrust, m.Trust, in.Who.quote()) {
		return m, fmt.Errorf("%w: %s", ErrUntrusted, in.ID)
	}
	if err := guardTx(tx, in.Who, s.nowMs(), in.ID); err != nil {
		return m, err
	}
	for _, src := range srcs {
		if err := addSourceTx(tx, m.ID, src, s.nowMs()); err != nil {
			return m, err
		}
	}
	if in.Kind != nil {
		if !validKind(*in.Kind) {
			return m, ErrInvalid
		}
		m.Kind = *in.Kind
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" || strings.ContainsAny(t, "\r\n") || utf8.RuneCountInString(t) > 200 {
			return m, ErrInvalid
		}
		m.Title = t
	}
	if in.Body != nil {
		m.Body = *in.Body
	}
	if in.Tags != nil {
		m.Tags = cleanTags(*in.Tags)
	}
	if in.Tools != nil {
		tools, err := cleanTools(*in.Tools)
		if err != nil {
			return m, err
		}
		m.Tools = tools
	}
	if in.Scope != nil {
		if !validScope(*in.Scope) {
			return m, ErrInvalid
		}
		m.Scope = *in.Scope
	}
	if in.Project != nil {
		m.Project = *in.Project
	}
	if in.Topic != nil {
		m.Topic = TopicKey(*in.Topic)
	}
	if in.State != nil {
		if !contains(States, *in.State) {
			return m, ErrInvalid
		}
		m.State = *in.State
		if m.State != StateStale {
			m.SupersededBy, m.StaleReason = "", ""
		} else if in.Reason != "" {
			m.StaleReason = in.Reason
		}
	}
	m.Version++
	m.UpdatedAt = s.nowMs()
	action := in.Action
	if action == "" {
		action = "update"
	}
	if err := writeTx(tx, m, action, in.Reason, m.UpdatedAt); err != nil {
		return m, err
	}
	if err := recordTx(tx, in.Idem, m.ID, ApplyUpdate, m.UpdatedAt); err != nil {
		return m, err
	}
	if err := tx.Commit(); err != nil {
		return m, err
	}
	if m.State != StateCandidate {
		s.closeFlagsOf(m.ID, m.State)
	} else if prevState == StateConfirmed && in.Who.human() {
		// 사람이 확정을 후보로 내렸다: 자동화가 같은 증거로 다시 확정하지 않게 보류로 남긴다(사람이 확정하면 풀린다).
		s.holdConfirm(m.ID, in.Who)
	}
	return s.Get(m.ID)
}

// SetState moves a memory to state with a reason.
func (s *Store) SetState(id, state, reason string) (Memory, error) {
	return s.Update(UpdateInput{ID: id, State: &state, Reason: reason, Action: state})
}

// Supersede marks old as stale, replaced by newID (by the person).
func (s *Store) Supersede(oldID, newID, reason string) (Memory, error) {
	return s.SupersedeAs(oldID, newID, reason, Who{})
}

// SupersedeAs marks old as stale, replaced by newID, by writer w: a writer
// other than the person needs the user's words to replace what the user said.
func (s *Store) SupersedeAs(oldID, newID, reason string, w Who) (Memory, error) {
	if _, err := getTx(s.db, newID); err != nil {
		return Memory{}, err
	}
	if oldID == newID {
		return Memory{}, ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	m, err := getTx(tx, oldID)
	if err != nil {
		return m, err
	}
	if err := guardTx(tx, w, s.nowMs(), oldID); err != nil {
		return m, err
	}
	// 출처가 믿을 수 없는 것뿐인 기억은 사용자 말 없이 owner·agent 기억을 낡게 하지 못한다(D7).
	if newTrust, err := memoryTrustTx(tx, newID); err != nil {
		return m, err
	} else if !mayOverride(newTrust, m.Trust, w.quote()) {
		return m, fmt.Errorf("%w: %s", ErrUntrusted, newID)
	}
	m.State, m.SupersededBy, m.StaleReason = StateStale, newID, reason
	m.Version++
	m.UpdatedAt = s.nowMs()
	if err := writeTx(tx, m, "supersede", reason, m.UpdatedAt); err != nil {
		return m, err
	}
	if err := tx.Commit(); err != nil {
		return m, err
	}
	return s.Get(oldID)
}

// History lists every version of a memory, oldest first.
func (s *Store) History(id string) ([]Version, error) {
	rows, err := s.db.Query(`SELECT version,action,reason,at,snapshot FROM memory_versions WHERE memory_id=? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		var snap string
		if err := rows.Scan(&v.Version, &v.Action, &v.Reason, &v.At, &snap); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(snap), &v.Memory)
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return out, rows.Err()
}

// FindInput selects memories.
type FindInput struct {
	Query   string
	Kinds   []string
	States  []string // 비우면 후보와 확정
	Project string   // "project:<키>": 전역과 이 프로젝트. 비우면 전역만(AllProjects가 아니면)
	// Place, when given, decides what "here" sees by the product map (S3 명세 2.3) instead of Project alone.
	Place       *place.Place
	AllProjects bool
	GlobalOnly  bool
	Tags        []string // 이 태그 중 하나라도 가진 기억만(표기 차이 무시)
	Scope       string   // 비우지 않으면 범위(scope 값)가 이것과 같은 기억만(Project·Place·AllProjects와 무관)
	Limit       int
	// SkipUnknownTokens leaves the query tokens no memory has out of the score (rank.go). The recall of a
	// request and a task sets it; memory_find does not.
	SkipUnknownTokens bool
}

// Hit is a found memory with its score for the query: 0~1 for each query on its own (rank.go), 1 for an
// empty query.
type Hit struct {
	Memory Memory
	Score  float64
}

// Find returns matching memories ranked by score, then confirmed before candidate, then the memories of
// here (not global) before the rest, then newest. An empty query lists them confirmed first, then newest.
func (s *Store) Find(in FindInput) ([]Memory, error) {
	hits, err := s.FindScored(in)
	if err != nil {
		return nil, err
	}
	out := make([]Memory, len(hits))
	for i, h := range hits {
		out[i] = h.Memory
	}
	return out, nil
}

// FindScored is Find with each memory's score. The search index finds the candidates; late memories (marked
// when written without the index, see index.go) are read by scanning, and every candidate gets the same score
// function. When the index's row counts disagree (an index broken without a mark) that find scans the late
// rows of the full check instead. A query reads only the memories it scores; an empty one lists them all.
// All of it is read in one read transaction, so the scores and the memories are of one moment even while
// another connection writes; the transaction ends before filtering and ordering. It only reads, so it works
// on a read-only store.
func (s *Store) FindScored(in FindInput) ([]Hit, error) {
	// ReadOnly는 쓰기 잠금 없이 연다: modernc sqlite는 _txlock=immediate 연결에서도 이때 BEGIN(DEFERRED)을 쓴다. WAL이라
	// 다른 연결의 쓰기를 막지 않고 첫 읽기의 시점을 끝까지 본다. 읽기 전용 열기(mode=ro)에서도 된다.
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	read, err := s.readFind(tx, in)
	// 읽기를 마치면 바로 닫는다: 저장소의 연결은 하나라 트랜잭션을 쥔 동안 이 프로세스의 다른 op가 기다린다.
	if rerr := tx.Rollback(); err == nil {
		err = rerr
	}
	if err != nil {
		return nil, err
	}
	if s.rankStep != nil {
		s.rankStep()
	}
	return rankFind(in, read), nil
}

// findRead is what a find reads in its transaction: the memories in its states that it may return, and
// their scores (nil for an empty query, where every memory scores 1).
type findRead struct {
	all    []Memory
	scores map[string]float64
	empty  bool
}

func (s *Store) readFind(q querier, in FindInput) (findRead, error) {
	states := in.States
	if len(states) == 0 {
		states = []string{StateCandidate, StateConfirmed}
	}
	// 빈 질의(공백뿐)는 색인을 거치지 않고 모두 1점이다. 토큰이 없는 질의(기호뿐)는 아무것도 맞지 않는다.
	r := findRead{empty: strings.TrimSpace(in.Query) == ""}
	var err error
	if r.empty {
		if r.all, err = listTx(q, states); err != nil {
			return r, err
		}
		if s.findStep != nil {
			s.findStep()
		}
		return r, nil
	}
	qs := scoreTokens(in.Query)
	if len(qs) == 0 {
		return r, nil
	}
	if r.scores, err = queryScores(q, qs, in.SkipUnknownTokens, s.findStep); err != nil {
		return r, err
	}
	ids := make([]string, 0, len(r.scores))
	for id, sc := range r.scores {
		if sc > 0 {
			ids = append(ids, id)
		}
	}
	r.all, err = memoriesTx(q, ids, states)
	return r, err
}

// rankFind filters and orders what a find read. It reads nothing.
func rankFind(in FindInput, read findRead) []Hit {
	all, scores, empty := read.all, read.scores, read.empty
	type ranked struct {
		Hit
		key  float64 // 비교용으로 반올림한 점수: 같은 값의 부동소수점 오차가 순위를 가르지 않게
		here bool
	}
	var tags []string
	for _, t := range in.Tags {
		if k := TagKey(t); k != "" {
			tags = append(tags, k)
		}
	}
	var hits []ranked
	for _, m := range all {
		if len(in.Kinds) > 0 && !contains(in.Kinds, m.Kind) {
			continue
		}
		if in.Scope != "" && m.Scope != in.Scope {
			continue
		}
		if len(tags) > 0 && !slices.ContainsFunc(tags, func(k string) bool { return hasTagKey(m.Tags, k) }) {
			continue
		}
		if !in.sees(m) {
			continue
		}
		score := 1.0
		if !empty {
			if score = scores[m.ID]; score <= 0 {
				continue
			}
		}
		hits = append(hits, ranked{Hit{m, score}, math.Round(score * 1e9), !empty && in.here(m)})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.key != b.key {
			return a.key > b.key
		}
		// 후보는 회상에 쓰되 확정보다 뒤에 둔다(A2).
		if ca, cb := a.Memory.State == StateConfirmed, b.Memory.State == StateConfirmed; ca != cb {
			return ca
		}
		if a.here != b.here {
			return a.here
		}
		if a.Memory.UpdatedAt != b.Memory.UpdatedAt {
			return a.Memory.UpdatedAt > b.Memory.UpdatedAt
		}
		return a.Memory.ID < b.Memory.ID
	})
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	var out []Hit
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].Hit)
	}
	return out
}

// sees says whether the find's place sees m. A given Scope alone decides (FindScored checks it).
func (in FindInput) sees(m Memory) bool {
	switch {
	case in.AllProjects, in.Scope != "":
		return true
	case in.GlobalOnly || (in.Project == "" && in.Place == nil):
		return m.Scope == ScopeGlobal
	case in.Place != nil:
		return in.Place.Sees(m.Scope, m.Project)
	}
	return (place.Place{Scope: in.Project}).Sees(m.Scope, m.Project)
}

// here says whether m belongs to the find's place itself (its product, repository or folder), not global.
func (in FindInput) here(m Memory) bool {
	switch {
	case m.Scope == ScopeGlobal:
		return false
	case in.Place != nil:
		return in.Place.Sees(m.Scope, m.Project)
	case in.Project != "":
		return (place.Place{Scope: in.Project}).Sees(m.Scope, m.Project)
	}
	return false
}

// List returns memories in the given states (all states when empty), newest first.
func (s *Store) List(states ...string) ([]Memory, error) {
	if len(states) == 0 {
		states = States
	}
	return s.list(states)
}

func (s *Store) list(states []string) ([]Memory, error) { return listTx(s.db, states) }

func listTx(q querier, states []string) ([]Memory, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
	args := make([]any, len(states))
	for i, v := range states {
		args[i] = v
	}
	rows, err := q.Query(`SELECT `+memoryCols+` FROM memories WHERE state IN (`+ph+`) ORDER BY updated_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, fillTrust(q, out, `memory_id IN (SELECT id FROM memories WHERE state IN (`+ph+`))`, args...)
}

// memoriesTx reads the memories ids that are in states, in no order. It looks each one up by its key (the
// ids go in as one JSON array, so there is no limit on how many), so it reads those memories only.
func memoriesTx(q querier, ids, states []string) ([]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
	args := []any{string(list)}
	for _, v := range states {
		args = append(args, v)
	}
	rows, err := q.Query(`SELECT `+memoryCols+` FROM memories WHERE id IN (SELECT value FROM json_each(?)) AND state IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, fillTrust(q, out, `memory_id IN (SELECT value FROM json_each(?))`, string(list))
}

// Count is the number of memories.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
}

// StateCounts is how many memories there are in each state.
func (s *Store) StateCounts() (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT state, COUNT(*) FROM memories GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Meta reads a stored setting of the database.
func (s *Store) Meta(key string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}

// SetMeta writes a stored setting of the database.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

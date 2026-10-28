package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit/internal/sqlitedb"
)

// Store is this PC's memory database (one SQLite file).
type Store struct {
	db   *sql.DB
	path string
	Now  func() time.Time
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
PRAGMA user_version=2;
`

// addColumns adds the columns later versions put on tables an older Orbit made.
func addColumns(db *sql.Tx) error {
	for _, c := range []struct{ table, column, def string }{
		{"retros", "session", "TEXT NOT NULL DEFAULT ''"},
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
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS retros_session ON retros(session) WHERE session <> ''`)
	return err
}

// migrate creates and upgrades the tables in one immediate transaction. Taking the
// write lock first lets the busy timeout apply when several processes open a fresh
// or old DB together; a statement that reads first and then writes would get
// SQLITE_BUSY at once instead of waiting.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("기억 DB를 열 수 없습니다: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema + eventsSchema); err != nil {
		return fmt.Errorf("기억 DB를 열 수 없습니다: %w", err)
	}
	if err := addColumns(tx); err != nil {
		return fmt.Errorf("기억 DB를 새 판으로 올리지 못했습니다: %w", err)
	}
	return tx.Commit()
}

// Open opens (creating if needed) the memory database at path. The directory is private to the user.
func Open(path string) (*Store, error) {
	db, err := sqlitedb.Open(path, 5*time.Second, "foreign_keys(1)", "synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("기억 DB를 열 수 없습니다: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path, Now: time.Now}, nil
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
}

func validKind(k string) bool { return contains(Kinds, k) }

func validScope(s string) bool {
	return s == ScopeGlobal || strings.HasPrefix(s, "project:") && len(s) > len("project:")
}

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
		if len(out) == 10 {
			break
		}
	}
	return out
}

func (in *SaveInput) validate() error {
	in.Title = strings.TrimSpace(in.Title)
	if !validKind(in.Kind) || in.Title == "" || strings.ContainsAny(in.Title, "\r\n") || utf8.RuneCountInString(in.Title) > 200 {
		return fmt.Errorf("%w: 종류(%s)·제목(한 줄, 200자 이하)을 확인해 주세요", ErrInvalid, strings.Join(Kinds, "|"))
	}
	if len(in.Body) > 32<<10 {
		return fmt.Errorf("%w: 본문은 32KB 이하로 써 주세요", ErrInvalid)
	}
	if in.Scope == "" {
		in.Scope = ScopeGlobal
	}
	if !validScope(in.Scope) {
		return fmt.Errorf("%w: 범위는 global 또는 project:<키>", ErrInvalid)
	}
	if !contains(SourceKinds, in.Source.Kind) {
		return fmt.Errorf("%w: 출처 종류는 %s 중 하나입니다", ErrInvalid, strings.Join(SourceKinds, "|"))
	}
	if in.State != "" && !contains(States, in.State) {
		return ErrInvalid
	}
	in.Tags = cleanTags(in.Tags)
	return nil
}

// Save stores a memory with its source. A direct quote of the user is
// confirmed at once; anything else starts as a candidate. Saving the same
// title (same kind and scope) again adds the source and the day: a candidate
// seen on two different days is confirmed. A hidden (rejected) candidate stays
// hidden unless the new source is the user's own quote.
func (s *Store) Save(in SaveInput) (Memory, error) {
	if err := in.validate(); err != nil {
		return Memory{}, err
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
		return Memory{}, err
	}
	defer tx.Rollback()

	key := TitleKey(in.Title)
	var existingID string
	if in.LegacyID == "" {
		_ = tx.QueryRow(`SELECT id FROM memories WHERE kind=? AND scope=? AND title_key=? AND state IN ('candidate','confirmed','hidden') ORDER BY created_at LIMIT 1`,
			in.Kind, in.Scope, key).Scan(&existingID)
	}
	if existingID != "" {
		if err := sightTx(tx, existingID, in.Source, day, now); err != nil {
			return Memory{}, err
		}
		if err := tx.Commit(); err != nil {
			return Memory{}, err
		}
		return s.Get(existingID)
	}

	state := in.State
	if state == "" {
		state = StateCandidate
		if in.Source.Kind == SourceQuote {
			state = StateConfirmed
		}
	}
	m := Memory{ID: NewID("m-"), Kind: in.Kind, Scope: in.Scope, Project: in.Project, State: state, Title: in.Title,
		Body: in.Body, Tags: in.Tags, Version: 1, CreatedAt: at, UpdatedAt: now, LegacyID: in.LegacyID}
	var legacy any
	if in.LegacyID != "" {
		legacy = in.LegacyID
	}
	tags, _ := json.Marshal(m.Tags)
	if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at,legacy_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Kind, m.Scope, m.Project, m.State, m.Title, key, m.Body, string(tags), 1, m.CreatedAt, m.UpdatedAt, legacy); err != nil {
		return Memory{}, err
	}
	if err := addSourceTx(tx, m.ID, in.Source, now); err != nil {
		return Memory{}, err
	}
	for _, src := range in.Extra {
		if !contains(SourceKinds, src.Kind) {
			return Memory{}, ErrInvalid
		}
		if err := addSourceTx(tx, m.ID, src, now); err != nil {
			return Memory{}, err
		}
	}
	if state == StateCandidate {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO sightings(memory_id, day) VALUES(?,?)`, m.ID, day); err != nil {
			return Memory{}, err
		}
	}
	if err := versionTx(tx, m, "create", "", now); err != nil {
		return Memory{}, err
	}
	if err := tx.Commit(); err != nil {
		return Memory{}, err
	}
	return s.Get(m.ID)
}

// sightTx records that memory id came up again on day with src. A quote of
// the user confirms it; a candidate seen on two different days is confirmed.
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
	switch {
	case src.Kind == SourceQuote && m.State == StateCandidate:
		next = StateConfirmed
	case m.State == StateCandidate:
		var days int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sightings WHERE memory_id=?`, m.ID).Scan(&days); err != nil {
			return err
		}
		if days >= 2 {
			next = StateConfirmed
		}
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

// Seen records that memory id came up again on day (YYYY-MM-DD) with src.
func (s *Store) Seen(id string, src Source, day string) (Memory, error) {
	if !contains(SourceKinds, src.Kind) {
		return Memory{}, ErrInvalid
	}
	if day == "" {
		day = Day(s.Now())
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if err := sightTx(tx, id, src, day, s.nowMs()); err != nil {
		return Memory{}, err
	}
	if err := tx.Commit(); err != nil {
		return Memory{}, err
	}
	return s.Get(id)
}

// AddSource attaches another source to an existing memory without a new version.
func (s *Store) AddSource(id string, src Source) error {
	if !contains(SourceKinds, src.Kind) {
		return ErrInvalid
	}
	_, err := s.db.Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at) VALUES(?,?,?,?,?,?,?)`,
		NewID("s-"), id, src.Kind, src.Ref, truncateBytes(MaskSecrets(src.Excerpt), 8<<10), src.Hash, s.atOr(src.At))
	return err
}

func (s *Store) atOr(at int64) int64 {
	if at != 0 {
		return at
	}
	return s.nowMs()
}

func addSourceTx(tx *sql.Tx, id string, src Source, now int64) error {
	if src.Ref == "" && src.Excerpt == "" && src.Kind != SourceSession {
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
	_, err := tx.Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at) VALUES(?,?,?,?,?,?,?)`,
		NewID("s-"), id, src.Kind, src.Ref, excerpt, src.Hash, at)
	return err
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
	tags, _ := json.Marshal(m.Tags)
	if _, err := tx.Exec(`UPDATE memories SET kind=?,scope=?,project=?,state=?,title=?,title_key=?,body=?,tags=?,version=?,updated_at=?,superseded_by=?,stale_reason=? WHERE id=?`,
		m.Kind, m.Scope, m.Project, m.State, m.Title, TitleKey(m.Title), m.Body, string(tags), m.Version, m.UpdatedAt, m.SupersededBy, m.StaleReason, m.ID); err != nil {
		return err
	}
	return versionTx(tx, m, action, reason, now)
}

func versionTx(tx *sql.Tx, m Memory, action, reason string, now int64) error {
	m.Sources, m.Days = nil, nil
	snap, _ := json.Marshal(m)
	_, err := tx.Exec(`INSERT INTO memory_versions(memory_id,version,action,reason,at,snapshot) VALUES(?,?,?,?,?,?)`, m.ID, m.Version, action, reason, now, string(snap))
	return err
}

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

const memoryCols = `id,kind,scope,project,state,title,body,tags,version,created_at,updated_at,superseded_by,stale_reason,COALESCE(legacy_id,'')`

func scanMemory(row interface{ Scan(...any) error }) (Memory, error) {
	var m Memory
	var tags string
	if err := row.Scan(&m.ID, &m.Kind, &m.Scope, &m.Project, &m.State, &m.Title, &m.Body, &tags, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.SupersededBy, &m.StaleReason, &m.LegacyID); err != nil {
		return m, err
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
	return m, err
}

// Get reads one memory with its sources and sighting days.
func (s *Store) Get(id string) (Memory, error) {
	m, err := getTx(s.db, id)
	if err != nil {
		return m, err
	}
	rows, err := s.db.Query(`SELECT id,kind,ref,excerpt,hash,at FROM sources WHERE memory_id=? ORDER BY at, id`, id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var src Source
		if err := rows.Scan(&src.ID, &src.Kind, &src.Ref, &src.Excerpt, &src.Hash, &src.At); err != nil {
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
	return m, rows.Err()
}

// UpdateInput changes a memory. Nil fields stay.
type UpdateInput struct {
	ID              string
	ExpectedVersion int // 0이면 판본을 확인하지 않는다
	Kind            *string
	Title           *string
	Body            *string
	Tags            *[]string
	Scope           *string
	Project         *string
	State           *string
	Reason          string
	Action          string
}

// Update edits a memory and records a new version.
func (s *Store) Update(in UpdateInput) (Memory, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	m, err := getTx(tx, in.ID)
	if err != nil {
		return m, err
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != m.Version {
		return m, ErrVersionConflict
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
	if in.Scope != nil {
		if !validScope(*in.Scope) {
			return m, ErrInvalid
		}
		m.Scope = *in.Scope
	}
	if in.Project != nil {
		m.Project = *in.Project
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
	if err := tx.Commit(); err != nil {
		return m, err
	}
	return s.Get(m.ID)
}

// SetState moves a memory to state with a reason.
func (s *Store) SetState(id, state, reason string) (Memory, error) {
	return s.Update(UpdateInput{ID: id, State: &state, Reason: reason, Action: state})
}

// Supersede marks old as stale, replaced by newID.
func (s *Store) Supersede(oldID, newID, reason string) (Memory, error) {
	if _, err := getTx(s.db, newID); err != nil {
		return Memory{}, err
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
	Query       string
	Kinds       []string
	States      []string // 비우면 후보와 확정
	Project     string   // "project:<키>": 전역과 이 프로젝트. 비우면 전역만(AllProjects가 아니면)
	AllProjects bool
	GlobalOnly  bool
	Limit       int
}

// Find returns matching memories ranked by relevance, then recency.
func (s *Store) Find(in FindInput) ([]Memory, error) {
	states := in.States
	if len(states) == 0 {
		states = []string{StateCandidate, StateConfirmed}
	}
	all, err := s.list(states)
	if err != nil {
		return nil, err
	}
	type ranked struct {
		m Memory
		r Rank
	}
	var hits []ranked
	for _, m := range all {
		if len(in.Kinds) > 0 && !contains(in.Kinds, m.Kind) {
			continue
		}
		switch {
		case in.AllProjects:
		case in.GlobalOnly || in.Project == "":
			if m.Scope != ScopeGlobal {
				continue
			}
		default:
			if m.Scope != ScopeGlobal && m.Scope != in.Project {
				continue
			}
		}
		r := Relevance(m.Doc(), in.Query)
		if r.Score <= 0 {
			continue
		}
		hits = append(hits, ranked{m, r})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.r.Score != b.r.Score {
			return a.r.Score > b.r.Score
		}
		if a.r.Title != b.r.Title {
			return a.r.Title > b.r.Title
		}
		if a.m.UpdatedAt != b.m.UpdatedAt {
			return a.m.UpdatedAt > b.m.UpdatedAt
		}
		return a.m.ID < b.m.ID
	})
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	var out []Memory
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].m)
	}
	return out, nil
}

// List returns memories in the given states (all states when empty), newest first.
func (s *Store) List(states ...string) ([]Memory, error) {
	if len(states) == 0 {
		states = States
	}
	return s.list(states)
}

func (s *Store) list(states []string) ([]Memory, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
	args := make([]any, len(states))
	for i, v := range states {
		args[i] = v
	}
	rows, err := s.db.Query(`SELECT `+memoryCols+` FROM memories WHERE state IN (`+ph+`) ORDER BY updated_at DESC, id`, args...)
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
	return out, rows.Err()
}

// Count is the number of memories.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
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

// Backup writes a consistent copy into dir (memory-YYYYMMDD.sqlite, one per day)
// and keeps the newest keep copies.
func (s *Store) Backup(dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "memory-" + s.Now().Local().Format("20060102") + ".sqlite"
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return "", fmt.Errorf("백업 실패: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dst, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "memory-") && strings.HasSuffix(e.Name(), ".sqlite") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
	return dst, nil
}

// Snapshot writes a named copy (<name>-YYYYMMDDTHHMMSS.sqlite) into dir that
// daily backup rotation never removes, e.g. before a migration.
func (s *Store) Snapshot(dir, name string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name+"-"+s.Now().Local().Format("20060102T150405")+".sqlite")
	if _, err := s.db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return "", fmt.Errorf("사본 실패: %w", err)
	}
	return dst, os.Chmod(dst, 0o600)
}

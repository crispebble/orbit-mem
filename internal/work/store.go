// Package work keeps this PC's work graph: an append-only ledger of observed
// host events and the current graph tables (works, steps, edges, runs,
// evidence, sessions) rebuilt from it. Hooks write; orb mcp reads and serves
// the orbit.work contract. Nothing here talks to a network.
package work

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/sqlitedb"
)

// Store is the work database (work.db).
type Store struct {
	db    *sql.DB
	path  string
	dbID  string
	Now   func() time.Time
	Spool string // 잠겨 있을 때 사건을 남기는 디렉터리. 비우면 쓰지 않는다
}

const schemaVersion = 3

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ledger (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, host TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, key TEXT, cwd TEXT NOT NULL DEFAULT '', data TEXT NOT NULL DEFAULT '{}', derived INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS ledger_key ON ledger(key) WHERE key IS NOT NULL;
CREATE TABLE IF NOT EXISTS sessions (
  host TEXT NOT NULL, id TEXT NOT NULL, surface TEXT NOT NULL DEFAULT 'unknown', cwd TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '', project_scope TEXT NOT NULL DEFAULT '', project_label TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '', transcript TEXT NOT NULL DEFAULT '', list_id TEXT NOT NULL DEFAULT '',
  parent_host TEXT NOT NULL DEFAULT '', parent_session TEXT NOT NULL DEFAULT '', parent_work TEXT NOT NULL DEFAULT '', parent_step TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL, last_seen INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0,
  end_reason TEXT NOT NULL DEFAULT '', end_detail TEXT NOT NULL DEFAULT '',
  turn TEXT NOT NULL DEFAULT '', turn_open INTEGER NOT NULL DEFAULT 0, updated_seq INTEGER NOT NULL DEFAULT 0,
  launcher TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(host, id));
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated_seq);
CREATE TABLE IF NOT EXISTS ended_turns (host TEXT NOT NULL, session TEXT NOT NULL, prompt TEXT NOT NULL, PRIMARY KEY(host, session, prompt));
CREATE TABLE IF NOT EXISTS waits (
  host TEXT NOT NULL, session TEXT NOT NULL, key TEXT NOT NULL, prompt TEXT NOT NULL DEFAULT '', tool TEXT NOT NULL DEFAULT '',
  opened_at INTEGER NOT NULL DEFAULT 0, resolved INTEGER NOT NULL DEFAULT 0, resolved_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(host, session, key));
CREATE TABLE IF NOT EXISTS lists (host TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, open_work TEXT NOT NULL DEFAULT '', last_work TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(host, kind, id));
CREATE TABLE IF NOT EXISTS works (
  id TEXT PRIMARY KEY, host TEXT NOT NULL, session TEXT NOT NULL, surface TEXT NOT NULL DEFAULT 'unknown',
  list_kind TEXT NOT NULL, list_id TEXT NOT NULL, cwd TEXT NOT NULL DEFAULT '', project_scope TEXT NOT NULL DEFAULT '', project_label TEXT NOT NULL DEFAULT '',
  parent_work TEXT NOT NULL DEFAULT '', parent_step TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '', title_at INTEGER NOT NULL DEFAULT 0,
  opened_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0, updated_seq INTEGER NOT NULL DEFAULT 0,
  launcher TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS works_updated ON works(updated_seq);
CREATE INDEX IF NOT EXISTS works_parent ON works(parent_step) WHERE parent_step <> '';
CREATE TABLE IF NOT EXISTS steps (
  id TEXT PRIMARY KEY, work_id TEXT NOT NULL, native TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', active_title TEXT NOT NULL DEFAULT '',
  ord INTEGER NOT NULL DEFAULT 0, listed INTEGER NOT NULL DEFAULT 1, host_status TEXT NOT NULL DEFAULT 'pending', owner TEXT NOT NULL DEFAULT '',
  declared TEXT NOT NULL DEFAULT '', declared_at INTEGER NOT NULL DEFAULT 0, interrupted_at INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0,
  last_host TEXT NOT NULL DEFAULT '', last_session TEXT NOT NULL DEFAULT '', created_seq INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS steps_work ON steps(work_id);
CREATE INDEX IF NOT EXISTS steps_session ON steps(last_host, last_session);
CREATE TABLE IF NOT EXISTS natives (host TEXT NOT NULL, list TEXT NOT NULL, native TEXT NOT NULL, step_id TEXT NOT NULL, PRIMARY KEY(host, list, native));
CREATE TABLE IF NOT EXISTS edges (step_id TEXT NOT NULL, after_id TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY(step_id, after_id));
CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, host TEXT NOT NULL, session TEXT NOT NULL, run_host TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
  child_session TEXT NOT NULL DEFAULT '', parent_session TEXT NOT NULL DEFAULT '', step_id TEXT NOT NULL DEFAULT '', link TEXT NOT NULL DEFAULT 'none',
  background INTEGER NOT NULL DEFAULT 0, cwd TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'running', end_reason TEXT NOT NULL DEFAULT '', tool_use TEXT NOT NULL DEFAULT '', transcript TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0, last_seen INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0, seq INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS runs_session ON runs(host, session);
CREATE INDEX IF NOT EXISTS runs_step ON runs(step_id) WHERE step_id <> '';
CREATE TABLE IF NOT EXISTS evidence (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, host TEXT NOT NULL, session TEXT NOT NULL, step_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, seq INTEGER NOT NULL, result TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '', sha TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '', files TEXT NOT NULL DEFAULT '[]');
CREATE INDEX IF NOT EXISTS evidence_step ON evidence(step_id) WHERE step_id <> '';
CREATE INDEX IF NOT EXISTS evidence_session ON evidence(host, session);
CREATE TABLE IF NOT EXISTS timeline (
  seq INTEGER NOT NULL, sub INTEGER NOT NULL, host TEXT NOT NULL, session TEXT NOT NULL, at INTEGER NOT NULL, kind TEXT NOT NULL,
  work_id TEXT NOT NULL DEFAULT '', step_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '', agent_transcript TEXT NOT NULL DEFAULT '', PRIMARY KEY(seq, sub));
CREATE INDEX IF NOT EXISTS timeline_session ON timeline(host, session, seq, sub);
CREATE TABLE IF NOT EXISTS changes (seq INTEGER NOT NULL, work_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  decision_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS decisions (
  id TEXT PRIMARY KEY, state TEXT NOT NULL DEFAULT 'open', source TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '', work_id TEXT NOT NULL DEFAULT '', step_id TEXT NOT NULL DEFAULT '', wait_key TEXT NOT NULL DEFAULT '',
  binding_kind TEXT NOT NULL DEFAULT '', binding_ref TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '{}',
  answer TEXT NOT NULL DEFAULT '', answered_by TEXT NOT NULL DEFAULT '', close_reason TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0, closed_at INTEGER NOT NULL DEFAULT 0, expires_at INTEGER NOT NULL DEFAULT 0,
  updated_seq INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS decisions_session ON decisions(host, session);
CREATE INDEX IF NOT EXISTS decisions_created ON decisions(created_at);
CREATE INDEX IF NOT EXISTS changes_seq ON changes(seq);
`

// graphTables are rebuilt from the ledger.
var graphTables = []string{"sessions", "ended_turns", "waits", "lists", "works", "steps", "natives", "edges", "runs", "evidence", "timeline", "changes", "decisions"}

// BusyTimeout is how long a writer waits for another writer before giving up.
// A hook that gives up spools the event (설계 「실패·중단·재개」).
const BusyTimeout = 2 * time.Second

// Open opens (creating if needed) the work database at path.
func Open(path string) (*Store, error) {
	return open(path, BusyTimeout)
}

func open(path string, busy time.Duration) (*Store, error) {
	// 한 프로세스에 연결 하나(sqlitedb): data_version은 연결마다 따로이고, 쓰기 대기는 프로세스 사이에서만 한다.
	db, err := sqlitedb.Open(path, busy, "synchronous(NORMAL)")
	if err != nil {
		return nil, i18n.Errorf("work.err.db_open", err)
	}
	s := &Store{db: db, path: path, Now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	// 이미 이 판이면 읽기만 한다: 읽는 쪽(orb mcp --reader, 재개 문맥, doctor)이 쓰기 잠금을 다투지 않게.
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return i18n.Errorf("work.err.db_open", err)
	}
	if v > schemaVersion {
		return i18n.Errorf("work.err.db_newer", v)
	}
	if v == schemaVersion && s.db.QueryRow(`SELECT value FROM meta WHERE key='db_id'`).Scan(&s.dbID) == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return i18n.Errorf("work.err.db_open", err)
	}
	defer tx.Rollback()
	// 다른 프로세스가 먼저 올렸을 수 있어 잠금을 잡은 뒤 다시 본다
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	old := v > 0 && v < schemaVersion
	if old {
		// 그래프 표는 원장에서 다시 만든다: 옛 판의 표를 지우고 새 모양으로 만든 뒤 다시 적용한다
		for _, t := range graphTables {
			if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(schema); err != nil {
		return i18n.Errorf("work.err.db_create", err)
	}
	if err := tx.QueryRow(`SELECT value FROM meta WHERE key='db_id'`).Scan(&s.dbID); errors.Is(err, sql.ErrNoRows) {
		s.dbID = newDBID()
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('db_id',?)`, s.dbID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if old {
		if err := rebuild(tx); err != nil {
			return i18n.Errorf("work.err.db_upgrade", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func newDBID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path is the database file.
func (s *Store) Path() string { return s.path }

// ID is the database identity carried in cursors. It changes when the
// database is recreated or restored from a backup (backup copies get a new one).
func (s *Store) ID() string { return s.dbID }

func (s *Store) nowMs() int64 { return s.Now().UnixMilli() }

// LastSeq is the ledger position of the last event.
func (s *Store) LastSeq() (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&n)
	return n.Int64, err
}

// DataVersion changes when another connection commits (SQLite PRAGMA data_version).
func (s *Store) DataVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v)
	return v, err
}

// Result says what an applied event changed. Opened lists works this event opened.
type Result struct {
	Seq     int64
	Skipped bool // 같은 멱등 키가 이미 있었다
	Opened  []string
}

// Append records the event and applies it to the graph in one transaction.
func (s *Store) Append(e Event) (Result, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	res, err := s.appendTx(tx, e)
	if err != nil || res.Skipped {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return res, nil
}

// appendTx records and applies the event inside tx.
func (s *Store) appendTx(tx *sql.Tx, e Event) (Result, error) {
	if e.At == 0 {
		e.At = s.nowMs()
	}
	var key any
	if e.Key != "" {
		key = e.Key
	}
	data := string(e.Data)
	if data == "" {
		data = "{}"
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO ledger(kind,host,session,at,key,cwd,data) VALUES(?,?,?,?,?,?,?)`,
		e.Kind, e.Host, e.Session, e.At, key, e.Cwd, data)
	if err != nil {
		return Result{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Result{Skipped: true}, nil
	}
	e.Seq, _ = res.LastInsertId()
	r := newReducer(tx, e, false)
	if err := r.apply(); err != nil {
		return Result{}, i18n.Errorf("work.err.apply_event", e.Kind, err)
	}
	if err := r.flush(); err != nil {
		return Result{}, err
	}
	return Result{Seq: e.Seq, Opened: r.opened}, nil
}

// Rebuild clears the graph tables and replays the ledger. Positions (seq)
// and ids stay the same, so readers' cursors still point at the same events.
func (s *Store) Rebuild() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rebuild(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func rebuild(tx *sql.Tx) error {
	for _, t := range graphTables {
		if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT seq,kind,host,session,at,COALESCE(key,''),cwd,data FROM ledger WHERE derived=0 ORDER BY seq`)
	if err != nil {
		return err
	}
	var evs []Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.Seq, &e.Kind, &e.Host, &e.Session, &e.At, &e.Key, &e.Cwd, &data); err != nil {
			rows.Close()
			return err
		}
		e.Data = []byte(data)
		evs = append(evs, e)
	}
	rows.Close()
	for _, e := range evs {
		r := newReducer(tx, e, true)
		if err := r.apply(); err != nil {
			return i18n.Errorf("work.err.reapply_event", e.Seq, e.Kind, err)
		}
		if err := r.flush(); err != nil {
			return err
		}
	}
	return nil
}

// hashID makes a stable id from parts, so a rebuild gives the same ids.
func hashID(prefix string, parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprint(h, p, "\x00")
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:12]
}

// Backup writes a consistent copy to dst. The copy gets a new identity, so a
// reader that kept a cursor from this database gets reset after a restore
// (설계 「변경 알림」).
func (s *Store) Backup(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if _, err := s.db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return err
	}
	c, err := sql.Open("sqlite", sqlitedb.FileURI(dst))
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Exec(`UPDATE meta SET value=? WHERE key='db_id'`, newDBID()); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}

// DailyBackup copies the work database into dir as work-YYYYMMDD.sqlite and
// keeps the newest keep copies. A missing database is skipped.
func DailyBackup(dbPath, dir string, keep int, now time.Time) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", nil
	}
	s, err := Open(dbPath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	dst := filepath.Join(dir, "work-"+now.Local().Format("20060102")+".sqlite")
	if err := s.Backup(dst); err != nil {
		return "", i18n.Errorf("work.err.backup", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dst, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "work-") && strings.HasSuffix(e.Name(), ".sqlite") {
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

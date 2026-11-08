package memory

import (
	"database/sql"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// 읽기 열기와 판 맞추기(설계 10.2, ORBIT-23 G1). 이전은 쓰기 열기(Open)에서만 한다. 읽기 op는
// OpenRead·OpenReadOnly로 이전·사본 없이 열고, DB 판이 실행 파일과 맞는지(SchemaInfo)를 보고 정한다.
// 판의 정본은 meta 표의 schema.version이다: 옛 실행 파일(0.16 이하)은 열 때마다 user_version을
// 자기 판으로 다시 쓰므로 user_version은 meta 키가 없는 DB에서만 판으로 본다.

// metaSchemaVersion is the meta key holding the DB's schema version (a decimal integer).
const metaSchemaVersion = "schema.version"

const (
	writeBusy = 5 * time.Second
	// readBusy bounds how long a write inside a read operation (shown marks, events)
	// waits for another process's lock; the read operation goes on without it.
	readBusy = 300 * time.Millisecond
)

var storePragmas = []string{"foreign_keys(1)", "synchronous(NORMAL)"}

// SchemaState is how the DB's schema compares with this binary's.
type SchemaState string

const (
	SchemaOK      SchemaState = "ok"      // 판과 꼴이 실행 파일과 같다
	SchemaNewer   SchemaState = "newer"   // DB가 실행 파일보다 새 판이다(실행 파일을 올려야 한다)
	SchemaOlder   SchemaState = "older"   // 판이 낮거나 표·색인·열이 빠졌다(이전이 필요하다)
	SchemaEmpty   SchemaState = "empty"   // 파일은 있고 표가 없다
	SchemaMissing SchemaState = "missing" // 파일이 없다
)

// SchemaInfo is the DB's schema version against this binary's. DB is the meta
// schema.version, or user_version when the key is missing; 0 when the file is
// missing or has no tables.
type SchemaInfo struct {
	Binary int         `json:"binary"`
	DB     int         `json:"db"`
	State  SchemaState `json:"state"`
}

// ErrSchemaNewer is the error of a write open of a DB a newer orbit-mem made: this
// binary does not migrate it, so it never undoes the newer schema.
var ErrSchemaNewer = i18n.Err("memory.err.schema_newer")

// OpenRead opens an existing memory DB for a read operation (context, recall, tool
// context, find, get). It never creates, migrates or copies the file, and its writes
// (shown marks, events, activity) wait readBusy for another process's lock and then
// fail; callers drop such failures. The schema state comes back with the store and
// the caller decides what a DB that is not SchemaOK gives (a read operation answers
// empty). A missing file is an error that errors.Is fs.ErrNotExist, with state
// SchemaMissing and no store; on any other error the state is "" (unknown).
func OpenRead(path string) (*Store, SchemaInfo, error) {
	return openKept(path, sqlitedb.Options{Busy: readBusy, Pragmas: storePragmas, Existing: true})
}

// OpenReadOnly opens an existing memory DB read only (doctor, version, copies): every
// write fails. Otherwise it is OpenRead.
func OpenReadOnly(path string) (*Store, SchemaInfo, error) {
	return openKept(path, sqlitedb.Options{Busy: readBusy, Pragmas: storePragmas, ReadOnly: true})
}

func openKept(path string, o sqlitedb.Options) (*Store, SchemaInfo, error) {
	info := SchemaInfo{Binary: SchemaVersion}
	db, err := sqlitedb.OpenWith(path, o)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			info.State = SchemaMissing
		}
		return nil, info, i18n.Errorf("memory.err.db_open", err)
	}
	if info, err = schemaInfo(db); err != nil {
		db.Close()
		return nil, info, i18n.Errorf("memory.err.db_open", err)
	}
	return &Store{db: db, path: path, Now: time.Now, Policy: DefaultPolicy()}, info, nil
}

// Schema reads the DB's schema state now.
func (s *Store) Schema() (SchemaInfo, error) { return schemaInfo(s.db) }

// schemaInfo compares db with the shape migrate gives. Only reads.
func schemaInfo(db *sql.DB) (SchemaInfo, error) {
	info := SchemaInfo{Binary: SchemaVersion}
	want, err := expectedShape()
	if err != nil {
		return info, err
	}
	have, err := readShape(db)
	if err != nil {
		return info, err
	}
	info.DB = have.version
	switch {
	case len(have.objects) == 0:
		info.State = SchemaEmpty
	case have.version > SchemaVersion:
		info.State = SchemaNewer
	case have.version < SchemaVersion || lacks(have, want):
		info.State = SchemaOlder
	default:
		info.State = SchemaOK
	}
	return info, nil
}

func lacks(have, want schemaShape) bool {
	for o := range want.objects {
		if !have.objects[o] {
			return true
		}
	}
	for c := range want.columns {
		if !have.columns[c] {
			return true
		}
	}
	return false
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// dbVersion is the DB's schema version: meta schema.version, or user_version when
// there is no meta table, no key or no integer in it (a DB orbit-mem 0.16 or older made).
func dbVersion(q rowQuerier) (int, error) {
	var tables int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&tables); err != nil {
		return 0, err
	}
	if tables > 0 {
		var v string
		err := q.QueryRow(`SELECT value FROM meta WHERE key=?`, metaSchemaVersion).Scan(&v)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); err == nil && perr == nil {
			return n, nil
		}
	}
	var uv int
	err := q.QueryRow(`PRAGMA user_version`).Scan(&uv)
	return uv, err
}

func schemaNewerError(db int) error {
	return i18n.Errorf("memory.err.schema_newer_at", ErrSchemaNewer, db, SchemaVersion)
}

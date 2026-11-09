// Package sqlitedb opens this PC's SQLite files (memory.db) the same way.
package sqlitedb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Options say how OpenWith opens a file. The zero value with a Busy is Open's write open.
type Options struct {
	// Busy is how long a statement waits for another process's lock (busy_timeout).
	Busy time.Duration
	// Pragmas are extra per-connection pragmas, such as "foreign_keys(1)".
	Pragmas []string
	// Existing opens only a file that is there (mode=rw) and leaves it as it is: no
	// directory or file is made, the journal mode and the permissions are not changed.
	// Transactions still take the write lock at Begin (_txlock=immediate), so a write
	// in a read operation waits for Busy instead of failing at once when it upgrades.
	Existing bool
	// ReadOnly opens only a file that is there, read only (mode=ro, query_only): every
	// write fails with SQLITE_READONLY. Transactions are deferred and take no write lock.
	ReadOnly bool
}

// Open opens path (creating it and its private directory if needed) with one
// connection, the busy timeout and the extra pragmas, such as "foreign_keys(1)",
// and puts the file in WAL.
//
// _txlock=immediate makes a writer wait for the busy timeout instead of failing at
// once when hooks, the MCP server and a retrospective write together. One connection
// per process keeps that wait between processes, not inside one.
func Open(path string, busy time.Duration, pragmas ...string) (*sql.DB, error) {
	return OpenWith(path, Options{Busy: busy, Pragmas: pragmas})
}

// OpenWith opens path with one connection as o says. Without Existing or ReadOnly it
// is Open. With either, a missing file is an error that errors.Is fs.ErrNotExist,
// and the file is opened before OpenWith returns so a damaged one fails here.
func OpenWith(path string, o Options) (*sql.DB, error) {
	keep := o.Existing || o.ReadOnly
	if keep {
		// mode=rw|ro never creates the file; the check gives the caller a plain "missing".
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := FileURI(path) + "?"
	switch {
	case o.ReadOnly:
		// WAL 전환 pragma는 mode=ro에서 열기를 SQLITE_READONLY로 실패시키므로 돌리지 않는다.
		dsn += "mode=ro&_pragma=query_only(1)&"
	case o.Existing:
		dsn += "mode=rw&_txlock=immediate&"
	default:
		dsn += "_txlock=immediate&"
	}
	dsn += fmt.Sprintf("_pragma=busy_timeout(%d)", o.Busy.Milliseconds())
	for _, p := range o.Pragmas {
		dsn += "&_pragma=" + p
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if keep {
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	if err := wal(db, o.Busy); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return db, nil
}

// FileURI is the driver's file: name for path, with the characters that would
// start a query or fragment escaped.
func FileURI(path string) string {
	return "file:" + strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F").Replace(filepath.ToSlash(path))
}

// IsBusy says whether err is SQLite's lock wait (SQLITE_BUSY or SQLITE_LOCKED):
// the same write can succeed later.
func IsBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	c := se.Code() & 0xff
	return c == sqlite3.SQLITE_BUSY || c == sqlite3.SQLITE_LOCKED
}

// wal switches to WAL. Turning a rollback-journal file (an old DB, or a new one
// another process is creating) into WAL returns SQLITE_BUSY at once without the
// busy handler, so the wait is done here.
func wal(db *sql.DB, busy time.Duration) error {
	deadline := time.Now().Add(busy)
	for {
		_, err := db.Exec(`PRAGMA journal_mode=WAL`)
		if err == nil || !IsBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

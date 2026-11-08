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

// Open opens path (creating it and its private directory if needed) with one
// connection, the busy timeout and the extra pragmas, such as "foreign_keys(1)",
// and puts the file in WAL.
//
// _txlock=immediate makes a writer wait for the busy timeout instead of failing at
// once when hooks, the MCP server and a retrospective write together. One connection
// per process keeps that wait between processes, not inside one.
func Open(path string, busy time.Duration, pragmas ...string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("%s?_txlock=immediate&_pragma=busy_timeout(%d)", FileURI(path), busy.Milliseconds())
	for _, p := range pragmas {
		dsn += "&_pragma=" + p
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := wal(db, busy); err != nil {
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

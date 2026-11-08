package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crispebble/orbit-mem/internal/backup"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/sqlitedb"
	"modernc.org/sqlite"
)

// 백업(ONID-136). 사본은 VACUUM INTO로 만든다: 읽기 트랜잭션 하나의 일관된 모습을 새 파일로 쓰고,
// WAL에서 쓰는 쪽을 막지 않으며, 읽기 전용으로 연 옛 work.db에도 그대로 쓴다. 복구만 SQLite 온라인
// 백업 API(sqlite3_backup, 사본→살아 있는 DB)를 쓴다: 다른 프로세스(MCP 서버·훅)가 연 DB 파일을 갈아
// 끼우지 않고 SQLite 잠금 안에서 한 트랜잭션으로 바꾸므로, 열린 연결은 바뀐 내용을 그대로 본다.

// SchemaVersion is the schema version this binary migrates to: migrate writes it to
// meta schema.version (and the schema still sets user_version to it for older binaries).
const SchemaVersion = 4

// Why a daily backup made no copy.
const (
	SkipFresh = "fresh" // 마지막 사본이 24시간 안이다
	SkipBusy  = "busy"  // 다른 프로세스가 지금 사본을 만든다
)

// BackupOptions are the inputs of a daily backup.
type BackupOptions struct {
	Dir   string        // 백업 폴더
	Keep  int           // 남길 하루 사본 수
	Force bool          // 24시간 규칙과 관계없이 만든다
	Wait  time.Duration // 다른 프로세스가 잠금을 쥐고 있으면 기다리는 시간(0이면 바로 건너뛴다)
}

// BackupResult is what a daily backup did. Memory is the copy made, or the newest
// existing one when it was skipped.
type BackupResult struct {
	Memory  string
	Made    bool
	Skipped string
	Removed []string // 보관 개수를 넘어 지운 하루 사본
}

// DailyBackup makes the daily copy of the memory DB under the backups folder's lock.
// Without Force it does nothing while the last memory copy is less than 24 hours old.
// The old work log (work.db) is no longer copied (2026-11-08, ORBIT-13).
func (s *Store) DailyBackup(o BackupOptions) (BackupResult, error) {
	var r BackupResult
	unlock, ok, err := backup.Lock(o.Dir, o.Wait)
	if err != nil {
		return r, i18n.Errorf("memory.err.backup", err)
	}
	if !ok {
		r.Skipped = SkipBusy
		r.Memory = lastCopy(o.Dir)
		return r, nil
	}
	defer unlock()
	// 잠금을 잡은 뒤에 다시 본다: 기다리는 동안 앞 프로세스가 만들었으면 건너뛴다.
	if !o.Force && !backup.Due(o.Dir, s.Now()) {
		r.Skipped = SkipFresh
		r.Memory = lastCopy(o.Dir)
		return r, nil
	}
	if r.Memory, err = s.copyDaily(o.Dir); err != nil {
		return r, err
	}
	r.Made = true
	removed, _ := backup.Prune(o.Dir, backup.PrefixMemory, o.Keep)
	r.Removed = append(r.Removed, removed...)
	return r, nil
}

func lastCopy(dir string) string {
	if c, ok := backup.Last(dir, backup.PrefixMemory); ok {
		return c.Path
	}
	return ""
}

// Backup writes a consistent copy into dir (memory-YYYYMMDD.sqlite, one per day; a
// second copy the same day replaces the first) and keeps the newest keep daily copies.
// It copies whatever the time of the last copy: DailyBackup keeps the 24-hour rule.
func (s *Store) Backup(dir string, keep int) (string, error) {
	dst, err := s.copyDaily(dir)
	if err != nil {
		return "", err
	}
	_, _ = backup.Prune(dir, backup.PrefixMemory, keep)
	return dst, nil
}

func (s *Store) copyDaily(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", i18n.Errorf("memory.err.backup", err)
	}
	dst := filepath.Join(dir, backup.PrefixMemory+"-"+s.Now().Local().Format("20060102")+".sqlite")
	tmp := dst + ".tmp"
	_ = os.Remove(tmp) // 앞 실행이 죽으며 남긴 것(같은 이름을 쓰는 다른 실행은 잠금으로 막는다)
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", i18n.Errorf("memory.err.backup", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, dst); err != nil { // 다 쓴 사본만 이름을 얻는다
		_ = os.Remove(tmp)
		return "", i18n.Errorf("memory.err.backup", err)
	}
	return dst, nil
}

// Snapshot writes a named copy (<name>-YYYYMMDDTHHMMSS.sqlite) into dir that
// daily backup rotation never removes, e.g. before a migration.
func (s *Store) Snapshot(dir, name string) (string, error) {
	return snapshot(s.db, dir, name, s.Now())
}

// snapshot never overwrites: two copies in the same second (two processes included)
// get -2, -3… VACUUM INTO refuses a file that exists, so a lost race tries the next name.
func snapshot(db *sql.DB, dir, name string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", i18n.Errorf("memory.err.copy", err)
	}
	base := filepath.Join(dir, name+"-"+now.Local().Format("20060102T150405"))
	for i := 1; i <= 100; i++ {
		dst := base + ".sqlite"
		if i > 1 {
			dst = fmt.Sprintf("%s-%d.sqlite", base, i)
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
			if _, serr := os.Stat(dst); serr == nil {
				continue // 다른 프로세스가 같은 이름을 먼저 썼다
			}
			return "", i18n.Errorf("memory.err.copy", err)
		}
		return dst, os.Chmod(dst, 0o600)
	}
	return "", i18n.Errorf("memory.err.copy", errors.New("too many copies in one second"))
}

// --- 스키마 이전 전 사본 ---

type schemaShape struct {
	version int
	objects map[string]bool // type:name
	columns map[string]bool // table.column
}

var wantShape struct {
	once  sync.Once
	shape schemaShape
	err   error
}

// expectedShape is the shape migrate gives, read once from an in-memory DB.
func expectedShape() (schemaShape, error) {
	wantShape.once.Do(func() {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			wantShape.err = err
			return
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if wantShape.err = migrate(db); wantShape.err == nil {
			wantShape.shape, wantShape.err = readShape(db)
		}
	})
	return wantShape.shape, wantShape.err
}

func readShape(db *sql.DB) (schemaShape, error) {
	sh := schemaShape{objects: map[string]bool{}, columns: map[string]bool{}}
	var err error
	if sh.version, err = dbVersion(db); err != nil {
		return sh, err
	}
	rows, err := db.Query(`SELECT m.type || ':' || m.name, ifnull(p.name, '') FROM sqlite_master m
		LEFT JOIN pragma_table_info(m.name) p ON m.type = 'table'
		WHERE m.type IN ('table','index','trigger','view') AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return sh, err
	}
	defer rows.Close()
	for rows.Next() {
		var obj, col string
		if err := rows.Scan(&obj, &col); err != nil {
			return sh, err
		}
		sh.objects[obj] = true
		if col != "" {
			sh.columns[strings.TrimPrefix(obj, "table:")+"."+col] = true
		}
	}
	return sh, rows.Err()
}

// needsSchemaChange says whether migrate would change an existing DB: it has tables
// and an older schema version (meta schema.version, see dbVersion) or a missing
// table, index or column. A new empty file has nothing to keep, and a DB a newer
// orbit-mem made is not migrated at all.
func needsSchemaChange(db *sql.DB) (bool, error) {
	info, err := schemaInfo(db)
	return info.State == SchemaOlder, err
}

// schemaBackupDir is where Open keeps the copy before a schema change: the backups
// folder next to the DB (config.Paths.Backups for the PC's memory.db).
func schemaBackupDir(path string) string { return filepath.Join(filepath.Dir(path), "backups") }

// --- 복구 ---

// CopyInfo describes a copy that passed the integrity check.
type CopyInfo struct {
	Memories      int `json:"memories"`
	SchemaVersion int `json:"schemaVersion"`
}

// ReadCopyInfo opens path read only, runs the integrity check and reads what a
// person needs to choose it. A file that is not an intact memory DB is an error.
func ReadCopyInfo(path string) (CopyInfo, error) {
	var info CopyInfo
	st, err := os.Stat(path)
	if err != nil {
		return info, i18n.Errorf("memory.err.copy_check", path, err)
	}
	if !st.Mode().IsRegular() {
		return info, i18n.Errorf("memory.err.copy_check", path, errors.New("not a file"))
	}
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return info, i18n.Errorf("memory.err.copy_check", path, err)
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return info, i18n.Errorf("memory.err.copy_check", path, err)
	}
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return info, i18n.Errorf("memory.err.copy_check", path, err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return info, i18n.Errorf("memory.err.copy_check", path, err)
	}
	if len(problems) > 0 {
		if len(problems) > 3 {
			problems = append(problems[:3], "…")
		}
		return info, i18n.Errorf("memory.err.copy_check", path, errors.New(strings.Join(problems, "; ")))
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&info.Memories); err != nil {
		return info, i18n.Errorf("memory.err.copy_check", path, err)
	}
	_ = db.QueryRow(`PRAGMA user_version`).Scan(&info.SchemaVersion)
	return info, nil
}

// CheckCopy says whether path is an intact memory DB.
func CheckCopy(path string) error {
	_, err := ReadCopyInfo(path)
	return err
}

// RestoreResult is what a restore did.
type RestoreResult struct {
	From   string   `json:"from"`
	Before string   `json:"before"` // 바꾸기 전 DB의 사본
	Info   CopyInfo `json:"info"`   // 바꾼 뒤의 DB
}

// restoreWait is how long a restore waits for a running backup.
var restoreWait = time.Minute

// Restore replaces the memory DB with the copy at src: it checks the copy's
// integrity, keeps the DB as it is now in dir (before-restore-*.sqlite) and copies
// src in with SQLite's online backup in one transaction, then checks the DB again.
// If that check fails the kept copy is put back. Nothing changes when the copy is
// not an intact memory DB or the before copy cannot be made.
func (s *Store) Restore(src, dir string) (RestoreResult, error) {
	r := RestoreResult{From: src}
	if _, err := ReadCopyInfo(src); err != nil {
		return r, err
	}
	unlock, ok, err := backup.Lock(dir, restoreWait)
	if err != nil {
		return r, i18n.Errorf("memory.err.restore", err)
	}
	if !ok {
		return r, i18n.Errorf("memory.err.restore", errors.New("another backup is running"))
	}
	defer unlock()
	if r.Before, err = s.Snapshot(dir, "before-restore"); err != nil {
		return r, i18n.Errorf("memory.err.restore", err)
	}
	if err := s.restoreFrom(src); err != nil {
		return r, i18n.Errorf("memory.err.restore", err)
	}
	if r.Info, err = ReadCopyInfo(s.path); err != nil {
		if berr := s.restoreFrom(r.Before); berr != nil {
			return r, i18n.Errorf("memory.err.restore_back", err, r.Before, berr)
		}
		return r, i18n.Errorf("memory.err.restore", err)
	}
	return r, nil
}

type restorer interface {
	NewRestore(srcURI string) (*sqlite.Backup, error)
}

// restoreFrom copies every page of src into the open DB in one step (one write transaction).
func (s *Store) restoreFrom(src string) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		rs, ok := dc.(restorer)
		if !ok {
			return errors.New("the SQLite driver cannot restore")
		}
		b, err := rs.NewRestore(sqlitedb.FileURI(src) + "?mode=ro")
		if err != nil {
			return err
		}
		for {
			more, err := b.Step(-1)
			if err != nil {
				_ = b.Finish()
				return err
			}
			if !more {
				break
			}
		}
		return b.Finish()
	})
}

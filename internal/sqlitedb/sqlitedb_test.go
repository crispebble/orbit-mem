package sqlitedb

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 옛 판의 DB는 롤백 저널이다. WAL로 바꾸는 PRAGMA는 SQLite의 바쁨 대기를 거치지 않고 곧바로
// SQLITE_BUSY를 내므로, 여러 프로세스(훅·MCP·회고)가 같은 순간 처음 열어도 모두 열려야 한다.
func TestOpen_롤백_저널_DB를_여럿이_동시에_열어도_WAL로_열린다(t *testing.T) {
	for round := 0; round < 30; round++ {
		path := filepath.Join(t.TempDir(), "old.db")
		old, _ := sql.Open("sqlite", "file:"+path)
		if _, err := old.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
			t.Fatal(err)
		}
		old.Close()
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			go func() {
				db, err := Open(path, 5*time.Second)
				if err == nil {
					var mode string
					if err = db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err == nil && mode != "wal" {
						t.Errorf("journal_mode = %s", mode)
					}
					db.Close()
				}
				errs <- err
			}()
		}
		for i := 0; i < 8; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}

func TestOpen_잠금이_바쁨_대기보다_길면_오류를_낸다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	holder, _ := sql.Open("sqlite", "file:"+path)
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	db, err := Open(path, 150*time.Millisecond)
	if err == nil {
		db.Close()
		t.Fatal("잠긴 DB가 열렸습니다")
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("바쁨 대기 전에 포기했습니다: %v", d)
	}
}

func TestOpen_한_연결과_요청한_PRAGMA를_쓴다(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sub", "new.db"), time.Second, "foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var fk, busy int
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	if fk != 1 || busy != 1000 || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("foreign_keys %d, busy_timeout %d, 연결 %d", fk, busy, db.Stats().MaxOpenConnections)
	}
}

// rollbackDB makes a rollback-journal SQLite file with one row, the way an old DB or another
// tool leaves it, so a test sees whether opening it switched it to WAL.
func rollbackDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", FileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
}

func journalMode(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", FileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

// walHolder opens path in WAL and holds its write lock (BEGIN IMMEDIATE) until the test ends.
func walHolder(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS t (id INTEGER); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (2)`); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWith_기본은_Open과_같이_폴더와_파일을_만들고_WAL로_연다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "new.db")
	db, err := OpenWith(path, Options{Busy: time.Second, Pragmas: []string{"foreign_keys(1)"}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var fk, busy int
	var mode string
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if fk != 1 || busy != 1000 || mode != "wal" || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("foreign_keys %d, busy_timeout %d, journal %s, 연결 %d", fk, busy, mode, db.Stats().MaxOpenConnections)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("파일 %v %v", fi, err)
	}
}

func TestOpenWith_있는_파일만_여는_길은_파일이_없으면_만들지_않는다(t *testing.T) {
	for _, o := range []Options{{Busy: time.Second, Existing: true}, {Busy: time.Second, ReadOnly: true}} {
		dir := filepath.Join(t.TempDir(), "sub")
		path := filepath.Join(dir, "none.db")
		db, err := OpenWith(path, o)
		if err == nil {
			db.Close()
			t.Fatalf("%+v: 없는 파일이 열렸습니다", o)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%+v: err = %v, 파일 없음이어야 합니다", o, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%+v: 폴더나 파일을 만들었습니다: %v", o, err)
		}
	}
}

func TestOpenWith_Existing은_WAL로_바꾸지_않고_트랜잭션은_처음부터_쓰기_잠금을_쥔다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	rollbackDB(t, path)
	os.Chmod(path, 0o644)
	db, err := OpenWith(path, Options{Busy: 200 * time.Millisecond, Existing: true, Pragmas: []string{"foreign_keys(1)"}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n, fk, busy int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("읽기 %d %v", n, err)
	}
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	if fk != 1 || busy != 200 || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("foreign_keys %d, busy_timeout %d, 연결 %d", fk, busy, db.Stats().MaxOpenConnections)
	}
	if m := journalMode(t, path); m == "wal" {
		t.Fatal("Existing이 WAL로 바꿨습니다")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("권한을 바꿨습니다: %v", fi.Mode().Perm())
	}
	// _txlock=immediate: Begin이 읽기 전에 쓰기 잠금을 쥐므로, 다른 연결의 쓰기 트랜잭션은 바쁨이다.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	other, err := sql.Open("sqlite", FileURI(path)+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`BEGIN IMMEDIATE`); !IsBusy(err) {
		t.Fatalf("Begin이 쓰기 잠금을 쥐지 않았습니다: %v", err)
	}
}

func TestOpenWith_ReadOnly는_쓰기를_거절하고_WAL로_바꾸지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	rollbackDB(t, path)
	db, err := OpenWith(path, Options{Busy: time.Second, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n, qo int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("읽기 %d %v", n, err)
	}
	db.QueryRow(`PRAGMA query_only`).Scan(&qo)
	if qo != 1 {
		t.Fatalf("query_only = %d", qo)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (2)`); err == nil {
		t.Fatal("읽기 전용 연결이 썼습니다")
	}
	if _, err := db.Exec(`CREATE TABLE u (id INTEGER)`); err == nil {
		t.Fatal("읽기 전용 연결이 표를 만들었습니다")
	}
	if m := journalMode(t, path); m == "wal" {
		t.Fatal("ReadOnly가 WAL로 바꿨습니다")
	}
}

// WAL에서 읽기는 쓰기를 기다리지 않는다. ReadOnly의 트랜잭션은 deferred라 읽기 트랜잭션도 바로 된다.
func TestOpenWith_다른_연결이_쓰기_잠금을_쥐어도_읽기는_바로_된다(t *testing.T) {
	for _, o := range []Options{{Busy: 300 * time.Millisecond, Existing: true}, {Busy: 300 * time.Millisecond, ReadOnly: true}} {
		path := filepath.Join(t.TempDir(), "m.db")
		walHolder(t, path)
		db, err := OpenWith(path, o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%+v: 읽기 %d %v (커밋 안 된 줄은 보이지 않아야 합니다)", o, n, err)
		}
		if o.ReadOnly {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("읽기 트랜잭션: %v", err)
			}
			if err := tx.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
				t.Fatalf("읽기 트랜잭션 안 읽기: %v", err)
			}
			tx.Rollback()
		}
		db.Close()
	}
}

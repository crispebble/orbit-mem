package sqlitedb

import (
	"database/sql"
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

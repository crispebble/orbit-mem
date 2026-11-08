package memory

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// 읽기 열기(설계 10.2, ORBIT-23 G1): 읽기 op는 이전·사본 없이 열고, DB 판은 meta schema.version으로 맞춘다.

// rawDB opens path without the memory package, for setting up and checking a file as another
// orbit-mem (an older or a newer binary) would leave it.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func rawExec(t *testing.T, path, q string) {
	t.Helper()
	db := rawDB(t, path)
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
	db.Close()
}

// fileShape is everything a migration would change: the schema objects, user_version and the meta rows.
func fileShape(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var objs, meta string
	var uv int
	if err := db.QueryRow(`SELECT ifnull(group_concat(x, char(10)), '') FROM (SELECT type || ' ' || name || ' ' || ifnull(sql, '') AS x FROM sqlite_master ORDER BY 1)`).Scan(&objs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT ifnull(group_concat(key || '=' || value, ','), '') FROM (SELECT key, value FROM meta ORDER BY key)`).Scan(&meta)
	return objs + "\nuser_version=" + strconv.Itoa(uv) + "\nmeta=" + meta
}

func metaVersion(t *testing.T, path string) string {
	t.Helper()
	var v string
	_ = rawDB(t, path).QueryRow(`SELECT value FROM meta WHERE key='schema.version'`).Scan(&v)
	return v
}

// legacyDB makes path look like a memory DB orbit-mem 0.16 or older left: no schema.version in
// meta, user_version 3, no audit tables and no retros.session column.
func legacyDB(t *testing.T, path string, titlesToKeep ...string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range titlesToKeep {
		saveFact(t, s, ti)
	}
	if _, err := s.DB().Exec(`DROP TABLE audit_acks; DROP TABLE audit_flags; DROP INDEX retros_session;
		ALTER TABLE retros DROP COLUMN session; DELETE FROM meta WHERE key='schema.version'; PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	s.Close()
}

type readOpen struct {
	name string
	open func(string) (*Store, SchemaInfo, error)
}

var readOpens = []readOpen{{"OpenRead", OpenRead}, {"OpenReadOnly", OpenReadOnly}}

func TestOpenRead_옛_꼴_DB를_이전하지도_사본을_만들지도_않고_older로_알린다(t *testing.T) {
	for _, o := range readOpens {
		home := t.TempDir()
		path := filepath.Join(home, "memory.db")
		legacyDB(t, path, "옛 기억")
		before := fileShape(t, path)
		s, info, err := o.open(path)
		if err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
		if info != (SchemaInfo{Binary: SchemaVersion, DB: 3, State: SchemaOlder}) {
			t.Fatalf("%s: info = %+v", o.name, info)
		}
		var title string
		if err := s.DB().QueryRow(`SELECT title FROM memories`).Scan(&title); err != nil || title != "옛 기억" {
			t.Fatalf("%s: 읽기 %q %v", o.name, title, err)
		}
		s.Close()
		if after := fileShape(t, path); after != before {
			t.Fatalf("%s: 옛 꼴이 바뀌었습니다\n전:\n%s\n뒤:\n%s", o.name, before, after)
		}
		if strings.Contains(before, "audit_flags") || strings.Contains(before, "retros_session") {
			t.Fatalf("%s: 옛 꼴을 만들지 못했습니다:\n%s", o.name, before)
		}
		if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
			t.Fatalf("%s: 사본 폴더를 만들었습니다: %v", o.name, err)
		}
	}
}

func TestOpenRead_파일이_없으면_만들지_않고_missing으로_알린다(t *testing.T) {
	for _, o := range readOpens {
		dir := filepath.Join(t.TempDir(), "orbit")
		path := filepath.Join(dir, "memory.db")
		s, info, err := o.open(path)
		if err == nil {
			s.Close()
			t.Fatalf("%s: 없는 DB가 열렸습니다", o.name)
		}
		if !errors.Is(err, fs.ErrNotExist) || s != nil {
			t.Fatalf("%s: err = %v, store %v", o.name, err, s)
		}
		if info != (SchemaInfo{Binary: SchemaVersion, State: SchemaMissing}) {
			t.Fatalf("%s: info = %+v", o.name, info)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s: 폴더나 파일을 만들었습니다: %v", o.name, err)
		}
	}
}

func TestOpenRead_표_없는_파일은_empty이고_표를_만들지_않는다(t *testing.T) {
	for _, o := range readOpens {
		path := filepath.Join(t.TempDir(), "memory.db")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		s, info, err := o.open(path)
		if err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
		s.Close()
		if info != (SchemaInfo{Binary: SchemaVersion, State: SchemaEmpty}) {
			t.Fatalf("%s: info = %+v", o.name, info)
		}
		if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
			t.Fatalf("%s: 빈 파일에 썼습니다: %v %v", o.name, fi, err)
		}
	}
}

func TestOpenRead_최신_DB는_ok이고_meta가_없으면_user_version을_판으로_본다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, w, "지금 기억")
	if info, err := w.Schema(); err != nil || info != (SchemaInfo{Binary: SchemaVersion, DB: SchemaVersion, State: SchemaOK}) {
		t.Fatalf("쓰기 열기 판 %+v %v", info, err)
	}
	w.Close()
	for _, o := range readOpens {
		s, info, err := o.open(path)
		if err != nil || info != (SchemaInfo{Binary: SchemaVersion, DB: SchemaVersion, State: SchemaOK}) {
			t.Fatalf("%s: %+v %v", o.name, info, err)
		}
		s.Close()
	}
	// 0.16 이하가 만든 DB(meta 키 없음)는 user_version이 판이다.
	rawExec(t, path, `DELETE FROM meta WHERE key='schema.version'`)
	s, info, err := OpenRead(path)
	if err != nil || info != (SchemaInfo{Binary: SchemaVersion, DB: SchemaVersion, State: SchemaOK}) {
		t.Fatalf("meta 없는 DB: %+v %v", info, err)
	}
	s.Close()
}

func TestOpenReadOnly_쓰기를_거절한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, w, "있던 기억")
	w.Close()
	before := fileShape(t, path)
	s, _, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetMeta("x", "1"); err == nil {
		t.Fatal("읽기 전용 열기가 meta를 썼습니다")
	}
	if _, err := s.Save(SaveInput{Kind: KindFact, Title: "새 기억", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "새 기억"}}); err == nil {
		t.Fatal("읽기 전용 열기가 기억을 저장했습니다")
	}
	if after := fileShape(t, path); after != before {
		t.Fatalf("읽기 전용 열기가 DB를 바꿨습니다")
	}
	if got := titles(t, path); got != "있던 기억" {
		t.Fatalf("기억 = %q", got)
	}
}

// 훅의 기한(4초) 안에서 끝나야 하므로, 읽기 op는 다른 프로세스의 쓰기를 기다리지 않고 읽고,
// 표시·사건 같은 쓰기는 300ms만 기다리다 포기한다(실패하면 표시 없이 넘어간다).
func TestOpenRead_다른_연결이_쓰기_잠금을_쥐어도_읽기는_되고_쓰기는_300ms쯤_기다리다_실패한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	saveFact(t, w, "커밋된 기억")
	tx, err := w.DB().Begin() // _txlock=immediate: 쓰기 잠금을 쥔다
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('held','1')`); err != nil {
		t.Fatal(err)
	}

	r, info, err := OpenRead(path)
	if err != nil || info.State != SchemaOK {
		t.Fatalf("잠긴 DB 읽기 열기: %+v %v", info, err)
	}
	defer r.Close()
	// 잠금은 이 시험 끝까지 풀리지 않으므로, 읽기가 잠금을 기다렸다면 성공할 수 없다.
	var n int
	if err := r.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("읽기 %d %v", n, err)
	}
	if v := r.Meta("held"); v != "" {
		t.Fatalf("커밋 안 된 줄이 보입니다: %q", v)
	}

	for name, write := range map[string]func() error{
		"자동 커밋 문장": func() error { return r.SetMeta("shown", "1") },
		"트랜잭션": func() error {
			tx, err := r.DB().Begin()
			if err == nil {
				tx.Rollback()
			}
			return err
		},
	} {
		start := time.Now()
		err := write()
		d := time.Since(start)
		if !sqlitedb.IsBusy(err) {
			t.Fatalf("%s: err = %v, 바쁨이어야 합니다", name, err)
		}
		// 아래 한계는 바쁨 대기를 지켰는지, 위 한계는 쓰기 열기의 5초가 아닌지를 본다.
		if d < 250*time.Millisecond || d > 3*time.Second {
			t.Fatalf("%s: %v 만에 포기했습니다(300ms 안팎이어야 합니다)", name, d)
		}
	}
}

func TestOpenRead_meta_판이_실행_파일보다_높으면_newer로_알리고_DB를_바꾸지_않는다(t *testing.T) {
	newer := SchemaVersion + 1
	for _, o := range readOpens {
		home := t.TempDir()
		path := filepath.Join(home, "memory.db")
		w, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
		rawExec(t, path, `UPDATE meta SET value='`+strconv.Itoa(newer)+`' WHERE key='schema.version'`)
		before := fileShape(t, path)
		s, info, err := o.open(path)
		if err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
		s.Close()
		if info != (SchemaInfo{Binary: SchemaVersion, DB: newer, State: SchemaNewer}) {
			t.Fatalf("%s: info = %+v", o.name, info)
		}
		if after := fileShape(t, path); after != before {
			t.Fatalf("%s: DB가 바뀌었습니다", o.name)
		}
	}
}

func TestOpen_DB가_실행_파일보다_새로우면_이전하지_않고_ErrSchemaNewer(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	// 새 판이 표를 하나 없앴다고 치면, 옛 판의 이전은 그 표를 다시 만들 것이다.
	rawExec(t, path, `DROP TABLE audit_acks; UPDATE meta SET value='`+strconv.Itoa(SchemaVersion+1)+`' WHERE key='schema.version'`)
	before := fileShape(t, path)
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("새 판 DB를 옛 실행 파일이 쓰기로 열었습니다")
	}
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("err = %v", err)
	}
	if after := fileShape(t, path); after != before {
		t.Fatalf("새 판 DB를 바꿨습니다\n전:\n%s\n뒤:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
		t.Fatalf("사본을 만들었습니다: %v", err)
	}
}

func TestOpen_이전이_meta_판을_쓰고_올리기만_한다(t *testing.T) {
	want := strconv.Itoa(SchemaVersion)
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if v := metaVersion(t, path); v != want {
		t.Fatalf("새 DB의 meta 판 = %q", v)
	}
	// 0.16 이하가 남긴 DB(meta 키 없음)는 이전이 키를 쓴다.
	old := filepath.Join(t.TempDir(), "memory.db")
	legacyDB(t, old, "옛 기억")
	if s, err = Open(old); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if v := metaVersion(t, old); v != want {
		t.Fatalf("옛 DB 이전 뒤 meta 판 = %q", v)
	}
	// 낮은 판은 올린다.
	rawExec(t, path, `UPDATE meta SET value='`+strconv.Itoa(SchemaVersion-1)+`' WHERE key='schema.version'`)
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if v := metaVersion(t, path); v != want {
		t.Fatalf("낮은 판을 올리지 않았습니다: %q", v)
	}
}

// Open이 판을 본 뒤 이전 트랜잭션을 잡기 전에 새 실행 파일이 이전할 수 있다. 이전은 쓰기 잠금 안에서
// 판을 다시 보고, 더 새로운 판을 낮추지 않는다.
func TestMigrate_쓰기_잠금_안에서_더_새로운_판을_만나면_낮추지_않고_ErrSchemaNewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newer := strconv.Itoa(SchemaVersion + 5)
	if err := s.SetMeta(metaSchemaVersion, newer); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.DB()); !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("err = %v", err)
	}
	if v := s.Meta(metaSchemaVersion); v != newer {
		t.Fatalf("meta 판을 %q로 낮췄습니다", v)
	}
}

// 옛 실행 파일(0.16 이하)은 열 때마다 user_version을 자기 판으로 다시 쓴다. 판의 정본은 meta라서,
// 그 뒤에도 새 실행 파일이 매번 사본·이전을 하지 않는다.
func TestNeedsSchemaChange_옛_실행_파일이_user_version을_되돌려도_meta_판이_맞으면_거짓(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	rawExec(t, path, `PRAGMA user_version=`+strconv.Itoa(SchemaVersion-1))
	db := rawDB(t, path)
	need, err := needsSchemaChange(db)
	if err != nil || need {
		t.Fatalf("needsSchemaChange = %v %v", need, err)
	}
	db.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
		t.Fatalf("판이 맞는 DB의 사본을 만들었습니다: %v", err)
	}
}

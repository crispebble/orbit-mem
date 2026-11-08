package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/backup"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
)

func saveFact(t *testing.T, s *Store, title string) {
	t.Helper()
	if _, err := s.Save(SaveInput{Kind: KindFact, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: title}}); err != nil {
		t.Fatal(err)
	}
}

func titles(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT title FROM memories ORDER BY title`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// --- 하루 한 번 규칙 ---

func TestDailyBackup_24시간_안이면_새로_만들지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	saveFact(t, s, "a")
	dir := filepath.Join(t.TempDir(), "backups")
	r, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14})
	if err != nil || !r.Made || r.Memory == "" || r.Skipped != "" {
		t.Fatalf("first = %+v %v", r, err)
	}
	// 사본 시각을 가짜 시계로 맞춘다(파일 시각은 실제 시계라서).
	at := c.t.Add(-time.Hour)
	os.Chtimes(r.Memory, at, at)
	saveFact(t, s, "b")
	r2, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14})
	if err != nil || r2.Made || r2.Skipped != SkipFresh || r2.Memory != r.Memory {
		t.Fatalf("within 24h = %+v %v", r2, err)
	}
	if got := titles(t, r.Memory); got != "a" {
		t.Fatalf("the fresh copy was rewritten: %s", got)
	}
	// 24시간이 지나면 다시 만든다.
	at = c.t.Add(-25 * time.Hour)
	os.Chtimes(r.Memory, at, at)
	r3, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14})
	if err != nil || !r3.Made {
		t.Fatalf("after 24h = %+v %v", r3, err)
	}
	if got := titles(t, r3.Memory); got != "a,b" {
		t.Fatalf("new copy = %s", got)
	}
}

func TestDailyBackup_Force면_24시간_안이어도_만든다(t *testing.T) {
	s, c := newTestStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	r, _ := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14})
	os.Chtimes(r.Memory, c.t, c.t)
	saveFact(t, s, "a")
	r2, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14, Force: true})
	if err != nil || !r2.Made || titles(t, r2.Memory) != "a" {
		t.Fatalf("forced = %+v %v", r2, err)
	}
}

// 기획 · orbit-mem X5(M-5): 옛 작업 기록(work.db)은 더 복사하지 않고, 옛 파일도 건드리지 않는다.
func TestDailyBackup_옛_작업_기록은_복사하지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	home := t.TempDir()
	work := filepath.Join(home, "work.db")
	os.WriteFile(work, []byte("옛 작업 기록"), 0o600)
	dir := filepath.Join(home, "backups")
	r, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 14})
	if err != nil || !r.Made {
		t.Fatalf("backup = %+v %v", r, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "work-*")); len(m) != 0 {
		t.Fatalf("work 사본 = %v", m)
	}
	if b, err := os.ReadFile(work); err != nil || string(b) != "옛 작업 기록" {
		t.Fatalf("work.db = %q %v", b, err)
	}
}

func TestDailyBackup_보관_개수를_넘는_하루_사본만_지운다(t *testing.T) {
	s, c := newTestStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "before-0.3.0-memory.sqlite"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "memory-keep.sqlite"), []byte("x"), 0o600)
	for i := 0; i < 5; i++ {
		if _, err := s.DailyBackup(BackupOptions{Dir: dir, Keep: 3, Force: true}); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(24 * time.Hour)
	}
	var daily []string
	for _, cp := range mustList(t, dir) {
		if cp.Kind == backup.KindMemory {
			daily = append(daily, cp.Name)
		}
	}
	if len(daily) != 3 {
		t.Fatalf("daily = %v", daily)
	}
	for _, n := range []string{"before-0.3.0-memory.sqlite", "memory-keep.sqlite"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("%s removed", n)
		}
	}
}

func mustList(t *testing.T, dir string) []backup.Copy {
	t.Helper()
	l, err := backup.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// 훅·회고·사람이 같은 순간에 불러도 사본은 하나만 만들고 깨지지 않는다(잠금).
func TestDailyBackup_동시에_불러도_사본은_하나만_만든다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	dir := filepath.Join(home, "backups")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	for i := 0; i < 50; i++ {
		saveFact(t, first, "m"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	const n = 8
	stores := make([]*Store, n)
	for i := range stores {
		if stores[i], err = Open(path); err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	var wg sync.WaitGroup
	results := make([]BackupResult, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = stores[i].DailyBackup(BackupOptions{Dir: dir, Keep: 14, Wait: 10 * time.Second})
		}(i)
	}
	// 사본을 만드는 동안에도 기억을 쓴다.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			saveFact(t, first, "w"+string(rune('a'+i)))
		}
	}()
	close(start)
	wg.Wait()
	made := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if results[i].Made {
			made++
		}
	}
	if made != 1 {
		t.Fatalf("made = %d, results %+v", made, results)
	}
	list := mustList(t, dir)
	if len(list) != 1 {
		t.Fatalf("copies = %+v", list)
	}
	if err := CheckCopy(list[0].Path); err != nil {
		t.Fatalf("copy integrity: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("left a temp file: %s", e.Name())
		}
	}
}

// --- 스키마 이전 전 사본 ---

// oldSchema makes path look like a memory DB an older orbit-mem left: user_version 3 and no audit tables.
func oldSchema(t *testing.T, path string, titlesToKeep ...string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range titlesToKeep {
		saveFact(t, s, ti)
	}
	if _, err := s.DB().Exec(`DROP TABLE audit_acks; DROP TABLE audit_flags; PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestOpen_스키마를_바꾸기_전에_사본을_남긴다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	oldSchema(t, path, "옛 기억")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	var snaps []backup.Copy
	for _, c := range mustList(t, filepath.Join(home, "backups")) {
		if strings.HasPrefix(c.Name, "before-schema-") {
			snaps = append(snaps, c)
		}
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if v := userVersion(t, snaps[0].Path); v != 3 {
		t.Fatalf("snapshot user_version = %d, want the old 3", v)
	}
	if got := titles(t, snaps[0].Path); got != "옛 기억" {
		t.Fatalf("snapshot memories = %q", got)
	}
	if v := userVersion(t, path); v != SchemaVersion {
		t.Fatalf("migrated user_version = %d", v)
	}
}

func TestOpen_열이_빠진_옛_DB도_스키마_이전으로_본다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP INDEX retros_session; ALTER TABLE retros DROP COLUMN session`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if l := mustList(t, filepath.Join(home, "backups")); len(l) != 1 || !strings.HasPrefix(l[0].Name, "before-schema-") {
		t.Fatalf("copies = %+v", l)
	}
}

func TestOpen_새_DB나_최신_스키마면_사본을_만들지_않는다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		saveFact(t, s, "x"+string(rune('a'+i)))
		s.Close()
	}
	if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
		t.Fatalf("backups made for a current DB: %v", err)
	}
}

func TestOpen_사본을_못_만들면_스키마를_바꾸지_않는다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	oldSchema(t, path, "옛 기억")
	os.WriteFile(filepath.Join(home, "backups"), []byte("폴더 자리에 파일"), 0o600)
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("open must fail when the copy before the schema change cannot be made")
	}
	if v := userVersion(t, path); v != 3 {
		t.Fatalf("schema changed without a copy: user_version %d", v)
	}
}

// --- 복구 ---

func TestRestore_바꾸기_전_DB를_남기고_사본으로_바꾼다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	dir := filepath.Join(home, "backups")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saveFact(t, s, "a")
	cp, err := s.Backup(dir, 14)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "b")
	other, err := Open(path) // 복구하는 동안 열려 있는 다른 연결(MCP 서버 같은)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if n, _ := other.Count(); n != 2 {
		t.Fatalf("before restore = %d", n)
	}
	r, err := s.Restore(cp, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(r.Before), "before-restore-") || titles(t, r.Before) != "a,b" {
		t.Fatalf("before = %+v (%s)", r, titles(t, r.Before))
	}
	if got := titles(t, path); got != "a" {
		t.Fatalf("restored = %q", got)
	}
	if n, _ := other.Count(); n != 1 {
		t.Fatalf("the other connection sees %d memories after restore", n)
	}
	if err := CheckCopy(path); err != nil {
		t.Fatalf("live DB after restore: %v", err)
	}
	if got := titles(t, cp); got != "a" {
		t.Fatalf("the copy itself changed: %q", got)
	}
}

func TestRestore_깨진_사본이면_바꾸지_않는다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	dir := filepath.Join(home, "backups")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 200; i++ {
		saveFact(t, s, strings.Repeat("긴 제목 ", 20)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	cp, err := s.Backup(dir, 14)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cp)
	for i := 4096 * 2; i < len(b) && i < 4096*6; i++ { // 첫 쪽(머리)은 두고 뒤쪽 몇 쪽을 망가뜨린다
		b[i] = 0xA5
	}
	os.WriteFile(cp, b, 0o600)
	before := titles(t, path)
	if _, err := s.Restore(cp, dir); err == nil {
		t.Fatal("restore from a broken copy must fail")
	}
	if titles(t, path) != before {
		t.Fatal("the live DB changed")
	}
	for _, c := range mustList(t, dir) {
		if strings.HasPrefix(c.Name, "before-restore-") {
			t.Fatalf("a refused restore left %s", c.Name)
		}
	}
}

func TestRestore_기억_DB가_아닌_파일은_받지_않는다(t *testing.T) {
	home := t.TempDir()
	s, err := Open(filepath.Join(home, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saveFact(t, s, "a")
	other := filepath.Join(home, "other.sqlite")
	db, _ := sql.Open("sqlite", other)
	db.Exec(`CREATE TABLE t(x)`)
	db.Close()
	text := filepath.Join(home, "text.sqlite")
	os.WriteFile(text, []byte("not sqlite"), 0o600)
	for _, p := range []string{other, text, filepath.Join(home, "none.sqlite")} {
		if _, err := s.Restore(p, filepath.Join(home, "backups")); err == nil {
			t.Fatalf("%s accepted", p)
		}
	}
	if got := titles(t, filepath.Join(home, "memory.db")); got != "a" {
		t.Fatalf("live DB changed: %q", got)
	}
}

func TestCopyInfo_사본의_기억_수와_스키마_판(t *testing.T) {
	s, _ := newTestStore(t)
	saveFact(t, s, "a")
	saveFact(t, s, "b")
	cp, err := s.Backup(t.TempDir(), 14)
	if err != nil {
		t.Fatal(err)
	}
	info, err := ReadCopyInfo(cp)
	if err != nil || info.Memories != 2 || info.SchemaVersion != SchemaVersion {
		t.Fatalf("info = %+v %v", info, err)
	}
}

// 이름을 정한 사본은 같은 초에 여러 번 찍어도(여러 프로세스가 함께 찍어도) 앞 사본을 덮지 않는다.
func TestSnapshot_같은_초에_함께_찍어도_모두_남는다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	dir := filepath.Join(home, "backups")
	fixed := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	const n = 6
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		s.Now = func() time.Time { return fixed }
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			paths[i], errs[i] = s.Snapshot(dir, "before-import")
		}(i, s)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range paths {
		if errs[i] != nil {
			t.Fatalf("snapshot %d: %v", i, errs[i])
		}
		seen[paths[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("paths = %v", paths)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// 검색 색인 운영(ORBIT-25 a7): migrate --reindex, migrate의 늦은 줄 채우기, 복구·install 뒤 채우기,
// doctor의 index-stale. 색인 채우기는 훅·읽기 op가 아니라 쓰기 op·install·migrate·복구가 한다.

func countTable(t *testing.T, s *memory.Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedN saves n memories and returns the store.
func seedN(t *testing.T, n int) *memory.Store {
	t.Helper()
	s := seed(t)
	for i := range n {
		ti := fmt.Sprintf("색인 시험 기억 %d", i)
		if _, err := s.Save(memory.SaveInput{Kind: "fact", Title: ti, Body: "본문 " + ti, Source: memory.Source{Kind: "quote", Excerpt: ti}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// makeStale does what an old binary does: bumps updated_at without touching the index.
func makeStale(t *testing.T, s *memory.Store) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE memories SET updated_at = updated_at + 1`); err != nil {
		t.Fatal(err)
	}
}

func problemIDs(t *testing.T) map[string]problem {
	t.Helper()
	m, _ := schemaProblems(t)
	return m
}

func TestMigrate_reindex는_망가진_색인을_고치고_두_번_해도_같다(t *testing.T) {
	home(t)
	s := seedN(t, 3)
	if _, err := s.DB().Exec(`DELETE FROM memory_index WHERE rowid=(SELECT MIN(rowid) FROM memory_index); UPDATE memory_fts SET body='엉뚱 zzz'; INSERT INTO memory_index(memory_id, indexed_at) VALUES('m-gone','1')`); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "", "migrate", "--reindex")
	if code != 0 || !strings.Contains(out, "3") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	check := func() {
		if n := countTable(t, s, "memory_index"); n != 3 {
			t.Fatalf("memory_index = %d", n)
		}
		if n := countTable(t, s, "memory_fts"); n != 3 {
			t.Fatalf("memory_fts = %d", n)
		}
		if n, _ := s.StaleIndexCount(); n != 0 {
			t.Fatalf("stale = %d", n)
		}
		var bad int
		s.DB().QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE body LIKE '%zzz%'`).Scan(&bad)
		if bad != 0 {
			t.Fatal("broken fts text left")
		}
	}
	check()
	out2, _, code := run(t, "", "migrate", "--reindex")
	if code != 0 || out2 != out {
		t.Fatalf("second run %d %q vs %q", code, out2, out)
	}
	check()
}

func TestMigrate_reindex는_다른_인자와_함께_쓰지_않는다(t *testing.T) {
	home(t)
	seedN(t, 1)
	if _, _, code := run(t, "", "migrate", "--reindex", "--from", "x"); code != 2 {
		t.Fatalf("code = %d", code)
	}
	if _, _, code := run(t, "", "migrate", "--reindex", "--dry-run"); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestMigrate_스키마_이전은_늦은_줄을_채운다(t *testing.T) {
	home(t)
	s := seedN(t, 4)
	makeStale(t, s)
	if n, _ := s.StaleIndexCount(); n != 4 {
		t.Fatalf("stale before = %d", n)
	}
	out, errOut, code := run(t, "", "migrate")
	if code != 0 || !strings.Contains(out, "4") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if n, _ := s.StaleIndexCount(); n != 0 {
		t.Fatalf("stale after = %d", n)
	}
	if n := countTable(t, s, "memory_index"); n != 4 {
		t.Fatalf("memory_index = %d", n)
	}
	// --json은 쓰던 키를 지키고 채운 수를 더한다.
	makeStale(t, s)
	out, _, code = run(t, "", "migrate", "--json")
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || m["indexFilled"] != float64(4) {
		t.Fatalf("%d %s", code, out)
	}
	for _, k := range []string{"db", "binary", "state"} {
		if _, ok := m[k]; !ok {
			t.Errorf("json key %q lost: %s", k, out)
		}
	}
}

func TestBackup_판_4_사본으로_복구하면_판_5가_되고_색인이_다_찬다(t *testing.T) {
	home(t)
	seed(t).Close()
	// 판 4 사본: 다른 폴더에서 만든다.
	old := filepath.Join(t.TempDir(), "memory-v4.sqlite")
	o, err := memory.Open(old)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		ti := fmt.Sprintf("옛 판 기억 %d", i)
		if _, err := o.Save(memory.SaveInput{Kind: "fact", Title: ti, Source: memory.Source{Kind: "quote", Excerpt: ti}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.DB().Exec(`DROP TABLE memory_fts; DROP TABLE memory_index; DROP TABLE save_requests;
		ALTER TABLE sources DROP COLUMN trust; UPDATE meta SET value='4' WHERE key='schema.version'; PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	o.Close()

	out, errOut, code := run(t, "", "backup", "restore", old, "--yes")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	s := seed(t)
	info, err := s.Schema()
	if err != nil || info.DB != 5 || info.State != memory.SchemaOK {
		t.Fatalf("schema = %+v %v", info, err)
	}
	if v := s.Meta("schema.version"); v != "5" {
		t.Fatalf("meta schema.version = %q", v)
	}
	n, _ := s.Count()
	if n != 3 || countTable(t, s, "memory_index") != n || countTable(t, s, "memory_fts") != n {
		t.Fatalf("memories %d index %d fts %d", n, countTable(t, s, "memory_index"), countTable(t, s, "memory_fts"))
	}
	if st, _ := s.StaleIndexCount(); st != 0 {
		t.Fatalf("stale = %d", st)
	}
}

func TestDoctor_늦은_색인_줄은_index_stale_문제로_보인다(t *testing.T) {
	newFakePC(t)
	s := seedN(t, 3)
	if _, ok := problemIDs(t)["index-stale"]; ok {
		t.Fatal("index-stale on a healthy DB")
	}
	makeStale(t, s)
	before := dbSum(t, paths().DB())
	probs := problemIDs(t)
	p, ok := probs["index-stale"]
	if !ok || p.Fix != "orbit-mem migrate" || !strings.Contains(p.Message, "3") {
		t.Fatalf("problems = %+v", probs)
	}
	if dbSum(t, paths().DB()) != before {
		t.Fatal("doctor changed the DB")
	}
	if _, _, code := run(t, "", "migrate"); code != 0 {
		t.Fatal(code)
	}
	if _, ok := problemIDs(t)["index-stale"]; ok {
		t.Fatal("index-stale after migrate")
	}
}

func TestDoctor_판이_맞지_않으면_index_stale은_내지_않는다(t *testing.T) {
	newFakePC(t)
	path := oldShapeDB(t)
	_ = path
	probs := problemIDs(t)
	if _, ok := probs["migration-needed"]; !ok {
		t.Fatalf("problems = %+v", probs)
	}
	if _, ok := probs["index-stale"]; ok {
		t.Fatal("index-stale with an older DB")
	}
}

func TestInstall_뒤에_늦은_색인_줄이_없다(t *testing.T) {
	newFakePC(t)
	s := seedN(t, 3)
	makeStale(t, s)
	out, errOut, code := run(t, "", "install", "--no-hosts", "--bin-dir", t.TempDir())
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if n, _ := s.StaleIndexCount(); n != 0 {
		t.Fatalf("stale after install = %d", n)
	}
	if n := countTable(t, s, "memory_index"); n != 3 {
		t.Fatalf("memory_index = %d", n)
	}
}

func TestHook_훅은_늦은_색인_줄을_채우지_않는다(t *testing.T) {
	home(t)
	s := seedN(t, 3)
	makeStale(t, s)
	for _, c := range []struct{ ev, in string }{
		{"session-start", `{"session_id":"IX1","cwd":"/tmp","source":"startup"}`},
		{"user-prompt", `{"session_id":"IX1","cwd":"/tmp","prompt":"색인 시험"}`},
	} {
		run(t, c.in, "hook", c.ev, "--host", "claude")
	}
	if n, _ := s.StaleIndexCount(); n != 3 {
		t.Fatalf("stale after hooks = %d", n)
	}
	if _, _, code := run(t, "", "doctor", "--json"); code != 0 {
		t.Fatal(code)
	}
	if n, _ := s.StaleIndexCount(); n != 3 {
		t.Fatalf("stale after doctor = %d", n)
	}
}

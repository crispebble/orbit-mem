package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	publicSet   = "../../internal/bench/testdata/public_set.json"
	benchProjs  = "../../internal/bench/claudelog/testdata/projects"
	benchSmall  = `{"now":"2026-11-01T00:00:00Z","cwd":"/work/demo","seed":"s","memories":[{"key":"a","kind":"rule","scope":"global","quote":true,"title":"Use tabs","topic":"style","body":"Use tabs in Go files."}],"cases":[{"id":"c1","prompt":"how should I indent Go files","expect":["a"]}]}`
	benchNoMems = `{"now":"2026-11-01T00:00:00Z","cwd":"/work/demo","cases":[{"id":"c1","prompt":"hello"}]}`
)

func TestBench잘못된_깃발은_사용법_오류(t *testing.T) {
	home(t)
	for _, args := range [][]string{
		{"bench"},
		{"bench", "nope"},
		{"bench", "recall"},
		{"bench", "recall", "--bogus"},
		{"bench", "extract", "--bogus"},
		{"bench", "extract", "extra"},
	} {
		if _, _, code := run(t, "", args...); code != 2 {
			t.Errorf("%v: 종료 %d, 2여야 한다", args, code)
		}
	}
	cases := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cases, []byte(benchSmall), 0o600)
	for _, args := range [][]string{
		{"bench", "recall", "--cases", cases, "--split", "x"},
		{"bench", "recall", "--cases", cases, "--recall", "x"},
		{"bench", "recall", "--cases", cases, "--db", "/nonexistent"},
	} {
		if _, _, code := run(t, "", args...); code != 1 {
			t.Errorf("%v: 종료 %d, 1이어야 한다", args, code)
		}
	}
	nm := filepath.Join(t.TempDir(), "n.json")
	os.WriteFile(nm, []byte(benchNoMems), 0o600)
	if _, errb, code := run(t, "", "bench", "recall", "--cases", nm); code != 1 || !strings.Contains(errb, "--db") {
		t.Errorf("memories도 --db도 없으면 오류여야 한다: %d %q", code, errb)
	}
}

func TestBench공개_묶음은_두_회상_보고를_낸다(t *testing.T) {
	home(t)
	out, errb, code := run(t, "", "bench", "recall", "--cases", publicSet)
	if code != 0 {
		t.Fatalf("종료 %d: %s", code, errb)
	}
	for _, want := range []string{"== hostpath ==", "== legacy-87ab0fb ==", "[train]", "[test]", "[all]", "save-check", "find-proxy", "test part"} {
		if !strings.Contains(out, want) {
			t.Errorf("보고에 %q가 없다", want)
		}
	}
}

// 보고 글은 고른 언어로만 나오고(키가 그대로 새지 않음), --split은 그 부분의 지표 묶음만 보인다.
func TestBench보고_글은_언어를_따르고_split은_부분만_보인다(t *testing.T) {
	small := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(small, []byte(benchSmall), 0o600)
	for _, tc := range []struct {
		lang               string
		train, test, whole string
	}{{"en", "[train]", "[test]", "[all]"}, {"ko", "[학습]", "[판정]", "[전체]"}} {
		home(t)
		t.Setenv("ORBIT_LANG", tc.lang)
		out, errb, code := run(t, "", "bench", "recall", "--cases", small)
		if code != 0 {
			t.Fatalf("%s: 종료 %d: %s", tc.lang, code, errb)
		}
		for _, want := range []string{tc.train, tc.test, tc.whole, "== hostpath ==", "== legacy-87ab0fb =="} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: %q가 없다:\n%s", tc.lang, want, out)
			}
		}
		if strings.Contains(out, "bench.") || strings.Contains(out, "%!") {
			t.Errorf("%s: 번역되지 않은 키나 형식 오류가 있다:\n%s", tc.lang, out)
		}
		if tc.lang == "en" && hasHangul(out) {
			t.Errorf("en 보고에 한글이 있다:\n%s", out)
		}
		if tc.lang == "ko" && !strings.Contains(out, "적중") {
			t.Errorf("ko 보고가 한국어가 아니다:\n%s", out)
		}
		for split, want := range map[string][]string{"train": {tc.train}, "test": {tc.test}, "all": {tc.train, tc.test, tc.whole}} {
			out, _, code := run(t, "", "bench", "recall", "--cases", small, "--split", split, "--recall", "legacy")
			if code != 0 || strings.Contains(out, "== hostpath") {
				t.Errorf("%s --split %s: %d\n%s", tc.lang, split, code, out)
			}
			for _, label := range []string{tc.train, tc.test, tc.whole} {
				if strings.Contains(out, label) != slices.Contains(want, label) {
					t.Errorf("%s --split %s: %q 보임 = %v:\n%s", tc.lang, split, label, strings.Contains(out, label), out)
				}
			}
		}
	}
}

func TestBench_json은_결정적이고_report는_사례별_결과를_쓴다(t *testing.T) {
	home(t)
	small := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(small, []byte(benchSmall), 0o600)
	rep := filepath.Join(t.TempDir(), "report.json")
	a, _, code := run(t, "", "bench", "recall", "--cases", small, "--json", "--report", rep)
	if code != 0 {
		t.Fatalf("종료 %d", code)
	}
	b, _, _ := run(t, "", "bench", "recall", "--cases", small, "--json")
	if a != b {
		t.Error("같은 입력의 --json이 다르다")
	}
	var v struct {
		Runs []struct {
			Name  string
			Cases []json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(a), &v); err != nil || len(v.Runs) != 2 || len(v.Runs[0].Cases) == 0 {
		t.Fatalf("JSON 꼴: %v %d", err, len(v.Runs))
	}
	got, err := os.ReadFile(rep)
	if err != nil || string(got) != a {
		t.Errorf("--report 파일이 JSON과 다르다: %v", err)
	}
	if fi, _ := os.Stat(rep); fi.Mode().Perm() != 0o600 {
		t.Errorf("보고 권한 %v", fi.Mode().Perm())
	}
}

func sums(t *testing.T, db string) map[string][32]byte {
	t.Helper()
	m := map[string][32]byte{}
	for _, suf := range []string{"", "-wal", "-shm"} {
		if b, err := os.ReadFile(db + suf); err == nil {
			m[suf] = sha256.Sum256(b)
		}
	}
	return m
}

func openedDB(t *testing.T) (string, *memory.Store) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "src.db")
	s, err := memory.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Save(memory.SaveInput{Kind: "fact", Scope: "global", Title: "bench fixture", Body: "kept", Source: memory.Source{Kind: "quote", Excerpt: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	return db, s
}

func TestBench_db_원본은_바뀌지_않는다(t *testing.T) {
	home(t)
	db, _ := openedDB(t) // 쓰는 연결을 열어 둔다: WAL 프레임이 남은 상태
	cases := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cases, []byte(benchNoMems), 0o600)
	before := sums(t, db)
	if _, ok := before["-wal"]; !ok {
		t.Log("WAL 파일이 없다(연결 설정에 따라 정상)")
	}
	if _, errb, code := run(t, "", "bench", "recall", "--cases", cases, "--db", db); code != 0 {
		t.Fatalf("종료 %d: %s", code, errb)
	}
	if after := sums(t, db); len(after) != len(before) {
		t.Errorf("파일 집합이 바뀌었다")
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%q의 sha256이 바뀌었다", k)
			}
		}
	}
}

func TestBench_extract는_세_파일을_쓴다(t *testing.T) {
	h := home(t)
	db, _ := openedDB(t)
	before := sums(t, db)
	out := filepath.Join(t.TempDir(), "replay")
	stdout, errb, code := run(t, "", "bench", "extract", "--projects", benchProjs, "--db", db, "--out", out, "--min-sessions", "1", "--min-requests", "1")
	if code != 0 {
		t.Fatalf("종료 %d: %s", code, errb)
	}
	for name, perm := range map[string]os.FileMode{"memory-copy.db": 0o600, "sessions.json": 0o600, "cases.json": 0o600} {
		fi, err := os.Stat(filepath.Join(out, name))
		if err != nil || fi.Size() == 0 || fi.Mode().Perm() != perm {
			t.Errorf("%s: %v %v", name, err, fi)
		}
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o700 {
		t.Errorf("폴더 권한 %v", fi.Mode().Perm())
	}
	var cf struct {
		Now      string
		Memories []any
		Cases    []struct {
			ID, Session, Kind, Origin, Prompt, Task string
			Expect                                  []string
		}
	}
	b, _ := os.ReadFile(filepath.Join(out, "cases.json"))
	if err := json.Unmarshal(b, &cf); err != nil || len(cf.Cases) == 0 || cf.Now == "" || len(cf.Memories) != 0 {
		t.Fatalf("cases.json: %v %+v", err, cf)
	}
	for _, c := range cf.Cases {
		hasPrompt := c.Kind == "request" && c.Prompt != ""
		if c.Session == "" || len(c.Expect) != 0 || (hasPrompt && c.Origin == "") || (!hasPrompt && c.Origin != "") {
			t.Errorf("사례 %+v", c)
		}
	}
	// 합성 기록의 요청·위임 글 원문(internal/bench/claudelog/testdata/projects/-work-alpha/s1.jsonl)은 파일에만 쓰고 출력에 찍지 않는다.
	var texts []string
	for _, c := range cf.Cases {
		for _, s := range []string{c.Prompt, c.Task} {
			if s != "" {
				texts = append(texts, s)
			}
		}
	}
	for _, want := range []string{"로그인 버그를 고쳐 줘", "원인을 조사하라"} {
		if !slices.Contains(texts, want) {
			t.Fatalf("cases.json lacks the text of s1.jsonl %q: %q", want, texts)
		}
	}
	for _, s := range texts {
		if strings.Contains(stdout+errb, s) {
			t.Errorf("요청·위임 글 %q가 출력에 찍혔다: %s", s, stdout)
		}
	}
	if !strings.Contains(stdout, out) {
		t.Errorf("개수 요약이 없다: %s", stdout)
	}
	after := sums(t, db)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("원본 %q가 바뀌었다", k)
		}
	}
	if entries, _ := os.ReadDir(h); len(entries) != 0 {
		t.Errorf("임시 홈에 --out 밖의 것이 써졌다: %v", entries)
	}
	// 만든 사본이 열리고 기억이 있다
	s, _, err := memory.OpenReadOnly(filepath.Join(out, "memory-copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestBench_extract는_저장소_안_out을_거절한다(t *testing.T) {
	home(t)
	db, _ := openedDB(t)
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	out := filepath.Join(repo, "deep", "replay")
	_, errb, code := run(t, "", "bench", "extract", "--projects", benchProjs, "--db", db, "--out", out)
	if code != 1 || !strings.Contains(errb, "git") {
		t.Errorf("거절해야 한다: %d %q", code, errb)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("거절했는데 폴더가 만들어졌다")
	}
}

func TestBench_extract와_recall은_닫힌_WAL_DB_옆에_파일을_만들지_않는다(t *testing.T) {
	home(t)
	db, s := openedDB(t)
	s.Close() // 쓰는 연결이 없는 WAL DB: -wal·-shm이 없다
	dir := filepath.Dir(db)
	list := func() []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	before, sumsBefore := list(), sums(t, db)
	if len(before) != 1 {
		t.Fatalf("the test needs the DB alone: %v", before)
	}
	out := filepath.Join(t.TempDir(), "replay")
	if _, errb, code := run(t, "", "bench", "extract", "--projects", benchProjs, "--db", db, "--out", out, "--min-sessions", "1", "--min-requests", "1"); code != 0 {
		t.Fatalf("extract: %d %s", code, errb)
	}
	cases := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cases, []byte(benchNoMems), 0o600)
	if _, errb, code := run(t, "", "bench", "recall", "--cases", cases, "--db", db); code != 0 {
		t.Fatalf("recall: %d %s", code, errb)
	}
	if after := list(); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("files next to the DB: %v, before %v", after, before)
	}
	if after := sums(t, db); after[""] != sumsBefore[""] {
		t.Error("the DB changed")
	}
	cp, _, err := memory.OpenReadOnly(filepath.Join(out, "memory-copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if list, err := cp.List(); err != nil || len(list) != 1 {
		t.Errorf("the copy has %d memories (%v), want 1", len(list), err)
	}
}

// --set은 설정 키를 덮어써 잰다: 같은 사례 파일도 문턱을 바꾸면 중복 지표가 달라지고, 옛 회상은 설정을 읽지 않아 그대로다.
func TestBench_set은_설정을_덮어써_지금_경로만_움직인다(t *testing.T) {
	home(t)
	base, errb, code := run(t, "", "bench", "recall", "--cases", publicSet, "--json")
	if code != 0 {
		t.Fatalf("종료 %d: %s", code, errb)
	}
	strict, errb, code := run(t, "", "bench", "recall", "--cases", publicSet, "--json", "--set", "saveSimilarMin=1")
	if code != 0 {
		t.Fatalf("종료 %d: %s", code, errb)
	}
	type dup struct {
		Hit struct{ Num, Den int }
	}
	type rep struct {
		Runs []struct {
			Name string
			All  struct{ Duplicates dup }
		}
	}
	var a, b rep
	if err := json.Unmarshal([]byte(base), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strict), &b); err != nil {
		t.Fatal(err)
	}
	if len(a.Runs) != 2 || len(b.Runs) != 2 {
		t.Fatalf("runs %d %d", len(a.Runs), len(b.Runs))
	}
	if a.Runs[0].All.Duplicates == b.Runs[0].All.Duplicates {
		t.Errorf("saveSimilarMin=1 did not move the current duplicates: %+v", a.Runs[0].All.Duplicates)
	}
	if a.Runs[1].All.Duplicates != b.Runs[1].All.Duplicates {
		t.Errorf("the frozen recall moved: %+v -> %+v", a.Runs[1].All.Duplicates, b.Runs[1].All.Duplicates)
	}
	// 같은 덮어쓰기는 같은 숫자를 낸다. 덮어쓴 값은 글 보고에 밝힌다.
	again, _, _ := run(t, "", "bench", "recall", "--cases", publicSet, "--json", "--set", "saveSimilarMin=1")
	if again != strict {
		t.Error("the same --set gave another report")
	}
	text, _, code := run(t, "", "bench", "recall", "--cases", publicSet, "--set", "saveSimilarMin=0.8", "--set", "recallChars=900")
	if code != 0 || !strings.Contains(text, "saveSimilarMin=0.8") || !strings.Contains(text, "recallChars=900") {
		t.Errorf("종료 %d, 보고에 덮어쓴 값이 없다:\n%s", code, text)
	}
}

func TestBench_set은_틀린_키와_값을_실행_전에_거절한다(t *testing.T) {
	home(t)
	for _, set := range []string{"nope=1", "saveSimilarMin=2", "saveSimilarMin=abc", "saveSimilarMin", "=1", "products={}", "recallChars=5"} {
		_, errb, code := run(t, "", "bench", "recall", "--cases", publicSet, "--set", set)
		if code != 1 || !strings.Contains(errb, strings.SplitN(set, "=", 2)[0]) && set != "=1" {
			t.Errorf("--set %s: 종료 %d, 오류 %q", set, code, errb)
		}
	}
}

func TestBench_recall은_판_4_DB를_사본에서_올려_돈다(t *testing.T) {
	home(t)
	db, s := openedDB(t)
	if _, err := s.DB().Exec(`DROP TABLE memory_fts; DROP TABLE memory_index; DROP TABLE save_requests;
		ALTER TABLE sources DROP COLUMN trust; UPDATE meta SET value='4' WHERE key='schema.version'; PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	cases := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cases, []byte(benchNoMems), 0o600)
	before := sums(t, db)
	if out, errb, code := run(t, "", "bench", "recall", "--cases", cases, "--db", db); code != 0 {
		t.Fatalf("종료 %d: %s %s", code, out, errb)
	}
	for k, v := range sums(t, db) {
		if before[k] != v {
			t.Errorf("%q의 sha256이 바뀌었다", k)
		}
	}
	if _, info, err := memory.OpenReadOnly(db); err != nil || info.DB != 4 {
		t.Errorf("원본 판 = %+v %v, 4 그대로여야 한다", info, err)
	}
}

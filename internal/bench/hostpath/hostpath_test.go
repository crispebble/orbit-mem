package hostpath_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
)

var testHome string

func TestMain(m *testing.M) {
	// 시험이 이 PC의 실제 홈과 기억 DB를 열지 못하게 임시 홈을 건다.
	tmp, err := os.MkdirTemp("", "orbit-mem-hostpath-test-")
	if err != nil {
		panic(err)
	}
	testHome = tmp
	os.Setenv("ORBIT_HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

const setJSON = `{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
"memories":[
 {"key":"pnpm","kind":"rule","scope":"project:/bench/app","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다. lock 파일은 pnpm-lock.yaml 하나만 둔다.","topic":"앱 패키지 설치","ageDays":40},
 {"key":"deploy","kind":"fact","scope":"global","quote":true,"title":"운영 배포는 main 병합으로 일어난다","body":"감시기가 origin/main을 보고 배포한다.","ageDays":3},
 {"key":"other","kind":"fact","scope":"global","title":"커피 취향은 오트 라떼","ageDays":2}
],
"cases":[{"id":"c1","origin":"user","kind":"request","prompt":"pnpm으로 설치해 줘","expect":["pnpm"]}]}`

type fixture struct {
	p   *bench.Prepared
	env bench.Env
	db  string
}

func open(t *testing.T) fixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(setJSON))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	env, err := hostpath.Factory(db, set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return fixture{p, env, db}
}

func TestRequest가_기대한_기억을_보이고_글자수를_룬으로_센다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Request("s1", "/bench/app", "pnpm으로 설치해 줘")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.IDs, f.p.IDOf("pnpm")) {
		t.Fatalf("ids = %v, want %s in it", got.IDs, f.p.IDOf("pnpm"))
	}
	if got.Text == "" || got.Chars != utf8.RuneCountInString(got.Text) {
		t.Fatalf("chars = %d, text runes = %d", got.Chars, utf8.RuneCountInString(got.Text))
	}
}

func TestStart와_Subagent와_Tool은_오류없이_Shown을_낸다(t *testing.T) {
	f := open(t)
	start, err := f.env.Recaller.Start("s2", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if start.Chars != utf8.RuneCountInString(start.Text) {
		t.Fatalf("start chars = %d", start.Chars)
	}
	if _, err := f.env.Recaller.Subagent("s2", "/bench/app", "pnpm 설치 확인"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Recaller.Tool("s2", "/bench/app", "Bash"); err != nil {
		t.Fatal(err)
	}
}

func TestFinder는_질의의_기억_id를_낸다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Finder.Find("/bench/app", "pnpm", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, f.p.IDOf("pnpm")) || slices.Contains(ids, f.p.IDOf("other")) {
		t.Fatalf("ids = %v", ids)
	}
	one, err := hostpath.NewFinder(f.db, mustSet(t)).Find("/bench/app", "배포", 1)
	if err != nil || len(one) != 1 || one[0] != f.p.IDOf("deploy") {
		t.Fatalf("NewFinder ids = %v, err = %v", one, err)
	}
}

func TestSimilar는_find로_대신하고_이름에_밝힌다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Similar.Similar("/bench/app", bench.Draft{Kind: "fact", Title: "운영 배포는 main 병합으로 한다", Body: "origin/main 배포"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 || len(ids) > bench.SimilarLimit || ids[0] != f.p.IDOf("deploy") {
		t.Fatalf("ids = %v", ids)
	}
	n, ok := f.env.Similar.(interface{ Name() string })
	if !ok || n.Name() != hostpath.SimilarName || hostpath.SimilarName != "find-proxy" {
		t.Fatalf("similar name = %v", f.env.Similar)
	}
}

func TestCwd_밖의_폴더에서는_프로젝트_기억을_찾지_않는다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Finder.Find("/bench/other", "pnpm", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids, f.p.IDOf("pnpm")) {
		t.Fatalf("ids = %v", ids)
	}
}

// 바깥 일(자동 백업·정리)은 하지 않고, 실제 ORBIT_HOME에는 아무것도 쓰지 않는다.
func TestFactory는_홈에_아무것도_쓰지_않는다(t *testing.T) {
	f := open(t)
	if _, err := f.env.Recaller.Start("s4", "/bench/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Recaller.Request("s4", "/bench/app", "pnpm"); err != nil {
		t.Fatal(err)
	}
	if err := f.env.Close(); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(testHome)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 0 {
		t.Fatalf("ORBIT_HOME(%s)에 %v가 생겼다", testHome, names)
	}
	_ = filepath.Join
}

func mustSet(t *testing.T) *bench.Set {
	t.Helper()
	set, err := bench.Parse([]byte(setJSON))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

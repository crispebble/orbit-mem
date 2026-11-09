package legacy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// toolHome is the home the go tool runs with (its build and module caches), kept before the tests move HOME.
var toolHome string

func TestMain(m *testing.M) {
	// 시험이 이 PC의 실제 기억 DB나 홈을 열지 못하게 시험 전체에 임시 홈을 건다(범위 키가 경로에서 나오므로 HOME도 고정).
	tmp, err := os.MkdirTemp("", "orbit-mem-legacy-test-")
	if err != nil {
		panic(err)
	}
	toolHome = os.Getenv("HOME")
	os.Setenv("ORBIT_HOME", tmp)
	os.Setenv("HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// smallSet은 기본 시험의 인라인 묶음이다: 사용자·제품 상시 핵심, 이 앱 규칙, 공통 교훈, 도구 트리거, 다른 프로젝트 사실,
// 최근 결정. 제품 「shop」은 /bench/app 폴더다.
const smallSet = `{
  "now": "2026-11-03T09:00:00+09:00",
  "cwd": "/bench/app",
  "seed": "legacy-small",
  "products": {"shop": {"match": ["/bench/app"], "names": ["샵"]}},
  "memories": [
    {"key":"core-user","kind":"rule","scope":"global","quote":true,"pinned":true,"title":"커밋 메시지는 한글로 쓴다","body":"제목은 50자 안에서 쓴다.","topic":"커밋 메시지 언어","ageDays":20},
    {"key":"core-prod","kind":"decision","scope":"product:shop","quote":true,"pinned":true,"title":"샵 결제는 카드만 받는다","topic":"샵 결제 수단","ageDays":10},
    {"key":"rule-pnpm","kind":"rule","scope":"here","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다.","topic":"앱 패키지 설치","ageDays":40},
    {"key":"lesson-deploy","kind":"lesson","scope":"global","title":"운영 배포 전에는 백업 사본을 확인한다","ageDays":5},
    {"key":"tool-git","kind":"rule","scope":"global","quote":true,"title":"git push는 브랜치 이름을 보고 한다","tools":["Bash"],"topic":"푸시 전 확인","ageDays":3},
    {"key":"other-proj","kind":"fact","scope":"project:/bench/other","title":"운영 배포는 다른 저장소에서 한다","ageDays":1},
    {"key":"decision-x","kind":"decision","scope":"global","quote":true,"title":"기록 형식은 JSON으로 정한다","topic":"기록 형식","ageDays":2}
  ],
  "cases": [{"id":"c1","prompt":"아무 요청"}]
}`

func prepare(t *testing.T, src string) *bench.Prepared {
	t.Helper()
	set, err := bench.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func open(t *testing.T, p *bench.Prepared) (bench.Recaller, string) {
	t.Helper()
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	r, closeFn, err := legacy.New(db, p.Set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeFn(); err != nil {
			t.Error(err)
		}
	})
	return r, db
}

func ids(p *bench.Prepared, keys ...string) []string {
	out := []string{}
	for _, k := range keys {
		out = append(out, p.IDOf(k))
	}
	return out
}

func keysOf(p *bench.Prepared, s bench.Shown) []string {
	out := []string{}
	for _, id := range s.IDs {
		out = append(out, p.Keys[id])
	}
	return out
}

func check(t *testing.T, step string) func(bench.Shown, error) bench.Shown {
	return func(s bench.Shown, err error) bench.Shown {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if s.Chars != len([]rune(s.Text)) {
			t.Errorf("%s: Chars %d, text has %d", step, s.Chars, len([]rune(s.Text)))
		}
		if s.IDs == nil {
			t.Errorf("%s: IDs is nil", step)
		}
		return s
	}
}

func TestLegacy_이름은_고정_판을_밝힌다(t *testing.T) {
	r, _ := open(t, prepare(t, smallSet))
	if got := r.Name(); got != "legacy-87ab0fb" {
		t.Fatalf("Name %q", got)
	}
}

func TestLegacy_세션_시작은_상시_핵심과_최근_결정을_내고_다시_불러도_같은_묶음이다(t *testing.T) {
	p := prepare(t, smallSet)
	r, _ := open(t, p)
	s := check(t, "start")(r.Start("s1", "/bench/app"))
	if want := ids(p, "core-user", "core-prod", "decision-x"); !slices.Equal(s.IDs, want) {
		t.Fatalf("start %v, want %v", keysOf(p, s), []string{"core-user", "core-prod", "decision-x"})
	}
	again := check(t, "start again")(r.Start("s1", "/bench/app"))
	if again.Text != s.Text || !slices.Equal(again.IDs, s.IDs) {
		t.Fatalf("the same session gave another bundle:\n%s\n---\n%s", s.Text, again.Text)
	}
	// 다른 제품 폴더 밖이면 제품 핵심이 없다.
	other := check(t, "start other")(r.Start("s2", "/bench/other"))
	if slices.Contains(other.IDs, p.IDOf("core-prod")) || !slices.Contains(other.IDs, p.IDOf("core-user")) {
		t.Fatalf("start in /bench/other %v", keysOf(p, other))
	}
}

func TestLegacy_요청은_강하게_맞는_범위_안_기억을_내고_묶음에_든_핵심은_다시_넣지_않는다(t *testing.T) {
	p := prepare(t, smallSet)
	r, _ := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	s := check(t, "deploy")(r.Request("s1", "/bench/app", "운영 배포 전에 백업 사본 확인하는 순서"))
	if !slices.Equal(s.IDs, ids(p, "lesson-deploy")) {
		t.Fatalf("deploy %v, want [lesson-deploy] (other-proj is another project)", keysOf(p, s))
	}
	s = check(t, "pnpm")(r.Request("s1", "/bench/app", "pnpm으로 앱 설치하는 방법"))
	if !slices.Equal(s.IDs, ids(p, "rule-pnpm")) {
		t.Fatalf("pnpm %v", keysOf(p, s))
	}
	// 상시 핵심(묶음에 든 승격 기억)은 요청이 맞아도 다시 넣지 않는다.
	s = check(t, "core")(r.Request("s1", "/bench/app", "커밋 메시지 한글로 쓰는 규칙"))
	if slices.Contains(s.IDs, p.IDOf("core-user")) {
		t.Fatalf("the core came again %v", keysOf(p, s))
	}
	// 약하게 겹치는 요청은 아무것도 넣지 않는다.
	s = check(t, "weak")(r.Request("s1", "/bench/app", "오늘 날씨"))
	if len(s.IDs) != 0 || s.Text != "" {
		t.Fatalf("weak %v %q", keysOf(p, s), s.Text)
	}
}

func TestLegacy_도구_직전은_걸린_기억을_세션에_한_번만_낸다(t *testing.T) {
	p := prepare(t, smallSet)
	r, _ := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	s := check(t, "tool")(r.Tool("s1", "/bench/app", "Bash"))
	if !slices.Equal(s.IDs, ids(p, "tool-git")) {
		t.Fatalf("tool %v", keysOf(p, s))
	}
	s = check(t, "tool again")(r.Tool("s1", "/bench/app", "Bash"))
	if len(s.IDs) != 0 || s.Text != "" {
		t.Fatalf("the same session got the tool memory again %v", keysOf(p, s))
	}
	s = check(t, "tool other session")(r.Tool("s2", "/bench/app", "Bash"))
	if !slices.Equal(s.IDs, ids(p, "tool-git")) {
		t.Fatalf("another session %v", keysOf(p, s))
	}
	s = check(t, "tool none")(r.Tool("s1", "/bench/app", "Read"))
	if len(s.IDs) != 0 {
		t.Fatalf("Read %v", keysOf(p, s))
	}
}

func TestLegacy_위임은_위임_글을_보지_않고_사용자_상시_핵심만_낸다(t *testing.T) {
	p := prepare(t, smallSet)
	r, _ := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	s := check(t, "subagent")(r.Subagent("s1", "/bench/app", "운영 배포 전에 백업 사본 확인하는 순서를 정리해"))
	if !slices.Equal(s.IDs, ids(p, "core-user")) {
		t.Fatalf("subagent %v, want [core-user] only", keysOf(p, s))
	}
	// 옛 위임은 핵심뿐이라 모두 핵심 제목 줄 몫이고 회상 몫은 없다.
	if !slices.Equal(s.Lead, s.IDs) || s.LeadChars != s.Chars || s.Chars == 0 {
		t.Fatalf("subagent lead %v (%d of %d chars), want all of it", keysOf(p, bench.Shown{IDs: s.Lead}), s.LeadChars, s.Chars)
	}
}

func TestLegacy_새_기억_줄은_세션에_한_번만_나온다(t *testing.T) {
	p := prepare(t, smallSet)
	r, db := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	// 세션이 시작한 뒤 다른 곳에서 확정된 기억(같은 DB에 다른 연결로 넣는다).
	newID := saveLater(t, db, p.Set, memory.SaveInput{Kind: memory.KindRule, Scope: memory.ScopeGlobal, Title: "릴리스 노트는 사용자 말로 쓴다",
		State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "릴리스 노트는 사용자 말로 쓴다"}})
	first := check(t, "first")(r.Request("s1", "/bench/app", "아무 관련 없는 질문 하나"))
	if !slices.Contains(first.IDs, newID) {
		t.Fatalf("the new memory line is missing: %v\n%s", first.IDs, first.Text)
	}
	second := check(t, "second")(r.Request("s1", "/bench/app", "아무 관련 없는 질문 하나"))
	if slices.Contains(second.IDs, newID) {
		t.Fatalf("the new memory line came twice: %v", second.IDs)
	}
}

func TestLegacy_원본_DB는_쓰지_않고_사본에만_세션을_남긴다(t *testing.T) {
	// 원본: 다른 DB 파일. bench.Prepare가 읽기 전용으로 복사하고 legacy는 Prepared.Copy만 연다.
	base := prepare(t, smallSet)
	src, err := base.Copy()
	if err != nil {
		t.Fatal(err)
	}
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c1","prompt":"x y"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	before := sums(t, src)
	r, db := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	check(t, "tool")(r.Tool("s1", "/bench/app", "Bash"))
	if after := sums(t, src); !slices.Equal(before, after) {
		t.Fatalf("the source DB changed: %v → %v", before, after)
	}
	s, _, err := memory.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, ok := s.SessionBundle(legacy.Host, "s1"); !ok {
		t.Fatal("the copy has no session bundle")
	}
	if len(s.SessionMarks(legacy.Host, "s1", memory.MarkTool)) != 1 {
		t.Fatal("the copy has no tool mark")
	}
}

// sums is the sha256 of a DB and its -wal and -shm ("-" when a file is missing).
func sums(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		f, err := os.Open(path + suffix)
		if os.IsNotExist(err) {
			out = append(out, "-")
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, hex.EncodeToString(h.Sum(nil)))
	}
	return out
}

// 87ab0fb는 같은 시각에 바뀐 분야 기억을 map 순서로 보였다(internal/inject/inject.go:648). 사본은 id 순으로 정해
// 기준선이 실행마다 같다.
func TestLegacy_같은_시각의_분야_신호는_id_순으로_늘_같다(t *testing.T) {
	const set = `{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c1","prompt":"x y"}],"memories":[
	  {"key":"a","kind":"fact","scope":"global","title":"접근성 점검: 대비는 충분히 둔다","tags":["분야:접근성"],"ageDays":39},
	  {"key":"b","kind":"fact","scope":"global","title":"접근성 점검: 키보드로 모두 쓴다","tags":["분야:접근성"],"ageDays":39},
	  {"key":"c","kind":"fact","scope":"global","title":"접근성 점검: 그림에 대체 글","tags":["분야:접근성"],"ageDays":39},
	  {"key":"d","kind":"fact","scope":"global","title":"접근성 점검: 움직임 줄이기","tags":["분야:접근성"],"ageDays":10}]}`
	p := prepare(t, set)
	r, _ := open(t, p)
	check(t, "start")(r.Start("s1", "/bench/app"))
	want := ids(p, "d", "a", "b", "c")
	for i := range 30 {
		s := check(t, "request")(r.Request("s1", "/bench/app", "접근성 점검 순서"))
		if !slices.Equal(s.IDs, want) {
			t.Fatalf("run %d: %v, want [d a b c]", i, keysOf(p, s))
		}
	}
}

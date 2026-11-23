package hostpath_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 재생의 세션 상태(Task 01b): 실행은 사본에 남아 있던 세션 상태를 실행 전에 한 번 비우고, 재생이 스스로 만든 묶음·표시는
// 그 세션의 뒤 사례까지 이어진다. 그래서 0.19.0이 얼린 묶음을 같은 세션의 뒤 시작에 다시 준 일(폐기 주입)이 재생에도 나온다.

// history is a memory DB with history written at fixed times: the times are the versions a replay rewinds to.
type history struct {
	t   *testing.T
	s   *memory.Store
	now time.Time
}

func newHistory(t *testing.T, src string) *history {
	t.Helper()
	s, err := memory.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	h := &history{t: t, s: s}
	s.Now = func() time.Time { return h.now }
	return h
}

// pinned saves a confirmed rule of scope at the time and makes it core (the session bundle of recall auto holds the
// core only).
func (h *history) pinned(at, scope, title string) string {
	h.t.Helper()
	h.at(at)
	m, err := h.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: title, Scope: scope,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.s.Pin(m.ID, memory.Who{}); err != nil {
		h.t.Fatal(err)
	}
	return m.ID
}

func (h *history) stale(at, id string) {
	h.t.Helper()
	h.at(at)
	if _, err := h.s.SetState(id, memory.StateStale, "낡음"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *history) at(rfc string) {
	h.t.Helper()
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		h.t.Fatal(err)
	}
	h.now = t
}

func (h *history) close() { h.s.Close() }

// replay runs the host path over the cases on a copy of src.
func replay(t *testing.T, src string, set *bench.Set) *bench.Report {
	t.Helper()
	p, err := bench.Prepare(set, src)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, hostpath.Factory)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func parseSet(t *testing.T, s string) *bench.Set {
	t.Helper()
	set, err := bench.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// starts are the session starts of a run by case id (the start cases).
func starts(run bench.RunReport) map[string]*bench.Seen {
	out := map[string]*bench.Seen{}
	for _, c := range run.Cases {
		if c.Start != nil {
			out[c.ID] = c.Start
		}
	}
	return out
}

func caseOf(run bench.RunReport, id string) bench.CaseResult {
	for _, c := range run.Cases {
		if c.ID == id {
			return c
		}
	}
	return bench.CaseResult{}
}

// 제품 지도: 재생 사례의 작업 폴더가 제품 app이다. 지도가 없으면 제품 핵심이 묶음에 들 수 없다.
const replayProducts = `"products":{"app":{"match":["/bench/app"]}}`

// 한 세션의 시작이 만든 묶음은 그 세션의 뒤 시작(재개)에 다시 전달된다. 그 사이 새로 올린 핵심은 그 시각에 새로 만든 묶음
// (다른 세션)에는 들지만 재개 시작에는 없다: 같은 id·같은 글자는 다시 계산한 것이 아니라 얼린 묶음이다. 사례 파일의 제품
// 지도로 제품 핵심도 묶음에 든다.
func TestReplay_세션의_묶음은_뒤_시작_사례에_재전달된다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "memory.db")
	h := newHistory(t, src)
	core := h.pinned("2026-10-20T09:00:00Z", memory.ScopeGlobal, "답은 존댓말로 한다")
	prod := h.pinned("2026-10-20T09:00:00Z", "product:app", "앱 배포는 화요일 오전에만 한다")
	later := h.pinned("2026-10-24T09:00:00Z", memory.ScopeGlobal, "보고는 결론부터 쓴다")
	h.close()
	set := parseSet(t, `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s",`+replayProducts+`,"cases":[
	 {"id":"s1-001","session":"s1","kind":"start","source":"startup","at":"2026-10-22T09:00:00Z"},
	 {"id":"s1-002","session":"s1","prompt":"배포 창 알려 줘","at":"2026-10-22T09:01:00Z"},
	 {"id":"s1-003","session":"s1","kind":"start","source":"resume","at":"2026-10-26T09:00:00Z"},
	 {"id":"s2-001","session":"s2","kind":"start","source":"startup","at":"2026-10-26T09:00:30Z"}]}`)
	run := replay(t, src, set).Runs[0]
	got := starts(run)
	first, resumed, other := got["s1-001"], got["s1-003"], got["s2-001"]
	if first == nil || resumed == nil || other == nil {
		t.Fatalf("starts %v, want the three start cases", got)
	}
	if !slices.Contains(first.IDs, core) || !slices.Contains(first.IDs, prod) || slices.Contains(first.IDs, later) {
		t.Fatalf("first start %v, want the user core %s and the product core %s, not %s", first.IDs, core, prod, later)
	}
	if !slices.Contains(other.IDs, later) {
		t.Fatalf("a bundle made at the resume's time %v holds %s: the test cannot tell a redelivery from a rebuild", other.IDs, later)
	}
	if !slices.Equal(resumed.IDs, first.IDs) || resumed.Chars != first.Chars {
		t.Fatalf("resumed start %v (%d chars), want the session's bundle again %v (%d chars)", resumed.IDs, resumed.Chars, first.IDs, first.Chars)
	}
	if st := run.All.Paths.Start; st.Calls != 3 || st.Dead != 0 {
		t.Fatalf("start path %+v, want 3 starts with nothing dead", st)
	}
}

// 묶음에 얼린 핵심이 재개 전에 낡는다. 묶음을 그대로 다시 주는 제품(0.19.0)이면 재개 시작의 그 id가 start 경로의 Dead(까닭
// 낡음)로 세어진다. 묶음 안 기억의 보임이 바뀌면 다시 만드는 제품(명세 F-1)이면 그 id는 묶음에서 빠지고 Dead가 없으며, 뒤
// 요청이 그 기억을 세션 소식(정정·무효 줄)으로 알린다. 이 시험은 두 버전 모두에서 돈다: 어느 쪽인지는 재개 시작이 그 id를
// 실었는지로 가르고(로그에 남긴다), 어느 쪽이든 실린 낡은 id는 Dead로 센다.
func TestReplay_묶음에_얼린_기억이_낡으면_재전달이_Dead로_세어진다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "memory.db")
	h := newHistory(t, src)
	core := h.pinned("2026-10-20T09:00:00Z", memory.ScopeGlobal, "답은 존댓말로 한다")
	h.stale("2026-10-25T09:00:00Z", core)
	h.close()
	set := parseSet(t, `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s","cases":[
	 {"id":"s1-001","session":"s1","kind":"start","source":"startup","at":"2026-10-22T09:00:00Z"},
	 {"id":"s1-002","session":"s1","kind":"start","source":"resume","at":"2026-10-26T09:00:00Z"},
	 {"id":"s1-003","session":"s1","prompt":"배포 창은 언제야 알려 줘","at":"2026-10-26T09:01:00Z"}]}`)
	// 무효 줄은 기억을 싣지 않아 보고의 id에 없다(inject.NewsShare): 요청 훅의 글을 따로 받아 둔다.
	texts := map[string]string{}
	p, err := bench.Prepare(set, src)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, func(db string, s *bench.Set) (bench.Env, error) {
		env, err := hostpath.Factory(db, s)
		env.Recaller = textOf{env.Recaller, texts}
		return env, err
	})
	if err != nil {
		t.Fatal(err)
	}
	run := rep.Runs[0]
	got := starts(run)
	first, resumed := got["s1-001"], got["s1-002"]
	if first == nil || resumed == nil || !slices.Contains(first.IDs, core) || len(first.Dead) != 0 {
		t.Fatalf("first start %+v, want the core in it while it was current", first)
	}
	st := run.All.Paths.Start
	if slices.Contains(resumed.IDs, core) {
		t.Log("the product hands the frozen bundle back: the stale core is a dead injection of the start path")
		if !slices.Equal(resumed.Dead, []string{core}) || resumed.DeadWhy[core] != bench.DeadStale {
			t.Fatalf("resumed start %+v, want %s dead as %s", resumed, core, bench.DeadStale)
		}
		if st.Dead != 1 || st.DeadCalls != 1 || st.DeadByWhy[bench.DeadStale] != 1 {
			t.Fatalf("start path %+v, want the one dead injection counted", st)
		}
		return
	}
	t.Log("the product makes the bundle anew without the stale core: nothing dead, the request tells the core void")
	if len(resumed.Dead) != 0 || st.Dead != 0 || st.DeadCalls != 0 {
		t.Fatalf("resumed start %+v, start path %+v, want nothing dead", resumed, st)
	}
	req := caseOf(run, "s1-003").Request
	if req == nil || req.NewsChars == 0 || !strings.Contains(texts["배포 창은 언제야 알려 줘"], core) {
		t.Fatalf("request after the resume %+v, text %q, want the session news to name %s", req, texts, core)
	}
}

// textOf keeps the text of every request recall by its prompt.
type textOf struct {
	bench.Recaller
	texts map[string]string
}

func (r textOf) Request(session, cwd, prompt string) (bench.Shown, error) {
	s, err := r.Recaller.Request(session, cwd, prompt)
	r.texts[prompt] = s.Text
	return s, err
}

// 사본에 실제 세션이 남긴 묶음(재생과 같은 호스트·세션 id)이 있어도 재생의 첫 시작은 그것을 받지 않는다: 실행 전에 한 번
// 비운다. 비우기는 사례마다가 아니라 실행 전 한 번이라, 재생이 만든 묶음은 같은 세션의 뒤 시작까지 이어진다. 원본은 그대로다.
func TestReplay_실행_전_세션_상태는_섞이지_않는다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "memory.db")
	h := newHistory(t, src)
	core := h.pinned("2026-10-20T09:00:00Z", memory.ScopeGlobal, "답은 존댓말로 한다")
	h.at("2026-10-20T09:00:00Z")
	planted, err := h.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포 창은 화요일 오전이다", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포 창은 화요일 오전이다"}})
	if err != nil {
		t.Fatal(err)
	}
	// 재생 경로의 호스트 이름은 "bench"다(hostpath). 사본이 이미 가진 세션 상태(같은 호스트·세션 id로 남은 묶음과 표시)를 흉내 낸다.
	if err := h.s.SaveSessionBundle("bench", "s1", "얼린 묶음 "+planted.ID, []string{planted.ID}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.MarkSession("bench", "s1", memory.MarkShown, core); err != nil {
		t.Fatal(err)
	}
	// 첫 시작 뒤에 올린 핵심: 다시 만든 묶음에는 들고, 이어진 묶음에는 없다.
	later := h.pinned("2026-10-22T12:00:00Z", memory.ScopeGlobal, "보고는 결론부터 쓴다")
	h.close()
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	set := parseSet(t, `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s","cases":[
	 {"id":"s1-001","session":"s1","kind":"start","source":"resume","at":"2026-10-22T09:00:00Z"},
	 {"id":"s1-002","session":"s1","prompt":"배포 창 알려 줘","at":"2026-10-22T09:01:00Z"},
	 {"id":"s1-003","session":"s1","kind":"start","source":"resume","at":"2026-10-23T09:00:00Z"}]}`)
	run := replay(t, src, set).Runs[0]
	got := starts(run)
	first, resumed := got["s1-001"], got["s1-003"]
	if first == nil || slices.Contains(first.IDs, planted.ID) || !slices.Contains(first.IDs, core) {
		t.Fatalf("first start %+v, want the bundle of this run (with %s), not the one left in the copy (%s)", first, core, planted.ID)
	}
	if resumed == nil || !slices.Equal(resumed.IDs, first.IDs) || resumed.Chars != first.Chars || slices.Contains(resumed.IDs, later) {
		t.Fatalf("resumed start %+v, want the bundle this run made for the session again %v (without %s)", resumed, first.IDs, later)
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Fatal("the source DB changed")
	}
}

// 같은 사본·사례로 두 번 돌리면 보고가 바이트까지 같다. 사례를 세션과 종류로 걸러(bench.Set.Keep) 돌려도 같고, 시작만 남겨
// 돌린 start 경로는 전체 사례로 돌린 그 세션의 시작과 같은 묶음을 같은 차례로 낸다(시작 사례가 없는 세션의 첫 시작도 그
// 시각에 선다).
func TestReplay_같은_입력이면_결과가_같다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "memory.db")
	h := newHistory(t, src)
	core := h.pinned("2026-10-20T09:00:00Z", memory.ScopeGlobal, "답은 존댓말로 한다")
	h.pinned("2026-10-20T09:00:00Z", "product:app", "앱 배포는 화요일 오전에만 한다")
	h.stale("2026-10-25T09:00:00Z", core)
	h.close()
	const cases = `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s",` + replayProducts + `,"cases":[
	 {"id":"s1-001","session":"s1","prompt":"배포 창 알려 줘","at":"2026-10-22T09:00:00Z"},
	 {"id":"s1-002","session":"s1","tool":"Bash","input":"git status","at":"2026-10-22T09:01:00Z"},
	 {"id":"s1-003","session":"s1","kind":"start","source":"compact","at":"2026-10-23T09:00:00Z"},
	 {"id":"s1-004","session":"s1","kind":"start","source":"resume","at":"2026-10-26T09:00:00Z"},
	 {"id":"s1-005","session":"s1","prompt":"배포 창은 언제야","at":"2026-10-26T09:01:00Z"},
	 {"id":"s2-001","session":"s2","kind":"start","source":"startup","at":"2026-10-22T10:00:00Z"},
	 {"id":"s2-002","session":"s2","prompt":"테스트 돌려 줘","at":"2026-10-22T10:01:00Z"}]}`
	once := func(keep func(*bench.Set)) (*bench.Report, string) {
		set := parseSet(t, cases)
		if keep != nil {
			keep(set)
		}
		rep := replay(t, src, set)
		b, err := rep.JSON()
		if err != nil {
			t.Fatal(err)
		}
		return rep, string(b)
	}
	full, a := once(nil)
	if _, b := once(nil); a != b {
		t.Fatal("the same copy and cases gave two reports")
	}
	startsOnly := func(set *bench.Set) {
		if _, err := set.Keep([]string{"s1"}, []string{bench.KindStart}); err != nil {
			t.Fatal(err)
		}
	}
	kept, c := once(startsOnly)
	if _, d := once(startsOnly); c != d {
		t.Fatal("the same kept cases gave two reports")
	}
	var want, gotIDs [][]string
	for _, s := range full.Runs[0].Sessions {
		if s.Session == "s1" {
			want = append(want, s.IDs)
		}
	}
	for _, id := range []string{"s1-003", "s1-004"} {
		want = append(want, starts(full.Runs[0])[id].IDs)
	}
	for _, c := range kept.Runs[0].Cases {
		if c.Start != nil {
			gotIDs = append(gotIDs, c.Start.IDs)
		}
	}
	if len(gotIDs) != len(want) {
		t.Fatalf("kept starts %v, want %v", gotIDs, want)
	}
	for i := range want {
		if !slices.Equal(gotIDs[i], want[i]) {
			t.Fatalf("kept start %d: %v, want %v as in the full run", i, gotIDs[i], want[i])
		}
	}
	fs, ks := full.Runs[0].All.Paths.Start, kept.Runs[0].All.Paths.Start
	if ks.Calls != 3 || ks.Dead != fs.Dead-deadOf(full.Runs[0], "s2") {
		t.Fatalf("kept start path %+v, full %+v, want the 3 starts of s1 with the same dead", ks, fs)
	}
}

// 시작의 출처는 호스트 계약의 source 칸으로 간다: 계약이 아는 출처는 받고, 모르는 출처는 계약의 검사에 걸린다. 비면
// 새 세션(startup)이다.
func TestStartFrom_시작_출처를_호스트_계약에_싣는다(t *testing.T) {
	f := open(t)
	s, ok := f.env.Recaller.(bench.SourcedStarter)
	if !ok {
		t.Fatal("the host path takes the source of a start")
	}
	for _, src := range []string{"", "startup", "resume", "fork", "compact", "clear"} {
		if _, err := s.StartFrom("s-"+src, "/bench/app", src); err != nil {
			t.Fatalf("source %q: %v", src, err)
		}
	}
	if _, err := s.StartFrom("s1", "/bench/app", "later"); err == nil {
		t.Fatal("a source the contract does not know must reach its check")
	}
}

// deadOf is the dead ids of the starts of a session in a run.
func deadOf(run bench.RunReport, session string) int {
	n := 0
	for _, s := range run.Sessions {
		if s.Session == session {
			n += len(s.Dead)
		}
	}
	for _, c := range run.Cases {
		if c.Session == session && c.Start != nil && c.ID != session+"-001" {
			n += len(c.Start.Dead)
		}
	}
	return n
}

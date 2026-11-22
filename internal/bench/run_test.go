package bench_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

func TestRun은_세션마다_Start를_먼저_부르고_사례는_파일_순서대로_돈다(t *testing.T) {
	_, calls := runFake(t, `
	 {"id":"c1","session":"s1","prompt":"p1"},
	 {"id":"c2","session":"s2","prompt":"p2"},
	 {"id":"c3","session":"s1","tool":"t3"},
	 {"id":"c4","prompt":"p4"},
	 {"id":"c5","session":"s2","kind":"subagent","task":"T5"}`, script{}, keyed{})
	want := []string{"start:s1", "req:p1", "start:s2", "req:p2", "tool:t3", "start:c4", "req:p4", "sub:T5"}
	if !slices.Equal(*calls, want) {
		t.Errorf("calls\n got %v\nwant %v", *calls, want)
	}
}

func TestRun은_회상마다_새_DB_사본에서_연다(t *testing.T) {
	p := prepare(t, `{"id":"c1","session":"s1","prompt":"p1"}`)
	var paths []string
	var closed int
	f := func(db string, set *bench.Set) (bench.Env, error) {
		if set != p.Set {
			t.Error("the factory gets the set of the run")
		}
		s, info, err := memory.OpenReadOnly(db)
		if err != nil || info.State != memory.SchemaOK {
			t.Fatalf("copy does not open: %v %v", info.State, err)
		}
		list, _ := s.List()
		s.Close()
		if len(list) != 10 {
			t.Errorf("copy has %d memories, want 10", len(list))
		}
		paths = append(paths, db)
		var calls []string
		return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, &calls, nil}, Close: func() error { closed++; return nil }}, nil
	}
	if _, err := bench.Run(p, f, f); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Errorf("paths %v: every recall path gets its own copy", paths)
	}
	if closed != 2 {
		t.Errorf("closed %d, want 2", closed)
	}
	for _, db := range paths {
		if _, err := os.Stat(db); err == nil {
			t.Errorf("copy %s is left behind", db)
		}
	}
	if filepath.Dir(paths[0]) == "" {
		t.Error("no dir")
	}
}

func TestRun은_회상이_오류를_내면_사례_id와_함께_멈춘다(t *testing.T) {
	p := prepare(t, `{"id":"c1","session":"s1","prompt":"p1"},{"id":"c2","session":"s1","kind":"duplicate","save":{"kind":"fact","title":"x"}}`)
	f := func(db string, set *bench.Set) (bench.Env, error) {
		var calls []string
		return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, &calls, nil}}, nil // Similar가 없다
	}
	if _, err := bench.Run(p, f); err == nil {
		t.Fatal("duplicate cases need a Similar")
	}
}

// 재개(resume)는 같은 세션의 세션 시작을 다시 부른다. 재생은 그 사례(종류 start)마다 Start를 다시 불러야 얼린 묶음의
// 재전달을 잴 수 있다. 세션의 첫 사례가 start면 그것이 그 세션의 시작이다(두 번 부르지 않는다).
func TestRun_같은_세션의_두번째_시작_사례는_시작_회상을_다시_부른다(t *testing.T) {
	rep, calls := runFake(t, `
	 {"id":"c1","session":"s1","kind":"start"},
	 {"id":"c2","session":"s1","prompt":"p1"},
	 {"id":"c3","session":"s1","kind":"start","source":"resume"},
	 {"id":"c4","session":"s1","tool":"Bash","input":"git status"},
	 {"id":"c5","session":"s2","prompt":"p2"}`, script{"start:s1": {[]string{"a"}, 30}}, keyed{})
	want := []string{"start:s1", "req:p1", "start:s1", "tool:Bash(git status)", "start:s2", "req:p2"}
	if !slices.Equal(*calls, want) {
		t.Fatalf("calls\n got %v\nwant %v", *calls, want)
	}
	run := rep.Runs[0]
	if len(run.Sessions) != 2 || run.Sessions[0].Session != "s1" || run.Sessions[0].Chars != 30 {
		t.Errorf("sessions %+v: one start per session, the first", run.Sessions)
	}
	for _, c := range run.Cases {
		switch c.ID {
		case "c1", "c3":
			if c.Kind != bench.KindStart || c.Start == nil || c.Start.Chars != 30 {
				t.Errorf("case %s: %+v, want the start it called", c.ID, c)
			}
		default:
			if c.Start != nil {
				t.Errorf("case %s has a start: %+v", c.ID, c.Start)
			}
		}
	}
	// 시작 사례는 요청 묶음에 들지 않는다.
	if m := run.All.Requests; m.Cases != 3 || m.Empty.Den != 3 {
		t.Errorf("requests %d (empty den %d), want the 3 request cases only", m.Cases, m.Empty.Den)
	}
	if b := rep.Split.Test; b.Answered+b.Empty+b.Subagent+b.Duplicate > 3 {
		t.Errorf("bundles %+v count a start case", b)
	}
}

// 압축(compact)·지우기(clear) 뒤 시작은 훅이 표시를 먼저 비우고(reset) 시작 회상을 부른다(hook/claudecode). 재생도 같은
// 차례로 부른다. 비울 수 없는 회상 경로로 그런 사례를 돌리면 조용히 넘기지 않고 멈춘다.
func TestRun_압축과_지우기_시작_사례는_표시를_비우고_시작_회상을_부른다(t *testing.T) {
	_, calls := runFake(t, `
	 {"id":"c1","session":"s1","kind":"start","source":"startup"},
	 {"id":"c2","session":"s1","prompt":"p1"},
	 {"id":"c3","session":"s1","kind":"start","source":"compact"},
	 {"id":"c4","session":"s1","kind":"start","source":"clear"},
	 {"id":"c5","session":"s2","kind":"start","source":"compact"}`, script{}, keyed{})
	want := []string{"start:s1", "req:p1", "reset:s1", "start:s1", "reset:s1", "start:s1", "reset:s2", "start:s2"}
	if !slices.Equal(*calls, want) {
		t.Fatalf("calls\n got %v\nwant %v", *calls, want)
	}
	p := prepare(t, `{"id":"c1","session":"s1","kind":"start","source":"compact"}`)
	f := func(db string, set *bench.Set) (bench.Env, error) {
		var calls []string
		return bench.Env{Recaller: noReset{fake{"r", script{}, nil, p.IDOf, &calls, nil}}}, nil
	}
	if _, err := bench.Run(p, f); err == nil {
		t.Fatal("a compact start on a path that cannot reset must stop the run")
	}
	for _, bad := range []string{
		`{"id":"x","session":"s1","kind":"start","source":"later"}`,
		`{"id":"x","session":"s1","prompt":"p","source":"resume"}`,
		`{"id":"x","session":"s1","kind":"start","prompt":"p"}`,
		`{"id":"x","session":"s1","kind":"start","expect":["a"]}`,
	} {
		if _, err := bench.Parse([]byte(setJSON(bad))); err == nil {
			t.Errorf("parse %s: want an error", bad)
		}
	}
}

// noReset is a recall path without Reset.
type noReset struct{ f fake }

func (n noReset) Name() string                                   { return n.f.Name() }
func (n noReset) Start(s, cwd string) (bench.Shown, error)       { return n.f.Start(s, cwd) }
func (n noReset) Request(s, cwd, p string) (bench.Shown, error)  { return n.f.Request(s, cwd, p) }
func (n noReset) Tool(s, cwd, t, in string) (bench.Shown, error) { return n.f.Tool(s, cwd, t, in) }
func (n noReset) Subagent(s, cwd, t string) (bench.Shown, error) { return n.f.Subagent(s, cwd, t) }

// 재생 사본에는 실제 세션이 남긴 세션 묶음·세션 표시·위임 글이 있다. 그대로 두면 재생 세션 id가 같을 때 얼린 묶음을
// 다시 받아, 재생이 잰 주입이 그 실행의 것이 아니게 된다. 실행은 사본을 열기 전에 그 표를 비운다. 원본 DB는 그대로다.
func TestRun_재생_사본은_세션_묶음과_표시를_비우고_시작한다(t *testing.T) {
	fixHome(t)
	src := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포 창은 화요일", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceSession, Ref: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSessionBundle("claude-code", "s1", "얼린 묶음", []string{m.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSession("claude-code", "s1", memory.MarkShown, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDelegation("claude-code", "s1", "worker", "맡긴 일"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c1","session":"s1","prompt":"배포"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, src)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	bundles, marks := -1, -1
	f := func(db string, set *bench.Set) (bench.Env, error) {
		c, err := sqlitedb.OpenWith(db, sqlitedb.Options{ReadOnly: true})
		if err != nil {
			return bench.Env{}, err
		}
		defer c.Close()
		// 세션 묶음 표에는 위임 글도 있다(memory/delegation.go): 줄 수가 0이면 둘 다 없다.
		if err := c.QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&bundles); err != nil {
			return bench.Env{}, err
		}
		if err := c.QueryRow(`SELECT COUNT(*) FROM session_marks`).Scan(&marks); err != nil {
			return bench.Env{}, err
		}
		var calls []string
		return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, &calls, nil}}, nil
	}
	if _, err := bench.Run(p, f); err != nil {
		t.Fatal(err)
	}
	if bundles != 0 || marks != 0 {
		t.Fatalf("the run copy kept the session state: bundles %d marks %d", bundles, marks)
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Fatal("the source DB changed")
	}
}

// sourced is the fake with bench.SourcedStarter: it keeps the source of every start it gets.
type sourced struct {
	fake
	sources *[]string
}

func (s sourced) StartFrom(session, cwd, source string) (bench.Shown, error) {
	*s.sources = append(*s.sources, session+":"+source)
	return s.fake.Start(session, cwd)
}

// 시작 사례의 출처(startup·resume·compact·clear 등)는 기록대로 시작 회상에 실린다. 기록된 시작이 없는 세션의 첫 시작은
// 출처가 비어 있다. 출처를 받는 경로가 아니면 지금처럼 Start를 부른다.
func TestRun_시작_사례의_출처를_시작_회상에_넘긴다(t *testing.T) {
	p := prepare(t, `
	 {"id":"c1","session":"s1","kind":"start","source":"resume"},
	 {"id":"c2","session":"s1","prompt":"p1"},
	 {"id":"c3","session":"s1","kind":"start","source":"compact"},
	 {"id":"c4","session":"s2","prompt":"p2"},
	 {"id":"c5","session":"s2","kind":"start","source":"clear"}`)
	var calls, sources []string
	f := func(db string, set *bench.Set) (bench.Env, error) {
		return bench.Env{Recaller: sourced{fake{"r", script{}, nil, p.IDOf, &calls, nil}, &sources}}, nil
	}
	if _, err := bench.Run(p, f); err != nil {
		t.Fatal(err)
	}
	want := []string{"s1:resume", "s1:compact", "s2:", "s2:clear"}
	if !slices.Equal(sources, want) {
		t.Fatalf("sources %v, want %v", sources, want)
	}
	if want := []string{"start:s1", "req:p1", "reset:s1", "start:s1", "start:s2", "req:p2", "reset:s2", "start:s2"}; !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

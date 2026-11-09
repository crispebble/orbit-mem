package bench_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
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
		return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, &calls}, Close: func() error { closed++; return nil }}, nil
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
		return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, &calls}}, nil // Similar가 없다
	}
	if _, err := bench.Run(p, f); err == nil {
		t.Fatal("duplicate cases need a Similar")
	}
}

package paths_test

import (
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

const changing = `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s",
 "memories":[{"key":"a","kind":"decision","scope":"global","quote":true,"title":"Retention is seven days","topic":"retention"}],
 "cases":[{"id":"c1","session":"s1","kind":"start","source":"startup"},
  {"id":"c2","session":"s1","kind":"start","source":"resume","changes":[{"memory":"a","state":"stale"}]},
  {"id":"c3","session":"s1","prompt":"how many days is retention","expect":[],"forbid":["a"]}]}`

// 옛 회상도 사례의 changes를 사본에 적용한다. 건너뛰면 낡은 기억이 그대로 나와 그 사례가 「틀림」으로 굳는다.
func TestLegacy는_사례의_changes를_사본에_적용한다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORBIT_HOME", t.TempDir())
	set, err := bench.Parse([]byte(changing))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fs, err := paths.Factories([]string{paths.Legacy})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := bench.Run(p, fs...)
	if err != nil {
		t.Fatalf("legacy cannot run a case with changes: %v", err)
	}
	for _, c := range rep.Runs[0].Cases {
		if c.ID != "c3" {
			continue
		}
		if c.Request == nil {
			t.Fatal("c3 has no request result")
		}
		if a := p.IDOf("a"); slices.Contains(c.Request.IDs, a) {
			t.Errorf("legacy recalled %s after the change moved it to stale: %v", a, c.Request.IDs)
		}
		return
	}
	t.Fatal("no result for c3")
}

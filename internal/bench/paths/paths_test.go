package paths_test

import (
	"errors"
	"os"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

const small = `{"now":"2026-11-01T00:00:00Z","cwd":"/bench/app","seed":"s",
 "memories":[{"key":"a","kind":"rule","scope":"global","quote":true,"title":"Use tabs","topic":"style","body":"Use tabs in Go files.","tools":["Edit"]}],
 "cases":[{"id":"c1","session":"s1","prompt":"how should I indent Go files","expect":["a"]},
  {"id":"c2","session":"s1","tool":"Edit"},
  {"id":"c3","session":"s1","kind":"subagent","task":"indent the files"},
  {"id":"c4","kind":"duplicate","save":{"kind":"rule","title":"Use tabs"},"expect":["a"]}]}`

func prepared(t *testing.T) *bench.Prepared {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORBIT_HOME", t.TempDir())
	set, err := bench.Parse([]byte(small))
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

func TestFactories는_이름대로_조립하고_겹친_이름은_한_번만_쓴다(t *testing.T) {
	fs, err := paths.Factories([]string{paths.Current, " legacy ", paths.Current})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := bench.Run(prepared(t), fs...)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rep.Runs {
		names = append(names, r.Name)
	}
	if len(names) != 2 || names[0] != hostpath.Name || names[1] != legacy.Name {
		t.Errorf("runs %v", names)
	}
}

func TestFactories는_모르는_이름을_거절한다(t *testing.T) {
	for _, n := range []string{"nope", ""} {
		_, err := paths.Factories([]string{paths.Current, n})
		var u paths.UnknownError
		if !errors.As(err, &u) || u.Name != n {
			t.Errorf("%q: err = %v", n, err)
		}
	}
}

// 두 회상은 저마다 찾기·비슷한 것 찾기를 갖추고(옛 회상은 87ab0fb 것을 얼린 사본), 뻔한 요청에는 같은 기억을 먼저 찾는다.
// Close는 연 것을 다 닫는다: 닫은 뒤 사본 옆에 -wal·-shm이 남지 않는다.
func TestFactories의_Env는_찾기를_갖추고_Close가_연_것을_다_닫는다(t *testing.T) {
	p := prepared(t)
	fs, err := paths.Factories(paths.Names)
	if err != nil {
		t.Fatal(err)
	}
	var found [][]string
	for _, f := range fs {
		db, err := p.Copy()
		if err != nil {
			t.Fatal(err)
		}
		env, err := f(db, p.Set)
		if err != nil {
			t.Fatal(err)
		}
		if env.Recaller == nil || env.Finder == nil || env.Similar == nil || env.Close == nil {
			t.Fatalf("env %+v", env)
		}
		if _, err := env.Recaller.Start("s1", "/bench/app"); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Recaller.Request("s1", "/bench/app", "how should I indent Go files"); err != nil {
			t.Fatal(err)
		}
		ids, err := env.Finder.Find("/bench/app", "indent Go files tabs", bench.FindLimit)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, ids)
		if _, err := env.Similar.Similar("/bench/app", bench.Draft{Kind: "rule", Title: "Use tabs"}); err != nil {
			t.Fatal(err)
		}
		if err := env.Close(); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(db + suffix); err == nil {
				t.Errorf("%s: %s is left after Close (a connection is still open)", env.Recaller.Name(), suffix)
			}
		}
	}
	if a := p.IDOf("a"); len(found) != 2 || len(found[0]) == 0 || len(found[1]) == 0 || found[0][0] != a || found[1][0] != a {
		t.Errorf("both paths find the one memory first: %v", found)
	}
}

// The frozen recall opens nothing of the current path: its search and similar search are frozen
// too (87ab0fb memory_find), so a change of the current code cannot move the legacy baseline.
// Close closes what it opened, once.
func TestLegacy는_지금_경로를_열지_않고_자기가_연_것만_닫는다(t *testing.T) {
	p := prepared(t)
	opened, closed := map[string]int{}, map[string]int{}
	counting := func(name string, env bench.Env, err error) (bench.Env, error) {
		opened[name]++
		if err != nil {
			return env, err
		}
		real := env.Close
		env.Close = func() error { closed[name]++; return real() }
		return env, nil
	}
	defer paths.SwapAdapters(
		func(db string, set *bench.Set) (bench.Env, error) {
			env, err := hostpath.Factory(db, set)
			return counting("host", env, err)
		},
		func(db string, set *bench.Set) (bench.Env, error) {
			env, err := legacy.Env(db, set)
			return counting("legacy", env, err)
		},
	)()
	fs, err := paths.Factories([]string{paths.Legacy})
	if err != nil {
		t.Fatal(err)
	}
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	env, err := fs[0](db, p.Set)
	if err != nil {
		t.Fatal(err)
	}
	if env.Finder == nil || env.Similar == nil {
		t.Fatalf("legacy env without its own finder: %+v", env)
	}
	if _, err := env.Finder.Find("/bench/app", "indent Go files tabs", bench.FindLimit); err != nil {
		t.Fatal(err)
	}
	if err := env.Close(); err != nil {
		t.Fatal(err)
	}
	if opened["host"] != 0 || closed["host"] != 0 {
		t.Errorf("the legacy path opened the current path: opened %v, closed %v", opened, closed)
	}
	if opened["legacy"] != 1 || closed["legacy"] != 1 {
		t.Errorf("legacy opened %d, closed %d, want each once", opened["legacy"], closed["legacy"])
	}
}

// 설정 덮어쓰기는 지금 경로에만 닿는다. 옛 회상은 얼린 사본이라 설정을 읽지 않는다.
func TestFactoriesWith는_덮어쓰기를_지금_경로에만_건다(t *testing.T) {
	p := prepared(t)
	opts := paths.Options{Set: map[string]string{"saveSimilarMin": "2"}}
	fs, err := paths.FactoriesWith([]string{paths.Legacy}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bench.Run(p, fs...); err != nil {
		t.Fatalf("legacy must ignore the overrides: %v", err)
	}
	fs, err = paths.FactoriesWith([]string{paths.Current}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bench.Run(p, fs...); err == nil {
		t.Fatal("the current path accepted saveSimilarMin=2")
	}
	fs, _ = paths.FactoriesWith([]string{paths.Current}, paths.Options{Set: map[string]string{"saveSimilarMin": "0.9"}})
	if _, err := bench.Run(p, fs...); err != nil {
		t.Fatalf("saveSimilarMin=0.9: %v", err)
	}
}

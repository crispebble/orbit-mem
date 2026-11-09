package bench_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

// -update-golden rewrites only baseline-current.json. The legacy baseline is compared strictly and
// never rewritten over: a missing one fails, and -write-legacy-baseline writes it only when it is missing
// (to make it on purpose: the first time, or after removing it because the definition of a metric or the
// cases changed; the legacy recall code stays as it is).
var (
	updateGolden = flag.Bool("update-golden", false, "rewrite internal/bench/testdata/baseline-current.json")
	writeLegacy  = flag.Bool("write-legacy-baseline", false, "write internal/bench/testdata/baseline-legacy.json when it is missing (never over one that exists)")
)

const (
	publicSet      = "testdata/public_set.json"
	baselineLegacy = "testdata/baseline-legacy.json"
	baselineNow    = "testdata/baseline-current.json"
)

// factory is the recall path of a name, put together as the command does.
func factory(t *testing.T, name string) bench.Factory {
	t.Helper()
	fs, err := paths.Factories([]string{name})
	if err != nil {
		t.Fatal(err)
	}
	return fs[0]
}

func TestPublicSetData(t *testing.T) {
	set, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(set.Memories); n < 190 || n > 210 {
		t.Errorf("memories = %d, want 190..210", n)
	}
	if n := len(set.Cases); n < 60 {
		t.Errorf("cases = %d, want >= 60", n)
	}
	all := bench.CountBundles(set.Cases, bench.Partition(set), bench.PartAll)
	if all.Subagent < 6 || all.Duplicate < 6 || all.Empty < 12 {
		t.Errorf("bundles = %+v, want subagent>=6 duplicate>=6 empty>=12", all)
	}
	sessions := map[string]bool{}
	for _, c := range set.Cases {
		if c.Session != "" {
			sessions[c.Session] = true
		}
	}
	if len(sessions) < 8 {
		t.Errorf("sessions = %d, want >= 8", len(sessions))
	}
	h := bench.CountBundles(set.Cases, bench.Partition(set), bench.PartTest)
	if h.Answered < 1 || h.Empty < 1 || h.Subagent < 1 || h.Duplicate < 1 {
		t.Errorf("test bundles = %+v, want each >= 1", h)
	}
}

// measure runs one recall path on a fresh copy of the public set and returns its report JSON.
func measure(t *testing.T, f bench.Factory) []byte {
	t.Helper()
	fixHome(t)
	set, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// diff names the first lines that differ, with the case id (or the metric block) they sit in.
func diff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	where := func(lines []string, i int) string {
		for ; i >= 0; i-- {
			s := strings.TrimSpace(lines[i])
			if strings.HasPrefix(s, `"id":`) {
				return "case " + strings.Trim(strings.TrimPrefix(s, `"id":`), ` ",`)
			}
			if strings.HasPrefix(s, `"train":`) || strings.HasPrefix(s, `"test":`) || strings.HasPrefix(s, `"all":`) {
				return "metrics " + strings.Trim(strings.SplitN(s, ":", 2)[0], `"`)
			}
		}
		return "header"
	}
	var out []string
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a == b {
			continue
		}
		if len(out) == 8 {
			out = append(out, "...")
			break
		}
		out = append(out, fmt.Sprintf("line %d (%s): baseline %s, now %s", i+1, where(w, min(i, len(w)-1)), strings.TrimSpace(a), strings.TrimSpace(b)))
	}
	return strings.Join(out, "\n")
}

func TestPublicSet(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		if err := checkLegacy(baselineLegacy, measure(t, factory(t, paths.Legacy)), *writeLegacy); err != nil {
			t.Error(err)
		}
	})
	t.Run("current", func(t *testing.T) {
		got := measure(t, factory(t, paths.Current))
		if *updateGolden {
			if err := os.MkdirAll(filepath.Dir(baselineNow), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(baselineNow, got, 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		want, err := os.ReadFile(baselineNow)
		if err != nil {
			t.Fatalf("%v (create it with: go test ./internal/bench -run TestPublicSet -update-golden)", err)
		}
		if string(want) != string(got) {
			t.Errorf("the current recall differs from %s:\n%s\n"+
				"If this follows merging 1-3 and 1-4, re-measure with `go test ./internal/bench -run TestPublicSet -update-golden` and record the difference in the issue.",
				baselineNow, diff(want, got))
		}
	})
}

// checkLegacy compares the legacy report with its baseline. A missing baseline is a failure unless
// write is set; then it is written. write never touches a baseline that exists.
func checkLegacy(path string, got []byte, write bool) error {
	want, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err) && write:
		if err := os.WriteFile(path, got, 0o644); err != nil {
			return err
		}
		return nil
	case os.IsNotExist(err):
		return fmt.Errorf("%s is missing: the legacy baseline is made once, on purpose, with "+
			"go test ./internal/bench -run 'TestPublicSet$/legacy' -write-legacy-baseline", path)
	case err != nil:
		return err
	case write:
		return fmt.Errorf("-write-legacy-baseline does not rewrite %s: the frozen copy must not move", path)
	case string(want) != string(got):
		return fmt.Errorf("the legacy recall differs from %s (the frozen copy must not move):\n%s", path, diff(want, got))
	}
	return nil
}

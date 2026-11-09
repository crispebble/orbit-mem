package bench_test

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

// out is what a fake recall step shows: memory keys and characters.
type out struct {
	keys  []string
	chars int
}

// script answers the fake steps: "start:<session>", "req:<prompt>", "tool:<tool>", "sub:<task>".
type script map[string]out

type fake struct {
	name  string
	s     script
	idOf  func(string) string
	calls *[]string
}

func (f fake) show(call string) (bench.Shown, error) {
	*f.calls = append(*f.calls, call)
	o := f.s[call]
	ids := []string{}
	for _, k := range o.keys {
		ids = append(ids, f.idOf(k))
	}
	return bench.Shown{IDs: ids, Chars: o.chars, Text: strings.Repeat("x", o.chars)}, nil
}
func (f fake) Name() string                                      { return f.name }
func (f fake) Start(s, cwd string) (bench.Shown, error)          { return f.show("start:" + s) }
func (f fake) Request(s, cwd, p string) (bench.Shown, error)     { return f.show("req:" + p) }
func (f fake) Tool(s, cwd, tool string) (bench.Shown, error)     { return f.show("tool:" + tool) }
func (f fake) Subagent(s, cwd, task string) (bench.Shown, error) { return f.show("sub:" + task) }

// keyed answers Find and Similar with keys.
type keyed struct {
	idOf func(string) string
	find map[string][]string
	sim  map[string][]string
}

func (k keyed) ids(keys []string) []string {
	out := []string{}
	for _, x := range keys {
		out = append(out, k.idOf(x))
	}
	return out
}
func (k keyed) Find(cwd, q string, limit int) ([]string, error) { return k.ids(k.find[q]), nil }
func (k keyed) Similar(cwd string, d bench.Draft) ([]string, error) {
	return k.ids(k.sim[d.Title]), nil
}

// memories is ten fake memories with the keys a..j.
const memories = `"memories": [
 {"key":"a","kind":"fact","scope":"global","title":"memory a"},{"key":"b","kind":"fact","scope":"global","title":"memory b"},
 {"key":"c","kind":"fact","scope":"global","title":"memory c"},{"key":"d","kind":"fact","scope":"global","title":"memory d"},
 {"key":"e","kind":"fact","scope":"global","title":"memory e"},{"key":"f","kind":"fact","scope":"global","title":"memory f"},
 {"key":"g","kind":"fact","scope":"global","title":"memory g"},{"key":"h","kind":"fact","scope":"global","title":"memory h"},
 {"key":"i","kind":"fact","scope":"global","title":"memory i"},{"key":"j","kind":"fact","scope":"global","title":"memory j"}]`

func setJSON(cases string) string {
	return `{"about":"test","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",` + memories + `,"cases":[` + cases + `]}`
}

// prepare reads a set of the ten memories and the cases and makes its DB.
func prepare(t *testing.T, cases string) *bench.Prepared {
	t.Helper()
	fixHome(t)
	set, err := bench.Parse([]byte(setJSON(cases)))
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

// runFake runs one fake recall path over the cases and gives the report and the calls it got.
func runFake(t *testing.T, cases string, s script, k keyed) (*bench.Report, *[]string) {
	t.Helper()
	p := prepare(t, cases)
	k.idOf = p.IDOf
	var calls []string
	f := func(db string, set *bench.Set) (bench.Env, error) {
		return bench.Env{Recaller: fake{"fake", s, p.IDOf, &calls}, Finder: k, Similar: k, Close: func() error { return nil }}, nil
	}
	rep, err := bench.Run(p, f)
	if err != nil {
		t.Fatal(err)
	}
	return rep, &calls
}

func wantRate(t *testing.T, name string, r bench.Rate, num, den int, val *float64) {
	t.Helper()
	if r.Num != num || r.Den != den {
		t.Errorf("%s = %d/%d, want %d/%d", name, r.Num, r.Den, num, den)
	}
	switch {
	case val == nil && r.Value != nil:
		t.Errorf("%s value = %v, want null", name, *r.Value)
	case val != nil && (r.Value == nil || *r.Value != *val):
		t.Errorf("%s value = %v, want %v", name, r.Value, *val)
	}
}

func wantDist(t *testing.T, name string, d bench.Dist, n int, mean, median float64, max int) {
	t.Helper()
	if d.N != n || d.Mean != mean || d.Median != median || d.Max != max {
		t.Errorf("%s = %+v, want n=%d mean=%v median=%v max=%d", name, d, n, mean, median, max)
	}
}

func f64(v float64) *float64 { return &v }

package bench_test

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

const holdout6Set = "testdata/holdout6_set.json"

// TestHoldout6SetData only checks that the 0.20.0 judging-only set loads and that every symptom has cases on
// both sides (something must come, something must not). It asserts no measured value: the set is read once,
// by the final verification, through `bench recall --split all`.
func TestHoldout6SetData(t *testing.T) {
	set, err := bench.Read(holdout6Set)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range pub.Cases {
		ids[c.ID] = true
	}
	// The four symptoms are told apart by the case id: v6-a (dropped or expired), v6-b (synonym and
	// Korean/English spelling), v6-c (weak confirmed against an exact candidate), v6-d (tool input).
	type side struct{ expect, none int }
	symptoms := map[string]*side{"a": {}, "b": {}, "c": {}, "d": {}}
	for _, c := range set.Cases {
		if ids[c.ID] {
			t.Errorf("case id %q is also in the public set", c.ID)
		}
		if !strings.HasPrefix(c.ID, "v6-") || len(c.ID) < 5 {
			t.Errorf("case id %q does not start with v6-<symptom>-", c.ID)
			continue
		}
		if c.CaseKind() == bench.KindStart {
			continue
		}
		s := symptoms[c.ID[3:4]]
		if s == nil {
			t.Errorf("case id %q has no symptom letter a-d", c.ID)
			continue
		}
		if len(c.Expect) > 0 {
			s.expect++
		} else {
			s.none++
		}
	}
	for name, s := range symptoms {
		if s.expect < 4 || s.none < 4 {
			t.Errorf("symptom %s: expect=%d none=%d, want >=4 on each side", name, s.expect, s.none)
		}
	}
}

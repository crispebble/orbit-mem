package bench_test

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

const holdout2Set = "testdata/holdout2_set.json"

// TestHoldout2SetData only checks that the second judging-only set loads and keeps the per-kind floors.
// It asserts no measured value: the set is used once, after the fixes, through `bench recall --split all`.
func TestHoldout2SetData(t *testing.T) {
	set, err := bench.Read(holdout2Set)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Memories) != len(pub.Memories) {
		t.Errorf("memories = %d, want the %d of the public set", len(set.Memories), len(pub.Memories))
	}
	ids := map[string]bool{}
	for _, c := range pub.Cases {
		ids[c.ID] = true
	}
	var answered, emptyHuman, nonHuman, subagent, duplicate int
	perSession := map[string]int{}
	for _, c := range set.Cases {
		if ids[c.ID] {
			t.Errorf("case id %q is also in the public set", c.ID)
		}
		perSession[c.Session]++
		switch {
		case c.CaseKind() == bench.KindSubagent:
			subagent++
		case c.CaseKind() == bench.KindDuplicate:
			duplicate++
		case c.CaseOrigin() != bench.OriginUser:
			nonHuman++
		case len(c.Expect) > 0:
			answered++
		default:
			emptyHuman++
		}
	}
	if answered < 60 || emptyHuman < 30 || nonHuman < 6 || subagent < 15 || duplicate < 8 {
		t.Errorf("answered=%d emptyHuman=%d nonHuman=%d subagent=%d duplicate=%d, want >=60 >=30 >=6 >=15 >=8",
			answered, emptyHuman, nonHuman, subagent, duplicate)
	}
	for name, n := range perSession {
		if n < 3 || n > 8 {
			t.Errorf("session %q has %d cases, want 3 to 8", name, n)
		}
	}
	if len(perSession) < 10 {
		t.Errorf("sessions = %d, want several", len(perSession))
	}
}

package bench_test

import (
	"reflect"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

const holdout4DupSet = "testdata/holdout4_dup_set.json"

// TestHoldout4DupSetData only checks that the judging-only duplicate set loads and keeps its floors.
// It asserts no measured value: the set is read once, through `bench recall --split all`.
func TestHoldout4DupSetData(t *testing.T) {
	set, err := bench.Read(holdout4DupSet)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(set.Memories, pub.Memories) {
		t.Errorf("memories differ from the public set, want a verbatim copy")
	}
	ids := map[string]bool{}
	for _, c := range pub.Cases {
		ids[c.ID] = true
	}
	var same, different int
	perSession := map[string]int{}
	for _, c := range set.Cases {
		if ids[c.ID] {
			t.Errorf("case id %q is also in the public set", c.ID)
		}
		if c.CaseKind() != bench.KindDuplicate {
			t.Errorf("case %q kind = %q, want duplicate", c.ID, c.CaseKind())
			continue
		}
		perSession[c.Session]++
		if len(c.Expect) > 0 {
			same++
		} else {
			different++
		}
	}
	if same < 20 || different < 12 || same+different < 30 {
		t.Errorf("same=%d different=%d, want >=20 >=12 and >=30 in all", same, different)
	}
	if n := len(perSession); n < 3 || n > 6 {
		t.Errorf("sessions = %d, want 3 to 6", n)
	}
}

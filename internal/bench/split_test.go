package bench_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

func manyCases(n int) (*bench.Set, string) {
	var cs []string
	for i := 0; i < n; i++ {
		cs = append(cs, fmt.Sprintf(`{"id":"c%02d","session":"s%02d","prompt":"p%d","expect":["a"]},{"id":"d%02d","session":"s%02d","prompt":"q%d"}`, i, i/2, i, i, i/2, i))
	}
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","seed":"s","cases":[` + strings.Join(cs, ",") + `]}`))
	if err != nil {
		panic(err)
	}
	return set, ""
}

func count(parts []string, part string) (n int) {
	for _, p := range parts {
		if p == part {
			n++
		}
	}
	return
}

func TestPartition은_세션_단위로_70대_30으로_나눈다(t *testing.T) {
	fixHome(t)
	set, _ := manyCases(40) // 세션 20개, 사례 80개
	parts := bench.Partition(set)
	if len(parts) != len(set.Cases) {
		t.Fatalf("parts %d", len(parts))
	}
	bySession := map[string]string{}
	units := map[string]string{}
	for i, c := range set.Cases {
		if prev, ok := bySession[c.Session]; ok && prev != parts[i] {
			t.Errorf("session %s is split", c.Session)
		}
		bySession[c.Session] = parts[i]
		units[c.Session] = parts[i]
	}
	train := 0
	for _, p := range units {
		if p == bench.PartTrain {
			train++
		}
	}
	if len(units) != 20 || train != 14 {
		t.Errorf("train sessions %d of %d, want 14 of 20", train, len(units))
	}
}

func TestPartition은_같은_seed에서_늘_같고_seed가_다르면_달라진다(t *testing.T) {
	set, _ := manyCases(40)
	a, b := bench.Partition(set), bench.Partition(set)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Error("not deterministic")
	}
	set.Seed = "other"
	if fmt.Sprint(bench.Partition(set)) == fmt.Sprint(a) {
		t.Error("the seed does not move the split")
	}
}

func TestPartition은_세션이_없으면_사례_id로_나눈다(t *testing.T) {
	var cs []string
	for i := 0; i < 10; i++ {
		cs = append(cs, fmt.Sprintf(`{"id":"c%d","prompt":"p"}`, i))
	}
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","seed":"s","cases":[` + strings.Join(cs, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts := bench.Partition(set)
	if count(parts, bench.PartTrain) != 7 || count(parts, bench.PartTest) != 3 {
		t.Errorf("%v", parts)
	}
}

func TestCountBundles는_묶음마다_센다(t *testing.T) {
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","cases":[
	 {"id":"1","prompt":"p","expect":["a"]},{"id":"2","prompt":"p"},{"id":"3","tool":"t"},
	 {"id":"4","kind":"subagent","task":"t"},{"id":"5","kind":"duplicate","save":{"kind":"fact","title":"t"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{bench.PartTrain, bench.PartTest, bench.PartTest, bench.PartTest, bench.PartTrain}
	if got := bench.CountBundles(set.Cases, parts, bench.PartTest); got != (bench.Bundles{Empty: 2, Subagent: 1}) {
		t.Errorf("test %+v", got)
	}
	if got := bench.CountBundles(set.Cases, parts, bench.PartAll); got != (bench.Bundles{Answered: 1, Empty: 2, Subagent: 1, Duplicate: 1}) {
		t.Errorf("all %+v", got)
	}
}

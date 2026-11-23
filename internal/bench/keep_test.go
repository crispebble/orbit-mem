package bench_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
)

// keepCases is a replay-like file: s1 starts with a request (the run makes its session start before it), s2 with a
// recorded start, s3 has a duplicate first.
const keepCases = `
 {"id":"s1-001","session":"s1","prompt":"p1","at":"2026-11-01T09:00:00Z"},
 {"id":"s1-002","session":"s1","tool":"Bash","at":"2026-11-01T09:01:00Z"},
 {"id":"s1-003","session":"s1","kind":"start","source":"compact","at":"2026-11-01T09:02:00Z"},
 {"id":"s2-001","session":"s2","kind":"start","source":"startup","at":"2026-11-01T10:00:00Z"},
 {"id":"s2-002","session":"s2","kind":"subagent","task":"T","at":"2026-11-01T10:01:00Z"},
 {"id":"s3-001","session":"s3","kind":"duplicate","save":{"kind":"fact","title":"x"},"at":"2026-11-01T11:00:00Z"},
 {"id":"s3-002","session":"s3","prompt":"p3","at":"2026-11-01T11:01:00Z"}`

func keepSet(t *testing.T) *bench.Set {
	t.Helper()
	set, err := bench.Parse([]byte(setJSON(keepCases)))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func ids(set *bench.Set) []string {
	var out []string
	for _, c := range set.Cases {
		out = append(out, c.ID)
	}
	return out
}

func TestKeep_세션으로_거르면_그_세션의_사례만_파일_순서대로_남는다(t *testing.T) {
	set := keepSet(t)
	n, err := set.Keep([]string{"s2", "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s1-001", "s1-002", "s1-003", "s2-001", "s2-002"}
	if got := ids(set); n != len(want) || !slices.Equal(got, want) {
		t.Fatalf("kept %d %v, want %v", n, got, want)
	}
	if _, err := keepSet(t).Keep([]string{"s1", "s9"}, nil); err == nil || !strings.Contains(err.Error(), "s9") {
		t.Fatalf("err %v, want a session with no case named", err)
	}
	if _, err := keepSet(t).Keep(nil, []string{"later"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	if _, err := keepSet(t).Keep([]string{"s3"}, []string{bench.KindSubagent}); err == nil {
		t.Fatal("a filter that keeps no case must be refused")
	}
}

// 시작만 남기면 재생이 부르는 세션 시작이 모두 같은 시각·같은 차례로 남는다: 기록된 시작 사례, 그리고 시작 사례 없이
// 시작하는 세션의 첫 시작(실행이 그 세션의 첫 사례 앞에 부르는 것)은 그 첫 사례의 시각에 선 시작 사례로 남는다. 중복
// 저장 사례 앞에서는 세션 시작을 부르지 않으므로 그 뒤 첫 사례가 기준이다.
func TestKeep_시작만_남기면_시작_사례가_없는_세션의_첫_시작을_그_시각에_세운다(t *testing.T) {
	full := prepare(t, keepCases)
	set := keepSet(t)
	if _, err := set.Keep(nil, []string{bench.KindStart}); err != nil {
		t.Fatal(err)
	}
	want := []string{"s1-001+start", "s1-003", "s2-001", "s3-002+start"}
	if got := ids(set); !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	if c := set.Cases[0]; c.CaseKind() != bench.KindStart || c.Source != "" || c.At != "2026-11-01T09:00:00Z" || c.Session != "s1" {
		t.Fatalf("stand-in start %+v, want a start of s1 at its first case's time with no source", c)
	}
	if c := set.Cases[3]; c.At != "2026-11-01T11:01:00Z" {
		t.Fatalf("stand-in start of s3 %+v, want the time of its first case after the duplicate", c)
	}
	var fullCalls, keptCalls []string
	f := func(calls *[]string, p *bench.Prepared) bench.Factory {
		return func(db string, s *bench.Set) (bench.Env, error) {
			return bench.Env{Recaller: fake{"r", script{}, nil, p.IDOf, calls, nil}, Similar: keyed{idOf: p.IDOf}, SetNow: func(time.Time) {}}, nil
		}
	}
	if _, err := bench.Run(full, f(&fullCalls, full)); err != nil {
		t.Fatal(err)
	}
	kept, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	if _, err := bench.Run(kept, f(&keptCalls, kept)); err != nil {
		t.Fatal(err)
	}
	var fullStarts []string
	for _, c := range fullCalls {
		if strings.HasPrefix(c, "start:") || strings.HasPrefix(c, "reset:") {
			fullStarts = append(fullStarts, c)
		}
	}
	if !slices.Equal(keptCalls, fullStarts) {
		t.Fatalf("kept run calls %v, want the starts of the full run %v", keptCalls, fullStarts)
	}
}

func TestProductsOf_설정_파일의_제품_지도를_그대로_읽는다(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"recall":"auto","products":{"app":{"match":["/bench/app"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := bench.ProductsOf(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"app":{"match":["/bench/app"]}}` {
		t.Fatalf("products %s", raw)
	}
	none := filepath.Join(dir, "none.json")
	if err := os.WriteFile(none, []byte(`{"recall":"auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bench.ProductsOf(none); err == nil {
		t.Fatal("a settings file without products must be refused")
	}
	if _, err := bench.ProductsOf(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing settings file must be refused")
	}
}

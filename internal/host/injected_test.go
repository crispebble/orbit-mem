package host

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// F-6: 주입 사건에는 사건을 기록할 때 읽은 id별 기억 상태가 남는다(기존 칸은 그대로).
// 묶음 재전달에 기대지 않고 injected()에 네 상태의 기억과 없는 id를 직접 준다.
func TestInjected_주입_기록에_기억_상태가_남는다(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	save := func(title, src string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: title, Scope: memory.ScopeGlobal,
			Source: memory.Source{Kind: src, Ref: "x", Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	cand, conf, stale, hidden := save("후보 기억", memory.SourceSession), save("확정 기억", memory.SourceQuote), save("낡은 기억", memory.SourceQuote), save("숨긴 기억", memory.SourceQuote)
	if _, err := s.SetState(stale.ID, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(hidden.ID, memory.StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	c := &call{req: Request{Host: "host-x", Session: "s-1"}, store: s}
	ids := []string{cand.ID, conf.ID, stale.ID, hidden.ID, "m-gone"}
	c.injected(memory.EventInjectSessionStart, inject.Injection{MemoryIDs: ids, Chars: 9})
	evs, err := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if err != nil || len(evs) != 1 {
		t.Fatalf("events = %v, %v", evs, err)
	}
	var d struct {
		MemoryIDs []string          `json:"memoryIds"`
		Chars     int               `json:"chars"`
		States    map[string]string `json:"states"`
	}
	if err := json.Unmarshal(evs[0].Data, &d); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{cand.ID: memory.StateCandidate, conf.ID: memory.StateConfirmed, stale.ID: memory.StateStale, hidden.ID: memory.StateHidden}
	if len(d.MemoryIDs) != 5 || d.Chars != 9 || len(d.States) != 4 {
		t.Fatalf("data = %s", evs[0].Data)
	}
	for id, st := range want {
		if d.States[id] != st {
			t.Fatalf("states[%s] = %q, want %q (%s)", id, d.States[id], st, evs[0].Data)
		}
	}
}

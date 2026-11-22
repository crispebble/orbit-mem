package host_test

import (
	"encoding/json"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// F-6: 주입 사건에는 id마다 주입 시점의 기억 상태가 남는다(기존 칸은 그대로).
func TestInjected_주입_기록에_기억_상태가_남는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	m := pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.recall(host.OpContext, map[string]any{"source": "startup"})
	var found bool
	for _, ev := range events(t, s) {
		if ev.Kind != memory.EventInjectSessionStart {
			continue
		}
		var d struct {
			MemoryIDs []string          `json:"memoryIds"`
			Chars     int               `json:"chars"`
			States    map[string]string `json:"states"`
		}
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			t.Fatal(err)
		}
		found = true
		if len(d.MemoryIDs) == 0 || d.Chars == 0 || d.States[m.ID] != memory.StateConfirmed {
			t.Fatalf("data = %s", ev.Data)
		}
	}
	if !found {
		t.Fatal("주입 사건이 없다")
	}
}

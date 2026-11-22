package mcpserver

// 재검토 후보 표시(L-2): memory_find와 memory_get은 마지막 확인 뒤 90일이 지난 현재 값을 빼지 않고 「재검토 후보」로 표시한다.
// 상태는 확정 그대로다.

import (
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestMemoryFindGet_재검토_후보는_빼지_않고_표시만_한다(t *testing.T) {
	cs, s := connect(t)
	m := quoteSave(t, s, "배포 창은 금요일 오후다", "배포 창", 0)
	fresh := quoteSave(t, s, "답은 한글 존댓말로 쓴다", "답 언어", 0)
	find := func() map[string]FindItem {
		var out FindOut
		call(t, cs, "memory_find", map[string]any{"scope": "all"}, &out)
		got := map[string]FindItem{}
		for _, it := range out.Items {
			got[it.ID] = it
		}
		return got
	}
	get := func(id string) MemoryView {
		var out GetOut
		call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, &out)
		if len(out.Items) != 1 {
			t.Fatalf("get %s: %+v", id, out)
		}
		return out.Items[0]
	}
	// 90일 안에는 표시가 없다.
	if got := find(); got[m.ID].Note != "" || got[fresh.ID].Note != "" {
		t.Fatalf("90일 안인데 표시가 있습니다: %+v", got)
	}
	if v := get(m.ID); v.Review != "" {
		t.Fatalf("90일 안인데 get에 표시가 있습니다: %+v", v)
	}
	advance(s, 91*24*time.Hour)
	// 사용자 말로 다시 확인한 것은 표시가 없고, 그대로 둔 것은 표시가 붙는다.
	body := "답은 한글 존댓말로 쓴다. 반말은 쓰지 않는다"
	if _, err := s.Update(memory.UpdateInput{ID: fresh.ID, Body: &body, Who: memory.Who{Actor: memory.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	got := find()
	if it, ok := got[m.ID]; !ok || it.Note != "재검토 후보" || it.State != memory.StateConfirmed {
		t.Fatalf("find: 빠졌거나 표시가 없습니다: %+v", got)
	}
	if got[fresh.ID].Note != "" {
		t.Fatalf("확인한 기억에 표시가 있습니다: %+v", got[fresh.ID])
	}
	if v := get(m.ID); v.Review != "재검토 후보" || v.State != memory.StateConfirmed || v.Notice != "" {
		t.Fatalf("get: %+v", v)
	}
	if v := get(fresh.ID); v.Review != "" {
		t.Fatalf("확인한 기억의 get에 표시가 있습니다: %+v", v)
	}
	// 폐기된 기억은 폐기됨이 먼저다: 재검토 표시를 덮어쓰지 않는다.
	if _, err := s.SetState(m.ID, memory.StateStale, "틀림"); err != nil {
		t.Fatal(err)
	}
	var out FindOut
	call(t, cs, "memory_find", map[string]any{"scope": "all", "states": []string{"candidate", "confirmed", "stale"}}, &out)
	for _, it := range out.Items {
		if it.ID == m.ID && it.Note != "폐기됨" {
			t.Fatalf("폐기된 기억의 표시: %+v", it)
		}
	}
	if v := get(m.ID); v.Review != "" || v.Notice == "" {
		t.Fatalf("폐기된 기억의 get: %+v", v)
	}
}

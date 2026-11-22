package mcpserver

// 재검토 후보 표시(L-2): memory_find와 memory_get은 마지막 확인 뒤 90일이 지난 현재 값을 빼지 않고 「재검토 후보」로 표시한다.
// 상태는 확정 그대로다.

import (
	"strings"
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

// 사람이 아니고 사용자 말 출처도 아닌 저장이 준 재검토 주기는 365일로 자르고 응답에 알린다. 사용자 말 저장은 그대로다.
func TestMemorySave_AI가_준_재검토_주기는_365일로_자르고_알린다(t *testing.T) {
	cs, _ := connect(t)
	var ai MemoryOut
	res := call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "배포 전에 린트를 돌린다", "reviewAfterDays": 3650,
		"source": map[string]any{"kind": "session"}}, &ai)
	if res.IsError || ai.Memory.ReviewDays != 365 || !strings.Contains(strings.Join(ai.Warnings, "\n"), "365") {
		t.Fatalf("AI 저장: %s %+v", text(res), ai)
	}
	var short MemoryOut
	res = call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "머지 전에 시험을 돌린다", "reviewAfterDays": 30,
		"source": map[string]any{"kind": "session"}}, &short)
	if res.IsError || short.Memory.ReviewDays != 30 || strings.Contains(strings.Join(short.Warnings, "\n"), "365") {
		t.Fatalf("365일 안의 주기는 그대로여야 합니다: %s %+v", text(res), short)
	}
	var user MemoryOut
	res = call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "엔진은 자체 포크를 쓴다", "topic": "엔진", "appliesTo": "user", "reviewAfterDays": 3650,
		"source": map[string]any{"kind": "quote", "excerpt": "엔진은 자체 포크를 쓴다"}}, &user)
	if res.IsError || user.Memory.ReviewDays != 3650 {
		t.Fatalf("사용자 말 저장은 상한 그대로여야 합니다: %s %+v", text(res), user)
	}
}

// L-3: 감사 목록(proposals status=audit)에 reviewDue·reviewDueCount가 실리고 제목은 80룬쯤으로 잘린다.
func TestProposals_감사_목록에_재검토_후보가_실리고_제목은_잘린다(t *testing.T) {
	cs, s := connect(t)
	long := strings.Repeat("가", 150)
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: long, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	var out ProposalsOut
	call(t, cs, "proposals", map[string]any{"status": "audit"}, &out)
	if out.Audit == nil || out.Audit.ReviewDueCount != 0 {
		t.Fatalf("0건이어야 합니다: %+v", out.Audit)
	}
	advance(s, 91*24*time.Hour)
	out = ProposalsOut{}
	res := call(t, cs, "proposals", map[string]any{"status": "audit"}, &out)
	if out.Audit == nil || out.Audit.ReviewDueCount != 1 || len(out.Audit.ReviewDue) != 1 || out.Audit.ReviewDue[0].ID != m.ID {
		t.Fatalf("재검토 후보가 없습니다: %s", text(res))
	}
	if n := len([]rune(out.Audit.ReviewDue[0].Title)); n > 81 { // 80룬과 자른 표시(…)
		t.Fatalf("제목 %d룬, 80룬쯤으로 잘려야 합니다", n)
	}
	if !strings.Contains(text(res), `"reviewDueCount"`) {
		t.Fatalf("응답에 reviewDueCount가 없습니다: %s", text(res))
	}
}

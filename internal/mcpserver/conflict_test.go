package mcpserver

// 어긋남 표시(명세 C-5): memory_find와 memory_get은 어긋남이 있는 현재 기억 옆에 어긋나는 새 사실을 보인다. 어긋남이 없는
// 기억에는 아무것도 붙지 않고, 풀린 어긋남은 사라진다.

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// conflictOf saves a user's fact and a session-learned fact on its topic, which the save leaves as a conflict.
func conflictOf(t *testing.T, s *memory.Store) (old, nw memory.Memory) {
	t.Helper()
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 금요일 오후에만 한다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 금요일 오후에만 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 화요일 오전에 한다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Who: memory.Who{Actor: memory.ActorAI}, Source: memory.Source{Kind: memory.SourceSession, Ref: "s"}})
	if err != nil || len(rep.Conflicts) != 1 {
		t.Fatalf("어긋남이 남지 않았습니다: %v %+v", err, rep)
	}
	return old, nw
}

func TestMemoryGet_어긋나는_새_사실을_보인다(t *testing.T) {
	cs, s := connect(t)
	old, nw := conflictOf(t, s)
	plain, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "점검은 월요일 새벽에 한다", Topic: "점검 일정", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "점검은 월요일 새벽에 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	var out GetOut
	if res := call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID, nw.ID, plain.ID}}, &out); res.IsError {
		t.Fatal(text(res))
	}
	byID := map[string]MemoryView{}
	for _, v := range out.Items {
		byID[v.ID] = v
	}
	want := "어긋나는 새 사실: 「배포는 화요일 오전에 한다」(" + nw.ID + ")"
	if got := byID[old.ID].Conflict; got != want {
		t.Fatalf("옛 기억의 conflict %q, want %q", got, want)
	}
	if byID[nw.ID].Conflict != "" || byID[plain.ID].Conflict != "" {
		t.Fatalf("어긋남이 없는 기억에 붙었습니다: %+v %+v", byID[nw.ID], byID[plain.ID])
	}
	// 폐기된 기억에는 경고 머리가 따로 있어 어긋남 줄을 붙이지 않는다.
	if _, err := s.SetState(old.ID, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	var stale GetOut // 새 값에 풀어야 한다: 같은 값에 다시 풀면 빠진 칸이 앞의 값으로 남는다
	call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID}}, &stale)
	if stale.Items[0].Conflict != "" || stale.Items[0].Notice == "" {
		t.Fatalf("폐기된 기억: %+v", stale.Items[0])
	}
}

func TestMemoryFind_어긋나는_새_사실을_보인다(t *testing.T) {
	cs, s := connect(t)
	old, nw := conflictOf(t, s)
	var out FindOut
	call(t, cs, "memory_find", map[string]any{"scope": "all", "query": "배포 일정"}, &out)
	got := map[string]FindItem{}
	for _, it := range out.Items {
		got[it.ID] = it
	}
	want := "어긋나는 새 사실: 「배포는 화요일 오전에 한다」(" + nw.ID + ")"
	if it, ok := got[old.ID]; !ok || it.Conflict != want {
		t.Fatalf("옛 기억 줄 %+v (%v), want %q", it, ok, want)
	}
	if it := got[nw.ID]; it.Conflict != "" {
		t.Fatalf("새 쪽에는 붙지 않습니다: %+v", it)
	}
	// 사용자 말로 정하면 풀려 표시가 사라진다.
	body := "사용자가 확인함"
	if _, err := s.Update(memory.UpdateInput{ID: old.ID, Body: &body, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "금요일이 맞아"},
		Sources: []memory.Source{{Kind: memory.SourceQuote, Excerpt: "금요일이 맞아"}}}); err != nil {
		t.Fatal(err)
	}
	var after FindOut
	call(t, cs, "memory_find", map[string]any{"scope": "all", "query": "배포 일정"}, &after)
	for _, it := range after.Items {
		if strings.Contains(it.Conflict, "어긋나는") {
			t.Fatalf("풀린 어긋남이 남았습니다: %+v", it)
		}
	}
}

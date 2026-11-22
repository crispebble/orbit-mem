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

// 어긋남 칸도 보임 판정을 지난 새 쪽만 싣는다: 거절·낡음·만료된 새 쪽, 다른 머리에 가려진 새 쪽은 싣지 않는다. memory_find는
// 지금 자리(here)에서 보이는 범위만, memory_get은 id로 직접 읽는 도구라 보임 판정만 본다.
func TestMemoryFindGet_보일_수_없는_새_쪽의_제목은_conflict에_싣지_않는다(t *testing.T) {
	cs, s := connectWith(t, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "t"})
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 금요일 오후에만 한다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 금요일 오후에만 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	cand := func(title, scope, topic string, mod func(*memory.SaveInput)) memory.Memory {
		in := memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: scope, Who: memory.Who{Actor: memory.ActorAI},
			Source: memory.Source{Kind: memory.SourceSession, Ref: "s"}}
		if mod != nil {
			mod(&in)
		}
		m, _, err := s.SaveWith(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.FlagConflict(m.ID, old.ID, "시험", memory.Who{}); err != nil {
			t.Fatal(err)
		}
		return m
	}
	foreign := cand("다른 프로젝트 후보 제목", "project:github.com/me/other", "", nil)
	rejected := cand("거절한 후보 제목", memory.ScopeGlobal, "", nil)
	if _, err := s.SetState(rejected.ID, memory.StateHidden, "틀림"); err != nil {
		t.Fatal(err)
	}
	cand("다른 머리에 가려진 제목", memory.ScopeGlobal, "다른 주제", nil)
	if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "다른 주제의 확정 사실", Topic: "다른 주제", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "다른 주제의 확정 사실"}}); err != nil {
		t.Fatal(err)
	}
	var find FindOut
	call(t, cs, "memory_find", map[string]any{"query": "배포 일정"}, &find)
	var found *FindItem
	for i, it := range find.Items {
		if it.ID == old.ID {
			found = &find.Items[i]
		}
	}
	if found == nil || found.Conflict != "" {
		t.Fatalf("find: 보일 수 없는 새 쪽이 붙었습니다: %+v", found)
	}
	var get GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID}}, &get)
	// get은 범위를 보지 않는다: 다른 프로젝트의 후보도 현재 값이면 보인다. 거절·가려진 것은 여전히 빠진다.
	if c := get.Items[0].Conflict; !strings.Contains(c, foreign.Title) || strings.Contains(c, "거절한") || strings.Contains(c, "가려진") {
		t.Fatalf("get: %q", c)
	}
	if c := get.Items[0].Conflict; strings.Contains(c, "외 ") {
		t.Fatalf("get: 새 쪽은 하나여야 합니다: %q", c)
	}
}

// 이스케이프가 큰 제목이어도 conflict 칸이 find 한 줄·get 한 건의 크기 보증을 깨지 않는다.
func TestSurface_conflict_칸이_있어도_글자가_모두_이스케이프돼도_상한을_지킨다(t *testing.T) {
	esc := strings.Repeat("<", 400)
	m := memory.Memory{ID: "m-0123456789ab", Kind: "decision", State: "confirmed", AppliesTo: "repo:" + esc, Title: esc[:200], Topic: esc, StaleReason: esc,
		Body: strings.Repeat("<", 5000), Tags: []string{esc[:50]}, Version: 3, UpdatedAt: 1760000000000,
		Sources: []memory.Source{{Kind: "file", Ref: esc, Excerpt: esc}, {Kind: "file", Ref: esc, Excerpt: esc}},
		Links:   []memory.MemoryLink{{MemoryID: "m-aaaaaaaaaaaa"}}}
	sides := []memory.Memory{{ID: "m-bbbbbbbbbbbb", Title: esc}, {ID: "m-cccccccccccc", Title: esc}}
	conflict := conflictNote(sides)
	if !strings.Contains(conflict, "외 1건") {
		t.Fatalf("conflict %q", conflict)
	}
	line := m
	line.AppliesTo = "repo:github.com/me/app" // 보통의 적용 범위: 이스케이프 폭탄은 제목과 어긋남 칸뿐이다
	for i := 0; i < 8; i++ {                  // 한 번에 8건: 한 줄마다 247룬 안이어야 2,000룬 안이다
		it := findItemWith(line, DetailIndex, "", conflict)
		if n := jsonLen(it); n > findItemBudget {
			t.Fatalf("색인 한 줄 %d룬 > %d", n, findItemBudget)
		}
		if it.Conflict == "" || !strings.HasPrefix(it.Conflict, "어긋나는 새 사실") || it.Title == "" {
			t.Fatalf("conflict와 제목은 남아야 합니다: %+v", it)
		}
	}
	v := memoryViewWith(m, "", conflict, func(string) (string, string) { return esc, "" }, false)
	if n := jsonLen(GetOut{Items: []MemoryView{v}}); n > getBudget {
		t.Fatalf("get 한 건 %d룬 > %d", n, getBudget)
	}
	if v.Conflict == "" {
		t.Fatal("get에서 conflict가 사라졌습니다")
	}
	// find 8건이 2,000룬 안이다.
	items := make([]FindItem, 8)
	for i := range items {
		items[i] = findItemWith(line, DetailIndex, "", conflict)
	}
	n := jsonLen(FindOut{Items: items})
	if n > 2000 {
		t.Fatalf("find 8건 %d룬 > 2000", n)
	}
	t.Logf("최악 자료: find 한 줄 %d룬, 8건 %d룬, get 한 건 %d룬", jsonLen(items[0]), n, jsonLen(GetOut{Items: []MemoryView{v}}))
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

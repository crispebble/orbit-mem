package mcpserver

// 어긋남 칸의 보임 판정(명세 C-5)을 찾기 범위(scope)와 가려진 기억에서 고정한다.

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// 어긋남 칸의 보임 판정은 찾기 범위와 별개로 지금 자리를 본다: global로 찾아도 다른 프로젝트 후보의 제목은 싣지 않고,
// 모든 프로젝트를 보겠다고 한 all에서만 싣는다.
func TestMemoryFind_다른_프로젝트_새_쪽의_제목은_scope가_global이어도_conflict에_싣지_않는다(t *testing.T) {
	cs, s := connectWith(t, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "t"})
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 금요일 오후에만 한다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 금요일 오후에만 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: "다른 프로젝트 후보 제목", Scope: "project:github.com/me/other",
		Who: memory.Who{Actor: memory.ActorAI}, Source: memory.Source{Kind: memory.SourceSession, Ref: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlagConflict(foreign.ID, old.ID, "시험", memory.Who{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		scope string
		shown bool
	}{{"", false}, {"here", false}, {"global", false}, {"all", true}} {
		var out FindOut
		call(t, cs, "memory_find", map[string]any{"query": "배포 일정", "scope": c.scope}, &out)
		var got *FindItem
		for i, it := range out.Items {
			if it.ID == old.ID {
				got = &out.Items[i]
			}
		}
		if got == nil {
			t.Fatalf("scope=%q: 옛 기억이 찾아지지 않았습니다: %+v", c.scope, out.Items)
		}
		if shown := strings.Contains(got.Conflict, foreign.Title); shown != c.shown {
			t.Fatalf("scope=%q: conflict %q, 싣는가 %v want %v", c.scope, got.Conflict, shown, c.shown)
		}
	}
}

// memory_get의 conflict 칸은 현재 값에만 붙는다: 가려진(현재 값이 아닌) 기억에 열린 쌍이 있어도 비어 있다.
func TestMemoryGet_가려진_기억에_열린_쌍이_있어도_conflict_칸은_비어_있다(t *testing.T) {
	cs, s := connect(t)
	old, _ := conflictOf(t, s)
	// 같은 주제의 다른 확정 값이 주제 머리가 되어 old를 가린다.
	head, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 수요일에 한다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 수요일에 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	var out GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID}}, &out)
	if len(out.Items) != 1 || out.Items[0].Conflict != "" {
		t.Fatalf("가려진 기억(머리 %s)의 conflict: %+v", head.ID, out.Items)
	}
}

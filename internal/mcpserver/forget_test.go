package mcpserver

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func saveForgettable(t *testing.T, s *memory.Store, title string) string {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func TestForgetTool_말과_함께_부르면_지우고_안내를_돌려준다(t *testing.T) {
	cs, s := connectAs(t, "")
	a, b := saveForgettable(t, s, "지울 기억 하나"), saveForgettable(t, s, "지울 기억 둘")
	var out ForgetOut
	res := call(t, cs, "memory_forget", map[string]any{"ids": []string{a, b}, "userQuote": "그 둘은 지워줘", "reason": "틀린 내용"}, &out)
	if res.IsError {
		t.Fatalf("도구 오류: %s", text(res))
	}
	if strings.Join(out.Forgotten, ",") != a+","+b {
		t.Fatalf("forgotten = %v", out.Forgotten)
	}
	if out.Note != forgetNote {
		t.Fatalf("note = %q", out.Note)
	}
	if _, err := s.Get(a); err == nil {
		t.Fatal("지운 기억이 남아 있습니다")
	}
}

func TestForgetTool_AI가_말_없이_부르면_도구_오류다(t *testing.T) {
	cs, s := connectAs(t, "")
	id := saveForgettable(t, s, "남아야 하는 기억")
	res := call(t, cs, "memory_forget", map[string]any{"ids": []string{id}}, nil)
	if !res.IsError {
		t.Fatalf("말 없는 AI 호출이 통과했습니다: %s", text(res))
	}
	if _, err := s.Get(id); err != nil {
		t.Fatalf("기억이 지워졌습니다: %v", err)
	}
}

func TestForgetTool_ids가_0개나_21개면_도구_오류다(t *testing.T) {
	cs, s := connectAs(t, memory.ActorUser)
	id := saveForgettable(t, s, "남아야 하는 기억")
	for name, ids := range map[string][]string{"0개": {}, "21개": make([]string, 21)} {
		for i := range ids {
			ids[i] = id
		}
		if res := call(t, cs, "memory_forget", map[string]any{"ids": ids}, nil); !res.IsError {
			t.Errorf("%s: 도구 오류여야 합니다: %s", name, text(res))
		}
	}
	if _, err := s.Get(id); err != nil {
		t.Fatalf("기억이 지워졌습니다: %v", err)
	}
}

func TestForgetTool_없는_id는_도구_오류에_그_id를_담는다(t *testing.T) {
	cs, s := connectAs(t, memory.ActorUser)
	id := saveForgettable(t, s, "같이 지워지면 안 되는 기억")
	res := call(t, cs, "memory_forget", map[string]any{"ids": []string{id, "m-없는id"}}, nil)
	if !res.IsError || !strings.Contains(text(res), "m-없는id") {
		t.Fatalf("res = %v %q", res.IsError, text(res))
	}
	if _, err := s.Get(id); err != nil {
		t.Fatalf("하나라도 없으면 아무것도 지우지 않아야 합니다: %v", err)
	}
}

func TestForgetTool_지운_뒤_다른_기억의_get과_find가_오류_없이_돈다(t *testing.T) {
	cs, s := connectAs(t, memory.ActorUser)
	gone, c := saveForgettable(t, s, "지울 기억"), saveForgettable(t, s, "이어진 기억")
	if _, _, err := s.Link(memory.LinkInput{From: c, To: gone, Origin: memory.ActorUser}); err != nil {
		t.Fatal(err)
	}
	var before GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{c}}, &before)
	if len(before.Items) != 1 || !strings.Contains(strings.Join(before.Items[0].Links, "\n"), gone) {
		t.Fatalf("시험 준비: 연결이 보여야 합니다: %+v", before.Items)
	}
	if res := call(t, cs, "memory_forget", map[string]any{"ids": []string{gone}}, nil); res.IsError {
		t.Fatalf("forget: %s", text(res))
	}
	var got GetOut
	if res := call(t, cs, "memory_get", map[string]any{"ids": []string{c}}, &got); res.IsError {
		t.Fatalf("memory_get: %s", text(res))
	}
	if strings.Contains(strings.Join(got.Items[0].Links, "\n"), gone) {
		t.Fatalf("links에 지운 id가 남았습니다: %v", got.Items[0].Links)
	}
	if res := call(t, cs, "memory_find", map[string]any{"query": "기억", "scope": "all"}, nil); res.IsError {
		t.Fatalf("memory_find: %s", text(res))
	}
}

func TestForgetTool_호출_사건에_말과_이유를_남기지_않는다(t *testing.T) {
	cs, s := connectAs(t, "")
	id := saveForgettable(t, s, "사건 시험 기억")
	const quote, reason = "독특한사용자말", "독특한이유글"
	if res := call(t, cs, "memory_forget", map[string]any{"ids": []string{id}, "userQuote": quote, "reason": reason}, nil); res.IsError {
		t.Fatalf("forget: %s", text(res))
	}
	evs, err := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventToolPrefix + "memory_forget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("memory_forget 사건 %d개", len(evs))
	}
	data := string(evs[0].Data)
	if strings.Contains(data, quote) || strings.Contains(data, reason) || !strings.Contains(data, `"count":1`) {
		t.Fatalf("data = %s", data)
	}
}

package mcpserver

// 설계 · orbit-mem 2판 S2: 규칙·결정은 주제가 있어야 저장되고(4.2), 주장 하나만 담으며(4.3.1, AC-9), supersedes로
// 옛 판을 같은 트랜잭션에서 대체하고, 도구 트리거(tools)를 단다(6절).

import (
	"strings"
	"testing"
)

func quote(words string) map[string]any { return map[string]any{"kind": "quote", "excerpt": words} }

func TestSave_규칙_결정은_주제가_없으면_거절하고_후보_주제와_비슷한_현재값을_보인다(t *testing.T) {
	cs, _ := connect(t)
	var first MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "상단바 상태는 레일에 둔다", "topic": "상단바 상태 표시", "source": quote("레일에 둬")}, &first)
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "appliesTo": "user", "title": "보고는 한국어로", "topic": "보고 언어", "source": quote("한국어로")}, nil)
	for _, kind := range []string{"rule", "decision"} {
		res := call(t, cs, "memory_save", map[string]any{"kind": kind, "appliesTo": "user", "title": "상단바 상태는 알약으로", "source": quote("알약으로")}, nil)
		msg := text(res)
		if !res.IsError || !strings.Contains(msg, "topic") || !strings.Contains(msg, "상단바 상태 표시") || !strings.Contains(msg, "보고 언어") ||
			!strings.Contains(msg, first.Memory.ID) {
			t.Fatalf("%s without topic: %v %s", kind, res.IsError, msg)
		}
	}
	// 사실·교훈은 주제가 선택이다.
	if res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "빌드는 go build", "source": quote("x")}, nil); res.IsError {
		t.Fatalf("fact refused: %s", text(res))
	}
}

func TestSave_규칙_결정_본문이_1000자를_넘으면_나눠_저장하라고_거절한다(t *testing.T) {
	cs, _ := connect(t)
	long := strings.Repeat("가", 1001)
	res := call(t, cs, "memory_save", map[string]any{"kind": "rule", "appliesTo": "user", "title": "긴 규칙", "topic": "긴 것", "body": long, "source": quote("x")}, nil)
	if !res.IsError || !strings.Contains(text(res), "나눠") {
		t.Fatalf("long rule: %v %s", res.IsError, text(res))
	}
	if res := call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "긴 교훈", "body": long, "source": quote("x")}, nil); res.IsError {
		t.Fatalf("long lesson refused: %s", text(res))
	}
	var ok MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "appliesTo": "user", "title": "짧은 규칙", "topic": "짧은 것", "body": strings.Repeat("가", 1000), "source": quote("x")}, &ok)
	if res := call(t, cs, "memory_update", map[string]any{"id": ok.Memory.ID, "body": long, "userQuote": "길게 고쳐"}, nil); !res.IsError || !strings.Contains(text(res), "나눠") {
		t.Fatalf("long edit: %v %s", res.IsError, text(res))
	}
}

func TestSave_supersedes와_같은_주제는_옛_판을_대체하고_응답에_싣는다(t *testing.T) {
	cs, s := connect(t)
	var a, b, c MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "상단바는 알약", "topic": "상단바 상태 표시", "source": quote("알약으로")}, &a)
	call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "제목 막대는 아일랜드", "topic": "제목 막대", "source": quote("아일랜드로")}, &b)
	call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "상단바 상태는 알약으로 하지 않는다", "topic": "상단바 상태 표시",
		"supersedes": []string{b.Memory.ID}, "source": quote("알약은 안 해")}, &c)
	for _, id := range []string{a.Memory.ID, b.Memory.ID} {
		m, _ := s.Get(id)
		if m.State != "stale" || m.SupersededBy != c.Memory.ID {
			t.Fatalf("%s = %s by %s", id, m.State, m.SupersededBy)
		}
		if !strings.Contains(strings.Join(c.Superseded, ","), id) {
			t.Fatalf("response lacks %s: %+v", id, c)
		}
	}
	var found FindOut
	call(t, cs, "memory_find", map[string]any{"query": "알약"}, &found)
	for _, it := range found.Items {
		if it.ID == a.Memory.ID || it.ID == b.Memory.ID {
			t.Fatalf("memory_find shows an old value: %+v", found.Items)
		}
	}
}

func TestSave_도구_트리거를_저장하고_고친다(t *testing.T) {
	cs, _ := connect(t)
	var m MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "appliesTo": "user", "title": "Orbit 문서는 Orbit Space에", "topic": "atlas 문서 위치",
		"tools": []string{"mcp__atlas__write_page"}, "source": quote("Orbit Space에 써")}, &m)
	if strings.Join(m.Memory.Tools, ",") != "mcp__atlas__write_page" {
		t.Fatalf("tools %v", m.Memory.Tools)
	}
	var u MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": m.Memory.ID, "tools": []string{"mcp__atlas__*"}, "userQuote": "atlas 도구 전부에 걸어"}, &u)
	if strings.Join(u.Memory.Tools, ",") != "mcp__atlas__*" {
		t.Fatalf("tools %v", u.Memory.Tools)
	}
}

func TestInstructions_임시인_말과_애매하면_묻기를_안내한다(t *testing.T) {
	for _, want := range []string{"topic", "supersedes", "volatile", "묻는다", "1,000자", "tools"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
}

func TestSave_정규화하면_빈_주제와_종류만_바꾼_긴_본문도_거절한다(t *testing.T) {
	cs, _ := connect(t)
	if res := call(t, cs, "memory_save", map[string]any{"kind": "rule", "appliesTo": "user", "title": "x", "topic": "   ", "source": quote("x")}, nil); !res.IsError {
		t.Fatal("blank topic accepted")
	}
	var f MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "긴 교훈", "body": strings.Repeat("가", 1001), "source": quote("x")}, &f)
	if res := call(t, cs, "memory_update", map[string]any{"id": f.Memory.ID, "kind": "rule", "userQuote": "규칙으로"}, nil); !res.IsError || !strings.Contains(text(res), "나눠") {
		t.Fatalf("kind change let a long rule in: %s", text(res))
	}
}

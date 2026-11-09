package inject

// 같은 시각(UpdatedAt)인 기억끼리의 순서는 id 오름차순으로 고정한다(같은 입력 → 같은 회상).

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestPrompt_같은_시각_같은_점수의_기억은_id_순이고_매번_같다(t *testing.T) {
	s := store(t)
	fixed := time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return fixed }
	var all []string
	for i := 0; i < 5; i++ {
		title := fmt.Sprintf("부가세 신고 항목 %d번", i)
		m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Body: "본문 " + title, Scope: memory.ScopeGlobal,
			State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, m.ID)
	}
	slices.Sort(all)
	first := PromptInjection(s, config.Default(), here, "부가세 신고 항목 확인")
	if !slices.Equal(first.MemoryIDs, all[:maxRecallLines]) {
		t.Fatalf("id 순 앞 %d개가 아닙니다: %v, 기대 %v\n%s", maxRecallLines, first.MemoryIDs, all[:maxRecallLines], first.Text)
	}
	for i := 0; i < 20; i++ {
		got := PromptInjection(s, config.Default(), here, "부가세 신고 항목 확인")
		if got.Text != first.Text || !slices.Equal(got.MemoryIDs, first.MemoryIDs) {
			t.Fatalf("%d번째 회상이 다릅니다:\n%s\n---\n%s", i, first.Text, got.Text)
		}
	}
}

func TestToolInjection_같은_시각의_기억은_id_순으로_들어간다(t *testing.T) {
	s := store(t)
	fixed := time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return fixed }
	var all []string
	for i := 0; i < 5; i++ {
		all = append(all, saveTool(t, s, fmt.Sprintf("문서 규칙 %d", i), "본문", memory.ScopeGlobal, []string{docsTool}, true).ID)
	}
	slices.Sort(all)
	for i := 0; i < 20; i++ {
		inj := ToolInjection(s, config.Default(), here, "h", fmt.Sprintf("s%d", i), docsTool)
		if !slices.Equal(inj.MemoryIDs, all[:MaxToolMemories]) {
			t.Fatalf("%d번째: %v, 기대 %v", i, inj.MemoryIDs, all[:MaxToolMemories])
		}
	}
}

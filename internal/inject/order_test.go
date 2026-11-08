package inject

// 같은 시각(UpdatedAt)인 기억끼리의 순서는 id 오름차순으로 고정한다(같은 입력 → 같은 회상).

import (
	"fmt"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestPrompt_같은_시각의_분야_신호는_id_순이고_매번_같다(t *testing.T) {
	s := store(t)
	fixed := time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return fixed }
	for i := 0; i < 5; i++ {
		title := fmt.Sprintf("부가세 신고 항목 %d번", i)
		if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Body: "본문 " + title, Tags: []string{"분야:세무"}, Scope: memory.ScopeGlobal,
			State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}}); err != nil {
			t.Fatal(err)
		}
	}
	first := PromptInjection(s, config.Default(), here, "부가세 신고 기한 확인해줘")
	if len(first.MemoryIDs) < 5 {
		t.Fatalf("분야 신호 5개가 모두 있어야 합니다: %v\n%s", first.MemoryIDs, first.Text)
	}
	re := regexp.MustCompile(`(?m)\(([^()\s]+)\)$`)
	var ids []string
	for _, m := range re.FindAllStringSubmatch(first.Text, -1) {
		ids = append(ids, m[1])
	}
	if !slices.IsSorted(ids) || len(ids) < 5 {
		t.Errorf("분야 신호 줄이 id 순이 아닙니다: %v", ids)
	}
	for i := 0; i < 20; i++ {
		got := PromptInjection(s, config.Default(), here, "부가세 신고 기한 확인해줘")
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

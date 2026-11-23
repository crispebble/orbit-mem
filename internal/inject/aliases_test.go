package inject

// 별칭(명세 Q-1)은 찾는 데만 쓴다: 요청에 별칭 낱말이 있으면 그 기억이 회상되지만, 회상 줄에는 별칭이 실리지 않는다.

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestRecallLine_별칭은_주입_줄에_없다(t *testing.T) {
	s := store(t)
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "배포 전에 점검표를 먼저 본다", Body: "점검표 항목은 문서에 있다",
		Scope: here, Topic: "배포 점검", Aliases: []string{"릴리스 체크리스트", "go-live"}, Tools: []string{"Bash"},
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포 전에 점검표를 먼저 본다"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	got := PromptInjection(s, cfg, here, "릴리스 체크리스트 확인해 줘")
	if !strings.Contains(got.Text, m.ID) {
		t.Fatalf("a request with the alias words did not recall the memory:\n%s", got.Text)
	}
	tool := ToolInjection(s, cfg, here, "claude", "S", "Bash", "ls")
	if !strings.Contains(tool.Text, m.ID) {
		t.Fatalf("the tool recall did not bring the memory:\n%s", tool.Text)
	}
	for name, text := range map[string]string{"request": got.Text, "tool": tool.Text} {
		for _, alias := range []string{"릴리스", "체크리스트", "go-live"} {
			if strings.Contains(text, alias) {
				t.Errorf("%s recall line carries the alias %q:\n%s", name, alias, text)
			}
		}
	}
}

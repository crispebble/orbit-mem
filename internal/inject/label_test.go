package inject

// 기억 문맥의 머리말은 기억 시스템 자신의 이름 orbit-mem을 쓴다(2026-11-05 옛 제품 이름 폐기).
// 세션 시작·요청 회상·도구 직전·서브에이전트·세션 중 정정 줄이 모두 같은 이름을 쓰는지 본다.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// memName is the memory system's name the headings must carry.
const memName = "orbit-mem"

// headLabel finds the name in a memory context heading: 「[<이름> 기억 · …]」 or 「[<이름> 관련 기억]」.
var headLabel = regexp.MustCompile(`\[(\S+) (?:관련 )?기억[ \]]`)

func checkLabels(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	if text == "" {
		t.Fatalf("%s: empty", what)
	}
	ms := headLabel.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		t.Fatalf("%s: no memory heading:\n%s", what, text)
	}
	for _, m := range ms {
		if m[1] != memName {
			t.Errorf("%s: heading names %q, want %q:\n%s", what, m[1], memName, text)
		}
	}
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: missing %q:\n%s", what, w, text)
		}
	}
}

func TestLabel_기억_문맥_머리말은_orbit_mem이다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	userCore := saveIn(t, s, memory.KindRule, "핵심: 답은 존댓말", memory.ScopeGlobal, "", "말투")
	deskCore := saveIn(t, s, memory.KindDecision, "핵심: 레일", "product:orbit-code-desktop", "", "레일")
	for _, m := range []memory.Memory{userCore, deskCore} {
		if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
			t.Fatal(err)
		}
	}
	saveIn(t, s, memory.KindRule, "보고는 표로 쓴다", memory.ScopeGlobal, "", "보고 꼴")
	tool, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "문서는 Atlas에 쓴다", Body: "로컬 문서를 만들지 않는다", Topic: "문서 위치",
		Tools: []string{"mcp__atlas__write_page"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "문서는 Atlas에"}})
	if err != nil {
		t.Fatal(err)
	}

	start := SessionBundle(s, cfg, f["제품 저장소"], "claude", "A")
	checkLabels(t, "session start", start.Text,
		"["+memName+" 기억 · 상시 핵심]", "["+memName+" 기억 · orbit-code-desktop 핵심]", "["+memName+" 기억 · 먼저 찾는 곳]", memName+" MCP(memory_find")

	rulesCfg := cfg
	rulesCfg.Inject = config.InjectRules
	checkLabels(t, "rules form", SessionStartInjection(s, rulesCfg, f["무관 저장소"]).Text, "["+memName+" 기억 · 확정 규칙]")

	checkLabels(t, "prompt recall", PromptInjection(s, cfg, f["무관 저장소"], "보고는 표로 쓰나").Text, "["+memName+" 관련 기억]")
	checkLabels(t, "pre-tool", ToolInjection(s, cfg, f["무관 저장소"], "claude", "A", "mcp__atlas__write_page").Text,
		"["+memName+" 기억 · mcp__atlas__write_page 호출 직전]", tool.ID)
	checkLabels(t, "subagent", SubagentStart(s, cfg, f["무관 저장소"], "보고는 표로 쓰나").Text, "["+memName+" 기억 · 상시 핵심]")

	// 세션 도중 대체된 핵심 기억은 정정 줄로 온다.
	later(s, time.Hour)
	if _, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "핵심: 답은 존댓말, 짧게", Topic: "말투",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "핵심: 답은 존댓말, 짧게"}}); err != nil {
		t.Fatal(err)
	}
	checkLabels(t, "session news", PromptInjectionFor(s, cfg, f["제품 저장소"], "오늘 점심 뭐 먹지", "claude", "A", Options{}).Text,
		"["+memName+" 기억 · 이 세션 중 바뀐 것]")
}

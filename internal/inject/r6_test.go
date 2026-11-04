package inject

// S3-R6·R7(명세 2.7·2.7b, 조사 0s4gere91nvht 9절): 세션 시작에는 상시 핵심만 넣고 「먼저 찾는 곳」 한 줄을 붙인다.
// 핵심 밖 확정 규칙은 요청 회상·도구 직전·memory_find로만 들어온다.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func TestR6_세션_시작에는_상시_핵심만_넣고_먼저_찾는_곳을_알린다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	var rules []memory.Memory
	for i := 0; i < 30; i++ {
		rules = append(rules, saveIn(t, s, memory.KindRule, fmt.Sprintf("확정 규칙 %d", i), memory.ScopeGlobal, "", fmt.Sprintf("규칙 %d", i)))
	}
	dec := saveIn(t, s, memory.KindDecision, "최근 결정 하나", memory.ScopeGlobal, "", "결정")
	userCore := saveIn(t, s, memory.KindRule, "핵심: 답은 존댓말", memory.ScopeGlobal, "", "말투")
	deskCore := saveIn(t, s, memory.KindDecision, "핵심: 레일 유지", "product:orbit-code-desktop", "", "레일")
	for _, m := range []memory.Memory{userCore, deskCore} {
		if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, brief := range []bool{false, true} {
		in := SessionStartInjectionWith(s, cfg, f["시안 폴더"], Options{Brief: brief})
		for _, r := range rules {
			if strings.Contains(in.Text, r.ID) {
				t.Fatalf("brief %v: non-core rule %s in session start:\n%s", brief, r.Title, in.Text)
			}
		}
		if !strings.Contains(in.Text, userCore.ID) || !strings.Contains(in.Text, deskCore.ID) || !strings.Contains(in.Text, dec.ID) {
			t.Fatalf("brief %v: core or decision titles missing:\n%s", brief, in.Text)
		}
		// 먼저 찾는 곳: 이 폴더에서 보이는 핵심 밖 확정 규칙 30개·결정 1개(핵심 결정은 뺌), memory_find를 가장 먼저.
		if !strings.Contains(in.Text, "먼저 찾는 곳") || !strings.Contains(in.Text, "확정 규칙 30개") || !strings.Contains(in.Text, "결정 1개") ||
			!strings.Contains(in.Text, "memory_find") || strings.Contains(in.Text, "확정 규칙 30개 더") {
			t.Fatalf("brief %v: find line:\n%s", brief, in.Text)
		}
		if in.Omitted != 0 || in.Chars > MaxSessionStartRunes-sessionNoteRunes {
			t.Fatalf("brief %v: omitted %d chars %d", brief, in.Omitted, in.Chars)
		}
	}
	// 세션 시작에 없는 확정 규칙은 모두 요청 회상 후보다(R7, AC-5).
	bundle := SessionStartInjection(s, cfg, f["시안 폴더"]).MemoryIDs
	byID, why, err := CandidatesAt(s, at(cfg, f["시안 폴더"]), Options{}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if _, ok := byID[r.ID]; !ok {
			t.Fatalf("rule %s not a recall candidate: %s", r.Title, why[r.ID])
		}
	}
	// 요청에 맞으면 들어온다.
	if text := PromptInjection(s, cfg, f["시안 폴더"], "확정 규칙 7").Text; !strings.Contains(text, rules[7].ID) {
		t.Fatalf("matching rule not recalled:\n%s", text)
	}
}

// inject: rules는 요청 회상이 없어 예전처럼 남는 칸을 확정 규칙으로 채운다.
func TestR6_inject_rules는_규칙을_채운다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	cfg.Inject = config.InjectRules
	r := saveIn(t, s, memory.KindRule, "답은 존댓말", memory.ScopeGlobal, "", "말투")
	if in := SessionStartInjection(s, cfg, s3Folders(t)["시안 폴더"]); !strings.Contains(in.Text, r.ID) {
		t.Fatalf("rules mode:\n%s", in.Text)
	}
}

package inject

// S3-R6·R7(명세 2.7·2.7b, 조사 0s4gere91nvht 9절): 세션 시작에는 상시 핵심만 넣고 「먼저 찾는 곳」 한 줄을 붙인다.
// 핵심 밖 확정 규칙은 요청 회상·도구 직전·memory_find로만 들어온다.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
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
	in := SessionStartInjection(s, cfg, f["시안 폴더"])
	for _, r := range rules {
		if strings.Contains(in.Text, r.ID) {
			t.Fatalf("non-core rule %s in session start:\n%s", r.Title, in.Text)
		}
	}
	// 핵심 밖 결정은 「최근 결정」 제목 줄로만 온다(1-6 b6r): 본문은 없다.
	if !strings.Contains(in.Text, userCore.ID) || !strings.Contains(in.Text, deskCore.ID) || !strings.Contains(in.Text, recentHead+" 본문은 memory_get — "+dec.Title+"("+dec.ID+")") {
		t.Fatalf("core missing or no decision title line in:\n%s", in.Text)
	}
	// 먼저 찾는 곳: 개수 없는 고정 글 한 줄(7판 4절).
	if !strings.Contains(in.Text, wantFindLine) || strings.Contains(in.Text, "30개") {
		t.Fatalf("find line:\n%s", in.Text)
	}
	if in.Omitted != 0 || in.Chars > MaxSessionStartRunes {
		t.Fatalf("omitted %d chars %d", in.Omitted, in.Chars)
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

// 검토 지적(2026-11-04): 여러 제품 폴더에서 묶음에 들지 않은 제품 핵심도 「먼저 찾는 곳」 줄을 부른다.
func TestR6_묶음에_들지_않은_핵심도_찾는_곳_줄을_부른다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	d := saveIn(t, s, memory.KindDecision, "핵심: 레일 유지", "product:orbit-code-desktop", "", "레일")
	if _, err := s.Pin(d.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	in := SessionStartInjection(s, cfg, s3Folders(t)["상위 폴더"])
	if strings.Contains(in.Text, d.ID) || in.Text != wantFindLine+"\n기억은 "+Label+" MCP(memory_find·memory_get·memory_save)로 찾고 남긴다." {
		t.Fatalf("multi folder:\n%s", in.Text)
	}
}

// 검토 지적(2026-11-04): 핵심 두 층이 가득하고 제품 이름이 가장 길고 결정·회고 줄이 다 차도 한도 안이다.
func TestR6_가장_긴_세션_시작도_한도_안이다(t *testing.T) {
	s := store(t)
	name := strings.Repeat("p", 64)
	cfg := config.Default()
	cfg.Products = map[string]place.Product{name: {Match: []string{"github.com/me/app"}}}
	fill := func(scope string, limit int) {
		for i := 0; ; i++ {
			m, err := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: fmt.Sprintf("핵심 %s %d", scope[:4], i), Topic: fmt.Sprintf("%s %d", scope, i), Scope: scope,
				Body: strings.Repeat("가", 300), Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
				break
			}
		}
		if used := memory.CoreUsed(mustCore(t, s, scope)); used < limit-400 {
			t.Fatalf("%s core %d", scope, used)
		}
	}
	fill(memory.ScopeGlobal, memory.UserCoreRunes)
	fill("product:"+name, memory.ProductCoreRunes)
	for i := 0; i < 300; i++ {
		saveIn(t, s, memory.KindDecision, fmt.Sprintf("결정 제목이 꽤 길다 %d %s", i, strings.Repeat("나", 40)), memory.ScopeGlobal, "", fmt.Sprintf("결정 %d", i))
		saveIn(t, s, memory.KindRule, fmt.Sprintf("규칙 %d", i), memory.ScopeGlobal, "", fmt.Sprintf("규칙 %d", i))
	}
	if _, err := s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at,journal_id) VALUES('r1','daily','2026-11-04','done',?,?,'m-000000000000')`,
		s.Now().UnixMilli(), s.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	in := SessionStartInjection(s, cfg, "project:github.com/me/app")
	// 핵심이 가득하면 꼬리(자동 되돌림 줄)는 빠질 수 있다. 찾는 곳 줄과 한도는 지킨다.
	if in.Chars > MaxSessionStartRunes || !strings.Contains(in.Text, wantFindLine) {
		t.Fatalf("%d chars\n%s", in.Chars, in.Text)
	}
}

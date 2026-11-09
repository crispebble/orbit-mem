package inject

// 설계 · orbit-mem 2판 S3(명세 0s4fsq3hy8pnp): AC-3 제품 범위 기억은 그 제품의 저장소·상위 폴더·시안 폴더에서 보이고
// 다른 제품·무관 저장소에서는 보이지 않는다(S3-1). 세션 시작은 user 핵심 → 제품 핵심 → 나머지 규칙 순이고(S3-6),
// 여러 제품 폴더는 요청 글에 이름이 나온 제품을 앞에 둔다(S3-9).

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// s3Config is the fixed product map: two products share the parent folder ~/_workspace/onidot.
func s3Config() config.Config {
	cfg := config.Default()
	cfg.Products = place.Map{
		"orbit-code-desktop": {
			Match: []string{"github.com/onidot-labs/orbit-studio", "~/_workspace/onidot/prototypes/orbit-desktop-*", "~/_workspace/onidot"},
			Names: []string{"Orbit 데스크탑"},
		},
		"atlas": {Match: []string{"github.com/onidot-labs/atlas", "~/_workspace/onidot"}, Names: []string{"Atlas"}},
	}
	return cfg
}

// s3Folders are the folders of the AC-3 table as scope keys.
func s3Folders(t *testing.T) map[string]string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"제품 저장소": "project:github.com/onidot-labs/orbit-studio",
		"상위 폴더":  "project:" + h + "/_workspace/onidot",
		"시안 폴더":  "project:" + h + "/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03",
		"다른 제품":  "project:github.com/onidot-labs/atlas",
		"무관 저장소": "project:github.com/someone/unrelated",
	}
}

func saveIn(t *testing.T, s *memory.Store, kind, title, scope, project, topic string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: kind, Title: title, Scope: scope, Project: project, Topic: topic, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAC3_제품_범위_기억은_그_제품의_폴더에서만_회상된다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	desk := saveIn(t, s, memory.KindDecision, "상단바 시안 결정은 레일", "product:orbit-code-desktop", "", "상단바")
	atlas := saveIn(t, s, memory.KindDecision, "상단바 시안 결정은 검색칸", "product:atlas", "", "상단바")
	unsure := saveIn(t, s, memory.KindFact, "상단바 시안 결정 폴더의 빌드 명령", "product:?", strings.TrimPrefix(f["시안 폴더"], "project:"), "")
	repo := saveIn(t, s, memory.KindFact, "상단바 시안 결정 무관 저장소 사실", f["무관 저장소"], "github.com/someone/unrelated", "")
	user := saveIn(t, s, memory.KindDecision, "상단바 시안 결정은 사용자 공통", memory.ScopeGlobal, "", "공통")
	want := map[string][]memory.Memory{
		"제품 저장소": {desk, user},
		"상위 폴더":  {desk, atlas, user},
		"시안 폴더":  {desk, unsure, user},
		"다른 제품":  {atlas, user},
		"무관 저장소": {repo, user},
	}
	all := []memory.Memory{desk, atlas, unsure, repo, user}
	for name, scope := range f {
		text := PromptInjection(s, cfg, scope, "상단바 시안 결정 다시 보자").Text
		for _, m := range all {
			shown := strings.Contains(text, m.ID)
			exp := false
			for _, w := range want[name] {
				exp = exp || w.ID == m.ID
			}
			if shown != exp {
				t.Errorf("%s: %s(%s) shown %v, want %v\n%s", name, m.Title, m.Scope, shown, exp, text)
			}
		}
	}
}

func TestS3_세션_시작은_user_핵심_제품_핵심_순이고_나머지_규칙은_넣지_않는다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	rule := saveIn(t, s, memory.KindRule, "나머지 확정 규칙", memory.ScopeGlobal, "", "나머지")
	userCore := saveIn(t, s, memory.KindRule, "핵심: 답은 존댓말", memory.ScopeGlobal, "", "말투")
	deskCore := saveIn(t, s, memory.KindDecision, "핵심: 왼쪽 얇은 레일 유지", "product:orbit-code-desktop", "", "레일")
	atlasCore := saveIn(t, s, memory.KindDecision, "핵심: Atlas 문서는 onidot Space", "product:atlas", "", "문서 위치")
	for _, m := range []memory.Memory{userCore, deskCore, atlasCore} {
		if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
			t.Fatal(err)
		}
	}
	order := func(text string, ms ...memory.Memory) bool {
		at := -1
		for _, m := range ms {
			i := strings.Index(text, m.ID)
			if i <= at {
				return false
			}
			at = i
		}
		return true
	}
	in := SessionStartInjection(s, cfg, f["시안 폴더"])
	// 핵심 밖 규칙은 넣지 않고 「먼저 찾는 곳」 줄이 핵심 뒤에 온다(S3-R6).
	if !order(in.Text, userCore, deskCore) || strings.Contains(in.Text, rule.ID) || strings.Index(in.Text, "먼저 찾는 곳") < strings.Index(in.Text, deskCore.ID) ||
		strings.Contains(in.Text, atlasCore.ID) || in.Chars > MaxSessionStartRunes {
		t.Fatalf("시안 폴더:\n%s", in.Text)
	}
	// 핵심 규칙은 아래 규칙 목록에 다시 나오지 않는다.
	if strings.Count(in.Text, userCore.ID) != 1 {
		t.Fatalf("user core twice:\n%s", in.Text)
	}
	in = SessionStartInjection(s, cfg, f["상위 폴더"])
	if !order(in.Text, userCore) || strings.Contains(in.Text, rule.ID) || strings.Contains(in.Text, deskCore.ID) || strings.Contains(in.Text, atlasCore.ID) {
		t.Fatalf("여러 제품 폴더는 제품 핵심을 넣지 않는다:\n%s", in.Text)
	}
	in = SessionStartInjection(s, cfg, f["다른 제품"])
	if !order(in.Text, userCore, atlasCore) || strings.Contains(in.Text, rule.ID) || strings.Contains(in.Text, deskCore.ID) {
		t.Fatalf("다른 제품:\n%s", in.Text)
	}
	// 승격된 기억은 그 층 이름 아래에 들어간다.
	if !strings.Contains(in.Text, "상시 핵심") || !strings.Contains(in.Text, "atlas") {
		t.Fatalf("layer headings:\n%s", in.Text)
	}
}

func TestS3_여러_제품_폴더는_요청에_이름이_나온_제품을_앞에_둔다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	parent := s3Folders(t)["상위 폴더"]
	desk := saveIn(t, s, memory.KindDecision, "상단바 시안 결정은 레일", "product:orbit-code-desktop", "", "상단바")
	atlas := saveIn(t, s, memory.KindDecision, "상단바 시안 결정은 탭줄", "product:atlas", "", "상단바")
	for prompt, first := range map[string]memory.Memory{
		"Atlas 상단바 시안 결정":      atlas,
		"Orbit 데스크탑 상단바 시안 결정": desk,
	} {
		text := PromptInjection(s, cfg, parent, prompt).Text
		other := desk
		if first.ID == desk.ID {
			other = atlas
		}
		i, j := strings.Index(text, first.ID), strings.Index(text, other.ID)
		if i < 0 || j < 0 || i > j {
			t.Fatalf("%q: %s should come first\n%s", prompt, first.Title, text)
		}
	}
}

func TestSubagentStart_user_핵심만_3000자_안에서_넣는다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	if inj := SubagentStart(s, cfg); inj.Text != "" {
		t.Fatalf("no core, still:\n%s", inj.Text)
	}
	saveIn(t, s, memory.KindRule, "핵심이 아닌 확정 규칙", memory.ScopeGlobal, "", "아님")
	userCore := saveIn(t, s, memory.KindRule, "핵심: 답은 존댓말", memory.ScopeGlobal, "", "말투")
	deskCore := saveIn(t, s, memory.KindDecision, "핵심: 레일", "product:orbit-code-desktop", "", "레일")
	for _, m := range []memory.Memory{userCore, deskCore} {
		if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
			t.Fatal(err)
		}
	}
	_ = f
	inj := SubagentStart(s, cfg)
	if !strings.Contains(inj.Text, userCore.ID) || strings.Contains(inj.Text, deskCore.ID) || strings.Contains(inj.Text, "핵심이 아닌") ||
		inj.Chars > memory.UserCoreRunes+200 || len(inj.MemoryIDs) != 1 {
		t.Fatalf("subagent:\n%s", inj.Text)
	}
}

// 검토 지적(2026-11-04): 핵심을 한도까지 채우고 결정이 많아도 세션 시작 묶음은 6,000자 안이다(안내 줄을 더한 한도는 recall_mode_test).
func TestS3_핵심을_한도까지_채워도_세션_시작은_한도_안이다(t *testing.T) {
	s := store(t)
	cfg := s3Config()
	f := s3Folders(t)
	fill := func(scope string, limit int, word string) {
		for i := 0; ; i++ {
			m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: fmt.Sprintf("%s 핵심 %d", word, i), Topic: fmt.Sprintf("%s %d", word, i), Scope: scope,
				Body: strings.Repeat(word, 470), Source: memory.Source{Kind: memory.SourceQuote, Excerpt: word}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
				break
			}
			if i > 20 {
				t.Fatal("never full")
			}
		}
		if used := memory.CoreUsed(mustCore(t, s, scope)); used < limit-600 {
			t.Fatalf("%s core only %d", scope, used)
		}
	}
	fill(memory.ScopeGlobal, memory.UserCoreRunes, "가")
	fill("product:orbit-code-desktop", memory.ProductCoreRunes, "나")
	for i := 0; i < 15; i++ {
		saveIn(t, s, memory.KindDecision, fmt.Sprintf("긴 결정 제목 %d %s", i, strings.Repeat("다", 60)), memory.ScopeGlobal, "", fmt.Sprintf("결정 %d", i))
		saveIn(t, s, memory.KindRule, fmt.Sprintf("규칙 %d", i), memory.ScopeGlobal, "", fmt.Sprintf("규칙 %d", i))
	}
	in := SessionStartInjection(s, cfg, f["시안 폴더"])
	if in.Chars > MaxSessionStartRunes {
		t.Fatalf("session start %d > %d", in.Chars, MaxSessionStartRunes)
	}
	for _, scope := range []string{memory.ScopeGlobal, "product:orbit-code-desktop"} {
		for _, m := range mustCore(t, s, scope) {
			if !strings.Contains(in.Text, m.ID) {
				t.Fatalf("core %s left out:\n%s", m.ID, in.Text)
			}
		}
	}
}

func mustCore(t *testing.T, s *memory.Store, scope string) []memory.Memory {
	t.Helper()
	c, err := s.Core(scope)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

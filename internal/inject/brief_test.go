package inject

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func TestSessionStart_짧은_꼴은_규칙마다_한_줄이고_한도_안이면_전부_넣는다(t *testing.T) {
	s := store(t)
	long := strings.Repeat("본문이 길다. ", 400)
	var rules []memory.Memory
	for i := 0; i < 30; i++ {
		rules = append(rules, save(t, s, memory.KindRule, "규칙 "+string(rune('가'+i))+" 은 이렇게 한다 (2026-11-01)", long, memory.ScopeGlobal, true))
	}
	save(t, s, memory.KindDecision, "Orbit 먼저 만든다", "", memory.ScopeGlobal, true)

	// 규칙을 세션 시작에 채우는 것은 요청 회상이 없는 inject=rules 판뿐이다(S3-R6).
	full := SessionStartInjection(s, rulesCfg(), here)
	brief := SessionStartInjectionWith(s, rulesCfg(), here, Options{Brief: true})
	if brief.Omitted != 0 || len(brief.MemoryIDs) != 30 {
		t.Fatalf("brief ids=%d omitted=%d", len(brief.MemoryIDs), brief.Omitted)
	}
	for _, m := range rules {
		if !strings.Contains(brief.Text, m.ID) {
			t.Fatalf("rule %s missing:\n%s", m.ID, brief.Text)
		}
	}
	if strings.Contains(brief.Text, "본문이 길다") || strings.Contains(brief.Text, "(2026-11-01)") {
		t.Fatalf("brief carries bodies or dates:\n%s", brief.Text)
	}
	for _, want := range []string{"memory_get", "규칙 가 은 이렇게 한다"} {
		if !strings.Contains(brief.Text, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief.Text)
		}
	}
	if utf8.RuneCountInString(brief.Text) >= utf8.RuneCountInString(full.Text) {
		t.Fatalf("brief %d >= full %d", utf8.RuneCountInString(brief.Text), utf8.RuneCountInString(full.Text))
	}
}

func TestSessionStart_전문_꼴은_그대로다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	if SessionStart(s, rulesCfg(), here) != SessionStartInjectionWith(s, rulesCfg(), here, Options{}).Text {
		t.Fatal("the full form changed")
	}
	if !strings.Contains(SessionStart(s, rulesCfg(), here), "반말을 쓰지 않는다") {
		t.Fatal("full form lost the body")
	}
}

func TestSessionStart_같은_주제는_최신_규칙만_넣는다(t *testing.T) {
	s := store(t)
	old, _ := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 영어로", Topic: "보고 언어", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 영어로"}})
	s.Now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.Local) }
	cur, _ := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 한국어로", Topic: "보고 언어", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 한국어로"}})
	for _, opts := range []Options{{}, {Brief: true}} {
		got := SessionStartInjectionWith(s, rulesCfg(), here, opts).Text
		if strings.Contains(got, old.ID) || !strings.Contains(got, cur.ID) {
			t.Fatalf("brief=%v:\n%s", opts.Brief, got)
		}
	}
}

func TestBundle_호스트마다_꼴을_따로_남긴다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	a := SessionBundleWith(s, config.Default(), here, "orbit-code", "s1", Options{Brief: true})
	b := SessionBundleWith(s, config.Default(), here, "orbit-code", "s1", Options{Brief: true})
	if a.Text != b.Text || strings.Contains(a.Text, "반말을 쓰지 않는다") {
		t.Fatalf("bundle:\n%s", a.Text)
	}
}

// rulesCfg is inject=rules: the one form whose session start still fills in confirmed rules (S3-R6).
func rulesCfg() config.Config {
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	return cfg
}

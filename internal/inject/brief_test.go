package inject

import (
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

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
	if got := SessionStartInjection(s, rulesCfg(), here).Text; strings.Contains(got, old.ID) || !strings.Contains(got, cur.ID) {
		t.Fatalf("%s", got)
	}
}

func TestBundle_호스트마다_묶음을_따로_남긴다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	a := SessionBundleWith(s, rulesCfg(), here, "orbit-code", "s1", nil)
	b := SessionBundleWith(s, rulesCfg(), here, "orbit-code", "s1", nil)
	if a.Text != b.Text || !strings.Contains(a.Text, "반말을 쓰지 않는다") {
		t.Fatalf("bundle:\n%s", a.Text)
	}
}

// rulesCfg is the old key inject=rules (recall=on_demand): the one mode whose session start still fills in
// confirmed rules, in full (S3-R6, 진행 결정 D1·D3).
func rulesCfg() config.Config {
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	return cfg
}

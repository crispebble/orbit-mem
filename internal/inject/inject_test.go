package inject

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
)

func store(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local) }
	t.Cleanup(func() { s.Close() })
	return s
}

func save(t *testing.T, s *memory.Store, kind, title, body, scope string, quote bool) memory.Memory {
	t.Helper()
	src := memory.Source{Kind: memory.SourceSession, Ref: "x"}
	if quote {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: title}
	}
	m, err := s.Save(memory.SaveInput{Kind: kind, Title: title, Body: body, Scope: scope, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const here = "project:github.com/me/app"

func TestSessionStart_확정_규칙_전문과_최근_결정_제목과_제안_수(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	save(t, s, memory.KindRule, "이 앱은 pnpm", "npm을 쓰지 않는다", here, true)
	save(t, s, memory.KindRule, "다른 앱 규칙", "보이면 안 됨", "project:github.com/me/other", true)
	save(t, s, memory.KindRule, "후보 규칙", "확정 전이라 안 보임", memory.ScopeGlobal, false)
	save(t, s, memory.KindDecision, "Orbit 먼저 만든다", "본문은 안 넣음", memory.ScopeGlobal, true)
	c := save(t, s, memory.KindFact, "후보 사실", "", memory.ScopeGlobal, false)
	s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "확정?", MemoryIDs: []string{c.ID}})

	got := SessionStart(s, config.Default(), here)
	for _, want := range []string{"답은 한글 존댓말", "반말을 쓰지 않는다", "이 앱은 pnpm", "npm을 쓰지 않는다", "Orbit 먼저 만든다", "제안 1건"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"다른 앱 규칙", "후보 규칙", "본문은 안 넣음"} {
		if strings.Contains(got, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
}

func TestSessionStart_주입_수준(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "규칙", "본문", memory.ScopeGlobal, true)
	save(t, s, memory.KindDecision, "결정", "", memory.ScopeGlobal, true)
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	got := SessionStart(s, cfg, here)
	if !strings.Contains(got, "규칙") || strings.Contains(got, "결정") {
		t.Fatalf("rules level: %s", got)
	}
	cfg.Inject = config.InjectOff
	if got := SessionStart(s, cfg, here); got != "" {
		t.Fatalf("off level: %q", got)
	}
}

func TestSessionStart_기억이_없으면_빈_문자열(t *testing.T) {
	if got := SessionStart(store(t), config.Default(), here); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestPrompt_강하게_맞는_기억만_최대_3건의_제목과_id(t *testing.T) {
	s := store(t)
	a := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "긴 본문은 넣지 않는다", memory.ScopeGlobal, false)
	save(t, s, memory.KindLesson, "launchd 회고 예약", "", "project:github.com/me/other", true)
	got := Prompt(s, config.Default(), here, "codex exec를 백그라운드에서 돌렸더니 멈춰요")
	if !strings.Contains(got, a.ID) || !strings.Contains(got, a.Title) || strings.Contains(got, "긴 본문") {
		t.Fatalf("got %q", got)
	}
	if got := Prompt(s, config.Default(), here, "오늘 점심 뭐 먹지"); got != "" {
		t.Fatalf("weak prompt: %q", got)
	}
	if got := Prompt(s, config.Default(), here, "launchd 회고 예약 다시"); got != "" {
		t.Fatalf("other project leaked: %q", got)
	}
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	if got := Prompt(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요"); got != "" {
		t.Fatalf("rules level must not add per prompt: %q", got)
	}
}

func TestHookOutput_호스트가_읽는_JSON(t *testing.T) {
	out := HookOutput("SessionStart", "문맥")
	if !strings.Contains(out, `"hookEventName":"SessionStart"`) || !strings.Contains(out, `"additionalContext":"문맥"`) {
		t.Fatal(out)
	}
	if HookOutput("UserPromptSubmit", "") != "" {
		t.Fatal("empty context must print nothing")
	}
}

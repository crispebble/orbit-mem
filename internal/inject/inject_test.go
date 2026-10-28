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

func TestPrompt_강하게_맞는_기억의_제목과_id와_본문_앞부분(t *testing.T) {
	s := store(t)
	a := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "본문 앞부분"+strings.Repeat("가", 300)+"끝부분", memory.ScopeGlobal, false)
	save(t, s, memory.KindLesson, "launchd 회고 예약", "", "project:github.com/me/other", true)
	got := Prompt(s, config.Default(), here, "codex exec를 백그라운드에서 돌렸더니 멈춰요")
	if !strings.Contains(got, a.ID) || !strings.Contains(got, a.Title) || !strings.Contains(got, "본문 앞부분") || strings.Contains(got, "끝부분") {
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

func TestSessionStartInjection_넣은_기억_id와_제목만_넣은_규칙_수(t *testing.T) {
	s := store(t)
	long := strings.Repeat("가", 2500)
	var rules []memory.Memory
	for i := 0; i < 4; i++ {
		rules = append(rules, save(t, s, memory.KindRule, "긴 규칙 "+string(rune('A'+i)), long, memory.ScopeGlobal, true))
	}
	d := save(t, s, memory.KindDecision, "결정 하나", "", memory.ScopeGlobal, true)
	other := save(t, s, memory.KindRule, "다른 앱 규칙", "", "project:github.com/me/other", true)

	inj := SessionStartInjection(s, config.Default(), here)
	if inj.Text != SessionStart(s, config.Default(), here) || inj.Chars == 0 {
		t.Fatalf("text mismatch")
	}
	ids := strings.Join(inj.MemoryIDs, ",")
	for _, m := range append(rules, d) {
		if !strings.Contains(ids, m.ID) {
			t.Errorf("missing %s in %s", m.ID, ids)
		}
	}
	if strings.Contains(ids, other.ID) {
		t.Errorf("other project id leaked")
	}
	if inj.TitleOnly < 1 {
		t.Errorf("titleOnly = %d", inj.TitleOnly)
	}
}

func TestPromptInjection_넣은_기억_id(t *testing.T) {
	s := store(t)
	a := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "", memory.ScopeGlobal, false)
	inj := PromptInjection(s, config.Default(), here, "codex exec를 백그라운드에서 돌렸더니 멈춰요")
	if len(inj.MemoryIDs) != 1 || inj.MemoryIDs[0] != a.ID || inj.Chars == 0 {
		t.Fatalf("inj = %+v", inj)
	}
	if inj := PromptInjection(s, config.Default(), here, "오늘 점심 뭐 먹지"); len(inj.MemoryIDs) != 0 || inj.Text != "" {
		t.Fatalf("weak = %+v", inj)
	}
}

func TestSessionBundle_같은_세션은_처음_묶음을_그대로_쓴다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "첫 규칙", "본문", memory.ScopeGlobal, true)
	first := SessionBundle(s, config.Default(), here, "claude", "s1")
	save(t, s, memory.KindRule, "세션 중에 확정된 규칙", "본문", memory.ScopeGlobal, true)
	again := SessionBundle(s, config.Default(), here, "claude", "s1")
	if again.Text != first.Text || strings.Contains(again.Text, "세션 중에") || strings.Join(again.MemoryIDs, ",") != strings.Join(first.MemoryIDs, ",") {
		t.Fatalf("bundle changed within the session:\n%s\n---\n%s", first.Text, again.Text)
	}
	if next := SessionBundle(s, config.Default(), here, "claude", "s2"); !strings.Contains(next.Text, "세션 중에 확정된 규칙") {
		t.Fatalf("next session must see the new rule:\n%s", next.Text)
	}
	if none := SessionBundle(s, config.Default(), here, "claude", ""); !strings.Contains(none.Text, "세션 중에 확정된 규칙") {
		t.Fatalf("no session id builds afresh")
	}
}

func TestPromptInjection_작업_회상은_순위대로_글자_수_안에서_묶는다(t *testing.T) {
	s := store(t)
	g := save(t, s, memory.KindLesson, "launchd 예약 회고 다시 걸기", "전역 본문", memory.ScopeGlobal, false)
	p := save(t, s, memory.KindLesson, "launchd 예약 회고 다시 걸기 (이 앱)", "프로젝트 본문", here, false)
	cfg := config.Default()
	inj := PromptInjection(s, cfg, here, "launchd 예약 회고를 다시 걸어야 해")
	if len(inj.MemoryIDs) != 2 || inj.MemoryIDs[0] != p.ID || inj.MemoryIDs[1] != g.ID {
		t.Fatalf("project first: %v (p=%s g=%s)\n%s", inj.MemoryIDs, p.ID, g.ID, inj.Text)
	}
	// 같은 점수·같은 범위면 사용 판정(따름 − 상관없음)이 높은 것이 먼저다.
	s2 := store(t)
	a := save(t, s2, memory.KindLesson, "codex exec 백그라운드 stdin 닫기", "가", memory.ScopeGlobal, false)
	b := save(t, s2, memory.KindLesson, "codex exec 백그라운드 stdin 닫기 두번째", "나", memory.ScopeGlobal, false)
	for i, v := range []string{memory.UsageIrrelevant, memory.UsageIrrelevant} {
		s2.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Session: "s" + string(rune('0'+i))}, map[string]any{"memoryId": b.ID, "verdict": v})
	}
	s2.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Session: "s9"}, map[string]any{"memoryId": a.ID, "verdict": memory.UsageFollowed})
	inj = PromptInjection(s2, cfg, here, "codex exec 백그라운드 stdin")
	if len(inj.MemoryIDs) != 2 || inj.MemoryIDs[0] != a.ID {
		t.Fatalf("usage order: %v (a=%s)", inj.MemoryIDs, a.ID)
	}
	// 글자 수 상한: 넘으면 본문을 빼고, 제목도 안 들어가면 멈춘다.
	s3 := store(t)
	var ids []string
	for i := 0; i < 8; i++ {
		m := save(t, s3, memory.KindFact, "회고 예약 launchd 설정 "+string(rune('A'+i)), strings.Repeat("본", 180), memory.ScopeGlobal, false)
		ids = append(ids, m.ID)
	}
	cfg.RecallChars = 400
	inj = PromptInjection(s3, cfg, here, "회고 예약 launchd 설정")
	if inj.Chars > 400 || len(inj.MemoryIDs) == 0 || len(inj.MemoryIDs) == 8 {
		t.Fatalf("budget: %d chars, %d memories\n%s", inj.Chars, len(inj.MemoryIDs), inj.Text)
	}
}

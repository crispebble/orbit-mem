package inject

// 어긋남 줄과 자동 대체 알림은 지금 자리에서 보일 수 있는 기억만 싣는다: 보이지 않는 범위, 낡거나 거절되거나 만료된 새 쪽, 다른
// 머리에 가려진 새 쪽의 제목은 줄에도 지시에도 나오지 않는다. 자동 대체 알림은 범위마다 한 번씩 받는다.

import (
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const otherHere = "project:github.com/me/other"

// pinnedRule saves a confirmed user rule in the global scope and pins it into the core layer.
func pinnedRule(t *testing.T, s *memory.Store, title string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: title, Body: "늘 이렇게 한다", Topic: "답 언어", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return m
}

// candidateIn saves a candidate the AI learned in scope and marks it as going against old.
func candidateIn(t *testing.T, s *memory.Store, in memory.SaveInput, old memory.Memory) memory.Memory {
	t.Helper()
	in.Who, in.Source = memory.Who{Actor: memory.ActorAI}, memory.Source{Kind: memory.SourceSession, Ref: "s"}
	m, _, err := s.SaveWith(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlagConflict(m.ID, old.ID, "시험", memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRecallLine_다른_프로젝트_범위의_새_쪽은_제목도_지시도_싣지_않는다(t *testing.T) {
	s := store(t)
	core := pinnedRule(t, s, "답은 항상 한글로 한다")
	foreign := candidateIn(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "다른 프로젝트의 영어 답 후보", Scope: otherHere}, core)
	cfg := config.Default()
	// 세션 시작의 핵심 줄.
	start := SessionBundleWith(s, cfg, here, "claude", "A", nil)
	if strings.Contains(start.Text, foreign.Title) || strings.Contains(start.Text, foreign.ID) || strings.Contains(start.Text, askMark) || strings.Contains(start.Text, sideMark) {
		t.Fatalf("보이지 않는 범위의 후보가 핵심 줄에 실렸습니다:\n%s", start.Text)
	}
	// 그 후보의 프로젝트 자리에서는 보인다.
	if own := SessionBundleWith(s, cfg, otherHere, "claude", "B", nil); !strings.Contains(own.Text, sideMark+": 「"+foreign.Title+"」("+foreign.ID+")") {
		t.Fatalf("제 자리에서는 보여야 합니다:\n%s", own.Text)
	}
	// 요청 회상과 도구 직전 줄도 같다.
	fact, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 금요일 오후에만 한다", Body: "릴리스는 금요일 오후 배포 창에서만 나간다", Topic: "배포 일정", Scope: memory.ScopeGlobal,
		Tools: []string{"Bash"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 금요일 오후에만 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	foreign2 := candidateIn(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "다른 프로젝트는 화요일에 배포", Scope: otherHere}, fact)
	req := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "C", Options{})
	tool := ToolInjection(s, cfg, here, "claude", "C", "Bash", "")
	for _, text := range []string{req.Text, tool.Text} {
		if !strings.Contains(text, fact.ID) || strings.Contains(text, foreign2.Title) || strings.Contains(text, askMark) {
			t.Fatalf("보이지 않는 범위의 새 쪽이 실렸습니다:\n%s", text)
		}
	}
}

func TestRecallLine_만료된_새_쪽은_열흘_뒤에도_붙지_않는다(t *testing.T) {
	s := store(t)
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: conflictOldTitle, Body: "릴리스는 금요일 오후 배포 창에서만 나간다", Topic: "배포 일정", Scope: here,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: conflictOldTitle}})
	if err != nil {
		t.Fatal(err)
	}
	temp := candidateIn(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "이번 주만 화요일 배포", Scope: here, Retention: memory.RetentionVolatile, TTLDays: 1}, old)
	cfg := config.Default()
	if got := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "A", Options{}); !strings.Contains(lineOf(got.Text, old.ID), temp.Title) {
		t.Fatalf("만료 전에는 보여야 합니다:\n%s", got.Text)
	}
	later(s, 10*24*time.Hour)
	got := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "B", Options{})
	if line := lineOf(got.Text, old.ID); line == "" || strings.Contains(line, temp.Title) || strings.Contains(line, askMark) {
		t.Fatalf("만료된 새 쪽이 붙었습니다:\n%s", got.Text)
	}
}

func TestRecallLine_다른_머리에_가려진_새_쪽과_거절된_새_쪽은_붙지_않는다(t *testing.T) {
	s := store(t)
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: conflictOldTitle, Body: "릴리스는 금요일 오후 배포 창에서만 나간다", Topic: "배포 일정", Scope: here,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: conflictOldTitle}})
	if err != nil {
		t.Fatal(err)
	}
	masked := candidateIn(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "다른 머리에 가려진 후보", Topic: "다른 주제", Scope: here}, old)
	if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "다른 주제의 확정 사실", Topic: "다른 주제", Scope: here,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "다른 주제의 확정 사실"}}); err != nil {
		t.Fatal(err)
	}
	rejected := candidateIn(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "거절한 후보", Scope: here}, old)
	if _, err := s.SetState(rejected.ID, memory.StateHidden, "틀림"); err != nil {
		t.Fatal(err)
	}
	got := PromptInjectionFor(s, config.Default(), here, conflictPrompt, "claude", "A", Options{})
	if line := lineOf(got.Text, old.ID); line == "" || strings.Contains(line, masked.Title) || strings.Contains(line, rejected.Title) || strings.Contains(line, askMark) {
		t.Fatalf("보일 수 없는 새 쪽이 붙었습니다:\n%s", got.Text)
	}
}

// 쌍 질의에서 옛 쪽의 상태 조건을 지우면 낡은 옛 쪽의 쌍이 남아 개수 줄이 0이 되지 않는다.
func TestSessionStart_옛_쪽이_대체된_뒤에는_넘어간_어긋남_개수_줄이_0건이다(t *testing.T) {
	s := store(t)
	old, _ := conflictPair(t, s)
	on := config.Default()
	on.AutoMode = true
	if text := SessionBundleWith(s, on, here, "claude", "A", nil).Text; !strings.Contains(text, "넘어간 어긋남 1건") {
		t.Fatalf("1건 줄이 없습니다:\n%s", text)
	}
	if _, err := s.SetState(old.ID, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if text := SessionBundleWith(s, on, here, "claude", "B", nil).Text; strings.Contains(text, "넘어간 어긋남") {
		t.Fatalf("옛 쪽이 낡았는데 줄이 있습니다:\n%s", text)
	}
}

// replaceIn saves a user's fact and then a checked fact on its topic in scope: the second replaces the first by itself.
func replaceIn(t *testing.T, s *memory.Store, scope, topic string) (old, nw memory.Memory) {
	t.Helper()
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 make all로 한다 " + topic, Topic: topic, Scope: scope,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "빌드는 make all로 한다 " + topic}})
	if err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 go build로 한다 " + topic, Topic: topic, Scope: scope,
		Who:    memory.Who{Actor: memory.ActorAI},
		Source: memory.Source{Kind: memory.SourceVerification, Ref: "/repo/Makefile", Excerpt: "build: go build ./...", Rechecked: true}})
	if err != nil || len(rep.Superseded) != 1 {
		t.Fatalf("자동 대체가 일어나지 않았습니다: %v %+v", err, rep)
	}
	return old, nw
}

func TestSessionStart_자동_대체_알림은_범위마다_자기_세션이_한_번씩_받고_다른_프로젝트_제목은_싣지_않는다(t *testing.T) {
	s := store(t)
	// B 범위의 대체가 더 앞서야 한다: 표시점이 하나뿐이면 A가 받는 순간 B의 것도 받은 것이 돼 사라진다.
	oldB, nwB := replaceIn(t, s, otherHere, "시험 명령")
	later(s, time.Minute)
	oldA, nwA := replaceIn(t, s, here, "빌드 명령")
	cfg := config.Default()
	a := SessionBundleWith(s, cfg, here, "claude", "A1", nil).Text
	if !strings.Contains(a, "「"+oldA.Title+"」 → 「"+nwA.Title+"」") || strings.Contains(a, oldB.Title) || strings.Contains(a, nwB.Title) {
		t.Fatalf("A 프로젝트 세션:\n%s", a)
	}
	// A가 받았어도 B 프로젝트의 세션은 자기 것을 받는다. A 것은 싣지 않는다.
	b := SessionBundleWith(s, cfg, otherHere, "claude", "B1", nil).Text
	if !strings.Contains(b, "「"+oldB.Title+"」 → 「"+nwB.Title+"」") || strings.Contains(b, oldA.Title) || strings.Contains(b, nwA.Title) {
		t.Fatalf("B 프로젝트 세션:\n%s", b)
	}
	// 각자 한 번씩만 받는다.
	for _, c := range []struct{ scope, session string }{{here, "A2"}, {otherHere, "B2"}} {
		if text := SessionBundleWith(s, cfg, c.scope, "claude", c.session, nil).Text; strings.Contains(text, "검증된 사실로 바꿨다") {
			t.Fatalf("%s에 다시 나왔습니다:\n%s", c.scope, text)
		}
	}
}

package inject

// 도구 입력 기준 회상(명세 T-1·T-2·T-4): 확정 규칙이 아닌 기억은 도구 입력과의 회상 점수가 문턱을 넘을 때만 붙고, 입력 조건이
// 달린 기억은 조건 낱말이 입력에 있을 때만 붙고, 입력이 없으면 도구 이름만으로 판정한다. 스위치(toolInputGate)를 끄면 점수 문만
// 꺼지고 지금 동작이다.

import (
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// gated is the default settings with the tool input gate on.
func gated() config.Config {
	cfg := config.Default()
	cfg.ToolInputGate = true
	return cfg
}

// saveOn saves a global memory of kind with tool triggers: a user's words (confirmed) or a session (candidate).
func saveOn(t *testing.T, s *memory.Store, kind, title, body string, tools []string, confirmed bool) memory.Memory {
	t.Helper()
	later(s, time.Minute)
	src := memory.Source{Kind: memory.SourceSession, Ref: "x"}
	if confirmed {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: title}
	}
	m, err := s.Save(memory.SaveInput{Kind: kind, Title: title, Body: body, Scope: memory.ScopeGlobal, Topic: title, Tools: tools, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 실제로 되풀이해 들어온 두 후보 교훈의 꼴: Bash에 걸려 있어 무슨 명령이든 붙었다. 입력과 관련 없는 명령에는 붙지 않고,
// 가리키기 줄에도 나오지 않으며(넣은 것이 아니다), 세션 표시도 남기지 않아 관련 있는 명령에서는 붙는다.
func TestToolInjection_교훈은_도구_입력과_관련이_없으면_붙지_않는다(t *testing.T) {
	s := store(t)
	rg := saveOn(t, s, memory.KindLesson, "백그라운드 셸에서 rg 명령은 검색 경로를 꼭 준다", "경로가 없으면 표준 입력을 끝없이 기다린다", []string{"Bash"}, false)
	dom := saveOn(t, s, memory.KindLesson, "happy-dom 요소를 실패하는 assert에 넘기면 메모리가 터진다", "node 시험에서 부정 단언은 참 거짓으로 비교한다", []string{"Bash"}, false)
	cfg := gated()
	for _, input := range []string{"ls -la", "git status", "go test ./internal/inject -count=1"} {
		if got := ToolInjection(s, cfg, here, "claude", "S", "Bash", input); got.Text != "" || len(got.MemoryIDs) != 0 {
			t.Fatalf("input %q brought %v:\n%s", input, got.MemoryIDs, got.Text)
		}
	}
	if marks := s.SessionMarks("claude", "S", memory.MarkTool); len(marks) != 0 {
		t.Fatalf("blocked memories were marked: %v", marks)
	}
	got := ToolInjection(s, cfg, here, "claude", "S", "Bash", "rg TODO internal")
	if !slices.Equal(got.MemoryIDs, []string{rg.ID}) {
		t.Fatalf("rg call brought %v, want only %s (not %s):\n%s", got.MemoryIDs, rg.ID, dom.ID, got.Text)
	}
}

// 확정 규칙은 입력과 관련이 없어도 도구 이름으로 붙는다. 같은 글의 후보 규칙은 확정 규칙이 아니라 입력 문을 지난다.
func TestToolInjection_확정_규칙은_도구_이름만으로_붙는다(t *testing.T) {
	s := store(t)
	rule := saveOn(t, s, memory.KindRule, "셸 명령의 결과는 요약해서 보고한다", "출력 전체를 붙이지 않는다", []string{"Bash"}, true)
	cand := saveOn(t, s, memory.KindRule, "셸 명령은 작업 폴더를 적어 부른다", "cd에 기대지 않는다", []string{"Bash"}, false)
	got := ToolInjection(s, gated(), here, "claude", "S", "Bash", "ls -la")
	if !slices.Equal(got.MemoryIDs, []string{rule.ID}) {
		t.Fatalf("ids %v, want the confirmed rule %s only (candidate %s gated):\n%s", got.MemoryIDs, rule.ID, cand.ID, got.Text)
	}
}

// 입력 조건(Bash(git commit))은 사용자가 단 조건이라 스위치와 관계없이 지킨다: ls에는 붙지 않고 git commit에는 붙는다.
func TestToolInjection_입력_조건이_달린_규칙은_ls에_붙지_않고_git_commit에_붙는다(t *testing.T) {
	for _, gate := range []bool{true, false} {
		s := store(t)
		rule := saveOn(t, s, memory.KindRule, "커밋 전에 시험을 돌린다", "실패하면 커밋하지 않는다", []string{"Bash(git commit)"}, true)
		cfg := config.Default()
		cfg.ToolInputGate = gate
		if got := ToolInjection(s, cfg, here, "claude", "S", "Bash", "ls"); got.Text != "" {
			t.Fatalf("gate %v: ls brought:\n%s", gate, got.Text)
		}
		if got := ToolInjection(s, cfg, here, "claude", "S", "Bash", "git commit -m x"); !slices.Equal(got.MemoryIDs, []string{rule.ID}) {
			t.Fatalf("gate %v: git commit brought %v", gate, got.MemoryIDs)
		}
	}
}

// 호스트가 도구 입력을 주지 않으면(T-4) 스위치가 켜져 있어도 도구 이름만으로 판정한다: 입력 문도 입력 조건도 쓰지 않는다.
func TestToolInjection_입력이_없으면_이름으로만_판정한다(t *testing.T) {
	s := store(t)
	lesson := saveOn(t, s, memory.KindLesson, "happy-dom 요소를 실패하는 assert에 넘기면 메모리가 터진다", "부정 단언은 참 거짓으로 비교한다", []string{"Bash"}, false)
	rule := saveOn(t, s, memory.KindRule, "커밋 전에 시험을 돌린다", "실패하면 커밋하지 않는다", []string{"Bash(git commit)"}, true)
	got := ToolInjection(s, gated(), here, "claude", "S", "Bash", "")
	if !slices.Equal(got.MemoryIDs, []string{rule.ID, lesson.ID}) {
		t.Fatalf("ids %v, want %v", got.MemoryIDs, []string{rule.ID, lesson.ID})
	}
}

// 기본값은 스위치가 꺼져 있다(문턱은 평가로 정한다, T-3). 끄면 입력이 있어도 조건 없는 기억은 입력이 없을 때와 같은 글·id다.
func TestToolInjection_toolInputGate를_끄면_지금_동작이다(t *testing.T) {
	cfg := config.Default()
	if cfg.ToolInputGate {
		t.Fatal("toolInputGate is on by default")
	}
	s := store(t)
	saveOn(t, s, memory.KindLesson, "happy-dom 요소를 실패하는 assert에 넘기면 메모리가 터진다", "부정 단언은 참 거짓으로 비교한다", []string{"Bash"}, false)
	saveOn(t, s, memory.KindFact, "시험 DB는 컨테이너로 띄운다", "포트 5433", []string{"Bash"}, false)
	saveOn(t, s, memory.KindRule, "셸 명령의 결과는 요약해서 보고한다", "출력 전체를 붙이지 않는다", []string{"Bash"}, true)
	with := ToolInjection(s, cfg, here, "claude", "S1", "Bash", "ls -la")
	without := ToolInjection(s, cfg, here, "claude", "S2", "Bash", "")
	if with.Text != without.Text || !slices.Equal(with.MemoryIDs, without.MemoryIDs) || len(with.MemoryIDs) != 3 {
		t.Fatalf("with input %v:\n%s\nwithout %v:\n%s", with.MemoryIDs, with.Text, without.MemoryIDs, without.Text)
	}
}

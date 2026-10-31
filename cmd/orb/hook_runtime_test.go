package main

import (
	"os"
	"strings"
	"testing"

	"github.com/onidot-labs/openorbit/internal/work"
)

const runtimeAsk = `{"hook_event_name":"PreToolUse","session_id":"R1","cwd":"/tmp","prompt_id":"P1","tool_name":"AskUserQuestion","tool_use_id":"toolu_r1",
	"tool_input":{"questions":[{"question":"어느 쪽?","header":"방식","multiSelect":false,"options":[{"label":"가"},{"label":"나"}]}]}}`

func decisions(t *testing.T) []work.Decision {
	t.Helper()
	if _, err := os.Stat(paths().WorkDB()); err != nil {
		return nil
	}
	s, err := work.Open(paths().WorkDB())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.ListDecisions(work.DecisionFilter{States: work.DecisionStates})
	if err != nil {
		t.Fatal(err)
	}
	return p.Items
}

// 사람이 직접 연 세션: 플러그인 훅이 질문 도구를 결정 카드로 연다.
func TestHook_질문_도구는_결정_카드가_된다(t *testing.T) {
	home(t)
	if _, _, code := run(t, runtimeAsk, "hook", "work", "--host", "claude"); code != 0 {
		t.Fatalf("종료 %d", code)
	}
	ds := decisions(t)
	if len(ds) != 1 || ds[0].Owner != "human" || ds[0].Source != "question" || ds[0].Questions[0].Text != "어느 쪽?" {
		t.Fatalf("카드 = %+v", ds)
	}
}

// 설계 「Claude 엔진 중복 규칙」: orbit-pi가 띄운 세션(ORBIT_RUNTIME=orbit-pi)에서 플러그인 훅은
// 기억 주입·작업 기록·카드를 모두 끄고, orbit-pi가 --runtime으로 직접 부른 훅만 기록한다.
func TestHook_orbit_pi_세션에서는_플러그인_훅이_끈다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_RUNTIME", "orbit-pi")
	start := withSession(t, probeInput(t, "SessionStart.1"), "R1", "/tmp")
	out, _, code := run(t, start, "hook", "session-start", "--host", "claude")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("플러그인 훅의 세션 시작 주입: %d %q", code, out)
	}
	run(t, runtimeAsk, "hook", "work", "--host", "claude")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "R1", "/tmp"), "hook", "work", "--host", "claude")
	if ds := decisions(t); len(ds) != 0 {
		t.Fatalf("플러그인 훅이 카드를 열었습니다: %+v", ds)
	}
	if _, err := os.Stat(paths().WorkDB()); err == nil && len(works(t).Items) != 0 {
		t.Fatal("플러그인 훅이 작업을 기록했습니다")
	}
	// orbit-pi가 같은 입력으로 직접 부른다: 한 번만 기록하고, 카드는 열지 않는다(orbit-pi가 decision_open)
	run(t, start, "hook", "session-start", "--host", "claude", "--runtime", "orbit-pi", "--launcher", "studio")
	run(t, runtimeAsk, "hook", "work", "--host", "claude", "--runtime", "orbit-pi")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "R1", "/tmp"), "hook", "work", "--host", "claude", "--runtime", "orbit-pi")
	p := works(t)
	if len(p.Items) != 1 || p.Items[0].Origin.Launcher != "studio" {
		t.Fatalf("orbit-pi 기록 = %+v", p.Items)
	}
	if ds := decisions(t); len(ds) != 0 {
		t.Fatalf("runtime 훅이 카드를 열었습니다: %+v", ds)
	}
	// 두 번째 같은 목록 호출이 단계를 두 번 남기지 않는다(플러그인 훅은 계속 끔)
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "R1", "/tmp"), "hook", "work", "--host", "claude")
	if got := works(t).Items[0].StepCount; got != p.Items[0].StepCount {
		t.Fatalf("단계 수 %d → %d", p.Items[0].StepCount, got)
	}
}

func TestHook_runtime_값_검사(t *testing.T) {
	home(t)
	_, errOut, _ := run(t, runtimeAsk, "hook", "work", "--host", "claude", "--runtime", "other")
	if !strings.Contains(errOut, "orb hook") {
		t.Fatalf("모르는 runtime: %q", errOut)
	}
	if ds := decisions(t); len(ds) != 0 {
		t.Fatalf("모르는 runtime으로 기록: %+v", ds)
	}
}

// 사람이 직접 연 세션에는 launcher=human이 남는다.
func TestHook_사람_세션의_launcher(t *testing.T) {
	home(t)
	run(t, withSession(t, probeInput(t, "SessionStart.1"), "H1", "/tmp"), "hook", "session-start", "--host", "claude")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "H1", "/tmp"), "hook", "work", "--host", "claude")
	if p := works(t); len(p.Items) != 1 || p.Items[0].Origin.Launcher != "human" {
		t.Fatalf("launcher = %+v", p.Items)
	}
}

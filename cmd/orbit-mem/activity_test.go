package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func hookIn(session, cwd, prompt string) string {
	b, _ := json.Marshal(map[string]string{"session_id": session, "cwd": cwd, "prompt": prompt, "hook_event_name": "x"})
	return string(b)
}

func activities(t *testing.T) []memory.Activity {
	t.Helper()
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	as, err := s.Activities(memory.ActivityFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return as
}

func hook(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"hook"}, args...)...)
	if code != 0 {
		t.Fatalf("hook %v: 종료 %d %s", args, code, errOut)
	}
	return out
}

// 끝 기준(M5): 세션 시작은 줄을 만들고, 요청은 수·시각·첫/마지막 글을, 세션 끝은 끝 시각을 남긴다.
// 사람이 연 세션은 launcher human, ORBIT_PARENT는 parent로 남는다.
func TestHook_활동_기록_세_훅(t *testing.T) {
	home(t)
	seed(t)
	t.Setenv("ORBIT_PARENT", "claude:P0")
	dir := t.TempDir()
	hook(t, hookIn("S1", dir, ""), "session-start", "--host", "claude")
	as := activities(t)
	if len(as) != 1 || as[0].Host != "claude" || as[0].Session != "S1" || as[0].Cwd != dir || as[0].Launcher != "human" || as[0].Parent != "claude:P0" ||
		as[0].StartedAt == 0 || as[0].LastAt < as[0].StartedAt || as[0].EndedAt != 0 || as[0].Prompts != 0 || as[0].ProjectLabel == "" || as[0].ProjectScope == "" {
		t.Fatalf("시작 %+v", as)
	}
	hook(t, hookIn("S1", dir, "첫 요청\n둘째 줄"), "prompt", "--host", "claude")
	hook(t, hookIn("S1", dir, "token sk-ant-api03-"+strings.Repeat("x", 40)+" 마지막 "+strings.Repeat("가", 500)), "prompt", "--host", "claude")
	as = activities(t)
	if len(as) != 1 || as[0].Prompts != 2 || as[0].FirstPrompt != "첫 요청 둘째 줄" || !strings.HasPrefix(as[0].LastPrompt, "token ") {
		t.Fatalf("요청 %+v", as)
	}
	if strings.Contains(as[0].LastPrompt, "sk-ant-api03") || len([]rune(as[0].LastPrompt)) > memory.ActivityPromptRunes+1 {
		t.Fatalf("글은 가리고 자른다: %q", as[0].LastPrompt)
	}
	hook(t, hookIn("S1", dir, ""), "session-end", "--host", "claude")
	if as = activities(t); as[0].EndedAt == 0 || as[0].Prompts != 2 {
		t.Fatalf("끝 %+v", as)
	}
	// 같은 세션의 재개는 같은 줄을 다시 연다
	hook(t, hookIn("S1", dir, ""), "session-start", "--host", "claude")
	if as = activities(t); len(as) != 1 || as[0].EndedAt != 0 {
		t.Fatalf("재개 %+v", as)
	}
	// 다른 호스트의 같은 id는 다른 줄이다
	hook(t, hookIn("S1", dir, "codex 요청"), "prompt", "--host", "codex")
	if as = activities(t); len(as) != 2 {
		t.Fatalf("호스트+세션이 열쇠다: %+v", as)
	}
}

// 끝 기준: 회고 실행 안(ORBIT_RETRO_ID)의 세션은 활동 기록에 남지 않는다.
func TestHook_회고_실행은_활동_기록에_남기지_않는다(t *testing.T) {
	home(t)
	seed(t)
	t.Setenv("ORBIT_RETRO_ID", "r-1")
	hook(t, hookIn("S1", "/tmp", "회고"), "session-start", "--host", "claude")
	hook(t, hookIn("S1", "/tmp", "회고"), "prompt", "--host", "claude")
	hook(t, hookIn("S1", "/tmp", ""), "session-end", "--host", "claude")
	if as := activities(t); len(as) != 0 {
		t.Fatalf("회고 세션이 남았습니다: %+v", as)
	}
}

// 설계 「Claude 엔진 중복 규칙」: ORBIT_RUNTIME=orbit-pi 세션에서 플러그인 훅은 아무것도 하지 않고,
// orbit-pi가 --runtime orbit-pi --launcher X로 부른 훅만 기록한다.
func TestHook_orbit_pi_세션은_runtime_훅만_기록한다(t *testing.T) {
	home(t)
	seed(t).Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	t.Setenv("ORBIT_RUNTIME", "orbit-pi")
	if out := hook(t, hookIn("R1", "/tmp", ""), "session-start", "--host", "claude"); strings.TrimSpace(out) != "" {
		t.Fatalf("플러그인 훅의 주입: %q", out)
	}
	hook(t, hookIn("R1", "/tmp", "요청"), "prompt", "--host", "claude")
	if as := activities(t); len(as) != 0 {
		t.Fatalf("플러그인 훅이 기록했습니다: %+v", as)
	}
	if out := hook(t, hookIn("R1", "/tmp", ""), "session-start", "--host", "claude", "--runtime", "orbit-pi", "--launcher", "studio"); !strings.Contains(out, "답은 존댓말") {
		t.Fatalf("runtime 훅은 주입한다: %q", out)
	}
	hook(t, hookIn("R1", "/tmp", "요청"), "prompt", "--host", "claude", "--runtime", "orbit-pi", "--launcher", "studio")
	as := activities(t)
	if len(as) != 1 || as[0].Launcher != "studio" || as[0].Prompts != 1 {
		t.Fatalf("runtime 기록 %+v", as)
	}
	// 모르는 launcher는 짐작하지 않고 orbit-pi로 둔다
	hook(t, hookIn("R2", "/tmp", ""), "session-start", "--host", "claude", "--runtime", "orbit-pi", "--launcher", "bogus")
	for _, a := range activities(t) {
		if a.Session == "R2" && a.Launcher != "orbit-pi" {
			t.Fatalf("모르는 launcher %+v", a)
		}
	}
	// 모르는 runtime 값은 인자 오류로 아무것도 하지 않는다(종료 0)
	_, errOut, code := run(t, hookIn("R3", "/tmp", ""), "hook", "session-start", "--host", "claude", "--runtime", "other")
	if code != 0 || !strings.Contains(errOut, "orbit-mem hook") {
		t.Fatalf("모르는 runtime: %d %q", code, errOut)
	}
}

// 끝 기준: hook work(옛 플러그인·옛 런타임이 부른다)는 입력을 버리고 아무것도 하지 않고 종료 0이다.
func TestHook_work는_아무것도_하지_않는다(t *testing.T) {
	h := home(t)
	seed(t)
	for _, args := range [][]string{{"work", "--host", "claude"}, {"work", "--host", "codex", "--skip-lists"}, {"work", "--host", "claude", "--runtime", "orbit-pi"}} {
		out, errOut, code := run(t, `{"hook_event_name":"PostToolUse","session_id":"W1","cwd":"/tmp","tool_name":"TaskCreate"}`, append([]string{"hook"}, args...)...)
		if code != 0 || out != "" || errOut != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	if as := activities(t); len(as) != 0 {
		t.Fatalf("hook work가 기록했습니다: %+v", as)
	}
	if lexists(h + "/work.db") {
		t.Fatal("hook work가 work.db를 만들었습니다")
	}
}

// 끝 기준: 세션 시작 주입에 열린 작업 문맥(work.db)이 붙지 않는다.
func TestHook_세션_시작에_작업_문맥이_없다(t *testing.T) {
	home(t)
	seed(t).Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	out := hook(t, hookIn("S9", "/tmp", ""), "session-start", "--host", "claude")
	if !strings.Contains(out, "답은 존댓말") || strings.Contains(out, "work_title") || strings.Contains(out, "열린 작업") {
		t.Fatalf("주입 %q", out)
	}
}

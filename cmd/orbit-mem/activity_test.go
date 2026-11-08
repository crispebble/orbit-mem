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

// 기획 · orbit-mem X6: 옛 hook work와 orbit-pi 전용 인자(--runtime·--launcher·--record-only)는 걷어냈다.
// 옛 쪽이 불러도 인자 오류로 아무것도 하지 않고 종료 0이다(호스트를 막지 않는다).
func TestHook_옛_work와_runtime_인자는_아무것도_하지_않는다(t *testing.T) {
	h := home(t)
	seed(t)
	for _, args := range [][]string{{"work", "--host", "claude"}, {"session-start", "--host", "claude", "--runtime", "orbit-pi", "--launcher", "studio"},
		{"prompt", "--host", "claude", "--record-only"}} {
		out, _, code := run(t, `{"hook_event_name":"PostToolUse","session_id":"W1","cwd":"/tmp","prompt":"x"}`, append([]string{"hook"}, args...)...)
		if code != 0 || out != "" {
			t.Fatalf("%v: %d %q", args, code, out)
		}
	}
	if as := activities(t); len(as) != 0 {
		t.Fatalf("기록했습니다: %+v", as)
	}
	if lexists(h + "/work.db") {
		t.Fatal("work.db를 만들었습니다")
	}
}

// 끝 기준: 세션 시작 주입에 열린 작업 문맥(work.db)이 붙지 않는다.
func TestHook_세션_시작에_작업_문맥이_없다(t *testing.T) {
	home(t)
	pinSave(t, seed(t), memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	out := hook(t, hookIn("S9", "/tmp", ""), "session-start", "--host", "claude")
	if !strings.Contains(out, "답은 존댓말") || strings.Contains(out, "work_title") || strings.Contains(out, "열린 작업") {
		t.Fatalf("주입 %q", out)
	}
}

// --record-only는 요청 훅의 인자다. 시작·끝 훅에 주면 인자 오류로 아무것도 하지 않는다(종료 0).
func TestHook_record_only는_요청_훅에만(t *testing.T) {
	home(t)
	seed(t)
	_, errOut, code := run(t, hookIn("R8", "/tmp", ""), "hook", "session-start", "--host", "claude", "--record-only")
	if code != 0 || !strings.Contains(errOut, "orbit-mem hook") {
		t.Fatalf("시작 훅 --record-only: %d %q", code, errOut)
	}
	if as := activities(t); len(as) != 0 {
		t.Fatalf("인자 오류인데 기록했습니다: %+v", as)
	}
}

package main

// 설계 · orbit-mem 2판 S1·S2를 훅·명령 쪽에서 잰다: 세션 시작 한도(AC-1), compact·clear 묶음 다시 만들기(AC-4),
// 다른 세션의 저장이 요청 훅과 PreToolUse 훅에 들어감(AC-8), doctor의 호스트 기억 경고.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func eventContext(t *testing.T, out, event string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var v struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.HookEventName != event {
		t.Fatalf("hook output %q: %v", out, err)
	}
	return v.HookSpecificOutput.AdditionalContext
}

func TestHook_세션_시작_문맥은_판_안내까지_6000자_이하다(t *testing.T) {
	home(t)
	s := seed(t)
	for i := 0; i < 200; i++ {
		s.Save(memory.SaveInput{Kind: "rule", Title: fmt.Sprintf("규칙 %d", i), Body: strings.Repeat("본문 ", 200), Source: memory.Source{Kind: "quote", Excerpt: "x"}})
	}
	out, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"startup"}`, "hook", "session-start", "--host", "claude", "--plugin-version", "0.0.1")
	ctx := eventContext(t, out, "SessionStart")
	if n := utf8.RuneCountInString(ctx); n == 0 || n > 6000 || !strings.Contains(ctx, "0.0.1") {
		t.Fatalf("session start %d chars:\n%s", n, ctx)
	}
	var res struct{ Text string }
	io, _, _ := run(t, "", "inject", "session-start", "--session", "o1", "--host", "host-x", "--rules", "brief")
	if err := json.Unmarshal([]byte(io), &res); err != nil || utf8.RuneCountInString(res.Text) > 6000 {
		t.Fatalf("brief %d: %v", utf8.RuneCountInString(res.Text), err)
	}
}

func TestHook_compact와_clear는_묶음을_다시_만들고_resume은_그대로다(t *testing.T) {
	home(t)
	s := seed(t)
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	first, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"startup"}`, "hook", "session-start", "--host", "claude")
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "새로 확정된 규칙", Source: memory.Source{Kind: "quote", Excerpt: "새로"}})
	if again, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"resume"}`, "hook", "session-start", "--host", "claude"); again != first {
		t.Fatalf("resume changed the bundle:\n%s", again)
	}
	for _, src := range []string{"compact", "clear"} {
		out, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"`+src+`"}`, "hook", "session-start", "--host", "claude")
		if !strings.Contains(out, "새로 확정된 규칙") {
			t.Fatalf("%s kept the old bundle:\n%s", src, out)
		}
	}
}

// AC-8 Orbit Space 사고 재현: 세션 A 시작 → 세션 B가 문서 위치 규칙 저장 → A의 요청 훅에 「새 기억」, A가
// mcp__atlas__write_page를 부르기 직전 PreToolUse 훅에 그 규칙.
func TestAC8_다른_세션의_저장이_요청과_PreToolUse_훅에_들어간다(t *testing.T) {
	home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	run(t, `{"cwd":"/tmp","session_id":"A","source":"startup"}`, "hook", "session-start", "--host", "claude")
	out, errOut, code := run(t, `{"kind":"rule","appliesTo":"user","title":"Orbit 문서는 Orbit Space에 쓴다","body":"onidot Space에 쓰지 않는다","topic":"atlas 문서 위치",`+
		`"tools":["mcp__atlas__write_page"],"source":{"kind":"quote","excerpt":"Orbit 문서는 Orbit Space에"}}`, "tool", "memory_save", "--session", "B", "--cwd", "/tmp", "--host", "host-x")
	if code != 0 {
		t.Fatalf("save: %s %s", out, errOut)
	}
	var saved struct{ Memory memory.Memory }
	if err := json.Unmarshal([]byte(out), &saved); err != nil {
		t.Fatalf("save out %q: %v", out, err)
	}
	p, _, _ := run(t, `{"cwd":"/tmp","session_id":"A","prompt":"설계 문서를 발행해 줘"}`, "hook", "prompt", "--host", "claude")
	if ctx := eventContext(t, p, "UserPromptSubmit"); !strings.Contains(ctx, "새 기억") || !strings.Contains(ctx, saved.Memory.ID) {
		t.Fatalf("prompt hook:\n%s", ctx)
	}
	in := `{"cwd":"/tmp","session_id":"A","hook_event_name":"PreToolUse","tool_name":"mcp__atlas__write_page","tool_input":{"action":"save"}}`
	pre, _, code := run(t, in, "hook", "pre-tool", "--host", "claude")
	if ctx := eventContext(t, pre, "PreToolUse"); code != 0 || !strings.Contains(ctx, saved.Memory.ID) || !strings.Contains(ctx, "onidot Space에 쓰지 않는다") {
		t.Fatalf("pre-tool hook (%d):\n%s", code, ctx)
	}
	if again, _, _ := run(t, in, "hook", "pre-tool", "--host", "claude"); strings.TrimSpace(again) != "" {
		t.Fatalf("pre-tool repeated in the session: %s", again)
	}
	if other, _, _ := run(t, strings.Replace(in, "write_page", "read", 1), "hook", "pre-tool", "--host", "codex"); strings.TrimSpace(other) != "" {
		t.Fatalf("another tool: %s", other)
	}
}

func TestDoctor_호스트_자체_기억이_켜져_있으면_경고한다(t *testing.T) {
	home(t)
	claude, codex := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "")
	problems := func() string {
		out, _, _ := run(t, "", "doctor", "--json")
		var rep struct{ Problems []struct{ ID, Fix string } }
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("doctor %q: %v", out, err)
		}
		var ids []string
		for _, p := range rep.Problems {
			ids = append(ids, p.ID+"="+p.Fix)
		}
		return strings.Join(ids, "\n")
	}
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"autoMemoryEnabled": true}`), 0o600)
	os.WriteFile(filepath.Join(codex, "config.toml"), []byte("[features]\nmemories = true\n"), 0o600)
	got := problems()
	if !strings.Contains(got, "claude-auto-memory=") || !strings.Contains(got, "autoMemoryEnabled") || !strings.Contains(got, "codex-memories=") {
		t.Fatalf("problems:\n%s", got)
	}
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"autoMemoryEnabled": false, "autoDreamEnabled": false}`), 0o600)
	os.WriteFile(filepath.Join(codex, "config.toml"), []byte("[features]\nmemories = false\n\n[memories]\ngenerate_memories = false\nuse_memories = false\n"), 0o600)
	if got := problems(); strings.Contains(got, "claude-auto-memory") || strings.Contains(got, "codex-memories") {
		t.Fatalf("off but warned:\n%s", got)
	}
}

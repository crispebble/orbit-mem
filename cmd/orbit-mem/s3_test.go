package main

// 설계 · orbit-mem 2판 S3(명세 0s4fsq3hy8pnp) 훅·명령 쪽: 서브에이전트 시작 훅이 user 핵심만 넣고(S3-7), 서브에이전트의
// PreToolUse는 주 세션과 따로 한 번 받으며, orbit-mem place가 지금 폴더의 제품을 보인다(S3-2).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

func TestHook_subagent_start는_user_핵심만_넣는다(t *testing.T) {
	home(t)
	s := seed(t)
	in := `{"session_id":"S","agent_id":"a1","agent_type":"general-purpose","cwd":"/tmp","hook_event_name":"SubagentStart"}`
	s.Save(memory.SaveInput{Kind: "rule", Title: "핵심이 아닌 규칙", Topic: "아님", Source: memory.Source{Kind: "quote", Excerpt: "x"}})
	if out, _, code := run(t, in, "hook", "subagent-start", "--host", "claude"); code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("no core: %d %q", code, out)
	}
	core, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Topic: "말투", Source: memory.Source{Kind: "quote", Excerpt: "존댓말"}})
	if _, err := s.Pin(core.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	out, _, _ := run(t, in, "hook", "subagent-start", "--host", "claude")
	ctx := eventContext(t, out, "SubagentStart")
	if !strings.Contains(ctx, core.ID) || strings.Contains(ctx, "핵심이 아닌") {
		t.Fatalf("subagent context:\n%s", ctx)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND session=?`, memory.EventInjectSubagent, "S/a1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("subagent events %d %v", n, err)
	}
}

func TestHook_서브에이전트의_도구_직전_기억은_주_세션과_따로_한_번(t *testing.T) {
	home(t)
	s := seed(t)
	m, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "Orbit 문서는 Orbit Space에", Topic: "문서 위치", Tools: []string{"mcp__atlas__write_page"},
		Source: memory.Source{Kind: "quote", Excerpt: "Orbit Space"}})
	pre := func(agent string) string {
		in := map[string]string{"session_id": "S", "cwd": "/tmp", "hook_event_name": "PreToolUse", "tool_name": "mcp__atlas__write_page"}
		if agent != "" {
			in["agent_id"], in["agent_type"] = agent, "general-purpose"
		}
		b, _ := json.Marshal(in)
		out, _, _ := run(t, string(b), "hook", "pre-tool", "--host", "claude")
		return eventContext(t, out, "PreToolUse")
	}
	for i, step := range []struct {
		agent string
		want  bool
	}{{"", true}, {"", false}, {"a1", true}, {"a1", false}, {"a2", true}} {
		if got := strings.Contains(pre(step.agent), m.ID); got != step.want {
			t.Fatalf("step %d agent %q: got %v", i, step.agent, got)
		}
	}
}

func TestPlace_지금_폴더의_제품을_보인다(t *testing.T) {
	h := home(t)
	draft := filepath.Join(h, "w", "onidot", "prototypes", "orbit-desktop-v4")
	repo := filepath.Join(h, "w", "studio")
	for _, d := range []string{draft, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", "git@github.com:onidot-labs/orbit-studio.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(h, "w"))
	cfg := config.Default()
	cfg.Products = place.Map{
		"orbit-code-desktop": {Match: []string{"github.com/onidot-labs/orbit-studio", filepath.ToSlash(real) + "/onidot/prototypes/orbit-desktop-*", filepath.ToSlash(real) + "/onidot"}},
		"atlas":              {Match: []string{"github.com/onidot-labs/atlas", filepath.ToSlash(real) + "/onidot"}},
	}
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string][]string{draft: {"orbit-code-desktop"}, repo: {"orbit-code-desktop"}, filepath.Join(h, "w", "onidot"): {"atlas", "orbit-code-desktop"}, h: nil} {
		_ = os.MkdirAll(dir, 0o755)
		out, errOut, code := run(t, "", "place", "--cwd", dir, "--json")
		var got struct {
			Scope    string   `json:"scope"`
			Products []string `json:"products"`
			Multi    bool     `json:"multi"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%s: %d %s %s", dir, code, out, errOut)
		}
		if strings.Join(got.Products, ",") != strings.Join(want, ",") || got.Multi != (len(want) > 1) || got.Scope == "" {
			t.Fatalf("%s: %+v, want %v", dir, got, want)
		}
	}
	if out, _, code := run(t, "", "place", "--cwd", draft); code != 0 || !strings.Contains(out, "orbit-code-desktop") {
		t.Fatalf("text: %d %s", code, out)
	}
}

// 설계 · orbit-mem 5.2절·L-4: 한 접두에 기억이 많아도 도구 훅 글은 2,500자 이하이고, 같은 세션의 기억 줄 합은
// 2,000자에서 멈춘 뒤 「N개 더」 줄만 온다. 도구 이름이 아주 길어도 넘지 않는다.
func TestHook_도구_직전_훅은_2500자_이하이고_세션_합계_2000자에서_멈춘다(t *testing.T) {
	home(t)
	s := seed(t)
	const n = 10
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("문서 도구 규칙 %d", i)
		if _, err := s.Save(memory.SaveInput{Kind: "rule", Title: title, Body: strings.Repeat("길다 ", 300), Topic: title, Scope: memory.ScopeGlobal,
			Tools: []string{"mcp__docs__*"}, Source: memory.Source{Kind: "quote", Excerpt: title}}); err != nil {
			t.Fatal(err)
		}
	}
	pre := func(session, tool string) string {
		b, _ := json.Marshal(map[string]string{"session_id": session, "cwd": "/tmp", "hook_event_name": "PreToolUse", "tool_name": tool})
		out, _, code := run(t, string(b), "hook", "pre-tool", "--host", "claude")
		if code != 0 {
			t.Fatalf("pre-tool exit %d", code)
		}
		ctx := eventContext(t, out, "PreToolUse")
		if c := utf8.RuneCountInString(ctx); c > 2500 {
			t.Fatalf("tool hook %d runes", c)
		}
		return ctx
	}
	total, shown, empty := 0, 0, 0
	for call := 0; call < 6; call++ {
		ctx := pre("S", "mcp__docs__write_page")
		if ctx == "" {
			empty++
			continue
		}
		var more string
		for _, line := range strings.Split(ctx, "\n")[1:] {
			switch {
			case strings.HasPrefix(line, "- 이 도구에 걸린 기억 "):
				more = line
			case strings.HasPrefix(line, "- "):
				shown++
				total += utf8.RuneCountInString(line) + 1
			default:
				total += utf8.RuneCountInString(line) + 1
			}
		}
		if total > 2000 {
			t.Fatalf("call %d: session memory lines %d runes", call, total)
		}
		// 같은 기억들은 세션에 한 번 알린다: 첫 호출만 남은 수를 센다.
		want := ""
		if call == 0 {
			want = fmt.Sprintf("- 이 도구에 걸린 기억 %d개 더 — memory_find", n-shown)
		}
		if more != want {
			t.Fatalf("call %d: more %q, want %q\n%s", call, more, want, ctx)
		}
	}
	if shown >= n || total+400 <= 2000 || empty < 2 {
		t.Fatalf("did not stop at the session sum once told: shown=%d total=%d empty=%d", shown, total, empty)
	}
	if ctx := pre("long", "mcp__docs__"+strings.Repeat("x", 10000)); !strings.Contains(ctx, "기억 8개 더") {
		t.Fatalf("long tool name:\n%.300s", ctx)
	}
}

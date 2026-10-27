package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/memory"
)

func run(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb}
	code := a.main(args)
	return out.String(), errb.String(), code
}

func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("ORBIT_HOME", h)
	return h
}

func seed(t *testing.T) *memory.Store {
	t.Helper()
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestHook_세션_시작은_호스트_JSON을_낸다(t *testing.T) {
	home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	out, _, code := run(t, `{"cwd":"/tmp","hook_event_name":"SessionStart","source":"startup"}`, "hook", "session-start", "--host", "codex")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	var v struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(v.HookSpecificOutput.AdditionalContext, "답은 존댓말") {
		t.Fatalf("out = %q %v", out, err)
	}
}

func TestHook_DB가_없거나_입력이_깨져도_조용히_넘어간다(t *testing.T) {
	home(t)
	out, _, code := run(t, `not json`, "hook", "prompt", "--host", "claude")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestProposals_터미널에서_승인(t *testing.T) {
	home(t)
	s := seed(t)
	m, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "후보", Source: memory.Source{Kind: "session"}})
	p, _ := s.Propose(memory.ProposalInput{Kind: "confirm", Title: "후보 확정", MemoryIDs: []string{m.ID}})
	out, _, _ := run(t, "", "proposals")
	if !strings.Contains(out, p.ID) || !strings.Contains(out, "대기 1건") {
		t.Fatalf("list: %s", out)
	}
	out, errOut, code := run(t, "", "proposals", "approve", p.ID, "--title", "고친 후보")
	if code != 0 || !strings.Contains(out, "승인했습니다") {
		t.Fatalf("approve: %s %s", out, errOut)
	}
	got, _ := s.Get(m.ID)
	if got.State != "confirmed" || got.Title != "고친 후보" {
		t.Fatalf("memory = %+v", got)
	}
}

func TestProposals_하나씩_검토(t *testing.T) {
	home(t)
	s := seed(t)
	a, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "가", Source: memory.Source{Kind: "session"}})
	b, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "나", Source: memory.Source{Kind: "session"}})
	s.Propose(memory.ProposalInput{Kind: "confirm", Title: "가 확정", MemoryIDs: []string{a.ID}})
	s.Propose(memory.ProposalInput{Kind: "confirm", Title: "나 확정", MemoryIDs: []string{b.ID}})
	out, _, code := run(t, "a\nr\n", "proposals", "review")
	if code != 0 || !strings.Contains(out, "남은 제안 0건") {
		t.Fatalf("review: %s", out)
	}
	ga, _ := s.Get(a.ID)
	gb, _ := s.Get(b.ID)
	if ga.State != "confirmed" || gb.State != "hidden" {
		t.Fatalf("states %s %s", ga.State, gb.State)
	}
}

func TestPluginBuild_와_검사(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	os.MkdirAll(filepath.Join(src, "skills", "x"), 0o755)
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"name":"demo","version":"0.1.0","description":"d","skills":["x"]}`), 0o644)
	os.WriteFile(filepath.Join(src, "skills", "x", "SKILL.md"), []byte("---\nname: x\ndescription: d\n---\nx"), 0o644)
	if o, e, code := run(t, "", "plugin", "build", src, "--out", out); code != 0 {
		t.Fatalf("build: %s %s", o, e)
	}
	if o, e, code := run(t, "", "plugin", "build", src, "--out", out, "--check"); code != 0 || !strings.Contains(o, "같습니다") {
		t.Fatalf("check: %s %s", o, e)
	}
}

func TestUsage_모르는_명령은_2(t *testing.T) {
	if _, _, code := run(t, "", "nope"); code != 2 {
		t.Fatal(code)
	}
}

// oni mcp를 실제 stdio로 띄워 도구를 부른다.
func TestMCP_stdio로_도구를_부른다(t *testing.T) {
	h := home(t)
	bin := filepath.Join(t.TempDir(), "oni")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(os.Environ(), "ORBIT_HOME="+h)
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_save", Arguments: map[string]any{
		"kind": "lesson", "title": "stdio로 저장", "source": map[string]any{"kind": "quote", "excerpt": "저장해"}}})
	if err != nil || res.IsError {
		t.Fatalf("save: %v %+v", err, res)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_find", Arguments: map[string]any{"query": "stdio"}})
	if err != nil || res.IsError {
		t.Fatalf("find: %v", err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "stdio로 저장") {
		t.Fatalf("find result: %s", b)
	}
}

func TestHook_인자가_틀려도_호스트를_막지_않는다(t *testing.T) {
	home(t)
	for _, args := range [][]string{{"hook"}, {"hook", "bogus"}, {"hook", "prompt", "--extra", "x"}} {
		if _, _, code := run(t, "", args...); code != 0 {
			t.Errorf("%v exit %d", args, code)
		}
	}
}

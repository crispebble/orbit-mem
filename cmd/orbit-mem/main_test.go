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

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	t.Setenv("ORBIT_LANG", "")         // 이 PC의 언어 설정과 무관하게 기본(영어)으로 돈다
	t.Setenv("CLAUDE_PROJECT_DIR", "") // 이 시험을 돌리는 세션의 폴더와 무관하게 돈다
	// 이 PC의 Claude Code·Codex 설정(호스트 자체 기억 판정)과 무관하게 돈다. HOME은 두어 go build 캐시를 쓴다.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(h, "no-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(h, "no-codex"))
	managed, cwd := claudeManagedDir, hostCwd
	claudeManagedDir = func() string { return filepath.Join(h, "no-managed") }
	hostCwd = func() (string, error) { return h, nil }
	t.Cleanup(func() { claudeManagedDir, hostCwd = managed, cwd })
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
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
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
	if !strings.Contains(out, p.ID) || !strings.Contains(out, "1 pending") || !strings.Contains(out, "proposals drain") {
		t.Fatalf("list: %s", out)
	}
	out, errOut, code := run(t, "", "proposals", "approve", p.ID, "--title", "고친 후보")
	if code != 0 || !strings.Contains(out, "approved.") {
		t.Fatalf("approve: %s %s", out, errOut)
	}
	got, _ := s.Get(m.ID)
	if got.State != "confirmed" || got.Title != "고친 후보" {
		t.Fatalf("memory = %+v", got)
	}
}

func TestProposals_연결_제안의_근거와_결과(t *testing.T) {
	home(t)
	s := seed(t)
	a, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "가", Source: memory.Source{Kind: "session"}})
	b, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "나", Source: memory.Source{Kind: "session"}})
	p, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalLink, Title: "잇기", MemoryIDs: []string{a.ID, b.ID}, Payload: memory.Payload{Note: "같은 결정의 근거"}})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ := run(t, "", "proposals", "show", p.ID)
	if !strings.Contains(out, "Why link: 같은 결정의 근거") {
		t.Fatalf("show: %s", out)
	}
	out, errOut, code := run(t, "", "proposals", "approve", p.ID)
	if code != 0 || !strings.Contains(out, "Added link l-") {
		t.Fatalf("approve: %s %s", out, errOut)
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
	if code != 0 || !strings.Contains(out, "0 proposals left") {
		t.Fatalf("review: %s", out)
	}
	ga, _ := s.Get(a.ID)
	gb, _ := s.Get(b.ID)
	if ga.State != "confirmed" || gb.State != "hidden" {
		t.Fatalf("states %s %s", ga.State, gb.State)
	}
}

func TestUsage_모르는_명령은_2(t *testing.T) {
	if _, _, code := run(t, "", "nope"); code != 2 {
		t.Fatal(code)
	}
}

// orbit-mem mcp를 실제 stdio로 띄워 도구를 부른다.
func TestMCP_stdio로_도구를_부른다(t *testing.T) {
	h := home(t)
	bin := filepath.Join(t.TempDir(), "orbit-mem")
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
	// MCP 응답 글은 AI가 읽으므로 언어 설정(기본 영어)과 무관하게 한국어다.
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "proposal_decide", Arguments: map[string]any{"id": "p-none", "decision": "approve"}})
	if res == nil || !res.IsError || !strings.ContainsFunc(res.Content[0].(*mcp.TextContent).Text, func(r rune) bool { return r >= 0xAC00 && r <= 0xD7A3 }) {
		t.Fatalf("not found: %+v", res.Content[0])
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

func TestHook_주입을_사건으로_남긴다(t *testing.T) {
	home(t)
	s := seed(t)
	r := pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	run(t, `{"cwd":"/tmp","session_id":"sess-1","hook_event_name":"SessionStart"}`, "hook", "session-start", "--host", "codex")
	run(t, `{"cwd":"/tmp","session_id":"sess-1","prompt":"점심 뭐 먹지"}`, "hook", "prompt", "--host", "codex")
	evs, err := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if err != nil || len(evs) != 2 {
		t.Fatalf("events = %+v %v", evs, err)
	}
	if evs[0].Kind != memory.EventInjectSessionStart || evs[0].Host != "codex" || evs[0].Session != "sess-1" || !strings.Contains(string(evs[0].Data), r.ID) {
		t.Errorf("session start = %+v %s", evs[0], evs[0].Data)
	}
	if evs[1].Kind != memory.EventInjectPrompt || !strings.Contains(string(evs[1].Data), `"memoryIds":[]`) {
		t.Errorf("prompt = %+v %s", evs[1], evs[1].Data)
	}
}

func TestStats_기간_지표를_보여_준다(t *testing.T) {
	home(t)
	s := seed(t)
	s.RecordEvent(memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-x", "injected": "no"})
	out, errOut, code := run(t, "", "stats")
	if code != 0 || !strings.Contains(out, "Repeated corrections: 1") {
		t.Fatalf("stats: %s %s", out, errOut)
	}
	out, _, code = run(t, "", "stats", "--days", "3", "--json")
	var v struct {
		From, To    string
		Corrections struct{ Repeated, NotInjected int }
	}
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Corrections.NotInjected != 1 {
		t.Fatalf("json: %s", out)
	}
	if _, _, code := run(t, "", "stats", "--week", "2026-10-21"); code != 1 {
		t.Fatalf("week must start on Monday, code %d", code)
	}
}

func TestHook_주입하지_않는_호출은_세지_않는다(t *testing.T) {
	h := home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	run(t, `{"cwd":"/tmp","session_id":"s","prompt":""}`, "hook", "prompt", "--host", "claude") // 빈 요청
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"inject":"rules","recentDecisions":5,"backupKeep":14}`), 0o600)
	run(t, `{"cwd":"/tmp","session_id":"s","prompt":"존댓말"}`, "hook", "prompt", "--host", "claude") // 요청마다 넣지 않는 수준
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"inject":"off","recentDecisions":5,"backupKeep":14}`), 0o600)
	run(t, `{"cwd":"/tmp","session_id":"s"}`, "hook", "session-start", "--host", "claude")
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
}

// K3: 세션 끝 훅은 활동 기록만 남긴다. 세션 끝 회고 대기열에 넣지 않고 뒤 프로세스도 띄우지 않는다
// (회고는 orbit-code가 돌린다). 프로세스를 띄우지 않는 것은 TestNoHostCalls가 코드로도 막는다.
func TestHook_세션_끝은_활동_기록만_남긴다(t *testing.T) {
	h := home(t)
	s := seed(t)
	out, _, code := run(t, `{"cwd":"/w/app","session_id":"sess-9","transcript_path":"/x/sess-9.jsonl","hook_event_name":"SessionEnd","reason":"exit"}`, "hook", "session-end", "--host", "claude")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	var ended int64
	if err := s.DB().QueryRow(`SELECT ended_at FROM activity WHERE host='claude' AND session='sess-9'`).Scan(&ended); err != nil || ended == 0 {
		t.Fatalf("activity ended_at = %d %v", ended, err)
	}
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM session_ends`).Scan(&n)
	if n != 0 {
		t.Fatalf("session_ends = %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(h, "logs", "session-retro.log")); !os.IsNotExist(err) {
		t.Fatalf("a background run was started: %v", err)
	}
}

// 기획 · orbit-mem R1~R3: 회고 명령(retro·retro-store)과 회고 MCP(mcp --retro), 그래프 읽기 서버(mcp --reader)는 없다.
func TestRetro_명령은_없다(t *testing.T) {
	home(t)
	for _, args := range [][]string{{"retro", "run"}, {"retro-store", "queue", "list"}} {
		_, errOut, code := run(t, "", args...)
		if code != 2 || !strings.Contains(strings.ToLower(errOut), "unknown command") {
			t.Fatalf("%v: code=%d err=%q", args, code, errOut)
		}
	}
	for _, flag := range []string{"--retro=r-1", "--reader"} {
		if _, _, code := run(t, "", "mcp", flag); code == 0 {
			t.Fatalf("mcp %s: code=%d", flag, code)
		}
	}
	_, help, _ := run(t, "", "help")
	for _, line := range strings.Split(help, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "orbit-mem" && strings.HasPrefix(f[1], "retro") {
			t.Fatalf("help lists %q", line)
		}
	}
}

func TestHook_같은_세션의_시작_묶음은_바뀌지_않는다(t *testing.T) {
	home(t)
	s := seed(t)
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	first, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"startup"}`, "hook", "session-start", "--host", "claude")
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "새로 확정된 규칙", Source: memory.Source{Kind: "quote", Excerpt: "새로"}})
	again, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"resume"}`, "hook", "session-start", "--host", "claude") // compact·clear는 v2_test
	if first != again || strings.Contains(again, "새로 확정된 규칙") {
		t.Fatalf("bundle changed:\n%s\n%s", first, again)
	}
	next, _, _ := run(t, `{"cwd":"/tmp","session_id":"s2","source":"startup"}`, "hook", "session-start", "--host", "claude")
	if !strings.Contains(next, "새로 확정된 규칙") {
		t.Fatalf("next session: %s", next)
	}
}

func TestLedger_승인한_변경을_보여_준다(t *testing.T) {
	home(t)
	s := seed(t)
	m, _ := s.Save(memory.SaveInput{Kind: "lesson", Title: "모호한 교훈", Source: memory.Source{Kind: "session"}})
	p, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalEdit, Title: "문구 고치기", MemoryIDs: []string{m.ID}, Payload: memory.Payload{Title: "분명한 교훈"}})
	res, err := s.Decide(memory.DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "", "ledger")
	if code != 0 || !strings.Contains(out, res.LedgerID) || !strings.Contains(out, "observing") || !strings.Contains(out, "문구 고치기") {
		t.Fatalf("ledger: %s", out)
	}
	out, _, _ = run(t, "", "ledger", "--json")
	var rows []struct{ ID, Verdict string }
	if json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0].ID != res.LedgerID {
		t.Fatalf("json: %s", out)
	}
}

func TestConfig_2차_키를_설정한다(t *testing.T) {
	home(t)
	for _, kv := range [][2]string{{"recallChars", "1500"}, {"ledgerWindowDays", "14"}, {"autoTidy", "false"}} {
		if _, errOut, code := run(t, "", "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("%s: %s", kv[0], errOut)
		}
	}
	cfg, err := loadConfig()
	if err != nil || cfg.AutoTidy || cfg.RecallChars != 1500 || cfg.LedgerWindowDays != 14 {
		t.Fatalf("cfg = %+v %v", cfg, err)
	}
	if _, _, code := run(t, "", "config", "set", "recallChars", "50"); code != 1 {
		t.Fatalf("out of range accepted: %d", code)
	}
}

// 끝 기준: 시작 훅은 --plugin-version이 자기 판과 다르면 문맥 끝에 맞추라는 한 줄을 더한다. 같으면 없고,
// 다른 훅과 hook work는 이 플래그를 받고 무시한다.
func TestHook_plugin_version이_다르면_시작_문맥에_한_줄(t *testing.T) {
	home(t)
	s := seed(t)
	in := `{"cwd":"/tmp","session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`
	ctx := func(args ...string) string {
		t.Helper()
		out, _, code := run(t, in, args...)
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		if out == "" {
			return ""
		}
		var v struct {
			HookSpecificOutput struct{ AdditionalContext string }
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%q %v", out, err)
		}
		return v.HookSpecificOutput.AdditionalContext
	}
	line := "orbit-mem 판(0.16.0)과 플러그인 판(0.3.9)이 다릅니다: `orbit-mem install`로 맞추세요."
	// 기억이 없어도 그 한 줄은 낸다
	if got := ctx("hook", "session-start", "--host", "claude", "--plugin-version", "0.3.9"); got != line {
		t.Fatalf("빈 문맥 %q", got)
	}
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	got := ctx("hook", "session-start", "--host", "codex", "--plugin-version", "0.3.9")
	if !strings.Contains(got, "답은 존댓말") || !strings.HasSuffix(got, "\n"+line) {
		t.Fatalf("다른 판 %q", got)
	}
	in = `{"cwd":"/tmp","session_id":"s2","hook_event_name":"SessionStart","source":"startup"}` // 안내 줄은 세션 묶음에 든다(T-20): 새 세션으로 본다
	if got := ctx("hook", "session-start", "--host", "codex", "--plugin-version", "0.16.0"); strings.Contains(got, "orbit-mem install") || !strings.Contains(got, "답은 존댓말") {
		t.Fatalf("같은 판 %q", got)
	}
	if got := ctx("hook", "prompt", "--host", "claude", "--plugin-version", "0.3.9"); strings.Contains(got, "orbit-mem install") {
		t.Fatalf("요청 훅 %q", got)
	}
	if out, _, code := run(t, in, "hook", "work", "--host", "codex", "--plugin-version", "0.3.9"); code != 0 || out != "" {
		t.Fatalf("hook work %d %q", code, out)
	}
	if out, _, code := run(t, in, "hook", "session-start", "--plugin-version"); code != 0 || out != "" {
		t.Fatalf("값 없는 플래그 %d %q", code, out)
	}
}

// 설계 · orbit-mem 2판 18.5(Q-8): 쌓인 대기 제안을 닫는다. --dry-run은 사본에서만 돌려 실제 DB를 바꾸지 않는다.
func TestProposals_drain은_대기를_닫고_미리보기는_바꾸지_않는다(t *testing.T) {
	home(t)
	s := seed(t)
	a, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "가", Source: memory.Source{Kind: "session"}})
	b, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "나", Source: memory.Source{Kind: "session"}})
	confirm, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	link, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalLink, Title: "잇기", MemoryIDs: []string{a.ID, b.ID}, Payload: memory.Payload{Note: "basis: 같은 결정"}})

	out, errOut, code := run(t, "", "proposals", "drain", "--dry-run")
	if code != 0 || !strings.Contains(out, "preview") || !strings.Contains(out, link.ID) {
		t.Fatalf("dry-run: %d %s %s", code, out, errOut)
	}
	if n := s.PendingCount(); n != 2 {
		t.Fatalf("미리 보기가 실제 DB를 바꿨습니다: 대기 %d", n)
	}

	out, errOut, code = run(t, "", "proposals", "drain", "--json")
	var v struct {
		DryRun bool               `json:"dryRun"`
		Result memory.DrainResult `json:"result"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.DryRun || v.Result.Applied != 1 || v.Result.Dropped != 1 {
		t.Fatalf("drain: %d %s %s", code, out, errOut)
	}
	if n := s.PendingCount(); n != 0 {
		t.Fatalf("대기 %d", n)
	}
	if p, _ := s.GetProposal(confirm.ID); p.Status != memory.StatusDropped {
		t.Fatalf("confirm %+v", p)
	}
	if p, _ := s.GetProposal(link.ID); p.Status != memory.StatusSettled {
		t.Fatalf("link %+v", p)
	}
	if got, _ := s.Get(a.ID); got.State != memory.StateCandidate || len(got.Links) != 1 {
		t.Fatalf("a %+v", got)
	}
}

func TestConfig_요청_회상_문턱을_설정한다(t *testing.T) {
	home(t)
	for _, kv := range [][2]string{{"recallMinScore", "0.45"}, {"recallMinCoverage", "0.6"}, {"recallMinRelative", "0.75"}} {
		if _, errOut, code := run(t, "", "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("set %s: %s", kv[0], errOut)
		}
	}
	cfg, err := loadConfig()
	if err != nil || cfg.RecallMinScore != 0.45 || cfg.RecallMinCoverage != 0.6 || cfg.RecallMinRelative != 0.75 {
		t.Fatalf("cfg = %v %v %v", cfg.RecallMinScore, cfg.RecallMinCoverage, err)
	}
	out, _, _ := run(t, "", "config")
	if !strings.Contains(out, `"recallMinScore": 0.45`) || !strings.Contains(out, `"recallMinCoverage": 0.6`) {
		t.Fatalf("config show: %s", out)
	}
	for _, key := range []string{"recallMinScore", "recallMinCoverage", "recallMinRelative"} {
		for _, bad := range []string{"0", "1.5", "-1", "abc", "NaN"} {
			if _, _, code := run(t, "", "config", "set", key, bad); code != 1 {
				t.Fatalf("%s %q accepted: %d", key, bad, code)
			}
		}
	}
	if cfg, _ := loadConfig(); cfg.RecallMinScore != 0.45 || cfg.RecallMinCoverage != 0.6 {
		t.Fatalf("bad value changed the file: %v %v", cfg.RecallMinScore, cfg.RecallMinCoverage)
	}
	if _, errOut, _ := run(t, "", "config", "set", "nope", "1"); !strings.Contains(errOut, "recallMinScore") || !strings.Contains(errOut, "recallMinCoverage") || !strings.Contains(errOut, "recallMinRelative") {
		t.Fatalf("unknown key list: %s", errOut)
	}
}

func TestConfig_saveSimilarMin을_설정한다(t *testing.T) {
	home(t)
	if _, errOut, code := run(t, "", "config", "set", "saveSimilarMin", "0.7"); code != 0 {
		t.Fatalf("set: %s", errOut)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.SaveSimilarMin != 0.7 {
		t.Fatalf("cfg = %v %v", cfg.SaveSimilarMin, err)
	}
	out, _, _ := run(t, "", "config")
	if !strings.Contains(out, `"saveSimilarMin": 0.7`) {
		t.Fatalf("config show: %s", out)
	}
	for _, bad := range []string{"0", "1.5", "-1", "abc"} {
		if _, _, code := run(t, "", "config", "set", "saveSimilarMin", bad); code != 1 {
			t.Fatalf("%q accepted: %d", bad, code)
		}
	}
	if cfg, _ := loadConfig(); cfg.SaveSimilarMin != 0.7 {
		t.Fatalf("bad value changed the file: %v", cfg.SaveSimilarMin)
	}
}

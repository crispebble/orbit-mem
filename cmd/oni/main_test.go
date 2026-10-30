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
	"github.com/onidot-labs/orbit/internal/config"
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
	if !strings.Contains(out, "잇는 근거: 같은 결정의 근거") {
		t.Fatalf("show: %s", out)
	}
	out, errOut, code := run(t, "", "proposals", "approve", p.ID)
	if code != 0 || !strings.Contains(out, "이은 연결 l-") {
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

func TestHook_주입을_사건으로_남긴다(t *testing.T) {
	home(t)
	s := seed(t)
	r, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
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
	if injected, known := s.InjectedIn("sess-1", r.ID); !injected || !known {
		t.Errorf("injected=%v known=%v", injected, known)
	}
}

func TestStats_기간_지표를_보여_준다(t *testing.T) {
	home(t)
	s := seed(t)
	s.RecordEvent(memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-x", "injected": "no"})
	out, errOut, code := run(t, "", "stats")
	if code != 0 || !strings.Contains(out, "반복 교정 1건") {
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
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"retroHost":"claude","retroHour":4,"inject":"rules","recentDecisions":5,"backupKeep":14}`), 0o600)
	run(t, `{"cwd":"/tmp","session_id":"s","prompt":"존댓말"}`, "hook", "prompt", "--host", "claude") // 요청마다 넣지 않는 수준
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"retroHost":"claude","retroHour":4,"inject":"off","recentDecisions":5,"backupKeep":14}`), 0o600)
	run(t, `{"cwd":"/tmp","session_id":"s"}`, "hook", "session-start", "--host", "claude")
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"retroHost":"claude","retroHour":4,"inject":"full","recentDecisions":5,"backupKeep":14}`), 0o600)
	t.Setenv("ORBIT_RETRO_ID", "r-1") // 회고 실행 안의 훅
	run(t, `{"cwd":"/tmp","session_id":"s"}`, "hook", "session-start", "--host", "codex")
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
}

func TestHook_세션_끝은_대기열에_넣고_뒤_실행을_띄운다(t *testing.T) {
	h := home(t)
	s := seed(t)
	var spawned [][]string
	old := spawnDetached
	spawnDetached = func(name string, args []string, log string) error { spawned = append(spawned, args); return nil }
	t.Cleanup(func() { spawnDetached = old })

	out, _, code := run(t, `{"cwd":"/w/app","session_id":"sess-9","transcript_path":"/x/sess-9.jsonl","hook_event_name":"SessionEnd","reason":"exit"}`, "hook", "session-end", "--host", "claude")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if len(spawned) != 1 || strings.Join(spawned[0], " ") != "retro sessions" {
		t.Fatalf("spawned = %v", spawned)
	}
	var host, session, transcript string
	if err := s.DB().QueryRow(`SELECT host, session, transcript FROM session_ends WHERE retro_id=''`).Scan(&host, &session, &transcript); err != nil ||
		host != "claude" || session != "sess-9" || transcript != "/x/sess-9.jsonl" {
		t.Fatalf("queue = %s %s %s %v", host, session, transcript, err)
	}
	// 넣지 않는 경우: Orbit 폴더 아래 세션, 회고 실행 안, 세션 끝 회고를 끈 PC.
	run(t, `{"cwd":"`+filepath.Join(h, "retro", "run")+`","session_id":"sess-r"}`, "hook", "session-end", "--host", "claude")
	t.Setenv("ORBIT_RETRO_ID", "r-1")
	run(t, `{"cwd":"/w/app","session_id":"sess-in-retro"}`, "hook", "session-end", "--host", "claude")
	t.Setenv("ORBIT_RETRO_ID", "")
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"retroHost":"claude","retroHour":4,"inject":"full","recentDecisions":5,"backupKeep":14,"sessionRetro":false,"recallChars":1000,"ledgerWindowDays":7}`), 0o600)
	run(t, `{"cwd":"/w/app","session_id":"sess-off"}`, "hook", "session-end", "--host", "claude")
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM session_ends`).Scan(&n)
	if n != 1 || len(spawned) != 1 {
		t.Fatalf("queued %d, spawned %d", n, len(spawned))
	}
}

func TestHook_같은_세션의_시작_묶음은_바뀌지_않는다(t *testing.T) {
	home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	first, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"startup"}`, "hook", "session-start", "--host", "claude")
	s.Save(memory.SaveInput{Kind: "rule", Title: "새로 확정된 규칙", Source: memory.Source{Kind: "quote", Excerpt: "새로"}})
	again, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"compact"}`, "hook", "session-start", "--host", "claude")
	if first != again || strings.Contains(again, "새로 확정된 규칙") {
		t.Fatalf("bundle changed:\n%s\n%s", first, again)
	}
	next, _, _ := run(t, `{"cwd":"/tmp","session_id":"s2","source":"startup"}`, "hook", "session-start", "--host", "claude")
	if !strings.Contains(next, "새로 확정된 규칙") {
		t.Fatalf("next session: %s", next)
	}
}

func TestRetroSessions_대기열이_비면_조용히_끝난다(t *testing.T) {
	home(t)
	seed(t)
	out, errOut, code := run(t, "", "retro", "sessions")
	if code != 0 || !strings.Contains(out, "대기 세션 0") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
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
	if code != 0 || !strings.Contains(out, res.LedgerID) || !strings.Contains(out, "관찰 중") || !strings.Contains(out, "문구 고치기") {
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
	for _, kv := range [][2]string{{"sessionRetro", "false"}, {"sessionRetroModel", "sonnet"}, {"sessionRetroDailyMax", "12"}, {"recallChars", "1500"}, {"ledgerWindowDays", "14"}} {
		if _, errOut, code := run(t, "", "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("%s: %s", kv[0], errOut)
		}
	}
	cfg, err := loadConfig()
	if err != nil || cfg.SessionRetro || cfg.SessionRetroModel != "sonnet" || cfg.SessionRetroDailyMax != 12 || cfg.RecallChars != 1500 || cfg.LedgerWindowDays != 14 {
		t.Fatalf("cfg = %+v %v", cfg, err)
	}
	if _, _, code := run(t, "", "config", "set", "recallChars", "50"); code != 1 {
		t.Fatalf("out of range accepted: %d", code)
	}
}

// 명세 · O3-5: oni lab은 플러그인 원본을 쓰는 PC에서만 돈다. 과제를 만들고 목록에 보이며, 실험실 설정 키를 바꿀 수 있다.
func TestLab_과제_명령과_설정(t *testing.T) {
	h := home(t)
	seed(t)
	if _, errOut, code := run(t, "", "lab", "task", "list"); code == 0 || !strings.Contains(errOut, "플러그인 원본") {
		t.Fatalf("원본 없이 돌면 안 됩니다: %d %s", code, errOut)
	}
	src := filepath.Join(h, "src")
	os.MkdirAll(src, 0o755)
	cfg := config.Default()
	cfg.PluginSource = src
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := run(t, "", "lab", "task", "new", "--title", "묻기", "--instruction", "물어라", "--criteria", "풀어 쓴다"); code != 0 || !strings.Contains(out, "과제를 만들었습니다") {
		t.Fatalf("과제 %d %s %s", code, out, errOut)
	}
	if out, _, _ := run(t, "", "lab", "task", "list"); !strings.Contains(out, "「묻기」") {
		t.Fatalf("목록 %s", out)
	}
	for _, kv := range [][2]string{{"labRuns", "5"}, {"labRunUSD", "0.5"}, {"labExperimentUSD", "8"}, {"labModel", "haiku"}, {"labJudgeModel", "sonnet"}} {
		if _, errOut, code := run(t, "", "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("%s: %s", kv[0], errOut)
		}
	}
	if _, _, code := run(t, "", "config", "set", "labJudgeModel", "haiku"); code != 1 {
		t.Fatal("고르는 모델이 실행 모델과 같으면 받지 않습니다")
	}
	if out, _, _ := run(t, "", "lab", "candidates"); !strings.Contains(out, "실험실 대기 0건") {
		t.Fatalf("후보 %s", out)
	}
	// 검토 지적 4: 실험하지 않을 실험실 대기 제안은 사용자가 닫을 수 있다.
	s, _ := openStore()
	skill := filepath.Join(src, "skills", "x", "SKILL.md")
	os.MkdirAll(filepath.Dir(skill), 0o755)
	os.WriteFile(skill, []byte("옛"), 0o644)
	p, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalApplyFile, Title: "스킬", Lab: true, Payload: memory.Payload{Path: skill, Content: "새"}})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := run(t, "", "lab", "drop", p.ID, "--note", "필요 없음"); code != 0 || !strings.Contains(out, "닫았습니다") {
		t.Fatalf("닫기 %d %s %s", code, out, errOut)
	}
	s, _ = openStore()
	defer s.Close()
	if got, _ := s.GetProposal(p.ID); got.Status != memory.StatusRejected || !strings.Contains(got.Note, "필요 없음") {
		t.Fatalf("닫은 제안 %+v", got)
	}
}

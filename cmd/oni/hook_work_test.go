package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/work"
)

func probeInput(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "work", "hook", "testdata", "claude-2.1.303", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func withSession(t *testing.T, raw, session, cwd string) string {
	t.Helper()
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	m["session_id"], m["cwd"] = session, cwd
	b, _ := json.Marshal(m)
	return string(b)
}

func works(t *testing.T) work.WorkPage {
	t.Helper()
	s, err := work.Open(paths().WorkDB())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.ListWorks(work.Filter{}, work.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func contextOf(t *testing.T, out string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var o struct {
		Hook struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("훅 출력이 JSON이 아닙니다: %q", out)
	}
	return o.Hook.Context
}

// 설계 O2: 작업을 연 목록 도구 호출의 사후 훅이 작업 id를 넣은 제목 요청 문맥을 한 번 붙인다.
func TestHookWork_작업을_열면_제목을_한_번_요청한다(t *testing.T) {
	home(t)
	out, _, code := run(t, probeInput(t, "PostToolUse.TaskCreate.1"), "hook", "work", "--host", "claude")
	if code != 0 {
		t.Fatalf("종료 %d", code)
	}
	p := works(t)
	if len(p.Items) != 1 {
		t.Fatalf("작업 %d", len(p.Items))
	}
	ctx := contextOf(t, out)
	if !strings.Contains(ctx, p.Items[0].ID) || !strings.Contains(ctx, "work_title") {
		t.Errorf("제목 요청 문맥: %q", ctx)
	}
	out, _, _ = run(t, probeInput(t, "PostToolUse.TaskCreate.2"), "hook", "work", "--host", "claude")
	if strings.TrimSpace(out) != "" {
		t.Errorf("두 번째 목록 호출에 문맥이 다시 붙었습니다: %q", out)
	}
	// 비동기 훅은 목록 도구를 동기 훅에 맡긴다
	run(t, probeInput(t, "PostToolUse.TaskCreate.3"), "hook", "work", "--host", "claude", "--skip-lists")
	if w := works(t); w.Items[0].Counts.Ready != 2 {
		t.Errorf("--skip-lists인데 목록을 기록했습니다: %+v", w.Items[0].Counts)
	}
}

func TestHookWork_깨진_입력과_꺼진_설정(t *testing.T) {
	h := home(t)
	for _, in := range []string{"", "{", `{"hook_event_name":"PostToolUse"}`} {
		out, _, code := run(t, in, "hook", "work", "--host", "claude")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("%q: %d %q", in, code, out)
		}
	}
	cfg := config.Default()
	cfg.Work = config.WorkOff
	if err := config.Save(filepath.Join(h, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	run(t, probeInput(t, "PostToolUse.TaskCreate.1"), "hook", "work", "--host", "claude")
	if _, err := os.Stat(paths().WorkDB()); err == nil {
		if len(works(t).Items) != 0 {
			t.Error("작업 기록을 껐는데 기록했습니다")
		}
	}
}

func TestHookWork_회고_실행은_기록하지_않는다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_RETRO_ID", "r-1")
	run(t, probeInput(t, "PostToolUse.TaskCreate.1"), "hook", "work", "--host", "claude")
	if _, err := os.Stat(paths().WorkDB()); err == nil && len(works(t).Items) != 0 {
		t.Error("회고 실행의 목록을 작업으로 남겼습니다")
	}
}

// 설계 O9: 세션 시작 주입에 같은 프로젝트의 열린 작업 제목과 진행·막힘 단계 한 줄을 더한다.
func TestHook_세션_시작에_열린_작업을_알린다(t *testing.T) {
	h := home(t)
	seed(t) // 기억 DB가 있어야 세션 시작 주입을 한다
	dir := t.TempDir()
	run(t, withSession(t, probeInput(t, "SessionStart.1"), "S-old", dir), "hook", "session-start", "--host", "claude")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "S-old", dir), "hook", "work", "--host", "claude")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskUpdate.2"), "S-old", dir), "hook", "work", "--host", "claude")
	out, _, _ := run(t, withSession(t, probeInput(t, "SessionStart.1"), "S-new", dir), "hook", "session-start", "--host", "claude")
	ctx := contextOf(t, out)
	id := works(t).Items[0].ID
	if !strings.Contains(ctx, "열린 작업") || !strings.Contains(ctx, id) || !strings.Contains(ctx, "A 준비") {
		t.Errorf("재개 문맥: %q", ctx)
	}
	// 다른 프로젝트에는 넣지 않는다
	out, _, _ = run(t, withSession(t, probeInput(t, "SessionStart.1"), "S-other", t.TempDir()), "hook", "session-start", "--host", "claude")
	if strings.Contains(contextOf(t, out), id) {
		t.Error("다른 프로젝트 세션에 열린 작업을 넣었습니다")
	}
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	_ = config.Save(filepath.Join(h, "config.json"), cfg)
	out, _, _ = run(t, withSession(t, probeInput(t, "SessionStart.1"), "S-rules", dir), "hook", "session-start", "--host", "claude")
	if strings.Contains(contextOf(t, out), id) {
		t.Error("주입 수준 rules에서 열린 작업을 넣었습니다")
	}
	// 요청 훅은 세션을 진행으로 만든다
	run(t, withSession(t, probeInput(t, "UserPromptSubmit.1"), "S-new", dir), "hook", "prompt", "--host", "claude")
	s, _ := work.Open(paths().WorkDB())
	defer s.Close()
	d, err := s.SessionEvents("S-new", "", 0, work.ReadOptions{})
	if err != nil || d.Session.State != "running" {
		t.Errorf("요청 뒤 세션: %+v %v", d.Session, err)
	}
}

// Orbit 개선의 세션 끝 훅(hook session-end)이 작업 세션 끝도 남긴다. 진행 중 단계는 중단이 된다.
func TestHook_세션_끝은_작업_세션도_끝낸다(t *testing.T) {
	home(t)
	dir := t.TempDir()
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskCreate.1"), "S1", dir), "hook", "work", "--host", "claude")
	run(t, withSession(t, probeInput(t, "PostToolUse.TaskUpdate.2"), "S1", dir), "hook", "work", "--host", "claude")
	if _, _, code := run(t, withSession(t, probeInput(t, "SessionEnd.1"), "S1", dir), "hook", "session-end", "--host", "claude"); code != 0 {
		t.Fatal(code)
	}
	s, _ := work.Open(paths().WorkDB())
	defer s.Close()
	d, err := s.SessionEvents("S1", "", 0, work.ReadOptions{})
	if err != nil || d.Session.State != "ended" || d.Session.EndReason != "session_end" {
		t.Fatalf("세션: %+v %v", d.Session, err)
	}
	w, _, _ := s.GetWork(works(t).Items[0].ID, false, work.ReadOptions{})
	if w.Steps[0].Status != "blocked" || w.Steps[0].Block.Reason != "interrupted" {
		t.Errorf("중단: %+v", w.Steps[0])
	}
}

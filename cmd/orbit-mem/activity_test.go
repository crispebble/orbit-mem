package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
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

// hook runs a hook that must finish in time. A hook over its time limit still ends 0 with an empty
// result (fail-open) and drops its records; this helper fails on that notice so the cause shows
// as itself, not as a missing record. 일부러 한도를 넘기는 fail-open 시험은 run을 바로 쓴다.
func hook(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"hook"}, args...)...)
	if code != 0 {
		t.Fatalf("hook %v: 종료 %d %s", args, code, errOut)
	}
	if strings.Contains(errOut, i18n.T("cli.hook.timeout")) {
		t.Fatalf("hook %v: 시간 한도를 넘겨 결과와 기록을 버렸다: %s", args, errOut)
	}
	return out
}

// countingGit puts a git in front of PATH that writes one line per run to a file and then runs the
// real git. It gives the file's line count so far (시간에 기대지 않고 git 실행 수를 센다).
func countingGit(t *testing.T) func() int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("가짜 git이 셸 스크립트라 Windows에서는 돌지 않는다(실행 수 세기는 unix에서 본다)")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git 없음")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "runs")
	script := "#!/bin/sh\necho run >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// macOS는 새로 쓴 실행 파일을 처음 돌 때 검사한다(0.3초 넘게, 부하 때 더): 한 번 돌려 둔 뒤 세기 시작한다.
	// 그러지 않으면 시험 대상의 첫 git이 그 시간을 떠안아 훅 한도를 넘긴다.
	if err := exec.Command(filepath.Join(bin, "git"), "--version").Run(); err != nil {
		t.Fatal(err)
	}
	os.Remove(log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "\n")
	}
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

// 간헐 실패 a8b: 세션 끝 훅의 한도(0.5초) 안에서 git이 돌아 부하 때 끝 기록이 버려졌다. 시작 훅이 줄을 만든 세션의
// 끝은 그 줄의 끝 시각만 고치고 프로젝트를 다시 판정하지 않는다(git 0번). 시작이 빠진 세션의 끝만 판정해 새 줄을 만든다.
// 저장소일 수 없는 폴더는 시작도 git을 부르지 않으므로(0.17 r0) 시작이 git을 부르는 저장소 폴더에서 본다.
func TestHook_세션_끝은_시작된_세션에서_git을_부르지_않는다(t *testing.T) {
	home(t)
	seed(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	runs := countingGit(t)
	hook(t, hookIn("S1", dir, ""), "session-start", "--host", "claude")
	if runs() == 0 {
		t.Fatal("가짜 git이 불리지 않았다: 시험이 git 실행을 세지 못한다")
	}
	before := runs()
	hook(t, hookIn("S1", dir, ""), "session-end", "--host", "claude")
	if n := runs() - before; n != 0 {
		t.Fatalf("세션 끝 git 실행 %d번, 0번이어야 한다", n)
	}
	as := activities(t)
	if len(as) != 1 || as[0].EndedAt == 0 || as[0].ProjectScope == "" || as[0].ProjectLabel == "" || as[0].Cwd != dir {
		t.Fatalf("끝 %+v", as)
	}
	// 시작 훅이 빠진 세션: 끝이 프로젝트를 판정해 줄을 만든다(전과 같은 줄)
	other := t.TempDir()
	hook(t, hookIn("S2", other, ""), "session-end", "--host", "claude")
	as = activities(t)
	var got *memory.Activity
	for i := range as {
		if as[i].Session == "S2" {
			got = &as[i]
		}
	}
	if got == nil || got.EndedAt == 0 || got.StartedAt == 0 || got.Cwd != other || got.ProjectScope == "" || got.ProjectLabel == "" || got.Launcher != "human" {
		t.Fatalf("시작 없는 끝 %+v", as)
	}
}

// 간헐 실패(0.17 r0): 요청·도구·서브에이전트 훅이 한도(도구 0.5초) 안에서 git을 띄워 부하 때 문맥이 비었다.
// 저장소 폴더는 시작 훅의 판정을 그 세션의 훅이 git 없이 다시 쓰고, 저장소일 수 없는 폴더는 어느 훅도 git을 부르지 않는다.
func TestHook_세션의_훅은_시작의_판정을_다시_써서_git을_부르지_않는다(t *testing.T) {
	home(t)
	seed(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	plain := t.TempDir()
	runs := countingGit(t)
	in := func(session, cwd string, more map[string]string) string {
		m := map[string]string{"session_id": session, "cwd": cwd, "hook_event_name": "x"}
		for k, v := range more {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	for _, c := range []struct {
		session, cwd string
		startGit     bool
	}{{"G1", repo, true}, {"P1", plain, false}} {
		before := runs()
		hook(t, in(c.session, c.cwd, map[string]string{"source": "startup"}), "session-start", "--host", "claude")
		if started := runs() - before; (started > 0) != c.startGit {
			t.Fatalf("%s 시작 git 실행 %d번", c.cwd, started)
		}
		before = runs()
		hook(t, in(c.session, c.cwd, map[string]string{"prompt": "답은 존댓말로 해 줘"}), "prompt", "--host", "claude")
		hook(t, in(c.session, c.cwd, map[string]string{"tool_name": "Bash"}), "pre-tool", "--host", "claude")
		hook(t, in(c.session, c.cwd, map[string]string{"agent_id": "a1", "agent_type": "worker"}), "subagent-start", "--host", "claude")
		hook(t, in(c.session, c.cwd, nil), "session-end", "--host", "claude")
		if n := runs() - before; n != 0 {
			t.Fatalf("%s 시작 뒤 훅 git 실행 %d번, 0번이어야 한다", c.cwd, n)
		}
	}
	for _, a := range activities(t) {
		if a.Prompts != 1 || a.EndedAt == 0 || a.ProjectScope == "" {
			t.Fatalf("활동 %+v", a)
		}
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

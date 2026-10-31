package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/openorbit/internal/memory"
)

type injected struct {
	Text      string   `json:"text"`
	MemoryIDs []string `json:"memoryIds"`
}

func runInject(t *testing.T, stdin string, args ...string) injected {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"inject"}, args...)...)
	var v injected
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
		t.Fatalf("inject %v: code %d out %q err %q", args, code, out, errOut)
	}
	return v
}

// orbit-pi의 기억 원천: 세션 묶음과 요청별 기억을 JSON으로 주고 사용 사건을 orbit-pi 호스트로 남긴다.
func TestInject_orbit_pi에_기억_문맥을_JSON으로_준다(t *testing.T) {
	home(t)
	s := seed(t)
	r, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	d, _ := s.Save(memory.SaveInput{Kind: "decision", Title: "atlas main 병합은 운영 배포", Body: "병합 전 확인", Source: memory.Source{Kind: "quote", Excerpt: "배포"}})

	start := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "pi-1")
	if !strings.Contains(start.Text, "답은 존댓말") || !containsID(start.MemoryIDs, r.ID) {
		t.Errorf("세션 묶음 = %+v", start)
	}
	prompt := runInject(t, "atlas main 병합 배포", "prompt", "--cwd", "/tmp", "--session", "pi-1")
	if !containsID(prompt.MemoryIDs, d.ID) {
		t.Errorf("요청별 기억 = %+v", prompt)
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if len(evs) != 2 || evs[0].Host != "orbit-pi" || evs[0].Session != "pi-1" || evs[0].Kind != memory.EventInjectSessionStart || evs[1].Kind != memory.EventInjectPrompt {
		t.Errorf("사건 = %+v", evs)
	}
}

func TestInject_DB가_없으면_빈_문맥이고_인자가_틀리면_사용법(t *testing.T) {
	home(t)
	if v := runInject(t, "", "session-start", "--cwd", "/tmp"); v.Text != "" || len(v.MemoryIDs) != 0 || v.MemoryIDs == nil {
		t.Errorf("빈 DB = %+v", v)
	}
	if _, _, code := run(t, "", "inject", "bogus"); code != 2 {
		t.Errorf("틀린 인자 code %d", code)
	}
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestRuntime_설치하고_상태를_본다(t *testing.T) {
	home(t)
	if _, errOut, code := run(t, "", "runtime", "status"); code != 1 || !strings.Contains(errOut, "not installed") {
		t.Fatalf("설치 전 status: code %d %q", code, errOut)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet", "-b", "main"}, {"add", "orbit"}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "--quiet", "-m", "one"}} {
		if args[0] == "add" {
			os.MkdirAll(filepath.Join(repo, "orbit", "scripts"), 0o755)
			os.WriteFile(filepath.Join(repo, "orbit", "scripts", "build.sh"), []byte("#!/usr/bin/env bash\ntrue\n"), 0o755)
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, errOut, code := run(t, "", "runtime", "install", "--repo", repo); code != 0 {
		t.Fatalf("install: code %d %s", code, errOut)
	}
	out, _, code := run(t, "", "runtime", "status", "--json")
	var st struct {
		Commit  string
		Current bool
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || len(st.Commit) != 40 || !st.Current {
		t.Fatalf("status: code %d %s", code, out)
	}
	if _, _, code := run(t, "", "runtime", "bogus"); code != 2 {
		t.Errorf("틀린 인자 code %d", code)
	}
}

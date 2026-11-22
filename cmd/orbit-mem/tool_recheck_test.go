package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// `orbit-mem tool memory_save --cwd <폴더>`는 호출자가 작업 폴더를 고른다. git 밖 폴더(점 폴더 꼴의 자격 증명 폴더)를 주고 그 안의
// 파일을 근거로 대도 서버는 그 파일을 읽지 않는다: 응답의 state가 「그 파일에 그 발췌가 있다」는 신호가 되지 않는다. 같은 호출을 저장소
// 안 폴더로 주면(대조군) 재확인해 바로 확정한다.
func TestTool_memory_save는_git_밖_폴더의_파일을_재확인하지_않는다(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	h := home(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	fake := filepath.Join(base, "home")
	t.Setenv("HOME", fake) // 이 아래부터 홈은 시험의 가짜 폴더다(홈 규칙과 git의 사용자 설정이 이 PC에 기대지 않게)
	t.Setenv("USERPROFILE", fake)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s := seed(t)
	if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 make deploy", Topic: "배포 명령", Scope: "project:github.com/me/app",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 make deploy"}}); err != nil {
		t.Fatal(err)
	}
	const line = "aws_secret_access_key = AKIA-test-only-0000"
	write := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	repo := filepath.Join(fake, "app")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	save := func(cwd, ref string) string {
		t.Helper()
		in, _ := json.Marshal(map[string]any{"kind": "fact", "title": "배포 자격 " + cwd, "topic": "배포 명령", "appliesTo": "repo:github.com/me/app",
			"source": map[string]any{"kind": "verification", "ref": ref, "excerpt": line}})
		out, errOut, code := run(t, string(in), "tool", "memory_save", "--host", "host-x", "--cwd", cwd)
		var r struct {
			Memory memory.Memory `json:"memory"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &r) != nil {
			t.Fatalf("save %s: code %d out %q err %q (%s)", cwd, code, out, errOut, h)
		}
		return r.Memory.State
	}
	if got := save(repo, write(filepath.Join(repo, "creds"))); got != memory.StateConfirmed {
		t.Fatalf("대조군: 저장소 안 파일의 상태 %s", got)
	}
	aws := filepath.Join(fake, ".aws")
	if got := save(aws, write(filepath.Join(aws, "credentials"))); got != memory.StateCandidate {
		t.Errorf("점 폴더를 --cwd로 줬는데 상태 %s", got)
	}
	work := filepath.Join(fake, "work")
	if got := save(work, write(filepath.Join(work, "notes"))); got != memory.StateCandidate {
		t.Errorf("홈 아래 git 밖 폴더를 --cwd로 줬는데 상태 %s", got)
	}
}

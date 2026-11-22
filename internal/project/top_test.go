package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitFor gives a runner of git for a test. The environment is fixed so no user git setting is read.
func gitFor(t *testing.T) func(args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func realSlashOf(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(r)
}

// Top은 폴더가 속한 git 작업 트리의 최상위다: 하위 폴더에서 열어도, 원격이 있어도 없어도 같고, 연결된 worktree는 그 worktree의
// 최상위다(주 저장소가 아니다). git 밖이거나 홈 전체가 저장소이면 비어 있다.
func TestResolve_Top은_작업_트리의_최상위다(t *testing.T) {
	git := gitFor(t)
	base := t.TempDir()
	main := filepath.Join(base, "main")
	git("init", "-q", main)
	git("-C", main, "commit", "-q", "--allow-empty", "-m", "x")
	sub := filepath.Join(main, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, ".hidden", "wt") // 루트 위쪽의 점 폴더
	git("-C", main, "worktree", "add", "-q", wt)
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	wantMain, wantWt := realSlashOf(t, main), realSlashOf(t, wt)
	check := func(name, dir, want string) {
		t.Helper()
		if got := Resolve(dir).Top; got != want {
			t.Errorf("%s: Top %q, want %q", name, got, want)
		}
	}
	check("원격 없음, 하위 폴더", sub, wantMain)
	check("원격 없음, 최상위", main, wantMain)
	check("연결된 worktree", wt, wantWt)
	check("git 밖", plain, "")
	check("없는 폴더", filepath.Join(base, "none"), "")
	git("-C", main, "remote", "add", "origin", "git@github.com:a/b.git")
	check("원격 있음, 하위 폴더", sub, wantMain)
	check("원격 있음, 연결된 worktree", wt, wantWt)
	if got := GitTop(sub); got != wantMain {
		t.Errorf("GitTop 하위 폴더 %q", got)
	}
	if got := GitTop(wt); got != wantWt {
		t.Errorf("GitTop worktree %q", got)
	}
	if got := GitTop(plain); got != "" {
		t.Errorf("GitTop git 밖 %q", got)
	}
	// 홈 전체가 저장소이면 최상위가 홈이라 루트가 아니다.
	home := filepath.Join(base, "home")
	git("init", "-q", home)
	t.Setenv("HOME", home)
	inHome := filepath.Join(home, "proj")
	if err := os.MkdirAll(inHome, 0o755); err != nil {
		t.Fatal(err)
	}
	check("홈 저장소 아래", inHome, "")
	if got := GitTop(inHome); got != "" {
		t.Errorf("GitTop 홈 저장소 아래 %q", got)
	}
}

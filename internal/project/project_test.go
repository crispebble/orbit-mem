package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote_주소_모양이_달라도_같은_키(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Onidot-Labs/orbit.git":          "github.com/onidot-labs/orbit",
		"https://github.com/onidot-labs/orbit.git":      "github.com/onidot-labs/orbit",
		"https://user:tok@github.com/onidot-labs/orbit": "github.com/onidot-labs/orbit",
		"ssh://git@github.com/onidot-labs/orbit.git/":   "github.com/onidot-labs/orbit",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestResolve_원격이_없으면_저장소_최상위_경로(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q").Run()
	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)
	p := Resolve(sub)
	real, _ := filepath.EvalSymlinks(dir)
	if p.Scope != "project:"+filepath.ToSlash(real) {
		t.Fatalf("scope = %s, want project:%s", p.Scope, real)
	}
	exec.Command("git", "-C", dir, "remote", "add", "origin", "git@github.com:a/b.git").Run()
	if p := Resolve(sub); p.Scope != "project:github.com/a/b" {
		t.Fatalf("scope = %s", p.Scope)
	}
}

func TestResolve_git이_아니면_경로(t *testing.T) {
	dir := t.TempDir()
	p := Resolve(dir)
	if p.Scope == "" || p.Label == "" {
		t.Fatalf("got %+v", p)
	}
	if Resolve("").Scope != "" {
		t.Fatal("empty cwd must give no project")
	}
}

// S3-R4(명세 2.2b): 원격이 없는 저장소의 worktree는 주 저장소 경로를 키로 쓰고, 세션 폴더와 git·임시 여부를 안다.
func TestResolve_원격_없는_worktree는_주_저장소_키를_쓴다(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", main)
	run("-C", main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(dir, "elsewhere", "wt")
	run("-C", main, "worktree", "add", "-q", wt)
	realMain, _ := filepath.EvalSymlinks(main)
	realWt, _ := filepath.EvalSymlinks(wt)
	pm, pw := Resolve(main), Resolve(filepath.Join(wt))
	if pm.Scope != "project:"+filepath.ToSlash(realMain) || pw.Scope != pm.Scope {
		t.Fatalf("main %s, worktree %s", pm.Scope, pw.Scope)
	}
	if pw.Dir != filepath.ToSlash(realWt) || !pw.Git {
		t.Fatalf("worktree dir %q git %v", pw.Dir, pw.Git)
	}
	// t.TempDir()는 $TMPDIR 아래라 임시 폴더다.
	if !pw.Temp {
		t.Fatal("temp folder not seen")
	}
	plain := Resolve(dir)
	if plain.Git || plain.Dir == "" {
		t.Fatalf("plain %+v", plain)
	}
}

func TestIsTemp_임시_폴더(t *testing.T) {
	for dir, want := range map[string]bool{
		"/tmp/x": true, "/private/tmp/claude-501/s": true, "/tmp": true,
		"/Users/u/_workspace/onidot": false, "/tmpfoo/x": false,
	} {
		if got := IsTemp(dir); got != want {
			t.Errorf("IsTemp(%s) = %v", dir, got)
		}
	}
	if !IsTemp(filepath.ToSlash(os.TempDir())) {
		t.Error("TMPDIR")
	}
}

// 검토 지적(2026-11-04): 주 저장소 경로는 연결된 worktree일 때만 쓴다. 서브모듈은 그 checkout 최상위가 키다.
func TestResolve_서브모듈은_checkout_최상위가_키다(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	lib, sup := filepath.Join(dir, "lib"), filepath.Join(dir, "sup")
	run("init", "-q", lib)
	run("-C", lib, "commit", "-q", "--allow-empty", "-m", "x")
	run("init", "-q", sup)
	run("-C", sup, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "sub")
	run("-C", filepath.Join(sup, "sub"), "remote", "remove", "origin") // 원격 없는 서브모듈
	p := Resolve(filepath.Join(sup, "sub"))
	if want := "project:" + filepath.ToSlash(filepath.Join(sup, "sub")); p.Scope != want || !p.Git {
		t.Fatalf("submodule %s, want %s", p.Scope, want)
	}
}

// 홈 자체가 저장소(dotfiles)면 홈 아래 폴더는 git 밖 폴더로 본다(S3-R1 ④).
func TestResolve_홈_저장소_아래는_폴더다(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	h, _ := filepath.EvalSymlinks(t.TempDir())
	exec.Command("git", "init", "-q", h).Run()
	sub := filepath.Join(h, "notes")
	os.MkdirAll(sub, 0o755)
	t.Setenv("HOME", h)
	p := Resolve(sub)
	if p.Git || p.Scope != "project:"+filepath.ToSlash(sub) {
		t.Fatalf("%+v", p)
	}
}

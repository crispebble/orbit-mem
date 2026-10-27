package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote_주소_모양이_달라도_같은_키(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Onidot-Labs/orbit.git":        "github.com/onidot-labs/orbit",
		"https://github.com/onidot-labs/orbit.git":    "github.com/onidot-labs/orbit",
		"https://user:tok@github.com/onidot-labs/orbit": "github.com/onidot-labs/orbit",
		"ssh://git@github.com/onidot-labs/orbit.git/": "github.com/onidot-labs/orbit",
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

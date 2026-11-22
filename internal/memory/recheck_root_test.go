package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 재확인 루트의 판정(홈·홈 위는 파일 동일성으로, 작업 트리 최상위만)과 확인 뒤 바꿔치기. 루트가 무엇이 되는지는 호출하는 쪽
// (mcpserver recheckRoots)이 git으로 정하고, 이 시험은 RecheckExcerpt가 받은 루트를 어떻게 거르는지를 본다.

const recheckLine = "deploy: ./scripts/release.sh"

// recheckHome is a fake home folder: HOME and USERPROFILE point at a folder of the test.
func recheckHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// recheckWorktree makes a folder that looks like a work tree top (it has a .git) with a file holding
// recheckLine, and gives the folder and the file.
func recheckWorktree(t *testing.T, dir string) (root, file string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(dir, "Makefile")
	if err := os.WriteFile(file, []byte(recheckLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func realTemp(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestRecheckExcerpt_홈과_홈_위는_파일_동일성으로_루트에서_뺀다(t *testing.T) {
	base := realTemp(t)
	home := filepath.Join(base, "home")
	repo, repoFile := recheckWorktree(t, filepath.Join(home, "app"))
	recheckHome(t, home)
	homeRoot, homeFile := recheckWorktree(t, home) // 홈 전체가 저장소
	baseRoot, baseFile := recheckWorktree(t, base)
	if !RecheckExcerpt([]string{repo}, repoFile, recheckLine) {
		t.Fatal("홈 아래 저장소의 파일을 못 읽었다")
	}
	if RecheckExcerpt([]string{homeRoot}, homeFile, recheckLine) {
		t.Error("홈 자체를 루트로 읽었다")
	}
	if RecheckExcerpt([]string{baseRoot}, baseFile, recheckLine) {
		t.Error("홈 위를 루트로 읽었다")
	}
	// 홈이 링크로 주어져도 같은 폴더다.
	link := filepath.Join(base, "homelink")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	recheckHome(t, link)
	if RecheckExcerpt([]string{homeRoot}, homeFile, recheckLine) || RecheckExcerpt([]string{baseRoot}, baseFile, recheckLine) {
		t.Error("링크로 준 홈을 가리지 못했다")
	}
}

// 대소문자를 가리지 않는 파일 시스템에서 홈의 대소문자 변형은 문자열로는 다른 경로지만 같은 폴더다. 가리는 파일 시스템이
// 아니면 이 시험은 볼 것이 없다.
func TestRecheckExcerpt_홈의_대소문자_변형도_루트에서_뺀다(t *testing.T) {
	base := realTemp(t)
	home := filepath.Join(base, "home")
	root, file := recheckWorktree(t, home)
	recheckHome(t, home)
	upper := filepath.Join(base, "HOME")
	hi, err := os.Stat(upper)
	if err != nil {
		t.Skip("대소문자를 가리는 파일 시스템")
	}
	if ri, _ := os.Stat(root); !os.SameFile(hi, ri) {
		t.Skip("대소문자를 가리는 파일 시스템")
	}
	if RecheckExcerpt([]string{upper}, filepath.Join(upper, filepath.Base(file)), recheckLine) {
		t.Error("홈의 대소문자 변형을 루트로 읽었다")
	}
	if RecheckExcerpt([]string{strings.ToUpper(base)}, filepath.Join(strings.ToUpper(base), "home", "Makefile"), recheckLine) {
		t.Error("홈 위의 대소문자 변형을 루트로 읽었다")
	}
}

func TestRecheckExcerpt_홈을_모르면_루트가_없다(t *testing.T) {
	base := realTemp(t)
	root, file := recheckWorktree(t, filepath.Join(base, "app"))
	recheckHome(t, filepath.Join(base, "other"))
	if !RecheckExcerpt([]string{root}, file, recheckLine) {
		t.Fatal("홈을 아는 때 읽혀야 한다")
	}
	recheckHome(t, "")
	if RecheckExcerpt([]string{root}, file, recheckLine) {
		t.Error("홈을 모르는데 읽었다")
	}
}

// 루트는 작업 트리의 최상위(.git이 있는 폴더)뿐이다: 홈 바로 아래 폴더나 점 폴더도 .git이 없으면 루트가 아니다. 루트 위쪽의
// 점 폴더는 문제 삼지 않는다(연결된 worktree가 점 폴더 아래에 있어도 읽힌다).
func TestRecheckExcerpt_루트는_작업_트리_최상위뿐이고_루트_위쪽의_점_폴더는_괜찮다(t *testing.T) {
	base := realTemp(t)
	recheckHome(t, filepath.Join(base, "home"))
	plain := filepath.Join(base, "home", "Documents")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	plainFile := filepath.Join(plain, "notes.txt")
	if err := os.WriteFile(plainFile, []byte(recheckLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if RecheckExcerpt([]string{plain}, plainFile, recheckLine) {
		t.Error("작업 트리가 아닌 폴더를 루트로 읽었다")
	}
	dot := filepath.Join(base, "home", ".aws")
	if err := os.MkdirAll(dot, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(dot, "credentials")
	if err := os.WriteFile(credentials, []byte(recheckLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if RecheckExcerpt([]string{dot}, credentials, recheckLine) {
		t.Error("점 폴더를 루트로 읽었다")
	}
	root, file := recheckWorktree(t, filepath.Join(base, "home", ".claude", "worktrees", "x"))
	if !RecheckExcerpt([]string{root}, file, recheckLine) {
		t.Error("루트 위쪽의 점 폴더 때문에 읽지 못했다")
	}
}

// 풀어 둔 경로를 확인한 뒤 열기 전에 링크나 다른 파일로 바꿔치기해도 읽지 않는다: 유닉스는 열 때 링크를 따르지 않고, 어디서나
// 연 파일이 확인한 경로의 파일과 같은지 본다.
func TestRecheckExcerpt_확인한_뒤_바꿔치기하면_읽지_않는다(t *testing.T) {
	base := realTemp(t)
	recheckHome(t, filepath.Join(base, "home"))
	outside := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte(recheckLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(outside, "other")
	if err := os.WriteFile(other, []byte(recheckLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	real := recheckOpen
	t.Cleanup(func() { recheckOpen = real })
	for _, c := range []struct {
		name string
		swap func(path string) error
		want bool
	}{
		{"바꿔치기 없음", func(string) error { return nil }, true},
		{"밖을 가리키는 링크로", func(p string) error {
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Symlink(secret, p)
		}, false},
		{"같은 내용의 다른 파일로", func(p string) error { return os.Rename(other, p) }, false},
	} {
		root, file := recheckWorktree(t, filepath.Join(base, "repo-"+strings.NewReplacer(" ", "-", ",", "").Replace(c.name)))
		recheckOpen = func(p string) (*os.File, error) {
			if err := c.swap(p); err != nil {
				t.Fatal(err)
			}
			return real(p)
		}
		if got := RecheckExcerpt([]string{root}, file, recheckLine); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if err := os.WriteFile(other, []byte(recheckLine+"\n"), 0o600); err != nil { // 다음 경우를 위해 되돌린다
			t.Fatal(err)
		}
	}
}

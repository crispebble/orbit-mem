package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// countingGit puts a git in front of PATH that writes one line per run to a file and then runs
// the real git (with body before it, if any). It gives the file's line count so far.
// 시간에 기대지 않고 git 실행 수를 센다(간헐 실패 a8b: 훅 한도 안에서 git이 여러 번 돌았다).
func countingGit(t *testing.T, body string) func() int {
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
	script := "#!/bin/sh\necho run >> '" + log + "'\nREAL='" + real + "'\n" + body + "exec \"$REAL\" \"$@\"\n"
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

// git 저장소가 아닌 폴더는 원격 조회와 rev-parse 한 번이면 안다: 「저장소 아님」 실패 뒤 옛 git용 재시도를 하지 않는다.
func TestResolve_git이_아닌_폴더는_git을_두_번까지만_부른다(t *testing.T) {
	runs := countingGit(t, "")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	p := Resolve(dir)
	if p.Git || p.Scope != "project:"+filepath.ToSlash(dir) || p.Label != filepath.ToSlash(dir) || p.Dir != filepath.ToSlash(dir) {
		t.Fatalf("%+v", p)
	}
	if n := runs(); n > 2 {
		t.Fatalf("git 실행 %d번, 두 번 이하여야 한다", n)
	}
}

// 없는 폴더에서는 git이 늘 실패한다: 부르지 않고 그 경로를 키로 쓴다(전과 같은 결과).
func TestResolve_없는_폴더는_git을_부르지_않는다(t *testing.T) {
	runs := countingGit(t, "")
	dir := filepath.Join(t.TempDir(), "gone", "app")
	p := Resolve(dir)
	if p.Git || p.Scope != "project:"+realSlash(dir) || p.Dir != realSlash(dir) {
		t.Fatalf("%+v", p)
	}
	if n := runs(); n != 0 {
		t.Fatalf("git 실행 %d번, 0번이어야 한다", n)
	}
}

// --path-format을 모르는 옛 git(2.31 전)은 그 옵션을 글 그대로 되돌려 주고 성공한다(줄 수가 어긋남):
// 그때만 --show-toplevel로 다시 묻는다.
func TestResolve_옛_git은_show_toplevel로_다시_묻는다(t *testing.T) {
	old := `if [ "$1" = "-C" ] && [ "$3" = "rev-parse" ]; then
  case "$4" in --path-format=*) d="$2"; f="$4"; shift 4; echo "$f"; exec "$REAL" -C "$d" rev-parse "$@";; esac
fi
`
	runs := countingGit(t, old)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a")
	os.MkdirAll(sub, 0o755)
	before := runs()
	p := Resolve(sub)
	if !p.Git || p.Scope != "project:"+filepath.ToSlash(dir) {
		t.Fatalf("%+v", p)
	}
	if n := runs() - before; n != 3 {
		t.Fatalf("git 실행 %d번, 원격·rev-parse·옛 git 재시도 세 번이어야 한다", n)
	}
}

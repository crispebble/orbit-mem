package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs a real git for a test's setup (before countingGit, or counted apart).
func gitIn(t *testing.T, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// 간헐 실패(0.17 r0): 훅 한도 안에서 저장소일 수 없는 폴더에도 git을 두 번 띄웠다. 그 폴더와 조상에 .git도 HEAD도 없고
// git 위치를 바꾸는 환경 변수가 없으면 git을 부르지 않고 폴더 키를 쓴다(전과 같은 결과).
func TestResolve_저장소일_수_없는_폴더는_git을_부르지_않는다(t *testing.T) {
	runs := countingGit(t, "")
	dir := realTemp(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, sub} {
		p := Resolve(d)
		want := Project{Scope: "project:" + filepath.ToSlash(d), Label: filepath.ToSlash(d), Dir: filepath.ToSlash(d), Temp: true}
		if p != want {
			t.Fatalf("%s: %+v, want %+v", d, p, want)
		}
	}
	if n := runs(); n != 0 {
		t.Fatalf("git 실행 %d번, 0번이어야 한다", n)
	}
}

// 판정을 건너뛰는 길이 생겨도 git이 저장소로 보는 폴더의 결과는 전과 같다(특성 시험: 기대값은 git이 정한 값).
func TestResolve_git이_보는_저장소는_전과_같다(t *testing.T) {
	dir := realTemp(t)
	slash := func(parts ...string) string {
		return filepath.ToSlash(filepath.Join(append([]string{dir}, parts...)...))
	}

	// 원격 있는 저장소의 하위 폴더
	gitIn(t, "init", "-q", filepath.Join(dir, "r"))
	gitIn(t, "-C", filepath.Join(dir, "r"), "remote", "add", "origin", "git@github.com:A/R.git")
	gitIn(t, "-C", filepath.Join(dir, "r"), "commit", "-q", "--allow-empty", "-m", "x")
	os.MkdirAll(filepath.Join(dir, "r", "x", "y"), 0o755)
	// 원격 없는 저장소와 그 연결된 worktree(.git이 파일)
	gitIn(t, "init", "-q", filepath.Join(dir, "plain"))
	gitIn(t, "-C", filepath.Join(dir, "plain"), "commit", "-q", "--allow-empty", "-m", "x")
	gitIn(t, "-C", filepath.Join(dir, "plain"), "worktree", "add", "-q", filepath.Join(dir, "wt"))
	os.MkdirAll(filepath.Join(dir, "wt", "deep"), 0o755)
	// 원격 있는 bare 저장소(.git 없이 HEAD만 있다)와 원격 없는 bare
	gitIn(t, "init", "-q", "--bare", filepath.Join(dir, "bare.git"))
	gitIn(t, "-C", filepath.Join(dir, "bare.git"), "config", "remote.origin.url", "https://github.com/a/bare.git")
	gitIn(t, "init", "-q", "--bare", filepath.Join(dir, "lone.git"))
	// 저장소로 가는 심볼릭 링크(저장소 밖에 있는 링크)
	os.Symlink(filepath.Join(dir, "r", "x"), filepath.Join(dir, "link"))

	cases := []struct {
		cwd  string
		want Project
	}{
		{filepath.Join(dir, "r", "x", "y"), Project{Scope: "project:github.com/a/r", Label: "github.com/a/r", Dir: slash("r", "x", "y"), Git: true, Temp: true}},
		{filepath.Join(dir, "plain"), Project{Scope: "project:" + slash("plain"), Label: slash("plain"), Dir: slash("plain"), Git: true, Temp: true}},
		{filepath.Join(dir, "wt", "deep"), Project{Scope: "project:" + slash("plain"), Label: slash("plain"), Dir: slash("wt", "deep"), Git: true, Temp: true}},
		{filepath.Join(dir, "bare.git"), Project{Scope: "project:github.com/a/bare", Label: "github.com/a/bare", Dir: slash("bare.git"), Git: true, Temp: true}},
		{filepath.Join(dir, "lone.git"), Project{Scope: "project:" + slash("lone.git"), Label: slash("lone.git"), Dir: slash("lone.git"), Temp: true}},
		{filepath.Join(dir, "plain", ".git"), Project{Scope: "project:" + slash("plain", ".git"), Label: slash("plain", ".git"), Dir: slash("plain", ".git"), Temp: true}},
		{filepath.Join(dir, "link"), Project{Scope: "project:github.com/a/r", Label: "github.com/a/r", Dir: slash("r", "x"), Git: true, Temp: true}},
	}
	for _, c := range cases {
		if p := Resolve(c.cwd); p != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.cwd, p, c.want)
		}
	}
}

// GIT_DIR(과 GIT_WORK_TREE)는 폴더 밖의 저장소를 가리킨다: 폴더에 .git이 없어도 git에 묻는다(전과 같은 결과).
func TestResolve_GIT_DIR_환경이면_git에_묻는다(t *testing.T) {
	dir := realTemp(t)
	repo := filepath.Join(dir, "repo")
	gitIn(t, "init", "-q", repo)
	gitIn(t, "-C", repo, "remote", "add", "origin", "git@github.com:a/env.git")
	plain := filepath.Join(dir, "plain")
	os.MkdirAll(plain, 0o755)
	runs := countingGit(t, "")
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	p := Resolve(plain)
	if want := (Project{Scope: "project:github.com/a/env", Label: "github.com/a/env", Dir: filepath.ToSlash(plain), Git: true, Temp: true}); p != want {
		t.Fatalf("%+v, want %+v", p, want)
	}
	if runs() == 0 {
		t.Fatal("GIT_DIR가 있는데 git을 부르지 않았다")
	}
}

// 설정을 환경으로 주는 git(GIT_CONFIG_COUNT 등)은 저장소 밖에서도 remote.origin.url을 줄 수 있다: git에 묻는다(전과 같은 결과).
func TestResolve_git_설정_환경이면_git에_묻는다(t *testing.T) {
	plain := realTemp(t)
	runs := countingGit(t, "")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "remote.origin.url")
	t.Setenv("GIT_CONFIG_VALUE_0", "git@github.com:a/cfg.git")
	p := Resolve(plain)
	if p.Scope != "project:github.com/a/cfg" || !p.Git {
		t.Fatalf("%+v", p)
	}
	if runs() == 0 {
		t.Fatal("GIT_CONFIG_COUNT가 있는데 git을 부르지 않았다")
	}
}

// 세션 안에서 같은 폴더를 다시 판정할 때(0.17 r0): 앞서 그 폴더를 판정한 값(Earlier)을 git 없이 다시 쓴다.
func TestAgain_같은_폴더는_앞선_판정을_git_없이_다시_쓴다(t *testing.T) {
	dir := realTemp(t)
	remote, plain := filepath.Join(dir, "remote"), filepath.Join(dir, "plain")
	gitIn(t, "init", "-q", remote)
	gitIn(t, "-C", remote, "remote", "add", "origin", "git@github.com:a/again.git")
	gitIn(t, "init", "-q", plain)
	os.MkdirAll(filepath.Join(plain, "sub"), 0o755)
	runs := countingGit(t, "")
	for _, c := range []struct {
		cwd      string
		gitKnown bool // 앞선 값만으로 Git까지 정해지는가(원격 키·상위 최상위면 저장소다)
	}{{remote, true}, {filepath.Join(plain, "sub"), true}, {plain, false}} {
		fresh := Resolve(c.cwd)
		before := runs()
		asked := 0
		p, known := Again(c.cwd, func() (Earlier, bool) {
			asked++
			return Earlier{Cwd: c.cwd, Scope: fresh.Scope, Label: fresh.Label}, true
		})
		if n := runs() - before; n != 0 {
			t.Fatalf("%s: git 실행 %d번, 0번이어야 한다", c.cwd, n)
		}
		if asked != 1 || p != fresh || known != c.gitKnown {
			t.Fatalf("%s: asked %d known %v\n got %+v\nwant %+v", c.cwd, asked, known, p, fresh)
		}
	}
}

// 앞선 값을 쓸 수 없으면 Resolve와 같다: 다른 폴더, 없는 값, 상대 경로, 모양이 틀린 값, git 위치 환경.
func TestAgain_쓸_수_없는_앞선_값이면_git으로_판정한다(t *testing.T) {
	dir := realTemp(t)
	repo := filepath.Join(dir, "repo")
	gitIn(t, "init", "-q", repo)
	gitIn(t, "-C", repo, "remote", "add", "origin", "git@github.com:a/fresh.git")
	stale := Earlier{Cwd: repo, Scope: "project:github.com/a/stale", Label: "github.com/a/stale"}
	runs := countingGit(t, "")
	want := Resolve(repo)
	cases := map[string]struct {
		cwd string
		e   Earlier
		ok  bool
		env bool
	}{
		"다른 폴더":     {repo, Earlier{Cwd: filepath.Join(dir, "elsewhere"), Scope: stale.Scope, Label: stale.Label}, true, false},
		"앞선 값 없음":   {repo, stale, false, false},
		"키와 라벨이 다름": {repo, Earlier{Cwd: repo, Scope: "project:x", Label: "y"}, true, false},
		"빈 키":       {repo, Earlier{Cwd: repo}, true, false},
		"GIT_DIR":   {repo, stale, true, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.env {
				t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
			}
			before := runs()
			p, known := Again(c.cwd, func() (Earlier, bool) { return c.e, c.ok })
			if p != want || !known {
				t.Fatalf("got %+v known %v, want %+v", p, known, want)
			}
			if runs() == before {
				t.Fatal("git을 부르지 않았다")
			}
		})
	}
	// 상대 경로는 프로세스 폴더에 따라 뜻이 달라 다시 쓰지 않는다.
	t.Chdir(dir)
	before := runs()
	p, _ := Again("repo", func() (Earlier, bool) { return Earlier{Cwd: "repo", Scope: stale.Scope, Label: stale.Label}, true })
	if p != want || runs() == before {
		t.Fatalf("상대 경로 %+v", p)
	}
}

// 저장소일 수 없는 폴더는 앞선 값을 보지도 않고 폴더 키다: 세션 중 .git을 지운 폴더에 옛 저장소 키를 쓰지 않는다.
func TestAgain_저장소일_수_없는_폴더는_앞선_값을_보지_않는다(t *testing.T) {
	dir := realTemp(t)
	runs := countingGit(t, "")
	p, known := Again(dir, func() (Earlier, bool) {
		t.Fatal("앞선 값을 물었다")
		return Earlier{}, false
	})
	if want := Resolve(dir); p != want || !known {
		t.Fatalf("%+v known %v, want %+v", p, known, want)
	}
	if n := runs(); n != 0 {
		t.Fatalf("git 실행 %d번", n)
	}
}

// 판정에 git이 필요한 폴더(세션의 첫 판정)는 원격 조회와 rev-parse를 함께 띄운다: 한도 안 시간이 둘의 합이 아니라
// 긴 쪽이다. 가짜 git은 서로 상대가 시작한 것을 본 뒤에야 「만남」을 적는다(차례로 돌면 먼저 돈 쪽이 상대를 못 본다).
func TestResolve_원격_조회와_rev_parse를_함께_띄운다(t *testing.T) {
	dir := realTemp(t)
	gitIn(t, "init", "-q", dir)
	marks := t.TempDir()
	meet := `case "$3" in config) me=config; other=rev-parse;; rev-parse) me=rev-parse; other=config;; *) me=; esac
if [ -n "$me" ]; then
  : > '` + marks + `'/"$me"
  i=0; while [ ! -e '` + marks + `'/"$other" ] && [ $i -lt 150 ]; do sleep 0.01; i=$((i+1)); done
  [ -e '` + marks + `'/"$other" ] && echo "$me" >> '` + marks + `'/met
fi
`
	countingGit(t, meet)
	p := Resolve(dir)
	if want := (Project{Scope: "project:" + filepath.ToSlash(dir), Label: filepath.ToSlash(dir), Dir: filepath.ToSlash(dir), Git: true, Temp: true}); p != want {
		t.Fatalf("%+v, want %+v", p, want)
	}
	b, _ := os.ReadFile(filepath.Join(marks, "met"))
	if got := string(b); got != "config\nrev-parse\n" && got != "rev-parse\nconfig\n" {
		t.Fatalf("두 git이 함께 돌지 않았다: %q", got)
	}
}

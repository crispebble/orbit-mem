package project

import (
	"os"
	"path/filepath"
	"testing"
)

// failOnce is a countingGit body: the first run of one git command (config or rev-parse) ends as how
// says, and the later runs go to the real git. A folder in marks holds the turn (mkdir is atomic).
func failOnce(marks, command, how string) string {
	return `if [ "$3" = "` + command + `" ] && mkdir '` + marks + `/failed' 2>/dev/null; then ` + how + `; fi
`
}

// J-1(0.17 r6): git이 답하지 못한 판정(실행 실패·신호로 끝남·뜻밖의 종료 코드, 저장소에서 원격을 읽지 못함)은
// 「모름」(Unsure)이다. 그 키는 짐작이라 세션의 다음 op가 다시 묻는다. 다음 판정은 git의 답을 얻는다.
func TestResolve_git이_답하지_못하면_모름이다(t *testing.T) {
	dir := realTemp(t)
	poison := filepath.Join(dir, "poison")
	gitIn(t, "init", "-q", poison)
	gitIn(t, "-C", poison, "remote", "add", "origin", "git@github.com:a/poison.git")
	plain := filepath.Join(dir, "plain")
	gitIn(t, "init", "-q", plain)
	sub := filepath.Join(plain, "sub")
	os.MkdirAll(sub, 0o755)
	cases := []struct {
		name, cwd, command, how string
		want                    Project // 다음 판정(git이 답한 값)
	}{
		{"원격 조회가 128로 끝남(저장소인데)", poison, "config", "echo 'fatal: unable to read config' >&2; exit 128",
			Project{Scope: "project:github.com/a/poison", Label: "github.com/a/poison", Dir: filepath.ToSlash(poison), Git: true, Temp: true}},
		{"원격 조회가 신호로 끝남", poison, "config", "kill -9 $$",
			Project{Scope: "project:github.com/a/poison", Label: "github.com/a/poison", Dir: filepath.ToSlash(poison), Git: true, Temp: true}},
		{"rev-parse가 신호로 끝남", sub, "rev-parse", "kill -9 $$",
			Project{Scope: "project:" + filepath.ToSlash(plain), Label: filepath.ToSlash(plain), Dir: filepath.ToSlash(sub), Git: true, Temp: true}},
		{"rev-parse가 뜻밖의 코드로 끝남", sub, "rev-parse", "exit 3",
			Project{Scope: "project:" + filepath.ToSlash(plain), Label: filepath.ToSlash(plain), Dir: filepath.ToSlash(sub), Git: true, Temp: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runs := countingGit(t, failOnce(t.TempDir(), c.command, c.how))
			if p := Resolve(c.cwd); !p.Unsure {
				t.Fatalf("git이 답하지 못했는데 모름이 아니다: %+v", p)
			}
			first := runs()
			if p := Resolve(c.cwd); p != c.want {
				t.Fatalf("다시 판정:\n got %+v\nwant %+v", p, c.want)
			}
			if runs() == first {
				t.Fatal("다시 판정이 git에 묻지 않았다")
			}
		})
	}
}

// git을 찾지 못하면(실행 실패) 저장소일 수 있는 폴더의 판정은 모름이다.
func TestResolve_git을_띄우지_못하면_모름이다(t *testing.T) {
	dir := realTemp(t)
	gitIn(t, "init", "-q", dir)
	t.Setenv("PATH", t.TempDir())
	p := Resolve(dir)
	if !p.Unsure || p.Scope != "project:"+filepath.ToSlash(dir) {
		t.Fatalf("%+v", p)
	}
}

// git이 폴더를 보고 거절한 답(128: 저장소 아님, 깨진 .git 파일, 작업 트리가 아님)은 확실한 답이다: 모름이 아니다.
// 깨진 .git 파일에서는 원격 조회도 128이다(저장소를 못 찾았으니 둘이 맞는다).
func TestResolve_git의_거절은_모름이_아니다(t *testing.T) {
	dir := realTemp(t)
	broken := filepath.Join(dir, "broken")
	os.MkdirAll(broken, 0o755)
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, "head")
	os.MkdirAll(filepath.Join(head, "in"), 0o755)
	os.WriteFile(filepath.Join(head, "HEAD"), []byte("x\n"), 0o644) // HEAD라는 이름의 보통 파일
	plain := filepath.Join(dir, "plain")
	gitIn(t, "init", "-q", plain)
	gitIn(t, "init", "-q", "--bare", filepath.Join(dir, "lone.git"))
	runs := countingGit(t, "")
	for _, d := range []string{broken, filepath.Join(head, "in"), filepath.Join(plain, ".git"), filepath.Join(dir, "lone.git")} {
		p := Resolve(d)
		if want := (Project{Scope: "project:" + filepath.ToSlash(d), Label: filepath.ToSlash(d), Dir: filepath.ToSlash(d), Temp: true}); p != want {
			t.Errorf("%s:\n got %+v\nwant %+v", d, p, want)
		}
	}
	if runs() == 0 {
		t.Fatal("git을 부르지 않았다: 시험이 git의 거절을 보지 못한다")
	}
}

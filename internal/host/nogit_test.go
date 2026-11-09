package host_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
)

// countingGit puts a git in front of PATH that writes one line per run to a file and then runs the
// real git (with body before it, if any). It gives the file's line count so far (시간에 기대지 않고 git 실행 수를 센다).
func countingGit(t *testing.T, body ...string) func() int {
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
	script := "#!/bin/sh\necho run >> '" + log + "'\n" + strings.Join(body, "") + "exec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// macOS는 새로 쓴 실행 파일을 처음 돌 때 검사한다: 한 번 돌려 둔 뒤 세기 시작한다.
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

// gitRepo makes a repository in a new temporary folder (with the remote if one is given) and gives its real path.
func gitRepo(t *testing.T, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		if args[len(args)-1] == "" {
			continue
		}
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// sessionOps runs the operations of a session after its start: request recall, tool context, a subagent's
// context and a read tool call. It gives how many git runs each took.
func sessionOps(t *testing.T, a fakeAdapter, runs func() int) map[string]int {
	t.Helper()
	got := map[string]int{}
	count := func(what string, f func()) {
		before := runs()
		f()
		got[what] = runs() - before
	}
	count("recall", func() { a.recall(host.OpRecall, map[string]any{"prompt": "답은 존댓말로 해 줘"}) })
	count("tool-context", func() { a.recall(host.OpToolContext, map[string]any{"tool": "Bash"}) })
	count("subagent context", func() {
		sub := a
		sub.agent = "a-1"
		sub.recall(host.OpContext, map[string]any{"audience": "subagent", "agentKind": "worker"})
	})
	count("call", func() {
		if _, err := a.do(host.OpCall, map[string]any{"name": "memory_find", "args": json.RawMessage(`{"query":"존댓말"}`)}); err != nil {
			t.Fatalf("call: %v", err)
		}
	})
	return got
}

func noGit(t *testing.T, what string, got map[string]int) {
	t.Helper()
	for op, n := range got {
		if n != 0 {
			t.Errorf("%s: %s git 실행 %d번, 0번이어야 한다", what, op, n)
		}
	}
}

// 간헐 실패(0.17 r0): 세션의 op마다 한도(tool-context 0.5초) 안에서 git을 띄워 부하 때 문맥이 비었다.
// 저장소일 수 없는 폴더의 세션은 시작부터 끝까지 git을 부르지 않는다.
func TestNoGit_저장소_아닌_폴더의_세션은_git을_부르지_않는다(t *testing.T) {
	e := newEnv(t)
	pin(t, e.store(), memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	runs := countingGit(t)
	a := e.adapter("n-1")
	a.recall(host.OpContext, nil)
	got := sessionOps(t, a, runs)
	if _, err := a.do(host.OpSessionEnd, nil); err != nil {
		t.Fatal(err)
	}
	// 시작이 빠진 세션의 op(활동 줄 없음)도 같다
	b := e.adapter("n-2")
	for op, n := range sessionOps(t, b, runs) {
		got["시작 없는 "+op] = n
	}
	if n := runs(); n != 0 {
		t.Errorf("모두 합쳐 git 실행 %d번", n)
	}
	noGit(t, "저장소 아닌 폴더", got)
	as := activities(t, e.store())
	if len(as) != 2 || as[0].ProjectScope != "project:"+e.where() {
		t.Fatalf("활동 %+v, want project:%s", as, e.where())
	}
}

// 저장소 폴더는 세션 시작이 판정한 프로젝트(활동 줄)를 그 세션의 op가 git 없이 다시 쓴다: 같은 키·라벨로 회상하고 기록한다.
func TestNoGit_저장소_폴더는_세션_시작의_판정을_다시_쓴다(t *testing.T) {
	e := newEnv(t)
	pin(t, e.store(), memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	repo := gitRepo(t, "git@github.com:a/reuse.git")
	runs := countingGit(t)
	a := e.adapter("g-1")
	a.cwd = repo
	a.recall(host.OpContext, nil)
	if runs() == 0 {
		t.Fatal("세션 시작이 git을 부르지 않았다: 시험이 git 실행을 세지 못한다")
	}
	noGit(t, "저장소 폴더", sessionOps(t, a, runs))
	s := e.store()
	as := activities(t, s)
	if len(as) != 1 || as[0].ProjectScope != "project:github.com/a/reuse" || as[0].ProjectLabel != "github.com/a/reuse" || as[0].Prompts != 1 {
		t.Fatalf("활동 %+v", as)
	}
	for _, ev := range events(t, s) {
		if ev.Scope != "" && ev.Scope != "project:github.com/a/reuse" {
			t.Fatalf("사건 범위 %+v", ev)
		}
	}
	// 같은 세션이라도 폴더가 다르면 다시 판정한다
	sub := filepath.Join(repo, "sub")
	os.MkdirAll(sub, 0o755)
	moved := a
	moved.cwd = sub
	before := runs()
	if _, err := moved.do(host.OpCall, map[string]any{"name": "memory_find", "args": json.RawMessage(`{"query":"존댓말"}`)}); err != nil {
		t.Fatal(err)
	}
	if runs() == before {
		t.Fatal("다른 폴더인데 앞선 판정을 썼다")
	}
}

// 원격 없는 저장소의 최상위는 키가 폴더 자체라 활동 줄만으로는 git 안인지 모른다(git이 거절한 .git이 있는 폴더도 키가 폴더다).
// 회상 op는 git 없이 키만 다시 쓰고, git 안인지로 기본 범위를 고르는 call(memory_save)은 git에 다시 묻는다:
// 저장한 범위는 git이 정한 값과 같다. 임시 폴더는 늘 그 폴더에 묶이므로(S3-R4) 임시 폴더 판정을 끈다.
func TestNoGit_키가_폴더인_저장소의_call은_git에_다시_묻는다(t *testing.T) {
	roots := project.TempRoots
	project.TempRoots = func() []string { return nil }
	t.Cleanup(func() { project.TempRoots = roots })
	repo := gitRepo(t, "")
	broken, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, dir, scope string
	}{{"원격 없는 저장소", repo, "project:" + filepath.ToSlash(repo)}, {"git이 거절한 .git", broken, "product:?"}} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.store() // DB가 없으면 회상 op는 판정 전에 no-db로 끝난다
			runs := countingGit(t)
			a := e.adapter("g-2")
			a.cwd = c.dir
			a.recall(host.OpContext, nil)
			before := runs()
			a.recall(host.OpRecall, map[string]any{"prompt": "답은 존댓말로 해 줘"})
			a.recall(host.OpToolContext, map[string]any{"tool": "Bash"})
			if n := runs() - before; n != 0 {
				t.Fatalf("회상 op git 실행 %d번", n)
			}
			if as := activities(t, e.store()); len(as) != 1 || as[0].ProjectScope != "project:"+filepath.ToSlash(c.dir) || as[0].Prompts != 1 {
				t.Fatalf("활동 %+v", as)
			}
			before = runs()
			out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"fact","title":"빌드는 make로 한다","source":{"kind":"session"}}`)})
			if err != nil {
				t.Fatal(err)
			}
			if runs() == before {
				t.Fatal("call이 git에 묻지 않았다")
			}
			if !strings.Contains(mustJSON(out), `"scope":"`+c.scope+`"`) {
				t.Fatalf("저장 범위가 %s가 아니다: %s", c.scope, mustJSON(out))
			}
		})
	}
}

// J-1(0.17 r6): 세션 시작에서 git이 답하지 못하면(여기서는 첫 원격 조회가 128로 끝남) 그 판정(폴더 키)은 짐작이라
// 활동 줄에 남기지 않는다. 같은 세션의 회상 op는 git에 다시 묻고 저장소 키로 그 저장소의 기억을 읽으며, 그 뒤 활동 줄은
// 저장소 키이고 다음 op는 다시 git 없이 그 키를 쓴다. 시작만 하고 끝난 세션의 끝은 프로젝트를 판정해 줄을 채운다.
func TestNoGit_세션_시작에서_git이_실패하면_다음_op가_다시_묻는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	rule := save(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "금요일 배포 금지", Body: "주말 장애", Scope: "project:github.com/a/poison"})
	repo := gitRepo(t, "git@github.com:a/poison.git")
	marks := t.TempDir()
	runs := countingGit(t, `if [ "$3" = "config" ] && mkdir '`+marks+`/failed' 2>/dev/null; then echo 'fatal: unable to read config' >&2; exit 128; fi
`)
	a := e.adapter("p-1")
	a.cwd = repo
	a.recall(host.OpContext, nil)
	if _, err := os.Stat(filepath.Join(marks, "failed")); err != nil {
		t.Fatal("세션 시작의 원격 조회가 실패하지 않았다: 시험이 실패를 주입하지 못했다")
	}
	if as := activities(t, e.store()); len(as) != 1 || as[0].ProjectScope != "" || as[0].Cwd != repo {
		t.Fatalf("짐작한 키를 활동 줄에 남겼다: %+v", as)
	}
	before := runs()
	got := a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	if runs() == before {
		t.Fatal("회상이 git에 다시 묻지 않았다")
	}
	if !slices.Contains(got.MemoryIDs, rule.ID) {
		t.Fatalf("저장소 기억을 놓쳤다: %+v", got)
	}
	if as := activities(t, e.store()); len(as) != 1 || as[0].ProjectScope != "project:github.com/a/poison" || as[0].ProjectLabel != "github.com/a/poison" {
		t.Fatalf("활동 %+v", as)
	}
	before = runs()
	a.recall(host.OpToolContext, map[string]any{"tool": "Bash"})
	if n := runs() - before; n != 0 {
		t.Fatalf("저장소 키를 얻은 뒤 tool-context git 실행 %d번", n)
	}

	// 시작만 하고(실패) 끝난 세션: 끝이 프로젝트를 판정해 줄을 채운다.
	os.Remove(filepath.Join(marks, "failed"))
	b := e.adapter("p-2")
	b.cwd = repo
	b.recall(host.OpContext, nil)
	if _, err := b.do(host.OpSessionEnd, nil); err != nil {
		t.Fatal(err)
	}
	// git이 끝에서도 답하지 못하면 끝도 짐작을 남기지 않는다.
	countingGit(t, `if [ "$3" = "config" ]; then exit 128; fi
`)
	c := e.adapter("p-3")
	c.cwd = repo
	c.recall(host.OpContext, nil)
	if _, err := c.do(host.OpSessionEnd, nil); err != nil {
		t.Fatal(err)
	}
	ended := map[string]memory.Activity{}
	for _, x := range activities(t, e.store()) {
		ended[x.Session] = x
	}
	if x := ended["p-2"]; x.ProjectScope != "project:github.com/a/poison" || x.EndedAt == 0 {
		t.Fatalf("끝난 줄 %+v", x)
	}
	if x := ended["p-3"]; x.ProjectScope != "" || x.EndedAt == 0 || x.Cwd != repo {
		t.Fatalf("끝까지 git이 답하지 못한 줄 %+v", x)
	}
}

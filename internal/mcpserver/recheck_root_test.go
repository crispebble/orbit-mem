package mcpserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
)

// 재확인 루트(명세 C-1·E-4): memory_save가 서버가 직접 읽는 파일은 세션 폴더가 속한 git 작업 트리의 최상위 아래뿐이다. 세션 폴더는
// 호출자가 고를 수 있어(tool --cwd, host call의 cwd) 폴더만으로 읽기를 허락하면 응답(확정이냐 후보냐)으로 발췌의 있고 없음을
// 캘 수 있다. 어느 길(MCP·Tools.Call)로 불러도 같은 처리기를 지난다.

const rootLine = "deploy: ./scripts/release.sh"

// rootEnv is a temporary world: a fake home and git settings that do not read the user's.
type rootEnv struct{ base, home string }

func newRootEnv(t *testing.T) rootEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	base := realDir(t, t.TempDir())
	e := rootEnv{base: base, home: filepath.Join(base, "home")}
	if err := os.MkdirAll(e.home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", e.home)
	t.Setenv("USERPROFILE", e.home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	return e
}

func gitRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// repo makes a git repository at dir, with a remote when remote is not empty.
func (e rootEnv) repo(t *testing.T, dir, remote string) string {
	t.Helper()
	gitRun(t, "init", "-q", dir)
	if remote != "" {
		gitRun(t, "-C", dir, "remote", "add", "origin", remote)
	}
	return dir
}

// file writes rootLine into path (and the folders on the way) and gives the path.
func (e rootEnv) file(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# 배포\n"+rootLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// optionsFor are the Options an assembly point builds for a session folder: from project.Resolve.
func optionsFor(dir string) Options {
	p := project.Resolve(dir)
	return Options{Project: p.Scope, ProjectLabel: p.Label, Dir: p.Dir, Git: p.Git, Temp: p.Temp, Top: p.Top, Version: "test", Actor: memory.ActorAI}
}

// saver saves through one way in: save(args) gives the response.
type saver func(args map[string]any) SaveOut

// paths opens a store and gives the ways in for a session folder: the MCP server and Tools.Call (what the host
// call and the tool command run). Each has its own store, with the user's fact the verified one may replace, under
// the auto mode auto.
func (e rootEnv) paths(t *testing.T, dir string, auto bool) map[string]struct {
	save saver
	s    *memory.Store
	old  memory.Memory
} {
	t.Helper()
	type way = struct {
		save saver
		s    *memory.Store
		old  memory.Memory
	}
	out := map[string]way{}
	cs, s := connectWith(t, optionsFor(dir))
	s.Policy.AutoMode = auto
	out["mcp"] = way{func(args map[string]any) SaveOut {
		t.Helper()
		var r SaveOut
		res := call(t, cs, "memory_save", args, &r)
		if res.IsError || !r.Saved {
			t.Fatalf("save %v: %s", args, text(res))
		}
		return r
	}, s, userFactAt(t, s, "배포는 make deploy", "배포 명령")}
	s2, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	s2.Policy.AutoMode = auto
	tl := NewTools(s2, optionsFor(dir))
	out["tools"] = way{func(args map[string]any) SaveOut {
		t.Helper()
		in, _ := json.Marshal(args)
		got, err := tl.Call("memory_save", in, "claude", "s1")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(got)
		var r SaveOut
		if err := json.Unmarshal(raw, &r); err != nil || !r.Saved {
			t.Fatalf("save %v: %s %v", args, raw, err)
		}
		return r
	}, s2, userFactAt(t, s2, "배포는 make deploy", "배포 명령")}
	return out
}

// rechecked says whether saving a verified fact whose evidence is file, from session folder dir, made the server's
// own check hold: the new memory was confirmed at once. With the auto mode on the user's words are replaced either
// way (a stated fact does too), so only the confirmation tells; a confirmed fact has always replaced them.
func (e rootEnv) rechecked(t *testing.T, dir, file string) bool {
	t.Helper()
	var results []bool
	for name, w := range e.paths(t, dir, true) { // 재확인 여부는 자동 모드를 켠 상태의 즉시 확정으로 본다
		out := w.save(verifiedArgs("배포는 release.sh", "배포 명령", file, rootLine))
		replaced := slices.Contains(out.Superseded, w.old.ID)
		confirmed := out.Memory.State == memory.StateConfirmed
		if confirmed && !replaced {
			t.Errorf("%s: 대체 %v 확정 %v", name, replaced, confirmed)
		}
		results = append(results, confirmed)
	}
	if results[0] != results[1] {
		t.Errorf("길마다 다르다: %v", results)
	}
	return results[0]
}

func TestSave_git_밖_폴더를_작업_폴더로_주면_재확인하지_않는다(t *testing.T) {
	e := newRootEnv(t)
	repo := e.repo(t, filepath.Join(e.home, "app"), "")
	if !e.rechecked(t, repo, e.file(t, filepath.Join(repo, "Makefile"))) { // 대조군: 저장소 안은 재확인한다
		t.Fatal("저장소 안 파일을 재확인하지 못했다")
	}
	for _, c := range []struct{ name, dir string }{
		{"점 폴더(자격 증명 폴더 꼴)", filepath.Join(e.home, ".aws")},
		{"홈 바로 아래 폴더", filepath.Join(e.home, "work")},
		{"임시 폴더", filepath.Join(e.base, "x")},
		{"홈 자체", e.home},
		{"홈 위", e.base},
	} {
		file := e.file(t, filepath.Join(c.dir, "credentials"))
		if e.rechecked(t, c.dir, file) {
			t.Errorf("%s: 재확인했다", c.name)
		}
		// 폴더 안 하위 폴더에서 열어도 같다.
		if sub := filepath.Join(c.dir, "sub"); c.dir != e.home && c.dir != e.base {
			if e.rechecked(t, sub, e.file(t, filepath.Join(sub, "f"))) {
				t.Errorf("%s 하위: 재확인했다", c.name)
			}
		}
	}
}

func TestSave_저장소_하위_폴더에서_연_세션도_저장소_최상위가_루트다(t *testing.T) {
	for _, remote := range []string{"", "git@github.com:me/app.git"} {
		e := newRootEnv(t)
		repo := e.repo(t, filepath.Join(e.home, "app"), remote)
		top := e.file(t, filepath.Join(repo, "Makefile"))
		deep := e.file(t, filepath.Join(repo, "pkg", "sub", "run.sh"))
		sub := filepath.Join(repo, "pkg", "sub")
		if !e.rechecked(t, sub, top) {
			t.Errorf("원격 %q: 저장소 최상위의 파일을 재확인하지 못했다", remote)
		}
		if !e.rechecked(t, sub, deep) {
			t.Errorf("원격 %q: 하위 폴더의 파일을 재확인하지 못했다", remote)
		}
		other := e.repo(t, filepath.Join(e.home, "other"), "")
		if e.rechecked(t, sub, e.file(t, filepath.Join(other, "Makefile"))) {
			t.Errorf("원격 %q: 다른 저장소의 파일을 재확인했다", remote)
		}
		if e.rechecked(t, sub, e.file(t, filepath.Join(repo, ".env"))) {
			t.Errorf("원격 %q: 점 파일을 재확인했다", remote)
		}
	}
}

func TestSave_점_폴더_아래의_연결된_worktree_안_파일도_재확인한다(t *testing.T) {
	e := newRootEnv(t)
	main := e.repo(t, filepath.Join(e.home, "app"), "")
	gitRun(t, "-C", main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(main, ".claude", "worktrees", "x")
	gitRun(t, "-C", main, "worktree", "add", "-q", wt)
	if !e.rechecked(t, wt, e.file(t, filepath.Join(wt, "Makefile"))) {
		t.Error("worktree 안 파일을 재확인하지 못했다")
	}
	e.file(t, filepath.Join(wt, "sub", "f")) // 하위 폴더가 있어야 세션 폴더가 된다
	if !e.rechecked(t, filepath.Join(wt, "sub"), e.file(t, filepath.Join(wt, "Makefile"))) {
		t.Error("worktree 하위 폴더에서 연 세션이 재확인하지 못했다")
	}
	if e.rechecked(t, wt, e.file(t, filepath.Join(wt, ".env"))) {
		t.Error("worktree 안 점 파일을 재확인했다")
	}
	if e.rechecked(t, wt, e.file(t, filepath.Join(main, "Makefile"))) { // 주 저장소 파일은 그 worktree의 것이 아니다
		t.Error("다른 작업 트리의 파일을 재확인했다")
	}
}

func TestSave_홈_전체가_저장소이면_재확인하지_않는다(t *testing.T) {
	e := newRootEnv(t)
	e.repo(t, e.home, "")
	if e.rechecked(t, filepath.Join(e.home, "proj"), e.file(t, filepath.Join(e.home, "proj", "Makefile"))) {
		t.Error("홈 저장소 아래 폴더를 재확인했다")
	}
	if e.rechecked(t, e.home, e.file(t, filepath.Join(e.home, "notes"))) {
		t.Error("홈에서 연 세션을 재확인했다")
	}
	// 홈 아래 따로 저장소를 만들면 그 저장소는 재확인한다.
	app := e.repo(t, filepath.Join(e.home, "app"), "")
	if !e.rechecked(t, app, e.file(t, filepath.Join(app, "Makefile"))) {
		t.Error("홈 아래 저장소를 재확인하지 못했다")
	}
}

// 재확인이 안 된 까닭은 응답 어디에도 드러나지 않는다: 루트 안의 보통 파일인데 발췌가 없는 경우(기준선)와 거절되는 모든 경우의
// 응답이 같다. 호출자가 준 값의 되울림(제목·id·시각)은 빼고 상태·경고·어긋남·대체를 견준다.
func TestSave_재확인이_안_된_까닭은_응답에_드러나지_않는다(t *testing.T) {
	e := newRootEnv(t)
	repo := e.repo(t, filepath.Join(e.home, "app"), "")
	outside := filepath.Join(e.base, "elsewhere")
	good := e.file(t, filepath.Join(repo, "Makefile"))
	e.file(t, filepath.Join(repo, "sub", "x"))
	e.file(t, filepath.Join(repo, ".git", "config"))
	e.file(t, filepath.Join(repo, ".env"))
	secret := e.file(t, filepath.Join(outside, "Makefile"))
	if err := os.Symlink(secret, filepath.Join(repo, "outlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "outdir")); err != nil {
		t.Fatal(err)
	}
	aws := e.file(t, filepath.Join(e.home, ".aws", "credentials"))
	plain := e.file(t, filepath.Join(e.home, "work", "notes"))
	view := func(dir, ref, excerpt string) string {
		t.Helper()
		w := e.paths(t, dir, false)["mcp"] // 기본 상태(자동 모드 끔)의 응답
		out := w.save(verifiedArgs("배포는 release.sh", "배포 명령", ref, excerpt))
		got := map[string]any{"state": out.Memory.State, "trust": out.Memory.Trust, "warnings": out.Warnings, "rewrites": out.Rewrites,
			"conflicts": len(out.Conflicts), "oldConflict": slices.Contains(out.Conflicts, w.old.ID), "superseded": out.Superseded,
			"similar": len(out.Similar), "resolve": out.Resolve, "choices": out.Choices, "near": out.Near != ""}
		b, _ := json.Marshal(got)
		return string(b)
	}
	baseline := view(repo, good, "go vet ./... passes") // 루트 안 보통 파일, 발췌 없음
	for _, c := range []struct{ name, dir, ref, excerpt string }{
		{"밖 절대경로", repo, secret, rootLine},
		{"밖을 가리키는 파일 링크", repo, filepath.Join(repo, "outlink"), rootLine},
		{"밖을 가리키는 중간 디렉터리 링크", repo, filepath.Join(repo, "outdir", "Makefile"), rootLine},
		{"..으로 나감", repo, filepath.Join(repo, "sub", "..", "..", "elsewhere", "Makefile"), rootLine},
		{"점 폴더 아래", repo, filepath.Join(repo, ".git", "config"), rootLine},
		{"점 파일", repo, filepath.Join(repo, ".env"), rootLine},
		{"없는 파일", repo, filepath.Join(repo, "none"), rootLine},
		{"상대경로", repo, "Makefile", rootLine},
		{"서버가 읽어 확인한 근거(자동 모드 끔이라 사용자 말 사실과 어긋남으로 같다)", repo, good, rootLine},
		{"디렉터리", repo, filepath.Join(repo, "sub"), rootLine},
		{"git 밖 점 폴더를 작업 폴더로", filepath.Join(e.home, ".aws"), aws, rootLine},
		{"git 밖 폴더를 작업 폴더로", filepath.Join(e.home, "work"), plain, rootLine},
		{"작업 폴더를 모름", "", good, rootLine},
	} {
		if got := view(c.dir, c.ref, c.excerpt); got != baseline {
			t.Errorf("%s: 응답이 기준선과 다르다\n got %s\nwant %s", c.name, got, baseline)
		}
	}
	// 기준선이 비어 있지 않은 응답인지(견줄 것이 있는지) 본다.
	if !strings.Contains(baseline, `"state":"candidate"`) || !strings.Contains(baseline, `"oldConflict":true`) {
		t.Errorf("기준선 %s", baseline)
	}
}

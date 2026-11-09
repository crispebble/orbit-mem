// Package project names the project of a working directory: its git remote
// when there is one, otherwise the repository root or the directory itself.
package project

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Project identifies the project of a working directory. Scope is the memory
// scope ("project:<key>"); Label is what a person reads. Dir is the working
// directory itself as a real slash path (the session folder, S3 명세 2.2b), Git
// says it is inside a git checkout, and Temp that it is under a temporary folder.
//
// Unsure says git was asked and gave no answer to go by (resolveGit): the key is a guess, and a
// caller does not keep it for later (a session's next operation asks git again).
type Project struct {
	Scope, Label string
	Dir          string
	Git, Temp    bool
	Unsure       bool
}

// realSlash is dir with its symbolic links resolved, as a clean slash path.
func realSlash(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.ToSlash(filepath.Clean(dir))
}

// TempRoots gives the temporary folders of this machine (S3-R4): /tmp, /private/tmp and $TMPDIR, also
// with their symbolic links resolved. Tests that work in a temporary home replace it.
var TempRoots = tempRoots

func tempRoots() []string {
	roots := []string{"/tmp", "/private/tmp", filepath.ToSlash(filepath.Clean(os.TempDir()))}
	for _, r := range roots[:] {
		roots = append(roots, realSlash(filepath.FromSlash(r)))
	}
	return roots
}

// IsTemp says whether a slash path is a temporary folder or under one.
func IsTemp(dir string) bool {
	dir = filepath.ToSlash(filepath.Clean(dir))
	for _, r := range TempRoots() {
		if r == "" || r == "/" || r == "." {
			continue
		}
		if dir == r || strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}

// NormalizeRemote turns the many spellings of a git remote into host/owner/repo.
func NormalizeRemote(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if at := strings.LastIndex(strings.SplitN(u, "/", 2)[0], "@"); at >= 0 {
			u = u[at+1:]
		}
	} else if at := strings.Index(u, "@"); at >= 0 && strings.Contains(u, ":") {
		u = strings.Replace(u[at+1:], ":", "/", 1)
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.ToLower(u)
}

// gitTimeout bounds one git run. Tests that make a git wait for another raise it.
var gitTimeout = 2 * time.Second

// gitRun runs git in dir. code is how git ended: 0 it answered, another number it ended with that
// exit code, -1 it gave no answer (it could not be started, ran out of time or was killed).
func gitRun(dir string, args ...string) (out string, code int) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(string(b)), 0
	case ctx.Err() == nil && errors.As(err, &exit):
		return "", exit.ExitCode() // 신호로 끝났으면 -1이다
	default:
		return "", -1
	}
}

// gitRefused is the exit code of git's die(): git looked at the folder and turned it down (not a
// git repository, an invalid gitfile, not a work tree, dubious ownership). It is git's answer: the
// next run gives the same. What else git may die of (a file it cannot read now) ends the same way
// and is not told apart: its message is translated, so it is not read.
const gitRefused = 128

// gitEnv are the environment variables that let git find a repository or a remote.origin.url
// without a .git or a HEAD above the folder (git(1) ENVIRONMENT, git-config(1) ENVIRONMENT). With one
// of them set the folder's project is always asked of git. GIT_CEILING_DIRECTORIES and
// GIT_DISCOVERY_ACROSS_FILESYSTEM are not here: they only narrow or widen the search for those entries.
var gitEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR",
	"GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"}

func gitEnvSet() bool {
	for _, k := range gitEnv {
		if _, ok := os.LookupEnv(k); ok {
			return true
		}
	}
	return false
}

// mayBeRepo says whether git could find a repository from a folder: one of the paths or a folder
// above it has a .git (a folder or a gitdir file) or a HEAD (a bare repository or a git folder),
// or an entry there cannot be looked at. Both the path as given and its real path are walked: git
// searches from the real one, and a link may also sit on the way.
func mayBeRepo(paths ...string) bool {
	for _, start := range paths {
		for d := start; ; {
			for _, name := range []string{".git", "HEAD"} {
				if _, err := os.Lstat(filepath.Join(d, name)); err == nil || !errors.Is(err, fs.ErrNotExist) {
					return true
				}
			}
			up := filepath.Dir(d)
			if up == d {
				break
			}
			d = up
		}
	}
	return false
}

// base is what Resolve knows without git: the project of an empty, missing or never-git folder
// (done), else the folder as an absolute path and the project with its folder fields.
func base(cwd string) (p Project, abs string, done bool) {
	if cwd == "" {
		return Project{}, "", true
	}
	if a, err := filepath.Abs(cwd); err == nil {
		cwd = a
	}
	dir := realSlash(cwd)
	p = Project{Dir: dir, Temp: IsTemp(dir)}
	// 없는 폴더에서는 git -C가 늘 실패하고, .git도 HEAD도 없는 폴더는 git이 저장소를 찾지 못한다: 부르지 않고
	// 그 경로를 키로 쓴다(git이 저장소가 아니라고 할 때와 같은 값, 훅 한도 안 시간을 아낀다). 환경이 git 위치를 바꾸면 묻는다.
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() || (!gitEnvSet() && !mayBeRepo(cwd, filepath.FromSlash(dir))) {
		p.Scope, p.Label = "project:"+dir, dir
		return p, cwd, true
	}
	return p, cwd, false
}

// Resolve finds the project of cwd. An empty cwd has no project.
func Resolve(cwd string) Project {
	p, cwd, done := base(cwd)
	if done {
		return p
	}
	return resolveGit(p, cwd)
}

// resolveGit asks git for the project of cwd, an existing absolute folder git may see as a repository.
// The remote and the repository questions go to git at once: the time is the longer of the two, not
// their sum (a hook's time limit holds the first resolving of a session's folder).
//
// A question git gave no answer to makes the project Unsure (J-1: a session start's failed git was kept
// for the whole session). Answers are, by exit code: for the remote (git-config --get) 0 (the remote)
// and 1 (no such key), for the repository (rev-parse) 0, and gitRefused for both. The remote question
// refused while rev-parse finds a repository is no answer either: git found the repository but did not
// read its config. Anything else (no start, a timeout, a signal, another code) is no answer.
func resolveGit(p Project, cwd string) Project {
	type answer struct {
		out  string
		code int
	}
	asked := make(chan answer, 1)
	go func() {
		out, code := gitRun(cwd, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--show-toplevel")
		asked <- answer{out, code}
	}()
	remote, remoteCode := gitRun(cwd, "config", "--get", "remote.origin.url")
	repo := <-asked
	if remoteCode == 0 && remote != "" {
		key := NormalizeRemote(remote)
		p.Scope, p.Label, p.Git = "project:"+key, key, true
		return p
	}
	p.Unsure = (remoteCode != 0 && remoteCode != 1 && remoteCode != gitRefused) ||
		(repo.code != 0 && repo.code != gitRefused) || (remoteCode == gitRefused && repo.code == 0)
	// 원격이 없는 저장소는 그 checkout의 최상위가 키다. 연결된 worktree(git 디렉터리와 공용 디렉터리가 다름)만
	// 주 저장소 경로를 써서 같은 키를 쓴다(S3-R4). 서브모듈·--separate-git-dir은 최상위 그대로다. git 한 번에 묻는다(위에서 띄운 것).
	// --path-format을 모르는 git(2.31 전)은 그 옵션을 글 그대로 되돌리고 0으로 끝난다(줄 수가 어긋난다): 그때만
	// --show-toplevel로 다시 묻는다. 거절(저장소 아님 등)은 다시 물어도 같아 묻지 않는다.
	root := ""
	if repo.code == 0 {
		if l := strings.Split(repo.out, "\n"); len(l) == 3 {
			gitDir, common, top := l[0], l[1], l[2]
			p.Git, root = true, top
			if gitDir != common && filepath.Base(common) == ".git" {
				root = filepath.Dir(common)
			}
		} else if top, code := gitRun(cwd, "rev-parse", "--show-toplevel"); code == 0 && top != "" { // git 2.31 전
			p.Git, root = true, top
		} else if code != 0 && code != gitRefused {
			p.Unsure = true
		}
	}
	// 홈 자체가 저장소(dotfiles)면 홈 아래 폴더는 git 밖 폴더로 본다(S3-R1).
	if home, err := os.UserHomeDir(); err == nil && root != "" && realSlash(root) == realSlash(home) {
		p.Git, root = false, ""
	}
	if root == "" {
		root = cwd
	}
	root = realSlash(root)
	p.Scope, p.Label = "project:"+root, root
	return p
}

// Earlier is what an earlier Resolve of a folder gave in the same session: the folder as it was
// asked (Cwd) and the project's Scope and Label.
type Earlier struct{ Cwd, Scope, Label string }

// Again is Resolve for a folder a session resolved before. When earlier (asked only for a folder
// git may see as a repository) is of the same absolute folder and has a project key, it is used
// as it is, without git; otherwise the folder is resolved with git. A folder that cannot be a
// repository is a folder key as Resolve gives, whatever earlier says.
//
// gitKnown is false only when the project came from earlier and Git could not be told from it: a
// key that is the folder itself is both a repository's top without a remote and a folder git
// refused. Git is then a guess (the folder has a .git and is not the home folder); a caller that
// depends on Git resolves again.
func Again(cwd string, earlier func() (Earlier, bool)) (p Project, gitKnown bool) {
	p, abs, done := base(cwd)
	if done {
		return p, true
	}
	// 상대 경로는 프로세스 폴더마다 뜻이 다르고, git 위치·설정 환경은 프로세스마다 다를 수 있다: 다시 쓰지 않는다.
	if gitEnvSet() || !filepath.IsAbs(cwd) {
		return resolveGit(p, abs), true
	}
	e, ok := earlier()
	if !ok || e.Cwd != cwd || e.Label == "" || e.Scope != "project:"+e.Label {
		return resolveGit(p, abs), true
	}
	p.Scope, p.Label = e.Scope, e.Label
	// 폴더 밖 판정(git 밖, 홈 아래, git이 거절)의 키는 늘 그 폴더다: 키가 폴더와 다르면 저장소다.
	if p.Label != p.Dir {
		p.Git = true
		return p, true
	}
	_, err := os.Lstat(filepath.Join(abs, ".git"))
	home, herr := os.UserHomeDir()
	p.Git = err == nil && (herr != nil || realSlash(home) != p.Dir)
	return p, false
}

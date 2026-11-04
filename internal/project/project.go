// Package project names the project of a working directory: its git remote
// when there is one, otherwise the repository root or the directory itself.
package project

import (
	"context"
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
type Project struct {
	Scope, Label string
	Dir          string
	Git, Temp    bool
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

func git(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Resolve finds the project of cwd. An empty cwd has no project.
func Resolve(cwd string) Project {
	if cwd == "" {
		return Project{}
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	dir := realSlash(cwd)
	p := Project{Dir: dir, Temp: IsTemp(dir)}
	if remote := git(cwd, "config", "--get", "remote.origin.url"); remote != "" {
		key := NormalizeRemote(remote)
		p.Scope, p.Label, p.Git = "project:"+key, key, true
		return p
	}
	// 원격이 없는 저장소는 그 checkout의 최상위가 키다. 연결된 worktree(git 디렉터리와 공용 디렉터리가 다름)만
	// 주 저장소 경로를 써서 같은 키를 쓴다(S3-R4). 서브모듈·--separate-git-dir은 최상위 그대로다. git 한 번에 묻는다.
	root := ""
	if out := git(cwd, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--show-toplevel"); out != "" {
		if l := strings.Split(out, "\n"); len(l) == 3 {
			gitDir, common, top := l[0], l[1], l[2]
			p.Git, root = true, top
			if gitDir != common && filepath.Base(common) == ".git" {
				root = filepath.Dir(common)
			}
		}
	} else if top := git(cwd, "rev-parse", "--show-toplevel"); top != "" { // git 2.31 전
		p.Git, root = true, top
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

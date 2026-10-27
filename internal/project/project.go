// Package project names the project of a working directory: its git remote
// when there is one, otherwise the repository root or the directory itself.
package project

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Project identifies the project of a working directory. Scope is the memory
// scope ("project:<key>"); Label is what a person reads.
type Project struct{ Scope, Label string }

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
	if remote := git(cwd, "config", "--get", "remote.origin.url"); remote != "" {
		key := NormalizeRemote(remote)
		return Project{Scope: "project:" + key, Label: key}
	}
	root := git(cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		root = cwd
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	root = filepath.ToSlash(filepath.Clean(root))
	return Project{Scope: "project:" + root, Label: root}
}

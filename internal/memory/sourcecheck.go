package memory

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Outcomes of checking a memory whose source file changed (설계 · Orbit 3차
// O3-1 「출처 파일 판정」). The retrospectives wrote them as source.check events
// (removed 2026-11-08); orbit-mem stats still counts the old events.
const (
	SourceKept   = "kept"   // 발췌가 바뀐 파일에 그대로 있음: 해시만 갱신
	SourceAbsent = "absent" // 파일이 없어짐: 근거가 지워졌을 뿐이라 제안하지 않음(2026-10-30 사용자 결정)
	SourceGone   = "gone"   // 발췌가 파일에서 사라짐: 낡음 제안
	SourceOK     = "ok"     // 발췌 없음, 회고 모델 판정: 아직 맞음
	SourceEdit   = "edit"   // 회고 모델 판정: 고쳐야 함(edit 제안)
	SourceStale  = "stale"  // 회고 모델 판정: 낡음(낡음 제안)
)

// Limits of RecheckExcerpt.
const (
	// recheckMaxBytes is the largest file it reads: a verification ground is a source file, a config or a
	// log excerpt, not a database or a build output. A larger file is not read at all (false).
	recheckMaxBytes = 1 << 20
	// recheckMinRunes is the shortest excerpt (spaces collapsed) it looks for: a few letters ("ok", "all:")
	// are in almost any file and prove nothing.
	recheckMinRunes = 10
)

var (
	// recheckTimeout bounds the whole read: the save waits no longer. A variable so a test can shorten it.
	recheckTimeout = time.Second
	// recheckOpen opens the file (openSource: where the system can, a link as the last element is not followed).
	// A variable so a test can make the read hang or swap the file.
	recheckOpen = openSource
)

// RecheckExcerpt says whether the server, reading the file itself at this moment, finds excerpt in it: the check
// that makes a verification source a ground the server confirmed (Source.Rechecked), so that a verified fact may
// be confirmed at once (E-4) and replace the user's words on its own (C-1). It never fails a save: anything it
// cannot check is false, and the source is then a ground only written by the AI.
//
// It reads only a file inside one of roots, which are the top folders of git work trees (mcpserver gives the
// top of the work tree the session folder is in; without one it reads nothing). A root that is not absolute, has
// no .git of its own, or is the file system's root, the home folder or a folder above it is not one: a session
// opened there would make any file of the person a ground. Home and above are told by file identity (os.SameFile),
// not by the spelling of the path, so a file system that ignores case or a link to the home folder does not pass
// as another folder; with no home folder known there is no root. The server may read files the host was told
// not to read, so a save must not become a way to probe them: what its answer (confirmed or candidate) can tell
// is held to the work's files.
//
// path must be absolute (a relative path is not resolved against any folder). It is cleaned and its symbolic
// links resolved, and the real path must lie under a root (its links resolved too), so a link or a ".." that
// leads out is false. Folders above the root are not looked at (a linked worktree may sit under a dot folder),
// but no folder or file below the root on the way may start with a dot (.git, .env, .ssh). The file must be a
// regular file of at most recheckMaxBytes, checked again on the open file; a directory, a device, a FIFO or a
// socket is false without opening it. The open does not follow a link in the last element (where the system has
// O_NOFOLLOW) and the opened file must be the very file the checked path named (os.SameFile), so a path swapped
// for a link or another file after the check is false. A folder on the way swapped between the check and the open
// is not caught: the folders are not held open, which stays a limit (the swap needs write access to the
// work tree, and what it can reach is still only what the person's own process may read). A missing file, one it
// may not read, an excerpt shorter than recheckMinRunes, or one not in the file is false. Runs of white space
// count as one space on both sides. The whole check gives up after recheckTimeout and is false; a step stuck in
// the system then ends on its own later.
//
// The answer is only true or false and why a path was not taken is told to no one, so the reasons cannot be
// told apart to learn what exists. What it reads is only compared and dropped: the file's content is not
// stored, logged or returned.
func RecheckExcerpt(roots []string, path, excerpt string) bool {
	path = strings.TrimSpace(path)
	want := strings.Join(strings.Fields(excerpt), " ")
	if !filepath.IsAbs(path) || utf8.RuneCountInString(want) < recheckMinRunes || len(roots) == 0 {
		return false
	}
	open, limit := recheckOpen, recheckTimeout
	done := make(chan bool, 1) // 한도가 지나 아무도 받지 않아도 확인 고루틴이 멈추지 않게 한 칸을 둔다
	go func() {
		real, checked := insideRoots(roots, path)
		done <- real != "" && fileHolds(open, real, checked, want)
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(limit):
		return false
	}
}

// insideRoots is path cleaned with its links resolved when that real path is a regular file of at most
// recheckMaxBytes under one of roots (their links resolved) with no dot-named folder or file below the root,
// else "". It also gives the file's Lstat, which the open file must match. Roots that may not be one (see
// RecheckExcerpt) are skipped.
func insideRoots(roots []string, path string) (string, os.FileInfo) {
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", nil
	}
	file, err := os.Lstat(real)
	if err != nil || !file.Mode().IsRegular() || file.Size() > recheckMaxBytes {
		return "", nil
	}
	homes := homeFolders()
	if len(homes) == 0 {
		return "", nil // 홈을 모르면 홈 규칙을 지킬 수 없다
	}
	for _, root := range roots {
		root = filepath.FromSlash(strings.TrimSpace(root))
		if !filepath.IsAbs(root) {
			continue
		}
		r, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil || !isWorktreeTop(r) || atOrAboveHome(r, homes) {
			continue // 작업 트리 최상위가 아니거나, 파일 시스템 뿌리·홈·홈 위
		}
		rel := below(r, real)
		if rel == "" {
			continue
		}
		dotted := false
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				dotted = true
				break
			}
		}
		if !dotted {
			return real, file
		}
	}
	return "", nil
}

// homeFolders are the home folder as the environment names it and with its links resolved, or none when it is
// not known.
func homeFolders() []string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" || !filepath.IsAbs(h) {
		return nil
	}
	homes := []string{filepath.Clean(h)}
	if r, err := filepath.EvalSymlinks(h); err == nil && r != homes[0] {
		homes = append(homes, r)
	}
	return homes
}

// atOrAboveHome says whether root is a home folder or a folder above one, or the file system's root, by file
// identity: every folder from a home up to the root is compared with root by os.SameFile, so the spelling of the
// path (case on a file system that ignores it, a link) cannot make another name of it pass. A root that cannot be
// looked at counts as one; a folder of the walk that does not exist (a home that is not there) is skipped.
func atOrAboveHome(root string, homes []string) bool {
	ri, err := os.Stat(root)
	if err != nil {
		return true
	}
	for _, h := range homes {
		for d := h; ; d = filepath.Dir(d) {
			if di, err := os.Stat(d); err == nil && os.SameFile(di, ri) {
				return true
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return false
}

// isWorktreeTop says whether dir has a .git of its own (a folder, or the file of a linked worktree or a
// submodule): the top folder of a git work tree. The caller already took git's word for it; this keeps a root
// that is only some folder from being read.
func isWorktreeTop(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// below is p relative to dir when p lies strictly under dir, else "".
func below(dir, p string) string {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}
	return rel
}

// fileHolds reads path (a regular file of at most recheckMaxBytes, checked again on the open file) and says
// whether want is in it, white space collapsed. The open file must be the file checked before the open
// (checked, the Lstat of the real path): a path swapped for another file in between is false.
func fileHolds(open func(string) (*os.File, error), path string, checked os.FileInfo, want string) bool {
	f, err := open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > recheckMaxBytes || !os.SameFile(info, checked) {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(f, recheckMaxBytes+1))
	if err != nil || len(b) > recheckMaxBytes {
		return false
	}
	return strings.Contains(strings.Join(strings.Fields(string(b)), " "), want)
}

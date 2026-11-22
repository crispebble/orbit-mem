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
	// recheckOpen opens the file. A variable so a test can make the read hang.
	recheckOpen = os.Open
)

// RecheckExcerpt says whether the server, reading the file itself at this moment, finds excerpt in it: the check
// that makes a verification source a ground the server confirmed (Source.Rechecked), so that a verified fact may
// be confirmed at once (E-4) and replace the user's words on its own (C-1). It never fails a save: anything it
// cannot check is false, and the source is then a ground only written by the AI.
//
// It reads only path that is absolute (a relative path is not resolved against any folder) and is a regular
// file of at most recheckMaxBytes, by its last element: a symbolic link, a directory, a device, a FIFO or a
// socket is false without opening it, and so is a file that turned into something else when opened. A missing
// file, one it may not read, an excerpt shorter than recheckMinRunes, or one not in the file is false. Runs of
// white space count as one space on both sides. The read gives up after recheckTimeout and is false; a read
// stuck in the system then ends on its own later.
//
// What it reads is only compared and dropped: the file's content is not stored, logged or returned.
func RecheckExcerpt(path, excerpt string) bool {
	path = strings.TrimSpace(path)
	want := strings.Join(strings.Fields(excerpt), " ")
	if !filepath.IsAbs(path) || utf8.RuneCountInString(want) < recheckMinRunes {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > recheckMaxBytes {
		return false
	}
	open, limit := recheckOpen, recheckTimeout
	done := make(chan bool, 1) // 한도가 지나 아무도 받지 않아도 읽기 고루틴이 멈추지 않게 한 칸을 둔다
	go func() { done <- fileHolds(open, path, want) }()
	select {
	case ok := <-done:
		return ok
	case <-time.After(limit):
		return false
	}
}

// fileHolds reads path (a regular file of at most recheckMaxBytes, checked again on the open file) and says
// whether want is in it, white space collapsed.
func fileHolds(open func(string) (*os.File, error), path, want string) bool {
	f, err := open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() || info.Size() > recheckMaxBytes {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(f, recheckMaxBytes+1))
	if err != nil || len(b) > recheckMaxBytes {
		return false
	}
	return strings.Contains(strings.Join(strings.Fields(string(b)), " "), want)
}

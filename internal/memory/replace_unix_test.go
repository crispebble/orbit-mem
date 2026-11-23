//go:build unix

package memory

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// recheckSpecialFiles checks in dir that RecheckExcerpt does not open a FIFO (opening one would wait for a
// writer) and is false for a file it may not read.
func recheckSpecialFiles(t *testing.T, dir string) {
	t.Helper()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if RecheckExcerpt([]string{dir}, fifo, "go build ./...") || time.Since(start) > time.Second {
		t.Errorf("FIFO: true 또는 %v 걸림", time.Since(start))
	}
	if os.Getuid() == 0 { // 관리자는 권한과 관계없이 읽는다
		return
	}
	locked := filepath.Join(dir, "locked")
	if err := os.WriteFile(locked, []byte("go build ./... ok\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if RecheckExcerpt([]string{dir}, locked, "go build ./... ok") {
		t.Error("읽을 권한 없는 파일: true")
	}
}

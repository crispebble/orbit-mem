// Package install puts orbit-mem on this PC: the binary and orbit-mem's own plugin in the
// hosts (hosts.go: Claude Code and Codex plugin commands, Codex's config.toml). It schedules
// nothing.
package install

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// Hosts are the host CLIs found on this PC. An empty path means not installed.
type Hosts struct {
	Claude, Codex string
	Run           func(name string, args ...string) (string, error)
}

// RunCommand runs a command and returns its combined output.
func RunCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = nil
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// FindHosts looks up claude and codex on PATH.
func FindHosts() Hosts {
	h := Hosts{Run: RunCommand}
	if p, err := exec.LookPath("claude"); err == nil {
		h.Claude = p
	}
	if p, err := exec.LookPath("codex"); err == nil {
		h.Codex = p
	}
	return h
}

// DefaultBinDir is where orbit-mem is installed: ~/.local/bin on Mac and Linux,
// %LOCALAPPDATA%\Programs\orbit on Windows.
func DefaultBinDir() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Programs", "orbit")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// BinaryName is orbit-mem's file name on this OS.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "orbit-mem.exe"
	}
	return "orbit-mem"
}

// CopyBinary installs the running orbit-mem at dst (atomically) unless it already is dst.
func CopyBinary(src, dst string) (bool, error) {
	if a, err := filepath.EvalSymlinks(src); err == nil {
		src = a
	}
	if b, err := filepath.EvalSymlinks(dst); err == nil && b == src {
		return false, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		old := dst + ".old"
		_ = os.Remove(old) // 지난번에 남은 것(실행 중이면 실패해도 된다)
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, old); err != nil { // 실행 중인 exe는 지울 수 없어 이름만 바꾼다
				os.Remove(tmp)
				return false, i18n.Errorf("install.err.replace_binary", err)
			}
			if err := os.Rename(tmp, dst); err != nil {
				_ = os.Rename(old, dst)
				return false, err
			}
			return true, nil
		}
	}
	return true, os.Rename(tmp, dst)
}

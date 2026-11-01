package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/install"
	"github.com/onidot-labs/orbit-mem/internal/version"
)

// 명세 · ONID-107: orb install은 orb를 깔고, 4차 동안 같은 바이너리를 가리키는 oni 별칭을 둔다.
// 지난 설치가 남긴 옛 oni는 별칭으로 바뀌고, 플러그인 훅·MCP는 orb 경로를 쓰며, oni로 불러도 그대로 돈다.
func TestInstall_orb를_깔고_oni_별칭을_둔다(t *testing.T) {
	h := home(t)
	tmp := t.TempDir()
	built := filepath.Join(tmp, "build", install.BinaryName())
	if out, err := exec.Command("go", "build", "-o", built, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldOni := filepath.Join(binDir, install.AliasName())
	if err := os.WriteFile(oldOni, []byte("#!/bin/sh\necho old-oni\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 플러그인 빌드는 openorbit-harness가 한다. 가짜 하네스는 받은 조각을 플러그인 폴더에 둔다.
	env := append(os.Environ(), "ORBIT_HOME="+h, "HOME="+filepath.Join(tmp, "home"), "ORBIT_LANG=",
		"ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI="+fakeTS(t), "FAKE_SOURCE="+filepath.Join(tmp, "src"))
	cmd := exec.Command(built, "install", "--bin-dir", binDir, "--no-hosts", "--no-schedule")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	orb := filepath.Join(binDir, install.BinaryName())
	so, err := os.Stat(orb)
	if err != nil {
		t.Fatalf("orb not installed: %v", err)
	}
	sa, err := os.Stat(oldOni)
	if err != nil {
		t.Fatalf("oni alias missing: %v", err)
	}
	if runtime.GOOS != "windows" && !os.SameFile(so, sa) {
		t.Fatal("oni alias does not point at orb")
	}
	// 플러그인 훅과 MCP는 orb 경로를 쓴다(별칭 경로가 아니다).
	var orbRefs, oniRefs int
	_ = filepath.WalkDir(filepath.Join(h, "plugin"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		s := strings.ReplaceAll(string(b), `\\`, `\`)
		s = strings.ReplaceAll(s, `\`, "/")
		orbRefs += strings.Count(s, filepath.ToSlash(orb))
		oniRefs += strings.Count(s, filepath.ToSlash(oldOni))
		return nil
	})
	if orbRefs == 0 || oniRefs != 0 {
		t.Fatalf("plugin refs orb=%d oni=%d", orbRefs, oniRefs)
	}
	// oni로 불러도 orb와 같이 돈다.
	for _, bin := range []string{orb, oldOni} {
		c := exec.Command(bin, "version")
		c.Env = env
		out, err := c.Output()
		if err != nil || strings.TrimSpace(string(out)) != "orb "+version.Version {
			t.Fatalf("%s version: %q %v", filepath.Base(bin), out, err)
		}
	}
	c := exec.Command(oldOni, "config", "show")
	c.Env = env
	if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), h) {
		t.Fatalf("oni config show: %v %s", err, out)
	}
}

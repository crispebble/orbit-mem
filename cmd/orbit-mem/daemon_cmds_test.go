package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orb daemon … 은 orbit-pi 호스트 CLI(packages/orbit-host/src/cli.ts)로 넘긴다(계획 0s39bc5qte308 H10).
func TestOrbitPiHostCommand_호스트_CLI로_넘긴다(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "packages", "orbit-host", "src", "cli.ts")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l := orbitPiLaunch{home: "/h", orb: "/bin/orb", roles: "/h/runtime/roles", base: []string{"PATH=/usr/bin"},
		getenv: func(k string) string { return map[string]string{"ORBIT_PI_DIR": dir}[k] }, installed: func() bool { return false }}
	argv, env, err := l.commandFor(hostCLI, "status", []string{"--json"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, "|") != strings.Join([]string{"node", cli, "status", "--json"}, "|") {
		t.Errorf("argv %q", argv)
	}
	if m := envOf(env); m["ORBIT_HOME"] != "/h" || m["ORBIT_ORB"] != "/bin/orb" {
		t.Errorf("env %v", env)
	}
	// 데몬 갈래가 없는 옛 orbit-pi면 업데이트를 안내한다.
	l.getenv = func(k string) string { return map[string]string{"ORBIT_PI_DIR": fakeOrbitPi(t)}[k] }
	if _, _, err := l.commandFor(hostCLI, "status", nil); err == nil || !strings.Contains(err.Error(), "orb runtime") {
		t.Errorf("err %v", err)
	}
}

func TestDaemonPathDirs_node와_엔진과_orb가_PATH에(t *testing.T) {
	dirs := daemonPathDirs("/opt/homebrew/bin/node", "/Users/me/.local/bin/orb", []string{"/Users/me/.local/bin/claude", "", "/opt/homebrew/bin/codex"})
	if strings.Join(dirs, ":") != "/opt/homebrew/bin:/Users/me/.local/bin" {
		t.Errorf("dirs %q", dirs)
	}
}

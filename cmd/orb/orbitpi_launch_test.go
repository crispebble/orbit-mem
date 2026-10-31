package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeOrbitPi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cli := filepath.Join(dir, "packages", "orbit-orchestra", "src", "cli.ts")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("// cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func envOf(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestOrbitPiCommand_ORBIT_PI_DIR가_앞선다(t *testing.T) {
	dir := fakeOrbitPi(t)
	l := orbitPiLaunch{home: "/h", orb: "/bin/orb", roles: "/h/runtime/roles", base: []string{"PATH=/usr/bin", "ORBIT_HOME=/old"},
		getenv: func(k string) string { return map[string]string{"ORBIT_PI_DIR": dir}[k] }, installed: func() bool { return false }}
	argv, env, err := l.command("run", []string{"--plan", "x y"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node", filepath.Join(dir, "packages", "orbit-orchestra", "src", "cli.ts"), "run", "--plan", "x y"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Errorf("argv %q", argv)
	}
	m := envOf(env)
	if m["ORBIT_HOME"] != "/h" || m["ORBIT_ORB"] != "/bin/orb" || m["ORBIT_ROLES_DIR"] != "/h/runtime/roles" || m["PATH"] != "/usr/bin" {
		t.Errorf("env %v", env)
	}
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "ORBIT_HOME=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("ORBIT_HOME이 %d번 있습니다", n)
	}
}

func TestOrbitPiCommand_설치한_런타임을_쓴다(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "runtime", "orbit-pi")
	cli := filepath.Join(dir, "packages", "orbit-orchestra", "src", "cli.ts")
	_ = os.MkdirAll(filepath.Dir(cli), 0o755)
	_ = os.WriteFile(cli, nil, 0o644)
	l := orbitPiLaunch{home: home, orb: "/bin/orb", roles: "/r", getenv: func(string) string { return "" }, installed: func() bool { return true }}
	argv, _, err := l.command("profile", nil)
	if err != nil || argv[1] != cli || argv[2] != "profile" || len(argv) != 3 {
		t.Errorf("argv %q, %v", argv, err)
	}
}

func TestOrbitPiCommand_없으면_설치를_안내한다(t *testing.T) {
	home := t.TempDir()
	for name, l := range map[string]orbitPiLaunch{
		"설치 안 됨":    {home: home, getenv: func(string) string { return "" }, installed: func() bool { return false }},
		"cli.ts 없음": {home: home, getenv: func(k string) string { return map[string]string{"ORBIT_PI_DIR": home}[k] }, installed: func() bool { return true }},
	} {
		_, _, err := l.command("tui", nil)
		if err == nil || !strings.Contains(err.Error(), "orb runtime install") || !strings.Contains(err.Error(), "ORBIT_PI_DIR") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOrb_인자가_없으면_TTY일_때만_TUI(t *testing.T) {
	if zeroArgTUI(false, true) || zeroArgTUI(true, false) || !zeroArgTUI(true, true) {
		t.Error("TTY 판정")
	}
	home(t)
	if _, errOut, code := run(t, ""); code != 2 || !strings.Contains(errOut, "orb run") {
		t.Errorf("TTY가 아닐 때 인자 없는 orb: %d %q", code, errOut)
	}
	if _, _, code := run(t, "", "--help"); code != 0 {
		t.Errorf("--help: %d", code)
	}
}

func TestOrb_run은_런타임이_없으면_실패한다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_PI_DIR", "")
	_, errOut, code := run(t, "", "run", "일")
	if code != 1 || !strings.Contains(errOut, "orb runtime install") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestOrb_TTY에서_런타임이_없으면_사용법과_설치_안내(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_PI_DIR", "")
	var out, errOut strings.Builder
	a := &app{stdin: strings.NewReader(""), stdout: &out, stderr: &errOut}
	code := a.zeroArg()
	if code != 2 || !strings.Contains(errOut.String(), "orb run") || !strings.Contains(errOut.String(), "orb runtime install") {
		t.Errorf("%d %q", code, errOut.String())
	}
}

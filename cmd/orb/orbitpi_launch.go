package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/orbitpi"
	"github.com/onidot-labs/openorbit/internal/orbitplugin"
)

// orbitPiLaunch builds the command that hands the terminal to orbit-pi's
// orchestrator (설계 · OpenOrbit 4차 R4-5): orb run, orb profile and orb with no
// arguments on a terminal.
type orbitPiLaunch struct {
	home, orb, roles string
	base             []string // 물려줄 환경
	getenv           func(string) string
	installed        func() bool
}

// orchestraCLI is orbit-pi's orchestrator entry inside its checkout.
var orchestraCLI = filepath.Join("packages", "orbit-orchestra", "src", "cli.ts")

// command is node <orbit-pi>/packages/orbit-orchestra/src/cli.ts <sub> <args…>
// with ORBIT_HOME, ORBIT_ORB and ORBIT_ROLES_DIR set. orbit-pi is ORBIT_PI_DIR
// when set, otherwise the checkout orb runtime install made.
func (l orbitPiLaunch) command(sub string, args []string) ([]string, []string, error) {
	dir := l.getenv("ORBIT_PI_DIR")
	if dir == "" {
		if !l.installed() {
			return nil, nil, i18n.Errorf("cli.run.no_runtime", orbitpi.Dir(l.home))
		}
		dir = orbitpi.Dir(l.home)
	}
	cli := filepath.Join(dir, orchestraCLI)
	if fi, err := os.Stat(cli); err != nil || fi.IsDir() {
		return nil, nil, i18n.Errorf("cli.run.no_runtime", cli)
	}
	argv := append([]string{"node", cli, sub}, args...)
	set := map[string]string{"ORBIT_HOME": l.home, "ORBIT_ORB": l.orb, "ORBIT_ROLES_DIR": l.roles}
	env := make([]string, 0, len(l.base)+len(set))
	for _, kv := range l.base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; !ok {
			env = append(env, kv)
		}
	}
	for _, k := range []string{"ORBIT_HOME", "ORBIT_ORB", "ORBIT_ROLES_DIR"} {
		env = append(env, k+"="+set[k])
	}
	return argv, env, nil
}

// zeroArgTUI says whether orb with no arguments opens orbit-pi's TUI: only when
// both stdin and stdout are a terminal. Otherwise it prints the usage as before.
func zeroArgTUI(stdinTTY, stdoutTTY bool) bool { return stdinTTY && stdoutTTY }

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func cmdRun(a *app, args []string) error     { return a.orbitPi("run", args) }
func cmdProfile(a *app, args []string) error { return a.orbitPi("profile", args) }

// zeroArg is orb with no arguments on a terminal: orbit-pi's TUI, or the usage
// and one line on installing orbit-pi when it is not there yet.
func (a *app) zeroArg() int {
	l, err := launcher()
	if err == nil {
		_, _, err = l.command("tui", nil)
	}
	if err != nil {
		a.usage()
		fmt.Fprintln(a.stderr, err.Error())
		return 2
	}
	return a.exit("orb", a.orbitPi("tui", nil))
}

func launcher() (orbitPiLaunch, error) {
	home := paths().Home
	orb, err := os.Executable()
	if err != nil {
		return orbitPiLaunch{}, err
	}
	if real, err := filepath.EvalSymlinks(orb); err == nil {
		orb = real
	}
	return orbitPiLaunch{home: home, orb: orb, roles: filepath.Join(home, "runtime", "roles"), base: os.Environ(), getenv: os.Getenv,
		installed: func() bool { _, err := orbitpi.Status(home); return err == nil }}, nil
}

// orbitPi writes the role sources, then replaces this process with orbit-pi (unix)
// or runs it with this terminal and exits with its code (windows).
func (a *app) orbitPi(sub string, args []string) error {
	l, err := launcher()
	if err != nil {
		return err
	}
	argv, env, err := l.command(sub, args)
	if err != nil {
		return err
	}
	if err := orbitplugin.WriteRoles(l.roles); err != nil {
		return i18n.Errorf("cli.run.roles_failed", err)
	}
	return execOrbitPi(a, argv, env)
}

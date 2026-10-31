package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/orbitpi"
	"github.com/onidot-labs/orbit-mem/internal/orbitplugin"
)

// orbitPiLaunch builds the command that hands the terminal to orbit-pi's
// orchestrator (설계 · OpenOrbit 4차 R4-5): orb run, orb profile and orb with no
// arguments on a terminal.
type orbitPiLaunch struct {
	home, orb, roles string
	lang             string   // orb가 해석한 언어(en|ko). 환경에 ORBIT_LANG가 없을 때 넘긴다
	base             []string // 물려줄 환경
	getenv           func(string) string
	installed        func() bool
}

// orchestraCLI is orbit-pi's orchestrator entry inside its checkout.
var orchestraCLI = filepath.Join("packages", "orbit-orchestra", "src", "cli.ts")

// hostCLI is orbit-pi's daemon entry (orb daemon …, R4-4).
var hostCLI = filepath.Join("packages", "orbit-host", "src", "cli.ts")

// command is node <orbit-pi>/packages/orbit-orchestra/src/cli.ts <sub> <args…>
// with ORBIT_HOME, ORBIT_ORB and ORBIT_ROLES_DIR set. orbit-pi is ORBIT_PI_DIR
// when set, otherwise the checkout orb runtime install made.
func (l orbitPiLaunch) command(sub string, args []string) ([]string, []string, error) {
	return l.commandFor(orchestraCLI, sub, args)
}

// commandFor is command for one of orbit-pi's entries (orchestraCLI, hostCLI).
func (l orbitPiLaunch) commandFor(entry, sub string, args []string) ([]string, []string, error) {
	dir := l.getenv("ORBIT_PI_DIR")
	if dir == "" {
		if !l.installed() {
			return nil, nil, i18n.Errorf("cli.run.no_runtime", orbitpi.Dir(l.home))
		}
		dir = orbitpi.Dir(l.home)
	}
	cli := filepath.Join(dir, entry)
	if fi, err := os.Stat(cli); err != nil || fi.IsDir() {
		return nil, nil, i18n.Errorf("cli.run.no_runtime", cli)
	}
	argv := append([]string{"node", cli, sub}, args...)
	set := map[string]string{"ORBIT_HOME": l.home, "ORBIT_ORB": l.orb, "ORBIT_ROLES_DIR": l.roles}
	keys := []string{"ORBIT_HOME", "ORBIT_ORB", "ORBIT_ROLES_DIR"}
	if l.lang != "" && envValue(l.base, i18n.EnvVar) == "" {
		set[i18n.EnvVar] = l.lang // orbit-pi가 orb와 같은 언어로 말한다
		keys = append(keys, i18n.EnvVar)
	}
	env := make([]string, 0, len(l.base)+len(set))
	for _, kv := range l.base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; !ok {
			env = append(env, kv)
		}
	}
	for _, k := range keys {
		env = append(env, k+"="+set[k])
	}
	return argv, env, nil
}

// envValue is the value of key in an environment list (the last one wins, as in exec).
func envValue(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// zeroArgTUI says whether orb with no arguments opens orbit-pi's TUI: only when
// both stdin and stdout are a terminal. Otherwise it prints the usage as before.
func zeroArgTUI(stdinTTY, stdoutTTY bool) bool { return stdinTTY && stdoutTTY }

// isTerminal says whether v is a terminal: a character device that is not the
// null device (orb </dev/null must not open the TUI). It keeps to the standard
// library; a serial port or another character device would still count.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
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
	lang := string(i18n.Resolve(os.Getenv(i18n.EnvVar), config.Language(paths().Config())))
	return orbitPiLaunch{home: home, orb: orb, roles: filepath.Join(home, "runtime", "roles"), lang: lang, base: os.Environ(), getenv: os.Getenv,
		installed: func() bool { _, err := orbitpi.Status(home); return err == nil }}, nil
}

// orbitPi writes the role sources, then replaces this process with orbit-pi (unix)
// or runs it with this terminal and exits with its code (windows).
func (a *app) orbitPi(sub string, args []string) error {
	return a.orbitPiEntry(orchestraCLI, sub, args)
}

func (a *app) orbitPiEntry(entry, sub string, args []string) error {
	l, err := launcher()
	if err != nil {
		return err
	}
	argv, env, err := l.commandFor(entry, sub, args)
	if err != nil {
		return err
	}
	if err := orbitplugin.WriteRoles(l.roles); err != nil {
		return i18n.Errorf("cli.run.roles_failed", err)
	}
	return execOrbitPi(a, argv, env)
}

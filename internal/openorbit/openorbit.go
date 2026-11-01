// Package openorbit is orb's bridge to the OpenOrbit TS packages (설계 · OpenOrbit 4차
// 「구조 (2026-11-01 확정)」: the harness and the work record live in OpenOrbit; orb keeps
// memory, hooks and the MCP fragment). Until orb itself is OpenOrbit (N4-2 차례 5), orb work,
// orb plugin, orb install, the lab and orb run call the TS CLIs through it.
//
// A CLI is the package's ORBIT_*_CLI when set, otherwise the one orbit-pi's runtime carries:
// <runtime>/node_modules/@onidot-labs/<package>/dist/cli.js, the runtime being ORBIT_PI_DIR or
// $ORBIT_HOME/runtime/orbit-pi (orb runtime install). Node is ORBIT_NODE, node on PATH, or a
// usual install folder, so a GUI app that starts orb with a short PATH still works.
package openorbit

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/orbitpi"
)

// Package is one OpenOrbit TS package with a CLI.
type Package struct {
	Name string // npm name under @onidot-labs, also the CLI name
	Env  string // environment variable that points at the CLI file directly
}

// The packages orb calls.
var (
	Work    = Package{Name: "openorbit-work", Env: "ORBIT_WORK_CLI"}
	Harness = Package{Name: "openorbit-harness", Env: "ORBIT_HARNESS_CLI"}
)

// Runner runs the TS CLIs for one orb.
type Runner struct {
	Home string   // ORBIT_HOME as orb resolved it; the child always gets it
	Lang string   // orb's language (en|ko); the child gets it when ORBIT_LANG is not set
	Env  []string // the environment to pass on (os.Environ())
}

// ExitCode is a child's non-zero exit code.
type ExitCode int

func (c ExitCode) Error() string { return "exit status " + strconv.Itoa(int(c)) }

func (r Runner) getenv(key string) string { return envValue(r.Env, key) }

// nodeDirs are the usual node install folders, looked at after PATH.
func nodeDirs(home string) []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".volta", "bin"))
	}
	return dirs
}

// Node finds node: ORBIT_NODE, then PATH, then the usual install folders.
func (r Runner) Node() (string, error) {
	if v := r.getenv("ORBIT_NODE"); v != "" {
		p, err := exec.LookPath(v)
		if err != nil {
			return "", i18n.Errorf("openorbit.err.bad_node", v, err)
		}
		return p, nil
	}
	dirs := filepath.SplitList(r.getenv("PATH"))
	dirs = append(dirs, nodeDirs(r.getenv("HOME"))...)
	for _, d := range dirs {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(d, "node")); err == nil {
			return p, nil
		}
	}
	return "", i18n.Errorf("openorbit.err.no_node", strings.Join(nodeDirs(r.getenv("HOME")), ", "))
}

// CLI finds the package's CLI file.
func (r Runner) CLI(p Package) (string, error) {
	if v := r.getenv(p.Env); v != "" {
		if !isFile(v) {
			return "", i18n.Errorf("openorbit.err.bad_cli", p.Env, v)
		}
		return v, nil
	}
	rt := r.getenv("ORBIT_PI_DIR")
	if rt == "" {
		rt = orbitpi.Dir(r.Home)
	}
	cli := filepath.Join(rt, "node_modules", "@onidot-labs", p.Name, "dist", "cli.js")
	if !isFile(cli) {
		return "", i18n.Errorf("openorbit.err.no_cli", p.Name, cli, p.Env)
	}
	return cli, nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Command is node <cli> <args…> and the child's environment: the inherited one with
// ORBIT_HOME set to orb's home and, when ORBIT_LANG is not set, ORBIT_LANG to orb's language.
func (r Runner) Command(p Package, args ...string) ([]string, []string, error) {
	cli, err := r.CLI(p)
	if err != nil {
		return nil, nil, err
	}
	node, err := r.Node()
	if err != nil {
		return nil, nil, err
	}
	argv := append([]string{node, cli}, args...)
	set := map[string]string{"ORBIT_HOME": r.Home}
	keys := []string{"ORBIT_HOME"}
	if r.Lang != "" && r.getenv(i18n.EnvVar) == "" {
		set[i18n.EnvVar] = r.Lang // TS CLI가 orb와 같은 언어로 말한다
		keys = append(keys, i18n.EnvVar)
	}
	env := make([]string, 0, len(r.Env)+len(keys))
	for _, kv := range r.Env {
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

// Run runs the CLI to its end with the given standard streams. A non-zero exit is ExitCode.
func (r Runner) Run(p Package, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	argv, env, err := r.Command(p, args...)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, stdin, stdout, stderr
	return exitErr(cmd.Run())
}

// Output runs the CLI and gives its standard output. When it fails the error carries its
// standard error (and wraps ExitCode when it exited non-zero).
func (r Runner) Output(p Package, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	err := r.Run(p, nil, &out, &errb, args...)
	if err != nil {
		var code ExitCode
		if !errors.As(err, &code) {
			return out.Bytes(), err
		}
		what := p.Name
		if len(args) > 0 {
			what += " " + args[0]
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.Bytes(), &runError{code: code, msg: i18n.T("openorbit.err.failed", what, int(code), msg)}
	}
	return out.Bytes(), nil
}

type runError struct {
	code ExitCode
	msg  string
}

func (e *runError) Error() string { return e.msg }
func (e *runError) Unwrap() error { return e.code }

func exitErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 {
		return ExitCode(ee.ExitCode())
	}
	return err
}

// Exec hands this process to the CLI with orb's standard streams: unix replaces orb with
// node (execve), Windows runs node and returns its exit code as ExitCode.
func (r Runner) Exec(p Package, args ...string) error {
	argv, env, err := r.Command(p, args...)
	if err != nil {
		return err
	}
	return execve(argv, env)
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

// New is a Runner with this process's environment.
func New(home, lang string) Runner { return Runner{Home: home, Lang: lang, Env: os.Environ()} }

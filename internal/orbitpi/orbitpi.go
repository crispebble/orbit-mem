// Package orbitpi installs and updates orbit-pi, the agent runtime of OpenOrbit 4차
// (설계 · OpenOrbit 4차 「orbit-pi 저장소 운영」: orb가 orbit-pi를 설치·업데이트한다).
//
// orbit-pi is a git checkout under ORBIT_HOME/runtime/orbit-pi. Install clones it on the first run,
// fetches on later runs, checks out the requested ref, and runs the checkout's own
// orbit/scripts/build.sh. Node is needed only here and when orbit-pi runs.
package orbitpi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the private orbit-pi repository; git uses the user's own credentials.
const DefaultRepo = "https://github.com/onidot-labs/orbit-pi.git"

// DefaultRef is what install and update check out.
const DefaultRef = "origin/main"

// Node version orbit-pi needs (pi's engines field).
const nodeMajor, nodeMinor = 22, 19

// Options of one install or update.
type Options struct {
	Repo string
	Ref  string
	// Log receives git and build output; discarded when nil.
	Log io.Writer
}

// State is the last successful install.
type State struct {
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref"`
	Commit      string    `json:"commit"`
	InstalledAt time.Time `json:"installedAt"`
	// Current reports whether the checkout still is at Commit (Status only).
	Current bool `json:"current"`
}

// Dir is the orbit-pi checkout.
func Dir(home string) string { return filepath.Join(home, "runtime", "orbit-pi") }

func statePath(home string) string { return filepath.Join(home, "runtime", "orbit-pi.json") }

// nodeVersion reports `node -v`; tests replace it.
var nodeVersion = func() (string, error) {
	out, err := exec.Command("node", "-v").Output()
	return strings.TrimSpace(string(out)), err
}

// Install clones or fetches orbit-pi, checks out opts.Ref and builds it. The state file changes only
// after a successful build, so a failed update leaves the last good install on record.
func Install(home string, opts Options) (State, error) {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.Ref == "" {
		opts.Ref = DefaultRef
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	if v, err := nodeVersion(); err != nil || !nodeAtLeast(v, nodeMajor, nodeMinor) {
		return State{}, fmt.Errorf("orbit-pi needs Node %d.%d or newer (found %q)", nodeMajor, nodeMinor, v)
	}
	dir := Dir(home)
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return State{}, err
		}
		if err := run(log, "", "git", "clone", "--quiet", opts.Repo, dir); err != nil {
			return State{}, err
		}
	} else if err := run(log, dir, "git", "fetch", "--quiet", "--prune", "--tags", opts.Repo, "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return State{}, err
	}
	if err := run(log, dir, "git", "checkout", "--quiet", "--detach", opts.Ref); err != nil {
		return State{}, err
	}
	if err := run(log, dir, filepath.Join(dir, "orbit", "scripts", "build.sh")); err != nil {
		return State{}, fmt.Errorf("orbit-pi build failed: %w", err)
	}
	commit, err := output(dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return State{}, err
	}
	st := State{Repo: opts.Repo, Ref: opts.Ref, Commit: commit, InstalledAt: time.Now().UTC()}
	b, _ := json.MarshalIndent(st, "", "  ")
	return st, os.WriteFile(statePath(home), append(b, '\n'), 0o644)
}

// Status reads the last install and whether the checkout is still at it.
func Status(home string) (State, error) {
	b, err := os.ReadFile(statePath(home))
	if err != nil {
		return State{}, fmt.Errorf("orbit-pi is not installed (orb runtime install): %w", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	head, _ := output(Dir(home), "git", "rev-parse", "HEAD")
	st.Current = head == st.Commit
	return st, nil
}

var nodeVersionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.`)

func nodeAtLeast(v string, major, minor int) bool {
	m := nodeVersionRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	gotMajor, _ := strconv.Atoi(m[1])
	gotMinor, _ := strconv.Atoi(m[2])
	return gotMajor > major || (gotMajor == major && gotMinor >= minor)
}

func run(log io.Writer, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/install"
)

// cmdDaemon runs the orbit-pi daemon (설계 · OpenOrbit 4차 R4-4, D2). Login
// start is orb's job (launchd, like the retro schedule); everything else —
// serve, start, status, stop, device, call, watch — is orbit-pi's host CLI.
func cmdDaemon(a *app, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "enable":
			return a.daemonEnable()
		case "disable":
			if err := install.RemoveDaemonAgent(install.RunCommand); err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, i18n.T("cli.daemon.disabled"))
			return nil
		}
	}
	sub, rest := "help", []string(nil)
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	return a.orbitPiEntry(hostCLI, sub, rest)
}

// daemonEnable registers the login agent. A daemon started by hand is stopped
// first so the agent's own daemon takes the address.
func (a *app) daemonEnable() error {
	l, err := launcher()
	if err != nil {
		return err
	}
	argv, env, err := l.commandFor(hostCLI, "stop", nil)
	if err != nil {
		return err
	}
	stop := exec.Command(argv[0], argv[1:]...)
	stop.Env, stop.Stdout, stop.Stderr = env, a.stdout, a.stderr
	if err := stop.Run(); err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return i18n.Errorf("cli.run.no_node", err)
	}
	h := install.FindHosts()
	home := ""
	if os.Getenv("ORBIT_HOME") != "" {
		home = paths().Home
	}
	agent := install.DaemonAgent{
		Orb:      l.orb,
		Home:     home,
		PathDirs: daemonPathDirs(node, l.orb, []string{h.Claude, h.Codex}),
		Log:      filepath.Join(paths().Home, "runtime", "daemon", "daemon.log"),
	}
	where, err := install.ApplyDaemonAgent(agent, install.RunCommand)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.daemon.enabled", where, agent.Log))
	return nil
}

// daemonPathDirs is the PATH the daemon needs: node first, then the engines and orb.
func daemonPathDirs(node, orb string, engines []string) []string {
	var dirs []string
	for _, p := range append([]string{node, orb}, engines...) {
		if p == "" {
			continue
		}
		if d := filepath.Dir(p); !contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

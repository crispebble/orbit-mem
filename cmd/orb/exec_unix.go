//go:build !windows

package main

import (
	"os/exec"
	"syscall"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

// execOrbitPi replaces orb with node so orbit-pi's TUI gets the terminal as it is.
func execOrbitPi(_ *app, argv, env []string) error {
	node, err := exec.LookPath(argv[0])
	if err != nil {
		return i18n.Errorf("cli.run.no_node", err)
	}
	return syscall.Exec(node, argv, env)
}

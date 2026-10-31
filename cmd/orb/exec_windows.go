//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

// execOrbitPi runs node with this terminal and exits with its code (Windows has no exec).
func execOrbitPi(_ *app, argv, env []string) error {
	node, err := exec.LookPath(argv[0])
	if err != nil {
		return i18n.Errorf("cli.run.no_node", err)
	}
	cmd := exec.Command(node, argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(ee.ExitCode())
	}
	return err
}

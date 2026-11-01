//go:build windows

package openorbit

import (
	"os"
	"os/exec"
)

// execve runs node with orb's standard streams and returns its exit code (Windows has no exec).
func execve(argv, env []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	return exitErr(cmd.Run())
}

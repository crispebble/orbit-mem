//go:build !windows

package lab

import (
	"os/exec"
	"syscall"
)

// setProcessGroup runs the host in its own process group so a timeout stops
// its children (MCP servers, node) too.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

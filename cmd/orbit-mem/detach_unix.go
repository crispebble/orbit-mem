//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in its own session, so the host ending the hook's process group does not stop it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

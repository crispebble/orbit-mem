//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in its own session so it outlives the hook that started it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

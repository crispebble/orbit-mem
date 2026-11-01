//go:build windows

package lab

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

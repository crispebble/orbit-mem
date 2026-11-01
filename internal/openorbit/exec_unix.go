//go:build !windows

package openorbit

import "syscall"

// execve replaces orb with node so the CLI gets orb's terminal, pipes and signals as they are.
func execve(argv, env []string) error { return syscall.Exec(argv[0], argv, env) }

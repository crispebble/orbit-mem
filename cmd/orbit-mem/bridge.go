package main

import (
	"errors"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/openorbit"
)

// bridge runs the OpenOrbit TS CLIs with orb's home and language (설계 · OpenOrbit 4차
// 「구조 (2026-11-01 확정)」: the work record and the harness live in OpenOrbit).
func bridge() openorbit.Runner {
	return openorbit.New(paths().Home, string(i18n.Current()))
}

// passthrough hands this process to a TS CLI with the arguments as they are: its output,
// errors and exit code are orb's.
func (a *app) passthrough(p openorbit.Package, args []string) error {
	return asExit(bridge().Exec(p, args...))
}

// asExit turns a TS CLI's exit code into orb's, so orb prints nothing more.
func asExit(err error) error {
	var code openorbit.ExitCode
	if errors.As(err, &code) {
		return exitCode(code)
	}
	return err
}

// cmdWork is orbit-pi's and Orbit Studio's door to orbit.work: emit, get, list and mcp are
// openorbit-work's.
func cmdWork(a *app, args []string) error { return a.passthrough(openorbit.Work, args) }

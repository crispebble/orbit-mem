package main

import (
	"io"
	"os"
)

// isTerminal says whether v is a terminal: a character device that is not the
// null device. It keeps to the standard library; a serial port or another
// character device would still count.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

// outputIsTerminal decides the thin commands' output: tables and text for a
// person on a terminal, JSON for a pipe or a file. Tests replace it.
var outputIsTerminal = func(w io.Writer) bool { return isTerminal(w) }

// wantJSON says whether a thin command prints JSON: --json, or stdout is not a terminal.
func (a *app) wantJSON(asJSON bool) bool { return asJSON || !outputIsTerminal(a.stdout) }

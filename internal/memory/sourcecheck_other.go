//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package memory

import "os"

// openSource opens path for reading. Where the system has no open that refuses a link, fileHolds' check that
// the opened file is the one checked before (os.SameFile) is all that catches a swap.
func openSource(path string) (*os.File, error) { return os.Open(path) }

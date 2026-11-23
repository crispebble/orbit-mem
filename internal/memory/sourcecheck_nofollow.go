//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package memory

import (
	"os"
	"syscall"
)

// openSource opens path for reading without following a link in its last element (a path swapped for a link
// after the check fails with ELOOP) and without waiting for a writer (a FIFO swapped in does not hold the
// open: the later regular-file check turns it away).
func openSource(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

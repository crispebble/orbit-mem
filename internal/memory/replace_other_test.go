//go:build !unix

package memory

import "testing"

// recheckSpecialFiles has nothing to check where there are no FIFOs or unix permissions.
func recheckSpecialFiles(t *testing.T, dir string) { t.Helper() }

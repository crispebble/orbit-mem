// Package paths puts the recall paths of the evaluation set together by name. It is the one
// place that knows which adapters a name means; the command and the tests of the set both use it.
package paths

import (
	"fmt"
	"strings"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
)

// Names of the recall paths.
const (
	Current = "current" // the recall of today, through the host contract
	Legacy  = "legacy"  // the frozen recall of 87ab0fb
)

// The adapters the factories give (tests swap them to see what a path opens and closes). The legacy
// path is the frozen copy alone: its recall, search and similar search are all 87ab0fb's
// (internal/bench/legacy), so it opens nothing of the current path and a change of the current
// code cannot move the legacy baseline.
var (
	hostFactory bench.Factory = hostpath.Factory
	legacyEnv   bench.Factory = legacy.Env
)

// Names is every recall path, in report order.
var Names = []string{Current, Legacy}

// UnknownError is a recall path name that is not in Names.
type UnknownError struct{ Name string }

func (e UnknownError) Error() string {
	return fmt.Sprintf("unknown recall path %q (%s)", e.Name, strings.Join(Names, ", "))
}

// Factories gives the factory of every name in order; spaces around a name are dropped and a
// name given twice is used once.
func Factories(names []string) ([]bench.Factory, error) {
	var out []bench.Factory
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if seen[n] {
			continue
		}
		switch n {
		case Current:
			out = append(out, hostFactory)
		case Legacy:
			out = append(out, legacyEnv)
		default:
			return nil, UnknownError{Name: n}
		}
		seen[n] = true
	}
	return out, nil
}

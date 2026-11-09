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

// The adapters the factories assemble (tests swap them to see what Close closes).
var (
	hostFactory bench.Factory = hostpath.Factory
	legacyEnv                 = legacy.Env
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
			out = append(out, legacyFactory)
		default:
			return nil, UnknownError{Name: n}
		}
		seen[n] = true
	}
	return out, nil
}

// legacyFactory pairs the frozen recall with the search and the similar search of the current
// path (the frozen recall has none of its own) on the same DB copy, so only the recall differs.
// Close closes both.
func legacyFactory(db string, set *bench.Set) (bench.Env, error) {
	env, err := legacyEnv(db, set)
	if err != nil {
		return bench.Env{}, err
	}
	hp, err := hostFactory(db, set)
	if err != nil {
		env.Close()
		return bench.Env{}, err
	}
	env.Finder, env.Similar = hp.Finder, hp.Similar
	closeLegacy := env.Close
	env.Close = func() error {
		err := hp.Close()
		if cerr := closeLegacy(); err == nil {
			err = cerr
		}
		return err
	}
	return env, nil
}

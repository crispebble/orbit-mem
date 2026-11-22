// Package paths puts the recall paths of the evaluation set together by name. It is the one
// place that knows which adapters a name means; the command and the tests of the set both use it.
package paths

import (
	"fmt"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
	"github.com/crispebble/orbit-mem/internal/memory"
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
	hostFactory func(hostpath.Options) bench.Factory = hostpath.FactoryWith
	legacyEnv   bench.Factory                        = legacyWithChange
)

// Names is every recall path, in report order.
var Names = []string{Current, Legacy}

// UnknownError is a recall path name that is not in Names.
type UnknownError struct{ Name string }

func (e UnknownError) Error() string {
	return fmt.Sprintf("unknown recall path %q (%s)", e.Name, strings.Join(Names, ", "))
}

// Options are what the command hands the paths. The overrides reach the current path only: the
// frozen recall reads no settings.
type Options struct {
	Set map[string]string // settings by their key in the settings file
}

// Factories gives the factory of every name in order; spaces around a name are dropped and a
// name given twice is used once.
func Factories(names []string) ([]bench.Factory, error) { return FactoriesWith(names, Options{}) }

// FactoriesWith is Factories with the options. An override the current path refuses is an error
// of the run that opens it.
func FactoriesWith(names []string, o Options) ([]bench.Factory, error) {
	var out []bench.Factory
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if seen[n] {
			continue
		}
		switch n {
		case Current:
			out = append(out, hostFactory(hostpath.Options{Set: o.Set}))
		case Legacy:
			out = append(out, legacyEnv)
		default:
			return nil, UnknownError{Name: n}
		}
		seen[n] = true
	}
	return out, nil
}

// legacyWithChange is the frozen path with the one thing the frozen recall has no part in: moving a memory
// to stale or hidden between two session starts (Case.Changes), through the same store call the current path
// uses (memory.Store.SetState, on a connection of its own). The recall, the search and the similar search stay
// the frozen copy; a case with changes is then measured on both paths instead of being counted as a miss
// on the one that skipped them.
func legacyWithChange(db string, set *bench.Set) (bench.Env, error) {
	env, err := legacy.Env(db, set)
	if err != nil {
		return env, err
	}
	now := set.At()
	setNow := env.SetNow
	env.SetNow = func(t time.Time) {
		now = t
		if setNow != nil {
			setNow(t)
		}
	}
	env.Change = func(id, state string) error {
		s, err := memory.Open(db)
		if err != nil {
			return fmt.Errorf("legacy: change %s: %w", id, err)
		}
		defer s.Close()
		s.Now = func() time.Time { return now }
		if _, err := s.SetState(id, state, "bench change"); err != nil {
			return fmt.Errorf("legacy: change %s to %s: %w", id, state, err)
		}
		return nil
	}
	return env, nil
}

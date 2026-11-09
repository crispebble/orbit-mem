package paths

import "github.com/crispebble/orbit-mem/internal/bench"

// SwapAdapters replaces the two adapters legacyFactory pairs until the returned function runs.
func SwapAdapters(host bench.Factory, legacy func(string, *bench.Set) (bench.Env, error)) (restore func()) {
	oldHost, oldLegacy := hostFactory, legacyEnv
	hostFactory, legacyEnv = host, legacy
	return func() { hostFactory, legacyEnv = oldHost, oldLegacy }
}

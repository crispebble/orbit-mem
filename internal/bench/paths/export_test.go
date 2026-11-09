package paths

import "github.com/crispebble/orbit-mem/internal/bench"

// SwapAdapters replaces the adapters of the current and the legacy path until the returned function runs.
func SwapAdapters(host bench.Factory, legacy bench.Factory) (restore func()) {
	oldHost, oldLegacy := hostFactory, legacyEnv
	hostFactory, legacyEnv = host, legacy
	return func() { hostFactory, legacyEnv = oldHost, oldLegacy }
}

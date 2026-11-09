package paths

import (
	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
)

// SwapAdapters replaces the adapters of the current and the legacy path until the returned function runs.
func SwapAdapters(host bench.Factory, legacy bench.Factory) (restore func()) {
	oldHost, oldLegacy := hostFactory, legacyEnv
	hostFactory, legacyEnv = func(hostpath.Options) bench.Factory { return host }, legacy
	return func() { hostFactory, legacyEnv = oldHost, oldLegacy }
}

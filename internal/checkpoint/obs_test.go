package checkpoint

// The §5b observer seam's own tests. They are IN-package rather than in checkpoint_test because
// the no-op registry and the package-level accessors are unexported: their whole purpose is to make
// the receiver-less functions safe before any writer exists, and that is only assertable from
// inside.

import (
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// restoreObservers puts the package back the way it was found. These are process-wide, so a test
// that set them and did not restore them would silently change what every later test in the
// package is measuring.
func restoreObservers(t *testing.T) {
	t.Helper()
	obsMu.RLock()
	l, m := curLog, curReg
	obsMu.RUnlock()
	t.Cleanup(func() {
		obsMu.Lock()
		curLog, curReg = l, m
		obsMu.Unlock()
	})
}

func TestPkgObserversAreNeverNil(t *testing.T) {
	restoreObservers(t)

	// The default state, before anything constructs a writer. Every receiver-less function in this
	// package reaches its observers through these two, so a nil here is a nil-pointer panic in
	// ExtractDecisions, Truncate or ValidatePointers -- reached from a hook, on the hot path.
	SetObservers(nil, nil)
	require.NotNil(t, pkgLog())
	require.NotNil(t, pkgMetrics())

	require.NotPanics(t, func() {
		pkgLog().Warn("no observers are set", "k", "v")
		pkgMetrics().Counter("checkpoint.test_counter").Add(1)
		pkgMetrics().Hist("checkpoint.test_hist").Observe(time.Millisecond)
		pkgMetrics().Gauge("checkpoint.test_gauge").Set(1)
	})
}

func TestSetObserversLastWriteWins(t *testing.T) {
	restoreObservers(t)

	reg := obs.New(nil)
	SetObservers(logging.Nop(), reg)
	require.Same(t, reg, pkgMetrics(), "the registry just installed is the one the package uses")

	// A later nil must not blank a live registry: SetObservers is called from OpenWriter, and a
	// second writer constructed with no metrics would otherwise silently disable the first one's.
	SetObservers(nil, nil)
	require.NotNil(t, pkgMetrics())
}

func TestNopRegistrySatisfiesTheWholeInterface(t *testing.T) {
	// obs.Registry has six methods, not the four the subplan names. A compile-time assertion is
	// what actually pins that, but the zero-value returns are asserted too: a Snapshot with nil
	// maps would panic in any caller that ranged over it, and CheckBudgets returning a non-empty
	// slice would report phantom breaches from a package with no observers configured.
	var r obs.Registry = nopRegistry{}

	require.NotPanics(t, func() {
		r.Hist("h").Observe(time.Millisecond)
		r.Counter("c").Add(3)
		r.Gauge("g").Set(2)
	})
	require.Zero(t, r.Counter("c").Value(), "a no-op counter never accumulates")
	require.Zero(t, r.Gauge("g").Value())

	snap := r.Snapshot()
	require.NotNil(t, snap.Counters, "an initialized empty map, not a nil one")
	require.NotNil(t, snap.Gauges)
	require.Empty(t, r.CheckBudgets(config.Defaults()))
	require.NoError(t, r.Persist(paths.Of(t.TempDir())))
}

func TestNopHistogramResets(t *testing.T) {
	h := nopRegistry{}.Hist("h")
	h.Observe(time.Millisecond)
	h.Observe(2 * time.Millisecond)
	require.NotPanics(t, func() { h.Reset() })
	// N stays zero however many samples arrive. That is what makes the no-op safe as a DEFAULT:
	// a caller that later reads a percentile off it gets a documented zero rather than a number
	// that looks measured but was never configured.
	require.Zero(t, h.Snapshot().N)
	require.Zero(t, h.Snapshot().P99)
}

package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/canon/canontest"
	"github.com/qompack/qompack/internal/config"
)

// TestCanonConformance runs SP-01's Registry conformance suite against SP-04's real implementations
// and requires it to pass with ZERO skips.
//
// The suite's behaviour block is guarded by skipIfStub, which probes Run for core.ErrNotImplemented.
// While canon was a stub that guard fired and devtool lint's stubskips sub-check counted the skip
// as owed work; the point of this test is that the guard no longer fires, so the assertions SP-01
// wrote — idempotence, non-growth, the Restore round trip and registration-order stability — are
// now actually executed against the fourteen canonicalizers rather than merely declared.
//
// Both factories are exercised because they are different shapes of Registry and the suite is
// written to accept either: NewRegistry starts empty, while Default arrives with the fourteen
// built-ins already registered. A suite that only ever saw one of them would not have proved that
// registration order survives a registry that was not empty to begin with.
func TestCanonConformance(t *testing.T) {
	canontest.RunRegistrySuite(t, "canon.NewRegistry", func(t *testing.T) canon.Registry {
		return canon.NewRegistry()
	})
	canontest.RunRegistrySuite(t, "canon.Default", func(t *testing.T) canon.Registry {
		return canon.Default(config.Defaults().Store.Canonicalize)
	})
}

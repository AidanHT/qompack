// Package canontest is the conformance suite for canon.Registry (00-ARCHITECTURE.md §5.22): every
// implementation SP-04 ships must pass RunRegistrySuite. SP-01 ships the suite itself, including
// the behaviour assertions SP-04 inherits (Rule W-1) — only the guarded /behaviour block is
// skipped until a real Registry lands.
package canontest

import (
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// shapeProbeCanonicalizer is a minimal canon.Canonicalizer used only by the shape block's Register
// probe call.
type shapeProbeCanonicalizer struct{}

func (shapeProbeCanonicalizer) Name() string                   { return "shape-probe" }
func (shapeProbeCanonicalizer) Applies(tool, path string) bool { return true }
func (shapeProbeCanonicalizer) Canonicalize(in []byte, o canon.Options) (canon.Result, error) {
	return canon.Result{Canonical: in}, nil
}

// RunRegistrySuite is the conformance suite for canon.Registry. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Registry on
// every call. The returned Registry need not start empty — a factory built from canon.Default,
// for example, arrives with SP-04's fourteen real canonicalizers already registered; see
// runRegistrationOrderPreservedCase's own comment for how the behaviour block stays correct
// either way.
func RunRegistrySuite(t *testing.T, name string, factory func(t *testing.T) canon.Registry) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		r := factory(t)
		require.NotNil(t, r)

		requireKnownError(t, r.Register(shapeProbeCanonicalizer{}))

		// For and Names have no error return; any value they produce, including nil, is
		// shape-valid.
		_ = r.For("bash", "some/path")
		_ = r.Names()

		_, err := r.Run("bash", "some/path", []byte("shape probe"), canon.Options{})
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("idempotent", func(t *testing.T) { runIdempotenceCase(t, factory) })
		t.Run("restore_after_canonicalize_is_identity_with_keep_deltas", func(t *testing.T) {
			runRestoreRoundTripCase(t, factory)
		})
		t.Run("never_grows_input", func(t *testing.T) { runNeverGrowsCase(t, factory) })
		t.Run("registration_order_preserved", func(t *testing.T) { runRegistrationOrderPreservedCase(t, factory) })
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Registry. plans/OWNERS.tsv names
// canon's probe method "Canonicalize", but Registry has no method by that name — Run is
// Registry's own operation that performs canonicalization, dispatching to every applicable
// Canonicalizer's Canonicalize in turn — so this probes Run instead, the natural realization of
// that same probe at the Registry level.
func isStub(t *testing.T, factory func(t *testing.T) canon.Registry) bool {
	t.Helper()
	_, err := factory(t).Run("probe-tool", "probe/path", []byte("probe"), canon.Options{})
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Registry, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) canon.Registry) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

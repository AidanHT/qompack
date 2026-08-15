// Package pinstest is the conformance suite for pins.Store (00-ARCHITECTURE.md §5.22): every
// implementation SP-10 ships must pass RunPinsSuite. SP-01 ships the suite itself, including the
// behaviour assertions SP-10 inherits (Rule W-1) — only the guarded /behaviour block is skipped
// until a real Store lands.
package pinstest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pins"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probePinnedMillis is an arbitrary, fixed timestamp used only by the shape-block probe
// invariant; its value carries no meaning beyond "a valid core.UnixMilli".
const probePinnedMillis = 1_700_000_000_000

// RunPinsSuite is the conformance suite for pins.Store. name distinguishes multiple factories run
// in the same test binary; factory must return a fresh, ready-to-use Store on every call.
func RunPinsSuite(t *testing.T, name string, factory func(t *testing.T) pins.Store) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		ctx := context.Background()

		_, err := s.All(ctx)
		requireKnownError(t, err)

		probe := pins.Invariant{
			ID:     "inv_shape_probe",
			Text:   "shape-probe invariant",
			Source: "agent",
			Pinned: core.UnixMilli(probePinnedMillis),
		}
		requireKnownError(t, s.Add(ctx, probe))
		requireKnownError(t, s.Remove(ctx, probe.ID))
		requireKnownError(t, s.Materialize(ctx))
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("add_is_append_only_and_all_returns_append_order", func(t *testing.T) {
			s := factory(t)
			ctx := context.Background()

			first := pins.Invariant{ID: "inv_a", Text: "first pinned fact", Source: "user", Pinned: core.UnixMilli(probePinnedMillis)}
			second := pins.Invariant{ID: "inv_b", Text: "second pinned fact", Source: "agent", Pinned: core.UnixMilli(probePinnedMillis + 1)}

			require.NoError(t, s.Add(ctx, first))
			require.NoError(t, s.Add(ctx, second))

			got, err := s.All(ctx)
			require.NoError(t, err)
			require.Equal(t, []pins.Invariant{first, second}, got,
				"All must return invariants in append order")
		})

		t.Run("remove_writes_a_tombstone_and_all_excludes_it", func(t *testing.T) {
			s := factory(t)
			ctx := context.Background()

			keep := pins.Invariant{ID: "inv_keep", Text: "must survive", Source: "user", Pinned: core.UnixMilli(probePinnedMillis)}
			drop := pins.Invariant{ID: "inv_drop", Text: "must be tombstoned", Source: "user", Pinned: core.UnixMilli(probePinnedMillis)}

			require.NoError(t, s.Add(ctx, keep))
			require.NoError(t, s.Add(ctx, drop))
			require.NoError(t, s.Remove(ctx, drop.ID))

			got, err := s.All(ctx)
			require.NoError(t, err)
			require.Equal(t, []pins.Invariant{keep}, got,
				"Remove must tombstone, never rewrite: All must exclude the removed ID and keep the rest")

			// Removing an ID a second time must not error and must not resurrect it: a tombstone
			// is itself an append-only record, never a toggle.
			require.NoError(t, s.Remove(ctx, drop.ID))
			got, err = s.All(ctx)
			require.NoError(t, err)
			require.Equal(t, []pins.Invariant{keep}, got)
		})

		t.Run("materialize_regenerates_the_view_without_changing_the_logical_set", func(t *testing.T) {
			s := factory(t)
			ctx := context.Background()

			inv := pins.Invariant{ID: "inv_materialize", Text: "survives a materialize", Source: "agent", Pinned: core.UnixMilli(probePinnedMillis)}
			require.NoError(t, s.Add(ctx, inv))

			before, err := s.All(ctx)
			require.NoError(t, err)

			require.NoError(t, s.Materialize(ctx))

			after, err := s.All(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after,
				"Materialize regenerates the on-disk view; it must never change the logical set All reports")
		})
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

// isStub reports whether factory currently produces a stub Store, using Add as the probe
// (plans/OWNERS.tsv: pins's probe method is Add).
func isStub(t *testing.T, factory func(t *testing.T) pins.Store) bool {
	t.Helper()
	err := factory(t).Add(context.Background(), pins.Invariant{
		ID: "inv_stub_probe", Text: "stub probe", Source: "agent", Pinned: core.UnixMilli(probePinnedMillis),
	})
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Store, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) pins.Store) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

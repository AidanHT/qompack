// Package schedulertest is the conformance suite for scheduler.Runtime (00-ARCHITECTURE.md
// §5.22): every implementation SP-12 ships must pass RunSchedulerSuite. SP-01 ships the suite
// itself, including the behaviour assertions SP-12 inherits (Rule W-1) — only the guarded
// /behaviour block is skipped until a real Runtime lands.
package schedulertest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunSchedulerSuite is the conformance suite for scheduler.Runtime. name distinguishes multiple
// factories run in the same test binary (for example "stub" vs. a real implementation's own
// name); factory must return a fresh, ready-to-use Runtime on every call.
func RunSchedulerSuite(t *testing.T, name string, factory func(t *testing.T) scheduler.Runtime) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		rt := factory(t)
		require.NotNil(t, rt)

		ctx := context.Background()

		// Observe has no error return; any ChangepointState it produces is shape-valid, so this
		// only asserts that calling it does not panic.
		_ = rt.Observe(ctx, scheduler.Features{PathJaccard: 1}, core.TurnIndex(0))

		_, err := rt.Evaluate(ctx)
		requireKnownError(t, err)

		rt.NotifyActivity(core.UnixMilli(1))

		// IdleSince has no error return; any (UnixMilli, bool) pair is shape-valid.
		_, _ = rt.IdleSince()

		requireKnownError(t, rt.Persist(ctx))
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("runtime_evaluate_is_deterministic", func(t *testing.T) {
			rt := factory(t)
			ctx := context.Background()
			d1, err1 := rt.Evaluate(ctx)
			d2, err2 := rt.Evaluate(ctx)
			require.Equal(t, err1, err2)
			require.Equal(t, d1, d2,
				"Evaluate must be pure: two calls against unchanged Runtime state must agree")
		})

		t.Run("evaluate_young_daly_matches_formula", func(t *testing.T) {
			runYoungDalyFormulaCase(t)
		})

		t.Run("composite_trigger_truth_table", func(t *testing.T) {
			runCompositeTriggerTruthTable(t)
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

// isStub reports whether factory currently produces a stub Runtime, using Evaluate as the probe
// (plans/OWNERS.tsv: scheduler's probe method is Evaluate).
func isStub(t *testing.T, factory func(t *testing.T) scheduler.Runtime) bool {
	t.Helper()
	_, err := factory(t).Evaluate(context.Background())
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Runtime, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) scheduler.Runtime) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

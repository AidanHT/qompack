// Package rehydratetest is the conformance suite for rehydrate.Build (00-ARCHITECTURE.md §5.22):
// every implementation SP-11 ships must pass RunRehydrateSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-11 inherits (Rule W-1) — only the guarded /behaviour
// block is skipped until a real Build lands.
//
// The suite takes a pre-bound BuildFunc rather than calling rehydrate.Build directly. A <pkg>test
// subpackage may import only its own package, testutil and core (00-ARCHITECTURE.md §3.2), and
// Build's second argument is a rehydrate.Deps full of store, negknow, dag, rules, skills and
// tokens seams this package may not construct. The caller, which may, binds them — and also fills
// in Request.Cfg, which is a config.Config for the same reason.
package rehydratetest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// BuildFunc is rehydrate.Build with its Deps already bound by the caller. A caller binds it as:
//
//	func(ctx context.Context, r rehydrate.Request) (rehydrate.Result, error) {
//	    r.Cfg = cfg                      // the suite cannot name config.Config (§3.2)
//	    return rehydrate.Build(ctx, r, deps)
//	}
//
// The closure may fill in Request fields the suite left zero, but must not overwrite Session,
// Source, Budget or Checkpoint: those are what each case varies.
type BuildFunc func(ctx context.Context, r rehydrate.Request) (rehydrate.Result, error)

// RunRehydrateSuite is the conformance suite for rehydrate.Build. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use BuildFunc on
// every call.
func RunRehydrateSuite(t *testing.T, name string, factory func(t *testing.T) BuildFunc) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		build := factory(t)
		require.NotNil(t, build)

		_, err := build(context.Background(), populatedRequest(generousBudget))
		requireKnownError(t, err)

		// A request naming no checkpoint at all is the SessionStart `startup` case, and must be
		// answered the same way: a Result and a known sentinel, never a panic.
		_, err = build(context.Background(), rehydrate.Request{
			Session: core.SessionID(fixtureSession),
			Source:  SourceStartup,
			Budget:  generousBudget,
		})
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("items_are_in_the_normative_item_kind_order", func(t *testing.T) { runItemOrderCase(t, factory) })
		t.Run("total_tokens_never_exceed_the_budget", func(t *testing.T) { runBudgetCase(t, factory) })
		t.Run("what_did_not_fit_is_reported_as_dropped", func(t *testing.T) { runDropReportCase(t, factory) })
		t.Run("the_payload_is_injection_tagged", func(t *testing.T) { runInjectionTagCase(t, factory) })
		t.Run("the_eliminations_item_carries_the_standing_instruction", func(t *testing.T) {
			runStandingInstructionCase(t, factory)
		})
		t.Run("a_starved_budget_degrades_rather_than_failing", func(t *testing.T) { runDegradeCase(t, factory) })
		t.Run("the_result_names_the_checkpoint_it_came_from", func(t *testing.T) { runSeqCase(t, factory) })
		t.Run("build_is_deterministic", func(t *testing.T) { runDeterminismCase(t, factory) })
		t.Run("every_session_start_source_is_answered", func(t *testing.T) { runSourceCase(t, factory) })
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

// isStub reports whether factory currently produces a stub Build (plans/OWNERS.tsv: rehydrate's
// probe method is Build).
func isStub(t *testing.T, factory func(t *testing.T) BuildFunc) bool {
	t.Helper()
	_, err := factory(t)(context.Background(), populatedRequest(generousBudget))
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Build, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) BuildFunc) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

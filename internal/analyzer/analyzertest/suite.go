// Package analyzertest is the conformance suite for analyzer.Selector, analyzer.DeltaScorer and
// analyzer.DetectRedundancy (00-ARCHITECTURE.md §5.22): every implementation SP-15 ships must
// pass RunSelectorSuite, RunDeltaScorerSuite and RunRedundancySuite. SP-01 ships the suites
// themselves, including the behaviour assertions SP-15 inherits (Rule W-1) — only each guarded
// /behaviour block is skipped until a real implementation lands.
//
// RunSelectorSuite has a third block the other suites do not: /constructor, which is NEVER
// skipped. analyzer.NewSelector's two guards are implemented for real by SP-01 (§14.1 rule 3 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md) precisely because they are structural —
// §13 invariant 4 and the closing note's ship-order priority 3 — so gating them behind the stub
// probe would leave the whole of §15's analyzertest behaviour row unexecuted in exactly the
// builds it was written to protect.
//
// The suites take pre-bound closures (NewSelectorFunc, DetectFunc) rather than the raw §5.12
// signatures. A <pkg>test subpackage may import only its own package, testutil and core
// (00-ARCHITECTURE.md §3.2), and §5.12's signatures name dag.Slice, map[dag.NodeID]float64 and
// store.Store — none of which this package may construct. The caller, which may, binds them; the
// suite supplies every assertion. Values of those types can still be OBSERVED here without an
// import, since Go needs no import to hold a value or to convert dag.NodeID to a string.
package analyzertest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// NewSelectorFunc is analyzer.NewSelector with its dag.Slice, lambda and lazy arguments already
// bound by the caller, and its Δ-score map keyed by the plain string form of a dag.NodeID —
// analyzertest may import neither dag nor config (§3.2). A caller binds it as:
//
//	func(p int, blocks []analyzer.Block, delta map[string]float64) (analyzer.Selector, error) {
//	    d := make(map[dag.NodeID]float64, len(delta))
//	    for k, v := range delta { d[dag.NodeID(k)] = v }
//	    return analyzer.NewSelector(p, blocks, slice, d, lambda, lazy)
//	}
type NewSelectorFunc func(p int, blocks []analyzer.Block, delta map[string]float64) (analyzer.Selector, error)

// DetectFunc is analyzer.DetectRedundancy with its store.Store argument already bound by the
// caller — analyzertest may not import store (§3.2).
type DetectFunc func(ctx context.Context, sess core.SessionID) (analyzer.RedundancyReport, error)

// RedundancyFixture is everything RunRedundancySuite needs. A real caller supplies a Detect bound
// to a store that already holds Session's tool uses, plus a second session id it deliberately
// recorded nothing under.
type RedundancyFixture struct {
	// Detect is the implementation under test.
	Detect DetectFunc
	// Session is a session with recorded tool uses.
	Session core.SessionID
	// EmptySession is a session with no recorded tool uses at all.
	EmptySession core.SessionID
}

// RunSelectorSuite is the conformance suite for analyzer.NewSelector and analyzer.Selector. name
// distinguishes multiple factories run in the same test binary; factory must return a fresh,
// ready-to-use NewSelectorFunc on every call.
func RunSelectorSuite(t *testing.T, name string, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		newSelector := factory(t)
		require.NotNil(t, newSelector)

		sel, err := newSelector(probeP, legalBlocks(), legalDelta())
		requireKnownError(t, err)
		if err != nil {
			return // a refused construction is shape-valid; /constructor pins WHICH refusals are legal
		}
		require.NotNil(t, sel, "a construction that reports no error must hand back a usable Selector")
		require.Equal(t, probeP, sel.P(), "a Selector reports the p it was constructed with")

		_, err = sel.Select(context.Background(), probeBudget)
		requireKnownError(t, err)
	})

	// NEVER skipped: see the package comment. These are SP-01's own implemented guards.
	t.Run(name+"/constructor", func(t *testing.T) {
		t.Run("refuses_any_block_before_p", func(t *testing.T) { runPreconditionPosCase(t, factory) })
		t.Run("pos_check_precedes_the_ship_order_check", func(t *testing.T) { runGuardOrderCase(t, factory) })
		t.Run("a_legal_candidate_set_either_constructs_or_reports_not_implemented", func(t *testing.T) {
			runShipOrderGateCase(t, factory)
		})
		t.Run("a_block_exactly_at_p_is_a_candidate", func(t *testing.T) { runPosBoundaryCase(t, factory) })
	})

	if skipIfStubSelector(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("selection_never_exceeds_its_budget", func(t *testing.T) { runBudgetCase(t, factory) })
		t.Run("keep_and_dropped_partition_the_candidate_set", func(t *testing.T) { runPartitionCase(t, factory) })
		t.Run("nothing_before_p_is_ever_kept", func(t *testing.T) { runNothingBeforePCase(t, factory) })
		t.Run("a_zero_budget_keeps_nothing", func(t *testing.T) { runZeroBudgetCase(t, factory) })
		t.Run("selection_is_deterministic", func(t *testing.T) { runSelectionDeterminismCase(t, factory) })
		t.Run("lazy_greedy_reports_its_evaluation_count", func(t *testing.T) { runItersCase(t, factory) })
	})
}

// RunDeltaScorerSuite is the conformance suite for analyzer.DeltaScorer. name distinguishes
// multiple factories run in the same test binary; factory must return a fresh, ready-to-use
// DeltaScorer on every call.
func RunDeltaScorerSuite(t *testing.T, name string, factory func(t *testing.T) analyzer.DeltaScorer) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		require.Contains(t,
			[]analyzer.DeltaMode{analyzer.DeltaCheap, analyzer.DeltaMedium, analyzer.DeltaExpensive},
			s.Mode(), "Mode must report one of the three config.selection.deltaScoring tiers")

		_, err := s.Score(context.Background(), legalBlocks(), coveringContinuation())
		requireKnownError(t, err)
	})

	if skipIfStubScorer(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("every_block_is_scored_in_the_unit_interval", func(t *testing.T) { runScoreRangeCase(t, factory) })
		t.Run("scoring_is_deterministic", func(t *testing.T) { runScoreDeterminismCase(t, factory) })
		t.Run("an_empty_block_set_scores_nothing", func(t *testing.T) { runScoreEmptyCase(t, factory) })
		t.Run("a_covering_continuation_scores_at_least_an_empty_one", func(t *testing.T) {
			runScoreCoverageCase(t, factory)
		})
	})
}

// RunRedundancySuite is the conformance suite for analyzer.DetectRedundancy. name distinguishes
// multiple factories run in the same test binary; factory must return a fresh, ready-to-use
// fixture on every call.
func RunRedundancySuite(t *testing.T, name string, factory func(t *testing.T) RedundancyFixture) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		f := factory(t)
		require.NotNil(t, f.Detect)

		_, err := f.Detect(context.Background(), f.Session)
		requireKnownError(t, err)
	})

	if skipIfStubDetect(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("a_session_with_no_tool_uses_reports_nothing", func(t *testing.T) { runRedundancyEmptyCase(t, factory) })
		t.Run("no_tool_use_is_its_own_near_duplicate", func(t *testing.T) { runRedundancySelfCase(t, factory) })
		t.Run("detection_is_deterministic_and_read_only", func(t *testing.T) { runRedundancyDeterminismCase(t, factory) })
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

// isStubSelector reports whether factory currently produces a stub Selector, using Select as the
// probe (plans/OWNERS.tsv: analyzer's probe method is Select).
//
// It has to look in two places, because there are two distinct ways this package can still be a
// stub: the ship-order gate can refuse to construct a Selector at all (core.ErrNotImplemented
// from NewSelector, which is the state of every build before SP-12), or a constructed Selector's
// Select can be the SP-01 stub. Either one means the behaviour block cannot run.
func isStubSelector(t *testing.T, factory func(t *testing.T) NewSelectorFunc) bool {
	t.Helper()
	sel, err := factory(t)(probeP, legalBlocks(), legalDelta())
	if err != nil {
		return core.IsNotImplemented(err)
	}
	_, err = sel.Select(context.Background(), probeBudget)
	return core.IsNotImplemented(err)
}

// skipIfStubSelector calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub Selector, and reports whether it did.
func skipIfStubSelector(t *testing.T, factory func(t *testing.T) NewSelectorFunc) bool {
	t.Helper()
	if isStubSelector(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubScorer reports whether factory currently produces a stub DeltaScorer, using Score as the
// probe: it is DeltaScorer's only operation with an error return, and therefore its own analogue
// of the Select probe plans/OWNERS.tsv names for the package as a whole.
func isStubScorer(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) bool {
	t.Helper()
	_, err := factory(t).Score(context.Background(), legalBlocks(), coveringContinuation())
	return core.IsNotImplemented(err)
}

// skipIfStubScorer calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub DeltaScorer, and reports whether it did.
func skipIfStubScorer(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) bool {
	t.Helper()
	if isStubScorer(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubDetect reports whether factory currently produces a stub DetectRedundancy.
func isStubDetect(t *testing.T, factory func(t *testing.T) RedundancyFixture) bool {
	t.Helper()
	f := factory(t)
	_, err := f.Detect(context.Background(), f.Session)
	return core.IsNotImplemented(err)
}

// skipIfStubDetect calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub DetectRedundancy, and reports whether it did.
func skipIfStubDetect(t *testing.T, factory func(t *testing.T) RedundancyFixture) bool {
	t.Helper()
	if isStubDetect(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

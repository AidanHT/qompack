package guards

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/scheduler"
)

// TestGuard_Phase0BeforeStore is closing note 1: "Phase 0. Without measurement, everything else is
// opinion."
//
// The failure this prevents is not a broken build — it is a project that ships a store, measures
// nothing, and can no longer tell whether any of it helped. Once the store exists without a
// baseline there is nothing left to compare against, and the comparison cannot be reconstructed
// after the fact.
func TestGuard_Phase0BeforeStore(t *testing.T) {
	if isStub(t, evalProbe) && !isStub(t, storeProbe) {
		t.Fatal("store (Phase 1) is implemented while eval (Phase 0) is still a stub — " +
			"closing note 1: without measurement, everything else is opinion")
	}
}

// TestGuard_StoreAndNegknowBeforeCheckpoint is closing note 2: the store and negative knowledge
// take priority over everything downstream of them.
//
// A checkpointer built before them has nothing durable to point at, so it would necessarily
// summarize from the transcript — which is the "compressing a compression" failure the whole
// design exists to avoid (§7.4). Landing it early would bake that shortcut in.
func TestGuard_StoreAndNegknowBeforeCheckpoint(t *testing.T) {
	if !isStub(t, checkpointProbe) && (isStub(t, storeProbe) || isStub(t, negknowProbe)) {
		t.Fatal("checkpoint (Phase 3) is implemented before store and negknow (Phases 1–2) — " +
			"closing note 2: a checkpoint with no store behind it can only summarize a summary")
	}
}

// TestGuard_SubmodularInertWithoutPSelection is the code half of closing note 3.
//
// errors.Is pins the REASON rather than merely "an error", so this cannot start passing because
// the ship-order check below fired instead: the two refusals mean different things and only one
// of them is §13 invariant 4.
func TestGuard_SubmodularInertWithoutPSelection(t *testing.T) {
	t.Parallel()

	_, err := analyzer.NewSelector(100, []analyzer.Block{{ID: dag.NodeID("b"), Pos: 99}},
		dag.Slice{}, nil, guardLambda, true)
	require.ErrorIs(t, err, core.ErrBudget,
		"NewSelector must refuse a block with Pos < p (§13 invariant 4)")
}

// TestGuard_SelectorRefusesWithoutPSelection is the ship-order half of closing note 3: while the
// scheduler has no p-selection, a selector must refuse outright rather than quietly select over a
// prefix boundary nobody computed.
func TestGuard_SelectorRefusesWithoutPSelection(t *testing.T) {
	t.Parallel()

	// Once p-selection exists this guard has nothing left to assert — SP-12 owns the behaviour
	// from that point. Returning rather than t.Skip is deliberate: only two skip reasons are
	// permitted repo-wide (Rules W-1 and W-2) and neither describes this, and a guard that has
	// become inapplicable is not a skipped test, it is a satisfied precondition.
	if scheduler.PSelectionAvailable() {
		return
	}
	_, err := analyzer.NewSelector(100, []analyzer.Block{{ID: dag.NodeID("b"), Pos: 200}},
		dag.Slice{}, nil, guardLambda, true)
	require.ErrorIs(t, err, core.ErrNotImplemented,
		"NewSelector must refuse while PSelectionAvailable() is false (closing note 3)")
}

// guardLambda is an arbitrary well-formed λ; the guards assert refusals that do not depend on it.
const guardLambda = 0.4 //nomagic:allow arbitrary well-formed input to a call expected to be refused

// The four closing-note priorities, encoded as tests rather than left as intentions.
//
// Each guard states a build-order rule that is cheap to violate accidentally — by landing a
// downstream package before its prerequisite, or by flipping a default that was meant to stay off
// until its owner merges. A rule that only lives in prose gets broken by the first person who has
// not read the prose.

// expectedMaxResidualTokens is the §8.5 forced-full-pass threshold. It is asserted as a literal
// here on purpose: the point of this guard is that the SHIPPED default is exactly this number, so
// reading it back from config.Defaults() would make the assertion tautological.
const expectedMaxResidualTokens = 20000 //nomagic:allow guard asserts the shipped default literally

// TestGuard_O1FlagDefaults is closing note 4: the incremental-span instruction is on by default.
//
// O1 is the cheapest large win in the design — narrowing the summarizer to the span after the
// checkpoint frontier — and it is only a win if it is on. Shipping it defaulted off "for safety"
// would silently reduce the plugin to the behaviour it was built to improve on.
func TestGuard_O1FlagDefaults(t *testing.T) {
	t.Parallel()

	d := config.Defaults()
	require.True(t, d.Checkpoint.IncrementalSpanInstruction,
		"checkpoint.incrementalSpanInstruction must default to true (closing note 4)")
	require.True(t, d.Checkpoint.Frontier.AdvanceOnSegmentClose,
		"checkpoint.frontier.advanceOnSegmentClose must default to true (§8.5)")
	require.Equal(t, expectedMaxResidualTokens, d.Checkpoint.Frontier.MaxResidualTokens,
		"checkpoint.frontier.maxResidualTokens must default to %d (§8.5)", expectedMaxResidualTokens)
}

// TestGuard_SubmodularDefaultsOff is the half of closing note 3 that needs no analyzer.
//
// The full guard also asserts NewSelector refuses a block positioned before p; that assertion
// lives with the other analyzer-dependent guards. This half stands alone because a default can be
// flipped by a one-line config edit, with no code change to review.
func TestGuard_SubmodularDefaultsOff(t *testing.T) {
	t.Parallel()

	d := config.Defaults()
	require.False(t, d.Runtime.Selection.SubmodularEnabled,
		"runtime.selection.submodularEnabled must default to false until SP-12 merges (closing note 3)")
	require.False(t, d.Selection.Submodular.Enabled,
		"the derived SelectionCfg.Submodular.Enabled must follow the runtime key")
}

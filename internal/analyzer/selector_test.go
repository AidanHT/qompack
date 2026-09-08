package analyzer_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/analyzer/analyzertest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// The compaction point and the lambda/lazy settings every case below shares. None duplicates a
// config default, so the fixtures stay readable without tripping D11 even if this file ever
// stopped being a _test.go file.
const (
	fixtureP      = 100
	fixtureLambda = 0.5
)

// Every constructor case below is written to hold in BOTH builds — before and after SP-12 flips
// scheduler.PSelectionAvailable() — so that none of them needs a t.Skip. The only skip reason
// permitted anywhere in this subplan's tree is Rule W-1's, and a constructor guard SP-01
// implements for real is never skipped (§14.1 rule 3 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md).
//
// The BEHAVIOUR cases below open the ship-order gate themselves. That is not a way around the
// guard: SP-12 owns the gate and daemon.NewSchedulerRuntime is what opens it in production, so a
// unit test of the selection behind it has to stand in for the runtime. Every such case opens the
// gate through withPSelection, which registers the matching DisablePSelection as cleanup, so the
// process-wide flag is back to its shipped default before the next test observes it. None of the
// cases in this file calls t.Parallel(), which is what makes that sequencing sound.

// withPSelection opens the SP-12 ship-order gate for the duration of one test and closes it again
// afterwards, so a build in which submodular selection is not yet enabled still runs — and still
// passes — every behaviour case in this file.
func withPSelection(t *testing.T) {
	t.Helper()
	scheduler.EnablePSelection()
	t.Cleanup(scheduler.DisablePSelection)
}

// TestNewSelector_RefusesABlockBeforeP is §13 invariant 4: nothing scattered before p. The
// candidate set is filtered in the constructor, so a pre-p block cannot even be handed to a
// Selector, let alone selected by one. This holds regardless of the ship-order gate.
func TestNewSelector_RefusesABlockBeforeP(t *testing.T) {
	for _, pos := range []int{0, 1, fixtureP - 1} {
		blocks := []analyzer.Block{{ID: dag.NodeID("tooluse:early"), Pos: pos}}

		sel, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
		require.Nil(t, sel, "a refused construction must not hand back a usable Selector")
		require.ErrorIs(t, err, core.ErrBudget, "a block before p is core.ErrBudget (pos=%d)", pos)
		require.Contains(t, err.Error(), "tooluse:early", "the error must name the offending block")
		require.Contains(t, err.Error(), "invariant 4", "the error must name the invariant it enforces")
	}
}

// TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck pins the normative ORDER of the two guards.
// A candidate set containing one legal and one pre-p block must report the invariant-4 error, so
// that the reason a caller sees is the structural one rather than the temporary ship-order one —
// and must keep reporting it after SP-12 opens the gate.
func TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck(t *testing.T) {
	blocks := []analyzer.Block{
		{ID: dag.NodeID("tooluse:late"), Pos: fixtureP + 1},
		{ID: dag.NodeID("tooluse:early"), Pos: fixtureP - 1},
	}

	_, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
	require.ErrorIs(t, err, core.ErrBudget)
	require.False(t, core.IsNotImplemented(err),
		"the ship-order error must never mask the §13 invariant 4 error")
}

// TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet is the closing note's priority 3: do
// not ship submodular selection before p-selection. With every block legal, exactly two outcomes
// are permitted, and which one applies is decided solely by scheduler.PSelectionAvailable().
func TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet(t *testing.T) {
	blocks := []analyzer.Block{
		{ID: dag.NodeID("tooluse:at-p"), Pos: fixtureP},
		{ID: dag.NodeID("tooluse:after-p"), Pos: fixtureP + 1},
	}

	sel, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)

	if scheduler.PSelectionAvailable() {
		require.NoError(t, err, "with p-selection available, a legal candidate set constructs")
		require.NotNil(t, sel)
		require.Equal(t, fixtureP, sel.P(), "a Selector reports the p it was constructed with")
		return
	}

	require.Nil(t, sel)
	require.True(t, core.IsNotImplemented(err), "the ship-order gate reports core.ErrNotImplemented")
	require.Contains(t, err.Error(), "p-selection", "the error must name what is missing")
}

// TestNewSelector_PosEqualToPIsLegal pins the boundary: the rule is "before p", so a block AT p
// is a candidate. Asserting only that the error is NOT ErrBudget keeps the case meaningful in
// both builds.
func TestNewSelector_PosEqualToPIsLegal(t *testing.T) {
	blocks := []analyzer.Block{{ID: dag.NodeID("tooluse:at-p"), Pos: fixtureP}}

	_, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
	require.NotErrorIs(t, err, core.ErrBudget, "a block exactly at p is not before p")
}

// TestNewSelector_EmptyCandidateSetStillConsultsTheShipOrderGate asserts the guards are not
// short-circuited by an empty candidate set: with nothing to filter, the ship-order gate is still
// consulted rather than skipped.
func TestNewSelector_EmptyCandidateSetStillConsultsTheShipOrderGate(t *testing.T) {
	sel, err := analyzer.NewSelector(fixtureP, nil, dag.Slice{}, nil, fixtureLambda, false)

	if scheduler.PSelectionAvailable() {
		require.NoError(t, err)
		require.NotNil(t, sel)
		return
	}
	require.True(t, core.IsNotImplemented(err))
}

// TestSelector_SelectIsRealBehindTheGate documents the split §14.1 rule 3 draws through this
// package, now that SP-15 has landed the selection itself: the CONSTRUCTOR is real in every
// build, and behind an open gate so is Select. Written so that it asserts the ship-order refusal
// in a build where construction is refused, and a real Selection in a build where it succeeds.
func TestSelector_SelectIsRealBehindTheGate(t *testing.T) {
	sel, err := analyzer.NewSelector(fixtureP, nil, dag.Slice{}, nil, fixtureLambda, false)
	if err != nil {
		require.True(t, core.IsNotImplemented(err))
		return
	}
	got, err := sel.Select(context.Background(), core.Tokens(1000))
	require.NoError(t, err, "behind an open gate, Select is no longer a stub")
	require.Empty(t, got.Keep, "an empty candidate set keeps nothing")
}

// ----------------------------------------------------------------------------------------------
// Block-level selection behaviour.
// ----------------------------------------------------------------------------------------------

// selRoot mints a content root from a short label, so a fixture can say "these two blocks hold
// the same bytes" without carrying 64 hex characters around.
func selRoot(label string) core.Hash { return core.HashBytes(core.DomainRoot, []byte(label)) }

// selBlocks is the candidate set most behaviour cases below run on: four independently rooted
// blocks with different prices, one of them superseded so the redundancy term has something to
// bite on.
func selBlocks() []analyzer.Block {
	return []analyzer.Block{
		{ID: "tooluse:a", Pos: fixtureP, Tokens: 90, Root: selRoot("a")},
		{ID: "tooluse:b", Pos: fixtureP + 1, Tokens: 140, Root: selRoot("b")},
		{ID: "tooluse:c", Pos: fixtureP + 2, Tokens: 70, Root: selRoot("c")},
		{ID: "tooluse:d", Pos: fixtureP + 3, Tokens: 160, Root: selRoot("d"), Superseded: true},
	}
}

// selDelta scores every selBlocks entry.
func selDelta() map[dag.NodeID]float64 {
	return map[dag.NodeID]float64{
		"tooluse:a": 0.85,
		"tooluse:b": 0.72,
		"tooluse:c": 0.61,
		"tooluse:d": 0.33,
	}
}

// selSelector constructs a Selector over blocks behind an already-open gate.
func selSelector(t *testing.T, blocks []analyzer.Block, delta map[dag.NodeID]float64,
	lazy bool,
) analyzer.Selector {
	t.Helper()
	sel, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, delta, fixtureLambda, lazy)
	require.NoError(t, err)
	require.NotNil(t, sel)
	return sel
}

// TestSelect_NeverExceedsItsBudgetAndReportsTheTrueTotal asserts the hard constraint and the
// honesty of the number that reports it: Tokens is the sum over the blocks actually kept, not a
// counter maintained beside them that a skipped path could leave stale.
func TestSelect_NeverExceedsItsBudgetAndReportsTheTrueTotal(t *testing.T) {
	withPSelection(t)
	blocks := selBlocks()
	sel := selSelector(t, blocks, selDelta(), true)

	byID := make(map[dag.NodeID]core.Tokens, len(blocks))
	for _, b := range blocks {
		byID[b.ID] = b.Tokens
	}

	for _, budget := range []core.Tokens{500, 200, 95, 1} {
		got, err := sel.Select(context.Background(), budget)
		require.NoError(t, err)
		require.LessOrEqual(t, int(got.Tokens), int(budget), "budget=%d", budget)

		var sum core.Tokens
		for _, id := range got.Keep {
			sum += byID[id]
		}
		require.Equal(t, sum, got.Tokens, "Tokens must be the sum over Keep (budget=%d)", budget)
		require.GreaterOrEqual(t, got.Value, 0.0,
			"a walk that only accepts positive gains can never report a negative value")
	}
}

// TestSelect_KeepAndDroppedPartitionTheCandidateSet is what makes /qompack:dropped able to be
// honest about what was lost (G4.5): every candidate is accounted for exactly once.
func TestSelect_KeepAndDroppedPartitionTheCandidateSet(t *testing.T) {
	withPSelection(t)
	blocks := selBlocks()
	sel := selSelector(t, blocks, selDelta(), true)

	got, err := sel.Select(context.Background(), core.Tokens(200))
	require.NoError(t, err)

	all := make([]dag.NodeID, 0, len(blocks))
	for _, b := range blocks {
		all = append(all, b.ID)
	}
	require.ElementsMatch(t, all, append(append([]dag.NodeID{}, got.Keep...), got.Dropped...))
	require.NotEmpty(t, got.Keep, "a 200-token budget admits something")
}

// TestSelect_ZeroBudgetKeepsNothing is the degenerate case a real greedy loop must not fall
// through: nothing is kept, nothing is spent, and everything is reported as dropped rather than
// quietly disappearing.
func TestSelect_ZeroBudgetKeepsNothing(t *testing.T) {
	withPSelection(t)
	blocks := selBlocks()
	sel := selSelector(t, blocks, selDelta(), true)

	got, err := sel.Select(context.Background(), core.Tokens(0))
	require.NoError(t, err)
	require.Empty(t, got.Keep)
	require.Zero(t, int(got.Tokens))
	require.Len(t, got.Dropped, len(blocks))
	require.Greater(t, got.Iters, 0, "the walk still priced every candidate before refusing it")
}

// TestSelect_IsDeterministicAcrossCalls asserts two Selects with the same budget agree exactly,
// ORDER included. Selection feeds a replay metric, so a nondeterministic tiebreak would make
// FractionOfOPT incomparable between commits.
func TestSelect_IsDeterministicAcrossCalls(t *testing.T) {
	withPSelection(t)
	sel := selSelector(t, selBlocks(), selDelta(), true)
	ctx := context.Background()

	first, err := sel.Select(ctx, core.Tokens(200))
	require.NoError(t, err)
	second, err := sel.Select(ctx, core.Tokens(200))
	require.NoError(t, err)
	require.Equal(t, first, second, "Select must be deterministic, tiebreaks included")

	// A second Selector built from the same inputs must agree with the first: state left behind
	// on the receiver would show up here and nowhere else.
	other := selSelector(t, selBlocks(), selDelta(), true)
	third, err := other.Select(ctx, core.Tokens(200))
	require.NoError(t, err)
	require.Equal(t, first, third)
}

// TestSelect_NothingBeforePIsEverKept is §13 invariant 4 asserted on the OUTPUT side. The
// constructor already makes a pre-p candidate impossible to hand over, so this pins that the
// selection cannot invent one either.
func TestSelect_NothingBeforePIsEverKept(t *testing.T) {
	withPSelection(t)
	blocks := selBlocks()
	sel := selSelector(t, blocks, selDelta(), true)

	got, err := sel.Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)

	pos := make(map[dag.NodeID]int, len(blocks))
	for _, b := range blocks {
		pos[b.ID] = b.Pos
	}
	for _, id := range got.Keep {
		require.GreaterOrEqual(t, pos[id], sel.P(), "kept block %s precedes p", string(id))
	}
}

// TestSelect_CoverageSaturatesWithinAContentRootGroup is the submodular half of the objective:
// two blocks holding the SAME bytes cover the same thing, so the second one buys almost nothing
// and pays the redundancy penalty on top. A modular objective would keep both and report twice
// the value for content the reader sees once.
func TestSelect_CoverageSaturatesWithinAContentRootGroup(t *testing.T) {
	withPSelection(t)
	shared := selRoot("shared")
	blocks := []analyzer.Block{
		{ID: "tooluse:one", Pos: fixtureP, Tokens: 40, Root: shared},
		{ID: "tooluse:two", Pos: fixtureP + 1, Tokens: 40, Root: shared},
		{ID: "tooluse:three", Pos: fixtureP + 2, Tokens: 40, Root: selRoot("other")},
	}
	delta := map[dag.NodeID]float64{"tooluse:one": 1.0, "tooluse:two": 1.0, "tooluse:three": 0.4}

	got, err := selSelector(t, blocks, delta, true).Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)
	require.Contains(t, got.Keep, dag.NodeID("tooluse:one"))
	require.Contains(t, got.Keep, dag.NodeID("tooluse:three"))
	require.NotContains(t, got.Keep, dag.NodeID("tooluse:two"),
		"a second delivery of an already-covered content root adds no coverage and costs lambda")
}

// TestSelect_AnUnscoredBlockIsNeverKept pins the deliberate choice made in newGreedySelector: a
// block nobody measured has no measured worth. Inventing a default for it would let unmeasured
// content displace content that was actually shown to matter.
func TestSelect_AnUnscoredBlockIsNeverKept(t *testing.T) {
	withPSelection(t)
	blocks := []analyzer.Block{
		{ID: "tooluse:scored", Pos: fixtureP, Tokens: 10, Root: selRoot("s")},
		{ID: "tooluse:unscored", Pos: fixtureP, Tokens: 10, Root: selRoot("u")},
	}
	delta := map[dag.NodeID]float64{"tooluse:scored": 0.7}

	got, err := selSelector(t, blocks, delta, true).Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)
	require.Equal(t, []dag.NodeID{"tooluse:scored"}, got.Keep)
	require.Equal(t, []dag.NodeID{"tooluse:unscored"}, got.Dropped)
}

// TestSelect_TheSliceScoreIsTheFallbackForAnUnscoredBlock: the dependence slice's own relevance
// score stands in when no Δ-score was supplied, which is what lets a caller that has run a slice
// but not a scorer still select something.
func TestSelect_TheSliceScoreIsTheFallbackForAnUnscoredBlock(t *testing.T) {
	withPSelection(t)
	blocks := []analyzer.Block{{ID: "tooluse:sliced", Pos: fixtureP, Tokens: 10, Root: selRoot("s")}}
	slice := dag.Slice{Scores: map[dag.NodeID]float32{"tooluse:sliced": 0.6}}

	sel, err := analyzer.NewSelector(fixtureP, blocks, slice, nil, fixtureLambda, true)
	require.NoError(t, err)

	got, err := sel.Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)
	require.Equal(t, []dag.NodeID{"tooluse:sliced"}, got.Keep)
	// float32 -> float64 is exact but 0.6 is not representable in either, so the tolerance is the
	// float32 mantissa's, not an admission that the arithmetic is approximate.
	require.InDelta(t, 0.6, got.Value, 1e-6)
}

// TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer is the evidence behind
// Selection.Iters. The lazy walk and the eager one must agree on WHAT they select — the stale
// upper bound is a pruning device, not a different algorithm — while the lazy one prices strictly
// fewer moves. Asserting only that Iters is non-zero would let a walk that prunes nothing pass.
func TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer(t *testing.T) {
	withPSelection(t)
	shared := selRoot("shared")
	blocks := []analyzer.Block{
		{ID: "tooluse:1", Pos: fixtureP, Tokens: 20, Root: shared},
		{ID: "tooluse:2", Pos: fixtureP, Tokens: 20, Root: shared},
		{ID: "tooluse:3", Pos: fixtureP, Tokens: 20, Root: selRoot("r3")},
		{ID: "tooluse:4", Pos: fixtureP, Tokens: 20, Root: selRoot("r4")},
		{ID: "tooluse:5", Pos: fixtureP, Tokens: 20, Root: selRoot("r5")},
		{ID: "tooluse:6", Pos: fixtureP, Tokens: 20, Root: selRoot("r6")},
	}
	delta := map[dag.NodeID]float64{
		"tooluse:1": 0.9, "tooluse:2": 0.8, "tooluse:3": 0.7,
		"tooluse:4": 0.6, "tooluse:5": 0.5, "tooluse:6": 0.45,
	}

	lazy, err := selSelector(t, blocks, delta, true).Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)
	eager, err := selSelector(t, blocks, delta, false).Select(context.Background(), core.Tokens(500))
	require.NoError(t, err)

	require.Equal(t, eager.Keep, lazy.Keep, "lazy pruning must not change what is selected")
	require.Equal(t, eager.Tokens, lazy.Tokens)
	require.InDelta(t, eager.Value, lazy.Value, 1e-9)
	require.Less(t, lazy.Iters, eager.Iters,
		"lazy=%d eager=%d: the stale-bound queue must actually skip evaluations", lazy.Iters, eager.Iters)
	t.Logf("marginal-gain evaluations: lazy=%d eager=%d", lazy.Iters, eager.Iters)
}

// TestSelect_CancelledContextReportsErrBudget: only the four sentinels are legal error values
// (00-ARCHITECTURE.md §5.22), and a cancelled context is a latency budget expiring — so it must
// arrive as core.ErrBudget while still wrapping the cancellation a caller may want to branch on.
func TestSelect_CancelledContextReportsErrBudget(t *testing.T) {
	withPSelection(t)
	sel := selSelector(t, selBlocks(), selDelta(), true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sel.Select(ctx, core.Tokens(500))
	require.ErrorIs(t, err, core.ErrBudget)
	require.ErrorIs(t, err, context.Canceled)
}

// TestAnalyzerSuite_SelectorBehaviourRunsAgainstTheRealSelector points analyzertest's own
// conformance suite at the landed implementation with the ship-order gate open, which is what
// turns its /behaviour block from a Rule W-1 skip into executed assertions. The suite is used
// UNCHANGED — §5.22 forbids editing a suite to make a block pass — and the factory binds exactly
// the three arguments analyzertest may not name itself (§3.2).
func TestAnalyzerSuite_SelectorBehaviourRunsAgainstTheRealSelector(t *testing.T) {
	withPSelection(t)
	cfg := config.Defaults()

	analyzertest.RunSelectorSuite(t, "analyzer.NewSelector", func(t *testing.T) analyzertest.NewSelectorFunc {
		t.Helper()
		return func(p int, blocks []analyzer.Block, delta map[string]float64) (analyzer.Selector, error) {
			d := make(map[dag.NodeID]float64, len(delta))
			for k, v := range delta {
				d[dag.NodeID(k)] = v
			}
			return analyzer.NewSelector(p, blocks, dag.Slice{}, d,
				cfg.Selection.Submodular.Lambda, cfg.Selection.Submodular.LazyGreedy)
		}
	})
}

// ----------------------------------------------------------------------------------------------
// Representation selection — Propose.
// ----------------------------------------------------------------------------------------------

// propRep builds one representation of item.
func propRep(item dag.NodeID, kind analyzer.RepresentationKind, cov float64, cost core.Tokens,
	root string, qual analyzer.Qualification, requires ...dag.NodeID,
) analyzer.Representation {
	var h core.Hash
	if root != "" {
		h = selRoot(root)
	}
	return analyzer.Representation{
		Item:          item,
		Kind:          kind,
		Coverage:      cov,
		AssembledCost: cost,
		Requires:      requires,
		Prov: analyzer.Provenance{
			Origin:        item,
			Root:          h,
			Qualification: qual,
		},
	}
}

// TestPropose_RunsTheConstructorPosGuard: Propose routes through NewSelector, so a candidate
// positioned before p refuses the whole call with core.ErrBudget rather than being quietly
// filtered out — a caller assembling candidates against the wrong compaction point has to hear
// about it.
func TestPropose_RunsTheConstructorPosGuard(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{{
		Item: "item:early", Pos: fixtureP - 1, Weight: 1,
		Reps: []analyzer.Representation{
			propRep("item:early", analyzer.RepExactSpan, 1, 10, "e", analyzer.QualCurrent),
		},
	}}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(500))
	require.ErrorIs(t, err, core.ErrBudget)
	require.Contains(t, err.Error(), "invariant 4")
	require.Equal(t, analyzer.Proposal{}, got, "a refused call hands back no proposal")
}

// TestPropose_RunsTheShipOrderGuard is the other half: with the gate closed Propose reports
// core.ErrNotImplemented, exactly as NewSelector does, because it is the same check. Deliberately
// does NOT call withPSelection.
func TestPropose_RunsTheShipOrderGuard(t *testing.T) {
	if scheduler.PSelectionAvailable() {
		// The gate is process-wide and SP-12 owns it; if some earlier construction opened it for
		// good, the guard this case describes has no closed build left to observe. Returning
		// rather than skipping: only Rules W-1 and W-2 may skip, and neither describes this.
		return
	}
	_, err := analyzer.Propose(context.Background(), fixtureP, nil, fixtureLambda, core.Tokens(500))
	require.True(t, core.IsNotImplemented(err))
	require.Contains(t, err.Error(), "p-selection")
}

// TestPropose_ChoosesAtMostOneRepresentationPerItem is feasibility rule 1. Delivering both the
// exact span and the capsule of one item would double-count its coverage and pay for it twice.
func TestPropose_ChoosesAtMostOneRepresentationPerItem(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{{
		Item: "item:a", Pos: fixtureP, Weight: 1,
		Reps: []analyzer.Representation{
			propRep("item:a", analyzer.RepExactSpan, 1.0, 100, "a", analyzer.QualCurrent),
			propRep("item:a", analyzer.RepCapsule, 0.6, 30, "a", analyzer.QualCurrent),
			propRep("item:a", analyzer.RepPointer, 0.2, 5, "a", analyzer.QualCurrent),
		},
	}}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(500))
	require.NoError(t, err)
	require.Len(t, got.Chosen, 1)
	require.Equal(t, analyzer.RepExactSpan, got.Chosen[0].Kind, "the whole budget buys the faithful form")
	require.Equal(t, core.Tokens(100), got.Tokens)
	require.False(t, got.Overflow)
}

// TestPropose_CarriesTheDependencyClosure is feasibility rule 2: a representation is delivered
// only together with every item it requires. A dependent delivered without its dependency is a
// record that references content the reader cannot see.
func TestPropose_CarriesTheDependencyClosure(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:base", Pos: fixtureP, Weight: 0.1,
			Reps: []analyzer.Representation{
				propRep("item:base", analyzer.RepExactSpan, 1.0, 40, "base", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:dependent", Pos: fixtureP + 1, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:dependent", analyzer.RepExactSpan, 1.0, 50, "dep", analyzer.QualCurrent,
					"item:base", "item:absent"),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(500))
	require.NoError(t, err)

	chosen := make(map[dag.NodeID]bool, len(got.Chosen))
	for _, r := range got.Chosen {
		chosen[r.Item] = true
	}
	require.True(t, chosen["item:dependent"])
	require.True(t, chosen["item:base"], "a required candidate must be carried with its dependent")
	require.Equal(t, core.Tokens(90), got.Tokens)

	// "item:absent" names something that is not in the candidate set at all. Contract §3 reads
	// that as already present in the prefix, which is the only reading under which a candidate set
	// assembled from a suffix can ever be feasible.
	require.False(t, chosen["item:absent"])
}

// TestPropose_ADependentIsRefusedWhenItsDependencyCannotBeDelivered: the other side of rule 2.
// An item whose only representation is archive-only delivers nothing, so anything requiring it is
// infeasible at every price and must never be chosen.
func TestPropose_ADependentIsRefusedWhenItsDependencyCannotBeDelivered(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:base", Pos: fixtureP, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:base", analyzer.RepArchiveOnly, 0, 0, "base", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:dependent", Pos: fixtureP + 1, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:dependent", analyzer.RepExactSpan, 1.0, 50, "dep", analyzer.QualCurrent,
					"item:base"),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(500))
	require.NoError(t, err)
	require.Empty(t, got.Chosen)
	require.Equal(t, []dag.NodeID{"item:base"}, got.Archive,
		"an archive-only item is reported as recoverable, never silently dropped")
	require.False(t, got.Overflow, "no MANDATORY record failed, so this is not an overflow")
}

// TestPropose_ZeroBudgetChoosesNothingAndDoesNotOverflow is contract §3's degenerate case: a zero
// budget yields Chosen == nil, and Overflow only if a Mandatory candidate existed.
func TestPropose_ZeroBudgetChoosesNothingAndDoesNotOverflow(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{{
		Item: "item:a", Pos: fixtureP, Weight: 1,
		Reps: []analyzer.Representation{
			propRep("item:a", analyzer.RepExactSpan, 1.0, 10, "a", analyzer.QualCurrent),
		},
	}}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(0))
	require.NoError(t, err)
	require.Nil(t, got.Chosen)
	require.Zero(t, int(got.Tokens))
	require.Zero(t, got.Value)
	require.False(t, got.Overflow)
}

// TestPropose_OverflowsRatherThanCarryingAMandatoryRecordPartially is contract §3's overflow rule.
// The proposal reports the failure, names the item and hands back the archive-recovery path; it
// never serializes the optional half of a record set whose mandatory half did not fit.
func TestPropose_OverflowsRatherThanCarryingAMandatoryRecordPartially(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:critical", Pos: fixtureP, Weight: 1, Mandatory: true,
			Reps: []analyzer.Representation{
				propRep("item:critical", analyzer.RepExactSpan, 1.0, 200, "crit", analyzer.QualCurrent),
				propRep("item:critical", analyzer.RepPointer, 0.2, 40, "crit", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:optional", Pos: fixtureP + 1, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:optional", analyzer.RepExactSpan, 1.0, 20, "opt", analyzer.QualCurrent),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(30))
	require.NoError(t, err, "an overflow is an outcome, not an error")
	require.True(t, got.Overflow)
	require.Contains(t, got.Reason, "item:critical", "Reason must name the overflowing item")
	require.Equal(t, []dag.NodeID{"item:critical"}, got.Archive)
	require.Nil(t, got.Chosen, "never a partial serialization")
	require.Zero(t, int(got.Tokens))

	// The pointer is what "including at RepPointer" means: raise the budget past the cheapest
	// qualified representation and the same candidate set stops overflowing.
	got, err = analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(60))
	require.NoError(t, err)
	require.False(t, got.Overflow)
	require.Len(t, got.Chosen, 2)
}

// TestPropose_MandatoryRoomIsReservedBeforeOptionalItemsAreBought: the mandatory record is carried
// even though its own marginal gain is smaller than the optional item's. A greedy that spent first
// and checked afterwards would report an overflow it caused itself.
func TestPropose_MandatoryRoomIsReservedBeforeOptionalItemsAreBought(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:elimination", Pos: fixtureP, Weight: 0, Mandatory: true,
			Reps: []analyzer.Representation{
				propRep("item:elimination", analyzer.RepCapsule, 1.0, 30, "elim", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:rich", Pos: fixtureP + 1, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:rich", analyzer.RepExactSpan, 1.0, 80, "rich", analyzer.QualCurrent),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(100))
	require.NoError(t, err)
	require.False(t, got.Overflow)
	require.Len(t, got.Chosen, 1)
	require.Equal(t, dag.NodeID("item:elimination"), got.Chosen[0].Item,
		"a zero-weight mandatory record has no marginal gain, and is carried anyway")
	require.Equal(t, core.Tokens(30), got.Tokens)
}

// TestPropose_AStaleMandatoryCandidateIsNeverPromotedToAConstraint is G6.3's other half. A
// Mandatory candidate whose evidence is only QualStale or QualUncertain keeps its qualification,
// is selected on its merits, and never forces an overflow: a false "already tried" that blocks a
// now-viable approach is what turns negative knowledge from asset to liability (§12, High).
func TestPropose_AStaleMandatoryCandidateIsNeverPromotedToAConstraint(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:stale", Pos: fixtureP, Weight: 1, Mandatory: true,
			Reps: []analyzer.Representation{
				propRep("item:stale", analyzer.RepExactSpan, 1.0, 400, "stale", analyzer.QualStale),
			},
		},
		{
			Item: "item:uncertain", Pos: fixtureP + 1, Weight: 1, Mandatory: true,
			Reps: []analyzer.Representation{
				propRep("item:uncertain", analyzer.RepExactSpan, 1.0, 400, "unc", analyzer.QualUncertain),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(50))
	require.NoError(t, err)
	require.False(t, got.Overflow,
		"stale and uncertain evidence never binds, so it can never force an overflow")
	require.Empty(t, got.Chosen, "neither fits, and neither is mandatory enough to make room for")
}

// TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence is G6.3's first half. A
// Mandatory candidate whose provenance is QualCurrent binds, and it is carried at a representation
// whose OWN provenance is current: delivering the constraint at stale evidence would hand the
// consumer something it must correctly decline to treat as binding, losing the constraint in
// transit while the proposal reported success.
func TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{{
		Item: "item:elimination", Pos: fixtureP, Weight: 0.1, Mandatory: true,
		Reps: []analyzer.Representation{
			propRep("item:elimination", analyzer.RepCapsule, 0.9, 5, "cheap-but-stale", analyzer.QualStale),
			propRep("item:elimination", analyzer.RepExactSpan, 1.0, 60, "current", analyzer.QualCurrent),
		},
	}}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(100))
	require.NoError(t, err)
	require.Len(t, got.Chosen, 1)
	require.Equal(t, analyzer.QualCurrent, got.Chosen[0].Prov.Qualification,
		"a binding constraint is only ever carried at active evidence")
	require.Equal(t, core.Tokens(60), got.Tokens)
}

// TestPropose_RedundantEvidenceIsPricedNotDoubleCounted: two representations that derive from the
// SAME original evidence root cover the same observation, so the second pays lambda without adding
// coverage. Prov.Root names the first observation rather than a derivative's own bytes, which is
// what makes two different capsules of one tool result recognizable as one duplicate.
func TestPropose_RedundantEvidenceIsPricedNotDoubleCounted(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{
		{
			Item: "item:one", Pos: fixtureP, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:one", analyzer.RepCapsule, 1.0, 10, "same-evidence", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:two", Pos: fixtureP + 1, Weight: 0.2,
			Reps: []analyzer.Representation{
				propRep("item:two", analyzer.RepCapsule, 1.0, 10, "same-evidence", analyzer.QualCurrent),
			},
		},
	}

	got, err := analyzer.Propose(context.Background(), fixtureP, cands, 0.5, core.Tokens(500))
	require.NoError(t, err)
	require.Len(t, got.Chosen, 1, "the second derivative of one observation gains 0.2 and costs 0.5")
	require.Equal(t, dag.NodeID("item:one"), got.Chosen[0].Item)
}

// TestPropose_IsDeterministic asserts contract §3's determinism clause, Iters included, and
// asserts it against a SHUFFLED input: the solver sorts candidates and representations itself, so
// two callers that assembled the same set in different orders must get the same proposal.
func TestPropose_IsDeterministic(t *testing.T) {
	withPSelection(t)
	forward := []analyzer.Candidate{
		{
			Item: "item:a", Pos: fixtureP, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:a", analyzer.RepExactSpan, 1.0, 60, "a", analyzer.QualCurrent),
				propRep("item:a", analyzer.RepCapsule, 0.5, 20, "a", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:b", Pos: fixtureP + 1, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:b", analyzer.RepExactSpan, 1.0, 60, "b", analyzer.QualCurrent),
			},
		},
		{
			Item: "item:c", Pos: fixtureP + 2, Weight: 1,
			Reps: []analyzer.Representation{
				propRep("item:c", analyzer.RepExactSpan, 1.0, 60, "c", analyzer.QualCurrent),
			},
		},
	}
	reversed := []analyzer.Candidate{forward[2], forward[1], {
		Item: forward[0].Item, Pos: forward[0].Pos, Weight: forward[0].Weight,
		Reps: []analyzer.Representation{forward[0].Reps[1], forward[0].Reps[0]},
	}}

	ctx := context.Background()
	first, err := analyzer.Propose(ctx, fixtureP, forward, fixtureLambda, core.Tokens(150))
	require.NoError(t, err)
	second, err := analyzer.Propose(ctx, fixtureP, forward, fixtureLambda, core.Tokens(150))
	require.NoError(t, err)
	shuffled, err := analyzer.Propose(ctx, fixtureP, reversed, fixtureLambda, core.Tokens(150))
	require.NoError(t, err)

	require.Equal(t, first, second, "two Proposes on equal inputs must agree, Iters included")
	require.Equal(t, first, shuffled, "input order must not reach the result")
	require.Greater(t, first.Iters, 0)
}

// TestPropose_DoesNotMutateTheCallersCandidateSet: the daemon assembles a candidate set once and
// may propose over it more than once, so a solver that sorted the caller's slices in place would
// make the second call's input different from the first's.
func TestPropose_DoesNotMutateTheCallersCandidateSet(t *testing.T) {
	withPSelection(t)
	cands := []analyzer.Candidate{{
		Item: "item:a", Pos: fixtureP, Weight: 1,
		Reps: []analyzer.Representation{
			propRep("item:a", analyzer.RepCapsule, 0.5, 20, "a", analyzer.QualCurrent, "item:z", "item:b"),
			propRep("item:a", analyzer.RepExactSpan, 1.0, 60, "a", analyzer.QualCurrent),
		},
	}}
	before := []analyzer.Representation{cands[0].Reps[0], cands[0].Reps[1]}

	_, err := analyzer.Propose(context.Background(), fixtureP, cands, fixtureLambda, core.Tokens(500))
	require.NoError(t, err)
	require.Equal(t, before[0], cands[0].Reps[0], "representation order must be untouched")
	require.Equal(t, before[1], cands[0].Reps[1])
	require.Equal(t, []dag.NodeID{"item:z", "item:b"}, cands[0].Reps[0].Requires,
		"the caller's Requires slice must not be sorted in place")
}

// TestPropose_CancelledContextReportsErrBudget mirrors Select's rule: only the four sentinels are
// legal, and a cancelled context is a latency budget expiring.
func TestPropose_CancelledContextReportsErrBudget(t *testing.T) {
	withPSelection(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := analyzer.Propose(ctx, fixtureP, nil, fixtureLambda, core.Tokens(500))
	require.ErrorIs(t, err, core.ErrBudget)
	require.ErrorIs(t, err, context.Canceled)
}

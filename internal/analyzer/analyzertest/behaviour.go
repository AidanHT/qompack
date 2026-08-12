package analyzertest

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// This file authors the assertions of the analyzertest suites (§15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): the two NewSelector constructor guards —
// which run in every build, because SP-01 implements them for real — plus the selection, scoring
// and redundancy behaviour SP-15 inherits behind the Rule W-1 probe.

// Fixture constants. Values are deliberately off the D11/§11.6 forbidden-literal set, since this
// is not a _test.go file and nomagic therefore applies to it.
const (
	// probeP is the compaction point every fixture candidate set is built around.
	probeP = 100
	// probeBudget is a budget large enough to admit every fixture block.
	probeBudget = core.Tokens(500)
	// zeroBudget admits nothing.
	zeroBudget = core.Tokens(0)
)

// The fixture block ids. They are dag.NodeID values, but this package may not import dag (§3.2),
// so they are written as untyped string constants — which Go converts implicitly at the field
// assignment — and read back with a string() conversion, which needs no import either.
const (
	blockAtP     = "tooluse:at-p"
	blockAfterP  = "tooluse:after-p"
	blockLater   = "tooluse:later"
	blockLatest  = "tooluse:latest"
	blockBeforeP = "tooluse:before-p"
)

// legalBlocks is a candidate set every block of which sits at or after probeP, so it passes the
// §13 invariant 4 filter. Token costs differ so a budget can bind on some of them.
func legalBlocks() []analyzer.Block {
	return []analyzer.Block{
		{ID: blockAtP, Pos: probeP, Tokens: core.Tokens(90)},
		{ID: blockAfterP, Pos: probeP + 1, Tokens: core.Tokens(140)},
		{ID: blockLater, Pos: probeP + 2, Tokens: core.Tokens(70), Ephemeral: true},
		{ID: blockLatest, Pos: probeP + 3, Tokens: core.Tokens(160), Superseded: true},
	}
}

// legalDelta is a Δ-score for every legalBlocks entry, keyed by the string form of its id.
func legalDelta() map[string]float64 {
	return map[string]float64{
		blockAtP:    0.85,
		blockAfterP: 0.72,
		blockLater:  0.61,
		blockLatest: 0.33,
	}
}

// coveringContinuation mentions every fixture block's path and symbol, so a token-overlap or
// symbol-reference scorer has something to find.
func coveringContinuation() analyzer.Continuation {
	return analyzer.Continuation{
		Text:     []byte("refreshToken acquires a pool connection in src/auth.ts and calls the identity provider"),
		Symbols:  []string{"refreshToken", "acquire"},
		Paths:    []string{"src/auth.ts", "src/pool.ts"},
		FromTurn: core.TurnIndex(probeP),
	}
}

// emptyContinuation observed nothing at all: no scorer can find overlap in it.
func emptyContinuation() analyzer.Continuation {
	return analyzer.Continuation{FromTurn: core.TurnIndex(probeP)}
}

// blockIDs projects blocks down to the string form of their ids, for set comparisons that would
// otherwise need to name dag.NodeID.
func blockIDs(blocks []analyzer.Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = string(b.ID)
	}
	return out
}

// nodeIDs is blockIDs' counterpart for a Selection's Keep/Dropped lists.
func nodeIDs[T ~string](ids []T) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

// tokensOf sums the token cost of the blocks named by ids.
func tokensOf(blocks []analyzer.Block, ids []string) core.Tokens {
	byID := make(map[string]core.Tokens, len(blocks))
	for _, b := range blocks {
		byID[string(b.ID)] = b.Tokens
	}
	var sum core.Tokens
	for _, id := range ids {
		sum += byID[id]
	}
	return sum
}

// runPreconditionPosCase is §13 invariant 4: nothing scattered before p. A candidate set holding
// any pre-p block is refused with core.ErrBudget, in every build, stub or real.
func runPreconditionPosCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	newSelector := factory(t)

	for _, pos := range []int{0, 1, probeP - 1} {
		blocks := append(legalBlocks(), analyzer.Block{ID: blockBeforeP, Pos: pos, Tokens: core.Tokens(10)})

		sel, err := newSelector(probeP, blocks, legalDelta())
		require.Nil(t, sel, "a refused construction must not hand back a usable Selector (pos=%d)", pos)
		require.ErrorIs(t, err, core.ErrBudget, "a block before p is core.ErrBudget (pos=%d)", pos)
	}
}

// runGuardOrderCase pins the normative ORDER of NewSelector's two guards: the Pos check runs
// first, so the structural §13 invariant 4 reason is never masked by the temporary ship-order one.
func runGuardOrderCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := append(legalBlocks(), analyzer.Block{ID: blockBeforeP, Pos: probeP - 1, Tokens: core.Tokens(10)})

	_, err := factory(t)(probeP, blocks, legalDelta())
	require.ErrorIs(t, err, core.ErrBudget)
	require.False(t, core.IsNotImplemented(err),
		"the closing-note-3 ship-order error must never mask the §13 invariant 4 error")
}

// runShipOrderGateCase is the closing note's priority 3: do not ship submodular selection before
// p-selection. Exactly two outcomes are legal for a candidate set that passes the Pos filter, and
// which one applies is decided solely by scheduler.PSelectionAvailable() — which this package may
// not call directly (§3.2), so the case asserts the disjunction instead.
func runShipOrderGateCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()

	sel, err := factory(t)(probeP, legalBlocks(), legalDelta())
	if err != nil {
		require.True(t, core.IsNotImplemented(err),
			"a legal candidate set may only be refused for want of p-selection, never for any other reason")
		require.Nil(t, sel)
		return
	}
	require.NotNil(t, sel, "a construction that reports no error must hand back a usable Selector")
	require.Equal(t, probeP, sel.P(), "a Selector reports the p it was constructed with")
}

// runPosBoundaryCase pins the boundary: the rule is "before p", so a block exactly AT p is a
// candidate, not a violation.
func runPosBoundaryCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := []analyzer.Block{{ID: blockAtP, Pos: probeP, Tokens: core.Tokens(90)}}

	_, err := factory(t)(probeP, blocks, legalDelta())
	require.NotErrorIs(t, err, core.ErrBudget, "a block exactly at p is not before p")
}

// mustSelector constructs a Selector for the behaviour cases, which only run once the ship-order
// gate is open and Select is real.
func mustSelector(t *testing.T, factory func(t *testing.T) NewSelectorFunc, blocks []analyzer.Block) analyzer.Selector {
	t.Helper()
	sel, err := factory(t)(probeP, blocks, legalDelta())
	require.NoError(t, err)
	require.NotNil(t, sel)
	return sel
}

// runBudgetCase asserts the hard constraint: a Selection never costs more than the budget it was
// asked for, and its reported Tokens is the true sum over the blocks it kept rather than an
// independently maintained counter.
func runBudgetCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := legalBlocks()
	sel := mustSelector(t, factory, blocks)

	for _, budget := range []core.Tokens{probeBudget, core.Tokens(200), core.Tokens(95)} {
		got, err := sel.Select(context.Background(), budget)
		require.NoError(t, err)
		require.LessOrEqual(t, int(got.Tokens), int(budget), "a Selection never exceeds its budget")
		require.Equal(t, tokensOf(blocks, nodeIDs(got.Keep)), got.Tokens,
			"Selection.Tokens must be the sum over the blocks actually kept")
	}
}

// runPartitionCase asserts Keep and Dropped are a partition of the candidate set: every candidate
// is accounted for exactly once, so /qompack:dropped can be honest about what was lost (G4.5).
func runPartitionCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := legalBlocks()
	sel := mustSelector(t, factory, blocks)

	got, err := sel.Select(context.Background(), core.Tokens(200))
	require.NoError(t, err)

	keep, dropped := nodeIDs(got.Keep), nodeIDs(got.Dropped)
	require.Subset(t, blockIDs(blocks), keep, "every kept node was a candidate")
	require.Subset(t, blockIDs(blocks), dropped, "every dropped node was a candidate")
	require.ElementsMatch(t, blockIDs(blocks), append(append([]string{}, keep...), dropped...),
		"Keep and Dropped must partition the candidate set exactly")
}

// runNothingBeforePCase is §13 invariant 4 asserted on the OUTPUT side as well as the input side:
// even with a legal candidate set, nothing a Selector keeps may sit before its own p.
func runNothingBeforePCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := legalBlocks()
	sel := mustSelector(t, factory, blocks)

	got, err := sel.Select(context.Background(), probeBudget)
	require.NoError(t, err)

	posByID := make(map[string]int, len(blocks))
	for _, b := range blocks {
		posByID[string(b.ID)] = b.Pos
	}
	for _, id := range nodeIDs(got.Keep) {
		require.GreaterOrEqual(t, posByID[id], sel.P(), "kept node %s precedes p (§13 invariant 4)", id)
	}
}

// runZeroBudgetCase asserts a budget of zero keeps nothing and drops everything — the degenerate
// case a real greedy loop must not fall through.
func runZeroBudgetCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	blocks := legalBlocks()
	sel := mustSelector(t, factory, blocks)

	got, err := sel.Select(context.Background(), zeroBudget)
	require.NoError(t, err)
	require.Empty(t, got.Keep, "nothing fits in a zero budget")
	require.Zero(t, int(got.Tokens))
	require.ElementsMatch(t, blockIDs(blocks), nodeIDs(got.Dropped), "everything is dropped")
}

// runSelectionDeterminismCase asserts two Selects with the same budget agree exactly, including
// the ORDER of Keep and Dropped. Selection feeds a replay metric, so a nondeterministic tiebreak
// would make FractionOfOPT incomparable between commits.
func runSelectionDeterminismCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	sel := mustSelector(t, factory, legalBlocks())
	ctx := context.Background()

	first, err := sel.Select(ctx, core.Tokens(200))
	require.NoError(t, err)
	second, err := sel.Select(ctx, core.Tokens(200))
	require.NoError(t, err)
	require.Equal(t, first, second, "Select must be deterministic, tiebreaks included")
}

// runItersCase asserts the lazy-greedy evaluation count is actually reported. Iters is what makes
// the (1-1/e) approximation-bound sanity assertion checkable at all (§5.12), so a zero here means
// the bound can never be audited.
func runItersCase(t *testing.T, factory func(t *testing.T) NewSelectorFunc) {
	t.Helper()
	sel := mustSelector(t, factory, legalBlocks())

	got, err := sel.Select(context.Background(), probeBudget)
	require.NoError(t, err)
	require.Greater(t, got.Iters, 0, "a non-empty candidate set costs at least one marginal-gain evaluation")
}

// runScoreRangeCase asserts every block passed to Score comes back with a Δ proxy in [0,1] —
// §5.12's own wording. A missing entry would make a downstream selector treat a block as
// worthless without ever saying so.
func runScoreRangeCase(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) {
	t.Helper()
	blocks := legalBlocks()

	scores, err := factory(t).Score(context.Background(), blocks, coveringContinuation())
	require.NoError(t, err)
	require.Len(t, scores, len(blocks), "Score returns a proxy for EACH block")

	for _, b := range blocks {
		v, ok := scores[b.ID]
		require.True(t, ok, "block %s was not scored", string(b.ID))
		require.GreaterOrEqual(t, v, 0.0, "block %s scored below 0", string(b.ID))
		require.LessOrEqual(t, v, 1.0, "block %s scored above 1", string(b.ID))
	}
}

// runScoreDeterminismCase asserts scoring the same blocks against the same continuation twice
// gives identical scores: Δ-scores are inputs to a replayable metric.
func runScoreDeterminismCase(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()

	first, err := s.Score(ctx, legalBlocks(), coveringContinuation())
	require.NoError(t, err)
	second, err := s.Score(ctx, legalBlocks(), coveringContinuation())
	require.NoError(t, err)
	require.Equal(t, first, second, "Score must be deterministic")
}

// runScoreEmptyCase asserts an empty block set scores nothing and is not an error: the analyzer is
// asked to price a prefix that may legitimately have no candidates left.
func runScoreEmptyCase(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) {
	t.Helper()

	scores, err := factory(t).Score(context.Background(), nil, coveringContinuation())
	require.NoError(t, err, "an empty candidate set is not an error")
	require.Empty(t, scores)
}

// runScoreCoverageCase asserts the direction the whole Δ proxy exists to express: a continuation
// that actually references the blocks' paths and symbols cannot score them LOWER, in total, than
// a continuation that observed nothing at all. It is stated as a sum rather than per block
// because which individual block a scorer credits is its own business; the direction is not.
func runScoreCoverageCase(t *testing.T, factory func(t *testing.T) analyzer.DeltaScorer) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()
	blocks := legalBlocks()

	covering, err := s.Score(ctx, blocks, coveringContinuation())
	require.NoError(t, err)
	empty, err := s.Score(ctx, blocks, emptyContinuation())
	require.NoError(t, err)

	var coveringSum, emptySum float64
	for _, b := range blocks {
		coveringSum += covering[b.ID]
		emptySum += empty[b.ID]
	}
	require.GreaterOrEqual(t, coveringSum, emptySum,
		"an observed continuation that references the blocks must not score them below one that references nothing")
}

// runRedundancyEmptyCase asserts a session with no recorded tool uses reports no redundancy, and
// is not an error: a fresh session is the normal case, not a missing one.
func runRedundancyEmptyCase(t *testing.T, factory func(t *testing.T) RedundancyFixture) {
	t.Helper()
	f := factory(t)
	require.NotEqual(t, f.Session, f.EmptySession, "fixture sanity: the two sessions must differ")

	got, err := f.Detect(context.Background(), f.EmptySession)
	require.NoError(t, err)
	require.Empty(t, got.Superseded)
	require.Empty(t, got.NearDups)
}

// runRedundancySelfCase asserts the report is internally coherent: no tool use is listed as its
// own near-duplicate, and no near-dup list repeats an id. Both would double-count a saving.
func runRedundancySelfCase(t *testing.T, factory func(t *testing.T) RedundancyFixture) {
	t.Helper()

	got, err := factory(t).Detect(context.Background(), factory(t).Session)
	require.NoError(t, err)

	for id, dups := range got.NearDups {
		require.NotContains(t, dups, id, "%s is listed as its own near-duplicate", string(id))
		seen := make(map[core.ToolUseID]bool, len(dups))
		for _, d := range dups {
			require.False(t, seen[d], "%s appears twice in %s's near-duplicate list", string(d), string(id))
			seen[d] = true
		}
	}
}

// runRedundancyDeterminismCase asserts detection is deterministic and read-only: two consecutive
// scans of an unchanged session agree, which also means the first scan did not itself mark
// anything superseded. DetectRedundancy reports; store.MarkSuperseded is the caller's call.
func runRedundancyDeterminismCase(t *testing.T, factory func(t *testing.T) RedundancyFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()

	first, err := f.Detect(ctx, f.Session)
	require.NoError(t, err)
	second, err := f.Detect(ctx, f.Session)
	require.NoError(t, err)
	require.Equal(t, first, second, "DetectRedundancy must be deterministic and must not mutate the store")
}

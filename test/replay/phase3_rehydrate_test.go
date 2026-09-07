package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
)

// Unit coverage for the qompack-rehydrate policy and §10 Phase 3's exit criterion.
//
// The GATE ITSELF lives in the driver binary (policy_rehydrate.go), not here: `devtool replay`
// shells out to `go run ./test/replay`, which does not compile test files. What lives here is the
// coverage that would otherwise have no home — that the block-id zip agrees with eval.Blocks, that
// a keep-set says what it is supposed to say, and that each of the four phase-3 assertions has a
// failing arm.
//
// EVERY CASE THAT REACHES rehydrate.Build IS EXPECTED TO FAIL until SP-11's Build lands; the
// four hand-built Context cases do not reach it and grade the gate arithmetic on its own.

// The fixture session's turn budget. Spelled as constants so an assertion that reads "the residual
// is turn 5" can be checked against the numbers rather than against a total nobody can trace.
const (
	p3Turn0Tokens = core.Tokens(100)
	p3Turn1Tokens = core.Tokens(50)
	p3Turn2Tokens = core.Tokens(30)
	p3Turn3Tokens = core.Tokens(40)
	p3Turn4Tokens = core.Tokens(20)
	p3Turn5Tokens = core.Tokens(60)
)

// p3CompactAt is the fixture's single compaction turn.
//
// It is turn 5, the LAST turn, and that is not arbitrary: eval.Blocks(s, at) emits blocks for
// turns strictly before at, so a compaction placed mid-fixture would hide the elimination and the
// second decision from the block index and make "the index is complete" untestable.
const p3CompactAt = core.TurnIndex(5)

// p3Path is the one file the fixture session reads and edits.
const p3Path = "src/db/pool.ts"

// p3Session is a small, hand-built session carrying one of every block kind the index has to
// resolve: two file-touching tool calls on the same path (so exactly one file block is minted),
// one record_eliminated call, and two decision markers in two different turns.
func p3Session() eval.Session {
	args := func(v map[string]string) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	return eval.Session{
		ID: "sess_phase3_fixture",
		Turns: []eval.Turn{
			{
				Index: 0, Role: "user", TS: 1_000, Tokens: p3Turn0Tokens,
				Text: "the pool exhausts under concurrent refreshes; find out why",
			},
			{
				Index: 1, Role: "assistant", TS: 2_000, Tokens: p3Turn1Tokens,
				Text: "Reading the pool. [decision:dec-read-pool]",
				ToolCalls: []eval.ToolCall{{
					ID: "toolu_p3_read", Name: "Read", Paths: []string{p3Path},
					Args:   args(map[string]string{"file_path": p3Path}),
					Result: json.RawMessage(`{"content":"export const pool = new Pool({ timeout: 30 });"}`),
				}},
			},
			{
				Index: 2, Role: "user", TS: 3_000, Tokens: p3Turn2Tokens,
				Text: "that did not help",
			},
			{
				Index: 3, Role: "assistant", TS: 4_000, Tokens: p3Turn3Tokens,
				Text: "Recording the dead end. [decision:dec-move-rotation]",
				ToolCalls: []eval.ToolCall{
					{
						ID: "toolu_p3_elim", Name: "record_eliminated",
						Args: args(map[string]string{
							"target":   p3Path,
							"approach": "widen pool timeout",
							"reason":   "pgbouncer ignores statement_timeout in transaction pooling mode",
						}),
						Result: json.RawMessage(`{"ok":true}`),
					},
					{
						ID: "toolu_p3_edit", Name: "Edit", Paths: []string{p3Path},
						Args:   args(map[string]string{"file_path": p3Path}),
						Result: json.RawMessage(`{"content":"applied 1 edit"}`),
					},
				},
			},
			{
				Index: 4, Role: "user", TS: 5_000, Tokens: p3Turn4Tokens,
				Text: "keep going",
			},
			{
				Index: 5, Role: "assistant", TS: 6_000, Tokens: p3Turn5Tokens,
				Text: "Searching for the other call sites.",
				ToolCalls: []eval.ToolCall{{
					ID: "toolu_p3_grep", Name: "Grep",
					Args:   args(map[string]string{"pattern": "refreshToken"}),
					Result: json.RawMessage(`{"content":"3 matches"}`),
				}},
			},
		},
		CompactionAt: []core.TurnIndex{p3CompactAt},
		Synthetic:    true,
	}
}

// p3BlockIDs is the set of ids eval.Blocks minted for the fixture at at.
func p3BlockIDs(s eval.Session, at core.TurnIndex) map[string]bool {
	out := map[string]bool{}
	for _, b := range eval.Blocks(s, at) {
		out[b.ID] = true
	}
	return out
}

// p3Policy is the registered policy, constructed the way the driver constructs it.
func p3Policy(t *testing.T) eval.Policy {
	t.Helper()
	p, ok := eval.PolicyByName(policyName, config.Defaults())
	require.True(t, ok, "%s must be registered by this file's own init", policyName)
	return p
}

// TestBlockIndex_MatchesEvalBlockIDs is the pin under the whole zip: every id blockIndex hands out
// must be an id eval.Blocks actually minted.
//
// It matters because the ids are opaque from out here. `elim:` is a domain-separated digest of
// (target, approach-class) that nothing outside internal/eval can reconstruct, and a zip that
// drifted by one position would produce ids that look plausible, satisfy no demand, and quietly
// score the policy at zero — a failure that reads as "the rehydrator preserves nothing" rather
// than "the test harness mislabelled a block".
func TestBlockIndex_MatchesEvalBlockIDs(t *testing.T) {
	s := p3Session()
	minted := p3BlockIDs(s, p3CompactAt)
	idx := newBlockIndex(s, p3CompactAt)

	for key, id := range idx.file {
		require.True(t, minted[id], "file block id %q (for path key %q) is not one eval.Blocks minted", id, key)
	}
	for tu, id := range idx.tool {
		require.True(t, minted[id], "tool block id %q (for tool_use %q) is not one eval.Blocks minted", id, tu)
	}
	for dec, id := range idx.dec {
		require.True(t, minted[id], "decision block id %q (for decision %q) is not one eval.Blocks minted", id, dec)
	}
	for target, id := range idx.elim {
		require.True(t, minted[id], "elimination block id %q (for target %q) is not one eval.Blocks minted", id, target)
	}

	// The index must also be COMPLETE for the fixture, or a passing "every id is real" assertion
	// could be satisfied by an index that resolved nothing at all.
	require.Len(t, idx.file, 1, "Read and Edit touch one path, so exactly one file block exists")
	require.Len(t, idx.tool, 3, "three tool calls precede the compaction")
	require.Len(t, idx.dec, 2, "two decision markers precede the compaction")
	require.Len(t, idx.elim, 1, "one record_eliminated call precedes the compaction")
}

// TestKeepSet_IDsAreEvalBlockIDs: eval.Replay satisfies a demand only when the keep-set literally
// contains that demand's BlockID, so an id from any other vocabulary — a raw path, a tool_use id,
// a checkpoint pointer — is not merely unhelpful, it is invisible.
func TestKeepSet_IDsAreEvalBlockIDs(t *testing.T) {
	resetRehydrateCounters()
	s := p3Session()
	minted := p3BlockIDs(s, p3CompactAt)

	ks, err := p3Policy(t).KeepSet(context.Background(), s, p3CompactAt, eval.DefaultKeepBudget)
	require.NoError(t, err)
	require.NotEmpty(t, ks.IDs, "a rehydration that keeps nothing has not rehydrated anything")

	for _, id := range ks.IDs {
		require.True(t, minted[id], "keep-set carries %q, which eval.Blocks never minted", id)
	}
	require.IsIncreasing(t, ks.IDs, "IDs must be sorted and deduplicated")
}

// TestKeepSet_TokensEqualsResultTokens: the keep-set's cost is the payload's cost, exactly.
// Anything else would grade the policy on a number the rehydrator never spent.
func TestKeepSet_TokensEqualsResultTokens(t *testing.T) {
	resetRehydrateCounters()

	ks, err := p3Policy(t).KeepSet(context.Background(), p3Session(), p3CompactAt, eval.DefaultKeepBudget)
	require.NoError(t, err)

	require.Positive(t, int(ks.Tokens), "a rehydrated digest costs something")
	require.Equal(t, int(maxRehydrationTokens), int(ks.Tokens),
		"the single build of this fixture is also the run's maximum, so the accumulator and the "+
			"keep-set must report the same number")
	require.Equal(t, 1, builds, "one compaction event, one build")
}

// TestKeepSet_PIsZero: a Full Compact rewrites the whole message array, so p_min is 0 and the
// rewrite cost is w·n. Holding P equal to stock's is what keeps the comparison about rehydration
// budget and divergence rather than about a rewrite-cost artifact.
func TestKeepSet_PIsZero(t *testing.T) {
	resetRehydrateCounters()

	ks, err := p3Policy(t).KeepSet(context.Background(), p3Session(), p3CompactAt, eval.DefaultKeepBudget)
	require.NoError(t, err)
	require.Zero(t, ks.P, "P must be 0, the same value eval.stockPolicy returns")

	stock, ok := eval.PolicyByName("stock", config.Defaults())
	require.True(t, ok)
	stockKS, err := stock.KeepSet(context.Background(), p3Session(), p3CompactAt, eval.DefaultKeepBudget)
	require.NoError(t, err)
	require.Equal(t, stockKS.P, ks.P, "both arms must agree on P or the rewrite column is a comparison of nothing")
}

// TestFrontierOf_LastUserTurn pins the stand-in frontier.
//
// F = at would make the residual identically zero and A3 unfailable, which is exactly the failure
// mode a gate like this is written to avoid. A later verification round substitutes the real
// writer-produced frontier; until then this is the definition every A3 number is relative to.
func TestFrontierOf_LastUserTurn(t *testing.T) {
	s := p3Session()

	require.Equal(t, core.TurnIndex(0), frontierOf(s, 0), "turn 0 is itself a user turn")
	require.Equal(t, core.TurnIndex(0), frontierOf(s, 1))
	require.Equal(t, core.TurnIndex(2), frontierOf(s, 2))
	require.Equal(t, core.TurnIndex(2), frontierOf(s, 3),
		"turn 3 is an assistant turn, so the frontier stays at the last user turn before it")
	require.Equal(t, core.TurnIndex(4), frontierOf(s, 4))
	require.Equal(t, core.TurnIndex(4), frontierOf(s, p3CompactAt),
		"turn 5 is an assistant turn; the frontier is the user turn before it")

	// A session with no user turn at or before at has no frontier to report, and -1 is the value
	// that makes the residual the whole prefix rather than silently zero.
	require.Equal(t, core.TurnIndex(-1), frontierOf(eval.Session{
		Turns: []eval.Turn{{Index: 0, Role: "assistant", Tokens: p3Turn1Tokens}},
	}, 0))

	// The residual over (F, at] is turn 5 alone; the stock span is every turn up to and including
	// the compaction point, which is what a Full Compact rewrites.
	residual, stockSpan := spans(s, p3CompactAt)
	require.Equal(t, p3Turn5Tokens, residual)
	require.Equal(t,
		p3Turn0Tokens+p3Turn1Tokens+p3Turn2Tokens+p3Turn3Tokens+p3Turn4Tokens+p3Turn5Tokens,
		stockSpan)
}

// ── phase-3 gate arithmetic ─────────────────────────────────────────────────────────────────

// p3GoodContext is a passing phase-3 Context: the rehydrated arm is cheaper and diverges later,
// and stock's own divergence turn is exactly the committed baseline's, so the §11.3 2% rule finds
// nothing to complain about.
func p3GoodContext() Context {
	return Context{
		Cfg: config.Defaults(),
		Driver: DriverReport{
			Policies: map[string]map[string]float64{
				baselinePolicyName: {
					"rehydration_tokens":    211_261,
					"first_divergence_turn": 11, // testdata/baseline/phase0.json's committed value
				},
				policyName: {
					"rehydration_tokens":    120_000,
					"first_divergence_turn": 14,
				},
			},
		},
	}
}

// p3PassingCounters puts the run-scoped accumulators in the state a good run leaves them in.
func p3PassingCounters(t *testing.T) {
	t.Helper()
	t.Cleanup(resetRehydrateCounters)
	resetRehydrateCounters()

	builds = 24
	tier1Drops = 0
	maxRehydrationTokens = core.Tokens(config.Defaults().Runtime.Rehydrate.MaxTokens)
	maxResidualTokens = core.Tokens(config.Defaults().Checkpoint.Frontier.MaxResidualTokens)
	residualReductions = []float64{residualReductionFloor, 0.71, 0.83}
}

// TestPhase3_PassesOnGoodReport: the four assertions hold together on a run that met all of them.
func TestPhase3_PassesOnGoodReport(t *testing.T) {
	p3PassingCounters(t)
	require.NoError(t, phase3(p3GoodContext()))
}

// TestPhase3_FailsWhenBudgetNotSmaller is A1: a rehydration that costs as much as the summary it
// replaces has bought divergence with tokens, which is the trade §11.3 exists to refuse.
func TestPhase3_FailsWhenBudgetNotSmaller(t *testing.T) {
	p3PassingCounters(t)
	c := p3GoodContext()
	c.Driver.Policies[policyName]["rehydration_tokens"] = c.Driver.Policies[baselinePolicyName]["rehydration_tokens"]

	err := phase3(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "A1 budget")
	require.Contains(t, err.Error(), "211261", "the failure names both numbers")
}

// TestPhase3_FailsWhenBudgetOverMaxTokens is A1's second half: the SUM being smaller says nothing
// about whether any single rehydration blew the per-injection cap, and the sum is the only thing
// the report carries.
func TestPhase3_FailsWhenBudgetOverMaxTokens(t *testing.T) {
	p3PassingCounters(t)
	maxRehydrationTokens = core.Tokens(config.Defaults().Runtime.Rehydrate.MaxTokens + 1)

	err := phase3(p3GoodContext())
	require.Error(t, err)
	require.Contains(t, err.Error(), "A1 budget")
	require.Contains(t, err.Error(), "runtime.rehydrate.maxTokens")
}

// TestPhase3_FailsWhenDivergenceNotBetter is A2: preserving context that does not delay divergence
// has preserved nothing that mattered.
func TestPhase3_FailsWhenDivergenceNotBetter(t *testing.T) {
	p3PassingCounters(t)
	c := p3GoodContext()
	c.Driver.Policies[policyName]["first_divergence_turn"] = c.Driver.Policies[baselinePolicyName]["first_divergence_turn"]

	err := phase3(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "A2 divergence")
}

// TestPhase3_FailsWhenStockItselfRegressed is A2's conjunction: "qompack diverges later than
// stock" is satisfiable by making stock worse, so the stock arm is judged against the committed
// baseline before the comparison is allowed to count.
func TestPhase3_FailsWhenStockItselfRegressed(t *testing.T) {
	p3PassingCounters(t)
	c := p3GoodContext()
	// A drop from 11 to 5 is a 54% move on a DirHigherBetter metric — far past the 2% rule — and
	// the qompack arm still "wins" against it, which is the trap.
	c.Driver.Policies[baselinePolicyName]["first_divergence_turn"] = 5

	err := phase3(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "A2 divergence")
	require.Contains(t, err.Error(), "first_divergence_turn")
}

// TestPhase3_FailsWhenResidualTooLarge is A3: past checkpoint.frontier.maxResidualTokens the
// frontier has not advanced and §8.5 forces a full pass, which is the state O1 exists to prevent.
func TestPhase3_FailsWhenResidualTooLarge(t *testing.T) {
	p3PassingCounters(t)
	maxResidualTokens = core.Tokens(config.Defaults().Checkpoint.Frontier.MaxResidualTokens + 1)

	err := phase3(p3GoodContext())
	require.Error(t, err)
	require.Contains(t, err.Error(), "A3 O1 span")
	require.Contains(t, err.Error(), "maxResidualTokens")
}

// TestPhase3_FailsWhenResidualReductionTooSmall is A3's second threshold: a frontier that removes
// less than half the rewritten span on a multi-compaction session has not bought the O1 property,
// however small its absolute residual happens to be.
func TestPhase3_FailsWhenResidualReductionTooSmall(t *testing.T) {
	p3PassingCounters(t)
	residualReductions = []float64{0.9, residualReductionFloor - 0.01}

	err := phase3(p3GoodContext())
	require.Error(t, err)
	require.Contains(t, err.Error(), "A3 O1 span")
}

// TestPhase3_FailsWhenTier1Dropped is A4: §6.9 truncates tier 3 first, then tier 2, and never tier
// 1. One drop at the default budget is a budgeting bug, not a tight fit.
func TestPhase3_FailsWhenTier1Dropped(t *testing.T) {
	p3PassingCounters(t)
	tier1Drops = 1

	err := phase3(p3GoodContext())
	require.Error(t, err)
	require.Contains(t, err.Error(), "A4 tier-1 intact")
	require.Contains(t, err.Error(), "1 of 24 builds")
}

// TestPhase3_FailsWithoutTheRehydratePolicy: the phase-3 number IS the rehydrator's behaviour, so a
// report that does not carry the policy has not answered the question — it must not pass by
// default because the map lookup returned a zero.
func TestPhase3_FailsWithoutTheRehydratePolicy(t *testing.T) {
	p3PassingCounters(t)
	c := p3GoodContext()
	delete(c.Driver.Policies, policyName)

	err := phase3(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), policyName)
	require.Contains(t, err.Error(), "--policies")
}

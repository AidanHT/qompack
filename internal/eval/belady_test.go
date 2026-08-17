package eval_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// optFixture builds a session whose candidate blocks have exactly the given weights and demand
// counts, so a Belady instance can be stated in the terms the algorithm reasons about.
//
// Layout: one producer turn per item (turn tokens 0, one FileRead whose result carries the item's
// weight), then the compaction turn itself, then consumer turns that re-read item i exactly
// demands[i] times — one per turn, because Demands deduplicates on (Turn, BlockID). Turn blocks
// weigh 0 and are never demanded, so the only candidates are the file blocks.
//
// Item i's file block therefore sits at position Σ weights[0:i], which is what makes p_min
// assertions expressible.
func optFixture(weights []core.Tokens, demands []int) (eval.Session, core.TurnIndex) {
	n := len(weights)
	turns := make([]eval.Turn, 0, n+1)
	for i, w := range weights {
		res, _ := json.Marshal(map[string]int{"tokens": int(w)})
		turns = append(turns, eval.Turn{
			Index: core.TurnIndex(i), Role: "assistant", Tokens: 0,
			ToolCalls: []eval.ToolCall{{
				ID:     core.ToolUseID("t" + strconv.Itoa(i)),
				Name:   "FileRead",
				Paths:  []string{"f" + strconv.Itoa(i) + ".go"},
				Result: res,
			}},
		})
	}
	at := core.TurnIndex(n)
	turns = append(turns, eval.Turn{Index: at, Role: "assistant", Tokens: 0})

	maxD := 0
	for _, d := range demands {
		maxD = max(maxD, d)
	}
	for k := range maxD {
		var calls []eval.ToolCall
		for i, d := range demands {
			if d > k {
				calls = append(calls, eval.ToolCall{
					ID:    core.ToolUseID("c" + strconv.Itoa(k) + "_" + strconv.Itoa(i)),
					Name:  "FileRead",
					Paths: []string{"f" + strconv.Itoa(i) + ".go"},
				})
			}
		}
		turns = append(turns, eval.Turn{
			Index: core.TurnIndex(n + 1 + k), Role: "assistant", Tokens: 0, ToolCalls: calls,
		})
	}
	return eval.Session{ID: "opt-fixture", Turns: turns}, at
}

// fileID is the block ID optFixture gives item i.
func fileID(i int) string { return "file:f" + strconv.Itoa(i) + ".go" }

// exactOptions is DefaultBeladyOptions with a granularity of 1, so a test asserts the DP's answer
// rather than the conservative upward rounding of the weight scaling.
func exactOptions() eval.BeladyOptions {
	o := eval.DefaultBeladyOptions()
	o.Granularity = 1
	return o
}

// TestBelady_UnitWeightsMatchesClassicBelady: with equal weights and a slot budget the knapsack
// degenerates to classic Belady — keep the items demanded most — which is what makes it the right
// generalization to heterogeneous block sizes rather than a substitute for it.
func TestBelady_UnitWeightsMatchesClassicBelady(t *testing.T) {
	s, at := optFixture(
		[]core.Tokens{256, 256, 256, 256, 256, 256},
		[]int{3, 2, 1, 0, 0, 0},
	)

	ks, detail, err := eval.BeladyDetail(context.Background(), s, at, 768, eval.DefaultBeladyOptions())
	require.NoError(t, err)

	require.Equal(t, []string{fileID(0), fileID(1), fileID(2)}, ks.IDs)
	require.Equal(t, core.Tokens(768), ks.Tokens)
	require.Equal(t, 6, detail.Value, "3 + 2 + 1 demands satisfied")
	require.True(t, detail.Exact)
	require.Equal(t, 3, detail.Candidates, "the three never-demanded blocks are pruned")
}

// TestBelady_KnapsackBeatsGreedyDensity asserts the solver takes the pair that fills the budget
// exactly rather than the single highest-value item.
func TestBelady_KnapsackBeatsGreedyDensity(t *testing.T) {
	s, at := optFixture([]core.Tokens{10, 6, 5}, []int{6, 5, 4})

	ks, detail, err := eval.BeladyDetail(context.Background(), s, at, 11, exactOptions())
	require.NoError(t, err)

	require.Equal(t, []string{fileID(1), fileID(2)}, ks.IDs)
	require.Equal(t, core.Tokens(11), ks.Tokens)
	require.Equal(t, 9, detail.Value, "5 + 4 beats the single 6-demand item that eats the budget")
}

// TestBelady_PMinIsEarliestDropped pins p_min: the earliest position among the candidates OPT
// decided to drop, which is the p of §5.2's cost = w·(n − p_min).
func TestBelady_PMinIsEarliestDropped(t *testing.T) {
	// Positions are the running sum of the weights before each item: 0, 100, 300, 600.
	s, at := optFixture([]core.Tokens{100, 200, 300, 600}, []int{5, 1, 1, 5})

	ks, _, err := eval.BeladyDetail(context.Background(), s, at, 700, exactOptions())
	require.NoError(t, err)

	require.Equal(t, []string{fileID(0), fileID(3)}, ks.IDs,
		"100+600 fits 700 for value 10; every richer combination overruns")
	require.Equal(t, 100, ks.P, "item 1, at position 100, is the earliest dropped candidate")
}

// TestBelady_PIsPrefixLengthWhenNothingDropped: with everything kept there is no dropped block to
// take a position from, so p_min is the whole prefix and the rewrite cost is zero.
func TestBelady_PIsPrefixLengthWhenNothingDropped(t *testing.T) {
	s, at := optFixture([]core.Tokens{100, 200}, []int{1, 1})

	ks, _, err := eval.BeladyDetail(context.Background(), s, at, 10_000, exactOptions())
	require.NoError(t, err)

	require.Len(t, ks.IDs, 2)
	require.Equal(t, 300, ks.P, "n = 100 + 200, the position-advancing tokens of the prefix")
}

// TestBelady_ZeroValueBlocksPruned: dropping every never-demanded block is what keeps the DP
// small enough to be exact on a real session.
func TestBelady_ZeroValueBlocksPruned(t *testing.T) {
	weights := make([]core.Tokens, 500)
	demands := make([]int, 500)
	for i := range weights {
		weights[i] = 128
	}
	demands[10], demands[100], demands[400] = 1, 1, 1

	_, detail, err := eval.BeladyDetail(optFixtureArgs(weights, demands))
	require.NoError(t, err)
	require.Equal(t, 3, detail.Candidates)
}

// optFixtureArgs adapts optFixture to BeladyDetail's argument list.
func optFixtureArgs(weights []core.Tokens, demands []int) (context.Context, eval.Session, core.TurnIndex, core.Tokens, eval.BeladyOptions) {
	s, at := optFixture(weights, demands)
	return context.Background(), s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions()
}

// TestBelady_BudgetNeverExceeded is the invariant the whole ceiling rests on: a keep-set that
// cheats on the budget is not comparable to one that does not.
func TestBelady_BudgetNeverExceeded(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 40).Draw(rt, "items")
		weights := make([]core.Tokens, n)
		demands := make([]int, n)
		for i := range n {
			weights[i] = core.Tokens(rapid.IntRange(0, 5000).Draw(rt, "w"))
			demands[i] = rapid.IntRange(0, 3).Draw(rt, "d")
		}
		budget := core.Tokens(rapid.IntRange(0, 100_000).Draw(rt, "budget"))

		s, at := optFixture(weights, demands)
		ks, _, err := eval.BeladyDetail(context.Background(), s, at, budget, eval.DefaultBeladyOptions())
		if err != nil {
			rt.Fatalf("BeladyDetail: %v", err)
		}
		if ks.Tokens > budget {
			rt.Fatalf("keep-set of %d tokens exceeds the %d-token budget", ks.Tokens, budget)
		}
	})
}

// TestBelady_Deterministic: two runs on the same instance must produce the identical keep-set, or
// the baseline number moves for reasons nobody changed.
func TestBelady_Deterministic(t *testing.T) {
	weights := make([]core.Tokens, 120)
	demands := make([]int, 120)
	for i := range weights {
		weights[i] = core.Tokens(100 + (i*37)%900)
		demands[i] = (i * 7) % 4
	}
	s, at := optFixture(weights, demands)

	first, d1, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
	require.NoError(t, err)
	second, d2, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, d1, d2)
	require.NotEmpty(t, first.IDs, "a fixture that keeps nothing would make this vacuous")
}

// TestBelady_FallbackWhenDPTooLarge: the 1/2-approximation is a bounded degradation, not a
// silent one — OPTDetail.Exact says so, and the gate WARNs on it.
func TestBelady_FallbackWhenDPTooLarge(t *testing.T) {
	weights := make([]core.Tokens, 50)
	demands := make([]int, 50)
	for i := range weights {
		weights[i] = core.Tokens(200 + (i*53)%1500)
		demands[i] = 1 + (i % 3)
	}
	s, at := optFixture(weights, demands)

	capped := eval.DefaultBeladyOptions()
	capped.MaxDPCells = 10
	approx, ad, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget, capped)
	require.NoError(t, err)
	require.False(t, ad.Exact, "a 10-cell cap cannot admit the exact solver")

	exact, ed, err := eval.BeladyDetail(context.Background(), s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
	require.NoError(t, err)
	require.True(t, ed.Exact)

	require.LessOrEqual(t, approx.Tokens, eval.DefaultKeepBudget)
	require.LessOrEqual(t, exact.Tokens, eval.DefaultKeepBudget)
	require.LessOrEqual(t, ad.Value, ed.Value, "an approximation cannot beat the exact optimum")
	require.GreaterOrEqual(t, 2*ad.Value, ed.Value, "the fallback is a 1/2-approximation")
}

// TestBelady_ContextCancelled: a cancelled 20M-cell run must unwind before allocating, not after.
func TestBelady_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s, at := optFixture([]core.Tokens{100, 200}, []int{1, 1})
	_, _, err := eval.BeladyDetail(ctx, s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
	require.ErrorIs(t, err, context.Canceled)
}

// TestBelady_NonPositiveBudgetIsEmptyNotAnError: a zero budget is a legitimate question with a
// legitimate answer, and the answer is "keep nothing".
func TestBelady_NonPositiveBudgetIsEmptyNotAnError(t *testing.T) {
	s, at := optFixture([]core.Tokens{100}, []int{1})

	ks, detail, err := eval.BeladyDetail(context.Background(), s, at, 0, eval.DefaultBeladyOptions())
	require.NoError(t, err)
	require.Empty(t, ks.IDs)
	require.Equal(t, core.Tokens(0), ks.Tokens)
	require.True(t, detail.Exact)
	require.Equal(t, 0, detail.Candidates)
}

// TestOraclePolicy_DelegatesToBelady: the oracle is the ceiling, so it must be the same answer
// Belady gives and not a re-derivation that could drift from it.
func TestOraclePolicy_DelegatesToBelady(t *testing.T) {
	s, at := optFixture([]core.Tokens{256, 256, 256}, []int{3, 2, 1})

	viaPolicy, err := eval.NewOraclePolicy(config.Defaults()).
		KeepSet(context.Background(), s, at, 512)
	require.NoError(t, err)
	viaBelady, _, err := eval.BeladyDetail(context.Background(), s, at, 512, eval.DefaultBeladyOptions())
	require.NoError(t, err)

	require.Equal(t, viaBelady, viaPolicy)
	require.Equal(t, "oracle", eval.NewOraclePolicy(config.Defaults()).Name())
}

// BenchmarkBeladyDetail_400Turns is budget E-2: <= 250 ms/op.
func BenchmarkBeladyDetail_400Turns(b *testing.B) {
	weights := make([]core.Tokens, 400)
	demands := make([]int, 400)
	for i := range weights {
		weights[i] = core.Tokens(150 + (i*61)%4000)
		demands[i] = (i * 3) % 5
	}
	s, at := optFixture(weights, demands)
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := eval.BeladyDetail(ctx, s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions()); err != nil {
			b.Fatal(err)
		}
	}
}

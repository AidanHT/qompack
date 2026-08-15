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
)

// fileReadTurn builds one assistant turn that reads path and produces a result of the given size.
func fileReadTurn(i int, path string, resultTokens core.Tokens) eval.Turn {
	res, _ := json.Marshal(map[string]any{"tokens": int(resultTokens)})
	return eval.Turn{
		Index: core.TurnIndex(i), Role: "assistant", Tokens: 50,
		ToolCalls: []eval.ToolCall{{
			ID:     core.ToolUseID("tu" + strconv.Itoa(i)),
			Name:   "FileRead",
			Paths:  []string{path},
			Result: res,
		}},
	}
}

// keptOfKind returns the kept IDs carrying the given prefix.
func keptOfKind(ids []string, prefix string) []string {
	var out []string
	for _, id := range ids {
		if len(id) >= len(prefix) && id[:len(prefix)] == prefix {
			out = append(out, id)
		}
	}
	return out
}

// restoreThenChatSession is nine file-reading turns followed by n small assistant turns.
//
// The separation is deliberate. Stage (b)'s preservation walk counts a turn's tool results, so a
// session whose nine most recent turns each carry an 8_000-token result puts ~32K into the
// preservation window on its own; adding stage (a)'s 25K then overruns the 40K harness budget and
// stage (c) evicts the very files the §2.4-step-7 assertion is about. Pushing the file reads out
// of the preservation window keeps the two stages independently observable, which is what lets
// this test assert step 7 and TestStockPolicy_UsedNeverGoesNegative assert eviction.
func restoreThenChatSession(chat int) eval.Session {
	var turns []eval.Turn
	for i := range 9 {
		turns = append(turns, fileReadTurn(i, "src/f"+strconv.Itoa(i)+".go", 8000))
	}
	for i := 9; i < 9+chat; i++ {
		turns = append(turns, eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 500})
	}
	return eval.Session{ID: "restore-then-chat", Turns: turns}
}

// TestStockPolicy_TopFiveFilesFiveKEach is §2.4 step 7: the host restores the top 5 recently-read
// files at 5K each under a 50K budget. Nine files are offered, each far over the per-file cap, so
// both the fan-out and the cap are exercised at once.
func TestStockPolicy_TopFiveFilesFiveKEach(t *testing.T) {
	s := restoreThenChatSession(30)

	ks, err := eval.NewStockPolicy(config.Defaults()).
		KeepSet(context.Background(), s, 39, eval.DefaultKeepBudget)
	require.NoError(t, err)

	files := keptOfKind(ks.IDs, "file:")
	require.Len(t, files, 5, "§2.4 step 7 restores the top 5 recently-read files")
	require.Equal(t, []string{
		"file:src/f4.go", "file:src/f5.go", "file:src/f6.go", "file:src/f7.go", "file:src/f8.go",
	}, files, "the five most recent by turn, in ascending turn order")
	require.Equal(t, 0, ks.P, "a Full Compact rewrites the whole message array, so p_min is 0")
}

// TestStockPolicy_PreservationMinimums is §2.3: walk backwards until BOTH minTokens=10_000 and
// minTextBlockMessages=5 are met, capped at maxTokens=40_000. Twenty 500-token turns hit the
// token floor exactly, and keeping a twenty-first would overshoot a minimum that is already met.
func TestStockPolicy_PreservationMinimums(t *testing.T) {
	var turns []eval.Turn
	for i := range 30 {
		turns = append(turns, eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 500})
	}
	s := eval.Session{ID: "preserve", Turns: turns}

	ks, err := eval.NewStockPolicy(config.Defaults()).
		KeepSet(context.Background(), s, 30, eval.DefaultKeepBudget)
	require.NoError(t, err)

	require.Len(t, keptOfKind(ks.IDs, "turn:"), 20,
		"20 x 500 = 10_000, the minTokens floor; the 5-message minimum was met long before")
	require.Equal(t, core.Tokens(10000), ks.Tokens)
}

// TestStockPolicy_UsedNeverGoesNegative is the regression test for the double-count the contrib
// map exists to prevent: stage (a) caps a file block at 5K while stage (b) would count the same
// block in full, so a naive accumulator drifts and can go negative once stage (c) evicts.
func TestStockPolicy_UsedNeverGoesNegative(t *testing.T) {
	s := restoreThenChatSession(30)

	ks, err := eval.NewStockPolicy(config.Defaults()).
		KeepSet(context.Background(), s, 39, core.Tokens(1000))
	require.NoError(t, err)

	require.GreaterOrEqual(t, ks.Tokens, core.Tokens(0), "the accumulator must never go negative")
	require.LessOrEqual(t, ks.Tokens, core.Tokens(1000),
		"stage (c) evicts until the equal budget is met")
}

// TestStockPolicy_BudgetSmallerThanOneBlockTerminates: oldestKeptTurn reporting "nothing kept" is
// what stops the eviction loop when even a single block will not fit, so a budget of 1 must
// return an empty keep-set rather than spin.
func TestStockPolicy_BudgetSmallerThanOneBlockTerminates(t *testing.T) {
	ks, err := eval.NewStockPolicy(config.Defaults()).
		KeepSet(context.Background(), restoreThenChatSession(10), 19, core.Tokens(1))
	require.NoError(t, err)
	require.Empty(t, ks.IDs)
	require.Equal(t, core.Tokens(0), ks.Tokens)
}

// TestNullPolicy_Empty pins the floor every other policy is bounded below by.
func TestNullPolicy_Empty(t *testing.T) {
	ks, err := eval.NewNullPolicy(config.Defaults()).
		KeepSet(context.Background(), eval.Session{}, 0, eval.DefaultKeepBudget)
	require.NoError(t, err)
	require.Nil(t, ks.IDs)
	require.Equal(t, core.Tokens(0), ks.Tokens)
	require.Equal(t, 0, ks.P)
}

// TestRegisterPolicy_DuplicatePanics: a duplicate name is a programming error at init, never a
// runtime path, so it must be loud immediately rather than silently shadowing a policy.
func TestRegisterPolicy_DuplicatePanics(t *testing.T) {
	require.Panics(t, func() {
		eval.RegisterPolicy("stock", func(config.Config) eval.Policy { return nil })
	})
}

// TestPolicyByName_UnknownIsNotFound asserts lookup reports absence rather than returning a nil
// Policy a caller would then invoke.
func TestPolicyByName_UnknownIsNotFound(t *testing.T) {
	_, ok := eval.PolicyByName("no-such-policy", config.Defaults())
	require.False(t, ok)

	p, ok := eval.PolicyByName("stock", config.Defaults())
	require.True(t, ok)
	require.Equal(t, "stock", p.Name())
}

// TestPolicyNames_Sorted asserts the registry's order is lexicographic, so a report's column
// order never depends on init order or on map iteration.
func TestPolicyNames_Sorted(t *testing.T) {
	require.Equal(t, []string{"null", "oracle", "stock"}, eval.PolicyNames(),
		"the three built-ins: the floor, the ceiling, and the thing being measured")
}

package evaltest

import (
	"context"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the evaltest suite (00-ARCHITECTURE.md §5.22
// table): deterministic replay and Belady optimality on a small, hand-verifiable instance. Both
// are authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-02
// inherits them rather than writing its own grader.

// budgetKeepEveryTurnPolicy is a small, fully deterministic Policy used only to exercise Replay's
// determinism: it keeps every tool call up to (but not exceeding) budget, walking turns in order.
// It intentionally ignores everything about *how* a real policy would rank importance —
// determinism, not quality, is what this fixture checks.
type budgetKeepEveryTurnPolicy struct{}

func (budgetKeepEveryTurnPolicy) Name() string { return "budget-keep-every-turn" }

func (budgetKeepEveryTurnPolicy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error) {
	var ids []string
	var total core.Tokens
	for _, turn := range s.Turns {
		if turn.Index >= at {
			break
		}
		if total+turn.Tokens > budget {
			continue
		}
		for _, tc := range turn.ToolCalls {
			ids = append(ids, string(tc.ID))
		}
		total += turn.Tokens
	}
	return eval.KeepSet{IDs: ids, Tokens: total, P: int(at)}, nil
}

const (
	deterministicReplaySeed   = 4242
	deterministicReplayBudget = 500
	turn0Tokens               = 130
	turn1Tokens               = 180
	turn2Tokens               = 140
)

// deterministicReplaySession is a small, fixed session for the determinism fixture.
func deterministicReplaySession() eval.Session {
	return eval.Session{
		ID: "det-replay-fixture",
		Turns: []eval.Turn{
			{Index: 0, Role: "assistant", Tokens: core.Tokens(turn0Tokens), ToolCalls: []eval.ToolCall{{ID: "call-0", Name: "Read", Paths: []string{"a.go"}}}},
			{Index: 1, Role: "assistant", Tokens: core.Tokens(turn1Tokens), ToolCalls: []eval.ToolCall{{ID: "call-1", Name: "Bash"}}},
			{Index: 2, Role: "assistant", Tokens: core.Tokens(turn2Tokens), ToolCalls: []eval.ToolCall{{ID: "call-2", Name: "Edit", Paths: []string{"b.go"}}}},
		},
		CompactionAt: []core.TurnIndex{3},
	}
}

// runDeterministicReplayCase asserts that two Replay calls against the same Session, Policy and
// ReplayOptions (Deterministic: true, a fixed Seed) produce a byte-identical Run.
func runDeterministicReplayCase(t *testing.T, factory func(t *testing.T) eval.Harness) {
	t.Helper()
	h := factory(t)
	ctx := context.Background()
	session := deterministicReplaySession()
	policy := budgetKeepEveryTurnPolicy{}
	opts := eval.ReplayOptions{
		Seed:          deterministicReplaySeed,
		Budget:        core.Tokens(deterministicReplayBudget),
		Deterministic: true,
	}

	run1, err := h.Replay(ctx, session, policy, opts)
	require.NoError(t, err)
	run2, err := h.Replay(ctx, session, policy, opts)
	require.NoError(t, err)
	require.Equal(t, run1, run2,
		"Replay must be deterministic: the same Session, Policy and Seed must produce an identical Run")
}

// beladyItem is one keepable item in the six-item Belady fixture below: a token cost and whether
// it is read again after the compaction point.
type beladyItem struct {
	id     string
	tokens core.Tokens
	reused bool
}

// beladyFixtureBudget is chosen so that the optimal subset uses the budget exactly, with a
// unique optimum (see bruteForceOptimalIDs and the hand-verified assertion in
// runBeladySixItemCase).
const beladyFixtureBudget = 250

// beladyFixtureItems is the six-item instance: a, b, d and f are read again after the
// compaction point (so a Belady-optimal policy wants to keep them); c and e are not (e is also
// too large to ever fit the budget on its own, which is deliberate — it must never be chosen
// regardless of its reuse status).
func beladyFixtureItems() []beladyItem {
	return []beladyItem{
		{id: "a", tokens: 100, reused: true},
		{id: "b", tokens: 200, reused: true},
		{id: "c", tokens: 150, reused: false},
		{id: "d", tokens: 100, reused: true},
		{id: "e", tokens: 350, reused: false},
		{id: "f", tokens: 50, reused: true},
	}
}

// bruteForceOptimalIDs enumerates every subset of items whose total token cost fits budget and
// returns the sorted IDs of the subset that maximizes the count of reused items included,
// breaking ties toward the lower-weight subset. This is the ground truth
// runBeladySixItemCase checks both the fixture itself and the Harness under test against.
func bruteForceOptimalIDs(items []beladyItem, budget core.Tokens) []string {
	n := len(items)
	bestValue := -1
	var bestWeight core.Tokens
	var bestIDs []string

	for mask := 0; mask < (1 << n); mask++ {
		var weight core.Tokens
		var value int
		var ids []string
		for i, it := range items {
			if mask&(1<<i) == 0 {
				continue
			}
			weight += it.tokens
			if it.reused {
				value++
			}
			ids = append(ids, it.id)
		}
		if weight > budget {
			continue
		}
		if value > bestValue || (value == bestValue && weight < bestWeight) {
			bestValue = value
			bestWeight = weight
			bestIDs = ids
		}
	}

	sort.Strings(bestIDs)
	return bestIDs
}

// beladyFixtureSession renders beladyFixtureItems() into a Session: one producer turn per item
// (indices 0..5, each with a single ToolCall touching a unique path and Turn.Tokens set to the
// item's weight), followed by one consumer turn per reused item that re-touches the same path —
// the observable "used again" signal a real Belady implementation would key off. CompactionAt
// names the turn index right after the six producer turns.
func beladyFixtureSession() eval.Session {
	items := beladyFixtureItems()
	turns := make([]eval.Turn, 0, len(items))
	for i, it := range items {
		turns = append(turns, eval.Turn{
			Index:     core.TurnIndex(i),
			Role:      "assistant",
			Tokens:    it.tokens,
			ToolCalls: []eval.ToolCall{{ID: core.ToolUseID(it.id), Name: "Read", Paths: []string{"file_" + it.id + ".go"}}},
		})
	}

	at := core.TurnIndex(len(items))
	next := len(items)
	for _, it := range items {
		if !it.reused {
			continue
		}
		turns = append(turns, eval.Turn{
			Index:     core.TurnIndex(next),
			Role:      "assistant",
			ToolCalls: []eval.ToolCall{{ID: core.ToolUseID("reuse-" + it.id), Name: "Read", Paths: []string{"file_" + it.id + ".go"}}},
		})
		next++
	}

	return eval.Session{ID: "belady-six-item-fixture", Turns: turns, CompactionAt: []core.TurnIndex{at}}
}

// runBeladySixItemCase brute-forces the optimal keep-set for beladyFixtureItems() (2^6 = 64
// subsets, well within "brute-forceable") and asserts Belady returns exactly that set.
func runBeladySixItemCase(t *testing.T, factory func(t *testing.T) eval.Harness) {
	t.Helper()
	h := factory(t)
	ctx := context.Background()
	items := beladyFixtureItems()
	session := beladyFixtureSession()
	at := core.TurnIndex(len(items))

	want := bruteForceOptimalIDs(items, beladyFixtureBudget)
	// Hand-verified ground truth for this fixture's exact weights: {a,d,f} sums to exactly the
	// budget (100+100+50=250) and is the only 3-reused-item combination that fits; every other
	// feasible combination keeps at most 2 reused items. If this ever fails, the fixture's
	// weights above changed and this comment needs updating, not the Harness under test.
	require.Equal(t, []string{"a", "d", "f"}, want)

	got, err := h.Belady(ctx, session, at, core.Tokens(beladyFixtureBudget))
	require.NoError(t, err)
	require.LessOrEqual(t, got.Tokens, core.Tokens(beladyFixtureBudget))

	gotIDs := append([]string(nil), got.IDs...)
	sort.Strings(gotIDs)
	require.Equal(t, want, gotIDs, "Belady must return the optimal keep-set on this brute-forceable instance")
}

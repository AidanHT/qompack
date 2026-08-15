package evaltest

import (
	"context"
	"encoding/json"
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

// beladyScale expresses the fixture's weights and budget in tokens rather than in slots.
//
// A real Harness quantizes weights before solving, so an instance stated in units of 1 token
// collapses to a zero-capacity knapsack and every item is dropped. Scaling both the weights and
// the budget by the same factor leaves the instance — and its hand-verified unique optimum
// {a, d, f} — arithmetically identical, while making it expressible in the units a Harness
// actually reasons about. bruteForceOptimalIDs stays in the readable unscaled units.
const beladyScale = 256

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

// beladyFixturePath is the path item id occupies, and therefore the file a consumer turn
// re-touches to signal "used again".
func beladyFixturePath(id string) string { return "file_" + id + ".go" }

// beladyFixtureSession renders beladyFixtureItems() into a Session.
//
// Layout: one producer turn per item (indices 0..5), each a single Read of a unique path whose
// result carries the item's weight; then the compaction turn itself; then one consumer turn per
// reused item that re-touches the same path.
//
// Two details are load-bearing rather than incidental. The weight rides on the tool RESULT, not on
// Turn.Tokens, because what a keep-set pays for is the content a tool call put into the
// transcript — a producer turn with an empty result costs nothing to keep, and a budget that binds
// on nothing cannot discriminate between keep-sets. And the consumer turns begin AFTER the
// compaction turn, not at it, because the compaction turn is the boundary: a turn at the boundary
// is neither before it nor after it, so an item re-touched exactly there would be invisible as a
// demand and the fixture would silently be a five-item instance.
func beladyFixtureSession() eval.Session {
	items := beladyFixtureItems()
	turns := make([]eval.Turn, 0, 2*len(items)+1)
	for i, it := range items {
		result, err := json.Marshal(map[string]int{"tokens": int(it.tokens) * beladyScale})
		if err != nil {
			panic("evaltest: marshalling the belady fixture result: " + err.Error())
		}
		turns = append(turns, eval.Turn{
			Index: core.TurnIndex(i),
			Role:  "assistant",
			ToolCalls: []eval.ToolCall{{
				ID:     core.ToolUseID(it.id),
				Name:   "Read",
				Paths:  []string{beladyFixturePath(it.id)},
				Result: result,
			}},
		})
	}

	at := core.TurnIndex(len(items))
	turns = append(turns, eval.Turn{Index: at, Role: "assistant"})

	next := int(at) + 1
	for _, it := range items {
		if !it.reused {
			continue
		}
		turns = append(turns, eval.Turn{
			Index:     core.TurnIndex(next),
			Role:      "assistant",
			ToolCalls: []eval.ToolCall{{ID: core.ToolUseID("reuse-" + it.id), Name: "Read", Paths: []string{beladyFixturePath(it.id)}}},
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

	budget := core.Tokens(beladyFixtureBudget * beladyScale)
	got, err := h.Belady(ctx, session, at, budget)
	require.NoError(t, err)
	require.LessOrEqual(t, got.Tokens, budget)

	// A Harness identifies what it keeps by block, not by tool-use id, so the brute-forced answer
	// is translated into the same vocabulary before comparison. The instance and its optimum are
	// unchanged; only the names are.
	//
	// The path is spelled literally rather than through paths.Key: evaltest's allow-set does not
	// include paths (§3.2), and beladyFixturePath is already lowercase ASCII, so the two agree on
	// every platform — case folding applies on Windows and macOS and is the identity here.
	wantIDs := make([]string, 0, len(want))
	for _, id := range want {
		wantIDs = append(wantIDs, "file:"+beladyFixturePath(id))
	}
	sort.Strings(wantIDs)

	gotIDs := append([]string(nil), got.IDs...)
	sort.Strings(gotIDs)
	require.Equal(t, wantIDs, gotIDs, "Belady must return the optimal keep-set on this brute-forceable instance")
}

package eval_test

import (
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// breakpointSession builds an n-turn session with assistant turns of ascending token cost and a
// compaction partway through, so BreakpointOPT has real candidate positions and caps to work with.
func breakpointSession(n int) eval.Session {
	turns := make([]eval.Turn, n)
	for i := range turns {
		role := "assistant"
		if i%6 == 0 {
			role = "user"
		}
		turns[i] = eval.Turn{
			Index: core.TurnIndex(i), Role: role,
			Tokens: core.Tokens(300 + (i*97)%1200),
			Text:   "turn " + strconv.Itoa(i),
		}
	}
	return eval.Session{ID: "bp", Turns: turns, CompactionAt: []core.TurnIndex{core.TurnIndex(n / 2)}}
}

// TestBreakpointOPT_NoteIsAlwaysTheDisclaimer is the honesty check §5.6 and §12 both demand: this
// analysis is measurement-and-port material, and its output must never read as a plugin feature.
func TestBreakpointOPT_NoteIsAlwaysTheDisclaimer(t *testing.T) {
	for _, markers := range []int{0, 1, 4, 99} {
		plan, err := eval.BreakpointOPT(breakpointSession(40), markers)
		require.NoError(t, err)
		require.Equal(t, eval.NotPluginActionable, plan.Note)
		require.Contains(t, plan.Note, "a plugin cannot place or move them")
	}
}

// TestBreakpointOPT_EchoesMarkerBudget: Markers echoes what the caller asked for even when fewer
// positions come back, so a report can say "4 asked, 2 useful" rather than silently reporting 2.
func TestBreakpointOPT_EchoesMarkerBudget(t *testing.T) {
	plan, err := eval.BreakpointOPT(breakpointSession(40), 4)
	require.NoError(t, err)

	require.Equal(t, 4, plan.Markers)
	require.LessOrEqual(t, len(plan.Positions), 4)
	require.Positive(t, plan.Candidates)
	require.NotContains(t, plan.Positions, 0, "position 0 is the implicit baseline, never a marker")
}

// TestBreakpointOPT_PositionsAscending: the plan is consumed as a marker set, so its order is part
// of the contract.
func TestBreakpointOPT_PositionsAscending(t *testing.T) {
	plan, err := eval.BreakpointOPT(breakpointSession(60), 4)
	require.NoError(t, err)

	for i := 1; i < len(plan.Positions); i++ {
		require.Less(t, plan.Positions[i-1], plan.Positions[i])
	}
}

// TestBreakpointOPT_CandidatesCappedAt256 keeps the O(markers · C²) DP bounded regardless of how
// long the session ran.
func TestBreakpointOPT_CandidatesCappedAt256(t *testing.T) {
	plan, err := eval.BreakpointOPT(breakpointSession(1200), 4)
	require.NoError(t, err)
	require.LessOrEqual(t, plan.Candidates, 256)
}

// TestBreakpointOPT_EmptySession: nothing to place markers between is not an error.
func TestBreakpointOPT_EmptySession(t *testing.T) {
	plan, err := eval.BreakpointOPT(eval.Session{ID: "empty"}, 4)
	require.NoError(t, err)
	require.Empty(t, plan.Positions)
	require.Equal(t, eval.NotPluginActionable, plan.Note)
}

// BenchmarkBreakpointOPT_256Candidates is budget E-5: <= 15 ms/op.
func BenchmarkBreakpointOPT_256Candidates(b *testing.B) {
	s := breakpointSession(1200)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := eval.BreakpointOPT(s, 4); err != nil {
			b.Fatal(err)
		}
	}
}

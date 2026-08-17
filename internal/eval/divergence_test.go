package eval_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// act is a terse Action constructor for divergence fixtures.
func act(turn int, tool string, paths ...string) eval.Action {
	return eval.Action{Turn: core.TurnIndex(turn), Tool: tool, Paths: paths}
}

// decisionAct is an Action that carries a decision.
func decisionAct(turn int, tool, decision string) eval.Action {
	return eval.Action{Turn: core.TurnIndex(turn), Tool: tool, Decision: decision}
}

// runOf builds a Run with the horizon metadata Compare reads off its compacted argument.
func runOf(branch string, firstCompaction, horizon int, acts ...eval.Action) eval.Run {
	return eval.Run{
		Branch:              branch,
		Actions:             acts,
		FirstCompactionTurn: core.TurnIndex(firstCompaction),
		Horizon:             horizon,
	}
}

func compare(t *testing.T, a, b eval.Run) eval.Divergence {
	t.Helper()
	return eval.New(eval.Options{}).Compare(a, b)
}

// TestCompare_IdenticalRuns pins the identity: no divergence anywhere in the horizon reports the
// LARGEST value FirstDivergenceTurn can take, never -1, because the metric is DirHigherBetter and
// a sentinel would score a perfect policy as the worst one.
func TestCompare_IdenticalRuns(t *testing.T) {
	r := runOf("compacted", 10, 20, act(11, "FileRead", "a.go"), act(12, "Edit", "b.go"))

	got := compare(t, r, r)

	require.Equal(t, eval.Divergence{
		FirstDivergenceTurn:  20,
		FileSetJaccard:       1,
		ToolEditDistance:     0,
		SameDecision:         true,
		DecisionPreservation: 1,
	}, got)
}

// TestCompare_NoCompactionIsTheIdentity: a session that never compacted has no branches to
// compare, and the honest answer is "identical", not a zeroed struct that reads as total drift.
func TestCompare_NoCompactionIsTheIdentity(t *testing.T) {
	got := compare(t,
		runOf("uncompacted", -1, 20, act(1, "FileRead", "a.go")),
		runOf("compacted", -1, 20),
	)

	require.Equal(t, 20, got.FirstDivergenceTurn)
	require.Equal(t, 1.0, got.FileSetJaccard)
	require.True(t, got.SameDecision)
	require.Equal(t, 1.0, got.DecisionPreservation)
}

// TestCompare_FirstDivergenceIsRelativeToCompaction: the metric counts turns AFTER the
// compaction, which is what makes it comparable across sessions that compact at different points.
func TestCompare_FirstDivergenceIsRelativeToCompaction(t *testing.T) {
	unc := runOf("uncompacted", 40, 20, act(41, "Edit", "a.go"), act(42, "Bash"), act(43, "Test"))
	cmp := runOf("compacted", 40, 20, act(41, "Edit", "a.go"), act(42, "Bash"), act(43, "FileRead", "a.go"))

	require.Equal(t, 3, compare(t, unc, cmp).FirstDivergenceTurn)
}

// TestCompare_FirstDivergenceAlwaysInRange is the property the 2% gate depends on.
func TestCompare_FirstDivergenceAlwaysInRange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		horizon := rapid.IntRange(1, 30).Draw(rt, "horizon")
		fct := rapid.IntRange(0, 50).Draw(rt, "compactionTurn")
		tools := []string{"FileRead", "Edit", "Bash", "Test", "ReAttempt", ""}

		build := func(label string) []eval.Action {
			n := rapid.IntRange(0, 20).Draw(rt, label)
			out := make([]eval.Action, n)
			for i := range out {
				out[i] = act(fct+1+i,
					tools[rapid.IntRange(0, len(tools)-1).Draw(rt, label+"tool")],
					"p"+string(rune('a'+rapid.IntRange(0, 5).Draw(rt, label+"path")))+".go")
			}
			return out
		}
		unc := runOf("uncompacted", fct, horizon, build("unc")...)
		cmp := runOf("compacted", fct, horizon, build("cmp")...)

		got := eval.New(eval.Options{}).Compare(unc, cmp).FirstDivergenceTurn
		if got < 0 || got > horizon {
			rt.Fatalf("FirstDivergenceTurn = %d, outside [0, %d]", got, horizon)
		}
	})
}

// TestCompare_JaccardBothEmpty: two branches that touched no files agree perfectly about files.
func TestCompare_JaccardBothEmpty(t *testing.T) {
	unc := runOf("uncompacted", 0, 20, act(1, "Bash"))
	cmp := runOf("compacted", 0, 20, act(1, "Bash"))
	require.Equal(t, 1.0, compare(t, unc, cmp).FileSetJaccard)
}

// TestCompare_JaccardHalf pins the arithmetic on a hand-checkable overlap.
func TestCompare_JaccardHalf(t *testing.T) {
	unc := runOf("uncompacted", 0, 20, act(1, "FileRead", "a.go", "b.go"))
	cmp := runOf("compacted", 0, 20, act(1, "FileRead", "b.go", "c.go"))

	require.InDelta(t, 1.0/3.0, compare(t, unc, cmp).FileSetJaccard, 1e-9,
		"intersection {b}, union {a,b,c}")
}

// TestCompare_EditDistanceKnown: one deletion turns [read, edit, test] into [read, test].
func TestCompare_EditDistanceKnown(t *testing.T) {
	unc := runOf("uncompacted", 0, 20, act(1, "read"), act(2, "edit"), act(3, "test"))
	cmp := runOf("compacted", 0, 20, act(1, "read"), act(2, "test"))

	require.Equal(t, 1, compare(t, unc, cmp).ToolEditDistance)
}

// TestCompare_EditDistanceProperties: symmetry, the length bound, and zero exactly on equality.
func TestCompare_EditDistanceProperties(t *testing.T) {
	tools := []string{"FileRead", "Edit", "Bash", "Test"}
	rapid.Check(t, func(rt *rapid.T) {
		build := func(label string) []eval.Action {
			n := rapid.IntRange(0, 12).Draw(rt, label)
			out := make([]eval.Action, n)
			for i := range out {
				out[i] = act(1+i, tools[rapid.IntRange(0, 3).Draw(rt, label+"t")])
			}
			return out
		}
		a, b := build("a"), build("b")
		ra := runOf("uncompacted", 0, 30, a...)
		rb := runOf("compacted", 0, 30, b...)
		h := eval.New(eval.Options{})

		forward := h.Compare(ra, rb).ToolEditDistance
		backward := h.Compare(rb, runOf("compacted", 0, 30, a...)).ToolEditDistance
		if forward != backward {
			rt.Fatalf("edit distance is not symmetric: %d vs %d", forward, backward)
		}
		if forward > max(len(a), len(b)) {
			rt.Fatalf("edit distance %d exceeds max(%d, %d)", forward, len(a), len(b))
		}
		same := h.Compare(ra, runOf("compacted", 0, 30, a...)).ToolEditDistance
		if same != 0 {
			rt.Fatalf("edit distance of a sequence with itself is %d, want 0", same)
		}
	})
}

// TestCompare_DecisionPreservationDenominatorZero: no decisions to lose means none were lost.
func TestCompare_DecisionPreservationDenominatorZero(t *testing.T) {
	unc := runOf("uncompacted", 0, 20, act(1, "Bash"))
	cmp := runOf("compacted", 0, 20, act(1, "Bash"))

	got := compare(t, unc, cmp)
	require.Equal(t, 1.0, got.DecisionPreservation)
	require.True(t, got.SameDecision)
}

// TestCompare_DecisionLost is the G8.1 signal: a compaction that drops a decision shows up as a
// drop in DecisionPreservation and a SameDecision of false.
func TestCompare_DecisionLost(t *testing.T) {
	unc := runOf("uncompacted", 0, 20,
		decisionAct(1, "Bash", "dec_a"), decisionAct(2, "Bash", "dec_b"))
	cmp := runOf("compacted", 0, 20,
		decisionAct(1, "Bash", "dec_a"), decisionAct(2, "Bash", ""))

	got := compare(t, unc, cmp)
	require.InDelta(t, 0.5, got.DecisionPreservation, 1e-9, "dec_a survived, dec_b did not")
	require.False(t, got.SameDecision, "the last decision reached differs")
}

// TestCompare_RedundantReadsCanBeNegative: a policy that PREVENTS re-reads is an improvement, and
// the metric has to be able to say so rather than clamping at zero.
func TestCompare_RedundantReadsCanBeNegative(t *testing.T) {
	unc := runOf("uncompacted", 0, 20,
		act(1, "FileRead", "a.go"), act(2, "FileRead", "a.go"), act(3, "FileRead", "a.go"))
	cmp := runOf("compacted", 0, 20,
		act(1, "FileRead", "a.go"), act(2, "Edit", "a.go"), act(3, "Bash"))

	require.Negative(t, compare(t, unc, cmp).RedundantReads)
}

// TestCompare_ReAttemptsCounted: a re-attempt of an eliminated approach is the §11.2 "re-attempts
// of eliminated approaches" half of the redundant-work metric.
func TestCompare_ReAttemptsCounted(t *testing.T) {
	unc := runOf("uncompacted", 0, 20, act(1, "Edit", "a.go"))
	cmp := runOf("compacted", 0, 20, act(1, "ReAttempt", "a.go"), act(2, "ReAttempt", "b.go"))

	require.Equal(t, 2, compare(t, unc, cmp).ReAttempts)
}

// TestCompare_HorizonBoundsTheComparison: actions past the horizon are not part of the estimate,
// so a difference out there must not register.
func TestCompare_HorizonBoundsTheComparison(t *testing.T) {
	unc := runOf("uncompacted", 10, 3, act(11, "Edit"), act(12, "Edit"), act(20, "Bash"))
	cmp := runOf("compacted", 10, 3, act(11, "Edit"), act(12, "Edit"), act(20, "Test"))

	got := compare(t, unc, cmp)
	require.Equal(t, 3, got.FirstDivergenceTurn, "turn 20 is outside the horizon [11, 13]")
	require.Equal(t, 0, got.ToolEditDistance)
}

// BenchmarkCompare_400Actions is budget E-4: <= 20 ms/op, Levenshtein dominated.
func BenchmarkCompare_400Actions(b *testing.B) {
	tools := []string{"FileRead", "Edit", "Bash", "Test"}
	build := func(shift int) []eval.Action {
		out := make([]eval.Action, 400)
		for i := range out {
			out[i] = act(1+i, tools[(i+shift)%4], "src/f"+string(rune('a'+i%26))+".go")
		}
		return out
	}
	unc := runOf("uncompacted", 0, 400, build(0)...)
	cmp := runOf("compacted", 0, 400, build(1)...)
	h := eval.New(eval.Options{})

	b.ReportAllocs()
	for b.Loop() {
		_ = h.Compare(unc, cmp)
	}
}

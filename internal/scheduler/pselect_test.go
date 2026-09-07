package scheduler

// This is a _test.go file, so the nomagic literal sets do not apply here.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEligible_KeepsOnlyRoundBoundaries(t *testing.T) {
	cands := []Candidate{
		{Pos: 10, RoundBoundary: false},
		{Pos: 20, RoundBoundary: true},
		{Pos: 30, RoundBoundary: false},
		{Pos: 40, RoundBoundary: true},
	}
	out, relaxed := eligible(cands)
	require.False(t, relaxed)
	require.Equal(t, []Candidate{{Pos: 20, RoundBoundary: true}, {Pos: 40, RoundBoundary: true}}, out)
}

func TestEligible_RelaxesWhenIntersectionEmpty(t *testing.T) {
	cands := []Candidate{{Pos: 10}, {Pos: 20}, {Pos: 30}}
	out, relaxed := eligible(cands)
	require.True(t, relaxed)
	require.Equal(t, []Candidate{{Pos: 10}, {Pos: 20}, {Pos: 30}}, out)
}

func TestEligible_EmptyInputStaysEmpty(t *testing.T) {
	out, relaxed := eligible(nil)
	require.Empty(t, out)
	require.False(t, relaxed)
}

func TestPrepareCandidates_SortsAndDetectsNonMonotonic(t *testing.T) {
	in := []Candidate{
		{Pos: 30, ReclaimableTokens: 5},
		{Pos: 10, ReclaimableTokens: 1},
		{Pos: 20, ReclaimableTokens: 3},
	}
	out, nonMonotonic := prepareCandidates(in)
	require.True(t, nonMonotonic)
	require.Equal(t, []Candidate{
		{Pos: 10, ReclaimableTokens: 1},
		{Pos: 20, ReclaimableTokens: 3},
		{Pos: 30, ReclaimableTokens: 5},
	}, out)
	require.Equal(t, 30, in[0].Pos, "the caller's slice must not be reordered")

	monotone := []Candidate{{Pos: 10, ReclaimableTokens: 5}, {Pos: 20, ReclaimableTokens: 5}, {Pos: 30, ReclaimableTokens: 1}}
	_, nonMonotonic = prepareCandidates(monotone)
	require.False(t, nonMonotonic, "equal reclaimable at a later Pos is non-increasing, not a violation")
}

func TestPrepareCandidates_CapKeepsHighestPos(t *testing.T) {
	in := make([]Candidate, 0, 100)
	for pos := 100; pos >= 1; pos-- { // descending, to prove the sort as well
		in = append(in, Candidate{Pos: pos, ReclaimableTokens: 7})
	}
	out, nonMonotonic := prepareCandidates(in)
	require.False(t, nonMonotonic)
	require.Len(t, out, 100, "prepareCandidates sorts; the cap is a separate, later step (R44)")
	out = capHighestPos(out)
	require.Len(t, out, maxScoredCandidates)
	require.Equal(t, 32, maxScoredCandidates)
	for i, c := range out {
		require.Equal(t, 69+i, c.Pos)
	}
}

func TestScoreCandidates_UsesConfigMultipliers(t *testing.T) {
	reg := CacheRegime{TTLMinSeconds: 300, TTLMaxSeconds: 300, ReadMultiplier: 0.2, WriteMultiplier: 2.0}
	cands := []Candidate{
		{Pos: 400, ReclaimableTokens: 300, Coupling: 10},
		{Pos: 1_200, ReclaimableTokens: 50, Coupling: 2}, // beyond n: tail clamps to 0
	}
	ss := scoreCandidates(cands, 1_000, 0.5, 0.4, reg)
	require.Len(t, ss, 2)

	// tail 600; reclaim 300 × 0.2 = 60; rewrite 2.0 × 600 × 0.5 = 600; distortion 0.4 × 10 = 4
	require.InDelta(t, 600.0, ss[0].tail, 0)
	require.InDelta(t, 60.0, ss[0].reclaim, 1e-9)
	require.InDelta(t, 600.0, ss[0].rewrite, 1e-9)
	require.InDelta(t, 4.0, ss[0].distortion, 1e-9)
	require.InDelta(t, 60.0-600.0-4.0, ss[0].score, 1e-9)

	// tail max(0, 1 000 − 1 200) = 0; reclaim 50 × 0.2 = 10; rewrite 0; distortion 0.4 × 2 = 0.8
	require.InDelta(t, 0.0, ss[1].tail, 0)
	require.InDelta(t, 10.0, ss[1].reclaim, 1e-9)
	require.InDelta(t, 0.0, ss[1].rewrite, 0)
	require.InDelta(t, 0.8, ss[1].distortion, 1e-9)
	require.InDelta(t, 10.0-0.8, ss[1].score, 1e-9)

	// λ ≤ 0 turns the distortion term off entirely.
	off := scoreCandidates(cands, 1_000, 0.5, 0, reg)
	require.Equal(t, 0.0, off[0].distortion)
}

func TestChooseP_TieBreakLatestWhenWarmDeepestWhenCold(t *testing.T) {
	tied := []scored{
		{c: Candidate{Pos: 20}, score: 1},
		{c: Candidate{Pos: 30}, score: 1},
		{c: Candidate{Pos: 10}, score: 1},
	}
	cfg := baseCfg()
	require.True(t, cfg.Idle.DeepCutWhenCold)

	best, ok := chooseP(tied, TTLWarm, cfg)
	require.True(t, ok)
	require.Equal(t, 30, best.c.Pos, "warm ⇒ edit as late as possible")

	best, ok = chooseP(tied, TTLCold, cfg)
	require.True(t, ok)
	require.Equal(t, 10, best.c.Pos, "cold + deepCutWhenCold ⇒ the deep cut is free")

	cfg.Idle.DeepCutWhenCold = false
	best, ok = chooseP(tied, TTLCold, cfg)
	require.True(t, ok)
	require.Equal(t, 30, best.c.Pos, "cold with the preference off ⇒ latest again")

	// A strictly better score still wins regardless of the tie-break direction.
	better := append([]scored(nil), tied...)
	better[0].score = 2
	best, _ = chooseP(better, TTLCold, cfg)
	require.Equal(t, 20, best.c.Pos)
}

func TestChooseP_EmptyReturnsNotOK(t *testing.T) {
	_, ok := chooseP(nil, TTLWarm, baseCfg())
	require.False(t, ok)
}

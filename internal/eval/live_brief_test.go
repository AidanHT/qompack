package eval_test

import (
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestMatchLiveBriefs: each compaction, in stream order, takes the first unclaimed record with its
// trigger: served with characters, served-empty (chars 0) as its own class, each unserved reason
// as itself, and "missing" when no record is left. Records no compaction claims are counted, and a
// trial with no non-empty served brief is flagged (W6 item 5).
func TestMatchLiveBriefs(t *testing.T) {
	compactions := []eval.HostCompaction{
		{Trigger: "manual"}, {Trigger: "auto"}, {Trigger: "manual"}, {Trigger: "manual"}, {Trigger: "manual"},
	}
	records := []eval.LiveBriefRecord{
		{Trigger: "precompute", Outcome: "served", Chars: 900, MS: 40},
		{Trigger: "manual", Outcome: "served", Chars: 1200, MS: 50},
		{Trigger: "auto", Outcome: "timeout", MS: 1500},
		{Trigger: "manual", Outcome: "served", Chars: 0, MS: 30},
		{Trigger: "manual", Outcome: "refused"},
	}
	got := eval.MatchLiveBriefs(compactions, records)
	var outcomes []string
	for _, c := range got.Compactions {
		outcomes = append(outcomes, c.Outcome)
	}
	require.Equal(t, []string{"served", "timeout", "served-empty", "error", "missing"}, outcomes,
		"a refused record is never logged, so one in the log is an error; the fifth compaction has none")
	require.Equal(t, 1200, got.Compactions[0].Chars)
	require.Zero(t, got.Compactions[2].Chars)
	require.Equal(t, 5, got.Records)
	require.Equal(t, 1, got.Unmatched, "the precompute brief matches no compaction")
	require.False(t, got.NoServedBrief)

	none := eval.MatchLiveBriefs(compactions[:2], []eval.LiveBriefRecord{{Trigger: "manual", Outcome: "served"}})
	require.True(t, none.NoServedBrief, "served-empty steered nothing")
	require.Equal(t, "served-empty", none.Compactions[0].Outcome)
	require.Equal(t, "missing", none.Compactions[1].Outcome)
	require.True(t, eval.MatchLiveBriefs(compactions[:1], nil).NoServedBrief)
}

// TestSummarizeLive_BriefShares: the steer arm's summary counts its compactions by class and gives
// the unserved share (missing included) with served-empty as its own share beside it, the trials
// with no non-empty served brief (noted under intention to treat), and the served lengths. Arms
// without briefs carry none.
func TestSummarizeLive_BriefShares(t *testing.T) {
	brief := func(outcomes ...string) *eval.LiveBriefOutcomes {
		b := &eval.LiveBriefOutcomes{}
		served := false
		for _, o := range outcomes {
			c := eval.LiveBriefCompaction{Trigger: "manual", Outcome: o}
			if o == eval.LiveBriefServed {
				c.Chars, served = 1000, true
			}
			b.Compactions = append(b.Compactions, c)
		}
		b.NoServedBrief = !served
		return b
	}
	ts := []eval.LiveTrial{
		{TaskID: "a", Arm: eval.ArmSteer, Trial: 1, Brief: brief("served", "missing"), PreregisteredModel: true},
		{TaskID: "a", Arm: eval.ArmSteer, Trial: 2, Brief: brief("served-empty", "cold"), PreregisteredModel: true},
		{TaskID: "a", Arm: eval.ArmClassic, Trial: 1, PreregisteredModel: true},
	}
	sum := eval.SummarizeLive("r", liveAnalysis(), ts)
	b := sum.Arms[eval.ArmSteer].Brief
	require.NotNil(t, b)
	require.Equal(t, 2, b.Trials)
	require.Equal(t, 4, b.Compactions)
	require.Equal(t, map[string]int{"served": 1, "missing": 1, "served-empty": 1, "cold": 1}, b.Outcomes)
	require.InDelta(t, 0.5, b.UnservedShare, 1e-12, "missing and cold are unserved")
	require.InDelta(t, 0.25, b.ServedEmptyShare, 1e-12)
	require.Equal(t, 1, b.NoServedTrials)
	require.Equal(t, eval.LiveDistribution{N: 1, Mean: 1000, P50: 1000, P95: 1000, Max: 1000}, b.ServedChars)
	require.Nil(t, sum.Arms[eval.ArmClassic].Brief)
	require.Contains(t, joinNotes(sum.Notes), "1 of 2 steer trial(s) had no non-empty served brief")
}

func joinNotes(notes []string) string {
	out := ""
	for _, n := range notes {
		out += n + "\n"
	}
	return out
}

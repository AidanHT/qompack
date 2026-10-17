package eval_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestParseHostStream_AbsentDurationAndCostAreUnknown (reviews STATS-5, CLI-6): a result line with no
// duration_ms or no total_cost_usd says so, rather than decoding the absent value as 0; the session's
// cost stays at the last running total a line carried, a lower bound, with a problem named; and the
// trial's step records and C carry the absence through.
func TestParseHostStream_AbsentDurationAndCostAreUnknown(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"a","duration_ms":1200,"duration_api_ms":900,"total_cost_usd":0.5}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"b"}`,
	}, "\n") + "\n"
	s, err := eval.ParseHostStream(strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.Len(t, s.Turns, 2)
	require.False(t, s.Turns[0].Result.DurationMissing)
	require.False(t, s.Turns[0].Result.CostMissing)
	require.True(t, s.Turns[1].Result.DurationMissing)
	require.True(t, s.Turns[1].Result.CostMissing)

	acct := eval.AccountHostStream(s, eval.AccountBaseline{})
	require.InDelta(t, 0.5, acct.HostCostUSD, 1e-12, "the absent total is not a zero")
	require.Contains(t, strings.Join(acct.Problems, "\n"), "carries no total_cost_usd")

	task := eval.LiveTask{Steps: []eval.LiveStep{{ID: "a", Prompt: "p"}, {ID: "b", Prompt: "q"}}}
	recs, _ := eval.StepRecords(task, s)
	require.True(t, recs[1].DurationMissing)
	require.True(t, recs[1].CostMissing)
	tr := eval.LiveTrial{Steps: recs, HostCostUSD: acct.HostCostUSD}
	c, status := tr.Cost()
	require.Equal(t, eval.LiveCostPartial, status)
	require.InDelta(t, 0.5, c, 1e-12)
	require.Equal(t, 1, tr.MissingDurations())
}

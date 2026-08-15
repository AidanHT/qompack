package eval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLatencyModel_Anchors pins the two sentences of the design the coefficients answer to. If
// anyone changes a coefficient, this test names the claim they broke rather than just going red.
//
//   - §6.7: "the summarization call at 167K input runs ~15–40s". At the §2.5 stock residual of
//     167_000 tokens the model must land inside that band.
//   - §8.5 (O5): after frontier advancement "that residual span is now 10–20K tokens rather than
//     150K, regardless of how long the session has run". At 10–20K the model must land in the
//     few-second range that makes O5 worth doing.
func TestLatencyModel_Anchors(t *testing.T) {
	lat := DefaultLatencyModel()
	require.True(t, lat.Modelled, "a built harness never reports these as observed")

	stock := modelledPauseMS(lat, hostAutoCompactThreshold(200_000))
	require.Equal(t, 28_050, stock)
	require.GreaterOrEqual(t, stock, 15_000, "§6.7 lower bound of the ~15–40s band")
	require.LessOrEqual(t, stock, 40_000, "§6.7 upper bound of the ~15–40s band")

	require.Equal(t, 4_500, modelledPauseMS(lat, 10_000), "§8.5 post-O5 lower residual")
	require.Equal(t, 5_250, modelledPauseMS(lat, 15_000))
	require.Equal(t, 6_000, modelledPauseMS(lat, 20_000), "§8.5 post-O5 upper residual")
}

// TestLatencyModel_FirstTurnAfterScalesWithRehydration: the first turn after a compaction pays for
// rebuilding and re-writing the cache, so it scales with what was kept, not with the residual.
func TestLatencyModel_FirstTurnAfterScalesWithRehydration(t *testing.T) {
	lat := DefaultLatencyModel()

	require.Equal(t, 800, modelledFirstTurnMS(lat, 0))
	require.Equal(t, 1_700, modelledFirstTurnMS(lat, 10_000))
	require.Equal(t, 4_400, modelledFirstTurnMS(lat, 40_000), "a full DefaultKeepBudget rehydration")
}

// TestModelledLatency_NeverNegative: a residual can be clamped to zero, and a negative millisecond
// count would poison a percentile.
func TestModelledLatency_NeverNegative(t *testing.T) {
	lat := DefaultLatencyModel()
	require.GreaterOrEqual(t, modelledPauseMS(lat, -5_000), 0)
	require.GreaterOrEqual(t, modelledFirstTurnMS(lat, -5_000), 0)
}

package eval_test

import (
	"os"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestLiveRateTableV7_AddsSonnet55 (amendment 4 item 9): claude-sonnet-5-5 is priced, dated and
// sourced in the file's provenance style. rates.json is a frozen material of the qompack-live-v1/v2
// pre-registration (TestLivePreregistrations_MatchTheCommittedMaterials pins its hash), so the
// addition is a new table that keeps every rates.json price unchanged. It affects the estimate only,
// never the decided cost C.
func TestLiveRateTableV7_AddsSonnet55(t *testing.T) {
	read := func(name string) eval.LiveRateTable {
		raw, err := os.ReadFile(liveTaskFile(name))
		require.NoError(t, err)
		tbl, err := eval.ParseLiveRateTable(raw)
		require.NoError(t, err)
		return tbl
	}
	v7, frozen := read("rates-v7.json"), read("rates.json")
	require.Equal(t, "2026-10-16", v7.Date)
	require.Contains(t, v7.Source, "claude-api skill bundled with Claude Code 2.1.300")
	require.Contains(t, v7.Source, "cached 2026-10-06")
	require.Equal(t, eval.LiveModelRate{
		InputPerMTokMicros: 2000000, OutputPerMTokMicros: 10000000, CacheReadMultiplier: 0.1,
		CacheWrite5mMultiplier: 1.25, CacheWrite1hMultiplier: 2,
	}, v7.Models["claude-sonnet-5-5"])
	require.Len(t, v7.Models, len(frozen.Models)+1)
	for m, r := range frozen.Models {
		require.Equal(t, r, v7.Models[m], "%s keeps its rates.json price", m)
	}
}

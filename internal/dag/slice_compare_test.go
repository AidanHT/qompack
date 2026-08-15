package dag_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// Qompack.md §6.4 says of thin slicing:
//
//	Thin slicing drops control-dependence-only edges for much smaller slices at the cost of
//	soundness — probably the right tradeoff here.
//
// "Probably the right tradeoff" is a hypothesis, and this file is where it stops being one. It
// measures what thin slicing actually buys and what it actually costs, across eight seeds, and
// fails the build if either number stops justifying the default.
//
// The measurement is the point, not the assertion thresholds: a future change that makes thin
// slicing pointless — because it stopped shrinking the slice, or started losing most of the truly
// relevant set — should be visible as a number someone has to look at, not discovered later as a
// quality regression nobody can attribute.

// compareSpec is the corpus shape: roughly 4,800 nodes and 14,700 edges per seed, the same shape
// the benchmarks use.
var compareSpec = dagtest.SynthSpec{
	Turns:                  400,
	ToolsPerTurn:           3,
	Files:                  40,
	SymbolsPerFile:         6,
	ControlOnlyFraction:    0.35,
	ControlCarriedFraction: 0.12,
	Supersessions:          30,
	TokensPerTool:          600,
}

// compareSeeds are the eight seeds the table is measured over. One seed would measure one graph;
// eight is enough that a threshold cannot be met by a lucky topology.
var compareSeeds = []int64{1, 2, 3, 4, 5, 6, 7, 8}

// The thresholds §6.4's claim is turned into. They are deliberately loose enough that ordinary
// variation between seeds cannot trip them, and tight enough that thin slicing ceasing to earn its
// place would.
const (
	// maxMeanSizeRatio: a thin slice must be meaningfully smaller. §6.4 promises "much smaller".
	maxMeanSizeRatio = 0.75
	// minMeanRecall: the soundness thin slicing trades away must stay a minority of the truly
	// relevant set. This is the number that says the tradeoff is worth making.
	minMeanRecall = 0.85
)

// compareRow is one seed's measurement. Wall-clock timings are deliberately NOT recorded here:
// this struct is committed as a golden, and a golden containing nanosecond timings is a golden that
// differs on every run and therefore tells nobody anything. The timing claim is asserted directly
// instead — thin must not be slower than full — and logged for whoever is reading the run.
type compareRow struct {
	Seed          int64   `json:"seed"`
	Nodes         int     `json:"nodes"`
	Edges         int     `json:"edges"`
	ThinSize      int     `json:"thin_size"`
	FullSize      int     `json:"full_size"`
	SizeRatio     float64 `json:"size_ratio"`
	Recall        float64 `json:"recall"`
	Precision     float64 `json:"precision"`
	RecallFull    float64 `json:"recall_full"`
	PrecisionFull float64 `json:"precision_full"`
}

// compareTable is the committed shape of thin-vs-full.json.
type compareTable struct {
	Spec              dagtest.SynthSpec `json:"spec"`
	Rows              []compareRow      `json:"rows"`
	MeanSizeRatio     float64           `json:"mean_size_ratio"`
	MeanRecall        float64           `json:"mean_recall"`
	MeanPrecision     float64           `json:"mean_precision"`
	MeanPrecisionFull float64           `json:"mean_precision_full"`
}

// compareUpdate reports whether -update was passed, sharing the flag testutil and golden_test.go
// already register rather than declaring a second one.
var compareUpdate = func() func() bool {
	const name = "update"
	if f := flag.Lookup(name); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(name, false, "rewrite golden files under testdata/golden/ instead of comparing against them")
	return func() bool { return *p }
}()

// compareTolerance is how far a recorded ratio may drift before the golden is treated as stale.
// These are measurements of a deterministic generator, so they do not move at all between runs on
// one machine; the tolerance exists because a Go release changing float evaluation order by an ulp
// must not fail the build over the fifth decimal of a ratio.
const compareTolerance = 0.02

// medianRuns is how many timed repetitions each slice gets. The median discards the one run that
// happened to land on a GC pause.
const medianRuns = 5

// sliceOnce runs one slice and returns its scores with the wall time it took.
func sliceOnce(tb testing.TB, g dag.Graph, criteria []dag.NodeID, thin bool) (map[dag.NodeID]float32, time.Duration) {
	tb.Helper()
	o := dag.DefaultSliceOptions(config.Defaults())
	o.Thin = thin
	o.Deadline = 0       // measure the walk, not the limiter
	o.MaxNodes = 1 << 20 // ditto

	var (
		scores map[dag.NodeID]float32
		best   time.Duration
	)
	times := make([]time.Duration, 0, medianRuns)
	for range medianRuns {
		start := time.Now()
		sl, err := g.BackwardSlice(criteria, o)
		elapsed := time.Since(start)
		require.NoError(tb, err)
		require.False(tb, sl.Truncated, "the comparison must measure complete slices")
		scores = sl.Scores
		times = append(times, elapsed)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	best = times[len(times)/2]
	return scores, best
}

// overlap returns how many of truth's members appear in scores.
func overlap(scores map[dag.NodeID]float32, truth map[dag.NodeID]bool) int {
	n := 0
	for id := range truth {
		if _, ok := scores[id]; ok {
			n++
		}
	}
	return n
}

// ratio guards the empty denominator so a degenerate seed reports 0 rather than NaN, which would
// silently poison every mean computed from it.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// TestThinVsFullComparison measures thin slicing against full slicing over eight seeded graphs,
// publishes the table, and asserts the tradeoff still holds.
func TestThinVsFullComparison(t *testing.T) {
	table := compareTable{Spec: compareSpec}
	var sumRatio, sumRecall, sumPrecision, sumPrecisionFull float64

	for _, seed := range compareSeeds {
		nodes, edges, criteria, truth := dagtest.Synth(seed, compareSpec)
		require.NotEmpty(t, criteria, "seed %d produced no criteria", seed)
		require.NotEmpty(t, truth, "seed %d produced no ground truth", seed)

		g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
		require.NoError(t, err)
		dagtest.Load(t, g, nodes, edges)

		thinScores, thinNS := sliceOnce(t, g, criteria, true)
		fullScores, fullNS := sliceOnce(t, g, criteria, false)

		row := compareRow{
			Seed:          seed,
			Nodes:         len(nodes),
			Edges:         len(edges),
			ThinSize:      len(thinScores),
			FullSize:      len(fullScores),
			SizeRatio:     ratio(len(thinScores), len(fullScores)),
			Recall:        ratio(overlap(thinScores, truth), len(truth)),
			Precision:     ratio(overlap(thinScores, truth), len(thinScores)),
			RecallFull:    ratio(overlap(fullScores, truth), len(truth)),
			PrecisionFull: ratio(overlap(fullScores, truth), len(fullScores)),
		}
		table.Rows = append(table.Rows, row)
		sumRatio += row.SizeRatio
		sumRecall += row.Recall
		sumPrecision += row.Precision
		sumPrecisionFull += row.PrecisionFull

		t.Logf("seed %d: nodes=%d edges=%d thin=%d full=%d ratio=%.3f recall=%.3f precision=%.3f  (thin %v, full %v)",
			seed, len(nodes), len(edges), len(thinScores), len(fullScores),
			row.SizeRatio, row.Recall, row.Precision, thinNS, fullNS)

		// §6.4's other claim: thin slicing is the CHEAPER walk. It visits a subset of the edges
		// full slicing does, so anything else would mean the thin path is doing extra work.
		require.LessOrEqual(t, thinNS, fullNS,
			"seed %d: thin slicing must not be slower than full slicing", seed)
	}

	n := float64(len(compareSeeds))
	table.MeanSizeRatio = sumRatio / n
	table.MeanRecall = sumRecall / n
	table.MeanPrecision = sumPrecision / n
	table.MeanPrecisionFull = sumPrecisionFull / n

	writeOrCompareTable(t, table)

	require.LessOrEqual(t, table.MeanSizeRatio, maxMeanSizeRatio,
		"thin slicing stopped producing much smaller slices (§6.4); mean size ratio %.3f", table.MeanSizeRatio)
	require.GreaterOrEqual(t, table.MeanRecall, minMeanRecall,
		"thin slicing is losing too much of the truly relevant set; mean recall %.3f", table.MeanRecall)
	require.GreaterOrEqual(t, table.MeanPrecision, table.MeanPrecisionFull,
		"thin slicing must not be less precise than full slicing: it drops the weakest evidence, "+
			"so what it keeps should be a purer set, not a muddier one")
}

// writeOrCompareTable publishes thin-vs-full.json, or checks the recorded ratios against it within
// compareTolerance.
func writeOrCompareTable(t *testing.T, got compareTable) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", "contracts", "dag", "thin-vs-full.json")

	encoded, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')

	if compareUpdate() {
		require.NoError(t, os.WriteFile(path, encoded, 0o644))
		t.Logf("updated %s", path)
		return
	}

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "thin-vs-full.json missing; regenerate with -update")

	var want compareTable
	require.NoError(t, json.Unmarshal(raw, &want))
	require.Len(t, got.Rows, len(want.Rows), "seed count changed")

	for i, wr := range want.Rows {
		gr := got.Rows[i]
		require.Equal(t, wr.Seed, gr.Seed, "row %d: seed order changed", i)
		require.InDelta(t, wr.SizeRatio, gr.SizeRatio, compareTolerance,
			"seed %d: size ratio moved", wr.Seed)
		require.InDelta(t, wr.Recall, gr.Recall, compareTolerance, "seed %d: recall moved", wr.Seed)
		require.InDelta(t, wr.Precision, gr.Precision, compareTolerance, "seed %d: precision moved", wr.Seed)
	}
	require.InDelta(t, want.MeanSizeRatio, got.MeanSizeRatio, compareTolerance, "mean size ratio moved")
	require.InDelta(t, want.MeanRecall, got.MeanRecall, compareTolerance, "mean recall moved")
}

package dag_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
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
// differs on every run and therefore tells nobody anything. The cost half of §6.4's claim is
// asserted from Slice.EdgesVisited instead — thin must not follow more edges than full — and the
// timings are logged for whoever is reading the run, with nothing gating on them.
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

// batchRuns is how many slices are timed as a single interval.
//
// One backward slice here costs a few hundred microseconds, which is not safely above the clock's
// own resolution. Go's monotonic clock on Windows falls back to roughly millisecond granularity
// whenever no process is holding the system timer finer, and that can change underneath a running
// test: inside one `go test ./...` this file reported a thin slice as "0s" and full slices pinned
// to 997-1008µs, alongside honest 515-532µs readings for other seeds in the same loop. An earlier
// version of this helper took a median of single-shot measurements, which does nothing about that
// — the median of five quantized samples is still quantized — and then compared two numbers 8µs
// apart, so the per-seed assertion below was deciding on clock ticks rather than on work.
//
// Timing a batch and dividing puts the measured span two orders of magnitude above the coarse
// granularity, which is the same reasoning TestCrossingLatencyBudget already applies to a
// microsecond-scale operation. At 20 runs the thin batch is ~10ms against the full batch's ~30ms.
//
// That last sentence used to end "a 3x gap no plausible scheduling noise inverts", and CI run
// 32397340626 disproved it: on a two-core windows-latest runner running ~20 package binaries in
// parallel under `go test -count=2 -timeout=30m ./...`, seed 3 read thin 3.99988ms against full
// 1.82256ms while seed 2 in the same loop read thin 441.63µs against full 4.36204ms. Batching
// fixes the clock's granularity; it cannot fix co-load, because co-load scales the whole batch.
// Nothing is gated on these timings any more — the loop below asserts on Slice.EdgesVisited — and
// the batch survives only so that the number it LOGS is a cost and not a clock tick.
const batchRuns = 20

// sliceOnce runs one slice and returns it whole — the caller needs its EdgesVisited count as well
// as its scores — alongside the per-slice wall-clock cost, which is a diagnostic and nothing more.
func sliceOnce(tb testing.TB, g dag.Graph, criteria []dag.NodeID, thin bool) (dag.Slice, time.Duration) {
	tb.Helper()
	o := dag.DefaultSliceOptions(config.Defaults())
	o.Thin = thin
	o.Deadline = 0       // measure the walk, not the limiter
	o.MaxNodes = 1 << 20 // ditto

	// Validated once, outside the timed region, so the batch measures slicing rather than testify.
	sl, err := g.BackwardSlice(criteria, o)
	require.NoError(tb, err)
	require.False(tb, sl.Truncated, "the comparison must measure complete slices")

	var batchErr error
	start := time.Now()
	for range batchRuns {
		if _, err := g.BackwardSlice(criteria, o); err != nil {
			batchErr = err
			break
		}
	}
	elapsed := time.Since(start)
	require.NoError(tb, batchErr)

	return sl, elapsed / batchRuns
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

// missingSample caps how many offending node ids a broken subset relation prints. A thin walk that
// has stopped being a subset misses hundreds of nodes at this corpus size — the mutation used to
// check this assertion has teeth reported 678 of them on seed 1 — and a message that dumps all of
// them is a message nobody reads. The count printed beside the sample is the number that matters.
const missingSample = 5

// notReachedByFull returns every node the thin slice scored that the full slice did not, sorted so
// that a failure names the same node on every run rather than whichever one the map happened to
// yield first.
func notReachedByFull(thin, full map[dag.NodeID]float32) []dag.NodeID {
	var missing []dag.NodeID
	for id := range thin {
		if _, ok := full[id]; !ok {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return missing
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

		thin, thinNS := sliceOnce(t, g, criteria, true)
		full, fullNS := sliceOnce(t, g, criteria, false)

		row := compareRow{
			Seed:          seed,
			Nodes:         len(nodes),
			Edges:         len(edges),
			ThinSize:      len(thin.Scores),
			FullSize:      len(full.Scores),
			SizeRatio:     ratio(len(thin.Scores), len(full.Scores)),
			Recall:        ratio(overlap(thin.Scores, truth), len(truth)),
			Precision:     ratio(overlap(thin.Scores, truth), len(thin.Scores)),
			RecallFull:    ratio(overlap(full.Scores, truth), len(truth)),
			PrecisionFull: ratio(overlap(full.Scores, truth), len(full.Scores)),
		}
		table.Rows = append(table.Rows, row)
		sumRatio += row.SizeRatio
		sumRecall += row.Recall
		sumPrecision += row.Precision
		sumPrecisionFull += row.PrecisionFull

		t.Logf("seed %d: nodes=%d edges=%d thin=%d full=%d ratio=%.3f recall=%.3f precision=%.3f"+
			"  (edges walked: thin %d, full %d; wall clock: thin %v, full %v)",
			seed, len(nodes), len(edges), len(thin.Scores), len(full.Scores),
			row.SizeRatio, row.Recall, row.Precision,
			thin.EdgesVisited, full.EdgesVisited, thinNS, fullNS)

		// §6.4's other claim: thin slicing is the CHEAPER walk. It visits a subset of the edges
		// full slicing does, so anything else would mean the thin path is doing extra work.
		//
		// That subset is what is asserted, in edges and in nodes. It was asserted as thinNS <=
		// fullNS until CI run 32397340626, and a stopwatch cannot carry the claim: these are
		// single sub-millisecond spans measured inside `go test -count=2 -timeout=30m ./...`, ~20
		// package binaries deep on a two-core runner, so they report how busy the machine was and
		// not what the walk cost. That run failed seed 3 at thin 3.99988ms against full 1.82256ms
		// while seed 2, doing comparable work in the same loop, read thin 441.63µs against full
		// 4.36204ms — `full` alone swinging 10x between seeds is the measurement disqualifying
		// itself, and no threshold or best-of-N repair fixes a quantity that is mostly noise.
		//
		// A count is the same claim made stronger. Slice.EdgesVisited is exactly reproducible,
		// co-load cannot move it, and a subset relation — unlike a timing win — cannot come out
		// right by luck. The node check is the reason the edge check holds: thin only ever REMOVES
		// edges, so every thin path is also a full path and every full score is at least the thin
		// one (which is what stops the minScore floor from reversing the containment), so the thin
		// walk finalizes a subset of the nodes whose adjacency lists the full walk expands. A thin
		// slice reaching a node the full slice missed would break that argument, so it is checked
		// here rather than assumed.
		require.LessOrEqualf(t, thin.EdgesVisited, full.EdgesVisited,
			"seed %d: thin slicing must not follow more edges than full slicing (§6.4)", seed)
		missing := notReachedByFull(thin.Scores, full.Scores)
		require.Zerof(t, len(missing),
			"seed %d: the thin slice reached %d node(s) the full slice did not, starting %v; thin "+
				"only ever removes edges, so every node it reaches must also be reachable with "+
				"those edges still in place",
			seed, len(missing), missing[:min(len(missing), missingSample)])
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

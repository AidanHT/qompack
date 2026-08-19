package dag_test

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// This file is package dag_test, not package dag, and it has to be: it imports
// internal/dag/dagtest for the synthetic generator, and dagtest imports dag. An in-package test
// file importing dagtest would be an import cycle.

// benchSpec is the graph shape 00-ARCHITECTURE.md §7 names for these benchmarks — roughly 5,000
// nodes and 15,000 edges, which is §6.4's "a few thousand nodes" made concrete.
var benchSpec = dagtest.SynthSpec{
	Turns:                  400,
	ToolsPerTurn:           3,
	Files:                  40,
	SymbolsPerFile:         6,
	ControlOnlyFraction:    0.35,
	ControlCarriedFraction: 0.12,
	Supersessions:          30,
	TokensPerTool:          600,
}

// benchSeed fixes the generator so every run of every benchmark walks the identical graph.
// Comparing two benchstat runs over two different graphs would measure the generator, not the code.
const benchSeed = 7

// The budgets these benchmarks defend. §6.4 puts backward slicing at "sub-millisecond"; §8.4 needs
// CrossingEdges cheap enough for the scheduler to score every candidate cut point in one pass,
// which is what the microsecond budget encodes.
const (
	sliceBudget    = 1 * time.Millisecond
	crossingBudget = 5 * time.Microsecond
)

// Those budgets are claims about the shipped binary, and two of the ways this module is tested do
// not produce one. `devtool cover` builds with -covermode=atomic, which injects a sync/atomic add
// on every statement — including the ones in the relaxation loop slicing spends its time in.
// `devtool test-race` builds with the race detector, which instruments every memory access, and
// slicing is map-heavy. Measured on this repository, backward slicing over the same graph:
//
//	uninstrumented     0.40ms
//	-covermode=atomic  0.81-1.18ms   (~3x)
//	-race              2.02ms        (~5x)
//
// so asserting 1ms against either build gates on the measurement apparatus rather than on §6.4.
// The ceilings below are scaled by a factor per instrumentation, multiplied when both are on,
// each set above its measured inflation to absorb the noise those tools also add.
//
// The gates are scaled rather than skipped under instrumentation. That matters less than it looks,
// because `ci-local`'s `test` step runs uninstrumented and therefore asserts the real budget — but
// a skip would still be the wrong shape: Rule W-1 bans t.Skip here, and a gate that silently
// evaporates under -race is one nobody notices has stopped running. Scaled, they stay live as
// backstops against an order-of-magnitude regression: the orderByScore defect this file was
// written to catch cost 5.4x, which breaches every ceiling here.
const (
	coverageBudgetFactor = 4
	raceBudgetFactor     = 8
)

// budgetFor returns the ceiling to assert against for a budget stated against an uninstrumented
// build.
func budgetFor(base time.Duration) time.Duration {
	return base * time.Duration(budgetFactor())
}

func budgetFactor() int {
	factor := 1
	if testing.CoverMode() != "" {
		factor *= coverageBudgetFactor
	}
	if raceEnabled {
		factor *= raceBudgetFactor
	}
	return factor
}

// budgetNote annotates a breach with the build that produced it, so an instrumentation-only failure
// is not misread as a plain regression against the figures quoted in the ADR.
func budgetNote() string {
	if budgetFactor() == 1 {
		return ""
	}
	note := " (ceiling scaled " + strconv.Itoa(budgetFactor()) + "x for"
	if mode := testing.CoverMode(); mode != "" {
		note += " -covermode=" + mode
	}
	if raceEnabled {
		note += " -race"
	}
	return note + ")"
}

// benchGraph builds the standard benchmark graph and returns it with its criterion set.
func benchGraph(tb testing.TB) (dag.Graph, []dag.NodeID) {
	tb.Helper()
	nodes, edges, criteria, _ := dagtest.Synth(benchSeed, benchSpec)
	g, err := dag.Open(tb.TempDir(), config.Defaults(), logging.Nop())
	if err != nil {
		tb.Fatalf("dag.Open: %v", err)
	}
	dagtest.Load(tb, g, nodes, edges)
	return g, criteria
}

// unboundedOpts measures the real cost of a walk rather than the cost of hitting its own limiter.
// Deadline is cleared and MaxNodes raised well past the graph, so what the benchmark reports is the
// traversal, not DefaultDeadline expiring.
func unboundedOpts(thin bool) dag.SliceOptions {
	o := dag.DefaultSliceOptions(config.Defaults())
	o.Thin = thin
	o.Deadline = 0
	o.MaxNodes = 1 << 20
	return o
}

// warm forces the position index to be built, so a benchmark of a read path does not measure a
// one-off rebuild in its first iteration.
func warm(g dag.Graph) {
	_ = g.CrossingEdges(1)
	_ = g.NodesAfter(0)
}

func BenchmarkBackwardSlice5000(b *testing.B) {
	g, criteria := benchGraph(b)
	o := unboundedOpts(true)
	warm(g)
	b.ResetTimer()
	for range b.N {
		if _, err := g.BackwardSlice(criteria, o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBackwardSlice5000Full(b *testing.B) {
	g, criteria := benchGraph(b)
	o := unboundedOpts(false)
	warm(g)
	b.ResetTimer()
	for range b.N {
		if _, err := g.BackwardSlice(criteria, o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForwardSlice5000(b *testing.B) {
	g, criteria := benchGraph(b)
	o := unboundedOpts(true)
	warm(g)
	b.ResetTimer()
	for range b.N {
		if _, err := g.ForwardSlice(criteria, o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCrossingEdges(b *testing.B) {
	g, _ := benchGraph(b)
	warm(g)
	maxPos := g.Stats().MaxPos
	if maxPos <= 0 {
		b.Fatal("fixture sanity: the synthetic graph must span a prefix")
	}
	b.ResetTimer()
	i := 0
	for range b.N {
		// Sweep across the whole prefix rather than probing one position, so the binary searches
		// land in different parts of the sorted slices and the number is not a cache-resident best
		// case.
		_ = g.CrossingEdges(i % maxPos)
		i += 977 // a prime stride, so consecutive probes do not correlate
	}
}

func BenchmarkNodesAfter(b *testing.B) {
	g, _ := benchGraph(b)
	warm(g)
	maxPos := g.Stats().MaxPos
	if maxPos <= 0 {
		b.Fatal("fixture sanity: the synthetic graph must span a prefix")
	}
	b.ResetTimer()
	i := 0
	for range b.N {
		_ = g.NodesAfter(i % maxPos)
		i += 977
	}
}

// BenchmarkRebuildIndex measures the cost of the dirty path: one mutation invalidates the index,
// and the next read rebuilds it. rebuildIndexLocked is unexported and this is an external test
// package, so the rebuild is driven the way production drives it — through a read that follows a
// write — which is the cost that actually matters anyway.
func BenchmarkRebuildIndex(b *testing.B) {
	g, _ := benchGraph(b)
	warm(g)
	b.ResetTimer()
	i := 0
	for range b.N {
		b.StopTimer()
		// A fresh node dirties the index without changing the graph's size materially.
		id := dag.FileNode("bench/rebuild-" + strconv.Itoa(i))
		if err := g.AddNode(dag.Node{ID: id, Kind: dag.KindFile, Ref: "bench", Pos: i + 1}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_ = g.CrossingEdges(1)
		i++
	}
}

// TestSliceLatencyBudget is the paired gate for BenchmarkBackwardSlice5000: §6.4's
// "sub-millisecond" is a claim about this repository, so it fails the build rather than being
// left to a human reading benchstat output.
//
// The MINIMUM of 20 runs is asserted, not the median. The median was chosen so "one scheduler
// hiccup on a loaded CI box cannot fail the build", and it under-delivered exactly that intent:
// inside `go test ./...` this package runs concurrently with the whole tree — including
// test/integration's real-process hot-path suites — and sustained co-scheduling inflated more
// than half the samples, failing the build at a 1.22 ms median while the identical walk on the
// identical tree measures 0.34 ms quiet (V2-VERIFY, ci-local). The minimum estimates the
// uncontended cost, which is what §6.4 budgets: every regression class this gate exists to
// catch (orderByScore cost 5.4×) inflates the fastest sample along with the rest, and a host
// whose uncontended walk genuinely exceeds the ceiling still fails — the §2.7a rule that a slow
// host is a real signal is preserved. The ceiling, the sample count and the instrumentation
// scaling are unchanged.
func TestSliceLatencyBudget(t *testing.T) {
	g, criteria := benchGraph(t)
	warm(g)

	for _, tc := range []struct {
		name string
		run  func(dag.SliceOptions) error
	}{
		{"backward", func(o dag.SliceOptions) error { _, err := g.BackwardSlice(criteria, o); return err }},
		{"forward", func(o dag.SliceOptions) error { _, err := g.ForwardSlice(criteria, o); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := unboundedOpts(true)
			require.NoError(t, tc.run(o), "warm-up run")

			const runs = 20
			ds := make([]time.Duration, runs)
			for i := range runs {
				start := time.Now()
				require.NoError(t, tc.run(o))
				ds[i] = time.Since(start)
			}
			sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
			fastest := ds[0]
			ceiling := budgetFor(sliceBudget)
			require.Less(t, fastest, ceiling,
				"%s slice over %d nodes: fastest of %d runs %v exceeds %v, the ceiling for §6.4's sub-millisecond budget — not one uncontended sample fit%s",
				tc.name, g.Stats().Nodes, runs, fastest, ceiling, budgetNote())
		})
	}
}

// TestCrossingLatencyBudget is the paired gate for BenchmarkCrossingEdges.
//
// It times a batch and divides, rather than timing one call: a single CrossingEdges is a few
// microseconds, which is close enough to the clock's own resolution that a per-call measurement
// would be mostly noise. A batch of 10,000 is comfortably above it.
func TestCrossingLatencyBudget(t *testing.T) {
	g, _ := benchGraph(t)
	warm(g)
	maxPos := g.Stats().MaxPos
	require.Positive(t, maxPos, "fixture sanity: the synthetic graph must span a prefix")

	const batch = 10000
	start := time.Now()
	for i := range batch {
		_ = g.CrossingEdges((i * 977) % maxPos)
	}
	per := time.Since(start) / batch

	ceiling := budgetFor(crossingBudget)
	require.Less(t, per, ceiling,
		"CrossingEdges over %d edges: %v per call exceeds %v, the ceiling for §8.4's budget%s",
		g.Stats().Edges, per, ceiling, budgetNote())
}

// BenchmarkAddNodeEdgePair measures the primitive the hot-path budget is actually stated against:
// one AddNode plus one AddEdge, no flush. BenchmarkAddToolUse below measures a whole observed tool
// call, which is five of these plus a sharesState query, so the two numbers answer different
// questions and both are worth recording.
func BenchmarkAddNodeEdgePair(b *testing.B) {
	g, err := dag.Open(b.TempDir(), config.Defaults(), logging.Nop())
	if err != nil {
		b.Fatalf("dag.Open: %v", err)
	}

	b.ResetTimer()
	for i := range b.N {
		// The pending queue is drained with the timer STOPPED, so the auto-flush never fires
		// inside a timed iteration. That is deliberate and it is what the budget means by "no
		// flush": AddNode and AddEdge are mutation-path operations, and folding an amortized
		// 20ms disk write into their per-op cost would measure the filesystem rather than the
		// graph. The flush itself is measured by BenchmarkOpen20k's sibling on the write side.
		if i%benchDrainEvery == 0 && i > 0 {
			b.StopTimer()
			if err := g.Flush(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}

		id := dag.ToolUseNode(core.ToolUseID("toolu_" + strconv.Itoa(i)))
		res := dag.ToolResultNode(core.ToolUseID("toolu_" + strconv.Itoa(i)))
		if err := g.AddNode(dag.Node{ID: id, Kind: dag.KindToolUse, Pos: i * benchPosStride}); err != nil {
			b.Fatal(err)
		}
		if err := g.AddEdge(dag.Edge{From: id, To: res, Kind: dag.EdgeProduces, Weight: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

// benchDrainEvery is how often the mutation benchmarks drain their pending queue. It is comfortably
// below autoFlushRecords (2000) so that each drained batch is two records per iteration short of
// triggering an auto-flush on its own.
const benchDrainEvery = 500

// BenchmarkAddToolUse measures one observed tool call all the way through §8.1 item 4's edge set:
// two nodes, an assistant node, a file node, a symbol node and five edges. This runs inside the
// daemon's l0_process path, so it is the one benchmark here whose budget is a hot-path budget.
func BenchmarkAddToolUse(b *testing.B) {
	g, err := dag.Open(b.TempDir(), config.Defaults(), logging.Nop())
	if err != nil {
		b.Fatalf("dag.Open: %v", err)
	}

	b.ResetTimer()
	for i := range b.N {
		id := core.ToolUseID("toolu_" + strconv.Itoa(i))
		prev := core.ToolUseID("toolu_" + strconv.Itoa(i-1))
		if i == 0 {
			prev = ""
		}
		err := dag.BuildToolUse(g, dag.ObservedTool{
			ToolUseID:     id,
			PrevToolUseID: prev,
			PrevTurn:      core.TurnIndex(i),
			Turn:          core.TurnIndex(i + 1),
			Pos:           i * benchPosStride,
			ResultPos:     i*benchPosStride + benchResultOffset,
			Tool:          "Read",
			PathKey:       "src/pkg/file.go",
			Symbols:       []string{"Handler"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// The position spacing BenchmarkAddToolUse advances by. Neither value carries meaning beyond
// keeping successive tool uses at distinct, increasing positions.
const (
	benchPosStride    = 600
	benchResultOffset = 100
)

// BenchmarkOpen20k measures loading a ~19,500-record log — the session-start cost, which is off
// the hot path (00-ARCHITECTURE.md §7 puts it in the B-D band) but still bounds how long a cold
// daemon takes to become useful.
func BenchmarkOpen20k(b *testing.B) {
	root := b.TempDir()
	nodes, edges, _, _ := dagtest.Synth(benchSeed, benchSpec)

	g, err := dag.Open(root, config.Defaults(), logging.Nop())
	if err != nil {
		b.Fatalf("dag.Open: %v", err)
	}
	dagtest.Load(b, g, nodes, edges)
	if err := g.Flush(context.Background()); err != nil {
		b.Fatalf("Flush: %v", err)
	}
	b.Logf("log holds %d records", g.Stats().LogRecords)

	b.ResetTimer()
	for range b.N {
		if _, err := dag.Open(root, config.Defaults(), logging.Nop()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompact20k measures the idle-only rewrite. Each iteration needs a fresh graph, because
// Compact is not idempotent — the second call on the same graph finds no waste and no-ops — so the
// setup runs with the timer stopped.
func BenchmarkCompact20k(b *testing.B) {
	nodes, edges, _, _ := dagtest.Synth(benchSeed, benchSpec)
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		g, err := dag.Open(b.TempDir(), config.Defaults(), logging.Nop())
		if err != nil {
			b.Fatal(err)
		}
		dagtest.Load(b, g, nodes, edges)

		// Tombstone every third node, which puts the log well past Compact's 25% waste gate.
		dead := make([]dag.NodeID, 0, len(nodes)/3+1)
		for i, n := range nodes {
			if i%3 == 0 {
				dead = append(dead, n.ID)
			}
		}
		m, ok := g.(dag.Maintainer)
		if !ok {
			b.Fatal("dag.Open's result must satisfy dag.Maintainer")
		}
		if err := m.Tombstone(dead); err != nil {
			b.Fatal(err)
		}
		if err := g.Flush(ctx); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if err := g.Compact(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is V2-VERIFY §4.6: the hot path measured, and degraded, against real resident state.
//
// Two architectural decisions are stated up front, because both are forced by the wave-1
// composition rather than chosen for convenience:
//
//  1. Test 1's MEASURED daemon is the bench harness's own real `qompack daemon` child process
//     (test/bench/hotpath starts it itself and owns its lifetime), and in wave 1 that binary
//     binds no ObserveTool — internal/cli/daemon.go wires nil Services because the observer is
//     SP-08's, exactly as V2-SP05-16's nil-service tolerance pins. The §4.2 ObserveTool binding
//     therefore lives where it CAN exist: the in-process daemon that builds the resident state
//     (startHookflowDaemon, the same seam §4.2 exercises), with the binding's liveness proven on
//     the real wire before the bulk drive. The measured child daemon then runs over that
//     project: the 40MB store and the DAG resident on disk under the same .qompack/, and the
//     sketch set genuinely resident in the measured process — daemon.Run loads
//     sketches/tried.bloom, touch.cms and explore.hll unconditionally at startup
//     (internal/daemon/daemon.go, Sketches.Load). This is the honest V2 form of "start a real
//     daemon over a project pre-populated with real state"; when SP-08 lands a production
//     ObserveTool, the same harness run exercises it with no change here.
//
//  2. Test 2's 25ms stall is a CLOCK stall, not a wall-clock sleep. §6.1 bans time.Sleep outside
//     test/bench (sleepcheck), and internal/daemon's own tests simulate a slow hot path exactly
//     this way: a controllable clock whose recvTS−reqTS gap the breach detector reads
//     (daemon_test.go's feedBreachingWindow constructs the same relationship by hand). Here the
//     bound ObserveTool consumes 25ms of the daemon's own clock per event while the stall is
//     switched on, and the driver stamps each request before the previous event's stall has been
//     consumed — so the daemon's TS-anchored estimate for the next sample is the stall, measured
//     by the same recordHotPathSample path production uses. A wall sleep would have measured the
//     host scheduler; the clock stall measures the mechanism.
//
// Deviations from §4.6's prose, with reasons, all reported in the completion report:
//
//   - "~5,000 nodes / ~15,000 edges": the §8.1 item-4 emission for 2,000 tool-use records is
//     3 nodes per record plus file and symbol nodes, so the real builder lands near 6,200 nodes
//     and 16,000 edges. Both spec figures are asserted as FLOORS (>= 5,000 / >= 15,000) and the
//     exact counts are logged; shrinking the corpus to hit "~5,000" exactly would mean fewer
//     than the 2,000 records the same sentence requires.
//   - The resident tried.bloom is seeded through sketch.ReplaceGenerational — §7.4's one
//     sanctioned door, the same one §4.7's test uses — because its production writer
//     (negknow.RebuildBloom, SP-09) is wave 3. Its content is a deterministic seed set; §4.4
//     already pins that nothing may be asserted about negative-knowledge records in V2.

// ─────────────────────────────────────────────────────────────────────────────────────────────
// The §4.6 corpus: sizes, identities and deterministic content
// ─────────────────────────────────────────────────────────────────────────────────────────────

const (
	// hotpathSession is the session every pre-population event belongs to.
	hotpathSession = core.SessionID("sess-hotpath-warm")
	// hotpathStallSession is test 2's session for the warm, stall and degraded phases.
	hotpathStallSession = core.SessionID("sess-hotpath-stall")

	// hotpathEventTotal is §4.6's "2,000 tool-use records".
	hotpathEventTotal = 2000
	// hotpathBenchIterations is §4.6's "2,000 real process spawns", forwarded to the harness as
	// --iterations. Numerically equal to hotpathEventTotal today, but a different quantity — the
	// same distinction test/bench/hotpath itself draws between its warm-up and measurement counts.
	hotpathBenchIterations = 2000
	// hotpathRawFloorBytes is §4.6's "40 MB of raw tool output in the real store", asserted
	// against store.Stats().RawBytes (the pre-dedup, pre-compression total).
	hotpathRawFloorBytes = 40 << 20
	// hotpathNodeFloor / hotpathEdgeFloor are §4.6's "~5,000 nodes / ~15,000 edges", asserted as
	// floors — see the file comment for why the real emission for 2,000 records sits above both.
	hotpathNodeFloor = 5000
	hotpathEdgeFloor = 15000

	// hotpathPathCount is how many distinct .ts files the corpus reads; each event i touches
	// path i % hotpathPathCount, so every path accumulates ~50 versions.
	hotpathPathCount = 40
	// hotpathFuncsPerFile is how many top-level `export function` declarations every corpus file
	// carries — the real extractor must find exactly this many per path, and each contributes one
	// shared-symbol edge per event on that path (2,000 × 5 = 10,000 of the edge floor).
	hotpathFuncsPerFile = 5
	// hotpathEventContentBytes is each event's raw tool output size. Nothing in the content is
	// strippable by the canonicalizer (comments and plain identifiers, no timestamps, no hex
	// runs), so canonical bytes ≈ raw bytes and 2,000 × 21.5KB clears the 40MB floor with margin.
	hotpathEventContentBytes = 21504

	// hotpathWireEvents is how many pre-population events are driven through the REAL transport
	// (ipc.Client → daemon → bound ObserveTool) before the bulk is driven through the same bound
	// func value directly — §4.2's own two-lane idiom. The wire tranche is what PROVES the
	// binding is the function the daemon dispatches, not merely a function the test holds.
	hotpathWireEvents = 16

	// hotpathBloomSeedEntries is the deterministic entry count seeded into the resident
	// tried.bloom (~10% of the configured 10,000 capacity — a realistically non-empty filter).
	hotpathBloomSeedEntries = 1024
)

// The on-disk floors for the three §4.6 sketch files ("12 KB bloom, 54 KB CMS, 2 KB HLL"), left
// deliberately below the exact encodings so a header/CRC change cannot fail them; exact sizes are
// logged for the completion report.
const (
	hotpathBloomFileFloor = 10 << 10
	hotpathCMSFileFloor   = 40 << 10
	hotpathHLLFileFloor   = 3 << 8 // 1.5KB spelled without a fractional shift: 3 × 512
)

// ─────────────────────────────────────────────────────────────────────────────────────────────
// Bounds. Per V2-MERGE-25 ② every daemon-mechanism wait derives from the exported constant in
// internal/daemon/timing.go that governs the mechanism being waited on; *Tick values are poll
// cadences, not bounds.
// ─────────────────────────────────────────────────────────────────────────────────────────────

const (
	// hotpathReplyBudget is the connect/ACK/reply budget for this file's own in-test clients —
	// the same basis §4.2 states for hookflowClientBudget: the daemon's own per-line worst case.
	hotpathReplyBudget = daemon.DrainLineDeadline

	// hotpathPipelineBound bounds one event's trip through the worker pool into the binding: a
	// dispatch in flight is bounded by the same per-line ceiling a drained line runs under.
	hotpathPipelineBound = daemon.DrainLineDeadline
	hotpathPipelineTick  = time.Millisecond

	// hotpathStopBound bounds a clean daemon shutdown: twice Stop's own bound on draining the
	// in-flight ring (the same basis §4.7's cdwStopBound states).
	hotpathStopBound = 2 * daemon.StopDrainBound

	// hotpathHarnessBound is the wall ceiling on one whole bench-harness run — a subprocess
	// lifetime like §4.4's replayMaxWall, not a daemon mechanism, so it cannot derive from
	// timing.go: 2,250 measured spawns plus warm-up, two `go build`s and daemon start/stop sit
	// under two minutes on this class of host; six is headroom, and a run that needs more is a
	// hung mechanism the harness's own internal bounds should have already named.
	hotpathHarnessBound = 6 * time.Minute

	// hotpathStatePollTick is the cadence at which test 1 samples state.bin while the harness
	// runs, watching for a spool transition the logs would also record.
	hotpathStatePollTick = 50 * time.Millisecond
)

// ─────────────────────────────────────────────────────────────────────────────────────────────
// Test 2's stall vocabulary
// ─────────────────────────────────────────────────────────────────────────────────────────────

const (
	// hotpathSampleWindow mirrors internal/daemon/budget.go's sampleWindow (§2.4's "rolling
	// 512-sample HDR histogram per hook") the same way §4.7's ao* constants mirror unexported
	// internal names: the window arithmetic below is meaningless without it.
	hotpathSampleWindow = 512

	// hotpathStallLatency is §4.6's "artificial 25 ms stall".
	hotpathStallLatency = 25 * time.Millisecond

	// hotpathStallWarmEvents is the clean wire tranche sent before the stall switches on, proving
	// the daemon serves in sync submode first. Deliberately far below one sample window, so the
	// first breaching window still closes with an overwhelming majority of stalled samples.
	hotpathStallWarmEvents = 8

	// hotpathStallSendCap bounds the stall loop: three full windows plus slack for the
	// asynchronous window-closure worker (hotPathWorker) to land the transition and NAK the next
	// send. Reaching the cap without a NAK fails the test — it means three consecutive breaching
	// windows did NOT flip the daemon.
	hotpathStallSendCap = 3*hotpathSampleWindow + hotpathSampleWindow/4

	// hotpathDegradedHooks is how many real-binary hooks are spawned while the daemon is degraded
	// (each must exit 0 and spool without connecting).
	hotpathDegradedHooks = 8

	// hotpathDegradedMsg is applyHotPathTransition's WARN/LOUD message, pinned verbatim (the same
	// string internal/daemon's TestHotModeTransitionWritesStateAndNAKs pins).
	hotpathDegradedMsg = "hot path degraded to spool submode"

	// hotpathDegradedCounter is the transition counter the status payload must report
	// (internal/daemon/handlers.go's counterHotpathDegraded).
	hotpathDegradedCounter = "hotpath_degraded"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────
// Deterministic corpus content
// ─────────────────────────────────────────────────────────────────────────────────────────────

// hotpathFillerWords is the vocabulary the filler lines rotate through — plain lowercase words,
// nothing the redactor flags and nothing (no timestamps, durations, or hex-address runs) the
// canonicalizer strips, so canonical size tracks raw size.
var hotpathFillerWords = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
}

// hotpathPathIdx maps event i onto its file.
func hotpathPathIdx(event int) int { return event % hotpathPathCount }

// hotpathFilePath is the project-relative .ts path event i reads — already in paths.Key form.
func hotpathFilePath(event int) string {
	return fmt.Sprintf("src/svc/handler%02d.ts", hotpathPathIdx(event))
}

// hotpathEventID is pre-population event i's tool_use id.
func hotpathEventID(event int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("toolu_hotpath_%04d", event))
}

// hotpathFuncName is the k-th exported function of pathIdx's file — a valid TypeScript
// identifier, stable per path so symbol nodes deduplicate per (path, name) exactly as
// dag.SymbolNode keys them.
func hotpathFuncName(pathIdx, k int) string {
	return fmt.Sprintf("handler%02dOp%d", pathIdx, k)
}

// hotpathFillerPhrase returns a deterministic 8-word phrase for (event, line). The phrase alone
// repeats across the corpus; the caller's line prefix (event and line numbers) makes every LINE
// unique, so content-defined chunking sees genuinely distinct bytes per event rather than one
// repeated block that would dedup the 40MB away.
func hotpathFillerPhrase(event, line int) string {
	const phraseWords = 8
	words := make([]string, phraseWords)
	start := (event*13 + line*7) % len(hotpathFillerWords)
	for w := 0; w < phraseWords; w++ {
		words[w] = hotpathFillerWords[(start+w)%len(hotpathFillerWords)]
	}
	return strings.Join(words, " ")
}

// hotpathFileContent builds event i's raw tool output: hotpathFuncsPerFile top-level exported
// functions (the shape §5.22b's extractor yields a named symbol for — the same property
// hookflowAuthTS documents) followed by unique comment filler up to hotpathEventContentBytes.
// Fully deterministic in i, per the §4 conventions (seeded/deterministic pre-population).
func hotpathFileContent(event int) string {
	pathIdx := hotpathPathIdx(event)
	var b strings.Builder
	b.Grow(hotpathEventContentBytes + 256)
	fmt.Fprintf(&b, "// %s - qompack V2-VERIFY hotpath fixture, event %04d.\n\n", hotpathFilePath(event), event)
	for k := 0; k < hotpathFuncsPerFile; k++ {
		fmt.Fprintf(&b, "export function %s(input: string): string {\n  return input + '_%02d_%d';\n}\n\n",
			hotpathFuncName(pathIdx, k), pathIdx, k)
	}
	line := 0
	for b.Len() < hotpathEventContentBytes {
		fmt.Fprintf(&b, "// pad event %04d line %04d %s\n", event, line, hotpathFillerPhrase(event, line))
		line++
	}
	return b.String()
}

// hotpathProbeContent is the small distinct content test 2's warm/stall/degraded probes ingest —
// real store work per event, kept tiny so the stall (not PutBytes) dominates each iteration.
func hotpathProbeContent(kind string, i int) string {
	return fmt.Sprintf("// %s probe %04d\nexport const probe_%s_%04d = 'qompack';\n", kind, i, kind, i)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// Shared setup helpers
// ─────────────────────────────────────────────────────────────────────────────────────────────

// hotpathSeedTriedBloom seeds sketches/tried.bloom through §7.4's one sanctioned door
// (sketch.ReplaceGenerational — negknow.RebuildBloom's own mechanism, exactly as §4.7's test
// seeds it), so the daemon under measurement loads a real, decodable, non-empty resident Bloom.
func hotpathSeedTriedBloom(t *testing.T, p *testutil.Project) {
	t.Helper()
	seed := sketch.NewBloom(p.Cfg.Sketches.Bloom.Capacity, p.Cfg.Sketches.Bloom.FPRate)
	for i := 0; i < hotpathBloomSeedEntries; i++ {
		seed.Add([]byte(fmt.Sprintf("hotpath-tried-%04d", i)))
	}
	triedPath := filepath.Join(paths.Of(p.Root).Sketches, "tried.bloom")
	_, err := sketch.ReplaceGenerational(triedPath, seed, 1)
	require.NoError(t, err, "seeding the resident tried.bloom through sketch.ReplaceGenerational")
}

// hotpathWaitPipeline waits until want DISTINCT events are fully through the bound pipeline —
// the §4.2 completion measure (an observation is recorded only after every side effect, sketches
// included) — then pins the count exactly.
func hotpathWaitPipeline(t *testing.T, hp *hookflowPipeline, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return hp.uniqueObservedCount() >= want },
		hotpathPipelineBound, hotpathPipelineTick,
		"%d/%d distinct events through the binding within daemon.DrainLineDeadline — an in-flight "+
			"dispatch is bounded by the same per-line ceiling a drained line runs under, so exceeding "+
			"it means an event was lost, not delayed (pipeline errors: %v)",
		hp.uniqueObservedCount(), want, hp.takeErrs())
	require.Equal(t, want, hp.uniqueObservedCount())
}

// hotpathPopulate drives the §4.6 pre-population corpus through the §4.2 binding. sendWire, when
// non-nil, carries the first hotpathWireEvents events over the real transport; every other event
// drives the bound func value directly — literally the same function a daemon dispatch calls, the
// idiom §4.2's own store-focused tests establish.
func hotpathPopulate(t *testing.T, ctx context.Context, hp *hookflowPipeline, sendWire func(ev hookio.Event)) {
	t.Helper()
	for i := 0; i < hotpathEventTotal; i++ {
		ev := hookflowEvent(t, hotpathSession, hotpathEventID(i), hotpathFilePath(i), hotpathFileContent(i))
		if sendWire != nil && i < hotpathWireEvents {
			sendWire(ev)
			continue
		}
		require.NoError(t, hp.ObserveTool(ctx, ev), "pre-population event %d must ingest", i)
	}
	hotpathWaitPipeline(t, hp, hotpathEventTotal)
	require.Empty(t, hp.takeErrs(), "no pre-population event may fail inside the binding")
}

// hotpathEnrichSymbols records the corpus's real symbol structure into the DAG: the REAL
// extractor (symbols.New) runs over each path's real ingested content, and the names feed
// dag.BuildToolUse alongside the byte-identical recorded observation — §4.3's "the caller
// resolves, dag never imports symbols" convention, and the §8.1 item-4 shape SP-08's observer
// will emit once it passes Symbols. BuildToolUse is idempotent for the already-recorded halves,
// so this adds exactly the symbol nodes and shared-symbol edges.
func hotpathEnrichSymbols(t *testing.T, hp *hookflowPipeline) {
	t.Helper()
	ext := symbols.New()
	byID := make(map[core.ToolUseID]dag.ObservedTool, hotpathEventTotal)
	for _, o := range hp.snapshotObserved() {
		byID[o.ToolUseID] = o
	}

	namesByPath := make(map[int][]string, hotpathPathCount)
	for i := 0; i < hotpathEventTotal; i++ {
		pathIdx := hotpathPathIdx(i)
		names, ok := namesByPath[pathIdx]
		if !ok {
			// The declarations are identical for every event on a path by construction, so the
			// extractor runs once per path, over that path's own real content.
			syms := ext.Extract(hotpathFilePath(i), []byte(hotpathFileContent(i)))
			for _, s := range syms {
				names = append(names, s.Name)
			}
			require.Len(t, names, hotpathFuncsPerFile,
				"the real extractor must find exactly the %d exported functions of %s — a shortfall "+
					"here would silently shrink the §4.6 edge corpus", hotpathFuncsPerFile, hotpathFilePath(i))
			namesByPath[pathIdx] = names
		}

		o, seen := byID[hotpathEventID(i)]
		require.True(t, seen, "event %d has no recorded observation to enrich", i)
		o.Symbols = names
		require.NoError(t, dag.BuildToolUse(hp.graph, o))
	}
}

// hotpathAssertResidentState pins the §4.6 pre-population postconditions and logs the exact
// numbers for the completion report.
func hotpathAssertResidentState(t *testing.T, ctx context.Context, p *testutil.Project, hp *hookflowPipeline) {
	t.Helper()

	require.NoError(t, hp.graph.Flush(ctx), "the DAG must persist to dag/deps.jsonl")
	require.NoError(t, hp.store.Flush(ctx), "the store must persist its buffered writes")

	stats, err := hp.store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, hotpathEventTotal, stats.ToolUses, "§4.6: 2,000 tool-use records in the real store")
	require.GreaterOrEqual(t, stats.RawBytes, int64(hotpathRawFloorBytes),
		"§4.6: 40 MB of raw tool output in the real store (got %d bytes)", stats.RawBytes)

	gs := hp.graph.Stats()
	require.GreaterOrEqual(t, gs.Nodes, hotpathNodeFloor, "§4.6: a real DAG of ~5,000 nodes (floor)")
	require.GreaterOrEqual(t, gs.Edges, hotpathEdgeFloor, "§4.6: ~15,000 edges (floor)")

	depsPath := filepath.Join(paths.Of(p.Root).DAG, "deps.jsonl")
	fi, err := os.Stat(paths.Long(depsPath))
	require.NoError(t, err, "dag/deps.jsonl must exist on disk after Flush")
	require.Positive(t, fi.Size())

	t.Logf("§4.6 resident state: ToolUses=%d RawBytes=%d (%.1f MB) StoredBytes=%d DedupRatio=%.2f "+
		"Objects=%d Files=%d; DAG Nodes=%d Edges=%d LogBytes=%d",
		stats.ToolUses, stats.RawBytes, float64(stats.RawBytes)/(1<<20), stats.Bytes, stats.DedupRatio,
		stats.Objects, stats.Files, gs.Nodes, gs.Edges, gs.LogBytes)
}

// hotpathAssertSketchFiles asserts the three §4.6 sketch files exist on disk at their real sizes
// (the exact bytes the measured daemon's Sketches.Load reads at startup) and logs them.
func hotpathAssertSketchFiles(t *testing.T, p *testutil.Project) {
	t.Helper()
	dir := paths.Of(p.Root).Sketches
	sizes := map[string]int64{}
	for name, floor := range map[string]int64{
		"tried.bloom": hotpathBloomFileFloor,
		"touch.cms":   hotpathCMSFileFloor,
		"explore.hll": hotpathHLLFileFloor,
	} {
		fi, err := os.Stat(paths.Long(filepath.Join(dir, name)))
		require.NoError(t, err, "sketches/%s must be on disk for the measured daemon to load", name)
		require.GreaterOrEqual(t, fi.Size(), floor, "sketches/%s is implausibly small for the real sketch", name)
		sizes[name] = fi.Size()
	}
	t.Logf("§4.6 resident sketch set on disk: tried.bloom=%dB touch.cms=%dB explore.hll=%dB",
		sizes["tried.bloom"], sizes["touch.cms"], sizes["explore.hll"])
}

// hotpathNewClient is this file's one client constructor: a real ipc.Client over the project's
// real spool, with the explicit State a warm hook would have read (the §4.7 idiom), except where
// a test deliberately re-reads state.bin to prove what a FRESH hook would do.
func hotpathNewClient(t *testing.T, p *testutil.Project, st ipc.State) ipc.Client {
	t.Helper()
	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	spool, err := ipc.NewSpool(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, spool, p.Log, nil, ipc.ClientOptions{
		ProjectRoot:     p.Root,
		State:           st,
		ConnectDeadline: hotpathReplyBudget,
		AckDeadline:     hotpathReplyBudget,
	})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// hotpathCountDegradedWarns counts `level=warn` day-log lines carrying the spool-transition
// message, across every qompack-*.log in the project (rotations included). The Loud copy of the
// same message renders level=loud and is deliberately not counted — §4.6's "one WARN line" names
// the WARN.
func hotpathCountDegradedWarns(t *testing.T, p *testutil.Project) int {
	t.Helper()
	// The plain path, not paths.Long: filepath.Glob does not accept an extended-length prefix as
	// a pattern, and internal/daemon's own readDayLogs globs the same directory the same way.
	pattern := filepath.Join(paths.Of(p.Root).Logs, "qompack-*.log")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err)
	n := 0
	for _, f := range files {
		b, readErr := os.ReadFile(f)
		require.NoError(t, readErr)
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "level=warn") && strings.Contains(line, hotpathDegradedMsg) {
				n++
			}
		}
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// The bench harness artifact shape (test/bench/hotpath is package main and cannot be imported;
// these mirror its Report/BudgetRow/SpawnFloor JSON verbatim, the way §4.4 mirrors the replay
// driver's growth-file shape).
// ─────────────────────────────────────────────────────────────────────────────────────────────

type hotpathBudgetRow struct {
	BudgetID string   `json:"budget_id"`
	N        int      `json:"n"`
	P50      float64  `json:"p50"`
	P95      float64  `json:"p95"`
	P99      float64  `json:"p99"`
	P999     float64  `json:"p999"`
	Max      float64  `json:"max"`
	LimitMs  *float64 `json:"limit_ms"`
	Pass     *bool    `json:"pass"`
}

type hotpathSpawnFloor struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

type hotpathBenchReport struct {
	Platform     string             `json:"platform"`
	N            int                `json:"n"`
	BAMethod     string             `json:"b_a_method"`
	SpawnFloorMs hotpathSpawnFloor  `json:"spawn_floor_ms"`
	Notes        []string           `json:"notes"`
	Budgets      []hotpathBudgetRow `json:"budgets"`
}

// hotpathSpawnEstimateID is the harness's renamed wall-clock diagnostic row (ruling #29:
// "rename their keys so nothing reads them as B-A").
const hotpathSpawnEstimateID = "B-A_spawn_estimate"

// hotpathBECPURowID mirrors test/bench/hotpath's own budgetIDBECPU: B-E measured in the CPU time
// the 50 `qompack checkpoint` children consumed rather than the wall time they waited. The harness
// is package main and cannot be imported, so this file carries the literal — the same reason
// hotpathSpawnEstimateID above carries its own.
const hotpathBECPURowID = "B-E_cpu"

// hotpathCheckpointSamples mirrors the harness's own checkpointIterations — task-7-spec.md step
// 7's "spawn qompack checkpoint 50 times", the population BOTH B-E rows are built from. Asserting
// it is what keeps "the CPU row passed" from being satisfiable by a row built from a handful of
// samples: the two rows must cover the same 50 children, in two clocks.
const hotpathCheckpointSamples = 50

// hotpathUnderColoadFlag is the harness's --under-coload flag (test/bench/hotpath/main.go,
// parseFlags), passed iff obs.UnderCoload() — the job's declaration, forwarded, never this test's
// own guess about who is running it. Both waiver notes the harness writes name the flag verbatim,
// so the same string is what the notes assertions look for.
const hotpathUnderColoadFlag = "--under-coload"

// hotpathBAWaiverMark is the opening of the harness's baWallWaivedNote (test/bench/hotpath/
// report.go) — the one phrase that note carries and no other note the harness writes does (B-E's
// waiver says "wall-clock row"; the tail-adjustment notes say "p99"). Spelled after obs.BA so the
// row's name is never a second literal here.
const hotpathBAWaiverMark = string(obs.BA) + "'s row is REPORTED, not gated"

// hotpathLimitDeltaMs is the tolerance for comparing a row's limit_ms against obs.Budgets(): the
// artifact renders limits in whole milliseconds, so anything under a microsecond is a float
// rendering difference, never a different budget.
const hotpathLimitDeltaMs = 0.001

// hotpathNotesMention reports whether any note in the artifact mentions sub — used to assert that
// a disclosure the harness owes the reader was actually emitted, without this file re-spelling the
// harness's prose (which would then have to be kept in sync with it).
func hotpathNotesMention(rep hotpathBenchReport, sub string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// hotpathRequireGatedRow asserts the shape bench-gate reads for every row it judges: the row
// carries limitMs as its limit, a pass verdict, the verdict is true, and its p99 is under the
// limit — the last restated here rather than trusted from the verdict, so the number the harness
// judged and the number the artifact shows can never disagree unnoticed. what names the row in
// the failure message.
func hotpathRequireGatedRow(t *testing.T, row hotpathBudgetRow, limitMs float64, what string) {
	t.Helper()
	require.NotNil(t, row.LimitMs, "%s is gated in this run and must carry its limit", what)
	require.InDelta(t, limitMs, *row.LimitMs, hotpathLimitDeltaMs,
		"%s's limit must be the one obs.Budgets() defines, not a private copy", what)
	require.NotNil(t, row.Pass, "%s is gated in this run and must carry a pass verdict", what)
	require.True(t, *row.Pass,
		"%s gate must pass against the real resident state (p99=%.3fms, limit %.0fms)", what, row.P99, limitMs)
	require.Less(t, row.P99, limitMs, "§4.6: %s p99 < %.0fms with the real store/DAG/sketches resident",
		what, limitMs)
}

// hotpathRequireReportedRow asserts the shape the harness gives a row it REPORTED under
// --under-coload: limit_ms and pass both null, exactly B-D's. A row that came back with pass=true
// here would mean the flag had stopped taking effect and the wall-clock gate was silently back,
// judging the runner's spare capacity; pass=false would mean the same thing having already
// failed. Both are caught. limitMs is the limit still applied elsewhere, for the message.
func hotpathRequireReportedRow(t *testing.T, row hotpathBudgetRow, limitMs float64, what string) {
	t.Helper()
	require.Nil(t, row.LimitMs,
		"%s must leave %s REPORTED (limit_ms null) in this run's artifact; it is still gated at %.0fms "+
			"by every run that does not pass the flag", hotpathUnderColoadFlag, what, limitMs)
	require.Nil(t, row.Pass, "%s must leave %s REPORTED (pass null) in this run's artifact",
		hotpathUnderColoadFlag, what)
}

// hotpathRow returns the report row for id, failing the test if the artifact does not carry it.
func hotpathRow(t *testing.T, rep hotpathBenchReport, id string) hotpathBudgetRow {
	t.Helper()
	for _, row := range rep.Budgets {
		if row.BudgetID == id {
			return row
		}
	}
	t.Fatalf("the bench artifact carries no %q row; budgets present: %+v", id, rep.Budgets)
	return hotpathBudgetRow{}
}

// hotpathBudgetLimitMs reads id's configured limit from obs.Budgets() against p.Cfg — the same
// single source of truth the harness gates on, never a re-hardcoded number.
func hotpathBudgetLimitMs(t *testing.T, p *testutil.Project, id obs.BudgetID) float64 {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return float64(b.Limit(p.Cfg).Microseconds()) / 1000.0
		}
	}
	t.Fatalf("obs.Budgets() does not define %s", id)
	return 0
}

// hotpathBuildBenchBinary compiles ./test/bench/hotpath and returns the executable — the exact
// §4.4 pattern (buildReplayDriver): the harness is package main, so it is driven as the process
// CI drives, flags and exit codes included; initialEnv keeps the pinned pre-test HOME so `go
// build` resolves the real module cache.
func hotpathBuildBenchBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bench-hotpath")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./test/bench/hotpath")
	cmd.Dir = growthModuleRoot(t)
	cmd.Env = initialEnv
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build -o %s ./test/bench/hotpath:\n%s", out, stderr.String())
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// §4.6 test 1 — TestIntegration_HotPathWarmWithRealResidentState
// ─────────────────────────────────────────────────────────────────────────────────────────────

// TestIntegration_HotPathWarmWithRealResidentState drives test/bench/hotpath, asserted rather
// than only reported: the project is pre-populated with the §4.6 resident state through the bound
// §4.2 ObserveTool, and the harness then runs its 2,000 real process spawns of the real hook
// binary against a real daemon started over that project (see the file comment for the wave-1
// composition). B-B p99 < 2ms and B-E's CPU-time p99 < 2000ms are asserted from the JSON artifact
// on every run; b_a_method must name the TS-anchored hook_controlled estimate (ruling #29);
// spawn_floor_ms must be present; and the daemon must never transition to spool during the run.
// B-A p99 < 15ms and B-E's wall-clock p99 < 2000ms are judged here only when the invoking job has
// not declared the run co-loaded (obs.UnderCoload — ci.yml's `timing` job runs this test alone
// for exactly that), and asserted REPORTED-and-disclosed when it has; see the comment above the
// harness invocation.
func TestIntegration_HotPathWarmWithRealResidentState(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)

	// ── Phase A: the resident state, built through the §4.2 binding. ──
	hotpathSeedTriedBloom(t, p)
	hp := newHookflowPipeline(t, p)
	d := startHookflowDaemon(t, p, hp)

	client := hotpathNewClient(t, p, ipc.State{Mode: contract.ModeFull, DaemonEnabled: true})
	wireSent := 0
	hotpathPopulate(t, ctx, hp, func(ev hookio.Event) {
		resp, sendErr := client.Send(ctx, ipc.Request{
			Op:      ipc.OpObserveTool,
			Session: hotpathSession,
			TS:      core.NowMilli(p.Clock),
			Event:   &ev,
		}, hotpathReplyBudget)
		require.NoError(t, sendErr)
		require.True(t, resp.OK, "wire event %d must be ACKed live — the bound ObserveTool is what "+
			"the daemon dispatches, and this tranche is the proof", wireSent)
		wireSent++
	})
	require.Equal(t, hotpathWireEvents, wireSent)
	require.Zero(t, clientSpoolLines(t, p.Root),
		"zero spool lines: every wire event must have reached the live daemon")

	hotpathEnrichSymbols(t, hp)
	hotpathAssertResidentState(t, ctx, p, hp)

	// A clean stop persists the written sketches (touch.cms, explore.hll) and releases the
	// project — Stop is synchronous and idempotent, and startHookflowDaemon's own cleanup
	// tolerates a Run that has already returned.
	require.NoError(t, d.Stop(ctx), "the pre-population daemon must stop cleanly")
	require.NoFileExists(t, paths.Long(ipc.StatePath(p.Root)),
		"a stopped daemon removes state.bin, freeing the project for the measured daemon")
	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(p.Root).Run, "daemon.lock")),
		"a stopped daemon releases daemon.lock, freeing the project for the measured daemon")
	hotpathAssertSketchFiles(t, p)

	// ── Phase B: the measured run — 2,000 real spawns against a real daemon over that state. ──
	bench := hotpathBuildBenchBinary(t)
	jsonPath := filepath.Join(t.TempDir(), "v2-bench-hotpath.json")

	// Watch state.bin for a spool transition while the harness runs. Observations count only
	// when a daemon has actually written the record (DaemonPID set) — ReadState's config
	// fallback for a missing file also reports HotSync and would otherwise dilute the watch.
	pollStop := make(chan struct{})
	var pollWG sync.WaitGroup
	var sawDaemonState, sawSpool atomic.Bool
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		tick := time.NewTicker(hotpathStatePollTick)
		defer tick.Stop()
		for {
			select {
			case <-pollStop:
				return
			case <-tick.C:
				st := ipc.ReadState(p.Root, p.Cfg)
				if st.DaemonPID != 0 {
					sawDaemonState.Store(true)
					if st.Hot == ipc.HotSpool {
						sawSpool.Store(true)
					}
				}
			}
		}
	}()

	// --under-coload is a statement of fact about the RUN'S ENVIRONMENT, and this test cannot make
	// it from inside: a Go test binary cannot tell whether it is one of ~20 the whole-tree
	// `go test -race ./...` / `-count=2 ./...` job runs concurrently on a 2-core GitHub runner, or
	// the only binary on a runner doing nothing else — and it spawns 2,250 more processes of its
	// own either way. The declaration therefore comes from the invoking job, through
	// obs.UnderCoload (QOMPACK_UNDER_COLOAD, internal/obs/coload.go): ci.yml's `test` job sets it,
	// and its `timing` job — which runs this test BY NAME, alone, on its own runner — does not.
	// The flag is forwarded to the harness iff the job declared it, and unset can only ever make a
	// run STRICTER.
	//
	// Under co-load a wall-clock sample stops being a measurement of the product. The evidence is
	// CI's own: on windows-latest the bench-gate job, which runs this same harness alone on its
	// own runner, reported B-E's wall p99 at 67.2 ms against the 2000 ms limit (ubuntu 232.1,
	// macos 21.3), and minutes later, same commit and same runner class, this test reported
	// 4302 ms from inside the whole-tree job — a 64x move with the product byte-identical. B-A,
	// one commit, same runner class: 3.072 ms in bench-gate, then 11.264 and 18.432 ms in two
	// whole-tree runs minutes apart, against 15 ms, while B-B — no process boundary inside it —
	// moved 0.576 → 0.768 / 0.704 ms. That was item 18/22/25's genus one more time
	// (plans/V2-report.md §0), and the audit V2-MERGE-25 asked for and never got: a wall-clock
	// bound failing on a correct product because it is priced against a host that is no longer
	// there.
	//
	// The flag does not remove either bound. For B-E it moves the JUDGEMENT to the clock that
	// survives co-load: the harness's B-E_cpu row (budgetIDBECPU, test/bench/hotpath/report.go)
	// gates the same 50 checkpoint children's own user+system CPU time against the same 2000 ms
	// limit, and that clock did not move at all across a quiet/co-loaded pair whose wall p50
	// moved 23x. For B-A — a latency across a process boundary, with no CPU clock to move to —
	// it leaves the judgement to the runs that do not pass the flag, and keeps B-B gated. Both
	// wall-clock rows are still judged at their limits by every run that does NOT pass it:
	// bench-gate's and nightly's `devtool bench-hotpath` lines, and this test itself in the
	// `timing` job, where every assertion below is the one bench-gate makes.
	underCoload := obs.UnderCoload()
	args := []string{
		"--iterations", strconv.Itoa(hotpathBenchIterations),
		"--hook", "observe-tool",
		"--warm-daemon",
	}
	if underCoload {
		args = append(args, hotpathUnderColoadFlag)
	}
	args = append(args, "--json", jsonPath, "--project", p.Root)

	hctx, hcancel := context.WithTimeout(ctx, hotpathHarnessBound)
	defer hcancel()
	cmd := exec.CommandContext(hctx, bench, args...)
	cmd.Dir = growthModuleRoot(t)
	cmd.Env = initialEnv
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	close(pollStop)
	pollWG.Wait()

	t.Logf("bench harness stdout:\n%s", stdout.String())
	if raw, readErr := os.ReadFile(jsonPath); readErr == nil {
		t.Logf("bench artifact %s:\n%s", jsonPath, raw)
	}
	require.NoError(t, runErr,
		"bench-hotpath exited non-zero (%s=%v): either a gated budget (B-B p99<%.0fms, B-E_cpu p99<%.0fms; "+
			"without %s also B-A p99<%.0fms and B-E's wall row p99<%.0fms) breached against the real "+
			"resident state, or the harness itself failed (its own delivery-integrity guard included)\n"+
			"stderr:\n%s",
		obs.UnderColoadEnv, underCoload, hotpathBudgetLimitMs(t, p, obs.BB), hotpathBudgetLimitMs(t, p, obs.BE),
		hotpathUnderColoadFlag, hotpathBudgetLimitMs(t, p, obs.BA), hotpathBudgetLimitMs(t, p, obs.BE),
		stderr.String())

	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err, "the harness must write the --json artifact")
	var rep hotpathBenchReport
	require.NoError(t, json.Unmarshal(raw, &rep))
	require.Equal(t, hotpathBenchIterations, rep.N)

	// Ruling #29: the gated B-A row is the daemon's TS-anchored hook_controlled estimate. A run
	// whose b_a_method is the wall-clock-minus-floor subtraction is measuring the host scheduler,
	// not the hook, and fails here.
	require.Containsf(t, rep.BAMethod, "hook_controlled",
		"b_a_method must name the TS-anchored hook_controlled estimate (ruling #29); a wall-clock-"+
			"minus-floor subtraction method is a FAIL: b_a_method=%q", rep.BAMethod)

	baLimit := hotpathBudgetLimitMs(t, p, obs.BA)
	bbLimit := hotpathBudgetLimitMs(t, p, obs.BB)

	// B-A's verdict is the two-mode block below, with B-E's wall row; its row is fetched here
	// because the structural cross-checks that follow read its population.
	ba := hotpathRow(t, rep, string(obs.BA))

	bb := hotpathRow(t, rep, string(obs.BB))
	require.NotNil(t, bb.LimitMs)
	require.InDelta(t, bbLimit, *bb.LimitMs, 0.001)
	require.NotNil(t, bb.Pass)
	require.True(t, *bb.Pass, "B-B gate must pass against the real resident state (p99=%.3fms)", bb.P99)
	require.Less(t, bb.P99, bbLimit, "§4.6: B-B p99 < %.0fms", bbLimit)

	// B-D is reported, never gated; the wall-clock survives ONLY as the spawn-estimate
	// diagnostic. The structural cross-check below is the teeth behind the b_a_method assertion:
	// the gated row's population is the daemon-side histogram (spawn loop plus warm-up hot
	// tranche), so a B-A row whose n collapsed to the spawn loop's would mean the gate had been
	// re-pointed at wall-clock samples.
	bd := hotpathRow(t, rep, string(obs.BD))
	require.Nil(t, bd.LimitMs, "B-D must never be gated — it is the host's cost")
	require.Nil(t, bd.Pass)
	require.Equal(t, hotpathBenchIterations, bd.N)
	spawnEst := hotpathRow(t, rep, hotpathSpawnEstimateID)
	require.Nil(t, spawnEst.LimitMs, "the wall-clock floor-subtracted estimate must stay informational")
	require.Nil(t, spawnEst.Pass)
	require.Greater(t, ba.N, bd.N,
		"the gated B-A row must be sourced from the daemon's hook_controlled histogram (spawn loop "+
			"plus warm-up hot tranche), not from the %d wall-clock spawn samples", bd.N)

	// §4.6's B-E, asserted in every mode on the clock that survives co-load. B-E is the one
	// budget the harness can only observe from OUTSIDE a whole real process, so its wall-clock
	// sample is host process creation plus scheduling weather plus the checkpoint — and §2.4 has
	// already ruled that the first of those must never be gated (B-D's "includes host process
	// creation. Reported only, never gated"). Ruling #29 made exactly this move for B-A. Here it
	// is made for B-E: the row gated in BOTH modes is the same 50 children's own user+system
	// CPU time, which co-load does not inflate — measured at p50/p99 = 15.625/46.875 ms in BOTH
	// halves of a quiet/co-loaded pair whose wall p50 moved 138.8 → 3219.1 ms (see
	// test/bench/hotpath/process.go's spawnSamples table). The limit is the same §2.4 number the
	// wall-clock row is gated on everywhere else, read from obs.Budgets() rather than restated.
	beLimit := hotpathBudgetLimitMs(t, p, obs.BE)
	beCPU := hotpathRow(t, rep, hotpathBECPURowID)
	require.Equal(t, hotpathCheckpointSamples, beCPU.N,
		"the B-E CPU row must cover every checkpoint spawn the harness made")
	require.NotNil(t, beCPU.LimitMs, "B-E_cpu is a hard gate in both modes and must carry its limit")
	require.InDelta(t, beLimit, *beCPU.LimitMs, 0.001,
		"both B-E rows read one limit from obs.Budgets(); a second number here would mean the harness "+
			"had grown a private copy of the budget")
	require.NotNil(t, beCPU.Pass)
	require.True(t, *beCPU.Pass, "B-E_cpu gate must pass against the real resident state (p99=%.3fms)", beCPU.P99)
	require.Less(t, beCPU.P99, beLimit,
		"§4.6: the checkpoint's own cost — the CPU its process actually consumed — must stay under "+
			"%.0fms with the real store/DAG/sketches resident", beLimit)

	// The two rows whose samples span a process boundary — B-A and B-E's wall-clock row — are
	// judged by the job that can judge them and asserted waived by the job that cannot, and in
	// neither mode is anything assumed:
	//
	//   - co-loaded (ci.yml's `test` job, QOMPACK_UNDER_COLOAD set): both rows must come back
	//     REPORTED (limit_ms/pass both null, exactly B-D's shape), and the harness's own
	//     disclosures must be in the notes — one per row, each naming the flag that caused it, the
	//     limit it did not apply and where it still applies (B-E's also names the row that still
	//     enforces the limit here). A null pass field is not an explanation; a reader must be able
	//     to see from the artifact alone which limit went unjudged on which row.
	//   - not co-loaded (ci.yml's `timing` job, which runs this test alone; bench-gate's shape):
	//     both rows gated at their obs.Budgets() limits, pass=true, p99 under the limit — and the
	//     waiver notes ABSENT, because a note present without the flag would mean the harness had
	//     waived on its own.
	beWall := hotpathRow(t, rep, string(obs.BE))
	require.Equal(t, hotpathCheckpointSamples, beWall.N)
	if underCoload {
		hotpathRequireReportedRow(t, ba, baLimit, "B-A")
		hotpathRequireReportedRow(t, beWall, beLimit, "B-E's wall-clock row")
		require.True(t,
			hotpathNotesMention(rep, hotpathUnderColoadFlag) && hotpathNotesMention(rep, hotpathBECPURowID),
			"the artifact must disclose B-E's wall-clock waiver in its notes, naming both the flag that "+
				"caused it and the row that still enforces the limit; notes present: %q", rep.Notes)
		require.True(t, hotpathNotesMention(rep, hotpathBAWaiverMark),
			"the artifact must disclose B-A's waiver in its own note (%q), not only B-E's; notes present: %q",
			hotpathBAWaiverMark, rep.Notes)
		t.Logf("§4.6 under %s: B-A p99=%.3fms (limit %.0fms) and B-E wall p99=%.3fms (limit %.0fms) are "+
			"REPORTED here, not judged; ci.yml's `timing` job runs this test alone and judges both",
			obs.UnderColoadEnv, ba.P99, baLimit, beWall.P99, beLimit)
	} else {
		hotpathRequireGatedRow(t, ba, baLimit, "B-A")
		hotpathRequireGatedRow(t, beWall, beLimit, "B-E's wall-clock row")
		require.False(t, hotpathNotesMention(rep, hotpathUnderColoadFlag),
			"no %s waiver may appear in a run that did not pass the flag — the harness would be waiving "+
				"on its own; notes present: %q", hotpathUnderColoadFlag, rep.Notes)
	}

	// spawn_floor_ms present, and recorded for the completion report alongside B-D.
	require.Positive(t, rep.SpawnFloorMs.N, "spawn_floor_ms must be present")
	require.Positive(t, rep.SpawnFloorMs.P50)

	// hotPathMode never transitioned to spool during the run: state.bin never showed hot=1 while
	// a daemon had it written (and the watcher provably saw the live daemon's record), no WARN
	// transition line reached the day logs, nothing reached LOUD.log, and the measured daemon
	// shut down cleanly enough to remove state.bin — the §12.2 quadruple, absent.
	require.True(t, sawDaemonState.Load(),
		"the state.bin watcher never observed the measured daemon's record; the no-spool check would be vacuous")
	require.False(t, sawSpool.Load(), "state.bin must report hot=0 (sync) for the entire measured run")
	require.Zero(t, hotpathCountDegradedWarns(t, p),
		"no %q WARN line may be logged during a run that stays inside its budget", hotpathDegradedMsg)
	loudPath := filepath.Join(paths.Of(p.Root).Logs, "LOUD.log")
	if loud, readErr := os.ReadFile(paths.Long(loudPath)); readErr == nil {
		require.NotContains(t, string(loud), hotpathDegradedMsg)
	}
	// A cleanly-stopped daemon removes state.bin — but the harness's own teardown DOCUMENTS that
	// a slow Windows daemon exit (go-winio's Close/Accept race, test/bench/hotpath process.go)
	// is tolerated by killing the child after its bound, and a killed child leaves state.bin
	// behind. §4.6's spelling is exactly "state.bin still reports hot=0": when the record
	// survived teardown, it must still say sync.
	if _, statErr := os.Stat(paths.Long(ipc.StatePath(p.Root))); statErr == nil {
		require.Equal(t, ipc.HotSync, ipc.ReadState(p.Root, p.Cfg).Hot,
			"a state.bin that survived the harness's teardown must still report hot=0 (sync)")
	}

	// The wall-clock B-E row is logged alongside the CPU one in both modes on purpose: the pair is
	// the evidence for the co-load argument above, and a future reader chasing a B-E question wants
	// to see both numbers from the same run, not just the one that was judged. The verdict word
	// says which mode this run was.
	wallVerdict := "gated"
	if underCoload {
		wallVerdict = "reported, not gated: " + hotpathUnderColoadFlag
	}
	t.Logf("§4.6 measured (platform %s, n=%d): B-A p99=%.3fms (limit %.0fms, n=%d; %s) | B-B p99=%.3fms "+
		"(limit %.0fms, n=%d) | B-E_cpu p99=%.3fms (limit %.0fms, n=%d) | B-E wall p50=%.3fms p99=%.3fms "+
		"(limit %.0fms; %s) | B-D p50=%.3fms p99=%.3fms max=%.3fms | spawn_floor "+
		"p50=%.3fms p99=%.3fms (n=%d) | B-A_spawn_estimate p50=%.3fms p99=%.3fms | b_a_method=%q",
		rep.Platform, rep.N, ba.P99, baLimit, ba.N, wallVerdict, bb.P99, bbLimit, bb.N,
		beCPU.P99, beLimit, beCPU.N, beWall.P50, beWall.P99, beLimit, wallVerdict,
		bd.P50, bd.P99, bd.Max, rep.SpawnFloorMs.P50, rep.SpawnFloorMs.P99, rep.SpawnFloorMs.N,
		spawnEst.P50, spawnEst.P99, rep.BAMethod)
	for _, note := range rep.Notes {
		t.Logf("bench note: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// §4.6 test 2 — TestIntegration_HotPathDegradesRatherThanBlocks
// ─────────────────────────────────────────────────────────────────────────────────────────────

// hotpathStallBinding wraps the §4.2 ObserveTool with a switchable 25ms stall. The stall consumes
// the daemon's own clock rather than wall time — see the file comment's decision 2: sleepcheck
// bans wall sleeps, and internal/daemon's own tests simulate a slow hot path through exactly this
// recvTS−reqTS relationship (fakeClock/feedBreachingWindow). Because the daemon, the driver's
// request stamps and this binding share one testutil.FakeClock, each stalled event's 25ms lands
// in the stamp→receive window of the NEXT request the driver has already stamped, and the breach
// detector reads it off the same recordHotPathSample path production uses.
type hotpathStallBinding struct {
	hp      *hookflowPipeline
	clk     *testutil.FakeClock
	stalled atomic.Bool
}

// ObserveTool is the bound Services.ObserveTool for test 2.
func (b *hotpathStallBinding) ObserveTool(ctx context.Context, e hookio.Event) error {
	if b.stalled.Load() {
		b.clk.Advance(hotpathStallLatency)
	}
	return b.hp.ObserveTool(ctx, e)
}

// TestIntegration_HotPathDegradesRatherThanBlocks is §4.6's second test: the same warm daemon
// (same §4.2 binding, same resident-state construction), a 25ms stall injected into the bound
// ObserveTool for three consecutive 512-sample windows, and §8.1's promise held against a real
// L1: the daemon flips to spool, clients stop connecting, every hook still exits 0, no event is
// lost after the next drain, and one WARN line plus the status payload record the transition.
func TestIntegration_HotPathDegradesRatherThanBlocks(t *testing.T) {
	ctx := context.Background()
	bin := buildQompackBinary(t)
	p := testutil.NewProject(t, testutil.WithEnv(testutil.E2EBinaryEnv, bin))

	// The transition arithmetic below is §2.4's: p99 over 512-sample windows against the 15ms
	// budget, three consecutive breaching windows to flip. Pin the premises to the configuration
	// the daemon will actually gate on, so a changed default fails loudly here instead of
	// silently bending the window count.
	require.Equal(t, 3, p.Cfg.Runtime.HotPath.BreachWindows,
		"§4.6's 'three consecutive windows' is cfg.Runtime.HotPath.BreachWindows' default")
	require.True(t, p.Cfg.Runtime.HotPath.SpoolOnBreach,
		"the spool fallback must be enabled for §8.1's degrade to be reachable")
	require.Greater(t, hotpathStallLatency,
		time.Duration(p.Cfg.Runtime.HotPath.BudgetMs)*time.Millisecond,
		"the injected stall must exceed the budget or no window can breach")

	// Same warm state as test 1, through the same binding — built before the daemon starts (the
	// wire lane needs no daemon; §4.2's store-focused tests drive the bound func the same way).
	hotpathSeedTriedBloom(t, p)
	hp := newHookflowPipeline(t, p)
	hotpathPopulate(t, ctx, hp, nil)
	hotpathEnrichSymbols(t, hp)
	hotpathAssertResidentState(t, ctx, p, hp)

	stall := &hotpathStallBinding{hp: hp, clk: p.Clock}

	// The daemon over the resident state, with the stall-capable binding bound through the same
	// §4.2 seam, and — unlike startHookflowDaemon — the project's FakeClock as the daemon's own
	// clock, so the binding's clock stall is the clock recordHotPathSample reads. §4.7's test
	// already runs the daemon on this same clock seam.
	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	opts.Clock = p.Clock
	opts.Store = hp.store
	opts.Graph = hp.graph
	opts.Sketches = hp.sketches
	opts.Bind(func(s *daemon.Services) { s.ObserveTool = stall.ObserveTool })
	d, err := daemon.New(opts)
	require.NoError(t, err)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	runDone := make(chan struct{})
	var runResult error
	go func() {
		runResult = d.Run(runCtx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(hotpathStopBound):
			t.Errorf("daemon.Run did not return within %v of cancellation", hotpathStopBound)
		}
	})
	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, hookflowProbeDial) },
		hookflowDaemonUpBound, hookflowDaemonUpTick,
		"the daemon never became reachable at %s within 8x daemon.SpawnPollBound", addr.Path)

	client := hotpathNewClient(t, p, ipc.State{Mode: contract.ModeFull, DaemonEnabled: true})

	// Register the session (a real host fires SessionStart first — §4.2 does the same). This is
	// also load-bearing twice over: registry.Ensure resets the hot submode and breach ring for a
	// NEW session, so it must happen before the stall's windows start filling; and a registered
	// live session keeps the idle controller from treating the daemon as idle and draining the
	// degraded-phase spool early.
	resp, err := client.Send(ctx, ipc.Request{
		Op: ipc.OpSessionStart, Session: hotpathStallSession, TS: core.NowMilli(p.Clock),
		Event: &hookio.Event{
			HookEventName: "SessionStart", SessionID: hotpathStallSession, Source: "startup", CWD: p.Root,
		},
	}, hotpathReplyBudget)
	require.NoError(t, err)
	require.True(t, resp.OK, "session.start must be ACKed")

	// Warm tranche: the daemon serves in sync submode before the stall.
	base := hotpathEventTotal
	for i := 0; i < hotpathStallWarmEvents; i++ {
		ev := hookflowEvent(t, hotpathStallSession,
			core.ToolUseID(fmt.Sprintf("toolu_hotpath_stall_warm_%02d", i)),
			"src/svc/stallprobe.ts", hotpathProbeContent("warm", i))
		resp, err = client.Send(ctx, ipc.Request{
			Op: ipc.OpObserveTool, Session: hotpathStallSession, TS: core.NowMilli(p.Clock), Event: &ev,
		}, hotpathReplyBudget)
		require.NoError(t, err)
		require.True(t, resp.OK, "warm event %d must be ACKed in sync submode", i)
	}
	base += hotpathStallWarmEvents
	hotpathWaitPipeline(t, hp, base)
	require.Equal(t, ipc.HotSync, d.Registry().HotMode(), "the daemon must start the stall phase in sync submode")

	walPath := filepath.Join(paths.Of(p.Root).Spool, "wal-"+string(hotpathStallSession)+".ndjson")
	require.Positive(t, countFileLines(t, walPath), "the warm tranche must be WAL-durable before the stall")

	// ── The stall: switched on via the binding, driven until the daemon NAKs. ──
	//
	// Each request's TS is stamped BEFORE the previous request is even sent — i.e. strictly
	// before that request's stalled processing can consume the clock — and the request is
	// delivered only after that processing has completed. Exactly one 25ms stall therefore lands
	// inside every sample's stamp→receive window, deterministically: the driver never fakes a
	// latency itself; it only overlaps each request with the stall the way a real hook that
	// started while the daemon was mid-stall overlaps it. (Stamping AFTER the previous ACK does
	// not work, and the first revision of this file proved it: the worker consumes the stall
	// before the ACK byte even reaches the client, so a post-ACK stamp always lands after the
	// advance and every sample reads clean.)
	stall.stalled.Store(true)
	sent := 0
	nakSeen := false
	tsCur := core.NowMilli(p.Clock)
	for sent < hotpathStallSendCap {
		// The NEXT sample's stamp, taken before this send can trigger this event's stall.
		tsNext := core.NowMilli(p.Clock)
		ev := hookflowEvent(t, hotpathStallSession,
			core.ToolUseID(fmt.Sprintf("toolu_hotpath_stall_%04d", sent)),
			"src/svc/stallprobe.ts", hotpathProbeContent("stall", sent))
		resp, err = client.Send(ctx, ipc.Request{
			Op: ipc.OpObserveTool, Session: hotpathStallSession, TS: tsCur, Event: &ev,
		}, hotpathReplyBudget)
		require.NoError(t, err)
		sent++
		if !resp.OK {
			// §12.2's NAK-with-hint: the request is already WAL'd, the client flips itself to
			// spool submode and spools the duplicate, and Drain's seen-set collapses it later.
			nakSeen = true
			break
		}
		hotpathWaitPipeline(t, hp, base+sent) // this event's stall has now consumed its 25ms
		tsCur = tsNext
	}
	require.True(t, nakSeen,
		"after %d stalled sends (cap %d) the daemon never NAKed: three consecutive breaching "+
			"512-sample windows did not flip the hot path to spool", sent, hotpathStallSendCap)
	require.GreaterOrEqual(t, sent, 3*hotpathSampleWindow-hotpathStallWarmEvents,
		"the transition may not fire before three full windows have closed (§2.4)")
	require.Eventually(t, func() bool { return d.Registry().HotMode() == ipc.HotSpool },
		hotpathPipelineBound, hotpathPipelineTick,
		"the registry must report spool submode after the NAK")
	base += sent
	hotpathWaitPipeline(t, hp, base) // the NAK'd event was accepted and processes too

	// The transition is visible everywhere §12.2 says it is: state.bin, the WARN line, status.
	st := ipc.ReadState(p.Root, p.Cfg)
	require.Equal(t, ipc.HotSpool, st.Hot, "state.bin must record the spool transition")
	require.NotZero(t, st.DaemonPID, "state.bin must still be the live daemon's record")
	require.Equal(t, 1, hotpathCountDegradedWarns(t, p),
		"exactly one %q WARN line must be logged for one transition", hotpathDegradedMsg)

	resp, err = client.Send(ctx, ipc.Request{
		Op: ipc.OpStatus, Session: hotpathStallSession, TS: core.NowMilli(p.Clock), Reply: true,
	}, hotpathReplyBudget)
	require.NoError(t, err)
	require.True(t, resp.OK, "status must answer while degraded: %s", resp.Err)
	var snap daemon.StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	require.Equal(t, "spool", snap.Hot, "the status payload must record the transition")
	require.EqualValues(t, 1, snap.Counters[hotpathDegradedCounter],
		"the status payload must count exactly one spool transition")

	// ── Degraded: clients stop connecting, every hook still exits 0. ──
	walBefore := countFileLines(t, walPath)
	spoolBefore := clientSpoolLines(t, p.Root)

	// A FRESH client — constructed the way a new hook constructs one, from state.bin — must spool
	// without dialling.
	fresh := hotpathNewClient(t, p, ipc.ReadState(p.Root, p.Cfg))
	ev := hookflowEvent(t, hotpathStallSession, core.ToolUseID("toolu_hotpath_degraded_client"),
		"src/svc/stallprobe.ts", hotpathProbeContent("degraded", 0))
	resp, err = fresh.Send(ctx, ipc.Request{
		Op: ipc.OpObserveTool, Session: hotpathStallSession, TS: core.NowMilli(p.Clock), Event: &ev,
	}, hotpathReplyBudget)
	require.NoError(t, err)
	require.False(t, resp.OK, "a client that read hot=spool must not be ACKed by a live connection")

	// Real spawned hooks: RunHook itself asserts §2.3's only permitted outcome — exit 0.
	for i := 0; i < hotpathDegradedHooks; i++ {
		p.RunHook(t, "PostToolUse", hookflowEvent(t, hotpathStallSession,
			core.ToolUseID(fmt.Sprintf("toolu_hotpath_degraded_%02d", i)),
			"src/svc/stallprobe.ts", hotpathProbeContent("degraded", i+1)))
	}
	degraded := hotpathDegradedHooks + 1

	require.Equal(t, walBefore, countFileLines(t, walPath),
		"no degraded-phase event may reach the daemon's ingest: clients stop connecting")
	require.Equal(t, spoolBefore+degraded, clientSpoolLines(t, p.Root),
		"every degraded-phase event must be durably spooled client-side, exactly once")
	require.Equal(t, ipc.HotSpool, ipc.ReadState(p.Root, p.Cfg).Hot,
		"the daemon must still be in spool submode after the degraded phase")

	// ── Recovery: the stall ends, the next drain loses nothing. ──
	//
	// Both in-test clients close first: a real hook is a short-lived process whose spool handle
	// dies with it, and the two clients above both hold this test process's client-<pid>.ndjson
	// open — on Windows an open handle keeps the drainer from deleting the fully-consumed file,
	// which is exactly the 2-line residue this file's first run observed. Close is idempotent
	// (ipc's spool Close is sync.Once-guarded), so the registered cleanups stay harmless.
	require.NoError(t, client.Close())
	require.NoError(t, fresh.Close())
	stall.stalled.Store(false)
	applied, err := d.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, degraded, applied,
		"the drain must apply exactly the spooled degraded-phase events — every WAL line and the "+
			"NAK duplicate are collapsed by the shared seen-set (871f574's exactly-once)")
	total := base + degraded
	hotpathWaitPipeline(t, hp, total)
	require.Empty(t, hp.takeErrs(), "no drained event may fail inside the binding")
	require.Zero(t, clientSpoolLines(t, p.Root), "a completed drain consumes every client spool file")

	stats, err := hp.store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, total, stats.ToolUses,
		"§8.1: degrade to async queue-and-drain loses freshness, never data — every event of every "+
			"phase must be in the real store")
	require.Equal(t, 1, hotpathCountDegradedWarns(t, p),
		"the drain must not log a second transition WARN")

	require.NoError(t, d.Stop(ctx), "the degraded daemon must still stop cleanly")
	select {
	case <-runDone:
	case <-time.After(hotpathStopBound):
		t.Fatalf("daemon.Run did not return within %v of Stop", hotpathStopBound)
	}
	require.NoError(t, runResult)

	t.Logf("§4.6 degrade: warm=%d stalled sends=%d (NAK after %d, three windows of %d), degraded "+
		"hooks=%d (all exit 0, spooled), drained=%d, store ToolUses=%d, WARN lines=1",
		hotpathStallWarmEvents, sent, sent, hotpathSampleWindow, degraded, applied, stats.ToolUses)
}

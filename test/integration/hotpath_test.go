package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/paths/pathstest"
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
//  2. Test 2's stall (hotpathStallFor: 25ms at the linux budget) is a CLOCK stall, not a
//     wall-clock sleep. §6.1 bans time.Sleep outside test/bench (sleepcheck), and
//     internal/daemon's own tests simulate a slow hot path exactly this way: a controllable clock
//     whose recvTS−reqTS gap the breach detector reads (daemon_test.go's feedBreachingWindow
//     constructs the same relationship by hand). Here the
//     bound ObserveTool consumes the stall from the daemon's own clock per event while it is
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

	// hotpathPipelineHangGuard is how long hotpathWaitPipeline waits for ACKed events to come out
	// of the binding before it calls the pipeline hung. It is a hang guard, not a latency bound,
	// because no daemon constant bounds that trip: a live worker dispatches under the daemon's own
	// context with no deadline (ingest.go, worker and route); daemon.DrainLineDeadline bounds only
	// a DRAINED line's handler call (drain.go), not the live path and not the line's other work;
	// and the ACK is written after the WAL fsync (and, for a leased delivery, the delivery-lease
	// fsyncs) while the dispatch runs after it, so an ACKed event can stay in flight well past
	// DrainLineDeadline on a slow fsync or a starved CPU without being lost. Loss
	// is judged where it can be judged: the end-of-test store ToolUses and drain counts. Basis:
	// twice daemon.IdleTickMax, the daemon's slowest periodic cadence; a dispatch that outlasts
	// two of those is wedged, not slow.
	hotpathPipelineHangGuard = 2 * daemon.IdleTickMax
	// hotpathPipelineTick is the poll cadence of the pipeline and submode waits. Not 1ms: the
	// daemon under test shares this process, and a 1ms poll is load the measured pipeline pays.
	hotpathPipelineTick = 5 * time.Millisecond

	// hotpathModeBound waits for the registry to report spool submode once the daemon has NAKed.
	// Basis: daemon.DrainLineDeadline, the bound this wait has always used; the NAK is the
	// daemon's own spool judgement, so the wait reads back a transition already made.
	hotpathModeBound = daemon.DrainLineDeadline

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

	// hotpathStallOverBudget is how far §4.6's "artificial 25 ms stall" sits above the B-A budget
	// it was written against: 25 ms against the 15 ms that budgetMs defaulted to everywhere before
	// D41. The stall is that margin over the configured budget (hotpathStallFor), so it stays 25 ms
	// on linux and still breaches on Windows (50 ms budget) and macOS (40 ms), where a fixed 25 ms
	// would sit inside the budget and no window could breach.
	hotpathStallOverBudget = 10 * time.Millisecond

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
	hotpathDegradedMsg = "hot path switched to spool submode; nothing is lost"

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
// included) — then pins the count exactly. The wait is a hang guard (hotpathPipelineHangGuard): a
// late event is delayed, not lost, and loss is judged by the callers' store and drain counts. When
// the guard does expire, the failure reports the state AT THAT MOMENT — the distinct and raw
// completion counts, the pipeline errors and the WAL and lease journal line counts — not the
// state when the wait began.
func hotpathWaitPipeline(t *testing.T, hp *hookflowPipeline, want int) {
	t.Helper()
	start := time.Now()
	if assert.Eventually(t, func() bool { return hp.uniqueObservedCount() >= want },
		hotpathPipelineHangGuard, hotpathPipelineTick) {
		require.Equal(t, want, hp.uniqueObservedCount())
		return
	}
	require.FailNowf(t, "pipeline hung",
		"after %v (hang guard %v, 2x daemon.IdleTickMax) %d/%d distinct events are through the "+
			"binding (%d raw completions); pipeline errors: %v; WAL lines: %d; client spool lines: "+
			"%d; delivery-lease journal lines: %d",
		time.Since(start).Round(time.Millisecond), hotpathPipelineHangGuard,
		hp.uniqueObservedCount(), want, hp.observedCount(), hp.takeErrs(),
		hotpathWALLines(t, hp.root), clientSpoolLines(t, hp.root), hotpathLeaseLines(t, hp.root))
}

// hotpathWALLines counts the non-empty lines across every wal-* segment under root's spool
// directory: every event the daemon has ACKed on the live path.
func hotpathWALLines(t *testing.T, root string) int {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	total := 0
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "wal-") {
			total += countFileLines(t, f)
		}
	}
	return total
}

// hotpathLeaseLines counts the non-empty lines across every delivery-leases.jsonl under root's
// state directory, segmented journals included: every delivery identity the daemon has made
// durable before its ACK. Only a hook's own client stamps the nonce a lease needs
// (internal/cli/hookclient.go), so this file's direct ipc.Client requests are unleased and add 0;
// the real spawned hooks of test 2's degraded phase are the deliveries it counts.
func hotpathLeaseLines(t *testing.T, root string) int {
	t.Helper()
	total := 0
	err := filepath.WalkDir(paths.Of(root).State, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() && d.Name() == "delivery-leases.jsonl" {
			total += countFileLines(t, path)
		}
		return nil
	})
	require.NoError(t, err)
	return total
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
	return len(hotpathDegradedWarnLines(t, p))
}

// hotpathDegradedWarnLines returns the lines hotpathCountDegradedWarns counts, verbatim.
func hotpathDegradedWarnLines(t *testing.T, p *testutil.Project) []string {
	t.Helper()
	// The plain path, not paths.Long: filepath.Glob does not accept an extended-length prefix as
	// a pattern, and internal/daemon's own readDayLogs globs the same directory the same way.
	pattern := filepath.Join(paths.Of(p.Root).Logs, "qompack-*.log")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err)
	var out []string
	for _, f := range files {
		b, readErr := os.ReadFile(f)
		require.NoError(t, readErr)
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "level=warn") && strings.Contains(line, hotpathDegradedMsg) {
				out = append(out, line)
			}
		}
	}
	return out
}

// hotpathDegradedLoudLines returns the LOUD.log lines carrying the spool-transition message. A
// LOUD.log that does not exist holds none (internal/logging creates it on the first Loud call);
// any other read failure fails the test rather than reading as "no transition".
func hotpathDegradedLoudLines(t *testing.T, p *testutil.Project) []string {
	t.Helper()
	loud, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Logs, "LOUD.log")))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err, "reading LOUD.log")
	var out []string
	for _, line := range strings.Split(string(loud), "\n") {
		if strings.Contains(line, hotpathDegradedMsg) {
			out = append(out, line)
		}
	}
	return out
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

// hotpathWarmHotTranche mirrors the harness's own warmHotTranche (test/bench/hotpath/main.go): the
// in-process observe.tool requests its --warm-daemon warm-up sends as genuine hot-path traffic
// ahead of the spawn loop. With hotpathBenchIterations it makes the harness's planned gated
// population, expectedHotPathSends (test/bench/hotpath/delivery.go): the hot-path sends the
// daemon-side B-A and B-B rows are built over, and the one population the wall-clock spawn samples
// (hotpathBenchIterations of them) can never match in size.
const hotpathWarmHotTranche = 64

// The two disclosures a daemon-side row's population is accounted from, mirrored from the harness
// the way hotpathBAWaiverMark mirrors its waiver notes. hotpathLedgerNote is buildNotes' delivery
// ledger (test/bench/hotpath/measure.go), written whenever the run deferred anything: sent,
// delivered live, DEFERRED, and a loss count the harness refuses to report as anything but 0.
// hotpathShortfallNote and hotpathBoundedNote are tailAdjustmentNote's (report.go) three shapes
// of "this row's population came up short", keyed by the row id they open with: the first two carry
// "<missing> of the <planned> planned samples", the third "<planned> ... <observed> ... <missing>".
// A harness whose prose drifted from these would leave a shortfall undisclosed here, and
// hotpathDaemonPopulations then FAILS on the unaccounted samples rather than passing without them.
var (
	hotpathLedgerNote = regexp.MustCompile(`^delivery ledger: (\d+) hot-path requests sent, (\d+) delivered live ` +
		`to the daemon, (\d+) DEFERRED to the client spool and 0 lost\.`)
	hotpathShortfallNote = regexp.MustCompile(`^(B-[AB])(?:'s p99 CANNOT be certified and its gate is failed on ` +
		`that ground)?: (\d+) of the (\d+) planned samples never reached the daemon's histogram`)
	hotpathBoundedNote = regexp.MustCompile(`^(B-[AB])'s p99 is reported over the full planned population of ` +
		`(\d+), not the (\d+) samples the daemon actually observed: the (\d+) missing sample\(s\)`)
)

// hotpathShortfall is what the harness's notes disclose about one daemon-side row's population.
type hotpathShortfall struct {
	missing, planned int
	observed         int // -1 when the note does not restate the observed count
}

// hotpathDeliveryLedger is the harness's whole-run delivery ledger, read off its note.
type hotpathDeliveryLedger struct{ sent, delivered, deferred int }

// hotpathPopulations is what hotpathDaemonPopulations proved, for the test's log.
type hotpathPopulations struct {
	planned, baMissing, bbMissing int
	ledger                        *hotpathDeliveryLedger
}

// hotpathAtoiAll converts a regexp's digit groups; they match only \d+, so the one failure left is
// an overflow, which is an unreadable artifact.
func hotpathAtoiAll(groups []string) ([]int, error) {
	out := make([]int, 0, len(groups))
	for _, g := range groups {
		n, err := strconv.Atoi(g)
		if err != nil {
			return nil, fmt.Errorf("the bench artifact carries an unreadable count %q: %w", g, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// hotpathReadShortfalls returns every row shortfall and the delivery ledger the artifact's notes
// disclose, refusing a row disclosed twice or a ledger stated twice.
func hotpathReadShortfalls(rep hotpathBenchReport) (map[string]hotpathShortfall, *hotpathDeliveryLedger, error) {
	short := map[string]hotpathShortfall{}
	var ledger *hotpathDeliveryLedger
	for _, note := range rep.Notes {
		if m := hotpathLedgerNote.FindStringSubmatch(note); m != nil {
			n, err := hotpathAtoiAll(m[1:])
			if err != nil {
				return nil, nil, err
			}
			if ledger != nil {
				return nil, nil, fmt.Errorf("the bench artifact states the delivery ledger twice: %q", rep.Notes)
			}
			ledger = &hotpathDeliveryLedger{sent: n[0], delivered: n[1], deferred: n[2]}
			continue
		}
		var id string
		var sh hotpathShortfall
		if m := hotpathShortfallNote.FindStringSubmatch(note); m != nil {
			n, err := hotpathAtoiAll(m[2:])
			if err != nil {
				return nil, nil, err
			}
			id, sh = m[1], hotpathShortfall{missing: n[0], planned: n[1], observed: -1}
		} else if m := hotpathBoundedNote.FindStringSubmatch(note); m != nil {
			n, err := hotpathAtoiAll(m[2:])
			if err != nil {
				return nil, nil, err
			}
			id, sh = m[1], hotpathShortfall{missing: n[2], planned: n[0], observed: n[1]}
		} else {
			continue
		}
		if _, dup := short[id]; dup {
			return nil, nil, fmt.Errorf("the bench artifact discloses %s's shortfall twice: %q", id, rep.Notes)
		}
		short[id] = sh
	}
	return short, ledger, nil
}

// hotpathDaemonPopulations proves, from the harness's own delivery accounting, that the B-A and B-B
// rows are the daemon-side histograms over the harness's planned hot-path sends, and not the
// hotpathBenchIterations wall-clock spawn samples. It returns an error naming the first check that
// does not hold:
//
//	(a) each row's observed n plus the shortfall the harness disclosed for it is exactly the
//	    planned population, hotpathBenchIterations spawns plus hotpathWarmHotTranche warm-up
//	    requests. The harness derives every disclosed shortfall as planned minus observed
//	    (gatedLedger.Undelivered, hookControlledShortfall), so any row built by
//	    buildBudgetRowFromSnapshot satisfies this by construction; what it catches is a row with
//	    NO disclosure short of the plan, which is the shape a ROW-level re-point (buildBudgetRow
//	    over the raw spawn samples: n = hotpathBenchIterations, no note) takes in every run.
//	(b) B-A's n is at most B-B's: a hook_controlled sample is recorded only for a request the
//	    daemon received, and l0_ingest (B-B) counts every received one; the difference is the
//	    daemon's hotpath_sample_invalid count.
//	(c) B-A's n is not exactly hotpathBenchIterations. Any re-point at the wall-clock samples
//	    has that n, however it was done. A SNAPSHOT-level re-point (a snapshot of the spawn
//	    samples fed to buildBudgetRowFromSnapshot) gets a harness-written "64 of the 2064 planned"
//	    disclosure and passes (a); the harness's own hookControlledShortfall refuses it only when
//	    deferrals plus hotpath_sample_invalid fall short of the tranche, and (b) refuses it only
//	    when B-B's shortfall exceeds the tranche. What neither sees is B-B's shortfall exactly
//	    equal to the tranche, or short of it with hotpath_sample_invalid (which the artifact does
//	    not carry) covering the rest: there a genuine B-A row of n = hotpathBenchIterations and a
//	    re-pointed one have identical counts. (c) refuses that coincidence rather than passing it,
//	    as the pre-ledger "B-A's n above B-D's" check did; it is the only case in which a genuine
//	    run fails here, and it needs the run's B-A shortfall to land on exactly the tranche.
//	(d) B-B's shortfall (the gated window's undelivered requests) is covered by the delivery
//	    ledger's DEFERRED count, with 0 lost: every sample missing from the daemon's population is
//	    a request the client spooled, never one that vanished. No shortfall and no ledger note is
//	    the clean run.
func hotpathDaemonPopulations(rep hotpathBenchReport) (hotpathPopulations, error) {
	planned := hotpathBenchIterations + hotpathWarmHotTranche
	out := hotpathPopulations{planned: planned}
	rows := map[string]int{}
	for _, row := range rep.Budgets {
		rows[row.BudgetID] = row.N
	}
	short, ledger, err := hotpathReadShortfalls(rep)
	if err != nil {
		return out, err
	}
	out.ledger = ledger
	missing := map[string]int{}
	for _, id := range []string{string(obs.BA), string(obs.BB)} {
		n, ok := rows[id]
		if !ok {
			return out, fmt.Errorf("the bench artifact carries no %s row", id)
		}
		sh := short[id]
		if sh.planned != 0 && sh.planned != planned {
			return out, fmt.Errorf("the harness disclosed %s's planned population as %d, not the %d this test "+
				"plans (%d spawns + %d warm-up hot tranche)", id, sh.planned, planned, hotpathBenchIterations,
				hotpathWarmHotTranche)
		}
		if sh.observed >= 0 && sh.planned != 0 && sh.observed != n {
			return out, fmt.Errorf("the harness disclosed %s's observed population as %d but the row carries n=%d",
				id, sh.observed, n)
		}
		if n+sh.missing != planned {
			return out, fmt.Errorf("the gated %s row's n=%d plus the %d samples the harness disclosed as never "+
				"reaching the daemon's histogram is %d, not the %d planned hot-path sends (%d spawns + %d warm-up "+
				"hot tranche): the row is not the daemon-side histogram over this run's sends — a row re-pointed "+
				"at the %d wall-clock spawn samples has exactly that n and no shortfall disclosure",
				id, n, sh.missing, n+sh.missing, planned, hotpathBenchIterations, hotpathWarmHotTranche,
				hotpathBenchIterations)
		}
		missing[id] = sh.missing
	}
	out.baMissing, out.bbMissing = missing[string(obs.BA)], missing[string(obs.BB)]
	if baN, bbN := rows[string(obs.BA)], rows[string(obs.BB)]; baN > bbN {
		return out, fmt.Errorf("the B-A row has %d samples, more samples than the %d requests the daemon "+
			"received (B-B, l0_ingest): hook_controlled records only received requests", baN, bbN)
	}
	if baN := rows[string(obs.BA)]; baN == hotpathBenchIterations {
		return out, fmt.Errorf("the B-A row has n=%d, exactly the %d wall-clock spawn samples: with %d of the "+
			"planned sends disclosed as missing from it, a daemon-side row and one re-pointed at the spawn "+
			"samples have the same counts and cannot be told apart, so this run cannot prove B-A's sourcing",
			baN, hotpathBenchIterations, out.baMissing)
	}
	if out.bbMissing == 0 {
		return out, nil
	}
	if ledger == nil {
		return out, fmt.Errorf("%d of the planned hot-path sends never reached the daemon but the artifact "+
			"carries no delivery ledger note accounting for them as deferred; notes: %q", out.bbMissing, rep.Notes)
	}
	if out.bbMissing > ledger.deferred {
		return out, fmt.Errorf("the %d gated sends missing from the daemon's population exceeds the %d the "+
			"delivery ledger deferred to the client spool: the rest are unaccounted for", out.bbMissing, ledger.deferred)
	}
	if ledger.delivered+ledger.deferred != ledger.sent || ledger.sent < planned {
		return out, fmt.Errorf("the delivery ledger does not add up: %d sent, %d delivered, %d deferred, against "+
			"%d planned gated sends", ledger.sent, ledger.delivered, ledger.deferred, planned)
	}
	return out, nil
}

// hotpathBreachFields reads the two fields applyHotPathTransition (internal/daemon/handlers.go)
// writes on its ToSpool WARN and LOUD lines, in the order it passes them: the breach detector's own
// limit in milliseconds and its consecutive-window count, rendered by internal/logging's formatLine
// as unquoted ` key=value` pairs.
var hotpathBreachFields = regexp.MustCompile(` budget_ms=(\d+) windows=(\d+)(?:\s|$)`)

// hotpathSpoolObservation is what test 1 saw of the §12.2 spool transition over the measured run.
type hotpathSpoolObservation struct {
	sawSpool      bool     // the state.bin watcher read hot=1 off a record a live daemon had written
	survivedSpool bool     // state.bin survived the harness's teardown and does not report hot=0
	warnLines     []string // level=warn day-log lines carrying hotpathDegradedMsg
	loudLines     []string // LOUD.log lines carrying hotpathDegradedMsg
}

// hotpathSpoolTransition is what hotpathJudgeSpool proved about a reported transition, for the log.
type hotpathSpoolTransition struct {
	transitions       int // WARN/LOUD pairs: applyHotPathTransition writes one of each per ToSpool
	budgetMs, windows int // what those lines name: the detector's limit and its breach-window count
}

// hotpathJudgeSpool judges test 1's §12.2 observation by mode (owner decision D39) and returns an
// error naming the first check that does not hold.
//
// Isolated (waived false), the transition stays forbidden exactly as before D39: state.bin never
// showed hot=1, no WARN line, nothing in LOUD.log, and a state.bin left by teardown says sync.
//
// Waived — co-loaded (D39), or on a non-reference disk on a GitHub Actions runner (D53(e),
// obs.NonReferenceDisk) — the transition is REPORTED: it is what the breach detector does when the
// wall budgets the run waives (ADR 0010; Q1) are breached for wantWindows consecutive windows, and on
// a throttled hosted disk B-A's p50 alone sits at its limit. A run with none of
// the four signs is the clean run. A run with any of them must show a transition that is loud and
// named, and one whose spooled events are accounted for:
//
//	(a) at least one degraded WARN line and at least one LOUD.log line, and as many of one as the
//	    other — applyHotPathTransition writes both, unconditionally, on every ToSpool; a spool
//	    record with neither is a silent degrade, which no mode reports;
//	(b) every one of those lines names the breach: budget_ms and windows present and equal to the
//	    configured hot-path budget and breach-window count the measured daemon gated on;
//	(c) the harness's delivery ledger is present and adds up — sent = delivered live + deferred to
//	    the client spool — with 0 lost, which hotpathLedgerNote only matches when the note says so.
//	    A transition moves every later hook to the client spool, so a transition run without the
//	    ledger has deferrals it did not account for.
//
// The watcher's own hot=1 sighting is not required in (a): it polls, and the lines are the
// product's record of the transition. Everything else the row asserts is outside this function
// and identical in both modes.
func hotpathJudgeSpool(o hotpathSpoolObservation, waived bool, wantBudgetMs, wantWindows int,
	ledger *hotpathDeliveryLedger,
) (hotpathSpoolTransition, error) {
	var out hotpathSpoolTransition
	if !waived {
		switch {
		case o.sawSpool:
			return out, errors.New("state.bin must report hot=0 (sync) for the entire measured run")
		case len(o.warnLines) != 0:
			return out, fmt.Errorf("no %q WARN line may be logged during a run that stays inside its budget; "+
				"logged: %q", hotpathDegradedMsg, o.warnLines)
		case len(o.loudLines) != 0:
			return out, fmt.Errorf("LOUD.log must not carry %q in a run that stays inside its budget; "+
				"carried: %q", hotpathDegradedMsg, o.loudLines)
		case o.survivedSpool:
			return out, errors.New("a state.bin that survived the harness's teardown must still report hot=0 (sync)")
		}
		return out, nil
	}
	if !o.sawSpool && !o.survivedSpool && len(o.warnLines) == 0 && len(o.loudLines) == 0 {
		return out, nil
	}
	if len(o.warnLines) == 0 || len(o.loudLines) == 0 || len(o.warnLines) != len(o.loudLines) {
		return out, fmt.Errorf("a waived (co-loaded or non-reference-disk) run may report the §12.2 spool "+
			"transition, never a silent one: "+
			"applyHotPathTransition writes one %q WARN line and one LOUD.log line per transition, but the "+
			"day logs carry %d and LOUD.log %d (state.bin watcher saw hot=1: %v; state.bin left by teardown "+
			"not sync: %v)", hotpathDegradedMsg, len(o.warnLines), len(o.loudLines), o.sawSpool, o.survivedSpool)
	}
	for _, line := range append(append([]string(nil), o.warnLines...), o.loudLines...) {
		m := hotpathBreachFields.FindStringSubmatch(line)
		if m == nil {
			return out, fmt.Errorf("the spool transition line does not name the breach (no budget_ms=/windows= "+
				"fields): %q", line)
		}
		n, err := hotpathAtoiAll(m[1:])
		if err != nil {
			return out, err
		}
		if n[0] != wantBudgetMs || n[1] != wantWindows {
			return out, fmt.Errorf("the spool transition line names budget_ms=%d windows=%d, not the configured "+
				"hot-path budget %dms over %d breach windows: %q", n[0], n[1], wantBudgetMs, wantWindows, line)
		}
	}
	if ledger == nil {
		return out, errors.New("the daemon moved to spool submode but the bench artifact carries no delivery " +
			"ledger note: the hooks spooled after the transition are not accounted for as deferred with 0 lost")
	}
	if ledger.delivered+ledger.deferred != ledger.sent {
		return out, fmt.Errorf("the delivery ledger of a run that moved to spool submode does not add up: %d sent, "+
			"%d delivered live, %d deferred", ledger.sent, ledger.delivered, ledger.deferred)
	}
	return hotpathSpoolTransition{transitions: len(o.warnLines), budgetMs: wantBudgetMs, windows: wantWindows}, nil
}

// hotpathUnderColoadFlag is the harness's --under-coload flag (test/bench/hotpath/main.go,
// parseFlags), passed iff obs.UnderCoload() — the job's declaration, forwarded, never this test's
// own guess about who is running it. Both waiver notes the harness writes name the flag verbatim,
// so the same string is what the notes assertions look for.
const hotpathUnderColoadFlag = "--under-coload"

// hotpathBAWaiverMark and hotpathBBWaiverMark are the openings of the harness's baWallWaivedNote
// and bbWallWaivedNote (test/bench/hotpath/report.go) — the one phrase each of those notes carries
// and no other note the harness writes does (B-E's waiver says "wall-clock row"; the
// tail-adjustment notes say "p99"; the two are distinguished from each other by the row name they
// open with). Spelled after obs.BA/obs.BB so a row's name is never a second literal here.
const (
	hotpathBAWaiverMark = string(obs.BA) + "'s row is REPORTED, not gated"
	hotpathBBWaiverMark = string(obs.BB) + "'s row is REPORTED, not gated"
)

// hotpathNonrefDiskPhrase is the harness's nonrefDiskReportedPhrase (test/bench/hotpath/report.go),
// which opens, after the row's name, every note the harness writes for a row it REPORTED because
// the run declared a non-reference disk (obs.NonReferenceDiskEnv, honoured only on GitHub Actions;
// D53(e)). The harness is package main, so the phrase is carried here, as the marks above are.
// hotpathNonrefDiskBAMark, ...BBMark and ...BEMark are that phrase on each row the declaration
// reports: the first two open the B-A and B-B notes, the third B-E's wall-clock row's.
const (
	hotpathNonrefDiskPhrase = "REPORTED, not gated, for this run: " + obs.NonReferenceDiskEnv + " declares"
	hotpathNonrefDiskBAMark = string(obs.BA) + "'s row is " + hotpathNonrefDiskPhrase
	hotpathNonrefDiskBBMark = string(obs.BB) + "'s row is " + hotpathNonrefDiskPhrase
	hotpathNonrefDiskBEMark = string(obs.BE) + "'s wall-clock row is " + hotpathNonrefDiskPhrase
)

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
		"the run's declaration (%s, or %s on GitHub Actions) must leave %s REPORTED (limit_ms null) in this "+
			"run's artifact; it is still gated at %.0fms by every run that declares neither",
		hotpathUnderColoadFlag, obs.NonReferenceDiskEnv, what, limitMs)
	require.Nil(t, row.Pass, "the run's declaration (%s, or %s on GitHub Actions) must leave %s REPORTED "+
		"(pass null) in this run's artifact", hotpathUnderColoadFlag, obs.NonReferenceDiskEnv, what)
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
// CI drives, flags and exit codes included; pathstest.Environ keeps the isolated home and the pinned
// toolchain (not the per-test HOME testutil.NewProject sets), so `go build` resolves the real module
// cache.
func hotpathBuildBenchBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bench-hotpath")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./test/bench/hotpath")
	cmd.Dir = growthModuleRoot(t)
	cmd.Env = pathstest.Environ()
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
// spawn_floor_ms must be present; and the daemon must never transition to spool during an
// isolated run, while a co-loaded one reports a transition that is loud, named and fully
// accounted for (D39, hotpathJudgeSpool). B-A p99 < runtime.hotPath.budgetMs (15ms on linux, 50 on
// Windows and 40 on macOS since D41) and B-E's wall-clock p99 < 2000ms are judged here only when
// the invoking job has not declared the run co-loaded (obs.UnderCoload — ci.yml's `timing` job
// runs this test alone for exactly that), and asserted REPORTED-and-disclosed when it has; see the
// comment above the harness invocation.
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
	// whole-tree runs minutes apart, against 15 ms, while B-B moved only 0.576 → 0.768 / 0.704 ms
	// and was read then as the co-load-robust control. That was item 18/22/25's genus one more time
	// (plans/V2-report.md §0), and the audit V2-MERGE-25 asked for and never got: a wall-clock
	// bound failing on a correct product because it is priced against a host that is no longer
	// there.
	//
	// The flag does not remove any of the three bounds. For B-E it moves the JUDGEMENT to the clock
	// that survives co-load: the harness's B-E_cpu row (budgetIDBECPU, test/bench/hotpath/report.go)
	// gates the same 50 checkpoint children's own user+system CPU time against the same 2000 ms
	// limit, and that clock did not move at all across a quiet/co-loaded pair whose wall p50
	// moved 23x. For B-A — a latency across a process boundary, with no CPU clock to move to —
	// it leaves the judgement to the runs that do not pass the flag.
	//
	// Since the owner's Q3 ruling (2026-09-13) B-B is in that second class too, and the control
	// reading above is retired: B-B has no process boundary inside it, but since f6a8691 it carries
	// the durable path's three fsyncs, which co-load moves like anything else, and there is no CPU
	// clock to re-point it at — no child process to read user+system time off, a cumulative
	// whole-process counter quantised to Windows's 15.625 ms tick, and blocked time that costs no
	// CPU at all. So it is REPORTED here and judged in isolation, which is ADR 0010's own fallback
	// branch rather than an exception to it (the ruling is recorded in test/bench/hotpath/main.go's
	// parseFlags; SP20-D1 design §7.6 priced the option and recommended it). It landed only after
	// the re-budget that made B-B green in isolation — 5d0b904 then cbfa3d3 — so the waiver absorbs
	// the co-loaded tail and nothing else.
	//
	// What that leaves behind in THIS job is stated rather than implied: the whole-tree `test` run
	// keeps no hot-path COST gate at all, only the structural ones — design §6.2's T9, T10 and T14
	// in internal/daemon, co-load-immune and run in every lane — plus the §12.2 spool assertions at
	// the end of this test, which are about what the product DID. Those are not unaffected by the
	// host: the breach detector moves the daemon to spool submode when B-A's wall budget is breached
	// for breachWindows consecutive windows, so here (D39) a transition is reported, and what is still
	// asserted is that it was loud, named the breach, and lost nothing (hotpathJudgeSpool). The
	// isolated runs keep all four spool signs forbidden. All three wall-clock rows are still judged at
	// their limits by every run that does NOT pass the flag, on a reference disk (the owner's quiet
	// runs): bench-gate's and nightly bench-deep's `devtool bench-hotpath` lines, and this test itself
	// in the `timing` job, where every assertion below is the one bench-gate makes. Hosted runs of
	// these lanes report the rows under QOMPACK_NONREFERENCE_DISK (ADR 0010 Addendum 2).
	underCoload := obs.UnderCoload()
	// The other declaration, D53(e): a non-reference disk, honoured only on a GitHub Actions runner
	// (obs.NonReferenceDiskEnv). It is not a flag: the harness reads it from the environment it
	// inherits — pathstest.Environ, the process environment at TestMain, where the job set it — so
	// this row and the harness can never disagree about it. Under it the same three wall rows are
	// REPORTED, each with a note naming that declaration, and the spool transition is reported as
	// under co-load; B-E_cpu, the delivery ledger and every structural check below stay gated. The
	// reference verdict on the wall rows is the owner's quiet local runs, which never set it
	// (test/guards' TestNonReferenceDisk_IsHostedCIOnly).
	nonrefDisk := obs.NonReferenceDisk()
	waived := underCoload || nonrefDisk
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
	cmd.Env = pathstest.Environ()
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
		"bench-hotpath exited non-zero (%s=%v, %s honoured=%v): either a gated budget (B-E_cpu p99<%.0fms "+
			"always; without %s or the non-reference-disk declaration also B-B p99<%.0fms, B-A p99<%.0fms "+
			"and B-E's wall row p99<%.0fms) breached against the real resident state, or the harness itself "+
			"failed (its own delivery-integrity guard included)\nstderr:\n%s",
		obs.UnderColoadEnv, underCoload, obs.NonReferenceDiskEnv, nonrefDisk, hotpathBudgetLimitMs(t, p, obs.BE),
		hotpathUnderColoadFlag, hotpathBudgetLimitMs(t, p, obs.BB), hotpathBudgetLimitMs(t, p, obs.BA),
		hotpathBudgetLimitMs(t, p, obs.BE),
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

	// B-A's and B-B's verdicts are the two-mode block below, with B-E's wall row; their rows are
	// fetched here because the structural cross-checks that follow read their populations.
	ba := hotpathRow(t, rep, string(obs.BA))
	bb := hotpathRow(t, rep, string(obs.BB))

	// B-D is reported, never gated; the wall-clock survives ONLY as the spawn-estimate
	// diagnostic. The structural cross-check below is the teeth behind the b_a_method assertion:
	// the gated row's population is the daemon-side histogram over the harness's planned hot-path
	// sends (spawn loop plus warm-up hot tranche), proven from the harness's own delivery accounting
	// (hotpathDaemonPopulations): n plus the disclosed shortfall is the planned population, B-A's
	// n is at most B-B's and is not exactly the spawn loop's, and the shortfall is requests the
	// delivery ledger deferred to the client spool with 0 lost. A B-A row re-pointed at the
	// wall-clock spawn samples has n equal to the spawn loop's, so it fails in every run, clean or
	// deferring, whether the re-point swapped the row (no shortfall note) or the snapshot (a
	// harness-written one); see that function for which check catches which shape. It no longer
	// reads "n above B-D's": a run that defers more than the warm-up tranche's worth of hook events
	// (§8.1/§12.2's degrade-rather-than-block path: the breach detector moving the daemon to spool
	// submode, or an ACK deadline expiring) has a daemon-side population smaller than the spawn loop
	// and failed that check with nothing wrong with the row's sourcing. A genuine run still fails
	// here in one case: when its B-A shortfall is exactly the warm-up tranche, the counts cannot tell
	// it from a re-point, and the test refuses to guess. Whether such a run can pass B-A is the
	// certification rule's business, and it is unchanged: the deferred samples are counted as over
	// budget (tailAdjustedP99), so a gated run certifies only when its p99 still lands among
	// delivered samples under the limit.
	bd := hotpathRow(t, rep, string(obs.BD))
	require.Nil(t, bd.LimitMs, "B-D must never be gated — it is the host's cost")
	require.Nil(t, bd.Pass)
	require.Equal(t, hotpathBenchIterations, bd.N)
	spawnEst := hotpathRow(t, rep, hotpathSpawnEstimateID)
	require.Nil(t, spawnEst.LimitMs, "the wall-clock floor-subtracted estimate must stay informational")
	require.Nil(t, spawnEst.Pass)
	pop, popErr := hotpathDaemonPopulations(rep)
	require.NoError(t, popErr,
		"the gated B-A row must be sourced from the daemon's hook_controlled histogram (spawn loop "+
			"plus warm-up hot tranche), not from the %d wall-clock spawn samples", bd.N)
	t.Logf("§4.6 populations: planned %d hot-path sends; B-A n=%d (%d never reached hook_controlled), "+
		"B-B n=%d (%d never reached l0_ingest); delivery ledger %+v",
		pop.planned, ba.N, pop.baMissing, bb.N, pop.bbMissing, pop.ledger)

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

	// The three wall-clock rows a co-loaded host or a non-reference disk inflates — B-A, B-B and
	// B-E's wall-clock row — are judged by the run that can judge them and asserted waived by the
	// run that cannot, and in no mode is anything assumed:
	//
	//   - co-loaded (ci.yml's `test` job, QOMPACK_UNDER_COLOAD set): all three rows must come back
	//     REPORTED (limit_ms/pass both null, exactly B-D's shape), and the harness's own
	//     disclosures must be in the notes — one per row, each naming the flag that caused it, the
	//     limit it did not apply and where it still applies (B-E's also names the row that still
	//     enforces the limit here). A null pass field is not an explanation; a reader must be able
	//     to see from the artifact alone which limit went unjudged on which row.
	//   - non-reference disk (QOMPACK_NONREFERENCE_DISK set, as every hosted run of `timing`,
	//     `test-e2e`, bench-gate and nightly bench-deep sets it; ADR 0010 Addendum 2): all three rows
	//     come back REPORTED, and the notes must carry one non-reference-disk disclosure per row
	//     (B-E's naming the row that still enforces the limit) — the nonrefDisk branch below.
	//   - neither (not co-loaded, on a reference disk: the owner's quiet runs, in `timing`'s
	//     isolation shape — this test alone; bench-gate's shape): all three gated at their
	//     obs.Budgets() limits, pass=true, p99 under the limit — and both kinds of waiver note
	//     ABSENT, because a note present without its flag would mean the harness had waived on its
	//     own.
	//
	// B-B is in this block rather than gated unconditionally above because of the Q3 ruling; the
	// gated arm below is the one bench-gate, nightly bench-deep, `timing` and `test-e2e` run on a
	// reference disk (the owner's quiet runs; hosted runs of these lanes report the rows under
	// QOMPACK_NONREFERENCE_DISK, ADR 0010 Addendum 2), and it asserts exactly what the
	// unconditional block asserted before the ruling.
	beWall := hotpathRow(t, rep, string(obs.BE))
	require.Equal(t, hotpathCheckpointSamples, beWall.N)
	if waived {
		hotpathRequireReportedRow(t, ba, baLimit, "B-A")
		hotpathRequireReportedRow(t, bb, bbLimit, "B-B")
		hotpathRequireReportedRow(t, beWall, beLimit, "B-E's wall-clock row")
	}
	if nonrefDisk {
		for _, mark := range []string{hotpathNonrefDiskBAMark, hotpathNonrefDiskBBMark, hotpathNonrefDiskBEMark} {
			require.True(t, hotpathNotesMention(rep, mark),
				"the artifact must disclose each row the non-reference-disk declaration reported, naming the "+
					"declaration (%q); notes present: %q", mark, rep.Notes)
		}
		require.True(t, hotpathNotesMention(rep, hotpathBECPURowID),
			"B-E's non-reference-disk note must name %s, the row that still enforces the limit; notes present: %q",
			hotpathBECPURowID, rep.Notes)
		t.Logf("§4.6 under %s (GitHub Actions): B-A p99=%.3fms (limit %.0fms), B-B p99=%.3fms (limit %.0fms) "+
			"and B-E wall p99=%.3fms (limit %.0fms) are REPORTED here, not judged: a hosted runner's disk is "+
			"not a reference disk (Q1, D53(e)); the owner's quiet reference runs judge all three",
			obs.NonReferenceDiskEnv, ba.P99, baLimit, bb.P99, bbLimit, beWall.P99, beLimit)
	} else {
		require.False(t, hotpathNotesMention(rep, hotpathNonrefDiskPhrase),
			"no non-reference-disk waiver may appear in a run where the declaration is not honoured — the "+
				"harness would be waiving on its own; notes present: %q", rep.Notes)
	}
	if underCoload {
		require.True(t,
			hotpathNotesMention(rep, hotpathUnderColoadFlag) && hotpathNotesMention(rep, hotpathBECPURowID),
			"the artifact must disclose B-E's wall-clock waiver in its notes, naming both the flag that "+
				"caused it and the row that still enforces the limit; notes present: %q", rep.Notes)
		require.True(t, hotpathNotesMention(rep, hotpathBAWaiverMark),
			"the artifact must disclose B-A's waiver in its own note (%q), not only B-E's; notes present: %q",
			hotpathBAWaiverMark, rep.Notes)
		require.True(t, hotpathNotesMention(rep, hotpathBBWaiverMark),
			"the artifact must disclose B-B's waiver in its own note (%q) too: this job leaves no hot-path "+
				"COST gate behind, and a null limit_ms is not an explanation of that; notes present: %q",
			hotpathBBWaiverMark, rep.Notes)
		t.Logf("§4.6 under %s: B-A p99=%.3fms (limit %.0fms), B-B p99=%.3fms (limit %.0fms) and B-E wall "+
			"p99=%.3fms (limit %.0fms) are REPORTED here, not judged; ci.yml's `timing` job runs this "+
			"test alone and judges all three",
			obs.UnderColoadEnv, ba.P99, baLimit, bb.P99, bbLimit, beWall.P99, beLimit)
	} else {
		if !waived {
			hotpathRequireGatedRow(t, ba, baLimit, "B-A")
			hotpathRequireGatedRow(t, bb, bbLimit, "B-B")
			hotpathRequireGatedRow(t, beWall, beLimit, "B-E's wall-clock row")
		}
		require.False(t, hotpathNotesMention(rep, hotpathUnderColoadFlag),
			"no %s waiver may appear in a run that did not pass the flag — the harness would be waiving "+
				"on its own; notes present: %q", hotpathUnderColoadFlag, rep.Notes)
	}

	// spawn_floor_ms present, and recorded for the completion report alongside B-D.
	require.Positive(t, rep.SpawnFloorMs.N, "spawn_floor_ms must be present")
	require.Positive(t, rep.SpawnFloorMs.P50)

	// The §12.2 quadruple — state.bin showing hot=1 while a daemon had it written (and the watcher
	// provably saw the live daemon's record), the WARN transition line in the day logs, its copy in
	// LOUD.log, and a state.bin that survived teardown still saying spool — judged per mode by
	// hotpathJudgeSpool (owner decision D39). Isolated, all four must be absent, exactly as before
	// D39. Co-loaded, a transition is the consequence of the wall budgets this mode waives (ADR
	// 0010), so it is REPORTED — but only a loud, named one with a ledger that adds up: see that
	// function for what a co-loaded transition must still show.
	require.True(t, sawDaemonState.Load(),
		"the state.bin watcher never observed the measured daemon's record; the no-spool check would be vacuous")
	spoolSeen := hotpathSpoolObservation{
		sawSpool:  sawSpool.Load(),
		warnLines: hotpathDegradedWarnLines(t, p),
		loudLines: hotpathDegradedLoudLines(t, p),
	}
	// A cleanly-stopped daemon removes state.bin — but the harness's own teardown DOCUMENTS that
	// a slow Windows daemon exit (go-winio's Close/Accept race, test/bench/hotpath process.go)
	// is tolerated by killing the child after its bound, and a killed child leaves state.bin
	// behind. §4.6's spelling is exactly "state.bin still reports hot=0": when the record
	// survived teardown, it must still say sync unless a co-loaded run's transition was reported.
	if _, statErr := os.Stat(paths.Long(ipc.StatePath(p.Root))); statErr == nil {
		spoolSeen.survivedSpool = ipc.ReadState(p.Root, p.Cfg).Hot != ipc.HotSync
	}
	hotCfg := p.Cfg.Runtime.HotPath
	spoolTr, spoolErr := hotpathJudgeSpool(spoolSeen, waived, hotCfg.BudgetMs, hotCfg.BreachWindows, pop.ledger)
	require.NoError(t, spoolErr)
	if spoolTr.transitions > 0 {
		t.Logf("§4.6 under %s=%v / %s=%v: the daemon moved to §12.2 spool submode %d time(s), REPORTED not "+
			"failed (D39, D53(e)): the WARN and LOUD.log lines name the breach as budget_ms=%d over windows=%d "+
			"consecutive %d-sample windows (state.bin watcher saw hot=1: %v; state.bin left by teardown "+
			"still spool: %v); delivered to the daemon: B-A n=%d p99=%.3fms, B-B n=%d p99=%.3fms, of %d "+
			"planned; delivery ledger %d sent = %d delivered live + %d deferred to the client spool, 0 lost",
			obs.UnderColoadEnv, underCoload, obs.NonReferenceDiskEnv, nonrefDisk,
			spoolTr.transitions, spoolTr.budgetMs, spoolTr.windows, hotpathSampleWindow,
			spoolSeen.sawSpool, spoolSeen.survivedSpool, ba.N, ba.P99, bb.N, bb.P99, pop.planned,
			pop.ledger.sent, pop.ledger.delivered, pop.ledger.deferred)
	}

	// The wall-clock B-E row is logged alongside the CPU one in both modes on purpose: the pair is
	// the evidence for the co-load argument above, and a future reader chasing a B-E question wants
	// to see both numbers from the same run, not just the one that was judged. The verdict word
	// says which mode this run was, and it is carried on B-B too since the Q3 ruling — a B-B number
	// in a co-loaded log is a measurement, and the log must not read as if it were a verdict.
	wallVerdict := "gated"
	switch {
	case underCoload && nonrefDisk:
		wallVerdict = "reported, not gated: " + hotpathUnderColoadFlag + " and " + obs.NonReferenceDiskEnv
	case underCoload:
		wallVerdict = "reported, not gated: " + hotpathUnderColoadFlag
	case nonrefDisk:
		wallVerdict = "reported, not gated: " + obs.NonReferenceDiskEnv
	}
	t.Logf("§4.6 measured (platform %s, n=%d): B-A p99=%.3fms (limit %.0fms, n=%d; %s) | B-B p99=%.3fms "+
		"(limit %.0fms, n=%d; %s) | B-E_cpu p99=%.3fms (limit %.0fms, n=%d) | B-E wall p50=%.3fms p99=%.3fms "+
		"(limit %.0fms; %s) | B-D p50=%.3fms p99=%.3fms max=%.3fms | spawn_floor "+
		"p50=%.3fms p99=%.3fms (n=%d) | B-A_spawn_estimate p50=%.3fms p99=%.3fms | b_a_method=%q",
		rep.Platform, rep.N, ba.P99, baLimit, ba.N, wallVerdict, bb.P99, bbLimit, bb.N, wallVerdict,
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

// hotpathStallFor is test 2's stall for cfg: hotpathStallOverBudget above the B-A budget the daemon
// will gate on (runtime.hotPath.budgetMs), so every stalled sample breaches on every platform.
func hotpathStallFor(cfg config.Config) time.Duration {
	return time.Duration(cfg.Runtime.HotPath.BudgetMs)*time.Millisecond + hotpathStallOverBudget
}

// hotpathStallBinding wraps the §4.2 ObserveTool with a switchable stall. The stall consumes
// the daemon's own clock rather than wall time — see the file comment's decision 2: sleepcheck
// bans wall sleeps, and internal/daemon's own tests simulate a slow hot path through exactly this
// recvTS−reqTS relationship (fakeClock/feedBreachingWindow). Because the daemon, the driver's
// request stamps and this binding share one testutil.FakeClock, each stalled event's stall lands
// in the stamp→receive window of the NEXT request the driver has already stamped, and the breach
// detector reads it off the same recordHotPathSample path production uses.
type hotpathStallBinding struct {
	hp      *hookflowPipeline
	clk     *testutil.FakeClock
	latency time.Duration // hotpathStallFor(cfg)
	stalled atomic.Bool
}

// ObserveTool is the bound Services.ObserveTool for test 2.
func (b *hotpathStallBinding) ObserveTool(ctx context.Context, e hookio.Event) error {
	if b.stalled.Load() {
		b.clk.Advance(b.latency)
	}
	return b.hp.ObserveTool(ctx, e)
}

// TestIntegration_HotPathDegradesRatherThanBlocks is §4.6's second test: the same warm daemon
// (same §4.2 binding, same resident-state construction), a stall of budget + 10ms (25ms at the
// linux budget, hotpathStallFor) injected into the bound ObserveTool for three consecutive
// 512-sample windows, and §8.1's promise held against a real
// L1: the daemon flips to spool, clients stop connecting, every hook still exits 0, no event is
// lost after the next drain, and one WARN line plus the status payload record the transition.
func TestIntegration_HotPathDegradesRatherThanBlocks(t *testing.T) {
	ctx := context.Background()
	bin := buildQompackBinary(t)
	p := testutil.NewProject(t, testutil.WithEnv(testutil.E2EBinaryEnv, bin))

	// The transition arithmetic below is §2.4's: p99 over 512-sample windows against the configured
	// B-A budget (15ms on linux, 50 on Windows, 40 on macOS since D41), three consecutive breaching
	// windows to flip. Pin the premises to the configuration the daemon will actually gate on, so a
	// changed default fails loudly here instead of silently bending the window count.
	require.Equal(t, 3, p.Cfg.Runtime.HotPath.BreachWindows,
		"§4.6's 'three consecutive windows' is cfg.Runtime.HotPath.BreachWindows' default")
	require.True(t, p.Cfg.Runtime.HotPath.SpoolOnBreach,
		"the spool fallback must be enabled for §8.1's degrade to be reachable")
	require.Greater(t, hotpathStallFor(p.Cfg),
		time.Duration(p.Cfg.Runtime.HotPath.BudgetMs)*time.Millisecond,
		"the injected stall must exceed the budget or no window can breach")

	// Same warm state as test 1, through the same binding — built before the daemon starts (the
	// wire lane needs no daemon; §4.2's store-focused tests drive the bound func the same way).
	hotpathSeedTriedBloom(t, p)
	hp := newHookflowPipeline(t, p)
	hotpathPopulate(t, ctx, hp, nil)
	hotpathEnrichSymbols(t, hp)
	hotpathAssertResidentState(t, ctx, p, hp)

	stall := &hotpathStallBinding{hp: hp, clk: p.Clock, latency: hotpathStallFor(p.Cfg)}

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
	// delivered only after that processing has completed. Exactly one stall therefore lands
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
		hotpathWaitPipeline(t, hp, base+sent) // this event's stall has now consumed its clock
		tsCur = tsNext
	}
	require.True(t, nakSeen,
		"after %d stalled sends (cap %d) the daemon never NAKed: three consecutive breaching "+
			"512-sample windows did not flip the hot path to spool", sent, hotpathStallSendCap)
	require.GreaterOrEqual(t, sent, 3*hotpathSampleWindow-hotpathStallWarmEvents,
		"the transition may not fire before three full windows have closed (§2.4)")
	require.Eventually(t, func() bool { return d.Registry().HotMode() == ipc.HotSpool },
		hotpathModeBound, hotpathPipelineTick,
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
	// dies with it, and the two clients above each hold their own client spool in this test process
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

// ─────────────────────────────────────────────────────────────────────────────────────────────
// The B-A population check, pinned without a daemon.
// ─────────────────────────────────────────────────────────────────────────────────────────────

// TestIntegration_HotPathBAPopulationIsTheDaemonHistogram pins hotpathDaemonPopulations, the check
// TestIntegration_HotPathWarmWithRealResidentState uses to prove that the B-A row is the daemon's
// hook_controlled histogram rather than the harness's wall-clock spawn samples, against artifacts
// shaped the way the harness writes them. It exists because the check it replaced, B-A's n above
// B-D's, failed every run in which the documented degrade-rather-than-block path deferred more than
// the warm-up tranche's worth of hook events to the client spool: the Phase 3 Linux gate's artifact
// (1539 of 2064 observed, 525 deferred, 0 lost) is the second case below, verbatim in its numbers.
func TestIntegration_HotPathBAPopulationIsTheDaemonHistogram(t *testing.T) {
	planned := hotpathBenchIterations + hotpathWarmHotTranche
	uncertifiable := func(id string, missing, observed int) string {
		return fmt.Sprintf("%s's p99 CANNOT be certified and its gate is failed on that ground: %d of the %d "+
			"planned samples never reached the daemon's histogram and are counted as over-budget samples, "+
			"which puts the full population's nearest-rank p99 at rank 2044 of %d — inside the un-delivered "+
			"block, since only %d samples were observed.", id, missing, planned, planned, observed)
	}
	exact := func(id string, missing int) string {
		return fmt.Sprintf("%s: %d of the %d planned samples never reached the daemon's histogram and are "+
			"counted back in as over-budget samples, but the full population's nearest-rank p99 still lands "+
			"at rank 2044", id, missing, planned)
	}
	bounded := func(id string, missing int) string {
		return fmt.Sprintf("%s's p99 is reported over the full planned population of %d, not the %d samples the "+
			"daemon actually observed: the %d missing sample(s) are counted back in as over-budget samples",
			id, planned, planned-missing, missing)
	}
	ledger := func(sent, delivered, deferred int) string {
		return fmt.Sprintf("delivery ledger: %d hot-path requests sent, %d delivered live to the daemon, %d "+
			"DEFERRED to the client spool and 0 lost. A deferral is the documented path", sent, delivered, deferred)
	}
	report := func(baN, bbN int, notes ...string) hotpathBenchReport {
		return hotpathBenchReport{N: hotpathBenchIterations, Notes: notes, Budgets: []hotpathBudgetRow{
			{BudgetID: string(obs.BA), N: baN},
			{BudgetID: string(obs.BB), N: bbN},
			{BudgetID: string(obs.BD), N: hotpathBenchIterations},
		}}
	}

	for _, tc := range []struct {
		name    string
		rep     hotpathBenchReport
		wantErr string // "" means the populations must be accepted
	}{
		{name: "every request delivered", rep: report(planned, planned)},
		{
			name: "Phase 3 Linux deferral: 525 of 2064 deferred, 0 lost",
			rep: report(1539, 1539, ledger(2130, 1540, 590),
				uncertifiable(string(obs.BA), 525, 1539), uncertifiable(string(obs.BB), 525, 1539)),
		},
		{
			name: "a few deferred plus one invalid B-A timestamp",
			rep: report(planned-4, planned-3, ledger(planned+66, planned+63, 3),
				bounded(string(obs.BA), 4), exact(string(obs.BB), 3)),
		},
		// Two ways to re-point the gated B-A row at the wall-clock spawn samples. The ROW-level one
		// builds it with buildBudgetRow over the raw samples, which writes no shortfall note: identity
		// (a) refuses it. The SNAPSHOT-level one feeds a snapshot of those samples to
		// buildBudgetRowFromSnapshot, and the harness then discloses "64 of the 2064 planned" as it
		// would for any daemon-side row, so identity (a) holds by construction and the other checks
		// have to catch it; those fixtures carry the B-A note exactly as tailAdjustmentNote writes it.
		{
			name:    "B-A row-level re-point at the wall-clock spawn samples in a clean run",
			rep:     report(hotpathBenchIterations, planned),
			wantErr: "B-A row's n=2000",
		},
		{
			name: "B-A row-level re-point at the wall-clock spawn samples in a deferring run",
			rep: report(hotpathBenchIterations, 1539, ledger(2130, 1540, 590),
				uncertifiable(string(obs.BB), 525, 1539)),
			wantErr: "B-A row's n=2000",
		},
		{
			name: "B-A snapshot-level re-point in a run deferring more than the warm-up tranche",
			rep: report(hotpathBenchIterations, 1539, ledger(2130, 1540, 590),
				uncertifiable(string(obs.BA), hotpathWarmHotTranche, hotpathBenchIterations),
				uncertifiable(string(obs.BB), 525, 1539)),
			wantErr: "more samples than the",
		},
		{
			// A genuine run whose B-B shortfall is exactly the warm-up tranche has these very counts
			// too: the two populations are the same size, and counts alone cannot tell them apart.
			name: "B-A snapshot-level re-point in a run deferring exactly the warm-up tranche",
			rep: report(hotpathBenchIterations, hotpathBenchIterations, ledger(2130, 2066, 64),
				uncertifiable(string(obs.BA), hotpathWarmHotTranche, hotpathBenchIterations),
				uncertifiable(string(obs.BB), hotpathWarmHotTranche, hotpathBenchIterations)),
			wantErr: "cannot be told apart",
		},
		{
			// 54 deferred, and the daemon's hotpath_sample_invalid count (which the harness reads and
			// the artifact does not carry) covering the other 10 of B-A's 64-sample shortfall.
			name: "B-A snapshot-level re-point with a shortfall covered by invalid timestamps",
			rep: report(hotpathBenchIterations, planned-54, ledger(2130, 2076, 54),
				uncertifiable(string(obs.BA), hotpathWarmHotTranche, hotpathBenchIterations),
				uncertifiable(string(obs.BB), 54, planned-54)),
			wantErr: "cannot be told apart",
		},
		{
			name: "B-A snapshot-level re-point in a clean run with 64 invalid timestamps",
			rep: report(hotpathBenchIterations, planned,
				uncertifiable(string(obs.BA), hotpathWarmHotTranche, hotpathBenchIterations)),
			wantErr: "cannot be told apart",
		},
		{
			// The refusal above is of exact equality only: one sample either side is provable.
			name: "a genuine run one sample short of the coincidence",
			rep: report(hotpathBenchIterations+1, hotpathBenchIterations+1, ledger(2130, 2067, 63),
				uncertifiable(string(obs.BA), 63, hotpathBenchIterations+1),
				uncertifiable(string(obs.BB), 63, hotpathBenchIterations+1)),
		},
		{
			name: "a shortfall the delivery ledger does not account for",
			rep: report(1539, 1539,
				uncertifiable(string(obs.BA), 525, 1539), uncertifiable(string(obs.BB), 525, 1539)),
			wantErr: "no delivery ledger note",
		},
		{
			name: "more B-B samples missing than the ledger deferred",
			rep: report(1539, 1539, ledger(2130, 1620, 510),
				uncertifiable(string(obs.BA), 525, 1539), uncertifiable(string(obs.BB), 525, 1539)),
			wantErr: "exceeds the 510 the delivery ledger deferred",
		},
		{
			name:    "more B-A samples than the daemon delivered",
			rep:     report(planned, planned-3, ledger(planned+66, planned+63, 3), exact(string(obs.BB), 3)),
			wantErr: "more samples than the",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hotpathDaemonPopulations(tc.rep)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestIntegration_HotPathSpoolTransitionJudgedPerMode pins hotpathJudgeSpool, the per-mode §12.2
// check TestIntegration_HotPathWarmWithRealResidentState ends with (owner decision D39), against
// observations shaped the way the measured daemon leaves them. The transition lines are rendered
// the way internal/logging's formatLine renders applyHotPathTransition's ToSpool WARN and Loud
// calls; the ledger is the co-loaded Windows run w9 recorded (2130 sent, 1537 delivered live, 593
// deferred, 0 lost), where the breach detector moved to spool after three 512-sample windows.
func TestIntegration_HotPathSpoolTransitionJudgedPerMode(t *testing.T) {
	const budgetMs, windows = 15, 3
	line := func(level string, budget, win int) string {
		return fmt.Sprintf(`ts=2026-09-28T00:00:00.000000001Z level=%s msg="daemon: %s" budget_ms=%d windows=%d`,
			level, hotpathDegradedMsg, budget, win)
	}
	warn, loud := line("warn", budgetMs, windows), line("loud", budgetMs, windows)
	ledger := &hotpathDeliveryLedger{sent: 2130, delivered: 1537, deferred: 593}
	transition := hotpathSpoolObservation{sawSpool: true, warnLines: []string{warn}, loudLines: []string{loud}}

	for _, tc := range []struct {
		name            string
		o               hotpathSpoolObservation
		underCoload     bool
		ledger          *hotpathDeliveryLedger
		wantErr         string // "" means the observation must be accepted
		wantTransitions int
	}{
		// Isolated: unchanged by D39. Each of the four signs fails on its own.
		{name: "isolated clean run", o: hotpathSpoolObservation{}},
		{name: "isolated full transition", o: transition, ledger: ledger, wantErr: "hot=0 (sync) for the entire"},
		{
			name:    "isolated WARN line only",
			o:       hotpathSpoolObservation{warnLines: []string{warn}},
			wantErr: "WARN line may be logged",
		},
		{
			name:    "isolated LOUD.log line only",
			o:       hotpathSpoolObservation{loudLines: []string{loud}},
			wantErr: "LOUD.log must not carry",
		},
		{
			name:    "isolated spool record left by teardown",
			o:       hotpathSpoolObservation{survivedSpool: true},
			wantErr: "survived the harness's teardown",
		},

		// Co-loaded: the transition is reported when it is loud, named and accounted for.
		{name: "co-loaded clean run", o: hotpathSpoolObservation{}, underCoload: true},
		{
			name: "co-loaded transition, loud and named, ledger adds up", o: transition, underCoload: true,
			ledger: ledger, wantTransitions: 1,
		},
		{
			name:        "co-loaded transition the watcher's poll missed, left spool by teardown",
			o:           hotpathSpoolObservation{survivedSpool: true, warnLines: []string{warn}, loudLines: []string{loud}},
			underCoload: true, ledger: ledger, wantTransitions: 1,
		},
		{
			name: "co-loaded spool record with no WARN and no LOUD line", underCoload: true, ledger: ledger,
			o:       hotpathSpoolObservation{sawSpool: true},
			wantErr: "never a silent one",
		},
		{
			name: "co-loaded transition missing its LOUD.log line", underCoload: true, ledger: ledger,
			o:       hotpathSpoolObservation{sawSpool: true, warnLines: []string{warn}},
			wantErr: "LOUD.log 0",
		},
		{
			name: "co-loaded transition missing its WARN line", underCoload: true, ledger: ledger,
			o:       hotpathSpoolObservation{sawSpool: true, loudLines: []string{loud}},
			wantErr: "day logs carry 0",
		},
		{
			name: "co-loaded WARN and LOUD counts disagree", underCoload: true, ledger: ledger,
			o:       hotpathSpoolObservation{sawSpool: true, warnLines: []string{warn, warn}, loudLines: []string{loud}},
			wantErr: "day logs carry 2 and LOUD.log 1",
		},
		{
			name: "co-loaded transition line that does not name the breach", underCoload: true, ledger: ledger,
			o: hotpathSpoolObservation{
				sawSpool: true, loudLines: []string{loud},
				warnLines: []string{`level=warn msg="daemon: ` + hotpathDegradedMsg + `"`},
			},
			wantErr: "does not name the breach",
		},
		{
			name: "co-loaded transition naming a different breach-window count", underCoload: true, ledger: ledger,
			o: hotpathSpoolObservation{
				sawSpool: true, warnLines: []string{warn}, loudLines: []string{line("loud", budgetMs, windows+1)},
			},
			wantErr: "budget_ms=15 windows=4, not the configured",
		},
		{
			name: "co-loaded transition with no delivery ledger", o: transition, underCoload: true,
			wantErr: "carries no delivery ledger note",
		},
		{
			name: "co-loaded transition whose ledger does not add up", o: transition, underCoload: true,
			ledger:  &hotpathDeliveryLedger{sent: 2130, delivered: 1537, deferred: 575},
			wantErr: "does not add up: 2130 sent, 1537 delivered live, 575 deferred",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hotpathJudgeSpool(tc.o, tc.underCoload, budgetMs, windows, tc.ledger)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantTransitions, got.transitions)
			if tc.wantTransitions > 0 {
				require.Equal(t, hotpathSpoolTransition{
					transitions: tc.wantTransitions, budgetMs: budgetMs,
					windows: windows,
				}, got)
			}
		})
	}
}

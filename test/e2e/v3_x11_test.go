package e2e

// This file is V3-VERIFY §5 X11 — TestV3_HotPathUnchangedWithLedgerResident.
//
// Seams: `ipc` client → daemon with observer AND ledger resident (SP-05 + SP-08 + SP-09).
// §13 invariant 9: "Adding work to L0 without a bench result is a review rejection." Wave 2 added
// the entire observer pipeline to the daemon's async path and a resident ledger to its memory;
// B-A must be unaffected. The measurement is test/bench/hotpath — the same real-process-spawn
// harness B-A's CI gate runs — against a warm daemon over a project pre-populated with 2,000 tool
// uses, 40 MB of raw tool output, and a ledger holding 5,000 active eliminations with a rebuilt
// tried.bloom.
//
// Composition (the §5 Rule): no wave-3 package is referenced. The resident state is built through
// the REAL wave-2 components — the real observer (SP-08) over a real store.Open/dag.Open, and the
// real negknow ledger (SP-09) whose RebuildBloom persists sketches/tried.bloom through §7.4's one
// sanctioned door. The measured daemon is the bench harness's own `qompack daemon` child, started
// over that project, exactly as V2's §4.6 test composed it — daemon.Run loads sketches/tried.bloom
// unconditionally at startup, so the rebuilt 5,000-entry filter is genuinely resident in the
// measured process.
//
// B-E's clock: the harness is invoked with --under-coload, so its wall-clock B-E row is reported
// (limit/pass null) and the gated B-E row is B-E_cpu — the same 50 `qompack checkpoint` children
// measured in the CPU time they consumed, against the same 2,000 ms limit. This is the accepted
// V2 ruling for a bench run issued from inside a `go test` binary (plans/V2-report.md, the
// co-load evidence in test/bench/hotpath/report.go's budgetIDBECPU comment): a wall-clock B-E
// sample from a co-loaded test job measures the runner's spare capacity, not the checkpoint, and
// CI already reproduced a 64x wall move with the product byte-identical. The spec bullet
// "B-E p99 < 2 s" is asserted on the CPU row — the gated budget — and the wall row is asserted to
// be present, disclosed, and covering the same 50 children.
//
// NOTE for runners: this is a TIMING test. It spawns ~2,250 real processes and runs two `go
// build`s; run it focused and alone (the P2 lane), not under whole-tree co-load.

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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// ── the X11 corpus: sizes and identities ─────────────────────────────────────────────────────────

const (
	// x11Session is the session every pre-population event and elimination belongs to.
	x11Session = core.SessionID("sess-v3-x11-warm")

	// x11EventTotal is the row's "2 000 tool uses".
	x11EventTotal = 2000
	// x11BenchIterations is the command's --iterations 2000 — the measured spawn count, a
	// different quantity from x11EventTotal that happens to share its value today.
	x11BenchIterations = 2000
	// x11RawFloorBytes is the row's "40 MB of raw tool output", asserted against
	// store.Stats().RawBytes (pre-dedup, pre-compression).
	x11RawFloorBytes = 40 << 20
	// x11PathCount is how many distinct .ts paths the corpus reads; event i touches path
	// i % x11PathCount, so every path accumulates ~50 versions.
	x11PathCount = 40
	// x11EventContentBytes is each event's raw tool output size: 2,000 × 21.5 KB clears the
	// 40 MB floor with ~1 MB of margin, the same corpus arithmetic V2's §4.6 test used.
	x11EventContentBytes = 21504

	// x11EliminationTotal is the row's "5 000 active eliminations".
	x11EliminationTotal = 5000
)

// x11V2BAp99Ms is the V2 figure the row compares against: B-A p99 = 3.072 ms, recorded in the V2
// completion report (plans/V2-report.md §9.1 and its §4.6 row: bench-hotpath, warm daemon,
// n=2064, windows/amd64 — reproduced identically on the final tip re-run). The row's rule:
// "a regression greater than 25% fails this checkpoint even if the absolute number is under
// budget (§7 benchstat policy applied to the hot path)."
const (
	x11V2BAp99Ms        = 3.072
	x11RegressionFactor = 1.25
)

// x11HarnessBound is the wall ceiling on the whole bench-harness subprocess — a subprocess
// lifetime, not a daemon mechanism (the same basis as test/integration's hotpathHarnessBound):
// ~2,250 real spawns plus warm-up, two `go build`s and daemon start/stop sit under two minutes on
// this class of host; six is headroom.
const x11HarnessBound = 6 * time.Minute

// x11InitialEnv is the process environment as it was before any test ran. Child `go build`s and
// the harness itself are run under it so that testutil.NewProject's HOME/USERPROFILE redirection
// (which points into t.TempDir) cannot hide the real module cache from the toolchain — the same
// pattern test/integration pins as initialEnv. Package-level var initialization runs before any
// test (and before TestMain's m.Run), so this snapshot is genuinely pre-test.
var x11InitialEnv = os.Environ()

// ── the bench artifact shape (test/bench/hotpath is package main and cannot be imported; these
// mirror its Report/BudgetRow/SpawnFloor JSON verbatim, exactly as test/integration does) ───────

type x11BudgetRow struct {
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

type x11SpawnFloor struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

type x11BenchReport struct {
	Platform     string         `json:"platform"`
	N            int            `json:"n"`
	BAMethod     string         `json:"b_a_method"`
	SpawnFloorMs x11SpawnFloor  `json:"spawn_floor_ms"`
	Notes        []string       `json:"notes"`
	Budgets      []x11BudgetRow `json:"budgets"`
}

// x11SpawnEstimateID and x11BECPURowID mirror the harness's own renamed rows (ruling #29 and the
// co-load ruling); the harness is package main, so this file carries the literals the same way
// test/integration/hotpath_test.go carries its own copies.
const (
	x11SpawnEstimateID = "B-A_spawn_estimate"
	x11BECPURowID      = "B-E_cpu"
	// x11CheckpointSamples mirrors the harness's checkpointIterations: task-7-spec.md step 7's
	// "spawn qompack checkpoint 50 times", the population BOTH B-E rows are built from.
	x11CheckpointSamples = 50
)

// ── deterministic corpus content ────────────────────────────────────────────────────────────────

// x11FillerWords is the corpus vocabulary: plain lowercase words — nothing the redactor flags and
// nothing (no timestamps, durations or hex runs) the canonicalizer strips.
var x11FillerWords = []string{
	"quartz", "raven", "sable", "tundra", "umbra", "violet", "willow", "xenon",
	"yonder", "zephyr", "amber", "basalt", "cedar", "dune", "ember", "fjord",
}

// x11FilePath is the project-relative .ts path event i reads.
func x11FilePath(event int) string {
	return fmt.Sprintf("src/svc/x11handler%02d.ts", event%x11PathCount)
}

// x11EventID is pre-population event i's tool_use id.
func x11EventID(event int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("toolu_v3_x11_%04d", event))
}

// x11FillerPhrase returns a deterministic 8-word phrase for (event, line). The caller's line
// prefix makes every LINE unique, so content-defined chunking sees genuinely distinct bytes per
// event rather than one repeated block.
func x11FillerPhrase(event, line int) string {
	const phraseWords = 8
	words := make([]string, phraseWords)
	start := (event*13 + line*7) % len(x11FillerWords)
	for w := 0; w < phraseWords; w++ {
		words[w] = x11FillerWords[(start+w)%len(x11FillerWords)]
	}
	return strings.Join(words, " ")
}

// x11FileContent builds event i's raw tool output: unique comment filler up to
// x11EventContentBytes, fully deterministic in i.
func x11FileContent(event int) string {
	var b strings.Builder
	b.Grow(x11EventContentBytes + 128)
	fmt.Fprintf(&b, "// %s - qompack V3-VERIFY X11 fixture, event %04d.\n", x11FilePath(event), event)
	line := 0
	for b.Len() < x11EventContentBytes {
		fmt.Fprintf(&b, "// pad event %04d line %04d %s\n", event, line, x11FillerPhrase(event, line))
		line++
	}
	return b.String()
}

// x11ToolUseEvent shapes one PostToolUse hook event the way the host would: a Read of path whose
// tool_response carries body — the same payload shape eventsFor produces for the Phase-1 corpus.
func x11ToolUseEvent(event int) hookio.Event {
	return hookio.Event{
		HookEventName: hookPostToolUse,
		SessionID:     x11Session,
		ToolName:      "Read",
		ToolUseID:     x11EventID(event),
		ToolInput:     mustJSON(map[string]string{"file_path": x11FilePath(event)}),
		ToolResponse:  mustJSON(map[string]string{"content": x11FileContent(event)}),
	}
}

// x11EliminationTarget / x11EliminationApproach are elimination i's identity. Both embed i so all
// 5,000 descriptors are distinct — the ledger's dedup is identity-based, and a collapsed pair
// would silently shrink the resident set below the row's figure.
func x11EliminationTarget(i int) string {
	return fmt.Sprintf("%s:x11Handler%04d", x11FilePath(i), i)
}

func x11EliminationApproach(i int) string {
	return fmt.Sprintf("widen retry window variant %04d", i)
}

// ── report helpers ──────────────────────────────────────────────────────────────────────────────

// x11Row returns the report row for id, failing the test if the artifact does not carry it.
func x11Row(t *testing.T, rep x11BenchReport, id string) x11BudgetRow {
	t.Helper()
	for _, row := range rep.Budgets {
		if row.BudgetID == id {
			return row
		}
	}
	t.Fatalf("the bench artifact carries no %q row; budgets present: %+v", id, rep.Budgets)
	return x11BudgetRow{}
}

// x11BudgetLimitMs reads id's configured limit from obs.Budgets() against p.Cfg — the same single
// source of truth the harness gates on, never a re-hardcoded number.
func x11BudgetLimitMs(t *testing.T, p *testutil.Project, id obs.BudgetID) float64 {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return float64(b.Limit(p.Cfg).Microseconds()) / 1000.0
		}
	}
	t.Fatalf("obs.Budgets() does not define %s", id)
	return 0
}

// x11NotesMention reports whether any note in the artifact mentions sub.
func x11NotesMention(rep x11BenchReport, sub string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// x11BuildBenchBinary compiles ./test/bench/hotpath and returns the executable: the harness is
// package main, so it is driven as the process CI drives, flags and exit codes included.
func x11BuildBenchBinary(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "bench-hotpath")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./test/bench/hotpath")
	cmd.Dir = root
	cmd.Env = x11InitialEnv
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build -o %s ./test/bench/hotpath:\n%s", out, stderr.String())
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// X11 — TestV3_HotPathUnchangedWithLedgerResident
// ─────────────────────────────────────────────────────────────────────────────────────────────

func TestV3_HotPathUnchangedWithLedgerResident(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)

	// ── Phase A: the resident state, built through the real wave-2 components. ──
	//
	// The store and graph are opened directly (not via p.Store) so this test controls when the
	// store's append-only handles close: on Windows they must be released before the harness's
	// child daemon takes the project over.
	st, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = st.Close()
		}
	}()

	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	metrics := obs.New(p.Clock)
	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root, Cfg: p.Cfg, Store: st, Graph: g,
		Touch:   sketch.NewCMS(phase1CMSEpsilon, phase1CMSDelta),
		Explore: sketch.NewHLL(phase1HLLRegisters),
		Hot:     sketch.NewMisraGries(phase1MGCounters),
		Log:     p.Log, Metrics: metrics, Clock: p.Clock,
	})
	require.NoError(t, err)

	// 2,000 tool uses through the real observer — the same OnToolUse the daemon dispatches.
	for i := 0; i < x11EventTotal; i++ {
		p.Clock.Advance(time.Second)
		_, err := obsv.OnToolUse(ctx, x11ToolUseEvent(i))
		require.NoError(t, err, "pre-population event %d must ingest", i)
	}
	require.NoError(t, g.Flush(ctx), "the DAG must persist to dag/deps.jsonl")
	require.NoError(t, st.Flush(ctx), "the store must persist its buffered writes")

	stats, err := st.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, x11EventTotal, stats.ToolUses, "X11 setup: 2 000 tool uses in the real store")
	require.GreaterOrEqual(t, stats.RawBytes, int64(x11RawFloorBytes),
		"X11 setup: 40 MB of raw tool output in the real store (got %d bytes)", stats.RawBytes)

	// ── The resident ledger: 5,000 active eliminations, then a rebuilt tried.bloom. ──
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: st, Graph: g, Session: x11Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	ledClosed := false
	defer func() {
		if !ledClosed {
			_ = led.Close()
		}
	}()
	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")

	for i := 0; i < x11EliminationTotal; i++ {
		p.Clock.Advance(time.Millisecond)
		rec, warns, ingestErr := m.IngestMCP(ctx, negknow.MCPArgs{
			Target:   x11EliminationTarget(i),
			Approach: x11EliminationApproach(i),
			Reason:   fmt.Sprintf("x11 elimination %04d: the approach was tried and rejected", i),
		})
		require.NoError(t, ingestErr, "elimination %d must record", i)
		require.Empty(t, warns, "elimination %d carries no dependencies, so nothing should be warned about", i)
		require.NotEmpty(t, rec.ID)
	}

	active, err := led.Active(ctx, negknow.ScopeSession)
	require.NoError(t, err)
	require.Len(t, active, x11EliminationTotal,
		"X11 setup: all 5 000 eliminations must be active and visible — a shortfall means the "+
			"identity dedup collapsed distinct records and the resident set is smaller than the row states")

	bloom, health, err := led.RebuildBloom(ctx)
	require.NoError(t, err, "the resident tried.bloom must be REBUILT, not merely accumulated")
	require.Positive(t, bloom.Count(), "the rebuilt filter must carry the active records' keys")
	require.Equal(t, x11EliminationTotal, health.Active, "Health must report all 5 000 records active")
	require.Zero(t, health.Stale)

	triedPath := filepath.Join(paths.Of(p.Root).Sketches, "tried.bloom")
	fi, err := os.Stat(paths.Long(triedPath))
	require.NoError(t, err, "RebuildBloom must persist sketches/tried.bloom — the file the measured "+
		"daemon's Sketches.Load reads at startup, which is what 'ledger resident' means to L0")
	require.Positive(t, fi.Size())
	t.Logf("X11 resident state: ToolUses=%d RawBytes=%d (%.1f MB) StoredBytes=%d DedupRatio=%.2f; "+
		"DAG Nodes=%d Edges=%d; ledger Active=%d Stale=%d FillRatio=%.3f tried.bloom=%dB",
		stats.ToolUses, stats.RawBytes, float64(stats.RawBytes)/(1<<20), stats.Bytes, stats.DedupRatio,
		g.Stats().Nodes, g.Stats().Edges, health.Active, health.Stale, health.FillRatio, fi.Size())

	// Release every handle before the measured daemon takes the project over: the ledger holds
	// records/eliminations.jsonl and the store holds index/*.jsonl append handles, and the
	// harness's child daemon must own the project alone.
	ledClosed = true
	require.NoError(t, led.Close(), "the pre-population ledger must close cleanly")
	storeClosed = true
	require.NoError(t, st.Close(), "the pre-population store must close cleanly")

	// ── Phase B: the measured run — the row's command, driven as the process CI drives. ──
	//
	//   go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool
	//     --warm-daemon --json v3-hotpath.json
	//
	// devtool's bench-hotpath task is a verbatim forwarder to `go run ./test/bench/hotpath`
	// (tools/devtool/benchhotpath.go), so building and spawning the harness binary directly runs
	// the identical measurement without nesting a `go run` inside the test. --project points it at
	// the pre-populated project; --under-coload declares this caller is a `go test` binary (see
	// the file comment for the B-E clock this moves the judgement to).
	bench := x11BuildBenchBinary(t)
	jsonPath := filepath.Join(t.TempDir(), "v3-hotpath.json")

	hctx, hcancel := context.WithTimeout(ctx, x11HarnessBound)
	defer hcancel()
	root, err := moduleRoot()
	require.NoError(t, err)
	cmd := exec.CommandContext(hctx, bench,
		"--iterations", strconv.Itoa(x11BenchIterations),
		"--hook", "observe-tool",
		"--warm-daemon",
		"--under-coload",
		"--json", jsonPath,
		"--project", p.Root,
	)
	cmd.Dir = root
	cmd.Env = x11InitialEnv
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	t.Logf("bench harness stdout:\n%s", stdout.String())
	if raw, readErr := os.ReadFile(jsonPath); readErr == nil {
		t.Logf("bench artifact %s:\n%s", jsonPath, raw)
	}
	require.NoError(t, runErr,
		"bench-hotpath exited non-zero with the observer and the 5 000-elimination ledger resident: "+
			"either a gated budget (B-A p99<%.0fms, B-B p99<%.0fms, B-E_cpu p99<%.0fms) breached, or "+
			"the harness itself failed\nstderr:\n%s",
		x11BudgetLimitMs(t, p, obs.BA), x11BudgetLimitMs(t, p, obs.BB),
		x11BudgetLimitMs(t, p, obs.BE), stderr.String())

	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err, "the harness must write the --json artifact")
	var rep x11BenchReport
	require.NoError(t, json.Unmarshal(raw, &rep))
	require.Equal(t, x11BenchIterations, rep.N)

	// Ruling #29 carried forward: the gated B-A row is the daemon's TS-anchored hook_controlled
	// estimate, not a wall-clock-minus-floor subtraction.
	require.Containsf(t, rep.BAMethod, "hook_controlled",
		"b_a_method must name the TS-anchored hook_controlled estimate: b_a_method=%q", rep.BAMethod)

	baLimit := x11BudgetLimitMs(t, p, obs.BA)
	bbLimit := x11BudgetLimitMs(t, p, obs.BB)
	beLimit := x11BudgetLimitMs(t, p, obs.BE)

	// ── Expected output 1: B-A p99 < 15 ms, pass true. ──
	ba := x11Row(t, rep, string(obs.BA))
	require.NotNil(t, ba.LimitMs, "B-A is a hard gate and must carry its limit")
	require.InDelta(t, baLimit, *ba.LimitMs, 0.001)
	require.NotNil(t, ba.Pass)
	require.True(t, *ba.Pass, "B-A gate must pass with the ledger resident (p99=%.3fms)", ba.P99)
	require.Less(t, ba.P99, baLimit,
		"X11: B-A p99 < %.0fms with the observer pipeline and the 5 000-elimination ledger resident", baLimit)

	// ── Expected output 2: B-B p99 < 2 ms, pass true. ──
	bb := x11Row(t, rep, string(obs.BB))
	require.NotNil(t, bb.LimitMs)
	require.InDelta(t, bbLimit, *bb.LimitMs, 0.001)
	require.NotNil(t, bb.Pass)
	require.True(t, *bb.Pass, "B-B gate must pass with the ledger resident (p99=%.3fms)", bb.P99)
	require.Less(t, bb.P99, bbLimit, "X11: B-B p99 < %.0fms", bbLimit)

	// ── Expected output 3: B-E p99 < 2 s, pass true, on the gated (CPU) row; the wall row is
	// disclosed as reported-not-gated for this --under-coload run (see the file comment). ──
	beCPU := x11Row(t, rep, x11BECPURowID)
	require.Equal(t, x11CheckpointSamples, beCPU.N,
		"the B-E CPU row must cover every checkpoint spawn the harness made")
	require.NotNil(t, beCPU.LimitMs, "B-E_cpu is the gated B-E row under co-load and must carry its limit")
	require.InDelta(t, beLimit, *beCPU.LimitMs, 0.001)
	require.NotNil(t, beCPU.Pass)
	require.True(t, *beCPU.Pass, "B-E gate must pass with the ledger resident (cpu p99=%.3fms)", beCPU.P99)
	require.Less(t, beCPU.P99, beLimit, "X11: B-E p99 < %.0fms (§11.3 L4) on the checkpoint's own clock", beLimit)

	beWall := x11Row(t, rep, string(obs.BE))
	require.Equal(t, x11CheckpointSamples, beWall.N, "both B-E rows must cover the same 50 children")
	require.Nil(t, beWall.LimitMs,
		"--under-coload must leave B-E's wall-clock row ungated in this run's artifact; bench-gate "+
			"still gates it at %.0fms in isolation", beLimit)
	require.Nil(t, beWall.Pass)
	require.True(t, x11NotesMention(rep, "--under-coload") && x11NotesMention(rep, x11BECPURowID),
		"the artifact must disclose the wall-clock waiver in its notes, naming the flag and the row "+
			"that still enforces the limit; notes present: %q", rep.Notes)

	// ── Expected output 4: `pass: true` for each gated budget — swept structurally, so a row this
	// test does not name explicitly can never fail its own gate unnoticed. ──
	for _, row := range rep.Budgets {
		if row.LimitMs == nil {
			continue
		}
		require.NotNil(t, row.Pass, "gated row %s must carry a pass verdict", row.BudgetID)
		require.True(t, *row.Pass, "gated row %s must pass (p99=%.3fms, limit=%.0fms)",
			row.BudgetID, row.P99, *row.LimitMs)
	}

	// ── Expected output 5: B-D reported (never gated). ──
	bd := x11Row(t, rep, string(obs.BD))
	require.Nil(t, bd.LimitMs, "B-D must never be gated — it is the host's process-creation cost")
	require.Nil(t, bd.Pass)
	require.Equal(t, x11BenchIterations, bd.N)
	spawnEst := x11Row(t, rep, x11SpawnEstimateID)
	require.Nil(t, spawnEst.LimitMs, "the wall-clock floor-subtracted estimate must stay informational")
	require.Nil(t, spawnEst.Pass)

	// ── Expected output 6: B-A p99 compared against the V2 completion report's figure — a
	// regression greater than 25% fails this checkpoint even under budget (§7 benchstat policy
	// applied to the hot path). ──
	regressionCeiling := x11V2BAp99Ms * x11RegressionFactor
	require.LessOrEqualf(t, ba.P99, regressionCeiling,
		"X11: B-A p99 regressed more than 25%% against V2's recorded %.3fms (got %.3fms, ceiling "+
			"%.3fms) — wave 2's observer pipeline and resident ledger are NOT allowed to move the hot "+
			"path, budget headroom or not (§13 invariant 9)",
		x11V2BAp99Ms, ba.P99, regressionCeiling)

	// The §5 completion-report row: B-A p99 now vs then, from one artifact.
	t.Logf("X11 measured (platform %s, n=%d): B-A p99 = %.3f ms (V2 was %.3f ms; ceiling %.3f ms, "+
		"limit %.0f ms) | B-B p99=%.3fms (limit %.0fms) | B-E_cpu p99=%.3fms (limit %.0fms, n=%d) | "+
		"B-E wall p50=%.3fms p99=%.3fms (reported, not gated: --under-coload) | B-D p50=%.3fms "+
		"p99=%.3fms max=%.3fms (n=%d) | spawn_floor p50=%.3fms p99=%.3fms (n=%d) | b_a_method=%q",
		rep.Platform, rep.N, ba.P99, x11V2BAp99Ms, regressionCeiling, baLimit,
		bb.P99, bbLimit, beCPU.P99, beLimit, beCPU.N, beWall.P50, beWall.P99,
		bd.P50, bd.P99, bd.Max, bd.N, rep.SpawnFloorMs.P50, rep.SpawnFloorMs.P99, rep.SpawnFloorMs.N,
		rep.BAMethod)
	for _, note := range rep.Notes {
		t.Logf("bench note: %s", note)
	}
}

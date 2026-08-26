package negknow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// This file is the subplan's performance-budget table, implemented (§11.2).
//
// Every row of that table has two things here: a Benchmark that measures it, and a companion
// TestBudget_<Name> that runs the benchmark through testing.Benchmark and REQUIRES the budget. The
// budgets are therefore enforced by `go test` rather than by a human reading benchstat output, and
// a regression that doubles a latency fails the same command everything else in this package fails
// under.
//
// How a budget row is read. The table states p99 figures for the latency rows, and a benchmark
// reports a mean rather than a percentile — so what each TestBudget_ below actually asserts is that
// the benchmark's MEAN ns/op is under the stated budget. That is the strictly-harder-to-satisfy
// reading in the direction that matters (a mean at the p99 ceiling means most operations are far
// over it), and it is the only reading a fixed-iteration-count benchmark can support honestly. The
// two one-shot rows — RebuildBloom and Open — are wall-clock budgets for a single operation, and
// each benchmark iteration performs exactly one of them, so the mean ns/op IS the operation's mean
// wall-clock duration for those.
//
// The fixtures are a fixed-seed synthetic generator in this file and never internal/eval's corpus:
// eval is outside this package's import allow-set (§3.2 constraint 1), and a fixture that changed
// with the corpus would make two commits' numbers incomparable.

// ── budgets (§11.2) ───────────────────────────────────────────────────────────────────────────

const (
	// budgetQueryHit is the §11.2 row "Query (bloom hit, 5 000 records)": a rounding error inside
	// B-F, the mcp_tool_call p95 < 250 ms budget of 00-ARCHITECTURE.md §2.4.
	budgetQueryHit = 50 * time.Microsecond
	// budgetQueryMiss is "Query (bloom miss)": one bloom Test and no map lookup at all.
	budgetQueryMiss = 5 * time.Microsecond
	// budgetRecord is "Record (append + bloom add + DAG emit)": on the MCP path, not the L0 hot
	// path.
	budgetRecord = 5 * time.Millisecond
	// budgetRebuildBloom is "RebuildBloom (5 000 active records, 10 000 keys)" — §8.3 item 3's
	// "cheap, because rebuild is a linear pass over a few thousand structured entries".
	budgetRebuildBloom = 50 * time.Millisecond
	// budgetRefreshStaleness is "RefreshStaleness ledger-side work (5 000 records, 2 000 distinct
	// deps), excluding ChangedSince".
	budgetRefreshStaleness = 10 * time.Millisecond
	// budgetOpen is "Open materialization (20 000 log lines)": daemon start / first ledger use,
	// off the hot path.
	//
	// RULING R26: the plan-invented row said 150 ms, and this is the measured budget that replaced
	// it. The row was never met: materializing 20 000 realistic lines (~666 B each, 13.3 MB of
	// JSON) cost ~185 ms of replay alone, before the index build or the rebuild-from-records a
	// cold Open owes. replayLog was made SINGLE-PASS under the same ruling — one json.Unmarshal
	// per line into log.go's combined logLine instead of a logProbe pass followed by a full
	// record decode — which took replay to ~151 ms and steady-state Open to 186-196 ms on an
	// Intel Core Ultra 7 155H. Still over 150 ms, so the row is revised to a measured budget with
	// room for the machines this runs on, rather than left as a gate nothing can pass.
	budgetOpen = 300 * time.Millisecond
	// budgetDetectorScan is "Detector.Scan (5 000 observations, 5 000 DAG nodes)" — §6.4's
	// "sub-millisecond BFS over a few thousand nodes" plus this package's own linear pass.
	budgetDetectorScan = 5 * time.Millisecond
	// budgetResidentBytes is "Resident memory, 5 000 records".
	//
	// RULING R27: the plan-invented row said 4 MB on an estimate of "~400 B/record + two 32 B
	// keys" = ~464 B/record. The measured figure is 860 B/record, and the difference is
	// structural rather than wasteful — the estimate counts a record's own text and its two bloom
	// keys, and omits everything the ledger builds around them. Measured directly, per record:
	//
	//	352 B  the recs slice backing array — sizeof(Record) is 296 B and append's growth leaves
	//	       cap ~19 % over len at 5 000 (cap 5 950)
	//	169 B  the record's own text: target + approach + reason (92), descriptor path + symbol +
	//	       class (42), id + session (35)
	//	 66 B  the DependsOn backing array (one 48 B Dep) plus its path text
	//	128 B  byMatch's and byKey's map keys, which are the 64-CHARACTER HEX spelling of a 32-byte
	//	       key rather than the key itself
	//	142 B  the three maps' buckets and byMatch's per-entry []int
	//	2.4 B  tried.bloom's share (11 984 B for all 5 000 records)
	//
	// The ruling revises the row to 5 MB and explicitly does NOT re-key the indices on the raw
	// 32-byte key, which is the one change that would move the number materially (it would halve
	// the 128 B row). That stays available to a later task.
	budgetResidentBytes = 5 * (1 << 20)
)

// Two of those numbers are the plan's; two are measured budgets that replaced plan-invented rows
// this task found unmeetable. The distinction is recorded because a budget quietly edited to match
// a measurement stops being a budget: SP-09 Task 5 measured every row, reported the two shortfalls,
// and the plan owner ruled on both (R26 for Open, R27 for resident memory). Each revised constant
// above carries its measurement and its ruling.
//
// Measured on an Intel Core Ultra 7 155H, uninstrumented, after the single-pass replay change:
//
//	BenchmarkQueryHit         1.3-1.9 us/op   against 50 us
//	BenchmarkQueryMiss        1.0-1.2 us/op   against 5 us
//	BenchmarkRecord           27-32 us/op     against 5 ms
//	BenchmarkRebuildBloom     10-12 ms/op     against 50 ms
//	BenchmarkRefreshStaleness 1.6-1.9 ms/op   against 10 ms
//	BenchmarkOpen             186-196 ms/op   against 300 ms (R26; was 235-304 against 150)
//	BenchmarkDetectorScan     1.8-1.9 ms/op   against 5 ms
//	TestMemoryFootprint       4.30 MB         against 5 MB (R27; the row's own estimate was 4)

// The fixture sizes each budget row names.
const (
	benchRecordCount      = 5000
	benchOpenLogLines     = 20000
	benchDistinctDeps     = 2000
	benchObservationCount = 5000
	benchDAGNodeCount     = 5000
	benchFiles            = 40
	benchSignalsPerTurn   = 4
	// benchPlantedPatterns is how many complete Pattern P instances the observation corpus
	// deliberately contains. Fifty abandoned approaches in a five-thousand-signal session is a
	// heavy but believable ratio; a corpus where every fourth signal completed a pattern would
	// measure emission, not detection, and detection is what the §11.2 row is about.
	benchPlantedPatterns = 50
)

// ── instrumentation scaling ───────────────────────────────────────────────────────────────────
//
// The budgets above are claims about the SHIPPED binary, and two of the ways this module is tested
// do not produce one. `devtool cover` builds with -covermode=atomic, which injects a sync/atomic
// add on every statement; `go test -race` (which `devtool ci-local`'s test-race step and CI's
// `test` job both run) instruments every memory access, and this package's query path is map-heavy
// and lock-guarded, which is exactly what the race detector charges most for.
//
// So the ceilings are SCALED per instrumentation rather than skipped, which is the pattern
// internal/dag/bench_test.go established for the same problem and for the same reason: Rule W-1
// bans t.Skip here, and a gate that silently evaporates under -race is one nobody notices has
// stopped running. Scaled, the gates stay live under every build as backstops against an
// order-of-magnitude regression, and `ci-local`'s uninstrumented `test` step is where the real,
// unscaled figure is asserted.
//
// The coverage factor is internal/dag's and holds here. The race factor is NOT dag's 8x any more:
// the heaviest row, BenchmarkOpen, measures ~190 ms uninstrumented against 1.61 s under -race warm
// (8.5x), 2.25 s in a cold process (11.8x), and 2.63 s under -race with the whole tree's test
// binaries co-running (13.9x) — the configuration CI's `test` job runs on all three OSes, and the
// one that breached the old 8x ceiling (2.4 s) in SP-09's own gate run. RULING R34 re-checked the
// factor exactly as this paragraph used to demand and set 16x: it covers every measured inflation
// with ~15% headroom, still fails an order-of-magnitude regression (the only thing a scaled gate
// is for), and the real 300 ms figure stays asserted by every uninstrumented run. Every other row
// is far below the ceiling either way (the query rows inflate ~11x but sit three orders of
// magnitude inside their budgets). A future revision of the Open budget still has to re-check
// this factor rather than assume it has headroom left.
//
// This file detects -race through debug.ReadBuildInfo rather than through internal/dag's pair of
// build-tagged files, because the setting is right there in the test binary's own build settings
// and one file is easier to keep honest than three.
const (
	coverageBudgetFactor = 4
	raceBudgetFactor     = 16
)

// raceEnabled reports whether this test binary was built with -race. The go command records the
// flag in the binary's build settings, which is the only runtime signal Go offers for it —
// testing.CoverMode() has no equivalent for the race detector.
var raceEnabled = func() bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range bi.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}
	return false
}()

// budgetFactor is how much the instrumentation in this build inflates a wall-clock measurement.
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

// budgetNote annotates a breach with the build that produced it, so an instrumentation-only
// failure is not misread as a plain regression against the figures the plan quotes.
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

// budgetAttempts is how many times requireBudget will run a benchmark before failing it, and the
// BEST of those runs is what the budget is asserted against. RULING R30 approved the estimator.
//
// This is not a way of retrying until the number is convenient. A wall-clock gate has to measure
// the code, and two things that are not the code inflate a single sample badly here. One is
// co-tenancy: this repository is developed with several test binaries running at once, and the
// house note on it — two wall-clock tests that fail only under co-load — is exactly this failure
// mode. The other is warm-up, which is systematic rather than random: the FIRST execution of
// BenchmarkOpen in a process pays for reading a 13 MB fixture that is not yet in the page cache
// and for growing the heap to hold 20 000 materialized records, and it measures 444 ms/op against
// the 186-196 ms every later execution in that same process reports.
//
// The minimum over a few attempts is the standard robust estimator for "how fast can this go",
// and it does not weaken the gate: a real regression is slow on every attempt, so it still fails.
// A benchmark inside its budget passes on the first attempt and costs exactly one run.
//
// A pass that needed more than one attempt logs "budget met on attempt N of M". That line is there
// to be grepped: a row that starts needing its retries is a row drifting toward its ceiling, and
// the difference between "passed" and "barely passed" should not be invisible in a CI log.
const budgetAttempts = 3

// requireBudget runs fn as a benchmark and requires its best mean ns/op to be at or under budget.
//
// A benchmark that fails or skips leaves testing.Benchmark with a zero result rather than
// propagating the failure — its output goes to the benchmark's own buffer, which nothing prints —
// so a zero iteration count is checked first and reported as what it is: the fixture broke, not
// the budget.
func requireBudget(t *testing.T, name string, fn func(*testing.B), budget time.Duration) {
	t.Helper()
	ceiling := budget * time.Duration(budgetFactor())

	var best time.Duration
	var iters, attempts int
	for attempt := 1; attempt <= budgetAttempts; attempt++ {
		res := testing.Benchmark(fn)
		require.NotZero(t, res.N,
			"%s ran zero iterations: the benchmark itself failed or skipped, so nothing was measured", name)
		attempts = attempt
		got := time.Duration(res.NsPerOp())
		if attempt == 1 || got < best {
			best, iters = got, res.N
		}
		if best <= ceiling {
			break
		}
		// Log THIS attempt's sample, not the running best — T9's gate run printed the best three
		// times over, which read as an impossibly identical re-measurement.
		t.Logf("%s: attempt %d is %v/op, over the %v ceiling — retrying", name, attempt, got, ceiling)
	}

	if attempts > 1 && best <= ceiling {
		t.Logf("%s: budget met on attempt %d of %d", name, attempts, budgetAttempts)
	}
	t.Logf("%s: %v/op over %d iterations (best of %d) against a %v budget%s",
		name, best, iters, attempts, budget, budgetNote())
	require.LessOrEqual(t, best, ceiling,
		"%s: %v/op exceeds the §11.2 budget of %v%s", name, best, budget, budgetNote())
}

// ── the fixed-seed synthetic generator ────────────────────────────────────────────────────────

// benchSeed fixes the generator so every run of every benchmark walks the identical corpus.
// Comparing two commits' numbers over two different corpora would measure the generator.
const benchSeed = 0x5109

// benchPick is a fixed-seed table of pseudo-random choices. The corpus is indexed rather than
// streamed from an *rand.Rand so that record i is the same record at any corpus size and in any
// benchmark, without a generator whose state depends on how many records came before it.
var benchPick = func() []int {
	rng := rand.New(rand.NewSource(benchSeed))
	out := make([]int, 1<<10)
	for i := range out {
		out[i] = rng.Intn(1 << 20)
	}
	return out
}()

// benchChoice returns the i'th fixed-seed choice. It is not named pick: this package already has
// a pick, and it is Query's active/stale candidate selector.
func benchChoice(i int) int { return benchPick[i%len(benchPick)] }

// benchApproaches are the approach phrases the corpus draws from. They classify to distinct
// ApproachClass labels, so two synthetic records never collide on a MatchKey by accident.
var benchApproaches = []string{
	"widen pool timeout",
	"disable connection pooling",
	"cache the parsed schema",
	"retry the failing request",
	"batch the writes",
	"inline the helper",
	"shard the queue",
	"pin the dependency version",
}

// benchSession is the session every synthetic record belongs to.
const benchSession core.SessionID = "sess-negknow-bench"

// The hash domains the generator mints its evidence roots and dependency versions under.
const (
	benchEvidenceDomain = "negknow.bench.evidence"
	benchDepDomain      = "negknow.bench.dep"
)

// benchEpochMillis is the fixed wall-clock instant every seeded record is stamped with, so a
// seeded log is byte-identical between runs.
const benchEpochMillis int64 = 1767225480000

// benchHash mints a deterministic, non-zero core.Hash for a synthetic record's evidence or a
// synthetic dependency's version.
func benchHash(domain, seed string) core.Hash { return core.HashBytes(domain, []byte(seed)) }

// benchPathAt returns the i'th synthetic source path, in paths.Key form.
func benchPathAt(i int) string {
	return fmt.Sprintf("src/pkg%02d/mod%02d.ts", i%benchFiles, i%benchFiles)
}

// benchDepAt returns the i'th synthetic dependency, drawn from benchDistinctDeps distinct paths.
func benchDepAt(i int) Dep {
	p := fmt.Sprintf("deps/lock%04d.json", i%benchDistinctDeps)
	return Dep{Path: p, Hash: benchHash(benchDepDomain, p)}
}

// benchRecordAt returns the i'th synthetic elimination: a distinct target for every i, so no two
// records dedup on identity and no two share a MatchKey, and a dependency drawn from the
// benchDistinctDeps-wide pool the RefreshStaleness row names.
func benchRecordAt(i int) Record {
	return Record{
		Target:    fmt.Sprintf("src/pkg%02d/mod%04d.ts:fn%04d", i%benchFiles, i, i),
		Approach:  benchApproaches[benchChoice(i)%len(benchApproaches)],
		Reason:    fmt.Sprintf("synthetic elimination %d observed on run %d", i, benchChoice(i+1)%97),
		Evidence:  benchHash(benchEvidenceDomain, strconv.Itoa(i)),
		DependsOn: []Dep{benchDepAt(benchChoice(i + 2))},
	}
}

// benchProject returns a fresh project root with the .qompack/ layout created and the REAL
// Appendix C configuration loaded for it.
//
// It is ledger_test.go's newProject with a testing.TB receiver: a benchmark cannot use the *T
// version, and ruling R16 rules out internal/testutil here — testutil imports internal/cli ->
// internal/daemon -> internal/negknow, which is an import cycle for an in-package test file.
func benchProject(tb testing.TB) (string, config.Config) {
	tb.Helper()
	dir := tb.TempDir()
	if err := paths.EnsureLayout(paths.Of(dir)); err != nil {
		tb.Fatalf("paths.EnsureLayout: %v", err)
	}
	cfg, _, _, err := config.Load(config.Env{
		ProjectRoot: dir,
		Getenv:      func(string) string { return "" },
	})
	if err != nil {
		tb.Fatalf("config.Load: %v", err)
	}
	return dir, cfg
}

// benchDeps is the Deps every fixture below opens its ledger with: a frozen clock (so the
// histograms cost no syscall inside the timed region), a discarding logger, and whatever store
// and graph the row under test needs.
func benchDeps(s store.Store, g dag.Graph) Deps {
	return Deps{Store: s, Graph: g, Session: benchSession, Log: logging.Nop(), Clock: frozenClock{}}
}

// benchOpen opens a ledger and registers its Close. The Close is not tidiness: Windows refuses to
// unlink a file with an open handle, so a ledger left open makes TempDir's own cleanup fail.
func benchOpen(tb testing.TB, root string, cfg config.Config, d Deps) *ledger {
	tb.Helper()
	led, err := Open(root, cfg, nil, d)
	if err != nil {
		tb.Fatalf("negknow.Open: %v", err)
	}
	tb.Cleanup(func() { _ = led.Close() })
	l, ok := led.(*ledger)
	if !ok {
		tb.Fatal("negknow.Open must return this package's own *ledger")
	}
	return l
}

// benchLedger builds a ledger holding n synthetic records, through the real Record path.
func benchLedger(tb testing.TB, n int, d Deps) *ledger {
	tb.Helper()
	root, cfg := benchProject(tb)
	l := benchOpen(tb, root, cfg, d)
	ctx := context.Background()
	for i := range n {
		if _, err := l.Record(ctx, benchRecordAt(i)); err != nil {
			tb.Fatalf("seeding record %d: %v", i, err)
		}
	}
	return l
}

// benchStore is the store.Store the RefreshStaleness row runs against: fakeStore's whole surface,
// with ChangedSince replaced by one that reports nothing changed and RECORDS NOTHING.
//
// The row is "RefreshStaleness ledger-side work … EXCLUDING ChangedSince", and this is how the
// exclusion is made real: the store call is a return statement, so what the benchmark measures is
// the ledger's own dependency collection, grouping and bookkeeping. fakeStore's own ChangedSince
// copies and retains every argument slice, which at 2 000 deps per call and a benchmark's
// iteration count would measure an ever-growing call log instead.
type benchStore struct{ *fakeStore }

func (benchStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	return nil, nil
}

var _ store.Store = benchStore{}

// benchGraph opens a real dependence graph over root. It is real rather than a fake for the reason
// detector_test.go gives: the shapes this package walks are the real graph's shapes.
func benchGraph(tb testing.TB, root string, cfg config.Config) dag.Graph {
	tb.Helper()
	g, err := dag.Open(root, cfg, logging.Nop())
	if err != nil {
		tb.Fatalf("dag.Open: %v", err)
	}
	return g
}

// ── Query ─────────────────────────────────────────────────────────────────────────────────────

func BenchmarkQueryHit(b *testing.B) {
	l := benchLedger(b, benchRecordCount, benchDeps(nil, nil))
	hit := benchRecordAt(benchRecordCount / 2)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a, err := l.Query(ctx, hit.Target, hit.Approach, ScopeSession)
		if err != nil {
			b.Fatal(err)
		}
		if a.State != AnswerActive {
			b.Fatalf("Query: state %v, want AnswerActive — the fixture is not a bloom hit", a.State)
		}
	}
}

func BenchmarkQueryMiss(b *testing.B) {
	l := benchLedger(b, benchRecordCount, benchDeps(nil, nil))
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a, err := l.Query(ctx, "src/never/recorded.ts:missing", "rewrite the module", ScopeSession)
		if err != nil {
			b.Fatal(err)
		}
		// BloomOnly is NOT asserted: at 10 000 keys in a 10 000-capacity filter the configured 1 %
		// false-positive rate is real, and a run that happened to collide is the filter working as
		// specified, not the fixture breaking. The state is what must hold either way.
		if a.State != AnswerAbsent {
			b.Fatalf("Query: state %v, want AnswerAbsent", a.State)
		}
	}
}

func TestBudget_QueryHit(t *testing.T) {
	requireBudget(t, "BenchmarkQueryHit", BenchmarkQueryHit, budgetQueryHit)
}

func TestBudget_QueryMiss(t *testing.T) {
	requireBudget(t, "BenchmarkQueryMiss", BenchmarkQueryMiss, budgetQueryMiss)
}

// ── Record ────────────────────────────────────────────────────────────────────────────────────

// BenchmarkRecord measures the whole append path the §11.2 row names — the JSONL append, both
// bloom Adds and the DAG emission — so the graph is a REAL dag.Graph and not the nil one a
// no-graph ledger would silently skip.
func BenchmarkRecord(b *testing.B) {
	root, cfg := benchProject(b)
	g := benchGraph(b, root, cfg)
	l := benchOpen(b, root, cfg, benchDeps(nil, g))
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if _, err := l.Record(ctx, benchRecordAt(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBudget_Record(t *testing.T) {
	requireBudget(t, "BenchmarkRecord", BenchmarkRecord, budgetRecord)
}

// ── RebuildBloom ──────────────────────────────────────────────────────────────────────────────

// BenchmarkRebuildBloom measures one full §3.3 rebuild over 5 000 active records — 10 000 keys,
// because every record contributes both Key and MatchKey — INCLUDING the persistence through
// sketch.ReplaceGenerational, which is the only door tried.bloom is ever replaced through and
// therefore part of what the row costs.
func BenchmarkRebuildBloom(b *testing.B) {
	l := benchLedger(b, benchRecordCount, benchDeps(nil, nil))
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		nb, h, err := l.RebuildBloom(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if h.Active != benchRecordCount {
			b.Fatalf("Health.Active = %d, want %d", h.Active, benchRecordCount)
		}
		if nb.Count() == 0 {
			b.Fatal("the rebuilt filter is empty")
		}
	}
}

func TestBudget_RebuildBloom(t *testing.T) {
	requireBudget(t, "BenchmarkRebuildBloom", BenchmarkRebuildBloom, budgetRebuildBloom)
}

// ── RefreshStaleness ──────────────────────────────────────────────────────────────────────────

func BenchmarkRefreshStaleness(b *testing.B) {
	s := benchStore{newFakeStore()}
	l := benchLedger(b, benchRecordCount, benchDeps(s, nil))
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		flipped, err := l.RefreshStaleness(ctx, s)
		if err != nil {
			b.Fatal(err)
		}
		if len(flipped) != 0 {
			b.Fatalf("RefreshStaleness flipped %d records; the store reports nothing changed", len(flipped))
		}
	}
}

func TestBudget_RefreshStaleness(t *testing.T) {
	requireBudget(t, "BenchmarkRefreshStaleness", BenchmarkRefreshStaleness, budgetRefreshStaleness)
}

// ── Open ──────────────────────────────────────────────────────────────────────────────────────

// benchSeededRecord is the i'th synthetic record as the ledger would have WRITTEN it: session,
// timestamp, descriptor, id and status all filled in, so a log built from these materializes
// without the ledger having to default anything.
func benchSeededRecord(i int) Record {
	r := benchRecordAt(i)
	r.Session = benchSession
	r.TS = core.UnixMilli(benchEpochMillis + int64(i))
	r.Scope, r.Status = ScopeSession, StatusActive
	r.Desc = Canonicalize(r.Target, r.Approach, r.Reason)
	r.ID = recordID(r.Session, r.TS, r.Desc)
	return r
}

// benchLogBytes renders n record lines in the on-disk wire form.
func benchLogBytes(tb testing.TB, n int) []byte {
	tb.Helper()
	var buf bytes.Buffer
	for i := range n {
		line, err := json.Marshal(benchSeededRecord(i))
		if err != nil {
			tb.Fatalf("marshalling seeded record %d: %v", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// BenchmarkOpen measures a COLD Open over a 20 000-line log: the replay, the index build, and the
// rebuild-from-records the missing filter forces.
//
// sketches/ is cleared before every iteration, untimed. Without it the first iteration would pay
// for the rebuild and every later one would load the filter it left behind, so the reported mean
// would drift with b.N — and it is the cold path that the row is about, since "daemon start /
// first ledger use" is exactly the case where no filter has been written yet.
func BenchmarkOpen(b *testing.B) {
	root, cfg := benchProject(b)
	p := logPath(root)
	if err := os.MkdirAll(filepath.Dir(p), logDirPerm); err != nil {
		b.Fatalf("creating the records directory: %v", err)
	}
	if err := os.WriteFile(p, benchLogBytes(b, benchOpenLogLines), 0o600); err != nil {
		b.Fatalf("seeding the elimination log: %v", err)
	}
	sketches := paths.Of(root).Sketches

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if err := os.RemoveAll(sketches); err != nil {
			b.Fatalf("clearing sketches/: %v", err)
		}
		b.StartTimer()

		l, err := Open(root, cfg, nil, benchDeps(nil, nil))
		if err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		led, ok := l.(*ledger)
		if !ok {
			b.Fatal("negknow.Open must return this package's own *ledger")
		}
		if got := len(led.recs); got != benchOpenLogLines {
			b.Fatalf("materialized %d records, want %d", got, benchOpenLogLines)
		}
		if err := l.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func TestBudget_Open(t *testing.T) {
	requireBudget(t, "BenchmarkOpen", BenchmarkOpen, budgetOpen)
}

// ── Detector.Scan ─────────────────────────────────────────────────────────────────────────────

// benchObsSource is the ObservationSource the detector row scans: benchObservationCount signals,
// rather than the ledger's own signalRing-bounded history, which holds only the last 512.
//
// It embeds the ledger so the detector still finds the depResolver and metricsSource seams
// NewDetector looks for — a source implementing neither gets no dependency fallback and a
// no-op registry, which would make the benchmark measure less than Scan actually does.
type benchObsSource struct {
	*ledger
	signals []Observation
}

func (s benchObsSource) Since(core.TurnIndex) []Observation { return s.signals }

var _ ObservationSource = benchObsSource{}

// benchObservationCorpus is one long session's signals: benchObservationCount observations over
// benchFiles files at benchSignalsPerTurn per turn, of which benchPlantedPatterns are deliberately
// complete Pattern P instances (edit -> failing test with an evidence root -> revert -> different
// edit) and the rest is noise — edits nobody reverted, tests that passed, reverts with no failure
// in front of them, failing tests with no evidence behind them.
//
// The noise is the point. Every noise edit with a Detail is still a Pattern P candidate and still
// pays for the windowed forward scan, which is the linear pass the §11.2 row budgets; only the
// planted ones reach candidateRecord and emit, because eliminations.requireEvidence drops a
// heuristic guess with no stored failing-test output behind it.
func benchObservationCorpus() []Observation {
	kinds := []ObsKind{ObsEdit, ObsEdit, ObsTestFail, ObsTestPass, ObsRevert}
	out := make([]Observation, benchObservationCount)
	for i := range out {
		out[i] = Observation{
			Turn:   core.TurnIndex(i / benchSignalsPerTurn),
			Kind:   kinds[benchChoice(i)%len(kinds)],
			Path:   benchPathAt(benchChoice(i + 3)),
			Detail: benchApproaches[benchChoice(i+4)%len(benchApproaches)],
		}
	}

	stride := benchObservationCount / benchPlantedPatterns
	for k := range benchPlantedPatterns {
		i := k * stride
		base := core.TurnIndex(i / benchSignalsPerTurn)
		p := benchPathAt(k)
		out[i] = Observation{Turn: base, Kind: ObsEdit, Path: p, Detail: benchApproaches[0]}
		out[i+1] = Observation{
			Turn: base + 1, Kind: ObsTestFail, Path: p,
			Root:    benchHash(benchEvidenceDomain, "pattern/"+strconv.Itoa(k)),
			ToolUse: core.ToolUseID(fmt.Sprintf("bench-tool-%04d", k)),
		}
		out[i+2] = Observation{Turn: base + 2, Kind: ObsRevert, Path: p}
		out[i+3] = Observation{Turn: base + 3, Kind: ObsEdit, Path: p, Detail: benchApproaches[1]}
	}
	return out
}

// benchScanGraph builds the benchDAGNodeCount-node dependence graph the detector walks: one file
// node per synthetic path, a tool-use node for every planted pattern's failing test with
// consumes-edges to three files each, and tool-use/tool-result filler up to the node count.
func benchScanGraph(tb testing.TB, root string, cfg config.Config) dag.Graph {
	tb.Helper()
	g := benchGraph(tb, root, cfg)

	add := func(n dag.Node) {
		if err := g.AddNode(n); err != nil {
			tb.Fatalf("dag.AddNode(%s): %v", n.ID, err)
		}
	}
	edge := func(e dag.Edge) {
		if err := g.AddEdge(e); err != nil {
			tb.Fatalf("dag.AddEdge: %v", err)
		}
	}

	nodes := 0
	for i := range benchFiles {
		p := benchPathAt(i)
		add(dag.Node{ID: dag.FileNode(p), Kind: dag.KindFile, Turn: 0, Ref: p, Root: benchHash(benchDepDomain, p)})
		nodes++
	}
	for k := range benchPlantedPatterns {
		use := dag.ToolUseNode(core.ToolUseID(fmt.Sprintf("bench-tool-%04d", k)))
		add(dag.Node{ID: use, Kind: dag.KindToolUse, Turn: core.TurnIndex(k), Ref: "Bash"})
		nodes++
		for j := range 3 {
			edge(dag.Edge{From: use, To: dag.FileNode(benchPathAt(k + j)), Kind: dag.EdgeConsumes, Turn: core.TurnIndex(k)})
		}
	}
	for i := 0; nodes < benchDAGNodeCount; i++ {
		id := core.ToolUseID(fmt.Sprintf("bench-filler-%05d", i))
		add(dag.Node{ID: dag.ToolUseNode(id), Kind: dag.KindToolUse, Turn: core.TurnIndex(i), Ref: "Read"})
		nodes++
		if nodes >= benchDAGNodeCount {
			break
		}
		add(dag.Node{ID: dag.ToolResultNode(id), Kind: dag.KindToolResult, Turn: core.TurnIndex(i), Ref: string(id)})
		nodes++
	}
	return g
}

func BenchmarkDetectorScan(b *testing.B) {
	root, cfg := benchProject(b)
	l := benchOpen(b, root, cfg, benchDeps(benchStore{newFakeStore()}, nil))
	g := benchScanGraph(b, root, cfg)
	src := benchObsSource{ledger: l, signals: benchObservationCorpus()}
	det := NewDetector(src, benchSession, cfg, frozenClock{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		recs, err := det.Scan(ctx, g, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(recs) == 0 {
			b.Fatal("Scan emitted nothing: the corpus no longer contains a complete Pattern P")
		}
	}
}

func TestBudget_DetectorScan(t *testing.T) {
	requireBudget(t, "BenchmarkDetectorScan", BenchmarkDetectorScan, budgetDetectorScan)
}

// ── resident memory ───────────────────────────────────────────────────────────────────────────

// TestMemoryFootprint asserts the §11.2 "Resident memory, 5 000 records" row as ruling R27 revised
// it: a ledger holding 5 000 materialized records — the records slice, the three indices over it,
// and the tried.bloom the configured capacity sizes — stays inside 5 MB of live heap. The
// per-record arithmetic behind that number is at budgetResidentBytes.
//
// Two GC()+ReadMemStats readings bracket the construction, and each reading is taken after TWO
// collections: the first drops what is unreachable, and the second collects what the first one's
// finalizers made unreachable, so HeapAlloc is a LIVE-heap figure rather than a high-water mark.
// runtime.KeepAlive holds the ledger past the second reading, without which the compiler is free
// to consider it dead before it is measured — which would make this test pass for any footprint at
// all.
//
// This is a wall-of-memory assertion and not a benchmark, so it is unaffected by the
// instrumentation scaling above: -race and -covermode change how long the code takes, not how many
// bytes a Record occupies.
func TestMemoryFootprint(t *testing.T) {
	root, cfg := benchProject(t)
	l := benchOpen(t, root, cfg, benchDeps(nil, nil))
	ctx := context.Background()

	// The baseline is taken with the ledger already OPEN and empty, so what the delta measures is
	// the marginal cost of holding 5 000 records — the records slice, the three indices over it,
	// and the bloom bits they set — rather than the fixed cost of a ledger, a loaded
	// config.Config and an empty 12 KB filter, none of which grow with the record count.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&before)

	for i := range benchRecordCount {
		_, err := l.Record(ctx, benchRecordAt(i))
		require.NoError(t, err, "seeding record %d", i)
	}
	require.Equal(t, benchRecordCount, l.Health().Records)

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(l)

	require.Greater(t, after.HeapAlloc, before.HeapAlloc, "the ledger must occupy SOMETHING")
	delta := after.HeapAlloc - before.HeapAlloc
	t.Logf("resident memory for %d records: %d bytes (%.2f B/record) against a %d-byte budget",
		benchRecordCount, delta, float64(delta)/benchRecordCount, budgetResidentBytes)
	require.LessOrEqual(t, delta, uint64(budgetResidentBytes),
		"%d records occupy %d bytes of live heap, over the §11.2 budget of %d",
		benchRecordCount, delta, budgetResidentBytes)
}

// ── the one unpinned package constant ─────────────────────────────────────────────────────────

// capProbeGraph builds the graph the cap probe walks: one tool-use node that consumed n distinct
// files. It is a separate helper rather than an inline loop because the assertion below is about
// what comes BACK from the walk, and burying that in setup is how a cap stops being visible.
func capProbeGraph(t *testing.T, root string, cfg config.Config, use core.ToolUseID, n int) dag.Graph {
	t.Helper()
	g := newDetGraph(t, root, cfg)
	id := dag.ToolUseNode(use)
	require.NoError(t, g.AddNode(dag.Node{ID: id, Kind: dag.KindToolUse, Turn: 1, Ref: "Bash"}))
	for i := range n {
		p := paths.Key(fmt.Sprintf("src/cap/f%02d.ts", i))
		require.NoError(t, g.AddNode(dag.Node{
			ID: dag.FileNode(p), Kind: dag.KindFile, Turn: 1, Ref: p, Root: benchHash(benchDepDomain, p),
		}))
		require.NoError(t, g.AddEdge(dag.Edge{From: id, To: dag.FileNode(p), Kind: dag.EdgeConsumes, Turn: 1}))
	}
	return g
}

// TestMaxAutoDepsCap pins maxAutoDeps, the cap on how many dependencies the one-hop DAG walk may
// contribute to an inferred elimination's staleness guard.
//
// The plan enumerates this package's package-level numeric constants and fixes this one at 8, and
// nothing else in the suite reaches it: the detector fixtures all sit well under the cap, so a
// change to it would go unnoticed. The assertion is behavioural rather than a comparison of the
// constant against itself — twelve files are put in front of the walk and exactly eight come back,
// in the sorted order the truncation slices off the end of.
func TestMaxAutoDepsCap(t *testing.T) {
	require.Equal(t, 8, maxAutoDeps, "the plan's constants table fixes the one-hop dep cap at 8")

	const candidates = 12
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), newFakeStore()))
	g := capProbeGraph(t, root, cfg, "cap-probe", candidates)

	d, ok := NewDetector(l, "s1", cfg, frozenClock{}).(*detector)
	require.True(t, ok)

	got := d.dagDeps(g, Observation{Kind: ObsTestFail, ToolUse: "cap-probe"})
	require.Len(t, got, maxAutoDeps,
		"the one-hop walk saw %d file neighbours and must contribute at most maxAutoDeps of them", candidates)
	for i := 1; i < len(got); i++ {
		require.Less(t, got[i-1].Path, got[i].Path, "the kept deps are the sorted prefix, not an arbitrary subset")
	}
	require.Equal(t, paths.Key("src/cap/f00.ts"), got[0].Path)
	require.Equal(t, paths.Key("src/cap/f07.ts"), got[len(got)-1].Path)
}

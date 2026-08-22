package obs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

func TestHistogram_MaxExact(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")

	h.Observe(3 * time.Millisecond)
	h.Observe(1234567 * time.Microsecond) // exactly 1.234567s
	h.Observe(500 * time.Microsecond)

	snap := h.Snapshot()
	require.Equal(t, 1234567*time.Microsecond, snap.Max)
	require.Equal(t, int64(3), snap.N)
}

func TestHistogram_PercentileConservative(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")

	const observation = 10 * time.Millisecond
	for i := 0; i < 10000; i++ {
		h.Observe(observation)
	}

	snap := h.Snapshot()
	require.Equal(t, int64(10000), snap.N)
	require.GreaterOrEqual(t, snap.P99, observation, "percentile must never under-report (conservative)")
	upperBound := time.Duration(float64(observation) * 1.0905)
	require.LessOrEqual(t, snap.P99, upperBound, "bucket over-reporting must stay within ~9.05%%")
}

func TestHistogram_EmptySnapshotIsZero(t *testing.T) {
	reg := obs.New(core.SystemClock())
	snap := reg.Hist("empty").Snapshot()
	require.Zero(t, snap.N)
	require.Zero(t, snap.P50)
	require.Zero(t, snap.P95)
	require.Zero(t, snap.P99)
	require.Zero(t, snap.P999)
	require.Zero(t, snap.Max)
}

func TestHistogram_Reset(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")
	h.Observe(5 * time.Second)
	require.NotZero(t, h.Snapshot().N)

	h.Reset()
	snap := h.Snapshot()
	require.Zero(t, snap.N)
	require.Zero(t, snap.Max)
}

func TestHistogram_NegativeDurationClampsToZero(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")
	h.Observe(-5 * time.Second)
	snap := h.Snapshot()
	require.Equal(t, int64(1), snap.N)
	require.Equal(t, time.Duration(0), snap.Max)
}

// TestHistogram_PercentilesOrdered asserts the percentiles a single Snapshot returns are
// non-decreasing in rank: P999's bucket can never resolve to an earlier bucket than P99's, and so
// on down to P50, because the rank thresholds used to find them (ceil(0.50*N) <= ceil(0.95*N) <=
// ...) are themselves non-decreasing and the cumulative bucket scan is monotone.
//
// Max is deliberately NOT asserted >= P999 here: Max is exact while every percentile is a
// conservative, rounded-UP bucket boundary (see hist.go's Snapshot doc comment), so a percentile
// can legitimately exceed the true maximum when the maximum falls early inside its bucket's range.
func TestHistogram_PercentilesOrdered(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")
	for i := 1; i <= 1000; i++ {
		h.Observe(time.Duration(i) * time.Microsecond)
	}
	snap := h.Snapshot()
	require.LessOrEqual(t, snap.P50, snap.P95)
	require.LessOrEqual(t, snap.P95, snap.P99)
	require.LessOrEqual(t, snap.P99, snap.P999)
}

func TestCounter_AddAndValue(t *testing.T) {
	reg := obs.New(core.SystemClock())
	c := reg.Counter("hits")
	c.Add(3)
	c.Add(4)
	require.Equal(t, int64(7), c.Value())

	// The same name must return the same underlying instrument.
	require.Equal(t, int64(7), reg.Counter("hits").Value())
}

func TestGauge_SetAndAdd(t *testing.T) {
	reg := obs.New(core.SystemClock())
	g := reg.Gauge("depth")
	g.Set(10)
	g.Add(-3)
	require.Equal(t, int64(7), g.Value())
	g.Set(2)
	require.Equal(t, int64(2), g.Value())
}

func TestRegistry_SnapshotIsDeepCopy(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	reg := obs.New(clock)
	reg.Hist("h").Observe(time.Millisecond)
	reg.Counter("c").Add(1)
	reg.Gauge("g").Set(1)

	snap := reg.Snapshot()
	snap.Hists["h"] = obs.HistSnapshot{N: 999}
	snap.Counters["c"] = 999
	snap.Gauges["g"] = 999

	fresh := reg.Snapshot()
	require.Equal(t, int64(1), fresh.Hists["h"].N)
	require.Equal(t, int64(1), fresh.Counters["c"])
	require.Equal(t, int64(1), fresh.Gauges["g"])
}

func TestRegistry_SnapshotTimestamp(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	reg := obs.New(clock)
	snap := reg.Snapshot()
	require.Equal(t, core.NowMilli(clock), snap.TS)
}

// TestBudgets_AllSixPresentAndConfigDriven grades 00-ARCHITECTURE.md §2.4's own six budgets,
// B-A..B-F: every one present, gated as §2.4 says, and reading its limit from configuration. The
// name is §2.4's count and stays that way even though Budgets() now returns seven — B-G is not a
// §2.4 budget and has its own test below — because plans/ cite this test by name and
// `devtool lint`'s planchecks fails a plan row whose -run pattern matches nothing.
func TestBudgets_AllSixPresentAndConfigDriven(t *testing.T) {
	budgets := obs.Budgets()
	// §2.4's six, plus B-G. Still a closed-world count: a seventh §2.4-shaped budget appearing
	// without a test of its own fails here.
	require.Len(t, budgets, 7)

	ids := make(map[obs.BudgetID]obs.Budget, len(budgets))
	for _, b := range budgets {
		ids[b.ID] = b
	}
	for _, id := range []obs.BudgetID{obs.BA, obs.BB, obs.BC, obs.BD, obs.BE, obs.BF} {
		_, ok := ids[id]
		require.True(t, ok, "missing budget %s", id)
	}

	require.True(t, ids[obs.BA].Gated)
	require.True(t, ids[obs.BB].Gated)
	require.False(t, ids[obs.BC].Gated, "B-C is soft per §2.4")
	require.False(t, ids[obs.BD].Gated, "B-D is reported only per §2.4")
	require.True(t, ids[obs.BE].Gated)
	require.True(t, ids[obs.BF].Gated)

	cfg := config.Defaults()
	require.Equal(t, 15*time.Millisecond, ids[obs.BA].Limit(cfg), "B-A reads runtime.hotPath.budgetMs")
	require.Equal(t, time.Duration(0), ids[obs.BD].Limit(cfg), "B-D always reports 0, never a config key")

	// Changing configuration must change every config-driven limit (all but B-D).
	mutated := config.Defaults()
	mutated.Runtime.HotPath.BudgetMs = 999
	mutated.Runtime.Budgets.L0IngestMs = 999
	mutated.Runtime.Budgets.L0ProcessMs = 999
	mutated.Runtime.Budgets.CheckpointFinalizeMs = 999
	mutated.Runtime.Budgets.MCPToolCallMs = 999

	for _, id := range []obs.BudgetID{obs.BA, obs.BB, obs.BC, obs.BE, obs.BF} {
		before := ids[id].Limit(cfg)
		after := ids[id].Limit(mutated)
		require.NotEqual(t, before, after, "budget %s must be config-driven", id)
		require.Equal(t, 999*time.Millisecond, after)
	}
	// B-D is the sole, documented exception.
	require.Equal(t, ids[obs.BD].Limit(cfg), ids[obs.BD].Limit(mutated))
}

// TestBudgets_BGCoversTheDegradedSpoolAppend pins B-G: the budget for the synchronous spool append
// a hook pays inside ipc.Client.Send when the daemon cannot take the event. Before it, that path
// had no budget at all — B-A's gated population is the daemon's own hook_controlled series, which
// has no sample for a request that never reached the daemon, and bc44d2a deliberately subtracted
// the append from the one test that incidentally bounded it (correctly: that assertion was
// measuring disk speed).
//
// B-G is REPORTED ONLY, and that is asserted here rather than left to be inferred. The reason is
// structural, not soft: CheckBudgets' only production caller is the resident daemon's Registry,
// hook_degraded is written only by a hook process's own Registry, and a B-G sample exists only
// when the daemon is unreachable — a sample and an evaluator can never coexist. What enforces the
// budget today is internal/ipc's rate-graded gate.
//
// Its limit reads a key of its own, runtime.budgets.hookDegradedMs, and this test pins that it is
// NOT derived from any other budget's key: a hot-path budget an operator tightens to test their
// own setup must not drag a filesystem bound down with it.
func TestBudgets_BGCoversTheDegradedSpoolAppend(t *testing.T) {
	bg := budgetByID(t, obs.BG)

	require.Equal(t, obs.BudgetID("B-G"), bg.ID)
	require.Equal(t, "hook_degraded", bg.Hist, "B-G's clock is the degraded path's own histogram")
	require.Equal(t, 99, bg.Pct, "B-G is stated at p99, like B-A")
	require.False(t, bg.Gated,
		"B-G is reported only: no production evaluator can ever see a hook_degraded sample")

	// Generous in the direction that matters: B-G's limit must be far above B-A's, because the
	// degraded path substitutes a filesystem create-and-append for a daemon round trip and can
	// never meet the budget for one.
	cfg := config.Defaults()
	ba := 15 * time.Millisecond
	require.Equal(t, ba, budgetByID(t, obs.BA).Limit(cfg), "premise: B-A's default is 15 ms")
	require.Greater(t, bg.Limit(cfg), ba, "B-G must be looser than B-A, not tighter")
	require.Equal(t, time.Second, bg.Limit(cfg), "B-G's default is runtime.budgets.hookDegradedMs")

	// Config-driven through its own key.
	own := config.Defaults()
	own.Runtime.Budgets.HookDegradedMs = 333
	require.Equal(t, 333*time.Millisecond, bg.Limit(own),
		"B-G must read runtime.budgets.hookDegradedMs, never a literal of its own")

	// And decoupled from every other budget's key. Riding runtime.hotPath.budgetMs was the first
	// shape this took and it was wrong: at budgetMs=1 — which test/guards' own end-to-end config
	// test sets — a 64x multiple would put the degraded ceiling at 64 ms, below figures this
	// append has already been measured at on a loaded runner.
	elsewhere := config.Defaults()
	elsewhere.Runtime.HotPath.BudgetMs = 1
	elsewhere.Runtime.Budgets.L0IngestMs = 999
	elsewhere.Runtime.Budgets.L0ProcessMs = 999
	elsewhere.Runtime.Budgets.CheckpointFinalizeMs = 999
	elsewhere.Runtime.Budgets.MCPToolCallMs = 999
	require.Equal(t, bg.Limit(cfg), bg.Limit(elsewhere),
		"no other budget's key may move B-G's limit")
}

// TestCheckBudgets_NeverReportsBG pins the reported-only half at the evaluator, not just in the
// table: however far over its limit the degraded path's histogram runs, CheckBudgets must not
// return it. A future wave that wires a production evaluator has to change this test deliberately.
func TestCheckBudgets_NeverReportsBG(t *testing.T) {
	cfg := config.Defaults()
	bg := budgetByID(t, obs.BG)

	reg := obs.New(core.SystemClock())
	for i := 0; i < 8; i++ {
		reg.Hist(bg.Hist).Observe(time.Hour)
	}
	require.EqualValues(t, 8, reg.Snapshot().Hists[bg.Hist].N, "premise: the samples were recorded")

	for i := 1; i <= 3; i++ {
		for _, b := range reg.CheckBudgets(cfg) {
			require.NotEqual(t, string(obs.BG), b.Budget,
				"call %d: B-G is reported only and must never surface as a breach", i)
		}
	}
}

// budgetByID returns the Budget obs.Budgets() declares for id, failing the test if there is none.
func budgetByID(t *testing.T, id obs.BudgetID) obs.Budget {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("obs.Budgets() does not declare %s", id)
	return obs.Budget{}
}

func TestCheckBudgets_CountsConsecutiveWindows(t *testing.T) {
	reg := obs.New(core.SystemClock())
	cfg := config.Defaults() // B-A budget: 15ms p99

	// Push B-A's histogram over budget: every observation well above 15ms.
	over := func() {
		reg.Hist("hook_controlled").Observe(50 * time.Millisecond)
	}

	over()
	first := findBreach(t, reg.CheckBudgets(cfg), obs.BA)
	require.Equal(t, 1, first.Windows)

	over()
	second := findBreach(t, reg.CheckBudgets(cfg), obs.BA)
	require.Equal(t, 2, second.Windows)

	over()
	third := findBreach(t, reg.CheckBudgets(cfg), obs.BA)
	require.Equal(t, 3, third.Windows)
	require.Equal(t, "B-A", third.Budget)
	require.Greater(t, third.Observed, third.Limit)
}

func TestCheckBudgets_ResetsStreakWhenBackUnderBudget(t *testing.T) {
	reg := obs.New(core.SystemClock())
	cfg := config.Defaults()

	reg.Hist("hook_controlled").Observe(50 * time.Millisecond)
	breaches := reg.CheckBudgets(cfg)
	require.Len(t, breaches, 1)

	// Reset and observe a comfortably-under-budget value: the streak must drop back to zero and
	// B-A must no longer appear in the breach list.
	reg.Hist("hook_controlled").Reset()
	reg.Hist("hook_controlled").Observe(time.Millisecond)
	breaches = reg.CheckBudgets(cfg)
	for _, b := range breaches {
		require.NotEqual(t, "B-A", b.Budget)
	}
}

func TestCheckBudgets_NeverReportsUngatedBudgets(t *testing.T) {
	reg := obs.New(core.SystemClock())
	cfg := config.Defaults()
	// B-C (l0_process) and B-D (hook_wall) are never gated, however far over any plausible
	// threshold their histograms run.
	reg.Hist("l0_process").Observe(time.Hour)
	reg.Hist("hook_wall").Observe(time.Hour)

	for _, b := range reg.CheckBudgets(cfg) {
		require.NotEqual(t, "B-C", b.Budget)
		require.NotEqual(t, "B-D", b.Budget)
	}
}

func TestRegistry_Persist(t *testing.T) {
	root := t.TempDir()
	layout := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(layout))

	reg := obs.New(core.SystemClock())
	reg.Hist("hook_controlled").Observe(2 * time.Millisecond)
	reg.Counter("loud.total").Add(1)

	require.NoError(t, reg.Persist(layout))

	b, err := os.ReadFile(filepath.Join(layout.Metrics, "latency.json"))
	require.NoError(t, err)

	var snap obs.Snapshot
	require.NoError(t, json.Unmarshal(b, &snap))
	require.Equal(t, int64(1), snap.Hists["hook_controlled"].N)
	require.Equal(t, int64(1), snap.Counters["loud.total"])
}

func TestTimed_RecordsDurationRegardlessOfError(t *testing.T) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("x")

	err := obs.Timed(h, func() error { return errBoom{} })
	require.Error(t, err)
	require.Equal(t, int64(1), h.Snapshot().N)

	require.NoError(t, obs.Timed(h, func() error { return nil }))
	require.Equal(t, int64(2), h.Snapshot().N)
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func findBreach(t *testing.T, breaches []obs.BudgetBreach, id obs.BudgetID) obs.BudgetBreach {
	t.Helper()
	for _, b := range breaches {
		if b.Budget == string(id) {
			return b
		}
	}
	t.Fatalf("no breach found for %s among %d breach(es)", id, len(breaches))
	return obs.BudgetBreach{}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time                  { return c.now }
func (c *fakeClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }

func BenchmarkHistogram_Observe(b *testing.B) {
	reg := obs.New(core.SystemClock())
	h := reg.Hist("bench")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Observe(time.Duration(i%1000) * time.Microsecond)
	}
}

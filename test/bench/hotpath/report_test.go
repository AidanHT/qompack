package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/obs"
)

// fixture512 is a hand-checkable 512-sample fixture: the durations 1ms..512ms, deliberately fed
// in DESCENDING order so percentiles's own sort is what makes the nearest-rank math below correct
// rather than an accident of already-sorted input.
//
// Nearest-rank method (matching internal/obs/hist.go's own Snapshot): rank = ceil(p*n), clamped to
// [1,n]; the reported value is the 1-indexed rank-th smallest sample.
//
//	p50:  rank = ceil(0.50*512) = 256  -> the 256th smallest -> 256ms
//	p95:  rank = ceil(0.95*512) = 487  -> 487ms   (0.95*512 = 486.4)
//	p99:  rank = ceil(0.99*512) = 507  -> 507ms   (0.99*512 = 506.88)
//	p999: rank = ceil(0.999*512) = 512 -> 512ms   (0.999*512 = 511.488)
//	max:  512ms
func fixture512() []time.Duration {
	out := make([]time.Duration, 512)
	for i := range out {
		// descending: out[0] = 512ms, out[511] = 1ms
		out[i] = time.Duration(512-i) * time.Millisecond
	}
	return out
}

func TestPercentiles_HandCheckedFixture(t *testing.T) {
	samples := fixture512()
	p50, p95, p99, p999, max := percentiles(samples)

	require.Equal(t, 256*time.Millisecond, p50)
	require.Equal(t, 487*time.Millisecond, p95)
	require.Equal(t, 507*time.Millisecond, p99)
	require.Equal(t, 512*time.Millisecond, p999)
	require.Equal(t, 512*time.Millisecond, max)
}

func TestPercentile_RankOneForSmallSamples(t *testing.T) {
	// n=3: p50 -> rank=ceil(1.5)=2 -> sorted[1]
	sorted := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	require.Equal(t, 2*time.Millisecond, percentile(sorted, 0.50))
	// p99 -> rank=ceil(0.99*3)=3 -> sorted[2]
	require.Equal(t, 3*time.Millisecond, percentile(sorted, 0.99))
}

func TestPercentile_EmptyIsZero(t *testing.T) {
	require.Equal(t, time.Duration(0), percentile(nil, 0.99))
}

// TestSubtractFloor pins task-7-spec.md step 6's per-sample derivation: B-A_i = max(0, B-D_i -
// floor_p50), never percentile-from-percentile.
func TestSubtractFloor(t *testing.T) {
	bd := []time.Duration{10 * time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond}
	floorP50 := 8 * time.Millisecond

	got := subtractFloor(bd, floorP50)
	want := []time.Duration{2 * time.Millisecond, 0, 12 * time.Millisecond}
	require.Equal(t, want, got)
}

func TestSubtractFloor_NeverNegative(t *testing.T) {
	bd := []time.Duration{1 * time.Millisecond}
	floorP50 := 100 * time.Millisecond
	got := subtractFloor(bd, floorP50)
	require.Equal(t, []time.Duration{0}, got)
}

// TestBuildBudgetRow_GatedPassAndFail pins buildBudgetRow's gating: pass is p99 < limit, and an
// ungated row (B-D) carries a nil limit_ms/pass, matching task-7-spec.md's out.json example.
func TestBuildBudgetRow_GatedPassAndFail(t *testing.T) {
	samples := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}

	pass := buildBudgetRow("B-A", samples, 15*time.Millisecond, true)
	require.NotNil(t, pass.Pass)
	require.True(t, *pass.Pass)
	require.NotNil(t, pass.LimitMs)
	require.InDelta(t, 15.0, *pass.LimitMs, 0.001)

	fail := buildBudgetRow("B-A", samples, 1*time.Microsecond, true)
	require.NotNil(t, fail.Pass)
	require.False(t, *fail.Pass)

	reported := buildBudgetRow("B-D", samples, 0, false)
	require.Nil(t, reported.Pass)
	require.Nil(t, reported.LimitMs)
}

// wantGoldenJSON is the out.json shape's golden. FIX ROUND 1 / controller ruling #29 amends
// task-7-spec.md's own example: the gated "B-A" row is now sourced from the daemon's own
// hook_controlled histogram (status op), never a wall-clock spawn estimate, and that wall-clock,
// floor-subtracted number survives as a fifth, renamed, ALWAYS-ungated diagnostic row
// ("B-A_spawn_estimate") instead of gating anything itself. b_a_method's own text is injected
// from the bAMethod constant (report.go) so this fixture can never silently drift from what the
// harness actually emits. It exercises the shape — every key, including b_a_method,
// spawn_floor_ms, notes, and all five budget rows' limit_ms/pass null-vs-set shape — rather than
// any particular timing, per the commit-7 checklist's own instruction.
var wantGoldenJSON = fmt.Sprintf(`{
  "platform": "windows/amd64",
  "n": 2000,
  "b_a_method": %q,
  "spawn_floor_ms": {"n": 200, "p50": 6.1, "p99": 11.4},
  "notes": ["B-C not measured in wave 1: the processing seams are stubs"],
  "budgets": [
    {"budget_id": "B-A", "n": 2000, "p50": 0.4, "p95": 1.2, "p99": 2.0, "p999": 3.1, "max": 4.5, "limit_ms": 15, "pass": true},
    {"budget_id": "B-B", "n": 2000, "p50": 0.10, "p95": 0.31, "p99": 0.62, "p999": 1.1, "max": 1.9, "limit_ms": 2, "pass": true},
    {"budget_id": "B-D", "n": 2000, "p50": 8.2, "p95": 13.9, "p99": 18.1, "p999": 24.0, "max": 31.5, "limit_ms": null, "pass": null},
    {"budget_id": "B-E", "n": 50, "p50": 14.0, "p95": 22.5, "p99": 31.0, "p999": 31.0, "max": 31.0, "limit_ms": 2000, "pass": true},
    {"budget_id": "B-A_spawn_estimate", "n": 2000, "p50": 2.1, "p95": 60.4, "p99": 78.0, "p999": 90.0, "max": 115.2, "limit_ms": null, "pass": null}
  ]
}`, bAMethod)

func floatPtr(f float64) *float64 { return &f }
func boolPtr(b bool) *bool        { return &b }

func TestReport_MatchesGoldenShape(t *testing.T) {
	r := Report{
		Platform: "windows/amd64",
		N:        2000,
		BAMethod: bAMethod,
		SpawnFloorMs: SpawnFloor{
			N: 200, P50: 6.1, P99: 11.4,
		},
		Notes: []string{"B-C not measured in wave 1: the processing seams are stubs"},
		Budgets: []BudgetRow{
			{BudgetID: "B-A", N: 2000, P50: 0.4, P95: 1.2, P99: 2.0, P999: 3.1, Max: 4.5, LimitMs: floatPtr(15), Pass: boolPtr(true)},
			{BudgetID: "B-B", N: 2000, P50: 0.10, P95: 0.31, P99: 0.62, P999: 1.1, Max: 1.9, LimitMs: floatPtr(2), Pass: boolPtr(true)},
			{BudgetID: "B-D", N: 2000, P50: 8.2, P95: 13.9, P99: 18.1, P999: 24.0, Max: 31.5, LimitMs: nil, Pass: nil},
			{BudgetID: "B-E", N: 50, P50: 14.0, P95: 22.5, P99: 31.0, P999: 31.0, Max: 31.0, LimitMs: floatPtr(2000), Pass: boolPtr(true)},
			{BudgetID: budgetIDBASpawnEstimate, N: 2000, P50: 2.1, P95: 60.4, P99: 78.0, P999: 90.0, Max: 115.2, LimitMs: nil, Pass: nil},
		},
	}

	got, err := json.Marshal(r)
	require.NoError(t, err)
	require.JSONEq(t, wantGoldenJSON, string(got))
}

// TestBudgetIDBASpawnEstimate_NeverCollidesWithB_A pins the FIX ROUND 1 rename (I-1 / controller
// ruling #29: "rename their keys so nothing reads them as B-A") at the constant level, not just in
// the golden fixture above.
func TestBudgetIDBASpawnEstimate_NeverCollidesWithB_A(t *testing.T) {
	require.NotEqual(t, string(obs.BA), budgetIDBASpawnEstimate)
	require.Equal(t, "B-A_spawn_estimate", budgetIDBASpawnEstimate)
}

// TestBuildBudgetRowFromSnapshot pins B-B's own construction path: unlike buildBudgetRow, there
// are no raw samples — the daemon's own obs.HistSnapshot IS the source of truth, reported
// verbatim.
func TestBuildBudgetRowFromSnapshot(t *testing.T) {
	snap := obs.HistSnapshot{
		N:   2000,
		P50: 100 * time.Microsecond, P95: 310 * time.Microsecond, P99: 620 * time.Microsecond,
		P999: 1100 * time.Microsecond, Max: 1900 * time.Microsecond,
	}
	row := buildBudgetRowFromSnapshot("B-B", snap, 2*time.Millisecond, true)
	require.Equal(t, 2000, row.N)
	require.InDelta(t, 0.62, row.P99, 0.001)
	require.NotNil(t, row.Pass)
	require.True(t, *row.Pass)
}

// TestBudgetLimit_ReadsFromConfigDefaults pins that the harness never re-hardcodes a budget limit
// (task-7-brief.md's binding ruling): B-A/B-B/B-E must come from config.Defaults() +
// obs.Budgets(), which SP-01 already pins at 15ms/2ms/2000ms.
func TestBudgetLimit_ReadsFromConfigDefaults(t *testing.T) {
	cfg := config.Defaults()
	require.Equal(t, 15*time.Millisecond, budgetLimit(cfg, obs.BA))
	require.Equal(t, 2*time.Millisecond, budgetLimit(cfg, obs.BB))
	require.Equal(t, 2000*time.Millisecond, budgetLimit(cfg, obs.BE))
}

func TestBudgetHistName_MatchesBudgetsTable(t *testing.T) {
	require.Equal(t, "l0_ingest", budgetHistName(obs.BB))
}

// TestCheckDeliveryIntegrity pins FIX ROUND 1's I-2 guard: gotN must equal iterations (+
// warmHotTranche when warm-up ran — FIX ROUND 2, N-1: only the warm-up's small hot-path tranche
// touches l0_ingest; the rest is admin.ping traffic), exactly — no tolerance either direction.
func TestCheckDeliveryIntegrity(t *testing.T) {
	require.NoError(t, checkDeliveryIntegrity(2000, 2000, false), "no warm-up: exact match passes")
	require.NoError(t, checkDeliveryIntegrity(int64(warmHotTranche+2000), 2000, true), "warm-up: exact match passes")

	require.Error(t, checkDeliveryIntegrity(1999, 2000, false), "one short must fail loudly")
	require.Error(t, checkDeliveryIntegrity(2001, 2000, false), "one over must also fail — not just a floor check")
	require.Error(t, checkDeliveryIntegrity(2000, 2000, true), "warm-up ran but l0_ingest only saw the loop's own count")
}

// TestReport_ExitNonZeroOnAnyGatedFailure pins GateFailed's decision rule: any gated (non-nil
// Pass) budget reporting false fails the run; B-D's nil Pass never can.
func TestReport_ExitNonZeroOnAnyGatedFailure(t *testing.T) {
	okReport := Report{Budgets: []BudgetRow{
		{BudgetID: "B-A", Pass: boolPtr(true)},
		{BudgetID: "B-D", Pass: nil},
	}}
	require.False(t, okReport.GateFailed())

	badReport := Report{Budgets: []BudgetRow{
		{BudgetID: "B-A", Pass: boolPtr(true)},
		{BudgetID: "B-E", Pass: boolPtr(false)},
	}}
	require.True(t, badReport.GateFailed())
}

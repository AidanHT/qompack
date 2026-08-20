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
	row, note := buildBudgetRowFromSnapshot("B-B", snap, 2*time.Millisecond, true, 0)
	require.Equal(t, 2000, row.N)
	require.InDelta(t, 0.62, row.P99, 0.001)
	require.NotNil(t, row.Pass)
	require.True(t, *row.Pass)
	require.Empty(t, note, "a run with nothing missing must add no disclosure note")
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

// TestExpectedHotPathSends pins the population FIX ROUND 1's I-2 guard has always counted
// against: iterations (+ warmHotTranche when warm-up ran — FIX ROUND 2, N-1: only the warm-up's
// small hot-path tranche touches l0_ingest; the rest is admin.ping traffic).
func TestExpectedHotPathSends(t *testing.T) {
	require.Equal(t, int64(2000), expectedHotPathSends(2000, false))
	require.Equal(t, int64(warmHotTranche+2000), expectedHotPathSends(2000, true))
}

// TestReconcileDelivery_KeepsEveryFailureTheOldGuardHad is the direct translation of the old
// checkDeliveryIntegrity test: every case it failed on with no spool evidence still fails, in
// both directions. Nothing about the guard is relaxed by knowing about deferral — a shortfall
// with nothing on disk to account for it is still a hard failure.
func TestReconcileDelivery_KeepsEveryFailureTheOldGuardHad(t *testing.T) {
	noSpool := spoolCensus{}

	_, err := reconcileDelivery(expectedHotPathSends(2000, false), 2000, noSpool)
	require.NoError(t, err, "no warm-up: exact match passes")

	_, err = reconcileDelivery(expectedHotPathSends(2000, true), warmHotTranche+2000, noSpool)
	require.NoError(t, err, "warm-up: exact match passes")

	_, err = reconcileDelivery(expectedHotPathSends(2000, false), 1999, noSpool)
	require.Error(t, err, "one short with NOTHING in the spool to account for it must still fail loudly")
	require.Contains(t, err.Error(), "LOST")

	_, err = reconcileDelivery(expectedHotPathSends(2000, false), 2001, noSpool)
	require.Error(t, err, "one over must also fail — not just a floor check")

	_, err = reconcileDelivery(expectedHotPathSends(2000, true), 2000, noSpool)
	require.Error(t, err, "warm-up ran but l0_ingest only saw the loop's own count, and nothing is spooled")
}

// TestReconcileDelivery_DeferredIsNotLost is the regression for run 32296920486's macos-latest
// failure: 2063 of 2064 requests reached the daemon and the 2064th is sitting in the client spool
// — §8.1/§12.2's documented degrade-rather-than-block path, not a defect. It must reconcile, and
// the deferral must be VISIBLE in the ledger so the percentile accounting can use it.
func TestReconcileDelivery_DeferredIsNotLost(t *testing.T) {
	sent := expectedHotPathSends(2000, true) // 2064
	ledger, err := reconcileDelivery(sent, 2063, spoolCensus{Deferred: 1, Files: 1})
	require.NoError(t, err, "a shortfall fully accounted for by a spooled request is a deferral, not a loss")
	require.Equal(t, deliveryLedger{Sent: 2064, Delivered: 2063, Deferred: 1, Lost: 0}, ledger)
	require.Equal(t, int64(1), ledger.Undelivered())
}

// TestReconcileDelivery_PartialEvidenceStillFails pins the discriminating half: spool evidence
// covers only what it covers. Three missing with one spooled line is still two events LOST.
func TestReconcileDelivery_PartialEvidenceStillFails(t *testing.T) {
	_, err := reconcileDelivery(2064, 2061, spoolCensus{Deferred: 1, Files: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "2 are LOST")
}

// TestReconcileDelivery_UnreadableSpoolLineRefusesToClassify pins that a line the harness cannot
// decode is never assumed to be a deferral in this run's favour.
func TestReconcileDelivery_UnreadableSpoolLineRefusesToClassify(t *testing.T) {
	_, err := reconcileDelivery(2064, 2063, spoolCensus{Deferred: 1, Unreadable: 1, Files: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "would not decode")
}

// TestReconcileDelivery_SpooledDuplicateIsNotAMissingSample pins the lost-ACK case: the daemon
// accepted the line AND the client spooled it (internal/ipc/client.go's awaitACK spools on a read
// timeout), so the spool holds more lines than there are missing samples. Nothing is missing from
// the population, so nothing is counted as deferred.
func TestReconcileDelivery_SpooledDuplicateIsNotAMissingSample(t *testing.T) {
	ledger, err := reconcileDelivery(2064, 2064, spoolCensus{Deferred: 1, Files: 1})
	require.NoError(t, err)
	require.Zero(t, ledger.Deferred, "a duplicate of a DELIVERED request is not a missing sample")
	require.Zero(t, ledger.Undelivered())
}

// TestHookControlledShortfall pins B-A's own population check: hook_controlled can legitimately
// be shorter than l0_ingest by exactly the daemon's hotpath_sample_invalid count (a received
// request whose wire timestamp validHotPathTS refused to time), and by nothing else.
func TestHookControlledShortfall(t *testing.T) {
	ledger := deliveryLedger{Sent: 2064, Delivered: 2063, Deferred: 1}

	missing, err := hookControlledShortfall(ledger, 2063, nil)
	require.NoError(t, err, "the deferral alone explains a shortfall of one")
	require.Equal(t, int64(1), missing)

	missing, err = hookControlledShortfall(ledger, 2061,
		map[string]int64{counterHotpathSampleInvalidName: 2})
	require.NoError(t, err, "deferral + two untimeable samples explain a shortfall of three")
	require.Equal(t, int64(3), missing)

	_, err = hookControlledShortfall(ledger, 2060,
		map[string]int64{counterHotpathSampleInvalidName: 2})
	require.Error(t, err, "one sample vanished from the GATED population with no accounting")
	require.Contains(t, err.Error(), "1 samples went missing")

	_, err = hookControlledShortfall(ledger, 2065, nil)
	require.Error(t, err, "more B-A samples than requests sent means the population is not this run's")
}

// TestTailAdjustedP99_MissingSamplesAreCountedAsOverBudget is the statistical half of the fix.
// The samples missing from a daemon-side histogram are the SLOW ones — a request only reaches the
// spool after Send has burnt its whole connect/ACK budget — so dropping them truncates the upper
// tail and the survivors' p99 UNDERSTATES the real one. They are counted back in at the top, and
// the nearest-rank p99 is re-derived over the full population.
func TestTailAdjustedP99_MissingSamplesAreCountedAsOverBudget(t *testing.T) {
	// A hand-checkable snapshot: n=2063 delivered, one deferred.
	//   full population = 2064; rank = ceil(0.99*2064) = 2044
	//   delivered p99 covers only rank ceil(0.99*2063) = 2043 -> too low
	//   delivered p999 covers rank ceil(0.999*2063) = 2061 -> 2044 <= 2061, so p999 bounds it
	snap := obs.HistSnapshot{
		N:   2063,
		P50: 1 * time.Millisecond, P95: 2 * time.Millisecond, P99: 3 * time.Millisecond,
		P999: 7 * time.Millisecond, Max: 40 * time.Millisecond,
	}

	value, basis, ok := tailAdjustedP99(snap, 1)
	require.True(t, ok)
	require.Equal(t, basisDeliveredP999, basis)
	require.Equal(t, 7*time.Millisecond, value,
		"the survivors' own p99 (3ms) sits below the rank the full population's p99 needs; the p999 is the tightest sound bound this snapshot can give")

	// Nothing missing: the daemon's own p99 is exact and nothing is adjusted.
	value, basis, ok = tailAdjustedP99(snap, 0)
	require.True(t, ok)
	require.Equal(t, basisDeliveredP99, basis)
	require.Equal(t, 3*time.Millisecond, value)
}

// TestTailAdjustedP99_CeilingCanAbsorbTheShift pins the third outcome: for some population sizes
// the nearest-rank ceiling already covers the shift, so a shortfall changes nothing and the
// delivered p99 stays exact. n=99 is such a size — ceil(0.99*99) = 99 and ceil(0.99*100) = 99 —
// and the disclosure note must say the rank was absorbed rather than claim a bound was used.
func TestTailAdjustedP99_CeilingCanAbsorbTheShift(t *testing.T) {
	snap := obs.HistSnapshot{N: 99, P99: 3 * time.Millisecond, P999: 7 * time.Millisecond, Max: 40 * time.Millisecond}

	value, basis, ok := tailAdjustedP99(snap, 1)
	require.True(t, ok)
	require.Equal(t, basisDeliveredP99, basis)
	require.Equal(t, 3*time.Millisecond, value)

	row, note := buildBudgetRowFromSnapshot("B-A", snap, 15*time.Millisecond, true, 1)
	require.NotNil(t, row.Pass)
	require.True(t, *row.Pass)
	require.Contains(t, note, "needed no adjustment")
	require.NotContains(t, note, "sound upper bound",
		"an absorbed shift must not claim a bound was substituted for the measurement")
}

// TestTailAdjustedP99_LargeShortfallCannotBeCertified pins the hard end: once the missing block
// is big enough to contain the p99 rank itself, there is no honest number to report and the gate
// must not pass.
func TestTailAdjustedP99_LargeShortfallCannotBeCertified(t *testing.T) {
	// 100 delivered, 5 missing: full population 105, rank = ceil(0.99*105) = 104 > 100.
	snap := obs.HistSnapshot{N: 100, P99: time.Millisecond, P999: 2 * time.Millisecond, Max: 3 * time.Millisecond}
	_, basis, ok := tailAdjustedP99(snap, 5)
	require.False(t, ok, "the p99 of the full population is one of the missing over-budget samples")
	require.Equal(t, basisUncertifiable, basis)

	// The boundary just below it: 100 delivered, 1 missing -> rank = ceil(0.99*101) = 100, which
	// is past the delivered p99's rank (99) but still inside ceil(0.999*100) = 100, so the p999
	// bounds it.
	value, basis, ok := tailAdjustedP99(snap, 1)
	require.True(t, ok)
	require.Equal(t, basisDeliveredP999, basis)
	require.Equal(t, 2*time.Millisecond, value)

	// And the rung above p999: 2000 delivered, 19 missing -> rank = ceil(0.99*2019) = 1999, past
	// ceil(0.999*2000) = 1998 but still inside the delivered set, so only the exact max bounds it.
	big := obs.HistSnapshot{N: 2000, P99: time.Millisecond, P999: 2 * time.Millisecond, Max: 3 * time.Millisecond}
	value, basis, ok = tailAdjustedP99(big, 19)
	require.True(t, ok)
	require.Equal(t, basisDeliveredMax, basis)
	require.Equal(t, 3*time.Millisecond, value)
}

// TestTailAdjustedP99_NeverReportsBelowTheDeliveredP99 is the property that makes this a
// strengthening rather than a re-tuning: for any shortfall, over a wide sweep of population
// sizes, the gated number is never SMALLER than what the harness reported before — a run can
// only get harder to pass, never easier.
func TestTailAdjustedP99_NeverReportsBelowTheDeliveredP99(t *testing.T) {
	snap := obs.HistSnapshot{
		N: 0, P50: time.Millisecond, P95: 2 * time.Millisecond, P99: 3 * time.Millisecond,
		P999: 7 * time.Millisecond, Max: 40 * time.Millisecond,
	}
	for _, n := range []int64{1, 2, 7, 50, 100, 512, 999, 2000, 2063, 2064, 5000} {
		for _, missing := range []int64{0, 1, 2, 3, 10, 64} {
			s := snap
			s.N = n
			value, _, ok := tailAdjustedP99(s, missing)
			if !ok {
				continue // an uncertifiable run fails the gate outright; there is no value to compare
			}
			require.GreaterOrEqualf(t, value, s.P99,
				"n=%d missing=%d: the tail-adjusted p99 must never fall below the delivered p99", n, missing)
		}
	}
}

// TestBuildBudgetRowFromSnapshot_ShortfallFailsTheGateItWouldOtherwisePass is the end-to-end shape
// of the two halves together: a delivered p99 comfortably inside the limit, one deferred request,
// and a p999 that is NOT inside the limit. Before the accounting this passed on the survivors'
// truncated tail; now the row reports the bound and fails.
func TestBuildBudgetRowFromSnapshot_ShortfallFailsTheGateItWouldOtherwisePass(t *testing.T) {
	snap := obs.HistSnapshot{
		N: 2063, P50: time.Millisecond, P95: 2 * time.Millisecond, P99: 3 * time.Millisecond,
		P999: 22 * time.Millisecond, Max: 40 * time.Millisecond,
	}
	limit := 15 * time.Millisecond

	clean, note := buildBudgetRowFromSnapshot("B-A", snap, limit, true, 0)
	require.NotNil(t, clean.Pass)
	require.True(t, *clean.Pass, "with nothing missing the delivered p99 (3ms) passes a 15ms gate")
	require.Empty(t, note)

	short, note := buildBudgetRowFromSnapshot("B-A", snap, limit, true, 1)
	require.NotNil(t, short.Pass)
	require.False(t, *short.Pass,
		"one deferred request moves the p99 rank past what the delivered p99 covers; the sound bound (p999=22ms) breaches the 15ms limit")
	require.InDelta(t, 22.0, short.P99, 0.001, "the gated field must carry the bound the gate was decided on")
	require.Equal(t, 2063, short.N, "n stays the delivered sample count the other percentiles are computed over")
	require.Contains(t, note, "counted back in as over-budget samples")
	require.Contains(t, note, "rank 2043 to rank 2044")
}

// TestBuildBudgetRowFromSnapshot_UncertifiableRowFailsAndSaysSo pins the row shape when the
// shortfall swallows the p99 rank itself.
func TestBuildBudgetRowFromSnapshot_UncertifiableRowFailsAndSaysSo(t *testing.T) {
	snap := obs.HistSnapshot{N: 100, P99: time.Millisecond, P999: 2 * time.Millisecond, Max: 3 * time.Millisecond}
	row, note := buildBudgetRowFromSnapshot("B-A", snap, 15*time.Millisecond, true, 5)
	require.NotNil(t, row.Pass)
	require.False(t, *row.Pass)
	require.Contains(t, note, "CANNOT be certified")
	require.InDelta(t, 1.0, row.P99, 0.001, "the delivered p99 is still reported, for diagnosis, alongside the note that it is not the gated number")
}

// TestBudgetsGatedFromSnapshotsAreP99Budgets pins the premise tailAdjustedP99's percentile ladder
// rests on: both budgets this harness gates off a daemon-side histogram snapshot are p99 budgets.
// A future budget table that gated one of them on p95 would need a different ladder, and this
// fails rather than letting the wrong one be applied silently.
func TestBudgetsGatedFromSnapshotsAreP99Budgets(t *testing.T) {
	for _, id := range []obs.BudgetID{obs.BA, obs.BB} {
		var found bool
		for _, b := range obs.Budgets() {
			if b.ID != id {
				continue
			}
			found = true
			require.True(t, b.Gated, "%s must be a gated budget", id)
			require.Equal(t, 99, b.Pct, "%s is gated on p99; tailAdjustedP99's ladder assumes it", id)
		}
		require.Truef(t, found, "%s must exist in obs.Budgets()", id)
	}
}

// TestCeilRank_MatchesTheNearestRankRule pins ceilRank against hand-computed values, including the
// exact ranks the run-32296920486 arithmetic turns on.
func TestCeilRank_MatchesTheNearestRankRule(t *testing.T) {
	require.Equal(t, int64(0), ceilRank(0.99, 0))
	require.Equal(t, int64(1), ceilRank(0.99, 1))
	require.Equal(t, int64(507), ceilRank(0.99, 512), "0.99*512 = 506.88")
	require.Equal(t, int64(2043), ceilRank(0.99, 2063), "0.99*2063 = 2042.37")
	require.Equal(t, int64(2044), ceilRank(0.99, 2064), "0.99*2064 = 2043.36")
	require.Equal(t, int64(2061), ceilRank(0.999, 2063), "0.999*2063 = 2060.937")
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

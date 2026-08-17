package main

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/obs"
)

// BudgetRow is one row of the out.json "budgets" array (task-7-spec.md's exact shape). LimitMs
// and Pass are pointers so an ungated budget (B-D) marshals its own null/null rather than a
// misleading zero value — task-7-spec.md's example pins exactly "limit_ms":null,"pass":null.
type BudgetRow struct {
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

// SpawnFloor is the process-creation floor measurement (task-7-spec.md step 4): `qompack version`
// spawned 200 times, recording its own wall-time distribution.
type SpawnFloor struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

// Report is the harness's out.json shape, verbatim per task-7-spec.md step 9.
type Report struct {
	Platform     string      `json:"platform"`
	N            int         `json:"n"`
	BAMethod     string      `json:"b_a_method"`
	SpawnFloorMs SpawnFloor  `json:"spawn_floor_ms"`
	Notes        []string    `json:"notes"`
	Budgets      []BudgetRow `json:"budgets"`
}

// bAMethod is the artifact's own explanation of how the gated "B-A" row is now produced.
//
// FIX ROUND 1 / controller ruling #29 amends task-7-spec.md step 6: B-A (internal/obs/budgets.go:
// "hook_controlled — client main() entry to exit") is defined to EXCLUDE OS process creation, but
// this harness can only measure real process spawns (task-7-spec.md step 5), so the wall-clock
// sample always carries fork/exec + image load on top of the number B-A actually names. Per-sample
// subtraction of a CONSTANT floor (spawn_floor_ms.p50) removes that constant's location but none
// of its dispersion, so the estimator is unbiased at p50 and dispersion-contaminated at p99 —
// exactly the statistic the gate reads. The gated "B-A" row below is therefore now sourced from
// the daemon's own TS-anchored estimate instead: hook_controlled (recvTS - reqTS, plus
// hotPathTailAllowance), the same series internal/daemon's breach detector itself consumes,
// fetched via the status op after the run. It is real, honestly-measured daemon-side data — not a
// wall-clock estimate — and it excludes process creation by construction, matching B-A's own
// definition exactly. The wall-clock, floor-subtracted number is NOT discarded: it survives as the
// "B-A_spawn_estimate" row (budgetIDBASpawnEstimate below), always informational, never gated —
// see its own doc comment for the caveat this constant explains.
const bAMethod = "daemon status hook_controlled (observed + hotPathTailAllowance), p99 gated against obs.Budgets() B-A's limit; " +
	"see the B-A_spawn_estimate row for the wall-clock, floor-subtracted diagnostic (unbiased at p50, dispersion-contaminated at p99 — spec step 6 amended by controller ruling #29)"

// budgetIDBASpawnEstimate is the renamed budget_id the wall-clock, per-sample-floor-subtracted
// number now reports under (FIX ROUND 1, controller ruling #29: "rename their keys so nothing
// reads them as B-A"). It is always informational (LimitMs/Pass nil), like B-D: subtracting a
// constant floor removes the floor distribution's location but none of its dispersion, so this
// estimator is a good, unbiased read of the MEDIAN hot-path cost above the floor, but its own p99
// is contaminated by host process-creation jitter and must never be read as if it were B-A's own
// p99. See bAMethod's doc comment for the full derivation.
const budgetIDBASpawnEstimate = "B-A_spawn_estimate"

// GateFailed reports whether any GATED budget (a non-nil Pass) reports false. B-D's Pass is
// always nil and can never fail the run (task-7-spec.md step 10).
func (r Report) GateFailed() bool {
	for _, b := range r.Budgets {
		if b.Pass != nil && !*b.Pass {
			return true
		}
	}
	return false
}

// percentile returns the p-th percentile (0<p<=1) of an ALREADY-SORTED-ASCENDING slice under the
// nearest-rank method: rank = ceil(p*n), clamped to [1,n] — the same method
// internal/obs/hist.go's own Snapshot uses, so the harness's own percentiles agree in spirit with
// the daemon's bucketed ones (this one is exact, over real samples, not bucketed).
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// percentiles sorts samples in place (ascending) and returns its five-number summary: p50, p95,
// p99, p999 and the exact max.
func percentiles(samples []time.Duration) (p50, p95, p99, p999, max time.Duration) {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 = percentile(samples, 0.50)
	p95 = percentile(samples, 0.95)
	p99 = percentile(samples, 0.99)
	p999 = percentile(samples, 0.999)
	if len(samples) > 0 {
		max = samples[len(samples)-1]
	}
	return
}

// subtractFloor derives B-A_i = max(0, B-D_i - floorP50) per sample (task-7-spec.md step 6),
// never subtracting a percentile from a percentile.
func subtractFloor(bd []time.Duration, floorP50 time.Duration) []time.Duration {
	out := make([]time.Duration, len(bd))
	for i, d := range bd {
		v := d - floorP50
		if v < 0 {
			v = 0
		}
		out[i] = v
	}
	return out
}

// msf renders a duration as milliseconds, the unit every field in the out.json artifact uses.
func msf(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// buildBudgetRow computes one BudgetRow from a raw (unsorted, not-yet-copied) sample set. samples
// is copied before sorting so the caller's own slice is never mutated out from under it. gated
// false (B-D) leaves LimitMs/Pass nil, matching the out.json example's "limit_ms":null,"pass":null.
func buildBudgetRow(id string, samples []time.Duration, limit time.Duration, gated bool) BudgetRow {
	cp := append([]time.Duration(nil), samples...)
	p50, p95, p99, p999, max := percentiles(cp)
	row := BudgetRow{
		BudgetID: id, N: len(samples),
		P50: msf(p50), P95: msf(p95), P99: msf(p99), P999: msf(p999), Max: msf(max),
	}
	if gated {
		lim := msf(limit)
		row.LimitMs = &lim
		pass := p99 < limit
		row.Pass = &pass
	}
	return row
}

// buildBudgetRowFromSnapshot builds one BudgetRow directly from a daemon-side obs.HistSnapshot —
// B-B's only source of truth (task-7-spec.md step 8: "read the daemon-side histograms via the
// status op"). Unlike buildBudgetRow, there are no raw per-sample durations to re-percentile: the
// daemon's own bucketed histogram already computed them, so this reports its N/P50/P95/P99/P999/
// Max verbatim.
func buildBudgetRowFromSnapshot(id string, snap obs.HistSnapshot, limit time.Duration, gated bool) BudgetRow {
	row := BudgetRow{
		BudgetID: id, N: int(snap.N),
		P50: msf(snap.P50), P95: msf(snap.P95), P99: msf(snap.P99), P999: msf(snap.P999), Max: msf(snap.Max),
	}
	if gated {
		lim := msf(limit)
		row.LimitMs = &lim
		pass := snap.P99 < limit
		row.Pass = &pass
	}
	return row
}

// budgetLimit looks up id's configured Limit from obs.Budgets(), evaluated against cfg —
// task-7-brief.md's binding ruling: "Read limits from config.Defaults() + obs.Budgets() rather
// than re-hardcoding." Returns 0 for an unknown id (never reached with the three ids this harness
// actually gates: B-A, B-B, B-E).
func budgetLimit(cfg config.Config, id obs.BudgetID) time.Duration {
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Limit(cfg)
		}
	}
	return 0
}

// budgetHistName looks up id's histogram name from obs.Budgets(), the same indirection
// budgetLimit uses — the harness never spells a histogram name as its own literal.
func budgetHistName(id obs.BudgetID) string {
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Hist
		}
	}
	return ""
}

// checkDeliveryIntegrity is FIX ROUND 1's I-2 guard: gotN (the daemon's own l0_ingest
// obs.HistSnapshot.N, read via the status op) must equal exactly iterations, plus warmHotTranche
// when warm-up ran — every observe.tool request this harness sent, and nothing else, touches
// l0_ingest.
//
// FIX ROUND 2, N-1: warm-up's own bulk is admin.ping traffic (measure.go's warmDaemon), which is
// not req.Op.HotPath() and never reaches ing.Accept — only the warm-up's small hot-path tranche
// (warmHotTranche observe.tool requests) does. This check's expected count moved from
// "iterations + warmIterations" to "iterations + warmHotTranche" for exactly that reason: the
// bulk of warmIterations is deliberately NOT expected to touch l0_ingest any more.
//
// A shortfall means some hook spawns (or the hot tranche itself) degraded to the spool path
// (internal/ipc/client.go's Send: a connect timeout, a write failure, DaemonEnabled==false, or a
// HotSpool breach all swallow the failure and return quickly) instead of reaching the daemon —
// which would bias a wall-clock estimate DOWNWARD (a spooled hook exits faster than a delivered
// one) and could pass a gate that is actually measuring the degraded path. Fails loudly rather
// than silently reporting on partial data.
func checkDeliveryIntegrity(gotN int64, iterations int, warmDaemonRan bool) error {
	want := int64(iterations)
	if warmDaemonRan {
		want += warmHotTranche
	}
	if gotN == want {
		return nil
	}
	return fmt.Errorf(
		"hotpath: delivery integrity check failed: the daemon's own l0_ingest histogram observed %d requests, want exactly %d (warm=%v x %d hot-path tranche + iterations=%d) — some hook spawns degraded to the spool path instead of reaching the daemon, which would bias every derived number; refusing to report a Report rather than silently passing a gate on partial data",
		gotN, want, warmDaemonRan, warmHotTranche, iterations)
}

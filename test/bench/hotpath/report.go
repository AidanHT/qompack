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

// budgetIDBECPU is the co-load-immune half of B-E: the same 50 `qompack checkpoint` children the
// "B-E" row above times on the wall, measured instead in the CPU time they actually consumed
// (user+system, off cmd.ProcessState — see process.go's spawnSamples), gated on p99 against the
// SAME obs.Budgets() B-E limit. It is a hard gate everywhere, --under-coload included.
//
// It is also the same number on every job, which the wall-clock row is not: buildBinary compiles
// the child with a plain `go build` (process.go), so what this row measures is the uninstrumented
// product's own cost whether the parent test binary was built with -race, with coverage, or with
// neither.
//
// Why it exists. B-E is the only budget this harness gates on a whole real process measured from
// outside, so its sample is host process creation plus scheduling weather plus the checkpoint's
// own cost — and 00-ARCHITECTURE.md §2.4 has already ruled on the first of those: B-D "includes
// host process creation. Reported only, never gated", because it "reports the host's
// process-creation cost, which no plugin architecture can budget away" (internal/obs/budgets.go's
// BD Limit comment). Ruling #29 applied that same reasoning to B-A and re-pointed the gated row at
// a measurement that excludes process creation by construction, leaving the wall-clock number as
// the always-informational B-A_spawn_estimate row. B-E kept its wall-clock gate only because 2000
// ms is large next to a spawn floor — until the whole-tree `go test` job put it on a shared runner
// and the gap stopped being large.
//
// What that costs, measured. CI run on windows-latest: bench-gate, which runs this harness alone
// on its own runner, reported B-E p99 = 67.2 ms (ubuntu 232.1, macos 21.3) against the 2000 ms
// limit; the `test (windows-latest)` job, same commit, same runner class, minutes later, running
// this harness from test/integration under ~20 concurrent package binaries on 2 cores, reported
// 4302 ms — a 64x move with the product byte-identical. Reproduced locally (process.go's
// spawnSamples table): wall p50 138.8 → 3219.1 ms while the same children's CPU p50/p99 stayed at
// 15.625/46.875 ms in both runs, unchanged to the tick. The wall row was measuring the runner's
// spare capacity; this row measures the checkpoint.
//
// Why not price the wall limit from the spawn floor instead — the wave-1 calibration pattern
// (internal/store/gc_test.go's gcCalibratedSweep, plans/V2-report.md §0 item 25). Tried first, and
// the measurement rejects it: under one identical co-load the spawn floor's own p50 inflated 4.4x
// (57.2 → 249.9 ms, and 52.0 → 231.5 ms in a second pair) while B-E's wall p50 inflated 23x and
// B-D's 6-8x. Inflation scales with how much work the child needs, so `qompack version` is not a
// baseline for `qompack checkpoint`: a limit priced off it under-scales by roughly 5x and would
// still fail on a correct product. A calibration baseline has to be the same shape of work as the
// thing it prices, and the only sample of that shape here is B-E itself.
//
// What this clock cannot see, stated rather than papered over: time a child spends BLOCKED —
// waiting on disk, on a lock, on the daemon — costs no CPU, so a regression that stalls the
// checkpoint on I/O without executing more instructions passes this gate. Quiet, roughly half of a
// B-E wall sample is exactly that (wall p50 138.8 ms against 57.2 ms of spawn floor and 15.6 ms of
// CPU). That is why the wall-clock "B-E" row keeps its own hard gate at the same limit and is
// waived ONLY for a run that has declared itself co-loaded (--under-coload, main.go): bench-gate
// and nightly still judge it, in the isolation where a wall-clock SLO is judgeable at all, and the
// whole-tree run — where it never was — judges the CPU one.
const budgetIDBECPU = "B-E_cpu"

// beWallWaivedNote is the artifact's own disclosure for a --under-coload run: the wall-clock B-E
// row in it is a MEASUREMENT and not a judgement, and a reader must not have to infer that from a
// null. It names the limit that was not applied and where it still is applied, so a passing
// artifact can never be read as the wall-clock budget having been met.
func beWallWaivedNote(limit time.Duration) string {
	return fmt.Sprintf(
		"%s's wall-clock row is REPORTED, not gated, for this run: --under-coload declares that the harness shares its host with unrelated concurrent work, and a wall-clock sample taken under co-load measures the host's spare capacity rather than the checkpoint (bench-gate measured this same row at 67.2ms on windows-latest in isolation and the whole-tree job at 4302ms on the same runner class minutes later, product unchanged). The %.0fms limit is still enforced on that wall-clock row by every run that does NOT pass --under-coload — bench-gate and nightly — and the %s row below enforces the same %.0fms limit on these children's own CPU time, which co-load does not move; see budgetIDBECPU (report.go)",
		obs.BE, msf(limit), budgetIDBECPU, msf(limit))
}

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

// The nearest-rank percentile ranks tailAdjustedP99 reasons about — the same two internal/obs/
// hist.go's own Snapshot computes, so a rank derived here means what the daemon's reported value
// means.
const (
	rank99  = 0.99
	rank999 = 0.999
)

// The bases tailAdjustedP99 can report its value on. They name WHICH order statistic of the
// DELIVERED sample set was used to bound the full population's p99, so the artifact's own note
// can say it out loud instead of presenting a bound as if it were a measurement.
const (
	basisDeliveredP99  = "p99"
	basisDeliveredP999 = "p999"
	basisDeliveredMax  = "max"
	basisUncertifiable = "uncertifiable"
)

// ceilRank is the nearest-rank rank of the p-th percentile over n samples: ceil(p*n), clamped to
// [1,n] — internal/obs/hist.go's percentileOf and this package's own percentile use the identical
// rule, so ranks computed here line up with the values the daemon reports.
func ceilRank(p float64, n int64) int64 {
	if n <= 0 {
		return 0
	}
	r := int64(math.Ceil(p * float64(n)))
	if r < 1 {
		r = 1
	}
	if r > n {
		r = n
	}
	return r
}

// tailAdjustedP99 answers the question a shortfall actually poses: with `missing` of the planned
// samples absent from snap's population, what can still honestly be said about the FULL
// population's p99?
//
// The missing samples are not missing at random, and that is the whole point — every route out of
// the population selects the SLOW end of the distribution:
//
//   - a request deferred to the spool got there only after internal/ipc/client.go's Send had
//     burnt its entire connect/ACK budget waiting. For B-A that is not merely "slow": ruling #29
//     gates B-A on the TS-anchored (recvTS - reqTS), and a deferred request's recvTS is the
//     daemon's NEXT DRAIN — seconds away, not milliseconds. It is an over-budget sample by the
//     gated series' own definition.
//   - for B-B (l0_ingest, the daemon's read-to-WAL-append cost) the same request contributes no
//     sample at all, and the omission is still not random: a client's connect or ACK deadline
//     expires when the daemon's accept loop is momentarily too busy to answer, which is the same
//     host-and-process pressure that makes its ingest slow. The missing B-B samples correlate
//     with B-B's own upper tail, so dropping them is optimistic in exactly the same direction.
//     Counting them as over-budget is the conservative reading of a correlation whose sign is
//     known and whose magnitude is not.
//   - a sample the daemon received but validHotPathTS refused to time (internal/daemon/
//     handlers.go, counted as hotpath_sample_invalid) was discarded because recvTS - reqTS had
//     already exceeded hotPathSampleMaxAge — a 10-second observation, thrown away.
//
// So the surviving sample set is not merely smaller, it is TRUNCATED AT THE TOP, and its p99 is a
// strict under-estimate of the full population's. Reporting it as the gated number would let a
// run pass B-A precisely because the slowest hooks failed to be measured. These `missing` samples
// are therefore counted back in, as over-budget samples sitting above every delivered one, and
// the p99 is re-derived over the whole population of observed+missing under the same nearest-rank
// rule internal/obs/hist.go itself uses — rank = ceil(0.99 * (observed + missing)):
//
//   - rank > observed — the p99 itself falls inside the un-delivered block. There is no honest
//     number to report and the gate cannot pass: ok is false.
//   - rank <= ceil(0.99*observed) — the ceiling absorbed the shift; the daemon's own p99 already
//     sits at or above the required rank and is exact.
//   - otherwise the required rank sits between two order statistics the daemon reports, so the
//     next one it DOES report (p999, else the exact max) is returned as a sound upper bound.
//     Bucketed histograms cannot be re-ranked from a five-number summary, so an upper bound is
//     the tightest honest answer available — and a bound is the correct direction for a gate:
//     it can refuse to certify a run it cannot prove good, never certify one it cannot.
func tailAdjustedP99(snap obs.HistSnapshot, missing int64) (value time.Duration, basis string, ok bool) {
	observed := snap.N
	if observed <= 0 {
		return 0, basisUncertifiable, false
	}
	if missing <= 0 {
		return snap.P99, basisDeliveredP99, true
	}

	want := ceilRank(rank99, observed+missing)
	switch {
	case want > observed:
		return snap.P99, basisUncertifiable, false
	case want <= ceilRank(rank99, observed):
		return snap.P99, basisDeliveredP99, true
	case want <= ceilRank(rank999, observed):
		return snap.P999, basisDeliveredP999, true
	default:
		return snap.Max, basisDeliveredMax, true
	}
}

// buildBudgetRowFromSnapshot builds one BudgetRow directly from a daemon-side obs.HistSnapshot —
// B-B's only source of truth, and (ruling #29) the gated B-A row's too (task-7-spec.md step 8:
// "read the daemon-side histograms via the status op"). Unlike buildBudgetRow, there are no raw
// per-sample durations to re-percentile: the daemon's own bucketed histogram already computed
// them, so N/P50/P95/P999/Max are reported verbatim over the samples that were actually observed.
//
// P99 is the one field that is not verbatim, and only when missing > 0: it carries
// tailAdjustedP99's value over the FULL planned population (see that function for why the
// survivors' own p99 would understate it), and the gate reads that number. The returned string is
// the disclosure note for the artifact — empty when nothing was missing, so a clean run's output
// is byte-identical to what this harness has always produced.
func buildBudgetRowFromSnapshot(id string, snap obs.HistSnapshot, limit time.Duration, gated bool, missing int64) (BudgetRow, string) {
	p99, basis, certifiable := tailAdjustedP99(snap, missing)
	row := BudgetRow{
		BudgetID: id, N: int(snap.N),
		P50: msf(snap.P50), P95: msf(snap.P95), P99: msf(p99), P999: msf(snap.P999), Max: msf(snap.Max),
	}
	if gated {
		lim := msf(limit)
		row.LimitMs = &lim
		pass := certifiable && p99 < limit
		row.Pass = &pass
	}
	if missing <= 0 {
		return row, ""
	}
	return row, tailAdjustmentNote(id, snap, missing, basis, certifiable)
}

// tailAdjustmentNote spells out, for the artifact's own notes array, exactly what was done to a
// row whose population came up short: how many samples are missing, what they were counted as,
// which rank the full population's p99 needs, and which order statistic of the delivered set was
// reported in its place. Nothing about the adjustment is left implicit — the number in the p99
// field is not a measurement, and a reader must not have to guess that.
func tailAdjustmentNote(id string, snap obs.HistSnapshot, missing int64, basis string, certifiable bool) string {
	total := snap.N + missing
	want := ceilRank(rank99, total)
	have := ceilRank(rank99, snap.N)

	switch {
	case !certifiable:
		return fmt.Sprintf(
			"%s's p99 CANNOT be certified and its gate is failed on that ground: %d of the %d planned samples never reached the daemon's histogram and are counted as over-budget samples, which puts the full population's nearest-rank p99 at rank %d of %d — inside the un-delivered block, since only %d samples were observed. The p99 field reports the DELIVERED set's own p99 for diagnosis only; it is a lower bound on the real one, never the gated number",
			id, missing, total, want, total, snap.N)
	case want <= have:
		return fmt.Sprintf(
			"%s: %d of the %d planned samples never reached the daemon's histogram and are counted back in as over-budget samples, but the full population's nearest-rank p99 still lands at rank %d — at or below the rank the delivered set's own p99 already reports (%d of %d) — so the p99 in this row is exact and needed no adjustment",
			id, missing, total, want, have, snap.N)
	default:
		return fmt.Sprintf(
			"%s's p99 is reported over the full planned population of %d, not the %d samples the daemon actually observed: the %d missing sample(s) are counted back in as over-budget samples (they are the SLOW ones — see tailAdjustedP99), which moves the nearest-rank p99 from rank %d to rank %d. That rank is not one the delivered p99 covers, so the delivered set's %s (%.3fms) is reported instead as a sound upper bound, and the gate is decided on it. n=%d and the other percentiles in this row are the delivered set's own",
			id, total, snap.N, missing, have, want, basis, msf(percentileOfBasis(snap, basis)), snap.N)
	}
}

// percentileOfBasis returns the snapshot field basis names — the inverse of tailAdjustedP99's own
// choice, used only to print the value inside the disclosure note.
func percentileOfBasis(snap obs.HistSnapshot, basis string) time.Duration {
	switch basis {
	case basisDeliveredP999:
		return snap.P999
	case basisDeliveredMax:
		return snap.Max
	default:
		return snap.P99
	}
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

// The delivery guard FIX ROUND 1's I-2 introduced as checkDeliveryIntegrity now lives in
// delivery.go, as reconcileDelivery + hookControlledShortfall. Its reason for existing is
// unchanged and unrelaxed — a Report must never be produced over partial data — but it now
// establishes whether a shortfall is a DEFERRAL (durable in the spool; §8.1/§12.2's documented
// degrade-rather-than-block path) or a LOSS before deciding, and hands the deferrals to
// tailAdjustedP99 above so they are counted back into the gated population as over-budget samples
// rather than quietly dropped out of it.

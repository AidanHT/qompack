package scheduler

import (
	"math"

	"github.com/qompack/qompack/internal/core"
)

// breakdownCapacity pre-sizes Decision.Breakdown. A fired Decision under a known regime carries
// ~40 keys; sizing the map for them up front keeps Evaluate inside its allocation budget (a map
// that grows from the default size reallocates three times on the way).
const breakdownCapacity = 64

// Breakdown["regime_rung"] codes (ruling R6): which rung of cacheregime.go's ladder fired,
// numeric because Breakdown is map[string]float64. Anything unrecognized is 0.
const (
	regimeRungOther float64 = iota
	regimeRungUnknown
	regimeRungEnable1H
	regimeRungDisabled
	regimeRungForce5M
	regimeRungSubagent5M
)

// backgroundTaskCount is the size of the O3 task set planBackground can emit, used to size
// its result exactly once.
const backgroundTaskCount = 6

// triggerReasonCount is how many TriggerReason values Evaluate can report at once — the six
// constants in types.go, soft_floor included — used to size Decision.Reasons exactly once. A
// seventh reason must bump it, or the append past the capacity allocates (BenchmarkEvaluate's
// allocs/op budget is the tripwire).
const triggerReasonCount = 6

// candidateBufferLen is how many supplied candidates Evaluate can sort without a heap copy —
// the plan's 64-candidate performance fixture, and twice what the sweep keeps.
const candidateBufferLen = 2 * maxScoredCandidates

// Evaluate is a PURE function of Inputs: no I/O, no clock, no globals, no mutation of the
// argument. Calling it twice with the same Inputs returns byte-identical Decisions. This is
// what makes the whole of Qompack.md §8.4 unit-testable and replayable (00-ARCHITECTURE §5.13).
func Evaluate(in Inputs) Decision {
	cfg := in.Cfg
	d := Decision{Breakdown: make(map[string]float64, breakdownCapacity), TTL: TTLUnknown}

	if in.EffectiveWindow <= 0 {
		d.Breakdown["error_no_window"] = 1
		return d
	}

	// Every float that enters the arithmetic is made finite first. Breakdown is JSON-marshalled
	// by the state codec and encoding/json refuses NaN and ±Inf outright, so a single bad
	// measurement must not be able to poison the decision or the persisted state.
	burn := finiteOrZero(in.BurnRateTokensPerMin)
	lambda := finiteOrZero(in.CouplingLambda)
	probChangepoint := finiteOrZero(in.Changepoint.ProbChangepoint)
	expiringFraction := finiteOrZero(in.ExpiringTriggerFraction)

	// ── thresholds ────────────────────────────────────────────────────────
	soft := SoftFloor(in.EffectiveWindow, cfg)
	hard := HardCeiling(in.EffectiveWindow, cfg)
	d.SoftFloorTokens, d.HardCeilingTokens = soft, hard
	n := in.ContextTokens

	// ── cache state (E1: time since last API CALL, never last cache write) ─
	// Regime first: every threshold below is relative to it. A zero-value Regime means the
	// Runtime did not resolve one, which is the unknown rung, not an error. A Disabled regime
	// keeps its zero TTL bounds: there is no cache to age.
	reg := in.Regime
	if reg.TTLMaxSeconds <= 0 && !reg.Disabled {
		reg = UnknownRegime(cfg, in.AssumeMaxTTLSeconds)
	}
	reg.ReadMultiplier = finiteOrZero(reg.ReadMultiplier)
	reg.WriteMultiplier = finiteOrZero(reg.WriteMultiplier)
	anchor := resolveTTLAnchor(in.LastRequestStartTS, in.LastAPICallTS)
	ttl, gap := ClassifyTTL(in.Now, anchor, reg, in.EffortChanged)
	cf := clampUnit(CacheFactor(ttl, gap, reg))
	d.TTL = ttl

	// ── Young–Daly cadence ────────────────────────────────────────────────
	var elapsed, interval float64
	delta, haveDelta := resolveDelta(in, cfg)
	if !isFinite(delta) {
		// An infinite "measurement" is not a measurement: treat it as unmeasured rather than
		// letting YoungDaly's own guard disable the clause silently.
		delta, haveDelta = 0, false
	}
	m := mtbfSeconds(n, hard, burn)
	switch {
	case !cfg.YoungDaly.Enabled:
		d.Breakdown["young_daly_disabled"] = 1
	case !haveDelta:
		d.Breakdown["young_daly_delta_unmeasured"] = 1
	case in.LastCompactionTS <= 0:
		d.Breakdown["young_daly_no_baseline"] = 1
	default:
		interval = YoungDaly(delta, m)
		elapsed = ageSeconds(in.Now, in.LastCompactionTS)
	}
	d.YoungDalySeconds = interval

	// ── the composite trigger, verbatim from §8.4 ─────────────────────────
	//   should_compact = tokens > soft_floor
	//                    AND ( at_changepoint
	//                          OR elapsed > young_daly_interval
	//                          OR tokens > hard_ceiling
	//                          OR idle_gap > ttl_max
	//                          OR (regime_known AND idle_gap > 0.8·ttl)
	//                          OR effort_changed )
	aboveSoftFloor := n > soft
	atChangepoint := in.Changepoint.AtChangepoint
	youngDalyElapsed := interval > 0 && elapsed > interval
	aboveHardCeiling := n > hard
	idleColdCache := ttl == TTLCold // effort_changed arrives here: ClassifyTTL makes it cold
	// Fire one band EARLIER than expiry when the regime is known: the summarization request still
	// reads the prefix from cache there, which is (1−r)·n cheaper than the same compaction after
	// the prefix dies. Gated on a known regime because the unknown band spans 150 s–3600 s, and
	// on a positive fraction because runtime.scheduler.cache.expiringTriggerFraction ≤ 0 turns the
	// trigger off.
	cacheExpiring := expiringFraction > 0 && ttl == TTLExpiring &&
		reg.TTLMinSeconds == reg.TTLMaxSeconds &&
		gap >= expiringFraction*float64(reg.TTLMaxSeconds)

	if aboveSoftFloor {
		d.Reasons = make([]TriggerReason, 0, triggerReasonCount)
		d.Reasons = append(d.Reasons, TriggerSoftFloor)
		if atChangepoint {
			d.Reasons = append(d.Reasons, TriggerChangepoint)
		}
		if youngDalyElapsed {
			d.Reasons = append(d.Reasons, TriggerYoungDaly)
		}
		if aboveHardCeiling {
			d.Reasons = append(d.Reasons, TriggerHardCeiling)
		}
		if idleColdCache {
			d.Reasons = append(d.Reasons, TriggerIdleColdCache)
		}
		if cacheExpiring {
			d.Reasons = append(d.Reasons, TriggerCacheExpiring)
		}
	}
	fired := aboveSoftFloor &&
		(atChangepoint || youngDalyElapsed || aboveHardCeiling || idleColdCache || cacheExpiring)

	// ── p-selection ───────────────────────────────────────────────────────
	// Both sweeps run on stack buffers: the candidate copy spills to the heap only when a
	// caller supplies more than candidateBufferLen, and the scored set never can, because
	// capHighestPos bounds the eligible set at maxScoredCandidates before scoring.
	var candBuf [candidateBufferLen]Candidate
	var scoreBuf [maxScoredCandidates]scored
	cands, nonMono := prepareCandidatesInto(candBuf[:0], in.Candidates)
	if nonMono {
		d.Breakdown["reclaimable_nonmonotonic"] = 1
	}
	elig, relaxed := eligible(cands)
	if relaxed {
		d.Breakdown["round_boundary_relaxed"] = 1
	}
	elig = capHighestPos(elig)
	ss := scoreCandidatesInto(scoreBuf[:0], elig, n, cf, lambda, reg)
	best, ok := chooseP(ss, ttl, cfg)

	switch {
	case !fired:
		d.ShouldCompact = false
	case !ok:
		// The trigger fired but there is no legal cut point. Do NOT claim a compaction the
		// scheduler cannot place; escalate instead so /qompack:status shows the pressure.
		d.ShouldCompact = false
		d.Breakdown["no_candidates"] = 1
	default:
		d.ShouldCompact = true
		d.P, d.PScore = best.c, best.score
		d.Breakdown["reclaimable"] = best.reclaim
		d.Breakdown["rewrite"] = best.rewrite
		d.Breakdown["distortion"] = best.distortion
		d.Breakdown["score"] = best.score
		d.Breakdown["p"] = float64(best.c.Pos)
		d.Breakdown["p_turn"] = float64(best.c.Turn)
		d.Breakdown["p_segment"] = float64(best.c.SegmentID)
		d.Breakdown["coupling"] = float64(best.c.Coupling)
		d.Breakdown["reclaimable_tokens"] = float64(best.c.ReclaimableTokens)
		d.Breakdown["rewrite_tokens"] = best.tail * cf // clamped tail, never negative
	}

	// ── urgency ───────────────────────────────────────────────────────────
	switch {
	case aboveHardCeiling:
		d.Urgency = UrgencyNow
	case fired:
		d.Urgency = UrgencyAdvisory
	default:
		d.Urgency = UrgencyNone
	}
	// Ruling R7: with no host auto-compact trigger to stay ahead of (DISABLE_COMPACT, or an
	// unenforced window), hard_ceiling still reports but cannot promise headroom Evaluate no
	// longer controls — every trigger becomes a recommendation.
	if in.HostTriggerAbsent && d.Urgency == UrgencyNow {
		d.Urgency = UrgencyAdvisory
		d.Breakdown["urgency_capped_advisory"] = 1
	}

	// ── O3 background plan ────────────────────────────────────────────────
	d.Background = planBackground(in, ttl, soft, haveDelta)

	// ── observability: every number /qompack:status and eval need ─────────
	d.Breakdown["context_tokens"] = float64(n)
	d.Breakdown["effective_window"] = float64(in.EffectiveWindow)
	d.Breakdown["soft_floor"] = float64(soft)
	d.Breakdown["hard_ceiling"] = float64(hard)
	d.Breakdown["idle_gap_seconds"] = gap
	d.Breakdown["cache_factor"] = cf
	d.Breakdown["ttl_min_seconds"] = float64(reg.TTLMinSeconds)
	d.Breakdown["ttl_max_seconds"] = float64(reg.TTLMaxSeconds)
	d.Breakdown["regime_read_multiplier"] = reg.ReadMultiplier
	d.Breakdown["regime_write_multiplier"] = reg.WriteMultiplier
	d.Breakdown["regime_rung"] = regimeRung(reg.Source)
	// The corpus for tuning expiringTriggerFraction: the gap as a fraction of the regime's upper
	// TTL at every evaluation. 0 when the regime has no TTL to be a fraction of.
	if reg.TTLMaxSeconds > 0 {
		d.Breakdown["fired_at_ttl_fraction"] = gap / float64(reg.TTLMaxSeconds)
	} else {
		d.Breakdown["fired_at_ttl_fraction"] = 0
	}
	if reg.Disabled {
		d.Breakdown["cache_disabled"] = 1
	}
	if in.EffortChanged {
		d.Breakdown["cold_reason_effort_change"] = 1
	}
	d.Breakdown["read_multiplier"] = finiteOrZero(cfg.Cache.ReadMultiplier)
	d.Breakdown["write_multiplier"] = finiteOrZero(cfg.Cache.WriteMultiplier)
	d.Breakdown["lambda"] = lambda
	d.Breakdown["delta_seconds"] = delta
	d.Breakdown["mtbf_seconds"] = m
	d.Breakdown["young_daly_seconds"] = interval
	d.Breakdown["elapsed_seconds"] = elapsed
	d.Breakdown["burn_rate_tokens_per_min"] = burn
	d.Breakdown["prob_changepoint"] = probChangepoint
	d.Breakdown["run_length"] = float64(in.Changepoint.RunLength)
	d.Breakdown["candidates"] = float64(len(ss))
	d.Breakdown["candidates_supplied"] = float64(len(in.Candidates))
	d.Breakdown["frontier_turn"] = float64(in.FrontierTurn)
	d.Breakdown["residual_tokens"] = float64(in.ResidualTokens)
	d.Breakdown["last_cache_write_age_seconds"] = ageSeconds(in.Now, in.LastCacheWriteTS)

	// Belt and braces for the JSON invariant: the inputs above were sanitized, but the
	// threshold, TTL and cadence helpers are shared with other seats and the invariant is
	// Evaluate's to keep. The sweep allocates nothing.
	for k, v := range d.Breakdown {
		if !isFinite(v) {
			d.Breakdown[k] = 0
		}
	}
	d.PScore = finiteOrZero(d.PScore)
	d.YoungDalySeconds = finiteOrZero(d.YoungDalySeconds)
	return d
}

// ageSeconds is the non-negative age of a timestamp at now; 0 when the timestamp is unset or
// in the future.
func ageSeconds(now, then core.UnixMilli) float64 {
	if then <= 0 {
		return 0
	}
	a := float64(now-then) / 1000.0
	if a < 0 {
		return 0
	}
	return a
}

// regimeRung maps CacheRegime.Source onto Breakdown["regime_rung"] (ruling R6).
func regimeRung(source string) float64 {
	switch source {
	case "unknown":
		return regimeRungUnknown
	case "enable_1h":
		return regimeRungEnable1H
	case "disabled":
		return regimeRungDisabled
	case "force_5m":
		return regimeRungForce5M
	case "subagent_5m":
		return regimeRungSubagent5M
	default:
		return regimeRungOther
	}
}

// isFinite reports whether v is neither NaN nor ±Inf.
func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// finiteOrZero returns v when it is finite and 0 otherwise.
func finiteOrZero(v float64) float64 {
	if isFinite(v) {
		return v
	}
	return 0
}

// clampUnit clamps a cache factor into [0, 1]; a non-finite factor is treated as warm (1), the
// conservative assumption CacheFactor itself makes for an unknown state.
func clampUnit(v float64) float64 {
	switch {
	case !isFinite(v):
		return 1
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// planBackground is the O3 plan (§8.4 "Idle-time background work"). It is pure: the set is
// derived from Inputs alone, and the Runtime's registered tasks consult it before running.
// The result is nil when nothing applies and sized exactly otherwise. Order is normative:
// advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc.
func planBackground(in Inputs, ttl TTLState, soft core.Tokens, haveDelta bool) []BackgroundTask {
	if !in.Cfg.Idle.BackgroundWork {
		return nil
	}
	advance := in.ResidualTokens > 0 // O5 — always first: it is the latency lever
	precompute := in.ContextTokens > soft
	refresh := !haveDelta
	// A gap longer than the TTL is the "next idle window" §8.3 names for the bloom
	// rebuild, and the only moment at which DAG compaction and GC cost nothing.
	cold := ttl == TTLCold
	if !advance && !precompute && !refresh && !cold {
		return nil
	}
	out := make([]BackgroundTask, 0, backgroundTaskCount)
	if advance {
		out = append(out, BackgroundAdvanceFrontier)
	}
	if precompute {
		out = append(out, BackgroundPrecomputeSlice)
	}
	if refresh {
		out = append(out, BackgroundRefreshDelta)
	}
	if cold {
		out = append(out, BackgroundRebuildBloom, BackgroundCompactDAG, BackgroundGC)
	}
	return out
}

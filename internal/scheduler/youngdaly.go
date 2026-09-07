package scheduler

import (
	"math"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// YoungDaly is Qompack.md §6.7 / Appendix A: I* = √(2·δ·M).
// Returns 0 when either term is unknown, non-positive, NaN or infinite, and 0 means "no cadence
// constraint" — the caller must treat a zero interval as a disabled clause, never as
// "elapsed > 0 is always true". The NaN/Inf guard is deliberate: a NaN interval would silently
// disable the clause (interval > 0 is false for NaN) instead of reporting the disabled state.
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64 {
	if deltaSeconds <= 0 || mtbfSeconds <= 0 ||
		math.IsNaN(deltaSeconds) || math.IsNaN(mtbfSeconds) ||
		math.IsInf(deltaSeconds, 0) || math.IsInf(mtbfSeconds, 0) {
		return 0
	}
	return math.Sqrt(2 * deltaSeconds * mtbfSeconds)
}

// secondsPerMinute converts a per-minute burn rate into the seconds M is stated in.
const secondsPerMinute = 60.0

// mtbfSeconds is M: "expected time to forced compaction at the current burn rate" (§8.4).
// Forced compaction means crossing the hard ceiling, because that is the last position at
// which the plugin still gets to checkpoint first.
func mtbfSeconds(contextTokens, hardCeiling core.Tokens, burnTokensPerMin float64) float64 {
	headroom := float64(hardCeiling - contextTokens)
	if headroom <= 0 || burnTokensPerMin <= 0 {
		return 0
	}
	return headroom / burnTokensPerMin * secondsPerMinute
}

// resolveDelta implements the δ precedence rule.
//
//  1. cfg.YoungDaly.MeasuredDeltaSeconds non-nil and positive → explicit operator override, wins.
//  2. in.MeasuredDeltaSeconds non-nil and positive            → the Runtime's measurement.
//  3. neither                                                 → unknown; the clause is disabled and
//     Breakdown["young_daly_delta_unmeasured"] = 1 is set by the caller.
//
// Appendix C's `null` means "measure at runtime", not zero (00-ARCHITECTURE §11.2), so a
// nil config value is NOT a zero δ and must never collapse the interval to 0 silently.
func resolveDelta(in Inputs, cfg config.SchedulerCfg) (float64, bool) {
	if cfg.YoungDaly.MeasuredDeltaSeconds != nil && *cfg.YoungDaly.MeasuredDeltaSeconds > 0 {
		return *cfg.YoungDaly.MeasuredDeltaSeconds, true
	}
	if in.MeasuredDeltaSeconds != nil && *in.MeasuredDeltaSeconds > 0 {
		return *in.MeasuredDeltaSeconds, true
	}
	return 0, false
}

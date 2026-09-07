package scheduler

import (
	"math"

	"github.com/qompack/qompack/internal/core"
)

// ttlExpiringFraction is the fraction of the TTL after which the prefix is treated as
// "expiring" and the marginal value of the warm cache begins to decay linearly to zero
// (Qompack.md §8.4: "idle long enough that expiry is imminent … the same preference applies").
const ttlExpiringFraction = 0.5

// millisPerSecond converts core.UnixMilli differences into the seconds every TTL is stated in.
const millisPerSecond = 1000.0

// resolveTTLAnchor picks the timestamp the idle gap is measured from: the start of the most
// recent API REQUEST when the observer recorded one (Inputs.LastRequestStartTS), else the last
// API-call hook (Inputs.LastAPICallTS), i.e. exactly the pre-regime behaviour. The request start
// is never later than the Stop that LastAPICallTS is usually written from, so this can only make
// the measured gap larger — the one-sidedness TestTTLAnchorIsNeverLaterThanStop asserts.
func resolveTTLAnchor(lastRequestStartTS, lastAPICallTS core.UnixMilli) core.UnixMilli {
	if lastRequestStartTS > 0 {
		return lastRequestStartTS
	}
	return lastAPICallTS
}

// ClassifyTTL returns the cache state and the observed idle gap in seconds.
//
// anchorTS is the start of the most recent API REQUEST, not the end of its response — see the
// clock corrections in cacheregime.go. Callers pass resolveTTLAnchor(Inputs.LastRequestStartTS,
// Inputs.LastAPICallTS).
//
// The two thresholds key off the two DIFFERENT bounds of the regime, and that asymmetry is what
// makes "provably cold" true rather than hopeful:
//
//	anchorTS == 0 or regime has no TTL   → TTLUnknown, gap 0
//	effortChanged                        → TTLCold at any gap (the prefix is gone, not aging)
//	gap <  0.5·TTLMin                    → TTLWarm     (readable under every regime)
//	0.5·TTLMin <= gap < TTLMax           → TTLExpiring (readable under SOME regime)
//	gap >= TTLMax                        → TTLCold     (dead under every regime)
//
// When the regime is known, TTLMin == TTLMax and this reduces exactly to the shipped behaviour.
// A disabled regime has no TTL and so is always TTLUnknown: there is no cache to be warm or cold.
func ClassifyTTL(now, anchorTS core.UnixMilli, reg CacheRegime, effortChanged bool) (TTLState, float64) {
	if anchorTS <= 0 || reg.TTLMaxSeconds <= 0 {
		return TTLUnknown, 0
	}
	gap := float64(now-anchorTS) / millisPerSecond
	if gap < 0 {
		gap = 0
	}
	if effortChanged {
		return TTLCold, gap
	}
	switch {
	case gap >= float64(reg.TTLMaxSeconds):
		return TTLCold, gap
	case gap >= ttlExpiringFraction*float64(reg.TTLMinSeconds):
		return TTLExpiring, gap
	default:
		return TTLWarm, gap
	}
}

// CacheFactor multiplies rewrite(p). Warm = 1 (pay in full), cold = 0 ("rewrite(p) → 0 for
// all p"), and the expiring band ramps linearly between them so the policy is continuous
// rather than a cliff:
//
//	f = (TTLMax − gap) / (TTLMax − ttlExpiringFraction·TTLMin), clamped to [0, 1]
//
// which for a known regime is exactly (ttl − gap)/(ttl·0.5). Unknown is conservative: assume
// warm. A regime with no TTL (disabled) always returns 1 — with no cache every token of tail
// costs exactly one token to resend and there is no cold state to exploit.
func CacheFactor(state TTLState, idleGapSeconds float64, reg CacheRegime) float64 {
	if reg.TTLMaxSeconds <= 0 {
		return 1
	}
	switch state {
	case TTLCold:
		return 0
	case TTLExpiring:
		ttlMax := float64(reg.TTLMaxSeconds)
		band := ttlMax - ttlExpiringFraction*float64(reg.TTLMinSeconds)
		if band <= 0 || math.IsNaN(idleGapSeconds) {
			return 1
		}
		f := (ttlMax - idleGapSeconds) / band
		return min(max(f, 0), 1)
	default: // TTLWarm, TTLUnknown
		return 1
	}
}

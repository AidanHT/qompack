package scheduler

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Claude Code's own trigger arithmetic (Qompack.md §2.5):
//
//	effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
//	autoCompactThreshold   = effectiveContextWindow − 13_000
//
// These describe the HOST, not Qompack tunables, so Appendix C has no key for any of them and
// §11.6's nomagic pass is satisfied by an explicit allowance on each.
const (
	// HostAutoCompactBuffer is the 13 000 tokens the host keeps below its effective window before
	// it auto-compacts.
	HostAutoCompactBuffer core.Tokens = 13_000 //nomagic:allow host constant, Qompack.md §2.5
	// HostMaxOutputCap caps how much of maxOutputTokens the host subtracts from the window.
	HostMaxOutputCap core.Tokens = 20_000 //nomagic:allow host constant, Qompack.md §2.5
	// HostDefaultContextWindow is the 200K model §2.5's worked example assumes.
	HostDefaultContextWindow core.Tokens = 200_000 //nomagic:allow host default, Qompack.md §2.5
	// HostDefaultMaxOutput is the output budget §2.5's worked example assumes.
	HostDefaultMaxOutput core.Tokens = 32_000 //nomagic:allow host default, Qompack.md §2.5
)

// EffectiveWindow is §2.5 line 1. Non-positive inputs return 0, which makes Evaluate
// short-circuit with error_no_window rather than invent a window.
func EffectiveWindow(contextWindow, maxOutputTokens core.Tokens) core.Tokens {
	if contextWindow <= 0 {
		return 0
	}
	outputCap := maxOutputTokens
	if outputCap < 0 {
		outputCap = 0
	}
	if outputCap > HostMaxOutputCap {
		outputCap = HostMaxOutputCap
	}
	if ew := contextWindow - outputCap; ew > 0 {
		return ew
	}
	return 0
}

// SoftFloor is Qompack.md §8.4: "default 55% of effective window". Exported so /qompack:status
// (SP-14) and eval (SP-02) render the same number Evaluate used.
func SoftFloor(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens {
	if effectiveWindow <= 0 || cfg.SoftFloorPct <= 0 {
		return 0
	}
	return core.Tokens(cfg.SoftFloorPct * float64(effectiveWindow))
}

// HardCeiling is Qompack.md §8.4: "one turn's worth of headroom below Claude Code's
// threshold". The host threshold is effectiveWindow − HostAutoCompactBuffer (§2.5); the
// headroom is scheduler.hardCeilingMargin (Appendix C default 20000).
// Clamped to [1, effectiveWindow] so a pathological config can never invert the ordering.
func HardCeiling(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens {
	if effectiveWindow <= 0 {
		return 0
	}
	hc := effectiveWindow - HostAutoCompactBuffer - core.Tokens(cfg.HardCeilingMargin)
	if hc < 1 {
		hc = 1
	}
	if hc > effectiveWindow {
		hc = effectiveWindow
	}
	return hc
}

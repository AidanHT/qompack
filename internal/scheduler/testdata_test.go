package scheduler

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// This file is the shared fixture the plan's test tables start from (plans/V4-SP-12-scheduler-l3.md,
// "Shared fixtures"). It is a _test.go file, so the nomagic literal sets do not apply here.

// baseNow is the fixed "now" every fixture is anchored on.
const baseNow core.UnixMilli = 1_700_000_000_000

// baseCfg is config.Defaults().Scheduler — Appendix C verbatim: softFloorPct 0.55,
// hardCeilingMargin 20000, youngDaly{enabled:true, measuredDeltaSeconds:nil},
// changepoint{hazardRate:0.004, features:[paths,tools,time,todos]}, cache{0.1, 1.25, 300},
// idle{120, true, true}.
func baseCfg() config.SchedulerCfg { return config.Defaults().Scheduler }

// knownFiveMinuteRegime pins the regime FORCE_PROMPT_CACHING_5M=1 resolves to: both TTL bounds at
// Appendix C's floor and the multipliers Appendix C states. Every fixture starts here so the plan's
// worked examples hold bit-for-bit; unknown-regime cases set in.Regime = CacheRegime{} explicitly.
func knownFiveMinuteRegime(cfg config.SchedulerCfg) CacheRegime {
	return CacheRegime{
		TTLMinSeconds:   cfg.Cache.TTLSeconds,
		TTLMaxSeconds:   cfg.Cache.TTLSeconds,
		ReadMultiplier:  cfg.Cache.ReadMultiplier,
		WriteMultiplier: cfg.Cache.WriteMultiplier,
		Source:          "force_5m",
	}
}

// baseInputs is the plan's warm, above-soft-floor, no-changepoint, δ-unmeasured scenario. Every
// Pos is strictly below ContextTokens. Warm scores (r 0.1, w 1.25, λ 0.4, cacheFactor 1):
// Pos 40 000 → −97 084; Pos 80 000 → −48 833.6; Pos 118 000 → −1 916.8.
// Cold scores (cacheFactor 0): 2 916; 1 166.4; 583.2.
func baseInputs() Inputs {
	cfg := baseCfg()
	return Inputs{
		Now:                  baseNow,
		ContextTokens:        120_000,
		EffectiveWindow:      180_000,
		MaxOutputTokens:      32_000,
		LastAPICallTS:        baseNow - 10_000,
		LastCompactionTS:     baseNow - 600_000,
		BurnRateTokensPerMin: 900,
		CouplingLambda:       0.4,
		Candidates: []Candidate{
			{Pos: 40_000, Turn: 10, SegmentID: 1, RoundBoundary: true, ReclaimableTokens: 30_000, Coupling: 210},
			{Pos: 80_000, Turn: 20, SegmentID: 2, RoundBoundary: true, ReclaimableTokens: 12_000, Coupling: 84},
			{Pos: 118_000, Turn: 33, SegmentID: 3, RoundBoundary: true, ReclaimableTokens: 6_000, Coupling: 42},
		},
		Cfg:                     cfg,
		Regime:                  knownFiveMinuteRegime(cfg),
		ExpiringTriggerFraction: 0.8,
		AssumeMaxTTLSeconds:     3600,
	}
}

// ptr is the *float64 helper the δ-precedence tests use.
func ptr(v float64) *float64 { return &v }

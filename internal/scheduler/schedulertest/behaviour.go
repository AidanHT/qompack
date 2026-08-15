package schedulertest

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the schedulertest suite (00-ARCHITECTURE.md
// §5.22 table): the composite trigger truth table and a consistency check that a Decision's
// YoungDalySeconds tracks scheduler.YoungDaly. Both work directly against the package-level pure
// scheduler.Evaluate rather than through the factory-supplied Runtime, because Inputs is the
// white-box, directly-constructible value the composite trigger is specified over (Qompack.md
// §8.4); a black-box Runtime has no way to inject a precise Inputs snapshot. They are authored
// now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-12 inherits them
// rather than writing its own grader.
//
// Fixture note for SP-12: Inputs has no explicit "time of last checkpoint" or "M" (mean time
// between forced compactions) field. This file assumes LastCacheWriteTS doubles as the time of
// the last significant rewrite for the Young-Daly clause, and derives M from the remaining
// headroom to the hard ceiling divided by BurnRateTokensPerMin, matching the prose of Qompack.md
// §6.7 ("M is expected time to forced compaction at the current burn rate"). If SP-12's real
// Evaluate derives either differently, adjust assumedMTBFSeconds and the Young-Daly fixture below
// rather than the truth table's other cases, which do not depend on this assumption.

// The base scenario every truth-table case starts from and perturbs. Values are deliberately
// off the nomagic forbidden-literal set (00-ARCHITECTURE.md §11.6) since this is not a _test.go
// file.
const (
	baseNowMillis          = 1_700_000_000_000
	effectiveWindowTokens  = 200_000
	quietContextTokens     = 150_000
	aboveCeilingTokens     = 190_000
	belowFloorTokens       = 50_000
	softFloorPct           = 0.6
	hardCeilingMargin      = 15_000
	idleDetectAfterSeconds = 90
	cacheTTLSeconds        = 250
	cacheReadMultiplier    = 0.2
	cacheWriteMultiplier   = 1.5
	youngDalyDeltaSeconds  = 30.0
	burnRateTokensPerMin   = 500.0
	idleGapSeconds         = 400
	secondsPerMinute       = 60.0
	millisPerSecond        = 1000
)

// baseInputs returns the "quiet" scenario: above the soft floor, below the hard ceiling, no
// changepoint, a cache write and an API call that both just happened. Every truth-table case
// copies this and perturbs exactly the field(s) relevant to the condition under test.
func baseInputs() scheduler.Inputs {
	var in scheduler.Inputs
	in.Now = core.UnixMilli(baseNowMillis)
	in.EffectiveWindow = core.Tokens(effectiveWindowTokens)
	in.ContextTokens = core.Tokens(quietContextTokens)
	in.LastAPICallTS = in.Now
	in.LastCacheWriteTS = in.Now
	in.Cfg.SoftFloorPct = softFloorPct
	in.Cfg.HardCeilingMargin = hardCeilingMargin
	in.Cfg.Idle.DetectAfterSeconds = idleDetectAfterSeconds
	in.Cfg.Idle.DeepCutWhenCold = true
	in.Cfg.Idle.BackgroundWork = true
	in.Cfg.Cache.TTLSeconds = cacheTTLSeconds
	in.Cfg.Cache.ReadMultiplier = cacheReadMultiplier
	in.Cfg.Cache.WriteMultiplier = cacheWriteMultiplier
	in.Cfg.YoungDaly.Enabled = true
	delta := youngDalyDeltaSeconds
	in.MeasuredDeltaSeconds = &delta
	in.BurnRateTokensPerMin = burnRateTokensPerMin
	return in
}

// assumedMTBFSeconds derives M, the expected time to a forced compaction at the current burn
// rate, from the remaining token headroom to the hard ceiling. See the fixture note above this
// file's imports for why this is an assumption SP-12 may need to adjust.
func assumedMTBFSeconds(in scheduler.Inputs) float64 {
	headroom := float64(in.EffectiveWindow) - float64(in.Cfg.HardCeilingMargin) - float64(in.ContextTokens)
	if in.BurnRateTokensPerMin <= 0 || headroom <= 0 {
		return 0
	}
	return headroom / (in.BurnRateTokensPerMin / secondsPerMinute)
}

// youngDalyIntervalSeconds is the Young-Daly interval implied by in, under the assumedMTBFSeconds
// derivation above.
func youngDalyIntervalSeconds(in scheduler.Inputs) float64 {
	if in.MeasuredDeltaSeconds == nil {
		return 0
	}
	return scheduler.YoungDaly(*in.MeasuredDeltaSeconds, assumedMTBFSeconds(in))
}

// runYoungDalyFormulaCase asserts that a Decision's YoungDalySeconds is derived from the same
// scheduler.YoungDaly formula this package ships for real, rather than a second, drifting copy
// of it — an integration check that only means something once Evaluate is real.
func runYoungDalyFormulaCase(t *testing.T) {
	t.Helper()
	in := baseInputs()
	want := youngDalyIntervalSeconds(in)
	got := scheduler.Evaluate(in)
	require.InDelta(t, want, got.YoungDalySeconds, 1e-6,
		"Decision.YoungDalySeconds must equal scheduler.YoungDaly(delta, M), not a re-derived value")
}

// runCompositeTriggerTruthTable exercises Qompack.md §8.4's composite trigger, one condition at a
// time, against scheduler.Evaluate:
//
//	should_compact = tokens > soft_floor AND (at_changepoint OR elapsed > young_daly_interval
//	                                           OR tokens > hard_ceiling OR idle_gap > ttl)
//
// Each "fires" case only asserts that ShouldCompact becomes true and the expected TriggerReason
// is present — not that it is the only one present — since several of the four OR-clauses can
// legitimately become true together as elapsed time grows. The "quiet" and "below_floor" cases
// assert the negative space precisely, because those are unambiguous from Qompack.md §8.4 alone.
func runCompositeTriggerTruthTable(t *testing.T) {
	t.Helper()

	t.Run("quiet_baseline_does_not_compact", func(t *testing.T) {
		got := scheduler.Evaluate(baseInputs())
		require.False(t, got.ShouldCompact)
		require.Contains(t, got.Reasons, scheduler.TriggerSoftFloor,
			"tokens are above the floor, so the gate condition itself should be reported")
		require.NotContains(t, got.Reasons, scheduler.TriggerChangepoint)
		require.NotContains(t, got.Reasons, scheduler.TriggerHardCeiling)
		require.NotContains(t, got.Reasons, scheduler.TriggerYoungDaly)
		require.NotContains(t, got.Reasons, scheduler.TriggerIdleColdCache)
	})

	t.Run("below_soft_floor_suppresses_every_other_trigger", func(t *testing.T) {
		in := baseInputs()
		in.ContextTokens = core.Tokens(belowFloorTokens)
		in.Changepoint = scheduler.ChangepointState{AtChangepoint: true, ProbChangepoint: 1}
		got := scheduler.Evaluate(in)
		require.False(t, got.ShouldCompact,
			"the AND-gate must block compaction even when a disjunct is true")
		require.NotContains(t, got.Reasons, scheduler.TriggerSoftFloor,
			"tokens are below the floor, so the gate condition itself is false")
	})

	t.Run("changepoint_fires", func(t *testing.T) {
		in := baseInputs()
		in.Changepoint = scheduler.ChangepointState{AtChangepoint: true, ProbChangepoint: 1}
		got := scheduler.Evaluate(in)
		require.True(t, got.ShouldCompact)
		require.Contains(t, got.Reasons, scheduler.TriggerChangepoint)
	})

	t.Run("hard_ceiling_fires", func(t *testing.T) {
		in := baseInputs()
		in.ContextTokens = core.Tokens(aboveCeilingTokens)
		got := scheduler.Evaluate(in)
		require.True(t, got.ShouldCompact)
		require.Contains(t, got.Reasons, scheduler.TriggerHardCeiling)
	})

	t.Run("young_daly_fires", func(t *testing.T) {
		in := baseInputs()
		interval := youngDalyIntervalSeconds(in)
		elapsedMS := int64(interval*2*millisPerSecond) + millisPerSecond
		in.LastCacheWriteTS = in.Now - core.UnixMilli(elapsedMS)
		got := scheduler.Evaluate(in)
		require.True(t, got.ShouldCompact)
		require.Contains(t, got.Reasons, scheduler.TriggerYoungDaly)
	})

	t.Run("idle_cold_cache_fires", func(t *testing.T) {
		in := baseInputs()
		in.LastAPICallTS = in.Now - core.UnixMilli(idleGapSeconds*millisPerSecond)
		got := scheduler.Evaluate(in)
		require.True(t, got.ShouldCompact)
		require.Contains(t, got.Reasons, scheduler.TriggerIdleColdCache)
	})
}

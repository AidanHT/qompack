package scheduler

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestYoungDaly_Formula pins YoungDaly to Appendix A's I* = √(2·δ·M): the plan's δ=20, M=1800
// anchor plus the assertions carried over verbatim from SP-01's formulas_test.go.
func TestYoungDaly_Formula(t *testing.T) {
	require.InDelta(t, 268.3281572999748, YoungDaly(20, 1800), 1e-9)
	require.InDelta(t, math.Sqrt(2*20*1800), YoungDaly(20, 1800), 1e-9)

	require.InDelta(t, math.Sqrt(2*30*600), YoungDaly(30, 600), 1e-9)
	require.Equal(t, 0.0, YoungDaly(0, 600), "delta<=0 must report 0, not NaN")
	require.Equal(t, 0.0, YoungDaly(-1, 5), "delta<=0 must report 0, not NaN")
	require.Equal(t, 0.0, YoungDaly(30, 0), "mtbf<=0 must report 0, not NaN")
	require.Equal(t, 0.0, YoungDaly(30, -5), "mtbf<=0 must report 0, not NaN")
}

// TestYoungDaly_NonPositive: every degenerate input reports 0, and 0 means "no cadence
// constraint". The NaN/Inf rows are the declared behaviour change over the shipped body.
func TestYoungDaly_NonPositive(t *testing.T) {
	cases := []struct {
		name        string
		delta, mtbf float64
	}{
		{"zero delta", 0, 1800},
		{"zero mtbf", 20, 0},
		{"negative delta", -1, 5},
		{"NaN delta", math.NaN(), 5},
		{"+Inf delta", math.Inf(1), 5},
		{"-Inf delta", math.Inf(-1), 5},
		{"NaN mtbf", 20, math.NaN()},
		{"+Inf mtbf", 20, math.Inf(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := YoungDaly(tc.delta, tc.mtbf)
			require.False(t, math.IsNaN(got), "a NaN interval would silently disable the clause")
			require.Equal(t, 0.0, got)
		})
	}
}

// TestMTBF_FromBurnRate is the plan's worked example: headroom 27 000 at 900 tokens/min is
// 30 minutes, i.e. 1 800 s.
func TestMTBF_FromBurnRate(t *testing.T) {
	require.Equal(t, 1800.0, mtbfSeconds(120_000, 147_000, 900))
	require.Equal(t, 3000.0, mtbfSeconds(120_000, 165_000, 900), "M tracks the ceiling, not a constant")
	require.InDelta(t, 268.3281572999748, YoungDaly(20, mtbfSeconds(120_000, 147_000, 900)), 1e-9)
}

func TestMTBF_ZeroWhenAtOrAboveCeiling(t *testing.T) {
	require.Equal(t, 0.0, mtbfSeconds(150_000, 147_000, 900))
	require.Equal(t, 0.0, mtbfSeconds(100_000, 90_000, 900))
	require.Equal(t, 0.0, mtbfSeconds(147_000, 147_000, 900), "at the ceiling there is no headroom left")
}

func TestMTBF_ZeroWhenBurnUnknown(t *testing.T) {
	require.Equal(t, 0.0, mtbfSeconds(120_000, 147_000, 0))
	require.Equal(t, 0.0, mtbfSeconds(120_000, 147_000, -900))
}

func TestResolveDelta_ConfigOverridesRuntime(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(12)
	cfg := baseCfg()
	cfg.YoungDaly.MeasuredDeltaSeconds = ptr(30)

	delta, ok := resolveDelta(in, cfg)
	require.True(t, ok)
	require.Equal(t, 30.0, delta)
}

func TestResolveDelta_RuntimeUsedWhenConfigNil(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(12)
	cfg := baseCfg()
	cfg.YoungDaly.MeasuredDeltaSeconds = nil

	delta, ok := resolveDelta(in, cfg)
	require.True(t, ok)
	require.Equal(t, 12.0, delta)

	// A non-positive config override is not a measurement either; the runtime's value stands.
	cfg.YoungDaly.MeasuredDeltaSeconds = ptr(0)
	delta, ok = resolveDelta(in, cfg)
	require.True(t, ok)
	require.Equal(t, 12.0, delta)
}

// TestResolveDelta_NilMeansMeasureNotZero: Appendix C's `null` is "measure at runtime", never a
// zero δ. Both nil ⇒ unknown, and the pure-function chain can never turn that into a positive
// interval. (Evaluate's Breakdown["young_daly_delta_unmeasured"] = 1 and its refusal to fire
// TriggerYoungDaly are asserted where Evaluate is real, in the composite-trigger tests.)
func TestResolveDelta_NilMeansMeasureNotZero(t *testing.T) {
	in := baseInputs()
	require.Nil(t, in.MeasuredDeltaSeconds, "the fixture is δ-unmeasured")
	cfg := baseCfg()
	require.Nil(t, cfg.YoungDaly.MeasuredDeltaSeconds, "Appendix C ships null")

	delta, ok := resolveDelta(in, cfg)
	require.False(t, ok)
	require.Equal(t, 0.0, delta)
	require.Equal(t, 0.0, YoungDaly(delta, mtbfSeconds(120_000, 147_000, 900)), "an unmeasured δ yields a disabled clause, not an always-elapsed one")

	// A pointer to a non-positive value is likewise "unknown", not "zero interval".
	in.MeasuredDeltaSeconds = ptr(0)
	cfg.YoungDaly.MeasuredDeltaSeconds = ptr(-1)
	delta, ok = resolveDelta(in, cfg)
	require.False(t, ok)
	require.Equal(t, 0.0, delta)
}

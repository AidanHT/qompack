package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
)

// TestEffectiveWindow_Section25Arithmetic pins EffectiveWindow to Qompack.md §2.5:
// effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000). The first row is §2.5's
// own worked example ("For a 200K model: effective ≈ 180K").
func TestEffectiveWindow_Section25Arithmetic(t *testing.T) {
	cases := []struct {
		name           string
		window, maxOut core.Tokens
		want           core.Tokens
	}{
		{"cap binds at 20k", 200_000, 32_000, 180_000},
		{"output below the cap", 200_000, 8_000, 192_000},
		{"zero output budget", 200_000, 0, 200_000},
		{"zero window", 0, 8_000, 0},
		{"window smaller than the cap is never negative", 10_000, 64_000, 0},
		{"negative output budget is treated as zero", 200_000, -5, 200_000},
		{"negative window", -1, 8_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, EffectiveWindow(tc.window, tc.maxOut))
		})
	}
}

// TestSoftFloor_55PctOfEffectiveWindow: Qompack.md §8.4 "default 55% of effective window".
func TestSoftFloor_55PctOfEffectiveWindow(t *testing.T) {
	require.Equal(t, core.Tokens(99_000), SoftFloor(180_000, baseCfg()))
}

// TestHardCeiling_OneTurnBelowHostThreshold: the host's own auto-compact threshold for a 200K
// model is 167 000 (§2.5); the plugin's hard ceiling sits hardCeilingMargin below it.
func TestHardCeiling_OneTurnBelowHostThreshold(t *testing.T) {
	cfg := baseCfg()
	hc := HardCeiling(180_000, cfg)
	require.Equal(t, core.Tokens(147_000), hc, "180 000 − 13 000 − 20 000")
	hostThreshold := core.Tokens(180_000) - HostAutoCompactBuffer
	require.Equal(t, core.Tokens(167_000), hostThreshold)
	require.Less(t, hc, hostThreshold, "the plugin must act before the host does")
	require.Equal(t, core.Tokens(cfg.HardCeilingMargin), hostThreshold-hc, "the headroom is exactly the configured margin")
}

// TestHardCeiling_ClampedWhenMarginExceedsWindow: a pathological margin can never push the
// ceiling to or below zero.
func TestHardCeiling_ClampedWhenMarginExceedsWindow(t *testing.T) {
	cfg := baseCfg()
	cfg.HardCeilingMargin = 500_000
	require.Equal(t, core.Tokens(1), HardCeiling(180_000, cfg))

	cfg.HardCeilingMargin = -1_000_000
	require.Equal(t, core.Tokens(180_000), HardCeiling(180_000, cfg), "a negative margin cannot lift the ceiling above the window")
}

// TestThresholds_ZeroWindow: no window ⇒ both thresholds are 0 so Evaluate can short-circuit.
func TestThresholds_ZeroWindow(t *testing.T) {
	cfg := baseCfg()
	require.Equal(t, core.Tokens(0), SoftFloor(0, cfg))
	require.Equal(t, core.Tokens(0), HardCeiling(0, cfg))
	require.Equal(t, core.Tokens(0), SoftFloor(-1, cfg))
	require.Equal(t, core.Tokens(0), HardCeiling(-1, cfg))

	cfg.SoftFloorPct = 0
	require.Equal(t, core.Tokens(0), SoftFloor(180_000, cfg), "a non-positive pct is a disabled floor, not a negative one")
}

// TestSoftFloorBelowHardCeiling_Property asserts the §12 "plugin acts first by design" ordering
// cannot invert. SoftFloor = pct·window and HardCeiling = window − 13 000 − margin, so the
// ordering holds exactly when (1 − pct)·window exceeds 13 000 + margin; the plan's weaker
// precondition (window > 13 000 + margin + 1) only guarantees the ceiling's clamp never engages.
func TestSoftFloorBelowHardCeiling_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		pct := rapid.Float64Range(0.001, 0.999).Draw(rt, "softFloorPct")
		margin := rapid.IntRange(1, 50_000).Draw(rt, "margin")
		window := core.Tokens(rapid.IntRange(50_000, 1_000_000).Draw(rt, "window"))

		cfg := baseCfg()
		cfg.SoftFloorPct = pct
		cfg.HardCeilingMargin = margin

		sf := SoftFloor(window, cfg)
		hc := HardCeiling(window, cfg)

		require.GreaterOrEqual(rt, sf, core.Tokens(0))
		require.LessOrEqual(rt, sf, window)
		require.GreaterOrEqual(rt, hc, core.Tokens(1))
		require.LessOrEqual(rt, hc, window)

		buffer := float64(HostAutoCompactBuffer) + float64(margin)
		if float64(window) > buffer+1 {
			require.Equal(rt, window-HostAutoCompactBuffer-core.Tokens(margin), hc, "the clamp must not engage")
		}
		if (1-pct)*float64(window) > buffer+1 {
			require.Less(rt, sf, hc, "soft floor %d must stay below hard ceiling %d (pct %v, margin %d, window %d)", sf, hc, pct, margin, window)
		}
	})
}

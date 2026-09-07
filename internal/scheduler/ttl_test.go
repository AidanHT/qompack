package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
)

// anchorSecondsAgo places the TTL anchor gap seconds before baseNow.
func anchorSecondsAgo(gap int) core.UnixMilli {
	return baseNow - core.UnixMilli(gap)*1000
}

// TestClassifyTTL_Table is the shipped table under a KNOWN five-minute regime: both bounds
// collapse onto 300 s, so the classifier is bit-identical to the pre-regime version.
func TestClassifyTTL_Table(t *testing.T) {
	reg := knownFiveMinuteRegime(baseCfg())
	require.Equal(t, reg.TTLMinSeconds, reg.TTLMaxSeconds, "the fixture regime must be known")

	cases := []struct {
		gap  int
		want TTLState
	}{
		{0, TTLWarm},
		{10, TTLWarm},
		{149, TTLWarm},
		{150, TTLExpiring},
		{224, TTLExpiring},
		{299, TTLExpiring},
		{300, TTLCold},
		{3600, TTLCold},
	}
	for _, tc := range cases {
		state, gap := ClassifyTTL(baseNow, anchorSecondsAgo(tc.gap), reg, false)
		require.Equal(t, tc.want, state, "gap %d s", tc.gap)
		require.Equal(t, float64(tc.gap), gap, "gap %d s must be reported exactly", tc.gap)
	}
}

// TestClassifyTTL_UnknownRegimeIsNotColdAt400s is the row that stops the 40× mis-cut: a
// 400-second gap proves nothing when the TTL might be an hour.
func TestClassifyTTL_UnknownRegimeIsNotColdAt400s(t *testing.T) {
	state, gap := ClassifyTTL(baseNow, anchorSecondsAgo(400), unknownTestRegime(), false)
	require.Equal(t, TTLExpiring, state)
	require.NotEqual(t, TTLCold, state)
	require.Equal(t, 400.0, gap)

	// The warm→expiring edge still keys off the LOWER bound, so warmth stops being trusted
	// exactly where it used to.
	state, _ = ClassifyTTL(baseNow, anchorSecondsAgo(10), unknownTestRegime(), false)
	require.Equal(t, TTLWarm, state)
	state, _ = ClassifyTTL(baseNow, anchorSecondsAgo(149), unknownTestRegime(), false)
	require.Equal(t, TTLWarm, state)
	state, _ = ClassifyTTL(baseNow, anchorSecondsAgo(150), unknownTestRegime(), false)
	require.Equal(t, TTLExpiring, state)
}

// TestClassifyTTL_UnknownRegimeColdAtMax: "provably cold" means dead under EVERY regime in the
// range, so the cold edge keys off the upper bound.
func TestClassifyTTL_UnknownRegimeColdAtMax(t *testing.T) {
	state, _ := ClassifyTTL(baseNow, anchorSecondsAgo(3599), unknownTestRegime(), false)
	require.Equal(t, TTLExpiring, state)
	state, gap := ClassifyTTL(baseNow, anchorSecondsAgo(3600), unknownTestRegime(), false)
	require.Equal(t, TTLCold, state)
	require.Equal(t, 3600.0, gap)
}

// TestClassifyTTL_EffortChangeIsColdAtAnyGap: effort is part of the cache key, so the prefix is
// gone rather than aging, and no wall-clock gap can reveal it.
func TestClassifyTTL_EffortChangeIsColdAtAnyGap(t *testing.T) {
	for _, reg := range []CacheRegime{knownFiveMinuteRegime(baseCfg()), unknownTestRegime()} {
		state, gap := ClassifyTTL(baseNow, anchorSecondsAgo(0), reg, true)
		require.Equal(t, TTLCold, state, reg.Source)
		require.Equal(t, 0.0, gap, reg.Source)

		state, gap = ClassifyTTL(baseNow, anchorSecondsAgo(10), reg, true)
		require.Equal(t, TTLCold, state, reg.Source)
		require.Equal(t, 10.0, gap, "the gap is still reported for Breakdown")
	}
}

// TestClassifyTTL_UnknownWhenNoAPICall: with no anchor there is no prefix to classify; with no
// TTL (a disabled cache) there is no cache to be warm or cold.
func TestClassifyTTL_UnknownWhenNoAPICall(t *testing.T) {
	state, gap := ClassifyTTL(baseNow, 0, knownFiveMinuteRegime(baseCfg()), false)
	require.Equal(t, TTLUnknown, state)
	require.Equal(t, 0.0, gap)

	state, gap = ClassifyTTL(baseNow, 0, knownFiveMinuteRegime(baseCfg()), true)
	require.Equal(t, TTLUnknown, state, "an effort change with no prior request has nothing to invalidate")
	require.Equal(t, 0.0, gap)

	disabled := CacheRegime{ReadMultiplier: 1, WriteMultiplier: 1, Disabled: true, Source: "disabled"}
	state, gap = ClassifyTTL(baseNow, anchorSecondsAgo(4000), disabled, false)
	require.Equal(t, TTLUnknown, state)
	require.Equal(t, 0.0, gap)
	require.Equal(t, 1.0, CacheFactor(state, gap, disabled), "no cache ⇒ every token of tail costs exactly one token")
}

// TestClassifyTTL_NegativeGapClamped: an anchor in the future (clock skew between hooks) reads as
// a zero gap, never as a negative one.
func TestClassifyTTL_NegativeGapClamped(t *testing.T) {
	state, gap := ClassifyTTL(baseNow, baseNow+5_000, knownFiveMinuteRegime(baseCfg()), false)
	require.Equal(t, TTLWarm, state)
	require.Equal(t, 0.0, gap)
}

// TestTTLAnchorIsNeverLaterThanStop is the one-sidedness property of the request-start anchor:
// for any turn with UserPromptSubmit ≤ PostToolUse ≤ Stop, and whichever of the two request
// starts the observer managed to record, the resolved anchor is never later than Stop — so the
// measured gap can only grow, and the classifier can only become more conservative about warmth.
func TestTTLAnchorIsNeverLaterThanStop(t *testing.T) {
	reg := knownFiveMinuteRegime(baseCfg())
	rapid.Check(t, func(rt *rapid.T) {
		prompt := core.UnixMilli(rapid.Int64Range(1, 1<<40).Draw(rt, "promptTS"))
		tool := prompt + core.UnixMilli(rapid.Int64Range(0, 1<<22).Draw(rt, "toolDelay"))
		stop := tool + core.UnixMilli(rapid.Int64Range(0, 1<<22).Draw(rt, "stopDelay"))
		havePrompt := rapid.Bool().Draw(rt, "havePrompt")
		haveTool := rapid.Bool().Draw(rt, "haveTool")

		// The observer records the LAST request start it saw: PostToolUse when the turn used
		// tools, UserPromptSubmit otherwise, nothing when it saw neither (a pre-field session).
		var requestStart core.UnixMilli
		switch {
		case haveTool:
			requestStart = tool
		case havePrompt:
			requestStart = prompt
		}
		lastAPICall := stop // Stop is the last hook of the turn and is what LastAPICallTS holds.

		anchor := resolveTTLAnchor(requestStart, lastAPICall)
		require.Greater(rt, anchor, core.UnixMilli(0))
		require.LessOrEqual(rt, anchor, stop)
		if requestStart > 0 {
			require.Equal(rt, requestStart, anchor, "a recorded request start always wins")
		} else {
			require.Equal(rt, lastAPICall, anchor, "no request start ⇒ exactly today's behaviour")
		}

		now := stop + core.UnixMilli(rapid.Int64Range(0, 1<<22).Draw(rt, "idle"))
		_, gapFromAnchor := ClassifyTTL(now, anchor, reg, false)
		_, gapFromStop := ClassifyTTL(now, stop, reg, false)
		require.GreaterOrEqual(rt, gapFromAnchor, gapFromStop, "the anchor can only make the gap larger")
	})
}

// TestCacheFactor_Ramp pins the worked examples: the known regime's numbers are the shipped ones
// and do not move; the unknown regime stretches the same ramp across [150, 3600].
func TestCacheFactor_Ramp(t *testing.T) {
	known := knownFiveMinuteRegime(baseCfg())
	unknown := unknownTestRegime()

	cases := []struct {
		name  string
		state TTLState
		gap   float64
		reg   CacheRegime
		want  float64
	}{
		{"warm", TTLWarm, 10, known, 1.0},
		{"expiring at the edge", TTLExpiring, 150, known, 1.0},
		{"expiring midway", TTLExpiring, 225, known, 0.5},
		{"expiring near cold", TTLExpiring, 299, known, 1.0 / 150.0},
		{"cold", TTLCold, 300, known, 0.0},
		{"cold after hours", TTLCold, 14_400, known, 0.0},
		{"unknown state assumes warm", TTLUnknown, 0, known, 1.0},
		{"unknown regime at 400 s", TTLExpiring, 400, unknown, (3600.0 - 400.0) / (3600.0 - 150.0)},
		{"unknown regime at 3600 s", TTLExpiring, 3600, unknown, 0.0},
		{"unknown regime cold", TTLCold, 3600, unknown, 0.0},
		{"unknown regime at the edge", TTLExpiring, 150, unknown, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, tc.want, CacheFactor(tc.state, tc.gap, tc.reg), 1e-6)
		})
	}
	require.InDelta(t, 0.00667, CacheFactor(TTLExpiring, 299, known), 1e-5)
	require.InDelta(t, 0.927536, CacheFactor(TTLExpiring, 400, unknown), 1e-6)

	// The ramp is clamped at both ends and continuous with its neighbours.
	require.Equal(t, 1.0, CacheFactor(TTLExpiring, 0, known), "below the band clamps to 1")
	require.Equal(t, 0.0, CacheFactor(TTLExpiring, 10_000, known), "past the band clamps to 0")
	require.Equal(t, 1.0, CacheFactor(TTLExpiring, 200, CacheRegime{}), "no TTL ⇒ 1")
	require.Equal(t, 1.0, CacheFactor(TTLCold, 200, CacheRegime{Disabled: true, ReadMultiplier: 1, WriteMultiplier: 1}), "a disabled cache has no cold state to exploit")

	// Composition with the classifier reproduces the worked example end to end.
	for _, gap := range []int{10, 150, 225, 299, 300} {
		state, g := ClassifyTTL(baseNow, anchorSecondsAgo(gap), known, false)
		direct := CacheFactor(state, g, known)
		switch gap {
		case 10:
			require.Equal(t, 1.0, direct)
		case 150:
			require.Equal(t, 1.0, direct)
		case 225:
			require.Equal(t, 0.5, direct)
		case 299:
			require.InDelta(t, 1.0/150.0, direct, 1e-9)
		case 300:
			require.Equal(t, 0.0, direct)
		}
	}
}

// TestCacheFactor_MonotoneDecreasing_Property: over both regimes, the classified factor is
// non-increasing in the gap and always in [0,1].
func TestCacheFactor_MonotoneDecreasing_Property(t *testing.T) {
	regimes := []CacheRegime{knownFiveMinuteRegime(baseCfg()), unknownTestRegime()}
	rapid.Check(t, func(rt *rapid.T) {
		reg := regimes[rapid.IntRange(0, len(regimes)-1).Draw(rt, "regime")]
		limit := 2 * reg.TTLMaxSeconds
		g1 := rapid.IntRange(0, limit).Draw(rt, "gap1")
		g2 := rapid.IntRange(g1, limit).Draw(rt, "gap2")

		classified := func(gap int) float64 {
			state, g := ClassifyTTL(baseNow, anchorSecondsAgo(gap), reg, false)
			require.Equal(rt, float64(gap), g)
			f := CacheFactor(state, g, reg)
			require.GreaterOrEqual(rt, f, 0.0)
			require.LessOrEqual(rt, f, 1.0)
			return f
		}
		require.LessOrEqual(rt, classified(g2), classified(g1), "factor must not increase from gap %d to %d (%s)", g1, g2, reg.Source)

		// The raw ramp is monotone on its own too, independent of the classifier's edges.
		f1 := CacheFactor(TTLExpiring, float64(g1), reg)
		f2 := CacheFactor(TTLExpiring, float64(g2), reg)
		require.LessOrEqual(rt, f2, f1)
		require.GreaterOrEqual(rt, f2, 0.0)
		require.LessOrEqual(rt, f1, 1.0)
	})
}

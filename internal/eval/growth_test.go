package eval_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// growthFixturePath resolves one of this package's W-2 provider-seam fixtures.
//
// They sat under testdata/golden/contracts/{store,negknow}/ until V2-MERGE-18. Those directories
// belong to SP-06 and SP-09, neither package's MANIFEST.json declared these two files, and SP-06's
// gen-contract-fixtures regenerates one directory while SP-09 records into the other in wave 3 —
// so an undeclared neighbour in either was one -update run away from being deleted without
// comment. testdata/golden/eval/ is SP-02's own, alongside the divergence fixtures.
func growthFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "golden", "eval", "growth", name)
}

// growthFixture is the W-2 contract fixture SP-06 later replaces with a real store.Stats walk.
// Until then it is the shape contract, and the wave-2 verification re-runs this same check
// against the real store.
func growthFixture(t *testing.T) []eval.StatsSample {
	t.Helper()
	raw, err := os.ReadFile(growthFixturePath(t, "stats-growth.json"))
	require.NoError(t, err)
	var out []eval.StatsSample
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// linearSamples is the case the guardrail exists to catch: dedup achieving nothing, so stored
// bytes track raw bytes one for one.
func linearSamples(n int) []eval.StatsSample {
	out := make([]eval.StatsSample, n)
	raw := int64(100_000)
	for i := range out {
		out[i] = eval.StatsSample{
			Turn: core.TurnIndex(25 * (i + 1)), Objects: 10 * (i + 1),
			Bytes: raw, RawBytes: raw, DedupRatio: 1,
		}
		raw *= 2
	}
	return out
}

// TestCheckSublinearGrowth_Sublinear: the committed fixture is an exact 0.62 power law, so the fit
// has a known answer rather than a plausible one.
func TestCheckSublinearGrowth_Sublinear(t *testing.T) {
	got := eval.CheckSublinearGrowth(growthFixture(t))

	require.InDelta(t, 0.62, got.Exponent, 0.02)
	require.True(t, got.Sublinear)
	require.Equal(t, 8, got.Samples)
	require.InDelta(t, 128.0, got.RawSpan, 0.01, "100K doubling seven times")
	require.Empty(t, got.Reason, "a conclusive verdict explains nothing")
}

// TestCheckSublinearGrowth_Linear: an exponent of 1 is exactly the failure §11.3 names.
func TestCheckSublinearGrowth_Linear(t *testing.T) {
	got := eval.CheckSublinearGrowth(linearSamples(8))

	require.InDelta(t, 1.0, got.Exponent, 0.01)
	require.False(t, got.Sublinear)
}

// TestCheckSublinearGrowth_TooFewSamples: an unmeasurable guardrail is not a passing guardrail, so
// too little data must refuse to judge rather than judge favourably.
func TestCheckSublinearGrowth_TooFewSamples(t *testing.T) {
	got := eval.CheckSublinearGrowth(linearSamples(3))

	require.False(t, got.Sublinear)
	require.Contains(t, got.Reason, "at least 6")
}

// TestCheckSublinearGrowth_SpanTooSmall: a fit over two doublings cannot distinguish an exponent
// of 0.9 from one of 1.0, so it must not pretend to.
func TestCheckSublinearGrowth_SpanTooSmall(t *testing.T) {
	samples := make([]eval.StatsSample, 8)
	raw := int64(1_000_000)
	for i := range samples {
		samples[i] = eval.StatsSample{
			Turn: core.TurnIndex(25 * (i + 1)), Objects: 10,
			Bytes: raw / 3, RawBytes: raw, DedupRatio: 3,
		}
		raw += 140_000 // 8 steps spans well under 8x
	}

	got := eval.CheckSublinearGrowth(samples)

	require.False(t, got.Sublinear)
	require.Contains(t, got.Reason, "8x")
}

// TestCheckSublinearGrowth_NonMonotoneRawBytes is what keeps the RawBytes-as-x-axis substitution
// honest rather than merely convenient: turn count is a bad regressor (one 40 MB test run and one
// 200-byte Grep are both "one turn"), and raw bytes is only a valid stand-in for session length
// while it actually grows with it.
func TestCheckSublinearGrowth_NonMonotoneRawBytes(t *testing.T) {
	samples := growthFixture(t)
	samples[5].RawBytes = samples[2].RawBytes // a later turn holding less content than an earlier one

	got := eval.CheckSublinearGrowth(samples)

	require.False(t, got.Sublinear)
	require.Contains(t, got.Reason, "monotone")
}

// TestCheckSublinearGrowth_DropsUnusableSamples: a zero-byte sample carries no logarithm, and
// silently fitting through it would move the exponent.
func TestCheckSublinearGrowth_DropsUnusableSamples(t *testing.T) {
	samples := append(growthFixture(t), eval.StatsSample{Turn: 225, Bytes: 0, RawBytes: 0})

	got := eval.CheckSublinearGrowth(samples)

	require.Equal(t, 8, got.Samples)
	require.True(t, got.Sublinear)
}

// TestCheckSublinearGrowth_Empty: no data at all is inconclusive, not sublinear.
func TestCheckSublinearGrowth_Empty(t *testing.T) {
	got := eval.CheckSublinearGrowth(nil)
	require.False(t, got.Sublinear)
	require.NotEmpty(t, got.Reason)
}

// TestSketchHealth_FixtureShape pins the §11.4 watch-for fixture SP-09 later replaces with real
// negknow health, so the provider seam has a shape contract before the provider exists.
func TestSketchHealth_FixtureShape(t *testing.T) {
	raw, err := os.ReadFile(growthFixturePath(t, "health.json"))
	require.NoError(t, err)

	var got eval.SketchHealth
	require.NoError(t, json.Unmarshal(raw, &got))

	require.InDelta(t, 0.18, got.FillRatio, 1e-9)
	require.InDelta(t, 0.006, got.EstFPRate, 1e-9)
	require.Less(t, got.EstFPRate, 0.10, "§11.4: at 10% the agent starts skipping viable approaches")
	require.Equal(t, got.Records, got.Active+got.Stale)
}

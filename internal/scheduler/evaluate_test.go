package scheduler

// Every test below that asserts P, PScore or a fired-only Breakdown term ("reclaimable",
// "rewrite", "distortion", "score", "p*", "coupling", "rewrite_tokens") first makes the decision
// FIRE, because Evaluate fills P only in its fired branch (the plan's `default:` case). Unless
// another clause already fires — the cold fixtures fire via idle_cold_cache, the 150 000-token
// fixtures via hard_ceiling — the tests set Changepoint.AtChangepoint = true (helper fire), which
// is the cheapest clause to turn on and changes no score.
//
// This is a _test.go file, so the nomagic literal sets do not apply here.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// goldenDir is testdata/golden/scheduler/ relative to this package directory, which is the
// working directory of the test binary.
const goldenDir = "../../testdata/golden/scheduler"

// fire turns the changepoint clause on, the cheapest way to make a warm, above-soft-floor
// fixture fire so that P is filled.
func fire(in Inputs) Inputs {
	in.Changepoint.AtChangepoint = true
	return in
}

// coldInputs is baseInputs with a 400-second idle gap under the known 5-minute regime: TTLCold,
// which fires idle_cold_cache on its own.
func coldInputs() Inputs {
	in := baseInputs()
	in.LastAPICallTS = baseNow - 400_000
	return in
}

// expiringInputs is baseInputs with a 250-second idle gap under the known 5-minute regime:
// TTLExpiring at 0.83·TTL, above the 0.8 fraction, so cache_expiring fires on its own.
func expiringInputs() Inputs {
	in := baseInputs()
	in.LastAPICallTS = baseNow - 250_000
	return in
}

// warmGoldenInputs is the fixture behind decision-warm.json: baseInputs, fired via the
// changepoint clause (the as-is fixture has no true disjunct and would never fill P).
func warmGoldenInputs() Inputs { return fire(baseInputs()) }

// ascendingCandidates returns n round-boundary candidates at Pos step·(i+1) with identical
// reclaimable and coupling, so every score ties and the retained set is visible through the
// tie-break.
func ascendingCandidates(n, step int) []Candidate {
	out := make([]Candidate, 0, n)
	for i := range n {
		out = append(out, Candidate{
			Pos:               step * (i + 1),
			Turn:              core.TurnIndex(i),
			SegmentID:         core.SegmentID(i),
			RoundBoundary:     true,
			ReclaimableTokens: 1_000,
			Coupling:          1,
		})
	}
	return out
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err, "golden %s must exist; it is hand-derived, never -update generated", name)
	return b
}

// deepCopyInputs clones every reference an Inputs carries so purity can be asserted with go-cmp.
func deepCopyInputs(in Inputs) Inputs {
	out := in
	out.Candidates = slices.Clone(in.Candidates)
	out.Changepoint.Posterior = slices.Clone(in.Changepoint.Posterior)
	if in.MeasuredDeltaSeconds != nil {
		out.MeasuredDeltaSeconds = ptr(*in.MeasuredDeltaSeconds)
	}
	out.Cfg.Changepoint.Features = slices.Clone(in.Cfg.Changepoint.Features)
	if in.Cfg.YoungDaly.MeasuredDeltaSeconds != nil {
		out.Cfg.YoungDaly.MeasuredDeltaSeconds = ptr(*in.Cfg.YoungDaly.MeasuredDeltaSeconds)
	}
	return out
}

// canonicalReasonOrder is the normative Reasons order (plan: "Ordering is normative").
var canonicalReasonOrder = []TriggerReason{
	TriggerSoftFloor, TriggerChangepoint, TriggerYoungDaly, TriggerHardCeiling,
	TriggerIdleColdCache, TriggerCacheExpiring,
}

func requireCanonicalOrder(t *testing.T, got []TriggerReason) {
	t.Helper()
	last := -1
	for _, r := range got {
		i := slices.Index(canonicalReasonOrder, r)
		require.NotEqual(t, -1, i, "unknown reason %q", r)
		require.Greater(t, i, last, "reasons out of canonical order: %v", got)
		last = i
	}
}

// ── the composite trigger ───────────────────────────────────────────────────

func TestEvaluate_BelowSoftFloor_NoCompact(t *testing.T) {
	in := fire(baseInputs())
	in.ContextTokens = 90_000
	d := Evaluate(in)
	require.False(t, d.ShouldCompact)
	require.Empty(t, d.Reasons)
	require.Equal(t, UrgencyNone, d.Urgency)
}

func TestEvaluate_AboveSoftFloorNoClause_NoCompact(t *testing.T) {
	d := Evaluate(baseInputs())
	require.False(t, d.ShouldCompact)
	require.Equal(t, []TriggerReason{TriggerSoftFloor}, d.Reasons)
	require.Equal(t, UrgencyNone, d.Urgency)
	require.Equal(t, TTLWarm, d.TTL)
}

func TestEvaluate_Changepoint_Fires(t *testing.T) {
	d := Evaluate(fire(baseInputs()))
	require.True(t, d.ShouldCompact)
	require.Equal(t, []TriggerReason{TriggerSoftFloor, TriggerChangepoint}, d.Reasons)
	require.Equal(t, UrgencyAdvisory, d.Urgency)
}

func TestEvaluate_YoungDaly_Fires(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(20)
	in.LastCompactionTS = baseNow - 300_000 // elapsed 300 s > 268.3 s
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Contains(t, d.Reasons, TriggerYoungDaly)
	// M = (147 000 − 120 000) / 900 × 60 = 1 800 s; I* = √(2 × 20 × 1 800) = 268.328…
	require.InDelta(t, 1_800.0, d.Breakdown["mtbf_seconds"], 1e-9)
	require.InDelta(t, math.Sqrt(2*20*1_800), d.YoungDalySeconds, 1e-9)
	require.InDelta(t, 268.328, d.YoungDalySeconds, 1e-3)
	require.InDelta(t, 300.0, d.Breakdown["elapsed_seconds"], 1e-9)
}

func TestEvaluate_YoungDaly_DoesNotFireBelowInterval(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(20)
	in.LastCompactionTS = baseNow - 200_000 // elapsed 200 s < 268.3 s
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerYoungDaly)
	require.False(t, d.ShouldCompact)
	require.InDelta(t, 268.328, d.YoungDalySeconds, 1e-3)
}

func TestEvaluate_YoungDaly_NoBaselineDisablesClause(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(20)
	in.LastCompactionTS = 0
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerYoungDaly)
	require.Equal(t, 1.0, d.Breakdown["young_daly_no_baseline"])
	require.Equal(t, 0.0, d.YoungDalySeconds)
}

func TestEvaluate_YoungDaly_DisabledByConfig(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(20)
	in.LastCompactionTS = baseNow - 300_000
	in.Cfg.YoungDaly.Enabled = false
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerYoungDaly)
	require.Equal(t, 1.0, d.Breakdown["young_daly_disabled"])
	require.Equal(t, 0.0, d.YoungDalySeconds)
}

// TestEvaluate_YoungDaly_UnmeasuredDeltaDisablesClause is the Evaluate half of the plan's
// TestResolveDelta_NilMeansMeasureNotZero (ruling R25): with δ unknown from both config and the
// runtime, the clause is disabled and announced — never fired at √(2·0·M) = 0 seconds.
func TestEvaluate_YoungDaly_UnmeasuredDeltaDisablesClause(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = nil
	in.Cfg.YoungDaly.MeasuredDeltaSeconds = nil
	in.LastCompactionTS = baseNow - 3_600_000 // an hour elapsed: a zero interval would have fired
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerYoungDaly)
	require.Equal(t, 1.0, d.Breakdown["young_daly_delta_unmeasured"])
	require.Equal(t, 0.0, d.YoungDalySeconds)
}

func TestEvaluate_HardCeiling_Fires_UrgencyNow(t *testing.T) {
	in := baseInputs()
	in.ContextTokens = 150_000
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Contains(t, d.Reasons, TriggerHardCeiling)
	require.Equal(t, UrgencyNow, d.Urgency)
	require.Equal(t, core.Tokens(147_000), d.HardCeilingTokens)
}

func TestEvaluate_IdleColdCache_Fires(t *testing.T) {
	d := Evaluate(coldInputs())
	require.True(t, d.ShouldCompact)
	require.Contains(t, d.Reasons, TriggerIdleColdCache)
	require.Equal(t, TTLCold, d.TTL)
	require.Equal(t, 0.0, d.Breakdown["cache_factor"])
}

func TestEvaluate_CacheExpiring_FiresBeforeExpiry(t *testing.T) {
	d := Evaluate(expiringInputs())
	require.True(t, d.ShouldCompact)
	require.Contains(t, d.Reasons, TriggerCacheExpiring)
	require.NotContains(t, d.Reasons, TriggerIdleColdCache)
	require.Equal(t, TTLExpiring, d.TTL)
	require.Greater(t, d.Breakdown["cache_factor"], 0.0)
	require.InDelta(t, 250.0/300.0, d.Breakdown["fired_at_ttl_fraction"], 1e-12)
}

func TestEvaluate_CacheExpiring_SilentBelowFraction(t *testing.T) {
	in := baseInputs()
	in.LastAPICallTS = baseNow - 200_000 // 0.67·TTL, below the 0.8 fraction
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerCacheExpiring)
	require.False(t, d.ShouldCompact)
	require.Equal(t, TTLExpiring, d.TTL)
}

func TestEvaluate_CacheExpiring_SilentWhenRegimeUnknown(t *testing.T) {
	in := baseInputs()
	in.Regime = CacheRegime{}
	in.LastAPICallTS = baseNow - 3_000_000 // 3 000 s: above 0.8·3 600 but the regime is a range
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerCacheExpiring)
	require.False(t, d.ShouldCompact)
	require.Equal(t, TTLExpiring, d.TTL)
	require.Equal(t, 3_600.0, d.Breakdown["ttl_max_seconds"])
	require.Equal(t, 1.0, d.Breakdown["regime_rung"])
}

func TestEvaluate_CacheExpiring_RequiresSoftFloor(t *testing.T) {
	in := expiringInputs()
	in.ContextTokens = 90_000
	d := Evaluate(in)
	require.False(t, d.ShouldCompact)
	require.Empty(t, d.Reasons)
}

func TestEvaluate_CacheExpiring_TriggerOffAtZeroFraction(t *testing.T) {
	in := expiringInputs()
	in.ExpiringTriggerFraction = 0
	d := Evaluate(in)
	require.NotContains(t, d.Reasons, TriggerCacheExpiring)
	require.False(t, d.ShouldCompact)
}

func TestEvaluate_EffortChangeIsColdImmediately(t *testing.T) {
	for _, tc := range []struct {
		name string
		reg  CacheRegime
	}{
		{"known_5m", knownFiveMinuteRegime(baseCfg())},
		{"unknown", CacheRegime{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInputs()
			in.Regime = tc.reg
			in.LastAPICallTS = baseNow // gap 0 s: wall-clock reads maximally warm
			in.EffortChanged = true
			d := Evaluate(in)
			require.Equal(t, TTLCold, d.TTL)
			require.Equal(t, 1.0, d.Breakdown["cold_reason_effort_change"])
			require.Equal(t, 0.0, d.Breakdown["idle_gap_seconds"])
			require.True(t, d.ShouldCompact)
			require.Contains(t, d.Reasons, TriggerIdleColdCache)
		})
	}
}

func TestEvaluate_RequestStartAnchorPreferredOverLastAPICall(t *testing.T) {
	in := baseInputs()
	in.LastAPICallTS = baseNow                // Stop fired just now …
	in.LastRequestStartTS = baseNow - 400_000 // … but the request started 400 s ago
	d := Evaluate(in)
	require.Equal(t, TTLCold, d.TTL)
	require.InDelta(t, 400.0, d.Breakdown["idle_gap_seconds"], 1e-9)
}

// TestEvaluate_ReasonsOrderStable pins the normative order soft_floor, changepoint, young_daly,
// hard_ceiling, idle_cold_cache, cache_expiring. No single Inputs can carry every clause at
// once — crossing the hard ceiling zeroes M and so disables Young–Daly, and idle_cold_cache and
// cache_expiring are mutually exclusive TTL states — so the three maximal achievable
// combinations are asserted exactly, and each is checked against the canonical order.
func TestEvaluate_ReasonsOrderStable(t *testing.T) {
	withYoungDaly := func(in Inputs) Inputs {
		in.MeasuredDeltaSeconds = ptr(20)
		in.LastCompactionTS = baseNow - 300_000
		return in
	}
	cases := []struct {
		name string
		in   Inputs
		want []TriggerReason
	}{
		{
			name: "changepoint_young_daly_cold",
			in:   withYoungDaly(fire(coldInputs())),
			want: []TriggerReason{TriggerSoftFloor, TriggerChangepoint, TriggerYoungDaly, TriggerIdleColdCache},
		},
		{
			name: "changepoint_hard_ceiling_cold",
			in: func() Inputs {
				in := fire(coldInputs())
				in.ContextTokens = 150_000
				return in
			}(),
			want: []TriggerReason{TriggerSoftFloor, TriggerChangepoint, TriggerHardCeiling, TriggerIdleColdCache},
		},
		{
			name: "changepoint_young_daly_expiring",
			in:   withYoungDaly(fire(expiringInputs())),
			want: []TriggerReason{TriggerSoftFloor, TriggerChangepoint, TriggerYoungDaly, TriggerCacheExpiring},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Evaluate(tc.in)
			require.Equal(t, tc.want, d.Reasons)
			requireCanonicalOrder(t, d.Reasons)
		})
	}
}

// ── p-selection ─────────────────────────────────────────────────────────────

func TestEvaluate_ArgmaxLatestWhenWarm(t *testing.T) {
	d := Evaluate(fire(baseInputs()))
	require.True(t, d.ShouldCompact)
	require.Equal(t, 118_000, d.P.Pos)
	// reclaimable 6 000 × 0.1 = 600; rewrite 1.25 × 2 000 × 1 = 2 500; distortion 0.4 × 42 = 16.8
	require.InDelta(t, -1_916.8, d.PScore, 1e-9)
	require.Less(t, d.PScore, 0.0, "a negative score is intended: the trigger decides whether, p-selection where")
}

func TestEvaluate_ArgmaxDeepestWhenCold(t *testing.T) {
	d := Evaluate(coldInputs())
	require.True(t, d.ShouldCompact)
	require.Equal(t, 40_000, d.P.Pos)
	// reclaimable 30 000 × 0.1 = 3 000; rewrite 0; distortion 0.4 × 210 = 84
	require.InDelta(t, 2_916.0, d.PScore, 1e-9)
	require.Equal(t, 0.0, d.Breakdown["rewrite"])
}

func TestEvaluate_UnknownRegimeDoesNotDeepCutAt400s(t *testing.T) {
	in := fire(coldInputs())
	in.Regime = CacheRegime{}
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Equal(t, TTLExpiring, d.TTL, "400 s under a [300, 3600] range is expiring, not cold")
	require.NotContains(t, d.Reasons, TriggerIdleColdCache)
	require.Equal(t, 118_000, d.P.Pos)
	require.Greater(t, d.Breakdown["rewrite"], 0.0)
	require.Equal(t, 2.0, d.Breakdown["regime_write_multiplier"], "the unknown rung charges the one-hour w")
	require.Equal(t, 1.25, d.Breakdown["write_multiplier"], "cfg's five-minute floor is still reported")
}

func TestEvaluate_DeepCutWhenColdDisabled(t *testing.T) {
	in := coldInputs()
	in.Cfg.Idle.DeepCutWhenCold = false
	for i := range in.Candidates {
		in.Candidates[i].ReclaimableTokens = 10_000
		in.Candidates[i].Coupling = 50
	}
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Equal(t, 118_000, d.P.Pos, "latest wins the tie when the deep-cut preference is off")
}

func TestEvaluate_RoundBoundaryIntersection(t *testing.T) {
	// Make the non-round-boundary candidate the most attractive one, so exclusion is what
	// decides the cut rather than the score.
	in := coldInputs()
	in.Candidates[1].RoundBoundary = false
	in.Candidates[1].ReclaimableTokens = 30_000 // cold score 2 966.4 > 2 916 at Pos 40 000
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.NotEqual(t, 80_000, d.P.Pos)
	require.Equal(t, 40_000, d.P.Pos)
	require.Equal(t, 2.0, d.Breakdown["candidates"])
	require.Equal(t, 3.0, d.Breakdown["candidates_supplied"])
	require.NotContains(t, d.Breakdown, "round_boundary_relaxed")
}

func TestEvaluate_RoundBoundaryRelaxed(t *testing.T) {
	in := fire(baseInputs())
	for i := range in.Candidates {
		in.Candidates[i].RoundBoundary = false
	}
	d := Evaluate(in)
	require.Equal(t, 1.0, d.Breakdown["round_boundary_relaxed"])
	require.True(t, d.ShouldCompact)
	require.Equal(t, 118_000, d.P.Pos)
	require.Equal(t, 3.0, d.Breakdown["candidates"])
}

func TestEvaluate_NoCandidates(t *testing.T) {
	in := fire(baseInputs())
	in.Candidates = nil
	d := Evaluate(in)
	require.False(t, d.ShouldCompact)
	require.Equal(t, 1.0, d.Breakdown["no_candidates"])
	require.Equal(t, UrgencyAdvisory, d.Urgency)
	require.Equal(t, Candidate{}, d.P)
	require.Equal(t, 0.0, d.Breakdown["candidates"])
}

func TestEvaluate_NoCandidatesAboveCeiling(t *testing.T) {
	in := baseInputs()
	in.Candidates = nil
	in.ContextTokens = 150_000
	d := Evaluate(in)
	require.False(t, d.ShouldCompact)
	require.Equal(t, UrgencyNow, d.Urgency)
	require.Equal(t, 1.0, d.Breakdown["no_candidates"])
}

func TestEvaluate_ScoreArithmeticExact(t *testing.T) {
	in := baseInputs()
	in.ContextTokens = 150_000 // fires via hard_ceiling
	in.Candidates = []Candidate{{Pos: 148_230, Turn: 40, SegmentID: 4, RoundBoundary: true, ReclaimableTokens: 6_000, Coupling: 42}}
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Equal(t, TTLWarm, d.TTL)
	// reclaimable = 6 000 × 0.1 = 600; rewrite = 1.25 × 1 770 × 1.0 = 2 212.5;
	// distortion = 0.4 × 42 = 16.8; PScore = 600 − 2 212.5 − 16.8 = −1 629.3
	require.InDelta(t, 600.0, d.Breakdown["reclaimable"], 1e-9)
	require.InDelta(t, 2_212.5, d.Breakdown["rewrite"], 1e-9)
	require.InDelta(t, 16.8, d.Breakdown["distortion"], 1e-9)
	require.InDelta(t, -1_629.3, d.PScore, 1e-9)
	require.InDelta(t, d.PScore, d.Breakdown["score"], 0)
	require.InDelta(t, 1_770.0, d.Breakdown["rewrite_tokens"], 1e-9)
}

func TestEvaluate_MultipliersReadFromConfig(t *testing.T) {
	base := fire(baseInputs())
	d1 := Evaluate(base)
	require.True(t, d1.ShouldCompact)

	doubledRead := base
	cfgR := baseCfg()
	cfgR.Cache.ReadMultiplier *= 2
	doubledRead.Cfg = cfgR
	doubledRead.Regime = knownFiveMinuteRegime(cfgR)
	d2 := Evaluate(doubledRead)
	require.Equal(t, d1.P.Pos, d2.P.Pos)
	require.InDelta(t, 2*d1.Breakdown["reclaimable"], d2.Breakdown["reclaimable"], 1e-9)
	require.InDelta(t, 2*d1.Breakdown["read_multiplier"], d2.Breakdown["read_multiplier"], 1e-12)
	require.InDelta(t, 2*d1.Breakdown["regime_read_multiplier"], d2.Breakdown["regime_read_multiplier"], 1e-12)

	doubledWrite := base
	cfgW := baseCfg()
	cfgW.Cache.WriteMultiplier *= 2
	doubledWrite.Cfg = cfgW
	doubledWrite.Regime = knownFiveMinuteRegime(cfgW)
	d3 := Evaluate(doubledWrite)
	require.Equal(t, d1.P.Pos, d3.P.Pos)
	require.InDelta(t, 2*d1.Breakdown["rewrite"], d3.Breakdown["rewrite"], 1e-9)
	require.InDelta(t, 2*d1.Breakdown["write_multiplier"], d3.Breakdown["write_multiplier"], 1e-12)
	require.InDelta(t, 2*d1.Breakdown["regime_write_multiplier"], d3.Breakdown["regime_write_multiplier"], 1e-12)
}

func TestEvaluate_LambdaZeroDisablesDistortion(t *testing.T) {
	in := fire(baseInputs())
	in.CouplingLambda = 0
	for i := range in.Candidates {
		in.Candidates[i].Coupling = 210
	}
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Equal(t, 0.0, d.Breakdown["distortion"])
	require.Equal(t, 0.0, d.Breakdown["lambda"])
	require.Equal(t, 210.0, d.Breakdown["coupling"])
	require.InDelta(t, 600.0-2_500.0, d.PScore, 1e-9)
}

func TestEvaluate_ZeroEffectiveWindow(t *testing.T) {
	in := fire(baseInputs())
	in.EffectiveWindow = 0
	d := Evaluate(in)
	require.Equal(t, map[string]float64{"error_no_window": 1}, d.Breakdown)
	require.False(t, d.ShouldCompact)
	require.Empty(t, d.Reasons)
	require.Nil(t, d.Background)
	require.Equal(t, Candidate{}, d.P)
	require.Equal(t, 0.0, d.PScore)
	require.Equal(t, UrgencyNone, d.Urgency)
	require.Equal(t, core.Tokens(0), d.SoftFloorTokens)
	require.Equal(t, core.Tokens(0), d.HardCeilingTokens)
}

func TestEvaluate_NonMonotonicReclaimableFlagged(t *testing.T) {
	in := fire(baseInputs())
	in.Candidates[0].ReclaimableTokens = 6_000
	in.Candidates[2].ReclaimableTokens = 30_000
	d := Evaluate(in)
	require.Equal(t, 1.0, d.Breakdown["reclaimable_nonmonotonic"])
	require.True(t, d.ShouldCompact)
	require.Equal(t, 118_000, d.P.Pos)
}

func TestEvaluate_CandidatesCappedAt32(t *testing.T) {
	// Cold cache with every score tied: the deep-cut tie-break picks the SMALLEST retained Pos,
	// which is only 69 000 if exactly the highest-Pos 32 of the 100 survived the cap.
	in := coldInputs()
	in.Candidates = ascendingCandidates(100, 1_000)
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Equal(t, 32.0, d.Breakdown["candidates"])
	require.Equal(t, 100.0, d.Breakdown["candidates_supplied"])
	require.Equal(t, 69_000, d.P.Pos)
}

// ── purity ──────────────────────────────────────────────────────────────────

func TestEvaluate_Purity_NoInputMutation(t *testing.T) {
	in := fire(baseInputs())
	in.MeasuredDeltaSeconds = ptr(20)
	in.Changepoint.Posterior = []float64{0.5, 0.3, 0.2}
	// Unsorted, with a non-round-boundary member, so both prepareCandidates and eligible have
	// something to reorder or drop — on their own copy, never on ours.
	in.Candidates = []Candidate{in.Candidates[2], in.Candidates[0], in.Candidates[1]}
	in.Candidates[1].RoundBoundary = false
	before := deepCopyInputs(in)
	_ = Evaluate(in)
	require.Empty(t, cmp.Diff(before, in), "Evaluate must not mutate its argument")
}

func TestEvaluate_Purity_Idempotent(t *testing.T) {
	in := fire(baseInputs())
	in.MeasuredDeltaSeconds = ptr(20)
	d1 := Evaluate(in)
	d2 := Evaluate(in)
	require.Empty(t, cmp.Diff(d1, d2))
}

// ── goldens ─────────────────────────────────────────────────────────────────

// TestEvaluate_GoldenDecisions compares the three hand-derived Decision documents (see the
// seat report for the arithmetic) against encoding/json of the live Decision.
func TestEvaluate_GoldenDecisions(t *testing.T) {
	cases := []struct {
		name   string
		in     Inputs
		pscore float64
	}{
		{"decision-warm.json", warmGoldenInputs(), -1_916.8},
		{"decision-expiring.json", expiringInputs(), 600 - 833.3333333333333 - 16.8},
		{"decision-cold.json", coldInputs(), 2_916},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Evaluate(tc.in)
			require.InDelta(t, tc.pscore, d.PScore, 1e-9)
			got, err := json.Marshal(d)
			require.NoError(t, err)
			require.JSONEq(t, string(readGolden(t, tc.name)), string(got))
		})
	}
}

func TestEvaluate_BreakdownKeysComplete(t *testing.T) {
	var golden struct {
		Breakdown map[string]float64
	}
	require.NoError(t, json.Unmarshal(readGolden(t, "decision-warm.json"), &golden))
	want := slices.Sorted(mapsKeys(golden.Breakdown))
	got := slices.Sorted(mapsKeys(Evaluate(warmGoldenInputs()).Breakdown))
	require.Equal(t, want, got, "Breakdown key set drifted from decision-warm.json (missing or extra key)")
	require.NotContains(t, got, "window_source", "window_source is the Runtime's key, added after Evaluate returns")
}

func mapsKeys(m map[string]float64) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// ── background plan ─────────────────────────────────────────────────────────

func TestEvaluate_BackgroundEmptyWhenDisabled(t *testing.T) {
	in := coldInputs()
	in.ResidualTokens = 9_000
	in.Cfg.Idle.BackgroundWork = false
	d := Evaluate(in)
	require.Nil(t, d.Background)
}

func TestEvaluate_BackgroundColdIncludesAllSix(t *testing.T) {
	in := coldInputs()
	in.ResidualTokens = 9_000
	d := Evaluate(in)
	require.Equal(t, []BackgroundTask{
		BackgroundAdvanceFrontier, BackgroundPrecomputeSlice, BackgroundRefreshDelta,
		BackgroundRebuildBloom, BackgroundCompactDAG, BackgroundGC,
	}, d.Background)
}

func TestEvaluate_BackgroundWarmIsFrontierOnly(t *testing.T) {
	in := baseInputs()
	in.ResidualTokens = 9_000
	in.MeasuredDeltaSeconds = ptr(20)
	in.ContextTokens = 90_000
	d := Evaluate(in)
	require.Equal(t, []BackgroundTask{BackgroundAdvanceFrontier}, d.Background)
}

func TestEvaluate_BackgroundNilWhenNothingApplies(t *testing.T) {
	in := baseInputs()
	in.MeasuredDeltaSeconds = ptr(20)
	in.ContextTokens = 90_000
	d := Evaluate(in)
	require.Nil(t, d.Background)
}

// ── rulings R6/R7 and the regimes ───────────────────────────────────────────

func TestEvaluate_HostTriggerAbsentCapsUrgency(t *testing.T) {
	in := baseInputs()
	in.ContextTokens = 150_000
	in.HostTriggerAbsent = true
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.Contains(t, d.Reasons, TriggerHardCeiling)
	require.Equal(t, UrgencyAdvisory, d.Urgency)
	require.Equal(t, 1.0, d.Breakdown["urgency_capped_advisory"])

	quiet := baseInputs()
	quiet.HostTriggerAbsent = true
	require.NotContains(t, Evaluate(quiet).Breakdown, "urgency_capped_advisory",
		"the cap is recorded only when it changed something")
}

func TestEvaluate_DisabledRegimeNeverCold(t *testing.T) {
	in := fire(coldInputs())
	in.EffortChanged = true
	in.Regime = CacheRegime{ReadMultiplier: 1, WriteMultiplier: 1, Disabled: true, Source: "disabled"}
	d := Evaluate(in)
	require.Equal(t, TTLUnknown, d.TTL)
	require.NotContains(t, d.Reasons, TriggerIdleColdCache)
	require.NotContains(t, d.Reasons, TriggerCacheExpiring)
	require.Equal(t, 1.0, d.Breakdown["cache_disabled"])
	require.Equal(t, 1.0, d.Breakdown["cache_factor"])
	require.Equal(t, 3.0, d.Breakdown["regime_rung"])
	require.Equal(t, 0.0, d.Breakdown["fired_at_ttl_fraction"])
	require.True(t, d.ShouldCompact)
	require.Equal(t, 118_000, d.P.Pos, "no cold state ⇒ the deep-cut branch is never taken")
	require.InDelta(t, 2_000.0, d.Breakdown["rewrite"], 1e-9, "r = w = 1: every tail token costs one token")
}

func TestEvaluate_RegimeRungCodes(t *testing.T) {
	for source, want := range map[string]float64{
		"unknown": 1, "enable_1h": 2, "disabled": 3, "force_5m": 4, "subagent_5m": 5, "": 0, "bogus": 0,
	} {
		in := baseInputs()
		in.Regime.Source = source
		require.Equal(t, want, Evaluate(in).Breakdown["regime_rung"], "source %q", source)
	}
}

// TestEvaluate_BreakdownIsFinite drives pathological floats through every term: the state
// codec JSON-marshals Breakdown, and encoding/json refuses NaN and ±Inf outright.
func TestEvaluate_BreakdownIsFinite(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cases := map[string]func(in Inputs) Inputs{
		"burn_rate_nan":        func(in Inputs) Inputs { in.BurnRateTokensPerMin = nan; return in },
		"burn_rate_inf":        func(in Inputs) Inputs { in.BurnRateTokensPerMin = inf; return in },
		"lambda_nan":           func(in Inputs) Inputs { in.CouplingLambda = nan; return fire(in) },
		"lambda_inf":           func(in Inputs) Inputs { in.CouplingLambda = inf; return fire(in) },
		"prob_changepoint_inf": func(in Inputs) Inputs { in.Changepoint.ProbChangepoint = inf; return in },
		"delta_inf": func(in Inputs) Inputs {
			in.MeasuredDeltaSeconds = ptr(inf)
			in.LastCompactionTS = baseNow - 300_000
			return in
		},
		"expiring_fraction_nan": func(in Inputs) Inputs {
			in.ExpiringTriggerFraction = nan
			in.LastAPICallTS = baseNow - 250_000
			return in
		},
		"regime_multipliers_nan_inf": func(in Inputs) Inputs {
			in.Regime.ReadMultiplier = nan
			in.Regime.WriteMultiplier = inf
			return fire(in)
		},
		"timestamps_in_future": func(in Inputs) Inputs {
			in.LastAPICallTS = baseNow + 10_000
			in.LastCacheWriteTS = baseNow + 10_000
			in.LastCompactionTS = baseNow + 10_000
			in.MeasuredDeltaSeconds = ptr(20)
			return fire(in)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := Evaluate(mutate(baseInputs()))
			for k, v := range d.Breakdown {
				require.False(t, math.IsNaN(v) || math.IsInf(v, 0), "Breakdown[%q] = %v", k, v)
			}
			require.False(t, math.IsNaN(d.PScore) || math.IsInf(d.PScore, 0), "PScore = %v", d.PScore)
			require.False(t, math.IsNaN(d.YoungDalySeconds) || math.IsInf(d.YoungDalySeconds, 0))
			_, err := json.Marshal(d)
			require.NoError(t, err)
		})
	}
}

// ── performance budget ──────────────────────────────────────────────────────

func TestEvaluate_NoAllocationsBeyondBudget(t *testing.T) {
	in := fire(baseInputs())
	in.Changepoint.Posterior = []float64{0.4, 0.3, 0.2, 0.1}
	in.Candidates = make([]Candidate, 0, 64)
	for i := range 64 {
		in.Candidates = append(in.Candidates, Candidate{
			Pos:               1_000 * (i + 1),
			Turn:              core.TurnIndex(i),
			SegmentID:         core.SegmentID(i / 4),
			RoundBoundary:     true,
			ReclaimableTokens: core.Tokens(100_000 - 1_000*i),
			Coupling:          i,
		})
	}
	allocs := testing.AllocsPerRun(200, func() { _ = Evaluate(in) })
	t.Logf("Evaluate with 64 candidates: %.0f allocs/op", allocs)
	require.LessOrEqual(t, allocs, 8.0)
}

// TestEvaluate_AssumedMaxTTLReachesClassification pins ruling R42 end to end: a regime resolved on
// the unknown rung with runtime.scheduler.cache.assumeMaxTTLSeconds = 7200 keeps a 3 700-second-old
// prefix Expiring, where the hard-wired one-hour assumption would already have called it Cold.
func TestEvaluate_AssumedMaxTTLReachesClassification(t *testing.T) {
	in := baseInputs()
	in.Regime = ResolveCacheRegime(func(string) string { return "" }, in.Cfg, "claude-opus-5", false, 7200)
	in.LastAPICallTS = baseNow - 3_700_000
	in.LastRequestStartTS = in.LastAPICallTS
	d := Evaluate(in)
	require.Equal(t, TTLExpiring, d.TTL)
	in.Regime = ResolveCacheRegime(func(string) string { return "" }, in.Cfg, "claude-opus-5", false, HostOneHourTTLSeconds)
	require.Equal(t, TTLCold, Evaluate(in).TTL)
}

// TestEvaluate_CapAppliesAfterRoundBoundaryFilter pins ruling R44: the 32-candidate cap runs on
// the round-boundary intersection, never before it, so eligible candidates are not discarded in
// favour of ineligible ones. Forty candidates; only the eight lowest-Pos sit on a round boundary.
func TestEvaluate_CapAppliesAfterRoundBoundaryFilter(t *testing.T) {
	in := coldInputs()
	in.Candidates = ascendingCandidates(40, 1_000)
	for i := range in.Candidates {
		in.Candidates[i].RoundBoundary = i < 8
	}
	d := Evaluate(in)
	require.True(t, d.ShouldCompact)
	require.NotContains(t, d.Breakdown, "round_boundary_relaxed")
	require.Equal(t, 8.0, d.Breakdown["candidates"])
	require.LessOrEqual(t, d.P.Pos, 8_000, "the cut must be one of the eight eligible candidates")
}

// Package schedulertest is the conformance suite for scheduler.Runtime and scheduler.Detector
// (00-ARCHITECTURE.md §5.22): every Runtime implementation SP-12 ships must pass
// RunSchedulerSuite, and every Detector must pass RunDetectorSuite. SP-01 shipped the suite
// gated behind a Rule W-1 stub probe; SP-12 removed the gate, so the behaviour block runs
// unconditionally — against the package-level pure scheduler.Evaluate for the white-box cases
// and against the factory-supplied Runtime for the black-box ones.
package schedulertest

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// Threshold arithmetic for the behaviour.go fixture (Qompack.md §2.5, §8.4):
//
//	SoftFloor   = 0.6 × 200 000                      = 120 000
//	HardCeiling = 200 000 − 13 000 − 15 000          = 172 000
//
// and the second, deeper candidate the argmax cases add to that fixture. Its reclaimable count
// exceeds the shallow candidate's, so reclaimable(p) stays non-increasing in p.
const (
	expectedSoftFloorTokens        = 120_000
	expectedHardCeilingTokens      = 172_000
	deepCandidatePosTokens         = 60_000
	deepCandidateReclaimableTokens = 90_000
	deepCandidateCouplingEdges     = 4
)

// Detector-suite parameters: the length of the deterministic observation series, the hard cap
// on the run-length posterior (bocd.go's bocdMaxRunLength), the tolerance on its total mass, the
// seed of the in-package LCG, and the scale of the synthetic inter-turn gaps.
const (
	detectorSuiteObservations = 200
	detectorMaxPosteriorLen   = 512
	posteriorMassTolerance    = 1e-9
	detectorSeriesSeed        = 0x9E3779B97F4A7C15
	detectorGapScaleSeconds   = 600.0
)

// coreBreakdownKeys are the Breakdown entries every Decision carries once a window resolves,
// whatever fired: /qompack:status and the eval harness read them unconditionally.
var coreBreakdownKeys = []string{"context_tokens", "soft_floor", "hard_ceiling", "cache_factor", "candidates"}

// RunSchedulerSuite is the conformance suite for scheduler.Runtime. name distinguishes multiple
// factories run in the same test binary (for example a minimal in-memory runtime vs. the daemon's
// real one); factory must return a fresh, ready-to-use Runtime on every call.
func RunSchedulerSuite(t *testing.T, name string, factory func(t *testing.T) scheduler.Runtime) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		rt := factory(t)
		require.NotNil(t, rt)

		ctx := context.Background()

		// Observe has no error return; any ChangepointState it produces is shape-valid, so this
		// only asserts that calling it does not panic.
		_ = rt.Observe(ctx, scheduler.Features{PathJaccard: 1}, core.TurnIndex(0))

		_, err := rt.Evaluate(ctx)
		requireKnownError(t, err)

		rt.NotifyActivity(core.UnixMilli(1))

		// IdleSince has no error return; any (UnixMilli, bool) pair is shape-valid.
		_, _ = rt.IdleSince()

		requireKnownError(t, rt.Persist(ctx))
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("runtime_evaluate_is_deterministic", func(t *testing.T) {
			rt := factory(t)
			ctx := context.Background()
			d1, err1 := rt.Evaluate(ctx)
			d2, err2 := rt.Evaluate(ctx)
			require.Equal(t, err1, err2)
			require.Equal(t, d1, d2,
				"Evaluate must be pure: two calls against unchanged Runtime state must agree")
		})

		t.Run("evaluate_young_daly_matches_formula", func(t *testing.T) {
			runYoungDalyFormulaCase(t)
		})

		t.Run("composite_trigger_truth_table", func(t *testing.T) {
			runCompositeTriggerTruthTable(t)
		})

		t.Run("threshold_arithmetic", func(t *testing.T) {
			runThresholdArithmeticCase(t)
		})

		t.Run("argmax_latest_when_warm", func(t *testing.T) {
			in := twoCandidateInputs()
			in.Changepoint = scheduler.ChangepointState{AtChangepoint: true, ProbChangepoint: 1}
			got := scheduler.Evaluate(in)
			require.True(t, got.ShouldCompact)
			require.Equal(t, scheduler.TTLWarm, got.TTL)
			require.Equal(t, candidatePosTokens, got.P.Pos,
				"warm cache ⇒ edit as late as possible (Qompack.md §5.4)")
		})

		t.Run("argmax_deepest_when_cold", func(t *testing.T) {
			in := twoCandidateInputs()
			in.LastAPICallTS = in.Now - core.UnixMilli(idleGapSeconds*millisPerSecond)
			got := scheduler.Evaluate(in)
			require.True(t, got.ShouldCompact)
			require.Equal(t, scheduler.TTLCold, got.TTL)
			require.Equal(t, deepCandidatePosTokens, got.P.Pos,
				"cold cache ⇒ the deep cut is free (Qompack.md §5.4)")
		})

		t.Run("evaluate_is_pure", func(t *testing.T) {
			in := twoCandidateInputs()
			in.Changepoint = scheduler.ChangepointState{AtChangepoint: true, ProbChangepoint: 1}
			require.Equal(t, scheduler.Evaluate(in), scheduler.Evaluate(in),
				"two Evaluate calls on the same Inputs must be equal")
		})

		t.Run("breakdown_carries_core_keys", func(t *testing.T) {
			got := scheduler.Evaluate(baseInputs())
			for _, key := range coreBreakdownKeys {
				require.Contains(t, got.Breakdown, key)
			}
		})

		t.Run("runtime_observe_returns_normalized_posterior", func(t *testing.T) {
			rt := factory(t)
			st := rt.Observe(context.Background(), scheduler.Features{PathJaccard: 1}, core.TurnIndex(0))
			requireNormalizedPosterior(t, st.Posterior)
		})

		t.Run("runtime_notify_activity_anchors_idle_since", func(t *testing.T) {
			rt := factory(t)
			ts := core.UnixMilli(baseNowMillis)
			rt.NotifyActivity(ts)
			got, _ := rt.IdleSince()
			require.Equal(t, ts, got, "IdleSince must report the timestamp NotifyActivity recorded")
		})

		t.Run("runtime_persist_is_repeatable", func(t *testing.T) {
			rt := factory(t)
			ctx := context.Background()
			requireKnownError(t, rt.Persist(ctx))
			requireKnownError(t, rt.Persist(ctx))
		})
	})
}

// RunDetectorSuite is the conformance suite for scheduler.Detector: the run-length posterior
// stays normalized and bounded over a deterministic series, MarshalBinary/UnmarshalBinary
// reproduce State() exactly, and two detectors fed the same series agree observation by
// observation. factory must return a fresh detector on every call.
func RunDetectorSuite(t *testing.T, name string, factory func(t *testing.T) scheduler.Detector) {
	t.Helper()

	t.Run(name+"/normalized_after_random_observations", func(t *testing.T) {
		det := factory(t)
		require.NotNil(t, det)
		for _, f := range detectorSeries() {
			requireNormalizedPosterior(t, det.Observe(f).Posterior)
		}
		requireNormalizedPosterior(t, det.State().Posterior)
	})

	t.Run(name+"/posterior_length_bounded", func(t *testing.T) {
		det := factory(t)
		for _, f := range detectorSeries() {
			require.LessOrEqual(t, len(det.Observe(f).Posterior), detectorMaxPosteriorLen)
		}
	})

	t.Run(name+"/marshal_round_trip_reproduces_state", func(t *testing.T) {
		det := factory(t)
		for _, f := range detectorSeries() {
			_ = det.Observe(f)
		}
		b, err := det.MarshalBinary()
		require.NoError(t, err)
		restored := factory(t)
		require.NoError(t, restored.UnmarshalBinary(b))
		require.Equal(t, det.State(), restored.State())
	})

	t.Run(name+"/deterministic_across_instances", func(t *testing.T) {
		a, b := factory(t), factory(t)
		for _, f := range detectorSeries() {
			require.Equal(t, a.Observe(f), b.Observe(f))
		}
		require.Equal(t, a.State(), b.State())
	})
}

// runThresholdArithmeticCase pins the fixture's soft floor and hard ceiling to the §2.5/§8.4
// arithmetic, both as the numbers Evaluate reports and as the exported helpers compute them.
func runThresholdArithmeticCase(t *testing.T) {
	t.Helper()
	in := baseInputs()
	got := scheduler.Evaluate(in)
	require.Equal(t, core.Tokens(expectedSoftFloorTokens), got.SoftFloorTokens)
	require.Equal(t, core.Tokens(expectedHardCeilingTokens), got.HardCeilingTokens)
	require.Equal(t, got.SoftFloorTokens, scheduler.SoftFloor(in.EffectiveWindow, in.Cfg))
	require.Equal(t, got.HardCeilingTokens, scheduler.HardCeiling(in.EffectiveWindow, in.Cfg))
	require.Equal(t, core.Tokens(effectiveWindowTokens)-scheduler.HostAutoCompactBuffer-core.Tokens(hardCeilingMargin),
		got.HardCeilingTokens)
}

// twoCandidateInputs is baseInputs plus a deeper round-boundary candidate, so the two limbs of
// §5.4's bimodality are distinguishable: warm prefers the shallow cut, cold the deep one.
func twoCandidateInputs() scheduler.Inputs {
	in := baseInputs()
	in.Candidates = append([]scheduler.Candidate{{
		Pos:               deepCandidatePosTokens,
		RoundBoundary:     true,
		ReclaimableTokens: core.Tokens(deepCandidateReclaimableTokens),
		Coupling:          deepCandidateCouplingEdges,
	}}, in.Candidates...)
	return in
}

// requireNormalizedPosterior asserts a run-length posterior is a probability distribution:
// non-empty, every entry finite and non-negative, total mass 1.
func requireNormalizedPosterior(t *testing.T, post []float64) {
	t.Helper()
	require.NotEmpty(t, post, "a posterior must carry at least one run-length hypothesis")
	sum := 0.0
	for i, p := range post {
		require.False(t, math.IsNaN(p) || math.IsInf(p, 0), "posterior[%d] = %v", i, p)
		require.GreaterOrEqual(t, p, 0.0, "posterior[%d]", i)
		sum += p
	}
	require.InDelta(t, 1.0, sum, posteriorMassTolerance, "posterior mass")
}

// lcg is a deterministic 64-bit linear congruential generator (Knuth's MMIX constants). The
// suite must not depend on math/rand's sequence across Go versions, and it needs no
// cryptographic quality — only reproducibility.
type lcg struct{ state uint64 }

func (g *lcg) next() float64 {
	g.state = g.state*6364136223846793005 + 1442695040888963407
	return float64(g.state>>11) / float64(uint64(1)<<53)
}

// detectorSeries is the deterministic observation series every RunDetectorSuite case feeds:
// detectorSuiteObservations unit-interval feature vectors with an inter-turn gap on
// [0, detectorGapScaleSeconds).
func detectorSeries() []scheduler.Features {
	g := &lcg{state: detectorSeriesSeed}
	out := make([]scheduler.Features, detectorSuiteObservations)
	for i := range out {
		out[i] = scheduler.Features{
			PathJaccard:     g.next(),
			ToolShift:       g.next(),
			LexicalCohesion: g.next(),
			GapSeconds:      g.next() * detectorGapScaleSeconds,
			TodoTransition:  g.next(),
		}
	}
	return out
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// implementation is allowed to return from an operation (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

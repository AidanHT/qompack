package eval_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// TestLiveBootstrap_PreregisteredSettings pins the production bootstrap (W6 item 2, decision D-c):
// B = 10,000 resamples from a fixed seed. The draws depend only on the task set, never on the order
// the trials arrive in, so a resumed run and an uninterrupted one draw the same matrix.
func TestLiveBootstrap_PreregisteredSettings(t *testing.T) {
	require.Equal(t, 10000, eval.LiveBootstrapB)
	require.Equal(t, uint64(20261016), eval.LiveBootstrapSeed)
	a := eval.NewLiveDraws([]string{"t2", "t1", "t3", "t1"}, 50, eval.LiveBootstrapSeed)
	b := eval.NewLiveDraws([]string{"t3", "t2", "t1"}, 50, eval.LiveBootstrapSeed)
	require.Equal(t, []string{"t1", "t2", "t3"}, a.Tasks)
	require.Equal(t, a, b)
	require.Len(t, a.Idx, 50)
	require.Len(t, a.Idx[0], 3)
	require.NotEqual(t, a.Idx, eval.NewLiveDraws(a.Tasks, 50, 7).Idx, "another seed draws another matrix")

	// The production matrix itself is pinned (review TESTS-09): a resumed run on another build of the
	// harness must draw the same resamples, or the union verdict could differ.
	ids := make([]string, 24)
	for i := range ids {
		ids[i] = fmt.Sprintf("t%02d", i)
	}
	d := eval.NewLiveDraws(ids, eval.LiveBootstrapB, eval.LiveBootstrapSeed)
	h := sha256.New()
	for _, row := range d.Idx {
		for _, j := range row {
			_ = binary.Write(h, binary.LittleEndian, j)
		}
	}
	require.Equal(t, liveDrawsDigest, hex.EncodeToString(h.Sum(nil)), "the production draw matrix changed")
}

// liveDrawsDigest is the SHA-256 of the production draw matrix over 24 tasks (B = 10,000, the
// pre-registered seed), its indices as little-endian int32 in row order.
const liveDrawsDigest = "96a96ae698fdbdcdc55e1b81d07cdf883426cf27f1bd17839dde76c45cb3605a"

// TestPairedBootstrapDiff_ResamplesTasksJointly: the statistic is the difference of pooled
// proportions; tasks are resampled jointly across arms, so task-level variation every arm shares
// costs nothing — with base rates from 0.1 to 0.9 and steer exactly 0.2 above classic on every task,
// the interval stays tight around 0.2.
func TestPairedBootstrapDiff_ResamplesTasksJointly(t *testing.T) {
	var tasks []string
	var counts []eval.LiveTaskCounts
	for i := range 9 {
		tasks = append(tasks, fmt.Sprintf("t%d", i))
		ka := 10 + 10*i // of 100: 0.1 .. 0.9
		kb := min(ka+20, 100)
		if i == 8 {
			ka = 70 // keep steer exactly 0.2 above classic everywhere
			kb = 90
		}
		counts = append(counts, eval.LiveTaskCounts{KB: kb, NB: 100, KA: ka, NA: 100})
	}
	d := eval.NewLiveDraws(tasks, 2000, eval.LiveBootstrapSeed)
	iv, ok := eval.PairedBootstrapDiff(counts, d, 0.95)
	require.True(t, ok)
	require.InDelta(t, 0.2, iv.Estimate, 1e-12)
	require.InDelta(t, 0.2, iv.Low, 1e-9, "a shared task effect costs nothing when tasks are resampled jointly")
	require.InDelta(t, 0.2, iv.High, 1e-9)
	require.Equal(t, 2000, iv.Resamples)

	_, ok = eval.PairedBootstrapDiff([]eval.LiveTaskCounts{{KB: 1, NB: 1}}, eval.NewLiveDraws([]string{"x"}, 10, 1), 0.95)
	require.False(t, ok, "a comparator with no trial defines no difference")
}

// TestPairedBootstrapRatio: the decided aggregate is the ratio of pooled means and the reported one
// the ratio of geometric means; a ratio with a zero mean, or a geometric mean over a value not above
// zero, is not defined.
func TestPairedBootstrapRatio(t *testing.T) {
	values := []eval.LiveTaskValues{{B: []float64{2, 4}, A: []float64{1, 3}}, {B: []float64{6}, A: []float64{4}}}
	d := eval.NewLiveDraws([]string{"a", "b"}, 500, eval.LiveBootstrapSeed)
	iv, ok := eval.PairedBootstrapRatio(values, eval.LiveRatioOfMeans, d, 0.95)
	require.True(t, ok)
	require.InDelta(t, (12.0/3)/(8.0/3), iv.Estimate, 1e-12)
	require.LessOrEqual(t, iv.Low, iv.Estimate)
	require.GreaterOrEqual(t, iv.High, iv.Estimate)
	geo, ok := eval.PairedBootstrapRatio(values, eval.LiveGeometricMeanRatio, d, 0.95)
	require.True(t, ok)
	require.InDelta(t, math.Cbrt(2*4*6)/math.Cbrt(1*3*4), geo.Estimate, 1e-12)

	_, ok = eval.PairedBootstrapRatio([]eval.LiveTaskValues{{B: []float64{1}, A: []float64{0}}}, eval.LiveRatioOfMeans,
		eval.NewLiveDraws([]string{"a"}, 10, 1), 0.95)
	require.False(t, ok, "a zero comparator mean defines no ratio")
	_, ok = eval.PairedBootstrapRatio([]eval.LiveTaskValues{{B: []float64{1}, A: []float64{0}}}, eval.LiveGeometricMeanRatio,
		eval.NewLiveDraws([]string{"a"}, 10, 1), 0.95)
	require.False(t, ok)
}

// TestPairedBootstrapDiff_Calibration (W6 item 2): under no effect at the planned 24 tasks, the
// one-sided rejection rate of "the lower bound is above 0" is within 0.020 of its nominal 0.025.
// R = 4,000 replications, B = 1,000 (decision D-c: a smaller B than production keeps the test well
// under 30 s, about 1 s here; production's B = 10,000 is pinned above). It measures 0.032; the binomial
// SE of a rate near 0.03 at R = 4,000 is about 0.003, so the tolerance's nearer edge (0.045) sits about
// 4.6 SEs above the measured rate, and the band admits the known mild anti-conservatism at 24 tasks
// (0.023–0.036, plan §9.2). The test is deterministic (fixed seeds).
func TestPairedBootstrapDiff_Calibration(t *testing.T) {
	const r, b, k, m = 4000, 1000, 24, 14
	rng := rand.New(rand.NewPCG(31, 1))
	tasks := make([]string, k)
	for i := range tasks {
		tasks[i] = fmt.Sprintf("t%02d", i)
	}
	rejected := 0
	for rep := range r {
		counts := make([]eval.LiveTaskCounts, k)
		for i := range counts {
			p := 1 / (1 + math.Exp(-(0.4 + rng.NormFloat64()))) // a task's recovery rate, shared by both arms
			for range m {
				if rng.Float64() < p {
					counts[i].KB++
				}
				if rng.Float64() < p {
					counts[i].KA++
				}
			}
			counts[i].NB, counts[i].NA = m, m
		}
		iv, ok := eval.PairedBootstrapDiff(counts, eval.NewLiveDraws(tasks, b, uint64(1000+rep)), 0.95)
		require.True(t, ok)
		if iv.Low > 0 {
			rejected++
		}
	}
	rate := float64(rejected) / r
	t.Logf("one-sided rejection rate under the null at %d tasks: %.3f", k, rate)
	require.InDelta(t, 0.025, rate, 0.020)
}

// efficiencyRun simulates one run of a cost (or time) metric under R5's model (efficiency design §4.9):
// per trial log X = a_k + 1[steer]·(log θ + d_k) + e, a_k ~ N(0, 0.5²) shared by both arms,
// d_k ~ N(0, 0.10²), e ~ N(0, σ_w²); k tasks and m trials per task per arm.
func efficiencyRun(rng *rand.Rand, k, m int, theta, sigmaW float64) []eval.LiveTaskValues {
	out := make([]eval.LiveTaskValues, k)
	for i := range out {
		a := 0.5 * rng.NormFloat64()
		d := 0.10 * rng.NormFloat64()
		for range m {
			out[i].A = append(out[i].A, math.Exp(a+sigmaW*rng.NormFloat64()))
			out[i].B = append(out[i].B, math.Exp(a+math.Log(theta)+d+sigmaW*rng.NormFloat64()))
		}
	}
	return out
}

// TestPairedBootstrapRatio_Calibration (amendment 4's calibration test): at 24 tasks × 14 trials per
// arm, σ_w 0.25 and a true ratio of 1.10 — the margin itself — the harm side's one-sided rate
// (L > 1.10) lies in [0.010, 0.060] and the non-inferiority side's (U < 1.10) in [0.010, 0.050].
// R = 4,000 replications and B = 1,000 resamples, above the design's minimums of 1,000 (D-c keeps the
// test under 30 s; about 2 s here). It measures 0.048 and 0.028 (the coordinator's re-run gave
// 0.044–0.050 and 0.017–0.039). At R = 4,000 the binomial SE is about 0.0034 and 0.0026, so the nearest
// band edge (0.060 on the harm side) sits about 3.5 SEs from the measured rate, and every other edge
// further (review TESTS-08 corrected the earlier claim of two SEs at R = 1,000). Deterministic.
func TestPairedBootstrapRatio_Calibration(t *testing.T) {
	const r, b, k, m = 4000, 1000, 24, 14
	rng := rand.New(rand.NewPCG(32, 1))
	tasks := make([]string, k)
	for i := range tasks {
		tasks[i] = fmt.Sprintf("t%02d", i)
	}
	harm, ni := 0, 0
	for rep := range r {
		values := efficiencyRun(rng, k, m, 1.10, 0.25)
		iv, ok := eval.PairedBootstrapRatio(values, eval.LiveRatioOfMeans, eval.NewLiveDraws(tasks, b, uint64(5000+rep)), 0.95)
		require.True(t, ok)
		if iv.Low > 1.10 {
			harm++
		}
		if iv.High < 1.10 {
			ni++
		}
	}
	harmRate, niRate := float64(harm)/r, float64(ni)/r
	t.Logf("at a true ratio of 1.10: harm side %.3f, non-inferiority side %.3f", harmRate, niRate)
	require.GreaterOrEqual(t, harmRate, 0.010)
	require.LessOrEqual(t, harmRate, 0.060)
	require.GreaterOrEqual(t, niRate, 0.010)
	require.LessOrEqual(t, niRate, 0.050)
}

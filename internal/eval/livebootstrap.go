package eval

// ── The paired task bootstrap (V7 W6 item 2, ruling R29; amendment 4) ──────────────────────────
//
// Trials cluster within tasks, so every Stage A decision resamples tasks, jointly across arms: a
// resampled task brings all its trials in every arm. One draw matrix — B resamples of the K tasks,
// drawn with replacement from a fixed seed — serves every decision of a run, the recovery and task
// tests and amendment 4's cost and time ratios alike. Each decision interval is the two-sided
// percentile interval of its statistic over the resamples.

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
)

// The pre-registered bootstrap: B resamples from a fixed seed (W6 item 2).
const (
	LiveBootstrapB    = 10000
	LiveBootstrapSeed = uint64(20261016)
)

// liveDrawStream is the PCG stream the draws use beside the seed.
const liveDrawStream = 0x5157_7061_636b_4236

// LiveDraws is a paired task bootstrap's draw matrix: Idx[b] is resample b's K task indices into
// Tasks, drawn with replacement.
type LiveDraws struct {
	Tasks []string
	Idx   [][]int32
}

// NewLiveDraws draws b resamples of tasks (sorted, so the matrix does not depend on input order) from
// seed.
func NewLiveDraws(tasks []string, b int, seed uint64) LiveDraws {
	sorted := slices.Clone(tasks)
	slices.Sort(sorted)
	d := LiveDraws{Tasks: slices.Compact(sorted)}
	k := len(d.Tasks)
	if k == 0 || b <= 0 {
		return d
	}
	rng := rand.New(rand.NewPCG(seed, liveDrawStream))
	d.Idx = make([][]int32, b)
	flat := make([]int32, b*k)
	for i := range d.Idx {
		row := flat[i*k : (i+1)*k]
		for j := range row {
			row[j] = int32(rng.IntN(k))
		}
		d.Idx[i] = row
	}
	return d
}

// index maps each task to its position in the draws.
func (d LiveDraws) index() map[string]int {
	m := make(map[string]int, len(d.Tasks))
	for i, t := range d.Tasks {
		m[t] = i
	}
	return m
}

// LiveInterval is a statistic's full-sample estimate and its paired-bootstrap percentile interval.
type LiveInterval struct {
	Estimate float64 `json:"estimate"`
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
	// P20 is the resamples' 20th percentile: S's pre-registered fallback reads it as a one-sided
	// 80 % lower bound (plan §9.4, revision-4 review V13).
	P20 float64 `json:"p20"`
	// Resamples is how many of the B resamples defined the statistic (a resample with no trial of
	// an arm defines none).
	Resamples int `json:"resamples"`
}

// LiveTaskCounts is one task's successes and trials in the treated arm (B) and the comparator (A).
type LiveTaskCounts struct {
	KB, NB, KA, NA int
}

// PairedBootstrapDiff is the difference of pooled proportions, B − A, with its interval at
// confidence over draws; byTask is aligned with draws.Tasks. False when the full sample leaves an
// arm with no trial.
func PairedBootstrapDiff(byTask []LiveTaskCounts, d LiveDraws, confidence float64) (LiveInterval, bool) {
	stat := func(mult []int) (float64, bool) {
		var kb, nb, ka, na int
		for i, m := range mult {
			if m == 0 {
				continue
			}
			c := byTask[i]
			kb, nb, ka, na = kb+m*c.KB, nb+m*c.NB, ka+m*c.KA, na+m*c.NA
		}
		if nb == 0 || na == 0 {
			return 0, false
		}
		return float64(kb)/float64(nb) - float64(ka)/float64(na), true
	}
	return pairedBootstrap(len(byTask), d, confidence, stat)
}

// LiveTaskValues is one task's per-trial values in the treated arm (B) and the comparator (A).
type LiveTaskValues struct {
	B, A []float64
}

// LiveRatioAgg names how PairedBootstrapRatio aggregates each arm's values.
type LiveRatioAgg int

// The aggregates: the ratio of pooled means (decided, amendment 4) and the ratio of geometric means
// (reported).
const (
	LiveRatioOfMeans LiveRatioAgg = iota
	LiveGeometricMeanRatio
)

// PairedBootstrapRatio is the ratio mean_B / mean_A of agg over the resampled tasks' pooled trials,
// with its interval at confidence, on the same draws as the recovery tests; byTask is aligned with
// draws.Tasks. False when the full sample cannot define it: an arm with no value, a zero mean, or
// for the geometric mean any value not above zero.
func PairedBootstrapRatio(byTask []LiveTaskValues, agg LiveRatioAgg, d LiveDraws, confidence float64) (LiveInterval, bool) {
	type sums struct {
		sb, sa float64
		nb, na int
	}
	per := make([]sums, len(byTask))
	for i, tv := range byTask {
		s := sums{nb: len(tv.B), na: len(tv.A)}
		for _, x := range tv.B {
			if agg == LiveGeometricMeanRatio {
				if x <= 0 {
					return LiveInterval{}, false
				}
				x = math.Log(x)
			}
			s.sb += x
		}
		for _, x := range tv.A {
			if agg == LiveGeometricMeanRatio {
				if x <= 0 {
					return LiveInterval{}, false
				}
				x = math.Log(x)
			}
			s.sa += x
		}
		per[i] = s
	}
	stat := func(mult []int) (float64, bool) {
		var sb, sa float64
		var nb, na int
		for i, m := range mult {
			if m == 0 {
				continue
			}
			p := per[i]
			sb, sa = sb+float64(m)*p.sb, sa+float64(m)*p.sa
			nb, na = nb+m*p.nb, na+m*p.na
		}
		if nb == 0 || na == 0 {
			return 0, false
		}
		mb, ma := sb/float64(nb), sa/float64(na)
		if agg == LiveGeometricMeanRatio {
			return math.Exp(mb - ma), true
		}
		if ma <= 0 {
			return 0, false
		}
		return mb / ma, true
	}
	return pairedBootstrap(len(byTask), d, confidence, stat)
}

// PairedBootstrapMedianDiff is the difference of the arms' pooled medians, B − A, with its interval
// (the steer − classic compaction-duration row, reported).
func PairedBootstrapMedianDiff(byTask []LiveTaskValues, d LiveDraws, confidence float64) (LiveInterval, bool) {
	var bufB, bufA []float64
	stat := func(mult []int) (float64, bool) {
		bufB, bufA = bufB[:0], bufA[:0]
		for i, m := range mult {
			for range m {
				bufB = append(bufB, byTask[i].B...)
				bufA = append(bufA, byTask[i].A...)
			}
		}
		if len(bufB) == 0 || len(bufA) == 0 {
			return 0, false
		}
		sort.Float64s(bufB)
		sort.Float64s(bufA)
		return median(bufB) - median(bufA), true
	}
	return pairedBootstrap(len(byTask), d, confidence, stat)
}

// median is the middle of an ascending, non-empty slice (the mean of the two middle values for an
// even length).
func median(s []float64) float64 {
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// pairedBootstrap evaluates stat on the full sample (every task once) and on every resample of d,
// given each task's multiplicity, and returns the percentile interval of the defined resamples.
func pairedBootstrap(k int, d LiveDraws, confidence float64, stat func(mult []int) (float64, bool)) (LiveInterval, bool) {
	if k != len(d.Tasks) || k == 0 {
		return LiveInterval{}, false
	}
	mult := make([]int, k)
	for i := range mult {
		mult[i] = 1
	}
	est, ok := stat(mult)
	if !ok {
		return LiveInterval{}, false
	}
	vals := make([]float64, 0, len(d.Idx))
	for _, row := range d.Idx {
		clear(mult)
		for _, j := range row {
			mult[j]++
		}
		if v, ok := stat(mult); ok {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return LiveInterval{}, false
	}
	sort.Float64s(vals)
	alpha := 1 - confidence
	return LiveInterval{
		Estimate: est, Low: quantileSorted(vals, alpha/2), High: quantileSorted(vals, 1-alpha/2),
		P20: quantileSorted(vals, 0.20), Resamples: len(vals),
	}, true
}

// quantileSorted is the p-quantile of an ascending, non-empty slice by linear interpolation between
// order statistics (Hyndman and Fan type 7).
func quantileSorted(s []float64, p float64) float64 {
	if len(s) == 1 {
		return s[0]
	}
	h := p * float64(len(s)-1)
	lo := int(math.Floor(h))
	if lo >= len(s)-1 {
		return s[len(s)-1]
	}
	return s[lo] + (h-float64(lo))*(s[lo+1]-s[lo])
}

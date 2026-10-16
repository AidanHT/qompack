package eval

// ── The sizing step (V7 W6 item 2, coordinator decision D-d; plan §9.3 item 2, amendment 4) ────
//
// `devtool live-eval --size-from <pilot summary.json>` starts nothing: from the pilot's summary and
// the task set's pre-registered Δs and ICC_int it gives each arm's n for the main run and a calendar
// estimate, and prints each efficiency test's approximate power at that n (which adds no trials).
//
//   - p₀ is the pilot's control arm (classic for H1, generic for H2), taken as the 80 % Wilson bound
//     nearer 0.5, which gives the larger n; p₁ = p₀ + Δ. S's p is the pilot's classic task-success
//     rate, taken the same way.
//   - H1, H2 (superiority): n₀ = (z₀.₀₂₅·√(2p̄(1−p̄)) + z₀.₂·√(p₀(1−p₀)+p₁(1−p₁)))² / Δ²;
//     S (non-inferiority, margin m): n₀ = 2p(1−p)(z₀.₀₂₅ + z₀.₂)² / m².
//   - n₀ is inflated by 1/(1 − u)², u the pilot's unserved share of steer's compactions (served-empty
//     included: it steered nothing), then by the design effect in its solved form
//     n = n₀(1 − ICC_int) / (1 − n₀·ICC_int/K); when n₀·ICC_int reaches K no n is enough.
//   - Each arm runs the largest n of the tests it is in, rounded up to a multiple of K; stock, report
//     only, runs n_H1.

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// The sizing's fixed levels: two-sided α = 0.05, power 0.8, the pilot rates' 80 % Wilson bound, and
// the efficiency power's task-by-arm spread τ (amendment 4 item 8).
var (
	liveZAlpha  = normalQuantile(1 - 0.05/2)
	liveZPower  = normalQuantile(0.8)
	liveZWilson = normalQuantile(1 - 0.20/2)
)

// LiveSizingTau is τ, the between-task spread of the log ratio the efficiency power assumes.
const LiveSizingTau = 0.10 //nomagic:allow amendment 4 item 8's pre-registered τ, not a config default

// LiveSizingNightHours is the planning night window when the run names none (plan §9.5).
const LiveSizingNightHours = 9.0

// SampleSizeSuperiority is n per arm to show p₁ > p₀ at two-sided α = 0.05 with power 0.8.
func SampleSizeSuperiority(p0, p1 float64) int {
	pbar := (p0 + p1) / 2
	num := liveZAlpha*math.Sqrt(2*pbar*(1-pbar)) + liveZPower*math.Sqrt(p0*(1-p0)+p1*(1-p1))
	return int(math.Ceil(num * num / ((p1 - p0) * (p1 - p0))))
}

// SampleSizeNonInferiority is n per arm to show non-inferiority at margin m when both rates are p.
func SampleSizeNonInferiority(p, margin float64) int {
	z := liveZAlpha + liveZPower
	return int(math.Ceil(2 * p * (1 - p) * z * z / (margin * margin)))
}

// SolvedDesignN inflates n0 by the design effect DE = 1 + (m − 1)·icc with m = n/k the final trials
// per task, solved for n: n = n0(1 − icc) / (1 − n0·icc/k), rounded up. False when n0·icc reaches k:
// no number of trials per task is then enough, and tasks must be added.
func SolvedDesignN(n0 int, icc float64, k int) (int, bool) {
	den := 1 - float64(n0)*icc/float64(k)
	if den <= 0 {
		return 0, false
	}
	return int(math.Ceil(float64(n0) * (1 - icc) / den)), true
}

// RoundToTasks rounds n up to a multiple of the task count.
func RoundToTasks(n, k int) int {
	if k <= 0 {
		return n
	}
	return (n + k - 1) / k * k
}

// wilsonNearHalf is the 80 % Wilson bound of k of n nearer 0.5.
func wilsonNearHalf(p Proportion) float64 {
	w := wilson(p.K, p.N, liveZWilson)
	if math.Abs(w.Low-0.5) < math.Abs(w.High-0.5) {
		return w.Low
	}
	return w.High
}

// LiveSizingTest is one decided test's size.
type LiveSizingTest struct {
	Test string   `json:"test"`
	Arms []string `json:"arms"`
	// P0 and P1 are the control rate and the rate to detect (P1 is 0 for S, whose P0 is its p).
	P0 float64 `json:"p0"`
	P1 float64 `json:"p1,omitempty"`
	// N0 is n per arm before inflation, NU after the unserved share, N after the design effect.
	N0 int `json:"n0"`
	NU int `json:"nu"`
	N  int `json:"n"`
}

// LiveEfficiencyPower is one efficiency test's approximate power at the planned n (amendment 4 item
// 8): SE ≈ √(2σ_w²/(K·m) + τ²/K) on the log ratio.
type LiveEfficiencyPower struct {
	Metric string  `json:"metric"`
	SigmaW float64 `json:"sigma_w"`
	SE     float64 `json:"se"`
	// HarmAt125 is P(harm shown) at a true ratio of 1.25; FalseHarmAt1 at 1.0; NIAt1 the chance the
	// non-inferiority bound (H3 for cost, the reported time row for T) holds at 1.0.
	HarmAt125    float64 `json:"harm_at_1_25"`
	FalseHarmAt1 float64 `json:"false_harm_at_1"`
	NIAt1        float64 `json:"non_inferiority_at_1"`
}

// LiveCalendar is the sizing's calendar estimate from the pilot's per-trial wall time and
// subscription use.
type LiveCalendar struct {
	Trials int     `json:"trials"`
	Hours  float64 `json:"hours"`
	Nights float64 `json:"nights"`
	// FiveHourWindows is the five-hour windows' worth of allowance the trials use at the gate, and
	// SevenDayWeeks the weeks of seven-day allowance at the ceiling; 0 when the pilot recorded none.
	FiveHourWindows float64 `json:"five_hour_windows"`
	SevenDayWeeks   float64 `json:"seven_day_weeks"`
	// Weeks is the larger of the nights over seven and the seven-day weeks.
	Weeks float64 `json:"weeks"`
}

// LiveSizing is the sizing step's result.
type LiveSizing struct {
	Tasks      int                   `json:"tasks"`
	ICCInt     float64               `json:"icc_int"`
	U          float64               `json:"unserved_share"`
	Tests      []LiveSizingTest      `json:"tests"`
	ArmN       map[string]int        `json:"arm_n"`
	Calendar   LiveCalendar          `json:"calendar"`
	Efficiency []LiveEfficiencyPower `json:"efficiency,omitempty"`
	Notes      []string              `json:"notes,omitempty"`
}

// LiveSizingPacing is what the calendar estimate paces on: the five-hour gate, the seven-day ceiling
// and the night window's length in hours.
type LiveSizingPacing struct {
	FiveHourGate, SevenDayCeiling, NightHours float64
}

// SizeLive sizes the main run from a pilot's summary over a task set of k tasks (the held-out tasks
// included, which the main run runs).
func SizeLive(a LiveAnalysis, k int, pilot LiveSummary, pace LiveSizingPacing) (LiveSizing, error) {
	if k <= 0 {
		return LiveSizing{}, fmt.Errorf("eval: sizing needs the task set's task count")
	}
	if a.DeltaH1 <= 0 || a.DeltaH2 <= 0 || a.NonInferiorityMargin <= 0 {
		return LiveSizing{}, fmt.Errorf("eval: sizing needs the task set's analysis.delta_h1, delta_h2 and " +
			"non_inferiority_margin (plan §9.3 item 2)")
	}
	out := LiveSizing{Tasks: k, ICCInt: a.ICCInt, ArmN: map[string]int{}}
	if st, ok := pilot.Arms[ArmSteer]; ok && st.Brief != nil {
		out.U = st.Brief.UnservedShare + st.Brief.ServedEmptyShare
	} else {
		out.Notes = append(out.Notes, "the pilot records no steer brief outcomes, so the unserved share u is taken as 0")
	}
	if out.U >= 1 {
		return out, fmt.Errorf("eval: the pilot's steer arm served no brief (u = %.2f); no n is enough", out.U)
	}
	size := func(test string, arms []string, p0, p1 float64, n0 int) error {
		nu := int(math.Ceil(float64(n0) / ((1 - out.U) * (1 - out.U))))
		n, ok := SolvedDesignN(nu, a.ICCInt, k)
		if !ok {
			return fmt.Errorf("eval: %s: n₀ %d × ICC_int %.2f reaches the %d tasks, so no number of trials per task is "+
				"enough: add tasks", test, nu, a.ICCInt, k)
		}
		out.Tests = append(out.Tests, LiveSizingTest{Test: test, Arms: arms, P0: p0, P1: p1, N0: n0, NU: nu, N: n})
		return nil
	}
	superiority := func(test, control string, delta float64, arms []string) error {
		c, ok := pilot.Arms[control]
		if !ok || c.Recovery.N == 0 {
			out.Notes = append(out.Notes, fmt.Sprintf("%s is not sized: the pilot has no %s recovery", test, control))
			return nil
		}
		p0 := wilsonNearHalf(c.Recovery)
		p1 := p0 + delta
		if p1 >= 1 {
			return fmt.Errorf("eval: %s: p₀ %.3f + Δ %.2f reaches 1", test, p0, delta)
		}
		return size(test, arms, p0, p1, SampleSizeSuperiority(p0, p1))
	}
	if err := superiority("H1", ArmClassic, a.DeltaH1, []string{ArmSteer, ArmClassic, ArmStock}); err != nil {
		return out, err
	}
	if err := superiority("H2", ArmGeneric, a.DeltaH2, []string{ArmSteer, ArmGeneric}); err != nil {
		return out, err
	}
	if c, ok := pilot.Arms[ArmClassic]; ok && c.TaskSuccess.N > 0 {
		p := wilsonNearHalf(c.TaskSuccess)
		if err := size("S", []string{ArmSteer, ArmClassic}, p, 0, SampleSizeNonInferiority(p, a.NonInferiorityMargin)); err != nil {
			return out, err
		}
	} else {
		out.Notes = append(out.Notes, "S is not sized: the pilot has no classic task success")
	}
	for _, t := range out.Tests {
		for _, arm := range t.Arms {
			out.ArmN[arm] = max(out.ArmN[arm], RoundToTasks(t.N, k))
		}
	}
	out.Calendar = liveCalendar(out.ArmN, pilot, pace)
	out.Efficiency = efficiencyPower(a, k, out.ArmN, pilot)
	return out, nil
}

// liveCalendar turns the arms' n into hours, nights and weeks from the pilot's per-trial wall time
// and utilization deltas.
func liveCalendar(armN map[string]int, pilot LiveSummary, pace LiveSizingPacing) LiveCalendar {
	var c LiveCalendar
	night := pace.NightHours
	if night <= 0 {
		night = LiveSizingNightHours
	}
	for arm, n := range armN {
		c.Trials += n
		as := pilot.Arms[arm]
		c.Hours += float64(n) * float64(as.MeanWallMS) / 3.6e6
		if u, ok := as.RateUse["five_hour"]; ok && pace.FiveHourGate > 0 {
			c.FiveHourWindows += float64(n) * u.Mean / pace.FiveHourGate
		}
		var week float64
		for w, u := range as.RateUse {
			if strings.HasPrefix(w, "seven_day") {
				week = math.Max(week, u.Mean)
			}
		}
		if pace.SevenDayCeiling > 0 {
			c.SevenDayWeeks += float64(n) * week / pace.SevenDayCeiling
		}
	}
	c.Nights = c.Hours / night
	c.Weeks = math.Max(c.Nights/7, c.SevenDayWeeks)
	return c
}

// efficiencyPower is each efficiency metric's approximate power at the planned steer and classic n.
func efficiencyPower(a LiveAnalysis, k int, armN map[string]int, pilot LiveSummary) []LiveEfficiencyPower {
	n := min(armN[ArmSteer], armN[ArmClassic])
	if n == 0 {
		return nil
	}
	m := a.efficiencyMargin()
	km := float64(n) // K·m, the arm's trials
	var out []LiveEfficiencyPower
	metrics := liveEfficiencyMetrics
	if a.Efficiency != nil {
		metrics = a.Efficiency.Metrics
	}
	for _, metric := range metrics {
		var sds []float64
		for _, arm := range []string{ArmSteer, ArmClassic} {
			if e := pilot.Arms[arm].Efficiency; e != nil {
				sd := e.LogSDWithinCost
				if metric == LiveMetricActiveTime {
					sd = e.LogSDWithinTime
				}
				if sd > 0 {
					sds = append(sds, sd)
				}
			}
		}
		if len(sds) == 0 {
			continue
		}
		var ss float64
		for _, sd := range sds {
			ss += sd * sd
		}
		sigma := math.Sqrt(ss / float64(len(sds)))
		se := math.Sqrt(2*sigma*sigma/km + LiveSizingTau*LiveSizingTau/float64(k))
		limit := math.Log(1 + m)
		out = append(out, LiveEfficiencyPower{
			Metric: metric, SigmaW: sigma, SE: se,
			HarmAt125:    normalCDF((math.Log(liveHarmPowerRatio)-limit)/se - liveZAlpha),
			FalseHarmAt1: normalCDF(-limit/se - liveZAlpha),
			NIAt1:        normalCDF(limit/se - liveZAlpha),
		})
	}
	slices.SortFunc(out, func(x, y LiveEfficiencyPower) int {
		return slices.Index(liveEfficiencyMetrics, x.Metric) - slices.Index(liveEfficiencyMetrics, y.Metric)
	})
	return out
}

// normalCDF is the standard normal distribution function.
func normalCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// liveHarmPowerRatio is the true ratio at which the sizing reports the guards' power to catch harm:
// a +25 % increase (plan §9.5).
const liveHarmPowerRatio = 1.25 //nomagic:allow the plan's reported +25 % harm, not a config default

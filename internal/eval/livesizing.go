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
	"maps"
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

// LiveSizingMinDF is the fewest degrees of freedom a pilot's within-task log-SD needs before the
// efficiency power uses it; below it the power uses §4.9's planning σ_w band (review I-4). The pilot
// runs about one trial per task per arm (plan §9.3 item 1), which leaves 0.
const LiveSizingMinDF = 10

// livePlanningSigmaW is the efficiency design's planning band for σ_w (DESIGN §4.9, pessimistic, from
// R5): cost 0.25 to 0.40, active time 0.35 to 0.55.
var livePlanningSigmaW = map[string][2]float64{
	LiveMetricCost:       {0.25, 0.40}, //nomagic:allow DESIGN §4.9's planning σ_w, not a config default
	LiveMetricActiveTime: {0.35, 0.55}, //nomagic:allow DESIGN §4.9's planning σ_w, not a config default
}

// LivePowerRow is the power at one σ_w: SE ≈ √(2σ_w²/(K·m) + τ²/K) on the log ratio; HarmAt125 is
// P(harm shown) at a true ratio of 1.25, FalseHarmAt1 at 1.0, and NIAt1 the chance the
// non-inferiority bound (H3 for cost, the reported time row for T) holds at 1.0.
type LivePowerRow struct {
	SigmaW       float64 `json:"sigma_w"`
	SE           float64 `json:"se"`
	HarmAt125    float64 `json:"harm_at_1_25"`
	FalseHarmAt1 float64 `json:"false_harm_at_1"`
	NIAt1        float64 `json:"non_inferiority_at_1"`
}

// LiveEfficiencyPower is one efficiency metric's approximate power at a planned n (amendment 4 item
// 8). PilotSigmaW and PilotDF are the pilot's pooled within-task log-SD over steer and classic and its
// degrees of freedom; when PilotDF is below LiveSizingMinDF the rows use the planning band's two ends
// (Planning), else the pilot's σ_w.
type LiveEfficiencyPower struct {
	Metric      string  `json:"metric"`
	N           int     `json:"n"`
	PilotSigmaW float64 `json:"pilot_sigma_w"`
	PilotDF     int     `json:"pilot_df"`
	Planning    bool    `json:"planning"`
	// DFUnrecorded says a pilot arm gives a σ_w without its df: a summary written before df was
	// recorded (review S-R1-N4).
	DFUnrecorded bool           `json:"df_unrecorded,omitempty"`
	Rows         []LivePowerRow `json:"rows"`
}

// LiveCalendar is the sizing's calendar estimate from the pilot's per-trial wall time and
// subscription use.
type LiveCalendar struct {
	// Trials counts the main run's trials and the §9.8 Sonnet check's (SonnetTrials of them).
	Trials       int     `json:"trials"`
	SonnetTrials int     `json:"sonnet_trials"`
	Hours        float64 `json:"hours"`
	Nights       float64 `json:"nights"`
	// FiveHourWindows is the five-hour windows' worth of allowance the trials use at the gate, and
	// SevenDayWeeks the weeks of seven-day allowance at the ceiling; 0 when the pilot recorded none.
	FiveHourWindows float64 `json:"five_hour_windows"`
	SevenDayWeeks   float64 `json:"seven_day_weeks"`
	// Weeks is the larger of the nights over seven and the seven-day weeks.
	Weeks float64 `json:"weeks"`
}

// LiveRedesignCheck is §9.3 item 1's brief redesign rule read off the pilot (review STATS-1): steer's
// mean C and mean T over classic's, against the pre-registered ratio. Mean C is the arm's mean over
// its trials with a known C (a partial C counted at its lower bound).
type LiveRedesignCheck struct {
	CostRatio float64 `json:"cost_ratio"`
	TimeRatio float64 `json:"time_ratio"`
	// CostKnown and TimeKnown say each ratio could be computed; one is given even when the other
	// cannot be (review S-R1-N6).
	CostKnown bool    `json:"cost_known"`
	TimeKnown bool    `json:"time_known"`
	Threshold float64 `json:"threshold"`
	Fires     bool    `json:"fires"`
}

// LiveSonnetSizing is the §9.8 check's size: classic and steer at n_H1 each, from the Sonnet pilot's
// rates when one is given (FromSonnetPilot), else from the main pilot's as a planning stand-in.
type LiveSonnetSizing struct {
	FromSonnetPilot bool                  `json:"from_sonnet_pilot"`
	H1              LiveSizingTest        `json:"h1"`
	N               int                   `json:"n"`
	Efficiency      []LiveEfficiencyPower `json:"efficiency,omitempty"`
}

// LiveSizing is the sizing step's result.
type LiveSizing struct {
	Tasks      int                   `json:"tasks"`
	ICCInt     float64               `json:"icc_int"`
	U          float64               `json:"unserved_share"`
	Tests      []LiveSizingTest      `json:"tests"`
	ArmN       map[string]int        `json:"arm_n"`
	Sonnet     *LiveSonnetSizing     `json:"sonnet,omitempty"`
	Calendar   LiveCalendar          `json:"calendar"`
	Efficiency []LiveEfficiencyPower `json:"efficiency,omitempty"`
	Redesign   *LiveRedesignCheck    `json:"redesign,omitempty"`
	Notes      []string              `json:"notes,omitempty"`
}

// LiveSizingPacing is what the calendar estimate paces on: the five-hour gate, the seven-day ceiling
// and the night window's length in hours.
type LiveSizingPacing struct {
	FiveHourGate, SevenDayCeiling, NightHours float64
}

// SizeLive sizes the main run from a pilot's summary over a task set of k tasks (the held-out tasks
// included, which the main run runs), and the §9.8 Sonnet check from sonnet, the Sonnet pilot's
// summary, when given (else from the main pilot's rates, as a labelled stand-in).
func SizeLive(a LiveAnalysis, k int, pilot LiveSummary, sonnet *LiveSummary, pace LiveSizingPacing) (LiveSizing, error) {
	if k <= 0 {
		return LiveSizing{}, fmt.Errorf("eval: sizing needs the task set's task count")
	}
	if a.DeltaH1 <= 0 || a.DeltaH2 <= 0 || a.NonInferiorityMargin <= 0 {
		return LiveSizing{}, fmt.Errorf("eval: sizing needs the task set's analysis.delta_h1, delta_h2 and " +
			"non_inferiority_margin (plan §9.3 item 2)")
	}
	out := LiveSizing{Tasks: k, ICCInt: a.ICCInt, ArmN: map[string]int{}}
	u, note := unservedShare(pilot)
	if note != "" {
		out.Notes = append(out.Notes, note)
	}
	if u >= 1 {
		return out, fmt.Errorf("eval: the pilot's steer arm served no brief (u = %.2f); no n is enough", u)
	}
	out.U = u
	if h1, ok, err := sizeSuperiority("H1", pilot, ArmClassic, a.DeltaH1, u, a.ICCInt, k,
		[]string{ArmSteer, ArmClassic, ArmStock}); err != nil {
		return out, err
	} else if ok {
		out.Tests = append(out.Tests, h1)
	} else {
		out.Notes = append(out.Notes, "H1 is not sized: the pilot has no classic recovery")
	}
	if h2, ok, err := sizeSuperiority("H2", pilot, ArmGeneric, a.DeltaH2, u, a.ICCInt, k,
		[]string{ArmSteer, ArmGeneric}); err != nil {
		return out, err
	} else if ok {
		out.Tests = append(out.Tests, h2)
	} else {
		out.Notes = append(out.Notes, "H2 is not sized: the pilot has no generic recovery")
	}
	if c, ok := pilot.Arms[ArmClassic]; ok && c.TaskSuccess.N > 0 {
		p := wilsonNearHalf(c.TaskSuccess)
		s, err := inflate("S", []string{ArmSteer, ArmClassic}, p, 0, SampleSizeNonInferiority(p, a.NonInferiorityMargin), u,
			a.ICCInt, k)
		if err != nil {
			return out, err
		}
		out.Tests = append(out.Tests, s)
	} else {
		out.Notes = append(out.Notes, "S is not sized: the pilot has no classic task success")
	}
	for _, t := range out.Tests {
		for _, arm := range t.Arms {
			out.ArmN[arm] = max(out.ArmN[arm], RoundToTasks(t.N, k))
		}
	}
	if err := out.sizeSonnet(a, k, pilot, sonnet); err != nil {
		return out, err
	}
	out.Calendar = liveCalendar(out.ArmN, pilot, out.Sonnet, sonnet, pace)
	out.Efficiency = efficiencyPower(a, k, min(out.ArmN[ArmSteer], out.ArmN[ArmClassic]), pilot)
	out.Redesign = redesignCheck(a, pilot)
	if r := out.Redesign; r != nil && r.Fires {
		out.Notes = append(out.Notes, fmt.Sprintf("BRIEF REDESIGN RULE FIRES (plan §9.3 item 1): the pilot's steer/classic "+
			"ratio of mean C is %s and of mean T %s, and one exceeds %.2f; redesign the brief and re-pilot steer before "+
			"the main run", RatioText(r.CostRatio, r.CostKnown), RatioText(r.TimeRatio, r.TimeKnown), r.Threshold))
	}
	seen := map[string]bool{}
	for _, e := range append(append([]LiveEfficiencyPower{}, out.Efficiency...), sonnetPower(out.Sonnet)...) {
		key := fmt.Sprintf("%s/%d/%t", e.Metric, e.PilotDF, e.DFUnrecorded)
		if !e.Planning || seen[key] {
			continue // one note per metric and df, not one per power table (review S-R1-N4)
		}
		seen[key] = true
		band := livePlanningSigmaW[e.Metric]
		if e.DFUnrecorded {
			out.Notes = append(out.Notes, fmt.Sprintf("the pilot summary gives a within-task log-SD of %s without its df "+
				"(written before df was recorded), so its power uses the planning σ_w band %.2f–%.2f (efficiency design "+
				"§4.9)", e.Metric, band[0], band[1]))
			continue
		}
		out.Notes = append(out.Notes, fmt.Sprintf("the pilot's within-task log-SD of %s rests on %d df (fewer than %d; a "+
			"pilot of one trial per task per arm leaves 0), so its power uses the planning σ_w band %.2f–%.2f (efficiency "+
			"design §4.9); the pilot's own figure is printed beside it", e.Metric, e.PilotDF, LiveSizingMinDF, band[0], band[1]))
	}
	return out, nil
}

// unservedShare is the pilot's unserved share of steer's compactions, served-empty included (it
// steered nothing), and a note when the pilot recorded none.
func unservedShare(pilot LiveSummary) (float64, string) {
	if st, ok := pilot.Arms[ArmSteer]; ok && st.Brief != nil {
		return st.Brief.UnservedShare + st.Brief.ServedEmptyShare, ""
	}
	return 0, "the pilot records no steer brief outcomes, so the unserved share u is taken as 0"
}

// inflate applies the unserved share and the solved design effect to n0.
func inflate(test string, arms []string, p0, p1 float64, n0 int, u, icc float64, k int) (LiveSizingTest, error) {
	nu := int(math.Ceil(float64(n0) / ((1 - u) * (1 - u))))
	n, ok := SolvedDesignN(nu, icc, k)
	if !ok {
		return LiveSizingTest{}, fmt.Errorf("eval: %s: n₀ %d × ICC_int %.2f reaches the %d tasks, so no number of trials "+
			"per task is enough: add tasks", test, nu, icc, k)
	}
	return LiveSizingTest{Test: test, Arms: arms, P0: p0, P1: p1, N0: n0, NU: nu, N: n}, nil
}

// sizeSuperiority sizes a recovery superiority test from pilot's control arm; false when the pilot has
// no recovery for it.
func sizeSuperiority(test string, pilot LiveSummary, control string, delta, u, icc float64, k int,
	arms []string,
) (LiveSizingTest, bool, error) {
	c, ok := pilot.Arms[control]
	if !ok || c.Recovery.N == 0 {
		return LiveSizingTest{}, false, nil
	}
	p0 := wilsonNearHalf(c.Recovery)
	p1 := p0 + delta
	if p1 >= 1 {
		return LiveSizingTest{}, false, fmt.Errorf("eval: %s: p₀ %.3f + Δ %.2f reaches 1", test, p0, delta)
	}
	t, err := inflate(test, arms, p0, p1, SampleSizeSuperiority(p0, p1), u, icc, k)
	return t, err == nil, err
}

// sizeSonnet sizes the §9.8 check: classic and steer at n_H1 from the Sonnet pilot's rates, or from the
// main pilot's as a stand-in (review STATS-6).
func (out *LiveSizing) sizeSonnet(a LiveAnalysis, k int, pilot LiveSummary, sonnet *LiveSummary) error {
	src := pilot
	if sonnet != nil {
		src = *sonnet
	}
	u, note := unservedShare(src)
	if note != "" && sonnet != nil {
		// Kept and labelled (review S-R1-N2): the main pilot's own note is already in the notes.
		out.Notes = append(out.Notes, "the Sonnet check: "+strings.Replace(note, "the pilot", "the Sonnet pilot", 1))
	}
	if u >= 1 {
		return fmt.Errorf("eval: the Sonnet pilot's steer arm served no brief (u = %.2f); no n is enough", u)
	}
	h1, ok, err := sizeSuperiority("H1 (Sonnet check)", src, ArmClassic, a.DeltaH1, u, a.ICCInt, k,
		[]string{ArmSteer, ArmClassic})
	if err != nil {
		return err
	}
	if !ok {
		out.Notes = append(out.Notes, "the Sonnet check (plan §9.8) is not sized: its pilot has no classic recovery, so "+
			"it is left out of the calendar, and the four-week trigger (plan §9.3 item 3) does not count its trials")
		return nil
	}
	n := RoundToTasks(h1.N, k)
	out.Sonnet = &LiveSonnetSizing{
		FromSonnetPilot: sonnet != nil, H1: h1, N: n, Efficiency: efficiencyPower(a, k, n, src),
	}
	if sonnet == nil {
		out.Notes = append(out.Notes, "the Sonnet check (plan §9.8) is sized from this pilot's rates as a planning "+
			"stand-in; size it from the Sonnet pilot's summary with --size-sonnet")
	}
	return nil
}

// sonnetPower is the Sonnet check's power rows, or none.
func sonnetPower(s *LiveSonnetSizing) []LiveEfficiencyPower {
	if s == nil {
		return nil
	}
	return s.Efficiency
}

// redesignCheck reads §9.3 item 1's brief redesign rule off the pilot: steer's mean C and mean T over
// classic's; nil when the block pins no ratio or either arm lacks the figures.
func redesignCheck(a LiveAnalysis, pilot LiveSummary) *LiveRedesignCheck {
	if a.Efficiency == nil {
		return nil
	}
	s, c := pilot.Arms[ArmSteer].Efficiency, pilot.Arms[ArmClassic].Efficiency
	if s == nil || c == nil {
		return nil
	}
	r := &LiveRedesignCheck{Threshold: a.Efficiency.PilotRedesignRatio}
	if c.MeanCostUSD > 0 && s.CostComplete+s.CostPartial > 0 {
		// A steer arm with no known C has a mean C of 0, which is no ratio (review R2-N4).
		r.CostRatio, r.CostKnown = s.MeanCostUSD/c.MeanCostUSD, true
	}
	if c.MeanActiveMS > 0 {
		r.TimeRatio, r.TimeKnown = s.MeanActiveMS/c.MeanActiveMS, true
	}
	if !r.CostKnown && !r.TimeKnown {
		return nil
	}
	r.Fires = (r.CostKnown && r.CostRatio > r.Threshold) || (r.TimeKnown && r.TimeRatio > r.Threshold)
	return r
}

// liveCalendar turns the arms' n, and the Sonnet check's, into hours, nights and weeks from the
// pilots' per-trial wall time and utilization deltas.
func liveCalendar(armN map[string]int, pilot LiveSummary, check *LiveSonnetSizing, sonnet *LiveSummary,
	pace LiveSizingPacing,
) LiveCalendar {
	var c LiveCalendar
	night := pace.NightHours
	if night <= 0 {
		night = LiveSizingNightHours
	}
	add := func(as ArmSummary, n int) {
		c.Trials += n
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
	for _, arm := range slices.Sorted(maps.Keys(armN)) {
		add(pilot.Arms[arm], armN[arm])
	}
	if check != nil {
		src := pilot
		if sonnet != nil {
			src = *sonnet
		}
		for _, arm := range []string{ArmClassic, ArmSteer} {
			add(src.Arms[arm], check.N)
			c.SonnetTrials += check.N
		}
	}
	c.Nights = c.Hours / night
	c.Weeks = math.Max(c.Nights/7, c.SevenDayWeeks)
	return c
}

// efficiencyPower is each efficiency metric's approximate power at n trials per arm of steer and
// classic: from the pilot's pooled within-task σ_w when it rests on enough df, else from the planning
// band's two ends.
func efficiencyPower(a LiveAnalysis, k, n int, pilot LiveSummary) []LiveEfficiencyPower {
	if n == 0 {
		return nil
	}
	limit := math.Log(1 + a.efficiencyMargin())
	metrics := liveEfficiencyMetrics
	if a.Efficiency != nil {
		metrics = a.Efficiency.Metrics
	}
	row := func(sigma float64) LivePowerRow {
		se := math.Sqrt(2*sigma*sigma/float64(n) + LiveSizingTau*LiveSizingTau/float64(k))
		return LivePowerRow{
			SigmaW: sigma, SE: se,
			HarmAt125:    normalCDF((math.Log(liveHarmPowerRatio)-limit)/se - liveZAlpha),
			FalseHarmAt1: normalCDF(-limit/se - liveZAlpha),
			NIAt1:        normalCDF(limit/se - liveZAlpha),
		}
	}
	var out []LiveEfficiencyPower
	for _, metric := range liveEfficiencyMetrics {
		if !slices.Contains(metrics, metric) {
			continue
		}
		var ss float64
		unrecorded := false
		var df int
		for _, arm := range []string{ArmSteer, ArmClassic} {
			e := pilot.Arms[arm].Efficiency
			if e == nil {
				continue
			}
			sd, d := e.LogSDWithinCost, e.LogSDWithinCostDF
			if metric == LiveMetricActiveTime {
				sd, d = e.LogSDWithinTime, e.LogSDWithinTimeDF
			}
			ss += float64(d) * sd * sd
			unrecorded = unrecorded || (sd > 0 && d == 0)
			df += d
		}
		p := LiveEfficiencyPower{Metric: metric, N: n, PilotDF: df, DFUnrecorded: unrecorded && df == 0}
		if df > 0 {
			p.PilotSigmaW = math.Sqrt(ss / float64(df))
		}
		if df < LiveSizingMinDF {
			p.Planning = true
			for _, s := range livePlanningSigmaW[metric] {
				p.Rows = append(p.Rows, row(s))
			}
		} else {
			p.Rows = []LivePowerRow{row(p.PilotSigmaW)}
		}
		out = append(out, p)
	}
	return out
}

// normalCDF is the standard normal distribution function.
func normalCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// liveHarmPowerRatio is the true ratio at which the sizing reports the guards' power to catch harm:
// a +25 % increase (plan §9.5).
const liveHarmPowerRatio = 1.25 //nomagic:allow the plan's reported +25 % harm, not a config default

// RatioText renders a ratio, or "not computable" when it could not be computed.
func RatioText(r float64, known bool) string {
	if !known {
		return "not computable"
	}
	return fmt.Sprintf("%.3f", r)
}

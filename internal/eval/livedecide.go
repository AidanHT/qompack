package eval

// ── The Stage A decisions (V7 W6 item 2; amendment 4, ruling R43) ──────────────────────────────
//
// They replace the two-arm DecideLive for a run of the Stage A arms. Every decision rests on one
// paired task bootstrap (livebootstrap.go), B = 10,000 resamples from a fixed seed, deciding
// alike:
//
//   - the fixed sequence of benefit claims, family-wise α = 0.05: H1 (steer > classic on recovery),
//     then H2 (steer > generic on recovery), then H3 (steer's cost non-inferior to classic's);
//     the first one not shown stops it, and the rest read "not tested";
//   - S, the ship condition: steer non-inferior to classic on task success, the interval's lower
//     bound above −margin (or, under the pre-registered fallback, the 20th percentile above it);
//   - harm: steer below classic on task success or on constraint-clean rate, the interval wholly
//     below zero; and amendment 4's gated cost and time guards, harm shown when the ratio's lower
//     bound exceeds 1 + M, "indeterminate" when the limited-trial sensitivity flips one or when the
//     ratio cannot be computed (fail-closed; ruling on review STATS-4).
//
// A run on another model than the pre-registered one (the Sonnet check, plan §9.8) decides only
// harm; its benefit intervals are reported, "measured on another model, not decided". Newcombe
// intervals are reported beside the bootstrap's, never decided. Labels follow §9.4: "shown", "not
// shown", "not tested", "harm shown", "indeterminate"; an ungated guard (classic vs stock) reads
// "overhead shown", a docs label and never a §9.6 harm; never "harmed" without harm shown, and never
// "did as well".

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

// The metrics a Stage A test compares.
const (
	LiveMetricRecovery        = "recovery"
	LiveMetricTaskSuccess     = "task_success"
	LiveMetricConstraintClean = "constraint_clean"
	LiveMetricCost            = "cost"
	LiveMetricActiveTime      = "active_time"
)

// The §9.4 labels, and the label of a row that is reported and never decided.
const (
	LiveLabelShown         = "shown"
	LiveLabelNotShown      = "not shown"
	LiveLabelNotTested     = "not tested"
	LiveLabelHarmShown     = "harm shown"
	LiveLabelIndeterminate = "indeterminate"
	LiveLabelReported      = "reported"
	// LiveLabelOverheadShown is an ungated guard's "harm shown" (classic vs stock): a label for the docs
	// and a defect item, never a §9.6 harm.
	LiveLabelOverheadShown = "overhead shown"
	// LiveLabelNotMeasured marks a §9.4 row this harness does not produce.
	LiveLabelNotMeasured = "not measured"
)

// The Stage A verdicts (LiveDecision.Verdict of a Stage A run).
const (
	// LiveVerdictShip: H1, H2 and S shown, no harm shown (and H3 shown when the owner made it a ship
	// condition): F1 ships on, subject to the other model's harm check.
	LiveVerdictShip = "ship"
	// LiveVerdictHarm: harm shown on a harm metric, or a gated efficiency guard indeterminate.
	LiveVerdictHarm = "harm-shown"
	// LiveVerdictNoShip: no harm shown, but the benefit or S was not shown: nothing new ships on.
	LiveVerdictNoShip = "no-ship"
	// LiveVerdictNoHarm: a run on another model than the pre-registered one, which decides only harm,
	// showed none.
	LiveVerdictNoHarm = "no-harm-shown"
	// LiveVerdictNotApplicable: the decisions could not be made (an arm missing, a stopped run).
	LiveVerdictNotApplicable = "not-applicable"
)

// The sensitivity analyses a ratio test carries beside its primary interval.
const (
	LiveSensLimited   = "limited"
	LiveSensCompleted = "completed"
	LiveSensObserved  = "observed"
	LiveSensGeometric = "geometric"
)

// LiveTest is one Stage A test, decided or reported.
type LiveTest struct {
	ID         string `json:"id"`
	Treated    string `json:"treated"`
	Comparator string `json:"comparator"`
	Metric     string `json:"metric"`
	// Statistic is "difference of pooled proportions" or "ratio of pooled means" (B over A).
	Statistic string        `json:"statistic"`
	Interval  *LiveInterval `json:"interval,omitempty"`
	// Newcombe is the unpaired hybrid score interval for a difference of proportions: reported beside
	// the bootstrap's, never decided.
	Newcombe *Difference `json:"newcombe,omitempty"`
	Rule     string      `json:"rule"`
	// Gated says the test blocks shipping (a harm test or a gated guard) or decides it (H1, H2, S).
	Gated  bool   `json:"gated"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	// Sensitivity is the ratio's intervals under the limited-trial sensitivity, on completed trials
	// only, on observed costs only and as a ratio of geometric means.
	Sensitivity map[string]*LiveInterval `json:"sensitivity,omitempty"`
}

// LiveUtilizationFit is a least-squares line of a subscription window's per-trial utilization delta
// against the trial's cost C: how well C stands in for the allowance (amendment 4, reported).
type LiveUtilizationFit struct {
	Window    string  `json:"window"`
	N         int     `json:"n"`
	Slope     float64 `json:"slope_per_usd"`
	Intercept float64 `json:"intercept"`
	R2        float64 `json:"r2"`
}

// LiveStageA is a Stage A run's decisions and reported rows.
type LiveStageA struct {
	B          int     `json:"b"`
	Seed       uint64  `json:"seed"`
	Tasks      int     `json:"tasks"`
	Confidence float64 `json:"confidence"`
	// Decided says the run is on the pre-registered model and decides the benefit claims and S; a run
	// on another model decides only harm (plan §9.8).
	Decided bool `json:"decided"`
	// Tests is every pre-registered test in order: H1, H2, S, the harm tests, the efficiency guards
	// and labels, H3.
	Tests []LiveTest `json:"tests"`
	// Reported is every row §9.4 reports and never decides.
	Reported        []LiveTest           `json:"reported,omitempty"`
	UtilizationFits []LiveUtilizationFit `json:"utilization_fits,omitempty"`
	// Outcome is the §9.6 row the decisions select, and Decision the run's verdict.
	Outcome string `json:"outcome"`
	// Interim says a stop or pause left the run without a decision: the tests are an interim description
	// (review I-2); a not-applicable record that is not interim is one that could not be decided at all.
	Interim  bool         `json:"interim,omitempty"`
	Decision LiveDecision `json:"decision"`
	Notes    []string     `json:"notes,omitempty"`
	// NotMeasured names the §9.4 reported rows this harness does not produce, so a pre-registration
	// cannot set a threshold on a quantity no run records (review CLI-4).
	NotMeasured []string `json:"not_measured,omitempty"`
}

// Test returns the test with id, and whether it exists.
func (s *LiveStageA) Test(id string) (LiveTest, bool) {
	for _, t := range s.Tests {
		if t.ID == id {
			return t, true
		}
	}
	return LiveTest{}, false
}

// LiveArmEfficiency is one arm's cost C and active time T over its trials (amendment 4): how many
// trials have a complete, partial or unknown C, the mean C over the known ones, the mean T split into
// API and local time, the reached steps with no result, and the arm's compactions.
type LiveArmEfficiency struct {
	Trials        int     `json:"trials"`
	CostComplete  int     `json:"cost_complete"`
	CostPartial   int     `json:"cost_partial"`
	CostUnknown   int     `json:"cost_unknown"`
	MeanCostUSD   float64 `json:"mean_cost_usd"`
	MeanActiveMS  float64 `json:"mean_active_ms"`
	MeanAPIMS     float64 `json:"mean_api_ms"`
	MeanLocalMS   float64 `json:"mean_local_ms"`
	TimedOutSteps int     `json:"timed_out_steps"`
	// DurationMissing counts the reached steps whose result line carried no duration_ms: observed T
	// counts them as 0, a decision's treated arm at the cap.
	DurationMissing int `json:"duration_missing,omitempty"`
	// EstimateRatio is the repository's Estimate over the host's C, Σ estimate / Σ C over the trials with
	// both a recorded estimate and a complete C (§9.4, reported); EstimateRatioN counts them (0: no
	// estimate recorded) and EstimateLowerBound says one of those estimates is incomplete (review H-NEW-1).
	// The fields the fix rounds added are omitted when zero, so a V6 two-arm summary gains no zero key
	// (review R2-N6).
	EstimateRatio      float64 `json:"estimate_ratio,omitempty"`
	EstimateRatioN     int     `json:"estimate_ratio_n,omitempty"`
	EstimateLowerBound bool    `json:"estimate_lower_bound,omitempty"`
	// Compactions counts the arm's compactions; CompactionMS and PostTokens summarize their
	// compact_boundary duration_ms and post_tokens.
	Compactions  int              `json:"compactions"`
	CompactionMS LiveDistribution `json:"compaction_ms"`
	PostTokens   LiveDistribution `json:"post_tokens"`
	// LogSDWithinCost and LogSDWithinTime are the pooled within-task standard deviations of log C (over
	// complete costs) and of log T: the σ_w the sizing step's efficiency power uses.
	LogSDWithinCost float64 `json:"log_sd_within_cost"`
	LogSDWithinTime float64 `json:"log_sd_within_time"`
	// LogSDWithinCostDF and LogSDWithinTimeDF are their degrees of freedom: the trials beyond the first
	// in each task, summed (0 at one trial per task per arm, and then omitted: a σ_w above 0 always has
	// df above 0, so one without df is a summary written before df was recorded).
	LogSDWithinCostDF int `json:"log_sd_within_cost_df,omitempty"`
	LogSDWithinTimeDF int `json:"log_sd_within_time_df,omitempty"`
}

// armEfficiency aggregates one arm's C and T; nil for no trials.
func armEfficiency(ts []LiveTrial, capMS int64) *LiveArmEfficiency {
	if len(ts) == 0 {
		return nil
	}
	e := &LiveArmEfficiency{Trials: len(ts)}
	var cost, active, api, estSum, estCost float64
	var costN int
	var compMS, post []float64
	logC, logT := map[string][]float64{}, map[string][]float64{}
	for _, t := range ts {
		c, status := t.Cost()
		switch status {
		case LiveCostComplete:
			e.CostComplete++
			if c > 0 {
				logC[t.TaskID] = append(logC[t.TaskID], math.Log(c))
			}
		case LiveCostPartial:
			e.CostPartial++
		default:
			e.CostUnknown++
		}
		if status != LiveCostUnknown {
			cost += c
			costN++
		}
		tm := t.ActiveMS(capFor(t, capMS))
		e.DurationMissing += t.MissingDurations()
		if c, status := t.Cost(); t.Estimate != nil && status == LiveCostComplete && c > 0 {
			estSum += float64(t.Estimate.Micros) / 1e6
			estCost += c
			e.EstimateRatioN++
			e.EstimateLowerBound = e.EstimateLowerBound || !t.EstimateComplete
		}
		active += float64(tm)
		api += float64(t.APIMS())
		if tm > 0 {
			logT[t.TaskID] = append(logT[t.TaskID], math.Log(float64(tm)))
		}
		e.TimedOutSteps += t.TimedOutSteps()
		for _, cp := range t.Compactions {
			e.Compactions++
			compMS = append(compMS, float64(cp.DurationMS))
			post = append(post, float64(cp.PostTokens))
		}
	}
	if costN > 0 {
		e.MeanCostUSD = cost / float64(costN)
	}
	n := float64(len(ts))
	e.MeanActiveMS, e.MeanAPIMS = active/n, api/n
	e.MeanLocalMS = e.MeanActiveMS - e.MeanAPIMS
	e.CompactionMS, e.PostTokens = distributionOf(compMS), distributionOf(post)
	if estCost > 0 {
		e.EstimateRatio = estSum / estCost
	}
	e.LogSDWithinCost, e.LogSDWithinCostDF = pooledWithinSD(logC)
	e.LogSDWithinTime, e.LogSDWithinTimeDF = pooledWithinSD(logT)
	return e
}

// EstimateRatioText renders an arm's estimate over its host-reported cost for both reports (reviews
// H-NEW-1, R2-N3): the ratio over the trials with both values, with their count and a lower-bound mark;
// "not recorded" for an arm that has estimates but no ratio (no trial pairs one with a complete C, or
// the summary was written before the ratio existed); "n/a" for an arm with no estimate.
func EstimateRatioText(as ArmSummary) string {
	switch e := as.Efficiency; {
	case e != nil && e.EstimateRatioN > 0:
		bound := ""
		if e.EstimateLowerBound {
			bound = ", a lower bound"
		}
		return fmt.Sprintf("%.3f over %d trial(s)%s", e.EstimateRatio, e.EstimateRatioN, bound)
	case as.MeanEstimateMc > 0:
		return "not recorded (the arm has estimates, but none on a trial with a complete C, or the summary " +
			"predates the ratio)"
	}
	return "n/a (no estimate recorded)"
}

// pooledWithinSD is the pooled within-group standard deviation, over the groups with at least two
// values (summed in key order, so the result does not depend on map iteration), and its degrees of
// freedom; 0 and 0 when there are none.
func pooledWithinSD(groups map[string][]float64) (float64, int) {
	var ss float64
	var df int
	for _, k := range slices.Sorted(maps.Keys(groups)) {
		xs := groups[k]
		if len(xs) < 2 {
			continue
		}
		mean := 0.0
		for _, x := range xs {
			mean += x
		}
		mean /= float64(len(xs))
		for _, x := range xs {
			ss += (x - mean) * (x - mean)
		}
		df += len(xs) - 1
	}
	if df == 0 {
		return 0, 0
	}
	return math.Sqrt(ss / float64(df)), df
}

// stageA is one decision's inputs: the analysis, the shared draws, the scored trials by arm and the
// limited trials by slot.
type stageA struct {
	a       LiveAnalysis
	draws   LiveDraws
	index   map[string]int
	byArm   map[string][]LiveTrial
	limited map[string][]LiveTrial
	capMS   int64
	conf    float64
	margin  float64
}

// DecideStageA makes a Stage A run's decisions over its scored trials; limited are the trials a
// subscription limit cut short (excluded from the primary analysis, used by the limited-trial
// sensitivity of amendment 4's efficiency tests).
func DecideStageA(a LiveAnalysis, scored, limited []LiveTrial) *LiveStageA {
	var tasks []string
	byArm := map[string][]LiveTrial{}
	decided := len(scored) > 0
	for _, t := range scored {
		tasks = append(tasks, t.TaskID)
		byArm[t.Arm] = append(byArm[t.Arm], t)
		decided = decided && t.PreregisteredModel
	}
	conf := a.Confidence
	if conf <= 0 || conf >= 1 {
		conf = 0.95
	}
	s := stageA{
		a: a, draws: NewLiveDraws(tasks, LiveBootstrapB, LiveBootstrapSeed), byArm: byArm,
		limited: map[string][]LiveTrial{}, capMS: a.stepTimeoutMS(), conf: conf, margin: a.efficiencyMargin(),
	}
	s.index = s.draws.index()
	for _, t := range limited {
		s.limited[t.Arm] = append(s.limited[t.Arm], t)
	}
	out := &LiveStageA{
		B: LiveBootstrapB, Seed: LiveBootstrapSeed, Tasks: len(s.draws.Tasks), Confidence: conf, Decided: decided,
		NotMeasured: liveNotMeasured,
	}
	if len(byArm[ArmSteer]) == 0 || len(byArm[ArmClassic]) == 0 {
		out.Outcome = "the Stage A decisions need the steer and classic arms"
		out.Decision = LiveDecision{Verdict: LiveVerdictNotApplicable, Reason: fmt.Sprintf(
			"the Stage A decisions compare steer with classic, and this run has trials of %s", orNoArms(byArm))}
		out.Reported = s.reported()
		return out
	}
	out.Tests = s.tests(decided)
	out.Reported = s.reported()
	out.UtilizationFits = utilizationFits(scored)
	out.Outcome, out.Decision = s.verdict(out)
	if !decided {
		out.Notes = append(out.Notes, "this run is not on the pre-registered model: it decides only harm (plan §9.8), "+
			"and its benefit intervals are measured on another model, not decided")
	}
	return out
}

// orNoArms names the arms that ran, sorted.
func orNoArms(byArm map[string][]LiveTrial) string {
	arms := slices.Sorted(maps.Keys(byArm))
	if len(arms) == 0 {
		return "no arm"
	}
	return strings.Join(arms, ", ")
}

// tests runs every pre-registered test in order.
func (s stageA) tests(decided bool) []LiveTest {
	var out []LiveTest
	notDecided := "this run is not on the pre-registered model; it decides only harm (§9.8)"

	h1 := s.proportion("H1", ArmSteer, ArmClassic, LiveMetricRecovery, true)
	h1.Rule = "shown when the interval's lower bound is above 0"
	switch {
	case !decided:
		h1.Label, h1.Reason = LiveLabelNotTested, notDecided
	case h1.Interval != nil && h1.Interval.Low > 0:
		h1.Label, h1.Reason = LiveLabelShown, "steer recovers more than classic: "+intervalText(h1.Interval)
	default:
		h1.Label, h1.Reason = LiveLabelNotShown, "the interval does not lie above 0: "+intervalText(h1.Interval)
	}
	out = append(out, h1)

	h2 := s.proportion("H2", ArmSteer, ArmGeneric, LiveMetricRecovery, true)
	h2.Rule = "tested only if H1 is shown; shown when the interval's lower bound is above 0"
	switch {
	case !decided:
		h2.Label, h2.Reason = LiveLabelNotTested, notDecided
	case h1.Label != LiveLabelShown:
		h2.Label, h2.Reason = LiveLabelNotTested, "the fixed sequence stopped at H1"
	case len(s.byArm[ArmGeneric]) == 0:
		h2.Label, h2.Reason = LiveLabelNotTested, "the run has no generic arm"
	case h2.Interval != nil && h2.Interval.Low > 0:
		h2.Label, h2.Reason = LiveLabelShown, "steer recovers more than generic: "+intervalText(h2.Interval)
	default:
		h2.Label, h2.Reason = LiveLabelNotShown, "the interval does not lie above 0: "+intervalText(h2.Interval)
	}
	out = append(out, h2)

	sTest := s.proportion("S", ArmSteer, ArmClassic, LiveMetricTaskSuccess, true)
	m := s.a.NonInferiorityMargin
	bound, boundName := math.Inf(-1), "lower bound"
	if sTest.Interval != nil {
		bound = sTest.Interval.Low
		if s.a.SFallback {
			bound, boundName = sTest.Interval.P20, "one-sided 80 % lower bound (the 20th percentile; the pre-registered fallback)"
		}
	}
	sTest.Rule = fmt.Sprintf("shown when the interval's %s is above -%.2f", boundName, m)
	switch {
	case !decided:
		sTest.Label, sTest.Reason = LiveLabelNotTested, notDecided
	case sTest.Interval != nil && bound > -m && s.a.SFallback:
		// §9.4: under the fallback the docs state that non-inferiority was not shown; S is met, not claimed.
		sTest.Label, sTest.Reason = LiveLabelShown, fmt.Sprintf("S met only through the pre-registered fallback: the "+
			"20th percentile %.3f lies above -%.2f; non-inferiority on task success was not shown (§9.4)", bound, m)
	case sTest.Interval != nil && bound > -m:
		sTest.Label, sTest.Reason = LiveLabelShown, fmt.Sprintf("steer is non-inferior to classic on task success: %s "+
			"%.3f lies above -%.2f", boundName, bound, m)
	default:
		sTest.Label, sTest.Reason = LiveLabelNotShown, "non-inferiority on task success was not shown: "+intervalText(sTest.Interval)
	}
	out = append(out, sTest)

	for _, h := range []struct{ id, metric string }{
		{"harm-task-success", LiveMetricTaskSuccess}, {"harm-constraint-clean", LiveMetricConstraintClean},
	} {
		t := s.proportion(h.id, ArmSteer, ArmClassic, h.metric, true)
		t.Rule = "harm shown when the interval lies wholly below 0"
		switch {
		case t.Interval == nil:
			t.Label, t.Reason = LiveLabelNotShown, "no trial of one arm carries this outcome"
		case t.Interval.High < 0:
			t.Label, t.Reason = LiveLabelHarmShown, "steer is below classic: "+intervalText(t.Interval)
		default:
			t.Label, t.Reason = LiveLabelNotShown, "the interval does not lie wholly below 0: "+intervalText(t.Interval)
		}
		out = append(out, t)
	}

	if e := s.a.Efficiency; e != nil {
		for _, g := range e.Guards {
			if len(s.byArm[g.Treated]) == 0 || len(s.byArm[g.Comparator]) == 0 {
				continue
			}
			for _, metric := range e.Metrics {
				out = append(out, s.guard(g, metric))
			}
		}
	}
	return append(out, s.h3(decided, h1, h2))
}

// guard is one efficiency guard on one metric: a gated harm guard, or the classic-vs-stock label.
func (s stageA) guard(g LiveEfficiencyGuard, metric string) LiveTest {
	id := "harm-" + metric
	if !g.Gated {
		id = "label-" + metric
	}
	if g.Treated != ArmSteer || g.Comparator != ArmClassic {
		id += "-" + g.Treated + "-vs-" + g.Comparator
	}
	t := s.ratio(id, g.Treated, g.Comparator, metric, g.Gated)
	limit := 1 + s.margin
	t.Rule = fmt.Sprintf("harm shown when the ratio's lower bound is above %.2f", limit)
	if !g.Gated {
		// An ungated guard is a label, never a §9.6 harm (review CLI-7).
		t.Rule = fmt.Sprintf("overhead shown when the ratio's lower bound is above %.2f (a label, not gating)", limit)
	}
	what := map[string]string{LiveMetricCost: "cost", LiveMetricActiveTime: "time"}[metric]
	harm := func(iv *LiveInterval) bool { return iv != nil && iv.Low > limit }
	switch {
	case t.Interval == nil && g.Gated:
		t.Label, t.Reason = LiveLabelIndeterminate, "could not be computed: investigate the measurement (an arm with no "+
			"known value, or a comparator mean of zero); the gated guard fails closed as indeterminate, which §9.6 treats "+
			"as harm shown"
		return t
	case t.Interval == nil:
		t.Label, t.Reason = LiveLabelNotShown, "the ratio could not be computed"
		return t
	}
	primary := harm(t.Interval)
	if lim, ok := t.Sensitivity[LiveSensLimited]; ok && g.Gated && harm(lim) != primary {
		t.Label = LiveLabelIndeterminate
		t.Reason = fmt.Sprintf("the limited-trial sensitivity (%s) flips the primary verdict (%s), so the guard reads "+
			"indeterminate, which §9.6 treats as harm shown", intervalText(lim), intervalText(t.Interval))
		return t
	}
	switch {
	case primary && g.Gated:
		t.Label, t.Reason = LiveLabelHarmShown, fmt.Sprintf("%s's %s is shown above %.0f %% over %s's: %s",
			g.Treated, what, 100*s.margin, g.Comparator, intervalText(t.Interval))
	case primary:
		t.Label, t.Reason = LiveLabelOverheadShown, fmt.Sprintf("Qompack 0.3.x's %s overhead was shown above %.0f %%: %s "+
			"(a label for the docs and a defect item; it does not gate F1)", what, 100*s.margin, intervalText(t.Interval))
	default:
		t.Label, t.Reason = LiveLabelNotShown, fmt.Sprintf("the lower bound does not exceed %.2f: %s", limit,
			intervalText(t.Interval))
	}
	return t
}

// h3 is the efficiency claim appended to the fixed sequence: steer's cost non-inferior to classic's.
func (s stageA) h3(decided bool, h1, h2 LiveTest) LiveTest {
	t := s.ratio("H3", ArmSteer, ArmClassic, LiveMetricCost, s.a.Efficiency != nil && s.a.Efficiency.H3ShipCondition)
	limit := 1 + s.margin
	t.Rule = fmt.Sprintf("tested only if H1 and H2 are shown; shown when the adverse-imputed cost ratio's upper bound is "+
		"below %.2f, on completed trials too, and the limited-trial sensitivity does not flip it", limit)
	below := func(iv *LiveInterval) bool { return iv != nil && iv.High < limit }
	switch {
	case s.a.Efficiency == nil:
		t.Label, t.Reason = LiveLabelNotTested, "the task set pre-registers no efficiency block"
	case !decided:
		t.Label, t.Reason = LiveLabelNotTested, "this run is not on the pre-registered model; it decides only harm (§9.8)"
	case h1.Label != LiveLabelShown || h2.Label != LiveLabelShown:
		t.Label, t.Reason = LiveLabelNotTested, "the fixed sequence stopped before H3"
	case !below(t.Interval):
		t.Label, t.Reason = LiveLabelNotShown, "steering's cost increase was not shown to be under "+
			fmt.Sprintf("%.0f %%: %s", 100*s.margin, intervalText(t.Interval))
	case !below(t.Sensitivity[LiveSensCompleted]):
		t.Label, t.Reason = LiveLabelNotShown, "it does not hold on trials whose task completed: "+
			intervalText(t.Sensitivity[LiveSensCompleted])
	case t.Sensitivity[LiveSensLimited] != nil && !below(t.Sensitivity[LiveSensLimited]):
		t.Label, t.Reason = LiveLabelNotShown, "the limited-trial sensitivity flips it: "+
			intervalText(t.Sensitivity[LiveSensLimited])
	default:
		t.Label, t.Reason = LiveLabelShown, fmt.Sprintf("steering's cost increase was shown to be under %.0f %% (%s)",
			100*s.margin, intervalText(t.Interval))
	}
	return t
}

// verdict selects the §9.6 row and the run's verdict.
func (s stageA) verdict(out *LiveStageA) (string, LiveDecision) {
	label := func(id string) string {
		t, _ := out.Test(id)
		return t.Label
	}
	var harms []string
	for _, t := range out.Tests {
		if t.Gated && (t.Label == LiveLabelHarmShown || t.Label == LiveLabelIndeterminate) && t.ID != "H3" {
			harms = append(harms, fmt.Sprintf("%s %s", t.ID, t.Label))
		}
	}
	chain := fmt.Sprintf("H1 %s; H2 %s; S %s; H3 %s", label("H1"), label("H2"), label("S"), label("H3"))
	if len(harms) > 0 {
		return "harm shown on a harm metric, or a gated efficiency guard indeterminate: nothing new ships on; " +
				"investigate the harm",
			LiveDecision{Verdict: LiveVerdictHarm, Reason: strings.Join(harms, "; ") + " (" + chain + ")"}
	}
	if !out.Decided {
		return "measured on a model other than the pre-registered one: only harm is decided, and none was shown",
			LiveDecision{Verdict: LiveVerdictNoHarm, Reason: "no harm shown on any harm metric (" + chain + ")"}
	}
	h3Condition := s.a.Efficiency != nil && s.a.Efficiency.H3ShipCondition
	switch {
	case label("H1") != LiveLabelShown:
		return "H1 not shown, no harm shown: nothing new (the mod stays off); steering was not shown to help",
			LiveDecision{Verdict: LiveVerdictNoShip, Reason: chain}
	case label("H2") != LiveLabelShown:
		return "H1 shown, H2 not shown, no harm shown: nothing new; W7 only. The engine brief beat Qompack 0.3.x but was " +
				"not shown to beat a generic instruction",
			LiveDecision{Verdict: LiveVerdictNoShip, Reason: chain}
	case label("S") != LiveLabelShown:
		return "H1 and H2 shown, S not met, no harm shown: nothing new (F1 stays off); non-inferiority on task success " +
			"was not shown", LiveDecision{Verdict: LiveVerdictNoShip, Reason: chain}
	case h3Condition && label("H3") != LiveLabelShown:
		return "H1, H2 and S shown, no harm shown, but H3 — made a ship condition at R2 — was not shown: nothing new",
			LiveDecision{Verdict: LiveVerdictNoShip, Reason: chain}
	case s.a.SFallback:
		return "H1 and H2 shown, S met only through the pre-registered fallback, no harm shown: F1 ships on, and the docs " +
				"say non-inferiority on task success was not shown",
			LiveDecision{Verdict: LiveVerdictShip, Reason: chain + " (S by the fallback)"}
	}
	return "H1 and H2 shown, S met by the sized test, no harm shown: F1 ships on, subject to the other model's harm tests",
		LiveDecision{Verdict: LiveVerdictShip, Reason: chain}
}

// reported is every comparison §9.4 reports and never decides.
func (s stageA) reported() []LiveTest {
	var out []LiveTest
	have := func(arms ...string) bool {
		for _, a := range arms {
			if len(s.byArm[a]) == 0 {
				return false
			}
		}
		return true
	}
	for _, p := range [][2]string{{ArmClassic, ArmStock}, {ArmGeneric, ArmClassic}, {ArmSteer, ArmStock}} {
		if !have(p[0], p[1]) {
			continue
		}
		for _, metric := range []string{LiveMetricRecovery, LiveMetricTaskSuccess, LiveMetricConstraintClean} {
			t := s.proportion(p[0]+"-vs-"+p[1]+"-"+metric, p[0], p[1], metric, false)
			t.Label, t.Rule, t.Reason = LiveLabelReported, "reported, never decided", intervalText(t.Interval)
			out = append(out, t)
		}
	}
	if have(ArmSteer, ArmClassic) {
		limit := 1 + s.margin
		for _, r := range []struct {
			id, metric string
			bound      float64
			what       string
		}{
			{"time-non-inferiority", LiveMetricActiveTime, limit, fmt.Sprintf("steer's active time under %.2f × classic's", limit)},
			{"cost-reduction", LiveMetricCost, 1, "steer's cost below classic's"},
			{"time-reduction", LiveMetricActiveTime, 1, "steer's active time below classic's"},
		} {
			t := s.ratio(r.id, ArmSteer, ArmClassic, r.metric, false)
			holds := t.Interval != nil && t.Interval.High < r.bound
			t.Label, t.Rule = LiveLabelReported, fmt.Sprintf("reported, never decided: holds when the upper bound is below %.2f", r.bound)
			t.Reason = fmt.Sprintf("%s: %s (%s)", r.what, map[bool]string{true: "holds", false: "does not hold"}[holds],
				intervalText(t.Interval))
			out = append(out, t)
		}
		cm := s.compactionMedianDiff()
		out = append(out, cm)
	}
	for _, p := range [][2]string{{ArmGeneric, ArmClassic}, {ArmSteer, ArmStock}} {
		if !have(p[0], p[1]) {
			continue
		}
		for _, metric := range []string{LiveMetricCost, LiveMetricActiveTime} {
			t := s.ratio(p[0]+"-vs-"+p[1]+"-"+metric, p[0], p[1], metric, false)
			t.Label, t.Rule, t.Reason = LiveLabelReported, "reported, never decided", intervalText(t.Interval)
			out = append(out, t)
		}
	}
	return out
}

// compactionMedianDiff is the steer − classic median compaction duration with its interval.
func (s stageA) compactionMedianDiff() LiveTest {
	values := make([]LiveTaskValues, len(s.draws.Tasks))
	for arm, side := range map[string]bool{ArmSteer: true, ArmClassic: false} {
		for _, t := range s.byArm[arm] {
			i := s.index[t.TaskID]
			for _, c := range t.Compactions {
				if side {
					values[i].B = append(values[i].B, float64(c.DurationMS))
				} else {
					values[i].A = append(values[i].A, float64(c.DurationMS))
				}
			}
		}
	}
	t := LiveTest{
		ID: "compaction-duration-median", Treated: ArmSteer, Comparator: ArmClassic, Metric: "compaction_duration_ms",
		Statistic: "difference of pooled medians", Label: LiveLabelReported, Rule: "reported, never decided",
	}
	if iv, ok := PairedBootstrapMedianDiff(values, s.draws, s.conf); ok {
		t.Interval = &iv
	}
	t.Reason = intervalText(t.Interval)
	return t
}

// proportion is a difference of pooled proportions, treated − comparator, with the paired bootstrap's
// interval and Newcombe's beside it.
func (s stageA) proportion(id, treated, comparator, metric string, gated bool) LiveTest {
	t := LiveTest{
		ID: id, Treated: treated, Comparator: comparator, Metric: metric, Statistic: "difference of pooled proportions",
		Gated: gated,
	}
	counts := make([]LiveTaskCounts, len(s.draws.Tasks))
	var kb, nb, ka, na int
	for _, side := range []struct {
		arm string
		b   bool
	}{{treated, true}, {comparator, false}} {
		for _, tr := range s.byArm[side.arm] {
			ok, n := trialOutcome(tr, metric)
			if !n {
				continue
			}
			k := 0
			if ok {
				k = 1
			}
			c := &counts[s.index[tr.TaskID]]
			if side.b {
				c.KB, c.NB = c.KB+k, c.NB+1
				kb, nb = kb+k, nb+1
			} else {
				c.KA, c.NA = c.KA+k, c.NA+1
				ka, na = ka+k, na+1
			}
		}
	}
	if iv, ok := PairedBootstrapDiff(counts, s.draws, s.conf); ok {
		t.Interval = &iv
	}
	z := normalQuantile(1 - (1-s.conf)/2)
	t.Newcombe = newcombe(wilson(kb, nb, z), wilson(ka, na, z))
	return t
}

// trialOutcome is a trial's outcome on metric and whether the trial counts in its denominator. A
// trial the harness could not run as designed is a failure on every outcome it carries.
func trialOutcome(t LiveTrial, metric string) (success, counts bool) {
	ok := t.HarnessError == ""
	switch metric {
	case LiveMetricRecovery:
		if t.Recovered == nil {
			return false, false
		}
		return ok && *t.Recovered, true
	case LiveMetricConstraintClean:
		if t.ConstraintClean == nil {
			return false, false
		}
		return ok && *t.ConstraintClean, true
	}
	return ok && t.TaskSuccess, true
}

// ratio is the ratio of pooled means, treated over comparator, of metric: C under the adverse
// imputation, or T; with its sensitivities beside it.
func (s stageA) ratio(id, treated, comparator, metric string, gated bool) LiveTest {
	t := LiveTest{
		ID: id, Treated: treated, Comparator: comparator, Metric: metric, Statistic: "ratio of pooled means", Gated: gated,
		Sensitivity: map[string]*LiveInterval{},
	}
	B, A := s.byArm[treated], s.byArm[comparator]
	add := func(name string, values []LiveTaskValues, ok bool, agg LiveRatioAgg) *LiveInterval {
		if !ok {
			return nil
		}
		iv, ok := PairedBootstrapRatio(values, agg, s.draws, s.conf)
		if !ok {
			return nil
		}
		if name != "" {
			t.Sensitivity[name] = &iv
		}
		return &iv
	}
	primary, ok := s.values(metric, B, A, valueRules{adverse: true})
	t.Interval = add("", primary, ok, LiveRatioOfMeans)
	add(LiveSensGeometric, primary, ok, LiveGeometricMeanRatio)
	if metric == LiveMetricCost {
		v, ok := s.values(metric, B, A, valueRules{})
		add(LiveSensObserved, v, ok, LiveRatioOfMeans)
	}
	v, ok := s.values(metric, B, A, valueRules{adverse: true, completedOnly: true})
	add(LiveSensCompleted, v, ok, LiveRatioOfMeans)
	if len(s.limited[treated])+len(s.limited[comparator]) > 0 {
		// The limited values only add to the primary's, and need a complete C in the treated arm only where
		// the primary does, so they compute whenever the primary does (review R2-N2): a ratio that cannot
		// be computed already reads indeterminate, or not shown for H3.
		v, ok := s.values(metric, B, A, valueRules{adverse: true, limited: true})
		add(LiveSensLimited, v, ok, LiveRatioOfMeans)
	}
	if len(t.Sensitivity) == 0 {
		t.Sensitivity = nil
	}
	return t
}

// valueRules selects a ratio's values: C under the adverse imputation or as observed (an unknown C
// left out), only the trials whose task completed, and the limited trials' partial C and T added to
// their slots' re-runs.
type valueRules struct {
	adverse, completedOnly, limited bool
}

// values is each task's per-trial values of metric in the treated arm (B) and the comparator (A),
// aligned with the draws. False when the adverse imputation needs a complete C the treated arm does
// not have.
func (s stageA) values(metric string, treated, comparator []LiveTrial, r valueRules) ([]LiveTaskValues, bool) {
	keep := func(ts []LiveTrial) []LiveTrial {
		if !r.completedOnly {
			return ts
		}
		var out []LiveTrial
		for _, t := range ts {
			if t.Completed && t.HarnessError == "" {
				out = append(out, t)
			}
		}
		return out
	}
	treated, comparator = keep(treated), keep(comparator)
	out := make([]LiveTaskValues, len(s.draws.Tasks))
	for _, side := range []struct {
		ts      []LiveTrial
		treated bool
	}{{treated, true}, {comparator, false}} {
		var vals []float64
		var keys []LiveTrial
		if metric == LiveMetricActiveTime {
			for _, t := range side.ts {
				tm := t.ActiveMS(capFor(t, s.capMS))
				if side.treated && r.adverse {
					tm = t.ActiveMSAdverse(capFor(t, s.capMS))
				}
				vals, keys = append(vals, float64(tm)), append(keys, t)
			}
		} else {
			var ok bool
			vals, keys, ok = costValues(side.ts, side.treated, r.adverse)
			if !ok {
				return nil, false
			}
		}
		for i, t := range keys {
			v := vals[i]
			if r.limited {
				add, ok := s.limitedAdd(t, metric, side.treated && r.adverse, side.ts)
				if !ok {
					return nil, false
				}
				v += add
			}
			tv := &out[s.index[t.TaskID]]
			if side.treated {
				tv.B = append(tv.B, v)
			} else {
				tv.A = append(tv.A, v)
			}
		}
	}
	return out, true
}

// limitedAdd is what the limited trials of t's slot spent on metric. The comparator's count as
// observed: a partial C at its lower bound, an unknown C as 0, T as observed. The treated arm's fail
// closed when adverse (reviews S-R1-N1, R2-M1): T as ActiveMSAdverse, and a partial or unknown C at the
// larger of its observed C and its task's (else the arm's) maximum complete C in arm, as costValues
// imputes a scored trial, so an attempt that got further never adds less than one that got nowhere.
// False when arm has no complete C, which costValues has refused already for the same side.
func (s stageA) limitedAdd(t LiveTrial, metric string, adverse bool, arm []LiveTrial) (float64, bool) {
	var add float64
	for _, l := range s.limited[t.Arm] {
		if l.TaskID != t.TaskID || l.Trial != t.Trial {
			continue
		}
		if metric == LiveMetricActiveTime {
			if adverse {
				// The treated arm's limited attempt fails closed like its scored trials (review S-R1-N1): a
				// result line with no duration_ms counts at the cap.
				add += float64(l.ActiveMSAdverse(capFor(l, s.capMS)))
			} else {
				add += float64(l.ActiveMS(capFor(l, s.capMS)))
			}
			continue
		}
		c, status := l.Cost()
		switch {
		case status == LiveCostComplete, !adverse && status == LiveCostPartial:
			add += c
		case !adverse:
			// The comparator's unknown C adds 0.
		default:
			worst, ok := completeMax(arm, l.TaskID)
			if !ok {
				return 0, false
			}
			add += math.Max(c, worst)
		}
	}
	return add, true
}

// completeMax is the largest complete C among ts in task, else among all of ts; false when ts has none.
func completeMax(ts []LiveTrial, task string) (float64, bool) {
	taskMax, armMax := math.Inf(-1), math.Inf(-1)
	for _, t := range ts {
		c, status := t.Cost()
		if status != LiveCostComplete {
			continue
		}
		armMax = math.Max(armMax, c)
		if t.TaskID == task {
			taskMax = math.Max(taskMax, c)
		}
	}
	switch {
	case !math.IsInf(taskMax, -1):
		return taskMax, true
	case !math.IsInf(armMax, -1):
		return armMax, true
	}
	return 0, false
}

// costValues is each trial's C under the imputation rule, with the trials the values belong to.
// Adverse (amendment 4, fails closed): the treated arm's partial or unknown trials take the larger of
// their observed C and their task's maximum complete C in that arm (the arm's maximum when the task
// has none); the comparator's partial trials keep their observed lower bound, and its unknown trials
// take their task's minimum complete C in that arm (the arm's minimum when the task has none, and 0
// when the arm has none at all). Observed: every known C as observed, an unknown one left out. False
// when the treated arm needs a complete C it does not have.
func costValues(ts []LiveTrial, treated, adverse bool) ([]float64, []LiveTrial, bool) {
	taskMax, taskMin := map[string]float64{}, map[string]float64{}
	armMax, armMin, anyComplete := 0.0, math.Inf(1), false
	for _, t := range ts {
		c, status := t.Cost()
		if status != LiveCostComplete {
			continue
		}
		anyComplete = true
		if v, ok := taskMax[t.TaskID]; !ok || c > v {
			taskMax[t.TaskID] = c
		}
		if v, ok := taskMin[t.TaskID]; !ok || c < v {
			taskMin[t.TaskID] = c
		}
		armMax, armMin = math.Max(armMax, c), math.Min(armMin, c)
	}
	var vals []float64
	var keys []LiveTrial
	for _, t := range ts {
		c, status := t.Cost()
		switch {
		case status == LiveCostComplete:
		case !adverse && status == LiveCostUnknown:
			continue
		case !adverse:
		case treated:
			if !anyComplete {
				return nil, nil, false
			}
			worst, ok := taskMax[t.TaskID]
			if !ok {
				worst = armMax
			}
			c = math.Max(c, worst)
		case status == LiveCostUnknown:
			best, ok := taskMin[t.TaskID]
			switch {
			case ok:
			case anyComplete:
				best = armMin
			default:
				best = 0
			}
			c = best
		}
		vals, keys = append(vals, c), append(keys, t)
	}
	return vals, keys, true
}

// utilizationFits fits each subscription window's per-trial utilization delta against C over the
// trials that have both (reported: it tests C as the allowance's proxy).
func utilizationFits(ts []LiveTrial) []LiveUtilizationFit {
	xs, ys := map[string][]float64{}, map[string][]float64{}
	for _, t := range ts {
		c, status := t.Cost()
		if status != LiveCostComplete {
			continue
		}
		for w, d := range t.RateUse {
			if d.Delta != nil {
				xs[w], ys[w] = append(xs[w], c), append(ys[w], *d.Delta)
			}
		}
	}
	var out []LiveUtilizationFit
	for _, w := range slices.Sorted(maps.Keys(xs)) {
		x, y := xs[w], ys[w]
		f := LiveUtilizationFit{Window: w, N: len(x)}
		if len(x) >= 2 {
			var mx, my float64
			for i := range x {
				mx, my = mx+x[i], my+y[i]
			}
			mx, my = mx/float64(len(x)), my/float64(len(x))
			var sxx, sxy, syy float64
			for i := range x {
				sxx += (x[i] - mx) * (x[i] - mx)
				sxy += (x[i] - mx) * (y[i] - my)
				syy += (y[i] - my) * (y[i] - my)
			}
			if sxx > 0 {
				f.Slope = sxy / sxx
				f.Intercept = my - f.Slope*mx
				if syy > 0 {
					f.R2 = sxy * sxy / (sxx * syy)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// intervalText renders an interval for a reason line.
func intervalText(iv *LiveInterval) string {
	if iv == nil {
		return "no interval (an arm had no trial carrying the value)"
	}
	return fmt.Sprintf("%.3f [%.3f, %.3f]", iv.Estimate, iv.Low, iv.High)
}

// capFor is the step timeout t's run used (plan.json's, recorded on the trial), or fallback — the
// analysis block's pinned value — for a record written before it was recorded.
func capFor(t LiveTrial, fallback int64) int64 {
	if t.StepTimeoutMS > 0 {
		return t.StepTimeoutMS
	}
	return fallback
}

// liveNotMeasured is the §9.4 reported rows the harness does not produce (coordinator ruling on review
// CLI-4): distrust incidents have no definition yet, and the -p teardown losses with F4 on reach the
// record only inside each trial's hook problems.
var liveNotMeasured = []string{
	"distrust incidents: not measured by this harness (their definition is owed by R2's pre-registration)",
	"-p teardown losses with F4 on: not measured by this harness as a row; the losses the host records appear " +
		"inside each trial's hook problems",
}

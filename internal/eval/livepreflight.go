package eval

// ── Is a plan the pre-registered design? ─────────────────────────────────────────────────────────
//
// V6 close-out C5.5. A live run is the confirmatory run only when it is the whole pre-registered
// design. Most of that is fixed the moment the run is planned — its materials, install path, model,
// arms, tasks, trials per arm, bundle and the operator's known-defect statement — and the rest only
// the trials can show. The plan-time half lives here so `devtool live-eval` can report it before the
// first session is spent and `qompack eval` applies the same rule, in the same words, afterwards.

import (
	"fmt"
	"strings"
)

// LivePlanDepartures lists every way plan p departs, before any trial runs, from a confirmatory run
// of its task set's pre-registered design: the frozen materials and install path (preregistration
// sections 2 and 3, amendments A1 and A7) and a set that was not superseded, the operator's
// known-defect statement (section 9, amendment A5), the pre-registered model or its one contingency
// alias (section 3, amendment A6), every arm of the design (both arms of the two-arm design, the
// entry's arms of a Stage A one, W6 item 14), every task including the held-out ones, the
// pre-registered trials per task of each arm (sections 4 and 7; the entry's per-arm n when it names
// them), the pinned step timeout (amendment 4), and every bundle the design loads, built from one
// commit and a clean tree. a is the task set's analysis block. An empty result is not a
// confirmatory run yet: what only the trials can show — every planned trial ran, no foreign plugin
// loaded on any arm, an alias resolved to one model — is judged from the run's summary.
func LivePlanDepartures(p LivePlan, a LiveAnalysis) []string {
	var out []string
	pre, ok := LivePreregistrations[p.TaskSet]
	if !ok {
		out = append(out, fmt.Sprintf("its task set %s has no pre-registration", orUnknownText(p.TaskSet)))
	} else {
		out = append(out, departuresFromMaterials(p, pre)...)
		out = append(out, openDefectDepartures(p, pre)...)
	}
	if !(p.PreregisteredModel != "" && p.Model == p.PreregisteredModel) && !LivePlanOnContingency(p) {
		out = append(out, fmt.Sprintf("it ran on %s, not the pre-registered model %s",
			orUnknownText(p.Model), orUnknownText(p.PreregisteredModel)))
	}
	out = append(out, armDepartures(p, a, pre, ok)...)
	if !p.HeldOutIncluded {
		out = append(out, "it excluded the held-out tasks")
	}
	tasks := map[string]bool{}
	for _, t := range p.Trials {
		tasks[t.Task] = true
	}
	switch {
	case p.TaskSetTasks == 0:
		out = append(out, "its plan does not record the task set's size, so a narrowed run cannot be ruled out")
	case len(tasks) != p.TaskSetTasks:
		out = append(out, fmt.Sprintf("it planned %d of the task set's %d tasks", len(tasks), p.TaskSetTasks))
	}
	if e := a.Efficiency; e != nil && p.StepTimeoutMS != 0 && p.StepTimeoutMS != e.StepTimeoutMS {
		out = append(out, fmt.Sprintf("it ran with a step timeout of %d ms, not the pre-registered %d ms at which active "+
			"time counts a step with no result (amendment 4)", p.StepTimeoutMS, e.StepTimeoutMS))
	}
	if pre.ArmTrialsPerTask != nil {
		a.TrialsPerArm, a.ArmTrialsPerTask = 0, pre.ArmTrialsPerTask
	}
	out = append(out, trialDepartures(p, a)...)
	switch {
	case p.Plugin == nil && p.PluginMods == nil:
		out = append(out, "its plan names no plugin bundle")
	case p.Plugin != nil && p.Plugin.Dirty:
		out = append(out, "its bundle was assembled from a worktree with uncommitted changes, not a frozen candidate")
	}
	if p.PluginMods != nil && p.PluginMods.Dirty {
		out = append(out, "its --bundle-mods was assembled from a worktree with uncommitted changes, not a frozen candidate")
	}
	return append(out, bundleDepartures(p, pre)...)
}

// armDepartures names how the plan's arms fall short of its design's: the pre-registration's arms
// when it names them, the four Stage A arms for a four-arm task set with no pre-registration, and the
// two-arm design's stock and qompack otherwise.
func armDepartures(p LivePlan, a LiveAnalysis, pre LivePreregistration, registered bool) []string {
	want := pre.DesignArms()
	if !registered && a.fourArm() {
		want = LiveArms
	}
	var missing []string
	for _, arm := range want {
		if !containsText(p.Arms, arm) {
			missing = append(missing, arm)
		}
	}
	switch {
	case len(missing) == 0:
		return nil
	case len(want) == 2:
		return []string{fmt.Sprintf("it did not run both arms (arms: %s)", orUnknownText(strings.Join(p.Arms, ", ")))}
	}
	return []string{fmt.Sprintf("it did not run every arm of the design (%s; arms: %s, missing %s)",
		strings.Join(want, ", "), orUnknownText(strings.Join(p.Arms, ", ")), strings.Join(missing, ", "))}
}

// bundleDepartures names every bundle the pre-registered design loads that the plan does not record,
// and a pair of bundles built from different commits (W6 item 14).
func bundleDepartures(p LivePlan, pre LivePreregistration) []string {
	var out []string
	for _, b := range pre.Bundles {
		switch {
		case b == LiveBundleClassic && p.Plugin == nil:
			out = append(out, "its plan names no --bundle, which the pre-registered design's classic arms load")
		case b == LiveBundleMods && p.PluginMods == nil:
			out = append(out, "its plan names no --bundle-mods, which the pre-registered design's steer arm loads")
		}
	}
	if c, m := p.Plugin, p.PluginMods; c != nil && m != nil && (c.Commit != m.Commit || c.Version != m.Version) {
		out = append(out, fmt.Sprintf("its --bundle (%s at %s) and --bundle-mods (%s at %s) are not built from one commit",
			c.Version, c.Commit, m.Version, m.Commit))
	}
	return out
}

// trialDepartures names every arm whose planned trials per task differ from a's. A plan that records
// per-arm counts is compared arm by arm; an older plan has one count for every arm.
func trialDepartures(p LivePlan, a LiveAnalysis) []string {
	if p.ArmTrials == nil {
		if a.TrialsPerArm > 0 && p.TrialsPerArm != a.TrialsPerArm {
			return []string{fmt.Sprintf("it ran %d trials per arm per task, not the pre-registered %d",
				p.TrialsPerArm, a.TrialsPerArm)}
		}
		return nil
	}
	var out []string
	for _, arm := range p.Arms {
		if got, want := p.ArmTrials[arm], a.TrialsFor(arm); got != want {
			out = append(out, fmt.Sprintf("it ran %d trials per task on the %s arm, not the pre-registered %d",
				got, arm, want))
		}
	}
	return out
}

// LivePlanOnContingency reports that plan p runs on the one alias its task set's pre-registration
// permits in place of the pre-registered model (section 3's contingency, amendment A6).
func LivePlanOnContingency(p LivePlan) bool {
	pre, ok := LivePreregistrations[p.TaskSet]
	return ok && p.Model != p.PreregisteredModel && pre.RunsPreregisteredModel(p.PreregisteredModel, p.Model)
}

// departuresFromMaterials lists how the plan departs from what its task set's pre-registration froze:
// the task-set file, the fixture and hidden-test tree and the install path of the qompack arm, and
// whether the set still stands or was superseded before use (qompack-live-v1, amendment A7).
func departuresFromMaterials(p LivePlan, pre LivePreregistration) []string {
	var out []string
	if pre.SupersededBy != "" {
		out = append(out, fmt.Sprintf("its task set %s was %s; the confirmatory set is %s (%s)",
			p.TaskSet, pre.SupersededWhy, pre.SupersededBy, pre.Document))
	}
	if p.TaskSetSHA256 != pre.TaskSetSHA256 {
		out = append(out, fmt.Sprintf("its task set file hashes to %s, not the pre-registered %s (%s)",
			shortDigest(p.TaskSetSHA256), shortDigest(pre.TaskSetSHA256), pre.Document))
	}
	if p.FixtureTreeSHA256 != pre.FixtureTreeSHA256 {
		out = append(out, fmt.Sprintf("its fixture tree hashes to %s, not the pre-registered %s (%s)",
			shortDigest(p.FixtureTreeSHA256), shortDigest(pre.FixtureTreeSHA256), pre.Document))
	}
	if p.Install != pre.Install {
		out = append(out, fmt.Sprintf("its qompack arm was installed by %s, not the pre-registered --%s",
			orUnknownText(p.Install), pre.Install))
	}
	return out
}

// openDefectDepartures applies preregistration section 9: "The confirmatory run must be on a
// candidate where C1.12 and C1.1 are fixed; a run on a candidate with a known open defect is labelled
// with that defect and is not the confirmatory run" (amendment A5 reads "a known open defect" as
// any). Nothing in a bundle proves which defects it fixes, so the plan carries the operator's
// statement, and a plan without one cannot be told apart from a run on a candidate that still
// carries C1.12.
func openDefectDepartures(p LivePlan, pre LivePreregistration) []string {
	switch {
	case p.KnownDefects == nil:
		return []string{fmt.Sprintf("its plan does not attest which known defects its bundle carries, and "+
			"preregistration section 9 counts only a run on a candidate where %s are fixed and no known defect "+
			"is open as the confirmatory run", strings.Join(pre.RequiredFixed, " and "))}
	case len(p.KnownDefects.Open) > 0:
		return []string{fmt.Sprintf("its bundle carries the known open defect(s) %s, as its plan attests, and "+
			"preregistration section 9 labels such a run with them and does not count it as the confirmatory run",
			strings.Join(p.KnownDefects.Open, ", "))}
	}
	return nil
}

// shortDigest abbreviates a hex digest for a reason line.
func shortDigest(h string) string {
	const keep = 12
	switch {
	case h == "":
		return "unknown"
	case len(h) <= keep:
		return h
	}
	return h[:keep] + "…"
}

// orUnknownText renders an empty string as an explicit unknown rather than as blank space.
func orUnknownText(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func containsText(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

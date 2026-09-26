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
// alias (section 3, amendment A6), both arms (section 3), every task including the held-out ones and
// the pre-registered trials per arm (sections 4 and 7), and a bundle built from a clean tree.
// trialsPerArm is the task set's pre-registered trials per arm. An empty result is not a
// confirmatory run yet: what only the trials can show — every planned trial ran, no foreign plugin
// loaded on either arm, an alias resolved to one model — is judged from the run's summary.
func LivePlanDepartures(p LivePlan, trialsPerArm int) []string {
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
	if !(containsText(p.Arms, ArmStock) && containsText(p.Arms, ArmQompack)) {
		out = append(out, fmt.Sprintf("it did not run both arms (arms: %s)", orUnknownText(strings.Join(p.Arms, ", "))))
	}
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
	if trialsPerArm > 0 && p.TrialsPerArm != trialsPerArm {
		out = append(out, fmt.Sprintf("it ran %d trials per arm per task, not the pre-registered %d",
			p.TrialsPerArm, trialsPerArm))
	}
	switch {
	case p.Plugin == nil:
		out = append(out, "its plan names no plugin bundle")
	case p.Plugin.Dirty:
		out = append(out, "its bundle was assembled from a worktree with uncommitted changes, not a frozen candidate")
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

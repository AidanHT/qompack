package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// EvalSchema is the version of the eval command's report document.
const EvalSchema = 1

// EvalVerdict is the run's overall outcome.
type EvalVerdict string

const (
	// VerdictPass: every decidable gate passed and every planned trial ran. A live run's failed
	// trials are already counted as failures in its decision (intention to treat), so they are part
	// of what its gate decided, not a reason to withhold the pass.
	VerdictPass EvalVerdict = "pass"
	// VerdictFail: at least one decidable gate failed.
	VerdictFail EvalVerdict = "fail"
	// VerdictInconclusive: nothing failed, and not enough ran to say it passed. It is a distinct
	// outcome because a run whose trials were skipped has not demonstrated anything, and calling
	// that a pass is how a gate stops gating.
	VerdictInconclusive EvalVerdict = "inconclusive"
)

// EvalGate is one measured outcome.
type EvalGate struct {
	ID     string `json:"id"`
	Metric string `json:"metric"`
	// Value is the measurement, or nil when the artifact did not carry one.
	Value *float64 `json:"value"`
	// Passed is the verdict, or nil when nothing in force decides it. A nil Passed is NOT a pass:
	// SP-14 renders evaluation results and owns no thresholds, so a metric with no declared bound
	// is reported and left unjudged rather than being quietly counted as satisfied.
	Passed *bool  `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// TrialCounts is how much of the intended evaluation actually happened.
type TrialCounts struct {
	Planned int `json:"planned"`
	Ran     int `json:"ran"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// EvalCost is the run's cost evidence, reported next to the outcomes and never mixed into them.
type EvalCost struct {
	// Categories is the per-category usage total with its own evidence counts.
	Categories map[eval.UsageCategory]eval.CategorySum `json:"categories"`
	// Kinds counts records per request kind, so retries, aborts, compaction and child work stay
	// attributed instead of collapsing into one total.
	Kinds map[eval.RequestKind]int `json:"kinds"`
	// PricingModes counts records per pricing source. An estimate and an invoice are different
	// observations and are never merged.
	PricingModes map[eval.PricingMode]int `json:"pricing_modes"`
	// Estimate is the priced total, when a rate schedule was in force.
	Estimate *eval.Money `json:"estimate"`
	// Completeness labels what the estimate could not account for.
	Completeness *eval.Completeness `json:"completeness"`
	// Available reports whether any ledger was read at all.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// EvalReport is what `/qompack:eval` reports.
//
// The grouping is the substance of this command. §11.1 made fraction-of-OPT the primary metric;
// the current primary outcomes are whether the task still succeeded, whether its constraints held
// and whether the evidence could be recovered. Fraction-of-OPT and the file/action similarity
// measures are retained under Historical, labelled, because dropping them would break comparison
// with every report already recorded — but they no longer lead, and they no longer decide.
type EvalReport struct {
	Schema int `json:"schema"`
	// Verdict is decided by Task and Recovery only. Cost never contributes to it.
	Verdict EvalVerdict `json:"verdict"`
	// Task is whether the work still came out right.
	Task []EvalGate `json:"task"`
	// Recovery is whether what was dropped could be got back.
	Recovery []EvalGate `json:"recovery"`
	// Cost is reported beside the outcomes, never folded into them.
	Cost EvalCost `json:"cost"`
	// Historical are the §11.1 metrics, retained as labelled diagnostics.
	Historical []EvalGate `json:"historical"`
	// Trials counts the deterministic replay's sessions; a live run carries its own in Live.
	Trials   TrialCounts `json:"trials"`
	Policy   string      `json:"policy"`
	Baseline string      `json:"baseline"`
	Sessions int         `json:"sessions"`
	Notes    []string    `json:"notes,omitempty"`
	// Live is the newest real-host evaluation run read, or nil when none was. Its gates are in Task
	// and Recovery beside the replay's, prefixed LIVE-.
	Live *LiveEvalReport `json:"live,omitempty"`
}

// EvalInput is the artifact set the eval command renders.
//
// It is supplied through a seam rather than computed here: running the replay harness is
// test/replay's job, and a command that re-ran it would be a second driver with its own corpus
// selection and its own idea of what a trial is.
type EvalInput struct {
	Report eval.Report
	Trials TrialCounts
	// Ledger is the SP-19 request ledger, or nil when none was recorded.
	Ledger *eval.RequestLedger
	// Notes carries anything the producer needs the reader to know.
	Notes []string
	// Live is a real-host evaluation run, or nil when none was read.
	Live *LiveEvalInput
}

// EvalArtifacts supplies a completed evaluation's artifacts.
type EvalArtifacts func(ctx context.Context, corpus string) (EvalInput, error)

// evalBody implements `/qompack:eval [--corpus <path>]`.
func evalBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	if len(inv.Args) > 0 {
		return nil, UsageErrorf("qompack eval: takes no positional arguments; use --corpus <path>")
	}
	if inv.Deps.EvalArtifacts == nil {
		return nil, fmt.Errorf("%w: no evaluation artifacts are readable in this build", ErrUnavailable)
	}

	in, err := inv.Deps.EvalArtifacts(ctx, flagValue(inv, "corpus"))
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}

	rep := buildEvalReport(in)
	data, marshalErr := json.Marshal(rep)
	if marshalErr != nil {
		return nil, fmt.Errorf("eval: encoding report: %w", marshalErr)
	}
	if !inv.JSON {
		renderEval(inv, rep)
	}
	if rep.Verdict == VerdictFail {
		return data, fmt.Errorf("eval: %w", errEvalFailed)
	}
	return data, nil
}

// errEvalFailed is the sentinel a failing evaluation returns, so a caller can branch on it.
var errEvalFailed = fmt.Errorf("the evaluation did not pass its task and recovery gates")

// buildEvalReport groups a run's metrics into outcomes, recovery and cost. The deterministic replay
// and a real-host live run are reported side by side when both were read; either alone is enough.
func buildEvalReport(in EvalInput) EvalReport {
	rep := EvalReport{
		Schema:   EvalSchema,
		Baseline: in.Report.Baseline,
		Sessions: in.Report.Sessions,
		Trials:   in.Trials,
		Notes:    append([]string(nil), in.Notes...),
		Cost:     costOf(in.Ledger),
	}

	policies := qompackPolicies(in.Report)
	ok := len(policies) > 0
	if ok {
		rep.Policy = policies[0]
	}
	rep.Notes = append(rep.Notes, replayPolicyNotes(in.Report, policies)...)
	if !ok && in.Live == nil {
		rep.Verdict = VerdictInconclusive
		if len(in.Report.Policies) == 0 {
			rep.Notes = append(rep.Notes, "no policy scores were reported, so nothing was evaluated")
		}
		return rep
	}
	if ok {
		for i, name := range policies {
			suffix := ""
			if i > 0 {
				suffix = "@" + name
			}
			addReplayGates(&rep, in.Report.Policies[name], suffix)
		}
	} else {
		rep.Notes = append(rep.Notes, "no Qompack replay score was read; only the live run is reported")
	}
	if in.Live != nil {
		rep.Live = buildLiveReport(*in.Live)
		task, recovery := liveGates(rep.Live)
		rep.Task = append(rep.Task, task...)
		rep.Recovery = append(rep.Recovery, recovery...)
	}

	rep.Verdict = verdictOf(rep, ok)
	return rep
}

// addReplayGates adds the deterministic replay's gates for one Qompack policy's score. The leading
// policy's gates carry the plain IDs (suffix ""); every further policy's carry "@<policy>".
func addReplayGates(rep *EvalReport, score eval.Score, suffix string) {
	d := score.Divergence
	withSuffix := func(gates ...EvalGate) []EvalGate {
		for i := range gates {
			gates[i].ID += suffix
		}
		return gates
	}
	rep.Task = append(rep.Task, withSuffix(
		boolGate("TASK-01", "final decision preserved", d.SameDecision),
		unjudged("TASK-02", "decision preservation", d.DecisionPreservation),
		unjudged("TASK-03", "turns to first divergence", float64(d.FirstDivergenceTurn)),
	)...)
	rep.Recovery = append(rep.Recovery, withSuffix(
		unjudged("REC-01", "retrieval hit rate", score.RetrievalHitRate),
		reAttemptGate(d.ReAttempts),
		unjudged("REC-03", "redundant reads", float64(d.RedundantReads)),
	)...)
	rep.Historical = append(rep.Historical, withSuffix(
		historical("HIST-01", "fraction of OPT (§11.1, retained diagnostic)", score.FractionOfOPT),
		historical("HIST-02", "file-set Jaccard (retained diagnostic)", d.FileSetJaccard),
		historical("HIST-03", "tool edit distance (retained diagnostic)", float64(d.ToolEditDistance)),
	)...)
}

// verdictOf decides the run's outcome from the task and recovery gates alone.
//
// Cost is not consulted. That is the SP14-M7-03 rule in its operational form: a run that was cheap
// and got the wrong answer must not pass, so the cheapness cannot enter the decision at any
// weight. Trials that were skipped cannot produce a pass either, because a gate that passes on an
// evaluation which did not run is not a gate — the replay's (when one was read) and the live run's
// alike. A replay's failed trials withhold the pass too: its scores do not count them. A live run's
// failed trials do not: its pre-registered decision already counts every one of them in every
// denominator (intention to treat, preregistration section 8), a harness failure as a failure on
// every outcome, so its gate is that decision and the report lists each failed trial by name.
func verdictOf(rep EvalReport, replayed bool) EvalVerdict {
	var decided int
	for _, g := range append(append([]EvalGate{}, rep.Task...), rep.Recovery...) {
		if g.Passed == nil {
			continue
		}
		decided++
		if !*g.Passed {
			return VerdictFail
		}
	}
	switch {
	case replayed && (rep.Trials.Failed > 0 || rep.Trials.Skipped > 0 || rep.Trials.Ran == 0):
		return VerdictInconclusive
	case rep.Live != nil && (rep.Live.Trials.Skipped > 0 || rep.Live.Trials.Ran == 0):
		return VerdictInconclusive
	case decided == 0:
		return VerdictInconclusive
	default:
		return VerdictPass
	}
}

// qompackPolicyPrefix begins the name of every replay policy that is Qompack's own
// (test/replay registers qompack-rehydrate and qompack-l3). Every other policy a replay scores is a
// reference that bounds the metric — stock is the baseline, null keeps nothing, oracle is the
// Belady ceiling — and is never the policy this command reports.
const qompackPolicyPrefix = "qompack"

// defaultReplayPolicy is the Qompack policy test/replay scores by default (its defaultPolicies) and
// the phase-3 gate grades: the product's rehydrator. When a replay scored it, it leads the report.
const defaultReplayPolicy = "qompack-rehydrate"

// qompackPolicies lists every Qompack policy a replay scored, in report order: the driver's default
// product policy first when it was scored, then the rest by name. Every one of them is judged — a
// replay run with `--policies qompack-rehydrate,qompack-l3` is an evaluation of both, and judging
// only one would let the other fail unseen. A replay that scored none has nothing of Qompack's to
// report, and no reference policy is promoted in its place — null re-attempts every eliminated
// approach by construction, so judging it would fail an evaluation on a policy no user runs.
func qompackPolicies(r eval.Report) []string {
	var out []string
	for n := range r.Policies {
		if strings.HasPrefix(n, qompackPolicyPrefix) && n != defaultReplayPolicy {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	if _, ok := r.Policies[defaultReplayPolicy]; ok {
		out = append([]string{defaultReplayPolicy}, out...)
	}
	return out
}

// replayPolicyNotes says which of a replay's scored policies the gates are — every Qompack policy,
// the first with the plain gate IDs — and what else it scored: the reference policies, which bound
// the metric and are never judged.
func replayPolicyNotes(r eval.Report, judged []string) []string {
	var refs []string
	for n := range r.Policies {
		if !strings.HasPrefix(n, qompackPolicyPrefix) {
			refs = append(refs, n)
		}
	}
	sort.Strings(refs)
	var out []string
	switch {
	case len(judged) > 0 && len(refs) > 0:
		out = append(out, fmt.Sprintf("replay: the gates are %s; the report also scores the reference "+
			"policies %s, which bound the metric and are not judged", judgedPolicies(judged), strings.Join(refs, ", ")))
	case len(judged) == 0 && len(refs) > 0:
		out = append(out, fmt.Sprintf("replay: the report scores only the reference policies %s and no Qompack "+
			"policy (%s*), so it has nothing of Qompack's to judge", strings.Join(refs, ", "), qompackPolicyPrefix))
	}
	if len(judged) > 1 && len(refs) == 0 {
		out = append(out, "replay: the gates are "+judgedPolicies(judged))
	}
	return out
}

// judgedPolicies names the judged Qompack policies and how their gates are told apart.
func judgedPolicies(judged []string) string {
	if len(judged) == 1 {
		return judged[0] + "'s"
	}
	return fmt.Sprintf("every Qompack policy the report scored: %s's with the plain IDs, and %s's with "+
		"the policy's name after @", judged[0], strings.Join(judged[1:], "'s, "))
}

// boolGate is a gate the artifact decides outright.
func boolGate(id, metric string, ok bool) EvalGate {
	v := 0.0
	if ok {
		v = 1
	}
	passed := ok
	return EvalGate{ID: id, Metric: metric, Value: &v, Passed: &passed}
}

// reAttemptGate fails when the compacted run retried an approach already eliminated, which is a
// constraint violation the artifact decides on its own: any re-attempt is one too many.
func reAttemptGate(n int) EvalGate {
	v := float64(n)
	passed := n == 0
	return EvalGate{
		ID: "REC-02", Metric: "re-attempts of eliminated approaches",
		Value: &v, Passed: &passed,
		Detail: "any re-attempt is a violation; the elimination was recorded and not respected",
	}
}

// unjudged reports a measurement with no threshold in force.
//
// SP-14 owns presentation, not policy. Inventing a bound here would put a gate's threshold in the
// renderer, where no plan reviewed it, so the number is shown and the verdict withheld.
func unjudged(id, metric string, v float64) EvalGate {
	val := v
	return EvalGate{
		ID: id, Metric: metric, Value: &val,
		Detail: "reported; no threshold is declared for this metric, so it is not judged here",
	}
}

// historical is a retained §11.1 diagnostic, labelled and never decisive.
func historical(id, metric string, v float64) EvalGate {
	val := v
	return EvalGate{
		ID: id, Metric: metric, Value: &val,
		Detail: "historical diagnostic, retained for comparison; it does not decide the verdict",
	}
}

// costOf summarizes a request ledger without ever inventing a number for it.
func costOf(l *eval.RequestLedger) EvalCost {
	if l == nil {
		return EvalCost{Reason: "no request ledger was recorded for this run"}
	}

	c := EvalCost{
		Available:    true,
		Categories:   l.Sum(),
		Kinds:        map[eval.RequestKind]int{},
		PricingModes: map[eval.PricingMode]int{},
	}
	for _, r := range l.Records {
		c.Kinds[r.Kind]++
		c.PricingModes[r.PricingMode]++
	}
	if l.Schedule != nil {
		money, completeness := l.Estimate(*l.Schedule)
		c.Estimate = &money
		c.Completeness = &completeness
	} else {
		c.Reason = "no rate schedule was in force, so no amount was priced"
	}
	return c
}

// renderEval writes the report for a person, outcomes first.
func renderEval(inv Invocation, rep EvalReport) {
	rw := &errWriter{w: inv.Out}

	rw.printf("eval: %s\n", strings.ToUpper(string(rep.Verdict)))
	if rep.Live != nil && rep.Policy == "" && rep.Sessions == 0 {
		// Only a live run was read: the replay's header would print zeroes for a replay that is not
		// there, which reads as a replay that ran nothing.
		rw.printf("replay: none read; the live run below is the whole evaluation\n\n")
	} else {
		rw.printf("policy: %s   baseline: %s   sessions: %d\n",
			orUnknown(rep.Policy), orUnknown(rep.Baseline), rep.Sessions)
		rw.printf("trials: %d planned, %d ran, %d skipped, %d failed\n\n",
			rep.Trials.Planned, rep.Trials.Ran, rep.Trials.Skipped, rep.Trials.Failed)
	}

	renderGates(rw, "task outcomes", rep.Task)
	renderGates(rw, "evidence recovery", rep.Recovery)

	rw.printf("cost — reported separately; it does not decide the verdict\n")
	renderCost(rw, rep.Cost)

	renderGates(rw, "historical diagnostics — retained, not decisive", rep.Historical)

	if rep.Live != nil {
		renderLive(rw, rep.Live)
	}

	for _, n := range rep.Notes {
		rw.printf("note: %s\n", n)
	}
}

// renderGates prints one group.
func renderGates(rw *errWriter, title string, gates []EvalGate) {
	rw.printf("%s\n", title)
	if len(gates) == 0 {
		rw.printf("  (none reported)\n\n")
		return
	}
	for _, g := range gates {
		rw.printf("  %-8s %-44s %s  %s\n", g.ID, g.Metric, gateValue(g), gateVerdict(g))
	}
	rw.printf("\n")
}

// gateValue renders a measurement, or says there was none.
func gateValue(g EvalGate) string {
	if g.Value == nil {
		return "not measured"
	}
	return fmt.Sprintf("%10.4f", *g.Value)
}

// gateVerdict renders pass, fail, or an explicit refusal to judge.
func gateVerdict(g EvalGate) string {
	if g.Passed == nil {
		return "not judged"
	}
	if *g.Passed {
		return "pass"
	}
	return "FAIL"
}

// renderCost prints the usage evidence, keeping every distinction the ledger drew.
func renderCost(rw *errWriter, c EvalCost) {
	if !c.Available {
		rw.printf("  unavailable: %s\n\n", orUnknown(c.Reason))
		return
	}

	for _, cat := range eval.UsageCategories() {
		sum, ok := c.Categories[cat]
		if !ok {
			rw.printf("  %-16s unknown: no record reported this category\n", string(cat))
			continue
		}
		rw.printf("  %-16s %10d tokens  (%d record(s) reported, %d did not)\n",
			string(cat), int64(sum.Known), sum.KnownRecords, sum.UnknownRecords)
	}

	if len(c.Kinds) > 0 {
		rw.printf("  request kinds:")
		for _, k := range []eval.RequestKind{
			eval.KindTurn, eval.KindRetry, eval.KindAbort,
			eval.KindCompaction, eval.KindChild, eval.KindNonToken,
		} {
			if n := c.Kinds[k]; n > 0 {
				rw.printf(" %s=%d", string(k), n)
			}
		}
		rw.printf("\n")
	}

	if len(c.PricingModes) > 0 {
		rw.printf("  pricing sources:")
		for _, m := range []eval.PricingMode{
			eval.PricingInvoice, eval.PricingSubscription, eval.PricingEstimate, eval.PricingNone,
		} {
			if n := c.PricingModes[m]; n > 0 {
				rw.printf(" %s=%d", string(m), n)
			}
		}
		rw.printf("\n")
	}

	switch {
	case c.Estimate == nil:
		rw.printf("  amount: not priced — %s\n", orUnknown(c.Reason))
	case c.Completeness != nil && !c.Completeness.Complete:
		rw.printf("  amount: %d micros %s — INCOMPLETE, %d record(s) left a category unknown\n",
			c.Estimate.Micros, c.Estimate.Currency, c.Completeness.UnknownRecords)
		for _, m := range c.Completeness.MissingCategories {
			rw.printf("    unaccounted: %s\n", string(m))
		}
	default:
		rw.printf("  amount: %d micros %s\n", c.Estimate.Micros, c.Estimate.Currency)
	}
	rw.printf("\n")
}

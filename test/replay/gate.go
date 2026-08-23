package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// The §11.3 2% rule's constants.
//
// absFloor is the point below which a relative comparison stops meaning anything: against a
// baseline of 0.0 every change is an infinite percentage, so the absolute tolerance takes over.
// ratioAbsTol and countAbsTol are those tolerances, split because a 0.02 move in a ratio metric
// and a 0.02 move in a token count are not remotely the same event.
const (
	regressionThreshold = 0.02
	absFloor            = 1e-6
	epsilon             = 1e-9
	ratioAbsTol         = 0.02
	countAbsTol         = 1.0
)

// bloomFPCeiling is §11.4's hard limit, quoted in the failure message it produces.
const bloomFPCeiling = 0.10

// bloomCeilingSentence is §11.4 verbatim, printed on failure so the number carries its reason.
const bloomCeilingSentence = "At 1% they are safe; at 10% the agent starts skipping viable approaches."

// corpusStalePhases is how many phases a corpus may fall behind before the gate calls it
// overfitted. §11.4: "logged sessions were produced by an agent operating under the CURRENT
// system. Behaviour changes when the system changes."
const corpusStalePhases = 2

// ratioMetrics are the metrics whose absolute tolerance is a ratio rather than a count.
//
// The two §11.4 watch-fors belong here for the same reason every other entry does: they are
// fractions in [0, 1]. Omitting them left them on countAbsTol — a tolerance of 1.0 — so no
// reachable move in a fill ratio or a false-positive rate could ever be judged a regression, and
// with a baseline of 0 (which is what a run without --sketch records) the relative branch was
// skipped too. The watch-for half of the 2 % rule could not fail on any input.
var ratioMetrics = map[string]bool{
	"fraction_of_opt":       true,
	"file_set_jaccard":      true,
	"decision_preservation": true,
	"retrieval_hit_rate":    true,
	"same_decision":         true,
	"bloom_fp_rate":         true,
	"bloom_fill_ratio":      true,
}

// watchForDirection is the gate's own small extension table for the §11.4 watch-fors.
//
// They are deliberately NOT eval.MetricsOf keys: they come from a provider, not from a Score, and
// keeping them out is what lets the MetricsOf/MetricDirection completeness test be an exact set
// equality instead of a subset check.
var watchForDirection = map[string]eval.Direction{
	"bloom_fp_rate":    eval.DirLowerBetter,
	"bloom_fill_ratio": eval.DirLowerBetter,
}

// signOffPattern is the trailer that makes a regression deliberate rather than accidental.
var signOffPattern = regexp.MustCompile(
	`(?mi)^sign-off:\s*(?P<metric>[a-z0-9_]+)\s*=\s*(?P<delta>[+-]?[0-9.]+%)\s+(?P<reason>\S.*)$`)

// signOffMinReason is how much explanation a sign-off has to carry. "wip" is not a reason.
const signOffMinReason = 10

// directionOf resolves a metric's direction across both tables.
func directionOf(metric string) eval.Direction {
	if d, ok := watchForDirection[metric]; ok {
		return d
	}
	return eval.MetricDirection(metric)
}

// loadBaseline resolves --baseline, which accepts two forms: a path, or a git ref whose committed
// baseline is read with `git show`.
//
// The ref form is what 00-ARCHITECTURE.md §8 spells as `--baseline develop`. A value containing a
// path separator, or ending in .json, is a path; anything else is tried as a ref. The git call is
// this driver's only subprocess and it is skipped entirely for the path form, so the unit tests
// never shell out.
func loadBaseline(value, repoRoot string) (baselineFile, error) {
	var out baselineFile
	raw, err := readBaseline(value, repoRoot)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("replay: parsing baseline %s: %w", value, err)
	}
	return out, nil
}

// readBaseline fetches the baseline bytes from a path or a git ref.
func readBaseline(value, repoRoot string) ([]byte, error) {
	if looksLikePath(value) {
		raw, err := os.ReadFile(value) //nolint:gosec // an explicitly named baseline path
		if err != nil {
			return nil, fmt.Errorf("replay: reading baseline %s: %w", value, err)
		}
		return raw, nil
	}

	if err := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", value).Run(); err != nil {
		return nil, fmt.Errorf(
			"replay: --baseline %q is neither a readable path nor a resolvable git ref; "+
				"pass a path such as testdata/baseline/phase0.json, or a ref such as develop", value)
	}
	raw, err := exec.Command("git", "-C", repoRoot, "show", value+":"+defaultBaselinePath).Output()
	if err != nil {
		return nil, fmt.Errorf("replay: reading %s from git ref %q: %w", defaultBaselinePath, value, err)
	}
	return raw, nil
}

// looksLikePath distinguishes the two --baseline forms.
func looksLikePath(v string) bool {
	return strings.ContainsAny(v, `/\`) ||
		strings.EqualFold(filepath.Ext(v), ".json") ||
		v == "." || v == ".."
}

// compare applies the §11.3 2% rule to every metric of every policy, plus the watch-fors.
func compare(baseline baselineFile, observed map[string]map[string]float64,
	watchFor map[string]float64, signOff string,
) []eval.Regression {
	var out []eval.Regression

	policies := make([]string, 0, len(observed))
	for name := range observed {
		policies = append(policies, name)
	}
	sort.Strings(policies)

	for _, policy := range policies {
		want, ok := baseline.Policies[policy]
		if !ok {
			continue // a newly registered policy has no baseline to regress against yet
		}
		for _, metric := range eval.MetricNames() {
			if r, regressed := judge(policy, metric, want[metric], observed[policy][metric], signOff); regressed {
				out = append(out, r)
			}
		}
	}

	names := make([]string, 0, len(watchFor))
	for name := range watchFor {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := baseline.WatchFor[name]; !ok {
			continue
		}
		if r, regressed := judge("watchFor", name, baseline.WatchFor[name], watchFor[name], signOff); regressed {
			out = append(out, r)
		}
	}
	return out
}

// judge decides whether one metric moved far enough, in the wrong direction, to count.
func judge(policy, metric string, base, observed float64, signOff string) (eval.Regression, bool) {
	worse := observed < base
	if directionOf(metric) == eval.DirLowerBetter {
		worse = observed > base
	}
	delta := (observed - base) / math.Max(math.Abs(base), epsilon) * 100

	if !worse {
		return eval.Regression{}, false
	}

	rel := math.Abs(observed-base) / math.Max(math.Abs(base), epsilon)
	tol := countAbsTol
	if ratioMetrics[metric] {
		tol = ratioAbsTol
	}
	regressed := math.Abs(observed-base) > tol
	if math.Abs(base) >= absFloor {
		regressed = rel > regressionThreshold
	}
	if !regressed {
		return eval.Regression{}, false
	}

	return eval.Regression{
		Metric:   metric,
		Policy:   policy,
		Baseline: base,
		Observed: observed,
		DeltaPct: delta,
		Allowed:  signedOff(signOff, metric),
	}, true
}

// signedOff reports whether the PR body carries a trailer naming this exact metric with a reason
// worth reading.
func signedOff(body, metric string) bool {
	if body == "" {
		return false
	}
	for _, m := range signOffPattern.FindAllStringSubmatch(body, -1) {
		named := strings.ToLower(strings.TrimSpace(m[signOffPattern.SubexpIndex("metric")]))
		reason := strings.TrimSpace(m[signOffPattern.SubexpIndex("reason")])
		if named == metric && len(reason) >= signOffMinReason {
			return true
		}
	}
	return false
}

// reportRegressions prints the regression table and returns whether any of them blocks the merge.
//
// The failure message ends with the exact trailer the author would have to add. A gate that tells
// you how to satisfy it is a gate people use rather than route around.
func reportRegressions(w io.Writer, regs []eval.Regression) bool {
	if len(regs) == 0 {
		return false
	}
	fmt.Fprintf(w, "\n%-28s %-9s %14s %14s %10s %8s\n",
		"metric", "policy", "baseline", "observed", "delta%", "allowed")
	blocking := false
	for _, r := range regs {
		fmt.Fprintf(w, "%-28s %-9s %14.6f %14.6f %9.2f%% %8t\n",
			r.Metric, r.Policy, r.Baseline, r.Observed, r.DeltaPct, r.Allowed)
		if !r.Allowed {
			blocking = true
		}
	}
	if !blocking {
		fmt.Fprintln(w, "\nevery regression above carries a matching sign-off trailer.")
		return false
	}

	fmt.Fprintln(w, "\n§11.3: no metric may regress by more than 2% to improve another without")
	fmt.Fprintln(w, "explicit sign-off. Add a trailer to the pull-request body for each blocking row:")
	for _, r := range regs {
		if !r.Allowed {
			fmt.Fprintf(w, "\n    Sign-off: %s=%+.2f%% <why this trade is worth it, at least %d characters>\n",
				r.Metric, r.DeltaPct, signOffMinReason)
		}
	}
	return true
}

// checkBloomCeiling enforces §11.4's hard limit on the bloom false-positive rate.
func checkBloomCeiling(health eval.SketchHealth) error {
	if health.EstFPRate > bloomFPCeiling {
		return fmt.Errorf(
			"bloom false-positive rate is %.4f, over the %.2f ceiling. %s",
			health.EstFPRate, bloomFPCeiling, bloomCeilingSentence)
	}
	return nil
}

// checkCorpusFreshness is the §11.4 overfitting guard, made mechanical.
func checkCorpusFreshness(m eval.CorpusManifest, phase int) error {
	if phase > m.RegeneratedAfterPhase+corpusStalePhases {
		return fmt.Errorf(
			"corpus stale: re-collect sessions under the current policy (§11.4). The corpus was "+
				"regenerated after phase %d and this run asserts phase %d, more than %d phases "+
				"later. See docs/adr/0003-replay-overfit-recollection.md for the protocol",
			m.RegeneratedAfterPhase, phase, corpusStalePhases)
	}
	return nil
}

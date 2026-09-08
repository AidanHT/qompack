package main

import (
	"encoding/json"
	"errors"
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
//
// The trailing % is OPTIONAL, and that is not a loosening. A row whose baseline is at or near zero
// has no defined percentage (see judge), so the gate asks for the trailer in ABSOLUTE units and a
// pattern that insisted on a % would have demanded a figure the gate itself refuses to print.
var signOffPattern = regexp.MustCompile(
	`(?mi)^sign-off:\s*(?P<metric>[a-z0-9_]+)\s*=\s*(?P<delta>[+-]?[0-9.]+%?)\s+(?P<reason>\S.*)$`)

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

// ---- Corpus identity: a baseline may only be compared to a run over the same workload ----------
//
// The 2% rule is a comparison of one policy's numbers against that policy's earlier numbers OVER
// THE SAME SESSIONS. Point it at a baseline recorded over a DIFFERENT corpus and every percentage
// it prints describes the difference between two workloads, not a change in any policy: it is a
// category error, not a strict gate. The V4 re-baseline made that concrete. Correcting the corpus
// so that all four demand kinds are raised and the Belady budget actually binds moved
// stock.fraction_of_opt from 0.695164 to 0.258291 -- a real and expected consequence of a harder
// corpus, and nothing whatever to do with the stock policy, which is unchanged. Judged against the
// old corpus's baseline it reads as a -62.84% regression demanding a written sign-off, and signing
// that off would put a false explanation in the record permanently.
//
// Both artifacts already carried the identity needed to catch this -- corpusSHA256, the digest of
// the corpus manifest -- and nothing consulted it, exactly as nothing consulted corpusTier before
// the 2026-08-22 audit. checkCorpusIdentity consults every field of it and refuses on any
// disagreement, and it refuses just as loudly when EITHER side declines to say which corpus it
// used: an unidentified baseline cannot be shown to match, and "cannot be shown to match" is not
// permission to compare.
//
// These are the same rules eval.Comparable states over the provenance sidecars, enforced here on
// the artifacts themselves so that a run cannot slip past them by never loading a sidecar.

// corpusIdentity is what two measurements must agree on before a percentage between them means
// anything.
type corpusIdentity struct {
	generator string
	tier      string
	sha256    string
	sessions  int
}

// identityOf projects a committed baseline onto its corpus identity.
func (b baselineFile) identity() corpusIdentity {
	return corpusIdentity{generator: b.Generator, tier: b.CorpusTier, sha256: b.CorpusSHA256, sessions: b.Sessions}
}

// identity projects this run onto the same shape.
func (d DriverReport) identity() corpusIdentity {
	return corpusIdentity{generator: d.Generator, tier: d.CorpusTier, sha256: d.CorpusSHA256, sessions: d.Sessions}
}

// checkCorpusIdentity refuses to compare a run against a baseline recorded over a different corpus.
//
// It reports EVERY disagreement rather than the first, because a reader deciding what to do next
// needs to know whether one field drifted or the whole workload was replaced.
func checkCorpusIdentity(baselineRef string, base baselineFile, d DriverReport, root string) error {
	want, got := base.identity(), d.identity()
	var reasons []string

	switch {
	case want.sha256 == "" && got.sha256 == "":
		reasons = append(reasons, "neither the baseline nor this run records a corpusSHA256, so there "+
			"is no evidence they measured the same sessions; an unidentified pair is refused rather "+
			"than assumed to match")
	case want.sha256 == "":
		reasons = append(reasons, fmt.Sprintf("the baseline records no corpusSHA256 while this run "+
			"replayed corpus %s: a baseline that does not say which sessions produced it cannot be "+
			"shown to describe these ones", got.sha256))
	case got.sha256 == "":
		reasons = append(reasons, fmt.Sprintf("this run recorded no corpusSHA256 (no CORPUS.json under "+
			"the --corpus directory) while the baseline was recorded over corpus %s", want.sha256))
	case want.sha256 != got.sha256:
		reasons = append(reasons, fmt.Sprintf("corpus sha256: the baseline was recorded over %s and this "+
			"run replayed %s. They are different sessions, so a percentage between them measures the "+
			"corpora and not the policy", want.sha256, got.sha256))
	}

	if want.tier != "" && want.tier != got.tier {
		reasons = append(reasons, fmt.Sprintf("corpus tier: the baseline is %q and this run is %q. The "+
			"2%% rule compares policies over the SAME population, and the two fidelity tiers are "+
			"different populations (ADR 0002)", want.tier, got.tier))
	}
	if want.sessions != 0 && want.sessions != got.sessions {
		reasons = append(reasons, fmt.Sprintf("corpus sessions: the baseline holds %d and this run "+
			"replayed %d", want.sessions, got.sessions))
	}
	if want.generator != "" && want.generator != got.generator {
		reasons = append(reasons, fmt.Sprintf("driver generator: the baseline was produced by %q and "+
			"this run by %q, so the measurement procedure differs", want.generator, got.generator))
	}
	if len(reasons) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "baseline %s does not describe the corpus this run replayed, so the two are not "+
		"two measurements of the same thing and no percentage between them means anything:", baselineRef)
	for _, r := range reasons {
		fmt.Fprintf(&b, "\n  - %s", r)
	}
	if hint := matchingBaseline(root, got.sha256); hint != "" {
		fmt.Fprintf(&b, "\n  Point --baseline at %s, whose corpusSHA256 is this run's.", hint)
	} else {
		fmt.Fprintf(&b, "\n  Point --baseline at a baseline whose corpusSHA256 is %s.", got.sha256)
	}
	b.WriteString("\n  A corpus change is re-baselined as a NEW artifact beside the old one, never by " +
		"overwriting it (M0-04): the old numbers stay readable as the record of what the old corpus " +
		"said, and eval.Comparable refuses the pair as a before/after. To run with no comparison at " +
		"all, say so with --baseline \"\".")
	return errors.New(b.String())
}

// matchingBaseline names a committed baseline recorded over the digest given, so the failure above
// ends in an instruction rather than a puzzle. It returns "" when it cannot find one, and never
// fails the run on its own account: this is a hint, not a check.
func matchingBaseline(root, digest string) string {
	if root == "" || digest == "" {
		return ""
	}
	dir := filepath.Join(root, "testdata", "baseline")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.Contains(e.Name(), ".v1.") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a committed baseline
		if err != nil {
			continue
		}
		var candidate baselineFile
		if json.Unmarshal(raw, &candidate) != nil {
			continue
		}
		if candidate.CorpusSHA256 == digest {
			return "testdata/baseline/" + name
		}
	}
	return ""
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
	if !worse {
		return eval.Regression{}, false
	}

	// defined is the whole question this function has to answer twice: once for the verdict, and
	// once for the number the verdict is reported with. A percentage needs a denominator, and when
	// |base| is under absFloor there effectively is not one — every move is an arbitrarily large
	// percentage of almost nothing. Those rows are judged on the ABSOLUTE tolerance (which is what
	// the code below already did) and reported as absolute movement (which is what it did not).
	defined := math.Abs(base) >= absFloor

	tol := countAbsTol
	if ratioMetrics[metric] {
		tol = ratioAbsTol
	}
	regressed := math.Abs(observed-base) > tol
	if defined {
		regressed = math.Abs(observed-base)/math.Abs(base) > regressionThreshold
	}
	if !regressed {
		return eval.Regression{}, false
	}

	r := eval.Regression{
		Metric:          metric,
		Policy:          policy,
		Baseline:        base,
		Observed:        observed,
		AbsDelta:        observed - base,
		DeltaPctDefined: defined,
		Allowed:         signedOff(signOff, metric),
	}
	if defined {
		r.DeltaPct = (observed - base) / math.Abs(base) * 100
	}
	return r, true
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
	fmt.Fprintf(w, "\n%-28s %-9s %14s %14s %14s %14s %8s\n",
		"metric", "policy", "baseline", "observed", "delta%", "abs delta", "allowed")
	blocking, degenerate := false, false
	for _, r := range regs {
		fmt.Fprintf(w, "%-28s %-9s %14.6f %14.6f %14s %+14.6f %8t\n",
			r.Metric, r.Policy, r.Baseline, r.Observed, deltaPctCell(r), r.AbsDelta, r.Allowed)
		if !r.Allowed {
			blocking = true
		}
		if !r.DeltaPctDefined {
			degenerate = true
		}
	}
	if degenerate {
		fmt.Fprintf(w, "\n%s\n", degenerateNote)
	}
	if !blocking {
		fmt.Fprintln(w, "\nevery regression above carries a matching sign-off trailer.")
		return false
	}

	fmt.Fprintln(w, "\n§11.3: no metric may regress by more than 2% to improve another without")
	fmt.Fprintln(w, "explicit sign-off. Add a trailer to the pull-request body for each blocking row:")
	for _, r := range regs {
		if !r.Allowed {
			fmt.Fprintf(w, "\n    Sign-off: %s=%s <why this trade is worth it, at least %d characters>\n",
				r.Metric, signOffDelta(r), signOffMinReason)
		}
	}
	return true
}

// degenerateDelta is what the delta% column says for a row whose percentage does not exist.
const degenerateDelta = "unreportable"

// degenerateNote explains that column, once, under the table that used it.
const degenerateNote = "delta% is " + degenerateDelta +
	" on every row above whose baseline is at or below 1e-06: a percentage of almost nothing " +
	"measures the denominator, not the move. Those rows are judged on the absolute tolerance and " +
	"reported by their absolute delta. Read the baseline and observed columns; sign one off in " +
	"absolute units if it is deliberate, and never write a rationale for a percentage the gate " +
	"refuses to print."

// deltaPctCell renders one row's relative change, or says it has none.
func deltaPctCell(r eval.Regression) string {
	if !r.DeltaPctDefined {
		return degenerateDelta
	}
	return fmt.Sprintf("%+.2f%%", r.DeltaPct)
}

// signOffDelta renders the delta the suggested trailer quotes: a percentage where one exists, and
// absolute units where one does not. signOffPattern accepts both forms for this reason.
func signOffDelta(r eval.Regression) string {
	if !r.DeltaPctDefined {
		return fmt.Sprintf("%+.6f", r.AbsDelta)
	}
	return fmt.Sprintf("%+.2f%%", r.DeltaPct)
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

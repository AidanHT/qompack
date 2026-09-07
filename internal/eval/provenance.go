package eval

// ── Baseline provenance: the second versioned ON-DISK artifact ──────────────────────────────────
//
// SP-19 commit 7, and the same format rules as ledger.go: snake_case keys, an explicit Version,
// and no relationship to the frozen camelCase §5.18 shapes in types.go.
//
// A committed metric without its provenance is not evidence. It cannot be checked (did the file
// change?), it cannot be compared (was the later run even measuring the same thing?), and it
// cannot be read honestly (which of its numbers are artefacts of the corpus rather than results?).
// This file supplies all three: Check recomputes the artifact's digest instead of trusting the
// record, Comparable enforces the record's own stated non-comparability rules, and
// HistoricalFailures carries the known limitations alongside the number so nobody has to
// rediscover them.
//
// It never edits the artifact it describes. testdata/baseline/phase0.json keeps its original
// definitions and bytes (M0-04); this record is added beside it, separately versioned.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// BaselineProvenanceVersion is the format version LoadBaselineProvenance accepts.
const BaselineProvenanceVersion = 1

// The two accepted MetricDefinition.Direction values.
const (
	// DirectionHigherBetter marks a metric a policy wants to maximize.
	DirectionHigherBetter = "higher_better"
	// DirectionLowerBetter marks a metric a policy wants to minimize.
	DirectionLowerBetter = "lower_better"
)

// unknownDirtyState is what DirtyChanges says when the working-tree state at measurement time was
// never recorded. It is deliberately not the empty string: an unrecorded observation has to be
// stated, not inferred from an absence.
const unknownDirtyState = "unknown"

// MetricDefinition is one metric's name, direction and meaning, as of the artifact this record
// describes.
//
// The definition travels with the number because "the metric moved" and "the metric now means
// something else" are otherwise indistinguishable, and the second one masquerading as the first is
// how a regression gets shipped.
type MetricDefinition struct {
	// Name is the metric's key in the artifact.
	Name string `json:"name"`
	// Direction is DirectionHigherBetter or DirectionLowerBetter.
	Direction string `json:"direction"`
	// Definition is one line saying what the metric measures.
	Definition string `json:"definition"`
}

// BaselineCorpus identifies the sessions a baseline was measured over.
type BaselineCorpus struct {
	// Path is the corpus directory, relative to the repository root.
	Path string `json:"path"`
	// SHA256 is the corpus manifest's digest, as the artifact itself records it.
	SHA256 string `json:"sha256"`
	// Tier is the §6.3 fidelity tier: "synthetic" or "recorded". A synthetic number reported as a
	// real-session number is the dishonest measurement §1.3 RC-3 indicts, so the tier travels with
	// the number.
	Tier string `json:"tier"`
	// Sessions is how many sessions the corpus holds.
	Sessions int `json:"sessions"`
	// Generator names the generator that produced the corpus.
	Generator string `json:"generator"`
}

// BaselineProvenance is everything needed to check a committed baseline artifact and to decide
// whether a later one may be compared to it.
type BaselineProvenance struct {
	// Version is the artifact format version.
	Version int `json:"version"`
	// Artifact is the baseline file this record describes, relative to the repository root.
	Artifact string `json:"artifact"`
	// ArtifactSHA256 is that file's digest, CRLF-normalized.
	ArtifactSHA256 string `json:"artifact_sha256"`
	// Generator names the driver that produced the artifact.
	Generator string `json:"generator"`
	// Snapshot is the commit that last wrote the artifact, and its date.
	Snapshot string `json:"snapshot"`
	// DirtyChanges describes the working-tree state at measurement time, or unknownDirtyState when
	// it was never recorded. Unknown is not clean.
	DirtyChanges string `json:"dirty_changes"`
	// Corpus identifies what was measured.
	Corpus BaselineCorpus `json:"corpus"`
	// Latency is "modelled" when the artifact's latency numbers were computed rather than
	// observed, and "observed" when a wall clock produced them.
	Latency string `json:"latency"`
	// MetricDefinitions defines every metric the artifact reports.
	MetricDefinitions []MetricDefinition `json:"metric_definitions"`
	// Seed records what made the measurement reproducible, and what did not depend on a seed.
	Seed string `json:"seed"`
	// Model names the model, or "none".
	Model string `json:"model"`
	// Provider names the provider, or "none".
	Provider string `json:"provider"`
	// Date is when the artifact was written.
	Date string `json:"date"`
	// HistoricalFailures carries the known limitations of this baseline: the defects it shipped
	// with, and the checks that never ran. They travel with the number so a reader cannot take it
	// for more than it is.
	HistoricalFailures []string `json:"historical_failures"`
	// NewFields declares the fields this artifact added relative to the artifact it supersedes. It
	// is empty for a first baseline, which supersedes nothing. Comparable derives the real metric
	// delta from MetricDefinitions rather than trusting this declaration.
	NewFields []string `json:"new_fields"`
	// OldFields declares the fields this artifact dropped relative to the artifact it supersedes,
	// on the same terms as NewFields.
	OldFields []string `json:"old_fields"`
	// NonComparable states, in prose, the rules under which two baselines may not be compared.
	// Comparable enforces exactly these, so a reader of the JSON alone learns them.
	NonComparable []string `json:"non_comparable"`
}

// LoadBaselineProvenance reads a provenance record from disk without validating it.
func LoadBaselineProvenance(path string) (BaselineProvenance, error) {
	var p BaselineProvenance
	raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named provenance artifact
	if err != nil {
		return p, fmt.Errorf("eval: reading the baseline provenance %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("eval: parsing the baseline provenance %s: %w", path, err)
	}
	return p, nil
}

// Validate checks the record's own invariants.
func (p BaselineProvenance) Validate() error {
	if p.Version != BaselineProvenanceVersion {
		return fmt.Errorf("eval: baseline provenance version %d, want %d", p.Version, BaselineProvenanceVersion)
	}
	if p.Artifact == "" {
		return fmt.Errorf("eval: baseline provenance names no artifact")
	}
	if !isSHA256(p.ArtifactSHA256) {
		return fmt.Errorf("eval: baseline provenance artifact sha256 %q is not 64 lowercase hex characters", p.ArtifactSHA256)
	}
	if !isSHA256(p.Corpus.SHA256) {
		return fmt.Errorf("eval: baseline provenance corpus sha256 %q is not 64 lowercase hex characters", p.Corpus.SHA256)
	}
	if p.Corpus.Sessions <= 0 {
		return fmt.Errorf("eval: baseline provenance records %d corpus sessions", p.Corpus.Sessions)
	}
	for name, v := range map[string]string{
		"generator":        p.Generator,
		"snapshot":         p.Snapshot,
		"latency mode":     p.Latency,
		"corpus path":      p.Corpus.Path,
		"corpus tier":      p.Corpus.Tier,
		"corpus generator": p.Corpus.Generator,
		"seed":             p.Seed,
		"model":            p.Model,
		"provider":         p.Provider,
		"date":             p.Date,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("eval: baseline provenance records no %s", name)
		}
	}
	if strings.TrimSpace(p.DirtyChanges) == "" {
		return fmt.Errorf(
			"eval: baseline provenance records no dirty changes; write %q rather than leaving it blank, because an unrecorded tree is not a clean one",
			unknownDirtyState)
	}
	return p.validateMetrics()
}

// validateMetrics checks the metric definition table.
func (p BaselineProvenance) validateMetrics() error {
	if len(p.MetricDefinitions) == 0 {
		return fmt.Errorf("eval: baseline provenance defines no metric; an undefined number cannot be compared to anything")
	}
	seen := map[string]bool{}
	for _, d := range p.MetricDefinitions {
		if d.Name == "" {
			return fmt.Errorf("eval: baseline provenance has an unnamed metric definition")
		}
		if seen[d.Name] {
			return fmt.Errorf("eval: baseline provenance defines metric %q twice", d.Name)
		}
		seen[d.Name] = true
		if d.Direction != DirectionHigherBetter && d.Direction != DirectionLowerBetter {
			return fmt.Errorf("eval: metric %q has direction %q, want %q or %q",
				d.Name, d.Direction, DirectionHigherBetter, DirectionLowerBetter)
		}
		if strings.TrimSpace(d.Definition) == "" {
			return fmt.Errorf("eval: metric %q has no definition", d.Name)
		}
	}
	if len(p.NonComparable) == 0 {
		return fmt.Errorf("eval: baseline provenance states no non-comparable rules")
	}
	return nil
}

// baselineArtifact is the subset of a committed baseline file this package reads.
//
// It is a local reader on purpose: test/replay owns the baseline's full envelope, and eval is
// foundation-only (§3.2) and may not import a composition root. Adding a field here is additive;
// it never redefines what the artifact means.
type baselineArtifact struct {
	Generator    string `json:"generator"`
	CorpusTier   string `json:"corpusTier"`
	CorpusSHA256 string `json:"corpusSHA256"`
	Sessions     int    `json:"sessions"`
	Latency      string `json:"latency"`
}

// Check verifies this record against the artifact at artifactPath.
//
// It recomputes the digest rather than trusting ArtifactSHA256, so an artifact edited after the
// record was written is caught here rather than never. It then cross-checks the facts the artifact
// states about itself — corpus digest, session count, tier, generator, latency mode — against the
// facts this record claims, because a provenance record that has drifted from its artifact is
// worse than none.
func (p BaselineProvenance) Check(artifactPath string) error {
	raw, err := os.ReadFile(artifactPath) //nolint:gosec // an explicitly named baseline artifact
	if err != nil {
		return fmt.Errorf("eval: reading the baseline artifact %s: %w", artifactPath, err)
	}
	if got := digestOfBytes(raw); got != p.ArtifactSHA256 {
		return fmt.Errorf("eval: baseline artifact %s has sha256 %s, but its provenance records %s",
			artifactPath, got, p.ArtifactSHA256)
	}

	var a baselineArtifact
	if err := json.Unmarshal(raw, &a); err != nil {
		return fmt.Errorf("eval: parsing the baseline artifact %s: %w", artifactPath, err)
	}
	if a.CorpusSHA256 != p.Corpus.SHA256 {
		return fmt.Errorf("eval: baseline artifact %s names corpus %s, but its provenance records corpus %s",
			artifactPath, a.CorpusSHA256, p.Corpus.SHA256)
	}
	if a.Sessions != p.Corpus.Sessions {
		return fmt.Errorf("eval: baseline artifact %s reports %d sessions, but its provenance records %d",
			artifactPath, a.Sessions, p.Corpus.Sessions)
	}
	if a.CorpusTier != p.Corpus.Tier {
		return fmt.Errorf("eval: baseline artifact %s is tier %q, but its provenance records tier %q",
			artifactPath, a.CorpusTier, p.Corpus.Tier)
	}
	if a.Generator != p.Generator {
		return fmt.Errorf("eval: baseline artifact %s names generator %q, but its provenance records %q",
			artifactPath, a.Generator, p.Generator)
	}
	if a.Latency != p.Latency {
		return fmt.Errorf("eval: baseline artifact %s is latency %q, but its provenance records %q",
			artifactPath, a.Latency, p.Latency)
	}
	return nil
}

// Comparable reports whether two baselines may be compared, and returns what a reader has to know
// either way.
//
// The five rules are the ones prev.NonComparable states in prose: a corpus digest change, a tier
// change, a generator change on either side of the pipeline, a latency-mode change, or any
// metric-definition change. Any of them means the two artifacts are not two measurements of the
// same thing, and no percentage between them means anything.
//
// Notes come back on BOTH paths. When the answer is false they are the reasons; when it is true
// they are the caveats — chiefly an unrecorded working-tree state, which stays visible as unknown
// instead of being quietly upgraded on the way through a successful comparison.
func Comparable(prev, next BaselineProvenance) (bool, []string) {
	var reasons, caveats []string

	if prev.Corpus.SHA256 != next.Corpus.SHA256 {
		reasons = append(reasons, fmt.Sprintf(
			"the corpus sha256 changed from %s to %s: the two baselines were measured over different sessions",
			prev.Corpus.SHA256, next.Corpus.SHA256))
	}
	if prev.Corpus.Sessions != next.Corpus.Sessions {
		reasons = append(reasons, fmt.Sprintf(
			"the corpus sessions count changed from %d to %d", prev.Corpus.Sessions, next.Corpus.Sessions))
	}
	if prev.Corpus.Tier != next.Corpus.Tier {
		reasons = append(reasons, fmt.Sprintf(
			"the corpus tier changed from %q to %q: the §6.3 fidelity tiers are different populations",
			prev.Corpus.Tier, next.Corpus.Tier))
	}
	if prev.Corpus.Generator != next.Corpus.Generator {
		reasons = append(reasons, fmt.Sprintf(
			"the corpus generator changed from %q to %q", prev.Corpus.Generator, next.Corpus.Generator))
	}
	if prev.Generator != next.Generator {
		reasons = append(reasons, fmt.Sprintf(
			"the driver generator changed from %q to %q: the measurement procedure differs",
			prev.Generator, next.Generator))
	}
	if prev.Latency != next.Latency {
		reasons = append(reasons, fmt.Sprintf(
			"the latency mode changed from %q to %q: modelled pauses were computed and never measured",
			prev.Latency, next.Latency))
	}
	reasons = append(reasons, metricDeltas(prev, next)...)

	for label, p := range map[string]BaselineProvenance{"earlier": prev, "later": next} {
		if strings.TrimSpace(p.DirtyChanges) == "" || p.DirtyChanges == unknownDirtyState {
			caveats = append(caveats, fmt.Sprintf(
				"the %s baseline's dirty_changes is %q: the working tree state when that artifact "+
					"was written was never recorded, so no difference between the two runs can be "+
					"attributed to it",
				label, unknownDirtyState))
		}
	}

	if len(reasons) > 0 {
		return false, append(reasons, caveats...)
	}
	return true, caveats
}

// metricDeltas reports every metric-definition change between two records, in prev's order first
// so the output is stable.
func metricDeltas(prev, next BaselineProvenance) []string {
	prevBy := make(map[string]MetricDefinition, len(prev.MetricDefinitions))
	for _, d := range prev.MetricDefinitions {
		prevBy[d.Name] = d
	}
	nextBy := make(map[string]MetricDefinition, len(next.MetricDefinitions))
	for _, d := range next.MetricDefinitions {
		nextBy[d.Name] = d
	}

	var out []string
	for _, d := range prev.MetricDefinitions {
		n, ok := nextBy[d.Name]
		if !ok {
			out = append(out, fmt.Sprintf("metric %s was removed", d.Name))
			continue
		}
		if n.Direction != d.Direction {
			out = append(out, fmt.Sprintf("metric %s changed direction from %q to %q",
				d.Name, d.Direction, n.Direction))
		}
		if n.Definition != d.Definition {
			out = append(out, fmt.Sprintf("metric %s was redefined", d.Name))
		}
	}
	for _, d := range next.MetricDefinitions {
		if _, ok := prevBy[d.Name]; !ok {
			out = append(out, fmt.Sprintf("metric %s was added", d.Name))
		}
	}
	return out
}

// digestOfBytes is the CRLF-normalized SHA-256 every digest in this package is computed with. It
// matches test/replay's manifestDigest, so a checkout that ignored .gitattributes cannot turn a
// pinned digest into a line-ending argument.
func digestOfBytes(raw []byte) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(raw), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// isSHA256 reports whether s is 64 lowercase hex characters.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

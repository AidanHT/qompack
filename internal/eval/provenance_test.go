package eval_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// phase0ArtifactSHA256 pins testdata/baseline/phase0.json.
//
// The artifact is the historical Phase 0 answer and M0-04 forbids replacing it. Pinning its digest
// in source is what makes "preserved" checkable: any edit to the old numbers — including one made
// to hide a regression — fails this test rather than being noticed later, or never.
const phase0ArtifactSHA256 = "5e2f1ff4deafa9f7b41977b6b7a467bda138cd030b3f25888d0700bb72627d5d"

// phase0CorpusSHA256 pins the corpus the artifact was measured over, as the artifact itself
// records it.
const phase0CorpusSHA256 = "e2d9fa5aa94946582e2ab711d74dc55e74b9a413bd57e4b9c465351984707fb3"

func baselinePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "baseline", name)
}

// digestOf hashes a committed file the way test/replay's manifestDigest does: CRLF-normalized, so
// a checkout that ignored .gitattributes cannot change a pinned digest.
func digestOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(raw), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// loadPhase0Provenance reads and validates the committed provenance record.
func loadPhase0Provenance(t *testing.T) eval.BaselineProvenance {
	t.Helper()
	p, err := eval.LoadBaselineProvenance(baselinePath(t, "phase0.provenance.v1.json"))
	require.NoError(t, err)
	require.NoError(t, p.Validate())
	return p
}

// TestPhase0Artifact_DigestIsPinned: the old artifact cannot change silently.
func TestPhase0Artifact_DigestIsPinned(t *testing.T) {
	require.Equal(t, phase0ArtifactSHA256, digestOf(t, baselinePath(t, "phase0.json")),
		"testdata/baseline/phase0.json is the preserved historical baseline (M0-04); it is not edited in place")
}

// TestBaselineProvenance_ChecksTheArtifactItDescribes: the committed record validates against the
// committed artifact, digest, corpus hash, session count, tier, generator and latency mode.
func TestBaselineProvenance_ChecksTheArtifactItDescribes(t *testing.T) {
	p := loadPhase0Provenance(t)

	require.Equal(t, 1, p.Version)
	require.Equal(t, "testdata/baseline/phase0.json", p.Artifact)
	require.Equal(t, phase0ArtifactSHA256, p.ArtifactSHA256)
	require.Equal(t, "test/replay/1", p.Generator)
	require.Equal(t, "modelled", p.Latency)
	require.Equal(t, phase0CorpusSHA256, p.Corpus.SHA256)
	require.Equal(t, "synthetic", p.Corpus.Tier)
	require.Equal(t, 24, p.Corpus.Sessions)
	require.Equal(t, "eval.Synthesize/1", p.Corpus.Generator)
	require.Equal(t, "none", p.Provider)
	require.Equal(t, "none", p.Model)

	require.NoError(t, p.Check(baselinePath(t, "phase0.json")))
}

// TestBaselineProvenance_RecordsTheSnapshotAndTheUnknownTreeState: the commit that last wrote the
// artifact is named, and the working-tree state at that moment is recorded as unknown rather than
// assumed clean. An unrecorded observation is not a clean observation.
func TestBaselineProvenance_RecordsTheSnapshotAndTheUnknownTreeState(t *testing.T) {
	p := loadPhase0Provenance(t)

	require.Contains(t, p.Snapshot, "299d026baf36d5e72e48e7ca82bf4a146834ebe0")
	require.Contains(t, p.Snapshot, "2026-08-25")
	require.Equal(t, "unknown", p.DirtyChanges)
	require.Equal(t, "2026-08-25", p.Date)
	require.Contains(t, p.Seed, "CORPUS.json")
	require.Contains(t, p.Seed, "1001")
}

// TestBaselineProvenance_CheckRejectsAChangedArtifact: Check recomputes the digest rather than
// trusting the record, so an artifact edited after the record was written is caught.
func TestBaselineProvenance_CheckRejectsAChangedArtifact(t *testing.T) {
	p := loadPhase0Provenance(t)

	raw, err := os.ReadFile(baselinePath(t, "phase0.json"))
	require.NoError(t, err)

	tampered := filepath.Join(t.TempDir(), "phase0.json")
	require.NoError(t, os.WriteFile(tampered,
		[]byte(strings.Replace(string(raw), "0.695164", "0.795164", 1)), 0o600))

	err = p.Check(tampered)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sha256")

	t.Run("a changed corpus hash is caught too", func(t *testing.T) {
		q := p
		q.ArtifactSHA256 = digestOf(t, tampered)
		q.Corpus.SHA256 = strings.Repeat("0", 64)
		require.ErrorContains(t, q.Check(tampered), "corpus")
	})

	t.Run("a changed session count is caught too", func(t *testing.T) {
		q := p
		q.Corpus.Sessions = 23
		require.ErrorContains(t, q.Check(baselinePath(t, "phase0.json")), "sessions")
	})

	t.Run("a missing artifact is an error, not a pass", func(t *testing.T) {
		require.Error(t, p.Check(filepath.Join(t.TempDir(), "absent.json")))
	})
}

// TestBaselineProvenance_DefinesEveryMetricTheArtifactReports: a baseline whose metric definitions
// are missing cannot be compared to anything, because "the number moved" and "the number now means
// something else" are indistinguishable without them.
func TestBaselineProvenance_DefinesEveryMetricTheArtifactReports(t *testing.T) {
	p := loadPhase0Provenance(t)

	defined := map[string]eval.MetricDefinition{}
	for _, d := range p.MetricDefinitions {
		require.NotEmpty(t, d.Definition, "metric %s carries no definition", d.Name)
		require.NotContains(t, defined, d.Name, "metric %s is defined twice", d.Name)
		defined[d.Name] = d
	}

	// Every metric the canonical list names.
	for _, name := range eval.MetricNames() {
		d, ok := defined[name]
		require.True(t, ok, "metric %s has no definition in the provenance record", name)
		want := "lower_better"
		if eval.MetricDirection(name) == eval.DirHigherBetter {
			want = "higher_better"
		}
		require.Equal(t, want, d.Direction, "metric %s", name)
	}

	// Every metric the committed artifact actually reports, read from the artifact itself rather
	// than from the code, so a metric that exists only in the old file is still covered.
	var artifact struct {
		Policies map[string]map[string]float64 `json:"policies"`
	}
	raw, err := os.ReadFile(baselinePath(t, "phase0.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &artifact))
	require.NotEmpty(t, artifact.Policies)

	for policy, metrics := range artifact.Policies {
		for name := range metrics {
			require.Contains(t, defined, name, "policy %s reports %s, which the provenance record does not define", policy, name)
		}
	}
	require.Len(t, defined, len(eval.MetricNames()))
}

// TestBaselineProvenance_RetainsTheHistoricalFailures: M0-G6 requires the failures to travel with
// the number. SP02-D1 through SP02-D6 say why the corpus cannot exercise several metrics, B11 says
// the 0.695164 is a synthetic metric rather than a task ceiling, and J5 records that no CI job ever
// ran for this baseline.
func TestBaselineProvenance_RetainsTheHistoricalFailures(t *testing.T) {
	p := loadPhase0Provenance(t)
	joined := strings.Join(p.HistoricalFailures, "\n")

	for _, id := range []string{"SP02-D1", "SP02-D2", "SP02-D3", "SP02-D4", "SP02-D5", "SP02-D6"} {
		require.Contains(t, joined, id)
	}
	require.Contains(t, joined, "0.695164")
	require.Contains(t, joined, "not a task ceiling")
	require.Contains(t, joined, "J5")
	require.Contains(t, joined, "waived")
	require.Len(t, p.HistoricalFailures, 8)
}

// TestComparable_RejectsACorpusChange is the headline rule: two baselines measured over different
// sessions are not two measurements of the same thing.
func TestComparable_RejectsACorpusChange(t *testing.T) {
	prev := loadPhase0Provenance(t)
	next := prev
	next.Corpus.SHA256 = strings.Repeat("a", 64)

	ok, notes := eval.Comparable(prev, next)
	require.False(t, ok)
	require.NotEmpty(t, notes)
	require.Contains(t, strings.Join(notes, "\n"), "corpus")
}

// TestComparable_RejectsEveryNonComparableRule walks the five rules the record itself states.
func TestComparable_RejectsEveryNonComparableRule(t *testing.T) {
	prev := loadPhase0Provenance(t)

	cases := []struct {
		name string
		want string
		fn   func(p *eval.BaselineProvenance)
	}{
		{"corpus sha", "corpus", func(p *eval.BaselineProvenance) { p.Corpus.SHA256 = strings.Repeat("a", 64) }},
		{"corpus tier", "tier", func(p *eval.BaselineProvenance) { p.Corpus.Tier = "recorded" }},
		{"corpus generator", "generator", func(p *eval.BaselineProvenance) { p.Corpus.Generator = "eval.Synthesize/2" }},
		{"driver generator", "generator", func(p *eval.BaselineProvenance) { p.Generator = "test/replay/2" }},
		{"latency mode", "latency", func(p *eval.BaselineProvenance) { p.Latency = "observed" }},
		{"sessions", "sessions", func(p *eval.BaselineProvenance) { p.Corpus.Sessions = 48 }},
		{"metric removed", "fraction_of_opt", func(p *eval.BaselineProvenance) {
			p.MetricDefinitions = p.MetricDefinitions[1:]
		}},
		{"metric redefined", "rewrite_tokens", func(p *eval.BaselineProvenance) {
			for i := range p.MetricDefinitions {
				if p.MetricDefinitions[i].Name == "rewrite_tokens" {
					p.MetricDefinitions[i].Definition = "something else entirely"
				}
			}
		}},
		{"metric redirected", "fraction_of_opt", func(p *eval.BaselineProvenance) {
			for i := range p.MetricDefinitions {
				if p.MetricDefinitions[i].Name == "fraction_of_opt" {
					p.MetricDefinitions[i].Direction = "lower_better"
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := prev
			next.MetricDefinitions = append([]eval.MetricDefinition(nil), prev.MetricDefinitions...)
			tc.fn(&next)

			ok, notes := eval.Comparable(prev, next)
			require.False(t, ok)
			require.Contains(t, strings.Join(notes, "\n"), tc.want)
		})
	}
}

// TestComparable_UnknownTelemetryStaysUnknown: comparing the record to itself is legitimate, but
// the unrecorded working-tree state must surface as a caveat rather than be quietly upgraded to
// "clean". An unknown that disappears on a comparison is an unknown that gets forgotten.
func TestComparable_UnknownTelemetryStaysUnknown(t *testing.T) {
	prev := loadPhase0Provenance(t)

	ok, notes := eval.Comparable(prev, prev)
	require.True(t, ok, "a record is comparable to itself")

	joined := strings.Join(notes, "\n")
	require.Contains(t, joined, "unknown")
	require.Contains(t, joined, "dirty")
	require.NotContains(t, strings.ToLower(joined), "clean")

	t.Run("a recorded clean tree needs no caveat", func(t *testing.T) {
		a, b := prev, prev
		a.DirtyChanges = "none"
		b.DirtyChanges = "none"
		ok, notes := eval.Comparable(a, b)
		require.True(t, ok)
		require.Empty(t, notes)
	})
}

// TestBaselineProvenance_Validate covers the record's own invariants.
func TestBaselineProvenance_Validate(t *testing.T) {
	prev := loadPhase0Provenance(t)

	cases := []struct {
		name string
		want string
		fn   func(p *eval.BaselineProvenance)
	}{
		{"version", "version", func(p *eval.BaselineProvenance) { p.Version = 0 }},
		{"artifact", "artifact", func(p *eval.BaselineProvenance) { p.Artifact = "" }},
		{"digest length", "sha256", func(p *eval.BaselineProvenance) { p.ArtifactSHA256 = "abc" }},
		{"corpus digest", "corpus", func(p *eval.BaselineProvenance) { p.Corpus.SHA256 = "abc" }},
		{"sessions", "sessions", func(p *eval.BaselineProvenance) { p.Corpus.Sessions = 0 }},
		{"dirty changes", "dirty", func(p *eval.BaselineProvenance) { p.DirtyChanges = "" }},
		{"metric direction", "direction", func(p *eval.BaselineProvenance) {
			p.MetricDefinitions = append([]eval.MetricDefinition(nil), p.MetricDefinitions...)
			p.MetricDefinitions[0].Direction = "sideways"
		}},
		{"no metrics", "metric", func(p *eval.BaselineProvenance) { p.MetricDefinitions = nil }},
		{"non-comparable rules", "non-comparable", func(p *eval.BaselineProvenance) { p.NonComparable = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := prev
			tc.fn(&p)
			require.ErrorContains(t, p.Validate(), tc.want)
		})
	}

	t.Run("a missing file is an error", func(t *testing.T) {
		_, err := eval.LoadBaselineProvenance(filepath.Join(t.TempDir(), "absent.json"))
		require.Error(t, err)
	})
}

// TestBaselineProvenance_CheckRejectsADriftedRecord: the artifact states several facts about
// itself, and a provenance record that disagrees with any of them has drifted from the thing it
// describes — which is worse than having no record, because it looks authoritative.
func TestBaselineProvenance_CheckRejectsADriftedRecord(t *testing.T) {
	prev := loadPhase0Provenance(t)
	artifact := baselinePath(t, "phase0.json")

	cases := []struct {
		name string
		want string
		fn   func(p *eval.BaselineProvenance)
	}{
		{"tier", "tier", func(p *eval.BaselineProvenance) { p.Corpus.Tier = "recorded" }},
		{"generator", "generator", func(p *eval.BaselineProvenance) { p.Generator = "test/replay/2" }},
		{"latency", "latency", func(p *eval.BaselineProvenance) { p.Latency = "observed" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := prev
			tc.fn(&p)
			require.ErrorContains(t, p.Check(artifact), tc.want)
		})
	}

	t.Run("an unparseable artifact is an error", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "phase0.json")
		require.NoError(t, os.WriteFile(broken, []byte("{"), 0o600))
		p := prev
		p.ArtifactSHA256 = digestOf(t, broken)
		require.ErrorContains(t, p.Check(broken), "parsing")
	})
}

// TestLoadBaselineProvenance_RejectsMalformedJSON: an unreadable record is an error, never an empty
// one that would validate away every claim it was supposed to carry.
func TestLoadBaselineProvenance_RejectsMalformedJSON(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "provenance.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err := eval.LoadBaselineProvenance(bad)
	require.ErrorContains(t, err, "parsing the baseline provenance")
}

// TestBaselineProvenance_ValidateMetricTable covers the metric table's own invariants.
func TestBaselineProvenance_ValidateMetricTable(t *testing.T) {
	prev := loadPhase0Provenance(t)

	cases := []struct {
		name string
		want string
		fn   func(p *eval.BaselineProvenance)
	}{
		{"unnamed metric", "unnamed", func(p *eval.BaselineProvenance) { p.MetricDefinitions[0].Name = "" }},
		{"duplicate metric", "twice", func(p *eval.BaselineProvenance) {
			p.MetricDefinitions[1].Name = p.MetricDefinitions[0].Name
		}},
		{"empty definition", "no definition", func(p *eval.BaselineProvenance) {
			p.MetricDefinitions[0].Definition = "  "
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := prev
			p.MetricDefinitions = append([]eval.MetricDefinition(nil), prev.MetricDefinitions...)
			tc.fn(&p)
			require.ErrorContains(t, p.Validate(), tc.want)
		})
	}

	t.Run("uppercase hex is not a digest", func(t *testing.T) {
		p := prev
		p.ArtifactSHA256 = strings.ToUpper(prev.ArtifactSHA256)
		require.ErrorContains(t, p.Validate(), "sha256")
	})

	t.Run("an added metric is a reason too", func(t *testing.T) {
		next := prev
		next.MetricDefinitions = append(append([]eval.MetricDefinition(nil), prev.MetricDefinitions...),
			eval.MetricDefinition{Name: "novel_metric", Direction: "higher_better", Definition: "new"})
		ok, notes := eval.Comparable(prev, next)
		require.False(t, ok)
		require.Contains(t, strings.Join(notes, "\n"), "novel_metric was added")
	})
}

// TestBaselineProvenance_StatesItsOwnNonComparableRules: the record carries the rules Comparable
// enforces, so a reader of the JSON alone learns them without reading the code.
func TestBaselineProvenance_StatesItsOwnNonComparableRules(t *testing.T) {
	p := loadPhase0Provenance(t)
	joined := strings.Join(p.NonComparable, "\n")

	for _, want := range []string{"corpus SHA-256", "tier", "generator", "latency", "metric-definition"} {
		require.Contains(t, joined, want)
	}
	require.Len(t, p.NonComparable, 5)

	// phase0 is the first baseline, so it supersedes nothing and declares no field delta.
	require.Empty(t, p.NewFields)
	require.Empty(t, p.OldFields)
}

// TestRecallBaselineProvenance_DescribesTheV4Rebaseline covers the artifact the V4 SP-02 work
// added BESIDE the preserved 2026-08-25 one.
//
// The old baseline is not edited in place (M0-04), so the re-baseline is a second artifact, and a
// second artifact that nothing checked would be exactly the drift this file exists to stop. Three
// things are asserted: it validates and its digest matches the file, eval.Comparable refuses it
// against the old one, and every one of the old record's eight historical failures still travels
// with it — the whole point of preserving both is that the new number does not erase what the old
// one said.
func TestRecallBaselineProvenance_DescribesTheV4Rebaseline(t *testing.T) {
	old := loadPhase0Provenance(t)

	next, err := eval.LoadBaselineProvenance(baselinePath(t, "phase0-recall.provenance.v1.json"))
	require.NoError(t, err)
	require.NoError(t, next.Validate())
	require.NoError(t, next.Check(baselinePath(t, "phase0-recall.json")))
	require.Equal(t, "testdata/baseline/phase0-recall.json", next.Artifact)
	require.Equal(t, "eval.Synthesize/2", next.Corpus.Generator)

	ok, notes := eval.Comparable(old, next)
	require.False(t, ok, "a re-baseline over a regenerated corpus is not a later reading of the old one")
	joined := strings.Join(notes, "\n")
	require.Contains(t, joined, "corpus")
	require.Contains(t, joined, "decision_preservation")

	carried := strings.Join(next.HistoricalFailures, "\n")
	for _, f := range old.HistoricalFailures {
		require.Contains(t, carried, f, "the re-baseline dropped a failure the old record carried")
	}
	require.Contains(t, carried, "0.695164", "the superseded headline number travels beside the new one")
	require.Contains(t, carried, "J5", "the waived-open CI-gate obligation is not discharged by a re-baseline")
}

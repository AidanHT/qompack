package docs

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// troubleshootingRel is the page these checks are about, named once.
const troubleshootingRel = "docs/troubleshooting.md"

// troubleshootingPath resolves the page against the module root.
func troubleshootingPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), filepath.FromSlash(troubleshootingRel))
}

// codeSpan reports whether the page names token in an inline code span, outside fenced blocks.
//
// This is stricter than userguide_test.go's mentions, and deliberately so: a troubleshooting page
// that merely happened to use the word "denied" in a sentence would satisfy a substring search
// while documenting nothing. A code span is the page committing to the identifier as an
// identifier, and the comparison is on the span's exact contents, so `expired` is not satisfied by
// `expired_deleted`.
func codeSpan(lines []string, token string) bool {
	for _, l := range lines {
		for _, span := range inlineCodeRe.FindAllString(l, -1) {
			if strings.Trim(span, "`") == token {
				return true
			}
		}
	}
	return false
}

// sourceLines reads a Go source file of this repository by its repo-relative slash path.
//
// test/docs is stdlib-only by Commit 1's contract — it may not import internal/core or
// internal/cli to enumerate their constants — so the inventories below are derived by reading the
// declaring source. That is weaker than a compiler check and stronger than a hand-written list:
// a value added to a closed enumeration fails here until the page documents it, and a value
// renamed fails here until both move together.
func sourceLines(t *testing.T, rel string) []string {
	t.Helper()
	return readLines(t, filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
}

// outcomeConstRe matches one core.EvidenceOutcome constant declaration and captures its value:
//
//	OutcomeUnavailable EvidenceOutcome = "unavailable"
var outcomeConstRe = regexp.MustCompile(`^\s*Outcome\w+\s+EvidenceOutcome\s*=\s*"([a-z_]+)"`)

// evidenceOutcomes returns every core.EvidenceOutcome value, read from its declaring file.
func evidenceOutcomes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, l := range sourceLines(t, "internal/core/evidence.go") {
		if m := outcomeConstRe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatal("internal/core/evidence.go: no EvidenceOutcome constants matched: the declaration shape changed")
	}
	return out
}

// TestTroubleshootingNamesEveryEvidenceOutcome fails if the page leaves a retrieval outcome
// undocumented. The whole point of the page's "denied or unavailable evidence" section is that a
// caller must not read one outcome as another, which it can only do for outcomes it names.
func TestTroubleshootingNamesEveryEvidenceOutcome(t *testing.T) {
	lines := defenced(t, troubleshootingPath(t))
	for _, name := range evidenceOutcomes(t) {
		if !codeSpan(lines, name) {
			t.Errorf("%s: no code span names the evidence outcome %q", troubleshootingRel, name)
		}
	}
}

// selfTestCheckIDRe matches one self-test check's ID field and captures the check name:
//
//	ID: "daemon.reachable", OK: true, Severity: contract.SevInfo,
var selfTestCheckIDRe = regexp.MustCompile(`^\s*ID:\s*"([a-z0-9._]+)"`)

// selfTestCheckNames returns the distinct check names internal/cli/selftest.go declares, sorted so
// a failure list reads the same way twice. The contract assertions self-test also reports are not
// here: their ids come from internal/contract (see contractAssertionIDs), and selftest.go passes
// them through as `ID: string(r.ID)`, which is not a literal and must not be.
func selfTestCheckNames(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, l := range sourceLines(t, "internal/cli/selftest.go") {
		if m := selfTestCheckIDRe.FindStringSubmatch(l); m != nil {
			seen[m[1]] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("internal/cli/selftest.go: no check ID literals matched: the declaration shape changed")
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestTroubleshootingNamesEverySelfTestCheck fails if the page tells a reader to start with
// `qompack self-test` and then does not say what one of its rows means. Derived from the source,
// never counted: a check added to selftest.go fails here until the page explains it.
func TestTroubleshootingNamesEverySelfTestCheck(t *testing.T) {
	lines := defenced(t, troubleshootingPath(t))
	for _, name := range selfTestCheckNames(t) {
		if !codeSpan(lines, name) {
			t.Errorf("%s: no code span names the self-test check %q", troubleshootingRel, name)
		}
	}
}

// contractIDRe matches one contract.ID constant declaration and captures its string value:
//
//	CAdditionalContext ID = "hook.additional_context_delivered"
var contractIDRe = regexp.MustCompile(`^\s*C\w+\s+ID\s*=\s*"([a-z0-9._]+)"`)

// contractAssertionIDs returns every host-contract assertion id, read from its declaring file.
func contractAssertionIDs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, l := range sourceLines(t, "internal/contract/ids.go") {
		if m := contractIDRe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatal("internal/contract/ids.go: no contract ID constants matched: the declaration shape changed")
	}
	return out
}

// TestTroubleshootingNamesEveryContractAssertion covers the other half of the self-test table. The
// page's claim is that an `ok` row whose OBSERVED column reads not-yet-implemented asserts nothing;
// that claim is only usable against assertions the reader can find by name.
func TestTroubleshootingNamesEveryContractAssertion(t *testing.T) {
	lines := defenced(t, troubleshootingPath(t))
	for _, name := range contractAssertionIDs(t) {
		if !codeSpan(lines, name) {
			t.Errorf("%s: no code span names the host-contract assertion %q", troubleshootingRel, name)
		}
	}
}

// configReferenceSectionLinks are the three compatibility sections Commit 2's generator emits.
// The page must send a reader to the generated page rather than restate a table that would drift
// from it; TestRelativeLinksResolve then proves each anchor still exists.
var configReferenceSectionLinks = []string{
	"config-reference.md#versioned-blocks",
	"config-reference.md#gated-switches-ship-off",
	"config-reference.md#retired-meaning-keys",
}

// TestTroubleshootingLinksConfigReferenceSections fails if the configuration section stops
// pointing at the generated page's own compatibility sections. It checks the link targets only —
// whether those anchors resolve is TestRelativeLinksResolve's job, and duplicating it here would
// make one broken anchor fail twice with two different messages.
func TestTroubleshootingLinksConfigReferenceSections(t *testing.T) {
	body := strings.Join(scrubbed(t, troubleshootingPath(t)), "\n")
	for _, want := range configReferenceSectionLinks {
		if !strings.Contains(body, "("+want+")") {
			t.Errorf("%s: does not link %s", troubleshootingRel, want)
		}
	}
}

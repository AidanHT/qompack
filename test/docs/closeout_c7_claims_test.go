package docs

import (
	"strings"
	"testing"
)

// The candidate 7 live lane (plans/sdd/V6-closeout/live/rerun-c7/, decisions D58(f) and D59)
// observed facts that retired sentences on these pages: the namespace under a release entry, how
// `claude plugin details` resolves its argument, when a Windows staged copy is pruned, and the
// D50 order of the rehydration's section 2. The checks below keep those sentences from coming
// back, in the same form as closeoutStaleClaims.

var closeoutC7StaleClaims = []staleClaim{
	{
		"docs/install.md",
		"the namespace has **not** been observed in a live session, and the host may derive it from the entry name instead",
		"observed under the qompack-windows-amd64 entry: the namespace comes from plugin.json's name (D58(f), D59)",
	},
	{
		"docs/install.md", "a copy is pruned only when a newer version is staged",
		"a spawn that writes a copy prunes every other build's copy, even at the same version (D58(f))",
	},
	{
		"docs/release-notes/v0.3.0.md",
		"The command and tool namespace a session shows under a `qompack-<os>-<arch>` entry has not been observed",
		"observed under the qompack-windows-amd64 entry (D59)",
	},
	{
		"CHANGELOG.md", "and the command and tool namespace under a `qompack-<os>-<arch>` entry, have not been observed",
		"observed under the qompack-windows-amd64 entry (D59)",
	},
	{
		"docs/uat.md", "Section 2's first unit is the **verbatim original intent from L0 capture**",
		"section 2 renders the evolution newest first and the original last, whole (D50)",
	},
	{
		"docs/user-guide.md", "the rehydration shows them under the original, newest first",
		"section 2 renders the evolution above the original (D50)",
	},
	{
		"docs/uat.md", "Both are fixed for candidate 8",
		"the fixes are ordered for candidate 8 (D59); a page states a fix only once it is merged",
	},
	{
		"docs/install.md", "which a later spawn removes once it is idle",
		"only a spawn that writes a new copy reaches pruneStaged; a verifying spawn prunes nothing",
	},
	{
		"docs/release-notes/v0.3.0.md",
		"`SessionStart` at startup, resume and fork arrived before the stream's init event and was not measured;",
		"38 of 455 pairs were not measured, 19 of them UserPromptSubmit (rerun-c7/C5.6/summary.md)",
	},
}

// TestCloseoutC7StaleClaimsAreGone asserts none of the sentences candidate 7's lane retired is back.
func TestCloseoutC7StaleClaimsAreGone(t *testing.T) {
	root := repoRoot(t)
	for _, c := range closeoutC7StaleClaims {
		if strings.Contains(normalized(readDoc(t, root, c.page)), normalized(c.claim)) {
			t.Errorf("%s still says %q: %s", c.page, c.claim, c.why)
		}
	}
}

// TestInstallDocumentsPluginDetailsArgument asserts install §9 says `claude plugin details` takes
// the plugin's name or the marketplace-qualified entry, since the bare entry name exits 1 (F2).
func TestInstallDocumentsPluginDetailsArgument(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), "docs/install.md"))
	for _, want := range []string{
		"`claude plugin details qompack-windows-amd64` exits 1",
		"`claude plugin details qompack`",
		"`claude plugin details qompack-windows-amd64@qompack`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs/install.md does not document plugin details' argument: missing %q", want)
		}
	}
}

// TestUATIntroNamesCandidate7 asserts the page's opening names the candidate the eight re-run
// Result blocks now report, and the decision that ruled their outcome.
func TestUATIntroNamesCandidate7(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), uatPath))
	end := strings.Index(body, "## The isolation rule")
	if end < 0 {
		t.Fatalf("%s: the isolation rule heading was not found", uatPath)
	}
	for _, want := range []string{"`d20309c0`", "D59"} {
		if !strings.Contains(body[:end], want) {
			t.Errorf("%s introduction does not name candidate 7's re-run: missing %s", uatPath, want)
		}
	}
}

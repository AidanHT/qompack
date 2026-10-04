package docs

import (
	"fmt"
	"regexp"
	"strconv"
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

// uatIntroCandidateRE matches the introduction's naming of a re-run candidate and its commit.
var uatIntroCandidateRE = regexp.MustCompile("candidate ([0-9]+) \\(commit `([0-9a-f]{8})`\\)")

// uatIntroCandidateFloor is the oldest candidate the introduction may name as its newest re-run:
// candidate 7's re-run (D59) is on this page, so an introduction whose newest candidate is older
// has dropped it. A newer candidate's re-check (D59, D60(f)) raises what the introduction names.
const uatIntroCandidateFloor = 7

// uatIntroRulingFloor is the oldest decision that can have ruled the newest re-run's outcome:
// D59 ruled candidate 7's.
const uatIntroRulingFloor = 59

// uatCandidateCommits are the frozen commits of the candidates this page has reported, from the
// coordinator's records (plans/sdd/V6-closeout/phase3/cN-CANDIDATE.md). A candidate not listed
// here is held to its Result blocks' Snapshot lines instead.
var uatCandidateCommits = map[int]string{3: "d5598eb4", 4: "9f6a2fad", 7: "d20309c0"}

// TestUATIntroNamesItsNewestCandidate asserts the page's opening names the newest candidate whose
// re-run the Result blocks report, by its commit, says those blocks report it, and names the
// decision that ruled the outcome; and that a Result block's Snapshot names that same commit.
func TestUATIntroNamesItsNewestCandidate(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), uatPath))
	end := strings.Index(body, "## The isolation rule")
	if end < 0 {
		t.Fatalf("%s: the isolation rule heading was not found", uatPath)
	}
	intro := body[:end]
	newest, commit := 0, ""
	for _, m := range uatIntroCandidateRE.FindAllStringSubmatch(intro, -1) {
		if n, _ := strconv.Atoi(m[1]); n > newest {
			newest, commit = n, m[2]
		}
	}
	if newest == 0 {
		t.Fatalf("%s introduction names no candidate by its commit (\"candidate N (commit `abcdef12`)\")", uatPath)
	}
	if newest < uatIntroCandidateFloor {
		t.Errorf("%s introduction's newest candidate is %d; candidate %d's re-run is on this page",
			uatPath, newest, uatIntroCandidateFloor)
	}
	if want, ok := uatCandidateCommits[newest]; ok && commit != want {
		t.Errorf("%s introduction names candidate %d as commit %s; its frozen commit is %s",
			uatPath, newest, commit, want)
	}
	if !strings.Contains(intro, fmt.Sprintf("report candidate %d", newest)) {
		t.Errorf("%s introduction does not say which Result blocks report candidate %d", uatPath, newest)
	}
	ruled := 0
	for _, d := range decisionRE.FindAllStringSubmatch(intro, -1) {
		if n, _ := strconv.Atoi(d[1]); n > ruled {
			ruled = n
		}
	}
	if ruled < uatIntroRulingFloor {
		t.Errorf("%s introduction names no decision from D%d on, so nothing rules candidate %d's re-run",
			uatPath, uatIntroRulingFloor, newest)
	}
	if !uatSnapshotNamesCommit(body[end:], commit) {
		t.Errorf("%s: no Result block's Snapshot names commit %s, the introduction's candidate %d",
			uatPath, commit, newest)
	}
}

// uatSnapshotNamesCommit reports whether a Snapshot line in body, normalized text from the Result
// blocks, names commit. A Snapshot runs from "Snapshot:" to the block's next "Date:" line; the
// commit named in a history line, a note or prose elsewhere does not count.
func uatSnapshotNamesCommit(body, commit string) bool {
	for rest := body; ; {
		at := strings.Index(rest, "Snapshot:")
		if at < 0 {
			return false
		}
		rest = rest[at+len("Snapshot:"):]
		snapshot := rest
		if end := strings.Index(rest, "Date:"); end >= 0 {
			snapshot = rest[:end]
		}
		if strings.Contains(snapshot, "commit "+commit) {
			return true
		}
	}
}

// TestUATSnapshotCommitCheckReadsOnlySnapshots is the negative of the Snapshot cross-check: a commit
// named only outside a Snapshot does not satisfy it.
func TestUATSnapshotCommitCheckReadsOnlySnapshots(t *testing.T) {
	const snap = "Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256 5212; commit " +
		"d20309c03ffc; Windows 11 Date: 2026-10-02 "
	if !uatSnapshotNamesCommit(snap, "d20309c0") {
		t.Errorf("a Snapshot naming commit d20309c0 must satisfy the check")
	}
	for name, body := range map[string]string{
		"history line": "Candidate 7 (commit d20309c0): pass. Snapshot: bundle 5212; commit 0d06ab12; Date: x ",
		"note after":   "Snapshot: bundle 5212; commit 0d06ab12; Date: 2026-10-02 Notes: commit d20309c0 ",
		"no snapshot":  "Result: pass on commit d20309c0 Date: 2026-10-02 ",
	} {
		if uatSnapshotNamesCommit(body, "d20309c0") {
			t.Errorf("%s: a commit named outside every Snapshot must not satisfy the check", name)
		}
	}
}

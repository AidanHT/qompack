package docs

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Candidate 8 (decisions D58(e), D60(f) and D61) is what release 0.3.0 tags, and the release notes
// are published verbatim from the tagged tree (release.yml). Candidate 8 changes product code
// (the drain pass budget, the session registry, the checkpoint writer, the hook configuration
// path, the contract reading, the rehydration and the command client), so candidate 7's byte
// comparison with candidate 6 no longer carries any machine evidence to the release: candidate 8's
// own night chain, hosted runs, live re-check and C5.5 do. Candidate 7's hosted ci.yml and nightly
// ran (D58(a)) and its live lane ran (D59). The checks below keep the sentences those facts retired
// from coming back, and keep the release-identity pages agreeing on one candidate.

var closeoutC8StaleClaims = []staleClaim{
	{
		"docs/release-notes/v0.3.0.md", "this release's binaries differ from candidate 6's only by",
		"candidate 8 changes product code; its own night and lanes supply its machine evidence (D58(e), D60(f))",
	},
	{
		"docs/release-notes/v0.3.0.md", "Candidate 6's machine evidence below carries to this release",
		"no byte comparison carries candidate 6's evidence to candidate 8 (D58(e), D60(f))",
	},
	{
		"README.md", "is being cut from release candidate 7",
		"the release tags candidate 8 or a descendant whose changes reach no bundle (D58(e))",
	},
	{
		"docs/release.md", "is being cut from release candidate 7",
		"the release tags candidate 8 or a descendant whose changes reach no bundle (D58(e))",
	},
	{
		"CHANGELOG.md", "Release candidate: release candidate 7",
		"the release tags candidate 8 or a descendant whose changes reach no bundle (D58(e))",
	},
	{
		"README.md", "The live lane and the live evaluation run on candidate 7's frozen bundles and have not run yet",
		"candidate 7's live lane ran (D59); C5.5 runs on candidate 8's frozen bundle (D58(e))",
	},
	{
		"README.md", "Neither job has been re-run on candidate 7 yet",
		"ci.yml 36981590450 and nightly 36981711009 ran on candidate 7 (D58(a))",
	},
	{
		"docs/release.md", "Neither workflow has run on candidate 7 yet",
		"ci.yml 36981590450 and nightly 36981711009 ran on candidate 7 (D58(a))",
	},
	{
		"docs/release.md", "the live lane on candidate 7's frozen bundles (D53(f))",
		"candidate 7's live lane ran (D59); candidate 8's live re-check is what is owed (D59, D60(f))",
	},
	{
		"CHANGELOG.md",
		"Rehydration pointers never show a path the host currently denies, a path outside the project, or a home- or variable-rooted path; they point by hash.",
		"D61(b) judges a structured summary whole and screens free text, with recorded limits (D61(b)(5))",
	},
	{
		"docs/troubleshooting.md", "so nothing older than the newest restatement is added after it",
		"item 2's share still carries the newest entries that fit (7 of 13 in F-C7-UAT04-1)",
	},
	{
		"docs/troubleshooting.md", "(`Tokens` well under `Budget` in",
		"the rehydrate state file's keys are lowercase `tokens` and `budget` (internal/rehydrate/drops.go)",
	},
	{
		"docs/release-notes/v0.3.0.md",
		"the entry's name appears only in the plugin id, `qompack-windows-amd64@<marketplace>`, and the install path",
		"it also appears in the session's plugins[].source (install.md §9, UAT-01)",
	},
	{
		"CHANGELOG.md",
		"(the store's preview of a path argument), is judged whole, as `re_read` judges a path, and points by hash",
		"only a file pointer points by hash; a refused summary is replaced by a \"summary withheld\" note " +
			"(D61(b)(1) rules how a summary is judged, not how it is rendered; rehydrate withheldSummary)",
	},
	{
		"docs/cannot-do.md",
		"are judged whole against the host's saved Read rules, as `re_read` judges a path, and point by hash",
		"only a file pointer points by hash; a refused summary is replaced by a \"summary withheld\" note " +
			"(D61(b)(1); rehydrate withheldSummary)",
	},
	{
		"docs/release-notes/v0.3.0.md", "candidates 6's and 7's",
		"the published notes say candidate 6's and candidate 7's",
	},
	{
		"README.md", "Candidate 7's live lane ran: 20 real sessions on its frozen bundles",
		"UAT-12's upgrade leg ran candidate 5's bundle as the previous build: 19 of the 20 sessions ran " +
			"candidate 7's bundles (release notes, uat.md UAT-12 Snapshot)",
	},
	{
		"README.md", "Its rows carry to candidate 8 by the diff, confirmed by the live re-check",
		"the live re-check on candidate 8 is owed, so it has confirmed nothing yet (D58(e), D60(f))",
	},
	{
		"docs/release.md", "The rehydration block's pointers never show a path the host denies",
		"D61(b)(5): aliases, globs and run-time names in free text are not resolved, so a free-text " +
			"summary can show a denied file by another name; only the saved Read rules are consulted (D7)",
	},
	{
		"docs/release-notes/v0.3.0.md", "The rehydration block shows no path your saved Read rules deny",
		"D61(b)(5): the screen does not resolve aliases or globs in free text or see run-time names, " +
			"so the lead may say what the block withholds, not that no denied path shows",
	},
	{
		"CHANGELOG.md",
		"`doctor` and the other commands that call the daemon have a connect budget of their own",
		"`qompack mcp` also calls the daemon and keeps runtime.daemon.connectDeadlineMs and its " +
			"10-attempt retry loop (D61(c)); only the slash-command frontends get the command budget",
	},
}

// TestCloseoutC8StaleClaimsAreGone asserts none of the sentences candidate 8's identity retired is
// back.
func TestCloseoutC8StaleClaimsAreGone(t *testing.T) {
	root := repoRoot(t)
	for _, c := range closeoutC8StaleClaims {
		if strings.Contains(normalized(readDoc(t, root, c.page)), normalized(c.claim)) {
			t.Errorf("%s still says %q: %s", c.page, c.claim, c.why)
		}
	}
}

// releaseCandidateFloor is the lowest candidate a release-identity page may name: D58(e) says the
// release tags candidate 8 or a descendant whose changes reach no bundle, so a page naming an
// earlier candidate's record describes binaries the release does not ship.
const releaseCandidateFloor = 8

// candidateRecordRE matches the coordinator's per-candidate record, written at each freeze.
var candidateRecordRE = regexp.MustCompile("`plans/sdd/V6-closeout/phase3/c([0-9]+)-CANDIDATE\\.md`")

// TestReleasePagesNameOneCandidateRecord asserts the three release-identity pages name the same
// candidate's record, and that it is not older than the candidate the release tags.
func TestReleasePagesNameOneCandidateRecord(t *testing.T) {
	root := repoRoot(t)
	named := map[string]int{}
	for _, page := range []string{"README.md", "CHANGELOG.md", "docs/release.md"} {
		ms := candidateRecordRE.FindAllStringSubmatch(normalized(readDoc(t, root, page)), -1)
		if len(ms) == 0 {
			t.Errorf("%s names no plans/sdd/V6-closeout/phase3/cN-CANDIDATE.md record", page)
			continue
		}
		seen := map[int]bool{}
		for _, m := range ms {
			n, _ := strconv.Atoi(m[1])
			seen[n] = true
			named[page] = n
		}
		if len(seen) != 1 {
			t.Errorf("%s names more than one candidate record (%v); the release has one candidate", page, seen)
		}
		if named[page] < releaseCandidateFloor {
			t.Errorf("%s names candidate %d's record; the release tags candidate %d or a descendant (D58(e))",
				page, named[page], releaseCandidateFloor)
		}
	}
	var first string
	for page, n := range named {
		if first == "" {
			first = page
			continue
		}
		if n != named[first] {
			t.Errorf("%s names candidate %d but %s names candidate %d", page, n, first, named[first])
		}
	}
}

// decisionRE matches a close-out decision number, D1 and up, as the docs cite it.
var decisionRE = regexp.MustCompile(`\bD([0-9]+)\b`)

// decisionRangeRE matches the capability table's statement of the ledger range it was written from.
var decisionRangeRE = regexp.MustCompile(`owner decisions D1 to D([0-9]+)\b`)

// TestCapabilityTableDecisionRangeCoversItsRows asserts docs/release.md's capability table says it
// was written from a ledger range that includes every decision its rows cite.
func TestCapabilityTableDecisionRangeCoversItsRows(t *testing.T) {
	body := readDoc(t, repoRoot(t), "docs/release.md")
	start := strings.Index(body, "## Capability status at 0.3.0")
	end := strings.Index(body, "## 4. Switches")
	if start < 0 || end < start {
		t.Fatalf("docs/release.md: the capability section or the section after it was not found")
	}
	section := body[start:end]
	m := decisionRangeRE.FindStringSubmatch(normalized(section))
	if m == nil {
		t.Fatalf("docs/release.md: the capability table does not say which decisions it was written from")
	}
	upTo, _ := strconv.Atoi(m[1])
	for _, d := range decisionRE.FindAllStringSubmatch(section, -1) {
		if n, _ := strconv.Atoi(d[1]); n > upTo {
			t.Errorf("docs/release.md: the capability table cites D%d but says it was written from D1 to D%d", n, upTo)
		}
	}
}

// TestReleasePagesListTheCandidate8Limits asserts the release notes' known limits and the
// capability table carry two documented limits: backup refusing while a newer settingsVersion is
// in force (D59), and nothing injected below the smallest loss notice (D59(b), D60(c)(ii)).
func TestReleasePagesListTheCandidate8Limits(t *testing.T) {
	root := repoRoot(t)
	notes := normalized(readDoc(t, root, "docs/release-notes/v0.3.0.md"))
	release := normalized(readDoc(t, root, "docs/release.md"))
	for _, page := range []struct{ name, body string }{
		{"docs/release-notes/v0.3.0.md", notes},
		{"docs/release.md", release},
	} {
		for _, want := range []string{
			"while a newer `settingsVersion` is in force",
			"below the smallest loss notice, nothing is injected",
		} {
			if !strings.Contains(strings.ToLower(page.body), strings.ToLower(want)) {
				t.Errorf("%s does not list the limit %q", page.name, want)
			}
		}
	}
}

// TestUATOpenQuestionsNameTheirRuling asserts the two candidate 7 Result blocks that left a
// question to the coordinator now name the decision that answered it, beside the unchanged
// candidate 7 verdict.
func TestUATOpenQuestionsNameTheirRuling(t *testing.T) {
	raw, secs := uatSections(t)
	for _, c := range []struct{ id, question, ruling string }{
		{"UAT-05", "decides which document is right", "D59(b)"},
		{"UAT-12", "is left to the coordinator", "D60(c)(i)"},
	} {
		found := false
		for _, s := range secs {
			if s.id != c.id {
				continue
			}
			found = true
			block := normalized(strings.Join(raw[s.start:s.end], "\n"))
			if !strings.Contains(block, c.question) {
				continue // the question is gone, so there is nothing left open to rule on
			}
			if !strings.Contains(block, "Ruling ("+c.ruling) {
				t.Errorf("%s: %s leaves %q open but has no `Ruling (%s` line", uatPath, c.id, c.question, c.ruling)
			}
		}
		if !found {
			t.Errorf("%s: no %s section", uatPath, c.id)
		}
	}
}

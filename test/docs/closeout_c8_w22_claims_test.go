package docs

import (
	"regexp"
	"strings"
	"testing"
)

// Audit 2 (decision D67) found the release pages still describing decision D61(b)(2)'s free-text
// screen, which withheld a summary only when it held a rule's literal, a withheld name or an outside
// path. D63 replaced it with a whitelist and D64 tightened it: a free-text summary is shown only when
// the whitelist proves it safe, and variables, globs, regular expressions, `%` escapes and `name:`
// shapes are withheld by design (D64(4)). The checks below keep D61's wording off the pages, keep the
// pages describing what the code runs, and pin the other audit 2 corrections to the release pages.

var closeoutW22StaleClaims = []staleClaim{
	{
		"CHANGELOG.md", "is screened: it is withheld when it contains a Read deny or ask rule's literal",
		"D63 replaced D61(b)(2)'s screen: free text is shown only when the whitelist proves it safe",
	},
	{
		"docs/cannot-do.md", "decoded and normalized",
		"the D63 whitelist percent-decodes nothing; a `%` before two hex digits withholds (D63, D64)",
	},
	{
		"docs/cannot-do.md", "A free-text summary is withheld when",
		"D63: a free-text summary is shown only when the whitelist proves it safe",
	},
	{
		"docs/release.md", "free-text summaries are withheld when they contain a Read deny or ask rule's literal",
		"D63: a free-text summary is shown only when the whitelist proves it safe",
	},
	{
		"docs/release-notes/v0.3.0.md", "free-text summaries (a command line, a query) are screened",
		"D63: a free-text summary is shown only when the whitelist proves it safe",
	},
	{
		"docs/uat.md", "and free text is screened",
		"D63 replaced the free-text screen D61(b) ruled with a whitelist",
	},
	{
		"docs/uat.md", "`Tokens` and `Budget` in the state file",
		"the rehydrate state file's keys are lowercase `tokens` and `budget` (internal/rehydrate/drops.go)",
	},
	{
		"docs/uat.md", "SessionEnd never reached the daemon",
		"D62 (sessionend): UAT-09 O-1 is by design; SessionEnd was delivered and ended the session",
	},
	{
		"CHANGELOG.md", "a glob that selects only files the block never recorded is judged as written",
		"free text withholds every glob (D67(l)); the unrecorded-glob limit is a structured glob's (D60(c)(iv))",
	},
	{
		"docs/cannot-do.md", "judges a glob that selects only files the block never recorded",
		"free text withholds every glob (D67(l)); the unrecorded-glob limit is a structured glob's (D60(c)(iv))",
	},
	{
		"docs/adr/0010-wall-clock-under-coload.md", "pending the owner's ruling, C7.2",
		"D57(a) records release.yml's declaration of QOMPACK_NONREFERENCE_DISK",
	},
}

// TestCloseoutW22StaleClaimsAreGone asserts none of the sentences audit 2 retired is back.
func TestCloseoutW22StaleClaimsAreGone(t *testing.T) {
	root := repoRoot(t)
	for _, c := range closeoutW22StaleClaims {
		if strings.Contains(normalized(readDoc(t, root, c.page)), normalized(c.claim)) {
			t.Errorf("%s still says %q: %s", c.page, c.claim, c.why)
		}
	}
}

// whitelistPages are the release pages that describe the rehydration block's free-text screen.
var whitelistPages = []string{
	"CHANGELOG.md",
	"docs/release-notes/v0.3.0.md",
	"docs/release.md",
	"docs/cannot-do.md",
}

// TestReleasePagesDescribeTheD63Whitelist asserts each release page that describes the free-text
// screen says what the code runs: the whitelist, both decisions, the accepted over-withholding of
// D64(4) by its examples, the root-unit rule of D64(1), the whitespace-collapse limit (D60(c)(iv))
// and that a path-named value holding several paths is judged piece by piece.
func TestReleasePagesDescribeTheD63Whitelist(t *testing.T) {
	root := repoRoot(t)
	for _, page := range whitelistPages {
		body := normalized(readDoc(t, root, page))
		for _, want := range []string{
			"D63", "D64", "whitelist", "shown only when",
			"`localhost:3000`", "`format:%h`", "variable", "glob", "regular expression", "`%`",
			"root unit", "collapses runs of whitespace", "piece by piece",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s describes the free-text screen without %q (D63, D64, D67(l))", page, want)
			}
		}
	}
}

// unrecordedGlobLimitRe matches the D60(c)(iv) limit stated as ADR 0011 §23 and docs/security.md
// state it: it belongs to a structured glob, and its point is that the glob reaches a refused file
// without spelling the rule's literal.
var unrecordedGlobLimitRe = regexp.MustCompile(`structured glob[^;]{0,80}? that selects a refused file ` +
	`the block never recorded, without spelling its literal[^;]{0,40}? is judged as written \(D60\(c\)\(iv\)\)`)

// bareGlobLimitRe matches the limit paraphrased without its subject: "a glob that selects only
// files ...", which in a paragraph about free text contradicts D67(l)'s "every glob is withheld".
var bareGlobLimitRe = regexp.MustCompile(`(?i)\bglob that selects only files`)

// TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs asserts every release page that
// describes the free-text whitelist states D60(c)(iv)'s unrecorded-glob limit, and states it of a
// structured glob (a lone Glob or recall pattern), never of the whitelist: in free text every glob is
// withheld (D67(l)).
func TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs(t *testing.T) {
	root := repoRoot(t)
	for _, page := range append(append([]string{}, whitelistPages...), "docs/uat.md", "README.md") {
		body := normalized(readDoc(t, root, page))
		if loc := bareGlobLimitRe.FindStringIndex(body); loc != nil {
			t.Errorf("%s states the unrecorded-glob limit without its structured subject: %q",
				page, body[max(0, loc[0]-80):min(len(body), loc[1]+80)])
		}
	}
	for _, page := range whitelistPages {
		if !unrecordedGlobLimitRe.MatchString(normalized(readDoc(t, root, page))) {
			t.Errorf("%s does not state that a structured glob selecting a refused file the block never "+
				"recorded, without spelling its literal, is judged as written (D60(c)(iv))", page)
		}
	}
}

// section2Re matches a sentence that puts section 2 of the rehydration block outside D50.
var section2Re = regexp.MustCompile(`(?i)sections 2 to 4|section 2's verbatim intent|section 2 \(your own prompts\)`)

// TestSection2IsRuledByD62f asserts every block of a release page that puts section 2 of the
// rehydration block outside D50 cites D62(f), the decision that ruled it: D60(c)(i) covers only
// sections 3 and 4.
func TestSection2IsRuledByD62f(t *testing.T) {
	root := repoRoot(t)
	for _, page := range append(append([]string{}, whitelistPages...), "docs/uat.md") {
		for _, block := range docBlocks(readDoc(t, root, page)) {
			if section2Re.MatchString(block) && !strings.Contains(block, "D62(f)") {
				t.Errorf("%s puts section 2 outside D50 without citing D62(f): %.160q", page, block)
			}
		}
	}
}

// docBlocks splits a page into paragraphs, list items and table rows, each normalized.
func docBlocks(body string) []string {
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, normalized(strings.Join(cur, " ")))
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, "#"):
			flush()
			cur = append(cur, trimmed)
		default:
			cur = append(cur, trimmed)
		}
	}
	flush()
	return blocks
}

// TestOwedEvidenceListsTheC52NightAndC116 asserts the three pages that list the evidence owed
// before the tag name the C5.2 night (D62(b), D65(b)) and the C1.16 re-measure (D62(c), D65(a)), and
// that docs/release.md's step 2 names docs/architecture.md's C1.16 paragraph among the pages it
// rewrites.
func TestOwedEvidenceListsTheC52NightAndC116(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []string{"README.md", "docs/release.md", "docs/release-notes/v0.3.0.md"} {
		body := normalized(readDoc(t, root, page))
		for _, want := range []string{"C5.2", "D62(b)", "D65(b)", "C1.16", "D65(a)"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lists the evidence owed before the tag without %q", page, want)
			}
		}
	}
	release := normalized(readDoc(t, root, "docs/release.md"))
	start := strings.Index(release, "2. **Fill `CHANGELOG.md`'s `[Unreleased]` section**")
	end := strings.Index(release, "3. **Run the gate locally")
	if start < 0 || end < start {
		t.Fatalf("docs/release.md: §1 step 2 or step 3 was not found")
	}
	if !strings.Contains(release[start:end], "`docs/architecture.md`") {
		t.Errorf("docs/release.md §1 step 2 does not name docs/architecture.md's C1.16 paragraph")
	}
}

// TestReleasePagesHaveAKnownIssuesSection asserts the release notes and the CHANGELOG carry the
// known-issues section D66(d) and D67(o) fill with the minors candidate 8 does not close.
func TestReleasePagesHaveAKnownIssuesSection(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct{ page, heading string }{
		{"docs/release-notes/v0.3.0.md", "\n## Known issues\n"},
		{"CHANGELOG.md", "\n### Known issues\n"},
	} {
		body := readDoc(t, root, c.page)
		if !strings.Contains(body, c.heading) || !strings.Contains(body, "D66(d)") {
			t.Errorf("%s has no %q heading citing D66(d)", c.page, strings.TrimSpace(c.heading))
		}
	}
}

// TestReleasePagesListTheAudit2Limits asserts the release notes, the capability table and the
// CHANGELOG list the limits audit 2 found missing: a quiet live session ended as abandoned until its
// next hook (D62), a duplicate daemon a missed connect can spawn (D61(c)), the recorded-corpus tier
// not exercised (D67(g)), macOS's case-insensitive assumption (D67(m)), and another session's tool
// use closing the bound session's segment (D67(b)).
func TestReleasePagesListTheAudit2Limits(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []string{"docs/release-notes/v0.3.0.md", "docs/release.md", "CHANGELOG.md"} {
		body := strings.ToLower(normalized(readDoc(t, root, page)))
		for _, want := range []string{
			"counted as ended until its next hook",
			"a second daemon",
			"recorded-corpus tier",
			"case-insensitive",
			"another session's tool use",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not list the limit %q", page, want)
			}
		}
	}
	if !strings.Contains(normalized(readDoc(t, root, "docs/cannot-do.md")), "D67(b)") {
		t.Errorf("docs/cannot-do.md does not list D67(b)'s cross-session segment close")
	}
}

// TestChangelogNamesTheWave20Fixes asserts CHANGELOG.md's Fixed section carries the user-visible
// fixes waves 19b to 21 merged (audit 2 #44 and #70).
func TestChangelogNamesTheWave20Fixes(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), "CHANGELOG.md"))
	start := strings.Index(body, "### Fixed")
	end := strings.Index(body, "### Security")
	if start < 0 || end < start {
		t.Fatalf("CHANGELOG.md: the Fixed or Security section was not found")
	}
	fixed := body[start:end]
	for _, want := range []string{
		"`state/config-violations.json` is written only when it changes",
		"names a disabled daemon",
		"`fsck` lists its detail lines in one order",
		"skips slash-command invocations",
		"never moves backwards",
		"a redelivered delivery",
		"a drain pass over blocked spools",
	} {
		if !strings.Contains(fixed, want) {
			t.Errorf("CHANGELOG.md's Fixed section does not say %q", want)
		}
	}
}

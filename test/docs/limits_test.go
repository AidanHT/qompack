package docs

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file checks the two pages SP-18 Commit 5 owns: docs/cannot-do.md (the limits page) and
// docs/upstream-issues.md (proposals prepared, none filed).
//
// The checks here stay mechanical, in the spirit of doc.go: they verify that the limits page still
// names every noun Qompack.md v1.5 §12 refuses, that the proposals page still says nothing was
// filed, and that the host contracts this build cannot assert are named SOMEWHERE on the two pages.
// Whether the prose around those nouns is true is the reviewer's job, not this package's.

// section12Nouns are the limits Qompack.md v1.5 §12 "What this plugin cannot do" states, in its own
// words. They are quoted from the plan rather than paraphrased so that a future §12 revision which
// respells one of them shows up here as a failure instead of leaving the page quietly describing a
// limit the binding source no longer states.
var section12Nouns = []string{
	"native-history cuts",
	"marker control",
	"deletion of already-delivered results",
	"summarizer model substitution",
	"exact native loaded bytes",
	"model compliance",
	"universal savings",
	"unobserved child work",
	"compaction request/veto",
	"reconstruct uncaptured history",
	"cache state from arbitrary elapsed time",
	"denied-read bypass",
}

// readDoc returns a page's full text with carriage returns removed.
func readDoc(t *testing.T, root, rel string) string {
	t.Helper()
	return strings.Join(readLines(t, filepath.Join(root, filepath.FromSlash(rel))), "\n")
}

// TestCannotDoCoversSection12Limits asserts docs/cannot-do.md states every limit §12 states. A
// limits page that dropped one would read as though the limit had been lifted.
func TestCannotDoCoversSection12Limits(t *testing.T) {
	body := readDoc(t, repoRoot(t), "docs/cannot-do.md")
	for _, noun := range section12Nouns {
		if !strings.Contains(body, noun) {
			t.Errorf("docs/cannot-do.md: does not state %q (Qompack.md v1.5 §12)", noun)
		}
	}
}

// issueURLRe matches a GitHub issue URL. Its presence on the proposals page would mean a proposal
// became a real issue; see TestUpstreamIssuesAreAllUnfiled.
var issueURLRe = regexp.MustCompile(`https://github\.com/\S*/issues/`)

// TestUpstreamIssuesAreAllUnfiled pins the one fact that makes docs/upstream-issues.md safe to
// publish: it is prepared text, and nothing on it has been sent anywhere. Every proposal section
// must carry its own "Status: not filed" — a page-level disclaimer would not survive someone
// appending a section — and the page must carry no issue URL at all.
//
// This test is meant to be flipped deliberately. The day a proposal is actually filed under
// separate authorization, its author changes that section's status, records the URL beside it, and
// changes this test with the artifact in hand. It must never be relaxed in advance.
func TestUpstreamIssuesAreAllUnfiled(t *testing.T) {
	root := repoRoot(t)
	rel := "docs/upstream-issues.md"
	lines := readLines(t, filepath.Join(root, filepath.FromSlash(rel)))

	if m := issueURLRe.FindString(strings.Join(lines, "\n")); m != "" {
		t.Errorf("%s: carries an issue URL (%s): a filed issue needs this test changed deliberately,"+
			" with the artifact", rel, m)
	}

	// Each level-2 heading opens a proposal section; the section runs to the next level-2 heading
	// or to the end of the page. The header material before the first one is not a proposal.
	type section struct {
		title string
		line  int
		body  []string
	}
	var sections []section
	for i, l := range lines {
		text, level, ok := headingText(l)
		if ok && level == 2 {
			sections = append(sections, section{title: text, line: i + 1})
			continue
		}
		if len(sections) > 0 {
			s := &sections[len(sections)-1]
			s.body = append(s.body, l)
		}
	}
	if len(sections) == 0 {
		t.Fatalf("%s: no level-2 proposal sections found: the scan is looking in the wrong place", rel)
	}
	for _, s := range sections {
		if !strings.Contains(strings.Join(s.body, "\n"), "Status: not filed") {
			t.Errorf("%s:%d: section %q does not say \"Status: not filed\"", rel, s.line, s.title)
		}
	}
}

// gatedRe matches one row of contract.StandardAssertions: gated(CSomeName, …).
var gatedRe = regexp.MustCompile(`gated\(\s*(C[A-Za-z0-9_]+)\s*,`)

// idConstRe matches one contract ID constant declaration: CSomeName ID = "some.name".
var idConstRe = regexp.MustCompile(`(C[A-Za-z0-9_]+)\s+ID\s*=\s*"([^"]+)"`)

// declareRe matches one producer declaration inside daemon.DeclareProducers.
var declareRe = regexp.MustCompile(`contract\.DeclareProducer\(\s*contract\.(C[A-Za-z0-9_]+)\s*\)`)

// TestCannotDoNamesTheUnimplementedChecks derives — never hard-codes — the host contracts this
// build cannot assert, and requires the documentation to name each one.
//
// The derivation follows the same path `qompack self-test` does. selfTestContractAssertions
// (internal/cli/selftest.go) declares producers against a ZERO daemon.Services and then runs
// contract.StandardAssertions; contract's gated wrapper (internal/contract/assertions.go) reports
// Observed "not-yet-implemented" for any assertion whose producer was not declared. So the
// not-yet-implemented set is every standard assertion minus those daemon.DeclareProducers declares
// UNCONDITIONALLY — the conditional declarations sit behind nil checks on Services fields a zero
// Services leaves nil.
//
// The premises of that walk are asserted, not assumed: if self-test stops passing a zero Services,
// or stops going through StandardAssertions, this test fails loudly rather than deriving a stale
// list from source it no longer describes.
func TestCannotDoNamesTheUnimplementedChecks(t *testing.T) {
	root := repoRoot(t)

	selftest := readDoc(t, root, "internal/cli/selftest.go")
	for _, premise := range []string{
		"daemon.DeclareProducers(&daemon.Services{})",
		"contract.StandardAssertions()",
	} {
		if !strings.Contains(selftest, premise) {
			t.Fatalf("internal/cli/selftest.go: no longer contains %q; this test derives the "+
				"not-yet-implemented rows from that call and must be rewritten against the new shape",
				premise)
		}
	}

	// Every standard assertion, by constant name, in declaration order.
	standard := readDoc(t, root, "internal/contract/standard.go")
	var all []string
	for _, m := range gatedRe.FindAllStringSubmatch(standard, -1) {
		all = append(all, m[1])
	}
	if len(all) == 0 {
		t.Fatal("internal/contract/standard.go: no gated(C…) rows found: the scan is looking in the wrong place")
	}

	// The constant name -> on-disk ID string.
	ids := map[string]string{}
	for _, m := range idConstRe.FindAllStringSubmatch(readDoc(t, root, "internal/contract/ids.go"), -1) {
		ids[m[1]] = m[2]
	}

	// The producers a zero Services declares: the DeclareProducer calls at the top level of
	// DeclareProducers' body. Anything nested inside an `if` is conditional on a Services field
	// that a zero Services leaves nil, so it is NOT declared on self-test's synthetic run.
	declared := map[string]bool{}
	body, ok := funcBody(readDoc(t, root, "internal/daemon/options.go"), "func DeclareProducers(s *Services) {")
	if !ok {
		t.Fatal("internal/daemon/options.go: DeclareProducers not found: the scan is looking in the wrong place")
	}
	depth := 0
	for _, line := range strings.Split(body, "\n") {
		if depth == 0 {
			if m := declareRe.FindStringSubmatch(line); m != nil {
				declared[m[1]] = true
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
	}
	if len(declared) == 0 {
		t.Fatal("internal/daemon/options.go: no unconditional DeclareProducer calls found")
	}

	var want []string
	for _, name := range all {
		if declared[name] {
			continue
		}
		id, ok := ids[name]
		if !ok {
			t.Errorf("internal/contract/ids.go: no ID constant for %s", name)
			continue
		}
		want = append(want, id)
	}
	if len(want) == 0 {
		t.Fatal("derived an empty not-yet-implemented set: either every producer is now declared " +
			"unconditionally (in which case the documentation claim is obsolete) or the derivation broke")
	}

	pages := readDoc(t, root, "docs/cannot-do.md") + "\n" + readDoc(t, root, "docs/upstream-issues.md")
	for _, id := range want {
		if !strings.Contains(pages, id) {
			t.Errorf("neither docs/cannot-do.md nor docs/upstream-issues.md names %q, which self-test "+
				"reports as not-yet-implemented on this tree", id)
		}
	}
}

// funcBody returns the text between the brace that opens decl and its matching close brace.
func funcBody(src, decl string) (string, bool) {
	i := strings.Index(src, decl)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(decl):]
	depth := 1
	for j, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:j], true
			}
		}
	}
	return "", false
}

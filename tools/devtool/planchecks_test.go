package main

import (
	"strings"
	"testing"
)

// bt is a backtick. The documents under test are markdown, so nearly every fixture here contains
// code spans, and a Go raw string cannot hold one.
const bt = "\x60"

func TestParsePlanRunPatterns_ExtractsPatternAndPackage(t *testing.T) {
	doc := "Run " + bt + "go test -run TestNightlyFuzzMatrix ./test/guards/" + bt + " before merging.\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("want 1 pattern, got %d: %+v", len(got), got)
	}
	if got[0].pattern != "TestNightlyFuzzMatrix" {
		t.Errorf("pattern = %q, want TestNightlyFuzzMatrix", got[0].pattern)
	}
	if got[0].pkg != "./test/guards" {
		t.Errorf("pkg = %q, want ./test/guards", got[0].pkg)
	}
	if got[0].line != 1 {
		t.Errorf("line = %d, want 1", got[0].line)
	}
}

func TestParsePlanRunPatterns_FindsPackageBeforeRunFlag(t *testing.T) {
	doc := bt + "go test ./tools/devtool/ -run TestLandedSubplans -v" + bt + "\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 || got[0].pkg != "./tools/devtool" || got[0].pattern != "TestLandedSubplans" {
		t.Fatalf("got %+v", got)
	}
}

// A pipe inside a markdown table cell MUST be escaped or it ends the cell, and the rendered document
// a reader copies from shows a bare pipe. So the escape is correct there and the pattern that gets
// checked is the unescaped one.
func TestParsePlanRunPatterns_UnescapesTableRowPipes(t *testing.T) {
	doc := "| row | " + bt + "go test -run 'TestAlpha\\|TestBeta' ./internal/cli/" + bt + " | PASS |\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("want 1 pattern, got %d: %+v", len(got), got)
	}
	if !got[0].inTable {
		t.Error("inTable = false, want true for a line starting with |")
	}
	if got[0].pattern != "TestAlpha|TestBeta" {
		t.Errorf("pattern = %q, want the unescaped alternation TestAlpha|TestBeta", got[0].pattern)
	}
	if got[0].pkg != "./internal/cli" {
		t.Errorf("pkg = %q, want ./internal/cli", got[0].pkg)
	}
}

// Outside a table there is nothing to escape for, and RE2 reads the backslash as making the pipe
// literal — so the pattern selects the single test named "TestAlpha|TestBeta", which is no test at
// all, and `go test` exits 0. This is silently-disabled-gate finding #9.
func TestParsePlanRunPatterns_FlagsEscapedPipeOutsideTable(t *testing.T) {
	doc := "Then run " + bt + "go test -run 'TestAlpha\\|TestBeta' ./internal/cli/" + bt + ".\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("want 1 pattern, got %d: %+v", len(got), got)
	}
	if !got[0].escaped {
		t.Error("escaped = false, want true")
	}
	if got[0].inTable {
		t.Error("inTable = true, want false")
	}
}

func TestParsePlanRunPatterns_SkipsShellVariables(t *testing.T) {
	doc := bt + "for p in ipc store; do go test -run TestX ./internal/$p; done" + bt + "\n"
	if got := parsePlanRunPatterns("plans/X.md", doc); len(got) != 1 || got[0].pkg != "" {
		// The pattern is still literal and worth parsing; the PACKAGE is a loop variable and
		// must not be guessed at, which leaves it unresolvable and therefore unchecked.
		t.Fatalf("want the package left empty for a loop variable, got %+v", got)
	}
}

// A fenced block is where the plans keep the commands a reader is most likely to run verbatim, so
// exempting fences would leave this check green while ignoring most of its subject matter — which
// is the silently-disabled-gate shape it exists to prevent. Markers are exempt inside fences;
// commands are not.
func TestParsePlanRunPatterns_IncludesFencedCommands(t *testing.T) {
	doc := "```sh\ngo test -run TestInsideAFence ./internal/cli/\n```\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("want fenced commands parsed, got %d: %+v", len(got), got)
	}
	if got[0].pattern != "TestInsideAFence" || got[0].line != 2 {
		t.Errorf("got %+v", got[0])
	}
	if got[0].inTable {
		t.Error("inTable = true inside a fence; a fence renders verbatim so \\| there is a real bug")
	}
}

// A document has to be able to WRITE DOWN a broken command — that is how a defect gets reported.
// The waiver is the recorded-decision form of that: mandatory reason, printed every run.
func TestParsePlanRunPatterns_InlineWaiverCoversItsOwnLine(t *testing.T) {
	doc := "The row ran " + bt + "go test -run TestImports ./internal/eval/" + bt +
		" and matched nothing. <!-- runpatterns: quotes the defect this finding is about -->\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("want 1 pattern, got %d", len(got))
	}
	if got[0].waiver != "quotes the defect this finding is about" {
		t.Errorf("waiver = %q", got[0].waiver)
	}
}

// A waiver on the line before a fence covers the whole block, so a shell transcript can be waived
// from the prose above it rather than having a comment wedged into the middle of the transcript.
func TestParsePlanRunPatterns_WaiverBeforeFenceCoversTheBlock(t *testing.T) {
	doc := "<!-- runpatterns: transcript demonstrating the unsatisfiable command -->\n" +
		"```\n$ go test -run TestImports ./internal/eval/\nok  [no tests to run]\n```\n" +
		"go test -run TestUnwaived ./internal/eval/\n"
	got := parsePlanRunPatterns("plans/X.md", doc)
	if len(got) != 2 {
		t.Fatalf("want 2 patterns, got %d: %+v", len(got), got)
	}
	if got[0].waiver == "" {
		t.Error("the fenced transcript must inherit the waiver above it")
	}
	if got[1].waiver != "" {
		t.Errorf("the waiver must not leak past the closing fence, got %q", got[1].waiver)
	}
}

func TestMatchesNoTest(t *testing.T) {
	names := []string{"TestAlpha", "TestBeta", "BenchmarkGamma", "FuzzDelta"}
	cases := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"exact hit", "TestAlpha", false},
		{"prefix hit, unanchored", "TestAlph", false},
		{"alternation hits", "TestAlpha|TestNope", false},
		{"benchmark hit", "BenchmarkGamma", false},
		{"fuzz hit", "FuzzDelta", false},
		{"subtest head hits", "TestAlpha/sub_case", false},
		{"renamed away", "TestPropIdempotence_", true},
		{"literal pipe from a bad escape", "TestAlpha\\|TestBeta", true},
		{"subtest head misses", "TestGone/sub", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchesNoTest(tc.pattern, names)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("matchesNoTest(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

// The two placeholders that survived into the committed, merged and tagged V2 report. The assembly
// script's own assertion used <[A-Z][A-Z0-9-]*>, which requires the closing bracket immediately
// after the capitals and so could not see either one.
func TestFindPlanMarkers_CatchesTheReportSurvivors(t *testing.T) {
	doc := "**Cross-branch collisions — resolution log.** <COLLISION-LOG — one line per §2.0a row>\n" +
		"**Inbound items — disposition.** <INBOUND-DISPOSITIONS — §2.3a, §2.2a>\n"
	got := findPlanMarkers("plans/V2-report.md", doc)
	if len(got) != 2 {
		t.Fatalf("want both survivors found, got %d: %+v", len(got), got)
	}
	if got[0].line != 1 || !strings.HasPrefix(got[0].token, "<COLLISION-LOG") {
		t.Errorf("first marker = %+v", got[0])
	}
	if got[1].line != 2 || !strings.HasPrefix(got[1].token, "<INBOUND-DISPOSITIONS") {
		t.Errorf("second marker = %+v", got[1])
	}
}

func TestFindPlanMarkers_CatchesBareHyphenatedMarkers(t *testing.T) {
	doc := "final ci-local cover step: <CILOCAL-VERDICT>\nquiet run: <QUIET-BASH100KB> and <QUIET-D5>\n"
	if got := findPlanMarkers("plans/X.md", doc); len(got) != 3 {
		t.Fatalf("want 3 markers, got %d: %+v", len(got), got)
	}
}

func TestFindPlanMarkers_AllowsEstablishedNotation(t *testing.T) {
	doc := "The key is <KEY>, the id <ID>, the address <EMAIL>, the count <N> of <NN> at <ISO-8601>.\n" +
		"Subplan <SP-NN> writes <REDACTED> after <ISO 8601 timestamp> in paragraph <P 12>.\n"
	if got := findPlanMarkers("plans/X.md", doc); len(got) != 0 {
		t.Fatalf("notation must not be flagged, got %+v", got)
	}
}

// CamelCase metavariables stand for a value the reader supplies. The plans are full of them and
// none is a forgotten TODO, so the head has to be genuinely all-caps before anything is flagged.
func TestFindPlanMarkers_AllowsMixedCaseMetavariables(t *testing.T) {
	doc := "Write <TempDir>/<Filename(seq)> at <RFC3339.mmm> for <ExactTestName> and <ToolDisplay>.\n" +
		"See <Symbol>, <Name>, <Description>, <Tool>, <B-x> and <Qompack.md sections>.\n"
	if got := findPlanMarkers("plans/X.md", doc); len(got) != 0 {
		t.Fatalf("metavariables must not be flagged, got %+v", got)
	}
}

func TestFindPlanMarkers_ExemptsCodeFences(t *testing.T) {
	doc := "```json\n{\"token\": \"<YOUR-API-TOKEN>\"}\n```\n"
	if got := findPlanMarkers("plans/X.md", doc); len(got) != 0 {
		t.Fatalf("fenced samples must not be flagged, got %+v", got)
	}
}

// Scope is derived from landedSubplans and the plan filenames, never hand-maintained. A wave whose
// subplans have all landed is checkable; a wave that has not landed names tests nobody has written.
func TestPlanDocsInScope_TracksLandedSubplans(t *testing.T) {
	files := []string{
		"plans/00-ARCHITECTURE.md",
		"plans/V1-SP-01-foundation-toolchain-and-contracts.md",
		"plans/V2-SP-06-content-addressed-store.md",
		"plans/V2-VERIFY-primitives-store-dag-and-baseline.md",
		"plans/V2-report.md",
		"plans/V3-SP-08-observer-l0.md",
		"plans/V3-VERIFY-observer-and-negative-knowledge.md",
		"plans/V4-SP-10-checkpointer-l4.md",
	}
	scope := planDocsInScope(files)

	for _, f := range files[:5] {
		if !scope[f] {
			t.Errorf("%s: want in scope (its wave has landed)", f)
		}
	}
	for _, f := range files[5:] {
		if scope[f] {
			t.Errorf("%s: want out of scope (SP-08 and later have not landed)", f)
		}
	}
}

// The scope rule must react to landedSubplans rather than to a second list somebody has to remember
// to update. Landing SP-08 and SP-09 has to bring the V3 documents into scope on its own.
func TestPlanDocsInScope_FollowsLandedSubplansWithoutASecondList(t *testing.T) {
	files := []string{
		"plans/V3-SP-08-observer-l0.md",
		"plans/V3-SP-09-negative-knowledge.md",
		"plans/V3-VERIFY-observer-and-negative-knowledge.md",
	}
	if scope := planDocsInScope(files); scope[files[2]] {
		t.Fatal("V3-VERIFY must be out of scope while SP-08/SP-09 are unlanded")
	}

	for _, sp := range []string{"SP-08", "SP-09"} {
		landedSubplans[sp] = true
		defer delete(landedSubplans, sp)
	}
	scope := planDocsInScope(files)
	for _, f := range files {
		if !scope[f] {
			t.Errorf("%s: want in scope once SP-08 and SP-09 land", f)
		}
	}
}

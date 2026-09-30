package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The candidate 4 live re-run (plans/sdd/V6-closeout/live/rerun-c4/) found documentation that
// contradicted the shipped code, and the close-out's decision D49 fixes each one before 0.3.0. The
// checks below keep those specific claims from coming back. Each names the decision that settled
// the fact, so a later change of the fact changes the decision and this table together.

// normalized collapses every run of whitespace to one space, so a claim is found however the page
// wraps it.
func normalized(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// staleClaim is one sentence a page must no longer make.
type staleClaim struct {
	page  string
	claim string
	why   string
}

var closeoutStaleClaims = []staleClaim{
	{
		"docs/troubleshooting.md", "read that record's fidelity through `expand`",
		"no retrieval response carries fidelity (D46); capture fidelity is on the capture record (D49)",
	},
	{
		"docs/troubleshooting.md", "Read the record's `Fidelity`.",
		"a retrieval record has no Fidelity field to read (D46); the capture sidecar has it (D49)",
	},
	{
		"docs/cannot-do.md", "It returns the label with the content",
		"no retrieval response carries the fidelity label (D46)",
	},
	{
		"docs/cannot-do.md", "is returned as it is, with that label",
		"no retrieval response carries the fidelity label (D46); it stays on the capture sidecar",
	},
	{
		"docs/user-guide.md", "comes from the daemon's own configuration files",
		"a spawned daemon also inherits the QOMPACK_* environment it was started with (D49)",
	},
	{
		"docs/architecture.md", "Capture, fidelity and coverage may be partial or unknown, and the response says which.",
		"no retrieval response carries fidelity or coverage (D46)",
	},
	{
		"docs/uat.md", "(`--set runtime.rehydrate.maxTokens=<small>` on a hook invocation",
		"the daemon builds the rehydration under its own configuration (D49, UAT-05 step 5)",
	},
	{
		"docs/uat.md", "the reachable form is to run the query in a project where the elimination ledger is not present",
		"the ledger opens on first use and answers absent there (D49, UAT-09 step 5)",
	},
}

// TestCloseoutStaleClaimsAreGone asserts none of the claims D46 and D49 retired is back.
func TestCloseoutStaleClaimsAreGone(t *testing.T) {
	root := repoRoot(t)
	for _, c := range closeoutStaleClaims {
		if strings.Contains(normalized(readDoc(t, root, c.page)), normalized(c.claim)) {
			t.Errorf("%s still says %q: %s", c.page, c.claim, c.why)
		}
	}
}

// TestFidelityIsReadFromTheCaptureRecord asserts the capture-gap section tells an operator where
// capture fidelity is recorded, since no command or tool shows it (D46, D49).
func TestFidelityIsReadFromTheCaptureRecord(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), "docs/troubleshooting.md"))
	start := strings.Index(body, "## 3. Capture gaps")
	end := strings.Index(body, "## 4. ")
	if start < 0 || end < start {
		t.Fatalf("docs/troubleshooting.md: section 3 not found")
	}
	if !strings.Contains(body[start:end], ".qompack/records/captures/") {
		t.Errorf("docs/troubleshooting.md section 3 does not say capture fidelity is on the sidecar " +
			"under .qompack/records/captures/")
	}
}

// hostDecodingSentence is the one sentence both pages carry about the host's decoding of binary
// files (D45: host behaviours are documented, not fixed), so the two cannot drift apart.
const hostDecodingSentence = "Qompack records what the host delivered and decodes nothing itself."

// TestHostBinaryDecodingIsDocumented asserts the user guide and the limits page both state it.
func TestHostBinaryDecodingIsDocumented(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []string{"docs/user-guide.md", "docs/cannot-do.md"} {
		if !strings.Contains(normalized(readDoc(t, root, page)), hostDecodingSentence) {
			t.Errorf("%s does not document the host's decoding of binary files: want %q", page,
				hostDecodingSentence)
		}
	}
}

// TestRecallDeniedCountIsExplained asserts docs/mcp-tools.md says what recall's denied count
// counts, including the pathless records it refuses, which is what a probe outside a session saw
// (rerun-c4/C4.9).
func TestRecallDeniedCountIsExplained(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), "docs/mcp-tools.md"))
	for _, want := range []string{"`denied`", "no usable path provenance"} {
		if !strings.Contains(body, want) {
			t.Errorf("docs/mcp-tools.md does not explain recall's denied count: missing %q", want)
		}
	}
}

// uatTestNameRe matches a Go test name quoted as inline code.
var uatTestNameRe = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// TestUATNamedTestsExist asserts every test docs/uat.md names is a real top-level test in this
// tree, and that UAT-02 carries D49's host-limited note naming them. A note naming a test that was
// renamed would point a reader at coverage that no longer exists.
func TestUATNamedTestsExist(t *testing.T) {
	root := repoRoot(t)
	raw, secs := uatSections(t)
	var uat02 string
	for _, s := range secs {
		if s.id == "UAT-02" {
			uat02 = normalized(strings.Join(raw[s.start:s.end], "\n"))
		}
	}
	const note = "non-exact fidelity is host-limited on Claude Code 2.1.280; covered by"
	if !strings.Contains(uat02, note) {
		t.Fatalf("%s UAT-02 does not carry D49's note %q", uatPath, note)
	}

	names := map[string]bool{}
	for _, m := range uatTestNameRe.FindAllStringSubmatch(strings.Join(raw, "\n"), -1) {
		names[m[1]] = false
	}
	if len(names) == 0 {
		t.Fatalf("%s names no test: the scan is looking in the wrong place", uatPath)
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		for n := range names {
			if strings.Contains(string(b), "\nfunc "+n+"(") {
				names[n] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for n, found := range names {
		if !found {
			t.Errorf("%s names `%s`, which no _test.go in this tree defines", uatPath, n)
		}
	}
}

// TestUATIntroDescribesTheCurrentRecord asserts the page's opening describes the run its Result
// blocks now record: candidate 3's first run and candidate 4's re-run under D47.
func TestUATIntroDescribesTheCurrentRecord(t *testing.T) {
	body := normalized(readDoc(t, repoRoot(t), uatPath))
	end := strings.Index(body, "## The isolation rule")
	if end < 0 {
		t.Fatalf("%s: the isolation rule heading was not found", uatPath)
	}
	for _, want := range []string{"`d5598eb4`", "`9f6a2fad`", "D47"} {
		if !strings.Contains(body[:end], want) {
			t.Errorf("%s introduction does not describe the current record: missing %s", uatPath, want)
		}
	}
}

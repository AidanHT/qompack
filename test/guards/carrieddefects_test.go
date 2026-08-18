package guards

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A defect a subplan knowingly ships has exactly two ways of being lost, and prose stops neither.
// It can be forgotten — nothing in the build mentions it, so the checkpoint that was supposed to
// resolve it never learns it exists. Or it can be fixed and left recorded as broken, so the note
// rots and the next reader distrusts the whole file.
//
// plans/CARRIED-DEFECTS.tsv plus the three tests below close both. The manifest is data rather than
// narrative, every open row must point at a test that still passes, and — the gate that matters —
// no row may still be open once its owning checkpoint has written its completion report. Resolving
// a row therefore requires either a fix or an explicit re-deferral; nothing is reachable by doing
// nothing, which is the only failure mode a note in a commit body actually has.
//
// This is the same shape as TestNightlyFuzzMatrix and TestStubRegistry_ListsEveryPackageOnDisk: a
// list that has to keep agreeing with the tree, checked mechanically.

// carriedDefect is one row of plans/CARRIED-DEFECTS.tsv.
type carriedDefect struct {
	id       string
	openedBy string
	owner    string
	status   string
	evidence string
	summary  string
	line     int
}

// open reports whether this row still needs work from its owning checkpoint.
func (d carriedDefect) open() bool { return d.status == "open" }

// carriedDefectsPath and carriedDefectsDoc are the manifest and the document that explains it.
const (
	carriedDefectsPath = "plans/CARRIED-DEFECTS.tsv"
	carriedDefectsDoc  = "plans/V2-SP-04-carried-defects.md"
)

// loadCarriedDefects parses the manifest, failing on anything it cannot read as a row rather than
// skipping it — a malformed line in a file whose whole purpose is to be honest about known problems
// would be the worst possible thing to tolerate silently.
func loadCarriedDefects(t *testing.T, root string) []carriedDefect {
	t.Helper()

	f, err := os.Open(filepath.Join(root, filepath.FromSlash(carriedDefectsPath)))
	require.NoError(t, err, "%s is missing; it is the record every carried defect lives in", carriedDefectsPath)
	defer func() { _ = f.Close() }()

	var out []carriedDefect
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		require.Len(t, fields, 6, "%s:%d: expected 6 tab-separated columns, got %d", carriedDefectsPath, n, len(fields))
		if fields[0] == "id" {
			continue
		}
		out = append(out, carriedDefect{
			id: fields[0], openedBy: fields[1], owner: fields[2],
			status: fields[3], evidence: fields[4], summary: fields[5], line: n,
		})
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, out, "%s parsed to zero rows", carriedDefectsPath)
	return out
}

// TestCarriedDefects_ManifestIsWellFormed checks the shape of every row: unique ids, a recognized
// status, an owning checkpoint that exists as a plan document, and a section in the detail document.
//
// The detail requirement is the one that does real work. An id with no section is a row someone can
// read but not act on, and "what does SP04-D3 actually mean" is precisely the question a checkpoint
// session will need answered months after the subplan that wrote it has been forgotten.
func TestCarriedDefects_ManifestIsWellFormed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	detail, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(carriedDefectsDoc)))
	require.NoError(t, err, "%s is missing", carriedDefectsDoc)

	seen := map[string]bool{}
	for _, d := range loadCarriedDefects(t, root) {
		t.Run(d.id, func(t *testing.T) {
			require.False(t, seen[d.id], "duplicate id %s", d.id)
			seen[d.id] = true

			require.NotEmpty(t, d.summary, "a row with no summary explains nothing")
			require.True(t,
				d.status == "open" || d.status == "fixed" || d.status == "wontfix" ||
					strings.HasPrefix(d.status, "deferred:"),
				"%s:%d: status %q must be open, fixed, wontfix or deferred:<checkpoint>",
				carriedDefectsPath, d.line, d.status)

			require.Contains(t, string(detail), "## "+d.id,
				"%s has no `## %s` section in %s; a row nobody can act on is worse than no row",
				d.id, d.id, carriedDefectsDoc)

			matches, gerr := filepath.Glob(filepath.Join(root, "plans", d.owner+"-*.md"))
			require.NoError(t, gerr)
			require.NotEmpty(t, matches,
				"%s names owner %q, which matches no plans/%s-*.md checkpoint document",
				d.id, d.owner, d.owner)
		})
	}
}

// TestCarriedDefects_OpenRowsHaveLivingEvidence asserts every open row's evidence test still exists.
//
// It is what stops a defect being fixed and left recorded as broken: the characterization tests pin
// the WRONG behaviour on purpose, so fixing the defect makes them fail, and the only way to get back
// to green is to update the row. Deleting the test instead is the other escape, and this closes it.
//
// A row may record "-" when the defect has no runtime symptom to pin — a process trap like SP04-D4,
// or a measurement note like SP04-D6. Those are carried by the document alone, which is why the
// manifest requires a detail section for every id and not only for the pinned ones.
func TestCarriedDefects_OpenRowsHaveLivingEvidence(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, d := range loadCarriedDefects(t, root) {
		if !d.open() || d.evidence == "-" {
			continue
		}
		t.Run(d.id, func(t *testing.T) {
			require.True(t, testExistsAnywhere(t, root, d.evidence),
				"%s is open and names evidence %q, but no such test exists. If the defect was "+
					"fixed, set %s's status to `fixed` in %s and say so in %s; if the test was "+
					"renamed, update the row",
				d.id, d.evidence, d.id, carriedDefectsPath, carriedDefectsDoc)
		})
	}
}

// TestCarriedDefects_WaveReportRequiresResolution is the gate.
//
// A checkpoint's completion report is the artefact that says the wave is done. This makes writing
// one conditional on having dispositioned every defect the wave inherited: fix it, or re-defer it to
// a named later checkpoint with the reason written down. Both are conscious acts that leave a trace
// in git history; neither is reachable by forgetting.
//
// While no report exists the assertion is vacuous and the test passes, which is the state on the
// branch that adds it. It is not skipped — a skip would be invisible to `devtool lint`'s stubskips
// sub-check and would read as an unfinished test rather than as an inapplicable condition.
func TestCarriedDefects_WaveReportRequiresResolution(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, d := range loadCarriedDefects(t, root) {
		if !d.open() {
			continue
		}
		// V2-VERIFY owns rows resolved before plans/V2-report.md is written; the checkpoint name
		// and the report name share the wave prefix, which is the convention V1 established.
		wave, _, ok := strings.Cut(d.owner, "-")
		require.True(t, ok, "%s: owner %q is not <wave>-<kind>", d.id, d.owner)

		report := filepath.Join(root, "plans", wave+"-report.md")
		if _, err := os.Stat(report); err != nil {
			continue
		}
		require.Failf(t, "carried defect left open at wave sign-off",
			"%s is still `open` in %s, but plans/%s-report.md exists — %s has been signed off "+
				"with an unresolved defect it owns.\n\n  %s\n\nResolve it one of two ways: fix it "+
				"and set the status to `fixed`, or set the status to `deferred:<checkpoint>` and "+
				"add the reason to %s. Both are fine; leaving the row open is not.",
			d.id, carriedDefectsPath, wave, d.owner, d.summary, carriedDefectsDoc)
	}
}

// testExistsAnywhere asks the toolchain whether any package declares a test or fuzz target named
// fn, using the same `go test -list` probe the nightly fuzz guard uses so the two cannot disagree
// about what "exists" means.
//
// A listing that FAILED is not an answer. `go test -list ./...` exits non-zero when ANY package in
// the module does not build, and the empty stdout that comes back with it means "the toolchain
// never got far enough to look", not "no such test". Reading the second as the first is the whole
// of V2-MERGE-23: while internal/dag would not compile, three rows whose evidence tests live in
// internal/canon — a package that built fine — were reported as having no evidence test at all,
// under a message advising the reader to mark them `fixed`. Following that advice would have closed
// three open defects on the strength of an unrelated compile error.
//
// So a listing error is fatal here, and says in those words that it is not evidence of anything
// about the defect. Absence is only ever reported from a listing that actually ran.
func testExistsAnywhere(t *testing.T, root, fn string) bool {
	t.Helper()

	cmd := exec.Command("go", "test", "-run", "^$", "-list", "^"+fn+"$", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			stderr = strings.TrimSpace(string(exit.Stderr))
		}
		t.Fatalf("listing the module's tests failed, so this guard cannot say whether %s exists.\n\n"+
			"  cd %s && go test -run '^$' -list '^%s$' ./...\n  %v\n\n%s\n\n"+
			"This is a BUILD/LISTING failure. It is NOT evidence that %s is absent, and it is NOT "+
			"evidence that the defect naming it was fixed: one package that does not compile makes "+
			"`go test -list ./...` exit non-zero with nothing usable on stdout, whatever the state "+
			"of the package the test actually lives in. Fix the build and run this guard again — do "+
			"not change any row in %s on the strength of this failure.",
			fn, root, fn, err, stderr, fn, carriedDefectsPath)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == fn {
			return true
		}
	}
	return false
}

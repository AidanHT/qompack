package guards

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// unresolved reports whether this row still needs work from SOME checkpoint.
//
// `deferred:<X>` counts, and that is the whole point of the distinction. Until the 2026-08-22 audit
// this was `status == "open"` alone, and every row in the shipped manifest is `fixed` or
// `deferred:V3-VERIFY` — so the evidence-liveness check below covered zero rows, the sign-off gate
// covered zero rows, and the header's three-way "load-bearing" claim was one-third true. A deferral
// is a promise to a named later checkpoint, not a resolution.
func (d carriedDefect) unresolved() bool {
	return d.status == "open" || strings.HasPrefix(d.status, deferredPrefix)
}

// deferredPrefix marks a status that names the checkpoint a row was deferred to.
const deferredPrefix = "deferred:"

// resolver is the checkpoint that must dispose of this row before it writes its report: the owner
// for an open row, and the DEFERRAL TARGET for a deferred one.
//
// Keying the gate off owner alone is what let six rows sit deferred to V3-VERIFY while the gate
// asked only whether V2-report.md existed — a question whose answer had already stopped changing.
func (d carriedDefect) resolver() string {
	if target, ok := strings.CutPrefix(d.status, deferredPrefix); ok {
		return strings.TrimSpace(target)
	}
	return d.owner
}

// carriedDefectsPath is the manifest every carried defect lives in.
const carriedDefectsPath = "plans/CARRIED-DEFECTS.tsv"

// carriedDefectsWave is the wave prefix plans/ spells its documents with. CARRIED-DEFECTS.tsv is
// V2's record — every row in it is owned by a V2 checkpoint — so every detail document it points
// at is a V2-* document. A later wave keeping its own manifest changes this one constant.
const carriedDefectsWave = "V2"

// The document explaining a row is DERIVED FROM THE ROW'S ID, never hardcoded. This guard used to
// name plans/V2-SP-04-carried-defects.md and nothing else, which held only while SP-04 was the one
// subplan with rows; wave 1 carried items from six, and an SP06-D1 row could then be explained only
// in a file titled for SP-04 (V2-MERGE-19). The scheme, in full:
//
//	 1. An id is <SPNN>-D<n>. Its subplan is that prefix with the hyphen plans/ spells and the id
//	    elides: SP04-D1 -> SP-04, SP06-D2 -> SP-06.
//	 2. If plans/V2-<subplan>-carried-defects.md is on disk, the `## <id>` section MUST be there.
//	    That is the name SP-04's document already has, generalized rather than special-cased.
//	 3. Otherwise the section MUST be in the wave-wide document, plans/V2-WAVE1-carried-defects.md
//	    — one file for the subplans that carried too little to deserve one each.
//
// Exactly one document is therefore correct for any given row, which is what lets the failure
// message name a single path rather than offering a choice. Adding the per-subplan document is what
// moves its rows: create plans/V2-SP-06-carried-defects.md and SP06-* sections must move into it.

// carriedDefectsSubplanRE matches the subplan prefix of an id — SP04, SP07 — and nothing else, so
// an id that is not <SPNN>-D<n> fails here rather than deriving a nonsense document path.
var carriedDefectsSubplanRE = regexp.MustCompile(`^SP[0-9]{2}$`)

// carriedDefectsWaveDoc is the fallback of rule 3, spelled once.
var carriedDefectsWaveDoc = "plans/" + carriedDefectsWave + "-WAVE1-carried-defects.md"

// carriedDefectsDocFor returns the repo-relative path of the ONE document that must carry id's
// section, applying rules 1-3 above.
func carriedDefectsDocFor(t *testing.T, root, id string) string {
	t.Helper()

	prefix, rest, ok := strings.Cut(id, "-")
	require.True(t, ok && rest != "" && carriedDefectsSubplanRE.MatchString(prefix),
		"%s: id %q is not <SPNN>-D<n> (SP04-D1, SP07-D2), so no detail document can be derived "+
			"from it and no reader can find out what the row means", carriedDefectsPath, id)

	perSubplan := "plans/" + carriedDefectsWave + "-SP-" + strings.TrimPrefix(prefix, "SP") + "-carried-defects.md"
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(perSubplan))); err == nil {
		return perSubplan
	}
	return carriedDefectsWaveDoc
}

// carriedDefectsDetail returns the document carriedDefectsDocFor named and its contents, failing
// with the exact path a row author has to create when it is not there.
func carriedDefectsDetail(t *testing.T, root, id string) (path, body string) {
	t.Helper()

	path = carriedDefectsDocFor(t, root, id)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	require.NoError(t, err,
		"%s's `## %s` section must live in %s, and that file does not exist. Either create it, or "+
			"create this row's own plans/%s-SP-NN-carried-defects.md — the guard prefers the "+
			"per-subplan document whenever it is on disk, and falls back to %s only when it is not",
		id, id, path, carriedDefectsWave, carriedDefectsWaveDoc)
	return path, string(b)
}

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
// status, an owning checkpoint that exists as a plan document, and a section in the detail document
// the row's own id selects.
//
// The detail requirement is the one that does real work. An id with no section is a row someone can
// read but not act on, and "what does SP04-D3 actually mean" is precisely the question a checkpoint
// session will need answered months after the subplan that wrote it has been forgotten.
func TestCarriedDefects_ManifestIsWellFormed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

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

			doc, detail := carriedDefectsDetail(t, root, d.id)
			require.Contains(t, detail, "## "+d.id,
				"%s has no `## %s` section in %s; a row nobody can act on is worse than no row. "+
					"That file is where this row's section must live: the guard uses "+
					"plans/%s-SP-NN-carried-defects.md when the subplan has one on disk, and %s "+
					"otherwise",
				d.id, d.id, doc, carriedDefectsWave, carriedDefectsWaveDoc)

			matches, gerr := filepath.Glob(filepath.Join(root, "plans", d.owner+"-*.md"))
			require.NoError(t, gerr)
			require.NotEmpty(t, matches,
				"%s names owner %q, which matches no plans/%s-*.md checkpoint document",
				d.id, d.owner, d.owner)

			// A deferral target is held to the owner's standard. `deferred:V9-VERIFY` reads like a
			// decision and is a way of never being asked again: nothing would ever write
			// plans/V9-report.md, so the sign-off gate could never fire on it.
			target, deferred := strings.CutPrefix(d.status, deferredPrefix)
			if !deferred {
				return
			}
			target = strings.TrimSpace(target)
			require.NotEmpty(t, target, "%s: `deferred:` must name the checkpoint it defers to", d.id)
			require.NotEqual(t, d.owner, target,
				"%s defers to %s, the checkpoint that already owns it — a deferral must move the row "+
					"forward to a LATER checkpoint", d.id, target)
			deferMatches, dgerr := filepath.Glob(filepath.Join(root, "plans", target+"-*.md"))
			require.NoError(t, dgerr)
			require.NotEmpty(t, deferMatches,
				"%s defers to %q, which matches no plans/%s-*.md checkpoint document. A deferral to "+
					"a checkpoint that does not exist is a row nothing will ever resolve",
				d.id, target, target)
		})
	}
}

// TestCarriedDefects_OpenRowsHaveLivingEvidence asserts every UNRESOLVED row's evidence test still
// exists — `open` rows and `deferred:<X>` rows alike. The name predates that distinction and is kept
// because plans/V2-VERIFY-primitives-store-dag-and-baseline.md's V2-MERGE-23 row names it.
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
		if !d.unresolved() || d.evidence == "-" {
			continue
		}
		t.Run(d.id, func(t *testing.T) {
			require.True(t, testExistsAnywhere(t, root, d.evidence),
				"%s is %s and names evidence %q, but no such test exists. If the defect was "+
					"fixed, set %s's status to `fixed` in %s and say so in %s; if the test was "+
					"renamed, update the row",
				d.id, d.status, d.evidence, d.id, carriedDefectsPath, carriedDefectsDocFor(t, root, d.id))
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
		if !d.unresolved() {
			continue
		}
		// The checkpoint that must dispose of the row is its RESOLVER, not always its owner: an
		// open row is its owner's, a `deferred:<X>` row is X's. The checkpoint name and the report
		// name share the wave prefix, which is the convention V1 established.
		resolver := d.resolver()
		wave, _, ok := strings.Cut(resolver, "-")
		require.True(t, ok, "%s: resolver %q is not <wave>-<kind>", d.id, resolver)

		report := filepath.Join(root, "plans", wave+"-report.md")
		if _, err := os.Stat(report); err != nil {
			continue
		}
		require.Failf(t, "carried defect left unresolved at wave sign-off",
			"%s is still `%s` in %s, but plans/%s-report.md exists — %s has been signed off with a "+
				"defect it was responsible for resolving.\n\n  %s\n\nResolve it one of two ways: "+
				"fix it and set the status to `fixed`, or set the status to `deferred:<a later "+
				"checkpoint>` and add the reason to %s. Both are fine; leaving it for this "+
				"checkpoint is not.",
			d.id, d.status, carriedDefectsPath, wave, resolver, d.summary,
			carriedDefectsDocFor(t, root, d.id))
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

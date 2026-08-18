package contract_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// lastSessionIDField is the field this guard protects. It is matched by NAME, without type
// information, which is deliberate: a name-only match is over-strict rather than under-strict, and
// over-strict is the correct direction for a field whose misuse is silent. Nothing else in this
// module declares a field by this name, so there is nothing for the imprecision to catch wrongly —
// and a future type that took the name would be worth a deliberate decision anyway.
const lastSessionIDField = "LastSessionID"

// ownerDir is the ONE package permitted to write lastSessionIDField, expressed as a
// slash-separated module-relative path.
const ownerDir = "internal/contract"

// TestGuard_LastSessionIDIsWrittenOnlyByContract is §2.5a D made structural.
//
// contract.SessionHistory.LastSessionID is owned exclusively by checkSessionStartFires
// (assertions.go). It is how that Check tells "this session's miss was already counted" from "a
// genuinely new session, count it too". Any other code that assigns it — in particular a daemon or
// an observer pre-setting it to the INCOMING session's id at SessionStart, which is the natural
// thing to write — makes checkSessionStartFires see "already counted" on the very first RunAll of
// every session. StartsWithoutMarker then never reaches 2, and session_start.fires, a SevCritical
// assertion, is permanently and silently disabled.
//
// The word that matters is SILENTLY. Every existing test still passes when this happens: the
// wedge is invisible to anything that does not specifically look for it. Protection before this
// test was a doc comment on the field (history.go) plus one wedge test
// (TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting), and neither can
// stop the write being added — they only describe why it must not be. This does stop it, at the
// point the code is written rather than at the point somebody notices the assertion never fires
// again.
//
// Scope is every NON-test .go file in the module outside internal/contract. Test files are exempt
// on purpose: a fixture may legitimately construct a SessionHistory with a LastSessionID set to
// pin a starting state (internal/cli/selftest_test.go does exactly that, and it is correct), and
// forbidding that would be forbidding tests from describing the states they exist to check.
//
// SP-08's L0 observer is the code most likely to hit this — it is the next thing to sit on the
// SessionStart path — which is why the failure below names the consequence, not just the rule.
func TestGuard_LastSessionIDIsWrittenOnlyByContract(t *testing.T) {
	root := moduleRootForGuard(t)

	violations := scanForLastSessionIDWrites(t, root, func(rel string) bool {
		return !strings.HasPrefix(rel, ownerDir+"/")
	})

	require.Empty(t, violations,
		"%s is owned exclusively by checkSessionStartFires (internal/contract/assertions.go); "+
			"see the field's own doc comment in internal/contract/history.go.\n\n"+
			"Writing it anywhere else — above all pre-setting it to the incoming session's id at "+
			"SessionStart — makes that Check read every session's first RunAll as already counted, "+
			"so StartsWithoutMarker never reaches 2 and the SevCritical session_start.fires "+
			"assertion is disabled for good, with every test still green.\n\n"+
			"If the caller needs to know which session was last counted, READ the field; if it needs "+
			"its own per-session bookkeeping, add its own field. Writes found:\n  %s",
		lastSessionIDField, strings.Join(violations, "\n  "))
}

// TestGuard_LastSessionIDScannerFindsTheOwnedWrites is the guard's own self-test, and it is not
// optional: a walker that silently matched nothing — a broken path filter, an AST shape this code
// does not visit — would make the test above pass vacuously forever, which is the same failure
// mode it exists to prevent. Pointed at internal/contract, the scanner must find the three writes
// checkSessionStartFires legitimately makes (assertions.go, the first-session, marker-found and
// marker-absent arms).
func TestGuard_LastSessionIDScannerFindsTheOwnedWrites(t *testing.T) {
	root := moduleRootForGuard(t)

	owned := scanForLastSessionIDWrites(t, root, func(rel string) bool {
		return strings.HasPrefix(rel, ownerDir+"/")
	})

	require.GreaterOrEqual(t, len(owned), 3,
		"the scanner found %d write(s) inside %s; checkSessionStartFires makes three, so a lower "+
			"number means this guard has stopped seeing assignments at all and the check above is vacuous: %v",
		len(owned), ownerDir, owned)
	for _, v := range owned {
		require.Contains(t, v, "assertions.go",
			"a write to %s appeared outside assertions.go inside %s; the field's owner is "+
				"checkSessionStartFires and nothing else", lastSessionIDField, ownerDir)
	}
}

// scanForLastSessionIDWrites parses every non-test Go file under root whose module-relative,
// slash-separated path satisfies include, and returns "path:line" for each write it finds, sorted.
//
// A "write" is either an assignment whose left-hand side selects the field (h.LastSessionID = x,
// including the compound and short forms) or a composite-literal key naming it
// (SessionHistory{LastSessionID: x}) — the two ways Go can put a value in a struct field. Reads
// are untouched: consulting the field is exactly what the daemon is supposed to do.
func scanForLastSessionIDWrites(t *testing.T, root string, include func(rel string) bool) []string {
	t.Helper()

	var found []string
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipGuardDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !include(rel) {
			return nil
		}

		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w", rel, parseErr)
		}
		for _, pos := range lastSessionIDWrites(fset, f) {
			found = append(found, rel+":"+pos)
		}
		return nil
	})
	require.NoError(t, err)

	sort.Strings(found)
	return found
}

// lastSessionIDWrites returns the line of every write to lastSessionIDField in f.
func lastSessionIDWrites(fset *token.FileSet, f *ast.File) []string {
	var lines []string
	record := func(p token.Pos) {
		lines = append(lines, fmt.Sprintf("%d", fset.Position(p).Line))
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == lastSessionIDField {
					record(sel.Sel.Pos())
				}
			}
		case *ast.IncDecStmt:
			// ++/-- do not typecheck against a SessionID, but a future retyping would make them
			// legal, and a guard that only knows one syntax is a guard with a hole in it.
			if sel, ok := node.X.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == lastSessionIDField {
				record(sel.Sel.Pos())
			}
		case *ast.CompositeLit:
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == lastSessionIDField {
					record(key.Pos())
				}
			}
		}
		return true
	})
	return lines
}

// skipGuardDir reports whether a directory is outside the scan: version-control and dependency
// trees hold no Qompack source, and testdata holds fixtures rather than code that runs.
func skipGuardDir(name string) bool {
	// The go tool ignores directories whose names begin with "." or "_"
	// (go help packages), so nothing under them is part of this module's
	// build -- .claude/worktrees in particular can hold entire nested
	// checkouts of this repo whose owned writes would otherwise be
	// reported at non-owner paths. vendor and node_modules are dependency
	// trees; testdata is the same toolchain convention by name.
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "testdata":
		return true
	}
	return false
}

// moduleRootForGuard walks up from the test's working directory to the directory holding go.mod,
// so the scan covers the whole module regardless of where the test binary was started.
func moduleRootForGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if fi, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

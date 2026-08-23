package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sketch.Load and sketch.LoadWithLog differ in exactly one way that matters, and it is invisible at
// the call site: Load hands LoadWithLog a logging.Nop, so a corrupt or oversize sketch fires the
// process-wide Loud ring and the obs counters behind it, but writes NO durable line — not to the
// day log, not to LOUD.log. The ring is a 32-entry debugging aid; the line is what an operator
// reads after the fact. internal/sketch/io.go says so in Load's own doc comment, and
// internal/sketch/doc.go repeats it as the package contract.
//
// It reverted anyway. internal/daemon/sketchset.go called Load from a method that already held a
// logger, and paired it with an `errors.Is(err, core.ErrNotFound)` branch — which LoadWithLog
// returns for EVERY failure — so a CRC-failed sketch was filed under "expected, Debug" all through
// wave 1. The 2026-08-22 plan audit found it; this guard is what stops it coming back, and it is
// the enforcement V2-SP-03's plan described and nobody wrote.
//
// The rule is the contract, stated as syntax: outside internal/sketch itself, no non-test file may
// call sketch.Load. Every production caller has a logger — that is what makes it a production
// caller — so there is no legitimate remaining use.
const sketchLoadForbidden = "sketch.Load"

// sketchLoadScanRoots are the trees a shipped binary is built from. tools/ is excluded on purpose:
// it is build-time only and never linked into cmd/qompack, which is the same boundary
// 00-ARCHITECTURE §8's import allowlists draw.
var sketchLoadScanRoots = []string{"internal", "cmd", "test"}

// TestGuard_NoSilentSketchLoadOutsideItsPackage is the guard itself.
func TestGuard_NoSilentSketchLoadOutsideItsPackage(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	scanned := 0
	var offenders []string

	for _, r := range sketchLoadScanRoots {
		dir := filepath.Join(root, r)
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := filepath.Base(p)
				if base == "testdata" || strings.HasPrefix(base, ".") && base != "." {
					return filepath.SkipDir
				}
				// internal/sketch owns both spellings: Load is its own exported API and it calls
				// LoadWithLog itself.
				if rel, relErr := filepath.Rel(root, p); relErr == nil &&
					filepath.ToSlash(rel) == "internal/sketch" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scanned++
			if callsSelector(t, p, sketchLoadForbidden) {
				rel, _ := filepath.Rel(root, p)
				offenders = append(offenders, filepath.ToSlash(rel))
			}
			return nil
		})
		require.NoError(t, err)
	}

	require.NotZero(t, scanned, "the walk found no non-test Go files — the guard would vacuously pass")
	require.Empty(t, offenders,
		"these non-test files call %s, whose failures are reported through a logging.Nop and therefore "+
			"write no durable log line: %s\n\n"+
			"Call sketch.LoadWithLog(p, s, log) instead, and classify the error by fs.ErrNotExist "+
			"(a genuine cold start, Debug) rather than by core.ErrNotFound, which LoadWithLog returns "+
			"for corrupt, truncated and oversize files too.",
		sketchLoadForbidden, strings.Join(offenders, ", "))
}

// TestGuard_SketchLoadScannerSeesACall is this guard's self-test, and it is not optional: a scanner
// that matched nothing would make the test above pass forever against the very defect it pins. It
// must see the forbidden spelling, and must NOT see the permitted one that shares its prefix.
func TestGuard_SketchLoadScannerSeesACall(t *testing.T) {
	t.Parallel()

	const bad = `package p

import "github.com/qompack/qompack/internal/sketch"

func load(p string, s sketch.Sketch) error { return sketch.Load(p, s) }
`
	const good = `package p

import (
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/sketch"
)

func load(p string, s sketch.Sketch, log logging.Logger) error {
	return sketch.LoadWithLog(p, s, log)
}
`
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.go")
	goodPath := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(badPath, []byte(bad), 0o600))
	require.NoError(t, os.WriteFile(goodPath, []byte(good), 0o600))

	require.True(t, callsSelector(t, badPath, sketchLoadForbidden),
		"the scanner must see sketch.Load, or the guard checks nothing")
	require.False(t, callsSelector(t, goodPath, sketchLoadForbidden),
		"sketch.LoadWithLog must not be mistaken for sketch.Load — the two differ by a suffix, and a "+
			"prefix match here would forbid the call the guard exists to require")
}

// callsSelector reports whether path contains a call whose function is the selector pkg.Name.
//
// The match is on the selector's identifier text, not on resolved types, which is how every other
// AST guard in this package works (sharedreaders_test.go says why): over-strict is the safe
// direction, since a local variable named sketch would be flagged rather than missed.
func callsSelector(t *testing.T, path, selector string) bool {
	t.Helper()

	pkg, name, ok := strings.Cut(selector, ".")
	require.True(t, ok, "selector %q must be spelled pkg.Func", selector)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing %s", path)

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != name {
			return true
		}
		if id, isIdent := sel.X.(*ast.Ident); isIdent && id.Name == pkg {
			found = true
			return false
		}
		return true
	})
	return found
}

package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// §13 invariant 4 / §5.3: nothing scattered before the compaction point p may be selected. The
// analyzer enforces it in exactly one place — NewSelector's constructor filter,
// internal/analyzer/selector.go's `if b.Pos < p` — on purpose, so the refusal cannot drift out of
// step with itself: a second Pos-versus-p comparison anywhere in the package is a second opinion
// about where p is, and the first one to disagree is a bypass. SP-15's plan required a
// TestNoSelectorBypass that AST-parses internal/analyzer and counts these sites; the V5-VERIFY
// inventory (I-15.13) found the property true by grep and enforced by nobody. This is the guard.
//
// Test files are unrestricted: internal/analyzer's own tests compare Pos against p to prove the
// refusal fires, and a guard that forbade that would forbid testing the invariant.
const (
	// selectorPkgDir is the package the scan is confined to, module-relative.
	selectorPkgDir = "internal/analyzer"
	// selectorPosCheckFile is the one non-test file allowed to hold the comparison.
	selectorPosCheckFile = "selector.go"
	// selectorPosCheckSites is how many Pos-versus-p comparisons the package may contain.
	selectorPosCheckSites = 1
)

// TestGuard_SelectorHasExactlyOnePosVersusPComparison is the enforcement itself.
func TestGuard_SelectorHasExactlyOnePosVersusPComparison(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := nonTestGoFiles(t, root, []string{selectorPkgDir}, nil)
	require.NotEmpty(t, files, "the walk found no non-test Go files — the guard would vacuously pass")

	var sites []string
	for _, p := range files {
		for _, line := range posVersusPComparisons(t, p) {
			rel, err := filepath.Rel(root, p)
			require.NoError(t, err)
			sites = append(sites, filepath.ToSlash(rel)+":"+line)
		}
	}

	require.Len(t, sites, selectorPosCheckSites,
		"%s must compare a block's Pos against p in exactly %d place, found %d: %s\n\n"+
			"§13 invariant 4 is enforced by NewSelector's constructor filter and nowhere else. A "+
			"second comparison is a second opinion about where p is; route the check through "+
			"NewSelector instead, or if the filter itself moved, move %s in this guard with it.",
		selectorPkgDir, selectorPosCheckSites, len(sites), strings.Join(sites, ", "),
		selectorPosCheckFile)
	require.Equal(t, selectorPosCheckFile, filepath.Base(strings.SplitN(sites[0], ":", 2)[0]),
		"the one permitted Pos-versus-p comparison is NewSelector's constructor filter in %s, "+
			"but the site found is %s", selectorPosCheckFile, sites[0])
}

// TestGuard_SelectorBypassScannerSeesAComparison is this guard's self-test, and it is not optional:
// a scanner that matched nothing would make the test above fail on its count, but one that matched
// only the spelling selector.go happens to use today would let a reordered or field-qualified
// comparison slip past it. It must see both operand orders and a receiver-qualified p, and must NOT
// see a comparison of Pos against anything else.
func TestGuard_SelectorBypassScannerSeesAComparison(t *testing.T) {
	t.Parallel()

	const src = `package p

type block struct{ Pos int }

type sel struct{ p int }

func a(b block, p int) bool  { return b.Pos < p }
func c(b block, p int) bool  { return p >= b.Pos }
func d(s sel, b block) bool  { return b.Pos == s.p }
func e(b block, q int) bool  { return b.Pos < q }
func f(b block, p int) int   { return b.Pos - p }
func g(b block, p int) bool  { return b.Pos < p && q(b) }
func q(block) bool           { return true }
`
	path := filepath.Join(t.TempDir(), "scan.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	got := posVersusPComparisons(t, path)
	require.Equal(t, []string{"7", "8", "9", "12"}, got,
		"the scanner must see Pos<p, p>=Pos and Pos==s.p, and must not see Pos<q or Pos-p")
}

// posVersusPComparisons returns the line numbers of every comparison in path whose two operands
// are a selector named Pos and an identifier — bare or receiver-qualified — named p.
//
// The match is on identifier text, not resolved types, which is how every other AST guard in this
// package works (sharedreaders_test.go says why): over-strict is the safe direction, since a
// comparison that merely looks like the invariant-4 check would be flagged rather than missed.
func posVersusPComparisons(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing %s", path)

	var lines []string
	ast.Inspect(f, func(n ast.Node) bool {
		bin, isBin := n.(*ast.BinaryExpr)
		if !isBin || !isComparison(bin.Op) {
			return true
		}
		if (isPosSelector(bin.X) && isPIdent(bin.Y)) || (isPIdent(bin.X) && isPosSelector(bin.Y)) {
			lines = append(lines, strconv.Itoa(fset.Position(bin.OpPos).Line))
		}
		return true
	})
	return lines
}

// isComparison reports whether op is one of the six ordering or equality operators.
func isComparison(op token.Token) bool {
	switch op {
	case token.LSS, token.LEQ, token.GTR, token.GEQ, token.EQL, token.NEQ:
		return true
	}
	return false
}

// isPosSelector reports whether e is spelled <anything>.Pos.
func isPosSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Pos"
}

// isPIdent reports whether e is spelled p or <anything>.p.
func isPIdent(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name == "p"
	case *ast.SelectorExpr:
		return x.Sel.Name == "p"
	}
	return false
}

// nonTestGoFiles walks the module-relative dirs under root and returns every non-test .go file,
// skipping testdata and dot directories and any module-relative directory named in skipDirs.
// The result is in WalkDir's lexical order, so a guard's failure message is stable run to run.
func nonTestGoFiles(t *testing.T, root string, dirs []string, skipDirs map[string]bool) []string {
	t.Helper()

	var files []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)),
			func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					base := filepath.Base(p)
					if base == "testdata" || strings.HasPrefix(base, ".") && base != "." {
						return filepath.SkipDir
					}
					rel, relErr := filepath.Rel(root, p)
					if relErr != nil {
						return relErr
					}
					if skipDirs[filepath.ToSlash(rel)] {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
					return nil
				}
				files = append(files, p)
				return nil
			})
		require.NoError(t, err)
	}
	return files
}

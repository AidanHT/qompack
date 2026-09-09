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

// internal/commands renders the slash commands' text and JSON, and the §2.14 target is stable
// command, JSON and exit semantics: the same report renders the same bytes, wherever it is written
// and whenever it runs. That holds only if the package writes through the io.Writer it is handed
// and reads the clock through Deps — a fmt.Println to the process's stdout escapes the envelope,
// an os.Stderr write escapes the exit-code contract, and a time.Now() makes two renders of one
// report differ. SP-14's plan required a TestNoDirectStdio and a TestNoTimeNowInPackage; the
// V5-VERIFY inventory (I-14.3) found the property true by grep and enforced by nobody. This is
// the guard.
//
// Test files are unrestricted: a test may read the wall clock to build a fixture, and the
// behavioural proxies (TestRenderStatus_IsDeterministic and its siblings) are what prove the
// rendering itself is pure.
const commandsPkgDir = "internal/commands"

// commandsForbidden are the selector shapes the scan flags, spelled pkg.Name. A trailing * matches
// any name with that prefix, so fmt.Print, fmt.Printf and fmt.Println are one row. The match is
// name-only, without type information, which is how every other AST guard in this package works
// (sharedreaders_test.go says why): over-strict is the safe direction.
var commandsForbidden = []string{"fmt.Print*", "os.Stdout", "os.Stderr", "time.Now"}

// TestGuard_CommandsAreFreeOfStdioAndWallClock is the enforcement itself.
func TestGuard_CommandsAreFreeOfStdioAndWallClock(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := nonTestGoFiles(t, root, []string{commandsPkgDir}, nil)
	require.NotEmpty(t, files, "the walk found no non-test Go files — the guard would vacuously pass")

	var offenders []string
	for _, p := range files {
		for _, hit := range forbiddenSelectors(t, p, commandsForbidden) {
			rel, err := filepath.Rel(root, p)
			require.NoError(t, err)
			offenders = append(offenders, filepath.ToSlash(rel)+":"+hit)
		}
	}

	require.Empty(t, offenders,
		"these non-test files in %s reach for the process's stdio or wall clock: %s\n\n"+
			"A command writes through the io.Writer its handler is handed and reads the clock "+
			"through Deps.now(), so one report renders the same bytes every time (§2.14). Route "+
			"the write through the envelope and the time through Deps.",
		commandsPkgDir, strings.Join(offenders, ", "))
}

// TestGuard_CommandsPurityScannerSeesAForbiddenSelector is this guard's self-test, and it is not
// optional: a scanner that matched nothing would make the test above pass forever. It must see all
// three shapes — a Print* call, a bare os.Stdout reference and a time.Now call — and must NOT see
// the permitted spellings that share their packages.
func TestGuard_CommandsPurityScannerSeesAForbiddenSelector(t *testing.T) {
	t.Parallel()

	const bad = `package p

import (
	"fmt"
	"os"
	"time"
)

func a()             { fmt.Println("x") }
func b() *os.File    { return os.Stdout }
func c()             { fmt.Fprintln(os.Stderr, "x") }
func d() time.Time   { return time.Now() }
func e() func() time.Time { return time.Now }
`
	const good = `package p

import (
	"fmt"
	"io"
	"time"
)

func a(w io.Writer)  { fmt.Fprintf(w, "%s", fmt.Sprint("x")) }
func b() time.Duration { return time.Second }
func c(now time.Time) time.Time { return now.Add(time.Minute) }
`
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.go")
	goodPath := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(badPath, []byte(bad), 0o600))
	require.NoError(t, os.WriteFile(goodPath, []byte(good), 0o600))

	require.Equal(t,
		[]string{"9 fmt.Println", "10 os.Stdout", "11 os.Stderr", "12 time.Now", "13 time.Now"},
		forbiddenSelectors(t, badPath, commandsForbidden),
		"the scanner must see the Print* call, both stdio streams and time.Now with or without a call")
	require.Empty(t, forbiddenSelectors(t, goodPath, commandsForbidden),
		"fmt.Fprintf, fmt.Sprint and time's constants and methods must not be mistaken for the "+
			"forbidden shapes")
}

// forbiddenSelectors returns "<line> pkg.Name" for every selector expression in path whose package
// identifier and name match one of the pkg.Name (or pkg.Prefix*) patterns.
//
// It looks at selectors rather than calls on purpose: os.Stdout is a variable, and time.Now passed
// as a method value is as much a wall-clock dependency as time.Now().
func forbiddenSelectors(t *testing.T, path string, patterns []string) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing %s", path)

	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, isSel := n.(*ast.SelectorExpr)
		if !isSel {
			return true
		}
		pkg, isIdent := sel.X.(*ast.Ident)
		if !isIdent {
			return true
		}
		for _, pat := range patterns {
			wantPkg, wantName, ok := strings.Cut(pat, ".")
			require.True(t, ok, "pattern %q must be spelled pkg.Name", pat)
			if pkg.Name != wantPkg {
				continue
			}
			prefix, glob := strings.CutSuffix(wantName, "*")
			if (glob && strings.HasPrefix(sel.Sel.Name, prefix)) || (!glob && sel.Sel.Name == wantName) {
				hits = append(hits, strconv.Itoa(fset.Position(sel.Pos()).Line)+" "+pkg.Name+"."+sel.Sel.Name)
				break
			}
		}
		return true
	})
	return hits
}

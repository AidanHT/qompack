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

// Breakpoint placement is measurement, not a feature (docs/adr/0002-replay-methodology.md). The
// Messages API's cache_control markers are the host's to place; a plugin that reordered the prefix
// or sorted blocks by mutation rate to game them would be doing the one thing §12 says it must not
// — and internal/eval's BreakpointOPT exists only so replay can score what an optimal placement
// WOULD have been worth. SP-16's plan therefore required that none of the cache-ordering
// vocabulary appear in non-test source outside internal/eval; the V5-VERIFY inventory (I-16.14)
// found the property true by grep and enforced by nobody. This is the guard.
//
// The scan is over identifiers and string literals, not comments: test/replay/report.go names the
// concept in a comment beside its cacheControlMarkers budget, and a comment that says what the
// plugin does NOT do is exactly the kind of sentence the guard should leave alone. A name or a
// wire-format key is a different matter — that is code reaching for the mechanism.
var cacheOrderingIdentifiers = []string{
	"cache_control", "CacheBreakpoint", "ReorderPrefix", "MutationRateSort",
}

// cacheOrderingScanRoots are every tree that holds Go source: the shipped binary's, the test
// harnesses', and tools/, which could otherwise grow a generator that emits the vocabulary.
var cacheOrderingScanRoots = []string{"internal", "cmd", "test", "tools"}

// cacheOrderingHome is the one package permitted to spell these names.
const cacheOrderingHome = "internal/eval"

// TestGuard_CacheOrderingIdentifiersStayInsideEval is the enforcement itself.
func TestGuard_CacheOrderingIdentifiersStayInsideEval(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := nonTestGoFiles(t, root, cacheOrderingScanRoots, map[string]bool{cacheOrderingHome: true})
	require.NotEmpty(t, files, "the walk found no non-test Go files — the guard would vacuously pass")

	var offenders []string
	for _, p := range files {
		for _, hit := range cacheOrderingMentions(t, p) {
			rel, err := filepath.Rel(root, p)
			require.NoError(t, err)
			offenders = append(offenders, filepath.ToSlash(rel)+":"+hit)
		}
	}

	require.Empty(t, offenders,
		"these non-test files outside %s name one of %s: %s\n\n"+
			"Breakpoint placement is measurement-and-port material (ADR 0002, §5.6, §12): the "+
			"plugin does not place, move or reorder around cache_control markers, and the only "+
			"code allowed to reason about them is internal/eval's scorer. If a new consumer "+
			"genuinely needs the analysis, it reads eval's result; it does not grow its own.",
		cacheOrderingHome, strings.Join(cacheOrderingIdentifiers, ", "), strings.Join(offenders, ", "))
}

// TestGuard_CacheOrderingScannerSeesAMention is this guard's self-test, and it is not optional: a
// scanner that matched nothing would make the test above pass forever. It must see each name as an
// identifier and as a string literal, and must NOT see a comment or an unrelated identifier that
// merely shares a prefix.
func TestGuard_CacheOrderingScannerSeesAMention(t *testing.T) {
	t.Parallel()

	const bad = `package p

func CacheBreakpoint() {}

func reorder(ReorderPrefix int) {}

var key = "cache_control"

type MutationRateSortPlan struct{}
`
	const good = `package p

// cache_control markers and CacheBreakpoint plans are named here on purpose, in a comment.
const cacheControlMarkers = 4

func reorderNothing() {}
`
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.go")
	goodPath := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(badPath, []byte(bad), 0o600))
	require.NoError(t, os.WriteFile(goodPath, []byte(good), 0o600))

	require.Equal(t,
		[]string{"3 CacheBreakpoint", "5 ReorderPrefix", "7 cache_control", "9 MutationRateSort"},
		cacheOrderingMentions(t, badPath),
		"the scanner must see each name as an identifier and as a string literal")
	require.Empty(t, cacheOrderingMentions(t, goodPath),
		"a comment or a merely similar identifier must not be mistaken for the vocabulary")
}

// cacheOrderingMentions returns "<line> <name>" for every identifier or string literal in path
// that contains one of cacheOrderingIdentifiers.
//
// Substring rather than whole-name matching is deliberate: MutationRateSortPlan is as much a
// second implementation as MutationRateSort, and over-strict is the safe direction here — a
// spurious hit costs a rename, a miss costs the invariant.
func cacheOrderingMentions(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing %s", path)

	var hits []string
	record := func(pos token.Pos, text string) {
		for _, name := range cacheOrderingIdentifiers {
			if strings.Contains(text, name) {
				hits = append(hits, strconv.Itoa(fset.Position(pos).Line)+" "+name)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			record(x.Pos(), x.Name)
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				record(x.Pos(), x.Value)
			}
		}
		return true
	})
	return hits
}

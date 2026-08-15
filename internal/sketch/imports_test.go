package sketch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// internalPrefix is the import-path prefix this test polices. Standard-library and third-party
// imports are none of its business; only the in-repo edges are.
const internalPrefix = "github.com/qompack/qompack/internal/"

// TestImports_FoundationOnly is the local, in-package mirror of the 00-ARCHITECTURE.md §3.2
// import-graph check. tools/devtool's importgraph pass allows internal/sketch the whole foundation
// set; this subplan restricts itself further, to core, paths and logging, so that the package
// stays constructible from scalars alone. Failing here rather than only in CI is what stops
// someone reaching for internal/config (which would let Appendix C defaults drift into two places)
// or internal/store (which imports this package, so the edge would be a cycle).
//
// It inspects NON-test files only. internal/testutil imports internal/store, which imports this
// package, so an in-package _test.go can never import testutil — but io_test.go and golden_test.go
// are package sketch_test and legitimately do. Parsing test files here would fail them for
// obeying their own rule.
//
// The assertion is "subset of {core, paths, logging}" plus "core is present", not equality. paths
// and logging enter the non-test surface only when io.go lands (Save/Load/LoadWithLog/
// ReplaceGenerational). An equality assertion would therefore have to be written as a lie today
// and quietly relaxed later; a subset assertion is true at every commit in the subplan and still
// fails the instant a fourth internal package is imported.
func TestImports_FoundationOnly(t *testing.T) {
	allowed := map[string]bool{
		internalPrefix + "core":    true,
		internalPrefix + "paths":   true,
		internalPrefix + "logging": true,
	}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	seen := make(map[string][]string)
	nonTestFiles := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		nonTestFiles++
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, err, "parsing %s", name)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			if !strings.HasPrefix(path, internalPrefix) {
				continue
			}
			require.True(t, allowed[path],
				"%s imports %s; internal/sketch may import only core, paths and logging (§3.2)", name, path)
			seen[path] = append(seen[path], name)
		}
	}

	require.NotZero(t, nonTestFiles, "no non-test files found; the check would pass vacuously")
	require.NotEmpty(t, seen[internalPrefix+"core"],
		"no non-test file imports internal/core; the allow-list check would pass vacuously")
}

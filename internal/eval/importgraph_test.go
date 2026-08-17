package eval_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/qompack/qompack"

// foundation is 00-ARCHITECTURE.md §3.2's allow-set for eval: "foundation only". It is what lets
// L7 be built in the earliest parallel wave, against no sibling's output, and still supply a
// regression signal to every later phase. Anything eval needs from a later wave — store.Stats for
// the growth guardrail, negknow health for the bloom watch-for — is declared as a provider
// interface here and supplied by test/replay, which is a composition root and may import
// anything.
var foundation = map[string]bool{
	"core": true, "paths": true, "config": true, "logging": true, "obs": true,
}

// moduleRoot walks up from the test's working directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// directImports parses every non-test Go file of one in-module package and returns its imports.
//
// It reads the source directly rather than shelling out to `go list`, so this test needs no
// toolchain subprocess and stays inside the security job's os/exec allowlist (which covers
// internal/daemon, internal/cli and tools/, but not internal/eval). Build constraints are
// deliberately ignored: parsing every file on every platform over-approximates the import set,
// and an over-approximation can only make this rule stricter, never laxer.
func directImports(t *testing.T, root, pkg string) []string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath+"/")))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "package %s", pkg)

	fset := token.NewFileSet()
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestImportGraph_EvalIsFoundationOnly asserts eval's transitive import closure contains no
// internal package outside the foundation. It is the mechanical form of the architectural claim
// this whole package rests on, so it fails loudly rather than being discovered as an import cycle
// by a sibling in the same wave.
func TestImportGraph_EvalIsFoundationOnly(t *testing.T) {
	root := moduleRoot(t)
	const self = modulePath + "/internal/eval"

	visited := map[string]bool{}
	violations := map[string]string{} // offending package -> the package that imported it

	var walk func(pkg string)
	walk = func(pkg string) {
		if visited[pkg] {
			return
		}
		visited[pkg] = true
		for _, imp := range directImports(t, root, pkg) {
			if !strings.HasPrefix(imp, modulePath+"/") {
				continue // stdlib or a vendored third-party dependency
			}
			if rel, ok := strings.CutPrefix(imp, modulePath+"/internal/"); ok {
				top, _, _ := strings.Cut(rel, "/")
				if !foundation[top] && imp != self {
					violations[imp] = pkg
				}
			}
			walk(imp)
		}
	}
	walk(self)

	// A path-resolution bug would make the walk visit nothing and the assertion below vacuous, so
	// pin that the closure actually reached the foundation packages eval is known to import.
	for _, want := range []string{"/internal/core", "/internal/config", "/internal/paths"} {
		require.True(t, visited[modulePath+want],
			"the walk never reached %s, so the rule below was not actually checked", want)
	}

	require.Empty(t, violations,
		"internal/eval's transitive closure must contain no internal package outside %v "+
			"(00-ARCHITECTURE.md §3.2); each entry below is offender -> importer", keysOf(foundation))
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package observer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

func TestSketches_CMSFedWithPathAndTool(t *testing.T) {
	h := newHarness(t)

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))

	require.Equal(t, uint32(1), h.Touch.Estimate([]byte("p\x00src/a.ts")), "the file-touch key")
	require.Equal(t, uint32(1), h.Touch.Estimate([]byte("t\x00FileRead")), "the tool key")
}

func TestSketches_HLLCountsDistinctPaths(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "a.ts", "alpha\n"),
		readOf("toolu_2", "b.ts", "beta\n"),
		readOf("toolu_3", "a.ts", "alpha again\n"),
	)

	require.Equal(t, uint64(2), h.Explore.Cardinality(), "breadth of exploration counts DISTINCT paths")
}

func TestSketches_MisraGriesTopK(t *testing.T) {
	h := newHarness(t)

	for i := range 5 {
		h.drive(readOf(fmt.Sprintf("toolu_a%d", i), "a.ts", "alpha\n"))
	}
	for i := range 2 {
		h.drive(readOf(fmt.Sprintf("toolu_b%d", i), "b.ts", "beta\n"))
	}

	require.Equal(t, []sketch.Counted{{Key: "a.ts", Count: 5}}, h.Hot.Top(1))
}

func TestSketches_NoPathNoHLL(t *testing.T) {
	h := newHarness(t)

	h.drive(bashOf("toolu_1", "go build ./...", "ok\n"))

	require.Equal(t, uint64(0), h.Explore.Cardinality())
	require.Equal(t, uint32(1), h.Touch.Estimate([]byte("t\x00Bash")), "the tool is still counted")
}

func TestSketches_EphemeralNotFed(t *testing.T) {
	h := newHarness(t)

	h.drive(toolUse("toolu_1", "mcp__qompack__expand",
		`{"path":"src/a.ts"}`, `{"content":"expanded"}`))

	require.Zero(t, h.Touch.Total())
	require.Equal(t, uint64(0), h.Explore.Cardinality())
	require.Empty(t, h.Hot.Top(1))
}

func TestSketches_NilTolerated(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Touch, o.Explore, o.Hot = nil, nil, nil
	})

	require.NotPanics(t, func() { h.drive(readOf("toolu_1", "src/a.ts", "alpha\n")) })
}

// TestObserverNeverFeedsBloom is §8.1 item 5's prohibition: "Feed the Bloom filter ONLY on explicit
// negative-knowledge events (§8.3)", and those events are SP-09's.
//
// The plan's own phrasing is a store "whose sketch.Bloom is replaced by a panicking double". There
// is no such seam to replace — neither Options nor store.Store nor store.Deps carries a Bloom — and
// that absence is the STRONGER form of the same assertion, so it is what this asserts: no Options
// field is a Bloom in any spelling, and a full 50-event session completes without reaching a single
// method of the embedded store.Store interface, every one of which is nil and would panic.
func TestObserverNeverFeedsBloom(t *testing.T) {
	h := newHarness(t)

	optionsType := reflect.TypeOf(Options{})
	for i := range optionsType.NumField() {
		f := optionsType.Field(i)
		require.NotContains(t, f.Type.String(), "Bloom",
			"Options.%s is a Bloom seam; L0 may not have one", f.Name)
	}

	require.NotPanics(t, func() {
		for i := range 50 {
			switch i % 3 {
			case 0:
				h.drive(readOf(fmt.Sprintf("toolu_%d", i), fmt.Sprintf("src/f%d.ts", i%5), "alpha\n"))
			case 1:
				h.drive(bashOf(fmt.Sprintf("toolu_%d", i), "go test ./...", "ok  \tpkg\t0.1s\n"))
			default:
				h.drive(toolUse(fmt.Sprintf("toolu_%d", i), "Grep",
					`{"pattern":"x","path":"src"}`, `{"content":"src/f1.ts:1:x"}`))
			}
		}
	})
	require.Len(t, h.Store.Records, 50)
}

// observerSourceFiles parses every non-test .go file of this package.
func observerSourceFiles(t *testing.T) (*token.FileSet, []*ast.File, []string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var files []*ast.File
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		require.NoError(t, err, "parsing %s", name)
		files = append(files, f)
		names = append(names, name)
	}
	require.NotEmpty(t, files)
	return fset, files, names
}

// TestObserverSourceHasNoBloomReference greps the package's own AST — not its comments, which
// legitimately explain the prohibition — for the Bloom identifiers.
func TestObserverSourceHasNoBloomReference(t *testing.T) {
	_, files, names := observerSourceFiles(t)

	forbidden := map[string]bool{"Bloom": true, "NewBloom": true, "RebuildBloom": true}
	for i, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			require.False(t, forbidden[id.Name], "%s references the identifier %q", names[i], id.Name)
			return true
		})
	}
}

// TestObserverImportSetIsExact pins the exit criterion of the subplan: the REALIZED import set,
// not the §3.2 ceiling. devtool's import-graph check permits negknow and chunk, so this is the
// only place the two declines (resolved decision 1 and §8.1 item 5) are actually enforced.
func TestObserverImportSetIsExact(t *testing.T) {
	_, files, _ := observerSourceFiles(t)

	const prefix = "github.com/qompack/qompack/internal/"
	got := map[string]bool{}
	for _, f := range files {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			got[strings.TrimPrefix(path, prefix)] = true
		}
	}

	want := []string{
		"canon", "config", "core", "dag", "grammar", "hookio",
		"logging", "obs", "paths", "sketch", "store", "tokens",
	}
	have := make([]string, 0, len(got))
	for k := range got {
		have = append(have, k)
	}
	sort.Strings(have)
	require.Equal(t, want, have,
		"the realized set is exactly this — chunk and negknow are declined, and symbols, scheduler, "+
			"checkpoint and contract are what the seams stand in for")
}

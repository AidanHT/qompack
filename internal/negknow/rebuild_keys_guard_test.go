package negknow

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// callSite is one call in this package's non-test code: the function it sits in and its argument
// list as source text.
type callSite struct{ in, args string }

// packageFuncs parses this package's non-test files and returns every function body by name,
// methods under their bare method name, plus the call sites of the selector calls named in want.
func packageFuncs(t *testing.T, want ...string) (*token.FileSet, map[string]*ast.FuncDecl, map[string][]callSite) {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	funcs := map[string]*ast.FuncDecl{}
	sites := map[string][]callSite{}
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			funcs[fd.Name.Name] = fd
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				for _, w := range want {
					if sel.Sel.Name == w {
						args := make([]string, len(call.Args))
						for i, a := range call.Args {
							args[i] = render(t, fset, a)
						}
						sites[w] = append(sites[w], callSite{in: fd.Name.Name, args: strings.Join(args, ", ")})
					}
				}
				return true
			})
		}
	}
	for _, s := range sites {
		sort.Slice(s, func(i, j int) bool { return s[i].in+s[i].args < s[j].in+s[j].args })
	}
	return fset, funcs, sites
}

func render(t *testing.T, fset *token.FileSet, n ast.Node) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, printer.Fprint(&b, fset, n))
	return b.String()
}

// TestRebuildWith_KeysOnlyFromOpensReindex is the §3.3 guard for the one rebuild input that does
// not come from the records at the moment of the rebuild: the pre-derived keys Open hands over.
// rebuildWith trusts a keys slice of the right length (checking it would re-derive every digest),
// so what keeps a wrong one out is the call graph, and this pins it:
//
//   - every rebuild but Open's passes nil and derives its own keys from visibleActive's records;
//   - Open's keys come from loadRecords, whose only non-nil answer is reindex() over the records
//     it has just materialized;
//   - between the two, Open runs only acquireBloom, which never touches the records; and
//   - reconcileBloom passes its keys through untouched.
//
// TestRebuildWith_DerivedKeysMatchFreshOnes shows those keys build the same filter bit for bit;
// this test is what stops a second, differently sourced caller from arriving unnoticed.
func TestRebuildWith_KeysOnlyFromOpensReindex(t *testing.T) {
	fset, funcs, sites := packageFuncs(t, "rebuildWith", "reconcileBloom", "reindex", "loadRecords")

	require.Equal(t, []callSite{
		{in: "rebuildLocked", args: "ctx, nil"},
		{in: "reconcileBloom", args: "context.Background(), keys"},
		{in: "reconcileBloom", args: "context.Background(), keys"},
	}, sites["rebuildWith"], "only reconcileBloom may pass pre-derived keys to the rebuild")
	require.Equal(t, []callSite{{in: "Open", args: "loadFailed, keys"}}, sites["reconcileBloom"])
	require.Equal(t, []callSite{{in: "Open"}}, sites["loadRecords"])
	require.Equal(t, []callSite{{in: "loadRecords"}}, sites["reindex"])

	// Open: the keys are loadRecords' answer, and the only step between it and the rebuild is the
	// filter acquisition.
	open := funcs["Open"]
	require.NotNil(t, open)
	var stmts []string
	for _, s := range open.Body.List {
		stmts = append(stmts, render(t, fset, s))
	}
	want := []string{"keys := l.loadRecords()", "loadFailed := l.acquireBloom(b)", "l.reconcileBloom(loadFailed, keys)"}
	at := -1
	for i := range stmts {
		if stmts[i] == want[0] {
			at = i
		}
	}
	require.GreaterOrEqual(t, at, 0, "Open must take its keys from loadRecords")
	require.Less(t, at+len(want)-1, len(stmts))
	require.Equal(t, want, stmts[at:at+len(want)], "nothing may run between reindex and the rebuild but acquireBloom")

	// loadRecords answers nil or reindex's keys, and nothing else.
	ast.Inspect(funcs["loadRecords"].Body, func(n ast.Node) bool {
		if r, ok := n.(*ast.ReturnStmt); ok {
			require.Len(t, r.Results, 1)
			got := render(t, fset, r.Results[0])
			require.Contains(t, []string{"nil", "l.reindex()"}, got, "loadRecords returned %s", got)
		}
		return true
	})

	// acquireBloom and what it calls touch the filter only, never the records the keys describe;
	// reconcileBloom and Open never reassign the keys.
	for _, fn := range []string{"acquireBloom", "newConfiguredBloom"} {
		ast.Inspect(funcs[fn].Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				require.NotEqual(t, "recs", sel.Sel.Name, "%s must not touch the records", fn)
			}
			return true
		})
	}
	for _, fn := range []string{"reconcileBloom", "Open"} {
		ast.Inspect(funcs[fn].Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range a.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "keys" {
						require.Equal(t, "Open", fn, "%s reassigns keys", fn)
						require.Equal(t, token.DEFINE, a.Tok, "Open defines keys once and never reassigns them")
					}
				}
			}
			return true
		})
	}
}

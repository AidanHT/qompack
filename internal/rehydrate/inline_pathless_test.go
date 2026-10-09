package rehydrate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInlinePathless_IsASubsetOfExpandsPathlessProducers keeps inlinePathless from drifting away
// from internal/mcp's authorizeOrigin: every pathless producer whose result is inlined must be one
// expand accepts, so the block never inlines what expand would refuse. internal/rehydrate may not
// import internal/mcp's unexported rule, so this reads it from the source: the string literals of the
// switch case in authorizeOrigin that returns nil.
func TestInlinePathless_IsASubsetOfExpandsPathlessProducers(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "mcp", "authorize.go"), nil, 0)
	require.NoError(t, err)

	accepted := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "authorizeOrigin" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok || len(cc.Body) != 1 {
				return true
			}
			ret, ok := cc.Body[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				return true
			}
			if id, ok := ret.Results[0].(*ast.Ident); !ok || id.Name != "nil" {
				return true
			}
			found = true
			for _, e := range cc.List {
				ast.Inspect(e, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, err := strconv.Unquote(lit.Value)
						require.NoError(t, err)
						accepted[s] = true
					}
					return true
				})
			}
			return false
		})
		return false
	})
	require.True(t, found, "authorizeOrigin's accepting case was not found: its shape changed, update this test")
	for tool := range inlinePathless {
		require.Truef(t, accepted[tool], "inlinePathless holds %q, which expand's authorizeOrigin no longer accepts", tool)
	}
}

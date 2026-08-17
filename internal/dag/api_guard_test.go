package dag

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoBooleanKeepAPI is the mechanical form of doc.go's NO SELECTION AUTHORITY note.
//
// Qompack.md closing note 3 forbids shipping slicing or submodular selection before p-selection, and
// 00-ARCHITECTURE.md §5.12 closes that path in analyzer.NewSelector, which refuses to construct
// unless scheduler.PSelectionAvailable() reports true. This package is the one most likely to
// re-open it by accident: it already computes, per node, exactly the number a keep/drop decision
// would be thresholded from. The note says the package exposes SCORES and nothing else; this test is
// what makes that a build failure rather than a convention.
//
// It parses the package rather than grepping it, deliberately. A grep over source text cannot tell
// the identifier `KeepAfter` from the word "keep" in a comment, cannot see through a type alias, and
// silently stops working the moment someone reformats a signature across two lines. go/parser sees
// the declarations themselves.

// noSelectionAuthorityNote is the literal doc.go must keep. Deleting the note is deleting the
// package's own statement of the constraint, so it fails here too.
const noSelectionAuthorityNote = "NO SELECTION AUTHORITY"

// forbiddenResultTypes are the rendered result types that ARE a selection decision rather than a
// score. Each maps a node (or an index over nodes) to a yes/no, which is precisely the keep-set
// §5.12 reserves for analyzer behind the p-selection gate.
//
// The dag-qualified spelling is listed alongside the bare one because this guard is written to keep
// working if it is ever pointed at a package that imports dag rather than at dag itself.
var forbiddenResultTypes = map[string]bool{
	"map[NodeID]bool":     true,
	"map[dag.NodeID]bool": true,
	"map[string]bool":     true,
	"[]bool":              true,
}

// forbiddenIdentifier matches an exported name that CLAIMS selection authority whatever it returns.
//
// It is anchored where anchoring is what makes it precise. `^keep` and `^drop` catch KeepAfter and
// DropStale while leaving `Bookkeeping`-style names alone; `keepset`, `dropset` and `evict` are
// unanchored because they are unambiguous wherever they appear in a name.
//
// The scope is deliberately narrow in the other direction too: this must NOT flag ParseNodeID,
// ParseNodeKind or ParseEdgeKind, each of which returns a bare `ok bool`. That bool reports whether
// a string PARSED, which is a validity answer about one argument, not a decision about a node's
// place in the context. A blanket "no exported function may return a bool" rule would be wrong, and
// would push the parse helpers into returning errors nobody wants to check.
var forbiddenIdentifier = regexp.MustCompile(`(?i)keepset|dropset|^keep|^drop|evict`)

// parseGuardPackage parses this package's non-test files. _test.go files are excluded because a
// test may legitimately build a map[NodeID]bool to express an expectation about a slice — that is a
// test's own bookkeeping, not API this package hands anybody.
func parseGuardPackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments|parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Len(t, pkgs, 1, "internal/dag must hold exactly one non-test package")

	var files []*ast.File
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			files = append(files, f)
		}
	}
	require.NotEmpty(t, files)
	return fset, files
}

// renderType renders one type expression the way it is written in the source, so a result type can
// be compared against forbiddenResultTypes as text without reimplementing Go's type syntax.
func renderType(t *testing.T, fset *token.FileSet, expr ast.Expr) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, printer.Fprint(&b, fset, expr))
	return b.String()
}

// TestNoBooleanKeepAPI fails if any exported declaration in this package returns a keep-set or a
// drop list, or names itself as one.
func TestNoBooleanKeepAPI(t *testing.T) {
	fset, files := parseGuardPackage(t)

	checkName := func(kind, name string) {
		t.Helper()
		require.Falsef(t, forbiddenIdentifier.MatchString(name),
			"exported %s %q claims selection authority: this package exposes relevance SCORES only "+
				"(doc.go's %s note; Qompack.md closing note 3)", kind, name, noSelectionAuthorityNote)
	}

	// funcs counts what the walk actually inspected, so a future refactor that quietly stops finding
	// any declaration at all fails here instead of passing vacuously.
	funcs := 0
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Every exported func NAME is checked, including methods on the unexported concrete
				// graph. "Exported type" is the wrong boundary in this package: Open returns an
				// interface backed by an unexported struct, and SP-05 and SP-12 already reach that
				// struct's extra methods by type-asserting to dag.Maintainer. An exported method on
				// it is exported API in every sense that matters here.
				if !d.Name.IsExported() {
					continue
				}
				funcs++
				checkName("func", d.Name.Name)
				if d.Type.Results == nil {
					continue
				}
				for _, result := range d.Type.Results.List {
					rendered := renderType(t, fset, result.Type)
					require.Falsef(t, forbiddenResultTypes[rendered],
						"exported func %q returns %s, which is a keep-set rather than a score "+
							"(doc.go's %s note; 00-ARCHITECTURE.md §5.12)",
						d.Name.Name, rendered, noSelectionAuthorityNote)
				}
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					checkName("type", ts.Name.Name)
					st, ok := ts.Type.(*ast.StructType)
					if !ok || st.Fields == nil {
						continue
					}
					for _, field := range st.Fields.List {
						for _, name := range field.Names {
							if name.IsExported() {
								checkName("field of "+ts.Name.Name, name.Name)
							}
						}
					}
				}
			}
		}
	}
	require.NotZero(t, funcs, "the guard must actually have inspected exported declarations")

	// The two shapes the guard is calibrated against, asserted directly so the calibration itself is
	// visible: the real accessors pass, and the hypothetical keep-set API fails both checks.
	require.False(t, forbiddenIdentifier.MatchString("NodesAfter"))
	require.False(t, forbiddenIdentifier.MatchString("CrossingEdges"))
	require.False(t, forbiddenIdentifier.MatchString("ParseNodeID"))
	require.False(t, forbiddenResultTypes["[]Node"])
	require.False(t, forbiddenResultTypes["int"])
	require.False(t, forbiddenResultTypes["bool"], "a bare ok bool reports parse validity, not a selection decision")
	require.True(t, forbiddenIdentifier.MatchString("KeepAfter"))
	require.True(t, forbiddenResultTypes["map[NodeID]bool"])
}

// TestNoSelectionAuthorityNoteSurvives asserts doc.go still carries the note this guard mechanizes.
// The two halves are load-bearing together: the parse guard without the note leaves nobody able to
// say WHY the shapes are forbidden, and the note without the parse guard is a comment.
func TestNoSelectionAuthorityNoteSurvives(t *testing.T) {
	_, files := parseGuardPackage(t)

	found := false
	for _, f := range files {
		if f.Doc != nil && strings.Contains(f.Doc.Text(), noSelectionAuthorityNote) {
			found = true
			break
		}
	}
	require.Truef(t, found,
		"the package comment must still carry the %q note (doc.go; Qompack.md closing note 3)",
		noSelectionAuthorityNote)
}

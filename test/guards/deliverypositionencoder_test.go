package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The v1 delivery position has ONE encoder, and both create sites go through it (SP20-D1 design §5).
//
// internal/daemon writes the v1 position sidecar from three places: the downgrade and the offline
// tool's conversion, both through writeDeliveryPositionV1, and the two opens that CREATE an empty
// one for a project's first use. The two creates used to marshal the record inline and discard the
// marshal error; extracting createEmptyDeliveryPositionV1 bought the error and a single expression,
// not a change of value — the bytes were, and had to be, identical.
//
// That is exactly why no value-based test can see the difference. deliveryPosition carries no
// omitempty, so an inline json.Marshal of the same four fields and a call to the helper produce the
// same bytes, byte for byte; every assertion about what is on disk passes either way. The carry
// stage's report concluded from that that "no test anywhere can separate them", and that conclusion
// is wrong in this repository: test/guards exists to pin source-level properties no value test can
// see, with sharedreaders_test.go and selectorbypass_test.go as the go/parser precedent and the
// stated rationale — the guard's job is only to stop the answer silently reverting afterwards.
//
// So this is that guard, in two halves. Each create site must call the helper, and no production
// function but encodeDeliveryPositionV1 may marshal a deliveryPosition at all. The second half is
// what makes the first mean something: without it a site could keep the call and gain an inline
// marshal beside it.

// deliveryPositionCreateSites are the production functions that CREATE the empty v1 sidecar.
//
// Both are opens, and they must create-both-or-neither with their journals, which is why the row is
// the function rather than the file: a create moved into a helper of its own would need this list
// revisited, not silently satisfied by the file still containing the string somewhere.
var deliveryPositionCreateSites = []struct {
	file string // module-relative, slash-separated
	fn   string // FuncDecl name; the receiver, if any, is ignored
	why  string
}{
	{
		file: "internal/daemon/delivery_lease.go",
		fn:   "openDeliveryJournal",
		why:  "O1, the lease journal's own empty position, seeded with deliveryChainSeed",
	},
	{
		file: "internal/daemon/delivery_lease.go",
		fn:   "openAckLocked",
		why:  "the acknowledgement journal's, seeded with deliveryAckChainSeed",
	},
}

// deliveryPositionEncoder is the one production function allowed to marshal a deliveryPosition.
const deliveryPositionEncoder = "encodeDeliveryPositionV1"

// TestGuard_TheV1DeliveryPositionHasOneEncoder pins both halves of design §5's "one v1 encoder".
func TestGuard_TheV1DeliveryPositionHasOneEncoder(t *testing.T) {
	root := repoRoot(t)

	for _, site := range deliveryPositionCreateSites {
		t.Run(site.file+":"+site.fn, func(t *testing.T) {
			fn := findFuncDecl(t, filepath.Join(root, filepath.FromSlash(site.file)), site.fn)

			require.True(t, callsFunction(fn, "createEmptyDeliveryPositionV1"),
				"%s no longer creates its empty v1 position through createEmptyDeliveryPositionV1 (%s).\n\n"+
					"An inline json.Marshal of the same deliveryPosition produces the same bytes, so no "+
					"assertion about the file can tell the two apart — and the inline form is the one "+
					"that discards the marshal error. If this open genuinely stopped creating a position, "+
					"delete its row from deliveryPositionCreateSites in the same change rather than "+
					"leaving a guard that checks nothing.", site.fn, site.why)
		})
	}

	t.Run("only the encoder marshals a deliveryPosition", func(t *testing.T) {
		got := functionsMarshallingADeliveryPosition(t, filepath.Join(root, "internal", "daemon"))

		require.Equal(t, []string{deliveryPositionEncoder}, got,
			"a production function outside %s marshals a deliveryPosition. That is a second producer "+
				"of v1 bytes, which is the shape design §5 removed: the two spellings agree today and "+
				"nothing makes them keep agreeing. Route it through %s (or, for a v2 record, through "+
				"encodeSlot).", deliveryPositionEncoder, deliveryPositionEncoder)
	})
}

// TestGuard_TheDeliveryPositionScannerSeesBothShapes is this guard's self-test, and it is not
// optional: a scanner that matched nothing would make both halves above pass forever. It is pointed
// at a fixture that does each thing once and must report each.
func TestGuard_TheDeliveryPositionScannerSeesBothShapes(t *testing.T) {
	const src = `package daemon

import "encoding/json"

func encodeDeliveryPositionV1(size int64) ([]byte, error) {
	return json.Marshal(deliveryPosition{Bytes: size})
}

func somethingElse(p string) error {
	b, err := json.Marshal(&deliveryPosition{})
	if err != nil {
		return err
	}
	_ = b
	return createEmptyDeliveryPositionV1(p, seed)
}

func marshalsSomethingUnrelated() ([]byte, error) {
	return json.Marshal(sealRecord{})
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	require.Equal(t, []string{"encodeDeliveryPositionV1", "somethingElse"}, marshalSitesIn(f),
		"the scanner must see the plain literal AND the pointer-to-literal form, and must not "+
			"report a function that marshals something else")

	site := funcDeclNamed(f, "somethingElse")
	require.NotNil(t, site)
	require.True(t, callsFunction(site, "createEmptyDeliveryPositionV1"),
		"the call scan must recognise a plain call, or every create-site row is vacuous")
	require.False(t, callsFunction(site, "writeDeliveryPositionV1"),
		"and it must not report a call that is not there")
}

// functionsMarshallingADeliveryPosition returns, sorted and de-duplicated, the names of the
// functions in dir's PRODUCTION files that pass a deliveryPosition composite literal to
// json.Marshal. Test files are excluded: a fixture building a position by hand is how several of
// these tests state what a v1 sidecar is, and pinning them would be pinning the wrong thing.
func functionsMarshallingADeliveryPosition(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading %s", dir)

	seen := map[string]bool{}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, perr, "parsing %s", name)
		scanned++
		for _, fn := range marshalSitesIn(f) {
			seen[fn] = true
		}
	}
	require.NotZero(t, scanned, "the scan found no production files in %s, so it proves nothing", dir)

	return sortedKeys(seen)
}

// marshalSitesIn returns the names of f's functions that marshal a deliveryPosition, in source
// order, de-duplicated. Both `deliveryPosition{…}` and `&deliveryPosition{…}` count, and nested
// function literals belong to the function that encloses them.
func marshalSitesIn(f *ast.File) []string {
	var out []string
	seen := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall || !isSelectorCall(call, "json", "Marshal") {
				return true
			}
			for _, arg := range call.Args {
				if isCompositeLitOfType(arg, "deliveryPosition") && !seen[fn.Name.Name] {
					seen[fn.Name.Name] = true
					out = append(out, fn.Name.Name)
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

// isSelectorCall reports whether call is pkg.fn(…), matched on the selector's spelling alone —
// the same name-only resolution every other AST guard here uses, and over-strict in the safe
// direction.
func isSelectorCall(call *ast.CallExpr, pkg, fn string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != fn {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// isCompositeLitOfType reports whether e is `name{…}` or `&name{…}`.
func isCompositeLitOfType(e ast.Expr, name string) bool {
	if unary, ok := e.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		e = unary.X
	}
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return false
	}
	ident, ok := lit.Type.(*ast.Ident)
	return ok && ident.Name == name
}

// callsFunction reports whether fn's body calls the plain (non-selector) function name, function
// literals included.
func callsFunction(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

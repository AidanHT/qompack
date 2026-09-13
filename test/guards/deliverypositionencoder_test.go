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
// So this is that guard, in two halves. Each create site must call the helper, and every production
// function in internal/daemon that hands a deliveryPosition to json.Marshal must be one this file
// NAMES, with the reason it may. The second half is what makes the first mean something: without it
// a site could keep the call and gain a marshal of its own beside it — inline, or through a local,
// which is why the scan follows a name this function ties to the type and not only a literal
// sitting in the argument.
//
// That tie is the limit of the claim, and it is name-only, the same resolution every other AST
// guard here uses: a composite literal in the argument, or an identifier the same function binds to
// one — a local or a var bound to the literal, a var declared of the type, a parameter or a named
// result of it — in the value spelling or the pointer one. A position that arrives from a call, a
// struct field or a container is NOT covered; a wider claim would be one this file cannot keep.

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

// deliveryPositionEncoder is the one production function that PRODUCES v1 sidecar bytes.
const deliveryPositionEncoder = "encodeDeliveryPositionV1"

// deliveryPositionType is the record design §5 gave one producer.
const deliveryPositionType = "deliveryPosition"

// deliveryPositionMarshalSites are the production functions in internal/daemon that pass a
// deliveryPosition to json.Marshal, each with why it may.
//
// One of the two produces v1 bytes and the other produces none, and that distinction is the whole
// content of a row: a function that marshals a position and is not here fails the second half,
// because an exemption nobody stated would be indistinguishable from a site that was forgotten —
// the rule sealresidualreport_test.go's releasesWithNoSealResidual follows, for the same reason.
var deliveryPositionMarshalSites = []struct {
	fn  string // FuncDecl name in internal/daemon
	why string
}{
	{
		fn: deliveryPositionEncoder,
		why: "design §5's one encoder: every v1 sidecar byte any build writes — the two creates', " +
			"the downgrade's, the offline tool's conversion's — is this call's output",
	},
	{
		fn: "loadDeliveryPosition",
		why: "the strict v1 reader's canonicality check, and no producer of bytes at all: it " +
			"re-encodes the record it has just DECODED and compares that with the bytes it read, " +
			"refusing a sidecar that is not in canonical form, and what it marshals is written " +
			"nowhere. This row is also why the scan follows a name — the position here is a var, " +
			"invisible to a literal-only scan, and a second WRITER in that same shape would have " +
			"been invisible too",
	},
}

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

	t.Run("every deliveryPosition marshal site is named here", func(t *testing.T) {
		var want []string
		for _, site := range deliveryPositionMarshalSites {
			want = append(want, site.fn)
		}
		sort.Strings(want)

		got := functionsMarshallingADeliveryPosition(t, filepath.Join(root, "internal", "daemon"))

		require.Equal(t, want, got,
			"a production function in internal/daemon marshals a deliveryPosition and is in no row of "+
				"deliveryPositionMarshalSites. If it produces v1 sidecar bytes, that is a second "+
				"producer of them — the shape design §5 removed, where the spellings agree today and "+
				"nothing makes them keep agreeing — so route it through %s (or, for a v2 record, "+
				"through encodeSlot). If it marshals a position for some other purpose, add it to that "+
				"list with the reason, the way loadDeliveryPosition's canonicality check is.",
			deliveryPositionEncoder)
	})
}

// TestGuard_TheDeliveryPositionScannerSeesEveryShapeItClaims is this guard's self-test, and it is
// not optional: a scanner that matched nothing would make both halves above pass forever. It is
// pointed at a fixture that writes each shape the header claims exactly once, and must report each
// — and must report neither of the two shapes that only look like one.
func TestGuard_TheDeliveryPositionScannerSeesEveryShapeItClaims(t *testing.T) {
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

func marshalsThroughALocal(size int64) ([]byte, error) {
	pos := deliveryPosition{Bytes: size}
	return json.Marshal(pos)
}

func marshalsThroughAVar(encoded []byte) ([]byte, error) {
	var decoded deliveryPosition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(&decoded)
}

func marshalsItsParameter(pos deliveryPosition) ([]byte, error) {
	return json.Marshal(pos)
}

func marshalsItsNamedResult(encoded []byte) (pos deliveryPosition, err error) {
	if err = json.Unmarshal(encoded, &pos); err != nil {
		return pos, err
	}
	_, err = json.Marshal(pos)
	return pos, err
}

func marshalsThroughAPointerParameter(pos *deliveryPosition) ([]byte, error) {
	return json.Marshal(*pos)
}

func marshalsAVarInitialisedFromTheLiteral(size int64) ([]byte, error) {
	var pos = deliveryPosition{Bytes: size}
	return json.Marshal(pos)
}

func marshalsSomethingUnrelated() ([]byte, error) {
	return json.Marshal(sealRecord{})
}

func marshalsAnUnrelatedLocal() ([]byte, error) {
	rec := sealRecord{}
	return json.Marshal(rec)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	require.Equal(t, []string{
		"encodeDeliveryPositionV1",              // the literal in the argument
		"marshalsAVarInitialisedFromTheLiteral", // a var whose VALUE is the literal
		"marshalsItsNamedResult",                // a named result of the type
		"marshalsItsParameter",                  // a parameter of the type
		"marshalsThroughALocal",                 // a local assigned the literal, marshalled by value
		"marshalsThroughAPointerParameter",      // a *deliveryPosition parameter, dereferenced
		"marshalsThroughAVar",                   // a var of the type, marshalled by address
		"somethingElse",                         // the pointer-to-literal in the argument
	}, marshalSitesIn(f),
		"the scanner must see the literal, the pointer-to-literal, and every name the same function "+
			"ties to the type — a local, a var declared of it, a var initialised from the literal, a "+
			"parameter and a named result, in the value spelling and the pointer one — because a site "+
			"that gained a second producer would spell it whichever way read best, and must report "+
			"neither function that marshals something else, or the name-only tie would be catching "+
			"the call rather than the type")

	site := funcDeclNamed(f, "somethingElse")
	require.NotNil(t, site)
	require.True(t, callsFunction(site, "createEmptyDeliveryPositionV1"),
		"the call scan must recognise a plain call, or every create-site row is vacuous")
	require.False(t, callsFunction(site, "writeDeliveryPositionV1"),
		"and it must not report a call that is not there")
}

// functionsMarshallingADeliveryPosition returns, sorted and de-duplicated, the names of the
// functions in dir's PRODUCTION files that pass a deliveryPosition to json.Marshal — as a composite
// literal, or through a name the same function ties to the type (marshalSitesIn has the exact
// rule). Test files are excluded: a fixture building a position by hand is how several of these
// tests state what a v1 sidecar is, and pinning them would be pinning the wrong thing.
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

// marshalSitesIn returns the names of f's functions that marshal a deliveryPosition, sorted and
// de-duplicated. Nested function literals belong to the function that encloses them.
func marshalSitesIn(f *ast.File) []string {
	var out []string
	seen := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		if seen[fn.Name.Name] || !marshalsADeliveryPosition(fn) {
			continue
		}
		seen[fn.Name.Name] = true
		out = append(out, fn.Name.Name)
	}
	sort.Strings(out)
	return out
}

// marshalsADeliveryPosition reports whether fn hands a deliveryPosition to json.Marshal, as a
// composite literal in the argument or through a name fn ties to the type. Following the name is
// what makes the second half cover the realistic revert: a site that wanted its own v1 bytes back
// would write `pos := deliveryPosition{…}; json.Marshal(pos)` as readily as the one-liner, and a
// literal-only scan sees nothing in the first spelling.
func marshalsADeliveryPosition(fn *ast.FuncDecl) bool {
	named := deliveryPositionNamesIn(fn)
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall || !isSelectorCall(call, "json", "Marshal") {
			return true
		}
		for _, arg := range call.Args {
			if isCompositeLitOfType(arg, deliveryPositionType) || named[identNameOf(arg)] {
				found = true
			}
		}
		return true
	})
	return found
}

// deliveryPositionNamesIn returns the identifiers fn ties to deliveryPosition: its parameters and
// named results of that type, the vars declared with it, and the locals assigned one of its
// composite literals. The pointer spellings are the same record and count as it; `_` never does.
//
// The scan is deliberately flow-insensitive, which is over-strict in the safe direction: a name
// bound on one branch counts on every branch, so the worst this can do is ask for a row in
// deliveryPositionMarshalSites that a human then reads and states the reason for.
func deliveryPositionNamesIn(fn *ast.FuncDecl) map[string]bool {
	named := map[string]bool{}
	add := func(id *ast.Ident) {
		if id != nil && id.Name != "_" {
			named[id.Name] = true
		}
	}
	for _, list := range []*ast.FieldList{fn.Type.Params, fn.Type.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			if !isTypeNamed(field.Type, deliveryPositionType) {
				continue
			}
			for _, id := range field.Names {
				add(id)
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, rhs := range node.Rhs {
				if !isCompositeLitOfType(rhs, deliveryPositionType) {
					continue
				}
				if id, isIdent := node.Lhs[i].(*ast.Ident); isIdent {
					add(id)
				}
			}
		case *ast.ValueSpec:
			if isTypeNamed(node.Type, deliveryPositionType) {
				for _, id := range node.Names {
					add(id)
				}
				return true
			}
			if len(node.Names) != len(node.Values) {
				return true
			}
			for i, value := range node.Values {
				if isCompositeLitOfType(value, deliveryPositionType) {
					add(node.Names[i])
				}
			}
		}
		return true
	})
	return named
}

// isTypeNamed reports whether e is the type name or a pointer to it.
func isTypeNamed(e ast.Expr, name string) bool {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == name
}

// identNameOf returns the identifier e names, through a leading & or *, or "" if e names none.
func identNameOf(e ast.Expr) string {
	switch node := e.(type) {
	case *ast.UnaryExpr:
		if node.Op == token.AND {
			return identNameOf(node.X)
		}
	case *ast.StarExpr:
		return identNameOf(node.X)
	case *ast.Ident:
		return node.Name
	}
	return ""
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

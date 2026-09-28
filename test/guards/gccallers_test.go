package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gcCaller names one product function that starts a store GC pass, and why it may.
//
// A store runs one GC pass at a time (internal/store/gcgate.go), and that gate orders the passes of
// one store handle only. It is enough because every pass runs inside the daemon, which holds the
// project's daemon lock and opens one writable store per project; that is why the V6 close-out
// (w6-gcserial) added no cross-process GC lock. A GC call anywhere else — a `qompack gc` command, a
// repair that collects — would run beside the daemon's passes unless it first took the daemon lock,
// or would need a store-level lock of its own. This inventory makes that decision due the moment
// such a call is written.
type gcCaller struct {
	file string // module-relative, slash-separated
	fn   string // FuncDecl name; the receiver, if any, is ignored
	why  string // why a pass started here is ordered with every other pass on the project
}

var gcCallers = []gcCaller{
	{
		file: "internal/observer/session.go",
		fn:   "onSessionEnd",
		why: "SessionEnd's §8.2 pass. The observer runs only inside the daemon (daemon.WireObserver is " +
			"its one constructor in product code), on the daemon's one store",
	},
	{
		file: "internal/daemon/scheduler_idle.go",
		fn:   "gcTask",
		why:  "the idle scheduler's pass, inside the daemon, on the same store",
	},
	{
		file: "internal/store/storetest/suite.go",
		fn:   "RunStoreSuite",
		why:  "the store conformance suite, which only tests run, on a store the test opened itself",
	},
}

// TestGuard_StoreGCRunsOnlyInsideTheDaemon keeps gcCallers exact: every product reference to a
// method named GC (other than runtime.GC) must be a row, and every row must still make one.
func TestGuard_StoreGCRunsOnlyInsideTheDaemon(t *testing.T) {
	root := repoRoot(t)
	found, files := scanGCCalls(t, root)
	require.GreaterOrEqual(t, files, minProductFiles, "the scan parsed too few product files to prove anything")

	listed := map[string]bool{}
	for _, c := range gcCallers {
		key := c.file + ":" + c.fn
		require.False(t, listed[key], "gcCallers names %s twice", key)
		require.NotEmpty(t, strings.TrimSpace(c.why), "gcCallers row %s gives no reason", key)
		listed[key] = true
	}
	var unlisted, stale []string
	for key := range found {
		if !listed[key] {
			unlisted = append(unlisted, key)
		}
	}
	for key := range listed {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)
	require.Empty(t, unlisted,
		"these product functions start a store GC pass outside the inventory. The store's gate "+
			"(internal/store/gcgate.go) orders the passes of one handle only, which is enough solely "+
			"because every pass runs in the daemon. A pass started anywhere else must first take the "+
			"daemon lock (daemon.AcquireLock), or the project needs a store-level GC lock that cannot "+
			"deadlock with the daemon's writer lease; then add a gcCallers row saying which")
	require.Empty(t, stale,
		"these gcCallers rows name a function that no longer calls GC (or no longer exists); delete "+
			"the row in the same change, or the inventory stops being exact")
}

// TestGuard_GCCallScannerSeesEveryShape is the scan's own self-test.
func TestGuard_GCCallScannerSeesEveryShape(t *testing.T) {
	const src = `package p

import "runtime"

var pass = st.GC

func direct() { _, _ = st.GC(ctx, p) }

func (x *T) method() { _, _ = x.store.GC(ctx, p) }

func closure() func() { return func() { _, _ = st.GC(ctx, p) } }

func notAStorePass() { runtime.GC() }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	got := map[string]bool{}
	gcCallsInFile(f, "sample.go", got)
	require.Equal(t, map[string]bool{
		"sample.go:" + packageScope: true,
		"sample.go:direct":          true,
		"sample.go:method":          true,
		"sample.go:closure":         true,
	}, got, "the scan must see a method value, a direct call, a method's call and a closure's call, "+
		"and must not count runtime.GC")
}

// scanGCCalls parses every non-test Go file under productReadRoots and returns the "file:fn" keys of
// the functions that reference a method named GC, with the number of files parsed.
func scanGCCalls(t *testing.T, root string) (map[string]bool, int) {
	t.Helper()
	found := map[string]bool{}
	files := 0
	for _, top := range productReadRoots {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			files++
			gcCallsInFile(f, filepath.ToSlash(rel), found)
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}
	return found, files
}

// gcCallsInFile adds to into the key of every declaration in f that references a selector named GC
// on anything but the runtime package, called or not.
func gcCallsInFile(f *ast.File, rel string, into map[string]bool) {
	for _, decl := range f.Decls {
		key := rel + ":" + packageScope
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil {
			key = rel + ":" + fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "GC" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "runtime" {
				return true
			}
			into[key] = true
			return true
		})
	}
}

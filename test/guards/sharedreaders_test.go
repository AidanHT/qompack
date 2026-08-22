package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sharedReader names one production function that MUST take its handle through
// paths.ReadFileShared / paths.OpenShared rather than os.ReadFile / os.Open.
//
// The list is an inventory, not a pattern: "reads a file some other process replaces or deletes
// while this one is live" is a judgement about the file, and there is no syntax that carries it.
// Adding a row is the deliberate act of making that judgement; the guard's job is only to stop the
// answer silently reverting afterwards.
type sharedReader struct {
	file  string // module-relative, slash-separated
	fn    string // FuncDecl name; the receiver, if any, is ignored
	why   string // the writer this reader could stall, named so a failure explains itself
	holds string // the file it reads
}

// sharedReaders is that inventory. Every row is a reader whose Windows handle, taken the ordinary
// way, would block the writer named in why.
//
// The mechanism is one fact with two faces (internal/paths/shared.go, and
// TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader for the measured behaviour): Go's os.Open and
// os.ReadFile take a handle with FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE, and
// while such a handle is open another process's os.Remove of that file fails with
// ERROR_SHARING_VIOLATION and paths.WriteAtomic's finishing replace of it fails too. A reader is
// not passive on Windows; it can stall the writer it is watching.
var sharedReaders = []sharedReader{
	{
		file:  "internal/daemon/lock.go",
		fn:    "readLockFile",
		holds: "run/daemon.lock",
		why:   "Lock.Release's os.Remove, the last act of a daemon shutdown, and removeLockFiles' reclaim of a stale lock",
	},
	{
		file:  "internal/ipc/client.go",
		fn:    "spawnLockIsStale",
		holds: "run/spawn.lock",
		why:   "daemon.removeSpawnLockFile, which deletes the lock from the spawned daemon's own process once it is listening",
	},
	{
		file:  "internal/contract/monitor.go",
		fn:    "load",
		holds: "state/contract.json",
		why:   "monitor.persist's paths.WriteAtomic, the only path that records a §12.1 degradation across sessions",
	},
	{
		file:  "internal/contract/history.go",
		fn:    "LoadHistory",
		holds: "state/history.json",
		why:   "SaveHistory's paths.WriteAtomic, which this lock-free pair leaves free to overlap a load",
	},
	{
		file:  "internal/ipc/state.go",
		fn:    "ReadState",
		holds: "run/state.bin",
		why:   "the daemon's WriteState, whose §12.2 hot-mode transition goes unpublished if the replace fails",
	},
}

// forbiddenReads and requiredReads are the two call shapes the scan classifies, spelled
// pkg.Func — a name-only match on the selector, which is how every other AST guard in this
// repository (internal/contract/lastsessionid_guard_test.go) resolves identifiers without dragging
// in type information. Over-strict is the correct direction here: a local variable named os or
// paths would be flagged rather than missed.
var (
	forbiddenReads = map[string]string{
		"os.ReadFile": "os.ReadFile",
		"os.Open":     "os.Open",
	}
	requiredReads = map[string]bool{
		"paths.ReadFileShared": true,
		"paths.OpenShared":     true,
	}
)

// TestGuard_HotFilesAreReadWithDeleteSharing pins the reader half of the delete-sharing fix, which
// nothing else in the tree can reach.
//
// The writer half is asserted directly and deterministically: internal/paths'
// TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader fails outright if FILE_SHARE_DELETE is ever
// dropped from paths.OpenShared, and internal/ipc's
// TestWriteStateLandsWhileAHookClientHoldsTheRecordOpen fails if WriteAtomic stops landing under a
// held shared handle. Neither can see WHICH open a given reader performs. Every reader in
// sharedReaders closes its handle before it returns, so there is no instant at which another
// goroutine can catch it holding one, and a reader reverted to os.ReadFile satisfies every
// behavioural test in this repository — measured, not assumed: reverting readLockFile to
// os.ReadFile leaves `go test ./internal/daemon/` green. internal/ipc's own contention test says
// the same thing about ReadState in its doc comment.
//
// So the wiring is pinned structurally instead. That is a weaker statement than a behavioural
// assertion — it checks the call, not the effect — but it is the statement that actually fails when
// the defect returns, and the alternative on offer was nothing.
func TestGuard_HotFilesAreReadWithDeleteSharing(t *testing.T) {
	root := repoRoot(t)

	for _, sr := range sharedReaders {
		t.Run(sr.file+":"+sr.fn, func(t *testing.T) {
			fn := findFuncDecl(t, filepath.Join(root, filepath.FromSlash(sr.file)), sr.fn)

			forbidden, required := classifyReadCalls(fn)

			require.Empty(t, forbidden,
				"%s reads %s with %s. On Windows that handle carries no FILE_SHARE_DELETE, so while "+
					"the read is in flight it blocks %s.\n\n"+
					"Use paths.ReadFileShared (or paths.OpenShared) instead: same bytes, same "+
					"*os.PathError shapes, plus delete sharing — see internal/paths/shared.go.",
				sr.fn, sr.holds, strings.Join(forbidden, " and "), sr.why)

			require.NotEmpty(t, required,
				"%s no longer calls paths.ReadFileShared or paths.OpenShared. It reads %s, which %s "+
					"replaces or deletes underneath it, so its handle must grant delete sharing. If "+
					"this function genuinely stopped reading that file, delete its row from "+
					"sharedReaders in the same change rather than leaving a guard that checks nothing.",
				sr.fn, sr.holds, sr.why)
		})
	}
}

// TestGuard_SharedReaderScannerSeesAForbiddenCall is this guard's own self-test, and it is not
// optional. classifyReadCalls returning nothing for every input — an AST shape it does not visit, a
// selector spelled differently — would make the test above pass vacuously forever, which is the
// exact failure mode it exists to prevent. So the classifier is pointed at a function that does
// both things and must report both.
func TestGuard_SharedReaderScannerSeesAForbiddenCall(t *testing.T) {
	const src = `package p

import (
	"os"

	"github.com/qompack/qompack/internal/paths"
)

func sample(p string) ([]byte, error) {
	if f, err := os.Open(p); err == nil {
		_ = f.Close()
	}
	if _, err := os.ReadFile(paths.Long(p)); err != nil {
		return nil, err
	}
	return paths.ReadFileShared(p)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	fn := funcDeclNamed(f, "sample")
	require.NotNil(t, fn, "the fixture must parse into a FuncDecl the scan can find")

	forbidden, required := classifyReadCalls(fn)
	require.Equal(t, []string{"os.Open", "os.ReadFile"}, forbidden,
		"the classifier must see both forbidden shapes; if it sees neither, every row above is vacuous")
	require.Equal(t, []string{"paths.ReadFileShared"}, required,
		"the classifier must also recognise the shared read, or a correct reader would fail the guard")
}

// findFuncDecl parses path and returns the one function declared there under name. Exactly one
// match is required: zero means the function was renamed or moved and the row above is now checking
// nothing, and more than one means the name is ambiguous and the guard cannot say which body it
// approved.
func findFuncDecl(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parsing %s", path)

	var found []*ast.FuncDecl
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name != nil && fd.Name.Name == name && fd.Body != nil {
			found = append(found, fd)
		}
	}
	require.Len(t, found, 1,
		"expected exactly one func %s with a body in %s, found %d — the sharedReaders row naming it "+
			"is stale, and a stale row checks nothing", name, path, len(found))
	return found[0]
}

// funcDeclNamed returns the first function in f called name, or nil. It is findFuncDecl's
// requirement-free half, used by the self-test where the fixture is known.
func funcDeclNamed(f *ast.File, name string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

// classifyReadCalls walks fn's body and reports which forbidden and which required read calls it
// makes, each sorted and de-duplicated. Nested function literals are included: a read moved into a
// closure inside the same function is the same read.
func classifyReadCalls(fn *ast.FuncDecl) (forbidden, required []string) {
	seenBad := map[string]bool{}
	seenGood := map[string]bool{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := pkg.Name + "." + sel.Sel.Name
		if label, bad := forbiddenReads[name]; bad {
			seenBad[label] = true
		}
		if requiredReads[name] {
			seenGood[name] = true
		}
		return true
	})

	return sortedKeys(seenBad), sortedKeys(seenGood)
}

// sortedKeys returns m's keys in a stable order, so a failure message and the self-test's equality
// assertion do not depend on map iteration.
func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

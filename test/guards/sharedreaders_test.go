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

// sharedReader names one function that MUST take its handle through paths.ReadFileShared /
// paths.OpenShared rather than os.ReadFile / os.Open: a production reader, or a test helper (under
// test/ or internal/testutil) that polls a file a live daemon is replacing or deleting.
//
// The list is an inventory, not a pattern: "reads a file some other process replaces or deletes
// while this one is live" is a judgement about the file, and there is no syntax that carries it.
// Adding a row is the deliberate act of making that judgement; the guard's job is only to stop the
// answer silently reverting afterwards.
//
// For product code the inventory is complete, not a sample: productreads_test.go's ordinaryReads
// classifies every other product function that opens a file the ordinary way, and
// TestGuard_EveryProductReadIsClassified fails on any product open that is in neither list.
//
// A test helper earns a row when its read can break the product write it is waiting for. The
// daemon's replace fails while the helper's ordinary handle is open, nothing retries it, and the
// helper's wait then times out on a write its own read prevented: a load-sensitive red with no
// product defect behind it.
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
		file:  "internal/ipc/spawnlock.go",
		fn:    "readSpawnLock",
		holds: "run/spawn.lock",
		why:   "daemon.removeSpawnLockFile, which deletes the lock from the spawned daemon's own process once it is listening, and a competing spawner's reclaim of a stale lock",
	},
	{
		file:  "internal/ipc/spawnlock.go",
		fn:    "removeSpawnLockIf",
		holds: "run/spawn.lock",
		why:   "the same two deleters: it reads the lock to check the claim is still the one it means to remove",
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
		file:  "internal/rehydrate/drops.go",
		fn:    "CurrentDrops",
		holds: "state/rehydrate-<session>.json",
		why: "the rehydrate service's Record, whose paths.WriteAtomic replaces the file just after a " +
			"compact SessionStart has answered (C1.16). The MCP dropped tool reads it through a Reporter " +
			"of its own (internal/cli/daemon.go), so the reporter's mutex does not order the two, and an " +
			"ordinary handle there both fails with ERROR_SHARING_VIOLATION and fails that replace " +
			"(w4-e2eflakes runs/diag-b-sharing-modes-rerun-windows.txt)",
	},
	{
		file:  "internal/ipc/state.go",
		fn:    "ReadState",
		holds: "run/state.bin",
		why:   "the daemon's WriteState, whose §12.2 hot-mode transition goes unpublished if the replace fails",
	},
	{
		file:  "test/e2e/observer_e2e_test.go",
		fn:    "obsSessionEndMarker",
		holds: "run/marker.json",
		why: "the daemon's contract.WriteMarker, a paths.WriteAtomic made once per SessionEnd and not " +
			"retried. obsAwaitSessionEnded polls through this helper for exactly that write, so a replace " +
			"its read made fail leaves the old marker in place and the wait times out (obsRunFlush's rows " +
			"and V3 x08)",
	},
	{
		file:  "test/e2e/sessionstart_compact_test.go",
		fn:    "scAwaitState",
		holds: "state/rehydrate-<session>.json",
		why: "the rehydrate service's Record, the paths.WriteAtomic that lands just after the compact " +
			"answer (C1.16) while this helper polls for it. An ordinary handle both failed the read with " +
			"ERROR_SHARING_VIOLATION (V5 x04's co-load red) and failed that replace " +
			"(w4-e2eflakes runs/diag-b-sharing-modes-rerun-windows.txt)",
	},
	{
		file:  "internal/testutil/daemongone.go",
		fn:    "DaemonHoldingLock",
		holds: "run/daemon.lock",
		why: "Lock.Release's os.Remove, the last act of a daemon shutdown, which every test/ shutdown " +
			"helper polls for through this one reader (testutil.ShutdownDaemonUntilGone, and the " +
			"e2e/fault/platform/release/security/guards gates and diagnostics around it). An ordinary " +
			"handle in this read made that remove fail about one run in twenty, so the helper itself " +
			"caused the abandoned lock it was waiting on (v1StopDaemonAndWaitGone measured it)",
	},
	{
		file:  "test/e2e/faultinject_test.go",
		fn:    "e2eSpawnInFlight",
		holds: "run/spawn.lock",
		why: "daemon.removeSpawnLockFile, the spawned daemon's single, unretried os.Remove of the marker " +
			"once it listens. e2eAwaitSpawnInFlight polls through this helper for exactly that daemon " +
			"(e2eShutdownIfReachable's spawn-in-flight wait), and a remove its read made fail leaves a " +
			"marker that suppresses every later lazy spawn until it ages out of ipc's spawnLockStaleAfter",
	},
	{
		file: "internal/store/backup.go",
		fn:   "backupFileDigest",
		// refuseIfTheProjectMoved delegates the actual open/read here. This streams
		// journals, seals and generation/segment authority through the shared reader.
		holds: "state/delivery-lease-position.json, state/delivery-ack-position.json and (more " +
			"briefly, and appended rather than replaced, which is why no writer of theirs is named " +
			"below) the two delivery journals beside them",
		why: "the daemon's paths.WriteAtomic of those sidecars — openSealHandle's v1→v2 conversion, " +
			"whose failure faults the delivery journal for that daemon's whole life, and closeSeals' " +
			"downgrade to v1, whose failure silently leaves a v2 file a pre-step-1 build cannot open",
	},
	{
		file:  "internal/store/backup.go",
		fn:    "TakeBackup",
		holds: "every copied file under .qompack, the two delivery-seal sidecars among them",
		why: "the same two WriteAtomics, plus every other writer the copy walk passes; the walk reads " +
			"the whole tree, so it is the wider of this file's two read windows",
	},
	// The V6 close-out's audit (w5-winfiles) of every product read of a file some writer replaces or
	// removes. Each reader below took an ordinary handle on a file that another process, or another
	// goroutine no lock of its own orders, replaces or removes; each was os.ReadFile or os.Open until
	// that audit. Every other product read is classified in productreads_test.go's ordinaryReads,
	// with the reason an ordinary handle is safe there, and TestGuard_EveryProductReadIsClassified
	// keeps that true.
	{
		file:  "internal/contract/marker.go",
		fn:    "readMarker",
		holds: "run/marker.json",
		why: "the daemon's contract.WriteMarker, a paths.WriteAtomic made once per SessionEnd and " +
			"PreCompact and never retried. checkSessionStartFires reads the marker in the daemon while " +
			"another session's terminal hook writes it (session ends run concurrently since C1.15), and " +
			"`qompack selftest` reads it from its own process. A replace this read made fail leaves the " +
			"previous marker in place, and a read refused by a finishing replace counted as an absence " +
			"toward the SevCritical §12.1 degradation although the hook had fired",
	},
	{
		file:  "internal/cli/qompack_commands.go",
		fn:    "readPersistedMetrics",
		holds: "metrics/latency.json",
		why: "the daemon's obs.Registry.Persist, a paths.WriteAtomic, while `qompack doctor` reads " +
			"the snapshot from its own process, and the /qompack status command falls back to it when " +
			"a live daemon does not answer inside its call deadline",
	},
	{
		file:  "internal/store/flush.go",
		fn:    "loadStoreState",
		holds: "state/store.json",
		why: "the daemon store's persistStoreState, a paths.WriteAtomic of the cumulative counters, " +
			"while `qompack doctor` and `qompack fsck` open the same store read-only from their own " +
			"process, and every store open runs this load",
	},
	{
		file:  "internal/store/capture_sidecar.go",
		fn:    "ReadCaptureSidecar",
		holds: "records/captures/**/<observation>.json",
		why: "the paths.WriteAtomic of the same sidecar by the daemon's ingest (publishCapture, " +
			"through WriteCaptureSidecar) and by the observer (LinkCaptureReference). A lost-ACK " +
			"duplicate reaches ingest on two paths, live and drain, and no lock this reader takes " +
			"orders them. A read refused by an in-flight replace also drops the prior sidecar's " +
			"Published record, which WriteCaptureSidecar would then write back as open",
	},
	{
		file:  "internal/store/lifecycle.go",
		fn:    "CompactRetentionRoots",
		holds: "state/retention-roots.jsonl",
		why: "another GC pass's compaction, a paths.WriteAtomic of the same file: GC passes are not " +
			"serialized, and each concurrent session end runs one (C1.15), as does the idle scheduler",
	},
	{
		file:  "internal/store/gcrun.go",
		fn:    "loadGCState",
		holds: "state/gc.json",
		why: "another GC pass's saveGCState (paths.WriteAtomic) and clearGCState (os.Remove), for " +
			"CompactRetentionRoots' reason: nothing serializes two passes",
	},
	{
		file:  "internal/store/gcrun.go",
		fn:    "pendingMarkerRoot",
		holds: "state/pending/<marker>.json",
		why: "the os.Remove of the same expired marker by a concurrent GC pass's " +
			"expirePendingMarkers, or by the late Put's own pendingWrite.done",
	},
	{
		file:  "internal/tokens/calibrate.go",
		fn:    "loadCalibEntry",
		holds: "~/.qompack/calibration.json",
		why: "every other project's daemon, and every store open, persisting into the one " +
			"user-global calibration document with paths.WriteAtomic: cross-process by design",
	},
	{
		file:  "internal/tokens/calibrate.go",
		fn:    "persist",
		holds: "~/.qompack/calibration.json",
		why: "the same user-global document: persist reads it to merge this scope's factor in while " +
			"any other process's persist replaces it, and a refused read made it write back this " +
			"scope's entry alone, dropping every other project's factor",
	},
	{
		file:  "internal/sketch/io.go",
		fn:    "LoadWithLog",
		holds: "sketches/*",
		why: "the daemon's sketch.Save (paths.WriteAtomic) and ReplaceBloom's renames of tried.bloom, " +
			"while `qompack fsck`, and the negknow ledger it opens, load every sketch from its own process",
	},
	{
		file:  "internal/daemon/scheduler_state.go",
		fn:    "readStateFile",
		holds: "state/bocd.json and state/scheduler.json",
		why: "schedRuntime.Persist, which writes both files with paths.WriteAtomic under persistMu " +
			"only, after releasing r.mu, while a session bind reads them under r.mu: the two are not " +
			"ordered, and both paths are shared by every session",
	},
	{
		file:  "internal/daemon/handlers.go",
		fn:    "LoadSessionRecovery",
		holds: "state/session-recovery.json",
		why: "the daemon's writeSessionRecovery, a paths.WriteAtomic that nothing retries. The " +
			"daemon's own callers hold recoveryMu with it, but the function is exported and polled " +
			"from another process (test/e2e's V5 x03 while the daemon settles a session end), and a " +
			"replace that poll made fail leaves a stale recovery marker behind",
	},
	{
		file:  "internal/daemon/blob.go",
		fn:    "readBlob",
		holds: "spool/blob-*.bin",
		why: "the drain's cleanupAcknowledged, whose os.Remove of a consumed blob runs under the " +
			"drainer's lock while the live ingest reads blobs without it; a lost-ACK duplicate is read " +
			"live while the drain retires its other copy, and a refused remove fails that drain pass",
	},
	{
		file:  "internal/checkpoint/gitindex.go",
		fn:    "readGitState",
		holds: "<gitdir>/HEAD and <gitdir>/index",
		why: "git itself, which replaces both by renaming a .lock file over them: a checkout rewrites " +
			"HEAD, and add, commit and the index-refreshing status editors run in the background " +
			"rewrite an index of up to 64 MiB. The rename fails while this read holds an ordinary handle",
	},
	// Owner decision D22 (00-ARCHITECTURE.md §3.2): internal/config may import internal/paths, so
	// the two config loaders now read config.json shared, which w5-winfiles could only record as a
	// residual.
	{
		file:  "internal/config/load.go",
		fn:    "Load",
		holds: "<project>/.qompack/config.json and <home>/.qompack/config.json",
		why: "the user's editor saving the file atomically, a new file renamed over the old one. An " +
			"ordinary handle made that save fail while config.Load held the file (the daemon's reload, " +
			"`config print`, doctor, self-test), and a read the save made fail was taken for a missing " +
			"file, so the whole layer fell back to the defaults without a warning " +
			"(TestLoad_ReadsThroughAnEditorsAtomicSaves)",
	},
	{
		file:  "internal/config/capture_load.go",
		fn:    "readCaptureConfig",
		holds: "<project>/.qompack/config.json and <home>/.qompack/config.json",
		why: "the same atomic save, on the hook path: an ordinary handle made the save or the hook's read " +
			"fail, and under D8 a config the hook cannot read refuses the capture, so saving the config " +
			"could make a hook record nothing. It reads through paths.OpenSharedLeaf, which also never " +
			"follows a final link, so no Lstat/os.SameFile identity check is left for a save to land " +
			"inside (TestLoadForCapture_ReadsThroughAnEditorsAtomicSaves)",
	},
	{
		file:  "test/fault/fault.go",
		fn:    "flushAndAwaitEnd",
		holds: "run/marker.json",
		why: "the daemon's contract.WriteMarker, made once per session end and never retried, which " +
			"this poll waits for: a replace its own read made fail would time the wait out, as " +
			"obsSessionEndMarker's did (w4-e2eflakes)",
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
		"paths.OpenSharedLeaf": true,
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

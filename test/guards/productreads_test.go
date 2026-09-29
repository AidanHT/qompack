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

// ordinaryRead names one product function that opens a file with os.ReadFile, os.Open or os.OpenFile
// — a handle with no FILE_SHARE_DELETE on Windows — and says why that is safe there: nothing
// replaces or removes the file while the handle can be open, or whatever does is ordered with this
// reader, or the handle is not a file's at all.
//
// It is sharedReaders' other half. sharedReaders lists the readers that must take their handle
// through paths.ReadFileShared or paths.OpenShared; ordinaryReads lists every other product
// function that opens a file, so that between the two lists every product open is a judgement
// someone made and wrote down. The V6 close-out's audit (w5-winfiles) made the first set of those
// judgements; TestGuard_EveryProductReadIsClassified makes the next one due whenever a product
// function starts opening a file the ordinary way.
type ordinaryRead struct {
	file string // module-relative, slash-separated
	fn   string // FuncDecl name; the receiver, if any, is ignored
	why  string // why an ordinary handle cannot stall a writer here
}

// Reasons shared by several rows, named once so the rows that rest on the same argument say so.
const (
	whyAppendOnlyIndex = "an append-only index log (index/*.jsonl): an append needs no delete access, and " +
		"nothing replaces or removes the log while a store is open"
	whyBackupTree = "a file of a backup tree, or of a restore's private staged copy of one: created " +
		"once under a fresh backup id (TakeBackup refuses an existing id before it writes) and never " +
		"replaced afterwards. The one removal, TakeBackup's ErrBackupMoved cleanup, runs before the " +
		"manifest exists, and these readers reach tree files only through that manifest. No lease is " +
		"assumed: fsck's VerifyBackupAt reads backups without one"
	whyMigrateFiles = "a state/migrate file, which only the Migrator writes, and only inside `qompack " +
		"backup` under the writer lease (fsck's VerifyBackupAt builds a bare Migrator that reads no " +
		"migrate file); fsck reads these files shared"
	whyDeliveryJournal = "a delivery journal or seal, which only the holder of the daemon lock writes (this " +
		"daemon, or the offline delivery-seal tool, which takes the same lock); every other process " +
		"reads them shared (backup, doctor, fsck)"
	whyDirectory      = "a directory handle, opened to enumerate or fsync it"
	whyHostTranscript = "the host's session transcript, which Claude Code appends to; an append needs no " +
		"delete access, and no Qompack process replaces it"
	whyGitPointer = "a git pointer file (a linked worktree's .git, commondir), written once by git " +
		"when the checkout is made; nothing replaces it while the checkout lives"
	whyEvalInput = "an evaluation input the operator named (corpus, ledger, provenance, transcript or " +
		"module file); no Qompack process rewrites it during the run"
	whySealedCheckpoint = "a sealed checkpoint artifact (§7.4): CreateNew writes it read-only once, and " +
		"nothing replaces or removes an artifact the manifest names"
	whyObject = "a content-addressed object, never replaced. GC removes only unreachable objects and " +
		"counts a removal a reader refuses as skipped for that pass (gcrun.go's sweep), and quarantine " +
		"runs under the daemon lock. These opens also carry flags OpenShared does not take"
	whyTestSupport = "test support, reading a file its own test wrote"
)

// ordinaryReads is that inventory, sorted by file then function.
var ordinaryReads = []ordinaryRead{
	{"internal/checkpoint/gitindex.go", "resolveGitDir", whyGitPointer},
	{"internal/checkpoint/reader.go", "Verify", whySealedCheckpoint},
	{"internal/checkpoint/reader.go", "verified", whySealedCheckpoint},
	{"internal/checkpoint/writer.go", "resumeDraft", "state/draft-<session>.json is written and removed " +
		"only by this daemon's FileWriter for that session, and Begin reads it only when no draft of " +
		"that session is live; the daemon lock keeps every other writer out, and backup copies it shared"},
	{"internal/cli/doctor.go", "bundleRow", "a file the plugin bundle ships, which no Qompack process writes"},
	{"internal/commands/evalartifacts.go", "readEvalJSON", whyEvalInput},
	{"internal/contract/sentinel.go", "ScanTranscriptForProbe", whyHostTranscript},
	{"internal/contract/sentinel.go", "readTail", whyHostTranscript},
	{"internal/daemon/delivery_lease.go", "loadAcksFrom", whyDeliveryJournal},
	{"internal/daemon/delivery_lease.go", "loadDeliveryPosition", whyDeliveryJournal},
	{"internal/daemon/delivery_lease.go", "loadFrom", whyDeliveryJournal},
	{"internal/daemon/delivery_lease.go", "readDeliverySealImage", whyDeliveryJournal},
	{"internal/daemon/drain.go", "drainFile", "a spool file, which hooks append to and only this drainer " +
		"removes, from the same pass under dr.mu and after closing this handle"},
	{"internal/daemon/drain.go", "loadState", "state/drain.json, whose only writer is saveState under the " +
		"same dr.mu; doctor reads it shared"},
	{"internal/daemon/drain.go", "scanPendingBlobs", "a spool file, for drainFile's reason: the pass " +
		"that removes spool files holds dr.mu, as this scan does"},
	{"internal/daemon/drain_host_order.go", "nextRecordHostTS", "a spool file, for drainFile's reason: " +
		"the pass orders its files under dr.mu, before it reads or removes any, and closes this handle first"},
	{"internal/daemon/lock.go", "touchFile", "the lock holder's own heartbeat. A reclaimer removes " +
		"daemon.hb only once it judges the holder gone, and a removal this handle refuses leaves the " +
		"lock with a holder that is alive"},
	{"internal/daemon/lock_linux.go", "pidAlive", "/proc/<pid>/stat, a kernel pseudo-file"},
	{"internal/daemon/spawn.go", "spawnDetached", "os.DevNull"},
	{"internal/daemon/spawn_stage.go", "copyStaged", "the running plugin binary, read once to stage a " +
		"copy; no Qompack process replaces or removes it"},
	{"internal/daemon/spawn_stage.go", "fileSHA256", "the running plugin binary, hashed to name its " +
		"staged copy; no Qompack process replaces or removes it. Staged copies, which spawners do " +
		"remove, are read by verifyStagedThen through paths.OpenShared (sharedReaders)"},
	{"internal/dag/log.go", "load", "dag/deps.jsonl, read once when the daemon opens its graph and " +
		"before that graph's first Compact can replace it; the daemon lock keeps every other writer " +
		"out, and backup reads it shared"},
	{"internal/eval/importer.go", "insideRepository", whyEvalInput},
	{"internal/eval/importer.go", "parseTranscript", whyEvalInput},
	{"internal/eval/ledger.go", "LoadRequestLedger", whyEvalInput},
	{"internal/eval/provenance.go", "Check", whyEvalInput},
	{"internal/eval/provenance.go", "LoadBaselineProvenance", whyEvalInput},
	{"internal/eval/replay.go", "Load", whyEvalInput},
	{"internal/hostperm/sources.go", "readSmall", whyGitPointer},
	{"internal/ipc/ipctest/behaviour.go", "readSpoolLines", whyTestSupport},
	{"internal/observer/state.go", "loadState", "state/observer.json, loaded once per daemon before that " +
		"daemon's first persistState; the daemon lock keeps every other writer out, and backup reads " +
		"it shared"},
	{"internal/observer/stop.go", "tailAssistantText", whyHostTranscript},
	{"internal/paths/appendonly.go", "OpenFile", "the §7.4-guarded opener every product write goes " +
		"through (appends and exclusive creates); its callers choose the file"},
	{"internal/paths/appendonly.go", "TerminatePartialTail", "the tail of an append-only log its caller " +
		"is about to append to; append-only logs are never replaced"},
	{"internal/paths/atomic.go", "fsyncDir", whyDirectory},
	{"internal/paths/leaf_unix.go", "openSharedLeaf", "OpenSharedLeaf's own body off Windows, where a " +
		"descriptor blocks no replace or remove"},
	{"internal/paths/manifest.go", "ReadManifest", "checkpoints/MANIFEST.jsonl, append-only under §7.4: " +
		"nothing replaces or removes it"},
	{"internal/paths/replace_other.go", "openShared", "OpenShared's own body off Windows, where a " +
		"descriptor blocks no replace or remove"},
	{"internal/paths/replace_other.go", "openSharedRW", "OpenSharedRW's own body off Windows, where a " +
		"descriptor blocks no replace or remove"},
	{"internal/pluginmanifest/manifest.go", "Validate", "a file of the plugin bundle under validation, " +
		"which no Qompack process writes"},
	{"internal/rules/scanner.go", "nestedRule", "a user-authored rules file (CLAUDE.md and what it " +
		"imports), which no Qompack process writes"},
	{"internal/rules/scanner.go", "scopedRule", "a user-authored rules file, for nestedRule's reason"},
	{"internal/store/backup.go", "RestoreBackup", whyBackupTree},
	{"internal/store/backup.go", "VerifyBackup", whyBackupTree},
	{"internal/store/backup.go", "readJSONLines", whyMigrateFiles},
	{"internal/store/flush.go", "loadSessions", whyAppendOnlyIndex},
	{"internal/store/gc_delivery_segments.go", "dsegListSegmentDirs", whyDirectory},
	{"internal/store/gcrun.go", "listRetentionDir", whyDirectory},
	{"internal/store/maintenance.go", "maintCopyVerify", whyBackupTree},
	{"internal/store/maintenance.go", "maintCreateCopy", "the O_EXCL create of a fresh file in a " +
		"backup's staging tree; its source is read through paths.OpenShared"},
	{"internal/store/maintenance.go", "maintHashFile", whyBackupTree},
	{"internal/store/maintenance.go", "maintReadBounded", whyBackupTree},
	{"internal/store/migrate.go", "Cursor", whyMigrateFiles},
	{"internal/store/migrate.go", "Handoff", whyMigrateFiles},
	{"internal/store/object_open_windows.go", "openObjectLeaf", whyObject},
	{"internal/store/objects.go", "openObjectChecked", whyObject},
	{"internal/store/roots.go", "loadRoots", whyAppendOnlyIndex},
	{"internal/store/tooluseindex.go", "scanIndexJSONL", whyAppendOnlyIndex},
	{"internal/testutil/fixtures.go", "readContractManifest", whyTestSupport},
	{"internal/testutil/fixtures.go", "readFixtureFile", whyTestSupport},
	{"internal/testutil/golden.go", "goldenAt", whyTestSupport},
	{"internal/testutil/procalive_linux.go", "ProcessAlive", "/proc/<pid>/stat, a kernel pseudo-file"},
	{"internal/tokens/chunkcache.go", "chunkCacheAppend", "state/chunktokens.bin, the estimator's own " +
		"cache: its append and its rewrite run under the estimator's fmu, so this handle is closed " +
		"before any replace of the file can start"},
	{"internal/tokens/chunkcache.go", "loadChunkCache", "state/chunktokens.bin, loaded when the " +
		"estimator is built, before its first flush; only a full store.Open passes a cache path, and " +
		"that is the daemon or a CLI holding the daemon lock or the writer lease"},
}

// productReadRoots are the module-relative trees whose non-test Go files are product code.
var productReadRoots = []string{"internal", "cmd"}

// ordinaryOpeners are the calls that take a Windows handle with no FILE_SHARE_DELETE. os.OpenFile
// is included whatever its flags: a read-write or write handle obstructs a replace exactly as a
// read handle does. os.Root's methods are not: they open with FILE_SHARE_DELETE
// (GOROOT/src/internal/syscall/windows/at_windows.go).
var ordinaryOpeners = map[string]bool{"os.ReadFile": true, "os.Open": true, "os.OpenFile": true}

// packageScope is the fn an ordinary open outside any function declaration is keyed under.
const packageScope = "(package scope)"

// minProductFiles is a floor on how many product files the scan must parse. It is far below the
// tree's real count and exists only so a wrong root, or a walk that stops early, fails loudly
// instead of classifying nothing.
const minProductFiles = 200

// TestGuard_EveryProductReadIsClassified requires every product function that opens a file the
// ordinary way to carry a row in ordinaryReads, and every row to still name such a function.
//
// It is what makes sharedReaders complete rather than a list of the readers someone once noticed.
// A reader that must be shared and was reverted to os.ReadFile fails here unless someone also
// writes down why an ordinary handle is safe for it; a new ordinary read fails here until that
// judgement is made. The failure names the function and both ways out.
func TestGuard_EveryProductReadIsClassified(t *testing.T) {
	root := repoRoot(t)
	found, files := scanProductOpens(t, root)
	require.GreaterOrEqual(t, files, minProductFiles, "the scan parsed too few product files to prove anything")

	classified := map[string]ordinaryRead{}
	for _, r := range ordinaryReads {
		key := r.file + ":" + r.fn
		_, dup := classified[key]
		require.False(t, dup, "ordinaryReads names %s twice", key)
		require.NotEmpty(t, strings.TrimSpace(r.why), "ordinaryReads row %s gives no reason", key)
		classified[key] = r
	}

	var unclassified []string
	for key, calls := range found {
		if _, ok := classified[key]; !ok {
			unclassified = append(unclassified, key+" ("+strings.Join(calls, ", ")+")")
		}
	}
	sort.Strings(unclassified)
	require.Empty(t, unclassified,
		"these product functions open a file with an ordinary handle, which on Windows carries no "+
			"FILE_SHARE_DELETE and makes another process's paths.WriteAtomic replace or os.Remove of "+
			"that file fail while it is open. For each one, either read through paths.ReadFileShared "+
			"or paths.OpenShared and add a sharedReaders row naming the writer it could stall, or add an "+
			"ordinaryReads row saying why nothing can replace or remove that file while it is open")

	var stale []string
	for key := range classified {
		if _, ok := found[key]; !ok {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	require.Empty(t, stale,
		"these ordinaryReads rows name a function that no longer opens a file the ordinary way (or no "+
			"longer exists); delete the row in the same change, or the inventory stops being exact")

	for _, sr := range sharedReaders {
		_, both := classified[sr.file+":"+sr.fn]
		require.False(t, both, "%s:%s is in both sharedReaders and ordinaryReads", sr.file, sr.fn)
	}
}

// TestGuard_ProductOpenScannerSeesEveryShape is the scan's own self-test: a scanner that missed a
// shape would pass the test above vacuously for every function written that way.
func TestGuard_ProductOpenScannerSeesEveryShape(t *testing.T) {
	const src = `package p

import (
	"os"

	"github.com/qompack/qompack/internal/paths"
)

var opener = os.Open

func direct(p string) ([]byte, error) { return os.ReadFile(p) }

func (x *T) method(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
	}
	return err
}

func closure(p string) func() error {
	return func() error {
		f, err := os.Open(p)
		if err == nil {
			_ = f.Close()
		}
		return err
	}
}

func shared(p string) ([]byte, error) { return paths.ReadFileShared(p) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	got := map[string][]string{}
	ordinaryOpensInFile(f, "sample.go", got)
	require.Equal(t, map[string][]string{
		"sample.go:" + packageScope: {"os.Open"},
		"sample.go:direct":          {"os.ReadFile"},
		"sample.go:method":          {"os.OpenFile"},
		"sample.go:closure":         {"os.Open"},
	}, got, "the scan must see a package-scope value, a direct call, a method's call and a closure's call, "+
		"and must not count a shared read")
}

// scanProductOpens parses every non-test Go file under productReadRoots and returns, keyed by
// "file:fn", the ordinary openers each function uses, with the number of files parsed.
func scanProductOpens(t *testing.T, root string) (map[string][]string, int) {
	t.Helper()
	found := map[string][]string{}
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
			ordinaryOpensInFile(f, filepath.ToSlash(rel), found)
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}
	return found, files
}

// ordinaryOpensInFile adds to into every ordinary opener f references, called or not, keyed by the
// enclosing function declaration (packageScope outside one), each list sorted and de-duplicated.
func ordinaryOpensInFile(f *ast.File, rel string, into map[string][]string) {
	for _, decl := range f.Decls {
		key := rel + ":" + packageScope
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil {
			key = rel + ":" + fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := pkg.Name + "." + sel.Sel.Name
			if !ordinaryOpeners[name] {
				return true
			}
			calls := into[key]
			if i := sort.SearchStrings(calls, name); i == len(calls) || calls[i] != name {
				calls = append(calls, "")
				copy(calls[i+1:], calls[i:])
				calls[i] = name
			}
			into[key] = calls
			return true
		})
	}
}

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

// A release of the singleton daemon lock REPORTS what it could not finish (SP20-D1 design §4.4).
//
// Lock.Release downgrades a held v2 delivery seal back to v1 on its way out, best effort: a failure
// there is latched as a residual rather than returned, because refusing to give up ownership over a
// v2 file every build with the dual reader can open would be far worse than leaving it. That makes
// the latch the ONLY record, and a latch nobody reads is the silence the residual exists to end —
// design §4.4's "after a clean stop: v1 is on disk" would stop being true with nothing saying so,
// and the operator would find out from an older binary that will not start.
//
// ReleaseWithReport is the shared release boundary for daemon-owned and CLI-owned
// leases. A borrowed lease outlives Run/Stop until the composition root closes its
// writers; reporting from Stop would run before the journal closes and miss the
// residual. The guard pins the actual Release/report pair and accounts for every
// direct Release in the daemon package. Behavioural tests cover owned and borrowed
// shutdown paths, including a failed downgrade reported exactly once.

// sealResidualReleaseSites are the production functions that release the singleton daemon lock.
//
// releases is the number of Release calls the function makes, and it is part of the row rather than
// derived, so that ADDING a release without its report fails here too — the guard's question is
// "does every release report", and a count is the only form of that question an AST can answer
// without reasoning about control flow.
var sealResidualReleaseSites = []struct {
	file     string // module-relative, slash-separated
	fn       string // FuncDecl name; the receiver, if any, is ignored
	releases int
	why      string
}{
	{
		file: "internal/daemon/borrowed_lease.go", fn: "ReleaseWithReport", releases: 1,
		why: "both daemon-owned and composition-owned leases report after their actual release",
	},
}

// releasesWithNoSealResidual are the Release calls in internal/daemon that are NOT the singleton
// lock's, or whose lock can carry no journal. Each is here with its reason: an exemption nobody
// stated would be indistinguishable from a site that was forgotten.
var releasesWithNoSealResidual = []struct {
	file string
	fn   string
	why  string
}{
	{
		file: "internal/daemon/delivery_seal_tool.go", fn: "RepairDeliverySeal",
		why: "the offline tool takes the lock to exclude a daemon and converts the sidecars itself; " +
			"it never calls openDeliveryJournal, so Lock.journal stays nil and SealDowngradeResidual " +
			"can only ever answer nil there",
	},
	{
		file: "internal/daemon/spawn.go", fn: "SpawnDetached",
		why: "os.Process.Release, which detaches the spawned child from this process — the same " +
			"method name on an unrelated type",
	},
}

// reportFunc is the reporter every row above must call once per release.
const reportFunc = "reportSealDowngradeResidual"

// TestGuard_AReleasedLockReportsItsSealResidual pins both halves: each known release site reports
// as many times as it releases, and no OTHER function in internal/daemon releases anything without
// appearing in one of the two lists above.
func TestGuard_AReleasedLockReportsItsSealResidual(t *testing.T) {
	root := repoRoot(t)

	for _, site := range sealResidualReleaseSites {
		t.Run(site.file+":"+site.fn, func(t *testing.T) {
			fn := findFuncDecl(t, filepath.Join(root, filepath.FromSlash(site.file)), site.fn)

			require.Equal(t, site.releases, countSelectorCalls(fn, "Release"),
				"%s no longer makes exactly %d Release calls (%s). Update this row in the same change: "+
					"a count that has drifted is a guard checking the wrong number.", site.fn, site.releases, site.why)

			require.Equal(t, site.releases, countPlainCalls(fn, reportFunc),
				"%s releases the daemon lock %d time(s) and calls %s %d time(s). A release that does not "+
					"report is a failed v1 downgrade nobody will ever learn about: the residual is latched "+
					"and never returned, so this call is its only reader, and design §4.4's \"after a clean "+
					"stop, v1 is on disk\" would stop being true in silence.\n\nSite: %s",
				site.fn, site.releases, reportFunc, countPlainCalls(fn, reportFunc), site.why)
		})
	}

	t.Run("every release in internal/daemon is accounted for", func(t *testing.T) {
		var want []string
		for _, site := range sealResidualReleaseSites {
			want = append(want, site.fn)
		}
		for _, ex := range releasesWithNoSealResidual {
			want = append(want, ex.fn)
		}
		sort.Strings(want)

		got := functionsCallingRelease(t, filepath.Join(root, "internal", "daemon"))

		require.Equal(t, want, got,
			"a production function in internal/daemon calls Release and is in neither list. If it "+
				"releases the singleton lock, add it to sealResidualReleaseSites and call %s beside the "+
				"release; if it releases something else, add it to releasesWithNoSealResidual with the "+
				"reason. Leaving it out is how the third site went unnoticed the first time.", reportFunc)
	})
}

// TestGuard_TheSealResidualScannerSeesBothCallShapes is this guard's self-test, and it is not
// optional: counters that answered zero for every input would make every row above pass forever. It
// is pointed at a fixture that makes each call shape a known number of times.
func TestGuard_TheSealResidualScannerSeesBothCallShapes(t *testing.T) {
	const src = `package daemon

func releasesTwice(lock *Lock, other *Lock) {
	if err := lock.Release(); err != nil {
		reportSealDowngradeResidual(lock, nil, "one")
	}
	defer func() {
		_ = other.Release()
		reportSealDowngradeResidual(other, nil, "two")
	}()
}

func releasesSomethingElse(cmd *exec.Cmd) error {
	return cmd.Process.Release()
}

func releasesNothing() {
	reportSealDowngradeResidual(nil, nil, "none")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	two := funcDeclNamed(f, "releasesTwice")
	require.NotNil(t, two, "the fixture must parse into a FuncDecl the scan can find")
	require.Equal(t, 2, countSelectorCalls(two, "Release"),
		"the release counter must see a call in an if-statement AND one inside a deferred literal, "+
			"or a row's count is met by half its sites")
	require.Equal(t, 2, countPlainCalls(two, reportFunc),
		"and the report counter must see both, or a site that stopped reporting would still pass")

	none := funcDeclNamed(f, "releasesNothing")
	require.NotNil(t, none)
	require.Zero(t, countSelectorCalls(none, "Release"),
		"the counter must not report a call that is not there")

	require.Equal(t, []string{"releasesSomethingElse", "releasesTwice"}, releaseSitesIn(f),
		"the completeness scan must see cmd.Process.Release too — an exemption it cannot see is an "+
			"exemption that proves nothing, and a real site hiding behind the same shape would be missed")
}

// functionsCallingRelease returns, sorted and de-duplicated, the names of the functions in dir's
// PRODUCTION files that call any method named Release. Test files are excluded: a fixture releasing
// its own lock is not a production site, and pinning them would be pinning the wrong thing. The scan
// is one directory deep, which is all of internal/daemon — it has no subpackages.
func functionsCallingRelease(t *testing.T, dir string) []string {
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
		for _, fn := range releaseSitesIn(f) {
			seen[fn] = true
		}
	}
	require.NotZero(t, scanned, "the scan found no production files in %s, so it proves nothing", dir)

	return sortedKeys(seen)
}

// releaseSitesIn returns the names of f's functions that call a method named Release, sorted and
// de-duplicated. The match is on the method name alone — the same name-only resolution every other
// AST guard here uses, and over-strict in the safe direction: a Release on an unrelated type is
// reported and answered with an exemption row rather than silently dropped.
func releaseSitesIn(f *ast.File) []string {
	seen := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		if countSelectorCalls(fn, "Release") > 0 {
			seen[fn.Name.Name] = true
		}
	}
	return sortedKeys(seen)
}

// countSelectorCalls counts the calls fn's body makes to a method named name, whatever it is called
// on. Nested function literals count: a release moved into a deferred closure is the same release,
// and daemon.go's own sites are written that way.
func countSelectorCalls(fn *ast.FuncDecl, name string) int {
	n := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel != nil && sel.Sel.Name == name {
			n++
		}
		return true
	})
	return n
}

// countPlainCalls counts the calls fn's body makes to the plain (non-selector) function name,
// function literals included. It is callsFunction's counting form; the two are kept apart because a
// presence test and a pairing test fail for different reasons and say so differently.
func countPlainCalls(fn *ast.FuncDecl, name string) int {
	n := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == name {
			n++
		}
		return true
	})
	return n
}

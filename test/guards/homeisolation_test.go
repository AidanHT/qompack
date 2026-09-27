package guards

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/paths/pathstest"
	"github.com/qompack/qompack/internal/tokens"
)

// The two guards in this file keep every test process away from the real home directory, where
// Qompack's user-global layer lives (<home>/.qompack: config.json, calibration.json, the fallback
// logs, the staged daemon copies under bin/) beside Claude Code's own settings (<home>/.claude).
// A test that reaches either with the developer's real home reads files the suite does not control,
// so its result depends on the machine, and it can write into them: its calibration samples would
// overwrite the user's real factor. ~/.qompack is in 00-ARCHITECTURE.md §13 invariant 7's write set
// for the product serving its user, never for a test, and ~/.claude is in no write set. w5-winfiles
// found internal/cli tests opening stores with no isolation at all, on a machine that would have a
// real calibration.json after the live UAT.
//
// The mechanism is internal/paths/pathstest: pathstest.Main, called from TestMain, points HOME and
// USERPROFILE at a fresh directory and unsets QOMPACK_HOME and CLAUDE_CONFIG_DIR for the whole test
// process, before any test runs.
//
//   - TestGuard_EveryHomeReachingTestPackageIsolatesHome proves every test binary that CAN reach the
//     home installs it, from the import graph: a package with a test file whose test binary links a
//     package that resolves the home must have a TestMain that calls pathstest.Main. It also refuses
//     a package-level snapshot of the environment, which Go evaluates before TestMain runs.
//   - TestGuard_IsolatedTestsNeverReadAPoisonedRealHome proves the mechanism works, with a sentinel:
//     it plants a fake "real home" whose config.json, calibration.json and Claude Code settings are
//     each poisoned so that reading them changes an answer, starts this test binary again with HOME,
//     USERPROFILE, QOMPACK_HOME and CLAUDE_CONFIG_DIR all pointing into it, and requires every home
//     lookup the product makes to miss the poison and every file in it to be left byte-for-byte as
//     it was.

// homeLookupCalls are the standard-library calls a product function finds the user's home (or a
// directory derived from it) with, spelled pkg.Func.
var homeLookupCalls = map[string]bool{"os.UserHomeDir": true, "os.UserConfigDir": true, "os.UserCacheDir": true}

// homeEnvNames are the environment variables that name the home or a user-global location ahead of
// it: the two os.UserHomeDir reads, the calibration file's directory (tokens.DefaultCalibPath) and
// Claude Code's settings directory (internal/hostperm).
var homeEnvNames = map[string]bool{"HOME": true, "USERPROFILE": true, "QOMPACK_HOME": true, "CLAUDE_CONFIG_DIR": true}

// envSnapshotCalls are the calls a package-level variable must not be initialized with: Go runs
// package initialization before TestMain, so the value would hold the real home however carefully
// TestMain isolates it afterwards.
var envSnapshotCalls = map[string]bool{
	"os.Environ": true, "os.UserHomeDir": true, "os.UserConfigDir": true, "os.UserCacheDir": true,
}

// homeResolverExempt are product-tree packages that read the home by design and are not product
// code: pathstest is the isolation itself.
var homeResolverExempt = map[string]bool{"internal/paths/pathstest": true}

// knownHomeResolvers are the packages the scan must find today. It guards the scan from passing
// vacuously; a new resolver is found without an edit here, and a resolver that stops reading the
// home is removed here in the same change.
var knownHomeResolvers = []string{"internal/cli", "internal/daemon", "internal/eval", "internal/hostperm", "internal/tokens"}

// minHomeReachingTestPackages is a floor on how many test packages the import graph must report as
// able to reach the home. It is far below the tree's real count (44 when this guard was written) and
// exists only so a go list that returns too little fails loudly instead of checking nothing.
const minHomeReachingTestPackages = 30

// pathstestImport is the import path of the isolation helper every TestMain must call.
const pathstestImport = modulePrefix + "internal/paths/pathstest"

// TestGuard_EveryHomeReachingTestPackageIsolatesHome requires a TestMain calling pathstest.Main in
// every package whose test binary links a package that resolves the home, and no package-level
// environment snapshot anywhere in the tree.
func TestGuard_EveryHomeReachingTestPackageIsolatesHome(t *testing.T) {
	root := repoRoot(t)

	resolvers := scanHomeResolvers(t, root)
	for _, pkg := range knownHomeResolvers {
		require.Contains(t, resolvers, pkg, "the home-lookup scan no longer finds %s; if it truly stopped "+
			"reading the home, drop it from knownHomeResolvers in the same change", pkg)
	}

	binaries := listTestBinaryDeps(t, root)
	var reaching, missing []string
	for pkg, deps := range binaries {
		via := ""
		for _, dep := range append([]string{pkg}, deps...) {
			if rel, ok := strings.CutPrefix(dep, modulePrefix); ok && resolvers[rel] != nil {
				via = rel
				break
			}
		}
		if via == "" {
			continue
		}
		rel := strings.TrimPrefix(pkg, modulePrefix)
		reaching = append(reaching, rel)
		if ok, why := testMainCallsPathstestMain(t, filepath.Join(root, filepath.FromSlash(rel))); !ok {
			missing = append(missing, fmt.Sprintf("%s (links %s, which reads the home in %s): %s",
				rel, via, strings.Join(resolvers[via], ", "), why))
		}
	}
	sort.Strings(missing)
	require.GreaterOrEqual(t, len(reaching), minHomeReachingTestPackages,
		"go list reported too few test packages that can reach the home to prove anything: %v", reaching)
	require.Empty(t, missing,
		"these test packages can reach the user's real home — their test binary links a package that "+
			"resolves it — and do not isolate it. Give each a TestMain that calls pathstest.Main "+
			"(`func TestMain(m *testing.M) { os.Exit(pathstest.Main(m)) }`, with any cleanup of its own "+
			"passed as pathstest.Main's after functions):\n  %s", strings.Join(missing, "\n  "))

	snapshots := scanPackageLevelEnvSnapshots(t, root)
	require.Empty(t, snapshots,
		"these package-level variables capture the environment or the home during package "+
			"initialization, which runs before TestMain isolates the home, so they hold the real one. "+
			"Read pathstest.Environ() (or the environment) where the value is used instead:\n  %s",
		strings.Join(snapshots, "\n  "))
}

// TestGuard_HomeIsolationScannersSeeEveryShape is the static guard's own self-test: a scanner that
// missed a shape would pass the guard above vacuously for every file written that way.
func TestGuard_HomeIsolationScannersSeeEveryShape(t *testing.T) {
	const product = `package p

import (
	sys "os"
)

func a() string { h, _ := sys.UserHomeDir(); return h }

func b() string { return sys.Getenv("QOMPACK_HOME") }

func c() (string, bool) { return sys.LookupEnv("CLAUDE_CONFIG_DIR") }

func d() string { return sys.Getenv("PATH") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", product, parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Equal(t, []string{"a: os.UserHomeDir", "b: os.Getenv(QOMPACK_HOME)", "c: os.LookupEnv(CLAUDE_CONFIG_DIR)"},
		homeLookupsInFile(f), "an aliased os, a lookup call, and both env readers with a home name; not PATH")

	const snapshot = `package p

import "os"

var env = os.Environ()

var (
	fine = 3
	home, _ = os.UserHomeDir()
)

func f() []string { return os.Environ() }
`
	f, err = parser.ParseFile(fset, "s.go", snapshot, parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Equal(t, []string{"env (os.Environ)", "home, _ (os.UserHomeDir)"}, envSnapshotsInFile(f),
		"both package-level forms, and not a call inside a function")

	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	write("a_test.go", "package p\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) { m.Run() }\n")
	ok, why := testMainCallsPathstestMain(t, dir)
	require.False(t, ok, "a TestMain that does not call pathstest.Main isolates nothing")
	require.Contains(t, why, "does not call")
	write("a_test.go", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\tiso \""+pathstestImport+
		"\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(iso.Main(m)) }\n")
	ok, why = testMainCallsPathstestMain(t, dir)
	require.True(t, ok, "an aliased import of pathstest is still pathstest.Main: %s", why)
	require.NoError(t, os.Remove(filepath.Join(dir, "a_test.go")))
	write("b_test.go", "package p\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	ok, why = testMainCallsPathstestMain(t, dir)
	require.False(t, ok)
	require.Contains(t, why, "no TestMain")
}

// scanHomeResolvers returns every product package (module-relative, under internal/ and cmd/) that
// resolves the home itself, with the function-level evidence the scan found.
func scanHomeResolvers(t *testing.T, root string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
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
			pkg := filepath.ToSlash(filepath.Dir(rel))
			if homeResolverExempt[pkg] {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			for _, hit := range homeLookupsInFile(f) {
				found[pkg] = append(found[pkg], filepath.Base(rel)+" "+hit)
			}
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}
	return found
}

// osLocalName returns the name f refers to package os by, or "" when f does not import it. A dot
// import is reported as ".", which no selector can match, so such a file is flagged by name alone.
func osLocalName(f *ast.File) string {
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err != nil || path != "os" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "os"
	}
	return ""
}

// homeLookupsInFile returns "fn: call" for every home lookup in f, in source order: a reference to
// one of homeLookupCalls, or an os.Getenv / os.LookupEnv call whose first argument is a string
// literal naming one of homeEnvNames.
func homeLookupsInFile(f *ast.File) []string {
	osName := osLocalName(f)
	if osName == "" {
		return nil
	}
	var hits []string
	for _, decl := range f.Decls {
		fn := packageScope
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil {
			fn = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if name := osCall(x.Fun, osName); (name == "Getenv" || name == "LookupEnv") && len(x.Args) > 0 {
					if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil && homeEnvNames[v] {
							hits = append(hits, fmt.Sprintf("%s: os.%s(%s)", fn, name, v))
						}
					}
				}
			case *ast.SelectorExpr:
				if name := osCall(x, osName); homeLookupCalls["os."+name] {
					hits = append(hits, fmt.Sprintf("%s: os.%s", fn, name))
				}
			}
			return true
		})
	}
	return hits
}

// osCall returns the selected name when e is <osName>.<Name>, and "" otherwise.
func osCall(e ast.Expr, osName string) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == osName {
		return sel.Sel.Name
	}
	return ""
}

// listTestBinaryDeps returns, for every package that has test files, the module packages its test
// binary links, from `go list -test`.
func listTestBinaryDeps(t *testing.T, root string) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-test", "-f", "{{.ImportPath}}\t{{join .Deps \",\"}}", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "go list -test: %s", stderr.String())
	binaries := map[string][]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		ip, deps, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasSuffix(ip, ".test") {
			continue
		}
		var mine []string
		for _, d := range strings.Split(deps, ",") {
			d, _, _ = strings.Cut(d, " ") // a recompiled dependency is listed as "path [pkg.test]"
			if strings.HasPrefix(d, modulePrefix) {
				mine = append(mine, d)
			}
		}
		binaries[strings.TrimSuffix(ip, ".test")] = mine
	}
	return binaries
}

// testMainCallsPathstestMain reports whether the test files in dir declare a TestMain that calls
// pathstest.Main, and why not when they do not.
func testMainCallsPathstestMain(t *testing.T, dir string) (bool, string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	require.NoError(t, err)
	for _, p := range files {
		f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		require.NoError(t, perr, "parsing %s", p)
		local := ""
		for _, imp := range f.Imports {
			if path, uerr := strconv.Unquote(imp.Path.Value); uerr == nil && path == pathstestImport {
				local = "pathstest"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != "TestMain" || fd.Body == nil {
				continue
			}
			calls := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && local != "" && osCall(c.Fun, local) == "Main" {
					calls = true
				}
				return !calls
			})
			if calls {
				return true, ""
			}
			return false, "the TestMain in " + filepath.Base(p) + " does not call pathstest.Main"
		}
	}
	return false, "no TestMain in its test files"
}

// scanPackageLevelEnvSnapshots returns "file: var (call)" for every package-level variable in the
// tree initialized with an envSnapshotCalls call or a home-naming os.Getenv / os.LookupEnv.
func scanPackageLevelEnvSnapshots(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, top := range []string{"internal", "cmd", "test", "tools"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if d.Name() == "testdata" || p == filepath.Join(root, "tools", "pinned") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			rel, _ := filepath.Rel(root, p)
			for _, hit := range envSnapshotsInFile(f) {
				found = append(found, filepath.ToSlash(rel)+": "+hit)
			}
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}
	sort.Strings(found)
	return found
}

// envSnapshotsInFile returns "name (call)" for every package-level variable of f whose initializer
// calls an envSnapshotCalls function or reads a homeEnvNames variable.
func envSnapshotsInFile(f *ast.File) []string {
	osName := osLocalName(f)
	if osName == "" {
		return nil
	}
	var hits []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			var names []string
			for _, n := range vs.Names {
				names = append(names, n.Name)
			}
			for _, v := range vs.Values {
				ast.Inspect(v, func(n ast.Node) bool {
					c, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					name := osCall(c.Fun, osName)
					hit := envSnapshotCalls["os."+name]
					if (name == "Getenv" || name == "LookupEnv") && len(c.Args) > 0 {
						if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							v, err := strconv.Unquote(lit.Value)
							hit = err == nil && homeEnvNames[v]
						}
					}
					if hit {
						hits = append(hits, fmt.Sprintf("%s (os.%s)", strings.Join(names, ", "), name))
					}
					return true
				})
			}
		}
	}
	return hits
}

// The sentinel. Each poison is chosen so that reading it changes an answer the child can see, and
// the parent proves each one would, with a control read that names the fake home explicitly.
const (
	// homeSentinelEnv names the fake real home to the child run; its presence is what makes
	// TestGuard_HomeSentinelChildProcess a child rather than a no-op.
	homeSentinelEnv = "QOMPACK_GUARD_HOME_SENTINEL"
	// homeSentinelProjectEnv names the child's project directory, which the poisoned calibration
	// entry is keyed to.
	homeSentinelProjectEnv = "QOMPACK_GUARD_HOME_SENTINEL_PROJECT"
	// poisonBudgetTokens is the poisoned user-global config value: valid (checkpoint.budgetTokens
	// is bounded to [1000, 100000]) and unlike the 12000 default.
	poisonBudgetTokens = 4242
	// poisonCalibrationObserved/poisonCalibrationEstimated is the ratio of every poisoned calibration
	// sample, 3.0, well past the default calibrationMax of 1.6, so the persisted factor is that clamp
	// and not the identity.
	poisonCalibrationObserved, poisonCalibrationEstimated = 300, 100
	// poisonCalibrationSamples is comfortably past the estimator's five-sample warm-up, after which
	// the factor leaves the identity.
	poisonCalibrationSamples = 8
	// poisonDenyAll is a Claude Code settings file that denies reading anything in the project.
	poisonDenyAll = `{"permissions":{"deny":["Read(./**)"]}}`
)

// TestGuard_IsolatedTestsNeverReadAPoisonedRealHome is the sentinel proof that pathstest's isolation
// holds for every home lookup the product makes: the calibration file (QOMPACK_HOME and the home),
// the user-global config layer (HOME and USERPROFILE through internal/cli), and Claude Code's
// settings (CLAUDE_CONFIG_DIR and the home through internal/hostperm). It fails if the child reads
// any poison, or if anything under the fake real home is created, changed or removed.
func TestGuard_IsolatedTestsNeverReadAPoisonedRealHome(t *testing.T) {
	require.NotEmpty(t, pathstest.Home(), "test/guards' own TestMain must have isolated the home")

	base := t.TempDir()
	realHome := filepath.Join(base, "real-home")
	project := filepath.Join(base, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(project, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project, "src", "a.go"), []byte("package a\n"), 0o600))
	cfg := config.Defaults()

	require.NoError(t, os.MkdirAll(paths.Global(realHome), 0o700))
	calibPath := filepath.Join(paths.Global(realHome), "calibration.json")
	poisoned := tokens.NewForProject(cfg, calibPath, project)
	for range poisonCalibrationSamples {
		poisoned.Calibrate(poisonCalibrationObserved, poisonCalibrationEstimated)
	}
	require.Equal(t, cfg.Runtime.Tokens.CalibrationMax, tokens.NewForProject(cfg, calibPath, project).Factor(),
		"control: an estimator pointed at the fake home's calibration.json must read the poisoned factor")

	cfgPath := config.UserConfigPath(realHome)
	require.NoError(t, os.WriteFile(cfgPath, fmt.Appendf(nil, `{"checkpoint":{"budgetTokens":%d}}`, poisonBudgetTokens), 0o600))
	ctl, _, _, err := config.Load(config.Env{ProjectRoot: project, HomeDir: realHome, Getenv: func(string) string { return "" }})
	require.NoError(t, err)
	require.Equal(t, poisonBudgetTokens, ctl.Checkpoint.BudgetTokens,
		"control: a load with the fake home as its home must apply the poisoned user-global layer")

	settings := filepath.Join(realHome, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o700))
	require.NoError(t, os.WriteFile(settings, []byte(poisonDenyAll), 0o600))
	d, err := hostperm.New(hostperm.Options{
		ProjectRoot: project, Home: realHome, Getenv: func(string) string { return "" },
		Managed: &hostperm.ManagedSources{},
	}).Check(filepath.Join(project, "src", "a.go"))
	require.NoError(t, err)
	require.Equal(t, hostperm.Deny, d.Effect, "control: a policy anchored at the fake home must read its deny rule")

	before := snapshotHome(t, realHome)

	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestGuard_HomeSentinelChildProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(),
		"HOME="+realHome,
		"USERPROFILE="+realHome,
		"QOMPACK_HOME="+paths.Global(realHome),
		"CLAUDE_CONFIG_DIR="+filepath.Join(realHome, ".claude"),
		homeSentinelEnv+"="+realHome,
		homeSentinelProjectEnv+"="+project,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the child test process read the poisoned real home or failed:\n%s", out)
	require.Contains(t, string(out), "--- PASS: TestGuard_HomeSentinelChildProcess",
		"the child must actually have run its checks:\n%s", out)

	require.Equal(t, before, snapshotHome(t, realHome),
		"a test process wrote under the real home's .qompack or .claude")
}

// TestGuard_HomeSentinelChildProcess is the child half of
// TestGuard_IsolatedTestsNeverReadAPoisonedRealHome, which starts this test binary again with the
// home pointing at a poisoned fake. Run by itself, with no sentinel in the environment, it has
// nothing to check and returns at once.
func TestGuard_HomeSentinelChildProcess(t *testing.T) {
	realHome := os.Getenv(homeSentinelEnv)
	if realHome == "" {
		t.Log("not a sentinel child; TestGuard_IsolatedTestsNeverReadAPoisonedRealHome runs this")
		return
	}
	project := os.Getenv(homeSentinelProjectEnv)
	require.NotEmpty(t, project)

	home := pathstest.Home()
	require.NotEmpty(t, home, "TestMain must have isolated the home before this test ran")
	require.NotEqual(t, realHome, home)
	for _, k := range []string{"HOME", "USERPROFILE"} {
		require.Equal(t, home, os.Getenv(k), "%s must name the isolated home, not the real one", k)
	}
	got, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, home, got)

	// The calibration file: tokens.DefaultCalibPath reads QOMPACK_HOME, then the home.
	calib := tokens.DefaultCalibPath()
	require.True(t, strings.HasPrefix(calib, home), "the calibration file must resolve under the isolated home: %s", calib)
	est := tokens.NewForProject(config.Defaults(), calib, project)
	require.Equal(t, 1.0, est.Factor(), "the estimator read the poisoned real calibration.json")
	for range poisonCalibrationSamples {
		est.Calibrate(poisonCalibrationObserved, poisonCalibrationEstimated) // a write, which must land in the isolated home
	}

	// The user-global config layer, through the CLI's own home resolution (HOME, then USERPROFILE).
	t.Setenv("QOMPACK_PROJECT_ROOT", project)
	var stdout, stderr bytes.Buffer
	code := cli.Dispatch(context.Background(), cli.All(), []string{"qompack", "config", "print", "--json"},
		cli.Env{Getenv: os.Getenv}, &stdout, &stderr)
	require.Equal(t, 0, code, "config print: %s", stderr.String())
	var printed config.Config
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &printed))
	require.Equal(t, config.Defaults().Checkpoint.BudgetTokens, printed.Checkpoint.BudgetTokens,
		"config print applied the poisoned real user-global config.json")

	// Claude Code's settings: CLAUDE_CONFIG_DIR, then the home's .claude.
	d, err := hostperm.New(hostperm.Options{ProjectRoot: project, Managed: &hostperm.ManagedSources{}}).
		Check(filepath.Join(project, "src", "a.go"))
	require.NoError(t, err)
	require.Equal(t, hostperm.Allow, d.Effect, "a default policy read the poisoned real Claude Code settings")
}

// snapshotHome maps every file and directory under dir to its type and, for a file, its size,
// modification time and content hash, so any creation, change or removal shows up as a difference.
//
// A directory's own modification time is left out: NTFS applies it lazily, so a snapshot taken just
// after the parent's own writes can read a directory time older than a file inside it, and the next
// snapshot then differs with nothing having been written (measured while writing this guard). A
// file created or removed in a directory still shows up, as an entry that appears or disappears.
func snapshotHome(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		entry := info.Mode().Type().String()
		if info.Mode().IsRegular() {
			entry = fmt.Sprintf("%v %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			sum := sha256.Sum256(b)
			entry += " " + hex.EncodeToString(sum[:])
		}
		snap[filepath.ToSlash(rel)] = entry
		return nil
	}))
	return snap
}

package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// The co-load policy has two halves, and this file guards the seam between them.
//
// internal/obs.UnderColoadEnv lets the whole-tree jobs (ci.yml's `test`, nightly's `race-windows`,
// `devtool test`) declare that ~twenty package binaries are sharing a two-core runner, and a test
// that reads obs.UnderCoload may then move a wall-clock JUDGEMENT to CPU time or report the
// measurement instead of gating on it. That is only sound if every such test is still judged, on
// the wall clock it was written against, by a job that does NOT make the declaration: ci.yml's
// `timing` lane, which runs them by name with -p 1 on an otherwise idle runner, and `test-e2e`,
// which runs test/e2e alone for the same reason.
//
// Nothing in the toolchain connects the two halves. A test can start consulting obs.UnderCoload in
// one commit and nobody has to touch ci.yml; the whole-tree job goes green, the `timing` lane goes
// on running the tests it already named, and the new test's budget is deferred everywhere and
// judged nowhere. That is the failure this file exists to prevent: a gate that reads as "deferred
// to the isolation lane" in one job's log while no lane ever picks it up. The lane also drifts the
// other way — a test that stops consulting the declaration, or is renamed, leaves a name in the
// -run alternation that matches nothing, and `go test -run` prints ok for a pattern that matches
// nothing rather than failing.
//
// So the tests below compute the set of yielders FROM SOURCE — every Test function that reaches an
// obs.UnderCoload() call, transitively through helpers in its own package's test files, since the
// negknow budgets consult it inside requireBudget rather than in each Test body — and compare that
// set, in both directions, against what ci.yml actually runs in isolation.

// coloadYieldPkgIdent and coloadYieldFunc are the two halves of the exact expression a yielding
// test spells, obs.UnderCoload(). The identifier is matched literally rather than resolved through
// the import table, which is the same trade every guard in this directory makes — an aliased
// import of internal/obs would hide a yielder from this file, and would also be the only aliased
// import of obs in the tree.
const (
	coloadYieldPkgIdent = "obs"
	coloadYieldFunc     = "UnderCoload"
)

// coloadYielderSkipDirs are directory names the yielder walk does not descend into: the VCS
// store, agent scratch directories, vendored JavaScript under plugin/, and testdata trees (which
// `go test` never compiles, so a _test.go under one is not a test).
var coloadYielderSkipDirs = map[string]bool{
	".git":         true,
	".superpowers": true,
	"node_modules": true,
	"testdata":     true,
}

// coloadYielderSkipPkgs are package directories, relative to the module root, whose test files
// are excluded from the yielder set even though they spell obs.UnderCoload():
//
//   - test/guards is this file. Its own source spells the selector only as two string constants,
//     but a guard that inspected itself would be one refactor away from a self-referential failure.
//   - internal/obs is the DECLARING package. coload_test.go's TestUnderCoload_ReadsTheDeclaration
//     calls obs.UnderCoload() to pin what the function returns under t.Setenv; it does not yield
//     a judgement to it, and running it in the isolation lane would judge nothing.
var coloadYielderSkipPkgs = map[string]bool{
	"test/guards":  true,
	"internal/obs": true,
}

// coloadE2EPkg is the one package whose yielders are judged by `test-e2e` rather than by name in
// `timing`: the whole package runs alone on its own runner, so naming its tests would be
// redundant, and the job comment on ci.yml's test-e2e says why the package is out of the tree run.
const coloadE2EPkg = "./test/e2e"

// coloadIsolationLane and coloadE2ELane are the two ci.yml jobs that judge yielders in isolation,
// coloadWholeTreeJob is the ci.yml job that declares co-load, and coloadNightlyRaceJob is
// nightly.yml's whole-tree job that makes the same declaration for the same run shape.
const (
	coloadIsolationLane  = "timing"
	coloadE2ELane        = "test-e2e"
	coloadWholeTreeJob   = "test"
	coloadNightlyRaceJob = "race-windows"
)

var (
	// workflowJobHeaderRE matches the line that opens a job under `jobs:` — a two-space-indented
	// key with no value. Everything until the next such line belongs to that job.
	workflowJobHeaderRE = regexp.MustCompile(`^  ([a-z0-9-]+):\s*$`)
	// workflowRunRE matches a step's `run:` line, in either the `- run:` or the `run:` position,
	// and captures the command (or the `|` / `>` block-scalar indicator).
	workflowRunRE = regexp.MustCompile(`^(\s*)(?:-\s+)?run:\s*(.*)$`)
	// workflowColoadEnvRE is the env-block line that makes the declaration. It is built from the
	// Go constant, so a rename of UnderColoadEnv that is not mirrored in the YAML reads here as the
	// whole-tree job no longer declaring anything — see TestColoadDeclarationIsPinnedToTheGoConstant.
	workflowColoadEnvRE = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(obs.UnderColoadEnv) + `:\s*\S`)
	// workflowColoadShapedEnvRE catches the other direction of the same drift: any env key that
	// LOOKS like the co-load declaration. Every match must spell obs.UnderColoadEnv exactly.
	workflowColoadShapedEnvRE = regexp.MustCompile(`(?m)^\s*(QOMPACK_[A-Z_]*CO_?LOAD[A-Z_]*):`)
	// timingRunPatternRE pulls the alternation out of the lane's `-run '^(A|B|C)$'` argument. The
	// anchored, parenthesised shape is deliberate and asserted: an unanchored pattern would match
	// tests by substring, and a lane that ran TestBudget_OpenSomethingElse by accident would be
	// reporting isolation for a test nobody asked it to judge.
	timingRunPatternRE = regexp.MustCompile(`-run\s+'\^\(([^)']*)\)\$'`)
	// timingPkgArgRE lists the package arguments of the lane's go test line.
	timingPkgArgRE = regexp.MustCompile(`(?:^|\s)(\./\S+)`)
	// timingSerialFlagRE is the -p 1 that makes the lane one binary at a time. Without it, the
	// lane's three packages would share the runner with each other, which is a smaller version of
	// the co-load the lane exists to escape.
	timingSerialFlagRE = regexp.MustCompile(`(?:^|\s)-p\s+1(?:\s|$)`)
	// goTestNameRE is `go test`'s own rule for which functions are tests: Test followed by
	// something that is not a lowercase letter (or nothing at all).
	goTestNameRE = regexp.MustCompile(`^Test(\P{Ll}|$)`)
)

// coloadYielder is one Test function that reaches obs.UnderCoload(). Pkg is spelled the way the
// lane's package arguments are (`./internal/store`) so the two compare without translation.
type coloadYielder struct {
	Pkg  string
	Name string
}

func (y coloadYielder) String() string { return y.Pkg + ":" + y.Name }

// TestColoadYieldersAreJudgedInIsolation is the guard obs.UnderColoadEnv's doc comment promises:
// a test that reads obs.UnderCoload and appears in neither `timing`'s -run alternation nor
// test/e2e (which `test-e2e` runs whole) fails the tree. It also fails when the alternation names
// a test that is no longer a yielder, when a yielder's package is missing from the lane's package
// list (a name in -run matches nothing in a package that is not run), and when either isolation
// job has quietly acquired the co-load declaration, which would make it isolation in name only.
//
// The yielder set is computed from source rather than transcribed, because the transcription is
// exactly the list that goes stale: the lane's -run alternation IS the hand-maintained copy, and
// this test is what checks it.
func TestColoadYieldersAreJudgedInIsolation(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	yielders := collectColoadYielders(t, root)
	require.NotEmpty(t, yielders,
		"no Test function in the tree reaches %s.%s(); the co-load policy has no yielders to "+
			"judge, which either means the declaration is dead code or this guard's walker broke",
		coloadYieldPkgIdent, coloadYieldFunc)
	t.Logf("%d yielders found from source: %v", len(yielders), yielders)

	jobs := workflowJobs(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	timing, ok := jobs[coloadIsolationLane]
	require.True(t, ok, "ci.yml has no `%s` job: the isolation lane that judges yielders by name is gone", coloadIsolationLane)
	require.False(t, timing.setsCoload,
		"ci.yml's `%s` job sets %s — it is the lane that judges wall-clock rows in isolation, and "+
			"the declaration would make every yielder defer there too, so nothing would ever be judged",
		coloadIsolationLane, obs.UnderColoadEnv)

	e2e, ok := jobs[coloadE2ELane]
	require.True(t, ok, "ci.yml has no `%s` job: test/e2e's wall-clock rows have no isolated judge", coloadE2ELane)
	require.False(t, e2e.setsCoload,
		"ci.yml's `%s` job sets %s — it is the isolation in which test/e2e's wall-clock rows are "+
			"judged, and the declaration would defer them there too", coloadE2ELane, obs.UnderColoadEnv)

	// The lane's go test line: one run step, anchored alternation, serial packages.
	var laneRun string
	for _, run := range timing.runs {
		if strings.HasPrefix(run, "go test") {
			require.Empty(t, laneRun, "ci.yml's `%s` job has more than one `go test` step; this guard reads exactly one", coloadIsolationLane)
			laneRun = run
		}
	}
	require.NotEmpty(t, laneRun, "ci.yml's `%s` job has no `go test` step", coloadIsolationLane)
	require.Regexp(t, timingSerialFlagRE, laneRun,
		"ci.yml's `%s` job must run with -p 1: its packages otherwise share the runner with each "+
			"other, which is the co-load the lane exists to escape", coloadIsolationLane)
	m := timingRunPatternRE.FindStringSubmatch(laneRun)
	require.NotNil(t, m,
		"ci.yml's `%s` job must select its tests with an anchored alternation, -run '^(A|B)$'; "+
			"got: %s", coloadIsolationLane, laneRun)
	laneNames := map[string]bool{}
	for _, name := range strings.Split(m[1], "|") {
		require.NotEmpty(t, name, "ci.yml's `%s` -run alternation has an empty branch, which matches every test", coloadIsolationLane)
		laneNames[name] = true
	}
	lanePkgs := map[string]bool{}
	for _, p := range timingPkgArgRE.FindAllStringSubmatch(laneRun, -1) {
		lanePkgs[p[1]] = true
	}
	require.NotEmpty(t, lanePkgs, "ci.yml's `%s` go test line names no packages", coloadIsolationLane)

	// `test-e2e` runs the whole package: a run step naming ./test/e2e with no -run filter.
	e2eWhole := false
	for _, run := range e2e.runs {
		if strings.HasPrefix(run, "go test") && strings.Contains(run, " "+coloadE2EPkg) && !strings.Contains(run, "-run") {
			e2eWhole = true
		}
	}
	require.True(t, e2eWhole,
		"ci.yml's `%s` job must run `go test ... %s` whole, with no -run filter: it is the only "+
			"job that judges test/e2e's yielders, and it does so by running the package, not by name",
		coloadE2ELane, coloadE2EPkg)

	// Direction one: every yielder is judged somewhere that does not set the declaration.
	yielderNames := map[string]bool{}
	for _, y := range yielders {
		if y.Pkg == coloadE2EPkg {
			t.Logf("%s is judged by `%s`, which runs %s whole", y, coloadE2ELane, coloadE2EPkg)
			continue
		}
		yielderNames[y.Name] = true
		require.True(t, laneNames[y.Name],
			"%s consults %s.%s() but is not named in ci.yml's `%s` -run alternation. The whole-tree "+
				"job defers its wall-clock judgement to the isolation lane, and the lane does not run "+
				"it, so its budget is judged nowhere. Add %s to the alternation (and %s to the package "+
				"list if it is not there).",
			y, coloadYieldPkgIdent, coloadYieldFunc, coloadIsolationLane, y.Name, y.Pkg)
		require.True(t, lanePkgs[y.Pkg],
			"%s is named in ci.yml's `%s` -run alternation but %s is not among the lane's package "+
				"arguments, so the name matches nothing and `go test -run` prints ok. Add %s to the "+
				"package list.",
			y, coloadIsolationLane, y.Pkg, y.Pkg)
	}

	// Direction two: the lane names only real yielders, and runs no package for nothing.
	for name := range laneNames {
		require.True(t, yielderNames[name],
			"ci.yml's `%s` -run alternation names %s, but no Test function of that name outside "+
				"%s reaches %s.%s(). Either the test was renamed or deleted (fix the alternation) or it "+
				"stopped consulting the declaration and is judged in the whole-tree job already "+
				"(drop it from the lane). A name that matches nothing makes `go test -run` print ok.",
			coloadIsolationLane, name, coloadE2EPkg, coloadYieldPkgIdent, coloadYieldFunc)
	}
	for pkg := range lanePkgs {
		found := false
		for _, y := range yielders {
			if y.Pkg == pkg {
				found = true
			}
		}
		require.True(t, found,
			"ci.yml's `%s` job builds and runs %s, but no test in it reaches %s.%s(); the lane "+
				"would compile a binary to match nothing. Drop the package from the lane.",
			coloadIsolationLane, pkg, coloadYieldPkgIdent, coloadYieldFunc)
	}

	t.Logf("every yielder is judged in isolation: %d by name in `%s`, the rest by `%s`",
		len(yielderNames), coloadIsolationLane, coloadE2ELane)
}

// TestColoadDeclarationIsPinnedToTheGoConstant keeps the YAML spelling of the declaration equal to
// obs.UnderColoadEnv and made by exactly the whole-tree jobs.
//
// The pin matters in both directions and neither fails on its own. Rename the Go constant and the
// workflows go on exporting the old name: every yielder stops seeing the declaration, the
// whole-tree job starts judging wall-clock rows under co-load again, and the first symptom is the
// flaky-p99 history docs/adr/0010 was written to end. Rename it in the YAML only and the same thing
// happens from the other side. devtool's wholeTreeEnv imports the constant and cannot drift; the
// two workflows are text, and this is their import.
func TestColoadDeclarationIsPinnedToTheGoConstant(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	workflows := filepath.Join(root, ".github", "workflows")

	ci := workflowJobs(t, filepath.Join(workflows, "ci.yml"))
	test, ok := ci[coloadWholeTreeJob]
	require.True(t, ok, "ci.yml has no `%s` job", coloadWholeTreeJob)
	require.True(t, test.setsCoload,
		"ci.yml's `%s` job does not set %s. It is the whole-tree run the declaration describes; "+
			"without it every wall-clock budget is judged under co-load, which is the history "+
			"docs/adr/0010 records. If the constant was renamed, rename it in the YAML too.",
		coloadWholeTreeJob, obs.UnderColoadEnv)

	nightly := workflowJobs(t, filepath.Join(workflows, "nightly.yml"))
	race, ok := nightly[coloadNightlyRaceJob]
	require.True(t, ok, "nightly.yml has no `%s` job", coloadNightlyRaceJob)
	require.True(t, race.setsCoload,
		"nightly.yml's `%s` job does not set %s. It runs the same whole tree under -race, the "+
			"slowest run shape there is, and makes the same declaration ci.yml's `%s` does.",
		coloadNightlyRaceJob, obs.UnderColoadEnv, coloadWholeTreeJob)

	// Every co-load-shaped env key in either workflow spells the constant exactly. A job that sets
	// QOMPACK_UNDER_COLOAD_V2 or QOMPACK_COLOAD sets nothing any test reads.
	for _, name := range []string{"ci.yml", "nightly.yml"} {
		body := liveWorkflowText(t, filepath.Join(workflows, name))
		shaped := workflowColoadShapedEnvRE.FindAllStringSubmatch(body, -1)
		require.NotEmpty(t, shaped, "%s sets no co-load-shaped variable at all", name)
		for _, s := range shaped {
			require.Equal(t, obs.UnderColoadEnv, s[1],
				"%s sets %s, which no test reads; the declaration internal/obs.UnderCoload consults "+
					"is spelled %s", name, s[1], obs.UnderColoadEnv)
		}
	}
}

// collectColoadYielders walks every _test.go under root, outside coloadYielderSkipDirs and
// coloadYielderSkipPkgs, and returns every Test function that reaches an obs.UnderCoload() call,
// transitively through function declarations in the same package's test files.
//
// Reachability is by NAME within a (directory, package) pair, and it over-approximates on purpose:
// any identifier in a function's body that names a same-package test-file function is an edge,
// whether it is called or passed as a value (requireBudget receives its benchmark as a value), and
// any selector's method name that names a same-package method is an edge too. A false edge makes
// the guard ask for a test to be added to the lane that did not strictly need it — a review-time
// question with a cheap answer. A missed edge is the silent failure this file exists to prevent.
func collectColoadYielders(t *testing.T, root string) []coloadYielder {
	t.Helper()

	type testPkgKey struct{ dir, pkg string }
	type fnDecl struct {
		isTest   bool
		yields   bool
		refs     map[string]bool // identifiers referenced in the body
		selRefs  map[string]bool // selector method names referenced in the body
		isMethod bool
	}
	decls := map[testPkgKey]map[string]*fnDecl{}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if coloadYielderSkipDirs[d.Name()] || coloadYielderSkipPkgs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		key := testPkgKey{dir: filepath.ToSlash(filepath.Dir(rel)), pkg: f.Name.Name}
		if decls[key] == nil {
			decls[key] = map[string]*fnDecl{}
		}
		for _, decl := range f.Decls {
			fd, isFn := decl.(*ast.FuncDecl)
			if !isFn || fd.Body == nil {
				continue
			}
			info := &fnDecl{
				isTest:   fd.Recv == nil && isTestFunc(fd),
				refs:     map[string]bool{},
				selRefs:  map[string]bool{},
				isMethod: fd.Recv != nil,
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, isIdent := x.X.(*ast.Ident); isIdent && id.Name == coloadYieldPkgIdent && x.Sel.Name == coloadYieldFunc {
						info.yields = true
					}
					info.selRefs[x.Sel.Name] = true
				case *ast.Ident:
					info.refs[x.Name] = true
				}
				return true
			})
			// Two declarations can share a name (a function and a method, or methods on two
			// types); merging their edges keeps the over-approximation one-directional.
			if prev, dup := decls[key][fd.Name.Name]; dup {
				prev.yields = prev.yields || info.yields
				prev.isTest = prev.isTest || info.isTest
				prev.isMethod = prev.isMethod || info.isMethod
				for r := range info.refs {
					prev.refs[r] = true
				}
				for r := range info.selRefs {
					prev.selRefs[r] = true
				}
				continue
			}
			decls[key][fd.Name.Name] = info
		}
		return nil
	})
	require.NoError(t, err)

	var yielders []coloadYielder
	for key, fns := range decls {
		// Propagate "reaches a yield" backwards over name edges until nothing changes. The graphs
		// are a few hundred functions at most; a fixed point is simpler than an explicit search.
		for changed := true; changed; {
			changed = false
			for _, fn := range fns {
				if fn.yields {
					continue
				}
				for ref := range fn.refs {
					if callee, known := fns[ref]; known && !callee.isMethod && callee.yields {
						fn.yields, changed = true, true
						break
					}
				}
				if fn.yields {
					continue
				}
				for ref := range fn.selRefs {
					if callee, known := fns[ref]; known && callee.isMethod && callee.yields {
						fn.yields, changed = true, true
						break
					}
				}
			}
		}
		for name, fn := range fns {
			if fn.isTest && fn.yields {
				yielders = append(yielders, coloadYielder{Pkg: "./" + key.dir, Name: name})
			}
		}
	}
	sort.Slice(yielders, func(i, j int) bool { return yielders[i].String() < yielders[j].String() })
	return yielders
}

// isTestFunc reports whether fd is a function `go test` would run: a TestXxx name and a single
// *testing.T parameter.
func isTestFunc(fd *ast.FuncDecl) bool {
	if !goTestNameRE.MatchString(fd.Name.Name) || fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	star, ok := fd.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// workflowJob is what this file needs to know about one job in a workflow: whether its live
// (non-comment) lines set the co-load declaration anywhere — job env or step env, the guard does
// not care which — and the commands of its run steps, block scalars flattened to one line.
type workflowJob struct {
	setsCoload bool
	runs       []string
}

// workflowJobs splits a workflow's live text into its jobs by the two-space-indented headers under
// `jobs:`. Comment lines are dropped first: every job here explains the declaration in prose, and
// a guard that read its own rationale as a setting would be unusable — the same reason
// goversion_test.go strips them.
func workflowJobs(t *testing.T, path string) map[string]workflowJob {
	t.Helper()

	lines := strings.Split(liveWorkflowText(t, path), "\n")
	jobs := map[string]workflowJob{}
	var name string
	var body []string
	flush := func() {
		if name == "" {
			return
		}
		jobs[name] = parseWorkflowJob(body)
	}
	inJobs := false
	for _, line := range lines {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := workflowJobHeaderRE.FindStringSubmatch(line); m != nil {
			flush()
			name, body = m[1], nil
			continue
		}
		body = append(body, line)
	}
	flush()
	require.NotEmpty(t, jobs, "%s: parsed no jobs; if the workflow's layout changed, update this guard rather than deleting it", path)
	return jobs
}

// parseWorkflowJob reads one job's lines into a workflowJob. A `run: |` or `run: >` block scalar
// is joined from the more-indented lines that follow it, so a multi-line command is one string.
func parseWorkflowJob(lines []string) workflowJob {
	var job workflowJob
	job.setsCoload = workflowColoadEnvRE.MatchString(strings.Join(lines, "\n"))
	for i := 0; i < len(lines); i++ {
		m := workflowRunRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent, cmd := len(m[1]), strings.TrimSpace(m[2])
		if strings.HasPrefix(cmd, "|") || strings.HasPrefix(cmd, ">") {
			var parts []string
			for i+1 < len(lines) && (strings.TrimSpace(lines[i+1]) == "" || leadingSpaces(lines[i+1]) > indent) {
				i++
				parts = append(parts, strings.TrimSpace(lines[i]))
			}
			cmd = strings.TrimSpace(strings.Join(parts, " "))
		}
		job.runs = append(job.runs, cmd)
	}
	return job
}

// leadingSpaces is the YAML indent of line.
func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// liveWorkflowText returns a workflow file with its comment lines removed.
func liveWorkflowText(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var live []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			live = append(live, line)
		}
	}
	return strings.Join(live, "\n")
}

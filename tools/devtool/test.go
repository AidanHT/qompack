package main

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/obs"
)

// taskTest runs the fmt-check gate and then the whole tree's `go test` passes (testInvocations):
// every package but test/e2e together under the co-load declaration, then test/e2e alone without
// it, as ADR 0010 decision 4 and ci.yml's `test-e2e` job run it. Both passes always run, so a red
// in the first does not leave test/e2e unrun, and the task fails if either failed.
//
// fmt-check is here because of V2-MERGE-24. `devtool fmt-check` runs the pinned gofumpt, which is
// strictly stricter than gofmt, and SP-04's branch tip failed it — its own exit criterion — with
// nothing in the wave noticing: internal/chunk/fastcdc_test.go carried a blank line before a
// closing brace from SP-04's first chunk commit through every later commit and the merge. Per-
// commit gating is this task, which did not include fmt-check, so only the final-commit ci-local
// could have caught it and evidently was not run, or was run before the last commit.
//
// The decision the V2 checkpoint took is to put the cheapest step in the pipeline where the
// per-commit gate is, rather than to add enforcement that a subplan ran ci-local at its tip.
// gofumpt over the whole tree costs a couple of seconds against a suite that costs minutes, and
// it is the step that halts ci-local before anything informative runs — so paying for it here
// converts a wasted ci-local into a formatting fix made at the commit that caused it.
//
// ci-local therefore runs fmt-check twice. That is deliberate and it is not a bug to optimise
// away: the ordering that matters is that fmt-check comes FIRST in both, and making ci-local's
// `test` step conditional on its own earlier `fmt-check` would couple two tasks that are meant to
// be independently runnable.
func taskTest(args []string) error {
	if err := taskFmtCheck(nil); err != nil {
		return err
	}
	listOut, listErr, err := runCapture(nil, "go", "list", "./...")
	if err != nil {
		return fmt.Errorf("test: go list ./...: %w\n%s", err, listErr)
	}
	var failed []string
	for _, inv := range testInvocations(strings.Fields(string(listOut))) {
		if err := goInheritEnv(inv.env, inv.args...); err != nil {
			failed = append(failed, fmt.Sprintf("go %s: %v", strings.Join(inv.args, " "), err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("test: %s", strings.Join(failed, "; "))
	}
	return nil
}

// goInvocation is one `go` command: its arguments and the environment it adds or takes back (an
// empty value takes a variable back, which is how the obs declarations read it).
type goInvocation struct {
	args []string
	env  map[string]string
}

// testInvocations is taskTest's `go test` commands over pkgs, one per wholeTreePasses pass, each
// at wholeTreeTestTimeout.
func testInvocations(pkgs []string) []goInvocation {
	passes := wholeTreePasses(pkgs)
	out := make([]goInvocation, 0, len(passes))
	for _, p := range passes {
		args := append([]string{"test", "-timeout=" + wholeTreeTestTimeout}, p.pkgs...)
		out = append(out, goInvocation{args: args, env: p.env})
	}
	return out
}

// testPass is one pass of a whole-tree `go test`: its packages and the environment it adds or
// takes back.
type testPass struct {
	pkgs []string
	env  map[string]string
}

// wholeTreePasses splits pkgs into isolatedPasses and gives each its environment. The shared pass
// is the co-loaded whole-tree run, so it declares co-load (wholeTreeEnv) and takes back a
// non-reference-disk declaration the caller's environment may carry: one cause per run (test/guards'
// TestNonReferenceDisk_IsHostedCIOnly). Each isolated package then runs alone with the co-load
// declaration taken back, exactly as ci.yml's `test-e2e` job runs test/e2e (ADR 0010 decision 4),
// and inherits whatever the caller declares of its disk. taskTest and cover both use it.
func wholeTreePasses(pkgs []string) []testPass {
	var passes []testPass
	for _, p := range isolatedPasses(pkgs) {
		if !isolatedPackages[p[0]] {
			env := map[string]string{obs.NonReferenceDiskEnv: ""}
			for k, v := range wholeTreeEnv {
				env[k] = v
			}
			passes = append(passes, testPass{pkgs: p, env: env})
			continue
		}
		passes = append(passes, testPass{pkgs: p, env: map[string]string{obs.UnderColoadEnv: ""}})
	}
	return passes
}

// wholeTreeEnv is the environment every whole-tree `go test` here runs under. A whole-tree run is
// co-loaded by construction — that is the very fact wholeTreeTestTimeout below provisions for —
// and the tests cannot see it from inside, so the run declares it, exactly as ci.yml's `test` job
// does. What the declaration licenses is documented on obs.UnderColoadEnv.
var wholeTreeEnv = map[string]string{obs.UnderColoadEnv: "1"}

// wholeTreeTestTimeout provisions every whole-tree `go test` for test/integration, whose two
// hot-path suites legitimately spend ~3 quiet minutes spawning real processes (2 000 measured
// spawns plus the degradation state machine). Under `go test ./...` the packages run in
// parallel, so on a loaded or small box that wall time inflates well past go's default 10m
// per-binary timeout -- V2's quiet ci-local watched every test pass isolated in 180s and still
// die at the default kill when sharing the machine with the store/e2e/guards suites. This is
// headroom for contention, not a slackened check: every budget the suite enforces is asserted
// in-test and unchanged.
const wholeTreeTestTimeout = "30m"

// isolatedPackages are the packages a whole-tree `go test` here runs in a pass of their own,
// after the parallel pass over the rest of the tree: test/e2e, the package of intrinsically
// wall-clock rows that ADR 0010 decision 4 takes out of the whole-tree run and ci.yml's `test` job
// leaves to `test-e2e`. Beside the rest of the tree on a small runner its rows' hooks take the
// designed degrade to the client spool and its binary can outrun -timeout (run 36816905394: killed
// at 30 minutes in lint-windows' stubskips; TestE2EHookRoundTrip red in cover). Alone, each pass
// keeps the same -timeout, so a hang is still killed and still reported.
var isolatedPackages = map[string]bool{modulePath + "/test/e2e": true}

// isolatedPasses splits pkgs into the `go test` passes a whole-tree run makes, in order: every
// package not in isolatedPackages together, then each isolated package alone. Every package lands
// in exactly one pass, the input order is kept within a pass, and no pass is empty.
func isolatedPasses(pkgs []string) [][]string {
	var shared []string
	var alone [][]string
	for _, p := range pkgs {
		if isolatedPackages[p] {
			alone = append(alone, []string{p})
			continue
		}
		shared = append(shared, p)
	}
	var passes [][]string
	if len(shared) > 0 {
		passes = append(passes, shared)
	}
	return append(passes, alone...)
}

// taskTestRace uses CI's non-e2e race scope (ADR-0010). E2E remains in taskTest's isolated pass
// and the isolated CI test-e2e job. Ordinary -race instruments the e2e harness, not the child go build;
// repeating that known timeout cannot certify races in the actual installed process.
func taskTestRace(args []string) error {
	out, stderr, err := runCapture(nil, "go", "list", "./...")
	if err != nil {
		return fmt.Errorf("test-race: list packages: %w: %s", err, stderr)
	}
	pkgs, err := racePackages(out)
	if err != nil {
		return err
	}
	fmt.Println("test-race: CI non-e2e scope; isolated e2e and any required child-process race evidence remain separate gates")
	argv := append([]string{"test", "-race", "-timeout=" + wholeTreeTestTimeout}, pkgs...)
	return goInheritEnv(wholeTreeEnv, argv...)
}

func racePackages(list []byte) ([]string, error) {
	var pkgs []string
	for _, pkg := range strings.Fields(string(list)) {
		if !strings.HasSuffix(pkg, "/test/e2e") {
			pkgs = append(pkgs, pkg)
		}
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("test-race: no non-e2e packages selected")
	}
	return pkgs, nil
}

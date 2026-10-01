package main

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/obs"
)

// taskTest runs the fmt-check gate and then `go test ./...`.
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
	return goInheritEnv(wholeTreeEnv, "test", "-timeout="+wholeTreeTestTimeout, "./...")
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

// taskTestRace uses CI's non-e2e race scope (ADR-0010). E2E remains in taskTest and the
// isolated CI test-e2e job. Ordinary -race instruments the e2e harness, not the child go build;
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

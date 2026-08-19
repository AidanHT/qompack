package main

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
	return goInherit("test", "-timeout="+wholeTreeTestTimeout, "./...")
}

// wholeTreeTestTimeout provisions every whole-tree `go test` for test/integration, whose two
// hot-path suites legitimately spend ~3 quiet minutes spawning real processes (2 000 measured
// spawns plus the degradation state machine). Under `go test ./...` the packages run in
// parallel, so on a loaded or small box that wall time inflates well past go's default 10m
// per-binary timeout -- V2's quiet ci-local watched every test pass isolated in 180s and still
// die at the default kill when sharing the machine with the store/e2e/guards suites. This is
// headroom for contention, not a slackened check: every budget the suite enforces is asserted
// in-test and unchanged.
const wholeTreeTestTimeout = "30m"

// taskTestRace runs `go test -race ./...`.
func taskTestRace(args []string) error {
	return goInherit("test", "-race", "-timeout="+wholeTreeTestTimeout, "./...")
}

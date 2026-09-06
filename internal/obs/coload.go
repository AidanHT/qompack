package obs

import "os"

// UnderColoadEnv is the environment variable through which the INVOKING JOB declares that the test
// binaries it is running share their host with unrelated concurrent work — a whole-tree
// `go test ./...`, which puts about twenty package binaries on a two-core CI runner at once, and
// the jobs that run it: ci.yml's `test`, nightly's `race-windows`, and `devtool test` /
// `test-race` / `cover`.
//
// It is a statement of fact about the run's environment, made by the only party that knows it, and
// it is spelled the way test/bench/hotpath's --under-coload flag is spelled for the same reason: a
// test cannot tell from inside whether the host it is measuring on is the host it is judging. The
// evidence that the difference matters is CI's own, and it is in the flag's doc comment
// (test/bench/hotpath/main.go, parseFlags): the same commit, the same runner class, minutes apart,
// B-A's p99 measured 3.072 ms with the harness alone on its runner and 18.432 ms inside the
// whole-tree job, against a 15 ms limit, with the product byte-identical.
//
// What a test may do with the declaration is exactly what the harness does with the flag, and
// nothing more:
//
//   - move a wall-clock JUDGEMENT to a clock co-load does not inflate — this process's own CPU
//     time, ProcessCPU, the way internal/dag's TestCrossingLatencyBudget and the harness's B-E_cpu
//     row already do — and keep gating on that;
//   - or, where the property is intrinsically wall-clock (a deadline honoured in wall time, a
//     hook's spawn-to-ack latency) and no such clock exists, REPORT the measurement instead of
//     judging it, with a log line that names the limit not applied and where it still is.
//
// It never justifies a t.Skip (devtool lint's stubskips permits three reasons and this is not one
// of them), and it never relaxes an assertion about what the product DID — only about how long
// the host took to let it. Every test that yields under it is still judged, in isolation, by a
// job that does not set it: ci.yml's `timing` lane names each such test by name, and `test-e2e`
// runs test/e2e alone on its own runner. test/guards' TestColoadYieldersAreJudgedInIsolation is
// what keeps that true — a test that reads UnderCoload and appears in neither job fails the tree.
//
// It lives here, beside Budgets and ProcessCPU, rather than in internal/testutil, because the
// tests that need it most are in-package tests of internal/store and internal/negknow, and
// testutil is a composition root those packages sit underneath (§3.2) — importing it from an
// in-package _test.go is an import cycle. obs is the package every budget is already read from.
//
// Unset by default, so forgetting to set it can only ever make a run STRICTER.
const UnderColoadEnv = "QOMPACK_UNDER_COLOAD"

// UnderCoload reports whether the invoking job has declared this run co-loaded (UnderColoadEnv is
// set to anything non-empty). See UnderColoadEnv for what that declaration licenses.
func UnderCoload() bool {
	return os.Getenv(UnderColoadEnv) != ""
}

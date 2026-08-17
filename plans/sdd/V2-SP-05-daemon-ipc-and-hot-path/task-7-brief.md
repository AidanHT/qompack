# Task 7 — Commit 7: hot-path bench harness, devtool task body, three-platform bench gate, bench baselines

You are adding `test/bench/hotpath` and wiring the CI bench gate in `github.com/qompack/qompack` (worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`). Tasks 1–6 delivered the full transport, daemon, contract monitor, and cli surface. **Read the actual code first: the cli subcommand surface (incl. `qompack version`, `qompack daemon`, the hook clients and their flags), ipc's QOMPACK_IPC_ADDR override and status/admin ops, the daemon's status payload, and `tools/devtool`'s existing task structure (the `bench-hotpath` task exists as a printing no-op you now implement).**

Read IN ORDER:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md`
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md`
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-7-spec.md` — verbatim requirements: the 10-step harness spec, the out.json shape, the CI yaml, the budget-gate rules, benchmarks table, and the commit-7 checklist.
4. `.github/workflows/ci.yml` + `nightly.yml` (current bench-gate placeholder with continue-on-error), `tools/devtool/` (task registration + lint's time.Sleep grep and its exemptions), `test/bench/` (any existing structure).

## Binding rulings (controller decisions amending the plan text)

- devtool already declares the `bench-hotpath` task name printing "harness not present" — replace that body: build the harness program (or invoke it via `go run ./test/bench/hotpath`) forwarding flags `--iterations --hook --warm-daemon --json --project`, print a human summary, and exit non-zero when a gated budget fails. Follow devtool's existing task/flag conventions.
- The harness is a standalone `package main` under `test/bench/hotpath/` (main.go, report.go, payload.go + report_test.go). `time.Sleep` is permitted ONLY here — check devtool lint's grep exemption actually covers test/bench (the inventory says it exempts it; if not, extend the exemption in the lint task and note it).
- Budgets and gating: B-A p99 < 15ms and B-B p99 < 2ms and B-E p99 < 2s gate (exit non-zero on failure); B-D reported, `limit_ms: null`, never gates. Read limits from `config.Defaults()` + `obs.Budgets()` rather than re-hardcoding (the B-A limit is cfg.Runtime.HotPath.BudgetMs; B-B/B-E from cfg.Runtime.Budgets via obs.Budgets()[..].Limit(cfg)).
- Per the spec: spawn-floor via 200 × `qompack version`; B-A_i = max(0, B-D_i − floor_p50) per-sample; `b_a_method` + `spawn_floor_ms` in the artifact; B-E via 50 × `qompack checkpoint` with a PreCompact payload; B-B + observed/estimated pair read from the daemon's `status` op; warm-up = 2000 observe.tool requests over a persistent connection totaling ~40MB deterministic payloads (seed 1, mixed Read/Bash/Grep/Edit shapes 4KB–256KB); `notes` array explains wave-1 stubs (B-C omitted).
- Use QOMPACK_IPC_ADDR + QOMPACK_PROJECT_ROOT to keep everything inside the temp project. Build the real binary with `go build` into the temp dir. The daemon is started as a REAL child process (`qompack daemon`), waited for via admin.ping.
- percentile computation: report_test.go pins it against a hand-checked fixture BEFORE implementation (TDD), plus a golden of the out.json shape (keys, not timings).
- CI: replace the bench-gate placeholder body per the spec's yaml (matrix ubuntu/macos/windows, go 1.26.x, `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-${{ matrix.os }}.json`, upload-artifact) and REMOVE its `continue-on-error: true` (leave replay-gate's alone — SP-02 owns it). Add the same job at `--iterations 5000` to nightly.yml following its existing structure. Keep surrounding workflow structure untouched. (Marking bench-gate as a required check is a GitHub settings action, not a file change — note it in your report for the controller.)
- Add benchmark baselines: run `go test -bench . -benchmem` for BenchmarkReadState / BenchmarkEncodeRequest (internal/ipc), BenchmarkIngestAccept (internal/daemon), BenchmarkServerRoundTrip (internal/ipc) and record the numbers into `testdata/bench-baseline.txt` in whatever format the repo already uses for baselines (check for an existing file/convention; if none, benchstat-compatible raw output with a header comment).
- Run the harness locally on this Windows machine at `--iterations 2000` and record the results in your report. If B-A p99 fails locally, investigate before assuming the budget is unmeetable — look for accidental blocking (config loads on the hot path, missing state file, dial timeouts) and report findings; do NOT relax any budget.

## Process

- TDD: report_test.go first (percentiles, floor subtraction, out.json shape) — confirm RED, then implement.
- Before committing: `go test ./test/bench/... ./internal/...`, devtool fmt/lint/vet, full `go test ./...` once, GOOS=linux+darwin builds, and the local harness run above.
- Exactly ONE commit, exact message below, no attribution trailers:

```
test(sp05): hot-path bench harness, three-platform bench gate, and fixtures

B-A is measured with real process spawns against a warm daemon because that is the
only honest way to measure it; B-D is reported alongside it and never gated, since
process creation is the host's cost and hiding it inside B-A would be the exact
unmeasured-system sin the design document indicts.

Refs: SP-05, §8.1, §11.3, 00-ARCHITECTURE §7, §8
```

## Report

Full report → `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-7-report.md` (TDD evidence, local bench numbers incl. B-A/B-B/B-D/B-E percentiles, files changed, self-review, concerns). Reply with ONLY: Status, commit SHA+subject, one-line test summary incl. local B-A p99, concerns, report path.

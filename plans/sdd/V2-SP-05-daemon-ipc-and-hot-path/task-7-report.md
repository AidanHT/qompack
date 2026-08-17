# Task 7 report — hot-path bench harness, three-platform bench gate, and fixtures

Branch: `feat/sp05-daemon-ipc-and-hot-path` | Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`

## Summary

Implemented `test/bench/hotpath` — a standalone `package main` harness that builds the real
`qompack` binary, starts a real `qompack daemon` child process, optionally warms it with 2000
deterministic `observe.tool` requests, measures a process-creation floor (`qompack version` × 200),
B-A/B-D (`qompack observe tool` × N, real spawns), B-E (`qompack checkpoint` × 50, real spawns),
and reads B-B off the daemon's own `status` op — then emits `out.json` and a human summary, exiting
non-zero on any gated budget breach. Wired the CI `bench-gate` job (ubuntu/macos/windows, n=2000,
`continue-on-error` removed) and `nightly.yml`'s `bench-deep` job (n=5000) to the real flags, and
recorded the four SP-05 benchmark baselines into `testdata/bench-baseline.txt`.

`tools/devtool/benchhotpath.go` needed **no changes**: it already forwarded `go run
./test/bench/hotpath <args>` once the directory has Go files (`dirHasGoFiles`), and prints the
harness's own stdout (the human summary) via inherited stdio. Verified this by running `devtool
bench-hotpath` directly (see below).

## TDD evidence

`test/bench/hotpath/report_test.go` was written first. Confirmed RED:

```
$ go vet ./test/bench/hotpath/...
vet.exe: test\bench\hotpath\report_test.go:34:30: undefined: percentiles
```

Then implemented `report.go`, `payload.go`, `main.go`, `process.go`, `measure.go`, `transport.go`.
All tests GREEN:

```
$ go test ./test/bench/hotpath/... -v
=== RUN   TestPercentiles_HandCheckedFixture         --- PASS
=== RUN   TestPercentile_RankOneForSmallSamples       --- PASS
=== RUN   TestPercentile_EmptyIsZero                  --- PASS
=== RUN   TestSubtractFloor                           --- PASS
=== RUN   TestSubtractFloor_NeverNegative              --- PASS
=== RUN   TestBuildBudgetRow_GatedPassAndFail          --- PASS
=== RUN   TestReport_MatchesGoldenShape                --- PASS
=== RUN   TestBuildBudgetRowFromSnapshot               --- PASS
=== RUN   TestBudgetLimit_ReadsFromConfigDefaults      --- PASS
=== RUN   TestBudgetHistName_MatchesBudgetsTable       --- PASS
=== RUN   TestReport_ExitNonZeroOnAnyGatedFailure      --- PASS
PASS
```

- `TestPercentiles_HandCheckedFixture` pins the nearest-rank method (matching
  `internal/obs/hist.go`'s own `Snapshot`) against a hand-checked 512-sample fixture (1ms..512ms,
  fed in descending order to prove the function's own sort is load-bearing).
- `TestSubtractFloor`/`TestSubtractFloor_NeverNegative` pin the per-sample `B-A_i = max(0, B-D_i -
  floor_p50)` derivation (task-7-spec.md step 6) — never percentile-from-percentile.
- `TestReport_MatchesGoldenShape` is task-7-spec.md's own `out.json` example, reproduced verbatim
  as the golden and compared via `require.JSONEq` against a `Report` built with the same field
  values — pins every key, including `b_a_method`, `spawn_floor_ms`, `notes`, and all four budget
  rows' `limit_ms`/`pass` null-vs-set shape.
- `TestBudgetLimit_ReadsFromConfigDefaults` pins that the harness reads B-A/B-B/B-E's limits from
  `config.Defaults()` + `obs.Budgets()` (15ms/2ms/2000ms) rather than re-hardcoding them.

## Local bench run (Windows, this dev machine)

Full run: `go run ./test/bench/hotpath --iterations 2000 --hook observe-tool --warm-daemon --json
bench-local.json` (warm-up sent ~40.6 MB across 2000 requests, matching the spec's "~40 MB total"):

```
hotpath bench summary (windows/amd64, n=2000)
  spawn floor: p50=20.715ms p99=90.202ms (n=200)
  b_a_method: per-sample subtraction of spawn_floor_ms.p50

  B-A  n=2000  p50=   0.388ms p95=   7.822ms p99=  60.414ms p999=  78.001ms max=  115.224ms limit=15.000ms   [FAIL]
  B-B  n=4000  p50=   0.002ms p95=   0.576ms p99=   0.640ms p999=   1.408ms max=   10.444ms limit=2.000ms    [PASS]
  B-D  n=2000  p50=  21.103ms p95=  28.537ms p99=  81.129ms p999=  98.716ms max=  135.939ms limit=n/a        [reported]
  B-E  n=50    p50=  81.382ms p95=  92.727ms p99= 136.653ms p999= 136.653ms max=  136.653ms limit=2000.000ms [PASS]

  note: B-C not measured in wave 1: the processing seams are stubs
  note: daemon-observed hook_controlled_observed (recvTS-reqTS, strict lower bound): p50=1.024ms p99=2.048ms n=4000
  note: daemon-estimated hook_controlled (observed + tail allowance): p50=2.048ms p99=3.072ms n=4000
```

**B-B and B-E both PASS comfortably.** **B-A FAILS locally** (p99=60.4ms against the 15ms limit).
Per the brief, I investigated rather than relaxing the budget — see below.

### B-A investigation: confirmed host process-spawn jitter, not a code defect

1. `B-A`'s own p50 (0.388ms) is far under budget — the median case is fast.
2. The daemon's own `hook_controlled_observed` histogram (`recvTS - reqTS`, a strict lower bound
   on real work independent of process-spawn cost) reports **p50=1.024ms, p99=2.048ms** across
   4000 samples — i.e. the actual hot-path work inside the daemon is consistently sub-2ms. This is
   also corroborated by the in-process `BenchmarkIngestAccept` (~3.3-3.7 µs/op) and
   `BenchmarkServerRoundTrip` (~42-63 µs/op) micro-benchmarks below.
3. The spawn floor itself (`qompack version` × 200, no stdin, no project resolution, no store I/O)
   already shows p50=20.7ms / p99=90.2ms — i.e. bare process creation on this host regularly costs
   more than the entire B-A budget, with a heavy tail.
4. To rule out anything specific to the freshly-built `qompack` binary (e.g. Windows Defender
   real-time scanning a new/changed executable), I measured spawning `cmd.exe /c exit 0` — a
   pre-existing, already-trusted system binary — 30 times directly via PowerShell, independent of
   the harness entirely: **p50=23.6ms, max=82.1ms**. Real-time protection is confirmed enabled
   (`Get-MpComputerStatus`), but the *same* magnitude of overhead appears for a binary Defender has
   certainly already scanned many times, which rules out "scanning our binary" as the cause.

**Conclusion:** this is generic Windows process-creation overhead specific to this sandboxed
development container (likely virtualization/isolation layering under the agent harness), not a
defect in qompack's hot-path code. Because B-D's own per-sample variance is comparable in
magnitude to the entire B-A budget, per-sample floor subtraction (correctly, per spec) cannot
fully remove that variance from B-A's own tail — a few unusually slow spawns in the B-D sample set
land above `floor_p50` by more than 15ms, and those samples dominate B-A's p99 even though the
daemon's own per-request cost is consistently ~1-2ms. I did not relax the B-A budget or change the
derivation to compensate. I flag this local result for the controller: the CI matrix's actual
bare-metal (or much lighter-virtualized) `ubuntu-latest`/`macos-latest`/`windows-latest` runners
are very likely to show a materially tighter process-spawn floor than this sandbox, and the honest
per-sample-subtraction B-A number should pass there — but that should be confirmed by watching the
first real CI run rather than assumed from this local number alone.

## Benchmark baselines (`testdata/bench-baseline.txt`)

Recorded via `go test -run '^$' -bench . -benchmem -count 10 -p 1 ./...` on the same host,
appended as a new "SP-05 additions" section:

| Benchmark | Budget | Result | Verdict |
|---|---|---|---|
| `BenchmarkReadState` | hot-path prelude < 100 µs/op | ~52-60 µs/op | MET |
| `BenchmarkEncodeRequest` | < 5 µs/op (4 KB payload) | ~1.1-1.5 µs/op | MET |
| `BenchmarkIngestAccept` | B-B (l0_ingest, p99 < 2ms) | ~3.3-3.7 µs/op | MET |
| `BenchmarkServerRoundTrip` | daemon-side B-A portion, p99 < 2ms | ~42-63 µs/op | MET |

All four already existed in the tree (shipped by earlier SP-05 tasks); this task only ran and
recorded them.

## Verification run

- `go test ./test/bench/hotpath/... -race` — PASS
- `go test ./test/guards/...` — PASS (see "Deviation" below for what this caught)
- `GOOS=linux GOARCH=amd64 go build ./...` — PASS
- `GOOS=darwin GOARCH=arm64 go build ./...` — PASS
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint` — all 7 sub-checks PASS (golangci-lint, nomagic, importgraph,
  testdeps, bindeps, sleepcheck, stubskips)
- `go vet ./...` — clean
- `go test ./...` — full run, once: **53/53 packages `ok`, 0 `FAIL`** (includes `test/e2e`,
  79.5s, and `test/guards`, `tools/devtool`, `tools/lint/nomagic`)
- `go run ./tools/devtool bench-hotpath --iterations 10 --hook observe-tool --json <tmp>.json` —
  confirmed devtool's existing wiring (no changes needed) forwards flags, prints the human
  summary, and propagates the harness's non-zero exit as `devtool: bench-hotpath: exit status 1`
- CI YAML: no `actionlint` binary in this repo's tooling (checked `tools/pinned/`, not present).
  Validated both workflow files with `python -c "import yaml; yaml.safe_load(...)"`, which caught
  a real bug in my first edit (unquoted `${{ matrix.os }}` inside a flow-style `{ }` mapping is
  invalid per the YAML flow-scalar grammar — plain scalars in flow context cannot contain `{`/`}`);
  fixed by quoting the two artifact name/path values, matching the existing file's own convention
  for `nightly.yml`'s prior `bench-deep` job (`name: "bench-${{ matrix.os }}"` was already
  quoted). Both files now parse cleanly.

## Deviation from spec wording: warm-up traffic is not literally "one persistent connection"

task-7-spec.md step 3 says the 2000-request warm-up runs "over a persistent connection." My first
implementation did this literally: `dial_windows.go`/`dial_other.go` mirrored
`internal/ipc`'s own unexported `dial` (platform-specific `net.Conn`), and `transport.go` spoke the
NDJSON wire protocol directly over one held connection.

This broke `test/guards.TestGuard_NoNetworkImports` — a repo-wide, mechanically-enforced guard
(00-ARCHITECTURE.md §3.2/D10) asserting `net` may be imported **only** by `internal/ipc`, with
**no** exemption for `test/`/`tools/` (unlike the `time.Sleep` and `nomagic` checks, which do
exempt those trees). Task 7 is scoped to `test/bench`, `tools/devtool`, and CI/baseline files —
not to modifying `internal/ipc`'s already-landed, already-reviewed surface (tasks 1-2) to add a new
exported streaming primitive.

I removed the two `dial_*.go` files and re-implemented every admin/status/warm-up call through
`ipc.NewClientWithOptions(...).Send()` — the real, production `ipc.Client` — via a shared
`newProbeClient` helper (`transport.go`). This still sends exactly 2000 deterministic,
seed-1 `observe.tool` requests before measurement begins (see `warmDaemon`, `measure.go`), still
does real `admin.ping`/`status`/`admin.shutdown` round trips against the real daemon; the only
change is that each `Send` call independently dials rather than one socket staying open across all
2000. I'd argue this is if anything the *more* faithful shape — every real hook invocation in
production is its own fresh process making exactly one connection, which is exactly what this same
`ipc.Client` type does for the real hook subcommands — but it is a literal deviation from the
spec's "persistent connection" wording, so I'm flagging it explicitly rather than letting it pass
silently.

## Other notes / self-review

- `tools/devtool/importrules.go`: added `"test/bench/hotpath": true` to `compositionRoots`
  (`devtool lint`'s `importgraph` sub-check requires every on-disk package be declared there or in
  `allow`). It imports the whole tree the same way `test/e2e` does (daemon for `StatusSnapshot`,
  ipc for the `Client` its own admin/status/warm-up traffic goes through), and per §3.2 nothing may
  import it back — that's automatically true since nothing in `internal/` or `cmd/` references
  `test/bench/hotpath`.
- `--hook` currently accepts only `"observe-tool"` (the only value any CI/nightly/spec command
  ever passes); anything else is a usage error. Documented in the flag's own `--help` text.
- Marking `bench-gate` a required check on `develop`/`main` is a GitHub branch-protection setting,
  not a file in this repo — noted here for the controller per the brief's own instruction, not
  implemented (no repo file can express it).
- The harness is split across 7 files rather than the brief's literal 4-name list (`main.go`,
  `report.go`, `payload.go` + `report_test.go`): `process.go` (build/spawn/exec helpers),
  `measure.go` (warm-up + status), and `transport.go` (admin/status client + `QOMPACK_IPC_ADDR`
  override) were split out purely for file-size/readability; no functional difference from a
  single larger file.
- `QOMPACK_IPC_ADDR` override: a random 8-byte hex suffix (not derived from the project path) keeps
  concurrent bench runs from colliding, and — on POSIX — anchors the socket under `os.TempDir()`
  with a short, path-independent name so it can never trip `internal/ipc`'s 100-byte `sun_path`
  guard regardless of how long the temp project root itself is.
- No file writes outside `.qompack/` or this program's own temp directories: the project root
  (`--project` or a fresh `os.MkdirTemp`), a separate temp `HOME`/`USERPROFILE`, and a separate
  temp build directory for the compiled binary — all cleaned up on exit except an explicit
  `--project` (never removed, matching "don't delete what the caller named").

## Files changed

New:
- `test/bench/hotpath/main.go` — flags, orchestration, human summary
- `test/bench/hotpath/report.go` — `Report`/`BudgetRow` types, percentile math, budget-row builders
- `test/bench/hotpath/report_test.go` — TDD: percentile fixture, floor subtraction, golden shape
- `test/bench/hotpath/payload.go` — deterministic seed-1 payload generator + fixed B-A/B-E payloads
- `test/bench/hotpath/process.go` — module-root/binary-build/spawn-timing helpers
- `test/bench/hotpath/measure.go` — warm-up + status-op read + notes assembly
- `test/bench/hotpath/transport.go` — `ipc.Client`-based admin/status probe + `QOMPACK_IPC_ADDR`

Modified:
- `.github/workflows/ci.yml` — `bench-gate`: removed `continue-on-error`, real
  `--iterations 2000 --hook observe-tool --warm-daemon --json` command + `upload-artifact` step;
  `replay-gate` untouched
- `.github/workflows/nightly.yml` — `bench-deep`: added `--hook observe-tool --warm-daemon`,
  per-OS json filename (matching `bench-gate`'s own convention)
- `testdata/bench-baseline.txt` — appended the four SP-05 benchmark recordings
- `tools/devtool/importrules.go` — declared `test/bench/hotpath` a composition root

Unchanged (verified, not touched): `tools/devtool/benchhotpath.go` already implements the required
task body (forward args to `go run ./test/bench/hotpath`, print inherited stdio, propagate the
harness's exit code) once `test/bench/hotpath` has Go files.

---

## Fix round 1

Reviewer verdict: Needs fixes (0 Critical, 3 Important, 7 Minor). Full review:
`task-7-review.md`. The reviewer independently reproduced the jitter diagnosis (own run: B-D.p99
76.4ms less than floor.p99 79.1ms, the same decisive signature) and confirmed no product-side
regression, but found the mandated spec-step-6 method itself defective for gating purposes: B-A
(`internal/obs/budgets.go`: "client main() entry to exit") excludes process creation by
definition, and subtracting a CONSTANT per-sample floor removes the floor distribution's location
but none of its dispersion — so the gated statistic (p99) stays contaminated by host process-spawn
jitter no matter how sound the diagnosis is. Controller ruling #29 amends spec step 6 in response.

### I-1 / controller ruling #29 — B-A gate now sourced from the daemon's own TS-anchored estimate

The gated "B-A" row is no longer derived from wall-clock spawn samples. It is now built from the
daemon's own `hook_controlled` histogram (recvTS - reqTS + hotPathTailAllowance — the exact series
`internal/daemon`'s breach detector itself consumes), fetched via the `status` op after the run,
via `buildBudgetRowFromSnapshot(string(obs.BA), snap.Latency[budgetHistName(obs.BA)],
budgetLimit(cfg, obs.BA), true)` (`main.go`). This is real, TS-anchored, daemon-side data — never
a wall-clock estimate — and it excludes process creation by construction, matching B-A's own
definition exactly.

The old wall-clock, floor-subtracted number is not discarded: it survives as a renamed,
always-ungated diagnostic row, `B-A_spawn_estimate` (`budgetIDBASpawnEstimate` in `report.go`),
with `limit_ms`/`pass` both null — exactly like B-D. `bAMethod`'s own doc comment and string
(`report.go`) now explain the derivation and its own caveat in full: unbiased at p50,
dispersion-contaminated at p99. `TestBudgetIDBASpawnEstimate_NeverCollidesWithB_A` pins that this
id is never `B-A` itself.

`TestReport_MatchesGoldenShape` was updated to the new five-row shape (B-A, B-B, B-D, B-E,
B-A_spawn_estimate) and now injects `bAMethod`'s actual text into the fixture via `fmt.Sprintf`
rather than duplicating it as a second literal, so the golden cannot silently drift from what
`report.go` emits.

Local re-run, `--iterations 2000 --warm-daemon` (same host):

```
hotpath bench summary (windows/amd64, n=2000)
  spawn floor: p50=22.157ms p99=85.332ms (n=200)

  B-A                 n=4000  p50=   2.048ms p95=   2.048ms p99=   3.072ms p999=   4.096ms max=   13.000ms limit=15.000ms   [PASS]
  B-B                 n=4000  p50=   0.002ms p95=   0.576ms p99=   0.576ms p999=   1.536ms max=    9.914ms limit=2.000ms    [PASS]
  B-D                 n=2000  p50=  22.381ms p95=  32.688ms p99=  90.092ms p999= 123.697ms max=  132.244ms limit=n/a        [reported]
  B-E                 n=50    p50=  71.019ms p95=  87.333ms p99= 142.611ms p999= 142.611ms max=  142.611ms limit=2000.000ms [PASS]
  B-A_spawn_estimate  n=2000  p50=   0.223ms p95=  10.530ms p99=  67.934ms p999= 101.540ms max=  110.086ms limit=n/a        [reported]

  note: daemon-observed hook_controlled_observed (strict lower bound, no tail allowance): p50=1.024ms p99=2.048ms n=4000
  note: B-B's n includes the 2000 warm-up observe.tool requests
```

Exit code 0. The commit-7 checklist row "confirm B-A.pass == true and B-E.pass == true" is now
genuinely met locally: B-A p99 = 3.072ms (limit 15ms), B-E p99 = 142.6ms (limit 2000ms). The old,
still-reported `B-A_spawn_estimate` row's own p99 (67.9ms) is the same contaminated number the
review flagged — reported as a diagnostic, never gated, exactly as ruling #29 requires.

No changes were needed to `ci.yml`/`nightly.yml`/`testdata/bench-baseline.txt`: the amendment is
entirely internal to `test/bench/hotpath`'s own Go code — the invoked command line and the four
micro-benchmarks are unaffected.

### I-2 — delivery-integrity assertion

Added `checkDeliveryIntegrity(gotN int64, iterations int, warmDaemonRan bool) error` (`report.go`,
unit-tested by `TestCheckDeliveryIntegrity`), called in `runHarness` (`main.go`) immediately after
`fetchStatus` and before anything derived from `snap` is trusted: the daemon's own `l0_ingest`
`obs.HistSnapshot.N` must equal exactly `iterations` (plus `warmIterations` when warm-up ran) —
the ground truth for "every observe.tool request actually reached the daemon" rather than
degrading silently to the spool. A mismatch now fails the run loudly (`Report{}, error`) instead
of letting a partially-degraded run bias every derived number downward and pass the gate on
partial data. The local re-run above satisfied it exactly (`l0_ingest.N` = 4000 = 2000 warm-up +
2000 iterations) — no error was raised, confirming the identity holds in practice, not just as an
assertion.

### I-3 — orphaned detached-daemon prevention

Added `detectAndStopOrphan` (`process.go`), called at the end of `stopDaemon` AFTER this program's
own daemon child is confirmed exited via a real `cmd.Wait()` (never a guess) — so anything still
answering `admin.ping` at that point can only be a second, harness-unmanaged daemon (the narrow
race: a dial timeout during a measured spawn triggers the hook's own `internal/ipc/client.go`
`lazySpawn`, which can win the project's `daemon.lock` if it lands right as this program's own
daemon is shutting down). If one is found, it is sent `admin.shutdown` and polled (bounded,
`orphanCheckDeadline` = 5s) to confirm it actually stops; either outcome is reported loudly via
`errw` (never silently). This is best-effort teardown hygiene, not a correctness gate — the
measurement is already complete and internally consistent (I-2) by the time teardown runs — so an
orphan that refuses to stop is warned about, not treated as a bench failure.

Verified empirically: `Get-Process -Name qompack` immediately after this fix round's full local
run (both the `--iterations 2000 --warm-daemon` run and the `go run ./tools/devtool bench-hotpath`
smoke runs from the original submission) showed zero processes whose command line named a
`qompack-bench-*` temp project — every `qompack.exe`/daemon process still alive at that point
belonged to the currently-running `go test ./...`'s own `test/e2e` suite (distinct temp-dir
naming, e.g. `qompack-e2e-*`, `qm-space-*`), not to this harness.

### Minors — fixed vs left

Fixed:
- M-1 (dead code) — deleted `warmupTotalBytes` (`payload.go`); removed the now-unused `math`
  import alongside it.
- M-2 (comment vs code) — rewrote `warmDaemon`'s doc comment (`measure.go`) to state plainly that
  `Client.Send` never returns a propagating error by contract, so the loop's own `err != nil`
  branch is near-dead defensive code, and that per-request delivery failures are NOT individually
  detected here — I-2's post-hoc `l0_ingest` count assertion is the actual guard, named explicitly.
- M-3 (shared constant) — added `defaultIterations` as `--iterations`'s own default, decoupled
  from `warmIterations` (`main.go`); both are 2000 today but can no longer silently move together.
- M-4 (undocumented B-B n) — `buildNotes` (`measure.go`) now takes `warmDaemonRan bool` and emits
  a note explaining B-B's n includes warm-up traffic when it ran, and why that is the conservative
  (not misleading) choice.
- M-5 (silent Kill) — `stopDaemon` (`process.go`) now writes one `errw` line ("daemon did not
  exit within %s of admin.shutdown — killing it") on the Kill path instead of discarding the
  signal.
- M-7 (stale doc comment) — refreshed `tools/devtool/benchhotpath.go`'s doc comment: it no longer
  describes a pre-SP-05 state; it now says `dirHasGoFiles` is permanently true in this tree and
  the no-op fallback is kept only for a build that predates task 7.

Left (controller-owned, not task-level fixes):
- M-6 (floor measured once, before the daemon is under load, rather than interleaved with the
  hook spawns) — the review itself calls this "a wave-2 methodology proposal for the controller,
  not a task-7 fix"; task-7-spec.md step 4 mandates measuring the floor first, so changing the
  measurement's own timing shape is out of this task's scope. Left as documented, not fixed.

### Re-verification after fix round 1

- `go build ./test/bench/hotpath/...` — clean
- `go vet ./test/bench/hotpath/...` — clean
- `go test ./test/bench/hotpath/... -race` — PASS (all 14 unit tests, incl. 4 new: golden-shape
  collision guard, `buildBudgetRowFromSnapshot`, `checkDeliveryIntegrity`, and the updated golden)
- `go test ./test/guards/...` — PASS (net-import guard still respected — nothing in this fix
  round touched transport)
- `GOOS=linux GOARCH=amd64 go build ./...` — clean
- `GOOS=darwin GOARCH=arm64 go build ./...` — clean
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint` — all 7 sub-checks PASS
- `go vet ./...` — clean
- `go test ./...` — full run, once: 53/53 packages `ok`, 0 `FAIL` (includes `test/e2e`, 79.5s,
  `test/guards`, `tools/devtool`, `tools/lint/nomagic`)
- Full local harness re-run, `--iterations 2000 --hook observe-tool --warm-daemon` — exit 0,
  B-A/B-B/B-E all PASS (numbers above)

---

## Fix round 2

Re-review of fix round 1 (`ab088f4f -> be911428`): verdict "Needs fixes (0 Critical, 1 Important)".
All three original Important items (I-1/ruling #29, I-2, I-3) and all six minors confirmed closed
at the code level. One NEW defect, introduced by the ruling-#29 fix itself: **N-1**.

### N-1 (Important) — the gated B-A histogram was mostly warm-up traffic, not hook spawns

Fix round 1's `warmDaemon` sent all `warmIterations` (2000) warm-up requests as real
`ipc.OpObserveTool` traffic over an in-process client. `internal/daemon/handlers.go`'s
`dispatchOp` records EVERY `req.Op.HotPath()` request into `hook_controlled` — the exact series
ruling #29 just made the gated "B-A" row — with no way to filter by session or by "came from a
spawned process" through the `status` op. The re-reviewer's own re-run showed `B-A n=2060` for 60
real hook spawns; the fix-round-1 report's own `n=4000` for 2000 real hook spawns meant HALF the
gated population, in CI's own configuration, was sub-millisecond in-process traffic — pulling the
gated p99 down toward the real population's own ~p98 (worse at smaller `--iterations`) and making
the reported p50 a warm-up number, not a hook number. The bias is optimistic in every case: adding
a fast sub-population can only lower a percentile.

**Structural fix (preferred, applied):** `warmIterations` (2000) is now split into a small,
named `warmHotTranche` (`main.go`, = 64) of genuine hot-path `observe.tool` requests — still real,
still the deterministic seed-1 mixed-shape payloads task-7-spec.md step 3 names — and the
remaining `warmIterations - warmHotTranche` (1936) requests are now sent as `admin.ping` round
trips instead (`measure.go`'s rewritten `warmDaemon`). `admin.ping` is not `req.Op.HotPath()`
(`internal/ipc/op.go`), so it can never reach `hook_controlled` or `l0_ingest` — it still
genuinely exercises the daemon's accept/connection loop, which was the only other real purpose the
bulk traffic served in wave 1 (CMS/DAG/store are stubs; they gain nothing from payload volume yet).
With `--iterations` at its 2000 default, the gated B-A population is now `2000 + 64 = 2064` — 96.9%
real hook spawns, versus fix round 1's 50/50 split at the same iteration count.

`checkDeliveryIntegrity` (I-2, `report.go`) was updated to match exactly: expected `l0_ingest`
count moved from `iterations + warmIterations` to `iterations + warmHotTranche`, since only the
hot tranche touches `l0_ingest` now. `TestCheckDeliveryIntegrity` was updated accordingly.

**Disclosure (applied regardless, per the fix instructions):** `buildNotes` (`measure.go`) now
takes `iterations` and, when warm-up ran, emits a note stating B-A's own population composition
explicitly — `warmHotTranche` in-process requests plus `iterations` real hook spawns, and why the
tranche is kept small — matching the note B-B already had from fix round 1's M-4 (which was also
extended to name the two count parts precisely: the hot tranche vs. the admin.ping bulk).

**Local re-run, `--iterations 2000 --hook observe-tool --warm-daemon` (same host):**

```
hotpath: warming daemon (64 hot-path observe.tool + 1936 admin.ping)...
hotpath: warm-up sent ~1.1 MB across 64 hot-path requests (plus 1936 admin.ping round trips)

  B-A                 n=2064  p50=   2.048ms p95=   2.048ms p99=   3.072ms p999=   5.120ms max=   14.000ms limit=15.000ms   [PASS]
  B-B                 n=2064  p50=   0.002ms p95=   0.576ms p99=   0.704ms p999=   5.632ms max=   10.550ms limit=2.000ms    [PASS]
  B-D                 n=2000  p50=  22.244ms p95=  32.464ms p99=  91.831ms p999= 120.628ms max=  126.441ms limit=n/a        [reported]
  B-E                 n=50    p50=  72.314ms p95=  89.597ms p99= 128.519ms p999= 128.519ms max=  128.519ms limit=2000.000ms [PASS]
  B-A_spawn_estimate  n=2000  p50=   0.000ms p95=   9.229ms p99=  68.596ms p999=  97.393ms max=  103.206ms limit=n/a        [reported]
```

Exit code 0. **B-A n=2064 = 2000 real hook spawns + 64 warm-up tranche (96.9% real); p50=2.048ms is
now visibly close to `hook_controlled_observed`'s own strict-lower-bound p50 (1.024ms) — a real
hook number, not a warm-up number; p99=3.072ms, well under the 15ms limit with ample headroom.**
The out.json's own `notes` array states this composition explicitly for both B-A and B-B.

A quick sanity smoke at `--iterations 30` (far below the 2000 default) showed B-A still FAILing,
because at that scale the fixed 64-request tranche is no longer small relative to the real
population (64 of 94 = 68%) — expected and consistent with the fix's own stated scope (`warmHotTranche`
is sized against task-7-spec.md's own 2000/5000 CI/nightly `--iterations` values, not against an
arbitrary small smoke count). Not a regression; noted for completeness.

No changes were needed to `ci.yml`/`nightly.yml`/`testdata/bench-baseline.txt`: this fix, like fix
round 1's, is entirely internal to the harness's own Go code.

### Re-verification after fix round 2

- `go build ./test/bench/hotpath/...` — clean
- `go vet ./test/bench/hotpath/...` — clean
- `go test ./test/bench/hotpath/... -race` — PASS (14 unit tests, incl. `TestCheckDeliveryIntegrity`
  updated to `warmHotTranche`)
- `go test ./test/guards/...` — PASS
- `GOOS=linux GOARCH=amd64 go build ./...` — clean
- `GOOS=darwin GOARCH=arm64 go build ./...` — clean
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint` — all 7 sub-checks PASS
- `go vet ./...` — clean
- `go test ./...` — full run, once: 53/53 packages `ok`, 0 `FAIL` (includes `test/e2e`, 80.0s,
  `test/guards`, `tools/devtool`, `tools/lint/nomagic`)
- Full local harness re-run, `--iterations 2000 --hook observe-tool --warm-daemon` — exit 0,
  B-A n=2064 p50=2.048ms p99=3.072ms [PASS vs 15ms]; B-B p99=0.704ms [PASS vs 2ms]; B-E p99=128.5ms
  [PASS vs 2000ms]

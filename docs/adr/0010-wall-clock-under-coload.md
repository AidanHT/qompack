# 10. A wall-clock budget is judged only where it is judgeable

Date: 2026-09-06

## Status

Accepted. Implemented at the close of V3-VERIFY's J5 backfill (`plans/V3-report.md` Addendum 2,
item 7). Supersedes nothing; generalises two earlier rulings that had each been made once.

## Context

CI had never been green in this repository's history. When GitHub Actions billing was restored
on 2026-09-06 and the pipeline ran for the first time against the merged tree, it found seven
defects; six were defects in the product or its tests, are fixed, and are recorded in Addendum 2.
The `test` job kept failing after every one of them, and for a reason no fix to the product could
reach:

| row | `bench-gate` (harness alone) | whole-tree `test` #1 | whole-tree `test` #2 | limit |
|---|---:|---:|---:|---:|
| spawn floor p50 | 12.954 ms | 24.431 ms | 23.143 ms | — |
| **B-A p99** | **3.072 ms** | 11.264 ms (pass) | **18.432 ms (FAIL)** | 15 ms |
| B-B p99 (no spawn) | 0.576 ms | 0.768 ms | 0.704 ms | 2 ms |

One commit, one windows-latest runner class, minutes apart, product byte-identical. B-A — the
daemon-observed spawn-to-ack latency of a hook process — is a coin flip inside a job that runs
about twenty package binaries on two cores, several of which spawn thousands of processes of their
own. B-B, the in-daemon half that contains no process spawn, barely moves. The same job failed
`TestGC_DeadlineOvershootIsBoundedByTheCheckInterval` (a sweep descheduled for 220 ms against a
113 ms overshoot limit priced from its own calibration), `TestGC_DeadlineTruncatesAndResumes` (a
budget priced from a co-loaded control pass that the judged pass then beat), `TestBudget_Open`
(564 ms/op best-of-three against 300 ms, on a benchmark that measures 190 ms in isolation), and
the two hot-path harness tests in `test/integration` and `test/e2e`. Separately, `test/e2e` under
`-race` did not finish inside the 30-minute whole-tree timeout on ubuntu or macos — 291 s without
the detector, over 1800 s with it and co-loaded — so those two jobs never reached a verdict at
all.

The repository had already ruled on this class twice, each time locally:

- `devtool bench-compare` was kept out of CI because its baseline is single-host and a
  cross-machine delta measures the machine (00-ARCHITECTURE.md §8, ADR-era ruling).
- The hot-path harness's `--under-coload` flag un-gates B-E's wall-clock row and gates the same
  50 checkpoint children on their own CPU time instead, with the measurement that justified it
  written into `test/bench/hotpath/report.go` (`budgetIDBECPU`): B-E wall p99 67.2 ms in
  `bench-gate`, 4302 ms in the whole-tree job on the same runner class, CPU p50/p99 unchanged to
  the tick.
- `internal/dag`'s latency budgets were rewritten to grade on `obs.ProcessCPU` after the same
  failure shape (`fix/a4-wallclock-gates`, merged).

Nothing had extended those rulings to B-A, to the GC deadline tests or to the negknow budgets,
and with CI never having run, nothing forced the question.

## Decision

**A wall-clock number measured under co-load is a measurement, not a judgement.** The pipeline is
arranged so that every wall-clock budget is still judged — in the isolation where it is judgeable
— and never silently dropped.

1. **The run declares its environment; the test does not guess it.** `QOMPACK_UNDER_COLOAD`
   (`internal/obs.UnderColoadEnv`) is set by exactly the jobs that run the whole tree at
   once — ci.yml's `test`, nightly's `race-windows`, and `devtool test` / `test-race` / `cover` —
   and by nothing else. It is a statement of fact by the only party that knows it, the same shape
   as the harness's `--under-coload` flag, and it defaults to unset so that forgetting it can only
   make a run stricter.

2. **What the declaration licenses is narrow.** A test that reads `obs.UnderCoload()` may do
   one of two things, and nothing else:
   - move a cost judgement to the clock co-load does not inflate — the process's own CPU time —
     and keep gating on it (all seven of `internal/negknow`'s `TestBudget_*` rows now report and
     gate a `cpu-ns/op` metric on every run, and gate wall ns/op only in isolation; this is the
     B-E / B-E_cpu shape applied to a benchmark);
   - or, where the property is intrinsically wall-clock and no such clock exists — a deadline
     honoured in wall time, a hook's spawn-to-ack latency — REPORT the measurement with a log
     line that names the limit not applied and the job that still applies it. B-A joins B-E as a
     row the harness reports under `--under-coload`; the two GC deadline tests report their
     overshoot and pricing judgements under the declaration and keep every assertion about what
     the collector did.
   It never licenses a `t.Skip` (devtool lint's `stubskips` permits three reasons and this is not
   one), and it never relaxes an assertion about what the product DID.

3. **Every yielder is judged in isolation, by name.** ci.yml gains a `timing` job that runs each
   test consulting the declaration — `-p 1`, one package binary at a time, on a runner doing
   nothing else, without the variable — and a `test-e2e` job that runs `test/e2e` alone, likewise
   without it, so X-11 gates B-A and B-E's wall row there exactly as `bench-gate` does.
   `test/guards.TestColoadYieldersAreJudgedInIsolation` reads the tree and the workflow and fails
   if a test that consults `UnderCoload` is missing from both jobs, or if the lane names a test
   that no longer consults it. The policy is therefore mechanical: a budget can be deferred by
   the whole-tree job but not lost by the pipeline.

4. **`test/e2e` leaves the whole-tree run and is not run under `-race` anywhere.** Every e2e test
   drives the real binary, built plainly by `Build(t)`; the daemon, hooks and store under test run
   in that uninstrumented process whatever flags the test binary carries, so the detector only
   ever instrumented the harness, at a 6x cost that no runner budget absorbs. The in-process
   components e2e composes directly (negknow in X-09, the observer pipeline in X-10) are under
   `-race` in their own packages and in `test/integration`.

5. **The harness's deferral rule is unchanged.** `tailAdjustedP99` still counts an undelivered
   sample as over-budget and judges on the delivered set's next order statistic. It is what
   failed `bench-gate (ubuntu-latest)` on run 34054286301 — one of 2064 requests spooled, so B-B
   was judged on its p999 and met a 37 ms outlier with p50 at 18 µs — and that is a strictness
   this ADR leaves in place: the rule cannot certify a run it cannot prove good, and a rerun is
   the honest answer to a scheduler hiccup in an isolated job. If it recurs at a rate that costs
   more than it protects, that is a separate ruling with its own measurement.

## Consequences

- The `test` job can be green, and its greenness means what it should: every structural property
  held, every cost budget held on the CPU clock, and every wall-clock budget was measured and
  written to the log for anyone pricing a regression.
- Two new matrix jobs (`timing`, `test-e2e`) on three runners each. `timing` costs roughly five
  minutes per runner, dominated by the integration hot-path test's 2 250 spawns; `test-e2e`
  costs what the package costs alone (about five minutes on ubuntu, twelve on windows).
- A wall-clock regression now surfaces in `timing` / `test-e2e` / `bench-gate`, not in `test`.
  Anyone reading a red `test` job knows it is not a timing problem; anyone reading a red
  `timing` job knows it is nothing else.
- `devtool test`, `test-race` and `cover` declare co-load locally, because a whole-tree run is
  co-loaded by construction — the fact `wholeTreeTestTimeout` already provisions for. A developer
  who wants the isolated verdict runs the `timing` lane's command by hand; it is one line in
  ci.yml.
- The nightly `race-windows` job excludes `test/e2e` and declares co-load, so it can also be green
  for the first time.

## What this does not decide

- The CPU ceiling for `internal/negknow`'s budgets is the wall budget, unchanged. That is the
  B-E precedent, but the two clocks do not relate here the way they do for a spawned child:
  process CPU includes the runtime's parallel GC mark workers on every other P, so on a 22-thread
  developer host `BenchmarkOpen` read 250-344 ms CPU/op best-of-three against 300 ms while its
  wall was under the ceiling, and at GOMAXPROCS=1 the two clocks agree to the millisecond. On
  CI's two-core runners the measured inflation is 10-31%. The quiet-host `cpu-ns/op` figure for
  `BenchmarkOpen` is the number nobody has yet, and the first green `timing` run supplies it; if
  it lands near 300 ms, the choices are to subtract the idle-P mark classes (`runtime/metrics`)
  from the reading or to price the CPU row on its own — an R26-style revision — not to widen the
  ceiling by hand.

- Whether B-B's deferral accounting should tolerate a bounded number of spooled samples in an
  isolated job (item 5 above).
- Whether `test/e2e` should ever run under `-race` again, with a budget sized by measurement
  rather than by the whole-tree constant.

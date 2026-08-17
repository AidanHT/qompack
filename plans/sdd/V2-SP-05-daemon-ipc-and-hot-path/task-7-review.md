# Task 7 review — hot-path bench harness, three-platform bench gate, and fixtures

Reviewer: task reviewer (read-only). Commit under review: `ab088f4f` (range `6fc27a3b..ab088f4f`).
Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch
`feat/sp05-daemon-ipc-and-hot-path`.

**Verdict 1 — SPEC COMPLIANCE: Needs fixes (0 Critical, 3 Important).**
**Verdict 2 — CODE QUALITY: Needs fixes (0 Critical, 0 Important, 6 Minor) — correctness of the
math, timers, lifecycle and CI matrix all check out; the gaps are measurement-integrity guards and
comment/dead-code hygiene.**

Combined: **Needs fixes (0 Critical, 3 Important)**.

---

## 0. What I verified myself (not taken from the report)

| Check | Command | Result |
|---|---|---|
| exactly one commit | `git log --oneline 6fc27a3b..ab088f4f` | 1 commit, `ab088f4 test(sp05): hot-path bench harness, three-platform bench gate, and fixtures` |
| commit message byte-exact | `git show -s --format=%B ab088f4f \| cat -A` | subject, blank line, 3-line body, blank line, `Refs: SP-05, §8.1, §11.3, 00-ARCHITECTURE §7, §8` — identical to task-7-brief.md's block (the `M-BM-'` sequences in `cat -A` are UTF-8 `§`). **No attribution trailers.** |
| working tree clean at review | `git status --porcelain` | empty |
| vet | `go vet ./test/bench/...` | clean |
| tests | `go test ./test/bench/... ./test/guards/...` | both `ok` |
| full lint | `go run ./tools/devtool lint` | all sub-checks PASS (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips) |
| **independent bench run** | `go run ./test/bench/hotpath --iterations 60 --hook observe-tool --warm-daemon --json …` | reproduced below — this is my own data, not the implementer's |

My own run (same host, n=60):

```
  spawn floor: p50=19.883ms p99=79.092ms (n=200)
  B-A  n=60    p50=  1.677ms p95= 7.492ms p99= 56.479ms max= 56.479ms limit=15.000ms [FAIL]
  B-B  n=2060  p50=  0.002ms p95= 0.576ms p99=  0.576ms max= 10.134ms limit=2.000ms  [PASS]
  B-D  n=60    p50= 21.560ms p95=27.375ms p99= 76.362ms max= 76.362ms limit=n/a      [reported]
  B-E  n=50    p50= 74.363ms p95=92.588ms p99=147.213ms max=147.213ms limit=2000ms   [PASS]
  daemon-observed hook_controlled_observed: p50=0.002ms p99=2.048ms n=2060
```

Two facts fall out of my run that matter for the adjudications below:

1. **`B-D.p99` (76.4 ms) is *below* the bare-`qompack version` floor's own `p99` (79.1 ms).** If the
   hook were doing real slow work at the tail, the hook spawn's tail would have to exceed the tail of
   a process that does nothing but print a string. It does not. This is the single most decisive
   number in the whole investigation, and it holds in both the implementer's run (81.1 vs 90.2) and
   mine.
2. **`B-B.n` == warm-up + iterations exactly** (2000 + 60 = 2060; 2000 + 2000 = 4000 in the
   implementer's run). Every single hook request was delivered to the daemon — none degraded to the
   spool path. This is load-bearing for the diagnosis, and (see I-2) the harness does not assert it.

---

## 1. Adjudication (a) — the local B-A p99 = 60.4 ms FAIL

### (a.1) Does the harness measure what B-A *defines*?

Not exactly, and the gap is the whole story.

`internal/obs/budgets.go:15` defines B-A as *"hook_controlled: client `main()` entry to exit (connect
+ write + ACK)"*. B-A therefore **excludes** OS process creation by construction; B-D
(`internal/obs/budgets.go:21`, *"hook_wall: includes host process creation"*) is where creation lives.

`test/bench/hotpath/process.go:177-179` starts the clock before `cmd.Run()` and stops it after — so
each raw sample is `fork/exec + image load + Go runtime init + main() + exit + reap`. That is B-D,
correctly. B-A is then derived at `main.go:245` as `subtractFloor(bdSamples, floorP50)`, i.e.

```
B-A_i  =  max(0, B-D_i − floor_p50)
       =  (hook_main_work_i − version_main_work_i)  +  (spawn_i − spawn_p50)
           └────────── the thing B-A actually is ─────┘   └─── pure host noise ───┘
```

Subtracting a **constant** removes the floor distribution's *location* and **none of its
dispersion**. The estimator is therefore approximately unbiased for B-A's *median* and badly
contaminated for B-A's *p99* — which is precisely the statistic CI gates on. On this host the floor's
own spread (`p99 − p50` = 59–70 ms) is four to five times the entire 15 ms budget, so the second term
alone can carry the p99 over the limit with a perfectly healthy hot path.

**This is a defect in the spec's step 6, not in the implementation.** task-7-spec.md:14 and the brief
both mandate per-sample subtraction of `spawn_floor_ms.p50` verbatim, and correctly point out that
`p99(X−Y) ≠ p99(X) − p99(Y)` — but per-sample subtraction of a *constant* does not solve that
problem either; it only relocates it. The implementer followed a binding ruling exactly. Nothing to
fix here at task level; it is a wave-2 methodology item (see M-6 and §5).

Verdict on (a.1): **the harness measures B-D honestly and derives B-A by the mandated method; the
derived B-A is a faithful implementation of a statistically weak estimator.** Implementation: correct.

### (a.2) Is the jitter diagnosis sound, or does it excuse a real regression?

**Sound.** I attacked each of the alternative explanations the review brief names, and each is ruled
out by evidence in the tree, not by assertion:

- **The 250 ms connect floor — does not apply.** `internal/cli/hookclient.go:156-162`
  (`hookConnectDeadline`) widens the dial budget to `hookConnectDeadlineFloor` **only** for
  `spec.deadline > 0 && !spec.op.HotPath()` — session-start, checkpoint, flush. `observe.tool` is
  `HotPath()`, so it keeps `state.bin`'s `ConnectDeadlineMs` (5 ms default) untouched. The 250 ms
  floor is not on the B-A path at all. (It *is* on B-E's, which passes with 25× headroom.)
- **`state.bin` reads — not material.** `BenchmarkReadState` in the recorded baseline is 52–60 µs/op
  on this very host. Even ten of them would be invisible against a 15 ms budget.
- **A hidden blocking prelude — contradicted by B-A's own median.** B-A `p50` is the *difference of
  medians* between the hook spawn and a `version` spawn. `runVersion`
  (`internal/cli/commands.go:63`) is a single `fmt.Fprintln(out, core.Version)` — no root
  resolution, no state read, no `.qompack/` touch — so B-A `p50` is exactly "everything the hook does
  that `version` does not". That number is **0.388 ms** (implementer) / **1.677 ms** (mine). The
  entire hook prelude — root resolve, `ReadState`, `ReadEvent`, spool open, dial, write, ACK, JSON
  out — costs ~0.4–1.7 ms at the median. There is no blocking prelude.
- **Spawn-per-invocation where a persistent daemon should absorb load — this *is* production.** Real
  Claude Code hooks are one process per invocation; that is why B-D exists as a separate budget. And
  the daemon *does* absorb it: `B-B` passes (`p99` 0.576 ms against 2 ms) and the daemon's own
  `hook_controlled_observed` (recvTS − reqTS) reports `p99` = 2.048 ms (bucketed) over 2060/4000
  samples. Nothing is queueing behind a cold daemon.
- **Positive evidence for the jitter explanation:** `B-D.p99 < floor.p99` in both runs (see §0).
  A regression in hook work cannot make the hook's tail *shorter* than the tail of a do-nothing
  process on the same binary.

The implementer's own control experiment (`cmd.exe /c exit 0` at p50 ≈ 23.6 ms via PowerShell,
outside the harness entirely, on a binary Defender has certainly already scanned) is a good
falsification of the "Defender is scanning our fresh binary" hypothesis and I accept it.

Verdict on (a.2): **the diagnosis is sound and does not excuse a product regression.** I found no
product-side cost that the numbers do not already account for.

### (a.3) Is "CI bare-metal will pass" justified, or wishful?

The harness *is* timing the hook process's spawn, and B-A's definition excludes that spawn — which is
exactly why the subtraction exists and exactly why it is inadequate at p99 (a.1).

**"CI bare-metal" is a category error, and the expectation is optimistic.** GitHub-hosted runners are
not bare metal: they are 2–4 vCPU Azure VMs on shared hosts, and `windows-latest` ships Defender
real-time protection enabled by default. The pass condition at `--iterations 2000` is that **fewer
than 20 of 2000 hook spawns exceed `floor_p50` by more than 15 ms** — i.e. the 1980th-ranked sample
must be under budget. On a 2-vCPU shared Windows VM issuing 2250 process creations back to back
(200 floor + 2000 hook + 50 checkpoint), twenty >15 ms excursions is not an unlikely tail, it is a
routine one. `nightly.yml` at `--iterations 5000` requires the 4950th-ranked sample to hold, on a
run 2.5× longer.

The report's own hedge — *"very likely … but that should be confirmed by watching the first real CI
run rather than assumed"* (task-7-report.md:110-113) — is the correct framing and I credit it. The
*expectation* embedded in it is nonetheless optimistic, and it is the only thing standing behind a
hard gate on three platforms.

### (a.4) My independent judgment: can this branch honestly claim the <15 ms gate?

**No.**

What this branch *can* honestly claim, and I will sign off on:
- B-A is measured by the method the spec mandates, with the floor made auditable exactly as §4
  requires (`qompack version` is a genuinely clean floor — verified at `internal/cli/commands.go:63`).
- The daemon-side hot path is demonstrably healthy: B-B passes with 3.5× headroom, the daemon's own
  observed histogram is `p99` ≤ 2.048 ms, and `BenchmarkIngestAccept` / `BenchmarkServerRoundTrip`
  corroborate at 3.4 µs and 47 µs per op.
- The measured B-A `p99` failure is attributable to host process-creation dispersion, with
  `B-D.p99 < floor.p99` as the decisive evidence.
- No budget was relaxed and no derivation was fudged to compensate. That restraint is the right call
  and I want it recorded as such.

What it **cannot** claim:
- That B-A `p99` < 15 ms has been demonstrated on any platform. It has been demonstrated on none.
- That the commit-7 checklist row *"confirm `B-A.pass == true` and `B-E.pass == true`"*
  (task-7-spec.md:87) is satisfied. It is half-satisfied: B-E yes, B-A **no**.
- That removing `continue-on-error` is safe. It ships a hard three-platform gate that has never been
  observed green anywhere. See **I-1**.

If windows-latest fails on the first CI run, the decision space — interleave the floor spawns with the
hook spawns so the subtraction is contemporaneous and paired; or report B-A from the hook's own
`main()`-entry-to-exit clock rather than from wall time minus a constant; or gate B-A on a trimmed
statistic and keep the raw p99 reported — belongs to the controller as a wave-2 methodology
amendment, **not** to this task and **not** by relaxing 15 ms.

---

## 2. Adjudication (b) — warm-up via `ipc.Client.Send` per request instead of a held connection

**Faithful, forced, and on balance an improvement. Not a fix. The prompt's stated worry is inverted.**

- **The constraint is real, not an excuse.** `test/guards`'s `TestGuard_NoNetworkImports` requires
  `require.Equal(t, netAllowedIn, pkg)` for *any* package importing `net` — a single-package
  allowance (`internal/ipc`) with **no** `test/` or `tools/` escape hatch, unlike `.golangci.yml`'s
  `exclude-rules` (which exempt `^test/` from forbidigo/gosec/unparam only, a different mechanism).
  I confirmed this in the guard source. Task 7 is scoped to `test/bench`, devtool and CI; widening
  `internal/ipc`'s shipped surface to add a streaming primitive would be out of scope and would
  re-open tasks 1–2. The implementer took the right branch.
- **It does not under-warm.** The spec's stated *purpose* for step 3 is *"so the WAL, registry, ring
  … are warm"* — a data-structure goal, reached identically either way. My own run confirms 40.6 MB
  across 2000 requests actually lands.
- **The accept-loop concern is backwards.** A single held connection would exercise `Accept` **once**
  and leave the accept path *cold*. Per-request `Send` exercises accept + per-connection setup and
  teardown 2000 times — which is exactly the shape the *measured* phase then uses, since every real
  hook is a fresh process making exactly one connection (`internal/ipc/client.go` `Send`: connect,
  write, ACK, `defer conn.Close()`). The adaptation warms **more** of the path under measurement, not
  less.
- **No tail inflation.** I looked for a mechanism and found none, and the data contradicts it: B-B
  `p99` = 0.576 ms *with* the full warm-up applied, and the daemon's observed histogram `p99` =
  2.048 ms. Nothing in B-A's tail traces to warm-up shape; B-A's tail tracks the floor's tail.
- **One honest caveat, for the record, not a fix:** the single thing a held connection would exercise
  and this does not is the server's *multi-request-per-connection* read-loop framing. That path is
  not on the production hot path and nothing gated depends on it — but it means this harness never
  warms or exercises it. Worth one line in the `notes` array if the artifact is ever revised.

The report flags the deviation explicitly at task-7-report.md:153-177 rather than letting it pass
silently. That is the correct disclosure behaviour.

---

## 3. Adjudication (c) — `bench-gate` as a required check

**Confirmed clean. Nothing in the repo pretends the requiredness is configured.**

- `.github/workflows/ci.yml:89-93` — the replacement comment says it outright: *"Marking it a
  required check on develop/main is a GitHub branch-protection setting, not a file in this repo — see
  task-7-report.md for the controller note."* Accurate and self-documenting.
- `task-7-report.md:189-191` records it for the controller, per the brief's instruction.
- `git grep -i "required check"` across the repo returns only (i) those two ci.yml comments, and (ii)
  plan/spec prose under `plans/` (`00-ARCHITECTURE.md:2033`, `V2-SP-05-…:1902/1972`, etc.) which
  describes *intent*, not *state*. No README, CONTRIBUTING, docs page or workflow asserts the
  branch protection is in place.

**Escalate to the human at branch finish** (two items, one of which is a trap):

1. `bench-gate` must be marked a required check on `develop` and `main` in GitHub branch protection.
   **Do this only after the first real CI run is green on all three platforms** — see I-1.
2. **Do not "fix" the remaining `continue-on-error`.** `plans/V4-VERIFY-…:1152` asserts that
   `git grep -n "continue-on-error" -- .github/workflows/ci.yml` produces no output. It still matches
   `ci.yml:109-111` (`replay-gate`), which **SP-02 owns** and this task was explicitly forbidden to
   touch. That V4 check is correctly red until SP-02 lands. Flagging it so nobody removes it here.

---

## 4. Verdict 1 — SPEC COMPLIANCE (row by row)

### 4.1 Bench-harness spec (task-7-spec.md steps 1–10)

| Step | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | temp project (or `--project`), `go build` real binary, set `QOMPACK_PROJECT_ROOT` + `QOMPACK_IPC_ADDR` | **adapted** | `main.go:142-195`, `process.go:50-80`. Binary is built into a *separate* temp build dir rather than into the project dir. Trivial, arguably better (keeps the project tree free of an artifact). Env override applied to both children (`buildChildEnv`) and this process (`os.Setenv`) before `ipc.Resolve` — ordering correct. |
| 2 | real daemon child process, wait for `admin.ping` | **met** | `process.go:115-124` spawns `binPath daemon --project …` as a real child (deliberately bypassing `SpawnDetached` so lifetime is owned); `transport.go:101-120` polls a real `admin.ping` round trip. |
| 3 | 2000 `observe.tool` warm-up, ~40 MB, seed 1, Read/Bash/Grep/Edit, 4 KB–256 KB | **met (transport adapted)** | `measure.go:28-48`, `payload.go:15-104`. Seed 1 (`newPayloadGen(1)`), four kinds round-robined, sizes clamped to [4 KB, 256 KB]. **I measured 40.6 MB in my own run.** Persistent-connection wording adapted — see adjudication (b). |
| 3 | `notes` explains wave-1 stubs, B-C omitted | **met** | `measure.go:83-95`; B-C absent from `budgets`; note present verbatim in my run. |
| 4 | floor = 200 × `qompack version`, measured first | **met** | `main.go:227-232`, `process.go:191-193`. `runVersion` verified clean at `internal/cli/commands.go:63` — no stdin, no root resolve, no `.qompack/`. Order (warm → floor → B-A/B-D → B-E) matches the spec. |
| 5 | N × real `qompack observe tool`, one at a time, `time.Now()` around `cmd.Run()`, representative stdin | **met** | `process.go:165-186`. Serial loop, timer tight around `cmd.Run()`, fixed byte-identical payload (`payload.go:110-126`) so spawn-to-spawn variance is host cost not payload noise — a good call the spec did not demand. |
| 6 | per-sample `B-A_i = max(0, B-D_i − floor_p50)`; `b_a_method` + full `spawn_floor_ms` in artifact | **met** | `report.go:96-106` (`subtractFloor`), `report.go:48` (`bAMethod` string verbatim), `report.go:29-33` + `main.go:267-269` (`spawn_floor_ms` n/p50/p99). Pinned by `TestSubtractFloor` / `TestSubtractFloor_NeverNegative`. |
| 7 | B-E = 50 × `qompack checkpoint`, PreCompact payload | **met** | `main.go:247-253`, `payload.go:130-143` (`HookEventName: "PreCompact"`, `Trigger: "manual"`). |
| 8 | B-B + observed/estimated `hook.controlled` pair from `status` | **met** | `measure.go:52-71` (`fetchStatus`), `main.go:261` reads `snap.Latency[budgetHistName(obs.BB)]`, `measure.go:83-95` emits both notes. Histogram names looked up via `obs.Budgets()` rather than spelled as literals — good. |
| 9 | `out.json` exact shape | **met** | `report.go:15-43`; `TestReport_MatchesGoldenShape` pins the spec's own example verbatim via `require.JSONEq`, including `limit_ms: null` / `pass: null` for B-D (pointer fields, correctly). |
| 10 | exit non-zero on any gated failure; B-D never gates; `time.Sleep` only here | **met** | `report.go:52-59` (`GateFailed`), `main.go:126-130`. Only `time.Sleep` is `transport.go:118`, under `test/bench`, exempted by `tools/devtool/sleepcheck.go:70`. Verified by `devtool lint`. |

Also: **gate direction is correct** — `pass := p99 < limit` (`report.go:126`, `report.go:145`), i.e.
fail at `p99 ≥ limit`, matching *"fails the build if B-A p99 ≥ 15 ms or B-E p99 ≥ 2 s"*.
**Limits are not re-hardcoded** — `budgetLimit` (`report.go:155-162`) reads `config.Defaults()` +
`obs.Budgets()`, pinned by `TestBudgetLimit_ReadsFromConfigDefaults`. This is the brief's binding
ruling, met properly.

### 4.2 devtool `bench-hotpath` body

| Requirement | Status | Evidence |
|---|---|---|
| replace the "harness not present" stub body | **met — no change required (brief's premise was stale)** | `tools/devtool/benchhotpath.go:11-19` already forwards `go run ./test/bench/hotpath <args>` with inherited stdio once `dirHasGoFiles` is true. SP-01 shipped more than the brief assumed. The implementer verified by running it end to end rather than assuming; I confirmed the file's contents. |
| forward `--iterations --hook --warm-daemon --json --project` | **met** | args pass through verbatim; `parseFlags` (`main.go:60-79`) declares all five. |
| print a human summary | **met** | `printSummary` (`main.go:303-329`), reaching the terminal via `goInherit` → `runInherit` inherited stdio. Reproduced in my own run. |
| exit non-zero on gated failure | **met** | `goInherit` returns the child's error; my run shows `exit status 1` propagating. |
| devtool conventions (registration, output format) | **met** | task already registered; `goInherit` is devtool's own helper; summary format matches the tool's plain-text style. |
| See **M-7** — the file's doc comment is now stale. | minor | |

### 4.3 `.github/workflows/ci.yml` — hunk-by-hunk surgicality

Two hunks, 11 lines, **all inside `bench-gate`**. Line by line:

- `-    # NOT a required check until the end of wave 1 (§8)…` / `-    # SP-05 removes this
  continue-on-error…` / `-    continue-on-error: true` → replaced by a 4-line comment that states the
  wave-1 promotion and points the branch-protection action at the report. **`continue-on-error`
  removed from `bench-gate` only.** ✅
- `strategy: fail-fast: false` / `matrix: os: [ubuntu-latest, macos-latest, windows-latest]` /
  `runs-on: ${{ matrix.os }}` — **unchanged**, already correct. ✅
- `- uses: actions/checkout@v4 with: { fetch-depth: 0 }` and `- uses: actions/setup-go@v5 with:
  { go-version: '1.26.x', cache: true }` — **unchanged**. The spec's yaml block shows these without
  `fetch-depth`/`cache`; keeping the file's existing form is correct under *"Keep surrounding
  workflow structure untouched."* ✅
- `-      - run: … bench-hotpath --iterations 2000 --json bench.json`
  `+      - run: … bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json "bench-${{ matrix.os }}.json"`
  — matches the spec's command, with the per-OS filename the spec's own `upload-artifact` step
  implies. ✅
- `+      - uses: actions/upload-artifact@v4` / `+        with: { name: "bench-${{ matrix.os }}", path: "bench-${{ matrix.os }}.json" }`
  — new, per spec. The quoting is **required**, not cosmetic: a plain scalar in YAML flow context
  cannot contain `{`/`}`. The report documents catching this with a real YAML parse; `nightly.yml`'s
  pre-existing `name:` was already quoted for the same reason, so this follows the file's own
  convention. ✅
- **`replay-gate`: zero changes.** I diffed and read the surrounding file — `continue-on-error: true`
  at `ci.yml:111` is intact, comment intact, step intact. ✅ **Constraint met.**
- No other job in `ci.yml` touched. ✅

### 4.4 `.github/workflows/nightly.yml`

- `bench-deep` already had the three-OS matrix and the `upload-artifact` step; the diff is 2 lines:
  the `--iterations 5000` command gains `--hook observe-tool --warm-daemon` and the per-OS json
  filename, and `path:` is aligned to that filename (it previously pointed at `bench.json` while
  `name:` was already per-OS — **a pre-existing latent bug this change happens to fix correctly**).
- Nightly variant at 5000 iterations: **met**. Structure preserved: **met**.

### 4.5 Baseline mechanism

- Existing convention detected and followed: `testdata/bench-baseline.txt` already carries a
  commented header + raw `go test -bench` output at `-count 10`; the SP-05 section appends in exactly
  that shape with the same command line, host identification and a budget-vs-result table.
- All four required benchmarks recorded: `BenchmarkReadState` (~52–60 µs vs <100 µs),
  `BenchmarkEncodeRequest` (~1.1–1.5 µs vs <5 µs), `BenchmarkIngestAccept` (~3.3–3.7 µs),
  `BenchmarkServerRoundTrip` (~42–63 µs). All MET.
- **Locally-seeded values are clearly marked**: the section names the exact host
  ("Windows 11 / Intel Core Ultra 7 155H / NTFS"), repeats the parent file's own caveat that
  filesystem-bound entries will read very differently on Linux CI, and states explicitly that
  `BenchmarkServerRoundTrip` excludes process spawn and points at `test/bench/hotpath` for that.
  Constraint met.
- Drift mechanism: benchstat-compatible raw output at `-count 10` (≥6 samples, as the file's own
  header explains is required for a confidence interval). Correct.

### 4.6 Commit-7 checklist

| Row | Status |
|---|---|
| `report_test.go` first — percentiles vs 512-sample fixture, floor subtraction, out.json golden incl. `b_a_method`/`spawn_floor_ms`/`notes`/four rows; confirm RED | **met** — RED evidence is a compile failure (`undefined: percentiles`), which is the honest RED for a TDD-first file in a package that does not yet exist. Fixture is genuinely hand-checkable and fed **descending** so the sort is load-bearing. |
| add `main.go`, `report.go`, `payload.go`; add the devtool task | **adapted** — 7 files instead of 3 (`process.go`, `measure.go`, `transport.go` split out); devtool task pre-existed and needed no edit. Disclosed at task-7-report.md:192-196. Acceptable. |
| run locally at n=2000 and **confirm `B-A.pass == true` and `B-E.pass == true`** | **NOT MET** — B-E passes; **B-A fails**. See **I-1**. |
| update `ci.yml` + `nightly.yml`; mark `bench-gate` required | **met (files)** / **deferred (branch protection, correctly — adjudication (c))** |
| add the four benchmarks and record them | **met** (all four pre-existed; recorded this task) |
| push and confirm CI green on all three platforms before merging to `develop` | **outstanding by construction** — cannot be done from the worktree. Must gate the merge. |

---

## 5. Verdict 2 — CODE QUALITY

### Correctness (all clear)

- **Percentile math.** Nearest-rank, `rank = ceil(p·n)` clamped to `[1,n]`, 1-indexed
  (`report.go:65-78`) — **identical to `internal/obs/hist.go:141-148`'s own method**, which I checked
  directly. No interpolation, no off-by-one: the 512-sample fixture's p50→256 ms, p95→487 ms,
  p99→507 ms, p999→512 ms are all correct under `ceil`, and are hand-verified in the test's own
  comment. Empty input → 0. `n=2000`: p99 → rank 1980; `n=5000`: p99 → rank 4950; `n=50` (B-E):
  p999 → rank 50 = max, which is why B-E's p99/p999/max collapse to one value — correct, not a bug.
  `max` is taken as the true largest element, not `percentile(1.0)`.
- **Sample-set hygiene.** `percentiles` sorts in place, so every caller copies first:
  `main.go:232` (`append(nil, floorSamples...)`) and `report.go:117` (`cp := append(nil, samples...)`).
  Crucially this means `bdSamples` is **still in collection order** when `subtractFloor` runs at
  `main.go:245` — the per-sample derivation is genuinely per-sample and not a sorted-order artifact.
  Checked; correct.
- **Timer placement.** `start := time.Now()` immediately before `cmd.Run()`, `elapsed` immediately
  after (`process.go:177-179`). No setup (payload marshal, `exec.Command` construction, stderr buffer
  alloc) inside the window. Correct for B-D; see (a.1) for what that means for B-A.
- **Gate direction.** `p99 < limit` in both builders. Fail at `≥`. Correct.
- **Warm-up sufficiency.** 2000 requests, 40.6 MB measured, seed 1, four tool shapes — see (b).
  Sufficient; no under-warming.
- **Daemon lifecycle hygiene.** Exactly one daemon per run. Defer ordering is **correct and
  load-bearing**: `cleanupProject` (`main.go:146`) and `buildDir` removal (`main.go:162`) are
  registered *before* `stopDaemon` (`main.go:211`), so LIFO runs `stopDaemon` **first** — the daemon
  is down before its project tree and its own executable are deleted. No leaked pipes: hook children
  are reaped by `cmd.Run()`, and the harness's own `ipc.Client`s are all `defer c.Close()`d. The
  `QOMPACK_IPC_ADDR` override carries an 8-byte crypto-random suffix so concurrent runs cannot
  collide, and the POSIX path is anchored under `os.TempDir()` with a short name to stay inside
  `internal/ipc`'s `sun_path` guard — a genuinely thoughtful detail. (One residual hole: **I-3**.)
- **Teardown tolerance of the go-winio wrinkle.** `stopDaemon` (`process.go:141-158`) sends
  `admin.shutdown` best-effort, waits `daemonDownBound` (10 s) on a real `cmd.Wait()`, then `Kill`s
  and **still drains `done`** (no goroutine leak). It tolerates a slow close without swallowing a
  *measurement* failure — the measurement is complete before teardown runs, and a daemon that died
  *during* the run is caught earlier by `fetchStatus` returning an error → exit 1. Correct tolerance,
  not masking. (Visibility nit: **M-5**.)
- **CI matrix correctness.** Three OSes, `fail-fast: false` (so one platform's failure still yields
  the other two's artifacts — the right choice for a bench gate), go 1.26.x, per-OS artifact name and
  path aligned, `n=2000` in CI / `n=5000` nightly. Per-OS gate values are **not** per-OS by design and
  should not be: all three read the same `config.Defaults()` limits (15/2/2000 ms) through
  `obs.Budgets()`, which is what §8.1 requires. Artifact is written (`main.go:118-123`) **before** the
  gate check (`main.go:126`), so a *failing* run still uploads its json — exactly right for
  diagnosing a red gate.
- **Baseline drift mechanism.** benchstat-compatible, `-count 10`, same command as the file's
  existing sections, host-caveated. Correct.
- **Constants / no magic numbers.** Every literal is a named, doc-commented constant citing its spec
  step (`spawnFloorIterations`, `checkpointIterations`, `warmIterations`, `daemonUpBound`,
  `minWarmPayloadBytes`, …). `devtool lint`'s `nomagic` passes. No underscore-counter obligations
  arise (the harness registers no counters).
- **File-write scope.** Writes confined to the temp project, temp `HOME`, temp build dir, and the
  caller-named `--json` artifact (which the spec requires). `--project`, when the caller names it, is
  never removed. Constraint met.

### Findings

#### Important

**I-1 — A hard three-platform gate is being shipped that has never been observed green.**
`.github/workflows/ci.yml:89-106` (removal of `continue-on-error`) + task-7-spec.md:87 (unmet
checklist row) + task-7-report.md:81.
The brief mandates the removal unconditionally, so the implementer had no alternative and correctly
refused to relax the budget. But the *combination* — checklist row "confirm `B-A.pass == true`"
unmet, gate promoted to hard-fail, and only an optimistic expectation (a.3) standing behind it — is
a real risk that must not be lost in the report's prose.
**Resolution is a controller/human decision, not an implementer fix.** Concretely: **do not mark
`bench-gate` a required check in branch protection until the first real CI run is green on all three
platforms**, and treat a `windows-latest` red as a wave-2 methodology amendment (a.4), never as a
reason to touch 15 ms. Escalate at branch finish.

**I-2 — No delivery-integrity assertion: a partially-degraded run biases B-A *downward* and passes
the gate silently.** `test/bench/hotpath/measure.go:52-71` and `main.go:255-277`.
`internal/ipc/client.go`'s `Send` degrades to `spoolAndReturn` on a connect timeout (step 5), on a
write error, on `DaemonEnabled == false`, and on `HotSpool` breach state (step 3). A spooled hook
exits *faster* than a delivered one — so a run where, say, 5 % of the 2000 hook spawns failed to
reach the daemon would produce a **better-looking B-A** and a **passing gate** while measuring the
degraded path. A wholly dead daemon is caught (`fetchStatus` errors → exit 1), but partial
degradation is invisible.
The harness already holds the evidence needed: `snap.Latency[l0_ingest].N` must equal
`warmIterations·(warm-daemon?1:0) + iterations`. I verified this identity holds exactly in both the
implementer's run (4000 = 2000+2000) and mine (2060 = 2000+60) — so today's numbers *are* clean, but
nothing enforces it. Assert it and fail loudly on a mismatch (or at minimum emit it as a `note` so a
CI artifact is self-diagnosing).

**I-3 — A dial failure inside a measured spawn can leave a second, harness-unmanaged daemon behind.**
`test/bench/hotpath/process.go:141-158` vs `internal/ipc/client.go` `Send` step 5
(`c.lazySpawn()` on connect error) and `internal/cli/hookclient.go:265` (`spawn := daemon.SpawnDetached`).
`observe.tool` runs on `state.bin`'s 5 ms connect deadline; on a host with the dispersion this
harness itself documents, a timed-out dial is plausible, and the hook will then spawn a **detached**
daemon on the same `QOMPACK_IPC_ADDR` with the same `QOMPACK_PROJECT_ROOT`. `stopDaemon` only manages
its own child, so that second daemon survives the run — holding a named pipe and writing into a
project directory the harness is about to `RemoveAll`. Nothing in the current code detects it.
Cheapest robust fix: after teardown, probe the address once more and fail/warn if anything still
answers. (I-2's N check would also surface the dial failures that trigger this.)

#### Minor

**M-1 — Dead code.** `test/bench/hotpath/payload.go:145-153` — `warmupTotalBytes` is defined and
never called (`main.go` uses the byte count `warmDaemon` actually returns). It escapes CI because
`.golangci.yml` enables `staticcheck` but **not** `unused`, so U1000 never runs. Delete it, or the
next reader will assume it is the number in the summary line.

**M-2 — Doc comment contradicts the code it documents.** `test/bench/hotpath/measure.go:23-27` claims
*"An occasional refused request … does not abort the warm-up"*, but the body at
`measure.go:43-45` returns an error on any `Send` failure. (In practice `Send` never returns a
propagating error, so the branch is near-dead — which is exactly why the wrong comment will survive.)
Separately, `resp.OK` is never checked, so a genuinely *refused* request silently counts as warm —
that half matches the comment's intent but is worth stating explicitly.

**M-3 — Two distinct quantities share one constant.** `test/bench/hotpath/main.go:64` uses
`warmIterations` as the **default value of `--iterations`**. Changing the warm-up count would
silently change the default measurement count. Give `--iterations` its own named default.

**M-4 — B-B's `n` is undocumented and differs from the spec's example.** `main.go:261` +
`report.go:137-149` report the daemon histogram's N verbatim, which includes warm-up traffic
(2060 / 4000 in the two runs) whereas task-7-spec.md:24's example shows `n: 2000`. Reporting what the
daemon actually observed is the *right* choice — and it makes B-B conservative, since warm-up carries
4 KB–256 KB payloads — but a `note` saying so would make the artifact self-describing rather than
requiring a reader to reconstruct it.

**M-5 — Teardown tolerance without visibility.** `test/bench/hotpath/process.go:150-157` discards
`cmd.Wait()`'s error entirely and prints nothing when it has to `Kill` after `daemonDownBound`. The
tolerance is correct (see above); the silence is not. One stderr line on the kill path preserves the
go-winio signal without ever failing the bench.

**M-6 — The floor is measured once, against an idle daemon, minutes before the samples it is
subtracted from.** `test/bench/hotpath/main.go:227-245`. The spec mandates this ordering
(task-7-spec.md:12, "measure the floor **first**"), so it is not a deviation — but `floor_p50` is
collected while the daemon is idle and then subtracted from samples collected while the daemon is
under 2000-spawn load. Interleaving floor spawns with hook spawns (alternate, then subtract a
contemporaneous paired floor) would make the estimator both contemporaneous and dispersion-reducing,
and is the most direct answer to (a.1). **Wave-2 proposal for the controller, not a task-7 fix.**

**M-7 — Stale doc comment on the devtool task.** `tools/devtool/benchhotpath.go:8-10` still reads
*"SP-05 owns it; before SP-05 lands this degrades to a zero-exit no-op"*, and the `dirHasGoFiles`
guard at line 13 is now permanently true. The implementer's decision not to touch a file that needed
no functional change is defensible; a one-line comment refresh would nonetheless stop the next reader
believing the harness might be absent.

### Maintainability (positive notes)

- Every non-obvious constant and function carries a doc comment naming the spec step it implements
  (`task-7-spec.md step N`), which makes the harness auditable against the plan without a diff.
- `budgetLimit` / `budgetHistName` (`report.go:155-173`) route every limit and histogram name through
  `obs.Budgets()`, so the harness cannot drift from the budget table — and
  `TestBudgetLimit_ReadsFromConfigDefaults` pins that it does not.
- `buildBudgetRow` copies before sorting and documents *why* — the kind of comment that survives.
- `LimitMs`/`Pass` as pointers to get honest `null`s instead of misleading zeros is exactly right for
  an artifact other tools will parse.
- The `--json` artifact is written before the gate check, so red runs are diagnosable. Deliberate and
  correct.
- The 7-file split is a readability win over one 900-line `main.go`, and the report discloses the
  divergence from the brief's file list rather than hiding it.

---

## 6. Summary

**Verdict: Needs fixes (0 Critical, 3 Important).**

| Severity | Count | IDs |
|---|---|---|
| Critical | 0 | — |
| Important | 3 | I-1 (unvalidated hard gate — controller escalation), I-2 (no delivery-integrity assertion), I-3 (lazy-spawn daemon leak) |
| Minor | 7 | M-1 … M-7 |

**Adjudications:**
- **(a)** Methodology is faithful to the mandated spec; jitter diagnosis is **sound** and I
  independently reproduced its decisive evidence (`B-D.p99` 76.4 ms **<** floor `p99` 79.1 ms). It is
  **not** excusing a product regression — the 250 ms connect floor does not apply to `observe.tool`,
  `state.bin` costs 55 µs, and every request was delivered. But the harness times the **hook process
  spawn**, which B-A's own definition excludes, and subtracting a *constant* floor removes the floor's
  location and none of its dispersion — so the gated statistic is contaminated by design. "CI
  bare-metal will pass" is optimistic (GitHub runners are shared 2–4 vCPU VMs; passing needs <20 of
  2000 spawns to exceed floor_p50 by 15 ms). **This branch cannot honestly claim the <15 ms gate is
  met — on any platform.** It can honestly claim the method is the mandated one, the daemon-side path
  is healthy, and no budget was fudged.
- **(b)** Faithful and **forced** (`test/guards.TestGuard_NoNetworkImports` allows `net` in
  `internal/ipc` only, with no `test/` exemption). It does **not** under-warm — the concern is
  inverted: per-request dialing exercises the accept path 2000 times where a held connection would
  exercise it once, and it matches the shape of the measured phase exactly. No tail-inflation
  mechanism found; B-B `p99` = 0.576 ms with warm-up applied. Sole caveat: the server's
  multi-request-per-connection read loop is never exercised (not on the production hot path).
- **(c)** Confirmed clean. Nothing in the repo pretends branch protection is configured;
  `ci.yml:89-93` and the report both say so explicitly. **Escalate at branch finish:** mark
  `bench-gate` required **only after** the first CI run is green on all three platforms, and **do
  not** remove `replay-gate`'s `continue-on-error` (SP-02 owns it) even though
  `plans/V4-VERIFY-…:1152` will keep flagging it.

**Commit hygiene: clean.** Exactly one commit; subject, body and `Refs:` line byte-identical to the
brief's mandated block; no attribution trailers.

---

# Re-review (fix round 1)

Scope: the delta `ab088f4f → be911428` (`review-task7-fix1.diff`, 39 KB) plus "Fix round 1" in
task-7-report.md. Controller ruling #29 amends task-7-spec.md step 6. **M-6 is controller-owned and
is not re-flagged.**

**Verdict: Needs fixes (0 Critical, 1 Important).** One NEW defect, introduced by the ruling-#29
fix itself. Everything the coordinator asked me to verify is otherwise correct, and all three
originally-Important items are closed at the code level.

## Commit hygiene

| Check | Result |
|---|---|
| single commit | `git log --oneline 6fc27a3b..be911428` -> 1 commit, `be91142` |
| message byte-identical | `git show -s --format=%B be911428 \| cat -A` -> subject, body and `Refs: SP-05, §8.1, §11.3, 00-ARCHITECTURE §7, §8` unchanged from `ab088f4f` and from the brief's mandated block |
| no attribution trailers | confirmed |
| tree clean | `git status --porcelain` empty |
| vet / tests / lint | `go vet ./test/bench/...` clean; `go test ./test/bench/... ./test/guards/...` both `ok`; `go run ./tools/devtool lint` -> all 7 sub-checks PASS |

## My own re-run (n=60, `--warm-daemon`)

```
  B-A                 n=2060  p50=1.024ms p95=2.048ms p99= 2.048ms p999=3.072ms max= 15.000ms limit=15ms   [PASS]
  B-B                 n=2060  p50=0.002ms p95=0.576ms p99= 0.640ms p999=1.536ms max= 10.836ms limit=2ms    [PASS]
  B-D                 n=60    p50=19.738ms               p99=78.222ms                         limit=n/a    [reported]
  B-E                 n=50    p50=77.622ms               p99=134.595ms                        limit=2000ms [PASS]
  B-A_spawn_estimate  n=60    p50=0.000ms p95=10.888ms   p99=57.932ms                         limit=n/a    [reported]
```

Exit 0. The gate now passes, the wall-clock number survives as an ungated diagnostic, and the
`B-A_spawn_estimate` p99 (57.9 ms) is visibly the same contaminated statistic as before — exactly
the intended separation.

## Per-item status

### (1) B-A gate re-sourced to the daemon's estimated `hook_controlled` — MET, with a new caveat (see N-1)

- `main.go:289-300`: `baSnap := snap.Latency[budgetHistName(obs.BA)]` ->
  `buildBudgetRowFromSnapshot(string(obs.BA), baSnap, budgetLimit(cfg, obs.BA), true)`. The
  histogram name and the 15 ms limit both route through `obs.Budgets()`; nothing is re-hardcoded.
- I traced the series to its source: `internal/daemon/handlers.go:177-191` —
  `observed := (recvTS − req.TS)`, `estimated := observed + hotPathTailAllowance`
  (`internal/daemon/budget.go:19`, 1 ms), written to `histName(obs.BA)` and pushed to
  `d.hotSamples`, i.e. **the identical value the breach detector consumes.** `req.TS` is stamped as
  the literal first statement of `doHook` (`internal/cli/hookclient.go:197`, commented "FIRST
  statement — the B-A origin"), so the series is genuinely anchored at hook `main()` entry and
  genuinely excludes process creation — matching `internal/obs/budgets.go:15`'s definition. The
  re-sourcing is correct and well founded, and `validHotPathTS` already rejects absent/skewed/stale
  timestamps so garbage cannot enter the gated series.
- Wall-clock numbers renamed as diagnostics: `budgetIDBASpawnEstimate = "B-A_spawn_estimate"`
  (`report.go:72`), emitted at `main.go:306` with `gated=false` -> `limit_ms`/`pass` both null, like
  B-D. `TestBudgetIDBASpawnEstimate_NeverCollidesWithB_A` pins it at the constant level, not only in
  the fixture. `GateFailed` is unchanged and cannot see a nil `Pass`, so the diagnostic rows are
  structurally incapable of gating.
- Dispersion caveat documented: `report.go:46-63` (`bAMethod`'s doc comment **and** its emitted
  string) and `report.go:65-72` both state that subtracting a constant removes the floor's location
  and none of its dispersion — unbiased at p50, contaminated at p99. The caveat now ships **inside
  the artifact**, so a CI json is self-explaining. `printSummary`'s column width was widened to 19 to
  fit the new id. Good.

### (2) I-2 delivery-integrity assert — MET

- `report.go:199-219` `checkDeliveryIntegrity(gotN, iterations, warmDaemonRan)`: exact equality
  against `iterations + warmIterations·[warm ran]`, **no tolerance in either direction**, with an
  error message that names both counts and explains why partial delivery would bias the numbers.
- Wired at `main.go:277-280`, **before** anything is built from the snapshot — so a degraded run
  returns an error from `runHarness` and `runMain` exits 1 without emitting a Report. Correct
  ordering: it cannot pass a gate on partial data.
- `TestCheckDeliveryIntegrity` pins five cases including **one-over** (2001) and the warm-ran-but-
  count-matches-loop-only case — i.e. it is a genuine equality check, not a floor. Good test design.
- I verified the identity holds in my own run (2060 = 2000 + 60) and that the guard is on the real
  path, not just unit-tested.

### (3) I-3 teardown leaves zero daemons + honest best-effort caveat — MET

- `process.go:147-171` `stopDaemon` now takes `errw`, and `process.go:203-227` adds
  `detectAndStopOrphan`, called only **after** `d.cmd.Wait()` has actually returned. The ordering is
  the load-bearing part and it is right: once our own child is confirmed dead by a real `Wait`, its
  pipe/socket handle is gone, so anything still answering `admin.ping` at that address provably is
  not ours. No false-positive window.
- Orphan handling: warn loudly -> `admin.shutdown` -> poll to `orphanCheckDeadline` (5 s) ->
  either "orphaned daemon stopped" or a second loud WARNING naming exactly what may still be held
  (daemon.lock, named pipe/socket).
- **Best-effort caveat is honestly documented**, not hidden: `process.go:196-201` states plainly
  that this is best-effort, why it is not a hard failure (the measurement is already complete, and
  I-2's check already catches the degraded-run scenario that triggers `lazySpawn` in the first
  place), and that a refusing orphan is reported rather than failing the bench. The doc comment also
  correctly identifies the narrow surviving race — a lazy spawn landing after our daemon released
  `daemon.lock` — rather than claiming the hole is closed. That is the right level of honesty.
- M-5 folded in at `process.go:162-167`: the `Kill` path now prints one line naming the bound it
  hit, so go-winio tolerance no longer discards the signal.
- Cost in the normal case is negligible (a nonexistent pipe/socket refuses fast, as
  `internal/cli/hookclient.go:141-143` documents).

### (4) Golden / baselines / nightly / ci.yml consistency — MET

- Golden updated to the five-row shape, and — better than asked — `b_a_method` is now **injected
  from the `bAMethod` constant** via `fmt.Sprintf(%q)` instead of being duplicated as a second
  string literal, so the fixture cannot silently drift from what `report.go` emits. The B-A row's
  numbers were changed to plausible daemon-side values and `B-A_spawn_estimate` carries the old
  wall-clock ones. Shape-only, as the checklist requires.
- `ci.yml`, `nightly.yml` and `testdata/bench-baseline.txt` are **byte-identical to `ab088f4f`**
  (`git diff ab088f4f..be911428 -- .github/ testdata/` is empty). Correct and consistent: the
  amendment is internal to the harness's Go code; the invoked command line and the four
  micro-benchmarks are genuinely unaffected. **`replay-gate` remains untouched.** Surgicality
  preserved.
- Only other file in the delta is `tools/devtool/benchhotpath.go` — a comment refresh (M-7), no
  behaviour change.

### (5) The six minors — all closed

| ID | Status |
|---|---|
| M-1 dead `warmupTotalBytes` | **fixed** — function and its now-unused `math` import removed (`payload.go`) |
| M-2 warm-up doc/code mismatch | **fixed** — `measure.go:26-36` now states accurately that `Send` never propagates, that the branch is near-dead and why it is kept, that `resp.OK` is deliberately unchecked, and points at I-2 as the real guard. Better than deleting the branch. |
| M-3 `--iterations` aliased `warmIterations` | **fixed** — `defaultIterations` (`main.go:38-43`) is its own named constant with the rationale recorded |
| M-4 B-B's `n` includes warm-up | **fixed** — `buildNotes` (`measure.go:100-104`) emits a note whenever warm-up ran, naming the count and why it is conservative for B-B |
| M-5 silent `Kill` | **fixed** — see (3) |
| M-7 stale devtool comment | **fixed** — `tools/devtool/benchhotpath.go:8-11` now says the harness has landed and the fallback exists only for a bisect |
| M-6 floor not contemporaneous | **controller-owned — not re-flagged**, per instruction |

## NEW defect

**N-1 (Important) — the newly-gated B-A row is computed over a population that is mostly NOT hook
spawns: the 2000 in-process warm-up requests are in it, and nothing says so.**
`test/bench/hotpath/main.go:289` (`snap.Latency[budgetHistName(obs.BA)]`) against
`internal/daemon/handlers.go:143-145` and `test/bench/hotpath/measure.go:37-57`.

`recordHotPathSample` fires for **every** `req.Op.HotPath()` request the daemon dispatches. The
harness's own warm-up (`warmDaemon`) sends 2000 `ipc.OpObserveTool` requests through an **in-process**
`ipc.Client` — their `req.TS` is stamped microseconds before the local `Send`, not at a spawned
process's `main()` entry — so all 2000 land in the very histogram that ruling #29 just made the gate.

This is not theoretical. My own run reports **`B-A n=2060`** for 60 hook spawns, and the
implementer's own fix-round-1 run reports **`B-A n=4000`** for 2000 hook spawns (task-7-report.md's
"Fix round 1" block) — half the gated population is warm-up traffic in the CI configuration, and
~97% of it at n=60. The effect on the gated statistic:

- **The percentile shifts.** With `n = 4000` (CI's `--iterations 2000 --warm-daemon`), nearest-rank
  p99 is rank 3960. The warm-up samples are the fast ones — `hook_controlled_observed` p50 is
  0.002 ms in a warm-dominated run versus 1.024 ms in the 50/50 run, so they sort to the bottom —
  which puts rank 3960 at roughly the **1960th of the 2000 real hook samples, i.e. the hook
  population's p98, reported and gated as if it were p99.** At smaller `--iterations` the distortion
  is far worse (at n=60 the gated "p99" sits around the hook population's p67).
- **The reported p50 is not a hook number at all** — 1.024 ms in my run is the warm-up median, not
  the hook median.
- The bias is **optimistic in every case**: adding a fast sub-population can only lower a percentile.

The fix round added exactly this note for B-B (M-4, `measure.go:100-104`) and missed it for B-A —
the row that is now the entire point of the gate, and where the contamination is qualitatively worse
(B-B's warm-up samples are at least *real ingest work*, merely with bigger payloads; B-A's warm-up
samples are not hook-process measurements at all).

Minimum honest fix, cheap and in scope: extend `buildNotes`'s warm-up note to cover the **B-A** row
too, stating that `n` includes the warm-up requests, that those are in-process client sends rather
than hook spawns, and that the gated percentile is therefore taken over a mixed population and is
optimistically biased. Better, if the controller wants the artifact to disclose the split: take a
`status` snapshot immediately after warm-up and before the B-A/B-D loop and report the warm-up's own
`hook_controlled` N alongside. (Filtering the histogram by session is not possible through the
existing `status` op, so a note is the honest in-scope answer; a clean split would need a
daemon-side change, out of scope for task 7.)

I am **not** asking for the gate to be re-sourced again. The series ruling #29 selected is the right
one; it just needs to say what is in it.

## Observations (no action required)

- **1 ms quantization on the gated series.** `observed` is `(recvTS − req.TS)` in whole-millisecond
  wire timestamps (`internal/daemon/handlers.go:185`), plus a flat 1 ms `hotPathTailAllowance` — so
  the gated values are integer milliseconds (surfacing as 1.024 / 2.048 / 3.072 ms once the
  1/8-octave buckets round them). Against a 15 ms budget the gate can distinguish roughly fifteen
  states. That is inherent to the series ruling #29 selected — the same one the breach detector
  uses — not to this implementation, so I record it rather than flag it.
- B-A's `max` read exactly 15.000 ms in my run while p99 read 2.048 ms and the row PASSed. Correct
  — `obs.Budgets()` gates B-A on p99 (`Pct: pctP99`) — but a reader glancing at the summary may find
  `max == limit` alarming. Not worth a change.
- `detectAndStopOrphan`'s probe writes through the real spool on failure, i.e. into the project's
  own `.qompack/spool` before `cleanupProject` removes it. Within the declared write scope; noted
  only for completeness.

## Coordinator's concern 1 — agreed

Confirmed, and unchanged by this fix round. The harness now passes locally, but the gate has still
**never been observed green on real CI**, and the newly-gated series has never run on
`ubuntu-latest`/`macos-latest`/`windows-latest` at all. `continue-on-error` is removed. **Do not mark
`bench-gate` a required check in branch protection until the first three-platform CI run is green.**
This matches the ledgered escalation from the original review's I-1 verbatim. (Also unchanged: do
not remove `replay-gate`'s own `continue-on-error` — SP-02 owns it — even though
`plans/V4-VERIFY-…:1152` will keep flagging it.)

## Re-review verdict

**Needs fixes (0 Critical, 1 Important).**

| Item | Status |
|---|---|
| (1) B-A gate re-sourced, wall-clock renamed to diagnostic, caveat documented | **MET** (see N-1) |
| (2) I-2 delivery-integrity assert | **MET** |
| (3) I-3 zero-daemon teardown + honest best-effort caveat | **MET** |
| (4) golden / baselines / nightly / ci.yml consistency, replay-gate untouched | **MET** |
| (5) six minors M-1/2/3/4/5/7 | **all fixed** |
| M-6 | controller-owned, not re-flagged |
| commit: single, trailer-free, byte-identical message | **MET** |
| NEW defects | **1 Important — N-1** |

Original I-1 is now genuinely closed at the code level (the gated number is TS-anchored daemon data
matching B-A's own definition, and the checklist row "confirm `B-A.pass == true`" is met locally for
the first time); its residual is purely the branch-protection escalation above. N-1 is a disclosure
gap in the replacement, not a reason to revisit the replacement.

---

# Re-review (fix round 2)

Scope: the delta `be911428 → b6181b7a` (`review-task7-fix2.diff`, 23.5 KB) plus "Fix round 2" in
task-7-report.md. Four files, all under `test/bench/hotpath/`.

**Verdict: Approved (0 Critical, 0 Important, 3 Minor).** N-1 is closed — both halves, correctly.
No new Critical or Important defect. Three Minors are recorded below, one of which is my adjudication
of the implementer's concern 2 and carries a concrete recommendation.

## Commit hygiene

| Check | Result |
|---|---|
| single commit | `git log --oneline 6fc27a3b..b6181b7a` -> 1 commit, `b6181b7` |
| message byte-identical | `git show -s --format=%B b6181b7a \| cat -A` -> unchanged from `ab088f4f`/`be911428` and from the brief's mandated block, `Refs:` line included |
| no attribution trailers | confirmed |
| tree clean | `git status --porcelain` empty |
| blast radius | `git diff --stat be911428..b6181b7a -- .github/ testdata/ tools/` is **empty** — ci.yml, nightly.yml, the baselines and devtool are all untouched this round. Surgical. `replay-gate` still untouched. |
| vet / tests / lint | `go vet ./test/bench/...` clean; `go test ./test/bench/... ./test/guards/...` both `ok`; `go run ./tools/devtool lint` -> all 7 sub-checks PASS |

## My own re-runs

**Run A — `--iterations 60 --warm-daemon`** (directly comparable to my round-1 run, which read
`B-A n=2060`):

```
  B-A                 n=124  p50=2.048ms p95=2.048ms p99= 3.072ms p999=20.480ms max=19.000ms limit=15ms [PASS]
  B-B                 n=124  p50=0.002ms p95=0.576ms p99= 1.152ms                            limit=2ms  [PASS]
  B-D                 n=60   p50=19.885ms                p99=112.918ms                       limit=n/a  [reported]
  B-A_spawn_estimate  n=60   p50=0.257ms p95=6.631ms  p99=93.290ms                           limit=n/a  [reported]
  warm-up sent ~1.1 MB across 64 hot-path requests (plus 1936 admin.ping round trips)
```

`B-A n` fell from **2060 to 124** for the same 60 hook spawns — the contamination N-1 named is gone.

**Run B — `--iterations 30 --warm-daemon`** (the implementer's own concern-2 case). Reported under
Minor R2-3 below, because it produced a result neither the implementer nor the coordinator predicted.

## Per-item status

### (1) Warm-up restructure — MET

- `warmHotTranche = 64` (`main.go:38-64`) is a named constant with a doc comment that states the
  mechanism (`recordHotPathSample` fires for every `req.Op.HotPath()` request, unfilterable through
  the status op), the resulting bias, the fix, and — importantly — that 64 is **not tuned to any
  budget**, only chosen small relative to the 2000/5000 the spec's own commands use. That is the
  right way to justify a constant in a repo with a `nomagic` pass; `nomagic` passes.
- `warmDaemon` (`measure.go:37-79`) now runs two loops: `hotTranche` genuine seed-1 mixed-shape
  `OpObserveTool` sends, then `n − hotTranche` `OpAdminPing` sends on the same client. I verified the
  routing claim at source rather than taking it: `internal/ipc/op.go`'s `Op.HotPath()` returns true
  only for `OpObserveTool/OpObservePrompt/OpObserveStop`, so `admin.ping` provably cannot reach
  `recordHotPathSample` (`internal/daemon/handlers.go:143-145`) or `l0_ingest`. The bulk warm-up
  still opens 1936 real connections, so the accept/connection-loop warming that adjudication (b)
  credited in round 1 is fully preserved.
- Gated histogram is now hook-dominated at every configuration the spec invokes: **n = iterations +
  64**, i.e. 2064 (96.9 % real) at CI's 2000 and 5064 (98.7 %) at nightly's 5000. Confirmed at 124
  in my own run.
- Flag help text and both progress lines were updated to describe the new shape honestly rather than
  still claiming "2000 observe.tool requests". Good — a stale `--help` string is exactly how this
  kind of change rots.

**A worked check of the residual distortion, since this is the number the gate rests on.** Under
nearest-rank with a tranche of size `t = 64` appended to `k` hook samples, if the tranche sorts below
the hook population the gated rank maps back to hook-population rank
`ceil(0.99(k+64)) − 64 = ceil(0.99k + 0.36) − 1`, versus the hook population's own p99 rank
`ceil(0.99k)`. Those differ by **at most one rank**, and are exactly equal whenever `0.99k` is an
integer — which it is at `k = 2000` (1980) and `k = 5000` (4950), the only two values CI and nightly
ever use. So at both shipped configurations the gate reads the real hook population's p99 **exactly**,
not an approximation of it. That is a better outcome than the "≈p98" I could only bound in round 1.

### (2) Delivery-integrity assert updated — MET

- `checkDeliveryIntegrity` (`report.go:199-227`) moved its expectation from
  `iterations + warmIterations` to `iterations + warmHotTranche`, with a doc comment that explains
  *why* the expected count dropped (the bulk is deliberately no longer expected to touch `l0_ingest`)
  rather than just changing the number — which is what stops a future reader from "fixing" it back.
- Still exact equality in both directions; `TestCheckDeliveryIntegrity` was updated to
  `warmHotTranche + 2000` and keeps its one-short/one-over/warm-ran-but-short cases.
- Empirically live: both my runs produced a Report, i.e. `l0_ingest.N` equalled `64 + iterations`
  exactly (124 and 94). The guard is on the real path and correctly recalibrated.

### (3) `buildNotes` discloses B-A's population composition — MET

`measure.go:124-133` emits, whenever warm-up ran, a note naming the gated `n`, the tranche count,
that those are **in-process Sends stamped at Send time rather than at a spawned process's `main()`
entry**, the real hook-spawn count, and a pointer to `B-A_spawn_estimate` as the purely-spawn-derived
diagnostic. That is exactly the minimum I asked for in N-1, phrased more precisely than I asked for
it. The B-B note was also correctly updated to say `64` rather than `2000` and to state that the
other 1936 are `admin.ping` that never reach `l0_ingest` — the two notes are now mutually consistent,
which they would not have been if only one had been touched. Both notes ship inside the CI artifact,
so a downloaded json is self-explaining. `buildNotes`'s own signature gained `iterations` for this;
threaded from `main.go:328`.

### (4) Concern 2 — adjudicated below (Minor R2-3)

### (5) New defects — none Critical or Important. Three Minors.

## Adjudication — the implementer's concern 2 (fixed tranche at very low `--iterations`)

**Short answer: the disclosure suffices for correctness; a guard has real value but the guard the
concern proposes would target the wrong mechanism. Not a blocker.**

I ran the case rather than reasoning about it. `--iterations 30 --warm-daemon`:

```
  B-A  n=94  p50=1.024ms p95=3.072ms p99=15.360ms p999=15.360ms max=15.000ms limit=15.000ms [FAIL]
  B-B  n=94  p50=0.002ms p95=0.576ms p99= 2.304ms p999= 2.304ms max= 2.117ms limit= 2.000ms [FAIL]
  note: daemon-observed hook_controlled_observed p99=14.336ms n=94
```

Two findings, and both cut against the concern as framed:

1. **The tranche does not dilute the gate downward at low `iterations` — if anything it pushes it
   up.** `warmDaemon` runs the hot tranche *first*, before the 1936 pings, so those 64 requests are
   the very first traffic a freshly-started daemon sees and they absorb the entire cold start (first
   WAL append, first fsyncs, JIT, allocation) while carrying 4 KB–256 KB payloads. They land in the
   **upper** tail, not the lower one. So the residual bias at small `n` is **conservative — a
   spurious FAIL, never a spurious PASS.** For a hard gate that is the safe direction, and it means
   the concern's worry (a fixed tranche "dominating" and thereby flattering the number) is not the
   failure mode that actually exists.
2. **The mechanism that bit here is small-`n` nearest-rank collapse, not the tranche ratio.** At
   `n = 94`, `ceil(0.99 × 94) = 94` — p99 *is* the maximum, so the gate reads a single worst sample.
   The same collapse happens at `--iterations 30` with no warm-up at all (`ceil(0.99 × 30) = 30`).
   Scaling the tranche with `iterations`, or warning on `iterations < N × tranche`, would not have
   prevented this run's red; only a larger `n` would.

So: **scaling the tranche is not warranted** (it would add a moving part to the one number the gate
reads, to fix a problem it does not cause, and would break the exact-rank property proved in item (1)
at `k = 2000/5000`). **A warning has modest value, but keyed to the right thing**: warn when the
gated percentile's rank falls within the top handful of samples — i.e. when `n` is too small for p99
to mean anything — so a developer's smoke run reads "p99 == max at this n; this FAIL may be a
small-sample artifact" instead of an unqualified red. That is a two-line, purely-diagnostic addition
and I record it as Minor R2-3 rather than requiring it, because:

- every configuration the spec, CI and nightly actually invoke (`2000`, `5000`) is well-conditioned:
  the p99 rank sits 20 and 50 samples below the maximum respectively, and the measured margin is
  3.072 ms against 15 ms;
- the failure direction is conservative;
- the composition note already prints the exact split (`n=94 … 64 … 30`) in the very output where it
  matters most, so nothing is hidden from the person reading a smoke run's red.

## Minor findings

**R2-1 — the brief's binding warm-up ruling is now materially unmet, and should be ledgered as an
amendment rather than left implicit.** `test/bench/hotpath/measure.go:37-79`.
task-7-brief.md's binding rulings say *"warm-up = 2000 observe.tool requests over a persistent
connection totaling ~40MB deterministic payloads"*, and the brief outranks the spec. After this
round the warm-up sends **64 observe.tool requests totalling ~1.1 MB** (my own run's own line) — 3.2 %
of the request count and 2.7 % of the payload volume the brief mandates — with the rest as
`admin.ping`, which touches neither the WAL, the ingest path, the session registry nor the ring that
spec step 3 names as the whole point of warming. Controller ruling #29 amended step 6 (the B-A
derivation); it did not amend step 3, and this round amends step 3 in substance.

I am **not** asking for it to be reverted: the trade is right (an unfilterable histogram means any
hot-path warm-up pollutes the gated series, so minimising the tranche is the only lever available),
the coordinator's round-2 message shows the controller sanctioned the restructure, and the practical
warming deficit is small — at CI settings 2044 hot-path requests precede the p99 rank, so the daemon
is thoroughly warm by the time the gated sample is drawn, and any residual under-warming lands in the
upper tail (conservative). But the warm-up ruling is a *binding brief ruling*, and a second amendment
to a binding ruling belongs in the controller's ledger explicitly, next to #29 — not inferred from a
code comment.

**R2-2 — the composition note asserts a dominance property that its own printed numbers can
contradict.** `test/bench/hotpath/measure.go:130`.
The note ends *"…the warm-up hot-path tranche is a small, fixed constant (warmHotTranche) chosen so
the gated population stays hook-spawn-dominated"* — stated unconditionally, in the same sentence that
prints `n=94 … 64 in-process warm-up … 30 real hook spawns`, where the tranche is **68 %** of the
population. At the configurations that matter the claim is true (96.9 % / 98.7 %); at a smoke run it
is visibly false, and a note whose assertion is refuted by its own adjacent numbers costs more
credibility than the sentence buys. Make the clause proportional (print the percentage, which the
function already has every input for) or conditional. One-line change; everything else about this
note is exactly right.

**R2-3 — no signal when `n` is too small for the gated percentile to be meaningful.**
`test/bench/hotpath/report.go:161-173` (`buildBudgetRowFromSnapshot`) / `main.go:334-360`
(`printSummary`). See the concern-2 adjudication above for the evidence and the suggested shape.
Recommendation, not a requirement.

## Observations (no action required, carried forward)

- **`p999` can exceed `max` in the snapshot-sourced rows** — run A shows B-A `p999 = 20.480 ms`
  against `max = 19.000 ms`, and run B shows B-B `p999 = 2.304 ms` against `max = 2.117 ms`. This is
  correct and pre-existing: `internal/obs/hist.go` tracks `Max` exactly but reports percentiles as
  bucket **upper** bounds, deliberately, *"so a latency gate can never pass because of rounding"*.
  Worth knowing before someone files it as a bug against this harness.
- The 1 ms quantization of the gated series (whole-millisecond wire timestamps plus a flat 1 ms
  `hotPathTailAllowance`) is unchanged from round 1 and remains a property of the series ruling #29
  selected, not of this harness.

## Coordinator's concern 1 — agreed, unchanged and still open

The gate still has **never been observed green on real CI**, and the round-2 warm-up shape has now
never run on any CI runner either. `continue-on-error` is removed. **Do not mark `bench-gate` a
required check in branch protection until the first three-platform CI run is green.** Unchanged from
the original I-1 escalation. (Also unchanged: leave `replay-gate`'s own `continue-on-error` alone —
SP-02 owns it.)

## Re-review verdict (round 2)

**Approved (0 Critical, 0 Important, 3 Minor).**

| Item | Status |
|---|---|
| (1) warm-up restructure, named `warmHotTranche`, gated histogram hook-dominated | **MET** — and exactly hook-p99 at both shipped configurations, proved by rank arithmetic and confirmed by run |
| (2) delivery-integrity assert recalibrated to the new exact counts | **MET** — exact both directions, live on the real path, test updated |
| (3) `buildNotes` discloses B-A's population composition | **MET** — more precise than asked; B-B note kept consistent |
| (4) concern 2 (fixed tranche at low `--iterations`) | **adjudicated: disclosure suffices; scaling not warranted; a small-`n` warning recommended (R2-3)** |
| (5) new defects | **none Critical/Important**; 3 Minor (R2-1 brief-ruling amendment to ledger, R2-2 note overclaims, R2-3 small-`n` signal) |
| commit: single, trailer-free, byte-identical message, surgical blast radius | **MET** |

N-1 is closed. The three Minors are cleanup and ledger hygiene; none of them affects the honesty or
the value of the gated number at any configuration this branch actually ships. The one thing that
must not be dropped on the way out is concern 1: the gate is hard, and it is still unproven on CI.

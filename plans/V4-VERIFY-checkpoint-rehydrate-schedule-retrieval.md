# V4 — Verification checkpoint after wave 3 (checkpointer, rehydrator, scheduler, retrieval)

> **Recommended model: Opus 5 · max effort**
>
> The core-loop gate: thirteen merged subplans, the O5 amortization claim tested directly, and real cross-component failure diagnosis across four brand-new subsystems. Run Opus 5's bug-finding at `max` rather than escalating to a second model tier.

**This file is a standalone prompt.** Read it end to end before running anything. You do not need any
other plan file to execute it, though `plans/00-ARCHITECTURE.md` and `Qompack.md` are the normative
references if a command below disagrees with the code.

---

## 0. When this runs, and where

**Trigger.** This checkpoint runs on `develop` immediately after **all four** wave-3 branches have
merged, each with `--no-ff`, in exactly this order (`00-ARCHITECTURE.md` §9 and §14):

1. `feat/sp10-checkpointer-l4` → `develop`
2. `feat/sp11-rehydrator-l5` → `develop`
3. `feat/sp12-scheduler-l3` → `develop`
4. `feat/sp13-mcp-retrieval-layer` → `develop`

**Why that order, and why it is not negotiable.** SP-10 is first because it is the only producer of
`core.DecisionID` (§5.14: *"It is the ONLY producer of `core.DecisionID`, and therefore the only
thing that makes the `why(decision_id)` MCP tool answerable"*) and the only writer of the checkpoint
artifact everything downstream reads. SP-11 is second because `rehydrate.Build` takes a
`checkpoint.Checkpoint` and a `checkpoint.Ref` as inputs and produces `rehydrate.Reporter`, the
`DropReporter` implementation SP-13's `dropped` tool is constructed against. SP-12 is third because it
drives `checkpoint.Writer.Advance` from the idle worker (O5) and consumes `dag.CrossingEdges` for
p-selection — it needs SP-10's writer to exist but nothing needs it. SP-13 merges last because two of
its eight tools are built on the branches above it: `why` needs SP-10's `checkpoint.Reader` and
`ExtractDecisions`, and `dropped` needs SP-11's `DropReporter` (§14: *"MCP is wave 3, not wave 2 …
`why` needs SP-10's `checkpoint.Reader`, and `dropped` needs SP-11's `DropReporter`"*).

Confirm the wave landed in that order before doing anything else:

```
git checkout develop && git pull --ff-only
git log --oneline --merges -8
# Expect four --no-ff merge commits, one per branch above, in that order.
git log --format=%B origin/main..develop | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'
# Expect: no output.  (PowerShell: ... | Select-String -Pattern 'co-authored-by|signed-off-by|generated with')
```

**Branch.** All work happens on `verify/v4`, cut from the post-merge `develop`:

```
git checkout develop && git pull --ff-only
git checkout -b verify/v4
go run ./tools/devtool ci-local     # baseline; record the result before changing anything
```

Every fix lands on `verify/v4` as small conventional commits. When the checkpoint is fully green,
`verify/v4` merges back into `develop` with `--no-ff` and `develop` is tagged `v0.3.0`.

**Gate.** §9: *"The next wave's branches are cut from the post-verification `develop`. No wave-N
branch is ever cut before verification V<N> is green."* Therefore **no wave-4 branch — not
`feat/sp14-slash-commands-and-observability`, not `feat/sp15-analyzer-selection-and-grammar`, not
`feat/sp16-phase7-refinements` — may be created until every row of §8 reads PASS.**

**What this checkpoint is.** It is **not** a smoke test. It is an exhaustive re-verification of *every
functionality that exists in the codebase at this point*: SP-01 through SP-09 (waves 0–2, verified at
V1, V2 and V3) **and** SP-10, SP-11, SP-12, SP-13 (wave 3, new). Wave 3 is the first wave in which the
plugin *acts* rather than merely records, and four classes of defect can only surface here:

- **The full compaction loop closes for the first time.** Until now `PreCompact` wrote nothing and
  `SessionStart(source=compact)` emitted nothing — `TestOnSessionStart_CompactWithoutRehydratorIsEmpty`
  asserted precisely that. At V4 that branch has a real rehydrator behind it and a real checkpoint in
  front of it. The `PreCompact → checkpoint → SessionStart(compact) → additionalContext` round trip
  has never once been executed end to end.
- **Every contract assertion gains a producer.** §12.1 lists four assertions whose producers arrive in
  wave 3: `precompact.has_time_to_write` and `precompact.custom_instructions_accepted` (SP-10),
  `hook.additional_context_delivered` (SP-11), `mcp.server_registered` (SP-13). At V1–V3 those four
  reported `OK:true, SevInfo, Observed:"not-yet-implemented"` and could not degrade a session. At V4
  all nine are **live observations**. `TestDeclaredProducerSetMatchesArchitecture` must therefore
  report **nine declared / zero not-yet-implemented** — the exact inverse of the five/four split V2 and
  V3 pinned.
- **B-E becomes a real measurement.** V2 and V3 both recorded B-E honestly annotated as *"exercises the
  client + daemon + nil-`Services.PreCompact` path only"*. At V4 `Services.PreCompact` is SP-10's
  `Compactor` and B-E measures what §11.3 names: checkpoint finalization. **B-F is in force for the
  first time at all.**
- **Rule W-2 fixture reconciliation, in three directions.** SP-12 developed its `checkpoint.Writer`
  call sites against `testdata/golden/contracts/checkpoint/`; SP-13 developed `why` and `dropped`
  against a `fakeReader` and a `fakeReporter` over the same fixtures; SP-11 read
  `full.json`/`minimal.json`/`empty.json` for its budget arithmetic. All three are now re-run against
  SP-10's and SP-11's real implementations. §5.22 Rule W-2: *"Any fixture that the real implementation
  cannot reproduce is a verification failure, not a fixture bug."*

**What this checkpoint must NOT test.** Nothing from SP-14 (slash commands), SP-15 (analyzer and
grammar), SP-16 (Phase-7 refinements), SP-17 (packaging) or SP-18 (documentation and UAT).
`internal/analyzer`, `internal/grammar` and `internal/commands` are still SP-01 stubs. Their
*stub-ness* is verified (V4-SP01-10, V4-SP01-12); their behaviour is not, and no test authored here may
assume it. Four consequences, so nobody "helpfully" over-tests:

- `checkpoint.SourceSet.Grammar` is a `grammar.Sequitur` and `grammar` is **SP-15**. Assert that
  `Advance` and `Finalize` tolerate a stub; assert nothing about grammar-compressed action history.
- `analyzer.NewSelector` is still a stub, and the closing-note-3 guards keep the shape they already
  have. State what is true rather than what sounds true: `test/guards` constructs no
  `scheduler.Runtime`, so `scheduler.PSelectionAvailable()` is still **`false`** in that binary,
  `TestGuard_SelectorRefusesWithoutPSelection` still asserts the **ship-order** refusal
  (`core.ErrNotImplemented` out of the constructor), and `TestGuard_SubmodularInertWithoutPSelection`
  still asserts the **invariant-4** refusal (`core.ErrBudget`), which never depended on the gate at
  all. What changes at V4 is elsewhere: a process that *has* constructed a `Runtime` — the daemon, or
  a test that opens the gate — now gets a working `stubSelector` back from `NewSelector`, so
  inertness has moved out of the constructor into `stubSelector.Select` returning
  `core.ErrNotImplemented` and into the config default `runtime.selection.submodularEnabled: false`
  (`TestGuard_SubmodularDefaultsOff`). Record `PSelectionAvailable()` on both sides and which refusal
  each guard asserted. There is no "the analyzer does not exist" refusal path in
  `internal/analyzer/selector.go` — the constructor has exactly two guards — and no row may record one.
- `internal/commands` is SP-14; `/qompack:status` does not exist. Every observability claim below is
  asserted against `.qompack/state/*.json`, `.qompack/metrics/latency.json` and the bench artifact.
- No `N/A` remains in the §2.4 budget table. Every budget the architecture names is measurable at V4.

**Phases closed at this point.** §10 Phase 0 (SP-02), Phase 1 (SP-03/04/05/06/08), Phase 2 (SP-09,
surfaced by SP-13), **Phase 3** (SP-10 producing half, SP-11 consuming half) and **Phase 4** (SP-12).
Phases 5, 6 and 7 are not started. **Tag** `v0.3.0` on success.

---

## 1. Ground rules, and how to execute this checkpoint

1. **Run everything from the repository root** `C:/Users/Quant/Documents/Programming/Projects/qompack`
   on the Windows dev machine unless a row says otherwise. The three-OS matrix rows are satisfied by
   CI, and the CI run on `verify/v4` is part of the checkpoint, not an optional extra.
2. **PowerShell caveats.** `wc -l` does not exist: for a commit count use `git rev-list --count`, and
   for a **file's line count** — V4-SP01-04 is the one row that needs it — use
   `(Get-Content <path> | Measure-Object -Line).Lines`. Where a row shows `grep`, use `Select-String`.
   Where a row shows `$TMP`, use `$env:TEMP`.
3. **Never regenerate a golden to make a test pass** (Rule W-2). `-update`, `-write-report`,
   `--write-baseline` and `--regen-corpus` are forbidden except where §7 authorises a reconciliation.
4. **Record every number.** Every row producing a measurement — latency, token count, ratio, coverage,
   allocations, divergence turn — feeds the §8 report. "Passed" without the number is not a pass.
5. **`Qompack.md` is immutable.** `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md`
   is empty at the start and at the end.
6. **A budget whose producer does not exist is `N/A`, never `PASS`.** At V4 no such budget remains.
7. **Fan out, then converge.** §4, §5 and §6 are **main-session only**.
8. **Never write `devtool replay --` (or `./test/replay --`) with a bare separator.** The `--` stops
   Go's `flag` parsing, so every flag after it used to be discarded — the command replayed the default
   corpus with no phase check and exited 0, the loudest silent pass V2 found
   (`plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.2a ①, §3.1 item 3).
   The driver now refuses the leftovers
   with **exit 2** (`TestReplayDriver_LeftoverArgumentsAreBadInput`), so today the same line fails a row
   that expects exit 0. Two related spellings go with it: the report flag is **`--out`**, never
   `--json`; and `--write-baseline` writes to whatever **`--baseline`** names, there being no `--to`.
   The driver's flag set is exactly `corpus baseline policies out signoff growth sketch phase
   regen-corpus write-baseline max-cpu max-wall ci` (`test/replay/main.go:144-165`). Anything else —
   `--json`, `--filter`, `--set` — is `flag provided but not defined` and **exit 2 with nothing
   measured**; there is no corpus-subset flag and no config-override flag, so a run that must vary a
   config key varies it in `.qompack/config.json` between two full replays.
   The standing sweep V2-VERIFY §3.1 opened applies here too: **any `devtool replay --` anywhere under
   `plans/` or `.github/` is the same bug**, and finding one is a finding, not a formatting nit.

**One-line preflight** (if it fails, stop and fix before proceeding):

```
go build ./... && go vet ./... && go run ./tools/devtool fmt-check
```

### 1.1 Subagent partition

Fan the §2 inventory out across **thirteen parallel subagents, one per subplan group**, then run
§3–§7 in the main session.

| Subagent | Owns | Returns (beyond pass/fail per row) |
|---|---|---|
| **V4-A** | §2.1 SP-01 (`V4-SP01-*`) | the exact text of any lint/guard failure, the `nomagic` and `importgraph` output, **which reason the ship-order guard is asserting** |
| **V4-B** | §2.2 SP-02 (`V4-SP02-*`) | `policies.*.fraction_of_opt` for every registered policy, E-1…E-5, the two `--write-baseline` byte comparison, whether corpus staleness tripped |
| **V4-C** | §2.3 SP-03 (`V4-SP03-*`) | `BenchmarkL0SketchUpdate` ns/op + allocs, empirical Bloom FP rate, golden reproduction results |
| **V4-D** | §2.4 SP-04 (`V4-SP04-*`) | the boundary-stability histogram, `testrunner` gain, the eleven micro-benchmark numbers |
| **V4-E** | §2.5 SP-05 (`V4-SP05-*`) | the 66-row fault table, **the contract producer split (must be 9/0)**, breach-detector transitions |
| **V4-F** | §2.6 SP-06 (`V4-SP06-*`) | `Stats.DedupRatio`, the thirteen store/token benchmark numbers, GC deadline accuracy |
| **V4-G** | §2.7 SP-07 (`V4-SP07-*`) | both slice benchmark numbers, `CrossingEdges` µs, thin-vs-full `size_ratio`/`recall` |
| **V4-H** | §2.8 SP-08 (`V4-SP08-*`) | both Phase-1 dedup ratios, the canonicalization gap factor, `Tombstone`/`OnToolUse` numbers |
| **V4-I** | §2.9 SP-09 (`V4-SP09-*`) | the Phase-2 report JSON, `Health` at 3 000 and 8 000 records, the seven negknow benchmarks |
| **V4-J** | §2.10 SP-10 (`V4-SP10-*`) | the observed top-level key order, the five checkpoint benchmark means, the residual-span on/off numbers |
| **V4-K** | §2.11 SP-11 (`V4-SP11-*`) | `Result.Tokens` at budgets 8 000/10 000/12 000, `L5-BUILD`/`L5-RULES`/`L5-SKILLS` p99, golden results |
| **V4-L** | §2.12 SP-12 (`V4-SP12-*`) | all eight scheduler benchmark numbers, the Phase-4 JSON artifacts, `PSelectionAvailable()` both sides |
| **V4-M** | §2.13 SP-13 (`V4-SP13-*`) | B-F p50/p95/p99/max, the eight tool names in order, golden reproduction results |

**Rules for subagents** — the same rules the subplans used. (1) A subagent runs **read-only
verification commands** and reports results; it fixes nothing unless the main session hands it a
diagnosed defect, and even then it returns a diff, never a commit. (2) No subagent may use `-update`,
`-write-report`, `--write-baseline` or `--regen-corpus` — reconciling a contract is the main session's
decision (§7.1 class (c)). (3) No subagent may edit `Qompack.md` or `plans/00-ARCHITECTURE.md`, add
`//nolint`, `t.Skip` or `//nomagic:allow`, or lower a threshold. (4) A subagent that finds a failure
returns the exact command, the full output, the two packages on either side of the seam, and its
classification under §7.1 step 4. (5) Give each subagent this file, `00-ARCHITECTURE.md`
§3.2/§5/§6/§7/§12, and its own subplan file.

**Main session only, never delegated:** the preflight and the branch cut; **every integration test in
§4** (new code spanning five to eight packages, permanent suite members, and exactly where a
subagent's local view produces a wrong assertion); the performance-budget sweep of §5 including
`bench-hotpath` and `benchstat` (one machine, one configuration, one set of numbers — delegated
benchmarks are not comparable); the replay gate and the 2 % rule (§6.3); every commit; the completion
report and the merge.

---

## 2. Cumulative functionality inventory

Every functionality that exists on `develop` at this point, grouped by the subplan that delivered
it. Each row is a **checklist item with an exact command and an exact expected result**. Run every
row. "Passes" means the command exits 0 *and* the stated observable holds.

For **SP-01 … SP-09** the inventories of V1, V2 and V3 are incorporated by reference and re-run in
full (§6.1), and each group below carries only the **delta** — the rows whose behaviour, wiring or
meaning changed because wave-3 code now exercises them. For **SP-10 … SP-13** the inventory is fully
explicit, drawn from each subplan's key deliverables, test plan and exit criteria.

Unless stated otherwise, every command runs from the repository root.

### 2.1 SP-01 — foundation, toolchain, contracts (waves 0, re-verified)

**Re-run in full:** `plans/V3-VERIFY-observer-and-negative-knowledge.md` group A (`A1`–`A17`), equivalently `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.1
(`V2-SP01-01` … `V2-SP01-26`). Every row must still pass unmodified.

**Wave-3 delta — the rows whose meaning changed:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP01-01 | Whole lint chain against four new packages and three modified composition roots | `go run ./tools/devtool lint` | exit 0. `importgraph` passes with `checkpoint`, `pins`, `rehydrate`, `rules`, `skills`, `scheduler`, `mcp` real; `bindeps` still shows only stdlib, `github.com/qompack/qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| V4-SP01-02 | `nomagic` D11/§11.6 with the wave-3 literal set | `go run ./tools/lint/nomagic ./...` | exit 0. No `450`, `8000`, `12000`, `20000`, `10000`, `16384`, `300`, `120`, `0.1`, `1.25`, `12.5`, `0.55`, `0.004`, `0.4` outside `internal/config/defaults.go`, `*_test.go`, or an annotated `//nomagic:allow` line — in particular none in `internal/scheduler`, `internal/mcp`, `internal/rehydrate`, `internal/checkpoint` |
| V4-SP01-03 | `internal/cli` after SP-10 and SP-13 edited it | `go test ./internal/cli/...` ; `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` | green; 30/30 fault combinations still exit 0 with parseable `hookio.Output` on stdout, now with a real `hook_checkpoint.go` and a real `cmd_mcp.go` behind them |
| V4-SP01-04 | `cmd/qompack/main.go` still under 150 LOC and dispatch-only after wave 3 | `(Get-Content cmd/qompack/main.go \| Measure-Object -Line).Lines` (bash: `wc -l cmd/qompack/main.go`) ; read the file ; `git diff --stat $(git rev-list --max-parents=0 HEAD) HEAD -- cmd/qompack/main.go` | **< 150** lines (30 today), and the file is still a `cli.Env` construction plus a single `os.Exit(cli.Dispatch(context.Background(), cli.All(), os.Args, env, os.Stdout, os.Stderr))` — a **data-table** dispatch over `cli.All()`, never a `switch os.Args[1]` — with no business logic and no package-level init beyond `var` declarations. The diff shows wave 3 did not touch it: SP-12's and SP-13's daemon wiring belongs in `internal/cli/daemon.go`. **No `TestMainIsDispatchOnly` exists in `test/guards`, and this row must not name one** — `go test -run` on a pattern that matches nothing prints `ok` and exits 0, which is the silent pass this checkpoint exists to prevent |
| V4-SP01-05 | Plugin bundle: **eight MCP tools are now real** | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | exit 0, clean; the job's "assert all 7 commands and 8 MCP tools present" clause now checks against `mcp.ToolNames()` rather than a placeholder |
| V4-SP01-06 | `obs` budget table with **B-F in force** | `go test -run 'TestBudgets_AllSixPresentAndConfigDriven\|TestBudgets_BGCoversTheDegradedSpoolAppend\|TestCheckBudgets_NeverReportsBG\|TestCheckBudgets_NeverReportsUngatedBudgets' ./internal/obs/` ; `go test -run TestV1_ObsBudgetsAreConfigDrivenEndToEnd ./test/guards/` | green; B-F's limit reads `runtime.budgets.mcpToolCallMs` (default 250) and `Gated` is true |
| V4-SP01-07 | Config docs and schema still cannot drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | exit 0, clean |
| V4-SP01-08 | Appendix C still reproduced verbatim after four branches touched config consumers | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green — `Defaults()` minus `runtime` deep-equals `testdata/golden/config/appendix-c.jsonc` |
| V4-SP01-09 | Append-only guard now has real checkpoints and real pins to guard | `go test -run TestAppendOnlyGuard ./internal/paths/` ; `go test -run 'TestPinsAppendOnlyGuard' ./internal/pins/` | all five illegal writes fail; `checkpoints/`, `pins/invariants.jsonl` and `sketches/tried.bloom` all refuse truncation, in-place rewrite and duplicate-seq creation |
| V4-SP01-10 | Conformance suites: **only three packages may still skip** | `go run ./tools/devtool lint --only=stubskips` ; `grep -rn "t.Skip" internal/ test/ \| grep -v "Rule W-1\|Rule W-2\|QOMPACK_TEST_ACL"` | the second command returns nothing. Remaining legal Rule W-1 skips exist **only** in `analyzertest`, `grammartest` and the `commands` suite. A skip anywhere in `checkpointtest`, `rehydratetest`, `rulestest`, `skillstest`, `schedulertest` or `mcptest` is a merge blocker |
| V4-SP01-11 | e2e: all six hooks, now with real handlers behind `checkpoint` and `session-start` | `go test -run 'TestE2E_AllSixHooksExitZero' ./test/e2e/` | green — six exit codes 0, six hook-log lines |
| V4-SP01-12 | Build-order guards, **with their V4 semantics** | `go test ./test/guards/... -v` | `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull`, `TestGuard_NoNetworkImports`, `TestGuard_WriteSetConfinedToQompack` all green. `TestGuard_SubmodularInertWithoutPSelection` and `TestGuard_SelectorRefusesWithoutPSelection` green — **record which condition each is asserting**. The truth at V4: this binary constructs no `scheduler.Runtime`, so `scheduler.PSelectionAvailable()` is still `false` here, the first guard asserts the invariant-4 refusal (`core.ErrBudget`, gate-independent) and the second asserts the ship-order refusal (`core.ErrNotImplemented`). The second guard early-returns instead of asserting once the gate is open, so if the recorded `PSelectionAvailable()` is `true` in this binary the row is a **finding**, not a pass |
| V4-SP01-13 | Six-target cross build with four new packages linked in | `go run ./tools/devtool build-all` | all six `dist/qompack-<os>-<arch>[.exe]` produced |
| V4-SP01-14 | Commit-message policy machinery | `go test -run TestCheckCommitMsg ./tools/devtool/` ; `git log --format=%B origin/main..develop \| grep -Ei 'co-authored-by\|signed-off-by\|generated with\|🤖'` | green; grep returns nothing |

### 2.2 SP-02 — replay harness, Belady OPT, Phase-0 baseline

**Re-run in full:** V3 group B (`B1`–`B13`), equivalently V2 §2.2 (`V2-SP02-01` … `V2-SP02-20`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP02-01 | Whole package still green under race | `go test -race ./internal/eval/...` | exit 0 |
| V4-SP02-02 | **The policy registry now has five members** | `go test -run 'TestPolicyNames\|TestRegisterPolicy' ./internal/eval/ ./test/replay/...` | `PolicyNames()` includes `null`, `oracle`, `stock` plus the wave-3 registrations `qompack-l3` (SP-12, `test/replay/l3policy`) and `qompack-rehydrate` (SP-11, `test/replay/phase3_rehydrate_test.go`). **There is no `stock-restore`**: SP-11's rewrite forbids a second "stock" arm by name, and `stock` (`internal/eval/policy.go`) is the one the Phase-0 number describes. `oracle == 1.0`, `null == 0.0`, every other policy strictly between on all 24 sessions |
| V4-SP02-03 | `Score.ResidualSpan`, `Score.CompactionPauseMS`, `Score.FirstTurnAfterMS`, `Score.RehydrationTokens` are **populated by real producers** | `go test -run 'TestScoreRun_\|TestReport_PercentilesRecomputedNotAveraged\|TestMetricsOf_CoversEveryDirection' ./internal/eval/` | green. These four §11.2 fields existed as zeroes through V3; at V4 SP-10/11/12 fill them. Assert non-zero on every multi-compaction session and record the medians |
| V4-SP02-04 | Latency honesty tags survive the arrival of real producers | inspect the report JSON from V4-SP02-06 | every latency value still tagged `"latency":"modelled"`; `.qompack/eval/phase4-pause.json` carries `"pause_modelled": true`; deterministic replay makes no model call and must never present a wall-clock as measured |
| V4-SP02-05 | Phase-0 baseline still byte-reproducible | `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --baseline $TMP/v4-a.json` then `--baseline $TMP/v4-b.json`; `diff` | byte-identical, and both equal the committed `testdata/baseline/phase0.json`. `git diff --exit-code -- testdata/baseline/` clean. **There is no `--to` flag**: `--write-baseline` writes to whatever `--baseline` names, and a bare filename would land in the repo root |
| V4-SP02-06 | **Replay gate at `--phase 4`** — five phase checks now run | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 4 --growth $TMP/v4-growth.json --max-cpu 3m --ci --out $TMP/v4-phase4.json` | exit 0; `sessions == 24`; `corpusTier == "synthetic"`; phase checks 0, 1, 2, **3** and **4** all run and all pass; `"regressions": []`. **The `--sketch` watch-fors are NOT run here.** Since `b103037` they are ratio metrics judged at 2 % against `testdata/baseline/phase0.json`'s `0.18` / `0.006`, which were themselves recorded from `testdata/golden/eval/growth/health.json`; a live ledger sitting at a different fill is not a regression against that fixture, so feeding a real `$TMP/v4-health.json` into a baselined run fails this row by construction. The absolute §11.4 ceiling is judged separately, with `--baseline ""`, in the row below. **No bare `--` and no `--json`**: the separator makes Go's `flag` stop parsing (the driver now refuses the leftovers with exit 2, `TestReplayDriver_LeftoverArgumentsAreBadInput`), and the report flag is `--out`. `--max-cpu` is the cost bound — the one that used to be spelled `--max-wall 3m` — while `--max-wall` keeps its 15 m liveness default |
| V4-SP02-06b | **The §11.4 bloom ceiling against a live ledger** | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --sketch $TMP/v4-health.json --baseline "" --phase 4 --max-cpu 3m --ci` | exit 0; `EstFPRate < 0.10`; the ceiling sentence absent from stderr. `--baseline ""` is the explicit no-baseline spelling (`TestReplayDriver_ExplicitlyNoBaselineIsOK`), so the live filter is judged against the **absolute** ceiling only, never against the fixture's ratio |
| V4-SP02-07 | Corpus staleness window still satisfied at phase 4 | same run; inspect the gate message | `--phase 4` against `CORPUS.json.regeneratedAfterPhase: 0` is **outside** the `phase ≤ regeneratedAfterPhase + 2` window. Either the corpus was regenerated during wave 3 and `regeneratedAfterPhase` reflects it, or `TestGate_CorpusStaleness` trips — and a trip is a **real finding** requiring corpus regeneration per `docs/adr/0003-replay-overfit-recollection.md`, not a gate to bypass. Record which |
| V4-SP02-08 | E-1…E-5 budgets with the corpus now driving four extra policies | `go test -bench 'BenchmarkBeladyDetail_400Turns\|BenchmarkSynthesize_320Turns\|BenchmarkCompare_400Actions\|BenchmarkBreakpointOPT_256Candidates' -run '^$' ./internal/eval/` | E-2 ≤ 250 ms/op, E-3 ≤ 50 ms/op, E-4 ≤ 20 ms/op, E-5 ≤ 15 ms/op; E-1 (full driver wall) < 180 s at `--phase 4` |
| V4-SP02-09 | `internal/eval` import purity is unchanged by wave 3 | `go test -run TestImportGraph_EvalIsFoundationOnly ./internal/eval/ -v` | `internal/eval` imports no `internal/` package outside `{core, paths, config, logging, obs}`. **`-run TestImports` matches nothing in this package** and prints `ok` — the vacuous-pass shape V2-VERIFY §3.1 opened a standing sweep for; the real test's name is spelled in full above. The wave-3 policies live in `test/replay/**`, which is a composition root |
| V4-SP02-10 | Coverage floor | `go run ./tools/devtool cover` | `internal/eval` ≥ **85 %** |

### 2.3 SP-03 — sketch library

**Re-run in full:** V3 group C (`C1`–`C9`), equivalently V2 §2.3 (`V2-SP03-01` … `V2-SP03-20`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP03-01 | Whole package green, race, twice | `go test -race -count=2 ./internal/sketch/...` | exit 0 |
| V4-SP03-02 | Frozen on-disk formats survive four more branches | `go test -run TestGolden ./internal/sketch/` (**no** `-update`) | the five `testdata/golden/contracts/sketch/*.v1.bin` reproduce byte-for-byte; `MANIFEST.json` hashes match |
| V4-SP03-03 | `RebuildBloom` is now driven by a **scheduler idle task**, not only by tests | `go test -run 'TestBloom_Rebuild' ./internal/sketch/` ; `go test -run TestIdleTaskRebuildBloomSkippedWhenLedgerNil ./internal/daemon/` ; `go test -bench BenchmarkRebuildBloom5000 ./internal/sketch/` | equivalent filter reconstructed; ≤ 15 ms for 5 000 keys; the `rebuild_bloom` idle task calls it through `negknow.RebuildBloom` and never directly |
| V4-SP03-04 | `tried.bloom` generational replacement holds with the scheduler writing on an idle tick | `go test -run 'TestSave_RefusesTriedBloom\|TestReplaceGenerational_\|TestAppendOnly_TriedBloomNeverTruncated' ./internal/sketch/` | `Save` on `tried.bloom` ⇒ `ErrGenerational`; exactly one `.bak` generation kept; rollback restores the prior generation |
| V4-SP03-05 | Bloom FP ceiling now surfaces through the MCP `already_tried` path | `go test -run 'TestBloom_EstimatedFPRateMatchesEmpirical\|TestBloom_SaturatedThreshold' ./internal/sketch/ -v` | empirical FP ∈ [0.008, 0.013] at capacity; `Saturated()` fires at `EstimatedFPRate ≥ 0.10` |
| V4-SP03-06 | **L0 sketch-update budget unchanged by wave 3** | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` ; `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` | **≤ 5 µs/op and 0 allocs/op** |
| V4-SP03-07 | Coverage floor | `go run ./tools/devtool cover` | `internal/sketch` ≥ **90 %** |

### 2.4 SP-04 — chunking, canonicalization, symbols

**Re-run in full:** V3 group D (`D1`–`D7`), equivalently V2 §2.4 (`V2-SP04-01` … `V2-SP04-27`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP04-01 | Three packages green under race | `go test -race ./internal/chunk ./internal/canon ./internal/symbols ./test/dedup` | exit 0 |
| V4-SP04-02 | **Chunk boundaries now define the MCP minimal span** | `go test -run 'TestSplit_GoldenBoundaries\|TestSplit_SizeBounds' ./internal/chunk/` ; `go test -run 'TestMinimalSpanIsChunkAligned\|TestMinimalSpanNeverExceedsChunkMax' ./internal/mcp/` | goldens reproduce byte-for-byte on all three OSes; every `expand`/`re_read` span offset is in the chunk-offset set and `End-Off ≤ 16384` unless one chunk is larger. A change to FastCDC parameters is now a change to the retrieval contract |
| V4-SP04-03 | **`symbols.Enclosing` now backs the MCP widener through the `Widener` port** | `go test -run 'TestEnclosing_' ./internal/symbols/` ; `go test -run 'TestSymbolAnchorSelectsEnclosingFunction\|TestSymbolWideningExtendsToFunctionEnd\|TestNoWidenerIsTolerated' ./internal/mcp/` | smallest containing span wins in both packages; `internal/mcp` still does **not** import `internal/symbols` (the port is what keeps the import graph legal) |
| V4-SP04-04 | Canon normative properties unchanged | `go test -run 'TestEveryCanonicalizer_Idempotent\|TestEveryCanonicalizer_NeverGrows\|TestRestore_RoundTrip\|TestNonGrowingGuard_DropsGrowingMatch' ./internal/canon/` | `Run(Run(x)) == Run(x)`; `len(canonical) ≤ len(input)`; `Restore` is a byte-exact inverse |
| V4-SP04-05 | Dedup measurement harness | `go test -v ./test/dedup/` | `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0`; `testdata/canon-dedup-report.json` reproduced byte-for-byte |
| V4-SP04-06 | SP-04 benchmark budgets | `go test -bench . -benchmem -run '^$' ./internal/chunk ./internal/canon ./internal/symbols` | `Split_100KB` < 800 µs; `GearScan_1MiB` ≥ 400 MB/s; `Run_Bash100KB` < 3 ms; `Extract_100KB` < 2 ms; `Enclosing_100KB` < 2 ms |
| V4-SP04-07 | Coverage floors | `go run ./tools/devtool cover` | `internal/chunk` ≥ **90 %**, `internal/canon` ≥ **90 %**, `internal/symbols` ≥ **75 %** |

### 2.5 SP-05 — daemon, IPC, hot path, contract monitor

**Re-run in full:** V3 group E (`E1`–`E14`), equivalently V2 §2.5 (`V2-SP05-01` … `V2-SP05-29`).

**Wave-3 delta — this is the largest delta in the checkpoint, because wave 3 fills every extension
seam SP-05 shipped:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP05-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/ipc ./internal/daemon ./internal/contract` | exit 0 |
| V4-SP05-02 | **`Services` is no longer mostly nil** | `go test -run 'TestServicesAllNil\|TestHandleOverridesDefaultRoute\|TestBindRunsInOrderAndDeclaresProducers' ./internal/daemon/` | `TestServicesAllNil` still passes with a fully nil `Services` (the nil-tolerance contract does not expire); `TestBindRunsInOrderAndDeclaresProducers` now sees `Rehydrate`, `PreCompact`, `MCPInitialized` and `Sched` bound |
| V4-SP05-03 | **All nine contract assertions have real producers** | `go test -run TestFreshBuildReportsModeFull ./internal/contract/ -v` ; `go test -run TestDeclaredProducerSetMatchesArchitecture ./internal/daemon/ -v` (it lives in `internal/daemon/options_test.go`, **not** in `internal/contract`) ; `go test -run TestV4_EveryContractAssertionHasARealProducer ./test/e2e/` | `ModeFull`; **zero** assertions at `Observed:"not-yet-implemented"`; `DeclareProducers` on a `Services` with `Rehydrate`, `MCPInitialized` and `PreCompact` set registers **all nine** ids. Five-declared/four-gated was the V2/V3 answer and is now a **regression** |
| V4-SP05-04 | `hook.additional_context_delivered` is a live SevCritical check | `go test -run 'TestSentinelRoundTrip\|TestAdditionalContextGatedUntilRehydrator\|TestSentinelNotObservedGivesTwoChances' ./internal/contract/` | with the producer declared the real check runs at `SevCritical`. **The daemon owns the sentinel end to end**: `contract.MintSentinel(sessionID, now)` mints it seeded on the mint timestamp, the `session.start` route appends `contract.RenderSentinel` **outside** the `<!-- /qompack:injected -->` close tag, and `scanSentinelForPrompt` reads it back out of the transcript tail into `History.Sentinel.Observed`. SP-11 ships **no** `rehydrate.Sentinel` and **no** `"sentinel"` key in the rehydrate state file (`plans/V4-SP-11-rehydrator-l5.md` §"The daemon mints it"), so a row that asserts the two sides recompute the same token from `(session, seq)` asserts a symbol that does not exist — and `StripInjections` must not swallow the probe line |
| V4-SP05-05 | `precompact.has_time_to_write` and `precompact.custom_instructions_accepted` read a real observable | `go test -run 'TestPreCompactTimeoutUnknown\|TestCustomInstructionsProbePhrase' ./internal/contract/` ; `cat .qompack/state/precompact.json` after §4.1 | `state/precompact.json` parses with `sentinel == "qompack checkpoint"`, `timeout_ms == 20000`, `wall_ms >= 0`, `frontier`, `span_instruction`; `wall_ms > 0.6 × timeout_ms` warns, timeout fails |
| V4-SP05-06 | `mcp.server_registered` observes a real `initialize` | `go test -run 'TestMCPInitializedSeamFlipsAfterInitialize\|TestInitializedWritesContractHistory' ./internal/daemon/ ./internal/mcp/` ; `go test -run TestE2ESelfTest ./test/e2e/` | `Services.MCPInitialized(ctx)` false before the first `initialize`, true after; **`state/history.json`** — `contract.HistoryPath(root)`, never `state/contract.json`, which stays the Monitor's own mode/reason/results file — carries `mcp_initialized: true`; `qompack self-test --json` exits 0 with `mode == "full"` |
| V4-SP05-07 | **`ipc.OpMCP` is routed** | `go test -run 'TestInstallMCPOpRegistersOp\|TestDaemonMCPOp' ./internal/daemon/` | the op is registered through `Options.Handle`, dispatches tool calls, resolves session and turn from the registry, and degrades cleanly on an empty registry |
| V4-SP05-08 | **The idle controller now runs six scheduler tasks plus the checkpoint cadence** | `go test -run 'TestIdle\|TestIsIdle\|TestIdleTasksRegistered' ./internal/daemon/` | priority order respected; budget respected; task panic isolated; `IsIdle` flips exactly at `idle.detectAfterSeconds`; the registered set is `act.advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc` plus `checkpoint_cadence`, `drain`, `sketches`, `metrics`, `negknow.maintain` |
| V4-SP05-09 | **Degraded-passive suppresses every wave-3 acting path** | `go test -run 'TestDegradedPassive\|TestModeOffSkipsIngest' ./internal/daemon/` ; `go test -run TestV4_DegradedPassiveWithEverySubsystem ./test/e2e/` | in `ModeDegradedPassive`: no `additionalContext`, no `customInstructions`, no scheduler-initiated checkpoint, no drop report — but L0/L1 still record, MCP retrieval tools stay available (§12.1: *"they are pull-based and cannot make anything worse"*), and `advance_frontier` still grows the draft while the cadence refuses to finalize |
| V4-SP05-10 | Hooks exit 0 under every injected fault, with four new subsystems behind them | `go test -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit' ./test/e2e/ -v` | **66/66** combinations exit 0 with valid JSON on stdout; only `self-test` may exit non-zero |
| V4-SP05-11 | `sync` → `spool` breach transition still observable | `go test -run 'TestBreachDetector\|TestHotModeTransition\|TestSpoolOnBreachFalse' ./internal/daemon/` | transition after exactly 3 consecutive 512-sample breach windows; reverts after 3 clean windows; visible in `state.bin`, the NAK frame, the WARN log and `status` |
| V4-SP05-12 | **B-A/B-B/B-D/B-E hot-path harness with the full wave-3 resident set** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v4-hotpath.json` and again `--iterations 200 --warm-daemon` (**no `--hook`** — the harness spawns `qompack checkpoint` itself for B-E; `hookArgs` rejects every value but `observe-tool` with exit 2) | `B-A.pass == true` (p99 < 15 ms), `B-B.pass == true`, **`B-E.pass == true` (p99 < 2 s, now a real `PreCompact`)**; `b_a_method` and `spawn_floor_ms` present; B-D reported, never gated. See §5 |
| V4-SP05-13 | **The scheduler is provably not on the hot path** | `go test -run TestSchedulerNotOnHotPath ./internal/scheduler/ ./internal/daemon/ -v` | call-graph inspection of `internal/cli` finds no hook subcommand path reaching `scheduler.Evaluate`, `schedRuntime.Evaluate` or `WrapServicesForScheduler` |
| V4-SP05-14 | Security posture with four new packages | `go run ./tools/devtool lint` + the CI `security` job | zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` and only `unix`; `os/exec` only in `internal/daemon`, `internal/cli`, `internal/testutil`, `tools/`; `govulncheck` clean |
| V4-SP05-15 | Coverage floors | `go run ./tools/devtool cover` | `internal/ipc`, `internal/daemon`, `internal/contract`, `internal/cli` each ≥ **75 %**. The SP-12- and SP-13-owned files inside `internal/daemon` count toward that floor and must not lower it |

### 2.6 SP-06 — content-addressed store, redaction, exact token accounting

**Re-run in full:** V3 group F (`F1`–`F13`), equivalently V2 §2.6 (`V2-SP06-01` … `V2-SP06-27`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP06-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/store ./internal/redact ./internal/tokens` | exit 0 |
| V4-SP06-02 | **`SegmentLog.MarkEncoded` is now driven by a real `checkpoint.Advance`** | `go test -run TestSegment_ ./internal/store/ -v` ; `go test -run TestAdvanceIsDPIGuarded ./internal/checkpoint/` ; `go test -run TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes ./internal/daemon/` | monotonic ids; idempotent for the same seq; **`core.ErrAlreadyEncoded` on a different seq**, raised through the live L4 path and handled by the L3 frontier advancer; `Frontier` is the contiguous encoded prefix |
| V4-SP06-03 | **`OpenSpan` is now the MCP minimal-span read path** | `go test -run 'TestOpenSpan_Boundaries' ./internal/store/` ; `go test -bench BenchmarkOpenSpan_4KB_of_4MB ./internal/store/` | all six span boundary cases exact; ≤ 150 µs/op. The §8.7 "minimum sufficient span" claim now has a caller |
| V4-SP06-04 | **`PutOptions.Ephemeral` is now written by a real producer** | `go test -run 'TestPutBytes_EphemeralFlagPersists' ./internal/store/` ; `go test -run 'TestEphemeralToolUseRecordWritten\|TestEphemeralRecordCarriesTurn' ./internal/mcp/` | every MCP retrieval result writes a `ToolUseRecord{Ephemeral: true, Tool: "mcp__qompack__<name>"}` that survives reopen |
| V4-SP06-05 | **GC roots now include real checkpoints and real pins** | `go test -run 'TestGC_HarvestsHashesFromCheckpointPinsEliminations\|TestGC_DeadlineTruncatesAndResumes' ./internal/store/ -v` ; plus §4.11's live-session GC assertion | roots harvested from `checkpoints/*.json`, `pins/invariants.json` and `records/eliminations.jsonl` **without importing those packages**; a pointer referenced by any checkpoint is never collected |
| V4-SP06-06 | `ChangedSince` still exactly hash inequality, now called from the scheduler's idle staleness refresh | `go test -run 'TestChangedSince_\|PropChangedSinceIsExactlyHashInequality' ./internal/store/` | key-normalized, input-ordered, unknown paths counted not errored |
| V4-SP06-07 | Phase-1 dedup ratio unchanged by wave 3 | `go test -run 'TestPhase1ExitCriterion_ReadHeavy\|TestStats_DedupRatio' ./internal/store/ -v` ; `go test -run 'TestDedupRatio_WithVsWithout\|TestDedupReport_Written' ./test/dedup/ -v` (the whole-binary arm lives in `test/dedup`, not `test/e2e`) | `DedupRatio ≥ 4.0` at both levels. Record both numbers and compare against the V3 figures |
| V4-SP06-08 | Frozen index formats | `go test -run TestGolden_IndexFormats ./internal/store/` (**no** `-update`) | `roots.jsonl`, `tool_use.jsonl`, `segments.jsonl`, `files.json`, `sessions.jsonl` byte-identical |
| V4-SP06-09 | Store/redact/tokens performance | `go test -bench . -benchmem -run '^$' ./internal/store ./internal/redact ./internal/tokens` | `PutBytes_100KB` cold ≤ 3 ms / warm ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan` ≤ 150 µs; `Search_1000Roots` ≤ 25 ms; `OpenStore_50kRoots` ≤ 400 ms; `MarkEncoded_100` ≤ 1 ms; `GC_50kObjects` ≤ 2 s; `Redact` 100 KB ≤ 2 ms; `EstimateRoot_64Cached` ≤ 5 µs |
| V4-SP06-10 | Coverage floors | `go run ./tools/devtool cover` | `internal/store` ≥ **90 %**, `internal/redact` ≥ **90 %**, `internal/tokens` ≥ **90 %** |

### 2.7 SP-07 — dependence DAG and slicing

**Re-run in full:** V3 group G (`G1`–`G9`), equivalently V2 §2.7 (`V2-SP07-01` … `V2-SP07-20`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP07-01 | Package green under race | `go test -race ./internal/dag/...` | exit 0 |
| V4-SP07-02 | **`CrossingEdges` is now `segment_coupling(p)` inside a live p-selection** | `go test -run 'TestCrossingEdges\|PropCrossingEdgesMatchesBruteForce' ./internal/dag/` ; `go test -run 'TestAssemble_CouplingFromCrossingEdges\|TestAssemble_CrossingEdgesCached\|TestAssemble_CacheInvalidatedOnGraphChange' ./internal/daemon/` ; `go test -run TestCrossingLatencyBudget ./internal/dag/` | matches brute force everywhere; **< 5 µs median on 15 000 edges**; the daemon's cache issues 3 calls on a second assembly and 6 after a graph mutation |
| V4-SP07-03 | **`BackwardSlice` now ranks eliminations (L5) and decisions (L4)** | `go test -run 'TestBackwardSlice\|TestSliceLatencyBudget' ./internal/dag/` ; `go test -run 'TestEliminations_OrderedBySliceScore\|TestEliminations_FileProxyScore' ./internal/rehydrate/` ; `go test -run TestExtractRanksBySliceScore ./internal/checkpoint/` | slices **< 1 ms/op** median of 20 on a 5 000-node graph; the two consumers rank by score and degrade to TS/ID ordering on a slice error |
| V4-SP07-04 | **`KindDecision` nodes are now actually created** | `go test -run TestExtractEmitsDecisionNodesAndEdges ./internal/checkpoint/` ; `go test -run 'TestNodeKind' ./internal/dag/` | `Graph.Node("decision:dec_…")` exists with `Kind == dag.KindDecision`; an `EdgeExplains` edge points from the evidence node to it. Before wave 3 this node kind had no producer |
| V4-SP07-05 | **No selection authority** — still mechanically enforced with a live scheduler | `go test -run TestNoBooleanKeepAPI ./internal/dag/ -v` | no exported function returns `map[NodeID]bool` / `[]bool`; `doc.go` still contains the literal `NO SELECTION AUTHORITY`. The scheduler's `Candidate.ReclaimableTokens` is assembled in `internal/daemon`, never in `dag` |
| V4-SP07-06 | Thin-vs-full measurement, still reproduced | `go test -run TestThinVsFullComparison ./internal/dag/ -v` | mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`; `thin-vs-full.json` matches within 2 % |
| V4-SP07-07 | Import purity is unchanged | `go run ./tools/devtool lint` (`importgraph`) | `internal/dag` imports only `core`, `paths`, `config`, `logging` — **not** `store`, **not** `symbols`, **not** `scheduler`, **not** `checkpoint` |
| V4-SP07-08 | Coverage floor | `go run ./tools/devtool cover` | `internal/dag` ≥ **85 %** |

### 2.8 SP-08 — L0 observer

**Re-run in full:** V3 group H (`H1`–`H16`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP08-01 | Package green under race | `go test -race ./internal/observer/...` | exit 0 |
| V4-SP08-02 | **`OnSessionStart(source=compact)` now delegates to a real rehydrator** | `go test -run 'TestOnSessionStart_CompactDelegates\|TestOnSessionStart_CompactWithoutRehydratorIsEmpty\|TestOnSessionStart_ClearDelegates' ./internal/observer/` ; `go test -run 'TestService_' ./internal/daemon/` | the unit tests still pass against the seam (nil rehydrator ⇒ empty output stays true); the daemon service test proves the live path emits injection-tagged `additionalContext`. **No file under `internal/observer` may have been modified by wave 3** — `git diff --name-only <v0.2.0>..HEAD -- internal/observer/` must be empty except for test files |
| V4-SP08-03 | **`observer.Signals` is now consumed by the scheduler tap** | `go test -run 'TestExtractSignals\|TestExtractTestOutcome' ./internal/observer/` ; `go test -run 'TestWrapServices_BoundarySignalsCloseSegment\|TestWrapServices_ObserveToolDrivesObserve' ./internal/daemon/` | todo completion, passing test run and git commit each close a segment exactly once with cause `todo`/`test`/`commit` — G1.5 is now wired end to end |
| V4-SP08-04 | **Segment lifecycle is now shared between SP-08 and SP-12** | `go test -run 'TestOnSessionStart_StartupOpensSegment\|TestOnSessionEnd_Order' ./internal/observer/` ; `go test -run 'TestRuntime_ObserveClosesSegmentOnChangepoint\|TestFrontier_NoCloseWithoutCurrentSegment' ./internal/daemon/` | SP-08 still opens the first segment at session start and closes the last at session end in the exact order `Segments().Close → Graph.Flush → Store.Flush → sketch.Save ×2 → state write → Store.GC`; SP-12 closes-and-rolls in between and never performs the first open |
| V4-SP08-05 | Verbatim user capture is still the **only** intent source | `go test -run 'TestOnUserPrompt\|TestVerbatimPromptID' ./internal/observer/` ; `go test -run 'TestUserIntent_FromL0NotCheckpoint\|TestUserIntent_EarliestTurnOfThisSessionWins\|TestUserIntent_IgnoresOtherSessions' ./internal/rehydrate/` | prompts stored with `Canon.Strip == nil` and MinHash disabled; L5 reads L0, never a summary; a mismatch emits `DropEntry{Kind:"intent_mismatch"}` and one `Loud` |
| V4-SP08-06 | Tombstones are now expandable through a real MCP tool | `go test -run TestTombstone ./internal/observer/` ; `go test -run TestV4_TombstoneToRecallToExpandRoundTrip ./test/integration/` | `TestTombstone_DesignExample` renders the §8.1 form; §4.5 proves the hash in the marker resolves through `recall`/`expand` |
| V4-SP08-07 | Observer hot-path cost inside B-C, with the scheduler tap added | `go test -bench 'BenchmarkOnToolUse_FileRead64KB\|BenchmarkOnToolUse_TestOutput256KB\|BenchmarkTombstone' -benchmem -run '^$' ./internal/observer/` ; `go test -bench BenchmarkSchedulerTap_ObserveTool ./internal/daemon/` | both `OnToolUse` benchmarks within **B-C p99 < 50 ms**, `FileRead64KB` p50 < 5 ms; `Tombstone` < 2 µs; `SchedulerTap_ObserveTool` ≤ 1.5 ms — and the sum still lands inside B-C, not B-A |
| V4-SP08-08 | Observer conformance suite and e2e | `go test -run Suite ./internal/observer/...` ; `go test -run 'TestE2E_ObserverThroughDaemon\|TestE2E_SupersessionVisibleAfterRestart\|TestE2E_VerbatimPromptSurvivesRestart' ./test/e2e/` | zero `t.Skip`; 44 lines in `index/tool_use.jsonl`; `sketches/tried.bloom` written only by the ledger |
| V4-SP08-09 | Coverage floor | `go run ./tools/devtool cover` | `internal/observer` ≥ **75 %** |

### 2.9 SP-09 — negative knowledge

**Re-run in full:** V3 group I (`I1`–`I14`).

**Wave-3 delta:**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP09-01 | Package green under race | `go test -race ./internal/negknow/...` | exit 0 |
| V4-SP09-02 | **`IngestMCP` is now driven by the real `record_eliminated` tool** | `go test -run 'TestIngestMCP' ./internal/negknow/` ; `go test -run 'TestRecordEliminated' ./internal/mcp/` | evidence stored in the store and resolvable; `depends_on` resolved to the newest roots; unresolved deps reported not dropped; `Source == SourceMCP`; scope defaults from `eliminations.defaultScope`; session-scope without a session is an error |
| V4-SP09-03 | **The three-way answer is now rendered by two consumers** | `go test -run TestQuery ./internal/negknow/` ; `go test -run 'TestAlreadyTried' ./internal/mcp/` ; `go test -run 'TestEliminations_StaleRendersTheVerbatimNote\|TestEliminations_StaleDroppedWhenConfigured' ./internal/rehydrate/` | `absent`/`active`/`stale` everywhere; `Note == negknow.StaleNote` **including the em dash** in the ledger, in the MCP result and in the rehydration payload; `staleResponse:"drop"` yields `absent` at the MCP surface and omission in the payload |
| V4-SP09-04 | **`RefreshStaleness` and `RebuildBloom` are now scheduler idle tasks** | `go test -run 'TestRefreshStaleness\|TestRebuildOnStale\|TestMaintenanceTask_Shape' ./internal/negknow/` ; `go test -run 'TestIdleTaskRebuildBloomSkippedWhenLedgerNil\|TestIdleTaskGatedByDecisionBackground' ./internal/daemon/` | exactly one `ChangedSince` call per refresh; `MaintenanceTask_Shape` returns `("negknow.maintain", 30, fn)`; the `rebuild_bloom` background task is inert unless `Decision.Background` contains it |
| V4-SP09-05 | **Bloom-as-cache, with a checkpoint now on disk** | `go test -run 'TestRebuildBloom_NeverFromCheckpoint\|TestBloomLoadFailure_NoRecords_NeverFalsePositive' ./internal/negknow/ -v` ; `git grep -n "sketch.RebuildBloom" -- internal/ ':!*_test.go' ':!internal/sketch/*'` | grep returns **exactly one** line, in `internal/negknow/bloom.go`; `internal/negknow` imports no `internal/checkpoint`. This assertion matters more at V4 than ever, because a checkpoint's `eliminated[]` array now exists and would be a tempting rebuild source — §13 invariant 3 forbids it |
| V4-SP09-06 | Eliminations reach the checkpoint's tier-1 slot | `go test -run 'TestBeginSeedsTierOneFromSources\|TestEliminatedCarriesEverySection85Key' ./internal/checkpoint/` | `Begin` seeds `Eliminated` from the ledger (both scopes); every entry carries all seven §8.5 keys |
| V4-SP09-07 | Phase-2 exit criterion still holds, now also surfaced through MCP | `go test -run TestPhase2ExitCriterion ./test/replay/ -v` | `stock_repeats > 0`; `negknow_repeats ≤ 0.75 × stock_repeats`; improvement on ≥ 8 individual sessions; `stale_blocks == 0` on every `DependencyChangeAt` session |
| V4-SP09-08 | negknow performance budgets | `go test -bench . -run 'TestBudget_\|TestMemoryFootprint\|TestBloomFileSize' ./internal/negknow/` | `Query` hit p99 < 50 µs, miss < 5 µs; `Record` < 5 ms; `RebuildBloom` (5 000) < 50 ms; `RefreshStaleness` < 10 ms; `Open` (20 000 lines) < 150 ms; `Detector.Scan` < 5 ms; resident memory at 5 000 records < 4 MB |
| V4-SP09-09 | Coverage floor | `go run ./tools/devtool cover` | `internal/negknow` ≥ **90 %** |

### 2.10 SP-10 — L4 checkpointer and pins (**new this wave**) — `internal/checkpoint`, `internal/pins`

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP10-01 | Both packages green, race + repeat | `go test -race -count=2 ./internal/checkpoint/... ./internal/pins/...` | exit 0 |
| V4-SP10-02 | **Append-only pins with tombstone deletion and a materialized view** | `go test ./internal/pins/ -v` | `TestPinsAddAppendsOneLine`, `TestPinsAddIsIdempotent`, `TestPinsMintIDStable`, `TestPinsRemoveWritesTombstone`, `TestPinsRemoveUnknownIsNotFound`, `TestPinsReplaySkipsMalformedLine`, `TestPinsMaterializeView`, `TestPinsAppendOnlyGuard`, `TestPinsEmptyTextRejected`, `TestPinsSourceNormalized` all PASS. `Remove` writes `{"op":"del"}`, never rewrites; `MintID` is `"inv_" + 12 hex`, total length 16 |
| V4-SP10-03 | **The §8.5 schema, verbatim, in importance order** | `go test ./internal/checkpoint/ -run 'TestSchemaFieldOrderIsImportanceOrder\|TestGoldenCheckpointRoundTrip\|TestEmptySlicesSerializeAsArrays\|TestBlockedOnNullIsExplicit\|TestHashMarshalsAsSha256Prefix\|TestEliminatedCarriesEverySection85Key' -v` | top-level key order scanned with `json.Decoder.Token()` equals exactly `[version session seq created parent encoded_segments invariants user_intent eliminated decisions open_questions current_work pointers narrative sketch_refs dropped cache]`; goldens 0001–0004 round-trip byte-identically; empty slices marshal as `[]`, the only `null` is `"blocked_on"`; hashes marshal as `"sha256:<64 hex>"`; every `eliminated[]` entry carries all seven §8.5 keys |
| V4-SP10-04 | Versioning and migration | `go test ./internal/checkpoint/ -run 'TestMigrate\|TestUnmarshalDropsUnknownFields' -v` | `TestMigrateV1IsIdentity`; `TestMigrateRejectsFutureVersion` ⇒ `core.ErrContract` with `"newer plugin"`; `TestMigrateRejectsMissingVersion` ⇒ `core.ErrContract`; unknown fields warn, never error |
| V4-SP10-05 | **No code snippets in checkpoints** (G3.4, §13 invariant 5) | `go test ./internal/checkpoint/ -run TestGoldenCheckpointsContainNoCodeBlocks -v` | PASS over every file in `testdata/golden/checkpoints/`: no triple-backtick fence in the raw bytes; no decoded string matching `(?m)^\s*(func \|class \|def \|import \|package \|const \|return \|if \(\|\}\s*$)`; no newline inside any `pointers.files[].why` or `pointers.tools[].summary` |
| V4-SP10-06 | **Store-only regeneration — the GC rule** (§8.5, §13 invariant 1) | `go test ./internal/checkpoint/ -run 'TestSourceSetCarriesNoText\|TestAdvanceStripsInjectionsFromStoredPrompts\|TestStripInjections' -rapid.checks=1000 -v` | `TestSourceSetCarriesNoText`: reflect over `SourceSet` shows every field is `reflect.Interface` — the type system makes "checkpoint from a summary" uncompilable. `TestAdvanceStripsInjectionsFromStoredPrompts`: no evolution entry contains `"stale summary"` or `"qompack:injected"`. `TestStripInjectionsPairedBlock`, `_UnmatchedOpen`, `_MangledResidue`, `_LeavesCleanTextAlone` all PASS |
| V4-SP10-07 | Incremental draft: begin, resume, discard, advance, abort | `go test ./internal/checkpoint/ -run 'TestBegin\|TestAdvance\|TestAbort\|TestSetCurrentWork\|TestPackageFunctionsWorkWithoutObservers' -v` | `TestBeginSeedsTierOneFromSources`, `_InheritsOriginalIntentFromParent`, `_ResumesPersistedDraft`, `_DiscardsDraftWithClaimedSeq`; `TestAdvanceEncodesClosedSegmentsOnly`, `_ExcludesSupersededAndEphemeralTools`, `_PointersCarryNoContent` (a 40 KB read yields a draft < 4 KB with the content substring absent), `_IsIdempotentForSameSeq`, `_KeepsPointersNewestFirst`, `_DerivesOpenQuestionsFromStaleEliminations`, `_DerivesCurrentWorkGoalFromLatestPrompt`; `TestAbortLeavesEncodedMarksIntact`; `TestSetCurrentWorkSuppressesDerivation`; package functions work with observers unset |
| V4-SP10-08 | **The DPI guard at L4** (§4.6, §8.2) | `go test ./internal/checkpoint/ -run TestAdvanceIsDPIGuarded -v` | `errors.Is(err, core.ErrAlreadyEncoded)` when a segment already `MarkEncoded` at seq 5 is advanced into a seq-6 draft; the draft is still persisted |
| V4-SP10-09 | **`ExtractDecisions` — the only producer of `core.DecisionID`** | `go test ./internal/checkpoint/ -run 'TestExtract\|TestDecisionIDIsStableAndDeterministic' -v` ; `grep -rn "DecisionID(" internal/ \| grep -v checkpoint/decisions.go` | `TestExtractFromEdgeExplains`, `_FromElimination`, `_FromDecisionPin`, `_DeduplicatesByID`, `_RanksBySliceScore`, `_EmitsDecisionNodesAndEdges`, `_CapsAtSixtyFour`, `_RespectsFromTurn`, `_SurvivesStoreReadFailure` all PASS; `MintDecisionID` is stable, `dec_`-prefixed, length 16; the grep shows only consumers |
| V4-SP10-10 | **Importance-ordered truncation** (§6.9) | `go test ./internal/checkpoint/ -run 'TestTruncate' -rapid.checks=300 -v` | `TestTruncateDropsTierThreeFirst`, `_DropsToolPointersBeforeFilePointers`, `_DropsPointersTailFirst`, `_ReachesTierTwoOnlyAfterTierThree` (result equals golden `0003-truncated.json`), `_EmptiesAlternativesBeforeDroppingDecisions`, `_NeverTouchesTierOne` (budget 10 ⇒ tier 1 byte-identical, one `DropEntry{Kind:"budget_exceeded"}`, **no error**), `_HonoursConfiguredNeverList`, `_ZeroBudgetIsUnlimited`, `_IsMonotone` (property), `_DropEntriesAreComplete` |
| V4-SP10-11 | **Pointer ground-truth validation** (G2.5) | `go test ./internal/checkpoint/ -run 'TestValidatePointers\|TestGitIndex\|TestGitDirAsFileWorktree' -v` ; `go test -run=XXX -fuzz FuzzParseGitIndex -fuzztime 60s ./internal/checkpoint/` | `pointer_missing`, `pointer_invalid` (directory, escape), `pointer_dirty` (with the branch name in `Detail`), `pointer_untracked`, clean file ⇒ no drop; git index v2 and v3 (extended flags) parsed, v4 and truncated degrade with `pointer_git_unavailable`; `.git`-as-file worktree resolved; zero fuzz crashers |
| V4-SP10-12 | MANIFEST, immutability, reader chain, verify, parent fallback | `go test ./internal/checkpoint/ -run 'TestManifestLineFormat\|TestFinalize\|TestGetDetectsManifestMismatch\|TestLatest\|TestChain\|TestVerify\|TestList\|TestReaderRef' -v` | manifest line is exactly `{"seq":N,"sha256":"sha256:<64hex>","bytes":B,"created":"<RFC3339.mmm>Z"}`; finalized files are `0444` / read-only on Windows; a seq collision increments; a flipped byte ⇒ `core.ErrContract` + one `Loud`; `Latest` falls back to the parent; `Chain` is oldest-first and rejects cycles; `Verify` returns mismatched seqs ascending; **`TestReaderRefLeavesWriterOnlyFieldsZero`** — `Ref.Tokens` and `Ref.Frontier` are 0 on every reader-returned `Ref` |
| V4-SP10-13 | **Focus instructions with the O1 incremental span** | `go test ./internal/checkpoint/ -run 'TestFocus' -v` | `TestFocusStandingTemplateVerbatim` equals `testdata/golden/checkpoints/focus/standing.txt` with a first paragraph byte-identical to §8.5's template; `_StandingOnlyWhenAllOptionsOff`; `_IncrementalSpanNamesPathAndTurn` equals `focus/incremental.txt` and contains ``A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn 58`` and `Summarize only what happened after turn 58`; `_OmitsSpanWhenFrontierZero`; `_OmitsSpanWhenConfigDisabled`; `_ContainsSentinel`; `_ForwardSlashesOnWindows`; `_CappedAtFourThousandBytes` |
| V4-SP10-14 | **Import discipline — `checkpoint` cannot read a transcript** | `go test ./internal/checkpoint/ -run TestNoForbiddenImports -v` ; `go run ./tools/devtool lint` (`importgraph`) | the import set is a subset of `{core, paths, config, logging, obs, store, dag, negknow, pins, grammar, tokens}` plus stdlib; `hookio`, `scheduler`, `ipc`, `daemon`, `os/exec`, `net`, `net/http` absent. `pins` imports the foundation only and does **not** import `checkpoint` |
| V4-SP10-15 | **`PreCompact` behaviour, including the near-deadline finalize** | `go test ./internal/checkpoint/ -run 'TestPreCompact' -v` | `_FinalizesExistingDraft` (`NewDraft == false`, instructions carry the draft's frontier); `_ColdPathBeginsDraft` (`NewDraft == true`, `checkpoint.cold_precompact == 1`, a checkpoint is still written); `_WritesStateObservable` (`state/precompact.json` with `sentinel`, `timeout_ms == 20000`, `span_instruction`, `wall_ms >= 0`); **`_FinalizesAsIsNearDeadline`** (returns within 600 ms with a valid checkpoint whose `EncodedSegments` is a strict subset, **no error**); `_StartsFreshDraftAfterFinalize`; `_ErrorStillLeavesHookHealthy`; `_RecordsSuppliedTimeout` with no `20000` literal in `internal/checkpoint/*.go` |
| V4-SP10-16 | **Scheduler-cadence checkpoints, gated by degradation** (§8.5 *"and independently on the scheduler's own cadence"*) | `go test ./internal/checkpoint/ -run 'TestCadence' -v` | `TestCadenceFinalizesWhenDraftReachesBudget` — a checkpoint is written on an idle tick with **no** `PreCompact`, and a fresh draft with `parent == 1` exists; `TestCadenceFinalizesAfterEightSegments`; **`TestCadenceIsOffInDegradedPassive`** — no checkpoint written, yet `advance_frontier` still ran and the draft still grew |
| V4-SP10-17 | Conformance suites, zero skips | `go test ./internal/checkpoint/... -run 'Suite'` ; `grep -R "t.Skip" internal/checkpoint internal/pins` | `RunWriterSuite`, `RunReaderSuite` and `RunPinsSuite` (all three in `checkpointtest`; `internal/pins` has no conformance subpackage) green; the grep returns nothing |
| V4-SP10-18 | Checkpoint e2e through the real hook | `go test ./test/e2e/ -run 'TestE2E_Checkpoint' -v` | `qompack checkpoint` exits 0 against a real daemon after 60 replayed turns and 3 segments closed via `store.SegmentLog.Close`; stdout parses as `hookio.Output` with `hookSpecificOutput.customInstructions` containing the span paragraph and `checkpoint.SentinelPhrase`; `checkpoints/0001.json` exists, is read-only and re-hashes to its manifest line; `state/precompact.json` exists. `checkpoint_degraded_test.go`: the file is still written and `customInstructions` is **absent** |
| V4-SP10-19 | W-2 contract fixtures are consumable by the real SP-11 and SP-13 | `go test -count=1 ./internal/rehydrate/... ./internal/mcp/...` | every test that reads `testdata/golden/contracts/checkpoint/{full,minimal,empty}.json` passes against those exact bytes. Per Rule W-2, a fixture the real writer cannot reproduce is a verification failure — fix the implementation, not the fixture, unless §7.1 class (c) applies |
| V4-SP10-20 | **Checkpoint benchmark budgets, including B-E** | `go test ./internal/checkpoint/ -bench . -benchmem -run '^$' -benchtime 2s` ; `go run ./tools/devtool bench-hotpath --iterations 200 --warm-daemon --json v4-be.json` | `BenchmarkFinalize` mean < 50 ms (40 segments, 64 decisions, 400 pointers); `BenchmarkAdvanceSegment` < 25 ms; `BenchmarkTruncate` < 5 ms; `BenchmarkExtractDecisions` < 20 ms; `BenchmarkStripInjections` < 2 ms; **B-E p99 < 2 s on linux, macos and windows** |
| V4-SP10-21 | **SP-10's own exit gate — frontier advancement shrinks the residual span** | Set `checkpoint.frontier.advanceOnSegmentClose` to `false` in the repo's `.qompack/config.json` (the driver resolves project config from the repo root, `test/replay/main.go:220` → `loadConfig`), then `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --phase 4 --max-cpu 3m --ci --out $TMP/v4-frontier-off.json`; set it back to `true` and repeat with `--out $TMP/v4-frontier-on.json`; then put the file back as you found it — `.qompack/` is git-ignored and `.qompack/config.json` is untracked, so `git checkout --` cannot restore it: copy it aside first, or delete it again if the "off" run is what created it | `on.Score.ResidualSpan.P50 ≤ 0.70 × off.Score.ResidualSpan.P50` (at least a 30 % reduction), and `Score.Divergence` does not regress by more than 2 % (§11.3). Record both P50s. **The A/B is a config toggle plus two full replays, not driver flags**: the driver registers only `corpus baseline policies out signoff growth sketch phase regen-corpus write-baseline max-cpu max-wall ci`, so `--filter`, `--set` and `--json` are each `flag provided but not defined` and exit 2 with nothing measured (§1 rule 8). **There is no corpus-subset flag**, so the `multi-compaction` restriction cannot be expressed here: both runs cover all 24 sessions, and the multi-compaction subset is isolated in the Go test (§4.4) rather than in the driver |
| V4-SP10-22 | Placeholder and stub-residue scan | `grep -rniE "TODO\|TBD\|FIXME\|XXX\|not implemented" internal/checkpoint internal/pins internal/daemon/wire_checkpoint.go internal/cli/hook_checkpoint.go` | no output; `core.ErrNotImplemented` appears nowhere in these two packages |
| V4-SP10-23 | Coverage floors | `go run ./tools/devtool cover` | `internal/checkpoint` ≥ **90 %**, `internal/pins` ≥ **90 %** — both floors are read from `plans/OWNERS.tsv`, which is the file `tools/devtool/cover.go` actually loads (`cover.go:95`); a floor stated here that disagrees with that row is not enforced, and the row is what the gate reports |

### 2.11 SP-11 — L5 rehydrator, rules, skills (**new this wave**) — `internal/rehydrate`, `internal/rules`, `internal/skills`

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP11-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/rehydrate/... ./internal/rules/... ./internal/skills/...` | exit 0 |
| V4-SP11-02 | Glob engine and `paths:` frontmatter parsing | `go test ./internal/rules/ -run 'TestMatch_\|PropMatch_NeverPanics\|TestParseFront_' -rapid.checks=1000 -v` | `**` matches zero and many segments; a trailing `**` matches the directory itself; a bare glob implies any depth; a single `*` never crosses a segment; char classes and `?` behave; a 40-segment pathological pattern returns `false` in < 1 ms; `PropMatch_NeverPanics` over 10 000 cases. `TestParseFront_ListForm`, `_InlineArray`, `_ScalarForm`, `_CRLF`, `_Unclosed`, `_NoFrontmatter`, `_UnknownKeysIgnored`, `_OversizeFrontmatter` all PASS |
| V4-SP11-03 | **Path-scoped rule restoration** (G4.1) | `go test ./internal/rules/ -run 'TestPathScoped_' -v` | matches only the pointer set, including the inline rule directly under `.claude/`; skips unscoped, empty-`paths:` and oversize files; empty pointer set ⇒ empty slice, nil error; byte-identical results across 10 runs; case-insensitive pointer matching on windows/darwin; bodies CRLF-normalized; `Rule.Tokens == core.Tokens((len(Body)+3)/4)` |
| V4-SP11-04 | **Nested `CLAUDE.md` restoration** (G4.2) | `go test ./internal/rules/ -run 'TestNestedClaudeMD_' -v` | containing directory only — `_ExcludesProjectRoot` and **`_DoesNotWalkAncestors`** are the literal §8.6 reading; dedup across sibling pointers; unicode, spaces and a > 260-char path all resolved; every returned `Rule.Nested == true` |
| V4-SP11-05 | **Skill index** (G4.4) | `go test ./internal/skills/ -run 'TestIndex_\|TestBodyTokens' -v` ; `grep -rn "450" internal/skills internal/rehydrate --include=*.go \| grep -v _test.go` | dir and flat skill forms both indexed, sorted by name; name from frontmatter else the directory basename; description falls back to the first non-heading body line truncated to 100 runes; empty ⇒ `"(no description)"`; **prefix** truncation, not cheapest-first; zero budget returns all; symlinks and oversize skipped; the grep returns nothing — the budget comes from `runtime.rehydrate.skillIndexTokens` |
| V4-SP11-06 | **The eight §8.6 items in normative order** | `go test ./internal/rehydrate/ -run 'TestRenderOrder_EightNormativeKindsInIotaOrder\|TestBuild_ItemOrderInPayload\|TestBuild_RankIsOneBasedAmongEmitted' -v` | the eight kinds appear in strictly increasing `ItemKind` order with the two additive kinds between `ItemPointers` and `ItemDropReport`; `## 1. ` … `## 8. ` at strictly increasing byte offsets with `## 6a.` and `## 6b.` between `## 6.` and `## 7.`; rank counts **emitted** items only |
| V4-SP11-07 | **Injection tagging and the contract sentinel** (§8.5, §12.1) | `go test ./internal/rehydrate/ -run 'TestBuild_InjectionTagging\|TestUnwrap_RejectsMalformed\|TestSentinel_Deterministic\|TestBuild_SentinelIsLastLineInsideTags' -v` | payload starts `<!-- qompack:injected seq=7 ver=1 -->` and ends `<!-- /qompack:injected -->`; `Unwrap` round-trips and returns `seq == 7`; `Sentinel(s, seq) == "qpk-ac-" + core.HashBytes("qompack.sentinel.v1", []byte("sess-1\|7")).Short()`; the sentinel comment is the last line inside the tags |
| V4-SP11-08 | Item 1 — invariants are never truncated | `go test ./internal/rehydrate/ -run 'TestInvariants_' -v` | all four invariants verbatim including a 900-byte text; at a forced 200-token cap they still appear with `Result.Degraded == true` and `Items[0].Truncated == false`; zero invariants omits `## 1.` |
| V4-SP11-09 | **Item 2 — verbatim intent from L0, never from a summary** (G2.3, G7.3) | `go test ./internal/rehydrate/ -run 'TestUserIntent_' -v` | `_FromL0NotCheckpoint` (L0 wins, `DropEntry{Kind:"intent_mismatch"}`, one `Loud`); `_EarliestTurnOfThisSessionWins`; `_IgnoresOtherSessions`; `_ToolUseLookupErrorSkipsHit`; `_FallsBackToCheckpointWhenStoreEmpty` / `_WhenStoreErrors` with `DropEntry{Kind:"user_intent_source"}`; `_StripsPriorInjections`; **`_FencedPromptSurvivesVerbatim`**; `_TruncatesEvolutionNotOriginal`; `_EvolutionHasItsOwnShare`; `_CapsAtMaxIntentBytes` |
| V4-SP11-10 | Item 3 — eliminations digest and the standing instruction | `go test ./internal/rehydrate/ -run 'TestEliminations_' -v` | top-N from `runtime.rehydrate.eliminationsTopN`; ordered by slice score with the `0.8 × 0.75` file-proxy rule and a TS-then-ID tiebreak; `15 further eliminations are recorded and not shown.`; **both scopes read**, not just `defaultScope`; the stale note verbatim; `staleResponse:"drop"` omits; one collapsed `DropEntry{Kind:"elimination"}`; ledger error falls back to `Checkpoint.Eliminated` with `DropEntry{Kind:"elimination_source"}`; graph error degrades silently; `"Before committing to an approach, call already_tried."` always present |
| V4-SP11-11 | Items 4/5/6 — decisions, current work, **pointers not contents** | `go test ./internal/rehydrate/ -run 'TestDecisions_\|TestCurrentWork_\|TestPointers_' -v` | what/why/rejected/evidence in order, empty lines omitted; drop `Detail` names `call why(dec_`; `blocked on: none` for a nil `BlockedOn`; a zero-valued `CurrentWork` omits `## 5.`; **no code fence anywhere in items 4, 5 or 6**; files before tools; a multiline pointer unit is dropped with a `Loud` and the rest survives; pointer drops name `call recall or re_read` |
| V4-SP11-12 | Items 6a/6b — instruction restoration (G4.1, G4.2, G4.3, G4.4) | `go test ./internal/rehydrate/ -run 'TestRestored_\|TestSkillIndex_' -v` | path rules and nested `CLAUDE.md` both restored for the pointer set, path rules first; **whole-rule-or-nothing** — a rule that does not fit is absent entirely with `DropEntry{Kind:"path_rule"}` and no partial body; scan error and nil scanner both degrade with a drop entry; skill-index budget from config; unindexed skills reported; a ~7 500-token skill body emits a `Detail` naming `head-first` and `5000`; six skills totalling 30 000 tokens emit a `DropEntry{Kind:"skill", ID:"*"}` naming the 25 000-token cap |
| V4-SP11-13 | **Item 7 — the drop report** (G4.5) | `go test ./internal/rehydrate/ -run 'TestDropReport_' -v` | rendered order `path_rule, nested_claude_md, skill, elimination, decision, pointer`; `Checkpoint.Dropped` entries included; truncation appends `… and N more; call dropped()`; `len(Result.Dropped)` is complete regardless of what was rendered; nothing dropped omits `## 7.` |
| V4-SP11-14 | **The 8–12K budget discipline** (G3.3, §8.6, §12 risk row) | `go test ./internal/rehydrate/ -run 'TestClampBudget_\|TestBuild_NeverExceedsMaxTokens\|TestBuild_SmallerThanStock\|TestBuild_MinFillReadmitsUnits\|TestBuild_PrefixTruncationNotCheapestFirst\|TestBuild_CarryForward\|TestBuild_TruncatedFlagSet\|PropBuild_' -rapid.checks=500 -v` | `Budget=0 ⇒ 12 000`, `500 ⇒ 8 000`, `50 000 ⇒ 12 000`, inverted config corrected without panic; `Result.Tokens ≤ 12 000` at budgets 8 000 / 10 000 / 12 000; **`Result.Tokens < 50 000 + 25 000`** — the §2.4 stock figure asserted as a test; min-fill re-admits and removes the drop entry; prefix truncation, never cheapest-first; carry-forward between shares; `PropBuild_MonotoneInBudget` proves the §6.9 embedded-coding property (the item set at `B1` is a prefix-wise subset of the set at `B2`); `PropBuild_NeverPanics`; `PropBuild_Deterministic` |
| V4-SP11-15 | Drop-report persistence — the backing for the MCP `dropped` tool | `go test ./internal/rehydrate/ -run 'TestReporter_' -v` | record→`CurrentDrops` round-trips in order; a missing file is `(nil, nil)`, not an error; a corrupt file is `(nil, nil)` + one `Loud` + deletion; `Reset` deletes and is idempotent; the session id is sanitized to `^rehydrate-[A-Za-z0-9._-]+\.json$`; the golden state file is byte-identical |
| V4-SP11-16 | Rehydration goldens and degradation | `go test ./internal/rehydrate/ -run 'TestBuild_Golden_\|TestBuild_NonCompactSourceEmitsNothing\|TestBuild_ContextCancelled\|TestBuild_NilDeps' -v` (**no** `-update`) | `full-12k.txt`, `full-8k.txt`, `minimal.txt`, `no-checkpoint.txt`, `degraded.txt` all reproduce byte-for-byte (`Build` reads no clock, so a golden that moves between runs is a bug); `startup`/`resume`/`clear` emit nothing; a cancelled context returns `ctx.Err()` with no partial write; all-nil `Deps` yields `Degraded == true` with items 1, 2 and 8 |
| V4-SP11-17 | **G7.5 — the checkpoint is the fallback when the summarizer fails** | `go test ./internal/rehydrate/ -run TestBuild_NoTranscriptRead_ClosesG75 -v` ; `go test ./test/e2e/ -run TestE2E_SessionStartCompactAfterFailedSummary -v` | the spy `Deps` record **zero** reads of `Event.TranscriptPath`, and the payload is byte-identical whether or not the transcript tail contains a `<summary>` block. This is §2.8's `content: null` failure costing the session nothing |
| V4-SP11-18 | Daemon `SessionStart(compact\|clear)` service | `go test ./internal/daemon/ -run 'TestService_' -v` | `_CompactEmitsAdditionalContext` with `HookEventName == "SessionStart"` and an injection-tagged payload; **`_DegradedPassiveEmitsNothing`** (`Build` never called — assert with a spy); `_CheckpointNotFoundStillEmits`; `_CheckpointErrorEmitsNothing` + one `Loud`; `_PanicRecovered`; `_RecordsState`; `_StateWriteFailureStillEmits`; `_ClearResetsState` with zero ledger/store calls; `_ClearOnMissingStateIsNoOp` |
| V4-SP11-19 | Rehydration e2e through the real binary | `go test ./test/e2e/ -run 'TestE2E_SessionStart' -v` | `qompack session-start` with `{"hook_event_name":"SessionStart","source":"compact","session_id":"e2e-1"}` exits **0** and prints JSON whose `hookSpecificOutput.additionalContext` contains `## 1. Invariants`, `## 7. No longer in context`, `## 8. Retrieval` and `Sentinel("e2e-1",7)`; the estimated token count is **≤ 12 000**; `source:"clear"` emits nothing and removes the state file; a deleted `.qompack/` still exits 0 with a degraded payload |
| V4-SP11-20 | `rehydrate.Reporter` structurally satisfies `mcp.DropReporter` | `go test ./test/e2e/ -run TestE2E_DropReporterSatisfiesMCP -v` | `var _ mcp.DropReporter = rehydrate.NewReporter(root, log)` compiles. The assertion lives in `test/e2e` because `rehydrate` may not import `mcp` (§3.2) |
| V4-SP11-21 | Import discipline | `go run ./tools/devtool lint` (`importgraph`) ; `git diff --name-only <v0.2.0>..HEAD -- internal/observer/` | `rules` and `skills` import the foundation only; `rehydrate` imports only `checkpoint store negknow dag rules skills tokens` plus the foundation; nothing imports `daemon`; no file under `internal/observer` was modified by SP-11 |
| V4-SP11-22 | **L5 benchmark budgets** | `go test ./internal/rehydrate/ ./internal/rules/ ./internal/skills/ -bench . -benchmem -run '^$' -benchtime 2s` ; `go test ./test/e2e/ -run TestE2E_SessionStartLatency -v` | `L5-BUILD` (`BenchmarkBuild`) p99 < **250 ms**; `L5-RULES` (`BenchmarkPathScoped`, 2 000-file tree / 24 rules / 40 pointers) p99 < **50 ms**; `L5-SKILLS` (`BenchmarkIndex`, 60 skills) p99 < **20 ms**; `L5-SESSIONSTART` p99 < **1.5 s** over 30 warm runs |
| V4-SP11-23 | Conformance suites, zero skips; placeholder scan | `go test ./internal/rehydrate/... -run Suite` ; `grep -rn "t.Skip\|ErrNotImplemented" internal/rehydrate internal/rules internal/skills` ; `grep -rniE "TODO\|TBD\|FIXME\|XXX" internal/rehydrate internal/rules internal/skills internal/daemon/rehydrate_service.go` | `rehydratetest`, `rulestest` and `skillstest` green with zero skips; both greps return nothing |
| V4-SP11-24 | Coverage floors | `go run ./tools/devtool cover` | `internal/rehydrate` ≥ **85 %**, `internal/rules` ≥ **75 %**, `internal/skills` ≥ **75 %** |

### 2.12 SP-12 — L3 scheduler (**new this wave**) — `internal/scheduler`, `internal/daemon/scheduler_*.go`, `test/replay/l3policy`

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP12-01 | Package green under race, and the daemon files with it | `go test -race -count=2 ./internal/scheduler/... ./internal/daemon/` | exit 0 |
| V4-SP12-02 | **Threshold arithmetic** (§2.5, §8.4) | `go test ./internal/scheduler/ -run 'TestEffectiveWindow_Section25Arithmetic\|TestSoftFloor_\|TestHardCeiling_\|TestThresholds_ZeroWindow\|TestSoftFloorBelowHardCeiling_Property' -rapid.checks=1000 -v` | `EffectiveWindow(200 000, 32 000) == 180 000` (the 20 000 cap binds), `(200 000, 8 000) == 192 000`, never negative; `SoftFloor == 99 000` at 55 %; `HardCeiling == 147 000` = 180 000 − 13 000 − 20 000, asserted **below the host's 167 000**; margin over-run clamps to 1; the property proves `SoftFloor < HardCeiling` always — §12's "plugin acts first by design" can never invert |
| V4-SP12-03 | **The sliding-TTL idle model, keyed on the last API call** (E1, §5.4, §8.4) | `go test ./internal/scheduler/ -run 'TestClassifyTTL_\|TestCacheFactor_\|TestSlidingTTLUsesAPICallNotCacheWrite' -rapid.checks=500 -v` | boundaries exact: gap 149 s `warm`, **150 s `expiring`**, 299 s `expiring`, **300 s `cold`**; no API call ⇒ `TTLUnknown`; a negative gap clamps to warm; `CacheFactor` ramps 1.0 → 0.5 at 225 s → ≈0.00667 at 299 s → 0.0 cold, monotone non-increasing and always in `[0,1]`; **a fresh `LastCacheWriteTS` with a stale `LastAPICallTS` is `TTLCold`** — the E1 correction, asserted directly |
| V4-SP12-03b | **The cache regime — which TTL and which `w` this session is actually billed at** (§5.1's *"verify against current pricing"*) | `go test ./internal/scheduler/ -run 'TestResolveCacheRegime_\|TestClassifyTTL_UnknownRegime\|TestClassifyTTL_EffortChange\|TestTTLAnchorIsNeverLaterThanStop' -rapid.checks=500 -v` | ladder order `force_5m` > `disabled` > `enable_1h` > `unknown`, with `FORCE_PROMPT_CACHING_5M` beating a simultaneous `ENABLE_PROMPT_CACHING_1H`; `WriteMultiplier` **2.0** under `enable_1h` and **1.25** under `force_5m`, `ReadMultiplier` 0.1 in both — the two documented figures, and the reason `w` cannot be a scalar; unknown yields `TTLMin == cfg.Cache.TTLSeconds`, `TTLMax == 3600` and the **dearer** `w`; `DISABLE_PROMPT_CACHING*` yields `r == w == 1.0`; a subagent pins to `(300, 300, 0.1, 1.25)`; **a 400-second gap on the unknown rung is `TTLExpiring`, not `TTLCold`** — the row that stops the 40× mis-cut; an effort change is `TTLCold` at gap 0; the anchor property holds for every event ordering. `git grep -n 'os\.Getenv' -- internal/scheduler` returns nothing, and `config.Defaults().Scheduler.Cache` still deep-equals Appendix C — the regime is computed beside the config, never written back into it |
| V4-SP12-04 | **Young–Daly cadence with δ measured at runtime** (§6.7) | `go test ./internal/scheduler/ -run 'TestYoungDaly_\|TestMTBF_\|TestResolveDelta_' -v` | `YoungDaly(20, 1800) == 268.3281572999748` (±1e-9); non-positive, NaN and Inf inputs all yield 0; `MTBF` from burn rate is 1800.0 at context 120 000 / ceiling 147 000 / burn 900, and 0 at or above the ceiling or with an unknown burn; **`TestResolveDelta_NilMeansMeasureNotZero`** — both nil ⇒ `(0, false)`, `young_daly_delta_unmeasured=1`, and the clause **never fires** |
| V4-SP12-05 | Ski-rental threshold computed, never written — and **it is two numbers** | `go test ./internal/scheduler/ -run 'TestSkiRentalShouldWrite\|TestSkiRentalThreshold_TracksRegimeNotConfig' -v` ; `go run ./tools/lint/nomagic ./internal/scheduler` | threshold `w/r == 12.5` at `r=0.1, w=1.25` **and `== 20` at `r=0.1, w=2.0`**, the one-hour write multiplier — the `≈12.5` in §5.6 and Appendix A is the five-minute figure and is not the only one; the caller reads `w` from `Inputs.Regime.WriteMultiplier`, never from `cfg.Cache.WriteMultiplier`, which stays at Appendix C's 1.25 in both cases; 12 ⇒ false, 12.5 ⇒ false, 12.6 ⇒ true; `r=0` or `w=0` ⇒ false; `nomagic` exits 0, which is the gate that actually enforces §11.6 here. **Do not use a tree-wide `grep -rn "12\.5" internal/`**: it already returns `internal/sketch/doc.go`, a doc comment enumerating `nomagic`'s forbidden set, and `nomagic` inspects literals rather than comments — a verifier running the grep records a FAIL for a non-defect or "fixes" a correct comment. If a grep is wanted anyway, scope it to the owning package and drop comment lines: `grep -rn "12\.5" internal/scheduler --include=*.go \| grep -v _test.go \| grep -v '^\s*//'` |
| V4-SP12-06 | **BOCD changepoint detection** (§6.6) | `go test ./internal/scheduler/ -run 'TestBOCD_' -rapid.checks=500 -v` | stationary series: `RunLength ≥ 100`, ≤ 2 declarations over 200 observations; a 0.6 step change is detected at index ∈ [100, 105]; hysteresis prevents a declaration storm; posterior normalized to within 1e-9 with every entry ≥ 0; `len(Posterior) ≤ 512` always and mean < 64 over 5 000 observations with non-increasing per-op wall time (the O(1)-amortized claim); marshal round-trip byte-identical; **ten corruption cases each wrap `core.ErrNotFound` and leave `State()` unchanged**; a header claiming `R=4e9` allocates < 64 objects; feature subsetting, unknown-feature dropping and empty-feature fallback all behave; invalid hazard clamped; `Reset` restores the prior; a degenerate posterior self-heals without NaN |
| V4-SP12-07 | **The composite trigger** (§8.4) | `go test ./internal/scheduler/ -run 'TestEvaluate_' -v` | below the soft floor nothing fires; above it with no clause, `Reasons == ["soft_floor"]` and `ShouldCompact == false`; changepoint ⇒ `UrgencyAdvisory`; Young–Daly fires only past the interval; hard ceiling ⇒ `UrgencyNow`; idle cold cache ⇒ `TTLCold`; **`TestEvaluate_ReasonsOrderStable` gives exactly `["soft_floor","changepoint","young_daly","hard_ceiling","idle_cold_cache","cache_expiring"]`** — `cache_expiring` is appended last so the five shipped orderings are unchanged; **`TestEvaluate_CacheExpiring_FiresBeforeExpiry`** fires at gap 250 s on a known 5-minute regime with `TTL == TTLExpiring` and `CacheFactor > 0`, which is the point: the prefix is still readable, so the summarization request this recommendation leads to reads it at `r` rather than reprocessing it at full price — `(1−r)·n` cheaper, 135 000 base-input-token-equivalents at a 150 000-token context; it stays silent below the `0.8` fraction, silent on an unknown regime, and silent below the soft floor; `TestEvaluate_Purity_NoInputMutation` and `_Purity_Idempotent` (`go-cmp`, no diff); `TestEvaluate_ZeroEffectiveWindow` yields a single `error_no_window` key; `TestEvaluate_NoAllocationsBeyondBudget` ≤ 8 allocations |
| V4-SP12-08 | **p-selection with the cache term** (§5.3, §5.4, G5.2) | `go test ./internal/scheduler/ -run 'TestEvaluate_Argmax\|TestEvaluate_ScoreArithmeticExact\|TestEvaluate_MultipliersReadFromConfig\|TestEvaluate_LambdaZeroDisablesDistortion\|TestEvaluate_RoundBoundary\|TestEvaluate_NoCandidates\|TestEvaluate_CandidatesCappedAt32\|TestEvaluate_NonMonotonicReclaimableFlagged\|TestEligible_\|TestPrepareCandidates_\|TestScoreCandidates_\|TestChooseP_' -v` | warm ⇒ `P.Pos == 118_000` with `PScore == −1_916.8`; cold ⇒ `P.Pos == 40_000` with `PScore == 2_916` and `Breakdown["rewrite"] == 0` — **with the regime pinned to known-5m**, without which the row asserts the superseded scalar behaviour; the same fixture on the **unknown** rung gives `P.Pos == 118_000` and `Breakdown["rewrite"] > 0`, the same inputs and the opposite cut, which is the finding; `deepCutWhenCold=false` ⇒ the latest boundary wins the tie; **`TestEvaluate_ScoreArithmeticExact`** — `600 − 2212.5 − 16.8 == −1629.3` (±1e-9); **`TestEvaluate_MultipliersReadFromConfig`** — doubling `readMultiplier` exactly doubles `Breakdown["reclaimable"]`, which is §12's "read `r` and `w` from config, never hardcode" proven arithmetically; candidates ∩ round boundaries, with a `round_boundary_relaxed` fallback; cap at 32 keeping the highest `Pos` |
| V4-SP12-09 | `Breakdown` completeness is frozen | `go test ./internal/scheduler/ -run 'TestEvaluate_BreakdownKeysComplete\|TestEvaluate_Background' -v` | the key set equals `testdata/golden/scheduler/decision-warm.json` — a missing **or extra** key fails, so SP-14's future status surface cannot silently lose a field. `window_source` is correctly absent (the Runtime adds it). Cold background list is exactly `["advance_frontier","precompute_slice","refresh_delta","rebuild_bloom","compact_dag","gc"]`; warm is `["advance_frontier"]`; `backgroundWork=false` yields nil |
| V4-SP12-10 | **The p-selection ship-order gate** (closing note 3) | `go test ./internal/scheduler/ -run 'TestPSelectionAvailable_DefaultFalse\|TestEnableDisablePSelection\|TestPSelectionGate_ConcurrentAccess' -race -v` ; `go test ./test/guards/ -run 'TestGuard_SubmodularInertWithoutPSelection\|TestGuard_SelectorRefusesWithoutPSelection' -v` | `PSelectionAvailable()` is `false` as a process default (recorded in `TestMain` before `m.Run`) and `true` only after `NewSchedulerRuntime` succeeds; concurrent access is race-clean. The `test/guards` binary constructs no `Runtime`, so the gate is still `false` there and both guards assert exactly what they asserted at V3 — invariant 4 (`core.ErrBudget`) and ship order (`core.ErrNotImplemented`). **Record both readings** (`false` in `test/guards`, `true` after `NewSchedulerRuntime` in `internal/daemon`), and author the one branch nothing covers today: `go test ./internal/analyzer/ -run TestNewSelector -v` with the gate opened by SP-12's exported setter must show `NewSelector` **succeeding** and `Select` returning `core.ErrNotImplemented`, which is where inertness now lives |
| V4-SP12-11 | **§8.7 eviction ordering as pure spec** | `go test ./internal/scheduler/ -run 'TestDropClassOf_\|TestEvictionRank_Order' -v` ; `go test ./internal/daemon/ -run 'TestClassifyDrop_' -v` | the §2.2 compactable set (`FileRead`, `Read`, `Bash`, `PowerShell`, `Grep`, `Glob`, `WebSearch`, `WebFetch`, `FileEdit`, `Edit`, `MultiEdit`, `FileWrite`, `Write`) is `DropOrdinary`; `Task`, `Agent`, `AgentTool`, `mcp__*`, `""`, `NotebookEdit` are `DropNone`; ephemeral beats everything, superseded beats the tool class; **`DropEphemeral(3) > DropSuperseded(2) > DropOrdinary(1) > DropNone(0)`** — §8.7's "ahead of ordinary tool results"; the store adapter maps all three record fields |
| V4-SP12-12 | Reclaimable-token suffix index | `go test ./internal/daemon/ -run 'TestReclaimableIndex_' -rapid.checks=500 -v` ; `go test -bench BenchmarkReclaimableIndexBuild_5000Blocks ./internal/daemon/` | suffix sums exact (`After(0)=23`, `After(11)=18`, `After(31)=0`); `DropNone` blocks excluded; **monotone non-increasing in `p`** by property test — §5.4's monotonicity proven, not assumed; build ≤ 3 ms/op |
| V4-SP12-13 | Feature extraction from L0 signals | `go test ./internal/daemon/ -run 'TestFeaturesFrom_' -v` ; `go test -bench BenchmarkFeaturesFrom ./internal/daemon/` | path Jaccard `2/4 = 0.5`, empty union ⇒ 1.0; tool shift as total variation (1.0 / 0.0 / 0.5); gap seconds from `FakeClock`, first observation and negative gaps ⇒ 0.0; todo/test/commit each ⇒ 1; windows bounded at 8 after 10 000 observations; a 200 KB preview caps shingles and runs < 10 ms; ≤ 100 µs/op |
| V4-SP12-14 | Candidate assembly with a cached `CrossingEdges` | `go test ./internal/daemon/ -run 'TestAssemble_' -v` ; `go test -bench BenchmarkAssembleCandidates_2000ToolUses ./internal/daemon/` | changepoints ∩ round boundaries; unresolvable turns counted (`sched.candidate.unresolved`); coupling from `dag.CrossingEdges`; **3 calls on a second assembly, 6 after a graph mutation**; cap at 32 keeping the highest `Pos`; reclaimable from the suffix index; nil graph ⇒ `(nil, nil)`; a panicking `CrossingEdges` drops that candidate with one `Loud`; ≤ 20 ms cold / ≤ 200 µs warm |
| V4-SP12-15 | **`scheduler.Runtime` — window ladder, context tokens, EWMAs, persistence, self-healing** | `go test ./internal/daemon/ -run 'TestNewSchedulerRuntime_\|TestRuntime_' -race -v` | missing deps each named in the error; the gate flips on construction and off on close; **`TestRuntime_WindowResolutionLadder`** gives 150 000 / 242 000 / 180 000 with `window_source` 3 / 2 / 1 and rejects out-of-range rung-1 values; context tokens summed from segments; δ EWMA `20 → 23.0`; burn-rate EWMA at α=0.2; `IdleSince` flips at `detectAfterSeconds`; **`TestRuntime_PersistRoundTrip`** restores `deltaEWMA`, `burnEWMA`, `cpTurns`, `rounds`, `frontier`, `residual`, `lastDecision`; state is discarded on session, hazard or feature-list mismatch; **corrupt state self-heals with two `Loud`s**; turn lists capped; close is idempotent; 8-goroutine concurrency race-clean over 5 000 operations |
| V4-SP12-16 | Scheduler state codec | `go test ./internal/daemon/ -run 'TestStateCodec_' -v` | `state/bocd.json` and `state/scheduler.json` round-trip; `p_score == −5379.3` equals `reclaimable − rewrite − distortion` (the golden SP-12's rewrite moved it to; a row asserting the pre-rewrite `−22429.3` fails against a correct implementation); unknown fields ignored at Debug; **`paths.WriteAtomic` is used and `paths.AppendOnly` is never called** — these two files are mutable daemon state, not append-only store state; turn lists capped |
| V4-SP12-17 | **The `Services` tap — L0 events reach L3 without editing SP-05 or SP-08** | `go test ./internal/daemon/ -run 'TestWrapServices_' -v` ; `git diff --stat <v0.2.0>..HEAD -- internal/daemon/` | inner seams run **first** and their `hookio.Output` and error are returned unchanged; all-nil inner seams tolerated; `ObserveTool` drives `Observe` with the record's turn; `ObserveStop` records a round boundary and `ObserveTool` at the same turn does not; boundary signals close a segment once each with cause `todo`/`test`/`commit`; `SessionStart` binds and `SessionEnd` persists then closes; **the prompt seam does zero store I/O** (the 250 ms reply deadline); a missing record is counted, not fatal; a nil runtime leaves `Services` pointer-identical. The `git diff --stat` lists only `scheduler_*.go` among SP-05's pre-existing daemon files |
| V4-SP12-18 | **O5 continuous frontier advancement + the DPI guard at L3** (§8.5) | `go test ./internal/daemon/ -run 'TestFrontier_' -v` | close-and-roll on todo completion, a passing test and a git commit; **no `Open` when there is no current segment** (SP-08 still owns the first open); `Advance` receives exactly the closed-and-unencoded ids, ascending; a nil writer is a counted no-op; a second call in one window moves nothing; **`TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes`** — `core.ErrAlreadyEncoded` for one id yields one `Loud`, a retry with the remainder, advancement only for the clean ids, and a second violation aborts the draft; residual recomputed, never negative, warned once per session above `maxResidualTokens` |
| V4-SP12-19 | **O3 idle-time background work, gated by decision and by degradation** (§8.4) | `go test ./internal/daemon/ -run 'TestIdleTask\|TestIdleActingTaskSkippedInDegradedPassive\|TestIdleWorkBindsDaemon\|TestIdleTasksRegistered' -v` | exactly six tasks in priority order `act.advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc` at priorities 110…160 (the band above SP-05's shipped `drain`/`sketches`/`metrics` at 10/20/30 — lower runs first); **`act.advance_frontier` is the only prefixed task and is suppressed in `ModeDegradedPassive`** while the other five keep running (§12.1 "no scheduler-initiated checkpoints"); every task inert until an `Evaluate` has placed it in `Decision.Background`; all six inert when `backgroundWork=false`; a nil ledger is safe; GC receives a positive deadline ≤ the run budget with retention from config; cancellation honoured within 5 ms |
| V4-SP12-20 | **The scheduler is not on the hot path** | `go test ./internal/scheduler/ ./internal/daemon/ -run TestSchedulerNotOnHotPath -v` | call-graph inspection of `internal/cli` finds no hook subcommand path reaching `scheduler.Evaluate`, `schedRuntime.Evaluate` or `WrapServicesForScheduler`. B-A is protected structurally, not by hope |
| V4-SP12-21 | The L3 replay policy | `go test ./test/replay/l3policy/ -v` | `Name() == "qompack-l3"`; byte-identical `KeepSet` and `Run` on a re-replay at `Seed: 1`; keep-set shape correct (everything before `P` plus preserved tools; the three droppable absent); **`TestPolicy_DoesNotImportDaemon`** — `go list -deps` contains `internal/scheduler` and **not** `internal/daemon`; the pause model anchors on `eval.DefaultLatencyModel()` as shipped — `PauseBaseMS 3000` plus `PausePerKResidualMS 150` per 1 000 residual tokens — so a 150 000-token residual yields **25 500 ms** and a 20 000-token residual **6 000 ms**. Assert against the model's own coefficients, never against a restated literal |
| V4-SP12-22 | Conformance suite, zero skips; placeholder scan; import purity | `go test ./internal/scheduler/... -run Suite` ; `grep -R "t.Skip" internal/scheduler/schedulertest/` ; `grep -RniE 'TODO\|FIXME\|TBD\|XXX\|not implemented' internal/scheduler internal/daemon/scheduler_* test/replay/l3policy` ; `go run ./tools/devtool lint` | `RunEvaluateSuite`, `RunDetectorSuite` and `RunRuntimeSuite` green; both greps return nothing; `internal/scheduler` imports only `core`, `paths`, `config`, `logging`, `obs` |
| V4-SP12-23 | **Scheduler benchmark budgets** | `go test ./internal/scheduler/ ./internal/daemon/ -bench . -benchmem -run '^$' -benchtime 2s` | `BenchmarkEvaluate_64Candidates` **≤ 50 µs/op and ≤ 8 allocs/op**; `BenchmarkBOCDObserve_4Features` ≤ 150 µs at a full 512-entry posterior and ≤ 20 µs at steady state; `BenchmarkBOCDMarshal` ≤ 2 ms; `BenchmarkFeaturesFrom` ≤ 100 µs; `BenchmarkAssembleCandidates_2000ToolUses` ≤ 20 ms cold / ≤ 200 µs warm; `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates` ≤ 25 ms; `BenchmarkReclaimableIndexBuild_5000Blocks` ≤ 3 ms; `BenchmarkSchedulerTap_ObserveTool` ≤ 1.5 ms |
| V4-SP12-24 | **The Phase-4 exit criterion** | `go test ./test/replay/ -run 'TestPhase4_' -v` | all six PASS — see §3.2 for the criterion quoted verbatim and the measurement procedure |
| V4-SP12-25 | Coverage floor | `go run ./tools/devtool cover` | `internal/scheduler` ≥ **85 %**; the SP-12-owned `internal/daemon` files must not pull that package below **75 %** |

### 2.13 SP-13 — L6 MCP retrieval layer (**new this wave**) — `internal/mcp`, `internal/daemon/mcpop.go`, `internal/cli/cmd_mcp.go`

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V4-SP13-01 | Package green under race, and the CLI/daemon files with it | `go test -race -count=2 ./internal/mcp/...` | exit 0 |
| V4-SP13-02 | **JSON-RPC 2.0 stdio server** | `go test ./internal/mcp/ -run 'TestInitialize\|TestNotification\|TestPing\|TestUnknownMethod\|TestMalformedJSON\|TestWrongJSONRPCVersion\|TestOversizedLine\|TestServeReturns' -v` | `initialize` echoes a supported protocol version and falls back to `2025-06-18`; `initialize.json` golden byte-equal; `serverInfo.name == "qompack"`; `instructions` contain `mcp.StandingInstruction`; notifications produce no response; `ping` returns `{}`; unknown method ⇒ `-32601` with `error.data.method`; malformed JSON ⇒ `-32700` with `"id":null` **and the stream keeps serving**; wrong `jsonrpc` ⇒ `-32600`; an oversized line ⇒ `-32600` then resynchronization; `Serve` returns nil on EOF and on context cancel |
| V4-SP13-03 | Stdout purity, concurrency, panic isolation | `go test ./internal/mcp/ -run 'TestNoStdoutPollution\|TestConcurrentCallsProduceWellFormedLines\|TestHandlerPanicIsolated' -race -v` | every line on `out` unmarshals into `rpcResponse` and nothing else appears; 200 interleaved `ping`s across 8 goroutines yield 200 well-formed lines with distinct ids and no interleaved bytes; a panicking handler yields `isError:true` with `internal error in tool panicky` and a subsequent `ping` succeeds (§12.3: *"MCP tool panic … never kills the server"*) |
| V4-SP13-04 | Server fuzz | `go test ./internal/mcp/ -run=XXX -fuzz FuzzServeLine -fuzztime 60s` | zero crashers over the committed seed corpus (valid `initialize`, a notification, a truncated object, a 2 MiB line, an embedded NUL, `{"jsonrpc":"1.0"}`) |
| V4-SP13-05 | In-repo JSON-schema validator | `go test ./internal/mcp/ -run 'TestSchema\|TestApplyDefaults\|TestAllEightSchemasCompile\|PropertySchemaAcceptsGeneratedValidDocs' -rapid.checks=1000 -v` | unknown property, missing required, wrong type, enum violation, below-minimum and above-maximum each produce one violation with the exact JSON pointer; defaults fill `k=5` and `full=false`; all eight schemas compile and byte-equal their goldens; 1 000 generated valid documents produce zero violations |
| V4-SP13-06 | **Exactly the eight §8.7 tools, in the design order** | `go test ./internal/mcp/ -run 'TestToolsList\|TestMCPConformance\|TestProxyAndDirectToolListsAreIdentical\|TestEveryToolRejectsUnknownArgument\|TestUnknownToolNameIsToolError\|TestDispatchReturnsErrToolNotFound' -v` ; `grep -R "t.Skip" internal/mcp` | names equal `["recall","expand","re_read","already_tried","record_eliminated","timeline","why","dropped"]` in that order; `tools-list.json` byte-equal; proxy and direct lists identical; an unknown tool name is a **tool error**, not an RPC error; every tool rejects an unknown argument with `invalid arguments for <name>:`; `mcptest.RunMCPSuite` green with zero skips |
| V4-SP13-07 | **The minimum-sufficient-span resolver** (§8.7) | `go test ./internal/mcp/ -run 'TestMinimalSpan\|TestSingleChunk\|TestFull\|TestExplicit\|TestSymbol\|TestLineAnchor\|TestNoWidenerIsTolerated\|PropertyNextSpanPaging\|PropertySpanNeverExceedsMaxResponse' -rapid.checks=1000 -v` | spans are chunk-aligned and ≤ 16 384 before widening; a single-chunk object returns whole; `full=true` returns the whole object to 262 144 then truncates with `NextSpan == "262144:137856"`; explicit byte and `L10-L20` line spans align outward; out-of-range clamps; a symbol anchor selects the enclosing function and widens to its end unless `spanWidenLines` forbids it; a nil widener is tolerated; **paging from offset 0 following `NextSpan` reconstructs the object byte-for-byte**; `End-Off ≤ MaxResponse` always |
| V4-SP13-08 | `recall` | `go test ./internal/mcp/ -run 'TestRecall' -v` | hits carry `^sha256:[0-9a-f]{64}$` hashes and summaries; default `k == 5`; selector prefixes parse into `store.Query{Text:"retry", Path:"src/*.ts", Symbol:"refreshToken", Tool:"FileRead", K:5}`; an empty result is `isError:false, count:0, found:false` |
| V4-SP13-09 | `expand` | `go test ./internal/mcp/ -run 'TestExpand' -v` | by `tool_use_id`, by root hash and by chunk hash all resolve; both-args and neither-arg rejected with `expand requires exactly one of hash or tool_use_id`; an unknown 64-hex hash is `found:false`, a malformed hash is `isError:true`; `full:true` returns the whole object; `span:"16384:16384"` pages correctly |
| V4-SP13-10 | `re_read` with historical `at` | `go test ./internal/mcp/ -run 'TestReRead' -v` | worktree first, store fallback when the file is deleted (`source` field distinguishes); `at` accepts an RFC3339 timestamp, a root hash and `turn:N`; `path:"src/auth.ts:refreshToken"` and `:400` anchor the span; `../../etc/passwd` ⇒ `isError:true` with `path escapes the project root`; an unknown path is `found:false`; a bad `at` yields the exact `at must be …` message |
| V4-SP13-11 | **`already_tried` — three-way, never a false positive** (G6.2, §8.3) | `go test ./internal/mcp/ -run 'TestAlreadyTried' -v` | `absent` on an empty ledger; `active` returns the reason and evidence; `stale` returns `Answer.Note` **verbatim** plus `stale_because`; `staleResponse:"drop"` ⇒ `absent`; a `BloomOnly` hit ⇒ `absent` with `meta["bloom_only"] == true`; **a ledger failure ⇒ `isError:false, state:"absent", degraded:true` and one `Loud`** — never `active`; the scope comes from `eliminations.defaultScope` |
| V4-SP13-12 | `record_eliminated` | `go test ./internal/mcp/ -run 'TestRecordEliminated' -v` | one ledger record with `Source == SourceMCP`, `Status == "active"`; the reason text is stored and its evidence root resolves to exactly that text; `depends_on` resolved to the newest roots with unresolved names reported; scope defaults from config; unknown scope, empty reason and oversize fields rejected; session scope without a session yields the exact `cannot record a session-scoped elimination` message with the ledger untouched; **`TestRecordEliminatedIsNotEphemeral`** — `Response.Ephemeral == false` and no ephemeral record written |
| V4-SP13-13 | `timeline`, `why`, `dropped` | `go test ./internal/mcp/ -run 'TestTimeline\|TestWhy\|TestDropped' -v` | timeline intersects `[from,to]` by turn and by RFC3339 timestamp, reports `frontier`, uses the greatest `EndTurn` (**not** `Frontier`) when `to` is empty, and errors on a bad bound; **`why` finds the decision in the latest checkpoint and searches the parent chain**, reporting `checkpoint_seq` and `searched_checkpoints`, with a nil reader reported as `available:false` rather than an error; `dropped` returns every `{kind,id,detail}` in order, reports `available:false` on a nil reporter, and turns a reporter error into a tool error with one `Loud` |
| V4-SP13-14 | **Ephemeral-at-birth** (§8.7, §12 re-inflation row) | `go test ./internal/mcp/ -run 'TestEveryRetrievalResponseCarriesEphemeralMeta\|TestEphemeral\|TestSyntheticToolUseIDIsDeterministic' -v` | all seven retrieval tools carry `_meta.qompack.ephemeral == true`; each writes `store.ToolUseRecord{Ephemeral:true, Tool:"mcp__qompack__<name>"}` carrying `Request.Turn`; `retrieval.ephemeralResults:false` disables both cleanly; a store failure on the ephemeral write does **not** fail the tool call; the synthetic tool-use id is deterministic in `(session, name, args, ts)` |
| V4-SP13-15 | Expansion promotion counting (feeds SP-16's Phase 7) | `go test ./internal/mcp/ -run 'TestNoteExpansion\|TestPromoter\|TestPromoted\|TestRecallDoesNotCountAsExpansion\|PropertyPromoterCountsMonotone' -rapid.checks=500 -v` | promotion fires at exactly `retrieval.promoteAfterExpansions` (2 by default, 3 when configured); it stays promoted; `Promoted` is deduplicated and promotion-ordered; state survives a restart; corrupt state is quarantined to `tmp/quarantine/` with one `Loud`; a threshold below 1 errors; `recall` never counts as an expansion; counts never decrease and `promoted ⟺ count ≥ threshold` |
| V4-SP13-16 | **Daemon wiring and the `mcp.server_registered` observable** | `go test ./internal/mcp/ ./internal/daemon/ -run 'TestWriteInitializedObservable\|TestObservableOverwritten\|TestInstallMCPOp\|TestMCPInitializedSeam\|TestInitializedWrites\|TestInitializedSurvivesHistoryWriteFailure\|TestDaemonMCPOp' -v` | `.qompack/state/mcp.json` written with `Initialized == true, Tools == 8`; `ipc.OpMCP` registered through `Options.Handle` and `Services.MCPInitialized` bound through `Options.Bind`; `contract.DeclareProducers` declares `CMCPRegistered`; **`state/history.json`** (`contract.HistoryPath(root)`) gains `mcp_initialized: true` idempotently — `state/contract.json` is the Monitor's own file and must be byte-unchanged by the handshake, since a `SessionHistory` written over it would destroy the persisted mode/reason/results — and the handler survives a history write failure; the daemon op dispatches tool calls, turns an unknown tool into a payload error, resolves the session from the registry by greatest `LastActivityTS`, degrades on an empty registry, and resolves the turn from the current open segment |
| V4-SP13-17 | `qompack mcp` transcoder never destabilizes the session | `go test ./internal/mcp/ -run 'TestCmdMCP' -race -v` | only JSON-RPC on stdout even at Debug logging; a missing daemon yields the exact `qompack daemon unavailable` tool error and the server keeps serving; retry stops when a listener appears with `Spawn` invoked exactly once and ≤ 10 attempts; **`TestCmdMCPNeverSpools`** — the client is handed a `nopSpool` and `.qompack/spool/` gains no file (a retrieval request must never be replayed later); a cancelled context returns promptly with no goroutine leak |
| V4-SP13-18 | MCP e2e over real stdio, and the standing-instruction agreement | `go test ./test/e2e/ -run 'TestStdioServerEndToEnd\|TestStandingInstructionsAgree' -v` | the real binary against a real daemon drives `initialize` → `notifications/initialized` → `tools/list` → `tools/call recall` → `tools/call expand`: eight tools listed, recall returns hits, expand returns a chunk-aligned span, `state/mcp.json` written. **`TestStandingInstructionsAgree` must no longer skip** — `mcp.StandingInstruction == rehydrate.StandingInstruction()` now that SP-11 has merged |
| V4-SP13-19 | Docs and manifest agreement | `go run ./tools/devtool gen-mcp-docs && git diff --exit-code -- docs/mcp-tools.md` ; `go test ./internal/mcp/ -run TestPluginValidateSeesEightTools -v` | no diff; `mcp.ToolNames()` has length 8 matching the §8.7 table |
| V4-SP13-20 | Import discipline and the `nomagic` set | `go run ./tools/devtool lint` ; `grep -rn "16384\|450\|12000\|10000" internal/mcp --include=*.go \| grep -v _test.go \| grep -v "nomagic:allow"` | `internal/mcp` imports only `store`, `negknow`, `checkpoint` plus the foundation — **no `symbols`, no `canon`, no `sketch`, no `ipc`, no `daemon`, no `rehydrate`** (the `Widener` port and the untouched `PutOptions.Canon` field are what keep the first two out); the grep returns nothing |
| V4-SP13-21 | **Budget B-F** (`00-ARCHITECTURE.md` §2.4) | `go test ./internal/mcp/ -run TestBudgetBF -v` | **p95 < `runtime.budgets.mcpToolCallMs` (250 ms)** over 200 `Dispatch` calls (100 `expand` minimal, 60 `recall`, 40 `re_read`) against the 2 000-tool-use / 40 MB fixture; `Registry.CheckBudgets(cfg)` reports no `B-F` breach; the failure message prints p50/p95/p99/max. Runs on ubuntu, macos **and** windows |
| V4-SP13-22 | MCP micro-benchmarks | `go test ./internal/mcp/ -bench . -benchmem -run '^$' -benchtime 2s` | `BenchmarkDispatchExpandMinimal`, `BenchmarkDispatchRecall`, `BenchmarkResolveSpanMinimal`, `BenchmarkToolsListMarshal` all tracked by `benchstat`; no > 25 % regression against `testdata/bench-baseline.txt`; `ResolveSpanMinimal` shows no allocation growth with object size |
| V4-SP13-23 | Placeholder scan and out-of-scope discipline | `grep -rnE 'TODO\|TBD\|FIXME\|XXX\|not implemented\|unimplemented' internal/mcp internal/daemon/mcpop.go internal/cli/cmd_mcp.go internal/cli/mcpwire.go tools/devtool/genmcpdocs.go docs/mcp-tools.md` ; `git diff --stat <v0.2.0>..HEAD` | the grep returns nothing and `core.ErrNotImplemented` no longer appears in `internal/mcp`; the diff shows no SP-13 edit under `internal/checkpoint`, `internal/rehydrate`, `internal/negknow`, `internal/store`, `internal/symbols`, `internal/scheduler`, `internal/commands` or `internal/analyzer` |
| V4-SP13-24 | Coverage floor | `go run ./tools/devtool cover` | `internal/mcp` ≥ **85 %** |

### 2.14 Whole-tree gates (main session, after all thirteen groups report)

| ID | Gate | Command | Expected |
|---|---|---|---|
| V4-ALL-01 | Full suite, race | `go test -race ./...` | exit 0 (ubuntu, macos) |
| V4-ALL-02 | Full suite, Windows repeat | `go test -count=2 ./...` | exit 0 (windows dev machine and CI) |
| V4-ALL-03 | Local CI | `go run ./tools/devtool ci-local` | exit 0 end to end |
| V4-ALL-04 | Coverage floors, all binding except three stubs | `go run ./tools/devtool cover` | ≥ 90 %: `config`, `paths`, `store`, `sketch`, `chunk`, `canon`, `negknow`, `redact`, `tokens`, **`checkpoint`**, **`pins`**. ≥ 85 %: `dag`, `eval`, **`rehydrate`**, **`scheduler`**, **`mcp`**. ≥ 75 %: everything else including `ipc`, `daemon`, `contract`, `observer`, **`rules`**, **`skills`**. Still exempt per `plans/OWNERS.tsv`: **`analyzer`, `grammar`, `commands` only** — a fourth exemption is a regression |
| V4-ALL-05 | Security posture | `go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...` ; `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps` | zero vulnerabilities; zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` (unix only); `os/exec` only in `internal/daemon`, `internal/cli`, `internal/testutil`, `tools/` |
| V4-ALL-06 | Placeholder scan across every implemented package | `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented\|handle edge cases' -- internal/ test/ tools/ ':!*_test.go'` | no output. `core.ErrNotImplemented` appears **only** in `internal/analyzer`, `internal/grammar` and `internal/commands` |
| V4-ALL-07 | Full CI on `verify/v4` | push the branch, watch GitHub Actions | all nine jobs green: `verify`, `test` (ubuntu/macos/windows), `cover`, `crossbuild`, **`bench-gate`** (required), **`replay-gate`** (required), `plugin-validate`, `security`, `docs`. Confirm neither gate carries `continue-on-error` |
| V4-ALL-08 | `Qompack.md` immutability | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |

---

## 3. Exit-criteria re-verification

Each criterion is **quoted verbatim** and paired with a concrete measurement procedure that runs
**now, on the integrated `develop`**, using SP-02's replay harness. A criterion a subplan proved on
its own branch against stubs or golden fixtures is not proven here until it is re-measured against the
real siblings. A quoted criterion that cannot be measured is a checkpoint failure, not a documentation
problem.

### 3.1 Phase 3 — checkpoint and rehydrate (SP-10 producing half, SP-11 consuming half)

Quoted verbatim from `Qompack.md` §10 Phase 3:

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

Three clauses, three assertions, all in `test/replay/phase3_rehydrate_test.go`, over the committed
24-session synthetic corpus (≥ 20, satisfying `eval.minSessions`) with `Deterministic: true`.

```
go test ./test/replay/ -run 'TestPhase3' -v
# No bare `--` before the flags (§1 rule 8) and no --json: the report flag is --out.
go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
  --baseline testdata/baseline/phase0.json --phase 3 --ci --out $TMP/v4-phase3.json
# .policies.<name> is a FLAT metric map (eval.MetricsOf): first_divergence_turn is a key of it,
# not a field of a nested .divergence object.
jq '.policies.stock.rehydration_tokens, .policies["qompack-rehydrate"].rehydration_tokens,
    .policies["qompack-rehydrate"].first_divergence_turn,
    .policies.stock.first_divergence_turn' $TMP/v4-phase3.json
# There is exactly one "stock" policy — the registered `stock` (internal/eval/policy.go) — and it is
# by definition what the Phase-0 number describes (test/replay/phases.go). SP-11's rewrite forbids a
# second "stock-restore" arm by name; a jq key that names one returns null and reads as zero.
```

These rows mirror `plans/V4-SP-11-rehydrator-l5.md`'s A-table and must be regenerated from it
whenever the subplan's gate is rewritten — the verification plan **re-runs** the subplan's gate, it
does not define a second one. There is exactly one "stock" policy: the registered `stock`
(`internal/eval/policy.go`), which is what the Phase-0 number describes (`test/replay/phases.go`).
SP-11's rewrite forbids an SP-11-defined `stockRestorePolicy` named `"stock-restore"` by name — two
definitions of "stock" in one gate is its own defect.

| Assertion | Definition | Pass condition |
|---|---|---|
| **A1 — "at a *smaller* rehydration budget than stock"** | `c.Driver.Policies["qompack-rehydrate"]["rehydration_tokens"]` vs `c.Driver.Policies["stock"]["rehydration_tokens"]`, over the 24-session synthetic corpus | `qompack < stock` on the corpus-wide **sum** (`Policies[…]["rehydration_tokens"]` is a sum, `internal/eval/score.go`), **and** `qompackRehydratePolicy` recorded no session whose `rehydrate.Build` `Result.Tokens` exceeded `runtime.rehydrate.maxTokens` (12 000) — the per-session maximum the policy records beside the sum, because no arithmetic on the sum can recover it. `c.Driver.BudgetViolations` is **not** that check: it fires on `eval.DefaultKeepBudget` (40 000) and any non-empty value already fails the whole run. Record the two sums and the per-session max |
| **A2 — "first-divergence turn index improves against Phase 0 baseline"** | `c.Driver.Policies[…]["first_divergence_turn"]` for `qompack-rehydrate` vs the registered `stock`, scored in the **same run** over the ≥ 20 sessions carrying a `CompactionAt` point | `qompack > stock` in the run, **and** the same invocation's `compare` step reports no regression on `stock.first_divergence_turn` against the committed `testdata/baseline/phase0.json` (`test/replay/gate.go`, §11.3's 2 % rule). The gate is the conjunction — the in-run head-to-head plus the committed-file verdict on the arm it is measured against — which is strictly stronger than reading the file, since the file cannot express the head-to-head at all. Record both numbers and the delta in turns |
| **A3 — "measured reduction in summarization-call input tokens"** | `residual` = Σ tokens of the turns in the half-open range (F, at], where `F` is SP-11's `frontierOf(s, at)` **replaced at V4 by the real `Ref.Frontier`** (see *What changes at V4* below); `stockSpan` = Σ tokens of every turn up to the compaction point | `residual ≤ core.Tokens(cfg.Checkpoint.Frontier.MaxResidualTokens)` (20 000) **and** `1 − residual/stockSpan ≥ 0.5` on the multi-compaction sessions. The driver surfaces it as `Policies["qompack-rehydrate"]["residual_span_p95"]`. Record the ratio per session |

**The honesty qualifier is normative and must survive.** §10's *"when the span instruction is
honoured"* is modelled by `Deterministic: true`, which assumes compliance. Non-compliance is
deliberately out of scope: §8.5 states `custom_instructions` is advisory and *"the rehydrator never
depends on the summary having complied"*, and §12 lists *"Summarizer ignores the incremental-span
instruction"* as an accepted Low risk. Confirm the test still carries the comment citing that
sentence; a removed comment, or an assumption silently upgraded to a claim of measurement, is a
checkpoint failure.

**What changes at V4.** SP-11 ran A1/A2/A3 with `Frontier` values from its own `frontierOf(s, at)`
helper — a synthetic frontier computed from the replayed session — because SP-10 was a same-wave
sibling. It did **not** read them from checkpoint goldens and could not have: `Frontier` is a field
of `checkpoint.Ref`, not of the on-disk `Checkpoint`, and no committed golden
(`testdata/golden/contracts/checkpoint/want/*.json`) carries one. At V4 `frontierOf` is replaced by
the real `Ref.Frontier` produced by SP-10's real
`Finalize` and, for the multi-compaction sessions, advanced by SP-12's real O5 loop. Derive the
frontier from `.qompack/state/precompact.json` — `Ref.Frontier` is writer-only and is zero on any
reader-returned `Ref` (`TestReaderRefLeavesWriterOnlyFieldsZero`) — and assert the number the test uses
is the number the writer produced.

§8.6's budget sentence — *"Default rehydration budget is deliberately far below Claude Code's 50K +
25K: target 8–12K"* — is discharged by `TestBuild_NeverExceedsMaxTokens`, `TestBuild_SmallerThanStock`,
`TestBuild_MinFillReadmitsUnits`, `PropBuild_MonotoneInBudget`,
`TestE2E_SessionStartCompactUnderBudget` and §4.1's live round trip. SP-10's own half is V4-SP10-21.

### 3.2 Phase 4 — scheduler (SP-12)

Quoted verbatim from `Qompack.md` §10 Phase 4:

> **Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).

```
go test ./test/replay/ -run 'TestPhase4_' -v
jq '.' .qompack/eval/phase4-rewrite.json ; jq '.' .qompack/eval/phase4-pause.json
```

| Test | Clause | Pass condition |
|---|---|---|
| `TestPhase4_RewriteTokensReduced` | "measured reduction in total rewrite tokens per session" | `Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock]` across all 24 sessions; per-session table in `.qompack/eval/phase4-rewrite.json` |
| `TestPhase4_NoDivergenceRegression` | "with no regression in divergence metrics" | `FirstDivergenceTurn`, `FileSetJaccard`, `DecisionPreservation`, `RedundantReads`, `ReAttempts` each regress **≤ 2 %** against `stock` (§11.3), with **no** `sign-off:` trailer available at a verification checkpoint |
| `TestPhase4_ResidualSpanFlatAsSessionGrows` | "residual span flat as session length grows (the amortization claim, tested directly)" | least-squares slope of `median(ResidualSpan)` against session turn count **≤ 0.02 tokens/turn**; longest-quartile median **≤ 1.25 ×** shortest-quartile median |
| `TestPhase4_ResidualUnderMaxResidualTokens` | §8.5's "10–20K tokens rather than 150K, regardless of how long the session has run" | `ResidualSpan.P95 ≤ 20 000` |
| `TestPhase4_CompactionPauseModelled` | "median compaction pause … flat", with the honesty qualifier | `PauseMS[i] == round(lat.PauseBaseMS + lat.PausePerKResidualMS × ResidualSpan[i] / 1000)` for every run, taking `lat` from `eval.DefaultLatencyModel()` rather than restating its coefficients — as shipped that is `3000 ms + 0.15 ms/residual-token`, i.e. 25 500 ms at a 150 000-token residual and 6 000 ms at 20 000; `.qompack/eval/phase4-pause.json` carries `"pause_modelled": true`. Deterministic replay makes no model call, so a "measured" wall-clock here would be exactly the dishonest measurement §1.3 RC-3 indicts; **the flatness claim is carried by residual span, which is real.** A lost flag or an upgraded claim is a checkpoint failure |
| `TestPhase4_PSelectionGateHonoured` | closing note 3, from both sides | with `DisablePSelection()` in effect `l3policy` still produces a `KeepSet` while `analyzer.NewSelector` refuses to construct |

**What changes at V4.** SP-12 ran these with `checkpoint.Writer` supplied by W-2 golden fixtures.
Re-run with SP-10's real writer behind `Advance`, so residual-span numbers come from segments the real
checkpointer encoded. Record whether the numbers moved, and by how much.

### 3.3 The four wave-3 subplans' own exit criteria

**SP-10.** Quoted from §11.3: *"Hook p99 latency < 15ms (L0), < 2s (L4)"*; and from §8.5: *"Note what
is **not** here: no code snippets. Files are pointers with a one-line reason."* The L4 half is B-E,
now a **real** measurement for the first time (§5.1) plus `BenchmarkFinalize` mean < 50 ms. The
no-snippets rule is `TestGoldenCheckpointsContainNoCodeBlocks` (V4-SP10-05) and
`TestAdvancePointersCarryNoContent` (a 40 KB read must yield a draft < 4 KB with the content substring
absent). SP-10's own gate is V4-SP10-21: residual span with frontier advancement on vs. off, ≥ 30 %
reduction, divergence within 2 %. Its three structural invariants each now carry live traffic:
`TestSourceSetCarriesNoText` (§13 inv. 1 — the alternative is uncompilable), `TestAdvanceIsDPIGuarded`
(§4.6, §8.2), `paths.TestAppendOnlyGuard` against `checkpoints/` and `pins/invariants.jsonl` (inv. 2).

**SP-11.** Quoted from §11.3 (the three bullets that bind this branch; the omitted one, *"Store growth
sublinear in session length after dedup"*, is SP-06's):

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

SP-11 adds no L0 and no L4 work, so its obligation is that B-A and B-E are *unchanged* — compare
against the V3 report's B-A p99, allowing noise but failing on a > 25 % move (§5.5). The 2 % rule is
§6.3; "every phase gate runs the full replay suite" is V4-SP02-06 at `--phase 4`. Local budgets are
V4-SP11-22. One item becomes checkable only at V4: a freshly built `develop` reports `ModeFull` **and**
`hook.additional_context_delivered` now reports a real `OK` rather than `not-yet-implemented`.

**SP-12.** Same three §11.3 bullets. `devtool bench-hotpath -n 2000` on all three platforms: **B-A p99
< 15 ms and unchanged from the pre-SP-12 baseline within noise**; `TestSchedulerNotOnHotPath`
(V4-SP12-20) proves structurally that no hook path reaches `Evaluate` — the tap runs daemon-side on the
worker pool, which is B-C. `replay-gate` green with no metric regressing more than 2 %. The eight
scheduler benchmark budgets are V4-SP12-23. `PSelectionAvailable()` must be **true** after
`NewSchedulerRuntime` succeeds and **false** where no runtime has been constructed, asserted from both
sides (V4-SP12-10), because SP-15 relies on that gate in wave 4.

**SP-13.** Quoted from §10 Phase 2: *"measurable reduction in repeated-elimination events in replay,
with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a
dependency change."* Quoted from §12: *"| Retrieval layer re-inflates the context window | Medium |
Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction
candidates (§8.7) |"*. Quoted from `00-ARCHITECTURE.md` §2.4: *"| **B-F** | `mcp_tool_call` — request →
response | p95 < 250 ms (`minimal` span) | CI |"*. Phase 2 is re-run whole (V4-SP09-07); SP-13's
contribution is the surface: `already_tried` returns `"stale"` — never `"active"` — for every record
whose `depends_on` hash changed in the `dependency-change-mid-session` fixture, and `"absent"` under
`staleResponse:"drop"` — **zero stale-block incidents attributable to the MCP layer** (V4-SP13-11,
§4.6). The re-inflation row is discharged by three independent mechanisms: ephemeral tagging
(V4-SP13-14), minimal-span defaults (V4-SP13-07), and the eviction ranking that puts ephemeral blocks
first (V4-SP12-11, §4.7). B-F is V4-SP13-21 and §5.1.

### 3.4 Phases 0, 1 and 2 must still hold

Wave 3 changed the runtime behaviour of every layer beneath it, so "V2 and V3 passed once" is not
evidence.

| Phase | Criterion (quoted) | Measurement now |
|---|---|---|
| **0** | *"a single number for stock behaviour, reproducible across at least 20 real sessions"* | V4-SP02-05: two `--write-baseline` runs byte-compared, both equal `testdata/baseline/phase0.json`; `sessions == 24`, `corpusTier == "synthetic"`. `docs/adr/0002-replay-methodology.md` must still state plainly that the committed number is synthetic-corpus and name the recorded-corpus command and its owner; a softened paragraph is a checkpoint failure |
| **1** | *"store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms"* | V4-SP06-07 (`DedupRatio ≥ 4.0` at store and observer level), V4-SP04-05 (`testrunner` gain ≥ 1.25, overall ≥ 1.0), V4-SP05-12 (B-A p99 < 15 ms with the full wave-3 resident set). Record both ratios against the V3 figures |
| **2** | *"measurable reduction in repeated-elimination events in replay, with zero stale-block incidents … on sessions that include a dependency change"* | V4-SP09-07: `stock_repeats > 0`, `negknow_repeats ≤ 0.75 × stock_repeats`, improvement on ≥ 8 sessions, `stale_blocks == 0` on every `DependencyChangeAt` session — now with `record_eliminated` and `already_tried` as real MCP surfaces rather than direct ledger calls |

### 3.5 The §11.3 guardrails and the §11.4 watch-fors

Quoted verbatim from `Qompack.md` §11.3:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

Discharged, in order, by: §5.1 B-A (hard gate on three platforms with checkpointer, rehydrator,
scheduler runtime, ledger and MCP server all resident); §5.1 B-E (**for the first time a real
`PreCompact`**, not the nil-service envelope V2 and V3 measured); §4.9 plus the `--growth` input to
§6.3's driver run (exponent ≤ 0.95, never `inconclusive`); §6.3; and V4-SP02-06 at `--phase 4`, where
five phase checks execute.

Quoted verbatim from `Qompack.md` §11.4:

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

Overfitting is materially more relevant now: wave 3 is the first wave whose policies *act* on the
corpus. V4-SP02-07 records whether `TestGate_CorpusStaleness` trips at `--phase 4`; a trip is a finding
to be resolved per `docs/adr/0003-replay-overfit-recollection.md`, never a gate to bypass. Bloom FP is
V4-SP03-05 and V4-SP09-05 plus the gate's hard `EstFPRate > 0.10` ceiling.

### 3.6 The §13 invariants, re-asserted with live wave-3 traffic

| Invariant (`00-ARCHITECTURE.md` §13) | Re-asserted by |
|---|---|
| 1. Never compress a compression | V4-SP10-06, V4-SP10-08, V4-SP06-02, V4-SP09-05, and §4.8 — the first end-to-end proof that a rehydrated injection is never a checkpoint source |
| 2. Append-only means append-only | V4-SP01-09, V4-SP10-02, V4-SP03-04, V4-SP12-16 (the scheduler's state files are correctly **not** append-only), §4.11 |
| 3. The bloom filter is a cache, never the source of truth | V4-SP09-05, V4-SP13-11 |
| 4. Nothing scattered before `p` | V4-SP12-10 and `TestPhase4_PSelectionGateHonoured`. The invariant itself is carried by `NewSelector`'s `Pos < p` check (`core.ErrBudget`), which is gate-independent and is what `TestGuard_SubmodularInertWithoutPSelection` asserts; the ship-order refusal beside it is a different rule. Record both `PSelectionAvailable()` readings and the gate-open assertion V4-SP12-10 adds in `internal/analyzer` |
| 5. No code snippets in checkpoints | V4-SP10-05, plus V4-SP11-11 for the rehydration payload's items 4–6 |
| 6. Hooks exit 0. Always. | V4-SP01-03, V4-SP05-10 (66/66), V4-SP10-15, §4.12 |
| 7. No network. No telemetry. No writes outside `.qompack/` | V4-SP05-14, V4-ALL-05, §4.11 |
| 8. Every constant §12 says might change is a config key | V4-SP01-02, V4-SP12-08 (`TestEvaluate_MultipliersReadFromConfig`), V4-SP11-05 |
| 9. Every latency budget is measured, not assumed | §5 in full — B-A, B-B, B-C, B-D, B-E and, newly, **B-F**, plus the reported-only **B-G** row `obs.Budgets()` carries after them |
| 10. Degradation is loud | V4-SP05-09, V4-SP10-16, V4-SP11-18, V4-SP12-19, §4.12 |

---

## 4. New cross-component integration tests

These tests **only make sense now**. Each exercises a seam that did not exist on any single wave-3
branch. They are authored during this checkpoint **in the main session** and **become part of the
permanent suite** — committed to `verify/v4` and run in CI from here on.

**Placement.** §4.1, §4.11, §4.12 and §4.13 drive the **real binary and a real daemon** and therefore
live in `test/e2e/v4_integration_test.go`; §4.14 lives there too because it needs a production-shaped
`Services`. The remaining nine live in `test/integration/v4_integration_test.go` (the package was
created at V2 and is already in `compositionRoots` in `tools/devtool/importrules.go`). Both
directories are composition roots and may import anything.
**Conventions:** `testify/require`; `testutil.NewProject(t)`; `testutil.FakeClock`; no `time.Sleep`;
deterministic and CI-safe. **Rule:** no test may reference `internal/analyzer`, `internal/grammar` or
`internal/commands` behaviour; where a wave-4 component would sit in the flow, compose the seam in
the test file and assert the halves fit.

### 4.1 `TestV4_PreCompactToCheckpointToRehydrateRoundTrip`

*Seams:* `cli` → `ipc` → `daemon` → `checkpoint.Compactor` → `checkpoints/0001.json` →
`observer.OnSessionStart(compact)` → `rehydrate.Build` → `additionalContext`. **This is the loop the
whole design exists to close, and it has never run.**

*Setup.* `testutil.NewProject(t)` seeded from `testdata/fixtures/rules/proj-a/`. Real binary, real
daemon started via `qompack session-start` (`source:"startup"`). Drive 60 events through the real
binary: 48 `observe tool` over `src/api/routes.ts`, `src/db/pool.ts`, `src/webhooks/retry.ts`;
9 `observe prompt`, the first being `"Fix the pgbouncer 1.18 pool bypass in the webhook retry"`;
3 `observe stop --subagent`. Record 2 eliminations through the real `record_eliminated` tool, add 2
pins, close 3 segments via `store.SegmentLog.Close`, run one idle tick so `advance_frontier` moves.

*Input.* `qompack checkpoint` with a real `PreCompact` payload, then `qompack session-start` with
`{"hook_event_name":"SessionStart","source":"compact","session_id":"v4-1"}`.

*Expected.* Both hooks exit **0**. `customInstructions` is non-empty and contains
`checkpoint.SentinelPhrase` plus the §8.5 standing paragraph. `checkpoints/0001.json` exists, is
read-only, and re-hashes to its MANIFEST line; its `Invariants` are the 2 pins verbatim, its
`UserIntent.Original` is the **first prompt byte-for-byte**, its `Eliminated` carries all seven §8.5
keys, no `Pointers.*.Why` contains a newline, and `EncodedSegments` names exactly the 3 closed
segments. `state/precompact.json` parses with `sentinel == "qompack checkpoint"`,
`timeout_ms == 20000`, `wall_ms < 2000`, `frontier > 0`, `span_instruction == true`. The
`additionalContext` opens `<!-- qompack:injected seq=1 ver=1 -->`, closes
`<!-- /qompack:injected -->`, contains `## 1. Invariants`, `## 2. Original user intent`,
`## 3. Approaches already eliminated`, `## 6. Pointers`, `## 6a. Restored instructions`,
`## 6b. Skill index`, `## 7. No longer in context` and `## 8. Retrieval`, and ends at that close tag.
The daemon's contract probe — `<!-- qompack-contract-probe <token> -->`,
minted by `contract.MintSentinel` from the session id and the mint timestamp — sits **after** that
close tag, not inside it: there is no `rehydrate.Sentinel` and the payload carries none.
**The budget:** its estimated token count is **≤ 12 000**
(`runtime.rehydrate.maxTokens`) and strictly < `50 000 + 25 000` — record the exact number, it is the
headline Phase-3 figure. `## 6a.` carries `.claude/rules/api-conventions.md` and `src/api/CLAUDE.md`
and **not** the project-root `CLAUDE.md`; `## 3.` ends with
`Before committing to an approach, call already_tried.`; `state/rehydrate-v4-1.json` has `seq == 1`
(and **no** `"sentinel"` key — the token lives in `History.Sentinel.Token` on the daemon side);
no decompressed object contains a secret literal.

### 4.2 `TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold`

*Seams:* `observer.Signals` → daemon scheduler tap → `Runtime.Observe` → `scheduler.Evaluate`.
§12: *"Soft floor well below the auto threshold; plugin acts first by design."*

*Setup.* Real store, DAG, observer and `scheduler.Runtime` over a `FakeClock`. Model a 200 K session:
`EffectiveWindow = 180 000` ⇒ `SoftFloor = 99 000`, `HardCeiling = 147 000`, **simulated stock
threshold 167 000** (§2.5). Drive tool uses behind the tap with `ContextTokens` growing monotonically,
completing todos at turns 30 and 62 and emitting a passing test at turn 78.

*Expected.* Some turn has `Decision.ShouldCompact == true` with `ContextTokens` **strictly below
167 000** — record the count and turn. `Reasons` is a prefix-ordered subset of
`["soft_floor","changepoint","young_daly","hard_ceiling","idle_cold_cache"]` containing `soft_floor`
and at least one of `changepoint`/`young_daly`. `Decision.P.Pos` appears in `Inputs.Candidates`, is a
`RoundBoundary` (or `Breakdown["round_boundary_relaxed"] == 1`), and `dag.CrossingEdges(P.Pos)`
equals `P.Coupling`. `Breakdown` carries `reclaimable`, `rewrite`, `distortion`, `candidates`,
`context_tokens`, `window_source`, `lambda`, with `PScore == reclaimable − rewrite − distortion`
(±1e-9). The first firing is `UrgencyAdvisory`, not `UrgencyNow`; driving on to 150 000 tokens yields
a later `hard_ceiling`/`UrgencyNow` decision. **Negative control:** with `softFloorPct = 0.95` the
first firing moves above 147 000 and the primary assertion fails — proving the test measures the soft
floor and not the fixture.

### 4.3 `TestV4_O1SpanInstructionFromARealCheckpointFrontier`

*Seams:* `store.SegmentLog` → `checkpoint.Advance` → `Draft.Frontier()` → `FocusInstructions` →
`HSO.CustomInstructions`. V2/V3 could only test `FocusInstructions` against a hand-supplied frontier;
the O1 claim is that the number in the instruction is the number the store actually encoded.

*Setup.* Real store and writer. Open a segment, drive 58 turns through the observer, close at turn 58,
`Begin` a draft and `Advance` over that segment.

*Expected.* `d.Frontier() == 58` **and** `store.SegmentLog.Frontier(ctx, sess) == 58` — writer and log
agree, and the log's frontier is the contiguous encoded prefix, not the maximum encoded id. The
emitted instruction contains verbatim
``A durable checkpoint (`.qompack/checkpoints/0001.json`) fully covers the session through turn 58``
and `Summarize only what happened after turn 58`; its first paragraph is byte-identical to
`focus/standing.txt`'s first paragraph (a paraphrase silently breaks the §12.1
`precompact.custom_instructions_accepted` probe); the path uses forward slashes on Windows; the whole
string is ≤ 4 000 bytes. With `checkpoint.incrementalSpanInstruction = false` the span paragraph is
absent and the standing paragraph plus snippet prohibition remain. With a frontier of 0 the span
paragraph is absent — the instruction never claims coverage it does not have. Advancing a second
segment (turns 59–92) regenerates an instruction naming turn 92: the frontier moves and the
instruction follows it.

### 4.4 `TestV4_FrontierAdvancementKeepsResidualSpanODelta`

*Seams:* scheduler idle `act.advance_frontier` → `checkpoint.Writer.Advance` →
`store.SegmentLog.MarkEncoded` → residual accounting. This is §8.5's O5 amortization claim —
*"Per-compaction cost drops from O(session) to O(delta)"* — which neither branch could measure alone.

*Setup.* Real store, DAG, observer, writer and `Runtime` with a `FakeClock`. A synthetic session
grown to **1 200 turns**, closing a segment on every todo completion (~every 40 turns) with one idle
tick after each close. Sample `(turnCount, residualTokens, encodedSegments, draftBytes)` at turns
100, 200, 400, 600, 800, 1 000, 1 200.

*Expected.* `residualTokens` at turn 1 200 is **≤ 1.25 ×** its value at turn 200 — flat, not growing
with session length. `residualTokens ≤ 20 000` (`checkpoint.frontier.maxResidualTokens`) at every
sample. The least-squares slope against `turnCount` is **≤ 0.02 tokens/turn**, the same threshold
`TestPhase4_ResidualSpanFlatAsSessionGrows` uses, asserted here against live segments.
`encodedSegments` grows monotonically and `Unencoded` holds only open segments at every sample.
**Negative control:** with `advanceOnSegmentClose = false`, `residualTokens` at turn 1 200 is **≥ 4 ×**
its value at turn 200 and at least one sample exceeds 20 000 — the O(session) behaviour the claim
replaces. Record both curves. Throughout, no `Advance` returns `core.ErrAlreadyEncoded`; injecting
one out of band makes the scheduler drop that batch, emit exactly one `Loud`, retry with the
remainder, and **never re-encode**.

### 4.5 `TestV4_TombstoneToRecallToExpandRoundTrip`

*Seams:* `observer.Tombstone` → `store` → `mcp.recall` → `mcp.expand` → `store.OpenSpan` →
`symbols.Enclosing` through the `Widener` port. V2 proved the hash resolves through `store.Open`;
G3.1 and G3.2 are only closed when a **tool call**, not a Go function call, gets the content back.

*Setup.* Real store, observer and MCP server (`mcp.NewServer` + `RegisterAll` with real `ToolDeps`).
Ingest `src/auth.ts` (24 KB TypeScript, three functions including `refreshToken`) as
`tool_use_id: "toolu_v4_auth"`.

*Expected.* `observer.Tombstone(rec)` matches
`^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$` with the 12
hex chars equal to `rec.Root.Short()`. `recall {"query":"path:src/auth.ts refreshToken"}` returns a
hit whose `hash` parses to `rec.Root` — **the hash in the tombstone is the hash the agent gets back**.
`expand {"tool_use_id":"toolu_v4_auth"}` is `found:true` with a chunk-boundary-aligned body ≤ 16 384
bytes before widening. `expand` with a span anchored inside `refreshToken` widens to the enclosing
function, returning its complete text including the closing brace with `_meta.qompack.widened == true`.
`expand` with `full:true` equals `io.ReadAll(store.Open(ctx, rec.Root))` byte-for-byte. Following
`_meta.qompack.next_span` from offset 0 on a 400 KB object reproduces it exactly. `internal/mcp` still
does not import `internal/symbols`.

### 4.6 `TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction`

*Seams:* `negknow.Ledger` → `rehydrate` items 3 and 8 → the injected standing instruction →
`mcp.already_tried`. §8.7's design note names a two-package contract that first exists at V4:
*"`already_tried` should be surfaced … as a standing instruction … Otherwise the affordance exists
and goes unused."*

*Setup.* Real store, ledger, graph, rehydrator and MCP server over one project;
`rebuildOnStale = "nextIdle"`, `staleResponse = "flag"`. Ingest 23 eliminations through
`record_eliminated`, one being `{target:"src/auth.ts:refreshToken", approach:"widen pool timeout",
reason:"pgbouncer 1.18 ignores it in transaction mode", scope:"project",
depends_on:["docker-compose.yml","package-lock.json"]}`. Write a checkpoint and rehydrate.

*Expected.* `## 3.` renders exactly `eliminationsTopN` (8) records by slice score followed by
`15 further eliminations are recorded and not shown.`; the payload carries
`Before committing to an approach, call already_tried.` verbatim and
`rehydrate.StandingInstruction() == mcp.StandingInstruction`; `## 8.` names `recall`, `expand`,
`re_read`, `already_tried`. **Absent:** an unrelated target ⇒ `{"state":"absent"}`. **Active:** the
synonym phrasing `"Increasing the connection-pool timeouts"` resolves through `ApproachClass` ⇒
`state:"active"` with the recorded reason byte-identical and a resolvable `evidence`. **Stale:**
rewriting `docker-compose.yml` through the observer and running `RefreshStaleness` makes the same
query ⇒ `state:"stale"`, `note == negknow.StaleNote` **including the em dash**, `stale_because`
naming `docker-compose.yml` — **never `active`**, which is the zero-stale-block-incident requirement
at the MCP surface. After the next idle rebuild, rehydrating again renders the stale record with the
same note; under `staleResponse:"drop"` it is absent from the payload and `already_tried` answers
`absent`. `tried.bloom` has exactly one `.bak` and was written only by `negknow.RebuildBloom`.

### 4.7 `TestV4_EphemeralRetrievalResultsRankFirstForEviction`

*Seams:* `mcp` ephemeral tagging → `store.ToolUseRecord{Ephemeral:true}` → `daemon.ClassifyDrop` →
`scheduler.DropClassOf` → `reclaimableIndex` → `Candidate.ReclaimableTokens`. §8.7: *"Every retrieval
result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary
tool results."* All three layers exist for the first time.

*Setup.* Real store, MCP server and scheduler runtime. Ingest 20 ordinary `FileRead` results and 2
`Task` results, mark 3 reads superseded, then issue 6 `expand` calls through the MCP server.

*Expected.* Each `expand` writes `ToolUseRecord{Ephemeral:true, Tool:"mcp__qompack__expand"}` and
carries `_meta.qompack.ephemeral == true`. `ClassifyDrop` returns `DropEphemeral` for the 6,
`DropSuperseded` for the 3, `DropOrdinary` for the 17 and `DropNone` for the 2 `Task` records. The
assembled droppable list sorts ephemeral → superseded → ordinary and `EvictionRank` gives
`3 > 2 > 1 > 0`. `reclaimableIndex.After(p)` counts all three droppable classes and **excludes** the
`DropNone` blocks at every `p`. A candidate positioned before the 6 ephemeral records has strictly
greater `ReclaimableTokens` than one after them, and the difference equals the sum of their `Tokens`.
With `retrieval.ephemeralResults = false` both the `_meta` flag and the ephemeral record disappear and
the same six results classify as `DropNone` — the ranking is driven by the flag, not the tool name.
The ephemeral records are **not** promoted into the next checkpoint's pointer tier.

### 4.8 `TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint`

*Seams:* `rehydrate.Wrap` → the transcript → `observer.OnUserPrompt` → `store` →
`checkpoint.StripInjections` → `checkpoint.Advance`. §8.5's regeneration rule — *"The rehydrator tags
every injection with its checkpoint sequence number precisely so the next checkpoint pass can
identify and ignore that material as a source"* — is the back-door DPI defence, and this is the first
wave in which tagger, stripper and a second checkpoint all exist.

*Setup.* Continue from §4.1: `0001.json` written, rehydration payload emitted.

*Input.* Feed the **exact emitted `additionalContext`** back through `observer.OnUserPrompt` at turn
61, plus three ordinary prompts at turns 62–64 and 20 tool uses. Close a segment over turns 61–84,
`Begin` a draft with `parent = 1`, `Advance`, `Finalize` into `0002.json`.

*Expected.* `0002.json`'s `UserIntent.Original` is **still the turn-0 prompt, byte-for-byte**. No
decoded string anywhere in `0002.json` contains `qompack:injected`, `/qompack:injected`,
`## 8. Retrieval`, `## 7. No longer in context` or `qpk-ac-` — assert by walking every string value,
not by a raw substring search alone. `UserIntent.Evolution` holds the three ordinary prompts and not
the turn-61 injection. `StripInjectionsCount` reports exactly 1 removal on the turn-61 text and the
residue is whitespace, contributing no evolution entry. A *mangled* residue (bare
`qompack:injected seq=1 ver=1`) is still stripped. `0002.json`'s `parent == "0001.json"`, its
`encoded_segments` is disjoint from `0001.json`'s, and `MarkEncoded` on any `0001` segment at seq 2
returns `core.ErrAlreadyEncoded`. **The compounding check:** repeat the cycle once more into
`0003.json` and assert its `UserIntent.Original` is *still* the turn-0 prompt byte-for-byte — three
generations, zero drift; §4.6's "never compress a compression" demonstrated rather than asserted.

### 4.9 `TestV4_GrowthGuardrailWithCheckpointsAndEphemerals`

*Seams:* `observer` + `mcp` → `store.Stats` → `eval.CheckSublinearGrowth` → `--growth`.

*Setup.* Real store and observer; `eval.Synthesize(0x51080001, readHeavy)` (400 turns) driven through
`observer.OnToolUse`, **plus** 40 `expand` calls and 4 checkpoint finalizations interleaved.

*Expected.* `RawBytes` non-decreasing in `Turn`; ≥ 6 usable samples over an ≥ 8× raw span;
`Sublinear == true` with `Exponent ≤ 0.95`; the driver run with `--growth $TMP/v4-growth.json` exits
0. **The ephemeral writes and checkpoint artifacts must not push the exponent above the threshold** —
if they do, that is a real finding about retrieval re-inflation on disk and belongs in the completion
report, not in a relaxed threshold. Negative control: a synthetic linear series still yields
`Sublinear == false` and a non-zero driver exit.

### 4.10 `TestV4_WhyAndDroppedAnswerFromRealProducers`

*Seams:* `ExtractDecisions` → `checkpoint.Reader` → `mcp.why`; `rehydrate.Reporter` →
`mcp.DropReporter` → `mcp.dropped`. This is the Rule W-2 reconciliation SP-13's exit criteria defer
to V4 — on its branch SP-13 used a `fakeReader` and a `fakeReporter`.

*Setup.* §4.1's project after two real checkpoints and one real rehydration.

*Expected.* `why` on the first `Decision.ID` from `0002.json` returns `found:true` with `what`, `why`,
`alternatives_rejected`, `evidence` and `turn` equal to the checkpoint's values field-for-field. A
decision present only in `0001.json` is found via the parent chain with `checkpoint_seq: 1` and
`searched_checkpoints` including 2. `dec_ffffffffffff` ⇒ `found:false` with a non-empty
`searched_checkpoints`; an empty id ⇒ `isError:true`. `dropped {}` returns exactly the entries in
`state/rehydrate-v4-1.json`'s `dropped[]`, in order, each `{kind,id,detail}` unchanged — including
the collapsed elimination entry whose `Detail` reads
`15 of 23 not shown; call already_tried(target, approach) or dropped()`. After
`SessionStart(source="clear")`, `dropped` reports `available:false` or an empty list, never a stale
report. Compare against the fixtures SP-13 used: **any divergence is a verification failure**, resolved
by fixing the implementation, or — only if the fixture is provably an SP-01 placeholder — under §7.1
class (c) with the measured values in the commit body.

### 4.11 `TestV4_LiveSessionWriteSetAppendOnlyAndImmutability`

*Seams:* everything — §13 invariants 2 and 7 over the whole live pipeline, now including
`checkpoints/` and `pins/`.

*Setup.* `testutil.NewProject(t)` inside a temp `HOME`; snapshot the project tree and `HOME` before.

*Input.* A 300-event session through the **real binary and real daemon**: session-start, 200
`observe tool`, 16 `observe prompt`, 4 `observe stop --subagent`, 24 MCP calls across all eight tools,
6 `record_eliminated`, one `RefreshStaleness` + `RebuildBloom` cycle, **three `PreCompact`
invocations** each followed by a `SessionStart(source=compact)`, 8 idle ticks, `flush`.

*Expected.* Every created or modified path is under `<projectRoot>/.qompack/` or `<home>/.qompack/`;
nothing else on disk changed. `p.AssertAppendOnly(t)` passes, and each of these fails with
`core.ErrAppendOnly` or `os.ErrExist`: `O_TRUNC` on `index/tool_use.jsonl`, `index/roots.jsonl`,
`index/segments.jsonl`, `dag/deps.jsonl`, `records/eliminations.jsonl`, `pins/invariants.jsonl`,
`spool/*.ndjson`; `WriteAtomic` onto `sketches/tried.bloom`; **`CreateNew` twice on
`checkpoints/0001.json`**; any write to a finalized checkpoint. `MANIFEST.jsonl` has exactly three
lines with ascending seqs and `Reader.Verify` returns an empty mismatch list. `state/bocd.json`,
`state/scheduler.json`, `state/precompact.json`, `state/rehydrate-*.json`, `state/mcp.json` and
`state/contract.json` are **atomically rewritten, not appended** — `paths.AppendOnly` is never invoked
for them and a temp file appears under `.qompack/tmp/` and is renamed. `store.GC` ran on `flush` and
**deleted nothing**: every checkpoint pointer, pin and elimination evidence hash still resolves. Zero
network syscalls.

### 4.12 `TestV4_DegradedPassiveWithEverySubsystem`

*Seams:* `contract.Monitor` → daemon mode enforcement → observer, ledger, checkpointer, rehydrator,
scheduler, MCP. §12.1's degraded-passive clause names exactly which subsystems keep running and which
stop; wave 3 supplies every "acting" subsystem it turns off.

*Setup.* Real daemon over real store, graph, ledger, writer, rehydrator, scheduler runtime and MCP
server. Force `contract.Monitor.Degrade("v4 test", …)`.

*Input.* 60 `observe tool`, 4 `observe prompt`, 1 `observe stop --subagent`, 3 `IngestMCP`
eliminations, 6 MCP calls (`recall`, `expand`, `already_tried`, `timeline`, `why`, `dropped`), one
`PreCompact`, one `SessionStart(source=compact)`, 4 idle ticks, `flush`.

*Expected.* Every hook exits 0. **Recording continues:** 65 lines in `index/tool_use.jsonl`, a
non-empty `dag/deps.jsonl`, `touch.cms` and `explore.hll` written, 3 `add` lines in
`records/eliminations.jsonl`, `tried.bloom` written by the ledger and only the ledger. **Acting
stops:** every `HookSpecificOutput` is nil — **no `additionalContext` and no `customInstructions`** —
`rehydrate.Build` is never called (spy), no `state/rehydrate-*.json` is written, and the cadence tick
writes no checkpoint. **But an explicit `PreCompact` still writes the artifact** —
`checkpoints/0001.json` exists — because losing the durable record would make the session *worse*,
which §7.1 forbids. `act.advance_frontier` is absent from `RunOnce`'s `ran` while `drain`, `sketches`,
`metrics`, `negknow.maintain`, `precompute_slice`, `refresh_delta`, `rebuild_bloom`, `compact_dag`
and `gc` are present. **MCP retrieval stays available** — all six calls return `isError:false` —
because §12.1 says pull-based tools "cannot make anything worse". `LOUD.log` has exactly one
degradation line and `state/contract.json` carries the reason and `DegradedSince`. Two clean `RunAll`
cycles restore `ModeFull` with a `Loud` line, and the next `SessionStart(compact)` emits a full
payload again.

### 4.13 `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet`

*Seams:* `ipc` client → daemon holding store, DAG, sketches, ledger, **scheduler runtime, BOCD
posterior, checkpoint draft and MCP server**. §13 invariant 9: *"Adding work to L0 without a bench
result is a review rejection."*

*Setup.* `test/bench/hotpath` against a warm daemon pre-populated with 2 000 tool uses, 40 MB of raw
output, a ~5 000-node / ~15 000-edge DAG, the real sketch set, a ledger with 5 000 active
eliminations and a rebuilt `tried.bloom`, a scheduler runtime with a 512-entry posterior and 32
candidates, and an open checkpoint draft advanced over 30 segments.

```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v4-hotpath.json
go run ./tools/devtool bench-hotpath --iterations 200 --warm-daemon --json v4-be.json   # no --hook: B-E is measured on every run
```

*Expected.* **B-A p99 < 15 ms**, **B-B p99 < 2 ms**, **B-E p99 < 2 s**, `pass: true` for each gated
budget; `hotPathMode` never transitions to `spool` (`state.bin` still reports `hot=0`, no WARN
transition line); B-D and `spawn_floor_ms` recorded. **Compare B-A p99 against the V3 completion
report's figure: a regression greater than 25 % fails this checkpoint even if the absolute number is
under budget** (§7 benchstat policy applied to the hot path).

### 4.14 `TestV4_EveryContractAssertionHasARealProducer`

*Seams:* `daemon.DeclareProducers` → `contract.Monitor` → all nine assertions.

*Setup.* A real daemon built from the merged tree with `Services` populated as production populates it.

*Expected.* `contract.HasProducer(id)` is true for **all nine** ids — `session_start.fires`,
`session_start.source_compact`, `hook.additional_context_delivered`, `precompact.has_time_to_write`,
`precompact.custom_instructions_accepted`, `hook.payload_shape`, `mcp.server_registered`,
`transcript.readable`, `plugin.root_resolves`. `RunAll` returns `ModeFull` with **zero** results at
`Observed == "not-yet-implemented"`. Each newly-produced assertion evaluates against its real
observable: the sentinel round trip through `state/rehydrate-<sess>.json`; `wall_ms`/`timeout_ms` from
`state/precompact.json`; the sentinel-phrase probe against the emitted `customInstructions`;
`MCPInitialized` from `state/history.json` (`contract.HistoryPath`, which is what `checkMCPServerRegistered` reads — not `state/contract.json`) after a real `initialize`. Removing any one producer from
`Services` returns that assertion to `OK:true, SevInfo, not-yet-implemented` and **never** degrades
the session — §12.1's rule still holds, it simply no longer fires in production.
`TestDeclaredProducerSetMatchesArchitecture` reports nine/zero; five/four is a wiring regression, not
a passing legacy state.

### 4.15 Test registration and CI wiring

In the same commit that adds `test/integration/v4_integration_test.go` and
`test/e2e/v4_integration_test.go`: confirm `test/integration` and `test/e2e` are still in
`compositionRoots` in `tools/devtool/importrules.go`; confirm `go run ./tools/devtool lint` still
passes (`importgraph`, `testdeps`, `bindeps`, `sleepcheck` — in particular no new `time.Sleep` outside
`test/bench`); confirm the `test` CI job picks both new files up on all three OSes and that none of the
new tests is skipped on Windows; record every new test name in the §8 completion report.

---

## 5. Performance budget validation

Every latency, size and ratio budget **in force at this point**. Measure on **one quiet machine** so
the numbers are comparable; the three-OS columns are filled from the `bench-gate` and `test` CI jobs
on `verify/v4`. Append results to `testdata/bench-baseline.txt` and diff with `benchstat`.

At V4 there is **no `N/A` row left in the §2.4 table**: B-F's producer landed with SP-13 and B-E's
producer landed with SP-10. Every budget the architecture names is now measurable, and every one is
recorded. The table below carries a seventh row, **B-G**, which is *not* a §2.4 budget: `obs.Budgets()`
ships it after B-F for the degraded spool append, reported-only. It is listed here so the checkpoint
records every row that table returns rather than the six §2.4 names alone.

### 5.1 Normative latency budgets (`00-ARCHITECTURE.md` §2.4, `Qompack.md` §8.1 / §11.3)

| ID | Clock | Threshold | Command | linux | macos | windows | Gate |
|---|---|---|---|---|---|---|---|
| **B-A** | `hook_controlled`: client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v4-hotpath.json`, warm with the **full wave-3 resident set** (§4.13) | | | | **Hard fail** |
| **B-B** | `l0_ingest`: daemon read → WAL append returned | p99 < 2 ms | same run; plus `go test -bench BenchmarkIngestAccept -run '^$' ./internal/daemon/` | | | | **Hard fail** |
| **B-C** | `l0_process`: WAL → chunked, stored, DAG/sketches updated, **plus the scheduler tap** (async) | p99 < 50 ms (**soft**) | `go test -bench 'BenchmarkOnToolUse_FileRead64KB\|BenchmarkOnToolUse_TestOutput256KB' -run '^$' ./internal/observer/` ; `go test -bench BenchmarkSchedulerTap_ObserveTool -run '^$' ./internal/daemon/` ; `go test -bench BenchmarkPutBytes_100KB_Cold -run '^$' ./internal/store/` | | | | Reported. Overrun ⇒ sampling + backpressure, never blocking |
| **B-D** | `hook_wall`: includes host process creation | reported, never gated | same bench run; `spawn_floor_ms` and `b_a_method` present | | | | No — it is the host's cost and gating it would be dishonest |
| **B-E** | `checkpoint_finalize`: `PreCompact` entry → exit | **p99 < 2 s** | `go run ./tools/devtool bench-hotpath --iterations 200 --warm-daemon --json v4-be.json` ; plus `go test -bench BenchmarkFinalize -run '^$' ./internal/checkpoint/` | | | | **Hard fail. First real measurement** — `Services.PreCompact` is SP-10's `Compactor`, not nil |
| **B-F** | `mcp_tool_call`: `Dispatch` entry → `Response` returned | **p95 < 250 ms** (`minimal` span) | `go test ./internal/mcp/ -run TestBudgetBF -v` — 200 calls (100 `expand` minimal, 60 `recall`, 40 `re_read`) against the 2 000-tool-use / 40 MB fixture | | | | **Hard fail. In force for the first time** (N/A at V2 and V3) |
| **B-G** | `hook_degraded`: the synchronous spool append inside `ipc.Client.Send` when the daemon cannot take the event | p99 < `runtime.budgets.hookDegradedMs` (default 1 000 ms) | `go test ./internal/ipc/ -run TestDegraded -v` ; `go test ./internal/obs/ -run TestBudgets_BGCoversTheDegradedSpoolAppend -v` | | | | **Reported only, and structurally so** — `Budgets()` ships it `Gated: false` because a sample and an evaluator can never coexist: `hook_degraded` is written only by a hook process's own Registry (never persisted), while `CheckBudgets`' only production caller is the resident daemon, and a B-G sample exists only when that daemon is unreachable. Record the number; a `Gated: true` here would be a claim nothing backs |

**B-E annotation, and why it changes at V4.** V2 and V3 both recorded B-E with the honest note *"the
checkpoint writer is SP-10 (wave 3), so this measures the hook envelope"*. Delete that annotation
from the V4 report. Record instead: the number of segments in the finalized draft, whether the run
took the cold path (`checkpoint.cold_precompact`), and whether `Truncated` was set. A B-E that
passes only because the draft was empty is not a pass; drive at least 30 encoded segments,
64 decisions and 400 pointers.

### 5.2 Local (non-§2.4) latency budgets introduced by wave 3

| Budget | Definition | Threshold | Command | linux | macos | windows |
|---|---|---|---|---|---|---|
| `L5-BUILD` | `rehydrate.Build` wall time, warm caches | p99 < **250 ms** | `go test -bench BenchmarkBuild -run '^$' ./internal/rehydrate/` | | | |
| `L5-RULES` | `rules.PathScoped` over a 2 000-file tree, 24 rules, 40 pointers | p99 < **50 ms** | `go test -bench BenchmarkPathScoped -run '^$' ./internal/rules/` | | | |
| `L5-SKILLS` | `skills.Index` over 60 skills | p99 < **20 ms** | `go test -bench BenchmarkIndex -run '^$' ./internal/skills/` | | | |
| `L5-SESSIONSTART` | `qompack session-start` wall time, `source=compact`, warm daemon | p99 < **1.5 s** (10 % of the 15 s manifest timeout) | `go test ./test/e2e/ -run TestE2E_SessionStartLatency -v` | | | |

### 5.3 Size, token and ratio budgets (`Qompack.md` §8.5, §8.6, §10, §11.3)

| Budget | Threshold | Command | Status |
|---|---|---|---|
| **Rehydration payload** | **8 000 ≤ `Result.Tokens` ≤ 12 000**, and strictly < 50 000 + 25 000 | `go test ./internal/rehydrate/ -run 'TestBuild_NeverExceedsMaxTokens\|TestBuild_SmallerThanStock\|TestBuild_MinFillReadmitsUnits' -v` ; §4.1 step 5 | **In force. New at V4** — §8.6's "target 8–12K" becomes a measured number for the first time |
| **Checkpoint on disk** | `checkpoint.budgetTokens` = 12 000 respected; tier 1 never truncated | `go test ./internal/checkpoint/ -run 'TestTruncate' -v` ; `jq '.' .qompack/checkpoints/0001.json` | **In force. New at V4** |
| **Residual span at compaction** | `ResidualSpan.P95 ≤ 20 000` (`checkpoint.frontier.maxResidualTokens`) | `go test ./test/replay/ -run TestPhase4_ResidualUnderMaxResidualTokens -v` ; §4.4 | **In force. New at V4** |
| **Store dedup ratio, read-heavy** | **≥ 4:1** | `go test ./internal/store/ -run TestPhase1ExitCriterion_ReadHeavy -v` ; `go test ./test/dedup/ -run TestDedupRatio_WithVsWithout -v` | In force |
| **Canonicalization gain** | `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0` | `go test -v ./test/dedup/` | In force |
| **Store growth sublinear** | OLS exponent `α ≤ 0.95`, never `inconclusive` | §4.9, then `--growth $TMP/v4-growth.json` | In force |
| **Rewrite tokens per session** | `Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock]` | `go test ./test/replay/ -run TestPhase4_RewriteTokensReduced -v` | **In force. New at V4** |
| **`tried.bloom` on disk** | 11 264–14 336 B at Appendix C defaults below capacity | `go test ./internal/negknow/ -run TestBloomFileSize -v` | In force |
| **Bloom FP rate** | empirical ∈ [0.008, 0.013] at capacity; `EstFPRate < 0.02` at 8 000 records; hard ceiling 0.10 | `go test ./internal/sketch/ -run TestBloom_EstimatedFPRateMatchesEmpirical -v` ; `go test ./internal/negknow/ -run TestRebuildBloom_Resizes -v` | In force |
| **Ledger resident memory** | < 4 MB at 5 000 records | `go test ./internal/negknow/ -run TestMemoryFootprint -v` | In force |
| **Sketch sizes match Appendix A** | bloom 11 984 B body (m = 95 872, k = 7); CMS 54 380 B (2719 × 5); HLL 2 102 B frame | `go test ./internal/sketch/ -run 'AppendixASizing\|AppendixSizing' -v` | In force |
| **MCP bounded I/O** | a single `expand`/`re_read` reads ≤ 557 056 B worst case, ≤ 49 152 B in the anchorless minimal case | `go test ./internal/mcp/ -run 'PropertySpanNeverExceedsMaxResponse\|TestFullTruncatesAtMaxResponseBytes' -rapid.checks=1000 -v` | **In force. New at V4** — this bound, not the 250 ms, is what makes B-F achievable on a 40 MB store |

### 5.4 Component micro-budgets (all in force; `benchstat` gated at > 10 % warn / > 25 % fail)

**Carried forward from V2/V3 and re-run unchanged.** Every row of V2 §5.3 and V3 §6.3 is re-run:
`chunk.Split` 100 KB < 800 µs; gear scan 1 MiB ≥ 400 MB/s; `Split` 1 MiB ≥ 120 MB/s ≤ 2 allocs;
`SplitStream` 4 MiB ≤ 40 ms; `RootHash` 1 000 chunks < 40 µs; `canon.Run` bash 100 KB < 3 ms,
go-test < 1 ms; `canon.Restore` 100 KB < 1 ms; `symbols.Extract`/`Enclosing` 100 KB < 2 ms;
`symbols.References` < 1 ms; **`L0SketchUpdate` ≤ 5 µs, 0 allocs**; Bloom/CMS/HLL single ops ≤ 1.0 µs
0 allocs; `HLL.Cardinality` ≤ 25 µs; `MisraGries.Add` ≤ 5 µs; `MinHash` 4 KiB ≤ 1.5 ms /
100 KiB ≤ 2.5 ms; sketch marshal ≤ 60 µs / CMS ≤ 250 µs; `RebuildBloom` 5 000 ≤ 15 ms;
`store.PutBytes` 100 KB cold ≤ 3 ms / warm ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan` ≤ 150 µs;
`Search` 1 000 roots ≤ 25 ms; `OpenStore` 50 k roots ≤ 400 ms; `MarkEncoded` 100 ≤ 1 ms;
`GC` 50 k objects ≤ 2 s (deadline ±50 ms); `redact.Redact` 100 KB ≤ 2 ms;
`tokens.EstimateRoot` 64 cached ≤ 5 µs; `dag.BackwardSlice`/`ForwardSlice` 5 000 nodes < 1 ms;
`dag.CrossingEdges` 15 000 edges < 5 µs; `dag.BuildToolUse` < 3 µs; `ipc.ReadState` < 100 µs;
`ipc` encode 4 KB < 5 µs; `ipc` server round trip p99 < 2 ms; `eval` E-2/E-3/E-4/E-5
250/50/20/15 ms; `obs.Histogram.Observe` < 100 ns; `config.Load` cold < 2 ms;
`paths.WriteAtomic` 4 KB < 2 ms; `negknow` Query hit p99 < 50 µs / miss < 5 µs, `Record` < 5 ms,
`RebuildBloom` < 50 ms, `RefreshStaleness` < 10 ms, `Open` < 150 ms, `Detector.Scan` < 5 ms;
`observer.Tombstone` < 2 µs.

**New at V4:**

| Component | Budget | Command |
|---|---|---|
| `checkpoint.Finalize` (40 segments, 64 decisions, 400 pointers) | mean < **50 ms** | `go test -bench BenchmarkFinalize -run '^$' ./internal/checkpoint/` |
| `checkpoint.Advance` (40 tool uses, 12 files) | mean < **25 ms** | `go test -bench BenchmarkAdvanceSegment -run '^$' ./internal/checkpoint/` |
| `checkpoint.Truncate` (golden 0002 at budget 12 000) | mean < **5 ms** | `go test -bench BenchmarkTruncate -run '^$' ./internal/checkpoint/` |
| `checkpoint.ExtractDecisions` (5 000 nodes, 800 explains edges) | mean < **20 ms** | `go test -bench BenchmarkExtractDecisions -run '^$' ./internal/checkpoint/` |
| `checkpoint.StripInjections` (256 KB transcript tail) | mean < **2 ms** | `go test -bench BenchmarkStripInjections -run '^$' ./internal/checkpoint/` |
| `rehydrate.Build` | `L5-BUILD` p99 < **250 ms** | `go test -bench BenchmarkBuild -run '^$' ./internal/rehydrate/` |
| `rules.PathScoped` | `L5-RULES` p99 < **50 ms** | `go test -bench BenchmarkPathScoped -run '^$' ./internal/rules/` |
| `skills.Index` | `L5-SKILLS` p99 < **20 ms** | `go test -bench BenchmarkIndex -run '^$' ./internal/skills/` |
| `scheduler.Evaluate` (64 candidates) | ≤ **50 µs/op, ≤ 8 allocs/op** | `go test -bench BenchmarkEvaluate_64Candidates -benchmem -run '^$' ./internal/scheduler/` |
| `scheduler` BOCD `Observe` (4 features) | ≤ **150 µs** at a full 512-entry posterior; ≤ **20 µs** steady state | `go test -bench BenchmarkBOCDObserve_4Features -run '^$' ./internal/scheduler/` |
| `scheduler` BOCD `MarshalBinary` | ≤ **2 ms/op** at 512 × 4 | `go test -bench BenchmarkBOCDMarshal -run '^$' ./internal/scheduler/` |
| `daemon.FeaturesFrom` | ≤ **100 µs/op** | `go test -bench BenchmarkFeaturesFrom -run '^$' ./internal/daemon/` |
| `daemon.AssembleCandidates` (2 000 tool uses) | ≤ **20 ms cold, ≤ 200 µs warm** | `go test -bench BenchmarkAssembleCandidates_2000ToolUses -run '^$' ./internal/daemon/` |
| `daemon` `Runtime.Evaluate` (2 000 tool uses, 32 candidates) | ≤ **25 ms/op** | `go test -bench BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates -run '^$' ./internal/daemon/` |
| `daemon` reclaimable index build (5 000 blocks) | ≤ **3 ms/op** | `go test -bench BenchmarkReclaimableIndexBuild_5000Blocks -run '^$' ./internal/daemon/` |
| `daemon` scheduler tap `ObserveTool` | ≤ **1.5 ms/op** (B-C, never B-A) | `go test -bench BenchmarkSchedulerTap_ObserveTool -run '^$' ./internal/daemon/` |
| `mcp.Dispatch expand` minimal | tracked; no > 25 % regression | `go test -bench BenchmarkDispatchExpandMinimal -run '^$' ./internal/mcp/` |
| `mcp.Dispatch recall` | tracked | `go test -bench BenchmarkDispatchRecall -run '^$' ./internal/mcp/` |
| `mcp.ResolveSpan` minimal (40 MB) | tracked; **no allocation growth with object size** | `go test -bench BenchmarkResolveSpanMinimal -benchmem -run '^$' ./internal/mcp/` |
| `mcp` `tools/list` marshal | tracked | `go test -bench BenchmarkToolsListMarshal -run '^$' ./internal/mcp/` |

### 5.5 Baseline discipline

After every budget above has been measured on `verify/v4`:

```
go test -bench=. -benchmem -run '^$' -count 6 ./... > v4-bench.txt
go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat testdata/bench-baseline.txt v4-bench.txt
```

Any micro-benchmark **> 10 % worse** than the committed baseline is a warning that must be explained
in the completion report; **> 25 % worse** fails the build (`00-ARCHITECTURE.md` §7). Update
`testdata/bench-baseline.txt` in a dedicated final commit
(`perf(bench): refresh baseline after wave 3`) **only after every other row is green**, so wave 4
inherits one integrated baseline rather than four per-branch ones.

---

## 6. Regression

This checkpoint subsumes its predecessors. Re-run their inventories in full — the wave-3 merge changed
the runtime behaviour of every layer beneath it, so "V1, V2 and V3 passed once" is not evidence.

### 6.1 Re-run the V1, V2 and V3 inventories

| Predecessor | Source of truth | How it is re-run here |
|---|---|---|
| **V1** (wave 0, SP-01) | `plans/V1-VERIFY-foundation-and-contracts.md` — its inventory is V3's group A (`A1`–`A17`) | Execute every row of its "Cumulative functionality inventory". §2.1 of this file is the **delta** on top of it, not a replacement. If `plans/V1-VERIFY-foundation-and-contracts.md` is absent, V3 group A discharges the obligation |
| **V2** (wave 1, SP-02…SP-07) | `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.1–§2.8 (`V2-SP01-*` … `V2-SP07-*`, `V2-ALL-*`) plus its §4 suite | Execute every row; §2.2–§2.7 here are the deltas. **V2's §4 tests are permanent** — `test/integration/*` must still pass in full, notably `TestIntegration_Phase1DedupRatioWithRealPipeline`, `TestIntegration_HotPathWarmWithRealResidentState`, `TestIntegration_BeladyPMinLandsAtLowCoupling`, `TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites` |
| **V3** (wave 2, SP-08, SP-09) | `plans/V3-VERIFY-observer-and-negative-knowledge.md` groups A–J (`A1`–`J6`) plus its §5 suite `X1`–`X12` | Execute every row; §2.8–§2.9 here are the deltas. **`X1`–`X12` are permanent** in `test/e2e/v3_integration_test.go`, notably `TestV3_ObserverFileVersionsDriveEliminationStaleness`, `TestV3_ObserverSegmentsRespectDPIGuard`, `TestV3_DegradedPassiveStillRecordsEverything`, `TestV3_HotPathUnchangedWithLedgerResident` |

**Prior-wave invariants wave 3 could plausibly have broken — each a regression if it does not hold:**

| Invariant | Command | Expected |
|---|---|---|
| Appendix C reproduced verbatim | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green |
| Append-only guard has teeth | `go test -run TestAppendOnlyGuard ./internal/paths/` | all five illegal writes fail |
| Hooks always exit 0 | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` + `go test -run TestHooksExitZeroUnderFaults ./test/e2e/` | 30/30 and 66/66 |
| Fresh build reports `ModeFull` | `go test -run TestGuard_FreshBuildReportsModeFull ./test/guards/` | green — **and now with zero `not-yet-implemented`** |
| Closing-note build-order guards | `go test ./test/guards/` | all green; submodular still inert (record the operative reason) |
| No network / confined write set | `go test -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' ./test/guards/` | green |
| Plugin bundle never drifts | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | clean, with 8 real MCP tools |
| Config docs never drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | clean |
| Import graph still a DAG; binary closure | `go run ./tools/devtool lint` (`importgraph`, `bindeps` ×6 targets) | clean with seven new real packages; only stdlib, `qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| `bench-gate`/`replay-gate` are required checks | `git grep -n "continue-on-error" -- .github/workflows/ci.yml` | no output |
| Rule W-1 skip discipline | `git grep -hn "t.Skip(" -- internal/ test/ \| grep -v "Rule W-1\|Rule W-2\|QOMPACK_TEST_ACL"` | no output; remaining W-1 skips only in `analyzertest`, `grammartest` and the `commands` suite |
| `OWNERS.tsv` is honest | `go run ./tools/devtool cover` | fails if it claims an owner for a package whose probe still returns `ErrNotImplemented`; exactly three packages remain exempt |
| `Qompack.md` unmodified | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |

### 6.2 Rule W-2 fixture reconciliation — the checkpoint's structural job

1. `testdata/golden/contracts/checkpoint/{full,minimal,empty}.json` — consumed by **SP-11** (budget
   arithmetic and goldens), **SP-12** (`checkpoint.Writer` call sites) and **SP-13** (`why`'s
   `fakeReader`, `dropped`'s `fakeReporter` seed). Re-run
   `go test -count=1 ./internal/rehydrate/... ./internal/mcp/... ./internal/daemon/` against SP-10's
   **real** writer and reader (§4.10).
2. `mcp.DropReporter` — declared in `internal/mcp` and tested against a fake.
   `TestE2E_DropReporterSatisfiesMCP` and §4.10 step 4 reconcile it against `rehydrate.NewReporter`.
3. `testdata/golden/scheduler/decision-*.json` — SP-12's frozen `Decision` documents. Re-run
   `TestEvaluate_BreakdownKeysComplete`; an extra key is as much a failure as a missing one.

The rule is unchanged: *"Any fixture that the real implementation cannot reproduce is a verification
failure, not a fixture bug."* The default response is to fix the implementation; regenerating a fixture
is permitted only under §7.1 class (c), with the measured values and the reason in the commit body.

### 6.3 The 2 % no-regression guardrail (`Qompack.md` §11.3)

Quoted verbatim:

> - No metric may regress by more than 2% to improve another without explicit sign-off

```
go run ./test/replay --corpus testdata/sessions/synthetic \
  --baseline testdata/baseline/phase0.json --phase 4 \
  --growth $TMP/v4-growth.json --signoff $TMP/v4-signoff.txt \
  --max-cpu 3m --ci --out $TMP/v4-replay.json            # expect exit 0 and "regressions": []
# No --sketch on a baselined run: the watch-fors are ratio metrics judged at 2 % against the fixture
# the baseline was recorded from, so a live ledger's health fails this by construction. The absolute
# ceiling is the separate run below.
# --max-cpu is the COST bound (the limit this line used to spell --max-wall 3m); --max-wall stays at
# its 15 m liveness default. The report flag is --out; there is no --json.

go run ./test/replay --corpus testdata/sessions/synthetic \
  --sketch $TMP/v4-health.json --baseline "" --phase 4 \
  --max-cpu 3m --ci                                      # expect exit 0; EstFPRate < 0.10

go test -v -run 'TestGate_TwoPercentBoundaryExclusive|TestGate_ImprovementNeverRegresses|TestGate_LowerBetterMetricDirection|TestGate_ZeroBaselineUsesAbsoluteTolerance' ./test/replay/
go test -v -run 'TestGate_SignOffAllowsNamedMetricOnly|TestGate_SignOffRejectsShortReason' ./test/replay/
```

For each of the 19 metrics in `MetricsOf`, per policy, the gate computes
`worse = (direction == DirHigherBetter) ? (v < b) : (v > b)` and
`regression = worse && (|b| >= 1e-6 ? |v-b|/max(|b|,1e-9) > 0.02 : |v-b| > absTol)`, with `absTol` 0.02
for ratio metrics and 1.0 for count metrics.

- **A regression discovered at a verification checkpoint is fixed, not signed off.** The `sign-off:`
  trailer exists for a deliberate trade in a feature PR; a checkpoint that signs off its own regression
  has defeated its purpose. If a metric genuinely must move, the sign-off belongs on the wave-3 PR
  (`feat/sp10-…`, `feat/sp11-…`, `feat/sp12-…` or `feat/sp13-…`) that trades it, naming that exact
  metric — this checkpoint verifies wave 3, so a wave-4 PR cannot carry a trade V4 is gating on.
- **The baseline is not rewritten to make the gate pass.** `testdata/baseline/phase0.json` is
  regenerated only if the *corpus* changed; `TestGate_CorpusStaleness` and
  `TestSynthesize_MatchesCommittedCorpus` both catch an attempt.
- `bloom_fp_rate` and `bloom_fill_ratio` are two different judgements and are run separately.
  The **relative** 2 % rule applies only against `testdata/golden/eval/growth/health.json`, the fixture
  `testdata/baseline/phase0.json`'s `0.18` / `0.006` were recorded from (`b103037`): that run measures
  reproducibility of the committed number. The **absolute** `EstFPRate > 0.10` §11.4 ceiling is what a
  live ledger is judged against, and it is measured with `--baseline ""` so the fixture ratio is not
  applied to it. Feeding a live health file into a baselined run asserts the ledger equals the fixture
  to within 2 %, which is not a contract anything owes. `benchstat` (§5.5) is the micro-benchmark
  analogue at 10 %/25 %.

### 6.4 Metric deltas against the Phase-0 baseline

Wave 3 is the first wave whose policies are *supposed* to move the numbers. Record, for every §11.2
metric, the delta of each policy against `testdata/baseline/phase0.json` — **even when it is zero**.
"No change" measured is worth more than "no change" assumed; that is §1.3 RC-3 applied to our own
process. Source: `jq '.policies | to_entries[] | {policy: .key, metrics: .value}' $TMP/v4-replay.json`.

| §11.2 metric | Direction | Baseline (phase 0) | `stock` | `qompack-rehydrate` | `qompack-l3` | Δ vs baseline | Within 2 %? |
|---|---|---|---|---|---|---|---|
| Fraction of Belady OPT | higher better | | | | | | |
| First-divergence turn | higher better | | | | | | |
| File-set Jaccard | higher better | | | | | | |
| Tool edit distance | lower better | | | | | | |
| Same decision reached | higher better | | | | | | |
| Decision preservation | higher better | | | | | | |
| Redundant reads | lower better | | | | | | |
| Re-attempts of eliminated approaches | lower better | | | | | | |
| Rewrite tokens / session | lower better | | | | | | |
| Forfeited discount tokens | lower better | | | | | | |
| Rehydration budget (tokens) | lower better | | | | | | |
| Retrieval hit rate | higher better | | | | | | |
| **Compaction pause (modelled)** | lower better | | | | | | |
| **Residual span at compaction** | lower better | | | | | | |
| **First-turn-after latency (modelled)** | lower better | | | | | | |
| Bloom FP rate | lower better | | | | | | |
| Bloom fill ratio | lower better | | | | | | |
| Store dedup ratio | higher better | | | | | | |
| Store growth exponent | lower better | | | | | | |

**Interpretation rule.** A metric that *improves* is never a regression regardless of magnitude. A
metric that worsens by more than 2 % is a failure to be fixed on `verify/v4`. The three metrics wave 3
is expected to move materially — rewrite tokens, residual span, rehydration budget — should all move in
the *improving* direction; if one worsens, the wave did not do what it claimed, and the finding goes in
the completion report before any fix is attempted.

### 6.5 Regression run order

Run in this order so a failure surfaces as early and as cheaply as possible: (1) preflight (§1); (2)
`go test ./...` without `-race`; (3) the thirteen group inventories in parallel (§1.1); (4)
`go test -race ./...` (V4-ALL-01); (5) coverage (V4-ALL-04); (6) lint / security / docs (V4-SP01-01,
V4-SP01-02, V4-SP01-07, V4-ALL-05, V4-ALL-06); (7) benchmarks and budgets (§5); (8) the replay gate,
the Phase-3 and Phase-4 criteria and the 2 % rule (§3.1, §3.2, §6.3); (9) the new integration tests
(§4); (10) CI on `verify/v4` (V4-ALL-07).

---

## 7. Failure protocol

Any failing row in §2, any unmet criterion in §3, any failing test in §4, any breached budget in §5,
any regression in §6, any red CI job puts this checkpoint in the failed state. There is no partial
pass and no "note it and move on".

### 7.1 Systematic diagnosis (before writing any fix)

1. **Reproduce deterministically.** Re-run the single failing test with `-run '^<ExactName>$' -v
   -count=1`; if it does not reproduce, try `-count=10` and `-race`. **A flake at an integration seam
   is a defect, not noise.** The usual wave-3 causes: a wall-clock read that should be
   `testutil.FakeClock`; map-iteration order leaking into a `Breakdown`, a `Reasons` slice or a
   rendered payload; an unsynchronized scheduler `Runtime` field; an idle tick racing a `PreCompact`.
2. **Name the seam.** Wave-3 failures are overwhelmingly at seams; the productive first question is
   which two packages disagree. The recurring candidates: `checkpoint` ↔ `store.SegmentLog` (who owns
   `MarkEncoded`, at which seq); `checkpoint` ↔ `rehydrate` (`Ref.Frontier` is writer-only and zero on
   any reader-returned `Ref`, so a consumer reading it from `Reader.Latest` gets 0 and silently
   degrades); `rehydrate` ↔ `store` (which record is "the earliest prompt of *this* session");
   `scheduler` ↔ `dag` (`Candidate.Pos` must be a token position; `CrossingEdges` uses `lo < pos <= hi`
   at both ends); `scheduler` ↔ `checkpoint` (the advance batch must be closed **and** unencoded);
   `mcp` ↔ `store` (chunk-aligned spans — `internal/mcp` must not import `symbols`, so a widener
   mismatch shows up as an off-by-a-chunk body); `mcp` ↔ `negknow` (three-way answer, `staleResponse`);
   `daemon` ↔ everything (`Services` nil-tolerance, op routing, the mode gate).
3. **Read the contract, not the code.** Open `00-ARCHITECTURE.md` §5 for the interface in question and
   determine which side violates it. If §5 itself is wrong, that is an amendment, not a workaround.
4. **Classify.** Exactly one of:
   - **(a) Implementation defect in a wave-3 package** (`checkpoint`, `pins`, `rehydrate`, `rules`,
     `skills`, `scheduler`, `mcp`, or the SP-12/SP-13-owned `internal/daemon` files) → fix on
     `verify/v4`.
   - **(b) Latent defect in a wave-0/1/2 package** exposed by wave-3 traffic → fix on `verify/v4`.
     Expect several: `store`, `dag` and `sketch` have never had an *acting* consumer before.
   - **(c) W-2 fixture divergence** where the real implementation is correct and the fixture was a
     placeholder → update the fixture **with a commit body stating the measured values and why they
     differ**, naming the owning subplan. Never to silence an undiagnosed red test, and never for
     `testdata/baseline/phase0.json` or the synthetic corpus.
   - **(d) Test/assertion defect** — the assertion encodes something the design does not require → fix
     the test, quoting the design sentence that justifies the change in the commit body.
   - **(e) Interface defect** — §5 is genuinely wrong. Do **not** work around it: open
     `arch/<short-reason>` off `develop`, amend §5, merge, rebase `verify/v4`, then fix. Silent
     divergence from §5 is the one failure mode that makes parallel waves worthless.
   - **(f) Budget breach** → profile before optimizing; attribute the cost (`-cpuprofile`, the daemon's
     per-stage histograms, `b_a_method`/`spawn_floor_ms`). **A budget met by removing work the design
     requires is a failure disguised as a pass** — a B-E that passes because the draft was empty, or a
     B-F that passes because the span resolver stopped widening.
5. **Never weaken the check.** No `//nolint`, no `t.Skip`, no `//nomagic:allow`, no lowered threshold,
   no golden regenerated to match broken output, no deleted assertion, no invented `sign-off:` trailer.
   A genuinely wrong threshold is class (e) and needs an amendment carrying the justifying measurement.
6. **Write the failing assertion first** where the fix is behavioural. If a defect slipped through wave
   3, the reason is a missing test: add it to the permanent suite (owning package, or
   `test/integration`/`test/e2e` for a seam), observe it fail, then fix.
7. **Check the blast radius.** A fix to a shared package (`store`, `sketch`, `canon`, `chunk`, `dag`,
   `checkpoint`) requires re-running that package's group **and** every group that consumes it. A fix
   in `canon` moves chunk boundaries, which moves MCP spans, which moves B-F, which moves the baseline.

### 7.2 Committing fixes

All fixes land on **`verify/v4`** — never on `develop`, never on a merged `feat/sp1N-*` branch, never
as an amended wave-3 commit. **Small conventional commits, as many as needed:** there is no
commit-count budget on a verification branch (the 5–8 rule is a subplan rule), one coherent fix per
commit, each compiling and passing `go run ./tools/devtool test` for the packages it touches before it
is made. Format per §10: `<type>(<scope>): <subject ≤ 72 chars>`, a body explaining the decision rather
than the diff, and a footer `Refs: V4, SP-<NN>, <gap ids>, <Qompack.md sections>`. Example:

```
fix(rehydrate): read the frontier from state/precompact.json, not from Ref

Reader.Latest returns a Ref whose Frontier is zero by design (00-ARCH §5.14,
TestReaderRefLeavesWriterOnlyFieldsZero), so the O1 span assertion silently
compared against turn 0 and passed vacuously. Discovered at V4 by
TestV4_O1SpanInstructionFromARealCheckpointFrontier, the first test to run the
real writer and the real rehydrator against each other.

Refs: V4, SP-10, SP-11, §8.5, §8.6
```

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR body
in this repository. CI's `verify` job greps the commit range for `Co-Authored-By`, `Signed-off-by`,
`Generated with` and `🤖` and fails the build if any appear.

Commit the **new integration tests of §4** as their own commits, separate from any fixes they provoked.
Commit the `testdata/bench-baseline.txt` refresh **last and alone**, once everything else is green.

### 7.3 Re-run the whole checkpoint from the top

After **any** fix — even a one-line one — **re-run this checkpoint from §2 row 1.** Not just the
failing row, not just the failing group. Wave 3's entire risk profile is composition: a fix in
`checkpoint` moves the frontier, which moves the residual span, which moves the Phase-4 slope, which
moves a replay metric; a fix in `canon` moves chunk boundaries, which moves MCP spans, which moves B-F.
A partial re-run cannot see any of that. Practically: re-dispatch all thirteen subagents against the
fixed tree, then re-run §3, §4, §5 and §6 in the main session, then push and re-run CI. **Record how
many full passes it took.** Iterate: diagnose → fix → commit → full re-run, until every row of §8 reads
PASS.

### 7.4 The wave-4 gate

> **No wave-4 branch is cut until this checkpoint is fully green.**

Until every row of §8 reads PASS **and** CI is green on `verify/v4` across `verify`, `test` (×3 OS),
`cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security` and `docs`, do not
create `feat/sp14-slash-commands-and-observability`, `feat/sp15-analyzer-selection-and-grammar` or
`feat/sp16-phase7-refinements`, and do not begin work described in their subplan files (§9: *"No wave-N
branch is ever cut before verification V<N> is green."*). A partially-green checkpoint is not a
checkpoint; if something cannot be fixed it is class (e) and needs an architecture amendment, not a
waiver.

### 7.5 On success — write the report, then merge and tag

**Write the completion report first.** Fill in the §8 template and commit it to `verify/v4` as
`plans/V4-report.md` **before** the merge below, exactly as V1 did
(`plans/V1-VERIFY-foundation-and-contracts.md` §8). This is not a filing convention:
`test/guards/carrieddefects_test.go`'s `TestCarriedDefects_WaveReportRequiresResolution` keys on
`plans/V4-report.md` and on nothing else, so while that file is absent the sign-off gate passes
without asserting anything and every `deferred:V4-VERIFY` row in `plans/CARRIED-DEFECTS.tsv` signs
off unexamined. Commit `docs/adr/0101-v4-verification.md` too if the `0100`-block per-checkpoint
record is wanted — it is the narrative copy, not the gate trigger.

```
git add plans/V4-report.md
git commit -m "docs(v4-report): record the wave-3 verification checkpoint"
go test ./test/guards/ -run TestCarriedDefects -v   # with plans/V4-report.md on disk
```

A failure from that guard run names the rows still owed a disposition; fix or re-defer them and
re-run before merging. Fill every `<sha>` and `<n>` in the §8 template by hand and check them by
eye: `go run ./tools/devtool lint`'s `docmarkers` sub-check will **not** catch them. Its
`markerToken` regex (`tools/devtool/planchecks.go:366`) matches only an angle-bracketed **all-caps**
head, and it exempts fenced blocks entirely, so a lower-case `<sha>` left in a committed
`plans/V4-report.md` ships silently — exactly how `plans/V2-report.md` shipped two unfilled markers.

```
go run ./tools/devtool ci-local                 # final full local gate
git log --format=%B develop..verify/v4 | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'   # no output
git checkout develop
git merge --no-ff verify/v4 -m "chore(verify): V4 — wave-3 verification checkpoint

Re-verified every functionality delivered by SP-01..SP-13 on the integrated
develop, closed the PreCompact -> checkpoint -> SessionStart(compact) ->
rehydration loop end to end for the first time, measured B-E against a real
PreCompact and B-F for the first time at all, and discharged the Phase 3 and
Phase 4 exit criteria.

Refs: V4, SP-10, SP-11, SP-12, SP-13, §10 Phase 3, §10 Phase 4, §11.3"
git tag v0.3.0
git push origin develop --tags
```

The numbers in `plans/V4-report.md` are the input to V5's regression comparison and must not live
only in a terminal scrollback. Paste an abridged copy into the merge commit body if you want it
visible in `git log`; that copy is a convenience, not the record.
Then, and only then, cut wave 4 from the post-verification `develop`:

```
git checkout -b feat/sp14-slash-commands-and-observability  develop
git checkout -b feat/sp15-analyzer-selection-and-grammar    develop
git checkout -b feat/sp16-phase7-refinements                develop
```

---

## 8. Completion report template

Fill this in as the checkpoint runs and paste it as the checkpoint's output. Every row gets a verdict
**and**, where a metric column exists, the measured value. A row with `PASS` and an empty metric cell
is not complete. `N/A` is a legitimate verdict only where this document already marks it so.

```markdown
# V4 completion report

- Branch: verify/v4 (cut from develop @ <sha>)
- develop head at start: <sha>   | verify/v4 head at end: <sha>
- Wave-3 merge order confirmed: SP-10 → SP-11 → SP-12 → SP-13 (four --no-ff merges)  [ ]
- Wave-3 build-order guard: `merge order unverified — no guard discriminates this wave's
  permutations` (`plans/README.md` step 3: a wave whose order no guard can discriminate declares it
  here instead. `TestGuard_StoreAndNegknowBeforeCheckpoint` is cross-wave — `store` lands in wave 1,
  `negknow` in wave 2 — so every permutation of SP-10..SP-13 satisfies it. If this wave authors a
  real guard instead, name it here and delete the declaration; doing neither is not an option)  [ ]
- Full checkpoint passes required: <n>
- Fix commits on verify/v4: <n>   (listed in §13 below)
- Platforms measured: ubuntu-latest / macos-latest / windows-11-dev
- Date: <ISO 8601>

## 1. Inventory — SP-01 foundation (delta; V1/V3-group-A re-run in full)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP01-01 | devtool lint with 7 new packages | | |
| V4-SP01-02 | nomagic with the wave-3 literal set | | |
| V4-SP01-03 | internal/cli after SP-10/SP-13 edits | | 30/30: |
| V4-SP01-04 | main.go still dispatch-only | | LOC: |
| V4-SP01-05 | plugin-validate sees 8 real MCP tools | | |
| V4-SP01-06 | obs budget table with B-F in force | | |
| V4-SP01-07 | config docs no-drift | | |
| V4-SP01-08 | Appendix C verbatim | | |
| V4-SP01-09 | append-only guard incl. checkpoints + pins | | |
| V4-SP01-10 | only analyzer/grammar/commands may skip | | remaining skips: |
| V4-SP01-11 | e2e six hooks | | |
| V4-SP01-12 | build-order guards, V4 semantics | | operative reason: |
| V4-SP01-13 | build-all six targets | | |
| V4-SP01-14 | commit-message policy | | |
| — | V1 inventory re-run in full | | source: |

## 2. Inventory — SP-02 replay / Belady / baseline (delta)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP02-01 | package -race | | |
| V4-SP02-02 | five registered policies | | PolicyNames(): |
| V4-SP02-03 | §11.2 latency/token fields populated | | medians: |
| V4-SP02-04 | latency honesty tags survive | | |
| V4-SP02-05 | Phase-0 baseline byte-reproducible | | |
| V4-SP02-06 | replay gate at --phase 4 | | five phase checks: |
| V4-SP02-06b | §11.4 bloom ceiling, `--baseline ""` | | EstFPRate: |
| V4-SP02-07 | corpus staleness at phase 4 | | tripped? |
| V4-SP02-08 | E-1..E-5 | | |
| V4-SP02-09 | eval import purity | | |
| V4-SP02-10 | coverage ≥ 85 % | | actual: |

## 3. Inventory — SP-03 sketches (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP03-01 … V4-SP03-07 | | L0SketchUpdate ns/op + allocs: ; empirical FP: |

## 4. Inventory — SP-04 chunk / canon / symbols (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP04-01 … V4-SP04-07 | | Split_100KB µs: ; testrunner gain: ; span-alignment: |

## 5. Inventory — SP-05 daemon / IPC / contract (delta)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP05-01 | three packages -race -count=2 | | |
| V4-SP05-02 | Services populated, nil-tolerance intact | | |
| V4-SP05-03 | **nine declared / zero not-yet-implemented** | | count: |
| V4-SP05-04 | additionalContext sentinel is live | | |
| V4-SP05-05 | precompact.json observable | | wall_ms / timeout_ms: |
| V4-SP05-06 | mcp.server_registered observes initialize | | |
| V4-SP05-07 | ipc.OpMCP routed | | |
| V4-SP05-08 | idle controller task set | | names: |
| V4-SP05-09 | degraded-passive suppresses acting paths | | |
| V4-SP05-10 | 66-combination fault table | | 66/66: |
| V4-SP05-11 | sync→spool transition | | |
| V4-SP05-12 | bench-hotpath B-A/B-B/B-D/B-E | | see §11 |
| V4-SP05-13 | scheduler not on the hot path | | |
| V4-SP05-14 | security posture | | |
| V4-SP05-15 | coverage ≥ 75 % ×4 | | actual: |

## 6. Inventory — SP-06 store / redact / tokens (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP06-01 … V4-SP06-10 | | DedupRatio (store / observer): ; GC deadline error: |

## 7. Inventory — SP-07 DAG / slicing (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP07-01 … V4-SP07-08 | | BackwardSlice ms: ; CrossingEdges µs: ; size_ratio / recall: |

## 8. Inventory — SP-08 observer (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP08-01 … V4-SP08-09 | | Phase-1 ratios: ; canon gap factor: ; OnToolUse p50/p99: |

## 9. Inventory — SP-09 negative knowledge (delta)
| ID | Verdict | Metric / note |
|---|---|---|
| V4-SP09-01 … V4-SP09-09 | | Phase-2: stock_repeats / negknow_repeats / stale_blocks: |

## 10. Inventory — SP-10 checkpointer + pins (new)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP10-01 | packages -race -count=2 | | |
| V4-SP10-02 | append-only pins + tombstones + view | | |
| V4-SP10-03 | §8.5 schema in importance order | | key order: |
| V4-SP10-04 | versioning and migration | | |
| V4-SP10-05 | no code snippets (G3.4) | | |
| V4-SP10-06 | store-only regeneration (GC rule) | | |
| V4-SP10-07 | draft begin/resume/advance/abort | | draft bytes for a 40 KB read: |
| V4-SP10-08 | DPI guard at L4 | | |
| V4-SP10-09 | ExtractDecisions, sole DecisionID producer | | decisions minted: |
| V4-SP10-10 | importance-ordered truncation | | |
| V4-SP10-11 | pointer ground-truth validation | | |
| V4-SP10-12 | MANIFEST / immutability / reader chain | | |
| V4-SP10-13 | focus instructions with O1 span | | |
| V4-SP10-14 | import discipline | | |
| V4-SP10-15 | PreCompact incl. near-deadline finalize | | near-deadline wall: |
| V4-SP10-16 | cadence checkpoints, gated by degradation | | |
| V4-SP10-17 | checkpointtest suites, zero skips | | |
| V4-SP10-18 | checkpoint e2e (full + degraded) | | |
| V4-SP10-19 | W-2 fixtures consumable by SP-11/SP-13 | | |
| V4-SP10-20 | benchmarks incl. B-E | | Finalize / Advance / Truncate / Extract / Strip: |
| V4-SP10-21 | frontier on-vs-off residual span | | off P50 / on P50 / reduction %: |
| V4-SP10-22 | placeholder + stub-residue scan | | |
| V4-SP10-23 | coverage ≥ 90 % / ≥ 90 % | | actual: |

## 11. Inventory — SP-11 rehydrator / rules / skills (new)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP11-01 | three packages -race -count=2 | | |
| V4-SP11-02 | glob engine + frontmatter | | |
| V4-SP11-03 | path-scoped rules (G4.1) | | |
| V4-SP11-04 | nested CLAUDE.md (G4.2) | | |
| V4-SP11-05 | skill index (G4.4), budget from config | | |
| V4-SP11-06 | eight items in normative order | | |
| V4-SP11-07 | injection tagging + sentinel | | |
| V4-SP11-08 | item 1 never truncated | | |
| V4-SP11-09 | item 2 verbatim intent (G2.3, G7.3) | | |
| V4-SP11-10 | item 3 eliminations + standing instruction | | topN / seen: |
| V4-SP11-11 | items 4/5/6, pointers not contents | | |
| V4-SP11-12 | items 6a/6b restored instructions | | |
| V4-SP11-13 | item 7 drop report (G4.5) | | entries: |
| V4-SP11-14 | 8–12K budget discipline (G3.3) | | Tokens @8K/10K/12K: |
| V4-SP11-15 | Reporter persistence | | |
| V4-SP11-16 | goldens + degradation | | |
| V4-SP11-17 | G7.5 checkpoint-as-fallback | | transcript reads: 0 ? |
| V4-SP11-18 | daemon SessionStart service | | |
| V4-SP11-19 | rehydration e2e | | payload tokens: |
| V4-SP11-20 | Reporter satisfies mcp.DropReporter | | |
| V4-SP11-21 | import discipline, observer untouched | | |
| V4-SP11-22 | L5 benchmark budgets | | BUILD / RULES / SKILLS / SESSIONSTART: |
| V4-SP11-23 | suites zero skips + placeholder scan | | |
| V4-SP11-24 | coverage 85 / 75 / 75 | | actual: |

## 12. Inventory — SP-12 scheduler (new)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP12-01 | scheduler + daemon -race -count=2 | | |
| V4-SP12-02 | threshold arithmetic | | soft / hard: |
| V4-SP12-03 | sliding-TTL keyed on last API call (E1) | | |
| V4-SP12-04 | Young–Daly with measured δ | | YoungDaly(20,1800): |
| V4-SP12-05 | ski rental computed, never literal | | |
| V4-SP12-06 | BOCD | | detection index: ; posterior len: |
| V4-SP12-07 | composite trigger | | reasons order: |
| V4-SP12-08 | p-selection with the cache term | | warm P / cold P / PScore: |
| V4-SP12-09 | Breakdown key completeness | | |
| V4-SP12-10 | p-selection ship-order gate | | default / after-runtime: |
| V4-SP12-11 | §8.7 eviction ordering | | |
| V4-SP12-12 | reclaimable suffix index | | |
| V4-SP12-13 | feature extraction | | |
| V4-SP12-14 | candidate assembly + cache | | cold / warm ms: |
| V4-SP12-15 | Runtime: ladder, EWMAs, persistence, healing | | |
| V4-SP12-16 | state codec (atomic, not append-only) | | |
| V4-SP12-17 | Services tap, no SP-05/SP-08 edits | | diff --stat: |
| V4-SP12-18 | O5 frontier advancement + DPI guard | | |
| V4-SP12-19 | O3 idle work, gated twice | | six tasks: |
| V4-SP12-20 | not on the hot path | | |
| V4-SP12-21 | l3policy | | |
| V4-SP12-22 | suite zero skips, imports, placeholders | | |
| V4-SP12-23 | eight benchmark budgets | | Evaluate µs/allocs: |
| V4-SP12-24 | Phase-4 exit criterion | | see §14 |
| V4-SP12-25 | coverage ≥ 85 % | | actual: |

## 13. Inventory — SP-13 MCP retrieval (new)
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V4-SP13-01 | package -race -count=2 | | |
| V4-SP13-02 | JSON-RPC server | | |
| V4-SP13-03 | stdout purity / concurrency / panic isolation | | |
| V4-SP13-04 | FuzzServeLine 60 s | | |
| V4-SP13-05 | schema validator | | |
| V4-SP13-06 | exactly eight tools, in order | | names: |
| V4-SP13-07 | minimal-span resolver | | |
| V4-SP13-08 | recall | | |
| V4-SP13-09 | expand | | |
| V4-SP13-10 | re_read with historical `at` | | |
| V4-SP13-11 | already_tried three-way | | |
| V4-SP13-12 | record_eliminated | | |
| V4-SP13-13 | timeline / why / dropped | | |
| V4-SP13-14 | ephemeral-at-birth | | |
| V4-SP13-15 | promotion counting | | threshold: |
| V4-SP13-16 | daemon wiring + mcp.server_registered | | |
| V4-SP13-17 | qompack mcp transcoder, never spools | | |
| V4-SP13-18 | stdio e2e + standing-instruction agreement | | |
| V4-SP13-19 | docs + manifest agreement | | |
| V4-SP13-20 | import discipline + nomagic | | |
| V4-SP13-21 | **budget B-F** | | p50/p95/p99/max: |
| V4-SP13-22 | MCP micro-benchmarks | | |
| V4-SP13-23 | placeholder + out-of-scope discipline | | |
| V4-SP13-24 | coverage ≥ 85 % | | actual: |

## 14. Whole-tree gates
| ID | Gate | Verdict | Note |
|---|---|---|---|
| V4-ALL-01 | `go test -race ./...` | | |
| V4-ALL-02 | `go test -count=2 ./...` (windows) | | |
| V4-ALL-03 | `devtool ci-local` | | |
| V4-ALL-04 | coverage floors; exactly three stub exemptions | | exempt: |
| V4-ALL-05 | security posture | | |
| V4-ALL-06 | placeholder scan | | |
| V4-ALL-07 | CI green on verify/v4 (9 jobs) | | |
| V4-ALL-08 | Qompack.md unmodified | | |

## 15. Exit-criteria re-verification
| Criterion (quoted) | Measured value | Verdict |
|---|---|---|
| **Phase 3** — "post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured" | A1 qompack median __ vs stock median __ ; A2 qompack median turn __ vs baseline __ ; A3 residual __ , reduction __ % | |
| **Phase 4** — "measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly)" | rewrite Σ ratio __ ; worst divergence delta __ % ; residual slope __ tokens/turn ; quartile ratio __ ; ResidualSpan.P95 __ ; pause_modelled __ | |
| Phase 0 — "a single number for stock behaviour, reproducible across at least 20 real sessions" | stock fraction_of_opt __ over 24 sessions; reproducible __ | |
| Phase 1 — "ratio ≥ 4:1 …; hook p99 < 15ms" | DedupRatio __ / __ ; B-A p99 __ ms | |
| Phase 2 — "measurable reduction in repeated-elimination events …, zero stale-block incidents" | repeats __ → __ ; stale_blocks __ | |
| SP-10 — "no code snippets. Files are pointers with a one-line reason." | | |
| SP-10 — frontier on-vs-off residual reduction ≥ 30 % | __ % | |
| SP-11 — "target 8–12K" rehydration budget | payload __ tokens (cap 12 000) | |
| SP-12 — "hook p99 < 15ms (L0), < 2s (L4)" unchanged | B-A __ ms vs V3 __ ms | |
| SP-13 — B-F "p95 < 250 ms (`minimal` span)" | __ ms | |
| §11.3 — "Every phase gate runs the full replay suite" | phase checks run: __ | |
| §11.4 — corpus staleness / bloom FP | tripped __ ; EstFPRate __ | |

## 16. New cross-component integration tests (permanent)
| Test | Seam | Verdict | Metric / note |
|---|---|---|---|
| TestV4_PreCompactToCheckpointToRehydrateRoundTrip | cli+ipc+daemon+checkpoint+rehydrate | | payload tokens: |
| TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold | observer+scheduler+dag | | fired at __ tokens (< 167 000) |
| TestV4_O1SpanInstructionFromARealCheckpointFrontier | store+checkpoint | | frontier: |
| TestV4_FrontierAdvancementKeepsResidualSpanODelta | scheduler+checkpoint+store | | slope: ; on/off at 1 200 turns: |
| TestV4_TombstoneToRecallToExpandRoundTrip | observer+store+mcp+symbols | | span bytes: |
| TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction | negknow+rehydrate+mcp | | |
| TestV4_EphemeralRetrievalResultsRankFirstForEviction | mcp+store+scheduler | | |
| TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint | rehydrate+observer+checkpoint | | generations clean: |
| TestV4_GrowthGuardrailWithCheckpointsAndEphemerals | observer+mcp+store+eval | | exponent: |
| TestV4_WhyAndDroppedAnswerFromRealProducers | checkpoint+rehydrate+mcp | | |
| TestV4_LiveSessionWriteSetAppendOnlyAndImmutability | everything | | |
| TestV4_DegradedPassiveWithEverySubsystem | contract+all wave-3 | | |
| TestV4_HotPathUnchangedWithTheFullWave3ResidentSet | everything | | B-A p99: ; Δ vs V3: |
| TestV4_EveryContractAssertionHasARealProducer | daemon+contract+ckpt+rehydrate+mcp | | 9/9: |

## 17. Performance budgets
| Budget | Threshold | linux | macos | windows | Verdict |
|---|---|---|---|---|---|
| B-A hook_controlled p99 | < 15 ms | | | | |
| B-B l0_ingest p99 | < 2 ms | | | | |
| B-C l0_process p99 (soft) | < 50 ms | | | | reported |
| B-D hook_wall p99 | reported | | | | reported |
| **B-E checkpoint_finalize p99** | **< 2 s (real PreCompact)** | | | | |
| **B-F mcp_tool_call p95** | **< 250 ms** | | | | |
| B-G hook_degraded p99 | < 1 000 ms (`runtime.budgets.hookDegradedMs`) | | | | reported |
| L5-BUILD | < 250 ms | | | | |
| L5-RULES | < 50 ms | | | | |
| L5-SKILLS | < 20 ms | | | | |
| L5-SESSIONSTART | < 1.5 s | | | | |
| Rehydration payload | 8–12 K tokens | | | | |
| Residual span P95 | ≤ 20 000 | | | | |
| Rewrite tokens ratio | ≤ 0.80 × stock | | | | |
| Store dedup ratio | ≥ 4:1 | | | | |
| Store growth exponent | ≤ 0.95 | | | | |
| checkpoint.Finalize | < 50 ms | | | | |
| checkpoint.Advance | < 25 ms | | | | |
| scheduler.Evaluate 64 cand. | ≤ 50 µs / ≤ 8 allocs | | | | |
| BOCD.Observe full / steady | ≤ 150 µs / ≤ 20 µs | | | | |
| AssembleCandidates cold / warm | ≤ 20 ms / ≤ 200 µs | | | | |
| SchedulerTap.ObserveTool | ≤ 1.5 ms | | | | |
| L0SketchUpdate | ≤ 5 µs, 0 allocs | | | | |
| dag BackwardSlice 5 000 | < 1 ms | | | | |
| dag CrossingEdges | < 5 µs | | | | |
| store PutBytes cold / warm | ≤ 3 ms / ≤ 400 µs | | | | |
| store OpenSpan 4 KB of 4 MB | ≤ 150 µs | | | | |
| chunk Split 100 KB | < 800 µs | | | | |
| eval E-2 / E-3 / E-4 / E-5 | 250 / 50 / 20 / 15 ms | | | | |
| benchstat vs baseline | no > 25 % regression | | | | warnings: |

## 18. Regression
| Item | Verdict | Note |
|---|---|---|
| V1 inventory re-run in full | | |
| V2 inventory re-run in full (incl. its §4 suite) | | |
| V3 inventory re-run in full (incl. X1–X12) | | |
| W-2 reconciliation: contracts/checkpoint fixtures | | |
| W-2 reconciliation: mcp.DropReporter vs rehydrate.Reporter | | |
| W-2 reconciliation: golden/scheduler/decision-*.json | | |
| Contract producers 9 declared / 0 gated | | |
| Coverage exemptions exactly {analyzer, grammar, commands} | | |
| §11.3 2 % rule: regressions found | | list, or "none" |
| §11.3 2 % rule: sign-offs used | | must be "none" at a checkpoint |
| §11.2 metric deltas vs phase0.json | | table in §6.4, or "all within 2 %" |
| benchstat > 10 % warnings | | explained: |
| §13 invariants 1–10 re-asserted | | |

## 19. Failures, diagnoses and fixes
| # | Failing item | Class (a–f) | Seam | Diagnosis | Fix commit | Re-run pass # |
|---|---|---|---|---|---|---|
| 1 | | | | | | |
```

---

## 9. Gate

`feat/sp14-slash-commands-and-observability`, `feat/sp15-analyzer-selection-and-grammar` and
`feat/sp16-phase7-refinements` do not exist until every one of the following is true.

- [ ] Every row of §2 (`V4-SP01-*` … `V4-SP13-*`, `V4-ALL-*`) is PASS, or a documented `N/A` this
      file authorises.
- [ ] The V1, V2 and V3 inventories were re-run **in full**, not by reference, and are PASS (§6.1).
- [ ] Every criterion in §3 is measured with a recorded number, including both the **Phase 3** and
      **Phase 4** exit criteria quoted verbatim.
- [ ] All fourteen new integration tests in §4 exist, pass, and are committed to the permanent suite.
- [ ] Every budget in §5 is measured on all three platforms; **B-E passes against a real
      `PreCompact`** and **B-F passes for the first time**; no `N/A` remains in the §2.4 table.
- [ ] `benchstat` shows no > 25 % regression; every > 10 % warning is explained in the report.
- [ ] The §11.3 2 % guardrail reports `"regressions": []` with **zero** `sign-off:` trailers used.
- [ ] `contract.Monitor` reports `ModeFull` with **nine declared producers and zero
      `not-yet-implemented`**.
- [ ] Rule W-2 reconciliation is complete for the checkpoint contract fixtures, `mcp.DropReporter`
      and the scheduler decision goldens; any fixture change carries its measured values in the
      commit body.
- [ ] Coverage floors hold, with exactly three packages still exempt: `analyzer`, `grammar`,
      `commands`.
- [ ] `testdata/bench-baseline.txt` updated with integrated wave-3 numbers, in its own final commit.
- [ ] CI green on `verify/v4`: `verify`, `test` ×3, `cover`, `crossbuild`, `bench-gate`,
      `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] No `Co-Authored-By`, `Signed-off-by`, `Generated with` or `🤖` anywhere in
      `develop..verify/v4`.
- [ ] `Qompack.md` is byte-identical to the root commit's copy.
- [ ] The completion report committed as `plans/V4-report.md` **before** the merge (and abridged
      into the merge body); `go test ./test/guards/ -run TestCarriedDefects` passes with it in place.
- [ ] `verify/v4` merged into `develop` with `--no-ff`; `develop` tagged `v0.3.0`.
- [ ] **Only now**: the three wave-4 branches are cut from the post-verification `develop`.


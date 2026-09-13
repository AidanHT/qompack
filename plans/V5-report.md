# V5-VERIFY completion report — commands, selection, grammar and Phase-7 refinements

**Branch / HEAD.** `verify/v5`, cut from `develop` @ `87c0c1d` (wave 4 integrated), reported at
`b1a0183` in worktree `../qompack-v5`; the closing commit after it edits only this report (§28,
§17's B2 row and the corrections the review asked for). Dirty baseline at cut: none (`git status --porcelain` empty).
The main repository stays on `verify/v3` @ `7f92af5` with its stale planning-draft dirt untouched.

**Date.** Executed 2026-09-08 (integration, lane 1, section 4 authoring, inventory), 2026-09-09
(carried defects, fix batch, fuzz, quiet pass) and 2026-09-10 (AC-gated benchmark windows,
carried-defect dispositions, lint, this report and its independent review).

**Environment.** Windows 11 Home 10.0.26200, `go1.26.6 windows/amd64`, Core Ultra 7 155H, 22 logical
CPUs, 31.4 GB RAM with 7–11 GB free during the run. No Linux runtime: `wsl -l -v` lists only a stopped
`docker-desktop` distribution and the Docker daemon was not running. Plugin manifest version `0.1.0`;
config settings versions `runtime.migration.settingsVersion = 1` and `runtime.phase7.settingsVersion = 1`;
Qompack.md at v1.5 (planning-only revision, 2026-09-06). Host: Claude Code 2.1.263 is installed on this
machine, but the plugin was not installed into it for this checkpoint (installed-host evidence is B01,
§29). Persisted schema versions on this tree: store index records v1 (v2 on a line that carries a
declared delta base or a retained original), checkpoint `SchemaVersion` 1, daemon scheduler state 1,
observer state 1, contract capability register 1, contract history 1, observation ledger 1. Qompack
makes no model calls; the planning
and execution seats are recorded in §25 with their routing caveat.

**Authority.** The user's instruction of 2026-09-08 ("implement now. Finish everything until wave 5 is
ready to run") is the authorization for every action below. It is also the only authority under which
SP-14 was merged: [V4-report.md §19](V4-report.md) records four open sign-off items and states that
SP-14 alone names "verified V4" as a prerequisite. That instruction is recorded here as a waiver of
the same kind as the 2026-08-26 V3 CI-gate waiver, and the four V4 items are carried unchanged in
§29. Nothing was pushed and no tag was created.

**Vocabulary.** Cells use the plan §8 set: `documented`, `verified_in_target`, `implemented_unverified`,
`unsupported`, `experimental`, `unknown`. Inventory rows additionally use the V4 disposition set
(`MAPPED`, `MAPPED-CMD`, `SUPERSEDED-BY-WAVE4`, `RETIRED`, `MISSING`, `NEEDS-COORDINATOR`) and the
result set (`PASS`, `FAIL`, `FAIL-BASELINE`, `FAIL-COLOAD-SUSPECT`, `SKIP`, `NOT-RUN`). A disposition is
never a pass; only an executed command yields `PASS`. No cell below copies an older report's result.

---

## 0. Wave-4 integration record

Integration followed the procedure recorded in the plan's "Wave-4 integration, before this checkpoint
starts" section (added by SP-21). All merges ran with `git -C ../qompack-develop merge --no-ff` and
`chore(spNN): …` subjects; the commit-msg hook accepted every subject.

| Step | Commit on `develop` | Validation run (command, result, artifact) |
|---|---|---|
| Lane 1 pre-merge assessment | — | Four read-only children, one per branch tip (SP-15 `db4ec2e`, SP-16 `03ba720`, SP-14 `154d2de`, SP-21 `803a94e`): build and vet green on all four; owning-package tests green except the known baseline; collision map predicted the SP-15/SP-16 config-surface overlap (five files, disjoint hunks) and the SP-15/SP-21 `stubs_test.go` adjacency. Journal: `subagents/workflows/wf_626f8d20-f98`. |
| SP-15 merge | `504f38f` | `go build ./...` 0, `go vet ./...` 0; analyzer, config, daemon, grammar, grammartest, rehydrate, replay `ok`; test/guards fails only `TestCarriedDefects_WaveReportRequiresResolution` (baseline). `scratchpad/m1-sp15-*.txt` |
| SP-16 merge | `0884ea8` | build/vet 0; checkpoint, config (golden merged clean), daemon, mcp, scheduler, store, analyzer, rehydrate `ok`; negknow `TestBudget_Open` and `TestBudget_DetectorScan` red under the nine-package co-load. Isolated re-run: `TestBudget_Open` red on both the merged tree and the pre-merge tree `504f38f` (`scratchpad/m2-negknow-quiet.txt`, `m2-negknow-premerge.txt`), so pre-existing on this host, not merge damage. |
| SP-14 merge | `33bb890` | build/vet 0; cli, commands, pluginmanifest, devtool `ok`. Whole tree `go test -p 1 -count=1 -timeout=30m ./...`: 66 package lines, failures exactly the baseline set (negknow `TestBudget_Open`; e2e `TestV3_HotPathUnchangedWithLedgerResident`; guards carried-defects; integration `TestIntegration_BeladyPMinLandsAtLowCoupling`, `TestIntegration_HotPathWarmWithRealResidentState`). Wall time 2 h 03 min, of which test/integration reported 6 292 s: the -timeout=30m never fired, which is only consistent with the machine having been suspended mid-run. `scratchpad/m3-wholetree.txt` |
| Lint on the SP-14 tree | — | `devtool lint` FAIL on two findings introduced by SP-15: golangci-lint `unparam` on `(*repSolver).reserve`, and runpatterns on a placeholder `-run …` in `plans/sdd/V5-SP-15/report-E.md`. The base `7c735ac` passes both (`scratchpad/base-runpatterns.txt`). |
| Two compatible fixes | `0dd982b` `refactor(analyzer): drop the unused reserve price result`; `6856cb2` `docs(sp15): waive the report-E column-header run placeholder` | `devtool lint --only=golangci-lint,runpatterns` PASS; analyzer package `ok`. |
| SP-21 merge (carries `feat/sp21-prerequisites` whole) | `87c0c1d` | build/vet 0; admission, contract, contracttest, devtool `ok`; guards baseline only; `TestV4_T13HandleResolvesAfterCompactionOverStdio` selected by `-list` and `ok`. Full `devtool lint`: all ten sub-checks PASS (`scratchpad/m4-lint.txt`). |
| `verify/v5` cut | branch at `87c0c1d`, worktree `../qompack-v5` | Seventeen authoring worktrees `../qompack-v5-x01..x17` (branches `v5/xNN`) and six fix worktrees off the same commit. |

**Order kept.** SP-15 → SP-16 → SP-14, then SP-21 on its own track after the SP-20 M1–M3 gate (SP-20 is
on `develop` since the V4 corrective candidate). `develop` is local-only; nothing was pushed.

---

## 1–16. Retained inventory, per subplan

The 274 retained row identifiers of the plan at HEAD `7f92af5` were extracted mechanically
(`scratchpad/v5-rows-7f92af5.json`; 274 rows, none dropped — the plan's own "260" count was a
mis-total) and reconciled one subplan per owner against `verify/v5` @ `87c0c1d`. Every row's current
definition, disposition, executed result, artifact path and old-to-new mapping is in the per-subplan
report named in the last column. Long, race, fuzz and timing gates were deferred to the serial quiet
pass (§17) and recorded `NOT-RUN` there, never `PASS`.

| SP | Rows | MAPPED | MAPPED-CMD | SUPERSEDED | RETIRED | MISSING | NEEDS-COORD | PASS | FAIL | BASELINE | CO-LOAD | NOT-RUN | Report |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| SP-01 | 24 | 15 | 5 | 2 | 1 | 0 | 1 | 19 | 1 | 0 | 1 | 3 | `plans/sdd/V5-VERIFY/inventory-SP-01.md` |
| SP-02 | 14 | 12 | 1 | 1 | 0 | 0 | 0 | 14 | 0 | 0 | 0 | 0 | `plans/sdd/V5-VERIFY/inventory-SP-02.md` |
| SP-03 | 13 | 13 | 0 | 0 | 0 | 0 | 0 | 11 | 0 | 0 | 0 | 2 | `plans/sdd/V5-VERIFY/inventory-SP-03.md` |
| SP-04 | 17 | 16 | 1 | 0 | 0 | 0 | 0 | 13 | 0 | 0 | 0 | 4 | `plans/sdd/V5-VERIFY/inventory-SP-04.md` |
| SP-05 | 20 | 18 | 1 | 1 | 0 | 0 | 0 | 19 | 0 | 0 | 0 | 1 | `plans/sdd/V5-VERIFY/inventory-SP-05.md` |
| SP-06 | 18 | 16 | 1 | 1 | 0 | 0 | 0 | 16 | 0 | 0 | 0 | 2 | `plans/sdd/V5-VERIFY/inventory-SP-06.md` |
| SP-07 | 11 | 11 | 0 | 0 | 0 | 0 | 0 | 10 | 0 | 0 | 0 | 1 | `plans/sdd/V5-VERIFY/inventory-SP-07.md` |
| SP-08 | 15 | 13 | 0 | 1 | 1 | 0 | 0 | 13 | 0 | 0 | 2 | 0 | `plans/sdd/V5-VERIFY/inventory-SP-08.md` |
| SP-09 | 14 | 13 | 0 | 1 | 0 | 0 | 0 | 13 | 0 | 1 | 0 | 0 | `plans/sdd/V5-VERIFY/inventory-SP-09.md` |
| SP-10 | 18 | 11 | 0 | 4 | 3 | 0 | 0 | 16 | 0 | 0 | 1 | 1 | `plans/sdd/V5-VERIFY/inventory-SP-10.md` |
| SP-11 | 19 | 16 | 0 | 0 | 2 | 1 | 0 | 19 | 0 | 0 | 0 | 0 | `plans/sdd/V5-VERIFY/inventory-SP-11.md` |
| SP-12 | 21 | 19 | 0 | 0 | 2 | 0 | 0 | 20 | 0 | 0 | 0 | 1 | `plans/sdd/V5-VERIFY/inventory-SP-12.md` |
| SP-13 | 19 | 14 | 1 | 4 | 0 | 0 | 0 | 17 | 0 | 0 | 1 | 1 | `plans/sdd/V5-VERIFY/inventory-SP-13.md` |
| SP-14 | 16 | 7 | 1 | 3 | 1 | 4 | 0 | 12 | 0 | 0 | 0 | 4 | `plans/sdd/V5-VERIFY/inventory-SP-14.md` |
| SP-15 | 18 | 9 | 0 | 3 | 2 | 4 | 0 | 14 | 0 | 0 | 0 | 4 | `plans/sdd/V5-VERIFY/inventory-SP-15.md` |
| SP-16 | 17 | 2 | 1 | 8 | 2 | 4 | 0 | 13 | 0 | 0 | 0 | 4 | `plans/sdd/V5-VERIFY/inventory-SP-16.md` |

**Totals.** 274 rows: 205 MAPPED, 12 MAPPED-CMD, 29 SUPERSEDED-BY-WAVE4, 14 RETIRED, 13 MISSING,
1 NEEDS-COORDINATOR (ruled in §23). Executed results: 239 PASS, 1 FAIL (I-01.18, a retired grep whose
replacement gate is the stubskips lint), 1 FAIL-BASELINE (I-09.14 `TestBudget_Open`), 5
FAIL-COLOAD-SUSPECT (I-01.24, I-08.13, I-08.15, I-10.17, I-13.19 — each re-run alone in §17/§20), 28
NOT-RUN (66 deferred commands, all run in the quiet pass or recorded MISSING with an owner).

**Additive rows (no renumbering).** I-06.19 (SP-20 M1 backup/import/GC/rollback drill; evidence
`internal/store/backup_test.go`, `migrate_test.go`; run in the whole-tree step) and I-14.17 (SP-19 M0-G5
request ledger: missing telemetry is unknown, never zero; `TestEval_MissingUsageIsUnknownNotZero` plus
the four ledger-accessor tests, 5 PASS, `scratchpad/i14-17-usage.txt`).

**MISSING rows and their owners.** I-11.19 (L5 benchmarks; `internal/rehydrate`, SP-17 hardening);
I-14.5 (RETIRED-with-pointer to 4.15); I-14.14 (commands conformance suite; SP-14 follow-up);
I-14.15 (RETIRED-with-pointer to 4.1); I-14.16 (command benchmarks; SP-17); I-15.4, I-15.6, I-15.18
(grammar/analyzer fuzz, size budget, benchmarks; SP-15/SP-17 — a silent-success class, see Q54);
I-15.14 (checkpoint action-history fold; retired, Q55); I-16.9 (tuned truncation reserves; retired,
Q61); I-16.15 (phase-7 e2e seam; `unsupported`, no production call site); I-16.16 (phase-7 budgets;
SP-16/V6); I-16.17 (warm-started CMS bound; SP-16/V6, no producer to test).

**How to read three cells.** I-11.19 is counted PASS on the three clauses that were measured; its L5-BUILD
clause has no benchmark (MISSING, Q39), so that PASS is partial. The table keeps each inventory's own
scoring: rulings Q48, Q51, Q55 and Q61 later retired I-14.5, I-14.15, I-15.14 and I-16.9, which the
MISSING list above marks, so four of the 13 MISSING are retired by ruling. I-01.19 (the golden and
contract command deferred to the whole tree) is discharged by B1, which ran every package's tests; B3 re-ran them on the report commit `b1a0183`, where only the three carried tests fail (§17).

---

## 17. Whole-tree and retained cross-wave gates (serial quiet pass)

The quiet pass ran as several passes on `verify/v5`, and `scratchpad/quiet/summary.txt` keeps every
line of every one. The commit each group of rows ran on, and what else shared the machine:

- **Fuzz campaigns D1–D18** (the `fuzz pass`): started on `e0eeea3`. The section-4 batch
  `783ad21`..`5ec96af` landed in the same worktree at 08:04 while D10 ran, so D11–D18 compiled against
  that tree, and other agents authored fix items F1–F3 during the pass (the D13 stall, item 15). None of
  the twelve fuzzed packages changed between `e0eeea3` and `b1a0183`, and none imports the production
  code that did change (`internal/commands`, `internal/daemon/drain.go`).
- **The first quiet pass on `7e0aac5`** (race rows C2–C16, whole-tree B1, lint B2): its lines come from
  two overlapping processes (a race suite ran beside its B1), before any lock directory existed. Its
  B1 is kept only as the run that surfaced §22 items 16–17; its other failures are item 30.
- **Functional rows A1–A5, B1, C1, F2–F7, G1, G2, I1:** `scratchpad/quiet-pass-final2.sh` (A1–A5) and
  `scratchpad/quiet-pass-resume2.sh` (the rest, under a lock directory), both on `b64b3f6` after the F4
  fix. The earlier attempts on that commit ran two whole-tree runs at once and are superseded (item
  22a); the B-B diagnosis ran beside resume2's last steps (H2, I1, F7). None of these rows depends on
  the clock.
- **Benchmark and timing rows E1–E13, F1, H1, H2:** `scratchpad/bench-window.sh` on `0d5c999`; the
  confirmation rows E4b–E9c, F1b and F1c by `scratchpad/bench-window2.sh` on `1a1bbaf`. Each step waited
  for the power and foreign-load probe it records (§20 gives each row's clock regime).
- **The report commit `b1a0183`:** lint B2, and the whole tree re-ran on `b1a0183` in a separate worktree (B3, failing only on the three carried tests: SP20-D1's two B-B tests and the Belady floor), as did the race detector over the three packages changed after `b64b3f6` (C1b, no data race).

Every commit after `b64b3f6` changes only tests, `tools/devtool` and documents
(`git diff --stat b64b3f6..b1a0183`). A step writes one output file per name, so a re-run replaces the
earlier run's file; where a cited first-pass file was replaced, §22 names the surviving copy instead. Per-step outputs are under
`scratchpad/quiet/`, summary in `scratchpad/quiet/summary.txt`.

| Step | Gate | Run | Result |
|---|---|---|---|
| A1 | fmt-check (gofumpt) | `A1-fmt-check` (final2, exit 0, 4 s) | PASS |
| A2 | go build ./... | `A2-build` (final2, exit 0, 2 s) | PASS |
| A3 | go vet ./... | `A3-vet` (final2, exit 0, 6 s) | PASS |
| A4 | devtool build-all | `A4-build-all` (final2, exit 0, 13 s) | PASS |
| A5 | devtool plugin-validate | `A5-plugin-valid` (final2, exit 0, 1 s) | PASS |
| B2 | devtool lint (all ten sub-checks) | `b1a0183`: one full run (`scratchpad/quiet/B2-lint-final.txt`) passed golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck and stubskips before the harness's low-memory watchdog killed it inside runpatterns; runpatterns and docmarkers re-run detached (`B2-lint-rest.txt`, the `B2b-lint-rest` line of `summary.txt`), coveragefloors alone (`B2c-coveragefloors.txt`); the closing commit re-ran the three plan-document checks and the guards on its own edit before committing | PASS, all ten |
| B3 | whole tree `go test -p 1 -count=1 -timeout=30m ./...` on `b1a0183`, in a separate detached worktree | `B3-wholetree-b1a0183` (`b3-final.sh`, exit 1, 2001 s) | 68 package lines: 65 ok, one without tests, `test/e2e` and `test/integration` FAIL on carried tests only. `TestV3_HotPathUnchangedWithLedgerResident` and `TestIntegration_HotPathWarmWithRealResidentState` fail on B-B alone (p50 786 / 852 ms against 2 ms; B-A p99 5.1 / 4.1 ms, inside 15 ms; SP20-D1); `TestIntegration_BeladyPMinLandsAtLowCoupling` reads 61.5 % against its 70 % floor (deterministic, V4 sign-off item). Against B1, the guards carried-defects gate now passes, and so do negknow's `TestBudget_Open` and `TestBudget_DetectorScan` (clock-bound, SP09-D1; the run took no power probe). |
| C1b | race detector on the three packages changed after `b64b3f6` (`tools/devtool`, `test/guards`, `internal/daemon`), on `b1a0183` | `C1b-race-changed` (`b3-final.sh`, exit 1, 5887 s wall) and `C1b-race-guards-rerun` (`c1b-guards.sh`, exit 0, 105 s) | `tools/devtool` (4.0 s) and `internal/daemon` (56.7 s) ok. `test/guards` printed nothing and the go command killed it at its 33 m limit: the host entered Modern Standby at 15:32:28, about 90 s after C1b started, and left it at 17:09:02 (System log, Kernel-Power 506/507), so the binary was frozen, not hung. Re-run alone with the display held awake (`awake.ps1`; no standby entry during the run): ok, 97.702 s. No data race in any of the three. |
| B1 | whole tree `go test -p 1 -count=1 ./...` (68 package lines) | `B1-wholetree` (resume2, exit 1, 2200 s) | 63 ok; failures = baseline set (negknow `TestBudget_Open` (585.9 ms CPU/op at 55 % processor performance, clock-bound: SP09-D1), e2e `TestV3_HotPathUnchangedWithLedgerResident`, guards carried-defects gate before the tsv update, integration Belady floor and `HotPathWarm`) plus `TestBudget_DetectorScan` (re-taken alone in F1). x09, x13, the drain pin and the SP08-D2 evidence pass. |
| C1 | devtool test-race (whole tree, -race) | `C1-test-race` (resume2, exit 1, 1796 s) | 64 ok (42 of them cached results of earlier race runs of unchanged packages), no data race; only the guards gate and the two integration rows above fail |
| C2 | race row C2 (see quiet-pass.sh) | `C2-race-cp-pins` (quiet, exit 0, 171 s) | PASS |
| C3 | race row C3 (see quiet-pass.sh) | `C3-race-l5` (quiet, exit 0, 10 s) | PASS |
| C4 | race row C4 (see quiet-pass.sh) | `C4-race-sched` (quiet, exit 0, 73 s) | PASS |
| C5 | race row C5 (see quiet-pass.sh) | `C5-I-05.3` (quiet, exit 0, 15 s) | PASS |
| C6 | race row C6 (see quiet-pass.sh) | `C6-I-05.4` (quiet, exit 0, 13 s) | PASS |
| C7 | race row C7 (see quiet-pass.sh) | `C7-I-05.6` (quiet, exit 0, 16 s) | PASS |
| C8 | race row C8 (see quiet-pass.sh) | `C8-I-06.8` (quiet, exit 0, 33 s) | PASS |
| C9 | race row C9 (see quiet-pass.sh) | `C9-I-07.3` (quiet, exit 0, 11 s) | PASS |
| C10 | race row C10 (see quiet-pass.sh) | `C10-I-08.3` (quiet, exit 0, 12 s) | PASS |
| C11 | race row C11 (see quiet-pass.sh) | `C11-I-09.6` (quiet, exit 0, 10 s) | PASS |
| C12 | race row C12 (see quiet-pass.sh) | `C12-I-12.8` (quiet, exit 0, 7 s) | PASS |
| C13 | race row C13 (see quiet-pass.sh) | `C13-I-12.10` (quiet, exit 0, 12 s) | PASS |
| C14 | race row C14 (see quiet-pass.sh) | `C14-I-13.2` (quiet, exit 0, 9 s) | PASS |
| C15 | race row C15 (see quiet-pass.sh) | `C15-I-13.16` (quiet, exit 0, 11 s) | PASS |
| C16 | race row C16 (see quiet-pass.sh) | `C16-I-15.1` (quiet, exit 0, 9 s) | PASS |
| D1 | fuzz campaign D1 (see quiet-pass.sh) | `D1-I-01.8` (quiet (fuzz section), exit 0, 67 s) | PASS, no crasher |
| D2 | fuzz campaign D2 (see quiet-pass.sh) | `D2-I-01.13` (quiet (fuzz section), exit 0, 74 s) | PASS, no crasher |
| D3 | fuzz campaign D3 (see quiet-pass.sh) | `D3-I-02.9` (quiet (fuzz section), exit 0, 80 s) | PASS, no crasher |
| D4 | fuzz campaign D4 (see quiet-pass.sh) | `D4-I-03.11-bloom` (quiet (fuzz section), exit 0, 71 s) | PASS, no crasher |
| D5 | fuzz campaign D5 (see quiet-pass.sh) | `D5-I-03.11-cms` (quiet (fuzz section), exit 0, 63 s) | PASS, no crasher |
| D6 | fuzz campaign D6 (see quiet-pass.sh) | `D6-I-03.11-hll` (quiet (fuzz section), exit 0, 64 s) | PASS, no crasher |
| D7 | fuzz campaign D7 (see quiet-pass.sh) | `D7-I-03.11-mg` (quiet (fuzz section), exit 0, 63 s) | PASS, no crasher |
| D8 | fuzz campaign D8 (see quiet-pass.sh) | `D8-I-03.11-sig` (quiet (fuzz section), exit 0, 63 s) | PASS, no crasher |
| D9 | fuzz campaign D9 (see quiet-pass.sh) | `D9-I-04.7-split` (quiet (fuzz section), exit 0, 130 s) | PASS, no crasher |
| D10 | fuzz campaign D10 (see quiet-pass.sh) | `D10-I-04.7-strm` (quiet (fuzz section), exit 0, 123 s) | PASS, no crasher |
| D11 | fuzz campaign D11 (see quiet-pass.sh) | `D11-I-04.13-can` (quiet (fuzz section), exit 0, 135 s) | PASS, no crasher |
| D12 | fuzz campaign D12 (see quiet-pass.sh) | `D12-I-04.13-res` (quiet (fuzz section), exit 0, 428 s) | PASS, no crasher |
| D13 | fuzz campaign D13 (see quiet-pass.sh) | `D13-I-04.15` (quiet (fuzz section), exit 0, 6840 s) | PASS, no crasher |
| D14 | fuzz campaign D14 (see quiet-pass.sh) | `D14-I-05.2` (quiet (fuzz section), exit 0, 69 s) | PASS, no crasher |
| D15 | fuzz campaign D15 (see quiet-pass.sh) | `D15-I-06.2` (quiet (fuzz section), exit 0, 79 s) | PASS, no crasher |
| D16 | fuzz campaign D16 (see quiet-pass.sh) | `D16-I-08.2` (quiet (fuzz section), exit 0, 72 s) | PASS, no crasher |
| D17 | fuzz campaign D17 (see quiet-pass.sh) | `D17-I-10.10` (quiet (fuzz section), exit 0, 68 s) | PASS, no crasher |
| D18 | fuzz campaign D18 (see quiet-pass.sh) | `D18-I-13.3` (quiet (fuzz section), exit 0, 70 s) | PASS, no crasher |
| E1 | I-01.24 obs/config/paths/cli benchmarks | `E1-I-01.24` (bench-window, exit 0, 17 s; power=AC charge=34 perf=157 foreign=4.9) | see §20 |
| E2 | I-03.13 sketch benchmarks | `E2-I-03.13` (bench-window, exit 0, 39 s; power=AC charge=35 perf=114 foreign=2.6) | see §20 |
| E3 | I-04.17 chunk/canon/symbols benchmarks | `E3-I-04.17` (bench-window, exit 0, 37 s; power=AC charge=37 perf=131 foreign=4) | see §20 |
| E4 | I-05.20 ipc/daemon benchmarks | `E4-I-05.20` (bench-window, exit 0, 21 s; power=AC charge=39 perf=210 foreign=2.3) | see §20 |
| E5 | I-06.18 store/tokens benchmarks | `E5-I-06.18` (bench-window, exit 0, 190 s; power=AC charge=40 perf=167 foreign=3.6) | see §20 (SP06-D2) |
| E6 | I-07.11 dag benchmarks | `E6-I-07.11` (bench-window, exit 0, 48 s; power=AC charge=44 perf=168 foreign=3.9) | see §20 |
| E7 | I-08.15 observer OnToolUse/Tombstone | `E7-I-08.15` (bench-window, exit 0, 24 s; power=AC charge=45 perf=214 foreign=2) | see §20 (SP08-D1) |
| E8 | I-09.14 negknow benchmarks | `E8-I-09.14` (bench-window, exit 0, 24 s; power=AC charge=46 perf=196 foreign=2.4) | see §20 |
| E9 | I-10.17 checkpoint benchmarks | `E9-I-10.17` (bench-window, exit 0, 245 s; power=AC charge=47 perf=170 foreign=2.8) | see §20 (SP10-D1) |
| E10 | I-11.19 rules/skills benchmarks | `E10-I-11.19` (bench-window, exit 0, 17 s; power=AC charge=52 perf=132 foreign=2.8) | see §20 |
| E11 | I-12.21 scheduler/daemon benchmarks | `E11-I-12.21` (bench-window, exit 0, 46 s; power=AC charge=53 perf=166 foreign=2.2) | see §20 |
| E12 | I-15.18 analyzer/grammar (no benchmarks exist) | `E12-I-15.18` (bench-window, exit 0, 3 s; power=AC charge=54 perf=204 foreign=2.7) | ok having measured nothing (MISSING, Q54) |
| E13 | I-16.16 Truncate / Evaluate_64Candidates | `E13-I-16.16` (bench-window, exit 0, 9 s; power=AC charge=55 perf=203 foreign=2.6) | see §20 |
| F1 | CI timing rows (`TestBudget_*`, GC deadline, `TestBudgetBF`, `HotPathWarm`), alone | `F1-ci-timing` (bench-window, exit 1, 180 s; power=AC charge=56 perf=225 foreign=5) | see §20 |
| F2 | I-08.13 observer-through-daemon e2e set, alone | `F2-I-08.13-e2e` (resume2, exit 0, 20 s) | PASS (the inventory's co-load suspect does not reproduce) |
| F3 | `TestV3_HotPathUnchangedWithLedgerResident`, alone | `F3-hotpath-e2e` (resume2, exit 1, 293 s) | FAIL on its B-B row only (B-A inside): the fsync-bound leased Accept of SP20-D1, taken on battery in resume2 and not re-taken because H1 measured the same row on AC (§20) |
| F4 | `TestIntegration_BeladyPMinLandsAtLowCoupling` | `F4-belady` (resume2, exit 1, 2 s) | FAIL, deterministic: 61.5 % vs the 70 % floor (V4 sign-off item, unchanged; not a timing row) |
| F5 | the sketch half of I-16.17 (four pre-existing CMS bound tests; I-16.17's own closing test does not exist, MISSING) | `F5-I-16.17` (resume2, exit 0, 4 s) | PASS |
| F6 | all seventeen `TestV5_*` section-4 tests in one serial run | `F6-v5-section4` (resume2, exit 0, 159 s) | PASS |
| F7 | x09 + x13 (the two tests F4 corrected), alone | `F7-corrected-e2e` (resume2, exit 0, 43 s) | PASS |
| G1 | devtool replay | `G1-replay` (resume2, exit 0, 6 s) | PASS |
| G2 | devtool replay --ci | `G2-replay-ci` (resume2, exit 0, 2 s) | PASS |
| H1 | devtool bench-hotpath (2 000 hooks, warm daemon) | `H1-bench-hotpath` (bench-window, exit 1, 77 s; power=AC charge=60 perf=161 foreign=4.8) | B-A PASS, B-B FAIL (item 20, SP20-D1), B-E PASS; see §20 |
| H2 | devtool bench (sweep + bench-compare vs testdata/bench-baseline.txt) | `H2-bench` (bench-window, exit 0, 446 s; power=AC charge=61 perf=208 foreign=3.1) | see §20 |
| I1 | devtool cover | `I1-cover` (resume2, exit 1, 773 s) | go test -coverprofile exits 1 on the same known failures as B1, so the floor stage did not run; per-package figures in the file, wave-4 packages 88.0 / 88.2 / 91.4 / 100.0 % against 75 / 85 / 75 / 90 (item 21); floors bind on CI |
| E9b | `BenchmarkFinalize` `-count=6` (SP10-D1 confirmation) | `E9b-finalize-count6` (bench-window2, exit 0, 372 s; power=battery charge=55 perf=92 foreign=7.1) | 66.6–73.1 ms/op at base clock, over; SP10-D1 → deferred (§20, §21) |
| E5b | I-06.18 `GetChunk` / `OpenSpan` / `Search` `-count=3` | `E5b-store-count3` (bench-window2, exit 0, 187 s; power=battery charge=46 perf=95 foreign=6.2) | 224–321 µs / 316–333 µs / 242–271 ms at base clock, the same allocations where recorded (its `Search` lines carry none); SP20-D2 confirmed (§20) |
| E6b | `BenchmarkAddToolUse` at the baseline regime, `-benchtime 20x -count=6` | `E6b-dag-regime` (bench-window2, exit 0, 4 s; power=AC charge=45 perf=96 foreign=3.8) | 10.4–16.1 µs/op = 2.1–3.2 µs per pair, five of six inside; not a regression (§20) |
| E4b | `BenchmarkIngestAccept` / `BenchmarkIngestAcceptLeased` `-benchtime 200x -count=3` | `E4b-accept-count3` (bench-window2, exit 0, 18 s; power=AC charge=46 perf=161 foreign=7.3) | 2.05–2.19 ms / 22.96–23.30 ms on AC turbo; SP20-D1 confirmed (§20) |
| F1b | `TestBudget_Open` / `TestBudget_DetectorScan` `-count=3`, alone | `F1b-negknow-count3` (bench-window2, exit 0, 15 s; power=AC charge=48 perf=120 foreign=4.5) | PASS 3 of 3 at 120 % (257.8–273.4 ms CPU/op against 300); SP09-D1 opened for the clock regime (§21) |
| F1c | `TestGC_DeadlineTruncatesAndResumes` / `…OvershootIsBounded…` `-count=3`, alone | `F1c-gcdeadline-count3` (bench-window2, exit 0, 119 s; power=AC charge=50 perf=95 foreign=4.7) | PASS 3 of 3 at a steady clock (§22 item 23) |
| E9c | `BenchmarkFinalize` `-count=6` repeat | `E9c-finalize-count6` (bench-window2, exit 0, 377 s; power=AC charge=54 perf=128 foreign=3.5) | 63.6–70.3 ms/op on AC at 128 %, over; SP10-D1 → deferred (§21) |

**Fuzz campaigns (18, all green).** Config `FuzzConfigLoad`, hookio `FuzzReadEvent`, eval `FuzzRedact`,
sketch ×5 (`FuzzBloomUnmarshalBinary`, CMS, HLL, MisraGries, Signature), chunk `FuzzSplit` and
`FuzzSplitStream` (120 s), canon `FuzzCanonicalize` and `FuzzRestore` (120 s), symbols `FuzzExtract`
(120 s), ipc `FuzzDecodeRequest`, redact `FuzzRedactIdempotent`, observer `FuzzExtractSignals`,
checkpoint `FuzzParseGitIndex`, mcp `FuzzServeLine`; `-parallel 4`. No crasher, no new failing input.
Anomaly: the symbols campaign spent 111 minutes in its baseline-coverage phase before fuzzing normally
for two minutes; a re-run of the same phase alone (`-fuzztime 1x`, 861 corpus entries) completed in
1.4 s (`scratchpad/fuzz-symbols-baseline.txt`), so the stall was a transient worker hang under the
concurrent fix-batch load, not a slow input. Recorded, not chased further.

---

## 18. Exit-criteria re-verification (plan §3)

| Criterion | Status | Evidence |
|---|---|---|
| R2 run map: focused/parallel groups, justified long gates, instrumented coverage, candidate identity, incomplete results | `verified_in_target` | §0 (lane map), §17 (every long gate named with its reason), §1–16 NOT-RUN cells, every run names its commit (§17: fuzz `e0eeea3`, race rows `7e0aac5`, functional rows `b64b3f6`, benchmark windows `0d5c999` and `1a1bbaf`, lint `b1a0183`); nothing after `b64b3f6` touches production code, and the whole tree re-ran on `b1a0183` in a separate worktree (B3, failing only on the three carried tests: SP20-D1's two B-B tests and the Belady floor), as did the race detector over the three packages changed after `b64b3f6` (C1b, no data race); no per-row whole-tree runs; coverage from `devtool cover` in §17. |
| V4 mandatory results and disabled capabilities carried without rewriting history | `verified_in_target` | V4-report.md untouched; its four open items in §29; disabled capabilities in §24. |
| SP-14 command/JSON/exit/status/request-ledger behaviour matches the supported plugin | `verified_in_target` with four gaps | I-14.x (12 PASS), 4.1, 4.15, F1 (§22); `plugin-validate` in §17. Gaps: the H3 `/qompack:checkpoint` route is absent, so `TestE2E_CheckpointNowThenStatus` cannot be written (`inventory-SP-14.md`); `Deps.EvalArtifacts` is unbound (`unsupported`, Q53); the I-14.14 conformance suite and the I-14.16 benchmarks are MISSING (owners SP-14, SP-17). |
| SP-15 selection reaches the real consumer with one representation, closure, deterministic ties, complete overhead/overflow | `verified_in_target` for the in-process consumer, `implemented_unverified` end-to-end | 4.6, 4.7, 4.9, I-15.x; the selector ships disabled behind `runtime.selection.submodularEnabled` (recorded disabled, not passed). |
| SP-15 warnings progress-aware, bounded, warning-only; grammar complexity justified or optional | `verified_in_target` (warning-only, degraded arm) / `unsupported` (state-aware detector not composed in production) | 4.10; SP-15 §3a.3 (Sequitur ablation: optional-and-disabled). |
| SP-16 scope/applicability/expiry/privacy and bounded retrieval; promotion has no native residency claim | `verified_in_target` at the package seams, `unsupported` end-to-end | 4.8, 4.11, 4.12; every phase-7 switch ships false and has no production call site (Q59). |
| SP-21 enabled surfaces satisfy all T21 gates; unsafe outcomes safe | `verified_in_target` for the foundation, no enabled surface | 4.17; SP-21 scenario status: eight Passing, T21-HOST-01 accepted unverified disposition, T21-QUALITY-01 inconclusive by construction; admission is off and recorded as disabled. |

---

## 19. Section-4 cross-component tests

All seventeen retained identifiers were authored on `verify/v5`, each by an author in its own worktree,
then adversarially reviewed (test re-run twice, negative control reproduced, criterion strength and
disposition honesty checked) and repaired until accepted; 4.1 needed two repair rounds, 4.5 one. Each
test's disposition file (`plans/sdd/V5-VERIFY/xNN-disposition.md`) carries the old-to-new map for every
historical expectation, the negative control and its proof, and the unverified remainder. The
identifiers are kept as the top-level test names.

| Row | Identifier | Level / file | Disposition | Negative control | Unverified remainder (owner) |
|---|---|---|---|---|---|
| 4.1 | `TestV5_ObserveToStatusRoundTrip` | e2e, `test/e2e/v5_x01_test.go` | authored | Three controls, all re-proved on the fixed tree. (1) Runtime switch: admin.shutdown, status must fall back to metrics/latency.json and (new this round) the persisted hook_controlled and l0_ingest HistSnapshots must equal the served ones quantile-for-quantile when N is unchanged. Full text: `plans/sdd/V5-VERIFY/x01-disposition.md`. | Unchanged from the prior rounds: (1) SP-14 status against SP-12 scheduler / SP-16 warm-start surfaces (historical frontier, scheduler.breakdown) — daemon.Options.StatusExtra is bound by nothing in the shipped composition, so snapshot.extra is absent and there is no producer; recorded as unverified, not passed. Full text: `plans/sdd/V5-VERIFY/x01-disposition.md`. |
| 4.2 | `TestV5_TombstoneToExpandRoundTrip` | e2e, `test/e2e/v5_x02_test.go` | authored | Real fault, no runtime switch needed: with the daemon stopped, the test opens the store, writes a record whose stored path is ../outside-the-root/secrets.ts (v4_x05's shape), closes the store, and overwrites the first chunk object file of the captured root with garbage (FSStore.getObject verifies size/decoding/content address, so the store quarantines it). Full text: `plans/sdd/V5-VERIFY/x02-disposition.md`. | (1) SP-13 does not yet consume SP-20's retrieval evidence envelope: the plan row "SP-13 consumes the M2 retrieval envelope and distinguishes unavailable from absence without changing its negotiated versions" is unchecked and internal/mcp references no core.Fidelity, so expand/re_read responses carry no fidelity field. Full text: `plans/sdd/V5-VERIFY/x02-disposition.md`. |
| 4.3 | `TestV5_HookEventToTombstoneToRetrievalAfterRestart` | e2e, `test/e2e/v5_x03_test.go` | authored | Three real switches, no source edit needed. (1) Built-in: the drain context is cancelled after the 3rd spooled handler completion via a probe Bind that wraps Services.ObserveTool (the real observer still runs). Full text: `plans/sdd/V5-VERIFY/x03-disposition.md`. | (1) A real process kill of a spawned daemon is not exercised: on Windows a killed daemon's lock is reclaimable only after the 90 s heartbeat staleness window, so the restart is a second in-process composition over the same tree (v4 harness convention); process death between an fsync and a rename is not modelled. Full text: `plans/sdd/V5-VERIFY/x03-disposition.md`. |
| 4.4 | `TestV5_PreCompactToRehydrateToDroppedRoundTrip` | e2e, `test/e2e/v5_x04_test.go` | authored | Two real severings, no source edit needed. (1) Runtime switch: the subtest negative_control_reinjection_disabled runs a project with runtime.migration.reinjection.sessionStartCompact=false (Qompack.md v1.5 Appendix C kill switch, read by the daemon at composition); with it off, SessionStart(compact) carries no <!-- qompack:injected span and nothing but the route's contract probe line, no state/rehydrate-<sess>.json e. Full text: `plans/sdd/V5-VERIFY/x04-disposition.md`. | (1) Item 7 rendered under a tight budget: on this tree a 600-token budget trips the hard-cap re-truncation and item 7 is the first discretionary item evicted (ADR 0011 §18; frozen in the 400-token degraded golden), so coverage under eviction is verified through state.degraded=true, item 8's dropped() affordance and the tool's complete persisted list, not through a rendered item 7. Full text: `plans/sdd/V5-VERIFY/x04-disposition.md`. |
| 4.5 | `TestV5_EliminationThroughEveryFourSurfaces` | e2e, `test/e2e/v5_x05_test.go` | partial | Runtime switch (unchanged from the prior version): subtest 4 replaces records/eliminations.jsonl with a directory, restarts the daemon, and asserts already_tried answers unavailable/degraded (never absent/stale) for both records, `qompack pin --eliminated --json` exits 1 with ok:false and a non-empty is_error data member, and status carries negknow.bloom.blind_mode >= 1. Full text: `plans/sdd/V5-VERIFY/x05-disposition.md`. | (1) Error state on the digest surface: a blind ledger renders the checkpoint's frozen copy [active] (rehydrate.eliminationCandidates falls back on ErrNotFound); observed and logged every run, not asserted; defect candidate for the SP-11/SP-13 digest owner, holds 4.5 at PARTIAL. Full text: `plans/sdd/V5-VERIFY/x05-disposition.md`. |
| 4.6 | `TestV5_SelectorGatedByRealScheduler` | e2e, `test/e2e/v5_x06_test.go` | authored | Subtest closing_the_runtime_closes_the_gate: a real switch on the shipped shutdown path. daemon.CloseSchedulerRuntime(rt) is called on the runtime whose construction opened the gate; scheduler.PSelectionAvailable() is then false, and the SAME p, real DAG blocks, real cheap-scorer deltas and lambda that constructed a Selector in the previous subtest are refused by analyzer.NewSelector with core.ErrNotImplemented (nil. Full text: `plans/sdd/V5-VERIFY/x06-disposition.md`. | (1) The operator switch runtime.selection.submodularEnabled (default false) and the daemon's rehydrateService.selectionFor path, which returns nil when the switch is off or the gate refuses, are not driven here; that is the rehydration-consumer seam of section 4.7. test/guards TestGuard_SubmodularDefaultsOff pins the default; this row records the feature as shipped-disabled at the daemon and does not claim it passed there. Full text: `plans/sdd/V5-VERIFY/x06-disposition.md`. |
| 4.7 | `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint` | e2e, `test/e2e/v5_x07_test.go` | authored | Two real runtime switches, each severed in its own subtest under the same 120-token pressure that makes the selector's decision visible in the consumer's drop report (read back via the real rehydrate.Reporter): (1) runtime.selection.submodularEnabled=false with the closing-note-3 gate open (real daemon.NewSchedulerRuntime constructed). Full text: `plans/sdd/V5-VERIFY/x07-disposition.md`. | (1) The p the selector reasons with (0) is not observable at the consumer; covered by internal/daemon/rehydrate_selection_test.go, not re-asserted here. (2) Arm 1's record-id-ascending order assertion is a necessary condition of the selection path, not sufficient alone; sufficiency rests on arms 2-4 and the red-run. Full text: `plans/sdd/V5-VERIFY/x07-disposition.md`. |
| 4.8 | `TestV5_GrammarAndPromotionCoexistInFinalize` | integration, `test/integration/v5_x08_test.go` | partial | Four layers, all through real switches or fault paths. (1) Real config switch both ways on identical inputs: runtime.phase7.retrieval.demandPromotion as the real loader resolved it (false. Full text: `plans/sdd/V5-VERIFY/x08-disposition.md`. | (1) Grammar fold into the checkpoint narrative: no producer on this tree -- internal/checkpoint only nil-checks SourceSet.Grammar and Compressed() is read only by internal/grammar and the replay harness's diagnostic counter; the row asserts the fold ABSENT (no "grammar-compressed" narrative text, sketch_refs exactly {tried, touch}, no grammar/actions.seq) so a future wiring fails it. Full text: `plans/sdd/V5-VERIFY/x08-disposition.md`. |
| 4.9 | `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly` | integration, `test/integration/v5_x09_test.go` | authored | Two real-switch controls inside the top-level test, no source edit needed. NC1: checkpoint.Promote re-run with Enabled = Cfg.Runtime.Phase7.Retrieval.DemandPromotion (asserted false, the shipped default) is report-only, Order is empty, Apply leaves the writer's order; at the witness budget where the promoted run kept the oldest pointer while cutting a peer, the same pointer is now absent (cut first). Full text: `plans/sdd/V5-VERIFY/x09-disposition.md`. | (1) The daemon's unexported selectionFor Proposal-to-SelectionOutcome translation is mirrored, not called; covered by internal/daemon/rehydrate_selection_test.go, not this row. Full text: `plans/sdd/V5-VERIFY/x09-disposition.md`. |
| 4.10 | `TestV5_ThrashWarningVisibleInStatusAndCheckpoint` | e2e, `test/e2e/v5_x10_test.go` | authored | Real runtime switch, arm 2: a contract.Monitor on state/contract.json is degraded to ModeDegradedPassive before the daemon exists (the persisted seam v3_x08 and the SP-10 degraded row use). Full text: `plans/sdd/V5-VERIFY/x10-disposition.md`. | (1) grammar.Detector (SP-15's progress-aware dedup and self-origin classifier) is not constructed by any production composition root on this tree and runtime.selection.loopWarningsEnabled defaults to false; arm 3 records the switch as disabled (loaded config and `qompack config print --json`) and exercises the detector in process, but makes no end-to-end claim that state-aware warnings reach a prompt hook. Full text: `plans/sdd/V5-VERIFY/x10-disposition.md`. |
| 4.11 | `TestV5_SegmentBloomNarrowsRecall` | integration, `test/integration/v5_x11_test.go` | authored | Real runtime switch, no source edit: store.SweepSegmentFilters(ctx, root, map[string]bool{}) (the producer's own maintenance entry point with an empty keep set) removes all 12 filter files while the append-only segment log still names them. Full text: `plans/sdd/V5-VERIFY/x11-disposition.md`. | (1) End-to-end narrowing of `recall`: no consumer on this tree reads Segment.BloomRef during retrieval (recall's handler calls store.Search, which never consults filters), so "filters narrow recall" is asserted at the store seam (filters narrow the segment set. Full text: `plans/sdd/V5-VERIFY/x11-disposition.md`. |
| 4.12 | `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession` | integration, `test/integration/v5_x12_test.go` | partial | Three real switches, each paired with its restored path in the same subtest: (1) the shipped config runtime.phase7.reuse.scopedCandidates=false from the real config.Load result — identical candidate and grant yield Enabled=false, Considered=1, Allowed=1 and zero candidates offered (recorded as disabled, never as passed). Full text: `plans/sdd/V5-VERIFY/x12-disposition.md`. | (1) Warm improvement of the first compaction: not asserted and not assertable — the criterion guarantees none, runtime.phase7.reuse.warmPrior ships false, and no production seam applies a prior; recorded as disabled, not passed (internal/scheduler/warmprior_ablation_test.go remains the evidence of record). Full text: `plans/sdd/V5-VERIFY/x12-disposition.md`. |
| 4.13 | `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne` | integration, `test/integration/v5_x13_test.go` | authored | Two controls. (1) Runtime switch, in the committed test: scheduler.EnableExperimentalPolicies() is the only switch that can make the capability register report CapSkiRentalWritePolicy available; the test asserts the flip (Available true, Require nil. Full text: `plans/sdd/V5-VERIFY/x13-disposition.md`. | (1) The historical leg "qompack status --json renders the same Breakdown map, sorted, every key present" has no producer on this tree: the status command does not render Decision.Breakdown and daemon.SchedulerSnapshotOf has no production consumer. Full text: `plans/sdd/V5-VERIFY/x13-disposition.md`. |
| 4.14 | `TestV5_EveryContractAssertionHasARealProducer` | e2e, `test/e2e/v5_x14_test.go` | authored | Two runtime controls, both in the committed test (no source edit was needed or applied; git diff of tracked files was empty throughout). (1) Severed delivery: the same full composition and the same declared injection producer, but the host never folds the injected additionalContext into the transcript; two real `observe prompt` scans spend the two chances (driven via Drain and polled on state/history.json), and the n. Full text: `plans/sdd/V5-VERIFY/x14-disposition.md`. | (1) mcp.server_registered driven to `observed` / "initialize-received": the in-process v4 rig cannot install the MCP op (internal/cli.installMCPTools is unexported), so the row asserts only that it is never `observed` without an initialize and that its `unavailable` reading tracks HasProducer; a spawned-daemon + real `qompack mcp` child row could close it. Full text: `plans/sdd/V5-VERIFY/x14-disposition.md`. |
| 4.15 | `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` | e2e, `test/e2e/v5_x15_test.go` | authored | Real switches, not source edits. (1) daemon_disabled: runtime.daemon.enabled=false severs the producer (ipc.Client.Send step 2 spools without dialling or lazy-spawning); the same status/recall/dropped invocations that answer ok:true in the degraded arm must report source none with a reason and null snapshot, and exit 1 with ok:false envelopes; ipc.Probe confirms no daemon appeared. Full text: `plans/sdd/V5-VERIFY/x15-disposition.md`. | (1) SP-21's admission.Pipeline itself is not exercised: internal/admission has no composition-root adapter on this tree (no production package imports it; it "ships off"). Full text: `plans/sdd/V5-VERIFY/x15-disposition.md`. |
| 4.16 | `TestV5_NoPackageWritesOutsideDotQompack` | e2e, `test/e2e/v5_x16_test.go` | authored | Real switch, real write: `qompack eval import --from <transcripts> --to <dir outside project and HOME>` is the one product command whose destination is user-directed and outside .qompack/ by design. Full text: `plans/sdd/V5-VERIFY/x16-disposition.md`. | (1) state/promotions.json (internal/mcp/promote.go, written on an MCP expand) and sketches/seg-NNNN.bloom (default-off segmentBloom switch): writers not driven; recorded as off/undriven, Layout-allowed if they appear. Full text: `plans/sdd/V5-VERIFY/x16-disposition.md`. |
| 4.17 | `TestV5_AdmissionExtension` | integration, `test/integration/v5_x17_test.go` | authored | Real switches inside the test, each asserting the honest refusal path (reason, stage, error, empty handle/mark/form/meaning, unknown coverage): store.PutOptions.KeepRaw=false (the real store's own canonical-only fidelity -> capture-failed/ErrUnrecoverableCapture, and a later KeepRaw=true redelivery of the same bytes stays canonical). Full text: `plans/sdd/V5-VERIFY/x17-disposition.md`. | (1) Host allowlist target evidence (B01): the one-entry allowlist is exact-match tested in process only; no installed-host canary or competing-hook observation exists, shipped allowlist stays empty. Full text: `plans/sdd/V5-VERIFY/x17-disposition.md`. |

**Production defects the authoring found, and fixed in their own commits** (each with a `Refs:` footer
and a characterization test): `fix(ipc)` `9c023ac` — the hook client minted a 16-byte delivery nonce that
hex-encodes to 32 characters while the daemon's delivery journal accepts exactly 64, so on the shipped
path no live delivery was ever leased, captured or acknowledged (found independently by 4.2 and 4.3;
4.3's commit kept, 4.2's duplicate dropped at cherry-pick); `fix(commands)` `8ff89b8` — a live status answer
stamped after `now` was reported with a negative age (4.1); `fix(checkpoint)` `d403363` — `Finalize` resolved
tool pointers by object where the store indexes by root (4.8); `fix(commands)` `a732424` — `recall` sent `k=0`
when `--k` was not given (4.15, observed by 4.16). Two `internal/store` side-record defects observed by
4.17 were recorded in its disposition and deliberately not fixed (identity and GC coupling); at the
independent review's request they are carried as SP20-D3 and §22 items 28–29.

**A fifth production defect surfaced only after the nonce fix made the capture path live.** The
whole-tree run on `7e0aac5` turned two older e2e tests red; item F4 traced one of them to the drain
re-dispatching spool copies of deliveries the frontier had already acknowledged (§22 item 17,
`fix(daemon)` `1c17f0d`). The residue it leaves by design, the observer's non-idempotence under a
legitimate reused-lease redelivery, is opened as SP08-D2 (§21).

---

## 20. Performance budget validation (plan §5)

Measured on the quiet pass only; the inventory's co-loaded numbers are diagnostics, not results. Every
figure below is Windows 11 on this laptop, one process at a time, at `-benchtime 2s` unless its row
names another; the reference
platform (Linux CI runner) was not measurable here (no WSL distribution, Docker stopped), and no
universal 15 ms, 4:1, sublinear-growth or first-turn-savings claim is made.

**How these figures were taken.** Two attempts were discarded before the table below: the first
pass ran beside an orphaned copy of itself, and the second ran on battery with the CPU at 55 %
processor performance (§22 item 22). The figures here come from `scratchpad/bench-window.sh` on
`verify/v5` @ `0d5c999` and `scratchpad/bench-window2.sh` @ `1a1bbaf` (which differs only in
`tools/devtool`). Both run one step at a time and refuse to start a step until two consecutive 10 s
probes read AC power or a battery charge of at least 25 %, ≥ 90 % processor performance, and foreign
CPU below 8 % (window 1) or 12 % (window 2, loosened after the gate had waited 43 min while the user
worked); each step's probe is on its `summary.txt` line and in §17. Window 1 ran every step on AC at
turbo clocks (processor performance 114–225 %). Window 2's seven confirmation rows ran in mixed
regimes, each stated where it is quoted: E9b on battery at 92 % and E5b on battery at 95 % (base
clock), E6b on AC at 96 % and F1c on AC at 95 %, E4b on AC at 161 %, F1b on AC at 120 % and E9c on AC
at 128 %. Where a row was measured in two regimes, the allocation columns separate code from clock:
they do not depend on the clock, and they agree to within 1 % wherever both runs recorded them (E9
and E5b's `Search` lines ran without `-benchmem`).
Reference platform: not measurable on this host (no WSL distribution, Docker stopped); every number
is Windows 11 on a Core Ultra 7 155H.

**Budget verdicts (plan §5, from the inventory's budget rows).** Of the budgeted rows, every one
passes on the quiet window except the following, each with its class:

| Row | Figure (budget) | Class | Record |
|---|---|---|---|
| `BenchmarkPutBytes_100KB_Cold` / `_Warm` | 15.74 ms (3 ms) / 5.65 ms (400 µs) | reference-platform; cold matches V3's 16.1 ms, but warm is 20 % over V3's 4.7 ms and cold allocations rose 922 → 1 183/op (+28 %) with `bench_test.go` unchanged, so the code moved as well (§22 item 31) | SP06-D2 → `deferred:V6-VERIFY` (§21) |
| `BenchmarkOnToolUse_TestOutput256KB` p99 | 81.9 / 81.9 / 180.2 ms (50 ms, soft) | reference-platform; 64 KB fixture inside (15.4–41.0 ms) | SP08-D1 → `deferred:V6-VERIFY`; Q25 not applied |
| `BenchmarkFinalize` | 42.8–43.8 ms at 170–208 % (E9 at `-benchtime 2s`, and the H2 sweep); 66.6–73.1 ms over `-count=6` on battery at 92 % (E9b); 63.6–70.3 ms over `-count=6` on AC at 128 % (E9c) (50 ms) | inside only at high turbo clocks after `perf(checkpoint)` (264–284 ms at V4); 23.6–23.8 k allocs/op wherever recorded (E9 ran without `-benchmem`) | SP10-D1 → `deferred:V6-VERIFY` (§21) |
| `BenchmarkPathsWriteAtomic_4KB` | 3.81 ms (2 ms) | disk-bound (one fsync + create + rename, Q4); `HookNoop_InProcess` 2.14 ms is inside its 3 ms, but its allocations rose 22× since the baseline (324 → 7 317/op, 34 KB → 517 KB/op), inherited from `develop` (§22 item 31) | recorded; joins the reference-platform class under SP06-D2's disposition |
| `BenchmarkAddToolUse` | 40.7 µs at `-benchtime 2s`; 10.4–16.1 µs/op at `-benchtime 20x` (E6b, `-count=6`, 96 % processor performance) = 2.1–3.2 µs per pair (3 µs/pair) | regime (V2 gates item 23: the benchmark appends b.N tool-uses to one persistent graph, so an organic run measures graph growth; the pair budget is graded at the 20x regime over the five-pair build): five of six samples inside, the sixth 7 % over at base clock, against V2's 1.3–2.2 µs/pair; the organic figure equals the committed baseline's 37–46 µs | recorded, not a regression |
| `BenchmarkGetChunk` / `BenchmarkSearch_1000Roots` / `BenchmarkOpenSpan_4KB_of_4MB` | 88.2 µs (60 µs) / 81.8 ms (25 ms) / 88.6 µs (150 µs, inside), baseline 45 µs / 48 ms / 46 µs; on battery at 95 % (E5b) 224–321 µs / 242–271 ms / 316–333 µs, the last over its budget too, with the same allocations where recorded (E5b's `Search` lines carry none) | code, not host: `16ecc77` verify-on-read (allocs 18 → 26, 19 → 27 and 30.3 k → 42.7 k, clock-independent); `Search` reads every candidate chunk through the same path | SP20-D2 → `deferred:V6-VERIFY` (§21, §22 item 25) |
| `BenchmarkIngestAccept` / `BenchmarkIngestAcceptLeased` | 1.93–2.00 ms / 18.4–20.0 ms (B-B 2 ms) | one fsync / four fsyncs per accepted delivery; the leased path is the shipped path since `fix(ipc)` | SP20-D1 (§21, §22 item 20); confirmed at `-benchtime 200x -count=3` on AC turbo (E4b, 161 % processor performance): unleased 2.05–2.19 ms, leased 22.96–23.30 ms, a 10.5–11.4× ratio; the unleased path alone sits at the 2 ms line |
| bench-hotpath B-B (`l0_ingest`) | p50 917.5 ms, p99 983.0 ms (2 ms) | queueing behind the fsync-bound `Accept` under the 2 000-hook burst; B-A p99 3.07 ms PASS, B-E PASS | SP20-D1; gate stays red as V4 left it |
| `TestBudget_Open` / `TestBudget_DetectorScan` | PASS 4 of 4 at turbo: F1 (225 %, no CPU figure logged) and F1b `-count=3` (120 %) 257.8–273.4 ms CPU/op (300 ms); FAIL in the serial B1 run on battery at 55 %: 585.9–632.8 ms; DetectorScan 2.6–3.6 ms at turbo, 6.1–6.7 ms at 55 % (5 ms) | clock-bound: CPU/op scales with the clock, so both hold on this host only at turbo, SP10-D1's pattern | SP09-D1 → `deferred:V6-VERIFY` (§21, Q28); §22 item 3 |
| `TestGC_DeadlineTruncatesAndResumes` | 511 objects scanned before the first deadline check (< 256) in the AC F1 run; F1c `-count=3`: both deadline tests 3 of 3 PASS, the probe before the step reading 95 % (the overshoot test logs a first check at 22.9–33.4 ms); the F1 failure came at 225 % right after calibration, a turbo ramp | wall-clock-shaped (ADR 0010 class), the "too fast" direction | §22 item 23 |
| `TestIntegration_HotPathWarmWithRealResidentState`, `TestV3_HotPathUnchangedWithLedgerResident` | fail on B-B only (B-A inside) | SP20-D1 | carried with it |
| `TestIntegration_BeladyPMinLandsAtLowCoupling` | 61.5 % vs 70 % floor | deterministic, V4 sign-off item | §29 |

**The other hot-path budgets.** B-D (`hook_wall`, host process creation included; reported, not gated):
p50 22.4 ms, p95 25.4 ms, p99 28.8 ms over 2 000 spawned hooks (H1 on AC,
`scratchpad/quiet/H1-bench-hotpath.txt`). B-E: PASS in the same run (p99 129.0 ms wall, 46.9 ms CPU,
against 2 s). F1's `TestIntegration_HotPathWarmWithRealResidentState` runs the same harness in-process and
reads alike (B-D p99 27.5 ms, B-B p50 852 ms and p99 1 049 ms, B-A PASS). B-F (`mcp_tool_call`,
p95 < 250 ms): `TestBudgetBF` ran alone in F1 and `internal/mcp` reads ok
(`scratchpad/quiet/F1-ci-timing.txt`); it fails only under co-load, as on the pre-wave-4 base.

**Regression gate (`devtool bench-compare` against `testdata/bench-baseline.txt`).** Run on the AC sweep (`scratchpad/quiet/v5-bench.txt`, step H2) once fix item F8 (`1a1bbaf`) had
classified the four observer units the gate stopped on (`scratchpad/quiet/H2b-bench-compare.txt`):
311 measurements compared, 6 with a significant change, none in the 10–25 % warning band, one over
the 25 % line — `PathsWriteAtomic_4KB` B/s **+28.7 % worse** (4.42 ms/op in the sweep against
3.10–3.23 ms in the baseline; 3.81 ms alone in E1). `internal/paths/atomic.go` is byte-identical to
the baseline commit `1e767c3`, so the delta is this disk's fsync latency on the day, not code: recorded
in Q4's disk-bound class with SP06-D2 (§22 item 24) and the gate left red rather than the baseline
touched. No other row was called significant, which at one sample per row says little (§22 item 24). Against
the baseline medians, 12 of the 89 benchmarks both files carry are more than 25 % slower on sec/op in
the sweep and 19 more are 10–25 % slower (`scratchpad/review/c-sweep-delta.py`). Part of that is this
host's clock and disk on the day; the rows whose allocation columns moved as well are code changes and
are recorded in this section (SP20-D2, and `HookNoop_InProcess` and `PutBytes_100KB_Cold`, §22 item
31). The timing deltas are not verdicts: the gate is blind here, and a baseline regeneration at
`-count` ≥ 4 is owed to SP-17.

**Baseline staleness.** The baseline's `BenchmarkIngestAccept` rows (2 µs) predate `f6a8691`'s
WAL fsync and `9c023ac`'s live leases; a regeneration on a quiet AC window is owed to SP-17 and was
not done here (the rule of this branch and of every earlier checkpoint).

**Per-step figures** (every benchmark line of the E steps as measured, budgets from the inventory; the
window2 steps are the base-clock and repeat regime described above):

**E1-I-01.24** (`scratchpad/quiet/E1-I-01.24.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkHistogram_Observe` | 6 ns |  | 100 ns (I-01.24) | PASS |
| `BenchmarkConfigLoad_ColdNoFiles` | 326.3 us | B/op 100878, allocs/op 2012 | 2.00 ms (I-01.24) | PASS |
| `BenchmarkPathsWriteAtomic_4KB` | 3.81 ms | MB/s 1.08, B/op 16498, allocs/op 174 | 2.00 ms (I-01.24, disk-bound (Q4)) | **breach 1.9x** |
| `BenchmarkHookNoop_InProcess` | 2.14 ms | B/op 516844, allocs/op 7317 | 3.00 ms (I-01.24, disk-bound (Q4)) | PASS |

**E2-I-03.13** (`scratchpad/quiet/E2-I-03.13.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkBloomAdd` | 158 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkBloomTest` | 153 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkCMSAdd` | 153 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkCMSEstimate` | 168 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkHLLAdd` | 138 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkHLLCardinality` | 6.3 us | B/op 0, allocs/op 0 | — | — |
| `BenchmarkMisraGriesAdd` | 79 ns | B/op 0, allocs/op 0 | — | — |
| `BenchmarkL0SketchUpdate` | 459 ns | B/op 0, allocs/op 0 | 5.0 us (I-03.13, also 0 allocs) | PASS |
| `BenchmarkMinHash4KiB` | 293.1 us | B/op 3072, allocs/op 2 | — | — |
| `BenchmarkMinHash100KiB` | 1.29 ms | B/op 396288, allocs/op 4 | — | — |
| `BenchmarkBloomMarshal` | 5.3 us | B/op 24640, allocs/op 3 | — | — |
| `BenchmarkBloomUnmarshal` | 3.8 us | B/op 12560, allocs/op 5 | — | — |
| `BenchmarkCMSMarshal` | 31.1 us | B/op 114752, allocs/op 3 | — | — |
| `BenchmarkCMSUnmarshal` | 21.2 us | B/op 57632, allocs/op 7 | — | — |
| `BenchmarkRebuildBloom5000` | 832.4 us | B/op 12400, allocs/op 4 | 15.00 ms (I-03.13) | PASS |

**E3-I-04.17** (`scratchpad/quiet/E3-I-04.17.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkSplit_100KB` | 110.1 us | MB/s 929.8, B/op 4992, allocs/op 2 | 800.0 us (I-04.17) | PASS |
| `BenchmarkGearScan_1MiB` | 529.5 us | MB/s 1980.26, B/op 0, allocs/op 0 | >= 400 MB/s (I-04.17) | PASS |
| `BenchmarkSplit_1MiB` | 1.17 ms | MB/s 895.39, B/op 57472, allocs/op 2 | — | — |
| `BenchmarkSplitStream_4MiB` | 5.57 ms | MB/s 752.75, B/op 33000, allocs/op 6 | — | — |
| `BenchmarkRootHash_1000Chunks` | 25.3 us | B/op 32, allocs/op 1 | 40.0 us (I-04.17) | PASS |
| `BenchmarkRun_Bash100KB` | 2.92 ms | MB/s 35.08, B/op 1.06342e+06, allocs/op 35 | 3.00 ms (I-04.17 (SP04-D5/D6 context)) | PASS |
| `BenchmarkRun_GoTest` | 839.9 us | MB/s 14.2, B/op 542529, allocs/op 284 | — | — |
| `BenchmarkRun_KeepDeltas` | 2.88 ms | MB/s 35.51, B/op 1.07274e+06, allocs/op 38 | — | — |
| `BenchmarkRestore_100KB` | 23.5 us | MB/s 4361.34, B/op 106496, allocs/op 1 | 1.00 ms (I-04.17) | PASS |
| `BenchmarkExtract_100KB` | 1.04 ms | MB/s 98.37, B/op 313472, allocs/op 2325 | 2.00 ms (I-04.17) | PASS |
| `BenchmarkEnclosing_100KB` | 1.06 ms | MB/s 96.57, B/op 313948, allocs/op 2325 | 2.00 ms (I-04.17) | PASS |
| `BenchmarkReferences_100KB_50Names` | 204.9 us | MB/s 501.75, B/op 4128, allocs/op 8 | 1.00 ms (I-04.17) | PASS |

**E4-I-05.20** (`scratchpad/quiet/E4-I-05.20.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkServerRoundTrip` | 29.2 us |  | 2.00 ms (I-05.20, p99 budget judged on the mean) | PASS |
| `BenchmarkReadState` | 35.0 us |  | 100.0 us (I-05.20) | PASS |
| `BenchmarkEncodeRequest` | 968 ns |  | 5.0 us (I-05.20) | PASS |
| `BenchmarkIngestAccept` | 1.93 ms |  | 2.00 ms (I-05.20 B-B, p99 budget judged on the mean) | PASS |
| `BenchmarkIngestAcceptLeased` | 18.35 ms |  | — | — |

**E4b-accept-count3** (`scratchpad/quiet/E4b-accept-count3.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkIngestAccept` | 2.05 ms |  | 2.00 ms (I-05.20 B-B, p99 budget judged on the mean) | **breach 1.0x** |
| `BenchmarkIngestAccept` | 2.19 ms |  | 2.00 ms (I-05.20 B-B, p99 budget judged on the mean) | **breach 1.1x** |
| `BenchmarkIngestAccept` | 2.17 ms |  | 2.00 ms (I-05.20 B-B, p99 budget judged on the mean) | **breach 1.1x** |
| `BenchmarkIngestAcceptLeased` | 23.30 ms |  | — | — |
| `BenchmarkIngestAcceptLeased` | 22.96 ms |  | — | — |
| `BenchmarkIngestAcceptLeased` | 23.01 ms |  | — | — |

**E5-I-06.18** (`scratchpad/quiet/E5-I-06.18.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkPutBytes_100KB_Warm_NoRedact` | 5.06 ms | MB/s 20.23, B/op 910159, allocs/op 17 | — | — |
| `BenchmarkPutBytes_100KB_Cold_NoRedact` | 15.04 ms | MB/s 6.81, B/op 3.4132e+06, allocs/op 1053 | — | — |
| `BenchmarkPutBytes_100KB_Cold` | 15.74 ms | MB/s 6.51, B/op 5.58394e+06, allocs/op 1183 | 3.00 ms (I-06.18, SP06-D2) | **breach 5.2x** |
| `BenchmarkPutBytes_100KB_Warm` | 5.65 ms | MB/s 18.11, B/op 1.01789e+06, allocs/op 19 | 400.0 us (I-06.18, SP06-D2) | **breach 14.1x** |
| `BenchmarkGetChunk` | 88.2 us | B/op 24282, allocs/op 26 | 60.0 us (I-06.18) | **breach 1.5x** |
| `BenchmarkOpenSpan_4KB_of_4MB` | 88.6 us | B/op 24778, allocs/op 27 | 150.0 us (I-06.18) | PASS |
| `BenchmarkOpenStore_50kRoots` | 282.23 ms | B/op 9.58531e+07, allocs/op 1.06278e+06 | 400.00 ms (I-06.18) | PASS |
| `BenchmarkGC_50kObjects` | 1.28 s | B/op 6.3903e+07, allocs/op 621835 | 2.00 s (I-06.18) | PASS |
| `BenchmarkSearch_1000Roots` | 81.77 ms | B/op 2.09698e+07, allocs/op 42671 | 25.00 ms (I-06.18) | **breach 3.3x** |
| `BenchmarkMarkEncoded_100` | 269.6 us | B/op 16896, allocs/op 301 | 1.00 ms (I-06.18) | PASS |
| `BenchmarkEstimateRoot_64Cached` | 1.6 us | B/op 0, allocs/op 0 | 5.0 us (I-06.18) | PASS |
| `BenchmarkUnits_100KB` | 144.4 us | MB/s 709.19, B/op 0, allocs/op 0 | — | — |

**E5b-store-count3** (`scratchpad/quiet/E5b-store-count3.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkGetChunk` | 224.4 us | B/op 24494, allocs/op 26 | 60.0 us (I-06.18) | **breach 3.7x** |
| `BenchmarkGetChunk` | 307.5 us | B/op 23727, allocs/op 26 | 60.0 us (I-06.18) | **breach 5.1x** |
| `BenchmarkGetChunk` | 320.9 us | B/op 24447, allocs/op 26 | 60.0 us (I-06.18) | **breach 5.3x** |
| `BenchmarkOpenSpan_4KB_of_4MB` | 316.1 us | B/op 24027, allocs/op 27 | 150.0 us (I-06.18) | **breach 2.1x** |
| `BenchmarkOpenSpan_4KB_of_4MB` | 325.1 us | B/op 24143, allocs/op 27 | 150.0 us (I-06.18) | **breach 2.2x** |
| `BenchmarkOpenSpan_4KB_of_4MB` | 332.5 us | B/op 23959, allocs/op 27 | 150.0 us (I-06.18) | **breach 2.2x** |
| `BenchmarkSearch_1000Roots` | 241.84 ms |  | 25.00 ms (I-06.18) | **breach 9.7x** |
| `BenchmarkSearch_1000Roots` | 270.92 ms |  | 25.00 ms (I-06.18) | **breach 10.8x** |
| `BenchmarkSearch_1000Roots` | 257.58 ms |  | 25.00 ms (I-06.18) | **breach 10.3x** |

**E6-I-07.11** (`scratchpad/quiet/E6-I-07.11.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkBackwardSlice5000` | 287.9 us |  | 1.00 ms (I-07.11) | PASS |
| `BenchmarkBackwardSlice5000Full` | 700.8 us |  | — | — |
| `BenchmarkForwardSlice5000` | 10.7 us |  | 1.00 ms (I-07.11) | PASS |
| `BenchmarkCrossingEdges` | 98 ns |  | 5.0 us (I-07.11) | PASS |
| `BenchmarkNodesAfter` | 87.0 us |  | — | — |
| `BenchmarkRebuildIndex` | 3.53 ms |  | 10.00 ms (I-07.11) | PASS |
| `BenchmarkAddNodeEdgePair` | 2.8 us |  | — | — |
| `BenchmarkAddToolUse` | 40.7 us |  | 3 us/pair at the 20x regime (I-07.11) | regime: organic run measures graph growth, see E6b |
| `BenchmarkOpen20k` | 66.15 ms |  | 250.00 ms (I-07.11) | PASS |
| `BenchmarkCompact20k` | 26.80 ms |  | 400.00 ms (I-07.11) | PASS |

**E6b-dag-regime** (`scratchpad/quiet/E6b-dag-regime.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkAddToolUse` | 16.1 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | **breach 1.1x** |
| `BenchmarkAddToolUse` | 12.7 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | PASS |
| `BenchmarkAddToolUse` | 13.8 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | PASS |
| `BenchmarkAddToolUse` | 14.2 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | PASS |
| `BenchmarkAddToolUse` | 10.4 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | PASS |
| `BenchmarkAddToolUse` | 11.3 us |  | 15.0 us (I-07.11: 3 us per pair x 5 pairs, graded at -benchtime 20x only (V2 item 23)) | PASS |

**E7-I-08.15** (`scratchpad/quiet/E7-I-08.15.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkTombstone` | 285 ns | B/op 272, allocs/op 6 | 2.0 us (I-08.15) | PASS |
| `BenchmarkOnToolUse_FileRead64KB/Deduped` | 10.27 ms | dedup-x 37009, objects 5, p50-ms 10.24, p99-ms 15.36 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | inside |
| `BenchmarkOnToolUse_FileRead64KB/Delta` | 17.28 ms | dedup-x 691.8, objects 145, p50-ms 18.43, p99-ms 40.96 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | inside |
| `BenchmarkOnToolUse_FileRead64KB/AllNovel` | 20.29 ms | dedup-x 64.22, objects 451, p50-ms 20.48, p99-ms 28.67 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | inside |
| `BenchmarkOnToolUse_TestOutput256KB/Deduped` | 35.24 ms | dedup-x 773, objects 34, p50-ms 36.86, p99-ms 81.92 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | **over (81.9 ms)** |
| `BenchmarkOnToolUse_TestOutput256KB/Delta` | 41.11 ms | dedup-x 165.5, objects 91, p50-ms 45.06, p99-ms 81.92 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | **over (81.9 ms)** |
| `BenchmarkOnToolUse_TestOutput256KB/AllNovel` | 110.69 ms | dedup-x 3.738, objects 1236, p50-ms 106.5, p99-ms 180.2 | p99 < 50 ms (I-08.15 B-C soft, SP08-D1) | **over (180.2 ms)** |

**E8-I-09.14** (`scratchpad/quiet/E8-I-09.14.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkQueryHit` | 1.3 us | cpu-ns/op 1916, B/op 952, allocs/op 10 | — | — |
| `BenchmarkQueryMiss` | 1.1 us | cpu-ns/op 1236, B/op 256, allocs/op 7 | — | — |
| `BenchmarkRecord` | 26.6 us | cpu-ns/op 33101, B/op 12551, allocs/op 94 | — | — |
| `BenchmarkRebuildBloom` | 9.94 ms | cpu-ns/op 1.2167e+07 | — | — |
| `BenchmarkRefreshStaleness` | 1.69 ms | cpu-ns/op 2.66995e+06 | — | — |
| `BenchmarkOpen` | 177.94 ms | cpu-ns/op 2.03125e+08 | — | — |
| `BenchmarkDetectorScan` | 1.73 ms | cpu-ns/op 1.9559e+06, B/op 1.03208e+06, allocs/op 9495 | — | — |

**E9-I-10.17** (`scratchpad/quiet/E9-I-10.17.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkExtractDecisions` | 8.10 ms |  | 20.00 ms (I-10.17) | PASS |
| `BenchmarkFinalize` | 43.75 ms |  | 50.00 ms (I-10.17, SP10-D1) | PASS |
| `BenchmarkStripInjections` | 198.6 us | MB/s 1319.71, B/op 1.16211e+06, allocs/op 14 | 2.00 ms (I-10.17) | PASS |
| `BenchmarkTruncate` | 1.08 ms | B/op 740263, allocs/op 1565 | 5.00 ms (I-10.17 (I-16.16 TruncateTuned: 20 ms)) | PASS |
| `BenchmarkAdvanceSegment` | 21.32 ms |  | 25.00 ms (I-10.17) | PASS |

**E9b-finalize-count6** (`scratchpad/quiet/E9b-finalize-count6.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkFinalize` | 72.07 ms | B/op 4.61455e+06, allocs/op 23634 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.4x** |
| `BenchmarkFinalize` | 70.03 ms | B/op 4.52213e+06, allocs/op 23624 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.4x** |
| `BenchmarkFinalize` | 73.07 ms | B/op 4.51102e+06, allocs/op 23624 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.5x** |
| `BenchmarkFinalize` | 69.88 ms | B/op 4.54145e+06, allocs/op 23627 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.4x** |
| `BenchmarkFinalize` | 66.60 ms | B/op 4.54242e+06, allocs/op 23626 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |
| `BenchmarkFinalize` | 67.29 ms | B/op 4.46623e+06, allocs/op 23623 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |

**E9c-finalize-count6** (`scratchpad/quiet/E9c-finalize-count6.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkFinalize` | 63.64 ms | B/op 4.56837e+06, allocs/op 23631 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |
| `BenchmarkFinalize` | 65.79 ms | B/op 4.61524e+06, allocs/op 23630 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |
| `BenchmarkFinalize` | 66.20 ms | B/op 4.56994e+06, allocs/op 23834 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |
| `BenchmarkFinalize` | 70.29 ms | B/op 4.54165e+06, allocs/op 23829 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.4x** |
| `BenchmarkFinalize` | 65.89 ms | B/op 4.58453e+06, allocs/op 23832 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |
| `BenchmarkFinalize` | 65.57 ms | B/op 4.59305e+06, allocs/op 23628 | 50.00 ms (I-10.17, SP10-D1) | **breach 1.3x** |

**E10-I-11.19** (`scratchpad/quiet/E10-I-11.19.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkPathScoped` | 1.10 ms | B/op 167425, allocs/op 2006 | 50.00 ms (I-11.19 L5-RULES) | PASS |
| `BenchmarkIndex` | 3.24 ms | B/op 276370, allocs/op 1225 | 20.00 ms (I-11.19 L5-SKILLS) | PASS |

**E11-I-12.21** (`scratchpad/quiet/E11-I-12.21.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkEvaluate_64Candidates` | 2.8 us | B/op 3736, allocs/op 6 | 50.0 us (I-12.21, also <= 8 allocs) | PASS |
| `BenchmarkBOCDObserve_4Features/full_posterior` | 24.3 us | B/op 4128, allocs/op 2 | 150.0 us (I-12.21 (full; steady 20 us)) | PASS |
| `BenchmarkBOCDObserve_4Features/steady_state` | 1.6 us | B/op 302, allocs/op 2 | 150.0 us (I-12.21 (full; steady 20 us)) | PASS |
| `BenchmarkBOCDMarshal` | 26.5 us | B/op 73728, allocs/op 1 | 2.00 ms (I-12.21) | PASS |
| `BenchmarkIngestAccept` | 2.00 ms |  | 2.00 ms (I-05.20 B-B, p99 budget judged on the mean) | PASS |
| `BenchmarkIngestAcceptLeased` | 19.99 ms |  | — | — |
| `BenchmarkFeaturesFrom` | 16.4 us | B/op 17056, allocs/op 39 | 100.0 us (I-12.21) | PASS |
| `BenchmarkReclaimableIndexBuild_5000Blocks` | 618.4 us | B/op 364592, allocs/op 17 | 3.00 ms (I-12.21) | PASS |
| `BenchmarkAssembleCandidates_2000ToolUses/cold` | 1.23 ms | B/op 937994, allocs/op 536 | 20.00 ms (I-12.21 (cold; warm 200 us)) | PASS |
| `BenchmarkAssembleCandidates_2000ToolUses/warm` | 3.5 us | B/op 9981, allocs/op 11 | 20.00 ms (I-12.21 (cold; warm 200 us)) | PASS |
| `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates/warm` | 11.3 us | B/op 46394, allocs/op 25 | 25.00 ms (I-12.21) | PASS |
| `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates/cold` | 2.07 ms | B/op 1.72786e+06, allocs/op 1970 | 25.00 ms (I-12.21) | PASS |
| `BenchmarkSchedulerTap_ObserveTool` | 30.2 us | B/op 20044, allocs/op 73 | 1.50 ms (I-12.21) | PASS |

**E12-I-15.18** (`scratchpad/quiet/E12-I-15.18.txt`)

_no benchmark lines_ — tail: `ok  	github.com/qompack/qompack/internal/grammar	0.601s`

**E13-I-16.16** (`scratchpad/quiet/E13-I-16.16.txt`)

| benchmark | ns/op | other | budget | verdict |
|---|---|---|---|---|
| `BenchmarkTruncate` | 1.35 ms | B/op 747072, allocs/op 1574 | 5.00 ms (I-10.17 (I-16.16 TruncateTuned: 20 ms)) | PASS |
| `BenchmarkEvaluate_64Candidates` | 2.8 us | B/op 3736, allocs/op 6 | 50.0 us (I-12.21, also <= 8 allocs) | PASS |

**Qompack-added tokens vs total context.** The estimator is `tokens.NewExactWithObs` (a calibrated
estimate, not a tokenizer count; Q18). 4.4 records the rehydrator's bounded-arm injection at 426–512
of a 600-token budget and 1 626 of 12 000 under the shipped budget; no held-out task run exists, so
no fraction-of-OPT or savings figure is produced (`unknown`).

**Request ledger.** Category usage, cache reads/writes, TTL, retries, aborted trials, compaction and
subagent usage for a real host session were not observed: no host session ran. The only executed
assertion is I-14.17 (missing telemetry is unknown, never zero). Estimated price: `unknown`; no rate
table was applied.

**Regression margins and samples.** Declared before running: a benchmark row breaches when its
`-benchtime 2s` best-of-run exceeds the budget on the quiet machine (window 2's confirmation rows use
`-count` repeats at the benchtime they state, and the per-step tables grade each sample; a row whose
samples straddle its budget is read by its entry in the verdict table); timing tests use their own
gated-clock retries. Sample sizes are the budgets' own (three attempts for `TestBudget_*`, 2 000
iterations for bench-hotpath). No statistical claim beyond these is made.

---

## 21. Regression and carried requirements (plan §6)

The V1–V4 reports and the V3 addendum are untouched (`git diff --stat 87c0c1d..HEAD` is empty for
them). Wave-2 plan documents change only where the carried-defect guard requires a detail section
(`V2-SP-02`, `V2-SP-08`, `V2-SP-10` and `V2-WAVE1-carried-defects.md`) and in ADR 0002 for SP02-D6's
rationale. The 2026-08-26 V3 waiver stands as V3's Addendum 2 (2026-09-06) left it: the three-platform
bench-gate p99 is discharged into ADR 0008, and J5 reads RED, partially discharged, with nine of its
ten jobs green on `develop` run 34052269275 and `test` red.

**Carried defects disposed by this checkpoint** (`plans/CARRIED-DEFECTS.tsv`, guard
`test/guards/carrieddefects_test.go`):

| Row | Before | After | Evidence and record |
|---|---|---|---|
| SP05-D1 | `deferred:V4-VERIFY`, no evidence | `fixed` | Already fixed by SP-20 (`f6a8691`, `a6faab0`): offset advance and seen-set commit now follow a successful binding, a dead context leaves the interrupted line pending. New characterization test `TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending` is RED on the pre-SP-20 tree (`scratchpad/sp05d1-v3-before.log`) and GREEN here, made deterministic by program-order expiry after review found the first version timer-flaky (16/40 under co-load; now 0/40 under `-race -count=40`). Residual recorded in `plans/V2-WAVE1-carried-defects.md`: a persistently refusing or panicking handler with a live context now blocks its file at that line (no retry cap) instead of losing it. |
| SP02-D6 | `deferred:V5-VERIFY` | `wontfix` | Qompack.md v1.5 retired §2.4 step 7 and its 25K figure; the corpus raises no skill demand; skills restore is covered in production by `internal/rehydrate`'s skill index. Dead `hostSkillBudget` removed; `TestCarriedDefect_SP02D6_StockIgnoresSkillInvocations` pins the exclusion; rationale in `plans/V2-SP-02-carried-defects.md` and ADR 0002. |
| SP10-D1 | `open`, owner V4-VERIFY | `deferred:V6-VERIFY` | `perf(checkpoint)`: `ValidatePointers` no longer routes every plain pointer through `paths.Norm` (79–88 % of Finalize was `filepath.EvalSymlinks` re-resolving the project root per pointer); byte-identical output proven by the golden tests; `TestValidatePointersCostDoesNotGrowWithRootDepth` pins the cost model. Loaded-host measurement 452 → 111 ms/op on the fix branch (`b1a8318`, which landed as `72f3226` after `d403363` resolved tool pointers by root; the landed chain allocates 23.6–23.8 k/op against the branch's 17.0 k); 42.8–43.8 ms at 170–208 % (E9 and the H2 sweep); 66.6–73.1 ms on battery at 92 % and 63.6–70.3 ms on AC at 128 % over `-count=6` — inside the 50 ms criterion only at high turbo clocks, with allocations within 1 % wherever recorded, so the row is re-owned rather than closed (§20, `plans/V2-SP-10-carried-defects.md`). |
| SP06-D2 | `deferred:V4-VERIFY` | `deferred:V6-VERIFY` | Quiet-pass `PutBytes` cold/warm figures in §20. Reference platform not measurable here. |
| SP08-D1 | `deferred:V4-VERIFY` | `deferred:V6-VERIFY` | Quiet-window `BenchmarkOnToolUse_*` p99 in §20: the 256 KB fixture over at every delta shape (81.9 / 81.9 / 180.2 ms), the 64 KB fixture inside (15.4–41.0 ms), so the summary is not widened (Q25). Reference platform not measurable here. |
| SP08-D2 (new) | — | `fixed` | Found by F4: the observer does not absorb an at-least-once redelivery under a reused lease (`captureSubagent` mints the subagent capture id from the fresh handler's turn; a replayed older read supersedes newer records). Evidence `TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent` (`d43ecb5`, adversarially reviewed, `-race -count=20` clean) pins the current drain-level outcome; the observer-level assertion belongs to the fix, which needs its own review against SP-20's capture contract. Manifestation that remains on the fixed tree: x09's SubagentStop count fails under heavy co-load when the pre-flush shutdown cancels a Stop between the observer append and the acknowledgement (the author's runs under an `internal/store` co-run: 1 of 3, 0 of 6, 2 of 12 and 3 of 3 red; the reviewer's four on a quiet host all green; `scratchpad/f5-results.txt`). Section in `plans/V2-SP-08-carried-defects.md`. **Closed `fixed` at the close-out** (`b0bf68d`): the observer recognizes a redelivered capture through its sidecar (`108ea8e`) and a read's record and its supersede marks land in one index write (`4e5a8f4`), with the evidence test inverted (`3c45084`); the two residuals go to SP08-D3 and SP20-D4. |
| SP05-D2 (new) | — | `fixed` | Found by F4: with the shipped `AckDeadlineMs` of 8, 25 and 26 of 196 hook deliveries in the two x09 runs whose counts are on file (13 %; `scratchpad/rev2-x09-red.txt`, `rev2-x09-ctrl-nofallback.txt`) fall back to the client spool after the daemon already acknowledged them. Harmless after the F4-P1 fix (skipped at the next drain) but wasted hot-path I/O; a deterministic pin needs a fake clock in the client's ACK wait, handed to SP-17. Section in `plans/V2-WAVE1-carried-defects.md`. **Closed `fixed` at the close-out** on both halves of the ordering it complained about: `AckDeadlineMs` raised from 8 to 73 ms on Windows, derived from the same attested runs as the B-B limit (`5d0b904`, `cbfa3d3`), and a leased `Accept` made about 2.5 to 3 times cheaper by SP20-D1's group commit and format-2 seal (median 7.306 ms). Evidence is now `BenchmarkIngestAcceptLeasedBurst`. The rate itself was not re-measured and the ACK wait is still a wall clock, so the fake-clock pin stays on SP-17's list. |
| SP09-D1 (new) | — | `deferred:V6-VERIFY` | `TestBudget_Open` (negknow, 300 ms CPU/op) passes at turbo (F1 at 225 %; F1b `-count=3` at 120 %: 257.8–273.4 ms CPU/op, `scratchpad/quiet/F1b-negknow-count3.txt`) but failed the serial B1 run on battery at 55 % processor performance at 585.9–632.8 ms CPU/op (`scratchpad/quiet/B1-wholetree.txt`), and it failed on the pre-wave-4 tree as well (§22 item 3). CPU/op scales with the clock, so the budget holds on this host only at turbo — SP10-D1's pattern, and opened for the same reason at the independent review's request (Q28). `TestBudget_DetectorScan` has the same shape (2.6–3.6 ms at turbo, 6.1–6.7 ms at 55 %, against 5 ms). Evidence `TestBudget_Open`; section in `plans/V2-WAVE1-carried-defects.md`. |
| SP20-D1 (new) | — | `fixed` | Found by H1 (§22 item 20): since `fix(ipc)` `9c023ac` made every delivery leased, `Accept` pays four fsyncs inside budget B-B's timed region before the transport ACK, so bench-hotpath reads B-B p50 917.5 ms and p99 983.0 ms on AC against 2 ms while B-A stays inside (p99 3.07 ms). Evidence `BenchmarkIngestAcceptLeased` (`0d5c999`; 18.3 ms/op on AC turbo, 22.96–23.30 ms at `-count=3`, against 1.93–2.19 ms unleased). A budget-versus-durability decision, and at the close-out it was decided both ways: **closed `fixed`** with the durability points moved onto leader/follower group commit and the position sidecar replaced by the format-2 A/B seal (`ebd86f6`, `2d3499a`, `25c5b7e`, `383f97b`, `5d32859`; leased `Accept` median now 7.306 ms), and `l0IngestMs` re-budgeted from measurement to 50 ms on Windows (`5d0b904`, `cbfa3d3`), against which three attested runs read B-B p99 20.480 / 12.288 / 20.480 ms, all PASS. Linux and darwin stay provisional pending CI's bench-gate, which is also the reference-platform figure still owed. Section in `plans/V2-WAVE1-carried-defects.md`. |
| SP20-D2 (new) | — | `deferred:V6-VERIFY` | Found by the quiet pass (E5, H2): `16ecc77`'s verify-on-read (two extra metadata syscalls and a plaintext content hash per object read) took `GetChunk` from 45 to 85–88 µs (26 vs 18 allocs/op; budget 60 µs) and `Search_1000Roots` from 45–52 to 82 ms (42.7 k vs 30.3 k allocs); allocation columns are clock-independent, so it is the code. Integrity decision versus budget, the class SP20-D1 records for durability; section in `plans/V2-WAVE1-carried-defects.md`. |
| SP20-D3 (new) | — | `fixed` | Found by 4.17 and carried at the independent review's request (§22 item 28): `putSideRecord` keys a delta side record on its deltas alone, so two roots that share a removed volatile token share one record whose declared base is the first root, and the second root's `RestoreOriginal` fails with `ErrDeltaCorrupt` although its put reported `FidelityExact`. Latent: `RestoreOriginal` has no production caller, but the records persist. Section in `plans/V2-WAVE1-carried-defects.md`. **Closed `fixed` at the close-out** (`782652d`, fix merged in `a3b4687`): a delta record's identity now includes its declared base (`48770c3`), so the evidence is `TestPutBytes_SharedVolatileTokenRestoresBothRootsExactly` rather than the `-` this row opened with. |

SP05-D1 was applied in `66690d5`, SP02-D6 in `69d896b`, the other rows and sections in `f40b504`, and
SP09-D1 and SP20-D3 in the closing commit. The guard package passes on `f40b504` (its carried-defect tests,
`scratchpad/quiet/guards-cd.txt`), on `b1a0183` (the independent review's run) and on the closing
commit's tree before it was committed. The guard requires every unresolved row to name a live evidence
test or `-`, and every id to have a `## <id>` section in its derived detail document. One row of this
table now carries `-`: SP06-D2, which has no runtime symptom to pin. The two that carried it when this
section was first written no longer do. SP20-D3 names the characterization test its fix landed with.
SP05-D2 always departed from the ledger header's rule that `-` means no runtime symptom — it had one,
a measured fallback rate, and no deterministic pin — and now names `BenchmarkIngestAcceptLeasedBurst`;
a deterministic pin for the rate itself still needs a fake clock in the client's ACK wait and stays on
SP-17's list, so the row closed on the two quantities whose ordering caused the fallback, not on a
re-measured rate.

**Close-out flips.** Four rows above were opened `deferred:V6-VERIFY` by this report and closed
`fixed` before the checkpoint ended; the After column records the close-out state, not the state at
the report's first writing. SP08-D2 in `b0bf68d`, SP20-D3 in `782652d`, and SP20-D1 and SP05-D2 on the
strength of the group commit, the format-2 seal and the measured re-budget (`5d0b904`, `cbfa3d3`,
`9638b30`). Each has a dated disposition block at the end of its section in the detail document, and
each keeps a live test or benchmark in the evidence column so a re-deferral is a one-field edit. One
further row, SP20-D5, was opened and closed inside the close-out and never appeared in this table: the
drain's recorded durable bound could fall to the consumed offset after a held segment was reopened,
fixed in `9d1ef2d` and `a12bae9` with its residual stated in `762c8cc`; its section is in
`plans/V2-WAVE1-carried-defects.md`.

**Other §6 items.** SP02-D1–D6 were disposed by V4 as one unit except D6 (above). SP04-D2/D3 and
SP06-D1 `wontfix` rationale preserved and re-proved (Q14: both SP-04 characterization tests PASS).
SP04-D7's delta consumer stays assigned to checkpoints. SP11-C28 `frontierOf`/`Ref.Frontier` is
reconciled in the SP-11 inventory (I-11.x). SP07 NodeID/generation rulings stand. Selector guards
requiring native p-selection were **not** retired (SP-15 §3a.5); 4.6 asserts the replacement local
capability gate from both sides. The historical CLI `bench` survivor stays until SP-17 (Q2).
Conformance activation: only real supported behaviour; skips recorded honestly (Q44).

---

## 22. Failure protocol log (plan §7)

Every failure met during the checkpoint, its severity, exact artifact and disposition. Fix items:
F1 `fix(commands)` recover a panicking status source (`cf38c49`); F2 `test(store)` walk every
append-only log (`e75ac57`); F3 three `test/guards` scanners (`b7f85b0`, `596e85a`, `8742499`) and
`docs(replay)` (`9b06c90`); F4 `fix(daemon)` `1c17f0d` with the x13 fold `69da7d5`, the x09 rewrite
`170d6a6`/`b1f3122`/`b64b3f6` and the SP08-D2 evidence `d43ecb5`; F5 `fix(devtool)` `b482011` lists the wave-4 subplans as
landed so their coverage floors bind; F6 `test(daemon)` `0d5c999` prices the leased Accept path
(SP20-D1 evidence); F7 the AC-gated re-take scripts for every benchmark and timing row
(`scratchpad/bench-window.sh`, `bench-window2.sh`, item 22; tooling, not in the repository); F8
`fix(devtool)` `1a1bbaf` classifies the observer's four custom benchmark units so `bench-compare`
runs at all (item 24).

| # | Failure | Severity | Artifact | Disposition |
|---|---|---|---|---|
| 1 | golangci-lint `unparam` on `(*repSolver).reserve` after the SP-15 merge | lint gate | `scratchpad/m3-lint.txt` | Fixed `0dd982b` (result dropped; loop bound unchanged). |
| 2 | runpatterns: `-run …` placeholder in `plans/sdd/V5-SP-15/report-E.md` | lint gate | same | Waived in place `6856cb2`. |
| 3 | `TestBudget_Open` red after the SP-16 merge | timing | `scratchpad/m2-negknow-*.txt`, `quiet/F1b-negknow-count3.txt`, `quiet/B1-wholetree.txt` | Pre-existing on the pre-merge tree. It passes at turbo (257.8–273.4 ms CPU/op at 120 %) and fails at 55 % (585.9–632.8 ms in B1), so the budget is clock-bound on this host: opened as SP09-D1 (`deferred:V6-VERIFY`, §21, Q28). |
| 4 | Delivery nonce width mismatch (hook client 32 hex vs journal 64 hex) | **correctness, shipped path** | 4.2/4.3 dispositions | Fixed `fix(ipc)` `9c023ac`; unit test on both sides; explains I-08.13's spool degradation (re-run in §17). |
| 5 | Live status answer with negative age | correctness | 4.1 disposition | Fixed `fix(commands)` `8ff89b8`. |
| 6 | `Finalize` tool pointers resolved by object, not root | correctness | 4.8 disposition | Fixed `fix(checkpoint)` `d403363`. |
| 7 | `recall` without `--k` sends `k=0` | correctness | 4.15/4.16 dispositions | Fixed `fix(commands)` `a732424`. |
| 8 | Panicking status source escaped `CollectStatus` (exit-0 contract) | correctness | F1 (`scratchpad/negctrl1-no-recover.txt`) | Fixed `fix(commands)`: per-source recover into the section's honest error; three tests. |
| 9 | Append-only guard walked only five of the store's logs | coverage | F2 | Guard extended to every append-only-by-contract log; rewritten-by-design logs documented with their writers. |
| 10 | No executed guard for selector bypass, cache-ordering identifiers, commands purity | coverage | F3 | Three `test/guards` scanners added, each proven red under a planted violation. |
| 11 | `p4DischargedBy` and a v3_x08 comment named things that do not exist | doc drift | F3 | Corrected; `docs/adr/0012-scheduler-l3.md:387` carries the same drift and is handed to V6 planning. |
| 12 | Eight `-run` patterns in the new inventory files named missing tests | lint gate | `scratchpad/rp1.txt` | Waived in place with the MISSING/RETIRED/NOT-RUN scoring stated (`f9e4232`). |
| 13 | Three test files gofmt- but not gofumpt-formatted | fmt gate | the first run's `A1-fmt-check.txt` was replaced by later A1 runs; `7e0aac5` names the three files | Formatted `7e0aac5`; quiet pass restarted on that HEAD. |
| 14 | Session limit killed 14 subagents on 2026-09-08 night | tooling | workflow journals | Resumed from journals; every worktree kept its commits; no work lost. |
| 15 | Symbols fuzz baseline phase stalled 111 min | tooling | `scratchpad/quiet/D13-I-04.15.txt` | Not reproducible (1.4 s alone); recorded. |
| 16 | `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet` red on the whole-tree B1 run of `7e0aac5`: its expected write-set list predates the now-live capture sidecars (`records/captures/<hash>.json`) | test drift | `scratchpad/rev-f4-runA.txt`–`runD.txt` and `rev2-x13-base.txt` (the first pass's `B1-wholetree.txt` was replaced by resume2's run), v4_x13_test.go:190 | Folded into the shape with a per-arm sidecar count (`69da7d5`); two negative controls (count +1, fold removed) fire; idle-pass control still distinguishes. |
| 17 | `TestV3_LiveSessionWriteSetAndAppendOnly` red on the same run: "GC must not add objects; …zst appeared during flush" | **correctness, shipped path** | F4 chain: `scratchpad/rev-f4-evidence/`, `r2e/`, `f4r3/`, `f5a/`; two adversarial reviews of the diagnosis, one of the fix | Not GC and not a stale assertion. The flush-time daemon's startup drain re-dispatched spool copies of deliveries the frontier already acknowledged (client fallback copies after a missed 8 ms ACK, and the WAL copy) because `Drain` consulted `journal.acknowledged` only inside the process-memory seenSet's completed branch. Effects: a phantom fifth `SubagentStop` capture under turn 0, and supersede marks in which an older replayed read superseded newer records. Fixed at the drain, `fix(daemon)` `1c17f0d`: every leased line is checked against the frontier before the seen set and an acknowledged copy advances the offset without dispatch; a leased-but-unacknowledged copy is still redelivered (`TestCrashCutBetweenReferenceAndFrontierRedelivers`). Pinned deterministically by `TestDrainDoesNotRedeliverAnAcknowledgedClientCopy` (landed with the fix; red with it reverted, `scratchpad/f5a/daemon-pin-neg.txt`) and end to end by the rewritten x09 flush arm (`170d6a6`, `b1f3122`), which matches every new record to a frontier crossing and requires every superseder to be newer than what it supersedes. Residue opened as SP08-D2 and SP05-D2 (§21); the x09 comment block re-described on the fixed tree (`b64b3f6`). The bodies of `170d6a6` and `b1f3122` still call x09 red by design: they were written on the fix branch before the drain fix was ordered ahead of them at cherry-pick, and history is kept as committed. |
| 18 | Third-round F4 reviewer stopped mid-run (no result after 20 min, no live process) | tooling | `wf_362d4ca6-c2f` journal | The second-round review had already confirmed the defect independently; the fix was dispatched as its own author/reviewer pair (`wf_c56b70dd-cd3`, both items accepted first round). |
| 19 | `internal/store` killed at the 10 min default timeout inside the F4 author's four-package run | tooling / co-load | `scratchpad/f5a/daemon-all.txt`, `store-v.txt` | Re-run alone with `-timeout=12m`: ok in 218 s; the whole-tree step uses `-timeout=30m`. |
| 20 | bench-hotpath budget B-B (`l0_ingest`, gated, p99 < 2 ms) at p50 917.5 ms / p99 983.0 ms on AC (H1 on `0d5c999`; F3 on battery on `b64b3f6` read p95 1 048.6 ms / p99 1 179.6 ms) (V4: 3.8 ms) | **hot-path budget, design conflict** | `scratchpad/quiet/H1-bench-hotpath.txt`, `v5-bench-hotpath.json`, `scratchpad/f7c/` (profile, numbers, power probes), reviewed diagnosis | Since `fix(ipc)` every delivery is leased, and `Accept` pays SP-20's three durability points (four fsyncs) inside the timed region before the transport ACK; the p50 near 0.9 s is queueing behind the serialized `Accept` under the 2 000-hook burst, and each leased call costs 18–23 ms on a quiet AC window (E4, E4b) and 37–42 ms co-loaded (`scratchpad/f7c/`), four fsyncs' worth. Not a measurement artifact and not fixable without a budget or durability decision: opened as SP20-D1 (`deferred:V6-VERIFY`) with evidence `BenchmarkIngestAcceptLeased` (`0d5c999`); the ACK-deadline consequence is folded into SP05-D2. The B-B gate stays red, as V4 left it, and no budget was lowered. |
| 21 | Wave-4 subplans absent from `landedSubplans`: coverage floors for `internal/commands`, `internal/analyzer`, `internal/grammar`, `internal/admission` silently unenforced since the integration | coverage gate | `scratchpad/quiet/I1-cover.txt` (per-package figures), `scratchpad/f5ls/` | Fixed `b482011` (three copies, examples retargeted at SP-17; negative control: removing SP-14 fails `TestLandedSubplansMatchesTheBranch`). All four packages measured above their floors (88.0 / 88.2 / 91.4 / 100.0 %). `devtool cover` cannot reach its floor stage on this host because three deterministic test failures (Belady floor, two hot-path rows, plus the guards gate before the tsv update) stop `go test -coverprofile` first; the floors are evaluated by hand in §17 and bind on CI. SP-16 owns no package and cannot be listed, so `planDocsInScope` never derives wave 5 as landed — a limit of the scope rule handed to V6 planning. |
| 22 | Quiet pass invalidated twice: (a) the harness's low-memory kill left a first pass running beside its relaunch (two whole-tree runs interleaved into one output file, `-trimpath` breaking source-scanning tests); (b) the laptop was on battery (17 % → 5 %, CPU performance 55 %) for the benchmark and timing rows, and slept for two hours mid-pass | tooling / environment | `scratchpad/quiet/summary.txt` (every run's lines kept), power probes in `scratchpad/f7c/power.txt` | (a) killed with PowerShell, re-run once under a lock dir, detached from the harness; (b) the functional rows stand (they do not depend on the clock); every benchmark and timing row except F3 was re-taken by `scratchpad/bench-window.sh`, which refuses to run a step until the probe reads AC power or a battery charge of at least 25 %, ≥ 90 % processor performance and < 8 % foreign load, and records the window in `summary.txt` (F3, the x11 hot-path e2e row, was not re-taken because H1 measured the same B-B row on AC); the confirmation rows (`bench-window2.sh`) ran in the regimes §20 lists row by row. |
| 23 | `TestGC_DeadlineTruncatesAndResumes` scanned 511 objects before the first deadline check (< 256 expected) in the AC F1 run — the "too fast" direction of a wall-clock-shaped test | timing (ADR 0010 class) | `scratchpad/quiet/F1-ci-timing.txt`, `F1c-gcdeadline-count3.txt` | F1c `-count=3`, the probe before the step reading 95 %: `TestGC_DeadlineTruncatesAndResumes` 3 of 3 PASS, and `TestGC_DeadlineOvershootIsBoundedByTheCheckInterval` 3 of 3 with it (the latter logs a first check after 22.9–33.4 ms, window 8.5–10.6×, slack 2.9–3.3×; `scratchpad/quiet/F1c-gcdeadline-count3.txt`). The F1 failure (511 objects before the first time-based check) happened at 225 % processor performance right after the test's own calibration: the sweep outran the speed the test had just measured — a turbo ramp between calibration and sweep, the "too fast" direction of ADR 0010's class and the safe one (the sweep stopped at the next check, which the second test bounds). Recorded, no code change, no row; the calibrate-then-sweep shape is handed to SP-17's hardening list (a count-based first check would not depend on the clock). |
| 24 | `devtool bench-compare` stopped on four unclassified observer units on every whole-tree sweep; once runnable its only failing row was `PathsWriteAtomic_4KB` (+28.7 % B/s, `atomic.go` unchanged since the baseline commit `1e767c3`) while the real read-path regression (item 25) passed it silently, because with one sample per row benchstat's test has almost no power: it called 6 of 311 rows significant, and its one FAIL rests on the baseline's tied two-decimal MB/s values (the same row's sec/op, 41 % over the baseline median, was not flagged). That is the blindness V2-VERIFY recorded as its gates item 24, and `devtool bench` still runs one sample | bench gate (dead-gate mode, then no power) | `scratchpad/quiet/v5-bench.txt` (the sweep; `H2-bench.txt` is the step's empty console log), `H2b-bench-compare.txt` | Units fixed `1a1bbaf` (F8; `TestBaselineUnitsAreClassified` pins every baseline unit, negative control fails on a dropped unit). The WriteAtomic row is disk-bound (Q4) and left red. The single-sample limit is handed to SP-17 with the baseline regeneration: `devtool bench` needs `-count ≥ 4` for the gate to have power; §20's regression verdicts come from the budgets and the allocation columns, not from this gate. |
| 25 | `BenchmarkGetChunk` 88 µs against 60 µs and the wave-3 baseline's 45 µs; `BenchmarkSearch_1000Roots` 82 ms against 25 ms and the baseline's 45–52 ms; allocation columns up 18 → 26 and 30.3 k → 42.7 k | **performance regression, shipped path** | `scratchpad/quiet/E5-I-06.18.txt`, `E5b-store-count3.txt`, `v5-bench.txt`; `git diff 1e767c3..HEAD -- internal/store/read.go internal/store/objects.go` | Cause read from the diff: `16ecc77` (SP-20 M1) verify-on-read — bounded read with `Lstat`/`fstat`/`SameFile` and a plaintext content hash per object read; `Search` reads every candidate chunk through it. A deliberate integrity decision with an unbudgeted cost: opened as SP20-D2 (`deferred:V6-VERIFY`, evidence `BenchmarkGetChunk`); no budget lowered, baseline not regenerated. |
| 26 | 4.5: with the observation ledger blind, the digest renders an MCP elimination record `[active]` from the checkpoint's frozen copy while its real state is stale and `already_tried` reports unavailable (`rehydrate.eliminationCandidates` falls back on `ErrNotFound`) | **defect candidate**, uncertainty on one surface | `plans/sdd/V5-VERIFY/x05-disposition.md` ("Unverified remainder"), logged on every 4.5 run | Routed to the SP-11/SP-13 digest owner. `items.go` documents the fallback as a deliberate degradation, so it awaits that owner's ruling rather than a ledger row; 4.5 stays `partial` on the digest surface (§19, §29). |
| 27 | 4.5: `qompack pin --eliminated` records are stamped `Source=mcp`; `negknow.IngestPin`, which stamps `SourceSlashCommand` (Qompack.md §8.3 source #2), has no production caller | **defect candidate**, provenance label | same | Routed to the SP-14 owner; 4.5's subtest 5 asserts the shipped stamp exactly, so a change is seen rather than absorbed (§29). |
| 28 | 4.17: a delta side record content-addressed over its deltas alone is shared by two roots that removed the same volatile token, so the second root's exact recovery fails (`ErrDeltaCorrupt`) although its put reported `FidelityExact` | **recovery fidelity, persisted data (latent)** | `plans/sdd/V5-VERIFY/x17-disposition.md` ("Findings in `internal/store`"), the author's `x17-run2.txt` | Opened as SP20-D3 (`deferred:V6-VERIFY`, evidence `-`), latent because `RestoreOriginal` has no production caller (§21, §29). |
| 29 | 4.17: put reports `FidelityExact` and read reports `FidelityCanonical` for the same bytes when canonicalization changed nothing (and `storedFidelity` for a dedup hit) | label divergence that under-claims | same, the author's `x17-run1.txt` | Recorded in SP20-D3's section; the bytes are right and the read label errs in the safe direction; owner SP-20 (store). |
| 30 | Nine non-zero step lines no other item disposes of. On `f9e4232`: A4 build-all exit 1, and B1 and B2 exit 127 (the pass was abandoned for item 13's gofumpt fix and restarted on `7e0aac5`). On `7e0aac5`: two whole-tree race runs C1 exit 1 (2697 s, 1463 s), C2 exit 127, and B2 exit 1 — golangci-lint found two gocritic offBy1 slices in v5_x05, ineffectual assignments in v5_x10 and v5_x12, and a mixed-typing constant group in v5_x13. On `b64b3f6`: final2's B2 exit 1 and its orphan's (runpatterns' `go test -list` died with 0xc0000142 while two whole-tree runs competed for memory), and resume's C1 exit 1 (killed with the duplicate pass, item 22a) | gates and tooling | `scratchpad/quiet/summary.txt`; `scratchpad/quiet/B2-lint.txt` (final2's runpatterns); the `7e0aac5` C1 and B2 outputs were replaced by later runs of the same steps | B2's lint findings fixed in `0d3e77b` (`test(v5): satisfy golangci-lint in four section-4 tests`). C2 re-ran with exit 0 in the same pass. The race runs are superseded by resume2's C1 on `b64b3f6` (no data race) and C1b on `b1a0183`. A4 passed on `7e0aac5` and `b64b3f6`. The 0xc0000142 lint runs are superseded by B2 on `b1a0183` (all ten sub-checks PASS). |
| 31 | Two allocation-backed changes the budget verdicts did not show: `BenchmarkHookNoop_InProcess` 324 → 7 317 allocs/op and 34 KB → 517 KB/op (22× on the hook's in-process path; wall 0.54 → 2.14 ms, still inside 3 ms), and `BenchmarkPutBytes_100KB_Cold` 922 → 1 183–1 220 allocs/op (+28–32 %) | performance change, shipped path | `testdata/bench-baseline.txt`; `scratchpad/quiet/E1-I-01.24.txt`, `E5-I-06.18.txt`, `v5-bench.txt`; `scratchpad/inv/SP-01/I-01.24.txt` (7 323 allocs/op at `87c0c1d`) | Found by the independent review. Both are code, not host: neither benchmark file changed since `1e767c3`, and HookNoop already read 7 323 allocs/op on `develop` @ `87c0c1d`, so it arrived with the `internal/cli` and `internal/hookio` commits `develop` gained after `1e767c3` (SP-20's admission and capture work), not with a V5 commit. Neither breaks a budget on its own (HookNoop is inside; PutBytes was already over as SP06-D2), so no row is opened: HookNoop's growth goes to the SP-20 owner with SP20-D1, the hook path's other new cost, and PutBytes's is recorded in SP06-D2's section. |


**Snapshot and owner of each item** (plan §7; reproduction conditions are in each item's artifact):

| Items | Seen on | Owner |
|---|---|---|
| 1, 2 | `504f38f` (SP-15 merge on `develop`) | SP-15 |
| 3 | `0884ea8` (SP-16 merge), loaded host | SP-09 (co-load, Q28) |
| 4 | `87c0c1d`, while authoring 4.2 and 4.3 | SP-05 / SP-20 (hook client and delivery journal) |
| 5, 7, 8 | `87c0c1d`, while authoring 4.1, 4.15, 4.16 and in F1 | SP-14 |
| 6 | `87c0c1d`, while authoring 4.8 | SP-10 |
| 9 | `87c0c1d`, F2 | SP-06 / SP-20 (store) |
| 10, 11 | `87c0c1d`, F3 | SP-15 (selector), SP-14 (commands), eval; SP-12 for the ADR 0012 pointer |
| 12, 13 | the inventory commits before `f9e4232`; `f9e4232` (the first quiet pass's A1) | V5-VERIFY |
| 14, 18, 19, 22 | tooling and environment, no product snapshot | the checkpoint's coordinator |
| 15 | `e0eeea3` (fuzz pass) | tooling |
| 16, 17 | `7e0aac5` (B1 of the first quiet pass) | V5 e2e (SP-20 capture sidecars); SP-05 (the daemon drain) |
| 20 | `b64b3f6` (H1 on battery) and `0d5c999` (H1 on AC) | SP-20 with the hot-path budget owner (V6-VERIFY, SP-17) |
| 21 | `b64b3f6` (I1) | V5-VERIFY (`tools/devtool`) |
| 23 | `0d5c999` (F1) | SP-06 (store GC) |
| 24 | `0d5c999` (H2), fixed on `1a1bbaf` | V5-VERIFY (`tools/devtool`) |
| 25 | `0d5c999` (E5), confirmed on `1a1bbaf` (E5b) | SP-20 (store read path) |
| 26, 27 | `87c0c1d` with the 4.5 commits, while authoring 4.5 | SP-11 / SP-13 (digest); SP-14 (pin) |
| 28, 29 | `87c0c1d` with the 4.17 commits, while authoring 4.17 | SP-20 (store) |
| 30 | `f9e4232`, `7e0aac5` and `b64b3f6`, as the item says | V5-VERIFY (the quiet-pass tooling and the section-4 tests' lint) |
| 31 | `0d5c999` (E1, E5, H2) against the baseline; HookNoop's count already on `87c0c1d` | SP-20 (hook-path capture and admission); SP-06 (PutBytes) |

Recording, reinjection, replacement and experimental policies were never enabled during the checkpoint;
no privacy denial path was exercised beyond the tests that assert it (4.2, 4.15, 4.17).

**Rollback rehearsal.** Data cutover was not performed; the rollback drill is the executed
`internal/store` backup/import/rollback tests (I-06.19) and SP-21's T21-ROLLBACK-01 (pre-write
compatible-reader-or-backup). Older-binary reads of new-format writes are asserted by
`migrate_test.go`'s compatible-reader cases; automatic downgrade is not promised.

---

## 23. Coordinator rulings on the inventory questions

The sixteen inventory owners returned 64 questions (numbered here in subplan order; the full text is
in each `plans/sdd/V5-VERIFY/inventory-SP-NN.md` "Questions" section). Every one is ruled below.
"Recorded" means the ruling changes a report cell or the old-to-new map only; "V5 action" means this
checkpoint did work for it; "handed to" names the later owner. No ruling turns a failure into a pass.

| # | Row / topic | Ruling |
|---|---|---|
| Q1 | I-01.17 `ErrNotImplemented` survivor classes | Recorded. A fourth class is added for exactly the three named sites: a **guarded refusal or unreachable default that no conformance probe can reach** (`internal/analyzer/selector.go` P-selection gate, pinned by I-01.20's guard; `internal/eval/replay.go` live-provider refusal; `internal/commands/commands.go` `bodyFor` default). None is a failure. The list predates SP-14/SP-15 and is corrected in the old-to-new map, not in the historical row. |
| Q2 | `bench` survivor in `internal/cli` | Recorded and carried to the wave-5 plan set as a named decision: SP-17 implements a real `bench` fronting `test/bench/hotpath` or deprecates the row compatibly (V5 plan §6 already requires one of the two). Not an SP-17 pass. |
| Q3 | I-01.18 skip-grep | Recorded as RETIRED / FAIL on the literal command; the replacement gate is `devtool lint --only=stubskips`, taken from the quiet pass (§17). |
| Q4 | I-01.24 disk-bound budgets | Decided by the quiet pass (§20). If the breach reproduces alone, the two disk-bound rows join the reference-platform class (see SP06-D2/SP08-D1/SP10-D1 disposition in §21) rather than fail V5 on a Windows laptop. |
| Q5 | V4-SP01-04 `main.go` < 150 LOC | Recorded as manual inspection carried forward; handed to SP-17 (packaging) as a guard candidate. |
| Q6 | SP02-D6 resolver | V5 action: disposed `wontfix` with rationale, evidence test and detail-doc section (commits `24d80d7`, `f68b832`, `69d896b`, `ededc0d`; independently reviewed). |
| Q7 | Row text verbatim vs restated | Historical rows stay verbatim; corrections live in the old-to-new map (V4 convention retained). |
| Q8 | E-1 home | Confirmed: `TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus` and `TestReplayDriver_MaxCPUExceeded` are E-1's home. |
| Q9 | M0-G5 accounting rows | SP-02 is not short a row, but no retained row anywhere asserts the request ledger. New additive row **I-14.17** (SP-19 M0-G5 / SP-14 usage evidence: missing telemetry is unknown, never zero) is minted with evidence `internal/commands` `TestEval_MissingUsageIsUnknownNotZero` and the four ledger-accessor tests, run green on this tree (`scratchpad/i14-17-usage.txt`, 5 PASS). Category sums and rate-schedule provenance stay `unknown` (§25). |
| Q10 | T20-M2-02 negknow rows | The negknow half is owned by SP-09's rows (I-09.x, `AnswerUncertain`/`BloomOnly`/`StaleNote` executed there) and by section-4 test 4.5 (elimination through every surface, stale/uncertain/error). SP-03's green suite is not read as covering it. |
| Q11 | T20-M1-02 semantic vs physical spans | `internal/symbols` stays as-is: the distinction is carried by the store-side capture sidecar (SP-20 M1) and the MCP `expand` span metadata, both exercised by 4.2. No new MISSING row against `internal/symbols`. |
| Q12 | I-04.1 exact error strings | "An error is returned" is the accepted reading; no defect row. |
| Q13 | v4-sp04-05/06 | Recorded: both MISSING scores are overridden by executed evidence (test/dedup, 12 benchmarks). |
| Q14 | SP-04 carried-defect guards | V5 action: re-proved, both PASS (`scratchpad/q14-sp04-guards.txt`). |
| Q15 | I-05.16 not-yet-implemented producers | Restated in the daemon-side form (bind-time producer registry). Section-4 test 4.14 was authored on this tree and asserts the real producers through three real compositions; the two natively unobservable assertions stay retired per V4. |
| Q16 | I-05.9 ring-full spill clause | Struck from the row (V2 ruling #23 stands); not carried into V6. |
| Q17 | I-05.20 p99 command | Corrected to `devtool bench-hotpath` for the two p99 clauses; recorded in the map. |
| Q18 | I-06.3 "exact" | Reading confirmed: constructor name, not an exactness claim; no test asserts a tokenizer-equal figure and none is required (plan §5). |
| Q19 | backup/import/cutover/rollback rows | New additive row **I-06.19** (SP-20 M1 backup/import/GC/rollback drill) is minted in the report, evidence `internal/store/backup_test.go` and `migrate_test.go` (23 of 26 run green by the SP-06 owner; the rest run in the quiet pass's whole-tree step). Scored under SP-06 so it is not orphaned. |
| Q20 | I-06.15 append-only guard scope | V5 action: fix item F2 extends the guard to every store-owned log that is append-only by contract and documents the rewritten ones (§22). |
| Q21 | SP06-D2 | See §21: reference-platform measurement is not obtainable on this host; disposed with the quiet-pass Windows numbers and re-deferred to V6-VERIFY, whose SP-17 pushes to CI. |
| Q22 | I-07.11 `BuildToolUse` bench | Run in the quiet pass (E6). |
| Q23 | T20-M2-01/02 under SP-07 | Not scored under SP-07; the unknown-coverage assertions live in negknow/core rows and section-4 tests 4.5 and 4.14. Recorded. |
| Q24 | SP08-D1 ownership | See §21 (same disposition as SP06-D2). |
| Q25 | SP08-D1 summary width | Not widened: on the AC quiet window the 64 KB fixture reads p99 15.4–41.0 ms, inside B-C; only the co-loaded inventory run had put it over. The row keeps its 256 KB wording (§21). |
| Q26 | `TestE2E_ObserverThroughDaemon` spool degradation | Root cause found by 4.2/4.3: the hook client minted a 16-byte delivery nonce the journal refused, so live deliveries were unleased. Fixed in `fix(ipc)` (§22); the quiet pass re-runs the test alone (F2). Owner: SP-20/SP-05 seam, now closed. |
| Q27 | I-08.7 clause split | Clause-level split recorded: MAPPED + RETIRED (Strip nil semantics). |
| Q28 | `TestBudget_Open` has no row | V5 action: new carried-defect row **SP09-D1**, `deferred:V6-VERIFY`, evidence `TestBudget_Open`. The quiet pass shows the budget holds only at turbo clocks (257.8–273.4 ms CPU/op at 120 %, 585.9–632.8 ms at 55 %, §20), the pattern for which SP10-D1 stays unresolved; the first draft of this ruling closed it as co-load, and the independent review showed the serial B1 failure was the clock, not co-load. |
| Q29 | SP-20 lineage / uncertain artifacts | Executed-test evidence in the inventory and 4.5/4.14 dispositions is the accepted substitute; the two SP-20 checkboxes are recorded as delivered-unchecked (see [SP-20 checkboxes](V4-report.md)). |
| Q30 | I-10.16 `customInstructions` | The e2e assertion stays; the plan text describes a host-boundary correction that has not landed. Recorded as plan-vs-code drift, handed to V6 planning. |
| Q31 | T10-* gates without definitions | Planning artefacts of the forward-looking rewrite; recorded as `documented` (not MISSING rows) and handed to V6 planning with the shipped-code evidence attached. |
| Q32 | SP10-D1 ownership | V5 action: perf fix landed (`72f3226`); the AC turbo window reads Finalize at 43.75 ms, the battery base-clock window 66.6–73.1 ms over six runs, and 63.6–70.3 ms on AC at 128 % (E9c) — inside the 50 ms criterion only with turbo clocks, so the row is re-owned `deferred:V6-VERIFY` with SP06-D2 and SP08-D1 rather than closed (§21). The reference-platform figure decides it. |
| Q33 | `p4DischargedBy` names a non-existent test | V5 action: fix item F3 re-points the constant at the tests that exist while keeping the V4 §4.4 reference visible; V4 sign-off item 2 stays open (user decision). |
| Q34 | AdvanceSegment / ExtractDecisions breaches | Decided by the quiet pass (E9): reproduced alone → new rows; otherwise co-load noise recorded. |
| Q35 | SP-11 T11-* gates vs shipped code | (c) with a condition: the T11 gate set is a planning artefact superseded by the shipped, green historical contract; every T11 gate is recorded `implemented_unverified` in §24, and the V6 planning pass must reconcile the SP-11 plan to the code before any T11 gate is claimed. |
| Q36 | carry-forward clause | Retired in writing (successor: SP-11's overflow rework, ADR 0011); no rebuild. |
| Q37 | Windows platform skips | Recorded; both run on the Linux CI leg (outward-facing, user's call). |
| Q38 | sentinel last-line placement | Not contractual on this tree (`hook.additional_context_delivered` reads the sentinel by content, `internal/contract` tests); recorded as retired-with-pointer. |
| Q39 | L5 budgets not machine-enforced | Recorded as MISSING (I-11.19) with owner `internal/rehydrate` (a `BenchmarkBuild`); handed to SP-17's hardening list. |
| Q40 | I-11.17 row extraction split on a pipe | Data hygiene noted; the owner reconciled against the intended command. |
| Q41 | I-12.20 native reading | Kept as MAPPED-with-native-retired (seven passing gates are not discarded). |
| Q42 | No SP-12 carried-defect row | Intentional. |
| Q43 | rc-v4-sp12-01/22/25 unowned | Covered by the quiet pass (C4 race gate, B2 lint import purity, I1 cover); recorded under §17 rather than as new rows. |
| Q44 | I-13.5 skips in mcptest | Reading is "no skip may fire"; the guards stay. |
| Q45 | plugin-validate | Run in the quiet pass (A5). |
| Q46 | T13-* artifact gates | T13-ROLLBACK is discharged by SP-20's backup/rollback tests (I-06.19) and 4.3; T13-SEARCH's parity manifest and T13-HANDLE's installed-host transcript remain `implemented_unverified` (installed host is B01 evidence, user's environment). |
| Q47 | I-14.3 purity guard | V5 action: fix item F3 adds the scanner. |
| Q48 | I-14.5 degraded banner | RETIRED-with-pointer to 4.15, which asserts the degraded arm's status output against a real daemon. |
| Q49 | I-14.6 no `recover()` | V5 action: fix item F1 (§22). |
| Q50 | I-14.7 bloom-saturation warning | Deliberate drop by SP-14's rewritten spec; recorded as RETIRED, no carried defect. |
| Q51 | I-14.15 command e2e | Folded into 4.1 (real daemon, daemon-stopped, nothing-observed arms); recorded RETIRED-with-pointer. |
| Q52 | I-14.16 benchmarks | Honest Measure/Gated labelling accepted for V5; the benchmarks are owed to SP-17 (recorded MISSING with owner). |
| Q53 | `Deps.EvalArtifacts` unbound | Not a V5 blocker; SP-14 left it open by design (recorded `unsupported`). |
| Q54 | SP-15 zero benchmarks / fuzz | Recorded MISSING with owner SP-15/SP-17; a silent-success class the report names explicitly. Not accepted as a floor. |
| Q55 | checkpoint action-history fold | Retired outright: it conflicts with the checkpoint schema's two-key policy; the grammar's checkpoint half is SP-16/V6 scope if ever revived. |
| Q56 | `TestNoSelectorBypass` | V5 action: fix item F3 adds the AST scan to test/guards. |
| Q57 | e2e thrash coverage | 4.10 was authored on this tree (real daemon, eleven cycles, degraded arm); the stale `v3_x08` comment is corrected by F3. |
| Q58 | `ToolUsesBySession` | Stays a V5 sign-off item, recorded open in §29. |
| Q59 | SP-16 mechanisms unwired | Rows I-16.10/I-16.15 stay open and move to SP-17/V6 with the switches pending; V5 records them `unsupported` (disabled by design), never passed. |
| Q60 | CMS carry (I-16.17) | New carried-defect row is not opened: no warm-started CMS exists to test; recorded as MISSING with owner SP-16/V6. |
| Q61 | tuned truncation reserves | Retired formally in the report's errata list (QOMPACK-ERRATA entry proposed for V6 planning). |
| Q62 | I-16.14 enforcement | V5 action: fix item F3 adds the scanner. |
| Q63 | config-docs staleness guard | `devtool gen-config-docs --check` is the replacement gate (run in lint/CI); recorded MAPPED-CMD. |
| Q64 | SP-16 partial M6-G16-C, no independent review | V5 accepts SP-16 as delivered-partial: the report records it `implemented_unverified` for M6-G16-C; the independent review of this checkpoint (§28) covers the combined artifacts. |

---

## 24. Migration and capability gates

**SP-19/M0 prerequisites.** Mapping, packaged capability/lifecycle canaries, request ledger, settings
and baseline provenance: M0-G1/G2/G4 rows executed under SP-01 (versioned settings live inside the
`runtime` namespace — Appendix C is not superseded, V4's NC-1b holds with an executed pass); M0-G5
under I-14.17; M0-G6 under SP-02 (baseline provenance rows PASS). `verified_in_target` at the
repository-validator level; installed-host compatibility stays `implemented_unverified` (B01).

**SP-20/M1–M2.** Capture/privacy (4.2, 4.15, I-08.x), crash/publication (4.3), resumable
migration/backup/rollback (I-06.19), state authority and uncertainty (4.5, I-09.x), retention and
scope gates (4.12, 4.16). `verified_in_target` for every gate except uncertainty, which is **partial**: it
does not survive the digest surface under a blind ledger (4.5, §22 item 26). SP20-D1, SP20-D2 and
SP20-D3 are carried (§21). The two SP-20 commit checkboxes for the lineage and
uncertain-response artifacts are delivered-unchecked (Q29).

**SP-21/M4 enabled-surface matrix.**

| Surface | Enabled on this tree | Status |
|---|---|---|
| Admission gate (`internal/admission.Gate`) | off (zero gate refuses; explicit opt-in only) | `verified_in_target` disabled; recorded as disabled, never passed |
| Fresh-owned-result transformation vs processed-envelope bypass | mechanism only, no host allowlist entry | T21-RECURSE-01 passing; 4.17 (a) |
| Host allowlist | empty | T21-HOST-01 accepted unverified disposition |
| Durable capture/authorization before replacement; relative-delta reset causes | mechanism only | T21-PIPE-01, T21-BASELINE-01 passing; 4.17 (b) |
| Unsupported content pass-through / privacy denial | mechanism only | T21-PASS-01, T21-FIDELITY-01 passing; 4.17 (c) |
| Quality/recoverability vs unmodified output | no observations | T21-QUALITY-01 inconclusive by construction; 4.17 (d) records it as such |
| Replacement | off | stays off until target and regression gates pass |

**Plan-vs-code drift recorded for V6 planning (not scored as failures).** SP-10's T10-* gate table,
SP-11's T11-* gates, SP-13's T13-SEARCH manifest and T13-HANDLE installed transcript, SP-10's
host-boundary `customInstructions` correction (Q30/Q31/Q35/Q46): each is `implemented_unverified`
or `documented` here, with the shipped-code evidence attached in the inventories.

---

## 25. Request usage and estimated price (checkpoint execution)

Qompack itself made no model calls. The checkpoint was executed by a Fable 5.1 coordinator session
with subagent seats requested as `opus` (inventory) or the session model (authoring, review, fixes);
effective routing is not exposed (B08), so no model identity below is a verified billing route. Harness-
reported subagent output tokens per workflow: lane 1 226 k; section 4 4.22 M (first run) + 4.82 M
(resumed run, cached replays included); inventory 2.14 M + 277 k; carried defects 847 k + 346 k;
SP02-D6 138 k + 191 k; fix batch 670 k. Cache categories, TTL, retries and the coordinator's own
usage are not exposed to this session: `unknown`. Estimated price: `unknown` (no rate table applied;
subscription allowance is not cash per token).

---

## 26. Statistical outcomes

None claimed. No held-out task runs, stochastic baselines or admission quality trials were executed;
T21-QUALITY-01 is inconclusive by construction and is recorded as such. Plan §8 also asks for three
evaluation layers, controlled and held-out snapshots, and separate stock, current-Qompack,
corrected-recovery and admission comparisons, with observation masking as a baseline only where the
harness supports it. None of these was executed at this checkpoint: no layer was run, no snapshot
taken, no comparison made, and the replay harness's masking was not used as a baseline. The only numbers in this
report are executed test results and single-machine benchmarks with their conditions stated.

---

## 27. Privacy, retention and write-set

4.16 (`TestV5_NoPackageWritesOutsideDotQompack`) asserts the write set under the real binary;
`test/guards/writeset_test.go` and the extended append-only guard (F2) cover the store; 4.2 and 4.15
assert privacy denial precedes retrieval and survives the admission switch being off. Retention roots
and GC are covered by I-06.x and the phase-7 compaction tests (`CompactDemandLog`,
`CompactRetentionRoots`, rewritten-by-design and documented as such).

---

## 28. Independent review

Three independent reviewers ran read-only against `b1a0183`: fresh-context Claude Opus 5 subagents that took
no part in the checkpoint. A first reviewer on Claude Fable 5.1 stopped at the provider's rate limit after
one step, a check of the report's quoted SHAs (`scratchpad/review/sha-check.txt`), which is where the
off-branch SHAs of finding A-M3 first showed. The three split one brief
(`scratchpad/review-prompt-b1a0183.md`) three ways and wrote their evidence under `scratchpad/review/`.
The combined verdict is the most severe of the three: **accept-with-corrections**, with no blocker. Every finding
below was addressed in the closing commit; the full text of each is in the reviewer's JSON file.

| Reviewer | Scope | Verdict | Blockers | Majors | Minors | Claims sampled | Numbers checked | Record |
|---|---|---|---|---|---|---|---|---|
| A | plan coverage and consistency (brief checks 1, 4) | accept-with-corrections | 0 | 5 | 10 | 17 | 5 | `scratchpad/review/A-coverage.json` |
| B | sampling audit and failure protocol (brief checks 2, 5) | accept-with-corrections | 0 | 3 | 8 | 29 | 10 | `scratchpad/review/B-claims.json` |
| C | performance and carried-defect numbers (brief check 3) | accept-with-corrections | 0 | 8 | 14 | 6 | 51 | `scratchpad/review/C-numbers.json` |

**Reviewer A, in its own words.** The coverage reviewer found a record in V5-report.md for every plan §2–§8 item except the few listed as coverage gaps. The inventory arithmetic is exact: all 274 §2 row IDs appear in their inventory files, every §1–16 count reproduces, V1–V4 reports and all testdata/golden/baseline paths are unchanged since 87c0c1d, and the §0 merge order and SHAs check out. Against that, eight SHAs quoted in §22/§23 are pre-cherry-pick commits on scratch branches rather than verify/v5 commits, and the report names f40b504 as the quiet-pass candidate although every step ran on e0eeea3, 7e0aac5, b64b3f6, 0d5c999 or 1a1bbaf. The ticked §8 gate claims no unresolved mandatory regression in enabled scope, yet SP20-D1 (B-B red on the shipped leased path, made live by this branch's fix(ipc)) and SP20-D2 remain open, and §29/§30 mislabel both as reference-platform questions; defect candidates found by rows 4.5 and 4.17 are carried nowhere in §21, §22 or §29, and 4.5 is ticked against its own partial disposition. None of this makes the merge unsafe, so the reviewer's verdict for its share is accept-with-corrections, on condition that the five majors are fixed in the closing commit before verify/v5 merges into develop.

**Reviewer B, in its own words.** The claims reviewer sampled 29 report claims against git history and the scratchpad artifacts, and ran go test ./test/guards and five pins on b1a0183: the F4 drain pin, the crash-cut redelivery test, the SP08-D2 evidence test and the two new devtool pins, all passing (review/B-*.txt). The substance holds: the drain fix is what §22 says and its pin fails with the fix reverted, the carried-defect statuses match git and every evidence name and detail section resolves, the x13 fold, the x09 rewrite and the three fix commits are as described, and the waiver quote and the four V4 items in §29 match their sources. §17's provenance paragraph is wrong in three ways, however: no quiet-pass step ran on f40b504 (the functional pass ran on b64b3f6 and the fuzz on e0eeea3), the passes shared the machine with other work, and §22 omits nine non-zero summary lines, including a golangci-lint failure fixed by 0d3e77b. None of this makes the merge unsafe, because every commit after b64b3f6 touches only tooling, tests or plans and is covered by green targeted runs; the three majors must be recorded in the report before verify/v5 merges into develop, and the eight minors can follow.

**Reviewer C, in its own words.** The numbers reviewer re-derived all 127 per-step benchmark rows in §20 from the quiet-pass files and found every value and every verdict correct against the stated budgets. It also matched all 23 window lines, most §21 and §22 figures and the seven changed TSV rows' figures against summary.txt and the cited artifacts, and found no blocker: keeping SP10-D1 unresolved and treating SP20-D2 as a code change follow from the numbers, and nothing requires a code change before verify/v5 merges. It found eight majors to correct before merge: §22 item 20's B-B figure (p50 1 048 / p99 1 180 ms) is in no file; the §20 regime paragraph puts five AC-powered confirmation rows on battery at base clock; the regression paragraph says every other row sits inside the bands while the single-sample sweep has 12 rows over +25 %; and HookNoop_InProcess's 324 -> 7 317 allocs/op increase (inherited from develop) is reported only as PASS. The other four: OpenSpan's base-clock breach is missing from the verdict table, SP10-D1's '44-50 ms' and '>= 157 %' rest on an overwritten, uncited run that also reached the TSV, §21 has no SP20-D1 row, and not opening SP09-D1 contradicts the report's own figures (the serial B1 run failed at 585.9 ms CPU/op and was labelled co-load). Fourteen minors are rounding, citation and wording faults that change no disposition.

**Findings and what the closing commit did with each.**

| Id | Severity | Finding | Disposition |
|---|---|---|---|
| A-M1 | major | The ticked §8 gate claimed no unresolved mandatory regression in enabled scope, while SP20-D1 and SP20-D2 are regressions on shipped paths; §29 and §30 framed both as reference-platform questions. | Fixed: the §8 gate is unticked and marked NOT met, naming SP20-D1, SP20-D2 and SP20-D3 as carried under the waiver; §29 item 2 and §30 separate host questions from budget-versus-guarantee decisions. |
| A-M2 | major | The report named `f40b504` as the quiet-pass candidate; the steps ran on `e0eeea3`, `7e0aac5`, `b64b3f6`, `0d5c999` and `1a1bbaf`. | Fixed: §17 gives the commit of every row group and what shared the machine, and §18's R2 row names each commit; the whole tree and the race detector on the changed packages re-ran on `b1a0183` (B3, C1b). |
| A-M3 | major | Eight quoted SHAs were pre-cherry-pick commits on fix branches, and four fix commits were cited by type only. | Fixed: the eight replaced by their landed commits, `9c023ac`, `8ff89b8`, `d403363` and `a732424` cited in §19 and §22, and five historical SHAs in the SP-10 and x01/x05 documents annotated with their landed commits; a reachability scan finds every SHA the report quotes on the branch. |
| A-M4 | major | 4.5 was ticked although its disposition is partial, and its two defect candidates were routed nowhere. | Fixed: 4.5 and 4.8 unticked with their partial dispositions stated; the candidates are §22 items 26–27 and §29 item 11; §24 and the SP-20/M1–M2 box record uncertainty as partial on the digest surface. |
| A-M5 | major | Two `internal/store` findings from 4.17 (a shared side record breaks exact recovery; a put/read fidelity label divergence) were carried nowhere. | Fixed: SP20-D3 opened (`deferred:V6-VERIFY`, section in `V2-WAVE1-carried-defects.md`), §22 items 28–29 and §29 item 12; the §8 gate names it. |
| A-m1 | minor | The plan's SP-09 box said SP09-D1 was opened while the report said it was not. | Resolved the other way: SP09-D1 is now opened (C-M8), so the box is true and the report agrees. |
| A-m2 | minor | Plan pointers were wrong (the waiver is not in §21, grammar and phase 7 are not in §24), and the merge box was ticked before §28 existed. | Fixed: the pointers name the Authority paragraph and §18; the merge box stays ticked now that §28 records the review. |
| A-m3 | minor | Ticks contradicted the plan's legend: SP-21/M4, SP-15 selection and warnings, and 4.8. | Fixed: the four boxes unticked with the report's status word, including the two SP-15 ticked at its delivery (`db4ec2e`) before verification; 4.12 annotated as partial only in what its criterion does not guarantee. |
| A-m4 | minor | §18's SP-14 row named one gap where four exist. | Fixed in §18 and the plan box: the H3 route, `Deps.EvalArtifacts`, I-14.14 and I-14.16. |
| A-m5 | minor | Inventory scoring: I-11.19 counted PASS with an unmeasured clause, retired rows still counted MISSING, F5 mislabelled, I-01.19 unmapped. | Fixed by a note under the inventory table (I-11.19 partial, four MISSING retired by ruling, I-01.19 discharged by B1 and B3) and F5's new label; the per-inventory counts stay as scored. |
| A-m6 | minor | Every negative-control and remainder cell in §19's table was cut mid-sentence. | Fixed: each cell ends on a complete sentence and points at its disposition file; the rows are in numeric order. |
| A-m7 | minor | The plan's Status line still read 'not executed'. | Fixed. |
| A-m8 | minor | §21's 'Waves 0–2 untouched' ignored the wave-2 detail documents this range amends, and SP10-D1's status line was stale. | Fixed: §21 names the amended documents and why; SP10-D1's status line reads `deferred:V6-VERIFY`. |
| A-m9 | minor | The plan's §3b tally omitted the FAIL-BASELINE and FAIL-COLOAD-SUSPECT rows. | Fixed. |
| A-m10 | minor | §22's rows lacked the snapshot and owner fields plan §7 asks for. | Fixed: a snapshot-and-owner table follows §22's table; reproduction conditions stay in each item's artifact. |
| B-M1 | major | No quiet-pass step ran on `f40b504`, and the fuzz ran on `e0eeea3`. | Fixed with A-M2; the carry-over evidence (no fuzzed package changed since `e0eeea3`, nothing after `b64b3f6` touches production code) is stated in §17. |
| B-M2 | major | 'No other agent or session on the machine' was false for most passes. | Fixed: §17 lists what overlapped each pass: the fix batch during the fuzz pass, two processes in the `7e0aac5` pass, duplicate whole-tree runs on `b64b3f6`, and the B-B diagnosis beside resume2's last steps. |
| B-M3 | major | Nine non-zero step lines had no disposition, including a golangci-lint failure fixed by `0d3e77b`. | Fixed: §22 item 30 disposes of all nine. |
| B-m1 | minor | Cited first-pass artifacts had been overwritten, and C1's cached results were not disclosed. | Fixed: items 13 and 16 cite the surviving files; §17's C1 row says 42 of the 64 ok lines are cached. |
| B-m2 | minor | §22 item 20's B-B figures were not in the cited file. | Fixed with C-M1: H1's 917.5 / 983.0 ms, and F3's battery p95/p99 labelled as such. |
| B-m3 | minor | SP20-D1 had no row in §21, and the note credited `f40b504` with SP05-D1 and SP02-D6. | Fixed: SP20-D1's row added; the note names `66690d5`, `69d896b`, `f40b504` and the closing commit. |
| B-m4 | minor | The note overstated the guard's evidence rule, and SP05-D2's `-` departs from the ledger header. | Fixed: the note states the `-` exemption and why SP05-D2 has no pin. |
| B-m5 | minor | The V3 waiver sentence was stale against V3's Addendum 2. | Fixed: J5 reads RED, partially discharged, and the three-platform p99 is discharged into ADR 0008. |
| B-m6 | minor | I-11.19 was scored PASS with a MISSING clause. | Fixed with A-m5. |
| B-m7 | minor | B1 has 68 package lines, not 66. | Fixed. |
| B-m8 | minor | `170d6a6` and `b1f3122` call x09 red by design, yet sit after the drain fix. | Recorded in §22 item 17: written before the cherry-pick reorder; history kept as committed. |
| C-M1 | major | §22 item 20's B-B figure is in no file. | Fixed (see B-m2). |
| C-M2 | major | §20 put five AC confirmation rows on battery at base clock and claimed exact allocation agreement. | Fixed: the regime paragraph gives each confirmation row's own probe, and allocation agreement is stated as within 1 % where recorded. |
| C-M3 | major | The regression paragraph said every other row sits inside the bands. | Fixed: it says only that nothing else was called significant at one sample, and quotes the 12 rows over +25 % and 19 at +10–25 % against the baseline medians. |
| C-M4 | major | `HookNoop_InProcess` allocations rose 22× and PutBytes cold 28 %, both unrecorded. | Fixed: §22 item 31, §29 item 13, the §20 rows, and SP06-D2's summary and section. No new row: HookNoop is inside its budget, and PutBytes was already over as SP06-D2. |
| C-M5 | major | OpenSpan's base-clock breach was missing from §20's verdicts and from SP20-D2. | Fixed in §20 and in SP20-D2's row and section. |
| C-M6 | major | SP10-D1's '44–50 ms' and '≥ 157 %' came from an overwritten, uncited run. | Fixed: 42.8–43.8 ms at 170–208 % (E9 and the H2 sweep), with no threshold, in §20, §21, the ledger row and the SP-10 section. |
| C-M7 | major | §21 had no SP20-D1 row. | Fixed (see B-m3). |
| C-M8 | major | Not opening SP09-D1 contradicted the report's own figures: B1 failed at 585.9 ms CPU/op, a clock failure rather than co-load. | Fixed: SP09-D1 opened (`deferred:V6-VERIFY`, evidence `TestBudget_Open`, section in `V2-WAVE1-carried-defects.md`); Q28, §17, §20, §21, §22 item 3, §29, §30 and the plan's tally follow. |
| C-m1 | minor | E4b's ratio is 10.5–11.4×, not 10.6–11.4×. | Fixed. |
| C-m2 | minor | Finalize's allocations were not identical across windows, and E9 recorded none. | Fixed: 23.6–23.8 k wherever recorded, E9 noted as run without `-benchmem`; the SP-10 section's command line corrected. |
| C-m3 | minor | SP10-D1's 452 → 111 ms pair was measured on the fix branch, a different code state. | Fixed in §21 and the SP-10 section. |
| C-m4 | minor | The SP-08 section said E7 ran at `-benchtime 200x`. | Fixed: `-benchtime 2s`. |
| C-m5 | minor | The x09 red counts cited the wrong files and dropped two runs. | Fixed in §21 and the SP-08 section with the full tally and the right files. |
| C-m6 | minor | SP05-D2's '24–44 of 196' is not on file, and the leased Accept cost was misquoted. | Fixed: 25–26 of 196 (13 %) from the two saved runs; 18–23 ms quiet, 37–42 ms co-loaded. |
| C-m7 | minor | Item 24's account of benchstat was incomplete, and it cited an empty file. | Fixed: the tie artifact behind the one FAIL is stated, and the sweep file is cited. |
| C-m8 | minor | The queueing arithmetic mixed regimes and called sublinear growth linear. | Fixed in item 20 and the SP20-D1 section. |
| C-m9 | minor | SP06-D2's ledger summary still quoted V2's multiples. | Fixed: 5.2× and 14.1× at V5. |
| C-m10 | minor | 'At turbo' is not in V2's AddToolUse record. | Fixed. |
| C-m11 | minor | The declared `-benchtime 2s` rule did not match the confirmation rows. | Fixed in §20's opening sentence and the grading rule. |
| C-m12 | minor | The GC figures are logged by the overshoot test, and 'steady 95 %' rests on one probe. | Fixed. |
| C-m13 | minor | 'Same allocs (E5b)' cannot hold for Search, and a cited file name did not exist. | Fixed. |
| C-m14 | minor | Item 22(b) said every timing row was re-taken (F3 was not) and omitted the battery allowance. | Fixed. |

The reviewers' coverage gaps are closed as well: A's six by the Environment paragraph (host and schema versions), §20's paragraph on B-D and B-F, §26's statement on the evaluation layers, the snapshot-and-owner table, items 26–29 and the I-01.19 note; B's ten by item 30 and the SP20-D1 row. The closing commit edits only documents: this report, the plan, the carried-defect ledger and its detail sections, and the SHA annotations in three disposition files. The guard package, the three plan-document lint checks and the SHA reachability scan passed on its tree before it was committed.

---

## 29. Remaining blockers and next authorized action

**Open, carried (not blockers for cutting wave 5 under the recorded waiver):**

1. V4 sign-off items 1, 2, 3 (installed half) and 5 (reference-platform performance; scenario 4.4 or
   its authorized retirement; installed-host discovery; Belady p_min floor) — unchanged, user decisions
   or CI evidence.
2. Reference-platform performance rows SP06-D2, SP08-D1, SP09-D1 and SP10-D1 (the last two inside
   their budgets only at high turbo clocks on this host) need a Linux run; the only path is a pushed branch (outward-facing, not
   taken here). Two known regressions in enabled scope are not host questions: SP20-D1 (budget B-B red
   on the shipped leased delivery path, made live by this branch's `fix(ipc)` `9c023ac`) and SP20-D2
   (verify-on-read over the `GetChunk` and `Search` budgets; the allocation counts show it is the
   code). Each needs a budget-versus-guarantee decision by the owner of the hot-path budgets with
   SP-20's author; a Linux figure informs that decision but cannot make either row pass. Plan §8's
   gate of no unresolved mandatory regression is therefore not met, and both rows are carried to
   V6-VERIFY under the user's 2026-09-08 waiver.
3. SP-14 open by design: H3 `/qompack:checkpoint` route, `Deps.EvalArtifacts`, I-14.14 conformance suite,
   I-14.16 benchmarks.
4. SP-15 open by design: `ToolUsesBySession` redundancy coverage (`ErrDegraded`), representation-overhead
   calibration, zero benchmarks/fuzz in analyzer and grammar (silent-success class).
5. SP-16 delivered-partial: M6-G16-C, every phase-7 mechanism unwired and off; I-16.9/I-16.15–17.
6. SP-21 T21-HOST-01 competing-hook matrix and T21-QUALITY-01 — need an installed host and held-out tasks.
7. `bench` CLI survivor — SP-17 implements or deprecates.
8. Plan-vs-code drift list in §24 and the ADR 0012 pointer — V6 planning pass.
9. Windows platform skips (`TestIndex_SkipsSymlinks`, `TestService_StateWriteFailureStillEmits`, the two
   symlink pins of SP10-D1) — Linux CI leg.
10. SP08-D2 (observer idempotence under a reused lease) and SP05-D2 (ACK-deadline fallback rate) —
    both opened by F4, owner V6-VERIFY; the observer change needs its own review against SP-20's
    capture contract, and until it lands x09's SubagentStop count is co-load-sensitive on this host.
11. Defect candidates from 4.5, routed and unruled: the blind-ledger digest renders a stale record
    `[active]` (SP-11/SP-13 owner), and pin records are stamped `mcp` because `IngestPin` has no
    production caller (SP-14 owner) — §22 items 26–27; 4.5 stays `partial` until both are ruled.
12. SP20-D3 (a delta side record shared across roots breaks exact recovery for all but the first;
    latent) and the put/read fidelity label divergence beside it — §22 items 28–29, owner SP-20.
13. The hook path's allocation growth (`HookNoop_InProcess` 22×, inherited from `develop`) goes to the
    SP-20 owner with SP20-D1; PutBytes's cold allocation rise is recorded with SP06-D2 (§22 item 31).

**Next authorized action.** Merge `verify/v5` into `develop` (local, `--no-ff`) after the independent
review in §28; then wave 5 (SP-17, SP-18) may start from the verified `develop`. The `v0.4.0` marker is
not applied: no tag exists beyond `v0.2.0`, and pushing a `v*` tag cuts a real Release.

---

## 30. Signoff

**Signed off for the merge of `verify/v5` into `develop` under the recorded waiver, not as a clean
gate.** Every plan §3 criterion that this host can decide is decided in §1–§22 with executed
evidence; what this host cannot decide is named, not scored: the reference-platform performance rows
(SP06-D2, SP08-D1, SP09-D1, SP10-D1), the installed-host and held-out-task evidence, and the V4 sign-off items
the user's instruction waives (§ Authority). Two known regressions in enabled scope, SP20-D1 (the B-B
gate, red on the shipped leased delivery path) and SP20-D2 (the store read path), are not host
questions but budget-versus-guarantee decisions, so plan §8's gate of no unresolved mandatory
regression is not met; both are carried to V6-VERIFY under the waiver, with the latent SP20-D3 and
the two defect candidates from 4.5. The checkpoint found and fixed four
shipped-path correctness defects (the delivery nonce width, the drain's replay of acknowledged
copies, `Finalize`'s pointer resolution, the recover-less status collector) and opened five rows for
what it could not fix without a design decision (SP08-D2, SP05-D2, SP20-D1, SP20-D2, SP20-D3), plus SP09-D1, a timing row of SP10-D1's class. No budget was
lowered, no baseline or golden file regenerated, no older report edited. The independent review in §28
ran on `b1a0183`, the tree this report describes; the closing commit folds in its findings, and §28
lists each with its disposition. Next authorized action: §29. Executed by the session coordinator (Claude Code) on the
user's instruction; the user is the signing authority for the waiver and for every outward-facing
step (push, CI, tag), none of which was taken.

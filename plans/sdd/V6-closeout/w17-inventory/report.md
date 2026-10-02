# Wave 17 inventory

Branch `closeout/w17-inventory`. Workflow `wf_bd12d061-33a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `387867f39be0c3224a171c1af032a01d32822ae1`

### Summary

No product code changed and nothing that goes into a bundle was touched. All edits are under plans/, on closeout/w17-inventory (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-inventory). Nothing was pushed.

## C6.2: inventory on candidate 6

**What I changed.** plans/sdd/V6-remediation/inventory-current.tsv now has two more columns, `c6_result` and `c6_evidence`. Every one of the 304 rows has a value; none was added, dropped or renumbered. I checked by script that the seven original columns are byte-identical to the committed version, and that the file stays LF.

**Vocabulary.** I used the file's existing terms. `partial_verified` is the pending state: every automated step is green on candidate 6, and a live (Phase 4), quiet C5.1 or C5.5 part is still outstanding and named in the cell.

**Results.**

| Result | Rows |
|---|---|
| verified_in_target | 257 |
| partial_verified | 28 |
| implemented_unverified | 1 |
| failed | 5 |
| unknown | 7 |
| unsupported | 3 |
| documented | 3 |

**New map.** plans/sdd/V6-closeout/inventory-c6-map.md is written from the TSV by gen_c6map.py. It covers:
- the evidence codes (log paths, job ids, which parts are red or pending);
- the rule;
- a table of every row that is not verified, with what closes it;
- the 14 section-3 identifiers;
- the SP19/20/21 switches.

I put superseding notes at the top of inventory-current-notes.md and current-candidate-gate-map.md, and a short note in inventory-map.md. The scripts (dispose.py, symidx.py, closure.py, gen_c6map.py) and proof logs are under plans/sdd/V6-closeout/w17-inventory/.

**Proof the evidence covers candidate 6** (in runs/unchanged-proofs.txt):
- **Pre-freeze runs.** `git diff --stat 61b0cd66 99d0b18 -- . ':(exclude)plans'` is empty. The only commit in 61b0cd66..9a56b305 is the docs waiver, and 99d0b18^2 is 9a56b305. So the pre-freeze Windows logs ran the candidate's own product and test code.
- **C5.2 numbers from candidate 5 (0d06ab12).** No product file changed in store, negknow, canon, chunk, symbols, dag, sketch, eval, config, paths (only the pathstest helper), scheduler, rules or skills.
  - The files the other benchmarks actually run are unchanged: observer tooluse.go and tombstone.go, checkpoint finalize/writer/decisions/inject/truncate.go, the daemon's scheduler_*.go, and every benchmark file.
  - Changes elsewhere in their imports are additive or off the measured path (details in runs/c52-closure-c5-to-c6.txt).
  - hostperm/policy.go did change, so its BenchmarkEvaluate number does not carry. The map lists it under 1.12.17 only because of a name clash; that row rests on the scheduler benchmarks.
- **Symbols.** symidx.py re-checked every test name the map cites against the candidate. The only missing ones are the three TestCheckpoint_* tests of 1.14.5, removed by D36(a).

**Evidence used.**
- Windows: pre-freeze internal, testpkgs and integration; p3-win-race; p3-win-e2e-timing (only X11 red); p3-win-timing (all 12 rows pass); two six-target bundle builds, byte-identical (91 files); host-validate.
- Linux: hosted test ubuntu and macos with -race; the container's isolated timing and e2e-timing runs; then the container -race lanes, which finished green during this task (whole tree every package PASS, e2e 336 pass / 0 fail, child set 8/8) and changed no row.
- Hosted: ci.yml 36955046276 (verify, lint-windows, docs, cover, security, crossbuild, plugin-validate, replay-gate, test-e2e) and nightly 36955043924 (fuzz ×27, race-product-child, replay-recorded).
- Per D53(b), the Linux fsync-bound rows (B-A, B-B) are not verified in target.

**Failed (5):**
- **1.5.19, 1.17.11, 1.17.14** – the hosted `test (windows-latest)` job runs everything twice. On the second pass, TestFault_Lifecycle/out_of_order_sessionend failed with a dangling retention root, owned by internal/daemon. It passed in every other run.
- **1.17.18** – release-dry-run failed: release-check reached its `ci-local cover` step and failed on X11 on ubuntu. Every step after that (including rollback rehearsal, plugin-validate and marketplace) was not reached.
- **1.17.19** – the hosted CI run on 99d0b18 has those two red jobs. The four SP-17-era job names (package-gate, platform-matrix, fault-gate, install-gate) were never created. develop has no branch protection (`gh api` says "Branch not protected") and main does not exist on origin.

**Pending, and what closes each.**
- **Candidate 6 live lane:** 1.6.4, 1.8.6 (UAT-06), 1.10.14, 1.11.11, 1.11.12, 1.13.4, 1.13.5, 1.13.14, 1.17.7, 1.17.8, 1.17.9, 1.17.12, 1.17.15 (C1.7 restore smoke).
- **Quiet C5.1 tonight:** 1.5.12 (also needs X11's disposition), 1.10.16, 1.12.14, 1.17.6 (implemented_unverified).
- **C5.5:** 1.14.6.
- **Not re-run on candidate 6.** C4.2 and UAT-02 passed on candidate 4 (9f6a2fad), with 204 product files changed since. That affects 1.5.15, 1.8.6, 1.17.16, and the agent-run half of 1.18.12. C4.11's Linux model sessions are recorded unknown (D34(c)).
- **Release steps:** 1.17.17 closes at C7.1/C7.4 with `release-check --tag v0.3.0`. Note that core.Version and plugin.json still say 0.1.0 at the candidate; the bundles say 0.3.0 only because `bundle --version` stamps them.
- **No artifact possible for part of the row:** 1.11.16 (no rehydrate benchmark), 1.14.10, 1.18.1, 1.18.3, 1.18.4, 1.18.8, 1.18.11 (sub-assertions retired under F-5).

**Unknown (D37(c)):** 1.10.18, 1.15.14 (no analyzer or grammar benchmark), 1.16.5, 1.16.10 (no ADR 0016 and no declaration anywhere), 1.17.3 (frozen binaries are 9.29–10.49 MB, recorded as a diagnostic), 1.17.5, 1.18.12.

**Unsupported:** 1.14.5 (D36(a)), 1.15.6 (F-3), 1.15.7 (E-1).
**Documented:** 1.1.1, 1.1.28 (Qompack.md v1.8), 1.18.9.

**Section-3 identifiers.**
- Verified: 3.2, 3.7, 3.8, 3.13.
- Unsupported: 3.5.
- Partial, waiting on the live lane: 3.1, 3.3, 3.4, 3.6, 3.9, 3.10, 3.11, 3.12.
- Failed: 3.14. I added X11 to its mapping; it was not in the original notes table.

**Switches.**
- Shipped disabled (experimental): rawEvidence, durableFrontier, newResult, experiments.enabled, runtime.selection.submodularEnabled, runtime.selection.loopWarningsEnabled.
- Retired (unsupported): automaticVeto, blockManualCompact.
- reinjection.sessionStartCompact, the one enabled switch: partial, waiting on the live C4.3 compaction run.

**Corrections to earlier records:**
- Delivery-journal rollover now ships on by default (D2, C1.10; SP20-D4 fixed). The old notes said default-off and not accepted.
- The notes had two wrong switch keys; they live under `runtime.selection`.
- TestBudget_DetectorScan and TestBudgetBF, which used to fail on develop, now pass in both isolated timing passes.

## C6.3: carried defects

All 28 rows in plans/CARRIED-DEFECTS.tsv were already fixed or wontfix; D54 had disposed C2.3–C2.8. No status changed, so no test/guards expectation data changed either.

I added a "candidate 6 confirmation" section to each of the five detail documents (V2-SP-02, V2-SP-04, V2-SP-08, V2-SP-10, V2-WAVE1). Each gives every row's ruling, with the commit that set it, and its candidate 6 evidence: the evidence tests pass on Windows, Linux and macOS, and the D54 benchmarks' covered code is unchanged since candidate 5. All the cited commits are ancestors of 99d0b18.

I checked what test/guards reads: only carrieddefects_test reads the files I changed. So I ran its three tests by exact name, and all passed (runs/guards-carrieddefects-windows.log).

## Criterion changes

None. No check was weakened.

Interpretation calls I made, so you can review them:
- Rows naming the whole test/fault package, or the hook-exit fault matrix, are marked failed because of the hosted Windows red. The actual failure was a dangling retention root, not a hook exiting non-zero.
- The C5.2 numbers carry to candidate 6 where the code the benchmark runs is byte-unchanged; additive declarations elsewhere in its imports don't count as a change.
- §3.14 includes X11.

### Commits

- 73a9fada72c5ef691ed91f58dcf28a048200ea07 chore(v6): add the candidate 6 inventory tooling and proofs
- 10820137f6c65afd8eecc609059bae8b08b5876b docs(v6): dispose the 304 inventory rows on candidate 6
- d2d8dd016103f434af773591b9c9d53b1a1e71d7 docs(plans): confirm the carried defects on candidate 6
- 387867f39be0c3224a171c1af032a01d32822ae1 docs(v6): fold the c6 container race lanes into the inventory map

### Tests

- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_ManifestIsWellFormed$' ./test/guards/` — PASS (runs/guards-carrieddefects-windows.log)
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_OpenRowsHaveLivingEvidence$' ./test/guards/` — PASS
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_WaveReportRequiresResolution$' ./test/guards/` — PASS
- `go run ./tools/devtool fmt-check` — exit 0 (runs/lint-subset-windows.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS, exit 0. Run after commits 2 and 3. The fourth commit is a small edit to inventory-c6-map.md made during quiet.sh, so I did not re-run lint on it; I grepped it by hand for the placeholder, go-test and coverage-floor patterns those checks look for.
- `python plans/sdd/V6-closeout/w17-inventory/dispose.py --write; python .../gen_c6map.py` — 304 rows, 7 original columns byte-identical, counts as in summary
- `go vet (Windows / GOOS=linux), go test ./test/docs, gen-*-docs --check` — not run: no Go code, docs/ or generated inputs touched

### Criterion changes

- None weakened. Interpretation 1: rows that name the whole test/fault package (1.5.19, 1.17.14) or the hook-exit fault matrix (1.17.11) are marked failed because of the hosted Windows TestFault_Lifecycle red, even though that failure is a dangling retention root rather than a hook exit. Rationale: an executed red in the row's named evidence on the candidate cannot be verified_in_target.
- Interpretation 2: C5.2 numbers measured on candidate 5 are accepted for candidate 6 when the code the benchmark runs is byte-unchanged; additive declarations elsewhere in the import closure (core.CutHostPluginTool, obs nonrefdisk/spool_submode, checkpoint's nil-checked PricedDrops hook, observer prompt-path changes) do not count as a change. The proofs are in runs/unchanged-proofs.txt. hostperm BenchmarkEvaluate does not carry.
- Interpretation 3: §3.14 HotPathHoldsEverySubsystemResident now includes X11 (TestV3_HotPathUnchangedWithLedgerResident), which was not in the notes' original mapping, so §3.14 is failed on candidate 6.

### Open issues

- RED-FAULT: TestFault_Lifecycle/out_of_order_sessionend failed on the second pass of the hosted windows-latest -count=2 job (110676058036), leaving a dangling retention root owned by internal/daemon. Rows 1.5.19, 1.17.11, 1.17.14 and 1.17.19 stay failed until the w17 ci workstream records a root cause and fix, or a disposition.
- RED-X11: TestV3_HotPathUnchangedWithLedgerResident is red on Windows when run in isolation (p3-win-e2e-timing, p3-win-x11-alone: B-A p99 81.9/98.3 ms and B-B 57.3/81.9 ms against 50 ms, 593 deliveries deferred, spawn floor p50 29-30 ms) and in hosted release-dry-run. §3.14 is failed and 1.5.12 cannot be verified until w17 x11win dispositions it.
- RED-RELDRY: hosted release-dry-run (110676058084) stopped at release-check's ci-local cover step, so build-all, generated docs, guards, licenses, determinism, rollback rehearsal, plugin-validate and marketplace never ran on candidate 6. Rows 1.17.18 and 1.17.19 are failed. A green release-check on the candidate is still owed.
- Pending P-C51: quiet C5.1 on candidate 6 (phase3/c6/quiet) was still running. It closes 1.5.12, 1.10.16, 1.12.14 and the B-A/B-E half of 1.17.6 on Windows. Linux B-A/B-B stay not verified in target (D53(b)).
- Pending P-LIVE: the candidate 6 live lane (rerun-parts-c6.js) closes 1.6.4, 1.8.6, 1.10.14, 1.11.11, 1.11.12, 1.13.4, 1.13.5, 1.13.14, 1.17.7, 1.17.8, 1.17.9, 1.17.12, 1.17.15 and §3.1/3.3/3.4/3.6/3.9/3.10/3.11/3.12. C4.2 and UAT-02 are not in that lane; they are carried from candidate 4 with a carry-forward note (rows 1.5.15, 1.8.6, 1.17.16, 1.18.12).
- Pending P-C55: 1.14.6 needs the C5.5 confirmatory evaluation (D53(g)).
- 1.17.17 and release versioning: core.Version and plugin.json still read 0.1.0 at 99d0b18, while the bundles are stamped 0.3.0 by bundle --version. This closes at C7.1/C7.4 (release-check --tag v0.3.0).
- 1.17.9: TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting and TestInstall_HostCLIInstallUpgradeUninstall skip without the claude CLI (container, hosted). The Windows chain log is non-verbose, so whether they ran on candidate 6 is unproven.
- 1.17.19 also needs branch protection on develop and main, which happens at C7.3.
- The candidate 6 evidence directory (phase3/c6/, incl. the linux/ artifacts) is still uncommitted in qompack-v6. The TSV and the map cite those paths, so the coordinator must commit it for the citations to resolve.
- The TSV and the map record every Phase 3 lane except quiet C5.1, which was still running. When the live lane and C5.1 land, re-run dispose.py and gen_c6map.py after updating the relevant per-row decisions in dispose.py.

## Independent review

### review:inventory: needs-fixes

- **major** `plans/sdd/V6-closeout/w17-inventory/dispose.py (override o("1.5.12", "partial_verified", ...)); plans/sdd/V6-remediation/inventory-current.tsv row 1.5.12; plans/sdd/V6-closeout/inventory-c6-map.md:130` — Row 1.5.12 (the hot-path budget) is marked partial_verified even though candidate 6 has red results for its own assertion. That breaks the map's own rule. The rule says `failed` applies when "a candidate 6 artifact for the row's assertion is red", and `partial_verified` requires that "every automated step the row names is green". It also disagrees with how the same evidence was handled elsewhere: §3.14 is marked failed on the same RED-X11, and 1.5.19 is marked failed for a red in a different test of the package it names.
  - Evidence: The row's named symbol is `devtool bench-hotpath; CI bench-gate`. D28 (in the inventory-map.tsv note) says isolated wall-clock hot-path rows are judged in the isolated timing pass. On candidate 6, X11's run of bench-hotpath without the ledger exits non-zero on Windows in isolation: p3-win-e2e-timing.log B-A p99 81.92 ms and B-B 57.34 ms against 50 ms; p3-win-x11-alone.log 98.30 ms and 81.92 ms. The Linux timing step the row names (LTIME) is also red on TestIntegration_HotPathWarmWithRealResidentState (linux/cx-p3-p3-linux-timing-.../summary.txt). The cell itself says "RED-X11 measures the same B-A/B-B on Windows and is red on c6", yet the map's §3.14 row (line 200) is `failed` on the same red.
  - Fix: In dispose.py, set 1.5.12 to `failed` with the RED-X11 code, with the note that it clears when w17 x11win records a root cause or disposition and P-C51 lands. Re-run dispose.py and gen_c6map.py so the row moves into the 'Failed on candidate 6' table and the counts change (verified 257, partial 27, failed 6). If the coordinator wants to keep it partial, record that as a named criterion change with a D-ruling instead of leaving it in the cell's prose.
- **minor** `plans/sdd/V6-closeout/inventory-c6-map.md:91-92; dispose.py 1.8.13 note; plans/V2-SP-08-carried-defects.md (SP08-D1 c6 row)` — The C5.2 carry for the observer benchmarks rests on a false statement: that observer's candidate 6 changes are "on the prompt path only" because tooluse.go is unchanged. In fact OnToolUse calls the changed prompt.go collectThrash, and sessionState gained two fields. The benchmark's measured path is unchanged only because its harness wires no Grammar. More broadly, the carry uses per-file diffs, where the task asked for git diff --stat over the covered paths. internal/obs (1.1.16), observer (1.8.2, 1.8.13), checkpoint (1.10.17, 1.16.11), cli (1.1.27) and daemon (1.12.17, 1.16.11) all have a non-empty non-test package diff between 0d06ab12 and 99d0b18.
  - Evidence: internal/observer/tooluse.go:292 calls `o.collectThrash(st)`. The collectThrash body changed between 0d06ab12 and 99d0b18 (internal/observer/prompt.go:320-325, ThrashFloor). internal/observer/observer.go:336-340 adds ThrashFloor and ReplyWarning to sessionState. benchHarness (internal/observer/tooluse_test.go:469-480) passes no Grammar, so the `if o.opt.Grammar != nil` branch is never taken. `git diff --stat 0d06ab12 99d0b18 -- internal/daemon ':(exclude)internal/daemon/*_test.go'` gives 13 files, +1320/-80.
  - Fix: Correct the wording in all three places: "OnToolUse calls the changed collectThrash only when a Grammar is wired; the benchmark harness wires none, so the measured path is unchanged; sessionState gained two fields." Then either get the owner to record interpretation 2 (narrowing package-level diffs to the files the benchmark executes) as a D-ruling, or re-measure the affected C5.2 benchmarks on candidate 6 in tonight's quiet window and keep those rows partial_verified until then.
- **minor** `plans/sdd/V6-remediation/inventory-current.tsv row 1.13.17; dispose.py override 1.13.17` — 1.13.17 is promoted to verified_in_target by declaring that its installed-host half "is C4.4's (P-LIVE), not this row's". That narrows the criterion without saying so: it is not listed in criterion_changes.
  - Evidence: The inventory-map.tsv note for 1.13.17 says: "partial_verified historically: not every documented tool checked against the installed host (C4.4 covers the live half)." The c6 cell reads: "the installed-host half of every tool is C4.4's (P-LIVE), not this row's".
  - Fix: Make 1.13.17 partial_verified pending P-LIVE (C4.4), like 1.13.14. Otherwise list it as interpretation 4 in criterion_changes for the owner.
- **minor** `plans/sdd/V6-remediation/inventory-current.tsv row 1.17.4 (c6_evidence 'WIN+LNX')` — Row 1.17.4 is verified_in_target, but its current_test_symbols still name TestPlatform_WindowsHookLauncherForms, which no longer exists. The c6 cell does not mention the rename. packaging/report.md had handed this item to C6.2 explicitly. symidx.py missed it because it checks inventory-map.tsv's already-resolved symbol column, not the TSV's symbols.
  - Evidence: plans/sdd/V6-closeout/packaging/report.md:108: "TestPlatform_WindowsHookLauncherForms was renamed TestPlatform_HookLauncherForms ... inventory row 1.17.4 ... still name the old test, for C6.2." The map notes the rename only for §3.2 (inventory-c6-map.md:181-182).
  - Fix: Add a dispose.py override note for 1.17.4: "judged on TestPlatform_HookLauncherForms (renamed from TestPlatform_WindowsHookLauncherForms, now every OS), green WIN, LNX". Then regenerate.
- **minor** `plans/sdd/V6-closeout/inventory-c6-map.md:60 (FUZZ row); implementer summary 'fuzz x27'` — The fuzz job count is wrong.
  - Evidence: `gh run view 36955043924 --json jobs` returns 31 jobs, all success: 25 fuzz, 3 bench-deep, race-product-child, race-windows and replay-recorded. There are 25 fuzz jobs, not 27.
  - Fix: Change it to "nightly fuzz matrix, 25 jobs". Optionally cite race-windows 110676054888 under WRACE as hosted corroboration.
- **minor** `plans/sdd/V6-closeout/inventory-c6-map.md:79 (CARRY-C4)` — "204 product files changed" overstates the change. The cited command counts test files too.
  - Evidence: `git diff --shortstat 9f6a2fad 99d0b18 -- internal cmd plugin` gives 204 files. With `':(exclude)*_test.go'` it gives 98 files, +5720/-654.
  - Fix: Write "204 files under internal/, cmd/ and plugin/ (98 non-test product files)" and cite both commands.
- **nit** `plans/sdd/V6-closeout/inventory-c6-map.md:61 (GATE row)` — GATE cites prefreeze/gate.log as green, but that step exited 1. It is green only together with the re-run after the waiver.
  - Evidence: prefreeze/gate.log:198-209 shows `FAIL runpatterns ... devtool: lint: failed sub-check(s): runpatterns`, and summary.log shows `step gate exit=1`. runpatterns-after-waiver.log shows `PASS runpatterns`.
  - Fix: State it in the row: "gate.log is green except runpatterns, which passes in runpatterns-after-waiver.log after the 9a56b305 waiver".
- **nit** `plans/sdd/V6-closeout/w17-inventory/runs/map-symbols-at-c6.txt; symidx.py:9; TSV row 1.15.8` — The summary says the only missing symbols are 1.14.5's three TestCheckpoint_* tests. symidx's own output also lists 1.15.8 internal/analyzer:Propose. That entry is a false positive: the `Prop` prefix in the FUNC regex matches the product function Propose. Separately, 1.15.8's mapped symbols (rehydrate Prop*) do not match its Sequitur-invariants assertion.
  - Evidence: map-symbols-at-c6.txt lists `"1.15.8": ["internal/analyzer:Propose"]`. The real evidence is TestSequitur_InvariantsHoldAfterEveryAppend (internal/grammar/sequitur_test.go:289), which runs in the WIN and LNX trees.
  - Fix: Record the false positive in the proofs. In 1.15.8's c6_evidence, name TestSequitur_InvariantsHoldAfterEveryAppend as the executed evidence.
- **nit** `TSV row 1.5.12 / 1.17.6 / 1.17.5` — 1.5.12 names `CI bench-gate`, but its cell never cites the hosted bench-gate results. 1.17.6 (`implemented_unverified`) and 1.17.5 (`unknown`) are both D37(c) launcher-split rows yet get different results without an explanation.
  - Evidence: On 99d0b18, bench-gate jobs 110676058217, 110676058253 and 110676058353 all succeeded. D37(c) groups "1.17.5/1.17.6 launcher split".
  - Fix: Cite the bench-gate jobs (report-only under Q1) in 1.5.12. Either give 1.17.6's launcher half the same `unknown` reason as 1.17.5, or explain why the result differs.
- **nit** `plans/V2-SP-10-carried-defects.md (SP10-D1 c6 row); inventory-c6-map.md:91` — The claim that candidate 6's only checkpoint change is the nil-checked PricedDrops hook leaves out other product changes.
  - Evidence: `git diff 0d06ab12 99d0b18 -- internal/checkpoint/types.go precompact.go` also adds the DropKindUnreplayedCapture/DropKindUnreplayedToolResult constants and the exported MaxPreCompactWindow constant. All of them are additive.
  - Fix: Reword it as: "candidate 6's checkpoint product changes are additive: the nil-checked PricedDrops hook on preCompact, two drop-kind constants and MaxPreCompactWindow; Finalize enters none of them".
- **nit** `plans/sdd/V6-closeout/inventory-c6-map.md (evidence codes C52, P-LIVE)` — Some cited paths do not exist on this branch's base (9a56b305), only on verify/v6 99d0b18: phase3/c5/quiet/c52-names.tsv and coordinator/rerun-parts-c6.js. The open issues name only the uncommitted phase3/c6 directory.
  - Evidence: `git cat-file -e HEAD:plans/sdd/V6-closeout/phase3/c5/quiet/c52-names.tsv` fails; the same check on 99d0b18 succeeds. The same is true for coordinator/rerun-parts-c6.js.
  - Fix: Add a note in the open issues: these citations resolve only once this branch is merged with verify/v6 and phase3/c6 is committed.

## Fix seat (review resolution) — status `done`, head `86a95960adab14d9de898dde998ead4f6754a638`

### Root cause

Five review findings were factual or consistency defects in the first pass. Finding 1: a hot-path row was left pending despite a red candidate 6 artifact for its own assertion (X11's isolated Windows bench-hotpath runs). Finding 2: the observer C5.2 carry was misworded; collectThrash is reached only with a Grammar wired, which the harness never does. Findings 3 to 6: 1.13.17 dropped its installed-host half without saying so; the 1.17.4 cell omitted its test rename; the fuzz count was 25, not 27; the candidate 4 file count included test files. Quiet C5.1 landed after the first pass and is now folded in.

### Summary

No product code changed and no bundled bytes were touched. Every change is under plans/, on closeout/w17-inventory (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-inventory), and I pushed nothing. `git diff --stat 387867f3 HEAD -- . ':(exclude)plans'` is empty.

## What changed in this fix round

**1. Quiet C5.1 results are now in the inventory.** The run finished at 05:32Z with `quiet exit=1`; the failure is the Linux B-A/B-B rows (D53(b)). The `P-C51` pending code is replaced by a new evidence code, `C51` (plans/sdd/V6-closeout/phase3/c6/quiet/):

| | Windows (c51-win.log, c51-win-bf.log) | Linux (c51-linux/c51-linux.log, bf/.../test.jsonl) |
|---|---|---|
| B-A p99 | 30.72 ms vs 50, PASS | 65.54 ms vs 15, FAIL |
| B-B p99 | 24.58 ms vs 50, PASS | 61.44 ms vs 15, FAIL |
| B-E p99 | 170.75 ms vs 2000, PASS | 384.11 ms, PASS |
| B-F p99 | 73.73 ms vs 250, PASS | 20.48 ms, PASS |

- On Linux, 3591 of 5130 hot-path requests were deferred to the client spool and 0 were lost. The B-A/B-B fails are fsync-bound and not verified in target (D53(b)).
- quiet.sh warned that the container was not idle *after* its run (1-minute load 2.97).
- The real home was unchanged (homeguard-check.txt).

Rows changed by this evidence:
- **1.10.16** is now verified_in_target.
- **1.17.5** stays unknown, with the B-D diagnostic added: p99 72.23 ms on Windows, 24.06 ms on Linux.

**2. The review findings are resolved** (next section).

**3. Regenerated outputs.**
- dispose.py and gen_c6map.py were updated and re-run. They wrote inventory-current.tsv and inventory-c6-map.md.
- I checked by script that the seven original TSV columns are byte-identical to 9a56b305 and the file is LF.
- Exactly nine rows changed: 1.5.12, 1.8.2, 1.8.13, 1.10.16, 1.12.14, 1.13.17, 1.17.4, 1.17.5, 1.17.6.
- The counts in inventory-current-notes.md and the note in current-candidate-gate-map.md are updated.

**New counts:**

| c6 result | rows |
|---|---|
| verified_in_target | 257 |
| partial_verified | 26 |
| failed | 8 |
| unknown | 7 |
| unsupported | 3 |
| documented | 3 |
| implemented_unverified | 0 |

## Review resolution

**F1 (major): 1.5.12 should be failed. Accepted, and applied to two more rows for consistency.**
- I confirmed the red myself. In p3-win-x11-alone.log, X11's *no-ledger* bench-hotpath run gives B-A p99 98.30 ms and B-B 81.92 ms against 50 ms. That run had 593 deliveries deferred, a spawn floor p50 of 29.96 ms, and exited non-zero.
- The map note for 1.5.12 names X11 (D28).
- 1.5.12 is now `failed` (RED-X11). The cell records that the quiet Windows run (C51) and WTIME pass, and that Linux is D53(b).
- The same rule covers every row that asserts the hot-path B-A budget, so **1.12.14** and **1.17.6** are also `failed` on RED-X11.
- Each cell names the state it returns to once x11win records a root cause and fix, or a disposition: 1.5.12 and 1.12.14 go back to partial_verified (the Linux half stays not verified), and 1.17.6 goes back to implemented_unverified (its launcher split still cannot run, D37(c)).
- Extending the rule to those two rows was my call, not the reviewer's.

**F2 (minor): the observer wording was wrong, and the carry needs package-level diffs. Accepted.**
- The fact is even narrower than the reviewer wrote. tooluse.go:282 calls collectThrash only inside `if o.opt.Grammar != nil`. benchHarness (tooluse_test.go:469-480) wires no Grammar. So neither the call nor the new ThrashFloor branch runs; sessionState gained two fields.
- 1.8.2 is BenchmarkTombstone, a pure function in the unchanged tombstone.go, so its note was corrected separately.
- The wording is fixed in dispose.py (1.8.2, 1.8.13), the map's C5.2 section, and V2-SP-08 (SP08-D1).
- V2-SP-10 (SP10-D1) said the only checkpoint change was the PricedDrops hook. It also adds three constants; none is on Finalize's path. Fixed.
- New proof file: runs/c52-package-diffs-c5-to-c6.txt. It has a `git diff --shortstat` per benchmarked package, and for each changed package, where the measured path meets the change. The package diffs are non-empty for obs (+93), cli (+82/-3), observer (+191/-9), checkpoint (+32) and daemon (+1320/-80).
- I could not re-measure tonight: four other workstreams share the machine, so no run would be quiet, and a C5.2 run is ten ABBA rounds.
- So I took the other route the reviewer offered: interpretation 2 now goes to the coordinator under needs_owner. The map says plainly that seven rows rest on it: 1.1.16, 1.1.27, 1.8.2, 1.8.13, 1.10.17, 1.12.17, 1.16.11.

**F3 (minor): 1.13.17 narrowed its criterion. Accepted.** It is now partial_verified, pending P-LIVE (C4.4).

**F4 (minor): 1.17.4 names a test that no longer exists. Accepted.** The cell now says the row is judged on TestPlatform_HookLauncherForms. That test replaced TestPlatform_WindowsHookLauncherForms and now runs on every OS (packaging/report.md item 4). The map's corrections section lists the rename.
- Partial rebuttal: the result was already right. inventory-map.tsv records the replacement in its symbols_missing column, and D37(b) judges such rows on the replacement. Only the explanation was missing.

**F5 (minor): the fuzz count was wrong. Accepted.** `gh run view 36955043924 --json jobs` shows 31 jobs, all success: 25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded. The FUZZ code now says 25. race-windows 110676054888 is cited under WRACE.

**F6 (minor): the candidate 4 carry overstated the change. Accepted.** It now says 204 files under internal/, cmd/ and plugin/, of which 98 are non-test product files (+5720/-654), with both commands.

## Tests and checks

All run after "quiet exit=" and "done" were in chain.log. Logs are under plans/sdd/V6-closeout/w17-inventory/runs/ (fix-*.log).
- `go run ./tools/devtool fmt-check`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every check PASS, exit 0.
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_ManifestIsWellFormed$' ./test/guards/`: PASS.
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_OpenRowsHaveLivingEvidence$' ./test/guards/`: PASS.
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_WaveReportRequiresResolution$' ./test/guards/`: PASS.
- Only test/guards/carrieddefects_test.go reads the edited documents.
- No Go code, docs/ or generated inputs changed, so I did not run vet, test/docs or the gen-*-docs checks.

## C6.3

No CARRIED-DEFECTS.tsv status changed, so no test/guards expectation data changed. The edits are wording only:
- SP08-D1 and SP10-D1: the exact carry evidence.
- SP05-D2 and SP20-D1 (V2-WAVE1): they now quote candidate 6's quiet C5.1 numbers instead of "(in progress)".

## Criterion changes (carried forward; none weakened)

1. Rows naming the whole test/fault package or the hook-exit matrix (1.5.19, 1.17.11, 1.17.14) are failed on the hosted Windows TestFault_Lifecycle red.
2. Interpretation 2: a C5.2 number from candidate 5 carries when the files the benchmark executes are byte-unchanged and the other package changes are additive or off its path. This is now escalated to needs_owner.
3. Section 3.14 includes X11.
4. New this round: a row asserting the Windows hot-path B-A budget is failed when any candidate 6 isolated artifact of that budget is red, even if the quiet run passes. That covers 1.5.12, 1.12.14 and 1.17.6.

## For the coordinator (not cited in committed docs, because the SHAs are on unmerged branches)

closeout/w17-ci has a root cause for RED-FAULT: test/fault's audit read the sidecars before the retention claims. The fix is dd8e9fd2 "fix(fault): read retention claims before the sidecars they name", test-only. closeout/w17-release has "chore(release): set the version to 0.3.0", which is a Go product change, so it implies a new candidate. Neither is on candidate 6, so 1.5.19, 1.17.11, 1.17.14 and 1.17.19 stay failed for candidate 6. x11win has no commits yet.

### Commits

- b5ba6a4cf491119cd2a8d47c39415d8af2e14c5d chore(v6): add the c5.2 package-level diff proof
- 8b368d091f742207805b0aa7066b1b5a3dea0e7e docs(v6): re-dispose the hot-path rows on c6's quiet run
- 77695349288afba2ac627b33d6b34d78fdff76d8 docs(plans): state the c6 carried-defect evidence exactly
- 86a95960adab14d9de898dde998ead4f6754a638 chore(v6): record the inventory fix-round checks on windows

### Tests

- `go run ./tools/devtool fmt-check` — exit 0 (runs/fix-fmt-check-windows.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — every check PASS, exit 0 (runs/fix-lint-subset-windows.log)
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_ManifestIsWellFormed$' ./test/guards/` — PASS (runs/fix-guards-carrieddefects-windows.log)
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_OpenRowsHaveLivingEvidence$' ./test/guards/` — PASS
- `go test -p 2 -count=1 -v -run '^TestCarriedDefects_WaveReportRequiresResolution$' ./test/guards/` — PASS
- `python plans/sdd/V6-closeout/w17-inventory/dispose.py --write; python plans/sdd/V6-closeout/w17-inventory/gen_c6map.py` — 304 rows; 257 verified, 26 partial, 8 failed, 7 unknown, 3 unsupported, 3 documented; 7 original columns byte-identical to 9a56b305 (script check), LF; 9 rows changed
- `gh run view 36955043924 --json jobs` — 31 jobs, all success: 25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded

### Criterion changes

- None weakened. Interpretation 1 is carried forward: 1.5.19, 1.17.11 and 1.17.14 name the whole test/fault package or the hook-exit matrix, so they are failed on the hosted Windows TestFault_Lifecycle red. The reported failure is a dangling retention root, not a hook exit.
- Interpretation 2 is carried forward and now escalated to needs_owner. A candidate 5 C5.2 figure carries when the benchmark's executed files are byte-unchanged and the other package changes are additive or off its path. The package-level diffs are disclosed in runs/c52-package-diffs-c5-to-c6.txt.
- Interpretation 3 is carried forward: section 3.14 includes X11 (TestV3_HotPathUnchangedWithLedgerResident).
- Interpretation 4, new this round and stricter, not weaker: a row asserting the hot-path B-A budget is failed when a candidate 6 isolated artifact of that budget is red (RED-X11), even though the quiet C5.1 Windows run passes. Applied to 1.5.12 (the reviewer's finding), and by the same rule to 1.12.14 and 1.17.6. Rationale: the map's rule says a red candidate 6 artifact for the row's assertion means failed, and section 3.14 was already failed on the same red.
- Restored, not narrowed: 1.13.17 keeps its installed-host half and is partial_verified pending C4.4, as the D37 map note says.

### Open issues

- RED-X11: X11 is red on Windows in isolation on candidate 6. The no-ledger run gives B-A 98.30 / B-B 81.92 ms and the ledger run 81.9 / 57.3 ms, against 50 ms, with 593 deferred and a spawn floor p50 of 30 ms; it is also red in hosted release-dry-run. The quiet C5.1 run on the same candidate passes on Windows (B-A 30.72 ms, B-B 24.58 ms). Rows 1.5.12, 1.12.14, 1.17.6 and section 3.14 stay failed until w17 x11win records a root cause and fix, or a disposition. When it does, change the three overrides in dispose.py to the 'clears to' states their cells name, then re-run dispose.py and gen_c6map.py.
- RED-FAULT: rows 1.5.19, 1.17.11, 1.17.14 and 1.17.19 stay failed on candidate 6. closeout/w17-ci has a test-only fix (dd8e9fd2) that is not on the candidate, so these rows need re-dispositioning on whichever candidate includes it.
- RED-RELDRY: release-dry-run (job 110676058084) stopped at ci-local cover, so 1.17.18 and 1.17.19 are failed. A green release-check on the candidate is still owed.
- Linux B-A/B-B are not verified in target (D53(b)) in LTIME and C51. This is a standing rule, not an open defect.
- P-LIVE: the candidate 6 live lane closes 1.6.4, 1.8.6, 1.10.14, 1.11.11, 1.11.12, 1.13.4, 1.13.5, 1.13.14, 1.13.17 (C4.4, newly pending this round), 1.17.7, 1.17.8, 1.17.9, 1.17.12, 1.17.15 and sections 3.1, 3.3, 3.4, 3.6, 3.9, 3.10, 3.11, 3.12. C4.2 and UAT-02 are carried from candidate 4 (rows 1.5.15, 1.8.6, 1.17.16, 1.18.12).
- P-C55: 1.14.6 needs the C5.5 confirmatory evaluation (D53(g)).
- 1.17.17 closes at C7.1/C7.4. At 99d0b18, core.Version and plugin.json still read 0.1.0. closeout/w17-release has a version commit, which is a product change and implies a new candidate.
- 1.17.9: TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting and TestInstall_HostCLIInstallUpgradeUninstall skip without the claude CLI. Whether they ran on Windows on candidate 6 is unproven, because the chain log is non-verbose.
- 1.17.19 also needs branch protection on develop and main (C7.3).
- The candidate 6 evidence directory (qompack-v6 plans/sdd/V6-closeout/phase3/c6/, including linux/ and quiet/) is still uncommitted. The TSV and the map cite those paths, so the coordinator must commit it for the citations to resolve.
- If a later candidate is frozen (the w17-ci/w17-release product changes imply one), the c6 columns describe candidate 6 only. A c7 pass needs new unchanged-proofs against the new head.

### Needs the owner

- Interpretation 2 needs a coordinator D-ruling. Question: may a C5.2 measurement from candidate 5 (0d06ab12) count as verified_in_target on candidate 6 when the files the benchmark executes are byte-unchanged, but other non-test files in the same package changed (additively or off the measured path)? The whole-package diffs are non-empty for obs (+93), cli (+82/-3), observer (+191/-9), checkpoint (+32) and daemon (+1320/-80); see runs/c52-package-diffs-c5-to-c6.txt. Seven rows rest on this reading: 1.1.16, 1.1.27, 1.8.2, 1.8.13, 1.10.17, 1.12.17, 1.16.11. The SP08-D1 and SP10-D1 confirmations also rest on it, though their wontfix/fixed status does not. If the whole-package reading is ruled instead: set those seven rows to partial_verified in dispose.py, pending a quiet C5.2 re-run on the release candidate, then regenerate. No new numeric bound is introduced either way.
- Ratify interpretation 4: a row that asserts the Windows hot-path B-A budget is failed when any candidate 6 isolated artifact of that budget (X11's bench-hotpath runs) is red, even though the designated quiet C5.1 run passes. It applies to 1.5.12, 1.12.14 and 1.17.6, consistent with section 3.14. If the coordinator rules that the designated quiet run governs instead, those rows become partial_verified (1.5.12, 1.12.14; Linux half per D53(b)) and implemented_unverified (1.17.6).

## Independent verification of the fix seat: needs-fixes

- **minor** `plans/sdd/V6-closeout/w17-inventory/dispose.py:92 (1.5.12 override); plans/sdd/V6-remediation/inventory-current.tsv row 1.5.12 (line 89); plans/sdd/V6-closeout/inventory-c6-map.md:133` — This error is new in this round. The new 1.5.12 cell says "X11's bench-hotpath runs (without and with the ledger) breach B-A/B-B on Windows". No with-ledger run exists on candidate 6. Both artifacts are the no-ledger twin from two separate executions of X11: the isolated e2e timing pass and X11 run alone. In each, the test fails on the twin before it reaches the ledger phase. The fix seat's open_issues makes the same mistake: it labels 81.9/57.3 ms as "the ledger run", but those numbers are the no-ledger run in p3-win-e2e-timing.log.
  - Evidence: p3-win-e2e-timing.log:3,18-19,141: "no-ledger run (the X11 corpus without the ledger)" B-A p99 81.920 ms, B-B 57.344 ms [FAIL], then "no-ledger run ... bench-hotpath exited non-zero". p3-win-x11-alone.log:3,18-19,141: the same no-ledger label, B-A 98.304 ms, B-B 81.920 ms [FAIL]. Neither log contains any other "... run" line from v3_x11_test.go. At 99d0b18, test/e2e/v3_x11_test.go:412-425 runs the no-ledger twin first, and its require failure ends the test.
  - Fix: In the dispose.py 1.5.12 note, replace "X11's bench-hotpath runs (without and with the ledger)" with "X11's no-ledger bench-hotpath run, in both isolated Windows executions (p3-win-e2e-timing.log B-A/B-B p99 81.9/57.3 ms, p3-win-x11-alone.log 98.3/81.9 ms; the ledger phase is not reached)". Then re-run dispose.py --write and gen_c6map.py. Correct the hand-off text for x11win the same way.
- **minor** `plans/sdd/V6-remediation/inventory-current.tsv rows 1.1.16, 1.1.27, 1.10.17, 1.12.17, 1.16.11 (dispose.py default C52 note and the 1.1.27/1.12.17/1.16.11 overrides)` — The F2 resolution is inconsistent between rows. The map's C5.2 section now says seven rows depend on interpretation 2, and that interpretation is still waiting for a coordinator ruling. Only two of those rows (1.8.2, 1.8.13) say so in their own cells. The other five are still published as verified_in_target, and their cells say only "covered code unchanged c5->c6". Their packages, however, have non-empty package-level diffs: obs +93, cli +82/-3, checkpoint +32, daemon +1320/-80. Anyone who reads only the TSV sees those five as unqualified passes, even though the ruling could move them to partial_verified.
  - Evidence: 1.1.16: "WIN+LNX+C52; C52 measured and reported (diagnostic); covered code unchanged c5->c6". 1.10.17 has the same text. 1.12.17 and 1.16.11 say "(covered code unchanged c5->c6)". 1.1.27 has no caveat at all. Compare OBS_C52 in dispose.py, which 1.8.13 uses: "the carry rests on interpretation 2 ... which awaits a coordinator ruling". runs/c52-package-diffs-c5-to-c6.txt shows the non-empty diffs for these packages.
  - Fix: Add the same interpretation-2 caveat to the five cells (pointing to runs/c52-package-diffs-c5-to-c6.txt and the pending ruling). The simplest way is to change the default C52 note in dispose.py for rows whose package diff is non-empty, and to extend the 1.1.27, 1.12.17 and 1.16.11 override notes. Then regenerate. Alternatively, hold all seven rows at partial_verified until the coordinator rules.


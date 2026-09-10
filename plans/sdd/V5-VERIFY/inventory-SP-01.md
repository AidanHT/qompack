# V5-VERIFY inventory — SP-01 (foundation, config, contracts)

**SP:** SP-01 — rows `I-01.1` … `I-01.24` (24 rows, taken from the V5 inventory at plan HEAD `7f92af5`).
**Reconciliation target:** SP-19 M0-G1/G2/G4; retained package/CLI/import guards, versioned settings and
supported capability states.

**Owner plan sections consulted**

- `plans/V4-SP-19-migration-reconciliation.md` — §gates table lines 114–117 (`M0-G1` mapping, `M0-G2`
  adapter, `M0-G4` packaging), step 6 `fix(config): version migration settings safely` (line 144),
  line 175 (`M0-G1–G6` dispositions), line 177 (unsupported controls remain disabled).
- `plans/sdd/V4-VERIFY/reconciliation-map.md` — rows `V4-SP01-01` … `V4-SP01-14` (lines 275–288),
  NC-1/NC-1b (lines 585, 664), serial-gate table S2/S4/S5/S7/S16. Mapping reused where still valid;
  **every result below was re-run on this tree**, nothing was carried over as a pass.
- `plans/CARRIED-DEFECTS.tsv` — no unresolved row names an SP-01 package as its evidence owner; `SP02-D6`
  (`deferred:V5-VERIFY`) is scheduler/eval, not SP-01, and is not scored here.

**Tree:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @ `87c0c1d`
(`chore(sp21): integrate deterministic admission control`).
**Platform:** Windows 11, `go1.26.6 windows/amd64`, Intel Core Ultra 7 155H (22 logical CPUs).
**Date:** 2026-09-08. **Machine was shared with other verification agents throughout.**

## Counts

| Disposition | Rows | | Result | Rows |
|---|---:|---|---|---:|
| `MAPPED` | 15 | | `PASS` | 19 |
| `MAPPED-CMD` | 5 | | `FAIL` | 1 |
| `SUPERSEDED-BY-WAVE4` | 2 | | `FAIL-BASELINE` | 0 |
| `RETIRED` | 1 | | `FAIL-COLOAD-SUSPECT` | 1 |
| `MISSING` | **0** | | `SKIP` | 0 |
| `NEEDS-COORDINATOR` | 1 | | `NOT-RUN` | 3 |
| **Total** | **24** | | **Total** | **24** |

Artifact root: `C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-01/`
(abbreviated `…/inv/SP-01/` in the table). Every `-run` pattern was confirmed with `go test -list`
before it was run; the `list-*.txt` files hold those enumerations.

## Row table

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-01.1 | Domain-separated hashing, `Hash` text/JSON, `DecisionID`, seven sentinels | MAPPED | `internal/core`: `TestHashBytes_DomainSeparation`, `TestHashBytes_KnownVector`, `TestHash_StringShortParse_RoundTrip`, `TestParseHash_Rejects`, `TestHash_JSONRoundTrip`, `TestNewDecisionID_Format`, `TestSentinels_AreDistinct` | PASS | `…/inv/SP-01/I-01.1.txt` | direct | All 7 named tests exist and ran; 12 `--- PASS` lines incl. subtests, `ok … 0.996s`. |
| I-01.2 | `.qompack/` ladder, `Norm`/`Key`, long paths, layout, self-ignore | MAPPED | `internal/paths`: 6× `TestResolve_*`, 13× `TestNorm_*`, `TestKeyFold`, `TestEnsureLayout_CreatesAllDirsAndSelfIgnore`, `TestLongPath_Over260` | PASS | `…/inv/SP-01/I-01.2.txt` | direct | `layout_test.go:33-47` read directly: the dir list is `require.Len(t, dirs, 17)` and `.qompack/.gitignore` is asserted `== "*\n"`. Both sub-claims of the expected text are live. |
| I-01.3 | Append-only invariant (§7.4, §13 inv. 2) | MAPPED | `internal/paths`: `TestAppendOnlyGuard`, `TestWriteAtomic_RefusesProtected`, `TestCreateNew_SetsReadOnly`, `TestReplaceBloom_KeepsOneBackup`, `TestAppendJSONL_OneLinePerRecord` | PASS | `…/inv/SP-01/I-01.3.txt` | direct | 10 PASS lines incl. `TestAppendOnlyGuard`'s five illegal-write subtests; `TestCreateNew_SetsReadOnly` covers mode `0444`. Reuses `V4-SP01-09`'s mapping, re-run here. |
| I-01.4 | Atomic writes + MANIFEST append/read | MAPPED | `internal/paths`: `TestWriteAtomic_ReplacesAndSyncs`, `TestManifest_AppendAndRead` | PASS | `…/inv/SP-01/I-01.4.txt` | direct | — |
| I-01.5 | Appendix C reproduced verbatim by `config.Defaults()` | MAPPED | `internal/config`: `TestDefaults_MatchesAppendixCVerbatim`, `TestDefaults_RuntimeNamespace`, `TestJSONSchema_Golden` | PASS | `…/inv/SP-01/I-01.5.txt` | direct | **Key reconciliation finding.** SP-19's versioned settings landed entirely inside the `runtime` namespace (`internal/config/runtime.go:118` `runtime.migration.settingsVersion`, `:180` `runtime.phase7.settingsVersion`), which this row's assertion explicitly subtracts. Appendix C is therefore *not* superseded by wave 4 — `V4-VERIFY` NC-1b's ruling still holds on this tree, now with an executed pass behind it. |
| I-01.6 | Five-layer precedence, per-leaf merge, env mapping, JSONC, `null` semantics | SUPERSEDED-BY-WAVE4 | `internal/config`: 31 `TestLoad_*`, incl. the four SP-19 additions `TestLoad_NewerSettingsVersionResetsTheWholeBlock`, `TestLoad_OlderSettingsVersionFallsBackToCurrent`, `TestLoad_PendingSwitchFallsBackWithWarning`, `TestLoad_RetiredMeaningKeysWarnButStillApply` | PASS | `…/inv/SP-01/I-01.6.txt` | direct | SP-19 step 6 (plan line 144) grew the requirement: a whole `settingsVersion` block written for a newer build is now reset wholesale *before* per-leaf fallback (`load.go:67`, `migration.go:189-227`). Both original sub-claims re-verified by reading the bodies: `load_test.go:97-98` pins `OriginEnv` for `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER`; `load_test.go:167` pins `require.Nil(…MeasuredDeltaSeconds)` for explicit `null`. See old-to-new map. |
| I-01.7 | Validation is fallback-not-crash; complete rule table; tiers partition; telemetry off | SUPERSEDED-BY-WAVE4 | `internal/config`: `TestValidate_EveryRule`, `TestValidate_DefaultsAreValid`, `TestValidate_RuleTableIsComplete`, `TestValidate_TiersPartition`, `TestValidate_TelemetryMustBeFalse`, **`TestValidate_RefusesEveryPendingSwitch`**; `internal/cli`: `TestCLI_ConfigViolationsAreLoudAndPersisted` | PASS | `…/inv/SP-01/I-01.7a.txt`, `…/inv/SP-01/I-01.7b.txt` | direct | `TestValidate_RefusesEveryPendingSwitch` is the "supported capability states" half of the reconciliation target and is selected by the row's own `TestValidate_` pattern — no pattern correction needed. Backs SP-19 plan line 177 ("unsupported controls remain disabled"). Both commands ran; both `ok`. |
| I-01.8 | Config fuzz (`FuzzConfigLoad`, 60s) | MAPPED-CMD | `internal/config`: `FuzzConfigLoad` exists (`go test -list` confirms) | NOT-RUN | `…/inv/SP-01/list-01.8.txt` | direct | **Fuzz runs are outside this seat's mandate.** Deferred with the exact command. |
| I-01.9 | Leveled logging + `Loud` three-destination channel + rotation | MAPPED | `internal/logging`: `TestLoud_ThreeDestinations`, `TestLogger_LevelFiltering`, `_Rotation`, `_With_AccumulatesFields`, `_ValueQuoting`, `_New_RejectsEmptyDir`, `_DayLogFileName`, `_ConcurrentWritesDoNotRace`, `_LoudBypassesMinLevel` | PASS | `…/inv/SP-01/I-01.9.txt` | direct | — |
| I-01.10 | Log-bucket histograms, conservative percentiles, exact max, budgets B-A..B-G | MAPPED | `internal/obs`: 7× `TestHistogram_*`, `TestBudgets_AllSixPresentAndConfigDriven`, `TestBudgets_BGCoversTheDegradedSpoolAppend`, `TestCheckBudgets_NeverReportsBG`, `TestCheckBudgets_NeverReportsUngatedBudgets`, `TestCheckBudgets_CountsConsecutiveWindows`, `TestCheckBudgets_ResetsStreakWhenBackUnderBudget` | PASS | `…/inv/SP-01/I-01.10.txt` | direct | The "six followed by B-G" shape survives verbatim: the table test is still named `AllSix` and B-G has its own test, exactly as the expected text describes. `TestCheckBudgets_NeverReportsBG` — the structural proof the row names — is present and green. B-F's presence (`V4-SP01-06`) is covered by `TestCheckBudgets_NeverReportsUngatedBudgets`. |
| I-01.11 | Token estimation: classification, prose/code, image dims, PDF pages, calibration clamp | MAPPED | `internal/tokens`: 41 selected tests incl. `TestEstimate_ImageCappedAt1600`, `TestCalibrate_ClampAndThreshold`, `TestCalibrate_LowClamp`, `TestCalibrate_ClampsAndPersists`, `TestEstimateRoot_SP01BaselineStillPasses` | PASS | `…/inv/SP-01/I-01.11.txt` | direct | No `--- SKIP` in the output: `tokenstest`'s `skipIfStub` did **not** fire (relevant to I-01.18). Image cap 1600 and the `[0.6,1.6]` clamp both have named tests. |
| I-01.12 | Hook wire format: seven payloads, unknown-field preservation, never panics, limits | MAPPED | `internal/hookio`: `TestReadEvent_AllSevenHookPayloads`, `_UnknownFieldsPreserved`, `_MissingFieldsNeverPanic`, `_NullFields`, `_LimitExceeded`, `_LimitExactFitSucceeds`, `_MalformedJSONReturnsRawAndError`, `_TopLevelNullNeverPanics`, `_ExtraIsValidJSONPerKey`, `TestWriteOutput_EmptyIsMinimal`, `_NoHTMLEscaping`, `_FullShape`, `TestSessionStartOutput_Shape` | PASS | `…/inv/SP-01/I-01.12.txt` | direct | `_EmptyIsMinimal` covers the `{}\n` claim; `_ExtraIsValidJSONPerKey` covers `Extra` survival. |
| I-01.13 | Hook payload fuzz (`FuzzReadEvent`, 60s) | MAPPED-CMD | `internal/hookio`: `FuzzReadEvent` exists | NOT-RUN | `…/inv/SP-01/list-01.8.txt` | direct | Fuzz runs outside mandate. Deferred. |
| I-01.14 | Hooks always exit 0 (§13 inv. 6) — 30 fault-injection combinations | MAPPED | `internal/cli`: `TestDispatch_HookAlwaysExitsZero`, `TestDispatch_PanicRecovered`, `TestDispatch_NonHookErrorExitsOne`, `TestDispatch_UnknownCommandExitsTwo` | PASS | `…/inv/SP-01/I-01.14.txt` | direct | **Exactly 30** `TestDispatch_HookAlwaysExitsZero/*` subtests PASS — the fault-injection count in the expected text still holds after wave 4 touched `internal/cli` (`V4-SP01-03`). Exit-1 and exit-2 arms green. |
| I-01.15 | Plugin manifest from one typed source; six hooks; seven commands; `.mcp.json` | MAPPED-CMD | `go test ./internal/pluginmanifest/ -v` (ok) + `go run ./tools/devtool plugin-validate` (exit 0) + `git diff --exit-code -- plugin/` (exit 0) | PASS | `…/inv/SP-01/I-01.15a.txt`, `…b.txt`, `…c.txt` | direct | All three sub-commands ran. `plugin-validate: OK (10 file(s), 7 command(s), 7 hook event(s))`; diff clean. `plugin/.mcp.json` present. Timeout tuple read from `plugin/hooks/hooks.json` in §2.4 order — UserPromptSubmit 5, PostToolUse 5, SessionStart 15, PreCompact 20, Stop 5, SubagentStop 10, SessionEnd 20 = **`5,5,15,20,5,10,20`, exact match**; `PostToolUse` carries `"matcher": "*"`. The row's prose "six hooks" counts the six *entry points*; the manifest carries seven *events* because `SubagentStop` re-uses `observe stop --subagent`. Not a discrepancy. |
| I-01.16 | Config docs never drift | MAPPED-CMD | `go run ./tools/devtool gen-config-docs --check` | PASS | `…/inv/SP-01/I-01.16.txt` | direct | Exit 0, `docs/config-reference.md is up to date`. Clears `V4-SP01-07`, which flagged this gate as meaningless until the doc was regenerated for the A0/C config keys — it has been. |
| I-01.17 | Every §5 interface has a live implementation; no stub residue | NEEDS-COORDINATOR | `test/guards`: `TestAllStubsReturnNotImplemented` (probe table, all subtests PASS incl. `contract`, `state`, `admission`) + `Select-String … ErrNotImplemented` | PASS | `…/inv/SP-01/I-01.17.txt`, `…/inv/SP-01/I-01.17-ps.txt`, `…/inv/SP-01/I-01.17-grep.txt` | direct | **Test half is clean.** The `internal/cli` `notImplemented` table (`commands.go:46-50`) carries **exactly three** rows — `fsck (SP-17)`, `doctor (SP-17)`, `bench (SP-05)` — and `mcp`, `status`, `recall`, `pin`, `why`, `dropped`, `eval` are all gone, as the row requires: `mcp` is a real `Cmd`, `eval` comes from `evalCmds()`, and the six slash commands from `slashCommandCmds()`. The standing-survivor `bench` reason the row asks V5 to carry forward is recorded in §5. **Grep half needs a ruling:** three *producers* of `core.ErrNotImplemented` exist outside classes (a)–(c). See Q1. |
| I-01.18 | Every conformance suite is live (Rule W-1) — no `t.Skip(` call under `internal/**/*test*/` | RETIRED | Literal command re-run; replacement is `go run ./tools/devtool lint --only=stubskips` (`tools/devtool/lint.go:33` → `tools/devtool/stubskips.go`) | **FAIL** | `…/inv/SP-01/I-01.18.txt` | direct | **30 matching calls, not zero.** The row's premise — that `internal/tokens/tokenstest/estimator_suite.go` is "the one hit this row exists to catch" — is false on this tree. 29 of the 30 are `t.Skip(ruleW1SkipMsg)` inside the identical `skipIfStub` idiom in `analyzertest, canontest, checkpointtest, chunktest, contracttest, dagtest, evaltest, grammartest, ipctest, mcptest, negknowtest, observertest, pinstest, redacttest, rehydratetest, rulestest, skillstest, storetest, symbolstest`; the 30th is tokenstest's `t.Skip(stubSkipMsg)`. Read `internal/dag/dagtest/suite.go:110-126` against `internal/tokens/tokenstest/estimator_suite.go:83-97`: structurally the same guard. The grep cannot distinguish an *unreachable* skip from a firing one, so it is the wrong instrument. See old-to-new map. |
| I-01.19 | Cross-wave contract fixtures reproduced by real implementations (Rule W-2) | MAPPED-CMD | 73 `Test*Golden*`/`Test*Contract*` definitions across 26 packages; `testdata/golden/contracts/` holds 11 subtrees (`checkpoint config contract dag hookio ipc negknow paths pins sketch store`) | NOT-RUN | `…/inv/SP-01/I-01.19-enum.txt` | direct | The row's command is `go test ./... -run 'Golden|Contract'` — a whole-tree run, outside this seat's mandate. Enumeration establishes the mapping is live; **no result is claimed.** Deferred with the exact command. |
| I-01.20 | Ship-order and safety guards — nine named + sibling guards | MAPPED | `test/guards`: all 18 `TestGuard_*` PASS | PASS | `…/inv/SP-01/I-01.20.txt`, `…/inv/SP-01/list-01.17-20.txt` | direct | **The hand-check the row demands, answered:** `TestGuard_SubmodularEnabledOnlyAfterPSelection` is **absent**; `TestGuard_SubmodularDefaultsOff` is **present and green**. One of the two names is in the output, so by the row's own rule this is **not** a V5 failure — SP-15 neither re-pointed nor deleted the guard; it left the shipped name in place. Recorded as a corrected pattern, not a defect. The other eight named guards all PASS (`Phase0BeforeStore`, `StoreAndNegknowBeforeCheckpoint`, `SubmodularInertWithoutPSelection`, `SelectorRefusesWithoutPSelection`, `O1FlagDefaults`, `FreshBuildReportsModeFull`, `WriteSetConfinedToQompack`, `NoNetworkImports`), as do all four sibling guards and all four `_Detects…`/`_Sees…` scanner self-tests (`NoNetworkImports_DetectsAViolation`, `SharedReaderScannerSeesAForbiddenCall`, `SketchLoadScannerSeesACall`, `WriteSetDetectsAStrayWrite`). |
| I-01.21 | Toolchain: `nomagic`, import-graph DAG, test-dep isolation, commit-msg checker | MAPPED | `tools/lint/nomagic`: `TestNoMagic_Analyzer`; `tools/devtool`: `TestImportGraph_AcceptsRealRepo`, `TestImportGraph_RejectsViolation`, `TestTestDeps_RejectsProductionTestify`, `TestCheckCommitMsg` | PASS | `…/inv/SP-01/I-01.21a.txt`, `…/inv/SP-01/I-01.21b.txt` | direct | All five tests the expected text names ran and passed. `TestImportGraph_AcceptsRealRepo` passing on this tree is the live check that SP-19/20/21's new packages did not break the import DAG (`V4-SP01-01`'s five-package set). The row's literal command is an *unfiltered* `-v` run of both packages; I ran the five named tests under `-run` filters instead, because the unfiltered `tools/devtool` package shells out to tree-wide `go test` from `stubskips_test`/`cover_test`. The unfiltered form is deferred. |
| I-01.22 | Test fixtures: temp project, FakeClock, Windows-hostile files, golden helper | MAPPED | `internal/testutil` (whole package): incl. `TestWindowsHostileFiles_AllCreatable`, `TestWindowsHostileFiles_ShapeIsStable`, `TestProject_AssertAppendOnly` | PASS | `…/inv/SP-01/I-01.22.txt` | direct | Full package `-v` run, no FAIL and no SKIP. |
| I-01.23 | e2e: all six hooks against the real binary | MAPPED | `test/e2e`: `TestE2E_AllSixHooksExitZero`, `TestE2E_ConfigPrintFromRealBinary` | PASS | `…/inv/SP-01/I-01.23.txt` | direct | Filtered run only (an unfiltered `./test/e2e` is outside mandate and carries the known baseline failure `TestV3_HotPathUnchangedWithLedgerResident`, which this filter excludes). Six hook subtests exit 0 against the built binary. Reuses `V4-SP01-11`'s mapping, re-run here. |
| I-01.24 | SP-01 benchmark budgets | MAPPED | `internal/obs`: `BenchmarkHistogram_Observe`; `internal/config`: `BenchmarkConfigLoad_ColdNoFiles`; `internal/paths`: `BenchmarkPathsWriteAtomic_4KB`; `internal/cli`: `BenchmarkHookNoop_InProcess` | **FAIL-COLOAD-SUSPECT** | `…/inv/SP-01/I-01.24.txt` | direct | Ran once, `-benchtime 2s`. **2 of 4 budgets met, 2 breached.** `Observe` 9.42 ns/op vs <100 ns ✅. `ConfigLoad_ColdNoFiles` 533 µs/op vs <2 ms ✅. `PathsWriteAtomic_4KB` **8.57 ms/op vs <2 ms ❌ (4.3×)**. `HookNoop_InProcess` **16.06 ms/op vs <3 ms ❌ (5.3×)**. Both breaches are fsync/disk-bound and both were measured while other verification agents were running `go test` on the same host; per the co-load policy (ADR 0010) they are classified suspect, not failures, and are deferred to the quiet pass. Note the two green budgets are the two CPU-bound ones — the pattern is consistent with I/O contention rather than a code regression. |

## Old-to-new assertion map

| Row | What changed | Reason | Replacement |
|---|---|---|---|
| **I-01.6** | `SUPERSEDED-BY-WAVE4` | SP-19 (`plans/V4-SP-19-migration-reconciliation.md` step 6, line 144: *"fix(config): version migration settings safely — reader/default/deprecation/guard compatibility and independent switches; no old fixture reset"*) added block-level `settingsVersion` semantics that sit *above* the five-layer per-leaf merge the row describes: a document declaring a newer `settingsVersion` has its whole block reset to defaults **before** per-leaf fallback can keep half of it (`internal/config/load.go:67`, `migration.go:178-227`). It also attached a v1.5 deprecation diagnostic to `scheduler.youngDaly.measuredDeltaSeconds`, so the null-semantics test now tolerates exactly one warning (`load_test.go:162-166`, `load_kinds_test.go:36-40` — both carry the `nonDeprecation(warns)` helper the change introduced). | Same command, same `TestLoad_` pattern (it already selects the new tests). The requirement now additionally reads: `TestLoad_NewerSettingsVersionResetsTheWholeBlock`, `TestLoad_OlderSettingsVersionFallsBackToCurrent`, `TestLoad_PendingSwitchFallsBackWithWarning`, `TestLoad_RetiredMeaningKeysWarnButStillApply`. |
| **I-01.7** | `SUPERSEDED-BY-WAVE4` | The §5 validation rule table grew a *supported-capability-state* rule: a config that turns on a switch this build treats as pending is refused and falls back, so unknown future switches stay off (SP-19 plan line 177, *"Unsupported controls remain disabled"*; `internal/config/validate.go:51,328`; `runtime.go:118,180` `rng:"[1,1]"`). | Same command. New assertion: `internal/config`: `TestValidate_RefusesEveryPendingSwitch` (already selected by the row's `TestValidate_` pattern). |
| **I-01.18** | `RETIRED` | The assertion is *"no `t.Skip(` call at statement position anywhere under `internal/**/*test*/`"*, on the stated premise that `tokenstest/estimator_suite.go` is the sole surviving hit. On this tree there are **30** such calls in **20** conformance-suite packages, all of them the same `skipIfStub` construction — the pattern the whole Rule W-1 idiom is built from, not stub residue. A textual grep for the *call site* cannot tell a skip that fires from one whose stub-probe branch is unreachable, which is precisely what the row cares about; the row's own closing sentence concedes the mechanism ("once I-01.17 is green the stub branch it guards is unreachable"). The assertion is therefore un-passable as written and measures the wrong thing. | **`go run ./tools/devtool lint --only=stubskips`** (`tools/devtool/lint.go:33` registers `{"stubskips", runStubSkips}`; `tools/devtool/stubskips.go:62` documents it as *"greps test output for that [Rule-W1] message and reports"*, and `:141-144` makes a red suite fatal rather than silently "OK"). That gate reads *executed* output, so it detects a skip that actually fired and ignores one that did not. Corroborating live evidence already collected: the `internal/tokens` run for I-01.11 produced **no `--- SKIP`**, i.e. `tokenstest`'s `skipIfStub` did not fire. The replacement command runs tests tree-wide and is deferred below. |
| **I-01.20** | Corrected pattern | `TestGuard_SubmodularEnabledOnlyAfterPSelection` does not exist on this tree. SP-15 did not perform the re-point the row anticipated. | `test/guards`: **`TestGuard_SubmodularDefaultsOff`** (`test/guards/buildorder_test.go`) — present, green. The row's contingency ("if *neither* name is in the output … that is a V5 failure") is not triggered. |
| **I-01.21** | Corrected command | The row's literal `go test ./tools/lint/nomagic/ ./tools/devtool/ -v` is an unfiltered two-package run; `tools/devtool`'s `stubskips_test` and `cover_test` shell out to tree-wide `go test`, which is outside this seat's mandate. | Ran `go test ./tools/lint/nomagic/ -run TestNoMagic_Analyzer -v` and `go test ./tools/devtool/ -run 'TestImportGraph_AcceptsRealRepo\|TestImportGraph_RejectsViolation\|TestTestDeps_RejectsProductionTestify\|TestCheckCommitMsg' -v`. All five named tests PASS. Unfiltered form deferred. |

No row was scored `MISSING`. Note for the coordinator: `V4-VERIFY` scored `V4-SP01-04` (a `cmd/qompack/main.go` < 150 LOC / dispatch-only guard) `MISSING`, and that gap is **still open** — no `I-01.*` row covers it, so it is not double-counted here, but it also has not been closed by wave 4.

## Deferred to coordinator

| Row | Exact command | Reason |
|---|---|---|
| I-01.8 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/config/ -run xxx -fuzz FuzzConfigLoad -fuzztime 60s` | Fuzz run — outside this seat's mandate. `FuzzConfigLoad` confirmed present. |
| I-01.13 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/hookio/ -run xxx -fuzz FuzzReadEvent -fuzztime 60s` | Fuzz run — outside mandate. `FuzzReadEvent` confirmed present. |
| I-01.19 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./... -run 'Golden\|Contract' -v -timeout 30m` | Whole-tree run — outside mandate. 73 matching definitions across 26 packages; `testdata/golden/contracts/` has 11 subtrees. |
| I-01.18 (replacement) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go run ./tools/devtool lint --only=stubskips` | Replacement gate for the retired grep; it runs `go test` tree-wide internally, so it belongs to the serial pass. |
| I-01.21 (literal form) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./tools/lint/nomagic/ ./tools/devtool/ -v -timeout 30m` | The row's unfiltered command; `tools/devtool` invokes tree-wide `go test` from `stubskips_test`/`cover_test`. The five named tests already PASS under filters. |
| I-01.24 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/obs/ ./internal/config/ ./internal/paths/ ./internal/cli/ -run '^$' -bench 'BenchmarkHistogram_Observe\|BenchmarkConfigLoad_ColdNoFiles\|BenchmarkHookNoop_InProcess\|BenchmarkPathsWriteAtomic_4KB' -benchtime 2s` | `FAIL-COLOAD-SUSPECT`. Re-measure **alone**: `BenchmarkPathsWriteAtomic_4KB` 8.57 ms/op vs <2 ms, `BenchmarkHookNoop_InProcess` 16.06 ms/op vs <3 ms. The two CPU-bound budgets passed with wide margin, so a quiet re-run is expected to move the two I/O-bound ones substantially. |

## Questions

**Q1 — I-01.17, the `ErrNotImplemented` survivor list (the one row I could not adjudicate).**
The `go test` half is clean: `test/guards`: `TestAllStubsReturnNotImplemented` PASS, probe table shows
zero stubbed packages, and `internal/cli`'s `notImplemented` table carries exactly the three permitted
rows. The grep half is not. The row admits three survivor classes — (a) the declaration, (b) `<pkg>test`
conformance suites plus `canon/registry.go`'s doc comment, (c) `internal/cli`'s `notImplemented` table —
and declares *"any hit outside (a)–(c) is a V5 failure."* Running the row's own command
(`Select-String -Path internal\**\*.go -Pattern "ErrNotImplemented" -Exclude *_test.go`, 44 hits, plus a
recursive cross-check at 73) leaves **three production sites that *return* the sentinel and fit none of
the three classes**:

1. `internal/analyzer/selector.go:54` — the ship-order gate refusing to construct a `Selector` while
   `scheduler.PSelectionAvailable()` reports false. This is a *deliberate, permanent refusal*, and it is
   the behaviour `TestGuard_SelectorRefusesWithoutPSelection` pins — a guard **I-01.20 itself requires to
   pass**. Reading I-01.17 literally would make I-01.17 and I-01.20 contradict each other.
2. `internal/eval/replay.go:156,161` — live-mode replay refusing unless the run supplies a live provider
   (gated on `liveModeEnvVar`). Also a permanent, intentional refusal, not stub residue.
3. `internal/commands/commands.go:166` — `bodyFor`'s `default:` arm. All seven §7.5 names (`status`,
   `recall`, `why`, `dropped`, `pin`, `checkpoint`, `eval`) resolve to real bodies, and `Specs()` is
   derived from `pluginmanifest.Default(...).Commands`, so the arm is **unreachable for every registered
   command**. It is dead-but-defensive, in a package the row's class (c) does not mention (class (c) names
   only `internal/cli`).

**Ruling needed:** does V5 add a fourth survivor class — *"a guarded refusal or an unreachable default
that no conformance probe can reach"* — covering these three sites, or is one or more of them a real V5
failure? My reading is that all three are correct code and the row's survivor list is simply incomplete
(it was written before SP-14/SP-15 landed `internal/commands` and the P-selection gate), but re-scoping a
V5 exit criterion is not this seat's call. Until it is ruled, I-01.17's grep half is unadjudicated; the
`PASS` in the table covers the executed `go test` half only.

**Q2 — I-01.17's standing survivor, carried forward as the row instructs.** `internal/cli`'s
`notImplemented` table has exactly three rows. `fsck` and `doctor` are annotated `(SP-17)` and are
V6-SP-17's to replace. **`bench` is annotated `(SP-05)` and has no owner**: SP-05 merged in wave 1, no
wave-3/4/5 plan registers a real `bench`, and V6-SP-17's Done checklist does not name it. Per the row,
V5 permits it as a standing survivor with a recorded reason — the hot-path harness it would front is
`test/bench/hotpath`, invoked directly by every budget row — and this fact must reach the V5 completion
report rather than passing silently as an SP-17 surface. Naming an owner (or deleting the row) is a
wave-5 plan-set decision.

**Q3 — I-01.18's disposition.** I scored the row `RETIRED` / `FAIL`: the literal command was executed and
returned 30 hits against an expectation of zero, so `FAIL` is the honest result, while the assertion
itself no longer measures what it intends. If the coordinator prefers the row scored purely on its
replacement gate, the disposition stands but the result should be re-taken from
`go run ./tools/devtool lint --only=stubskips` on the serial pass. Either way, **no conformance suite was
observed to actually skip** in any run I made.

**Q4 — I-01.24 and the reference platform.** The two breached budgets are both disk-bound and were
measured under co-load. `plans/CARRIED-DEFECTS.tsv` shows a standing pattern of Windows I/O budgets being
recorded as never measured on the reference platform (`SP06-D2`, `SP10-D1`). If the quiet re-run still
breaches, this row likely wants the same treatment — a carried defect naming the reference platform —
rather than a V5 failure. That is a coordinator call.

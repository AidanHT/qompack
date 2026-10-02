# V6 inventory map (C6.2 preparation)

Inputs: `plans/sdd/V6-remediation/inventory-current.tsv` (304 rows, not modified) and `current-candidate-gate-map.md`. Tree: `closeout/w8-inventory` at `closeout/integration` 17a42f6 (waves 1-7 merged). Output: `inventory-map.tsv`, with one row per inventory id in the same order.

This file maps rows to evidence. It does not give dispositions, and no test was run to build it. Symbols were resolved against every `func Test*/Benchmark*/Fuzz*/Prop*` declaration in the tracked Go files. Renamed or moved tests were traced with `git log -S`. `evidence_step` names the Phase 3 step (`phase3.sh`), Phase 4 live-lane scenario, or Phase 5 item whose executed artifact the Phase 6 seat should cite. Final dispositions come from the frozen candidate's evidence.

> **Candidate 6, 2026-10-02.** The dispositions this map prepared are given in
> `inventory-c6-map.md` and the `c6_result`/`c6_evidence` columns of
> `../V6-remediation/inventory-current.tsv`. Two notes below are out of date there: TestBudget_DetectorScan
> and TestBudgetBF pass on candidate 6 in both isolated timing passes, and /qompack:checkpoint (1.14.5) no
> longer ships (D36(a)).

## Rows per evidence step

A row can name more than one step, so the counts add up to more than 304.

| evidence step | rows |
|---|---|
| win-tree | 262 |
| linux-tree | 227 |
| gens | 24 |
| lint | 21 |
| C5.2 | 17 |
| linux-e2e | 15 |
| cover | 13 |
| Phase 4 UAT-NN (live lane) | 12 |
| fuzz | 9 |
| C5.1 | 5 |
| bundles | 5 |
| release | 5 |
| C5.3 | 4 |
| Phase 4 C4.6 | 4 |
| linux-timing | 4 |
| none | 4 |
| win-timing | 4 |
| Phase 4 C4.3 | 3 |
| Phase 4 C4.8 | 3 |
| Phase 4 C4.2 | 2 |
| Phase 4 C4.4 | 2 |
| linux-child | 2 |
| meta | 2 |
| win-race | 2 |
| C5.5 | 1 |
| Phase 4 C1.6 | 1 |
| Phase 4 C1.7 | 1 |
| Phase 4 C4.1 | 1 |
| Phase 4 C4.11 | 1 |
| Phase 4 C4.5 | 1 |

The Linux steps run the same Go tests on Linux. `linux-tree` and `linux-e2e` use `-race` under declared co-load, and the `*-timing` steps judge wall-clock rows on an otherwise idle host (D28). `win-tree` also runs `test/e2e` and `test/docs`. Benchmarks run only under C5.1 (bench-hotpath) or C5.2 (carried workloads).

## Rows with missing symbols (24)

Each row below names a symbol that does not exist on this tree. Most are plan-era names that never existed in code: `git log -S` finds them only in plans. The one real rename is `TestPlatform_WindowsHookLauncherForms` (927397e1).

| id | missing -> replacement |
|---|---|
| 1.3.15 | FuzzSketchUnmarshal -> replaced by internal/sketch:FuzzBloomUnmarshalBinary, internal/sketch:FuzzCMSUnmarshalBinary, internal/sketch:FuzzHLLUnmarshalBinary, internal/sketch:FuzzMisraGriesUnmarshalBinary, internal/sketch:FuzzSignatureUnmarshalBinary |
| 1.4.15 | FuzzCanonicalizeRun -> replaced by internal/canon:FuzzCanonicalize |
| 1.10.15 | TestCadenceIsOffInDegradedPassive -> replaced by test/e2e:TestE2E_CheckpointDegradedPassiveSealsNothing |
| 1.12.5 | PropCacheFactor_MonotoneDecreasing -> replaced by internal/scheduler:TestCacheFactor_MonotoneDecreasing_Property |
| 1.12.6 | TestRecordCompactionCost -> replaced by internal/scheduler:TestYoungDaly_Formula, internal/scheduler:TestEvaluate_YoungDaly_UnmeasuredDeltaDisablesClause |
| 1.12.12 | TestStateCorrupt -> replaced by internal/daemon:TestRuntime_CorruptStateFilesSelfHeal |
| 1.13.12 | PropPagingReconstructs -> replaced by internal/mcp:TestPropertyNextSpanPagingCoversObjectExactly |
| 1.14.10 | BenchmarkRenderStatus -> none (internal/commands has no benchmark) | commandstest -> none (no such package) |
| 1.15.6 | PropGuaranteeUnitCost -> none for the (1-1/e) bound itself; lazy-greedy equivalence test only; PropGuaranteeKnapsack also absent |
| 1.16.9 | TestTruncationCurve -> replaced by internal/checkpoint:TestGoldenTruncationFixtures |
| 1.16.10 | TestPrefixReorderingNotAttempted -> none; the ADR-prose declaration it points to (docs/adr/0016) does not exist either |
| 1.17.4 | TestPlatform_WindowsHookLauncherForms -> replaced by test/platform:TestPlatform_HookLauncherForms |
| 1.17.8 | TestUninstallLeavesNothing -> replaced by test/e2e:TestInstall_HostCLIInstallUpgradeUninstall |
| 1.17.9 | TestUpgradeAcrossVersions -> replaced by test/e2e:TestInstall_HostCLIInstallUpgradeUninstall, internal/cli:TestFsck_ANewerCheckpointSchemaIsNotCorrupt | TestUpgradeAcrossVersions_CheckpointSchemaBump -> replaced by internal/checkpoint:TestGetRefusesANewerSchemaVersion, test/e2e:TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting |
| 1.17.11 | TestHooksExitZero_Matrix -> test/fault suite (TestFault_*) + internal/cli TestFaultActive_* |
| 1.17.14 | TestDegradation_* -> test/fault suite (TestFault_*), different decomposition |
| 1.18.3 | TestMetaRangesMatchValidate -> replaced by tools/devtool:TestGenConfigDocs_LeavesMatchDefaultsOneToOne |
| 1.18.4 | TestConfigReferenceRuntimeSectionIsMarkedAdditive -> replaced by tools/devtool:TestGenConfigDocs_GatedSwitchesRenderFromSource |
| 1.18.5 | TestCannotDoListVerbatim -> replaced by test/docs:TestCannotDoCoversSection12Limits, test/docs:TestCannotDoNamesTheUnimplementedChecks |
| 1.18.7 | TestLoudGlossary* -> replaced by test/docs:TestTroubleshootingNamesEveryContractAssertion, test/docs:TestTroubleshootingNamesEveryEvidenceOutcome, test/docs:TestTroubleshootingNamesEverySelfTestCheck |
| 1.18.8 | TestAdrShape -> replaced by test/docs:TestADRIndexListsEveryADR | TestAdrShape_IndexEntriesExist -> replaced by test/docs:TestADRIndexListsEveryADR | TestAdrShape_NamesItsDecisionID -> replaced by test/docs:TestADRIndexListsEveryADR |
| 1.18.9 | TestArchitectureQuotesTenInvariants -> none (no verbatim-invariants test) | TestArchitectureQuotesTenInvariants_DigestSections -> none | TestArchitectureQuotesTenInvariants_LinksEveryADR -> replaced by test/docs:TestADRIndexListsEveryADR |
| 1.18.11 | TestUATQuotesPhaseExitCriteria -> none |
| 1.18.13 | BenchmarkGenerate -> none (B-DOC demoted to diagnostic, E-2) |

In 40 more rows, the inventory uses a short name for a test family, for example `TestRecall` for `TestRecall*` in internal/mcp. No function has exactly that name, but the family exists, so these rows count as found and carry a `[family; ...]` marker in symbols_found: 1.2.7, 1.3.8, 1.4.4, 1.5.10, 1.6.6, 1.6.7, 1.6.9, 1.6.10, 1.7.4, 1.7.5, 1.7.6, 1.7.8, 1.7.9, 1.8.8, 1.9.1, 1.9.2, 1.9.3, 1.9.6, 1.10.10, 1.10.12, 1.11.4, 1.11.15, 1.12.8, 1.12.9, 1.12.15, 1.12.16, 1.13.2, 1.13.3, 1.13.4, 1.13.5, 1.13.6, 1.13.7, 1.13.8, 1.13.9, 1.13.10, 1.13.11, 1.13.13, 1.14.1, 1.15.12, 1.15.13.

## Rows with no possible executed artifact

No planned Phase 3-5 step can produce an executed artifact for these rows as written. Each needs a Phase 6 disposition, not a PASS.

- **1.16.5** warm-vs-cold O4 delta: no test or step computes it.
- **1.16.10** prefix-reorder non-delivery: the ceremonial test is absent, `docs/adr/0016` does not exist, and no doc under docs/ declares it.
- **1.17.3** binary size budget: this tree has no devtool size check.
- **1.17.19** required CI jobs / branch protection: only a hosted CI run can show these (C7.2, Phase 7). The last recorded run was on historical source 9c84e31.
- **1.18.12**, the "executed by a human" half: under D3, UAT is agent-executed, so no human run will exist. The live lane evidences the agent-run form.

These rows get an artifact only if a step is widened:

- **C5.2 named list** (store/read/search, OnToolUse 256 KB, Finalize, negknow Open, GetChunk) leaves out the benches of 1.1.16, 1.1.27, 1.2.12, 1.3.7, 1.4.14, 1.7.8, 1.7.10, 1.8.2, 1.10.17 (AdvanceSegment/ExtractDecisions), 1.11.16, 1.12.17, 1.15.14 and 1.16.11.
- **1.10.18**: no step runs the two `replay --phase 4` runs with the frontier toggled, and C5.3 does not name them.
- **1.17.5 / 1.17.6**: bench-hotpath has no `--bundle universal|native` mode, so the launcher-overhead split cannot run as written.
- **1.14.10**: there is no `commandstest` package and no internal/commands benchmark. Only the coverage half has an artifact.
- **1.17.9**: C4.8's "previous build" is develop 301a8e9, not a released binary. The old-released-reader matrix has no planned artifact.
- **1.17.17 / 1.17.18**: release-check without `--tag` skips version agreement, and actionlint and the goreleaser snapshot run only on a tag (Phase 7).

## Rows whose assertion a D-decision changed

| id | decision | change |
|---|---|---|
| 1.1.5 | D14 | dependency allowlist now includes golang.org/x/sys/unix |
| 1.1.7 | D14, D22 | import graph amended: x/sys/unix added; config may import paths |
| 1.1.10 | D8 | runtime.redact and runtime.mode fail closed; only the other keys fall back |
| 1.1.28 | D5 | design doc is now v1.6 (host-cap revision), not v1.5 |
| 1.5.5 | D17, D27, D35(a) | a fresh spawn lock is honoured; Lock.Release gives run/spawn.lock back |
| 1.5.6 | D10, D17, D21 | Windows daemon runs from a verified staged copy; session-start spawn is bounded and may borrow |
| 1.5.8 | D13, D31, D35(b,c) | drain-replayed SessionEnd takes the async end; soft drain budget; host-order replay; a flush during daemon stop waits in the spool |
| 1.6.18 | D5 | budget is the host 10,000-char cap (~9,500 incl. wrapper) with named overflow |
| 1.8.6 | D35(b) | SP08-D3 not fixed as originally stated; host-order guarantee with a Warn and notice, documented residual |
| 1.8.9 | D13, D18 | SessionEnd is async; a home-directory project root is refused |
| 1.11.1 | D5 | priority-ordered whole records under the cap |
| 1.11.10 | D5 | the 8-12K-token budget is replaced by the 10,000-char cap |
| 1.11.11 | D11 | a failed build or unreadable store answers with the deferred note |
| 1.11.12 | D9, D11, D29 | 5 s compact bound, a "rehydration deferred" note, and the borrow-edge "no answer" note |
| 1.14.5 | D34(e) | /qompack:checkpoint must be routed or stop shipping; this tree still ships it |
| 1.17.8 | D2, D6, D16, D26 | rollover on by default makes the pre/post-write rollback real; backup Warn; Windows backup residual |
| 1.17.9 | D2 | the rollover format is the schema change old readers must survive |
| 1.17.17 | D1 | v0.3.0; core.Version and plugin.json move at C7.1 |
| 1.18.12 | D3 | UAT is agent-executed on the real host, never human UAT |
| 1.5.20, 1.13.4, 1.13.5, 1.17.12 | D7 | saved-settings deny/ask rules are honoured and fail closed; session, CLI and hook policies remain a documented residual |

Decisions that change how a row is judged but not what it asserts: D28 (isolated Linux timing pass) for 1.5.12, 1.6.14, 1.7.10, 1.9.12, 1.10.16, 1.13.16 and 1.17.6; D20/D26 durability costs for 1.5.12, 1.6.19 and 1.8.13; D6/D30 GC for 1.6.14; D13 for 1.5.7 and 1.5.18; D15 ceilings for 1.5.16 and 1.15.9; D19 for 1.5.14; D22/D25 for 1.1.9; D23 for 1.1.24; D24/D26 for 1.1.13; D12 for 1.14.6; D34 for 1.17.7 and 1.17.16; D4/D32/D33 for 1.17.18.

## Expected reds the Phase 6 seat should not misread

- 1.9.12 `TestBudget_DetectorScan` and 1.13.16 `TestBudgetBF` already fail on develop. Each needs its own disposition.
- 1.13.4 and 1.17.12 stay FAILED-preserved until test/security and the TestV6_* mcp suites pass on the frozen candidate.
- 1.17.15: publication accounting detects the problem but does not recover from it.
- 1.17.1: C3.11 needs all six targets, and the earlier rc1 bundle identity covered only two.

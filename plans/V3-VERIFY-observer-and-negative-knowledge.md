# V3 — Verification checkpoint after wave 2 (L0 observer + negative knowledge)

**This file is a standalone prompt.** Read it end to end before running anything. You do not need
any other plan file to execute it, though `plans/00-ARCHITECTURE.md` and `Qompack.md` are the
normative references if a command below disagrees with the code.

---

## 0. When this runs, and where

**Trigger.** This checkpoint runs on `develop` immediately after **both** wave-2 branches have
merged, in the merge order fixed by `00-ARCHITECTURE.md` §9 and §14:

1. `feat/sp08-observer-l0` → `develop` (`--no-ff`)
2. `feat/sp09-negative-knowledge` → `develop` (`--no-ff`)

Wave 2 is the pair *SP-08 observer L0* and *SP-09 negative knowledge* (§14 wave/subplan map).
Conflicts are resolved on the **incoming** branch and re-merged — never with a hand-edited merge
commit (§9).

**Branch.** All work for this checkpoint happens on:

```
git checkout develop
git pull
git checkout -b verify/v3
```

`verify/v3` is cut from the post-merge `develop`. Every fix this checkpoint produces lands on
`verify/v3` as small conventional commits, and `verify/v3` merges back into `develop` with
`--no-ff` when the checkpoint is fully green.

**Gate.** §9: *"The next wave's branches are cut from the post-verification `develop`. No wave-N
branch is ever cut before verification V<N> is green."* Therefore **no wave-3 branch — not
`feat/sp10-checkpointer-l4`, not `feat/sp11-rehydrator-l5`, not `feat/sp12-scheduler-l3`, not
`feat/sp13-mcp-retrieval-layer` — may be created until every row of the completion report in §8 of
this file reads PASS.**

**What this checkpoint is.** It is **not** a smoke test. It is an exhaustive re-verification of
*every* functionality that exists in the codebase at this point: SP-01, SP-02, SP-03, SP-04,
SP-05, SP-06, SP-07 (waves 0–1, already verified at V1 and V2) **and** SP-08, SP-09 (wave 2, new).
Everything that passed before must still pass, now that the observer is actually feeding the store,
the DAG and the sketches on every tool call, and now that a live elimination ledger sits beside it.

**What this checkpoint must NOT test.** Nothing from SP-10 (checkpointer), SP-11 (rehydrator),
SP-12 (scheduler), SP-13 (MCP), SP-14 (slash commands), SP-15 (analyzer/grammar), SP-16 (Phase 7),
SP-17 (packaging), SP-18 (docs). Those packages are still SP-01 stubs on this `develop`. Their
*stub-ness* is itself verified below (items A12, A14) — but their behaviour is not, and no test
authored in this checkpoint may assume it.

**Phases closed at this point.** `Qompack.md` §10 Phase 0 (measurement, SP-02), Phase 1 (store and
observer, SP-03/04/05/06/08) and Phase 2 (negative knowledge, SP-09). Phases 3–7 are not started.

**Tag.** On success, after the merge back into `develop`, apply `v0.2.0` (§9: `v0.<wave>.<n>` on
`develop` after each verification).

---

## 1. Ground rules for this checkpoint

1. **Run everything from the repository root** on the Windows dev machine unless a row says
   otherwise; the three-OS matrix rows are satisfied by CI, and the CI run on `verify/v3` is part of
   the checkpoint, not an optional extra.
2. **PowerShell caveats.** `wc -l` does not exist. Use `git rev-list --count <range>` for commit
   counts and `Select-String` where a row shows `grep`. Both spellings are given where it matters.
3. **Never regenerate a golden to make a test pass.** Rule W-2: *"Any fixture that the real
   implementation cannot reproduce is a verification failure, not a fixture bug."* `-update` and
   `-write-report` flags are forbidden during this checkpoint. `internal/sketch/golden_test.go`
   already refuses `-update` when `CI` is set; treat the local run under the same rule.
4. **Record every number.** Every row that produces a measurement (latency, ratio, coverage,
   allocation count) feeds the completion report in §8. "Passed" without the number is not a pass.
5. **`Qompack.md` is immutable.** `git diff <root-commit> HEAD -- Qompack.md` must be empty at the
   start and at the end of this checkpoint.
6. **Fan out, then converge.** See §9 for the subagent partition. The cross-component integration
   tests of §5 are authored and run **in the main session only**.

**One-line preflight** (run before anything else; if it fails, stop and fix before proceeding):

```
go build ./... && go vet ./... && go run ./tools/devtool fmt-check
```

---

## 2. Cumulative functionality inventory

Every item below is a functionality that exists in the codebase at this point. Each has an ID, the
exact command(s) to run, and the expected result. Items are grouped by the subplan that delivered
them; the grouping is also the subagent partition (§9).

Legend for expected results: **PASS** means exit code 0 and no failing test unless a specific
assertion is named.

---

### Group A — SP-01: foundation, toolchain, contracts

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **A1** | Repository topology: `main` and `develop` exist; root commit contains exactly `Qompack.md`, `plans/`, `.gitignore`, `LICENSE` | `git log --oneline --all \| tail -5` ; `git show --stat $(git rev-list --max-parents=0 HEAD)` | Root commit subject `chore: initial commit — design document and build plans`; its tree lists only those four paths |
| **A2** | Go module builds as a single static binary, all 6 release targets | `go run ./tools/devtool build` ; `go run ./tools/devtool build-all` | `bin/qompack` produced; `dist/qompack-{linux,darwin,windows}-{amd64,arm64}[.exe]` — 6 artifacts, all non-empty |
| **A3** | Full lint chain: golangci-lint + nomagic + importgraph + testdeps + bindeps + sleepcheck + stubskips | `go run ./tools/devtool lint` | Exit 0. In particular `importgraph` passes against the real repo (§3.2 allow-table), and `bindeps` proves `go list -deps ./cmd/qompack` contains only stdlib, `github.com/qompack/qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| **A4** | `nomagic` D11/§11.6 pass — no config-default literal outside `internal/config/defaults.go` | `go run ./tools/lint/nomagic ./...` | Exit 0. Every remaining occurrence of `{0.1,1.25,12.5,0.55,0.004,0.9,0.4}` / `{20000,12000,10000,8000,2048,1024,4096,16384,300,120,450}` is in `*_test.go`, in `defaults.go`, or on a `//nomagic:allow <reason>` line |
| **A5** | `internal/core` primitives: `Hash`, `HashBytes` domain separation, `ParseHash`, sentinels, `DecisionID` | `go test ./internal/core/...` | PASS: `TestHashBytes_DomainSeparation`, `TestHashBytes_KnownVector`, `TestHash_StringShortParse_RoundTrip`, `TestParseHash_Rejects`, `TestHash_JSONRoundTrip`, `TestNewDecisionID_Format`, `TestSentinels_AreDistinct` |
| **A6** | `internal/paths`: resolution, `Norm`/`Key`, `WriteAtomic`, `CreateNew`, long paths, manifest | `go test ./internal/paths/...` | PASS incl. `TestResolve_EnvWins`, `TestResolve_WalksToGitDir`, `TestResolve_GitFileWorktree`, `TestNorm_RejectsEscape`, `TestKeyFold`, `TestEnsureLayout_CreatesAllDirsAndSelfIgnore`, `TestLongPath_Over260`, `TestManifest_AppendAndRead`, `TestNorm_Property` |
| **A7** | **Append-only invariant (§7.4, §13 invariant 2)** mechanically enforced | `go test ./internal/paths/ -run TestAppendOnlyGuard -v` | PASS: all five illegal writes fail — `O_TRUNC` on a checkpoint, in-place rewrite of `pins/invariants.jsonl`, `WriteAtomic` onto `sketches/tried.bloom`, second `CreateNew` of the same seq, `AppendOnly` on a wrong-extension path |
| **A8** | `internal/config`: Appendix C verbatim, 5-layer precedence, deep per-leaf merge, env mapping, `null` semantics, provenance, JSON Schema, validate-and-correct | `go test ./internal/config/...` | PASS incl. **`TestDefaults_MatchesAppendixCVerbatim`**, `TestLoad_PrecedenceFiveLayers`, `TestLoad_DeepMergePerLeaf`, `TestLoad_EnvKeyMapping`, `TestLoad_NullMeansMeasure`, `TestLoad_UnknownKeyWarnsNeverErrors`, `TestLoad_InvalidLeafFallsBackNotCrash`, `TestValidate_EveryRule`, `TestValidate_RuleTableIsComplete`, `TestValidate_TiersPartition`, `TestValidate_TelemetryMustBeFalse`, `TestJSONSchema_Golden`, `FuzzConfigLoad` |
| **A9** | `internal/logging` `Loud` channel (three destinations) and `internal/obs` log-bucket histograms + budget IDs B-A…B-F | `go test ./internal/logging/... ./internal/obs/...` ; `go test -bench BenchmarkHistogram_Observe -run '^$' ./internal/obs/` | PASS incl. `TestLoud_ThreeDestinations`, `TestLogger_Rotation`, `TestHistogram_PercentileConservative`, `TestBudgets_AllSixPresentAndConfigDriven`, `TestCheckBudgets_CountsConsecutiveWindows`; `BenchmarkHistogram_Observe` **< 100 ns/op** |
| **A10** | `internal/hookio` codecs, unknown-field tolerance, seven hook payload goldens | `go test ./internal/hookio/...` | PASS incl. `TestReadEvent_AllSevenHookPayloads`, `TestReadEvent_UnknownFieldsPreserved`, `TestReadEvent_MissingFieldsNeverPanic`, `TestReadEvent_LimitExceeded`, `TestWriteOutput_EmptyIsMinimal`, `FuzzReadEvent` |
| **A11** | `internal/cli` dispatch and **hooks always exit 0** under fault injection (§13 invariant 6) | `go test ./internal/cli/... -run 'TestDispatch\|TestHooks\|TestConfig\|TestSetFlag'` | PASS: `TestDispatch_HookAlwaysExitsZero` (all 30 SP-01 combinations), `TestDispatch_NonHookErrorExitsOne`, `TestDispatch_UnknownCommandExitsTwo`, `TestDispatch_PanicRecovered`, `TestConfigPrint_Provenance`, `TestConfigSchema_Emits` |
| **A12** | Interface stubs for every §5 package a **later** wave owns, plus the 22 `<pkg>test` conformance suites | `go test ./internal/... -run 'Suite'` ; `go run ./tools/devtool lint --only=stubskips` | PASS. Suites for `analyzer, scheduler, checkpoint, pins, rehydrate, rules, skills, mcp` still skip their behaviour block with the **exact** message `behaviour: implementation is a stub (Rule W-1)`. Suites for `chunk, canon, symbols, redact, sketch, store, dag, negknow, eval, ipc, contract, tokens, observer` have **zero** skips |
| **A13** | `internal/pluginmanifest` and the generated `plugin/` bundle | `go run ./tools/devtool plugin-validate` ; `git diff --exit-code -- plugin/` ; `go test ./internal/pluginmanifest/...` | Exit 0 on all three. `TestManifest_CoversAllSixHooks` confirms the seven hook entries with timeouts `5,5,15,20,5,10,20`; `TestManifest_SevenCommands`; `TestMCPJSON_UsesPluginRoot` |
| **A14** | **Closing-note build-order guards** and the contract/write-set/network guards | `go test ./test/guards/... -v` | PASS: `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection` (still active — `scheduler.PSelectionAvailable()` is false at wave 2), `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull`, `TestGuard_WriteSetConfinedToQompack`, `TestGuard_NoNetworkImports`, `TestAllStubsReturnNotImplemented` |
| **A15** | `internal/testutil` fixtures + `test/e2e` harness against the real binary | `go test ./internal/testutil/... ./test/e2e/ -run 'TestProject\|TestFakeClock\|TestGolden\|TestWindowsHostileFiles\|TestE2E_AllSixHooksExitZero\|TestE2E_ConfigPrintFromRealBinary'` | PASS; `TestE2E_AllSixHooksExitZero` runs all six hooks through the built binary with exit 0 and parseable stdout |
| **A16** | `docs/config-reference.md` can never drift from `config.Defaults()` | `go run ./tools/devtool gen-config-docs --check` | Exit 0, no diff |
| **A17** | Commit-message policy: conventional commits, **no attribution trailers** | `git log --format=%B origin/main..develop \| grep -Ei "co-authored-by\|signed-off-by\|generated with\|🤖"` (PowerShell: `git log --format=%B origin/main..develop \| Select-String -Pattern "co-authored-by\|signed-off-by\|generated with"`) | **No output.** Also: every subject on `develop` matches `^(feat\|fix\|docs\|test\|refactor\|perf\|build\|ci\|chore\|revert)(\([a-z0-9/_.-]+\))?: .{1,64}$` |

---

### Group B — SP-02: replay harness, Belady OPT, Phase 0 baseline

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **B1** | `eval` data model, `Blocks` and `Demands` derivation | `go test ./internal/eval/ -run 'TestBlocks\|TestDemands\|TestApproachClass'` | PASS: `TestBlocks_PositionsAreCumulative`, `TestBlocks_FileBlockPerDistinctKey`, `TestBlocks_DecisionMarkerExtracted`, `TestDemands_OnlyPreCompactionBlocks`, `TestDemands_DeduplicatesWithinTurn`, `TestDemands_EliminationMatchByApproachClass`, `TestApproachClass_Normalization` |
| **B2** | **Belady OPT keep-sets (§6.10, §11.1)** — knapsack DP with fallback | `go test ./internal/eval/ -run TestBelady` | PASS: `TestBelady_UnitWeightsMatchesClassicBelady`, `TestBelady_KnapsackBeatsGreedyDensity`, `TestBelady_BudgetNeverExceeded`, `TestBelady_Deterministic`, `TestBelady_ZeroValueBlocksPruned`, `TestBelady_PMinIsEarliestDropped`, `TestBelady_FallbackWhenDPTooLarge`, `TestBelady_ContextCancelled` |
| **B3** | §5.6 breakpoint-placement OPT — **measurement only**, always disclaimed | `go test ./internal/eval/ -run TestBreakpointOPT` | PASS incl. `TestBreakpointOPT_KnownOptimum`, `TestBreakpointOPT_MoreMarkersNeverWorse`, `TestBreakpointOPT_NoteIsAlwaysTheDisclaimer` (`Plan.Note == NotPluginActionable`) |
| **B4** | Policy registry and the three built-ins (`stock`, `null`, `oracle`) | `go test ./internal/eval/ -run 'TestStockPolicy\|TestNullPolicy\|TestRegisterPolicy\|TestPolicyNames\|TestOraclePolicy'` | PASS: `TestStockPolicy_TopFiveFilesFiveKEach`, `TestStockPolicy_PreservationMinimums`, `TestStockPolicy_UsedNeverGoesNegative`, `TestStockPolicy_PIsZero`, `TestNullPolicy_Empty`, `TestPolicyNames_Sorted` == `["null","oracle","stock"]`, `TestOraclePolicy_ScoresExactlyOne` |
| **B5** | Counterfactual replay, deterministic mode, latency model, live-mode refusal | `go test ./internal/eval/ -run TestReplay` | PASS incl. `TestReplay_DeterministicAcrossRuns`, `TestReplay_LiveModeRefusedWithoutEnv`, `TestReplay_LatencyModelAnchors`, `TestReplay_HorizonRespected` |
| **B6** | §4.2 divergence metrics (all five bullets) | `go test ./internal/eval/ -run TestCompare` | PASS: `TestCompare_IdenticalRuns`, `TestCompare_FirstDivergenceIsRelativeToCompaction`, `TestCompare_JaccardHalf`, `TestCompare_EditDistanceKnown`, `TestCompare_EditDistance_Property`, `TestCompare_RedundantReadsCanBeNegative`, `TestCompare_DecisionPreservationDenominatorZero` |
| **B7** | `ScoreRun` / `Report` — fraction-of-OPT, §5.2 rewrite-cost table, percentiles, 19-metric map | `go test ./internal/eval/ -run 'TestScoreRun\|TestReport\|TestMetricsOf'` | PASS incl. `TestScoreRun_FractionIsMicroAveraged`, `TestScoreRun_RewriteTokensSection52TableA` (`rewrite_span_tokens == 17000`, `rewrite_tokens == 21250`, `forfeited_discount_tokens == 15300`), `TestScoreRun_RewriteTokensSection52TableB` (`157000 / 196250 / 141300`), `TestScoreRun_NoHardcodedMultiplier`, `TestReport_PercentilesRecomputedNotAveraged`, `TestMetricsOf_CoversEveryDirection` |
| **B8** | Deterministic synthesizer + the committed 24-session corpus | `go test ./internal/eval/ -run 'TestSynthesize\|TestCorpus'` | PASS: `TestSynthesize_ByteIdenticalForSeed`, **`TestSynthesize_MatchesCommittedCorpus`** (all 24 regenerate byte-for-byte), `TestSynthesize_ShapeInvariants`, `TestSynthesize_EveryCompactionHasDemands`, `TestCorpus_CountAtLeastMinSessions` (24 ≥ 20), `TestCorpus_ManifestHashesMatch` |
| **B9** | Recorded-corpus importer with redaction; `qompack eval import` | `go test ./internal/eval/ -run 'TestImport\|TestRedact'` ; `go test -fuzz FuzzRedact -fuzztime 60s ./internal/eval/` | PASS incl. `TestImport_RefusesDestinationInsideRepo`, `TestRedact_AllEightRules`, `TestRedact_Idempotent`; fuzz run clean, no new crashers |
| **B10** | `test/replay` driver: the §11.3 2% rule, sign-off trailer, phase gates, growth guardrail, watch-fors | `go test ./test/replay/...` | PASS: `TestGate_NoRegressionPasses`, `TestGate_TwoPercentBoundaryExclusive`, `TestGate_LowerBetterMetricDirection`, `TestGate_SignOffAllowsNamedMetricOnly`, `TestGate_SignOffRejectsShortReason`, `TestGate_ZeroBaselineUsesAbsoluteTolerance`, `TestGate_Phase0ExitCriterion`, `TestGate_Phase0Reproducibility`, `TestGate_BloomFPCeiling`, `TestGate_CorpusStaleness`, `TestGate_GrowthInconclusiveFails`, `TestGate_MaxWallExceeded`, `TestGate_PhaseChecksMayNotBeDisabledInCI`, `TestGate_BaselineHasExactlyNineteenKeysPerPolicy`, `TestReplayDriver_EndToEnd` |
| **B11** | **The Phase-0 single number, reproducible** | `go run ./test/replay --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 2 --growth testdata/golden/contracts/store/stats-growth.json --sketch testdata/golden/contracts/negknow/health.json --max-wall 2m --ci` | Exit 0. `sessions == 24`; `policies.stock.fraction_of_opt` equals the committed baseline exactly; `oracle == 1.0`; `null == 0.0`; `stock > null`. Note `--phase 2` — phase checks 0, 1 and 2 must all run and pass now |
| **B12** | Sublinear-growth guardrail (§11.3), now fed by the **real** store | see X6 in §5 | `GrowthResult.Sublinear == true`, `Exponent ≤ 0.95`, never `inconclusive` |
| **B13** | SP-02 performance budgets E-1…E-5 | `go test -bench 'BenchmarkBeladyDetail_400Turns\|BenchmarkSynthesize_320Turns\|BenchmarkCompare_400Actions\|BenchmarkBreakpointOPT_256Candidates' -run '^$' ./internal/eval/` | E-2 ≤ 250 ms/op; E-3 ≤ 50 ms/op; E-4 ≤ 20 ms/op; E-5 ≤ 15 ms/op. E-1 (full driver wall) < 120 s, from B11's elapsed time |

---

### Group C — SP-03: sketch library

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **C1** | Versioned `QPKS` header, CRC32C framing, domain-separated hashing | `go test ./internal/sketch/ -run 'TestHeader\|TestHash128'` | PASS incl. `TestHeader_FrameLayout`, `TestHeader_ParamsSortedDeterministically`, `TestHeader_DetectsSingleBitFlip`, `TestHeader_RejectLyingBodyLen` (0 allocs on rejection), `TestHash128_DomainSeparated` |
| **C2** | **Bloom filter** — Appendix A sizing, measured FP rate, resize, saturation, rebuild | `go test ./internal/sketch/ -run TestBloom` | PASS: `TestBloom_AppendixASizing` (`m == 95872`, `k == 7`, body 11 984 B), `TestBloom_NoFalseNegatives`, `TestBloom_FillRatioAtCapacity` ∈ [0.51,0.53], **`TestBloom_EstimatedFPRateMatchesEmpirical`** empirical FP ∈ [0.008,0.013], `TestBloom_ResizeFiresBeforeCapacity`, `TestBloom_SaturatedThreshold`, `TestBloom_RebuildFromIterator`, `TestBloom_ClampsIllegalArgs` |
| **C3** | **Count-Min** — Appendix A sizing, no-underestimate, merge, scale, heavy hitters | `go test ./internal/sketch/ -run TestCMS` | PASS: `TestCMS_AppendixASizing` (`2719 × 5`, body 54 380 B), `TestCMS_EstimateNeverUnderestimates`, `TestCMS_ErrorBoundHolds`, `TestCMS_MergeFromIsAdditive`, `TestCMS_ScaleDecays`, `TestCMS_HeavyHittersPairsWithMG` |
| **C4** | **HyperLogLog** — 2 048 registers, 2.3% standard error, exact merge | `go test ./internal/sketch/ -run TestHLL` | PASS: `TestHLL_AppendixSizing`, `TestHLL_SmallRangeLinearCounting`, `TestHLL_ErrorBounds` (≤ 0.07 at n ∈ {1e3,1e4,1e5,1e6}), `TestHLL_MergeIsExactUnion` |
| **C5** | **Misra-Gries** — deterministic top-k with no false positives | `go test ./internal/sketch/ -run TestMG` | PASS: `TestMG_UnderCapacityIsExact`, `TestMG_DecrementPhase`, `TestMG_FrequentItemGuarantee`, `TestMG_NoFalsePositives`, `TestMG_DeterministicUnderMapOrder`, `TestMG_MergeFrom` |
| **C6** | **MinHash** signatures, Jaccard, near-dup, shift invariance | `go test ./internal/sketch/ -run TestMinHash` | PASS: `TestMinHash_IdenticalInputs`, `TestMinHash_DisjointInputs`, **`TestMinHash_OneNewFailure`** (Jaccard ≥ 0.9), `TestMinHash_ShiftInvariance`, `TestMinHash_SubsamplingEngages`, `TestMinHash_StableAcrossRuns` |
| **C7** | Atomic `Save`/`Load`, generational `tried.bloom` replacement, quarantine, `Loud` on corruption | `go test ./internal/sketch/ -run 'TestSave\|TestLoad\|TestReplaceGenerational\|TestQuarantine\|TestAppendOnly'` | PASS: **`TestSave_RefusesTriedBloom`** (`ErrGenerational`), `TestReplaceGenerational_KeepsOneGeneration`, `TestReplaceGenerational_RollsBackOnWriteFailure`, `TestLoadWithLog_LoudOnCorrupt`, `TestAppendOnly_TriedBloomNeverTruncated` |
| **C8** | Frozen on-disk formats + fuzz robustness | `go test ./internal/sketch/ -run 'TestGolden\|TestProp\|TestImports'` ; `go test -fuzz FuzzBloomUnmarshalBinary -fuzztime 60s ./internal/sketch/` (repeat for the CMS/HLL/MG/Signature targets) | `TestGolden_OnDiskStability` reproduces all five binaries **without `-update`**; `TestGolden_V1StillDecodes`; `TestImports_FoundationOnly`; every fuzz target 60 s clean |
| **C9** | Sketch performance budgets, incl. the L0 composite | `go test -bench 'BenchmarkBloomAdd\|BenchmarkBloomTest\|BenchmarkCMSAdd\|BenchmarkHLLAdd\|BenchmarkL0SketchUpdate\|BenchmarkMinHash\|BenchmarkRebuildBloom5000' -benchmem -run 'TestL0SketchUpdate_ZeroAlloc' ./internal/sketch/` | `Bloom.Add/Test`, `CMS.Add/Estimate`, `HLL.Add` ≤ 1.0 µs/op & 0 allocs; **`BenchmarkL0SketchUpdate` ≤ 5 µs/op, 0 allocs** and `TestL0SketchUpdate_ZeroAlloc` PASS; `MinHash` 4 KiB ≤ 1.5 ms, 100 KiB ≤ 2.5 ms; `RebuildBloom` 5 000 keys ≤ 15 ms |

---

### Group D — SP-04: chunking, canonicalization, symbols

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **D1** | **FastCDC** — `Split`, `SplitStream`, `RootHash`, size bounds, contiguity, cross-platform determinism | `go test ./internal/chunk/...` | PASS: `TestParamsValidate_Table`, `TestGearTableGolden`, `TestSplit_SizeBounds`, `TestSplit_Contiguity`, `TestSplit_AllZeros_HitsMaxOnly`, `TestSplit_Determinism`, **`TestSplit_GoldenBoundaries`** (same golden on all three OSes in CI), `TestSplit_MeanChunkSize` ∈ [3200,5200], `TestSplitStream_MatchesSplit`, `TestRootHash_DomainSeparation` |
| **D2** | **Boundary stability under insertion/deletion** (§5.5 normative property) | `go test ./internal/chunk/ -run 'TestPropBoundaryStability' -v` | PASS: chunks before `k` byte-identical (every trial); a realignment index exists (every trial); novel-chunk count ≤ 12 (every trial); ≤ 2 in ≥ 85% and ≤ 3 in ≥ 95% of the 512 trials. The logged novelty histogram is recorded in the completion report |
| **D3** | **Canonicalizer registry** — 7 generic + 7 per-tool rules, deterministic order, `Strip` gate, overlap resolution | `go test ./internal/canon/...` | PASS incl. `TestRegistry_ForDeterministicOrder`, `TestRegistry_Names` (14 names), `TestMatcherClassAssigned`, `TestOverlapResolution_*`, `TestNonGrowingGuard_*`, `TestCRLF_Table`, `TestANSI_Table`, `TestTimestamps_Table`, `TestDurations_Table`, `TestPIDs_Table`, `TestAddresses_Table`, `TestTmpPaths_Table`, `TestBash_*`, `TestTestRunner_{Go,Jest,Pytest,Cargo}`, `TestGrep_PathPrefixOnly`, `TestGlob_*`, `TestFileRead_*`, `TestWebFetch_Table`, `TestGit_Table`, `TestGoldenCorpus_AllFiles` (no `-update`) |
| **D4** | Canonicalizer **normative properties**: idempotence, non-growth, byte-exact `Restore` | `go test ./internal/canon/ -run 'TestProp\|TestRestore'` ; `go test -fuzz FuzzCanonicalizeRun -fuzztime 120s ./internal/canon/` ; `go test -fuzz FuzzRestore -fuzztime 120s ./internal/canon/` | `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `len(canonical) ≤ len(input)` always; `Restore(canonical, deltas) == input` whenever `KeepDeltas`; both fuzz runs clean |
| **D5** | `internal/symbols` — language-agnostic extraction, `Enclosing`, `References` | `go test ./internal/symbols/...` ; `go test -fuzz FuzzExtract -fuzztime 120s ./internal/symbols/` | PASS across all dialect tables (`Go`, `TS`, `Python`, `Rust`, `JVM`, `C`, `Ruby`, `Shell`, `PHP`, generic); `TestEnclosing_SmallestSpanWins`; `TestReferences_WordBoundaries`; `TestSymbolsConformance` zero skips; fuzz clean |
| **D6** | **With/without-canonicalization dedup measurement** (`Qompack.md` §10 Phase 1, the "measure with and without" clause) | `go test ./test/dedup/...` | `TestDedupRatio_WithVsWithout`: `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0`; `fileread` group `ratioWith > 1.0`. `TestDedupReport_Written` reproduces `testdata/canon-dedup-report.json` byte-for-byte **without `-write-report`** |
| **D7** | SP-04 performance budgets | `go test -bench 'BenchmarkSplit_100KB\|BenchmarkGearScan_1MiB\|BenchmarkSplit_1MiB\|BenchmarkSplitStream_4MiB\|BenchmarkRootHash_1000Chunks' -benchmem -run '^$' ./internal/chunk/` ; `go test -bench 'BenchmarkRun_Bash100KB\|BenchmarkRun_GoTest\|BenchmarkRestore_100KB' -run '^$' ./internal/canon/` ; `go test -bench 'BenchmarkExtract_100KB\|BenchmarkEnclosing_100KB\|BenchmarkReferences_100KB_50Names' -run '^$' ./internal/symbols/` | `Split_100KB` **< 800 µs/op** (the §8.1 "well under 1ms" clause); `GearScan_1MiB` ≥ 400 MB/s; `Split_1MiB` ≥ 120 MB/s, ≤ 2 allocs/op; `SplitStream_4MiB` ≤ 40 ms/op; `RootHash_1000Chunks` < 40 µs/op; `Run_Bash100KB` < 3 ms; `Run_GoTest` < 1 ms; `Restore_100KB` < 1 ms; `Extract_100KB` < 2 ms; `Enclosing_100KB` < 2 ms; `References_100KB_50Names` < 1 ms |

---

### Group E — SP-05: daemon, IPC, hot path, contract monitor

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **E1** | IPC addressing (named pipe / unix socket, `sun_path` guard) and NDJSON framing | `go test ./internal/ipc/ -run 'TestProjectHash12\|TestResolve\|TestEncodeRequest\|TestDecodeRequest\|TestLineReader'` ; `go test -fuzz FuzzDecodeRequest -fuzztime 60s ./internal/ipc/` | PASS incl. `TestResolveUnixSunPathGuard`, `TestResolveWindowsPipeName`, `TestEncodeRequestByteExact` (golden), `TestLineReaderRejectsOversize`; fuzz clean |
| **E2** | 32-byte hot-path state record | `go test ./internal/ipc/ -run TestState` ; `go test -bench BenchmarkReadState -run '^$' ./internal/ipc/` | `TestStateRoundTrip` (file exactly 32 B), `TestStateBadCRCFallsBack`, `TestStateWriteIsAtomic` under `-race`; `BenchmarkReadState` **< 100 µs/op** |
| **E3** | Thin client: ACK path, NAK→spool, daemon-down spool, **never returns an error** | `go test -race ./internal/ipc/ -run 'TestSend\|TestSpool'` | PASS: `TestSendACKPath`, `TestSendNAKSwitchesToSpool`, `TestSendDaemonDownSpoolsAndReturnsNilError`, **`TestSendNeverReturnsError`** (200 random requests × 4 hostile server behaviours), `TestSendHotSpoolSkipsConnect`, `TestSendModeOffDoesNothing`, `TestSendOversizeExternalizes`, `TestSpoolAppendOnly`, `TestSpoolWriteFailureDropsAndLoudsOnce` |
| **E4** | IPC server: routing, ACK/NAK bytes, panic containment, concurrency, socket permissions | `go test -race ./internal/ipc/ -run TestServer\|TestUnixSocketPermissions\|TestStaleUnixSocket` ; `go test -bench BenchmarkServerRoundTrip -run '^$' ./internal/ipc/` | PASS incl. `TestServerConcurrentClients` (64×50, no race), `TestServerHandlerPanicIsContained`; `BenchmarkServerRoundTrip` p99 < 2 ms |
| **E5** | Daemon lifecycle: singleton lock, stale reclaim, heartbeat, registry, idle exit | `go test -race ./internal/daemon/ -run 'TestAcquireLock\|TestStaleLock\|TestLiveLock\|TestHeartbeat\|TestRegistry\|TestIdleExit\|TestRunReturnsNil'` | PASS as listed in SP-05's test plan |
| **E6** | Ingest: WAL-before-ACK, ring, spill-to-spool, idempotent resumable drain, blob resolution | `go test -race ./internal/daemon/ -run 'TestIngest\|TestDrain\|TestNAKDuplicate'` ; `go test -bench BenchmarkIngestAccept -run '^$' ./internal/daemon/` | PASS: `TestIngestWALIsExactBytes`, `TestIngestRingFullSpillsToSpool`, **`TestIngestACKPrecedesProcessing`**, `TestDrainIsIdempotent`, `TestDrainResumesAfterCancel`, `TestDrainResolvesBlobs`, `TestDrainSurvivesCorruptLine`, `TestNAKDuplicateIsDedupedOnDrain`; `BenchmarkIngestAccept` within **B-B p99 < 2 ms** |
| **E7** | `IdleController` (O3 seam) — priority order, budget, panic isolation, idle detection | `go test ./internal/daemon/ -run TestIdle` | PASS: `TestIdleRunsByPriority`, `TestIdleRespectsBudget`, `TestIdleTaskPanicIsolated`, `TestIsIdleUsesDetectAfterSeconds` |
| **E8** | Hot-path breach detector: `sync` → `spool` after 3 windows, revert after 3 clean | `go test ./internal/daemon/ -run TestBreachDetector\|TestHotModeTransition\|TestSpoolOnBreachFalse` | PASS: `TestBreachDetectorTransitionsAfterThreeWindows`, `TestBreachDetectorResetsOnCleanWindow`, `TestBreachDetectorRevertsAfterThreeCleanWindows`, `TestHotModeTransitionWritesStateAndNAKs` |
| **E9** | Extension seams (`Handle`, `Bind`, late-bound `Services`) and **nil-tolerance** | `go test ./internal/daemon/ -run 'TestServicesAllNil\|TestHandle\|TestBind\|TestSketchSet\|TestConfigReload'` | PASS: **`TestServicesAllNil`** — every op answered without panic with `Services{}`; `TestHandleOverridesDefaultRoute`; `TestBindRunsInOrderAndDeclaresProducers`; **`TestSketchSetNeverWritesTriedBloom`**; `TestConfigReloadDefersChunkChange` |
| **E10** | **Contract monitor (G9.3, §12.1)** — 9 assertions, not-yet-implemented rule, degrade/restore | `go test ./internal/contract/...` | PASS: **`TestFreshBuildReportsModeFull`** with exactly the **four** later-wave assertions (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`) reporting `OK:true, SevInfo, Observed:"not-yet-implemented"`; **`TestDeclaredProducerSetMatchesArchitecture`** still pins the 5/4 split (wave 2 adds no producer); `TestCriticalFailureDegrades`, `TestTwoCleanRunsRestore`, `TestOneCleanRunDoesNotRestore`, `TestPanickingAssertionDoesNotDegrade`, `TestMarkerIsWrittenByFlushAndCheckpointOnly` |
| **E11** | Three-mode degradation state machine enforced at exactly the specified sites | `go test -race ./internal/daemon/ -run 'TestDegradedPassive\|TestModeOff'` | PASS: `TestDegradedPassiveSuppressesActingPaths`, **`TestDegradedPassiveStillRecords`**, `TestModeOffSkipsIngest` |
| **E12** | CLI + hooks: **66 fault-injection combinations all exit 0**; `self-test` is the only non-zero exit | `go test ./test/e2e/ -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit'` | PASS — all 66 combinations exit 0 with valid JSON on stdout |
| **E13** | Daemon end-to-end through the real binary | `go test ./test/e2e/ -run 'TestE2EHookRoundTrip\|TestE2ELazySpawn\|TestE2EIdleExit\|TestE2ESelfTest\|TestE2ESpoolSubmodeEndToEnd'` | PASS as listed |
| **E14** | `obs` budget table B-A…B-F wired to config | `go test ./internal/obs/ -run TestBudgets\|TestCheckBudgets` | `TestBudgetsMatchArchitectureTable`, `TestBudgetALimitFollowsConfig`, `TestCheckBudgetsNeverGatesBD`, `TestCheckBudgetsIgnoresEmptyHistograms` |

---

### Group F — SP-06: content-addressed store, redaction, exact token accounting

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **F1** | **Redaction at the single choke point** (§13 invariant 7) — 10 secret families, idempotence, bounded growth | `go test ./internal/redact/...` ; `go test -fuzz FuzzRedactIdempotent -fuzztime 60s ./internal/redact/` | PASS: `TestRedact_PEMBlock`, `TestRedact_AWSKeys`, `TestRedact_GitHubTokens`, `TestRedact_AnthropicBeforeGeneric`, `TestRedact_JWT`, `TestRedact_BearerValueOnly`, `TestRedact_CredentialedURI`, `TestRedact_AssignmentValueOnly`, `TestRedact_DotenvGatedByKeyName`, `TestRedact_Idempotent`, `TestRedact_BoundedGrowth`, `TestRedact_Rejects{ZeroWidth,Narrow}UserPattern`; fuzz clean |
| **F2** | **Exact chunk-level token accounting (G10.2)** — media sizing, per-project calibration | `go test ./internal/tokens/...` | PASS: `TestClassify_All`, `TestClassify_SP01TableStillPasses`, `TestEstimateImage_PNG` (1049), `TestEstimateImage_Downscale` (clamped to 1600), `TestEstimatePDF_TextPDF` (**not** the flat 2 000 §2.2 indicts), `TestEstimateRoot_CacheHit/CacheMiss/ClassIndependentCache`, `TestEstimateRoot_SP01BaselineStillPasses` (750), `TestChunkCache_Persists`, `TestCalibrate_ClampAndThreshold`, `TestCalibrate_ReadsConfigNotLiterals` |
| **F3** | Object layer: 2-level fanout, zstd, global dedup, corruption quarantine | `go test ./internal/store/ -run 'TestPutBytes_Fanout\|TestPutBytes_Compression\|TestPutBytes_GlobalDedup\|TestPutBytes_IdenticalRoot\|TestGetChunk\|TestOpen_Streams\|TestOpenSpan\|TestHas_NoIO\|TestOpenStore\|TestClosedStore'` | PASS incl. `TestPutBytes_GlobalDedup`, `TestGetChunk_QuarantinesCorruption`, `TestOpenSpan_Boundaries`, `TestOpenStore_TruncatedFinalLine` |
| **F4** | Ingest pipeline order — **redact → canonicalize → chunk** | `go test ./internal/store/ -run 'TestPutBytes_Redaction\|TestPutBytes_Canonicalize\|TestPutBytes_KeepRaw\|TestPutBytes_NearDup\|TestPut_ReaderTruncation\|TestPutBytes_DedupHitReports'` | PASS: **`TestPutBytes_RedactionBeforeChunking`** (no object contains the literal `AKIA`), `TestPutBytes_RedactionRunsWhenDepsRedactIsNil`, `TestPutBytes_CanonicalizeBeforeChunk`, `TestPutBytes_KeepRawStoresDeltaRoot`, `TestPutBytes_NearDup`, `TestPutBytes_DedupHitReportsThisPutsRawBytes` |
| **F5** | `tool_use` index, args digest/preview, supersession record | `go test ./internal/store/ -run 'TestRecordToolUse\|TestToolUsesByPath\|TestMarkSuperseded\|TestArgsDigest'` | PASS incl. `TestMarkSuperseded_AppendOnly` (original line unmodified + one `"op":"supersede"` line), `TestArgsDigest_KeyOrderInvariant` |
| **F6** | File version history and `ChangedSince` (§8.3 staleness input) | `go test ./internal/store/ -run 'TestAppendFileVersion\|TestFileAt\|TestChangedSince\|TestFilesJSON'` | PASS: all five `ChangedSince` cases incl. `TestChangedSince_KeyNormalization` and `TestChangedSince_PreservesInputOrder` |
| **F7** | **Segment log and the encoded-once DPI guard (§4.6, §8.2)** | `go test ./internal/store/ -run TestSegment` ; `go test -bench BenchmarkMarkEncoded_100 -run '^$' ./internal/store/` | PASS: **`TestSegment_MarkEncodedRefusesDifferentSeq`** (`errors.Is(err, core.ErrAlreadyEncoded)`), `TestSegment_MarkEncodedBatchIsAllOrNothing`, `TestSegment_MarkEncodedIdempotentSameSeq`, `TestSegment_FrontierIsContiguous`, `TestSegment_Unencoded`, `TestSegment_Current`, `TestSegment_Range`, `TestSegment_SurvivesReopen`; benchmark ≤ 1 ms |
| **F8** | `Search` (backs future `recall`) — path/text/symbol ranking, determinism, span widening | `go test ./internal/store/ -run TestSearch` ; `go test -bench BenchmarkSearch_1000Roots -run '^$' ./internal/store/` | PASS incl. `TestSearch_BySymbol` (`Span == [812,1052]`), `TestSearch_Deterministic`, `TestSearch_TruncatesWithoutError`; benchmark ≤ 25 ms |
| **F9** | `Stats` / **`DedupRatio` ≥ 4:1 at the store level** / sublinear growth | `go test ./internal/store/ -run 'TestStats\|TestPhase1ExitCriterion_ReadHeavy'` | PASS: `TestStats_DedupRatio`, **`TestPhase1ExitCriterion_ReadHeavy`** (`Stats.DedupRatio ≥ 4.0`), `TestStats_SublinearGrowth` |
| **F10** | GC — deadline-bounded, resumable mark-and-sweep from checkpoints/pins/eliminations roots | `go test ./internal/store/ -run TestGC` ; `go test -bench BenchmarkGC_50kObjects -run '^$' ./internal/store/` | PASS: `TestGC_CollectsUnreferenced`, **`TestGC_ZeroPolicyInheritsConfigAndDeletesNothing`**, `TestGC_RetentionIsWhicheverIsLonger`, `TestGC_EphemeralNotInWindowByAge`, `TestGC_HarvestsHashesFromCheckpointPinsEliminations`, `TestGC_DryRun`, `TestGC_DeadlineTruncatesAndResumes`, `TestGC_TombstonesRootsAppendOnly`, `TestGC_ContextCancel`; benchmark ≤ 2 s, deadline honoured ±50 ms |
| **F11** | Flush, session index, append-only guard over every store file | `go test ./internal/store/ -run 'TestFlush\|TestAppendOnlyGuard_StoreFiles\|TestGolden_IndexFormats'` | PASS; `TestGolden_IndexFormats` reproduces `roots.jsonl`, `tool_use.jsonl`, `segments.jsonl`, `files.json`, `sessions.jsonl` byte-for-byte |
| **F12** | Store property tests + end-to-end secret containment | `go test ./internal/store/ -run Prop` ; `go test ./test/e2e/ -run 'TestE2E_StoreSurvivesProcessRestart\|TestE2E_SecretNeverLandsInObjects'` | PASS: `PropPutGetRoundtrip`, `PropOpenSpanMatchesSlice`, `PropDedupMonotone`, `PropChangedSinceIsExactlyHashInequality`, `PropMarkEncodedNeverDowngrades`; **`TestE2E_SecretNeverLandsInObjects`** — none of the ten secret literals appears in any decompressed object |
| **F13** | SP-06 performance budgets | `go test -bench 'BenchmarkPutBytes_100KB_Cold\|BenchmarkPutBytes_100KB_Warm\|BenchmarkGetChunk\|BenchmarkOpenSpan_4KB_of_4MB\|BenchmarkOpenStore_50kRoots\|BenchmarkEstimateRoot_64Cached' -run '^$' ./internal/store/ ./internal/tokens/` | Cold ≤ 3 ms; Warm ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan` ≤ 150 µs; `Open` 50k roots ≤ 400 ms; `EstimateRoot` 64 cached ≤ 5 µs; `Redact` 100 KB ≤ 2 ms |

---

### Group G — SP-07: dependence DAG and slicing

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **G1** | All nine node kinds and eight edge kinds, stable `NodeID` scheme | `go test ./internal/dag/ -run 'TestKinds\|TestNodeID'` | PASS; `nodeid.json` golden reproduced |
| **G2** | In-memory graph: upsert, dedup, tombstones, concurrency | `go test -race ./internal/dag/ -run 'TestGraph\|TestConcurrent'` | PASS, no race, no lock upgrade |
| **G3** | Position indexes: **`CrossingEdges` = `segment_coupling(p)`** and `NodesAfter` | `go test ./internal/dag/ -run TestIndex` ; `go test -bench 'BenchmarkCrossingEdges' -run '^$' ./internal/dag/` | `CrossingEdges` matches brute force on every `rapid` case and all twelve golden positions; **< 5 µs on 15 000 edges**; `NodesAfter` totally ordered, live-only, freshly allocated |
| **G4** | **Scored backward/forward slicing, thin by default** | `go test ./internal/dag/ -run TestSlice` ; `go test -bench 'BenchmarkBackwardSlice5000\|BenchmarkForwardSlice5000' -run TestSliceLatencyBudget ./internal/dag/` | `Slice.Scores` is `map[NodeID]float32`; `DefaultSliceOptions(config.Defaults()).Thin == true`; **both benchmarks < 1 ms/op** and `TestSliceLatencyBudget` PASS |
| **G5** | **No selection authority** (closing note 3, mechanically enforced) | `go test ./internal/dag/ -run TestNoBooleanKeepAPI -v` | PASS — no exported function returns a keep-set, drop list or `map[NodeID]bool`; the `NO SELECTION AUTHORITY` note is present in `doc.go` |
| **G6** | `deps.jsonl` persistence: byte-exact round-trip, torn tail, corrupt line, idle-only `Compact` | `go test ./internal/dag/ -run 'TestLog\|TestCompact'` | PASS; torn tail and corrupt line both load and are surfaced (`TruncatedTail`, `LoadErrors`, one `Loud`); `Compact` no-op below the 25% waste threshold |
| **G7** | §8.1 item 4 edge builders, **acyclic output** | `go test ./internal/dag/ -run 'TestBuilder'` | PASS incl. **`TestBuilderOutputIsAcyclic`** over 200 built tool uses with parallel siblings |
| **G8** | Thin-vs-full measured comparison | `go test ./internal/dag/ -run TestThinVsFullComparison` | `thin-vs-full.json` reproduced; mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85` across eight seeds |
| **G9** | `dagtest` conformance suite | `go test ./internal/dag/... -run Suite` | Zero `t.Skip`; `RunGraphSuite` passes against `dag.Open` |

---

### Group H — SP-08: L0 observer (**new this wave**)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **H1** | **Addressable tombstone (G3.2, §8.1 item 2)** | `go test ./internal/observer/ -run TestTombstone` ; `go test -bench BenchmarkTombstone -benchmem -run '^$' ./internal/observer/` | PASS: `TestTombstone_DesignExample` renders `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]`; `_NoPathUsesArgsPreview`, `_LongPreviewTruncatedTo48Runes`, `_MegabyteSize`, `_SupersededMarker`, `_EphemeralMarker`, `_BothMarkers`, `_NoSubject`; `TestTombstoneGolden` reproduces `testdata/golden/observer/tombstones.txt`; benchmark **< 2 µs/op** |
| **H2** | Tool classification: `NormalizeToolName`, `IsCompactable` (§2.2 set), supersedable class | `go test ./internal/observer/ -run 'TestNormalizeToolName\|TestIsCompactable\|TestSupersedableClass'` | PASS per the tables (nine §2.2 names compactable; `AgentTool`, `TodoWrite`, MCP tools not) |
| **H3** | **Task-boundary signals (G1.5)**: todo completion, passing test run, git commit | `go test ./internal/observer/ -run 'TestExtractSignals\|TestExtractTestOutcome\|TestPathsFromInput\|TestResponseText'` ; `go test -fuzz FuzzExtractSignals -fuzztime 60s ./internal/observer/` | PASS across the whole table (Go/Jest/Pytest/Cargo pass+fail, `git commit` incl. chained and no-op, `TodoWrite` completion, malformed JSON never panics); fuzz clean |
| **H4** | `OnToolUse` write path — store, index, file version, canon options, oversize truncation, soft failure | `go test -race ./internal/observer/ -run TestOnToolUse` | PASS incl. `TestOnToolUse_StoresAndIndexes`, `_CanonOptionsAlwaysIncludeCRLF`, `_CanonOptionsFromConfig`, `_FileVersionAppendedForFileContent`, `_NoFileVersionForGrep`, `_ArgsDigestAndPreview`, `_MCPResultIsEphemeral`, `_PutFailureIsSoft`, `_IndexFailureStillFeedsSketchesAndDAG`, `_CancelledContext`, `_OversizePayloadTruncated`, `_ReturnsEmptyOutput`, `_ToolUseRingEvictsAndClampsSubagentSince`, **`_ConcurrentSessionsRaceFree`**, `TestModePassiveStillWrites` |
| **H5** | **Supersession / redundancy detection (§8.1 item 3)** | `go test ./internal/observer/ -run 'TestSupersede\|TestIsSuperset\|PropertyIsSuperset'` | PASS across all sixteen rows, incl. `TestSupersede_SupersetChunkSet`, `_NearDuplicateAboveThreshold` / `_BelowThreshold`, `_DifferentClassIgnored`, `_NeverMarksLaterRecord`, `_EphemeralNeitherDirection`, `_LookbackCapped` (limit 32), `_StatusSurvivesReopen` |
| **H6** | DAG emission (§8.1 item 4) — produces/consumes/sequence/shared-file/shared-symbol, monotone `Pos` | `go test ./internal/observer/ -run 'TestGraph_\|TestNodeIDFormats'` | PASS incl. `TestGraph_PosIsMonotoneAndPreIncrement`, `TestGraph_SymbolsCappedAt64`, `TestGraph_SegmentAnchor`, `TestGraph_FlushNotCalledPerToolUse` |
| **H7** | Sketch feeding (§8.1 item 5) **and the Bloom prohibition** | `go test ./internal/observer/ -run TestSketches\|TestObserverNeverFeedsBloom\|TestObserverSourceHasNoBloomReference -v` | PASS: CMS keyed on path and tool, HLL distinct paths, Misra-Gries top-k, ephemeral results not fed, nils tolerated; **`TestObserverNeverFeedsBloom`** (panicking Bloom double survives a 50-event session) and **`TestObserverSourceHasNoBloomReference`** (zero occurrences of `Bloom`/`NewBloom`/`RebuildBloom` in non-test files) |
| **H8** | **Verbatim user capture (G2.3, §8.1 item 7)** + thrash-warning surface | `go test ./internal/observer/ -run 'TestOnUserPrompt\|TestVerbatimPromptID'` | PASS: `_StoresVerbatim` (no canonicalization, no MinHash), `_RecordsIndexEntry` (`prompt_<session>_0`), `_TurnIncrements`, `_DAGNodeAndSegmentEdge`, **`_NeverRegenerated`**, `_EmptyPromptIgnored`, `_GrammarSymbolAppended`, `_ThrashWarningInFullMode`, **`_NoThrashWarningInPassiveMode`**, `_ThrashWarnedOncePerRule`, `_PutFailureStillIncrementsTurn` |
| **H9** | **Subagent capture (G10.1, §8.1 item 8)** — summary + tool-result hashes | `go test ./internal/observer/ -run TestOnStop\|TestTailAssistantText` | PASS incl. `_SubagentCapturesToolHashes`, `_SubagentWindowStartsAtLastPrompt`, `_SummaryFromTranscriptTail`, `_TranscriptMissingIsSilent`, `_ConsumesEdges`, `_CaptureIsDeterministic`, **`_RetrievalPathG10_1`** (round-trips out of a real store) |
| **H10** | BOCD feature emission (§6.6's five features) | `go test ./internal/observer/ -run TestFeatures` | PASS across all thirteen rows; `TestFeatures_AllFinite` property holds; `TestFeatures_RecentRingBounded` == 16 |
| **H11** | `SessionStart` (startup/resume/compact-delegate/clear) and **ordered** `SessionEnd` | `go test ./internal/observer/ -run 'TestOnSessionStart\|TestOnSessionEnd\|TestState_'` | PASS: `_StartupOpensSegment`, `_ResumeReusesOpenSegment`, `_ResumeAdoptsFrontierTurn`, `_CompactDelegates` / `_ClearDelegates` through the `Rehydrator` seam, **`_CompactWithoutRehydratorIsEmpty`** (SP-11 absent at wave 2), `_UnknownSourceTreatedAsStartup`; **`TestOnSessionEnd_Order`** exactly `Segments().Close → Graph.Flush → Store.Flush → sketch.Save ×2 → state write → Store.GC`; `_GCPolicyFromConfig` (`{30,10,false,8s}`), `_GCFailureIsSoft`, **`_NeverWritesTriedBloom`**, `TestState_RoundTrip`, `TestState_CorruptFileRecovers`, `TestState_AtomicWrite` |
| **H12** | Daemon wiring: `WireObserver` registers the five L0 ops, adapts symbols, tolerates nil scheduler | `go test -race ./internal/daemon/ -run 'TestWireObserver\|TestServicesAllNil'` ; `go test ./test/e2e/ -run TestE2E_ObserverThroughDaemon` | Ops `observe.tool`, `observe.prompt`, `observe.stop`, `session.start`, `flush` routed; `o.Sched == nil` path exercised (scheduler is SP-12, wave 3); `Persister` registered on the idle controller at priority 50 |
| **H13** | `observertest` conformance suite | `go test ./internal/observer/... -run Suite` | `RunObserverSuite` passes; **zero `t.Skip`** (Rule W-1) |
| **H14** | Observer end-to-end through the real daemon and binary | `go test ./test/e2e/ -run 'TestE2E_ObserverThroughDaemon\|TestE2E_HooksExitZeroUnderFaultInjection\|TestE2E_SupersessionVisibleAfterRestart\|TestE2E_VerbatimPromptSurvivesRestart'` | PASS: 44 lines in `index/tool_use.jsonl`; `dag/deps.jsonl` non-empty; `sketches/touch.cms` and `explore.hll` exist; **`sketches/tried.bloom` does not exist**; every hook exits 0 with `.qompack/objects` read-only |
| **H15** | **Phase 1 exit criterion** (see also §3 and §5) | `go test ./test/e2e/ -run TestPhase1 -v` | `TestPhase1_DedupRatioReadHeavy` → `DedupRatio ≥ 4.0`; `TestPhase1_CanonicalizationGapOnTestOutput` → `ratioOn ≥ ratioOff*1.25`; `TestPhase1_StoreGrowthSublinear`; `TestPhase1_ReportArtifact`; `TestPhase1_CorpusSweep`; `TestPhase1_HotPathBudgetDocumented` |
| **H16** | Observer hot-path cost inside B-C | `go test -bench 'BenchmarkOnToolUse_FileRead64KB\|BenchmarkOnToolUse_TestOutput256KB' -benchmem -run '^$' ./internal/observer/` | Both within **B-C p99 < 50 ms**; `FileRead64KB` p50 < 5 ms |

---

### Group I — SP-09: negative knowledge (**new this wave**)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **I1** | Approach-class canonicalization (closed stopword/synonym set) | `go test ./internal/negknow/ -run 'TestApproachClass\|TestLemma'` | PASS: `_Table` (8 worked rows), `_SynonymFixpoint`, `_SilentERestore`, `_DoubledConsonantLimitation` (documented limitation pinned), `_Deterministic`, `_Bounded` (≤ 4 tokens, ≤ 24 B each, ≤ 128 B total), `_Empty` → `"unclassified"` |
| **I2** | **Canonical descriptor** `(normalized_path, symbol_or_null, approach_class, reason_hash)` + `MatchKey` | `go test ./internal/negknow/ -run 'TestSplitTarget\|TestDescriptor\|TestMatchKey\|TestKey_\|TestReasonHash'` | PASS: `TestDescriptorKey_Golden` reproduces all 24 rows of `descriptors.golden.json` byte-for-byte; `TestMatchKey_IgnoresReason`; `TestKey_SeparatorInjection`; `TestKey_Length` (32/32); `TestKey_NoAliasing` |
| **I3** | `Record` **is** the §8.5 `eliminated[]` schema slot (G6.1) | `go test ./internal/negknow/ -run TestRecord` | PASS: **`TestRecordJSON_Golden`** equals `record.golden.json` byte-for-byte and its seven §8.5 keys equal the literal transcribed from the design document; `_RoundTrip`, `_NilDependsOn`, `_MissingFields` defaults, `_BadDepHash`, `TestNormalizeRecord_Bounds`, `TestNormalizeRecord_Redact`, `TestRecordID_Stable` |
| **I4** | Append-only `records/eliminations.jsonl` with corrupt/truncated recovery | `go test ./internal/negknow/ -run 'TestReplayLog\|TestAppendOnly\|TestAppendLine'` | PASS: `_Golden`, `_Corrupt` (2 recovered, counters right), `_TruncatedTail`, `_DuplicateAdd`, `_OrphanStale`, **`TestAppendOnly_Enforced`** (`core.ErrAppendOnly` on `O_TRUNC`), `TestAppendLine_NoInteriorNewline` |
| **I5** | **Three-way `already_tried` answer** (absent / active / stale) with scope semantics | `go test ./internal/negknow/ -run TestQuery` | PASS across all twelve rows: `_Absent`, `_Active`, **`_ActiveViaSynonym`**, `_Stale_FlagNote` (`Note == negknow.StaleNote` **including the em dash**), `_Stale_Drop`, `_StaleBeforeRebuild`, **`_BloomOnly`**, `_PrefersActiveOverStale`, `_ScopeSession_OtherSessionHidden`, `_ScopeSession_OtherSessionHidden_NextIdle`, `_ScopeProject_CrossSession`, `_ScopeProject_ExcludesSessionScoped` |
| **I6** | `Record` write path: evidence requirement, identity dedup, re-record after stale, blind mode | `go test -race ./internal/negknow/ -run 'TestRecord_\|TestGetActiveAll\|TestTopActive\|TestAnswerMCPResult\|TestBlindMode\|TestClose\|TestOpenReturns\|TestConcurrentRecordQuery'` | PASS: `_RequireEvidence` (`ErrNoEvidence`, log byte-identical before/after), `_Idempotent`, **`_IdempotentAcrossTimestamps`**, **`_ReRecordAfterStaleIsNotDeduped`**, `_RejectsPresetStale`, `_DescriptorRecomputed`, `TestBlindMode` (never a false positive), `TestOpenReturnsMaintainer`, `TestOpenReturnsObservationSource`, `TestConcurrentRecordQuery` (1 600 records, 0 deduped, no race) |
| **I7** | **Staleness (§8.3, the High-severity §12 risk)** — `depends_on` hashes, flip, single `ChangedSince` call | `go test ./internal/negknow/ -run 'TestRefreshStaleness\|TestMarkStale\|TestRebuildOnStale\|TestMaintenanceTask\|TestOpenRefreshBounded'` | PASS: `_Flips`, `_NoChange`, **`_SingleChangedSinceCall`** (exactly once, deduplicated + sorted, 2 000 deps), `_GroupsByBecause`, `_MultiDep`, `_NilStore`, `_StoreError`; `TestMarkStale_Idempotent`; `TestRebuildOnStale_{Immediate,NextIdle,Never}`; `TestMaintenanceTask_Shape` returns `("negknow.maintain", 30, fn)`; `TestOpenRefreshBounded` returns in < 500 ms |
| **I8** | **Bloom-as-cache (§13 invariant 3)** — rebuilt from active records only, never from a checkpoint | `go test ./internal/negknow/ -run TestRebuildBloom\|TestBloom\|TestHealth -v` | PASS: `_ActiveOnly`, `_ExcludesForeignSession`, **`_NeverFromCheckpoint`** (exactly one `sketch.RebuildBloom` call site in `internal/`, in `negknow/bloom.go`; no import of `internal/checkpoint` anywhere in `internal/negknow`), `_HonoursConfiguredCapacity`, `_Resizes` (FillRatio ≤ 0.5, EstFPRate < 0.02 at 8 000 records), `_Persistence` (one `.bak`, `rebuild_seq == 1`, no `.qompack/tmp` leftovers), `_OneBakGeneration`, `TestBloomLoadFailure_RebuildsFromRecords`, **`TestBloomLoadFailure_NoRecords_NeverFalsePositive`**, `TestBloomUndercount_TriggersRebuild`, `TestBloomOvercount_SchedulesRebuild`, `TestBloomFileSize` ∈ [11 264, 14 336] B, `TestHealth` |
| **I9** | The four §8.3 ingestion sources | `go test ./internal/negknow/ -run 'TestIngest\|TestObserve_'` | PASS: `_MCP_Full`, `_MCP_AutoDeps`, `_MCP_UnknownDepSkipped`, `_MCP_BadScope`, `_MCP_NoStore_RequireEvidence`, `_Pin_EvidenceText` / `_BadEvidenceText`, `_UserStatement_Matches` / `_ApostropheVariants` / `_NoTarget` / `_NoMatch`, `TestObserve_AppendsSignals`, `TestObserve_RingBounded` (512) |
| **I10** | Heuristic detector (source #3): test-fail → revert → different-approach over the **real DAG** | `go test ./internal/negknow/ -run TestDetector` | PASS across all eleven rows incl. `_PatternP` (exact reason string), `_SameClassNoEmit`, `_NoRevertNoEmit`, `_TestPassBreaksPattern`, `_WindowExceeded`, `_DepsFromDAG`, `_NoEvidenceDropped`, **`_DoesNotAppend`** |
| **I11** | `negknowtest` conformance suite and DAG node IDs | `go test ./internal/negknow/... -run 'TestLedgerConformance\|TestNodeIDGolden'` | `RunLedgerSuite` passes; **zero `t.Skip` anywhere in `negknowtest`** (Rule W-1 merge blocker); `node-ids.json` reproduced |
| **I12** | Elimination lifecycle end-to-end + bloom-corruption recovery | `go test ./test/e2e/ -run 'TestE2E_EliminationLifecycle\|TestE2E_BloomCorruptionRecovery'` | Ingest → active → durable across reopen → dependency rewrite → `RefreshStaleness` flips 1 → `AnswerStale` with `StaleNote` → `RebuildBloom` → `Health{Active:0, Stale:1}`; `AssertAppendOnly` passes; exactly one `tried.bloom.*.bak`; nothing written outside `.qompack/`; corruption recovery logs exactly one `Loud` negknow line |
| **I13** | **Phase 2 exit criterion** | `go test ./test/replay/ -run TestPhase2ExitCriterion -v` | `stockRepeats > 0`; `negknowRepeats ≤ 0.75 × stockRepeats`; `negknowRepeats < stockRepeats` on ≥ 8 individual sessions; `staleBlocks == 0` across every `DependencyChangeAt` session with `dependencyChangeSessions > 0`; report JSON logged |
| **I14** | negknow performance budgets | `go test -bench 'BenchmarkQueryHit\|BenchmarkQueryMiss\|BenchmarkRecord\|BenchmarkRebuildBloom\|BenchmarkRefreshStaleness\|BenchmarkOpen\|BenchmarkDetectorScan' -run 'TestBudget_\|TestMemoryFootprint\|TestBloomFileSize' ./internal/negknow/` | `Query` hit p99 < 50 µs; miss p99 < 5 µs; `Record` p99 < 5 ms; `RebuildBloom` (5 000 records) < 50 ms; `RefreshStaleness` ledger-side < 10 ms; `Open` (20 000 lines) < 150 ms; `Detector.Scan` < 5 ms; resident memory at 5 000 records < 4 MB; every `TestBudget_*` PASS |

---

### Group J — repository-wide gates (run last, in the main session)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| **J1** | Whole-tree test run, race-enabled | `go test -race ./...` (Linux/macOS) ; `go test -count=2 ./...` (Windows) | Exit 0, zero failures, zero races |
| **J2** | Coverage floors (§6.4), now binding for every merged package | `go run ./tools/devtool cover` | ≥ 90%: `config`, `paths`, `store`, `sketch`, `chunk`, `canon`, **`negknow`**, plus SP-06's self-imposed 90% on `redact` and `tokens`. ≥ 85%: `dag`, `eval`. ≥ 75%: everything else incl. `ipc`, `daemon`, `contract`, **`observer`**. Still exempt (stubs, per `plans/OWNERS.tsv`): `analyzer`, `scheduler`, `checkpoint`, `pins`, `rehydrate`, `rules`, `skills`, `mcp`, `commands`, `grammar` |
| **J3** | Security posture (D10, §13 invariant 7) | `go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...` ; `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps` | Zero vulnerabilities; zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` (unix only); `os/exec` only in `internal/daemon`, `internal/cli`, `internal/testutil`, `tools/` |
| **J4** | Placeholder scan across every implemented package | `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented\|handle edge cases' -- internal/ test/ tools/ ':!*_test.go'` (PowerShell: `git grep -nE ... \| Select-String ...`) | No output. `core.ErrNotImplemented` appears **only** in the stub packages listed under J2's exemption line |
| **J5** | Full CI on `verify/v3` | push `verify/v3`, watch GitHub Actions | All nine jobs green: `verify`, `test` (ubuntu/macos/windows), `cover`, `crossbuild`, **`bench-gate`** (required), **`replay-gate`** (required), `plugin-validate`, `security`, `docs` |
| **J6** | `Qompack.md` immutability | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | Empty |

---

## 3. Exit-criteria re-verification

Each completed subplan's exit criteria, quoted, with the concrete measurement procedure. A quoted
criterion that cannot be measured is a checkpoint failure, not a documentation problem.

### 3.1 SP-01 — foundation

> SP-01 precedes Phase 0, so no phase exit criterion applies to it directly.

The guardrails it had to make *expressible* (`Qompack.md` §11.3), quoted:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Procedure.** Run `go test ./internal/obs/ -run TestBudgets_AllSixPresentAndConfigDriven -v` and
confirm B-A…B-F exist with B-A's limit sourced from `runtime.hotPath.budgetMs`. Confirm
`store.Stats.DedupRatio` exists (F9). Confirm the 2% rule is enforced (B10). Confirm `--phase 2`
runs three phase checks (B11). SP-01's own Definition of Done items 2–18 are re-run as inventory
items A1–A17; item 1 (commit count on the SP-01 branch) is historical and is re-verified only as
`git rev-list --count <root>..<sp01-merge-base>` = 7.

### 3.2 SP-02 — replay harness / Belady / Phase 0

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

**Procedure.** (a) `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline
--out $env:TEMP/v3-baseline-a.json` twice into two different output paths and byte-compare — they
must be identical. (b) Confirm `testdata/baseline/phase0.json` is unchanged by the run
(`git diff --exit-code -- testdata/baseline/`). (c) Confirm `sessions == 24 ≥ eval.minSessions
(20)` and `corpusTier == "synthetic"`. (d) Confirm `docs/adr/0002-replay-methodology.md` still
states that the committed number is synthetic-corpus and names the recorded-corpus command and its
owner — the §10 "real sessions" reading is *discharged by documentation plus a documented command*,
not silently substituted.

> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Procedure.** Growth: **now measured against the real store**, not the wave-1 fixture — see
integration test X6. 2% rule: `go test ./test/replay/ -run 'TestGate_TwoPercent|TestGate_SignOff'`.
Phase gate: B11 with `--phase 2`.

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

**Procedure.** `go test ./internal/eval/ -run 'TestBelady|TestOraclePolicy_ScoresExactlyOne|TestScoreRun_FractionIsMicroAveraged'`.
`oracle` must score exactly `1.0` on all 24 sessions and `null` exactly `0.0`; `stock` strictly
between. If `stock` ties `null`, the corpus is wrong — fix the corpus, never the assertion.

### 3.3 SP-03 — sketch library

SP-03 owns no phase exit criterion; it must not obstruct two, quoted:

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change.

**Procedure.** Its three concrete contributions are measured directly:
`BenchmarkL0SketchUpdate` **≤ 5 µs, 0 allocs** (C9) is the sketch share of the 15 ms hook budget;
`TestMinHash_OneNewFailure` (C6) is the near-dup capability the 4:1 ratio depends on;
`RebuildBloom` from an arbitrary `iter.Seq` at a **different** capacity in ≤ 15 ms (C2, C9) is what
makes "zero stale-block incidents" mechanically achievable. Plus the §11.4 watch-for:
`TestBloom_EstimatedFPRateMatchesEmpirical` must show empirical FP ∈ [0.008, 0.013].

### 3.4 SP-04 — chunking / canonicalization / symbols

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms.

SP-04 owns exactly the *"measure with and without canonicalization"* clause.

**Procedure.** `go test ./test/dedup/...` (D6). `testdata/canon-dedup-report.json` must be
reproduced byte-for-byte without `-write-report`, with `testrunner` group `gain ≥ 1.25` and overall
`gain ≥ 1.0`.

> FastCDC over 100KB is well under 1ms

**Procedure.** `BenchmarkSplit_100KB` < 800 µs/op (D7).

> boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

**Procedure.** D2 (distributional form: ≤ 2 novel chunks in ≥ 85% of trials, ≤ 3 in ≥ 95%, ≤ 12
always), plus `TestSplit_GoldenBoundaries` green on ubuntu **and** macos **and** windows in the CI
`test` matrix, plus `TestPropSizeBounds`.

> `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**Procedure.** D4, all three property tests plus both fuzz targets at 120 s.

### 3.5 SP-05 — daemon, IPC, hot path

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. … If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

> **Exit criterion:** … hook p99 < 15ms. (SP-05 owns and satisfies the second clause)

**Procedure.** This is now measured **with the observer wired in**, which is the material change at
V3:

```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v3-bench.json
```

Required: **B-A p99 < 15 ms**, **B-B p99 < 2 ms**, **B-E p99 < 2 s**, on ubuntu-latest,
macos-latest and windows-latest via the `bench-gate` job. B-D is reported, never gated. The JSON
must carry `spawn_floor_ms` and `b_a_method`. The degradation half of the clause is verified by E8
(`sync` → `spool` after three breach windows, reverting after three clean ones).

> Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently.

**Procedure.** E10 + E11. Additionally re-run `TestDeclaredProducerSetMatchesArchitecture`: wave 2
adds **no** new contract producer, so the split must still be five declared / four
`not-yet-implemented`.

> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

**Procedure.** E11 + E12 + integration test X8.

### 3.6 SP-06 — content-addressed store

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions …

**Procedure.** `TestPhase1ExitCriterion_ReadHeavy` (F9) at the store level, and
`TestPhase1_DedupRatioReadHeavy` (H15) at the observer level. Both must pass; the observer-level
number is the authoritative Phase-1 figure because it is the one produced by the real ingest path.
Record both.

> - Store growth sublinear in session length after dedup

**Procedure.** `TestStats_SublinearGrowth` (F9) plus X6.

> **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint.

**Procedure.** `TestSegment_MarkEncodedRefusesDifferentSeq` and
`TestSegment_MarkEncodedBatchIsAllOrNothing` (F7), plus integration test X7 which drives the guard
from **observer-produced** segments rather than hand-built ones.

### 3.7 SP-07 — dependence DAG and slicing

> This is graph reachability: BFS over a few thousand nodes, sub-millisecond. (§6.4)

**Procedure.** `BenchmarkBackwardSlice5000` and `BenchmarkForwardSlice5000` **< 1 ms/op**, enforced
by `TestSliceLatencyBudget` (G4).

> Thin slicing drops control-dependence-only edges for much smaller slices at the cost of soundness

**Procedure.** `TestThinVsFullComparison` (G8): mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`,
`thin-vs-full.json` reproduced.

> Output: a relevance score per node, not a binary keep/drop

**Procedure.** `TestNoBooleanKeepAPI` (G5) — a `go/parser` scan, so it cannot be satisfied by
convention.

> `segment_coupling(p)` is the count of DAG edges crossing `p`

**Procedure.** `CrossingEdges` matches brute force on every `rapid` case and all twelve golden
positions, < 5 µs on 15 000 edges (G3).

> Do not ship slicing or submodular selection before p-selection. (Closing note 3)

**Procedure.** `TestGuard_SubmodularInertWithoutPSelection` and
`TestGuard_SelectorRefusesWithoutPSelection` (A14). At wave 2 `scheduler.PSelectionAvailable()` is
still `false`, so the second guard must **still be active** (not skipped).

### 3.8 SP-08 — L0 observer (**closes Phase 1**)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

**Procedure.**
1. `go test ./test/e2e/ -run TestPhase1_DedupRatioReadHeavy -v` — drives every `ToolCall` of
   `eval.Synthesize(0x51080001, readHeavy)` through `observer.OnToolUse` against a real store on
   `t.TempDir()` with a `FakeClock` advancing 1 s per event. Assert
   `store.Stats().DedupRatio ≥ 4.0`, where the ratio is `RawBytes / Bytes` per §5.8. Record the
   number.
2. `go test ./test/e2e/ -run TestPhase1_CanonicalizationGapOnTestOutput -v` — run
   `eval.Synthesize(0x51080002, testOutputHeavy)` twice, `store.canonicalize.enabled` true then
   false. Assert `ratioOn ≥ ratioOff × 1.25`. Record **both** raw numbers and confirm they match
   the figures recorded in `docs/adr/0008-observer-l0.md`.
3. Hook p99: the `bench-gate` run of §3.5, now with a non-trivial handler behind it. Record B-A p99
   for all three platforms and confirm `TestPhase1_HotPathBudgetDocumented` finds them in the ADR.

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup

**Procedure.** As above, plus `TestPhase1_StoreGrowthSublinear` (bytes stored over the second half
of the read-heavy session strictly less than over the first half).

**Gap closure re-verified.** G3.2 → H1; G2.3 → H8 + `TestE2E_VerbatimPromptSurvivesRestart`;
G10.1 → H9 `TestOnStop_RetrievalPathG10_1`; G1.5 → H3 + `WireObserver`'s `OnSignals` path (H12).

### 3.9 SP-09 — negative knowledge (**closes Phase 2**)

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

**Procedure.** `go test ./test/replay/ -run TestPhase2ExitCriterion -v`. Assert, and record from the
emitted `phase2-negknow.json`:
- `stock_repeats > 0` (guards a vacuous pass on a corpus with no eliminations)
- `negknow_repeats ≤ 0.75 × stock_repeats` — the ≥ 25% reduction that operationalizes "measurable
  reduction"
- `negknow_repeats < stock_repeats` on **at least 8** individual sessions
- `stale_blocks == 0` across every session with a non-empty `DependencyChangeAt`, and
  `dependency_change_sessions > 0`

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

**Procedure.** `Health()` must report `FillRatio` and `EstFPRate`; `TestRebuildBloom_Resizes` proves
`FillRatio ≤ 0.5` and `EstFPRate < 0.02` at 8 000 active records (I8). The replay gate's hard
ceiling (`EstFPRate > 0.10` fails outright) is exercised by `TestGate_BloomFPCeiling` (B10), and the
`--sketch` input at B11 must now be produced from the **real** ledger — see X5.

> **The bloom filter is a cache, never the source of truth (§8.3).** Every membership answer is backed by a record lookup or explicitly flagged `BloomOnly`.

**Procedure.** `TestQuery_BloomOnly`, `TestBloomLoadFailure_NoRecords_NeverFalsePositive`,
`TestRebuildBloom_NeverFromCheckpoint` (I5, I8). Additionally, grep-assert by hand:
`git grep -n "sketch.RebuildBloom" -- internal/ ':!*_test.go' ':!internal/sketch/*'` must return
**exactly one** line, in `internal/negknow/bloom.go`.

---

## 4. Prior-wave invariants that must still hold

These are not new tests; they are the §13 invariants, re-asserted now that live code exercises
them. Each maps to an inventory item.

| Invariant (§13) | Re-asserted by |
|---|---|
| 1. Never compress a compression | F7 (`MarkEncoded` DPI guard), I8 (`RebuildBloom` never from a checkpoint), X7 |
| 2. Append-only means append-only | A7, C7, F11, I4, X9 |
| 3. The bloom filter is a cache, never the source of truth | I5, I8, X5 |
| 4. Nothing scattered before `p` | A14 (`TestGuard_SubmodularInertWithoutPSelection`) |
| 5. No code snippets in checkpoints | N/A at wave 2 — `checkpoint` is a stub; the guard test must still compile and skip with the W-1 message (A12) |
| 6. Hooks exit 0. Always. | A11, E12, H14 (`TestE2E_HooksExitZeroUnderFaultInjection`), X8 |
| 7. No network. No telemetry. No writes outside `.qompack/` | A14, J3, X9 |
| 8. Every constant §12 says might change is a config key | A4 (`nomagic`) |
| 9. Every latency budget is measured, not assumed | §6 in full |
| 10. Degradation is loud | E10, E11, I8 (corruption → exactly one `Loud`), X8 |

---

## 5. New cross-component integration tests

These tests only make sense now that SP-08 and SP-09 coexist with waves 0–1. **They are authored
during this checkpoint and become a permanent part of the suite** — they are committed on
`verify/v3` and merge into `develop` with it. Write them in the main session, not in a subagent.

**Placement.** New file `test/e2e/v3_integration_test.go` unless a row names another file.
`test/e2e` is a composition root (§3.2) and may import anything. Use `testutil.NewProject(t)`,
`testutil.FakeClock`, `testify/require`. No wall-clock sleeps.

**Rule.** No test below may reference `internal/checkpoint`, `internal/rehydrate`,
`internal/scheduler`, `internal/mcp`, `internal/commands`, `internal/analyzer` or
`internal/grammar` behaviour. Where a wave-3 component would normally sit in the flow, the test
composes the seam **itself**, in the test file, and asserts that the two halves fit.

---

### X1 — `TestV3_HookEventToTombstoneToRetrievalRoundTrip`

**Seams:** `cli` → `ipc` → `daemon` → `observer` → `redact` → `canon` → `chunk` → `store` →
`observer.Tombstone` → `store.Open`. (SP-05 + SP-08 + SP-06 + SP-04 + SP-03)

**Setup.** `testutil.NewProject(t)` with `src/auth.ts` (24 KB TypeScript, CRLF line endings, one
`@@SEC_ANTHROPIC_AB@@` literal on line 40, one `2026-08-11T09:14:22.318Z` timestamp).
Build the real binary; start a real daemon via `qompack session-start`.

**Inputs.**
1. `qompack observe tool` on stdin with a `PostToolUse` payload: `tool_name: "Read"`,
   `tool_use_id: "toolu_v3_001"`, `tool_input: {"file_path":"src/auth.ts"}`,
   `tool_response: <the 24 KB file bytes>`.
2. `qompack flush`.

**Expected outputs.**
- Hook exit code 0; stdout parses as `hookio.Output` and deep-equals `hookio.Empty()`.
- `store.ToolUse(ctx, "toolu_v3_001")` returns a record with `Tool == "FileRead"`,
  `Path == "src/auth.ts"` (`paths.Key` form), non-zero `Root`, `Tokens > 0`, `Status == StatusOK`.
- `observer.Tombstone(rec)` renders exactly
  `[cleared: sha256:<12hex>… · 24.0KB · FileRead src/auth.ts · re-expandable]` — assert against the
  regexp `^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$`.
  **This is the G3.2 round trip: the tombstone's hash is the retrieval key.**
- `core.ParseHash` of the hash embedded in the tombstone's short form matches `rec.Root`'s
  `Short()`.
- `io.ReadAll(store.Open(ctx, rec.Root))` returns bytes that (a) contain **no** `sk-ant-` literal
  (redaction ran first), (b) contain `«redacted:anthropic_key»`, (c) contain `<ts>` not the ISO
  timestamp (canonicalization ran second), (d) contain only `\n` line endings (crlf canonicalizer),
  and (e) are byte-identical to
  `canon.Default(cfg).Run("FileRead","src/auth.ts", redact.New(cfg).Redact(original)).Canonical`.
- Walking `.qompack/objects/**` and decompressing every object yields no occurrence of `sk-ant-`.
- `store.OpenSpan(ctx, rec.Root, off, n)` for the span returned by
  `symbols.New().Enclosing("src/auth.ts", canonical, offsetOfRefreshToken)` returns exactly that
  function's bytes — the minimum-sufficient-span contract that SP-13 will consume in wave 3.

---

### X2 — `TestV3_ObserverFileVersionsDriveEliminationStaleness`

**Seams:** `observer` (file version history) → `store.ChangedSince` → `negknow.RefreshStaleness` →
`negknow.RebuildBloom` → `negknow.Query`. (SP-08 + SP-06 + SP-09 + SP-03)

**This is the seam the design calls out as the High-severity risk** (§12: *"Stale negative knowledge
blocks a now-viable approach"*), and wave 2 is the first point at which both ends of it exist.

**Setup.** `testutil.NewProject(t)` containing `src/auth.ts`, `docker-compose.yml` (v1),
`package-lock.json`. Real `store.Open`, real `dag.Open`, real `negknow.Open` with a real
`sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)`. Real
`observer.New(...)` over the same store and graph. `eliminations.rebuildOnStale = "nextIdle"`.

**Inputs.**
1. Drive `observer.OnToolUse` with a `Read` of `docker-compose.yml` v1 and a `Read` of
   `package-lock.json` — the observer, not the test, writes the file versions.
2. `led.(negknow.Maintainer).IngestMCP(ctx, MCPArgs{Target:"src/auth.ts:refreshToken",
   Approach:"widen pool timeout",
   Reason:"pgbouncer 1.18 ignores it in transaction mode", Scope:"project",
   DependsOn:["docker-compose.yml","package-lock.json"]})`.
3. `led.Query(ctx, "src/auth.ts:refreshToken", "Increasing the connection-pool timeouts",
   ScopeProject)`.
4. Drive `observer.OnToolUse` with a `Write` of `docker-compose.yml` **v2** (different bytes).
5. `led.RefreshStaleness(ctx, st)`.
6. `led.Query(...)` again with the same synonym phrasing.
7. `led.RebuildBloom(ctx)`.
8. `led.Query(...)` a third time.

**Expected outputs.**
- Step 2: record created; `DependsOn` has exactly two `core.Dep`s, sorted by path, whose `Hash`
  values equal the **roots the observer wrote**, verified by comparing against
  `store.FileHistory(ctx, "docker-compose.yml")[0].Root` and the equivalent for
  `package-lock.json`. (If they differ, the observer and the ledger disagree about `paths.Key`
  normalization — that is the bug this test exists to find.)
- Step 3: `Answer{State: AnswerActive, BloomOnly: false}`, `Record.Reason` returned verbatim,
  `Note == ""`. The synonym phrasing must resolve through `ApproachClass`.
- Step 5: exactly one ID returned; `store.ChangedSince` called exactly once (assert via an
  `obs.Registry` counter or a thin wrapper); `StaleBecause[0]` names `docker-compose.yml` and the
  prior hash.
- Step 6: `Answer{State: AnswerStale}`, `Note == negknow.StaleNote` compared against the §8.3
  literal **including the em dash**, `Record` still returned. **Zero stale-block incidents means
  this answer is `stale`, never `active`.**
- Step 7: `tried.bloom` replaced; exactly one `tried.bloom.*.bak`; `Health{Records:1, Active:0,
  Stale:1}`; `FillRatio` in `(0, 0.5]`.
- Step 8: still `AnswerStale`, `BloomOnly == false`.
- `p.AssertAppendOnly(t)` passes; no file written outside `.qompack/`.

---

### X3 — `TestV3_ObserverDagDrivesHeuristicEliminationDetector`

**Seams:** `observer` (DAG edges + signals) → `dag.Graph` → `negknow.Detector`. (SP-08 + SP-07 +
SP-09)

**Setup.** Real store, real `dag.Open`, real ledger, real observer over all three. The test wires
the observer's `OnSignals` callback into `led.(Maintainer).Observe(...)` **inside the test file** —
that composition root does not exist in production at wave 2 (the observer deliberately does not
import `negknow`), and this test's job is to prove the two halves fit so SP-12's daemon wiring in
wave 3 is a wiring change and not a redesign.

**Inputs**, driven through `observer.OnToolUse` in order, with `FakeClock` advancing 30 s per event:
1. `Edit src/db.ts` with `tool_input {"file_path":"src/db.ts","new_string":"pool.timeout = 30000"}`
   (turn 4). Test maps it to `Observation{Kind: ObsEdit, Path:"src/db.ts",
   Detail:"widen pool timeout", Turn: 4}`.
2. `Bash go test ./internal/db/...` with a failing response containing `--- FAIL: TestPool` (turn
   5). `observer.ExtractTestOutcome` must return `TestFail`; test maps it to
   `Observation{Kind: ObsTestFail, Path:"src/db.ts", Root: <the stored root of that output>}`.
3. `Bash git checkout -- src/db.ts` (turn 6) → `Observation{Kind: ObsRevert, Path:"src/db.ts"}`.
4. `Edit src/db.ts` with `"disable connection pooling"` (turn 7) → `Observation{Kind: ObsEdit,
   Detail:"disable connection pooling"}`.
5. `negknow.NewDetector(led.(ObservationSource), sess, cfg, clk).Scan(ctx, g, 0)`.

**Expected outputs.**
- The DAG built by the observer contains, without any hand-added nodes: `tooluse:*` and
  `toolresult:*` for all four calls, `EdgeProduces` between each pair, `EdgeConsumes`/`EdgeProduces`
  to `file:src/db.ts`, and `EdgeSharedFile` from the later `src/db.ts` tool use to the earlier.
- `Scan` returns exactly **one** `Record`: `Target == "src/db.ts"`,
  `Approach == "widen pool timeout"`, `Source == SourceHeuristic`, `Evidence` equal to the stored
  root of the failing test output (proving the evidence hash came from the observer's store write,
  not from a synthetic value), `DependsOn` derived from the DAG's `EdgeConsumes` targets sorted by
  path.
- `Scan` **does not append** — `led.Health().Records == 0` afterwards; a subsequent
  `led.Record(ctx, r)` makes it 1.
- Repeat the whole sequence with step 2's response replaced by a **passing** test output: `Scan`
  returns zero records (`TestPass` breaks the pattern).

---

### X4 — `TestV3_VerbatimPromptBecomesEliminationEvidence`

**Seams:** `observer.OnUserPrompt` (verbatim, G2.3) → `store` → `negknow.IngestUserStatement`.
(SP-08 + SP-06 + SP-09)

**Setup.** Real store, real ledger, real observer.

**Inputs.**
1. `observer.OnUserPrompt` with `Prompt: "fix the pgbouncer 1.18 pool bypass"` (turn 0).
2. `observer.OnToolUse` with an `Edit` of `src/db.ts`.
3. `observer.OnUserPrompt` with `Prompt: "That didn’t work — the pool is still saturated."`
   (curly apostrophe) at turn 2.
4. Read back the stored root of the turn-2 prompt via
   `store.ToolUse(ctx, observer.VerbatimPromptID(sess, 2))`.
5. `led.(Maintainer).IngestUserStatement(ctx, UserStatement{Prompt: <the turn-2 text>, Turn: 2,
   Path: "src/db.ts", Approach: "widen pool timeout", PromptRoot: <that root>})`.

**Expected outputs.**
- Step 4: `io.ReadAll(store.Open(ctx, root))` equals the prompt **byte for byte**, curly apostrophe
  intact, with `Canon.Strip == nil` and MinHash disabled — the verbatim guarantee of G2.3 survives
  the store's ingest pipeline.
- Step 5: exactly one `Record`, `Source == SourceUserStatement`, `Reason` prefixed
  `"user stated: "`, `Evidence == PromptRoot`.
- `led.Query(ctx, "src/db.ts", "widen pool timeout", ScopeSession)` → `AnswerActive`.
- Re-run step 5 with `Path == ""` and `Symbol == ""`: zero records, counter
  `negknow.user_statement.unresolved == 1`.
- Re-run step 5 with `Prompt: "looks good, ship it"`: zero records, no counter movement.

---

### X5 — `TestV3_ReplayGateConsumesRealSketchHealth`

**Seams:** `negknow.Health` → `eval.SketchHealth` → `test/replay` `--sketch` watch-for.
(SP-09 + SP-02 + SP-03)

**Why now.** At wave 1 the gate read the fixture
`testdata/golden/contracts/negknow/health.json` (`fillRatio 0.18`, `estFPRate 0.006`). Rule W-2:
the wave's verification checkpoint re-runs the fixture-backed test against the **real**
implementation, and a fixture the real implementation cannot reproduce is a verification failure.

**Setup.** `testutil.NewProject(t)`; real ledger; ingest **3 000** active eliminations generated
from a fixed seed (distinct targets, so none is deduped) at the Appendix C defaults
(`capacity 10000, fpRate 0.01`); `RebuildBloom`.

**Inputs.** Marshal `led.Health()` into the `eval.SketchHealth` shape and write it to
`$TMP/v3-health.json`. Run the driver with `--sketch $TMP/v3-health.json`.

**Expected outputs.**
- `Health.FillRatio ≤ 0.5`; `Health.EstFPRate < 0.02`; `NeedsResize == false`.
- The gate emits `bloom_fp_rate` and `bloom_fill_ratio` and does **not** trip the hard ceiling
  (`EstFPRate > 0.10`).
- The real numbers are within the fixture's declared shape (same keys, same types). If the real
  `EstFPRate` differs materially from the fixture's `0.006`, **update the fixture on `verify/v3`
  with a commit that says why** — the fixture was a shape contract, and this is the wave-2 moment
  the architecture says to reconcile it.
- Negative control: build a second ledger with 30 000 active records against a **pinned** capacity
  of 10 000 with resizing disabled, emit its health, and assert the gate **fails** with the §11.4
  sentence in the message.

---

### X6 — `TestV3_ReplayGrowthGuardrailUsesRealStore`

**Seams:** `observer` → `store.Stats` → `eval.CheckSublinearGrowth` → `test/replay` `--growth`.
(SP-08 + SP-06 + SP-02)

**Why now.** SP-02 shipped `testdata/golden/contracts/store/stats-growth.json` (8 samples,
α ≈ 0.62) as a W-2 fixture and documented a provider seam so SP-06 could swap in a real source.
Wave 2 is the first point where a real *session* can be driven end to end, so this checkpoint
performs the swap and proves the guardrail is measured, not fixtured.

**Setup.** Real store; real observer; `eval.Synthesize(0x51080001, readHeavy)` (400 turns).

**Inputs.** Drive every tool call through `observer.OnToolUse`, sampling `store.Stats(ctx)` after
turns 25, 50, 75, 100, 150, 200, 300, 400 into `[]eval.StatsSample{Turn, RawBytes, Bytes}`. Feed
the slice to `eval.CheckSublinearGrowth`, then write it to `$TMP/v3-growth.json` and pass it to the
driver as `--growth`.

**Expected outputs.**
- `RawBytes` is non-decreasing in `Turn` (otherwise the result is `inconclusive`, which fails).
- `RawSpan ≥ 8×`; at least 6 usable samples.
- `Sublinear == true` with `Exponent ≤ 0.95`. Record the exponent.
- The driver run exits 0 with the real growth file.
- Negative control: feed a synthetic linear series (`Bytes == RawBytes`) and assert
  `Sublinear == false` and driver exit non-zero.

---

### X7 — `TestV3_ObserverSegmentsRespectDPIGuard`

**Seams:** `observer` (segment lifecycle) → `store.SegmentLog` DPI guard. (SP-08 + SP-06)

**Why now.** SP-08 opens a segment at session start and closes it at session end; SP-06 owns
`MarkEncoded`. Until wave 2 the guard could only be tested against hand-built segments. SP-12 will
close segments on changepoints and SP-10 will call `MarkEncoded` — neither exists yet, so this test
plays the checkpointer's role from the test file and asserts the guard holds against **real**
observer-produced segments.

**Setup.** Real store, real observer, `FakeClock`.

**Inputs.**
1. `observer.OnSessionStart` (`source: "startup"`) → a segment is opened.
2. 40 mixed tool uses and 3 prompts.
3. `observer.OnSessionEnd` → the segment is closed with `endTurn == st.Turn` and a 5-key feature
   map.
4. `segs.MarkEncoded(ctx, []core.SegmentID{id}, 7)`.
5. `segs.MarkEncoded(ctx, []core.SegmentID{id}, 7)` again.
6. `segs.MarkEncoded(ctx, []core.SegmentID{id}, 8)`.
7. `segs.Frontier(ctx, sess)` and `segs.Unencoded(ctx, sess)`.

**Expected outputs.**
- Step 3: `Segment.Closed == true`; `EndTurn` equals the observer's final turn; `Features` has
  exactly the five §6.6 keys.
- Step 4: nil error; `EncodedOnce == true`; `CheckpointSeq == 7`.
- Step 5: nil error (idempotent for the same seq); `segments.jsonl` gains **no** second
  `"op":"encode"` line.
- Step 6: **`errors.Is(err, core.ErrAlreadyEncoded)`** — the DPI guard of §4.6, now proven over the
  live write path.
- Step 7: `Frontier` equals the segment's `EndTurn`; `Unencoded` is empty.
- Reopen the store from disk and repeat step 6: still `ErrAlreadyEncoded` (the guard is durable,
  not in-memory).

---

### X8 — `TestV3_DegradedPassiveStillRecordsEverything`

**Seams:** `contract.Monitor` → `daemon` mode enforcement → `observer` → `negknow`.
(SP-05 + SP-08 + SP-09)

**Why now.** §12.1: in `ModeDegradedPassive`, *"L0 and L1 keep running (observe, chunk, store,
sketches, DAG, verbatim capture, elimination records — the store stays correct and the session's
data is not lost). Everything that acts is off."* Wave 2 is the first wave in which both the
recording half and the elimination-record half exist, so the clause becomes testable in full.

**Setup.** Real daemon over a real store, graph, ledger and observer. Force
`contract.Monitor.Degrade("v3 test", …)`.

**Inputs.** 40 `observe.tool`, 3 `observe.prompt` (one of which would trigger a thrash warning if
grammar were real — at wave 2 grammar is a stub, so assert only that no `additionalContext` is
emitted), 1 `observe.stop --subagent`, 2 `IngestMCP` eliminations, `flush`.

**Expected outputs.**
- Every hook exits 0.
- `index/tool_use.jsonl` has 44 lines; `dag/deps.jsonl` non-empty; `sketches/touch.cms` and
  `explore.hll` written; `records/eliminations.jsonl` has 2 `add` lines; `tried.bloom` written by
  the **ledger** (and only by the ledger).
- **Every `hookio.Output.HookSpecificOutput` is nil** — no `additionalContext`, no
  `customInstructions`.
- Idle tasks with an `act.` prefix do not appear in `IdleController.RunOnce`'s `ran`; `drain`,
  `sketches`, `metrics` and `negknow.maintain` **do**.
- `LOUD.log` contains exactly one degradation line; `state/contract.json` carries the reason and
  `DegradedSince`.
- Restore path: run two clean `RunAll` cycles → `ModeFull`, with a `Loud` restore line. Assert the
  observer's `Mode()` adapter flips back to `observer.ModeFull`.

---

### X9 — `TestV3_LiveSessionWriteSetAndAppendOnly`

**Seams:** everything. (§13 invariants 2 and 7, over the whole live pipeline)

**Setup.** `testutil.NewProject(t)` inside a temp `HOME`. Snapshot the entire filesystem
(project tree + `HOME`) before.

**Inputs.** A 200-event session driven through the **real binary and real daemon**: session-start,
160 `observe tool` (mixed `Read`/`Grep`/`Bash`/`Edit`/`Write`/`WebFetch`), 12 `observe prompt`,
4 `observe stop --subagent`, 20 MCP-shaped ephemeral tool results, 6 `IngestMCP` eliminations, one
`RefreshStaleness` + `RebuildBloom` cycle, `flush`.

**Expected outputs.**
- Every created or modified path is under `<projectRoot>/.qompack/` or `<home>/.qompack/`. Nothing
  else on disk changed.
- `p.AssertAppendOnly(t)` passes. Then attempt, and require failure with `core.ErrAppendOnly` or
  `os.ErrExist`, each of: `O_TRUNC` on `index/tool_use.jsonl`, `index/roots.jsonl`,
  `index/segments.jsonl`, `dag/deps.jsonl`, `records/eliminations.jsonl`, `spool/*.ndjson`;
  `WriteAtomic` onto `sketches/tried.bloom`; `CreateNew` twice on the same checkpoint seq.
- `sketches/tried.bloom` was written **only** through `negknow.RebuildBloom` — assert exactly one
  `.bak` generation and that the file's mtime is later than the `RebuildBloom` call and earlier
  than nothing else.
- Zero network syscalls: re-run `J3`'s import assertions over the built binary.
- `store.GC` ran on `flush` with the configured policy and deleted nothing (retention window covers
  everything).

---

### X10 — `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently`

**Seams:** `ipc` spool + daemon WAL → `daemon.Drain` → `observer` → `store`/`dag`/`sketch`, with the
ledger open concurrently. (SP-05 + SP-08 + SP-06 + SP-09)

**Setup.** Real daemon; real ledger open on the same project.

**Inputs.**
1. Drive 50 `observe.tool` events. Kill the daemon **without** `flush` (SIGKILL / `Process.Kill`)
   after event 30, so events 31–50 land in the client spool and the WAL holds events 1–30.
2. Ingest 3 eliminations through the ledger before the kill.
3. Restart the daemon; let it drain on start.

**Expected outputs.**
- After drain: `index/tool_use.jsonl` has exactly 50 records, no duplicates
  (`TestNAKDuplicateIsDedupedOnDrain`'s dedup extends across the crash), and every `tool_use_id` is
  unique.
- `store.Stats().Objects` equals a control run that never crashed (drive the same 50 events into a
  fresh project with no crash and compare).
- The ledger's `records/eliminations.jsonl` still has 3 `add` lines; `Open` recovers all 3;
  `Query` answers `AnswerActive` for each.
- `dag/deps.jsonl` loads with `TruncatedTail` handled and `LoadErrors == 0` (or, if the kill landed
  mid-line, `TruncatedTail == true` with exactly one `Loud` and every prior edge intact).
- Both spool files are deleted after the drain.

---

### X11 — `TestV3_HotPathUnchangedWithLedgerResident`

**Seams:** `ipc` client → daemon with observer **and** ledger resident. (SP-05 + SP-08 + SP-09)

**Why now.** §13 invariant 9: *"Adding work to L0 without a bench result is a review rejection."*
Wave 2 added the entire observer pipeline to the daemon's async path and a resident ledger to its
memory. B-A must be unaffected.

**Setup.** `test/bench/hotpath` against a warm daemon pre-populated with 2 000 tool uses, 40 MB of
raw tool output, **and** a ledger holding 5 000 active eliminations with a rebuilt `tried.bloom`.

**Command.**
```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v3-hotpath.json
```

**Expected outputs.** `B-A p99 < 15 ms`, `B-B p99 < 2 ms`, `B-E p99 < 2 s`, `pass: true` for each
gated budget. `B-D` reported. Compare `B-A p99` against the V2 figure recorded in the V2 completion
report: a regression greater than 25% fails this checkpoint even if the absolute number is under
budget (§7 benchstat policy applied to the hot path).

---

### X12 — `TestV3_FullCorpusIngestThroughObserverAndLedger`

**Seams:** `eval` corpus → `observer` → `store`/`dag`/`sketch` → `negknow` → `eval` metrics.
(SP-02 + SP-08 + SP-06 + SP-07 + SP-03 + SP-09)

**Setup.** For each of the 24 sessions in `testdata/sessions/synthetic/`, a fresh
`testutil.NewProject(t)`.

**Inputs.** Materialize hook events with the `eventsFor(s eval.Session)` helper (SP-08's Phase-1
harness), drive `OnUserPrompt`/`OnToolUse` in order, ingest every elimination event the session
carries into a real ledger, then `OnSessionEnd`.

**Expected outputs.**
- **No session panics.** (This is the broadest cross-component smoke surface in the repo and it is
  the cheapest place to catch a `paths.Key` / normalization mismatch between packages.)
- For every session: `store.Stats().DedupRatio > 1.0`; `Stats().ToolUses` equals the session's tool
  call count; `dag` node count ≥ 2 × tool call count; `sketches/tried.bloom` exists **only** for
  sessions that carried eliminations.
- Aggregate over the corpus: log a table of per-session `DedupRatio`, object count, DAG node/edge
  counts, ledger `Health`. Record the **read-heavy** sessions' ratios; at least one must be ≥ 4.0,
  corroborating H15 on independently-generated data.
- For the sessions with non-empty `DependencyChangeAt`: after `RefreshStaleness`, every elimination
  whose dependency changed is `stale` and `Query` never returns `AnswerActive` for it — the same
  assertion as I13 but at the ingest layer rather than the policy layer.

---

## 6. Performance budget validation

Every latency/size budget in force at this point. Budgets whose owning component does not exist yet
are listed explicitly as **not applicable**, so the omission is a decision and not an oversight.

### 6.1 Latency budgets (`00-ARCHITECTURE.md` §2.4)

| ID | Clock | Threshold | Command | Gated? |
|---|---|---|---|---|
| **B-A** | `hook_controlled`: client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (`Qompack.md` §8.1, §11.3 L0) | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v3-hotpath.json` (CI: all 3 OSes) | **Hard fail** |
| **B-B** | `l0_ingest`: daemon read → WAL append returned | p99 < 2 ms | same run; also `go test -bench BenchmarkIngestAccept -run '^$' ./internal/daemon/` | **Hard fail** |
| **B-C** | `l0_process`: WAL → chunked, stored, DAG/sketches updated (async) | p99 < 50 ms (**soft**; overrun → sampling + backpressure, never blocking) | `go test -bench 'BenchmarkOnToolUse_FileRead64KB\|BenchmarkOnToolUse_TestOutput256KB' -run '^$' ./internal/observer/` ; `go test -bench 'BenchmarkPutBytes_100KB_Cold' -run '^$' ./internal/store/` | Reported |
| **B-D** | `hook_wall`: includes host process creation | reported, never gated | same bench run; posted as artifact | No |
| **B-E** | `checkpoint_finalize`: `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | same bench run (`--hook checkpoint`) | **Hard fail** — measured against the current `qompack checkpoint` client path; the checkpoint *writer* is SP-10 (wave 3), so this measures the hook envelope, and that is the honest reading at wave 2 |
| **B-F** | `mcp_tool_call`: request → response | p95 < 250 ms (`minimal` span) | **Not applicable at wave 2** — `internal/mcp` is an SP-01 stub (SP-13, wave 3). Assert only that the budget row exists in `obs.Budgets()` (E14) | No |

### 6.2 Store, dedup and growth budgets (`Qompack.md` §10 Phase 1, §11.3)

| Budget | Threshold | Command |
|---|---|---|
| **Store dedup ratio, read-heavy** | **`Stats().DedupRatio ≥ 4.0`** | `go test ./test/e2e/ -run TestPhase1_DedupRatioReadHeavy -v` (authoritative) and `go test ./internal/store/ -run TestPhase1ExitCriterion_ReadHeavy` |
| **Canonicalization gap, test-output-heavy** | `ratioOn ≥ ratioOff × 1.25` | `go test ./test/e2e/ -run TestPhase1_CanonicalizationGapOnTestOutput -v` |
| **Corpus-level dedup gain (SP-04)** | `testrunner` gain ≥ 1.25; overall gain ≥ 1.0 | `go test ./test/dedup/...` |
| **Store growth sublinear** | OLS exponent `α ≤ 0.95` on `ln(Bytes) ~ ln(RawBytes)`, never `inconclusive` | integration test **X6**, then `go run ./test/replay … --growth $TMP/v3-growth.json` |
| **`tried.bloom` on disk** | 11 264–14 336 B at Appendix C defaults below capacity | `go test ./internal/negknow/ -run TestBloomFileSize` |
| **Bloom false-positive rate** | empirical ∈ [0.008, 0.013] at capacity; `EstFPRate < 0.02` at 8 000 records; hard ceiling 0.10 | `go test ./internal/sketch/ -run TestBloom_EstimatedFPRateMatchesEmpirical` ; `go test ./internal/negknow/ -run TestRebuildBloom_Resizes` ; integration test **X5** |
| **Ledger resident memory** | < 4 MB at 5 000 records | `go test ./internal/negknow/ -run TestMemoryFootprint` |

### 6.3 Component micro-budgets

| Component | Budget | Command |
|---|---|---|
| `sketch` — composite L0 update | **≤ 5 µs/op, 0 allocs** | `go test -bench BenchmarkL0SketchUpdate -benchmem -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` |
| `sketch` — Bloom/CMS/HLL single ops | ≤ 1.0 µs/op, 0 allocs | `go test -bench 'BenchmarkBloom\|BenchmarkCMS\|BenchmarkHLL\|BenchmarkMisraGries' -benchmem -run '^$' ./internal/sketch/` |
| `sketch` — MinHash | 4 KiB ≤ 1.5 ms; 100 KiB ≤ 2.5 ms | `go test -bench BenchmarkMinHash -run '^$' ./internal/sketch/` |
| `sketch` — RebuildBloom 5 000 keys | ≤ 15 ms | `go test -bench BenchmarkRebuildBloom5000 -run '^$' ./internal/sketch/` |
| `chunk` — Split 100 KB | **< 800 µs/op** | `go test -bench BenchmarkSplit_100KB -run '^$' ./internal/chunk/` |
| `chunk` — throughput | gear scan ≥ 400 MB/s; Split ≥ 120 MB/s | `go test -bench 'BenchmarkGearScan_1MiB\|BenchmarkSplit_1MiB' -benchmem -run '^$' ./internal/chunk/` |
| `canon` — Bash 100 KB / go test / Restore 100 KB | < 3 ms / < 1 ms / < 1 ms | `go test -bench 'BenchmarkRun_\|BenchmarkRestore_' -run '^$' ./internal/canon/` |
| `symbols` — Extract / Enclosing / References @100 KB | < 2 ms / < 2 ms / < 1 ms | `go test -bench . -run '^$' ./internal/symbols/` |
| `store` — Put cold / warm | ≤ 3 ms / ≤ 400 µs | `go test -bench 'BenchmarkPutBytes' -run '^$' ./internal/store/` |
| `store` — GetChunk / OpenSpan / Search / Open 50k | ≤ 60 µs / ≤ 150 µs / ≤ 25 ms / ≤ 400 ms | `go test -bench 'BenchmarkGetChunk\|BenchmarkOpenSpan\|BenchmarkSearch_1000Roots\|BenchmarkOpenStore_50kRoots' -run '^$' ./internal/store/` |
| `store` — GC 50k objects / MarkEncoded 100 | ≤ 2 s (deadline ±50 ms) / ≤ 1 ms | `go test -bench 'BenchmarkGC_50kObjects\|BenchmarkMarkEncoded_100' -run '^$' ./internal/store/` |
| `tokens` — EstimateRoot 64 cached | ≤ 5 µs | `go test -bench BenchmarkEstimateRoot_64Cached -run '^$' ./internal/tokens/` |
| `redact` — 100 KB | ≤ 2 ms | `go test -bench BenchmarkRedact -run '^$' ./internal/redact/` |
| `dag` — Backward/Forward slice, 5 000 nodes | **< 1 ms/op** | `go test -bench 'Slice5000' -run TestSliceLatencyBudget ./internal/dag/` |
| `dag` — CrossingEdges, 15 000 edges | < 5 µs | `go test -bench BenchmarkCrossingEdges -run '^$' ./internal/dag/` |
| `observer` — Tombstone | < 2 µs/op | `go test -bench BenchmarkTombstone -benchmem -run '^$' ./internal/observer/` |
| `observer` — OnToolUse 64 KB / 256 KB | within B-C (p99 < 50 ms); 64 KB p50 < 5 ms | `go test -bench BenchmarkOnToolUse -run '^$' ./internal/observer/` |
| `negknow` — Query hit / miss | p99 < 50 µs / < 5 µs | `go test -bench 'BenchmarkQuery' -run TestBudget_ ./internal/negknow/` |
| `negknow` — Record / RebuildBloom / RefreshStaleness / Open / Scan | < 5 ms / < 50 ms / < 10 ms / < 150 ms / < 5 ms | `go test -bench . -run TestBudget_ ./internal/negknow/` |
| `ipc` — ReadState / EncodeRequest | < 100 µs / < 5 µs (4 KB) | `go test -bench 'BenchmarkReadState\|BenchmarkEncodeRequest' -run '^$' ./internal/ipc/` |
| `obs` — Histogram.Observe | < 100 ns/op | `go test -bench BenchmarkHistogram_Observe -run '^$' ./internal/obs/` |
| `config` — cold load | < 2 ms/op | `go test -bench BenchmarkConfigLoad_ColdNoFiles -run '^$' ./internal/config/` |
| `eval` — E-1…E-5 | < 120 s / ≤ 250 ms / ≤ 50 ms / ≤ 20 ms / ≤ 15 ms | B13 |

### 6.4 Benchstat comparison against the `develop` baseline

```
go test -bench . -benchmem -run '^$' -count 6 ./... > v3-bench.txt
go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat testdata/bench-baseline.txt v3-bench.txt
```

**> 10% regression on any micro-benchmark posts a warning; > 25% fails this checkpoint.**
Update `testdata/bench-baseline.txt` on `verify/v3` only after every other row is green, in a
dedicated `perf(bench): refresh baseline after wave 2` commit.

---

## 7. Regression

This checkpoint subsumes its predecessors. Re-run their inventories in full — the wave-2 merge
changed the runtime behaviour of every layer beneath it, so "V1 and V2 passed once" is not evidence.

### 7.1 V1 (post-wave-0, SP-01) — full re-run

The V1 inventory is `plans/V1-VERIFY-foundation-and-contracts.md`. Its content is **exactly inventory group A** of this file
(items A1–A17), which is why group A is enumerated here in full rather than by reference: this file
is standalone. Re-run every row of group A.

Additional V1-specific rows that must still hold now that later waves have landed:
- `plans/OWNERS.tsv` still lists every package on disk with its owner, §6.4 floor and stub probe,
  and `devtool cover` fails if it claims `SP-01` for a package whose probe still returns
  `ErrNotImplemented`.
- Every remaining `t.Skip` in the tree carries the message
  `behaviour: implementation is a stub (Rule W-1)` or `contract fixture not yet recorded (Rule W-2)`
  and no other reason. Verify:
  `git grep -n "t.Skip(" -- ':!*_test.go' ':!internal/**/[a-z]*test/'` should be empty, and
  `git grep -hn "t.Skip(" -- internal/ test/ | grep -v "Rule W-1\|Rule W-2\|QOMPACK_TEST_ACL\|scheduler has landed"` should be empty.
- `.github/workflows/ci.yml`: `bench-gate` and `replay-gate` no longer carry
  `continue-on-error: true` (SP-05 and SP-02 removed it at the end of wave 1). Assert with
  `git grep -n "continue-on-error" -- .github/workflows/ci.yml` returning nothing.

### 7.2 V2 (post-wave-1, SP-02…SP-07) — full re-run

The V2 inventory is `plans/V2-VERIFY-primitives-store-dag-and-baseline.md`. Its content is **inventory groups B, C, D, E, F, G** of this
file (items B1–B13, C1–C9, D1–D7, E1–E14, F1–F13, G1–G9). Re-run every row.

Additional V2-specific rows that matter more now than they did then:
- **Rule W-2 fixture reconciliation.** V2 already re-ran the same-wave golden-fixture tests against
  the real implementations. At V3 two of those fixtures are consumed by *live* wave-2 code and must
  be reconciled against reality: `testdata/golden/contracts/negknow/health.json` (X5) and
  `testdata/golden/contracts/store/stats-growth.json` (X6). Any fixture the real implementation
  cannot reproduce is a verification failure — either the implementation is wrong, or the fixture
  is updated on `verify/v3` with a commit body explaining why.
- **Conformance-suite skip audit.** The suites for `chunk, canon, symbols, redact, sketch, store,
  dag, tokens, eval, ipc, contract` had their skips removed in wave 1; `observer` and `negknow` have
  theirs removed now. Re-assert (A12).
- **Contract producer split.** Wave 2 declares no new contract producer, so
  `TestDeclaredProducerSetMatchesArchitecture` must still report five declared / four
  not-yet-implemented (E10). If wave 2 accidentally declared one, `TestFreshBuildReportsModeFull`
  will fail and the fix is to remove the declaration, not to weaken the test.

### 7.3 The 2% no-regression guardrail (`Qompack.md` §11.3)

> - No metric may regress by more than 2% to improve another without explicit sign-off

**Procedure.**

```
go run ./test/replay --corpus testdata/sessions/synthetic \
  --baseline testdata/baseline/phase0.json \
  --phase 2 \
  --growth $TMP/v3-growth.json \
  --sketch $TMP/v3-health.json \
  --signoff $TMP/v3-signoff.txt \
  --max-wall 2m --ci
```

- For each of the 19 metrics in `MetricsOf`, for each policy, the gate computes
  `worse = (direction == DirHigherBetter) ? (v < b) : (v > b)` and
  `regression = worse && (|b| >= 1e-6 ? |v-b|/max(|b|,1e-9) > 0.02 : |v-b| > absTol)`,
  with `absTol` 0.02 for ratio metrics and 1.0 for count metrics.
- **Any unallowed regression fails this checkpoint.** The failure message prints the exact trailer
  line required.
- A regression is allowed **only** by a line in the PR body / signoff file matching
  `^sign-off:\s*<metric>\s*=\s*<±N.N%>\s+<reason ≥ 10 chars>$`, naming that exact metric. Do not
  invent sign-offs to make this checkpoint pass: a sign-off is a decision the human owner makes,
  and if none is available, the regression must be fixed.
- The two watch-for metrics (`bloom_fp_rate`, `bloom_fill_ratio`) are subject to the same 2% rule
  **plus** the hard ceiling `EstFPRate > 0.10`.
- Corpus staleness: `--phase 2` against `CORPUS.json.regeneratedAfterPhase: 0` is within the
  `phase ≤ regeneratedAfterPhase + 2` window and must **not** trip. If it does, the corpus needs
  regeneration per `docs/adr/0003-replay-overfit-recollection.md` — that is a real finding, not a
  gate to bypass.
- The benchstat comparison of §6.4 is the micro-benchmark analogue of the same guardrail and is
  subject to its own 10%/25% thresholds.

### 7.4 Regression run order

Run in this order so a failure surfaces as early and as cheaply as possible:

1. Preflight (§1) → 2. `go test ./...` (J1, no `-race`) → 3. group-by-group inventories in parallel
(§9) → 4. `go test -race ./...` (J1) → 5. coverage (J2) → 6. lint/security/docs (A3, A4, A16, J3,
J4) → 7. benchmarks and budgets (§6) → 8. replay gate and 2% rule (§7.3) → 9. new integration tests
(§5) → 10. CI on `verify/v3` (J5).

---

## 8. Failure protocol

Any failing row — a red test, a missed budget, a drifted golden, a lint violation, a coverage floor,
a CI job — puts this checkpoint in the failed state. There is no partial pass.

### 8.1 Systematic diagnosis (do this before proposing any fix)

1. **Reproduce deterministically.** Re-run the single failing test with `-run '^<ExactName>$' -v
   -count=1`. If it does not reproduce, run it with `-count=10` and with `-race`; a flake is a
   defect, not noise, and is diagnosed like any other failure.
2. **Locate the seam.** Name the two packages on either side of the failure. Wave-2 failures are
   overwhelmingly at seams: `paths.Key` normalization disagreements between `observer` and
   `negknow`; `core.Dep` hash mismatches between `store.FileHistory` and
   `negknow.RefreshStaleness`; `canon.Options` differences between what `observer` passes and what
   `store` expects; segment ownership between `observer` (opens/closes) and `store` (encodes).
3. **Read the contract, not the code.** Open `00-ARCHITECTURE.md` §5 for the interface in question
   and confirm which side violates it. If §5 itself is wrong, that is an `arch/<short-reason>`
   amendment off `develop` (§0 amendment rule) — **not** a workaround on `verify/v3`.
4. **Classify.** Exactly one of:
   - **(a) Implementation defect** in a wave-2 package (`observer`, `negknow`,
     `daemon/observer_ops.go`) → fix on `verify/v3`.
   - **(b) Latent defect in a wave-0/1 package** exposed by live traffic → fix on `verify/v3`.
   - **(c) Fixture drift** where the real implementation is correct and the W-2 fixture was a
     placeholder → update the fixture on `verify/v3` **with a commit body stating the measured
     values and why they differ**. Never do this to silence a red test you have not diagnosed.
   - **(d) Test defect** — the assertion encodes something the design does not require → fix the
     test, and quote the design section in the commit body.
   - **(e) Missing interface** requiring an §5 change → stop, open `arch/<reason>` off `develop`,
     land it, rebase `verify/v3`.
5. **Write the failing assertion first** where the fix is behavioural. TDD applies to fixes.
6. **Check the blast radius.** Every fix to a shared package (`store`, `sketch`, `canon`, `chunk`,
   `dag`) requires re-running that package's whole group **and** every group that consumes it.

### 8.2 Committing fixes

- All fixes land on `verify/v3`. Never on `develop` directly, never on a merged feature branch.
- **Small conventional commits, as many as needed.** Do not batch unrelated fixes. Format
  (`00-ARCHITECTURE.md` §10):

  ```
  <type>(<scope>): <subject ≤ 72 chars, imperative, no trailing period>

  <body — why, not what; wraps at 100>

  Refs: V3, SP-<NN>, <gap ids>, <Qompack.md sections>
  ```

  `type ∈ feat fix docs test refactor perf build ci chore revert`; `scope` is the Go package.
  The 5–8-commit-per-subplan rule does **not** apply to a verification branch: there is no upper
  bound on fix commits here. Each commit must compile and pass `devtool test` for the packages it
  touches.

- > **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

  That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR
  body in this repository. CI's `verify` job greps the commit range for `Co-Authored-By`,
  `Signed-off-by`, `Generated with` and `🤖` and fails the build if any appear.

- Commit the **new integration tests of §5** as their own commits, separate from any fixes they
  provoked, so the permanent-suite additions are reviewable on their own.

### 8.3 Re-run policy

**After any fix, re-run this checkpoint from the top.** Not the failing test. Not the failing
group. The whole of §2 → §3 → §5 → §6 → §7, in the order of §7.4, plus a fresh CI run on the pushed
`verify/v3`. A fix in `store` can move a dedup ratio, a hot-path p99 and a replay metric
simultaneously; a targeted re-run will not see it. This is the same discipline §11.3's "every phase
gate runs the full replay suite" imposes on phases, applied to verification.

Iterate: diagnose → fix → commit → full re-run. Repeat until every row of §8.5 reads PASS.

### 8.4 The wave-3 gate

**No wave-3 branch is cut until this checkpoint is fully green.** Concretely, until every row of
§8.5 reads PASS, do not create `feat/sp10-checkpointer-l4`, `feat/sp11-rehydrator-l5`,
`feat/sp12-scheduler-l3` or `feat/sp13-mcp-retrieval-layer`, and do not begin work described in
their subplan files. This is `00-ARCHITECTURE.md` §9: *"No wave-N branch is ever cut before
verification V<N> is green."*

### 8.5 On success — merge and tag

```
git checkout develop
git merge --no-ff verify/v3 -m "chore(verify): V3 — wave 2 verification checkpoint green"
git tag v0.2.0
```

Then, and only then, cut the four wave-3 branches from the post-merge `develop`:

```
git checkout -b feat/sp10-checkpointer-l4    develop
git checkout -b feat/sp11-rehydrator-l5      develop
git checkout -b feat/sp12-scheduler-l3       develop
git checkout -b feat/sp13-mcp-retrieval-layer develop
```

Record the completion report (§8.5's table below) in the merge commit body or as
`docs/adr/0100-v3-verification.md` — the numbers are the input to V4's regression comparison and
must not live only in a terminal scrollback.

---

## 9. Subagent strategy

This checkpoint is wide and mostly parallel. Fan the inventory of §2 out across parallel subagents,
one per subplan group, and keep everything that crosses a seam in the main session.

**Main session only, never delegated:**

- The preflight (§1) and the branch cut.
- **Every integration test in §5.** They are new code that spans four to six packages, they become
  part of the permanent suite, and they are exactly where a subagent's local view produces a wrong
  assertion. Author them, run them, and commit them yourself.
- The performance-budget sweep (§6), including `bench-hotpath` and `benchstat` — one machine, one
  configuration, one set of numbers. Delegated benchmarks are not comparable.
- The replay gate and the 2% rule (§7.3).
- Every commit. Subagents return **diffs, test output and measured numbers — never commits**.
- The completion report (§8.5) and the merge.

**Parallel subagents, one per inventory group:**

| Subagent | Owns | Given | Returns |
|---|---|---|---|
| **V3-A** | Group A (SP-01 foundation) A1–A17 | this file, §2 group A | pass/fail per row, the four SP-01 benchmark numbers, the exact text of any lint or guard failure |
| **V3-B** | Group B (SP-02 eval/replay) B1–B13 | this file, §2 group B, §3.2 | pass/fail per row, `policies.stock.fraction_of_opt`, E-1…E-5 numbers, byte-comparison result of the two `--write-baseline` runs |
| **V3-C** | Group C (SP-03 sketches) C1–C9 | this file, §2 group C, §3.3 | pass/fail per row, `BenchmarkL0SketchUpdate` ns/op + allocs, measured Bloom empirical FP rate, all golden reproduction results |
| **V3-D** | Group D (SP-04 chunk/canon/symbols) D1–D7 | this file, §2 group D, §3.4 | pass/fail per row, the D2 novelty histogram, `testrunner` gain, all eleven micro-benchmark numbers |
| **V3-E** | Group E (SP-05 daemon/IPC/contract) E1–E14 | this file, §2 group E, §3.5 | pass/fail per row, the 66-combination fault table result, the contract producer split (5/4), breach-detector transition results |
| **V3-F** | Group F (SP-06 store/redact/tokens) F1–F13 | this file, §2 group F, §3.6 | pass/fail per row, `DedupRatio` from `TestPhase1ExitCriterion_ReadHeavy`, all thirteen store/token benchmark numbers, GC deadline accuracy |
| **V3-G** | Group G (SP-07 dag) G1–G9 | this file, §2 group G, §3.7 | pass/fail per row, both slice benchmark numbers, `CrossingEdges` µs, `thin-vs-full.json` size_ratio and recall |
| **V3-H** | Group H (SP-08 observer) H1–H16 | this file, §2 group H, §3.8 | pass/fail per row, **both Phase-1 dedup ratios**, the canonicalization gap factor, `BenchmarkTombstone` and `BenchmarkOnToolUse` numbers |
| **V3-I** | Group I (SP-09 negknow) I1–I14 | this file, §2 group I, §3.9 | pass/fail per row, the Phase-2 report JSON (`stock_repeats`, `negknow_repeats`, `reduction_pct`, `stale_blocks`), `Health` at 3 000 and 8 000 records, all seven negknow benchmark numbers |

**Rules for subagents.**

1. A subagent runs commands and reports results. It **fixes nothing** unless the main session
   explicitly hands it a diagnosed defect to fix, and even then it returns a diff.
2. No subagent may run any command with `-update`, `-write-report`, `--write-baseline` or
   `--regen-corpus`. Regenerating a contract is the main session's decision (§8.1 case (c)).
3. No subagent may edit `Qompack.md` or `plans/00-ARCHITECTURE.md` under any circumstances.
4. A subagent that finds a failure returns: the exact command, the full failure output, the two
   packages on either side of the seam, and its classification under §8.1 step 4. It does not
   speculate about fixes in other groups.
5. Group J (repository-wide gates) is run by the main session **after** all nine group subagents
   have reported, because `-race`, coverage and CI are whole-tree operations.

---

## 10. Completion report template

Fill this in as the checkpoint runs. Every row needs a verdict **and**, where a metric column
exists, the measured value. A row with `PASS` and an empty metric cell is not complete.

**Header**

| Field | Value |
|---|---|
| Checkpoint | V3 — post-wave-2 (SP-08 observer L0, SP-09 negative knowledge) |
| Branch | `verify/v3` |
| `develop` commit at branch point | `<sha>` |
| `verify/v3` HEAD at completion | `<sha>` |
| Date started / completed | |
| Fix commits on `verify/v3` | `<n>` (`git rev-list --count develop..verify/v3`) |
| Attribution-trailer scan | clean / **FAIL** |
| Platforms | windows-11 (local) + ubuntu-latest, macos-latest, windows-latest (CI) |
| Phases closed | 0, 1, 2 |
| Verdict | **GREEN / RED** |

**Group A — SP-01 foundation**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| A1 | Repository topology | | |
| A2 | Build + 6 cross-build targets | | |
| A3 | Full lint chain | | |
| A4 | `nomagic` D11 pass | | allow-annotations: |
| A5 | `internal/core` primitives | | |
| A6 | `internal/paths` | | |
| A7 | **Append-only guard (5 illegal writes)** | | |
| A8 | `internal/config` + Appendix C golden | | |
| A9 | `logging.Loud` + `obs` budgets | | `BenchmarkHistogram_Observe`: ___ ns/op |
| A10 | `internal/hookio` | | |
| A11 | `internal/cli` hooks exit 0 (30 combos) | | |
| A12 | Stubs + 22 conformance suites | | remaining W-1 skips: |
| A13 | Plugin manifest + bundle | | |
| A14 | Build-order / contract / write-set guards | | |
| A15 | `testutil` + `test/e2e` scaffolding | | |
| A16 | `docs/config-reference.md` no drift | | |
| A17 | Commit policy on `develop` | | |

**Group B — SP-02 replay / Belady**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| B1 | Blocks / Demands | | |
| B2 | Belady OPT | | |
| B3 | Breakpoint OPT + disclaimer | | |
| B4 | Policy registry (stock/null/oracle) | | |
| B5 | Replay determinism | | |
| B6 | Divergence metrics | | |
| B7 | ScoreRun / Report / 19 metrics | | |
| B8 | Synthesizer + 24-session corpus | | |
| B9 | Importer + redaction + fuzz | | |
| B10 | Gate: 2% rule, sign-off, phases, watch-fors | | |
| B11 | **Phase-0 number, reproducible** | | `stock.fraction_of_opt` = ___ ; sessions = ___ |
| B12 | Sublinear growth (real store) | | α = ___ |
| B13 | E-1…E-5 budgets | | E-1 ___ s / E-2 ___ ms / E-3 ___ ms / E-4 ___ ms / E-5 ___ ms |

**Group C — SP-03 sketches**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| C1 | QPKS header + CRC | | |
| C2 | Bloom (sizing, FP, resize, rebuild) | | empirical FP = ___ ; fill@cap = ___ |
| C3 | Count-Min | | |
| C4 | HyperLogLog | | max rel. error = ___ |
| C5 | Misra-Gries | | |
| C6 | MinHash | | one-new-failure Jaccard = ___ |
| C7 | Save/Load/ReplaceGenerational | | |
| C8 | Goldens + 5 fuzz targets | | |
| C9 | **Sketch budgets incl. L0 composite** | | `L0SketchUpdate` = ___ µs, ___ allocs |

**Group D — SP-04 chunk / canon / symbols**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| D1 | FastCDC core + goldens | | mean chunk = ___ B |
| D2 | Boundary stability | | ≤2 novel in ___% ; ≤3 in ___% ; max ___ |
| D3 | Canonicalizer registry (14 rules) | | |
| D4 | Idempotence / non-growth / Restore | | |
| D5 | Symbols | | |
| D6 | **With/without dedup report** | | testrunner gain = ___ ; overall gain = ___ |
| D7 | SP-04 budgets | | `Split_100KB` = ___ µs ; gear scan = ___ MB/s |

**Group E — SP-05 daemon / IPC / contract**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| E1 | Addressing + framing | | |
| E2 | 32-byte state record | | `ReadState` = ___ µs |
| E3 | Client: spool fallback, never errors | | |
| E4 | Server: routing, concurrency, permissions | | round-trip p99 = ___ ms |
| E5 | Daemon lifecycle | | |
| E6 | Ingest WAL / ring / drain | | `IngestAccept` = ___ ms |
| E7 | IdleController | | |
| E8 | Breach detector sync↔spool | | |
| E9 | Extension seams + nil tolerance | | |
| E10 | **Contract monitor, 5/4 producer split** | | mode = ___ |
| E11 | Three-mode degradation | | |
| E12 | **66 fault combos exit 0** | | |
| E13 | Daemon e2e | | |
| E14 | `obs` budget table | | |

**Group F — SP-06 store / redact / tokens**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| F1 | Redaction (10 families) + fuzz | | |
| F2 | Exact token accounting (G10.2) | | |
| F3 | Object layer | | |
| F4 | **Redact → canon → chunk order** | | |
| F5 | tool_use index + supersession | | |
| F6 | File versions + `ChangedSince` | | |
| F7 | **Segment log + DPI guard** | | `MarkEncoded_100` = ___ ms |
| F8 | Search | | `Search_1000Roots` = ___ ms |
| F9 | Stats / DedupRatio / sublinear | | store-level ratio = ___ |
| F10 | GC | | `GC_50kObjects` = ___ s ; deadline err = ___ ms |
| F11 | Flush + index goldens | | |
| F12 | Properties + secret containment e2e | | |
| F13 | SP-06 budgets | | Put cold ___ ms / warm ___ µs |

**Group G — SP-07 dag**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| G1 | Kinds + NodeID | | |
| G2 | Graph mutation (race-free) | | |
| G3 | `CrossingEdges` / `NodesAfter` | | = ___ µs @15k edges |
| G4 | **Scored slicing, thin default** | | backward ___ µs / forward ___ µs |
| G5 | No-selection-authority guard | | |
| G6 | `deps.jsonl` persistence + Compact | | |
| G7 | Builders acyclic | | |
| G8 | Thin-vs-full measurement | | size_ratio = ___ ; recall = ___ |
| G9 | `dagtest` suite | | |

**Group H — SP-08 observer (new)**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| H1 | **Addressable tombstone (G3.2)** | | `BenchmarkTombstone` = ___ µs |
| H2 | Tool classification | | |
| H3 | **Task-boundary signals (G1.5)** | | |
| H4 | `OnToolUse` write path | | |
| H5 | Supersession / redundancy | | |
| H6 | DAG emission | | |
| H7 | Sketch feeding + **Bloom prohibition** | | |
| H8 | **Verbatim capture (G2.3)** | | |
| H9 | **Subagent capture (G10.1)** | | |
| H10 | BOCD feature emission | | |
| H11 | SessionStart / ordered SessionEnd | | |
| H12 | `WireObserver` daemon ops | | |
| H13 | `observertest` suite (0 skips) | | |
| H14 | Observer e2e through daemon | | |
| H15 | **Phase 1 exit criterion** | | read-heavy ratio = ___ (≥4.0) ; canon gap = ___× (≥1.25) |
| H16 | Observer B-C benchmarks | | 64KB p50 ___ ms / 256KB p99 ___ ms |

**Group I — SP-09 negative knowledge (new)**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| I1 | Approach-class canonicalization | | |
| I2 | **Canonical descriptor + goldens** | | |
| I3 | `Record` = §8.5 `eliminated[]` slot (G6.1) | | |
| I4 | Append-only elimination log | | |
| I5 | **Three-way `already_tried`** | | |
| I6 | Record write path + blind mode | | |
| I7 | **Staleness (single `ChangedSince`)** | | |
| I8 | **Bloom-as-cache, active-only rebuild** | | `tried.bloom` = ___ B ; EstFPRate@8k = ___ |
| I9 | Four ingestion sources | | |
| I10 | Heuristic detector | | |
| I11 | `negknowtest` suite (0 skips) | | |
| I12 | Elimination lifecycle e2e | | |
| I13 | **Phase 2 exit criterion** | | stock ___ / negknow ___ / reduction ___% / stale_blocks ___ / dep-change sessions ___ |
| I14 | negknow budgets | | Query hit ___ µs / Rebuild ___ ms / Open ___ ms |

**Group J — repository-wide**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| J1 | `go test -race ./...` / `-count=2` (win) | | |
| J2 | Coverage floors | | negknow ___% / observer ___% / store ___% / dag ___% |
| J3 | Security posture | | |
| J4 | Placeholder scan | | |
| J5 | CI on `verify/v3` (9 jobs) | | |
| J6 | `Qompack.md` unmodified | | |

**Cross-component integration tests (new, permanent)**

| ID | Test | Verdict | Metric / note |
|---|---|---|---|
| X1 | `TestV3_HookEventToTombstoneToRetrievalRoundTrip` | | |
| X2 | `TestV3_ObserverFileVersionsDriveEliminationStaleness` | | |
| X3 | `TestV3_ObserverDagDrivesHeuristicEliminationDetector` | | |
| X4 | `TestV3_VerbatimPromptBecomesEliminationEvidence` | | |
| X5 | `TestV3_ReplayGateConsumesRealSketchHealth` | | fixture reconciled? |
| X6 | `TestV3_ReplayGrowthGuardrailUsesRealStore` | | α = ___ |
| X7 | `TestV3_ObserverSegmentsRespectDPIGuard` | | |
| X8 | `TestV3_DegradedPassiveStillRecordsEverything` | | |
| X9 | `TestV3_LiveSessionWriteSetAndAppendOnly` | | |
| X10 | `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently` | | |
| X11 | `TestV3_HotPathUnchangedWithLedgerResident` | | B-A p99 = ___ ms (V2 was ___ ms) |
| X12 | `TestV3_FullCorpusIngestThroughObserverAndLedger` | | sessions ≥4.0 ratio: ___ / 24 |

**Performance budgets in force**

| Budget | Threshold | Linux | macOS | Windows | Verdict |
|---|---|---|---|---|---|
| B-A `hook_controlled` | p99 < 15 ms | | | | |
| B-B `l0_ingest` | p99 < 2 ms | | | | |
| B-C `l0_process` | p99 < 50 ms (soft) | | | | |
| B-D `hook_wall` | reported only | | | | n/a |
| B-E `checkpoint_finalize` | p99 < 2 s | | | | |
| B-F `mcp_tool_call` | p95 < 250 ms | — | — | — | **N/A — SP-13, wave 3** |
| Store dedup ratio (read-heavy) | ≥ 4.0 | | | | |
| Canonicalization gap (test-heavy) | ≥ 1.25× | | | | |
| Store growth exponent α | ≤ 0.95 | | | | |
| Bloom empirical FP @ capacity | ∈ [0.008, 0.013] | | | | |
| Bloom `EstFPRate` @ 8 000 records | < 0.02 | | | | |
| `tried.bloom` on disk | 11 264–14 336 B | | | | |
| benchstat vs `develop` baseline | ≤ 10% warn / ≤ 25% fail | | | | |

**Regression**

| Item | Verdict | Note |
|---|---|---|
| V1 inventory (group A) fully re-run | | |
| V2 inventory (groups B–G) fully re-run | | |
| W-2 fixture reconciliation (health.json, stats-growth.json) | | |
| Conformance-suite skip audit | | |
| Contract producer split still 5/4 | | |
| **2% no-regression guardrail (§11.3)** | | regressions: ___ ; signed off: ___ |
| Corpus staleness window (`phase ≤ regen+2`) | | |

**Gate**

| Question | Answer |
|---|---|
| Every row above PASS? | |
| `verify/v3` merged into `develop` with `--no-ff`? | |
| Tag `v0.2.0` applied? | |
| Wave-3 branches cut (SP-10, SP-11, SP-12, SP-13)? | **only if every answer above is yes** |

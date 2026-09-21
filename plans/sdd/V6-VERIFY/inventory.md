# V6-VERIFY — Cumulative functionality inventory (source-based reconciliation)

**Owner:** V6 cumulative inventory owner (Opus 4.8 high). **Status:** reconciliation pass only — NO runtime, build, benchmark, git-write or config change performed. Main attaches run artifacts and independent review separately.

**Candidate integrated base:** `301a8e9` (`chore(v6): integrate SP18 documentation delivery`), branch `verify/v6`. The worktree was clean **at the cut of this reconciliation**; it is no longer clean — Main is authoring the working-source overlay described in §H. This inventory reconciles the committed base `301a8e9` only.
**Historical baseline source:** `git show 7f92af5:plans/V6-VERIFY-production-readiness-and-uat.md` §1 tables (authoritative original assertions/commands/expected results). Quoted only as *historical evidence*, never as current execution.
**Current normative source:** `plans/V6-VERIFY-production-readiness-and-uat.md` (HEAD, 235 lines) — maps each retained ID to its current owner criterion.

## A. Method, scope and disposition legend

- Every retained ID `1.1.1 … 1.18.14` (304 rows across 18 families) is carried below with **no row dropped**, plus SP19/20/21 migration additions and the §3 integration identifiers. Historical V5 inventories use different row IDs/counts and are **not** joined by position.
- Current source verified against an index of the **committed base `301a8e9`**: **4 620** `Test*`/`Benchmark*`/`Fuzz*` functions across `internal/*`, `test/*`, `tools/*`, `cmd/*`. This count is the committed tree only and **excludes the uncommitted working-source overlay** (the new `TestV6_*` files Main is authoring — see §H); those are mapped additively, never folded into this index. Every cited symbol in §1 (406 distinct tokens) was cross-checked; 88 had no exact/prefix match and were individually resolved to their current (renamed/relocated/retired) form. Cited symbols below are **actual current** functions, not historical aliases.
- **This is a scope/mapping inventory, not a runtime assurance.** It maps original assertions to current source; it does not certify behaviour. Where the current tree gives insufficient evidence to resolve a row's symbol or disposition, the row is left explicitly incomplete (`unknown`/`verify`) rather than guessed — no row is silently replaced.
- **Execution result per row is `NOT-RUN (see execution overlay §G)`** — a single keyed default for all 304 rows plus the additions. No historical PASS is copied as a current run. Main supplies the keyed execution overlay in §G (one artifact key per row/family) so mapping and execution reconcile unambiguously; the Result column is intentionally not duplicated into every cell.
- **Disposition vocabulary:** `retained` (assertion still normative; test present) · `renamed` (same requirement, current symbol differs — current symbol cited) · `relocated` (moved to another package/suite) · `retired` (assertion withdrawn by current plan) · `replaced` (superseded by a different current assertion) · `superseded-guarantee` (historical numeric/guarantee reduced to a diagnostic by current §5/§6) · `unsupported` (capability ships disabled/refused) · `partial` (current criterion covered in part; some historical sub-assertion has no standalone current test) · `gap` (no current equivalent found — candidate for adding) · `unknown` (needs a run or human step to settle). Composite tags (e.g. `renamed/verify`) mean the primary disposition plus a residual item Main should confirm.
- **Report vocabulary for Main** (current plan §8): `documented`, `verified_in_target`, `implemented_unverified`, `unsupported`, `experimental`, `unknown`. Unless flagged otherwise, a `retained`/`renamed`/`relocated` row is `implemented_unverified` pending Main's run.

New packages vs the historical tree: `internal/admission` (SP-21), `internal/state` (SP-19/20 state authority), and `test/{bench,canary,integration,platform,release}` (the homes that realise most of §3). Package `tools/devtool/configdocs` **no longer exists** (bears on 1.18.13/1.18.14).

---

## B. Per-SP inventory reconciliation

Columns: **ID** · historical assertion (concise) · current requirement (owner) · current source (actual symbol · pkg) · disposition · result.
Result for every row is the single keyed default **`NOT-RUN (see execution overlay §G)`**; it is not duplicated per cell. Rows whose current symbol equals the historical name and is present in the base index are marked `retained`; the cell cites the verified current symbol.

### 1.1 SP-01 — foundation/toolchain/contracts → SP-19 M0-G1/G2/G4

| ID | Historical assertion | Current requirement | Current source (symbol · pkg) | Disp. |
|---|---|---|---|---|
| 1.1.1 | main+develop, root commit contents | repo shape provenance (SP-19 baseline) | `git`-shape check; no unit test (meta-row) | retained |
| 1.1.2 | `go build`/`go vet` clean | build/vet clean | `devtool build-all`/`vet` (toolchain) | retained |
| 1.1.3 | full lint suite clean | lint suite clean | `devtool lint` (tools/lint) | retained |
| 1.1.4 | fmt clean | fmt clean | `devtool fmt-check` | retained |
| 1.1.5 | binary dep closure closed | dep closure allowlist | `devtool` bindeps + `TestGoModVersions` (tools/devtool) | retained |
| 1.1.6 | test-only deps isolated | testdeps sub-check | `devtool lint` testdeps | retained |
| 1.1.7 | import-graph layer DAG | importgraph sub-check | `devtool lint` importgraph; `TestImports_FoundationOnly` (multiple pkgs) | retained |
| 1.1.8 | `Defaults()` = Appendix C verbatim | defaults match spec | `TestDefaults_MatchesAppendixCVerbatim` · config | retained |
| 1.1.9 | 5-layer precedence/env/`--set` | config precedence | `internal/config/...` suite incl. `TestLoad_SchedulerCacheRegime*` | retained |
| 1.1.10 | validation fallback-not-crash | per-leaf fallback + Loud | **`TestLoad_InvalidLeafFallsBackNotCrash`**, `TestLoad_WrongTypeFallsBackWithWarning` · config | renamed |
| 1.1.11 | provenance + JSON Schema | provenance/schema emission | config schema/provenance emitters | retained |
| 1.1.12 | append-only guard | append-only invariant | `TestAppendOnlyGuard` · paths | retained |
| 1.1.13 | atomic/Norm/Key/long-path | path primitives (Windows) | `internal/paths/...` incl. `TestWriteAtomic_*` | retained |
| 1.1.14 | core primitives + sentinels | core hash/clock/sentinels | `internal/core/...` | retained |
| 1.1.15 | logging Loud + rotation | Loud channel | `internal/logging/...` | retained |
| 1.1.16 | obs histograms/budget IDs | obs + B-A…B-F ids | `internal/obs/...`, `BenchmarkHistogram_Observe` | retained |
| 1.1.17 | hookio codecs + Extra | tolerant codecs | `internal/hookio/...`, `FuzzReadEvent` | retained |
| 1.1.18 | CLI dispatch exit policy | hook-always-zero dispatch | `TestDispatch_HookAlwaysExitsZero` · cli | retained |
| 1.1.19 | six hooks exit 0 (real bin) | e2e hooks exit 0 | `TestE2E_AllSixHooksExitZero` · test/e2e | retained |
| 1.1.20 | plugin manifest no drift | manifest≡generator | `devtool plugin-validate`; `TestBundle_OnDiskMatchesGenerator` · pluginmanifest | retained |
| 1.1.21 | config docs never stale | gen-config-docs check | `TestGenConfigDocs_CommittedPageIsCurrent` · tools/devtool | renamed |
| 1.1.22 | six-target cross-build | build-all | `devtool build-all` | retained |
| 1.1.23 | contract fresh posture `ModeFull` | producer-presence posture | `TestFreshBuildReportsModeFull` · contract & `TestGuard_FreshBuildReportsModeFull` · test/guards | retained |
| 1.1.24 | six closing-note guards; SP-15 **re-points** `SubmodularDefaultsOff`→`SubmodularEnabledOnlyAfterPSelection` | build-order/write-set/network guards intact | present: `TestGuard_Phase0BeforeStore`, `_StoreAndNegknowBeforeCheckpoint`, `_SubmodularInertWithoutPSelection`, `_SelectorRefusesWithoutPSelection`, `_O1FlagDefaults`, **`_SubmodularDefaultsOff`** · test/guards | **gap/divergence** — see F-1 |
| 1.1.25 | zero W-1/W-2 skips tree-wide | no stub skips | `classifySkips`/`stubskips` lint; `TestClassifySkips_BlocksW1ForSP01OwnedPackage` · tools/devtool | retained |
| 1.1.26 | testutil fixture | fixtures present | `internal/testutil/...` | retained |
| 1.1.27 | SP-01 baseline benchmarks in budget | baseline benches (diagnostic) | `BenchmarkConfigLoad_ColdNoFiles` etc. · multiple | superseded-guarantee |
| 1.1.28 | `Qompack.md` unmodified since root | — | design doc now **v1.5 with Revision log** | **retired** — see E-3 |

### 1.2 SP-02 — replay/accounting/baseline → SP-19 M0-G5/G6

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.2.1 | eval suite -race | `internal/eval/...` | retained |
| 1.2.2 | harness conformance live | `TestRunHarnessSuite_AgainstEvalNew` · eval | retained |
| 1.2.3 | 24-session corpus hash-stable | `TestCorpus_ManifestHashesMatch`, `TestSynthesize_MatchesCommittedCorpus` · eval | retained |
| 1.2.4 | Phase-0 baseline reproducible/tiered | `test/replay` write-baseline; `TestPhase0_Reproducibility` | retained |
| 1.2.5 | Belady OPT ceiling/floor bracket | `TestOraclePolicy_DelegatesToBelady`, `TestNullPolicy_Empty` · eval | retained |
| 1.2.6 | five divergence metrics | `TestCompare_*` · eval | retained |
| 1.2.7 | ten secondary metrics + latency trio | `TestMetricsOf` · eval (latency tagged modelled) | retained |
| 1.2.8 | breakpoint OPT not-plugin-actionable | replay `--out` breakpoint note | retained |
| 1.2.9 | replay-gate: 2%/trailer/phase/growth/max-wall | `devtool replay --ci`; gate suite | retained |
| 1.2.10 | every gate failure mode has a test | `TestGate_TwoPercentBoundaryExclusive`, `TestPhase0_SessionCountFloor`, `TestReplayDriver_MaxWallExceeded`, `TestGate_BloomFPCeiling`, `_CorpusStaleness`, `_GrowthInconclusiveFails` · test/replay | retained |
| 1.2.11 | redacting importer idempotent | `TestRedact*`, `FuzzRedact` · eval | retained |
| 1.2.12 | eval benches in budget (E-1…E-5) | `BenchmarkBeladyDetail_400Turns` etc.; E-1 read as CPU | superseded-guarantee |
| 1.2.13 | eval imports foundation only | importgraph | retained |
| 1.2.14 | coverage ≥85% | `devtool cover` | retained |

### 1.3 SP-03 — sketches → SP-20 T20-M2-02

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.3.1 | five sketches implement `Sketch` | `internal/sketch/...` | retained |
| 1.3.2 | Bloom sizing | `TestBloom_AppendixASizing` | retained |
| 1.3.3 | CMS sizing | `TestCMS_AppendixASizing` | retained |
| 1.3.4 | HLL sizing/error | `TestHLL_AppendixSizing` | retained |
| 1.3.5 | measured Bloom FP=1% | `TestBloom_EstimatedFPRateMatchesEmpirical` | retained |
| 1.3.6 | resize/saturation alarm | `TestBloom_ResizeFiresBeforeCapacity`, `_SaturatedThreshold` | retained |
| 1.3.7 | RebuildBloom from iterator | `TestBloom_RebuildFromIterator`; `BenchmarkRebuildBloom5000` | retained |
| 1.3.8 | Merge/Scale CMS/HLL/MG | **`TestMG_MergeFrom`**, `TestCMS_Merge`, `TestHLL_Merge` · sketch | renamed |
| 1.3.9 | MG Top(n) no false positives | `TestMG_NoFalsePositives`, `TestMG_FrequentItemGuarantee` | retained |
| 1.3.10 | MinHash/Jaccard/near-dup | `TestMinHash*` | retained |
| 1.3.11 | tried.bloom generational | `TestSave_RefusesTriedBloom`, `TestReplaceGenerational_*` | retained |
| 1.3.12 | corruption loud+typed | `TestLoadWithLog_LoudOnCorrupt`, `TestLoad_CorruptIsNotFoundAndCorrupt` | retained |
| 1.3.13 | frozen v1 wire decodes | `TestGolden_V1StillDecodes`, `TestHeader_*` | retained |
| 1.3.14 | frame-size guards | `TestHeader_EncodeRejectsOversizeFrame` etc. | retained |
| 1.3.15 | five fuzz targets clean | `FuzzBloomUnmarshalBinary`, `FuzzCMSUnmarshalBinary`, `FuzzHLLUnmarshalBinary`, `FuzzMisraGriesUnmarshalBinary`, `FuzzSignatureUnmarshalBinary` (no `FuzzSketchUnmarshal` — historical confirms) | retained |
| 1.3.16 | hot-path sketch update 0-alloc | `TestL0SketchUpdate_ZeroAlloc`, `BenchmarkL0SketchUpdate` | retained |
| 1.3.17 | foundation-only + coverage | `TestImports_FoundationOnly` | retained |

### 1.4 SP-04 — chunk/canon/symbols → SP-20 T20-M1-01/02/06

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.4.1 | FastCDC Split/SplitStream | `internal/chunk/...` | retained |
| 1.4.2 | boundary stability | `TestPropBoundaryStability_Insertion/_Deletion` · chunk | retained |
| 1.4.3 | cross-platform gear/boundary goldens | `TestGearTableGolden`, `TestSplit_GoldenBoundaries` · chunk | retained |
| 1.4.4 | Merkle RootHash domain-sep | `TestRootHash` · chunk | retained |
| 1.4.5 | 14 canonicalizers reg-order | `TestKnownClasses_RegistrationOrder` · canon | retained |
| 1.4.6 | idempotence | `*Idempot*` · canon | retained |
| 1.4.7 | byte-exact inverse | `TestRestore*`, `FuzzRestore` · canon | retained |
| 1.4.8 | no canonicalizer grows | `TestEveryCanonicalizer_NeverGrows` · canon | retained |
| 1.4.9 | every rule emits Class | `TestMatcherClassAssigned` · canon | retained |
| 1.4.10 | MinHash on Run result | `TestSignature_OnlyWhenEnabled`, `TestNearDup_DelegatesToSignature` · canon | retained |
| 1.4.11 | dedup measurement (O2) | `test/dedup/...`; canon-dedup-report | retained |
| 1.4.12 | symbol Extract/Enclosing/References | `internal/symbols/...` | retained |
| 1.4.13 | Enclosing minimal span | **`TestEnclosing_SmallestSpanWins`**, `_OutsideAnySymbol`, `_OutOfRange` · symbols | renamed |
| 1.4.14 | chunk/canon/symbol benches | superseded-guarantee (measure per §5) | superseded-guarantee |
| 1.4.15 | fuzz targets clean | `FuzzSplit` · chunk; **`FuzzCanonicalize`** (historical `FuzzCanonicalizeRun` → this) · canon; `FuzzRestore` · canon; `FuzzExtract` · symbols | renamed |
| 1.4.16 | import discipline + coverage | lint/cover | retained |
| 1.4.17 | corpus hygiene (no secrets) | grep guard + `.gitattributes` | retained |

### 1.5 SP-05 — daemon/IPC/hot path/contract → SP-19 M0-G2/G3 + SP-20 T20-M1-03/04/05

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.5.1 | IPC address per platform | `internal/ipc/...` (`TestResolveFor_*`) | retained |
| 1.5.2 | NDJSON framing/ACK-NAK | `TestMaxLineBytes_IsOneMebibyte` etc.; `FuzzDecodeRequest` | retained |
| 1.5.3 | Client.Send never errors, spools | `TestSendNeverReturnsError`, `TestSendNAKSwitchesToSpool` · ipc | retained |
| 1.5.4 | ipctest conformance live | `internal/ipc/ipctest` | retained |
| 1.5.5 | daemon singleton lock/stale reclaim | `TestLock*` · daemon | retained |
| 1.5.6 | lazy detached spawn | `TestLazySpawn_*` · ipc; `TestE2ELazySpawn` · test/e2e | retained |
| 1.5.7 | WAL ingest, ACK after WAL | `TestIngest*`, `TestAcceptWireLineWALsExactlyOneTerminator` · daemon | retained |
| 1.5.8 | Drain idempotent | `TestDrain*`, `TestDrainSurvivesCorruptLine` · daemon | retained |
| 1.5.9 | idle controller / idle exit | `TestIdle*`, `TestE2EIdleExit` | retained |
| 1.5.10 | config hot reload | `TestConfigReload` · daemon | retained |
| 1.5.11 | extension seams nil-tolerant | `TestServicesAllNil` · daemon | retained |
| 1.5.12 | **B-A p99<15ms / B-B p99<2ms** on real bin | hot-path budget (re-measure, no universal claim) | `devtool bench-hotpath`; CI bench-gate | superseded-guarantee (see E-2) |
| 1.5.13 | overrun degradation state transition | `TestBreachDetectorTransitionsAfterThreeWindows` · daemon | retained |
| 1.5.14 | nine contract assertions at SessionStart | `internal/contract/...` | retained |
| 1.5.15 | **post-wave-5 every producer real, not not-yet-implemented** | producers declared for shipped services | wired at `internal/daemon/options.go:339-352` (`DeclareProducer` ×9, PreCompact/AdditionalContext/MCPRegistered conditional); `TestStandardAssertions_MatchTheNormativeTable`, `checkMCPServerRegistered` · contract | retained — **implemented in source; real-observation is NOT-RUN** (F-2) |
| 1.5.16 | degradation semantics | `TestDegradedPassiveSuppressesActingPaths`, `_StillRecords`, `TestModeOffSkipsIngest` · daemon/contract | retained |
| 1.5.17 | restoration after two clean runs | `TestMonitor_TwoConsecutiveCleanRunsRestore` · contract | retained |
| 1.5.18 | session-start marker terminal-only | `TestMarkerIsWrittenByFlushAndCheckpointOnly` · daemon | retained |
| 1.5.19 | every hook exits 0 under faults | `TestFaultActive_UnsetIsInertForAllElevenSites` + cli fault suite; `test/fault/*` | renamed/relocated |
| 1.5.20 | transport security posture | `TestGuard_NoNetworkImports` · test/guards; `TestSecurity_MCPServerNeverImportsOSExec` · test/security; historical `devtool security-audit` has no current entry point | replaced |
| 1.5.21 | coverage floors | `devtool cover` | retained |

### 1.6 SP-06 — store/redaction/tokens → SP-20 M1 gates

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.6.1 | object fanout/zstd | `TestPutBytes_FanoutLayout` · store | retained |
| 1.6.2 | global dedup | `TestPutBytes_GlobalDedup` · store | retained |
| 1.6.3 | canonicalize-before-chunk | `TestPutBytes_CanonicalizeBeforeChunk` · store | retained |
| 1.6.4 | redaction at choke point | `TestPutBytes_RedactionBeforeChunking`; `TestE2E_SecretNeverLandsInObjects` | retained |
| 1.6.5 | ten redaction rules idempotent | `internal/redact/...`, `FuzzRedactIdempotent` | retained |
| 1.6.6 | tool_use index/preview | `TestRecordToolUse`/`TestToolUse*` · store | retained |
| 1.6.7 | supersession marking | `TestMarkSuperseded` · store | retained |
| 1.6.8 | file version history | `TestAppendFileVersion_History`, `TestFileAt` · store | retained |
| 1.6.9 | `ChangedSince([]core.Dep)` | `TestChangedSince` · store | retained |
| 1.6.10 | Search backing recall | `TestSearch` · store | retained |
| 1.6.11 | OpenSpan minimal-span | `TestOpenSpan_Boundaries` · store | retained |
| 1.6.12 | segment log encoded-once DPI | `TestSegment_MarkEncodedRefusesDifferentSeq`, `_BatchIsAllOrNothing` · store | retained |
| 1.6.13 | Frontier/Unencoded | `TestSegment_Frontier*`, `TestSegment_Unencoded` · store | retained |
| 1.6.14 | mark-sweep GC resumable/retention | `TestGC_RetentionIsWhicheverIsLonger`, `_DeadlineTruncatesAndResumes`, `_MarkPhaseHonoursTheDeadline` · store | retained |
| 1.6.15 | DedupRatio responds to toggle | `TestStats_DedupRatio` · store | retained |
| 1.6.16 | **Phase-1 dedup ≥4.0 read-heavy** | dedup gain (diagnostic, not acceptance) | `TestPhase1ExitCriterion_ReadHeavy` · store | superseded-guarantee (E-2) |
| 1.6.17 | sublinear growth | `TestStats_SublinearGrowth` · store | superseded-guarantee |
| 1.6.18 | exact chunk token accounting | `internal/tokens/...` (`EstimateRoot([]core.ChunkRef)`, clamp) | retained |
| 1.6.19 | store benches in budget | `BenchmarkPutBytes_100KB_Cold` etc. | superseded-guarantee |
| 1.6.20 | conformance live | storetest/redacttest/tokenstest | retained |
| 1.6.21 | import discipline/coverage | lint/cover | retained |

### 1.7 SP-07 — DAG/dependencies → SP-20 T20-M2-01/02 + SP-15 M5-G15-B

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.7.1 | 9 node/8 edge kinds, NodeID | `internal/dag/...` | retained |
| 1.7.2 | append-only deps.jsonl round-trip | `TestPropLogRoundTrip`, `TestFlushIsAppendOnly` · dag | retained |
| 1.7.3 | torn tail tolerated | `TestOpenTornTail`, `TestOpenCorruptLines` · dag | retained |
| 1.7.4 | idle Compact drops tombstones | `TestCompact` · dag | retained |
| 1.7.5 | slices return **scores** not keep/drop | `TestBackwardSlice`, `TestForwardSlice`, `TestNoBooleanKeepAPI` · dag | retained |
| 1.7.6 | thin slicing default | `TestDefaultSliceOptions` · dag | retained |
| 1.7.7 | measured thin-vs-full | `TestThinVsFullComparison` · dag | superseded-guarantee |
| 1.7.8 | CrossingEdges = coupling | `TestCrossingEdges`, `BenchmarkCrossingEdges` · dag | retained |
| 1.7.9 | NodesAfter ordered/live-only | `TestNodesAfter` · dag | retained |
| 1.7.10 | slice latency budget | `TestSliceLatencyBudget`, `BenchmarkBackwardSlice5000` · dag | superseded-guarantee |
| 1.7.11 | builder output acyclic | `TestBuilderOutputIsAcyclic` · dag | retained |
| 1.7.12 | concurrency safe | `TestConcurrent*` -race · dag | retained |
| 1.7.13 | golden fixtures | `TestGraphBasicGolden`, `TestSliceGoldenBackward` · dag | retained |
| 1.7.14 | conformance/imports/coverage | dagtest; lint/cover | retained |

### 1.8 SP-08 — observer → SP-20 T20-M1-01–05

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.8.1 | OnToolUse 14-step | `internal/observer/...` | retained |
| 1.8.2 | addressable tombstone | `TestTombstone*`, `BenchmarkTombstone` · observer | retained |
| 1.8.3 | supersession marks earlier read | `TestSupersede*` · observer | retained |
| 1.8.4 | DAG edges recorded | `TestGraph_*` · observer | retained |
| 1.8.5 | CMS/HLL fed; **Bloom never fed** | `TestOnSessionEnd_NeverWritesTriedBloom`, `TestObserverNeverFeedsBloom` · observer | retained |
| 1.8.6 | verbatim prompt capture | `TestOnUserPrompt_NeverRegenerated`; `TestE2E_VerbatimPromptSurvivesRestart` | retained |
| 1.8.7 | subagent capture + hashes | `TestOnStop_RetrievalPathG10_1` · observer | retained |
| 1.8.8 | task-boundary signals | `TestExtractSignals`; `TestWireObserver` · observer/daemon | retained |
| 1.8.9 | SessionStart/End branches | `TestOnSessionStart*`, `TestOnSessionEnd*` · observer | retained |
| 1.8.10 | Phase-1 dedup half | `TestPhase1_DedupRatioReadHeavy` · test/e2e | superseded-guarantee |
| 1.8.11 | canonicalization gap | `TestPhase1_CanonicalizationGapOnTestOutput` · test/e2e | superseded-guarantee |
| 1.8.12 | sublinear growth w/ observer | `TestPhase1_StoreGrowthSublinear` · test/e2e | retained |
| 1.8.13 | observer-side latency | `BenchmarkOnToolUse_*` (B-C) | superseded-guarantee |
| 1.8.14 | observer conformance | observertest | retained |
| 1.8.15 | import discipline/coverage | lint/cover | retained |

### 1.9 SP-09 — negative knowledge → SP-20 T20-M2-01/02 + SP-13 T13-STATE

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.9.1 | 4-field descriptor/Key | `TestCanonicalize` · negknow | retained |
| 1.9.2 | append-only eliminations.jsonl | `TestRecord` · negknow | retained |
| 1.9.3 | three-way already_tried | `TestQuery_Absent/_Active/_Stale` · negknow | retained |
| 1.9.4 | stale note byte-identical | `TestQuery_Stale_FlagNote` · negknow | retained |
| 1.9.5 | bloom-only never masquerades | `TestQuery_BloomOnly`, `TestBloomLoadFailure_NoRecords_NeverFalsePositive` · negknow | retained |
| 1.9.6 | RefreshStaleness via ChangedSince | `TestRefreshStaleness` · negknow | retained |
| 1.9.7 | RebuildBloom active-only | `TestRebuildBloom_Resizes`, `_NeverFromCheckpoint` · negknow | retained |
| 1.9.8 | scope session/project | `TestScope` · negknow | retained |
| 1.9.9 | heuristic Detector | `TestDetector*` · negknow | retained |
| 1.9.10 | Health() surfaces fill/FP | `TestHealth` · negknow | retained |
| 1.9.11 | Phase-2 exit in replay | `TestPhase2ExitCriterion` · test/replay | superseded-guarantee |
| 1.9.12 | performance budgets | `TestBudget_*` · negknow | superseded-guarantee |
| 1.9.13 | conformance/imports/coverage/write-set | negknowtest; lint/cover | retained |

### 1.10 SP-10 — checkpoint/pins → SP-10 test-plan gates

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.10.1 | §8.5 schema, importance-order | `TestSchemaFieldOrderIsImportanceOrder` · checkpoint | retained |
| 1.10.2 | Writer Begin/Advance/Finalize/Abort | `internal/checkpoint/...` | retained |
| 1.10.3 | SourceSet carries no text | `TestSourceSetCarriesNoText` · checkpoint | retained |
| 1.10.4 | Advance DPI-guarded | `TestAdvanceIsDPIGuarded` · checkpoint | retained |
| 1.10.5 | Finalize immutable artifact | `TestFinalize*`; `TestAppendOnlyGuard` | retained |
| 1.10.6 | Reader Latest/Get/Chain/Verify | `TestReader*`, `TestVerify*`, `TestLatestFallsBackToParent` · checkpoint | retained |
| 1.10.7 | Truncate tiering | `TestTruncate*` · checkpoint | retained |
| 1.10.8 | no code snippets | `TestGoldenCheckpointsContainNoCodeBlocks`, `TestCheckpointGolden_ContainsNoCodeBlocks` · checkpoint | retained |
| 1.10.9 | ExtractDecisions sole producer | **`TestExtractDecisionTargetAndRuneCaps`**, `BenchmarkExtractDecisions` · checkpoint | renamed |
| 1.10.10 | ValidatePointers vs tree | `TestValidatePointers` · checkpoint | retained |
| 1.10.11 | FocusInstructions verbatim | `TestFocus*` · checkpoint | retained |
| 1.10.12 | injection tagging/strip | `TestInjectionTags` · checkpoint | retained |
| 1.10.13 | pins append-only + tombstone | `internal/pins/...` | retained |
| 1.10.14 | PreCompact writes checkpoint | `TestE2E_Checkpoint*` · test/e2e | retained |
| 1.10.15 | independent cadence | `TestCadenceFinalizesWhenDraftReachesBudget`, `TestCadenceSealsNothingWhenNeitherConditionHolds` · daemon/checkpoint (note: `TestCadenceIsOffInDegradedPassive` → `TestE2E_CheckpointDegradedPassiveSealsNothing`) | renamed |
| 1.10.16 | **B-E p99<2s** PreCompact | checkpoint budget (re-measure) | `bench-hotpath` B-E | superseded-guarantee (E-2) |
| 1.10.17 | checkpoint micro-benches | `BenchmarkFinalize/AdvanceSegment/ExtractDecisions` | superseded-guarantee |
| 1.10.18 | residual-span reduction ≥30% | two replay --phase 4 runs (frontier toggle) | superseded-guarantee/unknown |
| 1.10.19 | conformance/imports/coverage | checkpointtest/pinstest; RunPinsSuite | retained |

### 1.11 SP-11 — rehydration/rules/skills → SP-11 T11-* gates

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.11.1 | eight-item order | `TestBuild_ItemOrderInPayload` · rehydrate | retained |
| 1.11.2 | item 2 = L0 verbatim | `TestUserIntent_EarliestTurnOfThisSessionWins`, `_IgnoresOtherSessions` · rehydrate | retained |
| 1.11.3 | eliminations digest + standing instruction | **`TestStandingInstruction_IsTheVerbatimSentence`**, `_IsOneBareLine`; `TestEliminations_StaleRendersTheVerbatimNote` · rehydrate | renamed |
| 1.11.4 | pointers not contents | `TestPointers` · rehydrate | retained |
| 1.11.5 | drop report + persistence | `TestDropReport*` · rehydrate | retained |
| 1.11.6 | rules PathScoped | `TestRestored_PathScoped` · rules/rehydrate | retained |
| 1.11.7 | rules NestedClaudeMD | `TestRestored_NestedClaudeMD` · rules | retained |
| 1.11.8 | skills index budget, no literal 450 | `TestSkillIndex_*`, `TestIndex_DescriptionFallsBackToFirstBodyLine` · skills | retained |
| 1.11.9 | injection tagging seq | `TestBuild_InjectionTagging` · rehydrate | retained |
| 1.11.10 | budget 8–12K < stock | `TestBuild_NeverExceedsMaxTokens`, `_SmallerThanStock`, `PropBuild_MonotoneInBudget` · rehydrate | retained |
| 1.11.11 | checkpoint-as-fallback | `TestBuild_NoTranscriptRead_ClosesG75`; `TestE2E_SessionStartCompactAfterFailedSummary` | retained |
| 1.11.12 | SessionStart compact/clear wired | `TestE2E_SessionStartCompactUnderBudget` · test/e2e | retained |
| 1.11.13 | degraded-passive emits nothing | `TestService_DegradedPassiveEmitsNothing` · daemon | retained |
| 1.11.14 | Reporter satisfies mcp.DropReporter | `TestE2E_DropReporterSatisfiesMCP` · test/e2e | renamed |
| 1.11.15 | Phase-3 exit | `TestPhase3` · test/replay | superseded-guarantee |
| 1.11.16 | L5 latency budgets | rehydrate/rules/skills benches | superseded-guarantee |
| 1.11.17 | conformance/imports/coverage | rehydratetest; lint/cover | retained |

### 1.12 SP-12 — scheduler → SP-12 M5-G12-A–E

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.12.1 | Evaluate pure | `internal/scheduler/...` | retained |
| 1.12.2 | composite trigger 4 clauses | **`TestEvaluate_BelowSoftFloor_NoCompact`**, `_Changepoint_Fires`, `_YoungDaly_Fires`, `_HardCeiling_Fires_UrgencyNow`, `_IdleColdCache_Fires` · scheduler | renamed |
| 1.12.3 | soft-floor/hard-ceiling from config | `TestSoftFloor_55PctOfEffectiveWindow`, `TestHardCeiling_OneTurnBelowHostThreshold`, `TestSoftFloorBelowHardCeiling_Property` · scheduler | renamed |
| 1.12.4 | p-selection argmax | **`TestChooseP_TieBreakLatestWhenWarmDeepestWhenCold`**, `_EmptyReturnsNotOK` · scheduler | renamed |
| 1.12.5 | sliding-TTL idle model | `TestTTL*`, `PropCacheFactor_MonotoneDecreasing` · scheduler | retained |
| 1.12.6 | Young–Daly measured δ | `TestYoungDaly_Formula`, `_UnmeasuredDeltaDisablesClause`, `_NoBaselineDisablesClause` (replaces `TestRecordCompactionCost`) · scheduler | renamed |
| 1.12.7 | BOCD over paths/tools/time | `TestBOCD*`, `TestBOCD_UnmarshalRejectsCorruption` · scheduler | retained |
| 1.12.8 | droppable-block ranking | `TestDropClassOf`/`TestClassifyDrop` · scheduler/daemon | retained |
| 1.12.9 | six idle tasks | `TestIdleTasks`; `TestDaemonIdleRunsSchedulerWork` · daemon/e2e | renamed |
| 1.12.10 | acting idle task suppressed | `TestIdleActingTaskSkippedInDegradedPassive` · daemon | retained |
| 1.12.11 | O5 frontier advancement | `TestAdvanceFrontier*` · daemon | retained |
| 1.12.12 | persistence/self-heal | **`TestRuntime_CorruptStateFilesSelfHeal`** · daemon (replaces `TestStateCorrupt`) | renamed |
| 1.12.13 | event path end-to-end | `TestWrapServices_*`; `TestDaemonIdleRunsSchedulerWork` | renamed |
| 1.12.14 | scheduler off hot path; **B-A<15ms unchanged** | `TestSchedulerNotOnHotPath`; bench-hotpath | retained + superseded-guarantee(E-2) |
| 1.12.15 | PSelectionAvailable gate | `TestPSelectionAvailable` · scheduler/daemon | retained |
| 1.12.16 | Phase-4 exit | `TestPhase4` · test/replay | superseded-guarantee |
| 1.12.17 | scheduler benches | `BenchmarkEvaluate` etc. | superseded-guarantee |
| 1.12.18 | conformance/imports/coverage | schedulertest; lint/cover | retained |

### 1.13 SP-13 — MCP retrieval → SP-13 T13-PROTOCOL…ROLLBACK

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.13.1 | JSON-RPC stdio server | `internal/mcp/...` | retained |
| 1.13.2 | eight tools registered | `TestToolsList` · mcp | retained |
| 1.13.3 | recall vs real store | `TestRecall` · mcp | retained |
| 1.13.4 | expand re-materializes | `TestExpand` · mcp | retained |
| 1.13.5 | re_read current/historical | `TestReRead`, `TestReReadFallsBackToStoreWhenFileDeleted` · mcp | retained |
| 1.13.6 | already_tried three-way | `TestAlreadyTried` · mcp | retained |
| 1.13.7 | record_eliminated through ledger | `TestRecordEliminated` · mcp | retained |
| 1.13.8 | timeline returns segments | `TestTimeline` · mcp | retained |
| 1.13.9 | why vs ExtractDecisions | `TestWhy` · mcp | retained |
| 1.13.10 | dropped vs DropReporter | `TestDropped` · mcp | retained |
| 1.13.11 | ephemeral-at-birth (7 tools) | `TestEphemeral` · mcp | retained |
| 1.13.12 | minimal-span + full escape | `TestSpan*`, `PropPagingReconstructs` · mcp | retained |
| 1.13.13 | promoter counts expansions | `TestNoteExpansion`/`TestPromoted*` · mcp | retained |
| 1.13.14 | **mcp.server_registered real** | producer declared `options.go:352` (`if MCPInitialized`); `TestMCPInitializedSeamFlipsAfterInitialize`, `TestInitializedWritesContractHistory` · daemon; `checkMCPServerRegistered` · contract | retained — real-obs NOT-RUN (F-2) |
| 1.13.15 | panic isolation/hostile input | **`TestHandlerPanicIsolated`**, `TestDispatchIsolatesHandlerPanic`, `TestMalformedJSONReturnsParseErrorAndKeepsServing`, **`FuzzServeLine`** · mcp | renamed |
| 1.13.16 | **B-F p95<250ms** | mcp budget (re-measure) | `TestBudgetBF` · mcp | superseded-guarantee(E-2) |
| 1.13.17 | docs from tool table | `TestGenMCPDocsCheckDetectsDrift`, `TestMCPToolsDocsUpToDate` · tools/devtool | renamed |
| 1.13.18 | conformance/imports/coverage | mcptest; lint/cover | retained |

### 1.14 SP-14 — commands/observability → SP-14 gates

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.14.1 | seven commands order | `commands.Names()`; `TestNames`/`TestSpecs_*` · commands | retained |
| 1.14.2 | status renders 11 sections | `TestRenderStatus_Golden`, `_JSONGolden`, `_NeverPrintsAZeroForAnAbsentMeasurement` · commands | renamed |
| 1.14.3 | no 2nd retrieval impl | `TestRetrieval_NoServerIsUnavailableNotEmpty`, `TestRetrieval_UnregisteredToolIsUnavailable` · commands | renamed |
| 1.14.4 | pin/pin --eliminated | `TestPin*`, `TestPin_AgentClaimIsNotUpgradedToAUserInstruction` · commands | retained |
| 1.14.5 | checkpoint-now DPI | `TestCheckpoint_NeverRequestsNativeCompaction`, `_TransportFailureStillReportsAndDoesNotClaimSuccess`, `_WithoutARouteIsUnavailable` · commands | renamed |
| 1.14.6 | eval reports OPT/secondaries | `TestEval_TaskAndRecoveryLeadNotFractionOfOPT`, `_MissingUsageIsUnknownNotZero`, `_CheapWrongResultCannotPass` · commands | renamed |
| 1.14.7 | uniform --json envelope + help | `TestEnvelope_CarriesSchemaAndCommand`, `TestRun_HelpFlagPrintsHelpAndSucceeds`, `TestRun_JSONEnvelopeCarriesTheError` · commands | renamed |
| 1.14.8 | deterministic ANSI-free render | `TestRenderStatus_IsDeterministic`, `_CarriesNoANSI` · commands | renamed |
| 1.14.9 | manifest≡binary≡docs | `TestGenCommandDocs_*` · tools/devtool | renamed |
| 1.14.10 | conformance/benches/coverage | commandstest; `BenchmarkRenderStatus` etc. | retained/superseded-guarantee |

### 1.15 SP-15 — selection/grammar → SP-15 M5/M6-G15

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.15.1 | cheap Δ-scorer [0,1] | `TestCheapScorer_EveryBlockIsScoredInTheUnitInterval`, `_IsDeterministic` · analyzer | renamed |
| 1.15.2 | Δ ranks negknow highest | `TestCheapScorer_ACoveringContinuationOutscoresAnEmptyOne` (+ `TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence`) · analyzer | renamed/verify (F-3) |
| 1.15.3 | DetectRedundancy superseded+near-dup | `TestDetectRedundancy_HonoursTheStoresOwnSupersededStatus`, `_NearDuplicatesAreCandidatesNotEquivalence` · analyzer | renamed |
| 1.15.4 | suffix constraint structural | **`TestNewSelector_RefusesABlockBeforeP`**, `TestSelect_NothingBeforePIsEverKept`, `TestPropose_RunsTheConstructorPosGuard` · analyzer | renamed |
| 1.15.5 | ship-order guard inert w/o p-sel | `TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet`, `TestSelector_SelectIsRealBehindTheGate`, `TestPropose_RunsTheShipOrderGuard` · analyzer | renamed |
| 1.15.6 | submodular lazy-greedy (1−1/e) bound | `TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer` · analyzer; **submodular ships DISABLED** (`SubmodularEnabled:false`); explicit `PropGuaranteeUnitCost/Knapsack` not found under historical names | superseded/unsupported — see F-3 |
| 1.15.7 | ephemeral results rank first | representation choice now in `TestPropose_ChoosesAtMostOneRepresentationPerItem`; native ephemeral-eviction **retired** per current §3.5 | replaced (E-1/§3.5) |
| 1.15.8 | Sequitur invariants online | `Prop*` · grammar (`internal/grammar`) | retained |
| 1.15.9 | thrash detection + warning | **`TestSequitur_ThrashThreshold`**, `_ThrashIgnoresUnrepeatedWork`, `TestFormatWarning`, `TestDetector_RendersThroughFormatWarning` · grammar | renamed |
| 1.15.10 | versioned CRC codec | `TestSave*`/`TestLoad*` · grammar | retained |
| 1.15.11 | grammar folded into checkpoint | relocated → `TestV5_GrammarAndPromotionCoexistInFinalize` · test/integration/v5_x08; `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly` · v5_x09 | relocated |
| 1.15.12 | Phase-5 exit | `TestPhase5` · test/replay | superseded-guarantee |
| 1.15.13 | Phase-6 exit | `TestPhase6` · test/replay | superseded-guarantee |
| 1.15.14 | analyzer/grammar benches | benches | superseded-guarantee |
| 1.15.15 | conformance/imports/coverage | analyzertest/grammartest | retained |

### 1.16 SP-16 — reuse/refinement → SP-16 M6-G16-A–E

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.16.1 | O4 warm start CMS decay+merge | `TestWarmStart*` · daemon | retained |
| 1.16.2 | warm-start honours config both ways | `TestWarmStart_AgeingCanBeDisabledButTheCapStillHolds`, `TestWarmStart_RefusalLadder`, `TestWarmStart_UnauthorizedIsARefusalNotADiscount` · scheduler/warmprior_test.go | renamed |
| 1.16.3 | project-scope eliminations carried | `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession` · test/integration/v5_x12; carry-forward at daemon start | relocated |
| 1.16.4 | BOCD priors seeded | `TestWarmPriorAblation_M6G16D`, `_IsDeterministic`, `_ARefusedPriorIsExactlyTheBaseline` · scheduler | renamed |
| 1.16.5 | O4 measured benefit | warm vs cold delta (diagnostic) | superseded-guarantee |
| 1.16.6 | demand-driven promotion | `TestPromote_PromotedPointersSurviveTruncation` · checkpoint; `TestV5_...Promotion...` · integration | relocated |
| 1.16.7 | per-segment Bloom (LSM) | **`TestSegmentFilter_*`** family, `TestSegment_BloomRefParsed`, `TestPublishFilter_*`, `TestReadSegmentFilter_*`, `TestSweepSegmentFilters_*`, `TestRunSegmentLogSuite_AgainstRealStore` · store | renamed |
| 1.16.8 | ski-rental computed not literal | **`TestSkiRental_ComputedNotLiteral`**, `TestSkiRental_ThresholdLiteralOnlyInTests`, `TestSkiRentalThreshold_TracksRegimeNotConfig` · scheduler | renamed |
| 1.16.9 | progressive truncation argmax | **`TestGoldenTruncationFixtures`** · checkpoint (replaces `TestTruncationCurve`) | renamed |
| 1.16.10 | declared non-delivery of prefix reorder | ceremonial test `TestPrefixReorderingNotAttempted` absent; non-delivery now carried as an ADR-prose declaration (verify `docs/adr/0016`), consistent with SP-16's "declared non-delivery" criterion | replaced — see F-8 |
| 1.16.11 | phase-7 benches | benches | superseded-guarantee |
| 1.16.12 | artifacts reproducible w/o golden flag | golden reproducibility tests w/ `QOMPACK_UPDATE_GOLDEN` unset | retained |
| 1.16.13 | coverage floors | cover | retained |

### 1.17 SP-17 — packaging/release → SP-17 SP17-M7-01–08

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.17.1 | deterministic bundle, byte-identical | `TestAssembleBundle_Deterministic`, `_Identity`, `_Layout`, `TestWriteArchive_Deterministic`, `TestWriteArchive_MembersMatchTheBundleDirectory`, `TestArchiveAssembledBundles_RemovesStaleArchives` · tools/devtool; `TestBundle_OnDiskMatchesGenerator` · pluginmanifest; `TestCanary_PackagingShape` · test/canary | renamed — reproducibility NOT-RUN |
| 1.17.2 | bundle layout §3.4 | `TestCanary_PackagingShape`, `TestCanary_PluginValidate` · test/canary | retained |
| 1.17.3 | binary size budget | `devtool` size check | superseded-guarantee |
| 1.17.4 | universal launcher shims | `TestPlatform_WindowsHookLauncherForms`, `TestPlatform_PluginRootWithSpacesAndUnicode` · test/platform | renamed |
| 1.17.5 | launcher overhead in B-D | bench-hotpath --bundle universal | superseded-guarantee(E-2) |
| 1.17.6 | per-platform pays no launcher cost; **B-A<15/B-E<2s** | bench-hotpath --bundle native | superseded-guarantee(E-2) |
| 1.17.7 | install-gate works | `TestInstall_HostCLIInstallUpgradeUninstall` · test/e2e; historical `devtool install-check` has no current entry point | replaced — live NOT-RUN |
| 1.17.8 | clean uninstall byte-identical | `TestInstall_HostCLIInstallUpgradeUninstall` · test/e2e (replaces `test/install/TestUninstallLeavesNothing`) | renamed/relocated |
| 1.17.9 | upgrade safety / schema-bump quarantine | `TestFsck_ANewerCheckpointSchemaIsNotCorrupt` · cli; `TestGetRefusesANewerSchemaVersion` · checkpoint; `TestInstall_...Upgrade...` (replaces `TestUpgradeAcrossVersions`/`_CheckpointSchemaBump`) | renamed |
| 1.17.10 | cross-platform matrix | `test/platform/*` (`TestPlatform_ProjectRootShapes`, `_ReadOnly*`, `_MixedCaseProjectRoot`) | renamed |
| 1.17.11 | hooks exit 0 full fault matrix (54) | `test/fault/*` (`TestFault_Lifecycle`, child/resources/publication) + cli `TestFaultActive_*` (replaces `TestHooksExitZero_Matrix`) | renamed/relocated |
| 1.17.12 | security audit, 3 proofs | `test/security/*` (`TestSecurity_TelemetryCannotBeTurnedOn`, `_NoSecretReachesAnyDurableSurface`, `_MCPServerNeverImportsOSExec`); `TestGuard_NoNetworkImports` · test/guards; historical `devtool security-audit` has no current entry point | replaced |
| 1.17.13 | govulncheck/gosec | CI security job | retained — NOT-RUN |
| 1.17.14 | §12.3 nine degradation rows | replaced by `test/fault/*` suite (different decomposition, not the 9 `TestDegradation_*` names) | replaced — see F-4 |
| 1.17.15 | `qompack fsck` | `internal/cli/fsck_test.go` (`TestFsck_ReportsEachSeededDefectAndRepairsNone`, `_RepairQuarantinesAFailingObjectAndRebuildsTheFilter`, `_ReportsEveryDanglingReferenceClass`) | renamed |
| 1.17.16 | `qompack doctor` 16 checks | `internal/cli/doctor_test.go` (`TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt`, `_AgreesWithStatusOnModeAndProvenance`, `_EveryMigrationGateIsPendingInThisBuild`) | renamed |
| 1.17.17 | version drift guard; single source | `TestVersionLdflags`, `TestReleaseCheckVersion`, `TestValidateBundleVersion`, `TestGoModVersions` · tools/devtool | renamed |
| 1.17.18 | release pipeline dry run | actionlint/goreleaser snapshot; `TestReleaseCheckDeterminismVersion` | retained — NOT-RUN |
| 1.17.19 | four new CI jobs required | inspect branch protection / ci.yml | unknown (needs repo/CI inspection) |
| 1.17.20 | checksum file format | `head SHA256SUMS`; packaging shape canary | retained |

### 1.18 SP-18 — docs/UAT → SP-18 SP18-M7-01–07 + UAT-01–12  (**substantially restructured — see F-5**)

| ID | Historical | Current source (symbol · pkg) | Disp. |
|---|---|---|---|
| 1.18.1 | owned doc set = 21 docs | `TestOwnedDocsExist` · test/docs (the `IsTwentyOne`/`HasNoDuplicates`/`StartWithH1` count assertions **not present** under those names) | renamed/partial |
| 1.18.2 | config ref never stale/complete | `TestGenConfigDocs_CheckDetectsMissingAndStale`, `_CommittedPageIsCurrent`, `_LeavesMatchDefaultsOneToOne`, `_GatedSwitchesRenderFromSource` · tools/devtool | renamed |
| 1.18.3 | config-ref ranges = Validate() | no standalone `TestMetaRangesMatchValidate`; range/leaf agreement carried by `TestGenConfigDocs_LeavesMatchDefaultsOneToOne` · tools/devtool | partial — F-5 |
| 1.18.4 | runtime namespace additive | no standalone `TestConfigReferenceRuntimeSectionIsMarkedAdditive`; additive/gated rendering carried by `TestGenConfigDocs_GatedSwitchesRenderFromSource` · tools/devtool | partial — F-5 |
| 1.18.5 | cannot-do list verbatim | `TestCannotDoCoversSection12Limits`, `TestCannotDoNamesTheUnimplementedChecks` · test/docs (replaces `TestCannotDoListVerbatim` + 3) | renamed |
| 1.18.6 | upstream issues + template | `TestUpstreamIssuesAreAllUnfiled` · test/docs | renamed |
| 1.18.7 | loud glossary covers contract IDs/failures | `TestTroubleshootingNamesEveryContractAssertion`, `_NamesEveryEvidenceOutcome`, `_NamesEverySelfTestCheck` · test/docs (replaces `TestLoudGlossary*`) | renamed |
| 1.18.8 | ADRs D1–D12 present/correct | `TestADRIndexListsEveryADR` · test/docs (replaces `TestAdrShape`/`_IndexEntriesExist`/`_NamesItsDecisionID`) | renamed/partial |
| 1.18.9 | architecture quotes ten invariants | no verbatim-invariants test (`TestArchitectureQuotesTenInvariants`/`_DigestSections`/`_LinksEveryADR` absent); ADR presence held by `TestADRIndexListsEveryADR` · test/docs | replaced/retired — see F-5 |
| 1.18.10 | hygiene: no placeholders/links resolve | `TestRelativeLinksResolve`, `TestNoPlannedPointerToAnExistingPage` · test/docs (subset of historical `TestNo*`) | renamed/partial |
| 1.18.11 | 12 UAT scenarios conformant + quote phase criteria | `TestUATRetainsAllTwelveIDs`, `TestUATSectionsHaveTheRecordShape`, `TestUATConfigKeysExist`, `TestUATUnexecutedRowsSayUnverified` · test/docs (no `TestUATQuotesPhaseExitCriteria`) | renamed/partial |
| 1.18.12 | **UAT executed end-to-end by a human** | manual, mandatory; test/docs only checks doc shape (`TestUATUnexecutedRowsSayUnverified`) | **unknown — human UAT NOT-RUN** (F-7) |
| 1.18.13 | generator determinism + **B-DOC <25ms** | generator relocated to `tools/devtool` root; determinism substance held by `TestGenConfigDocs_CommittedPageIsCurrent`/`_LeavesMatchDefaultsOneToOne`; B-DOC numeric budget demoted to diagnostic per E-2 (no `BenchmarkGenerate`) | replaced — see F-6 |
| 1.18.14 | doc-package import discipline (`configdocs`) | historical `configdocs` package removed; import discipline now applies to the relocated generator + `test/docs` | replaced — see F-6 |

---

## C. Migration additions (SP19/20/21) — gating state from actual config code

Source of truth: `internal/config/defaults.go:155-162` (Migration block) + `internal/config/runtime.go:118-157` (types/docs) + `internal/admission/types.go`. Gate table: `internal/config/migration.go`. **Every switch ships as below; disabled = refused-until-gate, recorded as disabled, never "passed".**

| Switch (config key) | Owner/gate | Shipped default | State | Evidence |
|---|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | SP-20 M1-01 | `false` | **DISABLED** (refused until M1) | defaults.go:157; runtime.go:129 |
| `runtime.migration.publication.durableFrontier` | SP-20 M1-02 | `false` | **DISABLED** | defaults.go:158; runtime.go:134 |
| `runtime.migration.reinjection.sessionStartCompact` | injection kill switch | `true` | **ENABLED** (the one tested injection adapter) | defaults.go:159; runtime.go:139 |
| `runtime.migration.replacement.newResult` | SP-21 M4 admission | `false` | **DISABLED** — admission ships off; pass-through | defaults.go:160; runtime.go:144; `admission/types.go:5,240` "False is the shipped value" |
| `runtime.migration.compaction.automaticVeto` | SP-19 M0-03 native veto | `false` | **DISABLED/refused** (native veto retired; no O(delta) claim) | defaults.go:161; runtime.go:150 |
| `runtime.migration.compaction.blockManualCompact` | §12 | `false` | **HARDWIRED off** (key exists only to say so) | defaults.go:161; runtime.go:151 |
| `runtime.migration.experiments.enabled` | SP-15/16 | `false` | **DISABLED** | defaults.go:162; runtime.go:156 |
| `runtime.pselection.submodularEnabled` | SP-15 | `false` | **DISABLED** | defaults.go:184 |
| `runtime.grammar.loopWarningsEnabled` | SP-15 | `false` | **DISABLED** | defaults.go:185 |

**SP-19 (M0 mapping/canaries/ledger/baseline/state):** `internal/state/*` (`TestSet_CannotAcquireUserAuthorityOrRewriteEvidence`, `TestSupersede_OnlyAuthoritativeSourcesMaySupersedeAuthoritativeState`, `TestConflict_IsRenderedUntilResolvedByAnAuthorizedSource`, `TestScope_DoesNotLeakAcrossSessionsOrWorktrees`, `TestDecode_RefusesDocumentsItCannotReadRatherThanGuessing`); contract producer wiring `options.go:339-352`; `TestDoctor_EveryMigrationGateIsPendingInThisBuild` · cli.
**SP-20 (M1–M2 capture/publication/backup/rollback/uncertainty/retention/scope):** `test/integration/capture_policy_test.go` (`TestCapturePolicyToHookEnvelopeRoundTrip`, `TestCapturePolicyProducesTheCompleteFidelitySet`); `test/fault/publication_test.go`; `internal/store/migrate_test.go` (`TestImport_LostCursorFallsBackToTheFrontierWithoutDuplicating`, `TestImport_LegacyUnknownFidelityIsNeverUpgraded`); `test/canary/optimize_test.go` (`TestCanary_CompactionBlocking`, `TestCanary_UsageAttribution`).
**SP-21 (M4 admission — opt-in, disabled):** `internal/admission/*` — `TestSwitchIsOffByDefault`, `TestADisabledGateOutranksAProcessedEnvelope`, `TestDisabledGateCapturesNothing`, `TestDisablingAdmissionCannotDisableRecording`, `TestPrivacyOutranksTheBypass`, `TestAdmissionIsIdempotentAcrossRedelivery` (processed-envelope recursion), `TestEveryMeaningFieldIsPreserved` (fidelity), `TestTheShippedComparisonIsInconclusive` (unmodified-output comparison), `TestRollbackOrderIsFixed`, `TestPostWriteRollbackValidatesReadBackOrRestores`; canary `TestCanary_NewResultReplacement`; release `TestSwitch_GatedCapabilitiesAreRefusedNotDisabled` · test/release.

---

## D. Section 3 — cross-component integration mapping (3.1–3.14)

**No `TestV6_*` function exists in base `301a8e9`; the new verification overlay is listed in §H.** The historical proposed names are identifiers only; the current tree realises the integration surface across `test/{canary,platform,release,security,fault,integration,e2e}` + `internal/cli` + `internal/pluginmanifest`. The genuine gaps are the **packaged-bundle-live** dimension and the **human/held-out evaluation** dimension (also flagged by the current plan §1/§7).

| §3 identifier | Historical intent | Current mapped case(s) | Disposition |
|---|---|---|---|
| 3.1 PackagedBundleObservesARealSession | packaged disposable-session, host version, recovery | `TestCanary_HostInventory`, `TestCanary_RegisterIsInternallyConsistent` · test/canary; `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` · e2e | partial — packaged live NOT-RUN |
| 3.2 UniversalLauncherPreservesHookSemantics | target OS/spaces/managed/competing-hook | `TestPlatform_WindowsHookLauncherForms`, `_PluginRootWithSpacesAndUnicode`, `_ReadOnly*` · platform; `TestCanary_CompetingHooks`, `_InvalidHookPayload` · canary | mapped |
| 3.3 DocumentedCommandsRunAgainstShippedBundle | docs' commands vs released artifact | `TestCanary_PluginValidate` · canary; `TestGenCommandDocs_MatchesTheInstalledHelp` · devtool (source-derived, not shipped-binary) | partial — against-shipped-artifact NOT-RUN |
| 3.4 EliminationStalenessSurvivesPackaging+MCP | stale/uncertain survive upgrade & packaged MCP | `TestV3_ObserverFileVersionsDriveEliminationStaleness` · e2e; `TestCanary_MCPLauncherDiscoversTools` · canary | partial — upgrade-survival NOT-RUN |
| 3.5 EphemeralRetrievalResultsAreEvictedFirst | **native eviction assertion** | **RETIRED** per current plan; representation policy in `TestPropose_ChoosesAtMostOneRepresentationPerItem`; legacy `TestV4_EphemeralRetrievalResultsRankFirstForEviction` · e2e | retired/replaced |
| 3.6 FsckRepairsSeededCorruption | fsck audit/repair preserves live | `internal/cli/fsck_test.go` (seeded-defect + repair suite); `TestFault_AuditSeesADeletedObjectUnderALiveIndex` · fault | mapped — packaged fsck live NOT-RUN |
| 3.7 DoctorAgreesWithStatus+Subsystems | doctor/status agree incl. unknown/degraded | `TestDoctor_AgreesWithStatusOnModeAndProvenance`, `_ReportsCapabilityEvidenceWithoutInventingIt` · cli | mapped |
| 3.8 ConfigReferenceDescribesTheBinary | source-derived setting/command inventory | `TestGenConfigDocs_LeavesMatchDefaultsOneToOne`, `TestUserGuideCoversEveryGeneratedCommandAndTool` · devtool/docs | mapped |
| 3.9 InstallUpgradeUninstallByteIdentical | install/upgrade/uninstall + rollback | `TestInstall_HostCLIInstallUpgradeUninstall` · e2e; admission `TestRollbackOrderIsFixed` | mapped — full packaged NOT-RUN |
| 3.10 DegradedPassiveFromPackagedBundle | packaged unknown-schema safe degrade | `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` · e2e; `TestPlatform_UnknownSettingsVersion`, `TestPlatform_UnsupportedOptimizationsDisabled` · platform | mapped — packaged NOT-RUN |
| 3.11 NoSecretAndNoNetworkFullPackaged | no leak/network across session | `TestSecurity_NoSecretReachesAnyDurableSurface`, `_TelemetryCannotBeTurnedOn`, `TestSecurity_MCPServerNeverImportsOSExec` · security; `test/canary` network posture | mapped — full-session live NOT-RUN |
| 3.12 CheckpointToRehydrationRoundTripBundle | repeated/missing/failed compaction recovery | `TestE2E_SessionStartCompactAfterFailedSummary`, `TestE2E_Checkpoint*` · e2e; `TestFault_CheckpointDropsAnUnresolvablePointer` · fault | mapped — bundle NOT-RUN |
| 3.13 ReleaseArtifactsReproducible | exact identifiers/licenses/reproducibility | `TestAssembleBundle_Deterministic`/`_ChecksumsFormat`/`_RefusesEscapingVersion`, `TestWriteArchiveChecksums`, `TestValidateBundleVersion`, `TestReleaseCheckDeterminismVersion`, `TestReleaseScope_ReleaseCheckVerifiedRaisesM707` · tools/devtool; `TestBundle_OnDiskMatchesGenerator` · pluginmanifest; `TestCanary_PackagingShape` · canary | mapped — bundle-assembly reproducibility run NOT-RUN (no `devtool package` command; entry point is the bundle/archive assembler above) |
| 3.14 HotPathHoldsEverySubsystemResident | full-resident latency on quiet targets | `TestIntegration_HotPathWarmWithRealResidentState`, `_HotPathDegradesRatherThanBlocks` · integration | mapped — measured latency NOT-RUN (quiet runner) |

---

## E. Superseded assertions — explicit replacements (current plan §5/§6)

- **E-1 Native-control / guaranteed-savings retired.** No native compaction veto, no O(delta) native latency, no guaranteed first-turn savings. Replacement: `runtime.migration.compaction.automaticVeto=false` (refused, `TestCanary_CompactionBlocking`), `blockManualCompact` hardwired false; scheduler is advisory only. §3.5 native ephemeral-eviction retired → representation policy (`TestPropose_ChoosesAtMostOneRepresentationPerItem`). Admission ships disabled → pass-through, output unmodified (`TestTheShippedComparisonIsInconclusive`, `TestDisablingAdmissionCannotDisableRecording`).
- **E-2 Numeric budgets are diagnostics, not acceptance facts.** B-A p99<15ms, B-B<2ms, B-E<2s, B-F p95<250ms, dedup ≥4.0, 4:1, sublinear growth, fraction-of-OPT — all retained as *measured-and-reported* per §5, never as a universal target. Current plan §5 requires re-measuring with binary/provider/OS/payload context. Rows: 1.1.27, 1.2.12, 1.4.14, 1.5.12, 1.6.16/17/19, 1.7.7/10, 1.8.10/11/13, 1.9.11/12, 1.10.16/17/18, 1.11.15/16, 1.12.14/16/17, 1.13.16, 1.15.12/13/14, 1.16.5/11, 1.17.3/5/6. Hosted-runner fsync tail is a known measurement artifact (owner decision Q1) and must not be re-derived into a constant.
- **E-3 Unchanged-Qompack assertion retired (1.1.28); documentation-baseline version discrepancy recorded.** The historical "byte-identical to the initial commit" rule is retired for the authorized revision (design doc now carries a Revision log). **Discrepancy to record, not adjudicate:** the current supplied user plan names documentation **v1.4** as the authorized planning baseline, whereas the integrated delivery ships `Qompack.md` at **v1.5** (a historical fact of the merged tree). This session does **not** authorize or redefine the baseline to v1.5; it only records that the authorized-baseline version (v1.4) and the shipped-doc version (v1.5) differ, and leaves reconciliation of which version governs to the plan owner. `Qompack.md`, current plan §6.

---

## F. Findings — divergences, retirements and standing obligations (source paths)

Findings are classified by what the **current** owner criterion requires. F-1/F-2/F-4/F-7 are live divergences or standing obligations. F-3/F-5/F-6/F-8 are historical assertions **retired or replaced** by current criteria — recorded so no ID is silently dropped, explicitly **not** recommendations to re-add ceremonial tests that the current plan has retired.

- **F-1 (1.1.24) SP-15 guard rename not performed.** Historical row requires, at V6, `TestGuard_SubmodularDefaultsOff` to be **absent** and `TestGuard_SubmodularEnabledOnlyAfterPSelection` to be **present**. Current `test/guards/buildorder_test.go` still holds `TestGuard_SubmodularDefaultsOff`; the enabled-only-after-p-selection guard is **absent**. Behaviour is covered elsewhere (`TestDefaults_SubmodularEnabledDefaultsFalseAndHidden`, `TestSubmodularEnabled_DerivedFromRuntime` · config; `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`). Decision for Main: either the historical rename expectation is withdrawn (current normative plan does not demand it) or SP-15 should re-point the guard. Not a correctness defect; a plan-vs-code naming divergence to record. `test/guards/buildorder_test.go`.
- **F-2 (1.5.15 / 1.13.14) Contract producers wired but real observation NOT-RUN.** All nine producers are declared in source at `internal/daemon/options.go:339-352` (five unconditional; PreCompact/AdditionalContext/MCPRegistered conditional on the composed `Services`). Whether each reports a real observation rather than `not-yet-implemented` depends on the composed daemon having Checkpoint/Rehydrate/MCPInitialized non-nil at runtime — Main must confirm on the candidate via `qompack self-test` / `status`. Guard: `internal/daemon/options_test.go` `TestDeclaredProducerSetMatchesArchitecture`.
- **F-3 (1.15.6 / 1.15.2) Submodular (1−1/e) guarantee is retired by the current criterion, not a missing gate.** `SubmodularEnabled` defaults false; the current SP-15 criterion is a *feasible actual consumer* with diagnostic signals and bounded warnings — it explicitly does **not** assert a guaranteed approximation bound. So the absent `PropGuaranteeUnitCost`/`PropGuaranteeKnapsack` rapid property tests are **retired-with-the-guarantee**, and restoring them would contradict the feasible-consumer criterion — this report does **not** recommend adding them. Current substance is held by `TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer` (the shipped lazy-greedy is equivalent to naive on its inputs). 1.15.2's "Δ ranks negative-knowledge highest" maps to `TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence` + `TestCheapScorer_ACoveringContinuationOutscoresAnEmptyOne`; no new test required. Path: `internal/analyzer`, `internal/config/defaults.go:184`.
- **F-4 (1.17.14) §12.3 nine-row degradation decomposition replaced.** The nine named `TestDegradation_*` (DaemonUnreachable, SpoolWriteFails, StoreCorrupt, ManifestMismatch, BloomLoadFails, ConfigInvalid, MCPToolPanic, HookPanic, PreCompactTimeout) do not exist; `test/fault/*` covers the same faults under a different structure (`TestFault_DaemonKilledMidIngest`, `TestFault_WriteFailures`, `TestFault_MCPChildKilledMidRequest`, cli `TestFaultActive_*`). Worth a mapping doc asserting each §12.3 row has ≥1 driven-failure test + recovery paragraph, so the nine-row obligation is provably met rather than lost in the rename. Path: `test/fault/`, `docs/security.md`.
- **F-5 (1.18.x) SP-18 doc suite restructured; historical fixed-count/verbatim/name assertions replaced per current criteria — not gaps to re-add.** The current SP-18 criterion is "documentation matches shipped evidence and real authorized workflows"; it does **not** mandate a fixed doc count (historical 1.18.2 already said "record the count, do not assert one"), verbatim-block equality, or a ten-invariants recital. So `OwnedDocs...IsTwentyOne`, `CannotDoListVerbatim`, ADR `NamesItsDecisionID`, `MetaRangesMatchValidate`, `UATQuotesPhaseExitCriteria`, `ArchitectureQuotesTenInvariants` are **replaced** by the lighter current checks (`TestOwnedDocsExist`, `TestCannotDoCoversSection12Limits`/`_NamesTheUnimplementedChecks`, `TestADRIndexListsEveryADR`, `TestGenConfigDocs_LeavesMatchDefaultsOneToOne`, `TestUATSectionsHaveTheRecordShape`/`_RetainsAllTwelveIDs`, `TestTroubleshootingNamesEveryContractAssertion`), which enforce the current substance. The only item Main should positively confirm is that some current test still ties the architecture digest to the invariant set; if none does, that is a documentation-completeness choice for the SP-18 owner, not a mandatory V6 test. Path: `test/docs/`, `docs/architecture.md`.
- **F-6 (1.18.13 / 1.18.14) `tools/devtool/configdocs` package removed; generator relocated — a replacement, not a mandatory gap.** Config-doc generation now lives at `tools/devtool` root and is checked by `TestGenConfigDocs_CommittedPageIsCurrent`/`_CheckDetectsMissingAndStale`/`_LeavesMatchDefaultsOneToOne` (the "current, never stale, complete" substance). The historical **B-DOC `BenchmarkGenerate` <25ms** is a numeric budget and is demoted to a diagnostic per E-2 — not a required V6 gate; this report does **not** recommend re-adding a ceremonial determinism/budget suite. 1.18.14's import-discipline assertion simply re-points from the removed `configdocs` package to the relocated generator + `test/docs`. Main should re-point the assertion's package target; no new test is owed. (Canon fuzz name for 1.4.15 is resolved: `FuzzCanonicalize`.) Path: `tools/devtool/`.
- **F-8 (1.16.10) Prefix-reordering non-delivery is a declared-non-delivery, satisfied by an ADR statement.** `TestPrefixReorderingNotAttempted` is absent tree-wide, but SP-16's criterion for this item is a *declared* non-delivery, not an executable gate — the guarantee is that the capability is not attempted and that the ADR says so. Disposition is `replaced` (ceremonial test → ADR prose). Main should only verify `docs/adr/0016-phase7-refinements.md` still carries the non-delivery sentence; no test is owed. Path: `docs/adr/`.
- **F-7 (1.18.12 / §3, §7) Human UAT and live packaged/held-out evaluation are the standing gaps.** `test/docs` proves the UAT document's shape and that unexecuted rows say "unverified" (`TestUATUnexecutedRowsSayUnverified`), but the twelve UAT verdicts require a human against a real project — mandatory for V6, cannot be substituted by a mocked CLI. Likewise §3.1/3.3/3.9/3.11/3.13/3.14 need the actual packaged bundle on supported OSes and §5's held-out/changing-requirement evaluation. These are Main/human obligations, recorded here as `unknown` until executed.

---

## H. Working-source overlay (uncommitted; Main-authored, NOT in base `301a8e9`)

At the cut of this reconciliation the base was clean; Main is now authoring new `TestV6_*` cases in the worktree. These are **additive current working-source**, mapped here separately from the committed base and **excluded from the 4 620 base index**. Their execution belongs to Main's overlay (§G) — this report invents no result for them.

| Overlay file (untracked) | New symbol(s) | Maps additively to | Note |
|---|---|---|---|
| `test/security/v6_authorization_test.go` | `TestV6_ArchivedReadRetainsItsAuthorizationBoundary`, `TestV6_HashAddressesDoNotBypassPathAuthorization` | §3.11 (no-secret/authorization), 1.13.5 (re_read historical), 1.5.20/1.17.12 (security posture) | strengthens the packaged/authorization boundary the historical `TestV6_*` names only proposed |
| `test/fault/v6_fsck_test.go` | `TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence` | §3.6 (fsck), 1.17.15 (fsck), SP-20 M1 publication/capture-evidence | fsck diagnostic over unpublished-capture evidence |

Per Main's note, the original run of the overlay produced **three failed trust artifacts** and the **fsck diagnostic later passes**; those outcomes are Main's to record in §G. This inventory does not assert PASS/FAIL for any overlay case — it only maps them to the IDs/§3 identifiers they extend so mapping and execution reconcile.

---

## G. Execution ledger (Main fills from real V6 runs) — keyed overlay

**The coordinator execution overlay is [execution.tsv](execution.tsv), with one explicit result for every retained ID. A focused supporting artifact is not a full-row PASS.** Main supplies one execution overlay keyed by row ID / family / §3 identifier (and by overlay symbol for §H), so each mapped row resolves to exactly one artifact without duplicating a Result column across 304 cells. Main attaches: branch/HEAD + dirty baseline (now includes the §H overlay); supported OS/provider/model/host/plugin/schema versions; per-row result keyed to the old→new assertion map already provided here; commands actually run + artifact paths; skips/failures/waivers; migration-gate statuses (all shipped disabled except reinjection); request-usage completeness + estimated-price provenance; statistical outcomes; privacy/retention + rollback drill; independent findings; remaining blockers. No historical PASS is to be copied; use `documented`/`verified_in_target`/`implemented_unverified`/`unsupported`/`experimental`/`unknown` per the current plan §8.

**Reconciliation status:** all 304 retained IDs (1.1.1–1.18.14) accounted for with no silent replacement; mapping is left explicitly incomplete (`verify`/`unknown`/`partial`) where the committed base gave insufficient evidence. SP19/20/21 additions and §3.1–3.14 mapped; the §H uncommitted overlay mapped additively. Eight findings (F-1…F-8): F-1/F-2/F-4/F-7 are live divergences/obligations, F-3/F-5/F-6/F-8 are retirements/replacements reconciled to current owner criteria. This is a scope/mapping inventory, not runtime assurance. Stops here for independent review.

## Coordinator disposition after inventory review

F-1: retire the historical guard-rename expectation; the current criterion requires honest capability gating, not a particular function name. F-4 remains incomplete until all nine degradation obligations are individually linked to current driven failures. Source-symbol inspection is partial evidence, not acceptance. The authoritative current run/status overlay is [execution.tsv](execution.tsv); the report is [V6-report.md](../../V6-report.md). Historical budget labels and fixed counts do not become release criteria.

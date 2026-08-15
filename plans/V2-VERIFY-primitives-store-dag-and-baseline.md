# V2 — Verification checkpoint: primitives, store, DAG, daemon, and the Phase-0 baseline

**This is a standalone prompt. Execute it exactly as written. Do not skim.**

---

## When this runs

This checkpoint runs **after every wave-1 subplan branch has merged into `develop`, and before any wave-2 branch is cut.**

Wave 1 is the six subplans SP-02 … SP-07 (`00-ARCHITECTURE.md` §14). Their branches merge into `develop` with `--no-ff`, in this order (`00-ARCHITECTURE.md` §9 "at the end of a wave, all of that wave's branches merge into `develop` **in the stated order**", with SP-06's own constraint that it lands after SP-03 and SP-04):

1. `feat/sp02-replay-harness-belady-baseline`
2. `feat/sp03-sketch-library`
3. `feat/sp04-chunking-canonicalization-and-symbols`
4. `feat/sp05-daemon-ipc-and-hot-path`
5. `feat/sp06-content-addressed-store`
6. `feat/sp07-dependence-dag-and-slicing`

Before you begin, confirm the wave actually landed:

```bash
git checkout develop && git pull --ff-only
git log --oneline --merges -12
# Expect six --no-ff merge commits, one per branch above, in that order.
git log --format=%B origin/main..develop | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'
# Expect: no output.
```

**Work happens on `verify/v2`, cut from `develop`:**

```bash
git checkout develop && git pull --ff-only
git checkout -b verify/v2
go run ./tools/devtool ci-local     # baseline; record the result before changing anything
```

Every fix this checkpoint produces is committed to `verify/v2`. When the whole checkpoint is green, `verify/v2` merges back into `develop` with `--no-ff` and `develop` is tagged `v0.1.0` (`00-ARCHITECTURE.md` §9). **No wave-2 branch (`feat/sp08-observer-l0`, `feat/sp09-negative-knowledge`) may be cut until that merge has happened.**

### What this checkpoint is

It is **not** a smoke test. It is an exhaustive re-verification of *every functionality that exists in the codebase at this point*. Wave 1 built six packages in parallel against SP-01's `ErrNotImplemented` stubs and against `testdata/golden/contracts/**` fixtures (Rule W-2). This is the first moment those six real implementations have ever been compiled, run, benchmarked, and measured **together**. Three classes of defect can only surface here:

- **W-2 fixture divergence.** SP-06 tested `store.Put` against SP-01's synthetic `chunk`/`canon`/`symbols`/`sketch` fixtures. SP-04's real canonicalizers and SP-03's real MinHash may not reproduce those bytes. `00-ARCHITECTURE.md` §5.22 Rule W-2 is explicit: *"Any fixture that the real implementation cannot reproduce is a verification failure, not a fixture bug."*
- **Budget composition.** SP-05 measured B-A p99 < 15 ms against a daemon holding *stub* sketches, a *stub* DAG and a *stub* store. The real ones are ~68 KB of sketches plus a 5 000-node graph plus a 2 000-root index. §8.1's budget is only proven when the daemon is warm with the real thing.
- **Exit-criterion composition.** The §10 Phase 1 exit criterion (`≥ 4:1` dedup) needs SP-04's canonicalizers *and* SP-06's store. Neither branch could prove it alone.

### Do not test forward

Nothing from SP-08, SP-09, SP-10, SP-11, SP-12, SP-13, SP-14, SP-15, SP-16, SP-17 or SP-18 is in scope. `internal/observer`, `internal/negknow`, `internal/checkpoint`, `internal/pins`, `internal/rehydrate`, `internal/rules`, `internal/skills`, `internal/scheduler`, `internal/mcp`, `internal/commands`, `internal/analyzer`, `internal/grammar` are still `core.ErrNotImplemented` stubs. Their conformance suites must still report skipped behaviour blocks with the exact Rule W-1 message. Asserting anything else about them is a checkpoint failure, not a bonus.

Three specific consequences, so nobody "helpfully" over-tests:

- Budget **B-F** (`mcp_tool_call` p95 < 250 ms) has no producer until SP-13. It is measured as **N/A**, not as a pass.
- Budget **B-E** (`checkpoint_finalize` p99 < 2 s) *is* in force — `qompack checkpoint` exists as a thin client (SP-05) — but it currently exercises the client + daemon + nil-`Services.PreCompact` path only. Record it honestly with that annotation.
- `contract.Monitor` must report `ModeFull` with exactly **four** assertions at `OK:true, SevInfo, Observed:"not-yet-implemented"` (`00-ARCHITECTURE.md` §12.1). Five is a regression; three means someone wired a wave-3 producer early.

### How to execute this checkpoint

Fan the §2 inventory out across **seven parallel subagents, one per subplan group**, then run §4 in the main session.

| Subagent | Owns | Returns |
|---|---|---|
| **V-A** | §2.1 SP-01 foundation inventory (V2-SP01-*) | pass/fail per row + the exact command output for every failure |
| **V-B** | §2.2 SP-02 replay/Belady/baseline (V2-SP02-*) | pass/fail per row, the `fraction_of_opt` numbers for `stock`/`null`/`oracle`, the reproducibility diff |
| **V-C** | §2.3 SP-03 sketches (V2-SP03-*) | pass/fail per row, the five Appendix-A sizing numbers, the `L0SketchUpdate` ns/op and alloc count |
| **V-D** | §2.4 SP-04 chunk/canon/symbols (V2-SP04-*) | pass/fail per row, the boundary-stability histogram, the dedup-report `gain` per group |
| **V-E** | §2.5 SP-05 daemon/IPC/contract (V2-SP05-*) | pass/fail per row, the 66-row fault table, the bench-hotpath JSON |
| **V-F** | §2.6 SP-06 store/redact/tokens (V2-SP06-*) | pass/fail per row, `Stats.DedupRatio` on the read-heavy fixture, the GC report |
| **V-G** | §2.7 SP-07 DAG/slicing (V2-SP07-*) | pass/fail per row, the slice latency medians, the thin-vs-full table |

Rules for the fan-out, which are the same rules the subplans used:

- Every subagent runs **read-only verification commands**. A subagent that finds a failure **reports the failing command and its full output**; it does not edit source, does not weaken an assertion, does not add `//nolint`, `t.Skip`, or a `nomagic:allow`.
- All fixes, all `git` operations and all commits happen in the **main session** (§7).
- §4 (new integration tests), §5 (performance budgets) and §6 (regression) are **main-session only**. §4 authors permanent test files; §5 must be measured on one quiet machine so the numbers are comparable; §6 needs the whole picture.
- Subagents run against a clean `verify/v2` working tree. Give each one this file, `00-ARCHITECTURE.md` §3.2/§5/§6/§7, and its own subplan file.

---

## Cumulative functionality inventory

Every functionality that exists on `develop` at this point, grouped by the subplan that delivered it. Each row is a **checklist item with an exact command and an exact expected result**. Run every row. "Passes" means the command exits 0 *and* the stated observable holds.

Unless stated otherwise, every command runs from the repository root `C:/Users/Quant/Documents/Programming/Projects/qompack`.

### 2.1 SP-01 — foundation, toolchain, contracts (wave 0, re-verified)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP01-01 | Repo shape: `main`/`develop`, root commit contents | `git log --oneline --all \| tail -3` ; `git show --stat $(git rev-list --max-parents=0 HEAD)` | root commit contains only `Qompack.md`, `plans/*`, `.gitignore`, `LICENSE` |
| V2-SP01-02 | Module builds; vet clean | `go build ./... && go vet ./...` | exit 0, no output |
| V2-SP01-03 | Full lint chain: golangci-lint, `nomagic`, importgraph, testdeps, bindeps, sleepcheck, stubskips | `go run ./tools/devtool lint` | exit 0. `importgraph` passes against the real repo with six new real packages; `bindeps` proves `go list -deps ./cmd/qompack` contains only stdlib, `github.com/qompack/qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| V2-SP01-04 | Formatting | `go run ./tools/devtool fmt-check` | prints nothing |
| V2-SP01-05 | `internal/core`: hash, ids, clock, sentinels | `go test ./internal/core/...` | all green, incl. `TestHashBytes_DomainSeparation`, `TestHash_StringShortParse_RoundTrip`, `TestNewDecisionID_Format`, `TestSentinels_AreDistinct` |
| V2-SP01-06 | `internal/paths`: resolve, Norm/Key, long paths, atomic write | `go test ./internal/paths/...` | all green, incl. `TestResolve_WalksToGitDir`, `TestNorm_RejectsEscape`, `TestLongPath_Over260`, `TestWriteAtomic_ReplacesAndSyncs` |
| V2-SP01-07 | **Append-only guard** (§7.4 / §4.6, mechanical) | `go test -run TestAppendOnlyGuard ./internal/paths/` | all five illegal writes against `checkpoints/`, `pins/`, `sketches/tried.bloom` fail; `TestCreateNew_SetsReadOnly` and `TestReplaceBloom_KeepsOneBackup` green |
| V2-SP01-08 | Appendix C defaults reproduced verbatim | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green — `Defaults()` minus `runtime` deep-equals `testdata/golden/config/appendix-c.jsonc` |
| V2-SP01-09 | Config: 5-layer precedence, deep merge, env mapping, null semantics | `go test ./internal/config/...` | all green, incl. `TestLoad_PrecedenceFiveLayers`, `TestLoad_DeepMergePerLeaf`, `TestLoad_EnvKeyMapping`, `TestLoad_NullMeansMeasure` |
| V2-SP01-10 | Config: invalid leaf falls back, never crashes; unknown key warns | `go test -run 'TestLoad_InvalidLeafFallsBackNotCrash\|TestLoad_UnknownKeyWarnsNeverErrors\|TestValidate_' ./internal/config/` | green; `TestValidate_RuleTableIsComplete` proves every leaf has a validation decision |
| V2-SP01-11 | Config schema + generated docs never drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | exit 0, clean |
| V2-SP01-12 | Logging `Loud` channel: three destinations | `go test -run 'TestLoud_ThreeDestinations\|TestLogger_' ./internal/logging/` | green |
| V2-SP01-13 | `obs`: log-bucket histograms, budget IDs B-A…B-F | `go test ./internal/obs/...` | green, incl. `TestBudgets_AllSixPresentAndConfigDriven`, `TestHistogram_PercentileConservative`, `TestCheckBudgets_CountsConsecutiveWindows` |
| V2-SP01-14 | `hookio`: all seven payloads, unknown-field tolerance | `go test ./internal/hookio/...` | green, incl. `TestReadEvent_AllSevenHookPayloads`, `TestReadEvent_UnknownFieldsPreserved`, `TestReadEvent_MissingFieldsNeverPanic` |
| V2-SP01-15 | **Hooks always exit 0** (30 fault combinations) | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` | green — 30/30 exit 0, stdout always parses as `hookio.Output` |
| V2-SP01-16 | Plugin bundle generated, never hand-edited | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | exit 0, clean; 6 hooks + 7 commands present |
| V2-SP01-17 | Six-target cross build | `go run ./tools/devtool build-all` | all six `dist/qompack-<os>-<arch>[.exe]` produced |
| V2-SP01-18 | Conformance suites still enforce Rule W-1 for **unimplemented** packages | `go test ./internal/... 2>&1 \| grep -c 'behaviour: implementation is a stub (Rule W-1)'` | > 0, and **every** remaining skip in the tree carries that exact message or `contract fixture not yet recorded (Rule W-2)`. See V2-SP01-19 for which packages may still skip. |
| V2-SP01-19 | Rule W-1 skips are gone from every wave-0/1 package | `go run ./tools/devtool lint` (`stubskips` sub-check) ; `grep -rn "t.Skip" internal/sketch internal/chunk internal/canon internal/symbols internal/store internal/redact internal/tokens internal/dag internal/ipc internal/contract internal/eval` | **no output** from the grep. Remaining legal skips exist only in `analyzertest schedulertest checkpointtest pinstest rehydratetest rulestest skillstest mcptest negknowtest grammartest observertest` |
| V2-SP01-20 | Build-order guards (closing note 1–4) | `go test ./test/guards/...` | green: `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull` |
| V2-SP01-21 | No network, no telemetry, write set confined | `go test -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' ./test/guards/` | green — `net/http`, `net/url`, `crypto/tls` absent; `net` only in `internal/ipc` |
| V2-SP01-22 | e2e: all six hooks against the real binary | `go test -run TestE2E_AllSixHooksExitZero ./test/e2e/` | green — six exit codes 0, six hook-log lines |
| V2-SP01-23 | `tokens` baseline behaviour preserved after SP-06's edit | `go test -run 'TestClassify_Table\|TestEstimate_ImageFromDimensions\|TestEstimate_ImageCappedAt1600\|TestEstimate_PDFPageCount\|TestEstimateRoot_SumsChunks\|TestCalibrate_ClampsAndPersists' ./internal/tokens/` | all six SP-01 tests still pass **unmodified** (SP-06 was allowed to rewrite only `TestEstimate_ProseVsCode`) |
| V2-SP01-24 | Pure functions SP-01 implemented rather than stubbed | `go test -run 'TestRootHash_Formula\|TestYoungDaly_Formula\|TestSkiRental_ComputedNotLiteral\|TestDescriptorKey_Stable\|TestStripInjections\|TestTombstone_MatchesDesignExample' ./internal/...` | green; `grep -R "12\.5" internal/ --include=*.go` outside `_test.go` returns nothing |
| V2-SP01-25 | SP-01 benchmark budgets still met | `go test -bench 'BenchmarkHistogram_Observe\|BenchmarkConfigLoad_ColdNoFiles\|BenchmarkPathsWriteAtomic_4KB' -benchmem ./internal/obs ./internal/config ./internal/paths` | < 100 ns/op, < 2 ms/op, < 2 ms/op respectively |
| V2-SP01-26 | Commit-message policy machinery | `go test -run TestCheckCommitMsg ./tools/devtool/` | green — rejects `Co-Authored-By`, `🤖`, over-length subjects, missing `Refs:` |

### 2.2 SP-02 — replay harness, Belady OPT, Phase-0 baseline (`internal/eval`, `test/replay`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP02-01 | Whole package green | `go test -race ./internal/eval/...` | exit 0 |
| V2-SP02-02 | Block/demand derivation (`Blocks`, `Demands`, `approachClass`) | `go test -run 'TestBlocks_\|TestDemands_\|TestApproachClass_' ./internal/eval/` | green, incl. `TestBlocks_PositionsAreCumulative`, `TestDemands_OnlyPreCompactionBlocks`, `TestDemands_EliminationMatchByApproachClass` |
| V2-SP02-03 | **Belady OPT** (§6.10) — knapsack DP, determinism, budget respect, p_min | `go test -run TestBelady_ ./internal/eval/` | green, incl. `TestBelady_UnitWeightsMatchesClassicBelady`, `TestBelady_KnapsackBeatsGreedyDensity`, `TestBelady_BudgetNeverExceeded`, `TestBelady_PMinIsEarliestDropped`, `TestBelady_FallbackWhenDPTooLarge` |
| V2-SP02-04 | Breakpoint OPT (§5.6), permanently disclaimed | `go test -run TestBreakpointOPT_ ./internal/eval/` | green; `Plan.Note == NotPluginActionable` on every path |
| V2-SP02-05 | Policy registry + the three built-ins | `go test -run 'TestStockPolicy_\|TestNullPolicy_\|TestRegisterPolicy_\|TestPolicyNames_' ./internal/eval/` | green; `PolicyNames() == ["null","oracle","stock"]` |
| V2-SP02-06 | Counterfactual replay, deterministic mode | `go test -run TestReplay_ ./internal/eval/` | green, incl. `TestReplay_DeterministicAcrossRuns`, `TestReplay_OracleFewerRepairsThanStock`, `TestReplay_LiveModeRefusedWithoutEnv`, `TestReplay_HorizonRespected` |
| V2-SP02-07 | Divergence metrics — all five §4.2 bullets | `go test -run TestCompare_ ./internal/eval/` | green, incl. `TestCompare_IdenticalRuns`, `TestCompare_FirstDivergenceIsRelativeToCompaction`, `TestCompare_JaccardHalf`, `TestCompare_EditDistanceKnown` |
| V2-SP02-08 | Fraction-of-OPT scoring + every §11.2 secondary metric | `go test -run 'TestScoreRun_\|TestMetricsOf_\|TestReport_' ./internal/eval/` | green, incl. `TestScoreRun_FractionIsMicroAveraged`, `TestScoreRun_RewriteTokensSection52TableA/B`, `TestScoreRun_NoHardcodedMultiplier`, `TestMetricsOf_CoversEveryDirection` |
| V2-SP02-09 | Deterministic synthesizer + the committed 24-session corpus | `go test -run 'TestSynthesize_\|TestCorpus_' ./internal/eval/` | green, incl. `TestSynthesize_MatchesCommittedCorpus` (byte-identical), `TestSynthesize_EveryCompactionHasDemands`, `TestCorpus_ManifestHashesMatch` |
| V2-SP02-10 | Redacting importer for recorded corpora | `go test -run 'TestImport_\|TestRedact_\|TestImportCommand_' ./internal/eval/` | green; `TestImport_RefusesDestinationInsideRepo` and `TestImportCommand_NoRedactRequiresEnv` pass |
| V2-SP02-11 | Sublinear-growth checker (§11.3) | `go test -run TestCheckSublinearGrowth_ ./internal/eval/` | green — `Sublinear==true` at α≈0.62, false at α≈1.0, `Reason` set when inconclusive |
| V2-SP02-12 | `evaltest` conformance suite, zero skips | `go test -run 'Suite' ./internal/eval/...` ; `grep -rn "t.Skip" internal/eval` | suite green; grep returns nothing |
| V2-SP02-13 | **Replay gate driver** — 2 % rule, sign-off trailer, phase assertions | `go test ./test/replay/...` | green: all 19 `TestGate_*` rows, incl. `TestGate_TwoPercentBoundaryExclusive`, `TestGate_SignOffAllowsNamedMetricOnly`, `TestGate_Phase0ExitCriterion`, `TestGate_BloomFPCeiling`, `TestGate_GrowthInconclusiveFails`, `TestGate_PhaseChecksMayNotBeDisabledInCI` |
| V2-SP02-14 | **Phase-0 baseline is reproducible** | `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --to /tmp-out-a.json` then again `--to /tmp-out-b.json`; compare | byte-identical; both equal the committed `testdata/baseline/phase0.json` (write the two outputs into the scratch dir, never over the committed baseline) |
| V2-SP02-15 | Replay gate runs end to end against the corpus | `go run ./tools/devtool replay -- --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 0 --ci` | exit 0; report JSON parses; `policies.oracle.fraction_of_opt == 1.0`, `policies.null.fraction_of_opt == 0.0`, `policies.stock.fraction_of_opt` strictly between them on all 24 sessions |
| V2-SP02-16 | Honesty tags in the report | inspect the report JSON from V2-SP02-15 | every latency value tagged `"latency":"modelled"`; breakpoint plan carries `NotPluginActionable`; `retrieval_hit_rate: 0.0` accompanied by `retrieval_actions: 0`; `forfeited_discount_tokens` reported separately from `rewrite_tokens`; `corpusTier: "synthetic"` |
| V2-SP02-17 | `internal/eval` import purity | `go test -run TestImports ./internal/eval/` (`importgraph_test.go`) | `internal/eval` imports no `internal/` package outside `{core, paths, config, logging, obs}` |
| V2-SP02-18 | Fuzz target | `go test -run=XXX -fuzz FuzzRedact -fuzztime 60s ./internal/eval/` | zero crashers |
| V2-SP02-19 | SP-02 benchmark budgets E-1…E-5 | `go test -bench 'Belady\|Breakpoint\|Synthesize\|Compare' -benchmem ./internal/eval/` | E-2 ≤ 250 ms/op, E-3 ≤ 50 ms/op, E-4 ≤ 20 ms/op, E-5 ≤ 15 ms/op; E-1 (full corpus replay) < 120 s |
| V2-SP02-20 | Coverage floor | `go run ./tools/devtool cover` | `internal/eval` ≥ **85 %** |

### 2.3 SP-03 — sketch library (`internal/sketch`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP03-01 | Whole package green under race, twice | `go test -race -count=2 ./internal/sketch/...` | exit 0 |
| V2-SP03-02 | QPKS header: framing, CRC32C, version, param ordering | `go test -run 'TestHeader_\|TestHash128_' ./internal/sketch/` | green, incl. `TestHeader_FrameLayout`, `TestHeader_DetectsSingleBitFlip`, `TestHeader_RejectUnsortedParams`, `TestHeader_EncodeRejectsBadParamName` |
| V2-SP03-03 | **Bloom Appendix-A sizing** | `go test -run TestBloom_AppendixASizing ./internal/sketch/ -v` | `mRaw = 95_851`, `mBits = 95_872`, `k = 7`, body 11 984 bytes |
| V2-SP03-04 | Bloom: no false negatives, measured FP rate at capacity | `go test -run 'TestBloom_NoFalseNegatives\|TestBloom_EstimatedFPRateMatchesEmpirical' ./internal/sketch/ -v` | 10 000/10 000 members test true; empirical FP ∈ [0.008, 0.013]; `EstimatedFPRate()` ∈ [0.009, 0.012] |
| V2-SP03-05 | Bloom saturation + resize (§11.4, §12) | `go test -run 'TestBloom_Resize\|TestBloom_Saturated\|TestBloom_Stats' ./internal/sketch/` | `ResizeTarget` returns `(2×cap, fp, true)` above 0.5 fill and `(cap, fp, false)` below; `Saturated()` fires at `EstimatedFPRate ≥ 0.10`; cap respected at `MaxBloomCapacity` |
| V2-SP03-06 | `RebuildBloom` from an arbitrary `iter.Seq` at a different capacity | `go test -run 'TestBloom_Rebuild' ./internal/sketch/` ; `go test -bench BenchmarkRebuildBloom5000 ./internal/sketch/` | equivalent filter reconstructed; ≤ 15 ms for 5 000 keys |
| V2-SP03-07 | **CMS Appendix-A sizing** + overestimate-only guarantee | `go test -run 'TestCMS_AppendixASizing\|TestCMS_EstimateNeverUnderestimates\|TestCMS_ErrorBoundHolds' ./internal/sketch/ -v` | `Dims() == (2719, 5)`, body 54 380 bytes; never underestimates; ≥ 99 % of keys within ε·N = 100 |
| V2-SP03-08 | CMS merge / scale / heavy hitters (O4 groundwork) | `go test -run 'TestCMS_MergeFrom\|TestCMS_Scale\|TestCMS_HeavyHitters' ./internal/sketch/` | merge additive and exact; `Scale` decays; `HeavyHitters(mg,3)` returns `[{a,500},{b,300},{c,100}]`; `ErrShapeMismatch` on mismatch and on nil |
| V2-SP03-09 | HLL sizing, error bounds, exact-union merge | `go test -run TestHLL_ ./internal/sketch/ -v` | 2 048 registers, 2 102-byte frame, ~2.3 % standard error; relative error ≤ 0.07 at n ∈ {1e3,1e4,1e5,1e6}; merged registers byte-identical to the union |
| V2-SP03-10 | Misra-Gries: no false positives, frequent-item guarantee, mergeable | `go test -run TestMG_ ./internal/sketch/` | green, incl. `TestMG_NoFalsePositives`, `TestMG_FrequentItemGuarantee`, `TestMG_DeterministicUnderMapOrder`, `TestMG_MergeFrom` |
| V2-SP03-11 | **MinHash** — the O2 near-dup signal | `go test -run TestMinHash_ ./internal/sketch/ -v` | green, incl. `TestMinHash_OneNewFailure` (Jaccard ≥ 0.9 for "same suite, one new failure"), `TestMinHash_ShiftInvariance` (≥ 0.9), `TestMinHash_DisjointInputs` (≤ 0.05), `TestMinHash_StableAcrossRuns` (frozen `Mins` constants) |
| V2-SP03-12 | Persistence: atomic `Save`/`Load`, `LoadWithLog` is the loud path | `go test -run 'TestSave_\|TestLoad_\|TestLoadWithLog_\|TestQuarantine' ./internal/sketch/` | green; `Load` emits zero log records, `LoadWithLog` emits exactly one `Loud` on corruption; corrupt ⇒ `errors.Is(err, core.ErrNotFound) && errors.Is(err, ErrCorrupt)` |
| V2-SP03-13 | **`tried.bloom` generational replacement** (§7.4 append-only) | `go test -run 'TestSave_RefusesTriedBloom\|TestReplaceGenerational_\|TestAppendOnly_TriedBloomNeverTruncated' ./internal/sketch/` | `Save` on `tried.bloom` ⇒ `ErrGenerational`, no file created; exactly one `.bak` generation kept; rollback restores the prior generation on write failure; `AssertAppendOnly` passes |
| V2-SP03-14 | Property suite (12 properties) | `go test -run TestProp_ ./internal/sketch/` | green, incl. `TestProp_BloomRebuildEquivalence`, `TestProp_CMSMergeAdditive`, `TestProp_HLLMergeIsRegisterMax`, `TestProp_MarshalIdempotent`, `TestProp_UnmarshalNeverPanics`, `TestProp_MinHashJaccardAccuracy` |
| V2-SP03-15 | Five fuzz targets | for each of `FuzzBloomUnmarshalBinary FuzzCMSUnmarshalBinary FuzzHLLUnmarshalBinary FuzzMisraGriesUnmarshalBinary FuzzSignatureUnmarshalBinary`: `go test -run=XXX -fuzz <T> -fuzztime 60s ./internal/sketch/` | zero crashers; every failure path returns a package sentinel |
| V2-SP03-16 | **Frozen on-disk format** (W-2 contract for SP-04/SP-06) | `go test -run TestGolden ./internal/sketch/` (**without** `-update`) | the five `testdata/golden/contracts/sketch/*.v1.bin` reproduce byte-for-byte; `MANIFEST.json` hashes match; `TestGolden_V1StillDecodes` passes |
| V2-SP03-17 | Import purity | `go test -run TestImports_FoundationOnly ./internal/sketch/` | imports exactly `{core, paths, logging}` |
| V2-SP03-18 | **L0 sketch-update budget** (the §8.1 item-5 contribution to B-A) | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` and `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` | **≤ 5 µs/op and 0 allocs/op** |
| V2-SP03-19 | Remaining sketch micro-budgets | `go test -bench . -benchmem ./internal/sketch/` | `Bloom.Add/Test`, `CMS.Add/Estimate`, `HLL.Add` ≤ 1.0 µs/op 0 allocs; `HLL.Cardinality` ≤ 25 µs; `MisraGries.Add` ≤ 5 µs; `MinHash` 4 KiB ≤ 1.5 ms, 100 KiB ≤ 2.5 ms; Bloom marshal/unmarshal ≤ 60 µs; CMS ≤ 250 µs |
| V2-SP03-20 | Coverage floor | `go run ./tools/devtool cover` | `internal/sketch` ≥ **90 %** |

### 2.4 SP-04 — chunking, canonicalization, symbols (`internal/chunk`, `internal/canon`, `internal/symbols`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP04-01 | Three packages green under race | `go test -race ./internal/chunk ./internal/canon ./internal/symbols ./test/dedup` | exit 0 |
| V2-SP04-02 | FastCDC params: validation and deterministic clamping | `go test -run 'TestParams\|TestNewClampsInvalidParams' ./internal/chunk/` | green; the 8-case validate table and the 3-case normalize table match |
| V2-SP04-03 | **Chunk size bounds and contiguity** (§5.5 normative) | `go test -run 'TestSplit_SizeBounds\|TestSplit_Contiguity\|TestPropSizeBounds\|TestPropAllBytesCovered' ./internal/chunk/` | every non-last chunk in `[1024, 16384]`; `Σ Len == len(data)`; concatenation reproduces the input |
| V2-SP04-04 | **Cross-platform determinism** | `go test -run 'TestGearTableGolden\|TestSplit_GoldenBoundaries\|TestSplit_Determinism' ./internal/chunk/` (**no** `-update`) | goldens reproduce byte-for-byte on ubuntu, macos **and** windows in CI's `test` matrix. A per-platform golden is a checkpoint failure |
| V2-SP04-05 | **Boundary stability under insertion/deletion** (§5.5, §6.1) | `go test -run 'TestPropBoundaryStability_Insertion\|TestPropBoundaryStability_Deletion' -v ./internal/chunk/` | per trial: pre-`k` chunks byte-identical, a realignment index exists, ≤ 12 novel chunks; aggregate: ≤ 2 novel in ≥ 85 % of trials, ≤ 3 in ≥ 95 %. **Record the logged histogram** — it is a wave-1 baseline |
| V2-SP04-06 | Mean chunk size and Merkle root | `go test -run 'TestSplit_MeanChunkSize\|TestRootHash_\|TestRefs_RoundTrip\|TestChunkHashMatchesCoreHashBytes' ./internal/chunk/` | mean ∈ [3200, 5200]; `RootHash` domain-separated and order-sensitive; `Refs` produce `core.ChunkRef` |
| V2-SP04-07 | Streaming chunker equals in-memory chunker | `go test -run TestSplitStream_ ./internal/chunk/` | identical chunk sequence for readers of 1, 7, 4096 and full size across seven input sizes; callback and read errors propagate unwrapped |
| V2-SP04-08 | `FuzzSplit` | `go test -run=XXX -fuzz FuzzSplit -fuzztime 120s ./internal/chunk/` | zero crashers |
| V2-SP04-09 | FastCDC performance (§8.1 "well under 1 ms over 100 KB") | `go test -bench . -benchmem ./internal/chunk/` | `BenchmarkSplit_100KB` < 800 µs/op; `BenchmarkGearScan_1MiB` ≥ 400 MB/s; `BenchmarkSplit_1MiB` ≥ 120 MB/s and ≤ 2 allocs/op; `BenchmarkSplitStream_4MiB` ≤ 40 ms/op; `BenchmarkRootHash_1000Chunks` < 40 µs/op |
| V2-SP04-10 | Canon registry: order, duplicate rejection, strip gating | `go test -run 'TestRegistry_\|TestRun_\|TestKnownClassesCoverConfigDefaults\|TestParseClass_' ./internal/canon/` | `For("Bash","")` returns `[crlf ansi timestamps durations pids addresses tmpPaths bash testrunner git]` in that exact order, 100 repeats identical; `Names()` has the 14 registered names |
| V2-SP04-11 | Match composition: overlap resolution and the non-growing guard | `go test -run 'TestOverlapResolution_\|TestNonGrowingGuard_\|TestApplied_\|TestReduced_' ./internal/canon/` | longer-at-same-offset wins; earlier offset wins; rank breaks ties; growing matches silently dropped |
| V2-SP04-12 | **Normative canon properties** (§5.6) | `go test -run 'TestPropIdempotence_EveryCanonicalizer\|TestPropNonGrowing_EveryCanonicalizer\|TestPropRestoreIsExactInverse' ./internal/canon/` | `Run(Run(x)) == Run(x)`; `len(canonical) ≤ len(input)` always; `Restore(canonical, deltas) == input` whenever `KeepDeltas` |
| V2-SP04-13 | The seven generic canonicalizers | `go test -run 'TestCRLF_\|TestANSI_\|TestTimestamps_\|TestDurations_\|TestPIDs_\|TestAddresses_\|TestTmpPaths_' ./internal/canon/` | every table row matches; `pid=7` unchanged (short-match guard); `127.0.0.1:54123` → `127.0.0.1:<p>` |
| V2-SP04-14 | The seven per-tool canonicalizers | `go test -run 'TestBash_\|TestTestRunner_\|TestGrep_\|TestGlob_\|TestFileRead_\|TestWebFetch_\|TestGit_' ./internal/canon/` | every table row matches; `TestBash_ProgressGatedByStrip` and `TestBash_TrailingWhitespaceIsAlwaysOn` pin the class-gating contract |
| V2-SP04-15 | Every emitted `Match` carries a class (no silent discard) | `go test -run TestMatcherClassAssigned ./internal/canon/` | green over **every** corpus file; no zero `Class` |
| V2-SP04-16 | `Restore` error handling | `go test -run 'TestRestore_\|FuzzRestore' ./internal/canon/` ; `go test -run=XXX -fuzz FuzzRestore -fuzztime 120s ./internal/canon/` | `ErrDeltaRange` / `ErrDeltaOrder` as specified; zero fuzz crashers |
| V2-SP04-17 | Near-dup decision surface consumed by SP-06 | `go test -run 'TestDecide_\|TestSignature_OnlyWhenEnabled\|TestOptionsFrom_MapsAppendixC' ./internal/canon/` | all 6 `Decide` rows match; the golden `testdata/golden/contracts/canon/dedup-decisions.json` reproduces; `OptionsFrom` maps Appendix C (`permutations 128`, `nearDupThreshold 0.9`, the six strip classes) |
| V2-SP04-18 | Canon golden corpus | `go test -run TestGoldenCorpus_AllFiles ./internal/canon/` (**no** `-update`) | every one of the 24 `testdata/corpora/toolout/**` files reproduces its `testdata/golden/canon/**.canon.txt` byte-for-byte |
| V2-SP04-19 | **Dedup measurement harness — "with and without canonicalization"** (§10 Phase 1) | `go test -v ./test/dedup/` | `TestDedupRatio_WithVsWithout`: `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0`; `fileread` group `ratioWith > 1.0`. `TestDedupReport_Written` reproduces `testdata/canon-dedup-report.json` byte-for-byte |
| V2-SP04-20 | Canon performance | `go test -bench . ./internal/canon/` | `BenchmarkRun_Bash100KB` < 3 ms/op; `BenchmarkRun_GoTest` < 1 ms/op; `BenchmarkRestore_100KB` < 1 ms/op |
| V2-SP04-21 | Symbols: ten dialects, spans, caps | `go test -run TestExtract_ ./internal/symbols/` | green across Go, TS, Python, Rust, JVM, C-family, Ruby, Shell, PHP and the generic brace fallback; braces in strings/comments ignored; cap at 20 000 symbols; truncation at 4 MiB |
| V2-SP04-22 | Symbols: `Enclosing` = the §8.7 minimal sufficient span | `go test -run 'TestEnclosing_\|TestReferences_\|TestPropSpansWellFormed' ./internal/symbols/` | smallest containing span wins; out-of-range ⇒ `(Symbol{}, false)`; `References` respects word boundaries and zero-fills |
| V2-SP04-23 | `FuzzExtract` | `go test -run=XXX -fuzz FuzzExtract -fuzztime 120s ./internal/symbols/` | zero crashers; every span in bounds |
| V2-SP04-24 | Symbols performance | `go test -bench . ./internal/symbols/` | `BenchmarkExtract_100KB` < 2 ms/op; `BenchmarkEnclosing_100KB` < 2 ms/op; `BenchmarkReferences_100KB_50Names` < 1 ms/op |
| V2-SP04-25 | Conformance suites, zero skips | `go test -run 'TestCanonConformance\|TestSymbolsConformance' ./internal/canon ./internal/symbols` ; `grep -rn "t.Skip" internal/canon/canontest internal/symbols/symbolstest internal/chunk/chunktest` | suites green; grep returns nothing |
| V2-SP04-26 | Corpus hygiene | `grep -rniE 'AKIA\|ghp_\|sk-ant\|BEGIN [A-Z ]*PRIVATE KEY\|@gmail\.com' testdata/corpora/toolout/` | no output — no credential, key, token or real email in the committed corpus |
| V2-SP04-27 | Coverage floors | `go run ./tools/devtool cover` | `internal/chunk` ≥ **90 %**, `internal/canon` ≥ **90 %**, `internal/symbols` ≥ **75 %** |

### 2.5 SP-05 — daemon, IPC, hot path, contract monitor (`internal/ipc`, `internal/daemon`, `internal/contract`, `internal/cli`, `test/bench/hotpath`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP05-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/ipc ./internal/daemon ./internal/contract` | exit 0 |
| V2-SP05-02 | Address resolution: pipe, socket, XDG, `sun_path` guard | `go test -run 'TestProjectHash12\|TestResolve' ./internal/ipc/` | 12-hex project hash, case-folded on windows/darwin; XDG preferred; `<TempDir>/qp-<h8>.sock` fallback ≤ 100 bytes; `ErrAddrTooLong` when impossible; `\\.\pipe\qompack.<h12>` on windows |
| V2-SP05-03 | NDJSON framing, byte-exact | `go test -run 'TestEncodeRequestByteExact\|TestDecodeRequestRoundTrip\|TestLineReaderRejectsOversize' ./internal/ipc/` | encoded bytes equal `testdata/golden/contracts/ipc/observe_tool.ndjson`, one trailing `\n`, no HTML escaping; 1 MiB line cap enforced with resynchronization |
| V2-SP05-04 | 32-byte hot-path state record | `go test -run TestState ./internal/ipc/` ; `go test -bench BenchmarkReadState ./internal/ipc/` | round-trip exact, file exactly 32 bytes; missing/short/bad-CRC all fall back to config defaults; atomic under 500 concurrent writers; **< 100 µs/op** |
| V2-SP05-05 | **`Client.Send` never returns a propagating error** | `go test -run 'TestSend' ./internal/ipc/` | all rows green, incl. `TestSendNeverReturnsError` (200 rapid cases × 4 hostile listeners, `err == nil` every time, wall time < 3 × ack deadline), `TestSendDaemonDownSpoolsAndReturnsNilError`, `TestSendNAKSwitchesToSpool`, `TestSendHotSpoolSkipsConnect`, `TestSendModeOffDoesNothing`, `TestSendOversizeExternalizes` |
| V2-SP05-06 | Spool is append-only and drop-safe | `go test -run TestSpool ./internal/ipc/` | pre-existing content intact after `Append`; `O_TRUNC` rejected by `paths.AppendOnly`; write failure ⇒ nil return, `l0.dropped` incremented per event, exactly one `Loud` per session |
| V2-SP05-07 | Server: routing, ACK/NAK, panic containment, concurrency | `go test -run TestServer ./internal/ipc/` | 3 200 ACKs across 64 goroutines under `-race`; unknown op ⇒ one `NAK` and the connection survives; handler panic ⇒ `NAK` + `ipc.route.panic == 1` |
| V2-SP05-08 | Transport permissions | `go test -run 'TestUnixSocketPermissions\|TestStaleUnixSocketReclaimed' ./internal/ipc/` ; on windows `QOMPACK_TEST_ACL=1 go test -run TestWindowsPipeACLRejectsOtherUser ./internal/ipc/` | socket `0600`, directory `0700`; stale socket reclaimed; SDDL contains the current SID and begins `D:P` |
| V2-SP05-09 | `ipctest` conformance suite, zero skips | `go test -run 'Suite' ./internal/ipc/...` ; `grep -rn "t.Skip" internal/ipc` | green; no output |
| V2-SP05-10 | Daemon singleton lock and lazy detached spawn | `go test -run 'TestAcquireLock\|TestStaleLockReclaimed\|TestLiveLockNotReclaimed\|TestHeartbeat\|TestRunReturnsNilWhenLockHeld' ./internal/daemon/` | second acquisition ⇒ `ErrLockHeld`; dead pid reclaimed; a live listener defeats an old heartbeat |
| V2-SP05-11 | Session registry | `go test -run TestRegistry ./internal/daemon/` | new session resets `HotSpool`→`HotSync`; ended sessions evicted first over `maxSessions`; all-live over cap keeps all and emits one `Loud` |
| V2-SP05-12 | **WAL ingest — the durability boundary** | `go test -run TestIngest ./internal/daemon/` ; `go test -bench BenchmarkIngestAccept ./internal/daemon/` | WAL holds exact bytes; ring full spills to spool without blocking (`l0.ring_full` counted); **ACK precedes processing**; `BenchmarkIngestAccept` meets **B-B p99 < 2 ms** |
| V2-SP05-13 | Drain: idempotent, resumable, blob-aware, corruption-tolerant | `go test -run TestDrain ./internal/daemon/` | 10 lines ⇒ 10 dispatches (not 20), file deleted; cancel/resume totals exactly 1 000; blob resolved and deleted; corrupt line counted, file still consumed |
| V2-SP05-14 | Idle controller (O3 seam) | `go test -run 'TestIdle\|TestIsIdle' ./internal/daemon/` | priority order respected; budget respected; task panic isolated; `IsIdle` flips exactly at `idle.detectAfterSeconds` |
| V2-SP05-15 | **§8.1 fallback: observable `sync` → `spool` transition** | `go test -run 'TestBreachDetector\|TestHotModeTransition\|TestSpoolOnBreachFalse' ./internal/daemon/` | transition after exactly 3 consecutive 512-sample breach windows; a clean window resets; reverts after 3 clean windows; the transition is visible in `state.bin`, in the NAK frame, in the WARN log and in `status` |
| V2-SP05-16 | **Wave-appropriate nil-service tolerance** | `go test -run 'TestServicesAllNil\|TestHandleOverridesDefaultRoute\|TestBindRunsInOrder' ./internal/daemon/` | all 13 ops answered with a fully nil `Services`, zero panics, WAL holds every hot-path event; `mcp` alone returns `OK:false, Err:"mcp not built"` |
| V2-SP05-17 | Degradation semantics (§12.1) | `go test -run 'TestDegradedPassive\|TestModeOffSkipsIngest' ./internal/daemon/` | in `ModeDegradedPassive`: no `additionalContext`, no `customInstructions`, no acting idle tasks — but 20/20 `observe.tool` still recorded; in `ModeOff` nothing is ingested |
| V2-SP05-18 | Config hot reload defers chunk changes | `go test -run TestConfigReloadDefersChunkChange ./internal/daemon/` | in-memory chunk block unchanged; `state/config-pending.json` written; one `Loud` |
| V2-SP05-19 | `SketchSet` never rewrites `tried.bloom` | `go test -run TestSketchSetNeverWritesTriedBloom ./internal/daemon/` | bytes and mtime unchanged |
| V2-SP05-20 | Idle exit | `go test -run TestIdleExitWithZeroSessions ./internal/daemon/` | `Run` returns; `run/daemon.lock` and `run/state.bin` removed |
| V2-SP05-21 | **Contract monitor reports `ModeFull` on a wave-1 build** | `go test -run 'TestFreshBuildReportsModeFull\|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/ -v` | `ModeFull`; exactly **four** assertions at `OK:true, SevInfo, Observed:"not-yet-implemented"` (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`); exactly five always-declared producers |
| V2-SP05-22 | Contract monitor: degrade loud, restore on two clean runs | `go test -run 'TestCriticalFailureDegrades\|TestTwoCleanRunsRestore\|TestOneCleanRunDoesNotRestore\|TestDegradeIsIdempotent\|TestHistoryPersistsAcrossMonitors' ./internal/contract/` | degrade writes exactly one `Loud` + `state/contract.json` with `DegradedSince`; restore needs two clean runs and is equally loud |
| V2-SP05-23 | The nine assertions, individually | `go test -run 'TestSessionStartFires\|TestSourceCompact\|TestSentinel\|TestAdditionalContextGated\|TestPreCompactTimeoutUnknown\|TestCustomInstructionsProbePhrase\|TestHookPayloadShape\|TestPluginRootUnset\|TestMarkerIsWrittenByFlushAndCheckpointOnly' ./internal/contract/` | each behaves as its row in §12.1 specifies; `TestPanickingAssertionDoesNotDegrade` proves an assertion crash never degrades the session |
| V2-SP05-24 | Budget table matches the architecture | `go test -run TestBudgets ./internal/obs/` | six budgets B-A…B-F in order with the §2.4 `Clock` strings; `Gated` true for B-A, B-B, B-E, B-F; B-D never gated; B-A limit follows `runtime.hotPath.budgetMs` |
| V2-SP05-25 | **Hooks exit 0 under every injected fault** | `go test -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit' ./test/e2e/ -v` | **66/66** combinations exit 0 with valid JSON on stdout; fault sites inert when `QOMPACK_FAULT` is unset; only `self-test` may exit non-zero |
| V2-SP05-26 | Daemon e2e against the real binary | `go test -run 'TestE2EHookRoundTrip\|TestE2ELazySpawn\|TestE2EIdleExit\|TestE2ESpoolSubmodeEndToEnd\|TestE2ESelfTest' ./test/e2e/` | 50 hooks ⇒ 50 WAL lines; lazy spawn listening within 1.5 s and the spool drained; idle exit removes lock and state; `self-test --json` exits 0 with `mode == "full"` |
| V2-SP05-27 | **B-A / B-B / B-D / B-E hot-path harness** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-v2-baseline.json` | `B-A.pass == true` (p99 < 15 ms), `B-B.pass == true` (p99 < 2 ms), `B-E.pass == true` (p99 < 2 s); `b_a_method` and `spawn_floor_ms` present; B-D reported, never gated. **See §5 — this must be re-run warm with the real store/DAG/sketches** |
| V2-SP05-28 | Security posture | `go run ./tools/devtool lint` + the CI `security` job on the branch | zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` and only `unix`; `os/exec` only in `internal/daemon`, `internal/cli`, `tools/`; `govulncheck` clean |
| V2-SP05-29 | Coverage floors | `go run ./tools/devtool cover` | `internal/ipc`, `internal/daemon`, `internal/contract` and SP-05's `internal/cli` files each ≥ **75 %** |

### 2.6 SP-06 — content-addressed store, redaction, exact token accounting (`internal/store`, `internal/redact`, `internal/tokens`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP06-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/store ./internal/redact ./internal/tokens` | exit 0 |
| V2-SP06-02 | **Redaction: all ten built-in rule families** | `go test -run TestRedact_ ./internal/redact/ -v` | PEM, AWS (`AKIA`/`ASIA`), GitHub (`ghp_`/`gho_`/`github_pat_`), Anthropic `sk-ant-` before generic `sk-`, JWT, bearer value-only, credentialed URI, assignment value-only, dotenv gated by key name — each fires exactly once on its fixture and leaves surrounding text byte-identical |
| V2-SP06-03 | Redaction: idempotent, bounded growth, deterministic | `go test -run 'TestRedact_Idempotent\|TestRedact_BoundedGrowth\|TestRedact_Deterministic\|TestRedact_NeverWholeInputExceptPEM' ./internal/redact/` ; `go test -run=XXX -fuzz FuzzRedactIdempotent -fuzztime 60s ./internal/redact/` | `Redact(Redact(x)) == Redact(x)`; `len(out) ≤ 2*len(in)+64`; zero fuzz crashers |
| V2-SP06-04 | Redaction: user patterns admitted or rejected safely | `go test -run 'TestRedact_UserPattern\|TestRedact_Rejects\|TestRedact_InvalidUserPattern\|TestRedact_GrowthBoundHoldsUnderHostilePattern\|TestRedact_Disabled' ./internal/redact/` | zero-width and single-char patterns rejected at `New` with one `Loud` and `redact.pattern_rejected == 1`; built-ins stay active |
| V2-SP06-05 | Token classification (G10.2) | `go test -run 'TestClassify_All\|TestClassify_SP01TableStillPasses' ./internal/tokens/` | seven classes correct; SP-01's 14-case table unchanged by the inserted image-magic rule |
| V2-SP06-06 | **Media sizing replaces the flat 2 000** (§2.2 / G10.2) | `go test -run 'TestEstimateImage_\|TestEstimatePDF_' ./internal/tokens/ -v` | 1024×768 PNG ⇒ 1049 tokens; 4000×3000 PNG downscaled then clamped to **1600**; JPEG/GIF/WebP dimensions parsed; text PDF within ±15 % of `units(text)*0.92` and **not** 2 000; scanned PDF ⇒ `PDFTokensPerPage` from config |
| V2-SP06-07 | **Exact chunk-level accounting** | `go test -run 'TestEstimateRoot_\|TestChunkCache_\|TestUnits_Deterministic' ./internal/tokens/` ; `go test -bench BenchmarkEstimateRoot_64Cached ./internal/tokens/` | cache hit ⇒ `tokens.chunk_miss == 0`; miss path reproduces SP-01's baseline formula exactly; memo key is `core.Hash` alone; cache persists across `Close`/reopen and survives a truncated `chunktokens.bin` with one `Loud`; **≤ 5 µs/op** |
| V2-SP06-08 | Per-project calibration, clamped and persisted | `go test -run TestCalibrate_ ./internal/tokens/` | factor clamped to `[CalibrationMin, CalibrationMax]` from config (never literals `0.6`/`1.6`); persists; ignores zero; reads SP-01's flat calibration file |
| V2-SP06-09 | **Object layer: 2-level fanout, zstd, dedup** | `go test -run 'TestPutBytes_FanoutLayout\|TestPutBytes_CompressionNone\|TestPutBytes_GlobalDedup\|TestPutBytes_IdenticalRootIsFree' ./internal/store/` | `objects/<h[0:2]>/<h[2:4]>/<h>.zst`; four file versions ⇒ 4 roots with strictly decreasing `Novel` and total objects < 1.6× v1 alone; identical payload ⇒ `Novel==0`, no new `roots.jsonl` line |
| V2-SP06-10 | **Ingest order: redact → canon → chunk** (§8.1 item 1, §13 invariant 7) | `go test -run 'TestPutBytes_RedactionBeforeChunking\|TestPutBytes_RedactionRunsWhenDepsRedactIsNil\|TestPutBytes_CanonicalizeBeforeChunk\|TestPutBytes_CanonFailureFallsBack' ./internal/store/` | no object anywhere under `objects/` contains the literal secret, including when `Deps.Redact` is nil; canon failure degrades to storing redacted bytes with a `Warn`, never an error |
| V2-SP06-11 | Raw-byte accounting is per-put, not per-root | `go test -run 'TestPutBytes_DedupHitReportsThisPutsRawBytes\|TestPutBytes_KeepRaw\|TestPut_ReaderTruncation\|TestPutBytes_EphemeralFlagPersists' ./internal/store/` | `Stats.RawBytes` is the sum of the inputs (the regression that would silently understate `DedupRatio`); delta roots restore byte-exactly; `Truncated` at `MaxPutBytes` without error; `Ephemeral` survives reopen |
| V2-SP06-12 | Near-duplicate detection | `go test -run 'TestPutBytes_NearDup\|TestPutBytes_NoNearDupForDistinctPaths' ./internal/store/` | v1→v2 ⇒ `NearDup != nil`, `Jaccard ≥ 0.9`, `PriorRoot` = v1 |
| V2-SP06-13 | Read paths: `GetChunk`, `Open`, **`OpenSpan` (§8.7 minimal span)**, `Has` | `go test -run 'TestGetChunk_\|TestOpen_StreamsFullRoot\|TestOpenSpan_Boundaries\|TestHas_NoIO' ./internal/store/` | round-trip exact; all six span boundary cases exact; corruption quarantined to `tmp/quarantine/` with one `Loud` and `core.ErrNotFound` |
| V2-SP06-14 | Index durability and tolerance | `go test -run 'TestOpenStore_\|TestClosedStoreErrors\|TestConcurrentPut' ./internal/store/` | 50 roots survive `Close`/reopen; truncated final line counted as `store.index.badline == 1` without data loss; closed store returns `core.ErrDegraded` everywhere; 8×50 concurrent puts race-clean |
| V2-SP06-15 | `tool_use` index + supersession | `go test -run 'TestRecordToolUse_\|TestToolUsesByPath_\|TestMarkSuperseded_\|TestArgsDigest_' ./internal/store/` | idempotent replay ⇒ one line; conflicting replay ⇒ `core.ErrAppendOnly`; supersession appends a mutation record and never rewrites; args digest key-order invariant; preview ≤ 120 bytes on a rune boundary |
| V2-SP06-16 | **File version history + `ChangedSince`** (§8.2, §8.3) | `go test -run 'TestAppendFileVersion_\|TestFileAt\|TestChangedSince_\|TestFilesJSON_MaterializedByFlush' ./internal/store/` | history ascending; `FileAt` picks the version at-or-before; `ChangedSince` is exactly hash inequality, key-normalized, input-ordered, unknown paths counted not errored |
| V2-SP06-17 | **Segment log and the DPI guard** (§4.6, §8.2) | `go test -run TestSegment_ ./internal/store/ -v` | monotonic ids; `MarkEncoded` idempotent for the same seq; **`TestSegment_MarkEncodedRefusesDifferentSeq` ⇒ `core.ErrAlreadyEncoded`**; batch all-or-nothing; open segments refused; `Frontier` is the contiguous encoded prefix, not the maximum |
| V2-SP06-18 | Search (backs `recall`) | `go test -run TestSearch_ ./internal/store/` ; `go test -bench BenchmarkSearch_1000Roots ./internal/store/` | exact-path beats suffix; text ranked by occurrence; symbol span widened to the enclosing symbol; default `K==5`; deterministic over 20 runs; truncation counted not errored; **≤ 25 ms/op** |
| V2-SP06-19 | **`Stats.DedupRatio` and the Phase-1 exit criterion** | `go test -run 'TestStats_DedupRatio\|TestPhase1ExitCriterion_ReadHeavy\|TestStats_SublinearGrowth' ./internal/store/ -v` | **`DedupRatio ≥ 4.0`** on the read-heavy corpus (40 reads across 10 files with edits between); `Bytes` after 200 puts < 25 × `Bytes` after 8 puts |
| V2-SP06-20 | **GC: deadline-bounded, resumable, mark-and-sweep** | `go test -run TestGC_ ./internal/store/ -v` ; `go test -bench BenchmarkGC_50kObjects ./internal/store/` | roots harvested from checkpoints/pins/eliminations **without importing those packages**, including bare 64-hex; retention is `max(30 days, 10 sessions)`; zero policy deletes nothing; dry run deletes nothing; deadline truncates with a resumable cursor and the union equals one unbounded run; tombstones are appends; **≤ 2 s/op, deadline honoured ±50 ms** |
| V2-SP06-21 | Flush + session index + append-only guard | `go test -run 'TestFlush_\|TestAppendOnlyGuard_StoreFiles' ./internal/store/` | `sessions.jsonl` last-wins per session; second flush appends nothing; every `*.jsonl` this package writes refuses truncation |
| V2-SP06-22 | Store property suite | `go test -run Prop ./internal/store/` | `PropPutGetRoundtrip`, `PropOpenSpanMatchesSlice`, `PropDedupMonotone`, `PropRedactIdempotent`, `PropChangedSinceIsExactlyHashInequality`, `PropMarkEncodedNeverDowngrades` all green |
| V2-SP06-23 | **Index formats are frozen** | `go test -run TestGolden_IndexFormats ./internal/store/` (**no** `-update`) | `roots.jsonl`, `tool_use.jsonl`, `segments.jsonl`, `files.json`, `sessions.jsonl` byte-identical to `testdata/golden/store/*` |
| V2-SP06-24 | Store e2e | `go test -run 'TestE2E_StoreSurvivesProcessRestart\|TestE2E_SecretNeverLandsInObjects' ./test/e2e/` | 200 payloads survive restart with matching `Stats`; **no built-in secret literal appears in any decompressed object** |
| V2-SP06-25 | Store/redact/tokens performance | `go test -bench . -benchmem ./internal/store ./internal/redact ./internal/tokens` | `PutBytes_100KB_Cold` ≤ 3 ms; `_Warm` ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan_4KB_of_4MB` ≤ 150 µs; `OpenStore_50kRoots` ≤ 400 ms; `MarkEncoded_100` ≤ 1 ms; `Redact` 100 KB ≤ 2 ms |
| V2-SP06-26 | Conformance suites, zero skips | `go test -run 'Suite' ./internal/store/... ./internal/redact/... ./internal/tokens/...` ; `grep -rn "t.Skip" internal/store/storetest internal/redact/redacttest internal/tokens/tokenstest` | `RunStoreSuite` and `RunSegmentLogSuite` green; grep returns nothing |
| V2-SP06-27 | Coverage floors | `go run ./tools/devtool cover` | `internal/store` ≥ **90 %**; `internal/redact` ≥ **90 %** and `internal/tokens` ≥ **90 %** (SP-06's self-imposed override over the §6.4 75 % floor) |

### 2.7 SP-07 — dependence DAG and slicing (`internal/dag`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP07-01 | Package green under race | `go test -race ./internal/dag/...` | exit 0 |
| V2-SP07-02 | Nine node kinds, eight edge kinds, multiplier table | `go test -run 'TestNodeKind\|TestEdgeKind' ./internal/dag/` | text round-trips for all kinds; multipliers exactly `1.00, 1.00, 1.00, 0.95, 0.88, 0.60, 0.50, 0.30`; `EdgeInvalid` ⇒ 0 |
| V2-SP07-03 | Stable `NodeID` scheme | `go test -run 'TestNodeID\|TestParseNodeID' ./internal/dag/` | `testdata/golden/contracts/dag/nodeid.json` reproduces byte-for-byte; 500-byte key ⇒ exactly 376 bytes with hash suffix; control chars and invalid UTF-8 sanitized and round-trip through `Flush`/`Open` |
| V2-SP07-04 | Graph mutation: validation, upsert, dedup, tombstones | `go test -run 'TestAddNode\|TestAddEdge\|TestTombstone\|TestOutInCopies\|TestAnchorNodePosIsEarliest\|TestClosedGraphRejects' ./internal/dag/` | invalid nodes/edges wrap `ErrInvalidNode`/`ErrInvalidEdge`; upsert merges; ephemeral is sticky; edge dedup keeps max weight and min turn; dangling endpoints tolerated and counted; anchor nodes keep the earliest `Pos` |
| V2-SP07-05 | Concurrency | `go test -race -run TestConcurrentMutationAndRead ./internal/dag/` | 8 writers × 8 readers × 2 000 iterations: no race, no deadlock, no panic; final `Stats().Nodes` exact |
| V2-SP07-06 | **`CrossingEdges` = `segment_coupling(p)`** (§8.4) | `go test -run 'TestCrossingEdges\|PropCrossingEdgesMatchesBruteForce' ./internal/dag/` ; `go test -bench BenchmarkCrossingEdges ./internal/dag/` and `go test -run TestCrossingLatencyBudget ./internal/dag/` | matches brute force on every rapid case and all twelve golden positions; the `lo < pos <= hi` rule asserted at both ends; dangling excluded; equal positions cross nothing; **< 5 µs median on 15 000 edges** |
| V2-SP07-07 | `NodesAfter` total order | `go test -run 'TestNodesAfterOrdering\|PropNodesAfterMatchesFilter' ./internal/dag/` | live-only, freshly allocated, ordered by `(Pos, Turn, ID)` |
| V2-SP07-08 | **Scored backward/forward slicing** (§6.4, §8.3) | `go test -run 'TestBackwardSlice\|TestForwardSlice\|TestSlice' ./internal/dag/` | exact decayed scores (`1, 0.85, 0.7225`); max-path not sum; `MaxNodes` truncation exact and the exact-fit case not marked truncated; `MaxDepth` respected; the `1e-4` floor is not a truncation; unknown criteria ⇒ empty with nil error; deterministic tie-break |
| V2-SP07-09 | Thin slicing is the default and is a subset of full | `go test -run 'TestThinDropsControlOnly\|TestDefaultSliceOptionsFromConfig\|PropThinSliceIsSubsetOfFull\|PropScoresBoundedAndMonotone' ./internal/dag/` | `DefaultSliceOptions(config.Defaults()).Thin == true`; `Decay == 0.85`; `MaxNodes == 5000`; `Deadline == 5ms`; thin ⊆ full with `thin[id] ≤ full[id]` |
| V2-SP07-10 | Slice goldens | `go test -run 'TestSliceGolden\|TestCrossingEdgesGolden' ./internal/dag/` (**no** `-update`) | `slice-backward.json` and `crossing.json` reproduce to 5 decimals / exactly |
| V2-SP07-11 | **Thin-vs-full measurement** (the §6.4 tradeoff, quantified) | `go test -run TestThinVsFullComparison ./internal/dag/ -v` | across 8 seeds: mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`, mean `precision ≥ precision_full`, `ns_thin ≤ ns_full`; `thin-vs-full.json` matches within 2 % |
| V2-SP07-12 | Persistence: append-only `deps.jsonl`, torn tails, corrupt lines | `go test -run 'TestFlush\|TestOpen\|TestAutoFlushAt2000\|PropLogRoundTrip' ./internal/dag/` | `Flush` bytes equal `graph-basic.jsonl` (minus the `g` header); torn tail ⇒ `TruncatedTail == true`, `LoadErrors == 0`; corrupt line ⇒ `LoadErrors == 1` and exactly one `Loud`; auto-flush at 2 000 records; flush failure retains pending |
| V2-SP07-13 | Idle-only `Compact` | `go test -run TestCompact ./internal/dag/` | drops tombstoned nodes, writes a `g` record with `gen:1`, no-op below the 25 % waste threshold, flushes pending first, preserves every slice answer, restores cleanly on cancel |
| V2-SP07-14 | **§8.1 item 4 edge builders, acyclic by construction** | `go test -run 'TestBuild\|TestBuilderOutputIsAcyclic' ./internal/dag/ -v` | the exact five-node/five-edge set for a read; consumes edge starts at the **previous** result; suppressed for parallel siblings (`PrevTurn == Turn`); future `PrevTurn` rejected; write direction reverses file/symbol edges; supersession edge scored `0.255`; **DFS over 200 built tool uses finds no cycle** |
| V2-SP07-15 | **No selection authority** (closing note 3, mechanized) | `go test -run TestNoBooleanKeepAPI ./internal/dag/ -v` | no exported function returns `map[NodeID]bool` / `[]bool`; no exported identifier matches `keepset\|dropset\|^keep\|^drop\|evict`; `doc.go` still contains the literal `NO SELECTION AUTHORITY` |
| V2-SP07-16 | `dagtest` conformance suite, zero skips | `go test -run 'Suite\|Conformance' ./internal/dag/...` ; `grep -rn "t.Skip" internal/dag` | `RunGraphSuite` green against the real `dag.Open`; grep returns nothing |
| V2-SP07-17 | Synthetic generator determinism | `go test ./internal/dag/dagtest/` | seed 7 produces an identical node/edge dump on two runs; the generated graph is acyclic |
| V2-SP07-18 | **Slice latency** (§6.4 "sub-millisecond") | `go test -bench 'Slice' ./internal/dag/` and `go test -run TestSliceLatencyBudget ./internal/dag/` | `BenchmarkBackwardSlice5000` and `BenchmarkForwardSlice5000` **< 1 ms/op** median of 20 on a 5 000-node / ~15 000-edge graph; `BuildToolUse` < 3 µs |
| V2-SP07-19 | Import purity | `go run ./tools/devtool lint` (`importgraph`) | `internal/dag` imports only `core`, `paths`, `config`, `logging` — in particular **not** `store`, **not** `symbols`, **not** `eval` |
| V2-SP07-20 | Coverage floor | `go run ./tools/devtool cover` | `internal/dag` ≥ **85 %** |

### 2.8 Whole-tree gates that must be green before §4 begins

| ID | Gate | Command | Expected |
|---|---|---|---|
| V2-ALL-01 | Full suite, race | `go test -race ./...` | exit 0 (ubuntu, macos) |
| V2-ALL-02 | Full suite, Windows repeat | `go test -count=2 ./...` | exit 0 (windows dev machine and CI) |
| V2-ALL-03 | Local CI | `go run ./tools/devtool ci-local` | exit 0 end to end |
| V2-ALL-04 | CI on `verify/v2` | push the branch | `verify`, `test` (×3 OS), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs` all green. `bench-gate` and `replay-gate` are **required checks from the end of wave 1 onward** (§8) — confirm the `continue-on-error` flag SP-01 left on them has been removed by SP-02/SP-05 |
| V2-ALL-05 | `Qompack.md` untouched by the whole wave | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |

---

## Exit-criteria re-verification

Each completed subplan's exit criteria, quoted, with the concrete measurement procedure to run **now, on the integrated `develop`**. A criterion that a subplan proved on its own branch against stubs is not proven here until it is re-measured against the real siblings.

### 3.1 SP-01 (wave 0)

> SP-01 precedes Phase 0, so no phase exit criterion applies to it directly. The criteria it must **make measurable for later waves** … are:
> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
> And the guardrails SP-01 must express as configuration and CI, verbatim from §11.3:
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Measurement.** SP-01's obligation was to make these measurable; wave 1 is where they become *measured*. Verify the machinery still exists and is now wired to real producers:

1. `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/` — B-A..B-F exist with config-driven limits (V2-SP01-13).
2. `go run ./tools/devtool cover` prints `exempt (stub, owned by SP-NN)` for **exactly** the still-stubbed packages and no longer exempts `store`, `sketch`, `chunk`, `canon`, `dag`, `eval` — those six now have owners on `develop`, so their §6.4 floors bind. `cover` must fail if `plans/OWNERS.tsv` claims an owner for a package whose probe still returns `ErrNotImplemented`.
3. `go run ./tools/devtool replay -- --phase 0 --ci` enforces the 2 % rule and the Phase-0 exit criterion (§3.2 below).
4. `go run ./tools/devtool bench-hotpath …` enforces B-A and B-E (§3.5, §5).
5. `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` — Appendix C still byte-exact after six branches touched config consumers.

### 3.2 SP-02 — Phase 0

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

**Measurement procedure.**

```bash
# a) the single number, and that it is one number over >= 20 sessions
go run ./test/replay --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 0 --ci --json v2-replay.json
jq '.sessions, .corpusTier, .policies.stock.fraction_of_opt' v2-replay.json
# Expect: 24, "synthetic", a single float equal to the committed baseline.

# b) reproducibility across processes
go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --to a.json
go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --to b.json
diff a.json b.json      # Expect: identical, and both equal to testdata/baseline/phase0.json

# c) the ceiling and floor are real
go test -run 'TestOraclePolicy_ScoresExactlyOne\|TestSynthesize_EveryCompactionHasDemands' ./internal/eval/
# Expect: oracle == 1.0 on all 24; null == 0.0 on all 24; stock strictly between.

# d) the 2% rule is live in both directions
go test -run 'TestGate_TwoPercentBoundaryExclusive\|TestGate_LowerBetterMetricDirection\|TestGate_SignOff' ./test/replay/

# e) sublinear growth — now measurable from a REAL store (see §4.4)
go test -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/
```

**Honesty requirement.** §10 Phase 0 says "real sessions". The committed number is synthetic-corpus. Confirm `docs/adr/0002-replay-methodology.md` still states this explicitly, names the recorded-corpus command, and names who runs it and when. If that paragraph has been softened or removed, that is a checkpoint failure.

### 3.3 SP-03 — sketch library

SP-03 owns no phase exit criterion. The two it must not obstruct, quoted:

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms.

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents … The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

> - Hook p99 latency < 15ms (L0), < 2s (L4)

**Measurement procedure.**

1. **Contribution to the 15 ms budget**: `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` ⇒ ≤ 5 µs/op, 0 allocs; `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` green. Record the ratio to 15 ms (target ≈ 0.03 %).
2. **Contribution to the 4:1 ratio**: `go test -run TestMinHash_OneNewFailure -v ./internal/sketch/` ⇒ Jaccard ≥ 0.9 on the "same test suite, one new failure" fixture; then §4.2's `TestIntegration_StoreNearDupUsesRealMinHash` proves the store actually consumes it.
3. **Contribution to zero stale-block incidents** (Phase 2, not achieved here — only *not obstructed*): `go test -run 'TestBloom_Rebuild\|TestSave_RefusesTriedBloom\|TestReplaceGenerational_' ./internal/sketch/` ⇒ rebuild from an arbitrary iterator at a different capacity works and `tried.bloom` can only be replaced generationally. Do **not** assert anything about `negknow` — it is SP-09.
4. **Format freeze**: `go test -run TestGolden ./internal/sketch/` without `-update`. Regenerating a fixture here to make it pass is the exact failure Rule W-2 forbids.

### 3.4 SP-04 — chunking, canonicalization, symbols

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
>
> SP-04 owns **"measure with and without canonicalization"** … The `≥ 4:1` ratio and the `< 15ms` hook p99 are **SP-08**'s and **SP-05**'s to achieve and assert; SP-04 must not claim them.

> FastCDC over 100KB is well under 1ms

> boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

> `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**Measurement procedure.**

```bash
# with/without canonicalization, per group and overall
go test -v ./test/dedup/
jq '.groups[] | {group, ratio_without, ratio_with, gain}, .overall' testdata/canon-dedup-report.json
# Expect: testrunner gain >= 1.25; overall gain >= 1.0; fileread ratio_with > 1.0.
# Expect: TestDedupReport_Written reproduces the committed file byte-for-byte.

# FastCDC over 100KB
go test -bench BenchmarkSplit_100KB -benchmem ./internal/chunk/     # < 800 us/op

# boundary stability, with the distribution logged
go test -v -run 'TestPropBoundaryStability_(Insertion|Deletion)' ./internal/chunk/
# Record the novelty histogram. Thresholds: <=12 per trial (hard), <=2 in >=85%, <=3 in >=95%.

# cross-platform determinism (must be checked on all three CI OSes, no -update anywhere)
go test -run 'TestGearTableGolden|TestSplit_GoldenBoundaries' ./internal/chunk/

# the three normative canon properties
go test -run 'TestPropIdempotence_EveryCanonicalizer|TestPropNonGrowing_EveryCanonicalizer|TestPropRestoreIsExactInverse' ./internal/canon/
```

**Scope discipline.** SP-04 must not be marked as having achieved `≥ 4:1` or `< 15 ms`. Those are re-verified under §3.5 and §3.6 respectively; §4.1 is where SP-04's canonicalizers and SP-06's store are composed to produce the ratio for the first time.

### 3.5 SP-05 — daemon, IPC, hot path

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. … If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 …; hook p99 < 15ms. *(SP-05 owns and satisfies the second clause)*

> - Hook p99 latency < 15ms (L0), < 2s (L4)

> Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently.

> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

**Measurement procedure.**

```bash
# B-A / B-B / B-D / B-E, real process spawns, warm daemon.
# CRITICAL: this must now run with the REAL store, DAG and sketches resident (see §4.5 / §5).
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v2-bench-hotpath.json
jq '.[] | {budget_id, n, p50, p95, p99, max, pass}' v2-bench-hotpath.json
# Expect: B-A p99 < 15ms pass:true ; B-B p99 < 2ms pass:true ; B-E p99 < 2s pass:true ;
#         B-D reported with pass omitted/false-gated; b_a_method and spawn_floor_ms present.

# the degradation doctrine, as an observable state machine
go test -run 'TestBreachDetector|TestHotModeTransitionWritesStateAndNAKs|TestSpoolOnBreachFalseDoesNotTransition' ./internal/daemon/
go test -run 'TestDegradedPassiveSuppressesActingPaths|TestDegradedPassiveStillRecords|TestModeOffSkipsIngest' ./internal/daemon/

# never fail silently
go test -run 'TestCriticalFailureDegrades|TestDegradeIsIdempotent|TestTwoCleanRunsRestore' ./internal/contract/

# a wave-1 build is FULL, not degraded
go test -v -run 'TestFreshBuildReportsModeFull|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/

# hooks never take the session down
go test -v -run TestHooksExitZeroUnderFaults ./test/e2e/     # 66/66
```

**B-E annotation.** Record B-E as *measured against the current `checkpoint` path, whose `Services.PreCompact` is nil at wave 1*. It is a real number for the code that exists; it is not yet a measurement of checkpoint finalization. Write that sentence into the completion report rather than implying otherwise.

### 3.6 SP-06 — content-addressed store

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms.

> - Store growth sublinear in session length after dedup

> **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint.

**Measurement procedure.**

```bash
# the ratio half, in-package (real chunker + real canon now that wave 1 is integrated)
go test -v -run 'TestPhase1ExitCriterion_ReadHeavy|TestStats_DedupRatio' ./internal/store/
# Expect: Stats.DedupRatio >= 4.0 on the read-heavy corpus.

# the ratio half, composed end to end across SP-03/SP-04/SP-06 (authored in §4.1)
go test -v -run TestIntegration_Phase1DedupRatioWithRealPipeline ./test/integration/

# sublinear growth, in-package and through SP-02's checker (§4.4)
go test -run TestStats_SublinearGrowth ./internal/store/
go test -v -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/

# the DPI guard
go test -v -run 'TestSegment_MarkEncodedRefusesDifferentSeq|TestSegment_MarkEncodedBatchIsAllOrNothing|PropMarkEncodedNeverDowngrades' ./internal/store/

# the hook-p99 half is NOT SP-06's; SP-06's bounded contribution is B-C
go test -bench 'BenchmarkPutBytes_100KB' -benchmem ./internal/store/    # cold <= 3ms, warm <= 400us
```

**W-2 re-verification (mandatory, this is the checkpoint's core job).** SP-06 developed against SP-01's synthetic `testdata/golden/contracts/{chunk,canon,symbols,sketch}/` fixtures. SP-04 regenerated the `canon` and `symbols` fixtures from the real implementations in its commit 7; SP-03 froze the `sketch` fixtures in its commit 6. Re-run SP-06's fixture-consuming tests against the **real** implementations:

```bash
go test -count=1 ./internal/store/...
```

Any SP-06 test that only passed against a placeholder fixture fails here. Per Rule W-2, **fix the test or the implementation — never the fixture** unless the fixture is provably the synthetic placeholder SP-01 shipped and the real owner (SP-03/SP-04/SP-07) has already replaced it on `develop`.

### 3.7 SP-07 — dependence DAG and slicing

> This is graph reachability: BFS over a few thousand nodes, sub-millisecond.

> Thin slicing drops control-dependence-only edges for much smaller slices at the cost of soundness — probably the right tradeoff here.

> Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.

> `segment_coupling(p)` is the count of DAG edges crossing `p` — a direct, cheap measure of how much the post-`p` region depends on pre-`p` detail.

> Do not ship slicing or submodular selection before p-selection.

**Measurement procedure.**

```bash
# sub-millisecond, proven by a gate not an assertion
go test -bench 'BenchmarkBackwardSlice5000|BenchmarkForwardSlice5000|BenchmarkCrossingEdges' ./internal/dag/
go test -v -run 'TestSliceLatencyBudget|TestCrossingLatencyBudget' ./internal/dag/
# Expect: slices < 1ms median of 20; CrossingEdges < 5us median.

# the thin tradeoff, as numbers
go test -v -run TestThinVsFullComparison ./internal/dag/
# Expect: mean size_ratio <= 0.75, mean recall >= 0.85, precision >= full, ns_thin <= ns_full.

# scores, never keep/drop
go test -v -run TestNoBooleanKeepAPI ./internal/dag/

# segment_coupling correctness
go test -run 'TestCrossingEdgesGolden|TestCrossingEdgesBoundaries|PropCrossingEdgesMatchesBruteForce' ./internal/dag/

# the ship-order guard still holds with dag real and scheduler stubbed
go test -v -run 'TestGuard_SubmodularInertWithoutPSelection|TestGuard_SelectorRefusesWithoutPSelection' ./test/guards/
# Expect: analyzer.NewSelector still errors because scheduler.PSelectionAvailable() is false.
# dag shipping slice SCORES is legal (00-ARCH §5.12); a scattered keep-set driving a drop is not.
```

---

## New cross-component integration tests

These tests **only make sense now**. Each exercises a seam that did not exist on any single wave-1 branch. They are authored during this checkpoint and **become part of the permanent suite** — they are committed to `verify/v2` and run in CI from here on.

**Location.** A new package `test/integration/` (a composition root, additive to the §3.1 `test/` grouping alongside `test/e2e`, `test/dedup`, `test/replay`, `test/bench/hotpath`). It lives outside `internal/` because it imports across the §3.2 allow-sets deliberately — e.g. `chunk` + `canon` + `store` + `dag` + `eval` in one file, which no `internal/` package is permitted to do. Add `test/integration` to `compositionRoots` in `tools/devtool/importrules.go` in the same commit, so `importgraph` keeps enforcing "nothing may import it".

**Conventions.** `testify/require`; `testutil.NewProject(t)` for every project root; `testutil.FakeClock` for anything time-dependent; no `time.Sleep`; every test deterministic and CI-safe.

### 4.1 Store ingest with the real chunker, canonicalizers and MinHash

The seam: `store.Deps{Chunker, Canon, Symbols, Redact, Tokens}` were stubs or fakes on SP-06's branch. This is the first execution of the real §8.1 item-1 pipeline.

**`TestIntegration_StorePutUsesRealChunkerAndCanon`**
*Setup.* `p := testutil.NewProject(t)`; `s, err := store.Open(p.Root, p.Cfg, store.Deps{Chunker: chunk.New(chunk.FromConfig(p.Cfg)), Canon: canon.Default(p.Cfg.Store.Canonicalize), Symbols: symbols.New(), Tokens: tokens.NewExact(p.Cfg, calib, cache), Redact: redact.New(p.Cfg), Clock: p.Clock})` — every dep real, none nil, none faked.
*Input.* `testdata/corpora/toolout/testrunner/go-test-pass.txt` with `PutOptions{Tool:"Bash", Canon: canon.OptionsFrom(p.Cfg.Store.Canonicalize, true)}`.
*Expected.* `PutResult.Root.CanonBytes < PutResult.Root.RawBytes` (canonicalization actually ran); every chunk length in `[1024, 16384]` except the last; `chunk.RootHash(chunks) == PutResult.Root.Hash`; `io.ReadAll(s.Open(ctx, root))` equals `canon.Default(...).Run("Bash","",raw,opts).Canonical` byte-for-byte. This last equality is the whole point: it pins the store's internal pipeline order against the canonicalizer's public output.

**`TestIntegration_CanonKeepRawRestoresThroughStore`**
*Setup.* Same store, `PutOptions{KeepRaw: true}`.
*Input.* `testdata/corpora/toolout/bash/npm-install.txt` (timestamps + ANSI + durations).
*Expected.* Read back the canonical bytes and the stored delta root; `canon.Restore(canonical, deltas)` reproduces the **redacted** input byte-for-byte. Asserts SP-04's `Restore` inverse and SP-06's delta-root encoding agree on the wire format.

**`TestIntegration_StoreNearDupUsesRealMinHash`**
*Setup.* Real deps, `store.canonicalize.minhash.enabled = true`, `nearDupThreshold = 0.9` from config.
*Input.* `go-test-pass.txt` then `go-test-rerun.txt` (same suite, one new failure) on the same `Path`.
*Expected.* Second `PutResult.NearDup != nil`, `Jaccard >= 0.9`, `PriorRoot` = the first root, and `NearDup.DeltaBytes < len(second canonical)/2`. This is the §8.1 "same test suite, one new failure" claim, proven across SP-03's MinHash, SP-04's canonicalizers and SP-06's store for the first time.

**`TestIntegration_Phase1DedupRatioWithRealPipeline`** — *the §10 Phase 1 exit criterion, composed.*
*Setup.* Real deps. Build a read-heavy session in-test from `testdata/corpora/toolout/sp06/fileread-auth-v1..v4.txt`: 10 synthetic file paths × 4 reads each, with a 2-line edit between reads 1→2, an 18-line edit between 2→3 and a 240-line edit between 3→4 (40 puts total).
*Expected.* `s.Stats(ctx).DedupRatio >= 4.0`. Run the same body twice — once with `canonicalize.enabled = true`, once `false` — and assert the enabled ratio is **strictly greater**, then write both numbers into the completion report. This is the composed form of "measure with and without canonicalization"; `test/dedup` measures it at the chunk level, this measures it at the store level.

**`TestIntegration_SecretsNeverSurviveTheRealPipeline`**
*Setup.* Real deps including the real canonicalizers (which rewrite bytes *after* redaction).
*Input.* A payload containing one instance of each of the ten built-in secret families, embedded in ANSI-coloured `npm install` output with timestamps.
*Expected.* Walk every file under `objects/`, zstd-decompress, and assert none of the ten literals appears. Then assert `canon` did not reconstruct a secret by joining across a placeholder boundary: no object contains a substring of any secret longer than 8 characters. §13 invariant 7 under canonicalization, which no single branch could test.

### 4.2 Hook event → daemon → store → tombstone → retrieval round-trip

The seam the parent checkpoint calls out. `internal/observer` is SP-08 (wave 2), so the wiring uses the extension seam SP-05 shipped for exactly this purpose: `daemon.Options.Bind(func(*Services))`. That is legitimate — it is the seam's first real exercise — and the test-local binding is replaced by `observer.OnToolUse` in V3.

**`TestIntegration_HookEventThroughDaemonToStore`**
*Setup.*
1. `p := testutil.NewProject(t)`; open the real `store`, real `dag.Graph`, real `daemon.SketchSet` (bloom/cms/hll).
2. `opts := daemon.NewOptions(p.Root, p.Cfg)`; set `Store`, `Graph`, `Sketches`; `opts.Bind(func(s *daemon.Services){ s.ObserveTool = testObserveTool })` where `testObserveTool` performs exactly the §8.1 items 1, 4, 5 that SP-08 will later own: `store.PutBytes` → `store.RecordToolUse` → `store.AppendFileVersion` → `dag.BuildToolUse` → `cms.Add(pathKey,1)` + `hll.Add(pathKey)`, then returns `hookio.Empty()`.
3. Start the daemon; send 200 `observe.tool` requests through the real `ipc.Client` (in-process server is not enough here — use `test/e2e`'s real-binary harness for at least 20 of them so the wire format is exercised).
*Expected.*
- 200 ACKs, zero spool lines, `wal-<sess>.ndjson` has 200 lines.
- After `Drain`, `store.Stats().ToolUses == 200`; `dag.Stats().Nodes` equals the builder's expected count; `hll.Cardinality()` within 7 % of the distinct path count; `cms.Estimate(hotPath)` ≥ the true count.
- `dag` remains acyclic (run the same DFS `TestBuilderOutputIsAcyclic` uses).

**`TestIntegration_TombstoneRoundTripThroughStore`** — *the retrieval round-trip.*
*Setup.* Continue from the store above. Pick a recorded `store.ToolUseRecord` for a `FileRead` of `src/auth.ts`.
*Input.* `marker := observer.Tombstone(rec)` (the pure function SP-01 implemented).
*Expected.*
1. `marker` matches `^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$` — the §8.1 item 2 form, with the real short hash.
2. Parse the hash out of the marker with `core.ParseHash` after re-expanding the short form via `store.ToolUse(rec.ID).Root` — assert `rec.Root.Short()` is exactly the 12 hex chars the marker carries.
3. **Full re-expansion:** `io.ReadAll(store.Open(ctx, rec.Root))` equals the canonicalized bytes originally ingested. The tombstone is therefore addressable, not decorative — G3.2, end to end.
4. **Minimal span (§8.7):** `symbols.New().Enclosing(path, full, offsetOfRefreshToken)` gives `(sym, true)`; `store.OpenSpan(ctx, rec.Root, int64(sym.Offset), int64(sym.Len))` returns exactly `full[sym.Offset : sym.Offset+sym.Len]`. This proves SP-04's symbol extractor and SP-06's span reader resolve the same boundaries — the seam `mcp.expand` will sit on in wave 3.
5. `store.Search(ctx, store.Query{Symbol:"refreshToken", K:5})` returns a `Hit` whose `Span` equals that symbol span and whose `Root` is `rec.Root`.

**`TestIntegration_SupersessionMarksEarlierReadThroughDAG`**
*Setup.* Ingest `fileread-auth-v1.txt` then `-v2.txt` on the same path through the same binding, passing `Supersedes` to `dag.BuildToolUse` and calling `store.MarkSuperseded`.
*Expected.* `store.ToolUse(older).Status == StatusSuperseded` and `SupersededBy` set; `dag.Out(dag.ToolUseNode(older))` contains an `EdgeSupersedes` to the newer node; a `BackwardSlice` from the newer tool use scores the older at `0.85 * 0.30 = 0.255`. §8.1 item 3 across `store` + `dag`, which neither package could assert alone (`store` must not import `dag`; `dag` imports neither).

**`TestIntegration_SpooledEventsSurviveToTheStore`**
*Setup.* Force `state.bin` to `hot=1` (spool submode). Send 50 `observe.tool` events through the real binary. Then flip back to `sync`, start the daemon, and let one idle tick drain.
*Expected.* Zero connects during the spool phase (daemon accept counter unchanged); 50 spool lines; after drain, `store.Stats().ToolUses == 50` and no duplicates (`TestNAKDuplicateIsDedupedOnDrain`'s dedup rule holds against a real store). Proves D4's degradation path loses freshness, never data — with a real L1 behind it.

### 4.3 Contract monitor against a real store

`internal/contract` may import `store` (§3.2). On SP-05's branch that was the stub.

**`TestIntegration_ContractMonitorRunsAgainstRealStore`**
*Setup.* `contract.NewMonitor(log, metrics, statePath)` with `contract.Env{ProjectRoot, Event, Cfg, Store: realStore, Log, Clock: fakeClock, History}`; register `contract.StandardAssertions()`.
*Expected.* `RunAll` returns `ModeFull`; exactly four results at `SevInfo`/`not-yet-implemented`; `transcript.readable` and `hook.payload_shape` evaluate against real data; no assertion touches a `store` method that returns `ErrNotImplemented` (assert by failing the test if any result's `Detail` contains `not implemented`).

**`TestIntegration_DegradedPassiveStillWritesToTheRealStore`**
*Setup.* Force the monitor to `ModeDegradedPassive`; drive 20 `observe.tool` events through the bound `ObserveTool`.
*Expected.* All 20 land in the real store with roots, tool-use records and file versions; **no** `Output.HookSpecificOutput` is emitted on any hook. This is §12.1's "L0 and L1 keep running … the store stays correct and the session's data is not lost", verified against the actual L1 rather than a stub.

### 4.4 Replay harness fed by real store statistics

SP-02 committed `testdata/golden/contracts/store/stats-growth.json` as a W-2 placeholder because no real store existed. It does now.

**`TestIntegration_RealStoreGrowthIsSublinear`**
*Setup.* Real store with real deps. Put a 100 KB payload mutated 1 % per iteration, 256 times, sampling `store.Stats()` at turns 8, 16, 32, 64, 128, 192, 224, 256 (8 samples, 32× span — satisfying `CheckSublinearGrowth`'s ≥ 6 samples and ≥ 8× span rules).
*Expected.* `eval.CheckSublinearGrowth(samples).Sublinear == true` with `Exponent < 1.0`; `Reason` empty. Then assert the committed placeholder is still *shape-compatible* with the real samples (same JSON field set, `dedupRatio` present and > 1) so the gate's provider seam is proven swappable without editing `internal/eval`.

**`TestIntegration_ReplayGateAcceptsRealGrowthFile`**
*Setup.* Write the real samples from the previous test to a temp file; run the gate driver with `--growth <file>`.
*Expected.* Exit 0. Then truncate to 3 samples and re-run: exit non-zero with a message naming "at least 6". Proves SP-02's §11.3 guardrail is wired to a real producer, not only to a fixture.

**`TestIntegration_RealBloomHealthFeedsTheFPCeiling`**
*Setup.* A real `sketch.Bloom(10_000, 0.01)` filled to capacity; map `bloom.Stats()` into `eval.SketchHealth{FillRatio, EstFPRate}`.
*Expected.* The gate's §11.4 bloom check passes at the real `EstFPRate` (≈ 0.01) and fails when fed a synthetic `0.11`, with the §11.4 sentence in the message. `negknow` is SP-09 — assert nothing about records, active/stale counts, or `already_tried`.

### 4.5 DAG, positions and the p-selection substrate

**`TestIntegration_BeladyPMinLandsAtLowCoupling`**
*Setup.* For each of the 24 synthetic corpus sessions: derive `eval.Blocks(s, at)` at each compaction turn; build a `dag.Graph` whose node `Pos` values are the block positions and whose edges follow `dag.BuildToolUse` from the session's tool calls; compute `eval.BeladyDetail(...)` to get `KeepSet.P`.
*Expected.* Across all compaction events, `dag.CrossingEdges(P)` is **strictly lower than the mean of `CrossingEdges` over 32 uniformly sampled positions** in the same session, in at least 70 % of events. This is the first empirical evidence that "cut where coupling is low" and "cut where OPT drops the earliest block" agree — the hypothesis SP-12's p-selection is built on. It is deliberately a *measurement with a floor*, not a proof; record the actual percentage in the completion report as the wave-1 baseline.
*Guard.* The test must not import `analyzer` or `scheduler`, and must not construct a keep-set from slice scores — that would violate closing note 3 and `TestNoBooleanKeepAPI`'s intent.

**`TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything`**
*Setup.* The store + DAG from §4.2. Take a `BackwardSlice` from the most recent tool-use node.
*Expected.* Every scored `NodeID` with a `tu:` prefix resolves through `store.ToolUse` to a real record (no dangling references between the two packages' key schemes); scores are in `(0,1]`; the returned value is `map[NodeID]float32`, never a keep-set. Pins the `dag.NodeID` ↔ `core.ToolUseID` ↔ `paths.Key` conventions across three packages that cannot import each other.

**`TestIntegration_SymbolNodesUseTheRealExtractor`**
*Setup.* Ingest a real TypeScript fixture; run `symbols.New().Extract` and feed the names into `dag.BuildToolUse{Symbols: names}`.
*Expected.* One `sy:<pathKey>#<name>` node per extracted symbol, deduplicated, edges appended in ascending name order; `store.Search(Query{Symbol: name})` finds the same root. `dag` never imports `symbols` (the caller resolves) — assert that the import-graph check still passes after this test exists.

### 4.6 The hot path with real resident state

**`TestIntegration_HotPathWarmWithRealResidentState`** (drives `test/bench/hotpath`, asserted rather than only reported)
*Setup.* Start a real daemon over a project pre-populated with **real** state: 2 000 tool-use records and 40 MB of raw tool output in the real store, a real DAG of ~5 000 nodes / ~15 000 edges, and the real sketch set (12 KB bloom, 54 KB CMS, 2 KB HLL) resident. Bind the §4.2 `ObserveTool`.
*Input.* 2 000 real process spawns of `qompack observe tool` with a representative payload.
*Expected.* **B-A p99 < 15 ms** and **B-B p99 < 2 ms**, and `hotPathMode` never transitions to `spool` during the run (assert `state.bin` still reports `hot=0` and no WARN transition line). Record B-D and `spawn_floor_ms`.
*Why it is new.* SP-05 measured this against stubs; §2.1 of `00-ARCHITECTURE.md` argues the *real* cold-state cost is what threatens the budget. This is the first honest measurement of the claim that the daemon removes it.

**`TestIntegration_HotPathDegradesRatherThanBlocks`**
*Setup.* Same warm daemon; inject an artificial 25 ms stall into the bound `ObserveTool` for three consecutive 512-sample windows.
*Expected.* The daemon flips to `spool`, clients stop connecting, every hook still exits 0, no event is lost after the next drain, and one WARN line plus the `status` payload record the transition. §8.1's "degrade to async queue-and-drain rather than blocking", proven with a real L1 under load.

### 4.7 Store, daemon and the append-only invariant under concurrency

**`TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites`**
*Setup.* Real daemon + real store + real DAG + real sketch set over one `.qompack/`; 8 concurrent sessions each driving 200 events, plus an idle tick running `store.GC` with a deadline and `dag.Compact`.
*Expected.* Under `-race`: no data race; `testutil.Project.AssertAppendOnly(t)` passes afterwards; `checkpoints/`, `pins/`, `sketches/tried.bloom` untouched; `dag/deps.jsonl` monotonically grows except for the single documented `Compact` rewrite, which bumps `Generation`; every `*.jsonl` still parses; `store.Open` on reopen reports the same `Stats` as before shutdown.

**`TestIntegration_GCNeverCollectsALiveRootUnderIngest`**
*Setup.* Run `store.GC` with a short deadline concurrently with ingest.
*Expected.* `GCReport.Truncated == true`; the mark phase restarts on live-digest mismatch; no root written during the run is ever collected; a subsequent unbounded GC completes and the union of reports equals a single unbounded run.

### 4.8 Test registration and CI wiring

In the same commit that adds `test/integration/`:

- add `"test/integration": true` to `compositionRoots` in `tools/devtool/importrules.go`;
- confirm `go run ./tools/devtool lint` still passes (`importgraph`, `testdeps`, `bindeps`, `sleepcheck`);
- confirm the `test` CI job picks the package up on all three OSes (`go test ./...` already covers it);
- record every new test name in the §8 completion report.

---

## Performance budget validation

Every latency/size budget **in force at this point**. Budgets whose component does not exist yet are marked N/A and must be reported as N/A, not as a pass. Measure on one quiet machine; append results to `testdata/bench-baseline.txt` and diff with `benchstat`.

### 5.1 Normative latency budgets (`00-ARCHITECTURE.md` §2.4, `Qompack.md` §8.1 / §11.3)

| ID | Clock | Threshold | Command | Status at V2 |
|---|---|---|---|---|
| **B-A** | `hook_controlled`: client `main()` → `exit` | **p99 < 15 ms** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v2-bench.json` **with the real store/DAG/sketches resident** (§4.6) | **In force. Hard gate.** Must pass on ubuntu, macos, windows |
| **B-B** | `l0_ingest`: daemon read → WAL append returned | p99 < 2 ms | same run; plus `go test -bench BenchmarkIngestAccept ./internal/daemon/` | **In force. Hard gate** |
| **B-C** | `l0_process`: WAL → chunked, stored, DAG/sketches updated | p99 < 50 ms (**soft**) | measure through §4.2's binding: instrument the bound `ObserveTool` with an `obs.Histogram` and read its p99 after 2 000 events | **In force, soft.** Overrun ⇒ sampling/backpressure, never blocking. Record the number; do not gate |
| **B-D** | `hook_wall`: includes host process creation | reported, never gated | same bench run; `spawn_floor_ms` and `b_a_method` present | **Reported.** Gating it would be dishonest — it is the host's cost |
| **B-E** | `checkpoint_finalize`: `PreCompact` entry → exit | **p99 < 2 s** | same bench run, `--hook checkpoint` | **In force**, but currently exercises the client + daemon + nil-`Services.PreCompact` path. Annotate accordingly |
| **B-F** | `mcp_tool_call`: request → response | p95 < 250 ms | — | **N/A at V2.** `internal/mcp` is a stub (SP-13, wave 3). Report N/A |

### 5.2 Size and ratio budgets (`Qompack.md` §10 Phase 1, §11.3)

| Budget | Threshold | Command | Status |
|---|---|---|---|
| **Store dedup ratio, read-heavy** | **≥ 4:1** | `go test -v -run 'TestPhase1ExitCriterion_ReadHeavy' ./internal/store/` and `go test -v -run TestIntegration_Phase1DedupRatioWithRealPipeline ./test/integration/` | **In force.** Both must report `DedupRatio ≥ 4.0` |
| **Canonicalization gain** | `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0` | `go test -v ./test/dedup/` ; `jq '.groups[], .overall' testdata/canon-dedup-report.json` | **In force.** "measure with and without canonicalization" |
| **Store growth sublinear** | `CheckSublinearGrowth(...).Sublinear == true`, `Exponent < 1.0` | `go test -run TestStats_SublinearGrowth ./internal/store/` ; `go test -v -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/` | **In force** |
| Sketch sizes match Appendix A | bloom 11 984 B body (m=95 872, k=7); CMS 54 380 B (2719×5); HLL 2 102 B frame | `go test -v -run 'AppendixASizing\|AppendixSizing' ./internal/sketch/` | **In force** |
| Bloom FP ceiling (§11.4) | measured FP ∈ [0.008, 0.013] at capacity; `Saturated()` at ≥ 0.10 | `go test -v -run 'TestBloom_EstimatedFPRateMatchesEmpirical\|TestBloom_SaturatedThreshold' ./internal/sketch/` | **In force** |
| Rehydration budget 8–12 K | — | — | **N/A.** `internal/rehydrate` is SP-11 |
| Checkpoint budget 12 K | — | — | **N/A.** `internal/checkpoint` is SP-10 |

### 5.3 Component micro-budgets (all in force; `benchstat` gated at >10 % warn / >25 % fail)

| Component | Budget | Command |
|---|---|---|
| `chunk.Split` 100 KB | < 800 µs/op | `go test -bench BenchmarkSplit_100KB -benchmem ./internal/chunk/` |
| gear scan 1 MiB | ≥ 400 MB/s | `go test -bench BenchmarkGearScan_1MiB ./internal/chunk/` |
| `chunk.Split` 1 MiB | ≥ 120 MB/s, ≤ 2 allocs/op | `go test -bench BenchmarkSplit_1MiB -benchmem ./internal/chunk/` |
| `SplitStream` 4 MiB | ≤ 40 ms/op | `go test -bench BenchmarkSplitStream_4MiB ./internal/chunk/` |
| `RootHash` 1 000 chunks | < 40 µs/op | `go test -bench BenchmarkRootHash_1000Chunks ./internal/chunk/` |
| `canon.Run` bash 100 KB | < 3 ms/op | `go test -bench BenchmarkRun_Bash100KB ./internal/canon/` |
| `canon.Run` go test output | < 1 ms/op | `go test -bench BenchmarkRun_GoTest ./internal/canon/` |
| `canon.Restore` 100 KB | < 1 ms/op | `go test -bench BenchmarkRestore_100KB ./internal/canon/` |
| `symbols.Extract` 100 KB | < 2 ms/op | `go test -bench BenchmarkExtract_100KB ./internal/symbols/` |
| `symbols.Enclosing` 100 KB | < 2 ms/op | `go test -bench BenchmarkEnclosing_100KB ./internal/symbols/` |
| `symbols.References` 100 KB / 50 names | < 1 ms/op | `go test -bench BenchmarkReferences_100KB_50Names ./internal/symbols/` |
| **`L0SketchUpdate`** (CMS+HLL+Bloom, the §8.1 item-5 share of B-A) | **≤ 5 µs/op, 0 allocs/op** | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` + `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` |
| Bloom/CMS/HLL `Add`/`Test`/`Estimate` | ≤ 1.0 µs/op, 0 allocs | `go test -bench 'BloomAdd\|BloomTest\|CMSAdd\|CMSEstimate\|HLLAdd' -benchmem ./internal/sketch/` |
| `HLL.Cardinality` | ≤ 25 µs/op | `go test -bench BenchmarkHLLCardinality ./internal/sketch/` |
| `MisraGries.Add` | ≤ 5 µs/op amortized | `go test -bench BenchmarkMisraGriesAdd ./internal/sketch/` |
| `MinHash` 4 KiB / 100 KiB | ≤ 1.5 ms / ≤ 2.5 ms (B-C, **not** B-A) | `go test -bench 'MinHash4KiB\|MinHash100KiB' ./internal/sketch/` |
| sketch marshal/unmarshal | bloom ≤ 60 µs, CMS ≤ 250 µs | `go test -bench 'Marshal\|Unmarshal' ./internal/sketch/` |
| `RebuildBloom` 5 000 keys | ≤ 15 ms/op | `go test -bench BenchmarkRebuildBloom5000 ./internal/sketch/` |
| `store.PutBytes` 100 KB cold / warm | ≤ 3 ms / ≤ 400 µs | `go test -bench 'PutBytes_100KB' -benchmem ./internal/store/` |
| `store.GetChunk` warm | ≤ 60 µs/op | `go test -bench BenchmarkGetChunk ./internal/store/` |
| `store.OpenSpan` 4 KB of 4 MB | ≤ 150 µs/op | `go test -bench BenchmarkOpenSpan_4KB_of_4MB ./internal/store/` |
| `store.Search` 1 000 roots / 8 MB | ≤ 25 ms/op | `go test -bench BenchmarkSearch_1000Roots ./internal/store/` |
| `store.Open` 50 000 roots | ≤ 400 ms/op | `go test -bench BenchmarkOpenStore_50kRoots ./internal/store/` |
| `store.MarkEncoded` 100 segments | ≤ 1 ms/op (inside B-E) | `go test -bench BenchmarkMarkEncoded_100 ./internal/store/` |
| `store.GC` 50 000 objects | ≤ 2 s/op, deadline ±50 ms | `go test -bench BenchmarkGC_50kObjects ./internal/store/` |
| `redact.Redact` 100 KB | ≤ 2 ms/op | `go test -bench . ./internal/redact/` |
| `tokens.EstimateRoot` 64 cached | ≤ 5 µs/op | `go test -bench BenchmarkEstimateRoot_64Cached ./internal/tokens/` |
| `dag.BackwardSlice` / `ForwardSlice` 5 000 nodes | **< 1 ms/op** median of 20 | `go test -bench 'Slice5000' ./internal/dag/` + `go test -run TestSliceLatencyBudget ./internal/dag/` |
| `dag.CrossingEdges` 15 000 edges | **< 5 µs** median | `go test -bench BenchmarkCrossingEdges ./internal/dag/` + `go test -run TestCrossingLatencyBudget ./internal/dag/` |
| `dag.BuildToolUse` | < 3 µs/op | `go test -bench BenchmarkAddToolUse ./internal/dag/` |
| `ipc.ReadState` | < 100 µs/op | `go test -bench BenchmarkReadState ./internal/ipc/` |
| `ipc` encode 4 KB request | < 5 µs/op | `go test -bench BenchmarkEncodeRequest ./internal/ipc/` |
| `ipc` server round trip | p99 < 2 ms | `go test -bench BenchmarkServerRoundTrip ./internal/ipc/` |
| `eval` E-2/E-3/E-4/E-5 | 250 ms / 50 ms / 20 ms / 15 ms per op | `go test -bench . ./internal/eval/` |
| `obs.Histogram.Observe` | < 100 ns/op | `go test -bench BenchmarkHistogram_Observe ./internal/obs/` |
| `config.Load` cold | < 2 ms/op | `go test -bench BenchmarkConfigLoad_ColdNoFiles ./internal/config/` |
| `paths.WriteAtomic` 4 KB | < 2 ms/op | `go test -bench BenchmarkPathsWriteAtomic_4KB ./internal/paths/` |

**Baseline discipline.** After every budget above has been measured on `verify/v2`:

```bash
go test -bench=. -benchmem -run '^$' ./... > v2-bench.txt
go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat testdata/bench-baseline.txt v2-bench.txt
```

Any micro-benchmark >10 % worse than the committed baseline is a warning that must be explained in the completion report; >25 % worse fails the build (`00-ARCHITECTURE.md` §7). Update `testdata/bench-baseline.txt` in the final `verify/v2` commit so wave 2 has an integrated baseline rather than six per-branch ones.

---

## Regression

### 6.1 Re-run all prior verification checkpoints' inventories

**V1 (wave 0, SP-01).** V1's inventory is re-run in full.

- If `plans/V1-VERIFY-foundation-and-contracts.md` exists, execute **its** "Cumulative functionality inventory" section verbatim, every row, before doing anything else in this checkpoint.
- If it does not exist in the tree, §2.1 above (`V2-SP01-01` … `V2-SP01-26`) **is** the V1 inventory, restated for this checkpoint, and executing §2.1 discharges the obligation.

Either way, the following V1-era invariants must hold unchanged after six branches landed on top of them, and each is a regression if it does not:

| V1 invariant | Command | Expected |
|---|---|---|
| Appendix C reproduced verbatim | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green |
| Append-only guard has teeth | `go test -run TestAppendOnlyGuard ./internal/paths/` | all five illegal writes fail |
| Hooks always exit 0 | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` + `go test -run TestHooksExitZeroUnderFaults ./test/e2e/` | 30/30 and 66/66 |
| Fresh build reports `ModeFull` | `go test -run TestGuard_FreshBuildReportsModeFull ./test/guards/` | green, four `not-yet-implemented` |
| Four closing-note guards | `go test ./test/guards/` | all green; submodular still inert |
| No network / confined write set | `go test -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' ./test/guards/` | green |
| Plugin bundle never drifts | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | clean |
| Config docs never drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | clean |
| Import graph still a DAG | `go run ./tools/devtool lint` | `importgraph` clean with six real packages + `test/integration` |
| Shipped binary dependency closure | `go run ./tools/devtool lint` (`bindeps`, all six GOOS/GOARCH) | only stdlib, `qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| `Qompack.md` unmodified | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |

**There is no V2 predecessor other than V1.** V2 is the second checkpoint; wave 1 is the first parallel wave.

### 6.2 The 2 % no-regression guardrail (§11.3)

> No metric may regress by more than 2% to improve another without explicit sign-off

Enforcement at this checkpoint:

```bash
# 1) the replay gate applies the rule to every metric in MetricsOf, per policy
go run ./tools/devtool replay -- \
  --corpus testdata/sessions/synthetic \
  --baseline testdata/baseline/phase0.json \
  --phase 0 --ci --json v2-replay.json
# Expect exit 0 and "regressions": [] .

# 2) prove the rule is live rather than vacuous, both sides of the boundary
go test -v -run 'TestGate_TwoPercentBoundaryExclusive|TestGate_ImprovementNeverRegresses|TestGate_LowerBetterMetricDirection|TestGate_ZeroBaselineUsesAbsoluteTolerance' ./test/replay/

# 3) the sign-off escape hatch is narrow and named
go test -v -run 'TestGate_SignOffAllowsNamedMetricOnly|TestGate_SignOffRejectsShortReason' ./test/replay/
```

Rules for this checkpoint specifically:

- **A regression discovered here is fixed, not signed off.** The sign-off trailer exists for a deliberate trade in a feature PR. A verification checkpoint that signs off its own regression has defeated its purpose. If a metric genuinely must move, the sign-off goes on the wave-2 PR that trades it, with a reason naming that exact metric.
- **The baseline is not rewritten to make the gate pass.** `testdata/baseline/phase0.json` is regenerated only if the *corpus* changed, and a corpus change at V2 is out of scope. `TestGate_CorpusStaleness` and `TestSynthesize_MatchesCommittedCorpus` both catch this.
- **Benchmarks obey the same spirit** through `benchstat`: >10 % warns (explain in the report), >25 % fails (fix on `verify/v2`).
- Record, in the completion report, the delta of every §11.2 metric against `testdata/baseline/phase0.json` even when it is zero. "No change" measured is worth more than "no change" assumed — that is `Qompack.md` §1.3 RC-3 applied to our own process.

---

## Failure protocol

Any failing row in §2, any unmet criterion in §3, any failing test in §4, any breached budget in §5, any regression in §6 triggers this protocol. There is no "note it and move on".

### 7.1 Systematic diagnosis (do this before writing a fix)

1. **Reproduce deterministically.** Re-run the exact failing command with `-count=1`. If it is flaky, that is itself the bug — flakiness at an integration seam is a real defect, not noise. Fix the nondeterminism (usually: a wall-clock read that should be `testutil.FakeClock`, a map-iteration order, or an unsynchronized shared sketch).
2. **Localize to a seam or a package.** Ask: does the failure reproduce with a *stub* on the other side of the seam? If yes, the defect is inside one package and belongs to that subplan's code. If no, it is a seam defect — the two implementations disagree about a contract in `00-ARCHITECTURE.md` §5.
3. **Classify.** Exactly one of:
   - **(a) Implementation bug** in a wave-1 package → fix the implementation.
   - **(b) Test/assertion bug** — the test asserts something the design never promised → fix the test, and quote the design sentence that justifies the change in the commit body.
   - **(c) W-2 fixture divergence** → per Rule W-2, *"any fixture that the real implementation cannot reproduce is a verification failure, not a fixture bug."* The default is to fix the implementation. Regenerating a fixture is permitted **only** when the fixture is provably SP-01's synthetic placeholder and the owning subplan has already replaced it on `develop`; say so explicitly in the commit body and name the owning subplan.
   - **(d) Interface defect** — §5 of `00-ARCHITECTURE.md` is genuinely wrong. Do **not** work around it. Open `arch/<short-reason>` off `develop`, amend §5, merge it, rebase `verify/v2`, then fix. This is §0's amendment rule; silent divergence is the one failure mode that makes parallel waves worthless.
   - **(e) Budget breach** → profile before optimizing. Attribute the cost (`go test -bench -cpuprofile`, the daemon's per-stage histograms, `b_a_method`/`spawn_floor_ms` from the bench artifact). A budget met by removing work that the design requires is a failure disguised as a pass.
4. **Never weaken the check.** Do not add `//nolint`, do not add `t.Skip`, do not add a `//nomagic:allow`, do not lower a threshold, do not regenerate a golden to match broken output, do not delete an assertion. If a threshold is genuinely wrong, that is class (d) and needs an amendment commit with the measurement that justifies it.
5. **Write the failing test first if one does not exist.** If a defect slipped through wave 1, the reason is a missing test. Add it to the permanent suite (in the owning package, or in `test/integration/` if it is a seam), observe it fail, then fix.

### 7.2 Fixing

- All fixes are committed to **`verify/v2`**. Never to `develop` directly, never to a merged `feat/sp0N-*` branch, never as an amended wave-1 commit.
- **Small conventional commits, as many as needed.** There is no commit-count budget on a verification branch — the 5–8 rule is a subplan rule. One coherent fix per commit. Format per `00-ARCHITECTURE.md` §10: `<type>(<scope>): <subject>`, body explaining the decision rather than the diff, footer `Refs: V2, SP-NN, §<sections>`. Example:

  ```
  fix(store): use canon output length for RawBytes on a dedup hit

  A put whose canonical form matches an existing root reported the prior
  put's RawBytes, which understated Stats.DedupRatio by the difference in
  volatile bytes between the two reads. Discovered at V2 by
  TestIntegration_Phase1DedupRatioWithRealPipeline, which is the first test
  to run the real canonicalizers against the real store.

  Refs: V2, SP-06, SP-04, §10 Phase 1, §8.2
  ```

- **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

  That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR body on this branch. CI's `verify` job greps for `Co-Authored-By`, `Signed-off-by`, `Generated with` and `🤖` in the commit range and fails the build if any appear.

- Each fix commit compiles and passes `go run ./tools/devtool test` for the packages it touches before it is made.

### 7.3 Re-run the whole checkpoint from the top

After **any** fix — even a one-line one — **re-run this checkpoint from §2 row 1.** Not just the failing row, not just the affected package. Wave 1's whole risk profile is composition: a fix in `canon` moves chunk boundaries, which moves dedup ratios, which moves store size, which moves B-C, which can move B-A. A partial re-run cannot see that.

Practically: re-dispatch all seven subagents against the fixed tree, then re-run §4, §5 and §6 in the main session. Record how many full passes it took in the completion report.

### 7.4 The gate

> **No wave-2 branch is cut until this checkpoint is fully green.**

`feat/sp08-observer-l0` and `feat/sp09-negative-knowledge` do not exist until every row of §2, every criterion of §3, every test of §4, every budget of §5 and every item of §6 passes on `verify/v2` **and** CI is green on the branch across `verify`, `test` (×3 OS), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.

A partially-green checkpoint is not a checkpoint. If something cannot be fixed, it is class (d) and needs an architecture amendment, not a waiver.

### 7.5 Merge and tag

Once fully green:

```bash
go run ./tools/devtool ci-local                 # final full local gate
git log --format=%B develop..verify/v2 | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'   # no output
git checkout develop
git merge --no-ff verify/v2 -m "chore(verify): V2 — wave-1 verification checkpoint

Re-verified every functionality delivered by SP-01..SP-07 on the integrated
develop, added the cross-component integration suite under test/integration,
and re-measured every budget in force with the real store, DAG and sketches
resident rather than against stubs.

Refs: V2, SP-01, SP-02, SP-03, SP-04, SP-05, SP-06, SP-07"
git tag v0.1.0
git push origin develop --tags
```

Then, and only then, cut wave 2 from the post-verification `develop`:

```bash
git checkout -b feat/sp08-observer-l0        # and, separately, feat/sp09-negative-knowledge
```

---

## Completion report template

Fill this in and paste it as the checkpoint's output. Every row gets a verdict. `N/A` is a legitimate verdict only where this document already marks it so; anything else must be `PASS` or `FAIL`.

```markdown
# V2 completion report

- Branch: verify/v2 (cut from develop @ <sha>)
- develop head at start: <sha>   | verify/v2 head at end: <sha>
- Wave-1 merge order confirmed: SP-02 → SP-03 → SP-04 → SP-05 → SP-06 → SP-07  [ ]
- Full checkpoint passes required: <n>
- Fix commits on verify/v2: <n>   (list below)
- Platforms measured: ubuntu-latest / macos-latest / windows-11-dev
- Date: <ISO 8601>

## 1. Inventory — SP-01 foundation
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP01-01 | Repo shape and root commit | | |
| V2-SP01-02 | build + vet | | |
| V2-SP01-03 | devtool lint (7 sub-checks) | | |
| V2-SP01-04 | fmt-check | | |
| V2-SP01-05 | internal/core | | |
| V2-SP01-06 | internal/paths | | |
| V2-SP01-07 | Append-only guard | | 5/5 illegal writes rejected: |
| V2-SP01-08 | Appendix C verbatim | | |
| V2-SP01-09 | Config precedence / merge / env / null | | |
| V2-SP01-10 | Config fallback-not-crash + validation table | | |
| V2-SP01-11 | Config schema + docs no-drift | | |
| V2-SP01-12 | logging Loud channel | | |
| V2-SP01-13 | obs histograms + B-A..B-F | | |
| V2-SP01-14 | hookio seven payloads | | |
| V2-SP01-15 | Hooks exit 0 (30 faults) | | 30/30: |
| V2-SP01-16 | plugin-validate no-drift | | |
| V2-SP01-17 | build-all six targets | | |
| V2-SP01-18 | W-1 skip message discipline | | |
| V2-SP01-19 | Zero skips in wave-0/1 packages | | |
| V2-SP01-20 | Closing-note build-order guards | | |
| V2-SP01-21 | No network / write set | | |
| V2-SP01-22 | e2e six hooks | | |
| V2-SP01-23 | tokens baseline preserved | | 6/6 SP-01 tests unmodified: |
| V2-SP01-24 | Pure functions (RootHash/YoungDaly/SkiRental/…) | | |
| V2-SP01-25 | SP-01 benchmark budgets | | ns/op: |
| V2-SP01-26 | commit-msg policy machinery | | |

## 2. Inventory — SP-02 replay / Belady / baseline
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP02-01 | package -race | | |
| V2-SP02-02 | Blocks / Demands | | |
| V2-SP02-03 | Belady OPT | | |
| V2-SP02-04 | Breakpoint OPT + disclaimer | | |
| V2-SP02-05 | Policy registry | | PolicyNames(): |
| V2-SP02-06 | Replay determinism | | |
| V2-SP02-07 | Divergence metrics | | |
| V2-SP02-08 | Fraction-of-OPT + §11.2 metrics | | |
| V2-SP02-09 | Synthesizer + 24-session corpus | | |
| V2-SP02-10 | Redacting importer | | |
| V2-SP02-11 | Sublinear-growth checker | | |
| V2-SP02-12 | evaltest suite, zero skips | | |
| V2-SP02-13 | Replay gate (19 rows) | | |
| V2-SP02-14 | Baseline reproducible | | byte-identical across runs: |
| V2-SP02-15 | Gate end to end | | stock=<x> null=<0.0> oracle=<1.0> |
| V2-SP02-16 | Honesty tags in the report | | |
| V2-SP02-17 | eval import purity | | |
| V2-SP02-18 | FuzzRedact 60 s | | |
| V2-SP02-19 | E-1..E-5 budgets | | |
| V2-SP02-20 | coverage ≥ 85 % | | actual: |

## 3. Inventory — SP-03 sketches
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP03-01 | package -race -count=2 | | |
| V2-SP03-02 | QPKS header / CRC / hashing | | |
| V2-SP03-03 | Bloom Appendix-A sizing | | mRaw/mBits/k/body: |
| V2-SP03-04 | Bloom FP measured | | empirical / estimated: |
| V2-SP03-05 | Saturation + resize | | |
| V2-SP03-06 | RebuildBloom | | ms for 5 000 keys: |
| V2-SP03-07 | CMS sizing + overestimate-only | | width/depth/body: |
| V2-SP03-08 | CMS merge / scale / heavy hitters | | |
| V2-SP03-09 | HLL sizing + error bounds + merge | | rel. err at 1e3/1e4/1e5/1e6: |
| V2-SP03-10 | Misra-Gries guarantees | | |
| V2-SP03-11 | MinHash behaviour | | one-new-failure Jaccard: |
| V2-SP03-12 | Save/Load/LoadWithLog | | |
| V2-SP03-13 | tried.bloom generational replacement | | |
| V2-SP03-14 | 12 properties | | |
| V2-SP03-15 | 5 fuzz targets × 60 s | | |
| V2-SP03-16 | Frozen goldens (no -update) | | |
| V2-SP03-17 | import purity | | |
| V2-SP03-18 | L0SketchUpdate budget | | ns/op, allocs: |
| V2-SP03-19 | remaining micro-budgets | | |
| V2-SP03-20 | coverage ≥ 90 % | | actual: |

## 4. Inventory — SP-04 chunk / canon / symbols
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP04-01 | three packages -race | | |
| V2-SP04-02 | params validate + clamp | | |
| V2-SP04-03 | size bounds + contiguity | | |
| V2-SP04-04 | cross-platform determinism | | goldens identical on 3 OSes: |
| V2-SP04-05 | boundary stability | | ≤2 novel in __% ; ≤3 in __% ; max __ |
| V2-SP04-06 | mean size + RootHash | | mean bytes: |
| V2-SP04-07 | SplitStream ≡ Split | | |
| V2-SP04-08 | FuzzSplit 120 s | | |
| V2-SP04-09 | chunk benchmarks | | 100 KB µs, MB/s: |
| V2-SP04-10 | registry order + gating | | |
| V2-SP04-11 | overlap + non-growing guard | | |
| V2-SP04-12 | idempotence / non-growth / inverse | | |
| V2-SP04-13 | seven generic canonicalizers | | |
| V2-SP04-14 | seven per-tool canonicalizers | | |
| V2-SP04-15 | every Match carries a Class | | |
| V2-SP04-16 | Restore errors + FuzzRestore | | |
| V2-SP04-17 | Decide + Signature + OptionsFrom | | |
| V2-SP04-18 | canon golden corpus | | |
| V2-SP04-19 | dedup report (with/without) | | testrunner gain __ ; overall gain __ |
| V2-SP04-20 | canon benchmarks | | |
| V2-SP04-21 | symbols ten dialects | | |
| V2-SP04-22 | Enclosing / References | | |
| V2-SP04-23 | FuzzExtract 120 s | | |
| V2-SP04-24 | symbols benchmarks | | |
| V2-SP04-25 | conformance suites, zero skips | | |
| V2-SP04-26 | corpus hygiene | | |
| V2-SP04-27 | coverage 90/90/75 | | actual: |

### 4.8 SP-04 carried defects — resolve or re-defer every row

SP-04 shipped six defects it knowingly did not fix, recorded in `plans/CARRIED-DEFECTS.tsv` with the
diagnosis and acceptance criteria for each in `plans/V2-SP-04-carried-defects.md`. They are listed
here because two of them change what this checkpoint must do, and one of them will interrupt it:

| id | what it costs | what this checkpoint owes it |
|---|---|---|
| SP04-D1 | JSON-escaped Windows temp paths are not canonicalized, so they fork the dedup space and carry user names into stored content | fix, or re-defer with a reason |
| SP04-D2 | canonicalization is not idempotent when a deletion joins two fragments; §5.6 says it must be | a DECISION, not a patch — the complete fix changes the `canon.Delta` contract SP-06 stores against |
| SP04-D3 | three timestamp/duration edges are wrong in the patterns and faithfully reproduced by the scanner | fix, or re-defer with a reason |
| SP04-D4 | `devtool cover`'s `landedSubplans` does not list SP-02 or SP-03, so **their coverage floors are off on the merged `develop`** | add both at the merge; the `cover` run fails until you do |
| SP04-D5 | canonicalization cost is now dominated by per-rule prefilter scans, ~1 ms of headroom left | re-measure and record; open a successor row if the headroom has gone |
| SP04-D6 | `BenchmarkRun_Bash100KB` measured ±33% on the reference host, which the 25% bench-gate cannot tell from a regression | record the baseline from a quiet machine at `-count 10`, or exempt this one benchmark with the distribution as justification |

`test/guards/carrieddefects_test.go` enforces this: it fails once `plans/V2-report.md` exists while
any row owned by `V2-VERIFY` is still `open`. Resolving a row means setting its status to `fixed`, or
to `deferred:<checkpoint>` with the reason written into the detail document. Both are fine. Leaving a
row open is what the gate stops.

```bash
go test ./test/guards/ -run TestCarriedDefects -v
# Expect: three PASS. After V2-report.md is written, expect a failure naming every unresolved row.
```

## 5. Inventory — SP-05 daemon / IPC / contract
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP05-01 | three packages -race -count=2 | | |
| V2-SP05-02 | address resolution | | |
| V2-SP05-03 | NDJSON framing byte-exact | | |
| V2-SP05-04 | 32-byte state record | | ReadState µs/op: |
| V2-SP05-05 | Send never errors | | |
| V2-SP05-06 | spool append-only + drop-safe | | |
| V2-SP05-07 | server routing / panic / concurrency | | |
| V2-SP05-08 | transport permissions | | |
| V2-SP05-09 | ipctest zero skips | | |
| V2-SP05-10 | singleton lock + lazy spawn | | |
| V2-SP05-11 | session registry | | |
| V2-SP05-12 | WAL ingest (B-B) | | p99: |
| V2-SP05-13 | drain idempotent/resumable | | |
| V2-SP05-14 | idle controller | | |
| V2-SP05-15 | sync→spool transition | | |
| V2-SP05-16 | all-nil Services tolerance | | 13/13 ops: |
| V2-SP05-17 | degraded-passive / off semantics | | |
| V2-SP05-18 | config reload defers chunk change | | |
| V2-SP05-19 | SketchSet never writes tried.bloom | | |
| V2-SP05-20 | idle exit | | |
| V2-SP05-21 | ModeFull + 4 not-yet-implemented | | count: |
| V2-SP05-22 | degrade loud / restore on 2 clean | | |
| V2-SP05-23 | nine assertions individually | | |
| V2-SP05-24 | budget table matches §2.4 | | |
| V2-SP05-25 | 66-combination fault table | | 66/66: |
| V2-SP05-26 | daemon e2e | | |
| V2-SP05-27 | bench-hotpath B-A/B-B/B-D/B-E | | see §9 |
| V2-SP05-28 | security posture | | |
| V2-SP05-29 | coverage ≥ 75 % ×4 | | actual: |

## 6. Inventory — SP-06 store / redact / tokens
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP06-01 | three packages -race -count=2 | | |
| V2-SP06-02 | ten redaction rule families | | |
| V2-SP06-03 | idempotent / bounded / deterministic | | |
| V2-SP06-04 | user-pattern admission | | |
| V2-SP06-05 | token classification | | |
| V2-SP06-06 | media sizing (G10.2) | | PNG/PDF values: |
| V2-SP06-07 | exact chunk accounting | | EstimateRoot µs/op: |
| V2-SP06-08 | calibration clamp + persist | | |
| V2-SP06-09 | fanout / zstd / dedup | | |
| V2-SP06-10 | redact→canon→chunk order | | |
| V2-SP06-11 | per-put raw accounting | | |
| V2-SP06-12 | near-dup detection | | |
| V2-SP06-13 | GetChunk / Open / OpenSpan / Has | | |
| V2-SP06-14 | index durability + tolerance | | |
| V2-SP06-15 | tool_use index + supersession | | |
| V2-SP06-16 | file history + ChangedSince | | |
| V2-SP06-17 | segment log + DPI guard | | ErrAlreadyEncoded: |
| V2-SP06-18 | search | | ms/op: |
| V2-SP06-19 | DedupRatio + sublinear growth | | ratio: |
| V2-SP06-20 | GC mark-and-sweep | | s/op, deadline error: |
| V2-SP06-21 | flush + session index + append-only | | |
| V2-SP06-22 | property suite | | |
| V2-SP06-23 | frozen index formats | | |
| V2-SP06-24 | store e2e incl. secret walk | | |
| V2-SP06-25 | store/redact/tokens benchmarks | | |
| V2-SP06-26 | conformance suites, zero skips | | |
| V2-SP06-27 | coverage 90/90/90 | | actual: |

## 7. Inventory — SP-07 DAG / slicing
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP07-01 | package -race | | |
| V2-SP07-02 | 9 node kinds / 8 edge kinds / multipliers | | |
| V2-SP07-03 | NodeID scheme + goldens | | |
| V2-SP07-04 | mutation semantics | | |
| V2-SP07-05 | concurrency | | |
| V2-SP07-06 | CrossingEdges = segment_coupling | | µs median: |
| V2-SP07-07 | NodesAfter total order | | |
| V2-SP07-08 | scored slicing | | |
| V2-SP07-09 | thin default + subset property | | |
| V2-SP07-10 | slice goldens | | |
| V2-SP07-11 | thin-vs-full measurement | | size_ratio __ recall __ precision __ |
| V2-SP07-12 | append-only persistence + tolerance | | |
| V2-SP07-13 | idle-only Compact | | |
| V2-SP07-14 | builders + acyclicity | | |
| V2-SP07-15 | no selection authority | | |
| V2-SP07-16 | dagtest zero skips | | |
| V2-SP07-17 | synth determinism | | |
| V2-SP07-18 | slice latency | | ms/op backward / forward: |
| V2-SP07-19 | import purity | | |
| V2-SP07-20 | coverage ≥ 85 % | | actual: |

## 8. Whole-tree gates
| ID | Gate | Verdict | Note |
|---|---|---|---|
| V2-ALL-01 | `go test -race ./...` | | |
| V2-ALL-02 | `go test -count=2 ./...` (windows) | | |
| V2-ALL-03 | `devtool ci-local` | | |
| V2-ALL-04 | CI green on verify/v2 (9 jobs) | | bench-gate & replay-gate required: |
| V2-ALL-05 | Qompack.md unmodified | | |

## 9. Exit-criteria re-verification
| Subplan | Criterion (quoted) | Measured value | Verdict |
|---|---|---|---|
| SP-01 | machinery for §11.3 guardrails exists and is wired to real producers | | |
| SP-02 | "a single number for stock behaviour, reproducible across at least 20 real sessions" | stock fraction_of_opt = __ over 24 synthetic sessions; reproducible: __ | |
| SP-02 | "Store growth sublinear in session length after dedup" | exponent = __ | |
| SP-02 | "No metric may regress by more than 2%…" | regressions: __ | |
| SP-02 | "Every phase gate runs the full replay suite" | phase 0 asserted: __ | |
| SP-03 | must not obstruct 4:1 / 15 ms | L0SketchUpdate = __ µs, __ allocs (= __ % of B-A) | |
| SP-03 | must not obstruct zero stale-block incidents | RebuildBloom + generational replacement: __ | |
| SP-04 | "measure with and without canonicalization" | testrunner gain __, overall gain __ | |
| SP-04 | "FastCDC over 100KB is well under 1ms" | __ µs/op | |
| SP-04 | boundary stability / determinism / size bounds | __ | |
| SP-04 | idempotence / inverse / non-growth | __ | |
| SP-05 | "hook p99 < 15ms" | B-A p99 = __ ms (linux) / __ (macos) / __ (windows) | |
| SP-05 | "< 2s (L4)" | B-E p99 = __ s **(nil PreCompact service at wave 1)** | |
| SP-05 | "degrade to async queue-and-drain rather than blocking" | transition observed at __ | |
| SP-05 | "Never fail silently" / graceful degradation | __ | |
| SP-06 | "ratio ≥ 4:1 on read-heavy sessions" | DedupRatio = __ (in-package) / __ (composed) | |
| SP-06 | "Store growth sublinear" | __ | |
| SP-06 | "encoded-once flag is the DPI guard" | __ | |
| SP-07 | "BFS over a few thousand nodes, sub-millisecond" | __ ms backward / __ ms forward | |
| SP-07 | "thin slicing … probably the right tradeoff" | size_ratio __, recall __ | |
| SP-07 | "a relevance score per node, not a binary keep/drop" | __ | |
| SP-07 | "segment_coupling(p) is the count of DAG edges crossing p" | __ µs | |
| SP-07 | "Do not ship slicing … before p-selection" | selector still inert: __ | |

## 10. New cross-component integration tests (permanent)
| Test | Seam | Verdict | Metric / note |
|---|---|---|---|
| TestIntegration_StorePutUsesRealChunkerAndCanon | chunk+canon+store | | |
| TestIntegration_CanonKeepRawRestoresThroughStore | canon+store | | |
| TestIntegration_StoreNearDupUsesRealMinHash | sketch+canon+store | | Jaccard: |
| TestIntegration_Phase1DedupRatioWithRealPipeline | chunk+canon+store | | ratio on/off canon: |
| TestIntegration_SecretsNeverSurviveTheRealPipeline | redact+canon+store | | |
| TestIntegration_HookEventThroughDaemonToStore | ipc+daemon+store+dag+sketch | | events/WAL/roots: |
| TestIntegration_TombstoneRoundTripThroughStore | observer.Tombstone+store+symbols | | |
| TestIntegration_SupersessionMarksEarlierReadThroughDAG | store+dag | | |
| TestIntegration_SpooledEventsSurviveToTheStore | ipc spool+daemon+store | | |
| TestIntegration_ContractMonitorRunsAgainstRealStore | contract+store | | |
| TestIntegration_DegradedPassiveStillWritesToTheRealStore | contract+daemon+store | | |
| TestIntegration_RealStoreGrowthIsSublinear | store+eval | | exponent: |
| TestIntegration_ReplayGateAcceptsRealGrowthFile | store+eval+test/replay | | |
| TestIntegration_RealBloomHealthFeedsTheFPCeiling | sketch+eval | | estFPRate: |
| TestIntegration_BeladyPMinLandsAtLowCoupling | eval+dag | | low-coupling in __% of events |
| TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything | dag+store | | |
| TestIntegration_SymbolNodesUseTheRealExtractor | symbols+dag+store | | |
| TestIntegration_HotPathWarmWithRealResidentState | everything | | B-A p99: |
| TestIntegration_HotPathDegradesRatherThanBlocks | daemon+store | | |
| TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites | daemon+store+dag+paths | | |
| TestIntegration_GCNeverCollectsALiveRootUnderIngest | store GC + ingest | | |

## 11. Performance budgets
| Budget | Threshold | linux | macos | windows | Verdict |
|---|---|---|---|---|---|
| B-A hook_controlled p99 | < 15 ms | | | | |
| B-B l0_ingest p99 | < 2 ms | | | | |
| B-C l0_process p99 (soft) | < 50 ms | | | | reported |
| B-D hook_wall p99 | reported | | | | reported |
| B-E checkpoint_finalize p99 | < 2 s | | | | (nil PreCompact) |
| B-F mcp_tool_call p95 | < 250 ms | N/A | N/A | N/A | N/A (SP-13) |
| Store dedup ratio (read-heavy) | ≥ 4:1 | | | | |
| Canon gain (testrunner / overall) | ≥ 1.25 / ≥ 1.0 | | | | |
| Store growth exponent | < 1.0 | | | | |
| L0SketchUpdate | ≤ 5 µs, 0 allocs | | | | |
| dag BackwardSlice 5 000 | < 1 ms | | | | |
| dag CrossingEdges | < 5 µs | | | | |
| store PutBytes 100 KB cold / warm | ≤ 3 ms / ≤ 400 µs | | | | |
| store Search 1 000 roots | ≤ 25 ms | | | | |
| store GC 50 k objects | ≤ 2 s | | | | |
| chunk Split 100 KB | < 800 µs | | | | |
| canon Run bash 100 KB | < 3 ms | | | | |
| symbols Extract 100 KB | < 2 ms | | | | |
| ipc ReadState | < 100 µs | | | | |
| eval E-2 / E-3 / E-4 / E-5 | 250/50/20/15 ms | | | | |
| benchstat vs baseline | no >25 % regression | | | | warnings: |

## 12. Regression
| Item | Verdict | Note |
|---|---|---|
| V1 inventory re-run in full | | source: plans/V1-VERIFY-foundation-and-contracts.md or §2.1 |
| Appendix C verbatim | | |
| Append-only guard | | |
| Hooks exit 0 (30 + 66) | | |
| ModeFull with four not-yet-implemented | | |
| Four closing-note guards | | |
| No network / confined write set | | |
| plugin & config docs no-drift | | |
| import graph + bindeps | | |
| §11.3 2 % rule: regressions found | | list, or "none" |
| §11.3 2 % rule: sign-offs used | | must be "none" at a checkpoint |
| §11.2 metric deltas vs phase0.json | | table or "all zero" |
| benchstat >10 % warnings | | explained: |

## 13. Failures, diagnoses and fixes
| # | Failing item | Class (a–e) | Diagnosis | Fix commit | Re-run pass # |
|---|---|---|---|---|---|
| 1 | | | | | |

## 14. Gate
- [ ] Every row above is PASS (or a documented N/A this file authorises)
- [ ] Every row in `plans/CARRIED-DEFECTS.tsv` owned by `V2-VERIFY` is `fixed` or `deferred:<checkpoint>`; `go test ./test/guards/ -run TestCarriedDefects` passes with `plans/V2-report.md` in place
- [ ] CI green on verify/v2: verify, test ×3, cover, crossbuild, bench-gate, replay-gate, plugin-validate, security, docs
- [ ] `testdata/bench-baseline.txt` updated with integrated wave-1 numbers
- [ ] No `Co-Authored-By` / `Signed-off-by` / `Generated with` / 🤖 anywhere in `develop..verify/v2`
- [ ] `verify/v2` merged into `develop` with `--no-ff`; `develop` tagged `v0.1.0`
- [ ] **Only now**: `feat/sp08-observer-l0` and `feat/sp09-negative-knowledge` cut from the post-verification `develop`
```

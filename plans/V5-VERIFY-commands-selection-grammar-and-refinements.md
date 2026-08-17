# V5 — Verification checkpoint: commands, selection, grammar, and Phase-7 refinements

> **Recommended model: Opus 5 · xhigh effort**
>
> The smallest checkpoint (1.2k lines), over subsystems that are frontends or refinements rather than new substrate.

**When this runs.** Immediately after every wave-4 branch has merged into `develop`, in the merge
order fixed by `00-ARCHITECTURE.md` §9 (`--no-ff`, conflicts resolved on the *incoming* branch and
re-merged, never with a hand-edited merge commit):

1. `feat/sp14-slash-commands-and-observability`
2. `feat/sp15-analyzer-selection-and-grammar`
3. `feat/sp16-phase7-refinements`

and **before any wave-5 branch (`feat/sp17-*`, `feat/sp18-*`) is cut**. 00-ARCHITECTURE §9 is
explicit: *"The next wave's branches are cut from the post-verification `develop`. No wave-N branch
is ever cut before verification V<N> is green."*

**Where the work happens.** Cut `verify/v5` from the merged `develop`:

```
git checkout develop
git pull
git log --oneline --merges -3          # must show the three wave-4 merges, in the order above
git checkout -b verify/v5
```

Every fix produced by this checkpoint lands on `verify/v5` as small conventional commits, and
`verify/v5` merges back into `develop` with `--no-ff` when the whole checkpoint is green
(§7 below). Tag `v0.4.0` on `develop` after the merge.

**What this checkpoint is.** Not a smoke test. This is an exhaustive re-verification of **every
functionality that exists in the codebase at this point** — all sixteen merged subplans, SP-01
through SP-16 — plus new integration tests for the seams that only exist now that wave 4 has
landed. Wave 5 (SP-17 packaging/hardening/release, SP-18 documentation/UAT) is **out of scope**:
do not test packaging, release artifacts, `goreleaser`, `qompack doctor`, `qompack fsck` beyond
what SP-01 shipped, the cross-platform release matrix, `docs/user-guide.md`, `docs/troubleshooting.md`,
or UAT scripts.

**Three known wave-4 collision points to inspect first**, because they are where a clean
three-way merge can still produce semantically wrong code:

| Site | SP-15 wants | SP-16 wants | Correct post-merge state |
|---|---|---|---|
| `internal/checkpoint/writer.go` `Finalize` | fold the grammar action history into the narrative | promote re-expanded hashes into `pointers.tools` | **both blocks present, grammar first, promotion second** (SP-16 Done checklist) |
| `internal/checkpoint/truncate.go` | action-history block must be tier-3, dropped first | Phase A/B/C tier reserves from the measured curve | both: `TestTruncateDropsActionHistoryFirst` **and** `TestTierReserveMatchesCurve` pass together |
| `internal/scheduler/evaluate.go` | untouched by SP-15 | `applySkiRental(&d, in)` before the return | SP-12's clause ordering intact, ski-rental applied last, `TestEvaluate_ReasonsOrderStable` still green |

Verify these three by reading the merged files **before** running anything else. A merge that
dropped one of the two `Finalize` blocks compiles and passes most tests.

---

## How to run this checkpoint

Fan the inventory out across **parallel subagents, one per subplan group**, then run the
integration tests (§4) in the main session.

| Subagent | Inventory sections | Packages under test |
|---|---|---|
| A | §2.1 SP-01, §2.2 SP-02 | `core paths config logging obs tokens hookio cli pluginmanifest testutil eval test/replay` |
| B | §2.3 SP-03, §2.4 SP-04 | `sketch chunk canon symbols test/dedup` |
| C | §2.5 SP-05, §2.6 SP-06 | `ipc daemon contract store redact tokens test/bench/hotpath` |
| D | §2.7 SP-07, §2.8 SP-08 | `dag observer` |
| E | §2.9 SP-09, §2.10 SP-10 | `negknow checkpoint pins` |
| F | §2.11 SP-11, §2.12 SP-12 | `rehydrate rules skills scheduler` + SP-12's `daemon` files |
| G | §2.13 SP-13, §2.14 SP-14 | `mcp commands` |
| H | §2.15 SP-15, §2.16 SP-16 | `analyzer grammar` + SP-16's `store/checkpoint/scheduler/daemon` files |

Rules for subagents:

- Each subagent works in its own git worktree off `verify/v5`
  (`git worktree add ../qompack-v5-<letter> verify/v5`) and **does not commit**. It reports
  pass/fail plus the measured number for every inventory row it owns, verbatim.
- No subagent may fix anything. Diagnosis and fixes happen in the main session under §7, so that
  two agents cannot fix the same file at once.
- Sections §4 (integration), §5 (performance budgets) and §6 (regression/replay gate) run in the
  **main session only** — they need one daemon, one bench machine and one uncontended CPU.
- Benchmarks are never run concurrently with anything else. Serialize §5 after §2–§4.

---

## 2. Cumulative functionality inventory

Every row is a functionality that exists on `develop` at this point. Every row names the exact
command and the exact expected result. Run every row.

Preamble (main session, once, before any subagent starts):

```
go build ./...                                   # exit 0
go vet ./...                                     # exit 0
go run ./tools/devtool ci-local                  # exit 0 — fmt-check, lint, vet, nomagic,
                                                 #   importgraph, testdeps, bindeps, sleepcheck,
                                                 #   stubskips, build, test
go run ./tools/devtool build-all                 # all six GOOS/GOARCH targets produced
git log develop --format=%B | Select-String -Pattern "co-authored-by|signed-off-by|generated with"
                                                 # must return nothing
```

---

### 2.1 SP-01 — foundation, toolchain, contracts

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-01.1 | Domain-separated hashing, `Hash` text/JSON forms, `DecisionID` minting, seven sentinel errors | `go test ./internal/core/ -run 'TestHashBytes_DomainSeparation\|TestHashBytes_KnownVector\|TestHash_StringShortParse_RoundTrip\|TestParseHash_Rejects\|TestHash_JSONRoundTrip\|TestNewDecisionID_Format\|TestSentinels_AreDistinct' -v` | 7 tests PASS; `ParseHash(h.String())==h`; `Short()` is 12 hex |
| I-01.2 | `.qompack/` resolution ladder, `Norm`/`Key`, long paths, layout creation, self-ignore | `go test ./internal/paths/ -run 'TestResolve_\|TestNorm_\|TestKeyFold\|TestEnsureLayout_CreatesAllDirsAndSelfIgnore\|TestLongPath_Over260' -v` | all PASS; 17 layout dirs exist; `.qompack/.gitignore` is exactly `*\n` |
| I-01.3 | **Append-only invariant** (§7.4, §13 inv. 2) | `go test ./internal/paths/ -run 'TestAppendOnlyGuard\|TestWriteAtomic_RefusesProtected\|TestCreateNew_SetsReadOnly\|TestReplaceBloom_KeepsOneBackup\|TestAppendJSONL_OneLinePerRecord' -v` | `TestAppendOnlyGuard` PASS: **all five** illegal writes fail; checkpoint files mode `0444` |
| I-01.4 | Atomic writes + MANIFEST append/read | `go test ./internal/paths/ -run 'TestWriteAtomic_ReplacesAndSyncs\|TestManifest_AppendAndRead' -v` | PASS; no `wa-*` residue in `.qompack/tmp/` |
| I-01.5 | **Appendix C reproduced verbatim** by `config.Defaults()` | `go test ./internal/config/ -run 'TestDefaults_MatchesAppendixCVerbatim\|TestDefaults_RuntimeNamespace\|TestJSONSchema_Golden' -v` | PASS; `Defaults()` minus `runtime` deep-equals `testdata/golden/config/appendix-c.jsonc` |
| I-01.6 | Five-layer config precedence, deep per-leaf merge, env mapping, JSONC, `null` semantics | `go test ./internal/config/ -run 'TestLoad_' -v` | all PASS; `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER=0.08` lands with `OriginEnv`; `measuredDeltaSeconds: null` is `nil`, not `0` |
| I-01.7 | Validation is fallback-not-crash; complete rule table; tiers partition; telemetry forced off | `go test ./internal/config/ -run 'TestValidate_' -v` and `go test ./internal/cli/ -run TestCLI_ConfigViolationsAreLoudAndPersisted -v` | PASS; invalid leaves fall back to defaults, `error == nil`; two `Loud` lines; `state/config-violations.json` written |
| I-01.8 | Config fuzz | `go test ./internal/config/ -run xxx -fuzz FuzzConfigLoad -fuzztime 60s` | no crashers; always a validated config |
| I-01.9 | Leveled logging + the `Loud` three-destination channel + rotation | `go test ./internal/logging/ -run 'TestLogger_\|TestLoud_ThreeDestinations' -v` | PASS; `LOUD.log` written; `LastLoud()` populated; observer closure fired once |
| I-01.10 | Log-bucket histograms, conservative percentiles, exact max, six budget IDs B-A..B-F | `go test ./internal/obs/ -run 'TestHistogram_\|TestBudgets_AllSixPresentAndConfigDriven\|TestCheckBudgets_' -v` | PASS; six budgets in order; B-A limit follows `runtime.hotPath.budgetMs`; B-D `Gated == false` |
| I-01.11 | Token estimation: classification, prose/code, image dimensions, PDF pages, calibration clamp | `go test ./internal/tokens/ -run 'TestClassify_\|TestEstimate_\|TestCalibrate_\|TestEstimateRoot_' -v` | PASS; image cap 1600; calibration clamped to `[0.6,1.6]` and persisted |
| I-01.12 | Hook wire format: all seven payloads, unknown-field preservation, never panics, limits | `go test ./internal/hookio/ -run 'TestReadEvent_\|TestWriteOutput_\|TestSessionStartOutput_Shape' -v` | PASS; `Empty()` emits exactly `{}\n`; unknown fields survive in `Extra` |
| I-01.13 | Hook payload fuzz | `go test ./internal/hookio/ -run xxx -fuzz FuzzReadEvent -fuzztime 60s` | no panics |
| I-01.14 | **Hooks always exit 0** (§13 inv. 6) — 30 fault-injection combinations | `go test ./internal/cli/ -run 'TestDispatch_HookAlwaysExitsZero\|TestDispatch_PanicRecovered\|TestDispatch_NonHookErrorExitsOne\|TestDispatch_UnknownCommandExitsTwo' -v` | PASS; exit 0 in all 30; non-hook error exit 1; unknown command exit 2 |
| I-01.15 | Plugin manifest generated from one typed source; six hooks; seven commands; `.mcp.json` | `go test ./internal/pluginmanifest/ -v` and `go run ./tools/devtool plugin-validate` and `git diff --exit-code -- plugin/` | tests PASS; task exit 0; diff clean; hook timeouts `5,5,15,20,5,10,20`; `PostToolUse` has `matcher:"*"` |
| I-01.16 | Config docs never drift | `go run ./tools/devtool gen-config-docs --check` | exit 0 |
| I-01.17 | **Every §5 interface has a live implementation now** — no stub residue anywhere | `go test ./test/guards/ -run TestAllStubsReturnNotImplemented -v` and `Select-String -Path internal\**\*.go -Pattern "ErrNotImplemented" -Exclude *_test.go` | the guard's probe table must show **zero** packages still reporting `ErrNotImplemented` except `core`'s declaration itself; the grep returns only `internal/core/errors.go` |
| I-01.18 | Every conformance suite is live (Rule W-1) | `Select-String -Path internal\**\*test\**.go -Pattern "t\.Skip"` | **returns nothing.** Any surviving `t.Skip` in `sketchtest canontest symbolstest storetest dagtest negknowtest checkpointtest rehydratetest schedulertest mcptest evaltest ipctest redacttest tokenstest analyzertest grammartest observertest commandstest` is a V5 failure |
| I-01.19 | Cross-wave contract fixtures present and reproduced by real implementations (Rule W-2) | `go test ./... -run 'Golden\|Contract' -v` | PASS; `testdata/golden/contracts/**` reproduced by the **real** implementations, not by fixtures |
| I-01.20 | Ship-order and safety guards | `go test ./test/guards/ -run 'TestGuard_' -v` | `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull`, `TestGuard_WriteSetConfinedToQompack`, `TestGuard_NoNetworkImports` all PASS |
| I-01.21 | Toolchain: `nomagic`, import-graph DAG, test-dep isolation, commit-msg checker | `go test ./tools/lint/nomagic/ ./tools/devtool/ -v` | `TestNoMagic_Analyzer`, `TestImportGraph_AcceptsRealRepo`, `TestImportGraph_RejectsViolation`, `TestTestDeps_RejectsProductionTestify`, `TestCheckCommitMsg` PASS |
| I-01.22 | Test fixtures: temp project, FakeClock, Windows-hostile files, golden helper | `go test ./internal/testutil/ -v` | PASS incl. `TestWindowsHostileFiles_AllCreatable`, `TestProject_AssertAppendOnly` |
| I-01.23 | e2e: all six hooks against the real binary | `go test ./test/e2e/ -run 'TestE2E_AllSixHooksExitZero\|TestE2E_ConfigPrintFromRealBinary' -v` | PASS; six exit-0s; hook log has six lines |
| I-01.24 | SP-01 benchmark budgets | `go test ./internal/obs/ ./internal/config/ ./internal/paths/ ./internal/cli/ -bench 'BenchmarkHistogram_Observe\|BenchmarkConfigLoad_ColdNoFiles\|BenchmarkHookNoop_InProcess\|BenchmarkPathsWriteAtomic_4KB' -benchtime 2s` | `Observe` < 100 ns/op; `ConfigLoad` < 2 ms/op; `HookNoop` < 3 ms/op; `WriteAtomic_4KB` < 2 ms/op |

### 2.2 SP-02 — replay harness, Belady OPT, divergence, Phase-0 baseline

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-02.1 | Block/demand extraction from a session | `go test ./internal/eval/ -run 'TestBlocks_\|TestDemands_\|TestApproachClass_Normalization' -v` | PASS; cumulative `Pos`; per-`paths.Key` file blocks; elimination demands matched by approach class |
| I-02.2 | **Belady OPT** — knapsack, exactness, budget, determinism, fallback | `go test ./internal/eval/ -run 'TestBelady_' -v` | all PASS; `TestBelady_UnitWeightsMatchesClassicBelady` exact; `KeepSet.Tokens <= budget` always; `TestBelady_PMinIsEarliestDropped` |
| I-02.3 | §5.6 breakpoint OPT, always labelled not-plugin-actionable | `go test ./internal/eval/ -run 'TestBreakpointOPT_' -v` | PASS; `Plan.Note == NotPluginActionable` on every path |
| I-02.4 | Policies: `stock` (§2.4 step 7 reproduction), `null`, `oracle` | `go test ./internal/eval/ -run 'TestStockPolicy_\|TestNullPolicy_Empty\|TestOraclePolicy_ScoresExactlyOne\|TestPolicyNames_Sorted' -v` | PASS; `oracle` scores exactly 1.0; `stock` keeps 5 files at 5K each, `P == 0` |
| I-02.5 | Counterfactual replay, horizon, determinism, live-mode refusal | `go test ./internal/eval/ -run 'TestReplay_' -v` | PASS; live mode refused without `QOMPACK_EVAL_LIVE`; two runs `go-cmp`-equal |
| I-02.6 | The five §4.2 divergence metrics | `go test ./internal/eval/ -run 'TestCompare_' -v` | PASS; identical runs give `{Horizon,1,0,true,1,0,0}`; edit distance symmetric |
| I-02.7 | Scoring: micro-averaged fraction-of-OPT, §5.2 rewrite arithmetic, percentiles, report | `go test ./internal/eval/ -run 'TestScoreRun_\|TestReport_\|TestMetricsOf_CoversEveryDirection' -v` | PASS; table A `rewrite_span_tokens == 17_000`, table B `== 157_000`; `TestScoreRun_NoHardcodedMultiplier` proves D11 at runtime |
| I-02.8 | **The 24-session synthetic corpus is byte-stable** | `go test ./internal/eval/ -run 'TestSynthesize_\|TestCorpus_' -v` | PASS; regeneration byte-identical; `TestSynthesize_EveryCompactionHasDemands` (no vacuous scoring); `CORPUS.json` hashes match |
| I-02.9 | Recorded-transcript importer + redaction | `go test ./internal/eval/ -run 'TestImport_\|TestRedact_' -v` and `go test ./internal/eval/ -run xxx -fuzz FuzzRedact -fuzztime 60s` | PASS; all 8 redact rules; idempotent; refuses in-repo destinations; no crashers |
| I-02.10 | Sublinear-growth guardrail computation | `go test ./internal/eval/ -run TestCheckSublinearGrowth_ -v` | PASS; committed fixture `Exponent ≈ 0.62 ± 0.02` |
| I-02.11 | **The replay gate** — 2% rule, sign-off trailer, phase assertions, corpus staleness, bloom FP ceiling | `go test ./test/replay/ -run 'TestGate_' -v` | all PASS incl. `TestGate_TwoPercentBoundaryExclusive` (−1.98% passes, −2.02% fails), `TestGate_SignOffAllowsNamedMetricOnly`, `TestGate_PhaseChecksMayNotBeDisabledInCI` |
| I-02.12 | Phase-0 baseline reproducible | `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline .\v5-phase0-a.json` then again to `.\v5-phase0-b.json`; compare | byte-identical; `policies.stock.fraction_of_opt` equals the committed `testdata/baseline/phase0.json`; `"corpusTier":"synthetic"` present |
| I-02.13 | Replay driver e2e | `go test ./test/replay/ -run TestReplayDriver_EndToEnd -v` | PASS; exit 0; report parses |
| I-02.14 | Eval benchmark budgets E-1..E-5 | `go test ./internal/eval/ -bench 'BenchmarkBeladyDetail_400Turns\|BenchmarkSynthesize_320Turns\|BenchmarkCompare_400Actions\|BenchmarkBreakpointOPT_256Candidates' -benchtime 2s` | ≤ 250 ms, ≤ 50 ms, ≤ 20 ms, ≤ 15 ms per op respectively |

### 2.3 SP-03 — sketch library

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-03.1 | Versioned CRC frame header, sorted params, all rejection paths | `go test ./internal/sketch/ -run 'TestHeader_\|TestHash128_' -v` | PASS incl. `TestHeader_DetectsSingleBitFlip`, `TestHeader_RejectLyingBodyLen` (zero allocations before rejection) |
| I-03.2 | **Bloom sized per Appendix A** | `go test ./internal/sketch/ -run 'TestBloom_AppendixASizing\|TestBloom_SizingTable' -v` | `mRaw == 95_851`, `mBits == 95_872`, `k == 7`, body 11 984 bytes |
| I-03.3 | Bloom correctness: no false negatives, measured FP rate, fill ratio, saturation | `go test ./internal/sketch/ -run 'TestBloom_NoFalseNegatives\|TestBloom_FillRatioAtCapacity\|TestBloom_EstimatedFPRateMatchesEmpirical\|TestBloom_SaturatedThreshold' -v` | PASS; empirical FP ∈ [0.008, 0.013] at capacity; `Saturated()` fires at est. FP ≥ 0.10 |
| I-03.4 | Resize + rebuild-from-iterator at a different capacity | `go test ./internal/sketch/ -run 'TestBloom_Resize\|TestBloom_RebuildFrom' -v` | PASS; grow 2× above 0.5 fill; capped at `MaxBloomCapacity` |
| I-03.5 | **Count-Min sized per Appendix A**, never underestimates, merge/scale/heavy-hitters | `go test ./internal/sketch/ -run 'TestCMS_' -v` | PASS; `Dims() == (2719, 5)`, body 54 380; `MergeFrom` additive; `ErrShapeMismatch` on mismatch and nil |
| I-03.6 | HyperLogLog sizing, error bounds, exact-union merge | `go test ./internal/sketch/ -run 'TestHLL_' -v` | PASS; 2048 registers, 2 102-byte frame, relative error ≤ 0.07 at every n |
| I-03.7 | Misra-Gries: no false positives, frequent-item guarantee, deterministic order | `go test ./internal/sketch/ -run 'TestMG_' -v` | PASS; 32 rebuilds byte-identical |
| I-03.8 | MinHash: shift invariance, "one new failure" near-dup, subsampling | `go test ./internal/sketch/ -run 'TestMinHash_\|TestSignature_\|TestSigSketch_' -v` | PASS; `TestMinHash_OneNewFailure` Jaccard ≥ 0.9; `TestMinHash_StableAcrossRuns` matches frozen constants |
| I-03.9 | Save/Load, the `tried.bloom` generational path, quarantine, the Loud contract | `go test ./internal/sketch/ -run 'TestSave_\|TestLoad_\|TestReplaceGenerational_\|TestQuarantine\|TestAppendOnly_TriedBloomNeverTruncated\|TestLoadWithLog_' -v` | PASS; `Save` returns `ErrGenerational` for `tried.bloom`; exactly one `.bak` generation; `LoadWithLog` emits exactly one `Loud` on corruption |
| I-03.10 | Sketch properties | `go test ./internal/sketch/ -run 'TestProp_' -rapid.checks=1000 -v` | all PASS |
| I-03.11 | Five fuzz targets | `go test ./internal/sketch/ -run xxx -fuzz FuzzBloomUnmarshalBinary -fuzztime 60s` (repeat for `FuzzCMSUnmarshalBinary`, `FuzzHLLUnmarshalBinary`, `FuzzMisraGriesUnmarshalBinary`, `FuzzSignatureUnmarshalBinary`) | no crashers; every failure is a package sentinel |
| I-03.12 | **Frozen on-disk format** (five goldens) | `go test ./internal/sketch/ -run 'TestGolden_OnDiskStability\|TestGolden_V1StillDecodes' -v` | PASS **without** `-update`; v1 bytes decode with the shipped decoder |
| I-03.13 | Sketch benchmark budgets, incl. the L0 contribution | `go test ./internal/sketch/ -bench 'BenchmarkL0SketchUpdate\|BenchmarkBloom\|BenchmarkCMS\|BenchmarkHLL\|BenchmarkMisraGries\|BenchmarkMinHash\|BenchmarkRebuildBloom5000' -benchtime 2s` and `go test ./internal/sketch/ -run TestL0SketchUpdate_ZeroAlloc -v` | `BenchmarkL0SketchUpdate` ≤ 5 µs/op with **0 allocations**; `RebuildBloom` 5 000 keys ≤ 15 ms |

### 2.4 SP-04 — FastCDC, canonicalizers, symbols

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-04.1 | FastCDC params validation and normalization | `go test ./internal/chunk/ -run 'TestParams\|TestNewClampsInvalidParams' -v` | PASS; exact error strings |
| I-04.2 | **Cross-platform deterministic chunking** | `go test ./internal/chunk/ -run 'TestGearTableGolden\|TestSplit_GoldenBoundaries\|TestSplit_Determinism' -v` | PASS on ubuntu, macos and windows against the *same* goldens. A per-platform golden is a V5 failure |
| I-04.3 | Size bounds, contiguity, coverage, mean chunk size | `go test ./internal/chunk/ -run 'TestSplit_' -v` | PASS; `1024 ≤ Len ≤ 16384` for all but the last; mean ∈ [3200, 5200] |
| I-04.4 | **Boundary stability under insertion/deletion** (§6.1) | `go test ./internal/chunk/ -run 'TestPropBoundaryStability_' -v` | PASS; ≤ 12 novel chunks per trial (hard), ≤ 2 in ≥ 85% and ≤ 3 in ≥ 95% of trials |
| I-04.5 | Merkle `RootHash` domain separation and order sensitivity | `go test ./internal/chunk/ -run 'TestRootHash_\|TestChunkHashMatchesCoreHashBytes\|TestRefs_RoundTrip' -v` | PASS |
| I-04.6 | Streaming chunker equals in-memory chunker | `go test ./internal/chunk/ -run TestSplitStream_ -v` | PASS across reader sizes 1, 7, 4096, full |
| I-04.7 | Chunk fuzz | `go test ./internal/chunk/ -run xxx -fuzz FuzzSplit -fuzztime 120s` | no crashers; invariants hold on every input |
| I-04.8 | Canonicalizer registry: deterministic order, 14 names, overlap resolution, non-growth guard | `go test ./internal/canon/ -run 'TestRegistry_\|TestOverlapResolution_\|TestNonGrowingGuard_\|TestApplied_\|TestReduced_Value\|TestMatcherClassAssigned' -v` | PASS; `For("Bash","")` returns the exact 10-name order |
| I-04.9 | All seven generic + seven per-tool canonicalizers | `go test ./internal/canon/ -run 'TestCRLF_\|TestANSI_\|TestTimestamps_\|TestDurations_\|TestPIDs_\|TestAddresses_\|TestTmpPaths_\|TestBash_\|TestTestRunner_\|TestGrep_\|TestGlob_\|TestFileRead_\|TestWebFetch_\|TestGit_' -v` | all table rows PASS |
| I-04.10 | **Idempotence, non-growth, exact inverse** (§5.6 normative) | `go test ./internal/canon/ -run 'TestPropIdempotence_EveryCanonicalizer\|TestPropNonGrowing_EveryCanonicalizer\|TestPropRestoreIsExactInverse\|TestRestore_' -rapid.checks=1000 -v` | PASS; `Restore(Canonicalize(x).Canonical, deltas) == x` |
| I-04.11 | Near-dup delta-vs-full decision | `go test ./internal/canon/ -run 'TestDecide_' -v` | PASS incl. the golden `dedup-decisions.json` |
| I-04.12 | Golden tool-output corpus reproduced | `go test ./internal/canon/ -run TestGoldenCorpus_AllFiles -v` | PASS **without** `-update` over all 24 corpus files |
| I-04.13 | Canon fuzz | `go test ./internal/canon/ -run xxx -fuzz FuzzCanonicalizeRun -fuzztime 120s`; then `-fuzz FuzzRestore -fuzztime 120s` | no crashers |
| I-04.14 | Symbol extraction across 10+ dialects, `Enclosing`, `References` | `go test ./internal/symbols/ -run 'TestExtract_\|TestEnclosing_\|TestReferences_\|TestPropSpansWellFormed' -v` | PASS; smallest enclosing span wins; caps at 20 000 symbols / 4 MiB |
| I-04.15 | Symbols fuzz | `go test ./internal/symbols/ -run xxx -fuzz FuzzExtract -fuzztime 120s` | no crashers |
| I-04.16 | **With/without canonicalization dedup measurement** (O2 justification) | `go test ./test/dedup/ -v` | `TestDedupRatio_WithVsWithout` PASS: `testrunner` group `gain ≥ 1.25`, overall `gain ≥ 1.0`; `TestDedupReport_Written` reproduces `testdata/canon-dedup-report.json` byte-for-byte |
| I-04.17 | SP-04 benchmark budgets | `go test ./internal/chunk/ ./internal/canon/ ./internal/symbols/ -bench . -benchtime 2s` | `Split_100KB` < 800 µs; `GearScan_1MiB` ≥ 400 MB/s; `Split_1MiB` ≥ 120 MB/s; `SplitStream_4MiB` ≤ 40 ms; `RootHash_1000Chunks` < 40 µs; `Run_Bash100KB` < 3 ms; `Run_GoTest` < 1 ms; `Restore_100KB` < 1 ms; `Extract_100KB` < 2 ms; `Enclosing_100KB` < 2 ms; `References_100KB_50Names` < 1 ms |

### 2.5 SP-05 — daemon, IPC, hot path, contract monitor

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-05.1 | Address resolution: pipe naming, XDG/TempDir ladder, `sun_path` guard | `go test ./internal/ipc/ -run 'TestProjectHash12\|TestResolveUnix\|TestResolveWindowsPipeName' -v` | PASS; >100-byte paths fall back to `qp-<hash8>.sock`; `ErrAddrTooLong` when impossible |
| I-05.2 | NDJSON framing byte-exactness, resynchronization, fuzz | `go test ./internal/ipc/ -run 'TestEncodeRequestByteExact\|TestDecodeRequestRoundTrip\|TestLineReaderRejectsOversize' -v`; `go test ./internal/ipc/ -run xxx -fuzz FuzzDecodeRequest -fuzztime 60s` | PASS; golden byte-equal; no crashers |
| I-05.3 | 32-byte state record: round-trip, CRC fallback, atomic concurrent access | `go test ./internal/ipc/ -run 'TestState' -race -v` | PASS; torn records impossible; bad CRC silently falls back to defaults |
| I-05.4 | **Client `Send` never propagates an error** | `go test ./internal/ipc/ -run 'TestSend' -race -v` | all PASS incl. `TestSendNeverReturnsError` (200 random requests × 4 hostile server shapes), `TestSendDaemonDownSpoolsAndReturnsNilError`, `TestSendOversizeExternalizes` |
| I-05.5 | Spool is append-only and drop-safe | `go test ./internal/ipc/ -run 'TestSpool' -v` | PASS; `O_TRUNC` rejected; write failure increments `l0.dropped` and Louds once per session |
| I-05.6 | Server: routing, ACK/NAK, panic containment, multiplexing, concurrency, permissions | `go test ./internal/ipc/ -run 'TestServer\|TestUnixSocketPermissions\|TestStaleUnixSocketReclaimed' -race -v` | PASS; 64×50 concurrent requests all ACKed; socket `0600`, dir `0700` |
| I-05.7 | Daemon singleton lock, stale reclamation, heartbeat | `go test ./internal/daemon/ -run 'TestAcquireLock\|TestStaleLockReclaimed\|TestLiveLockNotReclaimed\|TestHeartbeat' -v` | PASS; a live listener always wins over an old heartbeat |
| I-05.8 | Session registry: eviction, hot-mode reset per session | `go test ./internal/daemon/ -run 'TestRegistry' -v` | PASS |
| I-05.9 | WAL-backed ingest, ACK-before-processing, ring spill | `go test ./internal/daemon/ -run 'TestIngest' -v` | PASS; ACK observed while the worker is still blocked; ring-full spills to spool, `Accept` never blocks |
| I-05.10 | Drain: idempotent, resumable, blob-resolving, corruption-tolerant, dedup vs. client spool | `go test ./internal/daemon/ -run 'TestDrain\|TestNAKDuplicateIsDedupedOnDrain' -v` | PASS; 10 lines drained once, not twice; NAK duplicate seen exactly once |
| I-05.11 | Idle controller: priority order, budget, panic isolation, idle detection | `go test ./internal/daemon/ -run 'TestIdle' -v` | PASS; `detectAfterSeconds=120` boundary exact |
| I-05.12 | **Hot-path breach detector → `sync`/`spool` submode** (§8.1 fallback) | `go test ./internal/daemon/ -run 'TestBreachDetector\|TestHotModeTransitionWritesStateAndNAKs\|TestSpoolOnBreachFalse' -v` | PASS; transition on exactly 3 consecutive breach windows; revert on 3 clean; observable in `state.bin`, NAK frame, WARN log |
| I-05.13 | Late-bound `Services` tolerate nil; extension seams | `go test ./internal/daemon/ -run 'TestServicesAllNil\|TestHandle\|TestBind\|TestSketchSetNeverWritesTriedBloom\|TestConfigReloadDefersChunkChange' -v` | PASS; all 13 ops answered with a fully nil `Services`; `tried.bloom` untouched by the daemon |
| I-05.14 | Three-mode state machine enforcement | `go test ./internal/daemon/ -run 'TestDegradedPassive\|TestModeOffSkipsIngest' -v` | PASS; degraded-passive **records** but never **acts**; `act.`-prefixed idle tasks suppressed |
| I-05.15 | Daemon lifecycle: idle exit, lock-held no-op | `go test ./internal/daemon/ -run 'TestIdleExitWithZeroSessions\|TestRunReturnsNilWhenLockHeld' -v` | PASS; lock and `state.bin` removed on exit |
| I-05.16 | **Contract monitor** — nine assertions, five/four producer split, degrade/restore | `go test ./internal/contract/ -v` | all PASS incl. `TestFreshBuildReportsModeFull`, `TestDeclaredProducerSetMatchesArchitecture`, `TestCriticalFailureDegrades`, `TestTwoCleanRunsRestore`, `TestPanickingAssertionDoesNotDegrade`. **At V5, `mcp.server_registered`, `precompact.has_time_to_write`, `precompact.custom_instructions_accepted` and `hook.additional_context_delivered` all have real producers (SP-10, SP-11, SP-13) — they must report a real observation, not `not-yet-implemented`** |
| I-05.17 | Budget table matches the architecture | `go test ./internal/obs/ -run 'TestBudgetsMatchArchitectureTable\|TestBudgetALimitFollowsConfig\|TestCheckBudgetsNeverGatesBD' -v` | PASS |
| I-05.18 | **66 hook fault-injection combinations still exit 0** | `go test ./test/e2e/ -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit' -v` | PASS; 6 subcommands × 11 fault sites = 66 exit-0s; only `self-test` may exit non-zero |
| I-05.19 | Daemon e2e: round-trip, lazy spawn, idle exit, self-test, spool submode | `go test ./test/e2e/ -run 'TestE2EHookRoundTrip\|TestE2ELazySpawn\|TestE2EIdleExit\|TestE2ESelfTest\|TestE2ESpoolSubmodeEndToEnd' -v` | PASS |
| I-05.20 | IPC/daemon micro-benchmarks | `go test ./internal/ipc/ ./internal/daemon/ -bench 'BenchmarkReadState\|BenchmarkServerRoundTrip\|BenchmarkIngestAccept\|BenchmarkEncodeRequest' -benchtime 2s` | `ReadState` < 100 µs; `ServerRoundTrip` p99 < 2 ms; `IngestAccept` p99 < 2 ms (**B-B**); `EncodeRequest` < 5 µs |

### 2.6 SP-06 — content-addressed store, redaction, exact tokens

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-06.1 | **Redaction: ten rules, idempotent, bounded growth, user patterns** | `go test ./internal/redact/ -v` | all PASS incl. `TestRedact_AnthropicBeforeGeneric`, `TestRedact_DotenvGatedByKeyName`, `TestRedact_RejectsZeroWidthUserPattern` |
| I-06.2 | Redaction fuzz | `go test ./internal/redact/ -run xxx -fuzz FuzzRedactIdempotent -fuzztime 60s` | no crashers; `Redact(Redact(x)) == Redact(x)` |
| I-06.3 | Exact chunk-level token accounting (G10.2) | `go test ./internal/tokens/ -run 'TestUnits_\|TestEstimateImage\|TestEstimatePDF\|TestEstimateRoot_\|TestChunkCache_\|TestCalibrate_' -v` | PASS; PDF is page-derived, never the flat 2 000; `EstimateRoot` cache-key is `core.Hash` alone |
| I-06.4 | Object layout, compression modes, global dedup, `Novel`/`Reused` accounting | `go test ./internal/store/ -run 'TestPutBytes_FanoutLayout\|TestPutBytes_CompressionNone\|TestPutBytes_GlobalDedup\|TestPutBytes_IdenticalRootIsFree\|TestPutBytes_DedupHitReportsThisPutsRawBytes' -v` | PASS; two-level fanout; `RawBytes` never double-counted |
| I-06.5 | **Redaction happens before canonicalization and chunking** (§13 inv. 7) | `go test ./internal/store/ -run 'TestPutBytes_RedactionBeforeChunking\|TestPutBytes_RedactionRunsWhenDepsRedactIsNil' -v` and `go test ./test/e2e/ -run TestE2E_SecretNeverLandsInObjects -v` | PASS; **no object under `objects/` contains any of the ten secret literals** |
| I-06.6 | Canonicalize-first, delta roots, ephemeral flag, truncation, near-dup | `go test ./internal/store/ -run 'TestPutBytes_Canonicalize\|TestPutBytes_KeepRaw\|TestPutBytes_Ephemeral\|TestPut_ReaderTruncation\|TestPutBytes_NearDup' -v` | PASS |
| I-06.7 | Read paths: chunk get, quarantine on corruption, full open, span open | `go test ./internal/store/ -run 'TestGetChunk_\|TestOpen_StreamsFullRoot\|TestOpenSpan_Boundaries\|TestHas_NoIO' -v` | PASS; corrupt object quarantined + one `Loud`, `core.ErrNotFound` returned |
| I-06.8 | Index durability: reopen, torn tail, closed-store errors, concurrency | `go test ./internal/store/ -run 'TestOpenStore_\|TestClosedStoreErrors\|TestConcurrentPut' -race -v` | PASS |
| I-06.9 | tool_use index: idempotent replay, conflict rejection, supersession append-only | `go test ./internal/store/ -run 'TestRecordToolUse_\|TestToolUsesByPath_\|TestMarkSuperseded_\|TestArgsDigest_' -v` | PASS; supersession is a new `"op":"supersede"` line, never an edit |
| I-06.10 | File version history and **`ChangedSince`** (the staleness primitive) | `go test ./internal/store/ -run 'TestAppendFileVersion_\|TestFileAt\|TestChangedSince_\|TestFilesJSON_MaterializedByFlush' -v` | PASS; `paths.Key` normalization; input order preserved |
| I-06.11 | **Segment log + the encoded-once DPI guard** (§4.6, §8.2) | `go test ./internal/store/ -run 'TestSegment_' -v` | all PASS; **`TestSegment_MarkEncodedRefusesDifferentSeq` returns `core.ErrAlreadyEncoded`**; batch is all-or-nothing; frontier is contiguous |
| I-06.12 | Search backing `recall` | `go test ./internal/store/ -run 'TestSearch_' -v` | PASS; deterministic ordering across 20 runs; default `K == 5` |
| I-06.13 | **Stats and the Phase-1 dedup ratio** | `go test ./internal/store/ -run 'TestStats_\|TestPhase1ExitCriterion_ReadHeavy' -v` | `TestPhase1ExitCriterion_ReadHeavy`: `Stats.DedupRatio >= 4.0`; `TestStats_SublinearGrowth` PASS |
| I-06.14 | GC: mark-and-sweep from real roots, retention "whichever is longer", deadline resume, dry run | `go test ./internal/store/ -run 'TestGC_' -v` | all PASS; zero `GCPolicy` deletes nothing; ephemeral roots collected by age; tombstoned roots stay in `roots.jsonl` |
| I-06.15 | Flush/session index; append-only guard over every store file | `go test ./internal/store/ -run 'TestFlush_\|TestAppendOnlyGuard_StoreFiles' -v` | PASS |
| I-06.16 | Store properties | `go test ./internal/store/ -run 'Prop' -rapid.checks=1000 -v` | all six properties PASS |
| I-06.17 | Store e2e durability | `go test ./test/e2e/ -run TestE2E_StoreSurvivesProcessRestart -v` | PASS |
| I-06.18 | Store benchmark budgets | `go test ./internal/store/ ./internal/tokens/ -bench . -benchtime 2s` | `PutBytes_100KB_Cold` ≤ 3 ms; `_Warm` ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan_4KB_of_4MB` ≤ 150 µs; `OpenStore_50kRoots` ≤ 400 ms; `Search_1000Roots` ≤ 25 ms; `GC_50kObjects` ≤ 2 s; `MarkEncoded_100` ≤ 1 ms; `EstimateRoot_64Cached` ≤ 5 µs |

### 2.7 SP-07 — dependence DAG and slicing

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-07.1 | Nine node kinds, eight edge kinds, weight multipliers, node-ID scheme | `go test ./internal/dag/ -run 'TestNodeKind\|TestEdgeKind\|TestNodeID\|TestParseNodeID' -v` | PASS; multipliers exactly `1.00,1.00,1.00,0.95,0.88,0.60,0.50,0.30` |
| I-07.2 | Graph mutation: validation, upsert merge, anchor earliest-`Pos`, dedup, dangling, tombstones | `go test ./internal/dag/ -run 'TestAddNode\|TestAddEdge\|TestTombstoneHidesNode\|TestOutInCopies\|TestAnchorNodePosIsEarliest\|TestClosedGraphRejects' -v` | PASS |
| I-07.3 | Concurrency safety | `go test ./internal/dag/ -run TestConcurrentMutationAndRead -race -v` | PASS; no race, no deadlock across 8 writers × 8 readers |
| I-07.4 | **`CrossingEdges(pos)` = `segment_coupling(p)`** | `go test ./internal/dag/ -run 'TestCrossingEdges\|PropCrossingEdgesMatchesBruteForce\|TestNodesAfter\|PropNodesAfterMatchesFilter' -rapid.checks=1000 -v` | PASS; `lo < pos <= hi` rule exact at both ends; matches brute force on every generated graph |
| I-07.5 | Backward/forward slicing with **scores, not keep/drop** | `go test ./internal/dag/ -run 'TestBackwardSlice\|TestForwardSlice\|TestThinDropsControlOnly\|TestSliceMax\|TestSliceMinScoreFloor\|TestSliceOrderTieBreak\|TestSliceGolden\|TestDefaultSliceOptionsFromConfig\|TestSliceDeadlineTruncates' -v` | PASS; max-path scoring (never additive); `DefaultSliceOptions(Defaults()).Thin == true` |
| I-07.6 | Slice properties | `go test ./internal/dag/ -run 'PropThinSliceIsSubsetOfFull\|PropScoresBoundedAndMonotone' -rapid.checks=1000 -v` | PASS |
| I-07.7 | **No selection authority** (closing note 3, structural) | `go test ./internal/dag/ -run TestNoBooleanKeepAPI -v` | PASS; no exported API returns a keep-set/drop-list/`map[NodeID]bool`; `doc.go` still contains `NO SELECTION AUTHORITY` |
| I-07.8 | Append-only NDJSON log, torn tail, corrupt line, auto-flush, compaction | `go test ./internal/dag/ -run 'TestFlush\|TestOpen\|TestAutoFlushAt2000\|TestCompact' -v` and `go test ./internal/dag/ -run PropLogRoundTrip -rapid.checks=500 -v` | PASS; torn tail loads with `TruncatedTail == true`; corrupt line Louds exactly once |
| I-07.9 | §8.1 item-4 edge builders, acyclicity | `go test ./internal/dag/ -run 'TestBuild\|TestBuilderOutputIsAcyclic' -v` | PASS; `TestBuilderOutputIsAcyclic` finds no cycle over 200 built tool uses |
| I-07.10 | Thin-vs-full measured tradeoff | `go test ./internal/dag/ -run TestThinVsFullComparison -v` | PASS **without** `-update`; mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`, `ns_thin ≤ ns_full` |
| I-07.11 | Slicing latency budgets (§6.4 "sub-millisecond") | `go test ./internal/dag/ -run 'TestSliceLatencyBudget\|TestCrossingLatencyBudget' -v` and `go test ./internal/dag/ -bench . -benchtime 2s` | `BackwardSlice5000` and `ForwardSlice5000` median < **1 ms**; `CrossingEdges` median < **5 µs**; `BuildToolUse` < 3 µs |

### 2.8 SP-08 — observer L0

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-08.1 | **Addressable tombstones** (G3.2, §8.1 item 2) | `go test ./internal/observer/ -run 'TestTombstone\|TestNormalizeToolName\|TestIsCompactable\|TestSupersedableClass' -v` | PASS; `TestTombstone_DesignExample` renders `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]`; golden byte-equal |
| I-08.2 | **Task-boundary signals** (G1.5) | `go test ./internal/observer/ -run 'TestExtractSignals_\|TestExtractTestOutcome_\|TestPathsFromInput_\|TestResponseText_' -v` and `-fuzz FuzzExtractSignals -fuzztime 60s` | PASS; todo completion, go/jest/pytest/cargo pass-fail, git commit (incl. `&&` chains); no panics |
| I-08.3 | `PostToolUse` pipeline: store, index, canon options, file versions, args digest, MCP-ephemeral, soft failures | `go test ./internal/observer/ -run 'TestOnToolUse_' -race -v` | all PASS; a `PutBytes` failure returns `hookio.Empty(), nil` and increments `observer.err.put` |
| I-08.4 | **Supersession detection** (§8.1 item 3) | `go test ./internal/observer/ -run 'TestSupersede_\|TestIsSuperset_\|PropertyIsSupersetReflexive' -v` | all PASS; per-path, per-class; never marks a later record; ephemeral in neither direction; lookback capped at 32 |
| I-08.5 | DAG edge emission from real events | `go test ./internal/observer/ -run 'TestGraph_\|TestNodeIDFormats' -v` | PASS; `Pos` monotone and pre-increment; symbols capped at 64 |
| I-08.6 | Sketch feeding — and **never the bloom filter** | `go test ./internal/observer/ -run 'TestSketches_\|TestObserverNeverFeedsBloom\|TestObserverSourceHasNoBloomReference' -v` | PASS; a panicking `Bloom.Add` double is never called; zero `Bloom` identifiers in non-test observer source |
| I-08.7 | **Verbatim, immutable user capture** (G2.3) | `go test ./internal/observer/ -run 'TestOnUserPrompt_\|TestVerbatimPromptID' -v` | PASS; `Canon.Strip == nil`; identical text re-submitted produces the same root and no new objects |
| I-08.8 | Thrash warning delivery gate | `go test ./internal/observer/ -run 'TestOnUserPrompt_Thrash' -v` | PASS in `full` mode; **silent in `ModePassive`** (§12) |
| I-08.9 | **Subagent capture** (G10.1) | `go test ./internal/observer/ -run 'TestOnStop_\|TestTailAssistantText_' -v` | PASS; summary + tool-result hashes stored; `TestOnStop_RetrievalPathG10_1` round-trips through a real store |
| I-08.10 | BOCD feature extraction from the event stream | `go test ./internal/observer/ -run 'TestFeatures_' -rapid.checks=500 -v` | PASS; all fields finite and in range; recent ring bounded at 16 |
| I-08.11 | SessionStart branching and SessionEnd ordering | `go test ./internal/observer/ -run 'TestOnSessionStart_\|TestOnSessionEnd_\|TestState_' -v` | PASS; SessionEnd call order exactly `Close → Graph.Flush → Store.Flush → sketch.Save×2 → state write → GC`; `tried.bloom` never created |
| I-08.12 | Passive mode still records | `go test ./internal/observer/ -run TestModePassiveStillWrites -v` | PASS; identical write counts, only `AdditionalContext` differs |
| I-08.13 | Observer e2e through the real daemon | `go test ./test/e2e/ -run 'TestE2E_ObserverThroughDaemon\|TestE2E_HooksExitZeroUnderFaultInjection\|TestE2E_SupersessionVisibleAfterRestart\|TestE2E_VerbatimPromptSurvivesRestart' -v` | PASS; 44 index lines; `sketches/tried.bloom` absent |
| I-08.14 | **Phase-1 exit criterion end to end** | `go test ./test/e2e/ -run 'TestPhase1_' -v` | `TestPhase1_DedupRatioReadHeavy` `DedupRatio >= 4.0`; `TestPhase1_CanonicalizationGapOnTestOutput` `ratioOn >= ratioOff*1.25`; `TestPhase1_StoreGrowthSublinear` PASS |
| I-08.15 | Observer B-C budgets | `go test ./internal/observer/ -bench 'BenchmarkOnToolUse_\|BenchmarkTombstone' -benchtime 2s` | `OnToolUse_FileRead64KB` p99 < 50 ms; `OnToolUse_TestOutput256KB` p99 < 50 ms; `Tombstone` < 2 µs |

### 2.9 SP-09 — negative knowledge

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-09.1 | Approach-class canonicalization | `go test ./internal/negknow/ -run 'TestApproachClass_\|TestLemma' -rapid.checks=1000 -v` | PASS; deterministic, bounded, `"unclassified"` for empties |
| I-09.2 | **The four-field canonical descriptor** (§8.3) | `go test ./internal/negknow/ -run 'TestSplitTarget_\|TestDescriptorKey_Golden\|TestMatchKey_\|TestKey_\|TestReasonHash_\|TestDescriptorJSON_' -v` | PASS; golden `key_hex`/`match_key_hex` byte-stable across platforms |
| I-09.3 | **`Record` is the §8.5 `eliminated[]` schema slot** (G6.1) | `go test ./internal/negknow/ -run 'TestRecordJSON_\|TestNormalizeRecord_\|TestRecordID_Stable\|TestSourceKind_' -rapid.checks=500 -v` | PASS; `record.golden.json` byte-equal; all seven §8.5 keys present |
| I-09.4 | Append-only ledger log with recovery | `go test ./internal/negknow/ -run 'TestReplayLog_\|TestAppendOnly_Enforced\|TestAppendLine_NoInteriorNewline' -v` | PASS; corrupt/orphan/duplicate lines counted, never fatal |
| I-09.5 | **Three-way `already_tried` answer** and scope semantics | `go test ./internal/negknow/ -run 'TestQuery_' -v` | PASS; `AnswerAbsent`/`AnswerActive`/`AnswerStale`; `StaleNote` byte-identical to §8.3 **including the em dash**; session scope hidden across sessions |
| I-09.6 | Recording: evidence requirement, idempotence, re-record after stale | `go test ./internal/negknow/ -run 'TestRecord_\|TestGetActiveAll\|TestTopActive\|TestAnswerMCPResult\|TestBlindMode\|TestClose' -race -v` | PASS; `TestRecord_IdempotentAcrossTimestamps` dedups by identity; `TestRecord_ReRecordAfterStaleIsNotDeduped` creates a new active record |
| I-09.7 | **Staleness flip from `store.ChangedSince`** (§12 High risk) | `go test ./internal/negknow/ -run 'TestRefreshStaleness_\|TestMarkStale_\|TestRebuildOnStale_\|TestMaintenanceTask_Shape\|TestOpenRefreshBounded' -v` | PASS; exactly one `ChangedSince` call with a deduplicated sorted dep slice; `Open` bounded < 500 ms |
| I-09.8 | **Bloom rebuilt from active records only** (§13 inv. 3) | `go test ./internal/negknow/ -run 'TestRebuildBloom_\|TestBloomLoadFailure_\|TestBloomUndercount_\|TestBloomOvercount_\|TestBloomFileSize\|TestHealth' -v` | PASS; **`TestRebuildBloom_NeverFromCheckpoint`**: exactly one `sketch.RebuildBloom` call site, and `internal/negknow` never imports `internal/checkpoint`; `TestBloomLoadFailure_NoRecords_NeverFalsePositive` |
| I-09.9 | Four ingestion sources (MCP, slash command, heuristic, user statement) | `go test ./internal/negknow/ -run 'TestIngest' -v` | PASS; auto-dep ordering; unknown deps warn and are skipped |
| I-09.10 | Heuristic detector (test-fail → revert → different approach) | `go test ./internal/negknow/ -run 'TestDetector_' -v` | PASS; window, same-class suppression, DAG-derived deps, `Scan` never appends |
| I-09.11 | Ledger conformance and node IDs | `go test ./internal/negknow/ -run 'TestLedgerConformance\|TestNodeIDGolden\|TestOpenReturns' -v` | PASS; zero `t.Skip` in `negknowtest` |
| I-09.12 | Elimination lifecycle e2e | `go test ./test/e2e/ -run 'TestE2E_EliminationLifecycle\|TestE2E_BloomCorruptionRecovery' -v` | PASS; active → durable → stale after dependency change → rebuilt bloom → still stale; exactly one `.bak` |
| I-09.13 | **Phase-2 exit criterion** | `go test ./test/replay/ -run TestPhase2ExitCriterion -v` | PASS; `stockRepeats > 0`, `negknowRepeats <= 0.75 × stockRepeats`, improvement on ≥ 8 individual sessions, **`staleBlocks == 0`** on every dependency-change session |
| I-09.14 | negknow budgets | `go test ./internal/negknow/ -run 'TestBudget_' -v` and `go test ./internal/negknow/ -bench . -benchtime 2s` | every `TestBudget_*` PASS |

### 2.10 SP-10 — checkpointer L4 and pins

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-10.1 | Append-only pins with tombstone deletion and a materialized view | `go test ./internal/pins/ -v` | all `TestPins*` PASS; `Remove` writes `{"op":"del"}`, never rewrites |
| I-10.2 | **The §8.5 schema verbatim, in importance order** | `go test ./internal/checkpoint/ -run 'TestSchemaFieldOrderIsImportanceOrder\|TestGoldenCheckpointRoundTrip\|TestEmptySlicesSerializeAsArrays\|TestBlockedOnNullIsExplicit\|TestHashMarshalsAsSha256Prefix\|TestEliminatedCarriesEverySection85Key' -v` | PASS; top-level key order exactly as §8.5; `eliminated[]` carries all seven keys |
| I-10.3 | Versioning and migration | `go test ./internal/checkpoint/ -run 'TestMigrate\|TestUnmarshalDropsUnknownFields' -v` | PASS; v1 identity; future version → `core.ErrContract` |
| I-10.4 | **No code snippets in checkpoints** (G3.4, §13 inv. 5) | `go test ./internal/checkpoint/ -run TestGoldenCheckpointsContainNoCodeBlocks -v` | PASS over every file in `testdata/golden/checkpoints/`, **including the ones SP-15 and SP-16 changed** |
| I-10.5 | **Store-only regeneration** (GC rule, §13 inv. 1) | `go test ./internal/checkpoint/ -run 'TestSourceSetCarriesNoText\|TestAdvanceStripsInjectionsFromStoredPrompts\|TestStripInjections' -rapid.checks=1000 -v` | PASS; every `SourceSet` field is an interface; injected text never re-encoded |
| I-10.6 | Incremental draft: begin, resume, advance, abort, derivations | `go test ./internal/checkpoint/ -run 'TestBegin\|TestAdvance\|TestAbort\|TestSetCurrentWork\|TestPackageFunctionsWorkWithoutObservers' -v` | all PASS; pointers newest-first; superseded/ephemeral excluded; drafts < 4 KB for a 40 KB read |
| I-10.7 | **The DPI guard at L4** | `go test ./internal/checkpoint/ -run TestAdvanceIsDPIGuarded -v` | PASS; `errors.Is(err, core.ErrAlreadyEncoded)`; draft still persisted |
| I-10.8 | **`ExtractDecisions` — the only producer of `DecisionID`** | `go test ./internal/checkpoint/ -run 'TestExtract' -v` | PASS; explains-edges, eliminations and decision pins all mint decisions; capped at 64; ranked by slice score; emits `KindDecision` nodes |
| I-10.9 | **Importance-ordered truncation** (§6.9) | `go test ./internal/checkpoint/ -run 'TestTruncate' -rapid.checks=300 -v` | all PASS; tier 3 → tier 2 → **never tier 1**; monotone in budget; one `DropEntry` per removed element |
| I-10.10 | **Pointer ground-truth validation** (G2.5) | `go test ./internal/checkpoint/ -run 'TestValidatePointers\|TestGitIndex\|TestGitDirAsFileWorktree' -v` and `-fuzz FuzzParseGitIndex -fuzztime 60s` | PASS; missing/dir/escape/dirty/untracked drops; git index v2 and v3 parsed, v4 degrades cleanly; no crashers |
| I-10.11 | MANIFEST, immutability, reader chain, verify, parent fallback | `go test ./internal/checkpoint/ -run 'TestManifestLineFormat\|TestFinalize\|TestGetDetectsManifestMismatch\|TestLatest\|TestChain\|TestVerify\|TestList\|TestReaderRef' -v` | PASS; finalized files are `0444`; a mismatched hash Louds and falls back to the parent |
| I-10.12 | **Focus instructions with the O1 incremental span** | `go test ./internal/checkpoint/ -run 'TestFocus' -v` | PASS; standing template byte-identical to §8.5; span paragraph names the checkpoint path and turn N; forward slashes on Windows; capped at 4 000 bytes |
| I-10.13 | Import discipline (cannot read a transcript) | `go test ./internal/checkpoint/ -run TestNoForbiddenImports -v` | PASS; `hookio`, `scheduler`, `ipc`, `daemon`, `os/exec`, `net` all absent |
| I-10.14 | `PreCompact` behaviour incl. near-deadline finalize | `go test ./internal/checkpoint/ -run 'TestPreCompact' -v` | PASS; cold path still writes a checkpoint; near-deadline returns within 600 ms with a valid truncated artifact |
| I-10.15 | **Scheduler-cadence checkpoints, gated by degradation** | `go test ./internal/checkpoint/ -run 'TestCadence' -v` | PASS; finalizes at budget and at 8 segments; **no checkpoint in `ModeDegradedPassive`**, while `advance_frontier` still runs |
| I-10.16 | Checkpoint e2e through the real hook | `go test ./test/e2e/ -run 'TestE2E_Checkpoint' -v` | PASS; `qompack checkpoint` exits 0; `customInstructions` present in `full`, absent in `degraded-passive`; `0001.json` read-only and manifest-consistent |
| I-10.17 | Checkpoint benchmark budgets | `go test ./internal/checkpoint/ -bench . -benchtime 2s` | `Finalize` mean < 50 ms; `AdvanceSegment` < 25 ms; `Truncate` < 5 ms; `ExtractDecisions` < 20 ms; `StripInjections` < 2 ms |
| I-10.18 | Frontier advancement shrinks the residual span (SP-10's own gate) | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=false --json .\v5-frontier-off.json` then the same with `=true --json .\v5-frontier-on.json` | `on.Score.ResidualSpan.P50 <= 0.70 × off.Score.ResidualSpan.P50`, and no divergence metric regresses > 2% |

### 2.11 SP-11 — rehydrator L5, rules, skills

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-11.1 | Glob engine and `paths:` frontmatter parsing | `go test ./internal/rules/ -run 'TestMatch_\|PropMatch_NeverPanics\|TestParseFront_' -rapid.checks=1000 -v` | PASS; `**` semantics; CRLF frontmatter; unclosed frontmatter is not a rule |
| I-11.2 | **Path-scoped rule restoration** (G4.1) | `go test ./internal/rules/ -run 'TestPathScoped_' -v` | PASS; matches only the pointer set; skips unscoped/empty/oversize; deterministic across 10 runs |
| I-11.3 | **Nested `CLAUDE.md` restoration** (G4.2) | `go test ./internal/rules/ -run 'TestNestedClaudeMD_' -v` | PASS; containing directory only, never ancestors, never the project root; unicode, spaces and >260-char paths |
| I-11.4 | **Skill index** (G4.4) | `go test ./internal/skills/ -run 'TestIndex_\|TestBodyTokens' -v` | PASS; budget from `runtime.rehydrate.skillIndexTokens`, never the literal 450; prefix truncation, not cheapest-first |
| I-11.5 | **The eight §8.6 items in normative order** | `go test ./internal/rehydrate/ -run 'TestRenderOrder_\|TestBuild_ItemOrderInPayload\|TestBuild_RankIsOneBasedAmongEmitted' -v` | PASS; `## 1.` … `## 8.` at strictly increasing offsets with `## 6a.`/`## 6b.` between 6 and 7 |
| I-11.6 | Injection tagging and sentinel | `go test ./internal/rehydrate/ -run 'TestBuild_InjectionTagging\|TestUnwrap_\|TestSentinel_\|TestBuild_SentinelIsLastLineInsideTags' -v` | PASS; `<!-- qompack:injected seq=7 ver=1 -->` wrapper round-trips |
| I-11.7 | Item 1 invariants never truncated | `go test ./internal/rehydrate/ -run 'TestInvariants_' -v` | PASS even at a forced 200-token budget (`Degraded == true`, `Truncated == false`) |
| I-11.8 | **Item 2: verbatim intent from L0, never from a summary** (G2.3, G7.3) | `go test ./internal/rehydrate/ -run 'TestUserIntent_' -v` | all PASS; `TestUserIntent_FromL0NotCheckpoint`, `TestUserIntent_EarliestTurnOfThisSessionWins`, `TestUserIntent_FencedPromptSurvivesVerbatim` |
| I-11.9 | Item 3 eliminations digest + standing instruction | `go test ./internal/rehydrate/ -run 'TestEliminations_' -v` | PASS; top-N by slice score; both scopes read; stale note verbatim; `"Before committing to an approach, call already_tried."` always present |
| I-11.10 | Items 4/5/6 decisions, current work, **pointers not contents** | `go test ./internal/rehydrate/ -run 'TestDecisions_\|TestCurrentWork_\|TestPointers_' -v` | PASS; no code fence anywhere in items 4–6; multiline pointer units dropped with a `Loud` |
| I-11.11 | Items 6a/6b restored instructions | `go test ./internal/rehydrate/ -run 'TestRestored_\|TestSkillIndex_' -v` | PASS; whole-rule-or-nothing; host head-truncation and 25K-cap warnings emitted |
| I-11.12 | **Item 7 drop report** (G4.5) | `go test ./internal/rehydrate/ -run 'TestDropReport_' -v` | PASS; fixed kind order; `… and N more; call dropped()`; `Result.Dropped` complete regardless of rendering |
| I-11.13 | **8–12K budget discipline** (G3.3) | `go test ./internal/rehydrate/ -run 'TestClampBudget_\|TestBuild_NeverExceedsMaxTokens\|TestBuild_SmallerThanStock\|TestBuild_MinFillReadmitsUnits\|TestBuild_PrefixTruncationNotCheapestFirst\|TestBuild_CarryForward\|PropBuild_' -rapid.checks=500 -v` | PASS; `Result.Tokens <= 12 000` always; strictly less than the stock 50K+25K; monotone and prefix-wise embedded in budget |
| I-11.14 | Drop-report persistence (backs the MCP `dropped` tool) | `go test ./internal/rehydrate/ -run 'TestReporter_' -v` | PASS; corrupt state deleted + one `Loud`; golden state file byte-equal |
| I-11.15 | Rehydration goldens and degradation | `go test ./internal/rehydrate/ -run 'TestBuild_Golden_\|TestBuild_NonCompactSourceEmitsNothing\|TestBuild_ContextCancelled\|TestBuild_NilDeps' -v` | PASS **without** `-update` |
| I-11.16 | **G7.5: the checkpoint is the fallback when the summarizer fails** | `go test ./internal/rehydrate/ -run TestBuild_NoTranscriptRead_ClosesG75 -v` and `go test ./test/e2e/ -run TestE2E_SessionStartCompactAfterFailedSummary -v` | PASS; **zero** reads of `TranscriptPath`; identical payload with and without a `<summary>` block |
| I-11.17 | Daemon `SessionStart(compact|clear)` service | `go test ./internal/daemon/ -run 'TestService_' -v` | PASS; nothing emitted in `ModeDegradedPassive`; panics recovered |
| I-11.18 | Rehydration e2e | `go test ./test/e2e/ -run 'TestE2E_SessionStart' -v` | PASS; `additionalContext` contains `## 1. Invariants`, `## 7. No longer in context`, `## 8. Retrieval` and the session sentinel; ≤ 12 000 tokens; `clear` resets state |
| I-11.19 | L5 benchmark budgets | `go test ./internal/rehydrate/ ./internal/rules/ ./internal/skills/ -bench . -benchtime 2s` | `L5-BUILD` p99 < 250 ms; `L5-RULES` < 50 ms; `L5-SKILLS` < 20 ms; `TestE2E_SessionStartLatency` p99 < 1.5 s |

### 2.12 SP-12 — scheduler L3

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-12.1 | Threshold arithmetic: effective window, soft floor, hard ceiling | `go test ./internal/scheduler/ -run 'TestEffectiveWindow_\|TestSoftFloor_\|TestHardCeiling_\|TestThresholds_\|TestSoftFloorBelowHardCeiling_Property' -rapid.checks=1000 -v` | PASS; soft floor `99_000`, hard ceiling `147_000` at a 180 000 window; soft < hard always |
| I-12.2 | **Sliding-TTL model keyed on the last API call** (E1) | `go test ./internal/scheduler/ -run 'TestClassifyTTL_\|TestCacheFactor_\|TestSlidingTTLUsesAPICallNotCacheWrite' -rapid.checks=500 -v` | PASS; warm/expiring/cold boundaries exact at 150 s and 300 s; a fresh cache write with a stale API call is **cold** |
| I-12.3 | Young–Daly cadence with δ measured at runtime | `go test ./internal/scheduler/ -run 'TestYoungDaly_\|TestMTBF_\|TestResolveDelta_' -v` | PASS; `YoungDaly(20,1800) == 268.3281572999748`; `null` δ means *measure*, never zero, and never fires the clause |
| I-12.4 | Ski-rental threshold computed, never literal | `go test ./internal/scheduler/ -run TestSkiRentalShouldWrite -v` | PASS; threshold 12.5 at `r=0.1,w=1.25`; the literal appears only in `_test.go` |
| I-12.5 | **BOCD changepoint detection** (§6.6) | `go test ./internal/scheduler/ -run 'TestBOCD_' -rapid.checks=500 -v` | all PASS; step change detected within 5 observations; posterior normalized and ≤ 512 entries; marshal round-trip; corruption rejected before allocation |
| I-12.6 | **The composite trigger** (§8.4) | `go test ./internal/scheduler/ -run 'TestEvaluate_' -v` | all PASS; reason order exactly `["soft_floor","changepoint","young_daly","hard_ceiling","idle_cold_cache"]`; `TestEvaluate_ScoreArithmeticExact`; `TestEvaluate_MultipliersReadFromConfig`; purity and idempotence |
| I-12.7 | **p-selection with the cache term** (§5.3, G5.2) | `go test ./internal/scheduler/ -run 'TestEligible_\|TestPrepareCandidates_\|TestScoreCandidates_\|TestChooseP_' -v` | PASS; latest boundary when warm, deepest when cold, candidates ∩ round boundaries, cap at 32 keeping the highest `Pos` |
| I-12.8 | **The p-selection ship-order gate** | `go test ./internal/scheduler/ -run 'TestPSelectionAvailable_DefaultFalse\|TestEnableDisablePSelection\|TestPSelectionGate_ConcurrentAccess' -race -v` | PASS; false by default, true after a real `Runtime` is constructed |
| I-12.9 | §8.7 eviction ordering as pure spec | `go test ./internal/scheduler/ -run 'TestDropClassOf_\|TestEvictionRank_Order' -v` | PASS; ephemeral > superseded > ordinary > none |
| I-12.10 | Runtime: window ladder, context tokens, EWMAs, persistence, self-healing | `go test ./internal/daemon/ -run 'TestNewSchedulerRuntime_\|TestRuntime_' -race -v` | all PASS; state discarded on session/hazard/feature-list mismatch; corrupt state self-heals with two `Loud`s |
| I-12.11 | Candidate assembly with cached `CrossingEdges` | `go test ./internal/daemon/ -run 'TestAssemble_' -v` | PASS; 3 `CrossingEdges` calls on the second assembly, 6 after a graph change; panics recovered |
| I-12.12 | Reclaimable-token suffix index | `go test ./internal/daemon/ -run 'TestReclaimableIndex_\|TestClassifyDrop_' -rapid.checks=500 -v` | PASS; monotone non-increasing in `p` |
| I-12.13 | Feature extraction from L0 signals | `go test ./internal/daemon/ -run 'TestFeaturesFrom_' -v` | PASS; total-variation tool shift; bounded windows at 10 000 observations |
| I-12.14 | The `Services` tap (no SP-05/SP-08 edits) | `go test ./internal/daemon/ -run 'TestWrapServices_' -v` | PASS; inner seams run first and their output is returned unchanged; nil runtime is a no-op; prompt seam does zero store I/O |
| I-12.15 | **O5 frontier advancement + DPI guard at the scheduler** | `go test ./internal/daemon/ -run 'TestFrontier_' -v` | all PASS; only closed+unencoded segments advanced; `ErrAlreadyEncoded` drops the batch, Louds, and never re-encodes; residual never negative |
| I-12.16 | O3 idle tasks, gated by decision and by degradation | `go test ./internal/daemon/ -run 'TestIdleTask\|TestIdleActingTaskSkippedInDegradedPassive\|TestIdleWorkBindsDaemon\|TestIdleTasksRegistered' -v` | PASS; six tasks in priority order; `act.advance_frontier` suppressed in degraded-passive |
| I-12.17 | Scheduler state codec | `go test ./internal/daemon/ -run 'TestStateCodec_' -v` | PASS; atomic writes (never append-only); turn lists capped |
| I-12.18 | **The scheduler is not on the hot path** | `go test ./internal/scheduler/ ./internal/daemon/ -run TestSchedulerNotOnHotPath -v` | PASS; no hook subcommand call path reaches `Evaluate` |
| I-12.19 | L3 replay policy | `go test ./test/replay/l3policy/ -v` | PASS; deterministic; does not import `internal/daemon` |
| I-12.20 | **Phase-4 exit criterion** | `go test ./test/replay/ -run 'TestPhase4_' -v` | all PASS: rewrite tokens ≤ 0.80× stock; no divergence metric regressed > 2%; residual-span slope ≤ 0.02 tokens/turn and longest-quartile ≤ 1.25× shortest; `ResidualSpan.P95 ≤ 20 000`; pause modelled, never presented as measured |
| I-12.21 | Scheduler benchmark budgets | `go test ./internal/scheduler/ ./internal/daemon/ -bench . -benchtime 2s` | `Evaluate_64Candidates` ≤ 50 µs and ≤ 8 allocs; `BOCDObserve_4Features` ≤ 150 µs full / ≤ 20 µs steady; `BOCDMarshal` ≤ 2 ms; `FeaturesFrom` ≤ 100 µs; `AssembleCandidates_2000ToolUses` ≤ 20 ms cold / ≤ 200 µs warm; `RuntimeEvaluate` ≤ 25 ms; `ReclaimableIndexBuild_5000Blocks` ≤ 3 ms; `SchedulerTap_ObserveTool` ≤ 1.5 ms |

### 2.13 SP-13 — MCP retrieval layer

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-13.1 | JSON-RPC 2.0 stdio server: initialize, notifications, ping, error codes, resynchronization | `go test ./internal/mcp/ -run 'TestInitialize\|TestNotification\|TestPing\|TestUnknownMethod\|TestMalformedJSON\|TestWrongJSONRPCVersion\|TestOversizedLine\|TestServeReturns' -v` | PASS; `initialize.json` golden byte-equal; `-32700/-32600/-32601` as specified |
| I-13.2 | Stdout purity, concurrency, panic isolation | `go test ./internal/mcp/ -run 'TestNoStdoutPollution\|TestConcurrentCallsProduceWellFormedLines\|TestHandlerPanicIsolated' -race -v` | PASS; a panicking handler yields `isError:true` and the server keeps serving |
| I-13.3 | Server fuzz | `go test ./internal/mcp/ -run xxx -fuzz FuzzServeLine -fuzztime 60s` | no crashers |
| I-13.4 | In-repo JSON-schema validator over all eight tools | `go test ./internal/mcp/ -run 'TestSchema\|TestApplyDefaults\|TestAllEightSchemasCompile\|PropertySchemaAcceptsGeneratedValidDocs' -rapid.checks=1000 -v` | PASS; schemas byte-equal their goldens |
| I-13.5 | **Exactly the eight §8.7 tools, in the design order** | `go test ./internal/mcp/ -run 'TestToolsList\|TestMCPConformance\|TestProxyAndDirectToolListsAreIdentical\|TestEveryToolRejectsUnknownArgument\|TestUnknownToolNameIsToolError' -v` | PASS; `["recall","expand","re_read","already_tried","record_eliminated","timeline","why","dropped"]`; `tools-list.json` byte-equal; zero `t.Skip` in `mcptest` |
| I-13.6 | **Minimum-sufficient-span resolver** (§8.7) | `go test ./internal/mcp/ -run 'TestMinimalSpan\|TestSingleChunk\|TestFull\|TestExplicit\|TestSymbol\|TestLineAnchor\|TestNoWidenerIsTolerated\|PropertyNextSpanPaging\|PropertySpanNeverExceedsMaxResponse' -rapid.checks=1000 -v` | PASS; chunk-aligned; ≤ 16384 before widening; paging reconstructs objects exactly |
| I-13.7 | `recall` | `go test ./internal/mcp/ -run 'TestRecall' -v` | PASS; default `k=5`; selector prefixes parsed; empty result is not an error |
| I-13.8 | `expand` | `go test ./internal/mcp/ -run 'TestExpand' -v` | PASS; by hash, by tool_use_id, by chunk hash; exactly-one-of enforcement; `full=true` escape hatch |
| I-13.9 | `re_read` with historical `at` | `go test ./internal/mcp/ -run 'TestReRead' -v` | PASS; worktree → store fallback; `at` accepts timestamp, root hash and `turn:N`; path escapes rejected |
| I-13.10 | **`already_tried` — three-way, never a false positive** (G6.2) | `go test ./internal/mcp/ -run 'TestAlreadyTried' -v` | PASS; `absent`/`active`/`stale`; note verbatim; `BloomOnly` and ledger errors both report `absent` |
| I-13.11 | `record_eliminated` | `go test ./internal/mcp/ -run 'TestRecordEliminated' -v` | PASS; evidence stored; `depends_on` resolved; scope defaults from config; **not** ephemeral |
| I-13.12 | `timeline`, `why`, `dropped` | `go test ./internal/mcp/ -run 'TestTimeline\|TestWhy\|TestDropped' -v` | PASS; `why` searches the parent chain; `dropped` returns the real drop report |
| I-13.13 | **Ephemeral-at-birth** (§8.7, §12 re-inflation row) | `go test ./internal/mcp/ -run 'TestEveryRetrievalResponseCarriesEphemeralMeta\|TestEphemeral' -v` | PASS; seven ephemeral tools carry `_meta.qompack.ephemeral`; each writes a `ToolUseRecord{Ephemeral:true}`; disabled cleanly by config |
| I-13.14 | Expansion promotion counting | `go test ./internal/mcp/ -run 'TestNoteExpansion\|TestPromoter\|TestPromoted\|TestRecallDoesNotCountAsExpansion\|PropertyPromoterCountsMonotone' -rapid.checks=500 -v` | PASS; fires at exactly `retrieval.promoteAfterExpansions`; survives restart; corrupt state quarantined |
| I-13.15 | Daemon wiring and the `mcp.server_registered` observable | `go test ./internal/mcp/ ./internal/daemon/ -run 'TestWriteInitializedObservable\|TestInstallMCPOp\|TestMCPInitializedSeam\|TestInitializedWrites\|TestDaemonMCPOp' -v` | PASS; `contract.DeclareProducers` declares `CMCPRegistered`; `state/mcp.json` written |
| I-13.16 | `qompack mcp` transcoder | `go test ./internal/mcp/ -run 'TestCmdMCP' -race -v` | PASS; never spools; retry cancellable; daemon-unavailable is a tool error, not a crash |
| I-13.17 | MCP e2e over real stdio | `go test ./test/e2e/ -run 'TestStdioServerEndToEnd\|TestStandingInstructionsAgree' -v` | PASS; **`mcp.StandingInstruction == rehydrate.StandingInstruction()`** — at V5 this must no longer be skipped |
| I-13.18 | Docs and manifest agreement | `go run ./tools/devtool gen-mcp-docs` then `git diff --exit-code -- docs/mcp-tools.md`; `go test ./internal/mcp/ -run TestPluginValidateSeesEightTools -v` | no diff; 8 tools |
| I-13.19 | **Budget B-F** | `go test ./internal/mcp/ -run TestBudgetBF -v` | p95 < 250 ms over 200 dispatches against the 2 000-tool-use / 40 MB fixture; `CheckBudgets` reports no B-F breach |

### 2.14 SP-14 — slash commands and the observability surface

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-14.1 | **Seven commands in §7.5 order**, one per manifest spec | `go test ./internal/commands/ -run 'TestCommandNames_MatchesSection75\|TestSubcommandFor\|TestAll_OneCommandPerSpec\|TestDispatch_' -v` | PASS; `["status","recall","pin","checkpoint","why","dropped","eval"]`; `checkpoint` and `checkpoint-now` both dispatch |
| I-14.2 | Shared frontend contract: `--help`, `-h`, `--json` envelope, exit codes, nil-`Deps` tolerance | `go test ./internal/commands/ -run 'TestHelp_\|TestJSONFlag_EveryCommand\|TestJSONErrorEnvelope_EveryCommand\|TestExitCode\|TestNilDeps_' -v` | PASS; `-h` byte-identical to `--help` and exits 0; failing runs still emit a valid `Envelope{ok:false}`; **no panic on any nil `Deps` member** |
| I-14.3 | Determinism and purity of output | `go test ./internal/commands/ -run 'TestNoDirectStdio\|TestNoTimeNowInPackage\|TestNoANSI\|TestDeterminism_RenderStatus\|TestStatus_MapsAreSorted\|TestStatus_UnavailableIsSortedBySectionOrder\|TestProperty_' -rapid.checks=500 -v` | PASS; 100 identical renders; zero `os.Stdout`/`fmt.Print*`/`time.Now` in non-test source |
| I-14.4 | **`/qompack:status` — the eleven sections** (G8.1 surface) | `go test ./internal/commands/ -run 'TestStatus_' -v` | all PASS; `status_full.txt`/`.json` byte-equal; `dedup ratio  4.41:1`; `meets_phase1_ratio == true` |
| I-14.5 | Degradation is loud on the status surface (§13 inv. 10) | `go test ./internal/commands/ -run 'TestStatus_Degraded' -v` | PASS; first line is `!!!!  QOMPACK DEGRADED — degraded-passive  !!!!` naming assertion, expected and observed |
| I-14.6 | Status resilience: nil deps, section errors, panics, disk fallback | `go test ./internal/commands/ -run 'TestStatus_NilDepsUnavailable\|TestStatus_StoreStatsError\|TestStatus_CollectRecoversSectionPanic\|TestStatus_LatencyFromDiskFile\|TestStatus_LatencyMissingMetricsFile\|TestStatus_Fetch' -v` | PASS; exit 0 in every case; disk metrics read-only; **nothing is ever appended to the spool by `status`** |
| I-14.7 | Bloom saturation and latency-budget reporting (§11.4, §11.3) | `go test ./internal/commands/ -run 'TestStatus_BloomSaturationWarning\|TestStatus_LatencyBudgets' -v` | PASS; warning cites §11.4; B-A over budget renders `FAIL`; B-D renders `(reported, not gated)` with `pass == true` |
| I-14.8 | Daemon-side status op | `go test ./internal/commands/ -run 'TestStatusOpHandler_' -v` | PASS; `Schema == 1`; panics contained |
| I-14.9 | **`recall`/`why`/`dropped` have no second implementation** | `go test ./internal/commands/ -run 'TestRecall_\|TestWhy_\|TestDropped_' -v` | all PASS; **`TestRecall_NoSecondImplementation`**: zero references to `store.Store`, `negknow.Ledger`, `checkpoint.Reader` in those three files |
| I-14.10 | **`/qompack:pin` and `pin --eliminated`** (§8.3 source 2) | `go test ./internal/commands/ -run 'TestPin_' -v` | all PASS; deterministic pin id; `Materialize` after every mutation; elimination carries `SourceSlashCommand`, a canonical descriptor, a synthesized-or-explicit evidence hash and resolved `depends_on` |
| I-14.11 | **`/qompack:checkpoint` (`checkpoint-now`)** | `go test ./internal/commands/ -run 'TestCheckpointNow_' -v` | all PASS; `Begin → Advance → Finalize`; open/encoded segments filtered; `ErrAlreadyEncoded` aborts and names the DPI guard; session-resolution ladder exact |
| I-14.12 | **`/qompack:eval`** (G8.3 surface) | `go test ./internal/commands/ -run 'TestEval_' -v` | all PASS; **`Belady` computed 72×, once per session per compaction point, not per policy**; `Deterministic` forced even with `QOMPACK_EVAL_LIVE=1`; aggregation read from `Report`, never re-averaged; latency trio rendered |
| I-14.13 | Manifest/binary/docs cannot drift | `go test ./internal/commands/ ./internal/pluginmanifest/ -run 'TestCommandMarkdown_\|TestCommandFiles_\|TestCommandDocs_' -v`; `go run ./tools/devtool plugin-validate`; `go run ./tools/devtool gen-command-docs` then `git diff --exit-code -- plugin/ docs/commands.md` | PASS; task exit 0; no diff |
| I-14.14 | Command conformance suite | `go test ./internal/commands/commandstest/ ./internal/commands/ -run TestCommandSuite -v` | PASS; zero `t.Skip` |
| I-14.15 | Commands e2e against the real binary and a real daemon | `go test ./test/e2e/ -run 'TestE2E_EverySubcommandResolves\|TestE2E_StatusAgainstRealDaemon\|TestE2E_StatusWithDaemonStopped\|TestE2E_CheckpointNowThenStatus' -v` | PASS; `from_daemon == true` with a daemon and `false` without; `store.tool_uses == 50`; checkpoint seq agrees between the two commands |
| I-14.16 | Command budgets | `go test ./internal/commands/ -bench . -benchtime 2s` | `BenchmarkCollect` p95 < 250 ms; `BenchmarkRenderStatus` < 5 ms; `BenchmarkMCPFrontend` p95 < 250 ms (**B-F**); `BenchmarkCheckpointNow` < 2 s (**B-E**) |

### 2.15 SP-15 — analyzer selection and grammar

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-15.1 | **Sequitur, online and linear-time** (§6.3) | `go test ./internal/grammar/ -run 'TestSequitur' -race -v` | all PASS incl. `TestSequiturOverlapExceptionAAA`, `TestSequiturRuleUtilityInlines`, `TestSequiturUsesIsOccurrenceNotRefCount`, `TestSequiturConcurrentAppendAndRules` |
| I-15.2 | **Sequitur's two invariants, property-tested** | `go test ./internal/grammar/ -run Prop -rapid.checks=1000 -v` | `PropDigramUniqueness`, `PropRuleUtility`, `PropOccurrenceCountMatchesExpansion`, `PropExpansionRoundTrip`, `PropAcyclic`, `PropCompressionIsNotExpansion`, `PropDeterminism` all PASS |
| I-15.3 | Versioned CRC grammar codec + frozen golden | `go test ./internal/grammar/ -run 'TestCodec\|TestSaveLoadRoundTrip\|TestLoadMissingFileIsNotFound' -v` | PASS **without** regenerating; `actions_v1.seq` byte-equal; header `QPKG`, v1, Castagnoli CRC; every corruption class rejected with the receiver unchanged |
| I-15.4 | Grammar fuzz | `go test ./internal/grammar/ -run xxx -fuzz FuzzGrammarUnmarshal -fuzztime 120s` | no crashers; every survivor satisfies both invariants |
| I-15.5 | **Thrash detection and warning formatting** | `go test ./internal/grammar/ -run 'TestWarningsFor\|TestFormatWarning\|TestPromptAddendum' -v` | PASS; deterministic `ID ASC` tiebreak across 20 rebuilds; `thrash_warning.txt` byte-equal |
| I-15.6 | Grammar size budget | `go test ./internal/grammar/ -run TestCodecSizeBudget -v` | 50 000 appends serialize to < 512 KiB |
| I-15.7 | **Δ-scoring, cheap retrospective proxy** (§8.3) | `go test ./internal/analyzer/ -run 'TestCheapScorer\|TestStopwordListSize' -v` | all PASS; deterministic across 100 calls; nil store and missing objects tolerated |
| I-15.8 | **G6.3 made testable** | `go test ./internal/analyzer/ -run TestCheapScorerRanksNegativeKnowledgeHighest -v` | PASS; an elimination rationale outranks a re-readable file body |
| I-15.9 | **Redundancy / supersession detection** | `go test ./internal/analyzer/ -run 'TestDetectRedundancy\|TestSortedNearDupKeys\|TestExcludeFromSummary\|TestApplyToSetsSuperseded\|TestToolUseIDOf' -v` | all PASS; per-path rule; near-dup threshold read from config; deterministic ordering across 5 runs |
| I-15.10 | **Suffix-constrained selector: nothing before `p` is constructible** (§13 inv. 4) | `go test ./internal/analyzer/ -run 'TestNewSelectorRejectsPreP\|TestNewSelectorAcceptsPosEqualP\|TestNewSelectorRejectsNegativeLambda\|TestSelectorP' -v` | PASS; `ErrBlockBeforeP` names block id, pos and `p` |
| I-15.11 | Lazy-greedy submodular selection | `go test ./internal/analyzer/ -run 'TestSelect' -v` | all PASS; budget respected; superseded and ephemeral penalized; diminishing returns observable; lazy == naive keep-set with strictly fewer evaluations on ≥ 190/200 instances; golden small case reproduced |
| I-15.12 | **The `(1 − 1/e)` guarantee, brute-forced** | `go test ./internal/analyzer/ -run Prop -rapid.checks=1000 -v` | `PropGuaranteeUnitCost` and `PropGuaranteeKnapsack` PASS at 1 000 checks each; `PropMonotone`, `PropSubmodular`, `PropBudgetNeverExceeded`, `PropNothingBeforeP`, `PropCoverageMinusLambdaRedundancy` PASS |
| I-15.13 | **The ship-order guard is real and inert without p-selection** | `go test ./internal/analyzer/ -run 'TestNewSelectorInertWithoutPSelection\|TestNewSelectorLiveWithPSelection\|TestPSelectionProbeDefaultsToScheduler\|TestSetPSelectionProbeRestores' -v` and `go test ./test/e2e/ -run 'TestPSelectionProbeIsTestOnly\|TestNoSelectorBypass' -v` | PASS; zero non-test references to `SetPSelectionProbe`; **exactly one** `Pos <` occurrence in `internal/analyzer` non-test source |
| I-15.14 | **Grammar folded into the checkpoint at compaction time** | `go test ./internal/checkpoint/ -run 'TestBuildActionHistory\|TestRenderActionHistory\|TestFold\|TestFinalizeIncludesActionHistory\|TestTruncateDropsActionHistoryFirst' -v` | all PASS; narrative **appended**, never rewritten; `sketch_refs.grammar` set; **`"version": 1` unchanged**; no code fences (§13 inv. 5) |
| I-15.15 | Thrash warning reaches `additionalContext` | `go test ./test/e2e/ -run 'TestThrashWarning' -v` | PASS; `"[qompack] thrash:"` and `"repeated 11×"` present; inner context preserved; hook still exits 0 |
| I-15.16 | **Phase-5 exit criterion** | `go test ./test/replay/ -run TestPhase5 -v` | PASS; `FractionOfOPT(analyzer-suffix-submodular) > FractionOfOPT(baseline)` on all 24 sessions at an identical 12 000-token budget, delta ≥ 0.02 on the read-heavy and refactor subsets; `testdata/replay-baseline/phase5.json` matches to 4 dp |
| I-15.17 | **Phase-6 exit criterion** | `go test ./test/replay/ -run TestPhase6 -v` | PASS; on every `thrash-loop` session the first warning fires at or before the third repetition and ≥ 5 turns before the loop ends; zero warnings on non-thrash sessions |
| I-15.18 | Analyzer and grammar budgets | `go test ./internal/analyzer/ ./internal/grammar/ -bench . -benchtime 2s` | `SequiturAppend` < 20 µs/op with amortization proven (n=200 000 within 3× of n=20 000); `GrammarMarshal50k` < 20 ms; `WarningsFor` < 5 ms cold and `WarningsForCached` < 50 µs; `CheapScorer500` < 250 ms; `DetectRedundancy2000` < 300 ms; `LazyGreedy2000` < 50 ms; `LazyGreedyEvaluations` ≤ naive/5 |

### 2.16 SP-16 — Phase-7 refinements

| # | Functionality | Command | Expected |
|---|---|---|---|
| I-16.1 | `runtime.phase7` config namespace, additive only | `go test ./internal/config/ -run 'TestPhase7Defaults\|TestAppendixCGoldenStillPasses\|TestPhase7Validation\|TestPhase7InvalidConfigFallsBackNotCrashes\|TestConfigDocsNotStale' -v` | PASS; **Appendix C golden still green** — no Appendix C key changed; invalid leaves fall back, never crash |
| I-16.2 | **Ski-rental policy, computed not literal** (§5.6, Appendix A) | `go test ./internal/scheduler/ -run 'TestSkiRentalThresholdIsComputed\|TestNoLiteral12Point5InSource\|TestSkiRentalShouldWrite\|TestEstimateRemainingReads' -v` | PASS; `SkiRentalThreshold(0.1,1.25) == 12.5` exactly; **no compiled `12.5` literal anywhere in non-test source** |
| I-16.3 | Ski rental defers only on soft reasons | `go test ./internal/scheduler/ -run 'TestEvaluateDefers\|TestEvaluateNeverDefers\|TestEvaluatePrefers\|TestEvaluateKeepsDeepCut\|TestEvaluateColdCacheIgnoresSkiRental\|TestEvaluateIsStillPure' -rapid.checks=500 -v` | PASS; never overrides `hard_ceiling`, `changepoint` or `idle_cold_cache`; `Evaluate` still pure |
| I-16.4 | **BOCD feature priors seeded from past sessions** (O4) | `go test ./internal/scheduler/ -run 'TestDefaultFeaturePriors\|TestSeedPriors\|TestSeededDetector\|TestSetPriorWeight\|TestNewDetectorFromStateDisambiguates' -rapid.checks=200 -v` | PASS; standardization exact; format disambiguation never cross-restores and never panics |
| I-16.5 | **Per-segment Bloom filters** (§6.8) | `go test ./internal/store/ -run 'TestBuildSegmentBloom\|TestSegmentBloom\|TestSegmentKey\|TestSegmentMayContain\|TestSegmentsMayContain\|TestCloseSetsBloomRef\|TestBackfillSegmentBlooms\|TestCorruptSegmentBloomIsConservative' -v` | all PASS; Appendix A sizing (`m ∈ [19600,19700]`, `k == 7`); no false negatives; measured FP ≤ 1.5%; **zero object reads while narrowing**; `tried.bloom` untouched |
| I-16.6 | Ephemeral expansion counts and project touch counts from the index | `go test ./internal/store/ -run 'TestEphemeralExpansions\|TestProjectPathTouches' -v` | PASS; deterministic tie ordering; cancellation-safe |
| I-16.7 | **Demand-driven pointer promotion** (§8.7) | `go test ./internal/checkpoint/ -run 'TestPromote\|TestFinalizeIncludesPromotedPointers\|TestFinalizeStillUnderBudgetBE' -v` | all PASS; weight formula `boost × min(count/threshold, 3)`; idempotent; **`TestPromoteNeverAddsCodeSnippets`**; `Finalize` still < 2 s |
| I-16.8 | **Measured progressive-truncation reserves** (§6.9) | `go test ./internal/checkpoint/ -run 'TestTierReserve\|TestTruncateNeverDropsTier1\|TestTruncateDropsTier3BeforeTier2\|TestTruncateTier2DropOrder\|TestTruncateTier3DropOrder\|TestTruncateReservesLateBudget\|TestTruncateOversizeFieldDroppedWhole\|TestTruncateIsMonotone\|TestOrderPointersStable\|TestTierReserveMatchesCurve' -rapid.checks=300 -v` | all PASS; reserves equal the argmax recorded in `testdata/phase7/truncation-curve.json` |
| I-16.9 | Truncation curve artifact is reproducible | `go test ./test/replay/ -run 'TestTruncationCurve\|TestTunedReservesBeatUntuned' -v` (**without** `QOMPACK_UPDATE_GOLDEN`) | PASS; regenerates `testdata/phase7/truncation-curve.json` byte-identically; tuned ≥ untuned |
| I-16.10 | **O4 warm start** | `go test ./internal/daemon/ -run 'TestWarmStart\|TestSeedPriorsRunsBeforeRuntimeConstruction\|TestRegisterPhase7RegistersTwoTasks' -v` | all PASS; CMS decay+merge; bootstrap when the project CMS is missing; project-scope eliminations carried and correctly flipped stale; BOCD priors seeded **before** Runtime construction; step failures isolated; budget honoured |
| I-16.11 | Warm start never blocks the hook | `go test ./test/e2e/ -run 'TestWarmStartDoesNotBlockSessionStart\|TestHooksStillExitZeroUnderPhase7Faults' -v` | PASS; `qompack session-start` always exits 0 |
| I-16.12 | **Phase-7 warm-start delta** | `go test ./test/replay/ -run TestPhase7WarmStartDelta -v` | PASS; `mean(warm.FractionOfOPT) >= mean(cold.FractionOfOPT)` **and** `mean(warm.FirstDivergenceTurn) >= mean(cold.FirstDivergenceTurn)`; `testdata/phase7/warmstart-delta.json` reproduced |
| I-16.13 | Phase-7 regression and growth guardrails | `go test ./test/replay/ -run 'TestPhase7NoRegressionBeyondTwoPercent\|TestPhase7StoreGrowthStillSublinear' -v` | PASS; zero disallowed regressions; segment-bloom bytes ≤ 3 KB × segment count |
| I-16.14 | **Declared non-delivery is enforced** (§12 "cannot place or move `cache_control` breakpoints") | `go test ./test/replay/ ./test/e2e/ -run 'TestPrefixReorderingNotAttempted\|TestPhase7ADRDocumentsNonDelivery' -v` | PASS; no `cache_control`/`CacheBreakpoint`/`ReorderPrefix`/`MutationRateSort` outside `internal/eval`; ADR carries the verbatim sentence |
| I-16.15 | Phase-7 e2e seams | `go test ./test/e2e/ -run 'TestSegmentBloomAnswersWithoutExpansion\|TestPromotedPointersReachRehydration\|TestIndexExpansionsAgreeWithPromoter' -v` | PASS; promoted pointer reaches `additionalContext` within the 12 000-token cap; store index and MCP Promoter agree on the same hash set |
| I-16.16 | Phase-7 budgets | `go test ./internal/store/ ./internal/checkpoint/ ./internal/daemon/ ./internal/scheduler/ -bench 'BenchmarkBuildSegmentBloom\|BenchmarkSegmentsMayContain200\|BenchmarkPromote500\|BenchmarkTruncateTuned\|BenchmarkWarmStart10Sessions\|BenchmarkEvaluateWithSkiRental' -benchtime 2s` | `BuildSegmentBloom` < 50 ms; `SegmentsMayContain200` < 5 ms; `Promote500` < 50 ms; `TruncateTuned` < 20 ms at budget 12 000; `WarmStart10Sessions` < 3 s; `EvaluateWithSkiRental` within 10% of baseline |

---

## 3. Exit-criteria re-verification

Every phase exit criterion that has been claimed by any merged subplan is re-measured here against
the **merged** `develop`, not against the branch that claimed it. Quotes are verbatim from
`Qompack.md`.

### 3.1 Phase 0 (SP-02)

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

**Procedure.** `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline .\v5-p0-1.json`
twice into two files; `fc /b .\v5-p0-1.json .\v5-p0-2.json` (or `Compare-Object`) must report no
difference. Open `testdata/baseline/phase0.json` and confirm `policies.stock.fraction_of_opt`
equals the freshly computed number, `"corpusTier":"synthetic"` is present, and the session count is
**24 ≥ `eval.minSessions` (20)**. Confirm `docs/adr/0002-replay-methodology.md` still documents the
recorded-corpus command and owner. **The synthetic substitution must remain declared, never silent.**

### 3.2 Phase 1 (SP-04 measurement, SP-05 latency, SP-06 store, SP-08 closure)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

**Procedure.** (a) `go test ./test/e2e/ -run TestPhase1_DedupRatioReadHeavy -v` → `Stats().DedupRatio >= 4.0`,
where the ratio is `RawBytes / Bytes` and nothing else. (b)
`go test ./test/e2e/ -run TestPhase1_CanonicalizationGapOnTestOutput -v` → `ratioOn >= ratioOff × 1.25`;
record both raw numbers. (c) `go test ./test/dedup/ -v` reproduces `testdata/canon-dedup-report.json`
with `testrunner` `gain ≥ 1.25`. (d)
`go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json .\v5-ba.json`
on **all three platforms** → `B-A p99 < 15 ms`. Record the three p99 values.

### 3.3 Phase 2 (SP-09, surfaced by SP-13)

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

**Procedure.** `go test ./test/replay/ -run TestPhase2ExitCriterion -v`. Record `stock_repeats`,
`negknow_repeats`, `reduction_pct`, `stale_blocks`, `dependency_change_sessions` from the emitted
`phase2-negknow.json`. Required: `stock_repeats > 0`; `negknow_repeats ≤ 0.75 × stock_repeats`;
improvement on ≥ 8 individual sessions; **`stale_blocks == 0`** with `dependency_change_sessions > 0`.
Then confirm the MCP surface agrees: `go test ./internal/mcp/ -run 'TestAlreadyTriedStale' -v` —
`already_tried` returns `"stale"`, never `"active"`, for a record whose dependency changed.

### 3.4 Phase 3 (SP-10 producer, SP-11 consumer)

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

**Procedure.** `go test ./test/replay/ -run TestPhase3 -v` and record A1/A2/A3:
**A1** `median(qompack.RehydrationTokens) < median(stock.RehydrationTokens)` and
`qompack ≤ runtime.rehydrate.maxTokens (12 000)` on **every** session.
**A2** `median(qompack.FirstDivergenceTurn) > median(baseline)` from `testdata/baseline/phase0.json`
over the ≥ 20 sessions carrying a `CompactionAt`; the assertion message prints both numbers.
**A3** `residual ≤ checkpoint.frontier.maxResidualTokens (20 000)` and `1 − residual/stockSpan ≥ 0.5`
on the multi-compaction sessions. **At V5 this runs against SP-10's real `checkpoint.Writer` and
SP-12's real frontier advancement, not against the wave-3 goldens** — that substitution is the whole
point of re-running it here.

### 3.5 Phase 4 (SP-12)

> **Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).

**Procedure.** `go test ./test/replay/ -run 'TestPhase4_' -v`. Required:
`Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock]`; no divergence metric regressed > 2%
without a sign-off trailer; least-squares slope of median residual span vs. turn count
`≤ 0.02 tokens/turn`; longest-quartile median `≤ 1.25 ×` shortest-quartile median;
`ResidualSpan.P95 ≤ 20 000`; `.qompack/eval/phase4-pause.json` carries `"pause_modelled": true`.
**Re-run this after SP-16's ski-rental clause has merged into `Evaluate`** — a policy that defers a
compaction changes rewrite tokens, and the ≤ 0.80 ratio must still hold.

### 3.6 Phase 5 (SP-15)

> **Exit criterion:** improved fraction-of-OPT at equal budget.

**Procedure.** `go test ./test/replay/ -run TestPhase5 -v`:
`FractionOfOPT(analyzer-suffix-submodular) > FractionOfOPT(baseline)` on **all 24** sessions at an
identical 12 000-token budget, delta ≥ 0.02 on the read-heavy and refactor subsets, matching
`testdata/replay-baseline/phase5.json` to 4 decimal places. Also confirm the guarantee that makes
the number meaningful: `go test ./internal/analyzer/ -run Prop -rapid.checks=1000 -v`.

### 3.7 Phase 6 (SP-15)

> **Exit criterion:** thrash detected before the user notices it, on replay.

**Procedure.** `go test ./test/replay/ -run TestPhase6 -v`: on every `thrash-loop` session (at least
one must exist) the first warning fires at or before the third repetition **and** at least 5 turns
before the loop ends; on every non-`thrash-loop` session the end-of-session warning set is empty.

### 3.8 Phase 7 (SP-16)

Phase 7 has no exit criterion of its own; it is governed by §11.3 plus its statement of intent:

> **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions. First-compaction quality in a fresh session should benefit from every session before it.

**Procedure.** `go test ./test/replay/ -run 'TestPhase7WarmStartDelta\|TestPhase7NoRegressionBeyondTwoPercent\|TestPhase7StoreGrowthStillSublinear' -v`.
Record both signed deltas from `testdata/phase7/warmstart-delta.json`; both must be `≥ 0`.

### 3.9 The §11.3 guardrails (every phase gate)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

Measured in §5 (budgets) and §6 (regression). All four bind at V5.

### 3.10 The §11.4 watch-fors

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system… Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

**Procedure.** (a) `go test ./test/replay/ -run TestGate_CorpusStaleness -v` — the corpus
`regeneratedAfterPhase` must satisfy the schedule in `docs/adr/0003-replay-overfit-recollection.md`
for a build that has now merged Phase 7. If it does not, that is a V5 finding: regenerate the corpus
per the ADR on `verify/v5`, or record the deferral with the ADR's stated justification.
(b) `go test ./test/replay/ -run TestGate_BloomFPCeiling -v` and
`go test ./internal/negknow/ -run TestHealth -v` and `go test ./internal/store/ -run TestSegmentBloomFalsePositiveRateUnderBudget -v`
— every bloom in the system (elimination ledger **and** the new per-segment filters) reports
`EstFPRate` well under 0.10, and the segment filters measure ≤ 1.5% at design fill.

### 3.11 Invariants (§13) — all ten, re-asserted on the merged tree

| Invariant | Command |
|---|---|
| 1. Never compress a compression | `go test ./internal/checkpoint/ -run 'TestSourceSetCarriesNoText\|TestAdvanceIsDPIGuarded\|TestAdvanceStripsInjectionsFromStoredPrompts' -v`; `go test ./internal/store/ -run TestSegment_MarkEncoded -v` |
| 2. Append-only means append-only | `go test ./internal/paths/ -run TestAppendOnlyGuard -v`; `go test ./internal/testutil/ -run TestProject_AssertAppendOnly -v` |
| 3. The bloom filter is a cache | `go test ./internal/negknow/ -run 'TestQuery_BloomOnly\|TestBloomLoadFailure_NoRecords_NeverFalsePositive\|TestRebuildBloom_NeverFromCheckpoint' -v` |
| 4. Nothing scattered before `p` | `go test ./internal/analyzer/ -run 'TestNewSelectorRejectsPreP\|PropNothingBeforeP' -v`; `go test ./test/e2e/ -run TestNoSelectorBypass -v` |
| 5. No code snippets in checkpoints | `go test ./internal/checkpoint/ -run 'TestGoldenCheckpointsContainNoCodeBlocks\|TestRenderActionHistoryNoCodeFences\|TestPromoteNeverAddsCodeSnippets' -v` |
| 6. Hooks exit 0. Always. | `go test ./test/e2e/ -run 'TestHooksExitZeroUnderFaults\|TestHooksStillExitZeroUnderPhase7Faults\|TestThrashWarningHookStillExitsZero' -v` |
| 7. No network, no telemetry, no writes outside `.qompack/` | `go test ./test/guards/ -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' -v`; CI `security` job |
| 8. Every §12-volatile constant is a config key | `go run ./tools/devtool lint` (`nomagic`); `go test ./internal/scheduler/ -run 'TestEvaluate_MultipliersReadFromConfig\|TestNoLiteral12Point5InSource' -v` |
| 9. Every latency budget measured, not assumed | §5 below in full |
| 10. Degradation is loud | `go test ./internal/contract/ -run 'TestCriticalFailureDegrades\|TestDegradeIsIdempotent' -v`; `go test ./internal/commands/ -run TestStatus_Degraded -v` |

---

## 4. New cross-component integration tests

These tests **only make sense now**. Each spans a seam that did not exist before wave 4 merged.
They are authored during this checkpoint, committed to `verify/v5`, and become part of the permanent
suite. All live in `test/e2e/v5_integration_test.go` unless stated otherwise; each drives the
**real binary and a real daemon** against `testutil.NewProject(t)` with a `FakeClock`, and each ends
with `p.AssertAppendOnly(t)`.

### 4.1 `TestV5_ObserveToStatusRoundTrip`

**Seam:** L0 observer → store → sketches → DAG → scheduler runtime → `/qompack:status`
(SP-08 × SP-06 × SP-03 × SP-07 × SP-12 × SP-14). Until SP-14, nothing could read the whole pipeline
back out in one call.

*Setup.* Start a daemon. Send `session-start`, then 60 `observe tool` events over 8 distinct paths
(3 of them re-read twice so supersession fires), 4 `observe prompt`, 1 `observe stop --subagent`.

*Input.* `qompack status --json`.

*Expected.* Exit 0. `from_daemon == true`. `data.store.tool_uses == 65`; `data.store.dedup_ratio > 1.0`;
`data.store.objects > 0`. `data.sketches.bloom.fill_ratio == 0` (the observer never feeds the bloom).
`data.frontier.turn` and `data.frontier.residual_tokens` are both present and non-negative.
`data.latency` has a `hook_controlled.observe_tool` row with `n >= 60` and `p99 < 15ms`.
`data.mode.mode == "full"` and `data.mode.degraded == false`. `data.unavailable` is empty.

### 4.2 `TestV5_TombstoneToExpandRoundTrip`

**Seam:** addressable tombstone (SP-08) → store span resolution (SP-06 × SP-04) → MCP `expand`
(SP-13) → `/qompack:recall` frontend (SP-14).

*Setup.* Observe one 200 KB `FileRead` of a file containing `func refreshToken(...)`.

*Input.* Render the tombstone via `observer.Tombstone(rec)`; parse the `sha256:` short hash out of
it; drive `tools/call expand {"hash": "<full hash>"}` over real stdio; then run
`qompack recall "symbol:refreshToken" --json`.

*Expected.* The tombstone matches `^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead .+ · re-expandable\]$`.
`expand` returns a **chunk-aligned** span ≤ 16384 bytes containing the function, with
`_meta.qompack.ephemeral == true`. `recall`'s envelope contains a hit whose `hash` equals the
tombstone's root. **A `store.ToolUseRecord` with `Ephemeral: true` now exists for the expand**, and
`scheduler.DropClassOf` classifies it `DropEphemeral` — i.e. the retrieval result is the *first*
eviction candidate, closing the §8.7 loop end to end.

### 4.3 `TestV5_HookEventToTombstoneToRetrievalAfterRestart`

**Seam:** the full write path surviving a process boundary, then read back through L6
(SP-05 × SP-06 × SP-08 × SP-13).

*Setup.* Observe 40 tool uses, `flush`, kill the daemon, restart it.

*Input.* `tools/call expand` by `tool_use_id` for the 1st, 20th and 40th records; then
`tools/call re_read {"path":"src/auth.ts","at":"turn:12"}`.

*Expected.* All four calls return content. The `re_read` result equals the version stored at or
before turn 12, `source` is `"store"` or `"worktree"` per the file's presence, and the superseded
first read is still marked `StatusSuperseded` in `tool_use.jsonl`.

### 4.4 `TestV5_PreCompactToRehydrateToDroppedRoundTrip`

**Seam:** checkpoint (SP-10) → rehydrator (SP-11) → drop reporter → MCP `dropped` (SP-13) →
`/qompack:dropped` (SP-14). Four subplans, one fact.

*Setup.* A project fixture with `src/api/routes.ts`, `.claude/rules/api-conventions.md`
(`paths: ["src/api/**"]`), `src/api/CLAUDE.md`, and three `.claude/skills/`. Observe 40 tool uses
touching those paths, close 3 segments, run an idle tick, then `qompack checkpoint` with a real
`PreCompact` payload.

*Input.* `qompack session-start` with `{"source":"compact"}`; then `tools/call dropped {}`; then
`qompack dropped --json`.

*Expected.* The `PreCompact` output carries `customInstructions` containing the O1 span paragraph
naming the written checkpoint path and frontier turn. The `SessionStart` `additionalContext` contains
`## 1.` through `## 8.` in order, the restored rule body, the nested `CLAUDE.md` body, the skill index,
and is ≤ 12 000 tokens. **The `dropped` MCP tool and the `dropped` slash command return the
byte-identical entry list** (same kinds, same ids, same order) — SP-13's tool and SP-14's frontend
must not have diverged. `.qompack/state/rehydrate-<sess>.json` exists with the checkpoint seq.

### 4.5 `TestV5_EliminationThroughEveryFourSurfaces`

**Seam:** the elimination ledger reached from all four §8.3 sources now that all four exist
(SP-09 × SP-13 × SP-14 × SP-11).

*Setup.* Project with `docker-compose.yml` and `package-lock.json` ingested into the store.

*Input.* In order: (1) `tools/call record_eliminated` with target `src/auth.ts:refreshToken`,
approach `widen pool timeout`, reason `pgbouncer 1.18 ignores it in transaction mode`, scope `project`,
`depends_on` both files; (2) `qompack pin --eliminated --target src/db.ts --reason "pool is saturated" "disable pooling"`;
(3) a heuristic pattern driven through the DAG (`Edit → test:fail → revert → different Edit`);
(4) a user statement `"that didn't work"` through `observe prompt` with a resolvable target.

*Expected.* `Health().Records == 4` with `Source` values `SourceMCP`, `SourceSlashCommand`,
`SourceHeuristic`, `SourceUserStatement` — one each. `tools/call already_tried` returns
`"active"` with the exact stored reason for record 1 and for a **synonym** phrasing
(`"increasing the connection-pool timeouts"`). Then rewrite `docker-compose.yml`, re-ingest it, run
the negknow maintenance idle task: record 1 flips to `"stale"` and `already_tried` returns
`"stale"` with the §8.3 note **including the em dash**; `tried.bloom` is rebuilt from active
records only and exactly one `.bak` exists. Finally `qompack status --json` shows
`data.sketches.bloom.records == 4`, `active == 3`, `stale == 1`.

### 4.6 `TestV5_SelectorGatedByRealScheduler`

**Seam:** SP-12's live `PSelectionAvailable()` gate against SP-15's real selector — the first time
both sides are real (closing note 3).

*Setup.* Two sub-runs in one test, no `SetPSelectionProbe` (that is test-only and forbidden in
non-test code).

*Input.* (a) With no `scheduler.Runtime` constructed: call `analyzer.NewSelector(p, blocks, …)`.
(b) Construct a real `NewSchedulerRuntime` against a real store/DAG, then call it again.

*Expected.* (a) returns `ErrPSelectionUnavailable` and no `Selection`. (b) constructs, and
`Select` returns a keep-set with `Tokens ≤ budget`, every kept block having `Pos ≥ p`, and
`p` equal to the `Decision.P.Pos` the real scheduler chose from real candidates. Closing
`CloseSchedulerRuntime` makes (a) true again. This is the ship-order boundary asserted from both
sides with two real implementations rather than a stub and a flag.

### 4.7 `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint`

**Seam:** scheduler p-selection (SP-12) → analyzer suffix selection (SP-15) → checkpoint
pointer/narrative content (SP-10, SP-15, SP-16). This is §5.3's two-stage algorithm executed end to
end for the first time.

*Setup.* Drive a 300-turn synthetic session (`eval.Synthesize` seed `0x5105_0001`, multi-compaction
spec) through the real observer against a real store and DAG, with the scheduler runtime bound and
`checkpoint.frontier.advanceOnSegmentClose=true`.

*Input.* Run `Runtime.Evaluate()`; take `Decision.P`; build `analyzer.Block`s for
`dag.NodesAfter(P.Pos)`; construct the selector at `p = P.Pos`; select under a 12 000-token budget;
then run `checkpoint-now`.

*Expected.* `Decision.ShouldCompact == true` with a non-empty `Reasons`. Every block offered to the
selector has `Pos ≥ P.Pos` (the DAG's `NodesAfter` and the selector's constructor agree). The
selection's `Dropped` set contains **no** block whose `dag.NodeKind` is `KindUserPrompt`,
`KindDecision` or `KindElimination`. The written checkpoint contains every surviving decision, has
`encoded_segments` disjoint from every earlier checkpoint's (`MarkEncoded` never re-encodes), and
`Truncate` at the same budget produces the same tier-1 content byte-for-byte.

### 4.8 `TestV5_GrammarAndPromotionCoexistInFinalize`

**Seam:** the one file SP-15 and SP-16 both modify — `checkpoint.Finalize` (grammar fold **and**
pointer promotion). This test exists specifically to catch a merge that kept only one block.

*Setup.* Feed the grammar 11 `FileRead FileEdit Bash test:fail` cycles through the real observer.
Expand one root **3 times** through the MCP `expand` tool (threshold 2).

*Input.* `checkpoint-now --json`.

*Expected.* The written `NNNN.json` contains **both**: a `narrative` containing
`"Action history (grammar-compressed"` with `sketch_refs.grammar == "grammar/actions.seq"`, **and**
a `pointers.tools` entry whose `summary` starts with `"promoted: re-expanded 3×"` at an elevated
weight. Order is grammar first, promotion second. `"version": 1` is unchanged and the v1 key set is
identical to the golden. `TestGoldenCheckpointsContainNoCodeBlocks` passes on the produced file.
`Finalize` wall time < 2 s (**B-E**).

### 4.9 `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly`

**Seam:** SP-15's tier-3 action history under SP-16's measured tier reserves — the second
SP-15 × SP-16 collision point.

*Setup.* The checkpoint produced by 4.8.

*Input.* `checkpoint.Truncate` at descending budgets 12 000, 6 000, 3 000, 900, 1.

*Expected.* Monotone: `kept(b1) ⊆ kept(b2)` for `b1 < b2`. At 6 000 the action-history block is gone
before any decision is dropped. At 900 all of tier 3 (pointers **including promoted ones**, narrative)
is gone and tier 2 is partially cut in the documented order. At 1, tier 1 (`invariants`,
`user_intent`, `eliminated`) is **byte-identical to the input** with a single
`DropEntry{Kind:"budget_exceeded"}` and **no error**. `TestTierReserveMatchesCurve` still passes
against the committed curve artifact.

### 4.10 `TestV5_ThrashWarningVisibleInStatusAndCheckpoint`

**Seam:** grammar thrash (SP-15) → `UserPromptSubmit` `additionalContext` (SP-08 wiring) →
`/qompack:status` (SP-14) → checkpoint narrative (SP-15).

*Setup.* Real daemon; feed 11 thrash cycles through `observe tool`.

*Input.* `qompack observe prompt` with a real payload; then `qompack status --json`; then
`checkpoint-now`.

*Expected.* The prompt hook exits 0 and its `hookSpecificOutput.additionalContext` contains
`[qompack] thrash:` and `repeated 11×`. The same cycle appears once in the checkpoint's action
history. The warning is emitted **once per rule** — a second prompt does not repeat it. With the
contract monitor forced to `ModeDegradedPassive`, the prompt hook emits **nothing** while the
grammar still records the appends (§12.1 record-but-do-not-act), and `status` shows the degraded
banner.

### 4.11 `TestV5_SegmentBloomNarrowsRecall`

**Seam:** SP-16's per-segment filters answering a SP-13 retrieval question without touching objects.

*Setup.* 12 closed segments, each with 50 tool uses; one distinctive path present only in segment 7.

*Input.* `store.SegmentsMayContain` for that path, with an instrumented store counting
`Open`/`OpenSpan`/`GetChunk`/`GetRoot`; then `tools/call recall {"query":"path:<that path>"}`.

*Expected.* `SegmentsMayContain` returns `[7]` (≤ 1 extra false positive allowed) with **zero**
object-read calls. `recall` returns the hit. Truncating segment 7's bloom file to 8 bytes makes
`SegmentMayContain` conservative (`(true, err)`), emits exactly one `Loud` for that segment id, and
`recall` still returns the same hit — the filter is an optimization, never a source of truth.

### 4.12 `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession`

**Seam:** SP-16 warm start reading SP-06's store and SP-09's ledger, seeding SP-12's detector,
observed through SP-14's status surface. The O4 claim, end to end, across a real process boundary.

*Setup.* Session A: 200 tool uses concentrated on 6 hot files, one `scope:"project"` elimination,
20 closed segments; `flush`; daemon idle-exits. Session B: fresh daemon, fresh session id, same
project root.

*Input.* `qompack session-start` for session B, one idle tick, then `qompack status --json`.

*Expected.* `state/warmstart.json` records `Ran:true`. The live CMS estimate for a session-A hot
path is `> 0` and within the decay band. `already_tried` for the project-scope elimination returns
`"active"` in session B (cross-session carry-forward) — and returns `"stale"` instead if the
elimination's `depends_on` file was modified between the sessions. `state/bocd.json` existed before
the scheduler `Runtime` was constructed. `status --json` shows `data.scheduler.breakdown` populated
on the **first** evaluation of session B rather than after twenty turns. With
`sketches.cms.warmStartFromProject=false`, the CMS estimate is `0` and the elimination is still
carried (the two mechanisms are independent).

### 4.13 `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne`

**Seam:** SP-16's ski-rental clause inside SP-12's `Evaluate`, observed through SP-14's status
breakdown.

*Input.* Three `Runtime.Evaluate()` calls under scripted inputs: (a) soft reasons only, warm TTL,
`ExpectedRemainingReads = 5`; (b) same but `hard_ceiling` present; (c) same but `TTL = cold`.

*Expected.* (a) `ShouldCompact == false` with `"ski_rental_defer"` in `Reasons` and
`Breakdown["ski_rental_threshold"] == 12.5` (computed from config `w/r`, and doubling
`writeMultiplier` doubles it). (b) and (c) compact, with **no** ski-rental reason. `qompack status --json`
renders the same `Breakdown` map, sorted, with every key present — SP-14's
`TestEvaluate_BreakdownKeysComplete` golden must be updated on `verify/v5` if and only if SP-16
legitimately added keys, and the update must be a deliberate commit, not a `-update` sweep.

### 4.14 `TestV5_EveryContractAssertionHasARealProducer`

**Seam:** the contract monitor (SP-05) against the now-complete producer set (SP-10, SP-11, SP-13).

*Setup.* Drive a full session: `session-start` → 20 `observe tool` → `observe prompt` →
`qompack checkpoint` → `session-start` with `source=compact` → an MCP `initialize` → `flush` →
`session-start` again.

*Input.* `qompack self-test --json`.

*Expected.* Exit 0, `mode == "full"`, and **all nine** assertions report a real observation.
Specifically `mcp.server_registered`, `precompact.has_time_to_write`,
`precompact.custom_instructions_accepted` and `hook.additional_context_delivered` must **not** carry
`Observed: "not-yet-implemented"` — at V5 their producers all exist, and a `not-yet-implemented`
here means a `DeclareProducers`/`Bind` wiring regression, not a missing feature.

### 4.15 `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent`

**Seam:** §12.1's record-but-do-not-act rule with all sixteen subplans live — the first time every
acting path exists simultaneously.

*Setup.* Force `contract.ModeDegradedPassive`; then drive the same 60-event session as 4.1.

*Expected.* **Recording continues:** identical `PutBytes`/`RecordToolUse`/`AddNode`/CMS/HLL/Sequitur
counts to the `full`-mode run; elimination records still written. **Acting is off:** no
`additionalContext` on any hook, no `customInstructions` on `PreCompact`, no scheduler-initiated
checkpoint from the idle tick, no drop report emitted, no thrash warning, and `act.advance_frontier`
absent from `IdleController.RunOnce`'s `ran` list — while `drain`, `gc`, `segment_blooms` and
`warm_start` still run. **MCP retrieval stays available** (pull-based, cannot make anything worse):
`recall`, `expand`, `already_tried` all answer. `/qompack:status` leads with the degraded banner.
Two clean `SessionStart`s restore `ModeFull`, logged as loudly as the degradation.

### 4.16 `TestV5_NoPackageWritesOutsideDotQompack`

**Seam:** §13 invariant 7 with the wave-4 write paths added (`sketches/segments/`, `state/warmstart.json`,
`state/promotions.json`, `testdata`-free command output).

*Input.* Run the full 4.1 + 4.4 + 4.8 + 4.12 sequence under a filesystem-write recorder rooted at
the temp project.

*Expected.* Every write path is under `<root>/.qompack/` or `~/.qompack/` (calibration only). No
write to `plugin/`, `testdata/`, the repo, or any absolute path outside the project. `.gitignore`
self-ignoring still holds.

---

## 5. Performance budget validation

Every budget in force at this point. Run these **serially, on an idle machine**, after §2–§4. Record
every number in the completion report — a budget that is not written down was not measured
(§13 invariant 9).

| ID | Budget | Source | Command | Threshold |
|---|---|---|---|---|
| **B-A** | `hook_controlled` p99 | §8.1, §11.3 L0, 00-ARCH §2.4 | `go run ./tools/devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon --json .\v5-ba.json` on ubuntu, macos **and** windows | **p99 < 15 ms** on all three. Hard fail. |
| **B-A′** | `hook_controlled` p99 for the reply path | 00-ARCH §2.4 | same harness `--hook observe-prompt` | p99 < 15 ms **with the SP-15 thrash addendum active** — `WarningsFor` is inside this budget |
| **B-B** | `l0_ingest` (daemon read → WAL append) p99 | 00-ARCH §2.4 | reported by the same bench artifact; `go test ./internal/daemon/ -bench BenchmarkIngestAccept` | p99 < 2 ms |
| **B-C** | `l0_process` p99 (soft) | 00-ARCH §2.4 | `go test ./internal/observer/ -bench 'BenchmarkOnToolUse_' -benchtime 5s` | p99 < 50 ms; overrun degrades to sampling/backpressure, never blocking |
| **B-D** | `hook_wall` incl. host process creation | 00-ARCH §2.4 | same bench artifact | **reported, never gated.** Record it honestly. |
| **B-E** | `checkpoint_finalize` p99 | §11.3 L4, 00-ARCH §2.4 | `go run ./tools/devtool bench-hotpath --iterations 200 --hook checkpoint --json .\v5-be.json` | **p99 < 2 s** on all three platforms. Hard fail. Must hold **with grammar folding and pointer promotion both in `Finalize`.** |
| **B-F** | `mcp_tool_call` p95 (`minimal` span) | 00-ARCH §2.4 | `go test ./internal/mcp/ -run TestBudgetBF -v` and `go test ./internal/commands/ -bench BenchmarkMCPFrontend` | p95 < 250 ms |
| **P1-ratio** | Store dedup ratio on read-heavy sessions | §10 Phase 1 | `go test ./test/e2e/ -run TestPhase1_DedupRatioReadHeavy -v` | `Stats().DedupRatio ≥ 4.0` |
| **P1-gap** | Canonicalization gap on test-output-heavy sessions | §10 Phase 1 | `go test ./test/e2e/ -run TestPhase1_CanonicalizationGapOnTestOutput -v`; `go test ./test/dedup/ -v` | `ratioOn ≥ ratioOff × 1.25`; `testrunner` `gain ≥ 1.25`; overall `gain ≥ 1.0` |
| **Growth** | Store growth sublinear after dedup | §11.3 | `go test ./internal/store/ -run TestStats_SublinearGrowth -v`; `go test ./test/e2e/ -run TestPhase1_StoreGrowthSublinear -v`; `go test ./test/replay/ -run TestPhase7StoreGrowthStillSublinear -v` | sublinear on all three; growth exponent from `CheckSublinearGrowth` < 1.0; segment-bloom bytes ≤ 3 KB × segments |
| **Rehydration** | Injection budget | §8.6, §12 | `go test ./internal/rehydrate/ -run 'TestBuild_NeverExceedsMaxTokens\|TestBuild_SmallerThanStock' -v`; `go test ./test/e2e/ -run TestE2E_SessionStartCompactUnderBudget -v` | `Result.Tokens ≤ 12 000` always; strictly below the stock 50K+25K |
| **Residual span** | Tokens between frontier and compaction | §8.5 O5, §11.2 | `go test ./test/replay/ -run 'TestPhase4_ResidualUnderMaxResidualTokens\|TestPhase4_ResidualSpanFlatAsSessionGrows' -v` | `P95 ≤ 20 000`; slope ≤ 0.02 tokens/turn; longest quartile ≤ 1.25× shortest |
| **Rewrite tokens** | `Σ w·(n − p_min)` per session | §5.3, §11.2, §10 Phase 4 | `go test ./test/replay/ -run TestPhase4_RewriteTokensReduced -v` | `≤ 0.80 × stock` — re-measured **after** ski rental merged |
| **Bloom FP** | Elimination bloom and segment blooms | §11.4 | `go test ./internal/negknow/ -run TestHealth -v`; `go test ./internal/store/ -run TestSegmentBloomFalsePositiveRateUnderBudget -v`; `go test ./test/replay/ -run TestGate_BloomFPCeiling -v` | `EstFPRate < 0.02` at design fill; segment FP ≤ 1.5%; gate fails above 0.10 |
| **Micro-benchmarks** | Every named benchmark budget in §2 | 00-ARCH §7 | `go run ./tools/devtool bench` then `benchstat testdata/bench-baseline.txt .\v5-bench.txt` | no benchmark regressed **> 25%** (fail) — investigate every **> 10%** (warn). Update `testdata/bench-baseline.txt` on `verify/v5` **only** for legitimate wave-4 additions, in its own commit. |

Notes that must not be shortcut:

- **B-A is measured with real process spawns**, not `go test -bench`. The harness pre-populates a
  warm daemon with 2 000 tool uses and 40 MB of raw output so the CMS and DAG are realistically sized.
- **B-D is reported and never gated.** Hiding host process creation inside B-A is exactly the
  dishonest measurement §1.3 RC-3 indicts.
- Budgets whose components do not exist yet: none. At V5 every layer L0–L7 exists, so every budget
  in the table binds.

---

## 6. Regression

V5 re-runs the full inventory of every prior checkpoint, because "the wave-4 branches did not touch
that package" is a claim, not a measurement — and SP-16 in particular edits seven sites across six
files owned by SP-01, SP-05, SP-06, SP-10 and SP-12.

### 6.1 Predecessor checkpoints

| Checkpoint | Covered subplans | Re-run in V5 as |
|---|---|---|
| **V1** (wave 0) | SP-01 | §2.1 in full, plus the preamble `ci-local` / `plugin-validate` / `gen-config-docs --check` / `build-all` |
| **V2** (wave 1) | SP-02, SP-03, SP-04, SP-05, SP-06, SP-07 | §2.2–§2.7 in full, plus B-A/B-B/B-D and the Phase-0 and Phase-1 exit criteria (§3.1, §3.2) |
| **V3** (wave 2) | SP-08, SP-09 | §2.8–§2.9 in full, plus the Phase-1 and Phase-2 exit criteria (§3.2, §3.3) |
| **V4** (wave 3) | SP-10, SP-11, SP-12, SP-13 | §2.10–§2.13 in full, plus the Phase-3 and Phase-4 exit criteria (§3.4, §3.5) and budgets B-E and B-F |
| **V5** (wave 4, this one) | SP-14, SP-15, SP-16 | §2.14–§2.16, §3.6–§3.8, and all of §4 |

There is nothing to skip. Run §2 in its entirety across the eight subagents; run §3, §4 and §5 in
the main session.

### 6.2 The full CI matrix on `verify/v5`

```
go run ./tools/devtool ci-local           # verify: fmt, lint, vet, nomagic, importgraph,
                                          #   testdeps, bindeps, sleepcheck, stubskips, build
go test ./... -race                       # ubuntu + macos
go test ./... -count=2                    # windows
go run ./tools/devtool cover              # every §6.4 floor
go run ./tools/devtool crossbuild         # six targets
go run ./tools/devtool bench-hotpath --iterations 2000     # bench-gate, three platforms
go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop --ci
go run ./tools/devtool plugin-validate
go run ./tools/devtool gen-config-docs --check
go run ./tools/devtool gen-mcp-docs   ; git diff --exit-code -- docs/mcp-tools.md
go run ./tools/devtool gen-command-docs ; git diff --exit-code -- docs/commands.md plugin/commands/
govulncheck ./...                         # security job
```

Coverage floors that bind at V5 (00-ARCHITECTURE §6.4), all measured on the merged Linux profile:

| Floor | Packages |
|---|---|
| **90%** | `config` `store` `sketch` `chunk` `canon` `negknow` `checkpoint` `paths` (+ SP-06's self-imposed 90% on `redact` and `tokens`, and SP-10's on `pins`) |
| **85%** | `scheduler` `dag` `analyzer` `rehydrate` `eval` `mcp` |
| **75%** | everything else, including `commands` `grammar` `observer` `ipc` `daemon` `contract` `rules` `skills` |

A drop below any floor fails `verify` and is a V5 failure, not a waiver.

### 6.3 The 2% no-regression guardrail (§11.3)

> - No metric may regress by more than 2% to improve another without explicit sign-off

**Procedure.**

```
go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop --ci --json .\v5-replay.json
```

The gate compares every metric in `eval.MetricsOf` for every policy against the recorded baseline,
honouring `MetricDirection` (higher-better vs. lower-better) and the zero-baseline absolute
tolerance. Requirements at V5:

- **Zero `Regression` entries with `Allowed == false`.**
- Any regression that *is* allowed must be justified by a `sign-off:` trailer in the merge commit
  message on `verify/v5` naming **the exact metric** and giving a reason of more than four
  characters — `sign-off: rewrite_tokens=+3.1% traded for a 9% first-divergence gain`. A blanket
  sign-off is not accepted; the gate matches per metric.
- The 2% boundary is exclusive: −1.98% passes, −2.02% fails. Do not round.
- `TestGate_PhaseChecksMayNotBeDisabledInCI` must pass — `eval.replayOnPhaseGate` cannot be turned
  off to make this green.
- Every phase exit assertion merged so far (Phases 0–7) runs inside the gate. `--phase 7` must pass.

The same rule applies to `benchstat`: a micro-benchmark that regresses > 25% fails the build, and
one that regresses > 10% requires an explanation in the completion report even though CI only warns.

---

## 7. Failure protocol

Any failure — a red test, a missed budget, an unmet exit criterion, a coverage floor, a lint finding,
a golden mismatch, a `t.Skip` that should be gone, a `not-yet-implemented` contract assertion — stops
the checkpoint. Wave 5 is not cut.

**1. Diagnose systematically. Do not guess.**

- Reproduce the failure in isolation with the narrowest possible command
  (`go test ./internal/<pkg>/ -run <ExactTestName> -v`), and capture the output verbatim.
- Establish **when** it broke: `git bisect` between the pre-wave-4 `develop` tag (`v0.3.x`) and HEAD,
  or, faster for the three wave-4 branches, check out each merge commit's first parent in turn.
- Classify the failure before touching code:
  - **(a) Merge artifact** — a three-way merge that dropped or reordered one of the two blocks at a
    shared site. Check the three collision points listed in the header first. Fix by restoring both
    blocks in the documented order.
  - **(b) Genuine integration defect** — two subplans are individually correct and jointly wrong
    (e.g. SP-16's ski rental changes the p chosen, which changes SP-15's block set, which changes the
    Phase-5 number). Fix at the seam, and add the integration test that would have caught it to §4.
  - **(c) Latent defect in an earlier wave** exposed by new load. Fix in the owning package; do not
    work around it in the new caller.
  - **(d) A stale fixture or baseline.** Regenerating a golden is legitimate **only** when the
    implementation change is intended and the golden is the thing that is wrong. Rule W-2 is
    explicit: *"Any fixture that the real implementation cannot reproduce is a verification failure,
    not a fixture bug."* Never run `-update` or `QOMPACK_UPDATE_GOLDEN=1` to make red go green;
    regenerate only after writing down why the new bytes are correct, and put the regeneration in
    its own commit with that reason in the body.
  - **(e) An architectural interface is wrong.** Do not work around it. Open `arch/<short-reason>`
    off `develop`, amend `00-ARCHITECTURE.md` §5, get it merged, and rebase `verify/v5` (§0
    amendment rule).
- Write the diagnosis down before the fix. A fix whose commit body cannot say *why* the code was
  wrong is a guess.

**2. Fix on the verify branch.**

All fixes land on `verify/v5` as **small conventional commits — as many as needed**, not one large
one. Each commit compiles and passes `go run ./tools/devtool test` for the packages it touches.
Format per 00-ARCHITECTURE §10:

```
<type>(<scope>): <subject>

<body — why the code was wrong, not what the diff does>

Refs: V5, SP-NN, <gap ids>, <Qompack.md sections>
```

`type` ∈ `feat fix docs test refactor perf build ci chore revert`; `scope` is the Go package; the
subject is imperative, ≤ 72 chars, no trailing period.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR body
in this repository. CI's `verify` job greps the commit range for `Co-Authored-By`, `Signed-off-by`,
`Generated with`, and `🤖` and fails the build if any appear.

**3. Re-run the whole checkpoint from the top.**

After **any** fix, re-run §2 through §6 in full — not just the failing test. A fix in `store`
invalidates the measurements of every package above it, and the point of an exhaustive checkpoint is
that its result is a statement about the whole tree at one commit. Partial re-runs are not accepted
evidence.

**4. No wave-5 branch is cut until this checkpoint is fully green.**

`feat/sp17-packaging-hardening-and-release` and `feat/sp18-documentation-and-uat` are not created,
not branched, not started, until every row of §2, every criterion of §3, every test of §4, every
budget of §5 and every gate of §6 passes on `verify/v5`. This is 00-ARCHITECTURE §9's rule and it is
not negotiable for schedule reasons.

**5. Merge and tag.**

```
git checkout develop
git merge --no-ff verify/v5 -m "chore: verification checkpoint V5

<one line per fix landed, or 'no fixes required'>

Refs: V5, SP-14, SP-15, SP-16"
git tag v0.4.0
```

Then re-run `go run ./tools/devtool ci-local` and the replay gate **on `develop`** once more after
the merge, because a `--no-ff` merge can still surface a semantic conflict that neither parent had.
Only then cut wave 5 from this `develop`.

---

## 8. Completion report template

Fill this in and paste it into the merge commit body (abridged) and into
`docs/adr/0017-v5-verification.md` (in full). Every metric cell must carry a **measured number**,
never "ok" or a checkmark.

### 8.1 Header

```
V5 verification — commands, selection, grammar, Phase-7 refinements
develop commit:      <sha>          (merges: sp14 <sha>, sp15 <sha>, sp16 <sha>)
verify/v5 commit:    <sha>
Run started:         <ISO-8601>     Run completed: <ISO-8601>
Platforms:           ubuntu-<ver> / macos-<ver> / windows-<ver>
Go toolchain:        go1.26.<x>
Fixes landed on verify/v5: <n commits>
Attribution-trailer grep over develop..verify/v5: <empty / FINDINGS>
```

### 8.2 Inventory results

| ID | Functionality | Result | Metric / evidence |
|---|---|---|---|
| I-01.1 | core hashing & sentinels | PASS/FAIL | |
| I-01.2 | paths resolution & normalization | | |
| I-01.3 | append-only guard (5/5 refusals) | | |
| I-01.4 | atomic writes & manifest | | |
| I-01.5 | Appendix C verbatim | | |
| I-01.6 | config precedence & merge | | |
| I-01.7 | validation falls back, never crashes | | |
| I-01.8 | `FuzzConfigLoad` | | crashers: |
| I-01.9 | logging & Loud channel | | |
| I-01.10 | histograms & six budgets | | |
| I-01.11 | token estimation | | |
| I-01.12 | hook wire format | | |
| I-01.13 | `FuzzReadEvent` | | crashers: |
| I-01.14 | hooks exit 0 (30 combos) | | |
| I-01.15 | plugin manifest & validate | | |
| I-01.16 | config docs not stale | | |
| I-01.17 | zero stub residue | | packages still stubbed: |
| I-01.18 | zero `t.Skip` in conformance suites | | count: |
| I-01.19 | W-2 fixtures reproduced by real impls | | |
| I-01.20 | ship-order & safety guards | | |
| I-01.21 | toolchain lints | | |
| I-01.22 | testutil fixtures | | |
| I-01.23 | six-hook e2e | | |
| I-01.24 | SP-01 benchmark budgets | | ns/op: |
| I-02.1 … I-02.14 | SP-02 replay/Belady/baseline (14 rows) | | fraction_of_opt(stock)= |
| I-03.1 … I-03.13 | SP-03 sketches (13 rows) | | bloom m/k=, cms w/d=, L0 update ns= |
| I-04.1 … I-04.17 | SP-04 chunk/canon/symbols (17 rows) | | testrunner gain= |
| I-05.1 … I-05.20 | SP-05 daemon/IPC/contract (20 rows) | | B-A p99=, B-B p99= |
| I-06.1 … I-06.18 | SP-06 store/redact/tokens (18 rows) | | DedupRatio= |
| I-07.1 … I-07.11 | SP-07 dag/slicing (11 rows) | | BackwardSlice5000= |
| I-08.1 … I-08.15 | SP-08 observer (15 rows) | | ratioOn/ratioOff= |
| I-09.1 … I-09.14 | SP-09 negknow (14 rows) | | reduction_pct=, stale_blocks= |
| I-10.1 … I-10.18 | SP-10 checkpoint/pins (18 rows) | | Finalize mean=, residual P50 on/off= |
| I-11.1 … I-11.19 | SP-11 rehydrate/rules/skills (19 rows) | | Result.Tokens max= |
| I-12.1 … I-12.21 | SP-12 scheduler (21 rows) | | rewrite ratio=, residual slope= |
| I-13.1 … I-13.19 | SP-13 MCP (19 rows) | | B-F p95= |
| I-14.1 … I-14.16 | SP-14 commands/status (16 rows) | | Collect p95=, RenderStatus= |
| I-15.1 … I-15.18 | SP-15 analyzer/grammar (18 rows) | | ΔfractionOfOPT=, thrash first-warn turn= |
| I-16.1 … I-16.16 | SP-16 Phase 7 (16 rows) | | warm-cold ΔOPT=, ΔfirstDiv=, segment FP= |

*(Expand every abbreviated block to one row per inventory ID when filling this in. A collapsed block
is not a report.)*

### 8.3 Exit criteria

| Phase | Criterion (short) | Threshold | Measured | Result |
|---|---|---|---|---|
| 0 | reproducible stock fraction-of-OPT over ≥ 20 sessions | 24 sessions, byte-identical reruns | | |
| 1 | dedup ratio, read-heavy | ≥ 4.0 | | |
| 1 | canonicalization gap, test-output-heavy | ≥ 1.25× | | |
| 1 | hook p99 | < 15 ms ×3 platforms | | |
| 2 | repeated-elimination reduction | ≤ 0.75 × stock | | |
| 2 | stale-block incidents | == 0 | | |
| 3 | A1 rehydration budget vs stock | qompack < stock, ≤ 12 000 | | |
| 3 | A2 first-divergence vs Phase-0 baseline | median improves | | |
| 3 | A3 residual / span reduction | ≤ 20 000 and ≥ 50% | | |
| 4 | rewrite tokens | ≤ 0.80 × stock | | |
| 4 | divergence regression | ≤ 2% | | |
| 4 | residual-span slope | ≤ 0.02 tok/turn | | |
| 4 | residual-span P95 | ≤ 20 000 | | |
| 5 | fraction-of-OPT vs baseline, equal budget | > baseline, Δ ≥ 0.02 on read-heavy/refactor | | |
| 6 | thrash first warning | ≤ 3rd repetition, ≥ 5 turns before end | | |
| 6 | false thrash warnings on non-loop sessions | 0 | | |
| 7 | warm vs cold fraction-of-OPT | Δ ≥ 0 | | |
| 7 | warm vs cold first-divergence | Δ ≥ 0 | | |

### 8.4 New integration tests (§4)

| Test | Result | Notes / measured value |
|---|---|---|
| `TestV5_ObserveToStatusRoundTrip` | | tool_uses=, p99= |
| `TestV5_TombstoneToExpandRoundTrip` | | span bytes= |
| `TestV5_HookEventToTombstoneToRetrievalAfterRestart` | | |
| `TestV5_PreCompactToRehydrateToDroppedRoundTrip` | | injection tokens= |
| `TestV5_EliminationThroughEveryFourSurfaces` | | records=4, active/stale= |
| `TestV5_SelectorGatedByRealScheduler` | | |
| `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint` | | p= |
| `TestV5_GrammarAndPromotionCoexistInFinalize` | | Finalize ms= |
| `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly` | | |
| `TestV5_ThrashWarningVisibleInStatusAndCheckpoint` | | |
| `TestV5_SegmentBloomNarrowsRecall` | | object reads= |
| `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession` | | CMS estimate= |
| `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne` | | threshold= |
| `TestV5_EveryContractAssertionHasARealProducer` | | not-yet-implemented count= |
| `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` | | |
| `TestV5_NoPackageWritesOutsideDotQompack` | | |

### 8.5 Performance budgets

| ID | Threshold | ubuntu | macos | windows | Result |
|---|---|---|---|---|---|
| B-A `hook_controlled` p99 | < 15 ms | | | | |
| B-A′ `observe-prompt` p99 | < 15 ms | | | | |
| B-B `l0_ingest` p99 | < 2 ms | | | | |
| B-C `l0_process` p99 | < 50 ms (soft) | | | | |
| B-D `hook_wall` p99 | reported only | | | | n/a |
| B-E `checkpoint_finalize` p99 | < 2 s | | | | |
| B-F `mcp_tool_call` p95 | < 250 ms | | | | |
| Dedup ratio (read-heavy) | ≥ 4.0 | | | | |
| Canon gain (testrunner) | ≥ 1.25 | | | | |
| Store growth exponent | < 1.0 | | | | |
| Rehydration tokens (max) | ≤ 12 000 | | | | |
| Residual span P95 | ≤ 20 000 | | | | |
| Rewrite tokens vs stock | ≤ 0.80 | | | | |
| Elimination bloom EstFPRate | < 0.02 | | | | |
| Segment bloom measured FP | ≤ 1.5% | | | | |
| `benchstat` worst regression | ≤ 25% (warn > 10%) | | | | |

### 8.6 Regression and gates

| Gate | Command | Result | Notes |
|---|---|---|---|
| `verify` (fmt/lint/vet/nomagic/importgraph/testdeps/bindeps/sleepcheck/stubskips/build) | `devtool ci-local` | | |
| `test` ubuntu `-race` | `go test ./... -race` | | |
| `test` macos `-race` | | | |
| `test` windows `-count=2` | | | |
| `cover` (all §6.4 floors) | `devtool cover` | | lowest package + % |
| `crossbuild` (6 targets) | `devtool build-all` | | |
| `bench-gate` (B-A, B-E) | `devtool bench-hotpath` | | |
| `replay-gate` (2% rule + phases 0–7) | `devtool replay --ci` | | disallowed regressions: |
| `plugin-validate` | | | |
| `security` (`govulncheck`, import assertions, secret scan) | | | |
| `docs` (config, mcp-tools, commands) | | | |
| V1 inventory re-run | §2.1 | | |
| V2 inventory re-run | §2.2–§2.7 | | |
| V3 inventory re-run | §2.8–§2.9 | | |
| V4 inventory re-run | §2.10–§2.13 | | |

### 8.7 Findings and fixes

| # | Finding | Class (a–e per §7) | Root cause | Fix commit | Re-run result |
|---|---|---|---|---|---|
| 1 | | | | | |

### 8.8 Sign-off

```
[ ] Every inventory row in §2 executed and recorded — no row skipped, no row summarized.
[ ] Every exit criterion in §3 re-measured against the MERGED develop.
[ ] All sixteen §4 integration tests authored, passing, and committed to verify/v5.
[ ] Every §5 budget measured on all three platforms and written down.
[ ] Full §6 regression green, including V1–V4 inventories and the 2% guardrail.
[ ] Zero disallowed regressions; every allowed one carries a per-metric sign-off: trailer.
[ ] Zero t.Skip in any conformance suite; zero ErrNotImplemented outside internal/core.
[ ] Zero attribution trailers in develop..verify/v5.
[ ] verify/v5 merged into develop with --no-ff; ci-local and replay-gate re-run on develop after the merge.
[ ] Tag v0.4.0 applied.
[ ] No wave-5 branch existed at any point before this line was ticked.

Verified by: ____________________   Date: ____________
```

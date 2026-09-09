# V5-VERIFY inventory — SP-15 (analyzer selection and grammar)

**SP:** SP-15 — analyzer selection, submodular representation choice, Sequitur/grammar, thrash
warnings.
**Reconciliation target:** "SP-15 M5-G15-A/B/C and M6-G15-A/B; feasible actual consumer, diagnostic
signals, bounded progress-aware warnings."

**Owner plan sections consulted**
- `plans/V5-SP-15-analyzer-selection-and-grammar.md` — §Out of scope (line 21: no generic 1−1/e
  guarantee), §G6.3 (line 37: "a heuristic hypothesis, not a correctness or optimality guarantee"),
  §objective (line 41), §evidence layers (line 69), §broader checks (line 77), §quiet-runner
  benchmarks (lines 146 and 165).
- `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md` §3a — 3a.1 gate dispositions,
  3a.2 consumer integration, 3a.3 the M6-G15-B ablation, 3a.4 the one conformance-suite change,
  3a.5 selector guards NOT retired, 3a.6 defects, 3a.7 failures and their §7 disposition, 3a.8 what
  SP-15 does not discharge — and §4 (retained identifiers, incl. 4.6–4.10).
- `plans/sdd/V5-SP-15/contract.md` §2 (`Proposal`), §3 (objective/feasibility, no (1−1/e) claim),
  §4 (codec frame); `plans/sdd/V5-SP-15/report-main.md` §§1–5.
- `plans/sdd/V4-VERIFY/reconciliation-map.md` — the only SP-15-touching row is `rc-v4-all-04`
  (`analyzer` and `grammar` are two of the three coverage-floor exemptions; SP-15 had not landed at
  that base). **No `I-15.*` row was adjudicated for V4**, so no mapping was reused and every result
  below was executed on this tree.
- `plans/CARRIED-DEFECTS.tsv` — **no SP-15 row**; nothing carried in or out here.

**Tree:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @ `87c0c1d`
("chore(sp21): integrate deterministic admission control").
**Platform:** Windows 11, go1.26.6 windows/amd64. **Date:** 2026-09-08/09.
**Rows:** 18 (`I-15.1` … `I-15.18`), all reconciled, none dropped.

**Dispositions** — MAPPED 9 · MAPPED-CMD 0 · SUPERSEDED-BY-WAVE4 3 · RETIRED 2 · MISSING 4 ·
NEEDS-COORDINATOR 0.
**Results** — PASS 14 · FAIL 0 · FAIL-BASELINE 0 · FAIL-COLOAD-SUSPECT 0 · SKIP 0 · NOT-RUN 4.

Every `-run` pattern was confirmed with `go test -list` before it was run. Nine of the eighteen
historical patterns select **zero** cases on this tree and were corrected against the packages'
`*_test.go` files (§3); two more select cases that are **false positives** for what the row asserts,
and one historical command cannot run at all (§3, I-15.12). No `go test` output was piped; each run
was redirected to the artifact named on its row and the file read afterwards, with the exit code
captured explicitly.

---

## 2. Row table

Artifact root `A/` = `C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-15/`.

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-15.1 | Sequitur online/linear-time; `TestSequiturOverlapExceptionAAA`, `…RuleUtilityInlines`, `…UsesIsOccurrenceNotRefCount`, `…ConcurrentAppendAndRules` PASS under `-race` | MAPPED | `internal/grammar`: 15 tests / **82 passing cases** under `-run TestSequitur` — `_InvariantsHoldAfterEveryAppend`, `_ConformanceStreamInducesRules`, `_SmallGrammarsAreExact`, `_RetiresUnderUsedRules`, `_UtilitySweepIsANet`, `_Determinism`, `_Reset`, `_ThrashThreshold`, `_ThrashIgnoresUnrepeatedWork`, `_Snapshot*`, `_Restore*`, `_NewReturnsTheRealImplementation` | PASS | `A/I-15.1.txt` | direct | Run **without** `-race` (race runs barred for this seat); the race half is deferred (§4). The overlap exception is now the white-box `checkNoRepeatedDigram` inside `_InvariantsHoldAfterEveryAppend`, not a test of its own; the concurrency test is RETIRED (§3). |
| I-15.2 | Two invariants property-tested: `PropDigramUniqueness`, `PropRuleUtility`, `PropOccurrenceCountMatchesExpansion`, `PropExpansionRoundTrip`, `PropAcyclic`, `PropCompressionIsNotExpansion`, `PropDeterminism` at 1 000 checks | MAPPED | `internal/grammar`: `TestSequitur_InvariantsHoldAfterEveryAppend` (both invariants re-checked after **every** append) + `TestCodec_RoundTripIdentityProperty` at `-rapid.checks=1000` + the `grammartest` `invariants_hold_after_every_append` block driven by `TestSequitur_ConformanceStreamInducesRules` | PASS | `A/I-15.2.txt`, `A/I-15.1.txt` | direct | None of the seven `Prop*` names exists; `-run Prop` selects exactly one test here. `internal/grammar` does link `pgregory.net/rapid`, so `-rapid.checks=1000` is accepted (contrast I-15.12). |
| I-15.3 | Versioned CRC codec + frozen golden; `actions_v1.seq` byte-equal; header `QPKG`, v1, Castagnoli CRC; every corruption class rejected, receiver unchanged | SUPERSEDED-BY-WAVE4 | `internal/grammar`: `TestEncodeSnapshot_{FrameHeader,Deterministic}`, `TestCodec_{RoundTripIdentity,CompatibilityVectors,RoundTripIdentityProperty}`, `TestDecodeSnapshot_{RejectsBadFrames,RejectsEveryTruncation,DoesNotAliasInput,OnlyEverReportsKnownSentinels}` — 9 tests, all PASS without regenerating; goldens `internal/grammar/testdata/codec/v1-{empty,rules,edges}.golden` | PASS | `A/I-15.3.txt` | direct | New frame per contract §4 + amendment A6 (§3a.6 defect 4): magic `"qompack-grammar\x00"` (`codec.go:51`), **no CRC** — integrity is exact-length framing (`codec.go:114`–`134`); version 0 now refused. No `Save`/`Load` file API ships, so "missing file is not found" has no owner. `actions_v1.seq` exists nowhere in the repo. |
| I-15.4 | Grammar fuzz `FuzzGrammarUnmarshal -fuzztime 120s`: no crashers; every survivor satisfies both invariants | MISSING | **No fuzz target exists** — `grep -rn "^func Fuzz" internal/grammar internal/analyzer` returns nothing, and `FuzzGrammarUnmarshal` appears nowhere in the repo | NOT-RUN | — | direct | Fuzz runs are barred for this seat *and* there is nothing to run: the surface is `DecodeSnapshot`, so the target would be `FuzzDecodeSnapshot`, owned by `internal/grammar`. `go test -fuzz` over an empty target set reports success — the same false-green trap as `-run`. §4, §5 Q1. |
| I-15.5 | Thrash detection and warning formatting; deterministic `ID ASC` tiebreak across 20 rebuilds; `thrash_warning.txt` byte-equal | MAPPED | `internal/grammar`: `TestFormatWarning`, `TestStateSignature_Key`, `TestStateWarning_IsAdvisoryOnly`, `TestIsSelfOriginated`, `TestDetectorStats_SeparatesUsefulnessFromFrequency`, `TestDetector_{ProgressObservedSuppressesEntirely,ProgressUnknownIsOnlyEverUncertain,DeduplicatesAndCapsPerSession,SelfOriginatedNeverWarns,ExpiryBoundsDelivery,RendersThroughFormatWarning,ZeroConfigTakesDefaults,IsDeterministic}` — 13 tests, all PASS | PASS | `A/I-15.5.txt` | direct | Determinism is `TestDetector_IsDeterministic`, not a 20-rebuild loop. `thrash_warning.txt` does not exist; the golden is the inline literal `TestFormatWarning` asserts against `formatwarning.go:21`–`22`. |
| I-15.6 | Grammar size budget: 50 000 appends serialize to < 512 KiB (`TestCodecSizeBudget`) | MISSING | `go test -list` selects nothing for `TestCodecSizeBudget`; no size-budget assertion exists in `internal/grammar` (`grep -n "SizeBudget\|512"` over its `*_test.go` → empty) | NOT-RUN | — | direct | Nothing to run — the historical command prints a green `ok` having asserted nothing. `internal/grammar` owns writing it against `EncodeSnapshot`. §4, §5 Q1. |
| I-15.7 | Δ-scoring cheap retrospective proxy; deterministic across 100 calls; nil store and missing objects tolerated (`TestCheapScorer`, `TestStopwordListSize`) | MAPPED | `internal/analyzer`: `TestCheapScorer_{ModeIsTheTierItsConstructorNames,EveryBlockIsScoredInTheUnitInterval,IsDeterministic,AnEmptyBlockSetScoresNothing,ACoveringContinuationOutscoresAnEmptyOne,ANilStoreStillScoresEveryBlock,AnUnreadableRootDoesNotDropTheBlock,ALowScoreIsNotACorrectnessLabel,SurvivesZeroValuedArguments}` + `TestAnalyzerConformance_DeltaScorerAgainstARealStore` — 10 tests, all PASS | PASS | `A/I-15.7.txt` | direct | Nil store and unreadable root are covered by name. **`TestStopwordListSize` selects nothing** — no stopword list ships in the cheap tier; that clause is retired with no replacement needed (§3). |
| I-15.8 | G6.3 made testable: an elimination rationale outranks a re-readable file body in the cheap scorer | RETIRED | `internal/analyzer`: `TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence`, `TestPropose_AStaleMandatoryCandidateIsNeverPromotedToAConstraint`, `TestQualification_{TheZeroValueDoesNotBind,OnlyCurrentIsActive,StringsAreStable}`, `TestNewCandidates_QualificationIsUncertainUntilSomeoneEstablishesIt` — 27 passing cases in the `TestPropose\|TestQualification\|TestNewCandidates` run | PASS | `A/I-15.8.txt` | direct | `TestCheapScorerRanksNegativeKnowledgeHighest` selects nothing and cannot exist: `tools/devtool/importrules.go` denies `analyzer` any `negknow` import, and the plan (line 37) downgrades "always the highest-Δ" to a heuristic hypothesis. G6.3 now travels as `Candidate.Mandatory` + `Provenance.Qualification` into `Propose`. §3. |
| I-15.9 | Redundancy / supersession detection; per-path rule; near-dup threshold from config; deterministic ordering (`TestSortedNearDupKeys`, `TestExcludeFromSummary`, `TestApplyToSetsSuperseded`, `TestToolUseIDOf`) | MAPPED | `internal/analyzer`: `TestDetectRedundancy_{SupersessionChainIsExact,SupersessionStopsAtTheSessionBoundary,PathlessResultsAreNeverSuperseded,HonoursTheStoresOwnSupersededStatus,NearDuplicatesAreCandidatesNotEquivalence,NoResultIsItsOwnNearDuplicate,UnrelatedResultsAreNotNearDuplicates,IsReadOnly,AnEmptySessionReportsNothing,ANilStoreReportsNothing,UsesTheShippedThresholdWhenGivenNoConfig}`, `TestRelations_{ASharedFileEdgeIsPreservedAndProvesNothing,AbsenceOfAnEdgeEstablishesNothing,ANilGraphStillReportsWhatWasAsked}`, `TestAnalyzerConformance_RedundancyAgainstARealStore` — 15 tests, all PASS | PASS | `A/I-15.9.txt` | direct | The four named helper tests select nothing: the helpers are unexported and asserted through behaviour. `ToolUsesBySession` coverage stays OPEN per §3a.8 — `DetectRedundancy` returns `core.ErrDegraded` beside a valid result rather than presenting partial coverage as complete (§5 Q5). |
| I-15.10 | Suffix-constrained selector: nothing before `p` constructible; `ErrBlockBeforeP` names block id, pos and `p`; negative-λ rejected | MAPPED | `internal/analyzer`: `TestNewSelector_{RefusesABlockBeforeP,PosCheckRunsBeforeTheShipOrderCheck,ShipOrderGateDecidesTheLegalCandidateSet,PosEqualToPIsLegal,EmptyCandidateSetStillConsultsTheShipOrderGate}` — 5 tests, all PASS | PASS | `A/I-15.10.txt` | direct | All four historical names select **zero**. `ErrBlockBeforeP` → `core.ErrBudget` wrapping `"block %s at pos %d precedes p=%d"` (`selector.go:47`–`49`) — id, pos and `p` are all named, so the substance holds. **No negative-λ guard ships** and there is no direct `Selector.P()` test (§3). |
| I-15.11 | Lazy-greedy submodular selection: budget respected; superseded/ephemeral penalized; diminishing returns; lazy == naive with strictly fewer evaluations on ≥ 190/200 instances; golden small case | MAPPED | `internal/analyzer`: `TestSelector_SelectIsRealBehindTheGate`, `TestSelect_{NeverExceedsItsBudgetAndReportsTheTrueTotal,KeepAndDroppedPartitionTheCandidateSet,ZeroBudgetKeepsNothing,IsDeterministicAcrossCalls,NothingBeforePIsEverKept,CoverageSaturatesWithinAContentRootGroup,AnUnscoredBlockIsNeverKept,TheSliceScoreIsTheFallbackForAnUnscoredBlock,LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer,CancelledContextReportsErrBudget}` — 11 tests, all PASS | PASS | `A/I-15.11.txt` | direct | Diminishing returns = `_CoverageSaturatesWithinAContentRootGroup`. The **≥ 190/200 statistical claim is not asserted**: `_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer` is one deterministic instance asserting fewer iterations for the same keep-set — no ratio, no 200-instance sweep (§3). |
| I-15.12 | The `(1 − 1/e)` guarantee brute-forced: `PropGuaranteeUnitCost`/`PropGuaranteeKnapsack` at 1 000 checks, plus `PropMonotone`, `PropSubmodular`, `PropBudgetNeverExceeded`, `PropNothingBeforeP`, `PropCoverageMinusLambdaRedundancy` | RETIRED | `internal/analyzer`: `TestObjective_HeuristicAgainstTheExactOptimum` (7 brute-forced instances: plain, tiny-budget, zero-budget, dependency-closure, mandatory-overflow, qualification-g63, redundancy-nonmonotone) + `TestObjectiveFixtures_CoverEveryRequiredShape` — PASS | PASS | `A/I-15.12.txt` | direct | Two independent reasons. (a) The **guarantee itself is retired**: contract §3 and plan lines 21/41 state no approximation bound is claimed or claimable — redundancy subtraction breaks monotonicity and the dependency / one-representation constraints change the feasible family; the tests record the heuristic/exact **ratio** against a brute-forced optimum and assert no bound. (b) The historical command **cannot even run**: `internal/analyzer` does not link `pgregory.net/rapid`, so `-rapid.checks=1000` aborts the test binary with `flag provided but not defined` (captured in `A/I-15.12.txt` before correction), and `-run Prop` there selects 14 `TestPropose_*` by prefix — all false positives. |
| I-15.13 | Ship-order guard real and inert without p-selection; zero non-test `SetPSelectionProbe` refs; `TestNoSelectorBypass` AST-parses `internal/analyzer` and finds exactly one `Pos`-vs-`p` comparison | MAPPED | `test/guards`: `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SubmodularDefaultsOff`, `TestGuard_SelectorRefusesWithoutPSelection` — PASS. `internal/analyzer`: `TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck`, `TestNewSelector_RefusesABlockBeforeP` — PASS | PASS | `A/I-15.13-guards.txt`, `A/I-15.13.txt` | direct | All six historical names select zero. The **probe indirection is retired** — `SetPSelectionProbe` has zero references anywhere in the repo, so there is no test-only setter to count. The **AST bypass check has no replacement**: the property holds today (the only `Pos`-vs-`p` comparison in non-test `internal/analyzer` is `selector.go:47`, established by grep, not by an executed assertion), so a second one could be added with nothing failing. §5 Q3. |
| I-15.14 | Grammar folded into the checkpoint at compaction time; narrative appended never rewritten; `sketch_refs.grammar` set; `"version": 1` unchanged; no code fences | MISSING | No definition. All five historical names select zero in `internal/checkpoint`; `ActionHistory` appears **nowhere in the repo**; `SourceSet.Grammar` is only validated non-nil (`source.go:81`–`82`) and never read by non-test code | NOT-RUN | — | direct | Two of the five clauses have current owners and pass elsewhere: no-code-fences is `internal/checkpoint` `nocode_test.go` + `fixture_test.go:149` (§13 inv. 5), and `"version":1` is pinned in `finalize_paths_test.go`. The `sketch_refs.grammar` clause **conflicts with shipped policy**: `schema.go:22`–`27` says exactly the two keys `tried`/`touch`, "no more", so an amendment is required before any test could assert it. SP-15 commit 6 spent its checkpoint budget on selection integration instead. §5 Q2. |
| I-15.15 | Thrash warning reaches `additionalContext`: `"[qompack] possible loop:"` and `"repeated 11×"` present, inner context preserved, hook exits 0 | MAPPED | `internal/observer`: `TestOnUserPrompt_ThrashWarningInFullMode`, `TestOnUserPrompt_NoThrashWarningInPassiveMode`, `TestOnUserPrompt_ThrashWarnedOncePerRule`, `TestCollectThrash_{QueuesEachRuleExactlyOnce,QueuesANewlyThrashingRule,AppendsTheToolSymbolAndTheTestVerdict,NilGrammarIsTolerated}`, `TestPendingThrashLines_DrainsAndFormatsThroughGrammar` — 8 tests, all PASS | PASS | `A/I-15.15.txt` | direct | Mapped **at unit level only** — `test/e2e -run TestThrashWarning` selects zero; nothing at e2e level drives the positive path through the real binary. The row's prefix note is confirmed on this tree: `formatwarning.go:21`–`22` emits `[qompack] possible loop: … repeated N× (turns …) — …`; `"[qompack] thrash:"` appears nowhere. §5 Q4. |
| I-15.16 | Phase-5 exit criterion: `FractionOfOPT(analyzer-suffix-submodular) > baseline` on all 24 sessions at 12 000 tokens, delta ≥ 0.02 on read-heavy/refactor, `phase5.json` to 4 dp | SUPERSEDED-BY-WAVE4 | `test/replay`: `TestPhase5_{HeldOutTrialOutcomes,G63DispositionsAreTheOnesTheContractNames,HeuristicAgainstExactRatiosRecorded,SelectionAgainstTheCompleteRecordHeuristicOnTheCorpus,ShipOrderGateStillRefusesProposal,PhaseCheckIsSelfContained,DefaultsStayOff}` — 7 tests, all PASS | PASS | `A/I-15.16.txt` | direct | Pattern `TestPhase5` is still valid; the **criterion** is replaced. SP-15 commit 7 / V5-VERIFY §3a.1 gate **M5-G15-A**: held-out trials at three budget regimes plus a 117-row corpus arm with priors held fixed, and recorded heuristic-vs-exact ratios — not FractionOfOPT over 24 sessions, and no `phase5.json` 4-dp golden. |
| I-15.17 | Phase-6 exit criterion: first warning at or before the third repetition and ≥ 5 turns before the loop ends; zero warnings on non-thrash sessions | SUPERSEDED-BY-WAVE4 | `test/replay`: `TestPhase6_{HeldOutFalseAlarmAndUsefulnessReport,BoundedDeliveryUnderLoad,SelfSuppressionIsAClosedLoop,UncertainWarningsSayThatTheyAreUncertain,AblationSequiturAgainstStateSignatures,ArmsAreReproducible,ProgressSuppressionIsAWindowBoundAndNotAGag,PhaseCheckIsSelfContained}` — 8 tests, all PASS | PASS | `A/I-15.17.txt` | direct | Same commit; V5-VERIFY §3a.1 gate **M6-G15-A** plus §3a.3's **M6-G15-B** ablation. The criterion is now false-alarm rate (0/7), self-suppression, bounded delivery and uncertainty labelling; "zero warnings on non-thrash sessions" survives as the false-alarm arm, the timing lead does not. `_BoundedDeliveryUnderLoad` bounds a delivery **count**, not a duration, so no co-load suspicion applies. |
| I-15.18 | Analyzer and grammar budgets: `SequiturAppend` < 20 µs/op amortized, `GrammarMarshal50k` < 20 ms, `WarningsFor` < 5 ms cold / `WarningsForCached` < 50 µs, `CheapScorer500` < 250 ms, `DetectRedundancy2000` < 300 ms, `LazyGreedy2000` < 50 ms, `LazyGreedyEvaluations` ≤ naive/5 | MISSING | **Zero benchmarks exist** — `grep -rn "^func Benchmark" internal/grammar internal/analyzer` returns nothing, and none of the seven named benchmarks exists anywhere in the repo | NOT-RUN | — | direct | Also barred for this seat (benchmarks / quiet machine). `go test -bench .` on these two packages today prints a green `ok` having measured nothing. The `LazyGreedyEvaluations ≤ naive/5` clause has only the qualitative stand-in noted on I-15.11. **`internal/analyzer` and `internal/grammar` own writing these**; the plan's own "preserve the quiet-run benchmark requirement" (line 165, cf. line 146) is not recorded as discharged in §3a or `report-main.md`. §4, §5 Q1. |

---

## 3. Old-to-new assertion map

**Corrected `-run` patterns.** Each was confirmed with `go test -list` before any run; the full
package enumerations are in `A/list-grammar.txt` and `A/list-analyzer.txt`.

| Row | Historical pattern | Selects on this tree | Corrected pattern / replacement |
|---|---|---|---|
| I-15.1 | `TestSequiturOverlapExceptionAAA`, `TestSequiturRuleUtilityInlines`, `TestSequiturUsesIsOccurrenceNotRefCount`, `TestSequiturConcurrentAppendAndRules` | 0 each (the bare `TestSequitur` prefix still matches, by luck of the `_` naming) | `TestSequitur_SmallGrammarsAreExact` (the `aaa` overlap case), `TestSequitur_{RetiresUnderUsedRules,UtilitySweepIsANet}` (rule utility), `TestSequitur_InvariantsHoldAfterEveryAppend` via `checkNoRepeatedDigram` (occurrence, not refcount). **Concurrency: RETIRED** — `sequitur.go` carries no lock and `statewarn.go` states the detector is deliberately not safe for concurrent use because one session's observations arrive in turn order. A concurrent-append test would assert a property the package disclaims. |
| I-15.2 | `Prop` plus the seven `Prop*` names | 1, incidental: `TestCodec_RoundTripIdentityProperty` | `TestSequitur_InvariantsHoldAfterEveryAppend` + `TestCodec_RoundTripIdentityProperty -rapid.checks=1000` + the `grammartest` `invariants_hold_after_every_append` block. |
| I-15.3 | `TestSaveLoadRoundTrip`, `TestLoadMissingFileIsNotFound` | 0 | `TestEncodeSnapshot_*`, `TestDecodeSnapshot_*`, `TestCodec_CompatibilityVectors`. |
| I-15.5 | `TestWarningsFor`, `TestPromptAddendum` | 0 | `TestDetector_*` (11), `TestStateWarning_IsAdvisoryOnly`, `TestStateSignature_Key`, `TestFormatWarning`. |
| I-15.6 | `TestCodecSizeBudget` | 0 | none — MISSING. |
| I-15.7 | `TestStopwordListSize` | 0 | none; no stopword list ships in the cheap tier. Every `TestCheapScorer*` name gained a `_`. |
| I-15.8 | `TestCheapScorerRanksNegativeKnowledgeHighest` | 0 | RETIRED, below. |
| I-15.9 | `TestSortedNearDupKeys`, `TestExcludeFromSummary`, `TestApplyToSetsSuperseded`, `TestToolUseIDOf` | 0 | folded into `TestDetectRedundancy_*` (the helpers are unexported and asserted through behaviour); `TestRelations_*` carries the "an edge proves nothing" side. |
| I-15.10 | `TestNewSelectorRejectsPreP`, `TestNewSelectorAcceptsPosEqualP`, `TestNewSelectorRejectsNegativeLambda`, `TestSelectorP` | 0 | `TestNewSelector_RefusesABlockBeforeP`, `TestNewSelector_PosEqualToPIsLegal`, `TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet`. **No negative-λ guard ships**, and there is no direct `Selector.P()` test — `P()` exists and is read back by `Propose`, but only indirectly asserted. **`ErrBlockBeforeP` → `core.ErrBudget`** wrapping `"block %s at pos %d precedes p=%d (§5.3, §13 invariant 4)"` (`selector.go:47`–`49`). |
| I-15.11 | "≥ 190/200 instances, strictly fewer evaluations" | n/a | one deterministic instance, `TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer`; the statistical claim is asserted nowhere. |
| I-15.12 | `Prop` (`PropGuarantee*` and five others) with `-rapid.checks=1000` | 14 `TestPropose_*`, all false positives — **and the flag is undefined in this package, so the historical command exits non-zero without running a test** | `TestObjective_HeuristicAgainstTheExactOptimum`, `TestObjectiveFixtures_CoverEveryRequiredShape`. RETIRED, below. |
| I-15.13 | `TestNewSelectorInertWithoutPSelection`, `TestNewSelectorLiveWithPSelection`, `TestPSelectionProbeDefaultsToScheduler`, `TestSetPSelectionProbeRestores`; e2e `TestPSelectionProbeIsTestOnly`, `TestNoSelectorBypass` | 0 each | `test/guards` `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_SubmodularDefaultsOff`; `internal/analyzer` `TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck`. Probe indirection retired (no `SetPSelectionProbe` exists). **AST bypass check: no replacement.** |
| I-15.14 | `TestBuildActionHistory`, `TestRenderActionHistory`, `TestFold`, `TestFinalizeIncludesActionHistory`, `TestTruncateDropsActionHistoryFirst` | 0 each | none for the fold itself — MISSING. Two clauses have separate owners: `internal/checkpoint` `nocode_test.go` (no code fences) and `finalize_paths_test.go` (`"version":1`). |
| I-15.15 | e2e `TestThrashWarning` | 0 | `internal/observer` `TestOnUserPrompt_ThrashWarningInFullMode` and seven siblings. E2E-level coverage remains absent. |
| I-15.16 / I-15.17 | `TestPhase5` / `TestPhase6` | 7 / 8 — the **patterns are valid**; the **criteria** are superseded | see the row table. |
| I-15.18 | `-bench .` | 0 benchmarks — the command prints a green `ok` having measured nothing | none — MISSING. |

**Retirements (the assertion no longer matches current semantics)**

- **I-15.8 — "an elimination rationale outranks a re-readable file body" in the cheap scorer.**
  Reason: `tools/devtool/importrules.go` denies `analyzer` any `negknow` import, so the cheap scorer
  cannot see elimination evidence at all, and the owner plan (line 37) downgrades the
  always-highest-Δ claim to a heuristic hypothesis. Replacement: G6.3 evidence travels as a
  `Candidate` with `Mandatory` + `Provenance.Qualification` into `Propose` (V5-VERIFY §3a.2),
  asserted by `TestPropose_AnAuthoritativeEliminationIsCarriedWithItsOwnEvidence`,
  `TestPropose_AStaleMandatoryCandidateIsNeverPromotedToAConstraint`, `TestQualification_*`,
  `TestNewCandidates_QualificationIsUncertainUntilSomeoneEstablishesIt`, and `test/replay`
  `TestPhase5_G63DispositionsAreTheOnesTheContractNames`. Related: §3a.6 defect 1 made
  `QualUncertain` the zero value precisely so an omitted field cannot silently mint an authoritative
  elimination.
- **I-15.12 — the `(1 − 1/e)` guarantee.** Reason: contract §3 and plan lines 21/41 state that no
  approximation bound is claimed or claimable — redundancy subtraction does not preserve
  monotonicity, and the dependency and one-representation-per-item constraints change the feasible
  family; the plan's Out-of-scope section names "universal … greedy approximation guarantees"
  explicitly. Replacement: `TestObjective_HeuristicAgainstTheExactOptimum` records the
  heuristic/exact ratio per committed instance against a brute-forced optimum and asserts **no**
  bound, with a `redundancy-nonmonotone` instance committed rather than tuned away.

**Supersessions (a landed subplan changed the requirement itself)**

- **I-15.3** — SP-15 contract §4 plus amendment A6 (V5-VERIFY §3a.6 defect 4) replaced the
  CRC-bearing `QPKG` / on-disk `actions_v1.seq` codec with an in-memory `Snapshot` frame:
  `"qompack-grammar\x00"` magic, an explicit version (zero now refused), exact-length framing in
  place of a checksum, goldens under `internal/grammar/testdata/codec/`.
- **I-15.16** — SP-15 commit 7, V5-VERIFY §3a.1 gate **M5-G15-A**: held-out trials at three budget
  regimes plus a 117-row corpus arm with priors held fixed, replacing FractionOfOPT-over-24-sessions
  and the `phase5.json` 4-dp golden.
- **I-15.17** — same commit, §3a.1 gate **M6-G15-A** and §3a.3's **M6-G15-B** ablation: false-alarm
  rate, self-suppression, bounded delivery and uncertainty labelling, replacing the
  third-repetition / five-turn-lead timing criterion. §3a.3 records that M6-G15-B went **against**
  Sequitur (6/7 false alarms against 0/7 for the state-signature arm), which is why the induction
  ships optional and disabled while the warning path stays signature-only.

---

## 4. Deferred to coordinator

| Row | Exact command | Reason |
|---|---|---|
| I-15.1 (race half) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/grammar/ -run 'TestSequitur' -race -count=1 -timeout=30m` | Race runs are barred for this seat. The non-race run PASSED (82 cases, `A/I-15.1.txt`); only the detector is outstanding. Expect it quiet — the package holds no locks and asserts single-goroutine ownership. |
| I-15.4 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/grammar/ -run '^$' -fuzz FuzzDecodeSnapshot -fuzztime 120s -timeout=30m` | **Cannot pass today — no fuzz target exists.** Listed so the quiet pass does not silently skip the row: the target must be written first (`internal/grammar` owns it; `FuzzGrammarUnmarshal` has no current entry point — the surface is `DecodeSnapshot`). |
| I-15.6 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/grammar/ -run TestCodecSizeBudget -v -count=1 -timeout=30m` | **No such test.** The command prints a green `ok` today. The 512 KiB / 50 000-append budget must be written in `internal/grammar` before this row can be discharged. |
| I-15.14 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/checkpoint/ -run 'TestBuildActionHistory\|TestRenderActionHistory\|TestFold\|TestFinalizeIncludesActionHistory\|TestTruncateDropsActionHistoryFirst' -v -count=1 -timeout=30m` | **No such tests, and the `sketch_refs.grammar` clause contradicts `schema.go:22`–`27`.** Needs an ownership ruling (§5 Q2) before any command can discharge it; the command is listed only so the row is not silently green. |
| I-15.18 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/analyzer/ ./internal/grammar/ -run '^$' -bench . -benchtime 2s -timeout=30m` | Benchmarks plus quiet machine. **Warning: today this prints a green `ok` having measured nothing** — both packages contain zero `func Benchmark`. It discharges the row only once the seven budgets exist. |

No row was classified FAIL-COLOAD-SUSPECT: nothing in this set failed, and none of the five known
pre-existing failures on this tree (`test/guards` `TestCarriedDefects_WaveReportRequiresResolution`,
`test/e2e` `TestV3_HotPathUnchangedWithLedgerResident`, `test/integration`
`TestIntegration_{BeladyPMinLandsAtLowCoupling,HotPathWarmWithRealResidentState}`, `internal/negknow`
`TestBudget_Open`) is an SP-15 row. The `test/guards` run for I-15.13 was filtered to the three
selector/submodular guards and never reached the carried-defects test. No SP-15 row asserts a
wall-clock budget on this tree — the only timing-shaped name, `TestPhase6_BoundedDeliveryUnderLoad`,
bounds a delivery count rather than a duration.

---

## 5. Questions

1. **Zero benchmarks and zero fuzz targets in `internal/analyzer` and `internal/grammar`
   (I-15.18, I-15.4, I-15.6).** The historical inventory names seven micro-budgets, a 512 KiB codec
   size budget and `FuzzGrammarUnmarshal`; none exists, and the plan's own dispatch note ("preserve
   the quiet-run benchmark requirement", line 165) is not recorded as discharged in §3a or
   `report-main.md`. Does V5-VERIFY accept the two newly-real packages shipping with no performance
   floor and no fuzz coverage, or is this a new carried defect? The failure mode is silent: both
   `go test -bench .` and `go test -fuzz` over an empty target set report success.
2. **Who owns the checkpoint action-history fold (I-15.14)?** SP-15's commit 6 spent its checkpoint
   budget on selection integration; `SourceSet.Grammar` is validated non-nil and never read. The
   `sketch_refs.grammar` half additionally conflicts with `schema.go:22`–`27`'s "exactly the two
   keys, no more" policy, so an amendment is needed before any test could assert it. SP-16, a V5
   sign-off item, or retired outright?
3. **Is the AST bypass check (`TestNoSelectorBypass`) still required (I-15.13)?** The property holds
   today — exactly one `Pos`-vs-`p` comparison, at `selector.go:47` — but that is a grep, not an
   executed assertion, so a second comparison could be added without any test failing. `test/guards`
   already owns the two behavioural halves and would be the natural home for the AST scan.
4. **No e2e-level thrash coverage (I-15.15).** `test/e2e -run TestThrashWarning` selects nothing;
   the positive path is asserted only at `internal/observer` unit level. V5-VERIFY §4.10
   (`TestV5_ThrashWarningVisibleInStatusAndCheckpoint`) is the retained identifier for this and is
   still an unchecked future criterion. Should an e2e case be required for V5 sign-off, or do the
   observer-level tests plus `test/replay` phase 6 suffice? Related: `test/e2e/v3_x08_test.go`
   carries a comment asserting grammar is a stub, which is no longer true at this HEAD even if its
   negative assertion still holds for the degraded-passive mode X8 runs in.
5. **`ToolUsesBySession` (I-15.9)** is recorded OPEN in V5-VERIFY §3a.8 — `DetectRedundancy` returns
   `core.ErrDegraded` alongside a valid result rather than presenting partial coverage as complete.
   Confirming this stays a V5 sign-off item rather than an SP-15 inventory row.

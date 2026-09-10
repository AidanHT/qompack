# V5-VERIFY inventory — SP-10 (checkpointer L4 / pins)

**SP:** SP-10 — *Test plan gates; committed frontier, compatible readers, lifecycle gaps, complete
records and explicit overflow.*

**Owner plan sections consulted:** `plans/V4-SP-10-checkpointer-l4.md` — Mission (incl. the withdrawal
of the native O(delta) and first-turn-saving claims), §Host boundary correction, §Lifecycle model,
§Required invariants 1–7, §Implementation spec IS-10-01 … IS-10-09, §Test plan (TDD) T10-\* gate
table, §Exit criteria. Cross-read: `plans/sdd/V4-VERIFY/reconciliation-map.md` rows
`V4-SP10-02 … V4-SP10-21` (re-run here, not inherited) and `plans/CARRIED-DEFECTS.tsv` row `SP10-D1`.

**Tree:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @ `87c0c1d`
(*chore(sp21): integrate deterministic admission control*).
**Platform:** Windows 11, `go1.26.6 windows/amd64`, Intel Core Ultra 7 155H (22 logical CPUs).
**Date:** 2026-09-08. **Rows:** 18 (`I-10.1` … `I-10.18`), none dropped.

**Command deviations, applied uniformly and deliberately:** every run adds `-count=1` so that no cell
is scored from the build cache; the benchmark row adds `-run '^$'` so that `-bench` does not also
re-run the package's test suite. Nothing else in any historical command was changed except where the
"Old-to-new assertion map" says so.

## 1. Counts

### Disposition counts

| Disposition | Count |
|---|---|
| MAPPED | 11 |
| MAPPED-CMD | 0 |
| SUPERSEDED-BY-WAVE4 | 4 |
| RETIRED | 3 |
| MISSING | 0 |
| NEEDS-COORDINATOR | 0 |
| **Total** | **18** |

### Result counts

| Result | Count |
|---|---|
| PASS | 16 |
| FAIL | 0 |
| FAIL-BASELINE | 0 |
| FAIL-COLOAD-SUSPECT | 1 |
| SKIP | 0 |
| NOT-RUN | 1 |
| **Total** | **18** |

No row on this SP hit one of the five named pre-existing baseline failures, so `FAIL-BASELINE` is
zero. `I-10.10`'s `PASS` covers its executed test half only; its `-fuzz` half is `NOT-RUN` and is
listed in §4 — the row is counted once, under `PASS`, and the caveat is carried in its Result cell.

## 2. Row table

Artifact paths are all under
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-10/` (abbreviated `…/inv/SP-10/` below).

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-10.1 | Append-only pins, tombstone delete, materialized view; `Remove` writes `{"op":"del"}`, never rewrites | MAPPED | `internal/pins`: `TestPinsRemoveWritesTombstone`, `TestPinsAppendOnlyGuard`, `TestPinsRemoveTwiceIsANoOp`, `TestPinsReaddAfterRemoveAppendsAgain`, `TestPinsMaterializeView`, `TestPinsMaterializeRunsAfterEveryAddAndRemove`, `TestPinsReplaySkipsMalformedLine` (+34 more) | PASS | `…/inv/SP-10/I-10.1.txt` | direct | 41 top-level tests, 0 FAIL, 0 SKIP, 5.80 s. `TestPinsAppendOnlyGuard` is the never-rewrites clause. The plan's proposed commit 1 (pin provenance) is future work and does not change this assertion. |
| I-10.2 | §8.5 schema verbatim, importance order; `eliminated[]` carries all seven keys | MAPPED | `internal/checkpoint`: `TestSchemaFieldOrderIsImportanceOrder`, `TestGoldenCheckpointRoundTrip`, `TestEmptySlicesSerializeAsArrays`, `TestBlockedOnNullIsExplicit`, `TestHashMarshalsAsSha256Prefix`, `TestEliminatedCarriesEverySection85Key` | PASS | `…/inv/SP-10/I-10.2.txt` | direct | All six historical names still exist verbatim; 6/6 PASS. The plan keeps the frozen schema as a compatibility input; T10-COMPAT-01 (future) extends this row with legacy-unknown exposure, it does not replace it. |
| I-10.3 | Versioning/migration: v1 identity, future version → `core.ErrContract` | MAPPED | `internal/checkpoint`: `TestMigrateV1IsIdentity`, `TestMigrateRejectsFutureVersion`, `TestMigrateRejectsMissingVersion`, `TestMigrateRejectsMalformedJSON`, `TestMigrateRejectsVersionBelowOne`, `TestMigrateStepsThroughEveryVersionInOrder`, `TestMigrateWalksAChainLongerThanSixtyFourSteps`, `TestUnmarshalDropsUnknownFields` (12 total) | PASS | `…/inv/SP-10/I-10.3.txt` | direct | 12/12 PASS. T10-COMPAT-02 adds "migration retains identity and rollback evidence" — that added clause has no current test and is future-owner scope (see §5 Q2). |
| I-10.4 | No code snippets in any golden checkpoint (G3.4, §13 inv. 5) | MAPPED | `internal/checkpoint`: `TestGoldenCheckpointsContainNoCodeBlocks` (+ `TestCodeLineHeuristicFires`, `TestCheckpointGolden_ContainsNoCodeBlocks`) | PASS | `…/inv/SP-10/I-10.4.txt` | direct | Subtests enumerate all four files actually present in `testdata/golden/checkpoints/`: `0001-minimal.json`, `0002-full.json`, `0003-truncated.json`, `0004-tier1-over-budget.json`. Directory listed to confirm no golden escapes the walk. |
| I-10.5 | Store-only regeneration (GC rule): every `SourceSet` field an interface, injected text never re-encoded | SUPERSEDED-BY-WAVE4 | `internal/checkpoint`: `TestSourceSetCarriesNoText`, `TestAdvanceStripsInjectionsFromStoredPrompts`, `TestStripInjections_*` (14 total, `-rapid.checks=1000`); GC half now `internal/store`: `TestGC_ReportsPerRootOutcomes`, `TestGC_RetentionRootSourceHoldsObjectsLive` | PASS | `…/inv/SP-10/I-10.5.txt` | direct | The GC rule this row leans on was widened by the wave-4 corrective (V4 map `V4-SP10-06`, via `V4-SP06-05`); the plan restates it as own-injection exclusion with preserved provenance (IS-10-05). The checkpoint-side clauses are unchanged and all pass. |
| I-10.6 | Incremental draft lifecycle; pointers newest-first; superseded/ephemeral excluded; drafts < 4 KB for a 40 KB read | SUPERSEDED-BY-WAVE4 | `internal/checkpoint`: 43 tests incl. `TestBeginResumesPersistedDraft`, `TestAdvanceKeepsPointersNewestFirst`, `TestAdvanceExcludesSupersededAndEphemeralTools`, `TestAdvancePointersCarryNoContent`, `TestAbortIsIdempotent`, `TestSetCurrentWorkSuppressesDerivation`, `TestPackageFunctionsWorkWithoutObservers`; lifecycle ownership now `TestFrontierAdvancer_*` (6 tests) | PASS | `…/inv/SP-10/I-10.6.txt` | direct | 43/43 PASS, 8.83 s. Corrective unit G moved "begin, resume, discard, advance, abort" to the advancer's contract (the scheduler holds only the port) — plan §Lifecycle model / IS-10-04. The 4 KB clause is asserted literally at `internal/checkpoint/writer_test.go:483`: `require.Less(t, len(b), 4096, "a 40 KB read must reach the draft as a pointer, not as content")`. |
| I-10.7 | DPI guard at L4: `errors.Is(err, core.ErrAlreadyEncoded)`, draft still persisted | SUPERSEDED-BY-WAVE4 | `internal/checkpoint`: `TestAdvanceIsDPIGuarded`; new companion `TestFrontierAdvancer_PreservesPartialDPIGuardOutcome` | PASS | `…/inv/SP-10/I-10.7.txt` | direct | Body read at `writer_test.go:398-414`: `require.ErrorIs(t, err, core.ErrAlreadyEncoded)`, `require.Equal(t, core.CheckpointSeq(6), w.Seq, "the draft is still persisted after the DPI error")`, `require.Empty(t, cp.EncodedSegments, …)`, plus "the store's mark is untouched". Wave 4 replaced drop-batch-and-never-re-encode with preserve-the-owner-outcome-without-retry; the row's own three clauses survive verbatim. |
| I-10.8 | `ExtractDecisions` is the only producer of `DecisionID`; explains-edges/eliminations/decision pins mint; cap 64; slice-score rank; `KindDecision` nodes | MAPPED | `internal/checkpoint`: `TestExtractFromEdgeExplains`, `TestExtractFromElimination`, `TestExtractFromDecisionPin`, `TestExtractCapsAtSixtyFour`, `TestExtractRanksBySliceScore`, `TestExtractEmitsDecisionNodesAndEdges`, `TestDecisionIDIsStableAndDeterministic`, `TestExtractDeduplicatesByID` (14 total) | PASS | `…/inv/SP-10/I-10.8.txt` | direct | 14/14 PASS, 7.30 s — one current test per named clause. IS-10-02's "decision extraction remains a candidate until authority rules say otherwise" is a future qualification, not a change to what these assert today. |
| I-10.9 | Importance-ordered truncation (§6.9): tier 3 → tier 2 → never tier 1; monotone in budget; one `DropEntry` per removed element | MAPPED | `internal/checkpoint`: `TestTruncateDropsTierThreeFirst`, `TestTruncateReachesTierTwoOnlyAfterTierThree`, `TestTruncateNeverTouchesTierOne`, `TestTruncateIsMonotone`, `TestTruncateDropEntriesAreComplete`, `TestTruncateConformance`, `TestGoldenTruncationFixtures` (14 total, `-rapid.checks=300`) | PASS | `…/inv/SP-10/I-10.9.txt` | direct | 14/14 PASS. The plan's §Design context now frames importance ordering as a *declared reconstruction policy*, not proof that arbitrary truncation is optimal — a framing change, not an assertion change. The explicit-overflow half of IS-10-06 lives in `TestPromote_OverflowLeavesADropEntry` and, for the assembled payload, in `internal/rehydrate`. |
| I-10.10 | Pointer ground-truth validation (G2.5): missing/dir/escape/dirty/untracked drops; git index v2/v3 parsed, v4 degrades; no fuzz crashers | MAPPED | `internal/checkpoint`: `TestValidatePointersMissingFile`, `…Directory`, `…Escape`, `…DirtyAgainstIndex`, `…DirtyOnMTimeAlone`, `…Untracked`, `…CleanFileProducesNoDrop`, `…NoGitDir`, `…UnreadableHeadDegradesBranchOnly`, `…CancelledContext`, `TestGitIndexV2ParsesEntries`, `TestGitIndexV3ExtendedFlags`, `TestGitIndexV4Unsupported`, `TestGitIndexTruncated`, `TestGitIndexCraftedCorruptions`, `TestGitDirAsFileWorktree` (20 total); `FuzzParseGitIndex` exists | PASS (tests) / NOT-RUN (`-fuzz FuzzParseGitIndex`) | `…/inv/SP-10/I-10.10.txt` | direct | 20/20 PASS. The fuzz half is barred by the run policy (no fuzz runs) — exact command in §4. `FuzzParseGitIndex` confirmed present by `go test -list`; its seed corpus runs as part of the package, only the 60 s fuzzing pass is deferred. |
| I-10.11 | MANIFEST line format, immutability (`0444`), reader chain, verify, parent fallback; a mismatched hash is loud and falls back to the parent | MAPPED (pattern corrected) | `internal/checkpoint`: `TestFinalizeAppendsAManifestLineThatVerifies`, `TestFinalizeWritesAnImmutableArtifact`, `TestGetDetectsManifestMismatch`, `TestLatestFallsBackToParent`, `TestLatestInheritsAnotherSessionsChain`, `TestChainReturnsOldestFirst`, `TestChainRejectsCycle`, `TestVerifyReturnsMismatchedSeqs`, `TestVerifyRejectsMalformedManifestLine`, `TestListRejectsMalformedManifestLine`, `TestListIsAscendingAndCarriesManifestFields`, `TestReaderRefLeavesWriterOnlyFieldsZero`, `TestPublicationFailureRetainsThePreviousCheckpoint`, `TestRollbackToAPriorCheckpointWhenTheNewestIsCorrupt` (36 total) | PASS | `…/inv/SP-10/I-10.11.txt` | direct | 36/36 PASS, 21.88 s. `TestManifestLineFormat` does not exist — see §3. V4 held this row at `SPLIT-REVIEW` because the restart/crash-cut clause was `PENDING-A2`; on this tree the publication-failure and rollback tests exist and pass, so the split resolves. T10-CRASH-01 / T10-ROLLBACK-01 remain unwritten future gates (§5 Q2). |
| I-10.12 | Focus instructions with the O1 incremental span: template byte-identical to §8.5, span names path + turn N, forward slashes, capped at 4 000 bytes | RETIRED | Text assertions survive as `internal/checkpoint`: `TestFocusStandingTemplateVerbatim`, `TestFocusTemplatesAreVerbatimConstants`, `TestFocusIncrementalSpanNamesPathAndTurn`, `TestFocusForwardSlashesOnWindows`, `TestFocusCappedAtFourThousandBytes`, `TestFocusContainsSentinel`, `TestFocusFirstLineIsAProbePhrase` (11 total) | PASS | `…/inv/SP-10/I-10.12.txt` | direct | 11/11 PASS. RETIRED for the *native* O1-span claim only: plan Mission ¶5 withdraws the native O(delta) claim and §Host boundary correction says Qompack cannot promise to alter host history or compaction boundaries. The span is local draft progress; replacement gate T10-HOST-01 (revised from `TestFocusContainsSentinel`). |
| I-10.13 | Import discipline: `hookio`, `scheduler`, `ipc`, `daemon`, `os/exec`, `net` all absent from `internal/checkpoint` | MAPPED | `internal/checkpoint`: `TestNoForbiddenImports` (+ `TestImportViolationClassifier`) | PASS | `…/inv/SP-10/I-10.13.txt` | direct | 1/1 PASS, 0.01 s. |
| I-10.14 | `PreCompact` incl. near-deadline finalize: cold path still writes a checkpoint; returns within 600 ms with a valid truncated artifact | RETIRED | `internal/checkpoint`: `TestPreCompactSealsACheckpointAndReturnsInstructions`, `TestPreCompactFinalizesEvenWhenTheDeadlineHasPassed`, `TestPreCompactOnAColdSessionBeginsAndSeals`, `TestColdPreCompactSealsEvenWhenSegmentsCannotBeListed`, `TestColdPreCompactCatchesUpInOneBatchOldestFirst`, `TestPreCompactDerivesItsBudgetFromTheCallersDeadline`, `TestPreCompactReportsTruncationButNotPointerDrops` (11 total) | PASS | `…/inv/SP-10/I-10.14.txt` | direct | 11/11 PASS, 3.67 s. RETIRED for the output-setter semantics: plan §Host boundary correction (Evidence E10) says PreCompact receives `custom_instructions` as *input* and SP-10 must not set a `customInstructions` output field. The near-deadline finalize stays assertable and passes. The literal "600 ms" is gone — see §3. Replacement gate T10-LIFE-01. |
| I-10.15 | Scheduler-cadence checkpoints gated by degradation: finalize at budget and at 8 segments; no checkpoint in `ModeDegradedPassive`, `advance_frontier` still runs | SUPERSEDED-BY-WAVE4 | `internal/daemon`: `TestCadenceFinalizesWhenDraftReachesBudget` (two rows: "the draft prices at or above the budget" and "the draft absorbed eight segments while still under budget", the latter driven by `cadenceSegmentThreshold`), `TestCadenceSealsNothingWhenNeitherConditionHolds`, `TestACancelledContextStopsTheCadenceLoop`; degraded clause `test/e2e`: `TestE2E_CheckpointDegradedPassiveSealsNothing` | PASS | `…/inv/SP-10/I-10.15.txt`, `…/inv/SP-10/I-10.16.txt` | direct | 3/3 PASS in `internal/daemon`. Corrective unit K moved cadence out of `internal/checkpoint`, so the historical command targeted the wrong package (§3). **This updates the V4 adjudication:** V4 recorded `TestCadenceFinalizesAfterEightSegments` as having *no successor* / MISSING; on this tree the 8-segment condition is a named subtest of `TestCadenceFinalizesWhenDraftReachesBudget` at `internal/daemon/wire_checkpoint_test.go:240`, and the two cadence conditions are deliberately separated so neither row can pass on the other's strength. The degraded e2e asserts both halves: `require.Subset(t, ran, []string{"advance_frontier", "materialize_pins"})`, `require.NotContains(t, ran, "act.checkpoint_cadence")`, `require.Empty(t, cpCheckpointArtifacts(...), "no idle tick may seal an artifact in ModeDegradedPassive")`. |
| I-10.16 | Checkpoint e2e through the real hook: exits 0; `customInstructions` present in `full`, absent in `degraded-passive`; `0001.json` read-only and manifest-consistent | MAPPED | `test/e2e`: `TestE2E_CheckpointHookWritesImmutableArtifact`, `TestE2E_CheckpointDegradedPassiveSealsNothing` | PASS | `…/inv/SP-10/I-10.16.txt` | direct | 2/2 PASS, 24.03 s. Read directly at `test/e2e/checkpoint_test.go:481-560`: `require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(...))`, `require.Zero(t, fi.Mode().Perm()&0o222, "…0o444 (FILE_ATTRIBUTE_READONLY on Windows)…")`, `require.Equal(t, core.Hash(sum).String(), entries[0].SHA256, …)`, and the artifact re-parsed through `checkpoint.Unmarshal`. **The `customInstructions` clause is in direct tension with the plan's §Host boundary correction — see §5 Q1.** |
| I-10.17 | Checkpoint benchmark budgets: `Finalize` < 50 ms, `AdvanceSegment` < 25 ms, `Truncate` < 5 ms, `ExtractDecisions` < 20 ms, `StripInjections` < 2 ms | MAPPED | `internal/checkpoint`: `BenchmarkFinalize`, `BenchmarkAdvanceSegment`, `BenchmarkTruncate`, `BenchmarkExtractDecisions`, `BenchmarkStripInjections` | FAIL-COLOAD-SUSPECT | `…/inv/SP-10/I-10.17.txt` | direct | One run, co-loaded machine. Measured: `Finalize` **560.36 ms/op** (budget 50 ms, **breach**), `AdvanceSegment` **42.02 ms/op** (budget 25 ms, **breach**), `ExtractDecisions` **22.53 ms/op** (budget 20 ms, **marginal breach**), `Truncate` 2.59 ms/op (within budget), `StripInjections` 0.596 ms/op at 439 MB/s (within budget). `Finalize` is additionally the **open carried defect `SP10-D1`** (`plans/CARRIED-DEFECTS.tsv`: 264–284 ms/op on Windows, 88 % of it `paths.Norm` resolving one file pointer per call, reference platform never measured) — i.e. that clause is pre-existing regardless of load, and today's 560 ms is roughly 2× the recorded figure. Quiet re-run in §4; ownership question in §5 Q3. The whole run took 154 s, over the one-minute guidance; noted, not repeated. |
| I-10.18 | SP-10's own gate: `devtool replay` with `checkpoint.frontier.advanceOnSegmentClose` off vs on; `on.ResidualSpan.P50 ≤ 0.70 × off.ResidualSpan.P50`, no divergence metric regressing > 2 % | RETIRED | Replacements: `internal/checkpoint`: `TestFrontierAdvanceCutsResidualSpan`; `test/replay`: `TestV4_FrontierToggleIsNotConsumedByTheReplayPath` | NOT-RUN (historical `devtool replay` command; RETIRED) — replacement evidence PASS | `…/inv/SP-10/I-10.18.txt`, `…/inv/SP-10/I-10.18b.txt` | direct | The historical A/B is **provably vacuous on this tree**, and that is now itself a test. `TestV4_FrontierToggleIsNotConsumedByTheReplayPath` replays the corpus twice differing *only* in the toggle (field-by-field config comparison proves non-vacuity) and asserts both arms are byte-identical: PASS, 1.03 s, e.g. `qompack-l3 residual_span_p50 on=164443 off=164443`. Reason, from `test/replay/v4_x04_test.go:24-38`: the toggle is read in three places, all in `internal/daemon`; `internal/checkpoint` never consults it; the replay driver's residual span is `prefixTokens − KeepSet.P`, i.e. where the *policy* cut, which is not a frontier. The quantity the row wanted is measured instead by `TestFrontierAdvanceCutsResidualSpan` over a real store, SegmentLog and Finalize: PASS, `residual span: on=10 off=60 ratio=0.1667` — comfortably inside the historical `≤ 0.70`. Plan Mission ¶5 withdraws the underlying native-O(delta)/first-turn claim; V4 map `V4-SP10-21` retired the same row. |

## 3. Old-to-new assertion map

### 3.1 Corrected patterns (a historical name that selects nothing today)

Each was found with `go test -list`; the replacement was found by reading the package's `*_test.go`
files.

| Row | Historical name | Selects | Replacement on this tree | Reason |
|---|---|---|---|---|
| I-10.11 | `TestManifestLineFormat` | **nothing** | `TestFinalizeAppendsAManifestLineThatVerifies` (shape and verifiability in one), with `TestListRejectsMalformedManifestLine` and `TestVerifyRejectsMalformedManifestLine` for the reject side | The manifest line is no longer asserted as a standalone format string; it is asserted as the round trip a real `Finalize` appends and a real reader re-verifies. Corrective commit `926070d` plus `internal/checkpoint/publication_test.go` carry the manifest/verify/parent-fallback clauses. Everything else in the row's `-run` alternation (`TestFinalize`, `TestGetDetectsManifestMismatch`, `TestLatest`, `TestChain`, `TestVerify`, `TestList`, `TestReaderRef`) still selects. |
| I-10.14 | `TestPreCompactFinalizesAsIsNearDeadline` (the plan's own T10-LIFE-01 source name) | **nothing** | `TestPreCompactFinalizesEvenWhenTheDeadlineHasPassed` | Renamed. The stem `TestPreCompact` in the row's command still selects it, so the row's command remains runnable as written. |
| I-10.14 | "returns within **600 ms**" | no test asserts a 600 ms wall clock | `TestPreCompactDerivesItsBudgetFromTheCallersDeadline` — the budget is `Deadline.Sub(Now) − finalizeGuard`, clamped into `[minFinalizeWindow, maxPreCompact]`, with `require.True(t, f.pins.hasDeadline, "PreCompact must install a deadline of its own")` | The wall-clock number was replaced by a *structural* assertion over the caller's deadline, which is the right shape for a co-loaded machine: there is no longer a timing gate to be flaky. Recorded as a strengthening, not a gap. |
| I-10.15 | `internal/checkpoint -run 'TestCadence'` | **nothing in `internal/checkpoint`** | `internal/daemon -run 'TestCadence'` → `TestCadenceFinalizesWhenDraftReachesBudget`, `TestCadenceSealsNothingWhenNeitherConditionHolds`, `TestACancelledContextStopsTheCadenceLoop` | Corrective unit K moved cadence to `internal/daemon/wire_checkpoint.go` / `wire_checkpoint_test.go`. Running the historical command verbatim would have printed `ok` while selecting nothing. |
| I-10.15 | `TestCadenceFinalizesAfterEightSegments` | **nothing** | subtest `TestCadenceFinalizesWhenDraftReachesBudget/the draft absorbed eight segments while still under budget`, driven by `cadenceSegmentThreshold` | **Supersedes the V4 adjudication**, which recorded this clause as having no successor and therefore MISSING. It has one now, and the two §8.5 cadence conditions are deliberately separated so each row makes the *other* condition false. |
| I-10.16 | `TestE2E_Checkpoint` (stem) | two tests | `TestE2E_CheckpointHookWritesImmutableArtifact` + `TestE2E_CheckpointDegradedPassiveSealsNothing` | Stem still selects both; recorded so the row's two halves are attributable. |
| I-10.18 | `TestV4_FrontierAdvancementKeepsResidualSpanODelta`, named as V4-VERIFY §4.4's discharge at `test/replay/phase4_test.go:73` | **nothing, anywhere in the tree** | `TestFrontierAdvanceCutsResidualSpan` (`internal/checkpoint`) for the measurement; `TestV4_FrontierToggleIsNotConsumedByTheReplayPath` (`test/replay`) as the standing negative control | The §4.4 scenario was never authored, and `test/replay/v4_x04_test.go:20-38` is the written record of *why*. See §5 Q4: a live constant still points at a test that does not exist. |

### 3.2 RETIRED rows

| Row | Reason | Replacement pointer |
|---|---|---|
| I-10.12 | Plan Mission ¶5 withdraws the native O(delta) and guaranteed first-turn-saving claims; §Host boundary correction states Qompack "cannot promise to alter host instructions, history, compaction boundaries, cache markers, or delivered results". The *native* O1 incremental span is therefore not claimable. | The focus text itself is unchanged and fully asserted: `internal/checkpoint/focus_test.go` (`TestFocusStandingTemplateVerbatim`, `TestFocusIncrementalSpanNamesPathAndTurn`, `TestFocusForwardSlashesOnWindows`, `TestFocusCappedAtFourThousandBytes`). Future gate **T10-HOST-01**, revised from `TestFocusContainsSentinel`. Consistent with V4 map `V4-SP10-13`. |
| I-10.14 | Plan §Host boundary correction, from Evidence E10 in `plans/MIGRATION-EVIDENCE.md`: PreCompact *receives* `custom_instructions` as input; SP-10 "must not set a `customInstructions` output field or depend on an unsupported output setter". The row's PreCompact semantics are retired to that extent. | The bounded near-deadline finalize survives and passes: `TestPreCompactFinalizesEvenWhenTheDeadlineHasPassed`, `TestPreCompactDerivesItsBudgetFromTheCallersDeadline`, `TestPreCompactOnAColdSessionBeginsAndSeals`. Future gate **T10-LIFE-01**. Consistent with V4 map `V4-SP10-15`. |
| I-10.18 | Two independent reasons, both verified here: (a) plan Mission ¶5 withdraws the claim the gate was written to prove; (b) the experiment is mechanically vacuous — the toggle is not an input to the replay path, so both arms produce byte-identical metrics (executed; see §2). | `internal/checkpoint`: `TestFrontierAdvanceCutsResidualSpan` (real store / SegmentLog / Finalize; measured `on=10 off=60`, ratio 0.167) and `test/replay`: `TestV4_FrontierToggleIsNotConsumedByTheReplayPath` (negative control that fails loudly if the toggle ever *does* reach replay, which is the trigger to author §4.4). Consistent with V4 map `V4-SP10-21`. |

### 3.3 SUPERSEDED-BY-WAVE4 rows

The requirement itself moved; each row's own clauses still pass.

| Row | What wave 4 changed | New assertion | Plan section |
|---|---|---|---|
| I-10.5 | The GC / store-only regeneration rule was widened by the corrective units B/C/M. | `internal/store`: `TestGC_ReportsPerRootOutcomes`, `TestGC_RetentionRootSourceHoldsObjectsLive`, alongside the unchanged `internal/checkpoint`: `TestSourceSetCarriesNoText`. | IS-10-05 (own-injection exclusion); §Required invariants 1 and 4. |
| I-10.6 | Unit G (`02f807a`) gave draft lifecycle to the frontier advancer; the scheduler stores only the port and never calls `Begin`/`Abort`. | `internal/checkpoint`: `TestFrontierAdvancer_ReusesFileWriterLiveDraft`, `TestFrontierAdvancer_RetriesOneSealedDraft`, `TestFrontierAdvancer_ReportsRepeatedSealedDraft`, `TestFrontierAdvancer_CancellationAndEmptyBatchDoNotBegin`, `TestFrontierAdvancer_CancellationBetweenLifecycleCalls`. | IS-10-04 (bounded lifecycle); §Lifecycle model. |
| I-10.7 | Drop-batch-and-never-re-encode became preserve-the-owner-outcome-without-retry. | `internal/checkpoint`: `TestFrontierAdvancer_PreservesPartialDPIGuardOutcome`, with `TestAdvanceIsDPIGuarded` unchanged. | IS-10-02 / IS-10-05; §Required invariant 4. Future gate T10-DPI-01. |
| I-10.15 | Unit K moved cadence from `internal/checkpoint` to `internal/daemon`; the eight-segment condition became a named subtest. | `internal/daemon`: `TestCadenceFinalizesWhenDraftReachesBudget` (+2), `test/e2e`: `TestE2E_CheckpointDegradedPassiveSealsNothing`. | IS-10-08 (maintenance, crash, rollback); future gate T10-MAINT-01, revised from `TestCadenceFinalizesWhenDraftReachesBudget`. |

## 4. Deferred to coordinator

Every command below is quoted so it can be pasted and run serially on a quiet machine from
`C:/Users/Quant/Documents/Programming/Projects/qompack-v5`.

| Row | Why deferred | Exact command |
|---|---|---|
| I-10.10 (fuzz half) | Fuzz runs are barred by this pass's run policy. `FuzzParseGitIndex` is confirmed present; only the 60 s fuzzing pass is outstanding. | `go test -count=1 ./internal/checkpoint/ -run '^$' -fuzz FuzzParseGitIndex -fuzztime 60s` |
| I-10.17 | Timing/budget row, measured once on a co-loaded machine; three of five budgets breached. Needs a quiet-machine re-measurement before any of the three breaches is called real. | `go test -count=1 ./internal/checkpoint/ -run '^$' -bench . -benchtime 2s -timeout 10m` |
| I-10.18 | Historical `devtool replay` gate, RETIRED and shown vacuous here. Recorded so the coordinator can confirm the vacuity independently rather than take this report's word for it — expect the two JSON reports to carry identical residual-span figures. | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=false --json ./v5-frontier-off.json` then the same command with `=true --json ./v5-frontier-on.json` |

Not one of this SP's 18 rows, but adjacent and barred here: the V4 map's `V4-SP10-01` package gate
`go test -race -count=2 ./internal/checkpoint/... ./internal/pins/...` was **not** run (race runs are
barred). Both packages are green under a plain `-count=1` run in this report.

## 5. Questions

**Q1 — `customInstructions` is asserted by a passing e2e test and forbidden by the owner plan.**
`plans/V4-SP-10-checkpointer-l4.md` §Host boundary correction says, citing Evidence E10: "SP-10 must
not set a `customInstructions` output field or depend on an unsupported output setter." The shipped
code does set it, and `test/e2e/checkpoint_test.go:496` asserts it:
`require.NotEmpty(t, instr, "the full-mode PreCompact path must emit customInstructions")` — row
I-10.16, PASS. V4 already retired the unit-level PreCompact row (`V4-SP10-15`) on exactly this ground
but left the e2e row MAPPED. Which is authoritative for V5: is I-10.16's `customInstructions` clause
RETIRED with the plan and the e2e assertion scheduled for removal, or is the plan text describing a
correction that has not landed, so the assertion stays? I have not changed the disposition either
way; it is recorded MAPPED/PASS as executed.

**Q2 — do the unwritten `T10-*` gates become V5 inventory rows?** The owner plan's Test plan table
lists twenty future gates. Eight are "revised from" a test that exists and passes today
(T10-COMPAT-01/02, T10-EVIDENCE-01, T10-DPI-01, T10-LIFE-01, T10-HOST-01, T10-POINTER-01,
T10-MAINT-01) and are covered by the rows above. Twelve have **no current definition anywhere**:
T10-FRONTIER-01, T10-FRONTIER-02, T10-LIFE-02, T10-LIFE-03, T10-LIFE-04, T10-HOST-02,
T10-ACCOUNT-01, T10-ACCOUNT-02, T10-COVER-01, T10-POINTER-02, T10-CRASH-01, T10-ROLLBACK-01 — as do
the twelve IS-10-09 mandatory lifecycle scenarios and all eleven Exit criteria, which the plan itself
marks "All criteria are future and unchecked". None of the 18 historical rows I own asserts them, so
I raised no `MISSING` rows. Should V5-VERIFY open them as new MISSING rows against
`internal/checkpoint` / `internal/daemon`, or do they stay outside V5 as future-owner scope pending
V10-01 … V10-07?

**Q3 — who owns `SP10-D1` now?** `plans/CARRIED-DEFECTS.tsv` row `SP10-D1` has `owner = V4-VERIFY`,
`status = open`, evidence `BenchmarkFinalize`. V4-VERIFY is closed. Today's measurement is 560 ms/op
against a 50 ms budget — about double the 264–284 ms the row records, on a co-loaded host. Does V5
re-own the row, or is it consciously re-deferred to a later checkpoint (the TSV header requires a
`deferred:<checkpoint>` naming a checkpoint that has a `plans/<checkpoint>-*.md` document)? Note that
`test/guards/carrieddefects_test.go` is one of the five named pre-existing failures on this tree, so
the guard that would otherwise force this decision is not currently green.

**Q4 — a live constant names a test that does not exist.**
`test/replay/phase4_test.go:73` declares
`p4DischargedBy = "V4-VERIFY §4.4 TestV4_FrontierAdvancementKeepsResidualSpanODelta"`, and
`test/replay/phase4_test.go:47-50` says the §10 Phase 4 amortization clause "is discharged by" that
test. `grep` finds the name in exactly two files, both as prose — `phase4_test.go` and
`v4_x04_test.go` — and no `func` of that name exists. The measurement the constant wants is really
`internal/checkpoint`'s `TestFrontierAdvanceCutsResidualSpan`, which `v4_x04_test.go:166` itself
names as the stand-in. Should `p4DischargedBy` be re-pointed at the test that actually measures it,
or does V4-VERIFY §4.4 stay open with the constant as its marker? Read-only pass: I changed nothing.

**Q5 — `AdvanceSegment` and `ExtractDecisions` have no carried-defect row.** Unlike `Finalize`, the
other two breaches measured in I-10.17 (42.02 ms against a 25 ms budget; 22.53 ms against 20 ms) are
not recorded in `plans/CARRIED-DEFECTS.tsv`. If the quiet re-run in §4 reproduces them, they need
either a budget revision or two new carried-defect rows; if it does not, I-10.17 is co-load noise for
those two and `Finalize` alone stands. I could not tell which from this tree, so no row was opened.

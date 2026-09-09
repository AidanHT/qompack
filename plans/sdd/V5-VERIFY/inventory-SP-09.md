# V5-VERIFY inventory — SP-09 (negative knowledge)

**Subplan:** SP-09, negative knowledge (`internal/negknow`, its conformance suite, the elimination
e2e and the Phase-2 replay gate).
**Rows:** the 14 historical rows `I-09.1`–`I-09.14`, taken verbatim from the plan at `7f92af5`.
**Owner plan sections consulted:** `plans/V4-SP-20-capture-storage-and-state-remediation.md`
(test plan `T20-M2-01`, `T20-M2-02`; commit units 6 and 7), `plans/V4-SP-13-mcp-retrieval-layer.md`
(`T13-STATE`, the `already_tried` / `record_eliminated` / `why` tool rows, and the SP-13
trust/old-caller blocker), `plans/sdd/V4-VERIFY/reconciliation-map.md` rows `V4-SP09-01`–`09`,
`plans/CARRIED-DEFECTS.tsv` (no `SP09-*` row exists; no carried defect is owned by this subplan).
**Tree:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @ `87c0c1d`
("chore(sp21): integrate deterministic admission control" — develop with wave 4 integrated).
**Platform:** Windows 11, go1.26.6 windows/amd64. Machine shared with other verification children
throughout.
**Date:** 2026-09-08.

**Dispositions:** MAPPED 13 · MAPPED-CMD 0 · SUPERSEDED-BY-WAVE4 1 · RETIRED 0 · MISSING 0 ·
NEEDS-COORDINATOR 0 (total 14).
**Results:** PASS 13 · FAIL 0 · FAIL-BASELINE 1 · FAIL-COLOAD-SUSPECT 0 · SKIP 0 · NOT-RUN 0
(total 14). Two *commands* inside otherwise-executed rows were not run and are deferred (§4);
neither row was scored on them.

Every `-run` pattern in the 14 rows was first confirmed with `go test -list <pattern>
./internal/negknow/` (and `./test/e2e/`, `./test/replay/`): **all 14 patterns still select tests on
this tree — no pattern needed correction.** Selection counts are in
`.../inv/SP-09/list-negknow.txt`. The reconciliation target's three retained properties were
checked directly and all three hold: reason-independent identity (`TestMatchKey_IgnoresReason`,
`TestDescriptorKey_DifferentReasonHashDiffers`), exact confirmation retained (`TestQuery_Active`,
`TestRecord_Idempotent*` identity dedup), and errors-not-absence (`TestAnswerMCPResult`,
`TestBlindMode`, and `internal/mcp`'s `TestAlreadyTriedQueryErrorsReturnUnavailable`).

Artifacts live under
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-09/` (abbreviated `.../inv/SP-09/` below).

## 2. Row table

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-09.1 | Approach-class canonicalization: deterministic, bounded, `"unclassified"` for empties | MAPPED | `internal/negknow`: `TestApproachClass_Table`, `_Deterministic`, `_Bounded`, `_Empty`, `_SynonymFixpoint`, `_SilentERestore`, `_DoubledConsonantLimitation`, `TestLemma` (8 selected) | PASS | `.../inv/SP-09/I-09.1.txt` | direct | Run with `-rapid.checks=1000`; the flag is defined (rapid is imported by `classify_test.go`). 0.26 s. |
| I-09.2 | Four-field canonical descriptor (§8.3); golden `key_hex`/`match_key_hex` byte-stable | MAPPED | `internal/negknow`: `TestSplitTarget_Table`, `TestDescriptorKey_Golden`, `_Stable`, `_DomainSeparatesAdjacentFields`, `_DifferentReasonHashDiffers`, `TestMatchKey_IgnoresReason`, `_SeparatorInjection`, `TestKey_Length`, `TestKey_NoAliasing`, `TestReasonHash_Normalization`, `TestDescriptorJSON_*` (9 selected) | PASS | `.../inv/SP-09/I-09.2.txt` | direct | This row carries the reconciliation target's **reason-independent identity**: `TestMatchKey_IgnoresReason` still holds on this tree, untouched by wave 4. |
| I-09.3 | `Record` is the §8.5 `eliminated[]` slot; `record.golden.json` byte-equal, seven keys | MAPPED | `internal/negknow`: `TestRecordJSON_Golden`, `_RoundTrip`, `_MissingFields`, `_NilDependsOn`, `_BadDepHash`, `_UnparseableEvidence`, `TestNormalizeRecord_Bounds`, `_DepsDedupedAndSorted`, `_Redact`, `TestRecordID_Stable`, `TestSourceKind_RoundTrip`, `_WireFormIsInteger` (12 selected) | PASS | `.../inv/SP-09/I-09.3.txt` | direct | Run with `-rapid.checks=500`. The §8.5 slot is additionally pinned by `TestRecord_MatchesCheckpointFixtureEliminatedEntry` (executed under I-09.6). |
| I-09.4 | Append-only ledger log with recovery; corrupt/orphan/duplicate counted, never fatal | MAPPED | `internal/negknow`: `TestReplayLog_Corrupt`, `_OrphanStale`, `_DuplicateAdd`, `_TruncatedTail`, `_BareRecordLine`, `_EmptyIDIsCorrupt`, `_Golden`, `_SinglePassRecovery`, `_ReadErrorIsReturned`, `TestAppendOnly_Enforced`, `TestAppendLine_NoInteriorNewline` (11 selected) | PASS | `.../inv/SP-09/I-09.4.txt` | direct | — |
| I-09.5 | **Three-way** `already_tried` answer; `StaleNote` byte-identical incl. em dash; session scope hidden across sessions | SUPERSEDED-BY-WAVE4 | New assertion: a **five-state closed enum** (absent / active / stale / **unavailable** / **uncertain**), errors and degraded coverage never rendered as absence — `internal/negknow`: `TestQuery_Stale_Drop`, `TestQuery_UnknownStatusIsBloomOnly`, `TestAnswerMCPResult`, `TestBlindMode`, `TestThreeWayAnswerFixture_PinsEachState`; `internal/mcp`: `TestAlreadyTriedLedgerFailureReturnsUnavailable`, `TestAlreadyTriedQueryErrorsReturnUnavailable`, `TestAlreadyTriedRendersUnavailableCoverage`, `TestAlreadyTriedRendersUncertainCoverage`. Retained clauses: `TestQuery_Stale_FlagNote` (em dash, literal), `TestQuery_ScopeSession_OtherSessionHidden`, `_ScopeProject_ExcludesSessionScoped` | PASS | `.../inv/SP-09/I-09.5.txt`, `.../inv/SP-09/I-09.5-mcp.txt` | direct | Plan sections: SP-20 `T20-M2-02` ("uncertain/unavailable response, never active") and SP-13 `T13-STATE` ("unavailable query errors"; "unavailable reader remains explicit"). 13 `TestQuery_*` selected, all PASS; 10 `TestAlreadyTried*` in `internal/mcp` all PASS. See §3. |
| I-09.6 | Recording: evidence requirement, idempotence, re-record after stale (`-race`) | MAPPED | `internal/negknow`: `TestRecord_RequireEvidence`, `_EvidenceOptional`, `_Idempotent`, `_IdempotentAcrossTimestamps`, `_ReRecordAfterStaleIsNotDeduped`, `_RejectsPresetStale`, `_DescriptorRecomputed`, `_RedactsBeforeCanonicalizing`, `_RoundTripsFrozenEliminationFixture`, `_MatchesCheckpointFixtureEliminatedEntry`, `TestGetActiveAll`, `TestTopActive`, `TestAnswerMCPResult`, `TestBlindMode`, `TestClose_Idempotent`, `TestClosedLedgerRejectsWrites` (16 selected) | PASS | `.../inv/SP-09/I-09.6-norace.txt` | direct | **Run without `-race`** — race runs are outside this seat's mandate. Both named tests (`TestRecord_IdempotentAcrossTimestamps`, `TestRecord_ReRecordAfterStaleIsNotDeduped`) executed and passed. The `-race` variant is deferred (§4); the row is **not** scored on it. |
| I-09.7 | Staleness flip from `store.ChangedSince`: exactly one call, deduped sorted deps; `Open` < 500 ms | MAPPED | `internal/negknow`: `TestRefreshStaleness_SingleChangedSinceCall` (asserts `Len(calls)==1`, dedup to 2 000 distinct deps, path-then-hash ascending), `_Flips`, `_MultiDep`, `_NoChange`, `_NilStore`, `_StoreError`, `_StoreErrorRecovers`, `_GroupsByBecause`, `TestMarkStale_Idempotent`, `_UnknownID`, `TestRebuildOnStale_Immediate`, `_NextIdle`, `_Never`, `TestMaintenanceTask_Shape`, `TestOpenRefreshBounded` (15 selected) | PASS | `.../inv/SP-09/I-09.7.txt` | direct | Timing clause `TestOpenRefreshBounded` passed in 0.29 s under co-load — no quiet re-run needed. `TestMaintenanceTask_Shape` is the scheduler-idle-task form V4 recorded as `V4-SP09-04`, unchanged by wave 4. |
| I-09.8 | Bloom rebuilt from active records only; one `sketch.RebuildBloom` call site; no `internal/checkpoint` import | MAPPED | `internal/negknow`: `TestRebuildBloom_NeverFromCheckpoint` (asserts the single non-test call site is `internal/negknow/bloom.go`, that `bloom.go` mentions no checkpoint, and that the package never imports `internal/checkpoint`), `_ActiveOnly`, `_ExcludesForeignSession`, `_Persistence`, `_PersistenceFailureReseedsSeq`, `_Resizes`, `_OneBakGeneration`, `_SeqResumesFromDisk`, `_HonoursConfiguredCapacity`, `_UsesSanctionedDoor`, `TestBloomLoadFailure_NoRecords_NeverFalsePositive`, `_RebuildsFromRecords`, `TestBloomUndercount_TriggersRebuild`, `TestBloomOvercount_SchedulesRebuild`, `TestBloomFileSize`, `TestHealth`, `TestHealth_BlindIsZeroed` (17 selected) | PASS | `.../inv/SP-09/I-09.8.txt` | direct | Both specially named tests are present and passed. |
| I-09.9 | Four ingestion sources; auto-dep ordering; unknown deps warn and are skipped | MAPPED | `internal/negknow`: `TestIngestMCP_Full`, `_AutoDeps`, `_UnknownDepSkipped`, `_BadScope`, `_NoStore_RequireEvidence`, `TestIngestPin_EvidenceText`, `_BadEvidenceText`, `TestIngestUserStatement_Matches`, `_NoMatch`, `_NoTarget`, `_ApostropheVariants` (11 selected) | PASS | `.../inv/SP-09/I-09.9.txt` | direct | The fourth (heuristic) source is covered by I-09.10's detector row. |
| I-09.10 | Heuristic detector: window, same-class suppression, DAG deps, `Scan` never appends | MAPPED | `internal/negknow`: `TestDetector_PatternP`, `_WindowExceeded`, `_SameClassNoEmit`, `_DepsFromDAG`, `_DepsFallback`, `_DoesNotAppend`, `_NoRevertNoEmit`, `_NoEvidenceDropped`, `_OneEmissionPerEdit`, `_Since`, `_TestPassBreaksPattern`, `_TerminatesOnCyclicGraph` (12 selected) | PASS | `.../inv/SP-09/I-09.10.txt` | direct | — |
| I-09.11 | Ledger conformance and node IDs; **zero `t.Skip` in `negknowtest`** | MAPPED | `internal/negknow`: `TestLedgerConformance`, `TestNodeIDGolden`, `TestOpenReturnsMaintainer`, `TestOpenReturnsObservationSource` (4 selected); `internal/negknow/negknowtest`: `TestRunLedgerSuite_AgainstQompackOpen` (all six behaviour cases pass), `TestRunLedgerSuite_StubIsSkipped` | PASS | `.../inv/SP-09/I-09.11.txt`, `.../inv/SP-09/I-09.11-negknowtest.txt` | direct | The `t.Skip` clause is clause-level **retired** — see §3. Zero skips in the `internal/negknow` run; the single skip in the `negknowtest` package is `TestRunLedgerSuite_StubIsSkipped`, whose skip *is* its assertion (reason: `behaviour: implementation is a stub (Rule W-1)`), and its shape subtest still passes. |
| I-09.12 | Elimination lifecycle e2e: active → durable → stale → rebuilt bloom → still stale; exactly one `.bak` | MAPPED | `test/e2e`: `TestE2E_EliminationLifecycle` (2.42 s), `TestE2E_BloomCorruptionRecovery` (5.52 s) | PASS | `.../inv/SP-09/I-09.12.txt` | direct | Filtered `-run` only; no unfiltered `./test/e2e` run was made. |
| I-09.13 | Phase-2 exit criterion: `stockRepeats > 0`, `negknowRepeats ≤ 0.75 × stock`, ≥ 8 sessions improved, `staleBlocks == 0` | MAPPED | `test/replay`: `TestPhase2ExitCriterion` | PASS | `.../inv/SP-09/I-09.13.txt` | direct | Measured on this tree: `stock_repeats 124`, `negknow_repeats 35` (28.2 % of stock, ceiling 75 %), `stale_blocks 0`, `sessions 24`, `dependency_change_sessions 6`; 24 of 24 per-session rows improve or hold, and every row — including all six dependency-change sessions — reports `staleBlocks=0`. 4.76 s. |
| I-09.14 | negknow budgets: every `TestBudget_*` PASS (plus `-bench . -benchtime 2s`) | MAPPED | `internal/negknow`: `TestBudget_Open`, `_QueryHit`, `_QueryMiss`, `_Record`, `_RebuildBloom`, `_RefreshStaleness`, `_DetectorScan` (7 selected) | FAIL-BASELINE | `.../inv/SP-09/I-09.14.txt` | direct | 6 of 7 pass. `TestBudget_Open` fails at **322.92 ms CPU/op against the §11.2 300 ms budget** (three gated-clock retries: 364.6 / 322.9 / 324.2 ms CPU; best wall 315.8 ms) — the pre-existing host failure named in the brief, not a wave-4 regression. `TestBudget_DetectorScan` **passed** here (3.91 ms CPU vs a 5 ms budget), so its historically recorded pre-existing failure did not reproduce this run. The `-bench` half of the row was not run (§4). |

## 3. Old-to-new assertion map

No `-run` pattern required correction: all 14 historical patterns still select tests on this tree
(verified with `go test -list`; see `.../inv/SP-09/list-negknow.txt`, `list-e2e.txt`,
`list-replay.txt`). Two assertion-level changes:

| Row | Old assertion | New assertion | Reason / replacement pointer |
|---|---|---|---|
| I-09.5 | "**Three-way** `already_tried` answer: `AnswerAbsent` / `AnswerActive` / `AnswerStale`" | Five-state closed enum — `AnswerAbsent` / `AnswerActive` / `AnswerStale` / `AnswerUnavailable` / `AnswerUncertain` — with a `Coverage` field explaining *why* the last two occurred. A ledger that cannot be consulted (blind mode, query error) answers `AnswerUnavailable`; one whose coverage or freshness cannot be established answers `AnswerUncertain`; **neither is ever rendered as absence, and neither is ever rendered as active.** | The requirement itself changed: `internal/negknow/answer.go` (`5935015` added the two states, `aa073fa` "stop reporting absent for degraded answers", `0b530fb` "degrade coverage on staleness refresh failure"). The plan sections that own it now are SP-20 `T20-M2-02` (unknown-dependency coverage, stale filter watermark, rebuild/query failure → "uncertain/unavailable response, never active") and SP-13 `T13-STATE` (`already_tried` "unavailable query errors"; `why` "unavailable reader remains explicit"); consumer-side edits are SP-13's alone per SP-20 commit 7 ("SP-13 exclusively owns MCP consumer edits"). Replacements: `internal/negknow` `TestQuery_Stale_Drop`, `TestQuery_UnknownStatusIsBloomOnly`, `TestAnswerMCPResult`, `TestBlindMode`, `TestThreeWayAnswerFixture_PinsEachState`; `internal/mcp` `TestAlreadyTriedLedgerFailureReturnsUnavailable`, `TestAlreadyTriedQueryErrorsReturnUnavailable`, `TestAlreadyTriedRendersUnavailableCoverage`, `TestAlreadyTriedRendersUncertainCoverage`. The V4 map recorded the same change as `V4-SP09-03` SUPERSEDED-BY-CORRECTIVE; it is re-confirmed here **by execution**, which V4 never performed. The three-way *fixture* name survives (`TestThreeWayAnswerFixture_PinsEachState`) as a historical name, not a claim that only three states exist. |
| I-09.11 (clause) | "**zero `t.Skip` in `negknowtest`**" | "Zero skips when the suite runs against the real ledger; exactly one guarded skip **site** remains (`skipIfStub` → `ruleW1SkipMsg`), firing only for a factory that still produces an `ErrNotImplemented` stub." | Retired as literally stated. `internal/negknow/negknowtest/suite.go:20,150-165` deliberately keeps the Rule W-1 skip so the suite stays runnable against a stub seam, and `suite_test.go`'s `TestRunLedgerSuite_StubIsSkipped` **asserts that it skips** — a repo-wide zero would delete its own test. The door the historical clause guarded is closed differently: `negknowtest/behaviour.go:382-390` makes the maintainer-surface case a hard type assertion that **fails rather than skips**, so a weakened factory cannot buy a green suite. Operative replacement: `TestRunLedgerSuite_AgainstQompackOpen` runs all six behaviour cases against `negknow.Open` with zero skips (`.../inv/SP-09/I-09.11-negknowtest.txt`). The retirement predates wave 4 (`f4a94b9`); no wave-4 commit touched it. |

Wave-4 context checked and found **non-superseding** for these rows: `d680d7f` (SP-16 M6) adds
`scope.go`, `expiry.go` and `authorization.go` to `internal/negknow` — observed repository identity,
evidence-lifecycle events, and a cross-scope reuse gate. These sit *beside* the historical rows
rather than replacing them: 82 of their assertions were executed here as corroboration
(`.../inv/SP-09/wave4-scope.txt`, all PASS), including
`TestApplies_AccessOutcomeIsNeverReportedAsAbsence` — wave 4's restatement of the same
errors-not-absence rule I-09.5 now carries — and `TestApplies_AttributionNeverRises` /
`TestApplies_CrossScopeRequiresAGrantThatCoversIt`, which extend but do not contradict I-09.5's
"session scope hidden across sessions". No wave-4 commit changed `descriptor.go`, `ledger.go`,
`staleness.go`, `bloom.go`, `log.go`, `record.go`, `classify.go`, `ingest.go` or `detector.go`
(`git log -- internal/negknow/` tops out at `d680d7f` for those three new files only).

## 4. Deferred to coordinator

No row is NOT-RUN and none is FAIL-COLOAD-SUSPECT. Two *commands* inside executed rows fell outside
this seat's mandate, plus one baseline confirmation worth a quiet re-measure:

| Row | Exact command | Reason |
|---|---|---|
| I-09.6 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/negknow/ -run 'TestRecord_\|TestGetActiveAll\|TestTopActive\|TestAnswerMCPResult\|TestBlindMode\|TestClose' -race -v` | Race runs are excluded from this seat. The same filter passed **without** `-race` (16 tests, `.../inv/SP-09/I-09.6-norace.txt`); only the race detector's verdict is outstanding. The package also holds `TestConcurrentRecordQuery`, which `go test -race ./internal/negknow/...` (the `V4-SP09-01` command) would cover in the same pass. |
| I-09.14 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/negknow/ -bench . -benchtime 2s -run '^$'` | The benchmark half of the row; benchmarks are excluded from this seat and are co-load sensitive. Seven benchmarks exist: `BenchmarkOpen`, `QueryHit`, `QueryMiss`, `Record`, `RebuildBloom`, `RefreshStaleness`, `DetectorScan`. |
| I-09.14 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/negknow/ -run 'TestBudget_Open' -v` | Quiet-window confirmation of the FAIL-BASELINE. The failure is CPU-clock-gated (322.92 ms CPU/op vs 300 ms), so it is not purely a wall-clock co-load artifact, but the margin is 7.6 % and the reference platform has never measured it. Classified FAIL-BASELINE per the brief, **not** FAIL-COLOAD-SUSPECT. |

## 5. Questions

1. **`TestBudget_Open`'s standing for V5 sign-off.** It is a named pre-existing failure on this host,
   yet no `SP09-*` row exists in `plans/CARRIED-DEFECTS.tsv` — unlike `SP08-D1` and `SP10-D1`, which
   record their budget breaches there. Should the `BenchmarkOpen` 300 ms breach be opened as a
   carried defect owned by V5-VERIFY (or a later checkpoint), or does it ride the existing
   "reference platform never measured" waiver? This seat cannot open the row (read-only tree).
2. **Where the SP-20 M2 artifacts live.** `T20-M2-01` requires a "state lineage artifact" and
   `T20-M2-02` an "uncertain/unavailable response, never active" artifact. Both assertions exist as
   passing tests (§3), but no corresponding artifact file was found under `plans/sdd/`, and
   `plans/V4-SP-20-*.md`'s commit checkboxes 6 and 7 are unchecked although the code has landed.
   Does V5-VERIFY require those artifacts produced, or is the executed-test evidence in this report
   the accepted substitute?

# V5-VERIFY inventory — SP-06 (store / redaction / tokens)

**SP:** SP-06, rows `I-06.1` … `I-06.18` (18 rows, all reconciled, none dropped).
Historical baseline: `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md` §2.6 at HEAD `7f92af5`
(lines 224–241).

**Owner plan sections consulted**

- `plans/V4-SP-20-capture-storage-and-state-remediation.md` — Mission; Required invariants 1, 3, 4, 6, 9, 10;
  implementation spec **M1-01** (observation envelope and fidelity), **M1-02** (event ordering and durable
  publication), **M1-03** (object relations, storage and collection), **M1-04** (compatible migration and
  import); test-plan rows **T20-M1-01/02/03/04/06/07/08**; exit criteria 4–7.
- `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md` §2.6 — the reconciliation target:
  *"SP-20 M1 gates; object/index publication, backup/import/GC and privacy before persistence; assembled
  estimates calibrated, no exact chunk-sum claim."*
- `plans/sdd/V4-VERIFY/reconciliation-map.md` — rows `rc-v4-sp06-01`, `-03`, `-04`, `-05`, `-08`, `-09` and
  `rc-v4-sp10-06`. Its mappings were reused where still valid; **every result below was re-run on this tree.**
- `plans/CARRIED-DEFECTS.tsv` — `SP06-D1` (`wontfix`), `SP06-D2` (`deferred:V4-VERIFY`, still unresolved).

**Tree / HEAD:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5`
@ `87c0c1d` ("chore(sp21): integrate deterministic admission control" — develop with wave 4 integrated).
**Platform:** Windows 11, `go1.26.6 windows/amd64`. **Date:** 2026-09-08.
**Machine state:** shared — other V5-VERIFY inventory children ran concurrently in this tree throughout.

## Counts

| Disposition | Rows |
|---|---:|
| `MAPPED` | 16 |
| `MAPPED-CMD` | 1 |
| `SUPERSEDED-BY-WAVE4` | 1 |
| `RETIRED` | 0 |
| `MISSING` | 0 |
| `NEEDS-COORDINATOR` | 0 |
| **Total** | **18** |

| Result | Rows |
|---|---:|
| `PASS` | 16 |
| `FAIL` | 0 |
| `FAIL-BASELINE` | 0 |
| `FAIL-COLOAD-SUSPECT` | 0 |
| `SKIP` | 0 |
| `NOT-RUN` | 2 |

A disposition is never a pass. Only the executed commands recorded in the *Artifact* column produced a
`PASS`; nothing here is scored from a test **name**.

## Table

Artifact paths are under
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-06/`
(abbreviated `…/inv/SP-06/` below).

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-06.1 | Redaction: ten rules, idempotent, bounded growth, user patterns; `TestRedact_AnthropicBeforeGeneric`, `TestRedact_DotenvGatedByKeyName`, `TestRedact_RejectsZeroWidthUserPattern` | MAPPED | `internal/redact`: all three named tests exist and pass; whole-package run green (206 pass / 0 fail / 0 skip). Ten built-in rules confirmed by reading `internal/redact/rules.go` `builtinRules()`: `pem_private_key`, `aws_access_key_id`, `github_token`, `anthropic_key`, `generic_sk_key`, `jwt`, `bearer_token`, `credentialed_uri`, `assignment_secret`, `dotenv_value` | PASS | `…/inv/SP-06/I-06.1.txt` | direct | The same run also covers the wave-4 `TestCapturePolicy_*` / `TestCaptureFragmentPolicy_*` additions (see I-06.5). |
| I-06.2 | Redaction fuzz: no crashers, `Redact(Redact(x)) == Redact(x)` | MAPPED | `internal/redact`: `FuzzRedactIdempotent` confirmed present via `go test -list FuzzRedactIdempotent ./internal/redact/` | NOT-RUN | `…/inv/SP-06/list-06.1.txt` | direct | **Reason: fuzz runs are outside this child's mandate.** Deferred to the coordinator with the exact command. |
| I-06.3 | Exact chunk-level token accounting (G10.2); PDF page-derived, never flat 2 000; `EstimateRoot` cache-key is `core.Hash` alone | MAPPED (wording corrected) | `internal/tokens`: 42 selected tests, 46 pass / 0 fail. `TestEstimateRoot_ClassIndependentCache` proves the memo key is `core.Hash` alone (a chunk cached under `ClassProse` reprices under `ClassCode` with no miss). `TestEstimatePDF_TextPDF`, `_ScannedPDF`, `_ZeroPagesCountsAsOne` pin page-derived PDF sizing. `TestCalibrate_*` (14 tests) pin the calibrated-estimate path | PASS | `…/inv/SP-06/I-06.3.txt`, `…/inv/SP-06/list-06.3.txt` | direct | §2.6's "no exact chunk-sum claim" — the row title's word *exact* names the estimator (`tokens.NewExactWithObs`), not an exactness claim. See the old-to-new map. |
| I-06.4 | Object layout, compression modes, global dedup, `Novel`/`Reused`; two-level fanout; `RawBytes` never double-counted | MAPPED | `internal/store`: `TestPutBytes_FanoutLayout`, `_CompressionNone`, `_GlobalDedup`, `_IdenticalRootIsFree`, `_DedupHitReportsThisPutsRawBytes` — 5 pass / 0 fail | PASS | `…/inv/SP-06/I-06.4.txt` | direct | — |
| I-06.5 | **Redaction before canonicalization and chunking** (§13 inv. 7); no object under `objects/` holds any of the ten secret literals | SUPERSEDED-BY-WAVE4 | Historical clause still asserted and green: `internal/store`: `TestPutBytes_RedactionBeforeChunking`, `TestPutBytes_RedactionRunsWhenDepsRedactIsNil` (2 pass); `test/e2e`: `TestE2E_SecretNeverLandsInObjects` (pass — it ingests all ten families, walks `objects/`, **decompresses** every object and asserts no literal survives). **Wave 4 moved the boundary earlier**: privacy is now applied at the capture boundary before persistence — `internal/redact`: `TestCapturePolicy_*` (8) and `TestCaptureFragmentPolicy_*` (3), all green inside I-06.1's run; `internal/hookio`: `TestCaptureHook_DenialAndUnavailableDoNotExposePolicyBytes`, `TestCaptureHook_SourceBytesSurviveAPolicyDenial` | PASS | `…/inv/SP-06/I-06.5s.txt`, `…/inv/SP-06/I-06.5e.txt`, `…/inv/SP-06/I-06.1.txt` | direct (store/e2e/redact), by-name (hookio) | New requirement: SP-20 **§M1-01** + invariant 1 — "privacy denial/redaction occurs before persistence". Commits `9e939dd`, `a54a572`. The historical before-chunking clause is now a strict subset. |
| I-06.6 | Canonicalize-first, delta roots, ephemeral flag, truncation, near-dup | MAPPED | `internal/store`: `TestPutBytes_CanonicalizeBeforeChunk`, `TestPutBytes_KeepRawStoresDeltaRoot`, `TestPutBytes_KeepRawFalseStoresNoDeltas`, `TestPutBytes_EphemeralFlagPersists`, `TestPut_ReaderTruncation`, `TestPutBytes_NearDup` — 6 pass / 0 fail | PASS | `…/inv/SP-06/I-06.6.txt` | direct | The historical pattern's three bare stems are prefix matches, not stale names; resolved names are in the old-to-new map. Delta semantics were extended by wave 4 (`internal/store/lifecycle_test.go`: `TestPutBytes_DeltaRecordDeclaresItsBase`, `TestPutBytes_UnprovenDeltaFallsBackToAFullObject` — SP-20 invariant 6). |
| I-06.7 | Read paths: chunk get, quarantine on corruption, full open, span open; `core.ErrNotFound` + one `Loud` | MAPPED | `internal/store`: 11 selected tests, 15 pass / 0 fail, incl. `TestGetChunk_QuarantinesCorruption`, `TestGetChunk_QuarantineFailurePreservesObject`, `TestOpen_StreamsFullRoot`, `TestOpenSpan_Boundaries`, `TestHas_NoIO` | PASS | `…/inv/SP-06/I-06.7.txt` | direct | Wave 4 (`16ecc77`) **strengthened, not replaced**, this row: `TestGetChunk_RejectsValidCompressedReplacement`, `_RejectsSameLengthUncompressedReplacement`, `_RejectsUnindexedSubstitutedObject`, `_RejectsPhysicallyOversizeObjects`. Consistent with V4 `rc-v4-sp06-03`. |
| I-06.8 | Index durability: reopen, torn tail, closed-store errors, concurrency (`-race`) | MAPPED | `internal/store`: `TestOpenStore_LoadsIndex`, `TestOpenStore_TruncatedFinalLine`, `TestClosedStoreErrors`, `TestConcurrentPut` — 4 pass / 0 fail **without `-race`** | PASS (non-race half only) | `…/inv/SP-06/I-06.8.txt` | direct | **The `-race` half of the historical command was not run** (race runs are outside this child's mandate) and is deferred to the coordinator. |
| I-06.9 | tool_use index: idempotent replay, conflict rejection, append-only supersession | MAPPED | `internal/store`: 11 selected tests, 11 pass / 0 fail, incl. `TestRecordToolUse_IdempotentReplay`, `_ConflictingReplay`, `TestMarkSuperseded_AppendOnly`, `TestArgsDigest_KeyOrderInvariant` | PASS | `…/inv/SP-06/I-06.9.txt` | direct | — |
| I-06.10 | File version history and `ChangedSince`; `paths.Key` normalization; input order preserved | MAPPED | `internal/store`: 9 selected tests, 14 pass / 0 fail, incl. `TestChangedSince_KeyNormalization`, `TestChangedSince_PreservesInputOrder`, `TestFilesJSON_MaterializedByFlush` | PASS | `…/inv/SP-06/I-06.10.txt` | direct | — |
| I-06.11 | Segment log + encoded-once DPI guard (§4.6, §8.2); `ErrAlreadyEncoded`; all-or-nothing batch; contiguous frontier | MAPPED | `internal/store`: `TestSegment_` selects 21 tests, 21 pass / 0 fail. `TestSegment_MarkEncodedRefusesDifferentSeq` present and green; `segments.go:438` returns `core.ErrAlreadyEncoded`; `TestSegment_MarkEncodedBatchIsAllOrNothing` and `TestSegment_FrontierIsContiguous` green | PASS | `…/inv/SP-06/I-06.11.txt` | direct | — |
| I-06.12 | Search backing `recall`; deterministic across 20 runs; default `K == 5` | MAPPED | `internal/store`: 10 selected tests, 10 pass / 0 fail. `TestSearch_DefaultKIsFive` asserts `require.Len(hits, defaultK)` over a 20-seed corpus; `TestSearch_Deterministic` repeats the query 20 times | PASS | `…/inv/SP-06/I-06.12.txt` | direct | — |
| I-06.13 | Stats and the Phase-1 dedup ratio: `DedupRatio >= 4.0`; `TestStats_SublinearGrowth` | MAPPED | `internal/store`: 9 selected tests, 9 pass / 0 fail. Measured on this tree: `TestPhase1ExitCriterion_ReadHeavy` — **`DedupRatio=24.52`** (raw 878 852 B, stored 35 842 B, 78 objects), well above the 4.0 floor; `TestStats_DedupRatio` logged 81.11; `TestStats_SublinearGrowth` and `TestStats_GrowthGateFailsWithoutDedup` green | PASS | `…/inv/SP-06/I-06.13.txt` | direct | Run took 58.2 s under co-load. |
| I-06.14 | GC: mark-and-sweep from real roots, retention "whichever is longer", the **two** deadline truncations, dry run | MAPPED (root set enlarged) | `internal/store`: `TestGC_` selects **36** tests, **40 pass / 0 fail / 0 skip** (283 s). All three named clauses green: `TestGC_DeadlineTruncatesAndResumes` (62.5 s), `TestGC_MarkPhaseHonoursTheDeadline` (21.5 s), `TestGC_MarkIndexWalksAreNotTruncatedByTheDeadline` (13.4 s); plus `TestGC_ZeroPolicyInheritsConfigAndDeletesNothing`, `TestGC_RetentionIsWhicheverIsLonger`, `TestGC_EphemeralNotInWindowByAge`, `TestGC_TombstonesRootsAppendOnly`, `TestGC_DryRun` | PASS | `…/inv/SP-06/I-06.14.txt`, `…/inv/SP-06/list-06.14.txt` | direct | **Both truncations were exercised in the measured run, each by its own test, and both passed** — the sweep truncation persists a cursor and the next pass resumes from it; the mark-harvest truncation returns `Truncated: true`, collects nothing and persists no cursor. Wave 4 enlarged the root set (SP-20 §M1-03, invariant 9). `SP06-D1` (`wontfix`) still applies to deadline overshoot, which `TestGC_DeadlineOvershootIsBoundedByTheCheckInterval` now bounds. |
| I-06.15 | Flush/session index; append-only guard over **every** store file | MAPPED (partial — see note) | `internal/store`: `TestFlush_SessionsLastRecordWinsAndSecondFlushAppendsNothing`, `TestAppendOnlyGuard_StoreFiles` — 5 pass / 0 fail | PASS | `…/inv/SP-06/I-06.15.txt` | direct | **Coverage gap named, not scored as a pass:** `storeJSONLFiles` (`appendonly_test.go:20`) is the historical five logs only — `roots`, `tool_use`, `files`, `sessions`, `segments`. Wave 4 added at least ten further store-owned logs (`delivery-leases.jsonl`, `delivery-acks.jsonl`, `retention-roots.jsonl`, `evidence.jsonl`, `eliminations.jsonl`, `invariants.jsonl`, `mapping.jsonl`, `rollback.jsonl`, `newformat.jsonl`, `demand.jsonl`), none of which the guard walks. The word *every* is no longer literally true. Coordinator question Q3. |
| I-06.16 | Store properties: "all six properties PASS" at `-rapid.checks=1000` | MAPPED (count + command corrected) | `internal/store`: `-run Prop` selects **five** properties, not six: `TestPropChangedSinceIsExactlyHashInequality`, `TestPropPutGetRoundtrip`, `TestPropOpenSpanMatchesSlice`, `TestPropDedupMonotone`, `TestPropMarkEncodedNeverDowngrades` | PASS (corrected command) | `…/inv/SP-06/I-06.16.txt`, `…/inv/SP-06/I-06.16b.txt` | direct | **Two runs.** (a) The historical command verbatim — `-rapid.checks=1000` with no `-timeout` — **died on Go's default 10-minute panic** inside `TestPropDedupMonotone` after the first three properties had passed (`I-06.16.txt`, run wall time 945.9 s). (b) The same checks with `-timeout=30m`: `TestPropDedupMonotone` **PASS** (548.1 s; rapid itself 6 m 25.6 s) and `TestPropMarkEncodedNeverDowngrades` **PASS** (`I-06.16b.txt`). Across the two runs **all five properties passed at 1 000 checks**, so the row is scored PASS against the corrected command and the timeout is recorded as a command defect, not a property failure. Machine was co-loaded during (a). |
| I-06.17 | Store e2e durability | MAPPED | `test/e2e`: `TestE2E_StoreSurvivesProcessRestart` — pass (7.07 s) | PASS | `…/inv/SP-06/I-06.17.txt` | direct | Run with an explicit `-run` filter; the unfiltered `./test/e2e` package was not run. |
| I-06.18 | Store benchmark budgets (nine named budgets) | MAPPED-CMD | All nine named benchmarks exist on this tree, confirmed by `go test -list Benchmark ./internal/store/ ./internal/tokens/`: `BenchmarkPutBytes_100KB_Cold`, `_Warm`, `BenchmarkGetChunk`, `BenchmarkOpenSpan_4KB_of_4MB`, `BenchmarkOpenStore_50kRoots`, `BenchmarkSearch_1000Roots`, `BenchmarkGC_50kObjects`, `BenchmarkMarkEncoded_100`, `BenchmarkEstimateRoot_64Cached` | NOT-RUN | `…/inv/SP-06/list-06.18.txt` | direct | **Reason: benchmark/timing-budget row on a co-loaded machine; benchmarks are outside this child's mandate.** Deferred with the exact command. `SP06-D2` (`deferred:V4-VERIFY`, **still unresolved**) already records the cold/warm budgets as 9× / 19× over on Windows and never measured on the reference platform, so a quiet-window Windows run cannot by itself close this row. |

### Wave-4 surface with no historical row (recorded, not scored against any row)

The §2.6 target names *"backup/import/GC and privacy before persistence"*. GC and privacy map onto I-06.14
and I-06.5. **Backup, import, cutover and the rollback drill map onto no historical `I-06.*` row at all** —
that surface is entirely new in wave 4 (SP-20 §M1-04, T20-M1-08, invariant 10). Run here as supporting
evidence, not as a row result:

`go test ./internal/store/ -run 'TestRollbackDrill_|TestBackup_|TestMigration_|TestImport_|TestImportFidelity_|TestImportCursor_|TestParity_|TestHandoff_|TestCutover_|TestNewMigrator_' -v -timeout=20m`
→ **23 pass / 0 fail** (9.6 s), artifact `…/inv/SP-06/I-06.wave4-backup-import.txt`. Covers
`TestImport_SideBySideWithDurableCursor`, `_ResumesFromTheCursorWithoutDuplicating`,
`_LostCursorFallsBackToTheFrontierWithoutDuplicating`, `_CompletedImportIsANoOp`, `_RefusesADifferentSnapshot`,
`TestImport_LegacyUnknownFidelityIsNeverUpgraded`, `TestImportFidelity_NeverProducesExactFromAnythingElse`,
`TestParity_*` (4), `TestHandoff_LegacyOwnsTheWriterUntilCutover`, `TestCutover_*` (3), `TestRollbackDrill_*` (3),
`TestBackup_VerifyDetectsTamperingAndSizeDrift`, `TestBackup_RestoreOpensAsARealStore`,
`TestMigration_DeclaresRetentionRootsOnDisk`, `TestImportCursor_OnDiskShapeIsVersioned`,
`TestNewMigrator_RefusesWhileTheGateIsClosed`. See coordinator question Q2.

## Old-to-new assertion map

| Row | Historical wording | What it is now | Reason |
|---|---|---|---|
| I-06.3 | "**Exact** chunk-level token accounting (G10.2)" | *Assembled, calibrated estimate over chunk-level units.* `EstimateRoot` sums per-chunk `Units` memoized by `core.Hash` and reprices by class weight at read time; the miss path is SP-01's `ceil(len / charsPerToken)` formula (`TestEstimateRoot_CacheMiss`: 8 × 4096 code bytes → 9 104), and `TestCalibrate_*` adjusts the ratio against observed usage within a clamp | §2.6: "assembled estimates calibrated, **no exact chunk-sum claim**". The word *exact* in the row title is the estimator's constructor name (`tokens.NewExactWithObs`); no test on this tree asserts that a token figure equals a tokenizer's count. **Wording correction, not a retirement** — every named assertion still holds and was re-run. |
| I-06.5 | "Redaction happens before canonicalization and chunking (§13 inv. 7)" | *Privacy decision before **persistence**, at the capture boundary.* New assertions: `internal/redact` `CapturePolicy` / `CaptureFragmentPolicy` (`CapturePolicyVersion = "redact-json/v1"`) and `internal/hookio` capture-hook fidelity/denial tests. SP-20 **§M1-01** and invariant 1 | Wave-4 commits `9e939dd` ("admit captured bytes through explicit privacy policy") and `a54a572` ("byte-oriented capture fragment policy"). The historical clause is retained as a strict subset and still passes. |
| I-06.6 | Pattern stems `TestPutBytes_Canonicalize`, `TestPutBytes_KeepRaw`, `TestPutBytes_Ephemeral` | Resolved names: `TestPutBytes_CanonicalizeBeforeChunk`; `TestPutBytes_KeepRawStoresDeltaRoot` + `TestPutBytes_KeepRawFalseStoresNoDeltas`; `TestPutBytes_EphemeralFlagPersists` | Prefix matches, confirmed with `go test -list`. No pattern correction is required, but the resolved names are recorded so a future `-run` is never scored against a name that does not exist. |
| I-06.14 | "mark-and-sweep from **real roots**" (checkpoints and pins) | The root set is a **superset**: delivery leases (`TestGC_ReadsTheDeliveryLeaseJournalAsARetentionRoot`, `TestGC_AcknowledgedDeliveryLeaseStopsRetaining`), pending writes (`TestGC_RetainsPendingWritesAcrossACrashedRootAppend`), delta bases (`TestGC_RetainsADeltaWithItsBase`, `_RetainsADeltaBaseFromTheDeltaSide`), evidence references (`TestGC_RetentionRootSourceHoldsObjectsLive`), migration/rollback material (`TestGC_CannotCollectMigrationOrRollbackMaterial`), plus quota outcomes (`TestGC_QuotaEvictsInWindowRootsAndReportsThem`, `TestGC_QuotaNeverEvictsAHardRetentionRoot`) | SP-20 §M1-03 and invariant 9; commits `aa57f1b`, `7088509`, `b6820de`, `3b3acbb`, `9757575`. Same finding as V4 `rc-v4-sp06-05`. The historical root list is a strict subset, so the assertion is retained rather than retired. |
| I-06.15 | "append-only guard over **every** store file" | The guard covers the historical **five** index logs only; ten wave-4 logs are outside it (listed in the table note) | Wave-4 lifecycle/migration work added logs without extending `storeJSONLFiles`. The row's *every* is now literally false; the surviving assertion is "the five original index logs are never rewritten". Q3. |
| I-06.16 | "all **six** properties PASS" | **Five** properties exist: `TestPropChangedSinceIsExactlyHashInequality`, `TestPropPutGetRoundtrip`, `TestPropOpenSpanMatchesSlice`, `TestPropDedupMonotone`, `TestPropMarkEncodedNeverDowngrades` | Count correction, established by `go test -list Prop ./internal/store/`. No sixth property exists on this tree; the historical count is not evidence of a missing one and should be read as "every property in the package". |
| I-06.16 | Command `go test ./internal/store/ -run 'Prop' -rapid.checks=1000 -v` | The command **must** carry `-timeout=30m` | `-rapid.checks=1000` is a 10× amplification of `TestPropDedupMonotone`, whose own comment says "rapid runs the body a hundred times"; each check opens **three** real fsync-backed FS stores through `openFS`. As written the command hits Go's default 10-minute panic on this host. Command correction, matching the repo's established `-timeout=30m` convention. |

## Deferred to coordinator

| Row | Exact command | Reason |
|---|---|---|
| I-06.2 | `cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5 && go test ./internal/redact/ -run xxx -fuzz FuzzRedactIdempotent -fuzztime 60s` | Fuzz run — outside this child's mandate. `FuzzRedactIdempotent` confirmed to exist. |
| I-06.8 | `cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5 && go test ./internal/store/ -run 'TestOpenStore_\|TestClosedStoreErrors\|TestConcurrentPut' -race -v -timeout=30m` | Race run — outside this child's mandate. The identical non-race run passed here. |
| I-06.18 | `cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5 && go test ./internal/store/ ./internal/tokens/ -bench . -benchtime 2s -run '^$' -benchmem -timeout=60m` | Benchmark + timing-budget row; needs the quiet window. `SP06-D2` predicts the cold/warm budgets breach on Windows. |

Copy-paste forms (the table cells above escape `|` for Markdown; these do not):

```
go test ./internal/redact/ -run xxx -fuzz FuzzRedactIdempotent -fuzztime 60s
go test ./internal/store/ -run 'TestOpenStore_|TestClosedStoreErrors|TestConcurrentPut' -race -v -timeout=30m
go test ./internal/store/ ./internal/tokens/ -bench . -benchtime 2s -run '^$' -benchmem -timeout=60m
```

All three run from `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`. Nothing else is deferred:
every other row was executed on this tree, and no row is `FAIL`, `FAIL-BASELINE` or `FAIL-COLOAD-SUSPECT`.

## Questions

- **Q1 (I-06.3).** §2.6 says "no exact chunk-sum claim". I adjudicated the row title's *exact* as an estimator
  name rather than an exactness claim and kept the row `MAPPED` with a wording correction. Confirm that
  reading, or rule the row `RETIRED` with the calibrated-estimate assertion as its replacement.
- **Q2 (new surface).** Wave 4's backup / import / cutover / rollback-drill surface (26 tests in
  `internal/store/backup_test.go` and `migrate_test.go`; 23 run green here) is named by the §2.6 target but
  belongs to **no** retained `I-06.*` row. Should V5-VERIFY mint a new inventory row for it, or is it scored
  under SP-20's own T20-M1-08 by another owner? This report claims no row coverage of it.
- **Q3 (I-06.15).** The append-only guard walks only the five original index logs while the store now writes
  at least fifteen. Is extending `storeJSONLFiles` (or deriving it from the package's own file constants) in
  scope for a V5 fix, and which subplan owns it? Recorded as a named gap, not a failure: the test that exists
  does pass.
- **Q4 (I-06.18 / SP06-D2).** `SP06-D2` is `deferred:V4-VERIFY` and still unresolved on this tree. A quiet
  Windows run of I-06.18 will re-observe the breach rather than close it. Does V5 sign-off require the
  reference-platform measurement the defect names, or is the carried-defect row carried forward again?

# Wave 3 workstream report: w3-e2ereds (C3.2)

Branch `closeout/w3-e2ereds`. Workflow `wf_eed51aa0-3c3`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `36c4fe9`

### Root cause

V4 x05: its negative control struct-copied a record read from the in-process store, so the copy kept the hook capture's ObservationID. Since 99108a2, one delivery identity binds exactly one record (00-ARCHITECTURE.md §0.2.1), so the store correctly refused the copy as an append-only violation. The test premise was stale; the product is correct. V5 x02: re_read's newest version comes from the §8.2 file-version history, which the observer appends at step 7, after the tool_use line and the publication syncs plus the capture link. The test waited only for the tool_use line. 22ff16c added a SyncPublication into that gap and made it wide enough to fail consistently under load and under -race. The version still lands; it is late, not wrong. Elimination: already fixed on this base by 98e8557.

### Summary

W3-E2EREDS report. Branch closeout/w3-e2ereds, cut from 54a4334. HEAD is 36c4fe9. I changed only two test files; no product code changed. Nothing in internal/daemon or internal/paths was touched. Evidence is in plans/sdd/V6-closeout/w3-e2ereds/runs/, and the Linux artifacts are under runs/linux/.

## 1. TestV4_TombstoneToRecallToExpandRoundTrip: stale test premise, fixed in eb4302a
**Symptom.** The test fails at v4_x05_test.go:180 with "append-only violation: observation sha256:e0e3... already reserved for a different intent". It is deterministic:
- Windows at 54a4334: failed.
- Linux non-root -race at 54a4334: failed 3/3.
- It also fails at cf31e01, b070bbe and 99108a2.
- It passes at 99108a2^ (c95b7af).
- The breaking commit is 99108a2, "fix(store): bind recoverable observation publication intents".

**Root cause.**
- The negative control does `escapedRec := rec` and then changes only the ID and Path.
- `rec` comes from the rig's in-process `Store.ToolUse`, which returns the in-memory record. That record still carries `ToolUseRecord.Observation`, the hook capture's delivery identity. The field is in-process only; its doc says it is zero after a reload.
- Since 99108a2, `RecordToolUse` sends any record with an Observation through `publishObservation`. One `ObservationID` is one accepted host delivery and one intent (00-ARCHITECTURE.md §0.2.1).
- `reserveConflictLocked` therefore refuses a second, different intent for the same delivery. That refusal is the intended invariant. It is already pinned by the store test TestObservationPublish_IdentityConflictAndIdempotence ("Rebinding the observation to a different record identity is refused").

**Why this is not a product defect.** No product code copies a record read from the store and writes it again. The RecordToolUse callers (mcp/ephemeral.go, observer prompt_delivery.go, stop.go, supersede.go) all build fresh records.

**Fix (criterion change).**
- The escaped record is now written with `Observation = ""`. That matches what the test says it writes: an in-process write, not a hook delivery.
- The test now also asserts that the copy still carrying the identity is refused with `core.ErrAppendOnly`, after checking `NotEmpty(rec.Observation)`.
- Every recall, expand and authorization assertion is unchanged.
- Rationale: 99108a2 made the refusal deliberate and documented. The new assertion pins it in e2e, and the negative control keeps proving that authorization is checked at retrieval time.

## 2. TestV5_TombstoneToExpandRoundTrip: the test's wait was too early, fixed in 797b674
**Symptom.** "An unpinned re_read is the newest capture" fails with expected = rec2 (sha256:38aa...) and actual = rec (sha256:5fbaba...). It depends on load:
- 54a4334 on Windows: passed 6/6 in a quieter moment, then failed 4/4.
- b070bbe on Windows: failed 6/6.
- Linux -race at 54a4334: failed 3/3.

**Root cause.**
- An unpinned re_read answers from the §8.2 file-version history (`FileHistory`).
- The observer appends that history at tooluse.go step 7. By then the tool_use line is already visible, and the store's intent-completion syncs and commit, `finishObservation` and the capture link have run in between.
- `x02WaitIndexed` waits only for the tool_use line.

**Evidence.**
- A dump at the failing assertion (on b070bbe) showed index/files.jsonl at 153 bytes, holding only rec's version. Eight seconds later it was 306 bytes, and an unpinned re_read returned rec2. So the version arrives late; it is not lost.
- A temporary 3 s sleep before step 7 reproduced the exact failure on a run that otherwise passed. The test log showed rec.Root = 5fbaba..., the same "actual" as every reported failure.

**Bisect.** The runs were interleaved in the same load window on Windows:

| Commit | Result |
|---|---|
| 99108a2^ | pass 4/4 |
| 99108a2 | pass 4/4, then 8/8 |
| 22ff16c | fail 4/4 |
| cf31e01 | fail 4/4 |

22ff16c ("fix(observer): recover original publication before deriving replay") replaced `linkObservation` with `finishObservation`. That adds a full `SyncPublication` between the tool_use line and step 7, which lengthens the gap: it re-reads and verifies every chunk, then fsyncs every object, roots/tool_use and the index directory.

**Fix.**
- New helper `x02WaitFileVersion` polls index/files.jsonl for the exact (store key, root) pair, using `paths.Key` and `ReadFileShared`, within `mcpE2EIndexBound`. It runs before each unpinned re_read.
- This follows the precedent of test/security's `requireFileVersion` (2ccae0c, which measured 1.1–2.5 s for the same step on Linux).
- No assertion changed.
- This is not a criterion change and not a widened timeout. It waits for the exact publication step the assertion reads.
- The wait is proven not vacuous:
  - With the 3 s delay injected, the corrected test passes 2/2.
  - With rec2's file version suppressed, it fails at the wait with "the observer never recorded sha256:38aa... as a version of src/auth.ts".
  - Both diagnostic edits were reverted and never committed. They are recorded in runs/diag-observer-step7-patches.txt.

## 3. TestV5_EliminationThroughEveryFourSurfaces: already fixed on this base
- It was fixed by 98e8557 (w2-hookout, the C1.12/C1.18 criterion change to host-conforming PreCompact plus proof by the sealed artifact).
- At 54a4334 it passes: Windows 1/1, Linux -race 3/3, and it passes in the full run and after my changes.
- The w2-lifetime and w2-sessionend reds ("a full-mode PreCompact answers through hookSpecificOutput") came from branches cut before 98e8557 merged. No sibling subtest fails.

## Verification
- **Linux, after the fix.** The gate at 797b674, non-root with -race, running the three tests with --count 3: PASS, pass=27 fail=0.
- **Windows, focused.** V4 x05 passes 3/3. V5 x02 passes 6/6.
- **Windows, whole test/e2e package alone at 797b674** (1877 s, heavy co-load from other workstreams): 95 PASS, 4 FAIL. All three target tests pass. The four failures:
  - **a) TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight.** The helper returned 3.2 ms before the late daemon's `cmd.Wait` returned. It passes alone (3.92 s). The helper counts the daemon as gone when its lock is released, but the test asserts against process exit, which comes a few ms later, so it can flake under load. Owner is w2-hookout's aec178a.
  - **b) TestV3_HotPathUnchangedWithLedgerResident.** B-A p50 30.7 ms against a 15 ms limit, plus B-B. It fails alone too (440 s). This is the known co-load row that w2-hookout also saw on base b070bbe.
  - **c) TestV4_HotPathUnchangedWithTheFullWave3ResidentSet.** The baseline arm touched spool/<client> (a hook spooled under load) and the wave-3 arm did not. It passes alone (10.3 s).
  - **d) TestV5_PreCompactToRehydrateToDroppedRoundTrip/full_budget_round_trip.** A Windows sharing violation reading state/rehydrate-sess-e2e-v5-x04.json at v5_x04_test.go:305. It passes alone (17.5 s).
- **Checks.** fmt-check, go vet ./test/e2e, pinned golangci-lint ./test/e2e/... and nomagic ./test/e2e/... all clean (exit 0).

## Housekeeping
- I killed no processes, and none of mine remain.
- My scratch exports are deleted.
- The Linux /work/cx-w3-e2ereds-* directories are kept as evidence.
- No real Claude Code session was run.

## Key paths
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-e2ereds/test/e2e/v4_x05_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-e2ereds/test/e2e/v5_x02_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-e2ereds/plans/sdd/V6-closeout/w3-e2ereds/runs/

### Commits

- eb4302a test(e2e): keep x05's escaped record off the capture's identity
- 797b674 test(e2e): wait for x02's file versions before re_read
- 36c4fe9 docs(closeout): record the w3-e2ereds repro, bisect and gate runs

### Tests

- `go test ./test/e2e -run '^(TestV4_TombstoneToRecallToExpandRoundTrip|TestV5_TombstoneToExpandRoundTrip|TestV5_EliminationThroughEveryFourSurfaces)$' -count=1 -v -timeout=30m (Windows, base 54a4334)` — V4 x05 FAIL (append-only violation), V5 x02 PASS (quiet moment), Elimination PASS; runs/repro-three-base-54a4334-windows.log <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$' -count=6 / -count=4 -v (Windows, base 54a4334)` — PASS 6/6, then FAIL 4/4 later under load; runs/repro-v5x02-count6-base-54a4334-windows.log, runs/repro-v5x02-count4-base-54a4334-windows-2.log
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-e2ereds --out .../w3-e2ereds/runs/linux 54a4334 base-three-reds --run '<three>' --count 3 --timeout 30m -- ./test/e2e` — FAIL: V4 x05 3/3 fail, V5 x02 3/3 fail, Elimination 3/3 pass (non-root, -race)
- `V4 x05 on git-archive exports: cf31e01, 99108a2^ (c95b7af), 99108a2 (Windows)` — cf31e01 FAIL; c95b7af PASS; 99108a2 FAIL, so the break is 99108a2
- `V5 x02 -count=4/8 on exports: b070bbe, 99108a2^, 99108a2, 22ff16c, cf31e01 (Windows, same load window)` — b070bbe FAIL 6/6; 99108a2^ PASS 4/4; 99108a2 PASS 4/4 + 8/8; 22ff16c FAIL 4/4; cf31e01 FAIL 4/4
- `V5 x02 at b070bbe with index dump instrumentation (runs/diag-v5x02-b070bbe-instrumentation.patch.txt)` — at failure files.jsonl=153 B (rec only); 8 s later 306 B and unpinned re_read returns rec2
- `V5 x02 at 54a4334 with a 3 s sleep injected before observer step 7 (old test)` — FAIL with the reported expected/actual; rec.Root=5fbaba... confirmed as the 'actual'
- `go test ./test/e2e -run '^TestV4_TombstoneToRecallToExpandRoundTrip$' -count=3 -v (Windows, after eb4302a)` — PASS 3/3
- `go test ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$' -count=6 -v (Windows, after 797b674)` — PASS 6/6
- `corrected V5 x02 with injected 3 s step-7 delay / with rec2's file version suppressed (diagnostic, reverted)` — delay: PASS 2/2; suppressed: FAIL at x02WaitFileVersion (the wait is not vacuous)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-e2ereds --out .../w3-e2ereds/runs/linux 797b674 fixed-three-reds --run '<three>' --count 3 --timeout 30m -- ./test/e2e` — PASS pass=27 fail=0 (non-root, -race)
- `go test ./test/e2e -count=1 -v -timeout=75m (Windows, 797b674, whole package alone, co-loaded host)` — FAIL 4 of 99 top-level (95 PASS, 1877 s); all three target tests PASS; reds: ShutdownIfReachable_WaitsForASpawnStillInFlight, V3_HotPathUnchangedWithLedgerResident, V4_HotPathUnchangedWithTheFullWave3ResidentSet, V5_PreCompactToRehydrateToDroppedRoundTrip/full_budget_round_trip
- `each of the four reds re-run alone: go test ./test/e2e -run '^<name>$' -count=1 -v` — ShutdownIfReachable PASS (3.92 s); V4 HotPath full wave-3 PASS (10.3 s); V5 PreCompactToRehydrate PASS (17.5 s); V3 HotPath FAIL alone too (B-A p50 30.7 ms vs 15 ms; known co-load row) <!-- runpatterns: the -run argument is a placeholder for each of the four named reds, which the result column names; not a runnable pattern -->
- `go run ./tools/devtool fmt-check; go vet ./test/e2e; go run -modfile=tools/pinned/go.mod golangci-lint run ./test/e2e/...; go run ./tools/lint/nomagic ./test/e2e/...` — all exit 0 (runs/lint-touched-windows.log)

### Criterion changes

- test/e2e/v4_x05_test.go (eb4302a): the negative control's escaped record is now written with no delivery identity (Observation cleared) instead of carrying the hook capture's ObservationID, and the test also asserts that the identity-bearing copy is refused with core.ErrAppendOnly. Rationale: since 99108a2, one accepted host delivery publishes exactly one record (00-ARCHITECTURE.md §0.2.1; store unit test TestObservationPublish_IdentityConflictAndIdempotence), and the test describes an in-process write, not a hook delivery. All recall, expand and authorization assertions are unchanged.
- test/e2e/v5_x02_test.go (797b674): not a criterion change. It adds a readiness wait (x02WaitFileVersion, for the exact root in index/files.jsonl, within the existing mcpE2EIndexBound) before each unpinned re_read. No assertion was loosened. With the file version suppressed, the test still fails.

### Open issues

- TestV3_HotPathUnchangedWithLedgerResident fails even when run alone on this co-loaded host (B-A p50 30.7 ms against 15 ms). w2-hookout saw the same on b070bbe. It is not from this lane.
- TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight (aec178a) failed under co-load: the helper returned 3.2 ms before the daemon process exited. It passes alone. The helper counts the daemon as gone when its lock is released, but the test asserts against process exit, which comes a few ms later, so it can flake. Route to the w2-hookout owner.
- TestV4_HotPathUnchangedWithTheFullWave3ResidentSet: under load the baseline arm touched spool/<client> (a hook spooled) and the wave-3 arm did not. It passes alone. The row is load-sensitive.
- TestV5_PreCompactToRehydrateToDroppedRoundTrip/full_budget_round_trip hit a Windows sharing violation reading state/rehydrate-sess-e2e-v5-x04.json (v5_x04_test.go:305) under co-load. It passes alone. I did not diagnose whether the fix belongs in the test's open call or in the daemon's writer.
- Redundant durability passes on the leased PostToolUse path, one root synced four times per capture. onToolUse runs syncObservation before publishRecord. completeIntentLocked runs SyncPublication before and after the index write. finishObservation (added by 22ff16c) runs SyncPublication again on the same root before the link. The last pass sits between the visible tool_use line and the §8.2 file version. Removing it would shorten that window, though not close it, and cut hot-path fsyncs. Not changed here: it is durability code with other owners.
- Store.ToolUse returns ToolUseRecord.Observation for records written in this process, and zero after a reload. This is documented on the field, and no product caller re-records a record it read. eb4302a's NotEmpty(rec.Observation) assertion depends on this in-process behaviour and must change with it if an owner decides ToolUse should strip the field.
- Linux /work/cx-w3-e2ereds-* run directories (two runs) are kept in the container as evidence.

### Needs the owner

- Store/observer and perf owner: whether finishObservation's SyncPublication after publishObservation has already synced the same root is intended, or should be skipped on the fresh-publish path, while keeping it on the replay/recovery path.
- Coordinator: route the four co-load e2e rows from the full Windows run (the V3 hot path, the w2-hookout spawn-in-flight helper test, the V4 wave-3 touch-set, and the V5 x04 rehydrate-state sharing violation) to their owners. Three of them pass alone.

## Independent review

### review:e2ereds: sound


## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


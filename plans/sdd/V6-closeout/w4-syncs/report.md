# V6 close-out w4-syncs: SP08-D1 redundant PostToolUse durability passes

Branch `closeout/w4-syncs`. Workflow `wf_85543bfd-f18`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `fcaea148ac07a3939325f61b70fbb3b875401c8d`

### Root cause

A fresh leased capture (PostToolUse, and also UserPromptSubmit and SubagentStop) ran SyncPublication four times on the same root. The passes were: the observer's syncObservation before the write (P0); the store's completeIntentLocked before the record, which ran after the intent (S1); the store's pass after the write (S2); and finishObservation before the capture link (F; on the prompt path, a second syncObservation). Each pass re-reads and hash-verifies every object in the root's closure. It then fsyncs each object file and its three fanout directories, plus roots.jsonl, tool_use.jsonl and the index directory: 139 fsyncs per pass for a 34-object 256 KB root. S1 repeated P0, with only the intent line (which syncs itself) written in between. F repeated S2 with nothing written in between. Evidence: the base makes 22 context checks per capture against 14 now (ctxmap files); the pass counter reads 4 on the base and 2 now; strace counts 559 fsyncs per leased Deduped capture on the base and 281 now. There was also a latent store defect. On its own, the store wrote the intent before proving the root, so a root that could not be proven left an intent that can never complete. Only the observer's P0 prevented this on the leased path (TestObservationPublish_UnverifiableRootWritesNoIntent is RED on the base).

### Summary

# w4-syncs (C2.3 / SP08-D1): redundant publication passes on the leased capture path

- **Branch:** `closeout/w4-syncs`
- **Head:** `fcaea14` (last code commit: `2f4ebbb`)
- **Review base:** `6aff949`

**Outcome:** a fresh leased capture now makes 2 publication passes instead of 4. fsyncs per capture drop from 559 to 281 on Linux, and leased OnToolUse time roughly halves on Windows (every round, p=0.002). No durability or crash property weakens, and every existing publication/crash/fault row passes unchanged. One commit in the earlier draft broke a store test; `2f4ebbb` fixes it.

## 0. Resume handling
- **Merge.** `6aff949` merged as `b3c42c8`, with no conflicts. Build and `go vet` pass.
- **Stashes.** None were mine, and none were touched.
- **The five draft commits** (`71803ec`..`3d1c6b1`) were reviewed as a colleague's unreviewed work. All five are kept.
- **A real defect in `d55a59f`.** It added the exported `*FSStore` method `PublishesObservationsDurably` without classifying it for the read-only store. The earlier seat's killed full store run never reached the test that catches this. My full store run at `f133c9c` failed `TestReadOnly_EveryExportedMethodIsClassified` (log: `runs/win-full-store-f133c9c-readonly-RED.txt`). `2f4ebbb` fixes it as a new commit rather than a rewrite, so commits `d55a59f`..`f133c9c` each fail that one test.
- **The untracked sweep test** was finished, its header corrected against measured check maps, and committed as `f133c9c`.
- **Stale evidence.** The earlier seat's partial logs were moved out of the tree into my scratch directory. All evidence below was re-run at the final code.

## 1. Which pass each property depends on

Pass names used below:

| Pass | What it is |
|---|---|
| **P0** | The observer's `syncObservation` before the write (tool, prompt and subagent paths). |
| **S1** | The store's `completeIntentLocked` pass before the record. On the base it ran *after* the intent. |
| **pass 1** | The same proof, moved into `publishObservation` *before* the intent and outside `obsPubMu`. |
| **S2 / pass 2** | The store's pass after the record is written. |
| **F** | `finishObservation`'s pass before the link. On the prompt path it is the second `syncObservation`. |

| # | What must be durable, before what | Source | Base | Now | Pinned by |
|---|---|---|---|---|---|
| 1 | Root objects (hash-verified and fsynced), their directories, and the root's `roots.jsonl` line — before the intent line names the root | 00-ARCH §0.2.2 item 1. An intent over a non-durable root never completes, so its delivery can never be acknowledged (`TestObservationPublish_MissingObjectsKeepIntentIncomplete`) | P0, and only on the observer's leased path; the store alone did not guarantee it (RED test) | pass 1, inside the store | `TestObservationPublish_UnverifiableRootWritesNoIntent`; `TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite`; `TestObservationPublish_CrashAtEveryStepPublishesOnce` (root-synced cut); `TestPublicationPasses_CutAfterThePutPublishesOnceOnRedelivery` |
| 2 | Root durable before the `tool_use` record and its supersede marks | §0.2.2 item 1; docs/architecture.md §4 | P0 **and** S1. S1 repeated P0; only the self-fsyncing intent line was written in between | pass 1 | CrashAtEveryStep (intent-durable cut); `TestFault_PublicationBoundaries`; `TestPublicationOrderIsObjectReferenceFrontier` |
| 3 | Record, marks and index directory fsynced before the binding commits and before `RecordToolUse` returns | observation_publication.go contract ("UNAVAILABLE until durable and complete") | S2 | pass 2 (unchanged) | CrashAtEveryStep: record-written (as a process crash **and** as a power loss that drops the unsynced batch) and publication-synced cuts; `TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun` |
| 4 | Record durable before the capture link | tooluse.go stage 6b; §0.2.2 | S2, then F repeated it with nothing written in between | pass 2 | `TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart`; `TestV6Prompt_RestartRepairsIndexBeforeSidecarCut` |
| 5 | Record and link durable before the ACK / frontier (`commitDelivery`) | §0.2.2 item 2; Qompack.md §8 ("verifies objects, publishes durable identities, commits references/frontier") | S2 (and F) plus the link's `WriteAtomic` fsync | pass 2 plus the link's `WriteAtomic` | `TestCrashCutBetweenReferenceAndFrontierRedelivers`; `TestObserverAtomicPublicationFailureRemainsDrainRetryable`; `TestObserverPublicationFailureRemainsDrainRetryable` |
| 6 | The §8.2 file version (step 7, after the link) | §8.2; a soft stage, repaired on redelivery | No pass syncs `files.jsonl`. Its root is durable from P0. F only widened the gap w3-e2ereds measured | pass 1 covers the root; the gap is shorter | `TestRedelivery_ReadCutBeforeItsFileVersionIsRepairedByTheReplay`; e2e `TestV5_TombstoneToExpandRoundTrip` |
| 7 | A redelivery re-proves the original publication (after a restart) before rewriting the record and before the link | `observationRecord` / `RecoverToolUseByObservation` | S1 + S2, then F: 3 passes | **Unchanged**: `completeIntentLocked(rootSynced=false)` runs S1 + S2, then F | `TestPublicationPasses_RedeliveryReprovesTheOriginalPublication` (passes on the base too); `TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord`; `TestV6Prompt_PublishedMissingObjectIsNotAcknowledged` |
| 8 | A recorder that does not declare the barriers (for example, a wrapper that embeds `store.Store`) | — | P0 + S1 + S2 + F | The observer keeps P0 and F: 4 passes | `TestPublicationPasses_RecorderWithoutTheBarriersKeepsTheObserversPasses` |

## 2. What changed

**Store side**
- `71803ec`: `SyncPublication` bumps the counter `store.publication.sync` once per pass that gets past the write guard. This is the counting seam the tests use.
- `d55a59f`, in `publishObservation`:
  - It runs `SyncPublication(rec.Root)` **before** the intent is reserved, outside `obsPubMu`, which is where P0 ran before.
  - `completeIntentLocked` skips its own pre-record pass only when that same call just synced exactly that root. Recovery passes `false`.
  - A new capability, `store.DurableObservationPublisher`, declares the result so callers can drop their own passes.
  - A test-only fault seam, `obsPubFault`, stops the path after each durability step.

**Observer side**
- `ce39fb8` (tool path) and `df68b3b` (prompt and subagent paths): when the value the record is written through declares the barriers, the observer skips P0 and links without F.
- Redeliveries and the legacy-link branch still go through `finishObservation` and `recoverPrompt`, unchanged.

**Tests and benchmark**
- `3d1c6b1`: `BenchmarkOnToolUse_TestOutput256KB_Leased`, with the same fixtures on the daemon's leased path.
- `f133c9c`: a sweep that counts the context checks of a complete run and cuts after every one of them.
  - The existing SP08-D2 sweeps cut after at most 8 or 7 checks. A complete run makes 22 or 23 checks on the base and 14 or 15 now, so those sweeps never reached the record write.
  - The existing sweeps are unchanged. The new one passes on the base as well.
- `2f4ebbb`: the read-only classification fix.

No docs were changed: neither docs/architecture.md nor 00-ARCHITECTURE.md names the passes.

## 3. Measurements

**Load-independent**

Pass counter (`TestPublicationPasses_*`):

| Capture | Base (`71803ec`, RED) | Now |
|---|---|---|
| Fresh leased tool / prompt / subagent capture | 4 | 2 |
| Redelivery | 3 | 3 |
| Recorder that does not declare the barriers | — | 4 |

strace per OnToolUse, Linux, 5 captures after a warm-up (`runs/syscount/summary.txt`):

| Fixture | fsyncs, base → now | Other syscalls |
|---|---|---|
| Leased Deduped | 559 → 281 | openat 1240 → 624, read 276 → 140 |
| Leased Delta | 566.2 → 286.6 | — |
| Leased AllNovel | 1196.6 → 601.8 | — |
| Unleased (control) | identical: Deduped 0, Delta and AllNovel 4 | — |

**Windows timing** (`runs/win-ab`): paired ABBA, 10 rounds per side, benchtime 10x, host CPU load 14–100% because three other workstreams were running. Figures are paired ratios new/base with an exact sign test.

| Benchmark | ns/op ratio | p99 ratio | allocs | Rounds faster | p |
|---|---|---|---|---|---|
| Leased Deduped | 0.518 | 0.548 | −25% | 10/10 | 0.002 |
| Leased Delta | 0.521 | 0.563 | −23% | 10/10 | 0.002 |
| Leased AllNovel | 0.542 | 0.566 | −32% | 10/10 | 0.002 |

The unleased control shows no change (ns/op p≥0.75, identical allocs).

**Linux timing** (`runs/linux-ab`): 10 rounds, benchtime 3x, because one leased base capture took about 13 s on this VM's fsync. Load average 3.4–10.1.

| Benchmark | ns/op ratio | Rounds faster | p | Also |
|---|---|---|---|---|
| Leased AllNovel | 0.465 | 10/10 | 0.002 | — |
| Leased Delta | 0.585 | 8/10 | 0.11 | p99 9/10, p=0.02 |
| Leased Deduped | 0.587 | 8/10 | 0.11 | — |

- Allocs are lower in 10/10 rounds on every leased row. The unleased control shows no change.
- The reversed rounds line up with my own Linux x09 gate runs landing inside those rounds.

**These are not budget evidence.** Absolute times were co-loaded; the quiet C5.2 run is the coordinator's. No budget constant or CARRIED-DEFECTS status was changed.

## 4. Tests (all at `2f4ebbb` unless noted)
- `go test ./internal/observer -run '^TestPublicationPasses_' -count=1 -v` on an archive of `71803ec` with the new tests copied in: the Tool/Prompt/SubagentStop SyncsTwice tests FAIL (4 passes, want 2); the Redelivery test passes (3).
- `go test ./internal/store -run '^(TestSyncPublication_CountsOnePassPerCall|TestObservationPublish_UnverifiableRootWritesNoIntent)$' -count=1 -v` on the `71803ec` archive: UnverifiableRootWritesNoIntent FAILS (an intent is written for an unproven root).
- `go test ./internal/observer -run '^(TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun|TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun)$' -count=1 -v`: PASS on the `71803ec` archive (22 and 23 checks) and at the head (14 and 15).
- `go test ./internal/store -run '^(TestSyncPublication_CountsOnePassPerCall|TestObservationPublish_UnverifiableRootWritesNoIntent|TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite|TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord|TestObservationPublish_CrashAtEveryStepPublishesOnce)$' -count=1 -v`: PASS.
- `go test ./internal/store -count=1 -v` at `f133c9c`: FAIL, `TestReadOnly_EveryExportedMethodIsClassified`. Fixed in `2f4ebbb`.
- `go test ./internal/store -count=1 -timeout=60m -v`: PASS (758 s; one existing Windows symlink-privilege skip).
- `go test ./internal/observer -count=1 -timeout=75m -v`: PASS (520 s).
- `go test ./internal/daemon -run '^(TestStoreWrapperDropsTheSupersedingCapability|TestObserverAtomicPublicationFailureRemainsDrainRetryable|TestObserverPublicationFailureRemainsDrainRetryable|TestPublicationOrderIsObjectReferenceFrontier|TestCrashCutBetweenReferenceAndFrontierRedelivers|TestRedeliveryOfOneNonceIsObservedOnce|TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent|TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock)$' -count=1 -v` (the full 20-name list is in the log): 20/20 PASS.
- `go test ./test/fault -run '^(TestFault_PublicationBoundaries|TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence|TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus)$' -count=1 -v`: PASS.
- `go test ./test/integration -run '^(TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites|TestIntegration_GCNeverCollectsALiveRootUnderIngest|TestIntegration_ObserverKeepRawPutsProveTheirDeltaRoundTrip|TestIntegration_HookEventThroughDaemonToStore|TestIntegration_SpooledEventsSurviveToTheStore|TestIntegration_SupersessionMarksEarlierReadThroughDAG)$' -count=1 -v`: PASS.
- `go test ./test/e2e -run '^(TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently|TestV3_LiveSessionWriteSetAndAppendOnly|TestV5_TombstoneToExpandRoundTrip|TestV4_TombstoneToRecallToExpandRoundTrip)$' -count=1 -v`: PASS on Windows.
- **Linux** (`linux-nonroot-gate.sh --prefix cx-w4-syncs`, non-root, `-race`):
  - `./internal/observer`: PASS, 557 tests.
  - `./internal/store`: 844 pass and 1 FAIL, `TestGC_DeadlineOvershootIsBoundedByTheCheckInterval`. Re-run alone, it fails at the head **and** at `6aff949` in the same window; its own message calls this "a finding about the HOST".
  - Focused publication run across daemon, fault, integration and e2e: 56 pass and 1 FAIL, `TestV3_LiveSessionWriteSetAndAppendOnly`. It fails 3/3 at the head **and** 3/3 at `6aff949` (open issue 1).
- **Checks:** fmt-check exit 0; `go vet` on both packages exit 0; golangci-lint on `./internal/store/... ./internal/observer/...` clean; lint `--only=nomagic,importgraph,testdeps,bindeps,sleepcheck` all PASS. Nothing in `6aff949..HEAD` adds `t.Skip`, `nolint` or `nomagic:allow`.

## 5. Criterion changes
- **One classification entry, not a loosening.** `internal/store/readonly_test.go` now lists `PublishesObservationsDurably` in `readOnlyReadsAllowlist`, justified as "returns a constant capability declaration and touches nothing; readOnlyStore does not delegate it because a read-only store publishes nothing". The check still forces every exported method to be classified; the allowlist is its mechanism for that.
- **No other criterion changes.** Every existing assertion, test and threshold is unchanged.

## 6. Housekeeping
- None of my processes remain. My one self-started 10x Linux A/B was stopped by PID after one sample; its partial output is quarantined as `runs/linux-ab/aborted-10x-base-partial.txt` and not used.
- The Linux `/work/cx-w4-syncs-*` directories are kept as evidence.
- No real Claude Code session was run.

Evidence index: `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-syncs/plans/sdd/V6-closeout/w4-syncs/runs/INDEX.txt`

### Commits

- 71803ec feat(store): count publication sync passes
- d55a59f fix(store): sync an observation's root before its intent
- ce39fb8 fix(observer): drop tool capture passes the store now carries
- df68b3b fix(observer): drop prompt and subagent passes the store carries
- 3d1c6b1 test(observer): benchmark the leased 256 KB tool capture
- b3c42c8 chore(v6): merge closeout/integration 6aff949
- f133c9c test(observer): cut a leased capture after every context check
- 2f4ebbb fix(store): classify the durable-publisher declaration
- fcaea14 docs(closeout): record the w4-syncs evidence

### Tests

- `go test ./internal/observer -run '^TestPublicationPasses_' -count=1 -v (archive of 71803ec plus the new test files)` — RED as intended: fresh tool, prompt and subagent captures pay 4 passes (want 2); the redelivery pays 3 and passes
- `go test ./internal/store -run '^(TestSyncPublication_CountsOnePassPerCall|TestObservationPublish_UnverifiableRootWritesNoIntent)$' -count=1 -v (archive of 71803ec)` — RED as intended: UnverifiableRootWritesNoIntent fails because the base store writes an intent naming an unproven root; the counter test passes
- `go test ./internal/observer -run '^(TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun|TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun)$' -count=1 -v` — PASS at the head (14 and 15 checks) and on the 71803ec archive (22 and 23 checks)
- `go test ./internal/store -run '^(TestSyncPublication_CountsOnePassPerCall|TestObservationPublish_UnverifiableRootWritesNoIntent|TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite|TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord|TestObservationPublish_CrashAtEveryStepPublishesOnce)$' -count=1 -v` — PASS
- `go test ./internal/store -count=1 -v (at f133c9c)` — FAIL: TestReadOnly_EveryExportedMethodIsClassified (unclassified PublishesObservationsDurably); fixed in 2f4ebbb
- `go test ./internal/store -count=1 -timeout=60m -v (2f4ebbb, Windows)` — PASS in 758 s; one existing symlink-privilege skip
- `go test ./internal/observer -count=1 -timeout=75m -v (2f4ebbb, Windows)` — PASS in 520 s
- `go test ./internal/daemon -run '^(TestStoreWrapperDropsTheSupersedingCapability|TestObserverAtomicPublicationFailureRemainsDrainRetryable|TestObserverPublicationFailureRemainsDrainRetryable|TestPublicationOrderIsObjectReferenceFrontier|TestCrashCutBetweenReferenceAndFrontierRedelivers|TestRedeliveryOfOneNonceIsObservedOnce|TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent|TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock)$' -count=1 -v (full 20-name list in runs/win-daemon-publication-focused.txt)` — PASS 20/20
- `go test ./test/fault -run '^(TestFault_PublicationBoundaries|TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence|TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus)$' -count=1 -v` — PASS
- `go test ./test/integration -run '^(TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites|TestIntegration_GCNeverCollectsALiveRootUnderIngest|TestIntegration_ObserverKeepRawPutsProveTheirDeltaRoundTrip|TestIntegration_HookEventThroughDaemonToStore|TestIntegration_SpooledEventsSurviveToTheStore|TestIntegration_SupersessionMarksEarlierReadThroughDAG)$' -count=1 -v` — PASS
- `go test ./test/e2e -run '^(TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently|TestV3_LiveSessionWriteSetAndAppendOnly|TestV5_TombstoneToExpandRoundTrip|TestV4_TombstoneToRecallToExpandRoundTrip)$' -count=1 -v (Windows)` — PASS
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w4-syncs --out <runs/linux> 2f4ebbb touched-full --timeout 75m -- ./internal/store ./internal/observer` — observer PASS (557); store 844 pass, 1 fail: TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, which also fails alone at 6aff949 in the same window (a host finding)
- `linux-nonroot-gate.sh ... --run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' -- ./internal/store, at 2f4ebbb and at 6aff949` — FAIL on both builds: no attempt placed the deadline inside the sweep; pre-existing and not from this branch
- `linux-nonroot-gate.sh ... 2f4ebbb publication-focused (daemon, fault, integration and e2e publication rows, -race)` — daemon 29, fault 17, integration 6 PASS; e2e 4 PASS and TestV3_LiveSessionWriteSetAndAppendOnly FAIL
- `linux-nonroot-gate.sh ... --run '^TestV3_LiveSessionWriteSetAndAppendOnly$' --count 3 -- ./test/e2e, at 2f4ebbb and at 6aff949` — FAIL 3/3 on both builds (the SessionEnd 'observer: gc' line never appears); pre-existing
- `go run ./tools/devtool fmt-check; go vet ./internal/store ./internal/observer` — both exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/store/... ./internal/observer/...` — clean, exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck` — nomagic, importgraph, testdeps, bindeps and sleepcheck PASS; golangci-lint fails only on two findings in internal/daemon that this branch does not touch
- `strace -f syscall counts, throwaway golang:1.26.6-bookworm container (runs/syscount/syscount.sh)` — fsyncs per leased capture: Deduped 559 -> 281, Delta 566 -> 287, AllNovel 1197 -> 602; unleased identical
- `sh runs/ab_win.sh 10 <dir> 10x (paired ABBA, Windows)` — leased ns/op ratio 0.518 / 0.521 / 0.542, 10/10 rounds faster, p=0.002; unleased control no change
- `docker exec ... sh linux_ab.sh <W> 10 3x (paired ABBA, Linux)` — leased AllNovel ratio 0.465 (10/10, p=0.002); Deduped and Delta about 0.59 (8/10, p=0.11); allocs lower in 10/10 rounds; unleased control no change

### Criterion changes

- internal/store/readonly_test.go (2f4ebbb): PublishesObservationsDurably added to readOnlyReadsAllowlist with the justification 'returns a constant capability declaration and touches nothing; readOnlyStore does not delegate it because a read-only store publishes nothing'. This classifies a new exported method through the check's own mechanism and loosens nothing: every exported method must still be delegated, swept or justified. No other assertion, threshold, budget, golden or timeout changed.

### Open issues

- Linux TestV3_LiveSessionWriteSetAndAppendOnly fails 3/3 in the container at 6aff949 and at the head: the SessionEnd flush never produces the 'observer: gc' line within e2eHistoryConvergeBound, and the daemon then does not shut down within 20 s. It passes on Windows. This is pre-existing on the integration head and not from this branch; route to the SessionEnd/D13 owner (w2-sessionend) or e2eflakes.
- Linux TestGC_DeadlineOvershootIsBoundedByTheCheckInterval fails alone at both 2f4ebbb and 6aff949 in the same window; its own message says this is a host finding. w3-paths owns this row.
- Whole-module golangci-lint fails on the integration base with two internal/daemon findings this branch does not touch: spawn_stage_test.go:312 errcheck (657a31c) and handlers.go:803 gocritic ifElseChain (from the w2-sessionend merge). Route to their owners or startroute.
- Bisectability: commits d55a59f through f133c9c each fail TestReadOnly_EveryExportedMethodIsClassified. 2f4ebbb fixes it as a new commit, per the prefer-new-commits instruction.
- SP08-D1's evidence benchmark, BenchmarkOnToolUse_TestOutput256KB, runs without an observation identity, so it pays 0 fsyncs (Deduped) or 4 (Delta, AllNovel) and never touches the production path. Every daemon capture is leased. The leased path still costs about 281 fsyncs per 256 KB Deduped capture after this change, and 278 of them are the two remaining SyncPublication passes. B-C status is unchanged and the quiet C5.2 run is still owed.
- A redelivery still pays 3 passes. finishObservation's pass directly follows RecoverToolUseByObservation's two on the intent branch, so it is redundant there, but it was kept as the task requires. It is the only proof on the legacy sidecar-link branch.
- The existing SP08-D2 sweeps (rdxReadCutPoints=8, rdxStopCutPoints=7) rest on a false premise: a complete run makes 14 or 15 checks now and made 22 or 23 before. They were left unchanged, and the new every-check sweep covers the rest. The owner may want to correct their comment or constants.
- Wrapper trap, documented on DurableObservationPublisher: a type that embeds *FSStore and overrides RecordToolUse* without calling through would still claim the barriers. No production type does this; production wires *FSStore directly.
- Linux timing on Deduped and Delta is not significant at 0.05 (8/10 rounds, p=0.11). Two rounds reversed while other runs, some of them my own gate runs, co-loaded the container. The load-independent strace and alloc counts are decisive.
- Linux container directories /work/cx-w4-syncs-* are kept as evidence.

### Needs the owner

- Store/observer owner: confirm that the store now proves an observation's root before writing its intent (d55a59f). This applies to every observation-bearing RecordToolUse caller, not only the observer. It is strictly stronger than the base order and closes the unrecoverable-intent case the RED test shows.
- Store owner (proposal, not implemented): on the fresh path, narrow the post-write pass to an index-only sync (tool_use.jsonl, roots.jsonl, index directory). The same call has just verified and fsynced the same immutable objects, so this would cut about 136 of the 281 fsyncs and 34 object re-reads per Deduped capture. It was not done because it narrows a pass rather than removing a redundant one, and it removes the post-write re-check for objects lost concurrently.
- Store owner (proposal): within one SyncPublication, fsync each distinct directory once instead of once per object. The objects/ root is currently fsynced 34 times per pass for a 34-object root, about 102 directory fsyncs per pass in total.
- Store owner (proposal): keep an in-process set of objects already proven durable, cleared on restart, so deduplicated captures stop re-proving chunks an earlier publication already proved.
- Perf/budget owner: decide whether budget B-C (SP08-D1) should be judged on BenchmarkOnToolUse_TestOutput256KB_Leased (the production path) instead of the unleased benchmark. No budget constant or CARRIED-DEFECTS status was changed.
- Coordinator: route the pre-existing Linux reds (x09 SessionEnd GC line; GC deadline overshoot) and the two golangci-lint findings in internal/daemon to their owners.

## Independent review

### review:syncs: needs-fixes

- **minor** `internal/daemon/observer_atomic_publication_test.go:20-30 (atomicFaultStore); internal/observer/identity.go:117 (recorderPublishesDurably)` — The daemon's fault-injection wrapper describes itself as "the write path production actually takes", but it does not forward the new store.DurableObservationPublisher capability. The observer running over it therefore takes the non-declaring branch: the old 4-pass P0+S1+S2+F shape. That means TestObserverAtomicPublicationFailureRemainsDrainRetryable and its siblings no longer exercise the 2-pass branch production now ships, and no daemon-level fault/drain-retry test covers the declaring branch. This is the same capability-drop trap TestStoreWrapperDropsTheSupersedingCapability pins for SupersedingRecorder, and nothing pins it for the new capability.
  - Evidence: atomicFaultStore embeds store.Store and holds only idx store.SupersedingRecorder. store.Store does not declare PublishesObservationsDurably, so the embedded interface cannot promote it, and recorderPublishesDurably(o.toolRecorder()) returns false for this wrapper. The branch's own TestPublicationPasses_RecorderWithoutTheBarriersKeepsTheObserversPasses shows that such a wrapper pays 4 passes. The declaring branch's failure-to-redelivery path is covered only at observer level (TestPublicationPasses_CutAfterThePutPublishesOnceOnRedelivery, the new every-check sweep).
  - Fix: Existing tests must stay unchanged, so add a new daemon test whose fault wrapper also forwards DurableObservationPublisher from the backing *FSStore. It should inject the RecordToolUseSuperseding failure, then assert the delivery stays drain-retryable and publishes once with 2 passes after repair. Also add a pin next to TestStoreWrapperDropsTheSupersedingCapability showing that an interface-embedding wrapper does not satisfy store.DurableObservationPublisher. Route the stale "production actually takes" comment to the daemon owner.
- **nit** `internal/store/observation_publication.go:731-739 (completeIntentLocked rootSynced doc) and :687-696 (publishObservation)` — The justification for skipping S1 says nothing that pass 1 made durable "can have changed since" because objects are immutable. GC does not take obsPubMu and can remove objects, and the gap between the proof and the record write now includes the obsPubMu wait and the intent fsync (on the base, S1 ran inside the lock just before the record). If a GC sweep lands in that window, the record line is written before S2 detects the missing objects. S2 does still fail the call, the binding stays uncommitted and the delivery stays retained. The end state differs from the base only by an extra uncommitted record line, so this is not a durability weakening, but the comment overstates what the skip proves.
  - Evidence: gcrun.go sweeps objects that are not in the live set and whose mtime is before the pass start. Retention expiry and quota eviction are both possible for an old deduplicated root. publishObservation runs SyncPublication before s.obsPubMu.Lock(), and completeIntentLocked(rootSynced=true) goes straight to recordSupersedingCore. On the base the equivalent window, P0 to intent, was also unguarded, so this is not a new class of failure.
  - Fix: Reword the comment: a concurrent GC removal after pass 1 is caught by the post-write SyncPublication (S2), which fails the call before commit. The skip relies on S2 for that, not on immutability alone. No code change needed.
- **nit** `commits d55a59f..f133c9c (fixed by 2f4ebbb)` — Four intermediate commits fail TestReadOnly_EveryExportedMethodIsClassified, so the range is not bisectable. The implementer disclosed this and kept the fix as a new commit, per instruction.
  - Evidence: runs/win-full-store-f133c9c-readonly-RED.txt; 2f4ebbb only adds the readOnlyReadsAllowlist entry.
  - Fix: Optional, for the coordinator at integration time: fold the one-line allowlist entry from 2f4ebbb into d55a59f when the branch is next re-stacked. Otherwise leave it as documented.
- **nit** `plans/sdd/V6-closeout/w4-syncs/ (no report.md)` — Task item (1) asks for the property-to-pass table "in your report". The table exists only in the implementer's structured summary. The committed evidence (fcaea14) has runs/ and INDEX.txt but no report.md, unlike perfstore/ and w3-e2ereds/.
  - Evidence: ls plans/sdd/V6-closeout/w4-syncs shows only runs/. The earlier workstream reports were committed as standalone docs(v6) commits (29ad705, 611c6ff).
  - Fix: Commit the summary, including the eight-row durability table, as plans/sdd/V6-closeout/w4-syncs/report.md, whether the coordinator or the implementer writes it, so the table the code comments rely on resolves to a tracked file.

## Fix seat (review resolution) — status `done`, head `02c030f`

### Root cause

Carried defect SP08-D1, the publication half. A fresh leased PostToolUse ran SyncPublication on one root four times. P0: the observer's syncObservation before publishRecord. S1: the store's pass at the top of completeIntentLocked, which ran AFTER the intent line had already been written. S2: the store's pass after the record+marks write. F: finishObservation's pass before the capture link. Each pass re-reads and verifies every object in the root's closure, then fsyncs the objects, their directories, roots.jsonl, tool_use.jsonl and the index directory. The leased UserPromptSubmit and SubagentStop captures had the same shape. At the store boundary, only the observer's P0 kept an unprovable root out of the durable intent. TestObservationPublish_UnverifiableRootWritesNoIntent is RED on the base (red-store-71803ec.txt). The fix moves the root barrier into the store, before the intent (S1'). completeIntentLocked skips its pre-write pass only when the same call just synced the same root. The store declares the guarantee as store.DurableObservationPublisher. The observer drops P0 and F only for a recorder that makes that declaration, and only on the fresh path. Redelivery and recovery keep their passes. Result: two passes per fresh leased capture, down from four.

### Summary

W4-SYNCS (C2.3 / SP08-D1), FIX SEAT: FINAL STATE
Branch: closeout/w4-syncs @ 02c030f. Review base: 6aff949. Working tree clean. The fix seat changed no product code. It added one daemon test file (61baf28) and its evidence (02c030f). The product change is the implementer's, unchanged: 71803ec, d55a59f, ce39fb8, df68b3b, 2f4ebbb.

1. DURABILITY PROPERTIES AND THE PASSES THEY DEPEND ON
Sources: code, 00-ARCHITECTURE.md §0.2.2, docs/architecture.md §4, and the crash tests.
Pass names: P0 = observer before the write; S1 = store pre-write inside completeIntentLocked; S2 = store post-write; F = finishObservation before the link; S1' = new store pass before the intent.
Fresh leased path, in order: WAL+lease fsync (transport ACK) -> capture sidecar -> PutBytes -> [pass] -> intent fsync -> record+marks write -> [pass] -> binding commit -> capture link -> file version (soft) -> frontier ACK.

| Property | What must be durable | Base | New | Pinned by |
|---|---|---|---|---|
| (a) The intent line names a durable root | root closure + roots index | P0 only. The store's S1 ran after the intent | S1', inside the store, before the intent | TestObservationPublish_UnverifiableRootWritesNoIntent (RED on base); TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite |
| (b) The tool_use line becomes visible only over a durable root | same | P0 + S1 | S1'. Between S1' and the write, the only write is the intent line, which appendObservationIntent fsyncs together with its directory. Objects are immutable. S2 re-verifies the closure before commit | the same order test; TestObservationPublish_CrashAtEveryStepPublishesOnce (root-synced, intent-durable cuts) |
| (c) The record+marks are durable before the binding commits and before the capture link | tool_use.jsonl batch + index dir | S2 (F repeated it) | S2 | CrashAtEveryStep: record-written cut, run as a process crash and as a power loss; publication-synced cut |
| (d) The link is written before the frontier ACK | sidecar Published | order (atomic write), no pass | unchanged | TestCrashCutBetweenReferenceAndFrontierRedelivers; TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent |
| (e) The file-version record comes after the link | record (via S2) | no pass: soft, repaired on redelivery | unchanged | TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun; TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun (cut after every context check: 14 read / 15 stop checks now, 22/23 on base) |
| (f) No ACK without a publication | all of the above | unchanged | unchanged | TestPublicationOrderIsObjectReferenceFrontier; NEW TestObserverDurablePublisherFailureRemainsDrainRetryable |
| (g) A redelivery re-proves the original publication | closure + index | recovery S1 + S2 + F = 3 | unchanged, 3 | TestPublicationPasses_RedeliveryReprovesTheOriginalPublication; TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord |

Other cases:
- A cut between the put and the store's first barrier publishes once on redelivery: TestPublicationPasses_CutAfterThePutPublishesOnceOnRedelivery.
- A recorder without the declaration keeps 4 passes: TestPublicationPasses_RecorderWithoutTheBarriersKeepsTheObserversPasses.
- docs/architecture.md §4 and 00-ARCHITECTURE.md §0.2.2 describe the order but do not name the passes, so no docs change was needed.

2. WHAT CHANGED (implementer)
- 71803ec: new counter store.publication.sync.
- d55a59f:
  - SyncPublication now runs in publishObservation before the intent, outside obsPubMu.
  - completeIntentLocked takes rootSynced, true only when the same call synced the same root. RecoverToolUseByObservation passes false.
  - New store.DurableObservationPublisher declaration.
  - New obsPubFault step seam.
- ce39fb8, df68b3b: the observer gates P0 and F on recorderPublishesDurably. This covers the tool, prompt and subagent paths, fresh publication only.
- 3d1c6b1: new BenchmarkOnToolUse_TestOutput256KB_Leased.
- f133c9c: every-context-check sweep.
- 2f4ebbb: PublishesObservationsDurably added to the readonly allowlist, with a written justification.

Pass counts per capture:
- fresh leased: 4 -> 2
- redelivery: 3 (unchanged)
- non-declaring recorder: 4
- unleased: 0

3. MEASUREMENTS (implementer's runs; committed in fcaea14; product code unchanged since)
strace, Linux, per leased OnToolUse:
| Fixture | fsync base -> new | file syscalls base -> new |
|---|---|---|
| Deduped | 559 -> 281 | 6098 -> 3082 |
| Delta | 566 -> 287 | 6320 -> 3286 |
| AllNovel | 1197 -> 602 | 13664 -> 7163 |
Unleased counts are identical on both builds.

Paired ABBA A/B, 10 rounds per side, exact sign test. Ratios are new/base; ns/op is per op.
| Platform | Measure | Leased result |
|---|---|---|
| Windows | ns/op | 0.542 (AllNovel), 0.518 (Deduped), 0.521 (Delta); 10/10, p=0.0020 each |
| Windows | allocs/op | -32% / -25% / -23%; 10/10, p=0.002 |
| Linux | ns/op | AllNovel 0.465 (10/10, p=0.002); Deduped 0.587 and Delta 0.585 (8/10, p=0.109, NOT significant) |
| Linux | allocs/op | -29% / -21% / -20%; 10/10, p=0.002 |
- B/op, leased: -6% to -12% on both platforms.
- The unleased benchmark shows no significant change on either platform.
- Absolute times were taken on a co-loaded host and are NOT budget evidence. No budget constant or CARRIED-DEFECTS status was changed.

4. REVIEW RESOLUTION
The finding is minor. It splits into four parts:
(a) Claim: the observer over atomicFaultStore takes the old 4-pass branch, so TestObserverAtomicPublicationFailureRemainsDrainRetryable no longer exercises what production ships. REBUTTED.
- That test's request carries no Nonce, so ingest.leaseDelivery (ingest.go:1076-1079) leaves it unleased.
- ObservationFrom(ctx) is "", so the observer's leased code does nothing: 0 passes on both attempts, on base and head alike.
- The wrapper forwards neither PublicationSync nor ObservationRecovery. Given a LEASED delivery with no fault armed, it NAKs with 0 passes (observationRecord, identity.go:70-72). So it could never carry a leased capture on either branch.
- Evidence: runs/fix-seat/probe-atomic-fault-store-shape.txt, and the probe source as .txt. The probe was run once with the file in the package, then removed.
(b) Claim: no daemon-level fault/drain-retry test covers the declaring branch. CONFIRMED. The gap predates this branch: no daemon row put a leased store fault through the real observer at all. FIXED in 61baf28 with TestObserverDurablePublisherFailureRemainsDrainRetryable:
- The delivery is leased, through durableFaultStore, which embeds atomicFaultStore and explicitly forwards PublicationSync, ObservationRecovery and DurableObservationPublisher.
- After the refused RecordToolUseSuperseding it asserts: NAK, the WAL line byte-identical, no reference, no binding, sidecar unpublished, no ACK, 0 passes.
- After repair, with the drainer Run installs (dd.drainConfig()), it asserts: 1 record, binding committed, link written, ACK, exactly 2 passes, and a second drain pass that replays nothing, syncs nothing and appends nothing.
- Mutation-checked, each mutation reverted:
  - observer ignores the declaration: fails with 1 pass, want 0
  - declaring branch skips the link: fails
  - store skips its post-write sync: fails with 1 pass, want 2
  - FSStore declares false: fails at the fixture check
(c) The pin. FIXED in 61baf28 with TestStoreWrapperDropsTheDurablePublisherCapability. It is in the new file, so existing test files stay byte-unchanged. It pins four facts:
- A wrapper embedding the store.Store interface does not satisfy the capability.
- atomicFaultStore does not declare it.
- An explicit forwarder does.
- A type embedding *store.FSStore inherits it. That is the dangerous direction, the one documented at the interface.
(d) The stale comment. ROUTED to the daemon owner, with a corrected description. The phrase "the write path production actually takes" is inaccurate because the row is unleased while production deliveries carry a nonce. That inaccuracy predates this branch. It was not edited here.

5. CRITERION CHANGES
None. No threshold, budget, golden, skip or existing assertion was touched. One design note on the new row: it asserts that the live session's WAL is offset-marked and not replayed again, rather than removed. The production drainer (IsLive = registry) keeps a live session's WAL; the existing row's bare drainer treats every session as ended.

### Commits

- 71803ec feat(store): count publication sync passes (implementer)
- d55a59f fix(store): sync an observation's root before its intent (implementer)
- ce39fb8 fix(observer): drop tool capture passes the store now carries (implementer)
- df68b3b fix(observer): drop prompt and subagent passes the store carries (implementer)
- 3d1c6b1 test(observer): benchmark the leased 256 KB tool capture (implementer)
- b3c42c8 chore(v6): merge closeout/integration 6aff949 (implementer)
- f133c9c test(observer): cut a leased capture after every context check (implementer)
- 2f4ebbb fix(store): classify the durable-publisher declaration (implementer)
- fcaea14 docs(closeout): record the w4-syncs evidence (implementer)
- 61baf28 test(daemon): pin a leased store fault on the durable branch (FIX SEAT)
- 02c030f docs(closeout): record the w4-syncs fix seat's runs (FIX SEAT)

### Tests

- `go test ./internal/daemon -run '^(TestObserverDurablePublisherFailureRemainsDrainRetryable|TestStoreWrapperDropsTheDurablePublisherCapability|TestObserverAtomicPublicationFailureRemainsDrainRetryable|TestStoreWrapperDropsTheSupersedingCapability)$' -count=5 -v -timeout=10m (Windows, HEAD 61baf28)` — ok, 20 PASS (runs/fix-seat/win-daemon-durable-focused-61baf28.txt)
- `go test ./internal/daemon -count=1 -v -timeout=30m (Windows, co-loaded; tree = 61baf28 content)` — ok 292s, 720 PASS lines, 1 platform skip TestService_StateWriteFailureStillEmits; no wall-clock failures (runs/fix-seat/win-full-daemon-61baf28.txt)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w4-syncs --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-syncs/plans/sdd/V6-closeout/w4-syncs/runs/linux 61baf28 fixseat-daemon-durable --run '^(TestObserverDurablePublisherFailureRemainsDrainRetryable|TestStoreWrapperDropsTheDurablePublisherCapability|TestObserverAtomicPublicationFailureRemainsDrainRetryable|TestStoreWrapperDropsTheSupersedingCapability)$' --count 5 -- ./internal/daemon` — non-root uid 10001, -race: pass=20 fail=0, go_test_exit=0, source tree unchanged
- `go test ./internal/daemon -run '^TestObserverDurablePublisherFailureRemainsDrainRetryable$' -count=1 -v under four one-line mutations (each reverted with git checkout)` — all four FAIL as intended: M1 fixture declaration check; M1b 1 pass before write want 0; M2 link not written; M3 1 pass after repair want 2 (runs/fix-seat/mutation-*.txt)
- `scratch probe of atomicFaultStore (file temporarily in internal/daemon, run once with go test -v, then removed)` — existing row unleased (job.leased=false, ObservationFrom empty), 0 passes both attempts; leased delivery through it NAKs with 0 passes (runs/fix-seat/probe-atomic-fault-store-shape.txt)
- `go vet ./internal/daemon` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,sleepcheck` — all PASS
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/...` — exit 1 on the two pre-existing findings (spawn_stage_test.go:312 errcheck, handlers.go:803 gocritic) in files this branch does not touch; nothing in the new file

### Criterion changes

- None. No budget, threshold, golden, skip or existing assertion was changed, and existing test files are byte-unchanged. The new daemon row asserts that a live session's WAL is offset-marked and not replayed, instead of removed. That follows the production drainer (IsLive = registry). The existing row uses a bare drainer that treats every session as ended.

### Open issues

- The daemon owner needs to fix the atomicFaultStore doc comment (internal/daemon/observer_atomic_publication_test.go:20). It claims to cover 'the write path production actually takes', but the row is unleased (no nonce) and the wrapper cannot carry a leased delivery. This predates w4-syncs and is routed here, not edited.
- Pre-existing golangci-lint findings in internal/daemon: spawn_stage_test.go:312 errcheck and handlers.go:803 gocritic. They come from integration 6aff949, not from this branch.
- Linux: TestGC_DeadlineOvershootIsBoundedByTheCheckInterval (store) and x09 TestV3_LiveSessionWriteSetAndAppendOnly (e2e) fail on both the head and 6aff949 in the same window, per the implementer's runs. Pre-existing, not caused by this change.
- Linux paired A/B: the leased Deduped and Delta ns/op ratios (0.587, 0.585) are 8/10 rounds, p=0.109, not significant by the sign test. AllNovel and every alloc/B measure are significant. Windows is significant on all three.
- Absolute timings were taken on a co-loaded host. Whether B-C (l0_process p99 < 50 ms) now holds needs the coordinator's quiet C5.2 run. SP08-D1's CARRIED-DEFECTS status is left unchanged.
- Wrapper trap, documented and now pinned but not removed: a type embedding *store.FSStore that overrides RecordToolUse* without calling through still claims the barriers. No production or test type does this today.

### Needs the owner

- Daemon owner: correct the atomicFaultStore comment, or give that row a nonce and forward ObservationRecovery/PublicationSync. The new durable-branch row now covers the leased path.
- Coordinator: run the quiet C5.2 timing run of BenchmarkOnToolUse_TestOutput256KB_Leased and the B-C gate before any change to SP08-D1's status.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



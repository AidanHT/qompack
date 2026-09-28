# w6-ckptsync: missing durability barriers and the NTFS premise (D24)

Branch `closeout/w6-ckptsync`. Workflow `wf_8e58c74e-b50`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> **Coordinator note.** `74bdcd7` is a scratch commit that no branch holds (`f6095e2` plus the two cost benchmarks, used as the timing base). Its patch is saved at `runs/scratch-base-74bdcd7.patch`. `4b33c19` is w6-gcserial's head at the time; it is reachable from `closeout/integration` once w6-gcserial is merged.

## Implementer — status `done`, head `b51664b9f7cc6c266e6cbe13002f2fc71ab2cfd3`

### Root cause

On POSIX a file's fsync makes its bytes durable but not its directory entry, and an unsynced append can be lost however late it was written. Several writers returned or acknowledged "durable" while one of the two barriers was missing:
- Checkpoint Finalize never synced checkpoints/, the MANIFEST line or the segment marks.
- The pins log line was appended without a sync.
- GC tombstones were not synced before the irreversible sweep.
- New WAL segment names were never synced into spool/.
- Externalized blobs were named by an ACKed WAL line but never synced.
- Retention declarations were not synced before the backup manifest that relies on them.
- Backup and restore trees were published without syncing their directories.
- Directories created by EnsureLayout, by capture-sidecar sharding and by a restore into a new destination were never synced into their parents.
- The index/segments.jsonl name was never synced.
- record_eliminated's line and minted evidence were not synced, although the answer claims a durable record and no WAL line backs the call.

Separately, a0d6232's per-id re-mark added MarkEncoded calls to every seal. That broke the one-batch shape pinned by the cold PreCompact path.

### Summary

# w6-ckptsync report: missing durability barriers, and the D24 documentation

Branch `closeout/w6-ckptsync` in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync. Base f6095e2, head b51664b, working tree clean. Code head is 61c6c7f. Everything after it is evidence (plans/sdd/V6-closeout/w6-ckptsync/runs/). eddb208 → 61c6c7f changes comments only.

## 0. Resume handling

The earlier seats' work is 15 commits (2d12861..dc17169) plus an uncommitted segment-log draft. I reviewed all of it as a colleague's draft.

- **Kept:** all 15 commits.
- **Revised or completed:**
  - **402e44e** fixes a test. 703aee5's `TestRetentionRoots_ABackupsDeclarationsAreDurableBeforeItsManifest` expected exactly one barrier step. 06c40ca later routed the backup tree's directory syncs through the same `Migrator.barriers`, so the test FAILED at e6bc7a8 (13 extra dir steps). Its full store run had been killed at the pause, so nobody saw it. The test now filters to file syncs. It is a new test in this branch, not a pre-existing one.
  - **eddb208** fixes a real regression from a0d6232. Its per-id re-mark added four `MarkEncoded` calls to every seal. `TestColdPreCompactCatchesUpInOneBatchOldestFirst` failed (5 batches, want 1). My Windows full checkpoint run at 77a6524 found it (runs/red/red-coldpath-batch.txt). The seal now asks the log (`SegmentLog.Get`) and re-marks, in one batch, only the segments the log has lost. That is none in an ordinary seal, and only the ones a power cut took after a restart. The power-loss cut rows still exercise the re-mark path.
  - **7f5d7a4** is the second seat's uncommitted segment-log draft. I re-ran its RED on an archive and committed it.
  - **77a6524** completes 06c40ca: a restore destination that Restore itself creates is now synced into its parent.
  - **8428cbb** refines 3961a2f's D24 text.
- **One earlier position reversed:** 3961a2f's docs called the whole elimination log "unsynced by design". I reversed that for `record_eliminated` (2df3776, section 2).
- **Runs cut short by the pauses are not used.** The earlier seat's win-full-* logs, its Linux cf9537c run and my own stopped 77a6524 Linux run were set aside. Every result below was re-run.

## 1. Checkpoint seal (item 1)

**Root cause.**
- `paths.CreateNew` fsynced the artifact but not `checkpoints/`.
- `AppendManifest` appended through `AppendJSONL`, which does not sync.
- `Finalize`'s segment marks (`index/segments.jsonl`, appended by the idle `Advance`) were never synced before the seal.
- On POSIX, a power cut after `Finalize` returned could therefore lose:
  - the artifact's name (a MANIFEST line naming a missing artifact, which the reader refuses);
  - the MANIFEST line itself (the checkpoint un-sealed after the draft was retired and the PreCompact answer sent);
  - the marks (the DPI guard broken, so a later draft re-encodes sealed segments).

**Order now, pinned by `TestFinalizeSealsThroughItsBarriersInOrder`.** Each of these is durable before the next step:
1. The artifact's bytes (file sync).
2. The segment marks: lost marks re-marked, then `segLog.Sync`, which is a file sync plus an `index/` sync once per log lifetime (7f5d7a4).
3. `checkpoints/` synced, for the artifact's name.
4. The MANIFEST line synced. This is the seal.
5. `checkpoints/` synced again, when this seal created the MANIFEST.
6. Only then: pins view, draft retirement, successor draft, `precompact.json`, the PreCompact answer, and the next rehydration.

**Crash injection.** `TestFinalizeCutAtEveryBarrierLeavesTheCheckpointAbsentOrSealed` cuts at every barrier and after the last one. Each cut runs as a process crash and as a POSIX worst-case power loss, for both a first seal and a later seal. Every cut leaves the checkpoint either absent (its draft on disk and sealable on resume, with the segment then marked) or sealed with its marks.

**Degraded path.** `TestFinalizeSealsWhenItsSegmentMarksCannotBeSynced`: a failure to sync the marks degrades the seal but does not refuse it. It is counted (`checkpoint.segment_marks_unsynced`) and logged Loud.

**fsck.** fsck's orphan repair goes through the same `AppendManifest`.

## 2. Audit of every writer (item 2)

Legend:
- **REQ** — barrier required, because something durable depends on it.
- **OK** — already complete (WriteAtomic = staging fsync, rename, then directory fsync).
- **NO** — not required, with the reason.

**Checkpoint**
- `<seq>` artifact and MANIFEST: REQ, fixed (2d12861, a0d6232).
- Segment marks: REQ, fixed (a0d6232, 7f5d7a4, eddb208).
- fsck orphan line: REQ, fixed (through `AppendManifest`).
- Draft and `precompact.json`: OK (WriteAtomic).
- `.stale.json` set-aside rename: NO. It is evidence only.

**Delivery and spool**
- WAL segments: REQ. The ACK depends on the segment's name, so the spool directory is synced when a segment is opened (8a7459b).
- `spool/blob-*.bin`: REQ. The WAL line and the ACK name it, so the blob and then its directory are synced before the line (1a4311f; dc17169 opens only a regular file).
- Hook client fallback spool: NO. The hook exits 0 whatever happens and nothing acknowledges the line. The drain syncs file and directory before consuming it.
- Lease and ack journals: OK. They are created by WriteAtomic and every append is synced.
- v1 and v2 position seals: OK (WriteAtomic; `SyncData` in place).
- Delivery segments (log, head, staging): OK. The log's name is covered by the head's state/ fsync.
- Generation store, radix, terminal, frozen seal, drain state: OK. `state/delivery-generations/` is created on demand. Nothing depends on it before a segment transition, and that transition's head write fsyncs `state/`.
- Daemon lock and spawn lock (CreateNew), heartbeat: NO. They are stale after a power cut anyway.
- Staged daemon binary: NO. It is SHA-verified before use; a lost rename means it is staged again.

**Store**
- Objects: OK by design. A publication pass syncs them before any reference (w4, w5).
- `roots.jsonl` and `tool_use.jsonl`: OK (publication pass).
- GC tombstones: REQ. A deletion cannot be undone, so they are synced before the sweep (6b9381b). The change is the barrier only; it merges clean with w6-gcserial.
- Observation intent: OK (syncData plus `index/`).
- `index/files.jsonl`: NO (soft, repaired on redelivery; w4 row e).
- `index/sessions.jsonl`: NO (recomputed from the tool-use index).
- `files.json`, store state, pending-put markers: OK.
- Capture sidecar, `records/captures/<shard>/`: REQ. It is publication stage one, before the reference and the ACK. Its shard directory is made on demand and was never synced into its parent. Fixed by 5acdfb8 through the new `paths.Barriers.MkdirAll`: at most one sync per new shard (256 per project) plus one for `captures/`.
- The sidecar's retention declaration: NO. GC never walks `records/`, and a capture-domain hash cannot equal a chunk's hash. This is documented in the code, and must change if GC ever manages `records/`.
- `state/retention-roots.jsonl`: REQ. It is declared before the backup manifest, the mapping and the drill that name it (703aee5; one sync per batch).
- Retention compaction, `gc.json`, gc live set: OK.
- Backup tree, manifest and certification marker: REQ, fixed (06c40ca).
- Restore staged tree, publish, destination: REQ, fixed (06c40ca, 77a6524).
- Legacy `RestoreBackup` (the drill target): NO. It is a rehearsal target.
- Migration import (mapping log before the cursor, and its objects): this is a REAL GAP but it is UNREACHABLE. `config.LegacyImportGate` is closed and there is no production caller. Not fixed; see open issues.
- Migration new-format write log before the handoff: same gate, same status.
- Drill log: NO. A lost line makes cutover refuse.
- Demand log and its compaction rename: NO (telemetry).
- Segment filters: OK.
- Quarantine renames: NO (evidence).

**Pins**
- `invariants.jsonl`: REQ. The user was told "pinned", and the view derives from the log (ac0bd13).
- The view: OK (`ReplacePinsView`).

**Negative knowledge**
- `record_eliminated` / `IngestMCP` (and `IngestPin`): REQ, fixed in 2df3776.
  - The MCP answer is documented as "durable — this tool writes a persistent record".
  - The call is handled in the daemon, not through the WAL, so log.go's premise ("the spool is the boundary") does not cover it.
  - The minted evidence was also never published.
  - Now: evidence publication pass, then the line's file sync, then `records/` once per ledger lifetime. A failed barrier fails the call.
- Detector and user-statement lines, and the signal log: NO. They arrive through the spool, answer no one, and a lost tail costs one elimination by design. `TestRecord_AnUnacknowledgedSourcePaysNoBarrier` pins that they pay nothing.
- `tried.bloom` (`ReplaceBloom`, no syncs): NO. It is a cache: CRC-checked, rebuilt from the log when missing or corrupt, and count-reconciled.

**DAG**
- Log (synced per flush; name not synced at creation): NO. A lost batch degrades ranking to the documented recency tiebreak (rehydrate/build.go), and pending records are already lost on any process crash.
- Compact: OK.

**Other**
- EnsureLayout directories: REQ. Every durable file lives inside them (1b33b0d).
- Rehydrate state, calibration, contract files, mcp observable and promotions, observer state, metrics, scheduler state, session recovery, pending config, `config set`: OK (all WriteAtomic into EnsureLayout directories).
- Chunk cache: NO (cache).
- Day logs, rotation, loud log, hook quiet log: NO (diagnostics).
- selftest, cli fault, eval corpus/importer, pluginmanifest: NO (test tooling, re-importable operator artifacts, build-time).

## 3. D24 (item 3)

- **docs/architecture.md §4.** "Windows directory sync (owner decision D24)" now says:
  - what NTFS journals: metadata only, not file contents;
  - what a flush covers: it forces the log out through the file's own latest change, so earlier metadata is durable too;
  - what a power cut can still take: unflushed bytes, and a rename, creation or deletion after the last flush — a WriteAtomic replacement can revert to its previous complete version, and a removed file can reappear;
  - why no guarantee depends on more: every durable promise ends with a file flush after its directory change, and the files that can revert are derived, or leave a state the operator can retry from;
  - that the premise is untested with a real power cut, and that D24 declines the working CreateFile(GENERIC_WRITE|BACKUP_SEMANTICS) barrier.
- **Lists in the same section.** The durability list and the "unsynced by design" list were updated to match the audit.
- **docs/security.md §8** says the same in short.
- **`paths.SyncDir`'s doc comment** cites D24 and states the premise and the residual risk.
- `go test ./test/docs` and the three gen-*-docs --check runs are ok.

## 4. Cost

**Barrier counts per operation**, from the counting seams and pinned by the tests named. Directory syncs are no-ops on Windows.

| Operation | Added barriers (Linux) | Windows | Pinned by |
|---|---|---|---|
| Checkpoint seal | 1 file sync → 3 file + 1 dir; +1 dir when it creates MANIFEST; +1 dir (`index/`) once per daemon | 1 → 3 FlushFileBuffers | TestFinalizeSealsThroughItsBarriersInOrder |
| Pin add/remove | +1 file; +1 dir for the project's first pin | +1 flush | TestPinsAReportedChangeSurvivesAPowerCut |
| GC pass that retires roots | +1 file per pass | +1 flush | TestGC_TombstonesAreDurableBeforeTheSweepDeletesTheirChunks |
| WAL segment open | +1 dir per segment open (per session per daemon run, per rotation) | none | TestIngest_AWALSegmentsNameIsDurableBeforeItsFirstLineIsAcked |
| Externalized request (≥1 MiB line) | +1 file (the payload) + 1 dir | +1 flush | TestIngest_AnExternalizedPayloadIsDurableBeforeItsLineIsAcked |
| `record_eliminated` | +1 publication pass (3 file + 4 dir for a one-chunk reason; counts from a temporary scratch diagnostic, not committed) + 1 file; +1 dir once per ledger | +4 flushes | TestIngestMCP_PublishesItsEvidenceThroughTheRealStore (one pass) |
| Retention roots | N unsynced appends → 1 write + 1 file per batch | — | TestAppendLinesDurable_SyncsABatchOnce |
| Backup | +1 dir per directory in the tree, +1 (`backup/`), +2 (marker) | none | TestMaintenance_ABackupIsDurableBeforeItIsCertified |
| Restore | +1 dir per staged directory, +1 (destination), +1 (its parent, if created) | none | TestMaintenance_ARestoreIsDurableBeforeItIsReported |
| EnsureLayout | 3 dir syncs once for a fresh project | none | TestEnsureLayout_MakesTheDirectoriesItCreatesDurable |
| Capture sidecar | +1 dir per new shard, +1 for `captures/` | none | TestCaptureSidecar_ANewShardIsDurableBeforeTheSidecarIsWritten |

The steady-state hot path (leased Accept) is unchanged.

**Linux paired timing.** Via the gate script: `--no-race`, `--count 5`, `GOFLAGS=-bench=…`, 6 ABBA rounds. Base is scratch commit 74bdcd7 (f6095e2 plus the two new benchmark files; never a branch). Head is 312e15a. Results are head/base geo-mean ratios:

| Benchmark | Ratio | Head slower | Sign test p | Per-round range |
|---|---|---|---|---|
| BenchmarkIngestAcceptExternalized | **2.32** | 6/6 | 0.031 | 1.36–3.14 |
| BenchmarkIngestMCPRealStore | **3.59** | 6/6 | 0.031 | 2.71–4.36 |
| BenchmarkIngestAccept (control) | 1.13 | 3/6 | 1.0 | 0.56–2.52 |
| BenchmarkIngestAcceptLeased (control) | 0.89 | 2/6 | 0.69 | — |
| BenchmarkRecord (control) | 0.90 | 3/6 | 1.0 | — |

- The container was heavily co-loaded; one WAL fsync took about 20 ms. Absolute times are not budget evidence: externalized 26→59 ms, `record_eliminated` 56→203 ms, against B-F's 250 ms p95 budget.
- **BenchmarkFinalize could not be paired on Linux.** One sample took about 9 minutes under that load.
- **Windows BenchmarkFinalize ABBA** (6 rounds, 5x): ratio 0.927, head slower 2/6, p=0.69. Base and head both ran at 107–266 ms/op, so noise swamps the +2 flushes.

## 5. Tests (all at head-equivalent commits; see runs/INDEX.txt)

- **RED logs:** runs/red/ has one log per fix, and runs/red/INDEX.txt names each archive and failing test.
- **Windows full runs** (`go test ./internal/<pkg> -count=1 -timeout=70m -v`):
  - paths, pins, store at 77a6524: ok. negknow at 77a6524: 1 co-load failure (below).
  - These four are head-equivalent: none of them imports internal/checkpoint (`go list -deps -test`), and 77a6524 → head changes only internal/checkpoint/finalize.go.
  - checkpoint, daemon, cli at 312e15a: all ok.
- **Linux gate, eddb208, non-root, -race, the seven touched packages:** every package PASS except daemon, which had 1518 pass and 1 fail: `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest` ("3 of 5 arrivals acknowledged").
  - Re-run alone: `--run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 10` gave 10/10 PASS.
  - This is the known co-load row (w3, w5-dirsync; w6-linuxrows owns it).
- **negknow co-load failure:** `TestBudget_RefreshStaleness` failed once (10.7 ms wall/op against a 10 ms budget) in the Windows full run.
  - Alone at the head: 2/3, then 10/10.
  - At the base: 3/3 and 10/10, in the same 2.3–6.0 ms band.
  - This branch does not touch that path.
- **Focused rows at 312e15a on Windows, all PASS:**
  - mcp and commands `record_eliminated` and pin rows;
  - fault and integration rows: TestFault_CheckpointDropsAnUnresolvablePointer, TestFault_CheckpointResolvabilityIsBlindToADeletedObject, TestFault_PublicationBoundaries, TestFault_Lifecycle, the three TestV6Backup_ rows, TestV4_GrowthGuardrailWithCheckpointsAndEphemerals, TestV5_GrammarAndPromotionCoexistInFinalize, TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly;
  - six e2e checkpoint, rehydrate and elimination rows.
- **Lint subset at b51664b:** `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS.
- **Formatting and vet:** fmt-check ok. `go vet` passes on Windows and with GOOS=linux for all seven packages.
- **Merges:** `git merge-tree` against w6-borrow, w6-config, w6-linuxrows and w6-gcserial is clean for all four.

## 6. Process note

I stopped my first timing driver at its 20-minute cap, but its host shell survived the stop. It kept launching runs until about 19:5x UTC, co-loading ABBA rounds 1–6. I killed it and its container processes by my own run IDs. Its runs are unpaired or partial and are not in the evidence. No process of mine is still running, on Windows or in the container.

## 7. Criterion changes

- **402e44e.** A new branch test (not yet merged) now filters its step list to file syncs. The property it pins is still "one sync for the batch, before the manifest exists".
- **The restore test** gained a stronger assertion: the destination's parent is synced first.
- **Existing tests:** no pre-existing test file was modified. No skip, threshold, golden or timeout was changed.

### Commits

- 2d12861 fix(paths): make a manifest line durable after its artifact name (earlier seat; reviewed, kept)
- a0d6232 fix(checkpoint): seal once its marks, name and line are durable (earlier seat; kept, re-mark revised in eddb208)
- ac0bd13 fix(pins): sync a pin's log line before its view changes (earlier seat; kept)
- 6b9381b fix(store): sync gc tombstones before the sweep deletes chunks (earlier seat; kept; barrier only, merges clean with w6-gcserial 4b33c19)
- 8a7459b fix(daemon): sync the spool dir when a wal segment is opened (earlier seat; kept)
- 703aee5 fix(store): sync retention roots before the artifact naming them (earlier seat; kept, its test repaired in 402e44e)
- 06c40ca fix(store): make backup and restore trees durable before use (earlier seat; kept, completed by 77a6524)
- 1b33b0d fix(paths): make the directories EnsureLayout creates durable (earlier seat; kept)
- 1a4311f fix(daemon): sync an externalized payload before its wal line (earlier seat; kept)
- 3961a2f docs(durability): state the barrier order and the D24 premise (earlier seat; kept, refined by 8428cbb)
- cf9537c docs(checkpoint): count the seal barriers in Finalize's cost note (earlier seat; superseded by 61c6c7f)
- 46d6529 test(store): check the type assertions errcheck flagged (earlier seat; kept)
- e6bc7a8 test(checkpoint): pin the seal a failed marks sync degrades (earlier seat; kept)
- dc17169 fix(daemon): open only a regular file for the blob barrier (second seat; kept)
- 402e44e test(store): count only the batch's file syncs for retention roots
- 7f5d7a4 fix(store): make the segment log's name durable before a seal (second seat's uncommitted draft; RED re-run, committed)
- 2df3776 fix(negknow): sync an acknowledged elimination before answering
- 8428cbb docs(durability): say what NTFS journals and what a cut can take
- a53e544 docs(closeout): index the w6-ckptsync red evidence
- 9086913 test(durability): price the blob and elimination barriers
- 5acdfb8 fix(store): sync a new capture shard before its sidecar
- 77a6524 fix(store): sync a restore destination the restore creates
- eddb208 fix(checkpoint): re-mark only the segment marks the log lost
- 61c6c7f docs(checkpoint): count the seal's barriers exactly in Finalize's note
- 312e15a docs(closeout): record the cold-path red the full run found
- 5b8ca0f docs(closeout): record the w6-ckptsync full runs and timing
- b51664b docs(closeout): drop the aborted timing driver's unpaired runs

### Tests

- `go test ./internal/checkpoint -run '^(TestFinalizeSealsThroughItsBarriersInOrder|TestFinalizeCutAtEveryBarrierLeavesTheCheckpointAbsentOrSealed|TestFinalizeSealsWhenItsSegmentMarksCannotBeSynced|TestColdPreCompactCatchesUpInOneBatchOldestFirst)$' -count=1 -v (Windows, eddb208)` — PASS (4/4) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/negknow -run '^(TestIngest_AnAcknowledgedEliminationIsDurableBeforeItIsAnswered|TestIngest_AnEliminationWhoseSyncFailsIsNotAcknowledged|TestRecord_AnUnacknowledgedSourcePaysNoBarrier|TestIngestMCP_PublishesItsEvidenceThroughTheRealStore)$' -count=1 -v on a 7f5d7a4 archive without durable.go (runs/red/red-negknow-ack.txt), then at 2df3776` — RED: 3 FAIL + the no-barrier half PASS; GREEN: 4/4 PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/paths ./internal/store -run '^(TestBarriersMkdirAll_SyncsTheParentOfEveryDirectoryItCreates|TestCaptureSidecar_ANewShardIsDurableBeforeTheSidecarIsWritten)$' -count=1 -v, archive with Barriers.MkdirAll stubbed (runs/red/red-sidecar-shard.txt), then at 5acdfb8` — RED: both FAIL; GREEN: both PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/store -run '^TestMaintenance_ARestoreIsDurableBeforeItIsReported$' -count=1 -v, 5acdfb8 archive + extended test (runs/red/red-restore-dest.txt), then at 77a6524` — RED FAIL; GREEN PASS
- `go test ./internal/store -run '^TestSegmentLog_SyncMakesTheLogsNameDurableOnce$' -count=1 -v, dc17169 archive with the pre-fix Sync (runs/red/red-segdir.txt), then at 7f5d7a4` — RED FAIL; GREEN PASS
- `go test ./internal/<pkg> -count=1 -timeout=70m -v for paths, pins, negknow, store (Windows, 77a6524; head-equivalent: none imports internal/checkpoint, only finalize.go changed since)` — paths ok (231 pass, 7 platform skips); pins ok (59); store ok (858, 1 existing symlink skip); negknow 399 pass + TestBudget_RefreshStaleness co-load FAIL (10.7ms vs 10ms)
- `go test ./internal/negknow -run '^TestBudget_RefreshStaleness$' -count=5 -v, alternating head and base, twice each (Windows; runs/win/refresh-staleness-ab.txt)` — head 10/10 PASS, base 10/10 PASS, both 2.3-6.0 ms wall/op: the full-run failure was co-load
- `go test ./internal/checkpoint -count=1 -timeout=70m -v (Windows, 77a6524)` — FAIL: TestColdPreCompactCatchesUpInOneBatchOldestFirst (5 batches, want 1) - a0d6232 regression, fixed in eddb208
- `go test ./internal/<pkg> -count=1 -timeout=75m -v for checkpoint, daemon, cli (Windows, 312e15a)` — all ok: checkpoint 430 pass/2 skip, daemon 1512 pass/1 existing skip, cli 419 pass
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-ckptsync --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync/plans/sdd/V6-closeout/w6-ckptsync/runs/linux eddb208 touched-full --timeout 90m -- ./internal/paths ./internal/pins ./internal/negknow ./internal/checkpoint ./internal/store ./internal/daemon ./internal/cli` — non-root -race: paths 197/18 skip, pins 59, negknow 400/1 skip, checkpoint 432, store 859/4 skip, cli 419 PASS; daemon 1518 pass, 1 FAIL TestDeliveryOrder_LaneOverflowIsDrainedOnRequest (known co-load row)
- `linux-nonroot-gate.sh ... eddb208 lane-overflow-alone --run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 10 --timeout 30m -- ./internal/daemon` — 10/10 PASS alone
- `go test ./internal/mcp ./internal/commands -run '^(TestRecordEliminatedWritesLedgerRecord|TestRecordEliminatedStoresEvidence|TestRecordEliminatedResolvesDependsOnHashes|TestRecordEliminatedUnresolvedDependencyReported|TestRecordEliminatedDefaultsScopeFromConfig|TestRecordEliminatedIsNotEphemeral|TestPin_EliminationGoesThroughTheSP13Handler|TestPin_AddRecordsUserAuthorityByDefault|TestPin_RemoveAppendsATombstoneAndKeepsTheAddRecord)$' -count=1 -v (Windows, 312e15a)` — 9/9 PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/fault ./test/integration -run '^(TestFault_CheckpointDropsAnUnresolvablePointer|TestFault_CheckpointResolvabilityIsBlindToADeletedObject|TestFault_PublicationBoundaries|TestFault_Lifecycle|TestV6Backup_RoundTripPreservesSourceLaterWritesAndBackup|TestV6Backup_RefusesLiveWriterAndTargetConflict|TestV6Backup_CorruptedAndUnknownSchemaRefusedWithoutOverwriting|TestV4_GrowthGuardrailWithCheckpointsAndEphemerals|TestV5_GrammarAndPromotionCoexistInFinalize|TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly)$' -count=1 -timeout=40m -v (Windows, 312e15a)` — 10/10 PASS (fault ok 314s, integration ok 69s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact|TestE2E_SessionStartCompactRestoresCheckpointItems|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction|TestV5_EliminationThroughEveryFourSurfaces|TestV5_PreCompactToRehydrateToDroppedRoundTrip)$' -count=1 -timeout=50m -v (Windows, 312e15a)` — 6/6 PASS (ok 64s) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `bench-abba.sh (runs/linux/bench/bench-abba.sh.txt): linux-nonroot-gate.sh <74bdcd7|312e15a> benchq-rN-... --no-race --run '^$' --count 5 --env GOFLAGS=-bench=^(BenchmarkIngestAccept|BenchmarkIngestAcceptLeased|BenchmarkIngestAcceptExternalized|BenchmarkIngestMCPRealStore|BenchmarkRecord)$ -- ./internal/daemon ./internal/negknow, 6 ABBA rounds; python bench-pairs.py` — head/base geo ratios: Externalized 2.32 (6/6, p=0.031); IngestMCPRealStore 3.59 (6/6, p=0.031); controls Accept 1.13 (3/6), AcceptLeased 0.89 (2/6), Record 0.90 (3/6); co-loaded, ratios only
- `Windows ABBA of BenchmarkFinalize via ckpt-<base|head>.test.exe -test.run '^$' -test.bench '^BenchmarkFinalize$' -test.benchtime 5x, 6 rounds (runs/win/finalize-ab-pairs.txt)` — ratio 0.927, head slower 2/6, p=0.69: no detectable effect (noise 107-266 ms/op on both)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (b51664b)` — all 8 PASS
- `go run ./tools/devtool fmt-check; go vet (7 touched pkgs, Windows and GOOS=linux); go test ./test/docs -count=1; go run ./tools/devtool gen-mcp-docs/gen-config-docs/gen-command-docs --check` — all exit 0

### Criterion changes

- 402e44e changes TestRetentionRoots_ABackupsDeclarationsAreDurableBeforeItsManifest, a test new to this branch and not yet merged. It now filters its exact step list to file syncs before asserting "one sync for the whole batch". Reason: 06c40ca routed the backup tree's directory syncs through the same Migrator barriers, so the unfiltered list also contained 13 correct tree-directory steps, and the test failed at e6bc7a8. The ordering assertion is unchanged: the declarations are durable before the manifest exists. The tree's directory syncs are pinned by TestMaintenance_ABackupIsDurableBeforeItIsCertified.
- 77a6524 strengthens TestMaintenance_ARestoreIsDurableBeforeItIsReported, also new to this branch. It now also requires that the parent of a destination the restore creates is synced first.
- No pre-existing test file was modified: every changed *_test.go file is new on this branch. No skip, threshold, golden, timeout or budget was changed.

### Open issues

- Legacy migration import (internal/store/migrate.go) has the same missing-barrier class but is unreachable today because LegacyImportGate is closed and there is no production caller. Its mapping line is appended unsynced before the durable writeCursor, and the imported objects are never published. A power cut could leave the cursor ahead of lost mapping lines, which the re-run then skips for good (parity would catch it). RecordNewFormatWrite has the same shape against the handoff record. Fix before the gate opens (SP-20 M1-04 owner).
- Linux TestDeliveryOrder_LaneOverflowIsDrainedOnRequest failed once in the co-loaded touched-full run and passed 10/10 alone at eddb208. This is the known load-sensitive row (w3, w5-dirsync); w6-linuxrows owns it.
- Windows TestBudget_RefreshStaleness failed once under co-load (10.7 ms against 10 ms). Head and base are indistinguishable when re-run (10/10 each), and this branch does not touch the path. It is a wall-clock budget running within about 2x of its ceiling on this host.
- Windows BenchmarkFinalize runs at 107-266 ms/op on base and head alike, against the <50 ms exit criterion. This is pre-existing (SP10-D1 recorded 264-284 ms), not caused by this branch. Linux Finalize could not be paired here because one sample took about 9 minutes under co-load; the quiet C5.2 run should time it.
- Small redundancy: a session whose first request is externalized syncs spool/ twice, once for the blob and once when its WAL segment opens. This happens once per session per daemon run. Left in place.
- Backup cost scales with the tree: one directory fsync per directory, and a large store can have up to 65,536 object leaf directories. Correctness needs it, but it has not been measured on a large store.
- The NTFS-journaling premise (D24) is documented but has not been tested with a real power cut.
- My first timing driver's host shell survived its stop and co-loaded the ABBA rounds until I killed it. Evidence was kept only for the paired benchq rounds, and the controls show the noise floor. Pattern to remember: stopping a background shell does not stop its sh children.

### Needs the owner

- No new budget, bound or constant numbers were introduced; nothing needs numeric approval.
- Confirm reversing the earlier seat's 'elimination log is unsynced by design' for record_eliminated (2df3776). The MCP answer is documented as durable, but no spool line backs it. The cost is one evidence publication pass (3 file + 4 dir syncs for a one-chunk reason) plus one log file sync per call: about 3.6x on co-loaded Linux (56→203 ms absolute there) and about 70 ms/op in a Windows smoke run, against B-F's 250 ms p95. The alternative is to sync only the log line and accept that evidence can be lost.
- Accept the externalized-payload cost inside the B-B histogram (1a4311f). Requests whose line is at least 1 MiB now pay a payload fsync plus a spool/ directory fsync before the WAL line: 2.32x on co-loaded Linux (26→59 ms absolute there). The alternative is to have the hook fsync the blob before sending, which is the same user-visible latency but outside B-B.
- Checkpoint seal cost for the C5.2 quiet run: steady state goes from 1 to 3 file syncs + 1 directory sync (Windows: 1 → 3 FlushFileBuffers). The Windows paired timing showed no detectable effect (ratio 0.927, p=0.69); Linux timing is pending a quiet host.
- Route the legacy-import mapping→cursor and new-format→handoff barrier gaps to the SP-20 M1-04 owner before the LegacyImportGate opens.

## Independent review

### review:ckptsync: needs-fixes

- **minor** `internal/paths/barriers.go:128 and :156 (AppendLinesDurable); same pattern in internal/paths/layout.go:129 (EnsureLayout) and internal/paths/barriers.go:57-78 (Barriers.MkdirAll)` — The directory barrier does not fail closed across a retry. Whether to sync the directory is decided by an Lstat taken before the write: "file/dir did not exist" means sync its parent. If that sync fails, the call returns an error, but the file or directory is already there. The next call's Lstat therefore sees it, skips the directory sync, and returns success. From then on a name that was never made durable is reported as durable.
  - Evidence: barriers.go:128 `created := errors.Is(statErr, fs.ErrNotExist)` gates barriers.go:156 `if created { return x.syncDir(filepath.Dir(p)) }`. EnsureLayout and MkdirAll likewise sync only parents of directories that were missing at entry. Scenario on POSIX: the first `qompack pin` creates pins/invariants.jsonl, the file sync succeeds, the pins/ fsync returns EIO, and Add fails. The second pin appends, syncs the file, skips the directory sync, and prints "pinned". A power cut then loses the log's name and every pin in it. The same applies to a capture shard directory, a restore destination and a fresh .qompack/ layout. The branch's own segLog.Sync (dirSynced) and negknow syncAcknowledged (logNameDurable) avoid this by recording success and retrying until the barrier succeeds. These writers do not.
  - Fix: Make the directory barrier retry until it succeeds. Option (a): when the file/dir exists, sync its directory unconditionally once per process per path, tracked in a sync.Map of paths whose directory barrier has succeeded. Option (b): for AppendManifest-style callers, always sync the directory first, as AppendManifest already does. For EnsureLayout and MkdirAll, record the parents still to sync and retry them on the next call (or sync every parent in the chain whenever any sync failed earlier). Add a test that fails the first directory sync, then calls again and requires a directory sync on the second call.
- **minor** `internal/checkpoint/finalize.go:139-146 (AppendManifest error branch); internal/paths/barriers.go AppendManifest steps 2-3` — When a barrier fails after the MANIFEST line has been written, Finalize reports "not sealed" even though the line is visible to readers. This happens when the manifest's file sync fails, or when the step-5 checkpoints/ sync fails after the line is already file-synced. Finalize returns an error and `committed` stays false, so the draft is unsealed and kept in memory at seq N. The artifact at N and its line both exist. The next Finalize of the same in-memory draft gets ErrExist from CreateNew(N), moves to N+1 and seals a duplicate checkpoint. reconcileEncodedSeq then logs Loud seq_reference_drift, because the segments are already marked into N. The crash-injection rows cut the process at these barriers but never let the process continue after a barrier error, so this live path is untested.
  - Evidence: finalize.go:139 `if err := w.barriers.AppendManifest(w.l, entry); err != nil { ... return Ref{}, fmt.Errorf("checkpoint: manifest append %04d: %w", seq, err) }`. At that point the deferred d.unseal() runs. barriers.go AppendLinesDurable writes the line and then calls x.syncFile(f), and AppendManifest's third step runs syncDir after the line is durable. In finalize_durability_test.go runSealCut, a cut at stepManifestFile snapshots the disk and "crashes"; the in-process retry after an error is never exercised.
  - Fix: Have AppendManifest return a distinguishable error, for example a wrapped paths.ErrManifestLineUnsynced, once the line has been written. On that error Finalize should treat the draft as committed: set committed=true, retire the draft or at least never re-seal it at a new seq, and return a Loud, counted error that the seal is visible but may not be durable. Alternatively, on any AppendManifest error, re-read the manifest for seq before unsealing. Add a row that fails (does not cut) the manifest file sync and the step-5 directory sync, then retries PreCompact, and requires no duplicate seal and no drift.
- **minor** `internal/paths/barriers.go:57-78 (Barriers.MkdirAll), used by internal/store/capture_sidecar.go writeCaptureSidecar` — MkdirAll's "sync only what this call created" rule races with concurrent callers in the same process. Worker A creates a new records/captures/<shard>/ but has not yet synced captures/. Worker B's Lstat then sees the shard, so B syncs nothing, writes its sidecar through WriteAtomic (which syncs only the shard directory) and proceeds to reference and frontier-ACK it. A power cut before A's parent sync takes the shard and B's sidecar, although B's later records are durable.
  - Evidence: The daemon runs multiple ingest workers (ingest.go:746 defaultWorkerCount), and each calls store.WriteCaptureSidecar (ingest.go:1205). test/guards/sharedreaders_test.go documents concurrent sidecar writers (live and drain). MkdirAll decides `created` from a per-call Lstat, with no in-process coordination. The window is one fsync long but real, and it is exactly the property 5acdfb8 claims to establish.
  - Fix: Serialize creation with its barrier. Hold a package-level (or per-parent) mutex across the Lstat, MkdirAll and parent-sync sequence in Barriers.MkdirAll, so a caller that observes an existing directory knows its entry is already durable. Or keep an in-process set of directories whose parent sync has completed, and make a caller that finds the directory present but not yet in the set perform (or wait for) the sync. Extend TestCaptureSidecar_ANewShardIsDurableBeforeTheSidecarIsWritten with two concurrent writers into one fresh shard.
- **minor** `docs/architecture.md:392-396 ("Why that is enough"); docs/security.md:307-313; internal/paths/atomic.go SyncDir D24 comment` — The D24 residual-risk statement leaves out one promise it does not cover: a certified backup. It claims every durable promise ends with a file flush after the directory change it relies on. Backup certification does not. Its last steps on Windows are the manifest's WriteAtomic rename (after the staging flush) and the removal of the certification marker, and no flush follows either. A power cut after `qompack backup` reports certified can therefore revert the manifest to absent or bring the pending marker back. The docs' list of files that can revert names "a restore's final rename" but not the backup manifest or its certification.
  - Evidence: internal/store/backup.go:377 `paths.WriteAtomic(filepath.Join(dir, backupManifestFile), ...)` is the backup's last write. internal/store/maintenance.go TakeBackup then does os.Remove(pending) plus DirBarrier(backup/), which is a no-op on Windows under D24. paths has no MOVEFILE_WRITE_THROUGH or FILE_RENAME write-through flag (grep finds none). The docs list "Backup and restore" among the promises and then state "each write listed above ends with a file flush after the directory change it relies on".
  - Fix: Add the backup manifest and certification to the "can revert" list in all three places, with the consequence: the backup reads as uncertified or incomplete and must be retaken, which is safe because verification refuses it. Alternatively, give the certification a final file flush after the rename, such as a synced append to a backup log. Keep the three statements identical.
- **minor** `internal/store/migrate.go (Migrator import mapping log → writeCursor; RecordNewFormatWrite → handoff), around line 1004` — Item (2) says to fix every real gap with a test. The legacy-import mapping→cursor and new-format-write→handoff barrier gaps were found and classified as REAL, but were not fixed and are recorded only in the implementer's open_issues and needs_owner. Nothing in the code or in plans/CARRIED-DEFECTS.tsv will stop LegacyImportGate from being opened with the gap still present.
  - Evidence: The implementer's report says: "this is a REAL GAP but it is UNREACHABLE ... Not fixed". config.LegacyImportGate ships closed (config/migration.go:93-102), and RecordNewFormatWrite has no production caller, which I confirmed. git diff shows no comment added at the gap and no carried-defect row.
  - Fix: Either fix it now (AppendLinesDurable for the mapping line before writeCursor, and for the new-format write before the handoff, plus a publication pass for imported objects, with a counting test), or record it durably: add a carried-defect row owned by SP-20 M1-04 and a comment at the LegacyImportGate definition (and at the mapping append) saying the barrier must be added before the gate passes.
- **nit** `internal/checkpoint/finalize.go:288` — sealSegmentMarks skips the segment-log Sync entirely whenever any Get or per-id MarkEncoded returned an unexpected error. One bad id therefore leaves every other already-marked segment unsynced, although the sync would still make them durable.
  - Evidence: `if s, ok := src.Segments.(store.SegmentSync); ok && failed == nil { failed = s.Sync(ctx) }`
  - Fix: Always call Sync when the log offers it, and join its error with the earlier failure for the Loud line and counter: `if s, ok := ...; ok { note(s.Sync(ctx)) }`.
- **nit** `commits 8428cbb, 61c6c7f, cf9537c, e6bc7a8, 46d6529, 9086913` — These commits change product docs, code comments or tests but have no Refs footer. Every fix commit on the branch carries `Refs: V6-VERIFY, C1.6/D24`.
  - Evidence: `git log --format='%(trailers)' f6095e2..HEAD` is empty for these commits and for the evidence-only docs(closeout) commits. There are no attribution trailers, which is correct.
  - Fix: On the next history rewrite or integration, add `Refs: V6-VERIFY, C1.6/D24` to the non-evidence commits (at least 8428cbb, which changes docs/architecture.md and docs/security.md).

## Fix seat (review resolution) — status `done`, head `cd17e11`

### Summary

w6-ckptsync FIX seat. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync, branch closeout/w6-ckptsync, based on the implementer's head b51664b. I checked all five review findings independently. All five are correct. Findings 1, 2, 3 and 5 got a failing test first, and each RED run is recorded. Finding 4 was a docs error, fixed in all three places. Every test is green at the head on Windows and on Linux (non-root, -race).

## Review resolution
**F1 (minor, CONFIRMED -> fixed dd9444f): a directory barrier did not fail closed across a retry.**
- Root cause: AppendLinesDurable, Barriers.MkdirAll and EnsureLayout decided whether to sync a directory from an Lstat taken before the write ("it did not exist, so I created it"). A call whose parent sync failed left the name on disk, so the next call skipped the sync and reported success.
- The reviewer's pin scenario is worse than described: `qompack pin` is one process per pin (commands/cmd_pin.go -> pins.OpenWith in the CLI process). So the realistic retry happens in a new process, and in-process retry tracking alone would not fix it.
- Fix: a process-wide entry ledger, new file internal/paths/entries.go. For each name it records unknown, pending (this process created it and no sync of its parent has succeeded since) or durable (a parent sync decided on after the name existed succeeded). A per-name generation counter stops a stale sync from crediting a re-created name.
- AppendLinesDurable now syncs the log's directory on the first durable append to that path in each process, whoever created the file (the reviewer's option a). It also syncs pending ancestors.
- MkdirAll syncs every level that is missing, or pending in the ledger.
- EnsureLayout writes .qompack/.gitignore only after its syncs succeed, and uses it as a durability marker. A call that finds no .gitignore syncs every layout entry this process has not made durable. That covers a retry after a failed start in any process, because an EnsureLayout failure ends the daemon's start. It also closes a gap nobody had flagged: a .qompack a hook's spool created with a plain mkdir never had its entry in the project root synced.
- AppendManifest credits an existing manifest's name to its step-1 sync, so a seal's barrier count is unchanged. TestFinalizeSealsThroughItsBarriersInOrder passes unmodified.
- New tests: TestAppendLinesDurable_RetriesADirectoryBarrierThatFailed, TestAppendLinesDurable_SyncsTheNameOfAFileItDidNotCreate, TestBarriersMkdirAll_RetriesAParentSyncThatFailed (two rows, failing the first or the second sync), TestEnsureLayout_RetriesABarrierThatFailed, TestEnsureLayout_SyncsALayoutAnotherWriterStarted, and TestPinsAPinIsKeptWhenAnEarlierWriterLeftTheLogsNameUnsynced (first pin's pins/ sync failed; log created by another process). The pins test adds a power-loss model: before the fix the pin reported kept was lost.

**F2 (minor, CONFIRMED -> fixed da8512b): Finalize unsealed a seal that readers could already see.**
- Reproduced exactly as described. All three rows of the new test failed on seq_reference_drift = 1: the next PreCompact found seq N taken, sealed the same draft again at N+1, and re-pointed its segments.
- The in-process retry is real: finalizeIfDue retries each cadence tick, and a later PreCompact retries too.
- Fix: paths.ErrLineNotDurable wraps any failure after an append's write (file sync, close, or name sync). Finalize, on that error:
  - sets committed = true;
  - counts checkpoint.seal_not_durable and logs Loud ("after a power cut run qompack fsck");
  - runs the same afterSeal bookkeeping as success (pins view, retire the draft, open the successor);
  - still returns an error, so no durability promise is made.
- This is safe because barriers 1 and 3 had already made the artifact's bytes and name durable. A power cut that takes the line leaves an orphan artifact, which fsck re-indexes.
- New tests: TestFinalizeABarrierFailingAfterTheLineDoesNotSealTwice (three rows: manifest sync fails with a prior seal; new manifest's sync fails; new manifest's name fails, all followed by an in-process retry) and TestAppendManifest_SaysWhenTheLineIsWrittenButNotDurable, TestAppendLinesDurable_SaysWhenTheLineIsWrittenButNotDurable (the paths-level pins of the sentinel). The AppendManifest test also pins that a failed step 3 is retried through the next append's step 1.

**F3 (minor, CONFIRMED -> fixed dd9444f): MkdirAll raced with concurrent callers.** The ledger fixes it without a global lock held across a sync. The creating writer marks the name pending before the mkdir, so a concurrent writer that sees the directory finds it pending and syncs the entry itself. New test TestCaptureSidecar_AWriterSyncsAShardAnotherWriterIsStillSyncing: writer A is held inside its captures/ sync, writer B writes into the same fresh shard and must sync captures/ before its sidecar exists. RED before the fix; green with -race.

**F4 (minor, CONFIRMED -> fixed b5561e1): the D24 residual-risk statement left out backup certification.** Verified that TakeBackup's last steps are the manifest's WriteAtomic rename (after the staging flush) and os.Remove of the pending marker, and on Windows no flush follows either. Verified that readValidatedManifest refuses both a pending marker and an absent manifest. The same statement now stands in paths.SyncDir's comment, docs/architecture.md and docs/security.md: certification can revert, verification then refuses the backup, and it must be retaken. architecture.md's barrier list now also states the F1 and F2 rules.

**F5 (minor, CONFIRMED -> recorded d4f5a37, not fixed).**
- Why not fixed: a real fix needs per-batch durable mapping lines before each writeCursor, a publication pass for imported objects, and a durable new-format line before the handoff. That is SP-20 M1-04 design work on a feature that is gated off and has no production caller.
- Why not a CARRIED-DEFECTS row: the guard requires the owner to be a checkpoint with a plan document, and TestCarriedDefects_WaveReportRequiresResolution fails any unresolved V6-VERIFY row because plans/V6-report.md exists.
- What was done instead: comments at both appends in store/migrate.go and at config.LegacyImportGate, plus TestLegacyImportGate_StaysClosedUntilTheImportIsDurable, which fails with a message naming the gap if the gate opens first.

b08b47f corrects MkdirAll's comment, which wrongly claimed every on-demand directory creator goes through it (pins.OpenWith and the object store's shard directories use a plain os.MkdirAll), and reflows three comment lines. Comments only.

## Criterion changes (none weaken a check)
1. finalize_durability_test.go's sealModel now snapshots the draft file at a cut and restores it on rebuild. Why: the cut rows at barriers 4 and 5 failed after the F2 fix. The model assumed Finalize does nothing after a failed barrier, but the new branch retires the draft and persists the successor through the real WriteAtomic, and a process cut at the barrier would not. No assertion changed; each row still requires either absent-with-draft-at-seq-then-reseal or sealed-with-marks.
2. sealModel gains an additive failAt mode: one barrier fails, the rest run.
3. Contract wording: AppendLinesDurable's directory sync changes from "when this call created the file" to "unless this process has made the name durable". EnsureLayout's .gitignore is now written after its syncs and serves as the marker. Every existing test passes unmodified.

## Cost of barriers added by this seat (counting seam, POSIX; Windows directory syncs are no-ops)
- `qompack pin` / unpin: one file sync becomes one file sync plus one directory sync per command (each command is a process).
- Daemon: at most one extra directory sync per durable log per lifetime. The seal pays zero extra (count pinned unchanged). A backup's retention batch pays at most one per backup process.
- MkdirAll: zero in steady state; one per writer racing a shard's creation.
- EnsureLayout: zero in steady state; syncs root, .qompack and eval once for an unmarked layout.
- I ran no new Linux paired timing because nothing on a timed path changed its barrier count. The earlier seat's BenchmarkFinalize ABBA still applies.

## Commands and results (Windows 11, go1.26.6, co-loaded; no wall-clock failures seen)
- RED before the fixes (logs in plans/sdd/V6-closeout/w6-ckptsync/runs/red/red-review-*.txt): the focused runs listed under tests, all FAIL for the targeted defect.
- Full: go test ./internal/{paths,pins,config,checkpoint,store} -count=1 -timeout=70m -v at d4f5a37, all ok (store 860 pass / 1 skip, checkpoint 285 / 2, paths 153 / 7; same platform skips as the earlier seat's runs).
- Focus: cli '^TestFsck_' 70 PASS; commands+mcp '^(TestPin_|TestRecordEliminated)' 22 PASS; the earlier seat's e2e rows (6) and fault/integration rows (10) PASS at b08b47f.
- Linux: sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-ckptsync --repo C:/... --out C:/.../runs/linux d4f5a37 review-fix-touched --timeout 70m -- paths pins config negknow checkpoint store daemon cli. Non-root, -race: all eight PASS (daemon 1519 pass).
- Also: go vet on the touched packages (Windows and GOOS=linux) ok; devtool fmt-check ok; devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns PASS (runpatterns and docmarkers re-run PASS after the evidence commit); go test ./test/docs ok; devtool gen-config-docs --check up to date.
- No gc.json or GC files were touched (w6-gcserial coordination).

### Commits

- dd9444f fix(paths): never take an unsynced directory entry as durable
- da8512b fix(checkpoint): keep a visible seal sealed when its barrier fails
- b5561e1 docs(durability): name backup certification among what can revert
- d4f5a37 test(store): hold the legacy gate closed until the import is durable
- b08b47f docs(paths): state MkdirAll's cross-process residual accurately
- cd17e11 docs(closeout): record the w6-ckptsync review-fix runs

### Tests

- `go test ./internal/paths -run '^(TestAppendLinesDurable_RetriesADirectoryBarrierThatFailed|TestAppendLinesDurable_SyncsTheNameOfAFileItDidNotCreate|TestBarriersMkdirAll_RetriesAParentSyncThatFailed|TestEnsureLayout_RetriesABarrierThatFailed|TestEnsureLayout_SyncsALayoutAnotherWriterStarted)$' -count=1 -v` — RED at b51664b plus the new tests (all 5 FAIL, runs/red/red-review-f1-paths.txt); PASS after dd9444f <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/pins -run '^TestPinsAPinIsKeptWhenAnEarlierWriterLeftTheLogsNameUnsynced$' -count=1 -v` — RED (both rows FAIL, red-review-f1-pins.txt); PASS after dd9444f
- `go test ./internal/store -run '^TestCaptureSidecar_AWriterSyncsAShardAnotherWriterIsStillSyncing$' -count=1 -v` — RED (FAIL, red-review-f3-sidecar.txt); PASS after dd9444f, also with -race
- `go test ./internal/checkpoint -run '^TestFinalizeABarrierFailingAfterTheLineDoesNotSealTwice$' -count=1 -v` — RED (3 rows FAIL on seq_reference_drift=1, red-review-f2-finalize.txt); PASS after da8512b
- `go test ./internal/checkpoint -run '^(TestFinalizeABarrierFailingAfterTheLineDoesNotSealTwice|TestFinalizeSealsThroughItsBarriersInOrder|TestFinalizeCutAtEveryBarrierLeavesTheCheckpointAbsentOrSealed|TestFinalizeSealsWhenItsSegmentMarksCannotBeSynced)$' -count=1 -v` — PASS (cut rows 4 and 5 failed until the sealModel draft-snapshot correction; criterion change 1) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/paths -run '^(TestAppendManifest_SaysWhenTheLineIsWrittenButNotDurable|TestAppendLinesDurable_SaysWhenTheLineIsWrittenButNotDurable)$' -count=1 -v` — PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/store -run '^TestLegacyImportGate_StaysClosedUntilTheImportIsDurable$' -count=1` — PASS
- `go test ./internal/{paths,pins,config,checkpoint,store} -count=1 -timeout=70m -v (each package, sequentially, at d4f5a37)` — all ok: paths 153 pass/7 skip, pins 44, config 84, checkpoint 285/2 skip, store 860/1 skip (runs/win/full-*-d4f5a37.txt)
- `go test ./internal/cli -run '^TestFsck_' -count=1 -v` — ok, 70 PASS
- `go test ./internal/commands ./internal/mcp -run '^(TestPin_|TestRecordEliminated)' -count=1 -v` — ok, 22 PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact|TestE2E_SessionStartCompactRestoresCheckpointItems|TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_EliminationThroughEveryFourSurfaces|TestV5_PreCompactToRehydrateToDroppedRoundTrip)$' -count=1 -timeout=40m -v` — ok, 6 PASS (b08b47f) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/fault ./test/integration -run '^(TestFault_CheckpointDropsAnUnresolvablePointer|TestFault_CheckpointResolvabilityIsBlindToADeletedObject|TestFault_Lifecycle|TestFault_PublicationBoundaries|TestV4_GrowthGuardrailWithCheckpointsAndEphemerals|TestV5_GrammarAndPromotionCoexistInFinalize|TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly|TestV6Backup_CorruptedAndUnknownSchemaRefusedWithoutOverwriting|TestV6Backup_RefusesLiveWriterAndTargetConflict|TestV6Backup_RoundTripPreservesSourceLaterWritesAndBackup)$' -count=1 -timeout=60m -v` — ok, 10 PASS (b08b47f) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-ckptsync --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-ckptsync/plans/sdd/V6-closeout/w6-ckptsync/runs/linux d4f5a37 review-fix-touched --timeout 70m -- ./internal/paths ./internal/pins ./internal/config ./internal/negknow ./internal/checkpoint ./internal/store ./internal/daemon ./internal/cli` — exit 0, non-root -race: all 8 PASS (paths 209, pins 62, config 311, negknow 400, checkpoint 436, store 861, daemon 1519, cli 419)
- `go vet (Windows and GOOS=linux) ./internal/paths ./internal/checkpoint ./internal/pins ./internal/store ./internal/config; go run ./tools/devtool fmt-check; go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns; go test ./test/docs; go run ./tools/devtool gen-config-docs --check` — all clean/PASS; runpatterns and docmarkers re-run PASS after the evidence commit

### Criterion changes

- finalize_durability_test.go sealModel now snapshots the draft file at a cut and restores it on rebuild. The model assumed Finalize does nothing after a failed barrier. The ErrLineNotDurable branch (F2 fix) retires the draft and persists the successor through the real WriteAtomic afterwards, which a process cut at that barrier would not do. No assertion changed.
- sealModel gains an additive failAt mode: one barrier returns an error, later barriers run.
- AppendLinesDurable's directory-sync rule changes from 'when this call created the file' to 'unless this process has already made the file's name durable' (per-process entry ledger). Existing barrier-order tests pass unmodified.
- EnsureLayout now writes .qompack/.gitignore only after its parent syncs succeed, and treats a missing .gitignore as 'layout entries not known durable'. Existing layout tests pass unmodified.

### Open issues

- pins.OpenWith creates pins/ with a plain os.MkdirAll, and a hook's spool can create .qompack/ with a plain mkdir. A `qompack pin` run before any daemon has run EnsureLayout syncs pins/ (the log's name) but not pins/'s entry in .qompack or .qompack's entry in the project root. The exposure lasts until the file system journal commits (seconds) or the first EnsureLayout runs, which now syncs an unmarked layout. Not fixed: switching OpenWith to Barriers.MkdirAll covers only pins/, and .qompack's own entry would still need its own barrier. Candidate for the SP-17/next-wave owner.
- Cross-process MkdirAll residual, documented in its comment: a directory another process created is taken as durable, whether that process's barrier failed or it used a plain mkdir, until the journal commits it. The object store's shard directories are covered separately by the publication pass (w5-dirsync), not this ledger.
- fsck orphan repair: if AppendManifest returns ErrLineNotDurable, fsck reports the repair failed although the line is visible. A rerun sees it indexed. Cosmetic, left as is.
- The legacy-import barrier gaps (F5) remain unfixed behind the closed LegacyImportGate. The tripwire TestLegacyImportGate_StaysClosedUntilTheImportIsDurable fails if the gate opens first.
- Linux paired timing was not re-run for this seat's changes: no timed path changed its barrier count (the seal count is pinned unchanged). The earlier seat's Linux Finalize timing on a quiet host is still pending, as it reported.

### Needs the owner

- No new budget, bound or numeric constant was introduced by the implementer or by this fix seat; nothing needs numeric approval.
- (carried from implementer) Confirm reversing the earlier seat's 'elimination log is unsynced by design' for record_eliminated (2df3776). The MCP answer is documented as durable, but no spool line backs it. Cost: one evidence publication pass (3 file + 4 dir syncs for a one-chunk reason) plus one log file sync per call. That is about 3.6x on co-loaded Linux (56 to 203 ms absolute there) and about 70 ms/op in a Windows smoke run, against B-F's 250 ms p95. The alternative is to sync only the log line and accept that evidence can be lost.
- (carried) Accept the externalized-payload cost inside the B-B histogram (1a4311f). Requests whose line is at least 1 MiB now pay a payload fsync plus a spool/ directory fsync before the WAL line: 2.32x on co-loaded Linux (26 to 59 ms absolute there). The alternative is to have the hook fsync the blob before sending: the same user-visible latency, but outside B-B.
- (carried) Checkpoint seal cost for the C5.2 quiet run: steady state goes from 1 to 3 file syncs + 1 directory sync (Windows: 1 to 3 FlushFileBuffers). The Windows paired timing showed no detectable effect (ratio 0.927, p=0.69); Linux timing is pending a quiet host. This fix seat leaves the seal's barrier count unchanged.
- (carried, updated) Route the legacy-import mapping-to-cursor and new-format-to-handoff barrier gaps, plus a publication pass for imported objects, to the SP-20 M1-04 owner before LegacyImportGate opens. The gap is now named at both appends and at the gate, and TestLegacyImportGate_StaysClosedUntilTheImportIsDurable enforces it.
- (new) Accept the cost of review finding 1's cross-process fix: on POSIX, the first durable append to a log in each process syncs its directory. Each `qompack pin` / unpin command (one process) therefore pays one directory fsync on top of its file sync. The daemon pays at most one per durable log per lifetime, a backup at most one per process, and a seal nothing. The alternative is in-process retry tracking only, which leaves the reviewer's own pin-retry scenario broken.
- (new) D24 wording: docs now state that on Windows a backup reported certified can revert to uncertified or incomplete after a power cut. Verification refuses it and it must be retaken. The review offered the alternative of a final file flush after the certification. Confirm the documented residual is acceptable under D24.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/paths/layout.go:95-130 (Barriers.EnsureLayout marker logic) and its doc comment at layout.go:77-84; docs/architecture.md "Backup and restore" bullet (EnsureLayout sentence)` — The F1 fix for EnsureLayout covers a cross-process retry only when .qompack/.gitignore is absent. The marker is never cleared when a later call creates a missing layout directory. The function's own comment names that case: "once more when a newer build adds a directory", or an operator removed a layout directory. If that call's parent sync fails, the marker from the earlier complete layout is still there. The retry is always a new process, because an EnsureLayout error ends daemon start, cmd_mcp and selftest. That process finds the directory on disk, its ledger state is unknown and the layout is marked, so it syncs nothing and returns nil. This is F1's defect (a name never made durable, reported durable after a failed barrier and a retry) in a narrower case. Both the comment ("the call after one whose sync failed — in this process or in the next one") and architecture.md ("the next call over a layout whose syncs failed ... syncs its entries again") claim the case is covered.
  - Evidence: layout.go: `unmarked := gerr != nil`. The switch appends an existing directory only on `st == entryPending, unmarked && st != entryDurable`. In a fresh process the ledger is empty (st == entryUnknown) and the marker exists, so neither case holds and names stays empty. The .gitignore is written only `if unmarked`, and nothing removes it when a directory is found missing. Trace for a marked layout with l.Backup absent: process 1 calls creating(Backup), then MkdirAll, then syncEntries fails on syncDir(.qompack), so the daemon start fails. Process 2 finds Backup present, unknown and marked, skips the sync and returns nil. TestEnsureLayout_RetriesABarrierThatFailed checks only the in-process retry (ledger pending). TestEnsureLayout_SyncsALayoutAnotherWriterStarted checks only the unmarked tree. No test covers a marked layout that gains a directory.
  - Fix: Unmark the layout before creating any directory in it. When the entry scan finds a layout directory missing and the marker is present, remove .qompack/.gitignore before the MkdirAll calls, then rewrite it (as now) only after syncEntries succeeds. A failed call then leaves the layout unmarked, and the next process syncs every entry. Alternatively, drop the marker and sync the few layout parents (root, .qompack, eval) on every EnsureLayout, which runs once per process start. Add a test: mark a layout, remove one layout directory, fail its parent sync, reset the ledger or model a fresh process (for example a test-only ledger reset), and require the next EnsureLayout to sync the directory's parent. Keep the comment and architecture.md in line with whichever rule ships.
- **nit** `internal/store/lifecycle.go:251 (AppendRetentionRoot doc comment)` — The comment still gives the old rule this seat replaced: the state directory is synced "when the append created the file". Under dd9444f, AppendLinesDurable syncs the directory unless this process has already made the name durable. That includes a file another process created, or one whose earlier directory sync failed.
  - Evidence: lifecycle.go:251: "so the line is synced — and the state directory too when the append created the file — before this returns (paths.AppendLinesDurable)". Compare barriers.go's new step 2: "syncs the file's directory unless this process has already made the file's NAME durable".
  - Fix: Reword to: "and the state directory too unless this process has already made the file's name durable". Or just point to paths.AppendLinesDurable for the rule.


# V6 close-out w5-dirsync: one directory fsync per publication pass (D20)

Branch `closeout/w5-dirsync`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `f44f34854937143f4c49d16fcc268414461864b2`

### Root cause

SP08-D1, the pure-redundancy half. syncPublicationObject (internal/store/publication_sync.go on 90e1db3) walked up from each object file after fsyncing it: `for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) { paths.SyncDir(dir); if dir == s.l.Objects { return nil } }`. That fsynced the object's fanout leaf, its first-level fanout directory and the objects/ root once for every object. A 34-object 256 KB leased root therefore cost 103 directory fsyncs per pass: 3x34, plus index/. objects/ alone was fsynced 34 times per pass, and there are 2 passes per capture, so 206 directory fsyncs per capture. A directory fsync makes durable every entry that exists in that directory when it runs. Every entry the pass proves already exists before the pass starts: the put created the rename and the mkdirs, and the pass found each object when it verified it. No caller takes a dependent step until SyncPublication returns. So every repeat after the first fsync of a directory inside one pass made nothing more durable. The same pattern appeared twice in the daemon: appendGeneration and appendTransition fsynced their directory a second time, directly after writeHead's writeConfinedAtomic had fsynced that same directory following the head's rename, with nothing created in between. Evidence: the counter RED on an archive of 2ff7ccb (store fixture: 262 directory fsyncs per pass against 163 expected; observer: 103 per pass against a bound of 70), the daemon RED (two fsyncs per step), and strace (Deduped leased capture: 281 fsyncs before, 213 after).

### Summary

W5-DIRSYNC (C2.3 / SP08-D1 / D20): each directory is now fsynced once per publication pass. Branch closeout/w5-dirsync at f44f348 (code head b1935ee), base 90e1db3. Working tree clean.

Result: the full post-write re-proof is kept (D20). Only same-pass repeats are gone.
- Directory fsyncs per leased 256 KB capture, from the counter:
  - Deduped: 206 -> 138
  - Delta: 206 -> 136
  - AllNovel: 452 -> 274
- Total fsyncs per capture on Linux, from strace:
  - Deduped: 281 -> 213
  - Delta: 286.6 -> 215.8
  - AllNovel: 601.8 -> 438.2
- On Windows nothing moves, because paths.SyncDir is a no-op there (item 4).
- Every crash sweep and every publication, fault and integration row is green and unchanged. The two Linux reds are pre-existing: both reproduce alone at the base, or pass there.

0. RESUME HANDLING
- The earlier seat's 3 commits were reviewed as a colleague's draft and kept: 2ff7ccb (counters), 3412869 (one fsync per directory per pass), 658a806 (presized set). They are correct, and the code matches their messages.
- Its uncommitted daemon draft was also correct. I added t.Cleanup closes so a failing assertion no longer leaks handles and breaks TempDir cleanup on Windows. I re-ran the RED myself and committed it as 6c0dfbe.
- I added b1935ee. The count test pinned HOW MANY directory fsyncs a pass makes, not WHICH directories: mutation M1 (drop one leaf, fsync objects/ twice) kept the count and passed it. b1935ee adds a nil-default seam, FSStore.pubSyncDir, next to the existing obsSyncData/obsPubFault seams. Its new test requires the exact set: each closure object's leaf and first-level fanout directory, plus objects/ and index/, each exactly once, with index/ last, on both passes.
- Pre-restart logs from a killed run were moved out of the tree to scratch/w5/dirsync/pre-restart/: the partial full store run, the observer run at 3412869, the probe and the daemon RED. Everything committed was re-run: the REDs on an archive, everything else at b1935ee.

1. EQUIVALENCE PROOF (against the w4-syncs property table)
Old pass, for each object i: verify(o_i), fsync(o_i), fsync(leaf_i), fsync(fanout_i), fsync(objects/). Then roots.jsonl, tool_use.jsonl, index/.
New pass: for each object i, verify(o_i) and fsync(o_i). Then each distinct directory once. Then roots.jsonl, tool_use.jsonl, index/.

Why the two passes are equivalent:
- A directory fsync makes durable every entry that exists in that directory when it runs.
- These entries all exist at verify(o_i), which comes before every directory fsync: o_i's entry in leaf_i, leaf_i's entry in fanout_i, and fanout_i's entry in objects/. The put, or an earlier put for a deduplicated object, created them.
- So when the call returns, the same set of entries is durable.
- The order object -> index is preserved: every object file and directory fsync completes before the first index fsync, as before.

Every guarded step runs only after SyncPublication returns. I checked every caller: publishObservation, completeIntentLocked(true/false) including recovery, and the observer's syncObservation. Against each row of the table:
- (a) The intent names a durable root: pass 1 returns before reserveIntentLocked.
- (b) The tool_use line becomes visible only over a durable root: it is written after pass 1 and the intent.
- (c) Record, marks and index/ are durable before the binding commits and before the link: pass 2 returns before the commit. Its index fsyncs are unchanged, and each object directory is fsynced once before them.
- (d) Link before the frontier ACK, (e) file version after the link, (f) no ACK without a publication: no pass is involved; unchanged.
- (g) A redelivery re-proves the original publication: still 3 full passes, each with one fsync per directory.

Differences that change no promise:
- A pass that fails partway used to leave some directories fsynced. Callers stop on any error, so nothing ever relied on that.
- A concurrent GC removal after verify goes undetected by both old and new code. The class of failure is unchanged, and the post-write re-proof that D20 keeps still re-verifies the whole closure.

Where a repeated fsync of one directory IS load-bearing, and was kept:
- Pass 1's index/ fsync must precede the intent's own index/ fsync. A roots.jsonl or tool_use.jsonl entry created in this process must be durable before the intent names the root. The two fsyncs are separate steps, not one pass.
- Radix: the packs/ fsync before the pointer, so a pack is durable before any pointer names it.
- Segment staging: the segment directory, then segments/.
- Terminal journal: its directory, then the state directory.
- The intent's data and directory fsyncs, before the record.

2. IMPLEMENTATION AND PINS
Code:
- SyncPublication collects each object's directory into publicationDirs, a presized set kept in the order first reached. Adding a directory walks its ancestors up to objects/ and stops at the first one already in the set.
- Every directory fsync goes through syncPublicationDir, which counts store.publication.sync.dir.
- Every file fsync counts store.publication.sync.file.

Pins:
- The store tests pin the exact per-pass counts (file = closure objects + 2; directory = leaves + fanouts + 2) and the exact directory set.
- The observer test runs, for each benchmark fixture, the capture that BenchmarkOnToolUse_TestOutput256KB_Leased times. It pins:
  - 2 passes per capture
  - per-capture file and directory counts at exactly twice the standalone pass, so the post-write pass is still the whole re-proof
  - per-pass directories at most 2*objects+2
- Measured at b1935ee, per pass and per capture:

| Fixture | Objects | Per pass (file + dir) | Per capture (file + dir) | Pre-fix dir per pass |
|---|---|---|---|---|
| Deduped | 34 | 36 + 69 | 72 + 138 | 103 |
| Delta | 34 | 36 + 68 | 72 + 136 | 103 |
| AllNovel, capture 0 | 75 | 77 + 137 | 154 + 274 | 226 |

- The daemon tests pin one directory fsync per segment transition and per generation commit.
- No existing test file was changed. The every-context-check sweeps still count 14 and 15 checks, as in w4.

3. OTHER WRITERS
- PutBytes: object files are not fsynced by the put at all (their durability comes from SyncPublication). There is one pending marker per put, through WriteAtomic: 1 file and 1 directory fsync. No pattern; unchanged.
- checkpoint Finalize: paths.CreateNew fsyncs the file but not its directory, and AppendManifest does not fsync. There is no directory fsync to remove. See needs_owner.
- negknow Open/append: no fsync, by design (log.go: the daemon spool is the durability boundary). Unchanged.
- Delivery segments (appendTransition) and generations (appendGeneration): a back-to-back second fsync of the same directory. Removed in 6c0dfbe; the same proof holds. The rollover is on by default (D2), so this is live code, but it only runs on a transition or a generation commit, not per capture.
- Already one fsync per directory, left alone: segment staging, radix, the terminal journal, and the drain (once per pass via dirSynced).
- docs/architecture.md, 00-ARCHITECTURE.md and the ADRs never name per-object directory fsyncs or the passes, so no docs change was needed. test/docs is ok.

4. WHAT A DIRECTORY FSYNC DOES ON WINDOWS IN THIS CODE
- Nothing. paths.SyncDir calls fsyncDir, which returns nil when runtime.GOOS == "windows", before opening anything (internal/paths/atomic.go:166-169). No handle is opened and no syscall is made. This is pinned by TestSyncDir_ReportsAMissingDirectoryOffWindows.
- Go's File.Sync on Windows is FlushFileBuffers (os.File.Sync -> poll.FD.Fsync -> syscall.Fsync).
- os.Open(dir) opens with GENERIC_READ and FILE_FLAG_BACKUP_SEMANTICS. The probe shows File.Sync on that handle fails with "Access is denied" (errno 5).
- os.OpenFile(dir, O_RDWR) fails with EISDIR: Go leaves BACKUP_SEMANTICS off for write access and maps the resulting ACCESS_DENIED to EISDIR.
- Only a raw CreateFile(GENERIC_WRITE, FILE_FLAG_BACKUP_SEMANTICS) handle accepts FlushFileBuffers.
- So without the short circuit, every SyncDir on Windows would fail.
- Object files are flushed through O_RDWR handles.
- On Windows, the durability of a rename rests on the code's stated premise that NTFS journals MoveFileEx durably. I did not test that with a power loss.
- Consequence: on Windows this change removes zero syscalls, and the directory counters there count calls, not I/O.

5. MEASUREMENTS
Counter: the table in section 2.

strace, Linux, 5 leased captures after a warm-up, base 90e1db3 vs b1935ee:

| Fixture | fsync | openat and close | fcntl |
|---|---|---|---|
| Deduped | 281 -> 213 | -68 | -272 |
| Delta | 286.6 -> 215.8 | -70.8 | -283 |
| AllNovel | 601.8 -> 438.2 | -163.6 | -654 |

The unleased control is identical on both builds.

Allocations per SyncPublication pass (87 objects; temporary diagnostic, deterministic):
- Linux: 3236 -> 2944 (-9.0%)
- Windows: 4190 -> 4182 (-0.2%)

Paired ABBA timing, 12 rounds per side, benchtime 10x. Figures are new/base ratios with an exact sign test:

| Platform | Leased row | ns/op ratio | Rounds faster | p | allocs/op ratio |
|---|---|---|---|---|---|
| Windows | Deduped | 1.013 | 4/12 | 0.39 | 1.000 |
| Windows | Delta | 0.983 | 6/12 | 1.0 | 1.002 |
| Windows | AllNovel | 1.073 | 6/12 | 1.0 | 1.000 |
| Linux | Deduped | 0.853 | 9/12 | 0.15 | 0.979 (12/12, p=0.0005) |
| Linux | Delta | 1.010 | 6/12 | 1.0 | 0.979 (12/12, p=0.0005) |
| Linux | AllNovel | 0.853 | 10/12 | 0.039 | 0.964 (12/12, p=0.0005) |

- Windows ran at CPU load 0-100%. This is the null result item 4 predicts. The unleased control moved between 0.95 and 1.16, which is the noise floor.
- Linux ran at load average 2.7-4.6. The unleased Deduped control also moved 0.851 (10/12, p=0.039), so Linux timing cannot be attributed to this change at this noise level. The fsync and allocation counts are the decisive evidence.
- A second Linux batch was skipped because another workstream's gate pushed the container load average to 22.
- Absolute times are not budget evidence. No budget constant or CARRIED-DEFECTS status was changed.

CRITERION CHANGES: none; see criterion_changes.

### Commits

- 2ff7ccb feat(store): count the barriers a publication pass issues (earlier seat; reviewed, kept)
- 3412869 fix(store): fsync each directory once per publication pass (earlier seat; reviewed, kept)
- 658a806 perf(store): size a pass's directory set up front (earlier seat; reviewed, kept)
- 6c0dfbe perf(daemon): fsync a delivery step's directory once (the earlier seat's uncommitted draft, reviewed; I added cleanup closes to its tests, re-ran the RED and committed it)
- b1935ee test(store): pin which directories a publication pass fsyncs (new: nil-default pubSyncDir seam plus an exact directory-set test)
- f44f348 docs(closeout): record the w5-dirsync evidence (runs/ and INDEX.txt; no code)

### Tests

- `go test ./internal/store -run '^(TestSyncPublication_CountsAFileBarrierPerObjectAndIndex|TestSyncPublication_SyncsEachDirectoryOncePerPass|TestSyncPublication_FsyncsEveryClosureDirectoryExactlyOnce)$' -count=1 -v, on an archive of 2ff7ccb plus HEAD's store barrier tests and the pubSyncDir seam routed through the pre-fix loop (runs/red-store-dirs-2ff7ccb.txt) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — RED as intended: the file count passes; SyncsEachDirectoryOncePerPass fails at 262, want 163; FsyncsEveryClosureDirectoryExactlyOnce fails because objects/ is fsynced twice
- `go test ./internal/observer -run '^TestPublicationPasses_Leased256KBCaptureFsyncsEachDirectoryOncePerPass$' -count=1 -v, on the same 2ff7ccb archive (runs/red-observer-dirs-2ff7ccb.txt)` — RED as intended: Deduped/Delta 103 directory fsyncs per pass (bound 70), AllNovel 226 (bound 152)
- `go test ./internal/daemon -run '^(TestDeliverySegments_ATransitionFsyncsTheStateDirectoryOnce|TestDeliveryGeneration_ACommitFsyncsItsDirectoryOnce)$' -count=1 -v, with the trailing SyncDir restored through the seam (the pre-fix shape; runs/red-daemon-dirsync.txt) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — RED as intended: each step fsyncs its directory twice (want once); the same two tests PASS on the fixed code (ok 0.438s)
- `go test ./internal/store -run '^(TestSyncPublication_FsyncsEveryClosureDirectoryExactlyOnce|TestSyncPublication_SyncsEachDirectoryOncePerPass)$' -count=1 -v, under temporary mutation M1 (drop one leaf, fsync objects/ twice; reverted) (runs/mutation-m1-store-dirset.txt) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — the count test PASSES (count preserved); the directory-set test FAILS: objects/ fsynced twice. This shows the new pin catches what the count cannot
- `go test ./internal/store -count=1 -timeout=60m -v (Windows, b1935ee; runs/win-full-store-b1935ee.txt)` — ok 466.6s, 850 PASS lines, one existing symlink-privilege skip (TestMaintenance_SymlinkComponentInBackupTreeRefused)
- `go test ./internal/observer -count=1 -timeout=75m -v (Windows, b1935ee; runs/win-full-observer-b1935ee.txt)` — ok 466.0s; includes TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun (14 checks), TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun (15 checks), and the TestRedelivery_ sweeps, all PASS
- `go test ./internal/daemon -count=1 -timeout=30m -v (Windows, b1935ee; runs/win-full-daemon-b1935ee.txt)` — ok 368.7s, 747 PASS, one existing platform skip (TestService_StateWriteFailureStillEmits)
- `go test ./test/fault -run '^(TestFault_PublicationBoundaries|TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence|TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus)$' -count=1 -v; the same for ./test/integration (6 w4 rows) and ./test/e2e (4 w4 rows) (Windows, b1935ee) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — fault ok 263s, integration ok 119s, e2e ok 86s; every row PASS
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-dirsync --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-dirsync --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-dirsync/plans/sdd/V6-closeout/w5-dirsync/runs/linux b1935ee touched-full --timeout 90m -- ./internal/store ./internal/observer ./internal/daemon` — non-root, -race: store 851 pass/4 skip; observer 561 pass; daemon 1501 pass, 1 fail (TestDeliveryOrder_LaneOverflowIsDrainedOnRequest), 1 skip
- `linux-nonroot-gate.sh ... <rev> lane-overflow-alone --run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 10 -- ./internal/daemon, at b1935ee and at 90e1db3` — 10/10 PASS on both builds: the touched-full failure was load-induced (w3-paths and w3-startroute also saw it under load); its path never reaches the changed code
- `linux-nonroot-gate.sh ... b1935ee publication-focused --run '^(TestFault_PublicationBoundaries|...the 13 w4 fault/integration/e2e rows...)$' -- ./test/fault ./test/integration ./test/e2e <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — fault 17 pass, integration 6 pass, e2e 4 pass and 1 fail: TestV3_LiveSessionWriteSetAndAppendOnly
- `linux-nonroot-gate.sh ... <rev> x09-alone --run '^TestV3_LiveSessionWriteSetAndAppendOnly$' --count 2 -- ./test/e2e, at b1935ee and at 90e1db3` — FAIL 2/2 on BOTH builds ('observer: gc' line never appears): pre-existing on the integration base, the same finding w4-syncs recorded at 6aff949; it passes on Windows
- `go test ./internal/paths -run '^(TestSyncDir_SyncsAnExistingDirectory|TestSyncDir_ReportsAMissingDirectoryOffWindows)$' -count=1 -v (Windows; runs/win-syncdir-noop.txt) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — PASS: SyncDir is a no-op on Windows (a missing directory reports nil)
- `temporary diagnostic go run . of runs/winprobe-main.go.txt (go1.26.6, Windows; runs/win-dir-fsync-probe.txt)` — File.Sync on os.Open(dir): Access is denied (errno 5); os.OpenFile(dir, O_RDWR): is a directory; CreateFile(GENERIC_WRITE|BACKUP_SEMANTICS) + FlushFileBuffers: ok
- `MSYS_NO_PATHCONV=1 docker run --rm -v <scratch>/syscount:/src golang:1.26.6-bookworm sh /src/syscount.sh (throwaway container; base 90e1db3 vs b1935ee with the w4 harness; runs/syscount/)` — fsync per leased OnToolUse: Deduped 281 -> 213, Delta 286.6 -> 215.8, AllNovel 601.8 -> 438.2; unleased identical; all 12 cases exit 0
- `temporary diagnostic TestTempDiagSyncPublicationPassAllocs (runs/pass-allocs/tempdiag_passallocs_test.go.txt, copied into scratch archives of 90e1db3 and b1935ee only; not in the repository), count 3 per side, Windows and Linux` — allocs per pass (87 objects): Linux 3236 -> 2944; Windows 4190 -> 4182; bytes per pass noisy and unchanged
- `sh runs/ab_win.sh 12 <dir> 10x; python runs/ab_pairs.py runs/win-ab` — leased ns/op ratios 1.013 / 0.983 / 1.073 (p >= 0.39), allocs 1.000; no effect, as item 4 predicts; unleased control noise floor +/-15%
- `docker exec qompack-v6-linux-verification sh /work/cx-w5-dirsync-ab/linux_ab.sh cx-w5-dirsync-ab 12 10x; python runs/ab_pairs.py runs/linux-ab` — leased allocs/op 0.979 / 0.979 / 0.964, each 12/12 rounds lower, p=0.0005; ns/op Deduped 0.853 (p=0.15), Delta 1.010 (p=1.0), AllNovel 0.853 (p=0.039); the unleased Deduped control also moved 0.851 (p=0.039), so the timing is not attributable
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (b1935ee); runpatterns and docmarkers again after f44f348` — all 8 PASS
- `go run ./tools/devtool fmt-check; go vet ./internal/store ./internal/observer ./internal/daemon; GOOS=linux go vet (same packages); go test ./test/docs -count=1` — all exit 0

### Criterion changes

- None. No existing test file, assertion, threshold, budget, golden, skip or timeout was changed; every test on the branch is in a new file (internal/store/publication_barriers_test.go, internal/observer/publication_barriers_test.go, internal/daemon/delivery_dirsync_test.go). The additions only strengthen checks: exact per-pass barrier counts, the exact directory set, a per-capture relation of exactly twice the pass (so the post-write re-proof cannot be quietly narrowed), and one directory fsync per delivery step.

### Open issues

- Linux TestV3_LiveSessionWriteSetAndAppendOnly fails 2/2 alone at b1935ee AND at the base 90e1db3 (the SessionEnd 'observer: gc' line never appears); it passes on Windows. This is pre-existing and the same finding w4-syncs recorded at 6aff949. It is not from this branch; route it to the SessionEnd/e2e owner.
- TestDeliveryOrder_LaneOverflowIsDrainedOnRequest failed once in the co-loaded Linux touched-full run and passed 10/10 alone at head and at base. This is the third workstream to see this load sensitivity (w3-paths, w3-startroute).
- Linux paired timing is inconclusive: the leased rows are 0.853/1.010/0.853, but the unleased control moved 0.851 (p=0.039) at the same time. The decisive evidence is the load-independent fsync count (-68 per Deduped capture) and allocs (-2 to -4%, 12/12). Whether B-C holds is for the coordinator's quiet C5.2 run (D20).
- On Windows the change has no I/O effect, because paths.SyncDir is a no-op there. Windows timing and allocs are unchanged, as expected. The directory counters count calls on every platform by design.
- After this change, about 213 fsyncs remain per Linux Deduped leased capture: 2 x (34 object files + 34 leaves + 33 first-level fanouts + objects/ + 2 index files + index/), plus 3 others. That is what the kept full re-proof costs (D20); the index-only post-write pass and the proven-object cache stay rejected.
- The per-capture pin in the observer is a relation (exactly 2x the pass) plus a bound (per-pass directories at most 2*objects+2). The exact directory set is pinned in the store, because the observer package cannot see a root's recovery closure.
- The strace counts ran in a throwaway `docker run --rm` of the local golang:1.26.6-bookworm image, as w4-syncs did. The verification container was not modified. My /work/cx-w5-dirsync-* directories there are kept as evidence.

### Needs the owner

- Coordinator/owner (D20): run the quiet C5.2 timing of BenchmarkOnToolUse_TestOutput256KB_Leased to decide B-C; a miss goes back to the owner for a re-budget or wontfix. No budget constant or CARRIED-DEFECTS status was changed.
- No new budget or bound numbers. Listed only for completeness: fanoutLevels = 2 in internal/store/publication_sync.go is a layout fact, not a budget. It is the number of fanout directories in objects/ab/cd/<hash> (Qompack.md 7.4) and only presizes the pass's directory set; a wrong value costs one map regrowth, never correctness.
- Checkpoint owner (an observation, not implemented, outside D20): Finalize's paths.CreateNew fsyncs the artifact but not the checkpoints/ directory, and AppendManifest does not fsync. On POSIX a power loss can therefore lose a sealed checkpoint's name or its MANIFEST line. This is a missing barrier, not a redundancy, so D20 does not cover it.
- Windows durability premise: paths.SyncDir is a deliberate no-op on Windows and relies on NTFS journaling the rename. If the owner wants a real Windows directory barrier, FlushFileBuffers on a CreateFile(GENERIC_WRITE|FILE_FLAG_BACKUP_SEMANTICS) handle works (runs/win-dir-fsync-probe.txt). That would add barriers, which is a new decision, not part of this task.
- Coordinator: route the pre-existing Linux red TestV3_LiveSessionWriteSetAndAppendOnly (fails at 90e1db3) and the load-sensitive TestDeliveryOrder_LaneOverflowIsDrainedOnRequest to their owners.

## Independent review

### review:dirsync: sound

- **nit** `internal/store/publication_sync.go:130-134 (the new `for _, dir := range dirs.order` loop)` — Before this change, SyncPublication checked the context at least once for every 3 directory fsyncs, because GetChunk (read.go:30, ctx.Err()) ran per object and each object was followed by its own 3 directory fsyncs. Now all directory fsyncs run back to back after the object loop, with no context check between them: up to 2n+1 fsyncs, which is 69 for Deduped and 137 for AllNovel. Durability is not affected. What changes is how quickly a cancelled pass stops: on a host with an fsync tail (the hosted runners' 57-74 ms p99, per the hosted-runner fsync tail memory), cancellation could take seconds instead of about 3 fsyncs.
  - Evidence: Old: `for h := range objects { GetChunk(ctx,h) /*ctx check*/; syncPublicationObject(...) /*file + leaf + fanout + objects/*/ }`. New: `for h := range objects { GetChunk; syncPublicationObject /*file only*/; dirs.add }` then `for _, dir := range dirs.order { s.syncPublicationDir(dir) }` with no ctx.Err(). The context-check sweeps still count 14/15, which confirms no check was added.
  - Fix: Optional, and only after coordinator sign-off, because it changes the enumerated check counts in TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun/StopCut (14/15): check ctx.Err() before the directory loop, or once every k directories. Otherwise, record the longer uninterruptible window in the SyncPublication doc comment as an accepted consequence.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


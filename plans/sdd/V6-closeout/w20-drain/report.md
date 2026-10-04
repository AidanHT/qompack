# Wave 20 drain (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-drain`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/daemon/drain.go:723-736 (consume: notePassConsumed moved to the in-order branch), with drain.go:958 and :1032 (reattempt() after every done line) and internal/daemon/lock.go:439-445 (Lock.owned reads the lock file on every journal query)`: Since C1.13, a budgeted pass that only re-consumes lines is no longer bounded by its budget. Two older costs used to be capped at about 2 s for watcher, requested and idle passes, and are now uncapped. First, drainFile calls reattempt() after every consumed line, and reattempt calls processOne on every deferred line (up to 1024). Second, each processOne makes three journal queries (terminalDenied, acknowledged, predecessorsAcknowledged), each of which takes j.owner.mu and reads the lock file through owned(). So one file (client spool or WAL segment) holding a head that can never publish, D deferred lines of that head's session and L lines an earlier pass already published costs O(L x D) of these calls per pass. The pass holds dr.mu the whole time. The PreCompact settle waits up to its 500 ms bound, session-end and operator drains wait without a bound, and the idle controller is blocked. This disproves the doc comment at drain.go:66-71 ("runs past its budget by that bookkeeping and at most one line's drainLineDeadline"). requestedDrainPass and the idle drain are full Drain passes (WAL included), so a WAL segment with a lost arrival triggers this.
- **minor** `internal/daemon/drain.go:58-74 (withPassBudget doc), drain.go:720-747 (consume), drain.go:923-982 (per-line admit/lease before processOne)`: Measured performance trade-off. A budgeted pass that has spent its budget but made no progress now reads every blocked spool to EOF. Its wall time, and so the time it holds dr.mu, is no longer bounded by the budget plus one line. It grows by one durableEnd fsync and one drain.json save for each blocked client spool, plus about 2.6-4 ms for each re-consumed line. Most of that per-line cost is re-admission (admitDelivery's scopeRefusal runs again on every pass). Candidate 7 stopped such a pass at its first re-consumed line once the budget was spent. This is the trade D58(c)/D60(a) chose, made to end starvation. The withPassBudget doc calls the cost 'bookkeeping' but never says it is O(spools x fsync + lines). The longer hold makes the documented D56(e) limit more likely: a PreCompact settle waits behind a running pass. In practice it stays bounded, because client spools are one hook process each and hold one to a few lines. No hard bound exists.
- **minor** `internal/daemon/drain.go:843-845 and 848-850 (reattempt's passStopped checks)`: No committed row pins D31's 'at most one line' through the look-ahead. If both passStopped checks in reattempt are removed, a pass whose budget is spent goes on after its progress line and publishes every deferred line the progress unblocked. No row fails. The product code is correct today. The invariant the dimension asks about ('can a spent pass start a new line by any path') is enforced there, but nothing pins it.
- **minor** `internal/daemon/drain.go:763-772 and 784-786 (processOne absorb/retired branches append to fs.PendingBlobs) with drain.go:1295-1324 (cleanupAcknowledged keeps referenced blobs)`: Pre-existing, the same in c7, and not a c8 regression. Every pass re-consumes a blob-carrying line behind a blocked head and appends that line's blob name to fs.PendingBlobs again. cleanupAcknowledged keeps every copy, because the line is past fs.Offset and so still counts as referenced. drain.json therefore grows by one entry per such line on every pass (watcher, idle tick every <=30 s while idle, requested drains) until the head unblocks. While any intent stays pending, cleanupAcknowledged re-scans every spool file after each file's EOF. c8 adds a little: a spent pass on a slow host now reads these lines too, where c7 stopped. No capture is lost or duplicated; duplicate removals are harmless (IsNotExist -> nil).
- **minor** `internal/daemon/drain.go:769-772 (acknowledged branch; also :764, :785, :818, :827, :949) and drain.go:1310-1322 (cleanupAcknowledged keeps every referenced entry)`: Pre-existing (the lines are unchanged since c7), but C1.13 now makes budgeted passes reach these lines on every pass. Behind a blocked head, each pass re-consumes an acknowledged blob-bearing line. It re-reads the whole blob through readBlob (io.ReadAll of the blob) only to learn the blob's name, and appends that name to fs.PendingBlobs again. The spool still references the blob, so cleanupAcknowledged keeps every entry, duplicates included. state/drain.json grows by one entry per pass and is rewritten with WriteAtomic each pass.
- **minor** `internal/daemon/spool_watch_test.go:247-250`: TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff is the row that covers c7's hosted Windows failure condition (a first look longer than the horizon). It never asserts that its stall fired: `syncs` is declared inside the if block and never read. If a later product change stopped syncing the client spool on the first pass, or synced through another path, the row would quietly become a copy of the plain backoff row and stop covering the c7 condition. The w17c review raised this as a nit, and it is still open on INT.
- **minor** `internal/daemon/spool_watch_test.go:245-252 (spoolWatchBackoffRow stall seam); internal/daemon/spool_watch_test.go:438-440; internal/daemon/spool_watch.go:37-47`: w17c review and observation items were left without a disposition. (1) The stall row TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff never asserts that its stall fired: `syncs` is scoped inside the if-block and never read. If a product change stopped syncing the spool on its first pass, the row would silently become the plain row and keep passing. (2) Both reports carried an observation: DrainClientSpools passes every client spool, so a backed-off blocked spool is still re-read and re-synced whenever another spool is due. Yet TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached's doc calls this 'the fsync pressure the back-off exists to spare'. (3) Optional doc follow-up: spool_watch.go does not say that a scheduler.idle.detectAfterSeconds below 2 x spoolCheckInterval (4 s) means no retry at all.
- **minor** `docs/architecture.md:177-179; qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md D31 row (line 50)`: Both texts claim more than the code does. The architecture doc says 'once it is spent a pass starts no new line', and the ledger's D31 row says 'no new line after the budget is spent'. Since D31's own refinement, and more strongly in c8, a spent budget ends a pass only once the pass has made progress: it advanced a spool's consumed front, or it published or retired a line. A spent pass with no progress keeps starting lines, and publishes later spools, which is exactly what C1.13 relies on. The w18 implementer flagged architecture.md:176 and left it. D60(a) states the new rule but the D31 row was not amended.
- **minor** `docs/architecture.md:176-179`: The doc says the watcher, idle and requested passes 'share a soft 2 s pass budget ...: once it is spent a pass starts no new line'. That is not what the code does. Under D31 a pass that has consumed nothing is never cut, and since C1.13 a pass whose only consumption is re-consuming lines behind a blocked head is not cut either. Such a pass keeps starting lines, syncing and reading spools after its budget (measured 48x over in the finding above). The claim of a bounded drain pass goes beyond the evidence.
- nit `internal/daemon/drain.go:860 (S3 `if changed` in reattempt)`: No row pins S3's condition, which keeps a look-ahead absorption (changed=false) from counting as progress. The fix seat accepted this as a one-time over-count only. Confirmed independently.
- nit `internal/daemon/drain.go:883-1037 (no reattempt after readLoop ends)`: Pre-existing. reattempt runs only after a done or denied line (drain.go:958, 1032). A deferred head can become consumable during the pass after the spool's last done line, for example because a live worker acknowledged its predecessor. That head is not re-tried at EOF. The watcher then judges the spool unconsumable and backs it off 2^n intervals. Only latency: a requested or idle drain, or a later look, publishes it. Nothing is lost.
- nit `internal/daemon/spool_watch_test.go:56 (spoolWatchTraffic, used by spoolWatchBackoffRow for both C7.2 rows)`: Pre-existing. require.True(t, dd.dispatchOp(...).OK) runs on the traffic goroutine, not the test goroutine. A failing dispatch would call t.FailNow off the test goroutine, which the testing package forbids. deferred close(done) still runs, so there is no hang, but the failure surfaces late and without context.
- nit `internal/daemon/drain.go:908-916 (corrupt line) and 963-974 (never-leased unadmitted line)`: Pre-existing. A corrupt line behind a blocked head is counted in drain_file_error and logged at Warn again on every pass. A never-leased unadmitted line there is logged LOUD again ('capture not admitted; record skipped') on every pass. c8 lets a spent pass on a slow host reach more of these lines than c7 did.
- nit `internal/daemon/drain_pass_progress_test.go, spool_watch_pass_progress_test.go, spool_watch_test.go:TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff`: CI time: the rows added since c7 are bound by sleeps (slowSpoolSyncs holds each sync for idleRunBudget+spoolWatchTick) and run serially, following the package's convention for laneTestDaemon. They add about 36 s to each internal/daemon pass, so about 71 s to the Windows -count=2 leg. That leg was once killed at 1800 s (its limit is now 60m).

## impl:drain: status `done`, head `a03e6375235e8dcff2858de24b1c101176b9e806`

### Root cause

C1.13 counts only progress against a pass budget, so a budgeted pass that makes none reads every spool to its end. Behind a head waiting on an earlier arrival, every line an earlier pass consumed out of order was consumed again in full on every pass: admitted, lease looked up, three journal queries, blob read for its name, intent appended, counted and logged. The reason is that `processed` lived only for one drainFile call. reattempt() also re-ran every deferred line after every consumed line, whatever its session; this O(L x D) cost already existed before c8. Every blocked spool also paid an fsync and a drain.json rewrite on every pass.

### Summary

PRODUCT CODE CHANGED (internal/daemon/drain.go, internal/daemon/delivery_order.go; doc-only change in internal/daemon/spool_watch.go). Branch closeout/w20-drain, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-drain, 4 commits on 738d67c7. The tree is clean and nothing is pushed.

WHAT CHANGED (drain.go)
1. A per-drainer in-memory memo (spoolMemo) for each spool file that still has bytes past its consumed front. It holds the lines a pass consumed out of order: start offset, end offset, an FNV-1a sum of the bytes, and the gaps each left. It also records whether this drainer synced the file.
   - A memo is used only while the file has the same identity (os.SameFile), size and mtime.
   - A line is skipped only when the bytes read at its offset end at the same place and have the same sum.
   - Every memo is forgotten if drain.json holds anything other than this drainer's own last write (stateIsOwn), or if a save fails.
   - A memo is dropped when its file is removed, unlisted or changed.
   - Effect: a consumed line costs its read and an FNV-1a sum. There is no admission, lease lookup, journal query, blob read, intent append, counter or log, and no re-attempt. Its gaps are re-added, so DrainGaps reads the same on every pass.
2. durableEnd skips the sync, and the directory sync, for a file this drainer synced that is unchanged since.
3. saveState writes nothing when the state marshals to the bytes this drainer last wrote.
4. reattempt(sess) after a non-memo consumed line re-runs only that session's deferred lines. The ordering gate only waits on the same session, so this is exact; it removes the pre-existing L x D re-attempt cost. Every deferred line is also re-attempted once at end of file (fixes the EOF nit).
5. notePendingBlob keeps cleanup intents unique. pendingBlobOf names the blob from its descriptor plus an Lstat, which is readBlob's acceptance test without reading the body. It is used at the retired, acknowledged, Seen-completed and denied sites.
6. withPassBudget, passStopped, notePassConsumed and related docs now say exactly what bounds a pass. After progress: at most one line, under its drainLineDeadline. Before progress: the spool, not the clock.

MEASURED, same Windows host and probe (real dispatch, the audit's shape)
| Shape | 738d67c7 | fix | c7 d20309c0, today |
|---|---|---|---|
| D=300/L=600 first pass, journal queries | 546,004 | 5,107 | — |
| D=300/L=600 first pass, time | 6m48s | 2m38s | — |
| D=300/L=600 spent pass, admissions | 901 | 301 | 302 |
| D=300/L=600 spent pass, journal queries | 544,804 | 2,107 | 1,207 |
| D=300/L=600 spent pass, syncs | 1 | 0 | 1 |
| D=300/L=600 spent pass, drain.json | rewritten | not rewritten | — |
| D=300/L=600 spent pass, time | 47.5 s | 10.9 s (5.45 s profiled) | 52.3 s, and it stopped there (starvation) |
| D=300/L=600 2s-budget pass, time | 51.9 s | 12.6 s | 21.6 s |
| D=100/L=200 spent pass, time | 6.38 s | 293 ms | 494 ms |
| D=100/L=200 spent pass, journal queries | 61,604 | 707 | 407 |
| 4 blocked spools (slow-sync fixture), time | 8.21 s | 7.5 ms | — |
| 4 blocked spools, syncs | 4 | 0 | — |
| 4 blocked spools, admissions | 8 | 4 | — |
| 4 blocked spools, journal queries | 40 | 28 | — |
| 4 blocked spools, drain.json | rewritten | not rewritten | — |

- The fix matches c7's admission count, and admission dominates the remaining cost. The profile puts scopeRefusal at 4.07 s of 5.45 s, about 13 ms per waiting line with a populated store under co-load.
- Wall times today are heavily co-loaded. The audit measured c7 at 1.6 s for this pass.
- Probes are not committed: C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w20/zz_w20_probe_test.go and c7/internal/daemon/zz_w20_c7probe_test.go. Logs are in the same folder: red-base-long.log, after-probe.log, c7-probe.log.

ROWS
New rows, red on 738d67c7, in internal/daemon/drain_pass_cost_test.go:
- TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead: first-pass journal queries 546004 against a bound of 6307.
- TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing: 4 syncs, expected 0.
- TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent: two intents after pass 2.
- TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce: counter 3, expected 1.
- TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd: p1 not published.

New pins, green on the base and red under the named mutation:
- TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress (MX)
- TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass (M8b)
- TestDrainClientSpools_ALineRewrittenInPlaceBehindAWaitingHeadIsReadAgain (the per-line sum)
- The final step of the blob row (memo forgotten when drain.json changes behind the drainer).

MUTATION MATRIX (overlays on the fix)
- Red: MX (both subtests), per-line check only, M8b, M1 (5 rows), no memo, re-attempt all sessions, no EOF re-attempt, no save dedupe, no sync skip, no blob dedupe, no gap re-add, no line sum, no state check.
- Green: reattempt's top-of-round check alone. It is an equivalent mutant: with the per-line check in place it only changes which error path ends the file, and both are errPassBudgetSpent.

CHECKS
- All green: vet on windows, linux and darwin; golangci-lint; fmt-check; docmarkers and runpatterns; the gen-*-docs --check runs; test/docs and test/guards.
- internal/daemon in full: ok, 387.8 s.
- 14 new or changed rows: -count=20 and -race -count=3 all ok, no data race.
- 3 end-to-end spool rows by exact name: ok.

### Commits

- df53a14c fix(daemon): bound a drain pass that makes no progress
- 07568cd4 test(daemon): pin the look-ahead budget checks and progress rule
- 96f219ca test(daemon): check the backoff stall and its traffic goroutine
- a03e6375 docs(daemon): say what bounds a budgeted drain pass

### Findings resolution

- **fixed**: perf/major: drain.go:723-736,:958,:1032: a budgeted pass that only re-consumes lines is unbounded, O(L x D) reattempt plus three journal queries per processOne
  - Confirmed red on 738d67c7: the D=300/L=600 first pass made 546,004 journal queries; the spent pass made 544,804 and took 47.5 s on this host.

What I took from the auditor's fix:
- (a) Adopted: session-scoped re-attempt, plus one full re-attempt at end of file. I also added a memo so a re-consumed line costs O(1), and an unchanged spool is neither synced nor has its state rewritten again.
- (b) Rejected: a wall-clock ceiling. It would bring back the starvation that D31, D58(c) and D60(a) removed. A no-progress pass is now bounded by the spool: the bytes read plus the waiting lines, which is c7's per-line work. The rows and docs say so.
- (c) Not done: memoizing Lock.owned(). lock.go is outside this seat; it is listed under needs_owner.

Result: first pass 5,107 queries; spent pass 301 admissions, 2,107 queries, 0 syncs, 10.9 s (5.45 s profiled). Pinned by TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead, which is red under the no-memo and re-attempt-all-sessions mutants.
- **fixed**: drain/minor: drain.go:58-74,720-747,923-982: a no-progress spent pass pays one fsync and one drain.json save per blocked spool and ~3 ms per re-consumed line
  - Confirmed on base (4 blocked spools): 4 syncs, 8 admissions, drain.json rewritten, 8.21 s with the slow-sync fixture.

Fix: durableEnd skips the sync for a file this drainer synced that is unchanged by identity, size and mtime, and saveState skips writing identical bytes. Result: 0 syncs, 4 admissions, no rewrite, 7.5 ms.

Pinned by TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing, which is red under the no-sync-skip and no-save-dedupe mutants. The cost model is written into the withPassBudget doc and docs/architecture.md. The D31 ledger row is under needs_owner.
- **fixed**: drain/minor: drain.go:843-850: reattempt's two passStopped checks are unpinned (mutation MX)
  - Added TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress with two cases: progress made by the read loop (the audit's probe), and progress made by the look-ahead after a non-progress absorption.

Mutants:
- MX: both subtests red.
- Per-line check removed alone: red.
- Top-of-round check removed alone: green. It is an equivalent mutant: with the per-line check in place it only switches the file's end between the read-error and stop paths, and both are errPassBudgetSpent with notePassLeft. Recorded in commit 07568cd4.
- **fixed**: drain/minor: drain.go:763-786 + cleanupAcknowledged: PendingBlobs gains a duplicate per pass behind a blocked head
  - Red on base: [blob blob] after pass 2.

notePendingBlob appends a name only if it is absent, at all six sites, and the memo stops re-consumption anyway. TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent covers three unchanged passes, then a re-consumption after the file grows; it is red under the no-dedupe mutant.

I tried a second dedupe in cleanupAcknowledged and removed it. It masked the source fix in the mutation run, and legacy duplicates no longer grow.
- **fixed**: perf/minor: drain.go:769-772 (and :764,:785,:949): readBlob reads the whole blob only to learn its name; drain.json grows
  - pendingBlobOf names the blob from its descriptor plus one Lstat. These are readBlob's acceptance checks (descriptor field, Event, safeBlobName, regular file of the stated size) without reading the body. It is used at the retired, acknowledged, Seen-completed and denial sites; dispatchPending still reads the body because it needs it.

The acknowledged-branch naming is pinned by the blob row's final step: drain.json removed, the line re-absorbed, the intent recorded again. The growth is fixed by notePendingBlob.
- **fixed**: ci/minor: spool_watch_test.go:247-250: the stall row never asserts that its stall fired
  - The stall is now keyed on client-6161.ndjson's own sync (CompareAndSwap), not on the first syncFile call to any file. The row asserts stalled == (firstPassStall > 0) after the looks.

Green 20/20, and 3/3 under -race.
- **fixed**: complete/minor: w17c items without disposition: (1) stall assertion, (2) 'fsync pressure the back-off exists to spare' doc overstates, (3) a horizon below 4 s means no retry
  - (1) Same fix as the stall-row finding above.
(2) TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached's doc and the spool_watch.go header now say the back-off limits the passes a spool itself makes due. A pass another spool makes due still reads it, but no longer syncs it again while it is unchanged; the memo makes that true now.
(3) spool_watch.go now says a horizon of two check intervals or less (scheduler.idle.detectAfterSeconds of 4 or less, against spoolCheckInterval's 2 s) leaves the watcher no retry. This is derived from existing constants, not a new number.
- **partial**: drain/minor: docs/architecture.md:177-179 and the D31 ledger row claim 'no new line after the budget is spent'
  - docs/architecture.md is fixed: a spent budget ends a pass only after progress (front advanced, or a line published or retired); a no-progress pass reads on and is bounded by the spool.

The D31 row in plans/V6-CLOSEOUT-CHECKLIST.md is read-only for this seat. An amendment, or a pointer to D60(a), is under needs_owner.
- **fixed**: perf/minor: docs/architecture.md:176-179 claims a bounded drain pass
  - Reworded to say exactly what bounds a pass:
- after progress: at most the line in flight, under drainLineDeadline;
- before progress: the spool; each waiting line is admitted and checked again, a consumed line costs only its read, and an unchanged file is neither synced nor has its progress rewritten.

The auditor's hard ceiling is rejected, for the D31/D58(c) reason given in the first finding. test/docs is green.
- **fixed**: nit: drain.go:860: S3's `if changed` in reattempt is unpinned (mutation M8b)
  - Added TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass. p0 and p1 are accepted live; their spooled copies sit behind a waiting head, and both live copies publish while the pass reads. The look-ahead absorbs p1, and the spent pass must still publish the next spool's fresh capture.

Red under M8b; 20/20 and race 3/3 green.
- **fixed**: nit: drain.go:883-1037: no reattempt after the read loop ends
  - Every deferred line is now re-attempted once at end of file, unless the pass was cancelled or hit a read error; passStopped keeps D31. This also covers async settlements that the old re-attempt of every line after every consumed line caught by chance.

Pinned by TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd: red on base, red under the no-EOF mutant.
- **fixed**: nit: spool_watch_test.go:56: require.True on the traffic goroutine
  - spoolWatchTraffic reports with t.Errorf and returns, so t.FailNow is never called off the test goroutine. No assertion is weakened: a hook that is not served still fails the test.
- **fixed**: nit: drain.go:908-916,963-974: a corrupt or never-leased unadmitted line behind a blocked head is counted and logged Warn/LOUD on every pass
  - Via the memo, the counter and the Warn/LOUD fire once per memo life. DrainGaps still reports the same gaps on every pass.

TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce was red on base (counter 3, expected 1). It is red under the no-memo mutant, and red under the no-gap-re-add mutant through its gap-fidelity assertion.
- **deferred**: nit: CI time of the sleep-bound rows (about 36 s per daemon pass)
  - The auditor says no action is needed for the freeze. My fixture move keeps the same hold per pass (idleRunBudget + spoolWatchTick), now on the waiting head's admission instead of the sync.

The new D=300/L=600 cost row adds time: about 20 s on one run, and 892 s for -count=20 (about 45 s per run) on this co-loaded host. Its first pass makes over 1,800 lease, capture and ack fsyncs. Listed under needs_owner.

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/daemon (three runs)`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/...`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check`: exit 0 for all three
- `go test -p 1 -count=1 ./test/docs ./test/guards`: First run: TestGuard_EveryProductReadIsClassified failed, because my loadStateBytes moved os.ReadFile out of the classified loadState. Fixed by keeping the read in loadState and comparing re-marshalled state (stateIsOwn). Re-run: test/docs ok 35.0s, test/guards ok 84.5s.
- `go test -p 1 -count=1 -timeout 60m ./internal/daemon`: ok 387.754s
- `go test -p 1 -count=1 ./internal/daemon on 738d67c7 plus the new test file, -run anchored to the five red rows (names in the summary)`: All five FAIL for the stated reasons: syncs 4 expected 0; [blob blob]; counter 3 expected 1; p1 not published; first-pass journal queries 546004 against the 6307 bound (110 s)
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead$' ./internal/daemon`: ok 892.195s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing$' ./internal/daemon`: ok 31.148s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent$' ./internal/daemon`: ok 14.509s
- `go test -p 1 -count=20 -run '^TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce$' ./internal/daemon`: ok 7.826s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd$' ./internal/daemon`: ok 20.863s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineRewrittenInPlaceBehindAWaitingHeadIsReadAgain$' ./internal/daemon`: ok 16.248s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress$' ./internal/daemon`: ok 39.437s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass$' ./internal/daemon`: ok 22.345s
- `go test -p 1 -count=20 -run '^TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick$' ./internal/daemon`: ok 10.076s
- `go test -p 1 -count=20 -run '^TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff$' ./internal/daemon`: ok 52.541s
- `go test -p 1 -count=20 -run '^TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead$' ./internal/daemon`: ok 139.654s
- `go test -p 1 -count=20 -run '^TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead$' ./internal/daemon`: ok 94.100s
- `go test -p 1 -count=20 -run '^TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff$' ./internal/daemon`: ok 135.889s
- `go test -p 1 -count=20 -run '^TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt$' ./internal/daemon`: ok 98.632s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead$' ./internal/daemon`: ok 152.747s, no DATA RACE
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing$' ./internal/daemon`: ok 4.412s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent$' ./internal/daemon`: ok 2.494s
- `go test -p 1 -race -count=3 -run '^TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce$' ./internal/daemon`: ok 1.777s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd$' ./internal/daemon`: ok 3.772s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineRewrittenInPlaceBehindAWaitingHeadIsReadAgain$' ./internal/daemon`: ok 2.926s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress$' ./internal/daemon`: ok 4.639s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass$' ./internal/daemon`: ok 4.830s
- `go test -p 1 -race -count=3 -run '^TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick$' ./internal/daemon`: ok 2.702s
- `go test -p 1 -race -count=3 -run '^TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff$' ./internal/daemon`: ok 9.196s
- `go test -p 1 -race -count=3 -run '^TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead$' ./internal/daemon`: ok 21.315s
- `go test -p 1 -race -count=3 -run '^TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead$' ./internal/daemon`: ok 15.997s
- `go test -p 1 -race -count=3 -run '^TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff$' ./internal/daemon`: ok 20.612s
- `go test -p 1 -race -count=3 -run '^TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt$' ./internal/daemon`: ok 15.887s
- `mutation matrix: go test -overlay <mutant drain.go> on the fix, one mutant at a time, with the rows named in the summary`: Red: MX, per-line check alone, M8b, M1 (5 rows incl. the 4 moved-fixture rows), no memo, re-attempt all sessions, no EOF re-attempt, no save dedupe, no sync skip, no blob dedupe, no gap re-add, no line sum, no state check. Green: top-of-round check alone (equivalent mutant, explained)
- `go test -p 1 -count=1 -run '^TestE2ESpoolSubmodeEndToEnd$' ./test/e2e`: ok 4.927s
- `go test -p 1 -count=1 -run '^TestE2E_ThinSliceDropsControlOnlyEdges$' ./test/e2e`: ok 13.890s
- `go test -p 1 -count=1 -run '^TestE2E_SpooledSessionStartNeverDegradesTheProject$' ./test/e2e`: ok 5.286s

### Criterion changes

- Four rows used slowSpoolSyncs, which held every client-spool sync, and now use slowWaitingHead, which holds every admission of the waiting head past idleRunBudget + spoolWatchTick. Their fixture check is strengthened from 'held > 0' to 'held == number of passes'. The rows are TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead, TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead, TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff and TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt. Reason: the fix no longer syncs an unchanged blocked spool, so a held sync would spend the budget only in the first pass. These rows test the later passes, which must reach the re-consumed line with the budget spent. All four still fail under mutation M1.
- TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff's stall is keyed on client-6161.ndjson's own first sync instead of the first syncFile call to any file, and the row now asserts the stall fired exactly when firstPassStall > 0. This is stricter.
- spoolWatchTraffic uses t.Errorf plus return instead of require, because it runs off the test goroutine. A hook that is not served still fails the test.
- Product: a line consumed out of order behind a waiting head is skipped by later passes of the same daemon. They do not admit it again, look up its lease, query the journal, read its blob, append its cleanup intent, count or log it, or re-attempt deferred lines after it. They re-add its gaps, so DrainGaps reads the same on every pass. The drain_file_error and drain_unadmitted counters and the corrupt-line Warn and unadmitted Loud now fire once per memo life, not once per pass.
- Product: state/drain.json is not rewritten by a pass that changes no progress, and a spool file unchanged since this drainer synced it is not synced again.
- Product: cleanup intents are unique per file. An absorbed, retired or denied line's intent is named from its descriptor plus an Lstat, using readBlob's acceptance checks, without reading the blob body.
- Product: after a consumed line, only that line's session's deferred lines are re-attempted (before: every deferred line, after every consumed line), and every deferred line is re-attempted once at end of file (before: never).

### Open issues

- Wall times on the shared host are noisy and co-loaded. Today c7's D=300/L=600 spent pass took 52.3 s with 302 admissions, against the audit's 1.6 s on a quieter host. A quiet-host A/B of c7 against c8 with this fix should go in the night chain; the deterministic operation counts are the firm evidence.
- Re-admitting each waiting line now dominates a no-progress pass: scopeRefusal is 4.07 s of a 5.45 s profiled pass, about 13 ms per line with a populated store under load. c7 pays the same per line. Memoizing it is out of scope, because the policy can change between passes.
- The memo lives in memory for one daemon lifetime, so the first pass after a restart processes each blocked spool in full once.
- The end-of-file re-attempt costs 3 journal queries per waiting line per pass (in memory, plus Lock.owned's lock-file read).
- Reattempt's top-of-round passStopped check alone cannot be pinned: given the per-line check, removing it changes nothing a row can observe (recorded in commit 07568cd4).

### Needs owner

- New test-only number: journalQueriesPerLine = 7 in internal/daemon/drain_pass_cost_test.go. Derivation: a line's lease lookup (1), plus processOne's terminal, acknowledged and predecessor queries as it is read (3), plus those three again at the end-of-file re-attempt for a line still waiting (3). A published line pays its acknowledgement instead. It bounds the cost rows' journal queries: base 546,004 against a bound of 6,307; the fix makes exactly 5,107 and 2,107.
- Ledger: the D31 row (plans/V6-CLOSEOUT-CHECKLIST.md, read-only for this seat) still says 'no new line after the budget is spent'. Suggested amendment, or pointer to D60(a): a spent budget ends a pass only once it has made progress; a no-progress pass is bounded by the spool (its unconsumed bytes and its waiting lines), not the clock.
- Ratify a semantics change: a line consumed out of order behind a waiting head is final for the daemon's lifetime, as a line the front has passed always was. A later policy change no longer re-decides it on later passes (before, a later pass re-admitted it and could publish a line an earlier pass denied). SpooledPromptSettled is told once per consumption; the observer call is idempotent per nonce.
- Ratify a durability shortcut: a spool file unchanged by identity, size and mtime since this drainer's own successful sync is not synced again, and drain.json is not rewritten with identical bytes. An external in-place rewrite that also restores the mtime would not be synced again; consumption stays correct because of the per-line sum, which is pinned.
- CI time: the D=300/L=600 cost row (the task's shape) costs about 20-45 s per run on a co-loaded Windows host, because its first pass makes over 1,800 lease, capture and ack fsyncs. Using policy-denied lines for the 600 consumed lines would make it about 2 s while keeping every asserted count; I kept the published shape as the task asked.
- Lock.owned() reads the lock file on every journal query (internal/daemon/lock.go, outside this seat): 0.65 s of a 5.45 s profiled pass. The auditor's optional fix (c), a per-pass memo, is left for the owner.

## review:drain:0:r1: verdict `needs-fixes`, 8 finding(s)

- **minor** `internal/daemon/drain.go:1586 (scanPendingBlobs fails on any undecodable line), :1202 and :604 (cleanupAcknowledged after each finished file and at pass start); claim at drain.go ~385 (SpooledPromptSettled doc) and the seat's 'announced once per memo life' criterion`: The memo announces a corrupt line behind a waiting head once, but cleanupAcknowledged's reference scan then fails on that same line whenever any cleanup intent is pending anywhere. Every later pass therefore counts drain_file_error and logs a Warn 'daemon: drain: file error' for each file it finishes. Every pass returns an error, and no pending blob can ever be collected while the corrupt line stays ahead of a front. This is pre-existing, since scanPendingBlobs is unchanged, but it defeats the 'counted and announced once' outcome C1.13 asked for. TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce passes only because its fixture has no blob intent.
  - Evidence: Probe scratchpad/w20rev/zz_rev_corrupt_test.go, run on HEAD a03e6375 via go test -overlay. Spool A holds a waiting head and a corrupt line. Spool B holds a waiting head and a blob line. Results after passes 1-4: drain_file_error=2,4,6,8; corrupt-line warns=1,1,1,1; file-error warns=1,3,5,7. Every pass returned 'daemon: drain: invalid blob reference source'.
  - Fix: In scanPendingBlobs, do not fail on a line that does not decode. Mark as referenced every pending intent name that appears in its raw bytes, which is conservative because nothing a line could name is deleted, then continue. Add a row: a corrupt line behind a waiting head plus one pending intent; assert drain_file_error stays at 1 and the pass returns nil across 3 passes.
- **minor** `internal/daemon/drain.go:1513-1558 (cleanupAcknowledged/pendingBlobReferences), :1202; docs/architecture.md:185; withPassBudget doc drain.go:79-81; spool_watch.go header ('it pays the spool's read and its waiting lines')`: While any cleanup intent is pending, which lasts as long as a blob line sits behind a waiting head, every no-progress pass reopens and fully decodes every unconsumed line of every spool file. It does this once at pass start and once more after each file it finishes, so it costs O(F^2) file reads per pass. The audit's PendingBlobs finding named this re-scan; the seat fixed only the duplicate intents and gave the re-scan no disposition. The new docs claim that a line consumed behind a waiting head 'costs only its read', which is false in this state.
  - Evidence: Instrumentation-only overlay scratchpad/w20rev/instr_scans.go (adds a counter in scanPendingBlobs) with probe zz_rev_scan_test.go: 4 blocked spools, one with a blob line behind its head. Each no-progress pass (budget 0) shows scans=20, syncs=0, admitted=4, journal=28, while the drain itself reads 4 files.
  - Fix: Run the reference scan after a file only if that file's offset or intents changed in this pass, or cache the referenced set per file keyed by identity, size and offset as spoolMemo does. Add a counted row (scans per no-progress pass with one pending intent). At minimum, qualify the 'costs only its read' wording in architecture.md, withPassBudget and spool_watch.go.
- **minor** `internal/daemon/drain.go:1383 (pendingBlobOf's safeBlobName check)`: Blob naming at the retired, acknowledged, Seen-completed and denied sites moved from readBlob, whose traversal refusal TestResolveBlob_RefusesPathTraversal pins, into the new pendingBlobOf, and no row pins its safe-name check. If the check were dropped, a hostile spool line that is denied or absorbed and whose descriptor names a file outside spool/ would write an intent that loadState refuses ('invalid cleanup intent'). Every later pass would then be wedged. Product code is correct today.
  - Evidence: Mutant scratchpad/w20rev/mut_nosafe.go replaces line 1383's '!safeBlobName(ref.Blob)' with 'false'. go test -overlay over the whole of ./internal/daemon: ok 405.507s (survives). Probe zz_rev_safe_test.go passes on HEAD; under the mutant the second pass fails with 'daemon: drain: invalid cleanup intent'.
  - Fix: Commit the probe as a row: a policy-denied line whose descriptor names '../outside.bin', a regular file of the stated size, behind a waiting head; two passes, assert NoError. Optionally add an absorbed variant.
- **minor** `internal/daemon/drain.go:1855-1862 (saveState's forgetMemos on MkdirAll/WriteAtomic failure)`: The save-failure branch that forgets every memo is load-bearing, but no row pins it. Without it, stateOnDisk still holds the last successful write. The next pass then finds that older progress 'own', keeps the memo and skips the blob line whose cleanup intent never reached disk, so the blob leaks for the daemon's lifetime.
  - Evidence: Mutant mut_saveforget.go removes both forgetMemos calls. Committed rows '^(TestDrain|TestIdleDrain_|TestSpoolWatch_|TestSpoolRetryAfter_|TestDeliveryOrder_|TestPreCompactSettle_|TestCarriedDefect_)': ok 323.543s (survives). Probe zz_rev_probe_test.go TestZZRev_ASaveThatFailsForgetsTheMemo: Dispatch publishes the blob line, then .qompack/tmp is replaced by a file so only the drain-state save fails. Green on HEAD; under the mutant PendingBlobs is []string(nil) where [blob-8951-1.bin] is expected.
  - Fix: Commit the probe as a row, keeping the tmp-dir fault injected after the blob line's Dispatch returns. A read-only drain.json does not work as the fault, because WriteAtomic clears the attribute and retries.
- **minor** `internal/daemon/drain_pass_cost_test.go (meterDrainCost); drain.go:1377 pendingBlobOf`: The task required deterministic rows that count admissions, processOne calls, journal queries, blob reads and fsyncs. The rows count admissions, journal queries, syncs and drain.json rewrites. Blob reads are counted nowhere, and processOne calls only through journal queries. pendingBlobOf's main property, naming the blob without reading its body, is therefore an equivalent mutant for every row: putting readBlob back at the four sites stays green.
  - Evidence: No counter in drain_pass_cost_test.go touches readBlob, paths.OpenShared or blob bytes. TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent asserts only the intent list, and its 'final step' pins that the name is recorded again, not how it was obtained.
  - Fix: Add a blob-read seam or counter, for example a drainer field wrapping readBlob, or count OpenShared opens of spool/blob-*. In the blob row and a denied-blob variant, assert zero body reads for absorbed, retired and denied lines and for memo-skipped re-consumption.
- **nit** `internal/daemon/drain.go:1248-1250 (durableEnd's durable path returns synced=true)`: The sync skip carries forward from pass to pass only because the durable path reports synced=true for the next memo. Every committed sync and cost row stops at two passes, so returning synced=false is unpinned; an unchanged blocked spool would then be synced on every other pass.
  - Evidence: Mutant mut_dursync.go ('return size, false, nil'). Committed rows '^(TestDrainClientSpools_|TestDrain_|TestSpoolWatch_|TestIdleDrain_|TestDrainSyncs|TestDrainLeaves|TestDrainFsyncs|TestDrainNeverLeases|TestDrainLeasesNothing)': ok 115.336s (survives). Probe TestZZRev_TheSyncSkipHoldsOverEveryLaterPass: HEAD shows syncs 1,0,0,0; the mutant shows 1,0,1.
  - Fix: Extend TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing to a third no-progress pass that also asserts zero syncs and no rewrite.
- **nit** `internal/daemon/drain.go:71-73 (withPassBudget doc); docs/architecture.md:179-180`: The task asked the doc to say exactly what bounds a pass. 'Once it has made progress it runs past its budget by at most the line that made it, under that line's drainLineDeadline' is not exact in two ways. First, when progress came before the budget expired, the overrun is whatever line is in flight at expiry, which need not be a progress line. Second, drainLineDeadline bounds only that line's dispatch, not its admission, lease or journal work; slowWaitingHead holds an admission for idleRunBudget plus a tick.
  - Evidence: In the read loop, passStopped is checked only before ReadBytes. admitLine, leaseDelivery and processOne's journal queries run under no deadline; dispatchPending alone takes drainLineDeadline (drain.go:31-33).
  - Fix: Reword along these lines: 'once its budget is spent and it has made progress, it starts no new line: it overruns by at most the one line in flight when the budget ran out (the dispatch under drainLineDeadline; admission and journal checks unbounded by it), or, if the budget ran out first, by every line up to and including the one that makes progress, plus the bookkeeping that closes that file'.
- **nit** `internal/daemon/drain.go:1334-1345 (rememberFile), 965-977 (memo replay)`: Two fidelity gaps in the memo. (1) A pass that stops mid-file on an unchanged file replaces the memo with only the lines it read. Memo lines past the stop are dropped, so the next pass admits them again and re-announces any corrupt or unadmitted ones. Stops include a hard journal or dispatch error, ErrDegraded and a budget stop after progress. (2) A line absorbed through Seen completion before its ack existed replays 'in-memory completion has no frontier record' on every pass for the memo's life, even after the ack lands, where base stopped reporting it.
  - Evidence: rememberFile stores `processed`, which holds only lines with start < readPos. The memo gap replay re-adds known.gaps unconditionally.
  - Fix: (1) When the file is unchanged, carry forward memoLines entries with start >= readPos into the new memo. (2) Accept and document the stale gap, or do not memo a line consumed with DrainGapUnacknowledged.

## review:drain:1:r1: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/daemon/drain.go:779-781 and :1290-1293 (the memo only keeps what processed keeps, capped at orderingProcessedCap); doc claims at drain.go:79-81, drain.go:388-389, docs/architecture.md:184-186`: The fix does not make every re-consumed line cost O(1), and the docs say it does. spoolMemo.consumed is drainFile's processed map, and consume() stops adding to that map after orderingProcessedCap (4096) out-of-order lines per file. Every later pass consumes each line past the cap in full: it admits it again, looks up its lease, queries the journal, and counts and announces it again (Warn for a corrupt line, LOUD for an unadmitted one). Two places still overclaim: (1) withPassBudget's doc and docs/architecture.md:185 say such a line "costs only its read", with no condition; (2) the seat's resolution of the corrupt/unadmitted nit says "once per memo life". The audit's own 10k-corrupt-line shape is still announced 5904 times on every pass. The same sentences also leave out that the whole memo is dropped whenever the file changes. One example is a reused pid's hook appending to the spool, which is the fixture the new blob row uses. Every remembered line is then consumed in full again once. The spoolMemo doc says this; the public docs do not.
  - Evidence: Probe zz_rv_probe_test.go / zz_rv_head_test.go (in scratchpad/w20-review, run with -overlay, -p 1). Setup: 10,000 corrupt lines behind a waiting head, then three spent passes. drain_file_error delta per pass: HEAD a03e6375 +10000, +5904, +5904; base 738d67c7 +10000 each pass; c7 d20309c0 +1 each pass (it stops at the first line). A second probe used 6,000 policy-denied lines of another session behind a waiting head. HEAD admissions per pass: 6001, 1905, 1905 (1904 = 6000 - 4096). Under the cap the fix behaves as claimed. With 2,000 acknowledged lines behind a waiting head, each later pass on HEAD made 1 admission and 7 journal queries in 7-60 ms. The same shape cost 2001 admissions, 12004 queries and 7-17 s per pass on base. The rest of the lens reproduced at HEAD / base / c7 on this host in one session. D=300/L=600 spent pass: 1.81 s / 105.9 s / 1.45 s. Admissions 301 / 901 / 302, journal queries 2107 / 544804 / 1207, syncs 0 / 1 / 1, drain.json rewritten no / yes / yes. 2 s-budget pass: 2.16 s / 87.3 s / 2.02 s. First unbudgeted pass journal queries: 5107 / 546004 / 546004. 4 blocked spools with slow syncs, idleRunBudget pass: 31 ms / 8.21 s / 2.06 s (c7 stopped in the first spool). Admissions 4 / 8 / 2, syncs 0 / 4 / 1, rewritten no / yes / yes. Overlaying the committed cost rows onto 738d67c7 turns them red with "546004 is not <= 6307" and "Should be zero, but was 4". Their counts are deterministic: my independent meter matched the seat's exactly (5107, 2107, 301, 28, 4). A memo-unblock probe passed: the head's predecessor publishes, the front rolls over the remembered lines, and the spool and blob are released, whether the release pass is unbudgeted or spent.
  - Fix: Either (a) record out-of-order lines for the memo in a structure of their own, not capped by the roll-forward cap. Start, next and sum take about 24 B a line; bound it by a derived figure such as the file's line count. Or (b) state the bound. In withPassBudget's doc (drain.go:79-81), DrainConfig.SpooledPromptSettled (drain.go:388) and docs/architecture.md:184-186, say a consumed line costs only its read only up to orderingProcessedCap lines per file and only while the file is unchanged. Past the cap, or after the file changes, the line is consumed again in full: admission, lease lookup, journal queries, and its count and announcement. Either way, change the corrupt/unadmitted nit's resolution to partial, and add a row that pins whichever behaviour is chosen, for example 4096 + k corrupt lines asserting the per-pass delta.
- **nit** `internal/daemon/drain.go:1198-1204 (drainFile calls cleanupAcknowledged after every file) and :604, with :1513-1593 (pendingBlobReferences re-reads and decodes every spool file)`: Cleanup rescans still add I/O beyond the read in the blob-line shape this round targets. When a blob line has been consumed behind a waiting head, its cleanup intent stays pending for as long as the head waits. While any intent is pending, cleanupAcknowledged runs pendingBlobReferences on every pass: it lists the spool directory and decodes every line of every spool file from its offset. It runs once at the start of the pass and again after every file's end, even in a pass that changed nothing and whose saveState wrote nothing. So a no-progress pass over F blocked spools that each have a blob line behind the head reads and decodes the whole spool F+1 times on top of its own read. The task's goal was that a re-consumed out-of-order line costs nothing beyond its read. This cost predates the round and the diff does not change it, which is why it is a nit.
  - Evidence: Found by reading the code at a03e6375. drainFile calls dr.saveState(st), which skips the write when nothing changed, and then calls dr.cleanupAcknowledged(st) with no condition (drain.go:1198-1204). cleanupAcknowledged returns early only when no file has PendingBlobs (drain.go:1514-1520). Otherwise pendingBlobReferences runs ipc.SpoolFiles and scanPendingBlobs, which calls ipc.DecodeRequest on every line of every file (drain.go:1547-1593). passLocked also calls it before the first file (drain.go:604). TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent is exactly this state: the intent is still pending after every pass.
  - Fix: In drainFile, skip cleanupAcknowledged when this file's front did not advance and its PendingBlobs did not change during the pass. Nothing the file did can then have released an intent, and the call at the start of the pass still covers files that advanced or were removed elsewhere. Alternatively, compute the referenced set once per pass and reuse it across files. Add a scan counter, for example an injectable hook on scanPendingBlobs, to the 4-blocked-spool cost row with a blob line behind each head.

## fix:drain:r1: status `done`, head `6dcdb6b761449186cd18ea980121d6bff075deb2`

### Root cause

C1.13 counts only progress against a pass budget, so a budgeted pass that makes no progress reads every spool to its end. Behind a head waiting on an earlier arrival, every line consumed out of order was consumed again in full on every pass, because `processed` lived only for one drainFile call. reattempt() also re-ran every deferred line after every consumed line, whatever its session (O(L x D)). Every blocked spool paid an fsync and a drain.json rewrite on every pass.

Round 1 found the same class one layer down:
- cleanupAcknowledged re-scanned the whole spool after every file, O(F^2) per pass, even when nothing it could release had changed.
- scanPendingBlobs failed on any line that would not decode, so one corrupt line ahead of a front failed every pass's cleanup.
- The memo was dropped on any change to its file, including appends.
- The memo forgot lines past an early stop.
- The memo replayed a gap that could close.
- Past orderingProcessedCap, the memo re-announced lines on every pass.

### Summary

PRODUCT CODE CHANGED (internal/daemon/drain.go, internal/daemon/delivery_order.go; doc-only changes in internal/daemon/spool_watch.go and docs/architecture.md).

Branch closeout/w20-drain, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-drain. Six commits on 738d67c7: the four from round 0, plus 1e74e637 (fix) and 6dcdb6b7 (docs) from this round. The tree is clean and nothing is pushed.

ROUND 0, carried forward
- A per-drainer in-memory memo (spoolMemo) for each spool file with bytes still past its consumed front. Lines a pass consumed out of order cost later passes only their read and an FNV-1a sum check.
- Two durability shortcuts: an unchanged, already-synced spool is not synced again, and identical progress is not rewritten.
- Re-attempts are scoped to the session, plus one end-of-file re-attempt.
- Cleanup intents are de-duplicated and named without reading the blob body (pendingBlobOf).
- The pass-budget documentation was rewritten.
- Round-0 measurements (same Windows host, audit probe):
  - D=300/L=600 spent pass: 901 → 301 admissions, 544,804 → 2,107 journal queries, 1 → 0 syncs, 47.5 s → 10.9 s (c7: 302 admissions, 52.3 s, and it stopped there).
  - 4 blocked spools: 8.21 s → 7.5 ms, 4 → 0 syncs.

REVIEW RESOLUTION (round 1)
All 6 minor findings and 4 nits hold on re-verification. Each was fixed or pinned, except the cost half of R6, which is a documented bound.

R1, a corrupt line fails the reference scan: fixed.
- Reproduced at a03e6375: every pass returned "daemon: drain: invalid blob reference source".
- scanPendingBlobs now skips a line that does not decode. Such a line still holds back each pending blob whose name it carries, plain or JSON-encoded, because a binary that can decode it could still replay it.
- Row TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames has two subtests:
  - corrupt line names no blob: the blob and its spool are released, drain_file_error stays at 1, and no "file error" Warn appears over 3 passes;
  - corrupt line names the blob: the blob is kept.
- Red before the fix; red under three mutants (error on the line, reference nothing, reference everything).

R2 and nit 4, O(F^2) cleanup rescans: fixed.
- The cleanup after a file now runs only when the pass moved that file's front.
- A narrower intents-changed half was considered and dropped as provably redundant: admission never rewrites req.Raw, so an intent added without moving the front belongs to a line ahead of that front, which still references it.
- A scanBlobRefs seam counts the scans.
- TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing now puts a blob line behind each of the 4 heads. Scans per no-progress pass: 20 → 4. Syncs 0, no progress rewrite, 4 admissions, 0 blob reads, asserted on passes 2 and 3.
- Red under cleanup-always. The new row TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes is red under cleanup-never.
- The start-of-pass scan remains; it is documented, and listed under open issues.

R3, pendingBlobOf's safe-name check was unpinned: pinned.
- Row TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent, red under the no-safeBlobName mutant.

R4, saveState's forgetMemos on a failed write was unpinned: pinned.
- Row TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered injects the .qompack/tmp fault after Dispatch. Red under the no-forget mutant.

R5, blob body reads were not counted: fixed.
- A readBlobBody seam is now the drain's only way to read a blob body.
- The meter counts body reads. The blob row asserts 1 read in pass 1 (the publication) and 0 for the denied line, for the memo-skipped re-consumption, and for a restarted drainer that absorbs the line.
- TestDrainer_ReadsABlobBodyOnlyThroughItsSeam parses drain.go and fails on any direct readBlob call.
- Mutant results: naming through the seam turns the blob row red; calling the package readBlob directly turns the source pin red.

R6, the memo cap: partial.
- The memo still holds at most orderingProcessedCap (4096) out-of-order lines per file. No new number was introduced.
- Fixed beyond the cap: corrupt and unadmitted lines are now counted and announced once per memo life, past the cap too (spoolMemo.readTo).
- Fixed beyond the reviewer's ask: the memo used to be dropped whenever the file changed, so each hook append made the next pass consume everything behind the head again in full. A memo now survives growth (same identity, no shorter); per-line sums still guard every skip, and the sync skip still needs the same size and mtime.
- Remaining cost: past the cap, a line is re-admitted and journal-checked on every pass. This is now stated in withPassBudget, the SpooledPromptSettled doc, spoolMemo, spool_watch.go and docs/architecture.md, and pinned by TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce (4096 corrupt lines plus 3 unadmitted lines, including a pass that stops early). That row is red under the no-readTo and no-max mutants.
- New rows:
  - TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain: red under the same-size and unchanged-always mutants.
  - TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain: red under the no-identity mutant. It stats through handles, because os.Stat identity loads lazily by path on Windows.

Nit 1, durableEnd's synced=true across passes: pinned by a third pass in the blocked-spools row; red under the dursync mutant.

Nit 2, the withPassBudget wording: fixed.
- It now says: once spent with progress, the pass starts no new line.
- It overruns by the line in flight, or by every line up to the progress line when the budget ran out first, plus the file-closing bookkeeping.
- drainLineDeadline bounds only the dispatch.

Nit 3, memo fidelity: fixed.
- (1) A pass that stops early keeps the memo lines past its stop. Row TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop, red under the no-carry mutant.
- (2) A line consumed with DrainGapUnacknowledged is not memoized. Row TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands, red under the no-settled-filter mutant.

RED FIRST
Six rows were red on a03e6375 plus the new seams (otherwise identical to a03e6375), each for its stated reason:
- the R1 row: "invalid blob reference source";
- the blocked-spools row: 20 scans, expected 4;
- the cap row: unadmitted counter 6, expected 3;
- the early-stop row: 3 re-admissions;
- the unacknowledged row: stale unacknowledged gap;
- the growth row: 3 re-admissions.
The rows cannot compile on 738d67c7, which has no seams. The pins are green on HEAD and red under their named mutants.

MUTATION MATRIX (go test -overlay)
- 20 mutants were run against 16 rows; every one turned at least one row red.
- One more mutant (a direct readBlob call) was swapped into the file in place, because the source pin reads drain.go from disk; it turned the source pin red, and the file was restored and checked with cmp.
- The only equivalent mutant left is round 0's top-of-round passStopped check, explained in commit 07568cd4.

CHECKS, all green
- go vet on windows, linux and darwin; golangci-lint; fmt-check; docmarkers and runpatterns; gen-config-docs, gen-command-docs and gen-mcp-docs --check.
- test/docs ok 5.7 s; test/guards ok 50.7 s.
- internal/daemon in full: ok, 378.0 s.
- 13 new or changed rows: -count=20 and -race -count=3, all ok with no DATA RACE.
- 3 end-to-end spool rows: ok.

Probes and logs: C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w20r1/ (mutants.py, mut/*.log, red-seam.log, full-daemon.log, rows.out).

### Commits

- df53a14c fix(daemon): bound a drain pass that makes no progress
- 07568cd4 test(daemon): pin the look-ahead budget checks and progress rule
- 96f219ca test(daemon): check the backoff stall and its traffic goroutine
- a03e6375 docs(daemon): say what bounds a budgeted drain pass
- 1e74e637 fix(daemon): bound blob cleanup and keep spool memos exact
- 6dcdb6b7 docs(daemon): state what a no-progress drain pass costs

### Findings resolution

- **fixed**: perf/major: drain.go:723-736,:958,:1032: a budgeted pass that only re-consumes lines is unbounded, O(L x D) reattempt plus three journal queries per processOne
  - Round 0: a per-drainer memo makes a re-consumed line cost its read and an FNV-1a check; re-attempts are scoped to the session plus one at end of file; an unchanged spool is not synced and identical progress is not rewritten. D=300/L=600 spent pass: 901 → 301 admissions, 544,804 → 2,107 journal queries, 1 → 0 syncs, 47.5 s → 10.9 s. The auditor's wall-clock ceiling is rejected because it would bring back the starvation D31, D58(c) and D60(a) removed. Round 1 removes two remaining costs: a memo now survives its file growing (a hook appending behind a waiting head no longer forces a full re-consume), and the cleanup re-scan runs only after a file whose front moved. Pinned by TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead (re-run -count=20 ok 429 s, race 3/3 ok) and TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain. Bound past orderingProcessedCap: see R6.
- **fixed**: drain/minor: drain.go:58-74,720-747,923-982: a no-progress spent pass pays one fsync and one drain.json save per blocked spool and ~3 ms per re-consumed line
  - Round 0: 4 → 0 syncs and 8 → 4 admissions per pass, drain.json no longer rewritten, 8.21 s → 7.5 ms. Round 1 extends TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing to a third no-progress pass and to blob lines behind each head. Passes 2 and 3 each assert 0 syncs, no rewrite, 4 admissions, 4 scans and 0 blob reads. Red under dursync, cleanup-always, no-sync-skip and no-save-dedupe.
- **fixed**: drain/minor: drain.go:843-850: reattempt's two passStopped checks are unpinned (mutation MX)
  - Unchanged from round 0. TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress has two cases and is red under MX and under the per-line check removed alone. The top-of-round check alone is an equivalent mutant, recorded in commit 07568cd4.
- **fixed**: drain/minor: drain.go:763-786 + cleanupAcknowledged: PendingBlobs gains a duplicate per pass behind a blocked head
  - notePendingBlob de-duplicates intents. The round-1 blob row no longer forces re-consumption by growing the file, because the memo now survives growth. It uses a restarted drainer instead, which re-consumes the published line and a policy-denied blob line with both intents already in state. Intents stay [blob, deniedBlob] across three passes, the restart, and the drain.json removal step. Red under no-blob-dedupe.
- **fixed**: perf/minor: drain.go:769-772 (and :764,:785,:949): readBlob reads the whole blob only to learn its name; drain.json grows
  - pendingBlobOf names the blob from the descriptor plus an Lstat. Round 1 makes this observable: readBlobBody is the drain's only blob-body reader, and the meter counts it. The blob row asserts 1 read in pass 1 (the publication) and 0 for the denied line, for memo-skipped passes and for the restarted absorb. TestDrainer_ReadsABlobBodyOnlyThroughItsSeam fails on any direct readBlob call in drain.go.
- **fixed**: ci/minor: spool_watch_test.go:247-250: the stall row never asserts that its stall fired
  - Unchanged from round 0: the stall is keyed on client-6161.ndjson's own sync, and the row asserts that the stall fired exactly when firstPassStall > 0.
- **fixed**: complete/minor: w17c items without disposition: (1) stall assertion, (2) 'fsync pressure the back-off exists to spare' doc overstates, (3) a horizon below 4 s means no retry
  - Unchanged from round 0, except that round 1 restates the spool_watch.go header's cost sentence precisely: it pays the spool's read and its waiting lines within the bounds spoolMemo states, plus one more read while a blob's cleanup waits. No new number.
- **partial**: drain/minor: docs/architecture.md:177-179 and the D31 ledger row claim 'no new line after the budget is spent'
  - docs/architecture.md is fixed: after progress the pass starts no new line, and before progress it is bounded by the spool. The D31 row in plans/V6-CLOSEOUT-CHECKLIST.md is read-only for this seat and is listed under needs_owner.
- **fixed**: perf/minor: docs/architecture.md:176-179 claims a bounded drain pass
  - Round 1 reworded the paragraph again so that every claim is exact:
- after progress, no new line starts; the line in flight finishes, with only its dispatch under drainLineDeadline;
- a consumed line costs only its read for up to orderingProcessedCap (4096) lines per file, while the file is the same file and has only grown;
- past that bound, a line is re-admitted on every pass but announced once;
- a restarted daemon consumes each such line once more;
- while a blob's cleanup waits, the pass makes one extra start-of-pass read for references.
test/docs is green.
- **fixed**: nit: drain.go:860: S3's `if changed` in reattempt is unpinned (mutation M8b)
  - Unchanged from round 0: TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass, red under M8b.
- **fixed**: nit: drain.go:883-1037: no reattempt after the read loop ends
  - Unchanged from round 0: a one-time end-of-file re-attempt, pinned by TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd.
- **fixed**: nit: spool_watch_test.go:56: require.True on the traffic goroutine
  - Unchanged from round 0: t.Errorf plus return.
- **fixed**: nit: drain.go:908-916,963-974: a corrupt or never-leased unadmitted line behind a blocked head is counted and logged Warn/LOUD on every pass
  - The reviewer asked for partial, because lines past orderingProcessedCap were re-announced on every pass. Round 1 closes that: spoolMemo.readTo records how far the memo's passes have read, and a corrupt or unadmitted line before it is not counted or announced again, whether or not the cap let the memo keep it. This holds across a pass that stops early (max with the previous readTo). Pinned by TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce (4096 corrupt lines plus 3 unadmitted lines past the cap; counters and Warn/LOUD unchanged after passes 2-5, including an early-stop pass), red under no-readTo and no-max. The other source of repeated file errors (R1) is fixed too. Announcements recur once per memo life: after a restart, a replaced file, a drain.json the drainer did not write, or a failed progress write.
- **deferred**: nit: CI time of the sleep-bound rows (about 36 s per daemon pass)
  - Unchanged. The auditor says no action is needed for the freeze. The D=300/L=600 cost row costs 429 s for -count=20 today (about 21 s per run). Listed under needs_owner.
- **fixed**: R1 minor: drain.go:1586 scanPendingBlobs fails on any undecodable line; a corrupt line behind a waiting head plus any pending intent makes every pass error and count a file error per finished file
  - Confirmed at a03e6375: the reviewer's probe gave drain_file_error 2/4/6/8 over four passes, and my row failed with 'daemon: drain: invalid blob reference source'.
- Fix: a line that does not decode is skipped. It still marks as referenced each pending intent whose name it contains, as the raw name or its JSON-encoded form (encodedBlobName), because a newer binary could decode and replay it.
- The trailing partial-line refusal is unchanged; see open_issues.
- Row TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames, two subtests:
  - names no blob: 3 passes all return nil, drain_file_error == 1, zero 'file error' Warns, and the blob and its spool are released;
  - names the blob: the blob is kept.
- Red on a03e6375 plus seams; red under the scan_err, scan_none and scan_all mutants; 20/20 and race 3/3 green.
- **fixed**: R2 minor: cleanupAcknowledged/pendingBlobReferences re-scan every spool file at pass start and after each file while any intent is pending (O(F^2)); 'costs only its read' overclaims
  - Confirmed: 20 scans per no-progress pass over 4 blocked spools (one pass-start scan of 4 files, then a 4-file scan after each of the 4 files).
- Fix: the cleanup after a file runs only when the pass moved that file's front (fs.Offset != offsetBefore). Nothing else can release an intent: admission never rewrites req.Raw, so an intent added without moving the front belongs to its own line, ahead of the front, and that line still references it.
- A scanBlobRefs seam counts scans. The blocked-spools row asserts 4 scans per pass, red under cleanup-always. TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes pins that a front roll-over still collects in the same pass, red under cleanup-never.
- The pass-start scan stays: one extra read of the spool while an intent waits. It is documented in withPassBudget, spool_watch.go and architecture.md, and listed in open_issues.
- **fixed**: R3 minor: drain.go:1383 pendingBlobOf's safeBlobName check is unpinned; dropping it lets a hostile descriptor wedge every pass via an unsafe intent
  - Committed the probe as TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent. A policy-denied line behind a waiting head names '../outside.bin', a regular file of the stated size. Two passes return nil, PendingBlobs is empty, and the outside file stays. Green on HEAD, red under the nosafe mutant, 20/20 and race 3/3 green.
- **fixed**: R4 minor: drain.go:1855-1862 saveState's forgetMemos on write failure is load-bearing but unpinned
  - Committed the probe as TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered. After the blob line's Dispatch returns, .qompack/tmp is replaced by a file, so only the drain-state write fails. The fault is recorded with no require off the drain path, then removed. The third pass records [blob-8951-1.bin]. Green on HEAD, red under the saveforget mutant, 20/20 and race 3/3 green.
- **fixed**: R5 minor: blob body reads are counted nowhere; pendingBlobOf's no-read property is an equivalent mutant for every row
  - Added the readBlobBody seam, which dispatchPending uses, and a blobReads counter in meterDrainer.
- The blob row now carries a published blob line and a policy-denied blob line behind the waiting head. It asserts 1 body read in pass 1 and 0 in passes 2-3, and 0 for a restarted drainer that absorbs the published line and re-denies the other.
- The blocked-spools row asserts 0 reads in no-progress passes.
- TestDrainer_ReadsABlobBodyOnlyThroughItsSeam parses drain.go and fails on any direct readBlob call.
- Mutants: naming through readBlobBody turns the blob row red; calling the package readBlob in place turns the source pin red (run in place, then restored and checked with cmp).
- **partial**: R6 minor: the memo is capped at orderingProcessedCap; past it a line is consumed and announced in full every pass, and the memo is dropped whenever the file changes; docs overclaim
  - Confirmed.
Fixed:
- Announcements past the cap (spoolMemo.readTo): counted and announced once per memo life.
- File growth no longer drops the memo: same identity and no shorter is enough, and per-line sums still guard each skip; the sync skip still requires the same size and mtime. Rows TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain (red under samesize and unchanged_always) and TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain (red under noidentity).
Documented, not removed:
- Past 4096 out-of-order lines per file, a line is re-admitted, its lease looked up and the journal asked on every pass. Removing that would need a memo memory bound that is a new number; listed under needs_owner.
- The docs (withPassBudget, SpooledPromptSettled, spoolMemo, spool_watch.go, architecture.md) state the cap, the same-file-and-only-grown condition, and the once-more-after-restart rule.
- Pinned by TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce, which asserts that 3 lines past the cap are admitted on every pass.
- **fixed**: nit: drain.go:1248-1250 durableEnd's durable path returning synced=true is unpinned beyond two passes
  - The blocked-spools row now runs passes 2 and 3 with zero syncs and no rewrite each. Red under the dursync mutant (return size, false, nil).
- **fixed**: nit: drain.go:71-73 withPassBudget doc / architecture.md not exact about the overrun and what drainLineDeadline bounds
  - Reworded both docs:
- once spent with progress, the pass starts no new line;
- it overruns by the line in flight when the budget ran out or, when the budget ran out before any progress, by every line up to and including the one that made it, plus the bookkeeping that closes the file;
- drainLineDeadline bounds only the dispatch to the handler; admission, lease, journal checks and capture publication run under no pass deadline.
The spool_watch.go parenthetical was made exact the same way.
- **fixed**: nit: drain.go:1334-1345 rememberFile drops memo lines past an early stop; a line absorbed before its ack replays a stale unacknowledged gap
  - (1) rememberFile carries forward the previous memo's lines with start >= readPos (within the cap) and keeps the larger readTo. Row TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop: a spent pass publishes the line its predecessor's live publication released, then stops; the next pass re-admits 0 of the 3 remembered lines, where it re-admitted 3 at a03e6375. Red under nocarry.
(2) A line whose consumption left DrainGapUnacknowledged is not memoized (consumedLine.settled). Row TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands: Seen-completed without an ack, then acked; the next Drain reports no unacknowledged gap. Red under nosettled.
- **fixed**: nit: drain.go:1198-1204 + :604 cleanup rescans add I/O beyond the read in the blob-line shape
  - Same fix as R2: the post-file cleanup is gated on front movement, and the scan counter is in the 4-blocked-spool cost row with a blob line behind each head, 4 scans per no-progress pass (was 20). The pass-start scan remains and is documented.

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/daemon (three runs)`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/...`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 for all three
- `go test -p 1 -count=1 ./test/docs ./test/guards`: test/docs ok 5.7 s; test/guards ok 50.7 s
- `go test -p 1 -count=1 -timeout 60m ./internal/daemon`: ok 378.035s
- `red first: go test -p 1 -count=1 ./internal/daemon on a03e6375 plus the new seams, running the 10 new rows plus the changed blocked-spools and blob rows by exact name`: 6 FAIL for the stated reasons: TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames ('invalid blob reference source'), TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing (scans 20, expected 4), TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce (counter 6, expected 3), TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop (3 re-admissions), TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands (stale unacknowledged gap), TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain (3 re-admissions). Pins green, as expected.
- `mutation matrix: go test -p 1 -overlay <mutant drain.go> over the 16 memo, cost and look-ahead rows named in the summary, one mutant at a time (scratchpad w20r1/mutants.py)`: Red, with the rows that caught each mutant: scan_err, scan_none, scan_all (corrupt-line row); cleanup_always (blocked-spools); cleanup_never (release row); nosafe; saveforget; readbody_field (blob row); readto_none and readto_nomax (cap row); nocarry (early-stop row); nosettled (unack row); samesize and unchanged_always (growth row); dursync (blocked-spools); nosum (rewrite row); nomemo (6 rows); noidentity (replacement row). The package readBlob mutant, run in place, turned TestDrainer_ReadsABlobBodyOnlyThroughItsSeam red; the file was restored and checked with cmp.
- `go test -p 1 -count=20 -run '^TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames$' ./internal/daemon`: ok 14.940s
- `go test -p 1 -race -count=3 -run '^TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames$' ./internal/daemon`: ok 4.083s, no DATA RACE
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent$' ./internal/daemon`: ok 5.196s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent$' ./internal/daemon`: ok 2.119s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered$' ./internal/daemon`: ok 8.476s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered$' ./internal/daemon`: ok 2.878s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce$' ./internal/daemon`: ok 10.429s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce$' ./internal/daemon`: ok 3.572s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop$' ./internal/daemon`: ok 16.434s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop$' ./internal/daemon`: ok 4.873s
- `go test -p 1 -count=20 -run '^TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands$' ./internal/daemon`: ok 7.286s
- `go test -p 1 -race -count=3 -run '^TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands$' ./internal/daemon`: ok 2.478s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain$' ./internal/daemon`: ok 16.720s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain$' ./internal/daemon`: ok 4.395s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes$' ./internal/daemon`: ok 17.680s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes$' ./internal/daemon`: ok 4.018s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain$' ./internal/daemon`: ok 10.551s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain$' ./internal/daemon`: ok 2.997s
- `go test -p 1 -count=20 -run '^TestDrainer_ReadsABlobBodyOnlyThroughItsSeam$' ./internal/daemon`: ok 0.457s
- `go test -p 1 -race -count=3 -run '^TestDrainer_ReadsABlobBodyOnlyThroughItsSeam$' ./internal/daemon`: ok 1.471s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing$' ./internal/daemon`: ok 22.459s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing$' ./internal/daemon`: ok 4.724s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent$' ./internal/daemon`: ok 10.872s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent$' ./internal/daemon`: ok 2.704s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead$' ./internal/daemon`: ok 429.366s (its meter helper changed)
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead$' ./internal/daemon`: ok 69.050s, no DATA RACE
- `go test -p 1 -count=1 -run '^TestE2ESpoolSubmodeEndToEnd$' ./test/e2e`: ok 3.302s
- `go test -p 1 -count=1 -run '^TestE2E_ThinSliceDropsControlOnlyEdges$' ./test/e2e`: ok 9.931s
- `go test -p 1 -count=1 -run '^TestE2E_SpooledSessionStartNeverDegradesTheProject$' ./test/e2e`: ok 4.238s

### Criterion changes

- Round 1: TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing now puts a blob line behind each of the 4 heads and runs a third no-progress pass. Passes 2 and 3 each assert scans == 4 (one blob-reference read per spool file), blobReads == 0, syncs 0, no rewrite and 4 admissions; pass 1 asserts 4 blob reads. Reason: R2, R5 and nit 1. This is strictly stronger; the old assertions all remain.
- Round 1: TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent now also carries a policy-denied blob line and asserts body reads (1, then 0, 0, and 0 on restart). Its re-consumption step uses a restarted drainer instead of growing the file. Reason: a memo now survives growth, so growth no longer forces re-consumption; the restart does, with both intents already in state, which keeps the no-dedupe mutant red. The final drain.json-removal step is kept.
- Round 1: meterSyncs is renamed meterDrainer and also counts scanBlobRefs and readBlobBody calls; TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead uses it unchanged.
- Product, round 1: a spool line that does not decode no longer fails the blob-reference scan. It holds back only the pending blobs whose names it carries (raw or JSON-encoded).
- Product, round 1: the cleanup after a file runs only when the pass moved that file's front; the pass-start cleanup is unchanged.
- Product, round 1: a memo is used while its file has the same identity and is no shorter (it was: same size and mtime). The sync skip still requires the same size and mtime.
- Product, round 1: a pass that stops early keeps the previous memo's lines past where it stopped reading, within the cap.
- Product, round 1: a line consumed with DrainGapUnacknowledged is not memoized.
- Product, round 1: drain_file_error and the corrupt-line Warn, and drain_unadmitted and the not-admitted LOUD, fire only for a line starting at or past spoolMemo.readTo: once per memo life, including lines past orderingProcessedCap.
- Product, round 1: the drain reads blob bodies only through drainer.readBlobBody (default readBlob) and scans references through drainer.scanBlobRefs (default scanPendingBlobs). These are test seams with no behaviour change.
- Docs, round 1: withPassBudget, notePassConsumed, DrainConfig.SpooledPromptSettled, spoolMemo, the spool_watch.go header and docs/architecture.md now state the exact overrun, that drainLineDeadline bounds only the dispatch, the memo's cap and its same-file-and-only-grown condition, re-consumption after a restart, and the pass-start reference scan while an intent waits.
- Round 0, unchanged: four rows moved from slowSpoolSyncs to slowWaitingHead with held == number of passes; the stall row is keyed on client-6161.ndjson's own sync and asserts that the stall fired; spoolWatchTraffic uses t.Errorf plus return. Round 0 product changes stand: the memo, session-scoped and end-of-file re-attempt, no rewrite of identical progress, no re-sync of an unchanged synced file, de-duplicated intents named without a body read.

### Open issues

- Wall times on the shared host are noisy and co-loaded; carried from round 0. A quiet-host A/B of c7 against this branch belongs in the night chain. The deterministic operation counts are the firm evidence.
- Re-admitting each waiting line dominates a no-progress pass (scopeRefusal is 4.07 s of a 5.45 s profiled pass); c7 pays the same per line. Carried from round 0.
- Past orderingProcessedCap (4096) out-of-order lines in one file, a no-progress pass still admits, looks up and journal-checks each such line on every pass. This is documented and pinned; announcements past the cap are fixed.
- While any cleanup intent waits, every pass reads and decodes each spool file's unconsumed lines once, at its start (cleanupAcknowledged). Skipping that scan would need a stat-keyed cache of references, and that cache would authorize blob deletions on a stat, so it is left as is and documented.
- Pre-existing and outside this round's findings: scanPendingBlobs still refuses a trailing partial line, for example a hook killed mid-append. While any intent waits, such a client spool fails every pass's cleanup until the line is completed or the file removed. Treating it like an undecodable line, matching names by containment, is possible, but the safety argument differs because the line may still be growing.
- A line rewritten in place before spoolMemo.readTo is consumed afresh, guarded by its sum, but its corrupt or unadmitted announcement is suppressed. No writer rewrites a spool in place.
- The memo is in memory only: a restarted daemon consumes every remembered line in full once, including its count and announcement.
- The end-of-file re-attempt costs 3 journal queries per waiting line per pass; carried from round 0.
- Reattempt's top-of-round passStopped check alone cannot be pinned (an equivalent mutant, recorded in 07568cd4); carried from round 0.

### Needs owner

- New test-only number, carried from round 0: journalQueriesPerLine = 7 in internal/daemon/drain_pass_cost_test.go. It is 1 lease lookup, plus 3 frontier queries as a line is read, plus 3 more at the end-of-file re-attempt for a line still waiting. Base makes 546,004 queries against a bound of 6,307; the fix makes exactly 5,107 and 2,107. No new number was added in round 1: pastCap = 3 is a fixture count, and the docs cite the existing orderingProcessedCap (4096).
- Ledger: the D31 row in plans/V6-CLOSEOUT-CHECKLIST.md (read-only for this seat) still says 'no new line after the budget is spent'. Suggested amendment, or a pointer to D60(a): a spent budget ends a pass only once it has made progress; a no-progress pass is bounded by the spool, not the clock.
- Ratify a semantics change, extended in round 1: a line consumed out of order behind a waiting head is final for the daemon's lifetime, and stays final while its file grows (same identity, no shorter, each skip checked by the line's own FNV-1a sum). A later policy change no longer re-decides it on later passes. Corrupt and unadmitted lines are counted and announced once per memo life.
- Ratify the durability shortcut, carried from round 0: a spool file unchanged by identity, size and mtime since this drainer's own successful sync is not synced again, and drain.json is not rewritten with identical bytes. Now pinned by TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain: a replaced file with the same size and mtime is synced.
- Decide whether to lift the memo's per-file cap (orderingProcessedCap). Doing so needs a memory bound that is a new, derived number. Today a line past the cap is re-admitted on every pass, though announced once.
- CI time: the D=300/L=600 cost row costs about 21 s per run (429 s for -count=20) because its first pass makes over 1,800 lease, capture and ack fsyncs. Using policy-denied lines for the 600 consumed lines would cut that to about 2 s while keeping every asserted count. I kept the published shape the task asked for.
- Lock.owned() (internal/daemon/lock.go, outside this seat) reads the lock file on every journal query: 0.65 s of a 5.45 s profiled pass. The auditor's optional per-pass memo is left for the owner.

## review:drain:1:r2: verdict `sound`, 1 finding(s)

- **nit** `Seat result summary, 'Round-0 measurements' and findings_resolution[0].detail (the 'D=300/L=600 spent pass ... 47.5 s → 10.9 s (c7: 302 admissions, 52.3 s ...)' figures). No code change.`: The task asked for indicative wall times. The ones reported were taken under different co-load, so they cannot be compared, and they get the HEAD-versus-c7 relation backwards: they suggest HEAD's spent pass is about 5x faster than c7's. On a quiet host, run back to back, HEAD's no-progress pass costs about 1.3-1.5x c7's spent pass, which is inside the task's 'small constant of c7' target. The operation counts in the report are exact and agree with mine. Only the times would mislead the owner, who has to ratify the semantics change from these numbers. This is non-blocking: the product code and the cost rows are sound.
  - Evidence: Setup: same Windows host, this session, -p 1, one go test at a time. Probe: scratchpad/w20drain-r2rev/zz_rv_probe_test.go via -overlay; logs head-passcost.log, cand-passcost.log, int-passcost.log.

D=300/L=600, real dispatch:
- HEAD 6dcdb6b7: spent pass 1.85 s; 2 s-budget pass 1.83 s. Each: 301 admissions, 2107 journal queries, 0 syncs, drain.json not rewritten.
- c7 d20309c0: spent pass 1.21 s (round 1: 1.45 s); 2 s-budget pass 2.03 s. Spent pass: 302 admissions, 1207 queries, 1 sync, rewritten. It ended on errPassBudgetSpent.
- base 738d67c7: spent pass 56.3 s (round 1: 105.9 s); 2 s-budget pass 41.3 s (round 1: 87.3 s). Each: 901 admissions, 544804 queries, 1 sync, rewritten.
- First, unbudgeted pass: queries HEAD 5107 vs 546004 on both base and c7; time 1m23 / 3m16 / 1m28 (HEAD / base / c7).

HEAD's extra cost over c7 is the read of the 600 remembered lines plus the 900 end-of-file re-attempt queries (3 for each of the 300 waiting lines).

The other audit shapes:
- 4 blocked spools, syncs held: HEAD 20 ms and 5.5 ms per pass, 4 admissions, 28 queries, 0 syncs, no rewrite. Base 8.2 s per pass, 8 admissions, 40 queries, 4 syncs. c7 2.05 s, stopped in the first spool.
- N=2000 acknowledged lines: HEAD 6-25 ms per later pass, 1 admission, 7 queries. Base 5-14 s per pass, 2001 admissions, 12004 queries; one base pass took 48m30s, a host stall, so wall-time noise. c7 15-29 ms when stopped at the first line, and 2.01 s with 1406 admissions under the 2 s budget.
- 10k corrupt lines: drain_file_error grows by 10000, 0, 0 per pass on HEAD; 10000 every pass on base; 1 every pass on c7.

HEAD-only probes (zz_rv2_head_test.go):
- R1 at audit scale (10k corrupt lines plus a blob intent waiting in a second spool): file errors 10000, 0, 0, 0; every pass returns nil; 0 'file error' Warns; 2 scans per pass.
- F=40 blocked blob spools: 40 scans per pass, not O(F^2); 89-93 ms per pass.
- Growth, one line appended per pass: 103 admissions (101 waiting lines plus the new line, which is admitted twice: once at read, once at dispatch) and 1 sync per pass, so the memo survives growth.
- Past the cap (6000 denied lines): 1905 admissions per pass, 211-225 ms, as documented.

Cost rows red on 738d67c7: tested with seams only. drain_base_seams.go adds the scanBlobRefs and readBlobBody fields and routes base's existing scanPendingBlobs and readBlob calls through them, with no behaviour change. The three rows fail for their stated reasons:
- "546004" is not <= "6307";
- pass 2 syncs: 'Should be zero, but was 4';
- pass 1 blob reads: expected 1, actual 2.
The rows do not compile on raw 738d67c7.

HEAD: the 27 new or changed drain and spool-watch rows pass, ok 58.5 s. All three trees are left clean.
  - Fix: In the final report, replace the round-0 wall times with same-session A/B figures, for example the ones above: D=300/L=600 spent pass HEAD 1.85 s vs c7 1.21-1.45 s vs base 56-106 s. State that a no-progress pass on HEAD costs c7's re-admission of the waiting lines, plus a cheap read of each remembered line, plus 3 end-of-file journal queries per waiting line. Keep the deterministic counts as the primary evidence.

## review:drain:0:r2: verdict `needs-fixes`, 3 finding(s)

- **minor** `internal/daemon/drain.go:1056 (Failed branch: `if lineStart >= announced`), with rememberFile's watermark at :1415 (`readTo := readPos`) and :1422, and the paths that read a line without consuming it: :1025-1030 (the `refused replay has unresolved delivery identity or policy` continue), :1073-1080 (lease failure, ErrDegraded break), and processOne hard-error breaks on an unleased line`: spoolMemo.readTo assumes every line before it that ends up consumed as unadmitted was already counted and announced by an earlier pass. That is false. readPos also advances over lines a pass reads but does not consume and never announces: the unresolved-identity continue when existingLease errs, the line a lease failure breaks on, and an unleased (nonce-less) line whose dispatch hard-errors. If a later pass consumes such a line through the Failed branch (never leased, refused), its lineStart is below `announced`. The record is skipped with no drain_unadmitted count and no LOUD 'capture not admitted; record skipped', and the memo then holds it as consumed, so it is never announced. A capture is dropped silently, against the Failed branch's own rule ('losing it LOUDLY is correct'). The seat's claim that such lines are 'counted and announced once per memo life' is wrong for this case: here they are announced zero times. This is a regression: base 738d67c7 announces it.
  - Evidence: Probe scratchpad/w20r2/zz_r2_probe_test.go, run with -overlay (overlay-probe.json for HEAD 6dcdb6b7, overlay-base.json for base product and test files), -p 1. Spool layout: [blockedSpoolHead, X], with X never leased and Admit returning Failed.
(A) The existingLease lookup after X's first refusal fails once with a transient journal error. HEAD, passes 1-3: unadmitted=0 loud=0, admissions=1,2,2 (X consumed in pass 2 and memoized, never announced). Base: unadmitted=0,1,2 and loud=0,1,2.
(A2) X is admitted in pass 1, but its lease fails (ErrDegraded break, 'spool retained for recovery'), then it is refused in pass 2. HEAD: unadmitted=0 loud=0 after 3 passes. Base: 1 and 2.
The corrupt-line half of readTo is sound: a corrupt line is announced the moment it is read, and a partial or I/O-error line does not advance readPos.
  - Fix: Let readTo cover only lines whose consumption was announced. Option 1: in drainFile, record the start of each line the pass read but neither consumed nor left under a held lease (the :1029 continue with err != nil, and the line a readErr break stopped on while !leased). Keep those starts in spoolMemo (bounded by orderingProcessedCap), and count and announce a Failed or corrupt consumption when `lineStart >= readTo || unannounced[lineStart]`. Option 2: stop readTo at the first such line, readTo = min(readPos, firstUnannounced), so that max with prev.readTo cannot pass it. Commit probes A and A2 as rows asserting drain_unadmitted == 1 and one LOUD. Show them red on HEAD and green on the fix, and keep TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce green.
- **nit** `internal/daemon/drain.go:975-987 (memo skip) and :1050-1053 (Failed-branch comment 'The decision is baked into the spooled bytes'); seat needs_owner item 'Ratify a semantics change'`: The ratification item says only that 'a later policy change no longer re-decides' a memoized line. The memo also makes final a never-leased line refused for a process condition rather than a decision about its bytes, for example admitDelivery's Failed/CaptureErrorPolicy while capturePolicies cannot compile the configured policy. A Denied verdict from paths.ResolvesInside's on-disk walk can be transient the same way. Base re-admitted such an out-of-order line on every pass and published it once the condition cleared. HEAD skips it for the daemon's lifetime, and if the head later publishes, the front rolls over it and it is lost for good. The Failed-branch comment that the decision is baked into the bytes is not true for CaptureErrorPolicy.
  - Evidence: Probe B (same file): Admit returns Failed('policy') for X's first admission only, with X behind a blocked head. HEAD, passes 1-3: admissions=1,1,1, published=false. Base: admissions=1,3,4, published=true from pass 2.
  - Fix: Choose one. (a) In consumedLine.settled(), treat a DrainGapUnadmitted line whose reason is CaptureErrorPolicy (a process condition, like DrainGapUnacknowledged) as unsettled, so it is re-admitted each pass. Re-announcement is already suppressed by readTo once the fix above lands. (b) Leave the code, but name this case explicitly in the owner-ratification item and correct the Failed-branch comment.
- **nit** `internal/daemon/drain.go:921 (reattempt's gaps.startLine), :1685 (encodedBlobName match in scanPendingBlobs), :1138 (`!canceled && readErr == nil` guard), :1387 (memoOf shrink check), :612-616 (unlisted-memo deletion)`: The seat's mutation matrix says every new branch is pinned. Five new branches survive the 19 memo, cost and look-ahead rows. Two are worth pinning or commenting:
(1) Dropping reattempt's startLine: a memo hit replays its gaps into gapRecorder.line without takeLine. Without startLine, an out-of-order line that the end-of-file re-attempt consumes would memoize the previous line's gaps and report them as an extra DrainGap on every later pass. This effect is from reading the code; I did not probe it.
(2) The JSON-escaped name match for corrupt lines. safeBlobName allows <, > and &, which encoding/json escapes.
The other three are equivalent in practice and need no fix:
- the EOF guard: reattempt's own passStopped makes it redundant;
- the shrink check: validateProgress refuses any shrink below the recorded Size before memoOf can see one;
- the unlisted deletion: os.SameFile backstops it, except under POSIX inode reuse.
The memo is dropped on replacement (pinned by noidentity), on an external drain.json change (my noown mutant is red in TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent), on restart (new drainer), and on removal.
  - Evidence: Mutants under scratchpad/w20r2/mut/*.go, run with go test -overlay -p 1 against the rows in rows.txt.
- noreatstart, noenc, eofalways, noshrink, nounlisted: all ok, i.e. they survive.
- noown: TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent FAIL.
- noeof: TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd FAIL.
  - Fix: Add a row for (1): a memoized corrupt line last in the file, after an out-of-order line released at the end-of-file re-attempt, asserting GapState is identical on passes 2-4. Add a corrupt-line subtest for (2) naming a blob that contains '&'. Or add a short comment marking each as deliberately unpinned defence, and record the three equivalent mutants in the commit message, as 07568cd4 did for MX's top-of-round check.

## fix:drain:r2: status `done`, head `3f705053fd1dfb17e4d0705baaa912df6a28fe3e`

### Root cause

C1.13 counts only progress against a pass budget, so a budgeted pass that makes no progress reads every spool to its end. Behind a head waiting on an earlier arrival, three costs repeated on every pass:
- every line consumed out of order was consumed again in full, because `processed` lived only for one drainFile call;
- reattempt() re-ran every deferred line after every consumed line, whatever its session (O(L x D));
- every blocked spool paid an fsync and a drain.json rewrite.

Round 1 found the same class one layer down:
- O(F^2) cleanup re-scans;
- a corrupt line failed the reference scan;
- the memo was dropped on append;
- the memo forgot lines past an early stop;
- the memo replayed a gap that could close;
- past the memo cap, lines were re-announced on every pass.

Round 2 found the announcement watermark too optimistic. spoolMemo.readTo advanced over every line a pass read, including lines it left unconsumed under no lease: a failed lease lookup, a failed lease, an unleased line that was deferred or hard-errored. A later pass that refused such a line consumed it as unadmitted below readTo, so the capture was dropped with no count and no LOUD line, and the memo then kept it consumed.

### Summary

PRODUCT CODE CHANGED (internal/daemon/drain.go, internal/daemon/delivery_order.go; doc-only changes in internal/daemon/spool_watch.go and docs/architecture.md).

Branch closeout/w20-drain, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-drain, 8 commits on 738d67c7:
- rounds 0-1: the six commits listed before f6d509f5;
- this round: f6d509f5 (fix) and 3f705053 (test).
The tree is clean and nothing is pushed.

WHAT THE BRANCH DOES (rounds 0-1, carried forward)
- A per-drainer in-memory memo (spoolMemo) for each spool file with bytes past its consumed front. A line consumed out of order behind a waiting head costs later passes only its read and an FNV-1a sum check. This holds for up to orderingProcessedCap (4096) lines per file, while the file keeps its identity and has only grown.
- Re-attempts are scoped to the session, plus one at end of file.
- An unchanged, already-synced spool is not synced again, and identical progress is not rewritten.
- Cleanup intents are de-duplicated and named without reading the blob body.
- Corrupt and unadmitted lines are announced once per memo life (spoolMemo.readTo).
- The post-file cleanup runs only when the front moved.
- A corrupt line holds back only the blobs it names.

MEASURED BEFORE/AFTER (D=300/L=600 spent pass; operation counts are the firm evidence)
- base 738d67c7: 901 admissions, 544,804 journal queries, 1 sync, drain.json rewritten.
- c7 d20309c0: 302 admissions, 1,207 queries, 1 sync, rewritten. It ends on errPassBudgetSpent without reaching later spools.
- final tree: 301 admissions, 2,107 queries, 0 syncs, not rewritten. It reads to the end of the spool.
- Where the final tree costs more than c7: a read and sum of each of the 600 remembered lines, plus 900 end-of-file journal queries (3 for each of the 300 waiting lines). Re-admitting the waiting lines costs the same on both.

Wall time (indicative only; the host was co-loaded and same-session pairs are the only comparable figures):
- reviewer's pair at 6dcdb6b7: 1.85 s, vs c7 1.21 s and 1.45 s, vs base 56.3 s and 105.9 s;
- my pair this round, run back to back: final tree 1.05 s vs c7 1.40 s;
- a lone final-tree run: 0.63 s.
So the final tree's no-progress pass is roughly 0.75x to 1.5x c7's spent pass, which meets the task's "small constant of c7" target. The round-0 figures (47.5 s → 10.9 s, c7 52.3 s) came from runs under different co-load and are withdrawn.

Other shapes:
- 4 blocked spools: base 8.2 s per pass with 4 syncs; final tree about 5-20 ms with 0 syncs.
- 10,000 corrupt lines: drain_file_error grows 10,000, then 0, 0.

REVIEW RESOLUTION (round 2)
Minor (readTo silently drops a skipped capture): confirmed and fixed.
- Reproduction on 6dcdb6b7 with the reviewer's probes:
  - A: unadmitted=0, loud=0 after 3 passes;
  - A2: unadmitted=0, loud=0 after 3 passes;
  - base announced both.
- Cause: readPos also advances over lines a pass reads and leaves unconsumed under no lease, and nothing announces those lines.
- Fix: drainFile records firstLeft, the start of the first such line. rememberFile caps readTo there, even below an earlier pass's readTo: readTo = min(max(readPos, prev.readTo), firstLeft). The four leave points are:
  - the unresolved-identity continue when existingLease found no held lease;
  - the lease-failure break;
  - an unleased line that processOne defers;
  - an unleased line that processOne hard-errors on.
- A refused line whose lease the journal holds does not stop readTo. No pass can consume it as unadmitted (held && Failed stays where it is), and a lease once written stays held: leaseHeld resolves it from the journal or its archive.
- The cap is conservative: past the cap, a line behind a left line can be announced again, but never silently skipped.
- New row TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt has four subtests, one per leave point. Each asserts its pass-1 fixture shape (error or not, admissions, drain_leased_deny_pending), then drain_unadmitted == 1 and exactly one LOUD line after 3 passes. All four are red on 6dcdb6b7 (counted 0) and green on the fix.
- TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce now also carries a held-and-refused line ahead of the cap lines, which pins the held-lease exemption.

Nit B (process-condition refusals made final): decision (b), with the reason recorded.
- Probe B reproduces: admissions 1,1,1, never published.
- I kept the semantics and corrected the Failed-branch comment, which base 738d67c7 also had. It no longer claims the spooled bytes always decide the refusal; a policy the daemon cannot compile, or a scope it cannot prove, is a condition of the process. The case is now named explicitly in the owner-ratification item.
- Reasons for (b) over (a):
  - the same verdict on an in-order line is lost for good in base and HEAD alike, so base's retry happened only to lines that sat behind an unrelated waiting head;
  - on base, the LOUD 'record skipped' was contradicted when a later pass published the record; HEAD's announcement is true;
  - the reason label cannot separate a daemon-side CaptureErrorPolicy (a process condition) from a hook-side one baked into the capture;
  - (a) would recompile the policy for every such line on every pass, because capturePolicies does not cache a failure.
- Probe B still fails on the final tree, by design.

Nit (five unpinned branches): two pinned, three recorded as deliberate defence.
- reattempt's gaps.startLine is pinned by TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps. The last line read is a refused line whose lease is held, which leaves its gap uncollected; the end-of-file re-attempt then releases p1. Red under noreatstart: 2 gaps where 1 belongs.
- encodedBlobName is pinned by a third case in TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames. The blob's name contains '&', and the corrupt line carries the name as json.Marshal writes it, with a fixture check that the escaped form lacks the plain name. Red under noenc.
- An earlier version of that case was not red under noenc. The Edit tool had decoded the \u0026 I typed back into '&'; the case now builds the escaped form with json.Marshal.
- The end-of-file guard, memoOf's shrink check and the unlisted-memo deletion are recorded as equivalent in practice in 3f705053's body.

Nit (wall times): fixed above with same-session pairs.

ROUND-2 MUTATION MATRIX
10 mutants, run with go test -overlay against the four rows; all red:
- noreatstart and noenc;
- noleave_unresolved, noleave_lease and noleave_processone;
- leave_processone_erronly and leave_processone_deferonly (each turns its own subtest red);
- leave_held_unresolved and leave_processone_leased (both turn the cap row red: 6 announcements, expected 3);
- nomin (all four subtests red).
Script and logs: C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w20r3/ (mutants.py, mut/*.log, red-head.log, full-daemon.log, c7-passcost.log, head-passcost2.log, probe-final.log).

CHECKS, all green
- go vet on windows, linux and darwin; golangci-lint; fmt-check; docmarkers and runpatterns; gen-config-docs, gen-command-docs and gen-mcp-docs --check.
- test/docs ok 4.2 s; test/guards ok 57.8 s.
- internal/daemon in full: ok, 321.1 s.
- The four new or changed rows: -count=20 and -race -count=3, all ok.
- Commit f6d509f5 was checked on its own (its rows and vet pass) for bisectability.

### Commits

- df53a14c fix(daemon): bound a drain pass that makes no progress
- 07568cd4 test(daemon): pin the look-ahead budget checks and progress rule
- 96f219ca test(daemon): check the backoff stall and its traffic goroutine
- a03e6375 docs(daemon): say what bounds a budgeted drain pass
- 1e74e637 fix(daemon): bound blob cleanup and keep spool memos exact
- 6dcdb6b7 docs(daemon): state what a no-progress drain pass costs
- f6d509f5 fix(daemon): announce a skipped line no earlier pass announced
- 3f705053 test(daemon): pin the end-of-file gaps and escaped blob names

### Findings resolution

- **fixed**: perf/major: drain.go:723-736,:958,:1032: a budgeted pass that only re-consumes lines is unbounded, O(L x D) reattempt plus three journal queries per processOne
  - Mechanism, rounds 0-1:
- a per-drainer memo makes a re-consumed line cost its read and an FNV-1a sum check, and survives the file growing;
- re-attempts are scoped to the session, plus one at end of file;
- an unchanged synced spool is not synced again, and identical progress is not rewritten.
Counts for the D=300/L=600 spent pass:
- base: 901 admissions, 544,804 journal queries, 1 sync, rewritten;
- c7: 302 admissions, 1,207 queries, 1 sync, rewritten; it stops on its budget before later spools;
- final tree: 301 admissions, 2,107 queries, 0 syncs, not rewritten; it reaches the end of the spool. Re-confirmed this round.
Wall time (same-session pairs, indicative):
- reviewer: 1.85 s vs c7 1.21/1.45 s vs base 56.3/105.9 s;
- mine, back to back: 1.05 s vs c7 1.40 s.
That is roughly 0.75x to 1.5x c7. The round-0 wall times are withdrawn.
The auditor's wall-clock ceiling is rejected: it would bring back the starvation that D31, D58(c) and D60(a) removed.
Pinned by TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead and TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain.
- **fixed**: drain/minor: drain.go:58-74,720-747,923-982: a no-progress spent pass pays one fsync and one drain.json save per blocked spool and ~3 ms per re-consumed line
  - Per pass: 4 syncs become 0, 8 admissions become 4, and drain.json is no longer rewritten.
TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing asserts, on passes 2 and 3: 0 syncs, no rewrite, 4 admissions, 4 scans, 0 blob reads.
- **fixed**: drain/minor: drain.go:843-850: reattempt's two passStopped checks are unpinned (mutation MX)
  - TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress is red under MX and under removing the per-line check alone.
The top-of-round check alone is an equivalent mutant, recorded in 07568cd4.
- **fixed**: drain/minor: drain.go:763-786 + cleanupAcknowledged: PendingBlobs gains a duplicate per pass behind a blocked head
  - notePendingBlob de-duplicates intents.
TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent: the intents stay [blob, deniedBlob] across 3 passes, a restart and the drain.json-removal step. Red under no-blob-dedupe.
- **fixed**: perf/minor: drain.go:769-772 (and :764,:785,:949): readBlob reads the whole blob only to learn its name; drain.json grows
  - pendingBlobOf names the blob from its descriptor plus an Lstat.
readBlobBody is the drain's only blob-body reader, and the meter counts its calls:
- 1 read in pass 1 (the publication);
- 0 for the denied line, for memo-skipped passes and for a restarted absorb.
TestDrainer_ReadsABlobBodyOnlyThroughItsSeam fails on any direct readBlob call in drain.go.
- **fixed**: ci/minor: spool_watch_test.go:247-250: the stall row never asserts that its stall fired
  - The stall is keyed on client-6161.ndjson's own sync, and the row asserts that the stall fired exactly when firstPassStall > 0.
- **fixed**: complete/minor: w17c items without disposition: (1) stall assertion, (2) 'fsync pressure the back-off exists to spare' doc overstates, (3) a horizon below 4 s means no retry
  - Fixed in round 0. Round 1 restated the spool_watch.go header's cost sentence precisely. No new number.
- **partial**: drain/minor: docs/architecture.md:177-179 and the D31 ledger row claim 'no new line after the budget is spent'
  - docs/architecture.md is fixed. The D31 row in plans/V6-CLOSEOUT-CHECKLIST.md is read-only for this seat and is listed under needs_owner.
- **fixed**: perf/minor: docs/architecture.md:176-179 claims a bounded drain pass
  - The paragraph states exactly:
- what finishes and what bounds it: after progress, no new line starts; the line in flight finishes, with only its dispatch under drainLineDeadline;
- the memo's cap and its same-file-and-only-grown condition;
- re-admission past the cap;
- re-consumption after a restart;
- the start-of-pass reference read while a cleanup waits.
Round 2 adds the one exception to announce-once past the cap: a line behind one a pass left unleased and unconsumed is announced again.
test/docs is green.
- **fixed**: nit: drain.go:860: S3's `if changed` in reattempt is unpinned (mutation M8b)
  - TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass, red under M8b.
- **fixed**: nit: drain.go:883-1037: no reattempt after the read loop ends
  - A one-time end-of-file re-attempt, pinned by TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd.
- **fixed**: nit: spool_watch_test.go:56: require.True on the traffic goroutine
  - Replaced with t.Errorf plus return.
- **fixed**: nit: drain.go:908-916,963-974: a corrupt or never-leased unadmitted line behind a blocked head is counted and logged Warn/LOUD on every pass
  - spoolMemo.readTo makes each such line counted and announced once per memo life, past the cap too. Pinned by TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce.
Round 2 closes the opposite failure, a line announced zero times: readTo now stops at the first line a pass left unconsumed under no lease (see the round-2 entry).
- **deferred**: nit: CI time of the sleep-bound rows (about 36 s per daemon pass)
  - The auditor says no action is needed for the freeze. The D=300/L=600 cost row takes about 21 s per run. Listed under needs_owner.
- **fixed**: R1 minor: drain.go:1586 scanPendingBlobs fails on any undecodable line; a corrupt line behind a waiting head plus any pending intent makes every pass error and count a file error per finished file
  - A line that does not decode is skipped. It still holds back each pending blob whose name it carries, raw or JSON-encoded.
TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames now has three cases; round 2 added the escaped-name case, red under noenc.
- **fixed**: R2 minor: cleanupAcknowledged/pendingBlobReferences re-scan every spool file at pass start and after each file while any intent is pending (O(F^2)); 'costs only its read' overclaims
  - The post-file cleanup is gated on the front moving. Scans per no-progress pass over 4 blocked spools: 20 → 4.
Rows: TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing (red under cleanup-always) and TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes (red under cleanup-never).
- **fixed**: R3 minor: drain.go:1383 pendingBlobOf's safeBlobName check is unpinned; dropping it lets a hostile descriptor wedge every pass via an unsafe intent
  - TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent, red under the nosafe mutant.
- **fixed**: R4 minor: drain.go:1855-1862 saveState's forgetMemos on write failure is load-bearing but unpinned
  - TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered, red under the saveforget mutant.
- **fixed**: R5 minor: blob body reads are counted nowhere; pendingBlobOf's no-read property is an equivalent mutant for every row
  - Added the readBlobBody seam and a blobReads meter, plus the source pin TestDrainer_ReadsABlobBodyOnlyThroughItsSeam. Both mutants are red.
- **partial**: R6 minor: the memo is capped at orderingProcessedCap; past it a line is consumed and announced in full every pass, and the memo is dropped whenever the file changes; docs overclaim
  - Fixed:
- announcements past the cap (readTo);
- the memo now survives file growth.
Documented and pinned, not removed: past 4,096 lines per file, a line is re-admitted and journal-checked on every pass. Lifting the cap needs a new memory-bound number; listed under needs_owner.
- **fixed**: nit: drain.go:1248-1250 durableEnd's durable path returning synced=true is unpinned beyond two passes
  - The blocked-spools row runs a third pass with zero syncs. Red under the dursync mutant.
- **fixed**: nit: drain.go:71-73 withPassBudget doc / architecture.md not exact about the overrun and what drainLineDeadline bounds
  - The doc states:
- the exact overrun;
- that drainLineDeadline bounds only the dispatch.
Round 2 adds the past-cap announcement exception.
- **fixed**: nit: drain.go:1334-1345 rememberFile drops memo lines past an early stop; a line absorbed before its ack replays a stale unacknowledged gap
  - Carry-forward is pinned by TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop (red under nocarry).
The settled filter is pinned by TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands (red under nosettled).
- **fixed**: nit: drain.go:1198-1204 + :604 cleanup rescans add I/O beyond the read in the blob-line shape
  - Same fix as R2. The pass-start scan remains and is documented.
- **fixed**: Round 2 minor: drain.go:1056 + rememberFile readTo: a line read but left unconsumed under no lease (unresolved identity, lease failure, unleased deferral or hard error) is later consumed as unadmitted below readTo with no count and no LOUD line, then memoized: a capture dropped silently (regression vs 738d67c7)
  - Reproduced on 6dcdb6b7 with the reviewer's probes A and A2: unadmitted=0, loud=0 over 3 passes.
Fix (f6d509f5): drainFile records firstLeft, the first line it read and left unconsumed with no held lease. The four leave sites are:
- the unresolved branch when !held;
- the lease-failure break;
- processOne returning !done for an unleased line, whether deferred or errored.
rememberFile then caps readTo: min(max(readPos, prev.readTo), firstLeft).
Row TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt has four subtests:
- each asserts its pass-1 fixture shape, then drain_unadmitted == 1 and one LOUD line;
- all four are red on 6dcdb6b7 (counted 0);
- -count=20 ok 14.5 s; race 3/3 ok.
The held-lease exemption is pinned by a held-and-refused line added to TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce (red under leave_held_unresolved and leave_processone_leased: 6 announcements, expected 3).
Every leave site and the min are red under their own mutants (8 mutants).
On the final tree, probes A and A2 report unadmitted=1, loud=1.
- **partial**: Round 2 nit B: drain.go memo skip + Failed-branch comment: a never-leased line refused for a process condition (daemon-side CaptureErrorPolicy, unprovable scope) is made final by the memo; base re-admitted it out of order and published it once the condition cleared; the 'baked into the spooled bytes' comment is untrue for CaptureErrorPolicy
  - Reviewer's option (b) taken, with reasons.
Done:
- The comment is corrected. Base 738d67c7 carried the same claim.
- The ratification item now names this case explicitly.
Why the code is unchanged:
- an in-order line with the same verdict is lost for good on base and HEAD, so base's retry was positional, reaching only lines that sat behind an unrelated waiting head;
- base's LOUD 'record skipped' was contradicted when a later pass published the record; HEAD's announcement is true;
- the reason label cannot separate a daemon-side CaptureErrorPolicy from a hook-side one baked into the capture;
- re-admitting such a line on every pass would recompile the policy each time, because capturePolicies does not cache a failure.
Probe B still fails on the final tree (admissions 1,1,1), by design. Listed under needs_owner.
- **fixed**: Round 2 nit: five new branches survive the rows (reattempt startLine, encodedBlobName match, EOF guard, memoOf shrink check, unlisted-memo deletion)
  - Pinned:
- startLine: TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps. The last line read is a refused line whose lease is held (its gap is left uncollected), and the end-of-file re-attempt releases p1. Passes 2-3, via Drain, report that file's unadmitted gap count as 1. Red under noreatstart (2).
- encodedBlobName: the escaped-name case in TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames. The blob name contains '&' and the corrupt line carries json.Marshal's form. Red under noenc.
Recorded as deliberate, equivalent-in-practice defence in 3f705053's body:
- the EOF guard: reattempt checks passStopped itself;
- the shrink check: validateProgress refuses a shrink first;
- the unlisted deletion: os.SameFile covers it except under POSIX inode reuse, which no row can arrange.
- **fixed**: Round 2 nit: the reported round-0 wall times were taken under different co-load and invert the HEAD-vs-c7 relation
  - The round-0 times are withdrawn and replaced with same-session pairs.
- Reviewer's pair:
  - 6dcdb6b7: 1.85 s;
  - c7: 1.21 s and 1.45 s;
  - base: 56.3 s and 105.9 s.
- My back-to-back pair on the final tree, using the reviewer's probe through -overlay, with c7 rebuilt in this worktree through a c7 overlay:
  - final tree: 1.05 s;
  - c7: 1.40 s.
- A lone final-tree run took 0.63 s.
What a no-progress pass costs on the final tree: c7's re-admission of the waiting lines, plus a read and FNV sum of each remembered line, plus 3 end-of-file journal queries for each waiting line. The counts are exact and primary.

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/daemon (three runs)`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 for all three
- `go test -p 1 -count=1 ./test/docs ./test/guards`: test/docs ok 4.241s; test/guards ok 57.816s
- `go test -p 1 -count=1 -timeout 60m ./internal/daemon`: ok 321.082s (final tree)
- `red first: go test -p 1 -count=1 -overlay <6dcdb6b7 drain.go> -run '^TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt$' -v ./internal/daemon`: FAIL on all 4 subtests: 'the skipped record is counted once', expected 1, actual 0. Every pass-1 fixture assertion held, so each case reached its intended branch.
- `go test -p 1 -count=1 -overlay <6dcdb6b7 drain.go> -run '^TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps$' ./internal/daemon (plus the same for TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames)`: PASS, as expected for pins: they are red only under their named mutants
- `mutation matrix: scratchpad w20r3/mutants.py, go test -p 1 -overlay <mutant drain.go> over the 4 round-2 rows, one mutant at a time`: All 10 red: noreatstart, noenc, noleave_unresolved, leave_held_unresolved, noleave_lease, noleave_processone, leave_processone_erronly, leave_processone_deferonly, leave_processone_leased, nomin
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt$' ./internal/daemon`: ok 14.517s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt$' ./internal/daemon`: ok 4.592s, no DATA RACE
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps$' ./internal/daemon`: ok 9.298s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps$' ./internal/daemon`: ok 2.407s
- `go test -p 1 -count=20 -run '^TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames$' ./internal/daemon`: ok 19.049s
- `go test -p 1 -race -count=3 -run '^TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames$' ./internal/daemon`: ok 4.401s
- `go test -p 1 -count=20 -run '^TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce$' ./internal/daemon`: ok 7.845s
- `go test -p 1 -race -count=3 -run '^TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce$' ./internal/daemon`: ok 2.588s
- `bisectability: go vet plus go test -overlay <commit-1 drain_memo_test.go> over the round-2 fix rows, for f6d509f5's tree`: vet ok; tests ok 1.912s
- `reviewer probes zz_r2_probe_test.go via -overlay on the final tree (scratch tests, no -run pattern quoted)`: A and A2 PASS: unadmitted=1 and loud=1 from pass 2. B FAIL, by design under decision (b): admissions 1,1,1, never published.
- `reviewer pass-cost probe zz_rv_probe_test.go via -overlay, c7 overlay then the final tree, back to back (scratch test, no -run pattern quoted)`: D=300/L=600 spent pass: c7 1.40 s (302 admissions, 1207 queries, 1 sync, rewritten, ends on errPassBudgetSpent); final tree 1.05 s (301 admissions, 2107 queries, 0 syncs, not rewritten). A lone final-tree run took 0.63 s with identical counts.
- `carried from round 1: go test -p 1 -count=20 -run '^TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead$' ./internal/daemon`: ok 429.366s (round 1); this round, the row passes in the full package run

### Criterion changes

- Round 2: TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames.
- Subtests renamed from 'names the blob=false/true' to 'names no blob' and 'names the blob'.
- The blob's name is now blob-8891-1&a.bin.
- A third case, 'names the blob as an encoder escapes it', puts the name in the corrupt line in json.Marshal's form, with a fixture check that the plain name is absent.
- Reason: pin encodedBlobName. The old assertions all remain.
- Round 2: TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce gains a refused line whose lease the journal holds (session sess-cap-held), placed after `next`, with a fixture assertion that drain_leased_deny_pending == pass number. Reason: pin that a held-lease line left unresolved does not lower readTo. Every old assertion is unchanged.
- Round 2 new rows: TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt (4 subtests) and TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps.
- Product, round 2: spoolMemo.readTo stops at the first line a pass read and left unconsumed without a lease the journal holds (drainFile's firstLeft, applied in rememberFile even below an earlier readTo). The four leave points are:
- the unresolved-identity continue when !held;
- the lease-failure break;
- an unleased line that processOne defers;
- an unleased line that processOne hard-errors on.
- Product comment, round 2: the Failed branch no longer claims that the spooled bytes always decide the refusal. It names process conditions (an uncompilable policy, an unprovable scope) and says the record is lost loudly as it is in order. No behaviour change.
- Docs, round 2: withPassBudget, the spoolMemo bullet and the readTo doc, and docs/architecture.md now state the exception to announce-once past the cap (a line behind one a pass left unleased and unconsumed).
- Round 1, unchanged: blocked-spools and blob rows strengthened; meterDrainer counts scans and blob body reads; product changes (corrupt line in the reference scan, cleanup gated on front movement, memo survives growth, early-stop carry-forward, unacknowledged lines not memoized, readTo, seams).
- Round 0, unchanged: four rows moved to slowWaitingHead; the stall row is keyed on client-6161.ndjson's own sync and asserts that it fired; spoolWatchTraffic uses t.Errorf plus return. Product changes stand: the memo, session-scoped and end-of-file re-attempt, no identical rewrite, no re-sync of an unchanged synced file, de-duplicated intents named without a body read.

### Open issues

- Wall times on the shared host are noisy and co-loaded. Same-session pairs put the final tree at roughly 0.75x to 1.5x c7's spent pass. A quiet-host A/B belongs in the night chain; the operation counts are the firm evidence.
- Re-admitting each waiting line dominates a no-progress pass (scopeRefusal was 4.07 s of a 5.45 s profiled pass). c7 pays the same per line.
- Past orderingProcessedCap (4096) out-of-order lines in one file, a no-progress pass still admits, looks up and journal-checks each such line on every pass. This is documented and pinned.
- Round 2, conservative by design: past orderingProcessedCap, a corrupt or unadmitted line can be announced again on later passes. This happens when it sits behind a line a pass read and left unconsumed under no lease (e.g. a journal lookup that keeps failing), and lasts while that line stays left. A duplicate is possible; silence is not.
- Round 2, decision (b): a never-leased line refused for a process condition (daemon-side CaptureErrorPolicy, unprovable scope) and consumed out of order behind a waiting head stays final for the daemon's lifetime, as an in-order one is. Listed for ratification.
- While any cleanup intent waits, every pass reads and decodes each spool file's unconsumed lines once, at its start (cleanupAcknowledged). Left as is: a stat-keyed cache would authorize blob deletions on a stat.
- Pre-existing: scanPendingBlobs refuses a trailing partial line (a hook killed mid-append). While any intent waits, that client spool fails every pass's cleanup until the line is completed or the file removed.
- A line rewritten in place before spoolMemo.readTo is consumed afresh (its sum guards that), but its corrupt or unadmitted announcement is suppressed. No writer rewrites a spool in place.
- The memo is in memory only: a restarted daemon consumes every remembered line in full once, including its count and announcement.
- The end-of-file re-attempt costs 3 journal queries per waiting line per pass: 900 of the 2,107 in the D=300/L=600 shape.
- Unpinned defensive branches, recorded as equivalent in practice:
- reattempt's top-of-round passStopped check (07568cd4);
- the end-of-file guard, memoOf's shrink check and the unlisted-memo deletion (3f705053).

### Needs owner

- New test-only number, carried from round 0: journalQueriesPerLine = 7 in internal/daemon/drain_pass_cost_test.go. It is 1 lease lookup, plus 3 frontier queries as a line is read, plus 3 at the end-of-file re-attempt for a line still waiting. No new number in rounds 1-2. The new rows use fixture counts only (pastCap = 3, existing orderingProcessedCap).
- Ledger: the D31 row in plans/V6-CLOSEOUT-CHECKLIST.md (read-only for this seat) still says 'no new line after the budget is spent'. Suggested amendment, or a pointer to D60(a): a spent budget ends a pass only once it has made progress; a no-progress pass is bounded by the spool, not the clock.
- Ratify a semantics change, extended in round 2. A line consumed out of order behind a waiting head is final for the daemon's lifetime, and stays final while its file grows (same identity, no shorter, each skip checked by the line's FNV-1a sum). That includes a never-leased line refused for a condition of this process rather than of its bytes: a policy the daemon cannot compile (CaptureErrorPolicy) or a scope it cannot prove (ScopeUnprovable).
- Base re-admitted such a line on every pass and published it if the condition cleared before the front reached it. An in-order line with the same verdict is lost on base and HEAD alike.
- Corrupt and unadmitted lines are counted and announced once per memo life. A line read and left unconsumed under no lease is announced by the pass that later skips it.
- The alternative is the reviewer's option (a): re-admit CaptureErrorPolicy-refused lines on every pass. It costs a policy recompile per such line per pass, and cannot separate daemon-side from hook-side CaptureErrorPolicy.
- Ratify the durability shortcut, carried from round 0: a spool file unchanged by identity, size and mtime since this drainer's own successful sync is not synced again, and drain.json is not rewritten with identical bytes.
- Decide whether to lift the memo's per-file cap (orderingProcessedCap). Doing so needs a new, derived memory-bound number.
- CI time: the D=300/L=600 cost row takes about 21 s per run, because its first pass makes over 1,800 lease, capture and ack fsyncs. Policy-denied lines would cut it to about 2 s while keeping every asserted count. I kept the published shape the task asked for.
- Lock.owned() (internal/daemon/lock.go, outside this seat) reads the lock file on every journal query: 0.65 s of a 5.45 s profiled pass. The auditor's optional per-pass memo is left for the owner.

## verify:drain: verdict `sound`, 1 finding(s)

- **nit** `internal/daemon/drain.go:85 (withPassBudget doc); internal/daemon/drain_pass_progress_test.go:319 and :357; internal/daemon/spool_watch_pass_progress_test.go:42-43 and :81-82`: Some comments were edited in place and never re-wrapped. Each leaves either one overlong line or a short line cut off mid-sentence. drain.go:85 is the withPassBudget doc the task asked to correct, and it is 164 characters wide. No line in base drain.go's comments is longer than 130. This is cosmetic only and changes no behaviour or meaning.
  - Evidence: Running awk 'length > 120' over the touched files gives: drain.go:85 at 164 characters ('...(spoolMemo says what else the memo leaves out). A file unchanged since this daemon synced it is not synced, nor its'), drain_pass_progress_test.go:319 at 126 ('...The next only consumes that line again: it must finish the spool'), and :357 at 122. spool_watch_pass_progress_test.go:42 ends 'and its budget then ends it' with the sentence continuing on the next line, and :81 ends 'each pass after the first stopped in the'. At base 738d67c7, drain.go has only two lines over 120 characters (126 and 130), and both are untouched.
  - Fix: Re-wrap these comment paragraphs to the file's usual ~105-column width. The text needs no change.


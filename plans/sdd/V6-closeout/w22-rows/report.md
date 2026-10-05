# Wave 22 rows seat

Branch `closeout/w22-rows`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/daemon/delivery_ack_groupcommit_test.go:531 (TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend); internal/daemon/ingest_wal_groupcommit_test.go:239-246 (awaitClosed); internal/daemon/ingest_test.go:108 (ingestACKWait = 10s)`: This is a known load-sensitive row with no owner and no disposition. Each awaitClosed is a fixed 10 s wall-clock wait. The w20-redeliver seat saw it fail once in a whole-package run under co-load (44.9 s test, about 9 s alone) and routed it to 'whoever owns delivery_ack_groupcommit_test.go'. Nobody took it. It runs in the pre-freeze `internal` step (-p 2, co-load) and in release-check's shared pass (default -p, about 22). A red there refuses the freeze or stops release-check at its first FAIL.
- **minor** `plans/V6-CLOSEOUT-CHECKLIST.md on verify/v6 (D62); internal/daemon/ingest_wal_groupcommit_test.go:239-246 (awaitClosed, 10 s ingestACKWait); plans/sdd/V6-closeout/w20-redeliver/report.md:222-235`: Items the w20-redeliver seat routed to the coordinator have no disposition in the ledger. (1) TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend failed once in a whole-package run under co-load: 'the return of acknowledgement 0 never happened' after 44.9 s, against about 9 s alone. It runs in tonight's prefreeze `./internal/...` step, which stops the night on any red. (2) A replayed compact SessionStart re-anchors lastCompactionTS and lastActivity. (3) An owed close is not persisted across a restart. (4) An owed close for another session's tool use closes the bound session's segment. D62 records only the main fix.
- **major** `internal/daemon/precompact_settle_bound_test.go:134 (TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools)`: An intermittent red has no cause and no owner. It failed once in a full internal/daemon run in w20-status round 0, then passed 20/20 alone. The seat reported that it 'looks load-sensitive and needs an owner'. No ledger row, report or commit takes it. The row uses live workers (liveOrderWorkers) and a real 30 s settle bound (liveOrderBound). Its first assertion, 'the first look's own spool was replayed', depends on the live lane publishing during the settle. The pre-freeze `internal` step and release-check's whole tree both run it.
- **minor** `internal/daemon/daemon_test.go:1613-1653 (TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing); internal/daemon/handlers.go:633-690; handlers.go:32 (promptReplyDeadline = 250ms)`: A carried wall-clock dependency has no owner (w19c rehydrate open issue). The ObservePrompt seam runs on a goroutine, and dispatchOp waits at most 250 ms from the request's TS before returning Empty. The test asserts `calls == 1` immediately after dispatchOp returns, and the test is t.Parallel. If the seam goroutine is not scheduled within 250 ms (race lanes, release-check -p 22), calls is 0 and the row fails.
- **minor** `internal/daemon/drain.go:727 (durableEnd(..., unchanged && memo.synced)) and drain.go:1300-1301 (durableEnd returns synced=false for a WAL segment the ingest holds)`: The new sync-skip has one durability precondition, and no row pins it: the memo may skip the sync only when this drainer's own sync covered the file (memo.synced). Consider a WAL segment a full Drain read while the ingest held it. That pass read only up to the ingest's synced size and wrote its memo with synced=false. If the ingest then releases the segment with no further write, for example closing it after a failed Sync, the next pass must sync the segment before it reads the unsynced tail. Two mutants break that rule and every row stays green: dropping the `&& memo.synced` conjunct, and returning synced=true for a held segment. Under either one, the drain leases the bytes in that tail with no sync at all. That is the orphan-lease hole durableEnd exists to close. The product code is correct today; nothing in the tests protects it.
- **minor** `internal/daemon/drain.go:1453 (rememberFile: `if firstLeft >= 0 { readTo = min(readTo, firstLeft) }`); internal/daemon/drain_memo_test.go:271-357 (TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt)`: The round-2 fix, where readTo stops at the first line a pass read and left under no lease, is pinned only for a left line behind a waiting head, so always at an offset above 0. The most natural position is the file's first line: for example, a one-line client spool whose lease lookup hits a rotating or briefly unowned journal (leaseHeld then returns an error). Nothing pins that case. Mutating `>= 0` to `> 0` leaves every row green. Under that mutant, the silent drop the round-2 review found comes back: the next pass consumes the line in order as unadmitted, below readTo, with no drain_unadmitted count and no LOUD line. The product code is correct today.
- **minor** `internal/daemon/drain.go:1494-1507 (pendingBlobOf: `req.Event == nil`, `!fi.Mode().IsRegular()`, `fi.Size() != int64(ref.Bytes)`)`: pendingBlobOf replaced readBlob as the way a consumed line that is not published (absorbed, retired or denied) names its cleanup intent. It is meant to repeat readBlob's pre-read checks. Only its safeBlobName check is pinned (R3, the nosafe mutant). The no-Event, regular-file and size checks can each be removed, or all three together, and every row stays green. If they regressed, a denied or absorbed line naming a directory, a symlink or a wrong-size file called blob-*.bin would record an intent that readBlob would never have produced. cleanupAcknowledged would then os.Remove that entry. A non-empty directory would fail removeBlob on every pass, and with it cleanupAcknowledged, so every pass would return an error.
- **minor** `internal/daemon/lock.go:439-445 (Lock.owned reads the lock file per journal query); internal/daemon drain scanPendingBlobs (trailing partial line)`: Two w20-drain needs-owner and open items have no ruling. (1) Lock.owned() reads the lock file on every journal query: 0.65 s of a 5.45 s profiled no-progress pass. The auditor's optional per-pass memo was 'left for the owner'. (2) Pre-existing: scanPendingBlobs refuses a trailing partial line (a hook killed mid-append). While any cleanup intent waits, every pass's cleanup of that client spool fails until the line is completed, so blob deletions stall.
- **minor** `internal/daemon/drain_pass_cost_test.go:142-193 (TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead)`: CI time. internal/daemon grew 17% per pass. One row, a 900-line spool whose first pass publishes 600 lines, costs 32-61 s per run on this host, about 35-40% of the time all the new daemon rows add. Its assertions are per-line bounds (journalQueriesPerLine*(1+waiting)), so a tenth of the lines proves the same thing. Every budget still holds.
- **minor** `internal/daemon/drain_pass_cost_test.go:142 (TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead)`: This wave-20 row adds 37-50 s of Windows wall time to every internal/daemon run (about 7% of the package), but it only judges operation counts. Almost all of that time goes to the fixture's first pass, which makes 600 durable publications. The proof does not need 600 of them. It runs on every hosted daemon job (test x3 OS, stubskips, cover, release-dry-run, nightly race), and under -race it will cost several times more.
- nit `internal/daemon/drain.go:1447 (rememberFile carry: `if start >= readPos && len(lines) < orderingProcessedCap`)`: The spoolMemo doc says a memo holds at most orderingProcessedCap lines per file. The only thing that keeps that true across an early-stopped pass is the cap in the carry-forward loop, and no row pins it. Without the cap, a pass that stops early carries every earlier line at or past readPos on top of the up to 4096 it consumed. Repeated stops then let one file's memo grow past the cap.
- nit `internal/daemon/drain.go:1419 (memoOf: `fi.ModTime().Equal(m.stat.ModTime())`), :694/:729/:1565/:613-617 (memo deletions), :1149/:1060 (in-loop session re-attempts), :1160 (end-of-file guard)`: Several new branches survive mutation and are not recorded as deliberate defence. Commit 3f705053 did record three others: the end-of-file guard, memoOf's shrink check and the unlisted-memo deletion.
- The mtime clause of `unchanged` is unpinned.
- The memo deletions on a stat that finds the file gone, on a sync error, on a removal and for a fully consumed file are equivalent in practice: os.SameFile catches a file that comes back.
- The in-loop session-scoped re-attempts after a done line and after a denial are unobservable, because the end-of-file re-attempt releases the same lines in the same pass. Only within-pass latency changes.
- nit `docs/architecture.md:191-193 and internal/daemon/drain.go:86-88 (withPassBudget doc: 'the pass reads each file's unconsumed lines once more, at its start')`: Both texts say a pass reads the spool for blob references once, at its start, while an intent waits. That is exact only for a pass that moves no front. After every file whose front moved, drain.go:1259-1263 runs cleanupAcknowledged again, and pendingBlobReferences re-reads every spool file. So a pass that moves k fronts reads the whole spool 1+k times.

## impl:rows: status `done`, head `7fc33969 (squashed)`

### Summary

Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-rows, branch closeout/w22-rows, 8 commits on base 2bf29705. The tree is clean and nothing is pushed. Only internal/daemon/*_test.go and scanPendingBlobs in internal/daemon/drain.go changed; handlers.go is untouched because #74 did not need a seam.

#63 and #7 item 1. I added hangGuard(t) to ingest_test.go and removed ingestACKWait. The guard fires only at t.Deadline() less a reporting reserve (a tenth of the time left, at most 30 s). With no deadline it never fires. All 19 ingestACKWait sites now use it: awaitClosed, awaitAcks, queueAcks/queueLeases/queueBehind, awaitParked, awaitClosing, awaitRelease, acknowledgedWithin, awaitPasses, requireNoLeakedInflight, the serial-Sync detector and the rest. Red first: I injected an 11 s stall into batch 1's seal through an overlay probe. On a base export the T14 row failed with the production message "the return of acknowledgement 0 never happened". On HEAD it passes. A true hang (seal held for an hour, -timeout=90s) is still reported by name: "the return of acknowledgement 0 never happened" printed before the timeout panic.

#64. The failure did not reproduce with -count=50 at -p 4 under QOMPACK_UNDER_COLOAD=1, nor under -race -cpu 1 -count=50. Cause: the row's settle bound is 30 s, so the only wall-clock limit left on the replay path is the line's drainLineDeadline (5 s, drain.go:1884). A grep confirms these two are the only WithTimeout/WithDeadline calls on that path. The failing run took 11.73 s for a row that normally takes 0.6 s, with about 100 ms of dispatch (measured by a probe). A stall longer than 5 s cancels the line. The product then correctly names the capture as unreplayed, and the row fails on its first assertion. Red first: a stall past the line's deadline injected into the row's Dispatch seam gives the exact failure on base ("the first look's own spool was replayed") and passes on HEAD. Fix: a test helper, settleReplay, dispatches settle-row replays with context.WithoutCancel. I applied it to every settle row that asserts what a replay publishes: ReplaysTheSpoolBeforeTheSeal, ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers, DoesNotWaitBehindAWatcherPassItsOwnRequestKicked, TheLastLookReadsOnlyNamedAndNewSpools, ReadsASpoolTheDrainReleasedAndAHookRecreated and ReplaysAgainOnceALiveCopyAheadOfItPublishes. The settle deadline and settleCut still apply. AColdBacklogLeavesTheReplayTheRestOfTheBound and the drain's own line-deadline row keep the deadline, because it is their subject.

#74. The row now joins the seam through dd.stopPromptRecordings(context.Background()) before counting. That join has no clock. The row pins two schedules. The second holds the seam until the reply has returned, which is red on the base's count-at-return pattern (expected 1, actual 0). The passive-reply assertions are unchanged; under !MayAct the route drops the output whatever the timing.

#0, #1, #2. New rows, each red under its audit mutant, re-run through go test -overlay:
- TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead is red under ms_nosyncedflag and mw_heldsynced (syncs expected 1, actual 0).
- A new case in TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt puts the left line first in the spool; red under mf_left0 (unadmitted 0).
- TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent is now a table with three new cases: a directory under a blob's name, a wrong-size blob and a descriptor with no event. mb_noregular, msz_nosize and mev_noevent are each red on their own case; the combined mbs_all is red on all three.

#75. scanPendingBlobs now takes a trailing partial line as its final token and handles it like an undecodable line: it holds back only the pending blobs it names, plain or JSON-escaped. TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames fails all three cases on base at pass 1 with "daemon: drain: incomplete blob reference source", and passes on HEAD. The sibling corrupt-line row still passes. The Lock.owned per-query read is accepted under D67(e), so nothing was done there.

#38/#83. Left unchanged. A phase probe of the first pass (600 durable publications) put the lease and ack journal Syncs and seals at about 33 s of a 166 s pass on this loaded host. Most of the cost sits elsewhere on the publication path, so the row cannot get cheaper at 300/600 with the same bounds. Shrinking to 30/60 changes the scale, which the task rules out.

Sweep of internal/daemon for fixed wall-clock verdicts. 58 more selects that only guarded a channel now use hangGuard, with their messages unchanged. Files: daemon_test, prompt_record, prompt_warning_delivery, startup_accept, startup_daylog, admin_shutdown_reply, borrowed_lease, spawn_claim_release, ingest_test:285, delivery_path_v6_unix (3 s FIFO), scheduler_runtime (2 min), flush_async (11), delivery_order_live:692, publication_yield (5), session_end_gc (2), flush_arrival_order:213, session_start_bookkeeping (2), session_start_compact (3), session_start_replay:72. These remain, as listed in open issues.

Nit fixed: the memo-cap row now asserts len(memo.consumed) <= orderingProcessedCap after its early-stopped pass, red under mc_carrynocap.

Criterion changes:
- (1) Every guard-only channel wait in internal/daemon tests is now bounded by the test binary's deadline instead of a fixed 3 s to 2 min. The messages are unchanged and a real hang still fails by name; a slow host no longer fails them.
- (2) Settle rows that assert what a replay publishes no longer apply drainLineDeadline (5 s) to that replay's dispatch. The rows whose subject is a line the deadline cuts keep it.
- (3) The passive prompt row counts after joining the seam, not when the reply returns.

Scratch evidence (overlay probes, mutant runner, logs) is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/rows-work. Linux artifacts are in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rows/cx-linux-w22-rows-daemon-88e4596-20261004T191039Z-artifacts.

### Commits

- e95578f4 test(daemon): make the fixed 10 s waits pure hang guards
- 8e6b2eb3 test(daemon): count the passive prompt seam after joining it
- 3a22827d test(daemon): lift the line deadline from the settle rows' replays
- 7fc33969 test(daemon): pin three drain preconditions mutants survived
- 80709623 fix(daemon): skip a trailing partial line in the blob reference scan
- dd281507 test(daemon): bound the package's other channel waits by hangGuard
- 149c73d2 test(daemon): pin the memo's per-file cap across an early stop
- 7fc33969 (squashed) test(daemon): gofumpt the first-line case of the left-line row

### Findings resolution

- **fixed**: #63 (major) TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend awaitClosed fixed 10 s
  - e95578f4: hangGuard replaces ingestACKWait at all 19 sites. Red first: an 11 s seal stall injected on a base export fails with 'the return of acknowledgement 0 never happened'; HEAD passes. A real hang still fails by name.
- **fixed**: #7 item 1 (minor) awaitClosed as a pure hang guard (D67(b))
  - Same commit as #63. Items 2-4 of #7 belong to the redeliver seat and ledger (D67(b)), not this seat.
- **fixed**: #64 (major) TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools intermittent
  - 3a22827d. No repro at -count=50 -p 4 co-load, nor -race -cpu 1 -count=50. Cause: drainLineDeadline (5 s) is the only wall-clock limit left under the 30 s settle bound, so a stall past it cancels the replay line. A stall injected past the line deadline reproduces the exact base failure ('the first look's own spool was replayed'); HEAD passes. settleReplay is applied to all six settle rows that assert a replay's publication.
- **fixed**: #74 (minor) TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing 250 ms schedule
  - 8e6b2eb3: the row joins via stopPromptRecordings(Background) before counting. A late-seam subtest is red on the base's count-at-return (expected 1, actual 0) and green on HEAD. No seam was needed in handlers.go.
- **fixed**: #0 (minor) durableEnd synced precondition unpinned
  - 7fc33969: TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead, red under ms_nosyncedflag and mw_heldsynced.
- **fixed**: #1 (minor) firstLeft at offset 0 unpinned
  - 7fc33969: new case 'its lease lookup failed at the spool's first line', red under mf_left0.
- **fixed**: #2 (minor) pendingBlobOf regular/size/Event checks unpinned
  - 7fc33969: table cases (directory, wrong size, no event). mb_noregular, msz_nosize and mev_noevent are each red on their case; mbs_all is red on all three.
- **fixed**: #75 (minor) scanPendingBlobs trailing partial line; Lock.owned residual
  - 80709623: the trailing partial line is treated like an undecodable line and holds back only the pending blobs it names. TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames is red on base ('incomplete blob reference source') and green on HEAD. Lock.owned is accepted per D67(e).
- **deferred-known-issue**: #38/#83 (minor) drain_pass_cost row costs 37-61 s
  - Not cheaper at the same scale: journal fsyncs and seals are only about 20% of the first pass, and shrinking to 30/60 changes the scale. Release-notes sentence: 'internal/daemon's drain pass-cost row spends about 35-60 s of each package run on 600 durable publications; it is kept at full scale because its bounds are asserted there.'
- **fixed**: nit: rememberFile carry-forward cap unpinned
  - 149c73d2: the memo-cap row asserts len(memo.consumed) <= orderingProcessedCap after the early stop, red under mc_carrynocap.

### Tests

- `GOOS={windows,linux,darwin} go vet ./internal/daemon`: clean on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: clean (after 7fc33969 (squashed))
- `go test -p 2 -count=1 ./test/docs/...`: ok 27.0s
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows, HEAD 7fc33969 (squashed))`: ok 977.0s
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-rows 7fc33969 (squashed) w22-rows-daemon --gomaxprocs 2 --timeout 60m -- ./internal/daemon`: PASS, -race, non-root uid 10001: pass=1793 fail=0 skip=1, go_test_exit=0
- `QOMPACK_UNDER_COLOAD=1 go test -p 4 -count=20 -timeout=60m -run '^(TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead|TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt|TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent|TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames|TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce|TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools|TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend)$' ./internal/daemon`: ok 449.8s
- `go test -count=20 -run '^TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing$' ./internal/daemon`: ok
- `go test -race -count=3 -timeout=60m -run '^(the same 7 rows|TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing)$' ./internal/daemon`: ok 64.9s
- `go test -count=1 -run '^(TestPreCompactSettle_|TestPrecompactSettleBound_|TestSpoolHeadIndex_)' ./internal/daemon`: ok 101.1s
- `red-first overlays on a base export of 2bf29705 (11 s seal stall; stall past the line deadline)`: base FAIL with the production messages; HEAD ok
- `mutant overlays on drain.go: ms_nosyncedflag, mw_heldsynced, mf_left0, mb_noregular, msz_nosize, mev_noevent, mbs_all, mc_carrynocap`: all RED on the new or extended rows
- `QOMPACK_UNDER_COLOAD=1 daemon-base.test.exe -test.run '^TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools$' -test.count=50 (and -race -test.cpu=1 -test.count=50)`: both PASS 50/50 on base: no natural repro, so the cause was established by mechanism and stall injection

### Criterion changes

- Guard-only channel waits in internal/daemon tests (19 ingestACKWait sites plus 58 swept selects) are now bounded by hangGuard(t), which fires at the binary's deadline less a reserve, instead of a fixed 3 s to 2 min. Rationale: a fixed bound was a wall-clock verdict under co-load (#63). Messages are unchanged and a real hang still fails by name.
- The settle rows that assert what a replay publishes no longer apply drainLineDeadline (5 s) to that replay's dispatch (settleReplay, context.WithoutCancel). The settle deadline and settleCut still apply. Rationale: the line deadline was the only wall-clock limit left on the path, and a host stall past it caused #64's red. Rows whose subject is a line the deadline cuts keep it.
- TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing counts the seam after joining its goroutine (stopPromptRecordings with a grace that never ends) instead of when the reply returns, and pins the late-seam schedule. The reply assertions are unchanged (#74).

### Open issues

- Sweep, left as fixed wall-clock verdicts because the bound is the verdict or the change is not cheap: TestObservePromptBlockingVariantTimesOut (elapsed < 1 s, daemon_test.go:191); TestObservePromptRepliesWithinDeadline (needs the seam scheduled inside the 250 ms promptReplyDeadline; making it deterministic needs a handlers.go deadline seam); TestIngestRingFullWALsEveryLineAndNeverSpills (500 ms ingestAcceptBudget); flush_async_test.go:460 (Stop within its own bound); spawn_lock_test.go:449 and :565; scheduler_features_test.go:223 (best < 10 ms); scheduler_idle_test.go:534 (best <= 5 ms); delivery_order_lanes_test.go:555; flush_arrival_order_test.go:109 (flushOrderReplyBound 15 s); startup_accept_test.go:142 (startupReplyBound+1 s); daemon_test.go:947, 979 and 1491 (Run must idle-exit before its 15 s ctx, judged at 12 s); dispatchWithin and drainWithin with compactTestBound (3 s against the 5 s compact budget); drainDeadlockGuard (10 s) Run contexts and polls at daemon_test.go:499, 521, 616, 639, 1105, 1117 and 1199; and about 76 require.Eventually polls (mostly liveOrderBound 30 s, a few 10 s and 5 s) whose per-site semantics need review before converting.
- drain.go cleanupAcknowledged's comment 'incomplete/unreadable input cannot authorize collection' is slightly stale now that a trailing partial line no longer errors (only unreadable or overlong lines do). The comment is outside this seat's drain.go scope (scanPendingBlobs only).
- #38/#83: the drain pass-cost row stays at 300/600 and about 35-60 s per daemon pass (deferred-known-issue).

### Needs owner

- Ratify criterion change (2): settle rows that assert a replay's publication run that replay without drainLineDeadline (settleReplay). This is the same move settleTestDaemon already makes with its 30 s bound. A red from TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound under a host stall over 5 s remains possible by design.
- Decide whether the remaining Eventually polls and the 250 ms TestObservePromptRepliesWithinDeadline get a further seat. The latter needs a promptReplyDeadline seam in handlers.go.

## review:rows:r1: verdict `needs-fixes`, 5 finding(s)

- **major** `internal/daemon/daemon_test.go:1034 and :1047-1052 (TestAdminShutdownStopsTheDaemon); daemon_test.go:1105 and :1125-1130 (TestRunReturnsOnlyAfterAsyncStopHasFinished); internal/daemon/admin_shutdown_reply_test.go:36 and :64-69 (TestAdminShutdownReplyReachesTheCallerOverTheTransport); commit dd281507`: Three checks were weakened in the hangGuard sweep. These rows still run Run under a context with a fixed timeout: 10 s, drainDeadlockGuard (10 s), and 2*shutdownReplyBound. On base, the fixed select bound (8 s, 10 s, stopCleanupBound) fired before that context expired, so 'admin.shutdown did not stop the running daemon' was reachable. Now the select waits on hangGuard, which fires only near the binary deadline. Run therefore always returns nil through the context's own expiry first, and the rows pass even when admin.shutdown never stops the daemon. stopDone also closes, because the ctx.Done arm goes through Stop. Each row's own subject is therefore no longer checked. This breaks the hard rule against weakening a check.
  - Evidence: Mutant: handlers.go `go func() { _ = d.Stop(context.Background()) }()` replaced by a no-op (verify-rows/mut/nostop_*.json).
- Base 2bf29705: TestAdminShutdownStopsTheDaemon FAIL (8.08 s, 'admin.shutdown did not stop the running daemon'); TestRunReturnsOnlyAfterAsyncStopHasFinished FAIL (10.19 s, same message); TestAdminShutdownReplyReachesTheCallerOverTheTransport FAIL (15.46 s, same message).
- HEAD 7fc33969 (squashed): all three PASS, in 10.03 s, 10.05 s and 30.04 s, which is exactly when their Run contexts expire.
  - Fix: In these three rows, give Run a context.WithCancel with no timeout, so that hangGuard is the only bound and the stop must come from admin.shutdown. An alternative that needs no clock: after Run returns, require.NoError(t, ctx.Err()) to prove the shutdown ended Run, not the deadline. Show each row red under the no-Stop mutant. Then re-check every other converted select whose awaited event a fixed-timeout context can also produce. I found no others, but the seat should confirm.
- **major** `internal/daemon/publication_yield_test.go:215-231 (TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries); delivery_order.go:816 (requestedDrainPass uses withPassBudget(ctx, idleRunBudget), 2 s); drain.go:1900 (drainLineDeadline, 5 s)`: The Linux non-root gate is red on the seat's HEAD. The failing row is in #64's class: a product wall-clock limit inside a one-shot drain row whose assertions are about content, not timing. requestedDrainPass runs under the 2 s idleRunBudget pass budget. When the first delivery takes more than 2 s under -race and co-load, the pass stops before the second spool and the fixture assertion fails. The row itself is unchanged since base, so this is pre-existing, not a regression. But the seat's fix for #64 (settleReplay) covers only settle rows, and its sweep 'for any other test with a fixed wall-clock verdict' neither fixed nor listed the one-shot Drain and requested-pass rows that are exposed to the pass budget or drainLineDeadline. The seat's own new rows are among them: for example, TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames does require.NoError(dr.Drain(ctx)), then requires the line published. Drain also stops on DeadlineExceeded (drain.go:646).
  - Evidence: `sh linux-nonroot-gate.sh --repo <seat worktree> --out .../wave22/linux-rows-verify 7fc33969 (squashed) w22-rows-verify --gomaxprocs 2 --timeout 60m -- ./internal/daemon` returned: FAIL internal/daemon (pass=1792 fail=1 skip=1), TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries (5.59 s), publication_yield_test.go:226 'fixture: the pass published the second spool', go_test_exit=1. The seat's run of the same command passed (1793/0). The Windows full run passed: `go test -p 2 -count=1 -timeout=30m ./internal/daemon/`, ok 818.5 s.
  - Fix: Make this row independent of the pass budget without loosening it. For example, drive requestedDrainPass until passLeftWork stops asking: the product's own resumeDrain loop, counted, not timed. Or have the row install a drainer whose pass carries no budget, and assert the gate on every delivery as now. Then list the remaining one-shot Drain rows exposed to drainLineDeadline and pass budgets. Fix the cheap ones the way settleReplay does, and record the rest as a known issue with this row's failure signature.
- **minor** `internal/daemon/session_start_compact_test.go:107-111 (joinReplyWork, compactTestBound 3 s; called mid-row at sentinel_scan_order_test.go:205 and session_start_compact_failure_test.go:127 and :170); session_start_phases_test.go:35 (compactPhasesJoinBound 10 s); flush_async_test.go:88-92 (flushAsyncAwait, liveOrderBound 30 s)`: The sweep of fixed wall-clock verdicts missed the join-grace form of #63's class. Neither the commits nor the seat's open-issues list mention these sites. Each passes a fixed-timeout context to stopPromptRecordings or awaitSessionEnds as a pure hang guard. When the grace expires under co-load, the join cancels the work (promptCancel), or flushAsyncAwait fails outright. The row then asserts on what that work recorded: drops, histograms or the sentinel. A slow host can therefore turn these rows red, as ingestACKWait did.
  - Evidence: stopPromptRecordings (handlers.go:840-865) calls promptCancel as soon as grace.Done() fires. joinReplyWork wraps it in context.WithTimeout(compactTestBound=3s), and the rows that call it go on to assert CurrentDrops (session_start_compact_failure_test.go:128-132 and :171-176). flushAsyncAwait does require.True(dd.awaitSessionEnds(ctx with liveOrderBound)). None of these appear in dd281507 or in the seat's open_issues.
  - Fix: Join with context.Background(), the same no-clock join used for #74, and leave a hang to go test -timeout. Do the same for flushAsyncAwait (awaitSessionEnds with Background). At minimum, add the sites to the reported list.
- **minor** `commits dd281507, 80709623, 3a22827d on closeout/w22-rows`: Three commit subjects are longer than the 64-character limit.
  - Evidence: 'test(daemon): bound the package's other channel waits by hangGuard' is 66 characters; 'fix(daemon): skip a trailing partial line in the blob reference scan' is 68; 'test(daemon): lift the line deadline from the settle rows' replays' is 66. All 8 commits do have the Refs footer and no attribution trailers.
  - Fix: Reword these three subjects to 64 characters or fewer on the seat's own branch, by a non-interactive rebase or by recreating the commits. The fmt-only fixup 7fc33969 (squashed) can be folded into 7fc33969 at the same time.
- **nit** `internal/daemon/daemon_test.go:1682-1730 (TestObservePrompt_ModeOffNeverInvokesTheSeam)`: This is #74's sibling. It counts the seam's calls the moment dispatchOp returns, the pattern #74 retired. It cannot go red from this, but a seam started late (after the 250 ms reply wait) escapes its require.Zero, so the negative check depends on the scheduler.
  - Evidence: It has the same count-at-return structure as base's #74 row. Under the late-seam schedule, my overlay of HEAD's #74 row with the join removed showed calls read as 0 while the seam still ran (expected 1, actual 0).
  - Fix: Call dd.stopPromptRecordings(context.Background()) before counting, as the #74 row now does.

## fix:rows:r1: status `done`, head `ae872f0313ca062efd884f4ba944561a54a355a0`

### Summary

All four fix-round findings are closed on closeout/w22-rows, in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-rows. HEAD is ae872f03 and the branch holds 10 commits on base 2bf29705. The tree is clean and nothing is pushed. Every file changed is an internal/daemon/*_test.go file; this round touched no product code. Rewriting the history changed every SHA. The commits list maps old SHAs to new ones, and `git diff ae872f03 ae872f03` (ae872f03 was the pre-rewrite head) shows the rewrite left the final tree unchanged.

Finding 1 (major, three weakened rows): fixed in fcc66219. TestAdminShutdownStopsTheDaemon, TestRunReturnsOnlyAfterAsyncStopHasFinished and TestAdminShutdownReplyReachesTheCallerOverTheTransport now run Run under context.WithCancel, with no deadline.
- Second source, same class: Run's idle exit (default 1800 s) also returns nil through Stop. Under a go test -timeout of more than about 30 minutes, such as the Linux gate's 60m, it fires before hangGuard, so a row whose named shutdown path is broken still passes.
- New helper runTestConfig puts the idle exit out of reach (IdleExitSeconds = math.MaxInt32). Every row that waits on Run, under hangGuard or unbounded, now uses it: the three rows plus StartupDrainOfSpooledFlushLineDoesNotWedgeRun, RedrainOnFirstServedRequest, RunReturnsNilWhenLockHeld, ServeFailureTakesTheStopPath, RunCtxDoneGoesThroughStop, BorrowedLease, both spawn_claim_release daemons, Run_AcceptsDialsWhileItsStartupDrainRuns, Run_LogsATakenOverLockAndASpoolReplay and Run_ReportsRunningFromThePluginDirectory.
- Red first, under the verifier's no-op admin.shutdown Stop mutant: all three rows FAIL at hangGuard with 'admin.shutdown did not stop the running daemon' (about 81 s at -timeout=90s). The previous head passed them at context expiry.
- Idle-exit source, with the idle window compressed to 3 s by an overlay of config/defaults.go: a no-deadline context alone still PASSES the mutant, by idle exit in 3.95 s. With runTestConfig it FAILS.
- Sweep of all 76 converted hangGuard sites: no other test-supplied fixed-timeout context can produce the awaited event. The other WithTimeout Run rows (startup drain, daylog, lock-held B, spawn B) call cancel() before they wait, so the event is a cancellation either way. The remaining WithTimeout contexts are cleanup graces or rows whose subject is the timeout. None of the product timers (idleRunBudget 2 s, drainLineDeadline 5 s, stopCleanupBound 15 s) falls between a converted site's old bound and the binary deadline in a way that could stand in for the row's subject.

Finding 2 (major, Linux red on TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries): fixed in fee5baed.
- The row now runs as many requested passes as drainKick asks for, the way drainOnRequest would. It counts them (at most lines+1), never times them, and still checks the gate at every delivery of every pass.
- New helper withoutLineDeadline (settleReplay is now built on it) lifts the per-line drainLineDeadline from a dispatch and keeps the context's values. gateAtDispatch uses it, which covers both gate rows. So does memoDrainConfig, which all 13 drain_memo_test.go rows, including the seat's own new rows, now drain through. Pass budgets a row sets itself still apply.
- Red first, with an overlay that stalls the first dispatch in the process:
  - At 2.5 s the old row fails exactly as on Linux ('fixture: the pass published the second spool').
  - At 5.5 s the old row fails on the first spool; TestLookAtClientSpools_HoldsTheCaptureGateForItsPass fails on its spool; TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames fails with 'context deadline exceeded, pass 1'.
  - All three pass on HEAD under both stalls.
- The remaining exposed rows are listed in open_issues.

Finding 3 (minor, join-grace form of #63): fixed in ae872f03. joinReplyWork, the phases row's join and flushAsyncAwait now join with context.Background(). compactPhasesJoinBound is removed. Red first, with overlay stalls:
- Bookkeeping work held 3.5 s: TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest fails.
- Rehydration held 16 s: TestSessionStartRoute_RecordsItsPhases fails ('rehydrate.latest must be recorded once per compact start').
- A released SessionEnd held 31 s: TestFlush_AnswersOnceDurableNotAfterSessionEnd fails ('an accepted session end never finished').
- Each passes on HEAD under the same stall. The other stopPromptRecordings(grace) and awaitSessionEnds calls are all inside t.Cleanup and assert nothing.
- The verifier's drops sites (session_start_compact_failure_test.go:127 and :170) record their drops before the route answers (notBuilt calls recordNotBuilt under WithoutCancel). The join could not reach them, but they now get the same clockless join.

Finding 4 (minor, subjects too long): fixed by recreating the commits on the seat's own branch. All 10 subjects are 64 characters or fewer, the longest 62. All carry 'Refs: V6-VERIFY, C3.2' and no attribution trailers. The gofumpt fixup is folded into 7fc33969.

Scratch evidence (overlays, probe binaries, logs) is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/rows-fix1. Linux artifacts are in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rows-fix1/cx-linux-w22-rows-fix1-ae872f0-20261004T221938Z-artifacts. I started no containers, and no process I started is still running.

### Commits

- e95578f4 test(daemon): make the fixed 10 s waits pure hang guards (was e95578f4)
- 8e6b2eb3 test(daemon): count the passive prompt seam after joining it (was 8e6b2eb3)
- 3a22827d test(daemon): lift the line deadline from settle row replays (was 3a22827d, subject reworded)
- 7fc33969 test(daemon): pin three drain preconditions mutants survived (was 7fc33969, with the gofumpt fixup 7fc33969 (squashed) folded in)
- ac4e6523 fix(daemon): let the blob scan skip a trailing partial line (was 80709623, subject reworded)
- dd281507 test(daemon): bound the other channel waits by hangGuard (was dd281507, subject reworded)
- 149c73d2 test(daemon): pin the memo's per-file cap across an early stop (was 149c73d2)
- fcc66219 test(daemon): let only the row's own cause end Run (new, finding 1)
- fee5baed test(daemon): lift pass budget and line deadline off gate rows (new, finding 2)
- ae872f03 test(daemon): join reply work and session ends with no clock (new, finding 3)

### Findings resolution

- **fixed**: major: hangGuard sweep weakened TestAdminShutdownStopsTheDaemon, TestRunReturnsOnlyAfterAsyncStopHasFinished, TestAdminShutdownReplyReachesTheCallerOverTheTransport (Run under fixed-timeout contexts)
  - fcc66219. The three rows now run Run under context.WithCancel. runTestConfig (idle exit out of reach) also closes the second source of the same class, Run's 30-minute idle exit, for every row that waits on Run (14 rows). Red first under the no-op Stop mutant: all three FAIL at hangGuard with 'admin.shutdown did not stop the running daemon'. With the idle window compressed to 3 s, a no-deadline context alone PASSES the mutant (3.95 s), and runTestConfig makes it FAIL. The sweep of all 76 hangGuard sites found no other site whose awaited event a fixed-timeout context can produce.
- **fixed**: major: TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries red on Linux (idleRunBudget pass budget / drainLineDeadline in a content row)
  - fee5baed. The row runs the requested passes drainOnRequest would, counted through drainKick (at most lines+1), not timed. withoutLineDeadline lifts the line deadline in gateAtDispatch (both gate rows) and in memoDrainConfig (all 13 drain_memo_test.go rows, including the seat's new ones). Red first with a first-dispatch stall: at 2.5 s the old row fails with the Linux message; at 5.5 s it fails, as do TestLookAtClientSpools_HoldsTheCaptureGateForItsPass and TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames; HEAD passes both stalls. The remaining exposed rows are listed in open_issues.
- **fixed**: minor: join-grace form of #63 (joinReplyWork compactTestBound, compactPhasesJoinBound, flushAsyncAwait liveOrderBound)
  - ae872f03. All three now join with context.Background(), and compactPhasesJoinBound is removed. Red first under stalls: 3.5 s bookkeeping stall fails TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest; 16 s rehydration stall fails TestSessionStartRoute_RecordsItsPhases; 31 s SessionEnd stall fails TestFlush_AnswersOnceDurableNotAfterSessionEnd. All pass on HEAD under the same stalls. The other grace joins are cleanup-only.
- **fixed**: minor: three commit subjects over 64 characters (dd281507, 80709623, 3a22827d)
  - Commits recreated on closeout/w22-rows: dd281507 (56 chars), ac4e6523 (59), 3a22827d (60). The gofumpt fixup 7fc33969 (squashed) is folded into 7fc33969. My own new commit fee5baed was also kept under 64 (62). The tree is identical to the pre-rewrite head ae872f03.

### Tests

- `no-op admin.shutdown Stop mutant (overlay on handlers.go), fixed tree: each of TestAdminShutdownStopsTheDaemon, TestRunReturnsOnlyAfterAsyncStopHasFinished, TestAdminShutdownReplyReachesTheCallerOverTheTransport at -test.timeout=90s`: all three FAIL at hangGuard (81.2 s, 81.0 s, 81.3 s), 'admin.shutdown did not stop the running daemon'
- `same mutant plus IdleExitSeconds 1800->3 overlay, TestAdminShutdownStopsTheDaemon: no-deadline context only vs runTestConfig`: no-deadline context only: PASS in 3.95 s (masked by idle exit); runTestConfig: FAIL at hangGuard
- `first-dispatch stall overlay (2.5 s and 5.5 s), base rows vs HEAD rows: TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries, TestLookAtClientSpools_HoldsTheCaptureGateForItsPass, TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames`: base: 2.5 s fails the requested-pass row ('the pass published the second spool'); 5.5 s fails all three. HEAD: all pass under both stalls
- `reply-work and SessionEnd stall overlays, previous vs HEAD: TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest (3.5 s), TestSessionStartRoute_RecordsItsPhases (16 s), TestFlush_AnswersOnceDurableNotAfterSessionEnd (31 s)`: previous: all three FAIL; HEAD: all three PASS
- `GOOS={windows,linux,darwin} go vet ./internal/daemon`: clean on all three
- `go run ./tools/devtool fmt-check`: clean (exit 0)
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs/...`: ok 15.6s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows, HEAD ae872f03)`: ok 566.3s
- `sh .../linux-nonroot-gate.sh --repo <seat worktree> --out .../wave22/linux-rows-fix1 ae872f03 w22-rows-fix1 --gomaxprocs 2 --timeout 60m -- ./internal/daemon`: PASS, -race, non-root: pass=1793 fail=0 skip=1, go_test_exit=0
- `go test -p 2 -count=20 -timeout=30m -run '^(TestAdminShutdownStopsTheDaemon|TestRunReturnsOnlyAfterAsyncStopHasFinished|TestAdminShutdownReplyReachesTheCallerOverTheTransport|TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries|TestLookAtClientSpools_HoldsTheCaptureGateForItsPass|TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest|TestSessionStartRoute_RecordsItsPhases|TestFlush_AnswersOnceDurableNotAfterSessionEnd|TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames|TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead)$' ./internal/daemon`: ok 76.5s
- `go test -race -p 2 -count=3 -timeout=30m -run (same 10 rows) ./internal/daemon`: ok 16.4s <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=1 -run '^(TestDrain_|TestDrainClientSpools_|TestDrainer_|TestRequestedDrainPass_|TestLookAtClientSpools_|TestPreCompactSettle_|TestLaunchSessionEnd_|TestStartupPublicationAccounting_|TestDispatchOp_)' ./internal/daemon`: ok 232.3s
- `go test -p 2 -count=1 -run '^(TestSessionStartCompact_|TestSessionStartRoute_|TestSentinelScan_|TestFlush_|TestStop_|TestDrain_AReplayedFlush|TestDrain_AFlushEnded|TestFlushRoute_|TestLaunchSessionEnd_|TestSessionEnd_|TestContract|TestMCPContract|TestSessionLineage|TestObservePrompt_)' ./internal/daemon`: ok 24.6s

### Criterion changes

- Strengthened: the three admin.shutdown rows run Run under a context with no deadline. Every row that waits on Run uses runTestConfig, which puts the idle exit out of reach (IdleExitSeconds = math.MaxInt32). Rationale: a deadline on the context, or the default 30-minute idle exit under a long -timeout, ends Run through Stop and satisfies the wait whatever the row's named cause did (wave 22 verifier, finding 1).
- TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries runs every requested pass drainKick asks for. The passes are counted (at most lines+1), never timed, and every delivery must still find the gate held. The gate rows (gateAtDispatch) and all drain_memo_test.go rows (memoDrainConfig) deliver without drainLineDeadline (withoutLineDeadline, now shared with settleReplay); pass budgets a row sets still apply. Rationale: their subjects are the capture gate and memo content, and the pass budget and line deadline turned a co-loaded host into a red (finding 2).
- joinReplyWork, the phases row's join and flushAsyncAwait join with no clock (context.Background()), and compactPhasesJoinBound is removed. A hang is left to go test -timeout. Rationale: a grace that ran out cancelled the very work the rows then assert on (finding 3).

### Open issues

- Finding 2 class, known issue. Many one-shot drain rows still drain a real dispatch under drainLineDeadline (5 s), and a few run under a pass budget. A host stalled past 5 s inside one delivery cancels that line, and the row then fails on a publication or content assertion. Signature: 'fixture: ... published ...' or 'context deadline exceeded' at the drain call. Files: daemon_test, delivery_crash, delivery_order, delivery_order_lanes, delivery_order_live, delivery_publication, delivery_terminal_integration, drain_acknowledged_copy, drain_admission, drain_control_line, drain_durable, drain_live_wal, drain_pid_reuse, drain_prompt_capture, drain_recovery, drain_reused_lease, drain_stale_progress, flush_arrival_order, flush_async, observer_atomic_publication, observer_durable_publication, prompt_warning_delivery, scheduler_tap_redelivery, session_abandon_revive and spool_watch(_pass_progress). Rows whose subject is the deadline or the budget keep them by design: TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress, drain_pass_progress, the spool_watch budget rows and TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound. Release-notes sentence: 'Some internal/daemon drain rows still apply the drain's 5 s per-line deadline to a real delivery, so a host stalled for more than 5 s inside one delivery can fail them on a publication assertion; the product behaviour is correct.'
- flushAsyncDaemon's cleanup still joins session ends under liveOrderBound. It asserts nothing, so it was left as it is. The cleanup-only stopPromptRecordings(grace) joins in delivery_order_* and drain_* were left for the same reason.
- Carried from the previous result: the fixed wall-clock verdicts that remain because the bound is the verdict (TestObservePromptBlockingVariantTimesOut, TestObservePromptRepliesWithinDeadline, the idle-exit rows at daemon_test.go with 12 s inside 15 s, dispatchWithin/drainWithin at compactTestBound, flushOrderReplyBound, startupReplyBound, spawn_lock and scheduler best-time rows), plus about 76 require.Eventually polls whose per-site semantics need review.
- Carried: the comment on drain.go cleanupAcknowledged ('incomplete/unreadable input cannot authorize collection') is slightly stale. It lies outside this seat's drain.go scope (scanPendingBlobs only).
- Carried: #38/#83 deferred-known-issue. The drain pass-cost row stays at 300/600 publications and costs about 35-60 s per daemon run.

### Needs owner

- Ratify the criterion changes below. The withoutLineDeadline extension to the gate rows and the drain_memo rows is the same move as the settleReplay change already awaiting ratification.
- The closed history is rewritten on closeout/w22-rows, so every SHA differs from the previous result. Integrate from ae872f03.

## review:rows:r2: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/daemon/*_test.go: the drain rows the seat lists in open_issues (daemon_test, delivery_crash, delivery_order*, delivery_publication, delivery_terminal_integration, drain_acknowledged_copy, drain_admission, drain_control_line, drain_durable, drain_live_wal, drain_pid_reuse, drain_prompt_capture, drain_recovery, drain_reused_lease, drain_stale_progress, flush_arrival_order, flush_async, observer_*_publication, prompt_warning_delivery, scheduler_tap_redelivery, session_abandon_revive, spool_watch*)`: The #64 class is closed only for some rows. The class is a content row that sends a real dispatch under the 5 s drainLineDeadline (drain.go:1900) or under a pass budget. The seat lifted it from the settle replays, the two gate rows and the drain_memo rows. It deferred the rest as a known issue. Under the seat's brief, a deferral is allowed only for a minor that cannot be closed in the seat's own files. Every remaining row is in internal/daemon/*_test.go, which the seat owns, and the shared withoutLineDeadline helper already exists. So D66(b), 'fix the class', is not fully met. This is not a regression against 2bf29705: base behaves the same. The risk is low, since only a stall longer than 5 s inside one delivery trips it. Under D66(c) and (d) it does not block.
  - Evidence: The seat's own open_issues lists these rows as still exposed, with the signature 'fixture: ... published ...' or 'context deadline exceeded' at the drain call. `grep -c drainConfig() internal/daemon/*_test.go` finds 23 test files that build drainers, and only precompact_settle*, publication_yield (gateAtDispatch) and drain_memo wrap Dispatch in withoutLineDeadline. drain.go:1900 has `dctx, cancel := context.WithTimeout(lineCtx, drainLineDeadline)`. The class has gone red on this host before: #64 failed once in a full run where a 0.6 s row took 11.73 s.
  - Fix: Either wrap Dispatch with withoutLineDeadline in the shared drain fixtures (laneTestDaemon/drainConfig users) for every row whose subject is not the deadline or budget, keeping it on TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress, drain_pass_progress, the spool_watch budget rows and TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound. Or have the coordinator record the residual explicitly under D66(d) with the seat's release-notes sentence.
- **nit** `verifier environment (not a seat defect): C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/{head,base}`: Recorded for transparency. While verifying, I extracted git archives of ae872f03 and 2bf29705 over scratchpad/head and scratchpad/base. I had not realised this scratchpad is shared with other workflow agents, and those two directories already held another agent's tree (for example rehydrate_hostpaths_w22_test.go, dated 15:25). Tracked files in them now carry this seat's HEAD and base contents. Separately, I could not re-run the Linux gate: the Docker engine was not running (npipe dockerDesktopLinuxEngine not found), and the brief forbids starting it. The Linux result therefore rests on the seat's artifacts, whose identity.txt shows commit=ae872f03, race=1, non-root uid 10001, go_test_exit=0, pass=1793, fail=0.
  - Evidence: `ls --time-style=full-iso scratchpad/head/internal/daemon` shows files stamped 18:06:01, the HEAD commit time, next to 15:25 files from another seat. The gate log is at C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rows-verify-r2/gate.log: 'failed to connect to the docker API ... exit=1'. My own verification ran in a fresh directory, scratchpad/vw22rows-r2.
  - Fix: An agent that used scratchpad/head or scratchpad/base should re-export its tree before trusting it. The coordinator should re-run the Linux gate on ae872f03 the next time the container is up, or accept the seat's artifacts.

## fix:rows:r2: status `done`, head `62b81753`

### Summary

I fixed the fix-round-2 finding (the #64 line-deadline class was closed for only some rows) at class level in one commit, 62b81753, on closeout/w22-rows. The worktree is C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-rows. The branch now has 11 commits on base 2bf29705, the tree is clean and nothing is pushed. Earlier commits were not rewritten, so their SHAs are the same as in the previous result. Only internal/daemon/*_test.go files changed; no product code.

The Linux non-root gate did not run. The Docker engine is down: the npipe dockerDesktopLinuxEngine pipe is missing, so the container is unreachable. Under the brief I did not start the engine. GOOS=linux go vet ./internal/daemon is clean. The gate still needs to run on 62b81753 once the owner's engine is back.

What changed (new file internal/daemon/drain_line_deadline_test.go):
- **Two helpers.** Every drainer a test builds over the daemon's own dispatch now goes through one of them:
  - contentDrainConfig: for rows about what a pass publishes, holds, retires or remembers. Each line is dispatched without drainLineDeadline.
  - lineDeadlineDrainConfig: the product's dispatch unchanged, for rows whose subject is the line deadline or a pass budget. A lifted deadline would hide a product that turned the budget into a line deadline, which is what those rows must catch.
- **Bare dispatches wrapped.** Every bare `Dispatch: dd.drainDispatch` or `dd.runIngested` is now wrapped in withoutLineDeadline. That covers the shared fixtures newObservingDaemonWithResult, spD3Drainer (also used by spD3PidReuseDaemon and tappedDaemon), newRealObserverDaemon and newIdentityRecordingDaemon. That reaches delivery_crash, drain_admission, drain_acknowledged_copy, drain_pid_reuse and scheduler_tap without touching each row.
- **Custom dispatch closures.** Three closures that called the product dispatch directly now call the chosen config's dispatch: the scheduler tap row, TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed and the cold-backlog row.
- **Helpers merged.** memoDrainConfig is folded into contentDrainConfig, and withoutLineDeadline moved out of precompact_settle_test.go into the new file.
- **Rows that keep the product deadline:** TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain, _ARequestedPassFinishesALineSlowerThanItsBudget, _ARequestedPassAsksAgainOnlyWhileItMakesProgress; all of drain_pass_progress and spool_watch_pass_progress; spoolWatchBackoffRow, spoolWatchSlowDrain and TestDrainClientSpools_ABudgetedPassWhoseSyncsOutlastItsBudgetStillConsumesALine; TestStop_IsNotHeldBehindASessionEndsDrain (its subject is the deadline against the grace); TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound; TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare.
- **withoutLineDeadline refined.** It used to strip every cancellation (context.WithoutCancel). Applied to shared fixtures, that would have let a line outlive Stop or a row's cancel. It now lifts deadlines only and passes on any other cancellation with its cause, the way the product cancels a line.

Red first:
- New guard TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline parses the package's test files. On the previous head it FAILED, listing 100 offending sites; on HEAD it passes.
- New TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations, run against the previous WithoutCancel body, hung in its cancellation subtest until -timeout=2m (the red). On HEAD it passes.
- Behaviour check: an overlay of drain.go sleeps 5.5 s before each drainer's first dispatch, after the line's deadline has started. I ran all 214 content rows of the 30 affected files in one process per tree.
  - Previous tree: 42 failed, with the verifier's signatures ('context deadline exceeded' at the drain call, 'fixture: ... published', 'Condition never satisfied').
  - HEAD: 212 passed, 2 failed, both explained below.

Remaining (in open_issues):
- TestRedrainOnFirstServedRequest drains through the drainer that Run installs (daemon.go:702), which these files cannot reach.
- TestDrainCanceledRejectedHandlerLeavesCurrentLinePending fails only because the overlay stalls inside the drain itself; its dispatch is an instant fake. That is outside the class, which is real I/O inside a dispatch.

Evidence (overlays, binaries, logs) is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/rows-fix2. I started no containers. Every process I started has exited. The three daemon.test.exe processes still running belong to other seats, so I left them alone.

### Commits

- 62b81753 test(daemon): make every drain row choose its line deadline (new, fix round 2)
- ae872f03 test(daemon): join reply work and session ends with no clock (unchanged)
- fee5baed test(daemon): lift pass budget and line deadline off gate rows (unchanged)
- fcc66219 test(daemon): let only the row's own cause end Run (unchanged)
- 149c73d2 test(daemon): pin the memo's per-file cap across an early stop (unchanged)
- dd281507 test(daemon): bound the other channel waits by hangGuard (unchanged)
- ac4e6523 fix(daemon): let the blob scan skip a trailing partial line (unchanged)
- 7fc33969 test(daemon): pin three drain preconditions mutants survived (unchanged)
- 3a22827d test(daemon): lift the line deadline from settle row replays (unchanged)
- e95578f4 test(daemon): make the fixed 10 s waits pure hang guards (unchanged)
- 8e6b2eb3 test(daemon): count the passive prompt seam after joining it (unchanged)

### Findings resolution

- **fixed**: minor: #64 class (a content row draining a real dispatch under the 5 s drainLineDeadline or a pass budget) closed only for some rows; the rest were deferred though they are in the seat's own files
  - 62b81753. Every test drainer over the daemon's dispatch now goes through contentDrainConfig (no line deadline) or lineDeadlineDrainConfig (product dispatch, for deadline and budget rows). Bare drainDispatch and runIngested dispatches are wrapped in withoutLineDeadline, and the shared fixtures are converted. withoutLineDeadline now lifts deadlines but keeps every cancellation. Red first: the new guard listed 100 sites on the previous head and passes on HEAD; the helper row hangs under the old body. Under a 5.5 s first-dispatch stall, 42 of 214 content rows failed on the previous tree and 2 failed on HEAD. Those 2 are outside these files' reach or outside the class; see open_issues.

### Tests

- `go test -p 2 -count=1 -timeout=300s -run '^TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline$' ./internal/daemon (before converting the sites)`: FAIL: 100 offending sites listed (bare drainDispatch/runIngested, direct drainConfig() calls, closures calling the product dispatch)
- `go test -p 2 -count=1 -timeout=120s -run '^(TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations|TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline)$' ./internal/daemon, with the previous WithoutCancel body`: FAIL: panic: test timed out after 2m0s in TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations/a_cancellation_ends_the_dispatch_with_its_cause
- `QPROBE_DISPATCH_MS=5500 stallpd_prev.test.exe (drain.go overlay: 5.5 s before each drainer's first dispatch; previous tree's test files) -test.run <214 content rows of the 30 affected files>`: FAIL: 172 pass, 42 fail ('context deadline exceeded', 'fixture: ... published', 'Condition never satisfied')
- `QPROBE_DISPATCH_MS=5500 stallpd_new.test.exe (same overlay, HEAD 62b81753) -test.run <same 214 rows>`: 212 pass, 2 fail: TestRedrainOnFirstServedRequest (drainer installed by Run) and TestDrainCanceledRejectedHandlerLeavesCurrentLinePending (stall inside the drain with an instant fake dispatch)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows, the committed tree)`: ok 764.7s
- `go test -p 2 -count=20 -timeout=30m -run '^(TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations|TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline)$' ./internal/daemon`: ok 2.99s
- `go test -race -p 2 -count=3 -timeout=30m -run '^(TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations|TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline)$' ./internal/daemon`: ok 3.10s
- `GOOS={windows,linux,darwin} go vet ./internal/daemon`: clean on all three
- `go run ./tools/devtool fmt-check`: clean (exit 0)
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs/...`: ok 25.3s
- `sh .../linux-nonroot-gate.sh --repo <seat worktree> --out .../wave22/linux-rows-fix2 62b81753 w22-rows-fix2 --gomaxprocs 2 --timeout 60m -- ./internal/daemon`: NOT RUN: Docker engine down ('failed to connect to the docker API at npipe:////./pipe/dockerDesktopLinuxEngine'); I did not start it, per the brief

### Criterion changes

- Every content drain row in internal/daemon now dispatches its lines without drainLineDeadline: all drainers built through contentDrainConfig or wrapped in withoutLineDeadline. Rows whose subject is the line deadline or a pass budget keep the product dispatch through lineDeadlineDrainConfig (listed in the summary). Rationale: a 5 s wall-clock limit on real I/O turned a stalled host into a red content row (audit 2 #64, fix round 2). Lifting it hides nothing those rows assert, because pass budgets live in the pass context's value and are untouched, and the deadline/budget rows keep the real dispatch. A new guard row enforces the choice for every future drainer.
- withoutLineDeadline now lifts deadlines only and passes on every other cancellation with its cause (it was context.WithoutCancel). This applies to the settle replays, the gate rows and the drain_memo rows that already used it. Rationale: with the helper in shared fixtures, stripping cancellation would let a line outlive Stop, a test's cancel or a settle's cut, which the product never does. Keeping cancellations makes those rows match base 2bf29705 in every respect but the deadline.

### Open issues

- Linux non-root gate not run on 62b81753: the owner's Docker engine is down and the seat may not start it. Re-run linux-nonroot-gate.sh on 62b81753 for ./internal/daemon once the engine is back. GOOS=linux go vet is clean.
- Residual of the class, deferred-known-issue: rows that drain through the drainer Run installs (daemon.go:702, outside this seat's files) still run real dispatches under drainLineDeadline. Examples: TestRedrainOnFirstServedRequest, TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun, TestRun_LogsATakenOverLockAndASpoolReplay, the startup_accept row. They poll with require.Eventually, so a stalled line is retried within their bound. Release-notes sentence: 'A few internal/daemon rows that drain through a running daemon still apply the drain's 5 s per-line deadline to a real delivery, so a host stalled for more than 5 s inside one delivery can fail them; the product behaviour is correct.'
- TestDrainCanceledRejectedHandlerLeavesCurrentLinePending fails under the stall overlay only because the overlay sleeps inside the drain's own code past the line's deadline; its dispatch is an instant fake with no I/O. Outside the class; nothing to change.
- Carried from the previous result: fixed wall-clock verdicts where the bound is the verdict (TestObservePromptBlockingVariantTimesOut, TestObservePromptRepliesWithinDeadline, idle-exit rows, dispatchWithin/drainWithin, flushOrderReplyBound, startupReplyBound, spawn_lock and scheduler best-time rows), plus about 76 require.Eventually polls still to review site by site; the slightly stale comment on drain.go cleanupAcknowledged (outside the seat's drain.go scope); #38/#83 deferred (drain pass-cost row stays at 300/600 publications, about 35-60 s per run); cleanup-only grace joins (flushAsyncDaemon, stopPromptRecordings) left as they are.

### Needs owner

- Ratify the criterion change below (it extends the settleReplay/gate/memo change already awaiting ratification to every content drain row).
- Run the Linux non-root gate on 62b81753 once the Docker engine is up. Integrate from 62b81753; the earlier 10 SHAs are unchanged.


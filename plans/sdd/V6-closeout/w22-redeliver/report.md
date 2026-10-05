# Wave 22 redeliver seat

Branch `closeout/w22-redeliver`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/observer/stop.go:150-157 (mainAgentStop, reached from onStop :141 with no observation check). Also docs/architecture.md:361-362 and internal/daemon/scheduler_tap_redelivery_test.go:7`: When a main-agent Stop delivery is replayed, the observer does not recognise it. Wave 20 made the scheduler tap apply a cut-and-replayed delivery once. On the same path (Stop's runCancel, a bounded drain, or a PreCompact settle cutting a Stop's commit), the observer's own Stop handling runs again: st.Turn++ and, when a grammar is wired, Grammar.Append(stop). captureSubagent (:192), OnToolUse (tooluse.go:121) and the prompt capture (prompt.go:175) all check observationRecord; the main-agent branch does not. Every cut Stop therefore shifts the turn of every later record by one, and the scheduler's maxTurn follows. architecture.md:361 ('The observer absorbs the replay'), the redelivery test file's header and the w20 report all say the observer absorbs replays. That is false for this branch. This is pre-existing: `git diff d20309c0 2bf29705 --stat -- internal/observer/` is empty. No observer test covers a main-agent Stop redelivery; the TestRedelivery_Stop* rows all use subagent stops.
- **minor** `internal/daemon/handlers.go:1374-1404 (handleCheckpoint phase 2 calls d.svc.PreCompact and phase 3 calls AddPrecompactWallSample on a spool replay); internal/daemon/scheduler_tap.go:276-281 (preCompact → CloseSegmentForCompaction)`: A spooled duplicate of a PreCompact the daemon already handled live is replayed in full. The hook client spools a checkpoint whose reply misses its 15 s deadline: hookclient.go:35 sets checkpointReplyDeadline, and ipc client.go:288-294 spools on a read error. checkpoint_replay_test.go is built on that premise. The live route never leases the request (only ingest.Accept leases, ingest.go:344), so the drain mints a fresh lease (drain.go:1091) and drainDispatch → dispatchOp(withSpoolReplay) runs handleCheckpoint again. Only the contract arming is replay-aware (handlers.go:1350). The seal runs a second time. The scheduler closes the post-compaction open segment with cause 'compact' when work has happened since. PrecompactWallMs gets a second sample, which feeds precompact.has_time_to_write's p99. This is pre-existing. It is not reachable unless a PreCompact reply takes more than 15 s (a stall or heavy co-load).
- **minor** `internal/daemon/scheduler_state.go:315-332 (seedApplied); scheduler_runtime.go:1048-1058 (claimDeliveryLocked); scheduler_frontier.go:120-126 (bindUnboundLocked); scheduler_state.go:388-391 (another session's doc is discarded)`: Compared with 738d67c7 this is a regression, but only in an edge case. seedApplied suppresses the startup-drain replay of every identity in state/scheduler.json, on the assumption that the later bind restores the account that holds them. bindOnFirstHook can instead bind a different session than doc.Session; restoreSchedulerLocked then discards the doc, and the skipped replay's tokens are lost. Example: two windows. The runtime was bound to S1 and folded S2's last Read into S1's persisted account. That Read's live and Stop-drain commits were both cut. After the restart, S2's next hook arrives first. S2's account then misses that Read; 738d67c7 counted it.
- **minor** `internal/daemon/scheduler_runtime.go:229-244 (schedRuntime.applied and its comment); heldObservationsLocked scheduler_runtime.go:1126-1140`: schedRuntime.applied is never pruned. No code deletes from it: `grep -rn "delete(r.applied\|r.applied = " internal/daemon/*.go` finds only the constructor. It gains one entry per session the daemon ever sees, for the life of the process. The comment says this matches 'the observer's per-session state'. It does not: the observer deletes a session's state at SessionEnd (observer/session.go:402-405), and the registry evicts ended sessions beyond maxSessions (registry.go:370-398). A daemon that never idles for 30 minutes, such as a headless `claude -p` loop in one project, grows without bound. Every Persist also walks the whole map under r.mu (heldObservationsLocked).
- **minor** `plans/V6-CLOSEOUT-CHECKLIST.md on verify/v6 (D62); internal/daemon/ingest_wal_groupcommit_test.go:239-246 (awaitClosed, 10 s ingestACKWait); plans/sdd/V6-closeout/w20-redeliver/report.md:222-235`: Items the w20-redeliver seat routed to the coordinator have no disposition in the ledger. (1) TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend failed once in a whole-package run under co-load: 'the return of acknowledgement 0 never happened' after 44.9 s, against about 9 s alone. It runs in tonight's prefreeze `./internal/...` step, which stops the night on any red. (2) A replayed compact SessionStart re-anchors lastCompactionTS and lastActivity. (3) An owed close is not persisted across a restart. (4) An owed close for another session's tool use closes the bound session's segment. D62 records only the main fix.
- **minor** `internal/daemon/scheduler_tap.go:211-217 (closeOwed); internal/daemon/scheduler_state.go:105-114 (LastAppliedObservations, no cap)`: The w20-redeliver seat routed three items to the coordinator, and none has a disposition. (1) The tap's boundary close, now the owed close, closes the BOUND session's segment even for another session's tool use, and folds that session's tokens into the bound account (pre-existing cross-session design). (2) The persisted last_applied_observations map has no cap and grows about 110 bytes per session on a long-lived account. (3) An owed close is not persisted, so a cut first run, plus a cut Stop replay, plus a replay after a bind loses the close.
- **minor** `internal/cli/sessionstart_compact_load_test.go:369-408 (requireReadsReachedTheRig); w20-status/report.md Open issues`: There is an open product question with no owner. When the compact rig's daemon stops, only 4-20 of 43-95 delivered Reads are in index/tool_use.jsonl. The rest are WAL-only, and 0-30 per run are still in a client spool. 'Nobody has established whether WAL-only at stop is expected (replayed at the next start) or a gap in ingest under same-session load.' The C5.2 night re-runs the rig and records counts, but D65(d) records and does not judge, so nothing will answer the question.
- nit `internal/daemon/scheduler_tap.go:199-210 (observeTool's record-lookup error branch)`: On a replay of a delivery the tap already applied, a record lookup that fails with something other than ErrNotFound takes the anchorUnrecorded branch. That branch returns false because d.obs == obs, counts a redelivery, and returns without calling closeOwed. If that replay's commit then lands, the boundary or changepoint close the cut run owed is never made. The recognised-replay path on line 219 does make it.
- nit `internal/daemon/scheduler_state.go:150-161 (decodeSchedulerState), :315-332 (seedApplied), scheduler_runtime.go:1145-1160 (restoreAppliedLocked)`: LastAppliedObservations is loaded into memory with no cap and no validation, unlike the turn lists (capTurns) and the contract history's applyCaps. A hand-edited or corrupt but parseable scheduler.json can therefore insert any number of entries.

## impl:redeliver: status `done`, head `a4f7a394bc4bfd1ffe7974e920f7b9ec28c85fce`

### Summary

Wave 22 redeliver seat. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver, branch closeout/w22-redeliver: five commits on base 2bf29705. Nothing pushed or merged, and the tree is clean.

What was fixed:
- **#3 (major).** A replayed main-agent Stop re-ran Turn++ and Grammar.Append. The session now keeps LastStopObs, the identity of its last applied leased Stop. It is persisted in observer.json next to Turn as the omitempty field last_stop_observation. A replay is counted in observer.redelivery_absorbed and changes nothing: no turn, no grammar symbol, no graph flush, and LastTS keeps naming the last host event.
- **#4.** A spooled copy of a PreCompact this daemon already sealed is acknowledged without a second seal, without the tap's compaction close and without a second wall sample. handleCheckpoint claims each PreCompact's nonce before sealing, keeps the newest 8 in memory under historyMu, and releases a claim whose seal failed. Two cases still seal: a copy of a failed seal, and a PreCompact this daemon never saw.
- **#5.** seedApplied is removed. A replay before the bind is now applied while unbound, and bindUnboundLocked deducts its tokens only when the account the bind actually restored names that delivery. This also fixes a related case in the same class: the seed was read before the predecessor's final persist could land.
- **#6 and D67(b).** schedRuntime.applied is a recency-bounded LRU of 256 sessions. last_applied_observations is therefore bounded too, and a load caps it and validates the ids (audit nit 2).
- **Nit 1.** A replay whose record lookup fails now still makes the segment close it owes.
- **#7 item 2.** A replayed SessionStart moves no scheduler anchor. It binds only what a session's first hook would bind, so it no longer rebinds a runtime the live session holds and no longer binds an unbound runtime to a session that is not live.
- **#72(2).** last_applied_observations now has a cap (the same fix as #6).

**#73: no loss, so refuted.** I ran the C1.16 rig's shape eight times through an uncommitted overlay probe that restarts the daemon:
- At stop, 4–11 Reads were indexed out of 23–94.
- In 7 of 8 runs, the next start replayed every Read that was WAL-only or client-spooled, and 0 were lost.
- In the other run, the first restart's startup drain answered after 20.9 s with 38 Reads still WAL-only and 1 still in a client spool. Their files were left on disk unchanged, awaiting a later pass. That run did not restart again, and the case did not recur.
- At each compaction the rehydration does miss the unindexed recent Reads. Section 7's drop report counts them all (within ±1 of in-flight hooks) and names the newest 16–17, which is the D53(c)/D55 design.

**Docs.** architecture.md's redelivery paragraph and the redelivery test header were corrected.

**Outside the listed files.** I added one field to the daemon struct in internal/daemon/daemon.go (sealedPreCompacts, next to lastStartAnswer). I also added two new files, internal/daemon/precompact_duplicate.go and internal/daemon/checkpoint_spooled_duplicate_test.go.

**Checks.**
- All green: GOOS windows, linux and darwin vet; golangci-lint; fmt-check; test/docs; docmarkers and runpatterns.
- Full Windows runs pass: observer, and daemon at HEAD.
- New rows pass -count=20 and -race -count=3 on Windows.
- Linux non-root, observer: passes.
- Linux non-root, daemon:
  - Run whole, the package timed out at 30m under -race with gomaxprocs 2 (1465 passed, 0 failed before the timeout).
  - Split into two halves, it showed 4 timing-sensitive failures. Each passes -count=3 when run alone at the same HEAD, and none exercises code this seat changed. Details are under open_issues.

Evidence is under C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/redeliver-evidence and .../wave22/linux-redeliver.

### Commits

- 3ab4fca769db8fc04e8814fbc307826e3662b305 fix(observer): absorb a replayed main-agent stop by observation
- 78e1be7b1c78b479c08e0fb0771eb5d6acd8bb23 fix(daemon): dedupe unbound replays at bind, bound applied ids
- 81c9c8ab4f607e28ad433dbd6d5b53797e123734 fix(daemon): skip the seal for a spooled copy of a sealed precompact
- 4421750c879149203a5a08a00966958f7746dfc6 fix(daemon): let a replayed session start move no scheduler anchor
- a4f7a394bc4bfd1ffe7974e920f7b9ec28c85fce fix(daemon): evict an empty-session entry from the applied lru too

### Findings resolution

- **fixed**: #3 (major): a replayed main-agent Stop re-runs the observer's Turn++ and Grammar.Append
  - Commit 3ab4fca7. Red-first rows on base 2bf29705:
- TestRedelivery_MainAgentStopIsAbsorbed (internal/observer): same-process and restart-with-persisted-turn subtests; both red, expected 1 got 2.
- TestIngest_AStopThatCutsItsOwnCommitAdvancesTheTurnOnce (internal/daemon): real worker and drain, the probe-E shape; red, expected 1 got 2.

Fix: sessionState.LastStopObs is persisted next to Turn as last_stop_observation (omitempty, stateVersion stays 1). mainAgentStop counts a recognized replay as absorbed and returns before the grammar, Turn++, the graph flush and LastTS. A Stop with no identity is never absorbed and does not clear the recorded identity.

Class sweep:
- OnToolUse, OnUserPrompt and SubagentStop were already guarded by observationRecord.
- OnSessionStart (observer side) is idempotent: frontier max and ensureSegment adopt.
- A replayed OnSessionEnd re-creates and deletes an empty state. That leaves observer.json in the same state an idle persist after a normal SessionEnd would, so it is harmless.
- The daemon's ObserveStop wrappers (observer_ops, orderAfterCompactBookkeeping) are pass-through.
- The tap's applyStop was already guarded.

architecture.md:361 and the test header in scheduler_tap_redelivery_test.go now say how a Stop is recognized.
- **fixed**: #4: a spooled duplicate of a live-handled PreCompact is replayed in full
  - Commit 81c9c8ab. Red-first row TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain (new file internal/daemon/checkpoint_spooled_duplicate_test.go). Its first subtest was red on base: expected 1 seal, got 2. Two control subtests pin that a never-seen PreCompact and a copy of a failed seal still seal.

Fix: handleCheckpoint claims the nonce in phase 1 under historyMu. A deferred release drops the claim unless the seal returned nil. A spool replay of a claimed nonce skips phase 2 (the seal, and with it the tap's CloseSegmentForCompaction) and phase 3 (the wall sample), and answers OK so the frontier is reached. Phase 1's contract arming and the marker are left as they were: the arming is already replay-aware, and the marker compares session ids only.

Why in memory rather than in history.json: adding a field to history would mean editing internal/contract, which is not my file. Persisting would also be wrong after a crash, which may have cut the seal short, so a later daemon re-seals.

New helpers live in internal/daemon/precompact_duplicate.go. The one field is in daemon.go.

Class sweep:
- Flush is leased, so the drain already recognizes its nonce.
- The prompt reply path is already replay-aware.
- session.start belongs to the contract seat.
- **fixed**: #5: seedApplied loses tokens when the bind restores a different session
  - Commit 78e1be7b. Red-first rows on base:
- TestWrapServices_AReplayBeforeABindToAnotherSessionCountsForIt: expected 400 got 100; expected 450 got 150.
- TestWrapServices_AReplayIsDedupedAgainstTheAccountTheBindReads: expected 1000 got 1700. This is the probe-A shape: the seed was read at construction, before the predecessor's final persist.

Fix: seedApplied and its call are removed. applyToolUse records each session's first fold while unbound (unboundFolds, bounded). restoreSchedulerLocked keeps the restored identities (restoredApplied). bindUnboundLocked deducts a fold only when the restored account names the same observation; when the document belonged to another session it was discarded and nothing is deducted.

Only a session's first unbound delivery can be the one the account holds: the account names that session's last applied delivery, and the ordering gate holds every later one behind it.

The existing rows still pass: TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain and TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain, both before-bind and after-SessionStart subtests. TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook (internal/cli) passed -count=3.
- **fixed**: #6 and D67(b): schedRuntime.applied is never pruned; bound last_applied_observations
  - Commits 78e1be7b and a4f7a394. Red-first row TestWrapServices_TheAppliedIdentitiesAreBounded was red on base (all 300 sessions kept).

Fix: maxAppliedSessions = 256. stampAppliedLocked stamps recency on every new delivery and evicts the least recently applied entry. It is used by claimDeliveryLocked, anchorUnrecorded and restoreAppliedLocked (new entries only, in session order). In-place updates keep their stamp: oweLocked, releaseAccountLocked and holdLocked.

heldObservationsLocked is a subset of applied, so the persisted map is bounded at 256 too. a4f7a394 replaced the empty-session-id sentinel in the eviction scan.

The applied comment is corrected: it no longer says the map matches the observer's per-session state for the daemon's lifetime.

Cost of an eviction: only recognition of a replay of that session's last delivery is lost, which needs 256 other sessions to deliver in between. An unrecognized replay is applied again, as before wave 20. A dropped owed close falls under the accepted residual.
- **fixed**: #7 item 2 and #72 per D67(b): a replayed compact SessionStart re-anchors lastCompactionTS
  - Commit 4421750c. It is reachable: startCompactAnswer runs d.svc.SessionStart for a spoolReplay request too, and the tap then ran BindSession at the replay's instant.

Red-first row TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing was red on base in all three subtests:
- the anchors moved by 60 s;
- a replayed start of another session rebound the runtime;
- a replayed start of a session that is not live bound an unbound runtime.

Fix: tap.sessionStart(ctx, e) on spoolReplay(ctx) only calls bindOnFirstHook (unbound and live) and returns. No lastCompactionTS and no NotifyActivity at the replay's instant.

For a compaction the daemon never saw live, the last compaction stays earlier than the host's, which can only make the Young–Daly clause fire sooner.

Left unchanged on purpose: a replayed PreCompact on an unbound runtime still binds through CloseSegmentForCompaction, because that close must come before the seal.
- **fixed**: #72 item 2: the persisted last_applied_observations map has no cap
  - Commit 78e1be7b. The writer is bounded at 256 through the LRU (#6). decodeSchedulerState applies capApplied: it keeps a named session and a well-formed observation id (a canonical, non-zero core hash), at most 256 entries in session order.

Red-first row TestStateCodec_AppliedIdentitiesAreBoundedAndValidatedOnLoad was red on base. It also covers audit nit 2.
- **deferred-known-issue**: #7 item 4 and #72 item 1: a tap's owed close for another session's tool use closes the bound session's segment
  - Accepted per D67(b). Not changed.

Sentence for docs/cannot-do.md, for the docs seat: "The scheduler keeps one token account per project, bound to one session, and folds every session's tool uses into it, so a task-boundary or changepoint segment close that another session's tool use calls for (including the close a replay of that delivery still owes) closes the bound session's open segment and counts the other session's tokens in the bound session's account; Qompack keeps no separate scheduler account per concurrent session."
- **deferred-known-issue**: #7 item 3 and #72 item 3: an owed close is not persisted across a restart
  - Accepted residual per D67(b). Not changed.

Release-notes sentence: "A segment close owed by a delivery whose first run was cut is held in memory only, so if that run, its Stop-drain replay and a replay after a restart's bind are all cut, the close is never made and the span stays in the following segment, which is still encoded at a coarser boundary."
- **refuted**: #73 (D67(d)): Reads WAL-only at stop, and whether a compaction's rehydration misses recent Reads
  - No loss.

The probe is uncommitted; internal/cli is not my file. It runs the rig's real composition: bootstrapDaemon, the shipped hooks through Dispatch, compaction cycles under 2 workers of 64 KiB Reads. It stops, then restarts with bootstrapDaemon and a served admin.ping, which waits out the startup drain. Probe and logs: .../wave22/redeliver-evidence/f73-probe.

At stop, Reads were indexed / WAL-only / client-spooled as follows:
- 4/40/17 of 61, all replayed by the next start in 54 s;
- 10/68/16 of 94, all replayed in 99 s;
- 10/37/5 of 52, all replayed in 80 s;
- 7/4/12 of 23, all replayed in 17 s;
- 5/6/20 of 31, all replayed in 41 s;
- 11/31/10 of 52, all replayed in 40 s;
- 9/14/20 of 43, all replayed in 43 s.

The exception was run 3 (11/55/1 of 67). Its first restart's startup drain answered after 20.9 s with 38 still WAL-only and 1 still in a client spool. The WAL (10,218,828 bytes) and the spool were left unchanged on disk for a later pass. That run did not restart again, and the case did not recur in 7 later runs.

Ingest rate under this load: observer.tooluse p50 is about 0.6–0.7 s per 64 KiB Read (max 5.8 s on replay), so replay runs at about 0.5–1.4 Reads/s. The restarted daemon's first served request waits for the whole startup drain (17–99 s).

Rehydration: per compaction the block misses the Reads not yet indexed. Section 7's unreplayed drop entry counts them: for example 41 not indexed against 40 tool results counted, and 70 against 71 (in-flight hooks account for the ±1). It names the newest 16–17 by tool_use_id. This is the D53(c)/D55 design: counted, never silently missing.

Recording this in the ledger, and any c116-rig flag, is for the coordinator and the rows seat.
- **fixed**: nit: observeTool's record-lookup error branch skips the owed close on a replay
  - Commit 78e1be7b. anchorUnrecorded now returns (owed, noted). On a replay of an applied delivery (d.obs == obs) it returns d.owed, and observeTool calls closeOwed.

Red-first row TestWrapServices_AReplayWhoseRecordLookupFailsStillMakesTheOwedClose (fakeStore.toolUseErr set on the replay) was red on base: the segment stayed open.
- **fixed**: nit: LastAppliedObservations loaded with no cap and no validation
  - Commit 78e1be7b. capApplied runs in decodeSchedulerState and wellFormedObservation validates the ids. Row: TestStateCodec_AppliedIdentitiesAreBoundedAndValidatedOnLoad, red on base.

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver && go test -p 2 -count=1 -run '^TestRedelivery_MainAgentStopIsAbsorbed$' ./internal/observer/ (on base 2bf29705 with the row added)`: FAIL as intended: both subtests expected 1 got 2. Evidence: redeliver-evidence/f3-observer-red-base.txt
- `go test -p 2 -count=1 -run '^TestIngest_AStopThatCutsItsOwnCommitAdvancesTheTurnOnce$' ./internal/daemon/ (on base with the row added)`: FAIL as intended: expected 1 got 2 (f3-daemon-red-base.txt)
- `go test -p 2 -count=1 -run '^(TestWrapServices_AReplayBeforeABindToAnotherSessionCountsForIt|TestWrapServices_TheAppliedIdentitiesAreBounded|TestWrapServices_AReplayWhoseRecordLookupFailsStillMakesTheOwedClose|TestStateCodec_AppliedIdentitiesAreBoundedAndValidatedOnLoad)$' ./internal/daemon/ (on base with the rows added)`: FAIL as intended: all four red (f5-f6-nits-red-base.txt)
- `go test -p 2 -count=1 -run '^TestWrapServices_AReplayIsDedupedAgainstTheAccountTheBindReads$' ./internal/daemon/ (with the fix's four source files stashed)`: FAIL as intended: expected 1000 got 1700 (f5-construction-seed-red-base.txt)
- `go test -p 2 -count=1 -run '^TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain$' ./internal/daemon/ (on base with the row added)`: FAIL as intended: the duplicate sealed twice; the two control subtests passed (f4-red-base.txt)
- `go test -p 2 -count=1 -run '^TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing$' ./internal/daemon/ (on base with the row added)`: FAIL as intended: all three subtests red (f7-red-base.txt)
- `GOOS={windows,linux,darwin} go vet ./internal/observer/ ./internal/daemon/`: pass on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS (the first attempt hit another seat's 'parallel golangci-lint is running' lock; the retry passed)
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/observer/...`: ok (222.9 s), observertest ok. Observer is unchanged since 3ab4fca7, so this covers HEAD.
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/... (at HEAD a4f7a394)`: ok (1266.9 s); an earlier run at 4421750c was also ok (921.2 s)
- `go test -p 2 -count=20 -run '^TestRedelivery_MainAgentStopIsAbsorbed$' ./internal/observer/`: ok
- `go test -p 2 -count=20 -run '^(TestIngest_AStopThatCutsItsOwnCommitAdvancesTheTurnOnce|TestWrapServices_AReplayBeforeABindToAnotherSessionCountsForIt|TestWrapServices_AReplayIsDedupedAgainstTheAccountTheBindReads|TestWrapServices_TheAppliedIdentitiesAreBounded|TestWrapServices_AReplayWhoseRecordLookupFailsStillMakesTheOwedClose|TestStateCodec_AppliedIdentitiesAreBoundedAndValidatedOnLoad|TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain|TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing)$' ./internal/daemon/`: ok (84.4 s)
- `CGO_ENABLED=1 go test -race -p 2 -count=3 with the same two -run patterns, on ./internal/observer/ and ./internal/daemon/`: ok and ok <!-- runpatterns: prose: -run is followed by a description of the patterns given on the lines above, not by a pattern -->
- `go test -p 2 -count=3 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-redeliver a4f7a394 w22-redeliver --gomaxprocs 2 -- ./internal/observer ./internal/daemon`: observer PASS (583/0/0). daemon FAIL: 'panic: test timed out after 30m0s' with 1465 passed and 0 failed. Running at the timeout: TestDeliveryOrder_ArchiveReadsNeverRunAheadOfASettlement and TestDeliveryRollover_ArchiveRecordsExactlyWhatTheBatchCommitsRecord. Paused parallel tests never ran.
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-redeliver a4f7a394 w22-redeliver-daemon-nondelivery --gomaxprocs 2 --skip '^TestDelivery' -- ./internal/daemon`: 992 pass, 3 fail, 1 skip. Failures:
- TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing: context deadline exceeded.
- TestIngestRingFullWALsEveryLineAndNeverSpills: 748 ms, not less than 500 ms.
- TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries: the 2 s soft pass budget ended the pass before the second spool.
Every new row passed.
- `sh .../linux-nonroot-gate.sh ... a4f7a394 w22-redeliver-rerun3 --gomaxprocs 2 --count 3 --run '^(TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing|TestIngestRingFullWALsEveryLineAndNeverSpills|TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries)$' -- ./internal/daemon`: PASS 9/9
- `sh .../linux-nonroot-gate.sh ... a4f7a394 w22-redeliver-daemon-delivery --gomaxprocs 2 --run '^TestDelivery' -- ./internal/daemon`: 801 pass, 1 fail: TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing (delivery_order_lanes_test.go:429, 'Should be zero, but was 1'). The row times its fixture with real time.NewTimer delays.
- `sh .../linux-nonroot-gate.sh ... a4f7a394 w22-redeliver-rerun-flush --gomaxprocs 2 --count 3 --run '^TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing$' -- ./internal/daemon`: PASS 3/3
- `go test -p 2 -count=1 -timeout=10m -overlay <scratch>/probe73/overlay.json -run '^TestProbeC116_WALOnlyReadsAtStopAndTheNextStart$' -v ./internal/cli/ (the probe is uncommitted; 8 runs including -count=3 and QOMPACK_C116_ROUNDS=5)`: No Read the daemon received was lost in any run. In 7 of 8, the next start indexed every WAL-only or client-spooled Read. In run 3, 39 stayed pending on disk with their files unchanged (see #73). <!-- runpatterns: a scratch probe run through -overlay from an uncommitted test file; it is not committed, and its result is recorded on this line -->

### Criterion changes

- No existing assertion, golden or wall-clock margin was changed. Two documentation claims changed. docs/architecture.md's redelivery paragraph now says how the observer recognizes a replayed main-agent Stop. It also says the tap remembers the 256 most recently applied sessions and persists only those, and that a replay made before the bind is deducted only when the restored account holds it. The header of internal/daemon/scheduler_tap_redelivery_test.go says the same. Previously both claimed the observer absorbed every replay and that every session's identity was persisted.
- Behaviour change versus base 2bf29705: an unbound runtime no longer counts a startup-drain replay of a persisted delivery under sched.tap.redelivery. That replay is now applied and then deducted at the bind. No test or doc asserted the old count.

### Open issues

- Linux daemon gate. Run whole, ./internal/daemon under -race with --gomaxprocs 2 does not finish inside the 30m per-binary timeout on this shared machine. It was split by --skip/--run '^TestDelivery'. Across the two halves, four timing-sensitive rows failed: TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing, TestIngestRingFullWALsEveryLineAndNeverSpills, TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries and TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing. Each passes -count=3 when run alone at the same HEAD, and none exercises code this seat changed. They were not compared against base under the same load. D67(n)'s merged-wave Linux gate will meet the same 30m limit unless it splits the package too.
- #73 measurements for the ledger (D67(d)): replay is confirmed, but 1 of 8 runs showed a startup drain answering after 20.9 s with 39 Reads still pending on disk. Ingest runs at about 0.6–0.7 s per 64 KiB Read (observer.tooluse p50), and the restarted daemon serves nothing until its startup drain finishes (17–99 s with 16–84 Reads queued). This is a throughput property, not a loss. Making c116-rig flag a low indexed fraction is for the rows/cliwork seats.
- This seat edited internal/daemon/daemon.go, which is outside its listed files: one struct field, sealedPreCompacts, beside lastStartAnswer. The integrator should watch for a textual conflict with the contract seat in that struct.
- Left unchanged on purpose: a replayed PreCompact on an unbound runtime still binds through CloseSegmentForCompaction, so the compacted span closes before the replayed seal. This is the same not-live-binding question #7 raised for SessionStart, but changing it would cost the restarted daemon's seal its span.
- Finding #7 item 1 (awaitClosed as a pure hang guard) was not assigned to this seat and was not touched.

## review:redeliver:r1: verdict `needs-fixes`, 5 finding(s)

- **minor** `internal/daemon/scheduler_tap.go:186-194 (schedTap.sessionStart, spoolReplay branch, commit 4421750c); route side: internal/daemon/handlers.go handleSessionStart, d.registry.Ensure(ev, now)`: The binding rule for a replayed SessionStart is wrong in two ways when the request goes through the real route. (a) It regresses against 2bf29705. Say the runtime is still bound to a session that has ended (Close keeps the binding and calls DisablePSelection), and the live session's start was spooled because its connect failed. On base, the replay rebinds the runtime to the live session. At HEAD it never does: bindOnFirstHook only binds an unbound runtime. The live session therefore runs with p-selection disabled, and its tool uses fold into the ended session's account, until its next compaction. (b) The seat claims a replayed start of a session that is not live 'no longer binds an unbound runtime', and that claim fails in production. Before the tap runs, handleSessionStart calls registry.Ensure for the replay, which sets Live=true; it even revives a session that End marked ended. bindOnFirstHook's IsLive check therefore always passes for the replayed start's own session. The subtest 'a start on an unbound runtime' in TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing calls the tap seam directly and skips the route, so it does not model this. The anchor part (no lastCompactionTS or lastActivity move) is correct.
  - Evidence: Uncommitted overlay probes (scratchpad/vw22r-probes/zz_verify_probe_test.go and zz_verify_probe2_test.go). Each uses tappedDaemon with r.d = dd and sends ipc.OpSessionStart through dd.dispatchOp(withSpoolReplay(ctx), req). Probe 2: a live start of sess-a, registry.End(sess-a), Touch(sess-b), then a replayed start of sess-b. HEAD a4f7a394 logs 'bound="sess-a-ended"'; a fresh git-archive of 2bf29705 logs 'bound="sess-b-live"'. Probe 1: an unbound runtime and a replayed start of a never-seen session, and of an ended one. On both HEAD and base it logs 'live-after=true' and 'runtime bound to "sess-stale-start"', and the test fails with 'Should be empty, but was sess-stale-start'.
  - Fix: Decide liveness from the registry as it stood before the route's Ensure. Either the route reports whether the session was already live, or a replay registers with Touch semantics, which keep an ended session ended; this route part belongs to the contract seat. Then, on a replay, bind or rebind when the replayed session was live and the runtime is unbound or bound to a session that is no longer live, still moving no anchor. Add a row that goes through dispatchOp(withSpoolReplay(ctx), ...) rather than the seam alone, covering both the ended-bound/live-replayed case and the never-seen case.
- **minor** `docs/architecture.md:205`: After 81c9c8ab, the doc still says 'A replayed `PreCompact` still seals its checkpoint'. That is no longer true for a spooled copy of a PreCompact this daemon already sealed: it is acknowledged without a seal, without the tap's compaction close and without a wall sample. The seat corrected only the :361 paragraph and did not route this sentence to the docs seat. test/docs does not catch it.
  - Evidence: grep -n 'replayed `PreCompact` still seals' docs/architecture.md returns line 205. The behaviour is in handlers.go handleCheckpoint: duplicate := spoolReplay(ctx) && d.sealClaimedLocked(req.Nonce), followed by an early return of OK. It is pinned by TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain/a_copy_of_a_sealed_PreCompact.
  - Fix: Hand the docs seat a one-line correction, for example: 'A replayed PreCompact seals its checkpoint unless it is a hook's spooled copy of one this daemon already sealed, which is acknowledged without a second seal.'
- **nit** `internal/daemon/handlers.go handleCheckpoint (duplicate/claimed logic) and internal/daemon/precompact_duplicate.go`: A copy that arrives while its original's seal is still running counts as a duplicate and is acknowledged at once. If that live seal then fails, the deferred release drops the claim, but the copy has already been committed, so nothing retries the seal. Base would have sealed the copy. Reaching this needs three things: the live seal still running past the client's 15 s deadline, a drain picking up the copy in that window, and the live seal then failing.
  - Evidence: The claim is taken in phase 1 and released by the defer only after phase 2 returns non-nil. The duplicate branch returns ipc.Response{OK: true} without regard to whether the claim is still in flight. The seat's own comment treats 'a copy that arrives while the seal it copies is still running' as a duplicate.
  - Fix: Separate in-flight claims from sealed ones. For a copy of an in-flight claim, return not-OK so the drain leaves the line for a later pass, and acknowledge it only once the claim is marked sealed. A released claim then lets the copy seal.
- **nit** `commits 81c9c8ab, 4421750c, a4f7a394 (subject lines)`: Three commit subjects are longer than 64 characters as whole lines: 68 ('fix(daemon): skip the seal for a spooled copy of a sealed precompact'), 66 and 66. The repo's own checker (tools/devtool/checkcommitmsg.go:21) applies the 64 limit only to the text after 'type(scope): ', so the hook accepted them, and 42 of the last 400 subjects on base are also over 64. It is a violation only if the brief's '64 characters or fewer' means the whole subject line.
  - Evidence: git log --format=%s 2bf29705..HEAD gives lengths 66, 66, 68, 62, 63. None of the commits has a Co-Authored-By or Claude-Session trailer, and each has 'Refs: V6-VERIFY, C2.3'.
  - Fix: If the whole-line reading applies, reword the three subjects to 64 characters or fewer, for example 'fix(daemon): skip resealing a spooled copy of a sealed precompact'.
- **nit** `internal/observer/identity.go:30-31 (counterRedelivery doc comment)`: The comment says observer.redelivery_absorbed counts 'a redelivery of a publication this observation already made ... no second record, no second blob'. Since 3ab4fca7, it also counts an absorbed main-agent Stop, which has no publication or record.
  - Evidence: stop.go mainAgentStop now calls o.count(counterRedelivery) when obs == st.LastStopObs; that path writes nothing.
  - Fix: Widen the comment to cover a main-agent Stop recognized by the session's last applied Stop identity.

## fix:redeliver:r1: status `partial`, head `9b6563249efd60c7d36bdb97e3b701a4fa712c0c`

### Summary

Wave 22 seat redeliver, fix round 1. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver, branch closeout/w22-redeliver. Three commits on top of a4f7a394 (base 2bf29705). Nothing pushed or merged, and the tree is clean.

**Both verifier findings are fixed.** Status is partial for one reason: the Linux non-root gate could not run. The owner's Docker Desktop is stopped (the docker-desktop WSL distro shows Stopped and the engine pipe is missing), so the container qompack-v6-linux-verification cannot be reached, and the rules forbid starting the engine. Every Windows check passes, and the Linux and darwin test binaries compile.

**Finding 1: a replayed SessionStart bound by the wrong rule.**
- The route (handleSessionStart) now reads whether the session was live before its own registry.Ensure and puts that on the context (withLiveBeforeStart). It sits next to the existing existedBefore read. This is three lines in a contract-seat region.
- The tap's replayed-start path now goes through the new bindOnReplayedStart. It binds or rebinds only when the session was live before Ensure, and only a runtime that is unbound or bound to a session the registry no longer holds live. It moves no anchor.
- On a4f7a394 the new row fails three ways: no rebind from an ended session, a never-seen session binds, and an ended session binds. On 2bf29705 it fails on the not-live binds, the rebind away from a live session and the activity anchor. It passes at HEAD.
- The verifier's own two probes now pass: the ended-bound case ends bound to sess-b-live, and the not-live cases bind nothing.

**Finding 2: architecture.md still said a replayed PreCompact always seals.** I corrected the sentence myself (00889f2a) rather than leave it to a relay: the docs seat has not touched architecture.md, and the behaviour is handleCheckpoint's, which is my code.

**One part of the class is not closed, and it belongs to the route.** The route's Ensure still marks a replayed start's session live, including an ended or never-seen one. So that session's next replayed delivery binds an unbound runtime through bindOnFirstHook. An uncommitted probe confirmed it: after a stale replayed start the runtime stayed unbound, then the stale session's next tool use bound it. This needs a registry and route change, which is the contract seat's. Details are under open_issues.

Evidence is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/redeliver-evidence/fr1.

### Commits

- 4881428c fix(daemon): bind a replayed start by its pre-ensure liveness
- 00889f2a docs(architecture): a spooled sealed precompact is not resealed
- 9b656324 docs(daemon): name the route residual of a replayed start bind

### Findings resolution

- **fixed**: minor: a replayed SessionStart's binding rule is wrong through the real route (scheduler_tap.go sessionStart spoolReplay branch; handleSessionStart's registry.Ensure)
  - Commits 4881428c and 9b656324 (comment only).

Red-first row: TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive in internal/daemon/scheduler_tap_redelivery_test.go. It drives the route through dd.dispatchOp(withSpoolReplay(ctx), ...) with tappedDaemon and the runtime attached to the daemon, and it joins compact reply work with dd.promptWG.Wait(), so it has no wall-clock margin. Subtests:
- the live session after the bound one ended (the bound session's end runs registry.End and then the tapped SessionEnd seam);
- a live session on an unbound runtime, for a startup source and a compact source;
- another live session while the bound one is live;
- a never-seen or an ended session on an unbound runtime;
- a never-seen session on a runtime bound to an ended one.

Red on a4f7a394 in three subtests (route-row-red-head-a4f7a394.txt):
- the runtime stayed bound to sess-ended instead of rebinding to sess-live;
- a never-seen session bound the unbound runtime;
- an ended session bound the unbound runtime.

Red on 2bf29705 in seven subtests (route-row-on-base-2bf29705.txt):
- the three not-live binds;
- the rebind away from a live session;
- the activity anchor moved to the replay's instant in the three binding cases.

Fix:
- handleSessionStart reads d.registry.IsLive before Ensure and passes it on (ctx = withLiveBeforeStart(ctx, ...)). The value survives startCompactAnswer's reply-work goroutines, because startReplyWork keeps ctx's values.
- schedTap.sessionStart on a replay calls bindOnReplayedStart(sess, &e, liveBeforeStart(ctx, sess)). It does nothing in three cases: a session already bound, a session not live before Ensure (counted as sched.tap.bind.not_live when unbound), and a runtime held by a session that is still live.
- Otherwise it binds an unbound runtime through bindUnboundLocked, which keeps what was observed while unbound and dedupes against the restored account. A runtime bound to a session that is no longer live is rebound through bindSessionLocked. Either way the start's model and subagent hints are read, the new counter sched.tap.bind.replayed_start is incremented, and no anchor moves: no compaction re-anchor, no NotifyActivity.
- bindUnboundLocked now takes the binding event; its two existing callers pass nil.
- sessionLive(d, sess) is now shared with bindOnFirstHook.
- Without the route's report (the seam called directly), liveBeforeStart falls back to the registry's current answer. The existing seam row passes unchanged.

Why the endSession path is safe: it runs SessionEnd (Persist and Close) before its own drain. So the rebind that drain makes when it replays the live session's spooled start comes after the Close and re-enables p-selection.

Class sweep:
- bindOnFirstHook (tool use, Stop, captured prompt) keeps the base rule: an unbound runtime only. I did not extend it to rebind away from an ended session on a live hook. Between handleFlush's registry.End and endSession's SessionEnd seam, such a rebind would drop the ended session's final persist, and the Close that follows would then disable p-selection on the new binding. This behaviour is unchanged from base.
- CloseSegmentForCompaction is unchanged: it binds an unbound runtime and counts compact_foreign otherwise.
- The remaining route-level residual is under open_issues.
- **fixed**: minor: docs/architecture.md:205 still says a replayed PreCompact always seals its checkpoint
  - Commit 00889f2a. The sentence now reads:

"A replayed `PreCompact` re-arms that obligation only if no `SessionStart` of the session has arrived since the hook fired. It seals its checkpoint unless it is a hook's spooled copy of one this daemon already sealed, which is acknowledged without a second seal, without the scheduler's compaction close and without a second wall-time sample."

This matches handleCheckpoint: phase 1's replay-aware arming still runs, then the duplicate returns before the seal and before phase 3's AddPrecompactWallSample. TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain pins the behaviour.

No red row, because a prose sentence cannot have one and test/docs does not cover it. I made the edit in this seat for two reasons: the docs seat's branch has no change to docs/architecture.md (git diff 2bf29705 closeout/w22-docs is empty for it), so no conflict is expected, and this seat cannot hand text to that seat. If the docs seat would rather own the sentence, the text above is the exact replacement.

Doc sweep: troubleshooting.md:374 speaks only of re-arming, which is still accurate. No other doc claims that a replayed PreCompact always seals.

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver && go test -p 2 -count=1 -run '^TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive$' ./internal/daemon/ (row added to a git-archive of a4f7a394)`: FAIL as intended in 3 subtests: the live session after the bound one ended (expected sess-live, got sess-ended); a never-seen session on an unbound runtime; an ended session on an unbound runtime. Saved as fr1/route-row-red-head-a4f7a394.txt.
- `go test -p 2 -count=1 -run '^TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive$' ./internal/daemon/ (row added to a git-archive of 2bf29705)`: FAIL as intended in 7 subtests: the three not-live binds; the rebind away from a live session (expected sess-c1, got sess-live); the activity anchor in the three binding cases. Saved as fr1/route-row-on-base-2bf29705.txt.
- `go test -p 2 -count=1 -run '^(TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive|TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing)$' ./internal/daemon/`: ok at 4881428c
- `go test -p 2 -count=20 -run '^(TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive|TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing)$' ./internal/daemon/`: ok (27.1 s)
- `CGO_ENABLED=1 go test -race -p 2 -count=3 -run '^(TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive|TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing)$' ./internal/daemon/`: ok (5.3 s)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/...`: ok (587.7 s). Run at 00889f2a; 9b656324 changes only a comment.
- `go test -p 2 -count=3 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok
- `go test -p 2 -count=1 -v -run '^(TestVerifyProbe_ReplayedStartThroughTheRoute|TestVerifyProbe2_ReplayedStartOfTheLiveSessionAfterTheBoundOneEnded)$' ./internal/daemon/ (the verifier's two probes copied in for the run, then removed)`: PASS. Probe 2 ends bound to sess-b-live. Probe 1 binds nothing in both the never-seen and ended cases, though live-after is still true (the route residual).
- `GOOS={windows,linux,darwin} go vet ./internal/daemon/`: exit 0 on all three, at HEAD
- `GOOS={linux,darwin} GOARCH=amd64 go test -c ./internal/daemon/`: both test binaries compile
- `go run ./tools/devtool lint --only=golangci-lint`: PASS at HEAD
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `sh C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-redeliver 9b656324 <label> --gomaxprocs 2 -- ./internal/daemon`: NOT RUN. docker ps fails: the dockerDesktopLinuxEngine pipe is missing and the docker-desktop WSL distro is Stopped. The owner's engine is down, and the seat may not start it. Recheck before returning: still down.

### Criterion changes

- No assertion, golden or wall-clock margin was changed or removed. The seam row TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing passes as it stood.
- Behaviour versus a4f7a394: a replayed SessionStart of a session that was live before the route's Ensure now rebinds a runtime bound to a session the registry no longer holds live. That restores base 2bf29705's rebind, without base's NotifyActivity at the replay's instant.
- Behaviour versus base 2bf29705:
- A replayed SessionStart of a session that was not live before the route's Ensure (ended or never seen) no longer binds an unbound runtime and no longer rebinds a bound one.
- A replayed start never takes the runtime from a session that is still live.
- A replayed bind of an unbound runtime keeps what the runtime observed while unbound (bindUnboundLocked) instead of resetting it.
- A replayed start notes no activity.
- One counter is added: sched.tap.bind.replayed_start. sched.tap.bind.not_live now also counts a replayed start that found the runtime unbound and named a session that was not live. No test or doc listed these counters.
- Doc change: docs/architecture.md section 1's replay paragraph no longer says a replayed PreCompact always seals. It now names the spooled copy of an already-sealed PreCompact, which is acknowledged without a seal, the scheduler's compaction close or a wall-time sample.

### Open issues

- Linux non-root gate not run for this round. The owner's Docker Desktop engine is stopped, so the container qompack-v6-linux-verification cannot be reached, and the rules forbid starting the engine. It is owed on HEAD 9b656324 for ./internal/daemon. Split it as in the first round (--skip '^TestDelivery' and --run '^TestDelivery'), because the whole package under -race with --gomaxprocs 2 exceeds the 30m per-binary timeout. D67(n)'s merged-wave Linux gate covers the same package.
- Route-level residual of finding 1, for the contract seat. handleSessionStart's registry.Ensure marks a replayed start's session live even when it had ended or was never seen. The tap's own decision for the start is now correct. But that session's next replayed delivery finds it live and binds an unbound runtime through bindOnFirstHook (uncommitted probe: after a stale replayed start the runtime stayed unbound, then the stale session's next tool use bound it). The same false liveness also delays idle exit until EndAbandoned, and it is what the replayed start's contract run sees as SessionLive. The fix belongs in the route and registry.go: a replay registers its session without marking it live unless it already was. A never-seen session should be registered not live but revivable by a live Touch, and an ended session should stay ended. Release-notes sentence if this is not closed: "A SessionStart replayed from a hook's spool marks its session live in the daemon's registry even if that session has ended, so a later replayed delivery of the ended session can bind the scheduler to it until the live session's next SessionStart, and the daemon's idle exit waits for that session's silence timeout."
- Integration overlap: 4881428c adds three lines to handleSessionStart, a contract-seat region, beside `_, existedBefore := d.registry.Get(...)` at about line 981. The contract seat's branch edits handleSessionStart only at its StartTS line (about 1022) and handleCheckpoint's arming, so the hunks are separate, but the integrator should check. This is in addition to the first round's daemon.go struct field (sealedPreCompacts).
- The architecture.md:205 correction (00889f2a) was made in this seat, not by the docs seat. If the docs seat also rewrites that sentence, keep one version.
- Sibling left unchanged on purpose. A live hook of a live session while the runtime is bound to an ended session still leaves the binding alone, as on base: bindOnFirstHook binds only an unbound runtime. Rebinding on a hook would drop the ended session's final persist in the window between handleFlush's registry.End and endSession's SessionEnd seam, and that seam's Close would then disable p-selection on the new binding.
- Carried from the first round, unchanged:
- the #73 throughput measurements for the ledger;
- a replayed PreCompact on an unbound runtime still binds through CloseSegmentForCompaction, kept so the seal gets its span;
- finding #7 item 1 (awaitClosed) was not this seat's.

## review:redeliver:r2: verdict `needs-fixes`, 6 finding(s)

- **minor** `internal/daemon/handlers.go:1376-1395 (handleCheckpoint duplicate/claim), internal/daemon/precompact_duplicate.go; docs/architecture.md:205-208`: Finding #4's claim treats a nonce as already sealed as soon as the live route claims it, before the seal has finished. The hook spools a PreCompact only when the live reply misses its 15 s deadline, which means the live seal may still be running when a drain replays the copy. That copy is acknowledged as a duplicate and consumed. If the live seal then fails, releaseSealLocked drops the claim, but no copy is left to retry, so the compaction gets no checkpoint at all. Base 2bf29705 sealed it through the copy. This is a regression against base. The comment says "a copy of the request retries it", and architecture.md says only a copy of one "already sealed" is skipped. Both are false for this interleaving. It needs a double fault: a route slower than 15 s, then a failed seal.
  - Evidence: Overlay probe C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/r2/zz_verify_r2_test.go (TestVerifyR2_ACopyAckedDuringALiveSealThatFails). The seam blocks on its first call, the spooled copy is drained while it blocks, then the live seal returns an error. No wall-clock waits. Run at HEAD 9b656324 with -overlay: 'seam calls=1 successful seals=0', FAIL. Run on a git-archive of 2bf29705: 'seam calls=2 successful seals=1', PASS.
  - Fix: Distinguish a claim that is sealing from one that has sealed. Only a nonce whose seal succeeded should count as a duplicate. A copy that arrives while its seal is still in flight should either seal (as base did) or wait, bounded, for the outcome without holding the drain's mutex. Alternatively, the live route's failure path can re-run the seal when a copy was absorbed during it. Add a red-first row with a blocking seam, shaped like the probe. Correct the comment and the architecture.md sentence.
- **minor** `internal/daemon/scheduler_runtime.go:504-520 (bindOnReplayedStart: the cur == sess early return and the !wasLive case); internal/daemon/handlers.go:983 (withLiveBeforeStart)`: The liveness rule regresses the resume case documented in bindSessionLocked: 'a same-id rebind after Close is the --resume of a session whose SessionEnd already ran while this daemon stayed up'. SessionEnd runs registry.End, and Touch revives only an abandoned session, not one ended this way. So when a resumed session's SessionStart reaches the daemon only as a drain replay (its dial failed), IsLive is false before Ensure. If the runtime is still bound to that session, cur == sess returns at once: p-selection stays disabled from the end's Close, and the start's model and subagent hints are never recorded or re-resolved. If the runtime is bound to another ended session, the resumed session is not rebound and runs on the ended session's account. Base's BindSession re-enabled p-selection and rebound in both cases. Because this start was fired after the session ended, it is not a stale leftover, which is the case the rule exists to refuse. The effect is on scheduling only, and lasts until the session's next live SessionStart.
  - Evidence: Overlay probe C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/r2/zz_verify_r2b_test.go (TestVerifyR2b_AReplayedResumeStartOfTheBoundSessionReenablesPSelection). It runs through the real route: tappedDaemon, a live start, then registry.End plus svc.SessionEnd, then dispatchOp(withSpoolReplay) of a source=resume start. At HEAD: 'bound="sess-resumed" pselection=false live=true', FAIL. On a git-archive of 2bf29705: 'pselection=true', PASS. Code: registry.go Touch revives only when !s.Live && s.abandoned.
  - Fix: Treat a replayed start as the live session's start when its hook time (req.TS) is after the session's registry EndedTS, or when the session was live before Ensure. The registry already records EndedTS. In the cur == sess branch with such a start, run the same-id path without the compact anchor: noteBindingEventLocked, resolveRegimeLocked and EnablePSelection. A start fired before the end should stay refused. Add a route row for both the bound-to-self and the bound-to-another-ended cases. Re-check the route-residual release-notes sentence against this case.
- **minor** `internal/daemon (HEAD 9b656324; code delta 4881428c in scheduler_runtime.go, scheduler_tap.go, handlers.go)`: The Linux non-root gate has not run on HEAD. The last Linux evidence is the round-1 verifier's run on a4f7a394 (linux-redeliver-verify). 4881428c changes daemon code and adds a route row after that, so this round has no Linux evidence. I could not run it either: the docker-desktop WSL distro is Stopped and the dockerDesktopLinuxEngine pipe is missing, and the rules forbid starting the engine.
  - Evidence: docker ps: 'open //./pipe/dockerDesktopLinuxEngine: The system cannot find the file specified'. wsl -l -v lists only docker-desktop, Stopped. Windows checks at HEAD that passed: go test -p 2 -count=1 -timeout=30m ./internal/daemon/... ok (679 s); ./internal/observer/... ok; new rows -race -count=3 ok.
  - Fix: When the owner's engine is up, run linux-nonroot-gate.sh on 9b656324 for ./internal/daemon and ./internal/observer, split as in round 1 (--skip '^TestDelivery' and --run '^TestDelivery'). Otherwise carry it explicitly to D67(n)'s merged-wave Linux gate.
- **nit** `commits a4f7a394, 4421750c, 81c9c8ab`: Three subjects are longer than the brief's 64-character limit: 'fix(daemon): evict an empty-session entry from the applied lru too' (66), 'fix(daemon): let a replayed session start move no scheduler anchor' (66), 'fix(daemon): skip the seal for a spooled copy of a sealed precompact' (68). The commit hook counts only the text after 'type(scope): ' (tools/devtool/checkcommitmsg.go subjectRE allows .{1,64}), so they passed it. None has attribution trailers, and all carry 'Refs: V6-VERIFY, C2.3'.
  - Evidence: git log --format=%s 2bf29705..HEAD, measured with ${#s}.
  - Fix: If the coordinator holds to the brief's whole-subject rule, reword the three subjects on the seat's own branch before integration. Rewording rewrites the SHAs quoted in the evidence. Otherwise record that the hook's free-text rule is the one applied.
- **nit** `internal/daemon/scheduler_tap_redelivery_test.go:795-800 (TestWrapServices_TheAppliedIdentitiesAreBounded)`: The assertion 'last_applied_observations is bounded too' reads the persisted file through decodeSchedulerState. That function now trims to 256 on load (capApplied), so the check passes whatever Persist wrote. The code is correct, because the in-memory bound is asserted directly, but the assertion about the persisted file proves nothing.
  - Evidence: decodeSchedulerState calls capApplied (scheduler_state.go:162). The test asserts require.Len(doc.LastAppliedObservations, tapAppliedBound) on the decoded doc.
  - Fix: Unmarshal the raw scheduler.json into schedulerStateDoc with json.Unmarshal, not decodeSchedulerState, before asserting the length.
- **nit** `internal/daemon/scheduler_tap.go:182-190 (sessionStart comment)`: The comment says a compaction the daemon never saw live 'can only make the Young–Daly clause fire sooner, never later'. But when a replayed compact start binds an unbound runtime, or rebinds one, and no document of that session is restored, bindSessionLocked sets lastCompactionTS to sessionStartTS, which is the replay's instant. That is later than the host's compaction. This matches base and every first-hook bind, so it is not a regression, only an overclaim.
  - Evidence: scheduler_runtime.go bindSessionLocked: r.sessionStartTS = r.nowMS(); ... if r.lastCompactionTS == 0 { r.lastCompactionTS = r.sessionStartTS }. The route row's 'a live session on an unbound runtime, compact' case binds and does not check lastCompactionTS.
  - Fix: Either anchor a replayed compact start's bind at hookTime(req), or narrow the comment to the same-id path.

## fix:redeliver:r2: status `partial`, head `3018ab70484516d4aa2ee181ecc27b92877b24c4`

### Summary

Wave 22 seat redeliver, fix round 2. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver, branch closeout/w22-redeliver. Two commits sit on top of 9b656324 (base 2bf29705). Nothing is pushed or merged, and the tree is clean.

**Both code findings are fixed, red-first, at the class level.** The status is partial for one reason only: the Linux non-root gate (finding 3) still cannot run. The owner's Docker engine is still stopped. `docker ps` reports that the dockerDesktopLinuxEngine pipe is missing, and `wsl -l -v` lists docker-desktop as Stopped. The rules forbid starting the engine. Every Windows check passes, and the linux and darwin daemon test binaries compile.

**Finding 1 (a PreCompact copy was consumed during a live seal that then failed).**
- The route used to claim a nonce before sealing. It now records a nonce only after its seal succeeds, in phase 3 (`noteSealedLocked`). A copy that reaches a drain while its seal is running seals as well, as on base, so no copy is consumed before a seal of its PreCompact has succeeded.
- I did not make the copy wait for the running seal. The drain that replays the copy holds the drain's mutex, and the running seal's settle drains client spools itself (`replayOwnSpools`), so waiting there could stall that seal.
- The comment and the architecture.md sentence are corrected.

**Finding 2 (a replayed resume SessionStart was refused).**
- The route now reports, before its Ensure and NoteStart, whether the start is the latest one of its session's current life (new `SessionRegistry.CurrentAt`). That holds when the session was live, or had ended no later than the start's hook time, and no later start of it had been handled.
- Such a start reopens a runtime still bound to its own session (`reopenBoundLocked`: model and subagent hints, regime, p-selection), without the compaction anchor. Otherwise it binds or rebinds as in round 1.
- A start fired before its session's end stays refused. So does one superseded by a later start of the same session, so stale hints never overwrite newer ones.

**Both verifier probes now pass:**
- r2: `seam calls=2 successful seals=1`.
- r2b: `bound="sess-resumed" pselection=true`.

Evidence is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/redeliver-evidence/fr2.

### Commits

- 1d7d3ce1 fix(daemon): skip a precompact copy only after a seal succeeds
- 3018ab70 fix(daemon): bind a replayed start fired after its session ended

### Findings resolution

- **fixed**: minor: finding #4's claim treats a nonce as sealed as soon as the live route claims it. A spooled copy drained during a live seal that then fails is consumed with no retry (handlers.go handleCheckpoint, precompact_duplicate.go, docs/architecture.md:205-208).
  - Commit 1d7d3ce1.

**Red-first row:** TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded in internal/daemon/checkpoint_spooled_duplicate_test.go. It has the probe's shape and no clock:
- The sealRig seam's first call closes heldAt and blocks on a held channel.
- The live checkpoint runs on a goroutine. The spooled copy is drained while the seam blocks.
- Right after that drain, the row requires one successful seal. The copy was consumed while the live seal was still running, so it must have sealed itself.
- Two subtests: the live seal then fails, which then requires exactly one successful seal (the copy's), and the live seal then succeeds.
- A t.Cleanup releases the held seal and joins the live route if the row fails early.

**Results:**
- At 9b656324 it fails in both subtests with 'expected: 1, actual: 0' (fr2/f1-row-red-head-9b656324.txt).
- On a git-archive of 2bf29705 it passes in both (fr2/rows-on-base-2bf29705.txt). This confirms the regression was against base.
- It passes at HEAD. The verifier's probe passes too: 'seam calls=2 successful seals=1'.

**Fix:**
- precompact_duplicate.go: claimSealLocked, releaseSealLocked and sealClaimedLocked are replaced by noteSealedLocked and sealedLocked. A nonce is recorded only when its seal succeeds, deduplicated, still bounded at 8.
- handleCheckpoint: phase 1 only asks sealedLocked. Phase 3 calls noteSealedLocked(req.Nonce) when sealed. The deferred release closure is gone, since nothing is recorded before success.
- The comment says why the copy cannot wait for the running seal: the drain replaying it holds the drain's mutex, and the running seal's settle calls DrainClientSpoolsWithin.
- The daemon.go field comment now says 'whose seal has succeeded'.
- docs/architecture.md:205 now reads: 'It seals its checkpoint unless it is a hook's spooled copy of one whose seal has already succeeded in this daemon, which is acknowledged without a second seal, without the scheduler's compaction close and without a second wall-time sample. A copy that reaches a drain while its seal is still running is sealed as well, so a seal that then fails still leaves the compaction a checkpoint.'

**Class sweep against base 2bf29705.** The only remaining difference from base is that a copy arriving after a successful seal is skipped. The following all seal exactly as on base:
- a copy arriving during the live seal;
- a copy after a failed seal;
- a copy after a panic;
- a copy of a PreCompact whose live route had no seam or a mode that may not act;
- a copy of a PreCompact this daemon never saw;
- any copy after a restart (the record is in memory only).

The other at-least-once dedupes in this seat need no change, because each claims together with its effect: the observer's Stop identity is kept with Turn, and the tap's applied ObservationIDs are claimed in the same critical section as the fold. Hot-path copies still in flight are already deferred by the drain (seenSet, deferInFlight).
- **fixed**: minor: the liveness rule regresses the resume case. A replayed SessionStart of a session SessionEnd ended (registry.End; Touch revives only abandoned sessions) left p-selection off on a runtime bound to it, and did not rebind a runtime bound to another ended session (scheduler_runtime.go bindOnReplayedStart; handlers.go withLiveBeforeStart).
  - Commit 3018ab70.

**Red-first row:** TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume in internal/daemon/scheduler_tap_redelivery_test.go. It goes through the real route: tappedDaemon with r.d = dd, a live start, registry.End and then svc.SessionEnd, then dispatchOp(withSpoolReplay) and promptWG.Wait(). Hook times are set explicitly, with no wall-clock margin. Subtests:
- the resumed session holds the runtime (start fired in the end's own millisecond): bound, p-selection on, resume model hint read, anchors unchanged;
- another ended session holds the runtime: rebound to the resumed session with p-selection on;
- the runtime is unbound: binds, no activity noted;
- guard: a start fired before the end changes nothing;
- guard: a start a later live start of the session superseded does not overwrite the newer hints;
- guard: a start fired before the end does not take the runtime while another ended session holds it.

**Results:**
- At 9b656324 the three resume subtests fail: p-selection stays false; 'sess-other-ended' instead of 'sess-resumed'; '' instead of 'sess-resumed' (fr2/f2-rows-red-head-9b656324.txt).
- On a git-archive of 2bf29705 the bind and p-selection parts pass, as the verifier said. The row still fails there on the activity anchor and on the leftover guards, which round 1 fixed.
- The superseded guard is meaningful: with the `lastStart > at` line removed it fails, so I restored it.
- The verifier's probe passes at HEAD: 'bound="sess-resumed" pselection=true'.

**Fix:**
- New SessionRegistry.CurrentAt(id, at) in registry.go. It returns false for an unknown session. For an unknown at (zero) it returns Live. It returns false when a later start was handled (lastStart > at). Otherwise it returns Live || EndedTS <= at. It reads under one RLock.
- handleSessionStart reads it before Ensure and NoteStart, with req.TS, as withCurrentStart. This replaces round 1's withLiveBeforeStart line, so the contract-seat region still has only this one changed line plus its comment.
- The tap's liveBeforeStart becomes currentStart. Without the route it still falls back to sessionLive.
- bindOnReplayedStart(sess, e, current):
  - !current refuses, counting not_live when the runtime is unbound;
  - a runtime held by another live session is refused;
  - cur == sess goes to the new reopenBoundLocked (noteBindingEventLocked, resolveRegimeLocked, EnablePSelection, no anchor);
  - an unbound runtime goes to bindUnboundLocked;
  - otherwise the runtime is rebound through bindSessionLocked.
- bindSessionLocked's same-id branch now calls reopenBoundLocked, then sets the live compact anchor. Live behaviour is unchanged.

**Class sweep against base:**
- A replayed start that is the latest of its session's current life binds or rebinds as base did, and reopens a runtime bound to itself as base's same-id path did, minus base's compact anchor and its NotifyActivity at the replay's instant.
- It never takes a runtime from another live session. Base did.
- A start fired before its session's end, a never-seen session, and a superseded start bind nothing. Base bound all three and overwrote hints.
- bindOnFirstHook and CloseSegmentForCompaction are unchanged.

**Route-residual release-notes sentence, re-checked against this case.** A resumed start's registration marking its session live is now correct, so the sentence narrows to: "A SessionStart replayed from a hook's spool that the host fired before its session ended, or that names a session this daemon has not seen, still marks that session live in the daemon's registry, so a later replayed delivery of that session can bind an unbound scheduler to it until the live session's next SessionStart, and the daemon's idle exit waits for that session's silence timeout."
- **deferred-known-issue**: minor: the Linux non-root gate has not run on HEAD; the last Linux evidence is a4f7a394.
  - Not run. Rechecked just before returning:
- `docker ps`: 'open //./pipe/dockerDesktopLinuxEngine: The system cannot find the file specified'.
- `wsl -l -v`: docker-desktop is Stopped.

The rules forbid starting the owner's engine. What I could do:
- GOOS=linux and GOOS=darwin go vet ./internal/daemon/: exit 0.
- GOOS=linux and GOOS=darwin GOARCH=amd64 go test -c ./internal/daemon/: both compile.

It is carried explicitly to D67(n)'s merged-wave Linux gate. When the engine is up, run linux-nonroot-gate.sh on 3018ab70 for ./internal/daemon, split into --skip '^TestDelivery' and --run '^TestDelivery' as in round 1. ./internal/observer is unchanged since a4f7a394, so it can ride the merged-wave gate as well.

Release-notes sentence if the merged-wave gate does not cover it: "Wave 22's redelivery fixes to the daemon (the PreCompact copy record and the replayed-start bind) were verified on Windows only; their Linux non-root run is carried by the merged-wave Linux gate."

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-redeliver && go test -p 2 -count=1 -run '^TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded$' ./internal/daemon/ (at 9b656324, before the fix)`: FAIL as intended in both subtests: 'expected: 1, actual: 0' with the message 'the copy is consumed while the live seal is still running, so it must have sealed itself'. Saved as fr2/f1-row-red-head-9b656324.txt.
- `go test -p 2 -count=1 -run '^(TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume|TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive)$' ./internal/daemon/ (at 9b656324, before the fix)`: FAIL as intended in 3 subtests: the resumed session holds the runtime (p-selection false); another ended session holds the runtime (got sess-other-ended); the runtime is unbound (got ''). The guards and the existing row pass. Saved as fr2/f2-rows-red-head-9b656324.txt.
- `go test -p 2 -count=1 -v -run '^(TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume|TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded)$' ./internal/daemon/ (rows added to a git-archive of 2bf29705)`: The PreCompact row PASSES on base, confirming the regression. In the resume row, the rebind subtest passes; the reopen subtest fails only on the activity anchor (p-selection and binding hold, as the verifier said). The unbound subtest fails only on the activity anchor (it binds), and the leftover guards fail, as round 1 found. Saved as fr2/rows-on-base-2bf29705.txt.
- `go test -p 2 -count=1 -v -run '^(TestVerifyR2_ACopyAckedDuringALiveSealThatFails|TestVerifyR2b_AReplayedResumeStartOfTheBoundSessionReenablesPSelection)$' ./internal/daemon/ (the verifier's probes copied in at HEAD, then removed)`: PASS: 'seam calls=2 successful seals=1' and 'bound="sess-resumed" pselection=true live=true'. Saved as fr2/verifier-probes-after-fix.txt.
- `go test -p 2 -count=1 -run '^TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume$' ./internal/daemon/ (with the `case s.lastStart > at` line removed from CurrentAt; restored afterwards)`: FAIL in 'a start a later live start of the session superseded', so the guard is meaningful.
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/...`: ok (490.8 s) at 3018ab70. Saved as fr2/win-daemon-full-3018ab70.txt.
- `go test -p 2 -count=20 -run '^(TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded|TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain|TestSessionStartRoute_AReplayedStartFiredAfterItsSessionEndedIsAResume|TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive|TestWrapServices_AReplayedSessionStartMovesNoAnchorAndRebindsNothing)$' ./internal/daemon/`: ok (122.5 s)
- `CGO_ENABLED=1 go test -race -p 2 -count=3 -run (same pattern) ./internal/daemon/`: ok (22.1 s) <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=3 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok
- `GOOS={windows,linux,darwin} go vet ./internal/daemon/`: exit 0 on all three
- `GOOS={linux,darwin} GOARCH=amd64 go test -c ./internal/daemon/`: both test binaries compile
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `sh C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh ... 3018ab70 ... -- ./internal/daemon`: NOT RUN: the owner's Docker engine is stopped (dockerDesktopLinuxEngine pipe missing; docker-desktop WSL distro Stopped). Carried to D67(n).

### Criterion changes

- One fixture correction in an existing row. In TestSessionStartRoute_AReplayedStartBindsOnlyASessionThatWasLive, the subtest 'an ended session on an unbound runtime' is renamed 'an ended session's start fired before its end, on an unbound runtime', and its replayed start's hook time is set to 1 ms before the registry end (req.TS = now - 1). Rationale: under the verifier's rule, a start fired at or after its session's end is that session's resume and must bind. The old fixture's real-clock TS was at or after the end, so it was really a resume, not the leftover the subtest names. The assertion (binds nothing) is unchanged, and the resume case now has its own rows that require binding. No other assertion, golden or margin changed.
- Test-rig change, not an assertion: in checkpoint_spooled_duplicate_test.go the sealRig seam gains held/heldAt channels, failFirst and a sealedOK counter. With these unset, existing rows behave exactly as before.
- Behaviour change against round 1 (9b656324):
- A spooled PreCompact copy drained while its live seal runs is now sealed rather than acknowledged unsealed.
- A replayed SessionStart fired no earlier than its session's registry end now reopens a runtime bound to that session (hints, regime, p-selection, no anchor), or binds or rebinds as round 1 did for a live session.
- A replayed start superseded by a later handled start of its session now binds nothing.
- Behaviour change against base 2bf29705 for the replayed start, beyond round 1's: a superseded start no longer overwrites the newer start's model and subagent hints, and a reopen sets no compaction anchor and notes no activity.
- The meanings of counters sched.tap.bind.not_live and sched.tap.bind.replayed_start are restated (the latest start of the session's current life; replayed_start also counts reopens). No test or doc lists them.

### Open issues

- Linux non-root gate is owed on 3018ab70 for ./internal/daemon, split into --skip '^TestDelivery' and --run '^TestDelivery'. It is carried to D67(n)'s merged-wave Linux gate. ./internal/observer is unchanged since a4f7a394.
- Accepted consequence of finding 1's fix, which matches base 2bf29705 behaviour. A spooled PreCompact copy drained while its live seal is still running is sealed too, so if both succeed that compaction gets two checkpoints and two wall samples. Release-notes sentence if wanted: "A PreCompact whose reply missed the hook's 15 s deadline can be sealed twice when the hook's spooled copy is replayed before the slow live seal finishes; the daemon never consumes the copy before a seal has succeeded, so the compaction always keeps a checkpoint."
- Route residual, narrowed and still the contract seat's (handleSessionStart's registry.Ensure). Revised release-notes sentence: "A SessionStart replayed from a hook's spool that the host fired before its session ended, or that names a session this daemon has not seen, still marks that session live in the daemon's registry, so a later replayed delivery of that session can bind an unbound scheduler to it until the live session's next SessionStart, and the daemon's idle exit waits for that session's silence timeout."
- Integration overlap:
- internal/daemon/registry.go gains CurrentAt beside StartedSince. No wave 22 branch touches registry.go: w22-rehydrate's merge base predates 2bf29705 and its own diff does not touch the file.
- handleSessionStart still has a single changed line from this seat (now withCurrentStart and CurrentAt), next to `existedBefore` at about line 981. The contract seat's handlers.go hunks are elsewhere.
- Sibling unchanged from base: a replayed resume start that lands between handleFlush's registry.End and endSession's SessionEnd seam can reopen p-selection only for that seam's Close to turn it off again, as base's same-id bind also could. Closing that window needs the session-end ordering, which belongs to session_end.go.
- Carried from earlier rounds: the #73 throughput measurements for the ledger, and the replayed PreCompact on an unbound runtime that binds through CloseSegmentForCompaction.


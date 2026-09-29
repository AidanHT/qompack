# Wave 13 — w13-pinsckpt (live-lane defects, D45)

Branch `closeout/w13-pinsckpt`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `8d98bf82`

### Root cause

F-UAT05-2 had two defects. (a) The resident daemon's pins.Store (internal/pins/store.go) read invariants.jsonl once, at OpenWith, and answered every later All and Materialize from that snapshot. The code assumed this process's own appends were the only way the log could change. That assumption is false: `qompack pin` (internal/cli/qompack_commands.go buildCommandDeps) opens its own store in another process and appends to the same log. The CLI wrote the log line and a correct view. The daemon's idle materialize_pins task and its seal (Finalize.afterSeal) then rewrote invariants.json from the stale set (fsck: 'the view is stale'). Begin seeded the checkpoint's invariants from the same stale set, so the checkpoint had dropped [] and degraded false. A daemon restart replayed the log, which is why the pin only appeared after one. (b) Even with a fresh store, checkpoint invariants were seeded only at Begin (writer.go seedTierOne). A seal begins its successor draft at once, so every compaction after a session's first sealed the pin set of the previous compaction.

F-UAT03-1: only closed segments are encoded (writer.go Advance: 'Only closed segments may be encoded; SP-12 closes them'). Segments close only on a todo/test/commit boundary, a BOCD changepoint, or SessionEnd. The UAT-03 evidence (store/index_segments.jsonl) shows segment 1 opened at 1790704292187, PreCompact at 1790704302660, and the close only at 1790704321991 (SessionEnd). The segment holding both Reads was still open when the host compacted, so the checkpoint encoded nothing: encoded_segments, pointers.files and pointers.tools were all empty, and user_intent.evolution was empty too. Separately, checkpoint.draftForPreCompact caught up only on the cold path. A warm draft, which every compaction after the first finds, was sealed with no catch-up at all. From the design: Qompack.md §8.5 says the committed frontier 'records ... unresolved tails'. docs/uat.md UAT-03 step 3 expects 'the artifact carries its frontier and its references', and the checkpoint is the rehydration's only source of pointers. So empty is not correct: the compaction is the boundary of the span the host summarizes, and that span must be encoded into the checkpoint sealed for it.

### Summary

Both live findings are reproduced by deterministic tests that were RED on the base, and both are fixed. The tests use the shipped composition: runDaemon through Dispatch, the shipped hook clients in process, and `qompack pin` run beside the live daemon. No real Claude Code session was used.

WHAT CHANGED
1. internal/pins/store.go: every Add, Remove, All and Materialize first folds in whatever the log gained (catchUpLocked).
   - Nothing changed: one stat.
   - The log grew: only the appended bytes are read. A cursor stops at the last newline, so a record another process is still appending is re-read from its start.
   - The log got shorter (a restore): the set is rebuilt from a full replay.
   - The log vanished, or is no longer a regular file: the set already held is kept (never 'no pins'), with one Warn.
   - readCappedLine now reports the bytes it consumed. Comments in doc.go, pins.go and internal/cli/scheduler_wiring.go are updated.
2. internal/checkpoint/finalize.go: sealInvariants re-reads pins.All at the seal, with context.WithoutCancel so a deadline cannot empty tier 1. If the read fails, the draft's invariants are kept and the seal adds a named drop: {kind:"invariants", id:"pins", detail:...}. A pin that cannot be included is now a named drop, never silent. This is what makes a pin reach the very next checkpoint and block, including after an earlier compaction.
3. internal/daemon/scheduler_tap.go and scheduler_frontier.go: the scheduler tap now decorates PreCompact, and only when it is set.
   - Before the inner checkpointer seal runs, CloseSegmentForCompaction closes the compacting session's open segment at the runtime's highest observed turn (cause "compact", counter sched.segment.closed.compact) and rolls its successor open.
   - A compaction of a different session than the bound one is left alone and counted as sched.tap.compact_foreign.
   - closeSegmentLocked now delegates to a new closeSessionSegmentLocked that takes the session as a parameter.
4. internal/checkpoint/precompact.go: draftForPreCompact catches up both cold and warm drafts through catchUpForPreCompact. It uses the same single bounded Advance as before, over closed, unencoded segments only, oldest first.
5. docs/commands.md needed no change: the pin help text is unchanged.

TESTS ADDED (all RED before, green after)
- internal/cli/precompact_live_rows_test.go: TestLivePinReachesTheNextCheckpointAndBlock checks the pin reaches the checkpoint, the invariants.json view and the rehydration additionalContext. TestLivePinAfterACompactionReachesTheNextOne covers the successor-draft case.
- internal/cli/precompact_pointers_live_test.go: TestPreCompactCheckpointCarriesTheCompactedReads is the UAT-03 shape (prompt, two Reads, Stop, compaction); it checks encoded_segments, both tool pointers, both file pointers and no content. TestSecondPreCompactCarriesItsOwnSpan checks the second compaction's span.
- internal/pins/store_test.go: TestPinsSeesAnotherWritersAppends, TestPinsRebuildsWhenTheLogShrinks, TestPinsKeepsItsSetWhenTheLogVanishes.
- internal/checkpoint/finalize_test.go: TestFinalizeSealsAPinMadeAfterBegin, TestFinalizeNamesPinsItCouldNotReread.
- internal/checkpoint/precompact_test.go: TestWarmPreCompactEncodesTheSegmentClosedAtCompaction.
- internal/daemon/scheduler_tap_test.go: TestWrapServices_PreCompactClosesTheCompactedSegmentFirst, TestWrapServices_PreCompactLeavesAnotherSessionsSegment.

RED evidence is committed under plans/sdd/V6-closeout/w13-pinsckpt/runs/ (01 to 06):
- 01 (live pin rows, before any fix): '[]string(nil) does not contain inv_8382970dffd4'.
- 05 (pointer row, with the daemon change stashed): 'encoded_segments ... was []'.

CRITERION CHANGES (the rationale is also in the commit bodies)
- TestWrapServices_UndecoratedSeamsUntouched: PreCompact moved from 'untouched' to 'decorated' (require.NotEqual). A new assertion checks that an unset PreCompact stays nil. Rationale: the compaction boundary is now a deliberate tap decoration, and it runs before the inner seam, not after.
- TestPreCompactNamesTheContiguousFrontierInTheSpan: every assertion is unchanged. The fixture's gap segment 2 is now open (in flight) instead of closed but not yet advanced. Rationale: a warm PreCompact now legitimately encodes a closed segment, so the only gap a seal still cannot close is an open one. The test still proves the span paragraph names the contiguous frontier (10), not the maximum (30).

NEW BOUNDS: none. The existing coldEncodeWindow and maxColdEncodeSegments now also bound the warm catch-up.

WHAT THE COORDINATOR SHOULD KNOW
- Interaction with the intent seat (F-UAT05-1 and F-UAT06-2, user_intent.evolution always empty): encodeSegmentLocked appends every prompt in an encoded segment's range to user_intent.evolution. Evolution was empty in the live runs partly because no segment was ever encoded before the compaction, which is the F-UAT03-1 root cause. Checkpoints sealed at a compaction will now carry evolution entries and a pointers section, so rehydration blocks change shape. The intent seat's fix should be checked against this, and live rows that compare block contents may shift.
- Known limit: the compaction close uses the scheduler runtime's highest observed tool-use turn. A final prompt answered without a tool lands in the successor segment and is encoded by a later checkpoint, never lost. A daemon that restarted mid-session is unbound and closes with what it has observed since.
- Pre-existing, not introduced here: after any scheduler roll (todo, test, commit, changepoint, and now compact), the observer's in-memory st.Segment stays on the closed segment until the next SessionStart. Normally SessionStart(compact) follows and re-adopts the successor. After a host-failed compaction (the UAT-04 F-UAT04-5 shape), the successor is only closed by later boundaries. SessionEnd tries to close the stale id and gets a soft ErrAppendOnly.
- Found in passing, not fixed: the C1.16 load rig's Read payloads in internal/cli/sessionstart_compact_load_test.go (readPayload: absolute path plus a {type, file:{filePath, content}} response) are never captured. index/tool_use.jsonl ends up holding only the prompt, so that rig's 'same-session ingest' load never reached the observer. My tests use the observer e2e payload shape instead.
- Every run's temporary diagnostic test file was deleted before committing. No load generator or background process of mine is still running.

COMMANDS AND RESULTS
Machine loaded by other seats, -p 2 throughout. No wall-clock failures were seen.
- go run ./tools/devtool fmt and fmt-check: clean.
- go vet on the four touched package trees, on Windows and GOOS=linux: clean.
- Scoped lint (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns): all PASS. The first attempt's golangci-lint failed only with 'parallel golangci-lint is running' (another seat held the lock); the retry passed.

### Commits

- 47a8f2c2 fix(pins): fold another writer's appends before every read
- 4999c8aa fix(checkpoint): seal the pins current at the seal, not at Begin
- f3b54e6e fix(checkpoint): encode the compacted span at PreCompact
- 8d98bf82 test(v6): record the w13 pins and checkpoint pointer runs

### Tests

- `go test -p 2 -count=1 -run 'TestLivePinReachesTheNextCheckpointAndBlock|TestPreCompactCheckpointCarriesTheCompactedReads' ./internal/cli (base, before any fix)` — RED as intended: pin missing from the checkpoint; encoded_segments empty (runs/01)
- `go test -p 2 -count=1 -run 'TestPinsSeesAnotherWritersAppends|TestPinsRebuildsWhenTheLogShrinks' ./internal/pins (before fix)` — RED as intended (runs/03)
- `go test -p 2 -count=1 -run 'TestFinalizeSealsAPinMadeAfterBegin|TestFinalizeNamesPinsItCouldNotReread' ./internal/checkpoint (before fix)` — RED as intended (runs/02)
- `go test -p 2 -count=1 -run 'TestWrapServices_PreCompactClosesTheCompactedSegmentFirst|TestWrapServices_PreCompactLeavesAnotherSessionsSegment' ./internal/daemon (before fix)` — RED as intended (runs/04)
- `go test -p 2 -count=1 -run 'TestPreCompactCheckpointCarriesTheCompactedReads' ./internal/cli (daemon change stashed)` — RED as intended: encoded_segments empty (runs/05)
- `go test -p 2 -count=1 -run 'TestWarmPreCompactEncodesTheSegmentClosedAtCompaction' ./internal/checkpoint (before fix)` — RED as intended (runs/06)
- `go test -p 2 -count=1 ./internal/pins/... ./internal/checkpoint/...` — ok: pins 12.4s, pinstest 2.1s, checkpoint 151.4s, checkpointtest 2.4s (runs/07)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon` — ok 428.8s (runs/08)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli` — ok 68.3s (runs/09)
- `go test -p 2 -count=3 -run 'TestLivePinReachesTheNextCheckpointAndBlock|TestLivePinAfterACompactionReachesTheNextOne|TestPreCompactCheckpointCarriesTheCompactedReads|TestSecondPreCompactCarriesItsOwnSpan' ./internal/cli` — ok 14.1s (runs/10)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS, exit 0 (runs/11); the first attempt's golangci-lint was refused only by another seat's parallel-run lock
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) ./internal/pins/... ./internal/checkpoint/... ./internal/daemon/... ./internal/cli/...` — clean

### Criterion changes

- TestWrapServices_UndecoratedSeamsUntouched (internal/daemon/scheduler_tap_test.go): PreCompact moved from 'untouched' (require.Equal) to 'decorated' (require.NotEqual), plus a new assertion that an unset PreCompact stays nil. Rationale: the scheduler tap now deliberately decorates PreCompact, running before the inner seam, to close the compacted segment (F-UAT03-1).
- TestPreCompactNamesTheContiguousFrontierInTheSpan (internal/checkpoint/precompact_test.go): assertions unchanged; the fixture's gap segment 2 is now OPEN instead of closed but not yet advanced. Rationale: a warm PreCompact now legitimately encodes closed segments, so an open (in-flight) segment is the only gap a seal cannot close. The test still proves the contiguous frontier (10) is named, not the maximum (30).

### Open issues

- Intent seat interaction: checkpoints sealed at a compaction now encode the compacted span, so user_intent.evolution and the pointers section are populated. This changes rehydration blocks and may overlap the intent seat's F-UAT05-1/F-UAT06-2 fix; check the two merges together.
- The compaction close uses the scheduler runtime's highest observed tool-use turn. A final prompt answered without a tool is encoded by the next checkpoint, not this one. A daemon restarted mid-session (runtime unbound) closes with what it has observed since the restart.
- Pre-existing: after any scheduler segment roll the observer's st.Segment is stale until the next SessionStart. After a host-failed compaction, SessionEnd tries to close the already-closed id (soft ErrAppendOnly) and leaves the successor open until a later boundary.
- Pre-existing, found in passing: the C1.16 load rig (internal/cli/sessionstart_compact_load_test.go readPayload) sends Read payloads that are never captured into index/tool_use.jsonl, so its same-session ingest load never reached the observer.
- The rehydrator ranks the new checkpoint drop kind 'invariants' as an unknown kind (rank 99, last in section 7). It is still named, but the rehydrate owner may want to rank it next to overflow.
- Not run: whole test/e2e and test/integration, the hot-path rows, and Linux (container stopped). The coordinator re-runs the UAT-03 and UAT-05 live rows on the fixed candidate.

## Independent review

### review:pinsckpt: needs-fixes

- **minor** `internal/daemon/scheduler_frontier.go:85-91 (CloseSegmentForCompaction, unbound branch)` — When the runtime is bound to no session, the compaction close still runs, using r.maxTurn and r.openSegTokens. In that state both hold only what was observed since the daemon started, and are 0 before the first tool use. The only guard is `at < cur.StartTurn`, which cannot protect a session's first segment (StartTurn 0). A daemon restarted mid-session that receives PreCompact before any tool use closes segment [0,0] with Tokens 0. Everything the previous daemon saw moves into the successor. The next checkpoint encodes an almost empty segment, and the store keeps Segment.Tokens at zero for good. The comment in closeSessionSegmentLocked calls that zero harmful to contextTokens and ShouldCompact. The doc comment claims the unbound case is correct, but no test covers it.
  - Evidence: The wiring passes no Session in SchedulerRuntimeOptions (internal/cli), so the runtime binds only on a SessionStart hook (scheduler_runtime.go:283, scheduler_tap.go:163). A mid-session restart gets no SessionStart. resetSessionLocked sets maxTurn=0 and openSegTokens=0. MaxTurn and OpenSegmentTokens are restored from state/scheduler.json only inside BindSession → loadStateLocked (scheduler_state.go:403-404). The observer opens the first segment at StartTurn st.Turn, which is 0 at startup.
  - Fix: When r.session == "", bind sess under the lock before closing (a locked BindSession variant, so loadStateLocked restores MaxTurn and OpenSegmentTokens for that id). Otherwise skip the close when the runtime has made no observation for sess, and count it. Add a daemon test: a fresh runtime that is not bound, with an open first segment at StartTurn 0 and a persisted scheduler.json. PreCompact must not close [0,0] with zero tokens.
- **minor** `internal/daemon/scheduler_tap.go:285-290 with internal/observer/session.go:125-145,181-197` — Every PreCompact now rolls the segment. Before this change, a compaction never did. After a roll, the observer's st.Segment still names the closed segment until SessionStart(compact) calls ensureSegment again. A host-failed compaction (the UAT-04 F-UAT04-5 shape, seen in the live lane) never sends SessionStart(compact). The observer then keeps enrolling DAG nodes against the closed segment id. SessionEnd then closes that stale id (soft ErrAppendOnly) and leaves the successor open, so the session's tail is never closed and never encoded. The implementer calls this pre-existing, and it is for todo/test/commit rolls. This change adds a new trigger on a path the live lane has actually hit, so it is not a neutral carry-over.
  - Evidence: The implementer's open_issues item 3. observer ensureSegment runs only from OnSessionStart. The SessionEnd close uses st.Segment (session.go:197). CloseSegmentForCompaction opens a successor that the observer does not learn about.
  - Fix: Either (a) in observer.onSessionEnd, when st.Segment is already closed, re-resolve Segments().Current(sess) and close that instead, or (b) have the tap tell the observer about the roll. Because internal/observer is SP-08's, route this to the owning seat or coordinator as a ticket before 0.3.0, with a failing test: PreCompact, no SessionStart(compact), then SessionEnd, and the successor must end up closed.
- **minor** `internal/checkpoint/finalize.go:184-217 with internal/rehydrate/items.go:196-215 (kindRank)` — The new named drop {kind:"invariants", id:"pins"} is a tier-1 loss: a pin may be missing. The rehydrator does not rank it (kindRank has no entry), so it sorts last (rank 99). Section 7 of the rendered block can be cut down to a count, so this drop can vanish from the text the agent reads. That weakens the task's requirement that a pin which cannot be included is 'a named drop, never silent'.
  - Evidence: The implementer's open_issues item 5. budget.go:434-438 says the rendered section 7 may be truncated to a counted form, while Result.Dropped keeps the full list.
  - Fix: Add "invariants" to kindRank directly after dropKindOverflow (rank -1, or 0 with the others shifted), with a rehydrate test that a checkpoint carrying this drop renders it by name under a tight section-7 budget. If the file is out of this seat's scope, hand it to the rehydrate/intent seat as a merge-blocking follow-up.
- **minor** `internal/pins/store.go:525-582 (foldFrom countTail=false path); internal/pins/store_test.go` — The cross-process catch-up is concurrency-facing and has no direct test. When another process is still writing a record, the cursor must stop at the last newline. The unterminated partial must not be counted as damage, and it must be re-read from its start once it is finished. The three new tests cover a completed append, a shrink and a vanish, but not a partial line.
  - Evidence: store_test.go adds TestPinsSeesAnotherWritersAppends, TestPinsRebuildsWhenTheLogShrinks and TestPinsKeepsItsSetWhenTheLogVanishes. No test writes a torn tail after open and then completes it.
  - Fix: Add a test: open the store and read, append `{"op":"add",...` with no closing brace or newline, call All (the set is unchanged and pins.badline is still 0), finish the record plus '\n', call All (the pin appears exactly once, pins.badline is still 0), then compare with a fresh OpenWith replay.
- **nit** `plans/00-ARCHITECTURE.md:2461-2463; internal/checkpoint/precompact.go:83-97; plans/00-ARCHITECTURE.md:2014` — The docs now disagree with the code. The architecture 'Segment lifecycle' says SP-12 closes segments on a changepoint, a todo completion or a passing test; it now also closes on compaction, and the commit cause was already missing. The coldEncodeWindow and maxColdEncodeSegments comments still say they bound the cold path's catch-up, but they now bound the warm catch-up too. The Advance port doc says it is 'Called during idle (O5)', but PreCompact now calls it on every warm draft.
  - Evidence: The diff changes precompact.go:draftForPreCompact to catch up on both paths, and scheduler_tap.go now decorates PreCompact with cause "compact". None of these doc lines are touched.
  - Fix: Update the three passages: add compaction (and commit) to the segment lifecycle, reword the two bound comments to 'PreCompact's catch-up (cold or warm)', and note PreCompact as an Advance caller.
- **nit** `internal/cli/precompact_pointers_live_test.go:96-101,148-150` — Both pointer rows send an extra UserPromptSubmit with the text "/compact" as a sync barrier before the PreCompact. The real host does not send this for a built-in /compact. The rows therefore do not show that a PreCompact arriving right after fire-and-forget PostToolUse deliveries sees their maxTurn. The barrier also adds a prompt turn the live session did not have.
  - Evidence: The test comment says the barrier exists so that 'its answer is also the point by which the Reads' observers have run'.
  - Fix: Keep the barrier, but add one row (or one variant) that relies only on the daemon's per-session ordering of leased arrivals before PreCompact, without the extra prompt. Otherwise, name the ordering assumption in the coordinator notes so the live UAT-03 re-run is what checks it.
- **nit** `plans/sdd/V6-closeout/w13-pinsckpt/runs/01-red-cli-live-rows.txt; commit 8d98bf82` — The RED evidence in run 01 places TestPreCompactCheckpointCarriesTheCompactedReads at precompact_live_rows_test.go:98. The committed test is in precompact_pointers_live_test.go, so the evidence was captured before the file split and does not map to the committed tree. Run 05 is the matching RED. Commit 8d98bf82 also has no Refs: V6-VERIFY, C4.3 footer, although the three fix commits carry one.
  - Evidence: In run 01, the second FAIL trace is precompact_live_rows_test.go:98. In the committed tree, that file holds only the two TestLivePin* rows.
  - Fix: Add a one-line note to run 01 saying the test later moved to precompact_pointers_live_test.go (run 05 is its RED on the final layout), and add the Refs footer in a follow-up evidence commit if the convention requires it.

## Fix seat (review resolution) — status `done`, head `6a93fd18e8673f23d885875dc74117b4738de00f`

### Root cause

Finding 1: on a daemon restarted mid-session, the scheduler runtime is unbound when PreCompact arrives and its accumulators were reset to zero, so the compaction close recorded [0,0] with zero tokens. Fixed by binding the session first, restoring persisted state and merging what was observed since the restart, and by leaving a segment with no token account open. Finding 3: the seal's {invariants, pins} drop had no kindRank entry, so it sorted last and could be cut into the counted tail; it now ranks with overflow. Finding 4: missing coverage only; test added. Finding 2: a defect that predates this change in how the observer and scheduler share segment rolls; routed as a ticket with a failing reproduction.

### Summary

W13 pinsckpt fix seat. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-pinsckpt, branch closeout/w13-pinsckpt, now at 6a93fd18 on top of the implementer's 4 commits (base closeout/integration 84d2512). Nothing was pushed or merged, and nothing of mine is still running.

## Review resolution
I checked all four reviewer findings myself. Three were fixed or given test coverage in this branch. One is a real defect that predates this change and is routed to its owner as a ticket, with a reproduction that fails.

1. **Unbound runtime at PreCompact (minor): CONFIRMED and FIXED (2af2de46).**
   - **Root cause:** after a daemon restarts mid-session, no SessionStart arrives, so `schedRuntime.session == ""`. `resetSessionLocked` had zeroed `maxTurn` and `openSegTokens`. `CloseSegmentForCompaction` then closed the session's first segment at [0,0] with Tokens 0, and everything the previous daemon had seen moved into the successor.
   - **RED first:** runs/15-red-daemon-precompact-unbound.txt. All 3 new tests failed: EndTurn 0 instead of 9; Tokens 300 instead of 1000; the segment was closed when it should have stayed open.
   - **Fix, part 1:** `BindSession` is split so its locked body can be reused (`bindSessionLocked`). On an unbound runtime, `bindForCompactionLocked` binds the compacting session the way SessionStart does, restoring `state/scheduler.json`. It then folds in what the new daemon observed since the restart: turn = max of the two, tokens = sum. This cannot double-count, because an unbound runtime never persists. The live context is recounted and the burn baseline reset, the same as a bind.
   - **Fix, part 2:** a compaction close whose open-segment token count is still 0 now leaves the segment open and adds 1 to a new counter, `sched.tap.compact_unobserved`, instead of closing it with zero tokens forever.
   - **New tests (scheduler_tap_test.go):** TestWrapServices_PreCompactAfterARestartClosesWithThePersistedAccount, TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince, TestWrapServices_PreCompactWithNothingObservedLeavesTheSegmentOpen.

2. **Observer keeps naming the closed segment after a roll (minor): CONFIRMED, NOT FIXED HERE, ROUTED.**
   - **The defect:** only `OnSessionStart` ever calls `ensureSegment`. After any scheduler roll that no SessionStart follows, `st.Segment` still names the closed segment. SessionEnd then closes that stale id (a soft ErrAppendOnly), the successor stays open, and DAG members keep enrolling against the old id. The segment the scheduler closed also never gets a DAG segment node.
   - **Reproduction:** a temporary diagnostic test in internal/observer, removed after the run and not in the tree. It fails with expected segment 2, actual 1. Source is runs/16-ticket-observer-stale-segment.go.txt; output is runs/17-ticket-observer-stale-segment-red.txt.
   - **Why not fixed here:** the reviewer is right that the new compaction roll adds one more trigger on the host-failed-compaction path. But the same defect already fires on every todo, test, commit and changepoint roll, and those are never followed by a SessionStart. The compaction roll is the one roll a successful compaction normally re-syncs, through SessionStart(compact) and then ensureSegment.
   - **Why not a small fix at SessionEnd:** re-reading the current segment there would not be a correct fix. The observer does not know SegStartPos for the successor, so the segment node's Pos and Tokens would be wrong. The observer needs to learn about rolls, which is a design change between SP-08 and SP-12 across seats.
   - **Ticket for the coordinator:** route to the SP-08 observer owner before 0.3.0.

3. **The pin drop sorts last in item 7 (minor): CONFIRMED and FIXED (63ede369).**
   - **RED first:** runs/13-red-rehydrate-pins-drop-rank.txt. Under a tight allowance, the kept line was a path_rule and the `{invariants, pins}` drop went into the counted tail.
   - **Fix:** `dropKindInvariants` ("invariants") now ranks -1 in `kindRank`, alongside overflow. The kindRank and dropRank comments are updated to match.
   - **Test:** TestDropReport_UnreadPinSetSurvivesATightReport checks that `fillDropReport`, with room for the heading, one line and the tail, keeps exactly the pins line plus "… and 4 more".
   - **Merge note:** internal/rehydrate/items.go is also the intent seat's area. The change is one map entry, one const and comments, so any conflict should be trivial.

4. **No test for a partial record in the pins catch-up (minor): CONFIRMED as missing coverage, not a defect; test ADDED (c97b0765).**
   - TestPinsCatchUpWaitsForAnotherWritersPartialRecord: half a record is neither folded nor counted in `pins.badline`. Once the other writer finishes the line, the record is folded exactly once, `pins.badline` stays 0, and the result equals a fresh replay.
   - The test passes on the shipped code. To show it can fail, I temporarily changed the catch-up to call `foldFrom(offset, true)`; the test went red with `pins.badline` = 1, and I reverted the change (runs/12-review-pins-partial-record.txt).

## Commands and results (Windows, loaded machine, `-p 2`)
- `go test -p 2 -count=1 -run TestPinsCatchUpWaitsForAnotherWritersPartialRecord ./internal/pins/`: ok.
- `go test -p 2 -count=1 ./internal/pins/`: ok.
- `go test -p 2 -count=1 -run TestDropReport_UnreadPinSetSurvivesATightReport ./internal/rehydrate/`: RED before the fix, ok after.
- `go test -p 2 -count=1 ./internal/rehydrate/`: ok.
- `go test -p 2 -count=1 -run TestWrapServices_PreCompact ./internal/daemon/`: 3 of the 5 PreCompact tests RED before the fix; all 5 pass after.
- The implementer's 4 CLI live-row tests rerun on the fixed code, one pattern each (I quote them singly because the runpatterns lint splits `|` alternations):
  - `go test -p 2 -count=1 -run TestPreCompactCheckpointCarriesTheCompactedReads ./internal/cli/`
  - `go test -p 2 -count=1 -run TestSecondPreCompactCarriesItsOwnSpan ./internal/cli/`
  - `go test -p 2 -count=1 -run TestLivePinReachesTheNextCheckpointAndBlock ./internal/cli/`
  - `go test -p 2 -count=1 -run TestLivePinAfterACompactionReachesTheNextOne ./internal/cli/`
  - All four passed in one alternation run: ok, 5.5s.
- `go test -p 2 -count=1 ./internal/daemon/` with the default 10m timeout failed on the package total, not on a hang: the running test had been going for 1s. The implementer's unloaded run took 428s. Rerun with `-timeout=30m`: ok, 454s.
- `go run ./tools/devtool fmt-check`: clean.
- `go vet` on internal/daemon, internal/rehydrate and internal/pins, on Windows and with GOOS=linux: clean.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every sub-check PASS.
- No Linux container, no `-race`, no hot-path rows, and no whole test/integration or test/e2e packages, per this seat's limits.
- Evidence logs are committed as runs/12 through runs/19 under plans/sdd/V6-closeout/w13-pinsckpt/.

## Criterion changes
None. No check was weakened. The only existing test assertion changed in this branch is the implementer's TestWrapServices_UndecoratedSeamsUntouched: PreCompact is now decorated, and the test asserts that instead.

## New constants
- `sched.tap.compact_unobserved` is a counter name, not a budget or bound, so nothing goes to needs_owner.

### Commits

- 47a8f2c2 fix(pins): fold another writer's appends before every read (implementer)
- 4999c8aa fix(checkpoint): seal the pins current at the seal, not at Begin (implementer)
- f3b54e6e fix(checkpoint): encode the compacted span at PreCompact (implementer)
- 8d98bf82 test(v6): record the w13 pins and checkpoint pointer runs (implementer)
- c97b0765 test(pins): cover a catch-up that meets a half-written record
- 63ede369 fix(rehydrate): rank the seal's pin drop with overflow in item 7
- 2af2de46 fix(daemon): bind before a compaction close on an unbound runtime
- c48fb2b2 test(v6): record the observer stale-segment ticket reproduction
- 6a93fd18 test(v6): record the w13 fix seat's package, vet and lint runs

### Tests

- `go test -p 2 -count=1 -run TestPinsCatchUpWaitsForAnotherWritersPartialRecord ./internal/pins/` — ok (and FAIL under a temporary mutation that counts the catch-up's unterminated tail, which I reverted)
- `go test -p 2 -count=1 ./internal/pins/` — ok 5.6s
- `go test -p 2 -count=1 -run TestDropReport_UnreadPinSetSurvivesATightReport ./internal/rehydrate/` — FAIL before the fix (pins line in the counted tail), ok after
- `go test -p 2 -count=1 ./internal/rehydrate/` — ok
- `go test -p 2 -count=1 -run TestWrapServices_PreCompact ./internal/daemon/` — 3 new tests FAIL before the fix; all 5 PreCompact tests pass after
- `go test -p 2 -count=1 -run 'TestPreCompactCheckpointCarriesTheCompactedReads|TestSecondPreCompactCarriesItsOwnSpan|TestLivePinReachesTheNextCheckpointAndBlock|TestLivePinAfterACompactionReachesTheNextOne' ./internal/cli/` — ok 5.5s
- `go test -p 2 -count=1 ./internal/daemon/` — timed out at the default 10m on the loaded machine: package total, the running test had been going 1s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 454s
- `go run ./tools/devtool fmt-check; go vet (windows and GOOS=linux) ./internal/daemon/ ./internal/rehydrate/ ./internal/pins/` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS

### Open issues

- TICKET for the SP-08 observer owner, before 0.3.0 (review finding 2): the observer adopts a segment only in OnSessionStart (ensureSegment). After any scheduler roll that no SessionStart follows (changepoint, todo, test, commit, and now compact on a host-failed compaction), st.Segment names the closed segment. SessionEnd then closes that id (a soft ErrAppendOnly), the successor stays open and is never encoded, and DAG members keep enrolling against the old id; the closed segment also never gets a DAG segment node. The reproduction fails (expected segment 2, actual 1): plans/sdd/V6-closeout/w13-pinsckpt/runs/16-ticket-observer-stale-segment.go.txt and runs/17-ticket-observer-stale-segment-red.txt. The fix needs the observer to learn about rolls; re-reading the current segment at SessionEnd alone would give the segment node the wrong StartPos and Tokens.
- The scheduler runtime still stays unbound for the rest of a session after a mid-session daemon restart, except when a PreCompact now binds it. Until then Evaluate returns error_no_window and nothing is persisted. This predates this branch and is outside this seat's scope.
- Merge note: internal/rehydrate/items.go (kindRank gets a dropKindInvariants entry) overlaps the intent seat's area, so expect at most a trivial map-literal conflict.
- The coordinator's committed report should quote each -run pattern on its own; the runpatterns lint splits alternations at '|'.
- The coordinator re-runs the live rows F-UAT05-2 and F-UAT03-1 on the fixed candidate.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



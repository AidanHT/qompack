# Wave 20 redeliver (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-redeliver`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/daemon/scheduler_tap.go:201 (with internal/daemon/ingest.go:961-976, internal/daemon/daemon.go:1213, internal/cli/segment_roll_live_test.go:214-215 and :239)`: The pre-existing intermittent red in TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook (expected 8, actual 12) comes from a real redelivery defect, not from noise. The worker runs run() first, which is ObserveTool plus schedTap.observeTool, and AddOpenSegmentTokens folds the Read's tokens at that point. Only then does it run commitDelivery(ctx). When Stop cancels the worker's ctx during the commit, the commit fails ('daemon: delivery not acknowledged; WAL retained for retry ... context canceled'). Stop's own d.Drain then replays the retained WAL line, and the tap folds the same Read's tokens a second time. The store write is idempotent, but the tap's token fold is not, so open_segment_tokens is persisted inflated. The test's settle step does not prove the Read was acknowledged before stop(). It sends a prompt and relies on that prompt's reply, but under load the prompt hook's reply wait expires first. The code path is unchanged since c7 (git diff d20309c0 738d67c7 of ingest.go, scheduler_tap.go, scheduler_runtime.go, daemon.go and the test is empty; the test dates from 9726729c, 2026-09-29), so this is not a c8 regression. It is still a check likely to go red tonight. c8-night.sh's prefreeze 'internal' step runs ./internal/... at -count=1 and stops the whole night (no freeze, no push, no overnight) on any red. Hosted CI runs internal/cli about 9 times per push, each as a cold single run: test on 3 OSes (count=2 on Windows), stubskips in verify and lint-windows, cover, release-dry-run's test and cover steps, and nightly race-windows.
- **major** `internal/cli/segment_roll_live_test.go:239 (TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook); internal/daemon/scheduler_tap.go:201; plans/sdd/V6-closeout/w19-loudcfg/report.md:96,124`: An intermittent failure the loudcfg fix seat routed to the coordinator has no disposition anywhere. In one full internal/cli run, OpenSegmentTokens was 12 where 8 was expected ('looks like one Read counted twice'). D60 does not mention it, and no ledger row or report-c7 entry carries it. The night's pre-freeze `internal` step (prefreeze.sh: go test -p 2 ./internal/...) runs this test, and c8-night.sh refuses to freeze if any step fails. If it recurs tonight, the whole night is lost before the freeze. A real double count is also plausible: schedTap.observeTool calls AddOpenSegmentTokens(rec.Tokens) for every ObserveTool event with no guard per tool_use_id, so a delivery that reaches runIngested twice (live, then replayed) would be folded in twice.

## impl:redeliver: status `done`, head `47043d9b816414accefa1e91395d2031179e4d3d`

### Root cause

The handler, and the scheduler tap inside it, runs before the delivery is committed: ingest.dispatch runs run() and then commitDelivery(ctx), and drainer.dispatchPending runs Dispatch and then commitDelivery. When Stop's runCancel or a bounded drain's context cuts the commit, the WAL line is retained and replayed through the same handler. The observer absorbed the replay, but schedTap.observeTool folded rec.Tokens again with AddOpenSegmentTokens, re-observed the BOCD detector, and moved the prompt and activity anchors, with no per-delivery guard. Persist wrote the inflated account. Across a restart, the unbound runtime folded the replay into what bindUnboundLocked adds on top of the restored account, which already held it.

### Summary

PRODUCT CODE CHANGED (internal/daemon/scheduler_tap.go, scheduler_runtime.go, scheduler_state.go).

Both findings in redeliver.json describe the same defect, and both hold. I fixed it at the source on branch closeout/w20-redeliver in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-redeliver. Base is 738d67c7, new HEAD is 47043d9b. Nothing was pushed or merged.

Root cause, checked against the code. ingest.dispatch (ingest.go ~961-976) runs run() first. run() calls runIngested, which calls the ObserveTool seam: the observer runs, then schedTap.observeTool, which folds rec.Tokens with AddOpenSegmentTokens (scheduler_tap.go:201). Only after that does dispatch call commitDelivery(ctx). drainer.dispatchPending has the same order: Dispatch, then dr.commitDelivery(ctx).
- Stop (daemon.go ~1185-1213) calls runCancel and then d.Drain. If the cancel lands between the handler and the commit, the commit fails with "delivery not acknowledged; WAL retained ... context canceled". Stop's drain then replays the line through the same handler.
- The observer recognises the replay and absorbs it (observer.redelivery_absorbed). The tap does not. It folded the tokens again and also re-observed the BOCD detector. A replayed Stop re-observed the detector, and a replayed prompt capture moved lastRequestStartTS to the replay time.
- Persist then wrote the inflated account to disk.
- The same thing happens across a restart. When Stop's drain also fails to commit, the restarted daemon's startup drain replays the line into the still-unbound runtime. bindUnboundLocked then adds those tokens on top of the restored account, which already holds them.

Fix. The tap now applies each delivery exactly once, keyed by the observation identity the worker and the drain already put on ctx (observer.WithObservation).
- **Why one identity per session is enough:** the ordering gate (predecessorsAcknowledged, used by both the live path and the drain) lets a session's next leased delivery run only after every earlier one is acknowledged, and an acknowledged delivery is never dispatched again. So the only delivery that can come back after being applied is the last one applied for that session. schedRuntime.applied therefore holds one identity per session. If that invariant were ever broken, the check fails safe: the replay gets applied again, which is the old behaviour.
- **Atomic with the account:** new methods applyToolUse, applyStop and applyPrompt make the claim and every state change it guards under one hold of r.mu. A Persist can never snapshot an identity without its tokens.
- **Persisted for restarts:** the bound session's identity is written to state/scheduler.json as last_applied_observation (additive, omitempty, version stays 1, same pattern as last_local_checkpoint_ts). It is restored at bind (only if this process has no newer entry). A runtime constructed unbound seeds it with seedApplied, which reads through the same shared reader, readStateFile; that function is now a plain function, and its sharedReaders guard row still passes. So the startup drain recognises the replay.
- **What still runs as before:** replays are counted under sched.tap.redelivery. A delivery with no identity (unleased, or an in-process caller) is applied as before. The no-record path claims nothing, so a replay that finds a record the first run could not publish still gets applied.
- **Docs:** docs/architecture.md §4 gained one paragraph, in a separate hunk from the drain seat's lines 176-179. In the cli test file only comments changed (rigReadAndSettle's claim that the prompt proved the tap had run was wrong); no assertions changed.

Other non-idempotent effects on the same run-before-commit path:
- **Fixed by this change:** BOCD and feature history, the boundary-close counters, the token fold, the request-start and activity anchors, and NoteEffort, on all three observation seams.
- **Breach detector samples:** not on this path. recordHotPathSample runs on the accept route (handlers.go:298), not in run().
- **Contract observations:** not on this path. monitor.RunAll runs on the session.start route (handlers.go:1030).
- **The l0_* admission/evidence counters and histBC:** these count dispatch attempts, and a replay is a real attempt, so I left them unchanged.
- **SessionEnd replay:** Persist plus Close, which is idempotent.
- **PreCompact replay:** under the ordering gate, no delivery of that session is applied between two replays, so the successor segment has no tokens and CloseSegmentForCompaction leaves it open.
- **Compact SessionStart replay:** it re-anchors lastCompactionTS to the replay time. That is a timestamp, not an accumulator, and it runs on reply work after the route returns. I did not change it; see open_issues.

Evidence:
- **Red on 738d67c7:** all 5 new rows fail on the old product code. The unit rows show 1400 vs 700, the detector state differs, the anchor moves by +60000 ms, and the restart rows show 1700 vs 1000 and 1400 vs 700. The integration rows show 16 vs 8, and every fixture-sanity assertion before that final check passed.
- **CLI row, deterministic overlays (not committed):** an -overlay of ingest.go that holds every observe.tool commit until Stop cancels it gave 8 vs 16 three times out of three on the old code, and passed five out of five with the fix. Adding a drain.go overlay that also cuts Stop's drain commit gave 8 vs 20 three times out of three, each in a fresh process, on the old code (the cross-restart path), and 8 three times out of three with the fix.
- **After the fix:** the new rows pass 100/100 at -count=20 and 15/15 at -race -count=3. The restart row passes 50/50 at -count=50, plus 15/15 cold runs in fresh processes, and 3/3 under -race. internal/daemon and internal/cli each pass as a whole package.

Status of the two findings:
- Finding 1 (ci, major): fixed.
- Finding 2 (complete, major): fixed. Its missing disposition is now: product defect, red-first rows, fix 64508520. The coordinator still has to record that in the ledger (see needs_owner).

### Commits

- 64508520 fix(daemon): apply a redelivered delivery to the scheduler once
- 3e0a223c test(cli): say why the restart row holds under a cut commit
- 47043d9b docs(architecture): describe the scheduler tap's redelivery rule

### Findings resolution

- **fixed**: ci/major: a WAL replay double-counts open-segment tokens (scheduler_tap.go:201 with ingest.go:961-976, daemon.go:1213); intermittent 8-vs-12 red in TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook
  - Confirmed: the tap ran inside run(), before commitDelivery and before the drain's commit, and its fold had no guard against duplicates. The fix applies each delivery once per observation identity. It keeps one identity per session, which the ordering gate makes sufficient, and claims it under the same lock hold as the state it guards. The bound session's identity is persisted with the account (last_applied_observation), restored at bind, and seeded at construction for the unbound restart case. A replay is counted under sched.tap.redelivery. The same rule covers the detector observation, the Stop seam and the prompt-capture anchor. The new rows in internal/daemon/scheduler_tap_redelivery_test.go cut the commit with a context cancelled between the handler and the commit, which is exactly what Stop does, so no row depends on a wall-clock margin. All five were red on 738d67c7 and are green after the fix. The restart row passes 50/50 at -count=50, 15/15 cold runs in fresh processes, and 3/3 under -race. Under adversarial overlays that cut every commit (and also Stop's drain commit) it was red 3/3 on the old code (16, and 20 across the restart) and is green on the fix.
- **fixed**: complete/major: the w19-loudcfg 8-vs-12 red in TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook has no disposition; schedTap.observeTool folds without a per-tool_use_id guard
  - Disposition: a real product double count (at-least-once redelivery folded twice), not a test-fixture flake. It is fixed at the source in 64508520 with red-first rows. I keyed the dedupe on the delivery's ObservationID rather than the tool_use_id, so it also covers Stop and prompt deliveries, and a client copy with the same nonce carries the same identity. I kept the test exercising the real interleaving instead of adding an ack-wait, because the fix makes its assertion hold under every interleaving. Its comments now say so (3e0a223c). Recording this in the ledger is the coordinator's step (see needs_owner).

### Tests

- `go test -p 1 -count=1 -overlay <red-overlay defining counterTapRedelivery> -run '^TestWrapServices_ARedeliveredToolUseIsAppliedOnce$' ./internal/daemon/ (on 738d67c7 product)`: FAIL as intended: open segment 1400, expected 700
- `go test -p 1 -count=1 -overlay <red-overlay> -run '^TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce$' ./internal/daemon/ (on 738d67c7 product)`: FAIL as intended: stop subtest detector state differs (463 vs 327 bytes); captured prompt subtest anchor moved by +60000 ms
- `go test -p 1 -count=1 -overlay <red-overlay> -run '^TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain$' ./internal/daemon/ (on 738d67c7 product)`: FAIL as intended: 1700 vs 1000 (replayed before bind); 1400 vs 700 (replayed after a SessionStart bind)
- `go test -p 1 -count=1 -overlay <red-overlay> -run '^TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce$' ./internal/daemon/ (on 738d67c7 product)`: FAIL as intended: 16 vs 8, every fixture-sanity assertion before it passed
- `go test -p 1 -count=1 -overlay <red-overlay> -run '^TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce$' ./internal/daemon/ (on 738d67c7 product)`: FAIL as intended: 16 vs 8
- `QOMPACK_W20_CUT_COMMIT=1 go test -p 1 -count=3 -overlay <ingest.go cut-commit overlay> -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/ (738d67c7 product)`: FAIL 3/3: expected 8, actual 16 (deterministic)
- `QOMPACK_W20_CUT_COMMIT=1 QOMPACK_W20_CUT_DRAIN_ONCE=1 go test -p 1 -count=1 -overlay <ingest+drain cut overlays + HEAD scheduler files> -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/ x3 fresh processes`: FAIL 3/3: expected 8, actual 20 (cross-restart replay path)
- `QOMPACK_W20_CUT_COMMIT=1 go test -p 1 -count=5 -overlay <ingest.go cut-commit overlay> -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/ (fix)`: ok 5/5
- `QOMPACK_W20_CUT_COMMIT=1 QOMPACK_W20_CUT_DRAIN_ONCE=1 go test -p 1 -count=1 -overlay <ingest+drain cut overlays> -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/ x3 fresh processes (fix)`: ok 3/3
- `go test -p 1 -count=20 -run '^TestWrapServices_ARedeliveredToolUseIsAppliedOnce$' (and each of TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce, TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain, TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce, TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce) ./internal/daemon/`: ok, 100/100 top-level PASS (run as one -count=20 process)
- `go test -race -p 1 -count=3 on the same five rows ./internal/daemon/`: ok, 15/15 PASS, no DATA RACE
- `go test -race -p 1 -count=3 -run '^TestWrapServices_' ./internal/daemon/`: ok
- `go test -p 1 -count=50 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok, 50/50 PASS
- `cli.test.exe -test.count=1 -test.run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' x15 fresh processes`: 15/15 pass (cold)
- `go test -race -p 1 -count=3 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok
- `go test -p 1 -count=20 -run '^TestAHostFailedCompactionsSuccessorClosesAtSessionEnd$' ./internal/cli/`: ok
- `go test -race -p 1 -count=3 -run '^TestAHostFailedCompactionsSuccessorClosesAtSessionEnd$' ./internal/cli/`: ok
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon/`: ok (902.6s)
- `go test -p 1 -count=1 -timeout=30m ./internal/cli/`: ok (231.0s)
- `go test -p 1 -count=1 ./test/docs ./test/guards`: ok, ok
- `GOOS=windows|linux|darwin go vet ./internal/daemon/ ./internal/cli/`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/ ./internal/cli/ (host, and host-built binary with GOOS=linux)`: clean (exit 0) both
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers

### Criterion changes

- None. No existing assertion, bound or golden changed. The only edit to an existing test file, internal/cli/segment_roll_live_test.go, is comments: rigReadAndSettle's doc wrongly said the prompt's answer proved the Read's tap had run, and TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook's doc now explains why it holds under a cut commit.

### Open issues

- A compact SessionStart that a drain replays (a spooled hook) re-anchors lastCompactionTS and lastActivity to the tap's clock time each time it runs. A second replay after a cut commit moves the anchor forward by the gap between the two replays. It is a timestamp, not an accumulator, and the seam runs on reply work after the route has returned (session_start_compact.go startReplyWork), so a claim keyed on the line would not line up with that line's commit. Left unchanged; routed to the coordinator as low impact.
- The per-session applied map holds one small entry per session this daemon applied a delivery of, for the daemon's lifetime. That is the same lifetime as the observer's much larger per-session state, and the daemon exits after IdleExitSeconds (1800) with no live session. No cap was added, so no new number or constant was introduced.
- The new scheduler.json reader seedApplied reads through readStateFile, which test/guards' sharedReaders inventory already lists. readStateFile is now a plain function and keeps its row; I added no guard row and did not touch test/guards.

### Needs owner

- Record the disposition of the w19-loudcfg 8-vs-12 red in the ledger: a product defect (at-least-once redelivery folded twice by the scheduler tap), fixed in 64508520, rows in internal/daemon/scheduler_tap_redelivery_test.go. I did not edit V6-CLOSEOUT-CHECKLIST.md because it is read-only for this seat.
- docs/architecture.md §4 got one new paragraph (a separate hunk from the drain seat's lines 176-179). The coordinator should check that the two seats' edits merge cleanly.

## review:redeliver:0:r1: verdict `needs-fixes`, 3 finding(s)

- **minor** `internal/daemon/scheduler_tap.go:208-213 (also internal/daemon/scheduler_runtime.go:627, the changepoint close inside applyToolUse)`: This is a regression introduced by 64508520. A recognized replay now returns before closeOnBoundary, so a segment close the first run failed to make is never retried. The claim and fold happen under the lock, but the todo/test/commit boundary close (and a changepoint close inside observeLocked) runs with the delivery's ctx and can fail when Stop's runCancel lands. The claim already records the delivery as applied, so the replay skips the close. On the base code the replay ran the close again; it also double-folded, but the close was not lost. The side effect has gone from at-least-once to possibly never. The observer handles the same interleaving the other way: on a redelivery it repairs the effect the first run missed (tooluse.go:134 repairFileVersion; its comment cites a Stop's runCancel landing between the record and a later effect under co-load, x05).
  - Evidence: Code path: FSStore.ToolUse (store/tooluseindex.go:487) ignores ctx, so the tap claims and folds even when the ctx is cancelled. segLog.Close (store/segments.go:394) returns ctx.Err(), so the close fails and is logged at Debug. runIngested returns non-OK, the WAL line is kept, and Stop's drain replays it with a live ctx, where the replay is recognized and skips the close. Probe added via -overlay as internal/daemon/zz_probe_redeliver_test.go (TestProbe_A2_ReplayAfterCancelledBoundaryClose): a rtFixture with an open segment and a 50-token Bash `git commit` record; the first ObserveTool runs with the identity and a cancelled ctx, then the replay runs with a live ctx. HEAD: after the replay the current segment is still id=1 start=0, open, redelivery=1, and the commit boundary at turn 9 is never closed (FAIL). Base product (738d67c7 scheduler files via overlay): after the replay the current segment is id=2 start=10 (closed, but with the tokens doubled) (PASS).
  - Fix: On a recognized replay, retry an incomplete boundary close instead of returning straight away. Keep a per-session pending close next to the applied identity: {turn, features, cause}, set inside applyToolUse/applyStop when boundaryCause(sig) != "" or when the changepoint close in observeLocked fails, and cleared when CloseSegmentOn/closeSegmentLocked succeeds. On a replay (applied == false), retry it if it is still set. CloseSegmentOn is already idempotent: once the first close landed, the successor starts at at+1 and `at < cur.StartTurn` returns nil. Use stored features, because FeaturesFrom mutates the FeatureHistory. Add a red-first row like the probe above, plus a changepoint variant.
- **minor** `internal/daemon/scheduler_state.go:267 (with scheduler_tap.go:338, applyToolUse having no session guard)`: The cross-restart dedupe covers only the bound session. The tap folds every session's tool-use tokens into the one bound account; applyToolUse has no e.SessionID == r.session check, while CloseSegmentForCompaction refuses foreign sessions. saveStateLocked persists only r.applied[r.session], and seedApplied seeds only doc.Session. Suppose a second live session's delivery is folded into the bound account and persisted, and the daemon stops with that delivery's commit cut. The restarted daemon's drain replays it, nothing recognizes it, and it is folded into the unbound accumulator, which bindUnboundLocked adds on top of the restored account that already holds it. This is pre-existing (the base code gives the same number), but the task asked for exactly once across restarts whenever the counter is persisted.
  - Evidence: Probe TestProbe_B_OtherSessionsReplayAfterRestart (overlay): prev runtime bound to sess-c1 applies its own 700-token Read (identity sess-c1/1) and a 300-token Read of sess-other (identity sess-other/1), giving an account of 1000, then Persists. The restarted unbound runtime with a real registry replays sess-other/1 (not live, so no bind). sess-c1 is touched, and its live 100-token Read binds. Expected 1100, actual 1400 on HEAD; base also gives 1400.
  - Fix: Choose one. (a) Persist the applied identity of every session whose delivery was folded into the account since the last bind, e.g. last_applied_observations as a small map, omitempty and additive like the current field, and seed and restore from all of its entries. (b) Stop folding a foreign session's tool use into a bound runtime's account, the same rule CloseSegmentForCompaction already applies: count it, do not fold. Either way add a restart row with two sessions.
- **nit** `internal/daemon/scheduler_tap.go:196-206 (no-record path)`: The no-record path claims nothing (deliberately, so a later replay that finds the record still applies). It still calls NotifyActivity(now) and NoteRequestStart(now) on every replay. A tool use whose record is absent for good, such as a capture refused by CaptureScope (terminal, no record), therefore moves the request-start anchor to the replay instant each time its cut commit is replayed. That is the same anchor drift the seat fixed for prompt captures (+60000 ms in its row).
  - Evidence: The code at lines 196-206 runs NotifyActivity(now), NoteRequestStart(now) and NoteEffort(e) on every call that gets core.ErrNotFound, and calls neither claimDeliveryLocked nor any per-delivery guard. observer/tooluse.go:110-114 shows a scope refusal returns with no record and is terminal.
  - Fix: Guard the anchors on the no-record path with a separate per-session "anchored" identity. Record it without claiming the full application, so a later replay that finds the record still folds. Or document it as an accepted timestamp-only drift alongside the compact SessionStart open issue.

## fix:redeliver:r1: status `done`, head `f094f7870e3cb6fedd466fcd346fe46cb0664a06`

### Root cause

Round 1: the handler, with the scheduler tap inside it, runs before the delivery's commit (ingest.dispatch runs run() then commitDelivery; drainer.dispatchPending runs Dispatch then commitDelivery). A cut commit therefore replayed the delivery through a tap with no per-delivery guard. This round: the round-1 guard returned before the tap's segment close, which runs with the delivery's ctx and fails together with the commit under Stop's runCancel, so a replay never made it. The persisted guard covered only the bound session's identity, while the tap folds every session's tool use into the bound account. The no-record path, which deliberately claims nothing, re-noted its clock anchors on every replay.

### Summary

PRODUCT CODE CHANGED (internal/daemon/scheduler_tap.go, scheduler_runtime.go, scheduler_state.go, scheduler_frontier.go).

Branch closeout/w20-redeliver in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-redeliver. Base 738d67c7, round-1 head 47043d9b, new head f094f787. Nothing pushed or merged. No attribution trailers.

**Original audit findings (round 1, still fixed).** A WAL replay double-counted open-segment tokens. The ingest worker and the drain run the handler, with the scheduler tap inside it, before commitDelivery. When Stop's runCancel or a bounded drain context cut the commit, the line was replayed and the tap folded it a second time. 64508520 makes the tap apply each delivery once, keyed by its ObservationID: one identity per session (the ordering gate makes one enough), claimed under the same lock hold as the state it guards, and persisted. TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook is deterministic: 50/50 again this round.

## Review resolution

**R1 (minor, a replay skipped a boundary close the cut run missed): held, and a regression from 64508520. Fixed in 0844c392.**
- Verified: FSStore.ToolUse and the claim ignore ctx, but segLog.Close returns ctx.Err(). So Stop's runCancel landing mid-tap fails the close and the commit together. The recognized replay then returned before closeOnBoundary, and the close was never made.
- Fix: the applied entry now records the close the delivery owes (owedClose). That is the boundary its signals name, or a changepoint whose close failed (observeLocked now returns that error). The applying run attempts it, and each replay attempts it again until one run makes it.
- Why it cannot double-close: closeSegmentLocked is idempotent at its turn (the successor starts at turn+1).
- A changepoint close carries the delivery's own tokens to the successor, as an uncut run does. Stored features are reused, because FeaturesFrom mutates history.
- A rebind drops owed closes; they would otherwise close the new session's segment.
- Boundary counters now count once per applied delivery.
- Red rows on 47043d9b (all fixture-sanity checks before the final assertion pass):
  - TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed: segment never closed.
  - TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed: segment never closed.
- The same rows on the 738d67c7 product, via overlay: the boundary close is made but with tokens 100 instead of 50, confirming the reviewer's account; the changepoint close is not retried at all.

**R2 (minor, the cross-restart dedupe covered only the bound session): held, pre-existing. Fixed in a704a731 with the reviewer's option (a).**
- I did not choose (b), stop folding other sessions' tool uses into the bound account: that changes multi-session scheduling semantics, which is outside this C1.x durability/accounting seat.
- state/scheduler.json now carries last_applied_observations, a map from session to identity for every session whose delivery the persisted account holds.
  - An entry is "held" from its claim, or from the restore of the account it came with, until a rebind starts a new account.
  - bindUnboundLocked keeps the unbound runtime's claimed entries held, as it keeps their tokens.
  - seedApplied seeds every entry.
  - The map replaces round 1's unshipped last_applied_observation. It is omitempty and additive, so the version stays 1.
- Red row TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain: 1400 vs 1100 when the replay comes before the bind, 1300 vs 1000 after a SessionStart. The base gives the same numbers.

**Nit (no-record replay moved the anchors): held. Fixed in f094f787.**
- The entry now records a separate anchored identity, without claiming the delivery, so a replay notes nothing and is counted under sched.tap.redelivery.
- A later replay that finds the record still applies it.
- Red row TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor: both anchors moved +60000 ms.

**Rows.** The four new rows are in internal/daemon/scheduler_tap_redelivery_test.go. The cut is a context cancelled before the tap, which is equivalent because the close is the tap's only ctx-sensitive step. No row waits on a clock.

**Other changes.**
- CloseSegmentOn has no product caller now. I kept its contract, because the frontier tests drive it, and corrected the three comments that said the tap calls it.
- docs/architecture.md §4: the round-1 paragraph is rewritten in place, still a separate hunk from the drain seat's lines 176-179.
- Each of the three commits builds and passes its rows plus every TestWrapServices_, TestFrontier_, TestRuntime_, TestStateCodec_ and TestNewSchedulerRuntime_ row.

**Evidence.**
- New rows: 80/80 at -count=20. With the round-1 rows, 27/27 under -race -count=3, no data race. Round-1 rows: 100/100 at -count=20.
- CLI restart row: 50/50 at -count=50.
- internal/daemon in full: the first run (pre-commit tree, which differs from HEAD only in comments) had one red, TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend. That row is outside my files and has no scheduler code on its path; it passed 10/10 alone. A second full run on HEAD passed (654 s).
- internal/cli in full: passed.
- vet on windows, linux and darwin, golangci-lint (host and GOOS=linux), fmt-check, docmarkers/runpatterns, test/docs and test/guards: all clean.

### Commits

- 64508520 fix(daemon): apply a redelivered delivery to the scheduler once
- 3e0a223c test(cli): say why the restart row holds under a cut commit
- 47043d9b docs(architecture): describe the scheduler tap's redelivery rule
- 0844c392 fix(daemon): make the segment close a cut run owed on replay
- a704a731 fix(daemon): persist every session's applied delivery identity
- f094f787 fix(daemon): note an unrecorded tool use's anchors once

### Findings resolution

- **fixed**: audit ci/major: a WAL replay double-counts open-segment tokens (scheduler_tap.go:201, ingest.go:961-976, daemon.go:1213); intermittent 8-vs-12 red in TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook
  - Fixed in 64508520 (round 1). The tap applies each delivery once by its ObservationID: one identity per session, claimed under the lock with the state it guards, persisted and seeded across restarts. a704a731 widens the persistence to every session folded into the account. The restart row passes 50/50 at -count=50 this round. The round-1 rows pass 100/100 at -count=20 and under -race.
- **fixed**: audit complete/major: the w19-loudcfg 8-vs-12 red has no disposition; schedTap.observeTool folds without a per-delivery guard
  - Disposition: a real product double count (at-least-once redelivery folded twice), not a fixture flake. Fixed at the source in 64508520, with rows in internal/daemon/scheduler_tap_redelivery_test.go. Recording it in the ledger is the coordinator's step (needs_owner).
- **fixed**: review R1 (minor): a recognized replay returns before closeOnBoundary, so a boundary or changepoint close the cut run failed is never made (regression from 64508520)
  - Verified: segLog.Close returns ctx.Err() while ToolUse and the claim ignore ctx. 0844c392 records the close a delivery owes (boundary, or a changepoint whose close failed) on its applied entry. The applying run and every replay attempt it until it is made; closeSegmentLocked is idempotent at its turn. A changepoint close carries the delivery's tokens to the successor; a rebind drops owed closes. Rows TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed and TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed were red on 47043d9b (never closed) and are green at -count=20 and under -race. On the base, the boundary close was made with tokens 100 instead of 50, and the changepoint close was never retried.
- **fixed**: review R2 (minor): cross-restart dedupe covers only the bound session, though the tap folds every session's tool use into the bound account (pre-existing)
  - Reviewer's option (a), in a704a731. state/scheduler.json carries last_applied_observations: the identities of every session whose delivery the persisted account holds. An entry is held from its claim or restore until a rebind; bindUnboundLocked keeps the unbound runtime's entries held; seedApplied seeds them all. The field is additive and omitempty, and the version stays 1. Option (b), not folding foreign tool uses, would change multi-session scheduling semantics and is outside this seat. Row TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain: red 1400/1300 on both HEAD and base, green now.
- **fixed**: review nit: the no-record path moves the activity/request-start anchors to the instant of every replay
  - f094f787: anchorUnrecorded records a per-session anchored identity without claiming the delivery. A replay notes nothing and is counted as a redelivery; a replay that finds the record still applies it. Row TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor: red on HEAD and base (+60000 ms on both anchors), green now.

### Tests

- `go test -p 1 -count=1 -run '^(TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed|TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed|TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain|TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor)$' ./internal/daemon/ (on 47043d9b product)`: FAIL as intended: boundary and changepoint rows 'segment not closed'; restart row 1400 vs 1100 and 1300 vs 1000; anchor row moved +60000 ms. Every fixture-sanity assertion before these passed.
- `same four rows with -overlay mapping the 738d67c7 scheduler_tap/runtime/state/frontier files plus a const file for counterTapRedelivery`: FAIL: boundary close made with tokens 100 vs 50; changepoint close not made; restart 1400/1300; anchors moved
- `go test -p 1 -count=20 -run '^(TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed|TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed|TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain|TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor)$' ./internal/daemon/`: ok, 80/80 top-level PASS
- `go test -race -p 1 -count=3 -run '^(TestWrapServices_ARedeliveryMakesTheBoundaryCloseItsCutRunMissed|TestWrapServices_ARedeliveryMakesTheChangepointCloseItsCutRunMissed|TestWrapServices_AnotherSessionsRedeliveryAfterARestartIsNotFoldedAgain|TestWrapServices_ARedeliveryOfAnUnrecordedToolUseMovesNoAnchor|TestWrapServices_ARedeliveredToolUseIsAppliedOnce|TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce|TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain|TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce|TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce)$' ./internal/daemon/`: ok, 27/27 PASS, 0 DATA RACE
- `go test -p 1 -count=20 -run '^(TestWrapServices_ARedeliveredToolUseIsAppliedOnce|TestWrapServices_ARedeliveredStopOrCapturedPromptIsAppliedOnce|TestWrapServices_ARedeliveryAfterARestartIsNotFoldedAgain|TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce|TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce)$' ./internal/daemon/`: ok, 100/100 PASS
- `go test -p 1 -count=50 -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok, 50/50 PASS
- `per commit (0844c392, a704a731, f094f787): go build + go vet + devtool fmt-check + go test -p 1 -count=1 -run '^(TestWrapServices_.*|TestIngest_AStopThatCutsTheCommitFoldsTheReplayedReadOnce|TestDrain_AnInterruptedCommitFoldsTheReplayedReadOnce|TestFrontier_.*|TestRuntime_.*|TestStateCodec_.*|TestNewSchedulerRuntime_.*)$' ./internal/daemon/`: ok at each commit (29 / 82 / 83 top-level PASS)
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon/ (pre-commit tree, comments differ from HEAD)`: FAIL: only TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend/format_1,_the_v1_sidecar/position_chain_during_evaluation ('the return of acknowledgement 0 never happened', a 10 s ingestACKWait bound; 44.9 s under co-load). Not my code.
- `go test -p 1 -count=10 -run '^TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend$' ./internal/daemon/`: ok, 10/10 PASS (8.4-10.7 s each alone)
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon/ (HEAD f094f787)`: ok (654.0s)
- `go test -p 1 -count=1 -timeout=30m ./internal/cli/ (HEAD)`: ok (142.6s)
- `GOOS=windows|linux|darwin go vet ./internal/daemon/ ./internal/cli/ (HEAD)`: clean on all three
- `golangci-lint run ./internal/daemon/ ./internal/cli/ (binary built with go build -modfile=tools/pinned/go.mod; host and GOOS=linux; also go run -modfile form before commit)`: exit 0 for all
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 1 -count=1 ./test/docs ./test/guards`: ok, ok

### Criterion changes

- None. No existing assertion, bound or golden changed. Four rows were added to internal/daemon/scheduler_tap_redelivery_test.go. The persisted scheduler.json field last_applied_observation (round 1, unshipped) became the map last_applied_observations; no test or document referenced the old key.

### Open issues

- Carried from round 1: a compact SessionStart that a drain replays re-anchors lastCompactionTS and lastActivity to the tap's clock on each run. It is a timestamp, not an accumulator, and runs on reply work after the route returns (session_start_compact.go startReplyWork). Left unchanged, low impact.
- Carried from round 1, updated: the in-memory applied map holds one small entry per session this daemon saw a delivery of, for the daemon's lifetime (IdleExitSeconds 1800 bounds an idle daemon). The persisted last_applied_observations holds one entry per session whose delivery reached the current account, and is reset when a rebind starts a new account. It has no cap, so a single long-lived account touched by many concurrent sessions grows by about 110 bytes per session. No new number was introduced.
- Carried from round 1: seedApplied reads through readStateFile, which test/guards' sharedReaders inventory already lists. No guard row was added.
- New, narrow: an owed close is not persisted. If the first run's close, Stop's drain replay of it, and that replay's commit are all cut, and the restarted daemon replays the delivery after a SessionStart has bound the session, the close is not made. When the replay comes in the startup drain (the usual case), the base made no close either: an unbound runtime closes Current(""), which is never found. Persisting it would mean deferring the retry to the bind; not done.
- Observed, pre-existing, outside this seat: the tap's boundary close, now the owed close, closes the BOUND session's segment even for another session's tool use, the same cross-session design under which that session's tokens are folded into the bound account. Base behaviour is unchanged; routed to the coordinator.
- Observed, outside this seat's files: TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend failed once in a whole-package run under co-load. Its awaitClosed fixed 10 s bound (ingestACKWait) fired after a 44.9 s test against about 9 s alone, and it passed 10/10 in isolation. This is a wall-clock-margin wait, which D-rules forbid as a pass/fail criterion; it belongs to whoever owns delivery_ack_groupcommit_test.go / ingest_test.go.

### Needs owner

- Record the disposition of the w19-loudcfg 8-vs-12 red in V6-CLOSEOUT-CHECKLIST.md: a product defect (at-least-once redelivery folded twice by the scheduler tap). Fixed in 64508520 and hardened by 0844c392, a704a731 and f094f787; rows in internal/daemon/scheduler_tap_redelivery_test.go. The checklist is read-only for this seat.
- docs/architecture.md §4: this seat's paragraph was rewritten in place in a separate hunk from the drain seat's lines 176-179. Confirm the two seats' edits merge cleanly.
- Route the load-sensitive TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend (10 s ingestACKWait bound) to the seat that owns the delivery-journal tests.

## review:redeliver:0:r2: verdict `sound`, 0 finding(s)


## verify:redeliver: verdict `sound`, 0 finding(s)



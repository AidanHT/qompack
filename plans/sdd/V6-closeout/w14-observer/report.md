# Wave 14 — w14-observer (D46 follow-ups)

Branch `closeout/w14-observer`. Workflow `wf_e9768966-e7a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `31e750d7bdc82c2e82adb8af6a132287801713ea`

### Root cause

(1) internal/observer only ever adopted a segment in OnSessionStart (ensureSegment). The scheduler rolls segments on changepoint, todo, test, commit and compaction in closeSessionSegmentLocked, and nothing tells the observer. Its st.Segment therefore went stale after any roll that no SessionStart followed: members enrolled against the closed id, the closed segment never got its DAG node, and SessionEnd re-closed that id (soft ErrAppendOnly) while the successor stayed open. Two related defects: ensureSegment reset SegStartPos even for the segment it already held, and SessionEnd closed at a turn before the successor's start, which the real log refuses. (2) The scheduler runtime bound only on SessionStart or PreCompact. A daemon restarted mid-session receives neither, so the runtime stayed unbound: Evaluate returned error_no_window and Persist wrote nothing.

### Summary

Both D46 wave-14 follow-ups are fixed on closeout/w14-observer (5 commits on a8c7433). Every fix has a test that was RED before it and is GREEN after it. No new bounds or numbers. No check was weakened.

(1) OBSERVER STALE SEGMENT (the w13-pinsckpt ticket).
Root cause: the observer adopted a segment only in OnSessionStart. The scheduler closes a segment and opens its successor in scheduler_frontier.go closeSessionSegmentLocked, on a changepoint, todo, test, commit or compaction. Nothing tells the observer. So st.Segment kept naming the closed id, and DAG members kept enrolling against it. The closed segment never got a DAG segment node. SessionEnd then closed that id again (soft ErrAppendOnly), and the successor stayed open.
Two related defects in the same lifecycle:
- ensureSegment always reset SegStartPos to the current position, even when the log's open segment was the one already held. So a SessionStart with no roll before it moved the node's StartPos past members already enrolled.
- SessionEnd closed at st.Turn. A roll made for the session's last event opens the successor at turn+1, and the real segLog refuses endTurn < StartTurn. So the session's last segment stayed open for good.
Fix (internal/observer/session.go followSegmentRoll):
- Every entry point (tool use, worker-path prompt, Stop, SessionStart before ensureSegment, SessionEnd before its close) calls it under the session lock. It runs before the event moves the prefix position or enrols anything.
- The common case is one O(1) SegmentLog.Get of the held segment.
- If that segment is closed, the observer builds its DAG node: StartPos = SegStartPos, Tokens up to the boundary, EndTurn from the log. Any segment that was rolled open and closed again in between gets an empty node at the boundary, so the chain edges stay continuous. The open successor is then adopted at the boundary position.
- Position accuracy rests on the daemon's per-session dispatch lanes: each event runs the observer, then the tap, before the next event starts.
- ensureSegment keeps the start of a segment it already holds. Its open branch keeps PrevSegment when the session holds no segment.
- SessionEnd closes at max(turn, segment start).
- A nil SegmentLog is guarded. A held id the log does not know is treated as not rolled; SessionEnd's close reports it.
- New counter observer.segment.followed and new soft stage observer.err.segment.follow.
The ticket reproduction is committed as TestSessionEndClosesTheSuccessorOfAScheduledRoll.

(2) SCHEDULER UNBOUND AFTER A MID-SESSION RESTART.
Root cause: the runtime bound only on SessionStart, or on w13's PreCompact close (bindForCompactionLocked). A restarted daemon gets no SessionStart, so Evaluate answered error_no_window and Persist wrote nothing for the rest of the session.
Fix (internal/daemon/scheduler_runtime.go bindOnFirstHook):
- The tap calls it first on ObserveTool, ObserveStop and the worker's prompt capture.
- On an unbound runtime it binds the hook's session when the daemon registry holds that session live. Every hook route calls registry.Touch before dispatch.
- The bind restores state/bocd.json and state/scheduler.json, then folds in what the runtime observed while unbound. This is w13's merge, renamed bindUnboundLocked now that it has two callers.
- A replayed delivery of a session no hook has touched does not bind. A runtime already bound is never rebound here.
- The prompt reply path (250 ms deadline, no store I/O) never binds. The new exported observer.PromptReplyOnly lets the tap tell the reply path from the worker's capture.
- SessionEnd does not bind, because its route ends the session in the registry first.
- New counters: sched.tap.bind.first_hook and sched.tap.bind.not_live.

COMPOSITION ROWS (internal/cli, shipped runDaemon plus hook clients):
- TestAHostFailedCompactionsSuccessorClosesAtSessionEnd: PreCompact with no SessionStart(compact), a Read, then SessionEnd. The successor must close, the rolled segment gets its node, and the Read is enrolled in the successor. Before the fix the successor was never closed.
- TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook: stop the daemon mid-session and start a new one. Its shutdown must persist the session's max_turn and open_segment_tokens including the new Read. Before the fix: max_turn 1, expected 4.

Docs: plans/00-ARCHITECTURE.md 'Segment lifecycle' now names the commit and compaction rolls and says SP-08 follows every roll.

Defaults taken under D33 (no numbers involved; the coordinator may overrule):
- Liveness means the registry's IsLive; with no daemon or registry attached, every session counts as live.
- A roll with no successor (its Open failed) leaves the observer holding no segment until the next SessionStart. That SessionStart's new segment chains back to the rolled one.
- An empty successor is closed at its start turn rather than left open.

Evidence logs are committed under plans/sdd/V6-closeout/w14-observer/runs/ 01-10. The seat limits were followed: -p 2, no -race, no hot-path rows, no whole test/e2e or test/integration packages, and the Linux container was not started. All background processes I started have exited.

### Commits

- 2981cfc1 fix(observer): follow the scheduler's segment rolls
- 0cb99bbc fix(daemon): bind the scheduler on a session's first hook
- 9726729c test(cli): roll and restart rows through the shipped composition
- 746303fa docs(arch): name every segment roll and who follows it
- 31e750d7 test(v6): record the w14-observer red and green runs

### Tests

- `go test -p 2 -count=1 -run 'TestSessionEndClosesTheSuccessorOfAScheduledRoll|TestEventsAfterAScheduledRollEnrolInTheSuccessor|TestSessionStartAfterACompactionRollEmitsTheRolledSegment|TestSessionStartKeepsTheHeldSegmentsStart|TestTwoRollsBetweenEventsChainEverySegment|TestARollWithoutASuccessorKeepsTheChain|TestSessionEndClosesAnEmptySuccessorOnTheRealLog' ./internal/observer/ (base, fix absent)` — RED as intended, all 7 fail (runs/01). The ticket row has 1 close where 2 are expected; the rolled node's Tokens are 160, expected 80; SegStartPos is 80, expected 0; the rolled node is missing; the real-log successor is not closed.
- `same 7 rows with -v after the fix` — PASS, 7/7 (runs/04)
- `go test -p 2 -count=1 -run 'TestWrapServices_FirstToolAfterARestartBindsTheLiveSession|TestWrapServices_FirstStopOrCapturedPromptAfterARestartBinds|TestWrapServices_ReplyOnlyPromptDoesNotBind|TestWrapServices_AReplayOfASessionNoHookTouchedDoesNotBind|TestWrapServices_AHookOfAnotherSessionLeavesTheBoundOneAlone' ./internal/daemon/ (base, bind absent, counter names present)` — RED as intended (runs/02). The first-tool, stop and captured-prompt rows see session "" where sess-c1 is expected; the replay row's not_live counter is 0, expected 1. ReplyOnly and AHookOfAnotherSession are guard rows and are green on the base by design.
- `same 5 rows with -v, plus TestWrapServices_PreCompactAfterARestartClosesWithThePersistedAccount, TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince and TestWrapServices_PreCompactWithNothingObservedLeavesTheSegmentOpen, after the fix` — PASS, all (runs/05)
- `go test -p 2 -count=1 -run 'TestAHostFailedCompactionsSuccessorClosesAtSessionEnd|TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook' ./internal/cli/ (the nine implementation files stashed)` — RED as intended (runs/03). The successor is never closed ('Condition never satisfied'); max_turn is 1, expected 4.
- `same 2 rows with -v after the fix` — PASS in 1.49s and 1.86s (runs/06)
- `go test -p 2 -count=1 ./internal/observer/...` — ok: observer in 91.986s, observertest in 3.023s (runs/07)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok in 360.464s (runs/08)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok in 76.888s (runs/09)
- `go run ./tools/devtool fmt-check; go vet ./internal/observer/... ./internal/daemon/ ./internal/cli/ (Windows and GOOS=linux)` — all exit 0 (runs/10)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS. runpatterns and docmarkers were re-run PASS after the evidence commit.
- `go test -p 2 -count=1 ./test/docs` — ok in 2.150s

### Criterion changes

- No assertion was loosened, deleted or skipped.
- Observer test fakes (session_test.go fakeSegLog): the fake now keeps its segments by id the way the real segLog does, and gains Get and Range. For a segment it knows, Close now refuses what the real log refuses: a second close at a different turn, and an end turn before the start turn. Closes of ids it never saw are recorded as before. setCurrent registers the segment and bumps nextID. Rationale: the observer now reads the log, and the fake must not accept closes the real log refuses. This tightens the fake.
- Observer fakes_test.go: the plain fakeStore gains Segments(), which returns an empty fake log. Before, Segments() panicked on the nil embedded store.Store. Rationale: TestGraph_SegmentMembershipPointsIntoTheSegment sets st.Segment directly, and the new per-event roll check calls Segments(). An empty log answers ErrNotFound, which the observer reads as 'not rolled', so that row still pins exactly what it pinned before.
- Coverage shift, assertions unchanged: TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince (w13) still passes. Its fixture has no daemon attached, so its ObserveTool now binds the runtime on that first hook, and the PreCompact finds the runtime bound. The merge arithmetic (turn = max, tokens = sum) is now exercised through bindOnFirstHook by TestWrapServices_AReplayOfASessionNoHookTouchedDoesNotBind: persisted 700 + 300 observed unbound + 200 live = 1200. The compaction branch of bindUnboundLocked is still exercised by TestWrapServices_PreCompactAfterARestartClosesWithThePersistedAccount.

### Open issues

- Pre-existing race, narrowed but not closed. PreCompact is handled outside the per-session dispatch lane. Suppose a tool event has passed the observer but not yet the tap when PreCompact closes at the tap's maxTurn. That event stays a DAG member of the rolled segment (and counts in its node's Tokens), while the store's turn range places it in the successor, and the tap then folds its tokens into the successor. This is inherent to PreCompact running beside the lane; w13 documented it.
- Pre-existing, outside this scope: the scheduler runtime serves one session, and the tap does not check which session an event belongs to (observeTool, observeStop, closeOnBoundary). Two concurrent sessions in one daemon therefore mix: the second session's boundary signals close the bound session's segment at the second session's turn. For the same reason, events of a non-live session observed while unbound are merged into the live session's account when it binds.
- Pre-existing, found by reading code, not reproduced: the tap re-runs on an at-least-once redelivery. The observer absorbs the redelivery, but observeTool still looks up the record and calls AddOpenSegmentTokens, so a redelivered tool use is counted twice in openSegTokens.
- Found in passing: WireObserver captures o.Sched before wireScheduler sets it (internal/cli/daemon.go orders WireObserver first). So the observer's OnSignals and OnFeatures scheduler callbacks are nil in production, and the tap is the scheduler's only feed. The comment 'nil through wave 2' is stale. Nothing is broken by it, but the dead path is misleading.
- Progress() (the timeline's live view) reports the pre-roll segment until the session's next event reaches the observer. internal/mcp liveProgress already tolerates this by keeping the log's value for a segment the observer is not enrolling into.
- The header comment of scheduler_frontier.go (the 'two entry points' paragraph) still lists only the changepoint, todo, test and commit closes, not the compaction close. The w13 reviewer's nit about the checkpoint coldEncodeWindow comments also remains; those are checkpoint files, outside this seat.
- No Linux or -race runs. The seat limits forbid -race on whole packages, and the container is stopped. Linux coverage is GOOS=linux go vet only.

## Independent review

### review:observer: needs-fixes

- **minor** `internal/observer/session.go:209-276 (followSegmentRoll), with internal/daemon/scheduler_frontier.go:169-174 and internal/checkpoint/writer.go:804-815` — When a roll lands in the middle of a turn, the rest of that turn is filed in two different segments. The DAG and the token accounting put it in the successor, but the successor's turn range and the checkpoint encoder put it in the rolled segment. Mid-turn rolls are the common case: a todo, test, commit or changepoint roll can fire on any tool use, and a turn usually holds several tool uses. Nothing documents or tests this.
  - Evidence: Tool uses take st.Turn without advancing it (tooluse.go:171). Only prompts and Stops advance it (prompt.go:223, stop.go:155). The scheduler closes at rec.Turn=t and opens the successor at t+1 (scheduler_frontier.go:169-174). The next tool use in the same turn t goes through followSegmentRoll, is enrolled in the successor (Ref "t+1-..."), and its tokens count in the successor's node. checkpoint encodeSegmentLocked picks nodes by n.Turn in [StartTurn, EndTurn], so it encodes that tool use with the rolled segment. The followSegmentRoll comment says the rolled node "spans exactly what was enrolled in it", which holds for positions but not turns. Every new row puts the roll at a turn boundary (the next event is a Stop, a prompt or a second roll), so no row covers a mid-turn roll. The implementer's open issues name this mismatch only for the PreCompact race.
  - Fix: Pick one behaviour and pin it with a test: a roll at a tool use in turn t, then a second tool use in the same turn t, then check which segment the second tool use is in. If the current behaviour (follow token positions) is intended, state the turn-range mismatch in the followSegmentRoll doc comment and in the open issues for the coordinator. The alternative is to adopt the successor only once st.Turn > held.EndTurn, so DAG membership matches the turn ranges and the checkpoint; that trades away agreement with the scheduler's openSegTokens, so it needs a coordinator decision.
- **minor** `internal/daemon/scheduler_tap_test.go:525-558 (TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince); internal/daemon/scheduler_frontier.go:95-99` — No test now exercises the merge that CloseSegmentForCompaction does on an unbound runtime (seenTokens > 0 folded in at PreCompact). The w13 row named for it now binds at its ObserveTool, because its fixture has no daemon and every session counts as live. By the time PreCompact runs, the runtime is already bound. The row still passes, but its name no longer matches what it tests.
  - Evidence: This is the implementer's criterion_changes item 3. After the fix, the only way into CloseSegmentForCompaction's unbound branch with seenTokens > 0 is observations of sessions the registry does not hold live, and no row drives that. TestWrapServices_PreCompactAfterARestartClosesWithThePersistedAccount runs with seenTokens == 0 and returns early.
  - Fix: In that w13 row, attach a registryDaemon (as scheduler_tap_restart_test.go does) whose registry does not hold rtSession live when ObserveTool runs. The runtime then stays unbound until PreCompact, and the existing assertions (EndTurn = tapToolUseTurn+3, Tokens = 1000) again test the compaction path's merge. Alternatively, rename the row and add one that does.
- **nit** `internal/daemon/scheduler_frontier.go:20-22` — The file header still says 'Two entry points reach closeSegmentLocked' and lists only the changepoint, todo, test and commit closes. The compaction close (CloseSegmentForCompaction to closeSessionSegmentLocked) is missing. This seat edited this file and noted the staleness in its open issues but left it unfixed.
  - Evidence: The header comment at lines 20-22. The seat rewrote the comment of CloseSegmentForCompaction at lines 78-91 in the same file.
  - Fix: Update the header to name the third entry point (CloseSegmentForCompaction, cause compact, called from the PreCompact tap). Also note that SP-08 follows every roll itself (session.go followSegmentRoll).
- **nit** `internal/daemon/scheduler_runtime.go:405-410 (bindOnFirstHook)` — When no Daemon is attached, every session counts as live. In production r.d is set only by RegisterSchedulerIdleWork, and a failure there (for example a nil Idle controller) is logged Loud and the daemon keeps running. With r.d nil, the startup WAL drain binds the runtime to the first replayed session, possibly a dead one, and the live session then stays unbound until a SessionStart.
  - Evidence: scheduler_idle.go:74-89 sets r.d only after the nil-daemon, runtime-type and Idle() checks pass. scheduler_wiring.go:84-86 logs the failure Loud and continues. The fail-open default is one of the implementer's D33 defaults, and it exists mainly so the tests can run without a daemon.
  - Fix: Record this in the D33 defaults. Alternatively, attach the daemon before the idle registration's early-return checks (set r.d as soon as d is non-nil), so liveness is always known in production and the fail-open branch is reached only in tests.
- **nit** `internal/observer/session.go:276` — observer.segment.followed also fires in one case that is not a scheduler roll. A daemon crashes between an observer SessionEnd (whose step 5 persists the ending session with its closed Segment id) and the next persist, and the session then resumes in a new daemon. followSegmentRoll finds its own closed segment, rebuilds that segment's DAG node (a redundant deps.jsonl record, merged by AddNode) and counts a follow.
  - Evidence: onSessionEnd persists state (step 5) before it deletes the session (step 7). rehydrate restores Segment. followSegmentRoll treats any closed held segment as a roll.
  - Fix: Optional: clear st.Segment (or mark it closed by self) after the session-end close, before persistState, so a resumed state never holds a closed id. Or accept it and mention it in the counter's doc comment.

## Fix seat (review resolution) — status `done`, head `b0c01fee17d787fa9cf854f69af1827603fcc5fd`

### Root cause

Finding 1: the scheduler closes a segment at the triggering tool use's turn t and opens the successor at t+1 while turn t is still running. Turn ranges and capture order therefore diverge. The same divergence runs the other way when a close at the highest tool-use turn lands behind an already-enrolled toolless prompt. The observer can only enrol by capture order, so that is now the documented and pinned rule. Finding 2: bindOnFirstHook binds on the row's ObserveTool because a fixture with no daemon treats every session as live. The w13 merge row therefore stopped reaching CloseSegmentForCompaction's unbound merge, and a mutation check shows it was blind to that merge.

### Summary

FIX SEAT, w14-observer (branch closeout/w14-observer, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w14-observer). The implementer's 5 commits (2981cfc1, 0cb99bbc, 9726729c, 746303fa, 31e750d7) are unchanged. I added 3 commits on top. No production logic changed. Both reviewer findings were minor, and I confirmed both.

## Review resolution

### Finding 1 (minor): a mid-turn roll files the rest of the turn in two segments. CONFIRMED. Decided, pinned and documented.

**Analysis.**
- Tool uses take st.Turn without advancing it (tooluse.go). The scheduler closes at rec.Turn=t and opens the successor at t+1 (scheduler_frontier.go closeSessionSegmentLocked).
- So the next tool use of turn t goes through followSegmentRoll and is enrolled in the successor. Its tokens count in the successor's node, and in the scheduler's open-segment account (openSegTokens is reset at the roll and refilled by the tap).
- The log's turn ranges put turn t in the rolled segment. checkpoint encodeSegmentLocked partitions nodes by n.Turn in [StartTurn, EndTurn] (writer.go:802-815). It does not read DAG membership at all.

**Decision (under D33): keep capture order.** A node belongs to the segment that was open when it was captured. Capture order is the one rule every roll satisfies, because the disagreement also runs the other way:
- A close at the highest tool-use turn can end the segment before a prompt the observer has already enrolled in it. This happens on a compaction (CloseSegmentForCompaction's own doc says a final toolless prompt "lands in the successor") and on a changepoint declared at a Stop (observeStop closes at r.maxTurn).
- An enrolment cannot be moved afterwards. So the reviewer's alternative (adopt the successor only once st.Turn > held.EndTurn) would fix one direction but not the other.
- It would also split the DAG node's Tokens from the scheduler's account.
- It would need a forced adopt at SessionEnd/SessionStart anyway.
- Only BackwardSlice and CrossingEdges read the membership. Neither is keyed on turn ranges.

**Changes (9e3a61d8):**
- Two pin rows in internal/observer/segment_roll_test.go:
  - TestAMidTurnRollEnrolsTheRestOfTheTurnInTheSuccessor: a roll at a tool use in turn 0, then a second read in the same turn 0. The second read is in the successor. Rolled node Tokens=40, Ref "0-0". Successor Pos=40, Tokens=40, Ref "1-1".
  - TestARollBehindAnEnrolledPromptLeavesItInTheRolledSegment: the reverse direction. The prompt at turn 1 stays in the rolled segment "0-0", and the successor's Ref is "1-3".
- The followSegmentRoll doc comment (internal/observer/session.go) now states the capture-order rule and the turn-range disagreement in both directions.
- The segment-lifecycle note in plans/00-ARCHITECTURE.md now says the same in one sentence.

Both rows pin shipped behaviour, so they were green on arrival. They are not a RED/GREEN pair, and no production code changed.

### Finding 2 (minor): the w13 merge row no longer reaches CloseSegmentForCompaction's unbound merge. CONFIRMED and FIXED (c734e57f).

**RED.** I added the precondition assertion alone (the runtime is still unbound when PreCompact arrives). TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince failed with "Should be empty, but was sess-c1": its ObserveTool had already bound the session through bindOnFirstHook (runs/11).

**Fix.** The row now attaches `&registryDaemon{reg: NewSessionRegistry()}` (a registry that holds nothing). The observation is then a replayed delivery's and the runtime stays unbound until PreCompact. The row now asserts:
- the precondition: unbound, openSegTokens=300, one sched.tap.bind.not_live count;
- that the compaction did the bind: session bound, sched.tap.bind.first_hook still zero;
- the original checks, unchanged: EndTurn=tapToolUseTurn+3, Tokens=1000.

**Mutation check.** This was a temporary edit, reverted and never committed: bindUnboundLocked's `r.openSegTokens += seenTokens` replaced by `_ = seenTokens`.
- The committed row now fails: expected 1000, actual 700.
- The row as the implementer left it stayed green under the same mutation (runs/13). That proves it was blind to the merge.

## Tests run (Windows, -p 2, loaded machine)
Every result below passed the first time; no wall-clock failures.

| Check | Result |
|---|---|
| Finding-2 row RED (before the fix) | FAIL as expected (runs/11) |
| Finding-2 row GREEN (after the fix) | PASS (runs/12) |
| Mutation check, new and old rows | new row FAIL, old row PASS (runs/13) |
| Two finding-1 pin rows | PASS (runs/14) |
| fmt-check; go vet on Windows and GOOS=linux (observer, daemon) | exit 0 |
| lint subset (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns) | all PASS |
| test/docs | ok |
| internal/observer/... in full | ok, 79.3 s |
| internal/daemon in full | ok, 253.7 s |

Not run: internal/cli, because I did not touch it. The Linux container stayed stopped as instructed. No hot-path rows, no -race, no load generators. All my background tasks ended and nothing of mine is still running.

## Criterion changes (with rationale)
1. **TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince gains a fixture and assertions; nothing is loosened.** The fixture gains an empty-registry daemon. The assertions added are the unbound precondition, the not_live count, and "the bind was the compaction's". The existing assertions are unchanged. This restores the row to the path its name describes, the compaction's unbound merge. After bindOnFirstHook the row had silently drifted onto the first-hook path.
2. **New pin rows fix capture order as the observer's membership rule across rolls.** The turn-range disagreement is now documented as known behaviour, not treated as a defect.

## Open issues
- **Pre-existing, found in passing; code reading only, not reproduced.**
  - A mid-turn roll followed by an idle frontier pass before that turn's next tool use can leave that later tool use encoded by no checkpoint. The idle pass needs DetectAfterSeconds of quiet, 120 s by default: for example a TodoWrite completes a todo and the next tool is a long test run.
  - Why: advanceFrontier is an idle task and encodes the rolled segment [..t] at that point. The successor's range starts at t+1, and encodeSegmentLocked partitions by turn. Segments.Frontier is the last encoded EndTurn (t), so turn t reads as covered.
  - This predates w14. The encoder never read DAG membership, so the observer's choice does not change it.
  - Candidate fixes, for the coordinator to route:
    - defer a boundary close on a tool use to the turn's Stop; or
    - have the encoder pick up nodes at turns ≤ frontier that were captured after their segment was encoded.
- The implementer's own open items stand as they reported them (the PreCompact race turn/position note). I made no further changes there.

## Owner decisions
None new. I introduced no budget or bound numbers. The capture-order choice is a coordinator-level decision I made under D33 and recorded above.

## Files
C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w14-observer/
- internal/daemon/scheduler_tap_test.go
- internal/observer/segment_roll_test.go
- internal/observer/session.go
- plans/00-ARCHITECTURE.md
- plans/sdd/V6-closeout/w14-observer/runs/11..17-*.txt

### Commits

- c734e57f test(daemon): keep the compaction merge row on its unbound path
- 9e3a61d8 test(observer): pin capture-order membership across a roll
- b0c01fee test(v6): record the w14-observer fix seat's runs

### Tests

- `go test -p 2 -count=1 -run '^TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince$' ./internal/daemon/ (precondition assertion only, before the empty registry)` — FAIL as intended (RED): 'Should be empty, but was sess-c1', the row's ObserveTool had already bound the session (runs/11)
- `go test -p 2 -count=1 -v -run '^TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince$' ./internal/daemon/ (after c734e57f)` — PASS (runs/12)
- `TEMPORARY MUTATION (reverted): bindUnboundLocked merge dropped; go test -p 2 -count=1 -run '^TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince$' ./internal/daemon/` — new row FAIL (expected 1000, actual 700); the implementer's version of the row PASS under the same mutation, so it was blind to the merge (runs/13)
- `go test -p 2 -count=1 -v -run '^TestAMidTurnRollEnrolsTheRestOfTheTurnInTheSuccessor$' ./internal/observer/` — PASS (pins the shipped behaviour; green on arrival by design) (runs/14)
- `go test -p 2 -count=1 -v -run '^TestARollBehindAnEnrolledPromptLeavesItInTheRolledSegment$' ./internal/observer/` — PASS (pin row) (runs/14)
- `go run ./tools/devtool fmt-check; go vet ./internal/observer/... ./internal/daemon/; GOOS=linux go vet ./internal/observer/... ./internal/daemon/` — all exit 0 (runs/15)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS, exit 0 (runs/15)
- `go test -p 2 -count=1 ./test/docs` — ok (runs/15)
- `go test -p 2 -count=1 -timeout=30m ./internal/observer/...` — ok internal/observer 79.3s, ok observertest 2.5s (runs/16)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 253.7s (runs/17)

### Criterion changes

- TestWrapServices_PreCompactAfterARestartMergesWhatItObservedSince: added an empty-registry daemon to the fixture, and assertions for the unbound precondition, the not_live count and a compaction (not first-hook) bind. The existing EndTurn and Tokens assertions are unchanged. This restores the row to the compaction path's unbound merge that its name describes; after bindOnFirstHook it had drifted onto the first-hook path.
- New pin rows TestAMidTurnRollEnrolsTheRestOfTheTurnInTheSuccessor and TestARollBehindAnEnrolledPromptLeavesItInTheRolledSegment make capture order the observer's DAG membership rule across rolls. The disagreement with the log's turn ranges in both directions is now documented as known behaviour (followSegmentRoll doc comment, 00-ARCHITECTURE.md segment lifecycle), not treated as a defect.

### Open issues

- Pre-existing, found in passing; code reading only, not reproduced. A mid-turn scheduler roll followed by an idle advance_frontier pass (needs DetectAfterSeconds of quiet, 120 s by default) before that turn's next tool use can leave that later tool use encoded by no checkpoint. encodeSegmentLocked partitions nodes by turn, the successor's range starts at t+1, and Segments.Frontier (the last encoded EndTurn = t) marks turn t as covered. Candidate fixes for the coordinator: defer a boundary close on a tool use to the turn's Stop, or have the encoder pick up nodes at turns <= frontier that were captured after their segment was encoded.
- The implementer's open items carry forward unchanged (the PreCompact race turn/position note).

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



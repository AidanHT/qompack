# Wave 22 clock seat: wall-clock verdicts outside internal/daemon

Branch `closeout/w22-clock`. Workflow `wf_f94c150d-5f0`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:clock: status `done`, head `e98a6591`

### Summary

Wave 22 clock seat, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-clock, branch closeout/w22-clock, base 2bf29705, head e98a6591. Seven commits, unpushed. All are test-only except one seam in the checkpoint writer, which does not change product behaviour.

(1) The PreCompact row. FileWriter gains `wallNow func() time.Time` (internal/checkpoint/writer.go). It is nil, meaning time.Now, in production. PreCompact (internal/checkpoint/precompact.go) now installs its budget with `context.WithDeadline(ctx, wallNow().Add(budget))`, which is exactly what WithTimeout did. export_test.go adds SetWallNowForTest and MinFinalizeWindowForTest. The test records the installation instant and asserts the installed budget (deadline minus that instant) equals the floor or the cap exactly. I wrote the stall case first and showed it red under the old assertion ("327.5024ms is not greater than 875ms"); it is green under the new one.

(2) The sweep, across the 12 listed packages, excluding internal/daemon.

Fixed deterministically, each with the failing row shown red first and a mutant check:
- negknow TestOpenRefreshBounded (elapsed < 500ms measured after Open).
- ipc TestProbe_DoesNotWaitForAResponse (< 1s).
- cli TestCmdMCPRetryIsCancellable (300ms timer).
- ipc TestServerCloseWithLiveConnection (1s margin, the known Windows flake).
- One more found by the Linux gate, outside the wall-clock class but scheduling-dependent: cli TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving failed 5 of 20 and 6 of 20 at GOMAXPROCS=2. A diagnostic run showed every failure carried the sync-submode detail. Cause: bootstrapDaemon returns once the daemon is dialable, but the daemon writes state.bin from its own state after its accept loop starts (daemon.go:752), and that write overwrote the test's spool-submode record. The fix is one status round trip, which serveOp holds until startup is done. Linux passes 20 of 20 after it.

Compliant, left alone: store gc_test's two GC deadline rows (gcReportWallClock under obs.UnderCoload); negknow TestBudget_* through requireBudget; mcp TestBudgetBF; e2e TestE2E_SessionStartCompactUnderBudget p99 and X11 (UnderCoload); compact-load and X09/X10/X11 timings, which are logged only; lower-bound assertions such as shutdown_spawn_inflight_test.go:331 and TestSessionStartBudget_ADaemonThatNeverComesUpIsSpooledAtTheBorrowLimit's GreaterOrEqual; negative waits that can only pass falsely, never fail falsely (store observation_guards_test.go:92 50ms, recoveryidentity_test.go:243); and the hang guards of 10s to 15min (gc_serial 30s, obsProcessBound, e2eDaemonDownBound, mcpE2E*Bound, installHostBound, ipctest suiteWait, handlerSeenWait, fifo watchdog, liveWait, bootstrapUpBound).

negknow TestBudget_Open: confirmed it goes through requireBudget (`gateWall := !obs.UnderCoload()`), so it follows ADR 0010. Nothing changed.

Timing budget row that lacks the ADR 0010 helper: ipc TestDegradedSpoolAppendIsRateGradedAgainstThePlatform (B-G). It grades against a same-run platform calibration ratio (6x factor, documented co-load worst case 2.59x) rather than the helper. Reported, not changed.

Checks, all at the final HEAD unless noted:
- GOOS=windows, linux and darwin go vet on the four touched package sets: clean.
- devtool lint --only=golangci-lint: PASS. It caught an unchecked type assertion, which was fixed.
- fmt-check: PASS. docmarkers and runpatterns: PASS. sleepcheck: PASS. The fakes use `<-time.After`, not time.Sleep.
- Windows full: checkpoint, negknow and ipc/... ok on 47252607; ipc/... re-run ok after 97d7334e; cli ok on e98a6591.
- Linux non-root gate, GOMAXPROCS=2, race: on 97d7334 checkpoint 540 pass, negknow 426, ipc 160, ipctest 40; cli 551 pass with only the 2 doctor failures, since fixed. cli on e98a659: 553 pass, 0 fail.
- New rows -count=20: Windows 20 of 20 each, apart from the withdrawn probe peer-count check. Linux no-race 20 of 20 each, plus the doctor row 20 of 20 after its fix.
- New rows -race -count=3: Windows 3 of 3 each, doctor included. Linux 3 of 3 for the five rows.

Linux artifacts are under C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-clock/. Windows logs are under $TEMP/w22clock.

### Commits

- b269fd5b test(checkpoint): judge PreCompact's clamp by its installed budget
- 9ca3bb86 test(negknow): judge Open's refresh window, not its wall time
- cfa66cc4 test(ipc): judge Probe's no-wait rule without a wall-clock margin
- e9e7c3d1 test(cli): count the cancelled MCP retry's Sends, not its time
- 47252607 test(ipc): judge Close by its tracked conns, not a 1s margin
- 97d7334e test(ipc): drop the Probe row's peer byte count
- e98a6591 test(cli): wait out the daemon's startup state write in doctor

### Tests

- `go test -p 2 -count=1 -run '^TestPreCompactDerivesItsBudgetFromTheCallersDeadline$' ./internal/checkpoint/ (old assertion + stall case, before fix)`: FAIL as intended: deadline_well_in_the_future_under_a_stall '327.5024ms is not greater than 875ms'
- `go test -p 2 -count=1 -run '^TestPreCompactDerivesItsBudgetFromTheCallersDeadline$' -v ./internal/checkpoint/ (new row)`: PASS, 5/5 subtests incl. under_a_stall; zero-deadline-guard mutant FAILS zero_deadline
- `go test -p 2 -count=1 -run '^TestOpenRefreshBounded$' ./internal/negknow/ (old vs new)`: old FAIL stall_400ms '664.5281ms is not less than 500ms'; new PASS both subtests; doubled-window mutant FAILS both
- `go test -p 2 -count=1 -run '^TestProbe_DoesNotWaitForAResponse$' ./internal/ipc/ (old vs new, mutants)`: old FAIL under 1.1s simulated stall (1.1108603s); new PASS under stall; wait-for-response mutant FAILS at hang guard
- `go test -p 2 -count=1 -run '^TestCmdMCPRetryIsCancellable$' ./internal/cli/ (old vs new, mutant)`: old FAIL with 400ms stalled client (300ms timer); new PASS; ignore-cancellation mutant FAILS (10 Sends)
- `go test -p 2 -count=1 -run '^TestServerCloseWithLiveConnection$' ./internal/ipc/ (old vs new, mutant)`: old FAIL with 1.2s slow listener Close; new PASS; skip-closing-conns mutant FAILS (1 still tracked)
- `GOOS={windows,linux,darwin} go vet ./internal/checkpoint/ ./internal/negknow/ ./internal/ipc/... ./internal/cli/`: exit 0, no output, all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool lint --only=sleepcheck`: PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint ./internal/negknow ./internal/ipc/... ./internal/cli (Windows, one at a time)`: all ok (checkpoint 255.7s, negknow 56.0s, ipc 43.2s/ipctest 4.0s, cli 165.3s); ipc/... re-run ok after 97d7334e; cli ok 138.0s on e98a6591
- `go test -p 2 -count=20 -v -run '^(TestPreCompactDerivesItsBudgetFromTheCallersDeadline|TestOpenRefreshBounded|TestProbe_DoesNotWaitForAResponse|TestCmdMCPRetryIsCancellable|TestServerCloseWithLiveConnection)$' ./internal/checkpoint/ ./internal/negknow/ ./internal/ipc/ ./internal/cli/ (Windows)`: 20/20 each except probe 19/20 with the interim peer-count check (withdrawn in 97d7334e); revised probe row -count=20: 20/20
- `go test -race -p 2 -count=3 -v -run '<same five rows>' (Windows) and -run '^TestDoctor_SpoolSubmodeIsInformational$' ./internal/cli`: 3/3 each, no DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `linux-nonroot-gate.sh HEAD(97d7334) w22clock-touched --gomaxprocs 2 --timeout 30m -- ./internal/checkpoint ./internal/negknow ./internal/ipc/... ./internal/cli (race)`: checkpoint 540/0, negknow 426/0, ipc 160/0, ipctest 40/0, cli 551 pass 2 fail (TestDoctor_SpoolSubmodeIsInformational, pre-existing race, fixed in e98a6591)
- `linux-nonroot-gate.sh HEAD(97d7334) w22clock-rows-x20 --gomaxprocs 2 --no-race --count 20 --run '<five rows + TestDoctor_SpoolSubmodeIsInformational>'`: five new rows 20/20 each; doctor row 15/20 (5 fail) before its fix
- `linux-nonroot-gate.sh HEAD(97d7334) w22clock-rows-race3 --gomaxprocs 2 --count 3 --run '<five rows>' (race)`: exit 0, all pass
- `linux-nonroot-gate.sh HEAD(e98a659) w22clock-doctor-x20 --gomaxprocs 2 --no-race --count 20 --run '^TestDoctor_SpoolSubmodeIsInformational$' -- ./internal/cli`: pass=160 fail=0 (20/20)
- `linux-nonroot-gate.sh HEAD(e98a659) w22clock-cli-final --gomaxprocs 2 --timeout 30m -- ./internal/cli (race)`: pass=553 fail=0 skip=0

### Criterion changes

- internal/checkpoint TestPreCompactDerivesItsBudgetFromTheCallersDeadline: was 'time.Until(deadline) after PreCompact returns, compared with an 875ms midpoint'; now 'installed budget == 250ms floor' (zero, past and equal-now deadlines) or '== 1.5s cap' (future deadline). Rationale: the old row measured after the work, so 1.67s spent inside PreCompact under load left 802ms. The new criterion gives the same verdict for every case, is strictly tighter, and does not depend on scheduling. New case deadline_well_in_the_future_under_a_stall (pins fake waits 700ms in Materialize): the old row failed it at 327ms remaining; the new row passes. Mutant (zero-deadline guard removed) fails zero_deadline.
- internal/negknow TestOpenRefreshBounded: was 'Open wall time < 500ms and >= 125ms'; now 'the store call ends with context.DeadlineExceeded (not the fake's own timer, now a one-minute hang guard), deadline minus call entry <= openRefreshDeadline, and deadline minus test start >= openRefreshDeadline'. Both bounds hold whatever the scheduler does. New stall_400ms subtest (fake returns 400ms after cancel): the old row failed it at 664ms. Mutant (window doubled) fails both subtests.
- internal/ipc TestProbe_DoesNotWaitForAResponse: was 'Probe(addr, 2s) returns in < 1s'; now 'Probe(addr, 1h) returns true inside a one-minute hang guard' against the real Server with a hanging handler. A simulated 1.1s stall inside Probe failed the old row (1.11s) and passes the new one; a Probe that waits for a response fails at the guard. An interim never-writes byte-count check (cfa66cc4) was withdrawn in 97d7334e: it was never part of the original row, and on Windows go-winio drops a pipe client that disconnects before ConnectNamedPipe completes (it failed 1 in 20).
- internal/cli TestCmdMCPRetryIsCancellable: was 'returns within 300ms (a fifth of the retry budget)'; now 'exactly one Send and the unavailable answer', with a one-minute hang guard. The client keeps a 400ms per-Send stall, which failed the old row. A mutant that waits out mcpRetryDelay without watching ctx makes 10 Sends and fails.
- internal/ipc TestServerCloseWithLiveConnection: was 'Close and Serve each return within serverCloseWait/2 (1s)', a known Windows flake carried as known-deferred (§2.5a E); now 'after Serve has registered the silent conn, Close returns with zero tracked conns' (the handler has finished and unregistered), with one-minute hang guards. A 1.2s slow listener Close (go-winio's documented behaviour) failed the old row and is kept in the fixture. A mutant that skips closing tracked conns fails (1 still tracked).
- internal/cli TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving: no assertion changed. A status round trip before enterSpoolSubmode makes the fixture order deterministic.

### Open issues

- internal/cli sessionstart_budget_test.go, D17b/D21 live rows: TestSessionStartBudget_APreSendOverrunCutsTheReplyWait, TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling, TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered, TestSessionStartBudget_ADaemonThatNeverComesUpIsSpooledAtTheBorrowLimit and TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound. All assert took < bound + 1s, measured after the hook returns, with no ADR 0010 declaration. ACompactStartUpAt8s also depends on two 250ms windows: the poll's borrow limit (8.25s against a daemon that comes up at 8s) and the reply budget (13.5 - 8 - 0.25 = 5.25s against a daemon that holds 5s). That makes it the highest-risk row left. Listed, not fixed: the properties are intrinsically wall-clock, and a deterministic version needs a clock seam through hookBudget, EnsureRunningUntil's poll and the ipc deadlines, which is not cheap. The ADR 0010 route (report under co-load) is closed for the answered/not-spooled outcomes because they are product outcomes.
- internal/cli TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool: the fake daemon ACKs at 200ms against flushAckDeadline = 1.5s/2 - 250ms = 500ms. A load stall over about 300ms spools the flush and turns the row red. Listed: deterministic needs a window-free design; widening the margin is barred.
- internal/ipc TestSendNeverReturnsError and TestSendTimeBudgetIsItsOwnDeadlinesNotTheSpool: the deadline-governed part of Send is held to sendTransportBound (4x the deadline budget, about 640ms+), with no co-load declaration. Intrinsically wall-clock (a deadline honoured in wall time). The ADR 0010 route needs a ci.yml timing-lane entry, which this seat did not edit because the lane is shared with the rows seat.
- internal/ipc TestServerConcurrentDialVsClose: stressTestBound = 2*serverCloseWait + 1s, against Close's 4s construction cap. That is a 1s margin on a secondary signal; the primary signal is the WaitGroup panic. internal/ipc server_inflight_test.go inflightTestBound (5s) is the same shape. Listed: a longer bound is barred.
- internal/cli daemon_test.go:32, daemon_test.go:108 and daemon_disabled_test.go:144: require.Eventually 5s with a 20ms tick for an in-process daemon to become reachable (bootstrapUpBound elsewhere is 15s). That is a startup-latency margin under heavy co-load. Listed: a longer bound is barred.
- test/e2e v1_integration_test.go:571: each manifest hook's spawned process must finish under its declared manifest timeout (B-D, seconds). The margin is large and the property intrinsic. Listed.
- internal/cli TestCmdMCPRetryIsCancellable residual: '1 Send' depends on the product's own select between ctx.Done and a fresh 150ms timer. That timer can only be ready if the goroutine is preempted for 150ms inside the select statement itself.
- internal/ipc TestServerCloseWithLiveConnection residual: the verdict relies on the product's own waitForConns timer (2s), meaning the handler goroutine runs within 2s of its conn being closed. It no longer depends on go-winio's listener Close time.
- ipc TestDegradedSpoolAppendIsRateGradedAgainstThePlatform (B-G) is a budget row graded by same-run calibration ratio, not by the ADR 0010 helper. Reported per brief.

### Needs owner

- Decide whether the cli SessionStart budget rows (especially TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound, with two 250ms windows), TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool and ipc TestSendNeverReturnsError/TestSendTimeBudgetIsItsOwnDeadlinesNotTheSpool get a clock seam (product change in cli/ipc) or ADR 0010 yielder status. The second needs edits to the ci.yml timing lane and test/guards, which may conflict with the rows seat's edits to the same lane. This seat could make neither change without weakening a check or editing a shared lane.
- Coordinator: the doctor spool-submode race (fixed in e98a6591) is a pattern, a test that writes state.bin right after bootstrapDaemon. The cliwork seat may want to know; precompact_spool_submode_test is already ordered after a session-start round trip and is safe.

## review:clock:r1: verdict `needs-fixes`, 5 finding(s)

- **major** `internal/ipc/client_test.go:35 (testDeadline = 50ms), used by TestSendACKPath:119, TestSendHotSpoolSkipsConnect:213 (res2.OK), TestSendReplyPath:261, TestSendOversizeExternalizes:280, TestSendOversizeExternalizePreservesExistingRaw:340, and probably TestSendNAKSwitchesToSpool:136 (calls==1)`: The sweep missed internal/ipc's main wall-clock class. These rows build the client with ConnectDeadline and AckDeadline set to 50 ms, then assert res.OK (an ACK or reply arrived) or that the handler ran exactly once. So a 50 ms real-time window decides the verdict, with no ADR 0010 declaration. Under the co-load that produced the 1.67 s PreCompact stall, the server's handleConn goroutine can easily be descheduled for more than 50 ms. The client then spools, res.OK is false, and the row goes red. The 50 ms margin is far tighter than the 875 ms margin in the row that started this task. The seat's report covers ipc rows from the same file (TestSendNeverReturnsError, TestSendTimeBudget...) but says nothing about these, and it presents the sweep as complete.
  - Evidence: In a go -overlay copy (worktree untouched), I added `<-time.After(80 * time.Millisecond)` to TestSendACKPath's handler to simulate descheduling: `go test -p 2 -count=1 -run '^TestSendACKPath$' ./internal/ipc/` gives FAIL at client_test.go:128 'Should be true ... res.Err=""'. The unmodified row passes, so only scheduling separates the two outcomes. internal/ipc/ipctest uses 5 s deadlines (suiteDeadline, transportTestPatience) for exactly this reason.
  - Fix: For rows that assert success, use patient deadlines, as ipctest and replyProject do (for example ConnectDeadline/AckDeadline = handlerSeenWait). Keep testDeadline only for rows that expect failure or a spool, where a longer wait cannot change the verdict. That only removes a scheduling dependency; no assertion is loosened. Alternatively, list each row with its reason.
- **minor** `internal/ipc/server_test.go:35-36 (rawDialBound = 1s, rawIOBound = 2s), used by newRawClient/writeRequest/readByte in TestServerRoutesAndACKs, TestServerUnknownOpNAKs and the other raw-wire rows (lines 79, 104, 134, 167, 182), server_inflight_test.go, server_unix_test.go:66, and the rewritten TestServerCloseWithLiveConnection (newRawClient at :287)`: These are 1 s and 2 s real-time dial, write and read deadlines whose expiry fails the row (require.NoError on dial, Write and Read). They have no co-load declaration, and the seat neither fixed nor listed them. The doc comment's 'orders of magnitude of headroom' assumes microsecond scheduling, but the observed stalls are 1.67 s.
  - Evidence: server_test.go:44 `conn, err := dial(addr, rawDialBound); require.NoError(t, err)`; :60 `conn.SetReadDeadline(time.Now().Add(rawIOBound))` followed by require.NoError on Read. The seat's open_issues list has no entry for rawDialBound or rawIOBound.
  - Fix: Raise them to hang-guard scale (the server's own patience is connIdleTimeout, 10 min, so a longer client deadline cannot hide a wedged read; it only turns a failure into a slower one). Otherwise list them in open_issues with the reason.
- **minor** `internal/cli/flush_hook_test.go:25 TestFlushHook_IsFireAndForgetWithItsNonce (requireNoClientSpool at :49)`: This row has the same shape as TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool, which the seat listed, but it is not in the list. The flush hook ignores replyProject's 5 s AckDeadlineMs and always waits flushAckDeadline (1.5s/2 - 250ms = 500 ms; hooks.go:41 sets ackDeadline: flushAckDeadline). A stall of more than 500 ms spools the flush, and the 'nothing in the client spool' assertion fails.
  - Evidence: hookclient.go:56 `flushAckDeadline = sessionEndHostBudget/2 - hookConnectDeadlineFloor`; hooks.go:41 `doHook(hookSpec{op: ipc.OpFlush, reply: false, ackDeadline: flushAckDeadline})`; flush_hook_test.go:49 `requireNoClientSpool(t, root, "an ACK in time leaves nothing in the client spool")`.
  - Fix: Add it to open_issues and needs_owner next to the ASlowAck row. The same product clock seam or ADR 0010 decision covers both.
- **minor** `internal/cli/fsck_test.go:676, internal/cli/hookoutput_contract_test.go:193 and :203-205 (replyProject's 5 s Connect/Ack deadlines), internal/cli/cmd_mcp_handshake_test.go:119, internal/negknow/detector_test.go:390`: The seat listed the 5 s require.Eventually waits in daemon_test.go:32/108 and daemon_disabled_test.go:144 as wall-clock margins, but missed siblings of the same size. fsck_test and hookoutput_contract_test have a 5 s Eventually on ipc.Probe. replyProject's 5000 ms state deadlines decide whether the hookoutput rows get the daemon's answer or a spooled '{}'. The handshake row's Eventually is about 6.65 s (11 x 150 ms + mcpCallDeadline). The negknow detector row fails with 'Scan did not return on a cyclic graph' when a 5 s context expires. All of these are below the seat's own 10 s hang-guard threshold, so the sweep is not complete by its own classification.
  - Evidence: fsck_test.go:676 and hookoutput_contract_test.go:193: `require.Eventually(..., 5*time.Second, 10*time.Millisecond, ...)`; detector_test.go:390 `context.WithTimeout(context.Background(), 5*time.Second)` then `case <-ctx.Done(): require.FailNow(t, "Scan did not return on a cyclic graph")`; cmd_mcp_handshake_test.go:119-120.
  - Fix: List them in open_issues next to the daemon_test.go entries. A fix would need either hang-guard-scale bounds, which the brief treats as a longer margin, or an owner decision.
- **nit** `commits b269fd5b and cfa66cc4`: Two commit subjects exceed the brief's 64-character hard limit.
  - Evidence: b269fd5b 'test(checkpoint): judge PreCompact's clamp by its installed budget' is 66 characters; cfa66cc4 'test(ipc): judge Probe's no-wait rule without a wall-clock margin' is 65 characters (counted in characters with Python).
  - Fix: Reword both subjects when the branch is next rewritten (for example 'test(checkpoint): judge PreCompact's clamp by installed budget' and 'test(ipc): judge Probe's no-wait rule without a clock margin'). That is the seat's or coordinator's call; this verifier is read-only.

## fix:clock:r1: status `done`, head `0dc15508`

### Summary

Wave 22 clock seat, fix round. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-clock, branch closeout/w22-clock, base 2bf29705, head 0dc15508. There are nine commits, unpushed and test-only, except the checkpoint writer's wallNow seam from round 1, which does not change product behaviour. The tree is clean.

How the four findings were handled:

1. MAJOR, fixed in 939ef478: the internal/ipc 50ms class in client_test.go. First I showed the six success rows red in a go -overlay copy (the worktree was untouched) with a 1.1s simulated stall before each handler's return.
   - TestSendACKPath, TestSendHotSpoolSkipsConnect, TestSendReplyPath and both oversize rows failed on res.OK.
   - TestSendNAKSwitchesToSpool failed on res.Hot: an ACK timeout spools without setting HotSpool, so the verifier's guess (calls==1) was right that the row depends on scheduling, but the line that fails is the Hot check.
   - New const answerDeadline = handlerSeenWait (10s), used for ConnectDeadline, AckDeadline and the Send reply deadline in those six rows. The comments on testDeadline and newTestClient now say that testDeadline is only for rows that expect failure or a spool.
   - Under the same stall all six now pass. An 11s stall, longer than the guard, still fails ACK and Reply, so a server that never answers is still caught.
   - I checked every other testDeadline user: the lazy-spawn rows (nothing listening), DaemonDown, FailedSpoolAppend, ModeOff, the degraded rows (DaemonEnabled false) and TestServerConcurrentDialVsClose, where a dial failure is tolerated. All of them expect failure or never dial, so they keep 50ms.

2. MINOR, fixed in 0dc15508: rawDialBound and rawIOBound in server_test.go. Under a 2.1s simulated stall the old bounds failed TestServerRoutesAndACKs, TestServerUnknownOpNAKs and TestServerDecodeErrorNAKsAndCounts. rawDialBound is now handlerSeenWait, and rawIOBound stays twice that, both far under the server's 10-minute connIdleTimeout. Under the same stall the rows pass, and a 21s stall still fails. The comment's old 'orders of magnitude of headroom' reasoning is replaced with the co-load rationale.

3. MINOR, listed: TestFlushHook_IsFireAndForgetWithItsNonce. It is now in open_issues and needs_owner next to the ASlowAck row.

4. MINOR, listed: the 5s fixture waits the verifier named. While re-sweeping I also found a sibling the verifier missed: TestHookOutput_EveryEntryPointConformsToTheHostSchema/UserPromptSubmit has a 250ms window set by the product constant promptReplyDeadline, and it is listed too. I did not fix these, for two reasons: the brief treats a hang-guard-scale bound on these as a longer margin, and promptReplyDeadline and flushAckDeadline are product constants that need a clock seam.

Everything from round 1 stands: the PreCompact row, the negknow/ipc/cli fixes, the compliant rows, and TestBudget_Open confirmed to go through requireBudget.

Checks at the final HEAD:
- GOOS=windows, linux and darwin go vet ./internal/ipc/...: clean.
- devtool fmt-check: exit 0.
- lint docmarkers and runpatterns: PASS. golangci-lint: PASS.
- Windows go test ./internal/ipc/...: ok (ipc 28.3s, ipctest 3.1s).
- The 13 touched or affected rows (6 client rows plus 7 raw-wire and server rows) on Windows: -count=20 gave 20/20 each; -race -count=3 gave 3/3 each, with no DATA RACE.
- Linux non-root gate at GOMAXPROCS=2 on 0dc1550:
  - ipc/... with race: ipc pass=160 fail=0, ipctest pass=40 fail=0.
  - The 13 rows without race, -count 20: pass=260 fail=0.
  - The 13 rows with race, -count 3: pass=39 fail=0.

The cli, checkpoint and negknow packages are unchanged since round 1, which validated them on e98a6591.

Artifacts:
- Linux: C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-clock/ (the cx-linux-w22clock-fix-* directories).
- Windows logs: C:/Users/Quant/AppData/Local/Temp/w22clock/ (ipc-client-stall-old/new.txt, ipc-client-mutant.txt, ipc-server-stall-old/new.txt, ipc-server-mutant.txt, win-ipc-fix*.txt, linux-fix-*.txt).
- The overlay stall generator is scratchpad/stall.py.

### Commits

- b269fd5b test(checkpoint): judge PreCompact's clamp by its installed budget
- 9ca3bb86 test(negknow): judge Open's refresh window, not its wall time
- cfa66cc4 test(ipc): judge Probe's no-wait rule without a wall-clock margin
- e9e7c3d1 test(cli): count the cancelled MCP retry's Sends, not its time
- 47252607 test(ipc): judge Close by its tracked conns, not a 1s margin
- 97d7334e test(ipc): drop the Probe row's peer byte count
- e98a6591 test(cli): wait out the daemon's startup state write in doctor
- 939ef478 test(ipc): give the Send answer rows a hang guard, not 50ms
- 0dc15508 test(ipc): raise the raw-wire client bounds to hang-guard scale

### Tests

- `go test -p 2 -count=1 -overlay <1.1s handler stall> -run '^(TestSendACKPath|TestSendNAKSwitchesToSpool|TestSendHotSpoolSkipsConnect|TestSendReplyPath|TestSendOversizeExternalizes|TestSendOversizeExternalizePreservesExistingRaw)$' ./internal/ipc/ (old deadlines, before 939ef478)`: FAIL as intended: all 6 rows fail (res.OK false; NAK row Hot expected 0x1 actual 0x0)
- `same overlay stall, new deadlines (939ef478)`: PASS 6/6
- `go test -p 2 -count=1 -overlay <11s stall in ACK and Reply handlers> -run '^(TestSendACKPath|TestSendReplyPath)$' ./internal/ipc/`: FAIL both at 11.01s (a server that never answers still fails at the hang guard)
- `go test -p 2 -count=1 -overlay <2.1s stall at every handler return> -run '^(TestServerRoutesAndACKs|TestServerUnknownOpNAKs|TestServerDecodeErrorNAKsAndCounts)$' ./internal/ipc/ (old raw bounds)`: FAIL as intended: all 3 fail with a read-deadline error from readByte or ReadLine
- `same 2.1s overlay stall, new raw bounds (0dc15508)`: PASS 3/3
- `go test -p 2 -count=1 -overlay <21s stall> -run '^TestServerUnknownOpNAKs$' ./internal/ipc/`: FAIL at 21.01s (a wedged server still fails as a named error)
- `GOOS={windows,linux,darwin} go vet ./internal/ipc/...`: ok, all three
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/ipc/... (Windows)`: ok ipc 28.3s, ok ipctest 3.1s
- `go test -p 2 -count=20 -v -run '^(TestSendACKPath|TestSendNAKSwitchesToSpool|TestSendHotSpoolSkipsConnect|TestSendReplyPath|TestSendOversizeExternalizes|TestSendOversizeExternalizePreservesExistingRaw|TestServerRoutesAndACKs|TestServerUnknownOpNAKs|TestServerDecodeErrorNAKsAndCounts|TestServerHandlerPanicIsContained|TestServerMultiplexesLines|TestServerConcurrentClients|TestServerCloseWithLiveConnection)$' ./internal/ipc/ (Windows)`: 20/20 each of the 13 rows
- `go test -race -p 2 -count=3 -v -run '<same 13 rows>' ./internal/ipc/ (Windows)`: 3/3 each, 0 DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `linux-nonroot-gate.sh --out .../wave22/linux-clock HEAD(0dc1550) w22clock-fix-ipc --gomaxprocs 2 --timeout 30m -- ./internal/ipc/... (race)`: ipc pass=160 fail=0; ipctest pass=40 fail=0 skip=6; go_test_exit=0
- `linux-nonroot-gate.sh HEAD(0dc1550) w22clock-fix-x20 --gomaxprocs 2 --no-race --count 20 --run '<same 13 rows>' -- ./internal/ipc`: pass=260 fail=0
- `linux-nonroot-gate.sh HEAD(0dc1550) w22clock-fix-race3 --gomaxprocs 2 --count 3 --run '<same 13 rows>' -- ./internal/ipc (race)`: pass=39 fail=0
- `Round 1 checks on e98a6591 (checkpoint, negknow, cli and ipc full on Windows and in the Linux gate; the five round-1 rows -count=20 and -race -count=3)`: all green as reported in round 1; those packages are unchanged since

### Criterion changes

- internal/checkpoint TestPreCompactDerivesItsBudgetFromTheCallersDeadline: was 'time.Until(deadline) after PreCompact returns, compared with an 875ms midpoint'. Now 'installed budget == 250ms floor' for zero, past and equal-now deadlines, and '== 1.5s cap' for a future deadline. Why: the old row measured after the work, so 1.67s spent inside PreCompact under load left only 802ms. Every case keeps its verdict, the check is strictly tighter, and it no longer depends on scheduling. A new case, deadline_well_in_the_future_under_a_stall, has the pins fake wait 700ms in Materialize: the old row failed it with 327ms left, the new row passes.
- internal/negknow TestOpenRefreshBounded: was 'Open wall time < 500ms and >= 125ms'. Now: the store call ends with context.DeadlineExceeded; deadline minus call entry <= openRefreshDeadline; and deadline minus test start >= openRefreshDeadline. A new stall_400ms subtest failed the old row at 664ms and passes the new one.
- internal/ipc TestProbe_DoesNotWaitForAResponse: was 'Probe(addr, 2s) returns in < 1s'. Now 'Probe(addr, 1h) returns true inside a one-minute hang guard'. The interim peer-byte-count check was withdrawn in 97d7334e.
- internal/cli TestCmdMCPRetryIsCancellable: was 'returns within 300ms'. Now 'exactly one Send and the unavailable answer', inside a one-minute hang guard.
- internal/ipc TestServerCloseWithLiveConnection: was 'Close and Serve each return within 1s'. Now 'Close returns with zero tracked conns', inside one-minute hang guards.
- internal/cli TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving: no assertion changed. A status round trip before enterSpoolSubmode makes the fixture order deterministic.
- FIX ROUND, internal/ipc client_test.go: the deadlines change, the assertions do not. TestSendACKPath, TestSendNAKSwitchesToSpool, TestSendHotSpoolSkipsConnect, TestSendReplyPath, TestSendOversizeExternalizes and TestSendOversizeExternalizePreservesExistingRaw ran with 50ms (testDeadline) ConnectDeadline/AckDeadline and a 1s Send reply deadline. They now use answerDeadline = handlerSeenWait (10s, the same value as ipctest's suiteWait). Why: each row asserts that the server's answer arrives (an ACK, a NAK the client acts on, or a reply), and none judges how fast it came. The old windows let a descheduled server decide the verdict. With a 1.1s simulated handler stall, all six old rows failed (res.OK false; for the NAK row, Hot was HotSync rather than HotSpool). All six new rows pass under the same stall. A server that never answers still fails: an 11s stall fails ACK and Reply at the guard. testDeadline stays for rows that expect failure or a spool, where a longer wait cannot change the verdict.
- FIX ROUND, internal/ipc server_test.go: the bounds change, the assertions do not. rawDialBound went from 1s to handlerSeenWait (10s), and rawIOBound stays 2x, going from 2s to 20s. Why: these are the hand-rolled wire client's dial, write and read deadlines, and their expiry failed the row through require.NoError, although no row judges the server's speed. Under a 2.1s simulated handler stall, TestServerRoutesAndACKs, TestServerUnknownOpNAKs and TestServerDecodeErrorNAKsAndCounts all failed the old bounds with a read-deadline error and pass the new ones. Both bounds stay far under the server's 10-minute connIdleTimeout, so a wedged server still fails as a named error: a 21s stall fails TestServerUnknownOpNAKs. This also covers server_inflight_test.go's and server_unix_test.go's newRawClient dials.

### Open issues

- internal/cli sessionstart_budget_test.go D17b/D21 live rows: TestSessionStartBudget_APreSendOverrunCutsTheReplyWait, TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling, TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered, TestSessionStartBudget_ADaemonThatNeverComesUpIsSpooledAtTheBorrowLimit and TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound. Each asserts took < bound + 1s, measured after the hook returns, with no ADR 0010 declaration. ACompactStartUpAt8s also depends on two 250ms windows, which makes it the highest-risk row left. A deterministic version needs a clock seam through hookBudget, EnsureRunningUntil's poll and the ipc deadlines.
- internal/cli flush rows: TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool and TestFlushHook_IsFireAndForgetWithItsNonce (requireNoClientSpool at flush_hook_test.go:49). The flush hook ignores replyProject's 5s AckDeadlineMs and always waits flushAckDeadline: 1.5s/2 - 250ms = 500ms (hookclient.go:56, hooks.go:41). A stall over 500ms (about 300ms for ASlowAck, whose fake ACKs at 200ms) spools the flush and fails 'nothing in the client spool'. Not fixable without a product clock seam or an ADR 0010 decision; widening the margin is barred.
- internal/cli TestHookOutput_EveryEntryPointConformsToTheHostSchema/UserPromptSubmit (found in this round's re-sweep, not in the verifier's list): the prompt hook waits the product constant promptReplyDeadline (250ms, hookclient.go:31) for the daemon's reply, so a stall over 250ms writes '{}' instead of the golden. Same remedy as the flush rows.
- internal/cli 5s fixture waits below the 10s hang-guard threshold: daemon_test.go:32 and :108 and daemon_disabled_test.go:144 (Eventually 5s for an in-process daemon); fsck_test.go:676 and hookoutput_contract_test.go:193 (Eventually 5s on ipc.Probe); replyProject's ConnectDeadlineMs and AckDeadlineMs of 5000 (hookoutput_contract_test.go:203-205), which decide whether the SessionStart and other reply rows get the daemon's answer or a spooled '{}'; flush_hook_test.go:43 (5s wait for the request); cmd_mcp_handshake_test.go:119-120 (Eventually about 6.65s = 11 x 150ms + mcpCallDeadline). Listed: the fix would be hang-guard-scale bounds, which the brief treats as a longer margin.
- internal/negknow detector_test.go:390: a 5s context, and the row fails with 'Scan did not return on a cyclic graph' when it expires. Same class and same reason as the item above.
- internal/ipc TestSendNeverReturnsError and TestSendTimeBudgetIsItsOwnDeadlinesNotTheSpool: the deadline-governed part of Send is held to sendTransportBound (4x the deadline budget, about 640ms+) with no co-load declaration. This is intrinsically wall-clock. The ADR 0010 route needs a ci.yml timing-lane entry, and that lane is shared with the rows seat.
- internal/ipc TestServerConcurrentDialVsClose (stressTestBound = 2*serverCloseWait + 1s, against Close's 4s construction cap) and server_inflight_test.go inflightTestBound (5s): a 1s margin over the server's own bounds. The primary signal there is the WaitGroup panic. Listed: a longer bound is barred.
- test/e2e v1_integration_test.go:571: each manifest hook's process must finish under its declared manifest timeout (B-D, seconds). The margin is large and the property intrinsic.
- Residuals from round 1: TestCmdMCPRetryIsCancellable's '1 Send' relies on the product's own select between ctx.Done and a fresh 150ms timer. TestServerCloseWithLiveConnection relies on waitForConns' 2s timer.
- ipc TestDegradedSpoolAppendIsRateGradedAgainstThePlatform (B-G): a budget row graded by a same-run calibration ratio rather than the ADR 0010 helper. Reported per brief.

### Needs owner

- Decide whether these rows get a product clock seam (cli/ipc product change) or ADR 0010 yielder status: the cli SessionStart budget rows (especially TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound), TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool, TestFlushHook_IsFireAndForgetWithItsNonce, TestHookOutput_EveryEntryPointConformsToTheHostSchema/UserPromptSubmit, and ipc TestSendNeverReturnsError/TestSendTimeBudgetIsItsOwnDeadlinesNotTheSpool. The flush and prompt windows (500ms and 250ms) are product constants. ADR 0010 status would need edits to the ci.yml timing lane and test/guards, which the rows seat also edits.
- Decide whether the 5s fixture waits in cli (daemon_test, daemon_disabled_test, fsck_test, hookoutput_contract_test Probe Eventually and replyProject's 5000ms state deadlines, flush_hook_test:43, cmd_mcp_handshake_test) and negknow detector_test.go:390 may become hang guards. This round did exactly that for ipc (handlerSeenWait, 10s) on the verifier's instruction, but the brief treats it as a longer margin for these.
- Coordinator: the doctor spool-submode race (fixed in e98a6591) is a pattern: a test that writes state.bin right after bootstrapDaemon. The cliwork seat may want to know.


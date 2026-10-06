# Wave 22 spawn seat: hosted Windows red in the spawn poll (H4)

Branch `closeout/w22-spawn`. Workflow `wf_c2d62fdb-67b`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:spawn: status `partial`, head `3457c914a10ed3e5634039d753a111892c07dad8`

### Root cause

The row assumed the call reached its first claim within the 1 s (pollEndsWait) it gave the poll, so that the poll's deadline would be `until`. That is a wall-clock margin, which D61(c) forbids. On the loaded hosted windows-latest runner the call took about 1.42 s to reach its first claim. In the held case the poll began after `until` and correctly ran its own spawnLockMissBound wait, so dials past `until` were bounded by the real deadline. In the freed case the lock was already free at the first claim, so the post-claim dial came before the poll began, was never cut short (by design), failed against the hung daemon, and licensed a spawn under D17a. The product was right both times; the row's verdict depended on scheduling.

### Summary

H4 is a fixture problem, not a product race. The product followed its documented rule in both failing cases. The row's verdict depended on how fast the hosted runner scheduled the call. I made the row and the class of rows like it deterministic, without weakening any check. The one real gap: the Linux full-package gate could not finish because the owner's Docker engine stopped partway through. I did not restart it.

WHY IT IS THE FIXTURE (the numbers come from the CI log and a local reproduction)
- The row's assumption: the call reaches its first claim within the 1 s (pollEndsWait) the row gives, so the poll's deadline equals `until`. The hosted runner took about 1.42 s.
- claim_held_to_the_end: the poll began about 384 ms after `until`. The bound then gives that poll its own 200 ms (deadline = max(begun+after, until)). Every dial was capped at that deadline: the last one was bounded 584.49 ms after `until`, and the call returned at 584.9 ms. Nothing was spawned.
- claim_freed_near_the_end: the free (at until-125 ms) came before the call's first claim, so the claim was taken BEFORE the poll began, while the deadline was still zero. CI's dial 1 (667 ms past `until`) is a 250 ms post-claim dial made about 417 ms past `until`, with its whole bound. The hung daemon failed it, so the call spawned (D17a). The poll then ran 200 ms from the spawn (dials 2-10, 20 ms apart, the last at 867.98 ms). The claim was kept for the daemon it started, which explains CI's 'spawn.lock exists'.
- Reproduction: the original row body with only the pre-poll dial stalled by 1.42 s, on base 2bf29705, run with `go test -p 2 -count=1 -run '^TestH4Repro_ThePollEndsAtItsDeadlineUnderAStall$' ./internal/daemon/`. It fails every time with CI's exact failure: held, 8 dials past `until`; freed, spawned=true, 1 spawner call, 10 dials past `until`, spawn.lock exists. The logged dial issue times show dial 1 bounded 250 ms after it was made (a pre-poll claim dial), then 20 ms dials. That scratch file was not committed; a copy is at scratchpad/spawn_h4_repro_test.go.txt and its output at scratchpad/h4-repro-base2.log. <!-- runpatterns: a scratch reproduction run on base 2bf29705 from an uncommitted test file (copy kept outside the repository); it is not committed, and its result is recorded on this line -->

WHY THE PRODUCT IS SOUND
After a dial following a claim, ensureRunningWith decides on when the dial RETURNED (now >= deadline means no spawn), not on the instant it was bounded to. go-winio v0.6.2's tryDialPipe retries ERROR_PIPE_BUSY until the context deadline, which is time.Now()+timeout and so no earlier than `by`. A cut-short dial of a pipe that exists therefore returns no earlier than the end and never licenses a spawn. A dial that returns earlier found no pipe, and spawning is then right. A held fresh claim returns SpawnInFlight and is never dialled through to a spawn. Commit f0b9d525 records this reasoning in the comment on that case.

SWEEP OF SPAWN-DECISION CALLERS
- Session start (EnsureRunningUntil) and self-test (EnsureRunning): both go through ensureRunningWith. Sound, as above.
- Hook clients (hookclient.go), the command client (qompack_commands.go) and the MCP client (cmd_mcp.go): all use ipc lazySpawn. lazySpawn spawns only on SpawnClaimed, so a held claim never licenses a spawn. A connect cut short by its budget against a live but busy listener does spawn. That is D61(c)'s documented known limit (docs/troubleshooting.md:152-155): the duplicate loses daemon.lock and exits. Not changed.

FIX
1. f0b9d525: ensureRunningWith takes a pollClock (Now and NewTicker). Production uses wallPollClock: time.Now, time.NewTicker started at the same point, and the unchanged waitForTick, so behaviour is identical.
2. 3457c914, row rewrite: TestEnsureRunningUntil_ThePollEndsAtItsDeadline runs on a step clock (new file internal/daemon/spawn_pollclock_test.go). Time moves only through the stand-in dials and tick waits, and frees and stalls are scheduled at exact instants. Every run is judged by one contract:
   - each poll dial is bounded by min(its own timeout, the end of the wait it was made in);
   - the dial after a claim is capped at the end, and is never cut short before the poll begins;
   - a cut-short dial, or one returning at or after the end, licenses no spawn;
   - a held claim is never taken;
   - the call returns exactly at the end;
   - the lock is in the expected state afterwards.
   There are 6 named runs, including two that model the hosted runner's 1.4 s stall and one whose post-claim dial is stalled past the end, plus a sweep of start stalls from 0 to 1.5 s in 125 ms steps, for both held and freed claims (32 subtests in all).
3. Same class: PollsToItsDeadlineNotTheFixedBound, ALateSpawnStillGetsTheClassicWait and APassedDeadlineStillStartsADaemon had 2 s, 1.3 s and 1.5 s of wall-clock margin. They now run on the step clock with a stand-in daemon that comes up at an exact instant.

MUTATION CHECK
Ten product mutants each turn the rows red: cut-short spawn allowed; post-claim dial uncapped; poll dial uncapped; tick wait running past the end; held claim dialled through; pre-poll dial cut at `until`; `until` ignored; no classic wait after a late spawn; no cap at `latest`; a passed deadline cutting the spawn. Logs are at scratchpad/mutants.log and scratchpad/mutants-until.log.

Report files: scratchpad = C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad (row logs, daemon-full-windows.log, w22-spawn-lint.log). The Linux host record is at C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-spawn.

### Commits

- f0b9d525 refactor(daemon): run the spawn poll on an injectable clock
- 3457c914 test(daemon): make the spawn poll's deadline rows deterministic

### Tests

- `go test -p 2 -count=1 -run '^TestH4Repro_ThePollEndsAtItsDeadlineUnderAStall$' ./internal/daemon/  (scratch repro on base 2bf29705, not committed)`: FAIL as intended: reproduces CI exactly. Held: 8 dials past until. Freed: spawned=true, 1 spawner call, 10 dials past until, spawn.lock exists. Dial 1 was a 250 ms pre-poll claim dial. <!-- runpatterns: a scratch reproduction run on base 2bf29705 from an uncommitted test file (copy kept outside the repository); it is not committed, and its result is recorded on this line -->
- `go run mutants.py and mutants.py --until (10 product mutants against -run '^TestEnsureRunningUntil_ThePollEndsAtItsDeadline$' and '^TestEnsureRunningUntil_')`: All 10 mutants red; spawn.go restored afterwards
- `GOOS={windows,linux,darwin} go vet ./internal/daemon/`: PASS x3
- `go run ./tools/devtool fmt-check`: PASS (exit 0)
- `go run ./tools/devtool lint (full)`: exit 1. golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, runpatterns, docmarkers and coveragefloors all PASS. stubskips FAIL with one problem: the test/e2e binary was killed past -timeout=30m in pass 2 (30m10s) on the ten-seat co-loaded host. Pass 1 (72 packages, including internal/daemon) found no skip problems. Environmental, not from this diff.
- `go test -p 2 -count=20 -timeout=30m -run '^(TestEnsureRunningUntil_ThePollEndsAtItsDeadline|TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound|TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait|TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon|TestWaitForTick_EndsAtTheDeadlineNotTheTick)$' ./internal/daemon/`: PASS: 20/20 for each of the 5 rows, 0 FAIL
- `CGO_ENABLED=1 go test -race -p 2 -count=3 -timeout=30m -run (same 5-row pattern) ./internal/daemon/`: PASS: 3/3 for each row, no DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `QOMPACK_UNDER_COLOAD=1 go test -p 4 -count=50 -timeout=30m -run '^(TestEnsureRunningUntil_ThePollEndsAtItsDeadline|TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound|TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait|TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon)$' ./internal/daemon/`: PASS: 50/50 for each row; 1450 stalled-run subtests passed (simulated hosted stalls), 0 FAIL
- `QOMPACK_UNDER_COLOAD=1 go test -p 4 -cpu 1 -parallel 64 -count=50 -run (same 4-row pattern) ./internal/daemon/`: PASS: 50/50 for each row on one CPU <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=1 -timeout=30m -json ./internal/daemon/  (Windows, HEAD 3457c914)`: PASS: 1811 tests and subtests passed, 0 fail, 1 skip; package 478.1 s
- `sh .../linux-nonroot-gate.sh --out C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-spawn --prefix cx-w22-spawn 3457c914 daemon-full --gomaxprocs 2 -- ./internal/daemon`: BLOCKED, exit 127. The run started in the container (bundle verified; uid 10001, race=1). Mid-run the owner's Docker Desktop engine stopped: npipe dockerDesktopLinuxEngine is not found and the docker-desktop WSL distro is Stopped. I did not stop it and did not restart it. No artifacts; host record cx-w22-spawn-daemon-full-3457c91-20261004T223915Z.host.txt. I polled for about 25 minutes and the engine did not come back.

### Criterion changes

- TestEnsureRunningUntil_ThePollEndsAtItsDeadline: 'no dial of the poll may be bounded past its deadline' used to mean past the row's `until`. That only holds when the poll begins before until - after. It now means past the end of the wait the bound gives a poll beginning at the observed instant (max(begun+after, until), capped at latest, restated in the test rather than taken from bound.deadline). It is checked exactly as min(the dial's own timeout, that end) per dial. Rationale: the old criterion read the product's correct handling of a late-starting poll as a failure (hosted run 37229942287).
- Same row: the instant the poll returned after its deadline was a logged measurement (ADR 0010). It is now judged exactly on the step clock: the call returns at the end of its wait, or as the dial that overran it returns. This is stricter; real file I/O and OS scheduling are not modelled, and the doc comment says so.
- Same row: 'a dial cut short is no licence to spawn' now also covers a dial that returns at or after the end. It is paired with a positive control: a dial after a claim that had its whole bound (before the poll began, or inside the wait) and found nothing licenses one spawn, and the claim is kept for that daemon. Each run states wantSpawn explicitly.
- TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound: the 2 s untilDeadlineMargin is removed (until = daemon start plus one poll interval on the step clock). A control subtest is added, polled_for_the_fixed_bound: EnsureRunning's 1.5 s bound must miss the same daemon. The row now also asserts the daemon is found at the first tick after it comes up.
- TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait: now on the step clock. It asserts the daemon is found at the first tick after it comes up, where before it only asserted that it was found.
- TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon: 'does not wait' was time.Since(began) < 1.5 s. It is now zero step time plus exactly 2 dials (before and after the claim, none after the spawn). Stricter.

### Open issues

- The Linux full-package gate for internal/daemon at 3457c914 is still owed. The Docker engine went down mid-run, not by this seat. Rerun once the engine is back: cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-spawn && sh C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-spawn --prefix cx-w22-spawn 3457c914 daemon-full --gomaxprocs 2 -- ./internal/daemon. The interrupted run may have left /work/cx-w22-spawn-daemon-full-3457c91-20261004T223915Z* inside the container.
- Full devtool lint's stubskips failed only because the test/e2e binary hit -timeout=30m on the ten-seat co-loaded host. It needs a rerun on a quieter host. Every other lint sub-check passed.
- D61(c) residual, minor: the EnsureRunning rows in spawn_lock_test.go that use real listeners and real dials keep wall-clock margins. WaitsForTheDaemonAFreshSpawnLockAnnounces has about 1.3 s against a timer delay during the poll. AClaimThatGoesStaleMidWaitIsReclaimed has a 300 ms real ager against a 1 s wait. TwoColdHooksStartOneDaemon, AHookArrivingWhileAStagedSpawnIsStartingWaits, the second half of AnAbandonedSpawnLockBlocksOnlyUntilItIsStale, and spawn_home_test have about 4.8 s. I kept them on the wall clock because they are the only rows that exercise the real ipc dial path against real listeners; moving them to the step clock would lose that coverage.
- Known limit, documented and unchanged (D61(c), docs/troubleshooting.md:152-155): ipc lazySpawn (hook, command and MCP clients) spawns after a connect cut short against a live but busy listener. The duplicate loses daemon.lock and exits.
- Unverified, low: on Linux, Go's non-blocking connect to a unix socket whose listen backlog (somaxconn) is full may fail at once with EAGAIN instead of waiting as dial_other.go's comment says. A dial after a claim would then read as 'no daemon' and spawn a duplicate. Reaching it needs thousands of pending connections.

### Needs owner

- Docker Desktop's engine (owner's) stopped at about 22:50Z during this seat's Linux gate run. The internal/daemon Linux run at 3457c914 needs the engine back; the exact command is in open_issues.

## review:spawn:r1: verdict `sound`, 3 finding(s)

- **minor** `internal/daemon/spawn_lock_test.go:144-153 (TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces) and :333-367 (TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed)`: Two rows that test the same function in the same file still depend on a wall-clock margin, which D61(c) forbids. In the first, a 163 ms AfterFunc brings the daemon up against a 1.5 s poll. In the second, a 300 ms AfterFunc ager runs against a 1 s wait. A stall like H4's (about 1.4 s on hosted windows-latest), landing in a claim made after the poll has begun, ends the wait first. The product then correctly returns ErrNotFound, and the row reads that as a failure. The seat disclosed these rows as a residual and kept them on the wall clock to keep real-dial coverage. Under D66(b) they are the unswept rest of the class.
  - Evidence: I built an overlay of HEAD spawn.go with one 1.6 s sleep placed just before the first ipc.ClaimSpawn after the deadline is set. This models slow claim file I/O on a loaded runner. Scratch files: scratchpad/verifier-h4/stall_claim_spawn.go and ov_stall_claim.json. Command: go test -overlay ov_stall_claim.json -p 2 -count=1 -run '^TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces$' ./internal/daemon/ gave FAIL: 'Received unexpected error ... a spawn announced in a fresh spawn.lock must be waited for, not duplicated'. The same run with '^TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed$' gave FAIL: 'a daemon spawned on a claim reclaimed mid-wait must get its own wait to come up'. Logs: scratchpad/verifier-h4/stall-*.log.
  - Fix: Real dials can stay. Trigger the daemon's start and the ager from the call's own claim instead of from timers, as onFirstNowClock already does in TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn: for example, serve when clk is first read, or age the lock on the second claim. The verdict then no longer depends on scheduling. Otherwise, record them in the known issues under D66(d). They are outside wave 22's diff, so under D66(c) they do not block.
- **minor** `internal/daemon/spawn.go:298-307 (comment added in f0b9d525); internal/ipc/dial_other.go:27-32 (pre-existing comment)`: The new comment says a cut-short dial that returns before the end 'found no pipe or socket at all, and spawning then is right'. That is false on Linux. Go's non-blocking AF_UNIX connect fails at once with EAGAIN when the listener's accept queue is full. So a live but hung daemon with a full backlog fails every dial before the deadline, and both ensureRunningWith and ipc lazySpawn spawn a duplicate. The duplicate loses daemon.lock and exits, and nothing is lost. dial_other.go's claim that connect 'blocks until the listen backlog drains' is wrong for the same reason. The seat listed this as 'Unverified, low', but the source settles it.
  - Evidence: In Go 1.26.6, net/fd_unix.go (netFD.connect) treats only EINPROGRESS, EALREADY and EINTR as pending. EAGAIN falls through to the default case, which returns os.NewSyscallError("connect", err) at once. Linux unix_stream_connect returns -EAGAIN for a non-blocking socket when unix_recvq_full(other) is true. Connections from clients that have since closed stay queued until accepted, so a hung daemon fills a somaxconn-sized queue (4096 by default) after that many hook connects. Not reproduced at runtime: no Linux host was available, because the Docker engine is down.
  - Fix: Correct both comments to name the EAGAIN case, and add it to the release notes' known issues next to D61(c)'s lazySpawn limit. Optionally, have dial_other treat EAGAIN as busy and retry until the deadline, as go-winio does for ERROR_PIPE_BUSY. A cut-short dial would then never return a miss early on Linux either.
- **minor** `Linux non-root gate, internal/daemon at 3457c914`: The required Linux full-package run of internal/daemon is still owed. I could not run it either. Every Windows check passed, and the diff has no platform-specific code.
  - Evidence: At verification time, docker ps failed with 'open //./pipe/dockerDesktopLinuxEngine: The system cannot find the file specified', and wsl -l -v shows docker-desktop Stopped. The seat's host record cx-w22-spawn-daemon-full-3457c91-20261004T223915Z.host.txt has container_exit=127. What I did instead: GOOS=windows/linux/darwin go vet ./internal/daemon/ passed, and GOOS=linux and GOOS=darwin go test -c ./internal/daemon/ both build. What I ran on Windows at HEAD: the old row with a 1.4 s pre-claim stall (overlay, base files) reproduces both CI signatures exactly, so the root cause is confirmed as the fixture. Four independent product mutants all turn the new rows red (cut-short spawn allowed; judged by the bound rather than the return; held claim spawns at the end; in-flight wait counted from the call's start). The 5-row pattern passed at -count=20 (100/100, 680 subtests) and at -race -count=3 with no DATA RACE. QOMPACK_UNDER_COLOAD=1 -p 4 -count=50 passed (200/200, 1700 subtests). The full internal/daemon package passed: 1811 tests, 0 failures, 1 platform skip that predates this change. fmt-check passed. devtool lint passed every sub-check except stubskips, which I did not rerun; the diff adds no t.Skip.
  - Fix: Once the owner's engine is back, rerun: cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-spawn && sh C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-spawn --prefix cx-w22-spawn 3457c914 daemon-full --gomaxprocs 2 -- ./internal/daemon. D67(n)'s pre-freeze Linux gate on wave 22's touched packages also covers this.

## fix:spawn:r1: status `done`, head `a446e3fa1c1514410513162ba9560a336ffd3bf2`

### Root cause

H4 was a fixture problem, and the same class had spread to the other spawn rows. Each row's verdict depended on the call reaching a point (its first claim, its poll, its tick) before a wall-clock timer or bound ran out: a 163 ms daemon timer against a 1.5 s poll, a 300 ms ager against a 1 s wait, a 4.8 s margin, a 250 ms dial against an accept loop that started late, and half of a 5 s tick. A loaded runner that stalls about 1.4 s or more, as hosted windows-latest did, ends the wait first. The product then correctly returns ErrNotFound or spawns under D17a, and the row reads that as a failure. The product's spawn decision was sound in every case except one, which is now documented rather than changed: on Linux and macOS, a connect to a full listen queue fails at once, so a live daemon that has stopped accepting reads as absent. The duplicate it starts loses daemon.lock and exits.

### Summary

All three verifier findings are closed on closeout/w22-spawn at a446e3fa. The fix round was interrupted twice: once by the laptop-restart pause and once when a session ended. This run found its four commits (ca007f4b..a446e3fa) already on the branch with a clean tree. It checked them against the findings and finished the checks the interruptions had cut short. The tree is clean, nothing is pushed, and no other branch was touched.

FINDING 1 (wall-clock margin in spawn_lock_test.go; D61(c), D66(b)): fixed in ca007f4b and f9d4da8c.
- Both named rows now run their call on a stepPollClock, and so does the rest of the class in the file: TwoColdHooksStartOneDaemon, AHookArrivingWhileAStagedSpawnIsStartingWaits, AnAbandonedSpawnLockBlocksOnlyUntilItIsStale, ALockItsDaemonFreedIsNoLicenceToSpawn and WaitForTick_EndsAtTheDeadlineNotTheTick.
- The daemon coming up, the claim ageing and the end of staging are all scheduled at exact step instants and run in the call's own goroutine. No timer is involved.
- Real dials are kept. The daemons are still real ipc listeners, and the new stepProbeBy dials them through ipc.Probe with a bound no run reaches (beyondTestTimeout = 1 h). So which outcome a dial gets depends on where the step clock stands, not on scheduling.
- In the two-call rows, each call has its own clock. holdUntilUp keeps the waiting call from moving past a set instant until the other call's daemon is up.
- WaitForTick, which is production's own wall-clock wait, now uses a ticker that never ticks within a run. It asserts that the wait ended no earlier than its deadline, instead of asserting it took less than half of a 5 s tick.
- Red first: the old rows were built into old.test.exe and run against scratch stalls in overlays (redgreen.log):
  - a 1.6 s stall before the first claim turns the two named rows red, with the verifier's exact messages;
  - a 5.2 s stall before the first claim turns TwoColdHooks and AHookArriving red, and a 5.2 s stall before the first tick turns AnAbandoned red;
  - an accept loop started 400 ms late turns ALockItsDaemonFreed red;
  - a 2.6 s stall inside waitForTick turns WaitForTick red.
  The new rows pass under every stall, singly and all combined.
- I reran the verifier's own overlay (verifier-h4/ov_stall_claim.json, a 1.6 s sleep before the first ClaimSpawn) at HEAD. Both named rows pass: 1.61 s and 1.63 s.
- Mutants: six product mutants were tried. Each is killed by at least one row: fresh claim spawns, in-flight deadline kept after a spawn, no dial after the claim, poll dials never answer, waitForTick runs to the tick, waitForTick ends early. Within WaitsForTheDaemonAFreshSpawnLockAnnounces alone, 'poll dials never answer' survives. That row's daemon deletes spawn.lock, the claim then succeeds, and the dial after the claim finds the daemon, so the row's verdict ('waited for, not duplicated') is still right. Four other rows catch that mutant.

FINDING 2 (wrong EAGAIN comments): fixed in 9e4c3e0c and a446e3fa.
- The comment in spawn.go's cut-short case, the spawnClaimDialTimeout comment and the dialBusyRetryQuantum comment in dial_other.go now say the right thing. On Linux, Go's non-blocking AF_UNIX connect fails at once with EAGAIN when the listener's queue is full. macOS refuses such a connect at once (ECONNREFUSED). So a live daemon that has stopped accepting, with a full queue, reads as no daemon. The second daemon this starts loses daemon.lock and exits.
- The case is now a known limit next to D61(c)'s missed-connect limit, in three places:
  - troubleshooting.md, section 1 (qompack status);
  - release-notes/v0.3.0.md, known limits;
  - release.md, the residuals table.
- a446e3fa keeps this as its own entry, so it does not duplicate the docs seat's D61(c) entry (b1b246c8) on integration.
- The optional retry on EAGAIN in dial_other was not done. It would not close the limit on macOS, and the lazy spawners already spawn after a missed connect. The reasoning is in the commit message.
- The claim rests on Go and kernel source, not on a Linux runtime repro.

FINDING 3 (Linux gate owed): closed. The owner's engine is back, and the gate ran at a446e3fa in the container, uid 10001, with race on and gomaxprocs 2. It was run as two complementary runs to keep each under -timeout=30m:
- `--skip ^TestDelivery` over ./internal/daemon and ./internal/ipc: daemon 1011 pass, 0 fail, 1 platform skip (TestDrainWindowsOpenBlobRetainsCleanupIntent); ipc 160 pass. All 13 spawn rows ran and passed.
- `--run ^TestDelivery` over ./internal/daemon: 802 pass, 0 fail.
The earlier single full run at f9d4da8 was killed when the session ended (container_exit=143), not by a test.

OTHER CHECKS AT HEAD a446e3fa
- Full `devtool lint` passes every sub-check, but it took two invocations at the same HEAD. The 10:04 run passed golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck and stubskips (both passes; only the known platform-gated notices). Its process then died at runpatterns when the earlier session ended. This run finished the rest with `--only=runpatterns,docmarkers,coveragefloors`: all PASS, exit 0.
- `fmt-check`: exit 0.
- `GOOS=windows/linux/darwin go vet` on ./internal/daemon and ./internal/ipc: PASS on all three.
- Full Windows run of internal/daemon and internal/ipc: daemon 1811 pass, 0 fail, 1 platform skip (897 s); ipc 158 pass, 1 platform skip.
- A trial merge (`git merge-tree`) against closeout/integration 97eddc11 is clean. On that merged tree, vet passes on all three OSes and the 13 spawn rows pass at -count=3 (39/39).

Logs:
- Scratchpad: C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad
  - w22spawn-fr/: redgreen, mutants and stall overlays from the first pass;
  - w22spawn-fr3/: redgreen.log, mutants.log, rows13-c20.log, rows13-race3.log, coload50.out, daemon-full-windows.json, lint.log;
  - w22spawn-fr4/: lint-rest.log, verifier-overlay-head.log, merged-rows13-c3.log.
- Linux: C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-spawn/cx-w22-spawn-daemon-{nondelivery-ipc,delivery}-a446e3f-*-artifacts

### Commits

- f0b9d525 refactor(daemon): run the spawn poll on an injectable clock
- 3457c914 test(daemon): make the spawn poll's deadline rows deterministic
- ca007f4b test(daemon): run the spawn-lock rows on a step clock
- 9e4c3e0c docs(ipc): name a full listen queue as a fast connect failure
- f9d4da8c test(daemon): word the fresh-lock row's lock check as checked
- a446e3fa docs(release): keep the full-queue limit to its own case

### Tests

- `redgreen.py: old.test.exe vs new.test.exe for each spawn-lock row under its scratch stall overlay (PRECLAIM1=1.6s/5.2s, TICK1=5.2s, ACCEPT=400ms, WFT=2.6s), plus all stalls combined and no stall`: ALL-AS-EXPECTED. The 7 old rows fail under their stalls with the CI and verifier messages, for example 'a spawn announced in a fresh spawn.lock must be waited for, not duplicated' and 'a daemon spawned on a claim reclaimed mid-wait must get its own wait to come up'. All 9 new rows pass under every stall and under none.
- `go test -overlay verifier-h4/ov_stall_claim.json -p 2 -count=1 -v -timeout=30m -run '^TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed$' ./internal/daemon/  (and the same for '^TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces$'), at HEAD`: PASS (1.63 s and 1.61 s, so the verifier's 1.6 s stall was absorbed). The same overlay at 3457c914 FAILED, per the verifier's logs.
- `mutants.py: 6 product mutants via -overlay against the spawn-lock rows`: Each mutant is killed by at least one row. 'poll dials never answer' survives only in WaitsForTheDaemonAFreshSpawnLockAnnounces (its claim and post-claim dial find the daemon) and is killed by 4 other rows.
- `go test -p 2 -count=20 -timeout=30m -v -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces|TestEnsureRunning_TwoColdHooksStartOneDaemon|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits|TestEnsureRunning_ItsSpawnHoldsOffALazySpawn|TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale|TestEnsureRunning_ReleasesItsClaimWhenTheSpawnFails|TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn|TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed|TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound|TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait|TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon|TestEnsureRunningUntil_ThePollEndsAtItsDeadline|TestWaitForTick_EndsAtTheDeadlineNotTheTick)$' ./internal/daemon/`: PASS 260/260 top-level (13 rows x 20), 0 FAIL
- `CGO_ENABLED=1 go test -race -p 2 -count=3 -timeout=30m -v -run (same 13-row pattern) ./internal/daemon/`: PASS 39/39, 0 FAIL, no DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `coload50.py: QOMPACK_UNDER_COLOAD=1 go test -overlay w22spawn-fr/ov_new.json -p 4 -count=50 -timeout=30m -v -run '^<row>$' ./internal/daemon/, one row per run, each row under the stall that turned its old version red`: ALL-OK: 13 rows x 50/50 each, 0 FAIL (1600 ThePollEndsAtItsDeadline subtests, 100 PollsToItsDeadline subtests)
- `go test -p 2 -count=1 -timeout=30m -json ./internal/daemon/ ./internal/ipc/  (Windows, HEAD a446e3fa)`: PASS: daemon 1811 pass, 0 fail, 1 platform skip (TestService_StateWriteFailureStillEmits), 897 s; ipc 158 pass, 1 platform skip (TestWindowsPipeACLRejectsOtherUser)
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-spawn --prefix cx-w22-spawn a446e3fa daemon-nondelivery-ipc --gomaxprocs 2 --skip ^TestDelivery -- ./internal/daemon ./internal/ipc`: gate exit=0. daemon 1011 pass, 0 fail, 1 skip (TestDrainWindowsOpenBlobRetainsCleanupIntent); ipc 160 pass. uid 10001, race=1. All 13 spawn rows pass.
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-spawn --prefix cx-w22-spawn a446e3fa daemon-delivery --gomaxprocs 2 --run ^TestDelivery -- ./internal/daemon`: gate exit=0. daemon 802 pass, 0 fail, 0 skip, 602 s
- `GOOS={windows,linux,darwin} go vet -p 2 ./internal/daemon/ ./internal/ipc/`: PASS x3
- `go run ./tools/devtool fmt-check`: exit 0
- `GOFLAGS=-p=2 go run ./tools/devtool lint  (full run at a446e3fa, started 10:04; process died at runpatterns when the earlier session ended)`: PASS for golangci-lint, nomagic, importgraph (73 packages), testdeps (75), bindeps (6), sleepcheck, and stubskips (pass 1: 72 packages, 43m23s; pass 2: 1 package, 1h9m9s; only the known platform-gated notices)
- `GOFLAGS=-p=2 go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors  (same HEAD a446e3fa)`: PASS runpatterns, PASS docmarkers (407 docs), PASS coveragefloors (64 claims); exit 0
- `git merge-tree --write-tree closeout/integration(97eddc11) HEAD, then in an archive of that tree: GOOS={windows,linux,darwin} go vet ./internal/daemon/ ./internal/ipc/ and go test -p 2 -count=3 -v -run (same 13-row pattern) ./internal/daemon/`: Clean merge (tree 51e76250). Vet PASS x3. Rows 39/39 PASS. <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->

### Criterion changes

- TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces: now calls ensureRunningWith with pollBound{after: ensureRunningPollBound} on a step clock, instead of the public EnsureRunning. The nonexistent self path that would have failed if it spawned is replaced by a counting spawner that must stay at zero calls. Added: found at the first tick after the daemon is up, and spawn.lock gone (worded in f9d4da8c as 'removed by its daemon, none left by the call', because the check cannot tell a claim never taken from one given back). Rationale: D61(c). The 163 ms timer against a 1.5 s wait failed under a 1.6 s claim stall. EnsureRunning's own wall-clock wiring is still run by ItsSpawnHoldsOffALazySpawn and ReleasesItsClaimWhenTheSpawnFails, through ensureRunning.
- TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed: the claim ages at step 300 ms and the spawn stalls for 1.5 s of step time, where before it was a real 300 ms timer and a real stall. Added: a fixture assertion that the daemon comes up after the initial 1 s wait has ended, and an exact assertion that it is found at the first tick after it is up. Stricter.
- TestEnsureRunning_TwoColdHooksStartOneDaemon and TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits: each call runs on its own step clock, and the waiting call is held until the other call's daemon is up (holdUntilUp). In TwoColdHooks a spawned daemon comes up only once the other call has begun polling, which pins the overlap the wall-clock row got by chance. The assertions themselves (exactly one spawn, the second call does not spawn) are unchanged.
- TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale: now on a step clock. Added: the wait for the fresh lock ends exactly at its 200 ms bound, and after the lock goes stale the spawned daemon is found at the first tick after it is up. Stricter.
- TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn: the dial after the claim is real but bounded by beyondTestTimeout (stepProbeBy), not spawnClaimDialTimeout's 250 ms. The row therefore no longer checks that 250 ms is enough for a daemon whose accept loop starts late, which is a wall-clock margin under D61(c) and failed with a 400 ms late accept. It still checks that the product dials after the claim before spawning; the no_dial_after_claim mutant is killed. Added: the daemon is found at step instant zero, before any poll.
- TestWaitForTick_EndsAtTheDeadlineNotTheTick: 'elapsed < spawnLockTestBound/2' is replaced by 'ended no earlier than the deadline' (new: catches a wait that ends early). The slow ticker ticks only after 1 h, so a wait that ran to the tick now fails by the binary's -timeout rather than by an assertion. The fast-tick half's deadline moved from 5 s to 1 h. Rationale: D61(c); a 2.6 s stall failed the old elapsed check. The waitfortick_runs_to_the_tick and waitfortick_ends_early mutants are both killed.
- Docs: the full-listen-queue case on Linux and macOS is added as an accepted residual next to D61(c) in troubleshooting.md section 1, the release-notes/v0.3.0.md known limits and release.md's residuals table. Its own entry; the docs seat's D61(c) missed-connect entry is left alone.

### Open issues

- D66(d) residual, outside this seat's edit scope and outside wave 22's diff, so it does not block under D66(c). Two rows in other test files still use wall-clock bounds. The brief limits this seat to spawn_lock_test.go and new files, so the orchestrator should record them under D66(d). (1) The second half of TestEnsureRunning_RefusesTheHomeDirectory (internal/daemon/spawn_home_test.go:91) still calls ensureRunning with f.spawnUp, a 163 ms timer daemon, against spawnLockTestBound, a 5 s poll: about 4.8 s of margin. (2) strayClaimThenStop and TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon (internal/daemon/spawn_claim_release_test.go) bring real daemons up within 5 s Eventually and ConnectDeadline bounds.
- The EAGAIN and full-queue known limit is now documented, but it rests on Go and kernel source. It was not reproduced on a Linux runtime. The retry on EAGAIN in dial_other was deliberately not done; the rationale is in 9e4c3e0c.
- The full devtool lint at a446e3fa passed in two invocations at the same clean HEAD (the first process died at runpatterns when the earlier session ended), not in one uninterrupted run.
- The Linux gate for internal/daemon ran as two complementary runs (--skip ^TestDelivery with ipc, and --run ^TestDelivery) to keep each under -timeout=30m at gomaxprocs 2 with race on. The interrupted single run at f9d4da8 (container_exit=143) may have left /work/cx-w22-spawn-daemon-full-f9d4da8-20261005T012825Z* in the container. It was not removed, because the container is the owner's.


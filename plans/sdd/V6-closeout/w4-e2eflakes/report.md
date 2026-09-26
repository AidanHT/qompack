# V6 close-out w4-e2eflakes: load-sensitive Windows e2e rows

Branch `closeout/w4-e2eflakes`. Workflow `wf_85543bfd-f18`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `696ac7f`

### Root cause

(a) The Windows process probe, testutil.ProcessAlive, used GetExitCodeProcess. Windows sets a process's exit code before it closes the process's handles, and signals the process object only after that. In 120 of 120 measured exits the exit code was set before the object was signaled, by up to 151 ms. In 115 of them a file the child held without FILE_SHARE_DELETE could not yet be deleted. None was still held once the object was signaled. That gap is the 2-3 ms that w3 saw. Two smaller defects sat beside it. The helper learned the daemon's pid from one read of daemon.lock, and an empty, mid-CreateNew lock gave it pid 0, so the lock alone ended the wait. The row also timed a cmd.Wait goroutine's own scheduling.
(b) The fault was in the test rows, not the product. Since C1.16 the rehydrate service writes the state file after it answers, and the rows read it at once with os.ReadFile, which collides with WriteAtomic's DELETE-access rename handle. The product reader, CurrentDrops, reads shared.
(c) Four causes. The hook's client spool is written on a wall-clock ACK or connect miss (a late-ACK duplicate in every readable case), so it differs between arms under load. WaitIndexed counted the setup prompt's index line. Setup work leaked into only the observer-only arm's burst window. A standing client spool kept the spool watcher rewriting drain.json on its own clock.
(d) The failure is pure timing. It fails only at the bench harness's wall-clock gates for B-A and B-B, and every non-timing assertion passes under load.
Three more load-sensitive e2e rows turned up in the final runs. All three are test-side races against steps the product runs asynchronously or later: T13 (file version at step 7), ThinSlice (DAG after the capture link at step 6b) and V3 x08 (the observer's sketch save inside the SessionEnd that runs asynchronously since C1.15).

### Summary

W4-E2EFLAKES REPORT (branch closeout/w4-e2eflakes, worktree ../qompack-cx-w4-e2eflakes, review base 6aff949, HEAD 696ac7f; code-final 9e984d5).

## Final gates (every row)
- Windows, whole test/e2e at 9e984d5, `QOMPACK_UNDER_COLOAD=1` set exactly as `devtool test` sets it, no load generator of mine running: ok in 777.1 s. 102 of 103 top-level tests PASS and 0 FAIL.
  - The one SKIP is TestE2E_RequiredProductChildRaceInstrumentation, a permitted platform skip ("product-child race mode not requested").
  - Log: runs/pkg-test-e2e-windows-9e984d5.log.
- Linux, whole test/e2e at 9e984d5 through linux-nonroot-gate.sh (non-root uid 10001, -race, --coload, GOMAXPROCS 4): PASS, 300 cases pass, 0 fail, no race logs.
  - Three permitted skips: TestInstall_HostCLIInstallUpgradeUninstall and TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting ("claude CLI not on PATH"), and TestE2E_RequiredProductChildRaceInstrumentation.
  - Artifacts: runs/linux/cx-w4-e2eflakes-e2e-whole-9e984d5-9e984d5-20260926T185050Z-artifacts.
- Earlier whole runs, kept as evidence:
  - 78b33a1, Windows: 102/0/1 (runs/pkg-test-e2e-windows-78b33a1.log).
  - 78b33a1, Linux: FAIL 2 — ThinSlice and T13 (runs/linux/...-78b33a1-...).
  - a893e5d, Windows: 1 FAIL, V3 x08 (runs/pkg-test-e2e-windows-a893e5d.log).
  - a893e5d, Linux: PASS 300/0/3.
- The four target rows passed in every final run on both OSes.
- Every Windows run was co-loaded by other workstreams. My own Linux container run (GOMAXPROCS 4) overlapped the Windows runs. The Windows runs' "dirty entries" were untracked evidence files under plans/ only.

## Resume actions
- Merged 6aff949 (eval files only; clean build and vet).
- Reviewed the earlier seat's draft:
  - Kept 0de7707.
  - Its uncommitted helper edit had a mutation left in `settled()` (`return true // MUTATION M2`). I restored the real body before committing.
  - Split the draft into f6d9a71 and 87ffe91, and rewrote the spawn row's comment: its "goroutine scheduling only" causal story was incomplete, and (a) below corrects it.
- No product edit was left behind. Every diagnostic product edit I made was reverted with `git checkout` and recorded only as a .patch.txt.
- The earlier seat's partial logs are parked in my scratch dir, not committed. Everything I rely on was re-run on this branch. The (a)/(b)/(c) evidence comes from runs at the commits where each fix landed, and the whole-package gates are at 9e984d5.

## (a) TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight
**Root cause (Windows).** testutil.ProcessAlive asked GetExitCodeProcess. Windows sets the exit code before the kernel closes the process's handles, and signals the process object only after that.
- Measured (runs/diag-a-exitcode-vs-signaled-windows.txt, a standalone program, fresh OpenProcess per poll exactly as the old probe did):
  - In 120 of 120 exits the exit code was set before the object was signaled, by up to 151 ms. A daemon-sized child lagged 4.6-7.6 ms.
  - In 115 of 120, the child's file (held without FILE_SHARE_DELETE, as the day log is) could not be deleted at the instant the probe said "exited".
  - In 0 of 120 was the file still held once the object was signaled.
- So the helper could return while a daemon still held files open, and that is the 2-3 ms w3 saw against cmd.Wait.
- Two more defects:
  - The helper took the daemon's pid from one read of daemon.lock. An empty lock (paths.CreateNew creates the file, then writes its body) gave pid 0, and the lock's disappearance alone then ended the wait.
  - The row compared the helper's return with a cmd.Wait goroutine's time.Now, which measures that goroutine's scheduling.

**Changes:**
- beeaa15: ProcessAlive on Windows now opens the process with SYNCHRONIZE and treats it as alive until a zero-timeout WaitForSingleObject finds it signaled. The five-case table was re-measured (runs/diag-a-probe-table-windows.txt).
  - New test TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles (re-execs itself as a child holding 512 files and 64 MiB) requires the object signaled and the file deletable at the first "not alive". Old probe: FAIL at round 0. New: PASS 10/10.
- f6d9a71: e2eShutdownIfReachable records every pid the lock names on every check and waits for all of them.
  - New staged test TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit: an empty lock, then a body naming a live stand-in (`qompack mcp`), then the lock removed while the stand-in runs.
  - Old helper FAIL 2/2. Lock-only `settled()` mutation FAIL 2/2. New helper PASS 5/5.
- 87ffe91: the spawn row asserts the helper's own definition, e2eProcessAlive on its own child at the return, before reaping it.
  - Old assertion with the new probe: 20/20 under load.
  - Both rows under a CPU and fsync load generator: 40/40.
  - A helper that skips the spawn wait still fails 2/2.

## (b) TestV5_PreCompactToRehydrateToDroppedRoundTrip/full_budget_round_trip
The fault was in the test rows, not the product (0de7707, reviewed; numbers re-measured in 888ec93).
- Since C1.16 the service hands the compact answer over before its Record writes state/rehydrate-<sess>.json.
  - With a temporary 2 s delay before the Record, the pre-0de7707 rows (x04, c114, SessionStartClear) fail 3/3 with "file not found", and the current rows pass 3/3.
  - With the Record suppressed, the current rows fail at the wait 3/3 (not vacuous).
- The rows read with os.ReadFile, whose handle lacks FILE_SHARE_DELETE, so it collides with WriteAtomic's DELETE-access rename handle. Re-measured over 10 s each:
  - os.ReadFile: 863 sharing violations in 53,980 reads, and 98 of 402 replaces failed.
  - paths.ReadFileShared: 0 in 63,201 reads, and 0 of 449 replaces failed.
- The product's only reader, rehydrate.CurrentDrops behind the MCP dropped tool, already reads shared, so there is no product defect.
- It does race Record by design: the daemon builds two separate Reporters. 888ec93 therefore adds it to test/guards' sharedReaders inventory. Reverting it to os.ReadFile fails the guard.
- The rehydrate-state rows passed 16/16 under the load generator (-count=4). test/guards in full: only TestCarriedDefects_WaveReportRequiresResolution fails, which is expected until Phase 2.

## (c) TestV4_HotPathUnchangedWithTheFullWave3ResidentSet
Under the load generator the row failed 1/10 (capture count 7 of 8), then 5/15 on spool/<client> alone, in both directions. Diagnosis:
1. **Exact burst wait (26f8edf).** WaitIndexed(8) counted the setup prompt's index line, so a burst of 7 could pass. x13v4WaitBurstIndexed now waits for the 8 burst ids, draining on every tick.
2. **Criterion change (78b33a1).** spool/<client> is written by the HOOK on a missed connect or ACK deadline.
   - Where the spool was readable, the spooled delivery's nonce was already on a WAL line: a late ACK.
   - The equality now compares every file the daemon touched (x13v4DaemonWriteSet removes only that one token; a unit test shows every other planted token still fails it).
   - Each arm's fallback is read right after its burst and asserted by x13v4ClassifyFallback: every spooled line must be one of the arm's own burst deliveries, and the daemon's live hot-path receipts (B-A samples plus hotpath_sample_invalid) must be ≤ its hot-path WAL lines.
   - That is the one refusal that leaves no daemon-side trace. The other NAK routes write LOUD.log, which the equality keeps.
   - A temporary pre-WAL refusal of one wave-3 delivery fails the row 2/2.
3. **Two fixture defects the same load exposed** (no assertion changed):
   - Setup work could land in only the observer-only arm's window (its delivery journal opening there). x13v4SettleSetup now settles both arms before the baseline.
   - A standing client spool kept the spool watcher rewriting drain.json. The burst wait now also consumes the fallback.
   - The equality message now carries both arms' LOUD lines.

**Results:**

| Run | Result |
|---|---|
| Final, under the load generator | 20/20 |
| Final, beside V3 x11's 2,250 spawns | 20/20 |
| Linux -race | passes with 6-8 late-ACK duplicates per arm |

In the two Windows runs, 18 of 80 arms spooled, 41 deliveries in all, and every one was a late-ACK duplicate.

## (d) TestV3_HotPathUnchangedWithLedgerResident
This is a pure timing gate; no change. Both runs were alone on the loaded host at 78b33a1.
- Without the variable it fails only at v3_x11_test.go:487, the harness exit:
  - B-A p99 196.6 ms against 15. The harness also marks B-A uncertifiable: 528 of 2,064 samples never reached the daemon, because 593 deliveries deferred to the client spool.
  - B-B p99 180.2 ms against 50.
  - B-E wall 535 ms and B-E_cpu 62.5 ms both passed.
- With QOMPACK_UNDER_COLOAD=1 it PASSES in 401.8 s. Every non-timing assertion passes: 2,000 tool uses and 41 MB, 5,000 eliminations, N=2000, b_a_method, B-E_cpu gated at 62.5 ms against 2,000, B-D reported, and the waiver notes.
- It also passed in every co-load-declared whole-package run.
- The quiet C5.1 run still judges its numbers.

## Rows found in the final runs, fixed (test-side races)
- **T13, c0c4d96.** re_read after compaction answers from the file version appended at step 7, after the index record the row waited for.
  - A 3 s delay before step 7 reproduces the red on Windows.
  - The row now waits with x02WaitFileVersion: passes 2/2 with the delay, and fails at the wait with the version suppressed.
- **ThinSlice, a893e5d.** The row shut down after the index record. A shutdown before the 6b link cancels the first run, and the replay takes the redelivery path, which by design never emits the DAG node (step 6c).
  - A cancellable 3 s wait before 6b reproduces it 2/2. The same delay before step 10 does not.
  - The row now waits for the capture link: passes 2/2 with the delay, and fails at the wait with every link refused.
- **V3 x08, 9e984d5.** The row predates this branch: alone it failed 1/5 at 6aff949 and 2/5 at a893e5d.
  - Since C1.15 the SessionEnd, which writes touch.cms and explore.hll, runs asynchronously after the flush hook. A diagnostic dump found the file absent at the check and present 3 s later, with no Loud line.
  - A 3 s delay at the start of the observer's SessionEnd fails the row 3/3.
  - obsRunFlush's marker wait is split out as obsAwaitSessionEnded and x08 now uses it: passes 3/3 with the delay; with the sketch save suppressed it still fails at touch.cms.
  - x08 and both obsRunFlush users pass 4/4.
- ThinSlice and T13 each passed 5/5 alone on Linux before the fix.

## Checks at 9e984d5
All exit 0: fmt-check; go vet on internal/testutil, test/e2e and test/guards (windows and linux); pinned golangci-lint on those packages; `devtool lint --only=nomagic,importgraph,testdeps,bindeps,sleepcheck`. stubskips was not run because it is whole-tree. The test/docs and gen-docs checks do not apply: no docs or generated inputs changed.

## Scope notes
Two edits sit outside test/e2e:
- internal/testutil (beeaa15): it is e2eProcessAlive's single implementation. The same probe backs the shutdown helpers in test/fault, test/platform, test/release, test/security and test/guards; this workstream did not run those packages in full, and C3.2 will.
- test/guards (888ec93): the product reader of (b), pinned. The earlier seat's committed evidence file names were superseded by re-runs.

### Commits

- 0de7707 fix(e2e): wait for the rehydrate state and read it shared (earlier seat; reviewed and kept, numbers re-measured in 888ec93)
- 86ebd5c chore(v6): merge closeout/integration 6aff949
- beeaa15 fix(testutil): count a windows process alive until it is signaled
- f6d9a71 fix(e2e): wait for every daemon seen holding the lock
- 87ffe91 test(e2e): ask the spawn row's daemon the helper's own question
- 888ec93 test(guards): pin the rehydrate drop report's shared read
- 26f8edf fix(e2e): wait for x13's own burst before walking either arm
- 78b33a1 test(e2e): assert x13's hook fallback per arm, not by equality
- c0c4d96 fix(e2e): wait for t13's file version before its re_read
- a893e5d fix(e2e): let the thin-slice row's last event link before shutdown
- 9e984d5 fix(e2e): wait for x08's session end before reading its sketches
- 696ac7f docs(closeout): record the w4-e2eflakes gate runs

### Tests

- `exitdiag.exe -iters=40 -fresh with (-n=2000 -mb=256), (-n=20 -mb=32), (-n=5 -mb=8) (standalone program, source in runs/diag-a-exitcode-vs-signaled-windows.txt)` — The exit code was set before the process object was signaled in 120 of 120 exits (max 151 ms). The child's file was still held at that instant in 115 of 120. It was held in 0 of 120 once the object was signaled.
- `go test ./internal/testutil -run '^TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles$' -count=1 -v (old GetExitCodeProcess probe)` — FAIL round 0: event 0x102 (not signaled) at the first 'not alive' (runs/a-testutil-red-oldprobe-windows.log)
- `go test ./internal/testutil -run '^TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles$' -count=10 -v (new probe)` — PASS 10/10 (30 child exits); go test ./internal/testutil -count=1: ok
- `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit$' -count=2 -v (with the HEAD~ faultinject_test.go helper)` — FAIL 2/2: returned once the lock was released while the stand-in was running
- `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit$' -count=5 -v (new helper; also -count=2 with a temporary lock-only settled())` — New helper: PASS 5/5. Lock-only mutation: FAIL 2/2.
- `go test ./test/e2e -run '^(TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight|TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit)$' -count=20 -v under the CPU+fsync load generator` — PASS 40/40. With the spawn wait mutated out, the spawn row fails 2/2. The old timestamp assertion with the new probe passed 20/20.
- `go test ./internal/paths -run '^TestZZDiagW4_' -count=1 -v (temporary copy of runs/diag-b-sharing-modes_windows_test.go.txt, removed after)` — os.ReadFile: 863 sharing violations in 53,980 reads and 98 of 402 replaces failed. ReadFileShared: 0 in 63,201 reads and 0 of 449 replaces failed. A DELETE-access handle blocks os.ReadFile but not ReadFileShared.
- `go test ./test/e2e -run '^TestV5_PreCompactToRehydrateToDroppedRoundTrip$/^full_budget_round_trip$' and -run '^(TestE2E_SessionStartCompactFitsTheHostCap|TestE2E_SessionStartClear)$', with a temporary 2 s Record delay, then with the Record suppressed` — Delay, pre-0de7707 tests: FAIL 3/3, file not found. Delay, current tests: PASS 3/3. Suppressed: FAIL 3/3 at scAwaitState.
- `go test ./test/e2e -run '^(TestV5_PreCompactToRehydrateToDroppedRoundTrip|TestE2E_SessionStartCompactFitsTheHostCap|TestE2E_SessionStartCompact|TestE2E_SessionStartClear)$' -count=4 -v under load` — PASS 16/16 top-level, including every x04 subtest
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v (and with a temporary os.ReadFile in CurrentDrops)` — Green: PASS, including the CurrentDrops row. Mutation: FAIL on that row.
- `go test ./test/guards -count=1` — FAIL only in TestCarriedDefects_WaveReportRequiresResolution (expected until Phase 2). Everything else passes.
- `go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=10 / -count=15 (instrumented) under load, before the fixes` — 9/10: the observer-only arm's capture count was 7 against 8. 10/15: five equality failures on spool/<client> alone, in both directions; where readable, each spooled nonce was already in the WAL.
- `go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=20 at the intermediate cut (spool read after the walk, no settles yet), under the load generator with the CPU at 100 %` — 13/20: seven daemon-set diffs (journal files opened in one arm's window, tmp/<staging>, LOUD.log). This led to the two settles.
- `go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=20, final code, twice under load (the second beside V3 x11)` — PASS 20/20 and 20/20. 18 of 80 arms spooled, 41 deliveries in all, every one a late-ACK duplicate; received equalled durable (9) every time.
- `ZZMUT_W4_REFUSE=1 go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=2 (temporary product mutation, reverted)` — FAIL 2/2: received 9 but only 8 WAL lines, so the refusal is caught
- `go test ./test/e2e -run '^(TestV4_X13DaemonWriteSetSetsAsideOnlyTheHookFallback|TestV4_X13ClassifyFallback|TestV4_X13NormalizeFoldsOnlyThePerRunComponent|TestV4_X13NormalizeSeparatesWhatItUsedToConflate|TestV4_X13WriteSetSeesWhatItUsedToConflate|TestV4_X13NormalizeIsIdempotent)$' -count=1 -v` — PASS 6/6
- `QOMPACK_UNDER_COLOAD=1 go test ./test/e2e -run '^TestV3_HotPathUnchangedWithLedgerResident$' -count=1 -v (alone, loaded host, 78b33a1)` — PASS in 401.8 s. Wall rows reported (B-A p99 180.2 ms). B-E_cpu gated at 62.5 ms and passed. All structural assertions pass.
- `go test ./test/e2e -run '^TestV3_HotPathUnchangedWithLedgerResident$' -count=1 -v, without the variable (alone, loaded host, 78b33a1)` — FAIL only at v3_x11_test.go:487, the harness exit: B-A p99 196.6 against 15 ms, B-B p99 180.2 against 50 ms. B-E wall and B-E_cpu passed. A timing gate only.
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w4-e2eflakes --out C:/.../runs/linux 78b33a1 two-reds-alone --run '^(TestE2E_ThinSliceDropsControlOnlyEdges|TestV4_T13HandleResolvesAfterCompactionOverStdio)$' --count 5 --coload -- ./test/e2e` — PASS 10/10, so load-sensitive
- `go test ./test/e2e -run '^TestV4_T13HandleResolvesAfterCompactionOverStdio$' with a temporary 3 s delay (then a suppression) before observer step 7` — Old test with the delay: FAIL, same message as the Linux red. Fixed with the delay: PASS 2/2. Fixed with the version suppressed: FAIL at x02WaitFileVersion.
- `go test ./test/e2e -run '^TestE2E_ThinSliceDropsControlOnlyEdges$' with temporary delays before step 10, before 6b (cancellable), and with every link refused` — Step-10 delay, old test: PASS 2/2 (hypothesis ruled out). 6b delay, old test: FAIL 2/2, same as Linux. 6b delay, fixed: PASS 2/2. Links refused, fixed: FAIL at the link wait.
- `go test ./test/e2e -run '^TestV3_DegradedPassiveStillRecordsEverything$' -count=5 (on a893e5d, and on a git-archive export of 6aff949)` — FAIL 2/5 on a893e5d and FAIL 1/5 at 6aff949, so the failure predates this workstream
- `go test ./test/e2e -run '^TestV3_DegradedPassiveStillRecordsEverything$' with a temporary 3 s delay at the start of the observer's SessionEnd, then with its sketch save suppressed` — Delay, old test: FAIL 3/3. Delay, fixed: PASS 3/3. Suppressed, fixed: FAIL at touch.cms (the assertion still guards the file).
- `go test ./test/e2e -run '^(TestV3_DegradedPassiveStillRecordsEverything|TestE2E_ObserverThroughDaemon|TestV5_HookEventToTombstoneToRetrievalAfterRestart)$' -count=4 -v` — PASS 12/12
- `QOMPACK_UNDER_COLOAD=1 go test ./test/e2e -count=1 -v -timeout=120m (Windows, whole package, 9e984d5)` — ok in 777.1 s: 102 PASS, 0 FAIL, 1 platform SKIP (runs/pkg-test-e2e-windows-9e984d5.log). At 78b33a1: 102/0/1. At a893e5d: 1 FAIL, V3 x08, since fixed.
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w4-e2eflakes --out C:/.../runs/linux 9e984d5 e2e-whole-9e984d5 --coload --timeout 150m -- ./test/e2e` — PASS: 300 cases, 0 fail, 3 platform skips, no race logs. At a893e5d: PASS 300/0/3. At 78b33a1: FAIL 2 (ThinSlice, T13), since fixed.
- `go run ./tools/devtool fmt-check; go vet (windows and GOOS=linux) ./internal/testutil ./test/e2e ./test/guards; pinned golangci-lint on the same; go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,bindeps,sleepcheck (at 9e984d5)` — All exit 0 (runs/lint-final-windows.log)

### Criterion changes

- test/e2e/v4_x13_test.go (78b33a1): spool/<client>, the hook's client fallback spool, no longer takes part in the write-set equality between the arms; it is asserted per arm instead. Rationale: a hook writes it on a missed connect or ACK deadline, a wall-clock outcome that config.AckDeadlineMsWindows's derivation says engages under ordinary load, and the row's header defers timing to test/bench/hotpath. Under load the equality failed 5/15 on that token alone, in both directions (the observer-only arm spooled in 3). The per-arm assertion x13v4ClassifyFallback requires every spooled line to be one of the arm's own burst deliveries, and the daemon's live hot-path receipts to be ≤ its hot-path WAL lines. That catches the one daemon-side refusal that leaves no daemon-side trace (demonstrated: a pre-WAL refusal fails 2/2). The other NAK routes write LOUD.log, which stays in the equality. The normalizer, its tests, and every other token are unchanged.
- test/e2e/shutdown_spawn_inflight_test.go (87ffe91): the row's 'gone' check changed from 'a cmd.Wait goroutine's time.Now is not after the helper's return' to 'e2eProcessAlive(own child) is false at the return, before reaping'. Rationale: the old measure included that goroutine's scheduling lag, which no correct helper can beat. The new one asks exactly the helper's contract, and since beeaa15 it means the process object is signaled, which comes after its handles close. An early-returning helper still fails 2/2.
- internal/testutil/procalive_windows.go (beeaa15): Windows 'not alive' now means signaled rather than exit-code-set. This is stricter, never looser; callers can only wait slightly longer. All other changes (0de7707, 26f8edf, x13's settles, c0c4d96, a893e5d, 9e984d5) are readiness waits for the exact step each assertion reads, and none changes an assertion.

### Open issues

- Hook ACK fallback is frequent under co-load: 593 of 2,130 x11 deliveries deferred to the client spool, and on Linux -race all 6-8 x13 hooks per arm spooled as late-ACK duplicates. This is SP05-D2's ACK deadline engaging as its own derivation predicts; it is informational and was not changed.
- Product trade-off made visible by ThinSlice: a delivery whose first run is cut after its index record (6a) and before its link (6b) is replayed through the redelivery path. That path by design never emits the DAG node (tooluse.go step 6c), so the persisted graph permanently lacks it. The code documents this, but it is a derived-data loss on a Stop landing mid-publication; not changed.
- Under a 100 % CPU run at the intermediate x13 cut, before the settles, LOUD.log appeared in one arm's delta twice, cause unidentified. It did not recur in 80 later runs, and the equality message now prints both arms' LOUD lines for the next occurrence.
- Dependents of testutil.ProcessAlive (test/fault, test/platform, test/release, test/security, and test/guards' v1 helper) were not run in full here, except test/guards. C3.2 will exercise them with the stricter probe.
- The x13 Received count relies on a spelled counter name (hotpath_sample_invalid) and the B-A histogram looked up from obs.Budgets(). If the counter were renamed, Received would undercount silently and the check would weaken rather than fail.
- The staged holder test's sequence must fit inside the helper's 20 s e2eDaemonDownBound. It runs in about 2 s, so the margin is large.
- test/e2e/scheduler_idle_test.go still says the helper treats lock release as 'gone'. That wording is now stale but harmless.
- 0de7707's commit message quotes the earlier seat's sharing numbers (1,123 of 64,245). The re-run numbers are in 888ec93 and the code comment.
- The earlier seat's partial logs are kept in my scratch dir (resume/e2eflakes-work/earlier-seat-runs) and are not committed.
- The MCP dropped tool queried within milliseconds after a compaction can say 'nothing dropped' until the Record lands; that follows from C1.16's ordering, by design.

### Needs the owner

- Accept or reject the x13 criterion change: spool/<client> moves from the cross-arm equality to a per-arm assertion (rationale in criterion_changes and in x13v4HookFallbackToken's doc).
- Awareness and decision: the step 6c redelivery path permanently drops a first run's DAG node when a Stop or failure lands between 6a and 6b (surfaced by ThinSlice). Is preserve-and-report enough, or should replay emit it?
- Informational, SP05-D2 follow-up: the 73 ms (Windows) and 17 ms (Linux) ACK deadlines engage heavily under co-load (in the x11 run, 593 of 2,130 deliveries were deferred to the client spool).

## Independent review

### review:e2eflakes: needs-fixes

- **minor** `test/e2e/observer_e2e_test.go:196,206 (obsSessionEndMarker / obsAwaitSessionEnded, now also used by v3_x08_test.go:286-288)` — The new session-end wait that x08 now depends on polls run/marker.json with os.ReadFile. The daemon replaces that file with paths.WriteAtomic (internal/contract/marker.go:46). That is the exact reader-blocks-writer pattern this branch fixed for state/rehydrate-*.json in (b).
  - Evidence: The branch's own measurement (runs/diag-b-sharing-modes-rerun-windows.txt, quoted in scAwaitState's doc): an os.ReadFile reader racing WriteAtomic failed 98 of 402 replaces. obsAwaitSessionEnded polls on obsProcessTick while the daemon's SessionEnd writes the marker exactly once. If that one replace fails, the wait times out, which makes a new co-load flake source for x08. Before 9e984d5, x08 did not poll the marker at all.
  - Fix: Read through paths.ReadFileShared(contract.MarkerPath(root)) in both obsSessionEndMarker and obsAwaitSessionEnded, as scAwaitState does. The product's own reader, contract.readMarker (marker.go:58), has the same os.ReadFile pattern. Route it to its owner or the sharedReaders guard as a follow-up. Do not fix it here, because it is outside this scope.
- **minor** `test/e2e/v5_x04_test.go:576 (negative_control_reinjection_disabled), also the dropped-tool zero checks at :580-585` — This branch established that the rehydrate Record lands after the compact answer (C1.16). The positive arms now wait for it, but the negative control still asserts NoFileExists and a zero drop count right after the answer. A regression that wrote the state file with injection off would usually land after these checks, so the control passes vacuously. The task said never to weaken what a row proves, and this arm is one of the (b) rows.
  - Evidence: The 0de7707/888ec93 messages and scStateRecordBound's doc say the file is written after the answer. The implementer's own open_issues says the dropped tool 'can say nothing dropped until the Record lands'. Line 576 has no barrier between r.CompactStart and require.NoFileExists.
  - Fix: Order the absence checks after anything the Record could still be doing. Either stop the rig (Stop waits for the service's goroutines) and then assert the file is absent, or move the absence assertions after a later request that the service handles strictly after the Record. The simplest option is to assert absence after the rig's clean shutdown at the end of the subtest, and keep the immediate check as well.
- **minor** `test/e2e/faultinject_test.go:289-317 (e2eShutdownIfReachable loop)` — The holder set can still end up empty. Holders are observed once before the loop and then only after each admin.shutdown Send. Take this sequence: the initial observation sees the empty lock. The daemon writes its body and starts listening. The next Send reaches it, and its Stop and Lock.Release finish before the check that follows that Send. That check then sees no lock, no pid was ever recorded, settled() is true, and the helper returns while the daemon unwinds. This is the pid-0 defect f6d9a71 targets, in a narrower window that the staged test (a slow holder) does not cover.
  - Evidence: Line 289 is holders.observe(e2eDaemonHoldingLock(root)) before the loop. Line 302 is c.Send(... OpAdminShutdown ...). Line 316 is holders.observe(lockPID, held), after the Send. No observation sits between a Send that can trigger release and the moment the body became readable.
  - Fix: Call holders.observe(e2eDaemonHoldingLock(root)) immediately before each c.Send as well. The body lands microseconds after CreateNew, so a daemon that can receive this Send has already had its pid read. Also consider not treating 'held with no pid ever resolved' as settled until the set is non-empty or the spawn marker is stale.
- **minor** `test/e2e/faultinject_test.go:152-159 (e2eShutdownIfReachable doc comment)` — The helper's header still defines 'gone' as the lock disappearing: "The lock is released last, so its absence is the only signal that means the process is done." Item (a) asked for one definition of 'gone', and the code and the new rows now define it as no live holder AND every observed holder's process signaled/exited. The helper's own doc contradicts that.
  - Evidence: Lines 152 and 159 say lock absence is the definition. Lines 263-289 say process death, not lock absence, is the condition. The implementer flagged only scheduler_idle_test.go's stale wording.
  - Fix: Rewrite the header paragraph to state the definition the loop implements: no live process holds the lock, and every pid seen holding it has exited, which on Windows means its process object is signaled. Fix scheduler_idle_test.go's wording in the same edit.
- **minor** `test/fault/fault.go:948,1082; test/platform/platform.go:1131,1162; test/release/release.go:781,818; test/security/security.go:1221,1288; test/guards/v1_integration_test.go:942` — The sibling shutdown helpers keep the single-read shutdownPID, which treats pid 0 (an empty, mid-CreateNew lock) as settled. That is the defect f6d9a71 fixed in test/e2e. The report lists these packages only as ProcessAlive dependents to re-run, not as carrying the same pid-0 defect.
  - Evidence: Each of these helpers does `shutdownPID, _ := daemonHoldingLock(root)` once and has `if shutdownPID == 0 || shutdownPID == os.Getpid() { return true }` in its settled check. None of them has e2eAwaitSpawnInFlight, so the window is narrower, but the logic is the same.
  - Fix: These packages are outside this workstream's scope, so leave them. Add the defect to open_issues or needs_owner as a carried item for C3.2, or port e2eLockHolders to them in the workstream that owns them.
- **minor** `plans/sdd/V6-closeout/w4-e2eflakes/ (missing report.md)` — Every other close-out workstream commits a report.md next to runs/ (w2-eval2, w2-hookout, w2-lifetime, w2-lint, w2-rollover2, w2-sessionend, w2-wintriage, w3-e2ereds, w3-eval3). This one commits only runs/. The per-row verdicts, the x13 criterion change that needs an owner decision, the (d) timing-only confirmation and the needs_owner items therefore exist only in the transient structured result and scattered commit messages.
  - Evidence: `ls plans/sdd/V6-closeout/w4-e2eflakes` prints only runs. `git diff --name-only 6aff949 HEAD` adds no report.md.
  - Fix: Commit plans/sdd/V6-closeout/w4-e2eflakes/report.md with the summary, every row's result, the criterion_changes rationale, open_issues and needs_owner. Scan the SHAs it quotes for reachability.
- **minor** `plans/sdd/V6-closeout/w4-e2eflakes/runs/pkg-test-e2e-windows-9e984d5.log (final Windows gate)` — The task said to finish with the whole package 'alone on Windows (quiet as you can make it)'. By the implementer's own account, the final Windows run overlapped its own Linux container run (GOMAXPROCS 4) and ran with QOMPACK_UNDER_COLOAD=1, which waives the wall-clock gates. The gate is therefore not as quiet as this seat could have made it, and it does not judge the timing rows.
  - Evidence: Summary: 'My own Linux container run (GOMAXPROCS 4) overlapped the Windows runs', and the Windows run was made 'QOMPACK_UNDER_COLOAD=1 set exactly as devtool test sets it'.
  - Fix: Re-run the whole test/e2e package on Windows with no concurrent job of this seat's, or state in the report that the quiet whole-package Windows gate is deferred to C5.1 along with the timing rows.
- **nit** `test/e2e/v4_x13_test.go:533 (x13v4SampleInvalidCounter)` — Received adds snap.Counters["hotpath_sample_invalid"], spelled by hand. A rename in internal/daemon/handlers.go:88 would silently read 0, and the Received <= Durable refusal check would weaken instead of failing. The implementer disclosed this.
  - Evidence: A missing map key reads as 0. The B-A histogram lookup goes through x1v5HistOf, which fails loudly, but the counter lookup does not.
  - Fix: Require the counter to be registered, for example with require.Contains(snap.Counters, x13v4SampleInvalidCounter) after a registration, or add a test/guards source check that pins the constant's spelling against handlers.go.
- **nit** `commit 696ac7f` — The docs(closeout) commit has no Refs footer. Every other non-merge commit on the branch carries 'Refs: V6-VERIFY, C3.2'.
  - Evidence: `git log -1 --format=%B 696ac7f | grep -c '^Refs:'` prints 0. Prior docs(closeout) commits are mixed: 36c4fe9 has a footer, 4aaffea does not.
  - Fix: Add 'Refs: V6-VERIFY, C3.2' if the branch is rewritten before integration. Otherwise leave it.

## Fix seat (review resolution) — status `done`, head `8cf81376e0fba5208f50ca8116412230c20af371`

### Root cause

The Windows e2e rows that failed only under load were test-side races, and none was a product defect.
- A test reader held an ordinary os.ReadFile handle on a file the daemon replaces with WriteAtomic (the rehydrate state, and the session-end marker), which fails the replace.
- Rows read results that the daemon writes after it answers: the rehydrate Record since C1.16, and the observer's SessionEnd since C1.15.
- The shutdown helper's "gone" disagreed with its own tests. Three separate faults sat under that:
  - Windows ProcessAlive used GetExitCodeProcess, which reports an exit while the process still has handles open.
  - The helper read the pid once and could get 0.
  - A lock holder whose pid was never read counted as settled.
- The x13 equality compared a hook's wall-clock fallback spool as if the daemon had written it.
- The V3 hot-path row is a pure wall-clock gate. Its deferrals are the §12.2 spool transition that follows a B-A breach.

### Summary

# w4-e2eflakes: fix seat report (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes, branch closeout/w4-e2eflakes, HEAD 8cf8137, review base 6aff949)

All seven review findings were checked independently. Five are confirmed and fixed with a failing test first (F1-F4, F7). F5 is confirmed and carried because it is out of scope. F6 is handed to the coordinator because the harness refuses report files from subagents; this summary is the report. The tree is clean. Every SHA below was checked with `merge-base --is-ancestor` and is reachable from HEAD.

## Per-row verdicts (the task's items a-d)

**(a) TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight.** Test-infrastructure defect, not a product defect.
- **Probe:** on Windows, `testutil.ProcessAlive` used GetExitCodeProcess. Windows sets the exit code up to 151 ms before it signals the process object, while the process's handles are still open. This happened in 120 of 120 measured exits. Fixed in beeaa15 with a SYNCHRONIZE handle and a zero-timeout wait.
- **Assertion:** the row compared the helper's return with the moment a goroutine's cmd.Wait came back. It now asks the helper's own question at the return (87ffe91).
- **Pid 0:** the helper read the lock once, so it could see pid 0 (f6d9a71 fixed that).
- **Unidentified holder (fix seat, f84e0a2):** a holder whose pid was never read could still count as gone.
- **One definition of "gone"** now holds for the helper, its doc and every caller:
  - no live process holds daemon.lock;
  - every pid seen holding it has exited (on Windows, its process object is signaled);
  - no holder went unidentified.

**(b) TestV5_PreCompactToRehydrateToDroppedRoundTrip.** The test's reader was at fault, not the product.
- The rows read with os.ReadFile, which on Windows takes a handle without delete sharing, and they read before the Record had landed. Since C1.16 (e7c1954) the service writes the state file after it answers.
- The product's reader (`rehydrate.CurrentDrops`, behind the MCP dropped tool) already uses `paths.ReadFileShared`, so there is no product defect. The guard pins it (888ec93); the row fix is 0de7707.
- The fix seat also closed a negative-control arm that could pass without testing anything (8471f30) and fixed the same reader pattern in the session-end marker poll (c488456).

**(c) TestV4_HotPathUnchangedWithTheFullWave3ResidentSet.**
- **What changed:** spool/<client> is written by the hook when a wall-clock connect or ACK deadline passes. It leaves the arm-to-arm equality and is asserted per arm instead (78b33a1). The burst wait now waits for the burst's own ids (26f8edf).
- **Power kept:** temporarily refusing one delivery still fails the row 2 of 2. The rationale is on `x13v4HookFallbackToken` in test/e2e/v4_x13_test.go.

**(d) TestV3_HotPathUnchangedWithLedgerResident is purely a timing gate.** Its budget is unchanged.
- Run alone at 8471f30 with co-load declared, it passes: B-E_cpu is gated and every non-timing assertion is checked.
- Run strict, it fails only the B-A and B-B wall-clock gates.
- **New fact:** it fails strict even with host CPU at 0-32%. B-A p50 was 16.4 ms in the whole-package run and 18.4 ms alone, against a 15 ms limit.
- **The deferrals are not random.** Every recorded run shows exactly 593 of 2130 deliveries deferred. That is the §12.2 breach transition in internal/daemon/budget.go: the detector uses 512-sample windows, three consecutive windows over budget move the daemon to spool submode, and that happens at sample 1536.
- **Why B-B also fails:** those deferrals are why B-B is reported "uncertifiable" even though its delivered p99 of 12.3 ms is under 50 ms. B-B is a consequence of the B-A breach, not a separate defect.
- C5.1 judges the numbers.

**Other rows the implementer fixed** (reds from the whole-package runs; no assertion changed):
- c0c4d96: T13 now waits for the file version before its re_read.
- a893e5d: the thin-slice row waits for the capture link before shutdown.
- 9e984d5: x08 waits for session end before reading its sketches.

## What the fix seat changed

**f84e0a2**: files `test/e2e/faultinject_test.go` and `test/e2e/shutdown_spawn_inflight_test.go`.
- The helper checks the lock immediately before each admin.shutdown as well as after it.
- `e2eLockHolders` now records an unidentified holder: a lock seen held with no readable pid and gone at a later check. `settled()` stays false for the rest of the call, and the helper waits out e2eDaemonDownBound and says so.
- The helper's header now states the one definition of "gone".
- New row `TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified`. It creates an empty lock and removes it without ever writing a body, and it bounds the return from below.

**dc41e2d**: comments only. scheduler_idle_test.go described the old "gone". The x11 and x16 notes wrongly said an in-process daemon spends the whole bound. The 9e984d5 log has no such diagnostic, because own-pid settling already existed.

**c488456**: `obsSessionEndMarker` and `obsAwaitSessionEnded` now read through `paths.ReadFileShared`. test/guards/sharedreaders_test.go gains two test-helper rows (`obsSessionEndMarker`, `scAwaitState`), and its doc says when a test helper earns a row.

**8471f30**: the x04 negative control also asserts the state file absent after `r.D.Stop`. Stop joins the compact reply work within its drain grace before cancelling it. The immediate checks stay.

**8cf8137**: evidence under plans/sdd/V6-closeout/w4-e2eflakes/runs/.

## Review resolution

- **F1** (marker poll with os.ReadFile): **confirmed and fixed in c488456.**
  - A throwaway tight-loop reader against 400 WriteMarker calls: with os.ReadFile, 245 of 400 replaces failed (152 on a second run). Through obsSessionEndMarker on ReadFileShared, 0 of 400 failed (runs/fix-b-diag-marker-*.log; the diagnostic source is runs/diag-fix-b-marker_test.go.txt).
  - The new guard row fails on the old read. x08 and the two obsRunFlush users pass 3 of 3.
  - The product's `contract.readMarker` is routed to its owner (open issues).
- **F2** (x04 negative control could pass vacuously): **confirmed and fixed in 8471f30.**
  - A diagnostic daemon that answers {} with injection off and records 2 s later passes the old arm 2 of 2 (runs/fix-c-x04-negctl-vacuous-oldtest-windows.log).
  - The new arm fails it 2 of 2 at the post-Stop check. On the shipped daemon the whole row passes 3 of 3.
  - With the file absent through Stop, the dropped-tool zero counts are no longer vacuous either. The patch is runs/diag-fix-c-x04-late-record.patch.txt; it was never committed.
- **F3** (holder set can end empty): **confirmed and fixed in f84e0a2.**
  - The helper now checks before each Send and never treats an unidentified holder as settled.
  - The new row fails the old helper 2 of 2, and fails 2 of 2 when `settled()` ignores the unidentified flag. All three shutdown rows pass 3 of 3.
  - In the whole-package run, the 20 s wait fired only in the staged row itself.
- **F4** (stale "gone" docs): **confirmed and fixed** in f84e0a2 (helper header) and dc41e2d (scheduler_idle, and the stale in-process notes in x11 and x16).
- **F5** (sibling helpers keep the pid-0 single read): **confirmed by reading the code and carried.** The lines are listed under open issues.
- **F6** (no report.md): **handed to the coordinator.** The harness refuses subagent report files, and the coordinator commits each workstream report ("docs(v6): add the … workstream report"). This summary is the full report, with SHAs checked for reachability.
- **F7** (final Windows gate not quiet): **confirmed and re-run.**
  - Whole test/e2e at 8471f30 on Windows, no other job of mine running, QOMPACK_UNDER_COLOAD unset (as ci.yml's test-e2e runs it), host CPU 0-32% (load samples committed): 102 passed, 1 platform skip, 1 failure (the V3 timing row, re-run alone as in (d)).
  - The Linux run came afterwards, with no overlap: 301 passed, 0 failed, 3 platform skips, stderr empty.

## Files

- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/test/e2e/faultinject_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/test/e2e/shutdown_spawn_inflight_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/test/e2e/observer_e2e_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/test/e2e/v5_x04_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/test/guards/sharedreaders_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/plans/sdd/V6-closeout/w4-e2eflakes/runs/

### Commits

- f84e0a2 fix(e2e): never count a lock holder whose pid was never read as gone
- dc41e2d test(e2e): align the shutdown helper's callers with its gone
- c488456 fix(e2e): poll the session-end marker without blocking its write
- 8471f30 test(e2e): check x04's negative control once its daemon has stopped
- 8cf8137 docs(closeout): record the w4-e2eflakes fix seat's gate runs

### Tests

- `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified$' -count=2 -v (new row against the pre-fix helper)` — FAIL 2/2 'returned too early' (runs/fix-a-unidentified-red-oldhelper-windows.log)
- `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified$' -count=2 -v (mutation: settled() without the unidentified term)` — FAIL 2/2 (runs/fix-a-mutation-ignore-unidentified-windows.log)
- `go test ./test/e2e, count 3, the three staged shutdown rows as one alternation pattern: TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified, TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit, TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight` — PASS 9/9 (runs/fix-a-shutdown-rows-green-count3-windows.log)
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v (new rows, marker helper still os.ReadFile)` — FAIL on the obsSessionEndMarker row only (runs/fix-b-guard-marker-red-windows.log)
- `go test ./test/guards, TestGuard_HotFilesAreReadWithDeleteSharing and TestGuard_SharedReaderScannerSeesAForbiddenCall as one alternation pattern (after c488456's fix)` — PASS (runs/fix-b-guard-marker-green-windows.log)
- `throwaway diagnostic runs/diag-fix-b-marker_test.go.txt (not in tree): tight-loop reader against 400 contract.WriteMarker calls, before and after the fix` — os.ReadFile reader: 245/400 then 152/400 replaces failed; obsSessionEndMarker on ReadFileShared: 0/400
- `go test ./test/e2e, count 3, as one alternation pattern: TestE2E_ObserverThroughDaemon, TestV5_HookEventToTombstoneToRetrievalAfterRestart, TestV3_DegradedPassiveStillRecordsEverything` — PASS 9/9 (runs/fix-b-marker-users-count3-windows.log)
- `go test ./test/e2e -run '^TestV5_PreCompactToRehydrateToDroppedRoundTrip$/^negative_control_reinjection_disabled$' -count=2 -v, OLD test, diagnostic late-record daemon patch applied` — PASS 2/2: the arm passes without testing anything (runs/fix-c-x04-negctl-vacuous-oldtest-windows.log)
- `go test ./test/e2e -run '^TestV5_PreCompactToRehydrateToDroppedRoundTrip$/^negative_control_reinjection_disabled$' -count=2 -v, NEW test, same diagnostic patch` — FAIL 2/2 at the post-Stop NoFileExists (runs/fix-c-x04-negctl-red-newtest-windows.log)
- `go test ./test/e2e -run '^TestV5_PreCompactToRehydrateToDroppedRoundTrip$' -count=3 -v (shipped daemon)` — PASS 3/3, all three arms (runs/fix-c-x04-green-count3-windows.log)
- `go test ./test/e2e -count=1 -v -timeout=120m (Windows, 8471f30, QOMPACK_UNDER_COLOAD unset, no other job of this seat running, host CPU 0-32%)` — FAIL: 102 pass, 1 skip (child-race mode not requested), 1 fail = TestV3_HotPathUnchangedWithLedgerResident, wall-clock B-A/B-B only (runs/pkg-test-e2e-windows-8471f30-strict.log, -load-samples.txt)
- `go test ./test/e2e -run '^TestV3_HotPathUnchangedWithLedgerResident$' -count=1 -v, run alone, QOMPACK_UNDER_COLOAD unset` — FAIL: B-A p50 18.432 ms against 15 ms; B-B uncertifiable because 593 of 2130 deliveries were deferred by the §12.2 breach transition at sample 1536; B-E and B-E_cpu PASS (runs/d-x11-strict-alone-8471f30-windows.log)
- `go test ./test/e2e -run '^TestV3_HotPathUnchangedWithLedgerResident$' -count=1 -v, run alone, QOMPACK_UNDER_COLOAD=1` — PASS, every non-timing assertion and B-E_cpu included (runs/d-x11-coload-alone-8471f30-windows.log)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w4-e2eflakes --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w4-e2eflakes/plans/sdd/V6-closeout/w4-e2eflakes/runs/linux 8471f30 e2e-whole-fix --coload --timeout 150m -- ./test/e2e` — PASS: 301 passed, 0 failed, 3 platform skips; uid 10001, -race, stderr empty, source unchanged; run after the Windows gate with no overlap (runs/linux/cx-w4-e2eflakes-e2e-whole-fix-8471f30-20260926T201228Z-artifacts)
- `go test ./test/guards -count=1 -timeout=60m (Windows, 8471f30)` — FAIL only TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2): pre-existing, the same set as before, stays red until the V6 report (runs/guards-pkg-8471f30-windows.log)
- `go run ./tools/devtool fmt-check; go vet ./test/e2e/ ./test/guards/ (windows and GOOS=linux); pinned golangci-lint run ./test/e2e/... ./test/guards/...; go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,sleepcheck,runpatterns` — all exit 0 except runpatterns, which fails only on plans/sdd/V6-closeout/w3-e2ereds/report.md:107 and :119 (pre-existing, file unchanged on this branch) (runs/lint-fix-seat-8471f30-windows.log)

### Criterion changes

- f84e0a2 (stricter): e2eShutdownIfReachable checks the lock before each admin.shutdown as well as after it. It never counts as settled a lock holder whose pid no check could read; it waits out e2eDaemonDownBound and logs instead. The one definition of 'gone' is: no live lock holder, every pid seen holding the lock has exited (signaled on Windows), and no holder went unidentified.
- 8471f30 (stronger): the x04 negative control also asserts state/rehydrate-<session>.json absent after the rig's daemon is stopped. Stop joins the compact reply work within its drain grace. The immediate absence and zero-drop checks are kept.
- c488456 (stronger): test/guards' sharedReaders inventory now covers two test/e2e helpers that poll files the daemon replaces (obsSessionEndMarker, scAwaitState), and its doc says when a test helper earns a row.
- Implementer's, carried for the record: 78b33a1 moves x13's spool/<client> from the equality to per-arm assertions (needs owner acknowledgement). 87ffe91 changes the spawn row's assertion from 'the helper returned after cmd.Wait came back' to 'e2eProcessAlive(child) is false at the helper's return, before the child is reaped'. That is the helper's own definition, and a goroutine's time after Wait lags the real exit by its own scheduling.

### Open issues

- Carried (F5): the sibling shutdown helpers still take one pid read, and treat pid 0 (an empty lock caught mid-create) as settled. Locations: test/fault/fault.go:948,1082; test/platform/platform.go:1131,1162; test/release/release.go:781,818; test/security/security.go:1221,1288; test/guards/v1_integration_test.go:942. They have no spawn-in-flight wait, so the window is narrower than it was in test/e2e. The fix is to port e2eLockHolders (checks before and after each Send, plus unidentified tracking) in the workstream that owns those packages, before C3.2.
- Product, routed to the contract owner: contract.readMarker (internal/contract/marker.go:58) reads run/marker.json with os.ReadFile. checkSessionStartFires calls it, and `qompack selftest` can reach it from another process (internal/cli/selftest.go:374). On Windows that read can fail the daemon's WriteMarker replace, which is never retried. Once it is fixed, add it to test/guards' sharedReaders.
- The runpatterns lint (C3.5) fails on plans/sdd/V6-closeout/w3-e2ereds/report.md:107 (an unbalanced '^(' after the checker splits the alternation) and :119 (the placeholder '^<name>$'). The coordinator committed that report at 29ad705, and this branch does not change it.
- TestV3_HotPathUnchangedWithLedgerResident fails strict on this Windows host even at 0-32% CPU: B-A p50 16-18 ms against a 15 ms limit, and B-B uncertifiable because of the §12.2 breach transition (593 of 2130 deliveries deferred in every recorded run). It is a timing gate only, judged at C5.1 and probably a C2.8 item.
- The unidentified-lock-holder path in e2eShutdownIfReachable costs the whole e2eDaemonDownBound (20 s) when it fires. In the Windows and Linux whole-package runs it fired only in its own staged row.
- Only the two e2e polls named in the review were checked for reader-blocks-writer: the marker and the rehydrate state. Other test/e2e os.ReadFile reads of files the daemon replaces were not audited; for example scheduler_idle_test.go:145 polls metrics/latency.json and retries on its own.
- test/guards TestCarriedDefects_WaveReportRequiresResolution stays red until the V6 report sets final dispositions (pre-existing, O4).

### Needs the owner

- Acknowledge the x13 criterion change (78b33a1). spool/<client>, the hook's wall-clock fallback spool, leaves the arm-to-arm write-set equality and is asserted per arm by x13v4RequireFallbackIsTimingOnly. That assertion fails if the daemon refused any hot-path request it received. The rationale is on x13v4HookFallbackToken in test/e2e/v4_x13_test.go.
- C5.1/C2.8: B-A is over its 15 ms budget on this Windows host even with low host load (p50 16.4-18.4 ms). Every run then trips the §12.2 spool transition after three 512-sample windows. This needs a quiet C5.1 measurement and then either a real speed-up or a re-budget ruling. Nothing was changed here.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



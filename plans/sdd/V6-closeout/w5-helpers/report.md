# V6 close-out w5-helpers: one definition of a daemon's gone for every shutdown helper

Branch `closeout/w5-helpers`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `2be9b85`

### Root cause

(a) The sibling shutdown helpers (fault, platform, release, security, guards) read daemon.lock once and treated pid 0, an empty lock caught mid-CreateNew, as a holder that had already exited, so the lock's disappearance ended their wait while the daemon was still unwinding. A staged diagnostic failed all five old helpers and passes the ported ones. (b) atomicFaultStore's row is unleased (no nonce), and the wrapper forwarded neither ObservationRecovery nor PublicationSync, so it could not carry the leased path its comment claimed. (c) obs.Registry creates instruments lazily, so x13's spelled counter and the B-A histogram read as 0 when renamed, silently undercounting Received and weakening the refusal check (demonstrated: 0 of 9 received, row green).

### Summary

W5-HELPERS REPORT. Branch closeout/w5-helpers, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-helpers, base 90e1db3. The code is final at d1431ae; HEAD is 2be9b85, which adds evidence only. The tree is clean. Every SHA quoted here was checked with merge-base --is-ancestor.

## What I did with the earlier seat's work
- **919b0fb** (internal/testutil/daemongone.go and its tests): reviewed and kept. The logic matches test/e2e's e2eLockHolders line for line.
- **The six e2e edits** in the working tree: reviewed and committed as e01a441.
- **The sibling rewrites**: the earlier seat had parked these in the shared refs/stash as `w5-siblings`. I applied that stash, reviewed it, committed it as bb3317e and 6c02433, then dropped that one stash entry. No other stash was touched.
- **The five untracked zz_diag_w5_shutdown_test.go diagnostics**: I ran them against the old and new helpers, kept the source as runs/diag-w5-sibling-shutdown_test.go.txt, and removed them from the tree. None was committed.
- **Pre-restart evidence logs**: deleted. Everything I rely on was re-run.

## (a) One definition of a daemon being "gone"

**Root cause.** The shutdown helpers in test/fault, test/platform, test/release and test/security were clones of test/e2e's helper. test/guards had its own variant. They were copied before test/e2e learned its current definition of gone (f6d9a71, f84e0a2).
- Each helper read daemon.lock once, as its handshake began.
- The fault, platform, release and security copies then treated pid 0 as a holder that had already exited (`if shutdownPID == 0 ... return true`). The guards copy took the pid from daemon.ReadLock, and testutil.ProcessAlive(0) reports not alive, so it behaved the same.
- paths.CreateNew creates the lock file first and writes its body second. A daemon still coming up can therefore be read as pid 0.
- From then on, the lock disappearing was enough to end the wait, while the daemon was still unwinding.

**Evidence.** A staged diagnostic: an empty lock, then a body naming a live stand-in, then the lock removed while the stand-in keeps running.
- Old helpers: FAIL in all 5 packages ("returned once the lock was released ... stand-in alive=true").
- New helpers: PASS 15/15.

**Changes.**
- **One shared helper.** testutil.ShutdownDaemonUntilGone and testutil.DaemonHoldingLock (919b0fb). The importgraph and testdeps lints allow this; testutil already imports internal/cli.
  - It checks the lock before and after each admin.shutdown.
  - It tracks every pid it sees holding the lock.
  - pid 0 is never counted as settled, and a holder whose pid was never read is recorded as unidentified.
  - Our own pid counts as exited once the lock is gone, as before.
- **dd1f121** adds a regression row that pins the check before each Send: TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown. The test serves the project's address itself; its handler removes the lock before replying, and the lock names a live stand-in.
- **Mutation results** for daemongone.go (runs/a-testutil-mutations-windows.log):
  - old sibling logic: fails the every-holder and unidentified rows;
  - settled() ignoring unidentified holders: fails the table and unidentified rows;
  - lock-only settled(): fails the table, every-holder and reads-before rows;
  - no check before the Send: fails only the new reads-before row;
  - pid 0 never marked as unread: fails the table and unidentified rows.
- **e01a441**: test/e2e calls the shared helper. The reachability gate and the spawn-in-flight wait stay in test/e2e, and behaviour is unchanged.
- **bb3317e and 6c02433**: all five sibling helpers call the shared helper. Each keeps its own gate, bounds and log line.
  - fault and security still terminate their own fixture's daemon at the bound, as before (LockPID only, unchanged).
  - guards still fails the test on a live holder, a still-running holder, or a lock left behind by a holder that exited. It logs an unidentified holder rather than failing.
- **Regression test for the spawn-in-flight case**: TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit. Because there is now one helper, this single test covers every caller.

## (b) internal/daemon/observer_atomic_publication_test.go
- **Doc corrected.** atomicFaultStore's comment and the existing row's comment now say what the row is: an unleased delivery (no nonce), the path ingest.leaseDelivery counts as unleased. The existing row itself is unchanged, so no coverage was removed.
- **Real coverage added (994a7cf).**
  - New wrapper leasedAtomicFaultStore. It forwards ObservationRecovery and PublicationSync and deliberately does NOT declare DurableObservationPublisher.
  - New row TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable. It sends a leased delivery (with a nonce) through the observer's non-declaring branch, and asserts:
    - on failure: NAK, WAL line byte-identical, sidecar unpublished, frontier not acknowledged, no reference, no binding;
    - after the drain: one record, the binding, the link, the ACK, and no second replay;
    - the store's own pass counter: 1 before the fault, 4 more for the repaired publication.
- **Mutations** (temporary edits to observer code, reverted; runs/b-leased-row-mutations-windows.log):
  - every store treated as declaring: fails only the new row (expected 1 pass, actual 0);
  - finishObservation's pass skipped: fails only the new row (expected 5, actual 4);
  - a refused write swallowed: fails all three rows.
- observer_durable_publication_test.go needed no edit. atomicFaultStore's own method set is unchanged, so its comments stay true.

## (c) test/e2e/v4_x13_test.go (d1431ae)
- **Why presence cannot simply be required.** obs.Registry creates an instrument the first time it is used. A name the daemon no longer records under therefore reads as 0, not as missing. And a healthy run never creates hotpath_sample_invalid at all.
- **The invariant used instead.** Every hot-path WAL line has exactly one sample behind it:
  - Accept is reached only from dispatchOp's hot-path routes (the drain sends a replayed client-spool line to runIngested, which appends nothing);
  - dispatchOp records one sample for every hot-path request that passes admission.
- **The new check.** Received < Durable now fails with errX13v4Unaccounted, whose message names both instruments. A sample can still be in flight, so the row re-reads the registry on obsProcessTick within obsProcessBound first. Both constants already existed; there are no new numbers.
- **Unit tests.** TestV4_X13ClassifyFallback gains an unaccounted row. New TestV4_X13ReceivedFailsLoudOnARenamedInstrument drives a real obs.Registry with renamed instruments.
- **End-to-end evidence** (temporary daemon edits, reverted; runs/c-x13-row-mutations-windows.log): with either the B-A histogram renamed or the invalid-sample counter renamed with every sample invalid:
  - old row: PASS, with "received 0 ... holds 9 hot-path WAL lines";
  - new row: FAIL loudly.
- Unmutated, the row passes 3/3 with 9 samples against 9 lines in both arms.

## Full-package gates (every row)
**Windows, strict** (QOMPACK_UNDER_COLOAD unset), co-loaded host. My Linux gate runs overlapped these runs.
- **Non-e2e packages:**
  - testutil, daemon, fault, platform, release, security: ok.
  - guards: FAIL only in TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2). This is pre-existing and stays red until the V6 report (O4).
  - Skips are platform skips only: TestService_StateWriteFailureStillEmits, and one security symlink subtest (needs SeCreateSymbolicLinkPrivilege).
- **test/e2e:** 104 PASS, 1 SKIP (child-race mode not requested), 2 FAIL.
  - TestV3_LiveSessionWriteSetAndAppendOnly: re-run alone, PASS 3/3, so load-sensitive.
  - TestV3_HotPathUnchangedWithLedgerResident: re-run alone, strict, FAIL only at v3_x11_test.go:487 (B-A and B-B wall-clock; 527 of 2064 samples deferred). This is the known timing gate owned by C5.1/C2.8.
  - The unidentified-holder log appeared only in its own staged row.

**Linux** (linux-nonroot-gate.sh, uid 10001, -race, --coload, GOMAXPROCS 4). No race logs, stderr empty, source unchanged.
- **Non-e2e packages:**
  - testutil 53, daemon 1501 (1 Windows-only skip), fault 56, platform 14, release 8, security 30: all PASS.
  - guards: only the same carried-defects row fails.
- **test/e2e** (timeout 150m): 305 pass, 3 fail, 3 permitted skips.
  - TestE2E_ThinSliceDropsControlOnlyEdges: PASS 3/3 alone, so load-sensitive.
  - TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly: FAIL 3/3 alone at d1431ae **and** 3/3 alone at base 90e1db3, with identical messages. They are pre-existing and not caused by this branch.

## Checks at d1431ae
All exit 0: fmt-check; go vet on every touched package (Windows and GOOS=linux); `devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`. stubskips was not run. No docs or generated inputs changed, so test/docs and gen-docs do not apply.

## Scope
Only internal/testutil, test/* and internal/daemon/observer_atomic_publication_test.go changed. No product code was changed; every product edit was temporary and reverted with git checkout.

### Commits

- 919b0fb fix(testutil): define a daemon's gone once for every shutdown helper (earlier seat; reviewed and kept)
- dd1f121 test(testutil): pin the lock check made before each admin.shutdown
- e01a441 refactor(e2e): take the shutdown helper's gone from testutil
- bb3317e fix(fault,platform,release,security): wait for every lock holder
- 6c02433 fix(guards): wait for every holder of the v1 daemon's lock
- 994a7cf test(daemon): cover a leased capture through the atomic fault store
- d1431ae fix(e2e): fail x13 when its samples do not account for the WAL
- 2be9b85 docs(closeout): record the w5-helpers evidence and gate runs

### Tests

- `go test ./test/fault ./test/platform ./test/release ./test/security ./test/guards -run '^TestZZDiagW5_SiblingShutdownWaitsForEveryLockHolder$' -count=1 -v (TEMPORARY DIAGNOSTIC, pre-port sibling helpers) <!-- runpatterns: names a temporary diagnostic test kept as evidence in runs/diag-w5-sibling-shutdown_test.go.txt and removed after the run, not a test in this tree -->` — FAIL in all 5 packages: returned once the lock was released while the stand-in holder was alive (runs/a-sibling-diag-red-oldhelpers-windows.log)
- `go test ./test/fault ./test/platform ./test/release ./test/security ./test/guards -run '^TestZZDiagW5_SiblingShutdownWaitsForEveryLockHolder$' -count=3 -v (TEMPORARY DIAGNOSTIC, ported helpers, byte-identical to bb3317e/6c02433) <!-- runpatterns: names a temporary diagnostic test kept as evidence in runs/diag-w5-sibling-shutdown_test.go.txt and removed after the run, not a test in this tree -->` — PASS 15/15 (runs/a-sibling-diag-green-newhelpers-windows.log)
- `go test ./internal/testutil, as one alternation pattern: TestLockHolders_AnEmptyLockIsNeverSettled, TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit, TestShutdownDaemonUntilGone_WaitsOutALockHolderItNeverIdentified, TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown, TestShutdownDaemonUntilGone_AnAbandonedLockIsGoneAtOnce; under five temporary mutations of daemongone.go (runs/mutate.py.txt)` — Each mutation fails at least one row. OLD: every-holder and unidentified rows. M1: table and unidentified. M2: table, every-holder and reads-before. M3: reads-before only. M4: table and unidentified. (runs/a-testutil-mutations-windows.log)
- `go test ./internal/testutil -run '^TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown$' -count=3 -v` — PASS 3/3
- `go test ./internal/daemon, as one alternation pattern: TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable, TestObserverAtomicPublicationFailureRemainsDrainRetryable, TestObserverDurablePublisherFailureRemainsDrainRetryable; under three temporary observer mutations (runs/mutate_b.py.txt)` — B1 (every store treated as declaring): only the leased row fails (1 pass expected, 0 actual). B2 (finishObservation pass skipped): only the leased row fails (5 expected, 4 actual). B3 (refused write swallowed): all three fail. (runs/b-leased-row-mutations-windows.log)
- `go test ./internal/daemon, as one alternation pattern: TestStoreWrapperDropsTheSupersedingCapability, TestObserverAtomicPublicationFailureRemainsDrainRetryable, TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable, TestStoreWrapperDropsTheDurablePublisherCapability, TestObserverDurablePublisherFailureRemainsDrainRetryable` — PASS 5/5
- `go test ./test/e2e, as one alternation pattern: TestV4_X13ClassifyFallback, TestV4_X13ReceivedFailsLoudOnARenamedInstrument` — PASS, including the new unaccounted subtest and all three renamed-instrument subtests
- `go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=3 -v` — PASS 3/3; both arms received 9 samples and held 9 hot-path WAL lines (runs/c-x13-row-green-windows.log)
- `go test ./test/e2e -run '^TestV4_HotPathUnchangedWithTheFullWave3ResidentSet$' -count=1 -v under temporary daemon mutations C1 and C2 (runs/mutate_c.py.txt), each with the HEAD~ and the new test code` — Old code PASS under both mutations (received 0, holds 9). New code FAIL under both, loudly, with errX13v4Unaccounted naming the instruments. (runs/c-x13-row-mutations-windows.log)
- `go test ./internal/testutil ./internal/daemon ./test/fault ./test/platform ./test/release ./test/security ./test/guards -count=1 -v -timeout=30m -p 3 (Windows, d1431ae, QOMPACK_UNDER_COLOAD unset)` — testutil, daemon, fault, platform, release and security: ok. guards: FAIL only in TestCarriedDefects_WaveReportRequiresResolution (pre-existing, O4). Skips are platform skips only. (runs/pkg-non-e2e-windows-d1431ae.log)
- `go test ./test/e2e -count=1 -v -timeout=120m (Windows, d1431ae, QOMPACK_UNDER_COLOAD unset, co-loaded)` — FAIL: 104 pass, 1 platform skip, 2 fail (TestV3_LiveSessionWriteSetAndAppendOnly; TestV3_HotPathUnchangedWithLedgerResident, timing only) (runs/pkg-e2e-windows-d1431ae.log)
- `go test ./test/e2e -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' -count=3 -v (Windows, alone)` — PASS 3/3, so the whole-package red was load-sensitive (runs/e2e-x09-alone-windows-d1431ae.log)
- `go test ./test/e2e -run '^TestV3_HotPathUnchangedWithLedgerResident$' -count=1 -v (Windows, alone, strict)` — FAIL only at v3_x11_test.go:487, the harness exit: B-A p99 245.8 ms against 15 ms, B-B uncertifiable with 527 of 2064 samples deferred; B-E and B-E_cpu PASS. Known C5.1 timing gate. (runs/e2e-x11-alone-strict-windows-d1431ae.log)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-helpers --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-helpers --out C:/.../w5-helpers/runs/linux d1431ae e2e-whole --coload --timeout 150m -- ./test/e2e` — FAIL: 305 pass, 3 fail (ThinSlice, SpooledSessionStart, x09), 3 permitted skips; no race logs; stderr empty; source unchanged
- `linux-nonroot-gate.sh ... d1431ae e2e-reds-alone --count 3 --coload, with --run set to the alternation of TestE2E_ThinSliceDropsControlOnlyEdges, TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly -- ./test/e2e` — ThinSlice PASS 3/3 (load-sensitive). SpooledSessionStart FAIL 3/3 and x09 FAIL 3/3.
- `linux-nonroot-gate.sh ... 90e1db3 e2e-reds-alone-base --count 3 --coload, with --run set to the alternation of TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly -- ./test/e2e` — FAIL 3/3 for both at the base, with identical messages: pre-existing, not from this branch
- `linux-nonroot-gate.sh ... d1431ae non-e2e-pkgs --coload --timeout 45m -- ./internal/testutil ./internal/daemon ./test/fault ./test/platform ./test/release ./test/security ./test/guards` — testutil 53, daemon 1501 (1 Windows-only skip), fault 56, platform 14, release 8, security 30: all PASS. guards: only TestCarriedDefects_WaveReportRequiresResolution fails (pre-existing). No race logs.
- `go run ./tools/devtool fmt-check; go vet (windows and GOOS=linux) on internal/testutil, internal/daemon, test/e2e, test/fault, test/platform, test/release, test/security, test/guards; go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — All exit 0 at d1431ae

### Criterion changes

- d1431ae (stronger), test/e2e/v4_x13_test.go: x13v4ClassifyFallback now also fails when Received < Durable (errX13v4Unaccounted). The row re-reads the registry on obsProcessTick within obsProcessBound before failing. Rationale: every hot-path WAL line has exactly one dispatchOp sample behind it, so a shortfall means a sample was recorded under a name the row does not read, which silently weakened the refusal check (a daemon rename made the old row pass with 0 of 9 received). The existing Received > Durable refusal check is unchanged.
- bb3317e, 6c02433 (stricter): the fault, platform, release, security and guards shutdown helpers now use testutil's definition of gone. Their old definition counted pid 0 as exited and read the lock only once. A holder whose pid no check read now makes the helper wait out its whole bound. The guards helper logs rather than fails that case, since nothing is known to be alive; its existing failures (live holder, running holder, lock left by an exited holder) are kept, and the last now fires at once instead of after the bound.
- 994a7cf: no existing assertion changed. The atomic row's doc comment was corrected from 'the write path production actually takes' to the unleased path, and a new leased row was added.
- dd1f121, e01a441: a new test only, and a move with no behaviour change.

### Open issues

- Linux TestE2E_SpooledSessionStartNeverDegradesTheProject fails 3/3 alone at both d1431ae and 90e1db3: spooled_sessionstart_test.go:108 expected 1, actual 0 ('the replayed start still did the start's bookkeeping'). Pre-existing, not investigated here (outside this task); route to the w2-hookout / SessionStart owner.
- Linux TestV3_LiveSessionWriteSetAndAppendOnly fails 3/3 alone at both d1431ae and 90e1db3 ('flush never produced the observer: gc log line'). This contradicts the ledger's note that it passed 3/3 alone at 90e1db3. The container was co-loaded, and another workstream (cx-w5-dirsync) was running x09 at 90e1db3 at the same time. On Windows it failed once in the whole-package run and passed 3/3 alone.
- Linux TestE2E_ThinSliceDropsControlOnlyEdges failed in the whole run at observer_e2e_test.go:563 ('real mixed traffic must produce at least one EdgeControlOnly') and passed 3/3 alone. It is load-sensitive, and the assertion differs from the one w4 fixed in a893e5d.
- Windows TestV3_HotPathUnchangedWithLedgerResident fails strict, at its wall-clock gate only (known; C5.1/C2.8).
- test/guards TestCarriedDefects_WaveReportRequiresResolution stays red until the V6 report (pre-existing, O4).
- Outside scope and unchanged: tools/devtool/liveeval_host.go stops its daemon on lock absence alone. test/bench/hotpath's shutdownDaemon is fire-and-forget, with a process-kill fallback (a different design). test/release's stopDaemonAndWait still waits for the lock file itself after shutdownIfReachable, on purpose, so the next hook starts a fresh daemon.
- The unidentified-holder path costs a helper's whole bound when it fires: 20 s in e2e, StopCleanupBound+15s in fault/platform/security, 90 s in release, v1ShutdownPollBound in guards. In every full run it fired only in its own staged e2e row.
- Every gate ran on a co-loaded host. The Windows runs are strict but overlapped this seat's Linux gate runs, and the Linux runs declared --coload.

### Needs the owner

- New test-pacing constants in internal/testutil/daemongone_test.go (each is named, with a derivation comment):
- stagedShutdownTick 25 ms: paces the staged helper calls. If it is wrong, only the tests' speed changes.
- stagedShutdownRoundTrip 5 s: each attempt's connect/ACK deadline. Nothing listens in the staged rows, so connects fail at once; in the reads-before row the server answers promptly. If it is too short under load, a Send spools, which is harmless.
- stagedUnidentifiedBound 3 s: the unidentified row waits all of it by design. If it is too short on a starved host, the helper can run out before the test sees its removal, which gives a false red, never a false green.
- lockHolderExitBound 30 s: the reap fallback that kills the test's own stand-in child. It affects cleanup only.
stagedShutdownBound is daemon.StopCleanupBound, derived rather than new.
- Expected counts in internal/daemon/observer_atomic_publication_test.go: leasedObserverPrePasses = 1 and leasedPublicationPasses = 1 + 2 + 1 = 4. They are derived from the observer's non-declaring branch and FSStore's two barriers, not budgets. If the store's barrier count or the observer's pass structure changes, the row fails, which is intended.
- Acknowledge the x13 criterion change (d1431ae): Received < Durable now fails the row, after a re-read within obsProcessBound.
- Assign an owner for the two Linux e2e reds that reproduce at base 90e1db3: TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly.

## Independent review

### review:helpers: needs-fixes

- **minor** `internal/testutil/daemongone.go:168 (DaemonHoldingLock); test/guards/sharedreaders_test.go:44 (sharedReaders)` — DaemonHoldingLock is now the one daemon.lock poller behind every shutdown helper under test/ (e2e, fault, platform, release, security, guards), plus the waitDaemonUp and requireNoOrphan diagnostics. No guard pins its paths.ReadFileShared read. Its doc comment gives the exact failure a regression would bring back: an ordinary os.ReadFile handle made Lock.Release's os.Remove fail with ERROR_SHARING_VIOLATION about one run in twenty. When the reader was copied six times no single row could protect it. Now there is one definition, so one row would.
  - Evidence: sharedReaders at HEAD has rows for internal/daemon/lock.go readLockFile and for the e2e helpers obsSessionEndMarker and scAwaitState, but none for internal/testutil/daemongone.go DaemonHoldingLock. The inventory's own rule ("A test helper earns a row when its read can break the product write it is waiting for") fits this function exactly. Reverting it to os.ReadFile would leave every test and lint green.
  - Fix: Add a row {file: "internal/testutil/daemongone.go", fn: "DaemonHoldingLock", holds: "run/daemon.lock", why: "Lock.Release's os.Remove, the last act of a daemon shutdown, which every test/ shutdown helper polls for"}. Then confirm that swapping in os.ReadFile fails TestGuard_HotFilesAreReadWithDeleteSharing.
- **nit** `commits dd1f121, 994a7cf` — Two of the eight commits have no Refs footer. The other six carry "Refs: V6-VERIFY, C3.2".
  - Evidence: `git log --format=%b 90e1db3..HEAD`: dd1f121 (test(testutil): pin the lock check made before each admin.shutdown) and 994a7cf (test(daemon): cover a leased capture through the atomic fault store) both end with no Refs line. No attribution trailers appear anywhere, which is correct.
  - Fix: The branch is unpushed. Before integration, amend the two messages to add "Refs: V6-VERIFY, C3.2", for example with a scripted `git rebase --exec` or filter-branch msg-filter that touches only the message.
- **nit** `test/fault/fault.go:945-946; test/security/security.go:1218-1219` — When the bound expires, the fault and security helpers still pass only out.LockPID to terminateOwnDaemon. Take the case where the lock is released but a recorded holder is still running (out.Running is non-empty, LockPID is 0 or dead): terminateOwnDaemon returns at its pid<=0/!alive guard, so the straggler goes unterminated, and RemoveAll then races it. This is not a regression, because the old code passed the last-read lockPID, which is the same thing. But the shared outcome now carries the pids that would allow the last-resort kill this helper's doc promises ("a daemon that never goes is terminated").
  - Evidence: `t.Logf("fault: %s", out.Describe(...)); terminateOwnDaemon(t, root, out.LockPID)`. terminateOwnDaemon begins `if pid <= 0 || pid == os.Getpid() || !testutil.ProcessAlive(pid) { return }`.
  - Fix: Optional: call terminateOwnDaemon for out.LockPID and for each pid in out.Running (terminateOwnDaemon already skips our own pid, dead pids and roots that are not this package's fixtures). Otherwise, narrow the doc to say only a daemon still holding the lock is terminated.

## Fix seat (review resolution) — status `done`, head `9695dd9461365ae113f63f407974f9a5e3ab7221`

### Root cause

Review finding: DaemonHoldingLock became the only daemon.lock reader behind every test/ shutdown helper, and its delete-sharing read had no sharedReaders row. Swapping the read for os.ReadFile left TestGuard_HotFilesAreReadWithDeleteSharing green. test/e2e's e2eSpawnInFlight (reads run/spawn.lock) had the same gap before this branch. Implementer's items: five sibling shutdown helpers took one lock read as the handshake began and treated pid 0 as exited, so they could hand a directory to RemoveAll while the daemon was still unwinding. atomicFaultStore's comment claimed to cover the leased path, which it did not. x13's Received read a renamed instrument as zero and passed silently.

### Summary

W5-HELPERS: FIX-SEAT REPORT (branch closeout/w5-helpers, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-helpers, base closeout/integration 90e1db3, final HEAD 9695dd9)

The review raised one finding, rated minor. It is correct and now fixed. While checking it I found a second reader with the same gap and gave it a guard row too. No product code changed. No check was weakened.

== Review resolution ==
Finding (minor): DaemonHoldingLock (internal/testutil/daemongone.go) is now the only daemon.lock reader behind every shutdown helper under test/, but no sharedReaders row in test/guards pins its paths.ReadFileShared read.
- Checked independently, and the reviewer is right. Neither base 90e1db3 nor HEAD 2be9b85 had a sharedReaders row for it, and base had no row for its e2e predecessor e2eDaemonHoldingLock either.
- Showing the gap: I swapped the read for os.ReadFile(daemon.LockPath(root)). The package still compiled and passed go vet, and `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v` still printed ok (exit 0). So the regression the function's own doc comment describes (Lock.Release's os.Remove failing with ERROR_SHARING_VIOLATION about one run in twenty) could come back without any test noticing.
- Fix (90ab6ad): added the row {file internal/testutil/daemongone.go, fn DaemonHoldingLock, holds run/daemon.lock, why: Lock.Release's os.Remove, polled by every test/ shutdown helper through this one reader}. I also widened the sharedReader doc comment to cover test helpers in internal/testutil.
- Proof: with the mutation in place the guard fails on exactly the new subtest, with "Should be empty, but was [os.ReadFile]" and the row's message (exit 1). After `git checkout -- internal/testutil/daemongone.go` it passes, and TestGuard_SharedReaderScannerSeesAForbiddenCall still passes.
- Same gap in a second reader (248b481): test/e2e's e2eSpawnInFlight (faultinject_test.go) polls run/spawn.lock through paths.ReadFileShared during e2eShutdownIfReachable's wait for a spawn in flight. Its deleter is the one internal/ipc's spawnLockIsStale row already names: daemon.removeSpawnLockFile, a single os.Remove with no retry. The product's own comment describes the harm: a leftover marker "suppresses every later lazy spawn until it ages out of spawnLockStaleAfter". I ran the same three steps: with the read mutated to os.ReadFile and no row, the guard stays green; with the row it goes red on that subtest; with the mutation reverted it is green. This goes beyond what the finding asked for, so it is a separate commit the coordinator can drop.
- Evidence: plans/sdd/V6-closeout/w5-helpers/runs/review-guard-row-mutations-windows.log records every step with its diff, command and exit code.

== Commands and results (fix seat) ==
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v`: PASS with each mutation and no row (the gap), FAIL with each row plus mutation (exit 1), PASS with the mutations reverted.
- `go test ./test/guards -run '^TestGuard_SharedReaderScannerSeesAForbiddenCall$' -count=1`: PASS.
- `go run ./tools/devtool fmt-check`: exit 0. `go vet ./test/guards`: exit 0 on Windows and with GOOS=linux.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every sub-check passed except golangci-lint, which refused with "parallel golangci-lint is running" (another workstream held its lock). Re-run alone as `go run ./tools/devtool lint --only=golangci-lint`: PASS.
- Full test/guards package on Windows at 248b481 (`go test ./test/guards -count=1 -v -timeout=30m`, co-loaded host, QOMPACK_UNDER_COLOAD unset; log runs/pkg-guards-windows-248b481.log): FAIL, only TestCarriedDefects_WaveReportRequiresResolution (subtests SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3). Everything else passed, including both new rows.
- Full test/guards package on Linux at 248b481: `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-helpers --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-helpers --out <scratch>/linux 248b481 guards-review-248b481 --timeout 30m -- ./test/guards`. Run non-root (uid 10001) with -race and without --coload. Result: pass=167 fail=7, and the 7 are the same TestCarriedDefects_WaveReportRequiresResolution rows. The two new rows passed. No race log, stderr empty, source tree unchanged afterwards. Artifacts: runs/linux/cx-w5-helpers-guards-review-248b481-248b481-20260926T235812Z-artifacts.
- The TestCarriedDefects_WaveReportRequiresResolution red is ledger item C6.3, not this workstream. Its inputs (plans/CARRIED-DEFECTS.tsv, plans/V6-report.md, test/guards/carrieddefects_test.go) are byte-identical between 90e1db3 and HEAD. It fails because V6-report.md exists while six defects are still marked deferred:V6-VERIFY. It also appears in the implementer's runs at d1431ae.

== Implementer's work (commits 919b0fb..2be9b85, reconstructed from the commit messages and the committed logs) ==
(a) One definition of "gone". internal/testutil/daemongone.go adds ShutdownDaemonUntilGone and DaemonHoldingLock. They check the lock holders before and after each admin.shutdown, keep the set of every pid seen, track holders that were never identified, and never count pid 0 (an empty lock caught mid-create) as settled. test/e2e, test/fault, test/platform, test/release, test/security and test/guards' v1StopDaemonAndWaitGone now all call them.
- Root cause: the five sibling helpers took one daemon.lock read as the handshake began and treated pid 0 as a holder that had already exited, so the lock disappearing was enough to end the wait while the daemon was still unwinding.
- Staged regression tests in internal/testutil: TestLockHolders_AnEmptyLockIsNeverSettled, TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit (the spawn-in-flight case: empty lock, then a body naming a live stand-in, then the lock removed), TestShutdownDaemonUntilGone_WaitsOutALockHolderItNeverIdentified, TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown and TestShutdownDaemonUntilGone_AnAbandonedLockIsGoneAtOnce.
- A diagnostic failed all four old sibling helpers and passes the new ones (runs/a-sibling-diag-*.log). Mutations of the holder bookkeeping are in runs/a-testutil-mutations-windows.log.
- The v1 guard is stricter than before, not looser: where the old helper returned at once, it now waits out the whole bound for an unidentified holder and logs it.
(b) internal/daemon/observer_atomic_publication_test.go: atomicFaultStore's comment is corrected to say it covers the unleased path. A new leasedAtomicFaultStore forwards ObservationRecovery and PublicationSync, and a new row, TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable, drives a leased delivery through the observer's non-declaring branch (NAK, WAL line kept, frontier left uncommitted, then the drain publishes once and commits). The store's pass counts are pinned. Mutations: runs/b-leased-row-mutations-windows.log.
(c) test/e2e/v4_x13_test.go: a renamed instrument used to read as zero, and the row passed silently. Now Received < Durable fails the row (errX13v4Unaccounted), after re-reading within obsProcessBound. New row: TestV4_X13ReceivedFailsLoudOnARenamedInstrument. Mutations: runs/c-x13-row-mutations-windows.log. This is a criterion change: every hot-path WAL line has exactly one sample behind it, so a quiescent daemon has Received == Durable.
- Implementer's package runs at d1431ae. Windows: internal/testutil, internal/daemon, test/fault, test/platform, test/release and test/security all ok. test/guards failed only on C6.3. test/e2e whole package failed TestV3_LiveSessionWriteSetAndAppendOnly, which passed 3/3 when re-run alone, and TestV3_HotPathUnchangedWithLedgerResident, the x11 B-A/B-B timing gate on this co-loaded host, which still failed alone (B-A p99 245 ms against 15 ms, with 527 samples deferred). Linux (non-root, -race, run with --coload because the host is shared; coload never relaxes what the product did): the non-e2e packages are all PASS except test/guards on C6.3. test/e2e whole package (150m timeout): 305 pass, 3 fail. Of the 3, TestE2E_ThinSliceDropsControlOnlyEdges passed 3/3 alone. TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly failed 3/3 alone at d1431ae and also 3/3 at base 90e1db3, so they are pre-existing on Linux and not caused by this branch.

== Criterion changes ==
- Fix seat: two sharedReaders rows added (DaemonHoldingLock, e2eSpawnInFlight). Both strengthen the guard. Nothing was loosened.
- Carried from the implementer: x13 now fails when Received < Durable (d1431ae). This needs coordinator or owner acknowledgement.

== Open items ==
- C6.3: TestCarriedDefects_WaveReportRequiresResolution is red on both OSes until CARRIED-DEFECTS.tsv gets its final dispositions (coordinator).
- Linux e2e: TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly fail at base 90e1db3 as well as at HEAD, and nobody owns them yet.
- Windows x11 hot-path timing (TestV3_HotPathUnchangedWithLedgerResident) is red on this co-loaded host. It is the known hot-path budget class (owner Q1 / B-A), was not introduced here, and has to be judged on a quiet host.
- The fix seat did not re-run test/e2e or the other touched packages after 248b481, because the only non-docs change since d1431ae is test/guards/sharedreaders_test.go.

### Commits

- 919b0fb fix(testutil): define a daemon's gone once for every shutdown helper (implementer)
- dd1f121 test(testutil): pin the lock check made before each admin.shutdown (implementer)
- e01a441 refactor(e2e): take the shutdown helper's gone from testutil (implementer)
- bb3317e fix(fault,platform,release,security): wait for every lock holder (implementer)
- 6c02433 fix(guards): wait for every holder of the v1 daemon's lock (implementer)
- 994a7cf test(daemon): cover a leased capture through the atomic fault store (implementer)
- d1431ae fix(e2e): fail x13 when its samples do not account for the WAL (implementer)
- 2be9b85 docs(closeout): record the w5-helpers evidence and gate runs (implementer)
- 90ab6ad test(guards): pin the lock read every shutdown helper polls (fix seat: the review finding)
- 248b481 test(guards): pin the spawn marker read in e2e's shutdown wait (fix seat: the same gap in a second reader, found while checking the finding)
- 9695dd9 docs(closeout): record the w5-helpers review-fix guard runs (fix seat: evidence)

### Tests

- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v  (DaemonHoldingLock mutated to os.ReadFile, no row, HEAD 2be9b85)` — PASS (exit 0): demonstrates the gap the review named
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v  (row added, mutation in place)` — FAIL (exit 1) on subtest internal/testutil/daemongone.go:DaemonHoldingLock only
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v  (e2eSpawnInFlight mutated to os.ReadFile: without row, then with row)` — PASS without the row (gap); FAIL (exit 1) with the row on subtest test/e2e/faultinject_test.go:e2eSpawnInFlight
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1  (mutations reverted, both rows)` — PASS
- `go test ./test/guards -run '^TestGuard_SharedReaderScannerSeesAForbiddenCall$' -count=1` — PASS
- `go run ./tools/devtool fmt-check; go vet ./test/guards; GOOS=linux go vet ./test/guards` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — nomagic/importgraph/testdeps/bindeps/sleepcheck/runpatterns/docmarkers PASS; golangci-lint refused ('parallel golangci-lint is running', another workstream); re-run alone `go run ./tools/devtool lint --only=golangci-lint` PASS
- `go test ./test/guards -count=1 -v -timeout=30m  (Windows, 248b481, co-loaded, QOMPACK_UNDER_COLOAD unset)` — FAIL only TestCarriedDefects_WaveReportRequiresResolution (6 subtests, ledger C6.3, inputs untouched since base); all else PASS incl. both new rows
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-helpers --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-helpers --out <scratch>/linux 248b481 guards-review-248b481 --timeout 30m -- ./test/guards` — non-root -race, no --coload: pass=167 fail=7, the 7 being TestCarriedDefects_WaveReportRequiresResolution (C6.3); both new rows pass; no race log; source tree unchanged
- `go test ./test/guards -run '^TestCarriedDefects_WaveReportRequiresResolution$' -count=1` — FAIL (pre-existing ledger item C6.3: V6-report.md exists while SP06-D2/SP08-D1/SP10-D1/SP09-D1/SP20-D2/SP08-D3 are deferred:V6-VERIFY)
- `implementer (d1431ae): Windows go test ./internal/testutil ./internal/daemon ./test/fault ./test/platform ./test/release ./test/security ./test/guards -count=1 -v -timeout=30m -p 3` — all ok except test/guards (C6.3 only)
- `implementer (d1431ae): Windows go test ./test/e2e -count=1 -v -timeout=120m` — FAIL TestV3_LiveSessionWriteSetAndAppendOnly (3/3 PASS alone) and TestV3_HotPathUnchangedWithLedgerResident (x11 timing gate; still red alone on the co-loaded host)
- `implementer (d1431ae): Linux gate, non-e2e packages and test/e2e whole (--timeout 150m), both with --coload` — non-e2e all PASS except test/guards C6.3; e2e 305 pass / 3 fail: ThinSlice 3/3 PASS alone; SpooledSessionStart and LiveSessionWriteSet fail 3/3 alone at d1431ae AND at base 90e1db3 (pre-existing)

### Criterion changes

- Fix seat: two rows added to test/guards' sharedReaders inventory (internal/testutil/daemongone.go DaemonHoldingLock, and test/e2e/faultinject_test.go e2eSpawnInFlight). Both strengthen the guard: an os.ReadFile regression in either reader now fails TestGuard_HotFilesAreReadWithDeleteSharing. Nothing was loosened.
- Carried from the implementer (d1431ae): x13 now fails when Received < Durable (errX13v4Unaccounted), after a re-read within obsProcessBound. Rationale: every hot-path WAL line has exactly one sample behind it, so a quiescent daemon has Received == Durable. Before, a renamed instrument read as zero and the row passed silently.

### Open issues

- C6.3: TestCarriedDefects_WaveReportRequiresResolution is red on Windows and Linux (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3 are still deferred:V6-VERIFY while plans/V6-report.md exists). Its inputs are unchanged since base. The coordinator must record the final dispositions.
- Two Linux e2e reds, TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly, reproduce 3/3 alone at base 90e1db3 as well as at HEAD, so they are pre-existing and unowned.
- Windows TestV3_HotPathUnchangedWithLedgerResident (x11 B-A/B-B timing gate) is red on this co-loaded host even when run alone. It is the known hot-path budget class and must be judged on a quiet host or in the timing lane.
- The 248b481 row for e2eSpawnInFlight goes beyond the reviewer's finding (the same reader class). It is a separate commit, so the coordinator can drop it without affecting the finding's fix.

### Needs the owner

- New test-pacing constants in internal/testutil/daemongone_test.go, each named with a derivation comment. stagedShutdownTick = 25 ms paces the staged helper calls; if it is wrong, only test speed changes. stagedShutdownRoundTrip = 5 s is each attempt's connect/ACK deadline; if it is too short under load, a Send spools, which is harmless. stagedUnidentifiedBound = 3 s is waited out in full by the unidentified row; if it is too short on a starved host, the result is a false red, never a false green. lockHolderExitBound = 30 s is the reap fallback for the test's own stand-in child and affects cleanup only. stagedShutdownBound is daemon.StopCleanupBound, derived rather than new.
- Expected counts in internal/daemon/observer_atomic_publication_test.go: leasedObserverPrePasses = 1 and leasedPublicationPasses = 1 + 2 + 1 = 4. They are derived from the observer's non-declaring branch and FSStore's two barriers; they are not budgets. If the store's barrier count or the observer's pass structure changes, the row fails, which is intended.
- Acknowledge the x13 criterion change (d1431ae): Received < Durable now fails the row, after a re-read within obsProcessBound.
- Assign an owner for the two Linux e2e reds that reproduce at base 90e1db3: TestE2E_SpooledSessionStartNeverDegradesTheProject and TestV3_LiveSessionWriteSetAndAppendOnly.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



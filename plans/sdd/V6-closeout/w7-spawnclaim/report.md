# w7-spawnclaim: the lock holder gives the spawn claim back (D27), Defender docs (D32)

Branch `closeout/w7-spawnclaim`. Workflow `wf_0b499fad-a33`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `45e5d948`

### Root cause

D27 cause: a daemon spawned while another process held daemon.lock loses that lock and exits before it listens. Only Run's post-listen removeSpawnLockFile ever deleted run/spawn.lock. So the claim the loser's spawner wrote stayed fresh for spawnLockStaleAfter (10 s), and nothing gave it back when the winning daemon exited. A SessionEnd flush inside that window found no daemon and a fresh claim, stood aside (D17) and started nothing: ClaimSpawn answered SpawnInFlight (2) where SpawnClaimed (1) was expected. In production the loser is the `qompack daemon` command losing acquireWriterLease (internal/cli/daemon.go), not daemon.Run. Its winner releases the lock through the writer lease's Lock.ReleaseWithReport, and the daemon's own releaseRunLease does nothing there because the lease is borrowed. So the one place that reaches every holder is Lock.Release.

### Summary

# w7-spawnclaim report

Branch `closeout/w7-spawnclaim`, base `898bb8b`, head `45e5d948`. All three items are done, and the tests I ran pass on Windows and, for the new rows, on Linux non-root with -race.

## (1) D27: the stray spawn claim

**The rule I chose.** `Lock.Release` (internal/daemon/lock.go) now deletes `run/spawn.lock`. It only does this when the lock is still owned by the caller, and it does it just before deleting daemon.lock, so removing the lock file stays Release's last act.
- Every holder of daemon.lock releases through this function: a daemon's Run (the listen-failure and stopped-mid-startup paths), its Stop, the daemon command's writer lease, and the operator tools (fsck, delivery-seal).
- A Release of a lock someone else has since reclaimed touches nothing.

**Why this is safe for D17.** Any claim still present at release time was made while the lock was held, or belongs to the releasing daemon's own spawner.
- Its daemon has either already lost, or will find the lock free and take it.
- The worst case is one extra spawn, and daemon.lock turns that second daemon away. Two daemons can never run for one project.

**Why the losing daemon does not delete the claim itself** (the brief's other option):
- It would only fix the in-process path. In production the loser exits in internal/cli/daemon.go at acquireWriterLease and never reaches daemon.Run.
- It would remove the accidental throttle. Today a lingering claim limits a loaded session, where hooks' 5 ms dials keep failing, to about one spurious spawn per 10 s. With loser-removes it becomes one spawn per failed hook.
- Release-removes gives the claim back exactly when it matters: when no daemon holds the lock.

**Other changes in the fix commit:**
- internal/daemon/spawn.go: `removeSpawnLockFile` now calls a new `removeSpawnClaim(path)`; the comments are updated.
- internal/ipc/spawnlock.go: the `SpawnLock` doc comment now describes the new rule.
- docs/architecture.md: one sentence added to "Starting the daemon".

**Rows** (internal/daemon/spawn_claim_release_test.go):
- `TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops`: the w6-linuxrows diagnostic, landed. It adds a fixture check that the loser's claim is still there before A stops.
- `TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon`: a real ipc client sends a flush after A stops. Its lazy spawn runs exactly once, and the daemon it starts comes up, holds the lock and answers a probe.
- `TestLockRelease_GivesBackTheSpawnClaim`: Release removes the claim; a Release of a reclaimed lock leaves it alone.

**Fixture fix.** On the first run the diagnostic's own fixture was racy: A's startup deletes spawn.lock after it listens, and the old fixture waited only for the lock. The fixture now waits for an answered admin.ping, which only comes after A's startup is done. The three rows then failed on the pre-fix code and passed after it.

**x09's wait (c551fcd) is not dead code, so I kept it.** A hook's spawn that is still starting when the pre-flush shutdown ends still leaves a fresh claim, and its daemon then takes the lock. The wait makes the flush meet that daemon once it is up. Only the comments and the log line changed; the assertions and the bound are untouched. In the Windows run the wait never fired: the log line does not appear.

## (2) The w6-borrow verify nit

Reworded to say the reply keeps *about* the compact bound, less the poll's return and the dial's transit (the edge D29 accepted), the way docs/architecture.md reads:
- sessionstart.go: the `ensureDaemonRunning` doc comment.
- hookbudget.go: the `borrowBy` item and the `replyDeadline` doc comment.
- spawn.go: the `EnsureRunningUntil` doc comment ("in full" dropped, paragraph reflowed).

No code changed.

## (3) D32: Windows Defender

- **docs/troubleshooting.md §7**, new entry "Windows Defender flags `qompack.exe`" (Symptom / Diagnose / Meaning / Action):
  - the detection names and the Windows "file contains a virus" refusal;
  - Protection history;
  - `Get-FileHash` or `sha256sum` against the release's `checksums.txt`, and checking the staged copy against its content-addressed directory name;
  - what `!ml` means;
  - how to report a false positive at https://www.microsoft.com/en-us/wdsi/filesubmission;
  - that restoring the file or adding an exclusion is the user's own decision (Qompack never adds one), and that the plugin can be disabled meanwhile.
  - It says plainly that only development builds were observed flagged.
- **docs/release.md §7**: a new bullet "No code signing (open release item)", linking to the entry.

I changed no Defender setting and submitted nothing to Microsoft.

## Commands and results

Windows; the machine was loaded by other workstreams. Evidence is in plans/sdd/V6-closeout/w7-spawnclaim/runs/.
- `go test -count=1 -timeout=10m -v -run '^(TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops|TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon|TestLockRelease_GivesBackTheSpawnClaim)$' ./internal/daemon` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - pre-fix code: FAIL on all three: expected 1, got 2; expected 1, got 0; spawn.lock still exists (runs/d27-red-windows.log).
  - after the fix: ok (runs/d27-green-windows.log).
- `go test -count=1 -timeout=60m ./internal/daemon`: ok, 543 s (runs/daemon-full-windows.log).
- `go test -count=1 -timeout=20m ./internal/ipc`: ok (runs/ipc-full-windows.log).
- `go test -count=1 -timeout=30m ./internal/cli`: ok (runs/cli-full-windows.log).
- `go test -count=1 -timeout=30m -v -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' ./test/e2e`: PASS, GC line 9.3 s after the flush (runs/x09-windows.log).
- `go test -count=1 ./test/docs`: ok.
- Linux: `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-spawnclaim ... 2cd0a644 d27-rows --run <the three D27 rows plus the lock and EnsureRunning families> --count 5 -- ./internal/daemon`
  - non-root uid 10001, -race: PASS, 100 passed, 0 failed (runs/linux-d27-rows-2cd0a64.txt).
- Checks, all passing:
  - `go vet` on internal/daemon, internal/ipc, internal/cli and test/e2e, on Windows and with GOOS=linux;
  - `go run ./tools/devtool fmt-check`;
  - `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every sub-check PASS.

No wall-clock failures happened, so nothing needed a re-run alone. No new budget or bound constants were added: the rows reuse spawnLockTestBound, stopCleanupBound, ensureRunningPollInterval, spawnLockMissBound and spawnClaimDialTimeout.

## Criterion changes

None. No assertion, threshold or bound was changed. x09 changed only in its comments and its log line.

### Commits

- 5a5b018a docs(troubleshooting): document the Defender ML false positive
- 2c902593 docs(cli): say the borrow leaves about the compact bound
- 1859a8dd docs(daemon): drop "in full" from EnsureRunningUntil's doc
- 2cd0a644 fix(daemon): give run/spawn.lock back when daemon.lock is released
- 45e5d948 test(e2e): say why x09 still waits for a fresh spawn claim

### Tests

- `go test -count=1 -timeout=10m -v -run '^(TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops|TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon|TestLockRelease_GivesBackTheSpawnClaim)$' ./internal/daemon (pre-fix)` — FAIL on all three as expected (runs/d27-red-windows.log)
- `same, after the fix` — ok (runs/d27-green-windows.log)
- `go test -count=1 -timeout=60m ./internal/daemon` — ok 543s
- `go test -count=1 -timeout=20m ./internal/ipc` — ok
- `go test -count=1 -timeout=30m ./internal/cli` — ok
- `go test -count=1 -timeout=30m -v -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' ./test/e2e` — PASS (GC line 9.3s after the flush)
- `linux-nonroot-gate.sh --prefix cx-w7-spawnclaim 2cd0a644 d27-rows --count 5 -- ./internal/daemon (non-root, -race)` — PASS 100/0
- `go test -count=1 ./test/docs` — ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS
- `go vet (Windows and GOOS=linux) internal/daemon internal/ipc internal/cli test/e2e; devtool fmt-check` — clean

### Open issues

- Not fixed, and outside D27: a flush that arrives while the running daemon is still stopping (listener closed, lock still held) spawns a daemon that loses. The flush then waits in the spool until the next session (replayed, not lost). Neither fix option covers this.
- Possible x09 risk, not observed: a hook spawn still in flight at x09's pre-flush shutdown could take the lock during the in-process negknow phase. This existed before this change.
- Code signing of the Windows binaries is recorded as an open, unowned release item in docs/release.md section 7.

### Needs the owner

- D27 rule chosen: the lock holder gives the claim back in Lock.Release, and the losing daemon does not delete it itself. This keeps the roughly one-spurious-spawn-per-10s throttle while a daemon runs, and fixes the production path, where the loser exits in the cli writer lease before daemon.Run. Confirm, or ask for loser-removes as well.
- D32: the owner still has to submit the false-positive report and decide on the host exclusion. Code signing needs an owner and a decision before a public release.

## Independent review

### review:spawnclaim: needs-fixes

- **minor** `test/guards/sealresidualreport_test.go:70 (row fn "ensureRunning") vs internal/daemon/spawn.go:211 (ensureRunningWith)` — TestGuard_AReleasedLockReportsItsSealResidual/every_release_in_internal/daemon_is_accounted_for is red on this branch. The red is pre-existing: base 898bb8b already fails it, because w6-borrow's 1a3fa769 moved the ipc.SpawnLock.Release calls from ensureRunning into ensureRunningWith and never updated the guard row. This branch did not cause it. But this workstream changed what Lock.Release does in internal/daemon, and this guard polices Release calls in that package. The implementer's evidence does not include test/guards, and the report does not mention the red.
  - Evidence: `go test -count=1 -run TestGuard_AReleasedLockReportsItsSealResidual ./test/guards` fails with: expected [ReleaseWithReport RepairDeliverySeal ensureRunning spawnDetached], actual [... ensureRunningWith ...]. `git show 898bb8b:internal/daemon/spawn.go` already has `func ensureRunningWith` at line 211, and the base guard still names `ensureRunning`. The remaining test/guards failures are TestCarriedDefects_WaveReportRequiresResolution checkpoint-status rows, unrelated and also pre-existing.
  - Fix: In releasesWithNoSealResidual, change the spawn.go exemption row's fn from "ensureRunning" to "ensureRunningWith" and keep the same reason. This is one line in test/guards, which is outside this workstream's declared scope, so either land it at integration or route it to the owner. Also add `go test ./test/guards` (or at least this row) to the evidence for any change to Lock.Release.
- **nit** `test/guards/sharedreaders_test.go:59, :63-65, :127-130` — The shared-reader inventory's why strings still name only two deleters of run/spawn.lock: daemon.removeSpawnLockFile and a competing spawner's stale reclaim. Lock.Release now deletes it too, through removeSpawnClaim, from the lock holder's process: the winning daemon, the daemon command's writer lease, fsck and delivery-seal. That is exactly the kind of cross-process delete these rows exist to name. The guard still passes, because readSpawnLock and e2eSpawnInFlight already use shared handles, but the recorded reason is now incomplete.
  - Evidence: lock.go:405 adds `removeSpawnClaim(filepath.Join(filepath.Dir(l.path), runSpawnLockFileName))` inside Release. The sharedreaders rows for readSpawnLock, removeSpawnLockIf and e2eSpawnInFlight name only removeSpawnLockFile and the stale reclaim.
  - Fix: Add "and Lock.Release's give-back as daemon.lock's holder lets go (D27)" to those three why strings.
- **nit** `docs/troubleshooting.md:770-775` — The entry that explains a second `qompack daemon` process appearing and exiting at once lists two causes: a spawn slower than the 10 s window, and a missed short dial. The D27 rule adds a third, which docs/architecture.md and lock.go now state as the fix's accepted cost. A lock holder that releases while another spawn is still starting deletes that spawn's claim, so the next spawner can start one extra daemon, and daemon.lock turns it away. The operator-facing doc does not list this cause.
  - Evidence: lock.go Release doc: "removing it costs at most one extra spawn, which daemon.lock turns away (D17)". architecture.md says the same. troubleshooting.md:771-775 still says "Both are signs of heavy load" and does not mention a daemon (or fsck/backup) stopping while a spawn was in flight.
  - Fix: Add one clause, for example: "...or a daemon (or an operator command holding the lock) that stopped while a spawn was still starting, which gives that spawn's claim back (D27)."
- **nit** `commits 5a5b018a, 2c902593, 1859a8dd (Refs footers)` — All five commits carry `Refs: V6-VERIFY, C1.16/D27`. Three of them are not D27 work. 5a5b018a is D32 (the Defender docs), and 2c902593 and 1859a8dd are the w6-borrow verify nit (D29's edge). A grep for D32 or for the w6-borrow nit in the log will miss them.
  - Evidence: `git log 898bb8b..HEAD`: the D32 commit body says "(owner decision D32)", but its footer says `Refs: V6-VERIFY, C1.16/D27`. The borrow-comment commits say "w6-borrow verify nit" and "D29" in their bodies, with the same D27 footer.
  - Fix: If the branch is squashed or rebased at integration, set the footers to `Refs: V6-VERIFY, C1.16/D32` and `Refs: V6-VERIFY, C1.16/D29 (w6-borrow verify nit)`. Otherwise, note the mapping in the workstream report.

## Fix seat (review resolution) — status `done`, head `b00ddefe`

### Root cause

D27: a daemon spawned while another held daemon.lock lost the lock and exited without listening, so nothing removed the run/spawn.lock its spawner wrote. For spawnLockStaleAfter (10 s) every later spawner read that claim as a spawn in flight (ipc.ClaimSpawn -> SpawnInFlight), so a SessionEnd flush arriving after the running daemon exited took no claim and started no daemon. Fix (2cd0a644): Lock.Release removes run/spawn.lock while daemon.lock is still held, before it removes the lock itself. Any claim present then belongs to this owner's own spawner or was made while the lock was held, so the worst case is one extra spawn, and daemon.lock turns that away (D17 still holds). Review finding: 1a3fa769 (w6-borrow) moved the ipc.SpawnLock.Release calls from ensureRunning into ensureRunningWith but left the test/guards exemption row naming ensureRunning. That subtest was already red on base 898bb8b.

### Summary

Fix seat for w7-spawnclaim. The implementer's work (D27 fix, the three borrow comments reworded, D32 Defender docs, x09 comment) stands unchanged. The reviewer's one minor finding checked out, and I fixed it.

## Review resolution
- Finding (minor): TestGuard_AReleasedLockReportsItsSealResidual/every_release_in_internal/daemon_is_accounted_for is red, and the implementer's evidence does not cover test/guards. **Checked, and correct.** The base 898bb8b guard file still names `fn: "ensureRunning"` at line 70, but the three `lock.Release()` calls (ipc.SpawnLock, spawn.go:260/266/270) have lived in `ensureRunningWith` since 1a3fa769. No other closeout branch had fixed it yet (`git log --all -S'fn: "ensureRunningWith"'` finds nothing). **Action:** c1f1a3a3 changes that one row's fn to `ensureRunningWith` and keeps its reason word for word. This is not a criterion change: the row now points at the function the code moved to, the release count still has to match, and no check was taken out or loosened. The D27 change to Lock.Release adds a plain call, `removeSpawnClaim(...)`, not a selector `Release` call, so the guard's accounting does not change. The edit is one line outside the declared scope and sits in its own commit, so the coordinator can drop it if another branch lands the same line first. b00ddefe adds the test/guards run to runs/guards-full-windows.log.

## Commands and results (Windows; the machine was loaded)
- `go test -count=1 -run TestGuard_AReleasedLockReportsItsSealResidual ./test/guards` before the fix: FAIL (expected [... ensureRunning ...], actual [... ensureRunningWith ...]). This matches the reviewer's evidence.
- `go test -count=1 -run 'TestGuard_AReleasedLockReportsItsSealResidual|TestGuard_TheSealResidualScannerSeesBothCallShapes' -v ./test/guards` after the fix: PASS, exit 0.
- `go test -count=1 ./test/guards` (full package, once): exit 1. The only failures are TestCarriedDefects_WaveReportRequiresResolution rows SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2 and SP08-D3 ("still deferred:V6-VERIFY ... but plans/V6-report.md exists"). They were already failing on base, they are about ledger and report status, and they are the coordinator's, not this workstream's.
- `go vet ./test/guards` and `GOOS=linux go vet ./test/guards`: clean. `go run ./tools/devtool fmt-check`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.
- Implementer's evidence, carried forward unchanged and committed under plans/sdd/V6-closeout/w7-spawnclaim/runs/:
  - d27-red-windows.log and d27-green-windows.log: TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops, TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon and TestLockRelease_GivesBackTheSpawnClaim, red before the fix and green after.
  - Full-package runs for daemon, cli and ipc.
  - x09-windows.log.
  - linux-d27-rows-2cd0a64.txt: internal/daemon under -race as non-root, pass=100 fail=0.

## What changed across the branch
- internal/daemon/lock.go and spawn.go: Lock.Release gives back run/spawn.lock (removeSpawnClaim is shared with Run's cleanup after listen).
- internal/ipc/spawnlock.go: doc comment updated.
- New internal/daemon/spawn_claim_release_test.go with the three rows above.
- test/e2e/v3_x09_test.go: the wait stays. A claim can still be fresh when a hook's spawn was starting as the running daemon shut down; that daemon then takes the lock. So the wait is not dead code. Only its comment changed, and every assertion is kept.
- Comments in internal/cli/sessionstart.go, internal/cli/hookbudget.go and internal/daemon/spawn.go now say "about the compact bound, less the poll's return and the dial's transit" (D29). docs/architecture.md was aligned with them.
- docs/troubleshooting.md has a section on the Defender Bearfoos ML false positive: the symptom, checking the binary against the release checksums, reporting the false positive to Microsoft, and that an exclusion is the user's own decision. docs/release.md records code signing as an open release item.

No Defender setting was changed and nothing was submitted to Microsoft. None of my commits carries an attribution trailer. No background processes are left running.

### Commits

- 5a5b018a docs(troubleshooting): document the Defender ML false positive
- 2c902593 docs(cli): say the borrow leaves about the compact bound
- 1859a8dd docs(daemon): drop "in full" from EnsureRunningUntil's doc
- 2cd0a644 fix(daemon): give run/spawn.lock back when daemon.lock is released
- 45e5d948 test(e2e): say why x09 still waits for a fresh spawn claim
- c1f1a3a3 test(guards): follow the spawn-claim release into ensureRunningWith
- b00ddefe test(guards): record the w7-spawnclaim test/guards run

### Tests

- `go test -count=1 -run TestGuard_AReleasedLockReportsItsSealResidual ./test/guards (before c1f1a3a3)` — FAIL: expected ensureRunning, actual ensureRunningWith (already failing on 898bb8b)
- `go test -count=1 -run 'TestGuard_AReleasedLockReportsItsSealResidual|TestGuard_TheSealResidualScannerSeesBothCallShapes' -v ./test/guards` — PASS, exit 0
- `go test -count=1 ./test/guards` — exit 1: only TestCarriedDefects_WaveReportRequiresResolution rows (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3) fail; already failing on base, ledger status, not this workstream
- `go vet ./test/guards && GOOS=linux go vet ./test/guards` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `go test -count=1 -run 'TestSpawnClaim_ALostRaceLeavesNoClaimOnceTheRunningDaemonStops|TestSpawnClaim_AFlushAfterTheRunningDaemonExitsStartsExactlyOneDaemon|TestLockRelease_GivesBackTheSpawnClaim' ./internal/daemon (implementer)` — red before 2cd0a644, green after (runs/d27-red-windows.log, runs/d27-green-windows.log)
- `linux-nonroot-gate.sh --prefix cx-w7-spawnclaim, internal/daemon -race @2cd0a64 (implementer)` — PASS pass=100 fail=0 (runs/linux-d27-rows-2cd0a64.txt)

### Criterion changes

- test/guards/sealresidualreport_test.go: the releasesWithNoSealResidual row's fn changed from ensureRunning to ensureRunningWith. The ipc.SpawnLock.Release calls moved there in 1a3fa769. The reason text is unchanged and the release count is still enforced, so no check was loosened.

### Open issues

- test/guards TestCarriedDefects_WaveReportRequiresResolution rows SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2 and SP08-D3 fail on base 898bb8b and on this branch: CARRIED-DEFECTS.tsv still says deferred:V6-VERIFY while plans/V6-report.md exists. This is the coordinator's ledger work, not this workstream's.
- c1f1a3a3 is a one-line test/guards edit outside the declared scope. If another branch lands the same row fix, drop one copy at integration.

### Needs the owner

- D27 rule chosen: the lock holder gives the claim back in Lock.Release, and the losing daemon does not delete it itself. This keeps the roughly one-spurious-spawn-per-10s throttle while a daemon runs, and fixes the production path, where the loser exits in the cli writer lease before daemon.Run. Confirm, or ask for loser-removes as well.
- D32: the owner still has to submit the false-positive report and decide on the host exclusion. Code signing needs an owner and a decision before a public release.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



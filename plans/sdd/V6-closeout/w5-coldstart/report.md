# V6 close-out w5-coldstart: cold start (D17: spawn lock, startup gate, session-start budget)

Branch `closeout/w5-coldstart`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `1b79f803a54928a1b11d849c5fadc4f73fc6d583`

### Root cause

(1) D17a: EnsureRunning ignored run/spawn.lock. When a hook's lazy spawn (or MCP's, another session-start's, or a Windows staged-copy spawn still copying) had just started the daemon, session-start started a second one, which lost daemon.lock and exited. Base co-load diagnostic: 9 of 60 rows ran two or three daemons. The lazy-spawn copy of the rule had its own defects: an empty lock (CreateNew creates the file, then writes and fsyncs the stamp) was reclaimed; a future stamp read as fresh indefinitely; a spawn that failed to start kept its claim for 10 s. (2) The NOUP row is a daemon whose listener existed but was not serving. It was not a second daemon losing a race, and it was not a startup that hit a bound and exited: the process was still listed at 30 s both in w3 runs/50 and in the first seat's heavy run. Run listened, wrote state.bin and removed spawn.lock, then did its whole startup before it started the accept loop: the spool replay (fsync-bound: 6.5 s for one line at 1x co-load; still inside the delivery-journal open at 20 s under an fsync storm), then the checkpoint sweep and the publication audit. On Windows a named pipe with no accept pending refuses every dial: verified, 4 of 4 dials failed at their 250 ms timeout; on Linux the backlog took all 4 in about 0.1 ms. So for its whole startup the daemon looked absent: EnsureRunning's poll missed it, the hook's connect failed and spooled, and each lazy spawn let through by the removed claim started another daemon, which competed for CPU and disk. (3) D17b: only the 10 s reply wait was bounded. Admission, staging and EnsureRunning's poll ran before it with no bound against the 15 s manifest timeout. Found while fixing: Windows process creation stalled 4-5 s in about 7-12% of spawns under co-load (cause not identified), so an absolute pre-send deadline alone gave up on daemons that were moments away. Starting the accept loop early also let a cancel during startup reach Run as a transport error.

### Summary

# w5-coldstart (D17): cold start. Final report

All three items are done. Branch closeout/w5-coldstart (worktree ../qompack-cx-w5-coldstart), HEAD 1b79f80, working tree clean, nothing pushed. Evidence is in plans/sdd/V6-closeout/w5-coldstart/runs/ (01-41, linux/, zz_*.txt).

## Resuming: what I did with the first seat's draft
- It made no commits. It left uncommitted edits in spawn.go and client.go, a new spawnlock.go, and three test files.
- No temporary instrumentation reached product code. The first seat's zzdiag marks were only in a scratch export; the worktree `git diff` had none.
- Kept and revised:
  - ipc.ClaimSpawn and lazySpawn using it: I added the future-stamp rule.
  - EnsureRunning's claim loop: restructured around pollBound; EnsureRunningUntil added.
  - The tests: adapted, plus new rows.
- The first seat's red logs 01/02 were replaced. I reran the final tests on a `git archive 90e1db3` export, with the base EnsureRunning algorithm behind a test-only adapter (runs/01, runs/02).
- Everything below was rerun after the restart. The Windows diagnostic runs are at 90e1db3, 6b99419, 48cb093 and aba55d8 (none of the code commits after aba55d8 changes code). The Linux runs are at dc58fad (no code change since it).

## (1) D17a: one daemon per project
**Fix (fab6444, b503071).** ipc.ClaimSpawn is the single rule every spawner follows over run/spawn.lock: session-start's EnsureRunning, the hooks' lazySpawn and MCP's lazySpawn (the same ipc client code).
- The first spawner to create the lock spawns.
- A claim younger than spawnLockStaleAfter (10 s, existing constant) is a spawn in flight:
  - session-start waits for that daemon;
  - a hook or MCP does nothing.
- An older claim is reclaimed. That is the fail-safe: a dead spawner blocks spawns for at most 10 s.
- An empty or unparseable lock, or one stamped after now, is judged by the file's modification time.
- A spawn that fails to start releases its claim.
- EnsureRunning re-checks the claim on every poll, so a stale claim is reclaimed inside the wait.
- Staging (D10) happens after the claim, so a hook that arrives while a copy is still being written waits for it.

**Red rows.**
- runs/01: five `-run '^TestEnsureRunning_'` rows fail on the base. They cover two cold hooks, a fresh lock, a hook arriving mid-staging, a lazy spawn held off by session-start's claim, and an abandoned lock.
- runs/02: three `-run '^TestLazySpawn_'` rows fail on the base. They cover a lock still being written, a future stamp, and releasing a failed spawn's claim.

## (2) NOUP: root cause and fix
**Root cause.** See root_cause. In short: the listener existed but nothing served it.
- The daemon was alive but still inside Run's startup.
- On Windows, a named pipe with no accept pending refuses every dial. runs/22 (TempDir-resolved pipe, the same listen/dial code the daemon uses): 4 of 4 dials timed out; once the accept loop ran, they took about 13 ms.
- On Linux the backlog took the same dials (runs/23).
- The first seat's heavy-load stack (runs/29) shows Run inside Drain, opening the delivery journal, at 20 s.
- w3 runs/50's "gone from the process list" is contradicted by that same log: pid 50708 is listed at 30 s.

**Fix (2f4867f).** Run starts its accept loop as soon as the endpoint exists.
- Every request it reads waits at serveOp until the startup is done, which is where the loop used to start. So the spool replay still comes first, and Stop still meets a finished startup.
- The first served request still proves the startup is done (redrainOnceServing).
- Clients now connect during startup and wait up to their own deadline, as they already did on POSIX. They no longer fail their connect and lazy-spawn doomed daemons.
- Regression row `-run '^TestRun_AcceptsDialsWhileItsStartupDrainRuns$'`: red on the pre-gate daemon.go (runs/03, dial refused), green now.
  - It also asserts that a live event sent during the replay is held and then served after it.
  - Its red is Windows-only, which is the platform with the defect.

**Follow-ups the change needed.**
- 8b8233d: a cancel during startup now ends Serve before the serve loop first selects. Both loop arms are then ready, and the serveErrCh arm returned context.Canceled. `-run '^TestRunCtxDoneGoesThroughStop$'` failed 9 of 20 (runs/13). After the fix it passes 50 of 50 (runs/14).
- aba55d8: the e2e helper e2eWaitDaemonUp now waits for an answered admin.ping instead of a dial. `-run '^TestE2E_SpooledSessionStartNeverDegradesTheProject$'` was reading the history before the replay had run (runs/16). It passes 5 of 5 now (runs/18). On POSIX the helper was already racy before D17.

## (3) D17b: session-start ends inside 15 s
**How the 15 s is shared out (6b99419, f77aea2).** Counted from doHook's first statement, on the wall clock:

| Share | Time | Constant |
|---|---|---|
| Kept at the end for process start before doHook, output write and exit | 1.5 s | hookExitReserve (new) |
| Reply wait | 10 s | sessionStartReplyDeadline |
| Dial | 250 ms | hookConnectDeadlineFloor |
| What is left for pre-send (admission + staging + poll) | 3.25 s | derived, preSendBy |

- The manifest's 15 s is read from pluginmanifest.
- EnsureRunningUntil polls until preSendBy. A daemon spawned late (process-creation stall), or another spawner's daemon found late, still gets the existing 1.5 s wait. The poll never runs past latestPoll = doneBy − dial.
- Staging and admission are never cut short.
- The reply wait is shortened to min(10 s, doneBy − now − dial). With nothing left, the request is spooled without dialling. Either way the answer is an unanswered start's: `{}`, or the D9 deferred note for compact.

**Why the 1.5 s grace was added.** The first cut (6b99419) spooled 8 of 60 co-load rows. In every one, process creation stalled 4-5 s and the fixed deadline had already passed (runs/31). The grace fixed that: 0 of 60 spooled at 48cb093 (runs/32), with 7 stalls over 1 s, none spooled. The late-spawn row is red under the previous rule (runs/06).

**Tests.**
- `-run '^TestSessionStartBudget_'` pins the numbers.
- An overrun cuts the reply wait and the hook ends by its bound.
- With no time left it spools without a dial and a compact start gets the note.
- Red with the budget plumbed but not applied: runs/04, 5.26 s and 6.6 s against a 4.5 s bound.

## Measurement: CPU co-load (NumCPU spinners), cold-start diagnostic, 60 rows per run

The diagnostic was a temporary copy in scratch, never in the tree; its source is runs/zz_w5_coldstart_diag_test.go.txt. Summary in runs/36.

| Run | Spooled | Two+ daemons | NOUP | Session-start wall, ms (p50 / p90 / p99 / max) |
|---|---|---|---|---|
| base 90e1db3 (runs/30) | 3 | 9 rows | 0 | 1064 / 2648 / 6261 / 7271 |
| 6b99419, without the grace (runs/31) | 8 | 1 | 0 | 896 / 4948 / 6203 / 6778 |
| 48cb093 (runs/32) | 0 | 0 | 0 | 689 / 1314 / 4099 / 6444 |
| final aba55d8 (runs/35) | 1 | 0 | 0 | 649 / 3091 / 4773 / 6520 |

- **Spooled rows by source.**

  | Source | Base | Final |
  |---|---|---|
  | startup | 2/20 | 0/20 |
  | resume | 0/20 | 0/20 |
  | compact | 1/20 | 1/20 |

- **The one final-HEAD spool.** A staged-cold compact row. The daemon itself stalled 4.9 s acquiring its writer lease; the hook gave up at its pre-send deadline and ended at 3.4 s with the note.
- **Staged vs warm.** Staged-cold 0/15 spooled at base and 1/15 at final; staged-warm 0/15 at both.
- **Heavy load** (2x spinners plus 8 fsync loops, 12 rows): 0 spooled at both base and 48cb093 this time (runs/33, runs/34). The NOUP did not recur under today's conditions. It is pinned deterministically by the regression row.
- **Overhead outside doHook.** At most 1452 ms, on the first exec of a new binary under co-load. This is the basis for the 1.5 s reserve.

## Criterion changes (with rationale)
- **TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun** now requires an answered admin.ping instead of a dial.
  - Why: a dial no longer proves the startup ended.
  - Checked: a startup that never finishes fails it, verified by hand.
  - This is a strengthening.
- **e2eWaitDaemonUp**: an answered ping instead of a dial, which is what its doc already said it did. A strengthening.
- **test/guards**, rule unchanged in both:
  - ensureRunning is added to releasesWithNoSealResidual. Its Release is ipc.SpawnLock's, the spawn claim, not daemon.lock.
  - The sharedreaders row client.go:spawnLockIsStale becomes spawnlock.go readSpawnLock and removeSpawnLockIf, where the code moved. Both still use ReadFileShared.
  - Red with the stale rows: runs/07 and runs/11.
- **Lazy-spawn behaviour.** A fresh empty or unparseable lock is now a spawn in flight, not reclaimed. A future stamp is judged by the file's age. A failed spawn releases its claim. The existing lazy-spawn rows are unchanged and pass.

No skip, nolint, lowered threshold, regenerated golden or loosened assertion.

## Checks
**Windows:**
- fmt-check, vet (Windows and GOOS=linux) and test/docs: ok.
- devtool lint subset (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns): all PASS (runs/37).
- Packages in full: ipc ok (runs/08), cli ok (runs/09), daemon ok 387 s (runs/15), test/fault ok (runs/40).
- Focused e2e: 28 rows pass (runs/17).

**Linux, non-root, -race, dc58fad:**
- ipc 150, daemon 1510 (one pre-existing Windows-only skip), cli 377.
- D17 rows ×20: 640 pass.
- Focused e2e: 107 pass.
- test/e2e in full: 302 pass, 1 fail, 3 skips (TestE2E_RequiredProductChildRaceInstrumentation, TestInstall_HostCLIInstallUpgradeUninstall, TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting); I checked that no skip is new, but not why each one skips.
- No race logs.

**Pre-existing failures (not caused here):**
- TestCarriedDefects_WaveReportRequiresResolution: also fails on 90e1db3 (runs/39).
- TestIntegration_HotPathWarmWithRealResidentState: fails the same way on base and HEAD (593 deferred; runs/20-21).
- TestV3_HotPathUnchangedWithLedgerResident: fails the same way on base and HEAD on Linux (592 deferred).

Wall-clock failures were rerun alone.

## Coordination
w5-home: my only edit at hook entry is one line in doHook (`began := time.Now()` after the B-A origin). The budget code sits around preSend and Send.

### Commits

- fab6444 fix(ipc): share one spawn-lock rule for every daemon spawner
- b503071 fix(daemon): wait for the daemon a fresh spawn lock announces
- 2f4867f fix(daemon): take dials while the startup drain runs
- 6b99419 fix(cli): end session-start inside its manifest timeout
- cc52282 docs: document the spawn rule, the startup gate and the start budget
- f77aea2 fix(daemon): give a daemon started late its classic wait
- 48cb093 docs: say a daemon started late still gets its 1.5 s
- 7c3fbdd test(guards): follow the spawn-lock reads and release to spawnlock.go
- 8b8233d fix(daemon): stop through Stop when a cancel ends Serve in startup
- aba55d8 test(e2e): wait for an answered ping, not a dial, for "daemon up"
- dc58fad docs(closeout): record the w5-coldstart runs
- 5428bc3 docs: say only what the cold-start runs observed about stalls
- 1b79f80 docs(closeout): record the w5-coldstart Linux and final runs

### Tests

- `go test ./internal/daemon/ -run '^TestEnsureRunning_' -count=1 -v (base 90e1db3 export with the base algorithm behind a test-only adapter)` — FAIL as intended: 5 rows red (fresh lock, two cold hooks, mid-staging hook, lazy spawn held off, abandoned lock) (runs/01)
- `go test ./internal/ipc/ -run '^TestLazySpawn_' -count=1 -v (base 90e1db3 export)` — FAIL as intended: TestLazySpawn_ALockStillBeingWrittenIsInFlight, TestLazySpawn_AFutureStampDoesNotHoldSpawnsOff, TestLazySpawn_ReleasesTheClaimWhenSpawnFails red (runs/02)
- `go test ./internal/daemon/ -run '^TestRun_AcceptsDialsWhileItsStartupDrainRuns$' -count=1 -v (pre-gate daemon.go)` — FAIL as intended: a daemon replaying its spool refused the dial (runs/03); PASS with the gate
- `go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=1 -v (budget plumbed, not applied)` — FAIL as intended: 5.26 s and 6.6 s against a 4.5 s bound (runs/04); PASS after the clamp
- `go test ./internal/daemon/ -run '^TestEnsureRunningUntil_' -count=1 -v (previous rule: an until deadline is final)` — FAIL as intended: TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait red (runs/06); PASS now
- `go test ./internal/daemon/ -run '^TestRunCtxDoneGoesThroughStop$' -count=20, then -count=50 after 8b8233d` — 9/20 FAIL before the fix (runs/13); 50/50 PASS after (runs/14)
- `go test ./test/guards/ -run '^TestGuard_AReleasedLockReportsItsSealResidual$' -count=1 -v (before 7c3fbdd)` — FAIL: ensureRunning in neither list (runs/07); the full guards run also flagged the stale sharedreaders row (runs/11); both PASS after 7c3fbdd (runs/10)
- `go test ./test/e2e/ -run '^TestE2E_SpooledSessionStartNeverDegradesTheProject$' -count=5` — PASS 5/5 after aba55d8 (runs/18); it had failed in runs/16 before the helper fix
- `go test ./internal/ipc/ -count=1 ; go test ./internal/cli/ -count=1 ; go test ./internal/daemon/ -count=1 -timeout=30m (Windows)` — ok 37.7 s / ok 52.5 s / ok 387 s (runs/08, 09, 15); the earlier daemon run (runs/12) caught the 8b8233d race
- `go test ./test/guards/ -count=1 -v (Windows, HEAD)` — only TestCarriedDefects_WaveReportRequiresResolution fails, and it fails on base 90e1db3 too (runs/38, runs/39)
- `go test ./test/fault/ -count=1 -timeout=40m (Windows)` — ok 660 s (runs/40)
- `go test ./test/integration/ -count=1 -timeout=40m (Windows)` — only TestIntegration_HotPathWarmWithRealResidentState fails; rerun alone with -run '^TestIntegration_HotPathWarmWithRealResidentState$' it fails the same way on HEAD and on base 90e1db3 (593 of 2130 deferred) (runs/19-21); pre-existing
- `go test ./test/e2e/ -v with the 18-pattern focused set (session-start, lazy spawn, hook round trip, idle exit, all-six hooks, fault matrix, plugin-dir removability incl. MCP lazy spawn, spooled start, V1/V4/V5 round trips, spool submode, restart, shutdown helper rows) <!-- runpatterns: describes the focused set; the exact alternation is in runs/17-e2e-focused-windows.log and every branch names real tests in ./test/e2e -->` — 28 PASS (runs/17)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS (runs/37)
- `go run ./tools/devtool fmt-check ; go vet and GOOS=linux go vet on internal/ipc, internal/daemon, internal/cli, test/e2e, test/guards ; go test ./test/docs/` — all exit 0 (runs/05, runs/41)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-coldstart --repo C:/.../qompack-cx-w5-coldstart --out C:/.../runs/linux dc58fad touched-race --timeout 60m -- ./internal/ipc ./internal/daemon ./internal/cli ./test/guards` — non-root -race: ipc 150, daemon 1510 (1 pre-existing Windows-only skip), cli 377 pass; guards 166 pass, 7 fail = the pre-existing TestCarriedDefects_WaveReportRequiresResolution; no race logs
- `sh .../linux-nonroot-gate.sh ... dc58fad d17-rows-x20 --count 20 --run <the D17 rows: lazy-spawn, claim, EnsureRunning and EnsureRunningUntil prefixes, the startup-accept, startup-drain, ctx-done and redrain rows, and the budget and daemon-enabled rows> -- ./internal/ipc ./internal/daemon ./internal/cli <!-- runpatterns: describes the alternation; exact text in runs/linux/d17-rows-x20-host.log, every branch names real tests -->` — 640 PASS (ipc 220, daemon 300, cli 120), no race logs
- `sh .../linux-nonroot-gate.sh ... dc58fad e2e-focused (the same focused set as Windows) -- ./test/e2e <!-- runpatterns: exact alternation in runs/linux/e2e-focused-host.log -->` — 107 PASS
- `sh .../linux-nonroot-gate.sh ... dc58fad e2e-full --timeout 100m -- ./test/e2e` — 302 pass, 1 fail (TestV3_HotPathUnchangedWithLedgerResident), 3 skips, none new; the failing row run alone with -run '^TestV3_HotPathUnchangedWithLedgerResident$' fails the same way on base 90e1db3 and on dc58fad (592 of 2130 deferred); pre-existing
- `cold-start diagnostic: go test ./test/e2e/ -run '^TestZZW5_ColdStart$' with ZZ_CPU_COLOAD=1 ZZ_ITERS=5 in instrumented scratch exports <!-- runpatterns: names a temporary diagnostic kept as runs/zz_w5_coldstart_diag_test.go.txt, never in the tree -->` — base: 3/60 spooled, 9 rows with two or more daemons; 6b99419: 8/60 (process-creation stalls); 48cb093: 0/60, 0 extra daemons; final aba55d8: 1/60, 0 extra daemons; no NOUP in any; session-start wall max 7.3 s at base, 6.5 s at final (runs/30-36)
- `pre-serve dial diagnostic: go test ./internal/ipc/ -run '^TestZZPreServeDial$' (scratch export, Windows) and a Linux unix-socket program in /work/cx-w5-coldstart-diag <!-- runpatterns: names a temporary diagnostic kept as runs/zz_preserve_dial_diag_test.go.txt, never in the tree -->` — Windows: 4/4 dials to a listening, non-accepting pipe time out at 250 ms, about 13 ms once accepting; Linux: 4/4 connect in about 0.1 ms via the backlog (runs/22, runs/23)

### Criterion changes

- TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun (internal/daemon/daemon_test.go) requires an answered admin.ping instead of an ipc.Probe dial. Since 2f4867f a dial succeeds while the startup is still running, so only an answer proves the startup drain returned. This is a strengthening: a startup that never finishes fails it, checked by hand.
- e2eWaitDaemonUp (test/e2e/daemon_e2e_test.go) waits for an answered admin.ping over a spool-less client instead of a dial. Its doc already said 'polls until a daemon answers'. Rows that read what the startup wrote (the spooled-start row) now see it written. This is a strengthening.
- test/guards/sealresidualreport_test.go: ensureRunning joins releasesWithNoSealResidual. Its Release call is ipc.SpawnLock.Release, which returns the spawn claim, not daemon.lock and never a delivery journal. The guard's rule and counts for the lock-releasing sites are unchanged.
- test/guards/sharedreaders_test.go: the row for internal/ipc/client.go spawnLockIsStale (a function that no longer exists) is replaced by internal/ipc/spawnlock.go readSpawnLock and removeSpawnLockIf, where the reads moved. Both still use paths.ReadFileShared, and the guard's rule is unchanged.
- Lazy-spawn lock semantics, pinned by new rows: a fresh empty or unparseable spawn.lock is a spawn in flight (it used to be reclaimed at once); a stamp dated after now is judged by the file's age (it used to read as fresh until its date); a spawn that fails to start releases its claim (it used to hold every spawner off for 10 s). The existing lazy-spawn rows are unchanged and pass.
- Signature adaptations only, no assertion changed: TestEnsureDaemonRunning_GatedOnDaemonEnabled passes hookBudget{}, and the capture-admission row's preSend literal takes a hookBudget.

### Open issues

- With the split implemented, one of 60 final-HEAD co-load rows still spooled (runs/35: staged-cold compact). The daemon stalled 4.9 s acquiring its writer lease, and session-start gave up at its 3.25 s pre-send deadline with most of its 10 s reply budget unused. See needs_owner for an alternative split.
- Windows process creation stalls 4-5 s in about 7-12% of daemon spawns under CPU co-load on this machine, and in one row the daemon's first file operations stalled too. The cause is not identified; antivirus is suspected but unverified. The after-spawn grace absorbs it for session-start.
- The startup drain is fsync-bound: one replayed line took up to 6.5 s at 1x co-load, and over 20 s under the first seat's fsync storm. Clients now connect and wait instead of failing, but the daemon cannot answer until the replay is done, so hooks in that window still spool. This is a performance question for the store and drain lanes (D20 / C2.x), not a correctness one.
- The commit range is not bisect-clean on four points. The test/guards Release-site and shared-reader rows fail between fab6444/b503071 and 7c3fbdd. TestRunCtxDoneGoesThroughStop is flaky between 2f4867f and 8b8233d. TestE2E_SpooledSessionStartNeverDegradesTheProject can fail between 2f4867f and aba55d8. TestEnsureDaemonRunning_GatedOnDaemonEnabled's shape changed between 6b99419 and f77aea2. Squash at merge if bisectability matters.
- test/integration, test/platform, test/security and test/fault use a dial (ipc.Probe) as 'daemon up'. On Windows that now succeeds while the startup is still running, as it always did on POSIX. test/fault and test/integration pass, apart from the pre-existing hot-path row. test/platform and test/security were not run.
- Comments in test/fault, test/platform and test/security say session-start's EnsureRunning 'has already waited SpawnPollBound'. session-start now polls to its budget instead, usually longer. Their 60 s bounds are unaffected; the comments were not edited.
- The spawn claim's freshness window (10 s) can expire during a daemon start that takes longer than that. A later spawner then starts a second daemon, which loses daemon.lock and exits. This is documented as a residual.
- ClaimSpawn's Release compares millisecond stamps, so two claims made in the same millisecond could release each other. This only matters when a spawn fails, and the file format was kept for compatibility with older clients.
- Pre-existing failures, not from this lane: TestCarriedDefects_WaveReportRequiresResolution (plans/CARRIED-DEFECTS.tsv versus V6-report.md, fails on 90e1db3); TestIntegration_HotPathWarmWithRealResidentState (Windows); TestV3_HotPathUnchangedWithLedgerResident (Linux). The last two show the same fixed deferral of 592-593 of 2130 deliveries on the base and on HEAD.
- No real Claude Code session was run (not allowed). D17 is proven by unit, route, real-binary e2e and diagnostic runs only.

### Needs the owner

- New bound hookExitReserve = 1.5 s (internal/cli/hookbudget.go): the part of a hook's manifest timeout kept for process start before doHook's first statement, and for the output write, sink closes and exit after it. Derivation: session-start process wall time minus its own first-to-last statement span, under full CPU co-load, was p50 0.06-0.6 s and at most 1.45 s (first execution of a freshly written binary, runs/30); 1.2 s under heavy load (runs/34). If it is too small, a hook that waited out its reply can still be cancelled by the host and the answer is lost. If it is too large, the pre-send budget shrinks and more cold starts spool.
- Derived session-start split: pre-send budget = 15 s (manifest) - 1.5 s (reserve) - 10 s (reply) - 0.25 s (dial) = 3.25 s from doHook's first statement; doneBy = 13.5 s; latestPoll = 13.25 s. Pinned by TestSessionStartBudget_SplitsTheManifestTimeout. Behaviour: when the pre-send step overruns, the reply wait is cut to min(10 s, doneBy - now - dial), and with nothing left the request is spooled without a dial and answered as unanswered ({} or the D9 note). Measured: session-start wall p99 4.1-4.8 s and max 6.5 s at HEAD under co-load; before, 21.2 s in w3 runs/50. Please approve, or choose the alternative: let the poll borrow from the reply wait up to doneBy - dial - compactAnswerBudget (8.25 s). That alternative would have caught the one final-HEAD spool (runs/35), at the cost of a reply wait of 5 s or more instead of 10 s when the daemon is slow to come up.
- Existing constant, new use: ensureRunningPollBound (1.5 s) is now also the least wait a daemon session-start spawned late (or found late on its way) gets, even past the 3.25 s pre-send budget, taken from the reply wait and capped at latestPoll. It was added because the version without it spooled 8 of 60 co-load rows on 4-5 s process-creation stalls (runs/31); with it, 0 of 60 (runs/32).
- Existing constant, new use: spawnLockStaleAfter (10 s) now defines a 'fresh' spawn claim for session-start's EnsureRunning as well as for hooks and MCP, so a crashed spawner can hold spawning off for at most 10 s. Please confirm it as D17a's freshness bound.
- Please ratify the behaviour change from 2f4867f: a daemon accepts connections from the moment it listens and holds each request until its startup (spool replay, checkpoint sweep, publication audit) is done, instead of refusing connections until then. It matches the POSIX behaviour, keeps the replay-first order, and needs no new number.

## Independent review

### review:coldstart: needs-fixes

- **major** `internal/daemon/spawn.go:168-186 (ensureRunning loop), with internal/daemon/daemon.go:719 (Run's removeSpawnLockFile)` — D17a still starts a second daemon in the common case. When EnsureRunning finds a spawn in flight (SpawnInFlight), every later poll tick calls ClaimSpawn again, and it does so BEFORE it probes. The daemon that was announced deletes run/spawn.lock right after it listens (Run: NewServer, Serve, WriteState, removeSpawnLockFile). So on the first tick after that delete, ClaimSpawn finds the lock gone, CreateNew succeeds and EnsureRunning spawns a second daemon, all before the probe that would have found the first one. It avoids this only if one of its 25 ms probes happens to land between the moment the pipe is accepting and the moment the lock is deleted, a gap of about one WriteState (a few ms on an idle machine). Three consequences: (1) the second daemon loses daemon.lock and exits, which is the D17a symptom; (2) session-start pays for another spawnDetached (Windows staging plus process creation, which can stall 4-5 s under co-load) inside its 3.25 s pre-send budget; (3) the loser exits before it listens, so the waiter's new claim stays in spawn.lock for 10 s, and the call reports spawned=true for a daemon it did not start. The unit rows miss this because fakeDaemons never deletes spawn.lock the way Run does. The co-load diagnostic likely missed it because WriteState is slow under fsync load, which widens the gap.
  - Evidence: Reviewer probe on a `git archive HEAD` export in scratch (never in the tree). The fresh lock is written as in TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces, the fake daemon is served after fakeDaemonUpAfter+13ms, and removeSpawnLockFile(root) runs 2 ms after that, mimicking Run's listen, then WriteState, then remove. Calling ensureRunning(..., pollBound{after: spawnLockTestBound}, countingSpawner) 20 times gave `duplicate spawns: 20 of 20`. When the timers happened to line up with the ticker (serve at exactly 150 ms), it was 1 of 20. The loop body is `if !spawned { if lock, claim := ipc.ClaimSpawn(...); claim != ipc.SpawnInFlight { spawn(...) ; spawned = true } } ... if ipc.Probe(addr, ...) { return spawned, nil }`.
  - Fix: Once a call has seen SpawnInFlight, it must not treat a lock that is gone as licence to spawn before checking for the daemon. Probe first on every tick and claim only when the probe fails. After an earlier InFlight, also handle a ClaimSpawn that returns SpawnClaimed because the lock disappeared: probe again with the hook connect deadline (hookConnectDeadlineFloor-sized) before spawning, and Release the claim if the probe succeeds. The simplest alternative: after InFlight, spawn only when readSpawnLock reports the lock stale, never when it is gone, and keep polling until the deadline. Add a regression row whose fake daemon deletes spawn.lock just after it starts serving, the way Run does, for both the fresh-lock row and the two-cold-hooks row, with the serve delay off the 25 ms tick grid. It must be red on HEAD.
- **minor** `internal/daemon/spawn.go:80-81 and 179-182 (EnsureRunning doc; deadline set once)` — The fail-safe is described as happening inside the wait, but mostly it cannot. The doc says 'The wait re-checks the lock on every poll, so a claim whose spawner died goes stale inside it and is reclaimed, and this caller spawns after all (fail-safe)'. The lock's freshness window is 10 s, and the poll lasts 1.5 s (EnsureRunning, selftest) or up to preSendBy, 3.25 s (session-start). So a claim that was still fresh when found goes stale within the wait only if it was already about 6.75-10 s old. For a spawner that has just died, for example a hook killed by its host timeout during staging, session-start gives up at its deadline and spools, and its client's lazySpawn also sees the claim in flight. Nothing spawns until some later hook comes after the 10 s. Separately, when the reclaim does happen inside the wait, the deadline is not recomputed (`if deadline.IsZero()` is set only on the first pass). The late self-spawn therefore does not get the ensureRunningPollBound wait that the EnsureRunningUntil doc and the 'poll begins here, after this call's own spawn' comment promise.
  - Evidence: spawn.go:179-182 sets `deadline = bound.deadline(time.Now())` only while deadline is zero. The first pass that returns InFlight fixes the deadline, and a spawn on a later pass leaves it unchanged. TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale proves reclaim only across two separate calls, with the fake clock advanced in between. No row tests a reclaim inside one wait.
  - Fix: Either make the comments match the behaviour: a dead spawner's claim holds spawning off for up to spawnLockStaleAfter, session-start spools in that window, and the next spawner after it reclaims. Or recompute the deadline as max(deadline, bound.deadline(now)) when a spawn happens after an earlier InFlight (still capped at latest). Then add a row for a lock that goes stale mid-wait and is reclaimed and spawned within one call.
- **nit** `internal/cli/hookclient.go:507` — When no time is left, the spool-only branch calls sp.Append directly instead of going through the client's accounting path (appendToSpool). Two effects: l0_spooled, l0_dropped and the B-G degraded histogram are not recorded, and an Append error (the size refusal) is dropped with no Loud. The Resolve-failure branch at :468 already does the same, so it is consistent, but the degraded path now has two routes with different accounting.
  - Evidence: `_ = sp.Append(req)` compared with client.appendToSpool (internal/ipc/client.go:361-386), which counts and Louds.
  - Fix: Log a non-nil Append error, as the Resolve branch's logQuiet does, or expose a client method that spools without dialling so the counters stay uniform.
- **nit** `test/fault/fault.go:811, test/platform/platform.go:1041-1042, test/security/security.go:1030` — These comments still say session-start's EnsureRunning 'has already waited SpawnPollBound'. session-start now polls until its budget instead: preSendBy, or 1.5 s after a late spawn up to latestPoll. The implementer disclosed this but left it unedited. The comments justify each test's bound, so they now state the wrong reason.
  - Evidence: platform.go:1041: 'far longer than daemon.SpawnPollBound on purpose: session-start's own EnsureRunning has already waited that long before returning'.
  - Fix: Reword to 'session-start's EnsureRunningUntil has already polled to its pre-send budget (at least SpawnPollBound after a spawn)'. No bound changes.
- **nit** `commits dc58fad, 1b79f80` — The two evidence commits have no 'Refs: V6-VERIFY, C1.16/D17' footer. The other 11 commits carry it. Earlier docs(closeout) commits in the repo are mixed on this.
  - Evidence: `git log -1 --format=%B` for dc58fad and 1b79f80: no Refs line. No attribution trailers in any commit (good).
  - Fix: Add the Refs footer if the range is squashed or reworded at merge; otherwise accept it as consistent with earlier evidence commits.

## Fix seat (review resolution) — status `done`, head `6e39b89`

### Root cause

Finding 1 (major, CONFIRMED). Once `ensureRunning` had seen SpawnInFlight, it called `ipc.ClaimSpawn` on every poll before it probed. `Run` deletes run/spawn.lock right after it listens and writes state.bin. So the first poll after that delete claimed the lock the daemon had just freed, and spawned a second daemon; the probe that would have found the first one came only after the spawn. The call escaped only if a poll landed between the listen and the delete. The unit rows missed it because their fake daemon never deleted the lock, and the co-load diagnostic missed it because WriteState is slow under load, which widens that gap. Evidence: with the fake daemon changed to delete the lock the way Run does, three existing rows failed 56 of 60 times on the unfixed code (runs/43). With the real binary and no in-test co-load, the prespawned rows at 1b79f80 started two daemons (daemons=2, lockHeld=1) in 10 of 18 rows (runs/50). Finding 2 (minor, CONFIRMED). The deadline was set only once, `if deadline.IsZero()`, on the first pass. A spawn this call made later, after reclaiming a claim that went stale, got only what was left of the wait for the dead spawner's daemon. The doc also claimed the fail-safe happens inside the wait. That holds only for a claim already about 7-10 s old, because the window is 10 s and the wait is 1.5-3.25 s.

### Summary

## Report: w5-coldstart fix seat (V6 close-out, D17)

Both reviewer findings were checked independently and are correct. Each has a failing test first, then a fix, in its own commit on closeout/w5-coldstart. HEAD is 6e39b89, built on the implementer's 1b79f80.

### What changed
- **5dd108e fix(daemon)** (`internal/daemon/spawn.go`, `spawn_lock_test.go`, `test/guards/sealresidualreport_test.go`, `docs/architecture.md`, `docs/troubleshooting.md`)
  - Every poll in `ensureRunning` now dials first and claims only when the dial fails.
  - Any claim (SpawnClaimed or SpawnUnclaimable) is followed by one more dial before the spawn, bounded by the new constant `spawnClaimDialTimeout` = 250 ms. If a daemon answers, the claim is released and the call returns (false, nil).
  - Test fixture: `fakeDaemons.serve` now works in Run's order: listen, start serving, then delete spawn.lock. A second fake daemon for the same root exits without listening or touching the lock, like a real one that loses daemon.lock. `fakeDaemonUpAfter` moved from 150 ms to 163 ms, off the 25 ms poll grid.
  - New row `TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn`. A clock hook brings the daemon up and deletes the lock between the failed dial and the claim, so this row is deterministic.
  - The guard's exemption reason for `ensureRunning` now names both Release paths.
  - Architecture doc: says session-start dials before each claim and again before it spawns.
  - Troubleshooting doc: its "second daemon appears and exits" entry was incomplete. It now names both causes: a spawn that outlived the 10 s window, or a hook whose short dial a busy daemon did not answer.
- **639e6d5 fix(daemon)**
  - After any spawn this call makes, the deadline is recomputed as `bound.deadline(time.Now())`. This can only move it later, and it stays capped at `latest`.
  - The EnsureRunning, EnsureRunningUntil and pollBound docs now say what actually happens. A dead spawner's claim holds spawning off for up to 10 s. If it goes stale inside the wait, this call reclaims it and spawns, and that daemon gets its own wait. Otherwise the call returns ErrNotFound (session-start spools) and the first spawner after that reclaims the lock.
  - Architecture doc updated to match.
  - New row `TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed`. It ages the claim at 300 ms into a 1 s wait, then the spawn stalls for 1.3 s. It does not depend on timing to go red: the stalled spawn always ends after the old deadline.
- **6e39b89 docs(closeout)**: evidence under `plans/sdd/V6-closeout/w5-coldstart/runs/` (42-56, `linux/`, `zz_fixseat_instrument_after.py.txt`).

### Review resolution
- **Finding 1 (major): D17a still starts a second daemon** → **Fixed in 5dd108e.**
  - Red, unit: the three Run-like rows failed 56 of 60 at 1b79f80 (runs/43). The five review rows all failed (runs/42).
  - Green, unit: 100 of 100 (runs/45), plus all 11 spawn-lock rows ×5 (runs/47).
  - Red, real binary, prespawned rows, no in-test co-load: 10 of 18 rows had daemons=2 (runs/50).
  - Green, real binary, same rows: 0 of 18 (runs/51).
  - The reviewer's three consequences go away with it: the duplicate spawn, session-start paying for a second spawn inside its 3.25 s pre-send budget, and a false spawned=true with a leftover claim.
- **Finding 2 (minor): fail-safe mostly cannot happen inside the wait, and the deadline is not recomputed** → **Fixed in 639e6d5, both halves.**
  - The doc now matches the behaviour, and the deadline is recomputed after the spawn.
  - Red on 5dd108e: 5 of 5 (runs/46). Green: ×5 (runs/47) and ×20 on Linux.

### Commands and results
| Command | Result |
|---|---|
| `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces\|TestEnsureRunning_TwoColdHooksStartOneDaemon\|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits)$' -count=20 -v` on the unfixed code | 56 FAIL, 4 PASS, exit=1 (runs/43) |
| `go test ./internal/daemon/ -run '^TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed$' -count=5 -v` at 5dd108e | 5 of 5 FAIL, exit=1 (runs/46) |
| `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces\|TestEnsureRunning_TwoColdHooksStartOneDaemon\|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits\|TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn\|TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed)$' -count=20 -v` at HEAD | 100 of 100 PASS (runs/45) |
| The same with all 11 `TestEnsureRunning_`/`TestEnsureRunningUntil_` rows, `-count=5` | 55 of 55 PASS (runs/47) |
| `go test ./internal/daemon/ -count=1 -timeout=30m` | ok, 231 s (runs/48) |
| `go test ./test/guards/ -count=1 -v` | FAIL only in `TestCarriedDefects_WaveReportRequiresResolution`. This also fails on base 90e1db3 (runs/39); nothing else fails (runs/55) |
| `go test ./test/guards/ -run '^(TestGuard_AReleasedLockReportsItsSealResidual\|TestGuard_TheSealResidualScannerSeesBothCallShapes)$'` | ok |
| The same 28-test focused e2e set the implementer ran in runs/17 | all PASS, 173 s (runs/56) |
| `go test ./test/docs` | ok (runs/49) |
| `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` | all 8 PASS (runs/54) |
| `devtool fmt-check`; `go vet` on internal/daemon and test/guards, Windows and GOOS=linux | clean |
| Linux gate at 639e6d5, non-root, -race: `--count 20 --run '^(TestEnsureRunning_\|TestEnsureRunningUntil_)' -- ./internal/daemon` | pass=260, fail=0 |
| Linux gate at 639e6d5, non-root, -race: full `./internal/daemon` | pass=1512, fail=0, skip=1. The skip is the Windows-only `TestDrainWindowsOpenBlobRetainsCleanupIntent`, which the implementer's run also skipped |

**Cold-start diagnostic.** The temporary diagnostic (`runs/zz_w5_coldstart_diag_test.go.txt` with its w3 helpers) was copied into scratch exports of 1b79f80 and 639e6d5, instrumented with `zz_instrument_fixed.py.txt` plus the new `zz_fixseat_instrument_after.py.txt`, and never committed.
- Prespawned rows, 6 per source, no in-test co-load: multi-daemon 10/18 before, 0/18 after, and no row spooled either time. Session-start wall time p50 was 481 ms before and 469 ms after (runs/50, 51, 53).
- All 60 rows under CPU co-load at 639e6d5 (runs/52, 53): 4 spooled, 0 multi-daemon, 0 NOUP. Session-start wall time p50 2.66 s, p99 12.9 s, max 13.57 s, still inside the 15 s timeout.
- That run is **not comparable** to runs/35: the identical configuration took 822 s against 364 s, because the machine was much busier with the other workstreams.
- The slow rows are all staged-cold, where staging alone took up to 7.4 s. In all 6 printed timelines, `ensure-claimed` to `stage-start` is 0 ms, so the new dial costs nothing when no daemon exists.
- I did not rerun the before-tree under co-load, so there is no same-conditions co-load comparison.

### Criterion changes
- `fakeDaemonUpAfter` went from 150 ms to 163 ms. It is a fixture delay moved off the poll grid so no row depends on tick alignment; the reviewer asked for this. No assertion was loosened.
- The fake daemon now deletes spawn.lock the way Run does, which makes every row stricter: three existing rows went red on the old code.
- The guard's exemption text for `ensureRunning` was extended to name the second Release. The function is still listed with its reason, and the guard still counts every Release caller.

### Open items
1. **Hook and MCP lazy spawn, pre-existing and unchanged.** `ipc.client.lazySpawn` claims once after a failed connect and spawns without dialling again. The case that matters: a live but busy daemon misses a hot-path hook's short connect deadline, and it has long since deleted its lock. The hook then spawns a daemon that exits at daemon.lock. Fixing it needs a longer dial on the hook failure path, which is a latency and design decision beyond this review. The troubleshooting entry now names this case.
2. **Two daemons started together.** Two processes that reclaim the same stale lock at the same instant can still both spawn. This is documented, and the singleton lock turns the second away.
3. `TestCarriedDefects_WaveReportRequiresResolution` also fails on base 90e1db3; it is not caused by this branch.

### Commits

- 5dd108e fix(daemon): dial before a spawn claim and again after one
- 639e6d5 fix(daemon): give a spawn after a stale claim its own wait
- 6e39b89 docs(closeout): record the w5-coldstart fix-seat runs

### Tests

- `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces|TestEnsureRunning_TwoColdHooksStartOneDaemon|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits)$' -count=20 -v (production code at 1b79f80, new fixture)` — RED as intended: 56 FAIL / 4 PASS, exit=1 (runs/43) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces|TestEnsureRunning_TwoColdHooksStartOneDaemon|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits|TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn|TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed)$' -count=1 -v (production code at 1b79f80)` — RED as intended: 5 of 5 FAIL, exit=1 (runs/42) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed$' -count=5 -v (at 5dd108e)` — RED as intended: 5 of 5 FAIL, exit=1 (runs/46)
- `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces|TestEnsureRunning_TwoColdHooksStartOneDaemon|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits|TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn|TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed)$' -count=20 -v (HEAD)` — 100/100 PASS, exit=0 (runs/45) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^(TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces|TestEnsureRunning_TwoColdHooksStartOneDaemon|TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits|TestEnsureRunning_ItsSpawnHoldsOffALazySpawn|TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale|TestEnsureRunning_ReleasesItsClaimWhenTheSpawnFails|TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn|TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed|TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound|TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait|TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon)$' -count=5 -v` — 55/55 PASS, exit=0 (runs/47) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -count=1 -timeout=30m` — ok 231s, exit=0 (runs/48)
- `go test ./test/guards/ -count=1 -v` — exit=1; the only failure is TestCarriedDefects_WaveReportRequiresResolution, which also fails on base 90e1db3 (runs/39); nothing else fails (runs/55)
- `go test ./test/guards/ -run '^(TestGuard_AReleasedLockReportsItsSealResidual|TestGuard_TheSealResidualScannerSeesBothCallShapes)$' -count=1` — ok <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/e2e/ -run <the 28-test focused set of runs/17, e.g. TestE2ELazySpawn, TestE2E_SessionStartLatency, TestE2E_SpooledSessionStartNeverDegradesTheProject> -count=1 -v` — all 28 PASS, ok 173s, exit=0 (runs/56) <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test ./test/docs -count=1` — ok, exit=0 (runs/49)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS, exit=0 (runs/54)
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon/ ./test/guards/ (Windows and GOOS=linux)` — clean
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-coldstart --repo C:/.../qompack-cx-w5-coldstart --out C:/.../runs/linux 639e6d5 review-rows-x20 --count 20 --timeout 60m --run '^(TestEnsureRunning_|TestEnsureRunningUntil_)' -- ./internal/daemon` — non-root, -race: pass=260 fail=0, exit=0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-coldstart --repo C:/.../qompack-cx-w5-coldstart --out C:/.../runs/linux 639e6d5 daemon-race --timeout 60m -- ./internal/daemon` — non-root, -race: pass=1512 fail=0 skip=1 (the Windows-only TestDrainWindowsOpenBlobRetainsCleanupIntent), exit=0
- `temporary cold-start diagnostic (runs/zz_w5_coldstart_diag_test.go.txt, in scratch exports of 1b79f80 and 639e6d5, never committed), ZZ_MODES=prespawned ZZ_ITERS=6, no in-test co-load` — multi-daemon rows 10/18 before vs 0/18 after, 0 spooled either way (runs/50, 51, 53)
- `same diagnostic, all modes, ZZ_CPU_COLOAD=1 ZZ_ITERS=5, at 639e6d5` — 60 rows: 4 spooled, 0 multi-daemon, 0 NOUP; session-start wall p50 2.66 s, p99 12.9 s, max 13.57 s (<15 s). The machine was far busier than in runs/35 (822 s vs 364 s), so not comparable to it (runs/52, 53)

### Criterion changes

- fakeDaemonUpAfter (internal/daemon/spawn_lock_test.go) moved from 150 ms to 163 ms, a fixture start-up delay taken off ensureRunning's 25 ms poll grid so that no row depends on whether a poll lands just before or after the daemon comes up. The reviewer asked for this. No assertion was loosened.
- The fakeDaemons fixture now deletes run/spawn.lock after it starts serving, in Run's order, and a second fake daemon for the same root exits without listening or touching the lock. This makes rows stricter, not looser: three existing rows went red on the old code (56/60, runs/43).
- test/guards sealresidualreport_test.go: the exemption reason for internal/daemon/spawn.go ensureRunning now also names the Release after a daemon answers the post-claim dial. The function is still listed with its reason, and the guard still accounts for every Release caller.

### Open issues

- Hook and MCP lazy spawn (ipc.client.lazySpawn), pre-existing and unchanged: it claims once after a failed connect and spawns without dialling again. A live but busy daemon that misses a hot-path hook's short connect deadline, and has long since deleted its spawn.lock, gets a duplicate spawned that exits at daemon.lock. Fixing it needs a longer dial on the hook failure path, which is a latency and design decision outside this review. docs/troubleshooting.md now names this case.
- Two processes that reclaim the same stale spawn.lock at the same instant can still both spawn. This is documented, and the singleton daemon.lock turns the second away.
- test/guards TestCarriedDefects_WaveReportRequiresResolution fails (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3). It fails the same way on base 90e1db3 (runs/39); it is not caused by this branch.
- The CPU co-load diagnostic at 639e6d5 (runs/52) ran while the machine was much busier than for runs/35, so its spool rate and wall times cannot be compared with the implementer's. The before-tree was not rerun under the same co-load, so there is no same-conditions co-load comparison. The prespawned rows without in-test co-load are the before/after comparison for this fix (runs/50 vs 51).
- Scratch exports used for the diagnostic are left in scratchpad/w5/coldstart/fixseat-diag/ (before/, after/) so the runs can be reproduced; nothing from them is in the tree.

### Needs the owner

- NEW: spawnClaimDialTimeout = 250 ms (internal/daemon/spawn.go). This is the dial EnsureRunning makes after it has claimed run/spawn.lock and before it spawns. Derivation: the same value as internal/cli's hookConnectDeadlineFloor, for the same race; the daemon package cannot import cli. A daemon that has just listened and deleted the lock may not have its next accept posted yet. A Windows pipe then answers ERROR_PIPE_BUSY, and go-winio retries every 10 ms, so 20 ms (ensureRunningDialTimeout) buys only 2 attempts; 250 ms buys about 25. It costs nothing when no daemon exists, because a missing pipe or socket fails at once: 0 ms from claim to stage-start in all 6 co-load timelines (runs/52). If too short, a daemon that is up but slow to accept is missed and a duplicate spawned, which loses daemon.lock and exits after costing session-start a spawn inside its 3.25 s pre-send budget. If too long, a hung daemon (a pipe that exists but never accepts) delays its replacement by that much, inside the pre-send budget.
- CARRIED: New bound hookExitReserve = 1.5 s (internal/cli/hookbudget.go). It is the part of a hook's manifest timeout kept for process start before doHook's first statement, and for the output write, sink closes and exit after it. Derivation: session-start process wall time minus its own first-to-last statement span, under full CPU co-load, was p50 0.06-0.6 s and at most 1.45 s (first execution of a freshly written binary, runs/30); 1.2 s under heavy load (runs/34). If too small, a hook that waited out its reply can still be cancelled by the host and the answer is lost. If too large, the pre-send budget shrinks and more cold starts spool.
- CARRIED: The derived session-start split is pre-send budget = 15 s (manifest) - 1.5 s (reserve) - 10 s (reply) - 0.25 s (dial) = 3.25 s from doHook's first statement, with doneBy = 13.5 s and latestPoll = 13.25 s; pinned by TestSessionStartBudget_SplitsTheManifestTimeout. When the pre-send step overruns, the reply wait is cut to min(10 s, doneBy - now - dial); with nothing left, the request is spooled without a dial and answered as unanswered ({} or the D9 note). Please approve it, or choose the alternative: let the poll borrow from the reply wait up to doneBy - dial - compactAnswerBudget (8.25 s). Under the fix seat's heavier-load co-load run the maximum whole-hook time was 13.57 s, still inside 15 s (runs/52).
- CARRIED: existing constant, new use. ensureRunningPollBound (1.5 s) is also the least wait a daemon session-start spawned late, or found late on its way, gets, even past the 3.25 s pre-send budget. It is taken from the reply wait and capped at latestPoll. Since 639e6d5 this is counted from this call's own spawn even after it first waited on another spawner's claim that then went stale.
- CARRIED: existing constant, new use. spawnLockStaleAfter (10 s) now defines a 'fresh' spawn claim for session-start's EnsureRunning as well as for hooks and MCP, so a crashed spawner can hold spawning off for at most 10 s. As corrected in 639e6d5, a claim whose spawner has just died makes session-start spool when its wait ends first, and the first spawner after 10 s reclaims the lock. Please confirm it as D17a's freshness bound.
- CARRIED: please ratify the behaviour change from 2f4867f. A daemon now accepts connections from the moment it listens and holds each request until its startup (spool replay, checkpoint sweep, publication audit) is done, instead of refusing connections until then. It matches the POSIX behaviour, keeps the replay-first order, and needs no new number.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



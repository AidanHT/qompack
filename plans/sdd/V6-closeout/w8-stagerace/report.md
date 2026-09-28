# w8-stagerace: concurrent Windows staging no longer fails or deletes a correct copy

Branch `closeout/w8-stagerace`. Workflow `wf_50e94c35-219`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> **Coordinator note: SHA map.** The fix seat reworded commits to add Refs footers, so SHAs the implementer quotes are no longer on the branch. Each maps to a commit with the identical tree: `01af8364` → `301ad6de`, `0952b3fe` → `39c8de69`, `324018f1` → `0a4337bb`, `e702b599` → `8969b143` (the Linux artifact directory named `...-e702b59-...` ran that tree). `d6578301` was folded into `593c80e2`. `a69255bc` is the coordinator's ledger commit on verify/v6 (D35), reachable once integration is merged there at the freeze.

## Implementer — status `done`, head `0952b3fe`

### Root cause

A product race on Windows, not the virus scanner. The spawner whose os.Rename (MoveFileEx) has just put the staged copy in place still holds the renamed file with DELETE access until MoveFileEx closes the handle. A losing spawner checked that copy through os.Open, which does not share delete, got ERROR_SHARING_VIOLATION, and reported a correct copy as a failed install. Its first check also deleted any copy it could not read as if it had been tampered with. The fix reads through the delete-sharing paths.OpenShared and removes only a copy shown to be wrong.

### Summary

w8-stagerace report (branch closeout/w8-stagerace, base 17a42f6, code-final e702b59, HEAD 0952b3fe = e702b59 plus evidence logs only).

RESUME REVIEW. I read the digest, all four earlier commits and the untracked runs/ evidence. The earlier seat's run logs 00-07 and 10-13 finished before the pause. The only run the pause killed was the Linux gate at 01af836, whose /work/cx-w8-stagerace-w8-touched-01af836-* directory produced no artifacts. It is not used as evidence and I left the directory in place. I checked each commit against the code, the logs and the owner decisions (D10, D27, D35(c)) and kept all four without rewriting. I added one characterization row and the evidence commit. Every run I rely on below was repeated at e702b59.

(1) ROOT CAUSE: a real product race on Windows, not the virus scanner.
- Signature: the rename fails with "Access is denied", because the target exists and is sealed read-only, so replacing it is refused. The fallback check of the existing copy then fails with "The process cannot access the file because it is being used by another process" (ERROR_SHARING_VIOLATION).
- Mechanism: the spawner whose os.Rename (MoveFileEx) has just put the copy in place still holds the renamed file with DELETE access until MoveFileEx closes that handle. A losing spawner checks the copy through fileSHA256's os.Open. Go's os.Open does not share delete, so Windows refuses it while that handle is open. The loser then reports a correct copy as a failed install.
- A second defect: stageBinary's first check treated any error other than "file missing" as tampering, and deleted the copy. So a correct copy that simply could not be read at that moment was removed. In one loaded failure a third spawner's final check then found no file at all ("The system cannot find the file specified", 1 of the 20 failures in run 05).

Evidence:
- Temporary diagnostic (runs/diag-stagerace-windows_test.go.txt, never committed as a test): at each failure it asked the kernel which processes held the file (FileProcessIdsUsingFileInformation). It found only the test process itself, or no holder once the window had closed. No MsMpEng or other scanner process appeared (runs/diag-holders-and-serialized.log).
- Making the install step run one spawner at a time inside the process removed the failures: 0 in 2400 spawner calls, against 6 in 2400 without it.
- Checking the copy through paths.OpenShared, which shares delete, removed them too: 0 in 7200, against 4 in 2400 (runs/diag-openshared-verify.log). Sharing delete can only help if the conflicting handle holds DELETE access. A scanner does not open files for deletion; a renamer does.
- Limit on this claim: Defender real-time protection is on here, but reading its exclusions needs admin rights, so I cannot rule out that D32's exclusion covers %TEMP%. What I can say is precise: the failure reproduces deterministically with no scanner involved (TestStageBinary_VerifiesACopyItsRenamerStillHolds, a handle shaped like the renamer's: DELETE access, full sharing), and the fix removes it.

Reproduction under my own bounded load at e702b59 (8 copies of `yes` named w8stageload.exe, each capped by `timeout 1200`, stopped by path right after the runs):
- The same `go test -run '^TestStageBinary_ConcurrentSpawnersAgree$'` row, -test.count=1500, run on test binaries built from the base and from the head.
- Base: 35 of 1500 failed, every one with the exact failure first seen in the loaded run (runs/08).
- Head: 0 of 1500 (runs/09).
- The earlier seat's runs agree: base 2/1000 unloaded and 20/1500 loaded; fixed 0/1000 and 0/1500.

What the fix changes and what the product sees:
- fa9bb7a: fileSHA256 reads through paths.OpenShared. The guard inventory moves fileSHA256 from ordinaryReads to sharedReaders, so the change cannot silently revert.
- d1a0d8b: only a copy shown to be wrong (errStagedMismatch, or the new errStagedNotRegular) is removed. A copy that cannot be read returns an error and is kept, so that one spawn starts the daemon from the plugin binary, the existing fallback that is logged as a warning and reported by the daemon. security.md, architecture.md and troubleshooting.md now say this.
- Before the fix the spawn itself never failed: daemonProgram falls back to self. The real harm was a daemon running from the plugin binary, which reopens the D10 hazard of pinning the plugin directory, plus deletion of a correct copy another spawner was about to start.
- New row e702b59, TestStageBinary_StartsACopyItsRenamerStillHolds: a copy that has been checked can be started while the renamer's DELETE handle is open, because CreateProcess's open of the executable shares delete. This is a characterization row, not a red-first test: it passes on base spawn_stage.go too, which I checked. A temporary negative control (runs/diag-exec-nonshared-holder.log) showed that a holder not sharing read does refuse the start, so the row is able to fail.
- No retry loop and no new bound were needed, so nothing new needs owner approval.

(2) NITS.
- 324018f: the why strings for readSpawnLock, removeSpawnLockIf and e2eSpawnInFlight now name daemon.removeSpawnClaim from Lock.Release as a deleter (the winning daemon, the daemon command's writer lease, fsck, delivery-seal; D27). Only the strings changed; the rows and the check are untouched.
- 01af836, docs/troubleshooting.md §7: the entry for a second "qompack daemon" that appears and exits at once now lists three causes, adding D27's (a lock holder that lets go while another spawn is starting gives that spawn's claim back; one extra daemon starts and daemon.lock turns it away).
- 01af836, docs/cannot-do.md: new limit "A session's end can wait for the next session when the daemon is stopping", recorded under D35(c). A SessionEnd flush that arrives after the listener has closed but while daemon.lock is still held spawns a daemon that loses. The session's end work waits in the client spool and is replayed by the next daemon's drain, not lost. I checked this against handleFlush, the client's spoolAndReturn and endDrainedFlush, and checked the link anchors.

COMMANDS AND RESULTS (Windows unless noted, all at e702b59, under co-load from other workstreams):
- Row A/B: listed under (1).
- Full packages (runs/20-24): `go test -count=1 ./internal/daemon` ok (459 s); `./internal/ipc` ok; `./internal/cli` ok; `./test/docs` ok.
- `./test/guards` failed only on TestCarriedDefects_WaveReportRequiresResolution rows SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1 and SP20-D2. These are the known carried-defect rows the checklist expects at 17a42f6; nothing else failed.
- `go run ./tools/devtool fmt-check` exit 0. `go vet` on internal/daemon and test/guards passes for Windows and GOOS=linux.
- Lint subset (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns) exit 0, and runpatterns plus docmarkers passed again after the evidence commit.
- Linux: `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8-stagerace ... e702b59 w8-touched -- ./internal/daemon ./test/guards ./test/docs`, non-root (uid 10001), -race. internal/daemon passed 1542, failed 0, skipped 1 (the skip is already in the package, not one I added). test/docs passed 19. test/guards failed only the same seven carried-defect rows. Gate exit 1 is from those rows alone.
- Artifacts: runs/linux/cx-w8-stagerace-w8-touched-e702b59-20260928T131401Z-artifacts, with test.jsonl gzipped.

Criterion changes: none. No assertion was weakened, no skip or nolint added, and no timeout or budget changed.

Processes: I started only the 8 w8stageload.exe processes and all 8 were stopped (0 left). No background diagnostics are running.

### Commits

- fa9bb7a7 fix(daemon): verify staged copies through delete-sharing reads (earlier seat; reviewed, kept)
- d1a0d8b7 fix(daemon): keep a staged copy that cannot be read (earlier seat; reviewed, kept)
- 324018f1 test(guards): name Lock.Release among spawn.lock's deleters (earlier seat; reviewed, kept)
- 01af8364 docs(troubleshooting): add D27 cause and the stopping-daemon limit (earlier seat; reviewed, kept)
- e702b599 test(daemon): start a staged copy its renamer still holds (this seat)
- 0952b3fe test(daemon): record the w8-stagerace evidence runs (this seat; evidence only, no code)

### Tests

- `go test -run '^TestStageBinary_ConcurrentSpawnersAgree$' -count=1500 (base binary, 8 bounded busy processes)` — FAIL 35/1500 with the exact CI signature (runs/08-base-row-count1500-load8-rerun.log)
- `go test -run '^TestStageBinary_ConcurrentSpawnersAgree$' -count=1500 (e702b59 binary, same load)` — PASS 0/1500 failures (runs/09-head-e702b59-row-count1500-load8.log)
- `go test -count=1 -run '^TestStageBinary_VerifiesACopyItsRenamerStillHolds$' ./internal/daemon` — red at base (earlier seat, runs/01), green at head (runs/02, and in the full package run at e702b59)
- `go test -count=1 -run '^TestStageBinary_NeverRemovesACopyItCannotRead$' ./internal/daemon` — red at base (runs/01), green at head <!-- runpatterns: the implementer's name for the row the fix seat renamed TestStageBinary_NeverRemovesACopyHeldOpen (see criterion changes); the command ran as quoted at the implementer's head -->
- `go test -count=3 -run '^TestStageBinary_StartsACopyItsRenamerStillHolds$' ./internal/daemon` — PASS at head, and PASS at base (characterization row)
- `go test -count=1 -timeout=60m ./internal/daemon (Windows, e702b59)` — ok 459s
- `go test -count=1 ./internal/ipc (Windows, e702b59)` — ok
- `go test -count=1 ./internal/cli (Windows, e702b59)` — ok
- `go test -count=1 ./test/docs (Windows, e702b59)` — ok
- `go test -count=1 ./test/guards (Windows, e702b59)` — FAIL only the expected carried-defect rows of TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8-stagerace e702b59 w8-touched -- ./internal/daemon ./test/guards ./test/docs` — daemon PASS (1542/0/1 skip), docs PASS, guards only the same carried-defect rows; non-root, -race
- `go run ./tools/devtool fmt-check; go vet (windows + GOOS=linux) ./internal/daemon ./test/guards` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Open issues

- The claim that no virus scanner was involved rests on the kernel's holder list and on which handle shape the fix can cure. Defender's exclusion list cannot be read without admin rights, so whether D32's exclusion covers %TEMP% on this host is unknown. The failure reproduces deterministically without any scanner.
- No product tolerance was added for a scanner that holds the freshly written .stage temp copy without sharing delete, which would make the rename fail. It was never observed. If it happens, staging fails, that spawn falls back to the plugin binary (logged as a warning), and the next spawn stages again.
- test/guards still fails only the carried-defect rows of TestCarriedDefects_WaveReportRequiresResolution. This was expected at 17a42f6 and is outside this workstream.
- Nothing was changed for it: readSpawnLock's why string does not name the claimant's own SpawnLock.Release after a failed spawn (ipc/client.go lazySpawn), which is another cross-process remover of run/spawn.lock. That goes beyond the nit's scope, and it was already left out before this change.

## Independent review

### review:stagerace: needs-fixes

- **minor** `internal/daemon/spawn_stage.go:196-212 (verifyStaged) with :257 (fileSHA256) and :217-234 (pruneStaged)` — Because fileSHA256 now reads through a delete-sharing handle, verification is no longer tied to the name for the length of the hash. The base code's os.Open handle refused removal or rename of the copy while it was being hashed; the removed ordinaryReads row said so: "either one refused by this read fails only that removal". Now a remover wins while the hash is running, and verifyStaged still returns nil for bytes read from a file that is no longer at target. The real case is pruneStaged from a spawner of a different plugin version. During a plugin update, a v2 hook's first staging can prune v1's directory while a v1 hook is hashing v1's copy. The v1 spawner then returns target, and cmd.Start in spawnDetached fails (file not found or delete pending), so the spawn fails outright. Before this change that could only happen in the microseconds between closing the hash handle and CreateProcess; now it spans the whole hash of a multi-MB exe (tens of ms). The same widening applies to a same-user swap of the file under the hash; the threat model accepts that attacker, but it now has a larger window.
  - Evidence: paths.OpenShared's doc: FILE_SHARE_DELETE 'lets the writer land while this handle is open. The reader keeps seeing the bytes it opened'. The deleted productreads row for fileSHA256 relied on the old read refusing 'another spawner's removal ... and pruneStaged's best-effort removal'. The new sharedReaders why string lists those same removers, but nothing re-checks the name after the hash. spawnDetached (spawn.go ~349-366) has no fallback to self when starting the staged program fails.
  - Fix: In verifyStaged, hash through a handle you keep, then os.Lstat(target) again and require os.SameFile(handle.Stat(), lstat). If they differ, return an fs.ErrNotExist-wrapped error so stageBinary re-copies. Add a Windows row: open the copy through the verification path, delete it during the hash (or stub fileSHA256 so pruneStaged runs mid-hash), and assert stageBinary does not return a path that no longer exists. Alternatively, or as well, when cmd.Start of a staged program fails with ErrNotExist, retry once from self.
- **minor** `internal/daemon/spawn_stage.go:122-127 (stageBinary, new 'cannot be read' branch)` — The new keep-and-fall-back branch catches every verification error other than mismatch, not-regular and not-exist, including persistent ones: an ACL that denies read on the copy, or an I/O error partway through the read. Before this change such a copy was removed and staged again, which healed it. Now every later spawn logs the warning and runs the daemon from the plugin binary indefinitely, reopening the D10 hazard of pinning the plugin directory. The docs promise 'the next spawn checks the copy again', which does not help when the error is persistent. The reason for keeping the copy is transient: another spawner may have verified it and be about to start it. That reason only holds for sharing conflicts, because a persistent read failure means no other spawner could have verified the copy either.
  - Evidence: `case !errors.Is(verr, fs.ErrNotExist): return "", fmt.Errorf("daemon: verifying %s: %w", target, verr)`. With OpenShared the only transient reader conflict left is a holder that does not share read, which gives ERROR_SHARING_VIOLATION (or ERROR_LOCK_VIOLATION).
  - Fix: Keep the copy only on windows.ERROR_SHARING_VIOLATION or ERROR_LOCK_VIOLATION, using a small platform helper that is always false off Windows. For every other unreadable-copy error, go back to the old remove-and-restage path. Add a row where a deny-read ACE, or any persistent non-sharing open error, leads to a restage rather than a permanent fallback. Adjust the security.md, architecture.md and troubleshooting.md wording to say 'held open' rather than 'cannot be read'.
- **minor** `commits 324018f1, 01af8364, e702b599, 0952b3fe` — Four of the six commits have no Refs footer. In this close-out, code, test and docs commits carry `Refs: V6-VERIFY, <cid>` (fa9bb7a7 and d1a0d8b7 on this branch do, as do w7's 2cd0a644, c1f1a3a3 and 5a5b018a). 324018f (test(guards)), 01af836 (docs) and e702b59 (test(daemon)) are product and test changes that need one.
  - Evidence: `git log --format='%h %(trailers)' 17a42f6..HEAD` shows empty trailers for 0952b3fe, e702b599, 01af8364 and 324018f1, and 'Refs: V6-VERIFY, C1.17' only for d1a0d8b7 and fa9bb7a7.
  - Fix: Before integration, reword the commits to add `Refs: V6-VERIFY, C1.17` (for 324018f and 01af836, `Refs: V6-VERIFY, C1.17/D27/D35`). Do not add any attribution trailers.
- **nit** `docs/cannot-do.md:390` — The new limit's 'Recorded at' cites `plans/V6-CLOSEOUT-CHECKLIST.md` D35(c). That checklist on this branch and its base 17a42f6 has only D1-D8. D35 exists only in the coordinator ledger (commit a69255bc, which is not an ancestor of 17a42f6), so a reader of the shipped tree cannot resolve the citation. troubleshooting.md's 'V6 close-out D27' has the same problem.
  - Evidence: `grep -on '^| D[0-9]*' plans/V6-CLOSEOUT-CHECKLIST.md` stops at D8. `git merge-base --is-ancestor a69255bc 17a42f6` says it is not an ancestor.
  - Fix: Also cite a document that is in the tree and states the ruling: the w7-spawnclaim report's open-issues line (already cited) together with this workstream's report. Alternatively, ensure the coordinator ledger with D27 and D35 is merged into the integration branch before this lands.
- **nit** `test/guards/sharedreaders_test.go:59-62 (readSpawnLock why string)` — The why string still leaves out a cross-process remover of run/spawn.lock: the claimant's own ipc.SpawnLock.Release after a failed spawn (the lazySpawn and ensureRunningWith paths). The implementer disclosed this as out of scope, but the nit's intent was to list the deleters completely.
  - Evidence: Implementer open_issues item 4. The row now names removeSpawnLockFile, the stale reclaim and Lock.Release's removeSpawnClaim only.
  - Fix: Add 'and the claimant's own SpawnLock.Release when its spawn fails (ipc ensureRunningWith)' to the readSpawnLock and removeSpawnLockIf why strings.

## Fix seat (review resolution) — status `done`, head `2ce18f8a`

### Root cause

Finding 1: after fa9bb7a, fileSHA256 read the staged copy through a handle that shares delete, so a removal could land while the hash ran and verifyStaged returned nil for a file no longer at the target. Fixed with a post-hash os.SameFile identity check. That check then exposed a second cause on Linux: os.Rename there overwrites an existing target, so a spawner that lost the install replaced the winner's copy mid-hash. Fixed with a no-replace install (rename on Windows, which the read-only seal already refuses; os.Link elsewhere). Finding 2: d1a0d8b kept a staged copy on any read error, including persistent ones such as a denying ACE, which pinned every later spawn to the plugin binary. Now only ERROR_SHARING_VIOLATION or ERROR_LOCK_VIOLATION keeps the copy. Finding 3: four commits had no Refs footer.

### Summary

FIX seat for w8-stagerace. Branch closeout/w8-stagerace, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8-stagerace, HEAD 2ce18f8a on 17a42f6. The working tree is clean.

## Review resolution

**Finding 1 (minor): verification not tied to the name for the length of the hash. CONFIRMED and fixed in e8587965, with a follow-on fix in 593c80e2.**
- **Red first.** I added a test seam, `verifyStagedThen(target, sum, afterHash func())`, and removed the copy between the hash and the final checks. On Windows, `verifyStaged` returned nil for a file that no longer existed. The same happened when the copy was removed and a new file was written in its place. Evidence: `runs/30-red-review-findings-windows.log`.
- **Fix.** `verifyStagedThen` hashes through a handle it keeps (`paths.OpenShared`, as before). Then `stillFiledAt` requires `os.SameFile(handle.Stat(), os.Lstat(target))`.
  - If the copy was removed or replaced, it returns `errStagedMoved`, which wraps `fs.ErrNotExist`. `stageBinary` then makes and verifies its own copy.
  - `fileSHA256` now only ever hashes the running plugin binary. It is back to `os.Open` and back in `ordinaryReads`, with a why string. The `sharedReaders` row moved to `verifyStagedThen`, with its why string updated.
- **Regression it caused, now fixed (593c80e2).** The Linux gate at f96e0b8 then failed `TestStageBinary_ConcurrentSpawnersAgree` with "was replaced".
  - Root cause: on Linux, `os.Rename` overwrites an existing target. A spawner that lost the install replaced the winner's copy with its own equal bytes while another spawner was hashing it.
  - Failing row, cross-platform: `TestCopyStaged_NeverReplacesAnInstalledCopy`. On Linux at d657830 (test-only, later folded into 593c80e2) it failed 50 of 50, and the concurrency row also failed 50 of 50.
  - Fix: `installStaged` never replaces a file that is already in place. On Windows it stays a rename, which the copy's read-only seal already refuses (that is the original "Access is denied"). Elsewhere it is `os.Link`, which fails with EEXIST when the target exists.
  - `removeStagedTemp` drops the temporary name. It clears the seal only on Windows; elsewhere the temporary name can be a second link to the installed copy, and clearing it would unseal that copy.
  - Production staging is Windows-only (`stagingEnabled`), so off Windows this only changes what the tests exercise.
- **Not done: the reviewer's alternative (start again from self when `cmd.Start` of a staged program fails).** A window of microseconds between the identity check and CreateProcess remains. It was there before the implementer's change too. Listed as an open item.

**Finding 2 (minor): the keep-and-fall-back branch caught persistent read errors. CONFIRMED and fixed in f96e0b88.**
- **Red first.** New row `TestStageBinary_RestagesACopyItCanNeverRead`. The staged copy gets a DACL that denies FILE_READ_DATA to Everyone (SDDL `D:P(D;;0x1;;;WD)(A;;FA;;;WD)`). The implementer's code returned "Access is denied" and fell back to the plugin binary on every spawn. Evidence: `runs/30`.
- **Fix.** New `heldOpenElsewhere(err)` is true only for Errno 32 or 33 (ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION) and only when `runtime.GOOS` is windows.
  - The constants are named locally, not taken from `x/sys/windows`: §2.5 closes the dependency list, and `internal/ipc/state.go` does the same.
  - Only those two errors keep the copy. Every other failure other than not-exist goes back to remove-and-restage.
- **Tests.** The held-open row is renamed `TestStageBinary_NeverRemovesACopyHeldOpen` and now also asserts `ErrorIs ERROR_SHARING_VIOLATION`.
- **Docs.** `security.md` and `architecture.md` now say "held open without sharing read" instead of "cannot be read", and mention the post-hash identity check. `troubleshooting.md` already said "held open".

**Finding 3 (minor): four commits had no Refs footer. CONFIRMED and fixed.**
- I rewrote messages on my own branch only, with `filter-branch --msg-filter` over `d1a0d8b7..HEAD`, then deleted the backup ref.
  - 324018f1 is now 0a4337bb and 01af8364 is now 301ad6de; both carry `Refs: V6-VERIFY, C1.17/D27/D35`.
  - e702b599 is now 8969b143 and 0952b3fe is now 39c8de69; both carry `Refs: V6-VERIFY, C1.17`.
  - Every tree is byte-identical to the old one (checked with `rev-parse ^{tree}`).
  - Every commit message passes the commit-msg hook. I also checked that the hook rejects a Co-Authored-By trailer.
- Caveat: the implementer's evidence (39c8de69's message, runs 08/09/20-24, and `linux/…-w8-touched-e702b59-…`) quotes e702b59. That SHA is no longer reachable; the same tree is 8969b143. The d657830 in 593c80e2's message is also unreachable, by design (folded). All other quoted SHAs are reachable (checked with `merge-base --is-ancestor`).

## Commands and results
- **Windows focused rows.** `go test ./internal/daemon -run '^(TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed|TestStageBinary_RestagesACopyItCanNeverRead|TestStageBinary_NeverRemovesACopyHeldOpen)$' -count=1 -v` failed on the pre-fix code (runs/30). With the fixes, the full set of 12 staging rows at -count=20 printed ok. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- **Windows concurrency row under load.** `daemon.test.exe -test.run '^TestStageBinary_ConcurrentSpawnersAgree$' -test.count=1500 -test.timeout=19m`, with 8 busy loops each bounded by `timeout 1200` and stopped afterwards: PASS at 593c80e (runs/41). The implementer's base run had 35 of 1500 failing.
- **Windows full runs.**
  - At f96e0b8: daemon, ipc, cli and docs ok (runs/3x-*).
  - At 593c80e: daemon ok, 324 s (runs/40).
  - test/guards: `TestCarriedDefects_WaveReportRequiresResolution` fails with the same six subtests (SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2) as the implementer's runs 11 and 21. It is a plan-bookkeeping guard, and this branch changes no plan file other than run logs.
  - The two read-classification guards are green at HEAD.
- **Linux** (gate script, non-root, -race, prefix cx-w8-stagerace).
  - At 593c80e: `--run '^(TestCopyStaged_NeverReplacesAnInstalledCopy|TestStageBinary_ConcurrentSpawnersAgree|TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed)$' --count 50` passed 250 of 250.
  - Touched packages at 593c80e: daemon 1546 pass (1 skip), ipc 152, cli 427, docs 19. guards failed only on the same carried-defects guard.
  - Artifacts are under `runs/linux/`.
- **Static checks.** `go vet` on the touched packages for windows, linux and darwin. `devtool fmt-check` clean. `devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` all PASS. `go test ./test/docs` ok.
- **Cleanup.** No processes of mine are left running.

## Criterion changes
- **New assertions:** `TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed`, `TestStageBinary_RestagesACopyItCanNeverRead`, `TestCopyStaged_NeverReplacesAnInstalledCopy`.
- **`TestStageBinary_NeverRemovesACopyItCannotRead` (the implementer's, never merged)** is renamed `TestStageBinary_NeverRemovesACopyHeldOpen`. Its claim is narrowed to match finding 2 and the assertion is strengthened (ErrorIs sharing violation). Nothing was weakened.
- **Contract change:** a staged copy that cannot be read for a persistent reason is now removed and restaged. That was the behaviour before d1a0d8b; only a copy held open by another handle is kept.

## Owner decisions
No new budget, bound or threshold was introduced.

### Commits

- fa9bb7a7 fix(daemon): verify staged copies through delete-sharing reads (implementer, unchanged)
- d1a0d8b7 fix(daemon): keep a staged copy that cannot be read (implementer, unchanged)
- 0a4337bb test(guards): name Lock.Release among spawn.lock's deleters (was 324018f1; Refs footer added, same tree)
- 301ad6de docs(troubleshooting): add D27 cause and the stopping-daemon limit (was 01af8364; Refs footer added, same tree)
- 8969b143 test(daemon): start a staged copy its renamer still holds (was e702b599; Refs footer added, same tree)
- 39c8de69 test(daemon): record the w8-stagerace evidence runs (was 0952b3fe; Refs footer added, same tree)
- e8587965 fix(daemon): refuse a staged copy removed while it is hashed
- f96e0b88 fix(daemon): keep only a staged copy held open, restage the rest
- 593c80e2 fix(daemon): never install a staged copy over one in place
- 2ce18f8a test(daemon): record the w8-stagerace review-fix evidence runs

### Tests

- `go test ./internal/daemon -run '^(TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed|TestStageBinary_RestagesACopyItCanNeverRead|TestStageBinary_NeverRemovesACopyHeldOpen)$' -count=1 -v (pre-fix, Windows)` — FAIL as expected: removed/replaced verified nil; unreadable copy fell back (runs/30) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon -run '^(TestCopyStaged_NeverReplacesAnInstalledCopy|TestStageBinary_ConcurrentSpawnersAgree|TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed|TestStageBinary_VerifiesACopyItsRenamerStillHolds|TestStageBinary_StartsACopyItsRenamerStillHolds|TestStageBinary_NeverRemovesACopyHeldOpen|TestStageBinary_RestagesACopyItCanNeverRead|TestStageBinary_ReplacesATamperedCopy|TestStageBinary_ReplacesANonFileAtTheTarget|TestStageBinary_PrunesOtherVersions|TestStageBinary_CopiesOnceUnderTheContentAddress|TestStageBinary_UnreadableSourceFails)$' -count=20 (Windows, HEAD)` — ok <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `daemon.test.exe -test.run '^TestStageBinary_ConcurrentSpawnersAgree$' -test.count=1500 under 8 bounded busy loops (Windows, 593c80e)` — PASS (runs/41)
- `go test ./internal/daemon -count=1 -timeout=30m (Windows, 593c80e)` — ok 324s (runs/40)
- `go test ./internal/ipc ./internal/cli ./test/docs -count=1 (Windows, f96e0b8)` — ok
- `go test ./test/guards -count=1 (Windows, f96e0b8)` — FAIL only TestCarriedDefects_WaveReportRequiresResolution, same 6 subtests as runs 11/21 before this pass
- `linux-nonroot-gate.sh d6578301 --run '^(TestCopyStaged_NeverReplacesAnInstalledCopy|TestStageBinary_ConcurrentSpawnersAgree)$' --count 50 -- ./internal/daemon` — FAIL as expected, 100/100 failed (red for install fix)
- `linux-nonroot-gate.sh 593c80e2 --run '^(TestCopyStaged_NeverReplacesAnInstalledCopy|TestStageBinary_ConcurrentSpawnersAgree|TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed)$' --count 50 -- ./internal/daemon` — PASS 250/250
- `linux-nonroot-gate.sh 593c80e2 --timeout 30m -- ./internal/daemon ./internal/ipc ./internal/cli ./test/guards ./test/docs` — daemon/ipc/cli/docs PASS; guards FAIL only pre-existing TestCarriedDefects_WaveReportRequiresResolution
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon ./test/guards (windows, GOOS=linux, GOOS=darwin for daemon)` — clean

### Criterion changes

- Added TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed (cross-platform): a copy removed or replaced during the hash is not verified.
- Added TestStageBinary_RestagesACopyItCanNeverRead (Windows): a copy that can never be read is restaged instead of pinning spawns to the plugin binary.
- Added TestCopyStaged_NeverReplacesAnInstalledCopy (cross-platform): a losing install leaves the installed file in place, still sealed, and removes its own temporary copy.
- Renamed the implementer's unmerged TestStageBinary_NeverRemovesACopyItCannotRead to TestStageBinary_NeverRemovesACopyHeldOpen. Its claim is narrowed to held-open copies (finding 2), and the assertion is strengthened from require.Error to require.ErrorIs(windows.ERROR_SHARING_VIOLATION).
- Contract: only a copy held open by another handle (sharing or lock violation) is kept unread. Any other unreadable copy is removed and restaged, as before d1a0d8b. security.md and architecture.md are updated to match.

### Open issues

- A window of microseconds remains between verifyStagedThen's identity check and CreateProcess, as it did before this branch. The reviewer's other option, starting again from self when cmd.Start of a staged program fails with ErrNotExist, was not done.
- The implementer's evidence (39c8de69's message; runs 08, 09 and 20-24; linux/...-w8-touched-e702b59-...) quotes e702b59. After the Refs-footer reword that SHA is unreachable; the same tree is 8969b143. d657830 in 593c80e2's message is also unreachable, because it was folded into 593c80e2.
- TestCarriedDefects_WaveReportRequiresResolution fails on both Windows and Linux with six subtests (SP06-D2, SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2). The implementer's runs 11 and 21 fail the same way, and this pass changed no plan file. It needs the coordinator's wave-report resolution entries.
- The ipc, cli, guards and docs full Windows runs are at f96e0b8. 593c80e changed only internal/daemon, where the full Windows run and the read guards were re-run at HEAD.

## Independent verification of the fix seat: needs-fixes

- **minor** `commit 39c8de69 message (lines 5-7); plans/sdd/V6-closeout/w8-stagerace/runs/linux/cx-w8-stagerace-w8-touched-e702b59-20260928T131401Z-artifacts/{host.txt:4-5,identity.txt:2}; Windows runs 08, 09, 20-24` — The Refs-footer reword introduced this problem (finding 3's fix). The implementer's evidence still quotes e702b599 as the tested commit, and that SHA is no longer reachable. The same tree is now 8969b143, but that mapping appears only in the fix seat's summary. Nothing in the tree or in any commit message records it. By contrast, the unreachable d657830 is disclosed in 593c80e2's and 2ce18f8a's messages ('folded into 593c80e'). The reword pass also rewrote 39c8de69's message but left its e702b59 quotes unchanged. Anyone checking reachability at integration will find evidence pinned to a commit that does not exist.
  - Evidence: `git log -1 --format=%B 39c8de69` says 'full-package runs on Windows at e702b59 and the Linux non-root -race gate of the touched packages at e702b59 ... 08, 09 and 20-24 are at e702b59'. Linux identity.txt has commit=e702b599bdd65f61a76d95db644b990ddd464879. The fix seat's open_issues admits e702b59 is unreachable. No in-tree note maps it to 8969b143.
  - Fix: Record the mapping where a reader of the evidence will find it. Either reword 39c8de69's body to say 'at e702b59 (reworded to 8969b143, identical tree)', or add a one-line note to the w8-stagerace run index or to 2ce18f8a's message. Do not rewrite the raw run artifacts. Add no attribution trailers.
- **nit** `internal/daemon/spawn_stage_test.go:112 (TestVerifyStaged_RefusesACopyRemovedOrReplacedWhileItIsHashed doc comment)` — The new row's doc comment says the hash reads 'through a handle that shares delete (fileSHA256)'. In the same fix (e8587965), fileSHA256 went back to os.Open and now hashes only the plugin binary. The delete-sharing read of the staged copy is verifyStagedThen's paths.OpenShared, as the code and the sharedReaders guard row both say. The comment names the wrong function.
  - Evidence: spawn_stage.go:360-370: fileSHA256 uses os.Open(paths.Long(p)). spawn_stage.go:285 has verifyStagedThen's `f, err := paths.OpenShared(target)`. The test comment at :112 reads '(fileSHA256)'.
  - Fix: Change '(fileSHA256)' to '(verifyStagedThen)' in the comment.
- **nit** `internal/daemon/spawn_stage.go:124 (stageBinary doc comment)` — The fix spliced in one comment line that runs well past the file's roughly 100-column wrap: '...falls back to verifying that file. A copy some other process is executing cannot be replaced on Windows,'. The rest of the paragraph is wrapped normally.
  - Evidence: Line 124 of spawn_stage.go at HEAD 2ce18f8a.
  - Fix: Re-wrap the stageBinary doc paragraph to the surrounding width.


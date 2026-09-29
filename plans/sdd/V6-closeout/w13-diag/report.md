# Wave 13 — w13-diag (live-lane defects, D45)

Branch `closeout/w13-diag`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `99a9a60650fcf56b194124a6ca938e714fdfc278`

### Root cause

(1) The sentinel scan read only a 256 KiB tail, so a large first-turn result pushed the probe out of range. (2) mcp.server_registered and transcript.readable were evaluated at SessionStart, before the host connects MCP or creates the transcript in -p mode. On top of that, the one-shot handshake notice was dropped when no daemon was listening, and its history write raced the session start's own save. (3) Doctor ran fsck's captures walk with no index loaded, so every published root looked unresolvable. (4) The status source quoted the client's empty OK:false as a refusal; bucket-upper-bound percentiles were displayed beside an exact max; Windows pidAlive never proved an owner dead, so a terminated daemon's lock was held for the 90 s heartbeat window; nothing logged lock takeovers or spool replays. (5) The abandoned-session behaviour was undocumented.

### Summary

w13-diag seat: all five items fixed on closeout/w13-diag (base closeout/integration 84d2512, 12 commits). Every fix has a regression test that fails before the fix. The RED and GREEN logs are under plans/sdd/V6-closeout/w13-diag/runs/.

## Root causes and fixes

### (1) retrieval D4, HIGH: the sentinel scan was defeated by one large tool result
- **Cause.** `scanSentinelForPrompt` read only the last 256 KiB of the transcript (`contract.ScanTranscriptTail`). In UAT-11 the probe sat at byte 798 and the next prompt began at byte 954,253, so both chances missed and the session was degraded to passive.
- **Design.** When the daemon mints a probe it now records the transcript's size in a new field, `SentinelState.ScanFrom` (omitempty, so the frozen `history_degraded` golden is unchanged). The size is 0 when the host has not created the transcript yet. The size is taken in phase 3, under `historyMu`, before the hook returns, and the host appends the SessionStart answer that carries the probe only after that. The prompt scan (`contract.ScanTranscriptForProbe`) now reads two windows:
  - the bounded window starting at `ScanFrom`;
  - the tail window, exactly as before. This keeps older histories (where `ScanFrom` is 0 but the mint was mid-transcript) and `-p` startups working.
- **Bounds.** At most 2 × 256 KiB is read per prompt, and only until the probe is first observed; `Observed` never resets, so after that the scan stops. If `ScanFrom` is past EOF (the transcript was replaced) the scan starts from 0. A miss still spends a chance.
- **Why this design.** I chose it over "scan from the start up to a bound" because a compact or resume start mints its probe mid-transcript.
- Added a `test/guards` productreads row for the new opener (`whyHostTranscript`).
- **Tests:**
  - `TestSentinelScan_ALargeFirstResultDoesNotHideTheProbe`: the UAT-11 shape (transcript created after the start, probe near the start, a 376,892-char result). RED, then GREEN.
  - `TestSentinelScan_AProbeMintedMidTranscriptIsFoundPastALargeResult`: the compact shape. RED, then GREEN.
  - `TestSentinelScan_AnAbsentProbeStillSpendsAChance`: guard row, passes both before and after.
  - `TestScanTranscriptForProbe_ReadsTwoBoundedWindows` and `TestTranscriptSize_AnAbsentTranscriptIsZero`: unit tests.

### (2) install D4 / retrieval D7 / C45-1: healthy sessions read contract assertions as FAILING
Three causes combined:
- (a) Both checks run at SessionStart, before the host connects the MCP server and, in `-p` mode, before the host creates the transcript. Status shows those SessionStart-time results.
- (b) The stdio server sent its handshake notice once, fire-and-forget, through a client that never spools. It is usually sent before the daemon is listening, so it was dropped.
- (c) `recordMCPHandshake` did its read-modify-write of `history.json` outside `historyMu`, so a session start's own save could overwrite it.

Fixes:
- **Contract.** A new `initialize-pending` result is reported while the handshake is outstanding; the session that started waiting is recorded in `MCPAwaitSession`. The assertion fails with `initialize-not-received` only at the start of a *later* session. For the transcript, a start other than compact that finds no transcript reports `transcript-pending` and records `TranscriptAwaitPath`. If that transcript has still not appeared at the next start, the assertion fails with `an earlier session's transcript_path never appeared`. A transcript missing at a compact start still fails at once. Both new pending spellings were added to `noObservationSpellings`, and the vocabulary guard passes.
- **Daemon.** The handshake write now takes `historyMu` (through `DaemonFrom(ctx)`), and a session start merges the daemon's `MCPInitialized` seam into the history before the contract run.
- **CLI.** The handshake notice is now retried in the background on `forwardMCPCall`'s existing budget (`mcpRetryAttempts` × `mcpRetryDelay`), off the path that answers initialize, and stops when the server returns. A notice that the retries missed is sent once more after each host tool call.
- **Why a served call alone is not treated as proof.** Slash commands forward calls through the same handler, so a served call does not show that the host registered the MCP server. Only this server answers the host's initialize. I made the handshake reliable instead.
- **Tests (all RED, then GREEN):**
  - `TestMCPServerRegistered_TheFirstSessionIsPending`
  - `TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext`
  - `TestTranscriptReadable_NotYetWrittenAtStartupIsPending`
  - `TestTranscriptReadable_ATranscriptThatNeverAppearedFails`
  - `TestTranscriptReadable_AMissingTranscriptAtCompactFails`: this one passed before the fix too; it guards that real failures still fail.
  - `TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract`
  - `TestCmdMCPHandshakeReachesADaemonThatStartsLate`

### (3) install D5: doctor and fsck disagreed on unpublished captures
- **Cause.** Doctor's `captures.unpublished` ran fsck's `checkCaptures` on a scan with no index loaded, so every published sidecar's root looked unresolvable and was counted as a gap.
- **Fix.** The row now uses `store.PublicationAuditor.AuditPublication`, the same definition used by fsck's publication row and by the daemon's startup LOUD accounting. An incomplete audit reports unknown instead of clean; newer-schema sidecars get a note.
- **Test.** `TestDoctor_UnpublishedCapturesAgreesWithFsck` was RED with the live symptom ("1 gap(s) across 1 sidecar(s)" on a store where fsck captures and publication pass). It is now GREEN, and a real stage-1 gap is still reported as degraded.

### (4) Small diagnostics
- **Empty refusal reason.** With no daemon, the client returns `OK:false` with no error text, and `fetchDaemonStatus` quoted that as `status refused: `. It now prints `no daemon answered: none is listening for this project yet. This command asked one to start unless runtime.daemon.enabled is false; run status again once it is up`. Test: `TestStatusSource_NoDaemonNamesTheReason`, RED then GREEN.
- **Percentile above max.** Percentiles are reported as the bucket's upper bound (up to about 9% above the samples), while max is exact. `commands.latencyOf` now clamps each percentile to a known max (a max of 0 means unknown and is left alone), which keeps it an upper bound on the true percentile. `obs` histogram `Observe` now raises the max before counting the sample; Snapshot loads the counts before the max, so a clamp can never go below a sample it counted. Test: `TestStatus_APercentileNeverReadsAboveTheMax`, RED then GREEN.
- **~90 s refusal after a terminated daemon.**
  - Cause: on Windows `pidAlive` always answered "no opinion", so a dead owner's lock stayed held until `daemon.hb` was older than `staleAfter` (90 s). All maintenance commands and daemon spawns go through `AcquireLock`.
  - Fix: `lock_windows.go` `pidAlive` now proves an owner dead: `OpenProcess(SYNCHRONIZE)` returning `ERROR_INVALID_PARAMETER`, or the process object already being signaled. A running process, or access denied, is still "no opinion", because Windows reuses pids; the heartbeat decides as before in those cases.
  - Tests: `TestLock_AnExitedOwnerIsReplacedAtOnce` was RED on Windows and is now GREEN. `TestLock_ALiveOwnerKeepsItsFreshLock` passes.
  - I updated stale comments in `delivery_seal_tool.go`, `testutil/procalive_windows.go`, `e2e/procalive_windows_test.go` and `e2e/v3_x10_test.go`. The e2e heartbeat backdating is kept as a safety net.
- **Day log.** Two new Info lines:
  - `AcquireLock` records the stale record it replaced (`Lock.TookOver`), and `Run` logs `daemon: took over the project from a daemon that ended without releasing its lock` with pid, version and started.
  - The startup drain logs `daemon: replayed spooled deliveries at start` with the count.
  - Test: `TestRun_LogsATakenOverLockAndASpoolReplay`, RED then GREEN.

### (5) docs/troubleshooting.md
- New entry for the abandoned-session detector: default 1800 s, bookkeeping only, the session's next hook revives it, keep the setting above your longest silence including a long compaction, and the observed 31 s / 30 s case.
- Also documented:
  - the pending spellings and when each row fails;
  - the no-daemon status line and the percentile clamp;
  - how a terminated daemon's lock is taken over, the rare pid-reuse case that still waits up to 90 s, and the two day-log lines.

## Criterion changes (with rationale)
- `mcp.server_registered` no longer fails in the session whose own start came before the handshake. It is pending until a later session starts without one, because the host connects MCP servers beside the first SessionStart. The failure still fires after a whole session with no handshake, and it is still SevInfo.
- `transcript.readable` no longer fails when the transcript is absent at a non-compact start; it fails at the next start if the transcript never appeared, and at once at a compact start.
- Status display percentiles are clamped to the exact max. Budget gates are untouched: they still read the unclamped obs snapshot.
- Doctor's `captures.unpublished` counts only unpublished gaps, using the publication audit's definition. Whether a published sidecar's root resolves remains a question for fsck's captures row, which loads the whole index.
- No check was weakened, skipped or loosened. The one test assertion I edited was a comment in `test/e2e/v5_x15_test.go`, not an assertion.

## Scope notes
- Touched outside the listed scope, each as the root cause of an item in it:
  - `internal/cli/cmd_mcp.go`: the handshake relay, (2).
  - `internal/daemon/lock*.go` and `daemon.go`: the stale lock and day log, (4).
  - `internal/obs/hist.go`: the order of max and count, (4).
- No other seat's item was touched.
- The live F-C49-3 wording issue ('damaged object preserved' for a missing object) was not in my brief and is not fixed.

### Commits

- 96d15a5f fix(daemon): scan for the probe from where the mint left off
- d6cdb0e3 fix(contract): report late host observables as pending
- b6abf2ec fix(daemon): count a handshake the daemon saw at session start
- 7d6555e2 fix(cli): retry the mcp handshake notice until a daemon takes it
- d6fb02b9 fix(cli): count doctor capture gaps with the publication audit
- 781284f7 fix(cli): say why status has no daemon answer
- e2e7e480 fix(commands): never print a percentile above the max
- ddcb3d79 fix(daemon): reclaim a lock whose owner exited on windows
- a50d7534 fix(daemon): log a lock takeover and a spool replay at start
- bfd4132b docs(troubleshooting): explain pending rows and daemon takeovers
- 9cc0761d test(e2e): drop a stale mcp.server_registered comment
- 99a9a606 test(v6): record w13-diag full package runs

### Tests

- `go test -p 2 -count=1 -v -run 'TestSentinelScan_ALargeFirstResultDoesNotHideTheProbe|TestSentinelScan_AProbeMintedMidTranscriptIsFoundPastALargeResult|TestSentinelScan_AnAbsentProbeStillSpendsAChance' ./internal/daemon/` — RED before fix (first two FAIL; runs/d4-sentinel-red.txt), all PASS after (runs/d4-sentinel-green.txt)
- `go test -p 2 -count=1 -run 'TestScanTranscriptForProbe_ReadsTwoBoundedWindows|TestTranscriptSize_AnAbsentTranscriptIsZero' ./internal/contract/` — PASS
- `go test -p 2 -count=1 -run 'TestMCPServerRegistered_TheFirstSessionIsPending|TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext|TestTranscriptReadable_NotYetWrittenAtStartupIsPending|TestTranscriptReadable_ATranscriptThatNeverAppearedFails|TestTranscriptReadable_AMissingTranscriptAtCompactFails' ./internal/contract/` — RED before fix, 4 of 5 FAIL (runs/d7-contract-red.txt); the fifth (compact) passes both before and after as intended; all PASS after the fix
- `go test -p 2 -count=1 -v -run 'TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract' ./internal/daemon/` — RED before fix (runs/d7-daemon-fold-red.txt), PASS after (runs/d7-daemon-fold-green.txt)
- `go test -p 2 -count=1 -v -run 'TestCmdMCPHandshakeReachesADaemonThatStartsLate' ./internal/cli/` — RED before fix: condition never satisfied (runs/d4-handshake-red.txt); PASS after (runs/d4-handshake-green.txt)
- `go test -p 2 -count=1 -v -run 'TestDoctor_UnpublishedCapturesAgreesWithFsck' ./internal/cli/` — RED before fix: 'degraded, 1 gap(s) across 1 sidecar(s)' on a store fsck passes (runs/d5-doctor-red.txt); PASS after
- `go test -p 2 -count=1 -v -run 'TestStatusSource_NoDaemonNamesTheReason' ./internal/cli/` — RED before fix: "status refused: " (runs/d9-status-reason-red.txt); PASS after
- `go test -p 2 -count=1 -v -run 'TestStatus_APercentileNeverReadsAboveTheMax' ./internal/commands/` — RED before fix, PASS after
- `go test -p 2 -count=1 -v -run 'TestLock_AnExitedOwnerIsReplacedAtOnce|TestLock_ALiveOwnerKeepsItsFreshLock' ./internal/daemon/` — RED on Windows before fix (the exited-owner test FAILs); PASS after, together with the existing lock tests TestAcquireLockExclusive, TestStaleLockReclaimed, TestLiveLockNotReclaimed, TestAcquireLockRaceWindowIsNotStale and TestLockRefusesAfterReclaim
- `go test -p 2 -count=1 -v -run 'TestRun_LogsATakenOverLockAndASpoolReplay' ./internal/daemon/` — RED before fix: no Info lines (runs/d9-daylog-red.txt); PASS after
- `go test -p 2 -count=1 ./internal/contract/ ./internal/commands/ ./internal/obs/ ./internal/testutil/ ./test/docs/ (one package at a time)` — all ok (runs/full__*.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 497s (runs/full__internal_daemon_.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok 67s (runs/full__internal_cli_.txt)
- `go test -p 2 -count=1 -timeout=20m ./test/guards/` — FAIL only TestCarriedDefects_WaveReportRequiresResolution, the known O4 red (plans files this branch does not touch; the linux and packaging reports record the same). TestGuard_EveryProductReadIsClassified passes.
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) on touched packages` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS

### Criterion changes

- mcp.server_registered: pending (initialize-pending) through the session whose start came before the handshake; fails (initialize-not-received, still SevInfo) only at the start of a later session once a whole session passed with no handshake.
- transcript.readable: a missing transcript at a non-compact start is pending (transcript-pending); it fails at the next start if that transcript never appeared ('an earlier session's transcript_path never appeared'), and fails at once at a compact start (unchanged).
- Status display: p50, p95 and p99 are clamped to a known exact max; budget gates still read the unclamped obs snapshot.
- doctor captures.unpublished: counts the publication audit's UnpublishedCaptures, the definition fsck's publication row and the daemon's startup accounting use. Root resolution of published sidecars stays with fsck's captures row. An incomplete audit reads unknown, not ok.

### Open issues

- The live rows must be re-run on the fixed candidate by the coordinator: UAT-11 steps 5-6 (large first-turn Read, then /compact injects), plus C4.5/UAT-01/UAT-10 status banners, UAT-01/03 doctor captures.unpublished, and UAT-05/C4.9 behaviour after a terminated daemon. Real sessions were not allowed in this seat.
- The Linux container was stopped. The lock change only affects Windows; lock_unix.go and lock_linux.go are unchanged, and GOOS=linux vet is clean. No non-root -race run was done for this seat's commits.
- Residual on Windows: if Windows immediately reuses the dead daemon's pid for another process, pidAlive has no opinion and the old 90 s heartbeat window still applies. This is documented in troubleshooting §7.
- The mcpop history write is serialized through historyMu only when the op is dispatched with the daemon in ctx (every production route). Direct test fixtures that call handleMCPOp without a daemon take no lock, which is the same as before.
- The live F-C49-3 wording ('damaged object preserved' for a missing object) was not in this seat's brief and is not fixed.

### Needs the owner

- New bound sentinelScanFromMintBytes = 256 KiB (internal/daemon/handlers.go). Derivation: the host records a SessionStart answer's additionalContext as it takes the answer, before the turn the start opens. In the Phase 4 UAT-11 run the probe sat 798 bytes after the mint offset (sentinel-offsets.json). The value matches the existing tail window, so a scan reads at most 2 x 256 KiB per prompt, and only until the project's probe is first observed. If too small: a host that writes more than 256 KiB between answering the start and recording its context (for example a compaction summary that long) hides the probe again and the session degrades to passive. If too large: only read I/O per prompt before the first observation.

## Independent review

### review:diag: needs-fixes

- **major** `internal/contract/assertions.go:345-356 (checkTranscriptReadable); the same shape at :318 for checkMCPServerRegistered` — The new transcript.readable rule decides that a transcript "never appeared" at the next start of any other transcript. It does not check whether that earlier session has ended or had time to write. So a healthy project that opens a second session while the first one's transcript has not been written yet reads FAILING. Examples: two terminals opened in a row, or a session opened and closed with no prompt, where per the brief the host writes the transcript only later. This is the D4/D7/C45-1 symptom (status FAILING in a healthy session) reached by a different path. After the failure the awaited path is overwritten, so the first session's transcript is never checked again. mcp.server_registered has the same flaw: the start of any other session fails it. The doc comment and the troubleshooting text say "once a whole session has passed", but the code only checks "another session has started". That window is smaller now that the handshake is retried.
  - Evidence: Code path: sess-1 startup finds a missing path, so TranscriptAwaitPath = p1 and the row is pending. sess-2 startup has path p2 != p1 and os.Stat(p1) fails, so neverAppeared = true and the result is OK:false "an earlier session's transcript_path never appeared". TestTranscriptReadable_ATranscriptThatNeverAppearedFails pins this exact sequence. It cannot tell "never appeared" apart from "sess-1 is still live and has not typed yet". Concurrent sessions per project are supported (MaxSessions 8; sentinelMissCounts handles "a second window"). The troubleshooting.md:96-104 wording ("once a whole session has passed with no handshake") does not match the code at assertions.go:318, which fails on the start of any other session id.
  - Fix: Resolve an awaited transcript or handshake only once the awaited session is known to be over or overdue. Options: fail only if the daemon's registry shows that session ended or abandoned (not Live), or if a named, derived bound (e.g. one idle-exit window) has passed since the await was recorded; otherwise keep it pending and carry the record forward. Alternatively, re-check the awaited path at that same session's own later hooks (prompt/Stop/SessionEnd), which carry transcript_path. Add a regression row: sess-1 is pending and still live, sess-2 starts, and the result must be pending, not failing. Correct the doc and troubleshooting wording to match whatever rule ships.
- **minor** `internal/cli/qompack_commands.go:227-253 (statusNoDaemonReason / fetchDaemonStatus)` — The new message says "none is listening for this project yet" for every OK:false response with an empty Err. But ipc.Client also returns exactly that shape when a daemon is listening and did not reply in time or the connection broke mid-reply (awaitReply read deadline, ReadLine error, or decode error all go through spoolAndReturn). The same holds for a write failure after connect. A slow or hung daemon, which is itself something the diagnostics exist to reveal, is reported as absent, and the text tells the user a start was requested even though the connect succeeded and no lazySpawn ran.
  - Evidence: internal/ipc/client.go:291-302: awaitReply returns c.spoolAndReturn(req), i.e. Response{OK:false} with no Err, on SetReadDeadline, ReadLine and DecodeResponse failure. Send:251-256 does the same on write failure. Only the connect-failure branch (Send:233-247) calls lazySpawn. TestStatusSource_NoDaemonNamesTheReason covers only the offline-client case.
  - Fix: Tell the cases apart before wording the reason: e.g. call ipc.Probe(addr, short) (the dial lockIsStale already uses). If nothing answers the dial, keep the current "none is listening" text. If the dial succeeds, say "a daemon is listening but did not answer within <commandCallDeadline>". Or use neutral wording that claims neither. Add a test with a listener that accepts and never replies.
- **nit** `internal/cli/cmd_mcp.go:209-213 (buildMCPProxy handler)` — hs.flush runs synchronously after every tool call, including one where forward already failed after its whole retry budget. While a handshake is still pending, a failed call can then wait up to one more Send (connect deadline or the 5 s mcpCallDeadline) before the host gets its "temporarily offline" answer. The flush's own rationale is that the call just proved the daemon is up, which does not hold for a failed call.
  - Evidence: handler := func(...) { resp, err := forward(ctx, r); hs.flush(ctx); return resp, err }. forwardMCPCall returns unavailableResponse() after mcpRetryAttempts failed Sends, and flush then does client.Send(..., mcpCallDeadline) whenever h.raw != nil.
  - Fix: Flush only when forward reached the daemon: e.g. have forward report success, or skip the flush when resp is unavailableResponse(). Alternatively run the flush as `go hs.flush(ctx)` so it never delays the tool answer.
- **nit** `git log 84d2512..HEAD: bfd4132b, 9cc0761d, 99a9a606` — Three of the twelve commits have no `Refs:` footer; the other nine carry `Refs: V6-VERIFY, C4.2/C4.5`. There are no attribution trailers (correct).
  - Evidence: `git log --format=%B 84d2512..HEAD`: the docs(troubleshooting), test(e2e) and test(v6) commits end without a Refs line.
  - Fix: If the seat convention requires it, reword those three commits to add `Refs: V6-VERIFY, C4.2/C4.5` at integration.
- **nit** `internal/daemon/lock_windows.go:30-47; refusal texts (cmd_mcp.go:54 daemonUnavailableMsg, ErrLockHeld)` — Item (4) allowed shortening the wait or making the refusal say how long and why. The fix shortens the common case. The documented residual (Windows reuses the dead pid at once, so the lock waits up to 90 s on the heartbeat) still refuses with the old text, which says neither how long nor why. That is recorded only in troubleshooting §7.
  - Evidence: No change to the ErrLockHeld or daemonUnavailableMsg text in the diff. The troubleshooting.md addition describes the residual 90 s window.
  - Fix: Optional: when AcquireLock refuses on the heartbeat step with no listener answering the dial, append "(held until the previous daemon's heartbeat is 90 s old)" to the refusal, or record it as an accepted residual in the seat report.

## Fix seat (review resolution) — status `done`, head `414a072d`

### Root cause

Finding 1: the implementer's late-row rule settled an awaited MCP handshake or transcript at the start of any other session, without asking whether the awaited session was still live or had had its first prompt. The host creates the transcript at the first prompt (UAT-11 sentinel-offsets.json: the prompt is enqueued at offset 0), so a second window, or a session that ended with no prompt, read FAILING while healthy. Finding 2: ipc.Client gives the same empty OK-false answer for "nothing listening" and "a listening daemon never replied", and status only ever reported the first case.

### Summary

FIX seat for w13-diag, on branch closeout/w13-diag in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-diag. The base was the implementer's head 99a9a606 and the new head is 414a072d. I checked both reviewer findings myself: both are correct and both are fixed. Each fix has a regression test that failed before the fix, and the failing output is committed under plans/sdd/V6-closeout/w13-diag/runs/.

## Review resolution

### Finding 1 (major): late contract rows failed at the start of any other session. Confirmed and fixed (076e3352).

**Root cause.** In the implementer's `checkTranscriptReadable` and `checkMCPServerRegistered` (internal/contract/assertions.go), the start of any other session settled the awaited record. Nothing asked whether the awaited session was still running or had had a prompt yet.

**Why this was wrong.** The Phase 4 UAT-11 evidence shows the host creates the transcript at the first prompt: `sentinel-offsets.json` has `prompt_enqueue_offsets[0].offset == 0` and the probe at byte 798. So these healthy cases read FAILING:
- a second window opened before the first session's first prompt;
- a session closed without any prompt, which never gets a transcript at all.

**Red evidence (before the fix):**
- runs/review-contract-red.txt: `TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending` read `initialize-not-received`, and `TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending` read "an earlier session's transcript_path never appeared".
- runs/review-daemon-red.txt: `TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending`, run against the implementer's code with a real daemon, registry and history, failed at the second window's start.

**Fix:**
- **Prompt record.** `SessionHistory` now stores which session each await belongs to (`TranscriptAwaitSession`) and whether it has had a prompt (`MCPAwaitTurned`, `TranscriptAwaitTurned`). The daemon's prompt worker (`scanSentinelForPrompt`) calls `NotePrompt` inside the history load it already does. The history is saved only when something changed, and the sentinel scan behaves exactly as before.
- **Liveness seam.** A new `contract.Env.SessionLive` field is bound to `d.registry.IsLive` in `handleSessionStart`. If nothing is bound, or the session is unknown (for example after a daemon restart), it counts as not live.
- **The rule a later start applies:**
  - awaited transcript now exists: forget it;
  - awaited session still live: stay pending and keep the record;
  - session ended without a prompt: forget it, since nothing was due;
  - session had a prompt and has ended: fail once. For MCP, the wait then moves to the session that is starting.
- **Limit.** There is still one transcript slot. While it holds a live session's wait, a later start's own missing transcript is reported pending but not recorded. That can only miss a check; it never causes a false failure.
- **Mutation check.** With the `SessionLive` binding removed, the daemon test fails, so the test really exercises the binding.
- **Docs.** The troubleshooting.md wording now matches this rule (243ab201).

### Finding 2 (minor): a listening but silent daemon was reported as absent. Confirmed and fixed (9420caf9).

**Root cause.** `ipc.Client` answers OK false with an empty error both when nothing is listening and when a connected daemon never replies (client.go `awaitReply` and write-failure paths).

**Red evidence:** runs/review-status-red.txt. In `TestStatusSource_ASilentDaemonIsNotReportedAbsent`, a real `ipc.Server` accepts the request, but its answer cannot be encoded, so the connection closes with no reply. Status still said "none is listening … asked one to start".

**Fix:**
- `fetchDaemonStatus(ctx, client, listening)` now calls `ipc.Probe` on the project address before sending. Probing after the send could find the daemon that the send's own lazy spawn just started.
- If something was listening and no answer came, status now reads: "a daemon is listening for this project but did not answer within 10s: it may be busy or stuck…".
- The probe's time limit is `statusProbeTimeout = selfTestProbeTimeout` (50 ms), an existing bound reused, not a new number.
- The status section of troubleshooting.md now names this message.

## Criterion changes
- **Old tests updated.** `TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext` and `TestTranscriptReadable_ATranscriptThatNeverAppearedFails` used to require a failure as soon as any other session started. They now require the awaited session to have had a prompt and ended, and each gains an extra assertion that the row stays pending while that session is live.
  - Rationale: the old trigger is exactly the false failure the reviewer found. The real failure is still asserted: `initialize-not-received` at SevInfo, "never appeared" at SevWarn, reported once.
- **New test.** `TestTranscriptReadable_ALiveSessionsTranscriptThatAppearsLaterIsForgotten`.
- Nothing was skipped or loosened, and no limits or timeouts changed.

## Commands and results
All runs used `-p 2 -count=1`. No hot-path rows, no `-race`, and no whole test/integration or test/e2e packages were run.
- Focused green runs (logs in runs/review-*-green.txt): contract tests `TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext`, `TestTranscriptReadable_ATranscriptThatNeverAppearedFails`, `TestTranscriptReadable_ALiveSessionsTranscriptThatAppearsLaterIsForgotten`, `TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending`, `TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending`; daemon tests `TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending` and `TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract`; cli tests `TestStatusSource_ASilentDaemonIsNotReportedAbsent` and `TestStatusSource_NoDaemonNamesTheReason`. All PASS.
- Full package runs (logs in runs/review-full__*.txt): internal/contract ok (2.9 s), test/docs ok, internal/cli ok (63 s), internal/daemon ok (617 s, `-timeout=30m`).
- `go run ./tools/devtool fmt`: nothing to change.
- `go vet` on contract, daemon and cli: OK on Windows and with `GOOS=linux`.
- `devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.

## Notes
- `internal/daemon/delivery_seal_tool.go` showed as modified before I started, but `git diff` is empty (a stat-only change). I did not touch or commit it.
- `qompack self-test` does not bind `SessionLive`, so it counts every session as not live. It can therefore report one of these rows failing while the awaited session is still running, but only after that session has had a prompt and its transcript is still missing or no handshake has arrived.
- The coordinator still needs to re-run the live rows (D4/D7/C45-1) on the fixed candidate.

### Commits

- 076e3352 fix(contract): keep a live session's late rows pending
- 9420caf9 fix(cli): tell a silent daemon from an absent one in status
- 243ab201 docs(troubleshooting): state when late contract rows fail
- 414a072d test(v6): record w13-diag review-round package runs

### Tests

- `go test -p 2 -count=1 -run 'TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending|TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending' ./internal/contract/ (implementer code)` — FAIL as expected (RED), runs/review-contract-red.txt
- `go test -p 2 -count=1 -run 'TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending' ./internal/daemon/ (implementer code)` — FAIL as expected (RED), runs/review-daemon-red.txt
- `go test -p 2 -count=1 -run 'TestStatusSource_ASilentDaemonIsNotReportedAbsent' ./internal/cli/ (implementer code)` — FAIL as expected (RED), runs/review-status-red.txt
- `go test -p 2 -count=1 -run 'TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext|TestTranscriptReadable_ATranscriptThatNeverAppearedFails|TestTranscriptReadable_ALiveSessionsTranscriptThatAppearsLaterIsForgotten|TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending|TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending' -v ./internal/contract/` — PASS
- `go test -p 2 -count=1 -run 'TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending|TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract' -v ./internal/daemon/` — PASS
- `go test -p 2 -count=1 -run 'TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatusSource_NoDaemonNamesTheReason' -v ./internal/cli/` — PASS
- `go test -p 2 -count=1 ./internal/contract/` — ok 2.944s
- `go test -p 2 -count=1 ./test/docs/` — ok 2.164s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok 63.266s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 617.157s
- `go vet ./internal/contract/ ./internal/daemon/ ./internal/cli/ (Windows and GOOS=linux)` — OK
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `go run ./tools/devtool fmt` — no changes

### Criterion changes

- TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext and TestTranscriptReadable_ATranscriptThatNeverAppearedFails now require the awaited session to have had a prompt and ended before they expect the failure, instead of failing as soon as any other session starts. Each gains an extra assertion that the row stays pending while that session is live. Rationale: the old trigger was the false FAILING the reviewer found. The real failure is still asserted with the same spelling and severity (initialize-not-received at SevInfo, 'an earlier session's transcript_path never appeared' at SevWarn), reported once.

### Open issues

- The coordinator must re-run the Phase 4 live rows (retrieval D4 sentinel, install D4 / retrieval D7 / C45-1 status banner, install D5, the F-UAT03-3/F-C49-1 status reason) on the fixed candidate; real Claude Code sessions were not allowed in this seat.
- `qompack self-test` binds no SessionLive, so it treats every session as not live. It can report mcp.server_registered or transcript.readable failing while the awaited session is still running, but only after that session has had a prompt and its transcript is still missing or no handshake has arrived.
- transcript.readable keeps one awaited slot. While a live session holds it, a later start's own missing transcript is reported pending without being recorded. This can only miss a check, never cause a false failure.
- internal/daemon/delivery_seal_tool.go shows as modified in the worktree, but the diff is empty (stat-only, there before this seat started). It was not committed.

### Needs the owner

- New bound sentinelScanFromMintBytes = 256 KiB (internal/daemon/handlers.go), carried over from the implementer. Derivation: the host records a SessionStart answer's additionalContext as it takes the answer, before the turn that start opens; in the Phase 4 UAT-11 run the probe sat 798 bytes after the mint offset (sentinel-offsets.json). The value equals the existing tail window, so a prompt reads at most 2 x 256 KiB, and only until the project's probe is first observed. If too small: a host that writes more than 256 KiB between answering the start and recording its context (for example a compaction summary that long) hides the probe again and the session degrades to passive. If too large: only more read I/O per prompt before the first observation.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



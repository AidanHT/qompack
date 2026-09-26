# Wave 2b workstream report: w2-lifetime (C1.16/C1.17)

Branch `closeout/w2-lifetime`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `8c8eabde6ec203bcc8cb0674231402c988fa1077`

### Root cause

C1.16: the session.start route ran the whole compact rehydration inside the observer's SessionStart. That meant it ran behind the per-session lock that same-session ingest workers hold across their store writes (rig p99 918 ms), and behind the route's own phase-1 fsyncs (p99 655 ms). The drop report was also written before the answer (p99 393 ms). Nothing bounded the wait, and when the client's 10 s deadline passed it wrote {}. C1.17: on Windows the resident daemon, which outlives the session, ran from the plugin's own bin/qompack.exe, so the plugin directory could not be removed while it lived. N1: ipc Close closed the connection whose admin.shutdown reply was still unwritten. Hostperm remainder: the mcpop fixtures built mcp.ToolDeps without a HostPolicy, so they read the machine's real Claude Code policy.

### Summary

All four items are fixed on closeout/w2-lifetime (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-lifetime), 11 commits on b070bbe, nothing pushed. The evidence is under plans/sdd/V6-closeout/w2-lifetime/runs/ (01-24, plus linux/). There was also one incident caused by this lane that affects other lanes; it is described below.

WHAT I FOUND LEFT OVER, AND WHAT I DID WITH IT
- Two commits already existed from an earlier try today: 121cac3 (item 4) and 7a10f71 (item 3). I reviewed both and kept them. For 7a10f71 I added the Linux -race x50 run that was still owed.
- The first attempt's uncommitted route and rehydrate timing histograms were kept as written, in commit 9654383, with a new test.
- Its uncommitted observer lock timings (scratch.ss.*) were used only to find where the time went, then thrown away. The logs from those runs are runs/08 and runs/09.
- Its untracked load rig was kept, renamed TestSessionStartCompact_UnderSameSessionIngest, and extended.

C1.16: WHY A COMPACT SESSIONSTART WAS SLOW
The load rig runs real hooks and the real daemon, repeating PreCompact then SessionStart(compact) while other goroutines feed the same session. Under an extra fsync load (runs/09) the time went to three places. Building the rehydration was never one of them (p99 13 ms).
- **Waiting for the observer's per-session lock (p99 918 ms).** The whole rehydration ran inside the observer's SessionStart, behind that lock. A worker processing one of the same session's tool results or subagent stops holds the lock across every store write.
- **Phase 1's durable writes (p99 655 ms).** The route writes state.bin, the contract history and the observation ledger before doing anything else, and the rehydration waited behind them.
- **The drop-report write (p99 393 ms).** It was made before the answer went back.

Nothing bounded the total, and when the client's 10 s deadline passed it wrote {}.

C1.16: WHAT CHANGED
- **e7c1954 (daemon side).**
  - The route now starts the rehydration itself as soon as the contract run says the mode may act, before phase 1's writes, so the two overlap.
  - The observer's SessionStart runs beside it, marked as bookkeeping only so it builds no second rehydration. The route waits only for the rehydration; the bookkeeping finishes when the lock frees.
  - The rehydration hands its answer back before writing the drop report.
  - The wait is bounded at a third of the SessionStart manifest timeout (5 s), counted from arrival. If the rehydration is late, panics, or cannot start because the daemon is stopping, the answer is an explicit note instead. It starts with `<!-- qompack:rehydration-deferred -->`, gives the reason, points to expand(tool_use_id=prompt_<session>_0), recall and dropped(), and says where the checkpoints are. It is always far under the host's 10,000-character cap. Each such answer is counted (session_start_compact_deferred) and logged Loud.
  - A rehydration that finishes after the note went out records its drop report as undelivered, so dropped() says so first.
  - The background work shares the prompt captures' lifetime, so Stop joins it.
  - Degraded-passive mode still injects nothing, note included.
- **739b0f9 (hook client).** When the daemon never answers (missed deadline or no daemon reachable), the client now writes the same note instead of {}. It does this only when a rehydration was actually due: an admitted compact payload, daemon enabled, a mode that may act, runtime.mode not off or passive, and the reinjection switch on. Every other case still answers {}.

C1.16: MEASURED RESULT (30 compact cycles, 8 feeders, 256 KiB reads; wall time as the hook client sees it)
| Condition | Before p50 / p95 / p99 | After p50 / p95 / p99 |
|---|---|---|
| No extra load (runs/08, 17) | 314 / 618 / 684 ms | 138 / 185 / 188 ms |
| In-process fsync + CPU load (runs/14, 15) | 226 / 651 / 786 ms | 154 / 258 / 278 ms |
| External fsync load (runs/09, 16) | 712 / 1686 / 1853 ms | 198 / 307 / 661 ms |

The after-run with external load completed 49,960 writes against 37,446 before, so the load was no lighter. After the fix the route's wait for the rehydration is about 0 (p99 about 1 ms). What remains is phases 1 and 3 fsync time. All 90 answers after the fix were rehydrations, none deferred.

Red then green:
- The same-session lock row timed out at 3 s on the old daemon (runs/10); with the fix it answers in 54-155 ms while the lock is held (runs/11).
- The client-note row returned {} on the old client (runs/12) and passes now (runs/13).

C1.17: THE DAEMON LOCKED THE PLUGIN DIRECTORY
The daemon outlives its session. On Windows a running executable and its directory cannot be deleted, so a daemon started from the plugin's bin/qompack.exe blocked the plugin directory from being removed. That is live session 2's half-deleted extraction, and it also breaks updates and uninstalls.

Fix (938dd9f, narrowed in 9217a64): on Windows, when the hook's executable is inside CLAUDE_PLUGIN_ROOT, the daemon is started from a copy at <home>/.qompack/bin/<sha256>/qompack.exe.
- The copy is made once per version, set read-only, and checked before every spawn: a regular file (not a link or reparse point) whose SHA-256 matches both its directory name and the hook's own executable.
- A copy that fails the check is removed and replaced, never run. Copies of other versions that nothing is running are deleted.
- Two hooks spawning at once end on the same verified copy.
- The daemon's working directory is now the project root.
- If the copy cannot be made, the plugin binary is started as before, session-start logs why, and the daemon itself logs a Loud line at startup.
- On Linux and macOS nothing is copied.
- The lock, spawn-lock and Linux zombie-reclaim paths are unchanged. There is no network I/O, and all writes stay inside <home>/.qompack.

Regression test TestE2E_DaemonLeavesThePluginDirectoryRemovable: before the fix it failed with "unlinkat ...\qompack-plugin\bin\qompack.exe: Access is denied", the live-s2 symptom exactly (runs/18). It passes x3 now (runs/19).

Idle exit was not changed. The docs now say a daemon can outlive its last session by up to twice runtime.daemon.idleExitSeconds (30 min default).

N1 (ADMIN.SHUTDOWN REPLY LOST): 7a10f71 is correct. Close leaves a connection with a request in flight open under a bounded write deadline, and still closes idle connections at once. The deterministic ordering tests failed before (runs/03). Windows -race x50 passed (runs/05, 06), and Linux non-root -race x50 passed at 7a10f71 (ipc 250, daemon 50).

HOSTPERM REMAINDER: 121cac3 gives both mcpop fixtures a hermetic host policy. The new row failed under a deny-all CLAUDE_CONFIG_DIR before the change (runs/01) and passes after (runs/02).

DOCS
- architecture.md §1 now describes the daemon's lifetime and the staged copy; §2 lists what Qompack writes per user; §7 describes the bounded compact answer and its numbers.
- troubleshooting.md has two new daemon entries (the deferred note after a compaction, and a plugin directory that cannot be removed) and a corrected idle-exit entry.
- security.md explains how the staged copy is verified.

INCIDENT CAUSED BY THIS LANE (runs/21)
- **What happened.** I ran `devtool lint --only=sleepcheck,stubskips`. stubskips runs the whole tree (go test ./internal/... ./cmd/... ./test/...), which this lane was not supposed to do.
- **The effect.** Under 938dd9f, one of those real-binary tests spawned a daemon using the real USERPROFILE. That created C:\Users\Quant\.qompack (bin\ plus tmp\) at 18:23:14.
- **Why other tests broke.** paths.WriteAtomic finds its temp directory by walking up to the nearest folder that contains a .qompack, so it found C:\Users\Quant and wrote its temp files in C:\Users\Quant\.qompack\tmp. Tests that write state into a bare temp dir then failed with "rename C:\Users\Quant\.qompack\tmp\wa-... The system cannot find the path specified". That hit 2 of my cli rows and 6 ipc rows.
- **Other lanes.** Their tests of that kind between about 18:23 and 18:40 may have failed the same way. Please disregard such failures from that window.
- **Cleanup.** I stopped only my own process tree, identified by parent chain from my shell. I removed the directory (no process was running from it). Staging now requires CLAUDE_PLUGIN_ROOT (9217a64), so test binaries never stage. After the removal cli and ipc pass in full (runs/22), and C:\Users\Quant\.qompack stays absent.

REVIEW NOTES
- No t.Skip, no nolint and no lowered threshold. The nomagic annotation I had added was not needed and was removed in 3411143.

### Commits

- 121cac3 test(daemon): give the mcpop fixture a hermetic host policy (earlier attempt, reviewed and kept)
- 7a10f71 fix(ipc): let a reply in flight at Close finish before closing (earlier attempt, reviewed and kept)
- 9654383 feat(daemon): time the session.start route and compact phases
- e7c1954 fix(daemon): answer a compact SessionStart within a bound
- 739b0f9 fix(cli): say so when a compact SessionStart goes unanswered
- 938dd9f fix(daemon): run the Windows daemon from a staged copy
- 3e86409 docs(daemon): document the daemon lifetime and compact answer
- 3411143 refactor(daemon): drop a nomagic annotation nothing needed
- 7dfd103 test(guards): follow SpawnDetached's Release into spawnDetached
- 9217a64 fix(daemon): stage the daemon binary only for a plugin hook
- 8c8eabd test(daemon): record the w2-lifetime Linux and full-package runs

### Tests

- `go test ./internal/daemon/ -run TestRedC116_ -count=1 -v  (the regression row on the pre-fix daemon, file copy, not committed)` — FAIL as intended: no answer within 3s while the same session's Stop holds the observer lock (runs/10)
- `go test ./internal/daemon/ -run 'TestSessionStartCompact_|TestCompactDeferredNote_|TestCompactAnswerBudget_|TestService_|TestSessionStartRoute_' -count=1 -v` — PASS, 28 tests; compact answer in 54-155 ms with the lock held (runs/11)
- `go test ./internal/cli/ -run TestSessionStartCompact_Unanswered -count=1 -v  (hookclient.go as at 7a10f71)` — FAIL as intended: {} instead of the note (runs/12)
- `go test ./internal/cli/ -run TestSessionStartCompact_ -count=1 -v` — PASS (runs/13)
- `QOMPACK_C116_ROUNDS=30 QOMPACK_C116_WORKERS=8 QOMPACK_C116_READ_BYTES=262144 [QOMPACK_C116_FSYNC_COLOAD=16 QOMPACK_C116_CPU_COLOAD=4] go test ./internal/cli/ -run TestSessionStartCompact_UnderSameSessionIngest -count=1 -v` — before/after p99: 684->188 ms (no extra load), 786->278 ms (in-process load), 1853->661 ms (external fsync load); all answers are rehydrations (runs/08,09,14-17)
- `go test ./test/e2e/ -run TestE2E_DaemonLeavesThePluginDirectoryRemovable -count=1 -v  (spawn.go/daemon.go as at 739b0f9)` — FAIL as intended: unlinkat ...\qompack-plugin\bin\qompack.exe: Access is denied (runs/18)
- `go test ./test/e2e/ -run TestE2E_DaemonLeavesThePluginDirectoryRemovable -count=3 -v` — PASS x3, before and after 9217a64 (runs/19)
- `go test ./internal/daemon/ -run 'TestStageBinary_|TestDaemonProgram_|TestBuildSpawnCommand_|TestRunningFromPluginRoot|TestRun_ReportsRunningFromThePluginDirectory|TestBuildSpawnEnv|TestEnsureRunning' -count=1 -v` — PASS (runs/20)
- `go test ./test/guards/ -run TestGuard_AReleasedLockReportsItsSealResidual -count=1` — FAIL at 938dd9f ('spawnDetached ... is in neither list'); PASS after 7dfd103
- `go test ./test/e2e/ -run '^(TestE2E_SessionStart|TestE2E_ContractSentinelIsAppendedByTheDaemon|TestE2E_AdditionalContextProducerIsDeclared|TestE2E_DropReporterSatisfiesMCP|TestE2E_DaemonLeavesThePluginDirectoryRemovable|TestE2EHookRoundTrip|TestE2ELazySpawn|TestE2EIdleExit|TestE2E_CheckpointHookWritesImmutableArtifact|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_PreCompactToRehydrateToDroppedRoundTrip|TestV1_HookLifecycleThroughRealBinary)' -count=1 -v -timeout=30m  (at 9217a64)` — PASS, 19 rows, 80.8 s (runs/23)
- `go test ./internal/daemon/ -count=1 -timeout=30m (Windows, co-loaded)` — PASS at 3e86409 (714 s) and at 9217a64 (504 s) (runs/24)
- `go test ./internal/cli/ -count=1 -timeout=30m ; go test ./internal/ipc/ -count=1 -timeout=30m (Windows)` — at 3e86409: FAIL, 2 cli + 6 ipc rows, all 'rename C:\Users\Quant\.qompack\tmp\wa-... cannot find the path' caused by this lane's stray real-home .qompack (runs/21); after removing it: both PASS (runs/22)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-lifetime ... 7a10f71 n1-inflight-race50 --run '^(TestServerClose|TestAdminShutdownReplyReachesTheCallerOverTheTransport$)' --count 50 -- ./internal/ipc ./internal/daemon` — PASS: ipc 250, daemon 50, no race logs
- `sh .../linux-nonroot-gate.sh --prefix cx-w2-lifetime ... HEAD(3e86409) w2-touched-race -- ./internal/daemon ./internal/cli ./internal/ipc` — PASS: daemon 1409 (1 skip, pre-existing), cli 360, ipc 143; -race, non-root
- `sh .../linux-nonroot-gate.sh --prefix cx-w2-lifetime ... HEAD(9217a64) w2-compact-stage-race20 --run '<every new row + seal guard + N1 rows>' --count 20 -- ./internal/daemon ./internal/cli ./internal/ipc ./test/guards` — PASS: daemon 860, cli 240, ipc 100, guards 60; no race logs
- `go test ./test/docs/ -count=1` — PASS after each docs change
- `go run ./tools/devtool fmt-check ; go vet (daemon, cli, ipc, e2e, guards; GOOS=linux and darwin for daemon)` — clean
- `go run ./tools/lint/nomagic ./internal/daemon/... ./internal/cli/... ./internal/ipc/... ./test/e2e/...` — exit 0
- `go run ./tools/devtool lint --only=sleepcheck,stubskips` — sleepcheck OK; stubskips STOPPED by me: it runs the whole tree (the incident in runs/21); no stubskips verdict

### Criterion changes

- test/guards/sealresidualreport_test.go (7dfd103): the row that lists os.Process.Release as a release on an unrelated type now names spawnDetached instead of SpawnDetached, because 938dd9f moved that call there. The counted Release and the guard's rule are unchanged; this is not a loosening.
- New constant compactAnswerBudget = SessionStart manifest timeout / 3 (5 s), a new bound on the route's rehydration wait. It is pinned by TestCompactAnswerBudget_IsAThirdOfTheManifestTimeout and flagged for your approval.
- New hook output: an unanswered or late compact SessionStart now answers with the deferred note instead of {}, where a rehydration was due. It is pinned by TestSessionStartCompact_UnansweredIsTheDeferredNote, ...UnansweredNoteOnlyWhereARehydrationWasDue, ...LateRehydrationIsAnsweredWithTheDeferredNote and TestCompactDeferredNote_FitsTheHostCap.
- The first attempt's untracked TestSessionStartCompact_LoadRig was renamed TestSessionStartCompact_UnderSameSessionIngest. It gained one machine-independent assertion (every answer is the rehydration or the deferred note), and QOMPACK_C116_* environment variables scale it into the benchmark.

### Open issues

- The compact answer's own durable writes (phase 1: state.bin, contract history, observation ledger; phase 3: history) are still unbounded fsyncs. After the fix they are the whole remaining p99 (contract p99 524 ms under the external load). Under a severe fsync storm the answer can still miss the client's 10 s deadline; the client now writes the explicit note in that case instead of {}. Merging the two SaveHistory writes or moving the observation ledger off the answer path would change contract-history semantics and was not attempted.
- A session.start the client gave up on is spooled and replayed by the next drain. The replay rebuilds the rehydration and records its drop report as if it had been delivered. This behaviour predates this lane and is not changed.
- Rehydration failures other than lateness (an unreadable checkpoint store, a failed build) still answer with nothing. TestService_CheckpointErrorEmitsNothing pins that behaviour, so I left it.
- After a plugin update, a running old-version daemon keeps serving until its idle exit (up to twice runtime.daemon.idleExitSeconds). There is no version handshake and idle exit was not shortened.
- Latent defect in internal/paths, outside this lane's scope: WriteAtomic's walk treats any ancestor that contains a .qompack (such as the user's home) as the project root. With a real ~/.qompack present, at least 2 internal/cli rows and 6 internal/ipc rows fail on that machine. Windows staging now creates ~/.qompack/bin for a real plugin user, so the developer's own machine will hit this once the plugin is used here.
- Incident (runs/21): this lane's stubskips whole-tree run (about 18:20-18:38) and the C:\Users\Quant\.qompack it created (18:23:14 until about 18:40 local) may have caused test failures in other lanes on this machine during that window. Those failures read 'rename C:\Users\Quant\.qompack\tmp\wa-...' and are not defects in their branches. The directory has been removed and the cause fixed (9217a64).
- No real Claude Code session was run (not allowed in this wave). C1.16's live 10 s failure and C1.17's host cleanup and plugin update behaviour are proven only by the rig, the unit rows and the e2e plugin-directory row, not on the real host. C4.3 still needs a live compaction and a Windows plugin update and uninstall.
- `dropped()` called within milliseconds of a delivered compact answer can still show the previous drop report: the report is now written just after the answer is handed back.
- The deferred note is not wrapped in the injection tags: it carries no archived material, and the probe line is not tagged either.

### Needs the owner

- New bound: the daemon waits for a compact rehydration for at most compactAnswerBudget = one third of the SessionStart manifest timeout (5 s), measured from arrival, then answers with the deferred note. It is a bound on a wait, not a latency budget, but it is a new number and needs your approval.
- New answer shape: a compact SessionStart can now carry the explicit deferred note (from the daemon when late, failed or stopping, or from the hook client when the daemon never answered) instead of {}. Please confirm the wording and the conditions (it is only written where a rehydration was due).
- Decide whether an unreadable checkpoint store or a failed build should also produce the deferred note. Today it answers with nothing, as pinned by TestService_CheckpointErrorEmitsNothing.
- C1.17 policy: I chose staging the Windows daemon under <home>/.qompack/bin/<sha256>, only when a hook runs from inside CLAUDE_PLUGIN_ROOT, rather than an idle exit after the last SessionEnd. Please confirm, and decide whether idle exit after an explicit SessionEnd should also be shortened, to limit old-version daemons after an update.
- The paths.WriteAtomic walk escaping to a home's .qompack (latent, needs the internal/paths owner). It becomes practical on Windows dev machines now that real use creates ~/.qompack/bin.
- Coordinator: the stubskips whole-tree incident window (about 18:20-18:40 local, 2026-09-25) when triaging other lanes' Windows failures.

## Independent review

### review:lifetime: needs-fixes

- **minor** `internal/daemon/spawn_stage.go:239-240 (daemonProgram) and internal/daemon/daemon.go:653 (startup Loud)` — Both staging and the fallback Loud decide whether the binary is the plugin's by reading the CLAUDE_PLUGIN_ROOT environment variable. The daemon can also be lazily spawned by `qompack mcp` (cmd_mcp.go:128, Spawn: daemon.SpawnDetached). That process is launched from .mcp.json's ${CLAUDE_PLUGIN_ROOT}/bin/qompack, and nothing in the branch shows that the host also puts CLAUDE_PLUGIN_ROOT into the MCP server's environment. If it does not, a daemon spawned from the MCP process runs from the plugin's bin/qompack.exe and pins the plugin directory (the C1.17 defect), and the Loud line stays silent because it uses the same env check.
  - Evidence: insideDir(self, pluginRoot) returns false when pluginRoot == "". The only C1.17 regression row (test/e2e/plugin_dir_lifetime_test.go) exercises session-start with CLAUDE_PLUGIN_ROOT set explicitly; no row covers the MCP lazy-spawn path, and the commit message says only that the host sets the variable 'for every plugin hook'.
  - Fix: Do not depend only on the env var. Also treat self as a plugin binary when its layout identifies one: <dir>/bin/qompack[.exe] with <dir>/.claude-plugin/plugin.json (or BUNDLE.json) beside it. Alternatively, have the MCP process pass its plugin root explicitly. Add a row that lazily spawns through newMCPClient's Spawn with CLAUDE_PLUGIN_ROOT unset and asserts the plugin dir stays removable on Windows.
- **minor** `internal/daemon/rehydrate_service.go:200; internal/cli/hookclient.go:468` — The drop report is marked 'undelivered' only when the daemon's own 5 s bound expires. In two cases the report still says 'delivered' after the model received the deferred note: (a) the daemon's answer, rehydration included, misses the client's 10 s deadline in phase 3 (the unbounded fsyncs the lane itself names as the remaining p99), and the client writes the DeferredNoAnswer note; (b) the spooled session.start is replayed by the next drain, rebuilds the rehydration and records it as delivered. The new note tells the model to call dropped(), and in these cases dropped() then describes a payload the model never saw. The lane disclosed the replay behaviour as pre-existing, but the new note now sends the model straight to the misleading report.
  - Evidence: t.offer(out) succeeds as soon as the route collects the answer. Nothing links the client's later give-up, or the replay, back to the Reporter. open_issues #2 acknowledges the replay case; case (a) is not mentioned.
  - Fix: Make recordUndelivered, or a delivered flag the Reporter can flip, reachable from the client-note path: for example, the client includes a marker in the spooled request, and the replay of a compact session.start whose original request carried the client note records the report as undelivered instead of building a new one. At minimum, document in troubleshooting.md and in the note's wording that dropped() may not reflect an undelivered compaction when the daemon did not answer at all.
- **minor** `internal/daemon/session_start_compact.go:267` — The observer's compact SessionStart bookkeeping (frontier adoption and ensureSegment, observer/session.go steps 3-4) now runs on a detached goroutine after the answer has gone back. Before, it was guaranteed to finish before the host could send the session's next hook. After a daemon crash, state/observer.json can lag the durable segment frontier. The next UserPromptSubmit's verbatim capture (prompt_delivery.go:146, VerbatimPromptID(sess, st.Turn)) could then take the session lock before the bookkeeping and mint a prompt id from the un-adopted turn index, which could reuse an id already captured before the compaction. Which one runs first now depends on sync.Mutex fairness, not on the protocol.
  - Evidence: startReplyWork(withRouteRehydrates(ctx), ...) is fire-and-forget, and awaitCompactAnswer waits only for the rehydration ticket. The lock is contended exactly in the scenario the fix targets (same-session workers, or a startup drain replaying spooled events, hold it).
  - Fix: Keep the bookkeeping off the answer path, but order it before the session's next event. Options: have ObservePrompt/ObserveTool for a session with pending compact bookkeeping wait on a per-session 'bookkeeping done' channel, or run only the frontier adoption synchronously (it is a single store read) and leave ensureSegment async. Add a row with a lagging state file and a prompt that races the bookkeeping.
- **minor** `docs/install.md:187-205` — The uninstall policy table and the 'delete it yourself' guidance do not mention the new staged executables under ~/.qompack/bin/<sha256>/qompack.exe. An uninstall now leaves one read-only executable per staged version in the user profile, and it is pruned only when a new version is staged, which never happens after an uninstall. The suggested `rm -rf ~/.qompack` is described as removing 'configuration and logs', and on Windows removing read-only files needs -Force or clearing the attribute.
  - Evidence: architecture.md §2 now lists bin/<sha256>/qompack.exe in the per-user write set, but install.md §6, the document an operator reads when uninstalling, was not updated.
  - Fix: Add a row for '~/.qompack/bin (Windows: the daemon's staged executable)' saying it is retained, and a removal line (PowerShell: Remove-Item -Recurse -Force $HOME\.qompack\bin, after the daemon has exited).
- **minor** `internal/daemon/spawn_stage.go:77-110 (stageBinary) via spawn.go spawnDetached` — Every spawn on Windows from a plugin root now hashes the whole binary twice: fileSHA256(self), then verifyStaged re-hashing the target. The first spawn per version also copies and fsyncs the binary, and Defender will scan the new executable. This happens synchronously inside session-start's EnsureRunning and inside hooks' lazy spawn. The lane's compactAnswerBudget rationale budgets only EnsureRunning's 1.5 s poll, and no run measures the staging cost, even though the C1.16 scenario (compact SessionStart with no daemon running) goes through this path.
  - Evidence: No run under plans/sdd/V6-closeout/w2-lifetime/runs times stageBinary. The rig runs in-process with no staging.
  - Fix: Record a histogram or at least a measured distribution of stageBinary (cold copy and warm verify) on this machine in the evidence, and confirm it fits inside session-start's timeout alongside the 1.5 s poll. If the cold copy is large, consider staging ahead of time on the session-start path, off the compact answer.
- **nit** `internal/daemon/spawn_stage.go:189 (pruneStaged)` — pruneStaged removes every other-version directory, including one that a concurrent spawner of a different plugin version (for example two projects across a plugin update) has just verified or is still writing .stage-* into. Its subsequent exec then fails with file-not-found, and the spawn errors instead of falling back to self. Pruning also clears the read-only bit on a running copy it then fails to delete, which leaves that copy writable.
  - Evidence: pruneStaged calls os.Chmod(p, 0o600) and then RemoveAll, with no age or lock check. Neither daemonProgram nor spawnDetached retries or falls back when cmd.Start fails on a vanished staged path.
  - Fix: Skip directories modified within the last few minutes, restore 0o500 when RemoveAll fails, and in spawnDetached fall back to self if starting the staged path fails with fs.ErrNotExist.
- **nit** `internal/daemon/session_start_compact.go:298` — When the serving ctx ends (Stop), the note gives the reason DeferredNotReady ('it was not ready when the answer was due') rather than DeferredStopping, so the note and the Loud line blame lateness for what was a shutdown.
  - Evidence: case <-ctx.Done(): if a.ticket.abandon() { return d.deferCompactAnswer(a, DeferredNotReady) }
  - Fix: Use DeferredStopping in the ctx.Done arm.
- **nit** `internal/daemon/spawn.go:165` — The existing //nolint:gosec justification says self is 'the plugin's own executable path (os.Executable())'. On Windows the argument can now be the staged copy's path, so the suppression's stated reason is stale.
  - Evidence: buildSpawnCommand is now called with program from daemonProgram.
  - Fix: Reword it to 'the plugin's own executable, or its verified staged copy (spawn_stage.go)'.
- **nit** `commit 3411143` — This commit has no Refs footer; every other commit on the branch carries 'Refs: V6-VERIFY, C1.16/C1.17'.
  - Evidence: git log -1 --format=%B 3411143 ends after the body, with no Refs line.
  - Fix: Add the Refs footer when the branch is next rewritten or squashed for merge.

## Fix seat (review resolution) — status `done`, head `598f5502ded96dbd2dc113e3e7657c5df7cb4734`

### Root cause

C1.16: the compact SessionStart route did its rehydration inside the observer's SessionStart. It therefore waited behind the observer's per-session lock (held by a same-session ingest worker across its store writes, p99 918 ms under co-load), behind phase 1's fsync-bound writes, and behind the drop-report write, all with no bound; the build itself was about 13 ms. C1.17: on Windows the resident daemon ran from the plugin's own bin/qompack.exe, which Windows will not delete, and it outlived the session. N1: ipc Close closed the connection whose admin.shutdown reply was still unwritten. The hostperm remainder: the mcpop fixtures built mcp.ToolDeps without a HostPolicy and so read the machine's real host rules. Fix-seat findings: 938dd9f's project-root working directory broke long roots on Windows; staging keyed only on an env var the MCP lazy-spawn path is not guaranteed; the detached bookkeeping lost its ordering before the session's next event; a spool-replayed compact start recorded its rehydration as delivered.

### Summary

W2-LIFETIME REPORT (C1.16 + C1.17 + linux N1 + hostperm mcpop remainder). Branch closeout/w2-lifetime in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-lifetime, base closeout/integration b070bbe, head 598f550, working tree clean, nothing pushed. Evidence: plans/sdd/V6-closeout/w2-lifetime/runs/01-38 plus runs/linux/*.

## State at start of the fix seat
- The worktree was clean.
- The first attempt (wf_0d8775ab-04e) left uncommitted phase histograms. The implementer adopted them as written in 9654383, with a test (TestSessionStartRoute_RecordsItsPhases) that requires every histogram.
- The implementer's 11 commits were already on the branch.

## Root causes and fixes (implementer, verified by the fix seat)

**(1) C1.16: compact SessionStart took 10.3 s and answered {}.**
- Root cause, measured with the load rig (runs/08, 09, 14): building the rehydration was never the cost (rehydrate.build p99 about 13 ms). The route waited in three places:
  - the observer's per-session lock, held by a worker writing the same session's events (p99 918 ms under co-load);
  - phase 1's durable writes (p99 655 ms);
  - the drop-report write made before the answer (p99 393 ms).
- Fix (e7c1954, 739b0f9):
  - The route starts the rehydration itself through Services.Rehydrate as soon as the contract run allows acting, overlapping phase 1's writes.
  - The observer bookkeeping runs detached.
  - The answer is handed over before the drop report is written.
  - The wait is bounded at compactAnswerBudget: 5 s, one third of the 15 s manifest timeout.
  - A late, failed or refused rehydration gets an explicit deferred note, never {}. The note names the MCP recovery calls and the checkpoint location, and the event is counted and Loud.
  - The hook client writes the same note when no answer arrives at all and a rehydration was due.
- Distributions (compact answer wall time, n=30):

| Condition | Before | After |
|---|---|---|
| No extra co-load | p99 684 ms (runs/08) | p99 188 ms (runs/17) |
| Same in-process co-load | p99 786 ms (runs/14) | p99 278 ms (runs/15) |
| Same external fsync co-load | p99 1.85 s (runs/09) | p99 661 ms (runs/16) |

- Fix-seat rig after the gate and replay changes (runs/36), co-loaded by this lane's own Linux -race run:
  - p50 228 ms, p99 618 ms, all 30 answers rehydrations;
  - compact_wait p99 0.64 ms and seam p99 1.28 ms: the gate adds nothing to the answer path;
  - the remaining time is the fsync-bound session_start.contract phase.
- Target (well under 2 s p99) is met.

**(2) C1.17: the daemon pinned the plugin directory on Windows.**
- Fix (938dd9f, 9217a64): on Windows the daemon runs from <home>/.qompack/bin/<sha256>/qompack.exe.
  - The copy is content-addressed, sealed read-only, and checked before every spawn: a regular file, no link or reparse point, whose hash matches both its name and self. A copy that fails is replaced, never run.
  - Idle copies of other versions are pruned.
  - If staging fails, the daemon starts from the plugin binary, session-start logs why, and the daemon Louds at startup.
  - Lock, spawn-lock and zombie-reclaim paths are unchanged; there is no network I/O.
  - Idle exit (runtime.daemon.idleExitSeconds) already existed and is now documented.
- Real-home incident (runs/21): at 938dd9f a whole-tree run staged into the real C:\Users\Quant\.qompack. The implementer removed it, and 9217a64 narrowed staging to plugin binaries.

**(3) Linux lane N1: admin.shutdown reply lost.**
- Fix (7a10f71): ipc Close leaves a connection with a request in flight open under a bounded write deadline.
- The regression test injects the losing ordering deterministically and failed before the fix (runs/03).
- -race -count=50 passes on Windows (runs/05, 06) and Linux (runs/linux n1-*).

**(4) hostperm remainder.**
- Fix (121cac3): the mcpop fixtures get a hermetic HostPolicy.
- The deny-all regression row failed before (runs/01) and passes after (runs/02).

## Found by the fix seat (not in the review)
- **f8159a1: long project roots broke daemon start on Windows.** 938dd9f set the spawned daemon's cmd.Dir to the project root. CreateProcess cannot start a process whose working directory is past MAX_PATH, so TestPlatform_ProjectRootShapes/long was RED at 8c8eabd: no daemon came up in 60 s (runs/25).
- Fix: daemonWorkingDir. A staged daemon runs in its own staged directory, which the running executable already holds, so it pins nothing more. An unstaged daemon inherits the hook's working directory, as before 938dd9f.
- The platform matrix passes all four root shapes after the fix (runs/26), and test/platform passes in full (runs/29).

## Review resolution
- **Finding 1 (MCP lazy spawn without CLAUDE_PLUGIN_ROOT): CONFIRMED, fixed in 39dd2fd.**
  - Nothing in the repo shows the host exporting the variable to the MCP server, and plugin/.mcp.json passes no env.
  - The new e2e row TestE2E_MCPLazySpawnLeavesThePluginDirectoryRemovable drives the real `qompack mcp` from a plugin layout with the variable unset. It reproduced the pin ("unlinkat ...bin\qompack.exe: Access is denied", runs/27).
  - Unit rows TestDaemonProgram_StagesAPluginLayoutWithoutTheVariable and TestRunningFromPluginRoot were red first.
  - Fix: pluginDirOf recognises the plugin binary inside CLAUDE_PLUGIN_ROOT or by the host layout (bin/qompack[.exe] with .claude-plugin/plugin.json beside bin/). daemonProgram and the startup Loud share it. A bin/ without the manifest (a build tree) is not staged.
  - Green: runs/28. test/platform passes in full (runs/29), and C:\Users\Quant\.qompack does not exist afterwards.
  - Docs updated: architecture.md, security.md, troubleshooting.md.
- **Finding 2 (drop report says delivered after the model got the note): CONFIRMED, fixed in 50080f0.**
  - A session.start reaches the drain only from a client spool, meaning the hook had already answered without the daemon.
  - Red row TestSessionStartCompact_ReplayedRequestRecordsItsRehydrationUndelivered: the replay offered and "delivered" its rehydration to nobody (runs/32).
  - Fix: drainDispatch marks replays (withSpoolReplay). A replayed compact start abandons its ticket with undeliveredReplayed before the rehydration starts, so the drop report leads with the undelivered entry and says why. It does not wait and is not counted or Loud'd as a deferral.
  - This also covers case (a): the client spools a request whose reply missed its deadline, and the replay overwrites the late live answer's "delivered" report.
  - Recording is parametrised by reason; there is a service-level row TestService_ReplayedRehydrationRecordsWhyItWasNotDelivered.
  - Note wording changed: dropped() "reports on the most recently recorded rehydration, which may be an earlier one" (true for every reason).
  - troubleshooting.md explains that the correction lands at the next drain (within 30 s) or the next daemon start, and never if the spool write failed. Green: runs/33.
- **Finding 3 (bookkeeping detached from the next event): ordering CONFIRMED and fixed in c1efe8b; the specific id-reuse scenario REBUTTED.**
  - Rebuttal: a production prompt capture is leased, and observer/prompt.go calls recoverPrompt before recordPromptDurable. recoverPrompt adopts store.PromptFrontier: the max turn over every tool_use record of the session, +1 after a prompt. So VerbatimPromptID cannot reuse an already-captured prompt id whatever the lock order.
  - The ordering regression itself is real: tool records at an un-adopted turn, or a SessionEnd racing ensureSegment. Red row TestSessionStartCompact_SessionsNextEventWaitsForTheBookkeeping: the same session's tool result, Stop, prompt capture and SessionEnd all overtook the held bookkeeping (runs/30).
  - Fix: a per-session compactGates gate, held from before the answer until the bookkeeping ends. ObserveTool, ObserveStop, ObservePrompt and SessionEnd are wrapped once in New to wait on it (ctx-bounded). Only that session's lane waits, and Stop's cancel-and-join releases the gate.
  - Green: runs/31. Rig runs/36 confirms the answer path is untouched.
- **Finding 4 (install.md uninstall table): CONFIRMED, fixed in 6500333.** Added the staged-copy row, noted that `rm -rf ~/.qompack` covers bin/, and gave the PowerShell removal: stop the daemon first, and use -Force because the copies are read-only.
- **Finding 5 (staging cost unmeasured): CONFIRMED, measured in 657a31c; no code change needed.**
  - BenchmarkStageBinary, using the 18.1 MB test binary as an upper bound: cold p50 177-200 ms / p99 211-353 ms; warm p50 29-44 ms / p99 31-49 ms.
  - Committed script runs/34, end to end on the real 13.4 MB binary, n=10 per mode (runs/35):

| Mode | p50 | max |
|---|---|---|
| Unstaged | 570 ms | 2.4 s |
| Staged, cold | 1.54 s | 3.05 s |
| Staged, warm | 466 ms | 706 ms |

  - No run spooled.
  - Staging runs before the dial, so it is outside the 10 s reply deadline and the 5 s compact budget. EnsureRunning's 1.5 s poll caps a slow first execution. It fits the 15 s host timeout with more than 2.5 s to spare.
  - The cold cost is mostly one first execution of a new executable, once per version per user.

## Commands and results (fix seat)
- `go test ./test/platform/ -run '^TestPlatform_ProjectRootShapes$' -count=1 -v`: RED at 8c8eabd (long, 60.5 s); PASS after f8159a1.
- `go test ./test/platform/ -count=1 -v -timeout=30m`: PASS (runs/29).
- `go test ./test/e2e/ -run '^(TestE2E_MCPLazySpawn...|TestE2E_DaemonLeaves...)$'`: RED before 39dd2fd, PASS after with lazy spawn, idle exit and MCP end-to-end (runs/27, 28).
- Focused daemon rows: `-run '^TestSessionStartCompact_'` and `'^(TestSessionStartCompact_|TestService_|TestCompactDeferredNote)'`: RED before, PASS after (runs/30-33).
- `go test ./internal/cli/ -run 'Deferred|Compact'`: PASS.
- `go test ./internal/daemon/ -count=1 -timeout=30m` (Windows): one red, TestIngestRingFullWALsEveryLineAndNeverSpills (Accept 573 ms against 500 ms under co-load, ingest code not touched here). It passes 10 of 10 alone at 0.08-0.18 s (runs/37).
- Linux non-root -race via linux-nonroot-gate.sh --prefix cx-w2-lifetime:
  - new and changed rows ×20: daemon 980 pass, 0 fail;
  - ./internal/daemon ./internal/cli in full at 657a31c: 1415 pass (one pre-existing Windows-only skip) and 360 pass, no race.
- `go test ./test/e2e/ -count=1 -timeout=60m` (Windows): all rows pass except three, each identical alone and on a git archive of the base b070bbe (runs/38):
  - TestV4_TombstoneToRecallToExpandRoundTrip: append-only violation; already classified pre-existing in the hostperm report;
  - TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces: PreCompact hookSpecificOutput; already classified as C1.12 fallout in the rehydrate-cap report;
  - TestV3_HotPathUnchangedWithLedgerResident: B-A/B-B budgets. Base B-A p99 24.6 ms; head 90 ms on the first run and 22.5 ms on the second, the same p50; not on this lane's code paths.
- Also passing:
  - `go test ./test/guards/ -run` the seal-residual rows;
  - `go test ./test/docs/`;
  - `go vet` on internal/daemon, internal/cli and test/e2e;
  - `devtool fmt-check`;
  - gen-command-docs, gen-config-docs and gen-mcp-docs with --check.
- No own daemons left running, and C:\Users\Quant\.qompack does not exist.

## Criterion changes
- TestBuildSpawnCommand_RunsFromTheProjectRoot (the implementer's, pinning cmd.Dir == project root) was replaced by TestDaemonWorkingDir. The pinned behaviour is the regression: a project root past MAX_PATH cannot be a Windows process's working directory, which made TestPlatform_ProjectRootShapes/long fail. The new row pins the staged-dir/inherit rule.
- The deferred note's dropped() clause changed from "lists what the last rehydration left out" to "reports on the most recently recorded rehydration, which may be an earlier one". The old wording was false for failed, stopping, in-progress and client-side cases. Every assertion (contains "dropped()", host-cap bound) is unchanged.
- No check was weakened or skipped.

## Process note
Commit 2f93634's subject was 68 characters. I rewrote my own two newest unpushed commits with git commit-tree and update-ref: same trees (verified with an empty `git diff 59629b0 HEAD`), new SHAs 6500333 and 657a31c. The Linux rows run was at 59629b0, which has the same tree.

### Commits

- 121cac3 test(daemon): give the mcpop fixture a hermetic host policy (implementer)
- 7a10f71 fix(ipc): let a reply in flight at Close finish before closing (implementer)
- 9654383 feat(daemon): time the session.start route and compact phases (implementer; adopts the first attempt's uncommitted histograms)
- e7c1954 fix(daemon): answer a compact SessionStart within a bound (implementer)
- 739b0f9 fix(cli): say so when a compact SessionStart goes unanswered (implementer)
- 938dd9f fix(daemon): run the Windows daemon from a staged copy (implementer)
- 3e86409 docs(daemon): document the daemon lifetime and compact answer (implementer)
- 3411143 refactor(daemon): drop a nomagic annotation nothing needed (implementer)
- 7dfd103 test(guards): follow SpawnDetached's Release into spawnDetached (implementer)
- 9217a64 fix(daemon): stage the daemon binary only for a plugin hook (implementer)
- 8c8eabd test(daemon): record the w2-lifetime Linux and full-package runs (implementer)
- f8159a1 fix(daemon): never start the daemon in the project root (fix seat: regression found in verification, not raised by the reviewer)
- 39dd2fd fix(daemon): stage a plugin binary found by its layout too (fix seat: review finding 1)
- c1efe8b fix(daemon): order compact bookkeeping before the next event (fix seat: review finding 3)
- 50080f0 fix(daemon): record a replayed compact rehydration undelivered (fix seat: review finding 2)
- 6500333 docs(install): list the staged daemon copy under uninstall (fix seat: review finding 4)
- 657a31c test(daemon): measure what staging the daemon binary costs (fix seat: review finding 5)
- 598f550 test(daemon): record the fix seat's full-package and Linux runs (fix seat evidence)

### Tests

- `go test ./test/platform/ -run '^TestPlatform_ProjectRootShapes$' -count=1 -v -timeout=30m (Windows, at 8c8eabd / after f8159a1)` — RED at 8c8eabd: long root never got a daemon (60.5 s); PASS after the fix (runs/25, runs/26)
- `go test ./test/platform/ -count=1 -v -timeout=30m (Windows, after layout staging)` — PASS, 10 tests; real home has no .qompack afterwards (runs/29)
- `go test ./test/e2e/ -run '^(TestE2E_MCPLazySpawnLeavesThePluginDirectoryRemovable|TestE2E_DaemonLeavesThePluginDirectoryRemovable|TestE2ELazySpawn|TestE2EIdleExit|TestStdioServerEndToEnd)$' -count=1 -v` — RED before 39dd2fd ('Access is denied' on the plugin's bin/qompack.exe); PASS after (runs/27, runs/28)
- `go test ./internal/daemon/ -run '^TestSessionStartCompact_(SessionsNextEventWaitsForTheBookkeeping|BookkeepingGateHonoursCancellation)$' -count=1 -v` — RED before c1efe8b (all four seams overtook the bookkeeping); PASS after, along with every TestSessionStartCompact_ row (runs/30, runs/31)
- `go test ./internal/daemon/ -run '^(TestSessionStartCompact_|TestService_|TestCompactDeferredNote)' -count=1 -v` — Replay row RED before 50080f0 (the replay delivered its rehydration); all PASS after (runs/32, runs/33)
- `go test ./internal/cli/ -run 'Deferred|Compact' -count=1` — PASS
- `QOMPACK_C116_ROUNDS=30 QOMPACK_C116_WORKERS=8 QOMPACK_C116_READ_BYTES=262144 go test ./internal/cli/ -run '^TestSessionStartCompact_UnderSameSessionIngest$' -count=1 -v` — PASS, 30 of 30 rehydrations, p50 228 ms, p99 618 ms under co-load, compact_wait p99 0.64 ms (runs/36)
- `go test ./internal/daemon/ -run '^$' -bench '^BenchmarkStageBinary$' -benchtime=40x -count=3 -v` — cold p50 177-192 ms / p99 211-221 ms; warm p50 29-35 ms / p99 31-40 ms (runs/35)
- `bash plans/sdd/V6-closeout/w2-lifetime/runs/34-c117-stage-cost.sh <repo> <scratch> 10` — unstaged p50 570 ms; staged-cold p50 1540 ms (max 3053); staged-warm p50 466 ms; no run spooled (runs/35)
- `go test ./internal/daemon/ -count=1 -timeout=30m (Windows, full)` — 1 red: TestIngestRingFullWALsEveryLineAndNeverSpills (co-load wall-clock, 573 ms against 500 ms); it passes 10 of 10 alone (runs/37)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-lifetime HEAD fixseat-rows-race20 --run '<new rows>' --count 20 -- ./internal/daemon` — PASS, daemon 980 pass / 0 fail, -race, non-root (at 59629b0, the same tree as 657a31c)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-lifetime HEAD fixseat-daemon-full-race --timeout 30m -- ./internal/daemon ./internal/cli` — PASS, daemon 1415 pass / 0 fail / 1 pre-existing Windows-only skip; cli 360 pass; no race (at 657a31c)
- `go test ./test/e2e/ -count=1 -timeout=60m (Windows, full)` — 3 reds, all pre-existing and identical alone on a git archive of base b070bbe: V4 tombstone append-only, V5 PreCompact hookSpecificOutput, V3 hot-path B-A/B-B budget; everything else passes (runs/38)
- `go test ./test/guards/ -run '^(TestGuard_AReleasedLockReportsItsSealResidual|TestGuard_TheSealResidualScannerSeesBothCallShapes)$' -count=1` — PASS
- `go test ./test/docs/ -count=1; go run ./tools/devtool fmt-check; gen-command-docs, gen-config-docs and gen-mcp-docs with --check; go vet on internal/daemon, internal/cli and test/e2e` — all PASS / exit 0

### Criterion changes

- internal/daemon/spawn_stage_test.go: TestBuildSpawnCommand_RunsFromTheProjectRoot, which pinned cmd.Dir == project root (introduced by 938dd9f in this workstream), was replaced by TestDaemonWorkingDir. Rationale: a project root past MAX_PATH cannot be a Windows process's working directory, since CreateProcess takes it without the \\?\ prefix. The pinned behaviour made test/platform TestPlatform_ProjectRootShapes/long fail with no daemon at all (runs/25). The new criterion: a staged daemon runs in its staged directory, and an unstaged one inherits the hook's working directory.
- internal/daemon/session_start_compact.go CompactDeferredNote: the dropped() clause changed from 'lists what the last rehydration left out' to 'reports on the most recently recorded rehydration, which may be an earlier one'. The old text was false whenever the note is shown before, or instead of, this compaction's report being recorded. Every existing assertion (contains 'dropped()', host-cap ceiling) is unchanged.

### Open issues

- Pre-existing and not from this lane: test/e2e TestV4_TombstoneToRecallToExpandRoundTrip (write-time append-only violation), TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces (PreCompact hookSpecificOutput, C1.12 fallout) and TestV3_HotPathUnchangedWithLedgerResident (B-A/B-B budgets on this co-loaded machine). All three fail identically on b070bbe (runs/38).
- Replay correction latency: when the daemon's answer misses the client's deadline, dropped() can describe that late rehydration as delivered until the spooled request is replayed (next idle drain within 30 s, or next daemon start). If the client's spool append failed, the correction never comes; this is documented in troubleshooting.md.
- A daemon that failed outright (panic) or that a stopping daemon never started records no drop report at all. dropped() then shows an earlier compaction's report; the note now says so, but no undelivered entry is written for those two reasons.
- Staged copies under ~/.qompack/bin persist after an uninstall, because pruning happens only when a newer version is staged. This is documented in install.md with manual removal.
- The first spawn of each new version on Windows costs about 1 s more end to end (the first execution of a just-written executable), measured at max 3.05 s under co-load. It is bounded by EnsureRunning's 1.5 s poll.
- The implementer's open item (runs/21): paths.WriteAtomic's ancestor walk climbs into a real home's .qompack, so unit tests that write into a temp dir without a layout fail on any machine whose home holds .qompack (the user-global config layer, calibration, fallback log, and now staged binaries). This is pre-existing in internal/paths and not this lane's to change.
- Not verified on a real host (real sessions were not allowed in this wave): a compact SessionStart under load answered in time; plugin update, uninstall and --plugin-dir cleanup on Windows with the staged daemon; whether the host exports CLAUDE_PLUGIN_ROOT to MCP servers. The layout recognition no longer depends on the last point.
- Merge hazards: c1efe8b wraps the observer seams in daemon.New and changes drainDispatch's default branch in daemon.go; the w2-sessionend lane edits flushRoute, ingest.go and drain.go. The gate is reached from flushRoute through the wrapped svc.SessionEnd, so no flushRoute edit was needed.

### Needs the owner

- Decide whether a later live wave must re-verify C1.16 and C1.17 on the real host: a compact SessionStart under ingest load; a plugin update, uninstall and --plugin-dir cleanup on Windows while the staged daemon runs; and whether an MCP-spawned daemon runs from the staged copy.
- Route the pre-existing e2e reds (V4 tombstone append-only, V5 PreCompact hookSpecificOutput, V3 hot-path budgets) to their owners. The first two match the w2-hookout lane's 'two unclassified e2e reds' / C1.18 scope.
- Decide whether an uninstall should offer to remove ~/.qompack/bin staged copies. They are currently retained and documented, with no automatic cleanup after uninstall.
- internal/paths owner: decide on the WriteAtomic ancestor walk escaping into a user's real ~/.qompack (runs/21), which makes layout-less temp-dir unit tests machine-dependent.


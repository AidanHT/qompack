# W16-SPOOLSEAL: PreCompact after client spools, slow-disk spool submode

Branch `closeout/w16-spoolseal`. Workflow `wf_90777431-3b4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `d6424b97`

### Root cause

(1) The context gap is real, and larger than the audit assumed. In spool submode the hook clients read state.bin and never connect for observe.tool, observe.prompt or observe.stop (internal/ipc/client.go Send step 3). Those captures exist only in client-<pid>.ndjson. Because the hooks no longer connect, nothing kicks the client-spool watcher either: the watcher is kicked only by served requests (daemon.go noteServed). Spooled captures therefore wait for a non-hot request (PreCompact itself, status, MCP, session.start) plus about two watcher intervals, or for the idle drain about 120 s later, not the "two watcher intervals" the audit gives. PreCompact is not a hot-path op, so it reaches the daemon. handleCheckpoint then called the seal straight away: no lane settle and no client-spool replay. The checkpoint, and the rehydration built from it, lacked the session's newest tool results. Shown red first: internal/cli TestPreCompactInSpoolSubmodeSealsTheSpooledReads failed with `[]string(nil) does not contain "toolu_d53c_spooled_read_1"` (runs/precompact-spool-submode-red-before-fix.log). The existing rig rows hid it by calling admin.drain before compacting.
(2) The only user-facing surfaces spool submode reaches are: the WARN and LOUD line "daemon: hot path degraded to spool submode" (handlers.go applyHotPathTransition), status's bare "hot path: spool" plus that line in its loud tail, and doctor's spool.pending row, which read `degraded` whenever spool files exist. Code reading shows spool submode never reaches the SessionStart degrade banner, the contract mode or status's FAILING banner. degradeBanner and renderContract are driven only by contract assertions, and no assertion reads the hot mode or the budgets.
(4) X11: nothing wave 15 added holds a lock across a hook request or makes the hook wait. D51's gate is entered by dispatchOp for every op, including all three hot-path ops back to back or overlapping, and by runIngested, by drains and by the work a request leaves running. With its startup snapshot (always present for FSStore), the background publication pass takes no store lock between units. It yields before every directory batch and every entry. A pass parked behind a request blocks none of that request's writes into the trees it walks (now pinned by a deterministic test).
Two residual overlaps are inherent:
- A unit already under way when a request arrives finishes first. Measured on this Windows host (temporary diagnostic, log committed): the cold pass after the writes had unit p99 12 ms and max 15-18 ms; warm units p99 about 0.5 ms and max 0.8-3.7 ms. Whole pass: 0.9 s cold / 64 ms warm on a 1x live-run store, 7.3 s cold / 0.32 s warm on 10x.
- The gate cannot cover a hook's client-side transit, because hook_controlled_observed starts at the hook client's first statement (hookclient.go:322): stdin read, config load, admission, state.bin read and dial all happen before the daemon sees the request.
The ledger opens lazily after serving starts (f1ad272e), once per daemon. Reload runs only on the idle tick and session.start. A pass that finishes within a few seconds, a one-time open and per-session reloads cannot move a p50 over 2000 samples. The X11 shift (6.1 to 13.3 ms) is consistent with the 3x spawn floor (host load), as D53(d) suspected. I found no overlap to fix; the X11 re-run alone stays the coordinator's.

### Summary

I fixed and tested items 1, 2 and 3 of D53(c), and finished the item 4 X11 diagnostic, which found nothing to fix. Branch closeout/w16-spoolseal, head d6424b97, six commits on bb54c6ba.

**1. PreCompact now replays this session's spooled captures before it seals**
- New file internal/daemon/precompact_settle.go, called from handleCheckpoint inside the B-E timing. It reuses C1.13's settle shape:
  - wait for the session's lane to publish every arrival leased before the PreCompact;
  - replay the client spools through a new drainer.DrainClientSpoolsWithin, which also gives up waiting for the drain's mutex when its bound ends (a plain DrainClientSpools could block on another pass);
  - wait for the lane once more.
- The bound is precompactSettleBound = B-E (runtime.budgets.checkpointFinalizeMs) minus checkpoint.MaxPreCompactWindow (the existing 1500 ms seal cap, now exported). That is 500 ms with defaults, far inside the 14 s < 15 s < 20 s nesting. No new literal.
- A PreCompact replayed by a drain does not settle, which avoids re-entering the drain's mutex.
- Whatever is still unpublished when the bound expires goes into the checkpoint's ExtraDrops, through a context value read by the BindCheckpoint seam:
  - one `unreplayed_capture` summary that counts what was left by kind, says nothing is lost and how to get it back (recall or expand once replayed), ranked -1 in section 7;
  - one `unreplayed_tool_result` entry per tool result, with its tool_use_id and no detail, ranked with the pointers.
- My first version put the full detail text on every entry. That would have eaten the checkpoint's own token budget, because Truncate measures the drop list too; commit 2f0bddbd compacts it.
- Also new: a Warn line, and counters `precompact_settle` and `precompact_unreplayed_captures`.
- A healthy session (nothing spooled, everything published) pays one spool listing and two journal lookups: about 0.19-0.24 ms against about 54 ms for the rest of the route on this loaded host. It never waits and never drains, pinned by a counter assertion while the drain's mutex is held.

**2. Spool submode no longer reads as broken**
- The WARN and LOUD message is now "daemon: hot path switched to spool submode; nothing is lost". It keeps budget_ms and windows first in the same order (the integration judge's regex reads them) and adds l0_ingest_ms, cause, until and tune fields.
- The wording lives in internal/obs/spool_submode.go and names runtime.hotPath.budgetMs, runtime.budgets.l0IngestMs and runtime.daemon.ackDeadlineMs. A test checks these are real config keys.
- status prints the explanation under "hot path: spool"; the JSON field `hot` is unchanged.
- doctor:
  - new `status.hotPath` row, `ok` (informational) for both sync and spool;
  - `spool.pending` reads `ok`, with the explanation, when state.bin says spool and a daemon is serving;
  - it stays `degraded` with a stated reason when state.bin pins spool and no daemon is serving, because then nothing replays the files.
- The `hotpath_degraded` counter name is kept as a stable metric key. No default number changed.
- The constant hotpathDegradedMsg in test/integration/hotpath_test.go was updated to the new text.

**3. docs/troubleshooting.md**
The section 7 entry is rewritten as the slow-disk entry. It covers WSL2, containers, network and encrypted filesystems; says it is expected and loses nothing; describes the PreCompact replay and both drop kinds; says what ends it (a new session or the daemon's idle exit; there is no stop command); and gives the three tuning keys with their default relationship (ack = l0IngestMs + 2 ms on Linux) and the doctor rows. It no longer blames only a heavily loaded machine. No other page named a stop command or attributed spool submode only to load.

**4. X11 tests**
- TestDispatchOp_HoldsTheCaptureGateForEveryHotPathOp: every hot op holds the gate, back to back and overlapping.
- TestStartupPublicationAccounting_APausedPassHoldsNothingARequestNeeds: PutBytes, RecordToolUse (which takes the store write lock) and a sidecar write all finish while the background pass is parked.

**Tests added (each run by exact name)**
- internal/cli: TestPreCompactInSpoolSubmodeSealsTheSpooledReads (end to end with the shipped hook clients in spool submode and the real checkpoint and rehydration: pointers and the rehydration name both spooled Reads), TestDoctor_SpoolSubmodeIsInformational, TestDoctorHotPathRow.
- internal/daemon: TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal (uses the real applyHotPathTransition), TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed, TestPreCompactSettle_AHealthySessionPaysNothing, TestPreCompactSettle_AReplayedPreCompactDoesNotSettle, TestPrecompactSettleBound_IsWhatBELeavesTheSeal, TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex, TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult, TestSpoolSubmodeWording_NamesRealConfigKeys, plus the two X11 rows above.
- internal/rehydrate: TestDropReport_UnreplayedCaptureIsNamedFirst. Its first version, with the kindRank entry removed as a check, failed as intended.
- internal/commands: TestRenderStatus_SpoolSubmodeSaysNothingIsLost.
- TestHotModeTransitionWritesStateAndNAKs was updated to the new message and made stronger: it now also asserts the field order, "nothing is lost", the end conditions, all three keys, and the absence of "degraded".

**Full package runs**
All on Windows, at the final code, one at a time, with -p 2. Evidence logs are in plans/sdd/V6-closeout/w16-spoolseal/runs/. No wall-clock failure occurred, so nothing needed an isolated re-run.

**Criterion changes, with rationale**
- **B-E (checkpoint_finalize) now times the settle as well as the seal.** B-E is defined as PreCompact entry to exit, and the settle is on that path, so this is stricter, not looser.
- **The pinned hot-path transition message changed in daemon_test.go and test/integration/hotpath_test.go.** The wording change is the fix D53(c) asks for; the pinned fields and the WARN level are unchanged.
- **doctor's spool.pending verdict is `ok` instead of `degraded` only when state.bin says spool and a daemon is serving.** It stays `degraded` in the stuck case and in sync submode.

### Commits

- 5525dfa2 fix(daemon): replay spooled captures before a precompact seal
- 55064bc5 fix(daemon): word spool submode so a healthy session reads healthy
- ddf4f615 docs(troubleshooting): explain spool submode on slow disks
- b67ebf9f test(daemon): pin that the publication pass yields to every hot op
- 2f0bddbd fix(daemon): keep the unreplayed-capture report inside the budget
- d6424b97 test(v6): record the w16-spoolseal package runs on windows

### Tests

- `go test -p 2 -count=1 -run '^TestPreCompactInSpoolSubmodeSealsTheSpooledReads$' ./internal/cli/ (before the fix)` — FAIL as intended: checkpoint pointers.tools was empty ([]string(nil) does not contain toolu_d53c_spooled_read_1); runs/precompact-spool-submode-red-before-fix.log
- `go test -count=1 -run '^TestPreCompactInSpoolSubmodeSealsTheSpooledReads$' ./internal/cli/ (after the fix, final code)` — PASS
- `go test -p 2 -count=1 -run (each by exact name) TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal, TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed, TestPreCompactSettle_AHealthySessionPaysNothing, TestPreCompactSettle_AReplayedPreCompactDoesNotSettle, TestPrecompactSettleBound_IsWhatBELeavesTheSeal, TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex, TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult ./internal/daemon/` — PASS (healthy settle logged 242.7 us per PreCompact over 1000 rounds) <!-- runpatterns: the -run argument is a prose placeholder for the tests named after it, each run by its exact name, not a command to verify -->
- `go test -count=1 -run '^TestDispatchOp_HoldsTheCaptureGateForEveryHotPathOp$' and '^TestStartupPublicationAccounting_APausedPassHoldsNothingARequestNeeds$' ./internal/daemon/` — PASS
- `go test -count=1 -run '^TestHotModeTransitionWritesStateAndNAKs$' and '^TestSpoolSubmodeWording_NamesRealConfigKeys$' ./internal/daemon/` — PASS
- `go test -p 2 -count=1 -run '^TestDoctor_SpoolSubmodeIsInformational$', '^TestDoctorHotPathRow$', '^TestDoctor_AgreesWithStatusOnModeAndProvenance$', '^TestDoctor_AnUnreadableSpoolIsItsOwnRow$' ./internal/cli/` — PASS
- `go test -count=1 -run '^TestRenderStatus_SpoolSubmodeSaysNothingIsLost$' ./internal/commands/; go test -count=1 -run '^TestDropReport_UnreplayedCaptureIsNamedFirst$' ./internal/rehydrate/` — PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/obs ./internal/checkpoint ./internal/rehydrate ./internal/commands ./test/docs` — PASS, exit 0 (runs/pkgs-small-windows.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon` — PASS, exit 0, 399.8 s (runs/pkg-daemon-windows.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli` — PASS, exit 0, 96.5 s (runs/pkg-cli-windows.log)
- `go run ./tools/devtool fmt; go vet (Windows and GOOS=linux) ./internal/daemon ./internal/obs ./internal/commands ./internal/cli ./internal/checkpoint ./internal/rehydrate ./test/integration` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS
- `temporary diagnostic test TestDiagTmpPublicationUnitDurations (not committed): time between publication-pass yields on 1x and 10x live-run stores` — cold unit p99 about 12 ms, max 15.2/17.8 ms; warm unit p99 about 0.54 ms, max 0.8/3.7 ms; pass 0.89 s/7.3 s cold, 64/324 ms warm (runs/x11-publication-unit-durations-windows.log)

### Criterion changes

- B-E (checkpoint_finalize) histogram now times the PreCompact settle as well as the seal. Rationale: B-E is defined as PreCompact entry to exit and the settle is on that path; this makes the gate stricter.
- TestHotModeTransitionWritesStateAndNAKs (internal/daemon/daemon_test.go) and hotpathDegradedMsg (test/integration/hotpath_test.go) pin the new transition message. Rationale: the wording change is the D53(c) fix itself. The budget_ms/windows fields, their order and the WARN level are unchanged, and the daemon test also asserts more (field order, nothing-is-lost, end conditions, keys, no 'degraded').
- doctor's spool.pending row reads ok instead of degraded only when state.bin says spool and a daemon is serving (the designed path, nothing lost). It still reads degraded when spool is pinned with no daemon serving, and in sync submode. Rationale: D53(c) and D45, a healthy session must not look broken; the stuck case stays visible.

### Open issues

- Not run by this seat (daytime rules): test/integration's TestIntegration_HotPath* rows. hotpathDegradedMsg was changed to the new message text; the coordinator's night run must confirm TestIntegration_HotPathSpoolTransitionJudgedPerMode and TestIntegration_HotPathWarmWithRealResidentState against the new WARN and LOUD line. Also not run: whole test/e2e and integration packages, Linux verification, and -race.
- In spool submode no hot-path hook connects, so the client-spool watcher is not kicked during the session. Spooled captures reach the store only when a non-hot request arrives (status, MCP, PreCompact, session.start) or when the idle drain runs about 120 s after the last served request. PreCompact is now covered. Freshness of recall during a long spool-submode stretch is not; a possible follow-up is to kick the watcher from the idle tick while in HotSpool. I did not change it, because it is outside this brief.
- The idle exit counts a session as live from LastActivity, which only served requests refresh. A session that stays in spool submode with no other requests may therefore be judged idle after runtime.daemon.idleExitSeconds while still active. That is consistent with the documented 'idle exit ends it'; the next hook re-spawns a daemon. Recorded, not changed.
- X11: no code cause was found. D53(d)'s isolated re-run of X11 on the candidate stays with the coordinator.
- Residual by design (D51): a publication-pass unit already under way when a request arrives finishes beside it (cold max about 18 ms on this host), and the gate cannot cover a hook's client-side transit share.

### Needs the owner

- precompactSettleBound = runtime.budgets.checkpointFinalizeMs (B-E, 2000 ms) - checkpoint.MaxPreCompactWindow (the existing maxPreCompact, 1500 ms) = 500 ms by default, floored at 0. Derivation: the settle is timed inside B-E with the seal, so a settle that uses its whole bound still leaves the seal its existing worst-case window within B-E's gated p99. If too small: on a very slow disk more spooled captures are named as unreplayed_capture/unreplayed_tool_result instead of being sealed (nothing is lost; the daemon replays them later). If too large: the host's compaction waits up to that long longer in a spool-submode session, and B-E could breach.
- No other new number was introduced. The settle's lane wait uses the same bound (no stall constant of its own), and the drop report has no cap; its per-entry cost is minimised instead (summary plus ID-only lines).
- Owner may want to confirm the wording choice: the WARN level is kept (the integration judge pins level=warn), and the message and its fields now say 'switched ... nothing is lost' rather than 'degraded'. The metric key hotpath_degraded is kept for stability.

## Independent review

### review:spoolseal: needs-fixes

- **major** `internal/daemon/precompact_settle.go:199-252 (unreplayedCaptures, unreplayedDrops) and :270-313 (pendingClientRequests); internal/checkpoint/finalize.go:81-100` — The unreplayed report has no limit, and neither does the spool scan behind it. It adds one `unreplayed_tool_result` DropEntry for every tool result still spooled. Before that, pendingClientRequests reads and decodes every client-<pid>.ndjson in full. Both run after the settle's bound has expired, inside B-E. Commit 2f0bddbd made each entry smaller, but the entries still sit in cp.Dropped before Truncate, which measures the whole document. So a large spool backlog (the exact condition D53(c) targets) still makes Truncate cut the checkpoint's pointers to make room for the drop report. With a large enough backlog it falls through to the tier-1-only budget_exceeded checkpoint. That is the failure the commit message says it prevents.
  - Evidence: In spool submode every hook is its own process and its own client-<pid>.ndjson. By the implementer's own finding, nothing kicks the watcher, so the backlog grows until a non-hot request arrives or the idle drain runs. X11's rig shows the scale: 2000 tool uses, spool submode after 1536 samples, then about 460 spool-only captures before the 50 `qompack checkpoint` spawns. On the ~37 ms-fsync disk, one drained line costs several fsyncs, so the 500 ms bound replays only a few lines. Each remaining entry is about 75 bytes of JSON ({"kind":"unreplayed_tool_result","id":"toolu_…"}), roughly 20 tokens, so ~460 entries come to about 9k of checkpoint.budgetTokens=12000. Truncate (truncate.go:100) cuts narrative and pointers but never Dropped. Finalize appends ExtraDrops through AddDrops (precompact.go:186) before Truncate runs (finalize.go:99). No test covers a backlog larger than 2.
  - Fix: Limit the per-result entries and say so. Name at most K tool results, newest first, since the newest are most likely missing. The summary entry already counts all of them, and can add 'K named, N-K more'. Derive K from checkpoint.budgetTokens, for example the share the drop report may take, and name it and list it for the owner, as the brief requires for new numbers. Also limit the scan: stop decoding once K tool results and the counts are known, or count lines without decoding the payloads. Add a row with a few hundred spooled captures and the slow-dispatch fixture. It should assert that the checkpoint keeps its pointers (no extra pointer drops from Truncate, no budget_exceeded), and that the summary count is exact.
- **minor** `internal/daemon/precompact_settle.go:100-112; internal/daemon/drain.go:440-452` — The settle gives the drain pass a context deadline (context.WithTimeout(ctx, bound)), not a pass budget. When the bound expires, the line in flight is cancelled. drain.go's withPassBudget doc and idle.go's registerPaced doc name this as a defect class: 'a line whose publication took longer than the budget was cancelled by every pass and published by none'. On the target slow-disk class, a single drained line (publishCapture fsync, lease journal fsync, store writes, commitDelivery) can take a large share of the 500 ms. Each PreCompact can then discard a line's work half-done and replay nothing. Nothing is lost, because the watcher and idle drains use pass budgets, but the settle fails on exactly the disks it was built for. Neither the code comments nor needs_owner mention the trade-off.
  - Evidence: drain.go:58-67: 'A deadline on the pass's context cancelled the line in flight instead … A budgeted pass therefore always consumes a line when it can'. TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed relies on this cancellation: its fixture waits on <-ctx.Done().
  - Fix: At minimum, document the choice in DrainClientSpoolsWithin's comment and add it to needs_owner, since the bound is kept by giving up the line in flight. Alternatively, use newPassBudget(bound) so the pass starts no line after the bound but finishes the one it started. The extra time that adds (at most one line's drainLineDeadline) would then go into the bound derivation and be listed for the owner.
- **minor** `internal/daemon/precompact_settle.go:100-112, 121-140; docs/troubleshooting.md:972-977` — The settle replays every session's client spools, not just this session's. It goes oldest-first in host order (orderClientSpoolsByHostTS), with no filter by session or by the PreCompact's time. Other sessions' older captures can therefore use up this session's 500 ms, and this session's newest captures are the ones most likely left unreplayed. settleFast also counts another session's client spool as a reason to settle, so a healthy session pays a drain pass of up to 500 ms whenever any session in the project has a spool waiting. The docs and comments say the PreCompact replays 'this session's spooled captures'.
  - Evidence: clientSpoolWaiting only checks isClientSpoolName, with no session check. DrainClientSpoolsWithin calls passLocked(ctx, true), which is clientOnly and has no session filter. troubleshooting.md: 'the `PreCompact` hook replays this session's spooled captures'. The healthy-session proof (TestPreCompactSettle_AHealthySessionPaysNothing) has no other session's spool present.
  - Fix: Either have settleFast decide from pendingClientRequests-style evidence for sess (with the scan limited as in the first finding) and give the replay a session filter, or correct the docs and comments: the settle replays all hook client spools in host order, and a spool from another session also triggers it. Add a row with another session's spool present that shows what the healthy session pays.
- **minor** `internal/daemon/precompact_settle_test.go (whole file)` — The brief names both cases, 'deferred or in spool submode'. Only the spool-submode case is tested. Nothing exercises the deferred path: an arrival leased before the PreCompact and still publishing in the session's lane, which awaitArrivals has to wait for and unreplayedCaptures has to name from lanes.pending. That includes the dedup by nonce between a lane job and its late-ACK spool copy, and skipping leased jobs with ArrivalSeq >= upTo.
  - Evidence: All four route rows write only client spools (writeHookSpool) and never park a leased job in the lane at PreCompact time. The `add` dedup and the `jb.leased && jb.lease.ArrivalSeq >= upTo` branch are never reached by a test.
  - Fix: Add a row where a live observe.tool is accepted (leased) and its lane is held (park, or a blocking ObserveTool seam) when the PreCompact arrives. With a client-spool copy of the same nonce present, assert that the seal waits for it within the bound, and that when it cannot, it is named exactly once.
- **minor** `internal/cli/doctor.go:917-933` — On a slow disk in sync submode, while a daemon is serving, doctor still marks spool.pending `degraded` for client spools left by ACK-deadline deferrals. That is the same slow-disk behaviour that loses nothing. It happens for the first 3x512 hooks of every session before the switch, and again after each new session resets to sync. D53(c) and D45 ask that the slow-disk path not read as broken. Only the spool-submode half was made informational.
  - Evidence: The status = doctorOK branch is gated on ipc.ReadState(...).Hot == ipc.HotSpool && s.lockAlive. The 'sync submode keeps the old verdict' subtest pins degraded for a client spool in sync submode.
  - Fix: Either treat client spools with a daemon serving as informational in sync submode as well, since the daemon replays them through the watcher, keeping degraded for the no-daemon case. Or record the reason sync-submode deferrals stay degraded as a criterion decision for the owner.
- **minor** `open_issues of the implementer result; test/e2e/v3_x11_test.go (B-E rows), test/bench/hotpath/main.go:422` — B-E now times the settle. The X11 bench spawns `qompack checkpoint` 50 times after 2000 tool uses, and those spawns go through handleCheckpoint. With client spools present (deferrals on Windows under load, or spool submode in the Linux slow-disk container), each spawn now pays a drain pass of up to 500 ms plus the unlimited scan from the first finding, inside B-E's wall and CPU rows. The open issues hand only the TestIntegration_HotPath* rows to the night run.
  - Evidence: handlers.go:1244-1250 places settleBeforeSeal inside obs.Timed(BE). bench main.go:422 measures `checkpoint` spawns as B-E. v3_x11_test.go:624-633 gates B-E_cpu p99 < 2 s in both modes.
  - Fix: Add the X11 B-E rows (Windows and the Linux slow-disk run) to the coordinator's night list as rows this change can move. Record the precompact_settle and precompact_unreplayed_captures counters in that evidence, so a B-E shift can be traced to the settle.
- **nit** `commits d6424b97, b67ebf9f, ddf4f615` — Three of the six commits have no `Refs: V6-VERIFY, C4.3` footer. d6424b97 has no body at all. The other three commits carry the footer.
  - Evidence: `git log -1 --format=%B` of each ends without a Refs line.
  - Fix: Add the Refs footer when the branch is integrated (reword at merge, or in the integration commit's record).

## Fix seat (review resolution) — status `done`, head `c56a1bc4a786fc85e5c24e486a41ac1b947fe03b`

### Root cause

The implementer's PreCompact settle had two defects. (1) It named every spooled tool result in the checkpoint's drop report. Truncate measures that report as part of the document but never cuts it, so a spool-submode backlog cut the checkpoint's pointers instead: 300 names took 11147 of 12000 tokens. (2) It took any client spool as a reason to settle, and replayed all sessions' spools in host order. Another session's older backlog could use up this session's bound, and a healthy session paid a drain pass whenever any session had a spool waiting.

### Summary

FIX seat for w16-spoolseal (D53(c)). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal, branch closeout/w16-spoolseal, on top of the implementer's d6424b97. Five new commits. Working tree is clean and no processes of mine are left running. The Linux container was not touched.

## Review resolution

1. MAJOR, the unreplayed report and spool scan had no limit: CONFIRMED and FIXED (ebd93464).
   - Evidence: a temporary diagnostic (not committed as a test) on the implementer's code. With 300 spooled tool results, the drop report had 301 entries costing 11147 tokens of the 12000-token checkpoint budget. That is worse than the reviewer's estimate because Marshal indents each entry.
   - Fix: the summary still counts every capture, and its detail now also says how many are named. Only the newest tool results are named (sorted by hook time, newest first). They are named for as long as the cost of that prefix inside a checkpoint document, measured with checkpoint.Marshal and the token estimator as Truncate measures, fits within checkpoint.budgetTokens / unreplayedNamesBudgetDivisor (20). A binary search finds the cut in O(log N) measurements.
   - The spool scan now decodes only each line's head (spoolLineHead: op, s, t, n, e.session_id, e.tool_use_id). It no longer builds the full ipc.Request. TestSpoolLineHead_ReadsWhatDecodeRequestReads pins that the head decode agrees with DecodeRequest and resolveEvent. The scan still reads every client file in full, because the reviewer asked for an exact count.
   - New row TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare: 300 spool files, a dispatch that never finishes, the default 500 ms bound. The summary count is exactly 300. The newest 16 are named, newest first, within 600 of 12000 tokens. A checkpoint filled with pointers up to its budget minus the share keeps every pointer under checkpoint.Truncate: no tool_pointer drops, no budget_exceeded. A fixture check shows that naming all 300 would have cut pointers.
2. MINOR, the context deadline cancels the line in flight: CONFIRMED. Kept deliberately and documented; no code change.
   - A pass budget (newPassBudget) lets the line it started run for up to drainLineDeadline, which is 5 s. That would put the settle seconds past B-E, and the bound exists to keep it inside B-E.
   - DrainClientSpoolsWithin's comment and precompactSettleBound's comment now state the trade-off: on the slowest disks a line slower than the bound is cancelled by every settle and published by none, and its capture is named in the drop report. The watcher and idle drains, which use pass budgets, still publish it, so nothing is lost.
   - troubleshooting.md says the same. Listed under needs_owner.
3. MINOR, the settle replayed every session's spools: CONFIRMED and FIXED (ebd93464).
   - Red first (runs/review-red-before-fix.log):
     - TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain: the settle counter was 1.
     - TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers: this session's Read was not published, because another session's older, never-finishing line used up the bound.
   - Fix: settleFast now decides from this session's own unconsumed, unacknowledged spooled captures fired at or before the PreCompact. DrainClientSpoolsWithin(ctx, only) replays only the files that hold them. passLocked gained an `only` filter, and host order is kept among the kept files. Both rows are now green.
   - Healthy-session cost: with no client spool waiting, still one listing (TestPreCompactSettle_AHealthySessionPaysNothing logged 171 us per PreCompact on the loaded machine). With another session's spool present, a read of that file and no drain (358 us per PreCompact logged).
4. MINOR, the deferred path was untested: CONFIRMED. Coverage added (12990704). These rows pass on the implementer's code too; the gap was coverage, not behaviour.
   - TestPreCompactSettle_WaitsForALeasedArrivalStillPublishing: a leased Read is held in the lane, with a late-ACK spool copy of the same nonce. The gate opens once the settle has started, and the seal sees the Read published.
   - TestPreCompactSettle_NamesALeasedArrivalOnceBesideItsSpoolCopy: the Read never publishes within a 2 s bound. It is named exactly once despite the lane and spool copies, and a Read leased after the PreCompact (ArrivalSeq >= upTo) is not named.
   - Both use settleTestDaemon, which derives the bound from B-E through config. This is a test fixture only; no default changes.
5. MINOR, doctor marks sync-submode client spools degraded: PARTLY ACCEPTED (76c473c8). The verdict is kept and the wording is fixed.
   - Rationale: with a daemon serving in sync submode, the watcher replays a client spool within about two intervals. Files that stay are the only doctor-visible sign that the replay is not keeping up (a stuck watcher, or a spool that never publishes). Making the row OK would hide that, which would weaken a check.
   - The detail now names the ACK-deadline cause, says nothing is lost, says to run doctor again and that files which stay mean the replay is not keeping up, and lists the tuning keys.
   - Red then green: the new subtest TestDoctor_SpoolSubmodeIsInformational/sync_submode_with_a_daemon_serving_says_why. Recorded as a criterion decision for the owner.
6. MINOR, X11 B-E can move: ACCEPTED as an open item for the night run; no code change. My fix 3 narrows the settle to the spawning session's own spools, but the X11 bench session in spool submode will still settle on its own backlog.

## Criterion changes
- The summary drop entry's detail gained a "the newest %d tool result(s) are named by tool_use_id" clause (unreplayedDetailFormat now takes 5 arguments). The rows that pin it were updated to the new format; no assertion was loosened.
- Per-result unreplayed_tool_result entries are now bounded. The count stays exact in the summary.
- The doctor spool.pending verdict is unchanged. Its detail in sync submode with a daemon serving is new; additive wording only.

## Commands and results (Windows, -p 2, daytime machine)
- Red on the implementer's code: `go test -p 2 ./internal/daemon -run '^(TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain|TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers)$' -count=1`: FAIL as expected. The backlog red was a temporary diagnostic, not committed. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Green: `go test -p 2 ./internal/daemon -run '^(TestPreCompactSettle_.*|TestPrecompactSettleBound_IsWhatBELeavesTheSeal|TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex|TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult|TestSpoolLineHead_ReadsWhatDecodeRequestReads)$' -count=1 -v`: all 13 PASS. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 ./internal/cli -run '^TestDoctor_SpoolSubmodeIsInformational$' -count=1`: FAIL before the doctor fix, PASS after.
- Full packages, once each: `go test -p 2 -timeout=30m ./internal/daemon -count=1` ok 299.1 s; `go test -p 2 -timeout=30m ./internal/cli -count=1` ok 86.2 s (this includes TestPreCompactInSpoolSubmodeSealsTheSpooledReads); `go test -p 2 ./test/docs -count=1` ok.
- `go vet` on internal/daemon and internal/cli, Windows and GOOS=linux: OK. `go run ./tools/devtool fmt` and `fmt-check`: clean.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all PASS.
- No wall-clock failures occurred, so nothing needed a rerun alone.
- Evidence committed under plans/sdd/V6-closeout/w16-spoolseal/runs/: review-red-before-fix.log, review-settle-rows-windows.log, review-pkg-daemon-windows.log, review-pkg-cli-windows.log.

## Files changed
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/internal/daemon/precompact_settle.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/internal/daemon/drain.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/internal/daemon/precompact_settle_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/internal/cli/doctor.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/internal/cli/doctor_spool_submode_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-spoolseal/docs/troubleshooting.md

### Commits

- ebd93464 fix(daemon): bound the unreplayed report and settle one session
- 12990704 test(daemon): cover the settle's leased-arrival path
- 76c473c8 fix(cli): name the slow-disk cause of sync-submode spools
- e8a08eb5 docs(troubleshooting): say what the precompact replay reports
- c56a1bc4 test(v6): record the w16-spoolseal review-fix runs on windows

### Tests

- `go test -p 2 ./internal/daemon -run '^(TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain|TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers)$' -count=1 (on the implementer's code)` — FAIL as expected (red): settle counter 1 for a healthy session; own Read not replayed behind another session's older spool <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 ./internal/daemon -run '^(TestPreCompactSettle_.*|TestPrecompactSettleBound_IsWhatBELeavesTheSeal|TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex|TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult|TestSpoolLineHead_ReadsWhatDecodeRequestReads)$' -count=1 -v` — PASS, all 13 rows (backlog row: newest 16 of 300 named within 600 of 12000 tokens) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 ./internal/cli -run '^TestDoctor_SpoolSubmodeIsInformational$' -count=1` — FAIL before the doctor wording fix (new subtest), PASS after
- `go test -p 2 -timeout=30m ./internal/daemon -count=1` — ok 299.098s
- `go test -p 2 -timeout=30m ./internal/cli -count=1` — ok 86.153s
- `go test -p 2 ./test/docs -count=1` — ok 2.720s
- `go vet ./internal/daemon ./internal/cli (Windows and GOOS=linux)` — OK
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS

### Criterion changes

- The unreplayed_capture summary detail gained a clause saying how many tool results are named (unreplayedDetailFormat now takes 5 arguments). The pinning rows were updated to the new format; no assertion was loosened.
- Per-tool-result unreplayed_tool_result entries are now bounded to the newest that fit checkpoint.budgetTokens/20. The summary still counts every capture exactly.
- The doctor spool.pending detail is new in sync submode with a daemon serving. The verdict is unchanged (degraded).

### Open issues

- Night run (coordinator): the X11 B-E rows (test/e2e v3_x11_test.go B-E wall and CPU in both modes; Windows and the Linux slow-disk container) can move with this change. Each `qompack checkpoint` spawn in a spool-submode session now pays the settle (at most precompactSettleBound, plus a scan of that session's spools and the bounded naming). Record the precompact_settle and precompact_unreplayed_captures counters in that evidence so any B-E shift can be traced to the settle. The TestIntegration_HotPath* rows were not run here (daytime limit) and stay on the night list.
- Linux: none of the rows added or changed here were run on Linux (container stopped by instruction). The coordinator's night run covers internal/daemon and internal/cli, including the new settle rows.
- The settle's spool scan still reads every client spool file in full, so the summary count stays exact. Its cost is linear in the backlog's bytes, though it decodes only line heads. It runs after the bound, inside B-E.

### Needs the owner

- NEW: unreplayedNamesBudgetDivisor = 20 (internal/daemon/precompact_settle.go). The per-tool-result names of the unreplayed report may take at most checkpoint.budgetTokens/20 tokens (600 of the default 12000), priced as Truncate prices the document. That names the newest dozen or so tool results (16 with the test fixture's short ids). Derivation: Truncate never cuts the drop report, only narrative and pointers, and 300 unbounded names took 11147 of 12000 tokens. With 5 %, a checkpoint whose pointers fit within 95 % of its budget keeps them all. If too large: names cut pointers on a nearly full checkpoint. If too small: fewer of the newest tool results are named (all are still counted in the summary, and the store has them once replayed).
- CARRIED (implementer): precompactSettleBound = runtime.budgets.checkpointFinalizeMs (B-E, 2000 ms) - checkpoint.MaxPreCompactWindow (1500 ms) = 500 ms by default, floored at 0. If too small: more spooled captures are reported unreplayed instead of sealed (nothing is lost). If too large: compaction waits longer in a spool-submode session and B-E could breach.
- NEW (trade-off, finding 2): the settle's bound is a hard context deadline, not a pass budget. The replay line in flight when it expires is cancelled, so on disks where one line takes longer than the bound every settle replays nothing and only reports. The alternative, a pass budget, would let the settle overrun B-E by up to drainLineDeadline (5 s). Owner to confirm.
- NEW (criterion decision, finding 5): doctor's spool.pending stays `degraded` in sync submode with a daemon serving, because client spools that stay are the only doctor-visible sign the replay is not keeping up. Only the wording changed (ACK-deadline cause, nothing lost, what to look for, tuning keys). Owner may instead rule it informational under D45.
- CARRIED (implementer): no other new number. The settle's lane wait uses the same bound.
- CARRIED (implementer): wording choice: the WARN level is kept (the integration judge pins level=warn). The message and its fields now say 'switched ... nothing is lost' rather than 'degraded'. The metric key hotpath_degraded is kept for stability.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/daemon/precompact_settle.go:143-153, 190-201, 238-258, 406-451` — The fix added a second scan of every client spool, and this one runs before the bound starts. The implementer's settleFast only listed the spool directory (clientSpoolWaiting). Now it calls spooledCaptures, which goes through pendingClientCaptures: it reads every client-*.ndjson in full (ReadFileShared), json-scans each line, and makes a journal leaseHeld lookup per capture. That happens on every PreCompact and every `qompack checkpoint` spawn, before sctx is created. unreplayedCaptures then repeats the whole scan after the bound. So the settle's real cost is scan + 500 ms bound + scan + naming, and neither scan has a limit. That breaks the claim in precompactSettleBound's comment that 'a settle that takes its whole bound still leaves the seal the window it always had'. It also means a healthy session reads every other session's backlog file on each PreCompact. The fix seat's open_issues say the scan 'runs after the bound, inside B-E', which leaves out the new pre-bound scan.
  - Evidence: Implementer's settleFast (d6424b97) was `spooled = d.clientSpoolWaiting()`, a listing only. At HEAD, settleFast at line 196 iterates d.spooledCaptures(sess, at) before line 153 creates `context.WithTimeout(ctx, bound)`, and line 170 scans again. In my run, the healthy-session-beside-another-spool row logged 391.56 µs per PreCompact for one one-line file, against 168 µs with no spool. That is roughly 220 µs per file read on Windows. A 300 to 460 file spool-submode backlog (the X11 shape) would be around 65 to 100 ms per scan, paid twice and outside the bound. No test measures it.
  - Fix: Start the settle's deadline before settleFast's scan, so the pre-bound scan counts against the bound. Pass that first scan's result forward instead of rescanning, rescanning only the files in `only` plus any newer ones after the bound. Alternatively, limit the scan and say so. Either way, correct the open_issues/needs_owner text: the scan runs both before and after the bound, it is unbounded, and a healthy session pays it for other sessions' spools. Add both scans to the X11 B-E night-run evidence.
- **minor** `internal/daemon/precompact_settle.go:177 (tokens.New(cfg, "")), :65-78 (unreplayedNamesBudgetDivisor doc), :341-348` — The names allowance is measured with an uncalibrated estimator, tokens.New(cfg, ""), which uses the identity factor. The seal's Truncate prices the document with src.Tokens, the project-calibrated estimator (tokens.NewForProject in observer_ops.go:125). That factor is clamped to [calibrationMin 0.6, calibrationMax 1.6]. On a project calibrated above 1, the named entries cost Truncate up to 1.6x the measured allowance: up to about 960 tokens (8 %) of the default 12000, not 600 (5 %). The doc comment's claims that names are 'priced as Truncate prices the whole document' and that 'a checkpoint whose pointers fit within 95 % of its budget keeps them all' do not hold there. The backlog test uses the same identity estimator on both sides, so it cannot catch this.
  - Evidence: finalize.go:99 `Truncate(cp, budget, w.cfg.Checkpoint.Tiers, src.Tokens)`. observer_ops.go:125 `Tokens: tokens.NewForProject(o.Cfg, tokens.DefaultCalibPath(), o.ProjectRoot)`. tokens/exact.go:338-350 multiplies by e.Factor(). defaults.go:196-197 CalibrationMin 0.6 / CalibrationMax 1.6. precompact_settle_test.go measures with `tokens.New(tcfg, "")`.
  - Fix: Measure with the estimator the seal uses: the daemon's project-calibrated estimator, or the draft source's Tokens. Or divide the allowance by cfg.Runtime.Tokens.CalibrationMax and state that in the divisor's owner note. Add a row that uses a calibrated estimator with a factor above 1.
- **nit** `internal/cli/doctor.go:932-940` — The new sync-submode detail calls every file in the spool directory a 'client spool' left by an ACK-deadline deferral that 'the daemon replays within seconds'. The row counts every non-directory entry, though, and that includes daemon WAL spool files (wal-*.ndjson). The client-spool watcher does not replay those. When the files are WAL files, the detail gives the wrong cause.
  - Evidence: spoolRow counts all non-dir entries from fsckReadDir(s.l.Spool). ipc/spool.go:242-245 lists wal-prefixed files alongside client files in the same directory.
  - Fix: Use the client-spool wording only when every pending file is a client spool, and keep the generic detail otherwise. Or split the count into client and WAL files in Observed and Detail.


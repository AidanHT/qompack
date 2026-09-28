# V6 close-out w3-startroute: SessionStart route follow-ups (D11, replayed probe, cold-start wait)

Branch `closeout/w3-startroute`. Workflow `wf_85543bfd-f18`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `50967ad`

### Root cause

(1) D11: two failure branches of the rehydration service returned an empty output: a fatal checkpoint read (errFatalCheckpoint) and a build error. The route then answered a compact SessionStart with the §12.1 probe alone, and a Rehydrate seam returning an error got the same treatment. A panic already reached the note through the ticket, but the service itself returned empty. (2) drainDispatch sends a replayed session.start through dispatchOp and handleSessionStart. That handler minted a sentinel into an answer nobody received, and two prompts later hook.additional_context_delivered failed critically. Checking the other replayed ops turned up the same class of bug in four more places. (a) A replayed PreCompact re-armed session_start.source_compact after its compact start had already arrived. (b) A replayed startup fired before a pending PreCompact was taken for the compact start it announced. (c) Replays called noteServed, which spent Run's one re-drain before Serve accepted anything. (d) Prompt scans counted a miss that could not have been a miss: a prompt older than the probe, a prompt from another session (a replayed start mints nothing, so an earlier session's probe stays current), or the same prompt delivery handled twice. The last happens because runIngested scans before it captures: a failed capture is retried, and the drain's Seen set is in memory, so a restarted daemon redelivers. At 8d80aa6 the cold-start diagnostic showed one seed prompt counted twice (chances=2) in 3 of 18 compact rows with nothing spooled. (3) The cold-start wait (1.5 s EnsureRunning poll plus the 250 ms connect floor) is enough at ordinary machine load: 0 of 108 rows spooled. Under full CPU co-load it spools routinely. A spooled startup or resume then loses only that start's probe, and no longer degrades anything. A spooled compact loses its rehydration to the client's explicit note. A Windows first spawn from a freshly staged copy is not what makes this routine; load is.

### Summary

# w3-startroute (resumed seat): D11, replayed session.start probe, cold-start wait

All three items are done. Item (3) was measured and I did not change the wait; the owner has the options below.

## Merge and draft review
- Merged `closeout/integration` `6aff949` as `3a4d2f8`. No file overlapped; build and vet were clean.
- I reviewed the first seat's 7 commits (`5b7317e`..`d9393a0`) and kept all of them, with new commits on top. Each change is correct and in scope, and each has a red-first row:
  - replayed start: `4ecefb8`
  - compact-obligation replay order: `485a1c1`
  - replay is not a served request: `a493458`
  - D11: `0954251`
  - a pre-existing gocritic finding in the route: `b1e6671`
  - docs: `d9393a0`
- Evidence from the pause was not reused as proof: everything relied on was re-run at `8d80aa6`, and again at the final `6fc6f48`.

## Item 1: D11, the deferred note for unbuildable compactions
**Fix.** `rehydrateService.notBuilt` covers four failures:
- unreadable store: `DeferredCheckpointUnreadable`
- build error or panic: `DeferredFailed`
- a Rehydrate seam that returns an error: fails the ticket with `DeferredFailed`

For each, it:
- fails the route's ticket at once (counted `session_start_compact_deferred` and Loud, like any deferral);
- returns the note to any other caller;
- builds nothing on a store it cannot read.

A checkpoint that fails verification is still stepped over and delivered degraded; that boundary is pinned by `TestService_UnverifiableCheckpointStillRehydratesWithoutIt`. Degraded-passive and the reinjection switch still answer nothing (`TestSessionStartCompact_DegradedPassiveAnswersNothingForABrokenStore`). The note stays at or under 1,000 host chars for every reason, hostile session ids included (`TestCompactDeferredNote_FitsTheHostCap`).

**How dropped() reports it (my decision, never "delivered").** One `rehydration` entry, id `not-built`, with detail `not delivered: <cause>; <what the model received>; the checkpoints are kept in .qompack/checkpoints/ …`. No items, zero tokens, `Degraded: true`.
- `<what the model received>` is normally "the model received a deferred note instead".
- For a compaction replayed from a spool it is the replay's own wording, "the hook answered without it (the model received a deferred note, or nothing)". I added this (`94f5ea2`, red first `runs/28`): the first seat's version claimed a note the daemon cannot know was delivered.
- Documented in architecture.md §7 and troubleshooting.md.

**Rows per failure class:**
- Unreadable store:
  - service over a fake reader: `TestService_CheckpointErrorAnswersWithTheDeferredNote`
  - service over the real reader with a broken manifest: `TestService_UnreadableCheckpointManifestAnswersWithTheDeferredNote`
  - route: `TestSessionStartCompact_UnreadableCheckpointStoreIsAnsweredWithTheNote`
  - real binary: `TestE2E_SessionStartCompactUnreadableCheckpointStore`, new in `8d80aa6`. On an export of `6aff949` it answered the probe alone (`runs/32`); at head it passes (`runs/33`).
- Corrupt/fatal checkpoint: `TestService_UndecodableCheckpointAnswersWithTheDeferredNote`
- Build error: `TestService_BuildFailureAnswersWithTheDeferredNote`, `TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote`
- Panic: `TestService_PanicRecovered`, `TestSessionStartCompact_PanickingRehydrationServiceIsAnsweredWithTheNote`
- Hook client passes it through whole and under the cap: `TestSessionStartCompact_DaemonsFailureNoteReachesTheHostWhole` (internal/cli)

## Item 2: the replayed session.start probe, and the same class elsewhere
**Fix at the source.** A replayed start (`spoolReplay(ctx)`):
- does its durable bookkeeping: registry, contract run and observations, session count, the observer's SessionStart, and a compact start's undelivered drop report;
- mints no probe and answers empty;
- owes a degrade banner it cannot show to the next live start (`lastAnnouncedMode`);
- withdraws the probe and banner of its own lost live answer, matched by nonce (`withdrawLostStartAnswer`).

The w2-hookout diagnostic is now a real test, `TestE2E_SpooledSessionStartNeverDegradesTheProject`. It fails on `6aff949` because the replay mints a probe (`runs/59`) and passes at head.

**Audit of every op the drain replays:**
- session.start: fixed as above. A start fired before a pending PreCompact does not resolve `session_start.source_compact`.
- checkpoint (PreCompact): a replay re-arms that obligation only if no start of the session was seen since its hook fired (`StartedSince`). It still seals its checkpoint. Its output was already discarded (C1.18).
- dispatchOp: a replay no longer calls `noteServed`, so it no longer releases the re-drain or kicks the spool watcher.
- observe.prompt (new in this seat): replays are capture-only, and thrash warnings are not consumed. A miss now counts only from:
  - a prompt of the session the probe was minted for (`5e090a3`, red `runs/26`). Without this, a replayed session's own two prompts could still degrade the project through an earlier session's probe.
  - a prompt sent after the probe was minted (first seat, `5b7317e`).
  - a delivery nonce not already counted (`588d38c`, red `runs/45`). This is a real defect my head-level measurement surfaced: 3 of 18 compact rows at `8d80aa6` degraded with no spooling, one prompt counted twice (`runs/40` history dumps).
- observe.tool and observe.stop: capture only, nothing the host sees.
- flush: goes through `flushRoute` or the async end, never `dispatchOp`.
- status: read-only.
- mcp: never spooled (nopSpool clients).
- admin.*: skipped.

**Scope note.** `588d38c` adds `SentinelState.MissedBy` (omitempty, capped at 8, reset wherever Chances is) and `RecordSentinelScanOf` in internal/contract/history.go. That is outside the lane's listed files; see needs_owner. The frozen history golden is unchanged and internal/contract passes in full.

**Flush row.** `TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget` passes 10 of 10 on Linux at `6fc6f48`. It does not interact with the replay guard: its only spooled line is a flush, which drainDispatch routes to `flushRoute`, never to `dispatchOp`/`handleSessionStart`.

## Item 3: cold-start wait (no product change)
The diagnostic runs at head through the real binary (source in `runs/zz_startroute_coldstart_diag_test.go.txt`, never in the tree).

| Load | Run | Spooled | What it shows |
|---|---|---|---|
| Machine load | `runs/40` (`8d80aa6`) | 0/54 | all modes × startup/resume/compact; 3 degraded rows were the double count fixed in `588d38c` |
| Machine load | `runs/49` (`6fc6f48`) | 0/54 | 0 degraded; staged-cold adds about 0.45 s (wall p50 0.75 s vs 0.30 s) |
| Full CPU co-load, heavier | `runs/50` | unstaged startup/resume 9/12 | daemon reachable after 2.3–16 s, one never within 30 s; aborted in a compact seed |
| Full CPU co-load | `runs/51` | staged-cold startup 2/5, compact 5/5; prespawned startup 0/5, compact 1/5; unstaged 0/10 | cold stage + first run: daemon up p50 11.7 s, max 17.1 s; hook wall up to 13.4 s |

**What a spooled first SessionStart loses:**
- startup/resume: only that start's §12.1 probe. Every spooled row ended `mode=full` with no probe minted, and the replay did the bookkeeping.
- compact: the rehydration. The model gets the client's explicit "did not answer in time" note, and the drop report says it was not delivered.

**Why I made no change.** The only content loss is on compact. In production a compact SessionStart follows a PreCompact whose lazy spawn already started the daemon, with the whole summarization in between. A cold stage happens once per plugin version per user, and its first start is a startup or resume. Load, not staging, is what makes spooling routine: at machine load staging never spooled.

**Two things the lifetime lane should see:**
- Session-start's pre-send step (staging plus the 1.5 s poll) runs outside the 10 s reply deadline and has no bound against the 15 s manifest timeout. One co-load row took 21.2 s (`runs/50`, row 0).
- In that same row the daemon took the lock and wrote state.bin, then could not be dialled for 30 s and was gone from the process list.

## Criterion changes (rationale)
- `TestService_CheckpointErrorEmitsNothing` is now `TestService_CheckpointErrorAnswersWithTheDeferredNote`, and `TestService_PanicRecovered` now requires the note. Owner decision D11 replaces silence with the note. Zero builds and exactly one Loud are still asserted.
- `hook.additional_context_delivered` chances now come only from prompts of the probe's own session, sent after it was minted, once per delivery nonce. Those other prompts could never have found the probe. The severed-delivery negative control (x14, in `TestV5_EveryContractAssertionHasARealProducer`) still degrades.
- `session_start.source_compact` no longer resolves against a replayed start fired before the PreCompact, and a replayed PreCompact no longer re-arms after a start. Otherwise Qompack's own replay order fails a critical assertion.
- The degrade banner is owed, never dropped, and a replay is not a served request.

No threshold, budget, timeout or golden was changed, and there are no skips or nolint.

## Checks at the final head (`6fc6f48`; later commits add evidence only)
Windows:
- build and `go vet ./internal/contract/ ./internal/daemon/ ./internal/cli/ ./test/e2e/`: ok
- fmt-check: ok (`runs/52`)
- `go test ./internal/contract/ -count=1`: ok (`runs/53`)
- `go test ./internal/cli/ -count=1 -timeout=30m`: ok, 52 s
- `go test ./internal/daemon/ -count=1 -timeout=30m`: ok, 389 s (`runs/54`)
- `go test ./test/fault/ -count=1 -timeout=40m -v`: ok, 20 pass (`runs/55`)
- `go test ./test/docs/ -count=1`: ok (`runs/48`)
- gen-command-docs, gen-config-docs and gen-mcp-docs `--check`: all ok (`runs/31`)
- 21 focused e2e rows: all pass (`runs/56`)
- devtool lint without stubskips: nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers and coveragefloors pass. golangci-lint and runpatterns fail only on pre-existing findings (`runs/57`).

Linux, non-root with -race (`runs/linux-final`), no data races:
- touched packages in full: contract 160 pass, cli 374 pass, daemon 1495 pass with 1 fail and 1 pre-existing Windows-only skip. The fail was `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest`, which ran while my Windows daemon package loaded the machine. Re-run alone it passed 10 of 10; its path never reaches the changed code.
- flush row ×10: 10 pass
- new and changed rows ×20: contract 40, daemon 1100, cli 300, all pass

Evidence map: `runs/01`–`25`, `c1`–`c7` and `linux/` are the first seat's. `runs/26` onward, `linux-head/` and `linux-final/` are this seat's.

### Commits

- 5b7317e fix(daemon): count no probe miss for a prompt older than its probe (first seat, kept)
- 4ecefb8 fix(daemon): mint no probe for a replayed session.start (first seat, kept)
- 485a1c1 fix(daemon): let no replay re-arm or misresolve a compact start (first seat, kept)
- a493458 fix(daemon): do not take a replayed request for a served one (first seat, kept)
- 0954251 fix(daemon): answer an unbuildable compaction with the deferred note (first seat, kept)
- b1e6671 refactor(daemon): write the session.start seam wait as a switch (first seat, kept)
- d9393a0 docs: document replay rules and the D11 deferred note (first seat, kept)
- 3a4d2f8 chore(v6): merge closeout/integration 6aff949
- 5e090a3 fix(daemon): count no probe miss from another session's prompt
- 94f5ea2 fix(daemon): say what a replayed unbuildable compaction answered
- 14bc0c0 docs: state which prompts count as a probe's chances
- 8d80aa6 test(e2e): pin D11's note for an unreadable checkpoint store
- 06a530c docs(daemon): say when the sentinel scan has no probe to look for
- 588d38c fix(contract,daemon): spend one probe chance per prompt delivery
- 6fc6f48 docs: count a prompt once however often it is redelivered
- eeec278 docs(closeout): record the w3-startroute runs
- 50967ad docs(closeout): record the spooled-start red on 6aff949

### Tests

- `go test ./internal/daemon/ -run '^TestSentinelScan_' -count=1 -v` — red before 5e090a3 and 588d38c (runs/26: 2 misses from another session's prompts; runs/45: chances 2 for one retried or redelivered prompt), then PASS (runs/27, runs/46)
- `go test ./internal/daemon/ -run '^TestService_ReplayedUnbuildableRehydrationSaysWhatTheHookAnswered$' -count=1 -v` — red before 94f5ea2 (runs/28), then PASS (runs/29)
- `go test ./test/e2e/ -run '^TestE2E_SessionStartCompactUnreadableCheckpointStore$' -count=1 -v -timeout=10m` — FAIL on an export of 6aff949: probe alone, no note (runs/32); PASS at head (runs/33, runs/56)
- `go test ./test/e2e/ -run '^TestE2E_SpooledSessionStartNeverDegradesTheProject$' -count=1 -v -timeout=10m` — FAIL on an export of 6aff949: the replayed start minted a probe (runs/59); PASS at 6fc6f48 (runs/56)
- `go test ./internal/contract/ -run '^TestRecordSentinelScanOf_' -count=1 -v` — PASS, 2 rows
- `go test ./internal/contract/ -count=1` — ok at 6fc6f48 (runs/53); the frozen history golden is unchanged
- `go test ./internal/cli/ -count=1 -timeout=30m` — ok, 52.4 s, at 6fc6f48 (runs/53)
- `go test ./internal/daemon/ -count=1 -timeout=30m` — ok, 389 s, at 6fc6f48 (runs/54)
- `go test ./test/fault/ -count=1 -timeout=40m -v` — ok, 411 s, 20 top-level pass, at 6fc6f48 (runs/55)
- `go test ./test/docs/ -count=1` — ok (runs/48)
- `go test ./test/e2e/ -run '^TestE2E_SessionStart' -count=1 -v -timeout=30m, then one command for each other focused row (full list in runs/56)` — 21 rows PASS at 6fc6f48, including TestE2E_AllSixHooksExitZero, TestHooksExitZeroUnderFaults and TestV5_EveryContractAssertionHasARealProducer (the severed-delivery negative control still degrades)
- `go run ./tools/devtool fmt-check` — exit 0 at 6fc6f48 (runs/52)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors` — exit 1: only pre-existing failures, internal/daemon/spawn_stage_test.go:312 errcheck (unchanged since 54a4334) and plans/sdd/V6-closeout/w3-e2ereds/report.md:107 and :119 (runpatterns); every other sub-check PASS (runs/57, runs/58)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute --out <runs/linux-final> 6fc6f48 touched-race --timeout 40m -- ./internal/contract ./internal/daemon ./internal/cli` — contract 160 pass; cli 374 pass; daemon 1495 pass, 1 fail (TestDeliveryOrder_LaneOverflowIsDrainedOnRequest, under concurrent Windows load), 1 pre-existing Windows-only skip; no data races
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute --out <runs/linux-final> 6fc6f48 laneoverflow-alone-x10 --run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 10 -- ./internal/daemon` — PASS 10/10 alone: the failure above was load-induced
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute --out <runs/linux-final> 6fc6f48 flushpass-x10 --run '^TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget$' --count 10 -- ./internal/daemon` — PASS 10/10 (also 10/10 at 8d80aa6)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute --out <runs/linux-final> 6fc6f48 changed-rows-x20 --run <prefix alternation of the new and changed rows, exact text in runs/linux-final/changed-rows-x20-host.log> --count 20 --timeout 60m -- ./internal/contract ./internal/daemon ./internal/cli` — contract 40, daemon 1100, cli 300, all pass; no data races
- `cold-start diagnostic (runs/zz_startroute_coldstart_diag_test.go.txt, copied into test/e2e temporarily, never committed), run in 4 configurations: machine load at 8d80aa6 and 6fc6f48; CPU co-load twice at 6fc6f48` — machine load 0/108 spooled; co-load: routine spooling (runs/50: 9/12 unstaged startup/resume; runs/51: staged-cold 2/5 startup, 5/5 compact), with every spooled start ending mode=full

### Criterion changes

- TestService_CheckpointErrorEmitsNothing is now TestService_CheckpointErrorAnswersWithTheDeferredNote: an unreadable checkpoint store answers with the deferred note, not empty output (owner decision D11). Still pinned: nothing is built (zero estimator calls) and there is exactly one Loud.
- TestService_PanicRecovered now requires the deferred note (DeferredFailed) instead of empty output (D11: a panic is a failed build). Recovery, the nil error and the single Loud are unchanged.
- hook.additional_context_delivered: a transcript miss counts as one of the two chances only from a prompt of the session the probe was minted for, sent after it was minted, and once per delivery nonce. Finds count from any transcript, and an unknown session, timestamp or nonce counts as before. Rationale: the excluded prompts could never have found the probe, so counting them blamed the host for Qompack's own replays, redeliveries or other windows. The severed-delivery negative control in TestV5_EveryContractAssertionHasARealProducer still degrades.
- session_start.source_compact: a replayed start fired before the pending PreCompact runs its contract checks with the obligation held back, so the obligation stays pending for the start that follows; a replayed PreCompact re-arms only if no start of the session was seen since its hook fired. Rationale: otherwise Qompack's own replay order fails a critical assertion.
- Degrade banner: a mode change caused by a replay is counted at once and announced by the next live start, where before the replay spent it; the replay of a start whose answer was lost owes that answer's banner again.
- dispatchOp: a replayed request no longer counts as served, so it no longer releases the one re-drain after the first served request and no longer kicks the spool watcher.

### Open issues

- Pre-existing lint failures outside this lane: golangci-lint errcheck at internal/daemon/spawn_stage_test.go:312 (unchecked type assertion d.(*daemon), unchanged since 54a4334); runpatterns at plans/sdd/V6-closeout/w3-e2ereds/report.md:107 (the alternation is split at the pipe) and :119 (a '^<name>$' placeholder). Both arrived via the integration merge.
- withdrawLostStartAnswer and SessionRegistry.StartedSince are in memory only. A daemon restart between a late live answer and its replay, or between a compact start and a replayed PreCompact, loses the correction; so does a replay that overtakes its own still-running live route. All three are narrow; the doc comments say so.
- A replayed start's contract run counts toward §12.1's two-clean-run restore, and a start handled both live and by its own replay counts twice. Unchanged.
- The exact interleaving behind runs/40's double count was not reproduced on demand (0 of 66 targeted iterations, runs/41-44). The fix works at the invariant level (one chance per delivery nonce, persisted), and deterministic rows cover the retry and restart redelivery paths.
- TestDeliveryOrder_LaneOverflowIsDrainedOnRequest failed once on Linux while my Windows internal/daemon run loaded the machine; it passed 10 of 10 alone, and its path never reaches the changed code. This may be load sensitivity worth watching.
- The w2-lifetime nits are still open: the ctx.Done arm in awaitCompactAnswer says DeferredNotReady for a shutdown, and a build cancelled by Stop says 'building it failed'.
- No real Claude Code session was run (not allowed). D11 and the replay behaviour are proven by unit, route and real-binary e2e rows only.

### Needs the owner

- Item (3), the cold-start wait: I did not change it. At machine load 0 of 108 SessionStarts spooled. Under full CPU co-load spooling is routine; startup/resume then lose only the probe (harmless now), and compact loses its rehydration to the client's explicit note. Every option below would be a new bound and needs your decision: (a) let session-start keep polling a daemon it just spawned for longer, within its reply budget; (b) bound session-start's pre-send step (staging plus poll) so the whole hook stays under the 15 s manifest timeout — one co-load row took 21.2 s, and a staged-cold start reached 13.4 s; (c) have EnsureRunning honour a fresh spawn.lock instead of spawning a second daemon.
- Route to the lifetime lane: under full CPU co-load one daemon took daemon.lock and wrote state.bin, then could not be dialled for 30 s and was gone from the process list (runs/50, the NOUP row). Not investigated here.
- Please ratify the §12.1 counting change as a criterion change: a hook.additional_context_delivered chance is now one prompt delivery of the probe's own session, sent after the probe was minted.
- Please ratify the scope expansion into internal/contract (588d38c): SentinelState.MissedBy (json missed_by, omitempty, capped at 8) and SessionHistory.RecordSentinelScanOf. The field is new and persisted in history.json, but additive; the frozen golden is untouched.
- Please confirm the D11 dropped() wording: one 'rehydration' entry with id 'not-built' and detail 'not delivered: <cause>; <what the model received>; the checkpoints are kept in .qompack/checkpoints/ …'. It has no items and zero tokens, and is marked degraded.

## Independent review

### review:startroute: needs-fixes

- **minor** `internal/daemon/rehydrate_service.go:286 (latest default branch) and :147 (errFatalCheckpoint -> notBuilt)` — When Stop cancels a compact rehydration, the new D11 path reports a healthy checkpoint store as unreadable. startReplyWork cancels the rehydration's context through promptCtx. The shipped fileReader.Latest -> load then returns context.Canceled as soon as the manifest has an entry. latest() puts every error that is not NotFound/NotImplemented into errFatalCheckpoint, so OnCompact now answers with DeferredCheckpointUnreadable. It also writes a durable drop report, 'not delivered: the checkpoint store could not be read …', and dropped() keeps showing it afterwards, pointing the user at fsck for a store that is fine. Before D11 this path was silent, so the wrong cause is new with this lane. It is also worse than the acknowledged w2-lifetime nit that a cancelled build says 'building it failed': this one blames the store. The implementer's own row TestService_BuildFailureAnswersWithTheDeferredNote names 'the daemon stopping under it' as the real trigger for a build error, but it uses the fake reader, which ignores ctx, so it never hits this path.
  - Evidence: I copied HEAD 50967ad into the scratchpad (git archive) and added one scratch row: rsRealReaderService + rsWriteCheckpointArtifact(root, `{}`) + an already-cancelled ctx -> OnCompact. The output note contained '(the checkpoint store could not be read)', and state Dropped[0].Detail = 'not delivered: the checkpoint store could not be read, so no rehydration was built; the model received a deferred note instead; …'. The row failed as predicted, and the scratch copy was deleted afterwards. Code path: checkpoint/reader.go:229 load(ctx) -> :421 ctx.Err() -> Latest returns the ctx error -> rehydrate_service.go:286-288 Loud 'checkpoint unreadable' + errFatalCheckpoint -> :147-151 notBuilt(DeferredCheckpointUnreadable, notBuiltCheckpointUnreadable).
  - Fix: In latest(), handle cancellation before the default branch, e.g. `case ctx.Err() != nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):` returning a distinct sentinel such as errCheckpointCancelled, with no 'checkpoint unreadable' Loud. OnCompact should map that sentinel to notBuilt(ctx, sess, DeferredStopping, "not delivered: the daemon was shutting down, so no rehydration was built"), and should give a buildErr that wraps context.Canceled the same treatment. Add a real-reader row: a valid manifest entry plus a cancelled ctx must answer DeferredStopping, and its drop report must not mention an unreadable store.
- **nit** `docs/architecture.md:140` — The doc says 'Each time comparison is between the hooks' own timestamps.' sentinelMissCounts does not do that: it compares the prompt hook's req.TS with Sentinel.MintedAt. MintedAt is the daemon's own clock at the top of handleSessionStart (`now := core.NowMilli(d.clk)`), not the start hook's timestamp. The behaviour is safe, because a live prompt's hook fires after the daemon answered the start. Only the doc sentence is inaccurate.
  - Evidence: handlers.go: `h.Sentinel.MintedAt = now` in the mint branch, and `return promptTS <= 0 || promptTS >= s.MintedAt` in sentinelMissCounts. Only heldBack and StartedSince compare hook time with hook time.
  - Fix: Reword it, for example: 'The PreCompact/SessionStart comparisons use the hooks' own timestamps; a prompt's timestamp is compared with the instant the daemon minted the probe.'
- **nit** `commits 6fc6f48, 14bc0c0, 06a530c` — Three commits that change product docs or a code comment in the lane's scope have no `Refs: V6-VERIFY, C1.16/D11` footer, while every fix/test commit in the lane has one. Some integration docs commits also omit it, so this matches local precedent, but traceability to D11 is uneven.
  - Evidence: `git log --format='%h %s | %(trailers:key=Refs)' 6aff949..HEAD` shows an empty Refs for 6fc6f48 'docs: count a prompt once…', 14bc0c0 'docs: state which prompts count…' and 06a530c 'docs(daemon): say when the sentinel scan…'. No attribution trailers are present anywhere, as required.
  - Fix: When this lane is integrated, either add the Refs footer to these docs commits or record that docs-only commits are exempt.

## Fix seat (review resolution) — status `done`, head `8023548`

### Root cause

Stop cancels a compact rehydration's context through promptCtx (startReplyWork). Once that context is cancelled, the shipped checkpoint reader's load returns ctx.Err() as soon as the manifest has an entry. rehydrateService.latest() filed every error that was not NotFound or NotImplemented under errFatalCheckpoint, so a healthy store got the Loud line 'rehydrate: checkpoint unreadable', the note DeferredCheckpointUnreadable and a lasting 'checkpoint store could not be read' drop report. A build cut short the same way (BuildWithStats returns only ctx.Err()) was reported as DeferredFailed.

### Summary

## w3-startroute fix seat: final state

The reviewer found one minor issue. I reproduced it and fixed it. I wrote the failing tests first, on both the shipped checkpoint reader and the live session.start route. Branch closeout/w3-startroute is at HEAD 8023548 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-startroute. It is not pushed and not merged. The working tree is clean.

### Review resolution

| Finding | Verdict | Action |
|---|---|---|
| minor: when Stop cancels a compact rehydration, a healthy checkpoint store is reported as unreadable (rehydrate_service.go latest default branch, errFatalCheckpoint -> notBuilt) | CONFIRMED | FIXED in c028b39 |

**Root cause (verified).**
- startReplyWork runs the rehydration under `context.WithCancel(context.WithoutCancel(ctx))`. Only promptCtx cancels it, and only Stop cancels promptCtx.
- Once that context is cancelled, the shipped `fileReader.Latest` calls `load(ctx, entry)`. `load` returns `ctx.Err()` before it reads anything (internal/checkpoint/reader.go, load). So a manifest with one entry is enough to trigger this.
- `latest()` then filed every error that was not NotFound or NotImplemented under `errFatalCheckpoint`. The result:
  - a Loud line "rehydrate: checkpoint unreadable";
  - the note said "(the checkpoint store could not be read)";
  - the durable drop report said "not delivered: the checkpoint store could not be read …". `dropped()` kept serving that report, and troubleshooting.md sends readers to `qompack fsck` for this cause.
- The same thing happened on the build side. `rehydrate.BuildWithStats` never returns any error except `ctx.Err()` (see its own doc comment). So every "build failed" answer was really a Stop, and it was reported as `DeferredFailed`.
- One small correction to the finding: the drop report itself never mentions fsck. The fsck pointer comes from troubleshooting.md, which pairs that cause with fsck. The substance of the finding is correct.

**Red first** (runs/60-review-cancel-rows-red-windows.log, run on the pre-fix tree at 50967ad plus the new tests; all three rows failed for the predicted reason):
- `TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping`: the note said "(the checkpoint store could not be read)".
- `TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping`: the route answered with the unreadable-store note.
- `TestService_BuildFailureAnswersWithTheDeferredNote` (updated row): the note said "(building it failed)".

**Fix (c028b39, internal/daemon/rehydrate_service.go):**
- New `cutShort(ctx, err)`. It returns true only when `ctx.Err() != nil && errors.Is(err, ctx.Err())`, meaning the error is the context's own end. A real store error that happens while the context is still live is still reported as unreadable.
- `latest()` now returns a new `errCheckpointCutShort` for that case, with no unreadable-store Loud.
- `OnCompact` sends both a cut-short checkpoint read and a cut-short build through a new `stopped()`. It answers:
  - note reason: `DeferredStopping` ("the Qompack daemon was shutting down");
  - drop report: new `notBuiltStopping` = "not delivered: the Qompack daemon was shutting down, so no rehydration was built"; after it, as before, "; the model received a deferred note instead" (or the replay wording) and the checkpoints pointer;
  - one Loud line, "rehydrate: stopped before the rehydration was built". That keeps the Loud count the same as before; only the cause is now correct.
- These cases keep their existing causes and Louds: a real store failure (`DeferredCheckpointUnreadable`), a seam error or panic (`DeferredFailed`), degraded-passive and the reinjection kill switch (still answer nothing).
- `DeadlineExceeded` is also treated as the daemon stopping. That is correct in the daemon because startReplyWork strips request deadlines; the reasoning is documented on `cutShort`.
- Doc comments updated in session_start_compact.go (header, `DeferredStopping`, `DeferredFailed`, `counterCompactDeferred`) and on `notBuilt` / `recordNotBuilt`.

**Tests (c028b39):**
- New `TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping` (internal/daemon/rehydrate_service_test.go). It is the reviewer's suggested row: shipped reader, a real marshalled golden checkpoint, and a cancelled ctx. It requires:
  - the `DeferredStopping` note, under the host cap;
  - zero estimator calls and exactly one Loud, which must not be "rehydrate: checkpoint unreadable";
  - a never-built drop report naming the shutdown and not "checkpoint store could not be read".

  It then reads the same store with a live ctx and requires a real, non-deferred rehydration, which proves the store was healthy.
- New route row `TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping` (internal/daemon/session_start_compact_failure_test.go). A `stoppingReader` blocks until its context ends, then returns `ctx.Err()`. The test calls `promptCancel()` mid-read, which is what Stop does. The route must answer the `DeferredStopping` note plus the §12.1 probe, counted and Loud'd as a deferral. `dropped()` must lead with `notBuiltStopping`.
- Small test-only helpers: `rsLogger.loudMsgs()`, and `newFailureFixtureOver` (the failure fixture over any `checkpoint.Reader`; `newFailureFixture` delegates to it).

**Docs (4a2240f):**
- docs/troubleshooting.md lists "the Qompack daemon was shutting down" among the daemon's own causes, says it is not a store or build fault, names its LOUD.log line, and says what `dropped()` records for it.
- docs/architecture.md now counts a rehydration a stopping daemon "cuts short" among those answered with the note.

### Criterion change (written rationale)

`TestService_BuildFailureAnswersWithTheDeferredNote` used to require `DeferredFailed` ("building it failed") and the report "building the rehydration failed" for a build given a cancelled context. It now requires `DeferredStopping` and "the Qompack daemon was shutting down", plus a NotContains check for "building the rehydration failed".

Rationale: the only error `BuildWithStats` can return is its context's. In the daemon that context ends only when Stop cancels it, so the old expectation pinned the wrong cause. The w2-lifetime report had already noted this as a nit, and the reviewer asked for exactly this treatment.

Nothing is loosened: still exactly one Loud, still a nil error, still the note (not silence), still recorded as never built. Failures on the build's own merits stay pinned to `DeferredFailed` by `TestService_PanicRecovered` and `TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote`. The row's name is kept because committed run logs quote it. The rationale is also written into the row's doc comment.

### Commands and results

All `go test` runs used `-count=1 -timeout=30m` unless noted. Exit codes were captured with `echo exit=$?`, never through a pipe. Logs are committed under plans/sdd/V6-closeout/w3-startroute/runs/.

- **Red:** one invocation over these three names, `go test ./internal/daemon -v`, alternating in `-run`:
  - `-run '^TestService_BuildFailureAnswersWithTheDeferredNote$'`
  - `-run '^TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping$'`
  - `-run '^TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping$'`

  Result: exit 1, all three FAIL as predicted (runs/60).
- **Same three after the fix:** exit 0, 3/3 PASS (runs/61). With `-count=20`: 60/60 PASS, exit 0 (runs/67).
- `go run ./tools/devtool fmt-check`: exit 0. `go vet ./internal/daemon`: exit 0. `go test ./test/docs`: ok, exit 0 (runs/62).
- **Focused daemon rows:** `go test ./internal/daemon -v` with `-run` prefixes `'^TestService_'`, `'^TestSessionStartCompact_'`, `'^TestCompactDeferredNote_'` and `'^TestRehydrate'`, alternated in one invocation. 42 PASS, exit 0 (runs/63).
- **Full package once:** `go test ./internal/daemon`: ok 292.251s, exit 0 (runs/64). There were no wall-clock failures, so nothing needed a solo re-run.
- **`go run ./tools/devtool lint --only=runpatterns,docmarkers,sleepcheck`** (runs/65):
  - sleepcheck PASS, docmarkers PASS;
  - runpatterns FAIL, only on two lines in plans/sdd/V6-closeout/w3-e2ereds/report.md (107, 119). That failure is identical to runs/58 at eeec278 and comes from the committed w3-e2ereds report, not from this lane.
- **`go run ./tools/devtool lint --only=golangci-lint,nomagic`** (runs/66):
  - nomagic PASS;
  - golangci-lint FAIL, only on internal/daemon/spawn_stage_test.go:312 errcheck. That file is on the integration base 6aff949 (657a31c) and the same failure is in runs/57. Nothing in the files I touched was flagged.
- **Linux, non-root (uid 10001), -race, exact commit 4a2240f**, via `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute`:
  - Changed rows x20 (`--count 20`, `-run` prefixes `'^TestService_'`, `'^TestSessionStartCompact_'`, `'^TestCompactDeferredNote_'` alternated) on ./internal/daemon: PASS 860/0/0. Each of the three new or changed rows passed 20 times (runs/linux-review/review-rows-x20-host.log and its artifacts).
  - Full ./internal/daemon: PASS 1498, fail 0, skip 1. The skip is `TestDrainWindowsOpenBlobRetainsCleanupIntent`, which is Windows-only. go_test_exit=0 (runs/linux-review/review-daemon-race-host.log and its artifacts).

internal/cli was not touched: the client passes the daemon's note through unchanged, and `DeferredStopping` was already a route reason. So the cli package was not re-run in this round.

### Open items (not from this lane)

- `devtool lint` runpatterns is red on the committed plans/sdd/V6-closeout/w3-e2ereds/report.md: line 107 has an alternation the parser splits into an invalid regexp, and line 119 has a `^<name>$` placeholder. This is for the coordinator or whoever owns that report.
- golangci-lint errcheck at internal/daemon/spawn_stage_test.go:312 is on integration base 6aff949 (657a31c) and belongs to the owner of that test.

### Owner decisions

None new from this round. Treating a cancelled rehydration as `DeferredStopping` is a correctness fix within D11, not a new bound.

### Commits

- c028b39 fix(daemon): blame no store for a rehydration Stop cut short
- 4a2240f docs: name a rehydration a stopping daemon cut short
- 8023548 docs(closeout): record the w3-startroute review-fix runs

### Tests

- `go test -count=1 -timeout=30m ./internal/daemon -v with -run alternating '^TestService_BuildFailureAnswersWithTheDeferredNote$', '^TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping$', '^TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping$' (pre-fix tree)` — exit 1: all three FAIL for the predicted reason (runs/60) <!-- runpatterns: the word after -run describes how the three quoted anchored names were run in turn; each is a real test in ./internal/daemon -->
- `same three rows after the fix` — exit 0, 3/3 PASS (runs/61)
- `same three rows, -count=20` — exit 0, 60/60 PASS (runs/67)
- `go test -count=1 -timeout=30m ./internal/daemon -v with -run prefixes '^TestService_', '^TestSessionStartCompact_', '^TestCompactDeferredNote_', '^TestRehydrate' alternated` — exit 0, 42 PASS (runs/63) <!-- runpatterns: the word after -run describes how the four quoted prefixes were combined; each prefix names real tests in ./internal/daemon -->
- `go test -count=1 -timeout=30m ./internal/daemon` — ok 292.251s, exit 0 (runs/64)
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon; go test -count=1 ./test/docs` — all exit 0 (runs/62)
- `go run ./tools/devtool lint --only=runpatterns,docmarkers,sleepcheck` — sleepcheck PASS, docmarkers PASS; runpatterns FAIL only on plans/sdd/V6-closeout/w3-e2ereds/report.md:107,119, same as runs/58 on eeec278, not this lane (runs/65)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic` — nomagic PASS; golangci-lint FAIL only on internal/daemon/spawn_stage_test.go:312 errcheck, which is on base 6aff949 (runs/66)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute 4a2240f review-rows-x20 --run (TestService_/TestSessionStartCompact_/TestCompactDeferredNote_ prefixes) --count 20 -- ./internal/daemon` — non-root -race PASS 860/0/0; each new or changed row passed 20 times (runs/linux-review)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-startroute 4a2240f review-daemon-race --timeout 40m -- ./internal/daemon` — non-root -race PASS 1498, fail 0, skip 1 (TestDrainWindowsOpenBlobRetainsCleanupIntent, Windows-only), go_test_exit=0

### Criterion changes

- TestService_BuildFailureAnswersWithTheDeferredNote: its cancelled-context build now requires DeferredStopping and a 'the Qompack daemon was shutting down' drop report instead of DeferredFailed and 'building the rehydration failed'. Reason: the only error BuildWithStats can return is its context's, and in the daemon that context ends only when Stop cancels it, so the old expectation pinned the wrong cause. It still requires one Loud, a nil error, the note, and a never-built report, and it adds a NotContains check. Failures on the build's own merits stay pinned to DeferredFailed by TestService_PanicRecovered and TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote.

### Open issues

- devtool lint runpatterns is red on the committed plans/sdd/V6-closeout/w3-e2ereds/report.md lines 107 and 119 (an alternation the parser splits into an invalid regexp, and a ^<name>$ placeholder). Not this lane; the coordinator or the report's owner should fix it.
- golangci-lint errcheck at internal/daemon/spawn_stage_test.go:312 is on integration base 6aff949 (657a31c), not this lane.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



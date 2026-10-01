# W16F-SETTLEKICK: the PreCompact kicks the watcher after its seal

Branch `closeout/w16f-settlekick`. Workflow `wf_fc5fef3b-1b3`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `6f3169f7`

### Root cause

(1) dispatchOp calls noteServed for every live request, before routing (internal/daemon/handlers.go:246). noteServed (daemon.go) kicked the client-spool watcher, the PreCompact (ipc.OpCheckpoint) included. A kicked watcher runs a budgeted pass (DrainClientSpools, idleRunBudget) over every session's client spools in host order, under the drainer's mutex. The PreCompact route's settle replays the compacting session's own spools through DrainClientSpoolsWithin, which waits for that mutex with lockWithin up to the settle's bound. So a pass started by the PreCompact's own kick could hold the mutex over another session's older backlog while the settle waited, spending this session's bound on other sessions' spools (w16e-settlewin product check item 5).

Timing, from reading lookAtClientSpools, so the scope is clear. An idle watcher woken by a kick first marks the spools it has not seen as unsettled. It passes them at the trailing look one interval later (spoolCheckInterval = 2 s). With the defaults, a pass started by the PreCompact's own kick therefore overlaps the 500 ms settle only when the route's work before the seal runs about 2 s, or when B-E is configured so the bound exceeds the interval. A watcher already resting between looks does not wake on a kick, so a pass it starts during the settle is "another reason" (D55 degrade). The defect is real and the fix removes it completely, but at the default settings the window is narrower than "every PreCompact".

(2) Since c71740c2, spoolHeadIndex.heads also returns ok=false for any read error other than not-exist. Such a spool is counted unread, but unreadSpoolsClauseFormat still said only "could not be read within the bound".

### Summary

W16F-SETTLEKICK: the PreCompact's own watcher kick now comes after its seal, and the unread-spools clause covers failed reads.

Note on scope: the kick lives in daemon.go (noteServed), but its op-aware call site and the PreCompact route are in handlers.go, which the rearm seat also edits. My handlers.go hunks are small and separate from the observe.prompt code:
- dispatchOp line 246: `d.noteServed()` became `d.noteServed(req.Op)`.
- handleCheckpoint: a 7-line block right after the B-E-timed seal block, before Phase 3.

The coordinator may see a textual conflict only if rearm touched those exact lines.

## What changed

**0569d34d fix(daemon): kick the spool watcher after the precompact seal**
- daemon.go: `noteServed(op ipc.Op)` still releases firstServed for every request. It skips the watcher kick only for ipc.OpCheckpoint. Its doc comment says why.
- handlers.go dispatchOp: passes req.Op.
- handlers.go handleCheckpoint: kicks the watcher (`if !spoolReplay(ctx) { d.kickSpoolWatch() }`) after the seal block, so the kick happens once per live PreCompact, whether or not a seam is bound.
- Not changed: every other kick, the idle tick's kick, the drain mutex, lockWithin and every bound.
- precompact_settle.go, new paragraph in the header doc: the PreCompact's kick comes after the seal. A pass already running for another reason (an earlier request's kick, the idle tick, a drain the lanes ask for) is still waited for within the bound. That wait is the designed D55 degrade: what the bound leaves is named in the drop report and replayed later, nothing lost.
- spool_watch.go and daemon.go field comments now say a PreCompact kicks after its seal.
- spool_watch_test.go: `dd.noteServed()` became `dd.noteServed(ipc.OpStatus)` (signature only).
- New row TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked (precompact_settle_test.go), deterministic, no clock. The watcher is not running at first, and the kick channel is checked empty before the PreCompact.
  - At the settle's first spool read, the seam checks whether a kick is pending. If so, it starts the real watcher (startSpoolWatch), as an idle watcher would wake on that kick, and holds until the watcher's pass has the other session's older line in flight.
  - That line, if it is in flight before the seal, takes the whole bound (settleCut), as in ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers.
  - It asserts that the session's own spooled Read is published at the seal and that there are no drops.
  - It then asserts that exactly one kick is pending after the route returns. Finally it starts the watcher and checks that the other session's spool is published.

**bda82028 fix(daemon): word the unread-spools clause for failed reads too**
- The clause is now "; %d hook client spool file(s) could not be read within the bound or failed to read, so this session's captures in them, if any, are not counted here".
- The constant's doc comment, the sealReport doc and the settleBeforeSeal doc say the same.
- docs/troubleshooting.md (the PreCompact paragraph of the spool-submode entry) now says "a file it had no time to read, or failed to read (a sharing violation, an anti-virus lock, an I/O error), is counted ... as not read".
- Grep of the tree: the only other pins of the text are two rows that use the constant itself (ALookPastTheBoundReadsNothingAndSaysSo, ASpoolItCannotReadIsCountedNotTakenAsEmpty), so they follow it automatically. No plan or docs page outside sdd reports quotes the clause, and the section 7 text in internal/rehydrate renders the detail rather than quoting it. Earlier sdd reports and logs quote the old text as historical evidence; I left them unchanged.
- TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty gains `require.Contains(detail, "or failed to read")`.

**6f3169f7**: evidence logs in plans/sdd/V6-closeout/w16f-settlekick/runs/.

## Red first
- The new kick row against the base product code: FAIL at precompact_settle_test.go:579 in 0.29 s, "the settle replays the session's own spool without waiting behind a pass its own request kicked" (runs/red-kick-before-settle-base.log).
- The same row with the fix in place but the post-seal kick removed temporarily (restored from a scratch copy; never committed): FAIL at :585, "the PreCompact kicks the watcher once it has sealed" (runs/red-no-kick-after-seal.log).
- The clause row against the base: FAIL at precompact_settle_retry_test.go:120 (runs/red-unread-clause-base.log).

## Tests (Windows, -p 2, machine shared with two other seats)
| Command | Result |
|---|---|
| `go test -p 2 -count=1 -run '^TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked$' -v ./internal/daemon`, base product code | FAIL as intended |
| `go test -p 2 -count=1 -run '^TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty$' -v ./internal/daemon`, base product code | FAIL as intended |
| Both rows by exact name as an anchored alternation, `-count=10 -v`, after the fix | 20/20 PASS, ok 7.4 s (runs/green-new-rows-count10.log) |
| `go test -p 2 -count=1 -timeout=30m ./internal/daemon` (whole package once, tree with both changes) | ok 426.0 s (runs/daemon-full.log). After that run, only comment rewraps changed. |
| `go test -p 2 -count=1 ./test/docs` | ok 5.0 s |
| gen-command-docs, gen-config-docs, gen-mcp-docs `--check` | exit 0 |
| `go run ./tools/devtool fmt-check`; `go vet ./internal/daemon`; `GOOS=linux go vet ./internal/daemon` (final head) | PASS |
| Lint subset | golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck and docmarkers PASS. runpatterns FAILs only on base-committed reports: w16d-warnlate/report.md:164,169,192,198 and w16e-settlewin/report.md:92 ("<anchored ..." placeholders). Not from this seat. |

Every -run pattern quoted here matches a real test name exactly. No wall-clock failure occurred, so nothing needed a re-run alone. No Linux container, no Docker, no -race, no load generator. My background test run and monitors have ended.

## Criterion changes
None weakened. One assertion was added to ASpoolItCannotReadIsCountedNotTakenAsEmpty (the failed-read wording). The new row is a new pin. The spool_watch_test.go change is a call-signature update only.

## Owner decisions
No new budget, bound or constant. precompactSettleBound, idleRunBudget, spoolCheckInterval and drainLineDeadline are unchanged. The row uses the existing fixtures liveOrderBound and spoolWatchTick.

### Commits

- 0569d34d fix(daemon): kick the spool watcher after the precompact seal
- bda82028 fix(daemon): word the unread-spools clause for failed reads too
- 6f3169f7 docs(v6): record the w16f-settlekick evidence logs

### Tests

- `go test -p 2 -count=1 -run '^TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked$' -v ./internal/daemon (base product code)` — FAIL as intended at precompact_settle_test.go:579, own Read not replayed before the seal (runs/red-kick-before-settle-base.log)
- `same row with the post-seal kick removed temporarily (not committed)` — FAIL as intended at :585, no kick after the seal (runs/red-no-kick-after-seal.log)
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty$' -v ./internal/daemon (base clause)` — FAIL as intended at precompact_settle_retry_test.go:120 (runs/red-unread-clause-base.log)
- `go test -p 2 -count=10 -run '^(TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked|TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty)$' -v ./internal/daemon` — PASS 20/20, ok 7.4 s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon` — ok 425.986 s
- `go test -p 2 -count=1 ./test/docs` — ok 5.0 s
- `go run ./tools/devtool gen-command-docs --check / gen-config-docs --check / gen-mcp-docs --check` — exit 0 each
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon; GOOS=linux go vet ./internal/daemon` — PASS
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS except runpatterns, which fails only on base-committed w16d-warnlate and w16e-settlewin report lines

### Criterion changes

- TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty: added require.Contains(detail, "or failed to read") to pin the wording for failed reads. This is strictly stronger, and all existing assertions are kept.
- New row TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked (precompact_settle_test.go).
- spool_watch_test.go TestSpoolWatch_DoesNothingWithoutAKick: dd.noteServed() became dd.noteServed(ipc.OpStatus) for the new signature. Behaviour is unchanged.

### Open issues

- handlers.go is shared with the rearm seat. This seat's hunks are dispatchOp's noteServed call (line 246) and a 7-line kick block after handleCheckpoint's seal. Check for a textual conflict when merging both seats.
- Scope of the fixed defect: an idle watcher woken by the PreCompact's kick passes new spools only at its trailing look, one spoolCheckInterval (2 s) later. With the default 500 ms bound, the own-kick overlap needed slow pre-seal route work (about 2 s) or a configured bound longer than the interval. The fix removes it in all cases.
- A watcher pass already running, or one a resting watcher starts during the settle for another reason (an earlier request's kick, the idle tick, a lane-requested drain), still makes the settle wait in lockWithin within its bound. This is the designed D55 degrade (named in the drop report, nothing lost), documented in precompact_settle.go. The drain mutex was not redesigned.
- runpatterns lint fails on the base: plans/sdd/V6-closeout/w16d-warnlate/report.md:164,169,192,198 and w16e-settlewin/report.md:92 need the coordinator's usual waivers.
- Ran on Windows only (daytime limits). The changes were vetted with GOOS=linux but not run in the Linux container.

## Independent review

### review:settlekick: sound

- **minor** `internal/daemon/handlers.go:1361-1366` — The PreCompact's watcher kick moved from noteServed (before the handler) to a plain statement after the seal block. If svc.PreCompact or settleBeforeSeal panics, callHandler (handlers.go:314) recovers and refuses the request, and this kick never runs. Before this change the request had already kicked the watcher in dispatchOp. Nothing is lost: the next served request, the idle tick in spool submode or the idle drain still picks up other sessions' spools. But a panicking PreCompact now delays them, which the old code did not.
  - Evidence: handleCheckpoint: `_ = obs.Timed(... d.settleBeforeSeal(...); d.svc.PreCompact(sealCtx, *ev) ...)` and then `if !spoolReplay(ctx) { d.kickSpoolWatch() }`, with no defer. callHandler's recover returns {OK:false, Err:"panic: ..."} without reaching the kick.
  - Fix: Optional, and contained to the same hunk. Register the kick with defer right before Phase 2, so it runs after the seal on both the normal path and the panic path: `if !spoolReplay(ctx) { defer d.kickSpoolWatch() }` placed before the seal block. Or leave the code as is and say in the doc comment that a panicking route does not kick.
- **nit** `internal/daemon/spool_watch.go:26-29` — Rewrapping the comment left a ragged short line, "// watcher looks at the spool", in the middle of the paragraph. Separately, "a PreCompact once it has sealed" is slightly loose wording: the route also kicks when no seam is bound or mode.MayAct() is false, in which case there is no seal.
  - Evidence: diff: `// spool submode, when no hook request is served at all (kickSpoolWatchInSpoolSubmode). A kicked` / `// watcher looks at the spool` / `// directory once per spoolCheckInterval ...`
  - Fix: Reflow the paragraph. Optionally reword to "a PreCompact after its seal step".

## Fix seat (review resolution) — status `done`, head `0df91326`

### Root cause

(1) Every served request called noteServed, which kicked the client-spool watcher before the route ran. For a PreCompact, that kick could start a watcher pass over every session's spools. The pass holds the drain mutex, so the settle's DrainClientSpoolsWithin waited behind it (lockWithin) and spent this session's 500 ms bound on other sessions' backlog. (2) Since c71740c2 (wave 16d), unreadSpoolsClauseFormat also counts spools that failed to read (sharing violation, AV lock, I/O error), but its wording still said only "could not be read within the bound". (Review) The implementer moved the PreCompact's kick to a plain statement after the seal block in handleCheckpoint. A panic in settleBeforeSeal or svc.PreCompact is recovered by callHandler into a refusal, so it skipped the kick, which the old noteServed-before-route kick did not do.

### Summary

## w16f-settlekick: final state (fix seat)

Branch closeout/w16f-settlekick, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16f-settlekick, HEAD 0df91326. Base is closeout/integration @ 8fe6c1f4.

### What changed (implementer, already reviewed)
- internal/daemon/daemon.go: `noteServed(op ipc.Op)` no longer kicks the spool watcher for ipc.OpCheckpoint. handlers.go dispatchOp passes req.Op.
- internal/daemon/handlers.go handleCheckpoint: the route now makes the PreCompact's kick itself, after the seal.
- internal/daemon/precompact_settle.go: the doc comment explains the ordering. A watcher pass that is already running for another reason (an earlier kick, the idle tick, a lane-requested drain) is still waited for within the bound. That is the D55 degrade: the drop report names what is left and nothing is lost. The drain mutex is not redesigned.
- `unreadSpoolsClauseFormat` now reads "...could not be read within the bound or failed to read, so this session's captures in them, if any, are not counted here". Its doc comment, the sealReport and settleBeforeSeal comments, and docs/troubleshooting.md were updated to match. Rows pinning the text build it from the constant; TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty also checks for "or failed to read".
- New row TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked, shown red on the base (runs/red-kick-before-settle-base.log) and with the after-seal kick removed (runs/red-no-kick-after-seal.log).
- spool_watch.go and spool_watch_test.go: comment updates and the `noteServed(ipc.OpStatus)` call site.

### What changed (fix seat, 74d4188a)
- handlers.go handleCheckpoint: the kick is now `if !spoolReplay(ctx) { defer d.kickSpoolWatch() }`, registered right at the start of the route. The plain statement after the seal is removed. It still runs after the seal (when the route returns), and it now also runs when the route panics anywhere and callHandler refuses the request. The noteServed doc comment in daemon.go was updated to match.
- New row TestPreCompactSettle_APanickingSealStillKicksTheWatcher (precompact_settle_test.go): the seal panics, and the row checks that the request is refused, the handler-panic counter is 1, and exactly one kick is pending.

### Review resolution
- Finding (minor, handlers.go:1361-1366, "a panicking PreCompact no longer kicks the watcher"): **confirmed and fixed.** Before the fix, the new row failed for the right reason: its fixture checks passed, and the final assertion found "should have 1 item(s), but has 0" (runs/red-review-panic-no-kick.log). I took the reviewer's defer option but registered the defer at route entry instead of right before Phase 2, so a panic in Phase 1 (history save, marker write) also still kicks. The settle still cannot be overtaken: during the settle the defer has not run, and the 16f row (whose read seam checks `len(dd.spool.kick)==0` during the settle) stays green.

### Commands and results (fix seat; `-p 2`, daytime limits, no -race, no Docker)
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_APanickingSealStillKicksTheWatcher$' ./internal/daemon` before the fix: FAIL as expected (red).
- `go test -p 2 -count=10 -run '^(TestPreCompactSettle_APanickingSealStillKicksTheWatcher|TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked|TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty|TestSpoolWatch_DoesNothingWithoutAKick)$' -v ./internal/daemon`: exit 0, 40/40 PASS (runs/green-review-count10.log).
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon`: ok in 1399.6 s on a loaded machine, against 426 s for the implementer's run; nothing failed (runs/daemon-full-review.log).
- `go vet ./internal/daemon` and `GOOS=linux go vet ./internal/daemon`: both exit 0.
- `go run ./tools/devtool fmt` then `fmt-check`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all PASS except runpatterns. Its 5 failures are all in committed reports this seat did not write: plans/sdd/V6-closeout/w16d-warnlate/report.md lines 164, 169, 192, 198 and w16e-settlewin/report.md line 92, which use placeholder `-run "<anchored ...>"` patterns. With my changes stashed, runpatterns also exits 1. My changes add no doc changes, so `go test ./test/docs` was not re-run; the implementer's test-docs.log shows ok.
- Implementer's runs, already committed: runs/red-unread-clause-base.log, red-kick-before-settle-base.log, red-no-kick-after-seal.log, green-new-rows-count10.log, daemon-full.log (ok 426 s), test-docs.log (ok).

### Criterion changes
None. No assertion was loosened, no budget or timeout changed, nothing skipped. The clause text change is the wording fix the task asked for; rows pin it through the constant, and one row gained a stricter Contains check.

### Open items
- The runpatterns lint fails on the integration base because of the w16d-warnlate and w16e-settlewin reports (placeholder -run patterns). The coordinator owns those reports.
- No Linux run, by the daytime seat rules (no Docker). The change is OS-neutral; GOOS=linux vet is clean.

### needs_owner
None: no new budgets or bounds. The implementer's list was empty.

### Commits

- 0569d34d fix(daemon): kick the spool watcher after the precompact seal (implementer)
- bda82028 fix(daemon): word the unread-spools clause for failed reads too (implementer)
- 6f3169f7 docs(v6): record the w16f-settlekick evidence logs (implementer)
- 74d4188a fix(daemon): kick the spool watcher when a precompact panics too (fix seat)
- 0df91326 docs(v6): record the w16f-settlekick review-fix evidence (fix seat)

### Tests

- `go test -p 2 -count=1 -run '^TestPreCompactSettle_APanickingSealStillKicksTheWatcher$' ./internal/daemon (before fix)` — FAIL as intended: no kick pending after a panicking seal (runs/red-review-panic-no-kick.log)
- `go test -p 2 -count=10 -run '^(TestPreCompactSettle_APanickingSealStillKicksTheWatcher|TestPreCompactSettle_DoesNotWaitBehindAWatcherPassItsOwnRequestKicked|TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty|TestSpoolWatch_DoesNothingWithoutAKick)$' -v ./internal/daemon` — PASS, 40/40, exit 0
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon` — ok 1399.646s, exit 0 (machine loaded)
- `go vet ./internal/daemon && GOOS=linux go vet ./internal/daemon` — exit 0 both
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS except runpatterns: 5 failures, all in committed w16d-warnlate/w16e-settlewin reports, and the base fails the same way

### Criterion changes

- unreadSpoolsClauseFormat wording widened to 'could not be read within the bound or failed to read' so it is truthful for the wave-16d I/O-failure path; rows pin it through the constant, and TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty gained an extra Contains check (stricter, not looser)

### Open issues

- runpatterns lint fails on the integration base: placeholder -run patterns in plans/sdd/V6-closeout/w16d-warnlate/report.md:164,169,192,198 and w16e-settlewin/report.md:92 (not this seat's files)
- No Linux run this wave, by the daytime no-Docker rule; GOOS=linux vet is clean

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



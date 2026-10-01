# W16F-REARM: re-arm a warning whose answered prompt was spooled

Branch `closeout/w16f-rearm`. Workflow `wf_fc5fef3b-1b3`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `972879be`

### Root cause

A successful reply claim only means the daemon will send a reply carrying the thrash warning, not that the hook receives it. A claim won just inside ts+promptReplyDeadline can reach the hook after the hook's own give-up. ipc client.awaitReply then spools the prompt (it spools a reply request only when no reply line was read or decoded). The rule stays in WarnedRules although the host never saw the warning, and no path that handles the spooled copy (drain.go absorbs it, since the live copy is already acknowledged) re-armed it. The fix: remember which nonce's claimed reply carried which rules (in memory, one per session), and re-arm those rules when the drain consumes a client-spooled observe.prompt with that nonce.

### Summary

W16F-REARM: closes the thrash-warning loss that w16d-warnlate left open (its mode-(b) residual), on branch closeout/w16f-rearm in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16f-rearm, cut from closeout/integration 8fe6c1f4. All three tasks are done, and every row and package I touched is green.

Resume note: the worktree already held uncommitted work from an earlier run of this seat (the product change, both rows and evidence logs). I reviewed all of it before keeping it. One row in that work was flaky, and I fixed it (see "Row determinism" below). I re-ran every red and green myself on the final code and replaced the old evidence logs.

## Root cause
A successful claim (observer.WithPromptReplyClaim, w16d) means the daemon will send a reply that carries the warning. It does not mean the hook receives that reply. The claim can win just inside ts+promptReplyDeadline while the rendered reply reaches the hook after the hook's own give-up. ipc client.awaitReply then spools the prompt; it does that only when no reply line was read or decoded. The warning was drained from PendingThrash and its rule stayed in WarnedRules, so the rule was silenced for the rest of the session although the host never saw it. Nothing that later handled the spooled copy re-armed the rule. The exact signal: the drain consumes a client-spooled observe.prompt whose nonce the daemon already answered live with a warning. Normally it absorbs that copy without dispatch, because the live copy is already acknowledged.

## What changed
- internal/observer (6f952adc):
  - New context key WithPromptReplyNonce.
  - When a claimed reply carries a warning, promptReplyOutput stores sessionState.ReplyWarning{Nonce, Rules}. It lives in memory only, one per session (the latest replaces the earlier one), and is not added to persistedState, so there is no new persisted format.
  - New seam SpooledReplyRearmer.PromptReplySpooled(session, nonce). On a nonce match it re-arms exactly those rules the way a refused claim does: out of WarnedRules, multiplicity floor held at max(queued uses, current uses), any queued copy dropped, counted under the existing observer.thrash_undelivered and logged at Info. It is one-shot, and it never creates state for an unknown session.
  - rearmUndelivered now shares the logic through rearmRules (its behaviour is unchanged).
  - The state.go comment now lists ReplyWarning as non-persisted.
- internal/daemon (19688bfb):
  - callObservePromptWithDeadline takes req.Nonce and attaches it to the reply context.
  - New field DrainConfig.SpooledPromptSettled. drainFile calls it after it consumes a client-spool (isClientSpoolName) observe.prompt line, on both the main read loop and the reattempt path. It is called with the drain mutex held, which is the same drain-mutex-then-session-lock order Dispatch already uses.
  - daemon.settleSpooledPrompt hands the nonce to Services.PromptReplySpooled, which WireObserver binds.
  - The "Residual (w16d-warnlate, carried)" comment is replaced.
- Item 2 (ac0016a4, comments only, handlers.go): the callObservePromptWithDeadline doc no longer says the WAL line "for observe.prompt runs only the sentinel scan"; it now says runIngested captures the prompt under the leased identity (SP08-D3) and that this call is reply-only. Two constant comments in the same file made the same false claim, and I corrected them too: counterPromptReplyLate (it spoke of a "verbatim capture it started") and counterPromptReplayedUncaptured (now described as retired at zero, as drainDispatch says).
- Item 3 (92a44e8b): the test-e2e job's QOMPACK_NONREFERENCE_DISK comment in ci.yml now names X10's late or deferred prompt-reply recovery branch (written to the job summary) and points at internal/obs/nonrefdisk.go as the full list.

## Rows (red first)
- TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed (internal/daemon/prompt_warning_delivery_test.go). It uses the real wired daemon, observer and grammar. A "held" seam lets the observer answer, so the claim wins, then holds the reply before the daemon renders it. While the reply is held, the test writes the same request to client-04242.ndjson (the hook giving up and spooling), then releases it. The test checks:
  - the reply carries the warning and nothing is counted late (l0_prompt_reply_late stays 0);
  - drainRing captures the live copy, and Drain then absorbs the spooled copy (0 dispatched, spool file released);
  - observer.thrash_undelivered becomes 1;
  - the next prompt does not replay the stale warning;
  - one more loop cycle warns again at warnLoopCycles+1, and after that the rule is reported only once.
  Red on base 8fe6c1f4: I stashed the product files and spelled the one 5-argument call the 4-argument way base expects (temporary, not committed). It fails at "expected: 1 actual: 0" on the counter (runs/daemon-rearm-row-red-on-base.txt). A temporary diagnostic with the counter assertion removed fails on the behaviour: "a warning the host never received must not count as delivered once the loop continues" (runs/daemon-rearm-row-behaviour-red-on-base.txt).
- TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried (internal/observer/prompt_reply_claim_test.go) is the observer unit row: nonce matching, other nonce or session is a no-op, no state created for an unknown session, floor at the current multiplicity, the other rule's queued warning survives, one-shot, and a fresh warning only once the loop recurs. It cannot be red on base because the API does not exist there. Instead a temporary diagnostic with promptReplyOutput's ReplyWarning record removed fails "Should be true" (runs/observer-spooled-row-red-diagnostic.txt).

## Row determinism
The earlier draft of the daemon row stamped TS=now and waited on the wall clock until ts+250 ms. Under load it failed once in -count=10 at "the claim won, so the daemon's reply carries the warning": the claim lost because ingest.Accept's fsync ran past the stamp's budget. I rewrote the row in the way this file's other non-lateness rows already work: no hook stamp, so the claim gets the whole deadline from the route, and the hook's give-up is the test's own step (it writes the spool while the reply is held), not a clock. After that, -count=10 passes (runs/daemon-rearm-row-count10.txt). No assertion was loosened.

## Commands and results (Windows, -p 2, machine shared with other seats)
- `go test -p 2 -count=1 -v -run '^TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed$' ./internal/daemon/` on base product: FAIL, exit 1, as expected.
- The 7 daemon prompt rows plus the 4 observer prompt-reply rows, each by exact name: all PASS (runs/prompt-rows-green.txt).
- `go test -p 2 -count=10 -run '^TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed$' ./internal/daemon/`: ok, 4.5 s.
- `go test -p 2 -count=1 -timeout=30m ./internal/observer/`: ok, 81.2 s.
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/`: ok, 350.1 s.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet` on ./internal/daemon/ and ./internal/observer/, on Windows and with GOOS=linux: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every sub-check passes except runpatterns. Its 5 findings are all in reports already committed on base (w16d-warnlate/report.md lines 164, 169, 192 and 198, and w16e-settlewin/report.md line 92, which quote placeholder -run arguments). Nothing in this wave touches them; this is the coordinator's to resolve.

No Linux container, no Docker, no -race, no load generators, and no real Claude Code session or real ~/.qompack. No background process of mine is left running.

## Criterion changes
None. No assertion, threshold, budget or golden was changed. The earlier draft's wall-clock wait was removed from a new, uncommitted row only, as explained under "Row determinism".

### Commits

- 6f952adc fix(observer): re-arm a warning whose claimed reply was spooled
- ac0016a4 docs(daemon): stop saying a replayed prompt runs only the scan
- 92a44e8b ci(test): name X10's recovery branch in the non-reference note
- 19688bfb fix(daemon): re-arm a warning when its prompt's spooled copy settles
- 972879be docs(v6): record the w16f-rearm evidence logs

### Tests

- `go test -p 2 -count=1 -v -run '^TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed$' ./internal/daemon/ (base 8fe6c1f4 product stashed, row only)` — FAIL as expected: thrash_undelivered expected 1 actual 0 (runs/daemon-rearm-row-red-on-base.txt); temporary diagnostic without the counter assertion fails on the warn-again assertion (runs/daemon-rearm-row-behaviour-red-on-base.txt)
- `go test -p 2 -count=1 -v -run '^TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried$' ./internal/observer/ (temporary diagnostic: ReplyWarning record removed)` — FAIL as expected (runs/observer-spooled-row-red-diagnostic.txt)
- `go test -p 2 -count=1 -v -run '^(TestPromptWarning_DeliveredReplyCountsOnce|TestPromptWarning_LateReplyDoesNotConsumeTheRule|TestPromptWarning_SlowDurableAcceptIsLateForTheClient|TestPromptWarning_ReplyThatMayNotActRefusesTheClaim|TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed|TestPromptReplyHandoff_FirstSideWins|TestPromptReplyBudget_IsMeasuredFromTheHooksStamp)$' ./internal/daemon/` — PASS (7/7)
- `go test -p 2 -count=1 -v -run '^(TestPromptReplyClaim_RefusedClaimReArmsForTheLoopsNextOccurrence|TestPromptReplyClaim_AskedOnlyWhenThereIsAWarningToHandOver|TestPromptReplyClaim_PassiveModeKeepsTheQueue|TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried)$' ./internal/observer/` — PASS (4/4)
- `go test -p 2 -count=10 -run '^TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed$' ./internal/daemon/` — ok 4.520s
- `go test -p 2 -count=1 -timeout=30m ./internal/observer/` — ok 81.204s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 350.050s
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/daemon/ ./internal/observer/ (Windows and GOOS=linux)` — exit 0 both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS except runpatterns: 5 findings, all in reports already committed on base (w16d-warnlate/report.md:164,169,192,198; w16e-settlewin/report.md:92), none from this wave

### Open issues

- runpatterns lint fails on base: 5 placeholder -run arguments in reports already committed on base, plans/sdd/V6-closeout/w16d-warnlate/report.md (lines 164, 169, 192, 198) and w16e-settlewin/report.md (line 92). Untouched by this wave; the coordinator needs to waive or fix them before the full lint is green.
- Known limit, by design: ReplyWarning keeps only the latest warning-bearing nonce per session. If a later reply carries a warning for another rule before the drain settles the earlier spooled copy, the earlier rule stays counted as warned. A wider memory would need a new owner-set bound.
- Known limit: the memory does not survive a daemon restart, so a spooled copy drained after a restart re-arms nothing (consistent with WarnedRules not being persisted).
- Known limit: only a hook stamp that fails validHotPathTS measures the daemon's budget from route entry. In that case a claim could fall after the hook's give-up and after the drain settled the copy, and the warning would then not be re-armed. Real hooks stamp from the same machine clock.
- Stale comment, left alone to keep this wave contained: handleObservePrompt's doc (handlers.go, around line 529) still says the ObservePrompt seam does both recording and acting in one call; since SP08-D3 the call is reply-only.
- Not tested end to end: no Linux or -race run of the new rows (this seat was barred from the container and from -race), and no whole test/e2e run (X10 not exercised here).

## Independent review

### review:rearm: needs-fixes

- **minor** `internal/daemon/handlers.go:535 and :572` — Two comments in the same file still describe the pre-SP08-D3 seam, and they now contradict the doc block this wave corrected 50 lines below. The handleObservePrompt doc says "the ObservePrompt seam does both jobs in one call: G2.3's verbatim prompt capture is recording", and the inline comment at the call says "The call is recording, so it runs under the MayRecord check above". The corrected callObservePromptWithDeadline doc now says the call is reply-only and records nothing (observer.WithPromptReplyOnly). The implementer fixed two constant comments outside item (2) but left these two, which are the closest ones and make the opposite claim (it listed them in open_issues).
  - Evidence: handlers.go:533-537 "the ObservePrompt seam does both jobs in one call: G2.3's verbatim prompt capture is recording, and the hookio.Output it returns is acting"; handlers.go:572 "The call is recording, so it runs under the MayRecord check above". Compare handlers.go callObservePromptWithDeadline doc: "This call is reply-only (observer.WithPromptReplyOnly): it drains the pending warning under the session lock and records nothing." The observer confirms it: prompt.go onUserPrompt returns promptReplyOutput before any PutBytes when promptReplyOnly(ctx).
  - Fix: Make it comment-only. Reword handleObservePrompt's mode-gate paragraph: since SP08-D3 the verbatim capture is runIngested's, through the WAL line MayRecord gates, and the seam call is reply-only (it drains, or under a refused claim re-arms, the queued warning). Then MayRecord gates the WAL append and the call, and MayAct gates the reply. Change the inline comment at :572 to match.
- **minor** `test/e2e/v5_x10_test.go:516-545 (not in this diff); internal/observer/prompt_delivery.go:343` — Verification gap that the coordinator needs to close before freezing rc6. X10's "deferred to the hook's client spool" recovery branch (spooled && !wasLate) is exactly mode (b). Before this wave it could never recover, because the claim won and the rule stayed warned. It now depends on the re-arm this wave adds, and that re-arm happens only when a Drain settles the spooled copy. X10 waits for thrash_undelivered only on the late branch (require.Eventually under `if wasLate`). On the spooled branch it replays the recovery cycle straight away. rearmRules sets the floor to max(queued uses, uses at settle time). So if the spooled line is still deferred behind its unacknowledged live copy when waitPrimary returns, the recovery cycle can fold before the settle and be absorbed into the floor, and that attempt is wasted. The new rows cover the daemon and observer seams, but nobody has run X10 against this change: the seat was barred from test/e2e, from -race and from Linux.
  - Evidence: v5_x10_test.go:531-535: `if wasLate { require.Eventually(... x10v5CounterUndelivered ... > rearmed ...) }`. There is no equivalent for `spooled`. promptSpooled→waitPrimary drives Drains only until the live copy is indexed. prompt_delivery.go:343 `floor := max(rule.Uses, now[rule.ID])` is evaluated at PromptReplySpooled time. ci.yml now advertises this branch as licensed, but it has never been exercised.
  - Fix: Coordinator: run TestV5_ThrashWarningVisibleInStatusAndCheckpoint, and the w16f rows under -race on Linux, on the integration branch before the rc6 freeze. If the spooled branch is flaky, make X10 wait for thrash_undelivered on `spooled` the same way it does on `wasLate` before cycleAfter. That edit is in test/e2e, so it belongs to the seat that owns X10, not here.
- **nit** `internal/daemon/drain.go:930-943` — settledSpooledPrompt is called only on processOne's done paths. A client-spooled observe.prompt consumed through the admission-Denied branch (retired by a policy denial decided at drain time) is never reported. If that prompt's live reply carried a claimed warning, the rule stays warned. This needs the copy to be decided differently at drain than the live copy was (a legacy record with no baked decision plus a policy change), so it is a narrow edge. The DrainConfig doc limits itself to lines consumed "through its delivery stages", so the code matches its doc but not the task's "when the drain settles a client-spooled observe.prompt".
  - Evidence: drain.go:930-936: `case verdict.Denied: ... consume(lineStart, nextOffset); if retiredHere { dr.released(retired) }`. There is no settledSpooledPrompt(req). By contrast drain.go:1003 and :848 call it after consume.
  - Fix: Call settledSpooledPrompt(req) after consume in the verdict.Denied branch (and in verdict.Failed if wanted), or state in the DrainConfig.SpooledPromptSettled doc that admission-refused lines are deliberately not reported.
- **nit** `internal/observer/prompt_delivery.go:300-302 (PromptReplySpooled → rearmRules)` — Semantics note for the owner. A refused claim re-arms at about the moment the host missed the warning. A spooled reply is re-armed at drain-settle time, which can be up to idleTickMax (30 s) later unless something kicks the drain. The floor uses the multiplicity at settle time, so loop occurrences between the hook's give-up and the settle are treated as already seen, even though the host never received a warning. This follows the task text literally ("the same way a refused claim does ... hold the multiplicity floor"), and the observer row asserts it (ThrashFloor == 6, not 4). It is a design choice that should be on record, not a defect.
  - Evidence: prompt_reply_claim_test.go: `require.Equal(t, 6, st.ThrashFloor[1], "held at the multiplicity the rule has now")`. The warning was queued at 4. ipc/client.go:240 comment: "the next drain is an idle tick up to idleTickMax (30s) away".
  - Fix: None required for rc6. Optionally record in the report that the floor is taken at settle time, or capture the floor at claim time (store rule.Uses / Sequitur uses in replyWarning when the claim succeeds) if the owner prefers that loop growth after a lost warning still counts.
- **nit** `git log 8fe6c1f4..972879be (ac0016a4, 92a44e8b, 972879be)` — Three of the five commits have no Refs footer. The two fix commits carry `Refs: V6-VERIFY, C3.2`, but the docs(daemon) and ci(test) commits, which are part of the same C3.2 task (items 2 and 3), do not, and neither does the evidence commit. There are no attribution trailers, and the subjects are conventional.
  - Evidence: `git log --format='%h %s | %(trailers:key=Refs,valueonly)'` shows empty Refs for ac0016a4, 92a44e8b and 972879be. Base evidence commits are mixed (some also lack Refs).
  - Fix: Add `Refs: V6-VERIFY, C3.2` to ac0016a4 and 92a44e8b (and optionally 972879be) when the coordinator integrates, or accept it as base practice.
- **nit** `internal/daemon/daemon.go:1008` — The wiring line lands in daemon.go's drainConfig(), right next to the PreCompact-settle ClientSpoolRemoving entry. The settlekick seat owns daemon.go's served-request kick and the PreCompact route. The edit is two lines and necessary, since drainConfig is the only constructor, but it is a cross-seat file touch that may conflict textually at integration.
  - Evidence: diff: `+		// A hook's spooled copy of a prompt answered live re-arms the warning its reply carried.\n+		SpooledPromptSettled: d.settleSpooledPrompt,` placed after `ClientSpoolRemoving: d.spoolHeads.removing,`.
  - Fix: No code change. Coordinator: merge w16f-rearm and w16f-settlekick in sequence and check drainConfig() for conflicts.

## Fix seat (review resolution) — status `done`, head `6aac0f1e`

### Root cause

Finding 1: the w16f change corrected callObservePromptWithDeadline's doc to say the call is reply-only (SP08-D3 moved the verbatim capture to runIngested), but two comments 50 lines above still said the call was recording. Finding 2: the deferred X10 branch now depends on a re-arm that happens only when a Drain settles the spooled copy, and the copy is held until its live WAL copy is acknowledged. X10 waits for the re-arm only on the late branch.

### Summary

I was the fix seat for wave 16f rearm, on branch closeout/w16f-rearm in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16f-rearm (base 8fe6c1f4, now at 6aac0f1e). I fixed review finding 1, which only touched comments. I ran X10 once on Windows: it passed, but it did not take the spooled branch. Finding 2 is therefore still open for the coordinator, as the reviewer intended.

## Review resolution
- **Finding 1 (minor; handlers.go:535 and :572 still called the ObservePrompt call "recording"): CONFIRMED and FIXED in 8aa06f64, comments only.**
  - Evidence: in internal/observer/prompt.go, onUserPrompt returns promptReplyOutput when promptReplyOnly(ctx) is true (line 161). That return comes before store.PutBytes (line 190), so the reply-only call records nothing.
  - The handleObservePrompt mode-gate paragraph now says:
    - Since SP08-D3, G2.3's verbatim capture is done by runIngested, from the WAL line this route appends. That append is the recording.
    - The ObservePrompt call is reply-only. It drains the queued warning into the reply or, if the claim is refused, re-arms it.
    - MayRecord gates both the append and the call. MayAct gates only the reply. A reply under !MayAct has its claim refused (handoff.abandon() when !mayAct), so its warning is re-armed, not consumed.
  - The inline comment at the call now says the same. No code changed.
- **Finding 2 (minor; X10's spooled-but-not-late recovery branch was never run, and the re-arm could happen after the recovery cycle folds): ACCEPTED as a verification gap. It needs a coordinator run and an X10-owner edit; this seat is not the right one to fix it.**
  - I checked the mechanism and it is plausible:
    - promptSpooled→waitPrimary returns once the live copy's primary record is in the index.
    - The spooled copy is held back until the live WAL copy is acknowledged (drain.go ordering defer). settledSpooledPrompt only runs when that copy is consumed.
    - rearmRules sets the floor to max(rule.Uses, now[rule.ID]) at the moment of settling. So if the settle comes after the next Read→Edit→Bash cycle has folded, that cycle's extra uses are absorbed into the floor.
  - In practice the window is narrow:
    - Each f.tool in cycleAfter runs a hook subprocess and then an unconditional Drain (waitPrimary).
    - The spacer tool's Drain normally settles the spooled copy before the cycle folds.
    - For the race to happen, the live copy would have to stay unacknowledged through about four hook round trips and Drains.
  - The row-side fix is in test/e2e/v5_x10_test.go, outside this seat's scope: x10v5DeliverOrRecover should require.Eventually that thrash_undelivered goes above `rearmed` on `spooled` as it already does on `wasLate`, before calling cycleAfter.
  - My Windows run of X10 (log runs/fix-x10-windows.txt) passed in 95.3 s. It took the forced late-reply branch, which recovered with re-armed: true. The spooled branch was not taken (spooled: false), so that branch is still unexercised.

## What changed in this fix round
- internal/daemon/handlers.go: two comments rewritten (handleObservePrompt doc paragraph and the inline comment at the d.svc.ObservePrompt call).
- New evidence logs under plans/sdd/V6-closeout/w16f-rearm/runs/: fix-daemon-focused.txt, fix-daemon-package.txt, fix-x10-windows.txt.

## Commands and results (Windows, machine loaded by other seats)
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/daemon/` and `GOOS=linux go vet ./internal/daemon/`: both exit 0.
- `go build ./...`: exit 0.
- `go test -p 2 -count=1 -run '^(TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed|TestPromptWarning_ReplyThatMayNotActRefusesTheClaim|TestPromptWarning_LateReplyDoesNotConsumeTheRule)$' -v ./internal/daemon/`: all three PASS.
- `go test -p 2 -count=1 ./internal/daemon/`: ok in 361.951 s.
- `go test -p 2 -count=1 -timeout=30m -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e/`: PASS in 95.34 s. This is one named test, not the whole package, and was run without -race.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: every sub-check passes except runpatterns. All five of its failures are in reports already committed on the base, not in this branch's files (details under open issues).

## Criterion changes
None.

## Owner decisions
None. The implementer listed none, and this fix round adds no budget or bound numbers.

### Commits

- 6f952adc fix(observer): re-arm a warning whose claimed reply was spooled (implementer)
- ac0016a4 docs(daemon): stop saying a replayed prompt runs only the scan (implementer)
- 92a44e8b ci(test): name X10's recovery branch in the non-reference note (implementer)
- 19688bfb fix(daemon): re-arm a warning when its prompt's spooled copy settles (implementer)
- 972879be docs(v6): record the w16f-rearm evidence logs (implementer)
- 8aa06f64 docs(daemon): say the prompt route's seam call is reply-only (fix seat, review finding 1)
- 6aac0f1e docs(v6): record X10's Windows run on the w16f-rearm fix (fix seat, evidence)

### Tests

- `go test -p 2 -count=1 -run '^(TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed|TestPromptWarning_ReplyThatMayNotActRefusesTheClaim|TestPromptWarning_LateReplyDoesNotConsumeTheRule)$' -v ./internal/daemon/` — PASS (3/3), ok 1.612s
- `go test -p 2 -count=1 ./internal/daemon/` — ok 361.951s
- `go test -p 2 -count=1 -timeout=30m -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e/` — PASS 95.34s; forced late branch recovered (re-armed: true); spooled branch not taken (spooled: false)
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon/; GOOS=linux go vet ./internal/daemon/` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 1: all PASS except runpatterns; its 5 failures are all in the base's committed w16d-warnlate (4) and w16e-settlewin (1) reports

### Open issues

- Review finding 2 (coordinator action before freezing rc6): run TestV5_ThrashWarningVisibleInStatusAndCheckpoint and the w16f rows (TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed, TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried) under -race on Linux on the integration branch. X10's spooled-not-late recovery branch has never run. The robust fix is for the seat that owns X10, in test/e2e/v5_x10_test.go x10v5DeliverOrRecover: require.Eventually that thrash_undelivered goes above `rearmed` when `spooled` is true, as it already does for `wasLate`, before cycleAfter.
- Pre-existing runpatterns lint failure on the base: placeholder -run patterns in plans/sdd/V6-closeout/w16d-warnlate/report.md lines 164, 169, 192 and 198, and plans/sdd/V6-closeout/w16e-settlewin/report.md line 92. They are coordinator-committed reports, so I did not edit them.
- Residual stale comment, out of this finding's scope: in internal/daemon/handlers.go, the counterPromptCaptureRefused comment (lines 86-88) still calls a refused start 'a lost G2.3 capture'. Since SP08-D3 the capture belongs to runIngested from the already-appended WAL line, so a refused start loses the reply, not the capture. Two log strings in startPromptRecording (lines ~802 and ~822) use the same wording; they are left unchanged because log strings may be asserted.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



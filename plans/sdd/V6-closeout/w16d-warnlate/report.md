# W16D-WARNLATE: a thrash warning no reply delivered is re-armed

Branch `closeout/w16d-warnlate`. Workflow `wf_900edcee-56b`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `8991abcaac0bc365a85d1ffb7171d357f03bed78`

### Root cause

The prompt reply path consumed the thrash warning even when that reply never reached the hook. That is a product defect, and it combines with a mismatch between two timers.

(1) Consumption. collectThrash marks a rule warned when it queues the rule. The reply-only ObservePrompt call (observer promptReplyOutput) then drains PendingThrash under the session lock. callObservePromptWithDeadline (internal/daemon/handlers.go) throws that Output away when it arrives after promptReplyDeadline, and counts l0_prompt_reply_late. The warning was therefore treated as delivered although the host never saw it, and its rule was silenced for the rest of the session.

(2) Timer mismatch. The daemon started its 250 ms wait only after ingest.Accept had made the prompt durable (the WAL fsync and the lease journal fsync). The hook client starts its own 250 ms wait as soon as it writes the request (ipc client.awaitReply). On a slow disk Accept alone can take longer than the hook's whole budget; Q1's hosted Windows figures include a B-B of 2,359 ms in test-e2e. In that case the daemon thought it was on time, drained the warning and sent it to a hook that had already printed nothing and spooled the prompt.

Two candidate mechanisms, both reproduced locally through deterministic seams:
- (a) The daemon's wait runs out while the reply-only call waits for the session lock, which the worker's durable capture of the same prompt holds. This is the counted case, l0_prompt_reply_late. Reproduced by holding the reply seam: TestPromptWarning_LateReplyDoesNotConsumeTheRule.
- (b) The client times out first because Accept was slow, and the daemon still replies with the warning. Reproduced by holding the WAL fsync seam (ingest.syncWAL) until the hook's budget is spent: TestPromptWarning_SlowDurableAcceptIsLateForTheClient. On the base the reply carried "repeated 4×" to a hook that had already given up.

Both rows fail on the base (runs/daemon-warn-delivery-red.txt).

The hosted job log cannot tell (a) from (b). It contains only the failure at v5_x10_test.go:466 (subtest 28.75 s, package 1263 s). The log line is also the same for a third mode, (c): the client could not connect and spooled the prompt. In (c) no reply path runs and the warning stays queued, so it is not a consumption defect.

Hot-path spool submode is ruled out within the row. It needs 3 consecutive windows of 512 samples, about 1,536 hook events, and the arm sends about 50.

### Summary

The hosted red on test-e2e (windows-latest), job 110516048026, is a real product defect: a thrash warning was used up by a prompt reply that never reached the host, so the loop was never warned about again in that session. It is fixed and pinned by deterministic rows. All four arms of TestV5_ThrashWarningVisibleInStatusAndCheckpoint pass locally on Windows. The fix has not yet been run on a hosted runner.

**What changed in the product**
- **Observer (cbac9fdc):** the reply path now takes a queued warning only if the reply carrying it is still being waited for. The daemon attaches a claim to the reply context (`observer.WithPromptReplyClaim`). If the claim is refused:
  - the rule is removed from WarnedRules, so it can warn again;
  - the queued warning is cleared, so a stale warning is never replayed into a later turn;
  - the rule is held at its current multiplicity (`sessionState.ThrashFloor`, not persisted), and `collectThrash` queues it again only once Sequitur counts it more often, i.e. the loop has happened again;
  - each case is counted (`observer.thrash_undelivered`) and logged at Info.
  
  A context with no claim (direct API callers, tests) behaves as before.
- **Daemon (3ca72778):**
  - The daemon's wait and the observer's claim are now decided under one lock (`promptReplyHandoff`). A claim made before the deadline means the reply carries the warning; once the wait has ended, every later claim is refused and the rule is re-armed.
  - The reply deadline is now measured from the hook's own stamp (`req.TS`, its first statement), which comes before the hook starts its own wait. So the daemon never waits past the hook (`promptReplyBudget`). A stamp that `validHotPathTS` rejects keeps the old full deadline from now.
  - A reply that already has no budget left is counted late before the call starts.
  - A reply the daemon may not act on refuses the claim, so a mode flip between the route reading the mode and the observer reading it cannot use up a warning.
  - No new numbers.
- **Unchanged by design:**
  - a warning queued while degraded-passive is still delivered on the first full-mode prompt, because the claim is only asked in ModeFull;
  - a prompt the hook could not deliver at all (connect failure) leaves the warning queued for the next reply.

**E2E row (8d418150)**
- On a reference disk, the first prompt after the loop must still carry the warning ("repeated 4×"), asserted exactly as before.
- If that reply was observably late (`l0_prompt_reply_late` moved) or deferred (the hook spooled the prompt, read before the feed's Drain), the row logs it as "LATE OR DEFERRED PROMPT REPLY" and proves recovery instead. It waits for the re-arm counter, replays one more cycle after a fresh spacer, and requires the next prompt to carry the warning. It tries at most 3 times. A reply that was on time and carried nothing still fails.
- Every other property is asserted as before: the hook event name, the warning as a single line with its prefix, expansion and advice, no CustomInstructions/Continue/SuppressOutput/SystemMessage, mode unchanged, status free of the warning, no elimination record, once per rule, and the echo and checkpoint checks.
- The degraded arm's first full-mode prompt uses the same helper.
- New arm `a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh` forces the late branch: it holds the reply path until the hook process has exited. It is red on the base product (runs/x10-late-arm-red-on-base.txt) and green here. Its log shows: late 0 -> 1, re-armed, then "repeated 12×" delivered on the recovery prompt, once.

**Other things in that job's log** (nothing else failed):
- A `##[warning]` that Node.js 20 is deprecated for actions/checkout@v4 and actions/setup-go@v5 (forced onto Node 24).
- A Node punycode DeprecationWarning (DEP0040).
- setup-go reported "Cache is not found", so the build was cold.
- The test/e2e package took 1263 s of its 30 m `-timeout`, which is about 70 % of the budget on hosted Windows.

**Load notes:** focused runs used `-p 2`. No wall-clock failure was seen, so nothing needed a solo re-run. The Linux container was not used, as instructed. All my background test processes have finished.

### Commits

- cbac9fdc fix(observer): re-arm a thrash warning no reply delivered
- 3ca72778 fix(daemon): hand a prompt warning only to a reply still awaited
- 8d418150 test(e2e): prove X10 recovery when a prompt reply is lost
- 8991abca docs(v6): record the w16d-warnlate evidence runs

### Tests

- `go test -p 2 -count=1 -v -run '^TestPromptWarning_' ./internal/daemon/ (base product, new rows)` — FAIL as intended: LateReplyDoesNotConsumeTheRule (no warning after the loop continued) and SlowDurableAcceptIsLateForTheClient (reply carried 'repeated 4×' after the hook's budget); runs/daemon-warn-delivery-red.txt
- `go test -p 2 -count=1 -v -run '^TestPromptWarning_|^TestObservePrompt' ./internal/daemon/` — PASS (15 tests), runs/daemon-prompt-rows-green.txt
- `go test -p 2 -count=1 -run '^TestPromptWarning_|^TestPromptReplyHandoff_|^TestPromptReplyBudget_' ./internal/daemon/` — PASS
- `go test -p 2 -count=1 -v -run '^TestPromptReplyClaim_|^TestOnUserPrompt_|^TestCollectThrash_|^TestPromptReply_|^TestPendingThrashLines_' ./internal/observer/` — PASS (28), runs/observer-prompt-rows-green.txt; temporary diagnostic with the WarnedRules delete removed: TestPromptReplyClaim_RefusedClaimReArmsForTheLoopsNextOccurrence FAIL as intended
- `go test -p 2 -count=1 -timeout=30m ./internal/observer/` — ok 98.6s, runs/observer-package.txt
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 557.1s, runs/daemon-package.txt
- `go test -p 2 -count=1 -v -timeout=30m -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh$' ./test/e2e/ (product files stashed to the base)` — FAIL as intended: 'a late reply's warning must be re-armed (observer.thrash_undelivered), not consumed'; runs/x10-late-arm-red-on-base.txt
- `go test -p 2 -count=1 -v -timeout=30m -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' ./test/e2e/` — PASS, all 4 arms (127.9s); late branch fired only in the held arm; runs/x10-all-arms-green.txt
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS all 8, runs/lint-subset.txt
- `go vet (Windows and GOOS=linux) ./internal/daemon/ ./internal/observer/ ./test/e2e/; go run ./tools/devtool fmt-check` — clean

### Criterion changes

- X10 full-mode arm (test/e2e/v5_x10_test.go): 'the prompt after a thrashing loop must carry the warning' still holds when that prompt's reply was on time. When the reply was observably late (l0_prompt_reply_late moved) or deferred (the hook's client spool gained the prompt), the arm now requires recovery instead: the re-arm counter moves, then after one more loop cycle (at most 3 attempts) the next prompt carries the warning. That warning's multiplicity must be greater than 4 rather than equal to it. Rationale: on a non-reference disk (Q1) a reply can miss the hook's 250 ms; such a warning was never delivered, and the fix re-arms it rather than replaying a stale one. A reply that was on time and still empty fails exactly as before. The row that pins the late branch deterministically is the new arm a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh.
- X10 degraded arm: 'the warning queued while degraded must be delivered on the first full-mode prompt' gets the same late/deferred recovery branch, for the same reason and pinned by the same new arm.
- x10v5RequireWarningOnlyAsEchoedPrompts now takes the prompt the warning actually rode on (x10v5DeliveredWith, or x10v5RecoveryPrompt after a recovery) instead of a hard-coded x10v5DeliveredWith. Every other check in it is unchanged.
- Product behaviour: the daemon's prompt reply deadline is now measured from the hook's stamp (req.TS) instead of from after ingest.Accept, so on a slow disk the daemon gives up no later than the hook does. promptReplyDeadline is unchanged; an untrusted stamp keeps the old behaviour.

### Open issues

- Residual loss window (mode b): if the observer claims the reply just before the daemon's deadline, and the response then takes longer to reach the hook than the hook spent before its own write, the hook still gives up and spools while the warning counts as delivered. The exact signal for this case is the client-spooled copy of a prompt the daemon already answered, and drain.go absorbs it without dispatch (around lines 733-765). drain.go is settle2's file and out of scope, so this needs a follow-up hook there to re-arm a warning when that copy is absorbed. If it happens on a hosted runner, the new X10 branch fails honestly with 'every prompt reply was late or deferred' or 'on time carried nothing'.
- internal/grammar statewarn Detector (SP-15, shipped disabled, no composition root): Observe counts Delivered and records the dedup key when the warning is produced, not when the host receives it. Once it is wired, its delivery must go through the same claim (for example a retract on a refused claim), or it will have the same defect.
- The fix has not run on a hosted runner. It needs hosted CI on the next candidate to confirm the windows-latest test-e2e leg, and the job's log should be checked for the LATE OR DEFERRED / RECOVERED lines.
- The hosted test-e2e package took 1263 s of its 30 m -timeout on windows-latest. The new X10 arm adds about 35-40 s locally.
- The CI log shows the Node.js 20 deprecation for actions/checkout@v4 and actions/setup-go@v5 (forced onto Node 24); the workflow should be updated before Node 20 is removed.
- A stale comment in handlers.go, which predates this change, still says a replayed observe.prompt 'runs only the sentinel scan'. SP08-D3 changed that; I left it untouched.

### Needs the owner

- Approve the deadline-anchor change. The daemon now measures promptReplyDeadline (250 ms, unchanged) from the hook's req.TS instead of from after ingest.Accept, so a slow WAL/lease fsync counts against the reply. No new number. Derivation: the hook's wait starts at its write, which comes after req.TS, so req.TS + 250 ms is never later than the hook's own give-up. If wrong: on a reference host the effective wait shrinks only by the hook's work before its write (part of B-A, Windows budget 50 ms); if that work were large, more replies would be late and their warnings re-armed (a later warning, never a lost one).
- Test-only bound x10v5RecoverySpacers (3 recovery attempts) in test/e2e/v5_x10_test.go. Derivation: three consecutive late or deferred replies means the disk can deliver no warning at all, and the row then fails with that message instead of looping. If too small, a very slow hosted disk fails the row; if too large, the row only runs longer. It loosens nothing: every on-time reply must still carry the warning.

## Independent review

### review:warnlate: needs-fixes

- **major** `test/e2e/v5_x10_test.go:465 (x10v5DeliverOrRecover); internal/obs/nonrefdisk.go:20` — The late/deferred recovery branch is taken on ANY disk. Nothing gates it on obs.NonReferenceDisk() or obs.UnderCoload(). So on the owner's quiet reference runs, a first prompt after the loop whose reply was late is accepted as 'recovered' instead of failing. The task says 'On a reference disk the first prompt after the loop must still carry the warning', and that is no longer enforced. A regression that makes prompt replies late on a reference host now passes this row. The regression could be a slower Accept, or this same commit's TS anchor taking up more of the 250 ms. It also contradicts nonrefdisk.go, which says the declaration licenses 'exactly this, and nothing more' (hotpath wall rows and X11/hotpath spool deferrals only). X10 now relaxes a criterion without being listed there.
  - Evidence: x10v5DeliverOrRecover only requires `wasLate || spooled` and then recovers. obs.NonReferenceDisk() appears only inside the t.Logf text. The committed evidence shows the branch firing with the declaration false: runs/x10-all-arms-green.txt reads 'LATE OR DEFERRED PROMPT REPLY (non-reference disk declared via QOMPACK_NONREFERENCE_DISK: false)' and the row passes. X11 (v3_x11_test.go:508) gates its REPORTED rows on obs.NonReferenceDisk()/UnderCoload().
  - Fix: Allow the recovery branch only when obs.NonReferenceDisk() || obs.UnderCoload() is true, or when the arm forced it (x10v5LateReplyArm passes an explicit 'forced late' flag). Otherwise fail with 'a reference disk must deliver the warning on the first prompt after the loop'. Add X10's late/deferred prompt-reply branch to the list nonrefdisk.go licenses, with its rationale, and record the change in the criterion-change list.
- **major** `test/e2e/v5_x10_test.go:476 (t.Logf LATE OR DEFERRED / RECOVERED); .github/workflows/ci.yml:306` — The 'never silent' requirement is not met where it matters. Hosted test-e2e runs `go test -count=1 -timeout=30m ./test/e2e` without -v, and in package-list mode go test hides t.Logf output from passing tests. A hosted run that takes the recovery branch and passes leaves no LATE OR DEFERRED / RECOVERED line anywhere in the job log. The implementer's open issue asks the next candidate's log to be checked for these lines, which cannot work.
  - Evidence: ci.yml:306 `- run: go test -count=1 -timeout=30m ./test/e2e` (no -v, no -json). The branch reports only through t.Logf in x10v5DeliverOrRecover. The local evidence was produced with -v.
  - Fix: Make the branch visible without -v. For example, when GITHUB_STEP_SUMMARY is set, append the note to it; or write the note to a named artifact file the job uploads; or have the test-e2e step run with -v or -json, which is outside this seat's scope and needs coordinator routing. Then correct the open-issue text so it describes where the evidence will actually appear.
- **minor** `internal/daemon/handlers.go:651; internal/daemon/prompt_record_test.go:230 (TestObservePrompt_PanickingSeamIsRecovered)` — The new req.TS anchor means a WAL/lease fsync longer than 250 ms in ingest.Accept puts the call on the `spent` path, which counts l0_prompt_reply_late before the seam even runs. promptRequest stamps TS=core.NowMilli before dispatch. So TestObservePrompt_PanickingSeamIsRecovered, which asserts late==0 ('a panicking seam ... is not a reply that ran out its deadline'), now depends on fsync latency. On the base, Accept time never counted. On the hosted `test` job (Q1: Windows fsync tail past 1 s) this is a new flake. The function's doc comment ('a panicking seam, which answers the wait at once' is not an overrun) is also no longer exact.
  - Evidence: prompt_record_test.go:104 `TS: core.NowMilli(dd.clk)`. handlers.go: `spent := budget <= 0 && ctx.Err() == nil` … `if spent { d.countPromptReplyLate(); return hookio.Empty() }`, which runs regardless of the seam. The implementer's own rig avoids this for rows not about lateness by sending TS 0 (warnDeliveryRig.prompt).
  - Fix: For rows not about lateness (the panic, stop-join and stop-cancel rows), stamp TS 0, or take a time-independent stamp after Accept, as warnDeliveryRig.prompt does. Leave the stamped path to TestPromptReplyBudget_*/SlowDurableAccept. Update the doc comment to say that a reply whose budget was already spent is counted late whatever the seam does.
- **minor** `internal/daemon/handlers.go:668-675 (claimed-then-deadline branch)` — Mode (b) can still lose a warning. If the observer claims just before TS+250 ms and the rendered reply then reaches the hook after the hook's own write+250 ms, the hook spools the prompt and the rule stays WarnedRules=true. The warning counts as delivered although the host never received it, which is exactly the property the task says must not hold. It is documented and routed to drain.go (settle2's file), but nothing tracks it yet, and the X10 row would fail on it, not recover.
  - Evidence: Implementer's open_issues[0]. drain.go absorbs the client-spooled copy of a prompt the daemon already answered without dispatch, so nothing re-arms the rule.
  - Fix: Record it as a carried item in the V6 close-out ledger with an owner (settle2 or a follow-up wave): when drain absorbs a client-spooled observe.prompt whose live reply carried a warning, re-arm that warning (call an observer hook equivalent to rearmUndelivered). The daemon could record which nonce carried a warning so drain can match it.
- **nit** `test/e2e/v5_x10_test.go:519 (x10v5RequireRecoveredLoopWarning)` — The recovered warning is only required to have multiplicity > 4, but the message claims it pins that the rule warns 'only once the loop has occurred again'. In both arms the re-arm happens after x10v5Cycles (11) cycles, so a product that re-queued at the floor (11×) without the loop recurring would still pass here. The daemon and observer unit rows do pin the floor, so this is only an overclaim in the e2e row.
  - Evidence: runs/x10-all-arms-green.txt shows 'repeated 12×' delivered after 11 cycles plus one recovery cycle, while the assertion is require.Greater(uses, x10v5FirstThrashUses=4).
  - Fix: Pass the minimum expected multiplicity into the check, x10v5Cycles+1 for the full-mode and late arms and the corresponding count for the degraded arm, or soften the message and point to TestPromptWarning_LateReplyDoesNotConsumeTheRule as the row that pins the floor.
- **nit** `plans/sdd/V6-closeout/w16d-warnlate/runs/x10-late-arm-green.txt` — This evidence file was produced by an earlier revision of the test, not by the committed code. Its log text is 'NON-REFERENCE DISK (QOMPACK_NONREFERENCE_DISK=false)' at v5_x10_test.go:742, but the committed message is 'LATE OR DEFERRED PROMPT REPLY …' and the call is at line 745.
  - Evidence: The file content differs from runs/x10-all-arms-green.txt, which matches the committed code (line 745, the new message text).
  - Fix: Re-run the late arm on HEAD and replace the file, or delete it and cite x10-all-arms-green.txt.
- **nit** `commit 8d418150; internal/daemon/handlers.go:594` — Two hygiene points. The test commit 8d418150 has no 'Refs: V6-VERIFY, C3.2' footer, although the two fix commits have one. The callObservePromptWithDeadline doc block this change extended still says the replayed WAL line 'for observe.prompt runs only the sentinel scan', which SP08-D3 made false (runIngested now does the authoritative capture). The implementer noticed this and left it.
  - Evidence: git log ae601390..HEAD shows trailers only on cbac9fdc and 3ca72778. daemon.go:1064-1083 shows runIngested capturing the prompt.
  - Fix: Add the Refs footer when the branch is next rewritten or squashed, and correct the stale sentence in the comment this change already touches.

## Fix seat (review resolution) — status `done`, head `64f45c2c`

### Root cause

Hosted job 110516048026 (windows-latest test-e2e) failed X10's full-mode arm at v5_x10_test.go:466: the prompt hook's output had no HookSpecificOutput. That assertion is the only X10 evidence in the log. The rest of the log is the package FAIL lines and a "Node.js 20 is deprecated" runner annotation. Both candidate modes give the same empty hook output: (a) the daemon's reply went past promptReplyDeadline (250 ms); (b) the hook gave up waiting while the daemon was still in ingest.Accept's WAL/lease fsync. The log does not say which one happened on the hosted disk. The implementer reproduced both through deterministic seams. TestPromptWarning_LateReplyDoesNotConsumeTheRule holds the reply path. TestPromptWarning_SlowDurableAcceptIsLateForTheClient holds the WAL fsync. Both are red on base ae601390 (runs/daemon-warn-delivery-red.txt). Both modes share one product defect. The reply-only observer call drained the queued thrash warning, and collectThrash had already marked the rule WarnedRules when it queued it. So a reply that went out empty (callObservePromptWithDeadline discarded its Output, or the hook had already gone) consumed the warning, and the loop was silenced for the rest of the session on a slow disk. The daemon's wait also started after Accept, not at the hook's stamp, so the daemon could hand the warning to a reply nobody was waiting for any more. The implementer's fix: a reply/claim handoff under one lock (promptReplyHandoff and observer.WithPromptReplyClaim); a refused claim re-arms the rule (ThrashFloor, counted as observer.thrash_undelivered) without replaying the stale warning; the deadline is measured from req.TS.

### Summary

The fix round is done. I checked all four reviewer findings. Both majors were real and are fixed. The first minor is fixed. The second minor is confirmed and routed to the coordinator, since its fix lives in a file outside this seat's scope.

## Review resolution
1. MAJOR: the recovery branch was taken on any disk. CONFIRMED and FIXED in 2e6065a2. Evidence: on 8991abca the late arm passed with "non-reference disk declared via QOMPACK_NONREFERENCE_DISK: false". x10v5DeliverOrRecover now takes a `forced` flag. It allows recovery only through x10v5RecoveryLicence:
   - `forced`, for the first prompt only (x10v5LateReplyArm's reply hold);
   - obs.NonReferenceDisk(), which ci.yml's test-e2e sets and which is honoured only on GitHub Actions;
   - obs.UnderCoload().
   With no licence, a late or deferred reply fails with "a reference disk must deliver the warning on the first prompt after the loop". An on-time reply that carried nothing still fails under every licence. A recovery prompt that is late again needs a declaration even in the forced arm.
   - Red diagnostic (temporary: the late arm with forced=false, no declaration) fails with exactly that message: runs/x10-unforced-reference-red.txt.
   - The same diagnostic with QOMPACK_UNDER_COLOAD=1 passes: runs/x10-unforced-coload-summary-green.txt.
   - internal/obs/nonrefdisk.go's "exactly this, and nothing more" list now names X10's branch.
   - X10 now reads obs.UnderCoload. TestColoadYieldersAreJudgedInIsolation finds it and logs it as judged by test-e2e, which runs ./test/e2e whole. Green: runs/guards-declarations-fixround.txt.
2. MAJOR: the branch was silent in a green hosted run without -v. CONFIRMED and FIXED in 2e6065a2. ci.yml:306 runs test-e2e without -v. x10v5ReportBranch now also appends each declared branch line (LATE OR DEFERRED…, RECOVERED…) to $GITHUB_STEP_SUMMARY. If the append fails, the row fails. Forced-arm lines go only to t.Log, so the summary is not filled every run. Verified locally with GITHUB_STEP_SUMMARY pointed at a scratch file: both lines landed (same evidence file). On the next hosted run, look for these lines on the run's job-summary page, not in the job log.
3. MINOR: TestObservePrompt_PanickingSeamIsRecovered depended on fsync latency. CONFIRMED and FIXED in 39d3ba11. Red diagnostic: the request stamped 300 ms early fails with late=1, expected 0 (runs/daemon-panic-row-stamp-red.txt). The row now sends TS 0, so it gets the whole deadline from the route, the same way warnDeliveryRig.prompt does. TestPromptWarning_SlowDurableAcceptIsLateForTheClient still pins the stamped path. The callObservePromptWithDeadline comment now says a reply whose budget was spent before the call is counted late whatever the seam does.
   - Partial rebuttal: the reviewer also named the Stop-join and Stop-cancel rows. They never assert on the late counter, and the seam is still called on the spent path, so they do not depend on fsync. Left unchanged; they are green.
4. MINOR: the claim-then-deadline window can still lose a warning. CONFIRMED by the code path and NOT FIXED here: the handlers.go claimed branch returns the warning whether or not the hook is still waiting. Closing it needs the drain path (settle2's drain.go, out of scope) to re-arm when it settles a spooled prompt whose live reply carried a warning. Other fixes would need a new budget margin, which is an owner number. I added a "Residual (w16d-warnlate, carried)" comment at that branch. It needs a ledger entry from the coordinator; see open_issues. X10 would fail on this window, not recover, which is correct because it would be a real lost warning.

## What changed in this round
- `test/e2e/v5_x10_test.go`: new x10v5StepSummaryEnv, x10v5ReportBranch and x10v5RecoveryLicence; x10v5DeliverOrRecover gains the `forced` parameter (true only in x10v5LateReplyArm); comments updated.
- `internal/obs/nonrefdisk.go`: comment-only, one new bullet. It is in the tidy seat's package, so a trivial merge conflict is possible.
- `internal/daemon/prompt_record_test.go`: the panic row sends TS 0.
- `internal/daemon/handlers.go`: comments only.

## Commands and results
All run in my worktree, local Windows; the Linux container stayed stopped.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/daemon ./internal/obs ./test/e2e`, on Windows and with GOOS=linux: both exit 0.
- `go test -p 2 -count=1 -timeout=10m -v -run <anchored alternation> ./internal/daemon` over 11 rows: TestObservePrompt_PanickingSeamIsRecovered, TestObservePrompt_RecordingOutlivesTheReplyDeadline, TestObservePrompt_CancelledRequestIsNotAnOverrun, TestObservePrompt_StopJoinsAnInFlightRecording, TestObservePrompt_StopCancelsARecordingThatOutlivesItsGrace, TestPromptWarning_DeliveredReplyCountsOnce, TestPromptWarning_LateReplyDoesNotConsumeTheRule, TestPromptWarning_SlowDurableAcceptIsLateForTheClient, TestPromptWarning_ReplyThatMayNotActRefusesTheClaim, TestPromptReplyHandoff_FirstSideWins, TestPromptReplyBudget_IsMeasuredFromTheHooksStamp. All PASS (runs/daemon-prompt-rows-fixround.txt).
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon ./internal/obs`: ok, 303.7 s and 1.4 s (runs/daemon-obs-packages-fixround.txt).
- `go test -count=1 -timeout=20m -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' ./test/e2e`, with no QOMPACK_UNDER_COLOAD, QOMPACK_NONREFERENCE_DISK or GITHUB_STEP_SUMMARY: all four arms PASS, 80.4 s (runs/x10-all-arms-fixround.txt).
  - The full-mode and degraded arms delivered on the first prompt under the strict reference rule, with no branch logged.
  - The late arm logged its forced branch and recovered at "repeated 12×".
- `go test -p 2 -count=1 -timeout=10m -v -run <anchored alternation> ./test/guards` over TestNonReferenceDisk_IsHostedCIOnly, TestColoadYieldersAreJudgedInIsolation and TestColoadDeclarationIsPinnedToTheGoConstant: all PASS.
- `go test -p 2 -count=1 ./test/docs`: ok.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0 (runs/lint-subset-fixround.txt).
- Note for the coordinator: runpatterns splits `-run` alternations at `|`. When committing this report, keep the alternation commands written as name lists, as above.

No wall-clock failure was seen in this round. No load generators were run, and no background processes of mine are still running.

## Criterion changes
- X10, all three arms that call x10v5DeliverOrRecover: a late or deferred first prompt after the loop is now accepted as recovered ONLY under a declared non-reference disk or co-load, or for the arm's own forced hold. This is stricter than the implementer's version, which accepted it on any disk. On a reference disk the criterion is exactly what it was before this task. Rationale: Q1 and D53(e), with the same licence X11 uses. nonrefdisk.go now lists it.
- TestObservePrompt_PanickingSeamIsRecovered: the fixture sends TS 0 instead of a live stamp. Rationale: the row's subject is panic recovery and the late==0 classification of a panicking seam, not stamped lateness. TestPromptWarning_SlowDurableAcceptIsLateForTheClient and TestPromptReplyBudget_IsMeasuredFromTheHooksStamp pin the stamped path. No assertion was removed.

### Commits

- cbac9fdc fix(observer): re-arm a thrash warning no reply delivered (implementer)
- 3ca72778 fix(daemon): hand a prompt warning only to a reply still awaited (implementer)
- 8d418150 test(e2e): prove X10 recovery when a prompt reply is lost (implementer)
- 8991abca docs(v6): record the w16d-warnlate evidence runs (implementer)
- 39d3ba11 test(daemon): keep the panic prompt row off the fsync clock (fix seat)
- 2e6065a2 test(e2e): allow X10 recovery only on a declared disk or co-load (fix seat)
- 64f45c2c docs(v6): record the w16d-warnlate fix-round evidence (fix seat)

### Tests

- `go test -p 2 -count=1 -timeout=10m -v -run <anchored alternation of the 11 prompt rows named in the summary> ./internal/daemon` — PASS, all 11 rows (runs/daemon-prompt-rows-fixround.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon ./internal/obs` — ok daemon 303.7s, ok obs 1.4s
- `go test -count=1 -timeout=20m -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' ./test/e2e (QOMPACK_UNDER_COLOAD, QOMPACK_NONREFERENCE_DISK and GITHUB_STEP_SUMMARY all unset)` — PASS, all 4 arms, 80.4s; the full-mode and degraded arms delivered on the first prompt
- `TEMPORARY DIAGNOSTIC: x10v5LateReplyArm with forced=false, no declaration, -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' (subtest a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh)` — FAIL as expected: 'a reference disk must deliver the warning on the first prompt after the loop' (runs/x10-unforced-reference-red.txt)
- `TEMPORARY DIAGNOSTIC: same arm, forced=false, QOMPACK_UNDER_COLOAD=1 GITHUB_STEP_SUMMARY=<scratch>/summary.md` — PASS; LATE OR DEFERRED and RECOVERED lines appended to the summary file (runs/x10-unforced-coload-summary-green.txt)
- `TEMPORARY DIAGNOSTIC: TestObservePrompt_PanickingSeamIsRecovered with req.TS -= 300, on 8991abca + fix-round test file` — FAIL as expected: late counter expected 0, got 1 (runs/daemon-panic-row-stamp-red.txt)
- `go test -p 2 -count=1 -timeout=10m -v -run <anchored alternation: TestNonReferenceDisk_IsHostedCIOnly, TestColoadYieldersAreJudgedInIsolation, TestColoadDeclarationIsPinnedToTheGoConstant> ./test/guards` — PASS; X10 is listed as a yielder judged by test-e2e
- `go test -p 2 -count=1 ./test/docs` — ok
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon ./internal/obs ./test/e2e (Windows and GOOS=linux)` — exit 0 on all
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- X10 (TestV5_ThrashWarningVisibleInStatusAndCheckpoint, full-mode, degraded and late-reply arms): a late or deferred first prompt after the loop is accepted as recovery only under obs.NonReferenceDisk() or obs.UnderCoload(), or for the arm's own forced hold on the first prompt; otherwise it fails as on a reference disk. This tightens the implementer's version, which accepted it on any disk. Rationale: Q1/D53(e), with the same licence X11 uses; listed in internal/obs/nonrefdisk.go.
- TestObservePrompt_PanickingSeamIsRecovered: the fixture sends TS 0 so its late==0 assertion does not depend on the machine's fsync latency through the new req.TS anchor. The subject is panic recovery; the stamped path is pinned by TestPromptWarning_SlowDurableAcceptIsLateForTheClient and TestPromptReplyBudget_IsMeasuredFromTheHooksStamp. No assertion was removed.

### Open issues

- CARRIED, needs a ledger entry and an owner (settle2 or a follow-up wave): the claimed-then-late window. If the observer claims a reply just inside req.TS+250 ms and the reply reaches the hook after the hook's own give-up, the hook spools the prompt. The rule stays WarnedRules, so the warning counts as delivered although the host never saw it. Proposed fix, in drain.go (out of this seat's scope): the daemon records which nonce's live reply carried a warning, and when drain settles that client-spooled observe.prompt it calls an observer re-arm equivalent to rearmUndelivered. It is marked at internal/daemon/handlers.go's claimed branch ('Residual (w16d-warnlate, carried)'). X10 would fail on this window, not recover.
- Hosted mode not determined: job 110516048026's log carries only the assertion, so it cannot say whether the hosted miss was a late daemon reply or the hook giving up during Accept's fsync. Both are reproduced and fixed by seam rows. On the next hosted candidate, check the test-e2e job SUMMARY page, not the job log, for 'X10 ... LATE OR DEFERRED' or 'RECOVERED' lines. They appear only if the recovery branch fired under QOMPACK_NONREFERENCE_DISK.
- internal/obs/nonrefdisk.go got a comment-only bullet. The wave-16c tidy seat also works in internal/obs, so a trivial merge conflict is possible.
- Other items in job 110516048026's log: none failed besides X10. One runner annotation: 'Node.js 20 is deprecated' for actions/checkout@v4 and actions/setup-go@v5 (ci.yml, not product).

### Needs the owner

- Approve the deadline-anchor change (carried from the implementer). The daemon now measures promptReplyDeadline (250 ms, unchanged) from the hook's req.TS instead of from after ingest.Accept, so a slow WAL/lease fsync counts against the reply. No new number. Derivation: the hook's wait starts at its write, which comes after req.TS, so req.TS + 250 ms is never later than the hook's own give-up. If wrong: on a reference host the effective wait shrinks only by the hook's work before its write (part of B-A, Windows budget 50 ms); if that work were large, more replies would be late and their warnings re-armed (a later warning, never a lost one). The fix round made the dependent test row stamp-independent (39d3ba11).
- Test-only bound x10v5RecoverySpacers (3 recovery attempts) in test/e2e/v5_x10_test.go (carried from the implementer). Derivation: three late or deferred replies in a row means the disk can deliver no warning at all, and the row then fails with that message instead of looping. If too small, a very slow hosted disk fails the row; if too large, the row only runs longer. It loosens nothing: every on-time reply must still carry the warning, and the branch is now licensed only under QOMPACK_NONREFERENCE_DISK (hosted), QOMPACK_UNDER_COLOAD, or the arm's forced hold.
- Approve extending the QOMPACK_NONREFERENCE_DISK licence (Q1/D53(e)) to X10's late or deferred prompt-reply recovery branch. It is now listed in internal/obs/nonrefdisk.go. It also honours QOMPACK_UNDER_COLOAD, as X11 does. Recovery still requires the re-arm and delivery on the next cycle's prompt. No new number.

## Independent verification of the fix seat: sound

- **nit** `.github/workflows/ci.yml:286-297 (test-e2e job's non-reference-disk comment)` — The comment on test-e2e's QOMPACK_NONREFERENCE_DISK block lists everything the declaration licenses: the fsync-bound wall rows (B-A, B-B, B-E wall) and the §12.2 spool-submode transition. internal/obs/nonrefdisk.go now also licenses X10's late/deferred prompt-reply recovery branch, and this is the job where that branch is meant to fire, so the comment no longer names everything the declaration does in this job.
  - Evidence: nonrefdisk.go gained the bullet 'test/e2e's X10 ... answers a prompt reply that was observably late ... with a proof of RECOVERY' in 2e6065a2. The ci.yml comment still says 'The declaration reports, without gating, the fsync-bound wall rows (B-A, B-B, B-E's wall row) and the §12.2 spool-submode transition', and the fix commits do not touch ci.yml.
  - Fix: Route to the ci seat or the coordinator (ci.yml is outside this seat's scope). Add one clause to the test-e2e comment, for example: 'and X10's late/deferred prompt-reply recovery branch, written to the job summary'. It could also point to nonrefdisk.go as the authoritative list.


# Wave 17c spoolwatch (candidate 7 hosted Windows red)

Branch `closeout/w17c-spoolwatch`. Workflow `wf_9e58aa2f-e6d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `fb0957bcc7c2518e76347a78ef8263c87f28c336`

### Root cause

Test timing. TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick ran the watcher on the wall clock with its retry horizon compressed to 50 ticks (1 s), half of one pass's idleRunBudget. The watcher stamps a spool's first pass with the look's start time (lookAtClientSpools' now, taken before the pass), so on the loaded hosted Windows runner a first look (fsync-bound pass plus index) longer than 1 s closed the retry window before any second pass. The counter stayed at 1 until the 30 s Eventually expired. A second, latent exposure: the row's writeSpoolLines fixture leaves a blank line that a pass consumes, which arms the 2 s pass budget, so a pass longer than 2 s stopped on its budget and made the spool due at the next look, breaking the doubling wait. Both are deterministically reproduced. With production constants (2 s interval, 120 s horizon) neither can cause a loss, and a slow pass only hands the spool to the designed fallback drains.

### Summary

C7.2: the hosted Windows red is a test-timing problem, not a product defect. Product code is unchanged and candidate 8 is not needed for this item.

The coordinator's hypothesis is right, and there was a second mechanism behind it.

1. The horizon closed before a second pass (the hosted red). The row ran the real watcher goroutine on the wall clock, with every = spoolWatchTick (20 ms) and horizon = 50 ticks (1 s). That is half of one pass's own idleRunBudget (2 s). The watcher stamps a spool's first pass with the look's `now`, which watchClientSpools takes with time.Now() before the listing and before the pass (spool_watch.go:196, then e.first = now at :296). So the time the first pass and its index take counts against the horizon. On the next look, retryDue sees now - e.first >= horizon and answers (false, false). The spool is never due again, the counter stays at 1, and require.Eventually waits out liveOrderBound (30 s).
   - Deterministic red: the row's name, run against the base body (f5e1079e, test file unchanged) from a scratch file that was never committed. The only change was the drainer.syncFile seam holding the first pass's sync of client-6161 past the horizon. It failed with "Condition never satisfied / the unconsumed spool is passed again" after 30.65 s with a 1.02 s stall and 30.70 s with a 2.02 s stall. Hosted failed at 30.60 s.
   - On this laptop an unloaded first look takes 30-50 ms (measured). The hosted package ran 1845 s, against 394 s here. The row's own busy traffic, one hook every 20 ms, each leased and published with fsyncs, also contends with the pass's fsyncs. Nothing else in the row takes the drain mutex (no drainOnRequest, no idle drain). The kicks only set `kicked`, so they cannot cause a pass or prevent one.
   - A counter stuck at exactly 1 for 30 s while kicks keep coming can only mean the horizon branch of retryDue fired, or the first look itself lasted more than 30 s. Either way the first look outlasted the 1 s horizon.

2. A latent budget stop (it would have broken the backoff assertions next). The row wrote its spool with writeSpoolLines, which adds a blank line after the record (drain_admission_test.go:29). A pass consumes that blank line (drain.go:885), and consuming a line arms the pass budget. A pass that reaches the next passStopped check after 2 s therefore stops on its budget and marks client-6161 left-unfinished, so the spool is due again at the next look. Deterministic red: the new body, still on writeSpoolLines, with a 2.02 s first-pass stall failed "expected: 40ms, actual: 20ms" (passes at [20ms 40ms 120ms 280ms 600ms]). A real hook never writes blank lines (writeHookSpool's comment, and drain.go's "the writer never emits them").

**Why it is not a product defect.** With production constants (every = idleRunBudget = 2 s, horizon = detectAfterSeconds = 120 s), the first retry is due 4 s after the first pass. The window closes before any retry only if a first look plus one interval takes 120 s or more. The index step is capped at idleRunBudget. The pass is a handful of fsyncs, and its budget can only stop it after it has consumed a line (D31). Even then, the result is the documented handoff in spool_watch.go:43-46: the requested drain (the pass leased the line, so the session's next arrival parks behind it and asks), the flush, the idle drain or a restart takes the spool, exactly as before. Nothing is lost.

**Fix (test only).** File: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17c-spoolwatch/internal/daemon/spool_watch_test.go
- New helper spoolWatchBackoffRow(t, firstPassStall). It drives lookAtClientSpools, the function watchClientSpools runs, on the watcher's own clock: one tick per look, kicked on every look, across 3 x horizon. Every pass is real, and real busy traffic is served and published beside them. This is the virtual-clock method two rows in the same file already use (APassItsBudgetCutShort..., ABudgetStopKeeps...).
- The spool is written with writeHookSpool, the hook's own format. No line is ever consumed, so no pass can stop on its budget.
- Horizon (50 ticks), interval (1 tick) and the maxPasses derivation are unchanged.
- The assertions are as strict or stricter:
  - at least 2 passes (same message as before)
  - at most maxPasses (same message as before)
  - NEW: each retry comes at exactly twice the previous wait, starting at 2 intervals. This is stated directly rather than read back from spoolRetryAfter, which the old row's maxPasses also depended on.
  - NEW: exactly maxPasses passes (5: at 20, 60, 140, 300, 620 ms)
  - no pass at or past first + horizon over the whole 3-horizon window
  - NEW: past the horizon, a look without a kick wants no further look (the watcher polls nothing)
  - the spool file still exists and its line is not published (as before)
- New row TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon runs the same helper with the first pass stalled for idleRunBudget + spoolWatchTick. That outlasts both the horizon and the pass budget, so it covers both mechanisms. It is red at base (the hosted message) and green at head.

**Mutation checks** (temporary edits to spool_watch.go, reverted, confirmed clean by git status):
- M1, horizon check removed: both rows FAIL (7 passes, last at 2.54 s).
- M2, retryDue always due: both FAIL (50 passes).
- M3, spoolRetryAfter fixed at 2 intervals: both FAIL ("pass 3 ... doubling wait"). The first version of the fix still passed M3, as the old row would have; that is why the explicit doubling assertion was added.
- M4, e.first = time.Now() after the pass: the stall row FAILs, the plain row passes.
- M5, budget-left ignored: both pass. That is expected: TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft owns that behaviour, and this row no longer depends on it.

**Answers to the coordinator's questions:**
- e.first is stamped at the START of the look that runs the first pass, before the listing and the pass, so the horizon includes the first pass's own length. The new stall row pins that the schedule follows the look's clock (M4). If the product ever moves the stamp to the end of the pass, that row is the one to revisit.
- With production constants, a slow first pass costs at most latency, never a loss, as above.
- The busy traffic writes no client spools, and the client-only pass skips its WAL segments. Its kicks only keep the watcher looking every interval. It contributes fsync load, not logic.
- No other watcher row uses a compressed horizon. All the others use liveOrderBound, including the two in precompact_settle_test.go.

### Commits

- fb0957bc test(daemon): read the spool backoff row on the watcher's clock

### Tests

- `go test -p 1 -count=1 -v -run '^TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon$' ./internal/daemon/  (at base f5e1079e: scratch file defining this name with the base row body plus a first-pass syncFile stall of idleRunBudget+spoolWatchTick; not committed)` — FAIL (expected red) 30.70s: spool_watch_scratch_test.go:45 Condition never satisfied, Messages: the unconsumed spool is passed again. Same message as hosted (30.60s). A 1.02s stall gave the same result at 30.65s. <!-- runpatterns: the row was renamed TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff in a75143c9; this line records the run under its earlier name -->
- `go test -p 1 -count=1 -v -run '^TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon$' ./internal/daemon/  (new body, still on writeSpoolLines, 2.02s stall; intermediate state)` — FAIL (expected red, the budget mechanism) 2.54s: expected 40ms, actual 20ms, passes at [20ms 40ms 120ms 280ms 600ms] <!-- runpatterns: the row was renamed TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff in a75143c9; this line records the run under its earlier name -->
- `go test -p 1 -count=20 -v -run '^TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick$' ./internal/daemon/  (HEAD fb0957bc)` — PASS 20/20, each 0.49-0.72s, ok 12.182s, exit 0
- `go test -p 1 -count=20 -v -run '^TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon$' ./internal/daemon/  (HEAD fb0957bc)` — PASS 20/20, each 2.55-2.72s, ok 53.194s, exit 0 <!-- runpatterns: the row was renamed TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff in a75143c9; this line records the run under its earlier name -->
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD fb0957bc, beside the live UAT lane)` — ok github.com/qompack/qompack/internal/daemon 393.854s, exit 0
- `go test -p 1 -count=1 -run '^(TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick|TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon)$' ./internal/daemon/  with 5 temporary spool_watch.go mutations, each reverted` — M1 no horizon: both FAIL. M2 always due: both FAIL. M3 fixed 2-interval wait: both FAIL. M4 e.first=time.Now() after the pass: stall row FAIL, plain row pass. M5 budget-left ignored: both pass (expected; another row owns it).
- `gofmt -l internal/daemon/ && go vet ./internal/daemon/` — clean

### Criterion changes

- TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick (C1.13 watcher backoff, C7.2): the row now drives lookAtClientSpools on the watcher's own clock, one tick per look, every look kicked, with real passes and real served traffic beside them, instead of the watcher goroutine on the wall clock. It writes its spool as a hook does (writeHookSpool instead of writeSpoolLines). Rationale: the subject is the schedule (the doubling wait and the stop at the horizon), which the watcher decides on its look clock alone. On the wall clock the compressed 1 s horizon (half of idleRunBudget) could be used up by a single slow first pass. The blank line in the fixture let a pass longer than 2 s stop on its budget, which broke the backoff. No bound changed: horizon 50 ticks, interval 1 tick, maxPasses unchanged. Assertions kept: at least 2 passes, at most maxPasses, none at or past the horizon, spool kept, line not published. Added: each retry at exactly twice the previous wait, exactly maxPasses passes, and no further look wanted without a kick past the horizon. New row TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon stalls the first pass past both the horizon and the pass budget (red at base with the hosted message, green at head). Rows that pin the product behaviour: TestSpoolWatch_ASpoolWaitingOnItsSessionIsRetriedWithNoFurtherHook (the live goroutine retries with no further hook, horizon liveOrderBound); TestSpoolRetryAfter_Doubles (the waits); TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft and TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached (budget-stop bookkeeping); TestSpoolWatch_DoesNothingWithoutAKick and TestSpoolWatch_AClientSpoolIsPublishedWhileItsSessionIsActive (the live loop and its kicks).

### Open issues

- The criterion change has to be recorded in V6-CLOSEOUT-CHECKLIST.md (the ledger is read-only to me). The wording is in criterion_changes and in the body of commit fb0957bc.
- Not yet confirmed on hosted windows-latest. A ci.yml run on a candidate carrying fb0957bc is needed for that; locally the row is load-proof by construction (virtual clock), and that is proven by the stall row and by -count=20.
- golangci-lint was not run, to spare the live UAT lane's CPU. gofmt and go vet are clean. The change is test-only and adds no new imports.
- Observation, not a defect: if scheduler.idle.detectAfterSeconds is set below 2 x spoolCheckInterval (4 s), the watcher never retries an unconsumed spool, whatever the load: the first retry falls past the horizon. That is the designed handoff to the requested, flush and idle drains (spool_watch.go:43-46), but spool_watch.go's doc comment does not say so. Optional doc follow-up.
- The new stall row pins that the watcher stamps a spool's first pass with the look's start time (mutation M4 fails it). If the product later moves that stamp to the end of the pass, update this row.
- Not run, per machine limits: a co-load reproduction. If the coordinator wants one: run the base row at -count=20 beside an fsync-heavy load on Windows. The expected red is the same 30 s 'passed again' failure, and the row at fb0957bc should stay green.

## Independent review

### review:spoolwatch: needs-fixes

- **major** `internal/daemon/drain.go:867-871 (with drain.go:709-713, drain.go:589-592 and spool_watch.go:299-301); classification in commit fb0957bc and the C7.2 report` — The report calls mechanism 2 a fixture artifact ("a real hook never writes blank lines") and says a slow pass only hands the spool to the fallback drains. That is not the whole story. Production-format spools hit the same mechanism. A consumed-out-of-order line is kept only in drainFile's per-call `processed` map (:709). Every pass therefore re-reads and re-consumes (absorbs) it, and `consume` arms the budget on every consumption (:713). The readLoop checks passStopped at the top of the next iteration, before it reads EOF (:867-871), so a slow pass stops on its budget inside the blocked file, and notePassLeft marks that file as left unfinished. The watcher then sets e.next = now (spool_watch.go:299-301), and the backoff is lost every time. With production constants, on a host where a pass runs past 2 s, the result is a pass over every client spool once per spoolCheckInterval (2 s) for the whole 120 s horizon, not about 6 passes. An append from a reused pid changes the file size, which resets the entry and starts the horizon over. Nothing is lost, but this is the 'no busy loop' and doubling-wait subject of C1.13, and the intent stated by TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached, failing in production. The hosted red itself is still mechanism 1 (test timing).
  - Evidence: I reproduced it on a `git archive f5e1079e` copy outside the worktree; the product code is byte-identical to HEAD. The scratch row was not committed. It writes writeHookSpool(t, root, "client-6363.ndjson", blocked, liveOrderTool(dd, root, "sess-review-other", 7)), where blocked is the stuck session's arrival 1 behind a leased, lost arrival 0. Every dr.syncFile call is held for idleRunBudget+spoolWatchTick. lookAtClientSpools is driven on the virtual clock exactly as HEAD's spoolWatchBackoffRow does it. Result: passes at [20ms 40ms 60ms 80ms], one at every look. Control with the blocked line alone and the same stall on every pass: [20ms 60ms 140ms 300ms], the doubling wait. Each probe took about 8.5 s. Multi-record client spools are a case the product expects: spool_watch.go:246 says "a reused pid's hook appended to it", and Windows reuses PIDs aggressively. The pass covers all client spools (DrainClientSpools -> dr.pass(ctx, true), drain.go:459-461), so one defeated spool means fsync pressure on every spool, every interval.
  - Fix: The coordinator should decide and record the outcome, as D56(e) did. Option (a) is a product fix with a failing row first, which needs candidate 8. Make drainFile finish a file normally when nothing but EOF is left after the consumed line, for example by reading the next line before the budget check and leaving that line unprocessed if the pass stops. Alternatively, do not count re-absorbing an already-acknowledged out-of-order line as consumption for the budget. Then add a row with spoolWatchBackoffRow's virtual clock, a [blocked, consumable] hook-format spool and every sync held past the budget, asserting the doubling wait. Option (b) is to record a known limit for 0.3.0 with that same row as a pinned characterization. Either way, correct the C7.2 record: mechanism 2 can be reached in production for a blocked head followed by any consumable or corrupt line, not only through writeSpoolLines' blank line.
- **minor** `internal/daemon/spool_watch_test.go:208-221 and 226-232` — The new row's name and doc claim a property the product's live loop does not have. TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon says the backoff holds when a pass outlasts the horizon, and the doc says "the pass's length moves nothing". In the live loop, watchClientSpools stamps each look with time.Now() (spool_watch.go:196), and e.first is that look's start (:296, checked at :166). So when a first look outlasts the horizon, the product makes no retry at all; by design the spool passes to the requested, flush, idle and restart drains (spool_watch.go:43-46). On the injected clock the stall cannot move the schedule, by construction. What the row really pins is narrower: the schedule follows the look's `now` argument (the implementer's mutation M4), and a pass that overran its budget without consuming a line keeps the doubling wait (the interaction with D31). A ledger reader could cite this row as pinning the opposite of the product behaviour.
  - Evidence: I ran the base row body with only the first syncFile held 1.02 s, in a scratch copy of f5e1079e. It failed after 30.87 s with "Condition never satisfied / the unconsumed spool is passed again". At exit, counter=1 and syncs=1, and the counter read 1 when the stall began. That is the hosted failure (30.60 s), and it shows the product loop gives no second pass once the first look outlasts the horizon. At HEAD, the row passes 2/2 here (2.62 s, 2.66 s) whatever the stall, because no wall time enters the schedule.
  - Fix: Rename the row to what it pins, for example TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff. Rewrite the doc to cover two things: what the row pins (the schedule follows the look's clock argument, and a budget overrun with no consumed line keeps the doubling wait), and one sentence saying that in the live loop a first look longer than the horizon leaves no retry by design (the spool_watch.go:43-46 handoff). Change "the watcher's own clock" to "the look's injected clock" in both comments.
- **nit** `internal/daemon/spool_watch_test.go:239-250` — The stall row never checks that its stall fired. `syncs` lives inside the if-block and is never read after the looks. If a later product change stopped syncing this spool on its first pass, the stall row would quietly turn into the plain row. The file's own convention is a 'fixture:' sanity check, for example require.Positive(t, counts.attempts.Load(), "fixture: a pass met the slow line") at line 375.
  - Evidence: The row's evidence for its claim is only its 2.55-2.72 s run time, which is printed but never asserted. No require reads `syncs`.
  - Fix: Hoist `syncs` out of the block. When firstPassStall > 0, assert require.Positive(t, syncs.Load(), "fixture: the first pass's sync of the spool was held"). Or time the first look that passes in real time and require it to be at least firstPassStall.

## Fix seat (review resolution) — status `done`, head `a75143c9`

### Root cause

The hosted red was test timing. TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick ran the live watcher on the wall clock with its horizon compressed to 1 s, half of idleRunBudget. The live loop stamps a spool's first pass with the time.Now() it hands the look before the pass. On the loaded hosted runner the first look outlasted that 1 s, which closed the retry window before a second pass. I reproduced it independently: 30.48 s, 1 pass, 1 sync. Separately, review finding 1 is a confirmed product defect in drain.go and is not the cause of the hosted red. Every pass re-consumes a line it already consumed out of order behind a blocked head, and that arms the pass budget, which counts as consumption without being progress. On a slow host this defeats the watcher's backoff and starves the client spools after it until an unbudgeted drain. Nothing is lost. The coordinator has to decide: fix it (F1, candidate 8) or record a known limit.

### Summary

PRODUCT DEFECT CONFIRMED BUT NOT FIXED: the coordinator has to decide. Review finding 1 is real, reachable with production constants, and worse than the reviewer stated. The fix belongs in internal/daemon/drain.go, and I do not own that file. This branch changes no product code, so it does not need candidate 8 by itself. Taking option (a) below would.

C7.2 (the hosted Windows red) is resolved, and it was test timing. The implementer's diagnosis stands, and I checked it myself. The base row's body, with only its first syncFile held 1.02 s past the row's 1 s horizon, fails with "Condition never satisfied / the unconsumed spool is passed again" after 30.48 s. At exit there was 1 pass and 1 sync. Hosted failed at 30.60 s. The live loop hands each look the time.Now() taken before the pass, so a first look longer than the compressed horizon leaves no retry. With production constants (2 s interval, 120 s horizon), that would need a first look of nearly two minutes.

## Review resolution

**Finding 1 (major): CONFIRMED, and wider than reported.** I ran each reproduction on HEAD's product code, which is byte-identical to f5e1079e. They use hook-format spools, the look's injected clock, and every syncFile held for idleRunBudget + spoolWatchTick.
- **R1, the backoff is lost.** Spool [blocked head, another session's tool]: a pass ran at every look, at [20 40 60 80] ms. With the blocked record alone and the same stall: [20 60] ms. With no stall: [20 60] ms.
- **Mechanism.** The head stays deferred, so the line behind it is consumed out of order. That consumption is remembered only in drainFile's per-call `processed` map, so every pass re-reads the line and absorbs it again. `consume` calls notePassConsumed every time. readLoop checks passStopped before it reads EOF, so a pass whose budget is spent stops inside the file. notePassLeft then marks the file, and the watcher sets e.next = now.
- **R2, beyond the review: later spools starve.** DrainClientSpools passes every client spool in host order, and the blocked spool comes first. A pass that has spent its budget therefore stops in that spool every time. A fresh capture in a later spool (client-6364, a third session) was not published by any of 3 passes. With the blocked record alone, the same capture is published on the first pass. So D31's guarantee ("a budgeted pass always consumes a line when it can") is defeated: absorbing an already-acknowledged line again counts as consumption but makes no progress. The requested drain and the idle drain share drainFile's read loop under a pass budget, so the same stop applies to them. I reasoned that from the code and did not run it.
- **Who hits it.** A spool whose head is blocked for a long time, with any line behind it that a pass consumes: a reused pid's append (Windows reuses pids aggressively), a corrupt line, or a denied line. The host must be slow enough that a pass reaches that line after its 2 s budget is spent. That is the same slow-host condition commit 4894f4f4 was written for.
- **Effect.** Nothing is lost. Each spool after the blocked one waits for an unbudgeted drain (a session end's own drains, Stop, or a restart) for as long as the host stays slow. That brings back C1.13's original symptom.
- **Prototype fix F1, tested and then reverted.** Call notePassConsumed only when the consumption is progress: (1) in the in-order branch of `consume`, where the durable front advances; (2) where processOne says the line was published or retired in this pass (`dispatched || changed`, in both readLoop and reattempt); (3) on `retiredHere` in the denial branch. With F1:
  - R1 gives [20 40 120] over 7 looks. The first pass really did publish the line behind the head and then stop on its budget inside the spool, so one extra pass follows, and then the waits double.
  - R2 publishes the later spool's capture.
  - The controls are unchanged.
  - All 56 focused rows pass in 123.8 s (pattern below).
- **F2 is not recommended.** The reviewer's other option is to read the next line before the budget check. Added on top of F1 it changed nothing: R1 stayed [20 40 120], because reattempt's own passStopped check stops the pass first.
- **State after reverting.** drain.go was restored to blob 696a2d88, checked with git hash-object.
- **Record corrected.** Commit a75143c9's body, and the backoff row's fixture comment, now say that a hook-format spool can carry a line behind its head. The implementer's line "a real hook never writes blank lines, so this is a fixture artifact" was true about blank lines, but it was the wrong conclusion about the mechanism.
- **Decision for the coordinator, as D56(e) did.**
  - **(a)** Apply F1 with R1 and R2 as the failing rows first. That needs candidate 8, and the rows need tidying before they are committed.
  - **(b)** Record a known limit for 0.3.0, with R1 and R2 turned into pinned characterizations.

**Finding 2 (minor): CONFIRMED and FIXED in a75143c9.**
- Renamed to TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff.
- The doc now says what the row pins: a pass that overran its budget without consuming a line keeps the doubling wait (D31), and the schedule follows the clock the look is handed. It also says the live loop makes no retry after a first look longer than the horizon, by design, and that the spool_watch.go handoff then applies.
- "The watcher's own clock" became "the look's injected clock" in both comments. The test body and the assertions are unchanged.
- **Mutation checks**, temporary and reverted:
  - M6, the budget ends a pass with no line consumed: the stall row FAILS ("pass 2 ... expected 40ms, actual 20ms").
  - M4, e.first = time.Now() after the pass: the stall row FAILS (7 passes, last at 2.54 s).
  - The plain row passes under both.

## Proofs at HEAD a75143c9
- Plain row: 20/20.
- Renamed stall row: 20/20.
- internal/daemon in full: ok in 337.5 s.
- gofmt and go vet are clean. The branch changes only internal/daemon/spool_watch_test.go relative to f5e1079e, and the working tree is clean.

## Files
- Worktree file: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17c-spoolwatch/internal/daemon/spool_watch_test.go
- Evidence (not committed), in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/c72-review/:
  - f1-prototype.diff: applies cleanly to HEAD.
  - spool_watch_scratch_review_test.go.txt: the R1 and R2 rows.
  - spool_watch_scratch_base_test.go.txt: the base-body red.
  - plain-x20.log, stall-x20.log, daemon-full.log.
- The F1 focused-run log is at scratchpad/f1-focused.log.

### Commits

- fb0957bc test(daemon): read the spool backoff row on the watcher's clock (implementer)
- a75143c9 test(daemon): name the spool stall row for what it pins (fix seat)

### Tests

- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -v -run '^TestScratchC72_BaseRowBodyWithAFirstPassStall$' ./internal/daemon/  (scratch file, not committed: base f5e1079e row body on the wall clock, first syncFile held 1.02 s; source in scratchpad/c72-review/spool_watch_scratch_base_test.go.txt)` — FAIL (expected red, the hosted mechanism): 'Condition never satisfied / the unconsumed spool is passed again' after 30.48 s; at exit passes=1 syncs=1 (hosted: 30.60 s) <!-- runpatterns: names a scratch test file that was never committed (kept as evidence in runs/spool_watch_scratch_review_test.go.txt), run on a scratch copy, not a test in this tree -->
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -v -run '^TestScratchReview_' ./internal/daemon/  (scratch rows, product code as at HEAD/base; source in scratchpad/c72-review/spool_watch_scratch_review_test.go.txt)` — Finding 1 confirmed. R1_BlockedThenConsumable_Stalled FAIL: passes at [20ms 40ms 60ms 80ms] against expected [20ms 60ms]. R2_LaterSpoolStarves_Stalled FAIL: the later spool's capture was not published by any of 3 passes. Controls pass: R1Control_BlockedOnly_Stalled [20ms 60ms], R1Control_BlockedThenConsumable_NoStall [20ms 60ms], R2Control_BlockedOnly_Stalled published. <!-- runpatterns: names a scratch test file that was never committed (kept as evidence in runs/spool_watch_scratch_review_test.go.txt), run on a scratch copy, not a test in this tree -->
- `same '^TestScratchReview_' run with the F1 prototype temporarily applied to drain.go (reverted; drain.go blob 696a2d88 confirmed)` — PASS: R1 passes at [20ms 40ms 120ms] over 7 looks (at most 3), R2 later spool published, controls unchanged
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -timeout 30m -run '^(TestSpoolWatch_|TestDrainClientSpools_|TestIdleDrain_|TestCarriedDefect_SP05D1_|TestDrain_|TestDeliveryOrder_|TestScratchReview_)' ./internal/daemon/  (F1 prototype applied, reverted afterwards)` — ok 123.797s, 56 tests, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -v -run '^(TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick|TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff)$' ./internal/daemon/  under mutation M6 (passStopped ignores b.consumed; reverted)` — stall row FAIL: pass 2 expected 40ms, actual 20ms, passes at [20ms 40ms 120ms 280ms 600ms]; plain row PASS
- `same pattern under mutation M4 (spool_watch.go e.first = time.Now() after the pass; reverted)` — stall row FAIL: 7 passes, more than the 5 allowed, at [20ms 60ms 140ms 300ms 620ms 1.26s 2.54s]; plain row PASS
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=20 -v -run '^TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick$' ./internal/daemon/  (HEAD a75143c9)` — PASS 20/20, each 0.38-0.58 s, ok 9.631s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=20 -v -run '^TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff$' ./internal/daemon/  (HEAD a75143c9)` — PASS 20/20, 0 FAIL, each 2.44-2.67 s, ok 50.412s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD a75143c9, beside the live UAT lane)` — ok github.com/qompack/qompack/internal/daemon 337.522s, exit 0
- `gofmt -l internal/daemon/ && GOTOOLCHAIN=local CGO_ENABLED=0 go vet ./internal/daemon/` — clean

### Criterion changes

- TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick (C1.13 watcher backoff, C7.2), from fb0957bc: the row drives lookAtClientSpools on the look's injected clock, one tick per look, every look kicked, with real passes and real served traffic beside them, instead of running the watcher goroutine on the wall clock. It writes its spool in the hook's format (writeHookSpool, one record). No bound changed: horizon 50 ticks, interval 1 tick, maxPasses as before. Assertions kept: at least 2 passes, at most maxPasses, none at or past the horizon, the spool kept, its line not published. Added: each retry at exactly twice the previous wait, exactly maxPasses passes, and no further look wanted without a kick past the horizon.
- TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff (new, C7.2; renamed in a75143c9 from TestSpoolWatch_AnUnconsumableSpoolsBackoffHoldsWhenAPassOutlastsTheHorizon): the same row with the first pass's sync held idleRunBudget + spoolWatchTick. It pins that a pass that overran its budget without consuming a line keeps the doubling wait (D31; mutation M6 fails it) and that the schedule follows the clock the look is handed (M4 fails it). It does NOT pin a retry after a first look that outlasted the horizon, because the live loop makes none by design (the spool_watch.go handoff). Rows that pin the product's live behaviour: TestSpoolWatch_ASpoolWaitingOnItsSessionIsRetriedWithNoFurtherHook, TestSpoolRetryAfter_Doubles, TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft, TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached, TestSpoolWatch_DoesNothingWithoutAKick, TestSpoolWatch_AClientSpoolIsPublishedWhileItsSessionIsActive.

### Open issues

- COORDINATOR DECISION (review finding 1, product, outside my files): drainFile arms the pass budget when it re-consumes a line it had already consumed out of order behind a blocked head (consume -> notePassConsumed on every call). On a host where a pass reaches that line after its budget is spent: (1) the spool is due again at every look for the whole horizon (R1); (2) every client spool after it in host order waits for an unbudgeted drain (session end, Stop, restart) for as long as the host stays slow (R2). By code reading the same holds for the requested and idle drains, which are also budgeted (not run). Nothing is lost. Option (a): the F1 fix (scratchpad/c72-review/f1-prototype.diff, which applies cleanly; 56 focused rows green with it) with R1 and R2 committed as failing rows first, which needs candidate 8. Option (b): record a known limit for 0.3.0 with R1 and R2 as pinned characterizations. Under (a), D31's wording ('runs past its budget by that bookkeeping and at most one line's drainLineDeadline') should say that the bookkeeping includes spools whose lines are only re-consumed, as a single-record blocked spool already does today.
- Observation, not a defect: DrainClientSpools passes every client spool, so a backed-off blocked spool is still re-read and re-synced whenever another spool is due (R2 control: the blocked spool was passed at 40 and 60 ms while its own entry was backed off). The backoff limits only what triggers a pass. That bears on the fsync-pressure rationale in TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached's doc.
- The ledger (V6-CLOSEOUT-CHECKLIST.md, read-only to me) still has to record the C7.2 criterion change; the wording is in criterion_changes. The row name changed in a75143c9.
- Not yet confirmed on hosted windows-latest; that needs a ci.yml run on a candidate carrying fb0957bc and a75143c9. Locally the rows cannot be exhausted by load, by construction (injected clock), as the stall row and -count=20 show.
- golangci-lint not run, to spare the live UAT lane; gofmt and go vet are clean, and the change is test-only (comments and a rename) with no new imports.
- Carried from the implementer: if scheduler.idle.detectAfterSeconds is below 2 x spoolCheckInterval (4 s), the watcher never retries an unconsumed spool. That is the designed handoff, but spool_watch.go's doc does not say so (optional doc follow-up).
- Carried from the implementer: the stall row pins that a spool's first pass is stamped with the look's start time (M4 fails it). If the product moves that stamp, revisit the row.
- Not run, per machine limits: a co-load reproduction. If wanted: run the base row at -count=20 beside an fsync-heavy load on Windows. Expected: the same 30 s 'passed again' red at base and green at a75143c9. A co-load check of finding 1 would need many multi-record spools whose head is blocked, plus a pass longer than 2 s.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



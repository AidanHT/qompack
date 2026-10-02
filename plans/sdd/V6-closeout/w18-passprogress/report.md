# Wave 18 passprogress: a drain pass budget counts only progress (C1.13, D31, D58(c))

Branch `closeout/w18-passprogress`. Workflow `wf_aed1c8a1-01a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `cda2bd90cc77a9dd842f2c56383983722f8eb040`

### Root cause

drainFile's consume closure called notePassConsumed on every consumption. A line consumed out of order (behind a head waiting on an earlier arrival of its session) is remembered only in that call's processed map, so every later pass reads it and consumes it again: it absorbs an acknowledged copy, skips a retired or corrupt line again. Each of those re-consumptions armed the pass budget. On a host where a pass outlasts idleRunBudget, every budgeted pass then stopped inside the blocked spool having made no progress. The watcher re-passed the spool at every look (lost backoff, R1). No budgeted pass (watcher, requested or idle) reached a spool after it in host order (starvation, R2). A requested pass returned errPassBudgetSpent each time, so passLeftWork asked for another pass after every one, for as long as the host stayed slow. Nothing was lost: captures waited for an unbudgeted drain.

### Summary

PRODUCT CODE CHANGED: candidate 8 needed

Branch closeout/w18-passprogress, one commit cda2bd90 on 7aeb5c6e. Files changed: internal/daemon/drain.go (+28/-6), the new files internal/daemon/drain_pass_progress_test.go and internal/daemon/spool_watch_pass_progress_test.go, and one fixture comment in internal/daemon/spool_watch_test.go.

FIX (F1 without the prototype's top-of-loop removal). Now only progress counts as a pass consuming a line. There are four call sites in drain.go drainFile:
- S1, consume: notePassConsumed moved from the top of consume into its in-order branch, so it fires only when the consumed front advances.
- S2, readLoop done branch: `if changed { notePassConsumed(ctx) }` before consume.
- S3, reattempt (look-ahead) done branch: the same.
- S4, denial branch: `if retiredHere { notePassConsumed(ctx) }` before consume.
`changed` is processOne's "this pass published the line or retired it by a proven denial", and dispatched implies changed. Consuming an out-of-order line again (absorbed, already retired, corrupt, blank, unadmitted) no longer counts.

Doc comments updated:
- withPassBudget: "consuming a line means making progress". The D31 bookkeeping allowance now includes spools whose lines are only consumed again, the D31 wording change the w17c fix seat asked for.
- notePassConsumed: states what counts.
- A new comment on consume.

TOP-OF-LOOP passStopped CHECK: kept. Decided with evidence:
- (a) Every new row is green with the check in place, so no row needs it removed.
- (b) With the prototype's removal applied (mutation M5), all 206 focused rows plus subtests still passed: no existing row pinned the check.
- (c) Without the check, a pass that has spent its budget and consumed a line through a `continue` path (corrupt, blank, unadmitted or deferred line) reads and publishes the next line. That breaks D31's "starts no new line". The new row TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced now pins it: green on base, red under M5.
F2 was not applied.

NEW ROWS. All use hook-format spools. The watcher rows use the look's injected clock; the seam slowSpoolSyncs holds every client-spool sync for idleRunBudget+spoolWatchTick. Each row is red on base (drain.go blob 696a2d88) and green at HEAD unless noted.
- TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff (R1): passes at exactly [20ms 40ms 120ms]. Base gave [20ms 40ms 60ms 80ms] (fail-fast).
- TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt (R2): the later spool's capture is published by the 2nd pass. Base published nothing in 3 passes.
- TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead (requested-drain variant): after a pass that only re-consumed, drainKick stays empty, and the next pass publishes a later spool. Base: drainKick held 1 ("asks again").
- TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead (idle variant): the 2nd idle pass publishes the next spool. Base: not published.
- TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass (budget-0, deterministic; subtests: a published delivery, a retired delivery, a corrupt line): all three red on base.
- TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass (budget-0; subtests: published as read, retired as read, published by the look-ahead): guards against over-correction. The first two pass on base. The look-ahead one is red on base, because the absorbed copy of p0 ended the pass before the look-ahead published p1.
- TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced: green on base by design.

MUTATIONS (each applied alone to the fixed drain.go and reverted; the drain.go blob was checked as 09f0a4dd after every one):
- M1, S1 reverted (note at the top of consume): fails both ReconsumingALine... subtests sets (all 3), the look-ahead subtest, the requested row, the idle row, R1 and R2.
- M2, S2 removed: fails ALinePublishedOrRetired.../published_as_it_is_read.
- M3, S3 removed: fails .../published_by_the_look-ahead.
- M4, S4 removed: fails .../retired_as_it_is_read.
- M5, the prototype's top-of-loop removal: fails ASpentPassStartsNoLineAfterItsFrontAdvanced, and only that row among the focused set.
- M6, the in-order note dropped from consume: fails ASpentPassStartsNoLineAfterItsFrontAdvanced.

PRESERVED: D31 (a budgeted pass with nothing consumed is never cut, and it still consumes a line when it can). The three named budget rows are green 10/10: TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft, TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached and TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff. Also green: the ordering gate rows (TestDeliveryOrder_*), the roll-forward cap and the durability rows (TestDrain*), and the PreCompact settle rows (200 TestPreCompactSettle_ passes over 10 counts). Offsets, leases, WAL handling and seals are untouched: the change only decides which consumptions call notePassConsumed.

Commit message validated with `go run ./tools/devtool check-commit-msg`. It has no attribution trailers and the footer `Refs: V6-VERIFY, C1.13`.

### Commits

- cda2bd90 fix(daemon): count only progress against a drain pass budget

### Tests

- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -timeout 20m -v -run '^(TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass|TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass|TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced|TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead|TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead|TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff|TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt)$' ./internal/daemon/  (HEAD tests, drain.go temporarily at base blob 696a2d88 via git show 7aeb5c6e:internal/daemon/drain.go, restored with git checkout)` — FAIL as intended, 26.3s. Red on base: Reconsuming... (3/3 subtests), ALinePublishedOrRetired.../published_by_the_look-ahead, the requested row (drainKick 1, 'Should be zero, but was 1'), the idle row, R1 (actual [20ms 40ms 60ms 80ms] against [20ms 40ms 120ms]) and R2 (passes at [20ms 40ms 60ms], capture never published). Green on base by design: published_as_it_is_read, retired_as_it_is_read and ASpentPassStartsNoLineAfterItsFrontAdvanced.
- `same pattern at HEAD cda2bd90 (fix applied)` — PASS, every row and subtest. R1 6.4s, R2 6.6s, requested 8.6s, idle 6.5s, drain-level rows under 1.5s each.
- `mutation runs: M1 against the seven new rows above; M2, M3 and M4 with -run '^TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass$'; M5 and M6 with -run '^TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced$' (each mutation applied to the fixed drain.go and reverted, blob 09f0a4dd checked after every one)` — Every mutation is killed. M1: 8 failures (3 reconsume subtests, look-ahead, requested, idle, R1, R2). M2: published_as_it_is_read. M3: published_by_the_look-ahead. M4: retired_as_it_is_read. M5: front-advanced row. M6: front-advanced row. Each failed on 'the pass started no line after it' or n==1.
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -timeout 30m -v -run '^(TestDrain|TestSpoolWatch_|TestSpoolRetryAfter_|TestIdleDrain_|TestCarriedDefect_|TestDeliveryOrder_|TestDispatchLanes_|TestPreCompactSettle_|TestPrecompactSettleBound_|TestSpoolHeadIndex_|TestSpoolLineHead_|TestUnreplayedDrops_|TestSP08D3_)' ./internal/daemon/  under mutation M5 (prototype top-of-loop removal), before the front-advance row existed` — ok 158.2s with 206 RUN lines: no existing row pinned the check (the evidence for keeping it and adding the row).
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD cda2bd90, run 1)` — ok 342.085s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD cda2bd90, run 2)` — ok 336.952s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=10 -timeout 60m -v -run '^(TestDrain|TestSpoolWatch_|TestSpoolRetryAfter_|TestIdleDrain_|TestCarriedDefect_|TestDeliveryOrder_|TestDispatchLanes_|TestPreCompactSettle_|TestPrecompactSettleBound_|TestSpoolHeadIndex_|TestSpoolLineHead_|TestUnreplayedDrops_|TestSP08D3_)' ./internal/daemon/  (HEAD)` — ok 1572.894s: 1650 top-level PASS (165 tests x 10), 0 FAIL. Every new row 10/10. TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft, TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached and TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff 10/10 each. 200 PreCompact settle passes.
- `go vet ./internal/daemon/  and  GOOS=linux go vet ./internal/daemon/` — clean, both
- `go run ./tools/devtool fmt-check` — exit 0, no offenders
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/` — exit 0, no findings
- `go run ./tools/lint/nomagic ./internal/daemon/` — exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 ./test/docs/` — ok 4.566s

### Criterion changes

- No existing assertion was changed, loosened or skipped. TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff/spoolWatchBackoffRow: only its fixture comment changed. It no longer says a pass consuming a blank, corrupt or appended line behind the head can be stopped by its budget, which the fix made untrue for re-consumptions. It now says the first pass that publishes such a line can be stopped, that later re-consumptions are no progress, and it points to the R1 row for that schedule.
- New criterion rows for C1.13/D31: the seven tests listed in the summary. Ledger wording for D31 suggested: 'a pass's budget can end it only once it has made progress: advanced a spool's consumed front, or published or retired a line; consuming again a line an earlier pass consumed out of order is not progress, so a pass whose only consumption is that runs past its budget by that bookkeeping like a pass that consumed nothing (C1.13, candidate 8).'

### Open issues

- Ledger: C1.13, D31 and the w17c option (a) decision have to be recorded in qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md (outside my worktree). That covers candidate 8 for this product change and the new row names.
- -race was not run (machine limits; the coordinator runs it at night). The new rows run the drain on the test goroutine. Only publishLiveFirst calls dd.ing.dispatch from inside the drain's Admit callback, synchronously, so it adds no goroutine.
- Hosted CI was not run. The rows are load-insensitive by construction: the watcher rows use an injected clock, the drain-level rows use budget 0, and the stall rows only need a sync longer than idleRunBudget, which the timer guarantees.
- Behaviour note, by design: a budgeted pass whose budget is spent but which has made no progress now reads a blocked spool to EOF through its re-consumed lines before going on, instead of stopping at the first one. That is the same work an unspent pass already does over that file, and D31's 'bookkeeping' allowance now covers it in withPassBudget's doc. A spool with very many out-of-order lines behind a stuck head therefore costs a slow pass more reading, but no longer costs a whole wasted pass.
- docs/architecture.md (around line 176) says the budget rule as 'once it is spent a pass starts no new line', without the consumption condition. I left it unchanged: it does not state the consumption rule, and it was not wrong before or after. delivery_order.go's passLeftWork doc ('A pass its budget ended has consumed a line by then') is now true in the stronger sense; it is not my file and was left alone.
- Carried from the w17c fix seat, unchanged by this fix: DrainClientSpools passes every client spool, so a backed-off blocked spool is still re-read and re-synced whenever another spool is due. The backoff limits only what triggers a pass.

## Independent review

### review:passprogress:behaviour: needs-fixes

- **minor** `internal/daemon/drain.go:1021 (S2's `if changed`), internal/daemon/drain_pass_progress_test.go:172 (TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass)` — No row pins the 'retired' half of S2's condition. A leased line behind a blocked head can be admitted when the read loop reads it and then denied when dispatchPending admits it again (drain.go:1488-1494). processOne then returns done with changed=dl.leased and dispatched=false (drain.go:815-820). If S2 tested `dispatched` instead of `changed`, that retirement would not count. The spent pass would then start one more line, which D31's 'at most one line' bound forbids, and no committed row would fail. The existing 'retired as it is read' subtest goes through the denial branch (S4), not through processOne.
  - Evidence: I worked in a scratch copy (git archive of HEAD) with the drain.go blob checked after each mutation; the worktree was untouched and git status is clean. Mutation M7, drain.go:1021 `if changed {` changed to `if dispatched {`: all three drain-level rows pass (ok 3.062s). I wrote a scratch row, not committed: the line is leased, cfg.Admit admits its first admission and denies from the second, and a fresh capture sits in a later spool; the pass runs under withPassBudget(ctx, 0). It passes at HEAD (0.41s) and at base. Under M7 it fails: 'Should be false / the retirement at dispatch ended the spent pass', because the fresh capture was published past the spent budget. Everything else checked out. All seven new rows fail on base 696a2d88 for the stated reasons: R1 actual [20ms 40ms 60ms 80ms] against [20ms 40ms 120ms]; R2 passes at [20ms 40ms 60ms] with the capture unpublished; requested drainKick 'Should be zero, but was 1'; the idle capture unpublished; the reconsume subtests 3/3 and the look-ahead subtest red. They are green at 09f0a4dd (ok 32.8s). Mutations M1 to M6 each fail the rows the report names. The existing budget, backoff and settle rows are unchanged; only a fixture comment changed. Every quoted -run pattern and prefix matches real tests (165 for the count=10 set). The commit passes devtool check-commit-msg and carries no attribution trailer.
  - Fix: Add a subtest 'retired as it is dispatched' to TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass. Lease `other`, then wrap cfg.Admit with a per-nonce atomic counter that admits the first call and returns Denied from the second. Write [blocked head, other] and a later spool holding `fresh`, and run DrainClientSpools(withPassBudget(ctx, 0)). Assert j.terminalDenied(lease) is true, the error is errPassBudgetSpent, and fresh is not published. Show it red under the `if dispatched` mutation and green at HEAD.
- **nit** `internal/daemon/drain.go:860 (S3's `if changed` in reattempt)` — No row pins S3's condition either. Making the note at drain.go:860 unconditional changes no row's result. So nothing checks that a deferred line the look-ahead absorbs (already acknowledged, changed=false) is no progress. It only happens inside one pass: the line has to become acknowledged between the read and the re-attempt. It cannot recur across passes, so it does not reopen R1 or R2. The removal mutation (M3) is killed; only the condition is unpinned.
  - Evidence: Mutation M8, drain.go:860 `if changed {` changed to `if true {`: all three drain-level rows pass (ok 3.425s). Every new row reaches reattempt either with nothing resolving (the blocked head) or with a line it publishes (p1 in the look-ahead subtest, changed=true).
  - Fix: Optional. Add a variant of the look-ahead subtest: accept both p0 and p1 live, and when p0's spooled copy is admitted, have publishLiveFirst publish both jobs. The look-ahead then absorbs p1 with changed=false. Assert the spent pass goes on to publish the next spool's capture, and show it red under the unconditional-note mutation. Otherwise, say in the consume/notePassConsumed comment that the condition is deliberately left unpinned.

### review:passprogress:correctness: sound


## Fix seat (review resolution) — status `done`, head `a1113fa9160c938523e25b7046b37a1150cc1336`

### Root cause

drainFile's consume closure called notePassConsumed on every consumption. A line consumed out of order (behind a head that waits on an earlier arrival of its session) is remembered only in that call's processed map. Every later pass reads it and consumes it again: it absorbs an acknowledged copy, or skips a retired or corrupt line again. Each of those repeat consumptions counted against the pass budget. On a host where a pass outlasts idleRunBudget, every budgeted pass then stopped inside the blocked spool without making progress. Two effects followed. The watcher passed that spool again at every look, so the backoff was lost (R1). No budgeted pass (watcher, requested or idle) reached a spool after it in host order, which is starvation (R2). A requested pass returned errPassBudgetSpent every time, so passLeftWork asked for another pass after each one. Nothing was lost: captures waited for an unbudgeted drain (session end, Stop, restart). The fix, cda2bd90, counts only progress: the consumed front advancing in order, or a line this pass published or retired (changed or retiredHere). That fix is unchanged by this round. This round adds rows only.

### Summary

PRODUCT CODE CHANGED: candidate 8 needed

Branch closeout/w18-passprogress, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w18-passprogress, HEAD a1113fa9. Two commits on 7aeb5c6e:
- cda2bd90 (implementer): the product fix in internal/daemon/drain.go (blob 09f0a4dd) plus the new rows.
- a1113fa9 (this fix seat): test only. It adds two subtests to internal/daemon/drain_pass_progress_test.go. drain.go is unchanged and still blob 09f0a4dd.

THE FIX (cda2bd90, unchanged)
- notePassConsumed fires at four places in drain.go drainFile:
  - S1: in consume's in-order branch, when the front advances.
  - S2: in the read loop's done branch, `if changed`.
  - S3: in reattempt's done branch, `if changed`.
  - S4: in the denial branch, `if retiredHere`.
- Consuming an out-of-order line again (absorbed, already retired, corrupt, blank, unadmitted) no longer counts against the budget.
- The top-of-loop passStopped check is kept, on evidence: the prototype's removal (M5) broke no existing row and allows a spent pass to start a new line. TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced now pins that check.
- F2 was not applied.
- Doc comments that state the consumption rule (withPassBudget, notePassConsumed, consume) were updated in cda2bd90.

REVIEW RESOLUTION
Finding 1 (minor): "No row pins the 'retired' half of S2's `if changed`." CONFIRMED and fixed. I verified it on product code:
- When dispatchPending's admitLine denies a leased line it retires the line and returns errReplayDenied (drain.go ~1488-1494).
- processOne then returns done=true, changed=dl.leased, dispatched=false (drain.go ~815-820).
- With S2 mutated to `if dispatched {` (M7), the three drain-level rows at cda2bd90 all passed.

The same gap existed at S3, the look-ahead's `if changed` (line 860). The reviewer did not name it, but it is the same class, so I closed it too (M8).

Fix, failing row first. The helper denyNonceAtDispatch admits a nonce's first admission (the read loop's) and denies every later one (dispatchPending's), and it counts the admissions. Two new subtests of TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass use it:
- "retired as it is dispatched" (S2): `other` is leased, then admitted as it is read and denied at dispatch, behind a blocked head, with a fresh capture in a later spool. The pass runs under withPassBudget(ctx, 0). Fixture checks: exactly 2 admissions, not published. The row then asserts that terminalDenied(lease) is true, that the error is errPassBudgetSpent, and that the fresh capture is not published.
- "retired by the look-ahead at dispatch" (S3): p1 waits on p0. p0's live copy is published as the pass reads p0's spooled copy. p1 is admitted as it is read and denied at the look-ahead's dispatch. The row makes the same assertions, plus that p0's live copy was published.

Evidence:
- At HEAD both subtests pass, 10/10.
- M7 (line 1021 `if changed` changed to `if dispatched`) fails only retired_as_it_is_dispatched, with "Should be false: the pass started no line after it".
- M8 (line 860, the same change) fails only retired_by_the_look-ahead_at_dispatch, with the same message.
- Re-run against the extended row:
  - M2 (S2 removed) fails published_as_it_is_read and retired_as_it_is_dispatched.
  - M3 (S3 removed) fails both look-ahead subtests.
  - M4 (S4 removed) fails retired_as_it_is_read.
- Opposite direction, against TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass:
  - M9 (S4 made unconditional) fails a_delivery_a_pass_retired.
  - M10 (S2 made unconditional) fails a_delivery_a_pass_published.
- On the base drain.go (696a2d88):
  - retired_as_it_is_dispatched passes, as expected: the base over-counted.
  - retired_by_the_look-ahead_at_dispatch fails, like the existing look-ahead subtest: the absorbed copy of p0 ended the pass before the look-ahead ran.
- After every mutation, drain.go was restored with git checkout and confirmed as blob 09f0a4dd.

The whole change is drain_pass_progress_test.go +61/-11. The existing "retired as it is read" closure was folded into a shared retiredAt helper with identical assertions, and the row's doc comment now lists five ways in. No assertion was loosened, skipped or widened.

PROOFS AT HEAD a1113fa9
- vet on Windows and GOOS=linux, fmt-check, golangci-lint, nomagic and test/docs: all clean.
- internal/daemon in full twice with -p 1: ok both runs.
- The focused drain, spool watch, idle, order and settle set with -count=10: 1650 top-level PASS, 0 FAIL.
- Every C1.13 row is 10/10, including all five subtests of the extended row.
- The three named budget rows are 10/10.
- 200 TestPreCompactSettle_ passes.

Carried from the implementer's evidence, unchanged since drain.go did not change: the seven C1.13 rows are red on base and green at the fix, and mutations M1 to M6 are each killed.

### Commits

- cda2bd90 fix(daemon): count only progress against a drain pass budget
- a1113fa9 test(daemon): pin a retirement at dispatch as pass progress

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w18-passprogress && GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -timeout 20m -v -run '^TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass$' ./internal/daemon/  (HEAD, drain.go blob 09f0a4dd)` — PASS, all 5 subtests: published_as_it_is_read, retired_as_it_is_read, retired_as_it_is_dispatched, published_by_the_look-ahead, retired_by_the_look-ahead_at_dispatch. ok 2.906s
- `same -run '^TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass$' under M7 (drain.go:1021 `if changed {` -> `if dispatched {`), then restored with git checkout` — FAIL as intended: only retired_as_it_is_dispatched fails, 'Should be false / the pass started no line after it'. Restored blob 09f0a4dd.
- `same pattern under M8 (drain.go:860 `if changed {` -> `if dispatched {`), restored after` — FAIL as intended: only retired_by_the_look-ahead_at_dispatch fails, same message. Restored blob 09f0a4dd.
- `same pattern under M2 (lines 1021-1023 removed), M3 (860-862 removed), M4 (951-953 removed), and with drain.go at the base blob 696a2d88 (git show 7aeb5c6e:internal/daemon/drain.go); each restored after` — M2: published_as_it_is_read and retired_as_it_is_dispatched fail. M3: both look-ahead subtests fail. M4: retired_as_it_is_read fails. Base: both look-ahead subtests fail; the other three pass, as expected, because the base over-counted. Restored blob 09f0a4dd after each run.
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 -timeout 20m -v -run '^TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass$' ./internal/daemon/  under M9 (drain.go:951 `if retiredHere {` -> `if true {`) and M10 (drain.go:1021 `if changed {` -> `if true {`), each restored after` — M9: a_delivery_a_pass_retired fails. M10: a_delivery_a_pass_published fails. Restored blob 09f0a4dd.
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=10 -timeout 20m -v -run '^(TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass|TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass|TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced)$' ./internal/daemon/` — ok 42.862s: 110 PASS, 0 FAIL. Every subtest 10/10.
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD a1113fa9, run 1)` — ok 380.724s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout 30m ./internal/daemon/  (HEAD a1113fa9, run 2)` — ok 351.866s, exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=10 -timeout 60m -v -run '^(TestDrain|TestSpoolWatch_|TestSpoolRetryAfter_|TestIdleDrain_|TestCarriedDefect_|TestDeliveryOrder_|TestDispatchLanes_|TestPreCompactSettle_|TestPrecompactSettleBound_|TestSpoolHeadIndex_|TestSpoolLineHead_|TestUnreplayedDrops_|TestSP08D3_)' ./internal/daemon/  (HEAD a1113fa9)` — ok 1640.991s: 1650 top-level PASS (165 tests x 10), 0 FAIL. 10/10 each: all five subtests of the extended row, the R1/R2/requested/idle rows, TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft, TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached and TestSpoolWatch_APassThatOverranItsBudgetWithoutConsumingKeepsTheBackoff. 200 TestPreCompactSettle_ passes.
- `GOTOOLCHAIN=local go vet ./internal/daemon/  and  GOOS=linux GOTOOLCHAIN=local go vet ./internal/daemon/` — clean, both
- `GOTOOLCHAIN=local go run ./tools/devtool fmt-check` — exit 0
- `GOTOOLCHAIN=local go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/` — exit 0, no findings
- `GOTOOLCHAIN=local go run ./tools/lint/nomagic ./internal/daemon/` — exit 0
- `GOTOOLCHAIN=local CGO_ENABLED=0 go test -p 1 -count=1 ./test/docs/` — ok 5.043s
- `GOTOOLCHAIN=local go run ./tools/devtool check-commit-msg <a1113fa9 message>` — exit 0. Footer 'Refs: V6-VERIFY, C1.13'; no attribution trailers.

### Criterion changes

- No assertion was loosened, skipped or widened. In TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass, the 'retired as it is read' check moved into a shared retiredAt helper with the same terminalDenied assertion. The row's doc comment now describes five ways in.
- New criterion subtests (C1.13, D31) of TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass: 'retired as it is dispatched' pins the retired half of S2's `if changed`, and 'retired by the look-ahead at dispatch' pins the same at S3. A spent pass whose line was retired at dispatch, with changed set and dispatched clear, starts no further line.

### Open issues

- Ledger: C1.13, D31 and the w17c option (a) decision still have to be recorded in qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md, which is outside this worktree. That record covers candidate 8 for this product change and the new row names, including the two subtests added this round. Suggested D31 wording, from the implementer: 'a pass's budget can end it only once it has made progress: advanced a spool's consumed front, or published or retired a line; consuming again a line an earlier pass consumed out of order is not progress (C1.13, candidate 8)'.
- -race was not run, because of machine limits; the coordinator runs it at night. The new subtests run the drain on the test goroutine. Only the admit wrappers run inside it: publishLiveFirst runs synchronously, and denyNonceAtDispatch uses an atomic counter.
- Hosted CI was not run. The drain-level rows use budget 0 and are load-insensitive. The watcher rows use the look's injected clock.
- Behaviour note, by design and carried from the implementer: once a pass has spent its budget without making progress, it now reads a blocked spool to EOF through the lines it consumes again. withPassBudget's doc covers that work as D31 bookkeeping.
- Carried from the implementer: docs/architecture.md (~line 176) states only 'once it is spent a pass starts no new line'. That is still true, so it was left unchanged. delivery_order.go's passLeftWork doc is not my file and was left alone.
- Carried from the w17c fix seat, unchanged: DrainClientSpools passes every client spool, so a backed-off blocked spool is still re-read and re-synced whenever another spool is due.
- Not pinned, judged acceptable: S3 made unconditional (count every done line in reattempt) would only over-count a deferred line that a live worker acknowledged during the pass. Such a line is absorbed in the read loop on the next pass, so the over-count happens once and does not repeat. No starvation follows, and no row was added for it.


## Verify — verdict `sound`, no findings

An independent verify seat re-checked the fix seat's head `a1113fa9` against both review findings and returned `{"verdict": "sound", "findings": []}`.

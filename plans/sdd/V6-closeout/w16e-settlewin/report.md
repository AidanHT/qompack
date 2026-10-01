# W16E-SETTLEWIN: PreCompact settle rows cut by seam, not clock

Branch `closeout/w16e-settlewin`. Workflow `wf_360586e9-18d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `64025cb8`

### Root cause

The three hosted reds are D55's designed degrade, not a product fault. The rows left their subject to a 500 ms wall-clock bound, which a co-loaded hosted disk could exhaust before the subject happened.

(1) Evidence from the code. settleBeforeSeal starts its deadline first (precompact_settle.go:216). Only after that does it run leasedUpTo, then d.deliveryJournal() (lock.openDeliveryJournal), then listClientSpools. heads() reads a file only if ctx has not ended (spool_heads.go:122). The hosted NamesWhatTheBoundLeftUnreplayed drop entry was "0 capture(s) ...; 2 hook client spool file(s) could not be read within the bound". That means neither of the two spools had been read when the deadline passed: the first look began after it.

Evidence from measurement (temporary diagnostic, not committed):
- In that row's fixture nothing leases before the PreCompact, so the journal is still closed. Its first open (creating it on a fresh tree, fsync-bound) cost 140-545 ms here, inside the bound. Hosted fsync is slower still (Q1).
- With the journal pre-opened, the first read began 25-96 ms into the route.
- A single replayed line (lease, publish, ack, progress, release) took about 430-520 ms here under the current co-load, and ReplaysTheSpoolBeforeTheSeal failed once locally from that alone.

What contributed: the cold journal open (once per daemon, fixed work), the listing, scheduling under co-load, and above all the durable replay itself. Lane waits did not contribute: these rows have no leased arrivals, so awaitArrivals returns at once. captureGate.enter does not block.

Reproduced deterministically with a temporary 600 ms sleep (not committed). Placed before the first read, it fails ReplaysTheSpoolBeforeTheSeal, NamesWhatTheBoundLeftUnreplayed and ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers at the same lines as hosted (69, 108, 409). Placed in the listing, it fails six rows, NamesWhatTheBoundLeftUnreplayed with the hosted entry verbatim.

### Summary

W16E-SETTLEWIN: PreCompact settle rows made deterministic on slow, loaded hosts. Only tests changed (internal/daemon/precompact_settle_test.go, precompact_settle_bound_test.go); no product code changed.

Approach
- Subject rows (the subject is not the bound) now use settleTestDaemon(t, liveOrderBound). The bound is derived from the rig's config (B-E = MaxPreCompactWindow + 30 s), so the subject cannot exhaust it.
- Rows that need a cut use a new seam, settleCut. The row cancels the context the PreCompact route was dispatched with, at the moment its subject is in place. sctx derives from that context, and settleBeforeSeal only ever asks sctx.Err() != nil, never why it ended. So the waits, the replay's line in flight and the last look end exactly as they do at the deadline.
- New helper recordSettleLooks / ranUnderTheBound: a read seam that records each spool read's ctx deadline. It asserts each look ran under before+bound <= deadline <= readStart+bound, which is deterministic.

Per row
- TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal: liveOrderBound.
- TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers: liveOrderBound. The older session's dispatch now calls cut() if the settle ever replays it. With the old `<-ctx.Done()` under a 30 s bound, that line would end at its own drainLineDeadline (5 s) and the pass would go on to the session's own spool, hiding the regression. Shown red with the settle temporarily replaying every spool: runs/red-older-replays-every-spool.log.
- TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed (its subject is the bound): liveOrderBound, so both reads always happen. The slow Read's dispatch calls cut() and then waits on its ctx, so exactly that capture is left. Every original assertion is unchanged, plus a new looks-ran-under-the-bound check (2 reads).
- Sweep (item 4), same treatment:
  - TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain (500 ms; cold read of the other spool; red under the injected slow listing): liveOrderBound.
  - TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks (500 ms; cold journal open inside the bound; red under injection): liveOrderBound.
  - TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated (500 ms is its cut; red under injection): liveOrderBound plus settleCut from the held replay.
  - TestPreCompactSettle_NamesALeasedArrivalOnceBesideItsSpoolCopy (2 s wall cut; also left whether the later Read was in the lane before the seal to the clock): liveOrderBound plus cut() right after acceptPrompt(after). It is now certain the later Read is pending when the seal is made, which strengthens the row.
  - TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound (3 s bound, read held until 45 % was left, so the replay had 1.35 s of wall time): coldBacklogBound = drainLineDeadline, the longest bound whose deadline the replay's line still carries. The Dispatch seam records the replay line's ctx.Deadline() and asserts it Equal()s the looks' deadline. That fails for any first-look duration under 5fd55bdd's held-back deadline (shown with that code put back temporarily: runs/red-cold-backlog-heldback-deadline.log). Published, no drops, and 21 reads are all kept.

Rows that pin the bound itself (named in settleCut's comment)
- TestPrecompactSettleBound_IsWhatBELeavesTheSeal: the derivation.
- TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo: the zero bound; a look past the bound reads nothing and says so.
- TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound: the replay runs to the bound's own deadline.
- TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare: the default 500 ms deadline ends a held replay. Its outcome does not depend on timing because every file is index-served.

Rows judged safe (item 4)
- TestPreCompactSettle_AHealthySessionPaysNothing: no spool file, and arrivals are settled, so it returns before any read or wait.
- TestPreCompactSettle_AReplayedPreCompactDoesNotSettle: no settle at all.
- TestPreCompactSettle_WaitsForALeasedArrivalStillPublishing: already liveOrderBound, and the gate opens on an event.
- TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare: everything is pre-indexed, and index hits are taken before the ctx check, so the result holds whenever the 500 ms deadline lands.
- TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists: liveOrderBound against about 4 s of cold reads measured in 16b; passed both hosted iterations.
- TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools: liveOrderBound.
- TestPreCompactSettle_NamesFitTheirShareUnderTheSealsCalibratedEstimator: no settle.
- TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered and TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain: Background ctx.
- TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex: a cancelled ctx.
- TestPrecompactSettleBound_IsWhatBELeavesTheSeal, TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult, TestSpoolLineHead_ReadsWhatDecodeRequestReads: pure functions.
- TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode: liveOrderBound; its timer is a negative control.
- TestSpoolWatch_IndexesTheSpoolsItsPassLeavesForThePreCompactSettle: the settle is served from the index (hit before the ctx check), so nothing is read under the 500 ms bound.

Product check (item 5, report only; no defect fixed)
The code bounds the backlog a settle meets, but on Windows it does not keep that backlog small:
- In spool submode no hot-path hook is served, so the watcher is kicked only by Run's idle tick (at most every 30 s) and by non-hot requests, the PreCompact itself included.
- A spool needs to stand unchanged for one 2 s interval before it gets a pass, and each pass is budgeted at idleRunBudget (2 s). At about 0.43 s per replayed line here, a pass releases about 4 spools, then rests about 2 s.
- The index (spool_heads.go) holds only the spools a pass left. Every client-<pid>.ndjson written since the last look, up to about 30 s of hook activity at one file per hook process, is cold when a settle meets it: 13 ms each per 16b, 14-90 ms each here under load.
- So in a busy session on a slow disk the 500 ms bound usually covers the cold reads plus about 0-1 replayed lines. The rest is named in the drop report (the D55 degrade, nothing lost).
- A further limit: the PreCompact's own noteServed (daemon.go:931) kicks the watcher. If any spool is due, the watcher's budgeted pass over all sessions' spools in host order takes the drain mutex, and the settle's DrainClientSpoolsWithin waits behind it (lockWithin) for up to its whole bound.
- Not fixed, because this waiting is designed and pinned (TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex) and the outcome stays correct (named, never silent). It does, in effect, let other sessions' backlog spend this session's bound. Listed under open_issues with a proposed fix.

Criterion changes
1. Seven rows had their bound raised from the default 500 ms (or 2 s) to liveOrderBound. Only the rig's fixture config changed. The product default stays as it was, and its derivation is pinned by TestPrecompactSettleBound_IsWhatBELeavesTheSeal. The rows' subjects are what the settle replays, reads or names, not its speed.
2. Three rows (NamesWhatTheBoundLeftUnreplayed, ReadsASpoolTheDrainReleasedAndAHookRecreated, NamesALeasedArrivalOnceBesideItsSpoolCopy) cut the settle through settleCut (cancelling the route's context) instead of waiting for the wall-clock deadline. That the deadline itself ends a held replay is pinned by ABacklogIsCountedInFullAndNamedWithinItsShare (default 500 ms) and ALookPastTheBoundReadsNothingAndSaysSo (zero bound). Every original assertion is kept.
3. The cold-backlog row drops its 45 %-hold read seam and the two fixture-sanity assertions that described it (left > 0, left < bound/2). It gains an exact equality of the replay's deadline with the looks' deadline, which detects the 5fd55bdd regression for any first-look duration (strictly stronger). coldBacklogBound changes from 3 s to drainLineDeadline (5 s); this is a test-fixture constant with a derivation comment, not a product number.
4. The older-session row's dispatch for the other session now cuts the settle instead of waiting on its own line context. Without this change, the regression would have been hidden under the larger bound.

Evidence logs: plans/sdd/V6-closeout/w16e-settlewin/runs/.

### Commits

- 282a0fdb test(daemon): cut the precompact settle rows by seam, not clock
- 2f34e0aa test(daemon): read the replay's deadline in the cold-backlog row
- 64025cb8 docs(v6): record the w16e-settlewin evidence logs

### Tests

- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal$' (and the other 7 touched rows by exact name) -v ./internal/daemon, base rows with a temporary 600 ms sleep before each spool read (not committed)` — FAIL as intended: ReplaysTheSpoolBeforeTheSeal :69, NamesWhatTheBoundLeftUnreplayed :108, ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers :409 (the hosted lines), plus the cold-backlog row; runs/red-injected-slow-open-before.log
- `same 8 rows on base with a temporary 600 ms sleep in listClientSpools (not committed)` — FAIL as intended, 6 rows; NamesWhatTheBoundLeftUnreplayed reproduces the hosted drop entry '0 capture(s)...; 2 hook client spool file(s) could not be read within the bound'; runs/red-injected-slow-listing-before.log
- `same 8 rows on the new tests with the same temporary slow listing` — PASS 8/8; runs/green-injected-slow-listing-after.log
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -v ./internal/daemon with 5fd55bdd's held-back deadline put back temporarily` — FAIL as intended: the replay deadline was earlier than the looks' by the first look's duration; runs/red-cold-backlog-heldback-deadline.log
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers$' -v ./internal/daemon with the settle temporarily replaying every spool` — FAIL as intended in 1.2 s; runs/red-older-replays-every-spool.log
- `go test -p 2 -count=10 -run '<the 8 touched rows, each by exact name, as an anchored alternation>' -v ./internal/daemon (final test code)` — PASS 80/80, 36.6 s; runs/touched-count10.log (an earlier -count=10 before the older-row change was also 80/80)
- `go test -c ./internal/daemon, then the binary with -test.count=5 on the 8 touched rows while a two-goroutine spin generator ran (timeout 60, 55 s)` — PASS 40/40; runs/touched-coload.log (an earlier -count=3 run was 24/24); the generator exited on its own
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon (on 2f34e0aa)` — ok 371.4 s; runs/daemon-full.log
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_' ./internal/daemon (intermediate commit 282a0fdb state)` — ok 18.3 s
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon; GOOS=linux go vet ./internal/daemon` — PASS
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers PASS; runpatterns FAIL on a pre-existing line in plans/sdd/V6-closeout/w16c-settle2/report.md:194 (unclosed '^(TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain' from the parser's pipe split), committed on the base, not by this seat

### Criterion changes

- Seven rows (ReplaysTheSpoolBeforeTheSeal, ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers, AnotherSessionsSpoolCostsAHealthySessionNoDrain, CountsOnlyTheSpoolReadsOfItsOwnLooks, NamesWhatTheBoundLeftUnreplayed, ReadsASpoolTheDrainReleasedAndAHookRecreated, NamesALeasedArrivalOnceBesideItsSpoolCopy) run with the rig's B-E raised so the settle bound is liveOrderBound (30 s) instead of the default 500 ms (2 s for the last). Only fixture config changed; the product derivation is pinned by TestPrecompactSettleBound_IsWhatBELeavesTheSeal.
- NamesWhatTheBoundLeftUnreplayed, ReadsASpoolTheDrainReleasedAndAHookRecreated and NamesALeasedArrivalOnceBesideItsSpoolCopy are cut by settleCut (cancelling the route's context at the subject's moment) instead of the wall-clock deadline. Every original assertion is kept, and NamesWhatTheBoundLeftUnreplayed adds a looks-ran-under-the-bound check. The real deadline ending a held replay stays pinned by TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare and TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo.
- TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound: the 45 % hold and its two fixture-sanity assertions (left > 0, left < bound/2) are removed. They are replaced by an exact assertion that the replay line's deadline equals the looks' deadline, which detects 5fd55bdd's held-back deadline for any first-look duration (shown red). coldBacklogBound changes from 3 s to drainLineDeadline.
- TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers: the other session's held dispatch now cuts the settle, so the regression it guards stays red under the 30 s bound (shown red).

### Open issues

- runpatterns lint fails on the base: plans/sdd/V6-closeout/w16c-settle2/report.md:194 needs the usual <!-- runpatterns: ... --> waiver (the coordinator's file; not edited here).
- Product (item 5): in spool submode on Windows the watcher bounds the backlog a settle meets but does not keep it small. Only the idle tick (<=30 s) and non-hot requests kick it, the index holds only the spools a pass left, and every per-pid client spool written since the last look is a cold read inside the 500 ms bound. A slow disk's replayed line (about 430-520 ms here under load) is about the whole bound. Outcome: the D55 degrade (named, nothing lost) is the usual result there, not the exception.
- Product (item 5): the PreCompact's own noteServed kick, or any watcher pass already running, can hold the drain mutex for up to idleRunBudget (2 s) plus a line over all sessions' spools while the settle's DrainClientSpoolsWithin waits behind it, so other sessions' backlog can spend this session's bound. Proposed fix, not implemented: a settle-pending flag the budgeted pass checks at its line boundary (it already stops there for its budget and marks the rest due), or skipping the watcher kick for OpCheckpoint. The gain is limited on disks where one line is about the bound.
- Observation: a daemon whose first journal user is a PreCompact opens (creates) the delivery journal inside the settle bound (leasedUpTo); 140-545 ms here. Once per daemon lifetime; in real use a drain or a live lease usually opens it first.
- This seat ran only on Windows; the touched rows were not run in the Linux container (daytime limits). They are test-only changes vetted with GOOS=linux.

### Needs the owner

- No new product budget or bound. precompactSettleBound (500 ms), drainLineDeadline and idleRunBudget are unchanged. coldBacklogBound (now drainLineDeadline, 5 s; was 3 s) is a test-fixture constant with a derivation comment. If it were too short, the row would fail only when 21 cold reads plus one replayed line exceed 5 s; under the current load they took about 0.7 s.
- Decide whether the watcher/settle drain-mutex contention (open_issues) is fixed for 0.3.0 or recorded as a known limit of the D55 degrade.

## Independent review

### review:settlewin: needs-fixes

- **minor** `internal/daemon/precompact_settle_bound_test.go:259-327 (TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound)` — Criterion change 3 does not leave the row strictly stronger. Two problems. (a) The row no longer creates the scenario it is named for, a first look that spends most of the bound. Without the 45% hold the first look ends almost at once (0.5-0.97 s for the whole row in touched-count10 and touched-coload). The deadline-equality check catches any regression that moves the replay's deadline (5fd55bdd's hold-back, a fixed reserve, a fresh line deadline). It does not catch one that decides from elapsed or remaining time, such as 'skip the replay when less than half the bound is left'. The old hold detected that kind. (b) The published and no-drops assertions still depend on the wall clock. They need a cold delivery-journal create (140-545 ms measured locally), 21 cold reads and one durable replay (430-520 ms locally) to finish inside drainLineDeadline (5 s). This row did not exist in hosted run 36905843834, so it has no hosted evidence. That run's internal/daemon was about 3x slower than local (2367 s for -count=2 against 371 s). Five seconds is about a 2-3x margin over the local cost scaled to hosted speed, not a bound the subject cannot exhaust.
  - Evidence: Diff removes `coldBacklogLeft` and the read seam that held client-10019 until `time.Until(dl) - coldBacklogLeft`, plus the 'first look took more than half of the bound' sanity assertion. New code: `const coldBacklogBound = drainLineDeadline` and `require.True(t, replay.deadline.Equal(look.deadline), ...)` at :319. drain.go:1481 `dctx, cancel := context.WithTimeout(lineCtx, drainLineDeadline)` is why the bound cannot exceed 5 s and keep the equality. grep of ci6/test-win.log finds 0 occurrences of AColdBacklog.
  - Fix: Restore a context-driven hold on the last backlog read that leaves about half of coldBacklogBound (time.Until(dl) - coldBacklogBound/2, giving the replay 2.5 s, nearly twice the old 1.35 s). Keep the new deadline-equality assertion. Then the slow-first-look scenario and the equality are both pinned. Alternatively, keep the change as is and record in the report and needs_owner that the row's published outcome is still bounded by wall time (5 s minus the first look), and why: the equality caps the bound at drainLineDeadline. Also name the elapsed-time-conditioned regression class the row no longer detects.
- **nit** `internal/daemon/precompact_settle_test.go:131-142 (settleCut doc comment)` — The comment says TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare pins that 'the default bound's deadline ends a held replay, whatever the host's speed'. The row's outcome is indeed independent of speed, because every file is served from the index. But on a slow host the 500 ms deadline can pass before DrainClientSpoolsWithin starts: the gate is `if len(own) > 0 && sctx.Err() == nil`, after the 300-file listing and the journal lookups. In that case no replay is held or ended, and the row still passes. The pin that the real deadline ends a line in flight is actually the cold-backlog row's equality: the replay's line carries the bound's deadline. Context semantics then guarantee the cancellation.
  - Evidence: precompact_settle.go:245-257, where the replay runs only while sctx.Err() == nil. The ABacklog row's Dispatch is `<-ctx.Done()` and its assertions are identical whether or not a line was dispatched.
  - Fix: Reword the settleCut comment. ABacklogIsCountedInFull pins that the default deadline's outcome (counted in full, named within its share) holds whenever the deadline lands. AColdBacklog's deadline equality pins that a replay line runs under the bound's own deadline.

## Fix seat (review resolution) — status `done`, head `06f2d0e6`

### Root cause

Hosted windows-latest job 110516048169 ran internal/daemon co-loaded on a slow disk (2367 s for -count=2, against 371 s locally). The three settle rows ran under the default precompactSettleBound of 500 ms, which is B-E minus MaxPreCompactWindow. Since wave 16b every look at the spools counts against that bound. On that host the bound ran out before the first look could read the session's own spool files, or before one durable replay finished. The drop entry hosted logged, "0 capture(s) ... ; 2 hook client spool file(s) could not be read within the bound", is the approved, named degrade (D55). It is not a product fault. The rows had left their subject to the wall clock. Other contributors the implementer measured on this host, all inside the bound: the cold delivery-journal open (140-545 ms), the cold first reads (about 13 ms each under real-time scanning) and one durable replayed line (430-520 ms). Lane waits do not use a clock (awaitArrivals waits on the lane's own settles).

### Summary

## Outcome
Wave 16e settlewin is done. The implementer's three commits stand: they cut the precompact settle rows by seam, not clock, and gave the rows whose subject is not the bound a bound their subject cannot exhaust. The one reviewer finding was confirmed and fixed in 9600b4f0. The evidence logs are in 06f2d0e6.

## What the implementer changed (282a0fdb, 2f34e0aa)
- **Rows whose subject is not the bound** now run under liveOrderBound through settleTestDaemon. These are: replay before the seal, own session first, another session's spool costs a healthy session no drain, counts only its own reads, and the recreated spool.
- **Rows whose subject is the bound** now end the settle through the settleCut seam at the moment their subject is in place:
  - TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed, which also now checks that the looks ran under the bound's own deadline.
  - The leased-arrival-named-once row.
  - The recreated-spool row.
- **The older-session row** now cuts the settle if the settle ever replays the older spool.
- **Red evidence:** with a 600 ms slow listing injected temporarily, the old rows fail the way hosted did, hosted drop entry included, and the new rows pass (runs/red-injected-slow-listing-before.log, runs/green-injected-slow-listing-after.log).
- **Rows that pin the bound itself** keep their wall-clock subject:
  - zero bound: TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo
  - cold backlog: TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound
  - the bound's derivation row, in precompact_settle_test.go

## Review resolution
**Finding (minor): the cold-backlog row no longer makes a slow first look, and its published/no-drops outcome still depends on the wall clock. Verdict: CONFIRMED, fixed in 9600b4f0.**

Verification:
- I put in a temporary product mutation that skips the replay when less than half the bound is left.
- The row at the implementer's head (64025cb8) passed with that mutation in place (runs/review-gap-skip-below-half-old-row.log). That is exactly the gap the reviewer named.

Fix (test-only; no product change):
- The last backlog read (client-10019) is held again, by a context-driven timer, until `coldBacklogLeft` of the bound is left.
- `coldBacklogLeft = drainLineDeadline` (5 s) and `coldBacklogBound = 2 * coldBacklogLeft` (10 s).
- Because the hold leaves no more than drainLineDeadline, the replay's line still carries the settle's own deadline. dispatchPending's per-line `WithTimeout(lineCtx, drainLineDeadline)` starts after the hold, so it ends later than the settle's deadline. The implementer's `replay.deadline.Equal(look.deadline)` check therefore stays exact.
- Because the bound is twice what is left, the first look takes at least half of it.
- The two hold fixture-sanity checks are restored: the first look ended before the bound, and the time left is at most coldBacklogLeft.
- One message changed: the `replay.bounded` check is the row's subject, not fixture sanity. It now reads "the settle replayed the session's own Read, under a deadline".

Why this is stronger than both the reviewer's suggestion and the earlier versions:
- The replay now has 5 s of wall time. That is drainLineDeadline, the most any replayed line can ever be given. The reviewer's suggestion gave it 2.5 s, the pre-16e row 1.35 s, and the implementer's version 5 s minus the cold journal and 21 cold reads.
- The cold journal open and the cold reads now fall inside the held first half.
- Cost: about 5 s more for this row.

Red/green evidence:
- **Skip-below-half mutation:** the new row is red (runs/review-red-skip-below-half-new-row.log).
- **5fd55bdd's held-back deadline put back (sctx deadline minus the first look's duration):** the new row is red on the equality check (runs/review-red-heldback-deadline-new-row.log).
- Both mutations were reverted. precompact_settle.go is unchanged; git status was clean before the commit.

What the row still leaves to the wall clock (recorded for the owner): published and no-drops still need the session's one replayed line to finish inside 5 s, and the cold journal plus 21 cold reads to finish inside the 5 s before the hold. Locally the replay took about 0.2-0.3 s after the hold (row 5.27-5.80 s in all 18 runs). That is roughly 15x margin locally and about 5x if hosted is 3x slower. It cannot be widened further: drainLineDeadline is the product's ceiling for any single line.

## Criterion changes
**CC-R1** (TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound). The 3 s bound with its 45 % hold became a 10 s bound held to 5 s left. This adds the replay-deadline equality the implementer introduced and restores both hold sanity checks.
- Rationale: the row keeps the slow-first-look scenario its name promises and the exact deadline check, and its replay gets about 3.7x the wall time it had before.
- Weakened: nothing. The new row catches a strict superset of the old one's regressions, shown by the two mutations above.

## Tests (Windows, -p 2, machine loaded by other seats)
- The cold-backlog row at -count=10: 10/10 PASS (5.27-5.62 s each).
- The same row at -count=8 under a co-load of 2 busy goroutines (timeout 60 s): 8/8 PASS (5.27-5.80 s). The load generator exited on its own; I confirmed no burn process was left.
- Full internal/daemon once: ok in 322.9 s.
- `go vet ./internal/daemon` passes on Windows and with GOOS=linux; `devtool fmt-check` passes.
- Lint subset: golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck and docmarkers PASS. runpatterns FAILs on the base's committed plans/sdd/V6-closeout/w16c-settle2/report.md:194 (unclosed `^(TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain`). That report came in through 09e9cec0 on base 32004b52, not from this seat.

## Owner decisions
- No product budget or bound changed. precompactSettleBound (500 ms), drainLineDeadline and idleRunBudget are untouched.
- coldBacklogLeft and coldBacklogBound are test-fixture constants with derivation comments (details under needs_owner).

## Files
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16e-settlewin/internal/daemon/precompact_settle_bound_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16e-settlewin/plans/sdd/V6-closeout/w16e-settlewin/runs/ (review-*.log added)

### Commits

- 282a0fdb test(daemon): cut the precompact settle rows by seam, not clock (implementer)
- 2f34e0aa test(daemon): read the replay's deadline in the cold-backlog row (implementer)
- 64025cb8 docs(v6): record the w16e-settlewin evidence logs (implementer)
- 9600b4f0 test(daemon): hold the cold-backlog first look past half its bound (fix seat)
- 06f2d0e6 docs(v6): record the w16e-settlewin review-round evidence logs (fix seat)

### Tests

- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=10 -v` — PASS 10/10 (5.27-5.62 s each), ok 53.6 s; runs/review-coldbacklog-count10.log
- `daemon test binary -test.run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -test.count=8 under a co-load of 2 busy goroutines (timeout 60)` — PASS 8/8 (5.27-5.80 s); generator exited on its own; runs/review-coldbacklog-coload.log
- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=1 with a temporary mutation that skips the replay below half the bound` — new row FAIL, as intended (runs/review-red-skip-below-half-new-row.log); implementer's row PASS, the gap (runs/review-gap-skip-below-half-old-row.log); mutation reverted
- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=1 with 5fd55bdd's held-back deadline temporarily restored` — new row FAIL on the deadline equality, as intended (runs/review-red-heldback-deadline-new-row.log); mutation reverted
- `go test -p 2 -timeout=30m -count=1 ./internal/daemon` — ok 322.939 s; runs/review-daemon-full.log
- `go vet ./internal/daemon && GOOS=linux go vet ./internal/daemon; go run ./tools/devtool fmt-check` — PASS
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — All PASS except runpatterns, which FAILs on the base's committed w16c-settle2/report.md:194 (unclosed alternation); not from this seat

### Criterion changes

- CC-R1: TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound moves from a 3 s bound with its last backlog read held until 45 % was left (implementer: 5 s bound with no hold) to a 10 s bound (2*drainLineDeadline) held until drainLineDeadline is left. It keeps the replay-deadline equality check and restores both hold fixture-sanity checks; the replay.bounded message now names it as the subject, not fixture sanity. Rationale: the row again makes a slow first look (at least half the bound), so a regression that decides from the time left is caught. The deadline check stays exact, and the replay has 5 s of wall time instead of 1.35 s. Nothing is weakened: the temporary skip-below-half and held-back-deadline mutations both turn the row red.

### Open issues

- From the implementer, carried forward: the client-spool watcher and the PreCompact settle contend for the drain mutex. A settle that meets a watcher pass in progress can spend its bound in lockWithin, which degrades as D55 allows (named, nothing lost) but can leave a replay undone that would otherwise fit.
- runpatterns lint fails on the base: plans/sdd/V6-closeout/w16c-settle2/report.md:194 quotes the unclosed pattern ^(TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain. It came in through 09e9cec0, before this seat; the coordinator owns that report.
- The cold-backlog row's published/no-drops outcome is still bounded by wall time. The one replayed line must finish within drainLineDeadline (5 s, about 15x local headroom), and the cold journal plus 21 cold reads within the 5 s before the hold. The product caps any single line at drainLineDeadline, so the margin cannot be widened. The row has no hosted evidence yet: it did not exist in run 36905843834.

### Needs the owner

- Test-fixture constants, not product numbers. coldBacklogLeft = drainLineDeadline (5 s): the most any replayed line can be given, chosen so the replay's line carries the settle's own deadline. coldBacklogBound = 2 * coldBacklogLeft (10 s): makes the first look take at least half the bound. If they are wrong, the row fails only if one replayed line takes longer than 5 s, or the cold journal plus 21 cold reads take longer than 5 s; the hold fixture-sanity check says which. Locally both together took about 0.3 s. No product budget or bound changed: precompactSettleBound (500 ms), drainLineDeadline and idleRunBudget are untouched.
- Decide whether the watcher/settle drain-mutex contention (open_issues) is fixed for 0.3.0 or recorded as a known limit of the D55 degrade.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



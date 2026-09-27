# w6-gcserial: one GC pass per store

Branch `closeout/w6-gcserial`. Workflow `wf_8e58c74e-b50`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `ffa6ae7120648e2d21e3cb6e4b9b2e07d8da0c35`

### Root cause

Nothing ordered GC passes on a store. Each session end runs a pass (observer onSessionEnd, step 6), and since C1.15 session ends run concurrently on their own goroutines. The idle scheduler (daemon gcTask) runs a pass too. When two passes overlapped, both resumed the same state/gc.json cursor. Each added the cursor's Deleted/Freed counts to its own report, and each subtracted its freed bytes from bytesOnDisk, which state/store.json persists and the quota reads. The store's size was therefore under-reported for good. In runs/04, Stats.Bytes was 0 with 3,506 bytes of live objects on disk. In runs/05, on base f6095e2, it was 3 of 3: 0 against 1,761 bytes on disk, with the reports summing 561 deletions for 336 objects actually removed. The overlapping passes also collided on gc-live.bin, the tombstone phase, clearGCState deleting a cursor the other pass had just saved, and the CompactRetentionRoots rewrite of retention-roots.jsonl. Delivery-segment retention is read-only in GC (gc_delivery_segments.go only decodes), so it was never a writer race.

### Summary

## GC passes are not serialized (w6-gcserial)

This is the report the coordinator commits. The harness refuses report.md from subagents.

### Result
Every GC pass now goes through one gate per store, so at most one pass runs on a store at a time. The fix is `internal/store/gcgate.go`, with `FSStore.GC` calling `serializeGC(ctx, p, s.gcPass)`. The pass body moved unchanged into `gcPass`, which re-checks the closed-store guard and `ctx`.

The code was written by the two earlier seats (commits 3c17dbb..4b33c19). I reviewed it as an unreviewed draft and kept all of it. I found no defect needing a new commit. Every run the verdict relies on was redone at 4b33c19. The only commit after that, ffa6ae7, adds evidence under `plans/sdd/V6-closeout/w6-gcserial/runs/` and nothing else: `git diff 4b33c19 ffa6ae7` outside that folder is empty.

### Root cause (evidence)
- **Two passes overlap.** runs/01 (store, f6095e2 plus the new test, no fix): `TestGC_PassesStartedTogetherRunOneAtATime` shows two passes inside the after-harvest hook at once (most=2). runs/02 (daemon, same base): `TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass` shows a session end's pass running inside the idle scheduler's pass (most=2).
- **The store's size is lost.** runs/03 and runs/04 come from a temporary diagnostic. Its Go source is kept only as `runs/diagtemp_overlap_test.go.txt` and was never committed as a test. It shows both passes resuming one cursor: Stats.Bytes 0 against 3,506 bytes on disk, and 1,573 deletions reported for 1,100 objects removed. runs/05 runs the committed `TestGC_OverlappingCallsCountAResumedCursorOnce` on a git-archive of f6095e2 and fails 3 of 3 (Stats.Bytes 0 against 1,761 on disk). runs/06 and runs/44 pass it.

### What a second requester does (the decision, documented in gcgate.go, GC's doc and docs/architecture.md)
- **It never joins the running pass.** That pass harvested its retention sources before the request existed, so answering with its result could lose space the request's caller just freed. `TestGC_ARequestMadeDuringAPassIsAnsweredByOneThatStartsAfterIt` pins this: a checkpoint removed mid-pass is collected by the follow-up pass.
- **It waits, and is answered by exactly one follow-up pass that starts after it.** Every request that waited behind the same pass is answered by that one follow-up. The requests must be of the same kind (resolved retention, dry run, quota, outcome bound); only Deadline may differ.
- **Who starts the follow-up.** It is started by the waiter of the oldest waiter's kind that grants the longest Deadline, on that waiter's own goroutine and ctx.
- **When a follow-up answers nobody else.** A pass cut short by its starter's cancellation, by a shorter Deadline than the waiter's, or by a panic does not answer the other waiters. They get a pass of their own.
- **Only the caller's own ctx drops a request.** The wait answers to ctx, so Stop is never held behind a queue.
- **Deadline counts from the pass start**, not from when the request was made.
- **Bound on session-end bursts.** In the product, both callers ask for the same kind of pass. A burst of session ends costs at most two passes' time unless Stop cancels them. The session end's gcDeadline is 8s and the idle budget is 2s, so a session end starts the follow-up.
- **Counters:** `store.gc.queued` and `store.gc.shared`.

### Cross-process exclusion: not needed, and pinned by a guard
- No GC entry point exists outside the daemon. `qompack fsck --repair` never runs GC (performFsckRepairs). It takes `daemon.AcquireLock` first and opens the store only to quarantine.
- backup and restore take the writer lease (`acquireWriterLease`, which is `daemon.AcquireLock`) before opening a store.
- The daemon opens one writable store per project (WireObserver).
- So the gate on the store handle covers every pass, and no file lock was added that could deadlock with the writer lease.
- New guard `TestGuard_StoreGCRunsOnlyInsideTheDaemon` keeps an exact inventory of product GC callers: onSessionEnd, gcTask, and storetest.RunStoreSuite. A negative control with a GC call planted in internal/cli fails it (runs/10).
- The shared reads (loadGCState, CompactRetentionRoots, pendingMarkerRoot) stay `paths.ReadFileShared`, so the Windows fixes are kept. Only their sharedReaders reason text changed.

### Review findings on the draft (none needed a change)
- **Gate logic checked:**
  - floor/seq decide which pass may answer a waiter.
  - A waiter elected on an ended ctx still runs its pass and releases the gate.
  - A panic hands the gate on.
  - Withdrawal happens under the mutex, and fate is written before the channel closes.
  - `ErrDegraded` from a closed store is served to all waiters.
- **Idle Deadline is computed at request time.** When the idle request waits, its Deadline includes the wait, so the pass ends on its ctx deadline (DeadlineExceeded, which RunOnce logs at Warn) instead of Truncated. The cursor is still saved on the sweep's error path. This matches base: before the gate, the ctx deadline already expired microseconds before the pass Deadline, and `gcBudget.spent` checks ctx first. So this is not a regression.
- **Deadline and resume semantics are unchanged.** All five `TestGC_Deadline*` rows pass (runs/44, runs/50, Linux store).

### Changes (f6095e2..ffa6ae7)
- **internal/store:**
  - New `gcgate.go`.
  - `gcrun.go`: GC now calls the gate, and the body moved into gcPass.
  - `fsstore.go`: new `gcq` field.
  - `gc.go`: Deadline doc.
  - `lifecycle.go` and `gc_testhook.go`: doc updates.
  - New `gc_serial_test.go`: 3 real-store tests and 8 scripted-gate tests.
- **internal/daemon:** new `session_end_gc_test.go`; `scheduler_idle.go` doc.
- **internal/observer:** `session.go` doc.
- **test/guards:** new `gccallers_test.go`; `sharedreaders_test.go` reason text.
- **docs/architecture.md:** new "One GC pass at a time" paragraph.

### Commands and results (all at 4b33c19 unless marked; Windows runs were co-loaded)
- `go run ./tools/devtool fmt-check`: pass. It was also re-run at ffa6ae7 and passes.
- `go vet ./internal/store/... ./internal/daemon ./internal/observer ./test/guards` on Windows and with GOOS=linux: both exit 0 (runs/40).
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all 8 PASS (runs/41). docmarkers and runpatterns were re-run at ffa6ae7 and pass.
- `go test ./test/docs -count=1`, `go run ./tools/devtool gen-config-docs --check` and `go run ./tools/devtool gen-command-docs --check`: all pass (runs/42).
- Store gate rows, 10 runs each, 100 of 100 passed (runs/43):
  `go test ./internal/store -run '^(TestGC_PassesStartedTogetherRunOneAtATime|TestGC_ARequestMadeDuringAPassIsAnsweredByOneThatStartsAfterIt|TestGCGate_AnUncontendedCallRunsItsOwnPassOnItsOwnContext|TestGCGate_RequestsWaitingBehindAPassShareOneFollowUp|TestGCGate_AWaitingRequestWithdrawsWhenItsContextEnds|TestGCGate_AFollowUpItsStarterAbandonedDoesNotAnswerTheOthers|TestGCGate_TheLongestDeadlineOfAKindStartsTheFollowUp|TestGCGate_ADifferentKindOfRequestWaitsForItsOwnPass|TestGCGate_APanickingPassHandsTheGateOn|TestGCGate_ServesOnlyWhatThePassRanToItsOwnConclusionFor)$' -count=10 -v` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Deadline, recount and read-only rows, run twice, all pass (runs/44):
  `go test ./internal/store -run '^(TestGC_OverlappingCallsCountAResumedCursorOnce|TestGC_DeadlineTruncatesAndResumes|TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass|TestGC_DeadlineWindowNeverClaimsMoreRoomThanTheCheckSchedule|TestGC_DeadlineBudgetRepricesAfterAMissedWindow|TestGC_DeadlineOvershootIsBoundedByTheCheckInterval|TestReadOnly_EveryExportedMethodIsClassified)$' -count=2 -v` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Daemon rows, 5 runs each, all pass (runs/45):
  `go test ./internal/daemon -run '^(TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass|TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot)$' -count=5 -v` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Guard rows, all pass (runs/45):
  `go test ./test/guards -run '^(TestGuard_StoreGCRunsOnlyInsideTheDaemon|TestGuard_GCCallScannerSeesEveryShape|TestGuard_HotFilesAreReadWithDeleteSharing|TestGuard_SharedReaderScannerSeesAForbiddenCall)$' -count=1 -v` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- **Windows full packages, one at a time**, with `go test <pkg> -count=1 -v` and `-timeout=30m` (40m for fault and integration):

  | Package | Result | Log |
  |---|---|---|
  | internal/store | ok, 863 pass | runs/50 |
  | internal/daemon | ok, 1510 pass | runs/51 |
  | internal/observer | ok, 561 pass | runs/52 |
  | test/guards | only TestCarriedDefects_WaveReportRequiresResolution fails (6 subtests), identically at base f6095e2 (runs/56); pre-existing C6.3 | runs/53 |
  | test/fault | ok, 56 pass | runs/54 |
  | test/integration | 69 pass; only TestIntegration_HotPathWarmWithRealResidentState fails | runs/55 |

- **HotPathWarm, pre-existing.** On Windows, `go test ./test/integration -run '^TestIntegration_HotPathWarmWithRealResidentState$' -count=1 -v` fails alone at HEAD (runs/57) and on base f6095e2 (runs/58). Both show the same signature: 528 of 2064 samples never reached the daemon, so the B-A/B-B gate is failed on that ground.
- **Linux, non-root, -race, GOMAXPROCS 4.** Each group was a separate `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-gcserial --repo <worktree> --out <runs/linux> 4b33c19 full-<group> --timeout 60m -- <pkgs>` run, one at a time. No race logs, and the source tree was unchanged after each run.

  | Group | Result |
  |---|---|
  | store | PASS, 864 (4 Windows-only skips) |
  | daemon | PASS, 1517 |
  | fault | 54 pass; TestFault_PublicationBoundaries/object_written_index_line_absent failed (see open items) |
  | integration | 69 pass; only HotPathWarm fails |
  | observer | PASS, 561 |
  | guards | only CarriedDefects fails |

- **Linux alone re-runs.**
  - Gate `--run '^TestFault_PublicationBoundaries$/^object_written_index_line_absent$' --count 3 -- ./test/fault` at 4b33c19: PASS 3 of 3 (explicit_incomplete, gap reported).
  - Gate `--run '^TestIntegration_HotPathWarmWithRealResidentState$' -- ./test/integration` at 4b33c19 and at f6095e2: FAIL on both, with the identical 526-of-2064 undelivered signature. Pre-existing.
- The earlier seat's Linux run at 7397da9 ran all six packages in parallel (-p 4). It showed five daemon timing failures and the two V6Backup fault rows. All of them pass in the sequential runs at 4b33c19, so they were co-load.

### Criterion changes
None. No check was weakened, skipped, or loosened, and no golden was regenerated. New criteria were added: 11 store tests, 1 daemon test, and 2 guard tests. The sharedReaders rows keep the same functions and files; only their "why" text changed. The go test binary timeout was set to 40m for Windows fault and integration. That is not an assertion: a hang still panics with a dump. The earlier seat's integration run hit the 30m timeout under co-load (the log was deleted as superseded), and other workstreams use 40m.

### Housekeeping
- The earlier seat's superseded or partial runs (21–28, 30, and the 7397da9 Linux directory) were deleted.
- runs/20 is kept because b9c2676 cites it. runs/29 is kept as base evidence for the Windows AppendOnly co-load failure; AppendOnly passes in runs/55.
- None of my processes are left running, on Windows or in the container.

### Coordination
The gate does not touch saveGCState or clearGCState. w6-ckptsync's gc.json barrier should merge cleanly. If that branch edits the old GC() body, re-apply its edit inside `gcPass`, where the body moved verbatim.

### Commits

- 3c17dbb fix(store): run one gc pass per store at a time
- 769108e test(daemon): queue session-end gc behind the idle pass
- 19e990f test(guards): pin store gc to the daemon's two callers
- 7397da9 docs: describe one gc pass per store at a time
- 7be8117 docs(store): qualify the gate's two-pass bound and its hazards
- b9c2676 fix(store): keep the pass's re-check to the closed-store guard
- 2ac9c6e test(store): count a resumed gc cursor once across two calls
- 4b33c19 docs(store): name every user of the after-harvest test hook
- ffa6ae7 docs(sdd): add the w6-gcserial run evidence

### Tests

- `go test ./internal/store -count=1 -timeout=30m -v (Windows, 4b33c19, runs/50)` — ok, 863 pass, 1 skip
- `go test ./internal/daemon -count=1 -timeout=30m -v (Windows, runs/51)` — ok, 1510 pass
- `go test ./internal/observer -count=1 -timeout=30m -v (Windows, runs/52)` — ok, 561 pass
- `go test ./test/guards -count=1 -timeout=30m -v (Windows, runs/53)` — FAIL only TestCarriedDefects_WaveReportRequiresResolution (6 subtests); identical at base f6095e2 (runs/56), pre-existing C6.3
- `go test ./test/fault -count=1 -timeout=40m -v (Windows, runs/54)` — ok, 56 pass
- `go test ./test/integration -count=1 -timeout=40m -v (Windows, runs/55)` — 69 pass, 1 fail: TestIntegration_HotPathWarmWithRealResidentState (pre-existing: fails alone at HEAD runs/57 and base f6095e2 runs/58, same 528/2064 undelivered signature)
- `go test ./internal/store -run '^(TestGC_PassesStartedTogetherRunOneAtATime|TestGC_ARequestMadeDuringAPassIsAnsweredByOneThatStartsAfterIt|TestGCGate_AnUncontendedCallRunsItsOwnPassOnItsOwnContext|TestGCGate_RequestsWaitingBehindAPassShareOneFollowUp|TestGCGate_AWaitingRequestWithdrawsWhenItsContextEnds|TestGCGate_AFollowUpItsStarterAbandonedDoesNotAnswerTheOthers|TestGCGate_TheLongestDeadlineOfAKindStartsTheFollowUp|TestGCGate_ADifferentKindOfRequestWaitsForItsOwnPass|TestGCGate_APanickingPassHandsTheGateOn|TestGCGate_ServesOnlyWhatThePassRanToItsOwnConclusionFor)$' -count=10 -v (runs/43)` — 100/100 PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/store -run '^(TestGC_OverlappingCallsCountAResumedCursorOnce|TestGC_DeadlineTruncatesAndResumes|TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass|TestGC_DeadlineWindowNeverClaimsMoreRoomThanTheCheckSchedule|TestGC_DeadlineBudgetRepricesAfterAMissedWindow|TestGC_DeadlineOvershootIsBoundedByTheCheckInterval|TestReadOnly_EveryExportedMethodIsClassified)$' -count=2 -v (runs/44)` — all PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon -run '^(TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass|TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot)$' -count=5 -v (runs/45)` — 10/10 PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/guards -run '^(TestGuard_StoreGCRunsOnlyInsideTheDaemon|TestGuard_GCCallScannerSeesEveryShape|TestGuard_HotFilesAreReadWithDeleteSharing|TestGuard_SharedReaderScannerSeesAForbiddenCall)$' -count=1 -v (runs/45)` — PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/store -run '^TestGC_OverlappingCallsCountAResumedCursorOnce$' -count=3 -v on git archive f6095e2 plus the test (runs/05, earlier seat)` — FAIL 3/3 before the fix (Stats.Bytes 0 vs 1761 on disk)
- `go test ./internal/store -run '^TestGC_PassesStartedTogetherRunOneAtATime$' / go test ./internal/daemon -run '^TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass$' at f6095e2 plus tests (runs/01, runs/02, earlier seat)` — FAIL before the fix (two passes overlapped)
- `go run ./tools/devtool fmt-check; go vet on store/daemon/observer/guards (Windows and GOOS=linux) (runs/40)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (runs/41)` — 8/8 PASS
- `go test ./test/docs -count=1; go run ./tools/devtool gen-config-docs --check; go run ./tools/devtool gen-command-docs --check (runs/42)` — all pass
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-gcserial --repo <wt> --out <runs/linux> 4b33c19 full-<group> --timeout 60m -- <pkgs> (store, daemon, fault, integration, observer+guards, one at a time; non-root, -race)` — store PASS 864; daemon PASS 1517; observer PASS 561; fault 54/56 (object_written_index_line_absent, co-load, passes 3/3 alone); integration 69/70 (HotPathWarm, pre-existing); guards only CarriedDefects (C6.3); no race logs
- `linux-nonroot-gate.sh ... 4b33c19 alone-fault-unindexed-row --run '^TestFault_PublicationBoundaries$/^object_written_index_line_absent$' --count 3 -- ./test/fault` — PASS 3/3
- `linux-nonroot-gate.sh ... {4b33c19,f6095e2} alone-hotpathwarm[-base] --run '^TestIntegration_HotPathWarmWithRealResidentState$' -- ./test/integration` — FAIL at both HEAD and base with identical 526/2064 undelivered signature (pre-existing)

### Criterion changes

- None weakened. Added: 11 store tests (gc_serial_test.go), TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass, TestGuard_StoreGCRunsOnlyInsideTheDaemon and TestGuard_GCCallScannerSeesEveryShape.
- test/guards sharedReaders: the 'why' text of the loadGCState, CompactRetentionRoots and pendingMarkerRoot rows changed. The function, the held file and the requirement to read shared did not. Rationale: they claimed GC passes are unserialized, which is no longer true.
- Windows go test binary -timeout set to 40m for test/fault and test/integration (30m elsewhere). This is not an assertion: a hang still panics with a dump. The 30m timeout had fired under co-load, and other workstreams use 40m.

### Open issues

- Pre-existing C6.3: TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3) fails on Windows and Linux, identically at base f6095e2 (runs/56). Its inputs are unchanged since base.
- Pre-existing: TestIntegration_HotPathWarmWithRealResidentState fails alone at HEAD and at base f6095e2 on both OSes with the same signature (Linux 526/2064, Windows 528/2064 samples never reached the daemon). The gate is uncontended on this path; no session ends run during the bench.
- Co-load, routed to internal/daemon (hypothesis, not verified): TestFault_PublicationBoundaries/object_written_index_line_absent failed once in the loaded Linux fault run. Its recovery daemon never answered within 1m (lock pid 0), and it passes 3/3 alone and in the Windows full run. Daemon.Run runs the startup Drain, which ends replayed flushes inline and so runs GC, before LoudPublicationGaps (bounded by publicationStartupBound 250ms). When the recovery session arrives by spool, that GC can sweep the unindexed object before the accounting counts it, or the 250ms bound can cut the accounting short. Either way nothing names the gap. This is independent of GC serialization: at startup there is exactly one GC caller (the idle scheduler and async session ends start only after the serve loop), so the gate is uncontended.
- Minor: when an idle tick's GC request waits behind a session end's 8s pass, its 2s budget usually ends first. It withdraws with DeadlineExceeded, which RunOnce logs at Warn. This is correct, since the follow-up pass or the next tick covers it, but it may add Warn noise.
- Optional follow-up: CompactRetentionRoots is exported and ordered by the gate only when called from inside GC. Only gcrun calls it today, and the guard inventories GC callers, not direct CompactRetentionRoots callers.
- Coordination: w6-ckptsync may add a barrier to gc.json writes. Its edits belong in gcPass, where GC's body moved verbatim.

### Needs the owner

- gcSerialBound = 30s (internal/store/gc_serial_test.go, test-only). It is a failure bound for event waits in the GC serialization tests: each wait ends on an event and reaches the bound only if that event never comes. It mirrors the daemon tests' liveOrderBound of 30s ('generous for a co-loaded host'). If too small: spurious failures on a loaded -race host. If too large: only a slower report of a hang. No product effect.
- gcSerialTick = 1ms (same file, test-only): the require.Eventually polling interval over atomic loads. If wrong: only CPU cost while polling.
- No product budget or bound numbers were added. The one design decision, for the owner to confirm: a GC request that finds a pass running never joins it, but queues for one follow-up pass started after it. That follow-up answers every same-kind waiter; it is started by the longest-Deadline waiter; only the caller's ctx withdraws a request; and Deadline counts from the pass start. No cross-process lock was added, because no GC entry point exists outside the daemon and a guard keeps it that way.

## Independent review

### review:gcserial: needs-fixes

- **minor** `git log f6095e2..ffa6ae7: commits 769108e, 19e990f, 7397da9, 7be8117, 2ac9c6e, 4b33c19, ffa6ae7` — Seven of the nine commits have no `Refs:` footer. Only 3c17dbb and b9c2676 carry `Refs: V6-VERIFY, C1.10/C3.2`. 7397da9 and 7be8117 also have no body at all. The review brief makes a Refs footer part of commit hygiene, and on the base the matching test(guards)/docs close-out commits carry one (for example 248b481 and 90ab6ad).
  - Evidence: `git log --format='%h %s | %(trailers:only)' f6095e2..HEAD` shows no trailers on those seven commits. For example, 19e990f 'test(guards): pin store gc to the daemon's two callers' ends its body with no footer, and 7397da9 'docs: describe one gc pass per store at a time' has an empty body. No attribution trailers are present anywhere, which is correct.
  - Fix: At integration, reword the seven commits to end with `Refs: V6-VERIFY, C1.10/C3.2`. This can be a non-interactive `git rebase --exec`/`git filter-branch --msg-filter` on the branch, or the coordinator can add it when squashing or re-stacking. No code change is needed.
- **nit** `internal/observer/session.go:218-222` — The step-6 comment was edited in this change but still says GC is bounded "inside the SessionEnd hook's own 20s timeout". Since C1.15 the end runs on its own goroutine after the flush is answered, and the host's hook budget is a shared 1.5 s (internal/daemon/session_end.go header). With the gate, the call can now also wait a whole running pass (up to gcDeadline 8s plus the tombstone overshoot) before its own 8s pass starts. The retained clause therefore describes a bound that no longer applies.
  - Evidence: session.go:218 `bounded by gcDeadline inside the SessionEnd hook's own 20s timeout. The store runs one pass at a time ...`; session_end.go:13-16 'Claude Code gives a plugin's SessionEnd hooks ONE SHARED budget of 1.5 s ... The flush is now answered as soon as it is DURABLE'.
  - Fix: Drop the 20s-hook clause, or replace it with: 'bounded by gcDeadline once its pass starts; the end runs on its own goroutine (C1.15) under the daemon's session-end lifetime, which Stop cancels'.
- **nit** `test/guards/gccallers_test.go:34-52 (gcCallers) / internal/store/lifecycle.go:318` — The gate orders CompactRetentionRoots only when GC calls it, but the new guard inventories only `.GC` selectors. The function is exported and takes a project root. A future direct caller, such as a maintenance or CLI compaction, would race a daemon pass's compaction of retention-roots.jsonl without failing any guard. The implementer lists this as an optional follow-up, and today the only product caller is gcrun.go:948 (FSStore.compactRetentionRoots).
  - Evidence: `grep -rn CompactRetentionRoots --include=*.go internal cmd tools | grep -v _test.go` finds only lifecycle.go (the definition) and gcrun.go:948. gcCallsInFile matches `sel.Sel.Name != "GC"` only.
  - Fix: In gcCallsInFile, also match selectors or idents named CompactRetentionRoots (outside _test.go). Add an inventory row {file: internal/store/gcrun.go, fn: compactRetentionRoots, why: 'called only from inside gcPass, under the gate'}. Alternatively, unexport the function if no other package needs it.

## Fix seat (review resolution) — status `done`, head `da9448f11725c0d8744e20ccc9510f3bf769898d`

### Root cause

GC passes on a store were not serialized. Every session end runs a pass, and the ends have run concurrently since C1.15. The idle scheduler's gcTask runs one too. When two passes overlapped, both resumed the same gc.json cursor. Each added that cursor's counts to its own report and subtracted its freed bytes from bytesOnDisk again. store.json persists that figure and the quota reads it, so the store's size was under-reported for good. Evidence: runs/04 shows Stats.Bytes 0 with 3,506 bytes of live objects still on disk. runs/05 (3 of 3 on the base) shows two calls reporting 561 deletions for 336 objects removed, and Stats.Bytes 0 with 1,761 bytes on disk. The overlapping passes also collided on gc-live.bin, on the tombstones, on clearGCState deleting a cursor another pass had just saved, and on the retention-roots compaction. The fix is internal/store/gcgate.go. It lets at most one pass run per store handle. A call that finds a pass running waits, answering to its ctx. It is answered only by a pass that starts after it arrived, never by the one already running, because that pass harvested before the request existed. All requests that waited behind one pass share a single follow-up pass. GC has no entry point outside the daemon (fsck --repair never runs GC), and a new guard, TestGuard_StoreGCRunsOnlyInsideTheDaemon, keeps it that way, so no cross-process lock is needed.

### Summary

The fix seat for w6-gcserial is done. The reviewer raised one minor finding. I checked it independently and rebutted it with evidence. No product code changed in this seat. My one commit (da9448f) adds only evidence logs: runs/60, 61 and 62. The worktree is clean at da9448f on closeout/w6-gcserial, and I started no background processes.

## Review resolution

**Finding (minor): seven of the nine commits have no `Refs:` footer, and 7397da9 and 7be8117 have no body.**
- **Action: rebutted.** The facts are right, but the finding is not a defect, and the fix it proposes would do harm.
- **The rule covers feat and fix only.** The brief asks for the Refs footer on feat/fix commits. The repo's own checker, tools/devtool/checkcommitmsg.go (which the commit-msg hook runs), says the same: `refsRE ... required on feat and fix commits`, and it enforces this only when the type is feat or fix.
  - Both fix commits carry the footer: 3c17dbb and b9c2676.
  - The seven without it are test(daemon), test(guards), docs, docs(store) twice, test(store) and docs(sdd).
  - I ran the checker, built from this tree, on all nine messages. All nine pass (runs/62-fixseat-commit-msg-check-windows.log).
  - Nothing requires a commit body.
- **The base is not uniform either.** Merged base commits with no Refs include 2f9567a test(guards), 7abddc7, c48b82a and 6dbf63f (docs), and 421951d, dc58fad, da3c602, 1b79f80 and 6e39b89 (docs(closeout)/docs(v6)). So 248b481 and 90ab6ad reflect a common habit, not a rule.
- **Rewording would break the evidence.** Adding the footers means rewriting 769108e..ffa6ae7, which changes the SHA of 4b33c19. That is the exact commit every final run names:
  - the Windows logs cite `4b33c19` 133 times and the full SHA 14 times;
  - the Linux non-root -race gate ran exact commit 4b33c19f8853f3f0d5821f8f1f8dc6b9b8df5c54, and every gate artifacts directory records it in identity.txt;
  - ffa6ae7's own message cites 4b33c19.
  
  A rewrite would leave all of that pointing at an unreachable commit (the memory note on report-SHA reachability). Integrating by merge, as the coordinator's `chore(v6): integrate closeout/...` commits do, keeps every quoted SHA reachable. My recommendation is to merge as is. If the coordinator still wants the footers, re-anchoring the evidence has to be part of that rewrite.

## What I verified (Windows, co-loaded, at ffa6ae7)
ffa6ae7's product tree is identical to 4b33c19's: `git diff 4b33c19 ffa6ae7 --quiet -- internal cmd test tools docs` exits 0.
- **Gate and store rows.** I re-ran the 11 gate/serialization rows, the recount row, the 5 TestGC_Deadline* rows and TestReadOnly_EveryExportedMethodIsClassified with -count=3: 51 of 51 PASS, exit 0, in 493 s under load. The Deadline and resume semantics are unchanged. Log: runs/60.
- **Daemon and guards rows.** TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass and TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot passed with -count=3: 6 of 6, exit 0. The four guard rows passed once each: TestGuard_StoreGCRunsOnlyInsideTheDaemon, TestGuard_GCCallScannerSeesEveryShape, TestGuard_HotFilesAreReadWithDeleteSharing and TestGuard_SharedReaderScannerSeesAForbiddenCall. Log: runs/61.
- **Lint and docs.** `lint --only=runpatterns,docmarkers` passes, and `go test ./test/docs` is ok.
- **Code read.** I read gcgate.go independently and found no defect:
  - every waiter's channel is closed exactly once, under gcGate.mu (served, withdrawn or elected);
  - a request withdrawn on its own ctx is removed from the queue under the lock before anyone else can close its channel;
  - `floor >= seq` stops a waiter from being answered by the pass that was already running when it arrived;
  - a panicking or cancelled pass hands the gate on through the deferred finishGCPass and answers nobody else;
  - nothing waits on in-flight counts in `use()`/`mutate()`, so Close cannot deadlock against the queue.
- **Linux.** Not re-run: no product change, and the implementer's Linux gate runs cover exactly this tree at 4b33c19.

## Criterion changes
None. No check was weakened, skipped or loosened.

## Carried forward from the implementer (pre-existing, each re-run alone)
- TestCarriedDefects_WaveReportRequiresResolution fails on its SP06-D2 subtest, identically at base f6095e2 (runs/56; C6.3).
- TestIntegration_HotPathWarmWithRealResidentState fails when run alone, both at 4b33c19 and at the base, with the same undelivered-sample count on both hosts (runs/57, 58).
- TestFault_PublicationBoundaries/object_written_index_line_absent failed once in the loaded Linux full run, when its recovery daemon never answered. It passed 3 of 3 alone.

### Commits

- 3c17dbb fix(store): run one gc pass per store at a time (implementer)
- 769108e test(daemon): queue session-end gc behind the idle pass (implementer)
- 19e990f test(guards): pin store gc to the daemon's two callers (implementer)
- 7397da9 docs: describe one gc pass per store at a time (implementer)
- 7be8117 docs(store): qualify the gate's two-pass bound and its hazards (implementer)
- b9c2676 fix(store): keep the pass's re-check to the closed-store guard (implementer)
- 2ac9c6e test(store): count a resumed gc cursor once across two calls (implementer)
- 4b33c19 docs(store): name every user of the after-harvest test hook (implementer; the exact commit every final Windows and Linux run names)
- ffa6ae7 docs(sdd): add the w6-gcserial run evidence (implementer)
- da9448f docs(sdd): add the w6-gcserial review-fix runs (fix seat: runs/60, 61, 62 only, no product change)

### Tests

- `for each commit in f6095e2..ffa6ae7: git log -1 --format=%B <c> > msg; devtool check-commit-msg msg (devtool built from this tree)` — 9/9 PASS; Refs present on both fix commits (3c17dbb, b9c2676); log runs/62-fixseat-commit-msg-check-windows.log
- `git diff 4b33c19 ffa6ae7 --quiet -- internal cmd test tools docs` — exit 0: product tree at HEAD identical to the commit the implementer's final Windows/Linux runs name
- `go test ./internal/store -run '^TestGC_OverlappingCallsCountAResumedCursorOnce$' (and the 16 other rows named in the runs/60 header: 10 TestGCGate_*/TestGC_* gate rows, 5 TestGC_Deadline* rows, TestReadOnly_EveryExportedMethodIsClassified) -count=3 -v` — exit 0, 51/51 PASS (17 tests x 3), 493.8s on a co-loaded Windows host; log runs/60-fixseat-store-focused-windows.log
- `go test ./internal/daemon -run '^TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass$' plus '^TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot$' -count=3 -v` — exit 0, 6/6 PASS; log runs/61-fixseat-daemon-guards-focused-windows.log
- `go test ./test/guards -run '^TestGuard_StoreGCRunsOnlyInsideTheDaemon$' plus TestGuard_GCCallScannerSeesEveryShape, TestGuard_HotFilesAreReadWithDeleteSharing, TestGuard_SharedReaderScannerSeesAForbiddenCall -count=1 -v` — exit 0, 4/4 PASS; same log runs/61
- `go run ./tools/devtool lint --only=runpatterns,docmarkers` — PASS runpatterns (1008 of 1008 checkable resolved), PASS docmarkers; exit 0
- `go test ./test/docs -count=1` — ok (5.19s)

### Open issues

- Review finding (missing Refs footers on 7 test/docs commits) rebutted, not applied. The checker requires Refs on feat/fix only, and rewording would orphan 4b33c19, which 147 Windows log citations and every Linux gate identity.txt name. Integrate by merge to keep those SHAs reachable. If the footers are wanted anyway, the evidence must be re-anchored in the same rewrite.
- Pre-existing, not caused here: TestCarriedDefects_WaveReportRequiresResolution fails on its SP06-D2 subtest, identically at base f6095e2 (runs/56, C6.3).
- Pre-existing: TestIntegration_HotPathWarmWithRealResidentState fails when run alone, at 4b33c19 and at base f6095e2, with the same undelivered-sample count on both hosts (runs/57, 58).
- Load flake: TestFault_PublicationBoundaries/object_written_index_line_absent failed once in the loaded Linux full run (its recovery daemon never answered) and passed 3 of 3 alone.
- Coordination: w6-ckptsync may add a barrier to gc.json writes. This branch changes only how passes are serialized, not how gc.json is written, so the two should merge independently. Re-run the store rows in runs/60 after both land.
- The gate orders passes of ONE store handle. That is enough only while GC runs solely inside the daemon, which holds the daemon lock. A future out-of-daemon GC or repair caller trips TestGuard_StoreGCRunsOnlyInsideTheDaemon and must bring a store-level lock that cannot deadlock with the writer lease.

### Needs the owner

- gcSerialBound = 30s (internal/store/gc_serial_test.go, test-only). It bounds the event waits in the GC serialization tests. Each wait ends on an event and reaches the bound only if that event never comes. It mirrors the daemon tests' liveOrderBound of 30s ('generous for a co-loaded host'). If too small: spurious failures on a loaded -race host. If too large: only a slower report of a hang. No product effect.
- gcSerialTick = 1ms (same file, test-only): the require.Eventually polling interval over atomic loads. If wrong: only CPU cost while polling.
- No product budget or bound numbers were added. One design decision needs the owner's confirmation: a GC request that finds a pass running never joins it, but queues for one follow-up pass started after it. That follow-up answers every waiter of the same kind. The waiter granting the longest Deadline starts it. Only the caller's own ctx withdraws a request. Deadline counts from the pass start, not from the wait. No cross-process lock was added, because no GC entry point exists outside the daemon, and a guard keeps it that way.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



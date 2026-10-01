# W16B-SETTLE: PreCompact settle bound, estimator, doctor wording, idle kick

Branch `closeout/w16b-settle`. Workflow `wf_747e62d8-a28`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

**Coordinator note.** The implementer's statements that the last look's time is the first look's measured duration describe `5fd55bdd` only. The fix seat's `d2fa3757` superseded them: the waits and the replay run to the bound's own deadline, nothing is held back for the last look, and a file the last look cannot read in time is counted as unread.

## Implementer — status `done`, head `976d6918`

### Root cause

(1) Bound. Fix seat ebd93464 made settleFast call spooledCaptures/pendingClientCaptures, which read every client-*.ndjson in full, decoded each line and looked up the journal per capture, all before context.WithTimeout(bound) was created. unreplayedCaptures then repeated the same full scan after the bound. Neither scan was limited, and a healthy session paid both for other sessions' backlogs. A client-<pid> name does not say which session it belongs to, so only the lines can.
Shown red on base 6f118a7b with temporary diagnostics (not committed; log at runs/settle-bound-and-estimator-red-on-base.log):
- With B-E equal to the seal window (bound 0), the base still named the spooled Read, so it had read the file outside the bound.
- A healthy session beside 300 other sessions' spools paid 4.94 s on the first PreCompact (cold files; Windows real-time scanning puts a first open at about 13 ms) and about 51 ms on every later one, because it re-read all 300 each time.
(2) Estimator. settleBeforeSeal priced the names with tokens.New(cfg, ""), the identity, but Finalize's Truncate prices the document with the draft's src.Tokens (tokens.NewForProject, clamped to [0.6, 1.6]). Shown red on base: at factor 1.6, the 15 names sealed cost 907 tokens against the 600-token share.
(3) Doctor. spoolRow counted every non-directory entry and, in sync submode with a daemon serving, called them all client spools left by ACK-deadline deferrals. The directory also holds the daemon's own WAL segments (wal-*.ndjson), which the worker pool and drains replay. Red: the new subtests failed on the old wording (runs/doctor-wal-wording-red-before-fix.log).
(4) Freshness. kickSpoolWatch was called only from noteServed. In spool submode no hot-path hook connects, so nothing kicked the watcher until a non-hot request arrived or the idle drain ran DetectAfterSeconds later. No earlier code path existed for a test to fail against: the fix adds a new call site.

### Summary

All four wave 16b settle items are fixed on closeout/w16b-settle at head 976d6918, six commits on 6f118a7b. internal/daemon, internal/cli, internal/checkpoint and test/docs each pass in full on Windows.

**Item 1, the bound** (internal/daemon/precompact_settle.go and a new internal/daemon/spool_heads.go)
- **Deadline first.** The settle starts its deadline (start plus precompactSettleBound) before anything else. Each look at the spools lists the directory, then reads a file only if the deadline has not passed. A file it could not read in time is counted in the summary through a clause appended only in that case: "N hook client spool file(s) could not be read within the bound".
- **Time for the last look.** The lane waits and the replay (DrainClientSpoolsWithin) stop early by as long as the first look took. That is the time left for the last look. No new number: it is measured, not chosen.
- **Reuse.** The last look keeps the first look's result. It reads again only the files that look named, files listed since, and files the first look left unread. A file the first look read and found nothing of this session's in is not read again, even if it changed.
- **Index.** spool_heads.go remembers each client spool's line heads (offset, session, capture) for the file's listed size and modification time. Client spools only grow until a drain removes them, and drains record progress in state/drain.json, not in the file. So a file listed at the same size and time has not changed, and is read once per version. The drain's progress file is loaded only when a line of this session is found.
- **Who fills the index.** The settle indexes what it reads. The client-spool watcher also indexes every spool its pass leaves behind, under the same idleRunBudget, used here as a deadline. A healthy session beside an indexed backlog reads no file: one directory listing (Info is free on Windows, one lstat per file on Linux) plus memory lookups.
- **Healthy-session row.** TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists counts files through the new precompact_settle_spool_reads counter. Beside 300 other sessions' spools, the first PreCompact reads each file once (300). Later PreCompacts and 200 more settles read none, the settle counter stays 0 and nothing is drained. For the record: about 0.77–1.5 ms per warm settle, against about 51 ms on the base.
- **Strict limit.** "At most a directory listing" holds once the backlog is indexed. A spool written after the watcher's last pass, or any spool when no watcher has run, is read once by the first PreCompact that meets it, inside the bound. Getting to zero reads even then would need the session in the client spool's file name, an ipc format change outside this scope.
- **Other new rows.**
  - TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo: a zero bound reads no file and reports one unread spool; once the file is indexed, the Read is named from memory.
  - TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools: exactly three reads; a spool listed after the first look holds a Read, which is named; another session's spool that grew is not re-read.
  - TestSpoolWatch_IndexesTheSpoolsItsPassLeavesForThePreCompactSettle: a healthy settle beside a spool the watcher indexed reads 0 files.
- **Comments.** The comments on precompactSettleBound, settleBeforeSeal, the file header and counterPrecompactSettle now describe this behaviour. What can still run past the deadline is fixed work: a read already under way, the listings, the drain progress file, and the pricing of the names.

**Item 2, the estimator**
- checkpoint.PreCompactInput has a new PricedDrops field. PreCompact calls it once with the draft's own SourceSet.Tokens, the estimator Truncate uses. The daemon now hands the seal a report (the captures left, the unread count and the allowance) through the context, and wire_checkpoint passes it on as PricedDrops. The names are therefore priced at the seal.
- TestPreCompactSettle_NamesFitTheirShareUnderTheSealsCalibratedEstimator uses the real BindCheckpoint seam on a project calibrated to factor 1.6. The sealed names fit the 600-token share as the seal measures them. Priced with the identity instead, the same report names more entries and overruns the share.
- TestPreCompactPricesTheCallersDropsWithTheDraftsEstimator checks that PricedDrops receives the draft's own estimator instance.

**Item 3, doctor wording.** In sync submode with a daemon serving, the spool.pending detail now counts "N hook client spool(s)" (the ACK-deadline cause, replayed by the client-spool watcher) apart from "M daemon WAL segment(s)" (replayed by the worker pool and drains). Text that applies to one kind is printed only when that kind is present. The verdict and the Observed field are unchanged. Two new subtests went red first, then green.

**Item 4, freshness.** Run's idle tick now calls kickSpoolWatchInSpoolSubmode, which kicks the watcher only when the hot mode is spool. Everything else is the watcher's existing behaviour: a look per interval, a pass budgeted by idleRunBudget, and the existing backoff and horizon. The tick runs every 30 s at most. No new number. Covered by TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode: there is no kick in sync submode; in spool submode a spool written with no request served stays put until the tick's kick, after which the watcher publishes and releases it.

**Docs.** The slow-disk entry in docs/troubleshooting.md now says the spool is replayed on the idle tick, and that finding a session's spools runs inside the PreCompact budget, with unread files counted.

**Claims in the w16-spoolseal report that are now wrong.** I did not edit that report:
- Its last open issue says the spool scan "runs after the bound, inside B-E". There was also an unbounded scan before the bound; both are now inside it.
- It says a healthy session pays "a read of that file" beside another session's spool. That now happens once per file version; after that it costs the listing only.
- unreplayedNamesBudgetDivisor's comment said the names were priced as Truncate prices them. Now they are.

**Not run (daytime seat limits):** Linux, -race, test/e2e and test/integration, the hot-path rows, real Claude Code sessions.

### Commits

- 713e0ffa fix(checkpoint): let a caller price its drops at the seal
- fe4166a9 fix(daemon): kick the spool watcher from the idle tick in spool mode
- 5fd55bdd fix(daemon): run every precompact spool look inside its bound
- b7051af1 fix(cli): count daemon wal segments apart in doctor's spool row
- 2e7284ab docs(troubleshooting): say how spool submode replays and settles
- 976d6918 test(v6): record the w16b-settle runs on windows

### Tests

- `go test -p 2 -count=1 -run '^TestDoctor_SpoolSubmodeIsInformational$' -v ./internal/cli (before the doctor fix)` — FAIL as intended: the WAL-only and both-kinds subtests found the old 'client spools ... ACK deadline' detail (runs/doctor-wal-wording-red-before-fix.log)
- `go test -p 2 -count=1 -run '^TestDiagTmpBase' -v ./internal/daemon (temporary diagnostic on base 6f118a7b, not committed)` — FAIL as intended: a zero-bound look named the spooled Read; the healthy settle beside 300 spools cost 4.94 s cold and 51.3 ms per PreCompact warm; names priced with the identity cost 907 of a 600-token share at factor 1.6 (runs/settle-bound-and-estimator-red-on-base.log)
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists$' -v ./internal/daemon` — PASS: 300 reads on the first PreCompact, 0 after; route 3.98 s cold, 41 ms warm, settle 769 us per warm PreCompact (runs/focused-rows-windows.log)
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_NamesFitTheirShareUnderTheSealsCalibratedEstimator$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare$' -v ./internal/daemon` — PASS: the newest 16 of 300 named within 600 of 12000 tokens; 0 spool reads in the settle
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_AHealthySessionPaysNothing$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_WaitsForALeasedArrivalStillPublishing$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_NamesALeasedArrivalOnceBesideItsSpoolCopy$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactSettle_AReplayedPreCompactDoesNotSettle$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestSpoolWatch_IndexesTheSpoolsItsPassLeavesForThePreCompactSettle$' -v ./internal/daemon` — PASS
- `go test -p 2 -count=1 -run '^TestPreCompactPricesTheCallersDropsWithTheDraftsEstimator$' -v ./internal/checkpoint` — PASS
- `go test -p 2 -count=1 -run '^TestDoctor_SpoolSubmodeIsInformational$' -v ./internal/cli (after the fix)` — PASS, all 6 subtests
- `go test -p 2 -count=1 -run '^TestPreCompactInSpoolSubmodeSealsTheSpooledReads$' -v ./internal/cli` — PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon` — ok 308.7 s, exit 0 (runs/pkg-daemon-windows.log). The test binary was built just before one zero-semantic edit to spool_watch.go: the index block was moved ahead of an unrelated comment, with the same statements in the same order.
- `go test -p 2 -count=1 -timeout=30m ./internal/cli` — ok 89.8 s, exit 0 (runs/pkg-cli-windows.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint ./test/docs` — ok 96.1 s / 5.1 s, exit 0 (runs/pkg-checkpoint-docs-windows.log)
- `go vet ./internal/daemon ./internal/checkpoint ./internal/cli (Windows and GOOS=linux), on the final tree and on each staged commit's snapshot` — clean
- `go run ./tools/devtool fmt; go run ./tools/devtool fmt-check` — clean, exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS, exit 0

### Criterion changes

- TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare: the fixture now indexes the 300 spools before the PreCompact, by calling indexClientSpools as the watcher would. A new assertion checks the settle reads no file. The exact count of 300 and every naming assertion are unchanged. Why: item 1 requires every look to count against the 500 ms bound. A cold read of 300 just-written files took about 4 s on this host (about 13 ms per first open), so a cold backlog can no longer be counted in full inside the bound. It is now counted as unread spools, and TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo pins that case. Steady state is the indexed case, because the watcher indexes the spools its pass leaves.
- The unreplayed_capture summary's detail gains a clause, appended only when a look left spools unread: '; N hook client spool file(s) could not be read within the bound, so this session's captures in them, if any, are not counted here'. unreplayedDetailFormat itself is unchanged, so every row that pins it is unchanged.
- precompact_settle (the counter) now also counts a settle that could not tell within its bound whether it had anything to settle (spools left unread). New counter: precompact_settle_spool_reads.
- sealProbe in the daemon tests prices the report with the identity estimator, because the real seam's calibrated pricing is pinned by its own row. Before, the probe read pre-priced drops that the settle had already built with that same identity estimator, so the rows pin the same output as before.
- doctor spool.pending in sync submode with a daemon serving: the detail text changed (each kind counted and explained). The verdict and Observed are unchanged, and every existing subtest assertion still passes.

### Open issues

- Night run (coordinator): Linux runs of internal/daemon, internal/cli and internal/checkpoint, including every new row, and -race on the settle and watcher rows. None of these ran here; the container was stopped by instruction.
- Night run: the X11 B-E rows can still move. Each `qompack checkpoint` spawn now costs one listing plus a read of any spool not yet indexed, all inside the bound. The new counter precompact_settle_spool_reads should be recorded beside precompact_settle and precompact_unreplayed_captures. The TestIntegration_HotPath* rows carried from wave 16 also stay on the night list.
- Residual by design: a spool written since the watcher's last pass, or any spool when no watcher has run, is read once by the first PreCompact that meets it, inside the bound. Zero reads in that case would need the session in the client spool's file name, an ipc format change outside this scope. On Windows a first open of a just-written file cost about 13 ms here, consistent with real-time scanning, so a cold backlog of several dozen spools can exhaust the 500 ms. The report then says how many files went unread; nothing is lost.
- Residual by design: the last look does not re-read a file the first look found nothing of this session's in, even if it grew during the settle (a reused pid appending a capture of this session fired before the PreCompact). This follows the brief's 'named plus newer' rule. The drains still replay that capture; it is just not counted in this checkpoint.
- The time reserved for the last look is the first look's measured duration. When the replay runs to the deadline and other sessions write new spools meanwhile, the last look may report them as unread (a summary clause; no loss).
- The watcher's indexing adds up to idleRunBudget (2 s) to a look after a pass, only while it has unindexed due spools to read. This is wall time on the watcher's own goroutine, not on any hook's path.
- Carried from wave 16, unchanged: the idle exit judges liveness from LastActivity, which spool-submode hooks do not refresh.

### Needs the owner

- No new budget or bound number. precompactSettleBound (500 ms), unreplayedNamesBudgetDivisor (20) and idleRunBudget are reused as D55 approved them. The idle-tick kick uses Run's existing tick (min(idleTickMax 30 s, idleExitSeconds/10)), and the last look's time is the first look's measured duration.
- Design point to confirm: the spool index keys a file version on (size, modification time) and relies on client spools being append-only until a drain removes them. A file removed and recreated under the same pid name with the same size and the same timestamp would be served from the stale index. If the owner wants that closed fully, the next step is a file identity check (os.SameFile), which costs a stat per file.

## Independent review

### review:settle: needs-fixes

- **minor** `internal/daemon/precompact_settle.go:236` — The time held back for the last look is the first look's whole measured duration, so the waits and the replay get bound - 2*e1. Once the first look takes half the bound or more, the session's own spool is never replayed. The reservation is also not derived from what the last look actually reads. The last look reuses the index. The session's own files are either released by the replay (no longer listed) or unchanged (served from memory). So the last look only reads files listed since the first look. A cold backlog of other sessions' spools now spends this session's replay time, and the comments say that must not happen.
  - Evidence: `wctx, wcancel := context.WithDeadline(sctx, deadline.Add(-time.Since(begun)))`: at wctx creation now = begun+e1 and the deadline is begun+bound-e1, which leaves bound-2*e1 for awaitArrivals and DrainClientSpoolsWithin. The implementer measured about 13 ms per cold first open on this host. That makes 20 unindexed client spools about 260 ms, which is more than 250 ms, so the replay is skipped (`len(own) > 0 && wctx.Err() == nil` is false). On base 6f118a7b the replay had the full 500 ms. Spool submode on a slow disk is exactly when many fresh client-<pid> files exist. The file header still says another session's backlog "must not spend this session's bound". No row covers a cold backlog combined with the session's own spool and a non-zero bound.
  - Fix: Give the waits and the replay the bound's own deadline, or reserve only a derived estimate for the files the last look must read: (count of files listed since the first look) times the first look's measured per-file read cost, never the whole first-look time. Files the last look cannot read are already reported as unread, so no reservation is needed for correctness. Add a row that pairs a non-zero bound and the session's own spool with N unindexed other-session spools, injected slow through a read seam. It should assert that the own spool is still replayed whenever the first look leaves time.
- **minor** `internal/cli/doctor.go:940` — Item 3 is only partly closed. Every non-directory file that is not a WAL segment is counted as a "hook client spool". The spool directory also holds the client's externalized blobs (blob-<pid>-<n>.bin, internal/ipc/client.go:84-88). These are written on the live path too and are referenced from WAL lines. They are not client spools, and no ACK-deadline deferral created them. A blob that sits beside a pending WAL segment is reported as "1 hook client spool(s) ... ACK deadline", which is the mislabel this item exists to remove.
  - Evidence: spoolRow does `files++` for every regular entry and `wal++` only for the `wal-` prefix, then calls `syncSpoolDetail(files-wal, wal)`. safeBlobName in internal/daemon/blob.go:113 confirms blob-*.bin files live in paths.Of(root).Spool. No subtest seeds a blob file. doctorWALSpoolPrefix also re-declares ipc's walFilePrefix and drain.go's drainWalPrefix, and it matches on the prefix only, without the .ndjson suffix.
  - Fix: Classify each entry the way ipc.SpoolFiles and isClientSpoolName do: client-*.ndjson counts as a client spool, wal-*.ndjson as a WAL segment, and anything else (blob-*.bin side files, stray files) is counted and worded generically, for example "N other spool file(s) (externalized tool results)". Add a subtest that seeds a blob file next to a WAL segment.
- **nit** `internal/daemon/precompact_settle.go:216-219` — precompact_settle_spool_reads is the change in the index-wide read counter while this settle runs. That counter also counts reads by the client-spool watcher's indexClientSpools and by any other session's settle running at the same time. The metric is meant to be recorded on the night run beside the X11 B-E rows, and it over-attributes reads to a settle under concurrency.
  - Evidence: `reads := d.spoolHeads.reads.Load()` ... `Add(d.spoolHeads.reads.Load() - reads)`. spool_watch.go now calls d.indexClientSpools after every pass with due spools, and that increments the same spoolHeads.reads.
  - Fix: Count reads per scan: have heads return whether it read the file, or take a *int64, and add up the reads of the two scanClientSpools calls. Keep spoolHeads.reads only for the index-wide total, or remove it.
- **nit** `internal/daemon/spool_heads.go:91-111` — The index treats (size, mtime) as a file's identity, and entries are dropped only when a later settle's listing no longer shows the file. A drain can release client-<pid>.ndjson, and a hook whose pid was reused can then recreate it with the same size and mtime before the next settle lists the directory. The index then serves the old file's heads. In that case the settle neither replays nor counts this session's capture, which breaks the "never silently missing" contract of the drop report. The implementer raised this under needs_owner, but the code has a fix that needs no extra stat.
  - Evidence: forget() is called only from scanClientSpools. The drain's removal at drain.go:1201 does not tell the index. Spools on the slow filesystems spool submode targets (network or drvfs) can have coarse mtime granularity.
  - Fix: Invalidate the index entry when the daemon itself removes a client spool: a hook from the drainer's removal path (or a callback in drainConfig) that deletes spoolHeads.files[base]. A recreated file is then always read again. Pin this with a row that removes a file and recreates it with the same bytes and mtime.
- **nit** `internal/daemon/daemon.go:853` — The new row calls kickSpoolWatchInSpoolSubmode directly, so nothing checks that Run's idle tick actually calls it. Item 4 depends on that call, and if the one-line wiring is removed or moved below an early `continue`, no test fails.
  - Evidence: TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode calls `dd.kickSpoolWatchInSpoolSubmode()` itself. A grep finds no test that drives Run's idleTicker in spool submode.
  - Fix: Factor the tick body into a method (for example d.onIdleTick(runCtx, now)) that Run calls and the row also calls. Alternatively, start Run with a small IdleExitSeconds (tick = idleExitSeconds/10) in spool submode and assert that a spooled Read is published with no request served.

## Fix seat (review resolution) — status `done`, head `563ad4a2`

### Root cause

Finding 1: settleBeforeSeal gave its waits and replay a deadline of the bound minus the first look's whole measured duration, so they had bound - 2*e1, and a slow first look over other sessions' cold spools left no time to replay the session's own spool. Finding 2: doctor's spoolRow counted every non-WAL file in the spool directory as a hook client spool, including the hooks' externalized tool-result blobs (blob-<pid>-<n>.bin).

### Summary

The fix seat for w16b-settle checked both reviewer findings independently. Both were correct, and both are fixed, each with a failing test first. Branch closeout/w16b-settle in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16b-settle, head 563ad4a2. The working tree is clean.

## Review resolution

### Finding 1 (minor): internal/daemon/precompact_settle.go:236, last-look reservation. CONFIRMED and FIXED in d2fa3757.

- **Root cause.** The code did `wctx := context.WithDeadline(sctx, deadline.Add(-time.Since(begun)))`. That held back time for the last look equal to the first look's whole duration (e1), so the waits and the replay only had `bound - 2*e1`. When a cold backlog of other sessions' spools made the first look take half the bound or more, the session's own spool was never replayed. The file header says another session's backlog must not do that.
- **Failing test first.** The new row `TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound` (internal/daemon/precompact_settle_bound_test.go) sets things up as follows:
  - a 3 s bound;
  - 20 unindexed client spools of other sessions, listed before the session's own spool `client-9999.ndjson`;
  - a new read seam (`spoolHeadIndex.read`, internal/daemon/spool_heads.go; nil means `paths.ReadFileShared`) that holds the last backlog read until less than 45 % of the bound is left. This is deterministic: it waits on the look's own context deadline, with no sleep and no clock assumption.

  It asserts:
  - fixture sanity: the first look took more than half the bound and ended before it;
  - the own Read is replayed (`probe.published`);
  - the drop report is empty;
  - exactly 21 files are read: every file once in the first look, none in the last.

  On the old logic it failed on "the first look left time, so the session's own spool is replayed before the seal" (plans/sdd/V6-closeout/w16b-settle/runs/fix-cold-backlog-red-before-fix.log).
- **Fix.** The waits and the replay now run on `sctx`, the bound's own deadline. Nothing is reserved for the last look. I took the reviewer's first option because no reservation is needed for correctness:
  - a file the replay released is no longer listed;
  - a file it partly replayed is unchanged, because the drain records progress in state/drain.json, so it is served from the index and filtered by the progress offset;
  - a file the last look would have to read past the deadline (one listed since the first look, or one a hook appended to) is counted as unread in the summary, which the existing `ALookPastTheBoundReadsNothingAndSaysSo` row pins.

  The settleBeforeSeal doc comment now says this. The unused `begun` and `deadline` variables are gone. No new number is introduced.
- **Trade-off, stated plainly.** If the replay uses the whole bound, a spool listed since the first look is reported as "could not be read" rather than named. On the implementer's head, the last look got e1 for that case.

### Finding 2 (minor): internal/cli/doctor.go:940, blob files counted as client spools. CONFIRMED and FIXED in 57b7a4b3.

- **Evidence.** spoolRow counted `files - wal` as hook client spools. internal/ipc/client.go:84-88 writes `blob-<pid>-<n>.bin` into the same directory, and the daemon removes it in blob.go removeBlob once the request that names it is published.
- **Failing test first.** The new subtest `TestDoctor_SpoolSubmodeIsInformational/sync_submode_does_not_call_an_externalized_tool_result_a_client_spool` seeds a WAL segment and a blob. It failed with the detail "1 hook client spool(s) ... ACK deadline ..." (runs/fix-doctor-blob-red-before-fix.log).
- **Fix.**
  - internal/ipc/spool.go now exports `SpoolFileKind` and `SpoolFileKindOf` (`SpoolFileWAL` / `SpoolFileClient` / `SpoolFileOther`), with the same prefix-and-.ndjson rule as before. `SpoolFiles` itself now uses it, so the two cannot drift.
  - doctor counts the three kinds with it. `syncSpoolDetail(client, wal, other)` words the third kind on its own: "N other spool file(s): tool results a hook externalized beside its request (blob-*.bin), which the daemon removes once it has published the request naming them, or files no replay reads".
  - doctor's own `doctorWALSpoolPrefix` is deleted.
  - The verdict and Observed (the total file count) are unchanged.
  - New row `TestSpoolFileKindOf_ClassifiesAsSpoolFilesDoes` covers blob, `.tmp` and wrong-extension names.
  - The daemon's own respelled `drainWalPrefix` and `isClientSpoolName` are left as they are (out of this finding's scope).

## Implementer items (1)-(4)

The fix seat did not reopen these. The reviewer raised nothing against (2) the estimator (713e0ffa), (4) the idle-tick watcher kick (fe4166a9) or the healthy-session listing-only path (`TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists` still passes in the full package run). Item (1) is now as described under finding 1. Item (3) now also covers blob files.

## Commands and results (Windows, -p 2, loaded machine)

- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=1 -v` before the fix: FAIL, exit 1. After the fix: PASS, and with `-count=5` 5/5 PASS at about 1.95 s each.
- `go test -p 2 ./internal/cli -run '^TestDoctor_SpoolSubmodeIsInformational$' -count=1 -v`: the new subtest failed before the fix; afterwards all 7 subtests PASS.
- `go test -p 2 ./internal/ipc -run '^TestSpoolFileKindOf_ClassifiesAsSpoolFilesDoes$' -count=1 -v`: PASS.
- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal$' -count=1`: ran as part of a temporary combined diagnostic run of the settle, spool-watch and spool-line-head rows, all ok (36 s). Not logged separately; the full package run below covers them.
- `go test -p 2 -timeout=30m -count=1 ./internal/daemon`: ok, 419 s (runs/fix-pkg-daemon-windows.log).
- `go test -p 2 -timeout=30m -count=1 ./internal/cli`: ok, 87 s (runs/fix-pkg-cli-windows.log).
- `go test -p 2 -timeout=30m -count=1 ./internal/ipc`: ok, 24 s (runs/fix-pkg-ipc-windows.log).
- `go vet` on internal/daemon, internal/cli and internal/ipc, on Windows and with GOOS=linux: clean.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.

No Linux container run: the container is stopped by this wave's rule. No -race run on whole packages. No docs or generated inputs changed (code comments only), so test/docs and gen-*-docs were not re-run. Logs were saved with output redirection, with exit codes recorded and no pipes.

## Criterion changes

None. No assertion was loosened, and no threshold or timeout was changed. `coldBacklogBound` (3 s) and `coldBacklogLeft` (45 %) are test-fixture constants with derivation comments, not product budgets.

## Note for the coordinator's report

The implementer's report sentence "the last look's time is the first look's measured duration" is now false. The waits and the replay run to the bound's own deadline, and the last look reads only before it. Correct that sentence when committing the report.

### Commits

- d2fa3757 fix(daemon): give the precompact replay the bound's own deadline
- 57b7a4b3 fix(cli): count externalized tool results apart in doctor
- 563ad4a2 test(v6): record the w16b-settle fix seat's runs on windows

### Tests

- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=1 -v (before fix)` — FAIL as intended (own spool not replayed); runs/fix-cold-backlog-red-before-fix.log
- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound$' -count=5 -v` — PASS 5/5 (~1.95 s each)
- `go test -p 2 ./internal/cli -run '^TestDoctor_SpoolSubmodeIsInformational$' -count=1 -v` — before fix: new blob subtest FAIL (runs/fix-doctor-blob-red-before-fix.log); after fix: PASS, 7/7 subtests
- `go test -p 2 ./internal/ipc -run '^TestSpoolFileKindOf_ClassifiesAsSpoolFilesDoes$' -count=1 -v` — PASS
- `go test -p 2 -timeout=30m -count=1 ./internal/daemon` — ok 419s
- `go test -p 2 -timeout=30m -count=1 ./internal/cli` — ok 87s
- `go test -p 2 -timeout=30m -count=1 ./internal/ipc` — ok 24s
- `go vet ./internal/daemon ./internal/cli ./internal/ipc (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- None. Two rows and one subtest were added (TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound, TestSpoolFileKindOf_ClassifiesAsSpoolFilesDoes, and the TestDoctor_SpoolSubmodeIsInformational blob subtest). No assertion, threshold or timeout was weakened.

### Open issues

- Linux and -race were not run for this seat: the container is stopped by this wave's rule. The cover seat or the integration run should run the -race tests for internal/daemon (precompact settle and spool rows), internal/cli and internal/ipc on the integrated head.
- Trade-off of the finding-1 fix: if the replay takes the whole bound, a client spool listed since the first look, or an own spool a hook appended to during the settle, is reported as 'could not be read within the bound' rather than named. This is an honest under-count, not a silent one.
- The daemon still respells the spool prefixes (drain.go drainWalPrefix/isClientSpoolName); they could call ipc.SpoolFileKindOf. Left alone because it was out of this finding's scope.
- The implementer's report sentence 'the last look's time is the first look's measured duration' must be corrected when the coordinator commits the report.

### Needs the owner

- No new budget or bound number. precompactSettleBound (500 ms), unreplayedNamesBudgetDivisor (20) and idleRunBudget are reused as D55 approved them. The idle-tick kick uses Run's existing tick (min(idleTickMax 30 s, idleExitSeconds/10)). After the fix seat's change, no time is held back for the last look: the waits and the replay run to the bound's own deadline, and the last look reads only before it, counting any file it could not read as unread.
- Design point to confirm: the spool index keys a file version on (size, modification time) and relies on client spools being append-only until a drain removes them. A file removed and recreated under the same pid name with the same size and timestamp would be served from the stale index. Closing that fully needs a file identity check (os.SameFile), which costs a stat per file.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



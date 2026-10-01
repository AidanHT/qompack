# W16C-SETTLE2: per-settle read count, spool-index invalidation, idle-tick kick under test

Branch `closeout/w16c-settle2`. Workflow `wf_5e8aadd4-6ae`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

**Coordinator note.** The implementer's stray `git stash pop` (reported below) was checked after the wave: the repository's stash list still holds all nine entries, `stash@{0}` is the same feat/sp13 entry, and both wave 16c worktrees were clean.

## Implementer — status `done`, head `64b73856`

### Root cause

(1) The settle measured the index-wide spoolHeads.reads delta, so it also counted the watcher's indexClientSpools and other sessions' concurrent settle reads. (2) Spool-index entries were dropped only by a later listing; the drain's removal of a client spool never told the index, so a file recreated by a reused-pid hook at the same size and mtime was served the released file's heads. (3) The idle-tick row called kickSpoolWatchInSpoolSubmode directly, so Run's call to it was untested.

### Summary

W16c settle2 seat. It closes the three nits left open by the w16b-settle review (plans/sdd/V6-closeout/w16b-settle/report.md, Review section) and does the optional prefix cleanup. All changes are in internal/daemon on branch closeout/w16c-settle2, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16c-settle2. Four product/test commits and one evidence commit sit on top of ae601390. Evidence logs are in plans/sdd/V6-closeout/w16c-settle2/runs/.

## (1) precompact_settle_spool_reads over-attributed: FIXED (66ad4071)
- **Root cause.** settleBeforeSeal recorded the change in the index-wide `d.spoolHeads.reads` counter across the settle. That counter is shared with indexClientSpools, which the watcher runs after every pass that leaves spools, and with any concurrent settle of another session. Both were counted against this settle.
- **Fix.**
  - `spoolHeadIndex.heads` now returns `(lines, ok, read bool)`.
  - `spoolScan` carries `reads`.
  - The settle adds `first.reads + last.reads` in its deferred counter update.
  - `spoolHeads.reads` stays as the index-wide total. Existing rows use it, and its comment now says it counts every look.
- **Row.** `TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks` (internal/daemon/precompact_settle_bound_test.go). It uses a deterministic seam, not sleeps:
  - The `spoolHeads.read` test seam fires when the settle reads its one listed spool.
  - At that point it writes a second spool and runs `dd.indexClientSpools` on its own goroutine, then waits for it to finish.
  - Fixture sanity: the index read 2 files in all. The row asserts the settle counts 1.
- **Red before the fix:** expected 1, actual 2 (runs/red-before-fix-settle-rows.log).

## (2) The spool index could serve a recreated file's stale heads: FIXED (c275510c)
- **Root cause.** Index entries left only through `forget(listed)`, which runs when a later settle's listing no longer shows the file. The drain's removal (drain.go removeCompletedFile → removeIfUnchanged) never told the index. A client-<pid>.ndjson that a drain released and a reused-pid hook recreated at the same size and mtime was therefore served from the released file's heads. Those heads hold only an acknowledged nonce, so the settle found nothing and sealed with an empty drop report: the capture went silently missing.
- **Fix.**
  - New `DrainConfig.ClientSpoolRemoved func(base string)`. removeCompletedFile calls it for a client spool it removed, or found already gone (ErrNotExist).
  - The daemon wires it to the new `spoolHeadIndex.removed`, which deletes the entry and bumps a `removals` generation under the index mutex.
  - `heads` snapshots that generation before a cold read and stores the result only if no removal happened meanwhile. A read that straddled a removal therefore cannot put the removed file's heads back; it still returns what it read to its caller.
  - No stat is added and there is no new number.
  - The daemon never truncates a client spool (no Truncate in drain.go), so removal is the only path that needs the hook.
- **Rows.**
  - `TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated`:
    1. index client-7474.ndjson holding Read 1;
    2. `dd.Drain` publishes and releases it;
    3. recreate the file with Read 2, which has the same byte length;
    4. set the old mtime with `os.Chtimes`, with fixture-sanity asserts that size and mtime are equal;
    5. a PreCompact whose replay of Read 2 cannot finish in the bound.

    It asserts the settle reads the file again (`precompact_settle_spool_reads` == 1) and that the drop report names Read 2's tool_use_id. Red before the fix: expected 1, actual 0 (runs/red-before-fix-settle-rows.log).
  - `TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered`: the read seam calls `removed` in the middle of the read. The row asserts the read result is returned but not remembered, and that the next read is remembered (control). Red when the generation guard is disabled by a temporary local edit, not committed (runs/red-temp-no-removal-guard.log).
- **Design point for the reviewer.** The generation is index-wide, not per file. Any removal during a cold read discards that read's memo, which costs at most one extra read later. I chose this because a per-base generation map needs cleanup, and cleanup reintroduces an ABA case.

## (3) Item 4's idle-tick wiring was untested: FIXED (aa72b518)
- Run's idle-tick body before the exit decision is now `func (d *daemon) onIdleTick(ctx context.Context) core.UnixMilli`: Notify, kickSpoolWatchInSpoolSubmode, idle RunOnce if idle, config reload, and it returns now. Run calls it, then `idleExitDue`. Behaviour is unchanged.
- `TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode` now calls `dd.onIdleTick(ctx)` for both halves (no kick in sync submode; in spool submode the tick's kick publishes the spooled Read). It first marks the project active (`dd.idle.Notify(now)`), and asserts `!IsIdle(tick's now)`. That rules out the idle drain, so only the kick can publish the Read.
- kickSpoolWatchInSpoolSubmode no longer returns a bool that nothing reads.
- **Red shown once.** I deleted the kick line from onIdleTick in a temporary local edit, not committed, and restored it. The row then FAILED on "the tick's kick lets the watcher publish the spooled Read" (runs/red-temp-kick-removed-from-tick.log, 30.47 s).

## Optional cleanup (55f4b13d)
`isClientSpoolName` now returns `ipc.SpoolFileKindOf(base) == ipc.SpoolFileClient`, and the unused `drainClientPrefix` is deleted. `drainWalPrefix` and `drainFileExt` stay, because walSessionID takes the name apart. This is a 4-line diff, and `TestSpoolWatch_KnowsTheClientSpoolFamily` passes.

## Incident to report (own worktree only, no lasting damage)
While splitting hunks into commits, a failed `git apply --cached --check` in a `&&` chain fell through to a `; git stash pop` that I had meant to pair with a `stash push` that never ran. It popped the repository's shared stash@{0}, a 'WIP on feat/sp13-mcp-retrieval-layer' entry that is not mine. The pop hit conflicts, so git kept the entry: the stash list still has all 9 entries, and stash@{0} is the same entry.
- The pop touched only these files in my worktree: .github/workflows/ci.yml, plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md, plans/V5-VERIFY-commands-selection-grammar-and-refinements.md, tools/devtool/{cover.go, main.go, planscope_test.go, pluginvalidate.go}.
- I restored those files with `git checkout HEAD --`. My internal/daemon files were checked byte-identical against saved copies.
- No other worktree or branch was touched. I stopped using stash; staging is now done with `git apply --cached` of selected hunks.
- Each intermediate commit was checked for vet and its rows in an exported index tree.

## Commands and results (Windows, -p 2, loaded machine; no pipes on recorded go test runs; exit codes in the logs)
- Red before the fix, focused run of the two new settle rows (`TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks` and `TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated`): both FAIL as intended, exit 1 (runs/red-before-fix-settle-rows.log).
- The same two rows after the fix, -count=3: PASS 3/3 each, exit 0 (runs/green-after-fix-settle-rows.log).
- `go test -p 2 ./internal/daemon -run '^TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered$' -count=1 -v`: PASS. It FAILs with the guard disabled by a temporary edit (runs/red-temp-no-removal-guard.log).
- `go test -p 2 ./internal/daemon -run '^TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode$' -count=3 -v`: PASS 3/3 (runs/green-idle-tick-row.log). It FAILs with the kick removed from onIdleTick by a temporary edit (runs/red-temp-kick-removed-from-tick.log).
- `go test -p 2 ./internal/daemon -run '^TestSpoolWatch_KnowsTheClientSpoolFamily$' -count=1 -v`: PASS.
- `go test -p 2 -timeout=30m -count=1 ./internal/daemon`: ok, 329.6 s, exit 0 (runs/pkg-daemon-windows.log).
- `go vet ./internal/daemon` (Windows) and `GOOS=linux go vet ./internal/daemon`: clean.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0. It ran on a tree byte-identical to HEAD's internal/daemon files.

No Linux container run (the container is stopped by this wave's rule), and no -race. No docs or generated inputs changed (code comments only), so test/docs and gen-*-docs were not run.

## Criterion changes
None. No assertion was loosened, and no threshold or timeout was changed.
- `TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode` now drives the tick (onIdleTick) instead of calling the kick helper. It gains a fixture-sanity assertion (`!IsIdle`) and keeps all of its earlier assertions. This makes the row stricter: it now fails when the wiring is removed.
- `TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo` changes one line, mechanically, to the new three-value heads signature (`_, ok, _ :=`).

### Commits

- 66ad4071 fix(daemon): count only the settle's own spool reads
- c275510c fix(daemon): drop a released client spool from the spool index
- aa72b518 test(daemon): drive the idle tick's spool-watch kick through Run's tick
- 55f4b13d refactor(daemon): classify client spools with ipc.SpoolFileKindOf
- 64b73856 test(v6): record the w16c-settle2 seat's runs on windows

### Tests

- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks$' (inside the logged two-row runs, before and after the fix)` — before the fix: FAIL (expected 1, actual 2); after the fix: PASS 3/3
- `go test -p 2 ./internal/daemon -run '^TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated$' (inside the logged two-row runs, before and after the fix)` — before the fix: FAIL (expected 1, actual 0 reads); after the fix: PASS 3/3
- `go test -p 2 ./internal/daemon -run '^TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered$' -count=1 -v` — PASS; FAIL with the removal guard disabled by a temporary uncommitted edit
- `go test -p 2 ./internal/daemon -run '^TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode$' -count=3 -v` — PASS 3/3; FAIL with the kick deleted from onIdleTick by a temporary uncommitted edit
- `go test -p 2 ./internal/daemon -run '^TestSpoolWatch_KnowsTheClientSpoolFamily$' -count=1 -v` — PASS
- `go test -p 2 -timeout=30m -count=1 ./internal/daemon` — ok 329.6s, exit 0
- `go vet ./internal/daemon (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- None loosened. TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode now drives onIdleTick, the tick Run runs, instead of the kick helper, and adds a !IsIdle fixture-sanity assertion; it is stricter.
- TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo: one line changed mechanically to the three-value heads signature.

### Open issues

- Linux and -race were not run for this seat: the container is stopped by this wave's rule. The integration run should run the internal/daemon precompact-settle, spool-head and spool-watch rows under -race on Linux.
- The spool index's forget(listed) still drops an entry that a concurrent watcher indexed after the settle's listing. This is pre-existing and conservative: it costs one extra read later, never a stale serve.
- The removal guard is one index-wide generation: any client-spool removal during a cold read discards that read's memo, so at most one extra read follows. This was chosen over a per-base map, whose cleanup reintroduces ABA.
- Process note: a stray `git stash pop` in my own worktree briefly applied the shared stash@{0} (feat/sp13 WIP, not mine). Git kept the entry because of the conflicts. The affected files were restored to HEAD, and the stash list is intact with 9 entries.

## Independent review

### review:settle2: needs-fixes

- **minor** `internal/daemon/drain.go:1182-1186 (removeCompletedFile) with internal/daemon/spool_heads.go:142-147 (spoolHeadIndex.removed)` — The index is invalidated only after the unlink has returned, so a stale serve is still possible in the gap between them. The new comments overstate the guarantee: they say a recreated file "is always read again". During that gap the old entry is still in the index at (S, M). Sequence: removeIfUnchanged's os.Remove returns; a reused-pid hook recreates client-<pid>.ndjson at the same size and coarse mtime; a settle's (or the watcher's) listClientSpools sees (S, M) and heads() hits the old entry and returns the released file's heads; only then does ClientSpoolRemoved run. Neither the settle's scans nor indexClientSpools take the drain mutex, so nothing orders them against the drain. The gap is a few instructions plus any preemption, so it is very unlikely in practice, but it is exactly the silent-miss case nit 2 targets. Calling removed() before the unlink as well does not close it: a read of the old file between the pre-call and the unlink stores its heads with the current generation.
  - Evidence: drain.go: `removed, err := remove(path, fs.Offset); if removed || errors.Is(err, os.ErrNotExist) { if isClientSpoolName(base) && dr.cfg.ClientSpoolRemoved != nil { dr.cfg.ClientSpoolRemoved(base) } ... }`. spool_heads.go heads(): `if hit && f.size == l.size && f.mod.Equal(l.mod) { return f.lines, true, false }`, which serves the entry with no removal check. precompact_settle.go scanClientSpools and spool_heads.go indexClientSpools run without dr.mu. The spool_heads.go header says "a recreated file is always read again".
  - Fix: Bracket the unlink in the index. Option A: replace ClientSpoolRemoved with a hook that runs the removal under the index lock, for example `ClientSpoolRemove func(base string, remove func() (bool, error)) (bool, error)`, wired to a spoolHeadIndex method. That method takes x.mu, runs remove, and on success or ErrNotExist deletes the entry and bumps removals before it unlocks. heads holds x.mu only briefly and never across I/O, so a concurrent look waits for at most one stat and one unlink. The lock order is drain mu then index mu, which nothing reverses. Option B: begin(base) before the unlink adds base to a `removing` set, deletes the entry and bumps the generation; heads treats a base in `removing` as a miss and never stores it; end(base) after the unlink bumps again and clears the mark. Either way, add a seam row that recreates the file and runs heads between the unlink and the invalidation, and confirm it reads again. If the owner accepts the gap instead, reword the spool_heads.go header and the DrainConfig.ClientSpoolRemoved comment to name it.
- **nit** `commits aa72b518 and 55f4b13d` — Two code commits have no Refs footer. The other product commit in this seat (c275510c) and the base's code commits carry `Refs: V6-VERIFY, C4.3`; only the evidence-log commits (test(v6): record ...) leave it out.
  - Evidence: `git log --format='%h %s | %(trailers)' ae601390..HEAD`: 66ad4071 and c275510c have `Refs: V6-VERIFY, C4.3`. aa72b518 (test(daemon): drive the idle tick's spool-watch kick through Run's tick) and 55f4b13d (refactor(daemon): classify client spools with ipc.SpoolFileKindOf) have no trailers.
  - Fix: When the branch is next reworded or integrated, add `Refs: V6-VERIFY, C4.3` to aa72b518 and 55f4b13d. Add no attribution trailers.

## Fix seat (review resolution) — status `done`, head `8f59c41c`

### Root cause

removeCompletedFile told the spool index about a released client spool only after os.Remove returned. heads() served any entry whose (size, mtime) matched, without checking for a removal in progress, and the settle's and watcher's looks do not take the drain mutex. A reused-pid hook that recreated the file at the same size and coarse mtime inside that gap was therefore served the removed file's heads.

### Summary

The fix seat for w16c-settle2 is done. Branch closeout/w16c-settle2 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16c-settle2 is at 8f59c41c. The implementer's five commits are on top of ae601390, and the fix seat added two.

## Review resolution
- Finding (minor): the index was cleared only after the unlink returned, so a stale result could still be served in the gap. It also said the comments overstated the guarantee.
  - Verdict: CONFIRMED. In the implementer's code removeCompletedFile called ClientSpoolRemoved(base) only after remove() returned. heads() served any entry whose (size, mtime) matched, without checking for a removal in progress. The settle's scans and indexClientSpools do not take dr.mu, so nothing orders them against the drain. The reviewer was also right that a single call before the unlink does not close the gap.
  - Failing test first: I added the row TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain in internal/daemon/precompact_settle_bound_test.go. It uses a new drainer seam, removeSpool (removeIfUnchanged outside tests). The row drives the real Drain → removeCompletedFile path:
    1. Index the spool.
    2. Inside the removal, before the unlink, look at the released file.
    3. Unlink, then recreate the same name with bytes of the same length and the same mtime (os.Chtimes).
    4. Look again while the removal is still under way.
    5. After the drain, look once more, then a control look.
  - Before the fix it failed with "the look right after the unlink reads the recreated spool": it was served the removed file's heads. Log: plans/sdd/V6-closeout/w16c-settle2/runs/red-before-fix-unlink-window.log.
  - Fix: I used the reviewer's Option B rather than A, because A holds the index lock across stat+unlink. On the slow network or drvfs filesystems that spool submode is for, that would block every settle's heads() lookup for the whole unlink, outside the settle's context bound.
    - DrainConfig.ClientSpoolRemoved(base) is now ClientSpoolRemoving(base) (done func()). The drain's new removeSpoolFile calls it before the unlink of a client spool and runs done after the unlink, whatever the result.
    - spoolHeadIndex.removed is now removing. At both ends it deletes the entry and bumps removals; it also tracks which removals are in progress.
    - heads() treats a name being removed as a miss. Anything a look stores during the removal is deleted again by done, and a look that started before either end is not stored, because of the removals bump.
    - No lock is held across I/O. Lock order stays dr.mu then x.mu, with x.mu held only briefly.
    - Comments in the spool_heads.go header, removing, heads and DrainConfig now describe this. The old over-claim is gone: a recreated file is now read again in fact.
  - Mutation check (temporary local edit, not committed, restored and verified with grep): removing the hit-side check of removals in progress turns the new row red. Log: red-temp-no-unlinking-check.log.
  - An earlier draft also refused to store during the removal. I dropped that because done's delete already covers it, and a check no mutation can catch would only confuse readers.

## What changed
- internal/daemon/drain.go: ClientSpoolRemoving, the removeSpool seam, and removeSpoolFile.
- internal/daemon/spool_heads.go: removing, and the check for removals in progress in heads.
- internal/daemon/daemon.go: wires ClientSpoolRemoving to d.spoolHeads.removing.
- internal/daemon/precompact_settle_bound_test.go: the new row, plus a one-line change in TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered, which now calls removing(base)() in place of removed(base). Its assertions are unchanged.

## Commands and results (Windows, daytime limits: -p 2, no -race, no load generators, Linux container not used)
- `go test -p 2 ./internal/daemon -run '^TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain$' -count=1 -v` before the fix: FAIL, exit 1 (red-before-fix-unlink-window.log).
- The same command with the temporary mutation: FAIL, exit 1 (red-temp-no-unlinking-check.log).
- After the fix, five rows ran by exact name with -count=3: all PASS, exit 0 (green-after-fix-unlink-window.log). The rows are TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain, TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered, TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated, TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks and TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode.
- `go test -p 2 ./internal/daemon -count=1 -timeout=30m`: ok in 371.761 s, exit 0 (pkg-daemon-windows-review-fix.log). It ran on exactly the tree that was then committed.
- `go vet ./internal/daemon` and the same with GOOS=linux: clean.
- `go run ./tools/devtool fmt` then `fmt-check`: clean.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.
- Wall-clock failures: none.

## Criterion changes
None. No assertion was loosened, and no threshold, timeout or golden changed. The existing row's call-site edit only follows the API rename.

## Open items
- None from the review.
- No new budget or bound numbers, so there is nothing for the owner to approve.
- Linux was not run, because the container is stopped for this wave.

### Commits

- 66ad4071 fix(daemon): count only the settle's own spool reads (implementer)
- c275510c fix(daemon): drop a released client spool from the spool index (implementer)
- aa72b518 test(daemon): drive the idle tick's spool-watch kick through Run's tick (implementer)
- 55f4b13d refactor(daemon): classify client spools with ipc.SpoolFileKindOf (implementer)
- 64b73856 test(v6): record the w16c-settle2 seat's runs on windows (implementer)
- f7bee758 fix(daemon): bracket a client spool's unlink in the spool index (fix seat)
- 8f59c41c test(v6): record the w16c-settle2 review fix's runs on windows (fix seat)

### Tests

- `go test -p 2 ./internal/daemon -run '^TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain$' -count=1 -v (before fix)` — FAIL as expected (exit 1): look after unlink served the removed file's heads
- `same row with temporary mutation (hit-side check for in-progress removals removed, not committed)` — FAIL as expected (exit 1)
- `go test -p 2 ./internal/daemon -run '^(TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain|TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered|TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated|TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks|TestSpoolWatch_TheIdleTickKicksItInSpoolSubmode)$' -count=3 -v` — PASS (exit 0) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 ./internal/daemon -count=1 -timeout=30m` — ok 371.761s (exit 0)
- `go vet ./internal/daemon; GOOS=linux go vet ./internal/daemon` — clean
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- None. TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered's call site moved from removed(base) to removing(base)() to follow the API rename; its assertions are unchanged.

### Open issues

- Linux run not made: the container is stopped for this daytime wave.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



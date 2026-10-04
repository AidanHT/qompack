# Wave 21 polish (candidate 8 pre-freeze)

Branch `closeout/w21-polish`. Workflow `wf_792967ff-952`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:polish: status `done`, head `f0457dbc18865ec2a1a06912c91d1dbff7ea481b`

### Root cause

(1) This was a test gap, not a product defect. The shipped clear case already drops goal_turn, but no row looked at goal_turn after a clear, so removing the call went unnoticed. (2) Wave 20 inserted text into wrapped comment paragraphs without re-wrapping them. (3) The release jobs used the moving ubuntu-latest label, which GitHub moves to Ubuntu 26 beginning 2026-10-19. The hosted release evidence was produced on ubuntu-24.04.

### Summary

No product code changed. The only product file touched is internal/daemon/drain.go, and only its comment whitespace. The other changes are tests, workflow runs-on lines, a guard and one doc line. All three items are done in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-polish on closeout/w21-polish, three commits on f905ec9c, working tree clean.

(1) The checkpoint mutant (internal/checkpoint/intent.go:276, `d.setGoalTurnLocked(0, false)` replaced by `_ = 0`). New row TestClearedGoalDropsItsTurnForTheGraphFallback in internal/checkpoint/current_work_test.go. It uses the restored-backup shape of TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal: a draft planted with a derived goal and goal_turn 9, past every record the store lists, where the only record is a slash command. On resume the walk reads every record, finds no goal and clears it. The row asserts (a) no goal_turn is persisted beside the empty goal, then (b) a prompt at turn 2, encoded through the graph fallback while SessionPrompts is degraded, becomes current work. Under the mutant both checks fail: (a) fails with goal_turn=9, and with (a) removed temporarily, (b) fails because the goal stays empty, so each check catches the mutant by itself. It passes on the shipped code.

(2) The drain comments. I re-wrapped only the comments wave 20 edited in place, which I found by diffing 738d67c7..HEAD across drain*.go and spool_watch*.go for added comment lines over 108 columns:
- drain.go:85, the withPassBudget doc (164 columns)
- drain_pass_progress_test.go:319 (126), :358 (117) and :400 (115). The task named :357; the long line is :358, in the same paragraph.
I used a script that asserts the words are identical before and after, and `git diff -w --word-diff` shows only `//` moving. No comment line is over 105 columns, and the 13-character orphan lines a plain reflow at width 104 would have left were avoided. I left drain.go:1750 (130 columns) alone because it dates from 2026-09-12, not wave 20.

(3) The release image pin (D62(h)). `runs-on: ubuntu-24.04` is now set on ci.yml's release-dry-run and on release.yml's release job, which is its only job and needs nothing. I checked the facts against GitHub rather than relying on the existing docs:
- Run 36981590450's release-dry-run (job 110757119491) logged "Image: ubuntu-24.04", version 20260927.320.1.
- Its annotation reads "The ubuntu-latest label will migrate to Ubuntu 26 beginning October 19, 2026 ... runner-images/issues/14748".

The new guard is test/guards/releaserunner_test.go. TestReleaseJobsPinTheirRunnerImage checks the live ci.yml release-dry-run and every job of release.yml, following `needs:` in all three YAML spellings. TestReleaseRunnerGuardRejectsReshapedJobs is the negative test: the moving label, an expression, no runs-on, a runs-on deeper than job level, a missing job, and a needed job left on the label must each fail. Reverting either pin turns the live row red (checked for both files). The pinned value is the named constant releaseRunnerImage, whose comment records where the value came from. I rewrote the docs/release.md risk bullet to say what is pinned, by which guard, what still moves (ci.yml's other Linux jobs and nightly.yml), and that moving the pin needs a green hosted run on the new image first.

Checks, all green:
- go vet with GOOS=windows, linux and darwin on internal/checkpoint, internal/daemon, test/guards and test/docs
- the pinned golangci-lint on the three touched packages
- devtool fmt-check
- devtool lint --only=docmarkers,runpatterns
- full runs of test/guards plus test/docs, internal/checkpoint, and internal/daemon (426.6 s), one at a time, -p 1, output sent to files so exit codes were not masked

No test I added depends on wall-clock timing.

### Commits

- ff82e918 test(checkpoint): pin a cleared goal dropping its goal turn
- b8c2fc08 docs(daemon): re-wrap the drain comments wave 20 edited in place
- f0457dbc ci(release): pin the release jobs to ubuntu-24.04

### Tests

- `go test -p 1 -count=1 -run '^TestClearedGoalDropsItsTurnForTheGraphFallback$' -v ./internal/checkpoint`: PASS on the shipped code; FAIL with intent.go:276 mutated to `_ = 0` (goal_turn is 9); with the wire assertion removed it still fails (goal stays empty instead of the turn-2 prompt's goal)
- `go test -p 1 -count=1 -run '^TestReleaseJobsPinTheirRunnerImage$' -v ./test/guards`: PASS; FAIL with ci.yml release-dry-run reverted to ubuntu-latest, and FAIL with release.yml release reverted
- `go test -p 1 -count=1 -run '^TestReleaseRunnerGuardRejectsReshapedJobs$' -v ./test/guards`: PASS
- `go test -p 1 -count=1 -timeout=30m ./test/guards ./test/docs`: ok (guards 34.990s, docs 3.455s), rc=0
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: ok 98.085s, rc=0
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon`: ok 426.569s, rc=0
- `GOOS={windows,linux,darwin} go vet ./internal/checkpoint ./internal/daemon ./test/guards ./test/docs`: rc=0 on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/checkpoint/... ./internal/daemon/... ./test/guards/...`: no findings, rc=0
- `go run ./tools/devtool fmt-check`: rc=0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers

### Criterion changes

- New gate row TestReleaseJobsPinTheirRunnerImage, with its negative TestReleaseRunnerGuardRejectsReshapedJobs. Reason: D62(h). ubuntu-latest moves to Ubuntu 26 beginning 2026-10-19, and the hosted release evidence came from ubuntu-24.04, so the release jobs must keep running on that image.
- New row TestClearedGoalDropsItsTurnForTheGraphFallback. Reason: no existing row caught the mutant that removes the clear case's setGoalTurnLocked(0, false). No assertion was loosened and no golden file was changed.

### Open issues

- README.md:86 (the CI table) still lists `release-dry-run` on ubuntu-latest, and that is now stale. README.md is not in this workstream's files, and no docs test compares it with the workflows, so it needs a one-word follow-up from its owner.
- marketplace.yml's `pin` job runs on ubuntu-latest after a release is published. It regenerates marketplace.json with the release's generator and requires the bytes to match. It is not a job release.yml needs, and marketplace.yml is outside this workstream's files, so it was left on the moving label. If the coordinator counts it as part of the release path, pinning it means extending the new guard's file list.
- drain.go:1750 is a 130-column comment line from before wave 20 (2026-09-12). It was out of scope (only comments wave 20 edited in place) and left untouched.
- No hosted run has exercised the pinned runs-on lines yet, so the next ci.yml run on this candidate is the first one to show release-dry-run on the explicit ubuntu-24.04 label.

### Needs owner

- releaseRunnerImage = "ubuntu-24.04" (test/guards/releaserunner_test.go) is a new pinned value. It comes from run 36981590450's release-dry-run log (job 110757119491, Image: ubuntu-24.04, version 20260927.320.1). Moving the pin to the Ubuntu 26 image is an owner decision that needs a green hosted run on the new image first, as docs/release.md now says.

## review:polish:r1: verdict `needs-fixes`, 3 finding(s)

- **minor** `internal/daemon/spool_watch_pass_progress_test.go:42 and :81`: Item (2) is incomplete. Wave 20 commit df53a14c also edited two comment paragraphs in place in this drain-pass test file and left short orphan lines in the middle of each paragraph, and those were not re-wrapped. The task asked for 'any other comment wave 20 edited in place' to follow the file's convention, including 'no orphan short lines'. The seat says it diffed spool_watch*.go, but it only looked for lines over 108 columns, so it never checked for short orphan lines.
  - Evidence: Line 42 is '// and its budget then ends it' (30 cols) and is followed by '// inside the spool, which is therefore due again at the next look' (67 cols). Line 81 is '// each pass after the first stopped in the' (43 cols) and is followed by the rest of the sentence. git blame puts lines 40-42 and 80-81 at df53a14c7 'fix(daemon): bound a drain pass that makes no progress', which is not an ancestor of 738d67c7, so it is wave 20. I scanned every comment line wave 20 added in internal/daemon (738d67c7..f905ec9c) for mid-paragraph lines under 75 columns. These two are the only real orphans. The drain.go:65 hit is a line followed by a long identifier, which is fine.
  - Fix: Re-wrap the two paragraphs, at :37-48 (TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff) and :77-84 (…DoesNotStarveTheSpoolsAfterIt), to about 104 columns with the same word-identity check used for drain.go. Then confirm that git diff -w --word-diff shows only '//' moving.
- **minor** `docs/release.md:385-387; test/guards/releaserunner_test.go:17-19; .github/workflows/marketplace.yml:54`: Not every release path is on ubuntu-24.04, and the new text that lists what still moves leaves one out. marketplace.yml's `pin` job runs when a release is promoted to full (`release: types: [released]`). It re-downloads and re-verifies the published zips, regenerates marketplace.json with the release's generator, runs `cmp` against the uploaded file, and opens the pin PR. It is still on ubuntu-latest. A release promoted after 2026-10-19 would run that step on Ubuntu 26. Both the doc bullet and the guard's header comment say 'ci.yml's other Linux jobs and nightly.yml's stay on ubuntu-latest' and do not mention marketplace.yml, so a reader would think the whole release path is pinned.
  - Evidence: marketplace.yml:52-54 reads `pin:` / `if: startsWith(github.event.release.tag_name, 'v') && !github.event.release.prerelease` / `runs-on: ubuntu-latest`. The guard checks only ci.yml release-dry-run and release.yml. The seat raised this as an open issue but did not reflect it in the doc line it rewrote.
  - Fix: The coordinator should pick one. (a) Pin marketplace.yml's pin job to ubuntu-24.04 and add `releaseRunnerProblems(liveYAMLText(.../marketplace.yml), "pin")` to TestReleaseJobsPinTheirRunnerImage. This is preferred because the job takes part in publishing the release. (b) Name marketplace.yml's pin job in the docs/release.md bullet and in the guard comment as deliberately left on ubuntu-latest, with the reason (the generator is deterministic Go on a pinned toolchain, and the job only compares bytes and opens a PR).
- **nit** `README.md:86`: The README CI table still lists `release-dry-run` on ubuntu-latest, which the pin made out of date. No docs test compares this table with the workflows. The seat flagged it as outside its files.
  - Evidence: README.md:86 reads '| `release-dry-run` | ubuntu-latest | the six bundles at the release version ...', but ci.yml:584 is now `runs-on: ubuntu-24.04`.
  - Fix: Change README.md:86 to `ubuntu-24.04` in a follow-up owned by the README's workstream, or give the polish seat that one cell.

## fix:polish:r1: status `done`, head `92cb3a81`

### Root cause

(1) A test gap, not a product defect: the shipped clear case already drops goal_turn, but no row checked goal_turn after a clear. (2) Wave 20 (including df53a14c in spool_watch_pass_progress_test.go) inserted text into wrapped comment paragraphs without re-wrapping them, which left over-long lines and orphan short lines. The first pass scanned only for long lines, so it missed the orphans. (3) The release jobs used the moving ubuntu-latest label, which moves to Ubuntu 26 from 2026-10-19, while the hosted release evidence was produced on ubuntu-24.04. The first pass's doc text left marketplace.yml's pin job out of the list of jobs that still move.

### Summary

No product code changed. The only product file touched in this whole workstream is internal/daemon/drain.go, and only its comment whitespace. Work is in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-polish on closeout/w21-polish: five commits on f905ec9c, working tree clean, head 92cb3a81.

(1) Checkpoint mutant (internal/checkpoint/intent.go:276, `d.setGoalTurnLocked(0, false)` replaced by `_ = 0`). The new row is TestClearedGoalDropsItsTurnForTheGraphFallback in internal/checkpoint/current_work_test.go. It uses the restored-backup shape of TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal: a draft is planted with goal_turn 9, past every listed record, and the only record is a slash command, so on resume the walk clears the goal. The row checks two things:
- no goal_turn is persisted beside the empty goal;
- a turn-2 prompt, encoded through the graph fallback while SessionPrompts is degraded, becomes current work.
Under the mutant each check fails by itself: goal_turn is 9, and the goal stays empty. The row passes on the shipped code.

(2) Drain comments. Wave 20's in-place comment edits are now re-wrapped:
- drain.go:85 (the withPassBudget doc)
- drain_pass_progress_test.go:319, :358 and :400
- (round 1 fix) spool_watch_pass_progress_test.go:42 and :81, two mid-paragraph orphan lines left by df53a14c
A script asserted the words are identical before and after, and `git diff -w --word-diff` shows only `//` moving. No comment line in these files is over 105 columns.

(3) Release image pin (D62(h)). `runs-on: ubuntu-24.04` is now on ci.yml's release-dry-run and on release.yml's release job (its only job, needing none). The guard is test/guards/releaserunner_test.go:
- TestReleaseJobsPinTheirRunnerImage checks the live jobs. Reverting either pin turns it red.
- TestReleaseRunnerGuardRejectsReshapedJobs is the negative test.
- The pinned value is the named constant releaseRunnerImage, with a comment recording where the value came from.
The docs/release.md risk bullet says what is pinned and what still moves. In round 1 it now also names marketplace.yml's `pin` job as left on ubuntu-latest, with the reason, and says a release promoted after 2026-10-19 runs that job on Ubuntu 26. The guard's header comment says the same.

Review resolution:
- Finding 1 (spool_watch_pass_progress_test.go:42 and :81 orphans): CONFIRMED and FIXED in 89e7b76e. git blame puts both at df53a14c, and `git merge-base --is-ancestor df53a14c 738d67c7` returns 1, so it is wave 20. I re-wrapped each paragraph from the orphan line to the end at 104 columns, using a script that refuses any change to the word sequence; the word-diff shows only `//` moving. I also re-scanned every file wave 20 touched in internal/daemon, internal/checkpoint and test/guards for short mid-paragraph comment lines. That found four more:
  - daemon.go:197
  - drain.go:152
  - scheduler_runtime.go:525
  - spool_watch.go:28
  Their commits (2f4867fc7, 218932133, 4201d510f, 0569d34d7) are all ancestors of 738d67c7, so they predate wave 20 and are out of scope. They are listed as an open issue.
- Finding 2 (marketplace.yml `pin` job on ubuntu-latest, missing from the "what still moves" text): CONFIRMED. marketplace.yml:54 is `runs-on: ubuntu-latest`, and the job runs on `release: [released]`. I took option (b) in 92cb3a81, because marketplace.yml is not one of this seat's files (only ci.yml and release.yml runs-on). The docs/release.md bullet and the guard's header comment now name the job as deliberately unpinned. The reason, checked against marketplace.yml:52-114: the job only runs after a full release exists, changes no release asset, re-verifies the served zips against checksums.txt, regenerates marketplace.json and compares bytes, then opens a PR onto develop. So a red there means a re-run or a recorded disposition, not a rebuilt release. Option (a), pinning the job and adding it to TestReleaseJobsPinTheirRunnerImage, is a one-line workflow edit plus a one-line guard call. It is left to the coordinator (see needs_owner).
- Finding 3 (README.md:86 still says ubuntu-latest for release-dry-run): CONFIRMED but not fixed here, because README.md is outside this seat's files. It remains an open issue for the README owner. The fix is changing that one cell to `ubuntu-24.04`.

Checks, all green after the round 1 changes:
- GOOS=windows, linux and darwin go vet on internal/daemon, test/guards and test/docs
- the pinned golangci-lint on internal/daemon and test/guards
- devtool fmt-check
- devtool lint --only=docmarkers,runpatterns (both PASS)
- go test ./test/guards ./test/docs
- a full internal/daemon run (450.2 s, rc=0)
Every test ran with -p 1, one process at a time, with output sent to files so exit codes were not masked. internal/checkpoint was not touched in round 1, so its earlier full run (98.1 s, ok) still stands. No test added in this workstream depends on wall-clock timing.

### Commits

- ff82e918 test(checkpoint): pin a cleared goal dropping its goal turn
- b8c2fc08 docs(daemon): re-wrap the drain comments wave 20 edited in place
- f0457dbc ci(release): pin the release jobs to ubuntu-24.04
- 89e7b76e docs(daemon): re-wrap the spool-watch comments wave 20 split
- 92cb3a81 docs(release): name marketplace.yml's pin job as left unpinned

### Tests

- `go test -p 1 -count=1 -run '^TestClearedGoalDropsItsTurnForTheGraphFallback$' -v ./internal/checkpoint`: PASS on the shipped code; FAIL with intent.go:276 mutated to `_ = 0` (goal_turn is 9); with the persistence assertion removed it still fails (the goal stays empty instead of taking the turn-2 prompt's goal)
- `go test -p 1 -count=1 -run '^TestReleaseJobsPinTheirRunnerImage$' -v ./test/guards`: PASS; FAIL with ci.yml release-dry-run reverted to ubuntu-latest, and FAIL with release.yml release reverted (round 0)
- `go test -p 1 -count=1 -run '^TestReleaseRunnerGuardRejectsReshapedJobs$' -v ./test/guards`: PASS (round 0)
- `go test -p 1 -count=1 -timeout=30m ./test/guards ./test/docs`: round 1: ok (guards 41.248s, docs 5.763s), rc=0
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon`: round 1: ok 450.197s, rc=0
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: round 0: ok 98.085s, rc=0 (package not touched in round 1)
- `GOOS={windows,linux,darwin} go vet ./internal/daemon ./test/guards ./test/docs`: round 1: rc=0 on all three (round 0 also covered ./internal/checkpoint)
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/... ./test/guards/...`: round 1: no findings, rc=0
- `go run ./tools/devtool fmt-check`: round 1: rc=0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: round 1: PASS runpatterns, PASS docmarkers

### Criterion changes

- New gate row TestReleaseJobsPinTheirRunnerImage, with its negative TestReleaseRunnerGuardRejectsReshapedJobs. Reason: D62(h). ubuntu-latest moves to Ubuntu 26 from 2026-10-19, and the hosted release evidence came from ubuntu-24.04.
- New row TestClearedGoalDropsItsTurnForTheGraphFallback. Reason: no existing row caught the mutant that removes the clear case's setGoalTurnLocked(0, false).
- Round 1 changed no check: comment re-wraps that leave the words identical, plus doc and guard-comment prose naming marketplace.yml's pin job as deliberately unpinned. No assertion was loosened and no golden file was changed.

### Open issues

- README.md:86 (the CI table) still lists `release-dry-run` on ubuntu-latest, which is now out of date (ci.yml has `runs-on: ubuntu-24.04`). README.md is outside this workstream's files, and no docs test compares the table with the workflows. The README owner needs to change that one cell to `ubuntu-24.04`.
- marketplace.yml's `pin` job stays on ubuntu-latest. It is now documented as deliberately unpinned, with the reason, in docs/release.md and in the guard's header comment. A full release promoted after 2026-10-19 will run that job on Ubuntu 26 for the first time.
- These comment lines predate wave 20 and were left untouched as out of scope: drain.go:1750 is 130 columns, and there are short mid-paragraph orphan lines at daemon.go:197, drain.go:152, scheduler_runtime.go:525 and spool_watch.go:28 (from commits 2f4867fc7, 218932133, 4201d510f and 0569d34d7, all ancestors of 738d67c7).
- No hosted run has exercised the pinned runs-on lines yet. The next ci.yml run on this candidate will be the first to show release-dry-run on the explicit ubuntu-24.04 label.

### Needs owner

- releaseRunnerImage = "ubuntu-24.04" (test/guards/releaserunner_test.go) is a new pinned value. It comes from run 36981590450's release-dry-run log (job 110757119491, Image: ubuntu-24.04, version 20260927.320.1). Moving it to the Ubuntu 26 image needs a green hosted run on that image first, as docs/release.md says.
- Coordinator choice on review finding 2. This round documented marketplace.yml's pin job as deliberately left on ubuntu-latest (option b). Option (a) would set marketplace.yml:54 to `runs-on: ubuntu-24.04` and add releaseRunnerProblems(liveYAMLText(.../marketplace.yml), "pin") to TestReleaseJobsPinTheirRunnerImage. It was not done because marketplace.yml is outside this seat's files. If (a) is chosen, the docs/release.md bullet and the guard header sentences added in 92cb3a81 must be reverted too.

## review:polish:r2: verdict `sound`, 1 finding(s)

- **nit** `README.md:86`: Carried from round 1 and still open, outside this seat's files. The README CI table still lists release-dry-run on ubuntu-latest, but ci.yml:584 is now runs-on: ubuntu-24.04. A grep of every non-plans .md file finds no other stale mention of the release jobs' runner.
  - Evidence: README.md:86 reads '| `release-dry-run` | ubuntu-latest | the six bundles at the release version ...'. ci.yml:584 reads 'runs-on: ubuntu-24.04'. The seat listed this under open_issues.
  - Fix: In the README owner's follow-up, change that one cell to `ubuntu-24.04`. Otherwise give the polish seat that one cell. No guard change is needed.

## fix:polish:r2: status `done`, head `92cb3a81`

### Root cause

(1) A gap in the tests, not a product defect. The shipped clear case already drops goal_turn, but no row checked goal_turn after a clear. (2) Wave 20, including df53a14c, inserted text into wrapped comment paragraphs without re-wrapping them. (3) The release jobs used the moving ubuntu-latest label, but the hosted release evidence came from ubuntu-24.04. The README CI table is a separate copy of the runner labels that no test compares with the workflows, so the pin left README.md:86 stale.

### Summary

No product code changed. In this workstream the only product file touched is internal/daemon/drain.go, and only the whitespace in its comments. The work is in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-polish on branch closeout/w21-polish: five commits on top of f905ec9c, head 92cb3a81, working tree clean (`git status --short` printed nothing at the start of this round). Round 2 made no commits: the only finding is about a file this seat does not own.

(1) Checkpoint mutant (internal/checkpoint/intent.go:276; the mutant replaces `d.setGoalTurnLocked(0, false)` with `_ = 0`). The new row is TestClearedGoalDropsItsTurnForTheGraphFallback in internal/checkpoint/current_work_test.go. It reuses the restored-backup shape of TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal:
- A draft is planted with goal_turn 9, which is past every listed record.
- The only record is a slash command, so on resume the walk clears the goal.

The row checks two things:
- No goal_turn is persisted next to the empty goal.
- A turn-2 prompt, encoded through the graph fallback while SessionPrompts is degraded, becomes the current work.

Under the mutant each check fails on its own: goal_turn reads 9, and the goal stays empty. The row passes on the shipped code.

(2) Drain comments. Wave 20's in-place comment edits are re-wrapped, with no wording change:
- drain.go:85 (the withPassBudget doc)
- drain_pass_progress_test.go:319, :358 and :400
- spool_watch_pass_progress_test.go:42 and :81 (two orphan lines from df53a14c, fixed in round 1)

A script confirmed the word sequence is identical before and after, and `git diff -w --word-diff` shows only `//` moving. No comment line in these files is over 105 columns.

(3) Release image pin (D62(h)). `runs-on: ubuntu-24.04` is now set on ci.yml's release-dry-run job (ci.yml:584) and on release.yml's release job. That is release.yml's only job, and it needs no other job. The guard is test/guards/releaserunner_test.go:
- TestReleaseJobsPinTheirRunnerImage checks the live jobs. Reverting either pin turns it red.
- TestReleaseRunnerGuardRejectsReshapedJobs is the negative test.
- The pinned value is the named constant releaseRunnerImage, and a comment records where it came from.

The risk bullet in docs/release.md says what is pinned and what still moves. It names marketplace.yml's `pin` job as deliberately left on ubuntu-latest and gives the reason.

Review resolution (round 2):
- Finding 1 (nit, README.md:86, the CI table lists release-dry-run on ubuntu-latest): CONFIRMED, NOT FIXED here. README.md:86 does read "| `release-dry-run` | ubuntu-latest | ...", while ci.yml:584 reads `runs-on: ubuntu-24.04`. I grepped every .md outside plans/ for release-dry-run or ubuntu-latest. The only other hits are:
  - docs/adr/0008 and 0010, which describe historical runs, bench-gate's matrix and the co-load declaration, not the release job's runner;
  - docs/release-notes/v0.3.0.md, a past release's notes, so correct as written;
  - docs/release.md, which is already updated;
  - README.md:78, the verify row, which is correctly ubuntu-latest because verify is not pinned.

  So README.md:86 is the only stale cell. This seat's files are internal/checkpoint (the one row), the drain comments in internal/daemon, the runs-on lines in ci.yml and release.yml, test/guards and that one docs/release.md line. README.md is not among them, and the hard rules forbid editing outside them, so I made no edit. The fix is to change that one cell from `ubuntu-latest` to `ubuntu-24.04`, with no guard change. It is listed under needs_owner for the coordinator to either give the cell to this seat or route it to the README owner.

Round 1 resolution (unchanged):
- The spool_watch_pass_progress_test.go orphans were fixed in 89e7b76e.
- The marketplace.yml `pin` job is documented as deliberately unpinned (option b) in 92cb3a81.
- The README cell was carried forward as above.

Checks: nothing changed in round 2, so the round 1 results still stand and all are green:
- GOOS=windows, linux and darwin go vet
- the pinned golangci-lint on internal/daemon and test/guards
- devtool fmt-check
- devtool lint --only=docmarkers,runpatterns
- go test ./test/guards ./test/docs
- a full internal/daemon run (450.2 s)
- a full internal/checkpoint run (98.1 s, round 0; the package was not touched after that)

Every run used -p 1, one go test process at a time, with output written to files so exit codes were not masked. No test added in this workstream depends on wall-clock timing.

### Commits

- ff82e918 test(checkpoint): pin a cleared goal dropping its goal turn
- b8c2fc08 docs(daemon): re-wrap the drain comments wave 20 edited in place
- f0457dbc ci(release): pin the release jobs to ubuntu-24.04
- 89e7b76e docs(daemon): re-wrap the spool-watch comments wave 20 split
- 92cb3a81 docs(release): name marketplace.yml's pin job as left unpinned

### Tests

- `go test -p 1 -count=1 -run '^TestClearedGoalDropsItsTurnForTheGraphFallback$' -v ./internal/checkpoint`: PASS on the shipped code. FAIL with intent.go:276 mutated to `_ = 0` (goal_turn is 9). With the persistence assertion removed it still fails, because the goal stays empty instead of taking the turn-2 prompt's goal (round 0)
- `go test -p 1 -count=1 -run '^TestReleaseJobsPinTheirRunnerImage$' -v ./test/guards`: PASS. FAIL with ci.yml release-dry-run reverted to ubuntu-latest, and FAIL with release.yml release reverted (round 0)
- `go test -p 1 -count=1 -run '^TestReleaseRunnerGuardRejectsReshapedJobs$' -v ./test/guards`: PASS (round 0)
- `go test -p 1 -count=1 -timeout=30m ./test/guards ./test/docs`: round 1: ok (guards 41.248s, docs 5.763s), rc=0. No change since.
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon`: round 1: ok 450.197s, rc=0. No change since.
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: round 0: ok 98.085s, rc=0. Package not touched since.
- `GOOS={windows,linux,darwin} go vet ./internal/daemon ./test/guards ./test/docs`: round 1: rc=0 on all three (round 0 also covered ./internal/checkpoint)
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon/... ./test/guards/...`: round 1: no findings, rc=0
- `go run ./tools/devtool fmt-check`: round 1: rc=0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: round 1: PASS runpatterns, PASS docmarkers

### Criterion changes

- New gate row TestReleaseJobsPinTheirRunnerImage, with its negative test TestReleaseRunnerGuardRejectsReshapedJobs. Reason: D62(h). ubuntu-latest moves to Ubuntu 26 from 2026-10-19, and the hosted release evidence came from ubuntu-24.04.
- New row TestClearedGoalDropsItsTurnForTheGraphFallback. Reason: no existing row caught the mutant that removes the clear case's setGoalTurnLocked(0, false).
- Rounds 1 and 2 changed no check. Round 1 re-wrapped comments with the words unchanged and added doc and guard-comment prose. Round 2 made no edits.

### Open issues

- README.md:86, a cell in the CI table, still lists `release-dry-run` on ubuntu-latest, but ci.yml:584 is `runs-on: ubuntu-24.04`. Confirmed in round 2. README.md is outside this seat's files, and no docs test compares the table with the workflows. The fix is one cell: `ubuntu-latest` to `ubuntu-24.04`.
- marketplace.yml's `pin` job stays on ubuntu-latest. docs/release.md and the guard's header comment document it as deliberately unpinned and give the reason. A full release promoted after 2026-10-19 will run that job on Ubuntu 26 for the first time.
- Some comment problems predate wave 20 and were left alone as out of scope. drain.go:1750 is 130 columns. There are short orphan lines in the middle of paragraphs at daemon.go:197, drain.go:152, scheduler_runtime.go:525 and spool_watch.go:28. They come from commits 2f4867fc7, 218932133, 4201d510f and 0569d34d7, all ancestors of 738d67c7.
- No hosted run has exercised the pinned runs-on lines yet. The next ci.yml run on this candidate will be the first to show release-dry-run on the explicit ubuntu-24.04 label.

### Needs owner

- releaseRunnerImage = "ubuntu-24.04" (test/guards/releaserunner_test.go) is a new pinned value. It comes from the release-dry-run log of run 36981590450 (job 110757119491, Image: ubuntu-24.04, version 20260927.320.1). As docs/release.md says, moving it to the Ubuntu 26 image needs a green hosted run on that image first.
- Who fixes README.md:86 (review round 2): either give this seat that one cell or route it to the README owner. The edit is `ubuntu-latest` to `ubuntu-24.04` in the release-dry-run row, with no guard change.
- Coordinator choice on round 1 finding 2. Option (b), the current state, documents marketplace.yml's pin job as deliberately left on ubuntu-latest. Option (a) would set marketplace.yml:54 to `runs-on: ubuntu-24.04`, add releaseRunnerProblems(liveYAMLText(.../marketplace.yml), "pin") to TestReleaseJobsPinTheirRunnerImage, and revert the sentences 92cb3a81 added to docs/release.md and to the guard's header comment.

## verify:polish: verdict `needs-fixes`, 1 finding(s)

- **nit** `README.md:86 (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-polish/README.md)`: This is still open from review rounds 1 and 2. The README CI table still lists release-dry-run on ubuntu-latest, but this workstream's pin moved that job to ubuntu-24.04. The seat was right to leave it alone because README.md is outside its files, and it did not argue the finding away. So the doc copy of the runner label is still wrong at head 92cb3a81.
  - Evidence: At HEAD, README.md:86 reads '| `release-dry-run` | ubuntu-latest | the six bundles at the release version ...'. ci.yml:584 reads 'runs-on: ubuntu-24.04' (commit f0457dbc). No test compares that table with the workflows, so nothing turns red.
  - Fix: Have the coordinator either give the polish seat this one cell or send it to the README owner. The change is `ubuntu-latest` to `ubuntu-24.04` in the release-dry-run row only, and no guard needs to change. Everything else checked out. (1) The new row TestClearedGoalDropsItsTurnForTheGraphFallback passes on the shipped code and fails when intent.go:276 is mutated to `_ = 0` (checked read-only with a go test -overlay: 'Expected nil, but got: *core.TurnIndex'). (2) The internal/daemon diff touches comments only, and the word sequence is identical before and after (416 words). (3) runs-on is pinned in ci.yml release-dry-run and release.yml release, and TestReleaseJobsPinTheirRunnerImage plus its negative test are sound. The marketplace.yml pin job is documented as a deliberate choice (option b). go vet, fmt-check and go test on ./test/guards, ./test/docs and ./internal/checkpoint are all green. I did not re-run the full ./internal/daemon suite here; the seat's round 1 run was green, and the daemon change is comments only.


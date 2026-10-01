# W16C-TIDY: hostperm escaped-bracket fail-open, stale lane texts, test nits

Branch `closeout/w16c-tidy`. Workflow `wf_5e8aadd4-6ae`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

**Coordinator notes.** (1) The review's nit on plans/00-ARCHITECTURE.md (the example still carried the old "cache-aware ... compaction" description) is closed by the coordinator's `ec44260a`, which shows the shipped description. (2) plans/sdd/V6-closeout/w16b-cirec/report.md repeats, in its needs_owner text, that nightly's -race run of internal/daemon "alone" took 1203 s. As the corrected log line (`46ccd3b6`) says, that 1203 s is internal/daemon's time inside nightly's whole-tree -race run; the w16b report is kept verbatim and this note corrects it.

## Implementer — status `done`, head `e90c69da33ded3e850133f8362810eda739130c9`

### Root cause

(1) internal/hostperm/policy.go shortNameRe's class alternative `\[[^\]]*~` stopped at the first `]`, including an escaped `\]`. In a pattern such as CREDEN[\]~]1.SEC, path.Match reads one class holding `]` and `~`, so the pattern matches CREDEN~1.SEC. The regex saw no tilde inside a class, and the tilde outside one was followed by `]`, so shortShaped returned false. prefixRow then skipped the 8.3 alt, and a deleted file was judged on its long name. That is a fail-open. (3) The waiver notes and comments were written before D53(e)/D55 made every hosted isolation lane report B-A, B-B and B-E's wall row under QOMPACK_NONREFERENCE_DISK, so they still named those lanes as enforcing the limits. Items (2), (4), (5) and (6) were stale text or a mis-sized constant, not product defects.

### Summary

Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16c-tidy, branch closeout/w16c-tidy, 6 commits on ae601390. The working tree is clean.

(1) PRIVACY FAIL-OPEN, FIXED (0a022893). I wrote the failing tests first. I added these rows to TestShortShaped_NamesAnEightDotThreeNameOnlyByShape: `creden[\]~]1.sec`, `creden[\]-~]1.sec`, `creden[\~]1.sec` and `creden\~1.sec`. I added `Read(**/CREDEN[\]~]1.SEC)` to the 8.3-shaped rows of TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles; the existing deny() helper quotes the backslash for JSON. Before the fix the probe failed for creden[\]-~]1.sec (shortShaped false), and the seam row failed: the deleted file came back Allow (effect 0) where Deny (2) was expected. The fix is shortNameRe = `~[0-9*?\[\\]|\[(?:\\.|[^\]\\])*\\?~`, the verifier's candidate: the class alternative now steps over escapes.

I probed it (scratch .../scratchpad/w16c/tidy/probe/main.go, a temporary diagnostic, not committed). All six required spellings are shaped and match CREDEN~1.SEC under path.Match: creden[\]~]1.sec, creden[\]-~]1.sec, creden[~]1.sec, creden[}-~]1.sec, creden[\~]1.sec and creden\~1.sec. The old regex missed only the first two. *~, *.txt~ and *.[ch]~ are not shaped. I also ran an exhaustive search over every pattern of up to 7 characters drawn from {~ 1 [ ] \ - ^ * ? } a} that contains a tilde, checked with path.Match against x~1, ~1, x~12 and ~12. It found 0 patterns that match an ~<digit> name and are not shaped.

The remaining cases with no tilde at all are left as the task said: CREDEN?1.SEC, CREDEN[^a]1.SEC, and a range whose upper end is past 0x7E, such as [!-<non-ASCII>]. The new comment names this case explicitly and no longer overclaims. It says any tilde in a class is shaped whatever follows the class, which fails closed.

(2) DONE (ac670215). I dropped "cache" from the plugin.json example in plans/00-ARCHITECTURE.md. The document keeps inline "amended" notes rather than a revision log, so I added a "Keywords note (amended 2026-10-01, D53(e))" under the example.

(3) DONE (ec1ce53d). In test/bench/hotpath/report.go, baWallWaivedNote and bbWallWaivedNote now say the limit is still enforced "by every run that does NOT pass --under-coload, on a reference disk (the owner's quiet runs) — <lanes>; hosted runs of these lanes report the row under QOMPACK_NONREFERENCE_DISK (ADR 0010 Addendum 2)". The env name comes from obs.NonReferenceDiskEnv. I also qualified beWallWaivedNote, because it had the same stale claim ("bench-gate and nightly", now "bench-gate and nightly bench-deep"). The doc comments of all three were updated.

report_test.go has a new helper, requireHostedLanesReport, called from TestBEWallWaivedNote_NamesTheLimitItDidNotApply, TestBAWallWaivedNote_NamesTheLimitItDidNotApply and TestBBWallWaivedNote_NamesTheLimitItDidNotApply. It pins both new clauses. Every existing assertion is kept, including the pinned lane loop. All three tests failed against the old report.go and pass now. I also qualified the comments in main.go (two places), in test/integration/hotpath_test.go (two places, comments only), and the internal/obs/nonrefdisk.go doc, which now names D55, B-A's inclusion (D41) and nightly bench-deep. The note openings that test/integration and test/e2e match on are unchanged. No gate, limit or reported row changed.

(4) DONE (46ccd3b6). Only line 43 of plans/sdd/V6-closeout/w16-ci/runs/fix-daemon-count2-windows.log changed. It now reads "(internal/daemon took 1203 s inside nightly's whole-tree -race run there)".

(5) DONE (57fcc032). I renamed the test to TestDsealParsers_RefuseAnOversizedRunOfSpaces and kept its body. The comment says it does not pin the bound: json.Unmarshal refuses a run of spaces anyway, and no oversized input can pass the canonical re-encoding check. dsealMaxLine is a cost guard, and the row only shows that the oversized path returns false. No committed report quoted the old name.

(6) DONE (e90c69da). demand_compact_edge_test.go now uses bufio.MaxScanTokenSize+1. I checked that CompactDemandLog's scanners (phase7maint.go:165 and :236) are sized by bufio.MaxScanTokenSize.

Criterion changes: none weakened. The only test change that tightens anything is the added pins in (3), and the rows added in (1). The rename in (5) changes what the test claims, not what it asserts.

### Commits

- 0a022893 fix(hostperm): shape a class that escapes a bracket before its tilde
- ac670215 docs(plans): drop the cache keyword from the manifest example
- ec1ce53d fix(hotpath): qualify the waiver notes' lanes as reference-disk only
- 46ccd3b6 docs(v6): place the 1203 s daemon figure inside nightly's whole tree
- 57fcc032 test(store): stop claiming the dseal size bound is pinned
- e90c69da test(store): size the overlong demand line from the scanner's bound

### Tests

- `go test -p 2 -count=1 -run 'TestShortShaped_NamesAnEightDotThreeNameOnlyByShape|TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles' ./internal/hostperm (before fix)` — FAIL as expected: shortShaped("creden[\\]-~]1.sec") false; seam subtest Read(**/CREDEN[\]~]1.SEC) got Allow for the deleted file
- `go test -p 2 -count=1 -run 'TestShortShaped_NamesAnEightDotThreeNameOnlyByShape|TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles' -v ./internal/hostperm (after fix)` — PASS, all 10 seam subtests including Read(**/CREDEN[\]~]1.SEC)
- `go test -p 2 -count=1 -run 'TestBEWallWaivedNote_NamesTheLimitItDidNotApply|TestBAWallWaivedNote_NamesTheLimitItDidNotApply|TestBBWallWaivedNote_NamesTheLimitItDidNotApply' ./test/bench/hotpath` — FAIL x3 with the old report.go stashed, PASS with the new one
- `go test -p 2 -count=1 -run 'TestDsealParsers_RefuseAnOversizedRunOfSpaces|TestCompactDemandLog_ALogItCannotReadToTheEndIsLeftAlone' -v ./internal/store` — PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/hostperm` — ok 3.0s, exit 0
- `go test -p 2 -count=1 -timeout=30m ./internal/obs` — ok 1.4s, exit 0
- `go test -p 2 -count=1 -timeout=30m ./test/bench/hotpath` — ok 15.4s, exit 0
- `go test -p 2 -count=1 -timeout=30m ./test/docs` — ok 5.6s, exit 0
- `go test -p 2 -count=1 -timeout=30m ./internal/store` — ok 295.9s, exit 0
- `go vet ./internal/hostperm ./internal/obs ./test/bench/hotpath ./internal/store ./test/integration (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all sub-checks PASS
- `go run main.go (scratch probe of path.Match vs the candidate regex, temporary diagnostic)` — all 9 listed spellings as required; exhaustive search of tilde patterns up to 7 chars: 0 open

### Criterion changes

- TestDsealParsers_RefuseAnOversizedDocument renamed to TestDsealParsers_RefuseAnOversizedRunOfSpaces. Same body, no assertion removed. The rationale, as ordered by the task: the row cannot observe dsealMaxLine, because the input fails json.Unmarshal and the canonical check regardless, so the old name overclaimed. The comment now calls the bound a cost guard.
- TestBEWallWaivedNote_NamesTheLimitItDidNotApply, TestBAWallWaivedNote_NamesTheLimitItDidNotApply and TestBBWallWaivedNote_NamesTheLimitItDidNotApply each gained two pins (via requireHostedLanesReport): the reference-disk qualifier and the hosted QOMPACK_NONREFERENCE_DISK clause. This strengthens them; nothing was removed.

### Open issues

- Not run on Linux: the container is stopped by this seat's limits. rules_test.go's TestShortShaped_NamesAnEightDotThreeNameOnlyByShape is cross-platform and passes on Windows. GOOS=linux go vet is clean.
- Still accepted and out of scope: segments with no tilde that path.Match lets match an 8.3 name (CREDEN?1.SEC, CREDEN[^a]1.SEC, a class range whose upper end is past 0x7E). shortNameRe's comment now names this case.
- plans/sdd/V6-closeout/w16b-cirec/report.md:169 (needs_owner text) repeats the misstatement 'nightly's -race run of the package alone took 1203 s'. Only the log line was in scope; the coordinator may want to correct the report too.
- beWallWaivedNote's lane list names only bench-gate and nightly bench-deep, although timing and test-e2e (X11) also judge B-E's wall row. I qualified it as reference-disk only and did not widen the list.

## Independent review

### review:tidy: needs-fixes

- **minor** `test/integration/hotpath_test.go:1228-1230` — Item (3) is incomplete in this comment block. The implementer qualified the 'gated arm' sentence at 1235-1239, but the bullet just above it still says the not-co-loaded mode ('ci.yml's `timing` job, which runs this test alone; bench-gate's shape') has 'all three gated at their obs.Budgets() limits, pass=true, p99 under the limit'. On a hosted runner, the timing job sets QOMPACK_NONREFERENCE_DISK, and the nonrefDisk branch a few lines below then reports B-A, B-B and B-E's wall row. So this bullet makes the same stale claim the task told the seat to qualify. The block names only two modes (co-loaded / not co-loaded) and never the third, the non-reference disk, which the code at 1247ff handles.
  - Evidence: sed -n 1220,1250p at HEAD: '//   - not co-loaded (ci.yml's `timing` job, which runs this test alone; bench-gate's shape): //     all three gated at their obs.Budgets() limits, pass=true ...', followed by code `if nonrefDisk { for _, mark := range []string{hotpathNonrefDiskBAMark, hotpathNonrefDiskBBMark, hotpathNonrefDiskBEMark} ...`. The diff touches only lines 1235-1239 of this block.
  - Fix: Qualify the bullet as 'not co-loaded, on a reference disk (the owner's quiet runs; `timing`'s isolation shape)'. Either add a third bullet that says hosted runs of these lanes set QOMPACK_NONREFERENCE_DISK, so all three rows come back REPORTED with the non-reference-disk notes (ADR 0010 Addendum 2), or point to the nonrefDisk branch below. This is a comment-only change, so no test run is needed.
- **nit** `plans/00-ARCHITECTURE.md:823-832` — The new Keywords note says 'cache' was dropped 'because no supported feature backs it'. Two lines above, the same example still has the description "Cache-aware, retrieval-backed context compaction". That claim has the same problem, and the shipped manifest no longer uses that description: pluginmanifest.Description is 'Qompack keeps a local record of the session and restores the important ...'. The example still drifts from the shipped manifest on the very point the note is about.
  - Evidence: plans/00-ARCHITECTURE.md:823 `"description": "Cache-aware, retrieval-backed context compaction",`; internal/pluginmanifest/manifest.go:71 `const Description = "Qompack keeps a local record of the session and restores the important " + ...`
  - Fix: Narrow the note to the keyword only, or add a sentence saying the description (and the homepage) are illustrative and that pluginmanifest.Default is the source of truth. Alternatively, sync the description too. Changing the description goes slightly beyond item (2), so recording it as known drift is acceptable.
- **nit** `test/bench/hotpath/main.go:245; test/bench/hotpath/report.go:203` — Two of the reworded comment lines were not re-wrapped. Each is about 130-150 columns, while the surrounding paragraphs wrap at about 100. In report.go, the baWallWaivedNote doc also leaves a ragged short line ('Addendum 2). B-A is the daemon-observed hook_controlled estimate'). fmt-check and lint pass, so this is cosmetic only.
  - Evidence: Added comment lines longer than 102 characters: '// TestColoadYieldersAreJudgedInIsolation mechanically prevents that coverage from disappearing. internal/obs/budgets.go's B-B entry' (main.go), and '// lanes report the row under QOMPACK_NONREFERENCE_DISK (ADR 0010 Addendum 2). It also names what still holds in THIS run, because the honest answer for' (report.go bbWallWaivedNote doc).
  - Fix: Re-wrap the three edited doc paragraphs to the file's ~100-column width. Comment text only.

## Fix seat (review resolution) — status `done`, head `80f4ce45`

### Root cause

The implementer qualified only the gated-arm sentence of the hotpath B-row comment. The comment block's model of the modes (co-loaded / not co-loaded) never named the non-reference-disk mode that line 1104 folds into `waived`, so the not-co-loaded bullet kept the stale claim that hosted lanes gate the rows.

### Summary

I was the fix seat for w16c-tidy, on branch closeout/w16c-tidy in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16c-tidy. The implementer's six commits (0a022893..e90c69da) cover items 1-6, and I left them as they were. The reviewer raised one minor finding. I checked it, agreed with it and fixed it in one comment-only commit, 80f4ce45. HEAD is now 80f4ce45 and the working tree is clean.

## Review resolution
- The reviewer said the comment at test/integration/hotpath_test.go:1228-1230 still overclaims. **Confirmed and fixed.** The block named only two modes, co-loaded and not co-loaded. Its not-co-loaded bullet still said that `timing` and bench-gate gate B-A, B-B and B-E's wall-clock row at their obs.Budgets() limits. But the code at line 1104 sets `waived := underCoload || nonrefDisk`. So when a hosted run sets QOMPACK_NONREFERENCE_DISK, the gated arm (`if !waived { hotpathRequireGatedRow ... }`) is skipped and the `if nonrefDisk` branch requires one non-reference-disk note per row instead. The implementer had only qualified the sentence at lines 1235-1239. Changes in 80f4ce45:
  - The lead sentence now names a non-reference disk alongside a co-loaded host.
  - A new bullet describes the non-reference-disk mode: every hosted run of `timing`, `test-e2e`, bench-gate and nightly bench-deep sets the variable (ADR 0010 Addendum 2); all three rows come back REPORTED; there is one disclosure note per row, and B-E's note names the row that still enforces the limit. The bullet points to the nonrefDisk branch below it.
  - The gated bullet now reads "neither (not co-loaded, on a reference disk: the owner's quiet runs, in `timing`'s isolation shape ...)". It also says that both kinds of waiver note must be ABSENT, which is what the else-branches assert.
  - No code, assertion, gate, limit or row changed.

## Commands and results (fix seat)
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet -tags integration ./test/integration/`: exit 0 on Windows and under GOOS=linux.
- `go test -count=1 -run '^$' ./test/integration/`: ok, 0.374s, "no tests to run". This was a compile check only. I ran no test because the change is a comment, and the seat's limits forbid hot-path rows and the whole integration package.
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0, PASS. runpatterns resolved 1415 of 1415 checkable patterns.

## Criterion changes
None. No check was weakened.

## Open items / needs_owner
- No owner items: the implementer carried none forward (`[]`), and this fix adds no new budget or bound number.

### Commits

- 0a022893 fix(hostperm): shape a class that escapes a bracket before its tilde
- ac670215 docs(plans): drop the cache keyword from the manifest example
- ec1ce53d fix(hotpath): qualify the waiver notes' lanes as reference-disk only
- 46ccd3b6 docs(v6): place the 1203 s daemon figure inside nightly's whole tree
- 57fcc032 test(store): stop claiming the dseal size bound is pinned
- e90c69da test(store): size the overlong demand line from the scanner's bound
- 80f4ce45 docs(hotpath): name the non-reference-disk mode in the B-row block (fix seat)

### Tests

- `go run ./tools/devtool fmt-check` — exit 0
- `go vet -tags integration ./test/integration/ (Windows and GOOS=linux)` — exit 0 both
- `go test -count=1 -run '^$' ./test/integration/` — ok (compile only; comment-only change, no rows run per seat limits)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS, exit 0

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



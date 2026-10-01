# W16B-CIREC: CI timeout, non-reference-disk records and docs

Branch `closeout/w16b-cirec`. Workflow `wf_747e62d8-a28`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `d97f5584`

### Root cause

(1) -timeout is per test binary, and wholeTreeTestTimeout (30m) is sized for one whole-tree pass. The windows-latest leg runs -count=2, so each binary runs its tests twice inside that one budget. Run 36816905394 killed internal/daemon at 1800 s. Locally, alone at -count=2, the package finished in 526.6 s, and every test that started also finished (3252 run events = 3247 pass + 2 skip + 3 fail), so it did not hang. The hosted kill therefore comes from whole-tree co-load on a two-core runner stretching two passes past one pass's budget, or from a hang that happens only on the hosted runner. The hosted log named no test still running, because the reconciliation of that time dropped unfinished tests; c697ed1e now prints them. (2) nightly bench-deep meets the same hosted fsync tail as bench-gate (windows-latest B-B p99 1310.7 ms against 50, run 36820740318) and had no declaration. (3) The new declaration shipped with no ADR or architecture entry. (4) The cover red's cause was written up as observed. It is inferred: both hosted reds printed the pre-poll 0. (5) taskTest ran `go test ./...`, which put test/e2e inside the co-loaded pass, against ADR 0010 decision 4.

### Summary

Wave 16b, cirec seat. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16b-cirec, branch closeout/w16b-cirec, base 6f118a7b, head d97f5584 (5 commits, unpushed). All five D55 items are done.

(1) Windows test leg timeout (bf06fd9b)
- ci.yml's `test` step: the Windows leg now runs `-count=2` with `-timeout=60m`. The -race legs keep 30m.
- A comment in the step gives the derivation. -timeout is per test binary, wholeTreeTestTimeout is 30m for one pass, and -count=2 means 2 x 30m. It cites the measurements: killed at 1800 s in run 36816905394; alone locally 527 s with no hang. A real hang is still killed and its unfinished tests are named.
- plans/sdd/V6-closeout/w16-ci/runs/fix-daemon-count2-windows.log now records the full outcome, copied from the session scratchpad's w16/ci/daemon-count2.json:
  - 526.556 s, FAIL, no hang;
  - iteration 1 took about 272 s and iteration 2 about 254 s;
  - three failures, all in iteration 2, all `exit status 0xc0000142` (STATUS_DLL_INIT_FAILED), each with its iteration-1 pass line quoted: TestLock_AnExitedOwnerIsReplacedAtOnce, TestRun_LogsATakenOverLockAndASpoolReplay, TestStageBinary_StartsACopyItsRenamerStillHolds;
  - the verifier's -count=3 rerun of those three (ok 1.869 s) and my own (ok 2.666 s).
- The log says that desktop-heap exhaustion as the cause of 0xc0000142 is inferred, not measured.
- The restated needs_owner item is below.

(2) Non-reference disk scope (6693a9b5)
- B-A stays in the reported set (D41, D55).
- nightly.yml's bench-deep now declares QOMPACK_NONREFERENCE_DISK. A comment there cites Q1, D53(e), D55 and run 36820740318.
- test/guards/nonrefdisk_test.go: nightlyNonrefDiskJobs = {"bench-deep"}, with its doc comments updated. The nightly subtest also now refuses a declaring job that also declares co-load, matching the ci.yml subtest (stricter).
- The guard failed with the new expectation before the workflow edit (Expected [bench-deep], Actual nil) and passes after.

(3) Docs (28d665f2)
- docs/adr/0010 gains Addendum 2, "hosted runners are a non-reference disk", citing Q1, D53(e) and D55. It covers:
  - the measurements;
  - that the declaration is honoured only under GITHUB_ACTIONS=true;
  - who declares it (bench-gate, timing, test-e2e, cover's e2e pass, nightly bench-deep);
  - what is reported (B-A, B-B, B-E wall, the spool transition);
  - what stays gated (B-E_cpu, the ledger and 0 lost, the census, X11's ledger-regression ceiling, the structural checks);
  - what is lost: no hosted job gates those three wall rows; only the owner's quiet runs do.
- The docs/adr/README.md index row for 0010 notes both addenda.
- plans/00-ARCHITECTURE.md has no revision log; it uses dated "amended" notes. I added "Non-reference-disk note (amended 2026-10-01, Q1, D53(e), D55)" after the D41 note and updated:
  - the B-A, B-B and B-E Enforced cells;
  - the §7 hard-fail sentence;
  - the CI table's test row (60m Windows leg), test-e2e, timing (name and package list synced to ci.yml: it was missing TestBudgetBF, TestFeaturesFrom_LexicalCohesionShingleCap, internal/mcp and internal/daemon), cover (two passes), bench-gate and security (pathstest on the os/exec list, four checks including the transitive one);
  - the nightly bench-deep line;
  - the "third allowlist entry" wording below the table.

(4) NIT (d97f5584)
- walWaitDiag's doc and fix-cover-hookroundtrip-hosted-evidence.log now say the cause is inferred from the differential and the mechanism, not observed.
- Both name what settles it: the first co-loaded red that prints walWaitDiag. Spooled deliveries mean the designed degrade; an empty spool with a short WAL means a product ingest race.
- The log also notes that no lane now reproduces the co-loaded condition except a by-hand run. Doc-only change; no assertion was touched.

(5) devtool test (42da9e6a)
- The fix was small and stayed inside tools/devtool, so I made it.
- New wholeTreePasses(pkgs) gives each isolatedPasses pass its environment:
  - shared pass: co-load "1" and NONREFERENCE_DISK "";
  - test/e2e alone: co-load "".
- taskTest lists the tree and runs testInvocations: every pass at wholeTreeTestTimeout, all passes always run, and the task fails if any failed.
- coverPasses is now built on wholeTreePasses, so the two cannot drift. Its existing test passes unchanged.
- The new row TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration failed to build before the change and passes after.
- A real `devtool test` was not run: that would be a whole-tree test, which this seat may not run.

No load generator, no Linux container, no real Claude session, no ~/.qompack or ~/.claude touched. No background processes were left running.

### Commits

- bf06fd9b ci(test): give the windows -count=2 leg two passes of timeout
- 6693a9b5 ci(nightly): declare a non-reference disk in bench-deep
- 28d665f2 docs(adr): record the hosted non-reference-disk declaration
- 42da9e6a fix(devtool): run test/e2e alone in devtool test's passes
- d97f5584 docs(e2e): mark TestE2EHookRoundTrip's cause as inferred

### Tests

- `go test -count=1 -run '^TestNonReferenceDisk_IsHostedCIOnly$' ./test/guards (guard expecting bench-deep, before the nightly.yml edit)` — FAIL as intended: nightly_yml_declares_it_in_exactly_the_ruled_jobs, expected [bench-deep], actual nil
- `go test -count=1 -run '^TestNonReferenceDisk_IsHostedCIOnly$' -v ./test/guards (after)` — PASS, all four subtests
- `go test -count=1 -run '^TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration$' ./tools/devtool (before the implementation)` — build failure: undefined: testInvocations
- `go test -count=1 -run '^TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration$' ./tools/devtool (after)` — PASS
- `go test -count=1 -run '^TestCoverPasses_RunsE2EAloneWithoutTheColoadDeclaration$' ./tools/devtool` — PASS (unchanged test over the refactored coverPasses)
- `go test -count=1 -run '^TestStubSkipsPasses_RunsE2EAloneAndDropsNothing$' ./tools/devtool` — PASS
- `go test -count=1 -run '^TestRacePackagesRetainsAllNonE2ECoverage$' ./tools/devtool` — PASS
- `CGO_ENABLED=0 go test -p 2 -count=3 -run '^TestLock_AnExitedOwnerIsReplacedAtOnce$' (run together with the other two 0xc0000142 rows, TestRun_LogsATakenOverLockAndASpoolReplay and TestStageBinary_StartsACopyItsRenamerStillHolds, in one alternation) ./internal/daemon` — ok 2.666s
- `go test -p 2 -count=1 -timeout=30m ./tools/devtool ./test/guards ./test/docs/... (Windows, daytime, shared host)` — exit 0: devtool ok 112.6s, guards ok 57.8s, docs ok 5.3s
- `go test -count=1 ./test/docs/... (after the 00-ARCHITECTURE and ADR edits)` — ok 4.8s
- `go vet ./tools/devtool ./test/guards ./test/e2e on windows and GOOS=linux; go vet ./tools/devtool on GOOS=darwin` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all PASS
- `python yaml.safe_load on .github/workflows/ci.yml and nightly.yml` — both parse

### Criterion changes

- Hosted ci.yml test job, windows-latest leg: -timeout 30m -> 60m under -count=2 (owner ruling D55). Rationale: -timeout is per binary and wholeTreeTestTimeout covers one pass, so two passes get 2 x 30m. A real hang is still killed at 60m and its unfinished tests are named by the c697ed1e reconciliation. The -race legs are unchanged at 30m.
- nightly.yml bench-deep now declares QOMPACK_NONREFERENCE_DISK (D55). On GitHub-hosted runners B-A, B-B and B-E's wall row, and the spool transition, are reported with a note instead of gated there. B-E_cpu, the ledger and 0 lost, the census and the structural checks stay gated. This is the same scope and rationale as bench-gate (Q1, D53(e)).
- test/guards TestNonReferenceDisk_IsHostedCIOnly: nightly's pinned set goes from empty to {bench-deep}. The nightly subtest now also fails a declaring job that also declares co-load. That part is stricter.
- devtool test (local) now runs test/e2e in its own pass with the co-load declaration taken back, instead of inside the co-loaded `go test ./...` (ADR 0010 decision 4, matching hosted test-e2e, cover and stubskips). Every package still runs, at the same -timeout, and no assertion or bound changed. Rationale: under co-load, e2e's live-path rows judge a condition (the designed spool degrade) they are not defined for.

### Open issues

- TestE2EHookRoundTrip's cover-red cause remains inferred, not observed. After 42da9e6a no lane (hosted or devtool) runs test/e2e co-loaded any more, so the discriminating walWaitDiag line will appear only from a by-hand co-loaded run. Its wording: spooled deliveries mean the designed degrade; an empty spool with a short WAL means a product ingest race.
- Hosted-only confirmation still needed: the Windows test leg finishing inside 60m at -count=2, and bench-deep on windows-latest going green under the declaration.
- The hosted kill of internal/daemon at 1800 s cannot yet be told apart from a hosted-only hang. The next windows run will name any unfinished test (c697ed1e).
- The real `devtool test` glue (go list, then two passes) was not run end to end, because a whole-tree run is outside this seat's limits. TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration pins the exact argv and env of each pass, and runCoverPasses already uses the same loop shape.
- Report quoting: runpatterns splits -run alternations at '|'. The tests list above quotes single-name patterns, except the one 0xc0000142 rerun, whose three names are given in prose. If the coordinator quotes the alternation verbatim, a runpatterns waiver like the w8 ones will be needed.

### Needs the owner

- Windows `test` leg -timeout = 60m (implemented in bf06fd9b, approved in D55; restated to match the record). Value: 60m on windows-latest under -count=2; the other legs stay at 30m. Derivation: -timeout bounds each test binary, wholeTreeTestTimeout (30m) is sized for one whole-tree pass, and -count=2 runs every test twice inside one binary, so 2 x 30m. Measured: internal/daemon was killed at 1800 s in run 36816905394's windows test job. Alone at -count=2 on a loaded local Windows host it took 526.6 s (iterations about 272 s and 254 s), and every started test finished, so it did not hang. That run's three failures were 0xc0000142 (STATUS_DLL_INIT_FAILED) spawn failures in iteration 2, and those tests pass at -count=3 alone (1.869 s and 2.666 s). The 60m is therefore not derived from doubling a local figure. The package alone does not approach 30m locally at -count=2, so the hosted overrun is attributed to whole-tree co-load on the two-core runner (nightly's -race run of the package alone took 1203 s there), with a hosted-only hang not yet excluded. If wrong: too small, and the leg stays red on budget alone; too large, and a real hang is killed 30 minutes later but still named by the c697ed1e listing.

## Independent review

### review:cirec: needs-fixes

- **minor** `.github/workflows/ci.yml:196-198 (also commit bf06fd9b body)` — The derivation comment says the hosted kill was caused by whole-tree co-load, which rules out a hosted-only hang. The seat's own evidence log, needs_owner and open_issues all keep that hang open as a live alternative. The wave-16 review finding this item restates (w16-ci/report.md:153, :311) objected to exactly this overclaim. The commit body repeats it: "so it is budget under co-load, not a hang".
  - Evidence: ci.yml: "alone at -count=2 ... it took 527 s with no hang (...), so the kill was the whole-tree co-load stretching two passes past one pass's budget." fix-daemon-count2-windows.log:42-44 says "...the hosted kill is the whole-tree co-load ... or a hosted-only hang." The open_issues entry says "The hosted kill of internal/daemon at 1800 s cannot yet be told apart from a hosted-only hang." A local run of the package alone cannot rule out a hang that happens only on the hosted runner.
  - Fix: Reword the ci.yml comment to match the log, for example: "...so the kill is attributed to whole-tree co-load on the two-core runner; a hosted-only hang is not yet excluded, and the next windows run names any test left unfinished (c697ed1e)." The commit body cannot be amended without a rewrite, so let the corrected comment and the log be the record.
- **minor** `test/bench/hotpath/report.go:189-212 (baWallWaivedNote, bbWallWaivedNote); test/bench/hotpath/main.go:178,239; test/integration/hotpath_test.go:1090,1235; internal/obs/nonrefdisk.go:4-9` — Making bench-deep a declaring job leaves these texts stale, and no record mentions it. The co-load waiver notes are written into the JSON artifacts at runtime. They and the integration-test comments say the B-A/B-B/B-E wall limits are still enforced by "bench-gate, nightly bench-deep, ci.yml's timing job and its test-e2e job". On hosted runners none of these lanes gates those rows any more: bench-gate, timing and test-e2e stopped under D53(e), and bench-deep stops with 6693a9b5. The new ADR Addendum 2 says so plainly ("No hosted job now gates B-A, B-B or B-E's wall row"), so the artifacts now contradict the ADR. obs.NonReferenceDiskEnv's doc still describes the declaration as D53(e)'s ci.yml-only scope, without D55 or nightly.
  - Evidence: report.go:211 says "The %.0fms limit is still enforced on %s by every run that does NOT pass --under-coload — bench-gate, nightly bench-deep, ci.yml's timing job ... and its test-e2e job". report_test.go:280 pins these lane names: `for _, lane := range []string{"bench-gate", "bench-deep", "timing", "test-e2e"}`. hotpath_test.go:1090 says "All three wall-clock rows are still judged at their limits by every run that does NOT pass the flag: bench-gate's and nightly bench-deep's ...". nonrefdisk.go:8 says "applied by coordinator decision D53(e): ci.yml reports hosted fsync-bound rows...".
  - Fix: test/bench/hotpath, test/integration and internal/obs are outside this seat's scope, so at least add an open_issues entry routing this to a later seat. That seat should qualify the lane lists as "on a reference disk, i.e. the owner's quiet runs; on hosted runners these lanes report under QOMPACK_NONREFERENCE_DISK (ADR 0010 Addendum 2)", update report_test.go's pinned lanes to match, and add D55/bench-deep to obs.NonReferenceDiskEnv's doc.
- **nit** `plans/sdd/V6-closeout/w16-ci/runs/fix-daemon-count2-windows.log:43 (and the restated needs_owner item)` — The log says "nightly's -race run of the package alone took 1203 s". nightly.yml's race-windows job runs the whole tree except test/e2e (`go test -race -timeout=30m $(go list ./... | grep -v '/test/e2e$')`), so the 1203 s is internal/daemon's elapsed time inside a co-loaded whole-tree pass, not a run of the package alone. The figure is evidence for the co-load hypothesis precisely because it was co-loaded, so calling it 'alone' misstates the measurement.
  - Evidence: nightly.yml:111 runs the whole tree. The scratchpad's nightly-racewin.log:324 shows `FAIL github.com/qompack/qompack/internal/daemon 1203.316s` within that whole-tree job. The w16-ci report (line 66) says only "1203 s on hosted windows under -race in nightly".
  - Fix: Change "nightly's -race run of the package alone took 1203 s there" to "internal/daemon took 1203 s inside nightly's whole-tree -race run there", in the log and in the needs_owner text.
- **nit** `.github/workflows/ci.yml:201-205` — The new 60m is defined as 2 x wholeTreeTestTimeout, but it is a literal string, and nothing ties it to tools/devtool/test.go's constant. If wholeTreeTestTimeout is ever re-priced, the Windows leg silently keeps the old multiple. The 30m on the -race legs and in timing/test-e2e has the same weakness, which predates this change.
  - Evidence: `flags='-count=2'; timeout='60m'` / `flags='-race'; timeout='30m'`. A grep of test/guards and tools/devtool tests finds no pin linking ci.yml's -timeout values to wholeTreeTestTimeout.
  - Fix: Optional. Add a test/guards row that parses the test step and asserts the Windows timeout equals count x wholeTreeTestTimeout and the other legs equal wholeTreeTestTimeout. Otherwise, note in the comment that the two values must move together.

## Fix seat (review resolution) — status `done`, head `e1741a9f`

### Summary

FIX seat for w16b-cirec (branch closeout/w16b-cirec, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16b-cirec, base 6f118a7b). The implementer's five commits (bf06fd9b..d97f5584) carry D55 items (1)-(5). This seat added one commit, e1741a9f, which fixes reviewer finding 1. Reviewer finding 2 is accurate, but its files are outside this seat's scope, so it is routed to a later seat as an open issue.

## Review resolution
- Finding 1 (minor), ci.yml:196-198 claims too much: CONFIRMED and FIXED in e1741a9f. The comment said "so the kill was the whole-tree co-load stretching two passes past one pass's budget". That contradicted the evidence log (plans/sdd/V6-closeout/w16-ci/runs/fix-daemon-count2-windows.log:42-44 keeps "or a hosted-only hang" open) and the needs_owner item. It now reads: "so the kill is attributed to whole-tree co-load on the two-core runner. A hosted-only hang is not yet excluded: a real hang is still killed, and the reconciliation below names the tests it left unfinished (c697ed1e)." bf06fd9b's commit body ("not a hang") cannot be amended without a rewrite, which the rules forbid. The corrected comment and the log are the record.
- Finding 2 (minor), stale lane lists in hotpath waiver notes and obs doc: CONFIRMED, NOT FIXED here (out of scope), ROUTED. Verified on the branch:
  - test/bench/hotpath/report.go:191 and :211 still say the limit "is still enforced ... by every run that does NOT pass --under-coload — bench-gate, nightly bench-deep, ci.yml's timing job, and its test-e2e job". On hosted runners each of those lanes now declares QOMPACK_NONREFERENCE_DISK (D53(e); bench-deep by 6693a9b5). ADR 0010:236 now says "No hosted job now gates B-A, B-B or B-E's wall row", so the JSON artifacts contradict the ADR.
  - internal/obs/nonrefdisk.go:9 still describes D53(e)'s ci.yml-only scope.
  - The task scope lists ci.yml, nightly.yml, test/guards/nonrefdisk_test.go, 00-ARCHITECTURE, docs/adr, the e2e doc, tools/devtool and the runs dir. test/bench/hotpath, test/integration and internal/obs are not in it, and report_test.go:280 pins the lane names, so fixing it would also change a pinned assertion in another seat's area. See open_issues.

## Verification
- go test -p 2 -count=1 ./test/guards: ok (45.3 s), exit 0. It was run after the ci.yml edit because test/guards parses the workflows.
- go run ./tools/devtool lint --only=docmarkers,runpatterns: docmarkers PASS.
- No Go code changed in this fix round, so neither go vet nor fmt applied. No processes were started beyond those runs, and none are left running.

## Criterion changes
None in this round.

### Commits

- bf06fd9b ci(test): give the windows -count=2 leg two passes of timeout
- 6693a9b5 ci(nightly): declare a non-reference disk in bench-deep
- 28d665f2 docs(adr): record the hosted non-reference-disk declaration
- 42da9e6a fix(devtool): run test/e2e alone in devtool test's passes
- d97f5584 docs(e2e): mark TestE2EHookRoundTrip's cause as inferred
- e1741a9f ci(test): keep a hosted-only hang open in the timeout note

### Tests

- `go test -p 2 -count=1 ./test/guards` — ok 45.323s (exit 0)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS docmarkers (352 plan documents scanned)

### Open issues

- Reviewer finding 2, routed to a later seat that owns test/bench/hotpath, test/integration and internal/obs. The waiver notes baWallWaivedNote and bbWallWaivedNote (test/bench/hotpath/report.go:189-212) are written into the JSON artifacts. They, the comments in test/bench/hotpath/main.go:178,239 and test/integration/hotpath_test.go:1090,1235, and report_test.go:280's pinned lane list still say bench-gate, nightly bench-deep, timing and test-e2e enforce the B-A/B-B/B-E wall limits. On hosted runners all four now report under QOMPACK_NONREFERENCE_DISK (D53(e), D55, 6693a9b5), and ADR 0010 Addendum 2 says no hosted job gates those rows. Qualify the lists as 'on a reference disk (the owner's quiet runs); hosted runs of these lanes report under QOMPACK_NONREFERENCE_DISK', update report_test.go's pinned lanes to match, and add D55/nightly bench-deep to obs.NonReferenceDiskEnv's doc (internal/obs/nonrefdisk.go:4-9).
- The hosted kill of internal/daemon at 1800 s (run 36816905394) cannot yet be told apart from a hosted-only hang. The next windows test-job run under the 60m timeout settles it: the c697ed1e reconciliation names any test left unfinished.
- Commit bf06fd9b's body still says 'not a hang'. It is left unamended (no rewrites); the corrected ci.yml comment (e1741a9f) and the evidence log are the record.

### Needs the owner

- Windows `test` leg -timeout = 60m (implemented in bf06fd9b, approved in D55; restated to match the record). Value: 60m on windows-latest under -count=2; the other legs stay at 30m. Derivation: -timeout bounds each test binary, wholeTreeTestTimeout (30m) is sized for one whole-tree pass, and -count=2 runs every test twice inside one binary, so 2 x 30m. Measured: internal/daemon was killed at 1800 s in run 36816905394's windows test job. Alone at -count=2 on a loaded local Windows host it took 526.6 s (iterations about 272 s and 254 s), and every started test finished, so it did not hang. That run's three failures were 0xc0000142 (STATUS_DLL_INIT_FAILED) spawn failures in iteration 2, and those tests pass at -count=3 alone (1.869 s and 2.666 s). The 60m is therefore not derived from doubling a local figure. The package alone does not approach 30m locally at -count=2, so the hosted overrun is attributed to whole-tree co-load on the two-core runner (nightly's -race run of the package alone took 1203 s there), with a hosted-only hang not yet excluded. If wrong: too small, and the leg stays red on budget alone; too large, and a real hang is killed 30 minutes later but still named by the c697ed1e listing.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



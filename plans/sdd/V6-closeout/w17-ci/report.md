# Wave 17 ci

Branch `closeout/w17-ci`. Workflow `wf_bd12d061-33a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `1a591176`

### Root cause

(1) Q1's hosted fsync tail: ci.yml release-dry-run runs test/e2e alone inside release-check's ci-local test and cover steps but never declared QOMPACK_NONREFERENCE_DISK, so X11 gated B-A/B-B p99 49.152 ms against 15 on ubuntu-latest. release.yml has the same gap at tag time. (2) A test-harness read-order race: test/fault's auditRetentionRoots read the capture sidecars before retention-roots.jsonl while the live daemon publishes sidecar first, root second. The Stop that out_of_order_sessionend sends after SessionEnd, which nothing waits for, landed between the two reads and was reported as a dangling evidence root; the stopped-daemon frontier audit found the store clean.

### Summary

NO PRODUCT CODE CHANGED. Nothing that goes into a bundle was touched: only .github/workflows, test/guards, test/fault, docs/*.md (not bundled) and evidence logs. Candidate 6's bundled bytes still hold, and no candidate 7 is needed for C7.2. Branch closeout/w17-ci (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-ci), 3 commits on top of 9a56b305, not pushed.

## (1) release-dry-run (job 110676058084): the hosted fsync tail of Q1, and a job D53(e) missed

**Root cause, from reldry.log.** release-check ran these steps in order: version agreement (SKIPPED, no tag), then fmt-check, lint, vet, build and test, all PASS. test/e2e passed inside the test step in 396 s. Then the cover step failed, and the only failure was test/e2e TestV3_HotPathUnchangedWithLedgerResident at v3_x11_test.go:605. In the ledger-resident run:
- B-A p99 = 49.152 ms and B-B p99 = 49.152 ms against a 15 ms limit.
- B-B p50 = 0.576 ms.
- B-E_cpu passed, and every other package was ok.

The harness message said "QOMPACK_UNDER_COLOAD=false, QOMPACK_NONREFERENCE_DISK honoured=false". The same package passed in the test step on the same runner minutes earlier. A p50 of 0.58 ms against a p99 of 49 ms is the hosted disk tail, not a product regression.

The job never declared the non-reference disk even though release-check runs test/e2e alone twice: tools/devtool/test.go wholeTreePasses runs the isolated pass, which inherits the caller's declaration, and also takes the declaration back from the co-loaded pass. The old ci.yml comment, "this job runs no wall-clock budget", was wrong.

**Fix.**
- ci.yml `release-dry-run` now declares `QOMPACK_NONREFERENCE_DISK: '1'` at job level, with the same comment block as the other hosted lanes. That is the same mechanism test-e2e, timing, cover and bench-gate use. X10's late-reply branch still writes to the job summary itself through GITHUB_STEP_SUMMARY.
- **Audit of release.yml:** its tag-time `release` job runs `release-check --tag` on ubuntu-latest, which is the same gate. A hosted tail would therefore refuse the real release in the same way, so it gets the same job-level declaration.
- **Audit of marketplace.yml:** it runs no tests, only gh release download, sha256sum and devtool marketplace. It is unchanged.
- No Go defaults changed. obs.NonReferenceDisk() honours the declaration only when GITHUB_ACTIONS=true, so your local `release-check --tag` on the reference host still gates B-A, B-B and B-E's wall row. B-E_cpu, the ledger and the structural checks stay gated everywhere. Inside release-check, test/integration's hot-path row runs in the co-loaded pass, where devtool takes the declaration back, so its behaviour is unchanged.
- **Guard** (test/guards/nonrefdisk_test.go, TestNonReferenceDisk_IsHostedCIOnly):
  - nonrefDiskJobs now includes release-dry-run.
  - New subtest `release_yml_declares_it_in_exactly_the_release_job`.
  - New subtest `every_hosted_fsync_bound_job_declares_its_cause`: any job in ci.yml, nightly.yml, release.yml or marketplace.yml whose run command calls devtool release-check, ci-local, cover, test or bench-hotpath, or a `go test` naming test/e2e or HotPath, must declare co-load or the non-reference disk. This is the class check that would have caught the miss. It was red before the workflow change (runs/guard-nonrefdisk-red.log) and is green after.
- **Docs:** ADR 0010 Addendum 2 ("Who makes it" and "What is lost", which now names the tag's hosted gate) and docs/release.md §2 are updated.
- The repo does not use actionlint; it is not installed and nothing references it. The tests that parse workflows are in test/guards.

## (2) test (windows-latest) (job 110676058036), TestFault_Lifecycle/out_of_order_sessionend: classified (i), a timing artifact of the test's audit, not a product defect

**What failed, from test-win.log.** The first -count pass was recovered, with audit after {sidecars:57 dangling:0 reported:0}. The second pass failed at lifecycle_test.go:89/92: "left 1 dangling reference(s): retention_root(158e3206c3f5): retention root (class "evidence", "capture sidecar sha256:a3e3135b...") names 158e3206c3f5, which is not held", with audit after {tool_uses:38 roots:31 sidecars:57 checkpoints:3 dangling:1 reported:1}. The later `frontier_state_is_explicit` row, which runs a full audit after the daemon is shut down and fails on any dangling reference, passed. So the store ended consistent and the dangling reference was transient.

**Mechanism.**
- The matrix audits beside its one live daemon. Every other row ends with runFlush, which waits until the daemon has ended the session and published its deliveries. out_of_order_sessionend instead ends with a Stop sent after SessionEnd, and nothing waits for it.
- A Stop is a hot-path op that gets a capture sidecar. The startup row shows 3 sidecars per turn: prompt, tool and stop.
- The product publishes the sidecar first, through WriteAtomic, and then appends its evidence retention root (internal/store/capture_sidecar.go writeCaptureSidecar).
- test/fault's auditRetentionRoots read the sidecars, via sidecarBytesHashes, BEFORE retention-roots.jsonl. A Stop published between those two reads looks like a claim without evidence.
- The evidence across runs matches:
  - Failing pass: sidecars 57 (the Stop's sidecar not yet seen) with reported:1, meaning spool bytes still pending.
  - Run 36816905394 windows pass 1: sidecars 58 reported:1.
  - My local -v runs: sidecars 58 in 10/10, reported:1 in 7/10. The audit routinely runs while the Stop is still in flight.

**Other runs.** In run 36955046276, test (ubuntu-latest) 110676058062 and test (macos-latest) 110676058123 succeeded, so their reconciliation was clean and the row passed. In run 36905843834, windows test 110516048169 failed only on TestPreCompactInSpoolSubmodeSealsTheSpooledReads and three TestPreCompactSettle_* rows; Lifecycle passed. In run 36816905394, the windows Lifecycle failures on pass 2 were the old artifact-dir "cannot find the path specified" bug (fixed since), and out_of_order_sessionend's pass 1 recovered.

**Reproduction.**
- `go test -count=10 -p 1 -run '^TestFault_Lifecycle$' ./test/fault` on the base code: ok, 253.5 s (runs/lifecycle-base-count10.log). Did not reproduce.
- Same command under co-load (8 bounded busy loops beside the Linux -race chain, at most 600 s, stopped afterwards and confirmed gone): ok, 224.0 s. Did not reproduce; the window is narrow.
- So the row was shown red deterministically instead: the new TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence publishes a real capture through store.WriteCaptureSidecar between the audit's two reads. It failed on the old order with the hosted message shape (runs/fault-audit-order-red.log).

**Fix** (test/fault/fault.go): auditRetentionRoots now reads retention-roots.jsonl first and the sidecars second, so every claim it sees was declared after its sidecar was on disk. A test-only seam, auditRetentionBetweenReads, is nil outside that test. The new test also asserts that an evidence claim with no sidecar stays one dangling retention root. auditProject's doc comment was false ("every call site runs with the daemon already stopped") and now says which walks are safe beside a live daemon.

**Criterion change:** none. No assertion, bound, timeout or skip changed; the read order was corrected so the audit only reports dangling references that are real. The product behaviour this relies on, sidecar before root, is in writeCaptureSidecar's code order. TestCaptureSidecar_RegistersARetentionRoot pins that the root is declared, but nothing pins the order (see open issues).

## Commands and results (all logs under plans/sdd/V6-closeout/w17-ci/runs/)
Every Go run happened after "linux timing exit=" (04:17:32Z) and before "linux race exit=". Nothing ran in qompack-cx-cand, and Docker was not touched.
- `go test -p 2 -count=1 -run '^TestNonReferenceDisk_IsHostedCIOnly$' ./test/guards`: FAIL before the workflow edit, ok after.
- The 12 workflow-pinning guard tests by exact name, -v (TestColoadYieldersAreJudgedInIsolation, TestColoadDeclarationIsPinnedToTheGoConstant, TestWorkflowGoVersionMatchesToolchain, the three TestMarketplaceWorkflow* rows, TestGoreleaserBuildsNothingAndDraftsTheRelease, TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck, TestGoreleaserGuardRejectsReshapedYAML, TestNightlyFuzzMatrix, TestNightlyFuzz_LandedSubplansMirrorsCoverGo, TestNonReferenceDisk_IsHostedCIOnly): all PASS, 49.1 s.
- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault`: FAIL on the old order, PASS after.
- `go test -count=10 -p 1 -v -run '^(TestFault_Lifecycle|TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence)$' ./test/fault` with the fix: 10/10 and 10/10 PASS, 260.3 s.
- `go run ./tools/devtool fmt-check`: exit 0. `go vet ./test/fault ./test/guards` on Windows and with GOOS=linux: exit 0.
- `go test ./test/docs`: ok. gen-config-docs, gen-command-docs and gen-mcp-docs --check: all up to date. `go test ./test/release`: ok.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all PASS.

The hosted proof that release-dry-run and test (windows-latest) now go green needs a CI run on a pushed integration of this branch. I did not push.

### Commits

- e8c08b89 ci(workflows): declare the non-reference disk in the release jobs
- dd8e9fd2 fix(fault): read retention claims before the sidecars they name
- 1a591176 docs(release): say the hosted release gate reports fsync rows

### Tests

- `go test -p 2 -count=1 -run '^TestNonReferenceDisk_IsHostedCIOnly$' ./test/guards (before workflow edit)` — FAIL (expected red: release-dry-run and release.yml release undeclared) - runs/guard-nonrefdisk-red.log
- `go test -p 2 -count=1 -run '^TestNonReferenceDisk_IsHostedCIOnly$' ./test/guards (after)` — ok 0.334s - runs/guard-nonrefdisk-green.log
- `go test -p 2 -count=1 -v -run (the 12 workflow-pinning guard rows by exact name) ./test/guards` — all PASS, ok 49.131s - runs/guard-workflows-green.log <!-- runpatterns: the -run argument is a placeholder naming the 12 workflow-pinning guard rows that the evidence log runs/guard-workflows-green.log lists by exact name, not a runnable pattern -->
- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault (old read order)` — FAIL a_capture_published_mid_audit_is_not_dangling (reproduces hosted message) - runs/fault-audit-order-red.log
- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault (fixed)` — PASS both subtests - runs/fault-audit-order-green.log
- `go test -count=10 -p 1 -run '^TestFault_Lifecycle$' ./test/fault (base)` — ok 253.508s, no local repro - runs/lifecycle-base-count10.log
- `same under 8-process bounded co-load (base)` — ok 224.021s, no repro - runs/lifecycle-base-coload8-count10.log
- `go test -count=10 -p 1 -v -run '^(TestFault_Lifecycle|TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence)$' ./test/fault (fixed)` — 10/10 + 10/10 PASS, ok 260.306s - runs/lifecycle-fixed-count10.log
- `go run ./tools/devtool fmt-check; go vet ./test/fault ./test/guards (windows and GOOS=linux)` — exit 0 - runs/checks.log
- `go test -count=1 ./test/docs; devtool gen-config-docs/gen-command-docs/gen-mcp-docs --check; go test -count=1 ./test/release` — ok; all up to date; ok - runs/checks.log
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS - runs/lint.log

### Criterion changes

- None. No assertion, threshold, timeout or skip changed. The fault audit's read order was corrected to match the producer's publication order, and the new regression test also asserts that a real evidence claim with no sidecar still fails. Workflow change: two hosted release jobs gained the existing non-reference-disk declaration (env only, no Go defaults), and the guard pinning which jobs declare it was widened to include them and given a new cause check.

### Open issues

- Hosted confirmation is still owed: ci.yml release-dry-run and test (windows-latest) need a CI run on a pushed integration of closeout/w17-ci. I did not push.
- Same race, latent, in two other walks of the test/fault audit (not fixed; no current row hits them): (a) auditCheckpoints reads MANIFEST.jsonl before listing artifacts, so a checkpoint finalised in between would look like an orphan; (b) hashHeld resolves a root through the store's in-memory rootIndex, which is loaded at Open. Every lifecycle row except out_of_order_sessionend settles its deliveries before auditing. auditProject's doc comment now records both.
- The product's sidecar-before-root publication order (internal/store/capture_sidecar.go writeCaptureSidecar: WriteAtomic, then appendRetentionRootVolatile) is in the code order and the doc comment but not pinned by a test. The internal/store owner should add one.
- Product fsck (internal/cli/fsck.go checkCaptures before checkRetention) has the same read order. Run beside a live daemon it could report a transient false evidence-root defect. fsck already tells the user that results with the daemon running are "a snapshot of a moving target", so I left it alone: it is bundled code and would need candidate 7.

### Needs the owner

- Please confirm the policy extension: release.yml's tag-time `release` job now declares QOMPACK_NONREFERENCE_DISK (Q1 / D53(e) / D55 extended to the real release gate). The effect: the hosted tag gate reports X11's B-A, B-B and B-E wall rows instead of gating them, and the gate that judges them at their limits is your local `release-check --tag` on the reference host. Without it, a hosted fsync tail could refuse a real release. No new budget or bound numbers were introduced.

## Independent review

### review:ci: needs-fixes

- **minor** `test/fault/audit_order_test.go:54-80 with test/fault/fault.go:1825-1845` — The new regression test checks where the seam sits, not the read order its name promises. Undo the fix by moving `evidence := sidecarBytesHashes(root)` back above `readJSONLines` while leaving the seam where it is, and the test still passes. The green subtest also only ever takes the ErrNotExist early-return path. store.Open never creates retention-roots.jsonl (only writeCaptureSidecar and the AppendRetentionRoot family write it). So the first read returns ErrNotExist, the seam publishes the capture, and the function returns before it resolves any claim against a sidecar scan.
  - Evidence: In the old-order variant with the seam after readJSONLines, the sequence is: sidecarBytesHashes returns empty, readJSONLines returns ErrNotExist, the seam publishes, `return`, so Dangling is empty and the test is green. The red log (runs/fault-audit-order-red.log) only exists because the implementer moved the seam between the two reads in the old order. The test therefore pins that edit, not the order. Grepping internal/store for appendRetentionRoot shows no writer of the file at Open.
  - Fix: Make the seam order-sensitive. For example, call `auditRetentionRead(stage string)` at the start of each of the two reads ("claims", then "evidence"). The test publishes the capture on whichever read happens first, and asserts the stage sequence is exactly [claims evidence]. Also seed one unrelated claim, such as an evidence root whose sidecar already exists, so the roots file exists and the claim-resolution loop runs. Then a reverted order fails both on the sequence and on the dangling reference.
- **minor** `docs/release.md:84-90 (new paragraph) vs docs/release.md:23 (procedure step 3); docs/adr/0010-wall-clock-under-coload.md:239-243` — The docs now say a local `release-check --tag` on the reference host is the gate that judges B-A/B-B/B-E-wall at release. But the procedure's step 3 says only `go run ./tools/devtool release-check`: no --tag, no reference-host requirement, and no recorded result. With release.yml now waiving those rows, an unenforced and unrecorded manual step is the only thing that gates them at tag time. The docs state this as settled policy, while the implementer lists that same extension under needs_owner as unconfirmed. That claim goes past the evidence (A8).
  - Evidence: Step 3 reads: "**Run the gate locally** — `go run ./tools/devtool release-check` — and fix whatever it stops on." The new paragraph reads: "`release-check --tag` run locally on the reference host is the gate that judges those rows at their limits." needs_owner reads: "Please confirm the policy extension: release.yml's tag-time `release` job now declares QOMPACK_NONREFERENCE_DISK".
  - Fix: Change step 3 to require the run on the reference host and to keep its dist/release-check.json as the release's record of the fsync-bound rows. Alternatively, mark the paragraph and the ADR sentence pending the owner's ruling on the release.yml extension until it is recorded (with a D-number).
- **minor** `docs/release.md:86-89` — The release guide understates what the declaration waives on the release gate. It lists only X11's B-A, B-B and B-E wall rows. The ADR and both workflow comments also report the §12.2 spool-submode transition and X10's late or deferred prompt-reply recovery branch instead of gating them.
  - Evidence: The ci.yml release-dry-run and release.yml comments say: "The fsync-bound wall rows (B-A, B-B, B-E's wall row), the §12.2 spool-submode transition and X10's late or deferred prompt-reply recovery branch ... are reported". docs/release.md names only "X11's fsync-bound wall rows (B-A, B-B, B-E's wall row)".
  - Fix: Name all three reported items in docs/release.md, or point to ADR 0010 Addendum 2 as the complete list instead of giving a partial one.
- **minor** `plans/sdd/V6-closeout/w17-ci (summary) / reldry.log:1139` — "Nothing else failed in release-check" is true only for the steps that ran. release-check stops at its first failure, and the cover failure came before every step after it. Those steps never ran on candidate 6 in run 36955046276: build-all, the generated-doc checks, the licence inventory, real-binary determinism, the rollback rehearsal and the guards run. The summary does not say so, so a reader may take the hosted release dry run as green apart from X11.
  - Evidence: reldry.log ends with `release-check: ci-local cover FAIL` at 03:03:47, followed by `Process completed with exit code 1`. No later `=== release-check:` headers appear.
  - Fix: State in the evidence and handoff that steps after `ci-local cover` were not run in 36955046276. The hosted rerun on the integrated branch is the first evidence for them, and a new red there is not a regression of this fix.
- **nit** `test/guards/nonrefdisk_test.go:59-60` — The cause check is described as covering "every hosted job that judges a fsync-bound row". Its regex only matches devtool tasks, or `go test` lines that name test/e2e or HotPath literally. A future job running `go test ./...` or `go test ./test/integration`, which contains TestIntegration_HotPathWarmWithRealResidentState, with no declaration would pass the guard.
  - Evidence: fsyncBoundRunRE = `devtool (release-check|ci-local|cover|test|bench-hotpath)(\s|$)|go test .*(test/e2e|HotPath)`
  - Fix: Widen the `go test` branch to `go test .*(\./\.\.\.|test/e2e|test/integration|test/bench/hotpath|HotPath)`. Every current match (race-windows, race-product-child, test, test-e2e, timing) already declares a cause, so nothing new goes red.
- **nit** `docs/adr/0010-wall-clock-under-coload.md:227` — The edited bullet leaves one line far past the document's wrap width.
  - Evidence: "make one of the two. The owner's quiet reference runs (quiet.sh, phase3.sh, overnight.sh) never make it and never" (about 118 characters, against roughly 100 in the surrounding lines). The same happens at line 242.
  - Fix: Rewrap the two edited paragraphs to the surrounding width.

## Fix seat (review resolution) — status `done`, head `a55e6a67`

### Root cause

(1) Q1's hosted fsync tail. ci.yml's release-dry-run runs test/e2e alone inside release-check's ci-local test and cover steps but never declared QOMPACK_NONREFERENCE_DISK. So X11 gated B-A/B-B p99 of 49.152 ms against 15 ms on ubuntu-latest. release.yml had the same gap at tag time. (2) A read-order race in the test harness. test/fault's auditRetentionRoots read the capture sidecars before retention-roots.jsonl, while the live daemon publishes the sidecar first and the root second. The Stop that out_of_order_sessionend sends after SessionEnd, which nothing waits for, landed between the two reads and was reported as a dangling evidence root. The stopped-daemon audit found the store clean.

### Summary

NO PRODUCT CODE CHANGED. Nothing that goes into a bundle was touched, so candidate 6's bundled bytes still hold and no candidate 7 is needed for C7.2. `git diff --stat 9a56b305..HEAD` touches only these paths: .github/workflows/{ci,release}.yml, docs/release.md, docs/adr/0010-wall-clock-under-coload.md, test/fault/{fault.go,audit_order_test.go}, test/guards/nonrefdisk_test.go and evidence logs under plans/sdd/V6-closeout/w17-ci/runs/. None of these is plugin/**, a command source, a manifest or Go code compiled into bin/qompack. The work is on branch closeout/w17-ci in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-ci: 5 commits on top of 9a56b305, not pushed. This fix seat added 88626fdd and a55e6a67 on top of the first seat's three.

## Root causes and fixes (first seat, carried forward unchanged)

**(1) release-dry-run, job 110676058084.** This is the Q1 hosted fsync tail in a job that D53(e) missed. Inside release-check, the ci-local cover step's test/e2e pass failed TestV3_HotPathUnchangedWithLedgerResident at v3_x11_test.go:605: B-A and B-B p99 were both 49.152 ms against a 15 ms limit, with B-B p50 at 0.576 ms. The run had QOMPACK_NONREFERENCE_DISK honoured=false, and the same package had passed in the test step minutes earlier.
- **Fix:** ci.yml's release-dry-run and release.yml's tag-time release job now declare QOMPACK_NONREFERENCE_DISK at job level. Only the workflow environment changed; no Go defaults did.
- **Guard:** TestNonReferenceDisk_IsHostedCIOnly gains a class check, every_hosted_fsync_bound_job_declares_its_cause. It was red before the workflow edit.
- marketplace.yml runs no tests and is unchanged.

**(2) test (windows-latest), job 110676058036, TestFault_Lifecycle/out_of_order_sessionend.** Classified as (i), a timing artifact of the test's audit, not a product defect.
- **Mechanism:** test/fault's auditRetentionRoots read the capture sidecars before retention-roots.jsonl. The live daemon publishes in the other order: sidecar first, then retention root (writeCaptureSidecar). The Stop that the row sends after SessionEnd, which nothing waits for, landed between the two reads and was reported as a dangling evidence root.
- **The store itself was clean:** the frontier_state_is_explicit audit, run with the daemon stopped, found nothing dangling.
- **Fix:** read the claims first, then the evidence.

## Review resolution

- **Finding 1 (regression test pins the seam, not the order): CONFIRMED and FIXED** in 88626fdd.
  - *Verified by mutation:* I moved `evidence := sidecarBytesHashes(root)` back above readJSONLines and left the seam where it was. The old test still passed (exit 0). I also confirmed the reviewer's ErrNotExist point: store.Open never creates retention-roots.jsonl, so the old green path returned early.
  - *Fix (test/fault/fault.go):* the seam is now `auditRetentionAfterRead func(read string)`. It fires inside two helpers, readRetentionClaims ("claims") and readRetentionEvidence ("evidence"), so reordering the reads also reorders what the test observes.
  - *Fix (test/fault/audit_order_test.go):* the test first publishes a complete capture, so the roots file exists and a claim is resolved against the sidecar scan. It then publishes a second capture with distinct bytes after whichever read comes first. It asserts that the reads happened in the order [claims evidence] and that nothing dangles, and it checks that both claims were declared and resolve once the audit is settled.
  - *Red first:* in the old order, the new test fails on both assertions, "read [evidence claims], want [claims evidence]" and the hosted-shaped "retention root (class \"evidence\", ...) ... which is not held" (runs/fault-audit-order-r2-red.log). It is green in the fixed order (runs/fault-audit-order-r2-green.log). The first seat's runs/fault-audit-order-red.log and fault-audit-order-green.log are kept as history; the r2 logs replace them as the evidence.
- **Finding 2 (docs name a reference-host --tag gate that the procedure does not require; settled-policy claim): CONFIRMED and FIXED** in a55e6a67.
  - *docs/release.md §1 step 3* now requires the local release-check to run on the reference host. The run's dist/release-check.json is kept as the release's record of the fsync-bound rows, and a hosted or other non-reference run does not stand in for it.
  - --tag is not required there. The tag does not exist until step 4, and the hot-path rows run the same way with or without it.
  - *§2 paragraph:* now points at step 3 and states that the release job's declaration "is pending the owner's ruling (C7.2)". ADR 0010 Addendum 2 and the release.yml comment carry the same pending marker and the same step-3 pointer, in place of "coordinator's local release-check --tag".
- **Finding 3 (release.md understates what is reported): CONFIRMED and FIXED** in a55e6a67.
  - docs/release.md now names all three reported items: the fsync-bound wall rows, the §12.2 spool-submode transition in the hot-path tests, and X10's late or deferred prompt-reply recovery branch (D56(a), also written to the job summary). It names ADR 0010 Addendum 2 as the complete list.
  - The ADR's "What is reported" list itself lacked X10's branch, which D56(a) licenses under the same declaration. It now includes it.
- **Finding 4 ("nothing else failed in release-check" read as the whole gate): CONFIRMED, corrected here** (report wording only; no file change).
  - reldry.log shows release-check steps through `ci-local test PASS` at 02:49:05, then `ci-local cover FAIL` at 03:03:47, then the job ends. release-check stops at the first FAIL.
  - So run 36955046276 never ran the steps after ci-local cover on candidate 6: build-all, generated docs, guards, licences, real-binary determinism, rollback rehearsal, plugin-validate and marketplace. govulncheck is skipped in this job by design. The job's later bundle --archive and marketplace --tag steps did not run either.
  - The corrected claim: no other release-check step that ran failed, and within the cover step only test/e2e failed.
  - The hosted rerun on the integrated branch is the first evidence for the steps that did not run. A new red there is not a regression of this fix.

## Commands and results (this seat)

Logs are under plans/sdd/V6-closeout/w17-ci/runs/. Every run was after "linux timing exit=" (04:17:32Z) and before "linux race exit=", which is the -race window: focused tests only, one at a time. Nothing ran in qompack-cx-cand, Docker was not touched, and no load generator was used.
- **Mutation check of the old test** (scratch log w17/ci/mutation-oldtest.log): reverted read order with the seam in place, then `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault` gave PASS, confirming finding 1. The file was restored with git checkout.
- **Same command, new test, old order:** FAIL on both assertions (fault-audit-order-r2-red.log).
- **Same command, new test, fixed order:** PASS on both subtests (fault-audit-order-r2-green.log).
- **Lifecycle re-run:** `go test -count=3 -p 1 -v -run '^(TestFault_Lifecycle|TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence)$' ./test/fault` passed 3/3 and 3/3, ok in 56.0 s (lifecycle-r2-count3.log).
- **Format and vet:** `go run ./tools/devtool fmt-check`, `go vet ./test/fault` and `GOOS=linux go vet ./test/fault` all exit 0 (checks-r2.log).
- **Docs:** `go test -count=1 ./test/docs` ok (docs-r2.log). The gen-config-docs, gen-command-docs and gen-mcp-docs inputs are unchanged.
- **Guards:** `go test -p 2 -count=1 -v -run '^(TestNonReferenceDisk_IsHostedCIOnly|TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck|TestGoreleaserBuildsNothingAndDraftsTheRelease|TestWorkflowGoVersionMatchesToolchain|TestColoadDeclarationIsPinnedToTheGoConstant)$' ./test/guards` all PASS, ok (docs-r2.log).
- **Lint:** `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` all PASS (lint-r2.log).
- The first seat's commands and results (12 workflow guard rows, count=10 lifecycle runs on the base, under co-load and with the fix, test/release, gen-*-docs --check) stand as reported in its result.

## Criterion changes

None. No assertion, threshold, timeout or skip was loosened. The regression test was made stricter: it now asserts the read sequence and runs the claim-resolution loop. The fault audit's read order matches the producer's publication order. Two hosted release jobs carry the existing declaration through environment variables only, with no Go default changed. The docs now require a reference-host gate run before a tag, which is a stricter procedure.

### Commits

- e8c08b89 ci(workflows): declare the non-reference disk in the release jobs
- dd8e9fd2 fix(fault): read retention claims before the sidecars they name
- 1a591176 docs(release): say the hosted release gate reports fsync rows
- 88626fdd test(fault): observe the retention audit's read order directly
- a55e6a67 docs(release): require the reference-host gate run before a tag

### Tests

- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault (OLD test, read order reverted, seam unmoved)` — PASS (exit 0): confirms finding 1, the old test did not pin the order
- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault (new test, old read order)` — FAIL on the read sequence [evidence claims] and on the hosted-shaped dangling evidence root (runs/fault-audit-order-r2-red.log)
- `go test -p 2 -count=1 -v -run '^TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence$' ./test/fault (new test, fixed order)` — PASS, both subtests (runs/fault-audit-order-r2-green.log)
- `go test -count=3 -p 1 -v -run '^(TestFault_Lifecycle|TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence)$' ./test/fault` — 3/3 + 3/3 PASS, ok 56.037s (runs/lifecycle-r2-count3.log)
- `go run ./tools/devtool fmt-check; go vet ./test/fault; GOOS=linux go vet ./test/fault` — exit 0 for all three (runs/checks-r2.log)
- `go test -count=1 ./test/docs` — ok 2.373s (runs/docs-r2.log)
- `go test -p 2 -count=1 -v -run '^(TestNonReferenceDisk_IsHostedCIOnly|TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck|TestGoreleaserBuildsNothingAndDraftsTheRelease|TestWorkflowGoVersionMatchesToolchain|TestColoadDeclarationIsPinnedToTheGoConstant)$' ./test/guards` — all PASS, ok 0.331s (runs/docs-r2.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS (runs/lint-r2.log)

### Criterion changes

- None loosened. The regression test TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence was tightened: it now asserts the read sequence [claims evidence] and runs the claim-resolution loop against a real sidecar scan, and it fails on the old order. docs/release.md §1 step 3 now requires the local gate run on the reference host and keeps its dist/release-check.json. That is a stricter procedure, marked pending the owner's ruling on the release.yml declaration. No assertion, threshold, timeout, skip or Go default changed.

### Open issues

- Hosted confirmation is still owed. ci.yml's release-dry-run and test (windows-latest) need a CI run on a pushed integration of closeout/w17-ci; nothing was pushed. In run 36955046276 release-check stopped at ci-local cover, so build-all, generated docs, guards, licences, real-binary determinism, rollback rehearsal, plugin-validate, marketplace, and the job's bundle --archive and marketplace --tag steps never ran on candidate 6. The rerun is their first evidence, and a red there is not a regression of this fix.
- The same race is latent in two other walks of the test/fault audit, not fixed because no current row hits them. (a) auditCheckpoints reads MANIFEST.jsonl before listing artifacts. (b) hashHeld resolves through the store's rootIndex, which is loaded at Open. auditProject's doc comment records both.
- The product's sidecar-before-root publication order (internal/store/capture_sidecar.go, writeCaptureSidecar: WriteAtomic, then appendRetentionRootVolatile) is not pinned by any product test. The internal/store owner should add one; that is bundled code, so it waits for the next candidate.
- Product fsck (internal/cli/fsck.go, checkCaptures before checkRetention) uses the same claim/evidence read order. Run beside a live daemon it could report a transient false evidence-root defect. fsck already warns that results with the daemon running are a snapshot of a moving target. It is bundled code and would need candidate 7, so it was left alone.

### Needs the owner

- Please confirm (and record with a D-number) the policy extension: release.yml's tag-time `release` job now declares QOMPACK_NONREFERENCE_DISK, extending Q1 / D53(e) / D55 to the real release gate. Effect: the hosted tag gate reports X11's B-A, B-B and B-E wall rows, the §12.2 spool-submode transition and X10's late or deferred recovery branch instead of gating them. The verdict on those rows at their limits becomes the local release-check on the reference host, which docs/release.md §1 step 3 now requires before a tag, keeping its dist/release-check.json. Without the declaration, a hosted fsync tail can refuse a real release. docs/release.md, ADR 0010 Addendum 2 and the release.yml comment mark this pending your ruling. No new budget or bound numbers were introduced.

## Independent verification of the fix seat: needs-fixes

- **nit** `test/guards/nonrefdisk_test.go:46-51 (releaseNonrefDiskJobs doc comment)` — Finding 2's fix covered docs/release.md, ADR 0010 Addendum 2 and the release.yml comment but missed a fourth copy of the same claim. The guard's comment still states the release job's declaration as settled policy and names the old gate: "The reference verdict on those rows at tag time is the coordinator's local `release-check --tag` on the reference host". It carries no pending-C7.2 marker and does not point at docs/release.md §1 step 3. That contradicts the fix seat's report that the "coordinator's local release-check --tag" wording was replaced, and it is the over-claim finding 2 flagged, now in one remaining place. Only commit e8c08b89 touched this file; a55e6a67 did not.
  - Evidence: `grep -rn "coordinator's local" .github docs test/guards` at a55e6a67 matches only test/guards/nonrefdisk_test.go:50. release.yml:30-34 now reads "the reference verdict on those rows is the local release-check on the reference host that docs/release.md §1 step 3 requires before the tag. This declaration carries D53(e)/D55 to the tag-time gate and is pending the owner's ruling (C7.2)."
  - Fix: Reword the releaseNonrefDiskJobs comment to match release.yml: the reference verdict is the local release-check on the reference host required by docs/release.md §1 step 3, and the `release` job's declaration is pending the owner's ruling (C7.2). This changes a comment only; no assertion changes.


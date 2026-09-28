# Wave 2b workstream report: w2-wintriage (C3.2)

Branch `closeout/w2-wintriage`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `805634656956ad04bcd9b35a5991c0f8ac2363ab`

### Root cause

(1) The test harness was wrong, not the product. requireFileVersion was added in 2ccae0c (closeout/linux, merged at 568222c). It compared each index/files.jsonl "path" with the display path docs/NOTES.md. The observer records a file version under hookio.CaptureScope's PrimaryKey (internal/observer/tooluse.go:112). That key is paths.Key, which lowercases paths on Windows and macOS by design (internal/paths/norm.go:189-204, section 4). A dump of files.jsonl taken when the wait expired showed the version was written as "docs/notes.md" at the same instant as its record. Linux does not lowercase paths, and the sibling ReRead case uses the all-lowercase src/auth.ts, so both passed there. re_read looks versions up by the same key (internal/mcp/handlers.go:938, handlers_span.go:305). A second harness bug showed up under -count=5: artifactDir cached a t.TempDir by test name (since fc5228f), so every repeat after the first got a directory that had already been deleted. (2) The time is spent in the harness's own waits for something to be absent, not in any hook. For config_json_corrupt, admission refuses every delivery, so doHook returns before preSend, session-start never starts a daemon, and waitDaemonUp runs out its 60 s daemonUpBound. For delivery_seal_torn_slot, ingest.Accept writes the delivery to the WAL durably but refuses to lease it (ingest.go:296-300: no ACK, no substitute observation), so waitIndexed runs out its 30 s indexBound. Every hook answered in 1.05 s or less.

### Summary

Both items are done. The ArchivedText red on Windows was a test-harness bug and is fixed with regression tests. The two slow fault rows turned out to be the harness waiting out its own time limits, not a product defect, so I left test/fault unchanged and propose a faster check below for the owner to decide on.

RESUMING. The brief said this workstream had no first attempt, but it did. The branch already had two commits (bab2f4a and 69678a9, 2026-09-25 19:07Z), eight Windows logs in runs/, and an uncommitted Linux gate artifacts directory from 19:10Z. There were no uncommitted source edits. I checked everything before keeping it and decided as follows:
- **bab2f4a (kept).** I confirmed that paths.Key lowercases paths on windows/darwin, that the observer writes under scope.PrimaryKey, and that re_read looks up paths.Key(norm). I also re-ran the new predicate test with the old comparison swapped back in: it fails as it should (fold_true arm: key "docs/notes.md" must satisfy the wait for "docs/NOTES.md").
- **69678a9 (kept).** Its red and green logs are consistent. The cleanup deletes the cache entry exactly when the directory it points to is removed.
- **Linux artifacts (kept and committed in 14e5ed8).** linux-nonroot-gate.sh --prefix cx-w2-wintriage at 69678a9: uid 10001, -race, -count=5 over TestSecurity_ArchivedTextIsDataNeverAnInstruction and TestHarness_*. Exit 0, 35 pass, 0 fail, 0 skip. The code has not changed since, so the run still covers HEAD.

ITEM 1 — TestSecurity_ArchivedTextIsDataNeverAnInstruction.
- **Repro.** It failed alone on b070bbe three times: the coordinator's run, w2-win-security-repro-before.log, and the diagnostic dump run. The cause is a string mismatch, so the failure is deterministic.
- **Fix, test/security only, no product change.** fileVersionNames now compares against paths.KeyFold(rel, paths.DefaultFold()), the key the observer writes and re_read reads. The artifactDir cache entry is now deleted by a t.Cleanup registered together with the directory. The import-rules comment in tools/devtool/importrules.go now lists hookio (comment only).
- **New regression tests.**
  - TestHarness_FileVersionPredicateReadsTheObserversKey pins the check against the observer's own key on this host, and against both fold settings, so it also fails on Linux if lowercasing is ignored.
  - TestHarness_TempArtifactDirDoesNotOutliveItsTest covers the stale directory.
  - Both were red before the fix; logs are in runs/.
- **Windows results.**
  - The case alone, -count=5: 5 of 5 pass (earlier attempt's log plus my re-run, w2-win-security-archivedtext-count5-rerun.log).
  - The whole test/security package alone once: exit 0 in 46 s, 20 top-level tests pass. The one skip is the existing symlink-privilege platform skip of captured_file_replaced_by_link_outside.
  - tools/devtool once in full: exit 0.
  - fmt-check, go vet on test/security and tools/devtool, and lint importgraph, testdeps and sleepcheck all pass. check-commit-msg passes on all four commits.

ITEM 2 — TestFault_PublicationBoundaries slow subcases. No product defect: no hook and no daemon reply blocks.
- **Baseline run (no changes):** delivery_seal_torn_slot 39.2 s, config_json_corrupt 63.5 s, no hook over 5 s.
- **Diagnostic run** (temporary patch, committed as a .patch file beside its log, then reverted):
  - config_json_corrupt: session-start 48 ms, observe tool 62 ms, flush 42 ms; waitDaemonUp=false after 1m0.003s.
  - delivery_seal_torn_slot: session-start 513 ms, observe tool 95 ms, flush 523 ms; waitDaemonUp took 1 ms; waitIndexed=false after 30 s.
  - Control row roots_last_line_truncated: indexed in 210 ms, row 5.4 s.
  - The slowest hook in the whole run was a seed session-start at 1.052 s.
- **Proposed faster check (not implemented; needs an owner decision).**
  - The rule: a positive sign that the product refused the observation skips the wait, and the clock stays as the fallback.
  - Right after the recovery session's hooks, recoverSession would ask a row-specific question:
    - config row: self-test --json reports config.capture not ok at critical severity (read from the structured field), and the tool_use id is in no file under .qompack/. Then up=false and indexed=false are settled at once.
    - seal row: status --json shows l0_delivery_unleased up across the observe hook (it is counted per delivery in leaseDelivery), the id is present in a retained WAL or spool line, and it is absent from the index after flush. Then indexed=false is settled.
    - Whenever these conditions don't hold, the current full wait still runs.
  - **Why it is sound.** In judgeRecovery, a shorter or skipped wait can only turn "recovered" into explicit_incomplete or failed. That is stricter, never a false pass, and the shortcut only fires on the product's own refusal evidence. One caveat on the seal row: the counter is not tied to this one delivery (a drain retry could also bump it), which is why the WAL and index conditions are part of the check.
  - **Cheaper alternative for the config row only:** wait absentDaemonBound (10 s) instead of 60 s, gated on the same config.capture check. test/security already does this for the same situation, and test/fault/fault.go:784-788 declares that constant but never uses it.
  - Estimated saving: about 50 s plus about 29 s per run.

INCIDENT, my own. `devtool lint --only=importgraph,testdeps,bindeps,sleepcheck,stubskips` turned out to start a whole-tree `go test -json ./internal/... ./cmd/... ./test/...` inside the stubskips check. It ran from about 21:22Z to about 21:38Z. I stopped only my own process tree, five pids found by walking parent ids down from my devtool: devtool 36992, go 22476, e2e.test 46380, bench-hotpath 43040, and daemon 17908, whose project was that e2e test's own TempDir. I then confirmed no qompack.exe was left over. The only test processes still running belong to other workstreams and were left alone. This loaded the machine for about 15 minutes. The fault diagnostic run overlapped that window, and its hooks were still at 1.05 s or less.

CRITERION CHANGE (bab2f4a). requireFileVersion now waits for the version under its store key instead of the display path. On a host that lowercases paths, the version keyed "docs/notes.md" is the version of docs/NOTES.md, and it is exactly the entry re_read serves. The wait still requires a recorded version of that one path, and a version of any other path does not satisfy it, which the regression test's negative arm pins. No assertion was loosened, and no bound, skip or golden changed. 69678a9 changes no criterion.

### Commits

- bab2f4a test(security): wait for the file version under its store key (from the earlier attempt, re-verified and kept)
- 69678a9 test(security): forget a temp artifact dir when its test ends (from the earlier attempt, re-verified and kept)
- 14e5ed8 docs(v6): record the archived-text fix's Linux and Windows runs
- 8056346 docs(v6): record what the two slow fault rows wait on

### Tests

- `go test -count=1 -run TestHarness_FileVersionPredicateReadsTheObserversKey ./test/security (old comparison swapped back in temporarily)` — FAIL as expected (fold_true arm): regression test confirmed red on the old check; swap reverted
- `go test -count=5 -timeout 30m -run 'TestSecurity_ArchivedTextIsDataNeverAnInstruction$' -v ./test/security (Windows, CGO_ENABLED=0, QOMPACK_SECURITY_ARTIFACTS unset)` — exit 0, 5/5 PASS (runs/w2-win-security-archivedtext-count5-rerun.log)
- `QOMPACK_SECURITY_ARTIFACTS=<scratch> go test -count=1 -timeout 30m -v ./test/security (Windows, whole package alone)` — exit 0, 46.1 s, 20 top-level tests PASS, 1 existing platform SKIP (symlink privilege) (runs/w2-win-security-full.log)
- `linux-nonroot-gate.sh --prefix cx-w2-wintriage --run '^(TestSecurity_ArchivedTextIsDataNeverAnInstruction|TestHarness_.*)$' --count 5 -- ./test/security @69678a9 (earlier attempt's run, -race, uid 10001)` — exit 0, pass=35 fail=0 skip=0 (runs/cx-w2-wintriage-security-archivedtext-69678a9-20260925T191055Z-artifacts/)
- `go test -count=1 -timeout 30m ./tools/devtool (Windows)` — exit 0, 46.5 s (runs/w2-win-devtool-full.log)
- `go run ./tools/devtool fmt-check; CGO_ENABLED=0 go vet ./test/security ./tools/devtool` — both exit 0
- `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps,sleepcheck,stubskips` — importgraph PASS (72 pkgs), testdeps PASS (74), sleepcheck PASS; bindeps FAIL (golang.org/x/sys/unix reaches the linux/darwin binaries; this branch changes nothing in cmd/qompack's dependencies; not checked on b070bbe); stubskips was stopped by me because it launches a whole-tree go test
- `go test -count=1 -timeout 30m -run 'TestFault_PublicationBoundaries/(config_json_corrupt|delivery_seal_torn_slot)$' -v ./test/fault (Windows, unchanged code)` — exit 0; delivery_seal_torn_slot 39.23 s, config_json_corrupt 63.54 s; no hook over 5 s (runs/w2-win-fault-slow-rows-baseline.log)
- `same plus roots_last_line_truncated, with the temporary timing patch runs/w2-win-fault-slow-rows-diag.patch (reverted afterwards)` — exit 0; every hook <= 1.052 s; config waitDaemonUp=false after 1m0.003s; seal waitIndexed=false after 30 s; control row 5.40 s (runs/w2-win-fault-slow-rows-diag.log)
- `go run ./tools/devtool check-commit-msg <msg> for bab2f4a 69678a9 14e5ed8 8056346` — all exit 0

### Criterion changes

- test/security requireFileVersion (bab2f4a): it now waits for the file version under its store key, paths.KeyFold(rel, paths.DefaultFold()), instead of the display path. Rationale: the observer writes, and re_read serves, a Read's version under paths.Key, which lowercases paths on Windows and macOS by design (section 4). On such a host the entry docs/notes.md IS the version of docs/NOTES.md. The wait still requires a recorded version of exactly that path, and a version of any other path does not satisfy it (pinned by TestHarness_FileVersionPredicateReadsTheObserversKey's negative arm). No bound, assertion, skip or golden changed.
- No criterion change in test/fault. The faster check for the two slow rows is only a proposal awaiting an owner decision.

### Open issues

- TestFault_PublicationBoundaries: config_json_corrupt (~60 s, daemonUpBound) and delivery_seal_torn_slot (~30 s, indexBound) are still slow. The cause is the harness's own waits for absence; the fix is the proposal above.
- test/fault/fault.go:784-788 declares absentDaemonBound but nothing uses it.
- devtool lint bindeps reports golang.org/x/sys/unix reaching the linux/darwin binaries. This branch does not cause it, but I did not check it on b070bbe.
- devtool lint's stubskips check runs a whole-tree go test. Workstreams under the never-./... rule should leave it out of --only.
- delivery_seal_torn_slot, by design: after a half-present seal pair, recording does not resume until an operator recovers it. New deliveries are kept durably in the WAL and client spool but never indexed, and LOUD plus the l0_delivery_unleased counter report it (explicit_incomplete).
- test/security keeps the existing Windows skip captured_file_replaced_by_link_outside, because creating symlinks needs SeCreateSymbolicLinkPrivilege or Developer Mode.

### Needs the owner

- Whether to adopt the proposed faster check in test/fault recoverSession for config_json_corrupt and delivery_seal_torn_slot. It skips the wait only on positive refusal evidence (self-test config.capture critical plus the id in no file under .qompack/; or an l0_delivery_unleased rise plus the id retained in WAL or spool and absent from the index) and keeps the full daemonUpBound/indexBound as the fallback. The narrower alternative is absentDaemonBound (10 s) for the config row only. Either one is a criterion change in test/fault.

## Independent review

### review:wintriage: sound


## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


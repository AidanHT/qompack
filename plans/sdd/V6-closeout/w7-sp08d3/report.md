# w7-sp08d3: SP08-D3 status, C1.4 and C1.5 confirmations

Branch `closeout/w7-sp08d3`. Workflow `wf_0b499fad-a33`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `partial`, head `fd946b01`

### Root cause

SP08-D3 is only partly fixed. The recovery works: a replayed prompt is now captured verbatim under its lease, and turns follow arrival order for leased arrivals. But a prompt that reached only a hook's client spool gets its lease only when a drain reaches it, and nothing orders a replay by req.TS. Its turn is therefore its publication position, not the order the host sent it. Two paths put the host's second prompt at prompt_<s>_0, which the rehydrator serves as the verbatim original: (a) the first prompt was spooled after a failed dial and the second arrived live and was published before the spool was drained; (b) both prompts were spooled, each by its own hook process, and ipc.SpoolFiles drains client-<pid>.ndjson files in string order, which says nothing about time. readL0Intent (internal/rehydrate/items.go:421-435) checks only the record's session and turn, so the substitution is injected silently. Acceptance item 2 allows two resolutions, a req.TS order or a rehydrator notice; neither exists, and no owner ruling excludes host order.

### Summary

Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-sp08d3, branch closeout/w7-sp08d3, base 898bb8b. The three confirmations came out as follows. C1.4 and C1.5 pass on Windows and Linux without weakening. C2.1 (SP08-D3) is not fixed: the row stays deferred:V6-VERIFY, and it now names a new evidence test that pins the leftover defect.

== (1) C2.1 SP08-D3: NOT fixed, left deferred ==
The existing evidence test TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero was already inverted: it asserts the fixed behaviour for a single spooled prompt, and it passes. I checked each acceptance item in plans/V2-SP-08-carried-defects.md against the candidate:
- Item 1 (one capture per ObservationID, acknowledgement follows the capture): met.
- Item 2 (the session's first prompt is prompt_<s>_0 even when it arrives by replay): not met for client-spooled prompts. See root_cause.
- Item 3 (HotSpool ruling): never ruled. Capture-by-drain is what ships, but it does not keep turn order.
- Item 4 (SP08-D2 closed, including the derived index-before-link cut): met. TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart and TestDerivedPublication_V6_MissingLinkAfterSupersessionDrift are green.
- Item 5 (evidence inverted, X1 reconciled): met, but test/e2e/v5_x01_test.go still has comments saying a WAL replay runs only the sentinel scan. Those comments are stale.

Evidence for the defect: a temporary diagnostic (not committed) first reproduced it. prompt_0="second" and prompt_1="first", with first.TS=1790561204106 below second.TS=1790561204241. It became the committed test TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero in internal/daemon/drain_prompt_capture_test.go. The test runs through the real daemon and has two subtests, live_second and spool_file_order. It asserts today's wrong outcome on purpose, so a fix must invert it. It passes on Windows (-count=10) and on Linux (non-root, -race, -count=5).

What I changed:
- plans/CARRIED-DEFECTS.tsv, row SP08-D3: evidence switched to the new test, summary rewritten to name the leftover defect precisely. Status stays deferred:V6-VERIFY.
- The SP08-D3 section of plans/V2-SP-08-carried-defects.md: added a dated close-out note with each item's state, the evidence, and the two rulings the owner can choose between.

Why switch the evidence: the old evidence test asserts the fixed half, so if someone later fixed the rest it would stay green and nothing would flag the row as stale. The new test restores the guard's "cannot be silently fixed" property.

Guard results for this row: TestCarriedDefects_ManifestIsWellFormed/SP08-D3 and TestCarriedDefects_OpenRowsHaveLivingEvidence/SP08-D3 pass. TestCarriedDefects_WaveReportRequiresResolution/SP08-D3 fails, as expected, because plans/V6-report.md exists. It fails the same way for SP06-D2, SP08-D1, SP09-D1, SP10-D1 and SP20-D2, and it failed that way before this change.

How far the defect reaches (from reading the code, not measured):
- The client-spool watcher (C1.13) narrows the live case to a live prompt sent within about two spoolCheckInterval (2 s) of the next request the daemon serves.
- HotSpool, and daemon.enabled=false, spool every prompt, one file per hook process. In HotSpool the hooks never dial, so nothing kicks the watcher, the files pile up, and they are replayed in file-name order.
- Cold start is not affected: the startup Drain runs before Serve.

Tests on Windows (go1.26.6, loaded machine), all PASS:
- go test -count=1 -timeout=30m -v -run '^(TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero|TestSP08D3_DistinctSameTextRepliesStayDistinct|TestSP08D3_CaptureFailureIsNotAcknowledged|TestSP08D3_ReplayThroughRunIngestedEmitsNoOutput|TestPromptOrder_V6_OrderingGateGivesTurnZeroToEarliestLeasedArrival|TestPromptOrder_V6_UnleasedEarlierSpooledIsSeparateUncertainty|TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock)$' ./internal/daemon/ — the same invocation also covered the C1.4 and C1.5 daemon rows.
- go test -count=1 -v -run '^(TestV6Prompt_.*|TestDerivedPublication_V6_.*|TestOnUserPrompt_.*|TestVerbatimPromptID)$' ./internal/observer/
- go test -count=1 -v -run '^(TestE2E_VerbatimPromptSurvivesRestart|TestE2E_ObserverThroughDaemon|TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently)$' ./test/e2e/

Tests on Linux (container, non-root uid 10001, -race):
- linux-nonroot-gate.sh --prefix cx-w7-sp08d3 on 898bb8b, label focused, running the union of all the patterns above across ./internal/daemon ./internal/observer ./internal/mcp ./test/e2e: daemon 18, observer 29, mcp 3, e2e 3 passed, 0 failed.
- On 7eca106, label residual: 20/20 passed.

Phase 3 coverage: the internal/daemon and internal/observer rows are in win-tree, win-race and linux-tree. The e2e rows (TestE2E_*, TestV3_CrashRecovery...) are in win-tree and linux-e2e.

== (2) C1.4: 3dab390's two fixture corrections hold ==
3dab390 is an ancestor of the candidate. TestDeliveryMigration_OrphanArtifactsPreserveLegacyAnchorsWithoutContinuing and TestCrashCutBetweenReferenceAndFrontierRedelivers both pass on Windows and Linux (-race, non-root), in the same focused runs.

Neither correction weakened its row:
- The migration row changed its criterion from "legacy anchors still authorize continuation" to "refuse with ErrDegraded and keep every anchor file byte-identical". That is fail-closed and stronger. Since then, 2e99fdd only wraps the fixture in legacyFixture, because rollover is now on by default and the fixture must build a legacy store. The assertions are unchanged.
- The crash-cut row now cuts once the reference callback has run (calls()>0) instead of whenever the lease exists. The old trigger could fire before dispatch now that the ordering gate reads the journal. It also gained an assertion that the token is not acknowledged before the retry. It still proves the reference-to-frontier cut, with the offset held at 0, the spool retained, and the identity reused on restart.

Phase 3 coverage: win-tree, win-race, linux-tree.

== (3) C1.5 V6-AUTH-1/2 (inventory rows 1.13.4, 1.17.12): pass ==
- internal/mcp: TestV6_LostFilePathDoesNotBecomePathlessAuthority, TestV6_DedupDoesNotLaunderARestrictedHash and TestV6_HashRefusesStoreWithoutProvenance pass on Windows and Linux.
- internal/daemon capture scope: TestDispatchOp_OutOfProjectDirectIPCDoesNotReachWAL and all nine TestAdmitDelivery_ tests pass on both.
- test/security, whole package: Windows ok (176.7 s, 20 top-level tests; one subtest, TestSecurity_ArchivedRetrievalCannotBeWalkedOutsideTheProject/captured_file_replaced_by_link_outside, skipped because this Windows account lacks the symlink privilege, which is pre-existing). Linux: pass=30, fail=0, skip=0, and that subtest ran there.
- The historically failing real-capture test TestV6_HashAddressesDoNotBypassPathAuthorization now reports all three probes (v6_escape_tool_use_id, v6_escape_root_hash, v6_escape_chunk_hash) as verified-denied on both OSes; root_hash and chunk_hash were the ones that served content in the V6-VERIFY run.

One older criterion change to know about (not mine): 00e0c98 changed TestV6_ArchivedReadRetainsItsAuthorizationBoundary to plant the legacy stored shape, because new captures now refuse an out-of-project Read before anything is persisted. The real-capture half is covered by TestSecurity_OutOfProjectCaptureIsRefusedBeforePersistence, which drives the real hook and sweeps .qompack for both the bytes and the path; it passes on both OSes. TestGuard_NoNetworkImports was not re-run.

Phase 3 coverage: the test/security rows are in win-tree and linux-tree (the real-capture rows need the packaged bundle, which the package builds itself). The mcp and daemon rows are in win-tree, win-race and linux-tree.

== Other checks ==
- devtool fmt-check: 0. go vet ./internal/daemon/ on Windows and GOOS=linux: 0.
- lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns: 0.
- go test ./test/docs: ok.
- internal/daemon full package on Windows, run once while two other workstreams shared the machine: one failure outside my change. See open_issues.

Evidence logs are committed under plans/sdd/V6-closeout/w7-sp08d3/runs/. My processes have all ended, and no load generator was used.

### Commits

- 3755a827 test(daemon): pin the SP08-D3 client-spool turn-order residual
- 7eca1067 docs(plans): record the SP08-D3 close-out check and keep it deferred
- fd946b01 chore(v6): add w7-sp08d3 confirmation evidence logs

### Tests

- `go test -count=1 -timeout=30m -v -run '^(TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero|TestSP08D3_DistinctSameTextRepliesStayDistinct|TestSP08D3_CaptureFailureIsNotAcknowledged|TestSP08D3_ReplayThroughRunIngestedEmitsNoOutput|TestPromptOrder_V6_OrderingGateGivesTurnZeroToEarliestLeasedArrival|TestPromptOrder_V6_UnleasedEarlierSpooledIsSeparateUncertainty|TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock|TestDeliveryMigration_OrphanArtifactsPreserveLegacyAnchorsWithoutContinuing|TestCrashCutBetweenReferenceAndFrontierRedelivers|TestDispatchOp_OutOfProjectDirectIPCDoesNotReachWAL|TestAdmitDelivery_.*)$' ./internal/daemon/ (Windows, 898bb8b)` — PASS, 18 tests, exit 0
- `go test -count=1 -v -run '^(TestV6Prompt_.*|TestDerivedPublication_V6_.*|TestOnUserPrompt_.*|TestVerbatimPromptID)$' ./internal/observer/ (Windows)` — PASS, exit 0
- `go test -count=1 -v -run '^(TestV6_LostFilePathDoesNotBecomePathlessAuthority|TestV6_DedupDoesNotLaunderARestrictedHash|TestV6_HashRefusesStoreWithoutProvenance)$' ./internal/mcp/ (Windows)` — PASS, 3 tests
- `go test -count=1 -timeout=30m -v ./test/security/ (Windows)` — PASS, ok in 176.7 s; one subtest skipped for lack of the symlink privilege (pre-existing)
- `go test -count=1 -v -run '^(TestE2E_VerbatimPromptSurvivesRestart|TestE2E_ObserverThroughDaemon|TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently)$' ./test/e2e/ (Windows)` — PASS, 3 tests
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-sp08d3 ... 898bb8b focused --timeout 30m --run <union of all patterns above plus the e2e rows> -- ./internal/daemon ./internal/observer ./internal/mcp ./test/e2e` — PASS, non-root, -race: daemon 18, observer 29, mcp 3, e2e 3 passed, 0 failed
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-sp08d3 ... 898bb8b security --timeout 30m -- ./test/security` — PASS, 30 passed, 0 failed, 0 skipped
- `go test -count=10 -run '^TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero$' ./internal/daemon/ (Windows)` — PASS 10/10; the test deliberately pins the current wrong outcome
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-sp08d3 ... 7eca1067 residual --count 5 --run '^(TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero|TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero)$' -- ./internal/daemon` — PASS, 20/20, -race, non-root
- `go test -count=1 -v -run '^(TestCarriedDefects_ManifestIsWellFormed|TestCarriedDefects_OpenRowsHaveLivingEvidence|TestCarriedDefects_WaveReportRequiresResolution)$' ./test/guards/` — SP08-D3: ManifestIsWellFormed and OpenRowsHaveLivingEvidence pass. WaveReportRequiresResolution fails as expected, because V6-report.md exists; it fails the same way for SP06-D2, SP08-D1, SP09-D1, SP10-D1 and SP20-D2 as before this change
- `go test -count=1 -timeout=30m ./internal/daemon/ (Windows, whole package, machine loaded by other workstreams)` — FAIL: one test, TestStageBinary_ConcurrentSpawnersAgree (sharing violation); it passes alone -count=3 and -count=20; unrelated to this change
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon/ (Windows and GOOS=linux); go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns; go test ./test/docs` — all exit 0

### Criterion changes

- The evidence column of row SP08-D3 in plans/CARRIED-DEFECTS.tsv moved from TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero, which asserts the fixed half and still exists and passes, to the new TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero, which pins the remaining defect. Reason: while the row is unresolved, the file header wants a test that pins the current wrong behaviour so that fixing it fails the test. An evidence test that already asserts the fixed half would stay green if someone fixed the rest silently. No assertion was loosened, and no test was removed or skipped.

### Open issues

- SP08-D3 remains deferred:V6-VERIFY and still blocks the V6 report through TestCarriedDefects_WaveReportRequiresResolution/SP08-D3. It needs the owner ruling listed under needs_owner, then either a req.TS order or a rehydrator notice; either one must invert TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero.
- TestStageBinary_ConcurrentSpawnersAgree failed once in the loaded Windows whole-package run of internal/daemon, at spawn_stage_test.go:179, spawner 4. Error: 'rename .stage-94550389 -> qompack.exe: Access is denied (and the existing file: open ... qompack.exe: The process cannot access the file because it is being used by another process)'. It passes alone (-count=3, -count=20). It is outside this task's scope and unrelated to my test-only change. It may be a real concurrent-staging hazard under load (C1.17 staged copies), or a Defender scan holding the new exe (D32). Assign it before win-tree.
- Stale comments in test/e2e/v5_x01_test.go (around lines 71-72) still say a WAL replay of observe.prompt runs only the sentinel scan. That stopped being true with the V6 prompt-replay fix. Comment only; not changed because it is outside this task's scope.

### Needs the owner

- SP08-D3 acceptance item 2 needs a ruling. Option A: order prompt replays by req.TS per session, across client spools and against the live lane. At minimum, replay client spools by their first record's TS instead of client-<pid> file-name order. Option B: rule host order out and make the rehydrator honest. When a later-turn prompt of the session carries an earlier req.TS than prompt_<s>_0, emit a Warn and a drop entry naming the substituted turn, and drop the §8.5 provenance claim. Either option inverts the new evidence test and lets the row move to fixed. Leaving it as is leaves the row deferred, or it can be recorded wontfix with this residual documented.
- SP08-D3 acceptance item 3 (HotSpool) was never ruled: exempt observe.prompt from the client-side HotSpool short-circuit, or accept capture-by-drain. Capture-by-drain is what ships today, and while item 2 stands it replays HotSpool prompts in client-<pid> file-name order.

## Independent review

### review:sp08d3: needs-fixes

- **minor** `internal/daemon/drain_prompt_capture_test.go:185-187 (TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero)` — The test's claim that the host sent "first" first is not written into req.TS, the one field a fix would read. spD3Prompt stamps TS with core.NowMilli(dd.clk), and wireTestDaemon uses the real clock. Two back-to-back calls usually return the same millisecond, and require.LessOrEqual accepts a tie. A future Option A fix (order replays by req.TS, the first resolution the new detail section offers) cannot tell a tie apart from host order. In spool_file_order it would still fall back to file-name or stable order, so the pin can stay green or flip at random after a correct fix. That defeats the reason the evidence was switched: so that fixing the defect fails the test.
  - Evidence: spD3Prompt: `TS: core.NowMilli(dd.clk)`. wireTestDaemon: `NewOptions(root, testConfig())` with no fake clock. The test uses `require.LessOrEqual(t, first.TS, second.TS, ...)`. The implementer's diagnostic had a 135 ms gap, but the committed test does not enforce any gap.
  - Fix: Make host order explicit in the data. After building the two requests, set `second.TS = first.TS + 1` (or build both from a fixed base TS), and change the check to `require.Less(t, first.TS, second.TS)`. Then a req.TS-ordered fix reliably inverts both subtests.
- **nit** `plans/sdd/V6-closeout/w7-sp08d3/runs/win-residual-pin.log:1-6` — The committed Windows -v evidence log records a test name from before the rename, TestCarriedDefect_SP08D3_SpooledFirstPromptLosesTurnZeroToALiveSecond, not the committed TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero. The only Windows log for the committed name is win-residual-pin-x10.log, and it holds just 'ok ... 8.437s' with no test names. The Windows claim for the residual pin is therefore not shown by a log under the name the TSV row cites. (I re-ran it: -count=3 ok on this HEAD.)
  - Evidence: win-residual-pin.log line 1: `=== RUN   TestCarriedDefect_SP08D3_SpooledFirstPromptLosesTurnZeroToALiveSecond`. The TSV evidence column names TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero.
  - Fix: Re-run `go test -count=1 -v -run '^TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero$' ./internal/daemon/` and replace win-residual-pin.log with its output (or delete the stale log).

## Fix seat (review resolution) — status `done`, head `7742d41e`

### Root cause

The reviewer's finding was correct. The SP08-D3 residual pin took host order from two real-clock NowMilli stamps and accepted a tie (LessOrEqual), so a fix that orders replays by req.TS could not reliably invert it. The underlying SP08-D3 residual is unchanged: nothing orders a replay by req.TS, and client spools drain in client-<pid> file-name order.

### Summary

FIX seat, w7-sp08d3, branch closeout/w7-sp08d3 on closeout/integration 898bb8b. The reviewer's one finding (minor) is correct and is now fixed. The rest of the implementer's work is unchanged.

Outcome of the three confirmations:
- C2.1 SP08-D3: still DEFERRED. The V6 replay recovery does capture every replayed prompt. On Windows and Linux, TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero passes, as do the focused TestSP08D3_* cases and the e2e rows TestE2E_VerbatimPromptSurvivesRestart, TestE2E_ObserverThroughDaemon and TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently. Acceptance item 2 is still not met for prompts that were spooled on the client side. The ordering gate only orders leased arrivals, and no replay is ordered by req.TS. So the host's second prompt becomes prompt_<s>_0 in two cases: when the first prompt was spooled and the second arrived live, and when both were spooled and client-<pid> file-name order sorts the second one first ("client-10" before "client-9"). TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero (commit 3755a827) pins this residual. The TSV row and the detail doc plans/V2-SP-08-carried-defects.md record it (commit 7eca1067).
- C1.4: the two fixture corrections in 3dab390 (internal/daemon/delivery_migration_guard_test.go and delivery_publication_test.go) still pass. The internal/daemon package passed in full on Windows (my run: ok, 304s) and in the implementer's Linux non-root -race runs. In the implementer's full Windows daemon run, TestStageBinary_ConcurrentSpawnersAgree failed once under co-load. Run alone 20 times it passed every time (runs/win-stagebinary-x20.log), and it also passed in my full run. The implementer found that the corrections do not weaken what the rows prove.
- C1.5 V6-AUTH-1/2: the security regression tests pass on Windows and Linux. That is the whole test/security package (Linux 30/30) and the internal/mcp tests TestV6_LostFilePathDoesNotBecomePathlessAuthority, TestV6_DedupDoesNotLaunderARestrictedHash and TestV6_HashRefusesStoreWithoutProvenance.

Phase 3 coverage on the frozen candidate: the daemon and observer rows are covered by win-tree, win-race and linux-tree; the e2e rows by linux-e2e (and linux-e2e-timing for the restart and crash-recovery rows); test/security and internal/mcp by win-tree and linux-tree.

## Review resolution
- Finding (minor): the SP08-D3 residual pin did not write host order into req.TS. **Confirmed and fixed** in 9968f131. spD3Prompt stamps TS with core.NowMilli on the real clock, so two calls in a row can land on the same millisecond, and the test's require.LessOrEqual accepted that tie. A future fix that orders replays by req.TS could not tell host order from a tie, so the pin could stay green, or flip at random, after a correct fix. The test now sets `second.TS = first.TS + 1` and uses `require.Less(t, first.TS, second.TS, ...)`, with a comment giving the reason. This hardens the check and weakens nothing: both subtests still assert today's wrong outcome, and they will now reliably fail once a req.TS-ordered fix lands. There was no failing test to write first, because the change sharpens a pin of a defect that is still present rather than fixing product code.

## What I ran (fix seat)
- `go run ./tools/devtool fmt-check`, `go vet ./internal/daemon`, `GOOS=linux go vet ./internal/daemon`: clean.
- Windows, `go test ./internal/daemon -run 'SP08D3' -count=5 -v`: exit 0, all 5 SP08D3 tests passed on all 5 counts (runs/win-fix-sp08d3-x5.log).
- Windows, `go test ./internal/daemon -count=1 -timeout=30m`: ok, 304s (runs/win-fix-daemon-full.log).
- Linux non-root -race, `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-sp08d3 ... 9968f131 sp08d3-pin-fix --run 'SP08D3' --count 5 -- ./internal/daemon`: pass=35, fail=0, go_test_exit=0 (runs/linux/cx-w7-sp08d3-sp08d3-pin-fix-9968f13-*).
- Windows, `go test ./test/guards -run '^(TestCarriedDefects_ManifestIsWellFormed|TestCarriedDefects_OpenRowsHaveLivingEvidence)$' -count=1 -v`: ok, and both SP08-D3 subtests passed (runs/win-fix-guard-sp08d3-rows.log). <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Windows, `go test ./test/guards -run 'CarriedDefect' -count=1`: FAIL, on TestCarriedDefects_WaveReportRequiresResolution only. That red was already expected and is known: it is item O4 in plans/sdd/V6-closeout/linux/report.md, and it fails for all six unresolved rows because plans/V6-report.md exists. The implementer's run showed the same result (runs/win-guard-carried.log). It is not caused by this change (runs/win-fix-guard-carried.log).
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.

## Criterion changes
None. The pin went from LessOrEqual to a strict Less on host order stamped explicitly into req.TS; the rationale is above.

I started no background processes and left none running. The Linux /work dir is cx-w7-sp08d3-* only.

### Commits

- 3755a827 test(daemon): pin the SP08-D3 client-spool turn-order residual (implementer)
- 7eca1067 docs(plans): record the SP08-D3 close-out check and keep it deferred (implementer)
- fd946b01 chore(v6): add w7-sp08d3 confirmation evidence logs (implementer)
- 9968f131 test(daemon): stamp host order into the SP08-D3 residual pin (fix seat)
- 7742d41e chore(v6): add w7-sp08d3 review-fix evidence logs (fix seat)

### Tests

- `go test ./internal/daemon -run 'SP08D3' -count=5 -v (Windows)` — PASS, exit 0, 5 tests x5
- `go test ./internal/daemon -count=1 -timeout=30m (Windows)` — ok 304s
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-sp08d3 --repo C:/... --out C:/.../runs/linux 9968f131 sp08d3-pin-fix --run 'SP08D3' --count 5 -- ./internal/daemon` — PASS pass=35 fail=0, non-root -race
- `go test ./test/guards -run '^(TestCarriedDefects_ManifestIsWellFormed|TestCarriedDefects_OpenRowsHaveLivingEvidence)$' -count=1 -v` — ok (SP08-D3 subtests PASS) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/guards -run 'CarriedDefect' -count=1` — FAIL, but only TestCarriedDefects_WaveReportRequiresResolution, an expected red (O4) until the V6 report gives final dispositions
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `go vet ./internal/daemon; GOOS=linux go vet ./internal/daemon; go run ./tools/devtool fmt-check` — clean

### Criterion changes

- The SP08-D3 residual pin's host-order check is stricter: second.TS = first.TS + 1 with require.Less instead of LessOrEqual on two real-clock stamps. This hardens the check and loosens nothing.

### Open issues

- SP08-D3 stays deferred: prompts spooled on the client side are captured in publication order, not host order (acceptance item 2), pinned by TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero.
- TestCarriedDefects_WaveReportRequiresResolution stays red until the V6 report gives final dispositions for the unresolved carried rows (O4). This was expected already.
- TestStageBinary_ConcurrentSpawnersAgree failed once in the implementer's co-loaded full Windows daemon run. It passed 20 times alone and in my full run; it is load-sensitive, not a regression.

### Needs the owner

- SP08-D3 acceptance item 2 needs a ruling. Option A: order prompt replays by req.TS per session, across client spools and against the live lane. At minimum, replay client spools by their first record's TS instead of client-<pid> file-name order. Option B: rule host order out and make the rehydrator honest. When a later-turn prompt of the session carries an earlier req.TS than prompt_<s>_0, emit a Warn and a drop entry naming the substituted turn, and drop the section 8.5 provenance claim. Either option inverts the evidence test and lets the row move to fixed. Leaving it as is keeps the row deferred, or it can be recorded wontfix with this residual documented.
- SP08-D3 acceptance item 3 (HotSpool) was never ruled: exempt observe.prompt from the client-side HotSpool short-circuit, or accept capture-by-drain. Capture-by-drain is what ships today, and while item 2 stands it replays HotSpool prompts in client-<pid> file-name order.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



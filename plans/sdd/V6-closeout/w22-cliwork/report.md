# Wave 22 cliwork seat

Branch `closeout/w22-cliwork`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **minor** `internal/cli/status_connect_test.go:29-34 (header); TestStatus_TransientConnectMissStillReadsTheDaemon, TestStatus_CallDeadlineExpiryStillSaysSilent, TestStatus_RepeatedReadsOfUnchangedStateAgree`: Three status rows still need a real named-pipe connect within commandConnectDeadline (250 ms). D61(c) says 'no row may depend on a wall-clock margin'. The seats argue the margin is the product's own budget, but no ledger row accepts the exception. These run in the pre-freeze `internal` step and in release-check's -p ~22 shared pass.
- **minor** `internal/checkpoint/writer.go:949 (d.setGoalTurnLocked(turn, true) in the graph fallback); no covering row in current_work_test.go`: Nothing in the suite checks the graph fallback recording the goal's turn. Removing the call (mutant M40) leaves every test green, but it is not an equivalent mutant: without it the never-move-backwards guarantee breaks. Wave 20's review said this mutant was 'effectively equivalent given ascending segment encoding'. That is wrong, because a later records walk is gated by the same goalTurn.
- **minor** `internal/checkpoint/writer.go:946 (`!d.workExplicit` in `if !d.promptsAnswered && !d.workExplicit`)`: No test covers §7's rule that SetCurrentWork stops derivation on the graph-fallback path. Since wave 19, the fallback runs only while SessionPrompts cannot answer. TestExplicitCurrentWorkSurvivesAPromptRefresh uses the shipped store, where SessionPrompts answers, so the explicit-work guard in the fallback is never exercised. Deleting it (mutant M37) leaves the suite green. A regression would overwrite an explicit current work with a derived prompt during a degraded window, and a compaction then would seal it.
- **minor** `internal/checkpoint/intent.go:304 (goal walk reads limit 0) with intent.go:626 handOff and finalize.go:239-240 (successor Begin inside Finalize)`: When the newest prompt is a large paste (up to hookCaptureMaxBytes = 4 MiB), the goal walk reads it whole and runs it through fromStore once per draft. Finalize opens the successor draft synchronously inside PreCompact, and handOff passes on only promptText, which never holds an oversized prompt. goalSeen and oversized are not handed off. So every compaction re-reads the 4 MiB on the hook path: once in the successor's Begin, and once more for a cold PreCompact. Candidate 7 read only a 28 KB bounded prefix there. Per-refresh cost is unchanged.
- **minor** `.github/workflows/ci.yml:308 (test-e2e: go test -count=1 -timeout=30m ./test/e2e); plans/sdd/V6-closeout/coordinator/phase3.sh:84 (win-e2e-timing, -timeout=30m)`: On Windows, the whole test/e2e binary leaves only about 15% headroom under its 30 min -timeout, and the night's win-e2e-timing and hosted test-e2e run all of it in one binary. Today, under co-load, e2efunc took 24.4 min even with X11 (about 10-11 min alone), SessionStartLatency and X10 skipped. A quiet run of all of test/e2e has historically taken 25.2-25.5 min. If the host is slower tonight, the run can be killed at 30 m: that is a timeout red, not a product red. This is not a c8 regression, since test/e2e gained one row since d20309c0 (TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead).
- nit `internal/checkpoint/intent.go:270 (`!d.goalTurnSet ||`) and intent.go:323-325 (`if !set { turn = 0 }`)`: Both are dead code. goalTurn is always 0 while goalTurnSet is false (setGoalTurnLocked normalises it, and resumeDraft leaves it 0), so `turn >= d.goalTurn` already holds. Every caller passing set=false also passes turn 0. Mutants M07 and M19 survive as equivalent mutants. M05 (`>=` to `>`) is equivalent too: the same turn means the same record and the same goal.
- nit `internal/checkpoint/intent.go:273 (`case complete && len(own) <= goalWalkLimit:`)`: The clear case only works because it comes after case 1. It does not itself require !found, so any future narrowing of case 1 would make a found goal be cleared. Mutant M06 (dropping `complete ||` from case 1) shows this: a stale goal_turn (restored backup) clears the goal on one refresh and only recovers on the next. It survives because every row asserts the sealed checkpoint after two or more refreshes (Begin/Advance plus PreCompact's RefreshIntent), which hides that one-refresh behaviour.
- nit `internal/checkpoint/intent.go:386 (isCommandToken character set) and :350 (qompackCommandPrefix)`: Parts of the bare-command rule have no tests. Removing '.', '_' or digits from the allowed set, accepting a bare '/', or dropping the ':' from '/qompack:' all leave the suite green, so a bare '/my_cmd', '/cmd2' or '/foo.bar' is not pinned either way. A BOM-led '﻿/qompack:status' is taken as the goal because TrimSpace does not strip U+FEFF. That is cosmetic and unlikely from the host.
- nit `internal/checkpoint/writer.go:946 (promptsAnswered gate) and intent.go:194`: Nothing shows that the graph fallback is skipped while the records answer. Forcing promptsAnswered to false (mutants M02, M36, M50) does not change any sealed result, because the records walk that follows has the last word. The only observable difference is the fallback's extra ToolUse and Open reads on every encoded segment. These are equivalent in output, so this is a cost and clarity gap, not a correctness one.
- nit `internal/checkpoint/writer.go:1626 firstSentence via goalOf (intent.go:357)`: This predates candidate 8 and is unchanged from candidate 7, but it is now visible more often because the goal follows the open segment. The goal is not trimmed: a prompt '   \nFix it.' gives goal '   \nFix it.', and '. Then fix it.' gives goal '.', which section 5 then renders as the task in flight.
- nit `internal/cli/status_connect_test.go:521-527 (TestStatusProbe_HasTheCommandConnectBudget); internal/cli/qompack_commands.go:315`: The test is tautological: `const statusProbeTimeout = commandConnectDeadline`, so it can fail only if that declaration is edited. status_probe_windows_test.go:31-33 says this row 'pins the budget's value', but it pins equality, not 250 ms.
- nit `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-int/.qompack/spool (16 client-*.ndjson, up to 520 KB, last written 2026-10-03 14:10); internal/cli/main_test.go:83-95`: Stale checkout stores left by pre-fix cli runs (every record has the cli testClock t=1786530600000) sit in the integration worktree, and the same holds for qompack-cx-cand and qompack-v6. Because <root>/.qompack exists, TestMain's guard runs in its lenient mode, watching only its own pid's file, in any local run in cx-int, such as prefreeze on $INT. Hosted CI and fresh clones run the strict mode instead.

## impl:cliwork: status `done`, head `dbfbe97d46140a0bc9029e3da59b36a7529d5227`

### Summary

All five assigned findings (#80, #23, #24, #25, #84 with the ci.yml half of #53) are fixed on closeout/w22-cliwork in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-cliwork, 6 commits off 2bf29705. I also closed one nit (M06). Nothing is pushed or merged, and I touched no other worktree.

#80, the status rows (8be0dae5). Three named rows and one more of the same kind needed a real dial to connect inside the product's 250 ms budget. The fourth is TestStatusSource_ASilentDaemonIsNotReportedAbsent: if its real probe misses, the daemon reads as absent. A new helper, useHangGuardedStatusDials in status_connect_test.go, swaps the command client's connect and write budgets, and the liveness probe's budget, to bootstrapCallDeadline (5 s). That is a hang guard, not a margin, and the rows' other admin round trips already use it. None of the four rows is about the budget's value. TestCommandClient_HasItsOwnConnectBudget, TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon and TestStatusProbe_HasTheCommandConnectBudget pin that without a real dial. Red first: with a scratch simulated slow host (every dial takes 300 ms, applied and then reverted), all four rows fail on 2bf29705 and pass on 8be0dae5. I changed no product code; fetchDaemonStatus belongs to the contract seat. Also touched: status_order_test.go and status_reason_test.go, one call each.

#23 and #24, the fallback mutants (b1d6d326). Two new rows in current_work_test.go, both green on base:
- TestFallbackGoalTurnGatesALaterIncompleteWalk fails under M40. Both its goal_turn assertion and its behaviour assertion catch it: current work steps back to the turn-4 prompt. Wave 20's "effectively equivalent" reading of M40 is wrong, as the commit body says.
- TestExplicitCurrentWorkSurvivesTheGraphFallback fails under M37.
- Sweep of the fallback's other guards: dropping the turn gate, dropping its !goalTurnSet disjunct, dropping the goal assignment, or dropping deriveCurrentWorkLocked's workExplicit return is each already killed (3, 2, 9 and 3 failing rows). The promptsAnswered gate stays equivalent in output (nit).

#25, the large paste re-read (bc7b088e, row tightened in dbfbe97d). handOff and takeHandoff now carry a promptHandoff with the prompt texts, goalSeen and the oversized record id. Records are immutable, so the successor that Finalize opens inside PreCompact derives the same current work and intent without reading the paste again.
- The cost row is TestPreCompactReadsAnOversizedNewestPromptOnce, with a paste four times the read limit. On 2bf29705 a warm compaction read the paste whole once (want 0) and a cold one twice (want 1). Both pass now.
- After one co-loaded -count=20 failure, the row now runs PreCompact's own steps with no wall-clock budget, and it is still red on base.
- Sweep: the original and the inherited prompts were already handed on through the text cache. The graph fallback's whole read (ownNewestGoal) runs only in an idle Advance while the prompt list is degraded, once per encoded segment, and is not on the hook path.

#84 and #53's ci.yml half (b77e7ce3). test-e2e's -timeout goes from 30m to 45m, with the rationale in a comment. Measured: windows-latest 1188 s (run 36816905394), the quiet Windows reference host 1513.7 s (c5) and 1529.5 s (c6 win-e2e-timing), and a co-loaded local run that hit 1800 s. 45m is 2700 s, which is 2.3 times the hosted figure and 1.8 times the quiet local one. nightly.yml runs only eight named e2e rows under -race at 15m, not this binary, so it is unchanged. The value for the night seat's phase3.sh win-e2e-timing copy is 45m.

Nit M06 (7afeca6c). The goal walk's clear case now requires !found itself, with no behaviour change. TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal now also checks the goal after the resuming Begin alone, which fails under M06. I skipped the other nits: they are optional, and status_probe_windows_test.go belongs to the contract seat.

No check was weakened. The one criterion change is recorded below.

### Commits

- 8be0dae5 test(cli): hang-guard the status rows' real dials
- b1d6d326 test(checkpoint): pin the graph fallback's goal turn and guard
- bc7b088e perf(checkpoint): hand the goal memo to the successor draft
- b77e7ce3 ci: give test-e2e's whole-package binary a 45m hang guard
- 7afeca6c fix(checkpoint): the goal walk never clears a goal it found
- dbfbe97d test(checkpoint): take the read-count row off the wall clock

### Findings resolution

- **fixed**: #80 three status rows need a real named-pipe connect within commandConnectDeadline (250 ms)
  - 8be0dae5. The real dials and the probe in TestStatus_TransientConnectMissStillReadsTheDaemon, TestStatus_CallDeadlineExpiryStillSaysSilent, TestStatus_RepeatedReadsOfUnchangedStateAgree, and the same-kind TestStatusSource_ASilentDaemonIsNotReportedAbsent now use a hang guard (useHangGuardedStatusDials, bootstrapCallDeadline). Red first: with a scratch simulated slow host (every dial 300 ms), all four fail on 2bf29705 and pass on 8be0dae5. The budget's value stays pinned by TestCommandClient_HasItsOwnConnectBudget, TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon and TestStatusProbe_HasTheCommandConnectBudget.
- **fixed**: #23 graph fallback's setGoalTurnLocked (M40) survives
  - b1d6d326. TestFallbackGoalTurnGatesALaterIncompleteWalk is green on 2bf29705 and red under M40, both on the goal_turn assertion and on the behaviour assertion (the goal steps back to the turn-4 prompt). The commit body corrects wave 20's equivalence claim. Sweep: the turn gate, its !goalTurnSet disjunct, the goal assignment and deriveCurrentWorkLocked's workExplicit return are each killed by existing rows.
- **fixed**: #24 graph fallback's !d.workExplicit guard (M37) survives
  - b1d6d326. TestExplicitCurrentWorkSurvivesTheGraphFallback (explicit work, then a newer prompt encoded while SessionPrompts is degraded, then a seal) is green on 2bf29705 and red under M37.
- **fixed**: #25 large newest prompt read whole once per draft, so every compaction re-reads it on the hook path
  - bc7b088e plus dbfbe97d. The sealed draft hands on goalSeen and the oversized id along with the texts (promptHandoff). Cost row TestPreCompactReadsAnOversizedNewestPromptOnce: on 2bf29705 warm read the paste whole 1 time (want 0) and cold 2 times (want 1). It now passes -count=20 and -race -count=3, and it checks that the successor's CurrentWork and UserIntent equal the sealed checkpoint's. Remaining cost: one whole read per cold compaction (no live draft, for example after a daemon restart), which is once per draft.
- **fixed**: #84 / #53 (ci.yml half) test-e2e -timeout=30m leaves about 15% headroom on Windows
  - b77e7ce3. ci.yml test-e2e goes to -timeout=45m, with a written hang-guard rationale and the measured wall times: 1188 s hosted windows-latest, 1513.7 s and 1529.5 s on the quiet Windows reference host, and a co-loaded local run that reached 1800 s. nightly.yml is unchanged because it runs only eight named e2e rows at 15m. The value for the night seat's phase3.sh win-e2e-timing copy is 45m.
- **fixed**: nit M06: walk's clear case leans on case order
  - 7afeca6c. The clear case now says !found (no behaviour change). The resumed-draft row now checks the goal after one refresh and fails under M06.

### Tests

- `GOOS={windows,linux,darwin} go vet ./internal/checkpoint/ ./internal/cli/`: pass on all three at HEAD
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: pass (exit 0)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs/...`: ok (24.0 s)
- `go test -p 2 -count=1 -run '^(TestColoadYieldersAreJudgedInIsolation|TestColoadDeclarationIsPinnedToTheGoConstant|TestNonReferenceDisk_IsHostedCIOnly|TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps|TestReleaseJobsPinTheirRunnerImage|TestReleaseRunnerGuardRejectsReshapedJobs)$' ./test/guards/`: 7/7 pass (the guards that read ci.yml)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/ ./internal/checkpoint/ (Windows, HEAD dbfbe97d)`: ok cli 167 s, ok checkpoint 196 s. An earlier run at 7afeca6c under nine seats' co-load had reds unrelated to this diff: 5 rows where the daemon never became reachable inside bootstrapUpBound, TestDoctor_SpoolSubmodeIsInformational, and TestPreCompactDerivesItsBudgetFromTheCallersDeadline. Rerun alone, all passed except the doctor row, which passes 3/3 on base and 3/3 on HEAD.
- `sh linux-nonroot-gate.sh --out .../wave22/linux-cliwork 7afeca6c w22-cliwork --gomaxprocs 2 -- ./internal/cli ./internal/checkpoint`: PASS cli 553/553, checkpoint 544/544 (race, non-root)
- `sh linux-nonroot-gate.sh --out .../wave22/linux-cliwork dbfbe97d w22-cliwork-final --gomaxprocs 2 -- ./internal/cli ./internal/checkpoint`: checkpoint 544/544 PASS; cli 552/553: TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound got the deferred-rehydration notice (a parallel wall-clock row this diff does not touch; it passed in the 7afeca6c gate, and only a checkpoint test changed since)
- `sh linux-nonroot-gate.sh --out .../wave22/linux-cliwork dbfbe97d w22-cliwork-sessrerun --gomaxprocs 2 --run '^TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound$' --count 5 -- ./internal/cli`: 5/5 PASS (race, non-root)
- `go test -p 2 -count=20 -run '^(TestFallbackGoalTurnGatesALaterIncompleteWalk|TestExplicitCurrentWorkSurvivesTheGraphFallback|TestPreCompactReadsAnOversizedNewestPromptOnce|TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal)$' ./internal/checkpoint/`: 80/80 pass at HEAD. An earlier co-loaded pass with the row's first version failed (output lost); that led to dbfbe97d, and after it four -count=20 passes were all green
- `go test -race -p 2 -count=3 -run (same four checkpoint rows) ./internal/checkpoint/`: ok <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=20 -run '^(TestStatus_TransientConnectMissStillReadsTheDaemon|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree|TestStatusSource_ASilentDaemonIsNotReportedAbsent)$' ./internal/cli/`: 80/80 pass
- `go test -race -p 2 -count=3 -run (same four cli rows) ./internal/cli/`: 12/12 pass <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `red-first, #80: scratch slow-host simulation applied to dial_windows.go and bootstrapProbeTimeout, then the four status rows on 2bf29705 vs 8be0dae5`: base: all four FAIL (connect miss / none is listening); fix: all four PASS. The simulation was reverted.
- `red-first, #23 #24 #25 and M06: mutants M40 and M37 applied to writer.go, M06 to intent.go, and 2bf29705's intent.go and writer.go for the cost row`: each new or extended row FAILS on its mutant or on the base product and passes on HEAD

### Criterion changes

- The four status rows (TestStatus_TransientConnectMissStillReadsTheDaemon, TestStatus_CallDeadlineExpiryStillSaysSilent, TestStatus_RepeatedReadsOfUnchangedStateAgree, TestStatusSource_ASilentDaemonIsNotReportedAbsent) no longer dial with the product's 250 ms commandConnectDeadline. Their command client's connect and write budgets, and the liveness probe's budget, are a 5 s hang guard (bootstrapCallDeadline). Rationale: D61(c) forbids a verdict that rests on a wall-clock margin. These rows prove the resend, the silent-daemon reason, read stability and the present-but-silent reason, not the budget's value, which three rows still pin without a real dial. Every other assertion is unchanged.
- ci.yml test-e2e -timeout goes from 30m to 45m. A -timeout is a hang guard, not a product threshold. The measured wall times (1188 s hosted Windows, 1513.7 s and 1529.5 s on the quiet Windows reference host, 1800 s reached under co-load) left a slow but healthy run killed as a timeout red.

### Open issues

- Co-load reds outside my files, seen in the first full Windows run: bootstrapDaemon's 15 s bootstrapUpBound ran out in 5 internal/cli rows that use a live daemon (TestBootstrapClosesStoreExactlyOnce, TestBootstrapDAGOpenFailureDegrades, three TestLive* rows). All pass when rerun alone. They belong to the same kind as #80 (a helper's wall-clock bound) but were not assigned to this seat.
- TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving read degraded instead of ok twice under co-load (once even at -p 1). It passes 3/3 on 2bf29705 and 3/3 on HEAD when run alone. It looks load-sensitive in doctor's spool.pending decision against a live daemon (doctor.go, not my file).
- TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound failed once in the shared Linux container's full cli pass (the deferred notice instead of the late daemon answer) and passed 5/5 alone. It is a wall-clock row this diff does not touch.
- Residual for #25: a cold compaction (no live draft, for example right after a daemon restart) still reads a long newest paste whole once, for its goal. A release-notes sentence, if wanted: after a daemon restart, the first compaction of a session whose newest prompt is a very large paste reads that prompt once in full to derive current work.

### Needs owner

- Night seat: set plans/sdd/V6-closeout/coordinator/phase3.sh win-e2e-timing to -timeout=45m to match ci.yml test-e2e (b77e7ce3).

## review:cliwork:r1: verdict `needs-fixes`, 4 finding(s)

- **minor** `tools/devtool/test.go:114 (wholeTreeTestTimeout = "30m"), used at test.go:65, cover.go:269 and stubskips.go:227 for the isolated test/e2e pass; ci.yml lint-windows comment lines 91-99 ("killed at 30 if it hangs")`: The #84 fix covers the class only partly. b77e7ce3 raises the hang guard for the whole test/e2e binary to 45m in ci.yml test-e2e, and the night seat already set phase3.sh win-e2e-timing to 45m. But devtool still runs that same binary alone at -timeout=30m in three places: `devtool test` (isolatedPackages pass), `devtool cover` (an instrumented run, so slower) and lint's stubskips. The night chain runs all three on the Windows reference host: win-tree, cover, and release, which is release-check and includes lint. The seat's own justification puts that host at 1513.7 to 1529.5 s, about 15% headroom, which is exactly the risk #84 describes. The seat neither fixed this nor listed it as a deferred-known-issue or a needs_owner item. tools/devtool is outside its files.
  - Evidence: All nine w22 worktrees still have `const wholeTreeTestTimeout = "30m"` at tools/devtool/test.go:114. qompack-v6 phase3.sh:77 runs `go run ./tools/devtool test`, :112 `devtool cover` and :152 `devtool release-check`. testInvocations and runCoverPasses give each wholeTreePasses pass, test/e2e alone included, `-timeout=` + wholeTreeTestTimeout. The commit body and the ci.yml comment cite 1513.7 s and 1529.5 s for this binary on the quiet Windows reference host.
  - Fix: Route this to the owner of tools/devtool, or record it as a known issue. For example, give isolatedPackages passes their own 45m hang guard (with the same written rationale) in test, cover and stubskips, and update the lint-windows comment and its timeout-minutes arithmetic. At minimum, list it in the seat's needs_owner so the class does not stay half-raised.
- **minor** `internal/cli: daemon_precompact_test.go, live_*_test.go, sessionstart_budget_test.go (hook rows that dial with hookConnectDeadlineFloor, the same 250 ms constant commandConnectDeadline aliases, qompack_commands.go:195)`: #80 is closed for the status rows, but the rest of its class was not swept, deferred or routed. Eleven other internal/cli rows still need a real named-pipe connect inside the same 250 ms floor. One of them, TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound, went red in the seat's own final Linux gate at dbfbe97d. The seat called it 'a parallel wall-clock row this diff does not touch'. That is true, but it leaves out that the row depends on the same 250 ms real connect. The seat routed only the bootstrapUpBound rows as 'same kind'.
  - Evidence: In a scratch clone at dbfbe97d I made every dial with a timeout between 200 and 300 ms miss after that timeout, and raised bootstrapProbeTimeout to 1 s so bootstrap itself still works. The four hang-guarded status rows passed, and the full ./internal/cli run failed exactly these 11 rows: TestDaemonFirstPreCompactSealsCheckpoint, TestLivePreCompactCarriesEliminationAndDecision, TestLivePinReachesTheNextCheckpointAndBlock, TestLivePinAfterACompactionReachesTheNextOne, TestPreCompactCheckpointCarriesTheCompactedReads, TestSecondPreCompactCarriesItsOwnSpan, TestPreCompactInSpoolSubmodeSealsTheSpooledReads, TestAHostFailedCompactionsSuccessorClosesAtSessionEnd, TestSessionStartBudget_APreSendOverrunCutsTheReplyWait, TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered and TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound. Without the simulation the same clone passed cli 553/553 in the Linux non-root gate and on Windows.
  - Fix: Not closable in this seat's files. Record it as a deferred-known-issue, or route it: the hook-path rows still need a real connect inside hookConnectDeadlineFloor, the same flake class as #80. Correct the open_issues entry that calls the Linux-gate red unrelated to any connect budget.
- **nit** `plans/00-ARCHITECTURE.md:2713`: The architecture doc's CI table still gives test-e2e as `go test -count=1 -timeout=30m ./test/e2e`. After b77e7ce3, ci.yml says 45m, so the design doc no longer matches the workflow it quotes word for word.
  - Evidence: ci.yml:313 `- run: go test -count=1 -timeout=45m ./test/e2e`; 00-ARCHITECTURE.md:2713 `go test -count=1 -timeout=30m ./test/e2e`.
  - Fix: Have the docs seat or owner change the test-e2e row to -timeout=45m and cite the hang-guard rationale.
- **nit** `internal/checkpoint/current_work_test.go (doc comment of TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce)`: b1d6d326 deleted the space after the colon in an existing comment, leaving `TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce:the newest prompt's bytes...`.
  - Evidence: git diff 2bf29705..HEAD: `-// TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce: the newest` became `+// TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce:the newest`.
  - Fix: Put the space back.

## fix:cliwork:r1: status `done`, head `dd8cf7b71e63`

### Summary

Fix round 1 for seat cliwork is done on closeout/w22-cliwork (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-cliwork). There is one new commit, dd8cf7b7, on top of dbfbe97d. Nothing is pushed or merged, I touched no other worktree, and the tree is clean.

Finding 1 (the #84 class was only half raised) is fixed in dd8cf7b7. No other w22 branch touches tools/devtool/test.go, cover.go or stubskips.go, and the change extends my own b77e7ce3, so I closed the class here instead of routing it.
- A new passTimeout gives an isolatedPackages pass (test/e2e alone) isolatedTestTimeout = "45m". Every other pass keeps wholeTreeTestTimeout = "30m".
- All three call sites now read it: testInvocations (devtool test), the new coverTestArgs (devtool cover, called by runCoverPasses), and the new stubskipsTestArgs (lint's stubskips).
- stubskips' report for a killed binary (the new timedOutProblem) now names the timeout that package actually ran at, not always 30m.
- Sweep: taskTestRace and nightly's race job already leave test/e2e out. The ci.yml test job's 30m and 60m cover the tree without test/e2e. Nothing else in devtool sets a -timeout, and no guard or doc pins these values.
- Compared with base 2bf29705, the only change in behaviour is the -timeout argument of the test/e2e pass.
- ci.yml lint-windows: timeout-minutes goes from 75 to 90, and its comment now uses measured pass times instead of the estimate from before the split. Hosted runs 36905843834, 36955046276 and 36981590450 measured setup plus the earlier sub-checks at 3.6 to 4.4 min, pass 1 at 24m26s to 27m26s, pass 2 (test/e2e alone) at 19m49s to 21m14s, and the whole job at 48.5 to 53.8 min.
- With test/e2e's binary killed at 45m, the worst case is 4.5 + 30 + 45 = 79.5 min. A 75-minute job limit would end the job before go test could report the hang.
- The test-e2e comment now says devtool's isolated pass uses the same 45m.
- The cover rationale draws on a phase3 night: cover's instrumented e2e pass took 995 s against 918 s uninstrumented, about 8% slower.

Finding 2 (eleven hook-path rows in internal/cli need a real connect inside hookConnectDeadlineFloor) is deferred as a known issue.
- I could not close it in my files: the connect budget comes from hookConnectDeadline in hookclient.go, which is product code that the contract seat is also editing in w22.
- Three of the rows (TestSessionStartBudget_*) use that connect budget inside the hookBudget arithmetic they assert. A hang guard like the one I gave the status rows would change what those rows prove.
- I have corrected my earlier open_issues entry (see below). TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound's red in the dbfbe97d Linux gate is in this connect-budget class. My earlier report wrongly called it unrelated to any connect budget.

Criterion change: the existing row TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration now expects the isolated pass at isolatedTestTimeout instead of wholeTreeTestTimeout. That is the intended raise of the hang guard. Every other assertion in it is unchanged, and no check was weakened.

### Commits

- 8be0dae5 test(cli): hang-guard the status rows' real dials
- b1d6d326 test(checkpoint): pin the graph fallback's goal turn and guard
- bc7b088e perf(checkpoint): hand the goal memo to the successor draft
- b77e7ce3 ci: give test-e2e's whole-package binary a 45m hang guard
- 7afeca6c fix(checkpoint): the goal walk never clears a goal it found
- dbfbe97d test(checkpoint): take the read-count row off the wall clock
- dd8cf7b7 fix(devtool): give test/e2e's isolated pass a 45m hang guard

### Findings resolution

- **fixed**: FR1-1 (minor): devtool test/cover/stubskips still ran the isolated test/e2e binary at -timeout=30m while ci.yml test-e2e went to 45m; lint-windows comment and timeout-minutes arithmetic stale
  - dd8cf7b7. passTimeout in tools/devtool/test.go gives an isolatedPackages pass isolatedTestTimeout (45m) and every other pass wholeTreeTestTimeout (30m). testInvocations, coverTestArgs (cover.go) and stubskipsTestArgs (stubskips.go) all use it, and timedOutProblem names the timeout the killed package actually ran at. Red-first rows: TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard fails on the base code with `-timeout=30m` for test/e2e (want 45m). TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard and TestStubSkipsPasses_GiveTheIsolatedPassItsOwnHangGuard fail with the new helpers at base behaviour (pass 1 runs -timeout=30m) and pass at HEAD. ci.yml lint-windows timeout-minutes goes from 75 to 90 because the worst case is 4.5 + 30 + 45 = 79.5 min, with measured hosted pass times from runs 36905843834, 36955046276 and 36981590450 written into the comment. The test-e2e comment now points at isolatedTestTimeout.
- **deferred-known-issue**: FR1-2 (minor): eleven internal/cli hook-path rows (TestDaemonFirstPreCompactSealsCheckpoint, TestLivePreCompactCarriesEliminationAndDecision, TestLivePinReachesTheNextCheckpointAndBlock, TestLivePinAfterACompactionReachesTheNextOne, TestPreCompactCheckpointCarriesTheCompactedReads, TestSecondPreCompactCarriesItsOwnSpan, TestPreCompactInSpoolSubmodeSealsTheSpooledReads, TestAHostFailedCompactionsSuccessorClosesAtSessionEnd, TestSessionStartBudget_APreSendOverrunCutsTheReplyWait, TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered, TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound) need a real named-pipe connect inside hookConnectDeadlineFloor (250 ms), the #80 class
  - I could not close this in my files. Those rows dial through doHook's hookConnectDeadline (internal/cli/hookclient.go, product code that the contract seat is also editing in w22). There is no test seam like newCommandIPCClient on the hook path. Three of the rows feed that connect budget into the hookBudget arithmetic they assert, so swapping in a hang guard would change what they prove; this needs the hook-path owner. Correction to my earlier report: TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound's red in the dbfbe97d Linux gate belongs to this connect-budget class. It was not unrelated. Release-notes sentence: Eleven internal/cli test rows drive a hook against a live daemon and need a real named-pipe connect inside the 250 ms hook connect floor, so on a heavily co-loaded host they can fail without a product defect (a test flake of the same class as #80).

### Tests

- `go test -count=1 -run '^TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard$' ./tools/devtool/ (on the base devtool code, before any change)`: FAIL as intended: actual -timeout=30m for test/e2e, want 45m
- `go test -count=1 -run '^(TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard|TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard|TestStubSkipsPasses_GiveTheIsolatedPassItsOwnHangGuard)$' ./tools/devtool/ (helpers extracted at base behaviour)`: all three FAIL (pass 1 runs -timeout=30m, want 45m)
- `go test -count=1 -run '^(TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard|TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard|TestStubSkipsPasses_GiveTheIsolatedPassItsOwnHangGuard|TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration|TestCoverPasses_RunsE2EAloneWithoutTheColoadDeclaration|TestStubSkipsPasses_RunsE2EAloneAndDropsNothing)$' -v ./tools/devtool/ (HEAD)`: 6/6 PASS
- `GOOS={windows,linux,darwin} go vet ./tools/devtool/`: pass on all three
- `go run ./tools/devtool fmt-check`: pass
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers (407 plan documents)
- `go test -p 2 -count=1 -timeout=30m ./tools/devtool/ (Windows, HEAD dd8cf7b7)`: ok 283.1 s
- `sh linux-nonroot-gate.sh --out .../wave22/linux-cliwork dd8cf7b7 w22-cliwork-fr1 --gomaxprocs 2 -- ./tools/devtool`: PASS tools/devtool 348/348 (race, non-root), exit 0
- `go test -p 2 -count=20 -run '^(TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard|TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard|TestStubSkipsPasses_GiveTheIsolatedPassItsOwnHangGuard|TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration)$' ./tools/devtool/`: ok
- `go test -race -p 2 -count=3 -run (same four rows) ./tools/devtool/`: ok <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=1 ./test/docs/...`: ok (28.9 s)
- `go test -p 2 -count=1 -run '^(TestColoadYieldersAreJudgedInIsolation|TestColoadDeclarationIsPinnedToTheGoConstant|TestNonReferenceDisk_IsHostedCIOnly|TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps|TestReleaseJobsPinTheirRunnerImage|TestReleaseRunnerGuardRejectsReshapedJobs)$' ./test/guards/ (every guard that reads ci.yml)`: 7/7 PASS
- `python yaml.safe_load(.github/workflows/ci.yml)`: parses; lint-windows timeout-minutes = 90

### Criterion changes

- tools/devtool: the existing row TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration now expects test/e2e's isolated pass at isolatedTestTimeout (45m), not wholeTreeTestTimeout (30m). Rationale: a -timeout is a hang guard, not a product threshold. The binary alone takes 1513.7 s and 1529.5 s on the quiet Windows reference host and 19m49s to 21m14s in hosted lint-windows, so 30m was about 15% headroom (#84). The shared pass and every other assertion in the row are unchanged.
- ci.yml lint-windows timeout-minutes goes from 75 to 90. With test/e2e's binary killed at 45m, the job's worst case is 79.5 min (measured 4.4 + pass 1 taken at 30 + 45), so a 75-minute limit would end the job before go test reported a hang. No per-binary -timeout in the tree moved.

### Open issues

- Correction to round 0's open_issues: TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound's red in the dbfbe97d Linux gate is not unrelated to a connect budget. Like ten other internal/cli hook-path rows (FR1-2), it needs a real named-pipe connect inside hookConnectDeadlineFloor (250 ms). This is the #80 flake class, deferred as a known issue because the budget comes from hookclient.go's product hookConnectDeadline.
- Carried from round 0: in the first co-loaded Windows run, bootstrapDaemon's 15 s bootstrapUpBound ran out in 5 internal/cli live-daemon rows (TestBootstrapClosesStoreExactlyOnce, TestBootstrapDAGOpenFailureDegrades and three TestLive* rows). All passed when rerun alone. This is the same wall-clock-helper class, and it is outside this seat's files.
- Carried from round 0: TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving read degraded under co-load. It passes 3/3 on base and on HEAD when run alone (doctor.go, not this seat's file).
- Residual for #25, for the release notes: after a daemon restart, the first compaction of a session whose newest prompt is a very large paste reads that prompt once in full to derive current work.

### Needs owner

- Night seat: set plans/sdd/V6-closeout/coordinator/phase3.sh win-e2e-timing to -timeout=45m to match ci.yml test-e2e and devtool's isolatedTestTimeout (b77e7ce3, dd8cf7b7).
- Hook-path owner (internal/cli/hookclient.go and the live, pre-compact and sessionstart-budget rows): eleven rows need a real connect inside hookConnectDeadlineFloor (FR1-2). Closing it needs a test seam on doHook's client construction for the eight content rows, and a decision on the three TestSessionStartBudget_* rows, whose budget arithmetic includes the connect floor.

## review:cliwork:r2: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/cli/daemon_bootstrap_test.go:47 (bootstrapProbeTimeout = 250ms, used at :135 by bootstrapDaemon); affects TestStatus_TransientConnectMissStillReadsTheDaemon (via startStatusOrderSession/bootstrapDaemon) and TestStatus_RepeatedReadsOfUnchangedStateAgree (status_order_test.go:78)`: The #80 class is only partly closed. 8be0dae5 hang-guards the command-client and status-probe dials, but two of the four rows still need a real named-pipe connect inside 250 ms. The connect is in their fixture: bootstrapDaemon's readiness probe dials with bootstrapProbeTimeout (250 ms) inside a 15 s require.Eventually. On a host where every connect takes longer than 250 ms, both rows still go red before they reach what they prove. The seat's red-first claim ('every dial takes 300 ms: all four rows fail on 2bf29705 and pass on this commit') does not reproduce as stated. The seat's own open issue (bootstrapUpBound running out in 5 live rows under co-load) is this same class, and bootstrapDaemon is a helper of these rows.
  - Evidence: Scratch clone of dd8cf7b7 with internal/ipc/dial_windows.go patched to simulate a slow host: a dial whose budget is 200-300 ms sleeps for that budget and times out; a dial with a budget of 1 s or more sleeps 300 ms first. Command: go test -p 2 -count=1 -run '^(TestStatus_TransientConnectMissStillReadsTheDaemon|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree|TestStatusSource_ASilentDaemonIsNotReportedAbsent)$' ./internal/cli/. At HEAD: CallDeadline and SilentDaemon PASS; TransientConnectMiss and RepeatedReads FAIL with 'daemon_bootstrap_test.go:133 ... the daemon at ... never became reachable'. With bootstrapProbeTimeout temporarily set to 5 s, both PASS. With 2bf29705's three status test files under the same simulation, all four FAIL. So the seat's change works for the command dials, and the only dependency left is the fixture probe.
  - Fix: Give bootstrapDaemon's readiness probe the same hang guard (bootstrapCallDeadline, which statusDialHangGuard already uses) instead of 250 ms. It already polls inside require.Eventually, so the verdict still rests on the daemon becoming reachable, not on a margin. Then re-run the slow-dial simulation on all four rows. If the seat keeps it out of scope, record it as a known issue in the #80 class.
- **minor** `internal/cli/sessionstart_budget_test.go:413 (TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound); deferred FR1-2`: The deferral of the eleven hook-path rows is correct in scope: they dial through hookclient.go's product hookConnectDeadline, and wave 22's diff touches neither that code nor that test. But the red reproduces in the D67(n) gate on a touched package. My Linux non-root race run of dd8cf7b7 over ./internal/cli ./internal/checkpoint ./tools/devtool was red on this one row. checkpoint 544/544 and devtool 348/348 passed. The deferral exists, but the gate the coordinator must pass before the freeze will keep hitting this row.
  - Evidence: linux-cliwork-verify-r2/cx-vr2-cliwork-w22-cliwork-verify-r2-dd8cf7b-20261004T212453Z-artifacts: internal/cli pass=552 fail=1; output 'qompack:rehydration-deferred ... (the Qompack daemon did not answer in time)' does not contain 'late-daemon-answer'. The seat's dbfbe97d full run was red the same way; the 7afeca6c run and the first verifier's dbfbe97d run were green. Alone, on base 2bf29705 with --count 5: 5/5 PASS. The diff since 2bf29705 changes only non-parallel status test files in internal/cli, so this is not a regression from this seat.
  - Fix: No change in this seat. Make sure FR1-2's release-notes sentence and the needs_owner routing to the hook-path owner reach the coordinator, because D67(n) needs the Linux non-root gate on the touched packages green before the freeze.

## fix:cliwork:r2: status `done`, head `774c1de4`

### Summary

Fix round 2 for seat cliwork is done on closeout/w22-cliwork (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-cliwork). It adds one commit, 774c1de4, on top of dd8cf7b7. Nothing is pushed or merged, I edited no other worktree, the tree is clean, and the scratch clone I used for the slow-host simulation has been deleted.

FR2-1 (the #80 fixture half) is fixed for the whole class in internal/cli:
- **Root cause:** bootstrapDaemon's readiness wait dialled with bootstrapProbeTimeout = 250 ms. Four more readiness waits in the package dialled with 50 ms: daemon_test.go:34 and :110, daemon_disabled_test.go:146, fsck_test.go:676 and hookoutput_contract_test.go:193.
- **Fix:** every one of those waits now calls a new daemonReachable(addr). It dials through a fixtureProbe seam (ipc.Probe everywhere except in the row that records budgets) with bootstrapProbeTimeout = bootstrapCallDeadline (5 s). That is the bound the fixtures' admin round trips and statusDialHangGuard already use, so no new number is introduced.
- **Why this is not a margin:** each wait still polls inside its own unchanged require.Eventually bound, so the verdict is still "the listener came up". The per-dial budget is only a hang guard (D61(c)).
- **Left alone on purpose:** the negative probe at daemon_disabled_test.go:100 (require.False, nothing listening) can't go red on a slow host and was not part of the class.
- **Who else touches these files:** no other w22 branch edits the six files involved. status_connect_test.go, which the contract and rehydrate seats edit, is untouched.

Two new rows in internal/cli/fixture_probe_test.go:
- TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard starts a real daemon through bootstrapDaemon and records every budget its readiness wait dials with; each must equal bootstrapCallDeadline.
- TestFixtureReadinessWaits_GoThroughDaemonReachable reads every *_test.go file in the package and fails on any Eventually condition that calls ipc.Probe or fixtureProbe directly.
- **Red first:** with the seam added but the budget still 250 ms, the first row failed (expected 5s, actual 250ms). The sweep failed with 5 offenders: daemon_disabled_test.go:146, daemon_test.go:34 and :110, fsck_test.go:676 and hookoutput_contract_test.go:193. On base 2bf29705 the bootstrap probe itself makes a sixth.

Slow-host simulation, as the verifier asked: I used a scratch clone with internal/ipc/dial_windows.go patched so every connect takes 300 ms (a smaller budget sleeps that budget and times out).
- **On dd8cf7b7's test files:** TestStatus_TransientConnectMissStillReadsTheDaemon and TestStatus_RepeatedReadsOfUnchangedStateAgree fail with "never became reachable". TestDaemon_ExitsZeroOnCleanStop, TestDaemon_ForegroundFlagParses and TestDaemon_RunsWhenTheMergedConfigEnablesIt fail the same way. CallDeadline and SilentDaemon pass, which matches what the verifier saw.
- **With 774c1de4's files:** all four status rows, the three TestDaemon_* rows and the new bootstrap row pass. RepeatedReads took 39 s, about 120 dials at 300 ms each, so the simulation was really slowing every dial.

FR2-2 needs no code change in this seat (the verifier agrees). My Linux non-root race gate on 774c1de4 over ./internal/cli came back green, 555/555. Before the freeze, the coordinator still needs the FR1-2 release-notes sentence and the hook-path routing below.

No criterion was changed this round, and no check was weakened.

### Commits

- 8be0dae5 test(cli): hang-guard the status rows' real dials
- b1d6d326 test(checkpoint): pin the graph fallback's goal turn and guard
- bc7b088e perf(checkpoint): hand the goal memo to the successor draft
- b77e7ce3 ci: give test-e2e's whole-package binary a 45m hang guard
- 7afeca6c fix(checkpoint): the goal walk never clears a goal it found
- dbfbe97d test(checkpoint): take the read-count row off the wall clock
- dd8cf7b7 fix(devtool): give test/e2e's isolated pass a 45m hang guard
- 774c1de4 test(cli): hang-guard every fixture readiness wait's dial

### Findings resolution

- **fixed**: FR2-1 (minor): bootstrapDaemon's readiness probe still dials with bootstrapProbeTimeout (250 ms), so TestStatus_TransientConnectMissStillReadsTheDaemon and TestStatus_RepeatedReadsOfUnchangedStateAgree still need a real named-pipe connect inside 250 ms (#80 class partly open)
  - 774c1de4. bootstrapProbeTimeout is now bootstrapCallDeadline (5 s), used as a hang guard. All six positive readiness waits in internal/cli call daemonReachable, which dials through the fixtureProbe seam: bootstrapDaemon, daemon_test.go:34 and :110, daemon_disabled_test.go:146, fsck_test.go:676 and hookoutput_contract_test.go:193. The require.Eventually bounds are unchanged. Red-first rows: TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard failed with the seam at 250 ms (expected 5s, actual 250ms). TestFixtureReadinessWaits_GoThroughDaemonReachable failed with 5 offenders before the sites were converted. Under the 300 ms slow-dial simulation, both status rows and three TestDaemon_* rows fail on dd8cf7b7 and pass on 774c1de4. The other two #80 rows pass on both. This also removes the fixture-side cause of round 0's open issue, where bootstrapUpBound ran out under co-load in TestBootstrap*/TestLive* rows. Those rows still dial their hooks with product budgets (FR1-2).
- **deferred-known-issue**: FR2-2 (minor): TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound reproduces red in the D67(n) Linux non-root gate on a touched package (deferred FR1-2, hook-path connect budget)
  - No change in this seat, as the verifier also recommends. The row dials through hookclient.go's product hookConnectDeadline, which the contract seat is also editing. Three of the TestSessionStartBudget_* rows include that budget in the arithmetic they assert. It is not a regression: 5/5 PASS alone on base 2bf29705, and this seat's diff touches only non-parallel internal/cli test files. My D67(n) gate on 774c1de4 over ./internal/cli passed 555/555 this time; the row is intermittent. Release-notes sentence: Eleven internal/cli test rows drive a hook against a live daemon and need a real named-pipe connect inside the 250 ms hook connect floor, so on a heavily co-loaded host they can fail without a product defect (a test flake in the same class as #80).

### Tests

- `go test -p 2 -count=1 -run '^(TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard|TestFixtureReadinessWaits_GoThroughDaemonReachable)$' ./internal/cli/ (seam added, budget still 250 ms, sites not converted)`: FAIL as intended: expected 5s, actual 250ms; sweep offenders daemon_disabled_test.go:146, daemon_test.go:34, daemon_test.go:110, fsck_test.go:676, hookoutput_contract_test.go:193
- `same two rows at 774c1de4`: PASS
- `scratch clone with slow-host dial_windows.go (every connect 300 ms), dd8cf7b7 test files: go test -p 2 -count=1 -run '^(TestStatus_TransientConnectMissStillReadsTheDaemon|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestDaemon_ExitsZeroOnCleanStop|TestDaemon_ForegroundFlagParses|TestDaemon_RunsWhenTheMergedConfigEnablesIt)$' -v ./internal/cli/`: TransientConnectMiss, RepeatedReads and all three TestDaemon_* FAIL ('never became reachable'); CallDeadline and SilentDaemon PASS
- `same simulation with 774c1de4's test files (the same seven rows plus TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard)`: 8/8 PASS (RepeatedReads 39.5 s)
- `GOOS={windows,linux,darwin} go vet ./internal/cli/`: pass on all three
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers (407 plan documents)
- `go test -p 2 -count=20 -run '^(TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard|TestFixtureReadinessWaits_GoThroughDaemonReachable)$' ./internal/cli/`: ok 8.9 s
- `go test -race -p 2 -count=3 -run '^(TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard|TestFixtureReadinessWaits_GoThroughDaemonReachable)$' ./internal/cli/`: ok 6.9 s
- `go test -p 2 -count=1 ./test/docs/...`: ok 23.2 s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/ (Windows, 774c1de4, exit code captured unpiped)`: ok 162.2 s, exit 0
- `sh linux-nonroot-gate.sh --out .../wave22/linux-cliwork 774c1de4 w22-cliwork-fr2 --gomaxprocs 2 -- ./internal/cli`: PASS internal/cli pass=555 fail=0 (race, non-root uid 10001), exit 0; artifacts cx-linux-w22-cliwork-fr2-774c1de-20261004T221003Z-artifacts

### Criterion changes

- None this round. bootstrapProbeTimeout and the four 50 ms readiness dials are fixture hang guards, not assertions: each wait's require.Eventually bound and its failure message are unchanged, and no asserted product budget moved. The round 1 criterion changes (the isolated pass at 45m, lint-windows at 90 minutes) still stand.

### Open issues

- The same fixture class is still open outside internal/cli, in files this seat does not own. These are positive readiness probes inside Eventually with a sub-second dial budget: test/integration hookflowProbeDial 250 ms (hookflow_test.go:500, hotpath_test.go:1462), probeDialTimeout 250 ms (contractmonitor_test.go:431) and cdwProbeDial 100 ms (appendonly_test.go:530); internal/daemon borrowed_lease_test.go:33 (50 ms), daemon_test.go:1116 (dialProbeTimeout 20 ms), spawn_test.go:102 and spawn_stage_test.go:392 (ensureRunningDialTimeout 20 ms), and spawn_claim_release_test.go:130 (250 ms). The w22 rows and spawn seats edit internal/daemon. test/e2e's e2eProbeTimeout (250 ms) is defined in daemon_e2e_test.go, which the contract seat edits. Release-notes sentence: Some daemon-readiness waits in internal/daemon, test/integration and test/e2e dial with a sub-second budget, so on a heavily co-loaded host they can report a running daemon as unreachable (a test flake in the same class as #80).
- FR1-2 / FR2-2 is carried forward: eleven internal/cli hook-path rows need a real connect inside hookConnectDeadlineFloor (250 ms). TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound is the one the D67(n) Linux gate has hit; it passed in this round's gate (555/555).
- Carried from round 0: TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving read degraded under co-load and passes alone (doctor.go, not this seat's file).
- Residual for #25, for the release notes: after a daemon restart, the first compaction of a session whose newest prompt is a very large paste reads that prompt once in full to derive current work.

### Needs owner

- Coordinator: D67(n) needs the Linux non-root gate on touched packages green before the freeze, and TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound (FR1-2/FR2-2) can turn it red intermittently. Route the FR1-2 release-notes sentence and the hook-path decision below.
- Hook-path owner (internal/cli/hookclient.go and the live, pre-compact and sessionstart-budget rows): eleven rows need a real connect inside hookConnectDeadlineFloor. The eight content rows need a test seam on doHook's client construction. The three TestSessionStartBudget_* rows need a decision, because their budget arithmetic includes the connect floor.
- Owners of internal/daemon (w22 rows/spawn seats), test/e2e (contract seat) and test/integration (unowned in w22): the same fixture readiness-probe class listed in open_issues. The fix pattern is 774c1de4's: one reachability helper that dials with the package's admin-call hang guard, plus a source sweep row.
- Night seat (carried): set plans/sdd/V6-closeout/coordinator/phase3.sh win-e2e-timing to -timeout=45m to match ci.yml test-e2e and devtool's isolatedTestTimeout (b77e7ce3, dd8cf7b7).


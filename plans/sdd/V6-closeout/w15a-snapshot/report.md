# Wave 15a workstream snapshot

Branch `closeout/w15a-snapshot`. Workflow `wf_3f954a82-13d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `fc21a9cb`

### Root cause

Two separate defects produced the UAT-01 contradiction (rerun-c4/UAT-01/notes.txt:50-55; report-c4.md, Independent audit, minor finding on docs/uat.md:217).

(1) The contract snapshot went stale. The status op answered d.monitor.Report(), which is the last RunAll, and RunAll runs only at SessionStart. The MCP handshake and the probe's delivery come after SessionStart. The `mcp` op records the handshake in state/history.json (MCPInitialized), and the UserPromptSubmit sentinel scan records the probe there (Sentinel.Observed). Nothing brought those observations into the rows status reads, so mcp.server_registered kept reading initialize-pending and hook.additional_context_delivered kept reading not-yet-observed until the next SessionStart. doctor had the same problem: its capability rows read the observation ledger's newest entry (state/observations.json), which is also written at SessionStart, so the injection capability kept reading outcome not_observed.

(2) The banner miscounted. internal/commands/render.go renderContract treated every row with OK true as holding. Pending rows (not-yet-observed, initialize-pending, transcript-pending) and rows with nothing to judge (first-session, retired, timeout-unknown, not-yet-implemented) are OK true, so the banner printed '9 assertion(s), all holding'.

Neither defect belongs to another seat.

### Summary

W15A-SNAPSHOT REPORT (D50: contract snapshot and status banner). Branch closeout/w15a-snapshot, 6 commits on efd5bf68, head fc21a9cb.

RESUME NOTE: when this seat started, the worktree already held uncommitted work from an earlier run of the same seat. Its files were last edited at 14:16, no process of it was still running, and its red, full-package and lint logs were in the seat's scratch directory. I reviewed every file against D50 and the checks in internal/contract/assertions.go. I re-ran fmt-check, vet (Windows and GOOS=linux), the focused rows, all touched packages in full and lint myself, then split the work into focused commits. The RED logs committed under runs/ are that earlier run's output, captured before the fix was applied.

WHAT CHANGED
1. internal/contract/refresh.go (new).
   - StandingOf(Result) sorts a row into one of four groups: Holding (OK, and something was actually seen), Pending (OK, but still waiting for an observation), Idle (OK, with nothing to judge) or Failing.
   - pendingSpellings = not-yet-observed, initialize-pending, transcript-pending, marker-absent-once. A test pins it as a subset of noObservationSpellings.
   - RefreshFromHistory(results, h, clk) replaces only a Pending row whose observation history.json records permanently (MCPInitialized, Sentinel.Observed; neither is ever reset to false). The replacement is what that row's own check returns when run against a copy of the history, so the history is never mutated. Failing, Idle and Holding rows are never rewritten.
   - RefreshObservation does the same for one not_observed ledger entry. It re-derives the entry with ObservationsOf's rules and keeps the entry's target and scope.
2. internal/commands/render.go renderContract.
   - 'host contract: N assertion(s), all holding' is printed only when every row is Holding.
   - With no failure: 'host contract: 9 assertion(s), none failing: 2 holding, 3 pending, 4 with nothing to judge', plus one '  pending: <id> (<observed>)' line per pending row.
   - A failure still leads: 'host contract: 1 of 9 assertion(s) FAILING; 2 holding, 3 pending, 3 with nothing to judge', followed by the failure blocks and then the pending lines.
3. internal/daemon/contract_snapshot.go (new) plus handlers.go handleStatus.
   - The status op's Contract field is now d.contractSnapshot(ctx): it loads history.json under historyMu, counts a handshake seen only by the daemon's own MCPInitialized seam (the same rule as handleSessionStart), then applies RefreshFromHistory.
   - It writes nothing.
4. internal/cli/doctor.go capabilityRow. The newest ledger entry goes through RefreshObservation. A refreshed row reports outcome observed and says '(read from state/history.json; ...)'. The ledger file is not rewritten.
5. docs/troubleshooting.md, section on qompack status: describes the new banner wording and the history.json refresh for status and doctor.

TESTS
RED before the fix (runs/red-*.log):
- TestStatus_AnObservedHandshakeRefreshesTheContractSnapshot, TestStatus_AHandshakeOnlyTheDaemonSawStillRefreshes and TestStatus_AnObservedSentinelRefreshesTheContractSnapshot failed, reading initialize-pending / not-yet-observed.
- TestDoctor_AnObservedProbeRefreshesTheInjectionRow failed, reading outcome not_observed.
- TestRenderStatus_APendingRowIsNeverCountedAsHolding, TestRenderStatus_AFailingBannerStillCountsThePendingRows and TestRenderStatus_AnUnimplementedProducerIsNotHolding failed; the old banner said 'all holding' and '1 of 9 assertion(s) FAILING'.
- The internal/contract rows (TestRefreshFromHistory_*, TestRefreshObservation_*, TestStandingOf_CountsOnlyObservationsAsHolding) did not compile before refresh.go existed. That compile failure is their only RED; it was not logged.

Guard rows that passed before and after the fix: TestStatus_AMissedSentinelIsNotRefreshedIntoAnObservation, TestDoctor_AnUnobservedProbeKeepsTheLedgersWord, TestRenderStatus_AllHoldingOnlyWhenEveryRowIsAnObservation, TestStandingVocabulary_PendingIsNothingSeen and TestStandingVocabulary_EveryNothingSeenSpellingIsNotHolding.

GREEN: all rows pass; full commands and results are in the tests field. The Linux container was stopped as instructed, so no Linux run was done; GOOS=linux vet passes.

DESIGN NOTE: a refreshed row is stamped with the time status or doctor read it, because history.json records no time for these observations. Two status reads after a refresh can therefore differ only in that row's TS. The one test that compares Contract across two reads (test/e2e/v5_x01_test.go:448) has no MCP handshake or probe transcript, so it is not affected.

CRITERION CHANGES: none. No assertion, threshold or golden changed. The new banner text is additive: the FAILING lead that TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent checks is kept.

### Commits

- c3c6da86 fix(contract): refresh pending rows from history.json
- a95add71 fix(commands): never count a pending contract row as holding
- 25830470 fix(daemon): refresh the status contract snapshot from history
- e1369ed9 fix(cli): read an observed probe into doctor's capability row
- c82cae81 docs(troubleshooting): describe the pending-aware banner
- fc21a9cb test(evidence): record the w15a snapshot red and green runs

### Tests

- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/contract ./internal/commands ./internal/daemon ./internal/cli (Windows, and again with GOOS=linux)` — exit 0 on both
- `go test -p 2 -count=1 -v -run '^(TestStatus_AnObservedHandshakeRefreshesTheContractSnapshot|TestStatus_AHandshakeOnlyTheDaemonSawStillRefreshes|TestStatus_AnObservedSentinelRefreshesTheContractSnapshot|TestStatus_AMissedSentinelIsNotRefreshedIntoAnObservation)$' ./internal/daemon` — ok, 4 PASS (runs/green-daemon-focused.log); the first three were RED before the fix (runs/red-daemon.log)
- `go test -p 2 -count=1 -v -run '^(TestDoctor_AnObservedProbeRefreshesTheInjectionRow|TestDoctor_AnUnobservedProbeKeepsTheLedgersWord)$' ./internal/cli` — ok, 2 PASS (runs/green-doctor-focused.log); the first was RED before the fix (runs/red-doctor.log)
- `go test -p 2 -count=1 -v ./internal/contract ./internal/commands ./test/docs` — ok on all three (runs/full-contract-commands-docs.log); the three TestRenderStatus rows were RED before the fix (runs/red-render.log)
- `go test -p 2 -count=1 -timeout=30m -v ./internal/cli` — ok 70.6s (runs/full-cli.log)
- `go test -p 2 -count=1 -timeout=30m -v ./internal/daemon` — ok 220.5s (runs/full-daemon.log)
- `go test -p 2 -count=1 -v -run '^(TestV5_EveryContractAssertionHasARealProducer|TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent)$' ./test/e2e` — ok, 2 PASS (runs/e2e-contract-rows.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all 8 checks PASS (runs/lint.log)

### Open issues

- Not caused by this work, not fixed: TestV5_ObserveToStatusRoundTrip (test/e2e) failed once in the earlier run of this seat, in both the live and the daemon_down_falls_back_to_persisted_snapshot subtests. The B-A p99 was expected 180224 but read 165000. Cause: e2e7e480 (fix(commands): never print a percentile above the max) clamps displayed percentiles to Max in statuscollect.go latencyOf, but x1v5RequireLatencyEquals (v5_x01_test.go:176) still compares the display with the unclamped snapshot. It fails whenever the p99 bucket's upper bound exceeds the max sample, which is likely when few deliveries arrive live, so it shows up under load. That test was not re-run in this session (whole e2e runs are not allowed daytime). It needs a test-side fix in x1v5RequireLatencyEquals that applies the same clamp; route it to an e2e seat.
- Not in D50's scope: transcript.readable's transcript-pending and session_start.fires' marker-absent-once are not refreshed mid-session because history.json records no positive observation for them. The banner now counts and names them as pending instead of holding.
- docs/uat.md UAT-01 Result block (line 217), which quotes 'all holding', is left to the wave 15b docs seat as D50 routes it. From this build on, the same reading would say 'none failing: ... N pending' or, after the refresh, count the handshake and probe rows as holding.

## Independent review

### review:snapshot: needs-fixes

- **minor** `internal/contract/refresh.go:67 (RefreshFromHistory) with internal/contract/assertions.go:174-177` — The refresh only ever turns a pending row into a holding one. D50 asks that status and doctor read what history.json already knows, and history.json can also record a failure: once a session's prompts miss the probe twice, Sentinel.Chances is 2 or more, and the check itself returns OK:false 'sentinel not found after two chances'. Status keeps printing '  pending: hook.additional_context_delivered (not-yet-observed)' for the rest of that session, so the banner calls pending something history.json already records as failed.
  - Evidence: provenByHistory returns the check's row only when StandingOf(r)==StandingHolding (refresh.go:57). A probe history with Chances>=2 and Observed=false makes checkAdditionalContextDelivered fail, and that result is discarded. TestStatus_AMissedSentinelIsNotRefreshedIntoAnObservation covers only a single miss (Chances=1), so this case is untested.
  - Fix: Choose one: (a) copy Sentinel.Chances into the probe history and let a pending row show the check's non-OK result (pending to failing, severity as the check gives it; the mode is still left to the next start); or (b) keep refreshing to holding only, but stop calling the row pending once history.json shows its chances are spent, and document that. Add a row with two missed scans either way.
- **minor** `internal/cli/doctor.go:82` — The comment on doctorRow.Coverage still says coverage 'is rendered EXACTLY as the observation ledger stored it'. After e1369ed9 that is no longer true: capabilityRow now renders the coverage RefreshObservation re-derives from history.json, for example CoverageDeliveryUnderTestedContract for injection, which the ledger never stored. The doctor.go header (rule 1, lines 37-40) describes capability rows as citing 'the evidence register and the observation ledger' and says nothing about history.json.
  - Evidence: doctor.go:595-596: `obsv, fromHistory := contract.RefreshObservation(...)` followed by `row.Coverage = obsv.Coverage`. TestDoctor_AnObservedProbeRefreshesTheInjectionRow asserts coverage delivery_under_tested_contract while the ledger entry holds 'none'.
  - Fix: Update the doctorRow.Coverage comment and rule 1 in the header: coverage is the ledger's newest entry, or its refresh from state/history.json (D50), derived by ObservationsOf's rules, and never 'complete'.
- **minor** `docs/troubleshooting.md:147-149; internal/commands/render.go:127` — For the standard nine assertions, 'all holding' can no longer be printed. precompact.custom_instructions_accepted always reads 'retired' (checkPreCompactCustomInstr), or 'not-yet-implemented' when its producer is undeclared, and both count as nothing to judge. A fully healthy steady-state project therefore reads 'host contract: 9 assertion(s), none failing: N holding, 0 pending, M with nothing to judge'. The docs present 'all holding' as a reading a user can reach and never say what the healthy steady-state banner looks like. Two expected results in docs/uat.md quote 'all holding' and can never be reproduced: line 219 (which the implementer routed to wave 15b) and line 331 (C4.2 re-check), which the open issues do not mention.
  - Evidence: assertions.go:233-237 returns Observed "retired" unconditionally. StandingOf maps it to StandingIdle, so `holding == len(results)` (render.go) is false for every nine-row snapshot. `git grep 'all holding' docs/uat.md` returns lines 219 and 331.
  - Fix: In troubleshooting.md, state that the standard set always carries at least one row with nothing to judge (the retired custom-instructions row), so a healthy project reads 'none failing: ... 0 pending, ...', and give that as the healthy example. Add docs/uat.md:331 to the note routing uat.md to the wave 15b docs seat alongside line 219.
- **nit** `plans/sdd/V6-closeout/w15a-snapshot/runs/red-daemon.log` — The committed RED log does not come from the committed test file. It cites contract_snapshot_test.go:99, :122 and :147, but the failing assertions are at lines 86, 104 and 129 in HEAD, so the test was edited after the RED capture. I rebuilt the base efd5bf68 with the committed test files copied in, and the same seven rows fail for the stated reasons (initialize-pending, not-yet-observed, outcome not_observed, and the old banner). The RED is real, but the log is not evidence for these exact tests.
  - Evidence: grep -n in HEAD: line 86 `require.Equal(t, "initialize-received", row.Observed`, line 104, line 129. red-daemon.log: 'contract_snapshot_test.go:99', ':122', ':147'.
  - Fix: Re-capture red-*.log by running the committed tests against efd5bf68 (for example a git archive of the base plus the new test files), or add a note to the evidence saying the logs predate the test edits and name the re-verification.
- **nit** `commit fc21a9cb` — The evidence commit has a subject but no body and no Refs footer. The other five commits carry 'Refs: V6-VERIFY, C4.5'.
  - Evidence: `git log -1 --format=%B fc21a9cb` prints only 'test(evidence): record the w15a snapshot red and green runs'.
  - Fix: When the branch is next rewritten or squashed for integration, add 'Refs: V6-VERIFY, C4.5' to this commit.

## Fix seat (review resolution) — status `done`, head `e2be800f50a3a9841e4e97efa16959bbeee68656`

### Root cause

Reviewer finding 1: the D50 refresh (internal/contract/refresh.go, provenByHistory) passed the checks a probe history that held only MCPInitialized and Sentinel.Observed. It returned the check's row only when StandingOf was StandingHolding. history.json also records the probe's failure: SentinelState.Chances counts the session's prompts that missed the token its start minted (RecordSentinelScanOf), and the mint in handleSessionStart is the only thing that resets it. So with Chances>=2 and Observed=false, the check (checkAdditionalContextDelivered) reads "sentinel not found after two chances". The refresh did not copy Chances, so it never saw that failure, and status kept printing "pending: hook.additional_context_delivered (not-yet-observed)" for a failure history.json already recorded. A second problem was hidden behind the first: the refresh calls the raw check, not the gated assertion, so a failing row would have come back at SevInfo (the zero value) instead of the declared SevCritical.

### Summary

FIX seat for w15a-snapshot (D50). I verified all three reviewer findings independently against the code and all three were correct. I fixed them with failing tests written first. The branch closeout/w15a-snapshot is at e2be800f, 7 new commits on top of the implementer's 6, cut from efd5bf68. The worktree is clean. Nothing was pushed or merged, and there are no new budgets or bounds.

## What changed
- internal/contract/refresh.go: provenByHistory is renamed readByHistory, and historyProven is renamed historyRead.
  - The probe history now carries Sentinel.Chances as well as Observed.
  - A pending row is replaced when the check now reads it as holding or as failing. It stays pending otherwise.
  - A failing row gets its severity from declaredSeverity(id), which looks the id up in StandardAssertions() so there is no second copy of the severity table. That makes the probe row SevCritical, the same as gated gives it at a start.
  - A failing row also gets a Detail (refreshedFailureDetail): "read from state/history.json after this session's start; the next SessionStart evaluates it and applies it to the mode".
  - The refresh never changes the mode. The only consumer of StatusSnapshot.Contract is rendering (renderContract via internal/cli/qompack_commands.go:303).
  - The MCP row can still only become holding or stay pending: with no session in its Env, the check returns initialize-pending when there is no handshake.
- RefreshFromHistory and RefreshObservation docs updated. For doctor, a pending injection entry whose chances are spent now reads outcome failed with coverage none (ClassifyResult / coverageOf).
- internal/daemon/contract_snapshot.go: doc comment only. The single-miss daemon test's comment now says the probe still has a second chance.
- internal/cli/doctor.go: comments only (finding 2).
  - Header rule 1 now says a pending ledger entry is read against state/history.json, which is a record the daemon kept, not a probe.
  - The doctorRow.Coverage comment now says coverage is the ledger's newest entry or its refresh from history.json, derived by ObservationsOf's rules, and never "complete".
- docs/troubleshooting.md §1 (finding 3):
  - The standard nine always include precompact.custom_instructions_accepted reading retired (or not-yet-implemented), so the standard set never reads "all holding".
  - The healthy example is now "host contract: 9 assertion(s), none failing: 7 holding, 0 pending, 2 with nothing to judge".
  - Two chances missed reads "sentinel not found after two chances" and counts as failing, with a note that it was read from history.json; the mode changes only at the next SessionStart.
  - doctor reports the spent chances the same way.

## Tests (RED first)
I wrote each new row first and ran it before the fix. All four fix rows failed on the old code:
- TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing: row.OK was true.
- TestRefreshObservation_SpentChancesReadAsAFailedInjection: changed was false.
- TestStatus_TwoMissedSentinelScansReadAsFailing: status answered `{OK:true Severity:0 Observed:not-yet-observed}`.
- TestDoctor_SpentProbeChancesReadAsAFailedInjection: the detail read "outcome not_observed".

After the fix all four pass. Two render rows were added after the fix:
- TestRenderStatus_ASpentProbeReadFromHistoryIsFailing puts RefreshFromHistory and renderContract together.
- TestRenderStatus_AHealthyStandardSetReadsNoneFailing pins the healthy banner the docs now quote. It was green on its first run because it pins existing behaviour; it is not a regression test.

The touched packages each pass in full once with -p 2: contract, commands, test/docs, cli (87.8s) and daemon (277s). fmt-check, go vet (Windows and GOOS=linux) on the four touched packages, the permitted lint sub-checks and gen-config-docs --check all pass. The first golangci-lint run failed only because another seat held its lock ("parallel golangci-lint is running"); the rerun alone passed. Evidence logs are committed under plans/sdd/V6-closeout/w15a-snapshot/runs/fix-*.log.

## Not run
Per the seat's limits I did not run whole test/e2e or test/integration packages, the hot-path rows, -race, or the Linux container. I checked which e2e tests use the spent-probe spelling by reading them:
- test/e2e/v5_x14_test.go:492 reads the observation ledger directly, not doctor or status.
- test/e2e/spooled_sessionstart_test.go and test/fault/publication_test.go quote it only in comments or daemon log text.

None of them goes through the refresh, so the change cannot affect them.

## Review resolution
1. Finding 1 (minor, refresh never shows a spent probe as failing): correct, fixed with option (a) in 0cdf7ea4. I also found and fixed the severity problem described in the root cause. Pinned in contract, daemon, cli and commands (63f053da, 633dce22, 7e88d871).
2. Finding 2 (minor, stale doctor.go comments): correct, both comments updated in bb09d2f3.
3. Finding 3 (minor, "all holding" can never appear for the standard nine): correct; assertions.go returns "retired" unconditionally and StandingOf reads it as nothing to judge.
   - The troubleshooting.md part is fixed in 0c54c249, and 7e88d871 pins the example banner the docs quote.
   - docs/uat.md lines 219 and 331 were NOT edited: uat.md belongs to the wave 15b docs seat and is outside this seat's scope. One correction to the reviewer's wording: both lines are recorded results of earlier runs (candidate 4's re-check and the C4.2 re-check), not expected results for a future run. They quote what the pre-D50 binary printed. Both lines go to the 15b docs seat together (the implementer routed only 219), so it can add a note that the post-D50 banner reads "none failing: … 0 pending, …" instead.

## Criterion changes
None. No assertion was loosened and no test was skipped. No golden or threshold changed.

### Commits

- 0cdf7ea4 fix(contract): read a probe's spent chances from history.json
- 63f053da test(daemon): pin status reading two missed probe scans as failing
- 633dce22 test(cli): pin doctor reading spent probe chances as a failure
- bb09d2f3 docs(cli): say doctor's coverage may come from history.json
- 7e88d871 test(commands): pin the healthy banner and a spent probe row
- 0c54c249 docs(troubleshooting): give the healthy banner and spent probe row
- e2be800f test(evidence): record the w15a snapshot fix-seat runs

### Tests

- `go test -p 2 -count=1 -run 'TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing|TestRefreshObservation_SpentChancesReadAsAFailedInjection' ./internal/contract/` — RED before fix (exit 1: row.OK true; changed false), PASS after (runs/fix-red-contract.log, fix-green-contract.log)
- `go test -p 2 -count=1 -run 'TestStatus_TwoMissedSentinelScansReadAsFailing' ./internal/daemon/` — RED before fix (status row {OK:true Severity:0 Observed:not-yet-observed}), PASS after (fix-red-daemon2.log, fix-green-daemon2.log)
- `go test -p 2 -count=1 -run 'TestDoctor_SpentProbeChancesReadAsAFailedInjection' ./internal/cli/` — RED before fix (detail 'outcome not_observed'), PASS after (fix-red-doctor2.log, fix-green-doctor2.log)
- `go test -p 2 -count=1 -v -run 'TestRenderStatus_AHealthyStandardSetReadsNoneFailing|TestRenderStatus_ASpentProbeReadFromHistoryIsFailing' ./internal/commands/` — PASS, both rows ran (fix-green-render2.log); the healthy-set row pins existing behaviour for the docs
- `go test -p 2 -count=1 ./internal/contract/ ./internal/commands/ ./test/docs/` — ok x3 (fix-full-contract-commands-docs.log)
- `go test -p 2 -count=1 ./internal/cli/` — ok 87.8s (fix-full-cli.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 277.0s (fix-full-daemon.log)
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/contract/ ./internal/daemon/ ./internal/cli/ ./internal/commands/ (Windows and GOOS=linux)` — exit 0 both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS except golangci-lint, which failed only because another seat held its lock ('parallel golangci-lint is running') (fix-lint.log); rerun --only=golangci-lint PASS (fix-lint-golangci.log)
- `go run ./tools/devtool gen-config-docs --check` — up to date, exit 0

### Open issues

- docs/uat.md:219 and docs/uat.md:331 quote the pre-D50 banner 'all holding' in recorded results. The standard nine can no longer print that reading. Both go to the wave 15b docs seat for a note, not a rewrite of the historical record; the implementer routed only line 219.
- Not re-run under this seat's daytime limits: whole test/e2e and test/integration packages, the hot-path rows, -race, and the Linux container (stopped). By reading them, no e2e or fault test reads status or doctor output on the spent-probe path.
- Known behaviour, not changed: a row that was already failing at the start (for example the probe failing, then observed after the next mint) stays failing in status until the next SessionStart. RefreshFromHistory only settles pending rows, which is the implementer's design and what the troubleshooting page states.

## Independent verification of the fix seat: sound

- **nit** `internal/contract/refresh.go:22-26 (historyRead doc comment)` — Fix commit 0cdf7ea4 added a new comment that says SentinelState.Chances is reset only by the start's mint ('only that start's mint resets it'). The fix seat's root_cause makes the same claim. Two other paths also reset it. daemon.withdrawLostStartAnswer zeroes Chances when a start's reply was lost. RecordSentinelScanOf(found=true) also zeroes it, although Observed is true on that path. The code behaves correctly: after a withdrawal the refresh reads the row as pending again. Only the comment is inaccurate.
  - Evidence: internal/daemon/handlers.go:1007-1009: `if lost.token != "" && h.Sentinel.Token == lost.token { h.Sentinel.Token, h.Sentinel.Session, h.Sentinel.MintedAt, h.Sentinel.Chances = "", "", 0, 0 ...}`. internal/contract/history.go:292-295 sets Chances = 0 on a find.
  - Fix: Reword the comment, for example: 'a start's mint (or the withdrawal of a lost start's probe) resets it'. No code change is needed.


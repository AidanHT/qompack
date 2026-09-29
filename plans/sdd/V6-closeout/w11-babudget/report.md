# Wave 11 — w11-babudget: platform-derived B-A budget (D41)

Branch `closeout/w11-babudget`. Workflow `wf_002f8b87-e4d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `partial`, head `275f6048`

### Root cause

The daemon's gated B-A sample (internal/daemon/handlers.go recordHotPathSample) is (recvTS-reqTS) + measured pre-ACK handler time + a 1 ms tail allowance. It has included the handler since the SP20-D6 fix c78f610 (2026-09-21), and that handler time is B-B's durable ingest.Accept. B-B was re-budgeted per platform (15/50/40 ms) when the V5 ruling kept fsync-before-ACK. runtime.hotPath.budgetMs stayed at 15 on every platform. So on Windows, a delivery that met its own B-B budget still breached B-A, and the §8.1 breach detector moved the daemon to spool after 3 x 512 samples.

### Summary

D41 is implemented and verified on Windows. The previous seat's three commits hold up on review. runtime.hotPath.budgetMs now defaults to config.HotPathBudgetMsFor(l0IngestMsDefault()), which is max(HotPathBudgetMsFloor, l0IngestMs): 15 on linux, 50 on Windows, 40 on darwin. It is built from existing constants the same way ackDeadlineMsDefault() is. HotPathBudgetMsFloor = 15 is the old literal from defaults.go given a name, not a new number. A budgetMs the user sets is still applied exactly as written.

The derived value reaches every place that used the default. The schema golden now treats budgetMs as a fourth platform-specific leaf. gen-config-docs renders it as "`15` (`50` on Windows, `40` on macOS)" from the function. obs, guards and the bench tests read the limit instead of spelling 15. The degrade test's stall is now budget + 10 ms, which is 25 ms on linux as before, so it still breaches at 50. Docs updated: 00-ARCHITECTURE §2.4 (B-A row and D41 note) and §7, docs/architecture.md, docs/cannot-do.md, and Qompack.md v1.8 with its revision-log entry, ERRATA and digest pin. test/e2e/v3_x11_test.go already derived its message text from obs.Budgets(), so it needed no change.

My review changes:
1. 00-ARCHITECTURE's SP20-D6 B-A note was stale. It still said the gated sample leaves out the pre-ACK handler and named a test that has since been renamed. The D41 note relies on it, so I recorded the c78f610 fix (evidence test TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler).
2. I rewrapped comments and prose the draft had left broken mid-sentence or over-long. No wording changed.
3. TestDefaults_HotPathBudgetPerPlatform now runs one subtest per platform. Before, a regression showed only windows red and hid darwin.

RED/GREEN evidence:
- Before the fix: TestDefaults_RuntimeNamespace and TestDefaults_HotPathBudgetCoversTheIngestBudget were red on Windows ("15" is not >= "50"). The darwin vet failed on the undefined HotPathBudgetMsFor. The degrade premise was red ("25ms" is not greater than "50ms").
- Temporary mutation (reverted) making HotPathBudgetMsFor return the pre-D41 15: PerPlatform/windows and PerPlatform/darwin FAIL, linux_portable PASS; the other two rows are also red on Windows.
- GREEN: all 12 touched rows pass at -count=3.

Isolated wall-clock rows on Windows (each run alone, QOMPACK_UNDER_COLOAD unset, nothing else running):
- TestIntegration_HotPathWarmWithRealResidentState: PASS in 186.7 s. B-A n=2064, p50 24.576, p99 32.768 ms against the 50 ms limit. B-B p99 14.336 ms against 50. hook_controlled_observed p50 6.144, p99 11.264 ms. B-E wall p99 205.9 ms. No spool transition. This agrees with the previous seat's run (B-A p99 32.768, B-B p99 12.288). That seat's re-run was cut off by the overnight pause: spawn #1487 exited 0xc0000142 at 23:09. The log is kept as integration-hotpath-isolated-windows-rerun-interrupted-by-pause.log.
- TestV3_HotPathUnchangedWithLedgerResident: FAIL, third time in a row, only on the V2-relative ceiling. The harness gate passes: B-A p99 28.672 ms (limit 50) and B-B p99 13.312 ms (limit 50) are both PASS, hook_controlled_observed p99 9.216 ms, and there is no spool transition. The assertion is "X11: B-A p99 regressed more than 25% against V2's recorded 3.072ms (got 28.672ms, ceiling 3.840ms)"; the earlier two runs got 24.576 ms. I did not change the ceiling; the evidence for the owner decision is under needs_owner. Before D41 this ceiling was hidden, because the harness failed first on B-A against 15 ms (Phase 3's p3-win-e2e-timing.log).

Spool recovery (investigation only, nothing changed): within one session and one daemon lifetime, spool submode never returns to sync. ToSync can't be reached in production. Once in spool, every live hot-path request stops reaching the daemon. The only hot-path samples it still gets are spool replays, and those can only count as breaches or be thrown away. It ends only with a SessionStart for a new session id (any session in the project) or a daemon restart, including idle exit 1800 s after the last session ends. The file:line evidence is in open_issues.

### Commits

- 748f43ef fix(config): derive the B-A default budget from the platform B-B (previous seat, reviewed)
- 9700950b docs(arch): state B-A's per-platform default and its D41 origin (previous seat, reviewed)
- c3782687 docs(qompack): revise to v1.8 for the hot-path budget floor (previous seat, reviewed)
- adc44857 docs(arch): close the SP20-D6 note and rewrap the D41 edits
- d0fb4983 test(config): report each platform's D41 default as a subtest
- 275f6048 docs(v6): record w11-babudget's Windows evidence

### Tests

- `go test -count=1 -v -run '^(TestDefaults_HotPathBudgetPerPlatform|TestDefaults_HotPathBudgetCoversTheIngestBudget|TestDefaults_RuntimeNamespace)$' ./internal/config  (TEMPORARY DIAGNOSTIC: mutation, HotPathBudgetMsFor returns the pre-D41 15; reverted)` — FAIL as intended: PerPlatform/windows and PerPlatform/darwin red, linux_portable green, the other two rows red on Windows (runs/mutation-pre-d41-default-windows.log)
- `go test -count=3 -v -run '^<row>$' <pkg> for TestDefaults_HotPathBudgetPerPlatform, TestDefaults_HotPathBudgetCoversTheIngestBudget, TestDefaults_RuntimeNamespace, TestLoad_UserSetHotPathBudgetIsKept, TestJSONSchema_Golden (./internal/config); TestBudgets_AllSixPresentAndConfigDriven, TestBudgets_BGCoversTheDegradedSpoolAppend, TestCheckBudgets_CountsConsecutiveWindows, TestCheckBudgets_ResetsStreakWhenBackUnderBudget (./internal/obs); TestBudgetLimit_ReadsFromConfigDefaults, TestBAWallWaivedNote_NamesTheLimitItDidNotApply (./test/bench/hotpath); TestV1_ObsBudgetsAreConfigDrivenEndToEnd (./test/guards)` — PASS, 45 PASS and 0 FAIL, every exit=0 (runs/new-rows-count3-windows-resume.log)
- `go test -count=1 -p 4 -timeout=30m ./internal/config ./internal/obs ./internal/daemon ./internal/commands ./internal/ipc ./test/bench/hotpath ./tools/devtool` — PASS, all ok, exit=0 (runs/pkgs-windows-resume.log)
- `go test -count=1 -p 4 -timeout=30m ./test/guards` — FAIL only on the expected TestCarriedDefects_WaveReportRequiresResolution five perf rows (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2); TestQompackChangesOnlyThroughAnAuthorizedRevision passes (runs/guards-windows-resume.log)
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check; go test -count=1 ./test/docs` — PASS, all up to date, exit=0 (runs/gens-docs-windows-resume.log)
- `go run ./tools/devtool fmt-check; GOOS={windows,linux,darwin} go vet ./internal/config ./test/integration ./internal/obs ./test/guards ./test/bench/hotpath ./tools/devtool` — PASS, exit 0 on all three GOOS
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS, all 8 sub-checks (runs/lint-windows-resume.log)
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathDegradesRatherThanBlocks$' ./test/integration` — PASS, NAK after 1529 stalled sends (three windows of 512), 9 degraded hooks drained, 1 WARN (runs/degrade-green-windows-resume.log)
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration  (alone, QOMPACK_UNDER_COLOAD unset)` — PASS in 186.7 s: B-A p99 32.768 ms (limit 50, n=2064), B-B p99 14.336 ms (limit 50), observed p99 11.264 ms, no spool transition (runs/integration-hotpath-isolated-windows-resume.log)
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e  (alone, QOMPACK_UNDER_COLOAD unset)` — FAIL only on the V2-relative ceiling: B-A p99 28.672 ms vs ceiling 3.840 ms. Harness B-A and B-B rows PASS against 50 ms, no spool transition. Same result on all three runs, earlier two 24.576 ms (runs/e2e-x11-isolated-windows-resume.log, e2e-x11-isolated-windows*.log)

### Criterion changes

- The test/integration degrade row's stall is now the configured budget + 10 ms instead of a fixed 25 ms (previous seat). Rationale: under D41, Windows' 50 ms budget would sit above a fixed 25 ms stall and no window could breach, which made the row's own premise assertion fail. On linux it is still exactly 25 ms, and the premise that the stall exceeds the budget is still asserted.
- obs_test's over-budget observation is now 2 x the platform's B-A limit instead of a fixed 50 ms (previous seat). Rationale: a fixed 50 ms sits exactly on Windows' new limit. The test is just as strict as before.
- TestDefaults_HotPathBudgetPerPlatform was split into per-platform subtests (this seat). Same expected values; it now reports every failing platform instead of stopping at the first.

### Open issues

- TestV3_HotPathUnchangedWithLedgerResident is red on Windows (isolated) because of the V2-relative B-A ceiling. It is not a D41 defect, and it will stay red until the owner rules (see needs_owner). It was hidden before D41 because the harness failed on B-A against 15 ms first.
- SPOOL RECOVERY (report only): within one session and one daemon lifetime the daemon never returns to sync. Evidence: (1) the daemon writes state.bin with hot=spool before it flips (handlers.go applyHotPathTransition -> persistHotMode). (2) Every hook process reads state.bin once at startup (internal/cli/hookclient.go:334 ipc.ReadState; ipc/client.go NewClientWithOptions copies st.Hot). For observe.tool, observe.prompt and observe.stop (ipc/op.go:59 HotPath) it spools without connecting (ipc/client.go:215). A hook already in flight gets a NAK and sets hot=spool for its own process. (3) So no live hot-path sample reaches the daemon. The breach detector (daemon/budget.go Observe) only closes a window when samples arrive, so the need=3 clean windows ToSync requires can never happen. (4) The only hot-path samples it still receives are spool replays. drainDispatch calls dispatchOp (daemon.go:1094), which calls recordHotPathSample (handlers.go:278) with no spoolReplay exclusion. A replay's recvTS-req.TS is how long the line sat in the spool. Over 10 s it is discarded as invalid (hotPathSampleMaxAge); under 10 s it is far above any budget. Either way replays push toward breach, never toward clean. What ends spool mode: (a) a SessionStart for a session id the registry has not seen. registry.Ensure resets hot to HotSync (registry.go:181), breach.Reset runs, and handleSessionStart rewrites state.bin from the registry (handlers.go:848). A compact start reuses its session id and does not reset. A second concurrent session does reset the mode for every session, so the first session can flap back into spool. (b) A daemon restart, where Run writes state.bin from a fresh registry (daemon.go:716). That happens on idle exit (runtime.daemon.idleExitSeconds, default 1800 s, after Live() reaches zero; session.end is not a hot-path op, so it still connects and ends the session; a silent session is swept by EndAbandoned after one window), or on a crash. This matches §12.2's 'reverts ... in a subsequent session' but not the ToSync code path, which is effectively dead in production. A separate side finding: spool replays inside 10 s contaminate the hook_controlled histogram and the breach detector with spool age. About 6 such replays in one 512-sample window are enough to make it a breach window (p99 index 506). The one-line change (skip recordHotPathSample when spoolReplay(ctx)) was NOT made, because it changes the detector's behaviour and still would not restore ToSync. Coordinator decides.
- The TestIntegration_HotPathWarmWithRealResidentState doc comment still says 'B-B p99 < 2ms', which has been stale since SP20-D1. Left alone as out of scope.
- Several code comments outside scope still quote '§8.1 p99 < 15 ms' as the hot-path figure: internal/canon/generic.go:229, internal/canon/tools.go:41, internal/cli/bench_test.go:12, internal/sketch/bench_test.go:20, internal/store/stats_test.go:63, test/e2e/v5_x01_test.go:19. 00-ARCHITECTURE §2.1 quotes the original Qompack.md target. None of these state the config default, so I left them.
- Linux and darwin are not verified here; the container was stopped by instruction. Coordinator commands (Git Bash, host, from this worktree): (1) sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w11-babudget --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w11-babudget --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w11-babudget/plans/sdd/V6-closeout/w11-babudget/runs/linux 275f6048 w11-touched-race -- ./internal/config ./internal/obs ./internal/daemon ./internal/commands ./internal/ipc ./test/bench/hotpath ./test/guards  (expect only the CarriedDefects perf rows red in guards). (2) Same host options, then: 275f6048 w11-hotpath-isolated --no-race --timeout 60m --run '^TestIntegration_HotPathWarmWithRealResidentState$' -- ./test/integration  (expect PASS with B-A p99 under 15 ms on linux, where D41 changes nothing, and no spool transition). (3) Same host options, then: 275f6048 w11-x11-isolated --no-race --timeout 60m --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e  (expect the V2 ceiling to fail as on Windows unless linux B-A p99 is 3.84 ms or less, which is unlikely: B-A contains the durable fsync ingest). (4) Same host options, then: 275f6048 w11-degrade --no-race --run '^TestIntegration_HotPathDegradesRatherThanBlocks$' -- ./test/integration. darwin is checked only by GOOS=darwin vet and the pure-function subtest; macos-latest CI is the real check.

### Needs the owner

- X11 V2-relative B-A ceiling (test/e2e/v3_x11_test.go x11V2BAp99Ms = 3.072, x11RegressionFactor 1.25, ceiling 3.840 ms). What V2's 3.072 ms measured: windows/amd64, the dev laptop on a quiet machine (plans/V2-report.md §9.1 row B-A and §4.6 row line 453, artifact v2-hotpath-final.json, 2026-08-22/23, report commit bc89e7ae), reproduced on a windows-latest runner (V2-report §16.2); V3 recorded 2.048 ms (V3-report line 205). At that time the B-A sample was recvTS - req.TS + 1 ms only, with the handler excluded (3cd8719a handlers.go:191 'estimated := observed + hotPathTailAllowance'). Ingest was not durable: B-B p99 was 0.576-0.768 ms against a 2 ms budget. Since then two approved changes have moved what B-A measures. First, f6a86918 (2026-09-07) put three fsyncs before the ACK, and SP20-D1 (5d0b904/cbfa3d3) re-budgeted B-B to 15/50/40 ms. Second, the SP20-D6 fix c78f610f (2026-09-21) added the measured pre-ACK handler time to the gated sample. Today's B-A therefore contains B-B, about 9-14 ms on this host, by construction; the 3.072 ms figure contains neither. The like-for-like part (hook_controlled_observed, recvTS-reqTS) is now p99 6.1-11.3 ms on isolated Windows runs, against roughly 2 ms implied at V2. So the band also shows some real growth in the non-ingest part, but it cannot be measured against a figure that leaves out the handler. The V5-report ruling (line 1600: 'the ceiling is correct') was made before c78f610, while the sample still left out the handler. Recommendation: do not relax the band. Rebase it on a quantity whose definition has not changed. Option A (preferred): compare hook_controlled_observed p99 against a re-recorded V6 isolated baseline taken on the candidate, same host class, per platform. Option B: make X11 a paired same-run comparison, the no-ledger harness vs the ledger-resident harness, which is what 'the ledger must not move the hot path' actually asks. Either needs a newly recorded baseline number, which is the owner's. Until then X11 stays red on isolated Windows runs and probably on Linux too.
- HotPathBudgetMsFloor = 15 (internal/config/deadlines.go): not a new number. It is the value every platform shipped with until D41, now named. Derivation: §8.1/§11.3 L0's 15 ms. If it is wrong: linux's B-A default would be too tight or too loose. It does not affect Windows or darwin, where l0IngestMs is higher.
- hotpathStallOverBudget = 10 ms (test/integration/hotpath_test.go): a test stimulus, not a budget. Derived from §4.6's 25 ms stall minus the 15 ms budget it was written against, so linux keeps exactly 25 ms. If it is wrong: only the degrade test's margin over the budget changes; any positive value still breaches.

## Independent review

### review:babudget: needs-fixes

- **major** `implementer report open_issues 'SPOOL RECOVERY' item (4) and its side finding; code at internal/daemon/daemon.go:1076-1095, internal/daemon/handlers.go:213-218` — Part of the spool-recovery investigation is wrong. It says spool replays reach the breach detector because 'drainDispatch calls dispatchOp (daemon.go:1094), which calls recordHotPathSample (handlers.go:278) with no spoolReplay exclusion'. From that it concludes that replays younger than 10 s 'contaminate the hook_controlled histogram and the breach detector with spool age' (about 6 per window make it a breach window), and it offers a one-line fix for the coordinator to decide on. None of this happens. drainDispatch's `case req.Op.HotPath(): return d.runIngested(ctx, req)` sends every replayed observe.tool/prompt/stop line straight to runIngested. Only the `default:` branch (session.start/checkpoint/status/mcp) calls dispatchOp(withSpoolReplay(ctx), req), and daemon.go:1094 is that branch. dispatchOp's own doc comment (handlers.go:213-218) says hot-path ops 'go straight to runIngested'. recordHotPathSample has exactly one caller (handlers.go:279), inside dispatchOp. The spool watcher (spool_watch.go, DrainClientSpools) uses the same Dispatch = drainDispatch (daemon.go:947).
  - Evidence: daemon.go:1077-1085 `case req.Op.HotPath(): ... return d.runIngested(ctx, req)`; daemon.go:1094 `return d.dispatchOp(withSpoolReplay(ctx), req)` sits under `default:`; `grep -rn 'recordHotPathSample(' internal/` finds only handlers.go:279. The headline conclusion still holds and is actually stronger: once in spool, the detector gets no hot-path samples at all, apart from hooks that were already connected before the flip. ToSync is unreachable within one session and one daemon lifetime. Recovery comes only from a new session id (registry.go:174-186, Ensure resets hot) or a daemon restart. The proposed 'skip recordHotPathSample when spoolReplay(ctx)' change would do nothing for hot-path ops.
  - Fix: Correct the report before the coordinator relies on it. Remove item (4)'s replay path and the contamination side finding. State that spool replays never produce B-A samples (drainDispatch sends hot-path ops to runIngested), so in spool submode the detector is starved, not contaminated. Remove the suggested one-line change. Keep conclusions (a) and (b) on what ends spool mode.
- **minor** `Qompack.md:325 (§8.1, new last sentence)` — The new §8.1 sentence reads as an unconditional invariant: 'The hook's hot-path latency budget is never tighter than the durable capture it waits on ... the hook budget on each platform is at least that platform's durable-ingest budget.' D41 and the code apply it only to the DEFAULT. A user-set budgetMs below l0IngestMs, or a user-set l0IngestMs above the derived default, breaks it, and validate.go has no check. deadlines.go's comment says the two keys stay independent on purpose, so the design doc and the code disagree.
  - Evidence: internal/config/deadlines.go HotPathBudgetMsFor doc: 'It derives only the DEFAULT ... a user-set l0IngestMs does not move budgetMs'. TestLoad_UserSetHotPathBudgetIsKept loads budgetMs=7 with no warning. validate.go:239-241 checks only budgetMs > 0.
  - Fix: Reword the §8.1 sentence to say 'the hook budget's default on each platform is at least that platform's durable-ingest default; a configured value is applied as set'. This needs the same v1.8 revision and a digest re-pin. Alternatively, record under needs_owner whether Load should WARN when budgetMs < l0IngestMs. Do not enforce it silently.
- **nit** `internal/daemon/doc.go:4; internal/daemon/spawn.go:131; cmd/qompack/main.go:6` — These package and file doc comments still state B-A as a fixed 15 ms ('a hook has 15 ms at p99', 'the B-A hot path (budget 15 ms)', 'budget B-A (§2.4) allows 15 ms p99'). They describe the B-A budget itself, not the §8.1 PostToolUse design target, and internal/daemon is in scope. The implementer's list of out-of-scope stale comments leaves them out.
  - Evidence: grep -rn '15 \?ms' --include=*.go internal/ cmd/ | grep -v _test
  - Fix: Reword to 'B-A (runtime.hotPath.budgetMs: 15 ms on linux, 50 Windows, 40 macOS, D41)', or add these three to the reported list of stale comments.
- **nit** `plans/QOMPACK-ERRATA.md:436` — 'The first two were red on Windows before the change' refers to TestDefaults_HotPathBudgetPerPlatform and TestDefaults_HotPathBudgetCoversTheIngestBudget. The cited red log shows TestDefaults_RuntimeNamespace and CoversTheIngestBudget red. PerPlatform did not compile at RED (undefined HotPathBudgetMsFor) and went red only in the later mutation run.
  - Evidence: plans/sdd/V6-closeout/w11-babudget/runs/red-windows.log: FAIL TestDefaults_RuntimeNamespace, FAIL TestDefaults_HotPathBudgetCoversTheIngestBudget, vet 'undefined: config.HotPathBudgetMsFor'
  - Fix: Name the rows that were actually red, and cite mutation-pre-d41-default-windows.log for PerPlatform/windows and PerPlatform/darwin.
- **nit** `plans/00-ARCHITECTURE.md:329` — Calls it 'Owner decision D41'. The ledger, Qompack.md v1.8 and ERRATA call it a coordinator decision under D33.
  - Evidence: V6-CLOSEOUT-CHECKLIST.md:60 '(2026-09-28, coordinator under D33)'; Qompack.md:493 '(coordinator decision D41)'
  - Fix: Change it to 'Coordinator decision D41 (under D33)'.
- **nit** `commits 9700950b, c3782687, adc44857, d0fb4983, 275f6048` — Only 748f43ef has a Refs footer. No attribution trailers were found, which is correct. The base branch's recent commits mostly have no Refs footer either, so this is cosmetic.
  - Evidence: git log fc5289c..HEAD --format=%B
  - Fix: No action needed unless the coordinator requires Refs on every commit. If it does, add 'Refs: V6-VERIFY, C1/C5.1' during integration.

## Fix seat (review resolution) — status `done`, head `91271e8658cc07246bf28febbbbe310fe23cc504`

### Root cause

B-A's gated sample (internal/daemon/handlers.go recordHotPathSample) is (recvTS - reqTS) + the pre-ACK handler time + a 1 ms tail allowance. Because the ACK follows the durable ingest.Accept, the sample contains B-B's region by construction. The V5 ruling re-budgeted B-B per platform (L0IngestMsWindows 50, L0IngestMsDarwin 40, portable 15), but runtime.hotPath.budgetMs stayed at 15 everywhere. That value is both B-A's gated limit (internal/obs/budgets.go) and the §8.1 breach detector's limit (internal/daemon/budget.go). As a result, on Windows a delivery that was inside its own B-B budget still counted as a B-A breach, and the detector moved every session to spool submode after 3 x 512 samples. D41 fixes this: the default is now HotPathBudgetMsFor(l0IngestMsDefault()) = max(HotPathBudgetMsFloor=15, the platform's l0IngestMs default), which gives Linux 15, Windows 50 and darwin 40. A user-set value is applied as written.

### Summary

D41 is done and verified on Windows. The default B-A budget is now platform-derived: Linux 15 ms, Windows 50 ms, darwin 40 ms. The implementer's commits (748f43ef..275f6048) held up under review. This seat checked both reviewer findings independently and fixed one; the report text that the other finding flags is corrected below. On Windows, the integration hot-path row now passes with no spool transition. X11 still fails on its separate ceiling, which compares against V2's 3.072 ms. That ceiling was left unchanged, and the question is under needs_owner.

WHAT CHANGED (whole branch, fc5289c..91271e86)
- internal/config/deadlines.go: HotPathBudgetMsFloor = 15 names the value that already shipped. HotPathBudgetMsFor(l0IngestMs) = max(floor, l0IngestMs). hotPathBudgetMsDefault() = HotPathBudgetMsFor(l0IngestMsDefault()). The derivation comment cites D41 and explains why B-A must not be tighter than the B-B it contains. defaults.go now uses hotPathBudgetMsDefault(). No new literal was added.
- Tests: TestDefaults_HotPathBudgetPerPlatform has per-platform subtests pinning linux 15, windows 50, darwin 40 and B-A >= B-B. TestDefaults_HotPathBudgetCoversTheIngestBudget, TestDefaults_RuntimeNamespace (running platform) and TestLoad_UserSetHotPathBudgetIsKept (budgetMs=7 is kept) are added or updated.
- RED evidence: before the fix, runs/red-windows.log shows 'windows: runtime.hotPath.budgetMs (15) must be >= runtime.budgets.l0IngestMs (50)'. runs/mutation-pre-d41-default-windows.log is a temporary mutation diagnostic, since reverted: forcing the floor makes all three rows fail again.
- Other code: tools/devtool/genconfigdocs.go renders the Default cell as "`15` (`50` on Windows, `40` on macOS)". Test messages in test/integration/hotpath_test.go and test/bench/hotpath are now derived from the budget instead of hard-coding 15 ms. obs_test.go, test/guards/v1_integration_test.go and schema_test.go are updated to match.
- test/integration/hotpath_test.go: hotpathStallOverBudget = 10 ms, so the stall for the degrade test is budget + 10. That is exactly 25 ms on Linux, as before.
- Docs: docs/architecture.md, docs/cannot-do.md, the generated docs/config-reference.md, 00-ARCHITECTURE.md (§2.4 B-A row, a D41 note, §7 bench-gate sentence), Qompack.md v1.8 with a revision-log entry, and a QOMPACK-ERRATA.md v1.8 record.
- B-B, the ACK deadline, BreachWindows, the certification rule and every other budget are unchanged.

WINDOWS VERIFICATION (evidence committed under plans/sdd/V6-closeout/w11-babudget/runs/)
- New rows, -count=3: pass (new-rows-count3-windows-resume.log).
- Packages, one full run: go test -count=1 -p 4 -timeout=30m ./internal/config ./internal/obs ./internal/daemon ./internal/commands ./internal/ipc ./test/bench/hotpath ./tools/devtool. All ok, exit=0 (pkgs-windows-resume.log).
- TestIntegration_HotPathWarmWithRealResidentState, run alone at d0fb4983 with QOMPACK_UNDER_COLOAD unset (integration-hotpath-isolated-windows-resume.log): PASS.
  - B-A n=2064 p50 24.576, p99 32.768 ms against the 50 limit.
  - B-B n=2064 p99 14.336 ms against 50.
  - hook_controlled_observed p99 11.264 ms.
  - The '§4.6 populations' line shows B-A n=2064 with 0 never reaching hook_controlled, and delivery ledger <nil>. Nothing was deferred to the spool, so there was no spool transition. Phase 3 by comparison had 593 deferred.
  - The rerun that the overnight pause interrupted is kept as integration-hotpath-isolated-windows-rerun-interrupted-by-pause.log. It is partial and not evidence. The resume run above replaces it.
- TestV3_HotPathUnchangedWithLedgerResident, run alone (e2e-x11-isolated-windows-resume.log): FAIL, only on the V2-relative ceiling.
  - B-A p99 28.672 ms against 50: PASS.
  - B-B p99 13.312 ms: PASS.
  - n=2064, so every request reached the daemon and there was no spool deferral.
  - The failure message: 'X11: B-A p99 regressed more than 25% against V2's recorded 3.072ms (got 28.672ms, ceiling 3.840ms)'. Two earlier runs at c3782687 failed the same way with 24.576 ms.
- The degrade test TestIntegration_HotPathDegradesRatherThanBlocks: PASS twice. Stalled sends 1529, NAK after three windows of 512, WARN lines=1.
- This seat's review fix (fix-review-docs-guards-windows.log, fix-review-lint-windows.log):
  - gen-config-docs, gen-mcp-docs and gen-command-docs --check: all up to date.
  - fmt-check: exit 0.
  - go test -count=1 ./test/docs: ok.
  - go test -count=1 -p 4 -timeout=30m ./test/guards: fails only on TestCarriedDefects_WaveReportRequiresResolution's five expected rows (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2).
  - go test -count=1 -v -run '^(TestQompackChangesOnlyThroughAnAuthorizedRevision|TestQompackDeclaredVersionIsRecorded)$' ./test/guards: FAIL before the re-pin (the guard working), PASS after it.
  - go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns: all PASS.
- The implementer ran go vet on the touched packages for GOOS=windows, linux and darwin, and it was clean. This seat changed only a string constant in test/guards and docs.

REVIEW RESOLUTION
1. [major] The spool-recovery item (4) said replays reach the breach detector. CONFIRMED correct and ACCEPTED. That claim was wrong.
   - drainDispatch (daemon.go:1076-1095) sends every req.Op.HotPath() line straight to d.runIngested. dispatchOp(withSpoolReplay(ctx), req) is only on the default: branch (session.start, checkpoint, status, mcp).
   - recordHotPathSample has one production caller, handlers.go:279, inside dispatchOp. The spool watcher uses the same Dispatch = drainDispatch (daemon.go:947).
   - So spool replays never produce B-A samples. They do not contaminate the hook_controlled histogram or the detector. The 'about 6 replays per window' contamination finding and the suggested one-line 'skip recordHotPathSample when spoolReplay(ctx)' change are both withdrawn. That change would do nothing for hot-path ops.
   - The wrong text was only in the implementer's returned report, not in any committed file (git grep confirmed). The corrected investigation below replaces it.
2. [minor] Qompack.md §8.1 stated an unconditional invariant. CONFIRMED and FIXED in 91271e86.
   - HotPathBudgetMsFor's doc derives only the default. TestLoad_UserSetHotPathBudgetIsKept accepts budgetMs=7, and validate.go checks only budgetMs > 0.
   - §8.1 now says the default budget on each platform is at least that platform's durable-ingest default, and that a configured value is applied as set. It stays within the same v1.8 revision.
   - The revision-log entry, the QOMPACK-ERRATA v1.8 record (which notes the correction), the 00-ARCHITECTURE §2.4 B-A row and docs/architecture.md were reworded to match.
   - qompackDigest was re-pinned to 562cc87c7548650d6b3e7c1c6c84f982311415e82fe30c878d98594999cbff6b.
   - No enforcement was added. Whether Load should WARN is listed under needs_owner.
   - This is a docs-only change, so there is no failing product test to write first. The revision guard was RED on the edit and green after the re-pin.

SPOOL-RECOVERY INVESTIGATION (corrected; nothing changed)
- Once the detector returns ToSpool, persistHotMode writes state.bin with hot=spool before registry.SetHotMode (handlers.go:434-446).
- Every hook process reads state.bin once (internal/cli/hookclient.go:334, ipc.ReadState). ipc.Client.Send step 3 (internal/ipc/client.go:215) then spools every hot-path op (observe.tool, observe.prompt, observe.stop) without dialing.
- Replays of those spool lines go to runIngested (above), so they produce no samples either.
- In spool submode the detector is therefore STARVED, not contaminated. The only samples it can still get come from hooks that had already passed step 3 before the flip. ToSync needs 3 consecutive clean full 512-sample windows, so it is unreachable within one session in one daemon's lifetime.
- What ends spool mode:
  - (a) A session.start for a session id the registry has not seen. That op is a Reply op, not a hot-path op, so hooks still dial it. handleSessionStart calls registry.Ensure, which resets hot to HotSync (registry.go:168-186). It also calls d.breach.Reset() and writes state.bin with the current state (handlers.go:801-805, 848), so later hooks read sync. A resume or compact start that reuses a session id the live daemon already knows does not reset.
  - (b) A daemon restart. The registry starts at HotSync, the zero value (ipc/wire.go:48), and Run writes state.bin (daemon.go:716). Idle exit needs Live()==0 for runtime.daemon.idleExitSeconds (default 1800 s; idleExitDue, daemon.go:838-857). SessionEnd's flush is not a hot-path op, so it still reaches the daemon and ends the session. Abandoned sessions are swept by EndAbandoned over the same window.
- Net effect: a degraded session stays degraded until it ends. Data is not lost, because the spool is drained by the watcher and idle drain. D41 removes the systematic Windows trigger, so this now matters only for genuine breaches. The coordinator decides whether recovery within a session is wanted.

EXACT LINUX COMMANDS FOR THE COORDINATOR (container qompack-v6-linux-verification, via the committed gate script at head 91271e86, prefix cx-w11-babudget, --repo/--out as C:/ paths)
- go test -count=3 -v -run '^(TestDefaults_HotPathBudgetPerPlatform|TestDefaults_HotPathBudgetCoversTheIngestBudget|TestDefaults_RuntimeNamespace|TestLoad_UserSetHotPathBudgetIsKept)$' ./internal/config
- go test -count=1 -p 4 -timeout=30m ./internal/config ./internal/obs ./internal/daemon ./internal/ipc ./test/bench/hotpath
- go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration (run alone; expect budget 15 and no deferral)
- go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathDegradesRatherThanBlocks$' ./test/integration
- go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e (run alone; the V2 ceiling will very likely still fail, see needs_owner)

CRITERION CHANGES: see criterion_changes.

### Commits

- 748f43ef fix(config): derive the B-A default budget from the platform B-B
- 9700950b docs(arch): state B-A's per-platform default and its D41 origin
- c3782687 docs(qompack): revise to v1.8 for the hot-path budget floor
- adc44857 docs(arch): close the SP20-D6 note and rewrap the D41 edits
- d0fb4983 test(config): report each platform's D41 default as a subtest
- 275f6048 docs(v6): record w11-babudget's Windows evidence
- 91271e86 docs(qompack): scope the v1.8 budget floor to the default (review fix, this seat)

### Tests

- `go test -count=3 -v -run '^TestDefaults_HotPathBudgetPerPlatform$' ./internal/config (and the other three new/updated config rows, -count=3)` — PASS x3 each (runs/new-rows-count3-windows-resume.log); RED before fix (runs/red-windows.log); RED again under reverted mutation (runs/mutation-pre-d41-default-windows.log)
- `go test -count=1 -p 4 -timeout=30m ./internal/config ./internal/obs ./internal/daemon ./internal/commands ./internal/ipc ./test/bench/hotpath ./tools/devtool` — all ok, exit=0 (runs/pkgs-windows-resume.log)
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration (alone, QOMPACK_UNDER_COLOAD unset)` — PASS: B-A n=2064 p99 32.768ms vs 50, B-B p99 14.336ms vs 50, 0 never reached hook_controlled, delivery ledger <nil> (no spool transition)
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e (alone, QOMPACK_UNDER_COLOAD unset)` — FAIL only on the X11 V2-relative ceiling: B-A p99 28.672ms (PASS vs 50), B-B p99 13.312ms PASS, n=2064; 'regressed more than 25% against V2's recorded 3.072ms (ceiling 3.840ms)' — ceiling not changed, see needs_owner
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathDegradesRatherThanBlocks$' ./test/integration` — PASS twice (NAK after 1529 = three windows of 512, WARN lines=1)
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check; go run ./tools/devtool fmt-check` — all up to date / exit 0 (runs/fix-review-docs-guards-windows.log)
- `go test -count=1 ./test/docs` — ok
- `go test -count=1 -v -run '^(TestQompackChangesOnlyThroughAnAuthorizedRevision|TestQompackDeclaredVersionIsRecorded)$' ./test/guards` — FAIL before re-pin (expected), PASS after re-pin to 562cc87c...
- `go test -count=1 -p 4 -timeout=30m ./test/guards` — FAIL only on the expected TestCarriedDefects_WaveReportRequiresResolution five perf rows (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS, exit=0 (runs/fix-review-lint-windows.log)

### Criterion changes

- runtime.hotPath.budgetMs DEFAULT (the B-A gated limit and the §8.1 breach-detector limit) changes from a universal 15 ms to max(15, the platform's l0IngestMs default): Linux 15, Windows 50, darwin 40. Rationale: D41. B-A's sample contains B-B's durable ingest by construction, so B-A cannot soundly be tighter than B-B. The number is composed from approved constants. A user-set value is unchanged.
- Qompack.md §8.1 / v1.8: states the floor as a property of the DEFAULT only (review fix). No enforcement of budgetMs >= l0IngestMs on user-set values.

### Open issues

- X11 (TestV3_HotPathUnchangedWithLedgerResident) stays red on isolated Windows runs because of its V2-relative ceiling of 3.840 ms. It will probably be red on Linux too. Owner decision below.
- Linux and darwin runs of the hot-path rows at the derived default have not been done. Linux is for the coordinator (commands in the summary). darwin relies on CI macos-latest, and its 40 ms inherits B-B's provisional darwin value.
- Spool submode cannot recover within a session: the detector is starved because hooks stop dialing, and replays go to runIngested. Only a new session id's session.start or a daemon restart (idle exit after runtime.daemon.idleExitSeconds with no live sessions) ends it. Report only; no change made.
- Low, comment-only, out of scope: code comments in internal/canon/generic.go:229, internal/canon/tools.go:41, internal/cli/bench_test.go:12, internal/sketch/bench_test.go:20 and internal/store/stats_test.go:63 still cite 'p99 < 15ms' as the hook budget. That is correct for Linux and the §11.3 L0 figure, but not per platform.
- Linux container not run tonight, as this seat's machine limits require.

### Needs the owner

- X11's V2-relative B-A ceiling is x11V2BAp99Ms = 3.072 x x11RegressionFactor 1.25 = 3.840 ms (test/e2e/v3_x11_test.go:103-110, 595). It is unchanged. What V2's 3.072 ms measured: windows/amd64 on the dev laptop, a quiet machine (plans/V2-report.md §9.1 B-A row at line 370, §4.6 row at line 453, reproduced at lines 631/636 on windows-latest; artifact v2-hotpath-final.json, 2026-08-22/23). V3 recorded 2.048 ms. At that time the B-A sample was recvTS - req.TS + 1 ms only, with the handler excluded (3cd8719a handlers.go:191), and ingest was not durable (B-B p99 0.58-0.77 ms against a 2 ms budget). Two approved changes have since moved what B-A measures. f6a86918 put fsyncs before the ACK, and SP20-D1 re-budgeted B-B to 15/50/40. The SP20-D6 fix c78f610f then added the pre-ACK handler time to the gated sample. Today's B-A therefore contains B-B, about 9-14 ms on this host, and the 3.072 ms figure contains neither. The like-for-like part, hook_controlled_observed, is p99 6.1-11.3 ms in isolated Windows runs against roughly 2 ms at V2, so some real non-ingest growth exists too. Recommendation: do not relax the band; rebase it on a quantity whose definition has not changed. Option A (preferred): gate hook_controlled_observed p99 against a V6 isolated baseline re-recorded per platform. Option B: a paired same-run comparison of the no-ledger and ledger-resident harnesses, which is what the invariant asks. Either needs a new baseline number from the owner.
- HotPathBudgetMsFloor = 15 (internal/config/deadlines.go). This is not a new number: it names the value every platform shipped until D41, the §8.1/§11.3 L0 15 ms. If it is wrong, Linux's B-A default is too tight or too loose. It has no effect on Windows or darwin, where l0IngestMs is higher.
- hotpathStallOverBudget = 10 ms (test/integration/hotpath_test.go). This is a test stimulus, not a budget: §4.6's 25 ms stall minus the 15 ms budget it was written against, so Linux keeps exactly 25 ms. If it is wrong, only the degrade test's margin over the budget changes; any positive value still breaches.
- Optional (raised by review; not implemented): should config.Load WARN when a configured runtime.hotPath.budgetMs is below the effective runtime.budgets.l0IngestMs? Such a configuration makes every durable delivery count as a B-A breach and degrades sessions. Today both keys are independent by design and nothing checks this. Recommendation: a WARN only, never a silent clamp.
- Spool submode never recovers within a session (see open_issues). Is recovery within a session wanted, for example a probe or timed re-sync, or is 'a new session or a daemon restart ends it' acceptable now that D41 removes the systematic Windows trigger?

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



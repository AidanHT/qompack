# Wave 2b workstream report: w2-hookout (C1.18/C1.20)

Branch `closeout/w2-hookout`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `866ea1c8c673f27e916b5fc94b309c3d64585e58`

### Root cause

C1.20: the degrade banner quoted contract values, which can come from the host (the SessionStart source), with %q and no bound. The thrash warning joined one line per newly looping rule, and each line spells out a host tool-name expansion with no count or length bound. Measured at 900,123 and 894,999 UTF-16 units against the 10,000 cap. C1.18: the checkpoint seam still returned the focus instruction as a PreCompact customInstructions, the route recorded it, and a contract row probed the transcript for it, although no host accepts one. Red (a): not ConformOutput. Under load, session-start stops waiting for a cold daemon after about 1.75 s (1.5 s EnsureRunning poll plus the 250 ms connect floor), spools session.start and answers {}; 17/25 reproduced, each with session.start in the spool. This is a product issue in lifetime's code, and the test expectation is correct. Red (b): e2eShutdownIfReachable returned while a detached lazy-spawned daemon was still starting (fresh run/spawn.lock, no lock taken yet); the daemon then took its lock inside the directory t.TempDir was removing. 3/132 rows reproduced, each with spawn.lock present. Fixed in the harness.

### Summary

# w2-hookout report: C1.18, C1.20, and the two unclassified e2e reds

Branch `closeout/w2-hookout` (worktree `../qompack-cx-w2-hookout`), cut from `closeout/integration` at `b070bbe`. There are 9 commits (`d4c5dbf..866ea1c`), all unpushed. Nothing was pushed, tagged or merged, and no real Claude Code session was used. Evidence is under `plans/sdd/V6-closeout/w2-hookout/runs/`.

**Resuming the earlier attempt.** `git status` and `git diff` showed no uncommitted edits. The only file the cut-off attempt left was `runs/allsix-repro-windows-1.log`: `TestE2E_AllSixHooksExitZero/SessionStart` failing alone at b070bbe, with SessionStart taking 1.82 s. I kept it as evidence and committed it in 866ea1c.

**Rewritten local history.** One fixup commit was folded into 98e8557 before hand-off so that no commit has a red test (details under C1.18). The SHAs listed here are the final ones and all of them are reachable from HEAD.

## C1.20: two hook fields now have a size bound (done)

### Root cause
Two fields had no size bound.

- **`degradeBanner`** quoted a failing Result's `Expected` and `Observed` with `%q`. `Observed` can come from the host: `session_start.source_compact` reports the SessionStart payload's `source` word for word.
- **The UserPromptSubmit thrash warning** joined one line per newly looping rule. Each line spells out the rule's whole terminal expansion, and each terminal is a host tool name.

Red tests measured both:
- The banner reached 900,123 UTF-16 units against the host's 10,000 cap (`runs/c1.20-banner-red-windows.log`).
- The warning reached 894,999 units over 500 lines (`runs/c1.20-thrash-red-windows.log`).

### Fix
**Banner (d4c5dbf, b09a739).**
- Each quoted value is cut on a rune boundary to 200 host characters, with a `…` marker (`boundedQuote`).
- The length is measured on the quoted form with `hookio.HostChars`, because quoting is what makes a value long.
- The id is cut to 64 characters (`boundedPrefix`), so the whole banner stays at or under 1,000 characters.
- Short values render exactly as before.

**Thrash warning (e8e4301).**
- At most 5 warnings are shown, each cut to 360 host characters with a marker.
- A counted tail line follows: `[qompack] …and N more possible loops not shown`.
- The block stays under 2,000 characters (checked by a worst-case arithmetic test), and the whole queue still drains on one reply.
- Ordinary warnings are byte-identical to `grammar.FormatWarning`.

### Tests
Pathological inputs used: 200K-character ASCII, runes that `%q` escapes to 10 characters, astral runes, invalid UTF-8, runs of quotes and backslashes, 500 rules × 40 long tool names, and one rule with 50,000 symbols.
- The route test sends a 300K-rune `source` through the real `session.start` route and requires `HostCapOverruns(ConformOutput(...))` to be empty.
- Each field is also pinned with `HostChars <= its ceiling`, and each ceiling with `ceiling ≤ cap/10` or `ceiling ≤ cap/5`.

## C1.18: PreCompact instruction producer retired, checkpoint still written (done)

### Root cause
No host accepts a PreCompact instruction: 2.1.280 rejected the whole response over it (C1.12). The daemon still:
- rendered the instruction and returned it as `customInstructions` from the checkpoint seam (`wire_checkpoint.go`, `hookio.PreCompactOutput`);
- recorded it (`handleCheckpoint` → `SetPrecompactInstr`);
- had a contract row probe the transcript for it, which warned FAILING.

That probe could also give a false positive, because 2.1.280 replayed the rejected output, instruction included, into the transcript.

### Fix
**Daemon and hookio (98e8557).**
- The seam seals the checkpoint and answers `hookio.Empty()`.
- `handleCheckpoint` discards whatever Output the bound seam returns and records no instruction, so no future seam can bring the producer back. It still calls the seam (that is what seals), still writes the marker, and still records the wall-time sample.
- `hookio.PreCompactOutput` is removed.
- `HSO.CustomInstructions` is kept as a decode-only field, because a daemon from an older build can still be running after an upgrade. `ConformOutput` still strips it.

**Contract row (9104209).**
- `precompact.custom_instructions_accepted` now reports OK, SevInfo, `Observed:"retired"`, with a `Detail` giving the reason. It never probes and never warns, and it is still attributed to the unsupported `compaction_request` capability, so `ClassifyResult` gives `unsupported`.
- `retired` is added to `noObservationSpellings`. The old spellings stay so that an older build's observation ledger still classifies the same way.
- The ID and `History.PrecompactInstr` stay because both are persisted data. The unused probe helpers are deleted.
- `DeclareProducers` is unchanged, so the architecture's five-always and four-gated producer split still holds.

**Config (04a43a1).**
- The `checkpoint.incrementalSpanInstruction` doc tag and its retired-meaning note no longer say it "emits" anything.
- `testdata/golden/config/schema.json`: one description was edited by hand, because `-update` refuses a Windows host's platform defaults.
- `docs/config-reference.md` was regenerated with `gen-config-docs`.
- Only the comment of the O1 default guard changed.

**Docs (3856d6b).**
- architecture §7, cannot-do, troubleshooting (the degraded-mode and passive-step sections that still said `customInstructions`, plus the §1 no-observation list now includes `retired`), uat and upstream-issues.
- The `mode.go` and `monitor.go` comments were updated to match.

### What remains
`internal/checkpoint` (outside this lane's scope) still composes the focus text inside `FileWriter.PreCompact`. It now serves only the `state/precompact.json` debug record (`instructions_bytes`, `span_instruction`) and reaches no hop. This is listed under open issues.

## Unclassified red (a): `TestE2E_AllSixHooksExitZero/SessionStart` "hookSpecificOutput nil" — the test is right; the product gives up too early

### Root cause
This is not ConformOutput. A SessionStart handled while the daemon may act always carries the sentinel `additionalContext`, and ConformOutput keeps it.

The empty reply comes from a cold daemon that is not reachable in time:
1. The PostToolUse `lazySpawn` has already launched a daemon.
2. SessionStart's `EnsureRunning` probes with a 20 ms dial, spawns a second daemon, and polls for only `ensureRunningPollBound` (1.5 s).
3. The client then gets its 250 ms connect floor, spools the `session.start`, and answers `{}`.

All of this happens inside session-start's own 10 s reply deadline and 15 s manifest timeout.

### Evidence
`runs/diag-sessionstart-coload2-windows.log`, run with CPU at 100%:
- 17 of 25 SessionStarts returned no hookSpecificOutput.
- Each failing one took 1.57–1.92 s (1.5 s + 0.25 s + process overhead). In each, `session.start` was found in the spool, and the daemon took its lock 104–841 ms after SessionStart began, or had not taken it yet.
- The passing ones had an empty spool.

For comparison:
- At lighter load, 0 of 12 failed (`runs/diag-sessionstart-coload-daemonpkg-windows.log`).
- The original failures took 1.57, 1.82 and 1.83 s. The same message failed on cf31e01, before C1.12 (packaging report).
- The e2e harness's own `e2eDaemonUpBound` already allows 12 s for a lazily spawned daemon on a loaded host.

The same family explains the co-loaded x14 red in my full e2e run (a SessionStart through the rig, 10.65 s). x14 passes alone.

### Disposition
Not fixed here. The code is `internal/daemon/spawn.go` `EnsureRunning` and the session-start connect path, which the lifetime lane owns (C1.16). The test is unchanged. It will stay load-sensitive until the product waits for a starting daemon within its own reply budget.

### New defect found while tracing this (routed)
A `session.start` that was spooled and replayed later mints a §12.1 probe that was never delivered. The project is then degraded to passive recording two prompts later.
- `drainDispatch` routes a stale `session.start` through `dispatchOp` → `handleSessionStart`, which mints a sentinel token nobody receives.
- The next two UserPromptSubmit scans cannot find it, so `hook.additional_context_delivered` fails critically.
- Deterministic repro (`runs/diag-spooled-sessionstart-probe-windows.log`, diagnostic source in `runs/zz_diag_hookout_test.go.txt`): a daemon-down session-start (spooled), then a daemon whose startup drain replays it, then two prompts, then the next session-start → `mode=degraded-passive`, with banner "hook.additional_context_delivered … sentinel not found after two chances".
- On a loaded machine, red (a) leads straight into this: Qompack blames the host for a reply it lost itself.

## Unclassified red (b): `TestHooksExitZeroUnderFaults` "TempDir RemoveAll cleanup: … not empty" — harness defect, fixed

### Root cause
A hook's `lazySpawn` starts a detached daemon and exits. On a loaded host that process can take seconds to reach its first statement. `e2eShutdownIfReachable` found nothing reachable and no `daemon.lock` after its 1.5 s settle window, and returned. The daemon then took its lock, which recreates `.qompack/run` inside a tree that RemoveAll is deleting.

### Evidence
`runs/diag-faultrows-coload-windows.log`, with the old helper: 3 of 132 rows had a daemon take the lock 0.6–4.2 s after the helper returned. In every case a fresh `run/spawn.lock` was present at the moment of return.

### Fix (aec178a)
`run/spawn.lock` is the product's own marker that a spawn is in flight:
- `lazySpawn` writes it, with a timestamp, before launching;
- the daemon removes it once it is listening;
- clients treat it as valid for 10 s.

While a fresh marker exists, the helper now waits (bounded by `e2eDaemonUpBound`) for the daemon to take the lock, then shuts it down as before. The regression test stages the marker plus a real daemon started after the settle window:
- before the fix it failed (the helper returned 2.55 s before the daemon exited);
- after the fix it passed 3 of 3.

**Cost.** Daemon-down rows now wait for the no-op spawn's marker to go stale, about 10 s each (+~50 s on the matrix: 150 s in total when run alone). Details under open issues.

## Commands and results

Windows runs, co-loaded unless marked "alone". Wall-clock reds were re-run alone before being classified.

| Command | Result |
|---|---|
| `go test ./internal/daemon -run '^TestSessionStart_DegradeBannerFromAHostSuppliedSourceIsBounded$' -count=1 -v` (red, pre-fix) | FAIL: 900,123 units (runs/c1.20-banner-red-windows.log) |
| `go test ./internal/daemon -run 'TestSessionStart_DegradeBanner\|TestDegradeBanner_\|TestBoundedQuote_' -count=1 -v` | PASS (runs/c1.20-banner-green-windows.log) |
| `go test ./internal/observer -run '^TestPromptReply_ThrashWarningStaysUnderTheHostCap$' -count=1 -v` (temporary red test, later renamed) <!-- runpatterns: temporary red-only test; the committed test is TestPromptReply_ThrashWarningStaysFarUnderTheHostCap --> | FAIL: 894,999 units, 500 lines |
| `go test ./internal/observer -run 'TestPromptReply_ThrashWarning\|TestBoundThrashWarning_' -count=1 -v` | PASS, 3 tests, 6 subtests |
| `go test ./internal/daemon -run '^TestCheckpointRoute_SealsWithoutRenderingOrRecordingAnInstruction$' -count=1 -v` (red) | FAIL: reply carried a 787-char customInstructions |
| `go test ./internal/daemon -run 'TestCheckpointRoute_' -count=1 -v` (old daemon and hookio restored from stash) | both FAIL (runs/c1.18-route-red2-windows.log) |
| `go test ./internal/contract -run '^TestCustomInstructions_RetiredRowNeverProbesAndNeverWarns$' -count=1 -v` (red) | FAIL 5/5 (runs/c1.18-contract-red-windows.log) |
| `go test ./internal/daemon -run 'TestCheckpointRoute_\|TestBindCheckpoint\|TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve\|TestDegradedPassiveSuppressesActingPaths\|TestMarkerIsWrittenByFlushAndCheckpointOnly\|TestDeclaredProducerSetMatchesArchitecture\|TestSessionStartRecordsCapabilityObservations' -count=1 -v` | PASS |
| `go test ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact\|TestE2E_CheckpointDegradedPassiveSealsNothing\|TestV1_HookLifecycleThroughRealBinary\|TestV4_PreCompactToCheckpointToRehydrateRoundTrip\|TestV4_O1SpanInstructionFromARealCheckpointFrontier\|TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint\|TestV4_DegradedPassiveWithEverySubsystem\|TestV5_PreCompactToRehydrateToDroppedRoundTrip\|TestV5_EliminationThroughEveryFourSurfaces\|TestV5_ThrashWarningVisibleInStatusAndCheckpoint\|TestV5_EveryContractAssertionHasARealProducer\|TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent)$' -count=1 -v -timeout=30m` | 12/12 PASS, 174 s (runs/c1.18-e2e-rows-windows.log) |
| `go test ./internal/cli -run 'TestHookOutput_\|TestHostHookSchema_\|TestDaemonFirstPreCompactSealsCheckpoint' -count=1 -v` | PASS |
| `go test ./test/integration -run '^(TestIntegration_ContractMonitorRunsAgainstRealStore\|TestIntegration_DegradedPassiveStillWritesToTheRealStore)$' -count=1 -v` | PASS |
| `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight$' -count=1 -v -timeout=5m` (red, old helper) | FAIL: returned 2.55 s early (runs/faultrows-helper-red-windows.log); after the fix, -count=3 PASS |
| `go test ./test/e2e -run '^(TestHooksExitZeroUnderFaults\|TestE2E_AllSixHooksExitZero\|TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight)$' -count=1 -v -timeout=30m` | PASS, 150 s (runs/faultrows-and-allsix-after-windows.log) |
| `go test ./test/e2e -run '^TestZZDiag_SessionStartUnderLoad$' -count=1 -v -timeout=30m` with ZZ_ITERS=25 <!-- runpatterns: diagnostic kept as runs/zz_diag_hookout_test.go.txt, not in the tree --> | 17/25 nil HSO, all with session.start spooled |
| `go test ./test/e2e -run '^TestZZDiag_FaultRowCleanup$' -count=1 -v -timeout=60m` with ZZ_ROUNDS=2 <!-- runpatterns: diagnostic kept as runs/zz_diag_hookout_test.go.txt, not in the tree --> | 3/132 late daemons, each with spawn.lock present |
| `go test ./test/e2e -run '^TestZZDiag_SpooledSessionStartMintsAnUndeliveredProbe$' -count=1 -v -timeout=10m` <!-- runpatterns: diagnostic kept as runs/zz_diag_hookout_test.go.txt, not in the tree --> | mode=degraded-passive (the routed defect) |
| `go test ./internal/observer -count=1` | ok, 283.9 s |
| `go test ./internal/daemon -count=1 -timeout=30m` | 1st run: 1 FAIL, TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve (C1.18 fallout, fixed and folded into 98e8557); re-run at aec178a alone: ok, 289.8 s |
| `go test ./internal/cli -count=1 -timeout=30m`; same for ./internal/contract, ./internal/hookio, ./internal/config, ./test/docs | ok |
| `go test ./test/integration -count=1 -timeout=30m` | 4 FAIL + 30 m timeout, all "daemon never became reachable within 12s" under heavy co-load; the four re-run alone PASS (runs/integration-reds-rerun-alone-windows.log); HotPathWarmWithRealResidentState is the known co-load row |
| `go test ./test/guards -count=1 -timeout=30m` | only TestCarriedDefects_WaveReportRequiresResolution (6 subtests), pre-existing |
| `go test ./test/e2e -count=1 -v -timeout=75m` | FAIL 4: V3_HotPathUnchangedWithLedgerResident (known co-load row); V4_TombstoneToRecallToExpandRoundTrip (identical on a b070bbe export); V5_TombstoneToExpandRoundTrip (fails 4/4 on the b070bbe export too); V5_EveryContractAssertionHasARealProducer (co-load SessionStart family, PASS alone) |

Linux, non-root, -race (`linux-nonroot-gate.sh --prefix cx-w2-hookout` at aec178a):
- **Touched packages** (daemon, observer, contract, hookio, config, cli, integration, guards): all PASS except the known HotPathWarm and CarriedDefects rows.
- **15 e2e rows**, including the fault matrix and AllSix: 107/107 PASS.

Other checks:
- `devtool fmt-check`: clean. `go vet` on touched packages: clean. Pinned golangci-lint on touched packages: exit 0. `nomagic ./internal/...`: exit 0. gen-config, command and mcp docs `--check`: up to date.
- `devtool lint --only=nomagic,importgraph,testdeps,sleepcheck,docmarkers,runpatterns`: everything passes except runpatterns. Its failures are pre-existing, in the wave-1 reports (ingest, linux, packaging, rehydrate-cap).

### Commits

- d4c5dbf43da9eb5dc72075d536acfe1d8b9f04b7 fix(daemon): bound the SessionStart degrade banner under the host cap
- e8e430144a79cde8967b98e0032c0adb0ea71c5f fix(observer): bound the thrash warning under the host cap
- b09a739bae03bab6b8792aaf1fb8e17fac9476f7 refactor(daemon): read the banner value bound inside boundedQuote
- 98e85576ccac3f44e3c345423308f03cf7897032 fix(daemon): retire the PreCompact focus instruction producer
- 9104209baa5ffa1c0988de45f0dfeb174d5d8a72 fix(contract): report the custom-instructions row as retired
- 04a43a161f73aafff761b43cd3e8106500901119 fix(config): stop describing incrementalSpanInstruction as emitting
- 3856d6b21eed04805085e5e34bbf5641f553bc27 docs: record the retired PreCompact instruction and hook bounds
- aec178aa1ade7f89b2520bdee21eac6b598c72a1 fix(e2e): wait for an in-flight lazy spawn before shutdown
- 866ea1c8c673f27e916b5fc94b309c3d64585e58 docs(sdd): add w2-hookout run evidence

### Tests

- `go test ./internal/daemon -run '^TestSessionStart_DegradeBannerFromAHostSuppliedSourceIsBounded$' -count=1 -v (red, before d4c5dbf)` — FAIL: SessionStart systemMessage 900,123 UTF-16 units vs host cap 10,000 (runs/c1.20-banner-red-windows.log); PASS after the fix
- `go test ./internal/observer -count=1 -v, run filter TestPromptReply_ThrashWarning and TestBoundThrashWarning_ (red with a temporary test before e8e4301)` — red: 894,999 units, 500 lines; green: 3 tests, 6 subtests PASS; full package ok, 283.9 s
- `go test ./internal/daemon -run 'TestCheckpointRoute_' -count=1 -v` — both route tests FAIL on the old daemon and hookio code (runs/c1.18-route-red2-windows.log), PASS after 98e8557
- `go test ./internal/contract -run '^TestCustomInstructions_RetiredRowNeverProbesAndNeverWarns$' -count=1 -v` — FAIL 5/5 before 9104209 (runs/c1.18-contract-red-windows.log), PASS after; full ./internal/contract ok
- `go test ./test/e2e -count=1 -v -timeout=30m, run filter: the 12 C1.18 rows listed in the summary table` — 12/12 PASS, 174 s (runs/c1.18-e2e-rows-windows.log)
- `go test ./test/e2e -run '^TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight$' -count=1 -v -timeout=5m` — FAIL before aec178a (helper returned 2.55 s before the late daemon exited); PASS -count=3 after
- `go test ./test/e2e -count=1 -v -timeout=30m, run filter TestHooksExitZeroUnderFaults, TestE2E_AllSixHooksExitZero and the helper test` — PASS, 150 s; daemon-down rows ~10 s each (runs/faultrows-and-allsix-after-windows.log)
- `go test ./test/e2e (diagnostic TestZZDiag_SessionStartUnderLoad from runs/zz_diag_hookout_test.go.txt, ZZ_ITERS=25, heavy co-load)` — 17/25 SessionStart replies without hookSpecificOutput, each 1.57-1.92 s with session.start in the spool; 0/12 at lighter load
- `go test ./test/e2e (diagnostic TestZZDiag_FaultRowCleanup, ZZ_ROUNDS=2, old helper)` — 3/132 rows had a daemon take the lock 0.6-4.2 s after e2eShutdownIfReachable returned; spawn.lock present in all 3
- `go test ./test/e2e (diagnostic TestZZDiag_SpooledSessionStartMintsAnUndeliveredProbe)` — the drained spooled session.start minted an undelivered probe; two prompts later the project was degraded-passive (routed defect)
- `go test ./internal/daemon -count=1 -timeout=30m` — first run (co-loaded): 1 FAIL, TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve (C1.18 fallout, fixed and folded into 98e8557); re-run at aec178a: ok, 289.8 s
- `go test ./internal/cli ./internal/hookio ./internal/config ./test/docs -count=1` — ok
- `go test ./test/integration -count=1 -timeout=30m` — co-loaded: 4 FAIL plus a timeout, all 'daemon never became reachable within 12s'; the four PASS alone (runs/integration-reds-rerun-alone-windows.log); HotPathWarmWithRealResidentState is the known co-load row
- `go test ./test/guards -count=1 -timeout=30m` — only TestCarriedDefects_WaveReportRequiresResolution (6 carried defects) fails, pre-existing
- `go test ./test/e2e -count=1 -v -timeout=75m` — FAIL 4, all classified: V3_HotPathUnchangedWithLedgerResident (known co-load); V4 tombstone (same on a b070bbe export); V5 tombstone (fails 4/4 on the b070bbe export); V5_EveryContractAssertionHasARealProducer (co-load SessionStart family at a line before this lane's edits; PASS alone)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-hookout aec178a touched-race -- (8 touched packages)` — non-root -race: daemon, observer, contract, hookio, config, cli PASS; integration only the known HotPathWarm row; guards only CarriedDefects
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-hookout aec178a e2e-rows (15 e2e rows incl. the fault matrix) -- ./test/e2e` — 107/107 PASS, -race, non-root
- `go run ./tools/devtool fmt-check; go vet (touched packages); pinned golangci-lint on touched packages; go run ./tools/lint/nomagic ./internal/...; devtool gen-config-docs / gen-command-docs / gen-mcp-docs --check` — all clean / up to date
- `go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,sleepcheck,docmarkers,runpatterns` — all pass except runpatterns, whose failures are pre-existing in the wave-1 reports (ingest, linux, packaging, rehydrate-cap)

### Criterion changes

- internal/daemon TestBindCheckpointSealsOnTheFirstPreCompact and TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve: used a non-nil hookSpecificOutput (the retired instruction) as the seal signal. They now require the empty reply and prove the seal from the artifact; the first also parses checkpoint 0001 and checks its Session and Seq, which is stronger. TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened now also asserts that no 0001.json exists.
- internal/contract: TestCustomInstructionsProbePhrase and its three variants, plus TestFirstLine and TestProbePhrase, pinned the transcript probe of a mechanism no host has. The probe and its helpers are deleted. TestCustomInstructions_RetiredRowNeverProbesAndNeverWarns replaces them; it was red before the change and includes the stale-instruction case the old probe warned on and the replayed-rejection false positive. observation_test's emitted spelling for the row changes from no-instructions-emitted to retired.
- internal/hookio: TestPreCompactOutput_Shape and _EmptyInstructionOmitsField are removed with the retired constructor, replaced by TestOlderDaemonPreCompactReply_DecodesAndNeverReachesTheHost, which pins the upgrade path. TestConformOutput_PreCompactOutputNeverReachesTheHost, internal/cli TestHookOutput_PreCompactCarriesNoHookSpecificOutput and the test/integration degraded stub now build the older daemon's reply as a literal. Their assertions are unchanged.
- test/e2e content rows (v4 x01/x03/x08/x12, v5 x04/x10, IT-1, TestE2E_CheckpointHookWritesImmutableArtifact) read and pinned the instruction's content from the IPC reply. They now require the empty IPC reply (cpRequireNoInstructionReply) and keep their seal and artifact proof. x12's full-mode positive control is now 'seals a checkpoint', the act the degraded arm must not perform. x03 keeps the frontier half, and both of its arms check the reply. IT-1's secret-leak check over the instruction is replaced by checking that nothing is recorded and the IPC reply is empty, because the instruction no longer exists on any surface; the logs-corpus leak check is unchanged.
- test/e2e v5 x14: proved the full-mode PreCompact by the instruction recorded in contract history. It now proves it by the sealed artifact plus an empty PrecompactInstr, and additionally pins Observed == 'retired'. v5 x05: required a PreCompact hookSpecificOutput on the binary's stdout, which had failed since C1.12 (rehydrate-cap report). It now holds stdout to the host's PreCompact contract, and the seal is proven by the artifact assertions that follow.
- testdata/golden/config/schema.json and docs/config-reference.md: one description changed for checkpoint.incrementalSpanInstruction (an intended doc-tag change, not a regenerated broken output). test/guards TestGuard_O1FlagDefaults: comment only, assertions unchanged.
- test/e2e e2eShutdownIfReachable: waits longer, never less. While a fresh spawn.lock exists it waits for the daemon to take its lock and then shuts it down (bounded by e2eDaemonUpBound). Daemon-down rows cost about 10 s more each.

### Open issues

- ROUTED (lifetime lane / C1.16): SessionStart's cold-start path stops waiting for a starting daemon after about 1.75 s (EnsureRunning's 1.5 s poll with 20 ms dials, plus the client's 250 ms connect floor), well inside its own 10 s reply deadline. Under load it spools session.start and answers {} (no sentinel; for source=compact, no rehydration). It also launches a second daemon beside the PostToolUse lazy spawn. Evidence: runs/diag-sessionstart-coload2-windows.log. Code: internal/daemon/spawn.go EnsureRunning and internal/cli/sessionstart.go. TestE2E_AllSixHooksExitZero/SessionStart, IT-1 call 1 and x14 stay load-sensitive until this is fixed. Their expectation is correct and they were not changed.
- ROUTED NEW DEFECT (owner: daemon session.start / drain; lifetime or sessionend lane): a spooled session.start replayed by drainDispatch through handleSessionStart mints a §12.1 probe the host never received. Two prompts later hook.additional_context_delivered fails critically and the project degrades to passive recording, blaming the host for a reply Qompack itself lost. Deterministic repro: runs/diag-spooled-sessionstart-probe-windows.log (source: runs/zz_diag_hookout_test.go.txt, TestZZDiag_SpooledSessionStartMintsAnUndeliveredProbe). Fix direction: do not mint (or do not count) a probe for a replayed session.start whose reply nobody reads.
- C1.18 remnant outside this lane's scope: internal/checkpoint FileWriter.PreCompact still calls FocusInstructions to fill PreCompactResult.Instructions, which the daemon now discards. The text's length and span flag still go into the state/precompact.json debug record, which e2e x03 and the SP-10 artifact row read. Retiring that composition belongs to the checkpoint owner; the debug-record fields and the checkpoint package tests would change with it.
- The e2e helper respells internal/ipc's unexported spawnLockName and spawnLockStaleAfter (e2eSpawnLockName, e2eSpawnLockStaleAfter), as internal/daemon already respells spawnLockName. Exported aliases from internal/ipc would be better; ipc belongs to the lifetime lane.
- Under the daemon-down fault, lazySpawn still writes spawn.lock before its no-op spawn, so e2eShutdownIfReachable now waits about 10 s for that marker to go stale in each daemon-down row (+~50 s on the fault matrix). A product-side option: skip the spawn lock when the spawner is the fault no-op.
- Pre-existing, not from this lane: TestV5_TombstoneToExpandRoundTrip ('an unpinned re_read is the newest capture') fails 4/4 on a b070bbe export (runs/e2e-v5x02-count4-base-b070bbe-windows.log; one earlier base run passed, so it looks timing-dependent). TestV4_TombstoneToRecallToExpandRoundTrip fails identically on base (append-only violation). Neither has an owner in the wave-2 dispatch I could find.
- Pre-existing lint: devtool lint runpatterns flags -run patterns in the committed wave-1 reports (ingest, linux, packaging, rehydrate-cap). The report tooling truncates alternation at a bare |. This report keeps alternation only inside a markdown table with escaped pipes, and uses waivers for the diagnostic tests that are not in the tree.

### Needs the owner

- New size ceilings (not performance budgets), chosen under the brief's 'fixed small budget'. SessionStart degrade banner: 200 host characters per quoted value, 64 for the id, whole banner under 1,000. UserPromptSubmit thrash warning: at most 5 warnings of 360 characters plus a counted tail, under 2,000. Confirm or overrule.
- Decide whether internal/checkpoint should stop composing the retired focus text as well. It now only feeds the byte count and span flag in the state/precompact.json debug record. Recommended, via the checkpoint owner.
- The SessionStart cold-start wait policy (routed to lifetime / C1.16): how long session-start may wait for a daemon it just spawned before answering {}. Today it is about 1.75 s against a 10 s reply budget.

## Independent review

### review:hookout: needs-fixes

- **minor** `internal/checkpoint/focus.go:31-45, 80-92 (FocusInstructions / SentinelPhrase doc comments)` — Code comments that C1.18 made stale were left in place. They say the focus text "is emitted through PreCompact's custom_instructions channel" and that "PARAGRAPH 1'S POSITION IS LOAD-BEARING. contract.CPreCompactCustomInstr's probe is probePhrase(History.PrecompactInstr)". 9104209 deleted probePhrase, and the row no longer probes anything. The report lists the checkpoint package as a remnant ("still composes the focus text") but does not mention these comments, which now point at a symbol that no longer exists and describe a mechanism that is gone.
  - Evidence: `grep -n CPreCompactCustomInstr internal/checkpoint/focus.go` finds lines 34, 43 and 86. All three still describe the transcript probe. `git show 9104209 -- internal/contract/assertions.go` removes probePhrase/firstLine.
  - Fix: Either (a) extend the routed open issue to the checkpoint owner to cover rewriting these comments, or (b) make a comment-only edit (no behaviour change, so the scope cost is small): say the text now feeds only the state/precompact.json debug record (instructions_bytes, span_instruction), and that no contract assertion reads it.
- **nit** `internal/contract/standard.go:27` — The retired row is still declared with SevWarn (`gated(CPreCompactCustomInstr, SevWarn, ...)`), but checkPreCompactCustomInstr now always returns OK/SevInfo and the new doc comments say it is "never a warning". The declared severity no longer matches what the row can do. gated only applies the declared severity to !OK results, so there is no behavioural effect, but the table is the place a reader goes to learn a row's severity.
  - Evidence: standard.go:27 `gated(CPreCompactCustomInstr, SevWarn, "custom_instructions accepted", checkPreCompactCustomInstr)`; assertions.go:218-236 says it is SevInfo because "it is never a warning".
  - Fix: Declare it SevInfo (or add a comment that the declared SevWarn is inert because the retired check never fails). Also update standard.go's doc comment, which still lists it among the later-wave rows awaiting a producer.
- **nit** `internal/observer/prompt.go:331-347 (pendingThrashAt) with boundThrashWarning at :73` — The host-visible field is now bounded, but pendingThrashAt still runs grammar.FormatWarning over every queued rule, including rules that will only be counted in the tail, and over each rule's whole expansion before cutHostChars truncates it. CPU and memory for one reply therefore still grow with the number of rules and their expansion size, not with the ~2,000-character output. The lane's own pathological case (one rule with 50,000 symbols) builds a multi-MB string only to keep 360 characters of it.
  - Evidence: thrash_bound_test.go case "one enormous rule": thrashRulesOf(1, 50_000, longMCP). pendingThrashAt appends FormatWarning(...) for every entry of st.PendingThrash before boundThrashWarning slices to 5.
  - Fix: Optional: format only the first thrashWarningMaxLines rules and count the rest, or leave it and record that the grammar's own rule and expansion limits bound this. The output bound, which was the C1.20 requirement, is correct as it stands.
- **nit** `commits b09a739 (refactor(daemon): read the banner value bound inside boundedQuote) and 866ea1c (docs(sdd): add w2-hookout run evidence)` — Two of the nine commits have no Refs footer. The other seven carry `Refs: V6-VERIFY, C1.18/C1.20`. No commit has an attribution trailer, and all subjects are conventional.
  - Evidence: `git log -1 --format=%B b09a739` and `866ea1c` show no `Refs:` line.
  - Fix: If the lane convention requires Refs on every commit, reword both commits before integration (for example with a non-interactive `git rebase --exec` or `git commit --amend` on a fresh branch) to add `Refs: V6-VERIFY, C1.18/C1.20`. Otherwise accept as-is.

## Fix seat (review resolution) — status `done`, head `7d7e3f4`

### Root cause

C1.18: BindCheckpoint's seam returned the rendered focus instruction as PreCompact customInstructions, and handleCheckpoint recorded it into contract History. Only the client's ConformOutput kept it from a host that rejects it (C1.12). C1.20: degradeBanner quoted host-influenced Expected/Observed values with %q and no bound (900,123 UTF-16 units in the red test), and the thrash warning joined every pending line with no bound on count or length (894,999 units). AllSix/SessionStart: under load, the fresh daemon is not listening within EnsureRunning's 1.5 s poll bound, so the client spools session.start and fails open with {} (17 of 17 failures had session.start spooled). This is C1.16's class, not ConformOutput, and the test's expectation is correct. Fault-row cleanup: e2eShutdownIfReachable returned while a lazily spawned daemon was still in flight (fresh run/spawn.lock), and that daemon then recreated .qompack/run during RemoveAll. Review finding: comments in internal/checkpoint still described the retired delivery and named the deleted contract.probePhrase.

### Summary

W2-HOOKOUT: C1.18, C1.20 and two e2e reds. Final report from the fix seat.
Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-hookout, branch closeout/w2-hookout, base b070bbe, head 7d7e3f4. Nothing was pushed, merged or tagged. No commit has an attribution trailer.

RESUMING: the working tree was clean when I started. The prior attempt's only artifact, runs/allsix-repro-windows-1.log, was already committed by the implementer in 866ea1c as evidence. It was a real repro, so the implementer adopted it. I found no uncommitted edits to adopt or discard.

## Review resolution
- Finding (minor): stale comments in internal/checkpoint/focus.go (lines 31-45 and 80-92) still said the focus text "is emitted through PreCompact's custom_instructions channel". They also pointed at contract.probePhrase and a probe that CPreCompactCustomInstr no longer runs.
  - Verified: CONFIRMED. `grep` found probePhrase at focus.go:72 and :87, and a live-tense CPreCompactCustomInstr probe at :34, :43 and :86. More stale text sat in other files: focus_test.go:51, :106 and :161-165; precompact_test.go:86 and :93-97; precompact.go:53 ("PreCompactResult is what the daemon turns into a hookio.Output") and :271-273; writer.go:214 and :688-694; imports_test.go:22-25. 9104209 deleted probePhrase. 98e8557 made BindCheckpoint discard PreCompactResult (`_, err = w.PreCompact(...)`; `return hookio.Empty(), err`).
  - Action: took the reviewer's option (b) and fixed it in commit 7225a5b, which changes comments and assertion-message text only. The comments now say:
    - FocusInstructions still renders the text and returns it as PreCompactResult.Instructions, but the seam discards it and the text reaches no hop.
    - The text survives only as instructions_bytes and span_instruction in state/precompact.json.
    - No contract assertion reads it, and precompact.custom_instructions_accepted reports "retired".
    - Paragraph 1 is still the whole first line. That shape was once needed by the probe; it is still pinned because it keeps §8.5's paragraph order.
  - Unchanged: no assertion, threshold, test name or behaviour. TestFocusFirstLineIsAProbePhrase keeps its name because plans/sdd/V5-VERIFY/inventory-SP-10.md cites it; its comment explains why.
  - Check: after the edit, grep finds probePhrase and CPreCompactCustomInstr in internal/checkpoint only in past-tense "Until C1.18" sentences.
  - Tests: the full internal/checkpoint package passes (ok 74.4s), the focused TestFocus|TestPreCompact|TestNoForbiddenImports rows pass (23 top-level), and fmt-check, go vet and the pinned golangci-lint on the package all exit 0.
- No other findings were raised: 0 blockers, 0 majors.

## (1) C1.18: the PreCompact instruction producer is retired
Root cause: BindCheckpoint's seam returned hookio.PreCompactOutput(res.Instructions). handleCheckpoint then recorded that text into contract History (SetPrecompactInstr). Only the hook client's ConformOutput kept it from reaching the host. No host accepts it: Claude Code has no PreCompact hookSpecificOutput variant, 2.1.280 rejected the whole response over one (C1.12), and custom_instructions is PreCompact input (Qompack.md §7.3, §8.5).

What changed:
- 98e8557 (daemon and hookio):
  - The seam still seals the checkpoint, then answers hookio.Empty() on success and on failure.
  - handleCheckpoint discards any Output the seam returns, records no instruction, and always replies Empty.
  - hookio.PreCompactOutput is removed.
  - HSO.CustomInstructions stays decodable, because an older daemon can still be resident after an upgrade. ConformOutput still strips it.
  - New red-first route tests: TestCheckpointRoute_SealsWithoutRenderingOrRecordingAnInstruction and TestCheckpointRoute_AnswersEmptyWhateverTheSeamReturns (runs/c1.18-route-red*.log).
- 9104209 (contract row): CPreCompactCustomInstr now returns OK, SevInfo, Observed "retired", with a Detail explaining why.
  - It probes nothing and can never warn, and it is still attributed to the unsupported compaction_request capability.
  - "retired" is added to the no-observation spellings.
  - The ID and History.PrecompactInstr stay, because both are persisted data.
  - The probe helpers are removed.
  - Red first: runs/c1.18-contract-red-windows.log.
- 04a43a1 (config): the doc tag for checkpoint.incrementalSpanInstruction and its retired-meaning note now say C1.18 retired the instruction.
  - docs/config-reference.md was regenerated with gen-config-docs.
  - testdata/golden/config/schema.json changed by exactly that one description, edited by hand because `-update` refuses a Windows host's platform defaults.
- 3856d6b (docs): updated architecture §7, cannot-do, troubleshooting (the degraded-mode section and the passive-mode step), uat and upstream-issues. Remaining "custom_instructions" mentions in docs/ are about the retired row or the host boundary.
- 7225a5b (fix seat): the checkpoint comments, as described under Review resolution.

## (2) C1.20: the degrade banner and thrash warning are bounded
Root causes:
- degradeBanner (handlers.go) quoted a contract Result's Expected and Observed with %q and no limit. Observed can be host-supplied text: session_start.source_compact echoes the payload's `source`. The red run measured the systemMessage at 900,123 UTF-16 units.
- promptReplyOutput joined every pending thrash line with no bound on count or length. The red run measured additionalContext at 894,999 units.

What changed:
- d4c5dbf and b09a739 (banner):
  - Each quoted value is cut to 200 host characters with boundedQuote. It measures the quoted form, cuts on a rune boundary and ends a cut value with "…".
  - The id is cut to 64 with boundedPrefix.
  - The whole banner stays at or under 1,000. Short values render exactly as before.
  - Tests: TestDegradeBanner_StaysFarUnderTheHostCap, TestDegradeBanner_ShortValuesAreQuotedWhole, TestBoundedQuote_CutsOnARuneBoundaryAndSaysSo, and the end-to-end route test TestSessionStart_DegradeBannerFromAHostSuppliedSourceIsBounded. The route test was red at 900,123 and asserts HostChars ≤ cap on every field.
- e8e4301 (thrash warning):
  - boundThrashWarning shows at most 5 lines, each cut to 360 host characters on a rune boundary, plus a counted tail ("…and N more possible loops not shown"). The block stays at or under 2,000.
  - Ordinary warnings render byte for byte, and the whole queue still drains on one reply.
  - Tests: TestPromptReply_ThrashWarningStaysFarUnderTheHostCap (it was red at 894,999 under its earlier name, TestPromptReply_ThrashWarningStaysUnderTheHostCap), TestBoundThrashWarning_OrdinaryWarningsRenderWhole and TestBoundThrashWarning_WorstCaseFitsItsCeiling.

## (3a) TestE2E_AllSixHooksExitZero/SessionStart: the test is right, the product is too slow under load
- The red comes from the product's fail-open path, not from ConformOutput. session-start's EnsureRunning polls for 1.5 s (internal/daemon/spawn.go ensureRunningPollBound). On a loaded host the freshly spawned daemon is not listening by then, so the client spools session.start and answers {}.
- Diagnostic source: runs/zz_diag_hookout_test.go.txt.
  - Under co-load, 17 of 25 SessionStart replies had no hookSpecificOutput. All 17 had session.start in the spool, and all 8 good replies did not (runs/diag-sessionstart-coload2-windows.log).
  - On a lighter host, 0 of 12 failed (runs/diag-sessionstart-coload-daemonpkg-windows.log).
- The test's expectation states the product's intent and was not changed. This is the C1.16 class of problem: SessionStart must answer fast and reliably.
- The test passed alone at head: 2.78 s, runs/fix-e2e-rows-head-windows.log.

## (3b) TestHooksExitZeroUnderFaults "directory is not empty": fixed in the e2e helper (aec178a)
- Root cause: a hook's lazySpawn launches a detached daemon and exits. Under load, e2eShutdownIfReachable's 1.5 s settle window can find neither a reachable daemon nor daemon.lock, so it returns. The daemon then takes its lock and recreates .qompack/run inside a tree that RemoveAll is deleting.
- Evidence: 3 of 132 fault rows had a daemon take the lock 0.6 to 4.2 s after the helper returned, each with a fresh run/spawn.lock (runs/diag-faultrows-coload-windows.log).
- Fix: while a fresh spawn.lock exists (the product's own in-flight marker, stale after 10 s, matching ipc.spawnLockStaleAfter), the helper waits for the daemon to take the lock and then shuts it down. The wait is bounded by e2eDaemonUpBound.
- Regression test: TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight. It was red first (the helper returned 2.55 s before the late daemon exited) and then passed 3 of 3.

## Criterion changes, each with a rationale
- 98e8557, e2e rows: v4 x01/x03/x08/x12, v5 x04/x10, IT-1 (v1_integration) and the SP-10 artifact row (test/e2e/checkpoint_test.go) used to read the instruction from the IPC reply.
  - They now require the empty reply and keep their seal and artifact proof.
  - v5 x14 proves the seal from the artifact instead of the recorded instruction.
  - v5 x05 now holds the binary's stdout to the host's PreCompact contract; it had been failing since C1.12.
  - Rationale: the instruction is retired and no host accepts one.
- 98e8557, daemon seam rows: TestBindCheckpointSealsOnTheFirstPreCompact, TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened and TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve used a non-nil hookSpecificOutput as their seal signal.
  - They now read the seal, or its absence, from the checkpoints directory; the first also parses the artifact and checks its session and seq.
  - Rationale: the reply is always empty now, so the directory is the only honest signal. Each change carries its rationale in the test.
- 9104209, contract tests: four probe-phrase tests and two helper tests are replaced by one table test pinning OK, SevInfo and "retired", including a stale-instruction case the old probe warned on. v5 x14 pins "retired".
  - Rationale: the row makes no observation; the old probe could only warn about a mechanism that does not exist, or pass on a false positive.
- 04a43a1, schema golden: one description edited by hand, for the reason given under (1).
- Fix seat: none. 7225a5b changes comment and assertion-message text only.

## Tests: the fix seat's own runs
All on Windows, co-loaded, logs under plans/sdd/V6-closeout/w2-hookout/runs/fix-*.log:
- The focused daemon rows (degrade banner, bounded quote, checkpoint route, ArmSources, BindCheckpoint) and observer rows (thrash bound) pass.
- The full internal/contract, internal/hookio and internal/config packages pass.
- The e2e rows AllSix, fault rows, in-flight spawn, checkpoint artifact and EveryContractAssertionHasARealProducer pass (185.9 s).
- gen-config-docs, gen-command-docs and gen-mcp-docs --check are up to date, and go test ./test/docs passes.

## Tests recorded earlier by the implementer
- Windows full packages:
  - cli, config, contract, hookio and observer pass.
  - The daemon rerun at aec178a passes (289.8 s).
  - The daemon's first full run failed TestArmSources at wire_checkpoint_test.go:790. That line maps to b070bbe:780, `require.NotNil(out.HookSpecificOutput)`, the retired seal signal. It was an intermediate edit state before 98e8557 changed that row.
- Linux non-root -race at aec178a:
  - daemon 1401/0, observer 454/0, contract 158/0, hookio 112/0, config 311/0, cli 348/0.
  - e2e rows 107/0.
  - Only the two reds already known from the integrated gate: guards TestCarriedDefects and integration HotPathWarmWithRealResidentState.
  - Linux was not re-run for 7225a5b, which only changes comments and message strings in internal/checkpoint, a package outside that Linux run's set.

## Pre-existing and co-load reds (not caused by this branch)
- TestV4_TombstoneToRecallToExpandRoundTrip ("append-only violation … already reserved for a different intent") and TestV5_TombstoneToExpandRoundTrip ("an unpinned re_read is the newest capture") fail on a git-archive export of base b070bbe (runs/e2e-tombstone-on-base-b070bbe-windows.log and e2e-v5x02-count4-base-b070bbe-windows.log).
- TestV3_HotPathUnchangedWithLedgerResident fails its B-A and B-B gates when re-run alone.
  - Branch at 8ae65b6: p50 32.8 / 22.5 ms.
  - A base b070bbe export run alone on the same loaded host fails the same gates: p50 28.7 / 20.5 ms (runs/fix-v3-hotpath-*.log).
  - No commit on this branch touches the observe.tool path that the bench measures.
- The Windows test/integration full run hit the 30 m timeout under co-load. Its three reds, plus HotPathDegradesRatherThanBlocks, pass when re-run alone (runs/integration-reds-rerun-alone-windows.log).

Processes: I killed nothing. The qompack daemon 53800 seen afterwards was created after my runs had finished, from another workstream's e2e.test.exe, so I left it alone. My git-archive scratch export was deleted.

### Commits

- d4c5dbf fix(daemon): bound the SessionStart degrade banner under the host cap
- e8e4301 fix(observer): bound the thrash warning under the host cap
- b09a739 refactor(daemon): read the banner value bound inside boundedQuote
- 98e8557 fix(daemon): retire the PreCompact focus instruction producer
- 9104209 fix(contract): report the custom-instructions row as retired
- 04a43a1 fix(config): stop describing incrementalSpanInstruction as emitting
- 3856d6b docs: record the retired PreCompact instruction and hook bounds
- aec178a fix(e2e): wait for an in-flight lazy spawn before shutdown
- 866ea1c docs(sdd): add w2-hookout run evidence
- 7225a5b docs(checkpoint): say the focus text reaches no hop since C1.18 (fix seat)
- 54b3204 docs(sdd): add w2-hookout fix-seat run evidence (fix seat)
- 8ae65b6 docs(sdd): add w2-hookout generated-docs check evidence (fix seat)
- 7d7e3f4 docs(sdd): add w2-hookout V3 hot-path row base comparison (fix seat)

### Tests

- `go test -count=1 -timeout=30m ./internal/checkpoint/  (Windows, 7225a5b)` — ok 74.378s (runs/fix-pkg-internal-checkpoint-windows.log)
- `go test -count=1 -v -run 'TestFocus|TestPreCompact|TestNoForbiddenImports' ./internal/checkpoint/` — ok, 23 top-level PASS (runs/fix-checkpoint-focused-windows.log)
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/checkpoint/...` — exit 0 (runs/fix-golangci-checkpoint-windows.log)
- `go run ./tools/devtool fmt-check && go vet ./internal/checkpoint/` — both exit 0
- `go test -count=1 -v -run 'TestDegradeBanner|TestBoundedQuote|TestSessionStart_DegradeBanner|TestCheckpointRoute_|TestArmSources|TestBindCheckpoint' ./internal/daemon/  (HEAD 7225a5b)` — ok (runs/fix-focused-head-windows.log)
- `go test -count=1 -v -run 'TestPromptReply_ThrashWarning|TestBoundThrashWarning' ./internal/observer/` — ok (runs/fix-focused-head-windows.log)
- `go test -count=1 ./internal/contract/ ./internal/hookio/ ./internal/config/` — ok, ok, ok (runs/fix-focused-head-windows.log)
- `go test -count=1 -timeout=30m -v -run '^(TestE2E_AllSixHooksExitZero|TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight|TestHooksExitZeroUnderFaults|TestE2E_CheckpointHookWritesImmutableArtifact|TestV5_EveryContractAssertionHasARealProducer)$' ./test/e2e/  (HEAD 7225a5b)` — ok 185.869s; all five PASS; AllSix 2.78s, fault rows 137.77s (runs/fix-e2e-rows-head-windows.log)
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check; go test -count=1 ./test/docs  (54b3204)` — all up to date; test/docs ok (runs/fix-docs-checks-windows.log)
- `go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e/  (alone, branch 8ae65b6 and base b070bbe export)` — FAIL on both: B-A/B-B gates on a co-loaded host (branch p50 32.8/22.5 ms; base p50 28.7/20.5 ms); co-load, not this branch (runs/fix-v3-hotpath-*.log)
- `implementer: go test -count=1 ./internal/{cli,config,contract,hookio,observer}  and internal/daemon rerun at aec178a (Windows)` — all ok; daemon 289.8s (runs/pkg-*.log). First daemon run failed TestArmSources at an intermediate edit state (old seal-signal assertion), fixed in 98e8557
- `implementer: plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-hookout, -race, touched packages at aec178a` — daemon 1401/0, observer 454/0, contract 158/0, hookio 112/0, config 311/0, cli 348/0; e2e 107/0; known reds only: guards TestCarriedDefects (6 dispositions), integration HotPathWarmWithRealResidentState (co-load)
- `implementer: go test -run '^TestE2E_AllSixHooksExitZero$' ./test/e2e/ at b070bbe (alone, co-loaded)` — FAIL SessionStart hookSpecificOutput nil (runs/allsix-repro-windows-1.log); classified as spool fail-open after the 1.5 s EnsureRunning bound under load
- `implementer: TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight red then green` — red before aec178a (helper returned 2.55 s before the late daemon exited); green 3/3 after

### Criterion changes

- 98e8557: the e2e rows v4 x01/x03/x08/x12, v5 x04/x10, IT-1 (v1_integration) and the SP-10 artifact row (test/e2e/checkpoint_test.go) now require the empty PreCompact reply instead of reading customInstructions from it, and keep their seal and artifact proof. Rationale: C1.18 retired the instruction and no host accepts one (C1.12).
- 98e8557: v5 x14 proves the seal from the artifact instead of the recorded instruction. v5 x05 holds the binary's stdout to the host's PreCompact contract; it had failed since C1.12.
- 98e8557: three internal/daemon seam rows (TestBindCheckpointSealsOnTheFirstPreCompact, TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened, TestArmSourcesOpensNothingWhenTheSourcesAlreadyResolve) read the seal, or its absence, from the checkpoints directory. A non-nil hookSpecificOutput used to be their seal signal, and the reply is now always empty.
- 9104209: the four probe-phrase tests and two helper tests in internal/contract are replaced by one table test pinning OK, SevInfo and Observed "retired", including a stale-instruction case the old probe warned on. v5 x14 pins "retired". Rationale: the row makes no observation now; the probe could only warn about a mechanism that does not exist, or pass on a false positive.
- 04a43a1: one description in testdata/golden/config/schema.json was edited by hand, because -update refuses a Windows host's platform defaults. It matches the new doc tag; docs/config-reference.md was regenerated and its --check passes.
- Fix seat (7225a5b): no criterion change. Only comment and assertion-message text changed; no assertion, threshold or test name did.

### Open issues

- PRODUCT DEFECT, needs routing (SessionStart route; next to C1.16 and the lifetime lane): a session.start that was spooled and later replayed from the spool still mints a §12.1 sentinel whose reply nobody receives. After two UserPromptSubmit scans miss it, the next SessionStart degrades the plugin to passive and blames the host ('hook.additional_context_delivered … sentinel not found after two chances'). Evidence: runs/diag-spooled-sessionstart-probe-windows.log. Code: internal/daemon/handlers.go around lines 786-807 mints on every MayAct session.start, and drainDispatch sends replayed session.start through dispatchOp. Under load, a slow first spawn is enough to trigger a false degrade.
- TestE2E_AllSixHooksExitZero/SessionStart stays load-sensitive until C1.16 makes SessionStart answer within its budget. The test expectation was deliberately not weakened. It passes alone at head (2.78 s).
- Present on base b070bbe too, not this lane: TestV4_TombstoneToRecallToExpandRoundTrip (append-only violation 'already reserved for a different intent') and TestV5_TombstoneToExpandRoundTrip (unpinned re_read is not the newest capture).
- Co-load: TestV3_HotPathUnchangedWithLedgerResident fails B-A/B-B on this loaded Windows host at both branch and base, so it needs a quiet-window run. Linux integration TestIntegration_HotPathWarmWithRealResidentState is the known co-load row.
- Coordinator's job: test/guards TestCarriedDefects_WaveReportRequiresResolution (six carried-defect dispositions).
- Not run: Linux was not re-run after 7225a5b, which changes only comments and message strings in internal/checkpoint, a package outside the Linux touched set. No hosted CI was run.

### Needs the owner

- Decide which lane fixes the defect where a replayed spooled session.start mints an undelivered probe and falsely degrades the plugin. It touches the SessionStart route, which the lifetime lane also edits.
- Decide whether to delete the remaining focus-text renderer: checkpoint.FocusInstructions and PreCompactResult.Instructions, which now feed only instructions_bytes and span_instruction in state/precompact.json, plus the compatibility-only config key checkpoint.incrementalSpanInstruction. Doing so changes the config schema, the golden and the debug-record shape. The alternative is to keep them as-is.
- Decide when hookio.HSO.CustomInstructions, kept decodable for an older resident daemon after upgrade, and History.PrecompactInstr, kept for persisted-data compatibility, can be removed.


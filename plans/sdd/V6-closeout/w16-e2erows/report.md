# W16-E2EROWS: candidate 5's e2e rows and the store benchmark HOME leak

Branch `closeout/w16-e2erows`. Workflow `wf_90777431-3b4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `8c73844320b768d331646700b23764da28eecdc9`

### Root cause

(1) Scheduler row: stale criterion. After D49 (w15-ledger dfff29cb, f1ad272e) a project with no elimination records opens no ledger, and both frontier paths begin a draft without one (checkpoint admitNoLedger). So checkpoint.sources.unavailable stays 0. The row also closed no segment, so it never exercised the advance. (2) X01 disk row: stale criterion. e2e7e480 clamps every displayed percentile to the snapshot's max, but x1v5RequireLatencyEquals compared the raw bucket bound. (3) X01 live row: product defect. history.json recorded no time for the MCP handshake or the probe scan, so contract.readByHistory stamped each refreshed row with the clock at read time. Two status reads of unchanged state then differed in hook.additional_context_delivered's TS. (4) X10 row: stale criterion. Since D45/D46 the echoed warning prompt reaches user_intent.evolution verbatim. (5) Store benchmark HOME leak: BenchmarkSearch_1000Roots, BenchmarkSearch_1000Roots_DistinctChunks and BenchmarkGC_50kObjects passed a hand-made `&testing.T{}` to newTestStore. That T's cleanups never run, so newProject's t.Setenv of HOME, USERPROFILE and QOMPACK_HOME was never undone. Its TempDir has no test name, which is why the leaked path is digits only (Temp\<n>\001\home). The store was never closed. pathstest.Main then fails the binary after PASS.

### Summary

## W16-E2EROWS report

All five items are fixed on branch `closeout/w16-e2erows` (base bb54c6ba, head 8c738443, 6 commits). Each touched e2e row and the store benchmark invocation now pass on Windows. Nothing was run on Linux, because the container is stopped.

| Item | Verdict | Fix |
|---|---|---|
| (1) TestDaemonIdleRunsSchedulerWork | stale criterion | the row now asserts what the daemon does (576ab5c5) |
| (2) X01 disk arm | stale criterion | the helper applies status's clamp (688b9da3) |
| (3) X01 live arm | product defect | a refreshed row is dated by its observation (eb7a2ab5); the e2e row is unchanged |
| (4) X10 checkpoint | stale criterion | the assertion is narrowed (96bd1e03) |
| (5) store benchmark HOME leak | test-code defect | benchmarks pass their own `b` (78b7d17e) |

Evidence logs are in `plans/sdd/V6-closeout/w16-e2erows/runs/`. Every file whose name contains `tempdiag` is a temporary diagnostic: the product edit was reverted with `git checkout` right after the run, and its first line says what was broken.

### (1) TestDaemonIdleRunsSchedulerWork (`test/e2e/scheduler_idle_test.go`)
**What the daemon does now.** With no elimination records, nothing opens the ledger. Both frontier paths begin a draft without it (`checkpoint.admitNoLedger`). Whichever reaches a closed segment first encodes it into `state/draft-<session>.json`, and `checkpoint.sources.unavailable` stays 0.

The old row closed no segment, so nothing advanced. The candidate-5 snapshot shows only `sched.*` counters and the unavailable counter at 0.

**Row changes:**
- It first sends a `git commit` Bash PostToolUse. This is the tap's commit boundary, so it closes the open segment. The row waits for the close in `segments.jsonl`, then sends the two Reads.
- The order matters: Evaluate plans `act.advance_frontier` only while the residual is above zero (`scheduler.planBackground`). My first attempt sent the commit last; the encode left a zero residual and the gauge was never set.
- The poll also waits for a draft whose `encoded` list is not empty.
- New assertions:
  - `sched.segment.closed.commit` > 0
  - the draft encodes the segment
  - `sources.unavailable` == 0 (this replaces the old required > 0)
  - no `sketches/tried.bloom` (negknow.Open writes the filter even with no records)
- The existing `no_writer` == 0 and gauge == 0 checks are kept.

**Comment corrections.** "negknow.Open has exactly one production call site and fires on the first COMPACTION" was wrong. The daemon's memoized opener runs on:
- a compaction;
- the first already_tried or record_eliminated call (cli `openingLedger`);
- the checkpoint sources' first resolve after Run, but only when records exist (cli `recordedLedger`).

fsck also has its own offline Open. On the tests that guard it: TestE2E_ObserverThroughDaemon asserts no tried.bloom after a session that never touches the ledger, and V3-X08 asserts nothing but negknow.Open creates the file. The comment now also names the unit rows.

**Temporary diagnostics (each fails the row, then reverted):**
- `admitNoLedger` refuses every resolve error: no draft, unavailable = 6.
- `advanceFrontierTask` sends ErrNoLedger down the unavailable route (the shape before D49): "Should be zero, but was 2".
- The runtime is built with a nil advancer: `no_writer` = 1.
- `recordedLedger` opens without records: tried.bloom exists.

### (2) X01 disk arm (`test/e2e/v5_x01_test.go`)
`x1v5RequireLatencyEquals` now shows each percentile as `min(p, Max)` when Max > 0, the same rule as `commands.latencyOf`. N and Max are still compared exactly. The live arm uses the same helper. It had never reached this check on candidate 5, because the contract check failed first.

**Temporary diagnostics:** showing p50 as p99 fails both arms (expected 131000, got 20480). Removing the clamp fails both arms (expected 109000, got 114688).

### (3) X01 live arm: product fix
- `SessionHistory` gains two additive `omitempty` fields:
  - `MCPInitializedAt` (`mcp_initialized_at`), set by the new `RecordMCPInitialized(at)`. The `mcp` op calls it with the daemon's clock.
  - `SentinelState.ScannedAt` (`sentinel.scanned_at`), set by the new `RecordSentinelScanAt(found, delivery, at)` on a find or a counted miss. `scanSentinelForPrompt` uses it.
- `readByHistory` dates a refreshed row by that time. When none was recorded (an older history, or a handshake only the daemon's seam saw), it keeps the TS of the row or ledger entry it replaces. It never uses the read time.
- doctor's `RefreshObservation` follows the same rule.
- The frozen golden is untouched (`TestHistoryDegradedGolden_Decodes` passes). `docs/troubleshooting.md` gains one sentence on this.

**Test first.** Four new daemon rows in `internal/daemon/contract_snapshot_stamp_test.go` were RED before the fix: the refreshed row carried the read time, 5 s after the observation (`runs/x01-stamp-daemon-red.txt`). They are GREEN after it.

**Temporary diagnostic:** keeping the read time in `readByHistory` fails the e2e live row with the same TS difference as candidate 5 (…118723 vs …118739).

### (4) X10 (`test/e2e/v5_x10_test.go`)
The new `x10v5RequireWarningOnlyAsEchoedPrompts` walks every string in the sealed checkpoint. The warning prefix may appear only as a `user_intent.evolution` entry that is exactly the echoed prompt. The prompt the warning was delivered with (the new const `x10v5DeliveredWith`) must appear in evolution exactly as typed. The elimination-record checks are kept.

**Temporary diagnostics:**
- The observer appends the delivered warning to that prompt's capture: fails with "the prompt the warning was delivered with is in evolution as the user's own words, unchanged".
- Finalize copies the newest restatement into `current_work.next_step`: fails naming `.current_work.next_step`.

### (5) Store benchmark HOME leak
- **Fix (test code only):** `newProject`, `newTestStore`, `openOver` and `objectPaths` take a `testing.TB`, and the three benchmarks pass `b`.
- **Proof** with quiet.sh's store invocation (store test binary, `-test.run '^$'`, the same 14-name `-test.bench` regex, `-test.benchtime 100ms -test.benchmem -test.count 3`): exit 1 with the pathstest message before (`runs/store-bench-home-red.txt`), exit 0 after (`runs/store-bench-home-green.txt`). Run alone at 100ms, each of the three failed and the other eleven passed.
- **Other quiet.sh packages:** none of the 18 others has `&testing.T{}`, raw `os.Setenv` or `os.Unsetenv` in its tests. In candidate 5's quiet logs the pathstest message appears only in the store candidate logs, in all 10 rounds on both OSes.
- **Cleanup:** I deleted the 12 digit-named temp directories my own red runs leaked (each holds a 50k-object store; checked for `001/home` and `001/project/.qompack`). Candidate 5's overnight leaks were not mine and were not touched.

### Commands and results (Windows, `-p 2`, machine shared with other seats; nothing failed on wall-clock, so nothing needed a solo re-run)
Each e2e row ran alone on the final code:
- `go test -p 2 -count=1 -v -run '^TestDaemonIdleRunsSchedulerWork$' ./test/e2e/` — PASS
- `go test -p 2 -count=1 -v -run '^TestV5_ObserveToStatusRoundTrip$' ./test/e2e/` — PASS (live, disk and nothing-observed arms)
- `go test -p 2 -count=1 -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^sequitur_warning_is_warning_only_and_once_per_rule$' ./test/e2e/` — PASS
- `go test -p 2 -count=1 -v -run '^TestV5_EveryContractAssertionHasARealProducer$' ./test/e2e/` — PASS

The other checks are listed one by one under tests.

### Criterion changes, with rationale
1. **TestDaemonIdleRunsSchedulerWork:** `sources.unavailable` > 0 became == 0, plus the effect checks above. D49 made "no ledger yet" not an error. Still red on all four regressions shown above.
2. **x1v5RequireLatencyEquals:** compares under status's max clamp (e2e7e480: no displayed percentile above the max sample). Still red when the quantile is wrong or the clamp is missing.
3. **internal/contract `refresh_test.go`:** four rows used to require the read-time stamp ("dated when it was read"). That stamp is the D53(a) defect.
   - TestRefreshFromHistory_AnObservedHandshakeAndSentinelReplaceTheirPendingRows and TestRefreshObservation_AnObservedProbeIsTheInjectionCapabilitysNewestWord now require the recorded observation time exactly.
   - TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing requires the last miss's time.
   - TestRefreshFromHistory_MatchesWhatTheChecksRead compares every field exactly except TS, which must equal the recorded time.
   - The read-time diagnostic shows the e2e live row catches the regression.
4. **X10 checkpoint:** "no warning text anywhere" became "only as an evolution entry that is exactly an echoed prompt; the delivered-with prompt unchanged; no other field" (D45/D46: verbatim user prompts). Red on both regressions shown above.

No t.Skip, nolint, threshold, timeout or golden change.

### Owner decisions
No new budget or bound constants. The e2e prompt-scan wait reuses the existing `obsProcessBound`/`obsProcessTick`. history.json gains two additive `omitempty` fields under the rule in history.go's wire-freeze comment, so no fixture re-freeze is needed.

### Commits

- 576ab5c5 test(e2e): assert the idle frontier advances with no ledger
- 688b9da3 test(e2e): compare X01 latency rows under status's max clamp
- eb7a2ab5 fix(contract): date a refreshed contract row by its observation
- 96bd1e03 test(e2e): let X10's checkpoint carry an echoed prompt verbatim
- 78b7d17e fix(store): run the store benchmarks under their own testing.B
- 8c738443 test(v6): record the w16 e2e rows' final green runs and checks

### Tests

- `go test -p 2 -count=1 -v -run '^TestDaemonIdleRunsSchedulerWork$' ./test/e2e/` — PASS on the final code (runs/sched-idle-green.txt). Four temporary product breaks each turn it red (runs/sched-idle-tempdiag-break-{admit,task,unwire,eager}.txt)
- `go test -p 2 -count=1 -v -run '^TestV5_ObserveToStatusRoundTrip$' ./test/e2e/` — PASS, all three subtests (runs/x01-e2e-green.txt). Temporary diagnostics: wrong p99 and no clamp fail the live and disk arms; read-time stamping fails the live arm (runs/x01-e2e-tempdiag-*.txt)
- `go test -p 2 -count=1 -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^sequitur_warning_is_warning_only_and_once_per_rule$' ./test/e2e/` — PASS (runs/x10-e2e-green.txt). Temporary diagnostics: warning attached to the delivered-with prompt, and warning copied to next_step, both FAIL (runs/x10-e2e-tempdiag-*.txt)
- `go test -p 2 -count=1 -v -run '^TestV5_EveryContractAssertionHasARealProducer$' ./test/e2e/` — PASS (runs/x14-e2e-green.txt)
- `go test -p 2 -count=1 -v -run '^(TestStatus_ASpentProbeRowCarriesTheLastMissNotTheRead|TestStatus_AnObservedProbeRowCarriesTheScanThatFoundIt|TestStatus_AnObservedHandshakeRowCarriesTheHandshakeTime|TestStatus_AHandshakeOnlyTheSeamSawKeepsTheStartsTime)$' ./internal/daemon <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — All 4 RED before the fix: TS was the read time, 5 s after the observation (runs/x01-stamp-daemon-red.txt). All 4 PASS after it (runs/x01-stamp-daemon-green.txt)
- `go test -p 2 -count=1 -run '^(TestDoctor_AnObservedProbeRefreshesTheInjectionRow|TestDoctor_AnUnobservedProbeKeepsTheLedgersWord)$' ./internal/cli -v <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — PASS (both)
- `store.test.exe (built from internal/store) -test.run '^$' -test.bench '^Benchmark(GetChunk|OpenSpan_4KB_of_4MB|Search_1000Roots|Search_1000Roots_DistinctChunks|PutBytes_100KB_Cold|PutBytes_100KB_Warm|PutObject_NovelChunk|CountFold_4MiB|GC_50kObjects|MarkEncoded_100|OpenStore_50kRoots|PutBytes_100KB_Cold_NoRedact|PutBytes_100KB_Warm_KeepRaw|PutBytes_100KB_Warm_NoRedact)$' -test.benchtime 100ms -test.benchmem -test.count 3` — Before the fix: exit 1 after PASS, with the pathstest HOME message (runs/store-bench-home-red.txt). After: exit 0 (runs/store-bench-home-green.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/store/` — ok 316.1s (runs/store-pkg-full.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 278.7s (runs/daemon-pkg-full.txt)
- `go test -p 2 -count=1 ./internal/contract/...` — ok (contract 2.1s, contracttest 1.1s) (runs/contract-pkg-full.txt)
- `go test -p 2 -count=1 ./test/docs` — ok
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/contract/... ./internal/daemon ./internal/store ./test/e2e (Windows, and again with GOOS=linux)` — clean on both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all eight PASS (runs/lint.txt)

### Criterion changes

- TestDaemonIdleRunsSchedulerWork: the required checkpoint.sources.unavailable > 0 became == 0. The row now also closes a segment with a git commit and asserts the draft encodes it (no-ledger advance) and that no sketches/tried.bloom appears. Rationale: D49 and w15-ledger dfff29cb/f1ad272e (no ledger yet is not an error). Still red on four temporary regressions: admitNoLedger refusing, pre-D49 unavailable routing, a nil advancer, and an eager ledger open.
- x1v5RequireLatencyEquals (X01): percentiles are compared under status's clamp to the max (e2e7e480); N and Max stay exact. Still red when p50 is shown as p99 or the clamp is removed.
- internal/contract refresh_test.go: TestRefreshFromHistory_AnObservedHandshakeAndSentinelReplaceTheirPendingRows, TestRefreshObservation_AnObservedProbeIsTheInjectionCapabilitysNewestWord and TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing required a read-time TS (> start TS); they now require the recorded observation time exactly. TestRefreshFromHistory_MatchesWhatTheChecksRead compares every field exactly except TS, which must be the recorded time. Rationale: D53(a); the read-time stamp is the defect. The e2e live row goes red when the read time is restored.
- X10 sequitur_warning_is_warning_only_and_once_per_rule: 'the checkpoint contains no warning text' became: warning text only as a user_intent.evolution entry exactly equal to an echoed prompt, the delivered-with prompt present unchanged, and no other field holds it. The elimination-record checks are kept. Rationale: D45/D46, user prompts reach evolution verbatim. Still red when the warning is attached to the delivered-with prompt or copied into current_work.next_step.

### Open issues

- Not run under this seat's daytime limits: Linux (the container is stopped), -race, and the whole test/e2e package. The coordinator's night run should cover the three touched e2e rows on Linux and quiet.sh's store benchmark step on Linux. Linux's C5.2 store logs showed the same pathstest failure in all 10 rounds, and this fix covers it.
- Candidate 5's overnight C5.2 runs leaked digit-named temp directories, each holding a 50k-object test store, on the Windows host and in the container. They are not mine and were not touched; the coordinator may remove them. I removed only the 12 my own red runs leaked.
- A handshake only the daemon's in-memory seam saw (its history write lost) has no recorded time. Its refreshed row keeps the start's evaluation TS, which is stable across reads but earlier than the actual handshake.
- The `SessionHistory.RecordSentinelScanOf` method (no time) now records ScannedAt as unknown (0). Only tests call it; the daemon uses RecordSentinelScanAt.
- The w15a-snapshot report's design note says a refreshed row is stamped with the read time, and that no X01 comparison is affected. Both statements are superseded by D53(a) and eb7a2ab5.
- internal/rehydrate tests pass `&testing.T{}` to requestFor and fullDeps. Those helpers only call t.Helper, so nothing leaks, and rehydrate is not in quiet.sh's benchmark list. I left them unchanged.

## Independent review

### review:e2erows: needs-fixes

- **nit** `commit 8c738443 (plans/sdd/V6-closeout/w16-e2erows/runs/*)` — The evidence-only commit has no `Refs:` footer, unlike the branch's other five commits. Its type is also `test(v6)`, although it adds only run logs, and earlier evidence and report commits use docs(v6) or chore(v6).
  - Evidence: `git log --format='%(trailers)' bb54c6ba..HEAD` prints five `Refs: V6-VERIFY, C3.4` lines for six commits. 8c738443's body ends after "the eight-check lint." with no footer.
  - Fix: Reword 8c738443, which is unpushed, so its subject reads `docs(v6): record the w16 e2e rows' final green runs and checks` and it ends with `Refs: V6-VERIFY, C3.4`. Add no attribution trailers.
- **nit** `internal/contract/history.go:327 and docs/troubleshooting.md:166-167` — Prompt scans keep counting misses after the probe has failed: `scanSentinelForPrompt` runs while `!Observed`, and Chances++ has no cap. Every later prompt in the session therefore moves Sentinel.ScannedAt, and with it the refreshed failing row's TS. The history.go comment describes this correctly ("the miss that spent the newest counted Chance"). troubleshooting.md instead says the row carries the time of "the prompt scan that ... spent its last chance", which a reader takes to mean the second miss, the one that made the row fail. Two reads of unchanged state are still equal, so D53(a) holds; only the wording is inexact.
  - Evidence: In handlers.go:747-753 the scan and RecordSentinelScanAt run whenever `h.Sentinel.Token != "" && !h.Sentinel.Observed` and the miss counts, and nothing checks for Chances >= 2. At history.go:326-327, `h.Sentinel.Chances++; h.Sentinel.ScannedAt = at` runs on every counted miss.
  - Fix: The cheaper fix is to change troubleshooting.md to "the newest prompt scan that missed it once its two chances are spent". The alternative is to set ScannedAt only while Chances < 2, so the failing row stays dated by the miss that made it fail, and to add a history_test row for a third miss.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


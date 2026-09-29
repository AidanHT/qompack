# Wave 9 — w9-testfix: pathstest telemetry race and hot-path sourcing check

Branch `closeout/w9-testfix`. Workflow `wf_0ae7e642-c8d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `622bf94ee9f66e4e9bc087d5b55cb2f55f9a004b`

### Root cause

(1) IsolateHome's goEnv ran `go env -json GOPATH GOMODCACHE GOCACHE GOENV` under the caller's home before moving it, with no Go telemetry mode file. In go1.26.6, no mode file means mode "local", so every go command opens a counter file under os.UserConfigDir()/go/telemetry/local. Because it holds no fresh upload token, it also starts a detached telemetry sidecar that keeps writing local/ after `go env` returns. On Linux the config dir is $HOME/.config, and for TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack that home is a t.TempDir fake real home. Its RemoveAll cleanup raced the sidecar: "unlinkat .../.config/go/telemetry/local: directory not empty". Any go command a test runs under the isolated home had the same race with IsolateHome's own restore/RemoveAll. (2) The hot-path structural check `require.Greater(ba.N, bd.N)` proved sourcing only through size: the planned daemon-side population is 2000 spawns + 64 warm-up hot tranche = 2064, against B-D's 2000. Any run that deferred more than 64 gated sends to the client spool (the documented degrade-rather-than-block path, ACK deadline expiring) shrank B-A below 2000. Examples: Phase 3 Linux 1539 observed, 525 missing, ledger 590 deferred, 0 lost; Windows 1536, 528 missing. Those runs failed as a "sourcing error" when the row's sourcing was correct. There is no product defect in either case.

### Summary

Both test defects are fixed in test code only. The product is unchanged. Windows verification is green for everything the fixes touch. Linux verification is left to the coordinator, as the brief requires, because the container is stopped.

This seat resumed on a worktree where a previous run of it had already made the two fix commits (a9f276e7, 9213f899) and left its logs in the scratch directory. I reviewed both diffs against the harness and toolchain code, re-ran the focused rows, the full pathstest package, fmt/vet/lint and the evidence, and committed the evidence logs (622bf94e).

## (1) pathstest telemetry race: a9f276e7

**Root cause:** see root_cause.

**Mechanism verification.** I tested which mechanism go1.26.6 honours by probing the toolchain, not by guessing. Evidence is in runs/01-go-telemetry-probe-windows.log:
- With no mode file, `go env GOTELEMETRY` reports "local" and writes local/*.count, upload.token, weekends and upload/.
- With TEST_TELEMETRY_DIR pointing at a directory holding a "mode" file containing "off", the go command reports "off" and writes nothing else. APPDATA is untouched.
- The documented mode file (<UserConfigDir>/go/telemetry/mode = off) also gives "off" with nothing else written.

**Fix, in internal/paths/pathstest/home.go:**
- goEnv runs the go command with TEST_TELEMETRY_DIR pointing at a throwaway temp directory holding an "off" mode file. It removes that directory afterwards, so it writes nothing into the caller's home. TEST_TELEMETRY_DIR is the go command's own test hook rather than a documented setting. A regression row fails if a toolchain stops honouring it.
- IsolateHome also writes the documented "off" mode file into os.UserConfigDir()/go/telemetry, but only where that directory lies inside the isolated home (Linux without XDG_CONFIG_HOME, and macOS). Any go command a test later runs there (the 44 home-reaching packages use the helper through TestMain) then opens no counter file and starts no sidecar.
- Where the config dir is outside the home (Windows APPDATA, or a set XDG_CONFIG_HOME), nothing is written: that is the user's own directory and no test removes it.
- Isolation is unchanged: the home is still redirected, the toolchain is still pinned first, and no go command runs when the toolchain is already pinned.

**Regression rows, in home_test.go:**
- TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder: the user config dir sits in a fake real home. A go env file makes GOMODCACHE provably the go command's own answer, which is the control that the command ran. The row requires that no go/telemetry directory appears there. It was RED before the fix on Windows (runs/02: "directory ...\AppData\Roaming\go\telemetry exists") and is green after.
- TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry: runs `go env GOTELEMETRY` with pathstest.Environ(). Where the config dir follows the home, it requires "off" and requires that the only file in the isolated home is the mode file. On Windows it requires the isolated home to stay empty. It can only be red on Linux or macOS, so the Linux run below is its real check.

## (2) Hot-path B-A sourcing check: 9213f899

`require.Greater(ba.N, bd.N)` is replaced by `require.NoError(hotpathDaemonPopulations(rep))`, with the same failure message. The new check reads the harness's own accounting: the row n values, the tailAdjustmentNote shortfall disclosures in report.go (all three shapes), and the delivery-ledger note in measure.go's buildNotes. It requires:
- **(a)** For B-A and for B-B: n plus the harness-disclosed shortfall equals exactly the planned 2000 + 64 = 2064. A disclosed planned count other than 2064, or a disclosed observed count that differs from n, is refused.
- **(b)** B-A's n is at most B-B's, because hook_controlled only records requests l0_ingest received.
- **(c)** If B-B has a shortfall, it needs all three of these:
  - A delivery-ledger note exists.
  - B-B's missing count is no more than the ledger's DEFERRED count.
  - delivered + deferred = sent, and sent is at least 2064.
  The ledger note itself states 0 lost; the harness refuses to write a report otherwise.

A B-A row re-pointed at the wall-clock samples has n = 2000 and no disclosure, so 2000 + 0 ≠ 2064 and it fails in both a clean run and a deferring one. TestIntegration_HotPathBAPopulationIsTheDaemonHistogram pins eight artifact shapes without a daemon. These include the Phase 3 Linux numbers verbatim (accepted), re-pointing in a clean run and in a deferring run (rejected), an unaccounted shortfall, a shortfall larger than the ledger's deferrals, and B-A larger than B-B. A temporary diagnostic, TestW9Diag_RecordedArtifacts, was not committed and is not in the tree. It ran the check on both recorded Phase 3 artifacts: both were accepted, and both were rejected when re-pointed (runs/05).

**What the test now proves, by mode:**
- **Isolated (gated):** sourcing is proven exactly as above. It is unchanged that B-A (15 ms) and B-B (50 ms) are gated, and that undelivered samples count as over budget, so a heavily deferring run is still failed through the harness's non-zero exit ("B-A's p99 CANNOT be certified"). That fails at the bench-exit require before the population check, which is correct: a failed certification, not a sourcing error.
- **Co-load (QOMPACK_UNDER_COLOAD=1, B-A/B-B reported):** the same sourcing proof applies. Deferrals now pass it when the ledger accounts for every missing sample with 0 lost, and fail it when it does not.

No budget, gating rule, certification rule or harness code changed.

## Commands and results (Windows, this seat, -p 2, no -race)
All run from C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix.
- `go test -p 2 -count=3 -v -run '^TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder$' ./internal/paths/pathstest` → PASS ×3. It ran in one invocation together with the rows `^TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry$` and `^TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack$`, 9/9 PASS (runs/03).
- `go test -p 2 -count=1 -v ./internal/paths/pathstest` → ok, 4/4 PASS (runs/04).
- `go test -p 2 -count=3 -v -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration` → PASS ×3, 24/24 subtests (runs/06).
- The hot-path row alone, isolated: `go test -p 2 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration` → FAIL at the bench-exit require (hotpath_test.go:950), a genuine gated breach under the concurrent Phase 3 load. 528 of 2064 were undelivered, so the harness failed B-A/B-B certification. It never reached the new check (runs/07). This is load, not a defect; the row must be judged on a quiet host.
- The same row with QOMPACK_UNDER_COLOAD=1 → the new population check PASSED on real data: "planned 2064; B-A n=1536 (528 never reached hook_controlled), B-B n=1536 (528 never reached l0_ingest); ledger sent 2130, delivered 1537, deferred 593". The test then FAILED at the next, unchanged assertion, hotpath_test.go:1091 "state.bin must report hot=0 (sync) for the entire measured run" (runs/08). See open issues.
- Full test/integration package (`go test -p 2 -count=1 -timeout=180m -json ./test/integration`, QOMPACK_UNDER_COLOAD=1, under Phase 3 load) → every test passed except TestIntegration_HotPathWarmWithRealResidentState. That one failed on the harness's own delivery-integrity guard: "of the 2130 hot-path requests ... l0_ingest observed 1537 and only 575 are accounted for by a deferred request line in the client spool — 18 are LOST" (runs/09-*).
- `go test -p 2 -count=1 ./test/guards` → only TestCarriedDefects_WaveReportRequiresResolution fails (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2). This failure already exists on the Phase 3 candidate: it appears identically in phase3/p3-win-tree.log and is a plans-data issue unrelated to these changes (runs/10).
- The three home-guard rows, each passed by name with `-run`:
  - `^TestGuard_EveryHomeReachingTestPackageIsolatesHome$` → PASS
  - `^TestGuard_HomeIsolationScannersSeeEveryShape$` → PASS
  - `^TestGuard_IsolatedTestsNeverReadAPoisonedRealHome$` → PASS (runs/11)
- `go run ./tools/devtool fmt-check` → exit 0. `go vet ./internal/paths/pathstest ./test/integration` → exit 0 on Windows and with GOOS=linux (runs/12).
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` → all 8 PASS (runs/13).

The evidence is under plans/sdd/V6-closeout/w9-testfix/runs/, including both recorded Phase 3 hot-path artifacts.

## Linux commands for the coordinator (Git Bash, from the worktree)
- (1) under -race, non-root:
  `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix/plans/sdd/V6-closeout/w9-testfix/runs/linux --prefix cx-w9-testfix 622bf94e w9-pathstest-race --count 20 -- ./internal/paths/pathstest`
  Then the same command with label w9-guards-home-race and `--run '^TestGuard_EveryHomeReachingTestPackageIsolatesHome$' -- ./test/guards`.
  - Expected: all PASS, and TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry finds "off" and only .config/go/telemetry/mode.
  - The whole-tree -race run (ALL-NON-E2E) is the final confirmation that no other package's TempDir cleanup trips.
- (2) alone, non-race:
  `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --repo <same> --out <same> --prefix cx-w9-testfix 622bf94e w9-hotpath-alone --no-race --timeout 30m --run '^TestIntegration_HotPathWarmWithRealResidentState$' -- ./test/integration`
  Then the same command with label w9-hotpath-coload and `--coload`, and once with label w9-popcheck and `--run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$'`.
  - Expected isolated: if the container again defers about 28%, the harness correctly exits non-zero on "B-A's p99 CANNOT be certified". That is the container's fsync tail (the class covered by owner Q1 and the hosted-runner fsync tail), not this check.
  - Expected co-load: the population check passes. The run may then stop at the hot=0 assertion (see open issues).

## Criterion changes
One, with this rationale: the B-A sourcing criterion changed from "n(B-A) > n(B-D)", which is a size proxy, to an exact accounting identity against the harness's own ledger. This is stricter for sourcing, not looser. A re-pointed row is caught at any deferral level, where before it was caught only when deferrals were at most 64. What no longer fails is only what should not: a correctly sourced row whose shortfall the ledger shows as spooled with 0 lost. The one limit is written into the doc comment: two populations of exactly equal size cannot be told apart by counts. For that case the existing b_a_method assertion stays in place.

## Owner decisions
None under the new-number rule: no budget, bound or constant was introduced. hotpathWarmHotTranche = 64 mirrors the harness's existing warmHotTranche; if it drifts, the check fails closed on the disclosed planned count.

### Commits

- a9f276e7 fix(pathstest): keep go telemetry out of homes a test removes
- 9213f899 fix(integration): prove b-a sourcing from the delivery ledger
- 622bf94e docs(v6-closeout): record w9-testfix evidence logs

### Tests

- `go test -p 2 -count=3 -v -run '^TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder$' ./internal/paths/pathstest (in one invocation with the rows ^TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry$ and ^TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack$)` — PASS 9/9 (runs/03-pathstest-focused-count3.log)
- `go test -p 2 -count=1 -v ./internal/paths/pathstest` — ok, 4/4 PASS (runs/04)
- `pre-fix: go test -v -run '^TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder$' ./internal/paths/pathstest against the unfixed home.go` — FAIL as intended: AppData\Roaming\go\telemetry exists (runs/02)
- `go test -p 2 -count=3 -v -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration` — PASS x3, 24/24 subtests (runs/06)
- `go test -p 2 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration (isolated)` — FAIL at the bench-exit require (gated breach, B-A uncertifiable with 528/2064 deferred) under Phase 3 co-load on the laptop; never reached the new check (runs/07)
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration` — new population check PASSED on real data (1536+528=2064, ledger deferred 593, 0 lost); then FAIL at the unchanged hot=0 assertion hotpath_test.go:1091 (runs/08)
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=180m -json ./test/integration` — every test PASS except the hot-path row: the harness's delivery-integrity guard reported 18 LOST under Phase 3 load (runs/09-*)
- `go test -p 2 -count=1 ./test/guards` — FAIL only TestCarriedDefects_WaveReportRequiresResolution (already failing on the Phase 3 candidate, as in p3-win-tree.log); nothing else failed (runs/10)
- `go test -count=1 -v -run '^TestGuard_EveryHomeReachingTestPackageIsolatesHome$' ./test/guards (with ^TestGuard_HomeIsolationScannersSeeEveryShape$ and ^TestGuard_IsolatedTestsNeverReadAPoisonedRealHome$)` — PASS 3/3 (runs/11)
- `go run ./tools/devtool fmt-check; go vet ./internal/paths/pathstest ./test/integration (Windows and GOOS=linux)` — all exit 0 (runs/12)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — 8/8 PASS (runs/13)

### Criterion changes

- TestIntegration_HotPathWarmWithRealResidentState, B-A sourcing check: `require.Greater(ba.N, bd.N)` (a size proxy that failed whenever more than 64 gated sends were deferred) is replaced by hotpathDaemonPopulations. It requires n + disclosed shortfall = 2064 planned for both B-A and B-B, B-A n <= B-B n, and B-B's shortfall covered by the delivery ledger's DEFERRED count with delivered + deferred = sent >= 2064 and 0 lost. Rationale: the check's purpose is to prove the B-A population is the daemon histogram, not the 2000 wall-clock samples. The new identity proves that at every deferral level; a re-pointed row has n = 2000 with no disclosure and fails. It is stricter on sourcing. Only a correctly sourced, fully ledger-accounted deferral stops failing. Budgets, the isolated gating and the certification of undelivered samples as over budget are untouched.

### Open issues

- The hot-path row will still be red in co-load runs that defer heavily, now at the next assertion (hotpath_test.go:1091, 'state.bin must report hot=0 (sync) for the entire measured run'), which is unchanged. The daemon's §12.2 breach detector (internal/daemon/handlers.go applyHotPathTransition) moves to spool when l0 latency breaches its budget windows. That is a latency-budget outcome, and it is asserted unconditionally even though the co-load mode waives the B-A/B-B/B-E wall budgets. Its message even says 'a run that stays inside its budget'. Seen on Windows under Phase 3 load after the population check passed (runs/08). I did not change it: it lies outside this brief, and relaxing it would be a criterion change. The Phase 3 whole-tree co-load gate stays red on this row until that is decided.
- In the full test/integration run on Windows under Phase 3 load, the harness's delivery-integrity guard reported 18 of 2130 hot-path requests LOST: in neither l0_ingest nor the client spool (runs/09-integration-full-hotpath-output.log). This is a different failure from the two fixed here, and a lost event is a correctness concern. It is not classified yet. Candidates are a spool open/write failure under load (internal/ipc/spool.go writeLocked: AppendOnly open error, ErrSpoolFull, or a Windows sharing violation against a concurrent drain; the spool error is sticky once hit) or a harness accounting race. It needs a dedicated investigation on a quiet machine; this seat was not allowed to run that load.
- Linux verification of both fixes is pending: the container was stopped on purpose. The commands are in the summary. TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry only has real teeth on Linux or macOS, where the config dir follows HOME.
- Isolated runs in the Linux container that defer about 28% will still fail, correctly, on B-A certification (uncertifiable p99 from undelivered samples counted as over budget). That is the container and hosted-runner fsync tail (owner Q1), not a test defect.
- TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2) is already failing on the Phase 3 candidate and needs plans/CARRIED-DEFECTS.tsv resolution before the V6 report can be signed off.

### Needs the owner

- Decision: in co-load mode (QOMPACK_UNDER_COLOAD=1), should TestIntegration_HotPathWarmWithRealResidentState keep asserting that the daemon never moved to the §12.2 spool submode? Today it does: sawSpool false, no degraded WARN, nothing in LOUD.log. Keep it (the co-load gate stays red whenever the host is loaded enough to trip the breach detector), or report the transition in co-load mode and keep asserting it only when isolated? The second option is a criterion change for co-load runs only; the isolated gate would be unchanged. No new budget number is involved either way.

## Independent review

### review:testfix: needs-fixes

- **minor** `test/integration/hotpath_test.go:600-622 (hotpathDaemonPopulations doc comment and identity (a)); fixture at ~1456-1461; commit 9213f899 message` — Identity (a), n + disclosed shortfall = 2064, holds by construction for any row built by buildBudgetRowFromSnapshot, because the harness derives each row's missing count as Sent - observed (hookControlledShortfall and gatedLedger.Undelivered in test/bench/hotpath/delivery.go:341 and :79, reported through tailAdjustmentNote in report.go:427). So (a) only catches a re-point that swaps in buildBudgetRow(baSamples), which carries no disclosure. The other plausible re-point feeds a snapshot built from the 2000 wall-clock samples into buildBudgetRowFromSnapshot. The harness then discloses "64 of the 2064 planned", and (a) passes. That case is caught instead by the harness's hookControlledShortfall (clean run: 64 > deferred 0 + invalid 0) and by the new check (b), B-A n <= B-B n (deferring run with bbMissing > 64). It slips through when bbMissing == 64, which the doc concedes as the equal-size case. It also slips through in a case the doc does not mention: bbMissing < 64 with the daemon's hotpath_sample_invalid count covering 64 - bbMissing. The old `ba.N > bd.N` caught a 2000-sample row in both cases. The doc comment and the commit message say that a re-pointed row "fails in a clean run and in a deferring one", and the unit fixture "B-A re-pointed at the wall-clock spawn samples in a deferring run" uses a shape the harness cannot emit: a 2000-sample B-A row with no B-A shortfall note. So the fixture pins the easy mutation and leaves the realistic one unpinned.
  - Evidence: delivery.go:341 `missing := l.Sent - observed`; main.go:616 builds B-A with that missing count; report.go:427 `total := snap.N + missing`, so the note's planned count always equals n + missing. Fixture: `report(hotpathBenchIterations, 1539, ledger(2130, 1540, 590), uncertifiable(string(obs.BB), 525, 1539))` has no B-A note, while the harness would have written `B-A's p99 CANNOT be certified ...: 64 of the 2064 planned samples ...`.
  - Fix: Add fixtures for the snapshot-level re-point as the harness would write it. First: B-A n=2000 carrying bounded/exact("B-A", 64), B-B n=1539 with a 525-deferral ledger. Expect a rejection via the B-A <= B-B check ("more samples than the"). Second: the same B-A row with B-B n=2000 and bbMissing=64, documented as the known equal-size blind spot. Rewrite the hotpathDaemonPopulations doc comment and the in-test comment at ~983 to say which check catches which re-point shape: (a) catches a row with no disclosure; the harness guard plus (b) catch a re-pointed snapshot. List the residual cases: bbMissing == hotpathWarmHotTranche, and a shortfall covered by hotpath_sample_invalid. The artifact does not carry the invalid counter, so these residuals cannot be closed from the test alone, and saying so keeps the criterion-change rationale accurate.
- **nit** `internal/paths/pathstest/home_test.go:170-196 (TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry)` — On Windows the row checks only that the isolated home stays empty, because the go command's telemetry goes to the real APPDATA, so it cannot go red there. Its fix-side behaviour, the mode file that IsolateHome writes, is not exercised on any platform run by this seat. The implementer disclosed this. The Linux -race run the coordinator owes is therefore the only real evidence for the mode-file half of fix (1).
  - Evidence: `inHome := strings.HasPrefix(cfg, home)` is false on Windows (cfg = %APPDATA%), so `want` is nil and the `off` assertion is skipped. Only runs/01 and runs/02 (Windows) exist; there is no Linux run.
  - Fix: No code change. The coordinator must run the listed Linux command (w9-pathstest-race, --count 20, ./internal/paths/pathstest) and confirm from the -v output that the row logged or required "off" with only .config/go/telemetry/mode present before closing C3.2. The ALL-NON-E2E -race whole-tree rerun then confirms that no other package's TempDir cleanup trips.

## Fix seat (review resolution) — status `done`, head `54cdbb6e`

### Root cause

(1) pathstest: the go command ran under the isolated or fake home with no telemetry mode file, so it defaulted to local mode. It then wrote counter files there and started a detached telemetry sidecar that kept writing into the home while TempDir cleanup removed it. Fixed by the implementer (a9f276e7) with TEST_TELEMETRY_DIR plus an "off" mode file. (2) hotpath: the old `ba.N > bd.N` check treated any B-A shortfall of 64 or more as a sourcing error. In the observed runs that shortfall is the §8.1 breach detector switching the daemon to spool submode after 3 consecutive 512-sample windows over budget (n=1536). The implementer's ledger-based check (9213f899) left a blind spot for snapshot-level re-points. The fix seat closed it with a refusal of B-A n == 2000 (253495ca).

### Summary

Fix seat for w9-testfix. Branch: closeout/w9-testfix in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix. The working tree is clean and no processes from this seat are left running. Only tests and evidence changed. There are no product changes.

## Review resolution
Finding (minor): identity (a), n + disclosed shortfall = 2064, holds by construction. A snapshot-level re-point therefore slips through when bbMissing == 64, or when bbMissing < 64 and hotpath_sample_invalid covers the rest. The doc comment and commit message overstated the coverage.
-> CONFIRMED and FIXED. It is not just documented.
- Verified in the harness code:
  - gatedLedger sets Sent to 2064 and derives the shortfall as Sent - observed (delivery.go:79, :341).
  - buildDaemonRows passes that shortfall to buildBudgetRowFromSnapshot (main.go:614-622).
  - tailAdjustmentNote writes total = snap.N + missing (report.go:427).
  - So any harness-built row satisfies (a). Only a row-level re-point (buildBudgetRow over the raw samples, which writes no note) fails it.
- Failing test first: I added fixtures for the snapshot-level re-point exactly as tailAdjustmentNote writes it (B-A n=2000 carrying "64 of the 2064 planned"). Red run (runs/14-review-red-population-fixtures.log):
  - Three sub-cases were accepted with a nil error: bbMissing==64; bbMissing=54 with invalid covering 10; clean run with 64 invalid.
  - The >64 deferring case was already refused by check (b) "more samples than the". This confirms the reviewer's analysis in every case.
- Fix (253495ca), check (c): refuse a B-A row whose n is exactly hotpathBenchIterations (2000). Every re-point at the wall-clock samples has that n, whether it swaps the row or the snapshot. Counts alone cannot tell such a row from a genuine one, so the test refuses rather than passes. This restores everything the old `ba.N > bd.N` caught. The cost is one narrow false red: a genuine run whose B-A shortfall is exactly 64. A fixture pins that n=2001 (one sample off) is accepted.
- The hotpathDaemonPopulations doc now lists (a)-(d) and says which check catches which re-point shape. So does the in-test comment. Fixtures are renamed "row-level" and "snapshot-level".
- I did not amend the old commit message of 9213f899, which says a re-point "fails in a clean run and in a deferring one". It was true only for the row-level shape; 253495ca's message records the correction.

## What each mode now proves (item 2)
**Both modes:**
- The harness exits 0.
- b_a_method names hook_controlled.
- B-D n=2000 and is never gated.
- For B-A and B-B, n + the harness-disclosed shortfall = 2064 (2000 spawns + 64 warm-up tranche).
- B-A n <= B-B n.
- B-A n != 2000.
- Any B-B shortfall is covered by the delivery ledger's DEFERRED count, the ledger adds up (sent = delivered + deferred >= 2064), and it says 0 lost.

Together these prove B-A is the daemon-side hook_controlled population over this run's sends, and that deferrals are accounted for, not treated as a sourcing error.

**Isolated (gated):** in addition, the harness gates B-A and B-B. The certification rule is unchanged: undelivered samples are counted as over budget, and the gate fails ("CANNOT be certified") when the p99 rank falls inside the undelivered block. So a deferring run still fails its B-A gate unless its p99 lands among delivered samples under the limit.

**Co-load (reported):** B-A and B-B are reported only. The population proof above is the only B-A assertion, plus the existing no-spool / no-degrade / LOUD.log assertions (see the open owner decision).

## New root-cause evidence for item 2: the deferral is the breach detector, not ACK-deadline noise
Three runs deferred the identical count: the Windows Phase 3 artifact, the implementer's co-load run 08, and my co-load run 17. Each has B-A n=1536, ledger 2130 sent / 1537 delivered / 593 deferred. The Linux artifact has n=1539.
- 1536 = 3 x 512 = BreachWindows (default 3, config/defaults.go:123) x sampleWindow (512, daemon/budget.go:11).
- The delivered B-A p99 was 180 ms on Windows under load and 106 ms in the Linux container, against a 15 ms limit. In the Linux container B-B's p50 was 45 ms. So every 512-sample window breached.
- After the third window, breachDetector.Observe returned ToSpool. applyHotPathTransition then set HotSpool in state.bin (handlers.go:434-446), and every later hook spooled. The Linux run has 3 extra samples, consistent with requests already in flight when the transition landed.
- So the "deferrals" in these runs are the §8.1/§12.2 breach fallback working as designed. Transitions land on multiples of 512 (plus a few in flight), so check (c)'s coincidence at 2000 is not a natural landing point.
- Consequence for the coordinator: my fix removes the false sourcing error. But on a loaded Windows host and in the Linux container this row stays red for real reasons:
  - Isolated: the harness exits non-zero because B-A cannot be certified (implementer's runs/07).
  - Co-load: the sawSpool assertion fails at hotpath_test.go:1112, "state.bin must report hot=0 (sync) for the entire measured run" (runs/08 and my runs/17).
- A green run needs a host where the hot path meets 15 ms, or the owner decision below.

## Commands and results (Windows, -p 2, no -race, machine loaded by the Phase 3 gates)
- `go test -p 2 -count=1 -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration/`, before the (c) check: FAIL, 3 sub-cases red as expected (runs/14).
- The same test after the fix, with `-count=3 -v`: ok, 13 sub-tests x 3 all PASS (runs/15).
- `QOMPACK_UNDER_COLOAD=1 timeout 1200 go test -p 2 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration/`: the population check passed (log line "B-A n=1536 (528 never reached hook_controlled) ... deferred 593"). The test then FAILED at :1112 on the sawSpool assertion, which is the breach transition described above (runs/17). I did not re-run it isolated; on this loaded machine that gate-mode run breaches the budget, as the implementer's runs/07 already shows.
- `go test -p 2 -count=1 ./internal/paths/pathstest/`: ok.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./test/integration/` on Windows and with GOOS=linux: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all PASS. Re-ran runpatterns and docmarkers after the evidence commit: PASS.
- Not re-run: the full test/integration package. The implementer's run took 7673 s under load (runs/09). My change touches only the pure function hotpathDaemonPopulations and its fixture test, and nothing else in the tree calls it (checked with grep).

## Linux commands for the coordinator
The container was stopped on purpose, so I ran nothing on Linux. Use the gate script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh at commit 54cdbb6e, with `--prefix cx-w9-testfix` and --repo/--out as C:/ paths. Check the script header for the exact spelling of the package/-run arguments.
1. Item 1, -race non-root: `go test -race -count=3 -run '^(TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack|TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder|TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry)$' ./internal/paths/pathstest/`, then `go test -race ./internal/paths/pathstest/ ./test/guards/`.
2. Item 2, alone, non-race: `go test -count=1 -timeout=30m -v -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration/`, then `go test -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration/`, isolated (QOMPACK_UNDER_COLOAD unset).
   - Expect the population check to pass.
   - Expect the harness to exit non-zero for as long as the container's B-A p99 (106 ms) exceeds 15 ms. That is a real budget result, not this defect.

## Criterion changes
- (c) is new: `B-A n != 2000`. It refuses the one population size where counts cannot separate a daemon-side row from one re-pointed at the spawn samples. Rationale: never let a re-point pass. It restores coverage the pre-ledger check had and the implementer's version lost. The price is a rare false red at exactly a 64-sample B-A shortfall. The alternative is to accept that blind spot and just document it (the reviewer's suggestion); the coordinator can choose that instead.
- The implementer's change is carried unchanged: `ba.N > bd.N` became the ledger-based accounting, with budgets, gating and certification unchanged.

### Commits

- a9f276e7 fix(pathstest): keep go telemetry out of homes a test removes (implementer)
- 9213f899 fix(integration): prove b-a sourcing from the delivery ledger (implementer)
- 622bf94e docs(v6-closeout): record w9-testfix evidence logs (implementer)
- 253495ca fix(integration): refuse a b-a row sized like the spawn loop (fix seat, review finding)
- 54cdbb6e docs(v6-closeout): record w9-testfix review-round evidence (fix seat)

### Tests

- `go test -p 2 -count=1 -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration/ (before the fix)` — FAIL as intended: 3 snapshot-level re-point sub-cases accepted (runs/14)
- `go test -p 2 -count=3 -v -run '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$' ./test/integration/` — ok, 39/39 sub-test passes (runs/15)
- `QOMPACK_UNDER_COLOAD=1 timeout 1200 go test -p 2 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration/` — FAIL at hotpath_test.go:1112 (sawSpool: breach detector ToSpool after 3x512 windows, B-A p99 180ms vs 15ms on a loaded host); population check PASSED (runs/17)
- `go test -p 2 -count=1 ./internal/paths/pathstest/` — ok
- `go run ./tools/devtool fmt-check; go vet ./test/integration/ (windows and GOOS=linux)` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS (runpatterns/docmarkers re-run after evidence commit: PASS)

### Criterion changes

- test/integration hotpath: `require.Greater(ba.N, bd.N)` is replaced by hotpathDaemonPopulations, which checks: (a) n + disclosed shortfall = 2064 for B-A and B-B; (b) B-A n <= B-B n; (c) B-A n != 2000; (d) B-B shortfall <= the ledger's DEFERRED count, with 0 lost and a consistent ledger. Rationale: prove B-A's sourcing from the harness's own accounting so deferrals are not failed as sourcing errors. (c) keeps every re-point at the 2000 spawn samples failing, at the cost of a false red only at an exact 64-sample B-A shortfall. Budgets, isolated gating of B-A/B-B, and the rule that undelivered samples count as over budget are unchanged.
- internal/paths/pathstest (implementer): new regression rows fail if a go command leaves telemetry files in the home it ran under. No isolation guarantee was loosened.

### Open issues

- TestIntegration_HotPathWarmWithRealResidentState stays red on a loaded Windows host and in the Linux container for real reasons. The delivered B-A p99 was 180 ms (Windows under load) and 106 ms (Linux container), against 15 ms. The breach detector trips ToSpool after 3 x 512 windows (n=1536), so an isolated run fails the B-A certification and a co-load run fails the sawSpool assertion. The implementer's report attributed the deferrals to ACK-deadline expiry; that attribution is wrong.
- The implementer's full test/integration run under co-load (runs/09) failed the harness's delivery-integrity guard: 18 of 2130 hot-path requests were LOST (the appendToSpool drop path, internal/ipc/client.go), in neither the daemon nor the spool. This is possibly a product defect. The hot-path runs done alone did not reproduce it (0 lost). It needs its own workstream.
- Linux verification of both items was not run because the container was stopped by instruction. The commands are in the summary.
- Commit 9213f899's message says a re-pointed row fails in clean and deferring runs, which is true only for the row-level shape. It was not amended; 253495ca records the correction.

### Needs the owner

- Decision (carried from the implementer): in co-load mode (QOMPACK_UNDER_COLOAD=1), should TestIntegration_HotPathWarmWithRealResidentState keep asserting that the daemon never moved to the §12.2 spool submode (sawSpool false, no degraded WARN, nothing in LOUD.log)? Option A: keep it, and the co-load gate stays red whenever the host is loaded enough to trip the breach detector. Option B: report the transition in co-load mode and assert it only in isolated runs; this is a criterion change for co-load runs only. New evidence: in every observed failing run the spool transition was the breach detector firing after exactly 3 x 512 over-budget windows (B-A delivered p99 180 ms on Windows and 106 ms in the Linux container, against 15 ms). No new budget number is involved either way.
- Decision (new, fix seat): check (c) refuses a B-A row whose n is exactly 2000, closing the snapshot-level re-point blind spot the reviewer found. The cost is a false red if a genuine run's B-A shortfall is exactly 64, which breach transitions (multiples of 512) do not naturally produce. Keep this conservative refusal (current code), or drop (c) and document the blind spot as the reviewer suggested. No budget number is involved.

## Independent verification of the fix seat: needs-fixes

- **nit** `test/integration/hotpath_test.go:1008-1009 (in-test comment above the hotpathDaemonPopulations call, rewritten by 253495ca)` — The in-test comment still gives the ACK deadline expiring as the cause of the deferrals behind the criterion change: "(§8.1/§12.2's degrade-rather-than-block path, the ACK deadline expiring)". The fix seat's own root-cause evidence says that attribution is wrong. In every observed run (Windows Phase 3, runs/08, runs/17, and Linux n=1539), the shortfall was the breach detector moving to spool after 3 x 512 over-budget windows. 253495ca rewrote this paragraph but kept the rationale it had just disproved.
  - Evidence: hotpath_test.go:1009 reads "the ACK deadline expiring". The fix seat's result says "The implementer's report attributed the deferrals to ACK-deadline expiry; that attribution is wrong", and its root_cause says B-A n=1536 = BreachWindows(3) x sampleWindow(512) -> ToSpool -> HotSpool.
  - Fix: Change the parenthetical to name the breach fallback, for example "(§8.1/§12.2's degrade-rather-than-block path: the breach detector moving the daemon to spool submode, or an ACK deadline expiring)". That keeps the rationale for the criterion change accurate.
- **nit** `test/integration/hotpath_test.go:1012` — 253495ca added one comment line of about 190 columns: "...and the test refuses to guess. Whether such a run can pass B-A is the certification rule's business,". The rest of the block wraps near 100 columns. The new sentence was spliced into the old line instead of being re-wrapped. No linter enforces the width, so this is cosmetic only.
  - Evidence: sed -n 1012p shows one line holding both the new sentence and the start of the old one.
  - Fix: Re-wrap lines 1011-1014 to the block's usual width.


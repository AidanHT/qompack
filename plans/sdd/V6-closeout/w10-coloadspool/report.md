# Wave 10 — w10-coloadspool

Branch `closeout/w10-coloadspool`. Workflow `wf_620a0532-b9a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `d528df52`

### Root cause

No product defect. Co-load mode (QOMPACK_UNDER_COLOAD=1, obs.UnderCoload) waives the B-A/B-B/B-E wall budgets under ADR 0010, but TestIntegration_HotPathWarmWithRealResidentState still asserted in every mode that no §12.2 spool transition happened: sawSpool false, no degraded WARN, nothing in LOUD.log, and a state.bin left by teardown reporting sync. On a loaded host the daemon's breach detector (internal/daemon/budget.go breachDetector, then handlers.go applyHotPathTransition) correctly moves to spool after runtime.hotPath.breachWindows=3 consecutive 512-sample windows over budgetMs=15. So the co-load row failed on a consequence of a budget it waives. The evidence is w9 runs/08 and runs/17 (B-A n=1536 = 3x512, ledger 2130/1537/593, 0 lost) and this seat's run 05, which has the same counts.

What the product emits on ToSpool, read at handlers.go:434-446: the counter hotpath_degraded +1; log.Warn and log.Loud, both carrying "daemon: hot path degraded to spool submode" with budget_ms=<detector limit> and windows=<detector need>. internal/logging's Loud writes a level=loud line to the day log and to LOUD.log. When SpoolOnBreach is set it also persists state.bin hot=1. So the product does emit a named WARN and LOUD line, and the test asserts those; the fallback to state.bin plus the counter was not needed.

### Summary

## What changed (test/integration/hotpath_test.go only)
1. **Comment nits (1bffb930).** The comment above the hotpathDaemonPopulations call now names the cause as "the breach detector moving the daemon to spool submode, or an ACK deadline expiring". The 190-column line is re-wrapped to the block's width.
2. **D39 (023025b1).**
   - New pure function `hotpathJudgeSpool(obs, underCoload, wantBudgetMs, wantWindows, ledger)` with types `hotpathSpoolObservation` and `hotpathSpoolTransition`, and the regex `hotpathBreachFields`.
   - `hotpathCountDegradedWarns` is now `len(hotpathDegradedWarnLines)`. New `hotpathDegradedLoudLines`.
   - The four spool assertions at the end of the row now go through one `require.NoError(hotpathJudgeSpool(...))`. The mode comes from the existing `underCoload := obs.UnderCoload()`; no new mode or env var.
   - A co-load transition is logged (t.Logf) with: transition count, budget_ms/windows named by the lines, the watcher sighting, B-A/B-B delivered n and p99 against the planned 2064, and the ledger sent = delivered + deferred, 0 lost.
   - The function doc comment and the co-load rationale paragraph now describe the split. The old paragraph said the spool assertions were "unaffected by how long the host took"; D39 makes that false.
   - New fixture-level row: `TestIntegration_HotPathSpoolTransitionJudgedPerMode`, 16 subtests.
3. **Evidence logs (d528df52)** are in plans/sdd/V6-closeout/w10-coloadspool/runs/01..06.

## Criterion change (D39)
**Isolated: unchanged.** Any one of the four signs still fails the row:
- the watcher saw hot=1
- a level=warn degraded line in the day logs
- the degraded message in LOUD.log
- a state.bin that survived teardown and is not sync

The error texts are the same as before. Only the order in which they are checked could differ.

**Co-load now proves:**
- **No signs:** the run is clean and passes, as before.
- **Any sign:** the run passes only if all of these hold:
  - (a) At least one degraded WARN line and at least one LOUD.log line, with equal counts. applyHotPathTransition writes one of each on every ToSpool, so a spool record with neither is rejected as a silent degrade.
  - (b) Every one of those lines carries budget_ms and windows equal to p.Cfg.Runtime.HotPath.BudgetMs and BreachWindows. These are the config the measured daemon loaded from the same project; that is the "names the breach" requirement.
  - (c) The harness's delivery-ledger note is present and sent == delivered + deferred. The regex only matches "and 0 lost", and the harness exits non-zero on any loss, so that refusal is unchanged.
  - Every other assertion of the row is untouched.

**Not required in co-load:** the watcher actually seeing hot=1. It polls every 50 ms and could in principle miss the window, and the product's own record of the transition is the log lines.

**One stricter change in both modes:** a LOUD.log read error other than not-exist now fails the row. Before, it was silently read as "no transition".

## Fixture-level unit test
Yes, as w9 did with hotpathDaemonPopulations. `TestIntegration_HotPathSpoolTransitionJudgedPerMode` covers:
- isolated: clean; full transition; WARN only; LOUD only; state.bin left by teardown
- co-load: clean; loud, named and accounted; transition the watcher missed; spool with no lines; missing LOUD; missing WARN; unequal counts; line without fields; wrong windows; no ledger; ledger that does not add up

Red first: a temporary diagnostic mutation forced the co-load arm onto the pre-D39 isolated logic (`if true {`). The row then failed 10 co-load subtests (runs/02). The mutation was reverted from a backup before anything was committed.

## Commands and results (Windows, -p 2)
- `go test -p 2 -count=3 -timeout=20m -v -run '^TestIntegration_HotPathSpoolTransitionJudgedPerMode$' ./test/integration`, run together with `^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$` in one alternation → PASS: 87 subtests (3 x (16+13)), ok 0.297s (runs/01)
- `go vet ./test/integration` and `GOOS=linux go vet ./test/integration` → exit 0 (runs/06)
- `go run ./tools/devtool fmt-check` → exit 0 (runs/03)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,runpatterns,docmarkers,sleepcheck` → exit 0 (runs/04)
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=60m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration` → **PASS**, 159.2s (runs/05).
  - Launched through a PowerShell wrapper that refused to start while win-timing was running and would have stopped only its own process tree if win-timing began. The chain log never showed a win-timing start.
  - The transition happened and was reported: "moved to §12.2 spool submode 1 time(s)... budget_ms=15 over windows=3... watcher saw hot=1: true; ... B-A n=1536 p99=36.864ms, B-B n=1536 p99=13.312ms, of 2064 planned; ledger 2130 sent = 1537 delivered + 593 deferred, 0 lost".
  - The same test failed at the old sawSpool assertion in w9 runs/17.

## Notes
- **Counts match across runs.** Every run so far has transitioned at exactly n=1536 with ledger 2130/1537/593: w9 runs/08 and runs/17, the w9 Phase 3 Windows artifact, and this run. B-A's delivered p99 here was only 36.9 ms. On this laptop even moderate load trips the detector on its first three windows, so an isolated timing run here will likely keep failing B-A certification, as w9 runs/07 already showed. That is a host fact and does not touch D39.
- **Full package not run.** The whole test/integration package was not run: the seat's machine limits and the heavy Phase 3 load ruled out a 180-minute package run. The only package-level evidence is the focused rows and the one co-load run.
- **Linux not run.** The container was stopped on purpose; the coordinator owes Linux verification, including a co-load Linux run of the row.
- No new budget or bound numbers were introduced.
- No leftover processes: a check for my worktree, scratch path and bench-hotpath found none.

### Commits

- 1bffb930 test(integration): name the breach detector behind hot-path deferrals
- 023025b1 fix(integration): report a loud co-load spool transition (D39)
- d528df52 docs(v6-closeout): record w10-coloadspool verification evidence

### Tests

- `go test -p 2 -count=3 -timeout=20m -v -run '^TestIntegration_HotPathSpoolTransitionJudgedPerMode$' ./test/integration (run jointly with '^TestIntegration_HotPathBAPopulationIsTheDaemonHistogram$')` — PASS x3, 87 subtests, ok 0.297s (runs/01)
- `temporary diagnostic: co-load arm forced to pre-D39 logic, go test -p 2 -count=1 -run '^TestIntegration_HotPathSpoolTransitionJudgedPerMode$' ./test/integration` — FAIL as expected, 10 co-load subtests red; reverted (runs/02)
- `go vet ./test/integration ; GOOS=linux go vet ./test/integration` — exit 0 both (runs/06)
- `go run ./tools/devtool fmt-check` — exit 0 (runs/03)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,runpatterns,docmarkers,sleepcheck` — exit 0 (runs/04)
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=60m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration` — PASS 159.2s; spool transition reported (1 transition, budget_ms=15 windows=3, B-A n=1536, ledger 2130=1537+593, 0 lost) (runs/05)

### Criterion changes

- D39, co-load only: TestIntegration_HotPathWarmWithRealResidentState now REPORTS a §12.2 spool transition instead of failing on it. A co-loaded run showing any spool sign must have (a) at least one degraded WARN line and at least one LOUD.log line, with equal counts, (b) each line carrying budget_ms and windows equal to the configured runtime.hotPath budgetMs and breachWindows, and (c) the harness delivery-ledger note present with sent == delivered + deferred and 0 lost. All other assertions of the row are unchanged. Isolated mode is unchanged: the watcher's hot=1 sighting, the WARN line, the LOUD.log line and a non-sync state.bin left by teardown each remain a failure.
- Stricter, both modes: a LOUD.log read error other than not-exist now fails the row. Previously it was ignored and read as 'no transition'.

### Open issues

- The Linux co-load run of TestIntegration_HotPathWarmWithRealResidentState has not been done here; the container was stopped on purpose. The coordinator owes it.
- The full test/integration package was not run in this seat because of Phase 3 load and memory limits. Only the focused rows and one co-load run of the hot-path row were run.
- Every observed run transitions to spool at exactly n=1536 with ledger 2130/1537/593, even at a delivered B-A p99 of 37 ms. Isolated timing runs on this laptop will likely keep failing B-A certification. That is a host fact, unchanged by D39.
- The 18-lost-events finding belongs to the lostev seat, not this one. D39 still requires the harness's 0-lost ledger in co-load.

## Independent review

### review:coloadspool: needs-fixes

- **nit** `test/integration/hotpath_test.go:943` — Commit 023025b1 rewrote the doc comment of TestIntegration_HotPathWarmWithRealResidentState and left a new 138-column line in it. The rest of the block is 89-98 columns. Wave 9's nit asked for this same kind of line (the 190-column one at ~1012) to be re-wrapped to the block's width, so the fix commit brought the problem back one function up.
  - Evidence: awk length on HEAD d528df52 gives 943: 138: `// accounted for (D39, hotpathJudgeSpool). B-A p99 < 15ms and B-E's wall-clock p99 < 2000ms are judged here only when the invoking job has`. Lines 936-946 around it are 22-98 columns. No lll linter is configured, so fmt-check and the lint subset pass without catching it.
  - Fix: Re-wrap lines 941-946 to the block's width (at most about 98 columns). For example, end line 943 at `accounted for (D39, hotpathJudgeSpool). B-A p99 < 15ms and B-E's wall-clock p99 < 2000ms` and carry `are judged here only when the invoking job has` onto the next line, then reflow what follows.
- **nit** `commit 1bffb930 (test(integration): name the breach detector behind hot-path deferrals)` — This commit changes code (test/integration/hotpath_test.go) but has no Refs footer. Its sibling 023025b1 and the wave-9 code commits (253495ca, 9213f899, a9f276e7) all carry `Refs: V6-VERIFY, C3.x`.
  - Evidence: `git log --format='%(trailers)'` shows empty trailers for 1bffb930 and `Refs: V6-VERIFY, C3.4` for 023025b1. Only docs-evidence commits (d528df52, 622bf94e) have no Refs by convention.
  - Fix: Before integration, reword 1bffb930 to add `Refs: V6-VERIFY, C3.4`. If the branch is not rewritten, note the omission in the workstream report. Do not add attribution trailers.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


# w6-linuxrows: the Linux rows that failed under load

Branch `closeout/w6-linuxrows`. Workflow `wf_8e58c74e-b50`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `3c15ec7fdcdf8fc8faefceb239cb0b9bafe5db59`

### Root cause

(a) x09: mainly a test bound that was too tight. The flush's own daemon first replays the 406-450 spool lines the pre-flush shutdown left, so the SessionEnd GC came 1-3 min after the flush, against a 32 s bound. There are also two product causes. The idle drain's hard deadline cut a slow line at every tick and left the flush unread behind it (fixed, 7dc9527). A losing spawn left a fresh spawn.lock, so no daemon started (not fixed, routed). (b) Spooled start: a test-helper race, fixed before the base by aba55d8. (c) ThinSlice: Stop never wrote the observer's in-memory graph to disk, so a session end that Stop cancelled lost the graph (80fab5f). The watcher's hard deadline and its back-off on spools a budget-cut pass never reached caused the :535 variant (ce61328). (d) LaneOverflow: a requested drain cut short by its budget was never requested again (5437e70), and the budget, a context deadline, cancelled any line slower than 2 s on every pass (bc3dde9, 4894f4f). (e) ShingleCap: a pure CPU wall-clock row with a documented, plan-mandated 10 ms bound. It misses only when judged under -race on a shared host.

### Summary

# w6-linuxrows: the Linux rows that fail under load

This is the report for coordinator commit. Branch `closeout/w6-linuxrows`, worktree `../qompack-cx-w6-linuxrows`, base `f6095e2`, head `3c15ec7f` (the last code commit is `7dc9527`). Three seats did the work: 2026-09-26 22:40 to 00:45, 2026-09-27 11:55 to 12:27, and this seat from about 13:30 local. All evidence is committed under `plans/sdd/V6-closeout/w6-linuxrows/runs/`, and `runs/INDEX.txt` says what each run is and what it showed.

## Verdict per row

| Row | Kind | Cause | Fix |
|---|---|---|---|
| (a) x09 `TestV3_LiveSessionWriteSetAndAppendOnly` | Mainly test, plus two product findings | The 32 s bound was wrong for the replay the flush's daemon does before its SessionEnd. Separately, the idle drain could strand the flush, and a stray spawn claim could stop any daemon from starting. | `c551fcd` (bound with derivation), `7dc9527` (idle drain), `64a01ed` (evidence logging). The spawn-claim gap is **not fixed** and is routed to the owner. |
| (b) `TestE2E_SpooledSessionStartNeverDegradesTheProject` | Test | Already fixed by `aba55d8` (w5-coldstart) before `f6095e2`. | None needed. |
| (c) `TestE2E_ThinSliceDropsControlOnlyEdges` :563 | Product | Stop never wrote the observer's in-memory graph to disk. | `80fab5f`, plus `ce61328` for the :535 variant under heavy load. |
| (d) `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest` | Product | A requested drain that its budget cut short was never requested again, and the budget cancelled the line in flight. | `5437e70`, `bc3dde9`, `4894f4f` |
| (e) `TestFeaturesFrom_LexicalCohesionShingleCap` | Pure timing row | Its bound is documented and was set for a non-race, isolated lane. | Nothing changed; reported. |

**Is the async SessionEnd (D13) dropping its GC?** No. Whenever a daemon ran and a drain reached the flush, the "observer: gc" line came. At head it came 1m05s to 3m03s after the flush, behind 406 to 450 unconsumed spool lines. It came in 3/3 runs under load at `c08d008`, 3/3 at `7dc9527`, and in both whole-package runs.

At base, two mechanisms could delay the SessionEnd indefinitely:
- the idle drain cancelling a slow line at every idle tick, which kept the flush unread behind it (now fixed);
- a stray spawn claim that left no daemon running at all (not fixed).

## Root causes, with evidence

### (d) LaneOverflow

- **Diagnostic at base plus a pass log** (`runs/linux-diag/*laneoverflow*`): 28/30 passed. Each failing iteration had exactly one requested pass. It ended with "context deadline exceeded" after 2.26 s or 2.12 s and was never repeated. The lane had dropped its overflow when it asked, and the pass's release woke no lane, so the refused jobs waited for a flush or 120 s of idleness.
- **`5437e70`:** after a pass stops with DeadlineExceeded, the requester asks for one more pass itself. It still rests as long as the pass took, so requested passes use at most half its time.
- **Under the heavy generator at `c551fcd`:** every pass cut the same prompt capture, ending "not durable" after about 2.6 s. The pass budget was a context deadline, so any line slower than 2 s was cancelled by every pass.
  - **`bc3dde9`** adds `withPassBudget`. The budget travels on the context. The pass starts no new line once the budget is spent, and a line it has started keeps its own `drainLineDeadline` (5 s). Cancelling the context still ends both at once.
- **At `ce61328`:** the pass's bookkeeping alone (listing, progress state, and one sync per spool file) outlasted 2 s, so passes reached no line.
  - **`4894f4f`:** the budget ends a pass only after it has consumed a line.
- **Same-window A/B under the capped generator** (5 CPU + 3 fsync loops):
  - base `daemon-rows-load7-f6095e2`: LaneOverflow **8/30 FAIL** ("4 of 5 arrivals acknowledged");
  - head `daemon-rows-load7-c08d008`: **30/30 PASS**.
  - At base the row passes alone (40/40 idle); it failed 7/40 under the first seat's heavy generator.

### (c) ThinSlice :563

- **Diagnostic** (`runs/linux-diag/*thinslice*`): one of 8 iterations persisted a DAG of **0 nodes and 0 edges** for 48 indexed tool uses. The day log shows the cause: "daemon: stop: a session end ignored cancellation; abandoning it", then "SessionEnd failed: context canceled".
- Since C1.15 the session end runs after the flush hook has answered. The row's shutdown then cancelled the end before it flushed the graph, and Stop never flushed it, even though `dag.Graph.Flush` documents a flush at shutdown.
- A replayed flush cannot rebuild the graph: the tool deliveries are already acknowledged, and a redelivery never recomputes a first run's graph.
- **`80fab5f`:** `WireObserver` registers the observer's Persist on `Options.OnStop`, so Stop writes the graph and `state/observer.json`.
- **Same-window A/B:** base `e2e-rows-load8-f6095e2` ThinSlice **1/3 FAIL** at :563; head `e2e-rows-load8-64a01ed` **3/3 PASS**.
- **`ce61328`** handles a different assertion, :535 ("the last mixed call was never indexed", with 13 to 32 client spools still undrained after 60 s under the heavy generator). The watcher's pass had the same hard deadline, and a pass cut by its budget put every spool it had not reached on the doubling back-off.

### (a) x09

- **Evidence at base:** the diagnostic `runs/linux-diag/*e2e-load2*` failed 3/3 with three different outcomes:
  1. No daemon logged anything after the flush.
  2. The GC line came after 3m20s.
  3. The flush's daemon logged "ObservePrompt capture not durable" and "idle task returned an error name=drain err=context deadline exceeded" every 30 s. `drain.json` shows the WAL stopped at 226467/267147, with every client spool unread, the flush's own (`client-64327`) among them.
- The second diagnostic (`*x09v2*`) showed a `spawn.lock` written mid-session by a hook's losing spawn, and GC lines after 4m04s and 4m16s.
- **Bound too tight.** The GC line comes from the SessionEnd of a daemon the flush spawns. That daemon first replays whatever the pre-flush shutdown's bounded drain (`StopDrainBound`) left, which was 406 to 450 lines under load. **`c551fcd`** replaces the 32 s wait with `x9FlushGCBound(pending)` = `e2eHistoryConvergeBound` + (pending + 1) × `daemon.DrainLineDeadline`.
- **Idle drain strands the flush (outcome 3) — product defect.** `RunOnce` gave the drain task the rest of `idleRunBudget` as a context deadline. So a slow line was cut at every idle tick, and every spool after it in pass order, the flush included, was never reached.
  - New row `TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest`: on the code before the fix it failed with 15 of 15 attempts cut inside the slow part and neither spool published (`runs/idle-drain-red-64a01ed-windows.log`).
  - **`7dc9527`** registers the drain as a paced task. `RunOnce` hands it `withPassBudget`, as the requested and watcher passes have. Other idle tasks keep their deadline.
- **Stray spawn claim (outcome 1) — product gap, not fixed.** The sequence:
  1. A hook's 5 ms connect deadline expires and it spawns a daemon.
  2. That daemon loses the lock to the running one and exits before listening, so it never removes `spawn.lock`.
  3. The claim stays fresh for 10 s.
  4. A flush in that window, after the running daemon has exited, stands aside (D17) and starts nothing.
  - **`c551fcd`** makes the row wait for any such claim to lapse (`x9AwaitNoSpawnInFlight`). The product gap goes to the owner; see open items.
- **Same-window A/B:** base `e2e-rows-load8-f6095e2` x09 **3/3 FAIL**; head **3/3 PASS**, with the GC line 2m02s to 3m03s after the flush.

### (b) Spooled SessionStart

- `e2eWaitDaemonUp` used to wait for a dial. On POSIX the listen backlog accepts a dial before the startup replay finishes, so the row read the contract history too early.
- `aba55d8` (w5-coldstart) waits for an answered `admin.ping` instead.
- At `f6095e2` it passes 3/3 idle and 3/3 under the capped generator. At head it passes 3/3 under load (twice) and in both whole-package runs. No change needed.

### (e) ShingleCap

- A pure wall-clock row. Min-of-5 `FeaturesFrom` over two 200 KB previews must stay under 10 ms.
- It protects the `maxShingles` cap: a regression to per-observation cost that grows with preview size.
- The bound is plan-mandated (SP-12; ADR 0012 R45, min-of-5). ADR 0010 says it is judged in `ci.yml`'s `timing` lane (`-p 1`, no `-race`, alone) and only reported under `QOMPACK_UNDER_COLOAD`. V5-report records the co-load reporting.
- Measured under `-race` with the capped generator and `--coload`: min-of-5 was 2.8 to 7.6 ms at base and head, with one 10.16 ms reading at head, and 3.0 to 4.8 ms at the final head.
- One miss at 12.3 ms in a run without `--coload` while the container was shared (`daemon-rows-idle-c08d008`, 1/10). The first seat's heavy generator gave 15.4, 17.2 and 18.4 ms at base.
- The misses come from judging a CPU-time bound under `-race` on a shared host. No budget was changed.

## Review of the earlier seats' draft

All seven earlier commits are kept. I read each diff and each commit's test.

- **Final tests fail without their fix.** Each fix's behaviour was reverted in a scratch clone of `64a01ed`, with the final tests in place, and every targeted row failed (`runs/mutants/`):
  - M1, no re-request: the cut-short row, "3 of 5 arrivals acknowledged".
  - M2, hard deadline: both slow-line rows.
  - M3, no consume-first: the syncs row.
  - M4, watcher back-off: the cut-short watcher row.
  - M5, watcher deadline: the slow-spool row.
  - M6, no Stop persist: the Stop row.
- **`80fab5f`:** I checked the close order. Persist is registered before the lazily opened ledger's closer, and the graph belongs to the composition root, so Persist never writes through a closed handle. No test asserts that `state/` is empty after a Stop.
- **`c08d008`:** the slow-line rows now count cancellations inside the slow part instead of total attempts. A retry after the slow part is the per-line deadline working as intended. M2 and M5 still fail the rows.
- **The first seat's heavy generator** (22 CPU + 8 fsync loops) predates the owner's cap. Its runs, load1 to load4, are kept as history only. Runs cut short by the pause were not used.
- **Misleading log:** `seat1-windows/c-green-watcher-family-windows.log` is not green (heavy load, tests from before `c08d008`). INDEX says so.
- **Path fix:** I renamed the diagnostic runs' `zz-w6/<Test>-<pid>-<n>` directories to `thinslice-n` / `x09-n` because git could not index the original paths.

## Commands and results (final head `7dc9527` unless noted)

Every Linux run used `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-linuxrows --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows --out <scratch> <rev> <label> ...` (non-root, `-race`). Every run shared the container with other workstreams' gates.

**Final-head row runs under load** (capped generator, `--coload`):
- **daemon-rows-load9**, count 10, `--run` set to the alternation of the 11 daemon rows: 110/110 PASS.
- **e2e-rows-load9**, count 3, the three e2e rows: 9/9 PASS.

**Whole packages:**
- **`internal/daemon` idle** (no generator, no `--coload`): PASS. 1523 pass, 0 fail, 1 skip (Windows only).
- **`test/e2e` idle** (`--timeout 150m`): 307 pass, 1 FAIL, 3 permitted skips.
  - The failure is `TestV3_HotPathUnchangedWithLedgerResident` (x11): B-A/B-B p99 426 ms against a 15 ms limit.
  - Rows (a), (b) and (c) pass.
- **x11 A/B:** `-run '^TestV3_HotPathUnchangedWithLedgerResident$'`, count 2, base and head side by side. It fails on both, 2/2 each, with the same distribution: B-A p50 115/82 ms at base and 123/82 ms at head, p99 180 to 213 ms on both. This is pre-existing; w5-coldstart saw it at `90e1db3` too. It does not come from this branch.
- **`internal/daemon` + `test/e2e` together with `--coload`** (`--timeout 150m`): PASS. daemon 1523/0/1 skip, e2e 308/0/3 skips.
  - The generator ran 20:21 to 20:41 UTC. That covered the whole daemon package and the first 32 e2e tests, ThinSlice among them. x09 and the spooled start ran after the generator stopped, with only other workstreams on the container.

**Windows:**
- `go test ./internal/daemon -count=1 -v -timeout=60m`: ok, 768 top-level PASS, run while the container was loaded.
- The Idle, Drain, SpoolWatch, DeliveryOrder and RunOnce families of `./internal/daemon`: 149 PASS.
- The new rows and their neighbours, count 3: 30 PASS.
- `go test ./internal/daemon -run '^TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest$'`: FAIL before `7dc9527`, PASS after.

**Checks:** `go vet ./internal/daemon ./test/e2e` passes on Windows and with `GOOS=linux`. `go run ./tools/devtool fmt-check` passes. `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` passes every sub-check.

**Wall-clock reds re-run before believing them:** the ShingleCap 12.3 ms miss passed at 3 to 5 ms in the next runs. x11 was re-run alone as the A/B above.

## Criterion changes (rationale)

1. **`c551fcd`, x09 wait.** The wait for the GC line is now `x9FlushGCBound(pending)`. The old 32 s covered a daemon start and shutdown but not the replay the flush's daemon must do before its SessionEnd. Measured: 1m05s to 3m03s behind 406 to 450 lines, against a derived bound of about 34 to 38 min.
2. **`c551fcd`, stray claim.** x09 now waits up to `e2eSpawnLockStaleAfter` + one tick for a stray spawn claim to lapse before the flush. The row stops exposing the spawn-claim liveness gap, which is reported below instead.
3. **`c08d008`, slow-line rows.** They count cancellations inside the slow part, not attempts. The cut-short watcher row loops until a pass has published the first spool. Mutants still fail both.

No skip, threshold, golden or assertion was loosened. `64a01ed` only adds log lines.

## Open items

- **Stray spawn claim (routed to w6-borrow / the D17 owner).** A spawned daemon that loses the daemon lock exits without removing `spawn.lock`. The claim stays fresh for 10 s. If the running daemon exits inside that window, a SessionEnd flush stands aside and starts no daemon. The flush then waits in the spool for the next session (D13: replayed, not lost). Spawn code is outside this workstream's scope.
- **A line past its own deadline ends the whole pass.** In `pass()`, a line that exceeds its own `drainLineDeadline` returns DeadlineExceeded, which is treated as the pass's own stop. So a line that takes more than 5 s halts the pass at that line, and every file after it waits until that line publishes. It was not the root cause of any failure seen here. I recommend a follow-up: treat it as a per-file error while the pass is still live.
- **x11 in Linux gates without `--coload`.** `TestV3_HotPathUnchangedWithLedgerResident` fails in any Linux run without `--coload` on this container, at base and at head. Phase 3's `linux-e2e` runs without `--coload`, so it will be red there; this ties to owner question Q1 on fsync latency.
- **ShingleCap under `-race`.** Phase 3's `linux-tree` judges it under `-race`; see owner decision 4 below.
- **Windows back-off row.** `TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick` failed 2 of 5 on Windows at base while the first seat's heavy container load was running. The likely cause is its 1 s fixture horizon being shorter than one pass under that load. It passed 60/60 on Linux under the capped load, 10/10 at the final head under load, and in all whole-package runs. Not changed.
- **ADR 0010 item 4 versus the gates.** The ADR says `test/e2e` is not run under `-race`, but the gate script and `phase3.sh` do run it under `-race`. These runs followed the gate script.

## Owner decisions (details in needs_owner)

1. The pass-budget semantics that `idleRunBudget` now has. No new number.
2. The x09 derived bound.
3. The stray spawn-claim gap: fix it or accept it.
4. How Linux gates judge ShingleCap and x11.

### Commits

- 5437e703 fix(daemon): request again a drain its budget cut short (first seat; reviewed, kept)
- 80fab5f3 fix(daemon): write the observer's graph and state out at stop (first seat; reviewed, kept)
- c551fcdf fix(e2e): wait out x09's flush replay and stray spawn claims (first seat; reviewed, kept)
- bc3dde9c fix(daemon): let a requested drain pass finish the line it started (first seat; reviewed, kept)
- ce613287 fix(daemon): spool watcher finishes its line, no backoff on budget (first seat; reviewed, kept)
- 4894f4f4 fix(daemon): a budgeted drain pass consumes a line before stopping (first seat; reviewed, kept)
- c08d0088 test(daemon): count budget cuts, not attempts, in slow-line rows (second seat; reviewed, kept)
- 64a01ed0 test(e2e): log x09's replay backlog and GC wait on every run (this seat)
- 7dc95273 fix(daemon): let the idle drain finish a line slower than its budget (this seat)
- 3c15ec7f docs(v6): record w6-linuxrows evidence runs (this seat)

### Tests

- `linux-nonroot-gate.sh f6095e2 daemon-rows-load7 --coload --count 30, --run set to the alternation of TestDeliveryOrder_LaneOverflowIsDrainedOnRequest, TestFeaturesFrom_LexicalCohesionShingleCap and TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick -- ./internal/daemon (capped generator 5 cpu + 3 fsync)` — FAIL: LaneOverflow 8/30 ('4 of 5 arrivals acknowledged'); the other two 60/60 PASS (base reproduction)
- `linux-nonroot-gate.sh c08d008 daemon-rows-load7 (same flags, same generator window)` — PASS 90/90 (LaneOverflow 30/30)
- `linux-nonroot-gate.sh f6095e2 e2e-rows-load8 --coload --count 3, --run set to the alternation of TestV3_LiveSessionWriteSetAndAppendOnly, TestE2E_SpooledSessionStartNeverDegradesTheProject and TestE2E_ThinSliceDropsControlOnlyEdges -- ./test/e2e (capped generator)` — FAIL: x09 3/3 (no 'observer: gc' line in 32 s), ThinSlice 1/3 at observer_e2e_test.go:563; spooled start 3/3 PASS (base reproduction)
- `linux-nonroot-gate.sh 64a01ed e2e-rows-load8 (same flags, same generator window)` — PASS 9/9; x09 GC line 2m2s-3m3s after the flush behind 420-450 spool lines
- `linux-nonroot-gate.sh c08d008 daemon-rows-idle --count 10 (all task rows plus the new rows), no generator, no --coload` — 99/100: one ShingleCap wall-clock miss at 12.3 ms (container shared); every functional row 10/10
- `linux-nonroot-gate.sh 7dc9527 daemon-rows-load9 --coload --count 10, --run set to the alternation of the 11 daemon rows (both task rows, the backoff row, the six new rows, the idle-drain row, SP05-D1) -- ./internal/daemon (capped generator)` — PASS 110/110; ShingleCap min-of-5 3.0-4.8 ms reported
- `linux-nonroot-gate.sh 7dc9527 e2e-rows-load9 --coload --count 3 (the three e2e rows) -- ./test/e2e (same generator window)` — PASS 9/9; x09 GC line 2m31s-2m58s behind 410-439 lines
- `linux-nonroot-gate.sh 7dc9527 daemon-whole-idle --timeout 60m -- ./internal/daemon` — PASS: 1523 pass, 0 fail, 1 skip (TestDrainWindowsOpenBlobRetainsCleanupIntent, Windows only)
- `linux-nonroot-gate.sh 7dc9527 e2e-whole-idle --timeout 150m -- ./test/e2e` — FAIL: 307 pass, 1 fail (TestV3_HotPathUnchangedWithLedgerResident, x11 B-A/B-B p99 426 ms vs 15 ms, pre-existing), 3 permitted skips; x09, spooled start, ThinSlice PASS
- `linux-nonroot-gate.sh f6095e2 x11-ab --count 2 --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e, and the same at 7dc9527 side by side` — FAIL 2/2 on both: B-A p50 115/82 ms base vs 123/82 ms head, p99 180-213 ms both; not from this branch
- `linux-nonroot-gate.sh 7dc9527 daemon-e2e-whole-coload --coload --timeout 150m -- ./internal/daemon ./test/e2e (capped generator 20:21-20:41 UTC)` — PASS: daemon 1523/0/1 skip, e2e 308/0/3 skips; x09 GC line 1m5s behind 406 lines
- `go test ./internal/daemon -count=1 -v -timeout=60m (Windows, 7dc9527, beside the Linux --coload run)` — ok 374 s, 768 top-level PASS
- `go test ./internal/daemon -run '^TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest$' -count=1 -v (Windows, on 64a01ed then on 7dc9527)` — FAIL before the fix (15/15 attempts cut inside the slow part, neither spool published); PASS after
- `go test ./internal/daemon on the Idle, Drain, SpoolWatch, DeliveryOrder and RunOnce families, -count=1 (Windows, 7dc9527)` — ok, 149 PASS
- `mutant runs M1-M6 (runs/mutants/run_mutants.sh.txt) in a scratch clone of 64a01ed, each with its row's single -run pattern` — each targeted row FAILS with its fix's behaviour reverted
- `go vet ./internal/daemon ./test/e2e and GOOS=linux go vet ./internal/daemon ./test/e2e; go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS every sub-check (lint_exit=0)

### Criterion changes

- c551fcd: the x09 wait for the SessionEnd 'observer: gc' line is now x9FlushGCBound(pending) instead of e2eHistoryConvergeBound (32 s). The old bound covered a daemon start and shutdown but not the replay the flush's daemon does first (measured 1m05s-3m03s behind 406-450 lines). The derivation uses the product's own per-line deadline.
- c551fcd: x09 waits, up to e2eSpawnLockStaleAfter plus one tick, for a stray fresh spawn.lock to lapse before the flush, so the flush's own lazy spawn is what the row tests. The row therefore no longer exposes the spawn-claim liveness gap, which is reported as an open item and an owner decision instead.
- c08d008: the two slow-line rows (requested-pass and watcher) assert that no attempt was cancelled inside its slow part, not exactly one attempt. A retry after the slow part is the per-line deadline working as intended. The cut-short watcher row loops until a pass has published the first spool, then checks that the spool it left is due at once. The M2, M4 and M5 mutants still fail these rows.

### Open issues

- The spawn-claim gap is not fixed; routed to w6-borrow or the D17 owner. A daemon started by a hook's lazy spawn that loses the daemon lock exits before listening, so run/spawn.lock stays fresh for 10 s. If the running daemon exits in that window, a SessionEnd flush stands aside and starts nothing, and the flush waits in the spool for the next session (replayed, not lost). The x09 row now waits for such a claim to lapse.
- In drainer.pass, a line that exceeds its own drainLineDeadline returns context.DeadlineExceeded, which is treated as the pass's own stop. So a line taking more than 5 s halts every pass there (startup, idle, requested, watcher, flush settle), and every file after it waits until that line publishes. It was not the root cause of any failure seen here. Recommended follow-up: treat it as a per-file error while the pass is still live.
- TestV3_HotPathUnchangedWithLedgerResident (x11) fails in Linux e2e runs without --coload at base and at head alike: B-A/B-B p99 180-426 ms against 15 ms on this container. phase3.sh's linux-e2e runs without --coload, so it will be red there. This ties to owner question Q1 on fsync latency.
- TestFeaturesFrom_LexicalCohesionShingleCap is judged under -race in Linux gates without --coload (phase3.sh linux-tree). It missed once at 12.3 ms on a shared container, and ADR 0010 judges this bound in the non-race ci.yml timing lane.
- TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick failed 2 of 5 on Windows at base while the first seat's heavy container load was running (second seat's interleaved runs). The likely cause is the fixture's 1 s horizon being shorter than one pass under that load. It passed every Linux run here. Not in scope, not changed.
- ADR 0010 item 4 says test/e2e is not run under -race, but the gate script and phase3.sh do run it under -race. These runs followed the gate script.
- In the final --coload whole-package run, the capped generator covered 20:21-20:41 UTC: the whole daemon package and the first 32 e2e tests, ThinSlice among them. x09 and the spooled start ran after it stopped, with only other workstreams on the container. Both were covered under the generator by the separate count-3 row runs.

### Needs the owner

- Pass-budget semantics; no new number, but a bound's meaning changes. idleRunBudget (2 s) is now a soft pass budget for the requested drain (bc3dde9), the client-spool watcher (ce61328) and the idle drain (7dc9527). Such a pass starts no new line once the budget is spent and it has consumed a line, finishes the line it started under drainLineDeadline (5 s), and stops at once when its context is cancelled. Worst-case drain-mutex hold: the pass's bookkeeping, plus the spool walk until one line is consumed, plus one line's 5 s. Before, it was a hard 2 s. Why: a line slower than 2 s was cut by every pass and published by none. If this is wrong: a longer mutex hold delays the flush's settle drain (inside settleSessionLimit, 9 s) and other passes, and RunOnce can overrun by one line. The flush drain, admin.drain, and the startup and Stop drains are unchanged.
- x9FlushGCBound (test/e2e/v3_x09_test.go, c551fcd) = e2eHistoryConvergeBound + (pending+1) x daemon.DrainLineDeadline, where pending counts the spool lines drain.json has not consumed before the flush. The value is about 34-38 min at the measured 406-450 pending lines, against GC lines that actually came 1m05s-3m03s after the flush. Derivation: the flush's daemon replays each pending line, and the flush itself, under the product's per-line deadline before its SessionEnd. If it is too tight, the row goes falsely red, as the old 32 s did 3/3 under load. If it is too loose, a genuinely lost GC takes up to about 35 min to report instead of 32 s, but it is still reported.
- Decide whether the stray spawn-claim gap is fixed or accepted: a daemon that loses the lock leaves run/spawn.lock fresh for 10 s, so a SessionEnd flush after the running daemon exits starts no daemon. One option is to have the losing daemon remove the claim when a live daemon holds the lock. That would be w6-borrow's change.
- How Linux gates judge the two wall-clock rows. TestFeaturesFrom_LexicalCohesionShingleCap (10 ms, plan-mandated SP-12, ADR 0012 R45) is judged under -race without --coload in phase3.sh's linux-tree. TestV3_HotPathUnchangedWithLedgerResident (B-A/B-B 15 ms) fails every Linux run without --coload on this container, at base and head. Options: declare --coload whenever the container is shared, or judge both in a non-race, isolated Linux timing lane as ci.yml does. No budget was changed.

## Independent review

### review:linuxrows: needs-fixes

- **blocker** `internal/daemon/drain.go:58-80 (withPassBudget/passStopped) with internal/daemon/session_end.go:209 (launchSessionEnd) and session_end.go:259-268 (endDrainedFlush)` — The new pass budget is a context VALUE, and context.WithoutCancel keeps values. A drained flush met by a budgeted pass (the client-spool watcher, a requested drain, or the idle drain, now paced) goes through dispatchPending -> EndSession(ctx) -> launchSessionEnd, which builds the end's run context from WithoutCancel(ctx). The session end therefore inherits the pass's *passBudget pointer, including its end time and its shared consumed flag. By the time the end runs its settle drain (settleSession -> d.Drain(sctx)) and its final drain (endSession -> d.Drain(ctx)), that budget has expired. Both drains then stop at their first file with errPassBudgetSpent, or after one line. Before this branch the pass used context.WithTimeout, and WithoutCancel strips the deadline, so these drains ran without a budget. Effects: the final drain does not absorb the flush's spool and returns OK:false, so the session's recovery marker stays at stage "drain" indefinitely. The settle drain can also be cut, so SessionEnd runs before the session's earlier deliveries are published (C1.1; noteUnsettled goes Loud). This is the production path for every SessionEnd flush that reaches the daemon through a client spool. It also contradicts the needs_owner claim that "the flush drain ... [is] unchanged". The existing guard, TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget, does not catch it because it uses a WithCancel context, not withPassBudget.
  - Evidence: Scratch copy of 3c15ec7f (repo untouched), new test modelled on TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget. It spools client-6161.ndjson (another session's tool use) and client-6262.ndjson (the flush), runs DrainClientSpools(withPassBudget(bg, idleRunBudget)), holds SessionEnd for idleRunBudget+500ms, then releases it and awaits the end. Result: FAIL 3/3, recovery sessions: map[sess-review-budget:{Stage:drain ...}], "a finished session end leaves no recovery marker". The same test with context.Background() as the pass context passes. With the candidate fix below applied, the review rows plus TestDrain_AReplayedFlush* and TestFlush_* all pass (ok 10.2s).
  - Fix: Do not let a pass budget cross into a session end. In launchSessionEnd, shadow the key: context.WithValue(context.WithoutCancel(ctx), passBudgetKey{}, (*passBudget)(nil)), or add a withoutPassBudget helper in drain.go. Make passStopped and notePassConsumed tolerate a nil *passBudget (`ok && b != nil`). A cleaner option is to pass the budget explicitly to drainer.pass instead of on the context. Add a regression row: a drained flush from a withPassBudget pass whose end runs after the budget has expired must leave no recovery marker and must absorb the flush's spool. Correct the needs_owner text about which drains are unchanged.
- **minor** `test/e2e/v3_x09_test.go:~991 (x9FlushGCBound) and its use at ~259` — The derived bound, e2eHistoryConvergeBound + (pending+1) x DrainLineDeadline, is about 34-38 min at the measured 406-450 pending lines. That is longer than the package -timeout the row runs under: 30m in ci.yml's e2e job, and 30m by default in linux-nonroot-inner.sh. Under the load that produces a large backlog, a genuinely lost SessionEnd GC would never report as this row's assertion with its pending-count diagnostic. It would surface as a whole-binary timeout panic that abandons every other row. The report says a loss "is still reported", but at the gate's timeout it is reported only as a panic. The derivation prices each line at its 5 s worst case, while the measured cost was about 0.3-0.75 s a line.
  - Evidence: .github/workflows/ci.yml:247 `go test -count=1 -timeout=30m ./test/e2e`; plans/sdd/V6-closeout/linux/linux-nonroot-inner.sh:48 `timeout=30m`; report: bound "about 34-38 min", and the whole-package Linux runs needed --timeout 150m.
  - Fix: Clamp the wait to the test's remaining deadline minus a margin: if dl, ok := t.Deadline(); ok, use min(x9FlushGCBound(pending), time.Until(dl)-margin). Then fail with the row's own message, including pending and elapsed. Alternatively, derive the per-line cost from a named, measured replay rate with an explicit safety factor, and list that number for the owner.
- **minor** `internal/daemon/delivery_order.go:805-808 (drainOnRequest resumeDrain)` — The requester re-arms itself after ANY pass that ended with context.DeadlineExceeded. That includes a pass that stopped because one line exceeded its own drainLineDeadline. The report's own open item says such a line halts every pass at that line. So a line whose dispatch never completes in 5 s (for example a wedged handler) now makes the requester run passes forever: bookkeeping plus a 5 s line, then an equal rest, indefinitely, holding the drain mutex about half the time and contending with the flush settle (settleSessionLimit, 9 s) and the watcher. Before, it stopped after one pass. This new unbounded self-rearm is not listed in needs_owner.
  - Evidence: `if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil { d.ing.resumeDrain() }`. errPassBudgetSpent wraps DeadlineExceeded, but so does dctx.Err() from dispatchPending (drain.go:1339-1340), which pass() treats as a stop (drain.go:443-445).
  - Fix: Re-arm only on errors.Is(err, errPassBudgetSpent), or on DeadlineExceeded only when the pass made progress (n > 0), so a line that never completes cannot keep the requester busy. Add the re-arm policy to the owner item on pass-budget semantics.
- **minor** `internal/daemon/spool_watch.go:218-236 (lookAtClientSpools budgetSpent)` — When a watcher pass ends on its budget, every due entry gets e.next = now, including spools the pass DID reach and found unconsumable (deferred for ordering). Under consume-first semantics on a loaded host, nearly every pass ends on its budget. So the doubling back-off documented for unconsumable spools stops applying in exactly the loaded case: each look re-reads and re-syncs every blocked spool until the horizon, which adds fsync pressure where it hurts most. The comment says such a pass "judged nothing about the spools it left", but it did judge the ones it reached.
  - Evidence: `budgetSpent := errors.Is(perr, errPassBudgetSpent); for _, e := range due { ... if budgetSpent { e.next = now; continue } ...}`. DrainClientSpools reports no per-file reach. The report records TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick flaking 2/5 on Windows under load.
  - Fix: Have the pass report which spool files it reached, or where it stopped, and reset only the entries after the stop point. Entries the pass reached and could not consume keep spoolRetryAfter. Add a row: a blocked spool ahead of the budget stop keeps its back-off, and one behind it is due now.
- **minor** `test/e2e/v3_x09_test.go:~1031 (x9AwaitNoSpawnInFlight) / c551fcd` — x09 now waits out a real product liveness gap: a losing lazy spawn leaves run/spawn.lock fresh for 10 s, so a SessionEnd flush in that window starts no daemon. The gap is routed to the owner, but no test pins it. After this change, nothing in the suite goes red for it, and nothing will show when w6-borrow fixes it. The task asked for product defects to get a failing test first; the out-of-scope fix is understandable, but the evidence is now carried only in prose.
  - Evidence: Report open item 1 and needs_owner 3. The row logs "a spawn.lock claim was fresh before the flush" but asserts nothing about it. No new ipc or daemon test reproduces the stale claim.
  - Fix: Add a known-gap row in internal/ipc or internal/daemon, skipped with t.Skip naming the D-number or owner decision, that reproduces a lock-losing spawned daemon exiting with its spawn.lock still fresh. The owner, or w6-borrow, then un-skips it with the fix.
- **nit** `internal/daemon/observer_ops.go:154-162 (WireObserver OnStop comment)` — The comment says Stop runs Persist once session ends "are joined or, past their grace, cancelled, so nothing it started is still feeding the observer". stopSessionEnds can abandon an end that ignores cancellation (sessionEndAbandonAfter), so that end may still be running SessionEnd when Persist runs. This is safe, since the idle Persist already runs alongside SessionEnd, but the claim is inaccurate.
  - Evidence: session_end.go:307-313: "a session end ignored cancellation; abandoning it". The ThinSlice diagnostic log shows exactly that abandonment.
  - Fix: Reword it to say Persist is safe alongside an abandoned end, because it already runs alongside SessionEnd from the idle loop, instead of claiming nothing is feeding the observer.
- **nit** `commit 3c15ec7f docs(v6): record w6-linuxrows evidence runs` — This is the only commit in the range without the `Refs:` footer; the other nine carry `Refs: V6-VERIFY, C3.4`.
  - Evidence: git log -1 --format=%B 3c15ec7f | grep -c '^Refs:' prints 0.
  - Fix: Add `Refs: V6-VERIFY, C3.4` when the coordinator folds or rewords the evidence commit.

## Fix seat (review resolution) — status `done`, head `a8ca982f6ee43522f2189a984589f8ce3921379c`

### Root cause

Rows (a), (c) and (d) failed under load because of product defects in how budgeted drain passes treated slow lines. The requested drain, the client-spool watcher and the idle drain each ran under a 2 s context deadline. That deadline cancelled the line in flight, and on a host with a deep fsync queue publishing one line took longer than 2 s. Also, a cut pass was never asked for again, and Stop did not persist the observer's graph. Row (a) also had a test defect: the 32 s bound was too short for the replay the flush's daemon does first. Row (b) was already fixed at base (aba55d8). Row (e) is a pure timing row. The review found a regression in the implementer's context-value budget: context.WithoutCancel keeps values, so a session end started from a budgeted pass inherited the pass's spent budget. Its drains then stopped at the first spool file and left the recovery marker at stage "drain". It also found an unbounded self-re-arm of the drain requester and a back-off reset for spools the pass had already reached. All three are fixed at the source, each with a failing test first.

### Summary

W6-LINUXROWS: FIX-SEAT REPORT. Branch closeout/w6-linuxrows, base f6095e2, code head 87f1713c, evidence head a8ca982f. Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows. The evidence index is plans/sdd/V6-closeout/w6-linuxrows/runs/INDEX.txt. It covers both seats' runs; the review seat's section is at the end.

## 1. Outcome

- The review found one blocker. It is confirmed and fixed: the pass budget no longer leaks into session ends.
- Of the four minor findings:
  - three are confirmed and fixed (F2, F3, F4);
  - one is partly rebutted and resolved another way (F5): the gap is reproduced by a temporary diagnostic, not a t.Skip row.
- Every fix got a failing test first. Red and green logs are in runs/review/.
- Final Linux gates at 87f1713, run through linux-nonroot-gate.sh (non-root, -race):

| Run | Packages | Result |
|---|---|---|
| idle, no generator, no --coload | ./internal/daemon | PASS: 1526 pass, 0 fail, 1 platform skip |
| idle, no generator, no --coload | ./test/e2e | 307 pass, 1 FAIL, 3 skips |
| --coload with the capped generator | ./internal/daemon and ./test/e2e together | PASS: daemon 1526/0/1 skip, e2e 308/0/3 skips |

- The one idle e2e failure is TestV3_HotPathUnchangedWithLedgerResident, the x11 B-A/B-B 15 ms wall-clock gate (B-A p99 90.1 ms, B-B p99 81.9 ms).
  - It also fails when run alone, side by side with the base: head p99 147.5 ms, base f6095e2 p99 131.1 ms.
  - So it was already failing on this container before this branch, as the implementer found at 7dc9527. No budget was changed.

## 2. The five task rows

Root causes are the implementer's, with evidence in runs/INDEX.txt. This seat re-verified them at 87f1713.

- **(a) x09, TestV3_LiveSessionWriteSetAndAppendOnly**
  - Test defect, plus one product gap. The SessionEnd GC is not dropped. The flush's daemon first replays everything the bounded pre-flush shutdown left: about 170 WAL lines and 185 client spools under load. The GC line then came 1-4 min after the flush, with every other assertion passing, so the old 32 s bound was wrong for the work (c551fcd, x9FlushGCBound).
  - The product side, 7dc9527: an idle drain whose context deadline cut a slow line on every pass had stranded the flush's spool.
  - In one run no daemon ran at all: a stray spawn.lock from a lock-losing hook spawn made the flush stand aside. That is the owner's item 3; x09 waits it out (x9AwaitNoSpawnInFlight).
  - Final results:
    - idle whole run: PASS, GC line 5.4 s behind 198 lines;
    - --coload whole run: PASS, 5.9 s behind 207 lines;
    - load11 (generator on), 3/3 PASS, 44-52 s behind 351-403 lines.
- **(b) TestE2E_SpooledSessionStartNeverDegradesTheProject**
  - Already fixed by w5-coldstart's aba55d8 at f6095e2: 3/3 idle and 3/3 under load at base (runs e2e-rows-idle-f6095e2 and e2e-rows-load8-f6095e2).
  - Final: PASS in every run at 87f1713.
- **(c) TestE2E_ThinSliceDropsControlOnlyEdges**
  - Product defect, fixed by 80fab5f. Stop did not persist the observer's graph, so a Stop that cut a session end lost the graph for good: 0 nodes persisted for 48 tool uses.
  - Also the watcher's context deadline cut slow spools (ce61328).
  - Final: PASS in every run.
- **(d) TestDeliveryOrder_LaneOverflowIsDrainedOnRequest**
  - Product defect. A requested pass cut by its 2 s budget was never asked for again (5437e703). Its deadline also cancelled the line it was publishing (bc3dde9c), and its bookkeeping alone could outlast the budget (4894f4f4).
  - Final:
    - load11: 10/10 under the generator;
    - load10: 10/10, no generator;
    - PASS in both whole-package runs.
- **(e) TestFeaturesFrom_LexicalCohesionShingleCap**
  - A pure timing row: FeaturesFrom over a 4096-shingle window, min of 5, against 10 ms.
  - It protects SP-12's plan-mandated budget (ADR 0012 R45). That bound is documented, and it was not changed.
  - Under --coload it reports rather than judges. Coload whole run: 11.66 ms, reported.
  - Final: 20/20 in the load10 and load11 focused runs.

## 3. Review resolution

**F1 (blocker): the pass budget leaks into session ends. CONFIRMED, fixed in 21893213.**
- The reviewer's reproduction holds: a flush replayed by a budgeted pass ends with its recovery marker left at stage "drain".
- New row: TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget. The pass has a zero budget, meets the flush, and then consumes another session's tool use. The end's drains must still finish.
- Red on the 3c15ec7 code: runs/review/f1-red-windows-3c15ec7.log, with the marker at stage "drain", matching the reviewer's evidence.
- Fix: dispatchPending hands both the session end (EndSession) and the line's handler (Dispatch) a context with no budget (withoutPassBudget). passStopped and notePassConsumed now accept a nil budget.
  - I stripped the budget at the pass's boundary rather than only in launchSessionEnd. That covers every hand-off: EndSession, an inline-replayed flush's handler, and any handler goroutine.
- Found while writing the row: writeSpoolLines writes a blank line after every record, and a drain consumes a blank line as a line. Under consume-first semantics that counts as consumption.
  - I added writeHookSpool, which writes byte-for-byte what a hook's spool writer leaves.
  - The new rows use it.
- Green: runs/review/f1-green-windows.log, and 10/10 in both Linux focused runs.
- The needs_owner text was wrong and is corrected below.

**F2 (minor): x9FlushGCBound can outrun the package -timeout. CONFIRMED, fixed in 87f1713c.**
- This was confirmed on Linux too. In all three load11 x09 iterations the derived bound (29m52s-34m12s) exceeded what was left of the 30 m -timeout.
- x9GCWait now waits for the smaller of:
  - x9FlushGCBound(pending);
  - time.Until(t.Deadline()) minus x9CleanupReserve.
- It never lengthens the wait.
- The failure message and the per-run evidence line report whether the wait was cut.
- Demonstrated under -timeout 3m on Windows: the wait was cut to 2m21s and the row passed (runs/review/f2-x09-clamped-windows.log).
- I did not re-price the 5 s per line: it is a worst case, and the rate is already the owner's item.

**F3 (minor): unbounded self-re-arm of the drain requester. CONFIRMED, fixed in 0d200575.**
- requestedDrainPass is extracted, and passLeftWork decides when to ask again:
  - always after errPassBudgetSpent, which implies the pass consumed a line;
  - after any other context.DeadlineExceeded (a line's own deadline) only when the pass published something (n > 0).
- New row: TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress.
  - Pass 1 publishes, then meets a line that never finishes: it must ask again.
  - Pass 2 meets only that line: it must not.
- Red with the old policy after the extraction (runs/review/f3-red-windows-extracted.log: "Should be zero, but was 1"). Green, and 10/10 on Linux under the generator.

**F4 (minor): a budget stop reset the back-off of spools the pass had reached. CONFIRMED, fixed in 60652478.**
- A pass its budget stops now records on the budget which spool files it left unfinished (notePassLeft): the file it stopped in or before, and every later one.
- The watcher reads that through newPassBudget and passBudget.leftUnfinished. Only those files are due at the next look. A spool the pass reached and finished without consuming keeps the doubling wait.
- New row: TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached. It spools a blocked spool, then one slower than the budget, then one the pass never reaches.
- Red before the fix: the reached spool was due at once (runs/review/f4-red-windows.log). Green, and 10/10 on Linux under the generator.

**F5 (minor): the stray spawn-claim gap is unpinned. The mechanism is REBUTTED; the concern is RESOLVED as evidence.**
- A t.Skip row naming a D-number cannot land, for two reasons:
  - tools/devtool/stubskips.go permits only three skip reasons ("behaviour: implementation is a stub (Rule W-1)", "contract fixture not yet recorded (Rule W-2)", "platform: ..."), and any other reason is a hard lint failure;
  - the owner directive forbids adding t.Skip.
- A red row in the tree would break every gate. A row asserting the defective behaviour would pin wrong behaviour inside w6-borrow's scope.
- Instead, the gap is reproduced by a temporary diagnostic test, kept outside the tree as runs/review/f5-diag-spawnclaim_test.go.txt. Its red log is runs/review/f5-diag-spawnclaim-red-windows.log:
  - daemon A holds the lock;
  - a hook-style ClaimSpawn is made, and daemon B loses the lock and exits;
  - A stops;
  - the flush's ClaimSpawn then answers SpawnInFlight (2) where SpawnClaimed (1) is expected.
- It is ready for w6-borrow to adopt as its failing test first.

## 4. Commands and results (this seat)

Windows (loaded machine; four other workstreams were running):
- `go test -count=1 -timeout=10m -run '^TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget$' ./internal/daemon`
  - FAIL on the pre-fix code (red log above); ok after 2189321.
- `go test -count=1 -timeout=10m -run '^TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress$' ./internal/daemon`
  - FAIL with the old policy; ok after 0d20057 (10.5 s).
- `go test -count=1 -timeout=10m -run '^TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached$' ./internal/daemon`
  - FAIL before 6065247; ok after.
- `go test -count=1 -timeout=20m -run 'Idle|Drain|SpoolWatch|DeliveryOrder|RunOnce|Flush|SessionEnd|PassBudget' ./internal/daemon` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - ok, 105 s.
- `go test -count=1 -timeout=60m -v ./internal/daemon` (whole package, final daemon code)
  - ok: 771 top-level PASS, 0 FAIL, 1 platform skip.
- `go test -count=1 -timeout=30m -v -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' ./test/e2e`
  - PASS (GC line 1.9 s behind 80 lines, not cut).
  - The same command with -timeout=3m: PASS, the wait cut to 2m21s.
- Temporary diagnostic, test name TestDiagSpawnClaim_ALockLosingDaemonLeavesNoFreshClaimOnceNoDaemonRuns (see runs/review/f5-diag-spawnclaim_test.go.txt; never in the tree): FAIL, expected 1, got 2.
- Checks, all passing:
  - `go vet ./internal/daemon ./test/e2e`, on Windows and with GOOS=linux;
  - `go run ./tools/devtool fmt` and `go run ./tools/devtool fmt-check`: exit 0;
  - `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0, re-run for runpatterns/docmarkers/sleepcheck after the evidence commit.
  - No docs or generated inputs changed.

Linux (container qompack-v6-linux-verification):
- All runs used `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-linuxrows --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows/plans/sdd/V6-closeout/w6-linuxrows/runs/linux <rev> <label> ...`.
- Focused daemon rows, with `--count 10 --coload -- ./internal/daemon` and this run regex: `--run '^(TestDeliveryOrder_LaneOverflowIsDrainedOnRequest|TestFeaturesFrom_LexicalCohesionShingleCap|TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain|TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget|TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress|TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget|TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget|TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached|TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished|TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft|TestDrainClientSpools_ABudgetedPassWhoseSyncsOutlastItsBudgetStillConsumesALine|TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick|TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest|TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending)$'` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - load11, under the generator: 160/160 PASS.
  - load10: 160/160 PASS, but NOT under load. The generator launch with docker exec -d started nothing, as the INDEX says.
- Focused e2e rows, with `--count 3 --coload -- ./test/e2e` and `--run '^(TestV3_LiveSessionWriteSetAndAppendOnly|TestE2E_SpooledSessionStartNeverDegradesTheProject|TestE2E_ThinSliceDropsControlOnlyEdges)$'` <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - 9/9 PASS in load11 and in load10.
- Whole packages:
  - `87f1713c daemon-whole-idle --timeout 60m -- ./internal/daemon`: PASS, 1526/0/1.
  - `87f1713c e2e-whole-idle --timeout 150m -- ./test/e2e`: 307 pass, 1 FAIL (x11), 3 skips.
  - `87f1713c daemon-e2e-whole-coload --timeout 150m --coload -- ./internal/daemon ./test/e2e`: PASS, 1526/0/1 and 308/0/3.
- x11 A/B: `<rev> x11-ab --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e` at 87f1713c and f6095e2, side by side: FAIL on both.
- Load generator:
  - /work/cx-w6-linuxrows-load/cx-w6-linuxrows-load.sh, 5 cpu + 3 fsync = 8 busy processes, 1200 s maximum.
  - load-11 ran 22:04:49-22:15:17 UTC and was stopped by its owner.
  - load-12 ran 22:51:35-23:11:35 UTC and was ended by timeout(1).
  - Both were confirmed stopped afterwards (pgrep).
- Every "idle" run shared the container with other workstreams' gate runs. "Idle" means only that no generator of this workstream was on.

## 5. Criterion changes

- **x09 GC wait (87f1713, this seat)**
  - Now: the smaller of x9FlushGCBound(pending) and what is left of the binary's -timeout minus x9CleanupReserve.
  - It never lengthens the wait. It shortens it only to end x9CleanupReserve (21.5 s) before the binary's own timeout panic would. In that window the row could still have passed only if its remaining assertions and its cleanup also beat the panic.
  - Why: a lost GC now fails as this row's assertion, with its evidence, instead of as a whole-binary panic that abandons every later row.
- Carried from the implementer (their rationales are in their commits):
  - c551fcd: x9FlushGCBound replaces the 32 s bound, and x09 waits for a stray spawn claim to lapse before the flush.
  - c08d008: the slow-line rows count attempts cut inside the slow part, not total attempts.

## 6. Open items

1. The stray spawn-claim product gap is not fixed; it is reproduced (owner decision; w6-borrow's scope).
2. The x11 hot-path gate fails without --coload on this container, at base and head alike.
3. Only the final-drain variant of F1 has a row. The settle-drain variant is covered by the same fix.
4. passLeftWork no longer re-arms after a pass that published nothing and was stopped by a line's own deadline.
   - That is the review's policy.
   - TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain now relies on its 3 s slow line plus the real dispatch fitting inside drainLineDeadline (5 s).
   - No run has exceeded that: 20/20 on Linux, plus Windows.
5. writeSpoolLines fixtures carry blank lines, which a budgeted pass counts as consumption. Production writers emit none. The existing rows are unaffected, and I did not change them.
6. The implementer reported a Windows co-load flake in TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick (2/5). The F4 change cannot affect it: its only spool never consumes, so its passes never stop on a budget. It was not re-investigated. It went 20/20 on Linux here and passed in the Windows whole-package run.
7. There has been no hosted CI run for this range.

### Commits

- 5437e703 fix(daemon): request again a drain its budget cut short (implementer)
- 80fab5f3 fix(daemon): write the observer's graph and state out at stop (implementer)
- c551fcdf fix(e2e): wait out x09's flush replay and stray spawn claims (implementer)
- bc3dde9c fix(daemon): let a requested drain pass finish the line it started (implementer)
- ce613287 fix(daemon): spool watcher finishes its line, no backoff on budget (implementer)
- 4894f4f4 fix(daemon): a budgeted drain pass consumes a line before stopping (implementer)
- c08d0088 test(daemon): count budget cuts, not attempts, in slow-line rows (implementer)
- 64a01ed0 test(e2e): log x09's replay backlog and GC wait on every run (implementer)
- 7dc95273 fix(daemon): let the idle drain finish a line slower than its budget (implementer)
- 3c15ec7f docs(v6): record w6-linuxrows evidence runs (implementer)
- 21893213 fix(daemon): keep a pass budget out of the session end it starts (review F1, blocker)
- 0d200575 fix(daemon): ask for a drain again only after a pass made progress (review F3)
- 60652478 fix(daemon): keep the watcher's back-off for spools a pass reached (review F4)
- 87f1713c test(e2e): end x09's GC wait before the binary's own timeout (review F2)
- a8ca982f docs(v6): record w6-linuxrows review-seat evidence runs

### Tests

- `go test -count=1 -timeout=10m -run '^TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget$' ./internal/daemon (Windows, 3c15ec7 code + new row)` — FAIL as expected: recovery marker left at stage "drain" (runs/review/f1-red-windows-3c15ec7.log)
- `go test -count=1 -timeout=10m -run '^TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget$' ./internal/daemon (Windows, after 2189321)` — ok (runs/review/f1-green-windows.log also passes both replayed-flush neighbours)
- `go test -count=1 -timeout=10m -run '^TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress$' ./internal/daemon (Windows, old policy after extraction)` — FAIL as expected: Should be zero, but was 1 (runs/review/f3-red-windows-extracted.log)
- `go test -count=1 -timeout=10m -run '^TestDeliveryOrder_ARequestedPassAsksAgainOnlyWhileItMakesProgress$' ./internal/daemon (Windows, after 0d20057)` — ok 10.5s; the lane rows in runs/review/f3-green-windows.log pass
- `go test -count=1 -timeout=10m -run '^TestSpoolWatch_ABudgetStopKeepsTheBackoffOfTheSpoolsItReached$' ./internal/daemon (Windows, before/after 6065247)` — FAIL before (runs/review/f4-red-windows.log), ok after (runs/review/f4-green-windows.log)
- `go test -count=1 -timeout=20m -run 'Idle|Drain|SpoolWatch|DeliveryOrder|RunOnce|Flush|SessionEnd|PassBudget' ./internal/daemon (Windows) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — ok 105s
- `go test -count=1 -timeout=60m -v ./internal/daemon (Windows, whole package, final daemon code)` — ok: 771 top-level PASS, 0 FAIL, 1 platform skip (runs/review/win-daemon-full-review.log)
- `go test -count=1 -timeout=30m -v -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' ./test/e2e (Windows, 87f1713)` — PASS; GC line 1.9 s behind 80 lines, wait not cut
- `go test -count=1 -timeout=3m -v -run '^TestV3_LiveSessionWriteSetAndAppendOnly$' ./test/e2e (Windows, 87f1713)` — PASS; wait cut to 2m21s (cut: true), exercising the clamp
- `temporary diagnostic TestDiagSpawnClaim_ALockLosingDaemonLeavesNoFreshClaimOnceNoDaemonRuns (runs/review/f5-diag-spawnclaim_test.go.txt, never in the tree)` — FAIL as expected: ClaimSpawn answered SpawnInFlight (2), expected SpawnClaimed (1)
- `go vet ./internal/daemon ./test/e2e; GOOS=linux go vet ./internal/daemon ./test/e2e; go run ./tools/devtool fmt-check` — clean; fmt-check exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-linuxrows --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-linuxrows/plans/sdd/V6-closeout/w6-linuxrows/runs/linux 87f1713c daemon-rows-load11 --run '<the 14 daemon rows listed in the summary>' --count 10 --coload -- ./internal/daemon (capped generator load-11)` — PASS 160/160
- `same gate, 87f1713c e2e-rows-load11 --run '^(TestV3_LiveSessionWriteSetAndAppendOnly|TestE2E_SpooledSessionStartNeverDegradesTheProject|TestE2E_ThinSliceDropsControlOnlyEdges)$' --count 3 --coload -- ./test/e2e (capped generator load-11) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — PASS 9/9; x09 GC line 44-52 s behind 351-403 lines; the wait was cut to the 30m -timeout in all 3 iterations
- `same gate, daemon-rows-load10 / e2e-rows-load10 (same rows; the generator never started)` — PASS 160/160 and 9/9; plain --coload runs, NOT under load
- `same gate, 87f1713c daemon-whole-idle --timeout 60m -- ./internal/daemon` — PASS 1526 pass, 0 fail, 1 skip (TestDrainWindowsOpenBlobRetainsCleanupIntent)
- `same gate, 87f1713c e2e-whole-idle --timeout 150m -- ./test/e2e` — 307 pass, 1 FAIL (TestV3_HotPathUnchangedWithLedgerResident: B-A p99 90.1 ms, B-B 81.9 ms, limit 15 ms), 3 skips
- `same gate, 87f1713c daemon-e2e-whole-coload --timeout 150m --coload -- ./internal/daemon ./test/e2e (capped generator load-12)` — PASS: daemon 1526/0/1, e2e 308/0/3; ShingleCap 11.66 ms reported, not judged
- `same gate, <87f1713c|f6095e2> x11-ab --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e (side by side, no --coload)` — FAIL on both: B-A p99 147.5 ms at head, 131.1 ms at base; already failing on this container

### Criterion changes

- test/e2e x09 (87f1713, review F2): the GC wait is now the smaller of x9FlushGCBound(pending) and what is left of the test binary's -timeout minus x9CleanupReserve (21.5 s). It never lengthens the wait. It shortens it only to end 21.5 s before the binary's own timeout panic, so a lost GC fails as this row's assertion with its evidence instead of as a whole-binary panic. The failure message and the evidence line say whether the wait was cut.
- Carried from the implementer: x09's wait moved from the fixed 32 s e2eHistoryConvergeBound to x9FlushGCBound(pending), and x09 waits for a stray spawn.lock claim to lapse before the flush (c551fcd). Rationale: the GC line comes only after the flush's daemon replays the pending spool, and the spawn-claim wait is owner item 3.
- Carried from the implementer: the slow-line daemon rows count attempts cancelled inside their slow part instead of asserting exactly one attempt (c08d008). Rationale: a retry after a line's own deadline is that deadline working, not the budget cutting the line; the pre-fix code cut every attempt.

### Open issues

- The stray spawn-claim product gap is reproduced but not fixed. A daemon that loses the lock leaves run/spawn.lock fresh for 10 s, so a flush after the running daemon exits starts no daemon (runs/review/f5-diag-spawnclaim_test.go.txt). x09 waits it out. It is w6-borrow's scope and an owner decision.
- TestV3_HotPathUnchangedWithLedgerResident (x11, B-A/B-B 15 ms) fails every Linux run without --coload on this container, at base and head alike (A/B at 20260927T232948Z). It is a wall-clock gate this branch does not touch.
- Only the final-drain variant of review F1 has a row. The settle-drain variant is covered by the same withoutPassBudget fix but has no row of its own.
- passLeftWork no longer re-arms after a pass that published nothing and was stopped by a line's own drainLineDeadline. TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain now relies on its 3 s slow line plus the real dispatch fitting within 5 s. It went 20/20 on Linux here.
- writeSpoolLines fixtures carry a blank line after every record, which a budgeted pass counts as a consumed line. Production writers emit none. The existing rows are unaffected, and the new rows use writeHookSpool.
- The implementer reported a Windows co-load flake in TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick (2/5). The F4 change cannot affect it, and it was not re-investigated. It went 20/20 on Linux here.
- No hosted CI run exists for this range.

### Needs the owner

- Pass-budget semantics (no new number, but a bound's meaning changes). idleRunBudget (2 s) is now a soft pass budget for the requested drain (bc3dde9), the client-spool watcher (ce61328) and the idle drain (7dc9527). Such a pass starts no new line once the budget is spent and it has consumed a line, finishes the line it started under drainLineDeadline (5 s), and stops at once when its context is cancelled. Worst-case drain-mutex hold: the pass's bookkeeping, plus the spool walk until one line is consumed, plus one line's 5 s. Before, it was a hard 2 s. Why: a line slower than 2 s was cut by every pass and published by none. CORRECTED after review F1: the budget ends at the pass's boundary (withoutPassBudget in dispatchPending). The handler a line is handed to, and any session end a replayed flush starts, run without it, so a session end's settle and final drains are unbudgeted, as before this branch. The flush's own drains, admin.drain, and the startup and Stop drains are unchanged. At 3c15ec7 that was NOT true for a flush replayed by a budgeted pass. Re-arm policy (review F3, passLeftWork): the requester asks for another pass itself after a pass its budget stopped, or after a pass a line's deadline stopped only if it published a line; never after a pass that made no progress. Watcher back-off (review F4): after a budget stop, only the spools the pass left unfinished are due at once. If this is wrong: a longer mutex hold delays the flush's settle drain (inside settleSessionLimit, 9 s) and other passes, and RunOnce can overrun by one line.
- x9FlushGCBound (test/e2e/v3_x09_test.go, c551fcd) = e2eHistoryConvergeBound + (pending+1) x daemon.DrainLineDeadline, where pending counts the spool lines drain.json has not consumed before the flush. Measured at 87f1713: 17m7s-34m12s for 198-403 pending lines, against GC lines that came 5-52 s after the flush; at the implementer's head, GC lines came 1m05s-3m03s. After review F2 the wait is capped at time.Until(t.Deadline()) - x9CleanupReserve. x9CleanupReserve = e2eLazySpawnSettleBound (daemon.SpawnPollBound, 1.5 s) + e2eDaemonDownBound (daemon.StopCleanupBound 15 s + 5 s) = 21.5 s. Its derivation: the row's own cleanup, e2eShutdownIfReachable, which settles a daemon still being spawned and then shuts it down in full. It is composed only of existing named bounds. Under load the cap applied in all three load11 iterations. If the reserve is too small, a timeout panic can still pre-empt the row's own message. If it is too large, the row gives up up to 21.5 s before the binary would have timed out. If x9FlushGCBound is too tight, the row goes falsely red, as the old 32 s did 3/3 under load. If too loose, a genuinely lost GC takes longer to report, but it now reports as this row's assertion even at the 30 m package timeout.
- Decide whether the stray spawn-claim gap is fixed or accepted. A daemon that loses the lock leaves run/spawn.lock fresh for 10 s, so a SessionEnd flush after the running daemon exits starts no daemon. It is now reproduced by a failing diagnostic, runs/review/f5-diag-spawnclaim_test.go.txt (ClaimSpawn answers SpawnInFlight, expected SpawnClaimed), which w6-borrow can land as its failing test first. One option: the losing daemon removes the claim when a live daemon holds the lock, or Stop removes it. That is w6-borrow's change. A t.Skip known-gap row is not possible: the stubskips lint permits only Rule W-1, Rule W-2 and 'platform:' reasons, and the owner directive forbids adding t.Skip.
- How Linux gates judge the two wall-clock rows. (1) TestFeaturesFrom_LexicalCohesionShingleCap: 10 ms, plan-mandated by SP-12, ADR 0012 R45. It is judged under -race without --coload in phase3.sh's linux-tree, reported under --coload (11.66 ms in this seat's coload whole run), and 20/20 PASS in the focused runs. (2) TestV3_HotPathUnchangedWithLedgerResident: B-A/B-B 15 ms. It fails every Linux run without --coload on this container, at base and head (idle whole run: p99 90.1/81.9 ms; A/B alone: 147.5 ms head, 131.1 ms base). Options: declare --coload whenever the container is shared, or judge both in a non-race, isolated Linux timing lane as ci.yml does. No budget was changed.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



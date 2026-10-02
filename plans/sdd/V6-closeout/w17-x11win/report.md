# Wave 17 x11win (D53(d) investigation)

Branch `(none: investigation on a detached worktree at 99d0b18)`. Workflow `wf_bd12d061-33a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `99d0b18cf7d5991aa733e70ce52a542b80aa7710`

### Root cause

X11's no-ledger arm fails the same absolute B-A/B-B gates C5.1 gates, and trips the §8.1 detector at exactly 3×512 samples. The cause is a disk-flush latency tail inside the daemon's durable ingest (B-B p99 57-82 ms, while client-side observed p99 stays 6-12 ms), not a per-request cost from the code or corpus. Byte-identical code passed and failed both rows within 90 minutes, and the per-request path is unchanged since the last strict X11 passes (w12). The failures cluster inside or right after the e2e package. Which host factor drives the tail (near-full shared NVMe after sustained writes, WSL2 VM writes, or power/CPU state) is still undetermined; experiment E1/E2 decides it.

### Summary

W17 X11WIN: why X11 fails on this Windows laptop (D53(d)). Investigation only. No product, test or bundle bytes changed, and nothing was committed.

CLASSIFICATION: ENVIRONMENTAL. It is host state, specifically a disk-flush latency tail inside the daemon's durable ingest. It is not a product defect and not an X11 harness or corpus defect. Which host factor causes the tail is still UNDETERMINED; experiment E1/E2 below settles it.

Decisive evidence:
1. The same code passed and failed within 90 minutes, in both rows.
   - The pre-freeze head 61b0cd66 and candidate 6 (99d0b18) have no Go or product difference (`git diff --stat 61b0cd66 99d0b18 -- '*.go'` is empty).
   - X11's no-ledger arm passed in the pre-freeze run at about 21:40 EDT (B-A p99 41.0, B-B p99 28.7, 0 deferred). It failed overnight at about 22:40 and 22:58.
   - The integration hot-path row failed in the pre-freeze run at 21:54 (593 deferred). It passed overnight at 22:20.
2. In every failure the tail sits in B-B (the daemon's fsync region), not on the client side.
   - c6 X11-alone: observed p99 11.3 ms, but B-B p99 81.9 ms.
   - The client-side observed latency starts inside the spawned hook, so that is where spawn or CPU contention would show. It stays at 6-12 ms, the same as in passing runs.
3. The spawn floor does not predict the outcome.
   - The pre-freeze integration run failed at a floor of 15.1 ms, about the quiet 14.3.
   - w12's X11 passed twice at floors of 23.3-24.5 ms.
4. The per-request delivered path has not changed since the last strict X11 passes (w12, 2026-09-29, at deca6325/26a96f70).
   - `git log 26a96f70..99d0b18` over ingest.go, groupcommit.go, delivery_*.go, internal/ipc, internal/cli/hookclient.go and test/bench/hotpath shows only tests, a spool-only change (57b7a4b3), startup lock code and the waiver notes.

(a) What the no-ledger arm gates, and how it differs from TestIntegration_HotPathWarmWithRealResidentState

The harness invocation is identical in both rows:
- `bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json <f> --project <root>`, with no --under-coload.
- Same daemon defaults. Neither test writes `.qompack/config.json`, and HOME is a temp directory.
- On Windows: `runtime.hotPath.budgetMs` 50 (D41: max(15, L0IngestMsWindows 50)), breachWindows 3, spoolOnBreach true, l0IngestMs 50, ackDeadlineMs 73.
- Same detector. Same populations: 2000 spawns, 64 warm-up hot requests, 1936 admin.ping, 200 spawn-floor runs, 50 checkpoints, 64+2 ack-rtt requests.
- The B-A loop's payload is the same fixed small Read of src/main.go (payload.go observeRTTEvent) as the integration row and as C5.1.
- So the task's suggested experiment "the no-ledger arm with the integration row's configuration" has nothing to swap.

What actually differs is the project and what runs before the harness:

| | X11 | integration row |
|---|---|---|
| How the corpus is built | Real observer over store.Open/dag.Open, no daemon | In-process daemon's bound ObserveTool (16 over the wire, the rest direct) |
| Corpus | 2000 Reads, 40 paths × 21.5 KB; RawBytes 43.1 MB, stored 3.68 MB, dedup 11.71; DAG 9041 nodes / 17920 edges | 2000 events plus symbol enrichment |
| Sketches | No-ledger twin: copied before the ledger, minus logs; no eliminations.jsonl, no tried.bloom | Seeded 1024-entry tried.bloom; touch.cms and explore.hll persisted by a clean daemon stop |
| Work before the harness | About 6-8.5 min of fsync-bound work: 5000 IngestMCP calls (each syncs evidence and the log line, negknow/durable.go) and RebuildBloom, after the copy | About 1-2 min |

Strict gating:
- X11 first requires bench-hotpath to exit 0 (v3_x11_test.go:605). Only then do x11RequireAbsoluteRows run: no delivery-ledger note at all, B-A, B-B and B-E wall gated, B-E_cpu, B-D and the spawn estimate reported. Then the ledger arm runs, then the D42 pair.
- The integration row gates the same harness exit, plus a population identity and a ban on spool submode in isolation (hotpathJudgeSpool: no hot=1, no WARN, no LOUD).

What B-A contains (internal/daemon/handlers.go recordHotPathSample):
- B-A = (recvTS − req.TS) + (handler end − recvTS) + hotPathTailAllowance, all in whole milliseconds.
- req.TS is the hook's first statement (hookclient.go:322, "FIRST statement — the B-A origin"). That is after process creation and Go runtime start, so the spawn floor (the whole lifetime of a `qompack version` process) is not inside B-A. The spawn floor sits in B-D.
- The handler part covers admission and scope checking, EncodeRequest, and ing.Accept. ing.Accept is B-B: the WAL fsync, the lease-journal fsync, and the position sidecar via WriteAtomic (temp fsync plus directory fsync).

hotPathTailAllowance:
- It is `1 * time.Millisecond` (internal/daemon/budget.go:14-18): an estimate of the ACK-read and exit tail after the handler. Its own comment says it is "neither a measured tail nor a guaranteed upper bound". It has no derivation beyond the §2.4 estimate.
- It does not explain why B-A p50 is 32.8 ms while observed p50 is 6.1 ms. That 26 ms gap is the pre-ACK handler: B-B p50 is 15.4 ms, plus about 10 ms of admission, encode and millisecond truncation (estimated from 12.5%-wide histogram buckets, so approximate).
- A slow spawn floor does not raise B-A directly. Host contention raises the floor, observed and the handler together.

(b) Has X11 ever passed strict on Windows?

Close-out: yes, twice, both measured lines saying "gated":
- w12-x11pair/runs/e2e-x11-isolated-windows.log: PASS in 668.51 s.
  - No-ledger arm: floor 24.0, B-A p99 36.9, B-B p99 18.4, 0 deferred.
  - Ledger arm: B-A p99 28.7, B-B p99 13.3.
  - "X11 pair (gated: PASS)": 6.144 vs 6.144 ms, ceiling 7.680.
- fix-e2e-x11-isolated-windows.log: PASS in 628.13 s. No-ledger B-A p99 30.7 and B-B 13.3; ledger arm 28.7 and 13.3; pair PASS.
- w11 (before D42): all three harness runs passed strict (B-A p99 24.6 / 24.6 / 28.7, 0 deferred). The test failed only on the V2-relative ceiling of the time.

Never passed:
- Before D41, at a 15 ms B-A limit, it could not pass: B-A p50 was 15-16 ms.
- V5 never passed it. It was in the baseline failing set; one F3 run was on battery.

Integration row:
- Its w11 strict passes show n=2064 with no delivery-ledger note, so 0 deferred.
- The c5 and c6 p3-win-timing passes were not run with -v, so no numbers survive. A strict pass is impossible with a spool transition, though, so those passes had no 593-style deferral.

(c) What the detector compares, and why the first three windows breach

- Limit: 50 ms on Windows. Statistic: nearest-rank p99 of each closed 512-sample ring of B-A estimates, index ceil(0.99×512)−1 = 506, which is the 6th-largest sample. A window breaches if p99 ≥ limit, so 6 or more samples ≥ 50 ms. Three consecutive breaching windows switch to spool.
- One daemon-wide ring, reset only by session.start. The harness never sends session.start, so window 1 is the 64 warm-up samples plus the first 448 spawns.
- The switch happens exactly at sample 1536. state.bin then says hot=spool and the daemon NAKs; every later hook spools without dialing. Under D44 the submode never recovers within the session, so 528 gated plus 65 tranche requests always defer: 593.
  - 593 is therefore binary (the detector tripped at window 3), not a measure of how slow the host was.
  - It also explains the hook_ack_rtt p99 of 0.567 ms, and why B-E wall p95/p99 reaches 757-786 ms: the checkpoints pay the precompact settle in spool submode.
- All three windows breach because the host condition was already present before the B-A loop started (the spawn floor measured just before was 2x) and lasted throughout. The delivered B-A p95 is 41 ms and p99 98 ms, so every window has well over 6 samples above 50 ms. B-B p99 alone is 81.9 ms, above 50.
- The per-request cost is not the cause:
  - The durable path does not grow with store size.
  - The payload is a fixed, deduplicated Read.
  - At quiet, B-B p50 is 9.2 ms in both X11 (w12) and the integration row (w11).
- The one store-size-dependent change since w12 is that the startup publication pass now continues in the background (5b8f1e1a, paced by D51). Candidate 6 code passed the X11 no-ledger arm (pre-freeze) and the integration row (overnight) with it present, so it is not decisive. E3 tests it anyway.
- Host-side evidence: X11's Phase A, which is fsync-bound, took 511 s overnight against 347 s pre-freeze and 391-431 s in w12. The e2e package took 1529 s overnight against 1255 s pre-freeze.
- Host facts, read-only, about 00:30 EDT: C: is 93% full (1772 GB used, 134 GB free) on a Samsung MZVL22T0HBLB NVMe. That disk also holds Docker's docker_data.vhdx (288 GB). CPU is a Core Ultra 7 155H (P, E and LP-E cores), power plan Balanced, on AC. The chain's keep-awake holds only ES_SYSTEM_REQUIRED.

(d) Which gate fails

- X11 fails the same absolute gates as C5.1: B-A p99 < 50 and B-B p99 < 50, plus the rule that undelivered samples count as over budget. It does not fail its own D42 criterion.
- It goes further: in candidate 6 the delivered-only B-B p99 (81.9 and 57.3 ms) breaks the B-B gate even with no detector involved.
- Differences from C5.1: 2000 iterations instead of 5000, a 40 MB corpus project, and its place in the chain.
- The test aborts at the first arm (v3_x11_test.go:605, reached from :511), so the ledger arm and the ratio never run.
- The ratio passed in both w12 runs. It was never computed in c5 or c6. In the pre-freeze run it failed at the ledger arm (:513). Computed from that run's artifacts: base 3.072 ms, ledger 4.096 ms, ceiling max(3.840, 4.096) = 4.096, n 2064 = 2064. It would have passed exactly at the ceiling.

Position in the chain matters:
- Every failure ran inside or right after the roughly 25-minute test/e2e package: c5 X11 in e2e; c6 X11 in e2e; c6 X11-alone immediately after e2e; pre-freeze integration row immediately after e2e.
- The integration row passed when it ran first (c5, c6).
- D53(d) asked for X11 re-run alone before code is blamed. The c6 X11-alone run did not meet that precondition: it inherited the post-e2e host state (floor 2.1x).

(e) Decisive experiment, for the coordinator to run after chain.log says "quiet exit=" or "done"

The script is at C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w17/x11win/x11-decisive.sh. Syntax-checked, not run.

Command: `sh x11-decisive.sh C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-x11win <out>`. That worktree is a clean checkout at 99d0b18.

The script:
- unsets QOMPACK_UNDER_COLOAD and QOMPACK_NONREFERENCE_DISK;
- takes a read-only host snapshot before each run (docker ps, power, memory, vmmem and qompack processes, C: free space);
- logs bounded typeperf counters during each run (disk sec/write and transfer, queue length, % processor performance, available memory).

Phase E1, after at least 20 minutes with no test I/O:
1. ctl-a: `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json <out>/ctl-a.json`
2. x11-a: `go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e`
3. int-a: `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration`
4. ctl-b: the control again.

Phase E2, straight after with no gap:
1. e2e-b: `go test -count=1 -timeout=30m ./test/e2e`
2. int-b: the integration row again.
3. ctl-c: the control again.

How to read the results:
- E1 all green (0 deferred, X11 PASS including the pair) and E2 red, with B-B p99 above about 30 ms, observed p99 at 12 ms or less, and raised disk write latency:
  - The cause is environmental: the after-effect of sustained I/O on this host's 93%-full shared NVMe, possibly together with the WSL2 VM's writes.
  - x11-a is X11's Windows evidence.
  - Change the chain so X11-alone runs before the e2e package, or after an idle gap. No code change.
- ctl-a, int-a and ctl-b pass but x11-a fails: the cause is specific to X11. Run phase E3 (`abba` with OLD=<a checkout at deca6325>): X11 alone at deca6325 vs 99d0b18 in an A-B-B-A order.
  - Only 99d0b18 fails: a regression in 26a96f70..99d0b18. The background publication pass (5b8f1e1a) is the first suspect. That makes it a product defect, for wave 18.
  - Both fail: X11's harness or corpus interaction.
- ctl-a itself fails: the host is not quiet, so the result is undetermined. The counters show whether flush latency or CPU performance is the cause. Retry, recording the Docker state, and never stop the owner's containers.

Changes: none. Worktree clean at 99d0b18. Scratch files only: parse.py, table.txt (every harness run in the evidence), unit-percentile.log, x11-decisive.sh.

Commands run:
- `python parse.py <V6-closeout>` over every log containing a harness summary.
- git log / git diff / git merge-base (read only).
- Read-only Get-PhysicalDisk, Win32_Processor, Win32_Battery, Win32_OperatingSystem, powercfg /getactivescheme, Get-PSDrive.
- The three unit tests listed under tests, run by exact name in the -race window.

No criterion changes.

### Tests

- `go test -p 2 -count=1 -v -run '^TestPercentileDurationP99$' ./internal/daemon` — PASS (0.327s): pins that a window's p99 is its 6th-largest of 512 samples
- `go test -p 2 -count=1 -v -run '^TestBreachDetectorTransitionsAfterThreeWindows$' ./internal/daemon` — PASS (0.265s): three breaching 512-sample windows move to spool on the third
- `go test -p 2 -count=1 -v -run '^TestDefaults_HotPathBudgetPerPlatform$' ./internal/config` — PASS (0.888s): windows budgetMs 50, linux 15, darwin 40
- `sh -n scratchpad/w17/x11win/x11-decisive.sh` — syntax ok (not executed)

### Open issues

- D53(d) is still open. The c6 X11-alone run started immediately after the e2e package, so it did not test X11 on a quiet host. Run E1/E2 (x11-decisive.sh) after chain.log says 'quiet exit=' or 'done'.
- Chain order: every Windows hot-path failure since c5 ran inside or right after the ~25-minute test/e2e package; the integration row passed whenever it ran first. Until E1/E2 shows otherwise, run X11-alone before the e2e package or after an idle gap, and take host counters.
- The reference host's C: is 93% full (134 GB free of 2 TB) and shares the SSD with Docker's 288 GB docker_data.vhdx. Both may widen the fsync tail that B-B and the detector see. Freeing space is the owner's call; record it next to any quiet reference run.
- If E1 shows x11-a failing while the controls and int-a pass, run E3 (A-B-B-A against deca6325). The first suspect is the background publication pass over the 40 MB store (5b8f1e1a, D51 pacing), which landed after the last strict X11 pass.
- A qompack daemon this seat did not start was resident at about 00:30 EDT: pid 55264, `qompack.exe daemon --project %TEMP%\qompack-fault-1235450225\proj`, started 00:27:35, left untouched. The experiment's snapshot lists such processes, because a stray daemon is possible host load.

## Independent review

### review:x11win: needs-fixes

- **major** `summary, Decisive evidence 2 and root_cause ("In every failure the tail sits in B-B ... observed stays at 6-12 ms")` — The single-mechanism claim does not hold. Candidate 5's X11 failure has its tail on the client/handler side, not in B-B. The detector is fed the B-A estimate (observed + handler + 1 ms), not B-B, so in c5 the window breaches came from time outside the durable ingest. The claim that observed latency is unchanged also picks its comparison: against the same code's passing pre-freeze run an hour earlier, observed latency doubled in c6.
  - Evidence: phase3/c5/p3-win-e2e-timing.log:21-37: spawn floor p50 42.6 ms; delivered B-B p99 32.768 ms (under 50); B-A p99 61.44 ms; hook_controlled_observed p50 13.312 and p99 24.576 ms, 593 deferred. internal/daemon/handlers.go:341-353 sends `estimated` (observed+handler+allowance) to d.hotSamples, and budget.go:94-120 judges that. Pre-freeze no-ledger pass (prefreeze/e2e.log:28): observed p50 3.072, p99 6.144. c6 fails: 6.144/11.264 (x11-alone.log:31) and 7.168/12.288 (e2e-timing.log:31).
  - Fix: Report at least two failure modes: B-B tail (c6, pre-freeze integration row) and client/handler inflation with B-B under the limit (c5). Drop 'in every failure' and 'stays the same as in passing runs'. Compare against the closest same-code pass, not only w12.
- **major** `(c)/(d)/(e) 'after-effect of sustained I/O' hypothesis and the 'quiet C5.1' reference` — The quiet reference was not quiet. C5.1 Windows (c51-win, 14.3 ms floor) started about 13 s after candidate 5's whole chain ended (e2e package, Linux timing, four race suites, hotpath-coload), and quiet.sh logged a host-not-idle warning. It passed comfortably at 5000 iterations. So the empty-project harness passed straight after sustained load, while the resident-store rows fail after load. The investigator never checked when c51 ran. This undercuts a pure host after-effect and points to something that depends on resident state.
  - Evidence: phase3/c5/chain.log: 'run hotpath-coload exit=0 2026-10-01T03:11:53Z', then 'load before-c51-win 03:12:05Z: windows_cpu_pct=39 linux_loadavg=3.67 ... WARNING: host not idle'. quiet/c51-win.json started 2026-09-30T23:12:06-04:00, argv without --project. quiet/c51-win.log:12-16: floor p50 14.307, B-A p99 30.72, B-B p99 22.528, n=5064, no deferral.
  - Fix: Treat 14.3 ms as a post-load floor, not a quiet one. Add 'empty-project harness passes after load, resident rows fail after load' to the evidence, and drop or qualify the claim that the cause is purely environmental.
- **major** `Decisive evidence 4, (c) 'per-request cost is not the cause', and the dismissal of 5b8f1e1a` — Missed explanation: a product × host interaction. Every strict Windows X11 pass (w11 c3782687/d0fb4983, w12 deca6325) predates 5b8f1e1a, the background startup publication pass. X11 has not passed strict on any candidate that contains it. The pass runs daemon-wide beside the B-A loop and is not on the 'per-request path' the investigator diffed (ingest.go, groupcommit.go, delivery_*, ipc, hookclient). It yields only between I/O units, and a 256-name directory batch already under way finishes first. Its own doc records 15.1 s cold on a store of 3800 objects and a PutBytes p99 moving from 16 to 26 ms beside it before D51. The X11 and integration stores hold 9555 objects and 2000 captures. A cold page cache after a heavy chain would explain all of it: empty-project c51 passes after load, resident rows fail after load, and rows pass when they run first. The c5 INVALID note documents exactly that memory pressure (WSL VM held 12.7 GB, 1.5 GB free). One passing run of the pre-freeze no-ledger arm cannot rule out a contributor that only acts under load.
  - Evidence: `git merge-base --is-ancestor 5b8f1e1a` gives NOT in c3782687, d0fb4983, deca6325 or 26a96f70, and in 0d06ab1 (c5), 61b0cd66 and 99d0b18. internal/daemon/publication_audit.go:102-114 (pacing semantics and cold timings), internal/store/publication_audit.go:120 scanDirBatch = 256. phase3/c5/p3-win-x11-alone.INVALID.txt (12.7 GB page cache, 1.5 GB free).
  - Fix: Name this as a live hypothesis next to host-only causes. Extend the history check to publication_audit.go and internal/store. Classify the outcome as undetermined, not environmental, until it is excluded.
- **major** `(e) x11-decisive.sh decision table` — The experiment cannot separate the hypotheses it needs to. (1) No branch covers 'ctl passes, x11-a and int-a both fail'. That is what the publication-pass hypothesis predicts, because both rows carry 40 MB stores. (2) E3 is the only arm that tests the named suspect 5b8f1e1a, and it runs only when int-a passes while x11-a fails, which that suspect would not produce. (3) ctl-c is collected but never interpreted. Yet 'ctl-c passes, int-b fails' is what separates a resident-state × load cause from a host after-effect, and c51 already suggests that outcome. (4) Each cell runs once, although the evidence shows identical code flipping between pass and fail within 90 minutes. (5) No branch covers 'E1 green and E2 green' (no reproduction). (6) Inside E1, int-a and ctl-b run straight after x11-a, which does 6-8 minutes of fsync-bound Phase A work, so they are not idle runs. Finally, nothing records when the daemon's publication pass finishes relative to the B-A loop.
  - Evidence: x11-decisive.sh: 'abba  E3, only if E1's x11-a fails while ctl-a, int-a and ctl-b pass'. The phase lines run each row once ('idle) ctl ctl-a; x11 x11-a; introw int-a; ctl ctl-b'). The summary's outcome list has only three branches and never mentions ctl-c.
  - Fix: Interleave at least 3 repetitions per cell. Add interpretations for ctl versus resident rows in both E1 and E2, and for 'both resident rows fail with ctl green'. Run the A-B-B-A comparison (deca6325 vs 99d0b18, or a build with the background pass disabled) on the integration row too, and do not make it depend on X11 alone failing. Have the harness daemon's log record when the publication pass completes, and record free memory and the standby list. Put an idle gap before int-a.
- **minor** `Decisive evidence 1 ('The same code passed and failed within 90 minutes, in both rows')` — The comparison leaves out a confound. The pre-freeze runs were at below-normal priority on an evening-loaded host; the overnight runs were at normal priority with the host otherwise idle. The pair shows that code did not change, but not that conditions were comparable. The direction is also unexplained: the X11 no-ledger arm passed at below-normal priority and failed at normal priority.
  - Evidence: phase3/c6-CANDIDATE.md:19-33 ('Windows, sequential, at below-normal priority on an evening-loaded host ... The daemon ran below normal priority beside the owner's applications and the Docker VM'). Pre-freeze no-ledger arm PASS (prefreeze/e2e.log:18-19); overnight FAIL (p3-win-x11-alone.log:18-19).
  - Fix: State the priority and load difference, and use the pair only as evidence that the code was the same.
- **minor** `Classification and root_cause ('specifically a disk-flush latency tail inside the daemon's durable ingest')` — This attribution is not measured. B-B times all of makeDurable: WAL append, the lease journal and the position sidecar, including CPU time, mutex waits and scheduling, not only fsync. No disk counters were captured, and the B-B median doubled too (8.2 to 15.4 ms against the pre-freeze pass), not just the tail. The investigator itself says the host factor is undetermined, yet the classification is a firm 'ENVIRONMENTAL ... disk-flush'. Separately, the argument that observed latency 'is where spawn ... contention would show' is wrong: req.TS is set at the hook's first statement, so process creation and image load (for example Defender scanning the spawned exe) are outside observed.
  - Evidence: internal/daemon/ingest.go:300-321 (obs.Timed around work = makeDurable plus the ring send); internal/cli/hookclient.go:322 (the B-A origin is the first statement). B-B p50 is 8.192 in prefreeze/e2e.log:19 and 15.360 in p3-win-x11-alone.log:19.
  - Fix: Classify as undetermined (environmental likely, product × host not excluded) until counters exist, and describe B-B as 'durable-ingest time', not 'disk flush'.
- **nit** `(a) the ~26 ms gap between B-A p50 and observed p50` — The roughly 10 ms left after subtracting B-B is put down to 'admission, encode and millisecond truncation'. Truncating the two whole-millisecond terms can only lower B-A, never raise it. The residual also grows with the spawn floor: about 5 ms in the pre-freeze pass at a 14 ms floor, about 9 ms in w12 at 24 ms, about 11 ms in c6 at 30 ms. That pattern fits a contention-sensitive component in the handler window that goes unmeasured, such as capture.enter or scopeRefusal's filesystem checks.
  - Evidence: internal/daemon/handlers.go:342-344 (whole-ms truncation of observed and handler); handlers.go:262-296 (capture.enter, admitDelivery/scopeRefusal and the route all lie inside recvTS..handler end). The residuals come from prefreeze/e2e.log:18-19,28, w12 e2e-x11-isolated-windows.log:19-20,29 and p3-win-x11-alone.log:18-19,31.
  - Fix: Remove truncation from the explanation and mark the residual as unattributed and scaling with load.

## Fix seat (review resolution) — status `done`, head `99d0b18cf7d5991aa733e70ce52a542b80aa7710`

### Root cause

X11's no-ledger arm fails the same absolute B-A/B-B gates as C5.1 and trips the §8.1 detector at exactly 3×512 samples, so 593 deferred is binary. Every strict failure at normal priority since D41 (c5 X11, c6 X11 in e2e, c6 X11 alone) ran with the laptop on battery (Kernel-Power event 105: AC off 22:29:32 to 23:55:00 on 10-01, and 19:36-22:29 on 09-30 with Energy Saver on). Each of those runs was also inside or right after the e2e package, on code with 5b8f1e1a. Battery explains the 2-3x spawn floor (AC 13-16 ms in every AC run, battery 23-30, battery with Energy Saver 42.6) and the 1.45x slower daemon-free Phase A on identical code. Two signatures follow from it: a CPU-side one (c5: B-B under its limit, B-A over it) and a durable-ingest tail (c6: B-B p99 57-82). Battery alone is not sufficient: w12 and the c5 integration row passed on battery. A product × host co-factor (the background publication pass) is not excluded, so the cause is environmental as a trigger and undetermined as to code until e1/e4 run.

### Summary

W17 X11WIN, FIX SEAT: corrected investigation of D53(d), why X11 (TestV3_HotPathUnchangedWithLedgerResident) fails on this Windows laptop.

This was investigation only. No product, test, bundle or worktree bytes changed, nothing was committed, and the worktree is clean at 99d0b18. Two diagnostic patches and a script exist in scratch only (listed at the end).

== CLASSIFICATION ==
- The c5 and c6 failures are ENVIRONMENTAL as timing evidence. The trigger is identified, and the first seat missed it: **every strict X11 failure at normal priority since D41 ran with the laptop on battery.**
- The question of code is UNDETERMINED. In all three failing runs, battery coincides with two other factors:
  - the run sat inside or straight after the test/e2e package;
  - the code contains 5b8f1e1a (the background startup publication pass).
- No evidence yet excludes a product × host co-factor.
- The c6 X11-alone run did not meet D53(d)'s precondition ("re-run alone before code is blamed"). It ran on battery, right after the e2e package.

== THE NEW FINDING: POWER SOURCE ==
Source: Kernel-Power event 105 (AcOnline), read-only, history from 09-27. Copied to scratch acdc-events.txt.

The AC is cut unattended at 89-100 % charge and restored at about 35-40 %:
- 09-30: AC off at 19:36:33, on at 22:29:02, off at 23:37:20.
- 10-01: AC on at 01:08:43.
- 10-01: AC on at 21:03:48, **off at 22:29:32 (94 %), on at 23:55:00 (39 %).**

The DC profile is not the AC profile (`powercfg /qh`, read-only):

| Setting | AC | DC |
|---|---|---|
| EPP | 25 | 50 |
| Boost policy | 60 | 40 |
| Performance increase threshold | 60 | 90 |
| Heterogeneous policy | 0 | 4 |
| Core parking increase time | 1 | 4 |
| PCIe ASPM | off | maximum savings |
| Primary NVMe latency tolerance | 15 ms | 50 ms |
| Primary NVMe idle timeout | 200 ms | 100 ms |
| Energy Saver | off | on below 50 % |

Run map. Times are EDT; "post" means inside or right after test/e2e; "+pass" means the code contains 5b8f1e1a.

| Run | Power | Position | Code | Floor p50 | Observed p50/p99 | B-B p50/p99 | Result |
|---|---|---|---|---|---|---|---|
| c6 X11-alone 22:49-23:00 | BATTERY (since 22:29:32) | post | +pass | 30.0 | 6.1/11.3 | 15.4/81.9 | FAIL, 593 deferred |
| c6 X11 in e2e (harness certainly after 22:32) | BATTERY | post | +pass | 29.1 | 7.2/12.3 | 15.4/57.3 | FAIL, 593 deferred |
| c5 X11 in e2e (harness about 22:26-22:28, estimated) | BATTERY + Energy Saver (about 35 %) | post | +pass | 42.6 | 13.3/24.6 | 14.3/32.8 | FAIL (B-A p99 61.4), 593 deferred |
| c6 integration row 22:18-22:23 | AC | first, right after about 50 min of pre-freeze work | +pass | - | - | - | PASS (not -v) |
| c5 integration row 22:00-22:09 (09-30) | BATTERY + Energy Saver | first | +pass | - | - | - | PASS (not -v) |
| c51-win 23:12 (09-30), empty project, 5000 iterations | AC | after container load, host-not-idle WARNING | +pass | 14.3 | 3.1/6.1 | 13.3/22.5 | PASS |
| pre-freeze X11 no-ledger arm (about 21:4x) | AC, below-normal priority, evening load | inside e2e | +pass | 14.1 | 3.1/6.1 | 8.2/28.7 | PASS |
| pre-freeze X11 ledger arm | AC, below-normal, evening load | inside e2e | +pass | 15.6 | 4.1/6.1 | 9.2/49.2 | FAIL (B-A p99 57.3, 0 deferred) |
| pre-freeze integration row 21:50-21:54 | AC, below-normal, evening load | right after e2e | +pass | 15.1 | 4.1/11.3 | 14.3/73.7 | FAIL, 593 deferred |
| w12 X11 ×2 (commits 12:41 and 13:04, 09-29) | BATTERY (since 11:02:32) | isolated | pre-pass | 23.3-24.5 | 6.1/9.2-10.2 | 9.2/13.3-18.4 | PASS, pair PASS |
| w11 harness runs | likely AC (floors 13.2-15.0) and likely battery (floors 23.4-25.7); commit 11:52, AC off at 11:02 | isolated | pre-pass | | | | all PASS |

The c5 X11 timing is estimated from the Linux c6 e2e jsonl (X11 starts at 469 of 1100 s) scaled to Windows. Its 3x floor independently says it was not on AC.

What the table shows:
1. **The spawn floor tracks power, not code or position.**
   - Every AC run: 13-16 ms.
   - Battery: 23-30 ms.
   - Battery + Energy Saver: 42.6 ms.
   - D53(d)'s "3x spawn floor" was battery + Energy Saver.
2. **A daemon-free phase slowed by the same factor.** X11's Phase A (store/observer/negknow corpus build, no daemon, no publication pass), on identical code (61b0cd66 = 99d0b18 in Go), took 347 s on AC (pre-freeze) and 503 s / 511 s on battery (c6). The host was about 1.45x slower at fsync-bound work with no product daemon involved.
3. **Two failure signatures, not one.**
   - CPU side (c5): floor 3x, observed 4x. B-B p99 was under its limit, and B-A crossed it through observed plus non-B-B handler time.
   - Durable-ingest tail: B-B p99 57-82 ms with B-B p50 about 15 ms. Seen in c6 (battery, post-e2e) and also in the pre-freeze integration row (AC, post-e2e, evening load, below-normal, floor 15.1).
4. **The three factors line up only in the failures.**
   - Every strict failure at normal priority had battery AND post-e2e AND +pass. No strict pass had battery AND post-e2e.
   - Each factor alone has passes:
     - battery: w12 ×2, c5 integration row;
     - post-e2e on AC: the pre-freeze no-ledger arm, even below-normal under load;
     - +pass: c51, c6 and c5 integration rows, the pre-freeze no-ledger arm.

== (a) What X11's no-ledger arm gates vs TestIntegration_HotPathWarmWithRealResidentState ==
The harness invocation is the same in both:
- `bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json <f> --project <root>`, with no --under-coload.
- Defaults on Windows: budgetMs 50 (D41), breachWindows 3, spoolOnBreach true, l0IngestMs 50.
- Same detector, same populations (64 warm-up hot requests + 1936 admin.ping, 200 spawn-floor runs, 2000 spawns, 50 checkpoints, 64+2 ack-rtt), same fixed Read payload.
- So there is nothing to swap between them.

The projects differ:
- X11: real observer corpus, 2000 Reads, 43.1 MB raw / 3.68 MB stored, DAG 9041/17920. The no-ledger twin is copied before the ledger; Phase A is about 6-8.5 min of fsync-bound work.
- Integration row: in-process daemon corpus, 9555 objects, seeded 1024-entry tried.bloom.

Gating:
- X11 requires harness exit 0 (v3_x11_test.go:605, reached from :511), then the absolute rows, then the ledger arm, then the D42 pair.
- The integration row gates the same harness, plus population identity and no spool submode in isolation.

What B-A is (handlers.go:331-353): `B-A = (recvTS - req.TS) + (handler end - recvTS) + hotPathTailAllowance`, all in whole milliseconds.
- **Correction:** req.TS is taken at the hook's first statement (hookclient.go:322). Process creation, image load (including any Defender scan of the spawned exe) and Go runtime start are outside observed; they show in the spawn floor and B-D.
- Observed covers: the hook's own state.bin and config reads, stdin, dial/encode/write, and the daemon's read and decode up to recvTS.
- hotPathTailAllowance is `1 * time.Millisecond` (budget.go:14-18). It is an estimate, "neither a measured tail nor a guaranteed upper bound", with no derivation beyond §2.4.
- The B-A p50 32.8 vs observed 6.1 gap is the pre-ACK handler: B-B p50 15.4 plus about 10 ms of other handler time.
  - That other handler time is about 4 ms on AC (pre-freeze) and about 8-10 ms on battery, i.e. CPU-side.
  - These are differences of medians, so approximate.
- A slow floor does not enter B-A directly. The power state slows the floor, observed and the handler together.

== (b) Strict passes ==
X11 passed strict twice on Windows (w12, both on battery, pre-5b8f1e1a, isolated):
- runs/e2e-x11-isolated-windows.log, 668.51 s: no-ledger B-A p99 36.9, B-B 18.4, 0 deferred; ledger 28.7/13.3; "X11 pair (gated: PASS)" 6.144 vs 6.144, ceiling 7.680.
- runs/fix-e2e-x11-isolated-windows.log, 628.13 s.
- The full X11 test has never passed strict on code containing 5b8f1e1a. Its no-ledger arm (the arm that fails overnight) did pass at 61b0cd66 on AC.

Other history:
- V5 and pre-D41 runs never passed. At 15 ms, B-A p50 was 15-16 ms; those old rows show 593 deferred at B-A p99 under 50 because the limit was 15.
- The integration row's w11 passes show n=2064 with no ledger note (0 deferred).
- The c5 and c6 timing-set passes have no numbers (not -v), but a strict pass excludes a 593-style deferral.

== (c) What the detector compares ==
- It compares the B-A ESTIMATE (`d.hotSamples <- estimated`, handlers.go:349), not B-B.
- Limit 50 ms on Windows. Statistic: nearest-rank p99 of each closed 512-sample ring (index 506, the 6th-largest). A window breaches at p99 >= limit, and 3 consecutive breaches switch to spool.
- There is one daemon-wide ring and no session.start, so window 1 = 64 warm-up samples + the first 448 spawns. The switch lands exactly at sample 1536. With D44 the submode does not recover, so 528 + 65 requests always defer: 593 is binary.
- All three windows breach because the host state (battery: CPU policy, ASPM/NVMe power management) held for the whole B-A loop. In c6 the delivered B-B p99 alone (57-82) was over 50.

B-B is durable-ingest time, not "disk flush":
- obs.Timed (ingest.go:321) wraps makeDurable plus the ring send: WAL append + fsync, lease journal, position sidecar, plus CPU, mutex waits and scheduling. No disk counters were captured.

Hypothesis, unmeasured: on DC, PCIe ASPM at maximum savings plus NVMe states entered after a 100 ms idle with 50 ms tolerated latency would put wake latency on the fsyncs that follow longer gaps.
- c6 B-D p99 was 118-135 ms (over 100). w12's battery B-D p99 was 68-81 ms (under 100) and had no B-B tail.

The per-request cost is not shown to have grown:
- Since w12, the observe.tool path changed only by the capture gate's enter/leave in dispatchOp (d95f0520, one mutex each way). handlers.go's other changes are on the prompt route.
- **Correction:** the first seat's path list omitted handlers.go.
- New beside the B-A loop is the daemon-wide background publication pass (5b8f1e1a, gated by D51: d95f0520, bfc77162).
  - It yields before each I/O unit; a unit already under way (one ReadDir batch of up to scanDirBatch=256 names, or one sidecar read) finishes first. See publication_audit.go:102-114 and store/publication_audit.go:120/519.
  - D51's own evidence was measured on battery (w15c, 09-30 16:55, AC off since 16:48), co-loaded, warm 3800-object store: a PutBytes beside the gated pass had p99 18.9-19.9 ms against 22.9-25.1 ms alone. That argues against a large effect, but it is not the B-B path, not 9555 objects, and not a cold cache.

== (d) Which gate fails ==
- The same absolute gates as C5.1: B-A p99 < 50 and B-B p99 < 50, with undelivered samples counted as over budget. Not D42.
- The test aborts at the first arm (:605 via :511), so the ratio was never computed in c5 or c6.
- It passed in both w12 runs.
- The pre-freeze run failed at the ledger arm (:513). From its artifacts, the first seat computed base 3.072, ledger 4.096, ceiling 4.096, n equal: the pair would have passed exactly at the ceiling.

== (e) Decisive experiment ==
Script: scratch `w17/x11win/x11-decisive-v2.sh`. It supersedes x11-decisive.sh and passes `sh -n`; it was not run.
- It refuses to start until chain.log has "quiet exit=" or "done".
- It unsets QOMPACK_UNDER_COLOAD and QOMPACK_NONREFERENCE_DISK.
- Every block is power-gated (AC runs start at charge <= 80; battery runs start at >= 55, and >= 85 for a 45-minute post block).
- After each run it re-checks event 105 since the run's start. A power change makes the run INVALID-POWER, and the block is retried once.
- Each run gets a snapshot (power, docker ps, free memory, qompack/vmmem processes, C: free, power scheme) and a bounded typeperf log: disk sec/write and transfer, queue length, % processor performance, processor frequency, standby and modified lists, available MB.

Setup (coordinator):
- STOCK = a pristine checkout at 99d0b18 (qompack-cx-w17-x11win).
- TRACE = 99d0b18 + trace.patch.
- NOPASS = 99d0b18 + trace+nopass.patch.
- OLD = aa7b799c (w12's last strict pass; its own test and harness).
- Command: `STOCK=... TRACE=... NOPASS=... OLD=... sh x11-decisive-v2.sh <out> e1 e2 e3 e5`, then `COND=<condition> sh x11-decisive-v2.sh <out> e4 [e6]`.

Rows the script runs:
- x11 = `go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e`
- int = `go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration`
- ctl = `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json <f>`
- e2e = `go test -count=1 -timeout=30m ./test/e2e`

Plans and outcomes:
- **e1** (AC, 5 min idle before each run, STOCK, 3 rounds of ctl/x11/int): the reference condition.
  - All pass (0 deferred, pair PASS): X11 passes on the reference host. The c5/c6 X11 failures are battery runs; e1's X11 runs are D53(d)'s Windows evidence. Timing runs gain a power rule.
  - X11 fails while ctl and int pass: the cause is X11-specific; run e4 and e6 with COND=ac-iso.
  - ctl fails: even AC is not at reference; undetermined, read the counters.
- **e2** (AC, post: e2e, then x11, int, ctl with no gaps):
  - Pass: position alone does not fail on AC.
  - Fail with floor about 14 and a B-B tail: a post-e2e durable-ingest effect exists on AC; run e4 with COND=ac-post.
- **e3** (battery, post): the overnight condition. Expected to reproduce. If it passes, something else that night mattered (Docker restart loop, disk state): undetermined.
- **e5** (battery, isolated):
  - Pass: battery needs the position to fail.
  - Fail: battery alone suffices at the current code; run e4 with COND=bat-iso.
- **ctl versus resident rows** under the same condition:
  - ctl passes while x11/int fail: resident state × condition (product co-factor, or a store-size-dependent environment such as a cold cache).
  - ctl fails too: the host condition alone.
- **e4** (TRACE/NOPASS in A-B-B-A order on x11, then int, under the condition that reproduced):
  - NOPASS passes twice while TRACE fails twice: the background pass is a product co-factor. It breaks D51 ("must not slow the session") on that condition: a product defect for wave 18 and a new candidate.
  - Both fail: not the pass; run e6.
  - Mixed: repeat to 3 per cell.
- **e6** (OLD/TRACE in A-B-B-A order on x11):
  - OLD passes twice while TRACE fails twice: a regression in aa7b799c..99d0b18; bisect.
  - Both fail: battery (plus position) is sufficient; the reference is AC, and battery is the documented degrade (0 lost).

TRACE runs log harness phase times, the daemon's "continues after startup" line and a DIAG line when the background pass ends, and copy the harness daemon's .qompack/logs out of t.TempDir when QOMPACK_DIAG_KEEP_LOGS is set.

== REVIEW RESOLUTION ==
1. **Major, "tail always in B-B" / "observed unchanged": HOLDS, corrected.**
   - c5's B-B p99 was 32.8 (under 50) while B-A p99 was 61.4, observed 13.3/24.6 and floor 42.6 (c5/p3-win-e2e-timing.log:21-37). The detector judges the B-A estimate (handlers.go:341-349).
   - Two signatures are now reported.
   - Observed doubled on battery: 3.1 on AC pre-freeze against 6.1-7.2 in c6. The first seat compared against w12, which was itself on battery.
2. **Major, "quiet reference was not quiet": HOLDS in part.**
   - c51-win started 13 s after the c5 chain's container steps, with the not-idle WARNING (Windows CPU 39 %, Linux load 3.67; c5/chain.log:15-17). The last Windows-native test I/O had ended 38 min earlier.
   - It ran on AC (since 22:29:02). So 14.3 ms is the AC floor under residual load, not a quiet floor.
   - Rebutted: "resident rows fail after load". The resident integration row passed on battery (c5) and on AC right after heavy pre-freeze work (c6), and the resident X11 no-ledger arm passed inside e2e on AC. Power, not resident state, separates c51 from the failures.
3. **Major, publication-pass co-factor: HOLDS as a live hypothesis; classification changed to undetermined as to code.**
   - Ancestry verified: 5b8f1e1a is NOT in c3782687, d0fb4983, deca6325 or 26a96f70, and IS in 0d06ab12, 61b0cd66 and 99d0b18.
   - Yield semantics verified (one unit finishes first).
   - Rebutted as sufficient: post-5b8f1e1a code passed the no-ledger arm (AC) and the integration row (AC, and battery + Energy Saver), and Phase A (no daemon) slowed 1.45x on battery.
   - The discriminating test is e4.
4. **Major, experiment design: HOLDS; the experiment was rebuilt.**
   - Crosses power × position × code.
   - Interprets ctl against resident rows and both-rows-fail.
   - The A-B-B-A arms do not depend on X11 failing alone, and cover the integration row.
   - Covers the no-reproduction case, idle gaps before isolated runs, repeats, pass-end logging, and memory and standby counters.
5. **Minor, priority/load confound: HOLDS; now explained.** c6-CANDIDATE.md:19-33 confirms below-normal priority on an evening-loaded host. The below-normal runs were on AC; the normal-priority failures were on battery. The pre-freeze/overnight pair is used only as "same code".
6. **Minor, "disk flush" and the observed origin: HOLDS.** B-B is now described as durable-ingest time (ingest.go:300-321). The claim that spawn contention shows in observed is withdrawn (hookclient.go:322).

== COMMANDS RUN (read-only unless noted) ==
- Read chain.log, the c5/c6 logs and JSON, c6-CANDIDATE.md, the ledger D41-D56, the cited code and the w15c evidence.
- `git merge-base --is-ancestor 5b8f1e1a <7 heads>`; git log/diff 26a96f70..99d0b18 over the per-request files.
- Get-WinEvent Kernel-Power (ids 105, 506, 507, 566); powercfg /qh for SUB_PROCESSOR, DISK, PCIEXPRESS, SLEEP, VIDEO and ENERGYSAVER; BatteryStatus and Win32_Battery. Power at 01:06 EDT: AC, 93 %, charging.
- `python acmap.py`; the X11 offset from the c6 Linux e2e jsonl.
- `go vet -p 2 ./internal/daemon ./test/bench/hotpath ./test/e2e ./test/integration` on the scratch snapshot: both patch variants exit 0, and trace also on GOOS=linux.
- `git apply --check` of both patches on the candidate worktree: both apply, and nothing was applied.
- The three unit tests listed under tests.
- `sh -n x11-decisive-v2.sh`.

Scratch files (w17/x11win): acdc-events.txt, acmap.py, acmap.txt, mkpatch.py, trace.patch, trace+nopass.patch, x11-decisive-v2.sh, diag-src/ (a throwaway snapshot repo without plans/; deletable). The first seat's parse.py, table.txt, unit-percentile.log and x11-decisive.sh (superseded) are kept.

No criterion changes.

### Tests

- `go test -p 2 -count=1 -run '^TestBreachDetectorTransitionsAfterThreeWindows$' ./internal/daemon` — ok 0.248s. The name exists at budget_test.go:23 (the first seat saw PASS with -v): three breaching 512-sample windows switch to spool
- `go test -p 2 -count=1 -run '^TestPercentileDurationP99$' ./internal/daemon` — ok 0.223s. The name exists at budget_test.go:98: a window's p99 is its 6th-largest of 512 samples
- `go test -p 2 -count=1 -run '^TestDefaults_HotPathBudgetPerPlatform$' ./internal/config` — ok 1.198s. The name exists at defaults_test.go:169: windows budgetMs 50
- `go vet -p 2 ./internal/daemon ./test/bench/hotpath ./test/e2e ./test/integration (scratch diag-src, trace+nopass applied; then trace only, Windows and GOOS=linux)` — exit 0 for all three. The diagnostic patches compile; they were never applied to any worktree
- `git apply --check trace.patch; git apply --check trace+nopass.patch (in qompack-cx-w17-x11win)` — both apply cleanly to 99d0b18; the worktree is unchanged (git status empty)
- `sh -n x11-decisive-v2.sh, plus a dry call of its power and event-105 probes` — syntax ok; the probes returned 'AC 92' and '23:55:00 AC=true 22:29:32 AC=false'. Not executed as an experiment

### Open issues

- D53(d) is still open. Every strict X11 failure since D41 (c5 in e2e, c6 in e2e, c6 alone) ran on battery and inside or right after the e2e package, on code with 5b8f1e1a. Run x11-decisive-v2.sh e1 (AC, isolated) first; then e2, e3 and e5, and e4/e6 as its table says.
- TIME-CRITICAL for tonight: at 01:03-01:06 EDT the laptop was on AC at 92-93 % and charging, and the AC has been cut at 89-100 %. quiet.sh's C5.1 Windows run will probably run on battery. Before accepting it, check event 105 over its window with `Get-WinEvent -FilterHashtable @{LogName='System'; ProviderName='Microsoft-Windows-Kernel-Power'; Id=105; StartTime=(Get-Date).AddHours(-12)}`. A battery run is not a reference measurement.
- The chain scripts (overnight-c6.sh, quiet.sh) record CPU load but not power source. Every Windows timing step should log PowerOnline and charge, and any event-105 transition during the step. That is a coordinator-script change, not product code.
- Re-classify earlier evidence by power. c5's p3-win-timing pass and its X11 failure both ran on battery with Energy Saver on (about 35 %). w12's two strict X11 passes ran on battery. c51-win and the pre-freeze runs were on AC.
- A second signature is open: a durable-ingest tail after the e2e package on AC (pre-freeze integration row: floor 15.1, B-B p99 73.7, under evening load at below-normal priority). e2 tests it.
- Product co-factor not excluded: the background publication pass (5b8f1e1a) landed after w12's last strict passes. If e4 shows NOPASS passing where TRACE fails, it breaks D51 on that condition (a product defect for wave 18 and a new candidate).
- Carried from the first seat: C: is 93 % full (134 GB free) on the shared NVMe with Docker's 288 GB vhdx, and supabase_vector_promptly was restart-looping. Record both beside any reference run. The stray daemon (pid 55264, 00:30) was gone at 01:06.

### Needs the owner

- The reference laptop's AC is switched off at about 90-100 % charge and back on at about 35-40 %, unattended (Kernel-Power event 105, 09-27 to 10-01; e.g. off 22:29:32 and on 23:55:00 on 10-01). On DC, Windows applies a slower CPU policy, PCIe ASPM at maximum savings, deeper NVMe power states and Energy Saver below 50 %. The decision: keep AC connected on timing nights, or accept power-gated runs that wait for AC windows. Separately, Windows reference timing (C5.1, X11, the integration row) should state that it runs on AC, and a battery run should count as INVALID the way c5's memory-starved run did. No budget or threshold changes. The script's gating values (55/85/80 %, 300 s idle) are experiment parameters taken from the observed cycle, not product numbers.

## Independent verification of the fix seat: needs-fixes

- **major** `fix-seat summary: CLASSIFICATION ('The trigger is identified'), 'What the table shows' items 3-4, root_cause ('Two signatures follow from it'), and REVIEW RESOLUTION item 2 ('Power, not resident state, separates c51 from the failures')` — The battery-trigger claim only holds because the seat uses the below-normal pre-freeze runs one way when they pass and the other way when they fail. When they pass, they count as evidence: the pre-freeze no-ledger arm is cited as proof that post-e2e on AC, and +pass, are not sufficient to fail. When they fail, they are left out under the 'every strict failure at normal priority' rule. Two of those excluded runs are AC failures. The pre-freeze integration row failed on AC after e2e, with the same durable-ingest-tail signature and the same 593-deferred detector trip as c6. The pre-freeze X11 ledger arm also failed on AC. So the durable-ingest tail does not 'follow from' battery: it also appears on AC. Finding 2's pattern also still stands on AC: the empty-project c51 passed after load on AC, while the resident integration row failed on AC after e2e. The rebuttal of finding 2 is therefore unsound as written.
  - Evidence: prefreeze/summary.log: hotpath 01:50:44Z-01:54:31Z, i.e. 21:50-21:54 EDT on 10-01. AC had been on since 21:03:48 (scratch acdc-events.txt). prefreeze/hotpath.log: TestIntegration_HotPathWarmWithRealResidentState FAIL. Per acmap.txt: floor 15.1, B-B p50/p99 14.3/73.7, led 2130/1537/593. prefreeze/e2e.log: the ledger arm fails with B-A p99 57.3, on AC. c5/quiet/c51-win (AC, 23:12 on 09-30) passed with an empty project. The fix summary lists 'post-e2e on AC: the pre-freeze no-ledger arm, even below-normal under load' and '+pass: ... the pre-freeze no-ledger arm' as passes, while restricting failures to 'normal priority'. c6-CANDIDATE.md:19-33 confirms all of these ran at below-normal priority.
  - Fix: Treat the below-normal pre-freeze runs the same way whether they pass or fail: either drop all of them or keep all of them. Restate battery as a correlate of the three X11 failures, not as the identified trigger. Do not say the durable-ingest-tail signature 'follows from' battery, since it occurred on AC. Withdraw 'Power, not resident state, separates c51 from the failures' and record that empty-project-passes and resident-row-fails after load is also seen on AC, confounded by priority. Keep the overall classification undetermined until e1/e2 run.
- **major** `scratch w17/x11win/x11-decisive-v2.sh: block()/wait_power() power gating (BAT_MIN=55, BAT_BLOCK=85, AC_MAX=80), plans e5 and e4` — The battery gate checks the charge only when a block starts, and checks only for AC transitions afterwards. The seat itself separates battery (floor 23-30) from battery plus Energy Saver below 50 % (floor 42.6), but nothing keeps the two apart during a run. A bat-iso block that starts at 55 % crosses 50 % within about 8 minutes at the measured drain of about 0.64 %/min (94 % to 39 % in 86 minutes on 10-01). So e5's x11 and int, and e4 under COND=bat-iso, would run partly under Energy Saver. That is likely in the default order 'e1 e2 e3 e5', where e5 starts right after e3 has drained the battery to about 55-60 %. e5's branch 'Fail: battery alone suffices' would then confound battery with Energy Saver, and the INVALID-POWER check would not catch it. Separately, BAT_BLOCK=85 together with the roughly 3-hour AC cycle allows at most one bat-post block per discharge. e4's 8 'A-B-B-A' cells under the default COND=bat-post would therefore stretch over about a day of separate charge cycles. A cell that WAIT_MAX skips leaves an incomplete A-B-B-A, and the summary does not flag it.
  - Evidence: x11-decisive-v2.sh: 'BAT_MIN=${BAT_MIN:-55} # a battery run starts only above the DC Energy Saver threshold (50 %)'. wait_power is called once per block. verdict=INVALID-POWER only when transitions_since (event 105) is non-empty. e5 is 'block ... bat-iso stock ctl x11 int' with 300 s idle before each row, about 35 minutes in all. acdc-events.txt: 10-01 22:29:32 AC=false 94 %, 23:55:00 AC=true 39 %. e4 loops 'for row in x11 int; for a in trace nopass nopass trace; block ... $COND'. COND defaults to bat-post.
  - Fix: Re-check the charge before every run and make the run invalid if charge falls below about 52 % (ENERGY-SAVER) at any point. Alternatively, start bat-iso blocks only at 85 % or more, or record Energy Saver state per run. For e4/e6 on battery, run each A-B-B-A quartet inside one discharge window, as single rows rather than post blocks, or document that the pairs span cycles. Make the summary mark any quartet with a skipped cell as incomplete.
- **major** `fix-seat (e) decision table for e2, e3, e5 and 'ctl versus resident rows'; REVIEW RESOLUTION item 4 ('Covers ... repeats')` — Finding 4's single-run objection is resolved only for e1, which has 3 rounds. e2, e3 and e5 still run each row once, and their branches draw causal conclusions from one run: 'Pass: position alone does not fail on AC', 'Pass: battery needs the position to fail', 'If it passes, something else ... mattered'. The evidence these runs are meant to settle shows identical code flipping between pass and fail. The ctl-versus-resident comparison that finding 4 asked for is also confounded by order in the post blocks. In e2/e3, ctl runs last, after x11 (about 11 minutes) and int (about 4 minutes), so it sits about 15 minutes further from the e2e package than x11. 'ctl passes while x11/int fail' could then reflect a fading after-effect rather than resident state.
  - Evidence: x11-decisive-v2.sh: 'e2) block "e2-$n" ac-post stock x11 int ctl', 'e3) block ... bat-post stock x11 int ctl', 'e5) block ... bat-iso stock ctl x11 int'. block() retries only on INVALID-POWER. The fix summary states 'Covers the no-reproduction case, idle gaps before isolated runs, repeats'.
  - Fix: Repeat e2, e3 and e5 at least twice, or 3 times where battery windows allow. Otherwise label their single-run branches as provisional. Rotate or interleave ctl's position within post blocks, for example x11/ctl/int and then ctl/x11/int, so that the ctl-versus-resident inference is not confounded with distance from the e2e package.
- **minor** `fix-seat (c) 'The per-request cost is not shown to have grown ... New beside the B-A loop is the daemon-wide background publication pass'; REVIEW RESOLUTION item 3` — The history check still covers only the pre-ACK path, plus the publication pass. The summary then presents the pass as the only new thing beside the B-A loop. Since w12's last strict pass, though, the post-ACK work that each observe.tool delivery triggers has changed. That work runs beside the next hooks and competes for CPU and disk. For example, 2981cfc1 changes observer/tooluse.go and adds 156 lines to observer/session.go, and the daemon's drain, spool_watch, scheduler_tap and scheduler_runtime changed by about 2.9k lines across internal/daemon. The product co-factor space is therefore described too narrowly. e6 (OLD vs TRACE) would cover it, but only after e4 has failed on both arms.
  - Evidence: `git diff --stat aa7b799c 99d0b18 -- 'internal/daemon/*.go' 'internal/ipc/*.go' ':!*_test.go'` gives 34 files, +2985/-250, including drain.go +123, spool_watch.go +96, scheduler_tap.go +66 and scheduler_runtime.go +105. `git log aa7b799c..99d0b18 -- internal/observer/tooluse.go` lists 2981cfc1 'follow the scheduler's segment rolls'. 37492d56 changes observer.go and session.go.
  - Fix: Restate the claim as 'the pre-ACK path changed only by the capture gate; post-ACK worker, observer and scheduler work also changed (2981cfc1 and others) and is not excluded'. Note that e6 is the only arm that tests those changes, or let e6 run whenever a condition reproduces, not only after e4 fails on both arms.
- **minor** `fix-seat run map rows for w11 and c5, and 'What the table shows' item 1 ('The spawn floor tracks power')` — Some of the power labels behind 'spawn floor tracks power' are inferred from the spawn floor itself, which makes that part circular. The w11 rows are labeled 'likely AC (floors 13.2-15.0)' and 'likely battery (floors 23.4-25.7)'. The c5 X11 label is backed by 'Its 3x floor independently says it was not on AC'. The c5 harness timing is an estimate, and the best estimate (scaling the post-X11 tests from the Linux jsonl) puts the harness end at about 22:28:49 against the AC restore at 22:29:02. That is well inside the estimate's error, so part of the B-A loop may have run on AC.
  - Evidence: c5/p3-win-e2e-timing.json: 22:09:01-22:34:24. X11 took 615.72 s with a 2m39s harness. In the c5 Linux test.jsonl X11 ends at 752 of 1022 s, so 270 s of tests follow it. acdc-events.txt: 09-30 22:29:02 AC=true 34 %. acmap.txt labels w11 by commit time as 'BAT', and the fix seat relabels it by floor.
  - Fix: Base 'floor tracks power' only on runs whose power state comes from event 105 with exact run times: c51, the pre-freeze runs, c6, w12 and the c5 timing set. Mark the w11 and c5-X11 power labels as inferred, and do not cite the floor as independent evidence of power.


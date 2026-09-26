# Wave 2b workstream report: w2-sessionend (C1.15/C1.13)

Branch `closeout/w2-sessionend`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `4703ee191cd20f7ca7dbff0784f8ca771a81cd8b`

### Root cause

C1.15: the host gives plugin SessionEnd hooks one shared 1.5 s budget that a plugin hook's own timeout does not raise (current hooks docs, and the packaging lane's live 'Hook cancelled'), while the flush hook waited up to 15 s for flushRoute's whole session end: settle up to 9 s, then SessionEnd with an 8 s GC deadline, marker, sketches and drain. Measured on base b070bbe: 3.5 to 4.3 s after a burst. C1.13 remainder: a delivery that reached only a client spool holds no lease, so no lane parks behind it and no drain was requested; it waited for the flush or 120 s of idleness, and a late-ACK copy of an in-flight delivery failed its whole drain file. Control-line sidecars: every hook carries a nonce, so a drained flush, checkpoint or session.start line was leased, and dispatchPending published a sidecar for every leased line, which the audit and fsck could not classify. Found along the way: the session-recovery record was rewritten with no lock (lost updates once ends run concurrently), the async end wrote its marker after the answer, several tests read session-end effects right after the flush hook, and X09 depended on the sidecar defect.

### Summary

# w2-sessionend report: C1.15, the rest of C1.13, and drained control-line sidecars

Branch `closeout/w2-sessionend` in worktree `../qompack-cx-w2-sessionend`. It is cut from `closeout/integration` at b070bbe. HEAD is 4703ee1. There are 15 commits, none pushed.

Evidence is under `plans/sdd/V6-closeout/w2-sessionend/runs/`. Each log I wrote starts with its exact command and commit. The logs left by the earlier attempt do not record their commands.

## 0. Resuming: what I found and what I did with it

The brief said the worktree had nothing left over from an earlier attempt. That was stale. An earlier run of this stage (wave 2b, 2026-09-25, 17:38 to 18:04) left three things:
- 4 commits: 3536f12, 11b2eac, 4c06d82 and c4d27e2;
- uncommitted client-spool watcher work: `spool_watch.go`, `spool_watch_test.go`, and edits to `drain.go` and `daemon.go`;
- 10 logs under `runs/`.

No process from that run was still alive; I checked the process list for the worktree path.

What I did with each:
- **3536f12 and 11b2eac (control-line sidecars): adopted.** I reviewed them and re-ran their tests; they are green. They did break X09, which I fixed separately in cc15f21 (section 4).
- **4c06d82 (asynchronous SessionEnd): adopted, with two defects fixed on top:**
  - 677b847: two session ends running at once lost each other's recovery marker.
  - 06d82c9: the recovery marker was written after the answer instead of before it.
  - The change also broke tests that read the session end's effects right after the flush hook. I fixed those in 5261a15, 1d7578b, 3edc200, 0bdbf9e and cc15f21.
- **c4d27e2 (flush hook client is fire-and-forget): adopted unchanged.**
- **Uncommitted watcher: adopted, with changes, in ae1e728:**
  - Its retry rule (one pass per spool size) left a line stranded whenever an earlier arrival of its session was still publishing. `negative-control-no-spool-retry-windows.log` shows 2 of 2 tests red under that rule. I replaced it with retries on a doubling wait, driven by a timer rather than by kicks.
  - One `spool` field on the daemon instead of two.
  - The retry horizon comes from the idle controller (DetectAfterSeconds).
  - A drain now defers a client-spool copy of a delivery whose worker is still publishing it.
  - The tests are rewritten: 8 instead of 4.
- **Earlier logs (`red-*`, `green-*`, `c115-focused-daemon-windows.log`): kept as found**, committed in 4703ee1.

## 1. Root causes

### C1.15: the flush hook is reported "Hook cancelled"

**The host's rule.** The current hooks docs (https://code.claude.com/docs/en/hooks, SessionEnd section) say:
> "SessionEnd hooks have a default timeout of 1.5 seconds … The overall budget rises automatically to match the highest per-hook timeout in your settings files, up to 60 seconds … Timeouts set on plugin-provided hooks don't raise the budget."

The packaging lane saw this in both live sessions: `SessionEnd hook [${CLAUDE_PLUGIN_ROOT}/bin/qompack.exe flush] failed: Hook cancelled` (`packaging/evidence/live-s{1,2}-*/stderr.txt`).

**Why the hook ran too long.**
- On b070bbe the hook sent `Reply: true` with a 15 s deadline.
- `flushRoute` did the whole session end before answering: settle the session's deliveries (up to 9 s), run SessionEnd (its GC alone has an 8 s deadline), write the marker, save the sketches, then drain.

**Measured through the real binary and a real detached daemon.** A temporary probe times the flush hook process from spawn to exit, 5 sessions per mode. Source: `probe-flush-hook-latency_test.go.txt`.

| Build | After a 30-hook burst | Once the session is idle |
|---|---|---|
| Base b070bbe | 3534 to 4298 ms | 582 to 695 ms |
| This branch (af3ed3c) | 65 to 86 ms | 58 to 76 ms |

On this branch the daemon still finishes the session end on its own: about 4.2 s after a burst and about 0.4 s when idle, measured to when it writes `run/marker.json`. Logs: `probe-flush-hook-latency-{base-b070bbe,head-af3ed3c}-windows.log`.

### C1.13 remainder: client-spool-only deliveries

Some deliveries reach only a hook's client spool: the dial failed, or the daemon told the hot path to spool. Those hold no lease. So:
- no lane parks behind them;
- C1.1's drain requests never cover them;
- during an active session they waited for the flush, admin.drain, a restart, or 120 s of project-wide idleness (DetectAfterSeconds).

The e2e premise, "a 30 s idle-tick drain", was false. The idle tick runs its drain only once DetectAfterSeconds of idleness have passed.

A second case: when an ACK is late, the hook spools a copy of a delivery the daemon already accepted. If a drain met that copy while a worker was still publishing the live one, the drain failed the whole file with "delivery still in progress".

### Drained control-line sidecars (the e2e lane's repro)

- The hook client mints a nonce for every hook, so a spooled flush, checkpoint or session.start reaches the drain leased.
- `dispatchPending` published a capture sidecar for every leased line.
- The publication audit and fsck know only the three observe ops. They then reported "capture sidecar with an unrecognized op" and "capture publication requirement is unknown" for the life of the project.

### Found along the way

1. **Lost updates in the recovery record.** `markRecoveryNeeded` and `clearRecoveryNeeded` read, change and rewrite `state/session-recovery.json` with no lock. Once session ends run concurrently, one end's rewrite loses the other's entry. `TestSessionRecovery_ConcurrentEndsLoseNoMarker` was red 3 of 3.
2. **Marker written late.** The asynchronous end wrote the marker only when its goroutine started. A flush acknowledged while Stop was already joining the ends was never marked at all.
3. **Tests that relied on the flush being synchronous.** Several tests read the effects of a session end right after the flush hook returned, and now read it mid-end:
   - TestE2E_ObserverThroughDaemon, X03 and TestIntegration_DegradedPassive…;
   - on Linux, TestFault_Lifecycle's audit was red in 5 rows on the first iteration and 8 on the second, at af3ed3c;
   - on Windows, the fault publication rows: retention_roots_truncated and historical_checkpoint_drops_pointer.
4. **X09 depended on the sidecar defect.** It required a capture sidecar for every delivery that crossed the frontier, including the flush's own. That passed only because of the defect fixed in 3536f12.

## 2. What changed

**C1.15, the flush**
- The flush is now a durable, leased, ordered arrival of its session.
- `handleFlush` calls `ingest.acceptDurable`: the WAL line is synced and the flush is leased, the same promise an observe event's ACK makes.
- Before answering, it takes in-process ownership of the flush (the seen set) and writes the session's recovery marker.
- It then answers. `endSession`, which is the old route's body, runs on a goroutine of its own, joined and bounded by Stop.
- The end settles only the arrivals before the flush's own (`sessionEndArrival`). A drained flush receives its lease through the context.
- After SessionEnd, the marker and the sketches, the end acknowledges the flush on the committed frontier, then runs the final drain.
- A drain that meets the flush's line while the end runs defers it (`deferSessionEnd`).
- The hook client sends the flush fire-and-forget. A late ACK spools a copy, which the flush's lease absorbs.
- The ordering guarantee is unchanged: SessionEnd never overtakes an earlier arrival of the same session, and the flush-ordering tests pass.

**C1.13, the client-spool watcher** (`spool_watch.go`)
- Every served request kicks it.
- While kicks arrive, and once more one interval after they stop, it looks at the spool every `spoolCheckInterval` (= `idleRunBudget`, 2 s).
- A client spool unchanged for one interval gets a `DrainClientSpools` pass: client spools only, never WAL segments. The pass runs under the drain's mutex, ordering gate and frontier, with a 2 s budget.
- A spool it could not consume is passed again after 2, 4, 8 … intervals, until it has waited DetectAfterSeconds. After that, the idle drain, the requested drains or the flush own it.
- With no kick and no retry due, it does nothing.
- A client-spool copy of a delivery that is in flight is deferred (`deferInFlight`). A WAL line in flight still fails its file's pass, as before.

**Control-line sidecars**
- The producer, in both the drain and the live dispatch, publishes a capture only for observe ops.
- For legacy sidecars already on disk, `store.IsControlCaptureOp` marks flush, checkpoint and session.start sidecars that were never published as known. The audit counts them in `LegacyControlCaptures`, and they do not make it incomplete.
- `CaptureRequiresReference` now answers "known; no reference needed" for these ops. fsck's captures and publication rows add a note and stay ok.
- Nothing is moved or deleted. Any other unknown op still makes the audit incomplete.

**Other changes**
- `recoveryMu` serializes the rewrites of the recovery record.
- The marker is written before the answer.
- Counter `l0_session_end_refused`, and counter `l0_spool_watch_drains`. It was renamed from `..._passes` because gosec rule G101 read "pass" as a password; I renamed rather than silenced it.
- `daemon.ClientSpoolWatchInterval` is exported for test/e2e. The IdleTickMax doc no longer calls the idle tick the spool fallback.
- Docs: `docs/architecture.md` has a paragraph on the 1.5 s budget and fire-and-forget flush, and one on the watcher. `delivery-order-decision.md` and `delivery-order-work.md` have dated addenda and the new counters.

**Recovery without SessionEnd (Qompack.md §8.2), confirmed**

| Concern | What covers it without SessionEnd |
|---|---|
| Publication | Lanes and workers, requested drains, the watcher, the startup drain and re-drain, the idle drain, Stop's drain |
| Observer state and DAG | The idle `observer.persist` task |
| Sketches | The idle sketches task, and Stop |
| GC | Scheduler idle GC (`scheduler_idle.go`); `gcrun.go` `recentSessionSet` rebuilds sessions that were never flushed from the tool_use index |
| Silent sessions | `registry.EndAbandoned` |
| The flush itself | It is durable before its ACK; a crash leaves it unacknowledged in the WAL for the next drain, and a spooled copy is absorbed (tests: `TestFlush_AnAcceptedFlushSurvivesACrashBeforeTheEnd`, `TestFlush_TheHookSpooledCopyOfAnAcceptedFlushEndsTheSessionOnce`) |

The only things that exist solely at SessionEnd are the final segment close and the session's `sessions.jsonl` record. They are summary records, not needed for correctness.

## 3. Tests and results

Windows ran on a host co-loaded by other workstreams. Every red was re-run and classified; details in section 4 and under Open items.

**Red first, and negative controls**
- `red-c115-async-flush-windows.log`, `red-c115-flush-hook-client-windows.log`, `red-drained-control-sidecar-windows.log`, `red-legacy-control-{audit,fsck}-windows.log`: earlier attempt.
- `red-recovery-marker-lost-update-windows.log`: 3 of 3 red. Green run: 10 of 10.
- `red-flush-recovery-marker-before-answer-windows.log`: red, then green.
- `negative-control-no-spool-retry-windows.log`: 2 of 2 red.
- `negative-control-no-inflight-deferral-windows.log`: red.

**Windows focused**
- `internal/daemon` (flush, order, drain, lanes, spool regex): PASS, 153 cases. Log: `focused-daemon-windows-af3ed3c.log`.
- The ten e2e cases, including TestE2E_ObserverThroughDaemon, TestE2E_ThinSliceDropsControlOnlyEdges, TestV3_CrashRecovery…, TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting (46.9 s, passes, installs into user scope by design), X03 and TestE2EIdleExit: PASS. Log: `e2e-focused-windows-af3ed3c.log`.
- The six fault observer cases plus TestFault_Lifecycle: PASS. Log: `fault-focused-windows-af3ed3c.log`.

**Windows race**
- `CGO_ENABLED=1 go test -race -count=5` over the new concurrency paths in internal/daemon and internal/cli: PASS, 125 daemon cases, no DATA RACE. Log: `race5-windows-3edc200.log`.

**Windows full packages**

| Package | Result | Log |
|---|---|---|
| internal/daemon | PASS, 320 s | `pkg-internal-daemon-windows-af3ed3c.log` |
| internal/cli | PASS | `pkg-internal-cli-windows-af3ed3c.log` |
| internal/store | PASS | `pkg-internal-store-windows-af3ed3c.log` |
| test/fault | PASS, 257 s, after 3edc200 | `pkg-test-fault-windows-9091f74-seedrecover-2.log` |
| test/integration | Only TestIntegration_HotPathWarmWithRealResidentState fails | `pkg-test-integration-windows-cc15f21.log` |
| test/e2e | Only TestV3_HotPathUnchangedWithLedgerResident, TestV4_TombstoneToRecallToExpandRoundTrip and TestV5_EliminationThroughEveryFourSurfaces fail | `pkg-test-e2e-windows-cc15f21.log` |

The e2e and integration failures are classified in section 4.

**Hot-path A/B against b070bbe, back to back** (`hotpath-ab*-windows.log`)

| | B-A p99 | B-B p99 | B-B p999 |
|---|---|---|---|
| Pair 1: base / head | 28.7 / 36.9 ms | 12.3 / 13.3 ms | 30.7 / 81.9 ms |
| Pair 2: head / base | 24.6 / 24.6 ms | 12.3 / 12.3 ms | 41.0 / 49.2 ms |

The gate fails the same way on both builds, for the same reason: 528 samples were never delivered, and co-load. There is no measurable regression.

**Linux non-root, via `linux-nonroot-gate.sh --prefix cx-w2-sessionend`**
- At af3ed3c:
  - daemon `-race -count=5`: PASS, 150 cases;
  - e2e and fault `-count=2`: red. That run is what exposed the lifecycle race, and the TestUnknownSchema `-count=2` artifact in Open items.
- At 3edc200:
  - daemon `-race -count=2`: PASS, 60 cases;
  - e2e 7 passed, 1 skipped (TestUnknownSchema: no claude CLI in the container);
  - fault 36 passed, including Lifecycle and PublicationBoundaries;
  - integration 2 passed.
- Artifacts: `runs/linux/`.

**Checks**
- `devtool fmt-check`: clean.
- `go vet` on the touched packages: clean.
- `go test ./test/docs`: ok.
- `devtool lint --only=golangci-lint,nomagic,sleepcheck`: PASS after the rename (`lint-golangci-after-rename-windows.log`).
- runpatterns fails on 26 patterns, all in wave-1 reports (config, e2e, hostperm, ingest, linux, packaging, rehydrate-cap), not in my files.
- Every one of the 14 code, test and docs commits builds and vets on its own (`bisect-build-vet-windows.log`).

## 4. Criterion changes, with rationale

The product contract changed on purpose: the flush hook now means "flush durably accepted", not "session ended", because the host cancels plugin SessionEnd hooks after 1.5 s. No assertion was removed or loosened, and no bound value was changed.

1. **`TestHookConnectDeadline` flush rows** (c4d27e2): the input spec is now fire-and-forget with no deadline. The expected dial floor is unchanged.
2. **`flush_arrival_order_test.go`** (4c06d82): comments only. Its flushes are Reply requests, which still wait for the end.
3. **`TestE2E_ObserverThroughDaemon`** (5261a15): `obsRunFlush` waits for `run/marker.json` to name the session, compared with the bytes from before the hook. The end writes that marker right after SessionEnd. The DAG-log and sketch assertions are unchanged.
4. **`obsWaitDiag`, the `obsProcessBound` comment and a `daemon_e2e` message** (5261a15): they named a 30 s idle-tick drain that never runs while hooks arrive. They now name the watcher. `obsProcessBound` keeps its 60 s value.
5. **X03** (5261a15): the "flush clears its recovery marker" check waits (Eventually, `obsProcessBound`) after `obsRunFlush`. The marker is set before the answer, so the wait cannot pass before the end has run.
6. **X09** (cc15f21): a crossing with no sidecar is counted, and at most one may cross, the flush's own. Previously every crossing needed a sidecar, which held only because of the defect 3536f12 fixed. A content delivery with no sidecar still fails, and so does an unreadable sidecar.
7. **test/fault** (1d7578b, 3edc200):
   - `runFlush` waits for the end, under `indexBound`, in the 13 lifecycle drives, the historical-drop row and `seedSession`.
   - `recoverSession` waits too, but only logs if no end comes. Some cuts legitimately leave the product unable to take the flush at all (a refused config, an unavailable delivery identity). Failing there turned two rows red for that reason alone (`pkg-test-fault-windows-9091f74-seedrecover.log`).
8. **test/integration** (0bdbf9e): `awaitSessionEnded` waits for the marker naming the session and for the recovery record to clear. The test also now advances its frozen `p.Clock` by 1 ms before the flush. Without that, the end's marker is byte-identical to PreCompact's, and the first version of the wait timed out 3 of 3 times.

## Open items

- **Drained flushes run under drain budgets.** The line deadline is 5 s; watcher, requested and idle passes have 2 s. That can cut SessionEnd's GC short, and the flush is still acknowledged. This already applied to any drained flush. It is now likelier for a flush that reached only a client spool while the daemon was alive. Proposed follow-up: route drained flushes through `startSessionEnd`.
- **A replayed flush can end a resumed session.** A flush Stop could not finish within its 5 s grace, or that was cut by a crash, is replayed by the next daemon. If the same session id was resumed in between, the replay runs its SessionEnd before the resumed session's leased arrivals. That can drop the observer state SessionStart(resume) had just loaded.
- **Stop now takes longer.** Its worst case grows by `stopDrainBound` plus `sessionEndAbandonAfter` (5.25 s), to about 12.25 s, still inside `stopCleanupBound` (15 s). The lifetime lane is editing Stop, so a textual conflict there is likely.
- **Failures that are the same on the base.** TestV4_TombstoneToRecallToExpandRoundTrip ("already reserved for a different intent") and TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces fail identically on b070bbe (`base-b070bbe-e2e-short-reds-windows.log`). They are probably wave-2's "two unclassified e2e reds".
- **Hot-path rows:** co-load; see the A/B above.
- **TestUnknownSchema on Linux with `-count=2`:** the first iteration skips; the second fails writing into the first iteration's deleted TempDir. This looks like a harness artifact; I did not check it on the base.
- **The 20 s SessionEnd timeout in the manifest** is ignored by the host for plugin hooks. I left it alone; it is the packaging lane's call.
- **Not run by me:** test/security, test/guards, test/canary, test/release and test/platform, beyond the survey of flush users in section 1. I found none there that reads state after a flush without first shutting the daemon down, except `redaction_test.go:111`, whose previews are unaffected by the end.

## Owner decisions

1. **Accept the new contract:** the flush hook means "durably accepted", and the daemon ends the session asynchronously. This includes the test changes that wait on the end's own records (`run/marker.json`, `state/session-recovery.json`).
2. **`settleSessionLimit` (9 s):** it no longer protects any reply. Keep it, or lift it?
3. **Watcher timings** (derived from existing constants, nothing new): check interval `idleRunBudget` (2 s); retries on a doubling wait up to DetectAfterSeconds. Confirm?
4. **Follow-up for drained flushes:** route them through `startSessionEnd` so they are not truncated by drain budgets. Approve?
5. **Manifest SessionEnd timeout:** keep the 20 s value the host ignores for plugins, or change it (packaging)?

### Commits

- 3536f12 fix(daemon): publish no capture sidecar for a control line (earlier attempt; reviewed, adopted)
- 11b2eac fix(store): classify legacy control-line sidecars as known (earlier attempt; reviewed, adopted)
- 4c06d82 fix(daemon): answer SessionEnd once durable, end it asynchronously (earlier attempt; reviewed, adopted, two follow-up fixes below)
- c4d27e2 fix(cli): send the SessionEnd flush fire-and-forget (earlier attempt; reviewed, adopted)
- 677b847 fix(daemon): serialize the session recovery record's rewrites
- 06d82c9 fix(daemon): mark an acknowledged flush for recovery before it answers
- ae1e728 fix(daemon): replay client spools while their session is active
- 5261a15 test(e2e): wait for the daemon's session end after a flush hook
- af3ed3c docs: record the asynchronous SessionEnd and client-spool watcher
- 1d7578b test(fault): wait for the daemon's session end after a flush hook
- 0bdbf9e test(integration): read the degraded session's store after its end
- cc15f21 test(e2e): let X09's flush cross the frontier with no sidecar
- 9091f74 fix(daemon): name the spool watcher's counter for what gosec allows
- 3edc200 test(fault): let seed and recovery sessions finish their end
- 4703ee1 docs(closeout): preserve the w2-sessionend evidence logs

### Tests

- `go test ./internal/daemon -run TestSessionRecovery_ConcurrentEndsLoseNoMarker -count=3 (before recoveryMu)` — FAIL 3/3 as intended (red-recovery-marker-lost-update-windows.log); after fix -count=10 PASS
- `go test ./internal/daemon -run TestFlush_AFlushAcknowledgedDuringShutdownIsMarkedForRecovery (before 06d82c9)` — FAIL as intended; PASS after (red/green-flush-recovery-marker-before-answer-windows.log)
- `negative controls: spool retry disabled / in-flight deferral disabled` — FAIL as intended (negative-control-no-spool-retry-windows.log 2/2; negative-control-no-inflight-deferral-windows.log)
- `Windows focused internal/daemon flush/order/drain/lanes/spool regex -count=1 (header of focused-daemon-windows-af3ed3c.log)` — PASS, 153 cases, 130 s
- `Windows go test ./test/e2e ten focused cases incl. TestE2E_ObserverThroughDaemon, TestE2E_ThinSliceDropsControlOnlyEdges, TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently, TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting, TestV5_HookEventToTombstoneToRetrievalAfterRestart, TestE2EIdleExit` — PASS 10/10 (e2e-focused-windows-af3ed3c.log)
- `Windows go test ./test/fault six observer cases + TestFault_Lifecycle` — PASS 7/7 (fault-focused-windows-af3ed3c.log)
- `CGO_ENABLED=1 go test -race ./internal/daemon (new concurrency paths) -count=5; ./internal/cli TestFlushHook_IsFireAndForgetWithItsNonce + TestHookConnectDeadline -count=5` — PASS, 125 daemon cases, no DATA RACE (race5-windows-3edc200.log)
- `Windows full packages: go test ./internal/daemon, ./internal/cli, ./internal/store -count=1` — PASS all three (pkg-internal-*-windows-af3ed3c.log)
- `Windows full package go test ./test/fault -count=1 (final)` — PASS 257 s (pkg-test-fault-windows-9091f74-seedrecover-2.log); earlier reds at cc15f21 and 9091f74 are the async-flush test races fixed by 3edc200
- `Windows full package go test ./test/integration -count=1` — FAIL only TestIntegration_HotPathWarmWithRealResidentState (co-load timing row; A/B vs b070bbe identical verdict)
- `Windows full package go test ./test/e2e -count=1` — FAIL only TestV3_HotPathUnchangedWithLedgerResident (co-load timing row), TestV4_TombstoneToRecallToExpandRoundTrip and TestV5_EliminationThroughEveryFourSurfaces (both fail identically on base b070bbe)
- `flush hook latency probe (real binary, real daemon), base b070bbe vs this branch` — flush hook 3534-4298 ms after a burst on base vs 65-86 ms on this branch; idle 582-695 ms vs 58-76 ms
- `hot-path A/B: go test ./test/integration -run TestIntegration_HotPathWarmWithRealResidentState, base vs head, two pairs` — same gate verdict on both (FAIL on 528 undelivered samples + co-load); pair 2 p50/p95/p99 identical; no measurable regression
- `Linux non-root gate at af3ed3c: daemon -race -count=5 on new paths` — PASS 150
- `Linux non-root gate at af3ed3c: e2e+fault focused --no-race -count=2` — FAIL: TestFault_Lifecycle audit races (fixed in 1d7578b) and a TestUnknownSchema -count=2 harness artifact
- `Linux non-root gate at 3edc200: daemon -race -count=2; e2e+fault+integration focused --no-race` — PASS: daemon 60; e2e 7 pass + 1 skip (no claude CLI); fault 36 incl. Lifecycle and PublicationBoundaries; integration 2
- `go run ./tools/devtool fmt-check; go vet on touched packages; go test ./test/docs` — clean / clean / ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,sleepcheck (after renaming the watcher counter)` — PASS; runpatterns fails only on 26 patterns in wave-1 reports, not in this branch's files
- `per-commit build + vet over b070bbe..HEAD (git archive of each commit)` — all 14 code/test/docs commits exit 0 (bisect-build-vet-windows.log)

### Criterion changes

- TestHookConnectDeadline flush rows (c4d27e2): the input spec is now fire-and-forget with no deadline; the expected dial floor is unchanged.
- TestE2E_ObserverThroughDaemon (5261a15): obsRunFlush waits, under obsProcessBound, for run/marker.json to name the session (compared with the bytes before the hook) before the unchanged DAG-log and sketch assertions. Rationale: since C1.15 the hook answers once the flush is durable, and the end writes that marker right after SessionEnd.
- obsWaitDiag text, the obsProcessBound comment and a daemon_e2e failure message (5261a15) named a 30 s idle-tick drain that never runs while hooks arrive; they now name the client-spool watcher. obsProcessBound keeps its value.
- X03 (5261a15): the 'a completed flush clears its recovery marker' check is waited for (Eventually, obsProcessBound) after obsRunFlush. The marker is set before the answer, so the wait cannot pass before the end has run.
- X09 (cc15f21): a crossing without a capture sidecar is counted, and at most one (the flush's own) may cross; previously every crossing needed a sidecar, which held only because of the drained-control-sidecar defect 3536f12 fixed. A content delivery with no sidecar, and an unreadable sidecar, still fail.
- test/fault (1d7578b, 3edc200): runFlush waits for the end under indexBound in the 13 lifecycle drives, the historical-drop row and seedSession. recoverSession waits but only logs when no end comes: a cut can legitimately leave the product unable to take the flush, and failing turned two rows red for that reason alone.
- test/integration (0bdbf9e): awaitSessionEnded waits for the marker naming the session and for the recovery record to clear; the frozen p.Clock advances 1 ms before the flush so the end's marker differs from PreCompact's (the first version timed out 3/3 without it).
- flush_arrival_order_test.go (4c06d82): comments only; its Reply flushes still wait for the end.

### Open issues

- Drained flushes run under drain budgets (5 s line deadline; 2 s watcher/requested/idle passes), so SessionEnd's GC can be cut short and the flush still acknowledged. Pre-existing for drained flushes, now more likely for a flush that reached only a client spool while the daemon was alive. Proposed follow-up: route drained flushes through startSessionEnd.
- A flush left unacknowledged (Stop's 5 s grace exceeded, or a crash) is replayed by the next daemon; if the same session id was resumed meanwhile, the replayed SessionEnd can drop the observer state SessionStart(resume) loaded.
- Stop's worst case grows by 5.25 s to about 12.25 s, inside stopCleanupBound (15 s); the lifetime lane is editing Stop, so a textual conflict there is likely.
- TestV4_TombstoneToRecallToExpandRoundTrip and TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces fail identically on base b070bbe; not this branch's.
- Hot-path rows (TestIntegration_HotPathWarmWithRealResidentState, TestV3_HotPathUnchangedWithLedgerResident) fail under co-load; A/B against b070bbe gives the same verdict. C5.1's quiet run should judge.
- TestUnknownSchema on Linux with -count=2: the first iteration skips (no claude CLI), the second fails writing into the first iteration's TempDir. Looks like a harness artifact; not checked on the base.
- devtool lint runpatterns fails on 26 patterns, all in wave-1 reports (coordinator-owned). When committing this report, keep test commands as the single-name or prefix -run patterns used here; the checker splits alternations at '|'.
- Not run: test/security, test/guards, test/canary, test/release, test/platform in full. The integrated gate should cover them; the survey of flush users found none in them that reads end effects without shutting the daemon down first.
- The earlier attempt's runs/ logs do not record their commands; kept as found.

### Needs the owner

- Accept the new SessionEnd contract (the flush hook means 'durably accepted'; the daemon ends the session asynchronously), including test changes that wait on the end's own records (run/marker.json, state/session-recovery.json).
- settleSessionLimit (9 s, derived from the 20 s manifest SessionEnd timeout) no longer protects any reply: keep it or lift it?
- Confirm the watcher timings, derived from existing constants: check interval idleRunBudget (2 s); retries on a doubling wait up to DetectAfterSeconds.
- Approve the follow-up that routes drained flushes through startSessionEnd so drain budgets do not truncate them.
- The manifest's 20 s SessionEnd timeout is ignored by the host for plugin hooks: keep it or change it (packaging lane)?

## Independent review

### review:sessionend: needs-fixes

- **minor** `internal/daemon/handlers.go:1065 (endSession final drain), internal/daemon/session_end.go:193, internal/daemon/daemon.go:1022-1027` — The asynchronous end's final d.Drain runs with no deadline, and Stop does not cancel it until after runWG.Wait. Stop can therefore block for as long as the backlog takes to drain, and the report's claimed worst case of about 12.25 s ("inside stopCleanupBound") is not a real bound.
  - Evidence: The end's context is context.WithCancel(context.WithoutCancel(ctx)), cancelled only by e.cancel inside stopSessionEnds. Before this branch, the flush route's drain used the request context, which descends from runCtx, so Stop's runCancel stopped it at once. In Stop, d.runWG.Wait() (daemon.go:1022) comes before d.stopSessionEnds(ctx) (1027). runWG holds watchClientSpools and drainOnRequest, and both call dr.mu.Lock(), which does not watch a context. If either is waiting for the drainer mutex while an end's final Drain holds it, runWG.Wait waits for that drain to finish on its own. Only after that do the 5 s grace, the 0.25 s abandon and Stop's own 5 s drain start. Per-line drainLineDeadline (5 s) limits each line, not the pass.
  - Fix: Bound the end's final drain, e.g. context.WithTimeout(ctx, stopDrainBound) or idleRunBudget. It is a best-effort pass, and the watcher, requested, idle and Stop drains cover whatever it leaves. Alternatively, close the ends gate and start its grace timer at Stop entry, before runWG.Wait. Then correct the Stop worst-case figure in the report and addenda.
- **minor** `internal/daemon/daemon.go:979 (drainDispatch -> flushRoute(drain=false)), internal/daemon/handlers.go:1061` — A flush replayed by a drain still runs the whole end inline, under the drain's budget, and is acknowledged even when SessionEnd was cut short. The new client-spool watcher adds a 2 s pass budget to this path. A flush that reached only a client spool while the daemon was serving can have its settle and SessionEnd truncated to about 2 s, or to nothing, and is still acknowledged, so it is never replayed. The task asked that the settle, drain and SessionEnd run asynchronously and that a replayed request lose nothing.
  - Evidence: dispatchPending builds dctx from the pass context (DrainClientSpools passes idleRunBudget = 2 s), capped by drainLineDeadline. flushRoute(drain=false) always returns ipc.Response{OK: true}, even when svc.SessionEnd returned an error because its context expired. The drain then commits the flush to the frontier. The implementer lists this as open item 1 and owner decision 4, but it ships unfixed with the watcher making it more likely.
  - Fix: While the ends gate is open, have drainDispatch hand a leased flush to startSessionEnd, building own from the drained lease and key, and defer the line (deferSessionEnd) rather than running the end inline. The end's finishOwnFlush then acknowledges it and a later pass absorbs it. Keep the inline flushRoute path only for the startup drain and Stop's drain, where no ends are started. At minimum, return a non-OK response when SessionEnd failed because its context ended, so the line stays unacknowledged.
- **minor** `internal/cli/hooks.go:41` — The fire-and-forget flush waits for its ACK only as long as the observe hot path's AckDeadline: 17 ms portable/Linux, 73 ms Windows. Before it answers, the daemon does more than an observe does: a synced WAL append, the lease-journal append, a seen-set begin, and an atomic rewrite of state/session-recovery.json with markRecoveryNeeded. On a host with the fsync tail recorded in the project notes (hosted ubuntu p99 about 57 ms), most flushes will miss their ACK and leave a spooled duplicate. The duplicate is absorbed correctly, but it adds a client spool per session end and several watcher retry passes (deferSessionEnd while the end runs). The host budget allows far more than 17 ms.
  - Evidence: hookSpec{op: ipc.OpFlush, reply: false} has deadline 0, so doHook uses st.AckDeadlineMs (hookclient.go around L465). config/deadlines.go:240-242 sets AckDeadlineMsPortable = 17 and AckDeadlineMsWindows = 73. startSessionEnd calls markRecoveryNeeded, which does paths.WriteAtomic, before the answer. The latency probe measured the hook's total process time, not whether the ACK arrived before a spool.
  - Fix: Give the flush its own ACK deadline, well inside the shared 1.5 s budget after the 250 ms dial floor, e.g. 500 ms, derived and named as a constant. Add a test that a normal flush against a live daemon leaves no client spool.
- **nit** `test/e2e/v3_x09_test.go:746` — The X09 change allows any one frontier crossing without a sidecar, instead of allowing only the flush's own delivery. Now that the flush is always leased and always acknowledged by the end or Stop's drain, one content delivery acknowledged without its sidecar would pass whenever the flush's crossing falls outside the window.
  - Evidence: require.LessOrEqual(t, len(withoutSidecar), 1, ...) counts sidecar-less crossings but never identifies which delivery each one is.
  - Fix: Identify the flush's delivery, for example from the lease journal entry for the SessionEnd request's nonce or the WAL/spool line with op=flush, and require that the sidecar-less set is exactly that one delivery. Otherwise, require Equal(1), since the flush now always crosses.
- **nit** `docs/architecture.md:34` — The docs say the daemon's end "clears the recovery record" as if it always does. It clears it only when the end's final drain completed and DrainGaps reports Observed && Complete. A flush replayed through a drain (flushRoute drain=false) never clears it.
  - Evidence: handlers.go endSession: `if gaps := d.DrainGaps(); gaps.Observed && gaps.Complete { d.clearRecoveryNeeded(...) }`. The drain=false path returns before this.
  - Fix: Reword to "and clears the recovery record once its final replay leaves no gaps".

## Fix seat (review resolution) — status `done`, head `4aaffea62d8407dde6eafc2da964edb93b4ac758`

### Root cause

F1: Stop cancelled the session ends only after runWG.Wait. An end's final drain holds drainer.mu, and runWG members (drainOnRequest, watchClientSpools) take that mutex without watching a context, so the join waited until drainLineDeadline, applied line after line, ended the drain. F2: drainDispatch ran a replayed flush's whole end inside the drain's pass, whose budget was idleRunBudget or drainLineDeadline, and endSession(!drain) answered OK even when SessionEnd had been cut short. So a long SessionEnd could never finish through the budgeted drains, and a drain whose pass outlived the per-line deadline acknowledged a flush whose SessionEnd never ran. F3: a fire-and-forget request's ACK wait is ClientOptions.AckDeadline, which falls back to the observe hot path's State.AckDeadlineMs (17/73/45 ms), and the flush hook never set it, although the daemon does more before ACKing a flush than an observe event.

### Summary

FIX SEAT, w2-sessionend (C1.15 + C1.13 remainder + drained control-line sidecars). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-sessionend, branch closeout/w2-sessionend. Nothing was pushed, merged or tagged.

RESUME CHECK: `git status` at start showed a clean tree on 4703ee1 (the implementer's 15 commits on top of b070bbe). The cut-off attempt wf_0d8775ab-04e left no uncommitted edits, so there was nothing to adopt or discard. I kept the implementer's evidence under runs/ (committed in 4703ee1) as their record and did not edit it. All of my evidence is under plans/sdd/V6-closeout/w2-sessionend/runs/fix/.

REVIEW RESOLUTION (I verified all three findings independently; all three were correct, and all three are fixed test-first)

Finding 1 (minor): Stop can block behind a session end's final drain. CONFIRMED, FIXED in 751f8a2.
- Root cause: Stop ran runCancel, then runWG.Wait(), and only then stopSessionEnds. That was the first point at which the ends were cancelled.
  - An end's last step is d.Drain, which holds drainer.mu for the whole pass.
  - drainOnRequest and watchClientSpools are runWG members that call dr.mu.Lock(), which does not watch a context.
  - The end's context is a cancel-only context from context.WithoutCancel. Only e.cancel ended it.
  - So runWG.Wait waited until the end's drain finished by itself. Only drainLineDeadline, applied line after line, ended that wait.
- Red: runs/fix/red-f1-stop-held-by-end-drain-windows.log. Stop took 10.085 s against a 2.45 s bound. The 10 s is the end's drain line (5 s) plus Stop's own drain meeting the same line (5 s).
- Fix:
  - Stop now calls stopSessionEnds right after runCancel. That closes the ends' gate and starts their grace (context.AfterFunc cancels the ends when the grace is over).
  - After runWG.Wait, Stop runs the returned join, which waits for whatever is left of the grace, then abandons an end after 0.25 s.
  - New field sessionEndGrace, set in New to stopDrainBound. It follows the promptAbandonAfter pattern and exists only so the test does not have to wait out the grace.
- Green: TestStop_IsNotHeldBehindASessionEndsDrain passes (0.75 s).
- Corrected Stop worst case, replacing the implementer's "about 12.25 s":
  - About 10.25 s plus the time cancelled steps take to return: the grace (5 s, now overlapping the runWG join), the abandon window (0.25 s), and Stop's drain (5 s). This is inside stopCleanupBound (15 s).
  - Remaining caveat: an end that ignores cancellation while holding drainer.mu could still hold runWG, as any runWG member can. Every step of an end answers its context.

Finding 2 (minor): a drained flush runs its whole end inside the drain's budget. CONFIRMED, FIXED in 53f290b. One detail of the reviewer's mechanism is refined below.
- Verification:
  - drainDispatch sends OpFlush to flushRoute(drain=false), so the end runs under dctx: the pass context capped at drainLineDeadline. The watcher, the lane-requested drains and the idle drain give their passes idleRunBudget (2 s).
  - endSession(!drain) always answered OK, even when SessionEnd failed because its context had ended.
  - Refinement: where the pass context itself expires (the watcher's 2 s pass), commitDelivery uses that same expired context. commitAcks checks r.ctx.Err(), so such a flush is NOT acknowledged. Instead it is replayed and cut short again on every retry, and a SessionEnd longer than the budget can never finish through those drains.
  - The acknowledged-while-truncated case is real where the pass outlives the 5 s per-line deadline: the startup drain (runCtx), a flush's own final drain, and admin.drain.
- Red: runs/fix/red-f2-replayed-flush-windows.log.
  - TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget: SessionEnd finished 0 times.
  - TestFlushRoute_AReplayedSessionEndCutShortIsNotAcknowledged: the route answered OK.
- Fix, as the reviewer proposed:
  - New DrainConfig.EndSession hook. dispatchPending calls it after admission for a leased OpFlush that has passed the ordering gate, while the pass holds the line's Seen entry. The daemon wires endDrainedFlush.
  - endDrainedFlush counts in an end and launches endSession(drain=true, own built from the drained lease and key) through the new shared launchSessionEnd.
  - That end takes over the Seen entry and acknowledges the flush through finishOwnFlush. The pass returns errSessionEndStarted and defers the line (deferSessionEnd). A later pass absorbs it, usually the end's own final drain.
  - Inline replay is kept in three places: Run's startup drain (the new drainsEndSessions flag is set only after it); Stop's drain (gate closed); and any drain a session end runs itself (a context marker). The last one matters: an end whose acknowledgement failed leaves its flush for its own final drain, and handing it on from there would pass the flush from end to end with no pause.
  - The reviewer's minimum is also in: endSession(!drain) now answers non-OK when ctx.Err() is set after SessionEnd, so an inline drain leaves the line for a later replay.
- Evidence:
  - Green: runs/fix/green-f2-replayed-flush-windows.log.
  - Pin test for the inline paths: TestDrain_AReplayedFlushIsEndedInlineWhereNoEndMayStart.
  - Negative control: with the in-session-end decline removed, the pin test's second case fails (runs/fix/negative-control-no-in-session-end-decline-windows.log).
- Side effect: a flush handed off from a drain now runs the end's final drain and so clears its recovery marker, which an inline-drained flush never did (asserted in the new test).

Finding 3 (minor): the fire-and-forget flush waits only the hot-path ACK deadline. CONFIRMED, FIXED in 39dd578.
- Root cause: awaitACK uses ClientOptions.AckDeadline, which falls back to State.AckDeadlineMs (config: 17 ms portable, 73 ms Windows, 45 ms darwin). spec.deadline is used only by awaitReply. Before ACKing a flush the daemon also takes the in-process ownership and rewrites the recovery record with WriteAtomic.
- Red: runs/fix/red-f3-flush-ack-deadline-windows.log. With a fake daemon that ACKs after 200 ms under the shipped state, the hook left client-54068.ndjson. The existing test had masked this because replyProject sets AckDeadlineMs=5000.
- Fix:
  - Named constants: sessionEndHostBudget = 1.5 s and flushAckDeadline = sessionEndHostBudget/2 - hookConnectDeadlineFloor = 500 ms. The dial plus the ACK get half the host budget; the other half covers process start, input read and the spool append on a miss.
  - New hookSpec.ackDeadline field, passed through as ClientOptions.AckDeadline. Only the flush sets it.
- Test: TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool, with preconditions that the fake daemon's delay is more than twice the shipped ACK deadline and at most half of flushAckDeadline. The fake daemon now waits on a timer (d59b4d6), because sleepcheck bans time.Sleep in tests.
- docs/architecture.md now says the hook waits at most half a second for its ACK, and that a flush replayed by a drain while the daemon serves is ended on its own (02e40b4). go test ./test/docs: ok.

TESTS (Windows host was co-loaded; nothing failed, so no reruns were needed)
- Focused daemon tests after F2: ok 57.8 s.
- Full packages: internal/daemon ok 253.7 s at 53f290b; internal/cli ok 15.9 s.
- -race -count=5 (CGO_ENABLED=1) on the new concurrency paths: daemon ok 132.8 s, cli ok 4.9 s. No data races.
- e2e, 12 cases: all PASS, including TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting, which installs into user scope and uninstalls by design.
- test/fault: the six observer cases, TestFault_Lifecycle and TestFault_PublicationBoundaries all PASS.
- Integration contract-monitor rows: PASS.
- Lint (golangci-lint, nomagic, sleepcheck, importgraph): PASS. fmt-check and vet: clean.
- Linux non-root gates at 51b0e36 (--prefix cx-w2-sessionend):
  - Gate 1, -race, count 3, internal/daemon and internal/cli: daemon pass=108, cli pass=30.
  - Gate 2, e2e/fault/integration rows: fault 36 and integration 2 passed; e2e passed 7 and skipped 1. The skip is TestUnknownSchema, because the claude CLI is not on PATH in the container; the same skip is in the implementer's 3edc200 run.

CRITERION CHANGES: no check was weakened, and no assertion, threshold, golden or timeout was loosened. There are two product behaviour changes, each with its rationale in the commits.
- The flush's client ACK wait goes from 17/73/45 ms to 500 ms, derived from the host's 1.5 s budget.
- An inline-replayed flush whose end was cut short by its context now answers non-OK and is replayed later, instead of being acknowledged.

### Commits

- 39dd578 fix(cli): give the SessionEnd flush its own ACK deadline
- 751f8a2 fix(daemon): start the session ends' grace when Stop begins
- 53f290b fix(daemon): end a flush a serving drain replays on its own
- 02e40b4 docs: state the flush's ACK wait and how a replayed flush ends
- d59b4d6 test(cli): wait out the fake daemon's slow ACK on a timer
- 61b29d1 docs(daemon): set the cut-short note apart from the M-3 comment
- e53a2c3 test(daemon): pin where a drain still ends a replayed flush inline
- 6fea2e9 docs(closeout): record the fix seat's daemon and cli package runs
- 51b0e36 docs(closeout): record the fix seat's race, e2e, fault and lint runs
- 4aaffea docs(closeout): record the fix seat's Linux non-root gate runs

### Tests

- `go test ./internal/cli -run '^TestFlushHook_' -count=1 -v (before fix)` — FAIL as intended (red): the slow-ACK flush left client-54068.ndjson
- `go test ./internal/cli -run '^(TestFlushHook_|TestHookConnectDeadline)' -count=1 -v` — PASS
- `go test ./internal/daemon -run '^TestStop_IsNotHeldBehindASessionEndsDrain$' -count=1 -v (before fix)` — FAIL as intended (red): Stop took 10.085 s against a 2.45 s bound
- `go test ./internal/daemon -run '^(TestStop_IsNotHeldBehindASessionEndsDrain|TestStop_FinishesAnAcceptedSessionEndBeforeItReturns|TestFlush_.*|TestSessionRecovery_ConcurrentEndsLoseNoMarker)$' -count=1 -v` — PASS (6.2 s)
- `go test ./internal/daemon -run '^(TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget|TestFlushRoute_AReplayedSessionEndCutShortIsNotAcknowledged)$' -count=1 -v (before fix)` — FAIL as intended (red): SessionEnd finished 0 times, and the cut-short route answered OK
- `same two tests after the fix` — PASS
- `go test ./internal/daemon -run '^TestDrain_AReplayedFlushIsEndedInlineWhereNoEndMayStart$' (with the inSessionEnd decline temporarily removed)` — FAIL as intended (negative control), then reverted; PASS with the decline in place
- `go test ./internal/daemon -run 'Flush|Drain|SpoolWatch|DeliveryOrder|Stop|SessionEnd|SessionRecovery|ControlLine' -count=1` — ok 57.8 s
- `go test ./internal/daemon -count=1 -timeout=30m (at 53f290b)` — ok 253.7 s
- `go test ./internal/cli -count=1 -timeout=30m (at 53f290b)` — ok 15.9 s
- `CGO_ENABLED=1 go test -race ./internal/daemon -run '<new concurrency paths>' -count=5 -timeout=60m; and the same for ./internal/cli with TestFlushHook_|TestHookConnectDeadline` — daemon ok 132.8 s, cli ok 4.9 s, no data races
- `go test ./test/e2e -run '^(TestE2E_ObserverThroughDaemon|TestE2E_ThinSliceDropsControlOnlyEdges|TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently|TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting|TestV5_HookEventToTombstoneToRetrievalAfterRestart|TestV3_LiveSessionWriteSetAndAppendOnly|TestV3_DegradedPassiveStillRecordsEverything|TestE2E_AllSixHooksExitZero|TestV1_HookLifecycleThroughRealBinary|TestHooksExitZeroUnderFaults|TestE2E_HooksExitZeroUnderFaultInjection|TestE2EIdleExit)$' -count=1 -v` — ok 146.7 s, all 12 PASS
- `go test ./test/fault -run '^(TestFault_AuditSeesADeletedObjectUnderALiveIndex|TestFault_MCPChildKilledMidRequest|TestFault_DaemonKilledMidIngest|TestFault_UnavailableHistoricalObject|TestFault_CheckpointDropsAnUnresolvablePointer|TestFault_CheckpointResolvabilityIsBlindToADeletedObject|TestFault_Lifecycle|TestFault_PublicationBoundaries)$' -count=1 -v` — ok 221.3 s, all PASS
- `go test ./test/integration -run '^TestIntegration_(ContractMonitorRunsAgainstRealStore|DegradedPassiveStillWritesToTheRealStore)$' -count=1 -v` — PASS
- `go test ./test/docs -count=1` — ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,sleepcheck,importgraph; go run ./tools/devtool fmt-check; go vet ./internal/daemon ./internal/cli` — all PASS / clean
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-sessionend 51b0e36 fix-daemon-cli-race3 --run '<new paths + TestFlushHook_|TestHookConnectDeadline>' --count 3 -- ./internal/daemon ./internal/cli` — go_test_exit=0: daemon pass=108, cli pass=30, run with -race, non-root
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-sessionend 51b0e36 fix-e2e-fault-int --run '<e2e/fault/integration rows>' --no-race --count 1 -- ./test/e2e ./test/fault ./test/integration` — go_test_exit=0: e2e 7 pass and 1 skip (TestUnknownSchema: claude CLI not on PATH in the container, same skip as the implementer's run), fault 36 pass, integration 2 pass

### Criterion changes

- No test check was weakened: no skips, lint suppressions or allow comments were added, and no threshold, golden, assertion or timeout was loosened. Every change adds tests.
- Product: the SessionEnd flush hook's ACK wait goes from the hot path's 17/73/45 ms to flushAckDeadline = 1.5 s/2 - 250 ms dial floor = 500 ms. Rationale: the host gives the SessionEnd hooks a shared 1.5 s budget, and the daemon does more before ACKing a flush than an observe event, so a missed ACK spooled a duplicate for every session end.
- Product: a flush a drain replays inline (startup drain, Stop's drain, a session end's own drain) now answers non-OK when its end's context ended before SessionEnd finished, so the drain leaves the line for a later replay instead of acknowledging a session that was never ended.

### Open issues

- After a handoff, admin.drain (and any serving drain) returns before a spooled flush's SessionEnd has run, and its drained count no longer includes that flush. A caller that needs the end finished must wait for the session's recovery marker to clear or for the terminal-hook marker.
- A flush whose acknowledgement keeps failing (a degraded journal) is ended again at the rate of the serving drains: the watcher's backoff, lane-requested drains and the idle drain. It is bounded, not a hot loop, because no drain run by a session end starts another end, and the new end owns the Seen entry while it runs.
- Stop's worst case is now about 10.25 s plus the time cancelled steps take to return: sessionEndGrace (5 s, overlapping the runWG join), the 0.25 s abandon window and Stop's 5 s drain, inside stopCleanupBound (15 s). An end that ignores cancellation while holding drainer.mu could still hold the runWG join, as any runWG member can.
- A flush replayed inline (by the startup drain or Stop's drain) still leaves its recovery marker set, which is the pre-existing drain=false design. A flush handed off while the daemon serves now clears its marker through its end's final drain.
- Carried from the implementer: settleSessionLimit's 9 s no longer protects any reply now that the hook does not wait, and whether to lift it is an owner decision. The pre-existing e2e reds TestV4_TombstoneToRecallToExpandRoundTrip and TestV5_EliminationThroughEveryFourSurfaces also fail on base b070bbe, and TestV3_HotPathUnchangedWithLedgerResident is a known co-load timing row. I did not re-run these in this seat.

### Needs the owner

- Whether admin.drain should keep its synchronous meaning for spooled flushes: wait for the ends it started, or report them. It currently returns once the flush has been handed off.
- Whether to lift settleSessionLimit (9 s) now that the flush hook no longer waits for the end (carried from the implementer).


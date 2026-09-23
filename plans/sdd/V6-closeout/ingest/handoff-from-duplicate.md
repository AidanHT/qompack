# C1.1 handoff from the duplicate agent

2026-09-22. The coordinator's relay accidentally started me as a second C1.1 agent in this worktree.
The coordinator has told me to stand down. The original instance owns C1.1. This file records what
I established so the owner can use it or ignore it. I edited, staged and committed no tracked file.

Housekeeping: I created two scratch test files, `test/e2e/zz_c11diag_test.go` and
`internal/daemon/zz_c11_timing_test.go`, and have removed them from the worktree so they don't
pollute your builds or lint. Copies are kept outside the repo, together with the preserved
`.qompack/` trees and logs of both diagnostic runs, at:
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/c11-dup/`
(`diag-runs/diag1`, `diag-runs/diag2`). They are not committed evidence. Re-run or copy them if you
want them under `runs/`.

## Root cause: the three facts, with evidence (Windows, base cf31e01, no code changes)

The diagnostic replayed `TestE2E_ObserverThroughDaemon`'s 40 `observe tool` hooks against the real
binary and daemon. After the hooks it read `status` counters, then preserved `.qompack/`.

### 1. Why live dispatch defers: one deferral cascades through the whole session

- Status right after the 40 hooks: `l0_ordering_deferred=39`, `index/tool_use.jsonl` = 1 line.
  `state/delivery-leases.jsonl` = 40 lines (arrivals 1..40), `state/delivery-acks.jsonl` = 1 line
  (arrival 1 only).
- Hooks arrive about every 30 ms. One live dispatch costs about 50–85 ms on Windows. A per-stage probe
  of `ingest.dispatch` on 20 same-session Read events gave: capture 14–18 ms, run 24–48 ms, commit
  10–14 ms, admit 1.5–6 ms, gate 0.05–0.6 ms, terminal lookup 0.1–0.5 ms. Arrival 2 therefore reaches
  a free worker (NumCPU/2 = 11 workers) while arrival 1 is still publishing.
- The gate returns false and `dispatch` returns with the job dropped (WAL retained, seen released).
  Nothing live ever retries it. Every later arrival N+2, N+3, … is then deferred too, because
  arrival N+1 stays unacknowledged. So **after the first deferral, every later event of that session
  is permanently deferred** until some drain processes the WAL. 39/40 matches this exactly.

### 2. Why the drain does not recover: the idle drain is gated on 120 s of idleness

- 5 s and 45 s after the hooks, nothing had changed: index 1, acks 1, drain never ran.
  `state/drain.json` = `{}`.
- The idle ticker (`daemon.go` Run, `idleTickMax` 30 s) runs `d.idle.RunOnce(runCtx, idleRunBudget)`
  **only if `d.idle.IsIdle(now)`**. `idle.go` `IsIdle` requires `now - lastActivity >=
  DetectAfterSeconds*1000`, and `internal/config/defaults.go` sets `DetectAfterSeconds: 120`. The
  `drain` idle task (`idleTaskDrain`) is registered on that controller. So the first idle drain comes
  at least 120 s after the last hook, and it has a 2 s budget (`idleRunBudget`).
- The other drains are the startup drain, `redrainOnceServing` (fires once, on the first served
  request, before the deferrals exist), flush/SessionEnd, `admin.drain` and Stop. None of them runs
  while a session is active.
- Consequence: the e2e wait of `IdleTickMax + 30 s` = 60 s can never see the recovery. The test's
  diagnostic text ("waited on the 30s idle-tick drain") describes a drain cadence that does not
  exist while the session is active.
- The drain is also slow per line. Probe: drain of 20 WAL lines took 170–220 ms/line vs 60–85 ms/line
  live, same machine and run. I did not isolate the extra ~100+ ms. It is a separate performance
  observation, not the wedge.
- An explicit `admin.drain` does recover (diag2): index 1 → 10 in 5 s, then → 13 after 2 s more. The
  drain works; it just is not scheduled.
- Not verified: whether the drain aborting a file pass on `seen` "delivery still in progress"
  (`drain.go` processOne, `fmt.Errorf("daemon: drain: delivery still in progress")`) matters here.
  It would, if a drain ever runs while live workers hold the same keys.

### 3. Why clients spool: the cold first Accept misses the Windows ACK deadline

- The only observe-event client spool in both runs held **`toolu_obs_00`, the first event**. The same
  line (same nonce) is also line 1 of `wal-<session>.ndjson`. It is a duplicate of a delivery the
  daemon already made durable and leased, not a lost event.
- B-B (`l0_ingest`) max was 77 ms (diag1) and 139 ms (diag2), against Windows `AckDeadlineMs = 73`
  (`internal/config/deadlines.go`). The first Accept pays cold costs (WAL segment open, first lease
  batch fsync plus position sidecar), misses the ACK deadline, and the hook falls back to the spool.
- The second client spool in diag2 was my own `admin.drain` request timing out, not an observe
  event.
- The drain later absorbs the duplicate: same nonce → same lease → already acknowledged → consumed.
  This fact is independent of the ordering regression. Under co-load (rc1) more hooks can cross the
  deadline, and the e2e diagnostic reports that as "undrained client spool".

## Extra requirement relayed by the coordinator (e2e workstream)

In `TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting`, the gate defers tools behind an
in-flight prompt. `flushRoute` (`handlers.go` ~L969) then runs `registry.End`, `ing.CloseSession`,
`svc.SessionEnd`, WriteMarker and Sketches.Save, and only then `d.Drain`. SessionEnd drops the
observer's session state. Deferred or still-queued tools then replay onto a fresh `sessionState` at
turn 0, giving non-monotone turns (prompt@0, tools@0 | prompt@1, tools@0).

The e2e workstream ran with one worker and the gate kept, and turns were **still** non-monotone: the
worker lags the hooks, SessionEnd overtakes still-queued tools, and the log shows
"segment 1 is already closed at turn 1". With the gate off, turns are monotone.

Requirement: **the flush route must be ordered after every earlier accepted leased arrival of that
session**. Either drain or complete them before closing the session state, or make session close
idempotent against later replays. Per-event re-dispatch alone is not enough. Add a regression test
that asserts monotone turns when SessionEnd arrives while same-session events are still queued or
deferred. `handlers.go` is in C1.1's scope for this. The e2e workstream is not editing `handlers.go`,
`ingest.go` or `drain.go`.

## Proposed design (not implemented): a per-session sequencer

The goal is to keep the ordering contract (`delivery-order-decision.md`) and the terminal contract
(`delivery-terminal-decision.md`), including fail-closed on an unreadable frontier, without
stranding work behind the idle drain.

### Arrival-ordered lanes in `ingest`

- A `sessionSequencer {mu; lanes map[SessionID]*lane; parked int}`, where
  `lane {busy bool; pending []job /* sorted by lease.ArrivalSeq, unique by Delivery */; kicked, kickQueued bool}`.
- Worker: an unleased job is dispatched directly (legacy, never gated). A leased job goes into its
  session's lane, inserted by ArrivalSeq. If the lane is busy, the worker returns and the lane's
  runner will reach the job. Otherwise the worker marks the lane busy and runs it. Same-session work
  is serialized; cross-session work stays parallel. No worker ever waits on another.
- Runner loop: pop the lowest arrival, reset `kicked`, and call `dispatch`. `dispatch` should return
  an outcome (done / deferred-by-gate / dropped-retryable) instead of nothing, and it still runs the
  gate, so fail-closed is kept.
  - On a gate deferral: if `kicked` was set meanwhile, retry. Otherwise put the job back at the front
    and park the lane (`busy=false`).
  - On a retryable failure (blob, capture, NAK, commit): drop the copy (the WAL keeps it for the
    drain) and continue. The successor will simply park.
- Accept's lease order and ring order can differ between concurrent Accepts. With lanes, the job
  that arrives late (the lower arrival) finds the lane parked, takes it, and runs itself first. That
  covers `TestDeliveryOrder_LiveReversedRingOrderPublishesWithoutDrain`.
- Bound: a total parked/pending cap across lanes, for example `ringCapacity`, documented as a second
  bound beside the ring. Past the cap the job is dropped to the WAL with a counter, like
  `l0_ring_full`. Drop a lane's parked jobs on `CloseSession`/flush (after the settle step below),
  because they are all in the WAL.

### Waking parked successors

A predecessor can settle outside the lane: a drain acknowledges it, or a denial retires it. The
drain should collect the sessions it acknowledged or retired and call `ingest.wake(session)` at the
**end of the pass**. Waking inside the pass would race the pass's own dispatch of the same lines
through `seen` and hit "delivery still in progress".

`wake`: if the lane is busy, set `kicked`. If it is parked with pending jobs and no kick queued, send
the session on a `kicks` channel that workers also select on. Give `kicks` capacity equal to the
parked cap: each lane has at most one queued kick and at least one parked job, so the send can never
block or drop. There is no polling and no busy-spin.

### Flush settles the session before SessionEnd

In `flushRoute`, after `registry.End` and `CloseSession` and **before** `svc.SessionEnd`, call
`settleSession(ctx, s)`:

1. `wake(s)`.
2. Wait until the journal says every leased arrival of `s` is acknowledged or terminal. That is
   `predecessorsAcknowledged(s, last+1)`, where `last` is `j.arrivals[s]` (or `gen.lastArrival` when
   generations are on). The wait uses a broadcast channel that lane completions and drain wakes close
   and replace; no polling.
3. Stop waiting early when the lane is **stalled**: nothing of `s` is in the ring, and the lane is
   neither busy nor holding a queued kick, so it only has parked jobs blocked on something outside
   the live path. This needs a per-session count of jobs queued in the ring, incremented in Accept and
   decremented on lane submit or ring-full. Tests that pull jobs off `dd.ing.ring` by hand bypass this
   count, so keep a hard bound as backstop (ctx, or a per-step bound such as `drainLineDeadline`).
4. If still unsettled, run `d.Drain(ctx)` **before** SessionEnd. The WAL handle is already closed,
   so the drain syncs and reads the whole segment and publishes the remaining deliveries in arrival
   order. Then SessionEnd, marker, sketches and the existing final drain.

Check the recovery-marker stage order (`recoveryStageBegin/SessionEnd/Marker/Sketches/Drain`) and
any test that pins flush ordering before moving the drain. I launched a read-only investigation of
that; it was stopped before reporting.

### Unchanged by this design

WAL retained until the durable ACK. Identity never re-minted: the lanes only reorder dispatch and
never lease. Crash/restart semantics unchanged: parked jobs are memory only, and the WAL and drain
remain authoritative. `delivery_lease.go`, `delivery_generation.go`, `delivery_segment*.go` and
`delivery_radix.go` need no edits; the helpers can live in `delivery_order.go`.

## Things I noticed but did not verify

- The duplicate-agent red run you already have (`runs/red-live-order-cf31e01.log`) reports
  "0 of 6" and "0 of 2" acknowledged. In `LiveReversedRingOrder` with one worker, arrival 1 should
  still have acknowledged even on the unfixed code. It may be worth checking that prompt jobs built by
  `spD3Prompt` (no `Capture`) can reach the frontier on this path at all, before reading "0" as the
  ordering defect.
- `test/e2e/observer_e2e_test.go` sizes its wait on the belief that the fallback drain runs every
  `IdleTickMax`. With `DetectAfterSeconds=120` that is not true while a session is active. Once
  live ordering no longer depends on the drain, that bound should not matter. Any criterion change
  there needs its own rationale.

## Addendum: findings of my two read-only investigators (code reading only, no tests run)

### Drain scheduling and cost

- The idle gate has **always** existed. `git log -S"d.idle.IsIdle(now)"` returns only 3cd8719
  (2026-08-15), which introduced the tick with the gate already in place. The "cadence is
  daemon.IdleTickMax" comment (`test/e2e/observer_e2e_test.go:46`, from 3b2788b) and
  `daemon.go:725` are inaccurate whenever a session was recently active. The first idle drain comes
  120–150 s after the last hook and gets at most `idleRunBudget` = 2 s.
- `NewProject` overrides neither `DetectAfterSeconds` nor `IdleExitSeconds`. Only
  `test/e2e/scheduler_idle_test.go:108` sets `detectAfterSeconds:1`.
- Per-line drain overhead beyond the live path:
  - a known-nonce re-lease through `commitLeases`: `checkFile` → `checkSeal` plus Lstat/Stat, no write;
  - an extra `acknowledged` lookup;
  - a second full `admitDelivery`, whose `CaptureScopeRaw` checks each do `paths.Norm`
    (EvalSymlinks plus a Readlink walk);
  - about 5 journal lookups per line, each reading `daemon.lock` from disk via
    `owned()`/`ownedByFile()` → `readLockFile` (`lock.go:211-278`), against about 3 for the live path;
  - no ack group-commit batching, because the drain is a single goroutine;
  - `reattempt` costs O(deferred) per consumed line.

  None of these was measured individually.
- "delivery still in progress" (`drain.go:502-503`) breaks that file's read loop. Progress is saved,
  but `Drain` returns an error. The flush route then answers OK:false and keeps its recovery marker,
  and the rest of the file waits for the next drain. The e2e one-worker run (E1) logged exactly this.

### Flush / SessionEnd

- The server runs the handler before writing the reply, with the daemon-lifetime `runCtx` and no
  per-request deadline. The flush hook is `reply: true` with `flushReplyDeadline = 15 s`
  (`hookclient.go:37`; the manifest hook timeout is 20 s). On timeout the client **spools the flush**,
  and it is later replayed as `flushRoute(ctx, req, false)`. **Any settle wait plus SessionEnd must
  fit within 15 s.**
- Observer `onSessionEnd` (`observer/session.go:160-240`) closes the segment at `st.Turn`, flushes,
  persists, runs GC, then `delete(o.sess, id)`. A later event gets a fresh `sessionState` (Turn 0,
  Segment 0; `loadState` runs only once per process). An event processed between the unlock and the
  `delete` mutates state that is then discarded.
- The "segment N is already closed at turn M" warning **also appears with the gate off**. The e2e
  E2 run's log has "already closed at turn 2, refused for 4". It comes from degraded-passive mode
  skipping `svc.SessionStart` (`handlers.go:766`), so the second drive rehydrates Segment 1 from
  `observer.json`. That is a separate issue and not proof of the race.
- e2e evidence (`qompack-cx-e2e/plans/sdd/V6-closeout/e2e/runs/`):
  - baseline: all eight drive-1 tools were processed after the segment close, at turn 0;
  - E1 (one worker): only the tail was late (1_07 at turn 0; 2_06 and 2_07 at turn 0);
  - E2 (gate off): all tools landed before SessionEnd, with monotone turns.
- Constraints on a settle step in `flushRoute`:
  - never Drain on the `drain=false` path, because `dr.mu` is not reentrant (Critical C-1);
  - wait on real time or channels, never `d.clk` (the fake clock does not advance);
  - `liveWALDaemon` fixtures start no workers, so a settle wait there runs to its bound;
  - record no recovery stage after `sketches` on the `drain=false` path.
- Tests that pin flush behaviour:
  - `TestSessionEndRecordsRecoveryNeeded` (`delivery_publication_test.go:413`);
  - `test/e2e/v5_x03_test.go:529-535`;
  - `TestDrainOfSpooledFlushLineDoesNotDeadlock` and
    `TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun` (`daemon_test.go:422`, `:463`);
  - `TestMarkerIsWrittenByFlushAndCheckpointOnly` (`daemon_test.go:1167`);
  - `TestStragglerAfterSessionEndIsNotLostWithItsReopenedSegment` (`drain_live_wal_test.go:290`).

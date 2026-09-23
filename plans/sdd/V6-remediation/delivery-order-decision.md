# V6 coordinator: bounded leased-delivery ordering

2026-09-21. Implement ordering only among existing accepted leased arrivals. This is not a claim
about host-first order: client spool deliveries not yet leased remain a coverage uncertainty.

Before dispatch/publication of any leased observer event, require all lower leased arrival
sequences in the same session to be acknowledged. A blocked predecessor returns an explicit
retryable non-OK result, leaves WAL/spool bytes and blobs intact, releases same-process seen
ownership, and never blocks a worker waiting for another worker. Already acknowledged deliveries
stay idempotent. Legacy nonce-less/unleased paths keep their existing qualified behavior.
Use a bounded journal query, current single-file maps are bounded; no unbounded queues or polling.

Drain recovery must survive WAL order != lease order. WAL fsync and lease assignment are separate
batches, so after restart an earlier WAL line can hold arrival 2 and a later line arrival 1.
Merely breaking on a pending predecessor wedges that file forever. Allow bounded look-ahead or a
bounded pre-pass to process a ready predecessor while retaining the unconsumed prefix. Never
advance a durable offset past an unresolved line; retain later blobs until their original bytes
are consumed; use committed ACKs to absorb repeat look-ahead. Cross-file pending predecessors
must be retryable on later scheduled passes. No busy spin and no lease reassignments.

Also consider terminal denied/unadmittable records that ALREADY hold a lease: the existing policy
branch must not leave an immortal predecessor and wedge all later accepted work. If that needs a
new completion semantic, return the exact issue to main before weakening the acknowledged-capture
contract. Never persist denied bytes or silently classify unavailable as captured.

Exclusive author scope: internal/daemon/ingest.go (dispatch ordering only), drain.go (bounded
look-ahead / ordering only), NEW delivery_order.go + delivery_order_test.go; existing NEW
prompt_order_v6_test.go (invert defect assertion to desired behavior at actual dispatch layer).
No edits to delivery_lease.go/seal.go, daemon.go/handlers.go, store, observer, CLI, existing unrelated
tests, docs or carry ledger. Main owns those. delivery_archive/rollover files are unaccepted unused
prototypes and are not contracts. Use optional helper methods in your new delivery_order.go.
No commits/config changes/children. Focused ordering/drain/ingest tests only, actual Go exit status,
unique recorded failures. Do not run whole-tree/race/e2e/performance. You may use existing recorder;
parallel-tests-ready is present. Main will integrate full-package checks after your handoff.

## Addendum 2026-09-23 — V6 close-out C1.1 (live lanes, drain requests, flush settle)

Recorded by the C1.1 close-out (branch `closeout/ingest`, report
`plans/sdd/V6-closeout/ingest/report.md`). It extends this decision and relaxes none of its rules:
ordering only among leased arrivals, fail closed on an unreadable frontier, no worker waiting on
another, no busy spin, no polling, WAL and blobs kept until a durable ACK, no lease reassignment.
One sentence of the work record is superseded: a deferred LIVE delivery no longer waits for "the
next scheduled drain".

- **Live lanes.** Leased live jobs go through per-session lanes (`delivery_order.go`). A session's
  jobs are dispatched one at a time in arrival order. Sessions run in parallel. Bounds: at most
  `laneCapacity` (= `ringCapacity`, 4096) jobs across all lanes and `laneSessionCapacity`
  (= `orderingLookaheadBound`, 1024) per session. A job past either bound is refused, counted in
  `l0_ordering_lane_full`, and stays durable in the WAL.
- **Parking and waking.** A lane parks when its head stays pending and nothing signalled it during
  the dispatch. A parked lane has no owner and is not polled. It runs again on the session's next
  arrival, or when a drain pass that published or retired one of the session's leased lines ends
  (`DrainConfig.Released`). The release comes at the end of the pass, never mid-pass. A pass that
  only re-reads lines already on the frontier releases nothing, because such a release would feed
  a request loop.
- **Drain requests.** Four cases request one drain pass (`l0_ordering_drain_requested`): a lane that
  parks on a pending head, a lane that runs dry after jobs were refused behind it, a refused job
  whose lane no worker owns, and a leased job the ring dropped. `daemon.drainOnRequest` serves the
  requests. Each pass gets `idleRunBudget`, the requester then rests as long as the pass took, and
  concurrent requests merge. These cases no longer wait for the 120 s idle detector.
- **Flush settle.** Before SessionEnd, the flush waits for the session's lane while it keeps
  settling deliveries. The per-delivery stall bound is `settleSessionStall` (= `stopDrainBound`). The
  overall limit is `settleSessionLimit()`: manifest SessionEnd timeout − `precompactDeadlineSlack` −
  `settleSessionHeadroom`, which is 9 s with the shipped manifest. If the frontier still lacks a
  leased arrival, the flush then runs one drain pass (never on the `drain=false` replay path). If
  the session cannot settle, or the frontier is unreadable, SessionEnd still runs, but never
  silently: `l0_flush_unsettled` counts it and a LOUD line names it. After SessionEnd, the ended
  session's parked lane is released. Its jobs stay in the WAL for the flush's final drain.
- **Consequence.** The gate above already required each session to publish one delivery at a time,
  end to end. The lanes make that explicit. A session's publication rate is now one delivery per
  full publication latency. Under co-load the close-out measured about 0.07–0.17 s per delivery
  on Windows and up to about 0.85 s in the Linux container. On a quiet container, the observer
  e2e's 44 events finished in 5 to 19 s. The report puts this to the owner.

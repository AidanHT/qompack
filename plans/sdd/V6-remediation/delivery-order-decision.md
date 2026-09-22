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

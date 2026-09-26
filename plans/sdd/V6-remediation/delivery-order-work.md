# V6 remediation — bounded leased-delivery ordering (implementation record)

**Owner:** V6 delivery-order author (Opus 4.8, high). **Decision:**
plans/sdd/V6-remediation/delivery-order-decision.md. No commits/config/children.
**Exclusive scope honored:** `internal/daemon/ingest.go` (dispatch ordering only), `drain.go` (bounded
ordering/look-ahead only), NEW `delivery_order.go` + `delivery_order_test.go`, and
`prompt_order_v6_test.go` (inverted). No edits to delivery_lease.go/seal.go (main's c95b7af migration
anchors preserved), daemon.go/handlers.go, store, observer, CLI, existing unrelated tests, docs or the
carry ledger. delivery_archive/rollover and delivery_radix are other authors' and were left alone.

## Acceptance status (honest)

Followup round: main rejected three weakenings and corrected a false claim. All four are addressed
below. **One item is NOT self-contained and is returned to main: the terminal-completion contract for
a leased-then-denied delivery (item 4) needs a journal hook only main can add.** Until then the drain
PRESERVES such a line pending (visible, nothing lost) but a session with one wedges its later arrivals
— documented, not silently resolved. `TestCarriedDefect_SP08D2` FAILS BY DESIGN and is preserved for
main to re-fixture (item 2). Do not treat ordering as fully resolved on two-record swaps; main runs
integration + independent review.

## What was implemented (after the followup corrections)

Every LEASED observer event is published only after every earlier leased arrival of its session has
reached the committed frontier — so turn assignment follows arrival order.

- **`delivery_order.go`.** `deliveryJournal.predecessorsAcknowledged(session, arrival)` — a bounded
  scan of the journal's lease map (no I/O; Lock.mu then st, like `acknowledged`). It FAILS CLOSED
  (item 1): nil journal, lost ownership, closing, closed or fault → false, and the `arrival<=1`
  first-arrival short-circuit is taken ONLY after the journal is confirmed readable+owned, so a first
  arrival on an unusable journal is deferred, not bypassed. `leaseHeld(nonce)` queries an existing
  lease without creating one (item 4). The `leasedPredecessorsReady` wrappers (drain + ingest) fail
  closed with an explicit `l0_ordering_frontier_unavailable` diagnostic when the getter is missing or
  errors; an UNLEASED delivery is never gated (legacy). Counters `l0_ordering_deferred`,
  `l0_drain_ordering_deferred`, `l0_drain_ordering_resolved`, `l0_ordering_frontier_unavailable`,
  `l0_drain_leased_deny_pending`.
- **`ingest.go` dispatch (live worker).** Before publishing, ANY leased delivery (item 2: no host-id
  exception) whose predecessor is unacknowledged returns retryable: publish nothing, run nothing,
  leave the WAL bytes and blob intact, release seen ownership. Never waits on a worker; a later drain
  re-dispatches it.
- **`drain.go` `drainFile`.** The durable OFFSET advances only over the contiguous consumed prefix;
  the READ position runs ahead. A blocked line is DEFERRED (bytes/blob untouched, offset never past
  it). **Item 3 (no wedge):** the pass does NOT stop at the buffer bound — it keeps SCANNING to EOF so
  a predecessor deeper in the file is still dispatched and ACKED this pass (unblocking the prefix next
  pass). Bounded memory: `orderingLookaheadBound` caps buffered deferred requests and
  `orderingProcessedCap` caps the roll-forward map; overflow entries are neither buffered nor recorded
  — they rely on the durable committed frontier to be absorbed on a later pass. `reattempt` re-runs
  buffered deferred lines to a bounded fixpoint after each consume. **Item 4 (leased-then-denied):** a
  line refused on retry (a path that became a symlink escaping root; admitDelivery rechecks physical
  scope) that ALREADY holds a lease is PRESERVED PENDING — not consumed — with the
  `l0_drain_leased_deny_pending` diagnostic; a never-leased refusal keeps its terminal-skip. No forged
  ACK, no denied bytes persisted, no lease reassignment.

## Item-by-item resolution of the three rejections + the false claim

1. **Unreadable frontier no longer proceeds (was fail-open).** `predecessorsAcknowledged` and both
   wrappers now return false (defer, WAL/blob preserved, seen released) on closed/fault/lost-ownership/
   missing-getter, and the first arrival cannot bypass an unusable journal. Negative tests:
   `TestDeliveryOrder_UnreadableFrontierFailsClosed` (lost ownership → false incl. first arrival, and
   `leaseHeld` false), `TestDeliveryOrder_MissingJournalGetterFailsClosed` (nil getter → false + the
   diagnostic counter; unleased still true), `TestDeliveryOrder_UnreadableFrontierNoPublication` (no
   observer record published over an unreadable frontier).
2. **Host-id exception reverted.** `orderingApplies` is removed; every leased event is ordered. This
   RE-BREAKS `TestCarriedDefect_SP08D2` on purpose — preserved, not re-fixtured (see below).
3. **Look-ahead no longer wedges.** Replaced "stop at the bound" with "keep scanning, bounded memory,
   overflow absorbed via the committed frontier." `TestDeliveryOrder_DrainReversedPrefixBeyondBufferBoundDoesNotWedge`
   builds a synthetic >`orderingLookaheadBound` reversed prefix (1027) whose ready predecessor is at
   the end and asserts it fully resolves across a few passes with the deferred count exceeding the
   buffer bound (i.e., the scan continued past it). The `processed` map is capped so it cannot grow
   unbounded behind a stuck prefix.
4. **Terminal-safety claim was FALSE — corrected and RETURNED TO MAIN.** A delivery leased on one pass
   can be DENIED on a later pass (physical-scope recheck), and its earlier lease then blocks the
   session's later arrivals forever. The drain now detects a leased-then-refused line (`leaseHeld` by
   nonce, no create) and PRESERVES it pending rather than silently consuming it. It does NOT forge an
   ACK or persist denied bytes. `TestDeliveryOrder_LeasedDenyOnRetryPreservedPending` pins: the line is
   counted, unacknowledged, its spool bytes retained, nothing published, and its lease still blocks the
   session's later arrival (the visible gap).

### Terminal-completion hook main must supply (minimal source need)

Releasing the orphaned lease needs a journal record that marks a leased arrival TERMINALLY RESOLVED,
distinct from an acknowledgement (which asserts durable capture — which a denial has not made). That
record lives in main's `delivery_lease.go`/`delivery_seal.go`. The minimal hook: a
`deliveryJournal.completeTerminally(nonce, reason)` (or equivalent) that records the arrival as
resolved-without-capture, and a matching read so `predecessorsAcknowledged` treats a terminally-
resolved arrival as satisfied. The drain's leased-deny branch (delivery_order.go / drainFile) would
call it INSTEAD of preserve-pending. I did not add it: it changes the acknowledged-capture contract
and the journal, both main's. `delivery_order.go`'s `leaseHeld` is the read-only, no-create query the
branch already uses.

### SP08-D2 fixture — exact steps main must update (do NOT change sessions)

`internal/daemon/drain_reused_lease_test.go`: steps 1–2 lease `readToken` (arrival N, its ACK cut via
`cutToken`) then `laterToken` (arrival N+1, same session), and line 136 asserts
`journal.acknowledged(laterToken)` with lines 143–145 asserting the later read SUPERSEDED the first.
Under the ordering gate a later arrival cannot publish while its earlier same-session arrival is
unacknowledged, so `laterToken` is correctly deferred and line 136 fails. Main must, before asserting
`laterToken`'s frontier/supersession, REPLAY-and-ACK the predecessor `readToken` (arrival N) — e.g.
let the cut read redeliver and acknowledge first, then the later read publishes and supersedes. Same
session must be kept (distinct sessions would erase the regression this test exists for).

## Tests and recorded runs (actual Go exit status; dist/v6-remediation/runs/)

- `delivery-order-followup-ordering-2` — `TestDeliveryOrder_|TestPromptOrder_V6_` — **PASS** (exit 0,
  ~21s). Covers: the fail-closed gate (item 1, 3 tests), the >bound no-wedge scan (item 3), per-session
  independence, mid-pass cancellation preserving unconsumed bytes, leased-deny preserve-pending (item
  4), the reversed-2-record drain, and the inverted dispatch-layer prompt-order tests.
- `delivery-order-followup-drain-regression` — `TestDrain|TestCarriedDefect_SP08D3` — **PASS** (exit 0)
  — the `drainFile` rewrite (items 3/4) did not regress the drain path or SP08-D3.
- `delivery-order-followup-sp08d2-expected-fail` — `TestCarriedDefect_SP08D2` — **FAIL by design**
  (exit 1), preserved per item 2 (main owns the compatible fixture update above).

Selected names only — the costly unrelated `TestDeliveryJournal`/history tests were not swept. Build
and `go vet ./internal/daemon/` clean; gofmt clean on all changed files.

## Limitations (stated; no universal host-order claim; liveness only within demonstrated bounds)

- Ordering is among LEASED arrivals only; an earlier-but-unleased client-spool delivery cannot be
  ordered before a later leased one (pinned by the uncertainty test). No host-first claim.
- The terminal-completion gap (item 4) is OPEN pending main's journal hook; a leased-then-denied
  delivery preserves pending and blocks its session's later arrivals until then.
- Anti-wedge liveness is demonstrated for a reversed prefix that a restart's fsync-vs-lease reorder
  produces (a local swap, and the synthetic >1024 case). A pathological FULLY reversed file resolves
  one arrival per pass (O(N) passes) — progress, not fast liveness; not claimed beyond the demonstrated
  bounds. A cross-pass discovery cursor (to avoid re-scanning the resolved suffix) is a possible future
  optimization but needs durable state and was not added (scope + no over-claim).
- A deferred LIVE delivery retries on the next scheduled drain (no re-enqueue, no polling/busy-wait).
- Exactly-once is claimed only to the tested cuts.

## Close-out addendum (2026-09-23, C1.1)

The limitation "A deferred LIVE delivery retries on the next scheduled drain" was the C1.1
live-ingest regression. With several workers, every event of a busy session after the first was
deferred and stranded until a drain ran. It is superseded by the per-session live lanes and the
drain requests recorded in the decision's 2026-09-23 addendum. That round added these counters:

- `l0_ordering_lane_full`: leased jobs the lanes refused because all lanes together, or the job's
  own session, were at their bound. Each refused job stays in the WAL.
- `l0_ordering_drain_requested`: drain passes the lanes requested. Requests that arrive while one
  is pending merge into it.
- `l0_flush_unsettled`: flushes whose SessionEnd ran while some of the session's leased deliveries
  were still unpublished, or while the committed frontier could not be read to tell.

Record: `plans/sdd/V6-closeout/ingest/report.md`.

## Close-out addendum (2026-09-25, C1.15 and C1.13)

The flush became a leased arrival that is answered once it is durable, and the session's end runs
asynchronously. Client spools are replayed while their session is active. Both are recorded in the
decision's 2026-09-25 addendum. That round added these counters:

- `l0_session_end_refused`: flushes acknowledged while Stop was already joining the session ends,
  so no end was started for them in that process. Each one's line stays durable in its session's WAL
  and its session stays marked as needing recovery.
- `l0_spool_watch_drains`: client-spool passes the watcher ran.

Record: the `w2-sessionend` report, committed by the coordinator under `plans/sdd/V6-closeout/`.

# V6 remediation — delivery order + terminal integration: independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source review only — no source/test edits, no tests/builds (main runs the focused
tests; I ran nothing), no Git/config/permission changes. Only output: this file. No scope/final
signoff. Concurrency/crash/liveness claims are SOURCE reasoning; a focused unit PASS is not proof of
these paths under real interleavings/power-loss. Prior review artifacts untouched. `lease.go` and the
generation files are the capacity author's concurrent scope and are NOT reviewed here.

**Read:** `internal/daemon/delivery_order.go`, `delivery_terminal.go` (re-read),
`delivery_terminal_integration_test.go`, `drain.go` (admission→retire→ordering→look-ahead→dispatch),
`ingest.go` dispatch (live worker gate), against `delivery-order-decision.md` /
`delivery-terminal-decision.md`.

---

## 1. Verdict

The wiring is coherent and the fail-closed properties hold. The four claimed fixes are present in
source: (a) terminal denied-retirement is separate from ACK and both live and drain skip a retired
delivery; (b) `retireDenied` is never called with `owner.mu` held by the caller (drain/live call it
without holding it); (c) an unreadable/unowned frontier is deferred, never treated as absence, with
the arrival≤1 short-circuit taken only after the journal is confirmed owned+usable; (d) a
leased-then-denied replay is retired-not-recaptured even if admission would now permit it. Three
things for main: an ordering-gate **liveness edge** (lost predecessor), the **precise shape** of the
terminal-vs-capture exclusion (eventual, not atomic — by design), and a **test-coverage caveat**
(the physical-symlink transition skips on Windows; Linux pending). The appendix **corrects my prior
sidecar-review F3** per the coordinator's finding.

---

## 2. Confirmed correct

- **Terminal vs capture, both paths.** The live worker (`ingest.go:674-677`) and the drain
  (`drain.go:470-480`, `processOne`) both check `terminalForDelivery` FIRST for a leased delivery and
  skip capture if retired. A denied leased re-admission writes the terminal then consumes-not-captures
  (`drain.go:596-625`). A retired delivery re-read after restart with a now-permitting admission is
  still skipped, because `processOne`'s terminal check precedes any capture and does not depend on the
  fresh verdict (`TestDeliveryTerminal_RetirementBeforeOffsetSurvivesRestart` exercises this). No
  capture ACK is fabricated by retirement (`acknowledged` reads only `j.acks`).
- **`terminalStateDir` anchored to the owner lock, not a generation pathname.** `terminalDirectory`
  opens `os.OpenRoot(filepath.Dir(j.path))` = `state/`, with `SameFile` identity + symlink refusal, so
  `state/delivery-terminal/` is stable across a rollover (generations change; this does not).
- **Full v1 lease identity binding.** `existingLease` (`order.go:141-157`) binds an inherited request
  to its lease only when `Session` AND `RequestHash` match (`req.TS++` → mismatch → `deliveryJournalError`,
  the `binding conflict` test), and `retireDenied` re-checks the full lease struct against `j.leases`
  and writes/verifies the canonical `terminalFor(lease)` bytes with the ObservationID-derived filename.
  A mismatched/tampered replay is preserved pending, never retired.
- **Unknown frontier is never absence (fail-closed).** `predecessorsAcknowledged` returns false on
  nil/unowned/closing/closed/faulted journal, and takes the arrival≤1 short-circuit ONLY after those
  checks (`order.go:62-88`); `leasedPredecessorsReady` (drain and ingest) defers + counts
  `l0_ordering_frontier_unavailable` when the getter is missing/failing. "Publication never proceeds
  over an unreadable frontier" holds on both paths. `TestDeliveryTerminal_UnprovedPolicyAndUnavailableJournalRemainPending`
  covers policy-unavailable / journal-unavailable / binding-conflict → all stay pending, spool retained.
- **Look-ahead memory bounds.** `deferred` capped at `orderingLookaheadBound` (1024) — past it the
  line is not buffered but scanning continues (`drain.go:678-680`); `processed` offset roll capped at
  `orderingProcessedCap` (4096). Both are memory caps that rely on the durable frontier for overflow;
  they do not wedge a >bound reversed prefix (a ready predecessor found deeper is dispatched this pass,
  unblocking the prefix next pass).
- **Blob cleanup follows offset progress.** A retired / acked / seen-completed / dispatched line adds
  its blob to `PendingBlobs` (cleaned after the offset commits); a deferred or overflow line does not
  (its bytes are still needed for the retry). Consistent.

---

## 3. Findings for main

### O1 — ordering gate + a lost predecessor permanently defers successors (liveness edge)
`predecessorsAcknowledged` (`order.go:79-86`) blocks arrival N until every LEASED predecessor `<N` in
the session is acked or terminal-retired. If a predecessor was leased (durable in the journal) but its
spool/WAL bytes are lost (corruption, a dropped line), it can be neither captured (no bytes) nor acked
nor retired (retirement needs the re-admitted spool line) — so its successors defer **forever**, the
offset never passes them, and that session's spool tail is stuck across all future passes. The look-
ahead's "keep scanning" prevents a *reversed-order* wedge but not a *missing-predecessor* one. The
comment already limits the claim ("liveness is claimed only within these demonstrated bounds"), and it
surfaces as a perpetually-incomplete `DrainGaps` rather than silent loss, but there is no escape hatch.
**For main:** confirm this is acceptable (coverage stays explicitly incomplete) or decide whether a
lost-predecessor needs an operator/fsck path; a `sameToolUseIDs`-style deadline is not obviously safe.

### O2 — terminal-vs-capture exclusion is EVENTUAL, not atomic (document the shape)
`seen` serialises the same delivery between live and drain, but the drain writes the terminal in the
admission block (`drain.go:603`) BEFORE `processOne`'s `seen.begin`. So a live worker that already
passed its `terminalForDelivery` check and is mid-`publishCapture` can be running while the drain
writes the terminal for the same delivery — producing BOTH an ACK and a terminal. Per
`delivery-terminal-decision.md` ("both facts remain; this is not a data-deletion mechanism") this is
INTENDED, and `predecessorsAcknowledged` treats either an ack or a terminal as "ready," so it is
consistent. But the guarantee is "a *committed* terminal prevents *future* capture," not "a terminal
and a capture are mutually exclusive at the instant of retirement." The drain pass that raced a live
capture also ends with a "delivery still in progress" hard error after writing the terminal, then
converges on a later pass. **For main:** this is fine but the precise shape (eventual exclusion +
both-facts) should be stated so a reader does not expect atomic exclusion.

### O3 — cancellation not checked inside the reattempt fixpoint (minor)
The read loop checks `ctx.Done()` per line (`drain.go:553-556`), but `reattempt` (`:522-549`) runs its
bounded fixpoint (≤1024 deferred × passes) calling `processOne` without a ctx check; `dispatchPending`
is assumed to honor ctx, but the loop itself does not early-exit on cancel. Bounded, so it terminates;
note it as non-responsive-to-cancel work.

### O4 — test-coverage caveat: the headline physical transition skips on Windows; Linux pending
`TestDeliveryTerminal_DrainAfterPhysicalScopeChanges` is the core allowed→denied→retire→successor
scenario, but it depends on a directory symlink and `t.Skipf`s when unavailable
(`delivery_terminal_integration_test.go:44-46`). On the Windows dev host that path is unexercised, and
a Linux run is pending. `TestDeliveryTerminal_RetirementBeforeOffsetSurvivesRestart` (no symlink) does
cover the restart/no-recapture path portably. **For main:** the physical scope-change retirement is
NOT yet demonstrated on any run I can confirm; a Linux run is required before relying on it. A source
claim that a test is genuine is not evidence it executed on the target platform.

---

## 4. Appendix — correction to my prior sidecar review (F3)

My `observation-sidecar-final-review.md` §2/F3 concluded that the id-only committed binding was "safe
under append-only + reserved-identity + reopen re-derivation." **The coordinator found that too
weak; I withdraw the "safe" conclusion.** From `observation_publication.go` source, three real gaps:

1. **Committed obs with same id/Root but a DIFFERENT `Sup` list is treated as idempotent, then the
   CALLER's intent is completed, writing unrelated supersede marks.** `reserveConflictLocked`
   (`:372-377`) returns `(idempotent=true, nil)` whenever `recID == in.rec.ID`, WITHOUT comparing the
   target set; `publishObservation` then calls `completeIntentLocked(ctx, in)` with the caller's `in`
   (caller's `Sup`), so `recordSupersedingCore`/`completeSupersedeMarks` drive supersede marks from the
   fresh caller set, not the original committed intent. The committed binding does NOT freeze the
   supersede-operation set. This is reachable via a redelivery whose recomputed `older` set has drifted
   (§tooluse.go computes `older` from current index state). The "full canonical original intent" is not
   retained/enforced after commit.
2. **`sameBinding` allows `Sup` growth.** `sameBinding` (`:300-302`) compares only `rec.ID` +
   `identityOf(rec)`, not `Sup`; the load path (`applyIntentLocked:280`) and the pending path use it, so
   two intents for one observation with the same record but a GROWING target set are not flagged
   ambiguous.
3. **A torn line's "unavailable" loses priority to a committed binding.** `ToolUseByObservation`
   checks `obsCommitted` (`observation_lookup.go:48`) BEFORE `obsUnavailable` (`:58`), so an observation
   both committed and marked unavailable by a later torn line returns the committed record, hiding the
   uncertainty the torn line should raise.

These are the sidecar author's to correct (with bounded scan — my prior F1 — concurrency, and root
confinement — my prior F4). I did not and must not edit those files. **Unresolved durable-contract
question returned to main:** must a committed observation's supersede-operation set be immutable (the
first intent is authoritative and a divergent later same-id intent is a conflict, not idempotent), or
is the binding-only guarantee (record identity, supersede set caller-driven) intended? The current
code implements the latter silently; the decision's "replay the same operations, never a set
recomputed from state" language points to the former.

## 5. Non-acceptance

Source review of the order/terminal integration only; no scope or final signoff. The four wired fixes
are present and the fail-closed frontier/terminal properties hold in source. O1 (lost-predecessor
liveness) and O4 (Windows-skipped/Linux-pending physical transition) are the two items to resolve or
consciously accept; O2/O3 are documentation/minor. The F3 correction (appendix) supersedes my prior
"safe" conclusion. `lease.go`/generation files (capacity author) are unreviewed. I ran nothing;
uncertainty about untested paths and platform gates is preserved.

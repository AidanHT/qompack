# V6 remediation — delivery-journal rollover design: critical review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** DESIGN review only — no source/test edits (author owns `delivery_archive*`/`delivery_rollover*`;
I edited nothing), no tests/builds/git mutations/config changes. Only output: this file. Writer/rollover
trigger is DISABLED and stays so; nothing here clears it.

**Read:** `delivery-rollover-coordinator-decision.md`, `delivery-rollover-implementation-design.md`;
current `internal/daemon/delivery_lease.go` (`openDeliveryJournal`, `loadDeliveryPosition`,
`readDeliverySealImage`), `internal/daemon/ingest.go` (`leaseDelivery`); and, via read-only
`git show 301a8e9:…`, the same functions at the integrated base.

---

## 1. Verdict

The old-reader **marker refusal** (§4) is real and already true of shipped binaries. Everything
resting on top of it has confirmed defects: the first-rollover **rename destroys the crash-recovery
anchor and triggers a fresh-journal recreation** (F2, severe, identity loss), the **single mutable
manifest bricks a second rollover on crash** (F3), the per-segment **`joinOK` is sealed trust, not a
re-verifiable proof** (F4), and — the architectural blocker — the **bounded per-nonce lookup cannot
prove novelty, so the rollover refuses new deliveries once segments exceed the seek bound** (F5),
turning capacity repair into a relocated cap. All five of main's suspicions are confirmed against
source. The smallest safe next step is a reader hardening plus a redesign of F2/F3/F5; a writer must
not be built on the current lookup structure (§7).

---

## 2. F1 — Refusal barrier is sound at the reader layer, but "refuse" ≠ "product refuse" (concern 1, confirmed)

**Verified sound (§4):** the current AND base `loadDeliveryPosition` (`delivery_lease.go:864-873`)
rejects the proposed marker — no `"v"` key ⇒ `Version != core.EvidenceVersion`, `Chain.IsZero()`, and
the unknown keys fail the canonical `bytes.Equal` re-marshal — and `readDeliverySealImage`
(`:821-823`) rejects it on `info.Size() != deliverySealFileSize`. So a segmented marker file is
refused by binaries in the field with no new code. Good, and a committed segmented store (marker
present, legacy `delivery-leases.jsonl` renamed away) makes an old binary hit
`openDeliveryJournal`'s `else if journalErr != nil` branch (`:196-197`) → `deliveryJournalError()`.

**But the old daemon does not halt.** `leaseDelivery` (confirmed identical at 301a8e9 and current)
degrades a journal-open failure to **unleased**: `i.countUnleased(); return deliveryLease{}, false`.
The daemon keeps running, captures tool/prompt/stop results under DERIVED ids (not journal
ObservationIDs), writes them to the SHARED object/index/sidecar store, and (prompts always ACK)
releases the spool. It never touches the segmented journal (cannot open it), so journal identity is
preserved — but the coordinator's requirement that "old binaries actually refuse" is met only at the
journal-open layer, not at the product layer. **Decision for main:** is unleased old-daemon operation
against a segmented store acceptable, or must a hard startup version-gate refuse to *run*? The marker
does not answer this.

## 3. F2 — First-rollover rename strands the recovery anchor → fresh-journal recreation (concern 2, confirmed, SEVERE)

`openDeliveryJournal` (`delivery_lease.go:182-195`, **identical at 301a8e9**) creates a **fresh empty
journal + fresh v1 position** (arrivals restart at seed, ObservationIDs re-mintable) whenever BOTH
`delivery-leases.jsonl` AND `delivery-lease-position.json` are absent:

```
if os.IsNotExist(journalErr) && os.IsNotExist(positionErr) {
    ... paths.WriteAtomic(p, nil, ...) ; createEmptyDeliveryPositionV1(positionPath, deliveryChainSeed) ...
```

Design §2 renames both legacy files to `segments/lease-0000.*` **before** R5 writes the marker. A
crash **after the rename, before R5** leaves both legacy files absent and the marker not yet written.
The next open — any binary, and necessarily the LEGACY path since no marker exists to dispatch to the
segmented reader — hits the both-absent branch and **recreates a fresh journal**, restarting arrivals
and orphaning the renamed `segments/lease-0000.*`. That is exactly the "recreation/identity loss" main
feared, and it directly falsifies §8's claim that "crash before R5 … recovers to legacy/active-k …
whole rollover retried": the legacy files the retry needs are gone. The ACK journal has the identical
recreate-on-both-absent path (`:1075-1081`), so the hazard is doubled.

**Required correction:** never remove or rename the legacy recovery anchor before the atomic R5
commit. COPY the legacy journal/position to `segments/lease-0000.*` via `CreateNew` with the originals
left in place (or address segment 0 at the legacy path), so a crash-before-R5 always finds the legacy
files and `openDeliveryJournal` recovers legacy cleanly. "Rename, never delete" is still **lossy of
the original path**, and the original position file IS the pre-commit recovery anchor (R5 overwrites
it). Rename cannot be made crash-safe against binaries whose recreate-on-both-absent behavior is
already shipped and cannot be retroactively changed.

## 4. F3 — Single mutable manifest/frontier bricks a second rollover on crash (concern 3, confirmed)

`delivery-journal.json` (the manifest/head, §3) and `delivery-arrivals.json` (§5) are single files
rewritten in place; §8's R-table does not even list the manifest write, an underspecification. The
committed marker carries `manifest_sum`, and `openSegmented` (§10) verifies it. On a **second**
rollover (k→k+1) that rewrites `delivery-journal.json` to `H_{k+1}` and then crashes **before R5**,
the still-committed marker expects `H_k` but the file now holds `H_{k+1}` → headSum mismatch →
`openSegmented` refuses, and the legacy path is not taken (marker present) → **store unopenable
(bricked)**, even though the committed state (k) was intact. WriteAtomic makes each file atomic but
does not bind the manifest generation to the marker generation across the crash.

**Required correction:** immutable manifest generations. The marker references a generation id/sum; a
new manifest is written `CreateNew` as a NEW generation and becomes authoritative only when R5 commits
the new marker; the prior generation is retained until then (GC later). Bind the arrivals-frontier
generation the same way if its sum is chained into the head. Specify the manifest write's exact
R-step; as drawn it is unsequenced.

## 5. F4 — Per-segment `joinOK` is sealed trust, not a re-verifiable proof (concern 4, confirmed)

§7 replaces the open-time global ack↔lease join (`loadAcksFrom` requires every ack's delivery to have
a surviving lease with a matching ObservationID) with a per-segment `joinOK` boolean sealed at
archival and chained into `headSum`. Mandatory `headSum` verification proves no descriptor was dropped
or altered — but it cannot re-prove that `joinOK` was **computed correctly** at archival time. A buggy
archival join seals a false `joinOK` that `headSum` then faithfully protects. So the open-time
invariant reduces from "re-prove the global join" to "trust the archival join code + seal-chain
integrity." That is a genuine durable-data contract weakening. The design correctly escalates it for
countersign (§7, §11.3); affirm it is **trust, not proof**, acceptable only as an explicit
countersigned change, with `headSum` verification MANDATORY before any `joinOK` is trusted and the
archival join code itself under adversarial test. (Directionally the join is at least well-posed: an
ack references a lease that already existed, so segment k's acks join into the cumulative lease set of
segments ≤ k — no forward dependency.)

## 6. F5 — Bounded novelty lookup defeats the capacity goal (concern 5, confirmed, ARCHITECTURAL BLOCKER)

`resolveLease` (§6) searches archived indexes newest-first up to `maxArchiveSeeks`, returning
**unavailable** beyond the bound; §11.2 maps `unavailable → ErrDegraded` in `decide`. Minting a NEW
lease for a novel nonce is only safe if the nonce is proven absent from **all** segments — otherwise a
redelivery of an old delivery whose nonce lives in an un-searched segment gets a **second**
ObservationID (re-mint, forbidden). A bounded per-nonce search cannot prove global absence once
archived segments exceed `maxArchiveSeeks`, so every new-lease decision that misses the active +
bounded-archived set returns unavailable → `ErrDegraded` → **new deliveries are refused**. The
rollover — whose entire purpose is to ADD capacity — therefore imposes a hard ceiling at roughly
`maxArchiveSeeks` segments: it does not repair the cap, it relocates it.

§12 calls the bounded lookup "sound … with unavailable." It is sound as a LOOKUP (never false-absent),
but NOT as capacity repair: the safe behavior at the bound (unavailable → refuse) is precisely the
loss of new-delivery capacity the rollover was meant to remove. The coordinator's twin requirements —
bounded per-op AND real added capacity AND never re-mint/never false-absent — are not jointly
satisfied by a per-segment binary-search lookup.

**Required direction:** proving nonce novelty in bounded work needs a **global membership** structure,
not per-segment indexes — e.g., a sealed cumulative nonce set with a bounded/paged exact tier (or a
bloom filter whose only failure mode is a bounded exact fallback), so "is this nonce novel?" is
answerable in bounded work regardless of segment count. Until F5 has such a sound answer, the rollover
cannot be built as designed and cannot be called a capacity fix.

## 7. Smallest safe next step; is a reader-first prepare release a real prerequisite?

- Current binaries **already refuse the marker** (§2/F1), so a reader release is NOT needed merely to
  teach old readers to reject a segmented store.
- The real prerequisite is on the **writer/protocol**, because the recreate-on-both-absent behavior
  (F2) is baked into every deployed binary and cannot be retracted: the rollover must NEVER strand the
  legacy anchor (copy-not-rename) and must use immutable manifest generations (F3).
- A genuinely useful, small, reader-only **prepare step** does exist and is worth doing: harden
  `openDeliveryJournal`/`openAckLocked` to **refuse (fail-closed) rather than recreate** when a
  `segments/` directory or a manifest is present. That closes the both-absent recreation hole as
  defense-in-depth (a partially-migrated or in-progress store seen by any reader refuses instead of
  resetting). It does NOT fix F5.
- **Smallest safe next step overall:** keep the writer/trigger disabled; land only (a) the reader
  refuse-not-recreate hardening, and (b) a resolved redesign for F5 (bounded global novelty
  membership), F2 (copy/leave-in-place anchor), and F3 (immutable generations), with the F4 weakening
  countersigned. Do not implement the rollover writer on the per-segment-index lookup.

## 8. Non-acceptance

Design review only; no clearance, no writer enablement, no signoff. §4 refusal is verified sound at
the reader layer; F2 (severe identity-loss on crash-before-R5) and F5 (capacity ceiling from bounded
novelty proof) are blockers for the rollover as designed; F3 is a crash brick needing immutable
generations; F4 is a real contract weakening for main's countersign; F1 raises a product-refusal
question the marker does not settle. The coordinator's constraints (no raised cap, no forgotten
nonces/arrivals, no restarted arrivals, no faux old-reader compatibility) are not yet all met: F2/F5
can lose or refuse identity, and F1 shows "refuse" is only journal-deep.

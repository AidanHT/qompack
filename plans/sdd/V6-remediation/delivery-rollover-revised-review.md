# V6 remediation — delivery rollover: revised implementable design (closes F1–F5)

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** design authoring within a review — no source/test edits, no tests/builds, no git/config,
no release signoff. Only output: this file. I own this report only; the observation-sidecar and
delivery-order authors' files are not touched. The prior rollover prototype stays unaccepted; nothing
here asserts prototype or design safety — the crash claims below are DESIGN and require the
failure/rollback suite before any acceptance. Writer/rollover trigger stays disabled.

**Read against:** `delivery-rollover-critical-review.md` (F1–F5), `delivery-rollover-coordinator-decision.md`,
`observation-binding-decision.md` (incl. the new superseding decision), and current
`internal/daemon/delivery_lease.go` (`openDeliveryJournal`, `decide`/`commitLeases`, `loadAcksFrom`,
`loadDeliveryPosition`) and `delivery_seal.go` (the fixed 2-slot A/B seal).

---

## 1. Design goal and the five closures

A single replacement built from main's primitives — **immutable content-addressed generations**, an
**on-disk Merkle radix index keyed by `H(nonce)` with a collision-checked original nonce**, a
**separate sealed session-frontier index**, **legacy files left anchored until one atomic commit**,
and **no old-binary writer promise** — closes F1–F5 as follows:

| Finding | Closure |
|---|---|
| **F2** rename strands the recovery anchor → recreation | Legacy files are **copied**, never renamed; the legacy position file (the recreate-on-both-absent anchor, `delivery_lease.go:182-195`) stays in place until the atomic marker commit. Crash-before-commit opens legacy cleanly; the recreate branch never fires (§4, §5). |
| **F3** single mutable manifest bricks a 2nd rollover | Each generation is an **immutable, content-addressed** manifest referencing immutable segment/index roots. The committed marker names one generation root; a new generation is built as new content-addressed artifacts and becomes authoritative only when the marker commits. A crashed half-built generation is orphaned and ignored — the prior generation's root still verifies (§3, §5). |
| **F4** per-segment `joinOK` is trust, not proof | The ack↔lease join is verified once at ARCHIVAL and recorded as a property of the **content-addressed** generation; open-time integrity is a hash verify of the generation root the marker names, so the archived join cannot be altered without changing the root the marker commits (§6). Still trust-of-a-past-computation → **contract choice for main** (§8.1). |
| **F5** bounded per-nonce search can't prove novelty | Novelty/redelivery is answered by the **Merkle radix index over `H(nonce)`**: membership and proof-of-absence traverse **fixed hash depth** (≤32 byte-radix levels), not N segments, so a new lease can be minted or a redelivery recognized in bounded work regardless of segment count. Capacity actually grows (§2, §3). |
| **F1** "refuse" ≠ product refuse | The marker keeps the dual-reader refusal (§7); reader-first prep additionally hardens the loader to **refuse rather than recreate** when a generation marker/segments dir is present. Old binaries fail-closed; §8.2 flags the residual unleased-operation product question for main. |

---

## 2. Identity invariants (unchanged from arch §0.1 / `decide`)

- `ObservationID = HashBytes("qompack.observation.v1", session‖arrival₈)`; never re-minted, never
  content-derived (`delivery_lease.go:361`).
- `ArrivalSeq` per session dense, monotone, never restarts; continues from the session-frontier index
  across a generation boundary (§3), not from 1.
- A nonce → its full `deliveryLease` is resolvable for arbitrarily old copies via the radix index, or
  the delivery is **unavailable** — never re-minted, never false-absent.
- Per-file caps (`deliveryLeaseMaxEntries` / `deliveryLeaseMaxBytes`) are **unchanged**; each active
  segment obeys them. Only the number of generations grows, on disk.

## 3. On-disk layout (segmented; only after a rollover, which is not enabled)

```
state/
  delivery-lease-position.json     GENERATION MARKER once segmented — the barrier (§7). NOT a v2 seal.
  delivery-leases.jsonl            LEGACY tail — left ANCHORED in place; segment 0 is read here (§4)
  delivery-acks.jsonl / *.pos      LEGACY ack tail — same
  generations/
    gen-<rootHex>.json             immutable generation manifest, content-addressed by rootHex:
                                   { fmtV, genSeq, prevRoot, activeLeaseSeg, activeAckSeg,
                                     nonceIndexRoot, ackIndexRoot, sessionFrontierRoot,
                                     archivedJoinRoot, segChainRoot }
    active/ lease-<g>.jsonl lease-<g>.pos   ack-<g>.jsonl ack-<g>.pos   (the two active tails, v2 seal verbatim)
    nonce-index/  <page-hash>.page          Merkle radix pages over H(nonce) → full lease (or leaf ptr)
    ack-index/    <page-hash>.page          Merkle radix over H(ack.delivery) → ack
    frontier/     <page-hash>.page          sealed sorted session → lastArrival
    segments/     lease-0000.jsonl … (archived tails, immutable; segment 0 == the legacy file, read in place)
```

The **marker never grows a list**; it names one 32-byte generation root. The generation manifest is
immutable and content-addressed (`gen-<rootHex>`), so a new rollover writes a NEW file and never
overwrites the committed one (F3). The **active tails keep `delivery_seal.go` verbatim** on their own
`.pos`; nothing about the hot-path seal changes.

**Merkle radix index (F5).** Key = `H(nonce)` (32 bytes). A page is a sealed node whose child
pointers are child-page hashes; a leaf stores the full `deliveryLease` **and the original nonce
bytes**. Lookup/absence traverses at most the key's byte-depth, each page verified against its
parent's hash down from `nonceIndexRoot` (in the manifest). A hit compares the stored original nonce:
equal ⇒ redelivery (return the full lease, original `ObservationID`); unequal ⇒ `H`-collision, the
query nonce is **novel** (collision-checked soundness, not probabilistic). Absence ⇒ mint. Memory is
the manifest root + a bounded page-cache; each op is bounded to key-depth page reads.

**Session frontier (F5/dormant-session gap).** `frontier/` is a sealed sorted `{session,lastArrival}`
over EVERY session that ever leased, keyed the same way; a dormant session's arrival continues by a
bounded frontier lookup, or the mint is **unavailable** (never arrival 1). Dormant sessions are never
loaded whole.

## 4. Smallest complete integration boundary

Six touch points; everything else is additive and off the hot path.

1. **Reader dispatch** — `openDeliveryJournal`/`load` (`delivery_lease.go:146`): read
   `delivery-lease-position.json` once; if it is a generation marker → `openSegmented(genRoot)`; else
   the current legacy body **byte-identical**. Reader-first prep lands only this dispatch + the
   refuse-not-recreate hardening (§7), with no writer.
2. **Lease decide** — `decide` (`:347`): on `j.leases` (active-RAM) miss, consult the nonce radix
   index before minting; seed `arrivals[session]` from the frontier index on a dormant miss. Map an
   index `unavailable` to the existing fail-closed `ErrDegraded` so ingest/drain need no change
   (confirm the class with main).
3. **ACK join** — `acknowledge`/`loadAcksFrom` (`:1400-1466`): the ACTIVE ack tail is joined against
   {active leases ∪ bounded radix `resolveLease`}; archived generations' joins are trusted via the
   content-addressed `archivedJoinRoot` (§6). `resolveAck` mirrors `resolveLease` over `ack-index/`.
4. **Active tails** — the two active `.jsonl`+`.pos` use `delivery_seal.go` unchanged; segment 0 is
   read at the **legacy paths** (no move).
5. **Writer lock + rollover transaction** — under the existing singleton `Lock` (already serialises
   every batch via `enter`/`ownedByFile`); the rollover is a writer op (disabled) that seals the
   active tail, folds its nonces/acks/arrivals into new content-addressed index generations, writes
   the new manifest, then commits one marker (§5).
6. **Recovery + backup** — open verifies the marker's generation root chain (bounded) then opens the
   active tail via the existing seal reader; `BackupWatchedFiles`/`backupLiveWriterFiles`
   (`internal/store/backup.go`) must additively watch the active tails + marker + current manifest —
   **out of author scope, returned to main.**

## 5. Rollover transaction and crash matrix (commit = the marker; legacy anchored throughout)

| step | action (content-addressed CreateNew unless noted; fsync each) | crash before this recovers to |
|---|---|---|
| G1 | seal the active tail to a final position (existing v2 seal) | prior committed generation / legacy; retried |
| G2 | **copy** (never rename) the sealed tail to `segments/lease-<g>` and `ack-<g>` | G1 state; legacy/prior still anchored |
| G3 | build + seal new `nonce-index`, `ack-index`, `frontier` pages folding the newly-archived tail; write `archivedJoinRoot` from the archival ack↔lease verification | G2 state; pages are content-addressed, re-derivable, mismatch refuses |
| G4 | create the empty next active tail; write immutable `gen-<rootHex>.json` (prevRoot = current) | G3 state; orphan artifacts ignored, GC later |
| **G5** | **`paths.WriteAtomic(delivery-lease-position.json, marker(rootHex))`** | **before: prior committed generation / legacy, whole rollover retried; after: new generation active** |

Every pre-G5 artifact is content-addressed and idempotent on retry (a matching hash is accepted, a
mismatch refuses and preserves evidence). **No step renames or deletes a legacy/prior file** — the
recreate-on-both-absent branch (`delivery_lease.go:182-195`) can never fire because the legacy
position anchor is present until G5 replaces it atomically (F2). A crashed half-built generation is
unreferenced by any committed marker → orphan → ignored, and the prior generation root still verifies
(F3). Active-segment seal crashes inside a generation are the existing A/B recovery, unchanged.

## 6. ACK completion vs publication vs durable evidence

Kept strictly distinct, as today:

- **Durable evidence** is the object + capture sidecar (main owns object sync and
  capture-link-before-ACK); the rollover moves neither.
- **Publication** (the committed frontier) is the ack tail line; a delivery is published only after
  capture+reference succeed. The rollover only relocates WHERE leases/acks live, never the ordering.
- **ACK completion** stays a per-delivery durable fact in the ack tail: an unacked delivery
  re-delivers under its **radix-recovered lease** (same `ObservationID`), and `resolveAck` answers
  "already acked" in bounded work across generations. An ack whose lease cannot be resolved within
  the bound is **unavailable**, never treated as a missing frontier. The ack↔lease consistency
  invariant `loadAcksFrom` enforces at open (`:1459-1465`) is preserved: active is re-joined,
  archived is the content-addressed `archivedJoinRoot`. A lease-index or ack-index `unavailable` must
  NOT be read as "delivery absent" (would re-mint or double-publish) — it is the fail-closed class.

## 7. Reader-first legacy-loader adaptation (no migration enabled)

Two changes, reader-only, shippable to current binaries without any writer:

- **Marker refusal (already true).** A generation marker is a small JSON, so `readDeliverySealImage`
  rejects it on size ≠ `deliverySealFileSize` and `loadDeliveryPosition` rejects it on
  `Version != EvidenceVersion` / `Chain.IsZero()` / canonical mismatch (verified in
  `delivery_seal.go`/`delivery_lease.go` last review). The revised marker MUST keep this dual-refusal
  property — pin it with a test feeding the marker to the current `selectSeal`/`loadDeliveryPosition`.
- **Refuse-not-recreate (the real prep).** Harden `openDeliveryJournal` (and the ack open at
  `:1075`) so that when a generation marker OR a `generations/` dir is present but the segmented
  reader is absent/older, it returns `deliveryJournalError()` (fail-closed) rather than taking the
  both-absent recreate path. This closes the residual case where a marker exists over an
  incomplete/foreign generation. It enables no migration: no rollover trigger, no segmented writer,
  no cap change.

This is a genuine prerequisite, but the copy-not-rename anchor (§5) is what actually removes the
recreation hazard; the reader hardening is defense-in-depth.

## 8. Shared durable-contract choices — returned to main (not the user)

1. **F4 join weakening:** open-time global ack↔lease join → per-generation archival join recorded in
   the content-addressed `archivedJoinRoot`. Cryptographically bound to the committed marker, but
   still trust of a past computation (not re-proof); the archival join code needs adversarial test.
   **Countersign required.**
2. **F1 product refusal:** a current binary still degrades to **unleased operation** against a
   segmented store (writes derived-id captures to the shared store; `leaseDelivery` returns
   unleased). Decide whether that is acceptable or a hard startup version-gate is required — the
   marker refusal is journal-deep only.
3. **Copy-not-rename:** disk grows by a retained legacy/prior copy until a retention pass; define when
   a prior generation may be reaped (only after the new marker is durable and no in-flight reader
   needs it) — no step may delete a journal.
4. **New durable structures:** the nonce/ack radix indexes and the frontier index are new on-disk
   formats; fix their versioning and the `H(nonce)` domain, and the collision-leaf policy.
5. **Backup coverage:** `BackupWatchedFiles`/`backupLiveWriterFiles` extension for active tails +
   marker + manifest (out of author scope).

## 9. Blocking critique of the NEW superseding observation sidecar (for early steering)

From `observation-binding-decision.md` §"Superseding decision" against `tooluseindex.go`:

- **Blocking risk — do not split the atomic superseding append.** `RecordToolUseSuperseding`
  (`tooluseindex.go:311-391`) writes the record AND all its supersede marks in ONE `s.tuW.write(buf)`,
  so "record present, marks missing" is **currently unreachable** (this is the SP08-D2 fix). The new
  "publication intent BEFORE the legacy record append" + "complete only missing operations whose
  precondition still holds" language implies re-introducing a record-without-marks partial state. The
  legacy record+marks append MUST stay one atomic write; the intent may recover only the OBSERVATION
  BINDING, never the marks. If the author splits the write to interleave the intent, it regresses
  SP08-D2. **Steer this now.**
- **Blocking risk — intent mark-replay must be precondition-guarded against later supersession.**
  The superseding write sets `SupersededBy = by` for a held record unconditionally
  (`tooluseindex.go:386`, `MarkSuperseded:477`). Replaying supersede targets from a stored intent
  after later writes could clobber a NEWER `SupersededBy`, corrupting lineage. The decision says
  "preserve later supersession" — correct, but the implementation must only mark a target still
  `StatusOK` (or not superseded by a newer `by`), else the intent replay is a lineage bug.
- **Correctness — the committed `ToolUseByObservation` must never surface an intent-only binding.**
  The pure lookup (and its in-memory map) must be populated only from committed legacy records;
  `RecoverToolUseByObservation` alone resumes intents. If the map is seeded from intents, a lookup
  returns an observation→id whose record does not yet exist.
- **Cost/scope note (not blocking):** "intent flushed + dir synced before legacy append" adds two
  fsyncs to EVERY publishable capture's publish path (not only prompts), where the atomic single
  append had one write. The daemon owner should measure this against the B-C budget.
- Consistent and fine: objects-before-publication, capture-link-before-ACK unchanged, "a sidecar
  intent is never itself permission to ACK," and the explicit "do not claim one Write is power-loss
  atomic."

## 10. Non-acceptance

Design only; no clearance, no writer enablement, no signoff. This closes F1–F5 with no new
dependency and no cap raise, but its crash-safety (§5), the F4 join weakening (§8.1), and the ACK
semantics (§6) are DESIGN claims that require the failure/rollback suite before acceptance — do not
read this as prototype safety. The superseding-sidecar critique (§9) is source-grounded steering for
the current author, chiefly: keep the record+marks append atomic and guard intent mark-replay against
later supersession. Durable-contract choices (§8) are main's, not the user's.

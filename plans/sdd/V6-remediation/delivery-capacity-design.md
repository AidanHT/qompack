# V6 delivery-journal capacity — SP20-D4 rollover/archival design

**Owner:** V6 delivery-capacity investigator (Opus 4.8, high effort).
**Status:** READ-ONLY inspection. No source authored; output is this file only. Shared durable-data
decision returns to main. No implementation, no release sign-off.
**Sources:** `internal/daemon/delivery_lease.go`, `delivery_seal.go`, `delivery_ack*`, `core.NewObservationID`,
`docs/architecture.md §0.1`. `plans/sdd/V6-remediation/remediation-contracts.md` is absent.

## 1. The defect (SP20-D4)

The lease and ack journals are single append-only files capped by format at
`deliveryLeaseMaxEntries = 65536` and `deliveryLeaseMaxBytes = 64 MiB` (`delivery_lease.go:28-30`).
When either cap is reached, `decide` returns `ErrBudget` and no further nonce can be leased
(`delivery_lease.go:358`, `:374`). V6's fail-closed ingest/drain then refuses ACK/dispatch of any
nonce-bearing delivery and preserves WAL/spool input (no identity-free replacement), so **the daemon
stops delivering permanently** rather than corrupting identity. Capacity itself is unresolved.

Identity to preserve: `core.ObservationID = HashBytes("qompack.observation.v1", session ‖ arrivalSeq₈)`
— **deterministic** in `(session, arrivalSeq)`, never content or random (`core/evidence`, arch §0.1
line 58). A redelivery must return the SAME ObservationID for arbitrarily late copies (arch §0.1
line 69, idempotent-by-persisted-ObservationID), which requires the durable `nonce → lease` binding to
survive, and `arrivalSeq` to continue per session and "never restart at zero" (arch §0.1 line 60).

## 2. Why the existing format cannot host rollover without a migration (exact evidence)

Four format invariants are each anchored to a *single file replayed from zero*. Rollover to a second
file breaks all four at the reader.

1. **Dense per-file arrivals from 1.** `loadFrom` rebuilds `j.arrivals[session]` by replaying the one
   file and enforces `lease.ArrivalSeq == j.arrivals[lease.Session]+1` with `arrivals` starting empty
   (`delivery_lease.go:562-566`). A new segment continuing session X at, say, arrival 40001 fails
   immediately: a fresh file's `arrivals[X]` is 0, so `40001 != 0+1`. **The reader has no mechanism to
   be seeded with prior per-session arrivals**, so a rolled segment is unopenable. This is the hard
   blocker.
2. **Fixed chain seed.** The chain is seeded at the constant `deliveryChainSeed = HashBytes(domain,nil)`
   (`delivery_lease.go:33`, `load():505`) and `sealInBounds` requires an EMPTY journal's chain to equal
   that seed (`delivery_seal.go:174-179`). A segment N+1 whose opening chain is segment N's final chain
   (needed for whole-history tamper-evidence) is rejected as an invalid empty seal. Cross-segment
   linkage is **not representable** in the current seal; independent per-segment chains would let an
   attacker drop an entire archived segment undetected.
3. **Whole file in memory, one position per file.** `j.leases` and `j.arrivals` hold the entire file
   (`delivery_lease.go:540-580`); the v2 seal names ONE `(bytes,count,chain)` position for ONE file
   (`delivery_seal.go`). There is no representation of "K archived segments + 1 active," and holding
   every nonce in RAM is exactly the bounded-memory problem rollover must solve.
4. **Idempotency consults only the in-memory map.** `decide` resolves a redelivery via `j.leases` and
   the batch's `b.fresh` only (`delivery_lease.go:347-353`); there is no on-disk fallback for a nonce
   whose segment has been archived out of RAM.

**Old/new reader compatibility (the migration gate).** An old binary opens only `delivery-leases.jsonl`
plus its one position file. Presented with a store that has archived segments it cannot see, it would
rebuild `arrivals`/`leases` from the active segment alone and then **reassign arrivals / re-mint
ObservationIDs** for archived nonces — the two outcomes the task forbids. Rollover is therefore not
silently backward-compatible; it needs an explicit version gate that makes an old reader REFUSE (the
same discipline `loadDeliveryPosition` already uses to refuse a v2 seal, `delivery_seal.go:46-48`).

**Conclusion.** Bounded-memory rollover is achievable, but not by extending the on-disk format in place:
invariants (1) and (2) are baked into the reader and the seal-bounds check, and (3)/(4) require new
on-disk structures. **A reviewed, version-gated migration is required.** Per-file seal tamper detection
(`delivery_seal.go`) is reusable *unchanged*; the journal-level framing above it is what changes.

## 3. Concrete implementation path (smallest compatible; a migration)

Segment the journal; reuse the v2 seal per segment verbatim; add a small sealed spine and two bounded
side structures. No limit is raised, no journal is deleted, no identity is forgotten, no arrival is
reassigned.

1. **Segment files** `delivery-leases.<seg>.jsonl` (seg = 0,1,…), each **within the existing 64 MiB /
   65536 per-file caps** and each with its own v2 seal + v1 sidecar. Rollover = seal-and-close the
   active segment at the cap, open seg+1. The per-file seal, its A/B slots, its tamper checks and its
   backup/rollback story are **unchanged** (delivery_seal.go reused as-is per segment).
2. **Cross-segment chain.** Segment seg+1's chain seed = segment seg's final sealed chain (read from the
   manifest), not the fixed `deliveryChainSeed`. This links the whole history: dropping or altering any
   archived segment breaks the next segment's opening chain check. *This is the reader change that makes
   it a new format* — the seed becomes per-segment.
3. **Manifest** `delivery-journal.json` (itself sealed/chained): the ordered segment list, each entry
   `{seg, bytes, count, finalChain, sealIdent}`, the active segment index, and a **format version the
   old reader refuses**. It is the tamper-evident spine over the segment chain and is bounded (one small
   record per segment; hundreds of segments = years of use), held as metadata only.
4. **Arrival continuity checkpoint** `delivery-arrivals.json` (sealed): `map[session]lastArrivalSeq` for
   sessions in a bounded active window (bounded by live sessions, not total deliveries). On open, seed
   `j.arrivals` from it so a new segment continues each session's arrivals densely across the boundary;
   the dense-arrival check (`:562-566`) becomes "contiguous with the seeded value," not "starts at 1."
   This continuation is what keeps ObservationID stable and never restarting at zero (arch §0.1 line 60).
5. **Bounded-memory idempotency for late copies.** Keep only the ACTIVE segment's `leases` map in RAM.
   Each sealed archived segment emits a compact **sorted, sealed nonce index** `delivery-leases.<seg>.idx`
   (`nonce → ObservationID`), giving O(log n) on-disk lookup. `decide` on a live-map miss consults the
   archived indexes (newest-first) before minting, so an arbitrarily late redelivery of an archived
   nonce returns its ORIGINAL ObservationID and mints nothing. RAM is bounded to: active segment map +
   per-index offset metadata + the arrivals checkpoint. The ack journal segments identically.
6. **Old/new reader gate.** Ship the segment-aware READER first (it also opens a legacy single-file
   store), then flip the writer to roll over — the exact two-step v1→v2 seal rollout precedent
   (`delivery_seal.go:82-88`). A legacy store stays old-binary-readable until the first rollover; the
   first rollover writes the versioned manifest, after which only new binaries may open it, and an old
   binary refuses via the manifest version rather than silently reading the active segment alone.
7. **Backup/rollback.** The maintenance backup already copies `state/` wholesale, so segments + manifest
   + arrivals checkpoint are captured; the consistency guard `backupLiveWriterFiles`
   (`internal/store/backup.go`) must be extended additively to watch the ACTIVE segment, the manifest
   and the arrivals checkpoint (today it watches the two journals + two seal sidecars). Rollback to a
   pre-rollover backup restores the single legacy file consistently and remains old-binary-openable.

## 4. Hard limit to state plainly

Bounded **memory** is achievable (segmentation + on-disk archived indexes + arrivals checkpoint).
Bounded **storage** is NOT, under the task's constraints: "never forget an old delivery identity" +
"never reassign arrivals" means every `(nonce → ObservationID)` and every per-session arrival must
remain resolvable forever, so the archived segment set and their indexes grow monotonically with total
deliveries ever leased. That growth is on-disk, archived and backup-able (not in RAM), but it is
unbounded. A truly bounded-storage design would require a retention/forgetting window, which the task
explicitly forbids — so this design trades unbounded disk for the identity guarantees, and says so.

## 5. Decision returned to main (shared durable-data)

1. Accept that SP20-D4 needs a **new, version-gated journal format** (segment manifest + per-segment
   chain seed + seeded arrival density + archived nonce indexes), delivered as a reviewed migration with
   a reader-first / writer-second rollout — not an in-place format extension? (Evidence: §2 invariants
   1–4, each file:line.)
2. Confirm the **unbounded-disk / bounded-memory** trade in §4 is the intended contract (identity
   permanence over storage bound), or is a bounded retention window in scope after all? Durable-data
   call is main's.
3. Scope: the ack journal, the maintenance backup watch-list, and the offline repair tool
   (`loadFrom`/Rule R) all inherit segment awareness; each is additive but must land in the same
   migration. No coordinator ruling changes until an owner implements and tests it; SP20-D4's carry row
   stays open.

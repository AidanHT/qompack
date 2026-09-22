# Delivery-journal rollover — implementation design (protocol, crash table, refusal)

Owner: V6 delivery-capacity implementer (Opus 4.8 high). Scope per
`delivery-rollover-coordinator-decision.md`. Writer stays DISABLED; this designs the format, the
reader, the refusal barrier and the bounded identity lookup. Corrects the four gaps the coordinator
named, plus one it did not (§7, the cross-segment ack↔lease join).

## 1. On-disk layout (segmented; only after a rollover, which is not enabled)

```
state/
  delivery-lease-position.json   ROLLOVER MARKER once segmented — the barrier (§4). NOT a v2 seal.
  delivery-journal.json          sealed manifest: the segment chain spine (§3)
  delivery-arrivals.json         sealed COMPLETE per-session arrival frontier (§5), all sessions ever
  segments/
    lease-NNNN.jsonl  lease-NNNN.pos   one journal segment + its own v2 seal (delivery_seal.go, reused verbatim)
    lease-NNNN.idx                      sealed, sorted full-lease index for archived segment NNNN (§6)
    ack-NNNN.jsonl    ack-NNNN.pos    ack-NNNN.idx     the same three for the ack journal
```

Legacy (pre-rollover) layout is unchanged: `delivery-leases.jsonl` + `delivery-lease-position.json`
(v2 seal) + `delivery-acks.jsonl` + `delivery-ack-position.json`. A legacy store never grows a
`segments/` dir. First rollover renames the legacy files to `segments/lease-0000.*` / `ack-0000.*`
(rename, never delete — non-lossy) and makes segment 1 active. The ACTIVE segment is always the
highest index; its append/seal hot path is delivery_seal.go verbatim on the segment's own `.pos`.

## 2. Identity invariants preserved (from arch §0.1, delivery_lease.go)

- `ObservationID = HashBytes("qompack.observation.v1", session‖arrival₈)` — deterministic; never
  re-minted for a nonce, never re-derived from content.
- `ArrivalSeq` per session is dense and monotone and NEVER restarts; it continues across a segment
  boundary from the sealed arrival frontier (§5), not from 1.
- A nonce → its full `deliveryLease` (Delivery, Session, RequestHash, ArrivalSeq, ObservationID) is
  resolvable for arbitrarily late copies, or the delivery is answered UNAVAILABLE — never re-minted,
  never false-absent (§6).
- Per-file caps (65536 / 64 MiB) are UNCHANGED; each segment obeys them. Only the number of segments
  grows, on disk.

## 3. Manifest (segment chain spine) — bounded per operation

`delivery-journal.json` is a small sealed head record, not a growing list:
`{format:"segmented", fmtV:1, headSeg:N, headSum:H_N, arrivalsSum:A, sum:S}` where `H_N` is a hash
chain over segment descriptors: `H_0 = HashBytes(mfDomain, desc_0)`, `H_k = HashBytes(mfDomain, H_{k-1}‖desc_k)`.
Each `desc_k = {seg:k, leaseBytes, leaseCount, leaseFinalChain, ackBytes, ackCount, ackFinalChain,
leaseIdxSum, ackIdxSum, joinOK}` is stored in `segments/desc-NNNN.json` (fixed small size), sealed by
its own sum. `headSum` chains them so dropping/altering ANY archived segment breaks `H_N` at open.

Bounded memory/op: only `headSum`, `headSeg`, the active segment state, and a bounded LRU of recent
descriptors live in RAM. Verifying the full chain is an offline/`fsck` operation, not a per-delivery
one; a delivery consults at most the active segment plus a bounded number of archived indexes (§6).

## 4. Old-reader refusal — through the EXISTING parsed barrier (gap 1)

The old binary's `load()` reads `delivery-lease-position.json` FIRST via
`loadDeliverySeal → {readDeliverySealImage | loadDeliveryPosition}`. The marker is engineered so BOTH
existing readers refuse it, with no new code in the old binary:

- Size ≠ `deliverySealFileSize` (32768) ⇒ `readDeliverySealImage` returns nil ⇒ v1 path.
- The marker is `{"delivery_format":"segmented","fmt_v":1,"manifest_sum":"<64hex>"}` (canonical). In
  `loadDeliveryPosition` it unmarshals into `deliveryPosition{v,bytes,count,chain}` with `v==0`
  (no `"v"` key) and `chain` zero, so BOTH `position.Version != core.EvidenceVersion` AND
  `position.Chain.IsZero()` fire, AND the canonical re-marshal `bytes.Equal(canonical, encoded)` fails
  on the unknown keys. `loadDeliveryPosition` returns `deliveryJournalError()`.

Therefore an old binary REFUSES to open the journal (fail-closed, `unavailable`), and never scans the
active segment alone — so it cannot reassign arrivals or re-mint. This is a real barrier (a file the
old reader already parses and rejects), not an "invisible extra manifest." The NEW reader dispatches
BEFORE `loadDeliverySeal`: if the position bytes parse as the segmented marker, take the segmented
reader; else the legacy `loadDeliverySeal` path, byte-identical.

`TestRollover_OldReadersRefuseTheMarker` proves it by feeding the marker to the CURRENT-tree
`loadDeliverySeal`/`loadDeliveryPosition`/`readDeliverySealImage` (which ARE the old reader) and
asserting `deliveryJournalError()`.

## 5. Arrival frontier — complete, never forgets dormant sessions (gap 2)

`delivery-arrivals.json` is a sealed record whose payload is a sorted list of `{session, lastArrival}`
for EVERY session that ever leased, chained/sealed like a segment. It is the durable frontier the new
active segment seeds from. Bounded memory: it is NOT loaded whole. At open, the active segment scan
seeds `arrivals[session]` for a session the first time that session appears in the active segment, by
a bounded lookup into the sealed frontier (sorted ⇒ O(log) seeks, capped); dormant sessions (absent
from the active segment) are never brought into RAM but remain on disk and resolvable. A new mint for
a dormant session performs the same bounded frontier lookup to continue its arrival densely; if the
lookup exceeds its seek bound the mint is answered UNAVAILABLE, not arrival 1. Every rollover rewrites
the frontier (sealed) folding in the archived segment's per-session maxima, so it is always complete.

## 6. Bounded identity lookup — full record + ack, unavailable on bound (gaps 3, 4)

Each archived segment `k` emits `lease-NNNN.idx`: a sealed file of the segment's `deliveryLease`
records sorted by `Delivery` (nonce), with a fixed-width sorted key table for O(log n) in-file binary
search, and `ack-NNNN.idx` the same for `deliveryAck`. Both carry a sum bound into `desc_k`
(`leaseIdxSum`/`ackIdxSum`), so a tampered index is detected against the manifest.

`resolveLease(nonce)`:
1. active segment map (in RAM) → hit returns full lease.
2. else consult archived `lease-NNNN.idx`, newest-first, up to `maxArchiveSeeks` segments / seeks.
   Hit returns the full lease (Delivery, Session, RequestHash, ArrivalSeq, ObservationID), so a
   redelivery re-validates RequestHash and returns the ORIGINAL ObservationID.
3. neither, within the bound → `unavailable`. Beyond the bound → also `unavailable` (never "absent").

`resolveAck(nonce)` mirrors it over `ack-NNNN.idx`. `acknowledged` becomes: active `acks` map, else
bounded archived ack lookup, else `unavailable`. Memory is bounded to the active maps + a bounded
index-handle cache; each operation is bounded to `maxArchiveSeeks`.

## 7. Cross-segment ack↔lease join (gap the coordinator did not name — SHARED-CONTRACT FINDING)

`loadAcksFrom` today requires EVERY ack's delivery to have a surviving lease with matching
ObservationID (delivery_lease.go end of `loadAcksFrom`). Across segments this join spans archived
data and cannot be done in bounded memory at open. Resolution: the join is verified ONCE, at ARCHIVAL
time, when segment `k` is sealed — every ack then live is checked against the cumulative lease set and
the result recorded as `desc_k.joinOK` (chained into `headSum`). At open, only the ACTIVE segment's
acks are joined against {active leases ∪ bounded archived `resolveLease`}; archived segments are
trusted via their sealed `joinOK`. This changes the open-time invariant from a full in-memory join to
a per-segment sealed proof. **This is a durable-data contract change and must be countersigned by
main** (it weakens the open-time global join to a chained per-segment proof; the two are equivalent
only if `headSum` verification is mandatory before any archived `joinOK` is trusted).

## 8. Rollover crash transition table (commit = R5)

| step | action (fsync each) | crash before this step recovers to |
|---|---|---|
| R1 | compute `desc_k` from the sealed segment k (deterministic) | legacy/active-k state; retried |
| R2 | write+seal `lease-k.idx`, `ack-k.idx` (CreateNew; re-derivable) | R1 state; recompute, overwrite-if-mismatch-refuses |
| R3 | write `desc-k.json` (CreateNew, sum-sealed) | R2 state; idempotent by seg index+sum |
| R4 | create empty segment k+1 files, seed chain=`leaseFinalChain_k`; rewrite sealed arrivals frontier | R3 state; k+1 files are CreateNew, re-seedable |
| **R5** | **atomically replace `delivery-lease-position.json` with the marker (paths.WriteAtomic)** | **before: still active-k (legacy or prior marker) — whole rollover retried. after: segmented, k+1 active** |

R5 is the single commit. Everything before it is deterministic from sealed segment k and is
idempotent on retry (CreateNew + sum-verify: a matching artifact is accepted, a mismatching one
refuses and preserves evidence). A crash during R4 leaves k+1 half-created with the position still
pointing at active-k, so the next open ignores k+1 and re-runs rollover. No step deletes a journal.

## 9. Backup / rollback

Rollback to a pre-rollover backup restores the legacy single file and stays old-binary-openable
(the marker was never in that backup). A backup of a segmented store captures `segments/`, the
manifest, and the frontier (the maintenance backup copies `state/` wholesale). The backup
consistency guard `backupLiveWriterFiles` (internal/store/backup.go) must be EXTENDED additively to
watch the ACTIVE segment's `.jsonl`+`.pos`, the manifest, and the arrivals frontier. That file is
out of this scope — **returned to main** (§11).

## 10. Reader dispatch (the one hot-path hook, delivery_lease.go)

`openDeliveryJournal`/`load`: read `delivery-lease-position.json` bytes once; `if isRolloverMarker →
openSegmented()` (new file), `else` the current legacy body unchanged. `openSegmented` verifies
`headSum`, opens the active segment via the existing per-segment open (reusing `loadFrom`/`openSeal`
against the active segment paths), and seeds arrivals from the frontier. This is additive; the legacy
path is untouched, so every existing test still exercises it.

## 11. Adaptations returned to main (out of author scope)

1. `backupLiveWriterFiles` / `BackupWatchedFiles` extension (internal/store/backup.go) for the active
   segment + manifest + frontier.
2. `decide`/`acknowledge`/`acknowledged` must call `resolveLease`/`resolveAck` on an in-memory miss
   and map `unavailable` to the existing fail-closed error class (ErrDegraded) so ingest/drain
   (out of scope) need no change; confirm the error class.
3. The §7 open-time-join → per-segment sealed `joinOK` weakening — durable-data countersign.
4. Writer/rollover TRIGGER stays disabled; enabling it is a separate authorized step after
   reader/failure/rollback tests pass.

## 12. Soundness verdict

Old-reader refusal (§4) and bounded identity lookup with `unavailable` (§5,§6) are both sound and are
implemented+tested as self-contained primitives this turn (see delivery-rollover-work.md). The
crash-safe segment lifecycle (§8) and the hot-path reader dispatch (§10) are specified here but their
end-to-end landing needs the failure/rollback suite that is out of this turn's scope; they are NOT
claimed done. This is the design + verified linchpin primitives, with the remaining integration named,
not a partial rollover called fixed.

# Delivery-journal rollover — implementation record (SP20-D4)

Owner: V6 delivery-capacity implementer (Opus 4.8 high). Scope per
`delivery-rollover-coordinator-decision.md`. Design: `delivery-rollover-implementation-design.md`
(produced first, per protocol). No commits, no children, writer/rollover trigger NOT enabled.

## Delivered this turn (in scope, additive, verified)

New files only — no edit to the shared crash-critical hot path (delivery_lease.go / delivery_seal.go);
see "Boundary" for why. Old journal path and all old tests are untouched and runnable.

- `internal/daemon/delivery_rollover.go` — the **old-reader refusal barrier** (gap 1) and the manifest
  segment chain (gap 3 tamper spine):
  - `encodeRolloverMarker` / `parseRolloverMarker` / `isRolloverMarker`: the segmented-store marker at
    `delivery-lease-position.json`. It is engineered so the pre-rollover reader refuses it through the
    barrier it ALREADY parses — no `v:1`, zero chain, unknown keys, wrong size — while the new reader
    recognises it and dispatches.
  - `segmentDescriptor`, `manifestHead`, `manifestChainStep`, `verifyManifest`: `H_k = H(H_{k-1}‖desc_k)`
    over a dense 0..n segment chain, so dropping/altering/reordering any archived segment fails.
- `internal/daemon/delivery_archive.go` — the **bounded identity lookup** (gaps 3, 4):
  - Sorted, sealed per-segment lease and ack indexes carrying the FULL `deliveryLease`
    (Delivery, Session, RequestHash, ArrivalSeq, ObservationID) and `deliveryAck`.
  - `resolveLease` / `resolveAck` with an explicit per-op bound (`maxArchiveSeeks`) returning
    `found` (full record) / `absent` (whole archive searched within bound → genuinely new nonce,
    mintable) / **`unavailable`** (bound reached before the archive was fully searched → fail closed,
    NEVER a false absence, never a re-mint).

Tests (new files, run with the marker present):
```
$ GOMAXPROCS=2 go test -run 'TestRollover_|TestArchive_' -count=1 ./internal/daemon
ok  github.com/qompack/qompack/internal/daemon  0.218s   (EXIT=0)
```
`TestRollover_OldReadersRefuseTheMarker` (feeds the marker to the CURRENT-tree
`readDeliverySealImage`/`loadDeliveryPosition`/`loadDeliverySeal`, which ARE the old reader, and asserts
refusal), `TestRollover_MarkerRoundTripAndTamper`, `TestRollover_ManifestChainDetectsTampering`,
`TestArchive_BoundedLeaseLookup`, `TestArchive_BoundedAckLookup`, `TestArchive_IndexSealAndDuplicates`
— all PASS. `gofmt -l` and `go vet ./internal/daemon` clean. No existing test was modified.

## The four coordinator-named gaps — dispositions

1. **Old reader refusal via an existing barrier** — SOLVED and proven (§4 of the design;
   `TestRollover_OldReadersRefuseTheMarker`). The marker is not an "invisible extra manifest"; it
   occupies the position file the old reader parses and rejects.
2. **Active-only arrivals forget dormant sessions** — design §5: a COMPLETE sealed per-session frontier
   on disk; memory holds only sessions in the active segment; dormant sessions stay resolvable via a
   bounded lookup; a mint that cannot reach a dormant frontier within its bound answers `unavailable`,
   never arrival 1. (Frontier structure specified; not yet coded — see Boundary.)
3. **Metadata grows unbounded** — design §3/§6 + `delivery_archive.go`: per-op work is bounded to
   `maxArchiveSeeks`; only a bounded working set is in RAM; full-chain verification is an offline/fsck
   op. Disk history grows (unavoidable under "never forget"); memory and each op are bounded, with
   `unavailable` on bound-exceed (implemented and tested).
4. **Nonce index needs full lease/session/request-hash + ack** — implemented: the lease index stores
   the full `deliveryLease` and the ack index the full `deliveryAck`; `resolveLease`/`resolveAck` cover
   both.

## Shared-contract findings for main (return-to-main, not user questions)

- **F1 — cross-segment ack↔lease join (not in the coordinator's list; found in `loadAcksFrom`).** The
  current open requires every ack's delivery to have a surviving in-memory lease with matching
  ObservationID. Across segments this global join cannot run in bounded memory. Design §7 moves it to a
  per-segment sealed `joinOK` verified at ARCHIVAL time and chained into `headSum`. **This weakens the
  open-time global join to a chained per-segment proof and is a durable-data contract change requiring
  main's countersign.** It is sound only if `headSum` verification is mandatory before any archived
  `joinOK` is trusted.
- **F2 — `backupLiveWriterFiles` / `BackupWatchedFiles` (internal/store/backup.go, out of scope).** Must
  additively watch the ACTIVE segment `.jsonl`+`.pos`, the manifest and the arrivals frontier, or a
  consistent backup of a segmented store is not guaranteed. Additive; main owns that file.
- **F3 — `decide`/`acknowledge`/`acknowledged` (delivery_lease.go) integration.** On an in-memory miss
  they must call `resolveLease`/`resolveAck` and map `resolveUnavailable` to the existing fail-closed
  error class (ErrDegraded) so ingest/drain (out of scope) need no change. Confirm the error class.
- **F4 — reader dispatch hook (delivery_lease.go `openDeliveryJournal`/`load`).** One additive branch:
  `if isRolloverMarker(positionBytes) → openSegmented() else legacy`. Legacy path byte-identical.

## Boundary — what is NOT done, and why (not "partial rollover called fixed")

The self-contained, provably-sound linchpins (refusal barrier, bounded lookup, tamper chain) are
delivered and tested. NOT implemented this turn: the crash-safe segment lifecycle (design §8), the
durable arrivals-frontier read/update, the on-disk index materialisation, and the hot-path reader
dispatch (§10). Reasons, per the decision's "state the precise blocker" clause:

1. Those edits land in delivery_lease.go's crash-critical open/lease/ack path, which **main is
   concurrently editing** (git shows delivery_lease_cap_test.go and neighbours as main's); editing the
   same functions risks reverting others' work, which the decision forbids.
2. Their correctness cannot be established without the failure/rollback/crash test suite, which is
   **out of this turn's scope** (no whole-tree/race/e2e/failure runs authorized).
3. Writer enablement stays blocked by the decision until reader/failure/rollback tests pass; wiring the
   dispatch without those tests would be enabling-adjacent.

The soundness verdict stands: old-reader refusal and bounded identity lookup ARE sound (proven). The
remaining integration is fully specified in the design with exact hooks (F1–F4) and is the next gated
step, owned jointly with main. SP20-D4 stays open; the caps are unchanged; nothing forgets an identity
or reassigns an arrival.

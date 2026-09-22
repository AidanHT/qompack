# GC segmented-delivery integration + retention-read hardening — work record

Store-side, read-only integration of the daemon's segmented delivery journal (SP20-D4;
`delivery-segment-retention-decision.md`) into GC's retention harvest, plus the finish of the prior
retention-read safety round. Exclusive scope: `internal/store/gcrun.go`,
`internal/store/gc_delivery_segments.go`, and the `gc_retention_read_*` / `gc_delivery_segments` tests.
No daemon import (§3.2), no producer mutation, no shared-wire rewrite, **no enablement of rollover**.
The producer wire is UNSHIPPED; this decoder is dormant on every current (legacy) store.

## Part A — finishing the prior retention-read safety round

1. **`os.SameFile` was broken on Windows.** The prior round compared a plain `os.Lstat` result against an
   opened handle's `Stat`; on Windows `os.Lstat` goes through `FindFirstFile`, which does not populate
   the file-identity fields `os.SameFile` needs, so the comparison was always false and every lease
   harvest halted. Fixed with `rootedLstat(dir, base)` — a handle-based `os.Root.Lstat` whose `FileInfo`
   `os.SameFile` accepts — used in `openRetentionRoot`, `listRetentionDir`, and `dsegListSegmentDirs`.
2. **Ancestor confinement.** `openRetentionRoot`/`listRetentionDir` now reject a symlink/reparse
   component anywhere between the trusted project root and the target via `maintNoFollow(s.root, path)`
   — pinning only the immediate parent through `os.Root` (the prior approach) still follows an
   already-aliased `.qompack/state`. The comment no longer over-claims: `os.SameFile` DETECTS a
   Lstat→open swap, it does not prevent a symlink being followed during a blocking open, and a
   path-based shared open does not itself pin every ancestor (`maintNoFollow` does that, once, first).
3. **Explicit accumulated file-count ceiling.** Bounded batch reads do not bound the accumulated name
   list, so `maxRetentionSources` (1<<20) caps the TOTAL sources across all directories; reaching it
   truncates the whole mark (nothing swept), never a partial harvest.
4. **Enumeration budget after each batch, not before the first read.** `listRetentionDir` now checks
   ctx/deadline BETWEEN batches (breaking on `io.EOF` first), so a trivially small or empty directory
   always completes — matching the old `os.ReadDir` — while a genuinely large enumeration still
   truncates on the deadline. (A regression the broad suite caught: an expired deadline was truncating
   the mark on an empty `checkpoints/` dir; `TestGC_MarkIndexWalksAreNotTruncatedByTheDeadline`.)
5. Exact ACK join (nonce + exact non-zero canonical observation identity + known version) and the
   directory/FIFO/non-regular refusals from the prior round are retained and now apply across segments.

## Part B — segmented delivery integration (`gc_delivery_segments.go`)

A structural, byte-exact **read-only mirror** of `internal/daemon/delivery_segment.go`. It replicates the
producer's constants, field order, `sha256(domain‖0x00‖body)` chain (`dsegDigest`/`dsegChain`), head
and transition-log records, and validates:

- **Authority.** The whole transition log is validated as a chain from the seed; the head must name an
  exact committed record on a byte boundary (`log_bytes`/`last_len`); a complete tail beyond the head is
  adopted as newer authority WITHOUT writing anything.
- **Dense `active == seq` from zero** (main's correction — the producer now enforces dense; a non-dense
  active halts). seq 0 is the legacy active-0 segment; the base root is empty at active 0 and a 32-byte
  hex root otherwise.
- **Four files per committed segment**, not two: lease journal, ack journal (both regular, harvested)
  AND both position seals (`delivery-lease-position.json`, `delivery-ack-position.json`), each checked
  format-independently — confined, regular, non-empty, bounded (`dsegRequireSeal`). A missing or
  zero-byte seal is a structurally incomplete segment and halts. (Store cannot import the daemon's seal
  decoder, so the check is structural, not semantic.)
- **Legacy vs migrated.** No head + no log + no migration evidence (`delivery-generations/` or a
  non-empty `delivery-segments/`) → legacy: harvest only segment 0, exactly as before. Any migration
  evidence surviving without a readable authority, or any torn/missing/conflicting/unknown authority,
  halts — the reader NEVER reverts to legacy when a rotation's authority is present but unreadable.
- **Stable frontier over head AND log, rechecked AFTER the whole harvest.** `resolveDeliverySegments`
  records the head bytes, the full log bytes, and the committed set as a witness; `dsegRecheckFrontier`
  (called at the end of `harvestHashes`, after every retention source is harvested) re-reads head and
  log and refuses on any change, on newly-appeared migration evidence for a legacy tree, or on any
  now-missing required segment file. A committed rotation is a durable log APPEND that precedes the head
  checkpoint, so witnessing the head alone would miss it — the whole log is witnessed.
- **Required sources.** Committed-segment lease/ack sources are marked `required`; if one that existed at
  resolve vanishes mid-harvest, `harvestFile` halts rather than silently treating it as an empty
  optional source.
- **Cross-segment ACKs.** ACKs from every committed and staged segment fold into one set (bounded by
  `deliveryAckSetMax` total pairs); an ack recorded in a newer segment settles an older segment's lease.
- **Staged (uncommitted) segments** are conservatively harvested (their lease roots retained, never
  deleted), not required.
- **Bounds.** `dsegMaxLog` (1MiB), `dsegMaxLine` (4096), `dsegMaxSeal` (64KiB), `dsegMaxSegments`
  (65536); canonical 20-digit segment dir names only (no inferred history from a directory sort);
  enumeration answers to ctx.

`gcRootFiles` now assembles the lease sources from `deliveryLeaseSources` (legacy segment 0, or all
committed + staged segments) and threads the frontier to `harvestHashes` for the post-harvest recheck.

## Bugs found and fixed this round

- **`present`/`missing` bool inversion** in `dsegReadFileBounded`/`dsegReadHead`: an absent head was
  mis-handled and a present head treated as absent, which halted every legacy store. Fixed to consistent
  `present` semantics.
- The Windows `os.SameFile` and deadline-empty-dir regressions above.

## Verification (recorded via run.py; exact IDs and status)

- **Negative control (pre-integration reader), `gc-segments-negctl` — FAIL (exit 1):** the legacy-only
  reader fails the segment tests (segment-1 lease swept, cross-segment ack not settled, no halt on
  torn/missing authority); the legacy guard passes. (Preserved from the prior session as required.)
- **Negative control (new obligations), `gc-segments-r3-newchecks-negctl` — FAIL (exit 1):** with the
  seal check, dense check, and frontier recheck each neutered, `TestGCSegments_MissingSealHalts`,
  `…_NonDenseActiveHalts` and `…_FrontierRecheckDetectsLogTailRotation` fail — proving each check is
  load-bearing. (MissingSeal was additionally re-confirmed in isolation with only `dsegRequireSeal`
  neutered → FAIL at the halt assertion.)
- **`gc-segments-r3-1` — PASS (exit 0):** all `TestGCSegments_*` + `TestGCRetention_*`.
- **`gc-segments-r3-2` — PASS (exit 0):** all 11 `TestGCSegments_*` (11 RUN, 11 PASS, 0 SKIP/FAIL).
- **`gc-r3-broad` — FAIL (exit 1):** caught the deadline-empty-dir regression
  (`TestGC_MarkIndexWalksAreNotTruncatedByTheDeadline`); fixed.
- **`gc-r3-deadline` — PASS (exit 0):** the mark/deadline/cancel/resume tests plus segment/retention.
- **`gc-r3-broad2` — PASS (exit 0, 104 s):** `TestGC_* | TestGCRetention_* | TestGCSegments_* |
  TestCompactRetention* | TestDeclaredRetentionLines* | TestLoadRoots* | TestAppendRetentionRoot*`.
- `gofumpt -l` clean on all five files; `go vet ./internal/store` exit 0; `GOOS=linux go build
  ./internal/store` exit 0.

Stale/superseded prior-session artifacts, preserved: `gc-segments-fix-2` (FAILED, before the SameFile /
bool fixes) and `gc-retention-r2-suite2` (killed mid-run, never completed — status "running", 0-byte
log). Neither is a result; both are kept as evidence, not repeated.

### Tests (literal protocol fixtures)

`TestGCSegments_`: HarvestsOldestLeaseAcrossRotation, AckInLaterSegmentSettlesOlderLease,
MissingCommittedSegmentHalts, MissingSealHalts, NonDenseActiveHalts, TornLogHalts, UnknownSchemaHeadHalts,
MigrationEvidenceWithoutAuthorityHalts, StagedSegmentLeaseRetained, FrontierRecheckDetectsLogTailRotation,
UnmigratedTreeStaysLegacy. Fixtures build the exact head + chained log + four per-segment journal/seal
files with the store's own mirrored primitives.

## Limitations returned to main (no acceptance claim)

- **Cross-package agreement test is OWED.** This decoder duplicates the daemon's wire; a test that pins
  `dsegDigest`/`dsegChain`/field-order/constants byte-for-byte against the actual producer is owed (main
  is adding actual-producer GC agreement tests). If the producer's constants/shape drift, this decoder
  will (safely) refuse a valid authority rather than mis-read it.
- **Seal validation is format-independent (structural), not semantic** — confined/regular/non-empty/
  bounded. It cannot verify the seal's chain/position without importing the daemon decoder.
- **Post-harvest frontier recheck is unit-verified for the static case** (`FrontierRecheckDetectsLogTailRotation`
  mutates the log and calls the recheck directly). A live, concurrently-rotating race test is not
  written here (no injection seam) and is owed to main's producer-integration tests.
- **The Unix FIFO ack test (`gc_retention_read_unix_test.go`) is compile-verified, not executed** on this
  Windows host; `GOOS=linux go build ./internal/store` and gofumpt pass, but the store test binary links
  the daemon, so a linux run is deferred to CI/WSL.
- Residual concurrent-swap / blocking-open-of-a-FIFO races remain (documented in the code); bounded by
  the retention paths living under the project's own `.qompack` tree.
- **No enablement.** Rollover stays off; this is retention-read integration only. Archive retention and
  backup/restore parity remain main's to review before any enablement.

## Bounded correction — real seal-structure validation (round 4)

Main rejected the prior `dsegRequireSeal`, which accepted any non-empty bounded bytes ("a pretend
nonempty check"). Replaced with a **structural read-only validation of the supported seal format**, in
`gc_delivery_segments.go` only (no `gcrun.go` / `gc_retention_read*` edits — main now owns ACK
validation there).

- **Small exact read-only mirror** of `internal/daemon/delivery_seal.go` (store cannot import the daemon,
  §3.2): the v2 fixed-size (32768-byte) A/B image layout (`dsealIsImage`), per-slot classification
  (`dsealClassifySlot`) with the producer's canonical round-trip, in-bounds check, seq/parity, and the
  per-record **sum** (`dsealSum` = `HashBytes("qompack.delivery.seal.v2", chainDomain‖0x00‖slot‖0x00‖
  dec(seq)‖0x00‖dec(bytes)‖0x00‖dec(count)‖0x00‖chain)`), the strict A/B accept table (`dsealSelect`),
  and the v1 sidecar (`dsealParseV1`). The lease seal is validated against the lease journal's chain
  domain/seed, the ack seal against the ack's, so a record summed for the wrong journal is refused.
- **Both supported formats accepted**, not v1-only: the running producer writes v2 fixed-size A/B seals;
  a fresh segment (and a rolled-back build) writes a v1 empty sidecar. `dsegValidateSeal` takes either.
- **EXACT LIMIT (stated in code):** this validates the seal FILE's own supported structure and integrity
  fields; it deliberately does NOT re-scan the segment journal / its chain / the generation store to
  confirm the sealed position matches the journal bytes — that whole-journal fsck lives in the daemon's
  offline reader (`delivery_segment_readonly.go`), which store cannot import. A cross-package agreement
  test (main's `delivery_readers_v6_test`) is **owed** to keep the mirror in step; if the producer's
  layout / sum domain / chain domains / bounds drift, this refuses a valid seal rather than mis-accepting.
- **Fixtures corrected**: `installSegmentFiles` now writes REAL canonical v1 empty seals (each journal's
  seed), not the prior fake `{"seal":0}`; a `buildV2Seal` helper builds a real fresh v2 A/B image.
  Prior scenarios preserved.

**Verification (run.py, exact status):**
- Negative control `gc-segments-seal-negctl` — **FAIL (exit 1):** with `dsegValidateSeal` neutered to the
  old non-empty check, `SealGarbageHalts`, `SealUnknownSchemaHalts`, `SealTornV2Halts` and
  `SealWrongJournalSumHalts` all FAIL (they no longer halt), while `ValidV2SealAccepted` PASSES —
  proving the structural validation is load-bearing.
- `gc-segments-seal-1` and `gc-segments-seal-final` — **PASS (exit 0):** all 16 `TestGCSegments_*`
  (11 prior + Garbage/UnknownSchema/TornV2/WrongJournalSum/ValidV2), 0 SKIP/FAIL. `gofumpt -l` clean;
  `go vet ./internal/store` exit 0.
- Prior-round artifacts (`gc-segments-negctl`, `gc-segments-r3-*`, `gc-r3-broad2`, etc.) are immutable and
  not repeated (main's real daemon-writer GC integration and archived-ACK startup fix are separate).

New seal tests: `TestGCSegments_SealGarbageHalts`, `_SealUnknownSchemaHalts`, `_SealTornV2Halts`,
`_SealWrongJournalSumHalts`, `_ValidV2SealAccepted` (all with real supported seal encodings).

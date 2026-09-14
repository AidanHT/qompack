# 14. The delivery path's group commit and the A/B seal

Date: 2026-09-13

## Status

Accepted, on branch `verify/v5-final`. It records decisions that are already implemented and
merged rather than proposing new ones: SP20-D1's group commit and the format-2 seal landed at
`3d95582`, the drain progress floor fix that closed its part 3b at `eb4a76d`, and the re-budget
that those two made measurable at `5d0b904`, corrected at `cbfa3d3`.

It settles four of the twelve questions that
`plans/sdd/V4-SP-20-capture-storage-and-state-remediation/sp20d1-design-final.md` put to the owner,
in the direction the code took: **Q6** (the accessor's ownership read relocates into the per-batch
check), **Q7** (a strict reader plus operator repair, not an automatic Rule R), **Q10** (the batch
caps accepted as designed, untuned) and **Q12** (the write-format switch as a package constant).
**Q9** asked for the SP-20 author's countersign on the release-after-seal reading of invariant I3,
on the rewritten `Accept` comment, and on this ADR; the first two are in the tree and this document
is the third. The design's change list (§5, `docs` row) and its risk R7 both required it, and
nothing wrote it; `plans/V5-report.md` §31.5 carries that as an open row, which closes here.

Q3, the co-load treatment of B-B, is **not** settled here. It is ADR 0010's Addendum 1, and this
ADR points at it rather than restating it.

## Context

Since `f6a8691` made the delivery path durable, `ingest.Accept` carries three durability points and
the ACK byte is written only after it returns: the WAL segment fsync, the delivery-lease journal
fsync, and the seal of the position the journal is validated against on recovery. The order is
fixed and none of the three is optional — the WAL holds the received bytes, the journal holds the
nonce to arrival to ObservationID assignment a redelivery must recover rather than mint again, and
the seal is what detects a truncated journal. B-B times that region in full. A leased `Accept`
measured 18.4-23.3 ms on a quiet AC Windows host against an `L0IngestMs` of 2 and an
`AckDeadlineMs` of 8, so in the hot-path harness every hook timed out, spooled and exited, and the
burst queued behind `Lock.mu` (design §1.3 M2, §1.4).

The owner's ruling on 2026-09-10 was **keep durability, re-budget** (`plans/V5-report.md` §31.2,
item 1): do not weaken an fsync to meet a budget. Group-commit the WAL, lease and ack writes,
replace the single-slot delivery seal, and only then raise B-B to a measured durable p99 and raise
`AckDeadlineMs` to match. The measurement gives `L0IngestMs` 50 and `AckDeadlineMs` 73 on Windows
(§31.3); linux (15/17) and darwin (40/45) are seeded from the design's table and are marked
provisional in `internal/config/deadlines.go` itself, pending CI's `bench-gate`.

The single-slot seal had to go for a safety reason, not a latency one. `savePosition` sealed the
position with `paths.WriteAtomic` — a temp file, a temp fsync, a rename, a directory fsync — once
per lease. Keeping one slot and writing it in place loses the seal outright to a torn write
(design §2.12, X7). Two slots written alternately always leave a complete older seal on disk. The
in-place write is what makes design risk R10 reachable: a running daemon now rewrites position
sidecars while it serves, so a backup can copy a slot mid-write and record the copy as consistent.
`55b6f68` refuses such a backup with `store.ErrBackupMoved`, and `af34615` reads the files with
delete sharing so the backup cannot stall the daemon in turn.

## Decision

**D14-1 — The WAL, lease and ack writes are group-committed, with caps that are design-cited
rather than tuned.** `internal/daemon/groupcommit.go`'s `groupQueue[T]` is a leader/follower queue:
the first arrival leads, everything queued when the leader cuts rides in its batch, there is no
linger, no timer and no new long-lived goroutine, and an isolated delivery is a batch of one that
runs today's syscall sequence inline. Three pipelines use it — `ingest.walQ`, and the journal's
`leaseQ` and `ackQ` — so an acknowledgement commit can no longer delay a lease batch. The caps are
`groupCommitMaxRequests` 512 for all three, `walGroupCommitMaxBytes` 4 MiB and
`journalGroupCommitMaxBytes` 1 MiB, each citing the design section it comes from, with the head of
the queue always admitted so that an oversize request commits alone rather than never. They are
accepted untuned (Q10). A cap here is a blast radius and a bound on a follower's wait, not a
throughput knob: 512 is how many callers one failed commit fails with it, and the fill observed on
`BenchmarkIngestAcceptLeasedParallel` is about 32. Tuning a number the workload does not reach
would fit a constant to a benchmark.

**D14-2 — The seal is an in-place A/B slot write in format 2, read by a strict reader.**
`internal/daemon/delivery_seal.go`: the sidecar is a fixed 32 768-byte file that is one valid JSON
document, holding two 480-byte slot regions a 16 KiB stride apart, each inside its own sector and
4 KiB block. A seal writes the slot that holds seq−1, `paths.SyncData`s it, and then proves the
path still names the held inode before anything is released. `deliverySealWriteFormat = 2` is the
format this build writes. `selectSeal` accepts exactly two images — one valid slot at seq 1 beside
an empty slot, or two valid slots one seq apart where the older seals strictly fewer bytes and
strictly fewer entries — and refuses everything else: any invalid slot, an empty slot beside a seq
other than 1, two empty slots, a seq gap, a parity violation. A refusal writes nothing, so the
evidence survives. This is safe to be strict about because every crash-reachable image is one of
the two accepted rows: a write goes to the slot holding seq−1, and only after the bytes it seals
are durable. An invalid slot is therefore media damage or a foreign write, and guessing at one is
not recovery. The format's one rollback window is stated in `selectSeal`'s own comment rather than
claimed away — a file whose slots hold seq 1 and seq 2 becomes, once slot b is erased to exactly
`null` and padding, indistinguishable from a fresh file, and is read one batch back. Nothing is
misread there: the position read is one this journal really sealed, and load recovers the complete
tail past it. Which format a file is in is decided by the file's own layout (`loadDeliverySeal`),
never by a flag, so the two readers cannot disagree about which one owns a file; a v2 image whose
slots do not select is refused rather than handed down to the v1 reader.

**D14-3 — Rule R, the automatic acceptance of a torn slot, is not in the daemon.** It would accept
one valid slot beside one invalid one whenever the journal extends past the valid record, which in
steady state it always does — and that admits "rot of the newest slot plus a later line-aligned
truncation inside the last batch", which loses released identities silently where the strict reader
refuses. So it exists only offline: `sealRuleR` in `internal/daemon/delivery_seal_tool.go`
(`1696bd4`) is the only implementation, reachable only through `DeliverySealOptions.AcceptTornSlot`
together with `Confirm`, wired as `qompack admin delivery-seal --accept-torn-slot --yes`
(`internal/cli/admin.go`, `c2109d0`; the tool itself at `d1e2477`). It refuses two valid slots,
anything beside an empty slot, two invalid or two empty slots, and every image the strict reader
already accepts; it prints exactly which journal lines the acceptance admitted, and a run whose
report could not be written refuses the conversion rather than making the change without its
record. Operator repair beats automatic acceptance because the two differ in who carries the loss.
Automatic acceptance turns an unknown quantity of silent identity loss into a green start, once per
damaged file, with nobody told. The flags turn it into an unavailable journal, preserved evidence,
deliveries counted as unleased gaps, and one person who has read the lines they are admitting — and
a refusal is the same class this journal already applies to a torn journal tail, so the seal is
being held to the standard the rest of the file already meets.

**D14-4 — Invariant I3: no lease is released before its batch's seal.** `Accept`'s doc comment
forbids "sealing the position once per batch instead of once per lease". The forbidden act is
*release before seal*, not *one seal per line*: what the prohibition protects is the set of leases
handed to a caller, a job or the ACK while not covered by a durable seal, and that set is empty at
every instant here, exactly as it was with one seal per lease. Admission to the journal's identity
maps, every follower wake-up, and `Accept`'s return all happen after the seal and its post-seal
identity check have returned. What grows, from one line to at most one batch, is only the set of
lines that are durable, unsealed and **never released**, after a crash between the journal fsync
and the seal — today's "crash before the seal" outcome, which no caller ever saw and which the next
open re-seals through its complete-tail recovery. T10,
`TestDeliveryJournal_NoLeaseReleasedBeforeItsBatchSeal`, pins it at both edges: while the seal is
in progress and while it is durable but its call has not returned, no member has returned, nothing
is admitted, and `Release` cannot overtake the batch.

**D14-5 — Ownership moves from the accessor's lock-file read to the per-batch check (Q6).** The
accessor `Lock.openDeliveryJournal` now hands out an already-open journal without reading the lock
file; `Lock.ownedByFile()` re-reads it once per lease batch and once per acknowledgement batch,
after every member has arrived and before anything is appended or answered, and `acknowledged`
keeps `owned()` under `Lock.mu`. The read it removes ran under `Lock.mu` on every `Accept` — a
serial section in front of the durable path, and outside every group commit. Coverage is relocated,
not dropped, and the in-memory `released` check stays in the accessor. One visible difference is
recorded in the code rather than left to be discovered: a lock this process has lost is no longer
refused in the accessor but by each lease, whose callers count the same unleased gap and also log
the refusal at Warn. T20, `TestDeliveryJournal_AccessorOwnershipIsRecheckedPerBatch`, pins it.

**D14-6 — The write-format switch is a package constant, not a `migrationBuildGates` entry (Q12).**
`deliverySealWriteFormat` lives in `internal/daemon/delivery_seal.go` and is pinned by
`TestDeliverySeal_WriteFormatIsDeliberate`. ADR 0013's D13-6 gates are feature gates for M1-M3
migration behaviour, refused by `config.Validate` until an owning subplan flips one in a reviewed
commit; a seal's on-disk format is neither a feature nor user-visible configuration, and
registering it there would have meant changing an existing pinned table for a constant no operator
should set. `migrationBuildGates` is untouched.

**D14-7 — Rollback is that constant back to 1 plus the offline tool, never a revert.** The flip
commit `5d32859` also carries the format-2 coverage that a format-1 build still needs in order to
read the images it converts, so reverting it would remove the reader that makes the rollback safe.
The caveat, from `plans/V5-report.md` §31.6: a binary that understands only format 1 **fails
closed** on a format-2 seal — it parses the document, sees `"v":2` and refuses on the version, so
there is no misread and no partial read, only an unavailable journal with the WAL and the spool
still durable and the seal files untouched. Four things can leave a v2 file for such a binary to
meet: a crash; a `Release` whose journal was faulted or whose lock had been lost; an open that
converted a v1 file and then failed; and a downgrade that itself failed, which is best effort,
never blocks the release of ownership, and is now recorded as `Lock.SealDowngradeResidual` and
logged once at Warn rather than discarded. Each is repaired three ways, none of which loses data:
run a build carrying the dual reader once and stop it cleanly, run
`qompack admin delivery-seal --to v1`, or restore a verified backup taken with the daemon stopped.

## Consequences

- **The durable path got cheaper, and the seal is where the saving is.** A leased `Accept` is
  7.306 ms uncontended on the close-out's Windows host (`BenchmarkIngestAcceptLeased`, `-count=6`)
  against the 18.4-23.3 ms measured on that host class before this work: WAL sync 2.243, journal
  sync 2.197, seal slot 2.305, and about 0.4 ms outside the flushes. A slot write plus a
  `SyncData` into a held handle replaces a temp file, a temp fsync, a rename and a directory fsync.
  The format costs four `WriteAtomic`s per daemon lifetime — convert both seals at open, downgrade
  both at `Release` — and **zero per delivery**, plus 2 × 32 KiB of disk while a daemon runs.
- **The budget got much wider, and that is host contention rather than slack.** B-B p99 ran
  11.264-36.864 ms across the fifteen attested runs and its p50 7.168-14.336 ms, on a quiet host,
  because the harness is starting 2 000 processes beside it — the spawn floor alone is p50 22.3 ms,
  p99 77.1 ms. `L0IngestMs` is 50 on Windows and `AckDeadlineMs` 73, derived as
  `roundup5(1.25 × 36.864)` and `50 + ceil(22.257)` and confirmed by three acceptance runs. So the
  hook now blocks up to 73 ms on Windows before it spools, where it blocked 8 ms before, and a
  budget nearly seven times the service time is a coarse backstop rather than a regression gate.
  `deadlines.go` records the recommendation for V6: gate `BenchmarkIngestAcceptLeased` instead,
  where the durability path is measured without the spawn floor on top of it.
- **What actually holds the ordering is structural and clock-free.** T9
  (`TestDeliveryJournal_BatchCommitsOneWriteOneSyncOneSeal`: one Write, one Sync and one seal per
  batch, dense arrivals in queue order, every return after the seal), T10 (D14-4) and T14
  (`TestDeliveryJournal_CheckRunsAfterEvaluationAndImmediatelyBeforeAppend`: evaluate, then one
  `checkFile`, then append, with every corruption mode of both formats detected before the next
  Write) contain no wall clock and judge in every lane. The cost side is the coarse B-B backstop
  alone, and under `QOMPACK_UNDER_COLOAD` that row is reported rather than gated: see ADR 0010
  Addendum 1 for that ruling, its sequencing and what the whole-tree lanes lose by it.
- **One failed commit now fails up to 512 callers instead of one.** Each NAKs, its client spools a
  copy with the same nonce, and it becomes a counted unleased gap that the nonce dedupe collapses
  on redelivery. The handle is poisoned either way, so this is a wider availability blast radius
  and not a safety difference.
- **Two things stay open, and are carried rather than closed here.** **SP20-D6**: B-A's gate cannot
  see the ACK wait the hook pays. `plans/00-ARCHITECTURE.md` defines B-A as the client's `main()`
  entry to exit *including* the ACK, but the gated sample is `recvTS − req.TS` plus a 1 ms tail
  allowance whose comment assumes the daemon has stopped timing; since the ACK is written only
  after the durable `Accept`, the tail that allowance stands in for is now the whole B-B region.
  Each way out changes a frozen contract, so it is carried to V6-VERIFY with its options — the
  design's Q1 and Q2 — recorded in `plans/V5-report.md` §31.5. And **O2**, a write-through seal
  handle (Q8), which the design approved for measurement only and which was never measured: there
  is no `FILE_FLAG_WRITE_THROUGH` or `O_DSYNC` anywhere and no bench variant, so it stays a
  measurement item.

## References

- `plans/sdd/V4-SP-20-capture-storage-and-state-remediation/sp20d1-design-final.md` (committed at
  `2a11461`) §1.3, §1.4, §2.3, §2.5, §2.6, §2.9, §2.11, §2.12, §4.3-§4.5, §5, and §8's Q6-Q12.
- `plans/V5-report.md` §31.2 (the owner rulings), §31.3 and §31.3.1 (what shipped, the re-budget
  and part 3b), §31.3.3 (the twelve questions dispositioned), §31.5 (SP20-D6, O2), §31.6 (rollback).
- `docs/adr/0010-wall-clock-under-coload.md` Addendum 1 (B-B under co-load, `d03080d`, `9638b30`);
  `docs/adr/0013-migration-contracts.md`, whose delivery-assignment consequence paragraph stays
  literally true per batch, and whose D13-6 gates D14-6 declines to use.
- Code: `internal/daemon/groupcommit.go`, `delivery_seal.go`, `delivery_seal_tool.go`,
  `delivery_lease.go`, `lock.go` (`ownedByFile`, `SealDowngradeResidual`), `ingest.go` (`Accept`'s
  three durability points); `internal/cli/admin.go`; `internal/config/deadlines.go`;
  `internal/paths/shared.go` (`OpenSharedRW`, `SyncData`); `internal/store/backup.go`
  (`ErrBackupMoved`).
- Tests: `delivery_lease_groupcommit_test.go` (T9, T10, T14),
  `delivery_lease_accessor_test.go` (T20), `delivery_seal_test.go`
  (`TestDeliverySeal_StrictSelectionTable`,
  `TestDeliverySeal_V2IsOneJSONDocumentAndTheOldReaderRefusesIt`,
  `TestDeliverySeal_WriteFormatIsDeliberate`, `TestDeliverySeal_AFailedWriteLatchesTheSeal`),
  `delivery_seal_format2_test.go` (`TestDeliverySeal_ConversionAndDowngrade`,
  `TestDeliveryJournal_RollbackDrillAcrossFormats`,
  `TestDeliverySeal_AFailedDowngradeIsRecordedAndStillReleases`),
  `delivery_seal_tool_test.go`
  (`TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock`),
  `internal/daemon/bench_test.go` and `delivery_bench_test.go` (`BenchmarkIngestAcceptLeased`,
  `BenchmarkIngestAcceptLeasedParallel`).
- Commits: `f6a8691`, `3d95582`, `eb4a76d`, `5d32859`, `1696bd4`, `d1e2477`, `c2109d0`, `55b6f68`,
  `af34615`, `5d0b904`, `cbfa3d3`, `9638b30`, `d03080d`, `e897c9e`, `2a11461`.

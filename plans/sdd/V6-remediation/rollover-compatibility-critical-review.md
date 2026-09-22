# V6 remediation — rollover old-reader barrier + offline reader: independent critical review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high, no nested agents, no git
mutations, no source edits, no builds/tests/benchmarks. Route verified before work:
`plans/sdd/V6-remediation/routing/capacity-review-resume-final-result.json` names
`"canonicalModel":"claude-opus-4-8"`, `"provider":"firstParty"`, session `788ed24f`.
**Date:** 2026-09-22. **Worktree:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v6`.
**Sole output:** this file. NOT a V6 / capacity / rollover-readiness sign-off. The
`enableDeliveryGenerations` seam is default-OFF. GC files still under active authorship
(`gc_delivery_segments*.go`) were not touched. Shared durable-data decisions are the coordinator's;
this returns findings + a smallest concrete protocol recommendation only. All prior artifacts preserved.

**Read (current source):** `delivery_lease.go` (open/migration/seal/load), `delivery_segment.go`,
`delivery_generation.go`, `delivery_seal.go` (format/version constants), `delivery_path.go`,
`delivery_segment_readonly.go`, `delivery_seal_tool.go`. Exact line refs below are against the current
working tree.

---

## 1. Core determination — can an OLD binary open segment 0 after new-format writes?

### 1.1 What migration leaves on disk, and what an old binary parses

Segment 0 is the legacy four files under `state/` (`delivery-leases.jsonl`,
`delivery-acks.jsonl`, `delivery-position.json`, `delivery-ack-position.json`). When the seam is
enabled and a rotation occurs, writes move to `state/delivery-segments/<seq>/…` and **segment 0's four
files are left byte-for-byte in place as immutable evidence** (`switchToSegment` repoints `j.path` and
resets in-memory state; it never rewrites segment 0 — `delivery_lease.go:1461-1505`; `doRotate`
archives + commits the transition but touches only the new segment — `:1409-1437`).

The SP20-D4 migration evidence is entirely in **new files an old binary never opens**: the segment
authority (`delivery-segment-log.jsonl` + `-head.json`) and the generation store
(`delivery-generations/`). The producer's own barrier against a stale legacy reopen is a check on those
new files, made only by a **current** binary:

- `delivery_lease.go:227` — `if !enableDeliveryGenerations && existingDeliveryMigration(stateDir) {
  return nil, deliveryJournalError() }`, where `existingDeliveryMigration` (`:418-428`) tests the
  generations dir, the segments dir, and the segment head/log.
- `delivery_lease.go:253-255` — seam ON, authority absent but `migrationEvidenceExists` (`:409-416`,
  the two dirs) → refuse (head loss).

A binary that predates SP20-D4 has **neither** check. It resolves `state/delivery-leases.jsonl`
directly and, to open it, must parse exactly one derived-state file: `delivery-position.json`.

### 1.2 The only parsed barrier in segment 0's own files is the v2 seal — and it is partial

The single gate an old binary trips on inside segment 0's files is the position seal's version, in
`loadDeliveryPosition` (`delivery_lease.go:1166`: `position.Version != core.EvidenceVersion → refuse`)
and the v2 image layout. A v2 seal is a 32 KiB image whose JSON `"v"` is `deliverySealVersion = 2`
(`delivery_seal.go:46-48`), and a binary whose `core.EvidenceVersion` is 1 refuses it rather than
misreading it (confirmed by the constant's own comment and `delivery_seal_test.go:236`).

That yields a two-class result:

- **Pre-v2-seal binary** (`EvidenceVersion==1`, no dual reader): refused **iff segment 0's seal is
  currently v2**. But the seal format is NOT tied to migration — it is `deliverySealWriteFormat`/
  `sealFormat`, and **a clean Release downgrades the seal to v1** (`openSeal:912-915`; `delivery_seal.go:
  84-86`; `closeSeals` leaves v1). After a clean daemon stop — the most likely moment an operator runs
  an older binary — segment 0's seal is v1, and `loadDeliveryPosition` accepts it. **No barrier.**
- **v2-capable but SP20-D4-unaware binary** (has the dual reader, lacks the `:227` check): opens
  segment 0's v1 **or** v2 seal, loads the frozen segment-0 window, and **appends**. Its arrivals are
  numbered from segment 0's last per-session sequence, re-minting arrival numbers that later segments
  already advanced past their base root, invisibly to the segmented store. **No barrier.**

### 1.3 Verdict

**Segment 0 remains a fully openable, appendable legacy journal after new-format writes.** The migration
barrier lives only in new files that predating readers ignore, and the v2-seal accident stops only
pre-v2 binaries and only while the seal is left v2 (not after a clean Release). There is **no parsed
marker in segment 0's own journal or seal** that says "I am the frozen segment 0 of a migrated store."
The producer's guard is correct for a *current* binary (seam-off), but it is not an old-reader barrier.

This is not reachable in production **today** (seam default-OFF, so no migration and no frozen segment
0 exists), but it is a **pre-enablement blocker for the compatibility story**: the moment the seam is
enabled and one rotation lands, an older or seam-unaware binary can corrupt the arrival space by writing
into segment 0.

---

## 2. Smallest concrete protocol recommendation (coordinator owns the durable-data decision)

Use the file every reader is already forced to parse — the position seal — as the barrier, and stamp it
so that predating readers refuse it **through code paths they already have**, without touching the
journal (the history/evidence):

**When the segment authority names a migrated store (i.e. the active-0 transition is committed), write
segment 0's `delivery-position.json` and `delivery-ack-position.json` with a seal whose parsed
version/format an unmigrated reader rejects** — the natural lever is the existing hard gate
`position.Version != core.EvidenceVersion` (`delivery_lease.go:1166`, `delivery_segment_readonly.go:568`):
a bumped/tagged sealed version is refused by every binary compiled against the older `EvidenceVersion`,
and accepted by every current/future one. Only derived state changes; the `.jsonl` journal bytes are
untouched, satisfying "refuse without deleting history." This reuses the v1↔v2 conversion machinery
(`openSealHandle`, `paths.WriteAtomic`) that already rewrites seals independently of journals.

Two properties are essential and are exactly where the current v2 mechanism falls short, so they must be
called out for the coordinator:

1. **Format-stable across a clean Release while migrated.** Unlike the v2→v1 downgrade at Release, the
   segmented marker must NOT be downgraded while the store is still migrated — otherwise the barrier
   evaporates precisely when the daemon is stopped, which is when an old binary is most likely to run.
2. **`core.EvidenceVersion` is frozen wire/evidence state (Rule W-2).** Bumping or tagging it is a
   shared durable-data decision. I do not propose the exact encoding; I identify the seal as the
   necessary-and-sufficient leverage point and the version gate as the existing parsed barrier, and
   leave the format choice to the coordinator.

(A weaker, non-durable alternative — teach only current binaries to refuse — does nothing for the
binaries that are the actual threat, so it is not a substitute.)

---

## 3. Crash ordering, rollback roots, backup, default-off — findings

- **Crash ordering of the migration point is sound.** The active-0 authority is committed AFTER the
  legacy lease+ack files exist and BEFORE the generation store — the first *dir* evidence — is created
  (`delivery_lease.go:352-380`, `initActiveZero` at `:356` then gen store at `:363-374`). A crash before
  active-0 leaves an unmigrated tree that reopens as legacy; a crash after it is caught seam-off by
  `existingDeliveryMigration` (which includes the segment head/log). No silent legacy reopen with
  re-minted arrivals. See §5.1 for one narrow availability edge in this same guard.
- **Pre/post-write rollback roots are append-only, and the tool refuses to fake an in-place rollback.**
  `doRotate` archives the outgoing window into the generation store (`reconcileGenerations`, a superset)
  BEFORE `commitTransition`, so a committed transition's `base_root` is a superset of that segment's
  leases/acks (`delivery_lease.go:1409-1437`). Every `(active, base_root)` pair is preserved in the
  immutable transition chain; the producer only appends. There is no supported in-place rollback that
  deletes later segments, and `runSegmented` correctly REFUSES `--to v1` for a segmented store
  (`delivery_seal_tool.go:264-270`) precisely because a seal conversion does not make an old writer able
  to read the segmented authority/history — the honest posture. Segmented rollback = restore from a
  verified backup with a compatible reader. This is consistent; I make no rollback-readiness claim.
- **Consistent backup is coherent with rollover** (not re-reviewed here; covered in
  `budget-backup-independent-review.md`): each rotation changes the watched authority, and the segment
  mutable files + authority head/log + generation head/manifest are in the watch set, pages immutable.
- **Default-off seam is intact.** The entire segmented path is gated on `enableDeliveryGenerations`
  (`delivery_lease.go:239,298,363`); the seam-off guard at `:227` refuses a downgrade over migrated
  evidence. Nothing in §1 is reachable until the seam is turned on.

---

## 4. Old-reader barrier — supported vs unsupported scope

- **Supported (verified in source):** the seam-off current-binary guard (`:227`); head-loss refusal for
  migrated-evidence-without-authority; append-only rollback roots; the offline tool's refusal to
  pretend a seal conversion rolls a segmented store back.
- **Unsupported / gap (this review's headline):** there is no durable parsed barrier that stops a
  predating or seam-unaware binary from opening and appending to segment 0 after enablement+rotation.
  The v2 seal is partial (pre-v2 only, and not after a clean Release). §2 is the smallest fix; the
  format is the coordinator's call. This must be closed BEFORE the seam is enabled by default.

---

## 5. Offline reader corrections (`delivery_segment_readonly.go` / `delivery_seal_tool.go`)

Reviewed for error-swallow, bounds, and identity-contract regressions. The reader is, on the whole,
correct: it opens only existing files and writes nothing; every directory is reached through pinned
`os.Root` handles with `SameFile` re-verification; the manifest/authority logs are read as bounded
streams (`io.LimitReader` at `deliverySegmentMaxLog+1`/`genMaxLog+1`, bufio sized to the per-line cap,
`readBoundedLine` refuses `ErrBufferFull` — `delivery_segment_readonly.go:126,242,351,439,585-601`);
identity joins are exact (canonical re-marshal equality + `validDeliveryLease` + delivery match in
`resolveLease:309-326`; ObservationID join in `checkAckJournal:470-476`; `arrivalAt` refuses a non-8-byte
value at `:301`); a missing/corrupt predecessor page is propagated as an ERROR, never a silent "new
session at 1" (`checkLeaseJournal:378-385` with `berr` refusing at `:381`); torn/short/oversize evidence
refuses. I did **not** manufacture findings; the one concrete, defensible item follows.

### 5.1 [finding, low severity — fail-closed availability edge] a zero-length segment log with no committed head bricks the seam-off open

Not strictly in the two offline-reader files, but in the shared guard they and the producer rely on.
`openDeliverySegments` creates an EMPTY `delivery-segment-log.jsonl` via `openDeliveryAppend`
(`O_CREATE|O_EXCL`, `delivery_segment.go:154`, `delivery_path.go:86-96`) on **every** seam-on open of a
fresh tree, BEFORE `initActiveZero` commits the first transition (`delivery_lease.go:246` precedes
`:356`). If a crash lands in that window, the tree carries a **0-byte segment log with no head and no
committed transition** — no real migration, no history.

`recover` itself treats this correctly as not-yet-migrated (`!hasHead && size==0 → return false,nil`,
`delivery_segment.go:199-204`; the read-only `readonlySegmentAuthority:91-100` mirrors it). But
`existingDeliveryMigration` (`delivery_lease.go:422-427`) tests the mere **existence** of the segment
log file, so a subsequent open with the seam turned OFF hits `:227` and is **refused for good** —
because of an empty, meaningless file that represents zero committed migration. It is fail-closed (no
corruption, nothing deleted), but it is an availability regression: an empty segment log with no head
blocks the legacy path until the file is manually removed.

**Smallest fix (for the coordinator, since `:227` is a deliberate shared-contract strictness):** have
`existingDeliveryMigration` treat a **zero-length** segment log with **no head** as not-yet-migrated,
matching `recover`'s own `size>0` gate — i.e. gate on committed evidence, not on file existence alone.
If the coordinator prefers strict fail-closed here, that is a defensible choice; I flag it as concrete
and reachable (seam on → crash in first-open window → seam off) rather than prescribe.

### 5.2 Notes (not defects)

- The read-only radix builds `deliveryRadix{dir: genPagesDir, …}` with a RELATIVE `dir`
  (`delivery_segment_readonly.go:213`) vs the producer's absolute (`delivery_generation.go:200`). Harmless:
  `dir` feeds only `pagePath`, a test-only fault-injection helper; all real reads go through the pinned
  `root` (`readPage` uses `r.root`, verifying `SameFile`+size+content-hash). No identity impact.
- The segmented `--check` refuses a torn trailing partial in a segment journal (`:363,451`) rather than
  recovering the complete prefix as the daemon's `loadFrom` would. This is a deliberate, stricter
  integrity read appropriate to an offline tool that writes nothing (documented at `:39-43`), not a
  regression.
- `runSegmented`'s indexing of `auth.transitions[len-1]` (`:298,318`) is safe: `migrated==true` implies
  `scanAuthorityChain` validated ≥1 transition (`readSegHead` refuses `LogBytes<=0`, `:360`).

---

## 6. Non-acceptance / scope

Read-only source review; no V6, capacity, rollover, or rollback-readiness sign-off, and no target-
readiness claim. **Headline:** the old-reader barrier is incomplete — segment 0 stays openable and
appendable by a predating or seam-unaware (but v2-capable) binary after enablement+rotation, with no
durable parsed marker in segment 0's own files; §2 gives the smallest existing-lever fix (a
version/format-tagged position seal, format-stable while migrated), whose durable encoding is the
coordinator's shared-data decision. Crash ordering, append-only rollback roots, the tool's refusal to
fake segmented rollback, and the default-off seam are sound. The offline reader is sound on bounds,
identity, and error propagation; one concrete fail-closed availability edge (§5.1) is flagged for the
coordinator. GC files under active authorship were not reviewed. Nothing was built, run, edited, or
deleted; every prior artifact is preserved.

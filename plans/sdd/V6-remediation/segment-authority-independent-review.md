# V6 remediation — segmented journal / GC retention / backup shared contract: independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** read-only review of code under ACTIVE authorship — no source/test edits, no
tests/builds/benchmarks, no Git/config/permission changes. Only output: this file. Not a sign-off;
main inspects the final route JSON. I do not certify evolving code as final. The seam is default-off;
the ≥70-segment runtime proof and the restart/replay/dormant proofs are still OWED. Findings are
tagged **[source]** (actual current code), **[proposal]** (a requirement on the unshipped design), or
**[untested]** (a condition no run I can see covers). I do not re-file already-adjudicated points.

**Read:** `delivery-segment-retention-decision.md` (incl. main's "Review of the author's first
authority proposal"), `delivery-capacity-integration-work.md` (round-4 proposed wire + crash
ordering), `internal/daemon/delivery_generation.go`/`radix.go` context, `internal/daemon/delivery_migration_guard.go`,
`internal/store/backup.go` + `backup_segments.go`, `internal/store/gcrun.go` (retention reader).

---

## 1. Verdict

The backup multi-file watch is well-built and (subject to §4) adequate for the segment model. The two
shared-contract pieces main owns are NOT yet in place, and one of them contradicts main's own
adjudication:

- **A1 [proposal]:** the round-4 authority wire still encodes "Absence = legacy" and a bare mutable
  `chain`, the two things main's decision §1/§2 explicitly reject. Not yet compliant.
- **A2 [source]:** the GC lease-release join is NONCE-only today — exactly what the decision forbids
  ("never release … on a nonce-only or unproved join").
- **A3 [source]:** GC reads only the legacy `delivery-leases.jsonl`; it does not enumerate segments.
- **A4/A5 [proposal]:** backup-watch completeness is contingent on the final authority being a single
  atomic commit point; main's correction #2 (immutable predecessor record) changes that.

§6 gives the concrete GC-reader minimum (fields/anchors/read-checks, bounded, fail-closed).

---

## 2. A1 — proposed authority wire is not yet compliant with main's own corrections [proposal]

`delivery-capacity-integration-work.md` round-4 proposes
`deliverySegmentAuthority{v, active, prev, base_root, chain}` with **"Absence = legacy … the only
'absence is not unavailable' case"** (work doc §28-30) and **`chain = H(prevChain ‖ …)`** written into
a single `WriteAtomic(delivery-journal.json)` (§24, §42). Main's decision §55-72 already rules BOTH
insufficient:
- §1: absence cannot unconditionally mean legacy once segment/generation evidence survives — commit an
  **active-0 authority before staging the first segment**, so surviving migration evidence with a
  missing authority **refuses** (unavailable), never reopens the frozen legacy segment and re-mints
  arrivals.
- §2: a `prevChain`-based chain cannot be validated from **one overwritten head**; keep an **immutable
  authority/transition record per segment** (or an authenticated versioned log) before flipping the
  active pointer, and define how the reader validates predecessor/root binding without trusting a bare
  self-reported chain.

The proposed wire has not adopted either. This is the top shared-contract hole. It is **[proposal]**,
not a source bug — no `delivery_segment*.go` exists yet (git shows `delivery_generation.go` and the
store `backup_segments.go` helper, but the daemon does not yet write `delivery-segments/`; the path
appears only in `delivery_migration_guard.go` and the backup helper). The GC reader and the backup
watch both depend on this wire being settled first (§4, §6), so it gates them.

## 3. A2 / A3 — GC retention reader is legacy-nonce-only and does not read segments [source]

`gcrun.go:605-618` reads exactly `state/delivery-leases.jsonl` as `RetentionLease` and releases a
lease via `openLeaseLines(s.acknowledgedDeliveries())`. Two confirmed facts:

- **A3:** it never enumerates `state/delivery-segments/` — the budget-freeing blocker the author
  correctly cited. A settled lease archived into a segment is invisible to GC; a rotation that
  compacted settled leases out of the active file without this reader would strip retention from still-
  open archived leases and risk object loss. Correctly gated (seam off).
- **A2:** the release predicate keys on **Delivery (nonce) only** — `acknowledgedDeliveries()` builds
  `acked[rec.Delivery]` (`:715-736`) and `openLeaseLines` releases when `acked[rec.Delivery]` matches
  (`:757-758`), **ignoring ObservationID**. The decision §34/§37 requires an **exact ACK identity
  join** and "may never release a lease on a nonce-only or unproved join." So the current join is
  precisely what the segmented contract forbids; it must become an exact `(Delivery, ObservationID)`
  match. (For legacy this was safe under the append-only single journal; it is not safe once a nonce
  can recur across segments/sessions.)

Both are `gcrun.go`, which main owns; the store GC reader cannot import daemon (§3.2, `gcrun.go:595-599`),
so it must parse these formats **structurally** — see §6.

## 4. A4 / A5 — backup watch is sound for a single-authority-commit, contingent on A1 [proposal]

`backupLiveWriterFiles` (`backup.go:121-129`) watches the legacy four, the two generation-store files,
AND `state/delivery-journal.json` (the authority). `refuseIfTheProjectMoved` (`:370-397`) additionally
folds in every **captured** segment mutable file via `backupSegmentMutableFile` (`backup_segments.go`).
Assessment:

- **WalkDir-order question — answered:** yes, comparing each captured file to its post-walk re-read
  guarantees consistency **regardless of `filepath.WalkDir` order**, because any watched file that
  changed between its own copy-time and the post-walk `backupFileMatches` re-read (size + SHA-256 via
  `paths.OpenShared`, with `SameFile` before/after) refuses the backup. WalkDir order is irrelevant so
  long as (i) every file that can change is in the watch set and (ii) the authority is the SINGLE
  atomic commit point, so one watched file's change witnesses the whole rotation. "Atomic files ≠
  atomic backup" is correctly handled — this is exactly what `refuseIfTheProjectMoved` exists for.
- **A4 [proposal]:** completeness rests on the authority being the sole atomic commit point. Main's
  correction #2 introduces an **immutable predecessor/transition record written BEFORE the authority
  flip** — a multi-step commit. If that record is a NEW dynamic path (per-segment), it is neither in
  the fixed watch list nor necessarily `captured` (it can appear after the walk passed), so a mid-
  backup capture between the predecessor write and the authority flip could go undetected. The watch
  set must therefore include whatever immutable transition record the final wire adds, or the backup
  guarantee has a hole precisely at the crash window main's correction #2 is meant to make recoverable.
- **A5 [proposal]:** a dynamically-created segment (staging step 3) that the walk did not reach is not
  watched; today this is benign because the authority (watched) is unchanged until step 4, so the
  restored tree is a valid mid-staging state the reader's "adopt-if-fresh-empty-else-refuse" recovery
  handles — but that safety is again the single-atomic-authority-commit assumption (A4).

So the backup watch is **adequate as written for the round-4 single-commit model**, and becomes
**incomplete if A1's fix adds an immutable predecessor record that is not added to the watch set.**
The `backupedge_test.go` `len==6` pin must move with any watch-set addition.

## 5. Durable barrier / owner-lock ordering (rotation) [proposal, decision-conformant]

The round-4 rotation ordering (work doc §32-48) matches the decision §27-32: drain both pipelines to
zero WITHOUT holding the owner lock across the wait, reconcile the outgoing segment into the radix
(durable) BEFORE the authority flip, stage the new segment (adopt only an exactly-fresh-empty dir,
else refuse), then the single atomic `WriteAtomic(delivery-journal.json)` commit, then repoint. This is
sound in shape. The two open dependencies are A1 (the commit's durability rests on the authority
record's design) and the [untested] ≥70-segment/restart/replay-oldest/dormant-continuity runtime proof
the decision §45-47 requires before enablement. I cannot verify the drain-vs-ownership interleavings
without the daemon segment writer, which does not exist yet.

## 6. Deliverable — minimum contract for a store-side GC retention reader (cannot import daemon)

The reader parses daemon-owned formats **structurally** (as `gcrun.go` already does for the legacy
lease/ack files). Minimum fields / anchors / read-checks, all bounded and fail-closed:

**Anchor & mode (mirrors decision §1):**
1. Read `state/delivery-journal.json`.
   - Missing AND `state/delivery-segments/` absent AND no generation evidence → **legacy-only** (parse
     the legacy four as today). This is the ONLY absence-is-not-unavailable case.
   - Missing BUT segment/generation evidence present → **prevent collection** (unknown/lost authority).
   - Present but unknown `v` / non-canonical bytes / chain that does not validate against the immutable
     predecessor record / `active` whose segment or files are missing → **prevent collection**.

**Enumerate (never let an unreadable glob become an empty set — decision §39):**
2. `os.ReadDir(state/delivery-segments/)`; a read error → **prevent collection**. Accept only canonical
   `%020d` sequence dirs `1..active`; a referenced sequence dir or any of its four files missing →
   **prevent collection**. A staged-but-uncommitted dir (> active, or not fresh-empty) is conservatively
   **retained**, never authorises identity.

**Fields the reader must read, per record (structural):**
3. Lease line: `delivery`, `observation_id` (both required, as `acknowledgedDeliveries` already
   requires for acks). Ack line: `delivery`, `observation_id`. Terminal disposition: `delivery`,
   `observation_id`, outcome. Any torn/oversize/unparseable **lease** line → over-retain (do not drop
   the lease) and mark the pass incomplete; a torn **ack** line is safely "not acked yet."

**Join (exact identity — fixes A2):**
4. Build the settled set keyed by the pair `(delivery, observation_id)` over legacy + every segment's
   `delivery-acks.jsonl` (+ terminal dispositions, treated as settled-for-ordering but conservatively
   retained for objects unless proven). A lease is releasable ONLY if its exact `(delivery,
   observation_id)` is settled; **never on nonce alone.**

**Bounds (decision §37):**
5. The settled map is bounded; if it would exceed budget, **over-retain** (leave unconfirmed leases
   held) and identify the over-retention — never truncate the settled set and then release. Any
   enumeration/read/budget-exhaustion condition **prevents collection of the affected roots**, never
   yields a smaller-than-true retention set.

**Output:** the union of retention roots from every UNSETTLED lease across legacy + all authorized
segments (the hashes each open lease line references, as the legacy path already extracts). This keeps
an archived-but-open lease's capture reachable exactly as the legacy open-lease rule does today.

This needs no daemon import: it is file enumeration + structural JSON parse + the exact pair join, with
the authority file as the single anchor that decides legacy-vs-segmented-vs-unavailable.

## 7. Non-acceptance

Read-only review of an actively-authored shared contract; no sign-off, no certification of evolving
code. The backup watch and rotation ordering are sound in shape; A1 (authority wire not yet matching
main's §1/§2 corrections) is the gating hole, A2/A3 (GC nonce-only join, no segment enumeration) are
confirmed current-source gaps main must close in `gcrun.go`, and A4/A5 (backup completeness) are
contingent on A1's final record set. §6 is the concrete GC-reader minimum. The ≥70-segment /
restart / replay-oldest / dormant-continuity runtime proofs remain owed and the seam stays default-off;
I ran nothing and the store-drift/observer runs main cited are main's, not mine. High-stakes durable
decisions (the authority record shape, the exact-join retention semantics, the watch-set additions)
return to main.

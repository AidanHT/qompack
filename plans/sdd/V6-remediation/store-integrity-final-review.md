# V6 remediation — store observation-intent integrity: final independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** read-only review of the CURRENT source — no source/test edits, no tests/builds (I ran
nothing), no Git/config/permission changes. Only output: this file. NOT a release sign-off; main
inspects the final route JSON. I judged the source independently; I read
`observation-hardening-resolution.md` but did not take its claims as proof. Evidence below cites
current lines. Runs I reference (Linux race, full-store 250 s, the two failing observer fixtures) are
MAIN's runs, not mine, and are qualified as such. Journal capacity is another author's scope and is
excluded. The sidecar has a finite lifetime cap; no unlimited-capacity claim is made.

**Read (current):** `internal/store/observation_publication.go`, `observation_lookup.go`,
`observation_guards.go`, `observation_audit.go`, `fsstore.go` (Close/obsPubMu),
`tooluseindex.go` (MarkSuperseded barrier), `maintenance.go` (`proveReader`), `readonly.go`
(`AuditPublication`), and the observer wiring `observer/tooluse.go` + `identity.go`.

---

## 1. Verdict

H2–H5 are genuinely fixed in source (verified line-by-line, not from the resolution doc). H1 is
reshaped soundly AND the observer's Recover-before-publish wiring neutralises the drift regression I
raised — provided one wiring invariant holds (§3). The remaining honest gap is **evidence**: the
end-to-end observer↔store contract is exercised by the two observer fixtures that currently FAIL
(mock stores lacking the recovery capability); main's mock→real-store repair is PENDING and is not
accepted proof. No new store-side defect was found; two minor residuals (§5) and the finite cap
(§4) are noted.

---

## 2. H1–H5, independently verified against current source

- **H2 (LF required) — FIXED.** The custom `bufio.Scanner` split
  (`observation_publication.go:214-223`) returns an error for any data without a trailing `\n` at EOF
  and marks the tail's observation unavailable via `probeObservation`, so a complete-JSON-no-LF tail
  becomes `obsSidecarUncertain` (`:248-250`), never silent authority. While uncertain, new reservations
  are refused (`:471-473`), so no append can join onto a torn tail. Closes my prior H2.
- **H3 (non-regular / aliased index) — FIXED.** `loadObservationsLocked` now goes through
  `openObservationIndex` (`observation_guards.go:19-42`: pins `index` via the `.qompack` root with
  `Lstat`+`SameFile`, rejecting an aliased/symlinked index dir) and Lstat-checks `IsRegular` BEFORE
  `OpenFile` (`:190-197`), then re-verifies `SameFile` after open (`:206-210`). A FIFO/dir/symlink at
  `observations.jsonl` is now uncertain rather than a blocking `O_RDONLY`. Closes my prior H3 (the read
  path now has the write path's guard). The resolution's correction that `os.Root` permits in-root
  links is accurate; the `Lstat`+`IsRegular` guard is the real defense.
- **H4 (Close barrier) — FIXED, verified.** `Close` takes `s.obsPubMu.Lock()` around the whole
  `closeBody` (`fsstore.go:420-423`), which sets `closed` (`:458`); publishers/recover recheck lifecycle
  AFTER acquiring `obsPubMu` (`observation_publication.go:607-609,627-629,734-736`), and
  `MarkSuperseded` takes the same barrier (`tooluseindex.go:537-538`). An in-flight publisher is
  drained (Close blocks on the barrier); a queued one wakes to `closed`→`ErrDegraded` and writes
  nothing; a later mark cannot slip between a target snapshot and completion. Closes my prior H4.
- **H5 (Recover re-syncs closure) — FIXED.** `RecoverToolUseByObservation` no longer short-circuits a
  committed binding; `case b != nil` always runs `completeIntentLocked` (`:755-758`), which
  `SyncPublication`s the record's object closure (`:654,695`) before returning. A load-committed
  binding whose object was since lost now fails `ErrDegraded` at Recover rather than returning a record
  with unretrievable bytes. Idempotent for an intact committed binding (targets already superseded →
  no re-write). Closes my prior H5.
- **H1 (exact-replay idempotence) — reshaped soundly (§3).** `buildIntentLocked` reuses the STORED
  ORIGINAL `sup` for a known obs (`:413-418`) instead of recomputing it from live state, and targets
  now carry a full metadata digest (`observationTarget.Identity`, `observationRecordDigest` ignoring
  only mutable `Status`/`SupersededBy`, `observation_guards.go:13-17`). Idempotence/conflict is a full
  canonical-byte comparison including the caller's `asked` list (`:459-463`). The resolution's stance —
  a changed REQUESTED set is a conflict, the original SELECTION is replayed — is implemented.

---

## 3. The one substantive item: H1's exact-`asked` strictness depends on a wiring invariant (evidence-qualified)

`reserveConflictLocked` refuses an existing binding whose canonical bytes differ, INCLUDING the
caller's `asked` list (`observation_publication.go:459-461`), and does not distinguish committed from
pending. Taken alone, a redelivery that recomputed a different supersede-candidate list (`older` from
`supersessionCandidates`, `tooluse.go:107,192`, is lookback-window- and index-state-dependent, so it
CAN drift as the index advances) would hit `ErrAppendOnly` — where the pre-observation record dedup
returned a silent idempotent no-op. **However, this is not reachable through the wired observer path:**
`onUserPrompt`/`onToolUse` call `observationRecord` → `RecoverToolUseByObservation` BEFORE
`publishRecord` (`tooluse.go:114`, `identity.go:74`), and Recover answers from the STORED ORIGINAL
intent (no `asked` recompute). Because Recover returns a non-`ErrNotFound` result whenever
`obsBindings[obs]` exists (record, or `ErrDegraded`), the observer either short-circuits (recognised,
`tooluse.go:116-129`) or returns unpublished (`identity.go:81-83`) — it never falls through to
`publishObservation` against an already-committed binding with a drifted `asked`. So the drift
regression I raised does NOT bite the wired path.

**Two evidence-qualified caveats for main:**
1. This correctness rests on the invariant *the observer always attempts Recover before publish, and a
   committed obs never re-enters `publishObservation`.* It holds in the current `tooluse.go`/`prompt`
   /`stop` paths; preserve it (a future caller that publishes without a prior Recover would expose the
   exact-`asked` conflict on a benign redelivery).
2. The end-to-end observer↔store contract is exactly what the two observer fixtures at
   `tooluse_test.go:630+` exercise, and they currently FAIL (mock store lacks
   `ObservationRecovery`/`ObservationReader`; capture-session mismatch). Main's repair to real stores
   is PENDING. I do not treat the failing runs as a store defect, nor the eventual pass as proof: the
   wired contract is **unproven** until a real-store redelivery-after-index-drift case passes. **Exact
   test needed:** with a REAL store, publish an observation with one live target; advance the index so a
   redelivery would recompute a different `older`; then redeliver with the capture-sidecar link missing,
   and assert recovery is idempotent (recognised via Recover / no `ErrAppendOnly`).

---

## 4. Remaining focus areas — confirmed

- **Durable target publication.** `completeIntentLocked` marks only still-live targets and treats a
  vanished/identity-changed target as `lost` → unavailable, never forging completion
  (`:662-679`, `observationStateLocked:363-382`, with the full identity digest). Later supersession is
  preserved (a target superseded by another record counts as done, `:375-377`).
- **Uncertain / fsck / restore proof.** `obsSidecarUncertain` is process-lifetime, no runtime clear,
  no auto-truncation (comment `:29-33`; set at every torn/oversize/IO path). `auditObservationBindings`
  flags uncertain/ambiguous/unavailable and every non-committed binding even with an empty
  capture/object scan (`observation_audit.go`); `proveReader` refuses a restore when it reports
  incomplete (`maintenance.go:428-432`); `readonly.go` delegates `AuditPublication`. Torn/conflicting
  evidence therefore blocks a restore-reader-proof and needs a verified backup, matching the fail-closed
  posture.
- **Bounded resource use (FINITE, not unlimited).** Load: `LimitReader(64MiB+1)` + entry cap + a real
  `break` (`:212,230-233`). Append: `ErrBudget` past `maxObservationSidecarBytes`/`Entries`
  (`:485-490`). This is an explicit lifetime refusal at capacity — the sidecar shares the delivery
  journal's "bounded storage: no." No unlimited-publication claim is made or supported here.
- **Confined paths / privacy.** All sidecar I/O goes through `openObservationIndex` (root-pinned,
  regular-file-checked); the preview is redacted at `validateIntentInput`; `sup`/`asked` are opaque
  tool_use IDs; observation ids require the canonical nonzero digest (`validObservationID:129-135`) and
  the embedded legacy-record version is validated (`decodeObservationLine:266`).

---

## 5. Minor residuals (not blockers)

- **Read-open micro-TOCTOU.** Between `root.Lstat` (`:190`) and `root.OpenFile(O_RDONLY)` (`:198`) a
  swap to a FIFO would block the open before the post-open `SameFile` check catches it. A trusted-dir,
  microsecond-window race; noted, not a real concern.
- **Recover double-fsync on a committed binding.** `completeIntentLocked` `SyncPublication`s twice
  (`:654,695`) even when nothing is re-marked; Recover is a mutate-guarded recovery path, so the extra
  I/O is acceptable but worth knowing.

---

## 6. Non-acceptance

Read-only final review of the store observation-intent implementation and its observer/audit/restore
integration; no release sign-off. H2–H5 are fixed and H1 is reshaped soundly, all verified in current
source; the drift regression I previously raised is neutralised by the observer's Recover-first wiring,
subject to the §3 invariant. The one open item is EVIDENCE, not a found defect: the wired
observer↔store redelivery contract is unproven while the two observer fixtures fail and main's
real-store repair is pending — I name the exact test that would settle it and do not accept the pending
change as proof. Durable-target, uncertain/fsck/restore, finite-bound, and confined-path/privacy
properties hold in source. I ran nothing; the Linux race and 250 s store pass are main's runs, and
uncertainty about untested paths, platform gates, and the pending fixtures is preserved.

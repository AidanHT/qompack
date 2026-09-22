# V6 publication/recovery — production gap accounting (V6-RECOVERY-1)

Owner: V6 publication/recovery seat (claude-opus-4-8, high). Worktree `../qompack-v6`, branch
`verify/v6`. **No commit made.** Additive, read-only, four exclusive files only.

Revision 2 (2026-09-21): corrected against main's review — criteria calibrated to `qompack fsck`,
genuinely bounded traversal, honest object candidates, KeepRaw false-positive guard, cancellation.

## 1. Problem

V6-report §5 V6-RECOVERY-1 (release blocker): publication gaps lack automatic accounting. Two
`test/fault` rows fail despite a Go pass — `capture_sidecar_stage_one_only` (F4-4) and
`object_written_index_line_absent` (F4-1) — and only a **manually invoked** fsck detects the stage-one
fixture. `daemon.sweepCheckpointIntegrity` (daemon.go:1057) accounts for the checkpoint defects at
startup but nothing accounts for publication. There is **no production harness audit** —
`test/fault`'s `auditProject` is test-only, and V6-report §8 records the coordinator correcting an
earlier claim that it was a production service.

## 2. Exclusive files

### `internal/store/publication_audit.go` — bounded, read-only discovery

Additive concrete methods on `*FSStore`, reached through the new narrow `PublicationAuditor`
interface (the `RefCounter`/`Quarantiner` pattern). Frozen §5.8 `Store`/`ReadOnlyStore` interfaces and
all wire shapes untouched. Writes/repairs/deletes nothing.

**Criteria calibrated to `qompack fsck`** (internal/cli/fsck.go `checkCaptures`/`checkObjects`), so a
startup announcement and an operator's fsck cannot disagree:

- **Capture gap** = `!Published && Op∈{observe.tool, observe.stop} && Outcome==core.OutcomeOK &&
  BytesHash nonzero` (fsck.go:1111-1118, calibration rule 2). `published:false` alone is NOT a gap.
  - A **prompt** delivery publishes no reference by design → `LegitimatelyUnpublished`.
  - A **denied/absent/error** outcome admitted no bytes → `LegitimatelyUnpublished`.
  - An **ok capture with no durable bytes** → `LegitimatelyUnpublished`.
  - **Version must equal** `CaptureSidecarVersion` exactly: newer = support-gap note, older/zero/missing
    = unreadable-version note. Either way → `Incomplete`, never classified.
  - **Unknown/missing outcome** or **unknown op** → `Incomplete`, never a silent non-gap.
  - `observe.stop` is included under the **same** outcome/bytes criterion as `observe.tool` — per
    main's ruling, only when the actual capture outcome/bytes establish a reference is expected.
- **Object candidate** = an object file no live index chunk references and no pending-write marker
  covers. Named `UnindexedObjectCandidates` and reported **honestly**: it includes crash-orphans
  (F4-1) *and* tombstoned-but-unswept objects, which this pass does not distinguish (that needs a
  tombstone replay). No repair, no delete. **KeepRaw is not a source of these** — verified in source:
  `putSideRecord` (delta record *and* retained original) routes through `appendRoot → indexRootLocked`,
  so their chunks are in `chunkSet` (put.go:521-598). A dedicated CRLF+KeepRaw test locks this.

**Genuinely bounded** (main's #2/#4), pinned by tests:

- A shared `scanBudget` caps **total directory entries visited** (files, subdirs, unknown entries and
  symlinks all charged) and **total bytes read** (sidecars + pending markers). Directories are read in
  fixed 256-entry batches via `os.File.ReadDir`, never slurped whole — a directory bomb hits the count
  cap before it can allocate arbitrary names. Depth is fixed at the two fanout levels; **no recursive
  WalkDir**.
- **No object byte is read or re-hashed** — membership is `chunkSet` map lookup by filename under
  `RLock`.
- A sidecar is read only up to `captureSidecarReadLimit = 2×config.HookCaptureHardCapBytes + 64 KiB`
  (~8 MiB, covering a 4 MiB hook payload base64-expanded), **not** `MaxPutBytes` (64 MiB). A file past
  it is reported incomplete, never read. The byte budget also caps the pass overall, so the decoder's
  bounded allocation for the skipped `bytes` field is capped too.
- **Symlinks/reparse points are never traversed or read** (`os.ModeSymlink|os.ModeIrregular`), so the
  walk cannot be lured outside `.qompack`.
- **ctx checked every batch**; the pass stops on cancellation and returns `context.Canceled` alongside
  the partial report. Main wraps a short startup deadline around the call.
- **Incomplete ≠ empty**: any fs error, unreadable/unknown record, too-large file, symlink, or
  budget/deadline stop sets `Incomplete` + a fixed-vocabulary note; a zero gap count is "clean" only
  when `Incomplete` is false.

### `internal/daemon/publication_audit.go` — narrow report + LOUD helper

- `PublicationGapReport` — narrow, non-sensitive (counts/booleans/fixed notes; no id/path/hash/byte).
  `Observed:false` when the store exposes no `PublicationAuditor` (nil/double).
- `AccountPublicationGaps(ctx)` — folds any store error (ctx-cancel at shutdown, closed store) into
  `Incomplete` **without leaking the error text**.
- `LoudPublicationGaps(ctx)` — the generic LOUD helper main calls: one LOUD line (counts + booleans +
  fixed notes) on a gap or an incomplete pass; a clean/complete pass is quiet (Debug). Counters:
  `daemon.publication.unpublished_captures`, `.unindexed_object_candidates`, `.accounting_incomplete`.

## 3. Integration call — exactly where (main wires it; I did not edit daemon.go)

> In `daemon.Run`, immediately after the existing `d.sweepCheckpointIntegrity(runCtx)` (daemon.go:594):
> `d.LoudPublicationGaps(runCtx)`.

**After the startup Drain** (numbers are real, not transient) · **before serve** · **on Run's
goroutine, never from Stop**. The method only reads, so writer stability does not constrain the
position — it is about the *meaning* of the answer (a post-drain snapshot of the live writer store,
preliminary under a concurrent write, which is why every unknown/error state stays explicit). It is an
**announcement, not a gate**. Main will also store the report into `status` later and wire
`daemon.go`/`handlers.go` itself; the same `OpenReadOnly(...).(store.PublicationAuditor)` surface is
available to `fsck`/`doctor`.

## 4. Tests — argv and results (focused, `GOMAXPROCS=2`, no race/bench)

```
$ GOMAXPROCS=2 go test ./internal/store -run 'TestAuditPublication' -count=1 -v -timeout=120s
--- PASS: TestAuditPublication_ClassifiesCaptureSidecars (0.12s)
--- PASS: TestAuditPublication_DeniedCaptureIsNotAGap (0.04s)
--- PASS: TestAuditPublication_EmptyOkCaptureIsNotAGap (0.04s)
--- PASS: TestAuditPublication_UnknownOutcomeIsIncomplete (0.03s)
--- PASS: TestAuditPublication_UnknownOpIsIncomplete (0.03s)
--- PASS: TestAuditPublication_FutureSchemaIsIncompleteNotEmpty (0.03s)
--- PASS: TestAuditPublication_MissingOrOlderSchemaIsIncomplete (0.03s)
--- PASS: TestAuditPublication_MalformedTrailingJSONIsIncomplete (0.03s)
--- PASS: TestAuditPublication_UnreadableSidecarIsIncomplete (0.02s)
--- PASS: TestAuditPublication_UnindexedObjectCandidate (0.02s)
--- PASS: TestAuditPublication_IndexedObjectsAreNotCandidates (0.04s)
--- PASS: TestAuditPublication_KeepRawPutCreatesNoCandidates (0.06s)
--- PASS: TestAuditPublication_PendingObjectIsNotACandidate (0.03s)
--- PASS: TestAuditPublication_EmptyProjectIsCleanAndComplete (0.02s)
--- PASS: TestAuditPublication_RespectsTheCaptureCap (0.07s)
--- PASS: TestAuditPublication_RespectsTheEntryBudget (0.06s)
--- PASS: TestAuditPublication_ClosedStoreDegrades (0.02s)
--- PASS: TestAuditPublication_HonoursCancellation (0.02s)
ok  	github.com/qompack/qompack/internal/store	0.940s

$ GOMAXPROCS=2 go test ./internal/daemon -run 'TestLoudPublicationGaps|TestAccountPublicationGaps' -count=1 -v -timeout=120s
--- PASS: TestLoudPublicationGaps_AnnouncesAnUnpublishedCapture (0.04s)
--- PASS: TestLoudPublicationGaps_QuietWhenClean (0.02s)
--- PASS: TestAccountPublicationGaps_UnobservedForNonAuditorStore (0.00s)
ok  	github.com/qompack/qompack/internal/daemon	0.269s
```

Coverage added for main's #1/#2/#3: denied capture, empty-ok capture, unknown outcome, unknown op,
future schema, missing/older schema, malformed trailing JSON, unreadable sidecar (all → not-a-gap /
incomplete as appropriate); KeepRaw-creates-no-candidates (false-positive guard, confirmed the CRLF
put records a recovery side record — ≥2 roots — with zero candidates); entry-budget truncation;
context cancellation.

Static checks (no broad lint over sibling files, per main): `gofmt -l` clean; pinned `gofumpt -l`
exit 0, no files; `go vet ./internal/store ./internal/daemon` clean.

## 5. Not fully fixed — remaining for main

This is **surfacing, not full recovery** (main). Delivered: discovery + accounting + a LOUD
announcement + a narrow report. **Not** delivered and explicitly main's separate work:

- **The recovery writer** — durable acknowledgement / drain-side re-publish of a stage-one capture /
  operator-gated `fsck --repair` of an unindexed object. A writer owning the publication contract
  (SP20/SP17), not this read-only seat.
- **Storing the report into `status`** and wiring `daemon.go`/`handlers.go` — main's.
- **Packaged tests / status** and the `test/fault` F4 expectation updates — main updates those explicit
  expectations with the new evidence; I left the pinned fixtures/outcomes untouched.

Do not mark V6-RECOVERY-1 fully fixed until the recovery writer and packaged tests/status are handled.

## 6. Open questions returned to main

1. **Object-candidate split.** `UnindexedObjectCandidates` conflates crash-orphans (F4-1) with
   tombstoned-but-unswept objects. Splitting them durably needs a signal GC already computes (a
   tombstone/collectable view) — a shared contract I did not invent. Do you want the split, and who
   owns that signal?
2. **Cap sourcing.** Defaults: 8192 captures / 16384 objects / 262144 entries / 32 MiB bytes, plus an
   ~8 MiB per-sidecar read limit derived from `config.HookCaptureHardCapBytes`. If a real project's
   steady-state exceeds a cap, a healthy startup reports `Truncated` (incomplete). The caps are an
   explicit seam (`publicationScanCap()`); source them from config if you prefer.
3. **Startup deadline.** You said you'll wrap a short context deadline around the call. The pass
   honours cancellation and returns partial counts + `context.Canceled`; `LoudPublicationGaps` folds
   that into `Incomplete` and still announces. Confirm the deadline budget you intend so the default
   entry/byte caps are not the thing that bites first.
```

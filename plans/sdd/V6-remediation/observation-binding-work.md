# Durable observation binding — work record

Owner: V6 subagent (Opus 4.8 high). Scope per `observation-binding-decision.md` (superseding section).
No commits, no children, no broad/race/perf runs. Focused runs recorded through
`dist/v6-remediation/run.py`.

## Part A — REJECTED private-index proposal (preserved as failed evidence)

The first implementation added an `omitempty observation_id` to the PRIVATE `tuRec` line plus an
in-memory secondary map. The independent review (`observation-binding-critical-review.md`) accepted
its fixture/old-reader compatibility but found the architectural blocker: `00-ARCHITECTURE.md §0.2`
mandates the observation↔record join lives in the dedicated versioned sidecar `index/observations.jsonl`
with the tool_use wire frozen. The coordinator REJECTED the private-index approach.

Actions taken:
- The rejected edits are preserved under `dist/v6-remediation/rejected-binding/`
  (`observation_lookup.go.rejected`, `observation_lookup_test.go.rejected`,
  `private-index-tuRec.patch`, `README.md`) without touching other authors' files.
- The private `tuRec` shape is RESTORED to frozen: the `Obs` field, the `RecordToolUse`/
  `RecordToolUseSuperseding` writes, the `loadToolUse` recovery and the `putToolUseLocked` hook are
  reverted. The golden guard `testdata/golden/store/tool_use.jsonl` stays byte-identical (verified by
  running the `Golden`/`Fixture`/`Supersed`/`TestRecovery_ToolUseIndex` tests green).

## Part B — FINAL implementation: versioned sidecar + intent-first publication

Binding lives in `index/observations.jsonl`, versioned and append-only. The crash contract is
**intent-first, not one-Write-atomic**.

### Exact interfaces (additive; frozen Store interface untouched)

```go
// observation_publication.go
type ObservationReserver interface {
    ReserveObservation(ctx, obs core.ObservationID, rec ToolUseRecord, supersedes []core.ToolUseID) error
}
type ObservationRecovery interface {
    RecoverToolUseByObservation(ctx, obs core.ObservationID) (ToolUseRecord, error)
}
// observation_lookup.go — pure committed read
type ObservationReader interface {
    ToolUseByObservation(ctx, id core.ObservationID) (ToolUseRecord, error)
}
var _ ObservationReserver = (*FSStore)(nil)
var _ ObservationRecovery = (*FSStore)(nil)
var _ ObservationReader   = (*FSStore)(nil)
```

- **`ReserveObservation`** writes one versioned intent line (`{v,obs,rec:tuRec,sup}`), fsyncs it and
  `SyncDir`s `index/` BEFORE the caller's legacy record — under `obsPubMu` so concurrent leased calls
  cannot steal a reserved derived id. It redacts `ArgsPreview` at the same choke point and stores the
  redacted record so replay reproduces exact bytes (never recomputed). It refuses a conflicting
  reservation (same tool_use id, different Root; or an observation already bound to a different record)
  with `ErrAppendOnly`; two observations over the SAME compatible record (same id+Root) are allowed
  (a repeated host id legitimately maps several observations to one record).
- **`RecoverToolUseByObservation`** resumes a pending intent and completes its legacy publication by
  replaying the stored record and only the supersede targets whose precondition still holds (a target
  already superseded by a LATER record is left untouched), then commits and returns the record. An
  ambiguous / unreadable / unknown-version intent is `ErrDegraded`; an observation with no intent is
  `ErrNotFound`. It calls `mutate()`, so a read-only store refuses it.
- **`ToolUseByObservation`** is the pure committed lookup: committed → record; reserved-but-unpublished
  intent → `ErrDegraded` (unavailable, never a false published record); ambiguous / unreadable →
  `ErrDegraded`; closed → `ErrDegraded`; empty/invalid id or genuine miss → `ErrNotFound`. It never
  writes and never mints.

### Load / recovery derivation

`loadToolUse` calls `loadObservationsLocked()` AFTER the tool_use index (same open pass), so
completeness is derived from it. An intent whose record is present with the reserved Root and whose
required marks are applied is committed; otherwise pending. A malformed / truncated / unknown-version
line is counted and its observation (recovered by a lenient top-level `obs` token scan that tolerates a
truncated tail — the normal crash shape) marked unavailable, never dropped into absence. Two intents
for one observation naming different records → ambiguous.

### Files changed (exact scope)

- `internal/store/observation_publication.go` (NEW) — sidecar format, reserve, recover, load,
  completeness derivation, converters.
- `internal/store/observation_lookup.go` (rewritten) — committed reader.
- `internal/store/fsstore.go` — additive fields `obsCommitted/obsPending/obsAmbiguous/obsUnavailable/
  obsReserved` + `obsPubMu`.
- `internal/store/tooluseindex.go` — reverted to frozen `tuRec`; one added call
  `loadObservationsLocked()` at the end of `loadToolUse`.
- `internal/store/tooluse.go` — `Observation` field comment only (points at the sidecar).
- `internal/store/readonly_test.go` — capability classification only: `ToolUseByObservation` added to
  `readOnlyReadsAllowlist`; `ReserveObservation` and `RecoverToolUseByObservation` added to
  `readOnlyRefusals`.
- `internal/store/observation_publication_test.go`, `observation_lookup_test.go` (NEW/rewritten).

## Verification (recorded, direct exit status)

```
$ GOMAXPROCS=2 python dist/v6-remediation/run.py obs-sidecar-final go test \
    -run 'TestObservation|TestReadOnly_EveryExportedMethodIsClassified|Golden|Supersed|TestRecovery_ToolUseIndex' \
    -count=1 ./internal/store
{"id":"obs-sidecar-final","status":"passed","exit_code":0,...}  ok  (EXIT=0)
```
Runs recorded: `obs-sidecar-1` (a failing iteration on truncated-line attribution, fixed), `-2`,
`-golden`, `-final` under `plans/sdd/V6-remediation/runs/`. `gofmt -l` and `go vet ./internal/store`
clean. 12 focused tests map to the required cut points: reserve→recover→commit→reopen; restart cut
between intent and record; unknown-version and malformed → unavailable; conflicting reservation
refused; ambiguous sidecar → degraded; partial supersede batch + later supersession preserved; bounded
intent refused; plus context / closed / empty-invalid / pending-unavailable / no-binding reads.

## Remaining limitations / shared-durability choices returned to main

1. **In-process commit is via `RecoverToolUseByObservation`, not an auto-flip on record-land.** The
   observer's intended path is Reserve→Recover (Recover appends the record and commits). If the
   observer instead appends the legacy record directly (bypassing Recover), the committed lookup
   reports the intent unavailable until a Recover or a reopen re-derives it. Wiring an auto-reconcile
   on record insertion is a small additive hook (a reverse `recordID→observations` index consulted in
   `putToolUseLocked`); I did not add it because the record-land path is main's observer orchestration,
   not this store scope. **Return to main:** confirm the Reserve→Recover flow, or request the
   reconcile hook.
2. **No power-loss atomicity claim.** The intent is fsynced + dir-synced before the record; a crash
   between them leaves a recoverable pending intent (reopen + Recover complete it). One `Write` is not
   claimed atomic.
3. **Object sync / capture-link-before-ACK / ACK authority are NOT here** (main owns `publication_sync.go`
   and the observer). A sidecar intent is never permission to ACK.
4. **Distinguishing duplicate observation vs repeated host identity:** implemented as two observations →
   one compatible record (same id+Root) allowed; two records → one observation ambiguous; one id, two
   Roots refused. If main needs a different rule for repeated `HostID`, that is a contract choice to
   confirm.
5. No historical bytes migrated; no `runtime.migration.*` switch enabled.

## Part C — Correction round (prior handoff NOT accepted; six defects fixed)

Main inspected the Part-B handoff and rejected it. Defects in the prior handoff and the exact
corrections (all within the assigned scope; runs `obs-sidecar-v2-*` under `runs/`, exit status direct):

1. **Binding was not wired into the public write path.** Prior handoff exposed only Reserve/Recover;
   an ordinary `RecordToolUse`/`RecordToolUseSuperseding(rec.Observation != "")` created NO binding, so
   main's derived-recovery-first path still lacked it. **Fix:** both public methods now route an
   observation-bearing record through `publishObservation` (fsynced intent → single record-plus-marks
   batch → `SyncPublication` → committed reconciliation), serialized under `obsPubMu`, with the legacy
   cores (`recordToolUseCore`/`recordSupersedingCore`) factored out to avoid reentrant deadlock.
   Ordinary NO-observation writers now honour existing reservations (`reservedIdentityConflictLocked`)
   so they can no longer steal a reserved id. Frozen line shapes unchanged.
2. **The old partial-batch test was not a real cut** (record never written before recovery, so a
   pre-existing record made `RecordToolUseSuperseding` return early with marks missing yet Recover
   reported success). **Fix:** `completeSupersedeMarks` completes only the missing marks;
   `completeIntentLocked` returns the record ONLY when `observationPublishedLocked` holds (never success
   while false); a later conflicting supersession is preserved. New `TestObservationPublish_ActualPartialBatchCut`
   persists the intent + record + one of two marks, then proves recovery completes the missing mark and
   leaves the later supersession intact.
3. **Recovery claimed durability without syncing.** **Fix:** `completeIntentLocked` calls
   `s.SyncPublication(ctx, rec.Root)` to verify+sync the original root BEFORE the legacy reference and
   again after the append, before committing; a missing original object keeps the intent incomplete
   (`TestObservationPublish_MissingObjectsKeepIntentIncomplete`, real `PutBytes` roots).
4. **Load discarded scan errors / could yield false absence.** **Fix:** a global `obsSidecarUncertain`
   flag is set on any malformed/oversize/unattributable line, scan-bound exhaustion, or unreadable
   file; while set, an UNKNOWN observation lookup and any new reservation are `ErrDegraded` (never
   false absence); committed bindings are preserved. Strict per-line decode rejects unknown version and
   duplicate/noncanonical fields (byte-for-byte canonical round-trip); observation ids are validated as
   canonical `core.ParseHash` (tests now use `core.NewObservationID`, not `sha256:o1`). Total scan bytes
   and entries are bounded.
5. **Identity compared id only (silent rebind).** **Fix:** reservations bind an id to a full
   `reservedIdentity{Root,Session,Tool}`; re-reserving the exact intent is idempotent (writes nothing);
   the same id under different immutable metadata, or an observation rebind, is `ErrAppendOnly`; a
   distinct observation over the SAME compatible record is allowed
   (`TestObservationPublish_IdentityConflictAndIdempotence`).
6. **I/O failure / non-regular sidecar.** **Fix:** a write/sync/close failure sets
   `obsSidecarUncertain` (poisons completeness, blocking absent-claims and rewrites over partial bytes);
   `refuseNonRegularSidecar` refuses a symlink/dir/special path before opening for write
   (`TestObservationPublish_NonRegularSidecarRefused`, portable directory case).

**Verification.** `obs-sidecar-v2-2` (12 observation tests + classification) and `obs-sidecar-v2-regress`
(golden/fixture/supersede/recovery/read-only) both PASS (exit 0). `gofmt`/`go vet` clean. `readonly_test.go`
classification updated (`ReserveObservation`/`RecoverToolUseByObservation` swept; `ToolUseByObservation`
allowlisted) and the swept set again equals the mutate-guarded set.

**Remaining limitations / durable-contract choices for main.**
- Main's parent `observer`-derived-recovery-first test is main's to run; the store-side binding is now
  created by the ordinary write path, but I do not claim that parent test passes.
- `obsSidecarUncertain` is conservative: a torn sidecar tail blocks NEW reservations and turns unknown
  lookups unavailable until the sidecar is repaired (fsck). This is the decision's "unavailable if
  completeness cannot be proved" applied literally; if main wants a torn TAIL (the normal crash shape)
  to be tolerated for new writes, that is a durable-contract relaxation to confirm.
- `completeIntentLocked` commits when the record is durable and every selected target is non-live
  (superseded by us or by a later record); if a required target was superseded by a DIFFERENT record it
  is preserved and treated as done — the observation binding is the primary guarantee. Confirm if a
  target-level "must be our mark" rule is wanted instead.

## Part D — Hardening round (final review F1/F2/F4 + main findings 1–4)

An independent `observation-sidecar-final-review.md` and four main findings were dispatched against the
Part-C sidecar. All fixes are within the assigned scope (`observation_publication.go`,
`observation_lookup.go`, and their `_test.go` files). Runs are `observation-hardening-*` under `runs/`,
`GOMAXPROCS=2`, focused patterns only, exit status direct.

1. **F1 — the byte/entry caps did not actually stop the load scan.** Part C set `obsSidecarUncertain`
   at the ceiling but `bufio.Scanner` still read to EOF. **Fix:** `loadObservationsLocked` now scans over
   `io.LimitReader(f, maxObservationSidecarBytes+1)` with a `maxObservationLine` buffer and `break`s the
   loop the moment the running byte/entry total crosses the cap — a real work bound, not merely "stop
   applying". An over-long line or read error via `sc.Err()` also poisons completeness.
2. **F4 — TOCTOU on the sidecar path.** Part C `Lstat`-checked then opened by absolute path. **Fix:**
   both load and append go through `os.OpenRoot(index)` + `root.OpenFile`/`root.Lstat`, so a symlink or
   reparse alias swapped between check and open cannot redirect the read/write (same confinement as
   `publication_sync.go`). A non-regular sidecar fails closed before any write-open.
3. **Finding 1 — committed replay could append unrelated marks.** The committed branch compared the
   record id only, and `publishObservation` completed the CALLER's supersede list; so re-publishing a
   committed observation with a DIFFERENT list appended new, unrelated marks. **Fix:** every observation
   stores its complete ORIGINAL canonical intent (`obsBinding.raw`), for committed AND pending. The
   conflict/idempotence test is EXACT byte-for-byte equality of the frozen `tuRec` + target list
   (`canonicalIntent`, deterministic, no trailing newline); a differing list is `ErrAppendOnly` and
   appends nothing. Completion always replays the STORED original (`b.intent`), never the caller's.
   `sameIntent`-by-Root/Session/Tool is gone. Duplicate/noncanonical parsed intent → the observation is
   ambiguous; a truncated/unknown attributed intent takes priority over a previously committed record.
   (`TestObservationPublish_CommittedReplayWithDifferentSupersedesRefused`.)
4. **Finding 2 — a missing target was counted "done".** Part C's `observationPublishedLocked` checked
   only the record root; a target now absent looked complete, hiding corruption. **Fix:** the intent now
   records each selected target's identity at reserve (`observationTarget{ID,Root}`, sidecar `v2`).
   `buildIntentLocked` normalises to the exactly-LIVE (present + `StatusOK`) target set — a legacy caller
   passing an absent/already-superseded target has it omitted (a genuine no-op), distinguishable on
   recovery from a LOST target. `observationStateLocked`/`completeIntentLocked` return `lost` (→
   `markObservationUnavailableLocked`, `ErrDegraded`) when a recorded target has vanished or its Root no
   longer matches — never a forged success; a later supersession is preserved only when the recorded
   identity still agrees. (`TestObservationPublish_LostTargetIsUnavailableNotForged`, both the vanished
   and identity-changed cases.)
5. **Finding 3 — poison flag written without `s.mu`, and a reservation/legacy admission race.**
   `obsSidecarUncertain` was set on the write path while lookups read it under `s.mu`. **Fix:**
   `poisonSidecar()` takes `s.mu.Lock`; every uncertain-flag mutation is under `s.mu`. Reservation vs the
   plain no-observation writer is serialised: the ordinary `RecordToolUse[Superseding]` no-observation
   path now also takes `obsPubMu`, so `reservedIdentityConflictLocked` (run by the legacy core under
   `s.mu`) cannot interleave with a reservation's conflict-check-then-install. The record-plus-marks
   batch is never split, and cores never take `obsPubMu` (they run beneath it), so no reentrant deadlock.
6. **Finding 4 — caps were LOAD-only, not a lifetime append bound.** **Fix:** `appendObservationIntent`
   checks `obsSidecarBytes`/`obsSidecarEntries` (tracked across load AND every append) BEFORE the write
   and refuses at the ceiling with `core.ErrBudget` — an explicit capacity limit, not unbounded support.
   (`TestObservationPublish_AppendCapacityCapRefused`, both the byte and entry ceilings.)
7. **F2 posture — fail closed until reopen, no runtime repair.** A runtime intent-write/sync failure or
   any torn/oversize/unreadable load sets `obsSidecarUncertain` for the PROCESS LIFETIME: no silent
   clear, no automatic truncation, no fsck helper exists. A healthy full store reopen re-derives the
   state (and `SyncPublication` verifies the original data before recovery succeeds). While uncertain,
   an UNKNOWN observation and any new reservation are `ErrDegraded`. An injected-`obsSyncData` regression
   proves the failure poisons the sidecar, that clearing the transient fault does NOT clear the poison,
   and that no legacy record was fabricated
   (`TestObservationPublish_InjectedSyncFailureFailsClosed`). Runtime transient poison therefore requires
   a daemon restart; torn/conflicting on-disk evidence needs a verified backup/recovery.

**Verification (recorded, direct exit status).** `observation-hardening-1` — the four new hardening tests
(`AppendCapacityCapRefused`, `InjectedSyncFailureFailsClosed`, `CommittedReplayWithDifferentSupersedesRefused`,
`LostTargetIsUnavailableNotForged`) — PASS, exit 0. `observation-hardening-4` — every `TestObservation*`
(the full Part-B/C suite plus these four, plus main's `TestObservationRestore_PendingIntentCannotCertifyReader`
which exercises the binding) — PASS, exit 0. `gofmt -l` clean on the changed files; `go vet ./internal/store`
exit 0.

**Known concurrent breakage (NOT mine, NOT fixed).** `observation-hardening-2/3` failed ONLY on
`TestReadOnly_EveryExportedMethodIsClassified`: "AuditPublication is delegated; remove its reads
justification". `AuditPublication` lives in main-owned `publication_audit.go`, and the stale allowlist
entry is in main-owned `readonly_test.go` (line 396); both show ` M` uncommitted, and this test PASSED at
this session's first run — main is mid-edit on that method. It is outside my exclusive scope; I did not
touch either file. My own exported methods (`ReserveObservation`, `RecoverToolUseByObservation` swept;
`ToolUseByObservation` allowlisted) remain correctly classified — the failure names only `AuditPublication`.

**Limitations returned to main (unchanged from Part C, plus).**
- The Part-C limitations above still hold (parent observer test is main's to run; torn-tail is
  conservatively fail-closed; target-level "must be our mark" is not enforced).
- The sidecar `v2` intent format (which now records target identities) is NOT shipped, so the format
  change is free; if main ships a `v1` sidecar first, a version-gated migration is required.
- No fsck/repair helper is provided. Recovering a poisoned or torn sidecar is a backup/restore or a clean
  reopen — deliberately, so no code path silently rewrites durable evidence.

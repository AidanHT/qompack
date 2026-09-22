# GC retention-read safety — work record

Main's durable-data adjudication: **unreadable or incomplete retention evidence must never be treated
as proven absence and then swept against.** The GC mark phase harvests hash references from the
retention-root files (pins, evidence, delivery leases, declared retention roots, checkpoints, pending
markers) and from the delivery-ack journal; anything those files name is held live and everything else
is collected. Four read paths quietly turned a *failed read* into an *empty retention set*, so a live
root named only by a damaged source would be swept.

Exclusive scope this round: `internal/store/gcrun.go`, new `internal/store/gc_retention_read_v6_test.go`,
this report. No other source/test changed. Public interfaces and frozen wires are untouched (the two
functions whose signatures changed — `acknowledgedDeliveries`, `openLeaseLines` — are unexported and
called only within `gcrun.go`). Focused `TestGCRetention_*` / `TestGC_*` runs only, `GOMAXPROCS=2`, via
`dist/v6-remediation/run.py`.

## The four defects (verified against the shipped producer)

1. **`harvestFile` read every `os.Open` error as "missing".** A path that EXISTS but is a directory, a
   symlink/reparse alias, or hits a permission/I/O error returned `(false, nil)` — the same as a
   producer that never shipped — so the pass continued and swept whatever the file would have held.
2. **`harvestFile` ignored `bufio.Scanner.Err()`.** A line past `scannerMaxBuf` (or a mid-file read
   error) stops the scan; the lines *after* it were never read. The loop returned as if the file were
   fully harvested, dropping every live reference beyond the overlong line.
3. **`harvestTokens` treated ALL `json.Decoder.Token()` errors as a clean end of stream.** A malformed
   or torn token mid-document stopped the walk and returned `(false, nil)`, so any hash *after* the
   break (the "hash beyond first valid prefix") was silently dropped from the live set.
4. **ACK-based lease release joined on the delivery nonce alone.** `acknowledgedDeliveries` admitted any
   line with a non-empty nonce + non-empty observation id (ignoring version), and `openLeaseLines`
   released a lease whenever its nonce was in that set. An acknowledgement for delivery *N* under
   observation *A* would release a lease for delivery *N* under observation *B*, and an ack carrying an
   unknown version this build cannot interpret would still release. That is "release a different lease"
   and "trust evidence you cannot read" — both forbidden.

The shipped producer (`internal/daemon/delivery_lease.go`) writes `deliveryLease{v, delivery, session,
request, arrival, observation_id}` and `deliveryAck{v, delivery, observation_id, root}`, both at
`v = core.EvidenceVersion` (=1). So both records carry an observation identity and a version — the
exact fields a safe join needs. The existing store test fixtures for
`TestGC_AcknowledgedDeliveryLeaseStopsRetaining` write a *legacy* lease shape with no `observation_id`;
that compatibility case is handled explicitly (below).

## The fix (smallest compatible mechanism)

- **`openRetentionRoot(path)`** (new): returns `missing=true` only for `os.IsNotExist`. A path that
  exists but is not a readable regular file — Lstat is symlink-*non-following*, so a planted symlink or
  directory is caught — returns `errRetentionRootsUnavailable`. A post-open `fstat` on the handle held
  closes the Lstat→open TOCTOU window. No repair, no deletion.
- **`harvestFile`**: opens through `openRetentionRoot`, and after the line loop checks `sc.Err()`; a
  scan that stopped short halts the pass with `errRetentionRootsUnavailable`.
- **`harvestTokens`**: distinguishes `io.EOF` (the clean terminator — unchanged behavior) from every
  other decode error; a torn/malformed stream halts the pass rather than truncating the live set. A
  well-formed multi-record `.json`/`.jsonl` still walks to `io.EOF` and is unaffected.
- **`acknowledgedDeliveries`**: admits an ack only under `v == core.EvidenceVersion` and a `core.ParseHash`-
  valid observation id, and returns `nonce → {observation ids}`. Bounded by `deliveryAckSetMax` *total*
  (nonce, observation) pairs, so a single-nonce hostile journal cannot grow the map without bound;
  stopping leaves the remaining leases open (over-retain).
- **`openLeaseLines`**: releases a lease only when its nonce is acknowledged AND its version is known
  AND its `observation_id` is exactly one acknowledged under that nonce. An ack for a different
  observation, or an unknown lease version, retains. A **legacy** lease with no `observation_id` is
  released by a valid ack for its unique nonce — the nonce alone names the delivery, and nonces are
  unique, so this cannot release a *different* lease; a lease that *carries* an identity must match it
  exactly.

Every failure direction is over-retention (collect nothing, or keep a lease open). `GC` already maps
`errRetentionRootsUnavailable` to `GCReport{RetentionRootsError: true}` and a nothing-collected pass, so
the new halts surface on that existing report field and are not a hard error. `ctx` cancellation and
the deadline are checked *before* `dec.Token()` in the harvest loop, so they still return
`context.Canceled` / `Truncated` and are never reclassified as a retention-source failure.

## Not changed, on purpose

- `declaredRetentionLines` still over-retains a *parseable-but-classless* declaration and Louds once per
  pass (finding F4-5, `TestDeclaredRetentionLines_CountsEachBadLineAndLoudsOnce` is unchanged). Only a
  genuinely *torn* (unparseable-JSON) retention-roots line now halts via `harvestTokens`, which is the
  brief's "an incomplete set is not safe to sweep."
- No rollover/segment machinery, and no capacity-closure claim: main owns the new contract design.
- Out of scope but noted for main: `gcRootFiles`'s `os.ReadDir` of `checkpoints/` and
  `pendingRootFiles`'s `os.ReadDir` of the pending-write dir still drop their whole contribution on a
  directory-read error (same defect class, different call path). They were not among the four named
  defects and touching them widens the change surface; the "directory retention root" hazard is already
  covered for the harvested files by `openRetentionRoot`.

## Verification (recorded via run.py, direct exit status)

Tests-first with a negative control, exactly as the brief required.

- **Negative control (unfixed reader): `gc-retention-negctl`, `gc-retention-negctl-recorded` — FAIL (exit 1).**
  `TestGCRetention_UnreadableLeaseRootHaltsCollection`, `…_TornCheckpointHidesLaterReferenceHaltsCollection`,
  `…_OverlongLeaseLineHidesLaterLiveReferenceHaltsCollection`, and `…_AckReleaseRequiresExactObservationAndVersion`
  all failed by demonstrating the actual loss — `RetentionRootsError=false` with the pass sweeping, and
  the wrong-observation / unknown-version acks releasing their leases. The legacy-compat and cancellation
  guards (`…_LegacyLeaseWithoutObservationStillReleasesOnNonce`, `…_CancelledContextIsNotARetentionError`)
  already passed, confirming the fix preserves them.
- **After the fix: `gc-retention-fix-1` — PASS (exit 0).** All six `TestGCRetention_*` plus the full
  existing suite `TestGC_*` / `TestHarvest*` / `TestCompactRetention*` / `TestDeclaredRetentionLines*` /
  `TestLoadRoots*` / `TestAppendRetentionRoot*` (incl. `TestGC_AcknowledgedDeliveryLeaseStopsRetaining`,
  `TestGC_UnreadableAcknowledgementKeepsItsLeaseOpen`, `TestGC_HarvestsHashesFromCheckpointPinsEliminations`,
  and `backup_test`'s `RetentionRootsError=false` assertion). `gc-retention-fix-2` re-ran the ack tests
  after the total-pairs bound refinement — PASS (exit 0).
- `gofumpt -l` clean on both changed files; `go vet ./internal/store` exit 0.

## New regression tests (real store + real GC pass)

- `TestGCRetention_UnreadableLeaseRootHaltsCollection` — a directory planted at `delivery-leases.jsonl`;
  the doomed root survives and `RetentionRootsError` is set.
- `TestGCRetention_TornCheckpointHidesLaterReferenceHaltsCollection` — a checkpoint that names an early
  hash, breaks, then names a late hash; both survive after the fix.
- `TestGCRetention_OverlongLeaseLineHidesLaterLiveReferenceHaltsCollection` — a first lease line past
  `scannerMaxBuf` hides a later live reference; it survives.
- `TestGCRetention_AckReleaseRequiresExactObservationAndVersion` — matching ack releases; different-
  observation ack and unknown-version ack retain; unacknowledged lease stays open.
- `TestGCRetention_LegacyLeaseWithoutObservationStillReleasesOnNonce` — the compatibility pin for a
  lease predating the `observation_id` field.
- `TestGCRetention_CancelledContextIsNotARetentionError` — a cancelled pass reports `context.Canceled`,
  never `RetentionRootsError`.

## Limitations returned to main

- The version join is pinned to `core.EvidenceVersion`. When the daemon bumps the lease/ack version,
  this reader will treat the new-version records as "unknown → conservatively retain / do not release"
  until it is taught the new version. That is the safe direction, but it means a version bump without a
  matching store-side update stops ACK-based lease release (over-retention, not data loss).
- The two `os.ReadDir` paths noted above (checkpoints dir, pending-write dir) are the same defect class
  and remain; addressing them changes `harvestHashes`/`gcRootFiles` signatures and was left out of this
  minimal round.
- A persistently torn known-retention file now blocks *all* collection every pass until it is repaired
  by a verified backup/restore — there is no fsck/repair here by design (main's durable-data decision).

# Round 2 — finishing the class (directory read errors, identity, FIFO/alias hardening)

Main adjudicated that the round-1 "out of scope" directory-read-error items are the same defect class
and must be finished, flagged a false TOCTOU guarantee in `openRetentionRoot`, and required the ack
journal to be opened without a FIFO-hang or parent-alias hazard. Same exclusive scope.

## What round 1 left, now closed

1. **`gcRootFiles` swallowed the checkpoints `os.ReadDir` error** (`if … err == nil`) and
   **`pendingRootFiles` returned `nil` on any `os.ReadDir` error** — both read a listing failure or a
   non-directory in place of the dir as "no entries", dropping every root that directory names.
   **Fix:** a new `listRetentionDir` enumerates each directory. A directory that does not exist yet is
   normal absence; a symlink alias, a non-directory in its place, or a partway listing failure halts
   the pass with `errRetentionRootsUnavailable`. `gcRootFiles(budget)` and `pendingRootFiles(budget)`
   now return `([]gcRootFile, truncated, error)`; `harvestHashes` propagates both. Enumeration is
   **bounded**: `os.File.ReadDir(gcDirBatch=512)` per batch, never `os.ReadDir` materialising the whole
   listing, with a ctx check (error) and a deadline check (truncate) BETWEEN batches — the same
   context/deadline distinction the harvest loops keep.

2. **`openRetentionRoot`'s false TOCTOU claim.** Round 1 claimed the post-open `Stat` closed the
   TOCTOU window but only checked regular mode — no `os.SameFile`. **Fix:** the parent is now opened as
   an `os.Root` and the entry Lstat'd THROUGH that root (confined, symlink-non-following → static alias
   rejection); the read goes through `paths.OpenShared` (so a concurrent `WriteAtomic` replace of the
   daemon journals / retention-roots compaction is never blocked on Windows); and `os.SameFile(li,
   ofi)` compares the confined Lstat with the opened handle, refusing a mismatch. The comment now states
   honestly that this DETECTS a swap between Lstat and open — it does not PREVENT a symlink being
   followed during a blocking open (residual race, bounded by these paths living under the project's own
   `.qompack` tree). A `NotExist` from the open AFTER a successful Lstat is now treated as observed
   evidence disappearing → fail closed (`errRetentionRootsUnavailable`), not `missing`.

3. **`acknowledgedDeliveries` used a plain `os.Open`** — which can hang on a FIFO and does not reject a
   parent alias. **Fix:** it now opens through the same confined/non-regular-rejecting/shared
   `openRetentionRoot`, so a FIFO/dir/symlink where the ack journal belongs is refused BEFORE any
   blocking open and another tree's file cannot stand in. Because failing to read acks only RETAINS more
   (leases stay open), any such refusal is swallowed to `nil` here rather than halting — the exact
   nonce+observation+known-version join from round 1 is unchanged, and a torn/oversized ack line still
   just yields fewer acknowledgements.

## Verification (recorded via run.py; daemon-dependent runs gated on main's build)

- **Negative control (guard neutralised): `gc-retention-dir-negctl-v2` — FAIL (exit 1).** With
  `listRetentionDir`'s guard temporarily short-circuited to mimic the old swallow-all behavior, both
  `TestGCRetention_UnreadableCheckpointDirHaltsCollection` and `…UnreadablePendingDirHaltsCollection`
  fail at the `RetentionRootsError` assertion — the doomed root is swept. Guard restored immediately
  after. (An earlier `gc-retention-dir-negctl` run also failed against the unmodified code; the pending
  case there tripped on a setup detail — `PutBytes` pre-creates the pending dir — corrected to
  `RemoveAll`+`WriteFile` so the negative control is genuine.)
- **After the fix: `gc-retention-r2-1` and `gc-retention-r2-2` — PASS (exit 0).** All nine
  `TestGCRetention_*` plus the round-1-relevant existing cases (`HarvestsHashesFromCheckpointPinsEliminations`,
  `ReadsTheDeliveryLeaseJournalAsARetentionRoot`, `AcknowledgedDeliveryLeaseStopsRetaining`,
  `UnreadableAcknowledgementKeepsItsLeaseOpen`, `MissingRootFilesAreNotAnError`, `ReportsPerRootOutcomes`,
  `DeclaredRetentionLines`) pass — confirming `os.SameFile(root.Lstat, handle.Stat)` accepts a genuine
  same-file read, and the plumbing signature change is sound.
- The full focused GC/deadline/cancel/resume/mark suite (`gc-retention-r2-suite2`) is gated on main's
  daemon compiling: the store *test* binary imports `internal/daemon`, which is mid-edit
  (`delivery_terminal_generation.go` / `commitAck`/`commitTerminal` signatures — main's rollover work).
  A background watcher re-runs it the moment the daemon builds. My own source compiles clean:
  `go build ./internal/store` and `GOOS=linux go build ./internal/store` both exit 0.
- `gofumpt -l` clean on all three changed files (`gcrun.go`, `gc_retention_read_v6_test.go`,
  `gc_retention_read_unix_test.go`).

## New regression tests (round 2)

- `TestGCRetention_UnreadableCheckpointDirHaltsCollection` — checkpoints/ replaced by a regular file →
  halt, doomed root survives.
- `TestGCRetention_UnreadablePendingDirHaltsCollection` — pending-write registry dir replaced by a
  regular file → halt, doomed root survives.
- `TestGCRetention_NonRegularAckSourceRetainsWithoutHaltingOrHanging` — a directory where the ack
  journal belongs → the lease is retained, no halt (portable, Windows-runnable).
- `TestGCRetention_FifoAckSourceDoesNotHang` (**`//go:build unix`**) — a FIFO where the ack journal
  belongs is rejected before any open; a watchdog fails the test if GC ever blocks on the pipe. It is
  gofumpt-clean and my source cross-compiles for linux, but it is **not executed** this round: the store
  test binary cannot link while main's daemon is mid-edit, on any OS. Left for a green-daemon run
  (WSL/Linux or CI).

## Round-2 limitations returned to main

- The `os.SameFile` identity check DETECTS a Lstat→open swap; it does not PREVENT a symlink being
  followed during a blocking open. `paths.OpenShared` opens by path (it must, for the Windows
  concurrent-replace guarantee), so full open-time confinement is not possible with the current
  `os.Root` API. The residual is bounded by these paths living under the project's own `.qompack` tree.
- The FIFO rejection is static (confined Lstat). A dynamic swap-to-FIFO between the Lstat and the
  `OpenShared` is a residual race; on Unix a blocking `open(2)` of a FIFO could still stall in that
  narrow window. Not closed here (would need `O_NONBLOCK`, which `paths.OpenShared` does not expose).
- Directory enumeration truncates on the deadline mid-listing (returns `truncated`, collects nothing,
  resumable), and answers ctx between batches; it does not persist a partial listing.
- Capacity/rollover is unchanged and NOT complete: this round is retention-read safety only.

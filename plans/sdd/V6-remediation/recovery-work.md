# V6 remediation — maintenance backup/restore store seam (implementation record)

**Owner:** V6 recovery implementation (Opus 4.8, high effort).
**Authorization:** main reviewed `recovery-design.md` and authorized implementation (decisions
D-A…D-F). This record documents the *store seam only*; main owns the CLI verbs, the real daemon-lease
coordination, and the e2e/UAT wiring (D-F).
**Exclusive edit scope honored:** only `internal/store/maintenance.go` (new),
`internal/store/maintenance_test.go` (new), and this file were created. No existing file, and nothing
under daemon/mcp/observer/config/docs/Git, was edited. `LegacyImportGate` was not flipped and no
importer/cutover surface was exposed.

---

## 1. What was built

`internal/store/maintenance.go` — a gate-free `Maintenance` capability wrapping an unexported
`*Migrator` (built with `src == nil`, no gate check), exposing only:

```
func NewMaintenance(s Store, root string, o MaintenanceOptions) (*Maintenance, error)
func (x *Maintenance) TakeBackup(ctx, id) (BackupManifest, error)   // reuses engine TakeBackup
func (x *Maintenance) VerifyBackup(id) (BackupManifest, error)      // validates manifest, then engine VerifyBackup
func (x *Maintenance) Restore(ctx, id, dest) (RestoreProof, error)  // stage → re-verify → prove → atomic publish
type MaintenanceOptions struct { Cfg config.Config; Clock core.Clock; WriterLeaseHeld func() error }
type RestoreProof struct { …; SameBuildOnly bool; CheckpointSealCovered bool; Note string }
```

The `Maintenance` type does **not** embed `*Migrator`, so `Import`/`Parity`/`Cutover`/
`RecordNewFormatWrite`/`AcquireWriter` are unreachable through this surface (D-A). The migrator is
constructed by an in-package struct literal rather than `NewMigrator`, which is the only way to get a
gate-free instance without editing `migrate.go` (allowed by the brief: "migration internals may be
constructed directly inside package without exposing Migrator").

### Decisions, as implemented

- **D-A** — narrow wrapper, no import/cutover, no gate flip. ✔
- **D-B** — `TakeBackup` **fails closed** (`ErrWriterLeaseRequired`) unless `WriterLeaseHeld` is
  supplied *and* returns nil. The seam does not itself acquire the daemon lock (it may not import
  daemon, §3.2); main holds the daemon singleton lock from before `store.Open` and passes the
  re-confirming guard. `NewMaintenance` **requires an explicit config** (`ErrMaintenanceConfig` on a
  zero `Cfg`) — no silent `Defaults()` override.
- **D-C** — `Restore` refuses an existing destination `.qompack` (dir/file/symlink/reparse, via
  `Lstat`) **before any write**; stages inside `dest` (same filesystem); verifies each backup-tree
  file against the manifest at read time; re-verifies the *staged* bytes against the manifest at the
  publication boundary; proves a same-build reader; then `os.Rename`s the staged `.qompack` into the
  still-absent destination (re-checked immediately before). A failed stage is preserved and named in
  the error (`…preserved as evidence at <path>`).
- **D-D** — the source live tree is never touched; `Restore` writes only to a separate destination.
  A test asserts the source retains a write made after the backup and the restore does not contain it.
  No rollback-activation or automatic-downgrade claim is made.
- **D-E** — the reader proof is **same-build only**: it enumerates the *restored* store's own roots
  and tool references and reads their payloads (content-addressing verifies integrity on read). It
  makes **no** legacy-id claim (not vacuous) and no cross-version claim. `ContentRootsProven == 0` is
  refused as `ErrRestoreVacuous`: a successful `Open` alone certifies nothing.
- **D-F** — store seam only; CLI verbs and e2e are main's.

### Security hardening (as requested)

- Backup ids validated to a simple safe set `[A-Za-z0-9._-]`, ≤128 bytes, no dot-forms, no leading
  dot, no Windows reserved device names (`maintValidateID`).
- Manifest entries validated **before any file they name is read** (`readValidatedManifest` runs
  ahead of the engine's `VerifyBackup`): unknown/missing schema refused, and every name must be a
  clean slash-relative path with no NUL, backslash, absolute form, drive letter, or `..` segment;
  duplicates refused. `maintWithin` is defense-in-depth on the write path.
- Post-verify drift: staged files are re-hashed against the manifest **before** publication
  (`maintVerifyStaged`), so a backup-tree file that changes after the read-time verify is caught.
- Restore never overwrites: existing destination `.qompack` refused up front and re-checked before
  the atomic rename.

### Scope explicitly NOT covered (named narrower result, per the brief)

`RestoreProof.CheckpointSealCovered` is **always false**. The same-build content reader proves object
roots and tool references materialize; it does **not** verify the checkpoint chain or delivery-seal
readability. `RestoreProof.Note` directs the operator/main to run packaged `fsck --seal-check` against
the published destination — that check lives outside this engine seam and is main's follow-up.

---

## 2. Verification (exact commands and results)

Environment: Windows 11, `GOMAXPROCS=2`, focused new tests only. Failures were preserved and iterated
(one build failure fixed — an unused `bytes` import after the live-comparison path was dropped in
favor of the correct restored-enumeration proof; see §3).

```
$ GOMAXPROCS=2 go test -run 'TestMaintenance' -count=1 -v ./internal/store
=== RUN   TestMaintenance_ConstructorRequiresExplicitConfig            --- PASS (0.02s)
=== RUN   TestMaintenance_TakeBackupFailsClosedWithoutLease            --- PASS (0.14s)
=== RUN   TestMaintenance_BackupIDValidation                          --- PASS (0.14s)
=== RUN   TestMaintenance_TakeVerifyRestoreRoundTrip                  --- PASS (0.26s)
=== RUN   TestMaintenance_RestoreRefusesExistingDestination          --- PASS (0.14s)
=== RUN   TestMaintenance_CorruptedBackupIsRefused                    --- PASS (0.18s)
=== RUN   TestMaintenance_AdversarialManifestRefusedBeforeReadingFiles --- PASS (0.08s)
=== RUN   TestMaintenance_StagedRehashCatchesPostCopyCorruption       --- PASS (0.16s)
=== RUN   TestMaintenance_RestoreLeavesSourceLaterWritesIntact        --- PASS (0.23s)
PASS
ok  github.com/qompack/qompack/internal/store 1.552s

$ gofmt -l internal/store/maintenance.go internal/store/maintenance_test.go   # (empty = clean)
$ go vet ./internal/store                                                     # (clean)
```

Adversarial coverage delivered (mapped to the brief's list): adversarial path/manifest
(`AdversarialManifestRefused…`), live-writer guard refusal (`TakeBackupFailsClosedWithoutLease`),
corrupted backup (`CorruptedBackupIsRefused`), target conflict (`RestoreRefusesExistingDestination`),
post-copy corruption (`StagedRehashCatchesPostCopyCorruption`), successful readback
(`TakeVerifyRestoreRoundTrip`), original later writes retained (`RestoreLeavesSourceLaterWritesIntact`),
plus id validation and unknown-schema refusal.

**Not run** (out of scope / avoided by directive): whole-package `go test ./internal/store` (would
compile sibling children's in-flight `provenance.go` / `publication_audit.go` and is not my scope);
the repo-pinned `gofumpt`/`devtool fmt` (memory: `devtool fmt` rewrites other worktrees — never run
`fmt -w` across the tree). `gofmt` is clean; main should run the pinned formatter/linter in CI over
the integrated tree.

---

## 3. Design deltas from `recovery-design.md`

One substantive correction during implementation: the design §5.5 suggested comparing the restored
store byte-for-byte against the **live** store. That is wrong under D-D — the live store legitimately
advances past the backup (later writes), so a live comparison would fail a correct point-in-time
restore. The proof instead enumerates the **restored** store's own captured roots and reads each back
(self-verifying via content-addressing), which is both correct and non-vacuous. `RestoreLeavesSource
LaterWritesIntact` is the regression that pins this.

---

## 4. Residual questions returned to main

1. **Daemon-lease wiring (D-B).** The seam requires `WriterLeaseHeld`; main must acquire the daemon
   singleton lock *before* `store.Open` and hold it through the whole operation, then pass the
   re-confirming guard. The seam cannot enforce ordering across the `store`/`daemon` boundary.
2. **Checkpoint/seal coverage.** The reader proof deliberately excludes checkpoint-chain and
   delivery-seal integrity. Main should wire packaged `fsck --seal-check` against the published
   destination as the complementary check (RestoreProof.Note says so).
3. **Windows rename-after-close.** `Restore` opens the staged store for the proof, `Close()`s it, then
   renames the staged `.qompack`. Tests pass on this Windows host, but if a busy host ever leaves a
   lingering handle, the rename returns an error and the staging tree is preserved as evidence
   (explicit, non-destructive). No silent partial publish is possible.
4. **Empty-source restore.** A backup that captured zero content roots is currently refused
   (`ErrRestoreVacuous`) rather than published, to avoid a false "complete" from a bare `Open`. If an
   operator ever needs to restore a genuinely empty store, main can add an explicit opt-in.
5. **Manifest TOCTOU.** `readValidatedManifest` validates names, and the read-time + staged re-hash
   both bind bytes to the manifest, so a swapped backup-tree file is caught. The backup directory is
   the product's own; no live adversary racing it is assumed. Flagged for completeness.

---

## 5. Hardening round (post-review, same scope)

Main's review found gaps before CLI integration. All fixed in `maintenance.go`/`maintenance_test.go`
only; no existing file changed.

- **Single validated manifest snapshot.** `VerifyBackup`/`Restore` now hash the tree themselves
  against the one snapshot `readValidatedManifest` returns; the old second, unvalidated
  `m.VerifyBackup` re-read (TOCTOU) is gone.
- **Hostile manifest input.** Bounded manifest bytes (64 MiB) and entry count (1<<20); per-entry
  validation of clean slash-relative names with per-component checks: no `..`/`.`, no `:` (drive or
  NTFS ADS), no backslash, no trailing dot/space, no control chars, no reserved device name; plus
  non-negative bounded sizes, 64-hex digest shape, exact **and portable case-fold** duplicate
  rejection, and manifest `ID` must equal the requested id.
- **Symlink/reparse refusal.** `maintNoFollow` Lstats every path component under the trusted project
  root before reading the manifest and each backup file, rejecting symlinks/reparse points (a hostile
  symlink component test proves it; skips where the host denies symlink creation).
- **Streaming under context + size bound.** Manifest read is bounded; every verify/copy/hash streams
  through a 1 MiB buffer that checks `ctx.Err()` each iteration and refuses more than the manifest
  size — no whole-file `os.ReadFile` of large objects/index files. `Restore` threads the caller's
  context; `VerifyBackup` stays ctx-free (main's `cli/backup.go` calls `VerifyBackup(id)` — out of
  scope) and runs `verifyTree` under a background context (a `VerifyBackupContext` is available to wire
  if main wants a cancellable verify).
- **Lease guard before AND after.** `TakeBackup` re-confirms the guard after the copy; a lease lost
  mid-copy fails the call (no success claim) and **preserves** the artifact for inspection.
- **Read-only reader proof.** `proveReader` now uses `OpenReadOnly` so proving changes no staged byte,
  and the staged tree is re-hashed **again** after the proof, before publishing.
- **Publish exclusivity.** `maintPublish` holds an `O_EXCL` lock file and refuses an existing
  destination before a single `os.Rename`.

### Escalated to main (portable-primitive difficulty)

**Atomic no-clobber directory publish is not portable in pure Go.** Windows `MoveFile` is no-clobber
for a directory; POSIX `rename(2)` **replaces an empty target directory**, and Go's stdlib exposes no
`renameat2(RENAME_NOREPLACE)`. `maintPublish` therefore gives: exclusion among maintenance publishers
(the lock) + refuse-existing + single rename — **not** an absolute never-overwrite. `RestoreProof.Note`
and the comments say so. A non-maintenance actor racing an *empty* `.qompack` into place between the
refuse-check and the rename on POSIX is the residual. **Main should choose the supported-platform
no-clobber primitive** (Windows `MoveFileEx` without `MOVEFILE_REPLACE_EXISTING`; Linux
`renameat2(RENAME_NOREPLACE)` via `x/sys`) at the CLI/OS layer, or accept the lock-based exclusion as
sufficient for the operator flow.

### Interface note

`VerifyBackup(id string)` signature is preserved to keep main's `internal/cli/backup.go` compiling
(`go build ./internal/cli` verified green). `TakeBackup(ctx,id)`, `Restore(ctx,id,dest)`,
`NewMaintenance`, `MaintenanceOptions{Cfg,Clock,WriterLeaseHeld}` and `RestoreProof` are unchanged.

### Verification (hardening round)

```
$ GOMAXPROCS=2 go test -run 'TestMaintenance' -count=1 -v ./internal/store   # 11 tests: 10 PASS, 1 SKIP (symlink needs Windows privilege)
$ gofmt -l …maintenance.go …maintenance_test.go   # clean
$ go vet ./internal/store                           # clean
$ go build ./internal/cli                           # clean (main's backup.go compiles against the seam)
```
Tests added/updated: after-copy lease loss, case-fold/ADS/reserved/trailing-dot/backslash/bad-digest/
bad-size/id-mismatch manifest cases, hostile symlink component, context cancellation. Initial-round
failures preserved in §2.

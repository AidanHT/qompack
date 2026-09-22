# V6 remediation — independent recovery / backup / maintenance review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high, same session
`788ed24f-1a30-4096-b080-f98cfd4ab92d`, no nesting.
**Date:** 2026-09-21.
**Nature:** source review only. No tests, builds, Git, config, permission/auth/billing changes. This
document is the review's only output. Findings are read from code, not from child work records.

**Read (this pass):** `internal/store/maintenance.go`, `internal/cli/backup.go`,
`internal/cli/writer_lease.go`, `internal/daemon/borrowed_lease.go`, `internal/daemon/daemon.go`
(Run lease + audit call, ~500-619), `internal/cli/daemon.go` (bootstrap ~40-167),
`internal/daemon/lock.go` (AcquireLock / Heartbeat / owned / Release),
`internal/paths/rename_noreplace_{windows,linux,darwin,other}.go`,
`internal/store/publication_audit.go`, `internal/daemon/publication_audit.go`,
`internal/store/backup.go` (TakeBackup + refuseIfTheProjectMoved), `internal/store/migrate.go`
(NewMigrator + Migrator struct). Not re-read line-by-line: the engine's `RestoreBackup`/rollback
drill (the maintenance path does **not** call them — see §2) and the full config loader.

---

## 1. Verdict at a glance

The maintenance/backup/audit implementation is **substantially sound and closes the recovery-design
blockers it set out to close.** Writer exclusion is real, the restore path avoids the engine's D3
in-place hazard, malicious-manifest hardening is thorough, and the publication audit is genuinely
bounded, read-only and wired at the right place. Six residual findings (R1–R6) follow; only R1 rises
to a trust/durability concern worth blocking on before frozen validation. Four questions return to
main (§5). This review grants **no** runtime acceptance.

**What is confirmed correct (not just claimed):**

- **Writer exclusion is enforced at the real single writer.** `runBackup`/`runDaemon` call
  `acquireWriterLease` → `daemon.AcquireLock`, which returns `ErrLockHeld` when a live daemon answers
  the liveness dial (`lock.go:176`, `backup.go:96-99`). A backup therefore **cannot** run while the
  daemon is up — this closes recovery-design D2, whose gap was that `AcquireWriter` excluded only
  another `Migrator`, not the daemon.
- **The lease guard genuinely re-confirms ownership.** `Lock.Heartbeat` → `owned()` reads the lock
  file and checks `PID == getpid() && Owner == l.owner` (`lock.go:255-295`), so `WriterLeaseHeld`
  (`lease.Heartbeat`) detects a takeover, it does not blind-refresh. `TakeBackup` re-checks it before
  and after the copy (`maintenance.go:118,125`).
- **Borrowed-lease bootstrap has single, correct ownership.** `NewWithLease` validates the lease
  path belongs to the project, heartbeats it, and marks `borrowedLease` (`borrowed_lease.go:13-31`);
  `Run` heartbeats rather than re-acquires (`daemon.go:519-524`); `releaseRunLease` no-ops for a
  borrowed lease (`borrowed_lease.go:33-38`); the composition root's `defer releaseLease()`
  (`cli/daemon.go:61`) is registered first, so LIFO runs it **after** every store/ledger close. Lease
  outlives all writers, exactly as the contract requires.
- **Restore avoids the D3 in-place hazard entirely.** It does **not** use the engine's
  `RestoreBackup`; it stages into a fresh `dest/.qompack.restore-<id>-<rand>/.qompack`, streams +
  re-hashes every file, proves a read-only reader, re-hashes again, then publishes via an atomic
  no-replace directory rename (`maintenance.go:175-220`). Malicious-manifest input is hardened
  strongly: `maintValidateName` (no traversal/abs/backslash/`:`/reserved-name/control/trailing-dot),
  `maintNoFollow` (rejects symlink/reparse components), `maintWithin` (defence in depth), bounded
  sizes, exact + case-fold duplicate detection, SHA and version pinning. Failures preserve the staging
  tree as evidence.
- **Restore proof is non-vacuous and honestly scoped.** `proveReader` reads back every content root
  (chunk-verified on read) and refuses `ContentRootsProven == 0` with `ErrRestoreVacuous`
  (`maintenance.go:405-407`); `RestoreProof` declares `SameBuildOnly` and `CheckpointSealCovered:false`,
  and the CLI runs `fsckScanProject` on the published destination afterwards (`backup.go:131-135`),
  covering the checkpoint/seal integrity the reader proof does not.
- **Publication audit is bounded and correctly positioned.** Shared entry + byte budgets, per-file
  read limits, fixed two-level fanout (no `WalkDir`), symlink-refusing, `os.OpenRoot`-confined opens,
  `ctx` checked per batch; `Incomplete`/`Truncated` never let a zero count read as "clean"; the report
  is counts/booleans/fixed-vocabulary only. It is wired at `daemon.go:605` **after** the startup drain
  and **before** serve, under a 250 ms timeout — the position the design specified — and `Observed`
  distinguishes "can't audit" from "clean."

---

## 2. R1 — Blocker candidate: a lost-lease backup is left fully verifiable but uncertified

**Where:** `maintenance.go:111-130` (`TakeBackup` wrapper) over `backup.go:186-302`.

**Mechanism.** The engine's `TakeBackup` writes the manifest **last** (`backup.go:294-298`) with
`Consistent: true` set unconditionally (`backup.go:217`), and returns it successfully. The maintenance
wrapper then re-checks the lease *after* the copy; if it is lost, it returns
`ErrWriterLeaseRequired` **without** the manifest and warns "artifact backup/<id> is preserved … but
is NOT certified" (`maintenance.go:125-128`). But the on-disk artifact is by then a **complete,
internally self-consistent backup**: manifest present, every file's size/digest matches the manifest
(they were computed from the same bytes). A later `qompack backup verify --id <id>` re-hashes files
against that manifest and **passes** (`maintenance.go:280-294`). The "taken while a replacement writer
may have been mutating the store" taint lives only in the transient error string of the failed
`create` command — **nowhere in the durable artifact.**

**When reachable.** The post-copy lease check fails only if ownership was lost during the copy, which
(given the 30 s heartbeat ticker in `acquireWriterLease`) means the backup process stalled ≥ 90 s and
another process reclaimed the stale lock and started writing. `refuseIfTheProjectMoved` is *not* a
sufficient backstop: it re-checks only the four delivery-seal files (`backup.go:304-320`), so a
replacement daemon that appended `objects/**`/`index/*.jsonl` without a seal write would pass it and
still tear the snapshot. The lease post-check is the belt to that suspenders — and its verdict is not
made durable.

**Consequence.** An operator who sees `create` fail, later runs `verify`, sees it pass, and trusts a
possibly-torn backup. A backup is supposed to be evidence; an uncertified one must not be able to
masquerade as certified.

**Fix (recommended).** On post-copy lease loss, `os.RemoveAll(backupDir(id))` and free the id — mirror
the `ErrBackupMoved` path (`backup.go:278`), which already removes a provably-unusable tree. Or, if the
artifact must be retained for inspection, record the lease-confirmation outcome **in the manifest**
(e.g., `Certified bool`) so `verify` surfaces "uncertified" rather than passing. Returned as **Q1**.

---

## 3. Lower-severity findings (R2–R6)

**R2 — Create path buffers whole files; no per-file/byte bound (Low–Medium, durability/memory).**
`TakeBackup` walks `.qompack` and reads each file whole with `paths.ReadFileShared` then
`paths.WriteAtomic(dst, b)` (`backup.go:247-257`) — no streaming, no per-file size ceiling, no total
byte budget, unlike the maintenance verify/restore paths which stream through `maintStreamCopyHash`
bounded by `maintMaxFileBytes`. A single large file in `.qompack` (a `KeepRaw` original, a 64 MiB WAL
segment, a large log) allocates whole. This is the engine's pre-existing behaviour reused unchanged;
it is not a correctness bug but is asymmetric with the hardened restore side. Recommend a streamed,
bounded copy for the create path before frozen validation. Returned as **Q3**.

**R3 — Source store opened writable for verify/restore that never use it (Low, trust/doc).**
`backup.go:101` opens the source store with `store.Open` (writable) for all three actions. Only
`create` needs it (for `m.s.Flush`); `verify` reads the backup tree, and `restore` reads the backup
tree + proves the *staged* store via `OpenReadOnly` — neither touches the live source store. A
writable `Open` may perform work on open (recovery, index load, tmp cleanup, lock), so the header
claim "the source live tree is never touched (D-D)" (`maintenance.go:35`) is overstated for
verify/restore. Under the exclusive lease this is safe from concurrency, but recommend opening the
source **read-only** for verify/restore and qualifying the "never touched" wording. Main is already
aware. Returned as **Q2**.

**R4 — `NewMaintenance` builds `*Migrator` by direct struct literal (Low, maintainability).**
`maintenance.go:104` constructs `&Migrator{s, root, l, src:nil, batch, clock, cfg}` rather than the
shared unexported builder the design proposed. It is **correct today** — the literal covers every
field `TakeBackup`/`VerifyBackup`/`RestoreBackup` read, and `cfg` is guarded non-default via
`ErrMaintenanceConfig` (the D4 fix) — but a future field that `NewMigrator` initializes
(`migrate.go:319-328`) would be silently zero here. Recommend the tiny shared builder so the two
constructors cannot drift.

**R5 — Stale publish lock can wedge future restores (Low, availability).**
`maintPublish` creates `dest/.qompack.publish.lock` `O_EXCL` and removes it on defer
(`maintenance.go:582-592`). A crash between create and the deferred remove leaves the lock behind, and
every later restore into that dest then fails `ErrRestoreTargetExists` "already publishing" with no
staleness reclaim. Restore is a rare manual op and cleanup is a single file delete, but consider a
staleness/owner check or documenting the manual cleanup.

**R6 — Restore fails closed on filesystems/platforms without atomic no-replace (Low, portability).**
Correct and intentional: Linux `Renameat2(RENAME_NOREPLACE)` returns EINVAL/ENOSYS on unsupporting
kernels/filesystems, and `rename_noreplace_other.go` returns `ErrUnsupported`. This is the right
fail-closed posture (no racy `os.Rename` fallback), but it means restore is **unavailable** on some
Linux filesystems and all non-{win,linux,darwin} platforms; ensure that limitation is documented for
operators rather than surfaced only as a runtime error.

**Config-sentinel dependency (note, not numbered).** `NewMaintenance` gates on
`o.Cfg.Runtime.Migration.SettingsVersion != 0` as its "explicit config" proxy (`maintenance.go:89`).
It fails closed if zero (safe), but a config with a nonzero `SettingsVersion` yet otherwise-default
chunk sizes would pass and defeat the D4 intent. Given the known `LoadForCapture` no-fallback defect
tracked for V6-VERIFY, confirm the loader always stamps a real `SettingsVersion` on the path
`runBackup` uses (`backup.go:87-95`).

---

## 4. Things I checked that are NOT problems

- **Publish TOCTOU.** `maintRefuseExistingDot` (Lstat, detects symlink/reparse) followed by an
  *atomic* no-replace rename closes the check-then-act window; the rename itself refuses an existing
  dest, so a race between the two only yields a clean `ErrRestoreTargetExists`.
- **`maintCopyVerify`'s `os.Rename`** (`maintenance.go:496`) is a temp→dst move *within* fresh
  staging (first write, same dir), not the publish rename — no overwrite risk.
- **Reader-proof handle lifetime.** `proveReader` closes the read-only store before `maintPublish`
  runs, so no open handle blocks the directory rename (would have failed on Windows otherwise).
- **CLI defer ordering.** Report emission is deferred first (`backup.go:59`) so it runs last, after
  `s.Close()` (105) and `release()` (100); a failed flush or lost lease cannot ride out on a
  success result. Correct.
- **Windows `windows.MoveFile`** (no `MOVEFILE_REPLACE_EXISTING`) inherently refuses an existing
  destination — correct no-replace semantics.

---

## 5. Questions returned to main (shared durable-data / contract)

- **Q1 (R1, recommended before freeze).** On post-copy lease loss, should the artifact be
  removed/quarantined (mirroring the `ErrBackupMoved` RemoveAll), or should the lease-confirmation
  outcome be recorded durably in the manifest so `verify` cannot pass an uncertified backup? Today it
  is left fully verifiable with the taint only in a transient error.
- **Q2 (R3).** Open the source store **read-only** for `verify`/`restore`, and qualify the
  "source live tree is never touched" claim to "not mutated by maintenance operations"?
- **Q3 (R2).** Is whole-file buffering on the create path acceptable for the expected `.qompack` size
  distribution, or should `TakeBackup` be streamed/bounded (matching restore) before frozen
  validation?
- **Q4 (R5).** Publish-lock staleness policy after a crash mid-publish — accept documented manual
  cleanup, or add a staleness reclaim?

## 6. Non-acceptance

Source review only; nothing is marked PASS and no gate is cleared. The maintenance capability closes
recovery-design D1/D2/D3/D5 and is well hardened; R1 is a genuine trust/durability gap that should be
resolved (Q1) before the frozen full-validation matrix, and R2–R6 are quality/robustness
improvements. What I reviewed is the maintenance wrapper, the CLI/daemon lease wiring, the atomic
no-replace primitives, and the publication audit; I did not exercise them at runtime and did not
re-audit the engine's unused `RestoreBackup`/rollback-drill paths.

# V6 remediation — critical resolution review (fixes for prior findings)

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high, same session
`788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source review only — no tests, builds, Git, config, permission/auth/billing changes.
Findings read from code, not from work records. Only output: this file.

**Read:** `internal/store/maintenance.go`, `internal/store/backup.go` (TakeBackup),
`internal/cli/backup.go`, `internal/cli/capture_admission.go`, `internal/hookio/capture_scope.go`,
`internal/daemon/handlers.go` (admitDelivery / scopeSupplied / scopeRefusal),
`internal/daemon/drain.go` (Failed handling), `internal/observer/tooluse.go` (scope defence),
plus `internal/daemon/capture_scope_v6_test.go` for pinned expectations. Prior review:
`recovery-review.md`.

---

## 1. Verdict — the four recovery fixes and the capture-scope work all hold

| Item | Status |
|---|---|
| R1 durable certification marker | **Resolved** (§2.1) |
| R2 fully streamed create + restore, incl. protected logs | **Resolved** (§2.2) |
| R3 nil source for verify/restore | **Resolved** (§2.3) |
| R5 atomic no-replace, no stale publish lock | **Resolved** (§2.4) |
| V6-AUTH-1 capture-time scope (raw-before-redaction, duplicate paths, lifecycle vs producer) | **Sound** (§3) |
| V6-HOST-1 native Read policy | **Correctly NOT claimed fixed** (§4) |

No new blocker. Six low-severity residuals/nits remain (§5), one of which (F2) is a coupling
assumption worth a test. This review grants no runtime acceptance.

---

## 2. Recovery fixes

### 2.1 R1 — lease-loss taint is now durable

`TakeBackup` writes `.<id>.certification-pending` via `CreateNew` **before** the engine can publish
its manifest, and removes it only after the post-copy lease re-check succeeds
(`maintenance.go:163-180`). `readValidatedManifest` refuses any backup whose pending marker still
exists or cannot be `Lstat`-ed (`maintenance.go:292-294`), so both `VerifyBackupContext` and
`Restore` reject an uncertified artifact — closing the R1 gap where a possibly-torn backup verified
clean. `readValidatedManifest` also now requires `man.Consistent` (`:313`). Marker/backup-dir names
cannot collide (a marker starts with `.`, an id may not — `maintValidateID`).

*Residual F4 (low, no data loss):* a crash between a successful backup and the `os.Remove(pending)`
(or a `Remove` failure, `:177-178`) leaves a genuinely-complete backup permanently unverifiable, and
there is no operator path to clear a stale pending marker for a backup that is in fact consistent.
Fail-safe, but consider documenting manual removal or a re-certify step.

### 2.2 R2 — creation and restore are streamed; protected logs handled

Creation now routes through `m.copyBackupFile` (`backup.go:250-261`), set by `NewMaintenance` to
`maintCreateCopy`, which streams via `maintStreamCopyHash` under `O_EXCL` + `Sync` with a per-file
`maintMaxFileBytes` ceiling (`maintenance.go:108-142`) — no whole-file buffering. The legacy
`ReadFileShared`+`WriteAtomic` path survives only as the `copyBackupFile == nil` fallback. Restore's
`stageRestore` streams **every** file through `maintCopyVerify` (`maintenance.go:390`), which
`O_EXCL`-creates, streams, `Sync`s, verifies against the manifest, then `chmod 0444`s **immutable**
protected artifacts while leaving `.jsonl` logs appendable (`maintenance.go:528-536`). Protected logs
are thus restored byte-identically and stay writable — R2's specific ask.

### 2.3 R3 — verify/restore no longer open the source store

`NewMaintenance` accepts `s == nil`, skipping the source mkdir and building a Migrator with `s: nil`
(`maintenance.go:85-107`); `TakeBackup` refuses `x.m.s == nil` (`:157`). The CLI opens `store.Open`
**only** for `create` (`backup.go:101-108`); verify/restore pass a nil store. The header claim "the
source live tree is never touched (D-D)" is now accurate for verify/restore. (`VerifyBackupAt`
provides a config-free pure-verify entry, `maintenance.go:210-213`.)

### 2.4 R5 — no publish lock to go stale

`maintPublish` dropped the `.qompack.publish.lock`; it now `maintRefuseExistingDot`s then calls
`paths.RenameDirectoryNoReplace` as the sole, atomic arbiter (`maintenance.go:617-624`). No lockfile
can wedge a retry after a crash. Concurrent restores into one dest race only on the atomic rename;
the loser gets `ErrRestoreTargetExists` and its staging tree is preserved as evidence.

---

## 3. Capture-time path scope (V6-AUTH-1) — sound, with the three flagged risks checked

The boundary is `hookio.CaptureScope` / `CaptureScopeRaw`: every structured path (`file_path`,
`path`, `notebook_path`, recursively `edits[].file_path`) is checked both lexically (`paths.Norm`)
and physically (`paths.ResolvesInside`); a proven escape → `ScopeOutOfProject`; a file-content
producer that dropped its path, or any malformed/ambiguous input → `ScopeUnprovable`; a
pathless-by-nature producer → `ScopeAllow` (`capture_scope.go:83-120`). It is enforced at three
layers, all **before** anything servable persists.

- **Raw-before-redaction — addressed.** The hook client scopes the derived Event's input *and* the
  original raw envelope, dropping bytes and clearing the Event on refusal **before** the request is
  spooled/sent (`capture_admission.go:149-168`, `scopeGuardCapture`). The daemon runs `scopeRefusal`
  **before** the redaction policy on the no-capture path (`handlers.go:1274-1279`) and re-scopes a
  client-supplied OK/degraded capture — Event, `Capture.Bytes` **and** `Raw` — because scope is not
  trusted from the client (`handlers.go:1237-1257`, `scopeSupplied` `:1330-1343`). The observer is
  the last line, refusing before `PutBytes` (`tooluse.go:103-107`).
- **Duplicate paths — addressed.** `structuredPaths` / `rawToolFields` use a strict streaming decoder
  that fails closed on a duplicate structured key or a duplicate `tool_name`/`tool_input`
  (`capture_scope.go:205-210,267-273`) and on any trailing garbage (`closesCleanly`). Because a
  decoded `Event.ToolInput` can silently collapse duplicate keys, every site *also* scopes the raw
  envelope, so a key-collapse cannot launder an out-of-scope path.
- **Missing/null lifecycle vs file producer — addressed.** Null/empty input is well-formed "no paths"
  (`:153-155`); a producer in `fileContentProducers` with no provable path → `ScopeUnprovable`
  (refuse), a producer outside it → `ScopeAllow` (preserve). `capture_scope_v6_test.go:152-154`
  ("a pathless producer is preserved") and `:138-140` ("a genuine in-project OK capture is admitted
  unchanged") pin that lifecycle/pathless captures are **not** lost.
- **No poison-pill / stall.** A `ScopeUnprovable` maps to `Failed`, and `Failed` is **terminal** in
  the drain — the offset advances past the record, a `DrainGapUnadmitted` gap is counted and LOUD-ed,
  and the drain proceeds (`drain.go:477-498`). A deterministically-unprovable record is skipped
  loudly, not redelivered forever.

---

## 4. V6-HOST-1 — not claimed fixed (correct)

The scope boundary enforces **project containment** (`Norm` + `ResolvesInside`), and the code frames
it precisely as a path-scope trust boundary against the project root, never as the host's native
per-path Read deny policy (`capture_scope.go:11-22`). No false PASS is asserted. The product decision
(authority-review §6, options A/B/C) remains open and is not this change's to resolve.

---

## 5. Residual findings (low; exact file, concrete need)

- **F1 (low, residual).** `internal/daemon/ingest.go` `Accept` appends the raw line to the WAL/spool
  **before** admission runs, so a *direct-IPC* caller (not the hook client, which pre-scopes in
  `capture_admission.go:scopeGuardCapture`) can transiently place out-of-project bytes in the spool
  until drain/dispatch admission refuses and skips them. Durable sidecar/object are protected and the
  WAL is transient (skipped, offset advanced, removed); the production hook path is clean. *Need:* a
  one-line acknowledgment that the spool-level guarantee holds only for the hook path.
- **F2 (low, coupling to pin with a test).** `scopeSupplied` / `scopeGuardCapture` scope
  `Capture.Bytes` via `CaptureScopeRaw` → `rawToolFields`, which admits only a single complete
  unambiguous JSON **object**. A legitimate capture whose retained bytes were valid JSON but not the
  top-level hook-envelope object would be refused as `Unprovable` and its evidence dropped. Holds for
  the shipped hook path (bytes *are* the hook object); *need:* a test pinning that assumption so a
  future capture-shape change cannot silently start dropping in-project evidence.
  (`internal/hookio/capture_scope.go`, `internal/daemon/handlers.go:1336-1337`,
  `internal/cli/capture_admission.go:204`.)
- **F3 (nit).** `internal/hookio/capture_scope.go:81` calls Grep/Glob "pathless by nature," but they
  carry a structured `path` and *are* scoped by it (an out-of-project Grep path is correctly Denied).
  The comment understates the actual, better behavior.
- **F4 (low, §2.1).** No operator path to clear a stale certification-pending marker on a
  genuinely-consistent backup. `internal/store/maintenance.go:177-185,292-294`.
- **F5 (low).** `stageRestore` runs `paths.EnsureLayout` **after** the verified copy
  (`maintenance.go:394`); anything EnsureLayout creates is outside `verifyStaged`'s manifest re-hash
  and is published unverified. Confirm EnsureLayout only `MkdirAll`s (no file content) or fold its
  outputs into verification.
- **F6 (nit).** `maintCopyVerify` re-derives the staging root by string-searching `/.qompack/`
  (`maintenance.go:530-533`) to decide the protected-file chmod, instead of receiving the staging
  root it already knows at the call site. Correct today, fragile. Pass it explicitly.

---

## 6. Non-acceptance

Source review only; nothing is marked PASS and no gate is cleared. R1/R2/R3/R5 are correctly
resolved, and the capture-scope work soundly implements the authority-review §5/§7 capture-time
recommendation with matching test coverage and no stall/poison-pill. The residuals above are
low-severity; F2 is the one worth a pinning test before frozen full validation. V6-HOST-1 stays an
open, honestly-stated product decision, not a claimed fix.

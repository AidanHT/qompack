# V6 remediation — packaged backup/restore regression (authoring record)

**Owner:** V6 recovery packaged-test author (Opus 4.8, high effort).
**Scope (exclusive):** new `test/fault/v6_backup_test.go` and this file only. No store/CLI/daemon/config
code, no Git, no other file was touched. **Not built or run** — per the brief, main runs the fault
suite after the capture author finishes. This note records the exact commands, env, and inspected JSON
contract so main can run it as-is.

## What was authored

Three bounded packaged cases in `test/fault/v6_backup_test.go` (`package fault`), each driving the
REAL bundled binary via the existing helpers `assembledBundle`, `newProject`, `seedSession`,
`recoverSession`, `run`, `shutdownIfReachable`, `requireNoOrphan`, `objectFingerprints`,
`changedObjects`, and recording evidence via `newRecord`/`writeRecord` (Phase `lifecycle`).

1. **`TestV6Backup_RoundTripPreservesSourceLaterWritesAndBackup`** — seed a store with the real hooks;
   stop the daemon; `backup create` then `backup verify`; snapshot the source objects + the original
   manifest bytes; make a genuine later write via `recoverSession` (new `gamma` content); stop; then
   `backup restore` into a fresh, isolated destination. Asserts:
   - restore exit 0; `restore.OpenedOK`, `restore.SameBuildOnly` true; `restore.CheckpointSealCovered`
     false; `restore.ContentRootsProven >= 1` (non-vacuous same-build read proof);
   - `integrity` (the CLI's `fsck --seal-check` report) present with `exit == 0`;
   - **point-in-time**: `objectFingerprints(dest) == preLater` (the backed-up objects, NOT the later
     write);
   - **source retained**: `changedObjects(source_now, source_after_later)` empty, and the later write
     is still present;
   - **original backup unchanged**: `backup/before/manifest.json` bytes identical after restore.
   Outcome `recovered`.

2. **`TestV6Backup_RefusesLiveWriterAndTargetConflict`** — with the daemon UP, `backup create` must
   refuse (the CLI acquires the daemon lease before opening the store) with an error naming the daemon,
   and create **no** `backup/live` artifact and change no object. Then, daemon stopped and a valid
   backup taken, `backup restore` into a destination that already holds a `.qompack` must refuse
   (error contains "exist") and leave the occupied destination's sentinel byte-for-byte intact.
   Outcome `explicit_incomplete`.

3. **`TestV6Backup_CorruptedAndUnknownSchemaRefusedWithoutOverwriting`** — take a valid backup, then
   (a) tamper one backup-tree file and (b) bump the manifest `version` to 999; each restore, aimed at
   its own fresh destination, must exit non-zero and publish **no** destination `.qompack`. Outcome
   `explicit_incomplete`.

## Exact commands and env (as the test issues them via `run`)

`run(t, b.Bin, <cwd=source root>, <args>, nil, p.Env)` where `p.Env = {QOMPACK_PROJECT_ROOT=<source>,
HOME, USERPROFILE}` and `run` first strips inherited `QOMPACK_*`/`CLAUDE_*`.

```
backup create  --project <source> --id before --json
backup verify  --project <source> --id before --json
backup restore --project <source> --id before --destination <fresh> --json
backup create  --project <source> --id live   --json     # refused while the daemon is up
backup restore --project <source> --id before --destination <occupied> --json   # refused
backup create  --project <source> --id good   --json
backup restore --project <source> --id good   --destination <fresh> --json       # corrupt / schema: refused
```

Selection: run the three by name, e.g.

```
GOMAXPROCS=2 go test -run '^TestV6Backup_' -count=1 ./test/fault
```

Optionally set `QOMPACK_FAULT_ARTIFACTS=<dir>` to collect the three JSON evidence records
(`v6_backup_roundtrip_preserves_source.json`, `v6_backup_refuses_live_writer_and_target_conflict.json`,
`v6_backup_corrupt_or_unknown_schema_refused.json`) plus `INDEX.json`.

## Inspected JSON contract (from `internal/cli/backup.go`, main-owned — read only)

`backupReport`: `{schema,int; action; id; manifest?(store.BackupManifest, json tags: version/id/
root/taken_at/consistent/snapshot_id/frontier/files[]{name,size,sha256}); restore?(store.RestoreProof);
integrity?(fsckReport: schema/exit/checks[]{id,ok,...}); error}`. On refusal the command still emits the
JSON with `error` set and exits non-zero (the `defer` encodes after the lease/writer defers).

`store.RestoreProof` carries **no JSON tags**, so its keys are the Go field names
(`BackupID`, `Destination`, `OpenedOK`, `ContentRootsProven`, `ToolRefsProven`, `SameBuildOnly`,
`CheckpointSealCovered`, `Note`); the test's `restoreProofJSON` uses the same field names (encoding/json
matches case-insensitively). The restore report's `integrity` is `fsckScanProject(dest, repairing=false,
sealCheck=true)` — i.e. the packaged **fsck --seal-check** over the destination, so exit 0 there is the
checkpoint/seal integrity the same-build content proof deliberately does not itself cover.

Refusal wiring confirmed: `runBackup` → `acquireWriterLease(root)` → `daemon.AcquireLock`, which fails
while a daemon holds the lock and yields `"backup: stop the source daemon before maintenance: …"`;
target/corrupt/schema refusals come straight from `store.Maintenance` (`ErrRestoreTargetExists`,
`ErrBackupCorrupt`, `ErrBackupManifest`).

## Same-build discipline

No case asserts old-release/cross-version reader compatibility. The proof is the running build reading
its own restore, and `CheckpointSealCovered:false` plus the separate `fsck --seal-check` integrity are
the honest split.

## Notes for main when running

- Packaged cases spawn the real detached daemon; timing is handled by `seedSession`/`waitDaemonUp`/
  `shutdownIfReachable`, not by sleeps. Each project uses its own `newProject` temp base (isolated).
- The round-trip's `integrity.exit == 0` assertion assumes a freshly restored store passes packaged
  `fsck --seal-check`; if seal-check surfaces a benign row on a quiet restored store, that is a real
  finding for main to adjudicate, not a test-authoring error.
- The live-writer case depends on `backup create` reaching `acquireWriterLease` and failing while the
  seeded daemon still holds the lock; `seedSession` leaves it up and the test re-affirms with
  `waitDaemonUp`.

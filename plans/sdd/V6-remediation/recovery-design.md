# V6 remediation — supported operator backup / verify / restore design

**Owner:** V6 recovery implementation-design (Opus 4.8, high effort).
**Status:** design + inspection only. No production code, tests, builds, Git, configs, or
permission/auth/billing posture were changed. This document is additive; `plans/V6-report.md`
remains immutable history.
**Target defect:** V6-RECOVERY-2 (`plans/V6-report.md` §5) — *"operator backup/restore handoff
incomplete … production `LegacyImportGate` is closed and no supported operator command performs that
flow."*
**Sources inspected:** `internal/store/backup.go`, `internal/store/migrate.go`,
`internal/config/migration.go`, `internal/cli/dispatch.go`, `internal/cli/fsck.go`,
`internal/store/store.go`, `internal/store/lifecycle.go`, `internal/store/recovery_test.go`,
`internal/paths/{layout,appendonly,atomic,shared}.go`, `internal/daemon/{lock,mcpop,spawn}.go`,
`test/e2e/install_test.go`, `test/e2e/install_helpers_test.go`, `docs/uat.md` (UAT-12),
`docs/troubleshooting.md` §9.

---

## 1. Headline recommendation

**Yes — the backup engine can and should be separated from the legacy-import gate with a new,
additive *maintenance* constructor, and this is the correct minimal fix for V6-RECOVERY-2.**

The backup / verify / restore surface has **zero dependency on `LegacySource` and no dependency on
the `LegacyImportGate`**. The gate exists to guard the *mutating* half of `internal/store/migrate.go`
(import → parity → cutover → writer handoff), which changes the destination store and transfers the
single-writer role. Backup/verify/restore do none of that: a backup is a byte copy under a lease, a
verify re-hashes, and a restore writes into a *separate* destination root. Coupling them to the
import gate is what makes the promised recovery workflow unreachable.

The fix is a small, additive, supported *maintenance capability* — not a claim that migration passed,
and it must **not** open `LegacyImportGate`. It exposes point-in-time backup, verification, and
restore-to-a-fresh-root of the *current engine's* store, and proves a **same-build** reader over the
restored tree. Cross-version / old-release reader compatibility and cutover stay behind the closed
import gate exactly where they are.

---

## 2. Source boundaries (what is gated, what is not)

`store.Migrator` fuses two concerns that have different safety profiles:

| Concern | Methods | Touches `m.src`? | Mutates live store? | Needs the gate? |
|---|---|---|---|---|
| **Maintenance (safe/additive)** | `TakeBackup`, `VerifyBackup`, `RestoreBackup` | No | No¹ | **No** |
| Rollback drill | `RehearseRollback` | No | No (restores to scratch) | No, but see §5/§8 |
| **Migration (mutating/gated)** | `Import`, `Parity`, `Cutover`, `AcquireWriter`(handoff side), `RecordNewFormatWrite`, `semanticParity` | **Yes** | **Yes** | **Yes** |

¹ On an ordinary (never-migrated) project `TakeBackup` makes **no durable mutation** of live content:
`retainFrontier` reads an empty mapping log, so `retainRoots` is called with zero hashes and
`AppendRetentionRoot` is never invoked (`backup.go:290`, `migrate.go:625-635`, `lifecycle.go:246`,
`migrate.go:348-359`). Its only live-side effects are `s.Flush(ctx)` (durability, not mutation) and a
transient `migrate/writer.lock` it creates and removes.

The single hard coupling is the constructor:

```
// internal/store/migrate.go:303-306
func NewMigrator(s Store, root string, o MigrateOptions) (*Migrator, error) {
    if !o.Gate.Passed {
        return nil, fmt.Errorf("%w: %s", ErrMigrationGateClosed, config.LegacyImportGateKey)
    }
    ...
```

`LegacyImportGate()` ships `Passed:false` and has **no config leaf** (`config.LegacyImportGateKey =
"store.migrate.legacyImportCutover"`, `migration.go:65-103`), so nothing short of a reviewed
build-flag flip — or a test constructing a passed gate (`install_helpers_test.go:1338-1339`) — can
build any `Migrator` at all. That is why `TakeBackup`/`RestoreBackup` are unreachable from any
command (`docs/troubleshooting.md` §9; `docs/uat.md` §UAT-12).

---

## 3. Actual signatures (verified against source)

Backup engine (`internal/store/backup.go`):

```
func (m *Migrator) TakeBackup(ctx context.Context, id string) (BackupManifest, error)   // :186
func (m *Migrator) VerifyBackup(id string) (BackupManifest, error)                      // :352
func (m *Migrator) RestoreBackup(id, dest string) error                                 // :395
func (m *Migrator) RehearseRollback(ctx context.Context, o RollbackOptions) (RollbackDrill, error) // :504
func (m *Migrator) refuseIfTheProjectMoved(man BackupManifest) error                    // :325 (unexported)
func BackupWatchedFiles() []string                                                      // :117
```

Constructor and options (`internal/store/migrate.go`):

```
func NewMigrator(s Store, root string, o MigrateOptions) (*Migrator, error)  // :303
type MigrateOptions struct { Source LegacySource; Gate config.MigrationGate;
                             Batch int; Clock core.Clock; Cfg config.Config } // :258
```

Store seam and layout the maintenance path needs (all already exported):

```
func store.Open(root string, cfg config.Config, deps store.Deps) (store.Store, error) // store.go:100
type store.Deps struct { ... Clock core.Clock; Log logging.Logger; ... }              // store.go:70
store.Store: Open(ctx, h) (io.ReadCloser,…) · GetRoot(ctx,h) · Flush(ctx) · Close()
func paths.Of(root string) paths.Layout                    // Layout{Root,Dot,Migrate,Backup,State,Objects,Index,…}
func paths.EnsureLayout(l paths.Layout) error              // :67
func paths.IsProtected(root, p string) bool                // appendonly.go:25
func paths.RestoreLog(p string, b []byte) error            // appendonly.go:196
func paths.CreateNew(p string, b []byte) error             // appendonly.go:163  (os.ErrExist if present)
func paths.WriteAtomic(p string, b []byte, perm fs.FileMode) error // atomic.go:125
func paths.ReadFileShared(p string) ([]byte, error)        // shared.go:45
func daemon.LockPath(projectRoot string) string            // lock.go:148
```

CLI dispatch entry shape a future `qompack backup` would register into (`dispatch.go:36-46`):
`Cmd{Name, Summary, Hook:false, Run func(ctx, env Env, args []string, out, errw io.Writer) error}`.
`cli` is a composition root and may import `store`, `daemon`, `paths` (`dispatch.go:10-11`).

---

## 4. Recommended maintenance construction

Add an **additive** constructor and a **narrow wrapper type** in `internal/store` that reuse the
existing engine unchanged. Do not add a config leaf; the constructor itself is the authorization
boundary, and it is safe to leave open because every method it exposes is non-mutating to live
content and restores only into a caller-named *separate* destination.

```
// PROPOSED — internal/store, additive only.
type MaintenanceOptions struct {
    Clock core.Clock       // nil → core.SystemClock()
    Cfg   config.Config    // config a RESTORED store is opened with; zero → the project's config, not Defaults()
}

// NewMaintenance builds a backup/verify/restore capability over an existing store WITHOUT the
// legacy-import gate and WITHOUT a LegacySource. It is deliberately gate-free: none of its methods
// import legacy data, transfer the writer, or mutate live content.
func NewMaintenance(s Store, root string, o MaintenanceOptions) (*Maintenance, error)

type Maintenance struct { m *Migrator }   // holds an unexported *Migrator built with src=nil, gate bypassed

func (x *Maintenance) TakeBackup(ctx context.Context, id string) (BackupManifest, error)
func (x *Maintenance) VerifyBackup(id string) (BackupManifest, error)
func (x *Maintenance) RestoreBackup(id, dest string) error
func (x *Maintenance) VerifyRestore(ctx context.Context, id, scratchRoot string) (RestoreProof, error)
```

Key properties:

- **The wrapper is the safety boundary.** `Maintenance` holds the `*Migrator` *unexported* and does
  **not** embed it, so `Import`/`Parity`/`Cutover`/`RecordNewFormatWrite`/`AcquireWriter`(as handoff)
  are unreachable through the maintenance surface. The migrator it wraps is built with `src == nil`;
  any source-using method would nil-panic, but none is exposed, so that is unreachable, not latent.
- **No new mutation of `migrate.go`'s gated path.** `NewMaintenance` reuses `NewMigrator`'s body only
  for directory creation (`os.MkdirAll` of `l.Migrate`, `l.Backup`) and field wiring; it must **not**
  route through the `!o.Gate.Passed` refusal. Prefer a tiny shared unexported builder both
  constructors call, so `NewMigrator` keeps its gate check verbatim and `NewMaintenance` skips only
  that one clause.
- **`VerifyRestore`, not `RehearseRollback`, for the maintenance proof.** See §5 and §8 — the
  rollback drill's reader proof is *vacuous* on a non-migrated project and would over-claim.

---

## 5. The four requested guarantees, grounded in the code

### 5.1 Writer exclusion

Two distinct exclusion mechanisms exist and they are **not** the same thing:

- `AcquireWriter(h.Owner)` (`migrate.go:883`) creates an exclusive `migrate/writer.lock`. **This only
  excludes another `Migrator`.** The real store writer in production is the **daemon** — *"The daemon
  is the single writer of the store"* (`daemon/mcpop.go:19`) — and the daemon takes **no**
  `migrate/writer.lock` (it takes the daemon lock, `daemon/lock.go`). Hook-driven writes spool to a
  WAL and are drained by that daemon.
- `refuseIfTheProjectMoved` (`backup.go:325`) is the real consistency backstop: after the copy walk it
  re-reads the four delivery-seal files (`BackupWatchedFiles()`) and returns `ErrBackupMoved` unless
  each still holds exactly the captured bytes.

**Consequence / requirement.** `TakeBackup`'s "single writer lease … so no other STORE writer is
running" (`backup.go:163-171`) is literally true only in the legacy-writer/importer model. On an
ordinary project a live daemon can be appending `objects/**` and `index/*.jsonl` under the walk; those
are tolerated as "append-only tails a reader steps over," so the backup is only *crash-consistent*
for the delivery seals, not a quiesced snapshot. **The maintenance operation must therefore require
the daemon stopped** and enforce it, rather than trust the migrate lease. Recommended enforcement,
mirroring `fsck --seal-check`/`--repair` (`fsck.go:198-217`, `459-467`): before `TakeBackup`, probe
`daemon.LockPath(root)`; if a live daemon holds it, refuse with a "stop the daemon and retry"
message (the same shape `installStopWriters` uses, `install_helpers_test.go:1370-1377`). `refuseIfThe
ProjectMoved` remains the backstop for a race the probe misses.

### 5.2 Rollback before / after the first new write

The engine already distinguishes the two phases (`RollbackPhase`, `backup.go:438-445`) and the drill
is genuinely different in each (`RehearseRollback` steps 3–4, `backup.go:541-594`). **These phases are
meaningful only after a cutover**, because "after the first new-format write" requires
`RecordNewFormatWrite`, which refuses unless `Handoff.Owner == WriterQompack` (`migrate.go:1005-1007`)
— i.e., a completed cutover, which is gated. For the maintenance capability (no cutover), there is no
"new format" versus "old format" write: a restore is simply point-in-time. The before/after
distinction stays owned by the gated migration path and must not be re-implemented or claimed here.

### 5.3 Retention of later writes

- **Migration path (gated):** `RehearseRollback` enumerates every write made after the backup as
  `UnreadableByOldReader` (`backup.go:546-550`, `NewFormatWrite`, `migrate.go:979-989`) and asserts
  `EvidenceRetained` — nothing collected — against the live store (`backup.go:596-615`). GC cannot
  reap that material because `recordDrill`/`importOne` declare it a `RetentionRollback` root
  *before* the referencing artifact is written (`migrate.go:536-541`, `backup.go:644-664`).
- **Maintenance path (this design):** a restore is a **point-in-time snapshot**. Writes made after the
  backup are, by definition, **not** in it. The maintenance capability must state plainly that
  restoring reverts to the backup instant and that any writes after it are **lost unless the operator
  preserves them** — exactly the "move the post-run `.qompack/` aside as evidence, never delete it"
  discipline UAT-12 and `troubleshooting.md` §9 already teach (`docs/uat.md` step 8; `troubleshooting.md`
  lines 95-102, 542-554). No automatic downgrade, no silent later-write retention is promised. This is
  the honest, additive answer; anything stronger belongs to the gated cutover model.

### 5.4 Atomicity and crash-failure handling

Already sound in the engine and inherited unchanged:

- **Backup id claimed once.** A second `TakeBackup` under an id is `os.ErrExist`, never an overwrite —
  "a backup is evidence" (`backup.go:173-185`, `191-195`). A failed backup keeps its id with no
  manifest, so it can never verify or restore (`VerifyBackup` needs the manifest, `backup.go:356-359`).
  The one exception is `ErrBackupMoved`, which removes `backup/<id>` and frees the id for a clean retry
  (`backup.go:270-284`).
- **Per-file durability.** Every copied byte is hashed as written and re-hashed on verify
  (`backup.go:258-259`, `369-381`); files land via `paths.WriteAtomic` (temp + rename). The manifest is
  written **last**, after the walk, `refuseIfTheProjectMoved`, and `retainFrontier`
  (`backup.go:294-301`), so a crash mid-backup leaves an unfinished tree with no manifest — unusable as
  a backup, exactly as intended.
- **Restore ordering.** `RestoreBackup` re-verifies before writing (`backup.go:396-399`) and honors
  §7.4 protected paths (append-only logs via `RestoreLog`, artifacts via `CreateNew`,
  `backup.go:414-425`). **But see the partial-restore hazard in §7 (Defect D3).**

### 5.5 Compatible-reader proof

For the **maintenance** capability the proof must be scoped to **same-build**: restore into a fresh
scratch root, `store.Open` it with the project's config, and read content back — the property the e2e
suite already demonstrates (`install_test.go:477-501`, "the restored root … fsck exit 0 … recall
answered N hits"). `qompack fsck --project <scratch>` is the packaged form of that check (`fsck.go`;
`troubleshooting.md` §9). **Do not claim old-release / cross-version reader compatibility** — that is a
migration property, proved only by the gated drill's `readRoot` byte-comparison of every legacy id
(`backup.go:578-593`), and asserting it here would be the false "migration passed" the task forbids.
`VerifyRestore` (proposed §4) should therefore return an explicit `RestoreProof{OpenedOK,
IntegrityOK, SameBuildOnly:true}` and never a legacy-id list.

---

## 6. Focused test matrix (design-only; no tests written here)

In-package (`internal/store`), maintenance-scoped:

| # | Property | Assertion |
|---|---|---|
| M1 | `NewMaintenance` needs no gate/source | builds over a plain store with closed `LegacyImportGate`; no `ErrMigrationGateClosed` |
| M2 | Maintenance surface cannot migrate | `*Maintenance` exposes no `Import`/`Cutover`/`RecordNewFormatWrite` (compile-time / API test) |
| M3 | Backup of a never-migrated project | `TakeBackup` succeeds; manifest lists real files; `SnapshotID==""`, `Frontier` per empty cursor |
| M4 | No live mutation | live `retention-roots.jsonl` and index bytes byte-identical before/after `TakeBackup` on a plain project |
| M5 | Verify catches drift | flip one restored byte → `ErrBackupCorrupt` names the file |
| M6 | Restore to fresh root opens | `store.Open(scratch)` succeeds; `GetRoot`/`Open` return the original bytes |
| M7 | `ErrBackupMoved` path | with `afterBackupWalk` mutating a seal, backup refuses and frees the id (reuse existing hook, `migrate.go:290-296`) |
| M8 | Daemon-alive refusal | maintenance `TakeBackup` refuses while the daemon lock is held (new guard, §5.1) |
| M9 | In-place restore hazard (Defect D3) | restoring over a non-empty root with an existing protected artifact fails **before** any live write, or is rejected up-front |

E2e (`test/e2e`), through the installed launcher (extend the existing pattern, do not duplicate):

| # | Property | Reuse |
|---|---|---|
| E1 | `qompack backup` / `verify` / `restore` round-trip on a real recorded project | mirror `installImportAndBackup` + `install_test.go:477-501` but via the maintenance surface, no gate opened |
| E2 | Restored root reads through the launcher | `installFsck` exit 0 + `installRecallHashes` parity (`install_helpers_test.go:1097-1237`) |
| E3 | Survives upgrade + uninstall | fold into `TestInstall_…` (backup taken on base, restored/verified on next) |

UAT: UAT-12's recovery portion (`docs/uat.md` lines 1029-1125) becomes executable once E1/E2 land —
its `Rollback verified` row can move off `—` **only** when the same-build backup/verify/restore path,
the daemon-stopped writer exclusion, and the fresh-root reader proof are all demonstrated; the
old-release matrix stays out of scope and stays unverified.

---

## 7. Concrete defects in existing backup/restore code that block this flow

| ID | Severity | Location | Defect | Fix direction |
|---|---|---|---|---|
| **D1** | **Blocker (reachability)** | `migrate.go:303-306` | Safe maintenance ops (`TakeBackup`/`VerifyBackup`/`RestoreBackup`) are only constructible through the gated, source-requiring `NewMigrator`. There is no gate-free path, so the promised recovery workflow is unreachable (root of V6-RECOVERY-2). | Add `NewMaintenance` (§4). |
| **D2** | **High (writer-exclusion gap)** | `backup.go:163-171`, `migrate.go:883`; cf. `daemon/mcpop.go:19` | `AcquireWriter` excludes only another `Migrator`, not the daemon (the real single writer). A live daemon draining under the walk makes the backup crash-consistent only for delivery seals; other appends are "tolerated tails." The engine does not require or check daemon-stopped. | Maintenance op must probe `daemon.LockPath` and refuse while held (§5.1); document that a consistent backup requires the daemon stopped. |
| **D3** | **Blocker (in-place restore) / partial-write hazard** | `backup.go:395-431` | `RestoreBackup` overwrites non-protected files with `WriteAtomic` but refuses protected artifacts with `CreateNew` (`os.ErrExist`). Restoring into a non-empty/live root overwrites `objects/`+`index/` first, then aborts on the first existing `checkpoints/NNNN.json` or `sketches/tried.bloom`, leaving the destination **partially restored and inconsistent**. The docstring assumes a fresh dest but nothing enforces it. | Restore policy = **fresh-dir-then-swap only**; have the maintenance `restore` refuse a non-empty/established destination up-front (reuse `projectEstablished`-style check) before writing a single byte. |
| **D4** | **Low (config mismatch)** | `migrate.go:709-712`, `MigrateOptions.Cfg` (`:271`) | A restored store opens with `m.cfg`, defaulting to `config.Defaults()` when unset. A project with non-default chunk/config settings would be re-opened under defaults. Content-addressed reads still succeed, but config-derived behaviour differs. | `MaintenanceOptions.Cfg` should default to the **project's** loaded config, not `Defaults()`. |
| **D5** | **Guidance (over-claim risk)** | `backup.go:504-618` | `RehearseRollback` "before" phase on a non-migrated project has an empty mapping order, so the reader-proof loop is vacuous: `OK=true` with zero `RetainedLegacyIDs`. Using it as the maintenance "verify restore" would record a pass that proved less than it appears. | Ship a purpose-built `VerifyRestore` (open + integrity read / fsck), not the rollback drill, for the maintenance proof (§4, §5.5). |

D1 and D3 are hard blockers for a supported operator flow; D2 is a genuine semantic gap the operator
command must close; D4/D5 are correctness/anti-over-claim guidance.

---

## 8. Decisions to return to main (shared durable-data / rollback rulings)

No implementation is proposed until these are ruled. Each is a shared trust/durable-data decision the
coordinator owns:

1. **D-A — Approve the additive `NewMaintenance` constructor + narrow `Maintenance` wrapper in
   `internal/store`, gate-free, that does NOT open `LegacyImportGate`?** (Recommended: yes.) This is a
   production-code change and is therefore out of scope for *this* task; it needs main's authorization
   before any implementation subplan runs.
2. **D-B — Writer-exclusion posture: require the daemon stopped and enforce it by probing the daemon
   lock (like `fsck --seal-check`), with `refuseIfTheProjectMoved` as backstop?** (Recommended: yes.)
   Confirms the maintenance op crosses the `cli → daemon` boundary, which the composition root permits.
3. **D-C — Restore target policy = fresh-dir-then-swap only; the operator `restore` refuses an
   established/non-empty destination (Defect D3)?** (Recommended: yes; no in-place restore.)
4. **D-D — Later-write semantics: a maintenance restore is point-in-time and explicitly loses writes
   after the backup unless the operator preserves them (move-aside), with no automatic downgrade?**
   (Recommended: yes — matches UAT-12 / troubleshooting §9 discipline.)
5. **D-E — Compatible-reader scope = same-build only; the old-release / cross-version matrix stays
   behind the closed import gate and stays unverified?** (Recommended: yes — this is what keeps the
   capability honest and avoids falsely asserting "migration passed.")
6. **D-F — Whether to also expose CLI verbs (`qompack backup` / `verify` / `restore`) now, or land the
   `store.Maintenance` seam first and wire the CLI in a follow-up.** (Recommended: seam first, CLI
   second; both are additive and neither touches auth/billing/config posture.)

Once D-A…D-F are ruled, the implementation is small: one additive constructor + wrapper in
`internal/store`, an optional daemon-lock guard, one CLI command group registered in the dispatch
table, and the M/E/UAT tests in §6 — reusing the existing `install_*` harness rather than duplicating
it. Nothing here requires opening `LegacyImportGate`, changing configs, or altering
permission/auth/billing posture.

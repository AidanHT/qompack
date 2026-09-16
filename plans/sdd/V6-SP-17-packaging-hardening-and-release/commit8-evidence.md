# Commit 8 evidence — `test(e2e): rehearse installation upgrade and removal`

Evidence for acceptance rows SP17-M7-01 (installed half), SP17-M7-05 and SP17-M7-06
(unknown-schema and uninstall halves). Every row below was executed on this host against the real
`claude` CLI; nothing here is a reading of documentation.

## Host

| | |
| --- | --- |
| OS / arch | windows / amd64 (Windows 11) |
| Go toolchain | go1.26.6 |
| Claude CLI | `claude --version` -> `2.1.263 (Claude Code)` |
| Bundles | assembled twice by `go run ./tools/devtool bundle --target windows/amd64`, versions `0.1.0` and `0.1.1` |
| Records | `commit8-install-windows-amd64/` — 14 records (14 verified, 0 failed) plus `INDEX.json`, in the shape `install-record-schema.md` fixes |
| Tests | `test/e2e/install_test.go`, `test/e2e/install_helpers_test.go` |

The two versions are stamped by the test rather than resolved from `git describe`. The upgrade case
needs a SEMVER BUMP of the version it just installed, and a described version (`v0.2.0-NNN-gxxxx`)
has no successor. They name no release.

### What was rehearsed, and what it cost

Every host invocation ran with `CLAUDE_CONFIG_DIR` pointed at a fresh temp directory, with stdin
closed and a 120 s bound, and only ever as `claude plugin validate|install|update|uninstall|list`
or `claude plugin marketplace add|update` (coordinator ruling R8-1: no subcommand-less `claude`, no
`-p`, no `--plugin-dir` with a prompt, no live model session).

R8-2 is enforced by construction and then asserted: the user's real `~/.claude/settings.json` is
hashed and `~/.claude/plugins/` is listed (path, size) before the first host invocation of each
test and again after the last, and the two fingerprints must be equal. `HOME`/`USERPROFILE` are
restored to the REAL ones for every `claude` child, because `testutil.NewProject` points them at a
temp tree and a CLI resolving its own installation from there would reinstall itself mid-test.

## docs/install.md corrections

Applied to `docs/install.md` in the follow-up. The numbered list below is the observed-fact
source; the page is the corrected text. Corrections 3, 5, 6, 10 and 16 are host-output
observations (from the CLI's stdout/`--help`), not test assertions — the page must not cite the
test for them.

### Marketplace manifest

1. A plugin `source` must be a path BENEATH the marketplace root — the directory holding
   `.claude-plugin/` — written with a leading `./`. A `..` segment is refused outright, and the
   refusal names the rule: "Plugin source paths are resolved relative to the marketplace root (the
   directory containing .claude-plugin/), not relative to marketplace.json". The practical
   consequence for this repository is that the marketplace root must be the bundle directory's
   PARENT, i.e. `devtool bundle --out <dir>` and the marketplace share `<dir>`.
2. Under `--strict`, a marketplace manifest without a `description` fails: `success` is `false`
   with zero errors and one warning. A minimal manifest that passes carries `name`, `description`,
   `owner` and `plugins`.
3. `claude plugin validate <bundle dir> --strict --json` on an assembled qompack bundle reports
   `success: true`, `manifest.type: "plugin"`, and empty `errors`, `warnings` and `contents`;
   `target` is the resolved `.claude-plugin/plugin.json` path, not the directory. (Host-output
   observation, not a test assertion.)

### What an install writes

4. `enabledPlugins` keys are `<plugin>@<marketplace>` (`qompack@qompack-rehearsal`), never the bare
   plugin name.
5. `claude plugin marketplace add <dir>` writes `extraKnownMarketplaces` into `settings.json` AND
   `plugins/known_marketplaces.json`, and reports "(declared in user settings)". (Host-output
   observation, not a test assertion.)
6. Installing creates, under `CLAUDE_CONFIG_DIR`: `settings.json`, `.claude.json`,
   `backups/.claude.json.backup.<ms>`, `plugins/installed_plugins.json`,
   `plugins/known_marketplaces.json`, an empty `plugins/marketplaces/`, and the payload cache
   `plugins/cache/<marketplace>/<plugin>/<version>/`. (Host-output observation, not a test
   assertion.)
7. The payload cache holds the WHOLE bundle directory, `BUNDLE.json` and `checksums.txt` included.
   Every `checksums.txt` line verifies inside the cache, and the cached `bin/qompack.exe` is
   byte-identical to the assembled one (same sha256).
8. `plugins/data/<id>/` is NOT created by an install. It appears only once a plugin has run in a
   live session, so on a rehearsal host `--keep-data` has nothing to keep.

### Flags that exist, and flags that do not

9. Only `validate` and `list` accept `--json`. `install`, `update`, `uninstall` and every
   `marketplace` subcommand do not; their output is prose on stdout.
10. `install`, `update` and `uninstall` accept `-y`/`--yes`, which the CLI documents as REQUIRED
    when stdin or stdout is not a TTY for the paths that would otherwise prompt. (Host-output
    observation, not a test assertion.)
11. `install` and `uninstall` take `-s|--scope user|project|local`; `update` also accepts `managed`.
12. `uninstall` accepts `--keep-data` and `--prune`; `--prune` needs `-y` in non-interactive use.

### Upgrade and removal, as observed

13. Republishing is two commands, in this order: `claude plugin marketplace update <marketplace>`
    (it re-reads a local directory marketplace and says "Validating local marketplace"), then
    `claude plugin update <plugin> -s user`. The second prints
    `Plugin "<name>" updated from <old> to <new> for scope <scope>. Restart to apply changes.`
14. After an upgrade the OLD cache version directory is RETAINED
    (`plugins/cache/<marketplace>/qompack/` held both `0.1.0` and `0.1.1`). The host docs' "about
    fourteen days" is consistent with this; the page should not promise removal.
15. `claude plugin uninstall` empties the `enabledPlugins` entry and the
    `plugins/installed_plugins.json` entry. On this version it does NOT remove the payload cache —
    neither with `--keep-data` nor by default. A page that says the default uninstall deletes the
    payload is wrong for 2.1.263.
16. An uninstall leaves `extraKnownMarketplaces` in place. Removing the marketplace is a separate
    `claude plugin marketplace remove <name>`. (Host-output observation, not a test assertion.)
17. Every source file is byte-identical across install, upgrade and uninstall; `.qompack/` is
    byte-identical across the upgrade and survives the uninstall with its objects. Deleting
    recorded data is a MANUAL step (remove `<project>/.qompack/`), and it is not secure physical
    erasure across backups or media.

### qompack's own surface, as the page should describe it

18. `qompack config print --json` prints the effective configuration ONLY. Provenance is
    `qompack config print --provenance`, which is not a JSON document — a page telling a reader to
    look for provenance in `--json` output is wrong.
19. `qompack fsck --project <root> --json` is the read-only integrity report; its `severity` field
    is a NUMBER (internal/contract.Severity is a uint8 with no String method), and `read_only` is
    true for a default run.
20. A checkpoint artifact or capture sidecar newer than the build appears in `fsck --json` as a
    NOTE inside the row's `detail`, with `ok: true` and `exit: 0` — a support gap, not a defect.
    Neither `status --json` nor `doctor --json` carries a per-artifact schema row today, so the
    page should point a reader at `fsck` for that question.
21. Task 7 added `devtool bundle --archive`. Task 8 still rehearsed the DIRECTORY form: identity
    is `BUNDLE.json` plus `checksums.txt` (11 lines verified). Archive extraction was not rehearsed.

## 1. The matrix executed on this host

Every row is one record under `commit8-install-windows-amd64/`. `outcome` is the record's own.

| case | capability | host command (or API call) | outcome | what the host actually did |
| --- | --- | --- | --- | --- |
| `install_validate_bundle_strict` | install | `claude plugin validate <bundle> --strict --json` | verified | exit 0, `success: true`, zero errors and zero warnings, `manifest.type: "plugin"` |
| `install_marketplace_user_scope` | install | `claude plugin marketplace add <dir>` then `claude plugin install qompack@qompack-rehearsal -s user -y` | verified | `enabledPlugins` gained `qompack@qompack-rehearsal`; the cache holds the bundle with all 11 `checksums.txt` lines verifying; `plugin list --json` reports it enabled at user scope |
| `install_launcher_resolves_in_cache` | install | `<cache>/bin/qompack.exe version`, `... status --json` | verified | the installed launcher runs, prints `0.1.0`, and hashes to the bundle's `binary_sha256` |
| `upgrade_marketplace_republish` | upgrade | `claude plugin marketplace update qompack-rehearsal` then `claude plugin update qompack -s user -y` | verified | `0.1.0` -> `0.1.1` enabled; the OLD cache directory was RETAINED; `.qompack/` byte-identical across the upgrade; `fsck` exit 0 and MCP `recall` answered through the new launcher |
| `uninstall_keep_data` | uninstall | `claude plugin uninstall qompack -s user --keep-data -y` | verified | `enabledPlugins` entry gone, `plugin list` empty, payload cache RETAINED, no `plugins/data/` had ever been created |
| `uninstall_default` | uninstall | `claude plugin uninstall qompack -s user -y` | verified | same as above plus: the project tree outside `.qompack/` and `.git/` is byte-identical to the pre-install snapshot, `.qompack/` survives with its objects, and the manual deletion then restores the tree exactly |
| `rollback_sealed_checkpoint_restore` | rollback | `store.RehearseRollback` over a project that sealed a checkpoint | verified | OK after ada54d1 (`commit6-evidence.md` row 19): RestoreBackup writes protected paths through `paths.CreateNew`. The restored root holds the checkpoint artifact byte-for-byte. Original observation (failed, WriteAtomic on protected path) is history in §5 |
| `rollback_before_first_new_write` | rollback | `store.RehearseRollback(before-first-new-format-write)` | verified | refused as a RESULT beside a live daemon, then OK with it stopped: backup verified, reader proved, writers stopped, 3 legacy ids retained, `automatic_downgrade` false, evidence retained |
| `rollback_after_first_new_write` | rollback | `store.RehearseRollback(after-first-new-format-write)` | verified | OK with exactly 1 write enumerated in `unreadable_by_old_reader`, legacy ids still 3, `automatic_downgrade` false |
| `rollback_restored_root_reads_through_the_launcher` | rollback | `qompack status --json`, `qompack fsck --json`, `qompack mcp` `recall` over the restored root | verified | status parses, `fsck` exit 0 with no defect row, and every recall hit carried a root identity the live project holds |

## 2. Bundle identity

The bundle is assembled once per version per test binary and every record carries its identity.
Version `0.1.0`'s binary hashes to
`596aa5dd1fe9d42bf4396d1da4b2237ba841817f9672c68d840b4d93f409af5c`; version `0.1.1`'s differs, and
the upgrade case asserts that it does — two versions of one bundle that were the same binary would
make the whole upgrade unobservable.

Three identity checks run before a host sees the bundle, and one after:

- every line of `checksums.txt` (11 files) re-hashes against the assembled tree;
- `.claude-plugin/plugin.json`'s `version` equals `BUNDLE.json`'s;
- `claude plugin validate --strict --json` reports success with no errors and no warnings;
- after installing, every line of the CACHED `checksums.txt` re-hashes against the cached tree, and
  the cached `bin/qompack.exe` hashes to the record's `binary_sha256`.

## 3. The user's real configuration was never written (R8-2)

`~/.claude/settings.json`'s sha256 and a (path, size) listing of `~/.claude/plugins/` are taken
before each test's first host invocation and asserted equal after its last, including after the
uninstall each helper registers in `t.Cleanup`. The real home is captured at package
initialisation, before any test calls `testutil.NewProject` — that constructor `t.Setenv`s `HOME`
and `USERPROFILE` at a temp tree, and a fingerprint taken later would have described a directory
that never existed and passed vacuously.

Independently of the test, the same two facts were taken by hand around the whole session:
`settings.json` was `bf1c77b88e0bbd73ac31de072e03805a88bc29f670dfc3e0e36b9ace818a1303` before the
first probe and after the last, and `~/.claude/plugins/` listed the same 35 entries both times.

## 4. The rollback drill, on real data

The gate is opened the way `internal/store/backup_test.go` opens it and no other way:
`config.LegacyImportGate()` is exported and `MigrationGate.Passed` is an exported field, so a test
outside the package constructs a PASSED gate and says out loud that it is running a gated
capability. R8-4's fallback ("rehearsed in-package") was therefore not needed: every step below ran
from `test/e2e` through the store's exported surface, against a project the real launcher recorded.

`StopWriters` is the real check rather than a stub. It reads the project's `run/daemon.lock`,
resolves the pid and reports whether that process is alive, so "the incompatible writers were
stopped" is a statement about the machine and not about the test's intentions.

| step | outcome |
| --- | --- |
| drill with the daemon ALIVE | refused as a RESULT, not an error: `ok` false, `writers_stopped` false, refusal names the daemon's pid and its lock file, and `migrate/rollback.jsonl` holds exactly one line |
| drill with the daemon stopped, before phase | `ok`, `backup_verified`, `reader_proved`, `writers_stopped`, 3 legacy ids retained, nothing in `unreadable_by_old_reader`, `automatic_downgrade` false, `evidence_retained`; the log now holds two lines |
| cutover, one new-format write, after phase | `ok`, exactly one write enumerated in `unreadable_by_old_reader` carrying the legacy id it supersedes, 3 legacy ids still retained, `automatic_downgrade` false |
| the restored root through the launcher | `status --json` parses, `fsck --json` exits 0 with no defect row, and MCP `recall` answered with root identities the live project holds |

GC's inability to collect the material either drill depends on is proved in-package by
`internal/store`'s `TestGC_CannotCollectMigrationOrRollbackMaterial`, which forces retention and
re-reads. It is CITED rather than duplicated; this rehearsal asserts only that `migrate/` and
`backup/` are still present after both drills.

## 5. Defects — both fixed in commit 6 fix round 1 (`ada54d1`)

The original observations below are history. The follow-up re-measured both records on this tree.

### D8-1 — `store.RestoreBackup` of a sealed checkpoint — FIXED

Original observation: `RestoreBackup` wrote every backed-up file with `paths.WriteAtomic`, which
refuses §7.4's protected paths, so a project that had sealed a checkpoint could not restore:

```
the backup did not restore: store: restore backup "sp17-pre-cutover": qompack: append-only
violation: WriteAtomic on protected path <restore root>/.qompack/checkpoints/0001.json
```

Fixed in `ada54d1`, `commit6-evidence.md` row 19: `internal/store/backup.go:413` `RestoreBackup`
writes a destination `paths.IsProtected` names through `paths.CreateNew`. Re-measured here:
`rollback_sealed_checkpoint_restore` is **verified** — OK, backup verified, reader proved,
writers stopped, `automatic_downgrade` false, evidence retained; the restored root holds the
checkpoint artifact byte-for-byte.

### D8-2 — `config.LoadForCapture` newer `settingsVersion` — FIXED

Original observation: `LoadForCapture` had no `applyVersionedSections`, so a newer
`settingsVersion` refused capture (hooks exit 0 empty, no daemon) while `config print` reset
the block.

Fixed in `ada54d1`, `commit6-evidence.md` row 8: `internal/config/capture_load.go:123` calls
`applyVersionedSections` before the clamp and returns the reset as a violation. Re-measured
here: `unknown_schema_config_settings_version` stays **verified** with the new reason — capture
continues, `state/config-violations.json` names the reset, the effective block is defaults
(`reinjection.sessionStartCompact` planted false, observed true after the reset;
`experiments.enabled` is a gated key restored by Validate on any build, not reset evidence),
hooks exit 0, a daemon comes up, the planted file's bytes are unchanged, and
`config print --provenance` still reports `reset: newer settingsVersion`.

## 6. Unknown schemas, planted in a project that already holds real data

Four artifacts newer than this build are planted beside real recorded data. The property asserted
for each is the same: the build REPORTS the version it cannot read and leaves the bytes exactly as
it found them. Every planted file is hashed before and after, and every hash is unchanged.

| case | what was planted | outcome | what the build did |
| --- | --- | --- | --- |
| `unknown_schema_checkpoint_artifact` | `checkpoints/9999-newer-plugin.json` with `version` 2 (build reads 1), no MANIFEST line | verified | a whole session still records; `fsck`'s `checkpoints` row stays `ok` and its `detail` names the file "written by a newer plugin"; bytes unchanged |
| `unknown_schema_capture_sidecar` | `records/captures/newer-plugin.json` with `v` 2 (build reads 1) | verified | readable and degraded: `fsck`'s `captures` row stays `ok` and names it "newer than this build"; sidecars are evidence and are never repaired or swept; bytes unchanged |
| `unknown_schema_delivery_seal_v2` | `state/delivery-ack-position.json` declaring `v` 2 | verified | `fsck`'s `delivery` row classifies it as "a v2 position document" and leaves it alone; bytes unchanged |
| `unknown_schema_config_settings_version` | `.qompack/config.json` with `runtime.migration.settingsVersion` 2, gated `experiments.enabled` true, and ungated `reinjection.sessionStartCompact` false (build reads 1 / default true) | verified | see §5 D8-2 (fixed): capture continues with the block reset; `state/config-violations.json` names the reset; effective `sessionStartCompact` is true; `experiments.enabled` is gated and restored by Validate on any build; hooks exit 0; a daemon comes up; `config print --provenance` still reports `reset: newer settingsVersion`; bytes unchanged |

Two orderings in that test are findings rather than conveniences, and both are commented in the
source so nobody "tidies" them away:

- the delivery seal is planted with the project QUIET and read only by `fsck`. A hand-written seal
  beside a RUNNING daemon makes `loadDeliveryPosition` refuse the journal and publication stalls —
  that measures a race, not a reader.
- the config plant comes last so the other three artifacts are hashed against a default
  configuration; after ada54d1 capture continues through the reset.

A third is a property of the product, not of the test: ingest's seen-set collapses a repeated
`tool_use_id` back to one dispatch, correctly, so two sessions over one project must not reuse
ids or the second indexes nothing. The helper numbers every hook suite for that reason.

## 7. What was NOT executed here, and why

| deliverable | status | why |
| --- | --- | --- |
| `--archive` bundle identity | not executed | Task 7 added the flag. This rehearsal still installs from the assembled DIRECTORY plus `BUNDLE.json` and `checksums.txt`. |
| `releaseCheckExtraTests` wiring | done in the follow-up | the three names are in `tools/devtool/releasechecksteps.go` `releaseCheckExtraTests`; `TestReleaseCheckRollbackTestsAreNamed` confirms them with `go test -list` against `./test/e2e/`. |
| `release-scope --markdown` paste | done in the follow-up | see "Release scope at this commit" below. |
| `docs/install.md` corrections applied | done in the follow-up | the page is the corrected text; the numbered list above remains the source. |
| `docs/release.md` scope table | done in the follow-up | replaced with the follow-up `release-scope --markdown` output. |
| CHANGELOG line | done in the follow-up | `[Unreleased]` Added. |
| Windows shell resolution of `${CLAUDE_PLUGIN_ROOT}/bin/qompack` | cited, not repeated | Task 2's platform matrix already established it (`commit2-evidence.md`, the `shell-*` records). This commit adds only what those records could not: the path is the HOST'S OWN cache directory. |
| GC cannot collect migration or rollback material | cited, not repeated | `internal/store`'s `TestGC_CannotCollectMigrationOrRollbackMaterial`. |
| an OLD binary refusing a v2 delivery seal | cited, not repeated | `internal/daemon`'s `delivery_seal_format2_test.go`. |

No case was `skipped`: the `claude` CLI was on PATH for every run, and every skip path in the test
writes its record before skipping so a future absence is visible rather than silent.

## Release scope at this commit

Generated by `go run ./tools/devtool release-scope --markdown` on 2026-09-16 after the records
were regenerated (14 verified, 0 failed). `windows/amd64` is `installed-verified` citing a
verified install record. SP17-M7-05 is `verified` because D8-1 is fixed (`ada54d1`,
`commit6-evidence.md` row 19) and the sealed-checkpoint record re-measured `verified`. SP17-M7-06
is `verified` (uninstall + unknown_schema). SP17-M7-01's installed half is `verified`.

```
## Supported scope

Derived by `go run ./tools/devtool release-scope` from the committed records under `plans/sdd/V6-SP-17-packaging-hardening-and-release`.
A status is raised only by a record with an `outcome` (for `release-check.json`, derived from `ok` and its step statuses); prose never raises one.

| target | status | artifact |
| --- | --- | --- |
| `linux/amd64` | unknown | — |
| `linux/arm64` | unknown | — |
| `darwin/amd64` | unknown | — |
| `darwin/arm64` | unknown | — |
| `windows/amd64` | installed-verified | commit8-install-windows-amd64/install_launcher_resolves_in_cache.json |
| `windows/arm64` | unknown | — |

`installed-verified` > `directory-validated` > `built` > `unknown`. `built` means a committed record names that target; a cross-compile proven only by CI's
`crossbuild` job leaves no record here and so reads `unknown`.

## Acceptance rows

| row | status | artifact | note |
| --- | --- | --- | --- |
| SP17-M7-01 | verified | commit8-install-windows-amd64/install_launcher_resolves_in_cache.json | — |
| SP17-M7-02 | verified | platform records | 17 record(s): 17 verified, 0 skipped, 0 failed |
| SP17-M7-03 | unverified | commit3-security-windows-amd64/posture_out_of_project_capture_is_archived.json | a security record reports failed: posture_out_of_project_capture_is_archived |
| SP17-M7-04 | unverified | commit4-fault-windows-amd64/capture_sidecar_stage_one_only.json | a fault record reports failed: capture_sidecar_stage_one_only |
| SP17-M7-05 | verified | commit8-install-windows-amd64/rollback_after_first_new_write.json | — |
| SP17-M7-06 | verified | switches + install records | — |
| SP17-M7-07 | unverified | — | accounting attached; no release-check record verifies it |
| SP17-M7-08 | unverified | — | still missing: a verified release-check record |
| SP17-M7-07 / live-task layer | excluded | — | no live model runs are executed by this repository's tests; eval.LiveRunner is nil in every shipped build and is deliberately left unwired (ruling R7-2) |
```

## 8. Reproducing it

```sh
go run ./tools/devtool fmt-check
go vet ./test/e2e/
go test -count=1 -timeout=30m ./test/e2e/ -run 'TestInstall|TestRollbackRehearsal|TestUnknownSchema' -v
```

The collecting run adds one environment variable and rewrites the record directory:

```sh
QOMPACK_INSTALL_ARTIFACTS=plans/sdd/V6-SP-17-packaging-hardening-and-release/commit8-install-windows-amd64 \
  go test -count=1 -timeout=30m ./test/e2e/ -run 'TestInstall|TestRollbackRehearsal|TestUnknownSchema' -v
```

It prunes every `*.json` in that directory first and writes `INDEX.json` from `TestMain` after the
last case, so a case that fatalled before writing its record shows up as an absence in a document
written afterwards rather than as a stale row from an earlier run.

The whole pass is serial, brings up one daemon at a time, shuts every daemon it started down, and
makes no timing assertion. It takes about three and a half minutes on this host, of which roughly
two are the two real cross-compiles `devtool bundle` performs.

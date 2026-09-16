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
| Records | `commit8-install-windows-amd64/` — 14 records (13 verified, 1 failed) plus `INDEX.json`, in the shape `install-record-schema.md` fixes |
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

`docs/install.md` does not exist on this commit's base — Task 7 owns it and had not landed when
Task 8 ran. This section is therefore the correction list itself: every statement below is a fact
this rehearsal OBSERVED on `claude` 2.1.263, phrased so it can be applied to that page verbatim.
The coordinator applies it at the fold.

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
   `target` is the resolved `.claude-plugin/plugin.json` path, not the directory.

### What an install writes

4. `enabledPlugins` keys are `<plugin>@<marketplace>` (`qompack@qompack-rehearsal`), never the bare
   plugin name.
5. `claude plugin marketplace add <dir>` writes `extraKnownMarketplaces` into `settings.json` AND
   `plugins/known_marketplaces.json`, and reports "(declared in user settings)".
6. Installing creates, under `CLAUDE_CONFIG_DIR`: `settings.json`, `.claude.json`,
   `backups/.claude.json.backup.<ms>`, `plugins/installed_plugins.json`,
   `plugins/known_marketplaces.json`, an empty `plugins/marketplaces/`, and the payload cache
   `plugins/cache/<marketplace>/<plugin>/<version>/`.
7. The payload cache holds the WHOLE bundle directory, `BUNDLE.json` and `checksums.txt` included.
   Every `checksums.txt` line verifies inside the cache, and the cached `bin/qompack.exe` is
   byte-identical to the assembled one (same sha256).
8. `plugins/data/<id>/` is NOT created by an install. It appears only once a plugin has run in a
   live session, so on a rehearsal host `--keep-data` has nothing to keep.

### Flags that exist, and flags that do not

9. Only `validate` and `list` accept `--json`. `install`, `update`, `uninstall` and every
   `marketplace` subcommand do not; their output is prose on stdout.
10. `install`, `update` and `uninstall` accept `-y`/`--yes`, which the CLI documents as REQUIRED
    when stdin or stdout is not a TTY for the paths that would otherwise prompt.
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
    `claude plugin marketplace remove <name>`.
17. Nothing any of these commands do touches the project: `.qompack/` and every source file are
    byte-identical across install, upgrade and uninstall. Deleting recorded data is a MANUAL step
    (remove `<project>/.qompack/`), and it is not secure physical erasure across backups or media.

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
21. `devtool bundle` has no `--archive` flag on this commit; a bundle is a DIRECTORY whose identity
    is `BUNDLE.json` plus `checksums.txt`. Any archive identity is Task 7's to add.

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
| `rollback_sealed_checkpoint_restore` | rollback | `store.RehearseRollback` over a project that sealed a checkpoint | **failed** | the drill refuses at its restore step: `WriteAtomic on protected path .../checkpoints/0001.json`. RETURNED — see §5 |
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

## 5. Returned defects

Both are RETURNED with owners and are NOT fixed here (Role F's rule; the coordinator's final fix
wave decides). Each is asserted in its observed shape so the rehearsal stays green and the evidence
stays true, and each assertion carries a comment telling a future reader to move the record back to
`verified` if the behaviour changes.

### D8-1 — `store.RestoreBackup` cannot restore a project that has ever sealed a checkpoint

Owner: `internal/store` (SP-20 M1-04, the owner of the migration/rollback API).
Row affected: **SP17-M7-05**. Record: `rollback_sealed_checkpoint_restore.json` (`failed`).

`TakeBackup` copies the whole `.qompack` tree except `backup/ run/ tmp/ logs/ metrics/`, so a real
project's backup contains `checkpoints/NNNN.json`. `RestoreBackup` writes every backed-up file with
`paths.WriteAtomic`, and `WriteAtomic` refuses §7.4's protected paths outright — `checkpoints/`,
`pins/` and `sketches/tried.bloom`. The drill therefore stops at step 4 with

```
the backup did not restore: store: restore backup "sp17-pre-cutover": qompack: append-only
violation: WriteAtomic on protected path <restore root>/.qompack/checkpoints/0001.json
```

This is a RESULT rather than an error, which is the API behaving as designed; what is defective is
that the result can never be `ok` for any project that has compacted even once, and the in-package
drill does not see it because its fixtures seal no checkpoint, create no pin and build no tried
bloom. `paths.CreateNew` is the sanctioned writer for those paths and is what a restore needs.

Until it is fixed, SP17-M7-05 is `unverified` for a real project: the second project in the
rehearsal (recorded identically but with the PreCompact hook left out) carries every other property
of the drill, and the `failed` record above is what `release-scope` must read.

### D8-2 — `config.LoadForCapture` has no versioned-section reset, so a newer config turns capture off

Owner: `internal/config` (SP-01 owns the layering; the hot-path entry is `capture_load.go`).
Row affected: SP17-M7-06's unknown-schema half — reported, not silently wrong, so the row still
holds; but the two readers disagree and only one of them matches the plan's words.

`Load` applies `applyVersionedSections` before `Validate`, so a project config whose
`runtime.migration.settingsVersion` is newer than the build resets that whole block to defaults and
emits one Warning — plan §5's "unknown host/schema disables unsafe optimization and reports
unsupported/degraded without corrupting data", exactly.

`LoadForCapture` — the hot-path entry every hook admits through — never calls it, and refuses on
any `Validate` violation with `core.ErrDegraded`. A newer `settingsVersion` IS such a violation
(`validate.go`'s exact-equality check), so on this build:

- all six hooks still exit 0 with a parseable empty output (§13 invariant 6 holds);
- the refusal is recorded in `logs/hook-quiet-*.jsonl` as "running in degraded mode: capture
  configuration unavailable" (§13 invariant 10 holds);
- but nothing is recorded at all, and `session-start` never reaches its daemon-spawning `preSend`,
  so no daemon comes up for the rest of the session;
- and `qompack config print --provenance`, which goes through `Load`, cheerfully reports the block
  as reset — two answers to one question, from one binary, in one project.

The rehearsal asserts the observed behaviour rather than the documented one. Whoever fixes this
should decide which reader is right before changing either.

## 6. Unknown schemas, planted in a project that already holds real data

Four artifacts newer than this build are planted beside real recorded data. The property asserted
for each is the same: the build REPORTS the version it cannot read and leaves the bytes exactly as
it found them. Every planted file is hashed before and after, and every hash is unchanged.

| case | what was planted | outcome | what the build did |
| --- | --- | --- | --- |
| `unknown_schema_checkpoint_artifact` | `checkpoints/9999-newer-plugin.json` with `version` 2 (build reads 1), no MANIFEST line | verified | a whole session still records; `fsck`'s `checkpoints` row stays `ok` and its `detail` names the file "written by a newer plugin"; bytes unchanged |
| `unknown_schema_capture_sidecar` | `records/captures/newer-plugin.json` with `v` 2 (build reads 1) | verified | readable and degraded: `fsck`'s `captures` row stays `ok` and names it "newer than this build"; sidecars are evidence and are never repaired or swept; bytes unchanged |
| `unknown_schema_delivery_seal_v2` | `state/delivery-ack-position.json` declaring `v` 2 | verified | `fsck`'s `delivery` row classifies it as "a v2 position document" and leaves it alone; bytes unchanged |
| `unknown_schema_config_settings_version` | `.qompack/config.json` with `runtime.migration.settingsVersion` 2 (build reads 1) | verified | see D8-2: off the hot path the block resets with `reset: newer settingsVersion` provenance and the effective values are the build's; on the hot path capture is refused, all six hooks still exit 0 with an empty output, `logs/hook-quiet-*.jsonl` names the degraded mode, and no daemon is spawned; bytes unchanged |

Two orderings in that test are findings rather than conveniences, and both are commented in the
source so nobody "tidies" them away:

- the delivery seal is planted with the project QUIET and read only by `fsck`. A hand-written seal
  beside a RUNNING daemon makes `loadDeliveryPosition` refuse the journal and publication stalls —
  that measures a race, not a reader.
- the config plant comes last, because after it nothing records at all (D8-2).

A third is a property of the product, not of the test: ingest's seen-set collapses a repeated
`tool_use_id` back to one dispatch, correctly, so two sessions over one project must not reuse
ids or the second indexes nothing. The helper numbers every hook suite for that reason.

## 7. What was NOT executed here, and why

| deliverable | status | why |
| --- | --- | --- |
| `--archive` bundle identity | not executed | `devtool bundle` has no `--archive` flag on this commit's base. The bundle DIRECTORY plus `BUNDLE.json` and `checksums.txt` is what was verified; the archive identity is Task 7's and is folded in afterwards. |
| `releaseCheckExtraTests` wiring | not executed | `tools/devtool/releasecheck.go` does not exist on this base. The three test names to append are `TestInstall_HostCLIInstallUpgradeUninstall`, `TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite` and `TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting`, all in `./test/e2e/`. |
| `release-scope --markdown` paste | not executed | `tools/devtool/releasescope.go` does not exist on this base. The records this commit writes are in the shape `install-record-schema.md` fixes, so ingestion needs no change on this side. |
| `docs/install.md` corrections applied | not executed | the page does not exist on this base. The corrections are listed above, ready to apply verbatim. |
| `docs/release.md` scope table | not executed | same reason. |
| CHANGELOG line | not written | deliberately deferred to the fold, with the docs edits it belongs beside. |
| Windows shell resolution of `${CLAUDE_PLUGIN_ROOT}/bin/qompack` | cited, not repeated | Task 2's platform matrix already established it (`commit2-evidence.md`, the `shell-*` records). This commit adds only what those records could not: the path is the HOST'S OWN cache directory. |
| GC cannot collect migration or rollback material | cited, not repeated | `internal/store`'s `TestGC_CannotCollectMigrationOrRollbackMaterial`. |
| an OLD binary refusing a v2 delivery seal | cited, not repeated | `internal/daemon`'s `delivery_seal_format2_test.go`. |

No case was `skipped`: the `claude` CLI was on PATH for every run, and every skip path in the test
writes its record before skipping so a future absence is visible rather than silent.

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

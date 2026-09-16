# Installing Qompack

Task 8 rehearsed install, upgrade and uninstall on **windows/amd64** against `claude` 2.1.263,
with `CLAUDE_CONFIG_DIR` pointed at a disposable home. The supported-scope table in
`docs/release.md` §3 is generated from those records: today that host reads `installed-verified`;
the other five targets have no install record.

Commands this page marks **rehearsed** are the ones Task 8 actually ran. Anything else stays
**recorded from host docs, not rehearsed**.

## 1. What you are installing

A **bundle**: one directory per release target, which *is* the plugin root. `packaging/README.md` is
the contract; the short version:

```
qompack-plugin-<version>-<os>-<arch>/
├── .claude-plugin/plugin.json
├── .mcp.json
├── hooks/hooks.json
├── commands/*.md
├── bin/qompack            (bin/qompack.exe on windows)
├── BUNDLE.json            identity: what this bundle is and what is in it
└── checksums.txt          sha256sum-format listing of everything above
```

A release ships each bundle as a `.zip` (windows) or `.tar.gz`, plus one `checksums.txt` over the
archives (`devtool bundle --archive`, Task 7). Task 8 rehearsed the **directory** form: it verified
the bundle's own `checksums.txt` against the assembled tree (11 lines) and installed from that
directory. Archive extraction was **recorded from host docs, not rehearsed**:

```sh
sha256sum -c checksums.txt
```

Then verify the contents against the bundle's own `checksums.txt`, from inside the extracted
directory. `BUNDLE.json` records the version, the target, the Go toolchain, and the commit it was
assembled from — including a `dirty` flag, which is `true` for any bundle that cannot be reproduced
from its commit and therefore is not a release artifact.

## 2. Trying it without installing anything

*(Recorded from host docs, not rehearsed.)*

```sh
claude --plugin-dir /path/to/qompack-plugin-<version>-<os>-<arch>
```

This loads the plugin **for that session only**. No settings file changes, nothing is copied into
`~/.claude`, and an installed plugin of the same name is shadowed for the session. It is the right
way to evaluate Qompack, and the right way to reproduce a problem without disturbing a working
setup.

`claude --settings '<json or path>'` applies configuration keys for one session in the same spirit,
and `CLAUDE_CONFIG_DIR` relocates the whole home configuration if you want an install rehearsal that
cannot touch your real one. Task 8 used `CLAUDE_CONFIG_DIR` for every host invocation.

## 3. Installing from a local directory

A marketplace plugin `source` must be a `./`-relative path **beneath** the marketplace root — the
directory holding `.claude-plugin/` — not relative to `marketplace.json`. A `..` segment is refused
outright. Practically, `devtool bundle --out <dir>` and the marketplace share `<dir>`, and the
plugin source is `./qompack-plugin-<version>-<os>-<arch>`. Under `--strict`, a marketplace manifest
without a `description` fails (`success: false`, one warning); a passing minimal manifest carries
`name`, `description`, `owner` and `plugins`.

```sh
claude plugin marketplace add /path/to/parent-of-the-bundle
claude plugin install qompack@<marketplace> -s user -y
```

`-s` selects the scope: `user` writes `enabledPlugins` in `~/.claude/settings.json`, `project`
writes `.claude/settings.json`, `local` writes `.claude/settings.local.json`. Task 8 rehearsed
**user** scope only; project and local are recorded from host docs, not rehearsed.

What an install writes, as observed on 2.1.263 (host-output observations, not test assertions,
except where noted):

- `enabledPlugins` keys are `<plugin>@<marketplace>` (`qompack@qompack-rehearsal`), never the bare
  name. **Asserted.**
- `claude plugin marketplace add <dir>` writes `extraKnownMarketplaces` into `settings.json` and
  `plugins/known_marketplaces.json`, and reports "(declared in user settings)".
- Installing creates, under `CLAUDE_CONFIG_DIR`: `settings.json`, `.claude.json`,
  `backups/.claude.json.backup.<ms>`, `plugins/installed_plugins.json`,
  `plugins/known_marketplaces.json`, an empty `plugins/marketplaces/`, and the payload cache
  `plugins/cache/<marketplace>/<plugin>/<version>/`.
- The payload cache holds the **whole** bundle, `BUNDLE.json` and `checksums.txt` included. Every
  `checksums.txt` line verifies inside the cache, and the cached `bin/qompack.exe` is
  byte-identical to the assembled one. **Asserted.**
- `plugins/data/<id>/` is **not** created by an install. It appears only once a plugin has run in a
  live session, so `--keep-data` has nothing host-side to keep on a rehearsal host.

Only `validate` and `list` accept `--json`. `install`, `update`, `uninstall` and every
`marketplace` subcommand do not; their output is prose on stdout. `install`, `update` and
`uninstall` accept `-y`/`--yes`, which the CLI documents as required when stdin or stdout is not a
TTY (host-output observation).

Your recorded sessions are **not** in the cache. They are in `<project>/.qompack/` and
`~/.qompack/` — see §6.

## 4. Checking that it worked

```sh
claude plugin validate /path/to/qompack-plugin-<version>-<os>-<arch> --strict --json
qompack doctor --json
```

On an assembled qompack bundle, `claude plugin validate --strict --json` reported `success: true`,
`manifest.type: "plugin"`, and empty `errors`, `warnings` and `contents`; `target` is the resolved
`.claude-plugin/plugin.json` path, not the directory (host-output observation). Note what
validation is not: the directory is validated **where it sits**, so installed manifest resolution,
`${CLAUDE_PLUGIN_ROOT}` expansion and launcher discovery remain unverified by it (Qompack.md §7.5).
The install record is what raises `installed-verified`.

`qompack doctor --json` answers version, host, capability, scope, and which controls are disabled.
It carries a `project` field and a `checkpoint.latest` row; it does **not** carry a per-artifact
schema row. A checkpoint artifact or capture sidecar newer than the build appears in
`qompack fsck --project <root> --json` as a note inside the row's `detail`, with `ok: true` and
`exit: 0` — a support gap, not a defect. Point a reader at `fsck` for that question.
`fsck --json`'s `severity` field is a number (`internal/contract.Severity` is a uint8), and
`read_only` is true for a default run.

`qompack config print --json` prints the effective configuration only. Provenance is
`qompack config print --provenance`, which is not a JSON document.

## 5. Upgrading

Republishing is two commands, in this order:

```sh
claude plugin marketplace update <marketplace>
claude plugin update qompack -s user -y
```

The first re-reads a local directory marketplace ("Validating local marketplace"). The second
prints `Plugin "<name>" updated from <old> to <new> for scope <scope>. Restart to apply changes.`

After an upgrade the **old** cache version directory is retained
(`plugins/cache/<marketplace>/qompack/` held both `0.1.0` and `0.1.1`). The host docs' "about
fourteen days" is consistent with this; this page does not promise removal.
`${CLAUDE_PLUGIN_ROOT}` therefore **changes on every update**: anything that hard-codes the old
path — a shell alias, a wrapper script, a `PATH` entry pointing into the cache — breaks silently
at the next upgrade. Resolve the binary through the plugin root the host exports, never through a
remembered path.

Every source file is byte-identical across install, upgrade and uninstall. `.qompack/` is
byte-identical across the upgrade and survives the uninstall with its objects. Deleting recorded
data is a manual step (remove `<project>/.qompack/`), and it is not secure physical erasure across
backups or media.

## 6. Uninstalling, and what happens to your data

```sh
claude plugin uninstall qompack -s user -y
claude plugin uninstall qompack -s user --keep-data -y
```

`--prune` (also remove the cached payload) is recorded from host docs, not rehearsed; it needs
`-y` in non-interactive use.

**On Claude Code 2.1.263 the payload cache is retained on both paths** — `--keep-data` and the
default. The two commands are indistinguishable here because no `plugins/data/<id>/` is ever
created by an install. A page that says the default uninstall deletes the payload is wrong for
this version. An uninstall leaves `extraKnownMarketplaces` in place; removing the marketplace is a
separate `claude plugin marketplace remove <name>` (host-output observation).

**The policy, stated plainly:**

| what | what uninstall does on 2.1.263 |
| --- | --- |
| the host integration (`enabledPlugins` entry, `plugin list`) | removed |
| the bundle's binaries and manifest under the host's cache | **retained** on both paths |
| host-side plugin data under `~/.claude/plugins/data/<id>/` | not created by an install; `--keep-data` has nothing to keep |
| **`<project>/.qompack/` and `~/.qompack/` — your recorded sessions** | **retained. Always.** |

The last row is deliberate and it is not an oversight. `.qompack/` is your data: the captures, the
index, the checkpoints and the diagnostic evidence from your own sessions. Uninstalling a tool is
not the same decision as deleting what it recorded, so uninstall never makes the second decision for
you.

To delete it, delete it yourself:

```sh
rm -rf /path/to/project/.qompack        # one project's store
rm -rf ~/.qompack                       # user-level configuration and logs
```

**No secure-erasure promise.** Those commands unlink files. They say nothing about backups you made,
copies on other media, or a filesystem that snapshots. If a credential ever reached the store — see
`docs/security.md` §2 for what redaction does and does not catch — treat every copy as still holding
it.

## 7. Turning features off without uninstalling

Four keys disable a live path, and they compose: set one and the rest keep working. The full table,
including `runtime.redact.enabled` and which keys are *refused* rather than disabled, is in
`docs/release.md` §4. Schema and every default live in `docs/config-reference.md`. After changing
configuration, check `state/config-violations.json` — it is where the product records any value it
refused, including a newer `settingsVersion` reset.

Recording can be disabled for privacy (`runtime.mode: "off"`) while whatever was already recorded
stays retrievable: retrieval remains scoped to what host permission allows **today**, re-checked at
every request, not at capture time.

## 8. Scope

What is actually verified on which platform is in `docs/release.md` §3, and it is generated from
committed records rather than written by hand. If your platform reads `unknown` there, the plugin
may work perfectly — nobody has measured it, and this page will not pretend otherwise.

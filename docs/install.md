# Installing Qompack

Every command on this page was **recorded from the Claude Code documentation** and is rehearsed
against a real host in SP-17 Task 8; until that rehearsal lands, treat the exact spellings as the
host's published contract rather than as something this repository has executed. The supported-scope
table in `docs/release.md` §3 says which platforms actually have a record behind them — today that
is one of six, and it is a *directory* validation rather than an installed one.

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
archives. Verify before you extract:

```sh
sha256sum -c checksums.txt
```

Then verify the contents against the bundle's own `checksums.txt`, from inside the extracted
directory. `BUNDLE.json` records the version, the target, the Go toolchain, and the commit it was
assembled from — including a `dirty` flag, which is `true` for any bundle that cannot be reproduced
from its commit and therefore is not a release artifact.

## 2. Trying it without installing anything

```sh
claude --plugin-dir /path/to/qompack-plugin-<version>-<os>-<arch>
```

This loads the plugin **for that session only**. No settings file changes, nothing is copied into
`~/.claude`, and an installed plugin of the same name is shadowed for the session. It is the right
way to evaluate Qompack, and the right way to reproduce a problem without disturbing a working
setup.

`claude --settings '<json or path>'` applies configuration keys for one session in the same spirit,
and `CLAUDE_CONFIG_DIR` relocates the whole home configuration if you want an install rehearsal that
cannot touch your real one.

## 3. Installing from a local directory

*(Recorded from host docs, rehearsed in Task 8.)*

```sh
# inside claude, add the directory holding the bundle as a marketplace
/plugin marketplace add /path/to/qompack-plugin-<version>-<os>-<arch>

# then install from it
claude plugin install qompack@<marketplace> -s user
```

`-s` selects the scope: `user` writes `enabledPlugins` in `~/.claude/settings.json`, `project`
writes `.claude/settings.json`, `local` writes `.claude/settings.local.json`. The payload is cached
under `~/.claude/plugins/cache/<marketplace>/qompack/<version>/`, and per-plugin host data under
`~/.claude/plugins/data/<id>/`.

Your recorded sessions are **not** in either place. They are in `<project>/.qompack/` and
`~/.qompack/` — see §6.

## 4. Checking that it worked

```sh
claude plugin validate /path/to/qompack-plugin-<version>-<os>-<arch> --strict --json
qompack doctor
```

`claude plugin validate` is the host's own verdict on the manifest. Note what it is not: the
directory is validated **where it sits**, so installed manifest resolution,
`${CLAUDE_PLUGIN_ROOT}` expansion and launcher discovery remain unverified by it (Qompack.md §7.5).
`qompack doctor` answers the other half — version, host, capability, scope, and which controls are
disabled — and is the first thing to attach to any bug report.

## 5. Upgrading

Install the new version the same way. The host keeps the previous version's payload for roughly
**14 days** and then removes it. `${CLAUDE_PLUGIN_ROOT}` therefore **changes on every update**:
anything that hard-codes the old path — a shell alias, a wrapper script, a `PATH` entry pointing
into the cache — breaks silently at the next upgrade. Resolve the binary through the plugin root the
host exports, never through a remembered path.

Your data is untouched by an upgrade. `.qompack/` belongs to the project, not to the plugin
directory (00-ARCHITECTURE.md §3.3).

## 6. Uninstalling, and what happens to your data

```sh
claude plugin uninstall qompack -s user
claude plugin uninstall qompack -s user --keep-data     # keep the host-side plugin data directory
claude plugin uninstall qompack -s user --prune         # also remove the cached payload
```

**The policy, stated plainly:**

| what | what uninstall does |
| --- | --- |
| the host integration (`enabledPlugins` entry, hooks, MCP server registration) | removed |
| the bundle's binaries and manifest under the host's cache | removed (`--prune` removes the cached payload too) |
| host-side plugin data under `~/.claude/plugins/data/<id>/` | removed unless you pass `--keep-data` |
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
`docs/release.md` §4. The short version:

| to | set |
| --- | --- |
| stop everything | `runtime.mode: "off"` |
| keep recording, stop acting | `runtime.mode: "passive"` |
| stop reinjection after compaction | `runtime.migration.reinjection.sessionStartCompact: false` |
| run without a resident daemon | `runtime.daemon.enabled: false` |

Put them in `<project>/.qompack/config.json` or `~/.qompack/config.json`; see
`docs/config-reference.md` for the schema and every default. After changing configuration, check
`state/config-violations.json` — it is where the product records any value it refused.

Recording can be disabled for privacy (`runtime.mode: "off"`) while whatever was already recorded
stays retrievable: retrieval remains scoped to what host permission allows **today**, re-checked at
every request, not at capture time.

## 8. Scope

What is actually verified on which platform is in `docs/release.md` §3, and it is generated from
committed records rather than written by hand. If your platform reads `unknown` there, the plugin
may work perfectly — nobody has measured it, and this page will not pretend otherwise.

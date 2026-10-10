# Installing Qompack

**Tested host version: Claude Code 2.1.280**, on Windows 11 (windows/amd64). Two sets of records
stand behind this page. SP-17's Task 8 rehearsed install, upgrade and uninstall on **windows/amd64**
against `claude` 2.1.263, with `CLAUDE_CONFIG_DIR` pointed at a disposable home; the supported-scope
table in `docs/release.md` §3 is generated from those records, so that host reads
`installed-verified` and the other five targets have no install record. The V6 close-out's live lane
then installed its frozen windows/amd64 bundles into Claude Code 2.1.280 through a disposable local
marketplace at **local** scope and ran real sessions in them (`plans/sdd/V6-closeout/live/`;
agent-executed on the owner's machine under owner decision D3, not human UAT).

Commands this page marks **rehearsed** are the ones those runs actually made. Anything else stays
**recorded from host docs, not rehearsed**.

**Host requirement: Claude Code 2.1.139 or later, for every install path** — `--plugin-dir`, a local
directory and the marketplace alike. Every hook and the MCP server are exec form (`command` plus
`args`), and 2.1.139 is the release that "Added hook `args: string[]` field (exec form) that spawns
the command directly without a shell" (changelog, fetched 2026-09-22). An older host does not know
`args`. If it runs the entry anyway, it runs the bare `bin/qompack`, which prints its usage and
exits 0: every hook silently does nothing, and SessionStart and UserPromptSubmit put that usage text
into Claude's context, because the host adds their plain-text stdout as context (hooks reference).
No pre-2.1.139 host has been run against this build, so which of those happens is not observed.
`qompack doctor` does not check the host version. The marketplace (§9) needs **2.1.224 or later**
for its `archive` sources. Check with `claude --version`.

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

A release ships each bundle as a `.zip` — all six targets, since C7.5 — plus one `checksums.txt`
over the archives (`devtool bundle --archive`) and a `marketplace.json` generated from it (§9). The
zip holds the bundle at its root, with `bin/` recorded as mode 0755. Task 8 rehearsed the
**directory** form: it verified
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

Once a release is published, the public marketplace in §9 is the way to install; this section is
for a bundle you assembled or downloaded yourself.

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
writes `.claude/settings.json`, `local` writes `.claude/settings.local.json`. `install` and
`uninstall` take `user|project|local`; `update` also accepts `managed`. Task 8 rehearsed
**user** scope on 2.1.263. The V6 live lane rehearsed **local** scope on 2.1.280:
`claude plugin marketplace add <dir> --scope local` ("(declared in local settings)"), then
`claude plugin install qompack@<marketplace> -s local --json`, and `claude plugin list --json`
reported the install with `scope: local` and the project it belongs to. Project and managed scope
are recorded from host docs, not rehearsed.

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
  live session, so `--keep-data` has nothing host-side to keep on a rehearsal host. Where it does
  exist, `uninstall` **without** `--keep-data` removes it: the CLI's help on 2.1.280 documents
  `--keep-data` as "Preserve the plugin's persistent data directory
  (~/.claude/plugins/data/{id}/)". Qompack writes nothing there (no Qompack code reads
  `CLAUDE_PLUGIN_DATA`; your recorded sessions are in `.qompack/`, §6), so what that removes is
  whatever the host itself put in the directory.

On Claude Code 2.1.280, `install`, `update` and `uninstall` accept `--json`. For `install` and
`update` the help reads "Print one machine-readable result line on stdout instead of the human
message (same exit codes; a marketplace-declared command is still shown and must be confirmed —
pass -y when not interactive)", so `--json` alone does not make an install non-interactive; for
`uninstall` it reads "(same exit codes; not with --prune)". `install` and `update` also take
`--accept-command <sha256>`: it accepts the marketplace-declared command "whose sha256 a previous
--json run reported as shownCommand.sha256; counts as -y for exactly that command, for that plugin
and marketplace catalog, and nothing else", and refuses and reports the command again if either
changed. A bundle installed from a local directory declares no command, so Qompack's flow never
needs it. `validate`, `list` and `marketplace list` accept
`--json` too; `marketplace add`, `update` and `remove` do not, and their output is prose on stdout.
Task 8 ran on 2.1.263, where the three commands had no `--json` and there was no
`--accept-command`; both are recorded from the CLI's help, not rehearsed. `-y`/`--yes` is documented as required when stdin or stdout is
not a TTY, but for different prompts: on `install` and `update` it accepts a marketplace-declared
command (a command-source install, or the `headersHelper` that fetches an archive) — a bundle
installed from a local directory declares none — and on `uninstall` it skips the `--prune`
confirmation (CLI help, 2.1.280).

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

Run the checks from a project directory, and start sessions in one. Qompack does not use your home
directory as a project (owner decision D18): a session whose project root is the home directory —
started there, or below a home that is itself a git repository in a directory with no `.git` of its
own — is told on its first line that Qompack is inactive, and records nothing. There
`qompack doctor` reports `scope.root` as `disabled` with the reason, and `fsck`, `backup` and
`self-test` exit 1 ([docs/troubleshooting.md](troubleshooting.md#qompack-is-inactive-in-the-home-directory)).

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
`update` also accepts `-s managed`.

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

`--prune` is recorded from the CLI's help, not rehearsed: on 2.1.280 it "Also remove[s]
auto-installed dependencies that are no longer needed", not the cached payload, and needs `-y` in
non-interactive use. Without `--keep-data`, `uninstall` also removes the host's data directory for
the plugin, `~/.claude/plugins/data/<id>/`, when one exists (§3).

**On Claude Code 2.1.263 the payload cache is retained on both paths** — `--keep-data` and the
default. The two commands are indistinguishable here because no `plugins/data/<id>/` is ever
created by an install. A page that says the default uninstall deletes the payload is wrong for
this version. An uninstall leaves `extraKnownMarketplaces` in place; removing the marketplace is a
separate `claude plugin marketplace remove <name>` (host-output observation).

**On Claude Code 2.1.280 the payload cache is still left behind**, observed by the V6 live lane at
local scope: after `claude plugin uninstall qompack@<marketplace> -s local --json` (`"keptData":
false`) and `claude plugin marketplace remove <marketplace>`, the host left
`plugins/cache/<marketplace>/qompack/<version>/` in place, marked with an `.orphaned_at` file. The
live session had created an empty `plugins/data/qompack-qompack-live/` directory, the
`plugins/data/<id>/` of §3; the run removed it itself before the uninstall, so what an uninstall on
2.1.280 does with that directory was not observed.

**The policy, stated plainly:**

| what | what uninstall does (observed on 2.1.263, and on 2.1.280 where stated) |
| --- | --- |
| the host integration (`enabledPlugins` entry, `plugin list`) | removed |
| the bundle's binaries and manifest under the host's cache | **retained** on both paths; on 2.1.280, left behind and marked `.orphaned_at` |
| host-side plugin data under `~/.claude/plugins/data/<id>/` | not created by an install; once a live session has created it, removed unless `--keep-data` (CLI help, 2.1.280) |
| **`<project>/.qompack/` and `~/.qompack/` — your recorded sessions** | **retained. Always.** |
| `~/.qompack/bin/<sha256>/qompack.exe` — Windows only: the copy of the binary the daemon runs from ([architecture §1](architecture.md#1-process-model)) | retained; a spawn that has to write a new copy prunes every other copy no daemon is running, which never happens after an uninstall, so remove them by hand (below) |

The recorded-sessions row is deliberate and it is not an oversight. `.qompack/` is your data: the captures, the
index, the checkpoints and the diagnostic evidence from your own sessions. Uninstalling a tool is
not the same decision as deleting what it recorded, so uninstall never makes the second decision for
you.

To delete it, delete it yourself:

```sh
rm -rf /path/to/project/.qompack        # one project's store
rm -rf ~/.qompack                       # user-level configuration, logs and, on Windows, bin/
```

On Windows, stop the daemon first (it exits on its own after `runtime.daemon.idleExitSeconds` with
no live session, or end the process whose `pid` is in `.qompack\run\daemon.lock`): a running
executable cannot be deleted. The staged copies are sealed read-only, so PowerShell needs `-Force`:

```powershell
Remove-Item -Recurse -Force $HOME\.qompack\bin     # only the daemon's staged executables
Remove-Item -Recurse -Force $HOME\.qompack          # everything user-level
```

**When staged copies are pruned.** A staged copy is filed under its binary's SHA-256, not under a
version string. When a daemon spawn finds no copy of its own binary that verifies, it writes one
and then removes every other directory under `~/.qompack/bin/` named by a SHA-256, except a copy a
running daemon is executing, which Windows will not delete; the next spawn that writes a new copy
removes it once that daemon has exited (`pruneStaged` in `internal/daemon/spawn_stage.go`). A
spawn whose own copy already verifies prunes nothing. So two builds that both report version
`0.3.0` remove each other's copy: the V6 live lane saw candidate 5's build and candidate 7's build,
both 0.3.0, each prune the other's copy when its session started. Nothing depends on a pruned
copy; the next spawn of that build makes it again. A spawn that finds its own copy held open by
another process without read sharing (Windows) cannot check it, so it runs the plugin's own binary
instead and neither writes nor prunes anything; the next spawn checks the copy again.

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

Recording can be disabled for privacy (`runtime.mode: "off"`, spelled exactly; a value the hooks
cannot apply, such as `"OFF"`, also stops recording but is reported as a configuration failure —
[troubleshooting §6](troubleshooting.md#6-configuration-and-schema-compatibility)) while whatever
was already recorded stays retrievable: retrieval remains scoped to what host permission allows **today**, re-checked at
every request, not at capture time.

## 8. Scope

What is actually verified on which platform is in `docs/release.md` §3, and it is generated from
committed records rather than written by hand. If your platform reads `unknown` there, the plugin
may work perfectly — nobody has measured it, and this page will not pretend otherwise.

## 9. Installing from the public marketplace

**Status: published, and rehearsed.** 0.3.1 is the newest release, published on GitHub with this
marketplace as a release asset, and develop carries the newest release's marketplace as
`.claude-plugin/marketplace.json` (merged by each release's marketplace pull request). The install
below was rehearsed from each release with Claude Code 2.1.293, 0.3.0 on 2026-10-08 and 0.3.1 on
2026-10-10: on Windows in an isolated profile and in a real session at local scope, and on Linux in a
container with no model session (V6 close-out decisions D80 and D85).

**What it is.** One marketplace, `qompack`, with six entries — one per release target — each an
`archive` source: a zip on the GitHub Release, pinned by sha256.

| entry | install it on |
| --- | --- |
| `qompack-linux-amd64` | Linux, x86-64 |
| `qompack-linux-arm64` | Linux, ARM64 |
| `qompack-darwin-amd64` | macOS, Intel |
| `qompack-darwin-arm64` | macOS, Apple silicon |
| `qompack-windows-amd64` | Windows, x86-64 |
| `qompack-windows-arm64` | Windows, ARM64 |

**Install exactly one entry — the one for your machine.** Every entry is the same plugin (its
`plugin.json` name is `qompack`) built for a different target; two installed at once would register
the same seven hooks and the same MCP server twice. Archive sources need **Claude Code 2.1.224 or
later** ("Requires Claude Code v2.1.224 or later"); older versions refuse the entry or fail to load
the marketplace.

Add the marketplace from the repository, then install your machine's entry:

```sh
claude plugin marketplace add --sparse .claude-plugin -- https://github.com/AidanHT/qompack.git
claude plugin install qompack-linux-amd64@qompack -s user
```

**Use `--sparse .claude-plugin`.** The host clones the marketplace's repository into a temporary
directory under `~/.claude/plugins/marketplaces/`, and it needs only the marketplace file. A full
clone also checks out the source tree and its test fixtures, whose deepest paths there come close
to Windows' 260-character limit; past it, the clone fails (`Filename too long`, `Clone succeeded,
but checkout failed`) unless git's `core.longpaths` is on. `--sparse .claude-plugin` checks out only
the marketplace file and the repository root, so it works on every platform and is much faster. The
`--` ends `--sparse`'s list of paths. The marketplace tracks develop: `claude plugin marketplace
update qompack` fetches develop's newest pinned release.

**SSH and the shorthand.** `claude plugin marketplace add AidanHT/qompack` works too, but "GitHub
`owner/repo` shorthand sources clone over SSH by default; set `CLAUDE_CODE_PLUGIN_PREFER_HTTPS=1`
to clone them over HTTPS instead" (plugin-marketplaces, fetched 2026-09-22). Without a GitHub SSH
key it fails even though the repository is public; the `https://…/qompack.git` form avoids that.
Pass `--sparse .claude-plugin` with either form.

**A release's `marketplace.json` asset cannot be added by URL.** Claude Code treats every
`github.com` URL as a git repository: `claude plugin marketplace add
https://github.com/AidanHT/qompack/releases/download/v0.3.0/marketplace.json` tries to clone
`…/marketplace.json.git/` and fails with `repository not found` (observed on 2.1.293). The asset
is still published, as a record of the release's pins.

The entry name is what the host keys the install by: "When a marketplace entry lists the plugin
under a different name, the marketplace entry name is what `enabledPlugins` keys and `/plugin`
use" (plugins-reference). Observed on 2.1.280 with a local probe marketplace, `claude plugin list
--json` reports the install as `qompack-windows-amd64@qompack` while `claude plugin details` names
the plugin `qompack`.

**`claude plugin details` takes the plugin's name, not the entry's.** On 2.1.280, `claude plugin
details qompack-windows-amd64` exits 1 with `Plugin "qompack-windows-amd64" not found`. Give it the
plugin name, `claude plugin details qompack`, or the entry qualified by its marketplace, `claude
plugin details qompack-windows-amd64@qompack`; both print the same page. Its `Description:` line is
the marketplace **entry's** description (it ends "Install this entry on Windows, x86-64."), not
`plugin.json`'s, and its `Source:` line is the entry, `qompack-windows-amd64@<marketplace>`
(observed by the V6 live lane on candidate 7 through a local marketplace holding that one entry).

**What a session shows.** The namespace comes from `plugin.json`'s name, `qompack`, not from the
entry's name. The V6 live lane installed candidate 7 on 2.1.280 through a local marketplace whose
one entry is named `qompack-windows-amd64`, the release entry's name, and a live session listed:

| what | observed under the `qompack-windows-amd64` entry |
| --- | --- |
| MCP server | `plugin:qompack:qompack` |
| tools | `mcp__plugin_qompack_qompack__<tool>`, for example `mcp__plugin_qompack_qompack__recall` |
| slash commands | `/qompack:<command>`: `/qompack:dropped`, `eval`, `pin`, `recall`, `status`, `why` |
| plugin id (`claude plugin list --json`) | `qompack-windows-amd64@<marketplace>` |
| install path | `~/.claude/plugins/cache/<marketplace>/qompack-windows-amd64/0.3.0` |

So the entry's name appears only in the install id, the cache path and the session's
`plugins[].source`; the server, tools and commands are the same as under an entry named `qompack`.
These docs write `/qompack:<command>` throughout. That was observed on Windows from a local
marketplace, not yet from the published marketplace on every target; if your session lists the
commands or tools under another prefix, use the one it lists.

**Checksums.** The host verifies every download against the entry's pin: "If the downloaded file
doesn't match the pin, Claude Code refuses the install and reports `Plugin archive integrity check
failed`." The same digests are in the release's `checksums.txt`; to check a zip yourself before
extracting it by hand:

```sh
sha256sum --ignore-missing -c checksums.txt
```

**Updating.** Each release changes both `plugin.json`'s `version` (the host's update signal) and
the entry's pin:

```sh
claude plugin marketplace update qompack
claude plugin update qompack-linux-amd64@qompack -s user
```

`marketplace update` "refresh[es] marketplaces from their sources": it fetches develop again, and
with it develop's newest pinned release. To remove the marketplace, run `claude plugin marketplace
remove qompack` (which, per the host docs, also uninstalls the plugins installed from it; your
`.qompack/` data is not in the plugin cache, §6).

**The executable bit: kept on Linux, unverified on macOS.** The zip records `bin/qompack` as 0755.
Installed from the 0.3.0 release's `qompack-linux-amd64` entry with Claude Code 2.1.293, the
installed `bin/qompack` kept mode `-rwxr-xr-x` and ran (D80). No macOS host has installed it. If a
hook reports `permission denied` on macOS, `claude plugin list --json` gives the `installPath`, and
`chmod +x <installPath>/bin/qompack` is the workaround. `qompack doctor` run with
`CLAUDE_PLUGIN_ROOT=<installPath>` reports this case on its own `version.pluginRoot` row:
`degraded`, "set but the binary is not executable".

## 10. Unsigned binaries: Gatekeeper, SmartScreen and Defender

Qompack's binaries are not code-signed: no Authenticode signature on Windows, and no Apple
Developer ID signature or notarization on macOS ([release §7](release.md#7-not-claimed)). What you
can check instead is the digest: every release zip is listed in that release's `checksums.txt`,
every file of a bundle in the bundle's own `checksums.txt` (§1), and the release workflow attests
build provenance for the archives ([release §1](release.md#1-procedure), step 5). None of the
behaviour below has been observed with a published Qompack release; it is what the two operating
systems document for unsigned downloads. What Claude Code shows when the operating system refuses a
hook's binary has not been observed either.

**macOS: Gatekeeper.** macOS marks a file that a browser or another quarantine-aware app downloaded
with the `com.apple.quarantine` extended attribute, and files extracted from such an archive carry it
too. Gatekeeper refuses to run a quarantined executable that is not notarized, with a dialog saying
that the file cannot be opened because Apple cannot check it for malicious software (the wording
varies with the macOS version). A file without the attribute is not checked this way. Whether
Claude Code's own download of a marketplace archive sets the attribute has not been observed; a zip
you downloaded in a browser and extracted yourself does carry it. To allow the binary, first check
its archive against the release's `checksums.txt`, then remove the attribute from the installed
binary (`claude plugin list --json` gives the `installPath`):

```sh
shasum -a 256 qompack-plugin-<version>-darwin-arm64.zip   # compare with the release's checksums.txt
xattr -l <installPath>/bin/qompack                         # lists com.apple.quarantine if it is set
xattr -d com.apple.quarantine <installPath>/bin/qompack
```

Alternatively, after the first refusal, System Settings › Privacy & Security offers **Open Anyway**
for the refused file. Either way the decision is yours: it tells macOS to run this one file without
the notarization check. The executable-bit question of §9 is separate, and so is its workaround.

**Windows: SmartScreen and Defender.** Windows adds a Mark of the Web to a file a browser downloads,
and Explorer carries it to the files it extracts from such a zip. SmartScreen checks marked files
when they are opened from Explorer, and an unsigned, rarely seen executable gets the "Windows
protected your PC" screen. Whether a binary that Claude Code downloaded and starts itself is checked
this way has not been observed. To clear the mark on a zip you have checked, before extracting it:

```powershell
Get-FileHash -Algorithm SHA256 .\qompack-plugin-<version>-windows-amd64.zip   # compare with checksums.txt
Unblock-File -Path .\qompack-plugin-<version>-windows-amd64.zip
```

(the file's Properties dialog has the same **Unblock** box). Microsoft Defender Antivirus scans
the binary whatever its mark, and its machine-learning detection has flagged development builds of
Qompack as `Trojan:Win32/Bearfoos.A!ml` and `B!ml` (V6 close-out decision D32).
[Troubleshooting §7](troubleshooting.md#windows-defender-flags-qompackexe) says how to recognise
that, how to check the flagged file against the release's checksums, and how to report a false
positive to Microsoft yourself. Qompack never adds a Defender exclusion and needs none, and this
release makes no claim that Microsoft has reviewed or cleared its binaries.

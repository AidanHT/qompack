# Qompack

Qompack is a Go sidecar plugin for Claude Code. It records what a session produces — tool results,
prompts and turn boundaries — into a local, append-only store under the project's `.qompack/`
directory through the host's hooks (`SessionStart`, `PostToolUse`, `UserPromptSubmit`, `Stop`,
`SubagentStop`, `PreCompact`, `SessionEnd`, as registered in `plugin/hooks/hooks.json`); it writes a
checkpoint at `PreCompact`; after a compaction it injects a bounded rehydration payload through
`SessionStart` with `source=compact`; and it exposes retrieval, negative knowledge and
observability through an MCP server (`qompack mcp`, registered by `plugin/.mcp.json`) and a set of
slash commands. The tools it advertises are the ones listed in
[docs/mcp-tools.md](docs/mcp-tools.md); the commands are the ones listed in
[docs/commands.md](docs/commands.md). Both pages are generated from the shipped code, so they are
the inventory — this page does not restate a count.

It is a sidecar in the strict sense. It does not rewrite the host's conversation history, does not
evict anything from the native context, and performs no network I/O of any kind.

## Status: pre-release

Qompack has not been released. Two version numbers exist and they do not agree:

- the last git tag in this repository is `v0.2.0`;
- `plugin/.claude-plugin/plugin.json` declares version `0.1.0`.

Both are reported here as they stand. Release and versioning are SP-17 deliverables and neither
number is changed by this page.

## Supported environments

What is actually built and tested is what `.github/workflows/ci.yml` runs. Every job pins Go
`1.26.6` (the exact patch `go.mod`'s `toolchain` line names; a guard test fails the build if the two
disagree):

| Job | Runners | What it does |
|---|---|---|
| `verify` | ubuntu-latest | `devtool fmt-check`, `devtool lint`, `go vet ./...`, `go build ./...`, commit-message checks |
| `lint-windows` | windows-latest | the whole `devtool lint` again, because several sub-checks are blind on Linux |
| `test` | ubuntu-latest, macos-latest, windows-latest | the whole tree except `test/e2e`; `-race` on ubuntu and macos, `-count=2` on Windows |
| `test-e2e` | ubuntu-latest, macos-latest, windows-latest | `test/e2e` alone, on its own runner |
| `timing` | ubuntu-latest, macos-latest, windows-latest | the wall-clock-judged tests by name, one package at a time, on an otherwise idle runner |
| `bench-gate` | ubuntu-latest, macos-latest, windows-latest | the hot-path budget gate |
| `crossbuild` | ubuntu-latest | `devtool build-all` — six targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64 |
| `cover`, `replay-gate`, `plugin-validate`, `security`, `docs` | ubuntu-latest | coverage floors, the replay gate, the plugin bundle check, `govulncheck` and the import allowlist, and the generated-document drift check |

So: Linux, macOS and Windows are exercised in CI on the runners' own architectures, and six
GOOS/GOARCH pairs are cross-compiled.

**What that does not cover.** Compatibility with an *installed* Claude Code host is
`implemented_unverified` (`plans/V5-report.md` §24, item B01). The host's own validator accepts the
committed bundle where it sits in this repository, but the plugin has not been installed into a
host and run from there, so installed manifest resolution, `${CLAUDE_PLUGIN_ROOT}` expansion and
launcher discovery inside a live session are unverified. Verifying it means installing the bundle
in a real Claude Code host and running a session against it.

There is also no installable release artifact yet. Planned (SP-17): `docs/install.md` and
`docs/security.md`, plus the packaged bundle and `docs/release.md`. None of those files exists; they
are named here as plain text, not as links, on purpose.

## Building from source

```
go build ./cmd/qompack
```

That produces a single binary named `qompack` (`qompack.exe` on Windows) in the current directory.
It is the whole product: the hooks, the MCP server and the resident daemon are all subcommands of
it.

The plugin bundle lives in `plugin/`:

```
plugin/
  .claude-plugin/plugin.json   name, version, description, homepage
  .mcp.json                    registers the `qompack` MCP server as `${CLAUDE_PLUGIN_ROOT}/bin/qompack mcp`
  hooks/hooks.json             the seven hook registrations, each invoking a `qompack` subcommand
  commands/*.md                the slash commands
```

Every generated file in that tree refers to the binary as `${CLAUDE_PLUGIN_ROOT}/bin/qompack`, which
the host expands to the installed plugin directory. All four files are generated from one typed
value in `internal/pluginmanifest`, and

```
go run ./tools/devtool plugin-validate
```

regenerates them and checks the committed tree against the result, which is the bundle check CI
runs.

## Configuration

Configuration is documented in [docs/config-reference.md](docs/config-reference.md), which is
generated from `config.Defaults()` and gated in CI — do not hand-edit it. In short: values resolve
through five layers (defaults → `~/.qompack/config.json` → `<project>/.qompack/config.json` →
`QOMPACK_*` environment → `--set <dotted.key>=<value>`), the merge is deep and per leaf, an invalid
value is never fatal (the leaf falls back to its default, the violation is reported and recorded in
`.qompack/state/config-violations.json`), and an unknown key produces a warning rather than an
error. `qompack config print --provenance` shows the effective value of every key and where it came
from.

## What ships off, and why you should leave it off

A number of switches exist in the configuration and default to **off**. They are not
feature-flagged experiments you are invited to try: each is refused until the gate named beside it
passes, and enabling one is unsupported.

The `runtime.migration` block (see `plans/MIGRATION-EVIDENCE.md`, "Disabled capabilities and their
switches"):

| Key | Default | State |
|---|---|---|
| `runtime.migration.capture.rawEvidence` | `false` | refused until the SP-20 M1 gate passes |
| `runtime.migration.publication.durableFrontier` | `false` | refused until the SP-20 M1 gate passes |
| `runtime.migration.replacement.newResult` | `false` | refused until the SP-21 M4 gate passes |
| `runtime.migration.compaction.automaticVeto` | `false` | refused; the recovery/proactive distinction is unverified |
| `runtime.migration.experiments.enabled` | `false` | refused until the SP-15/SP-16 gates pass |
| `runtime.migration.compaction.blockManualCompact` | `false` | hardwired false — a manual `/compact` is never blocked; the key exists only to say so |
| `runtime.migration.reinjection.sessionStartCompact` | `true` | the one switch on by default: the tested injection adapter, and the kill switch for injection without touching recording |

The `runtime.phase7` block — `reuse.scopedCandidates`, `reuse.warmPrior`,
`retrieval.demandPromotion`, `retrieval.reminders`, `filters.segmentBloom` — is off in the same way,
each refused until its own SP-16 M6 gate passes. `runtime.telemetry.enabled` is hardwired off, and
`runtime.selection.submodularEnabled` and `runtime.selection.loopWarningsEnabled` are off as
ship-order gates.

A `true` written against a refused switch falls back to the default and warns. Both blocks carry a
`settingsVersion` (`runtime.migration.settingsVersion`, `runtime.phase7.settingsVersion`): a config
file written for a **newer** version has the whole block reset to defaults, so switches a future
build might add stay off in this one rather than being read by accident.

`plans/V5-report.md` §24 records the same thing for SP-21's surface: the admission gate is off (a
zero gate refuses; explicit opt-in only), replacement is off, and the host allowlist is empty. Those
are recorded as *disabled*, never as *passed*.

## Where to read next

- [docs/architecture.md](docs/architecture.md) — the actual contracts: process model, write set,
  identity, durability, retrieval, rehydration, and what is not supported
- [docs/adr/README.md](docs/adr/README.md) — every architecture decision record, with its status
- [docs/config-reference.md](docs/config-reference.md) — every configuration key (generated)
- [docs/commands.md](docs/commands.md) — the slash commands (generated)
- [docs/mcp-tools.md](docs/mcp-tools.md) — the MCP tools (generated)
- [LICENSE](LICENSE)

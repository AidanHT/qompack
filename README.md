# Qompack

Qompack is a Go sidecar plugin for Claude Code. It records what a session produces — tool results,
prompts and turn boundaries — into a local store under the project's `.qompack/` directory
(checkpoints, pins and the elimination bloom are append-only-protected; retention and GC do rewrite
other parts, see [docs/architecture.md](docs/architecture.md)) through the host's hooks
(`SessionStart`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `PreCompact`,
`SessionEnd`, as registered in `plugin/hooks/hooks.json`); it writes a
checkpoint at `PreCompact`; after a compaction it injects a bounded rehydration payload through
`SessionStart` with `source=compact`; and it exposes retrieval, negative knowledge and
observability through an MCP server (`qompack mcp`, registered by `plugin/.mcp.json`) and a set of
slash commands. The tools it advertises are the ones listed in
[docs/mcp-tools.md](docs/mcp-tools.md); the commands are the ones listed in
[docs/commands.md](docs/commands.md). Both pages are generated from the shipped code, so they are
the inventory — this page does not restate a count.

It is a sidecar in the strict sense. It does not rewrite the host's conversation history, does not
evict anything from the native context, and performs no network I/O of any kind.

## Status: release candidate

Qompack has not been released. Release **0.3.0** (V6 close-out decision D1) is being cut from
candidate 6, `verify/v6` commit `99d0b18` ("freeze close-out candidate 6"). Its six per-target
bundles are stamped `0.3.0` at link time by `devtool bundle --version 0.3.0`, so an installed bundle
reports `0.3.0` from `qompack version`, its `plugin.json` and its `BUNDLE.json` alike. Nothing has
been tagged, published or listed in a marketplace; [docs/release.md](docs/release.md#1-procedure)
§1 is the procedure, and each of its outward steps waits for the candidate's evidence to be
complete.

The version numbers in this source tree do not agree yet, and are reported as they stand: the last
git tag is `v0.2.0`, an internal verification checkpoint that was never a release, and
`internal/core.Version` and `plugin/.claude-plugin/plugin.json` still declare `0.1.0`. Both move to
`0.3.0` in the release's own version commit (release §1, step 1). Until that commit lands, a binary
from a plain `go build ./cmd/qompack` reports `0.1.0` from `qompack version`.

**What 0.3.0 is.** A local recorder and retriever for Claude Code sessions: it keeps a durable record
of what the hooks deliver, seals a checkpoint when the host is about to compact, puts a bounded
rehydration block (at most 9,500 characters, inside the host's 10,000-character cap) into the
session after the host's compaction, names what did not fit, and answers recall, expansion and
negative-knowledge questions through its MCP tools and slash commands. It runs on your machine
only.

**What 0.3.0 is not.**

- **It does not compact anything.** Compaction is the host's. Qompack cannot request, veto, shape or
  time a compaction, and it sends the host's summarizer no instructions: its `PreCompact` answer is
  the empty object ([docs/cannot-do.md](docs/cannot-do.md)).
- **It is not cache-aware.** It does not observe, model or manage the host's prompt cache, and it
  infers no cache state from elapsed time.
- **It makes no claim that it improves recovery after a compaction, task success or constraint
  retention.** The release's pre-registered live evaluation can show that Qompack does not make work
  after a compaction worse; a claim of improvement would need that outcome's 95 % lower bound above
  zero (`plans/sdd/V6-closeout/eval/preregistration.md`, amendment A8). No page of these docs makes
  such a claim.
- **It makes no performance or storage promise** on your machine
  ([docs/cannot-do.md §4](docs/cannot-do.md#4-guarantees-qompack-does-not-make)), and its binaries
  are not code-signed
  ([docs/install.md §10](docs/install.md#10-unsigned-binaries-gatekeeper-smartscreen-and-defender)).

## Supported environments

**Claude Code 2.1.139 or later** is required for any install: every hook is exec form, and 2.1.139
added the hook `args` field that form needs. Installing from the marketplace
needs **2.1.224 or later**. The host version this release was tested with is **Claude Code
2.1.280**. See [docs/install.md](docs/install.md).

The following checks are configured in `.github/workflows/ci.yml`. Go jobs pin
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
| `release-dry-run` | ubuntu-latest | `devtool release-check` without a tag, then the six archives and the marketplace document |

`test-e2e`, `timing`, `cover` and `bench-gate` run with `QOMPACK_NONREFERENCE_DISK=1`: a hosted
runner's disk is not the reference platform, so its fsync-bound timing rows are reported, not gated
(owner decision Q1), while every structural check in those rows still gates.

**What is verified where, for candidate 6.**

| Where | What the evidence shows |
|---|---|
| Hosted CI, ubuntu-latest (x86-64) and macos-latest (macOS 26, arm64) | `ci.yml` run `36955046276` on `99d0b18`: `test`, `test-e2e`, `timing` and `bench-gate` passed on both, as did `verify`, `lint-windows`, `cover`, `crossbuild`, `replay-gate`, `plugin-validate`, `security`, `docs`, and Windows' `test-e2e`, `timing` and `bench-gate`. Two jobs failed, `release-dry-run` and `test (windows-latest)`, and each must be fixed or dispositioned before the release (C7.2). The nightly run `36955043924` on the same commit passed all 31 jobs: the Windows race lane, the product-child race lane, the recorded replay, `bench-deep` on three OSes and 25 fuzz targets. |
| Windows, the reference host | The quiet hot-path run (C5.1) passed against Windows' 50 ms budgets: B-A p99 30.7 ms, B-B p99 22.5 ms, measured on candidate 5 with the store under a path excluded from Windows Defender scanning (decisions D53(b), D53(h), D32). |
| Linux | The fsync-bound rows, B-A and B-B, are **not verified in target** (D53(b)). The only local Linux is a Docker Desktop/WSL2 container whose fsync is about 40 times slower than a hosted ubuntu runner's at the median, and hosted runner figures never become constants (Q1). The Linux budget stays 15 ms. In the container the daemon degrades as designed: hooks switch to spool submode and nothing is lost ([docs/troubleshooting.md §7](docs/troubleshooting.md#7-daemon-problems)). |
| Installed in Claude Code | windows/amd64 only, on Claude Code 2.1.280, in sessions run by an agent on the owner's machine (decision D3: agent-executed, never human UAT). No other target has been installed into Claude Code. |
| macOS | The test suites run natively on hosted macos-latest (arm64), above. No Mac has installed the plugin into Claude Code, and darwin/amd64 is cross-compiled only. Whether the marketplace install keeps `bin/qompack` executable, and what Gatekeeper does with the unsigned binary, have not been observed. |
| linux/arm64 and windows/arm64 | Cross-compiled and bundled; no test has run on either architecture and neither has been installed. |
| Every target's bundle | Built twice from the candidate, byte-identical, and accepted by `claude plugin validate` (2.1.280) where it sits, which is a manifest check and not an install. |

The generated scope table in [docs/release.md](docs/release.md#3-supported-scope) §3 is the
release's per-target claim, raised only by committed records, and
[the capability table beside it](docs/release.md#capability-status-at-030) gives every capability's
0.3.0 status.

Packaging and release tooling: [docs/install.md](docs/install.md) and
[docs/security.md](docs/security.md) cover installing the bundle and its security and recovery
posture, and [docs/release.md](docs/release.md) covers how a release is cut and what it claims. The
bundle is assembled by `go run ./tools/devtool bundle`; no release has been published from this
repository yet, and the tag-triggered release workflow has never run
([docs/release.md](docs/release.md#7-not-claimed) §7).

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
  .mcp.json                    registers the `qompack` MCP server: `${CLAUDE_PLUGIN_ROOT}/bin/qompack`, args `["mcp"]`
  hooks/hooks.json             the seven hook registrations, each launching the binary with a subcommand
  commands/*.md                the slash commands
```

Every hook and the MCP server are exec form: `command` is exactly the bundled executable and `args`
the subcommand, so the host spawns the binary directly and no shell — Git Bash, `sh` or PowerShell —
ever parses the string. The committed tree is the linux/darwin rendering, naming
`${CLAUDE_PLUGIN_ROOT}/bin/qompack`; each release bundle is rendered for its own target, and the
windows bundles name `bin/qompack.exe`. All four files are generated from one typed value in
`internal/pluginmanifest`, and

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
error. The hooks follow the same rules, except that two kinds of problem stop recording instead:
input they cannot read safely (a config file that does not parse, is not a plain file or is over its
size bound, or an oversized `QOMPACK_*` or `--set` value), and a setting of `runtime.redact` or
`runtime.mode` that cannot be applied as written, where a fallback would record under a privacy
policy you did not write, or record while you were switching recording off. `qompack config print --provenance` shows the effective value of
every key and where it came from, and `qompack self-test`'s `config.capture` row says whether the
hooks can load it ([docs/troubleshooting.md](docs/troubleshooting.md#6-configuration-and-schema-compatibility)).

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

- [docs/user-guide.md](docs/user-guide.md) — what each command, slash command and tool reports,
  and how to read the qualifications those reports carry
- [docs/troubleshooting.md](docs/troubleshooting.md) — what each observable failure means and what
  to do about it
- [docs/cannot-do.md](docs/cannot-do.md) — the capabilities this build does not have, and where
  each limit is recorded
- [docs/upstream-issues.md](docs/upstream-issues.md) — the host changes that would lift those
  limits, as prepared proposals
- [docs/uat.md](docs/uat.md) — the acceptance scenarios and the evidence a run has to leave
- [docs/architecture.md](docs/architecture.md) — the actual contracts: process model, write set,
  identity, durability, retrieval, rehydration, and what is not supported
- [docs/adr/README.md](docs/adr/README.md) — every architecture decision record, with its status
- [docs/config-reference.md](docs/config-reference.md) — every configuration key (generated)
- [docs/commands.md](docs/commands.md) — the slash commands (generated)
- [docs/mcp-tools.md](docs/mcp-tools.md) — the MCP tools (generated)
- [LICENSE](LICENSE)

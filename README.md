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

Qompack has not been released. Release **0.3.0** (V6 close-out decision D1) is cut from release
candidate 8 (decision D58(e)), commit `3ec62ad2`, whose frozen bundles and evidence are recorded in
`plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md`. The release tags candidate 8, or a descendant whose
changes reach no bundle. Its six per-target bundles are stamped `0.3.0` at link time by
`devtool bundle --version 0.3.0`, so an installed bundle reports `0.3.0` from `qompack version`,
its `plugin.json` and its `BUNDLE.json` alike. Nothing has been tagged, published or listed in a
marketplace; [docs/release.md](docs/release.md#1-procedure) §1 is the procedure.

The source tree declares the same version: `internal/core.Version` and
`plugin/.claude-plugin/plugin.json` read `0.3.0` from the release's own version commit on (release
§1, step 1), so a binary from a plain `go build ./cmd/qompack` also reports `0.3.0`. The
repository's last git tag, `v0.2.0`, was an internal verification checkpoint and never a release.

**What 0.3.0 is.** A local recorder and retriever for Claude Code sessions: it keeps a durable record
of what the hooks deliver, seals a checkpoint when the host is about to compact, puts a bounded
rehydration block (at most 9,500 characters, inside the host's 10,000-character cap) into the
session after the host's compaction, names what did not fit (in the block, or, at a configured
budget too small for even the shortest loss notice, in the drop report and `LOUD.log`, with nothing
injected), and answers recall, expansion and negative-knowledge questions through its MCP tools and
slash commands. It runs on your machine only.

**What 0.3.0 is not.**

- **It does not compact anything.** Compaction is the host's. Qompack cannot request, veto, shape or
  time a compaction, and it sends the host's summarizer no instructions: its `PreCompact` answer is
  the empty object ([docs/cannot-do.md](docs/cannot-do.md)).
- **It is not cache-aware in any way that reaches the host.** It cannot see, keep warm or change
  the host's prompt cache. Its internal scheduler does estimate a cache state (warm, expiring or
  cold) from the time since the last API request and the configured TTL, and uses that estimate
  only to choose its own idle background work; the estimate is not an observation, and a
  checkpoint records the cache state as `unknown`
  ([docs/cannot-do.md](docs/cannot-do.md#no-inference-of-cache-state-from-arbitrary-elapsed-time)).
- **It makes no claim that it improves recovery after a compaction, task success or constraint
  retention.** The release's pre-registered live evaluation is designed to test whether Qompack
  makes work after a compaction worse; this release's run did not settle that question (its verdict
  is inconclusive, see below). A claim of improvement would need that outcome's 95 % lower bound
  above zero (`plans/sdd/V6-closeout/eval/preregistration.md`, amendment A8). No page of these docs
  makes such a claim.
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
| `release-dry-run` | ubuntu-24.04 | the six bundles at the release version (`internal/core.Version`), kept as a workflow artifact; then `devtool release-check` without a tag, then the six dry-run archives and the marketplace document |

`test-e2e`, `timing`, `cover`, `bench-gate` and `release-dry-run` run with
`QOMPACK_NONREFERENCE_DISK=1`: a hosted runner's disk is not the reference platform, so its
fsync-bound timing rows are reported, not gated (owner decision Q1), while every structural check in
those rows still gates.

**What is verified where, and for which candidate.** Candidate 8 (`3ec62ad2`) is candidate 7
(`d20309c0`) plus product fixes, with their tests, among them the drain pass budget, the session
registry, the checkpoint writer, the hook configuration path, the contract reading, the rehydration
block and the command client (decisions D58(e), D60(f), D61). Its binaries are therefore new: no
byte comparison carries earlier candidates' machine evidence to it. The table below records
candidate 8's own evidence, from `plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md` and decisions D75,
D76 and D77. Its night chain on the frozen tree ran 19 steps, every one on AC power: 16 passed,
among them the Windows timing set and X11, the Windows and Linux `-race` lanes, two reproducible
bundle builds, the quiet C5.1 run on Windows and `release-check --tag v0.3.0` (all 18 of its steps
`PASS`, none skipped). The other three were Linux container timing steps: two failed only their
fsync-bound rows, and the quiet C5.1 run there is reported only (the Linux row below). The C5.2
night re-measures every benchmark in full against `cf31e01` in package chunks that may span more
than one night (D62(b), D65(b)). Six of its eight chunks ran complete on candidate 8's night: no row
is slower than the base with significance except negknow's `BenchmarkOpen` on Windows (1.16x, inside
its 300 ms budget and off the hot path) and symbols' `BenchmarkEnclosing_100KB` (1.011x), both
recorded and not acted on (D75(b)). The Linux checkpoint and other chunks and the C1.16 rig
re-measure, whose figure [docs/architecture.md](docs/architecture.md#7-checkpoint-and-rehydration)
restates (D62(c), D65(a)), were measured on the C5.2 night of 2026-10-07/08: both Linux chunks
passed, with `BenchmarkFinalize` 1.32x and negknow `BenchmarkOpen` 1.34x on the container, each
inside its budget as decision D54 recorded (D78(a)); and the rig's compact answer under same-session
ingest was the rehydration every time, at p99 143 ms with no extra load and 417 ms under its
in-process fsync and CPU co-load (D78(e)). The short live re-check ran 20 real sessions on the
frozen bundle, and every scenario passed (D76). The pre-registered live evaluation, C5.5, ran on the
frozen bundle, and its decision reads "inconclusive — interval [-0.214, 0.214] straddles -0.200"
(D77). At this sample size that is the expected verdict, by design, and it is not evidence that
Qompack adds nothing (amendment A8, item 3). The [release notes](docs/release-notes/v0.3.0.md) give
its figures.

| Where | What the evidence shows |
|---|---|
| Hosted CI, ubuntu-latest (x86-64), macos-latest (macOS 26, arm64) and windows-latest | On candidate 8, `ci.yml` run `37562946379` concluded success: every job passed, `test (macos-latest)` and `test (windows-latest)` on its second attempt, a re-run of the failed jobs on the same commit (D75(c)). Attempt 1's two reds each have a recorded disposition. `test (macos-latest)` failed a fixture-sanity count in `TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes`, a test defect: the fixture does not wait for the live worker to own the delivery, and when the settle's replay owns it the product publishes both Reads in order and drops nothing. `test (windows-latest)` ended `internal/daemon`'s `-count=2` binary at 1,185 s with a runtime crash dump whose first lines the job's summary does not keep; its cause is undetermined, it did not recur, and the Windows and Linux `-race` trees passed on the same tree. `release-dry-run` passed, and its release-version bundles were byte-identical to candidate 8's frozen ones in both directions: all 91 files, every `bin/`, the six zips and `checksums.txt` (`plans/sdd/V6-closeout/phase3/c8/hosted-release-bundles.txt`, D53(h)(4)). The nightly run `37562945914` passed. |
| Windows, the reference host | Candidate 8's quiet hot-path run (C5.1, `plans/sdd/V6-closeout/phase3/c8/quiet/`, on AC power) passed: B-A p99 16.4 ms and B-B p99 11.3 ms against 50, B-E p99 166.8 ms against 2,000 (B-E_cpu p99 46.9 ms), and B-F p99 73.7 ms against 250 over 2,000 tool uses (n=200). The whole tree passed under `-race`, as did the isolated wall-clock rows in `internal/store`, `internal/negknow`, `internal/mcp`, `internal/daemon`, `test/integration` and `test/e2e`. X11 (`TestV3_HotPathUnchangedWithLedgerResident`, the hot path over a project holding 2,000 tool uses and 41 MB of tool output, with and without a 5,000-entry elimination ledger) passed when run alone on AC (`plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md`, D75). A run on battery is not a reference measurement, neither a pass nor a fail, because on battery Windows applies slower CPU, PCIe and NVMe power policies (D57(d)). On battery the hot path switches to spool submode as designed and nothing is lost (D53(c)). Windows reference timings are taken on AC with the store under a path excluded from Windows Defender scanning (decisions D32, D53(h)). |
| Linux | In the local Docker Desktop/WSL2 container, as a non-root user, the whole tree, `test/e2e` and the product-child lane passed under `-race` on candidate 8. The fsync-bound rows, B-A and B-B, are **not verified in target** (D53(b)): on candidate 8's night the container's two hot-path tests, `TestIntegration_HotPathWarmWithRealResidentState` and X11, failed B-A and B-B alone against 15 ms, with 592 and 590 deliveries deferred to the client spool and 0 lost, while every other row of both timing steps passed; its quiet C5.1 run is reported only, for the same reason (D75(a)). Hosted runner figures never become constants (Q1), and the Linux budget stays 15 ms. In the container the daemon degrades as designed: hooks switch to spool submode and nothing is lost ([docs/troubleshooting.md §7](docs/troubleshooting.md#7-daemon-problems)). |
| Installed in Claude Code | windows/amd64 only. Candidate 8's frozen bundle was loaded into Claude Code 2.1.280 through `--plugin-dir` for its live re-check, 20 real sessions in which every scenario passed and 650 hook calls drew no host-reported failure or timeout (D76, D53(i)), and for the Qompack arm of its live evaluation (D77). The live lanes on candidates 3, 4 and 7 installed those candidates' bundles. All of these were run by an agent on the owner's machine (decision D3: agent-executed, never human UAT). No other target has been installed into Claude Code. |
| macOS | The test suites run natively on hosted macos-latest (arm64), above. No Mac has installed the plugin into Claude Code, and darwin/amd64 is cross-compiled only. Whether the marketplace install keeps `bin/qompack` executable, and what Gatekeeper does with the unsigned binary, have not been observed. |
| linux/arm64 and windows/arm64 | Cross-compiled and bundled; no test has run on either architecture and neither has been installed. |
| Every target's bundle | Candidate 8's six bundles were built at its freeze, and the windows/amd64 bundle was accepted by `claude plugin validate --strict --json` (2.1.280) where it sits, which is a manifest check and not an install (`plans/sdd/V6-closeout/phase3/c8/host-validate.txt`). Its night built the six bundles twice more, byte-identical across all 91 files (`plans/sdd/V6-closeout/phase3/c8/bundle-diff.txt` is empty), and the hosted release-version bundles equal the frozen ones byte for byte (above). |

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

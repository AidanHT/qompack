# Qompack — 00 Global Architecture

**Status:** normative. Every subplan (`plans/NN-*.md`) conforms to this document.
**Source of truth for requirements:** `Qompack.md` v1.2. Where this document and `Qompack.md`
disagree on *what* to build, `Qompack.md` wins. This document decides *how*.

---

## 0. How to use this document

This is the contract that lets 18 subplans be built — several of them simultaneously, by
different agents, in different worktrees — and still compose. It fixes four things:

1. **The runtime substrate** (§2) — language, process model, IPC, dependency policy.
2. **The physical layout** (§3, §4) — where every file lives, source vs. runtime data.
3. **The seams** (§5) — exact typed signatures. A subplan may change the *inside* of a package
   it owns. It may not change an interface in §5 without an amendment commit to this file that
   lands on `develop` before the change.
4. **The process** (§6–§13) — tests, benchmarks, CI, git, commits, config, degradation.

**Reading order for an implementing agent:** §1 (decisions), then §5 for the packages your
subplan owns *and* every package it consumes, then §6–§10, then your own subplan file.

**Amendment rule.** If an interface in §5 is wrong, you do not work around it. You open a
branch `arch/<short-reason>` off `develop`, change §5, get it merged, and rebase. Silent
divergence from §5 is the single failure mode that makes parallel waves worthless.

---

## 1. Decision summary

| # | Decision | Rationale (short) | Section |
|---|---|---|---|
| D1 | **Go 1.26+**, single static binary, `CGO_ENABLED=0` | Fastest realistic cold start of any mainstream managed toolchain; trivial cross-compile to 6 targets; pure-Go zstd; no runtime to ship | §2.3 |
| D2 | **Resident daemon + thin client**, *both* | Neither alone meets 15 ms p99 on Windows. Compiled client floors the spawn cost; daemon removes per-hook state load/serialize (~70 KB of sketches + DAG) which is the real budget killer | §2.1–§2.4 |
| D3 | IPC = **named pipe (Windows) / Unix domain socket (POSIX)**, NDJSON framing, 1-byte ACK | No TCP (no ports, no firewall prompts, no cross-user exposure). NDJSON is debuggable with `type`/`cat` | §2.4 |
| D4 | **Client-side spool fallback** (`.qompack/spool/*.ndjson`) | The queue-and-drain degradation of §8.1 must exist below the daemon, not inside it | §2.4, §12 |
| D5 | **Hand-rolled CLI dispatch**, no cobra/urfave | The hot path must not pay for a command framework's init | §2.5 |
| D6 | **Hand-rolled MCP server** (JSON-RPC 2.0 / stdio), conformance-tested | Pre-1.0 SDK churn vs. ~400 LOC we fully control; we need `_meta` for ephemeral tagging | §5.17 |
| D7 | **Sketches, FastCDC, Sequitur, BOCD implemented in-house** | We need versioned serialization, resize-and-rebuild, and merge semantics no library exposes | §2.5, §5.8 |
| D8 | **`.qompack/` resolved from the hook payload's project root**, never from the plugin install dir | One store per project, matching §7.4 | §3.3 |
| D9 | **Conformance suites** (`RunXContractTests`) ship with every interface in SP-01 | This is the mechanism that makes same-wave parallel builds safe | §5.22, §6.4 |
| D10 | **No network I/O anywhere in the plugin**, ever | Security posture is "reads your code, writes to one gitignored directory, talks to nobody" | §3.3, §13 |
| D11 | Cache multipliers `r`, `w`, TTL, and every §11.3 budget are **config keys with a lint gate** forbidding literals | §12 risk "Cache multipliers change" | §11.6 |
| D12 | Three operating modes: `full` → `degraded-passive` → `off`, plus hot-path `sync`/`spool` submode | §12 contract-monitor mitigation, §8.1 overrun fallback | §12 |

---

## 2. Language, runtime, and toolchain

### 2.1 The latency problem, quantified

`Qompack.md` §8.1 sets the hard constraint: *"the hook is on the hot path of every tool call.
Target < 15 ms p99."* §11.3 restates it as a guardrail. Two independent costs threaten it, and
they are usually conflated:

**Cost 1 — process creation.** Claude Code invokes a hook as `command` in a shell. Every hook
firing is at minimum one `CreateProcess`/`fork+exec`. Measured floors on the target platforms:

| Runtime | Linux | macOS | Windows 11 (Defender on) |
|---|---|---|---|
| Trivial compiled binary (Go/Rust, static) | 1–3 ms | 2–5 ms | 6–20 ms |
| Node 20 (`node -e ''`) | 25–40 ms | 30–55 ms | 40–90 ms |
| Python 3.12 (`python -c ''`) | 15–30 ms | 20–45 ms | 35–80 ms |
| Bun / Deno | 8–20 ms | 10–25 ms | 20–45 ms |

Windows is the binding constraint and Windows is the dev machine. Any interpreted runtime is
disqualified on this row alone: Node's *floor* exceeds the entire budget before Qompack executes
a single instruction.

**Cost 2 — cold state.** This is the cost people forget, and on a store-backed plugin it is
larger than Cost 1 after an hour of session. A stateless per-hook process must, on *every*
tool call:

- load `sketches/tried.bloom` (~12 KB), `touch.cms` (~54 KB), `explore.hll` (~2 KB) — ~68 KB of
  deserialization, then re-serialize and atomically rewrite all three,
- load enough of `dag/deps.jsonl` to append coherently and to answer `CrossingEdges`,
- load Sequitur grammar state and BOCD run-length posterior,
- open, append, and fsync 3–5 index files.

Even at NVMe speeds, that is 5–15 ms of syscalls and allocation on Linux and materially worse on
Windows (`NtCreateFile` + Defender filter driver per open). Combined with Cost 1, a stateless
compiled binary lands around 15–35 ms p99 on Windows — *over budget*, and it gets worse as the
session grows because the CMS and DAG grow.

**Conclusion.** A fast-cold-start compiled binary is **necessary but not sufficient**. The
resident daemon is what removes Cost 2 entirely: sketches, DAG, grammar, BOCD posterior, open
file handles, and the zstd encoder pool all live in daemon memory for the life of the session.
The hook then costs: spawn + connect + one write + one 1-byte ACK.

So the architecture is **both**, not either/or.

### 2.2 Candidates evaluated

| Candidate | Cold start | Cross-compile | zstd w/o cgo | stdio MCP | Verdict |
|---|---|---|---|---|---|
| **Go** | 1–3 ms Linux / 6–20 ms Win | `GOOS`/`GOARCH`, one command, 6 targets, no toolchain per target | `klauspost/compress/zstd`, pure Go, ~2× ref speed — fine at our volumes | trivial | **Chosen** |
| Rust | ~same as Go, slightly lower | needs `cross`/target toolchains; MSVC vs GNU ABI decision on Windows | `zstd` crate = cgo-equivalent build deps; pure-Rust `ruzstd` is decode-only/slow | trivial | Close second; rejected on build-matrix and iteration friction, not on merit |
| Node/TS | 25–90 ms | needs Node on user machine or SEA/pkg (50–100 MB artifacts) | native addon or wasm | trivial | **Disqualified** by §8.1 |
| C#/.NET AOT | 5–15 ms | per-RID publish, large artifacts | ok | ok | Rejected: heavier toolchain, weaker fit for a CLI plugin |
| Zig / C | fastest | manual | ok | manual | Rejected: not mainstream/pragmatic for a 6-month multi-agent build |

**D1: Go 1.26+.** Additional decisive properties for *this* program: goroutines make the daemon's
"N sessions × async drain + idle worker" model ~200 lines instead of an event-loop rewrite;
`go test -race`, `-fuzz`, and `-bench` are first-party (we need all three); a single 12–20 MB
static binary ships as the entire plugin runtime with zero user prerequisites.

### 2.3 Runtime shape

```
qompack (one binary, ~15 MB, static, no cgo)
├── qompack observe tool        ← hook, thin client   (hot path)
├── qompack observe prompt      ← hook, thin client   (hot path)
├── qompack observe stop        ← hook, thin client   (hot path)
├── qompack session-start       ← hook, thin client   (warm path; starts daemon)
├── qompack checkpoint          ← hook, thin client   (PreCompact, ≤20 s)
├── qompack flush               ← hook, thin client   (SessionEnd)
├── qompack mcp                 ← long-lived stdio MCP server
├── qompack daemon              ← resident per-project daemon
├── qompack status|recall|pin|why|dropped|eval|config|fsck|doctor|bench
└── qompack self-test           ← contract monitor, exits non-zero (the ONLY one that may)
```

**Hook subcommands must always `exit 0`.** A non-zero hook exit surfaces noise to the user and,
for some hooks, can block the turn. Every error path logs and exits 0. This is a hard rule and
CI enforces it with a test that fault-injects every dependency of every hook subcommand.

### 2.4 Process and IPC architecture

```
Claude Code
   │  spawn (hook)                     ┌───────────────────────────────┐
   ▼                                   │  qompack daemon (per project) │
qompack observe tool ──connect──────►  │  ┌─────────────────────────┐  │
   │  write 1 NDJSON line              │  │ session registry        │  │
   │  read 1-byte ACK  (deadline 8 ms) │  │ in-memory: bloom, cms,  │  │
   │                                   │  │ hll, DAG, sequitur,     │  │
   ├─ ACK ──────────► exit 0           │  │ BOCD, scheduler state   │  │
   │                                   │  ├─────────────────────────┤  │
   └─ timeout/refused                  │  │ ingest queue (ring+WAL) │  │
       └─► append .qompack/spool/  ──► │  │ worker pool → L1 store  │  │
           client-<pid>.ndjson         │  │ idle worker → O3/O5     │  │
           exit 0                      │  └─────────────────────────┘  │
                                       └───────────────────────────────┘
```

**Transport.**

- Windows: named pipe `\\.\pipe\qompack.<hash12>` via `github.com/Microsoft/go-winio`,
  ACL'd to the current user SID only. `hash12` = first 12 hex chars of
  `sha256(normalizedAbsProjectRoot)`.
- POSIX: `SOCK_STREAM` Unix socket. Path resolution order:
  1. `$XDG_RUNTIME_DIR/qompack/<hash12>.sock`
  2. `<os.TempDir()>/qompack-<uid>/<hash12>.sock`
  Directory `0700`, socket `0600`. If the resolved path exceeds 100 bytes (macOS `sun_path` is
  104), fall back to `<os.TempDir()>/qp-<hash8>.sock`.

**Framing.** One request per line: UTF-8 JSON, `\n`-terminated, 1 MiB max line. Response for
fire-and-forget requests is a single byte `\x06` (ACK) or `\x15` (NAK). Requests that need data
back (`UserPromptSubmit` additionalContext, MCP-over-daemon, `status`) set `"reply": true` and
receive one NDJSON response line instead.

**Why ACK instead of pure fire-and-forget.** A bare write followed by immediate `exit` is
*usually* delivered but is not guaranteed to be observed before the daemon's read loop is torn
down on abnormal daemon exit, and on Windows message-mode pipes a client close can race the
server read. One byte costs ~50 µs and converts "usually" into "provably enqueued." Budget
allows it.

**Daemon lifecycle.**

- Started by `qompack session-start` (off the hot path, generous hook timeout). Also started
  lazily by any client that finds no listener, using a detached spawn
  (`SysProcAttr{HideWindow:true, CreationFlags: CREATE_NO_WINDOW|DETACHED_PROCESS}` on Windows;
  `Setsid` on POSIX) — the *spawning* client does not wait for it, it spools and exits.
- Singleton per project via `.qompack/run/daemon.lock` (`O_CREATE|O_EXCL`, contains pid + start
  time + pipe path). A lock whose pid is dead is stale and reclaimable.
- Idle-exits after `runtime.daemonIdleExitSeconds` (default 1800) with zero live sessions.
- Drains `.qompack/spool/*.ndjson` on start and on every idle tick, then deletes drained files.
- Crash-safe: the WAL (`spool/wal-<session>.ndjson`, `O_APPEND`, no fsync) is the durability
  boundary. ACK is sent after the WAL append returns, before any indexing work.

**Latency budgets (normative — these are what CI gates on).**

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-A** | `hook_controlled` — client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (§11.3 L0) | CI on linux/macos/windows, 5 000 iterations |
| **B-B** | `l0_ingest` — daemon read → WAL append returned | p99 < 2 ms | daemon self-metrics + CI |
| **B-C** | `l0_process` — WAL → fully chunked, stored, DAG/sketches updated (async) | p99 < 50 ms | soft; overrun → sampling + backpressure, never blocking |
| **B-D** | `hook_wall` — includes host process creation | reported, not gated; tracked in `/qompack:status` and the bench artifact | — |
| **B-E** | `checkpoint_finalize` — `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | CI |
| **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

B-A is the number the design document names. B-D is reported honestly because process creation is
the host's cost and no plugin architecture can remove it; hiding it inside B-A would be dishonest
measurement, which is exactly the sin §1.3 RC-3 indicts.

**When the budget is exceeded (§8.1 fallback).** The daemon keeps a rolling 512-sample HDR
histogram per hook. If B-A p99 exceeds budget for 3 consecutive 512-sample windows, the daemon
sets `hotPathMode = spool` in the session registry and returns it in the next ACK's NAK-with-hint
frame; clients then skip the connect entirely and append straight to the spool for the rest of
the session. This is the "degrade to async queue-and-drain rather than blocking" clause,
implemented as an observable state transition, logged at WARN, and surfaced by
`/qompack:status`.

### 2.5 Dependency policy

Runtime dependencies are a security and portability surface. The allowed list is closed; adding
to it requires an amendment to this section.

**Runtime (shipped in the binary):**

| Module | Purpose | Why not stdlib |
|---|---|---|
| `github.com/klauspost/compress/zstd` | object compression (§7.4 "zstd-compressed") | no stdlib zstd; pure Go, no cgo |
| `github.com/Microsoft/go-winio` | Windows named pipes (build-tagged `windows`) | no stdlib named-pipe support |
| `golang.org/x/sys/windows` | go-winio's own dependency for the named pipe above; also imported directly by `internal/paths` for its POSIX-semantics file replace (build-tagged `windows`) | stdlib keeps `SetFileInformationByHandle` unexported (`syscall.setFileInformationByHandle`) |

`golang.org/x/sys/windows` is listed rather than added: it has shipped in the Windows binary
since SP-05 task 6 as go-winio's transitive dependency, and `tools/devtool/bindeps.go`'s
`allowedBinDep` has named it explicitly ever since. What changed is only that this module now
also imports it directly — from one `//go:build windows` file,
`internal/paths/replace_windows.go`, for `FileRenameInfoEx`/`FILE_RENAME_POSIX_SEMANTICS`, the
rename a concurrent reader cannot block. Nothing new enters the binary, on Windows or anywhere
else; naming it here makes the closed list match the check that already enforces it.

That is the entire runtime dependency list. Everything else — SHA-256, JSON, JSON-RPC, HDR
histograms (we use a fixed-bucket log histogram), CLI parsing, glob matching, atomic file
writes — is stdlib or in-repo.

**Implemented in-house, deliberately (D7):** FastCDC, Bloom, Count-Min, HyperLogLog,
Misra-Gries, MinHash, Sequitur, BOCD, submodular lazy greedy, MCP server. Each needs one of:
versioned on-disk serialization with a CRC and a documented upgrade path; `MergeFrom` for
cross-session warm start (O4); rebuild-from-records with a *different* capacity (§8.3);
domain-separated hashing shared with the store. No off-the-shelf package gives all of that, and
all are 100–400 lines with textbook references in Appendix B.

**No CLI framework (D5).** `main()` is a `switch os.Args[1]`; each subcommand builds its own
`flag.FlagSet` lazily. Hot-path subcommands parse zero flags. Measured saving vs. cobra: ~0.4 ms
of init and ~2 MB of binary, both of which matter at B-A.

**Test-only dependencies:** `github.com/stretchr/testify/require`,
`github.com/google/go-cmp/cmp`, `pgregory.net/rapid` (property tests). Never imported by
non-`_test.go` files; CI enforces with an import-graph check.

### 2.6 Toolchain

| Concern | Tool | Pin |
|---|---|---|
| Language | Go | `go 1.26` + `toolchain go1.26.4` in `go.mod`; CI matrix `1.26.x` |
| Format | `gofumpt` | version pinned in `tools/go.mod` |
| Lint | `golangci-lint` | `.golangci.yml`: `govet staticcheck errcheck revive gocritic ineffassign unconvert unparam misspell bodyclose gosec forbidigo copyloopvar` |
| Custom lint | `tools/lint/nomagic` (in-repo `x/tools/go/analysis` pass) | forbids float/int literals that duplicate a config default outside `internal/config/defaults.go` (D11) |
| Vuln scan | `govulncheck` | CI job |
| Typecheck | `go build ./... && go vet ./...` + `staticcheck` | CI job `verify` |
| Task runner | `go run ./tools/devtool <task>` | one Go program, identical on PowerShell / bash / zsh. **No Makefile-only workflow** — the dev machine is Windows |
| Release | `goreleaser` | `.goreleaser.yaml`, 6 targets: linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/{amd64,arm64} |
| Benchmark diffing | `benchstat` | `devtool bench-compare`, run locally against `testdata/bench-baseline.txt` (V2 ruling: the baseline is single-host; `bench-gate` gains the comparison only once per-OS baselines are recorded on the runners) |

`tools/devtool` tasks (canonical names used by CI and by every subplan's local loop):
`fmt`, `lint`, `vet`, `build`, `build-all`, `test`, `test-race`, `cover`, `bench`,
`bench-hotpath`, `bench-compare`, `replay`, `plugin-validate`, `fsck`, `ci-local`, `gen-config-docs`,
`fmt-check`, `gen-contract-fixtures`, `gen-fixtures`, `install-hooks`, `check-commit-msg`.

---

## 3. Repository layout

### 3.1 Tree

```
qompack/                                  module: github.com/qompack/qompack
├── Qompack.md                            design document (immutable input; amend only via PR)
├── README.md                             SP-18
├── CHANGELOG.md                          keep-a-changelog, SP-17
├── LICENSE
├── go.mod  go.sum
├── .gitignore .gitattributes .editorconfig
├── .golangci.yml  .goreleaser.yaml
├── .github/
│   ├── workflows/{ci.yml,release.yml,nightly.yml}   # bench-gate + replay-gate are ci.yml jobs (§8)
│   ├── ISSUE_TEMPLATE/{bug.yml,upstream-tracker.yml}
│   └── CODEOWNERS
├── plans/
│   ├── 00-ARCHITECTURE.md                ← this file
│   └── NN-sp<NN>-<slug>.md               18 subplan prompts
├── docs/                                 SP-18 (+ per-subplan ADRs)
│   ├── architecture.md  config-reference.md  mcp-tools.md  commands.md
│   ├── user-guide.md  troubleshooting.md  uat.md
│   ├── cannot-do.md  upstream-issues.md  security.md
│   └── adr/NNNN-<slug>.md
├── cmd/
│   └── qompack/main.go                   the single binary; dispatch only, <150 LOC
├── tools/
│   ├── devtool/                          task runner
│   └── lint/nomagic/                     custom analysis pass
├── internal/                             ── everything below is import-private ──
│   │
│   ├── core/          L—  ids, Hash, TurnIndex, Tokens, errors, clock, rand
│   ├── paths/         L—  .qompack resolution, atomic write, append-only guard, long paths
│   ├── config/        L—  Appendix C schema, defaults, load/validate/override, provenance
│   ├── logging/       L—  leveled structured logging, rotation, "loud failure" channel
│   ├── obs/           L—  counters, log-bucket histograms, latency budgets, status snapshot
│   ├── tokens/        L—  token estimation + per-project calibration (G10.2)
│   ├── contract/      L—  hook-contract monitor, degradation state machine (G9.3)
│   ├── redact/        L—  secret scrubbing applied before anything enters the store (§5.23)
│   ├── pluginmanifest/L—  typed source of plugin.json / hooks.json / .mcp.json (§3.4)
│   │
│   ├── hookio/        L0  Claude Code hook payload/response types, stdin/stdout codecs
│   ├── ipc/           L0  transport (winpipe|unixsock), framing, client, server
│   ├── daemon/        L0  resident process: registry, ingest queue, workers, idle loop, spool
│   ├── cli/           L0  subcommand dispatch, exit-code policy, flag sets
│   │
│   ├── observer/      L0  PostToolUse · UserPromptSubmit · Stop/SubagentStop pipeline
│   ├── chunk/         L1  FastCDC (min 1 KB / target 4 KB / max 16 KB)
│   ├── canon/         L1  canonicalizer registry, per-tool rules, MinHash (O2)
│   ├── symbols/       L1  language-agnostic symbol extraction (§5.24) — one owner, 4 consumers
│   ├── store/         L1  objects, roots, tool_use index, files.json, segments.jsonl, GC
│   ├── sketch/        L1  bloom · cms · hll · misra-gries · minhash + serialization
│   ├── dag/           L1  dependence DAG, edges, backward/thin slice, segment coupling
│   ├── grammar/       L2  Sequitur over the action log, thrash detection
│   ├── analyzer/      L2  Δ-scoring, redundancy, submodular lazy greedy (suffix-constrained)
│   ├── negknow/       L2  elimination ledger, descriptors, staleness, bloom-as-cache
│   ├── scheduler/     L3  BOCD · Young–Daly · composite trigger · p-selection · idle model
│   ├── checkpoint/    L4  schema v1, incremental writer, frontier, focus instructions, pins
│   ├── pins/          L4  pins/invariants.json (append-only)
│   ├── rehydrate/     L5  8-item injection, rule/CLAUDE.md restoration, skill index, drops
│   ├── rules/         L5  path-rule + nested CLAUDE.md discovery from disk
│   ├── skills/        L5  ~450-token skill index builder
│   ├── mcp/           L6  JSON-RPC 2.0 stdio server + 8 tool handlers
│   ├── commands/      L6  7 slash-command backends (`qompack status|recall|…`)
│   ├── eval/          L7  replay harness, Belady OPT, divergence metrics, report
│   │
│   └── testutil/          golden files, synth session generator, fake clock, temp project
├── plugin/                               the installable bundle (SP-01 skeleton, SP-17 ships)
│   ├── .claude-plugin/plugin.json
│   ├── hooks/hooks.json
│   ├── .mcp.json
│   ├── commands/{status,recall,pin,checkpoint,why,dropped,eval}.md
│   └── bin/                              (populated at package time: qompack-<os>-<arch>)
├── testdata/
│   ├── sessions/synthetic/               24 seeded transcripts (in-repo, deterministic)
│   ├── sessions/recorded/.gitkeep        real sessions never committed; see §6.3
│   ├── golden/contracts/<pkg>/           cross-wave fixtures (Rule W-2)
│   ├── golden/checkpoints/               checkpoint JSON goldens
│   ├── corpora/toolout/                  raw bash/test/grep output for canonicalizer goldens
│   └── fixtures/rules/                   fake project trees: paths: rules, nested CLAUDE.md
└── test/
    ├── e2e/                              drives the real binary + real daemon end to end
    ├── bench/hotpath/                    B-A / B-D harness (5 000 spawns)
    └── replay/                           replay-gate driver, phase-exit-criteria assertions
```

### 3.2 Layer mapping (§7.2 L0–L7)

| Layer | §7.2 responsibility | Packages | Owning subplan |
|---|---|---|---|
| **L0 Observer** | PostToolUse · UserPromptSubmit · SessionStart · SessionEnd · Stop | `hookio` `cli` `ipc` `daemon` `observer` | SP-01 (`hookio`, `cli`) · SP-05 (`ipc`, `daemon`) · SP-08 (`observer`) |
| **L1 Store** | CDC chunks · Merkle index · sketches · dependence DAG · segment log | `chunk` `canon` `symbols` `store` `sketch` `dag` | SP-04 (`chunk` `canon` `symbols`) · SP-06 (`store` `redact`) · SP-03 (`sketch`) · SP-07 (`dag`) |
| **L2 Analyzer** | slicing · Δ-scoring · submodular · Sequitur · redundancy | `analyzer` `grammar` `negknow` `dag` | SP-15 (`analyzer` `grammar`) · SP-09 (`negknow`) · SP-07 (`dag` slicing) |
| **L3 Scheduler** | BOCD · Young–Daly · p-selection · TTL awareness | `scheduler` | SP-12 |
| **L4 Checkpointer** | PreCompact → immutable versioned artifact, importance-ordered | `checkpoint` `pins` | SP-10 |
| **L5 Rehydrator** | SessionStart(compact) · progressive budget fill · drop report | `rehydrate` `rules` `skills` | SP-11 |
| **L6 Retrieval** | recall · expand · re_read · already_tried · timeline · why · dropped | `mcp` `commands` | SP-13 (MCP) · SP-14 (commands) |
| **L7 Evaluation** | replay harness · Belady OPT · CI gate | `eval` `test/replay` | SP-02 |
| **cross** | config, logging, metrics, contracts, degradation, tokens | `core` `paths` `config` `logging` `obs` `contract` `tokens` `pluginmanifest` `testutil` | SP-01 (+ SP-05 `contract`, + SP-06 `tokens` exact accounting) |

**Dependency rule (enforced by an import-graph test in `verify`).** The L0–L7 numbering in §7.2
describes *data flow*, not import direction: `observer` (L0) legitimately calls `store` (L1) on
the write path, while `mcp` (L6) calls `store` on the read path. A numeric ordering therefore
cannot express the constraint, and the rule is an explicit DAG instead. The allowed import sets
are exhaustive; anything not listed is forbidden.

| Package | May import |
|---|---|
| `core` | — (nothing in `internal/`) |
| `paths`, `config` | `core` |
| `logging`, `obs` | `core` `paths` `config` |
| *(the five above are the **foundation**; every package below may also import all of them)* | |
| `hookio`, `sketch`, `chunk`, `symbols`, `redact`, `grammar`, `rules`, `skills`, `pins`, `tokens`, `eval`, `scheduler` | foundation only |
| `canon` | `sketch` |
| `dag` | — |
| `store` | `chunk` `canon` `sketch` `symbols` `redact` `tokens` |
| `negknow` | `sketch` `store` `dag` |
| `analyzer` | `store` `dag` `sketch` `scheduler` |
| `checkpoint` | `store` `dag` `negknow` `pins` `grammar` `tokens` |
| `rehydrate` | `checkpoint` `store` `negknow` `dag` `rules` `skills` `tokens` |
| `mcp` | `store` `negknow` `checkpoint` |
| `contract` | `hookio` `store` |
| `ipc` | `hookio` `contract` |
| `observer` | `hookio` `store` `chunk` `canon` `sketch` `dag` `grammar` `negknow` `tokens` |
| `daemon`, `cli`, `commands`, `testutil`, `cmd/qompack` | **composition roots** — may import anything; nothing may import them |

Consequences worth stating explicitly, because they are the ones that would otherwise be
discovered as import cycles in wave 1:

- `store` must **not** import `negknow` — hence `ChangedSince` takes `[]core.Dep`, not
  `[]negknow.Dep` (§4, §5.8, §5.10).
- `tokens` must **not** import `store` — hence `EstimateRoot` takes `[]core.ChunkRef` (§5.20).
- `checkpoint` imports `pins`, so `Invariant` is defined in `pins` and aliased by `checkpoint`
  (§5.14).
- `scheduler` is a pure package: `Evaluate` is a pure function and `Runtime` is an *interface*.
  The `Runtime` implementation, which assembles `Candidate`s from `dag` and `store`, lives in
  `internal/daemon` (§5.13).
- `observer` must not import `scheduler` or `checkpoint`: it emits `observer.Signals` and the
  daemon translates them into `scheduler.Features`.

### 3.3 Runtime data: `.qompack/` (§7.4)

**Location.** `.qompack/` lives at the **root of the project Claude Code is operating on**,
resolved in this order and cached per daemon:

1. `QOMPACK_PROJECT_ROOT` (tests, CI, explicit override)
2. `cwd` / `project_dir` from the hook payload, walked upward to the nearest `.git`
3. the payload `cwd` itself

It is **never** the plugin install directory and never `~`. Global, cross-project state lives in
`~/.qompack/` (`config.json` user-global layer, `daemons.json` registry, `calibration.json`).

**It is not source.** `.gitignore` contains `/.qompack/`, and SP-01 additionally writes
`.qompack/.gitignore` containing `*` on first use, so the store self-ignores even in a project
whose `.gitignore` we never touch.

```
.qompack/                       # gitignored, project-root
├── .gitignore                  # contains "*" — self-ignoring
├── config.json                 # project layer (Appendix C, partial)
├── objects/ab/cd/<sha256>.zst  # content-addressed, zstd, 2-level fanout   [§8.2]
├── index/
│   ├── tool_use.jsonl          # tool_use_id → root hash, ts, tool, args    (append-only)
│   ├── files.json              # path → [(ts, root_hash)] version history
│   ├── roots.jsonl             # root hash → chunk list, sizes              (append-only)
│   └── segments.jsonl          # changepoint-delimited segment log          (append-only)
├── sketches/
│   ├── tried.bloom             # negative knowledge — NEVER regenerated from a summary
│   ├── touch.cms
│   └── explore.hll
├── dag/deps.jsonl              # dependence edges                           (append-only)
├── grammar/actions.seq         # Sequitur state over the action log
├── checkpoints/
│   ├── MANIFEST.jsonl          # (seq, sha256, bytes, created)              (append-only)
│   ├── 0001.json               # immutable, importance-ordered
│   └── 0002.json
├── pins/invariants.json        # user- and agent-pinned, never summarized
├── eval/{replay/,opt/}
│   ── ── ── ── ── ── ── ── ──  runtime-only, not in §7.4, additive:
├── records/eliminations.jsonl  # structured source of truth for tried.bloom (append-only)
├── state/                      # daemon-persisted: bocd.json, scheduler.json, frontier.json
├── run/                        # daemon.lock, daemon.pid, socket path hint  (never committed)
├── spool/                      # WAL + client fallback queue                (append-only)
├── logs/qompack-YYYYMMDD.log   # rotated, 10 MB × 5
├── metrics/latency.json        # rolling histograms for /status and bench
└── tmp/                        # atomic-write staging (same volume as target)
```

**Append-only invariant (§7.4, mechanically enforced).** `checkpoints/`, `pins/`, and
`sketches/tried.bloom` are additive-only.

- `paths.AppendOnly(p)` opens `O_WRONLY|O_APPEND|O_CREATE` and returns an error if `O_TRUNC` is
  requested. It is the *only* write path allowed into `*.jsonl`.
- `paths.CreateNew(p)` uses `O_EXCL`. Checkpoint files use it — writing `0007.json` twice is an
  error, not an overwrite. After a successful write the file is set read-only
  (`0444` / `FILE_ATTRIBUTE_READONLY`).
- `checkpoints/MANIFEST.jsonl` records `(seq, sha256, bytes, created)` per checkpoint;
  `qompack fsck` re-hashes every checkpoint against it. Any mismatch is a loud failure and flips
  the session to `degraded-passive`.
- `pins/invariants.json` is written by read-modify-**append**: it is a JSON array persisted as a
  JSONL log (`pins/invariants.jsonl` internally) with `invariants.json` regenerated as a
  materialized view; the log is the truth. Deletion is a tombstone record, never a rewrite.
- `sketches/tried.bloom` may be *replaced* only by `negknow.RebuildBloom`, whose input is
  `records/eliminations.jsonl` filtered to `status:"active"` — never a checkpoint, never a
  summary, never context. The rebuild writes a new file and renames; the previous file is kept as
  `tried.bloom.<seq>.bak` for one generation.
- A conformance test (`paths.TestAppendOnlyGuard`) attempts truncation, in-place rewrite, and
  out-of-order seq writes against all three locations and asserts every one fails.

**Atomic writes.** `paths.WriteAtomic(p, b)` writes to `.qompack/tmp/<rand>`, `Sync()`, then
`os.Rename` onto `p` (same volume by construction, so `MoveFileEx(REPLACE_EXISTING)` semantics
hold on Windows). Directory fsync on POSIX. Never used for append-only targets.

### 3.4 Plugin bundle and manifest (§7.5)

`Qompack.md` §7.5 gives the *logical* manifest. Claude Code's *physical* plugin layout splits it
across files. SP-01 owns both, keeps them consistent, and validates them:

`plugin/.claude-plugin/plugin.json`

```json
{
  "name": "qompack",
  "version": "0.1.0",
  "description": "Cache-aware, retrieval-backed context compaction",
  "author": { "name": "Qompack" },
  "homepage": "https://github.com/qompack/qompack",
  "keywords": ["compaction", "context", "memory", "cache"]
}
```

`plugin/hooks/hooks.json` — the six hooks of §7.3, with `${CLAUDE_PLUGIN_ROOT}` resolution to the
per-platform binary:

```jsonc
{
  "hooks": {
    "PostToolUse":     [{ "matcher": "*", "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack observe tool",   "timeout": 5  }] }],
    "UserPromptSubmit":[{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack observe prompt", "timeout": 5  }] }],
    "SessionStart":    [{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack session-start",  "timeout": 15 }] }],
    "PreCompact":      [{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack checkpoint",     "timeout": 20 }] }],
    "Stop":            [{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack observe stop",   "timeout": 5  }] }],
    "SubagentStop":    [{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack observe stop --subagent", "timeout": 10 }] }],
    "SessionEnd":      [{                 "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack flush",          "timeout": 20 }] }]
  }
}
```

`plugin/.mcp.json`

```json
{ "mcpServers": { "qompack": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack", "args": ["mcp"] } } }
```

`plugin/commands/*.md` — seven files, one per §7.5 command, each a frontmatter'd prompt that
shells out to the corresponding `qompack` subcommand.

**Contract risk (G9.3).** The exact key names above (`hooks.json` shape, `matcher`,
`${CLAUDE_PLUGIN_ROOT}`, `hookSpecificOutput.additionalContext`, `SessionStart.source`) are
partly undocumented and *will* drift. SP-01 generates the manifest from a single typed Go source
(`internal/pluginmanifest`) and SP-05's contract monitor asserts each contract at runtime (§12).
`devtool plugin-validate` diffs the generated files against the committed ones in CI so drift can
never land silently.

---

## 4. Cross-cutting primitives

```go
// package core
type Hash [32]byte                   // sha256
func (h Hash) String() string        // "sha256:" + hex  (canonical text form everywhere)
func (h Hash) Short() string         // first 12 hex chars
func ParseHash(s string) (Hash, error)
func HashBytes(domain string, b []byte) Hash   // sha256(domain || 0x00 || b) — domain-separated

type SessionID string
type ToolUseID string
type TurnIndex int          // 0-based index into the session's turn sequence
type SegmentID int          // 1-based, monotonic per project
type CheckpointSeq int      // 1-based, monotonic per project; 0 = none
type DecisionID string      // "dec_" + first 12 hex of HashBytes("qompack.decision", …)
type Tokens int
type UnixMilli int64

// ChunkRef and Dep live in core, not in store/negknow, so that `tokens` can size a root without
// importing `store` and `store` can answer staleness without importing `negknow` (§3.2).
type ChunkRef struct{ Hash Hash; Len int }
type Dep struct{ Path string; Hash Hash }   // path is paths.Key form

type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
// testutil.FakeClock implements Clock; every package that takes time takes a Clock.

// Sentinel errors — every package reuses these rather than defining synonyms.
var (
    ErrNotImplemented = errors.New("qompack: not implemented")   // SP-01 stubs return this
    ErrNotFound       = errors.New("qompack: not found")
    ErrAppendOnly     = errors.New("qompack: append-only violation")
    ErrAlreadyEncoded = errors.New("qompack: segment already encoded (DPI guard)")
    ErrBudget         = errors.New("qompack: budget exceeded")
    ErrDegraded       = errors.New("qompack: running in degraded mode")
    ErrContract       = errors.New("qompack: host contract violated")
)
```

**Path normalization.** `paths.Norm(projectRoot, p) (string, error)` returns a project-relative,
forward-slash, cleaned path; it rejects escapes above the root and resolves symlinks only within
the root. `paths.Key(p) string` returns the *dedup key*: `Norm` plus lowercasing on Windows and
macOS (case-insensitive filesystems) — the original casing is always preserved alongside. All
store keys, DAG file nodes, `depends_on` entries, and glob matching use `paths.Key`.

**Text normalization.** Content entering the store is CRLF→LF normalized by the `crlf`
canonicalizer before chunking, so a Windows and a Linux read of the same file dedup to the same
chunks. The original line-ending class is recorded as a `canon.Delta`.

---

## 5. Module interface contracts

These are the seams. Signatures are normative; a subplan may add methods to a struct it owns but
may not change or remove anything below without an amendment (§0).

Every interface listed here is created by **SP-01** as a compiling stub returning
`core.ErrNotImplemented`, together with a conformance suite (§5.22).

### 5.1 `internal/config`

```go
type Config struct {
    Store        StoreCfg        `json:"store"`
    Scheduler    SchedulerCfg    `json:"scheduler"`
    Checkpoint   CheckpointCfg   `json:"checkpoint"`
    Sketches     SketchesCfg     `json:"sketches"`
    Eliminations EliminationsCfg `json:"eliminations"`
    Retrieval    RetrievalCfg    `json:"retrieval"`
    Selection    SelectionCfg    `json:"selection"`
    Eval         EvalCfg         `json:"eval"`
    Runtime      RuntimeCfg      `json:"runtime"` // EXTENSION beyond Appendix C — see §11.5
}

type Origin uint8 // OriginDefault, OriginUserFile, OriginProjectFile, OriginEnv, OriginFlag
type Source struct{ Origin Origin; Location string } // e.g. "C:/proj/.qompack/config.json:12"
type Provenance map[string]Source                    // key = dotted path, e.g. "scheduler.cache.readMultiplier"

type Env struct {
    ProjectRoot string
    HomeDir     string
    Getenv      func(string) string
    Flags       map[string]string // from the invoking subcommand's --set k=v
}

func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
func (c Config) Validate() []Violation
func (c Config) Get(dotted string) (any, bool)
func (c Config) JSONSchema() []byte          // generated; feeds docs/config-reference.md
type Warning struct{ Key, Message, Location string }
type Violation struct{ Key, Message string; Got, Want any }
```

Precedence, lowest to highest: `Defaults()` → `~/.qompack/config.json` → `<project>/.qompack/config.json`
→ `QOMPACK_*` env → `--set` flags. Merge is **deep, per leaf key** — a project file that sets
only `scheduler.softFloorPct` inherits every other scheduler default. Env key mapping:
`QOMPACK_SCHEDULER__CACHE__READMULTIPLIER=0.08`.

### 5.2 `internal/logging` and `internal/obs`

```go
// logging
type Level uint8 // Debug, Info, Warn, Error, Loud
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    // Loud writes to the log AND to .qompack/logs/LOUD.log AND surfaces in /qompack:status.
    // Reserved for contract violations and degradation transitions. Never silent. (§12)
    Loud(msg string, kv ...any)
}
func New(dir string, lvl Level) (Logger, io.Closer, error)
func Nop() Logger

// obs
type Histogram interface { Observe(d time.Duration); Snapshot() HistSnapshot; Reset() }
type HistSnapshot struct{ N int64; P50, P95, P99, P999, Max time.Duration }
type Registry interface {
    Hist(name string) Histogram
    Counter(name string) Counter
    Gauge(name string) Gauge
    Snapshot() Snapshot          // feeds /qompack:status and metrics/latency.json
    CheckBudgets(cfg config.Config) []BudgetBreach
}
type BudgetBreach struct{ Budget string; Observed, Limit time.Duration; Windows int }
func Timed(h Histogram, f func() error) error
```

### 5.3 `internal/hookio`

Typed representations of the Claude Code hook wire format. **All fields are optional-tolerant**:
unknown fields are preserved in `Extra`, missing fields never panic. This package is where host
drift is absorbed.

```go
type Event struct {
    HookEventName  string          `json:"hook_event_name"`
    SessionID      core.SessionID  `json:"session_id"`
    TranscriptPath string          `json:"transcript_path"`
    CWD            string          `json:"cwd"`
    Source         string          `json:"source"`          // SessionStart: startup|resume|compact|clear
    Trigger        string          `json:"trigger"`         // PreCompact: manual|auto
    ToolName       string          `json:"tool_name"`
    ToolUseID      core.ToolUseID  `json:"tool_use_id"`
    ToolInput      json.RawMessage `json:"tool_input"`
    ToolResponse   json.RawMessage `json:"tool_response"`
    Prompt         string          `json:"prompt"`
    StopHookActive bool            `json:"stop_hook_active"`
    Extra          map[string]json.RawMessage `json:"-"`
}
func ReadEvent(r io.Reader, limit int64) (Event, []byte, error) // returns raw bytes too

type Output struct {
    Continue           *bool  `json:"continue,omitempty"`
    SuppressOutput     *bool  `json:"suppressOutput,omitempty"`
    HookSpecificOutput *HSO   `json:"hookSpecificOutput,omitempty"`
    SystemMessage      string `json:"systemMessage,omitempty"`
}
type HSO struct {
    HookEventName      string `json:"hookEventName"`
    AdditionalContext  string `json:"additionalContext,omitempty"`
    CustomInstructions string `json:"customInstructions,omitempty"` // PreCompact (§8.5)
}
func WriteOutput(w io.Writer, o Output) error
func Empty() Output   // the zero-cost response used by PostToolUse in the normal case
```

### 5.4 `internal/ipc` and `internal/daemon`

```go
// ipc
type Addr struct{ Kind AddrKind; Path string } // NamedPipe | UnixSocket
func Resolve(projectRoot string) (Addr, error)

type Request struct {
    Op       Op              `json:"op"`
    Session  core.SessionID  `json:"s"`
    TS       core.UnixMilli  `json:"t"`
    Reply    bool            `json:"r,omitempty"`
    Event    *hookio.Event   `json:"e,omitempty"`
    Raw      json.RawMessage `json:"x,omitempty"`
}
type Op string // "observe.tool" "observe.prompt" "observe.stop" "session.start"
               // "checkpoint" "flush" "status" "mcp" "admin.*"
type Response struct {
    OK     bool            `json:"ok"`
    Mode   contract.Mode   `json:"mode"`
    Hot    HotPathMode     `json:"hot"`   // Sync | Spool  — clients honour this immediately
    Output *hookio.Output  `json:"out,omitempty"`
    Err    string          `json:"err,omitempty"`
    Data   json.RawMessage `json:"data,omitempty"`
}

type Client interface {
    // Send is the hot path. It connects, writes, awaits ACK within deadline, and returns.
    // On ANY failure it spools to disk and returns (Response{OK:false}, nil) — never an error
    // that a hook would propagate.
    Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
    Close() error
}
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client

type SpoolWriter interface{ Append(req Request) error; Path() string }
func NewSpool(dir string) (SpoolWriter, error)

type Server interface {
    Serve(ctx context.Context, h Handler) error
    Addr() Addr
    Close() error
}
type Handler func(ctx context.Context, req Request) Response

// daemon
type Daemon interface {
    Run(ctx context.Context) error
    Registry() *SessionRegistry
    Drain(ctx context.Context) (int, error)   // spool + WAL replay; idempotent
    Idle() IdleController                     // O3/O5 background work
    Stop(ctx context.Context) error
}
type Options struct {
    ProjectRoot string; Cfg config.Config; Log logging.Logger
    Metrics obs.Registry; Clock core.Clock
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
}
func New(o Options) (Daemon, error)

// Extension seams (SP-05 ships these so later waves wire in WITHOUT editing daemon internals,
// which is what keeps wave-3 subplans from colliding inside one package):
//   - Handle registers an Op handler; the op-routing table is data, not a switch.
//   - IdleController.Register adds O3/O5 background work.
//   - Services is the late-bound dependency set; nil members mean "not built yet" and every
//     call site must tolerate that (waves 1–2 run with Checkpoints and Sched nil).
//
// Handle takes a POINTER receiver. The routing table is an unexported map that Handle allocates
// on first use, so a value receiver would mutate a copy and register nothing — every caller would
// silently get an empty table. Wiring is therefore `o := daemon.Options{...}; o.Handle(...)` and
// composition roots must pass &o where an *Options is wanted.
func (*Options) Handle(op ipc.Op, h ipc.Handler)

type IdleController interface {
    // Register work that may run only when the session is idle (§8.4 O3).
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}
type SessionRegistry struct{ /* per-session live state, hot-path mode, counters */ }
```

### 5.5 `internal/chunk` (FastCDC)

```go
type Params struct{ Min, Target, Max int } // defaults 1024 / 4096 / 16384  (§8.1)
func DefaultParams() Params                // reads config.StoreCfg.Chunk
func (p Params) Validate() error           // Min < Target < Max, Target power-of-two-ish

type Chunk struct{ Offset int64; Len int; Hash core.Hash }
type Chunker interface {
    // Split is allocation-free apart from the returned slice; safe for concurrent use.
    Split(data []byte) []Chunk
    SplitStream(r io.Reader, fn func(Chunk, []byte) error) error
}
func New(p Params) Chunker

// RootHash is the Merkle root: HashBytes("qompack.root.v1", concat(chunk hashes)).
func RootHash(chunks []Chunk) core.Hash
```

Normative properties (fuzz-tested by SP-04): boundary stability under insertion (inserting bytes
at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms
and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

### 5.6 `internal/canon` (canonicalizer registry + MinHash, O2)

```go
type Class string // "timestamps" "ansi" "pids" "addresses" "tmpPaths" "durations" "crlf" "paths"
type Delta struct{ Offset, Len int; Original string; Class Class }

type Result struct {
    Canonical []byte
    Deltas    []Delta          // volatile side record; empty when KeepDeltas=false
    Applied   []string         // canonicalizer names, in application order
    Signature sketch.Signature // MinHash over canonical shingles
    Reduced   float64          // 1 - len(canonical)/len(input)
}

type Options struct {
    Strip      []Class
    KeepDeltas bool
    MinHash    MinHashOptions // Permutations, ShingleSize, Enabled
}

type Canonicalizer interface {
    Name() string
    Applies(tool, path string) bool
    Canonicalize(in []byte, o Options) (Result, error)  // MUST be idempotent
}

type Registry interface {
    Register(c Canonicalizer) error        // error on duplicate Name
    For(tool, path string) []Canonicalizer // deterministic order (registration order)
    Run(tool, path string, in []byte, o Options) (Result, error)
    Names() []string
}
func NewRegistry() Registry
func Default(cfg config.CanonicalizeCfg) Registry
// Default registers, in order: crlf, ansi, timestamps, durations, pids, addresses, tmpPaths,
// then per-tool: bash, testrunner (go/jest/pytest/cargo), grep, glob, fileread, webfetch, git.

func Restore(canonical []byte, deltas []Delta) ([]byte, error) // byte-exact inverse
```

Normative properties (SP-04): `Canonicalize(Canonicalize(x)) == Canonicalize(x)`;
`Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever
*grows* its input.

### 5.7 `internal/sketch`

```go
type Kind uint8 // KindBloom, KindCMS, KindHLL, KindMisraGries, KindMinHash
type Header struct {
    Magic [4]byte // 'Q','P','K','S'
    Ver   uint16  // format version, bumped on any layout change
    Kind  Kind
    Params map[string]float64
    Count  uint64
    Created core.UnixMilli
    CRC32C  uint32
}
type Sketch interface {
    Header() Header
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func Save(p string, s Sketch) error   // atomic (paths.WriteAtomic) except tried.bloom (§3.3)
func Load(p string, s Sketch) error   // CRC + version checked; corrupt → ErrNotFound + Loud log

// ── Bloom ──────────────────────────────────────────────────────────────
type Bloom struct{ /* … */ }
func NewBloom(capacity int, fpRate float64) *Bloom  // m = -n·ln(p)/(ln2)², k = (m/n)·ln2
func (b *Bloom) Add(key []byte)
func (b *Bloom) Test(key []byte) bool
func (b *Bloom) Count() int
func (b *Bloom) FillRatio() float64
func (b *Bloom) EstimatedFPRate() float64            // watch-for §11.4
func (b *Bloom) Capacity() (n int, fp float64)
func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool) // grow 2× at fill > 0.5

// ── Count-Min ──────────────────────────────────────────────────────────
type CMS struct{ /* … */ }
func NewCMS(epsilon, delta float64) *CMS             // width=⌈e/ε⌉, depth=⌈ln(1/δ)⌉
func (c *CMS) Add(key []byte, n uint32)
func (c *CMS) Estimate(key []byte) uint32
func (c *CMS) MergeFrom(o *CMS) error                // O4 warm start; errors on shape mismatch
func (c *CMS) Scale(factor float64)                  // exponential decay for warm start
func (c *CMS) HeavyHitters(mg *MisraGries, n int) []Counted

// ── HyperLogLog ────────────────────────────────────────────────────────
type HLL struct{ /* … */ }
func NewHLL(registers int) *HLL                      // 2048 → ~2 KB, ~2.3% error
func (h *HLL) Add(key []byte)
func (h *HLL) Cardinality() uint64
func (h *HLL) MergeFrom(o *HLL) error

// ── Misra-Gries ────────────────────────────────────────────────────────
type Counted struct{ Key string; Count int }
type MisraGries struct{ /* … */ }
func NewMisraGries(k int) *MisraGries
func (m *MisraGries) Add(key string, n int)
func (m *MisraGries) Top(n int) []Counted             // no false positives, by construction
func (m *MisraGries) MergeFrom(o *MisraGries) error

// ── MinHash ────────────────────────────────────────────────────────────
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }
type Signature struct{ Perms uint16; Mins []uint64 }
func MinHash(data []byte, o MinHashOptions) Signature
func (s Signature) Jaccard(o Signature) float64
func (s Signature) IsNearDup(o Signature, threshold float64) bool
func (s Signature) MarshalBinary() ([]byte, error)
func (s *Signature) UnmarshalBinary([]byte) error
```

### 5.8 `internal/store` (L1)

```go
type ChunkRef = core.ChunkRef   // alias: the type lives in core so `tokens` need not import store
type Root struct {
    Hash       core.Hash
    Chunks     []ChunkRef
    CanonBytes int64
    RawBytes   int64
    Tokens     core.Tokens     // exact chunk-level accounting (G10.2)
}

type PutOptions struct {
    Tool      string
    Path      string          // paths.Key form; "" if none
    Canon     canon.Options
    KeepRaw   bool            // store the volatile deltas alongside
    Ephemeral bool            // retrieval results, born ephemeral (§8.7)
}
type PutResult struct {
    Root      Root
    Novel     int             // chunks actually written
    Reused    int
    Signature sketch.Signature
    NearDup   *NearDupInfo    // set when a prior version is within nearDupThreshold
}
type NearDupInfo struct{ PriorRoot core.Hash; Jaccard float64; DeltaBytes int64 }

type Supersession uint8 // StatusOK, StatusSuperseded
type ToolUseRecord struct {
    ID           core.ToolUseID
    Session      core.SessionID
    Turn         core.TurnIndex
    TS           core.UnixMilli
    Tool         string
    ArgsDigest   core.Hash
    ArgsPreview  string          // ≤120 chars, for tombstones and `timeline`
    Root         core.Hash
    Path         string
    Bytes        int64
    Tokens       core.Tokens
    Signature    sketch.Signature
    Status       Supersession
    SupersededBy core.ToolUseID
    Ephemeral    bool
    Subagent     string          // "" for main agent; agent name for SubagentStop captures
}

type FileVersion struct{ TS core.UnixMilli; Root core.Hash; Turn core.TurnIndex; Bytes int64 }

type Store interface {
    // ── objects ──
    Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error)
    PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
    GetChunk(ctx context.Context, h core.Hash) ([]byte, error)
    GetRoot(ctx context.Context, root core.Hash) (Root, error)
    Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
    OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) // §8.7 minimal span
    Has(h core.Hash) bool

    // ── tool_use index ──
    RecordToolUse(ctx context.Context, rec ToolUseRecord) error
    ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
    ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
    MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error

    // ── file version history (§8.2) ──
    AppendFileVersion(ctx context.Context, path string, v FileVersion) error
    FileHistory(ctx context.Context, path string) ([]FileVersion, error)
    FileAt(ctx context.Context, path string, at time.Time) (FileVersion, error)
    // ChangedSince returns the subset of deps whose current file-version hash differs.
    // Takes []core.Dep, NOT []negknow.Dep — store must not import negknow (§3.2).
    ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) // staleness (§8.3)

    // ── search (backs `recall`) ──
    Search(ctx context.Context, q Query) ([]Hit, error)

    Segments() SegmentLog
    Stats(ctx context.Context) (Stats, error)
    GC(ctx context.Context, p GCPolicy) (GCReport, error)
    Flush(ctx context.Context) error
    Close() error
}

type Query struct {
    Text   string
    Path   string
    Symbol string
    Tool   string
    Since  time.Time
    K      int
}
type Hit struct {
    Root core.Hash; ToolUseID core.ToolUseID; Path string; Tool string
    TS core.UnixMilli; Score float64; Summary string; Span [2]int64
}

type Segment struct {
    ID            core.SegmentID
    Session       core.SessionID
    StartTurn, EndTurn core.TurnIndex
    StartTS, EndTS     core.UnixMilli
    Features      map[string]float64  // BOCD feature summary at close
    Tokens        core.Tokens
    EncodedOnce   bool                // DPI guard (§8.2)
    CheckpointSeq core.CheckpointSeq
    Closed        bool
    BloomRef      string              // per-segment bloom, LSM-style (§6.8); "" until SP-16
}
type SegmentLog interface {
    Open(ctx context.Context, s Segment) (core.SegmentID, error)
    Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
    Get(ctx context.Context, id core.SegmentID) (Segment, error)
    Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
    Current(ctx context.Context, s core.SessionID) (Segment, error)
    // MarkEncoded is the DPI guard. Returns ErrAlreadyEncoded if any id already has
    // EncodedOnce==true with a different CheckpointSeq. Idempotent for the same seq.
    MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
    Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
    Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error)
}

type GCPolicy struct{ RetainDays, RetainSessions int; DryRun bool; Deadline time.Duration }
type GCReport struct {
    ScannedObjects, LiveObjects, DeletedObjects int
    BytesFreed int64; Roots int; Duration time.Duration; Truncated bool
}
type Stats struct {
    Objects int; Bytes, RawBytes int64
    DedupRatio float64            // RawBytes / Bytes — the Phase 1 exit criterion (≥ 4:1)
    ToolUses, Segments, Files int
    Sketches map[string]int
}

func Open(root string, cfg config.Config, deps Deps) (Store, error)
type Deps struct {
    Chunker chunk.Chunker; Canon canon.Registry; Tokens tokens.Estimator
    Symbols symbols.Extractor      // backs Query.Symbol and the §8.7 symbol-aware widener
    Redact  redact.Redactor        // applied to every byte on the way in (§5.23)
    Log logging.Logger; Metrics obs.Registry; Clock core.Clock
}
```

**GC semantics.** Roots are: every checkpoint's `pointers`, every pin, every elimination's
`evidence` and `depends_on`, every `tool_use`/file-version entry inside the retention window
(`max(30 days, 10 sessions)` by default — "whichever is longer" per §8.2). Collection is
authoritative **mark-and-sweep** from those roots; an approximate refcount is maintained
alongside purely for `/qompack:status` and for cheap "is this worth keeping" decisions. GC runs
on `SessionEnd` and during idle (O3), always with a `Deadline`, always resumable
(`GCReport.Truncated`).

### 5.9 `internal/dag`

```go
type NodeKind uint8 // KindToolUse, KindToolResult, KindAssistant, KindUserPrompt,
                    // KindFile, KindSymbol, KindDecision, KindElimination, KindSegment
type NodeID string  // "<kind>:<stable-key>"
type Node struct {
    ID NodeID; Kind NodeKind
    Turn core.TurnIndex; TS core.UnixMilli
    Pos  int                // token position in the prefix — required for p-selection
    Ref  string             // path, symbol, tool_use_id, decision id …
    Root core.Hash
    Tokens core.Tokens
    Ephemeral bool
}
type EdgeKind uint8 // EdgeSequence, EdgeProduces, EdgeConsumes, EdgeSharedFile,
                    // EdgeSharedSymbol, EdgeSupersedes, EdgeExplains, EdgeControlOnly
type Edge struct{ From, To NodeID; Kind EdgeKind; Weight float32; Turn core.TurnIndex }

type SliceOptions struct {
    Thin      bool          // drop EdgeControlOnly (§6.4 thin slicing; default true)
    MaxDepth  int           // 0 = unbounded
    MaxNodes  int           // hard cap, sets Truncated
    Decay     float32       // per-hop score decay, default 0.85
    Deadline  time.Duration
}
type Slice struct {
    Scores    map[NodeID]float32   // relevance score, NOT binary keep/drop (§8.3)
    Order     []NodeID             // descending score, stable tiebreak by Turn
    Truncated bool
    Visited   int
}

type Graph interface {
    AddNode(n Node) error
    AddEdge(e Edge) error
    Node(id NodeID) (Node, bool)
    Out(id NodeID) []Edge
    In(id NodeID) []Edge
    BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
    ForwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
    // CrossingEdges is segment_coupling(p): edges whose endpoints straddle token position pos.
    CrossingEdges(pos int) int
    NodesAfter(pos int) []Node
    Flush(ctx context.Context) error       // append to dag/deps.jsonl
    Compact(ctx context.Context) error     // rewrite the log dropping GC'd nodes (idle only)
    Stats() GraphStats
}
func Open(root string, cfg config.Config, log logging.Logger) (Graph, error)
```

### 5.10 `internal/negknow` (L2 negative knowledge)

```go
type Scope string  // "session" | "project"
type Status string // "active" | "stale"
type SourceKind uint8 // SourceMCP, SourceSlashCommand, SourceHeuristic, SourceUserStatement

// Canonical descriptor (§8.3) — exactly the four fields the design specifies.
type Descriptor struct {
    NormalizedPath string     // paths.Key; "" when the elimination is not file-scoped
    Symbol         string     // "" == null
    ApproachClass  string     // canonicalized verb-phrase class, e.g. "widen-timeout"
    ReasonHash     core.Hash
}
func Canonicalize(target, approach, reason string) Descriptor
func (d Descriptor) Key() []byte    // stable bloom key: HashBytes("qompack.neg.v1", …)

type Dep = core.Dep    // alias of core.Dep (§4) — store cannot import negknow (§3.2)

type Record struct {
    ID        string          `json:"id"`
    Session   core.SessionID  `json:"session"`
    TS        core.UnixMilli  `json:"ts"`
    Target    string          `json:"target"`
    Approach  string          `json:"approach"`
    Reason    string          `json:"reason"`
    Desc      Descriptor      `json:"descriptor"`
    Evidence  core.Hash       `json:"evidence"`
    DependsOn []Dep           `json:"depends_on"`
    Scope     Scope           `json:"scope"`
    Status    Status          `json:"status"`
    StaleSince core.UnixMilli `json:"stale_since,omitempty"`
    StaleBecause []string     `json:"stale_because,omitempty"`
    Source    SourceKind      `json:"source"`
}

type AnswerState uint8 // AnswerAbsent, AnswerActive, AnswerStale   ← the three-way response
type Answer struct {
    State  AnswerState
    Record *Record // nil when Absent, or when only a bloom hit with no backing record
    Note   string  // Stale: "previously eliminated, but the evidence has changed since —
                   //         re-verification may be warranted"
    BloomOnly bool // true = bloom said yes but no record found (possible false positive)
}

type Ledger interface {
    Record(ctx context.Context, r Record) (string, error)   // appends; updates bloom
    Query(ctx context.Context, target, approach string, scope Scope) (Answer, error)
    Get(ctx context.Context, id string) (Record, error)
    Active(ctx context.Context, scope Scope) ([]Record, error)
    All(ctx context.Context) ([]Record, error)
    MarkStale(ctx context.Context, ids []string, because []string) error
    // RefreshStaleness compares every active record's depends_on hashes against the store's
    // current file versions and flips changed ones to stale. Returns the flipped ids.
    RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
    // RebuildBloom rebuilds tried.bloom from ACTIVE RECORDS ONLY. Never from a checkpoint,
    // never from context. Resizes if sketch.Bloom.ResizeTarget says so.
    RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
    Health() Health
    Close() error
}
type Health struct{ Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool }

func Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error)

// Heuristic detection source #3 (§8.3): test-fail → revert → different-approach in the DAG.
type Detector interface {
    Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error)
}
```

### 5.11 `internal/grammar` (Sequitur)

```go
type Symbol string          // tool name, or "user", or "test:pass"/"test:fail"
type RuleID int
type Rule struct{ ID RuleID; Body []Symbol; Uses int; Expansion []Symbol; Span int }

type Sequitur interface {
    Append(s Symbol)
    Rules() []Rule
    // Thrash returns rules whose multiplicity exceeds cfg and whose expansion length ≥ 2.
    Thrash(minUses int) []Rule
    Compressed() []Symbol      // grammar-compressed action history for the checkpoint
    Reset()
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func New() Sequitur

type Warning struct{ Rule Rule; Repeats int; Message string; Turns []core.TurnIndex }
func FormatWarning(w Warning) string   // one-line text injected via UserPromptSubmit
```

### 5.12 `internal/analyzer` (L2 Δ-scoring, redundancy, submodular)

```go
type Block struct {
    ID      dag.NodeID
    Pos     int              // token position in the prefix
    Tokens  core.Tokens
    Kind    dag.NodeKind
    Root    core.Hash
    Ephemeral bool           // §8.7 first eviction candidate
    Superseded bool
}

type DeltaMode string // "cheap" | "medium" | "expensive"  (config.selection.deltaScoring)
type DeltaScorer interface {
    Mode() DeltaMode
    // Score returns Δ(c) proxies in [0,1] for each block against the OBSERVED continuation.
    Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error)
}
type Continuation struct{ Text []byte; Symbols []string; Paths []string; FromTurn core.TurnIndex }
func NewCheapScorer(s store.Store) DeltaScorer   // token-overlap + symbol-reference counting

type RedundancyReport struct {
    Superseded []core.ToolUseID
    NearDups   map[core.ToolUseID][]core.ToolUseID
}
func DetectRedundancy(ctx context.Context, s store.Store, sess core.SessionID) (RedundancyReport, error)

// Selector is CONSTRUCTED with p. It is structurally impossible to select a block before p:
// NewSelector filters the candidate set in the constructor (§8.3, §5.3).
type Selector interface {
    P() int
    Select(ctx context.Context, budget core.Tokens) (Selection, error)
}
type Selection struct {
    Keep    []dag.NodeID
    Tokens  core.Tokens
    Value   float64
    Dropped []dag.NodeID
    Iters   int          // lazy-greedy evaluations, for the (1-1/e) sanity assertion
}
func NewSelector(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
                 lambda float64, lazy bool) (Selector, error) // errors if any block.Pos < p
```

**Ship-order guard (closing note 3).** `NewSelector` returns an error unless
`scheduler.PSelectionAvailable()` reports true, and `config.SelectionCfg.Submodular.Enabled`
defaults to `false` until SP-12 (scheduler, wave 3) has merged. A CI test asserts that submodular
selection is inert on a `develop` where the scheduler is absent. Note that `dag`
(SP-07, wave 1) ships slicing *scores* long before this: scores are legal input to ranking inside
a checkpoint or rehydration budget, which is not a prefix edit. What closing note 3 forbids is a
scattered keep-set driving a drop decision, and that path is the one this guard closes.

### 5.13 `internal/scheduler` (L3)

```go
type TriggerReason string // "soft_floor" "changepoint" "young_daly" "hard_ceiling" "idle_cold_cache"
type TTLState string      // "warm" | "expiring" | "cold" | "unknown"
type Urgency uint8        // UrgencyNone, UrgencyAdvisory, UrgencyNow

type Features struct {
    PathJaccard     float64 // locality over recently-touched paths
    ToolShift       float64 // tool-type distribution shift
    LexicalCohesion float64 // TextTiling-style
    GapSeconds      float64 // inter-turn time gap
    TodoTransition  float64 // todo-list state change
}
type ChangepointState struct {
    RunLength      int
    ProbChangepoint float64
    AtChangepoint  bool
    Posterior      []float64 // pruned
}
type Detector interface {                      // BOCD
    Observe(f Features) ChangepointState
    State() ChangepointState
    Reset()
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func NewBOCD(hazardRate float64, features []string) Detector

type Candidate struct {
    Pos               int              // token position
    Turn              core.TurnIndex
    SegmentID         core.SegmentID
    RoundBoundary     bool             // API-round boundary (§8.4 candidates = changepoints ∩ rounds)
    ReclaimableTokens core.Tokens
    Coupling          int              // dag.CrossingEdges(Pos)
}

type Inputs struct {
    Now              core.UnixMilli
    ContextTokens    core.Tokens
    EffectiveWindow  core.Tokens
    MaxOutputTokens  core.Tokens
    LastAPICallTS    core.UnixMilli   // sliding-TTL idle model (E1) — NOT last cache write
    LastCacheWriteTS core.UnixMilli
    BurnRateTokensPerMin float64
    MeasuredDeltaSeconds *float64     // δ for Young–Daly; nil (config null) → measure at runtime
    Changepoint      ChangepointState
    Candidates       []Candidate
    FrontierTurn     core.TurnIndex
    ResidualTokens   core.Tokens      // O5: tokens between frontier and now
    ExpectedRemainingReads float64    // ski-rental (Phase 7)
    Cfg              config.SchedulerCfg
}

type Decision struct {
    ShouldCompact bool
    Reasons       []TriggerReason
    P             Candidate
    PScore        float64
    Breakdown     map[string]float64 // "reclaimable","rewrite","distortion" — for /status and eval
    Urgency       Urgency
    TTL           TTLState
    YoungDalySeconds float64
    Background    []BackgroundTask   // O3 work to run during idle
    SoftFloorTokens, HardCeilingTokens core.Tokens
}
type BackgroundTask string // "advance_frontier" "gc" "precompute_slice" "refresh_delta"
                           // "rebuild_bloom" "compact_dag"

// Evaluate is a PURE function of Inputs. No I/O, no clock, no globals. This is what makes the
// whole of §8.4 unit-testable and replayable.
func Evaluate(in Inputs) Decision

func PSelectionAvailable() bool     // ship-order guard for analyzer.NewSelector
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64 // √(2·δ·M)
func SkiRentalShouldWrite(expectedReads, r, w float64) bool // reads > w/r (≈12.5)

type Runtime interface {                        // the stateful wrapper the daemon owns
    Observe(ctx context.Context, f Features, at core.TurnIndex) ChangepointState
    Evaluate(ctx context.Context) (Decision, error)
    NotifyActivity(ts core.UnixMilli)
    IdleSince() (core.UnixMilli, bool)
    Persist(ctx context.Context) error           // state/bocd.json, state/scheduler.json
}
```

**Package purity and where `Runtime` lives.** Package `scheduler` imports foundation packages
only (§3.2): `Evaluate` is pure, `Runtime` is an *interface*. Assembling `Inputs.Candidates`
requires `dag.CrossingEdges` and the store's tool-use records, so the `Runtime` **implementation**
lives in `internal/daemon` (a composition root). SP-12 owns both the `scheduler` package and that
implementation file.

**Who computes `Candidate.ReclaimableTokens`.** The `Runtime` implementation, not the analyzer.
Droppable blocks after `p` are ranked ephemeral-first, then superseded, then ordinary compactable
tool results, then everything else — the §8.7 eviction order. This must exist in wave 3 for
p-selection to be meaningful; `analyzer` (wave 4) later refines *which* of them to keep, never
*whether* they are droppable.

### 5.14 `internal/checkpoint` (L4) and `internal/pins`

The on-disk schema is **verbatim from §8.5** and is versioned (`"version": 1`). Any change to
the JSON shape bumps `version` and adds a migration in `checkpoint/migrate.go`.

```go
type Checkpoint struct {
    Version int                `json:"version"`
    Session core.SessionID     `json:"session"`
    Seq     core.CheckpointSeq `json:"seq"`
    Created string             `json:"created"`
    Parent  string             `json:"parent,omitempty"`
    EncodedSegments []core.SegmentID `json:"encoded_segments"` // DPI guard: from originals only

    // Tier 1: never truncated
    Invariants []Invariant `json:"invariants"`
    UserIntent UserIntent  `json:"user_intent"`
    Eliminated []negknow.Record `json:"eliminated"`

    // Tier 2: truncate late
    Decisions     []Decision  `json:"decisions"`
    OpenQuestions []string    `json:"open_questions"`
    CurrentWork   CurrentWork `json:"current_work"`

    // Tier 3: truncate first
    Pointers Pointers `json:"pointers"`
    Narrative string  `json:"narrative"`

    // Metadata
    SketchRefs map[string]string `json:"sketch_refs"`
    Dropped    []DropEntry       `json:"dropped"`
    Cache      CacheInfo         `json:"cache"`
}
type UserIntent struct{ Original string `json:"original"`; Evolution []string `json:"evolution"` }
type Decision struct {
    ID core.DecisionID `json:"id"`
    What string `json:"what"`; Why string `json:"why"`
    AlternativesRejected []string `json:"alternatives_rejected"`
    Evidence core.Hash `json:"evidence"`
    Turn core.TurnIndex `json:"turn"`
}
type CurrentWork struct{ Goal, NextStep string; BlockedOn *string }
type Pointers struct {
    Files []FilePointer `json:"files"`   // {path, hash, why} — NO code snippets, ever (§4.4)
    Tools []ToolPointer `json:"tools"`   // {tool_use_id, hash, summary}
}
type DropEntry struct{ Kind string `json:"kind"`; ID string `json:"id"`; Detail string `json:"detail,omitempty"` }
type CacheInfo struct {
    PChosen int `json:"p_chosen"`; RewriteTokens int `json:"rewrite_tokens"`
    TTLState string `json:"ttl_state"`
}
// Invariant is DEFINED IN `pins` and aliased here: checkpoint imports pins (SourceSet), so the
// reverse edge would be a cycle (§3.2).
type Invariant = pins.Invariant

// ── Source rule (GC, §8.5). SourceSet has NO field that can carry live context text.
// Compilation makes "checkpoint from a summary" impossible.
type SourceSet struct {
    Store    store.Store
    Segments store.SegmentLog
    Ledger   negknow.Ledger
    Pins     pins.Store
    Graph    dag.Graph
    Grammar  grammar.Sequitur
    Tokens   tokens.Estimator
}

type Draft struct{ /* opaque; accumulates tiers incrementally */ }
type Ref struct {
    Seq core.CheckpointSeq; Path string; SHA256 core.Hash
    Bytes int64; Tokens core.Tokens; Frontier core.TurnIndex; Created core.UnixMilli
}

type Writer interface {
    Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error)
    // Advance encodes CLOSED, UNENCODED segments into the draft. Called during idle (O5).
    // Calls SegmentLog.MarkEncoded; returns ErrAlreadyEncoded on a DPI violation.
    Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error)
    // Finalize writes the immutable artifact via paths.CreateNew + MANIFEST append.
    // Must complete inside budget B-E (2 s p99) because everything heavy is already in the draft.
    Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error)
    Abort(d *Draft) error
}
type Reader interface {
    Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
    Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
    List(ctx context.Context) ([]Ref, error)
    Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error)
    Verify(ctx context.Context) ([]core.CheckpointSeq, error)   // MANIFEST re-hash; fsck
}

// Truncate applies importance ordering (§6.9): tier 3 first, then tier 2, tier 1 never.
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)

// Ground-truth validation (G2.5): pointer paths checked against the working tree / git status.
func ValidatePointers(ctx context.Context, root string, p Pointers) ([]DropEntry, error)

// ExtractDecisions mints tier-2 decisions from the SourceSet: EdgeExplains chains in the DAG,
// elimination records with an alternatives_rejected shape, and explicitly recorded decisions.
// It is the ONLY producer of core.DecisionID, and therefore the only thing that makes the
// `why(decision_id)` MCP tool answerable. A decision also gets a dag KindDecision node so slice
// scores can rank it. Owned by SP-10.
func ExtractDecisions(ctx context.Context, src SourceSet, from core.TurnIndex) ([]Decision, error)

type FocusOptions struct {
    IncrementalSpan bool          // O1
    Frontier        core.TurnIndex
    CheckpointPath  string
    ForbidSnippets  bool          // G3.4
}
// FocusInstructions emits the §8.5 template verbatim plus, when IncrementalSpan, the
// span-narrowing paragraph naming the checkpoint path and turn N.
func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string

// ── injection tagging (§8.5 regeneration rule) ──
const InjectionOpenTag  = "<!-- qompack:injected seq=%d ver=%d -->"
const InjectionCloseTag = "<!-- /qompack:injected -->"
func StripInjections(s string) string   // used when reading any transcript-derived text

// package pins
type Invariant struct{ ID, Text, Source string; Pinned core.UnixMilli }
type Store interface {
    Add(ctx context.Context, inv Invariant) error       // append-only
    Remove(ctx context.Context, id string) error        // tombstone record, never a rewrite
    All(ctx context.Context) ([]Invariant, error)
    Materialize(ctx context.Context) error              // regenerate invariants.json view
}
```

### 5.15 `internal/rehydrate` (L5), `internal/rules`, `internal/skills`

```go
type ItemKind uint8
const (
    ItemInvariants ItemKind = iota  // 1. pins, verbatim, always
    ItemUserIntent                  // 2. verbatim original intent, from L0 (G2.3)
    ItemEliminations                // 3. top-N by slice score + "already_tried covers the rest"
    ItemDecisions                   // 4. decisions with rationale
    ItemCurrentWork                 // 5. current work and next step
    ItemPointers                    // 6. pointers, not contents
    ItemDropReport                  // 7. explicit drop report (G4.5)
    ItemAffordance                  // 8. one line: recall / re_read / already_tried exist
)                                   // ORDER IS NORMATIVE — this is the §8.6 importance order.

type Item struct{ Kind ItemKind; Rank int; Tokens core.Tokens; Text string; Truncated bool }
type DropEntry = checkpoint.DropEntry

type Request struct {
    Session     core.SessionID
    Source      string             // startup | resume | compact | clear
    ProjectRoot string
    Budget      core.Tokens        // default 8_000–12_000 (§8.6); hard cap from config
    Checkpoint  checkpoint.Checkpoint
    Ref         checkpoint.Ref
    Cfg         config.Config
}
type Result struct {
    Items    []Item
    Text     string        // the additionalContext payload, injection-tagged
    Tokens   core.Tokens
    Dropped  []DropEntry
    Degraded bool
    Seq      core.CheckpointSeq
}
type Deps struct {
    Store store.Store; Ledger negknow.Ledger; Graph dag.Graph
    Rules rules.Scanner; Skills skills.Indexer; Tokens tokens.Estimator; Log logging.Logger
}
func Build(ctx context.Context, r Request, d Deps) (Result, error)

// StandingInstruction is item 3's companion, emitted verbatim (§8.7 design note):
//   "Before committing to an approach, call already_tried."
func StandingInstruction() string

// package rules  (G4.1, G4.2)
type Rule struct{ Path string; Globs []string; Body string; Tokens core.Tokens; Nested bool }
type Scanner interface {
    // PathScoped returns every rule whose `paths:` frontmatter glob matches any pointer path.
    PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error)
    // NestedClaudeMD returns CLAUDE.md files in directories containing a pointer-set file.
    NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error)
}

// package skills  (G4.4)
type Entry struct{ Name, Description, Source string }
type Indexer interface {
    // Index returns a compact skill index: names + one-line descriptions only, budgeted.
    Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
}
// The ~450-token skill-index budget of §8.6 is a CONFIG KEY
// (runtime.rehydrate.skillIndexTokens, §11.5), not a package constant: §11.6 forbids the
// literal 450 outside internal/config/defaults.go.
```

**Rehydration budget.** Appendix C has no rehydration budget key (`checkpoint.budgetTokens` is a
different budget — what the checkpoint may cost on disk), so the 8–12K cap of §8.6 lives in the
additive `runtime.rehydrate` namespace (§11.5). `Request.Budget` is filled from it; the hard cap
is `runtime.rehydrate.maxTokens`.

### 5.16 `internal/mcp` (L6)

```go
type Tool struct {
    Name        string
    Title       string
    Description string
    InputSchema json.RawMessage
    Handler     Handler
    Ephemeral   bool           // results born ephemeral (§8.7)
}
type Request struct {
    Session core.SessionID
    Name    string
    Args    json.RawMessage
    Deadline time.Time
}
type Content struct{ Type string; Text string; Meta map[string]any }
type Response struct {
    Content   []Content
    IsError   bool
    Ephemeral bool                 // → _meta.qompack.ephemeral = true
    Meta      map[string]any       // hash, span, truncated, promoted …
}
type Handler func(ctx context.Context, r Request) (Response, error)

type Server interface {
    Register(t Tool) error
    Serve(ctx context.Context, in io.Reader, out io.Writer) error   // JSON-RPC 2.0 / stdio
    Tools() []Tool
}
func NewServer(name, version string, log logging.Logger) Server

// ── the eight tools (§8.7). Arg/result types are normative. ──
type RecallArgs struct{ Query string `json:"query"`; K int `json:"k"` }              // default k=5
type RecallHit  struct{ Hash, Path, Tool, Summary string; TS string; Score float64 }
type ExpandArgs struct{ Hash string `json:"hash"`; ToolUseID string `json:"tool_use_id"`;
                        Full bool `json:"full"`; Span string `json:"span"` }
type ReReadArgs struct{ Path string `json:"path"`; At string `json:"at"`; Full bool `json:"full"` }
type AlreadyTriedArgs struct{ Target string `json:"target"`; Approach string `json:"approach"` }
type AlreadyTriedResult struct {
    State  string `json:"state"`   // "absent" | "active" | "stale"   ← three-way (§8.3)
    Reason string `json:"reason,omitempty"`
    Note   string `json:"note,omitempty"`
    Evidence string `json:"evidence,omitempty"`
}
type RecordEliminatedArgs struct {
    Target string `json:"target"`; Approach string `json:"approach"`; Reason string `json:"reason"`
    Scope  string `json:"scope"`;  DependsOn []string `json:"depends_on"`
}
type TimelineArgs struct{ From string `json:"from"`; To string `json:"to"` }
type WhyArgs      struct{ DecisionID string `json:"decision_id"` }
type DroppedArgs  struct{}

type ToolDeps struct {
    Store store.Store; Ledger negknow.Ledger; Checkpoints checkpoint.Reader
    Rehydrator DropReporter; Promoter Promoter; Cfg config.Config
}
func RegisterAll(s Server, d ToolDeps) error

// DropReporter backs `dropped` (implemented by rehydrate in wave 3).
type DropReporter interface{ CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error) }
// Promoter implements retrieval.promoteAfterExpansions (§8.7, Phase 7).
type Promoter interface {
    NoteExpansion(ctx context.Context, sess core.SessionID, h core.Hash) (count int, promoted bool, err error)
    Promoted(ctx context.Context, sess core.SessionID) ([]core.Hash, error)
}
```

**Span default.** `expand` and `re_read` return the *minimum sufficient span* by default
(`retrieval.defaultSpan: "minimal"`): the matching function/hunk resolved via the store's chunk
boundaries plus a symbol-aware widener, capped at `store.chunk.max`. `full=true` is the escape
hatch. Every response carries `_meta.qompack.ephemeral=true` when
`retrieval.ephemeralResults` is set, and the observer records the resulting tool-use record with
`Ephemeral: true` so `analyzer.Block.Ephemeral` ranks it first for eviction.

### 5.17 `internal/commands` (slash-command backends)

```go
type Command interface {
    Name() string                                  // status|recall|pin|checkpoint|why|dropped|eval
    Run(ctx context.Context, args []string, out io.Writer) error
}
func All(d Deps) []Command
type Deps struct {
    Store store.Store; Ledger negknow.Ledger; Checkpoints checkpoint.Reader
    Writer checkpoint.Writer; Pins pins.Store; Sched scheduler.Runtime
    Eval eval.Harness; Metrics obs.Registry; Contract contract.Monitor; Cfg config.Config
}
```

`/qompack:status` output is the observability surface (G8.1): mode (`full`/`degraded-passive`),
contract-assertion table, store size + dedup ratio, sketch fill ratios and estimated FP rate,
hook latency p50/p99 per hook against budgets B-A/B-D, frontier turn and residual tokens, last
checkpoint seq/size, last scheduler `Decision.Breakdown`, GC stats, and the last five `Loud`
messages.

### 5.18 `internal/eval` (L7)

```go
type Turn struct {
    Index core.TurnIndex; Role string; TS core.UnixMilli
    Text string; ToolCalls []ToolCall; Tokens core.Tokens
}
type ToolCall struct{ ID core.ToolUseID; Name string; Args, Result json.RawMessage; Paths []string }
type Session struct {
    ID string; Turns []Turn; CompactionAt []core.TurnIndex
    Meta map[string]string; Synthetic bool
}

type Policy interface {
    Name() string
    // KeepSet decides what survives a compaction at turn `at` under `budget`.
    KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}
type KeepSet struct{ IDs []string; Tokens core.Tokens; P int }

type Run struct {
    Policy string; Session string; Branch string   // "uncompacted" | "compacted"
    Actions []Action; Keeps []KeepSet
    PauseMS []int; ResidualSpan []core.Tokens; FirstTurnAfterMS []int
}
type Action struct{ Turn core.TurnIndex; Tool string; Paths []string; Decision string }

type Divergence struct {
    FirstDivergenceTurn int      // §11.2
    FileSetJaccard      float64
    ToolEditDistance    int
    SameDecision        bool
    DecisionPreservation float64
    RedundantReads      int
    ReAttempts          int
}
type Score struct {
    FractionOfOPT float64        // §11.1 PRIMARY
    Divergence    Divergence
    RewriteTokens int            // Σ w·(n − p_min)
    RehydrationTokens core.Tokens
    RetrievalHitRate float64
    CompactionPauseMS Percentiles
    ResidualSpan      Percentiles
    FirstTurnAfterMS  Percentiles
}
type Percentiles struct{ P50, P95, P99, Max float64 }

type Harness interface {
    Load(dir string) ([]Session, error)
    Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
    Compare(uncompacted, compacted Run) Divergence
    Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
    ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
    Report(ctx context.Context, scores map[string][]Score) (Report, error)
}
type ReplayOptions struct{ K int; Seed int64; Budget core.Tokens; Deterministic bool }
type Report struct {
    Policies map[string]Score
    Baseline string
    Regressions []Regression      // §11.3 2% rule
    Sessions int
    GeneratedAt time.Time
}
type Regression struct{ Metric, Policy string; Baseline, Observed, DeltaPct float64; Allowed bool }

// Synthetic fixtures (§6.3)
func Synthesize(seed int64, spec SynthSpec) Session
type SynthSpec struct {
    Turns int; ToolMix map[string]float64
    FileRereadRate float64; TestOutputNoise float64
    Changepoints int; Eliminations int; SubagentCalls int
    DependencyChangeAt []core.TurnIndex   // exercises §8.3 staleness
    CompactionAt []core.TurnIndex
}
```

**Divergence without an API.** `Replay` runs in two modes: `Deterministic` (default, CI-safe) —
the policy is applied to the *logged* action sequence and divergence is computed against what the
session actually did next, giving a reproducible, model-free estimate; and live mode (off by
default, `QOMPACK_EVAL_LIVE=1`, never in CI) which re-executes the fork against a real model.
CI gates on deterministic mode only; the release gate runs live mode over the recorded corpus.

### 5.19 `internal/contract` (G9.3, §12)

```go
type ID string
const (
    CSessionStartFires      ID = "session_start.fires"
    CSessionStartSourceCompact ID = "session_start.source_compact"
    CAdditionalContext      ID = "hook.additional_context_delivered"
    CPreCompactTiming       ID = "precompact.has_time_to_write"
    CPreCompactCustomInstr  ID = "precompact.custom_instructions_accepted"
    CHookPayloadShape       ID = "hook.payload_shape"
    CMCPRegistered          ID = "mcp.server_registered"
    CTranscriptReadable     ID = "transcript.readable"
    CPluginRootResolves     ID = "plugin.root_resolves"
)
type Severity uint8 // SevInfo, SevWarn, SevCritical
type Result struct {
    ID ID; OK bool; Severity Severity
    Expected, Observed string; TS core.UnixMilli; Detail string
}
type Mode uint8 // ModeFull, ModeDegradedPassive, ModeOff
func (m Mode) String() string

type Assertion struct {
    ID ID; Severity Severity; Description string
    Check func(ctx context.Context, e Env) Result
}
type Env struct {
    ProjectRoot string; Event hookio.Event; Cfg config.Config
    Store store.Store; Log logging.Logger; Clock core.Clock
    History History      // observed hook firings this session and previous ones
}
type Monitor interface {
    Register(a Assertion) error
    RunAll(ctx context.Context, e Env) ([]Result, Mode)
    Mode() Mode
    // Degrade logs LOUD, writes .qompack/logs/LOUD.log, sets mode, and persists the reason so
    // /qompack:status and the next SessionStart both surface it. NEVER silent.
    Degrade(reason string, results []Result)
    Restore(reason string)
    Report() []Result
}
func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor
func StandardAssertions() []Assertion
```

### 5.20 `internal/tokens` (G10.2)

```go
type Class uint8 // ClassProse, ClassCode, ClassJSON, ClassDiff, ClassImage, ClassPDF, ClassBinary
type Estimator interface {
    Estimate(b []byte, c Class) core.Tokens
    EstimateString(s string, c Class) core.Tokens
    // EstimateRoot uses per-chunk cached measurements keyed by chunk hash — the "exact
    // chunk-level accounting" that closes G10.2; it never re-scans bytes already accounted for.
    // Takes []core.ChunkRef, NOT store.Root: `tokens` must not import `store` (§3.2).
    EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
    Calibrate(observed core.Tokens, estimated core.Tokens)  // per-project factor, persisted
    Factor() float64
}
func New(cfg config.Config, calibPath string) Estimator
func Classify(tool, path string, b []byte) Class
```

Images and PDFs are estimated from actual dimensions/page count, not the host's flat 2 000.
Calibration compares our estimate against `usage.input_tokens` deltas parsed from the transcript
when available; the factor is clamped to `[0.6, 1.6]` and stored in `~/.qompack/calibration.json`.

### 5.21 `internal/observer` (L0 semantics)

```go
type Observer interface {
    OnToolUse(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnUserPrompt(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnStop(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error)
    OnSessionStart(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnSessionEnd(ctx context.Context, e hookio.Event) (hookio.Output, error)
}
// Tombstone renders the addressable marker of §8.1 item 2:
//   [cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]
func Tombstone(rec store.ToolUseRecord) string
type Signals struct{ TodoCompleted, TestPassed, GitCommit bool; Paths []string } // G1.5 → L3
func ExtractSignals(e hookio.Event) Signals
```

**Split ownership of the two multi-owner hooks — normative, so three subplans do not fight over
one file.**

| Hook | Owner of the CLI/daemon entry point | Owner of the semantics |
|---|---|---|
| `SessionStart` | SP-05: `qompack session-start` dispatch, daemon start, `contract.Monitor.RunAll` before any other work | SP-08: `startup`/`resume` branch (session registration, store open, warm state). SP-11: the `compact` branch (rehydration) and `clear` reset. The `source` switch itself lives in `observer.OnSessionStart` and delegates. |
| `SessionEnd` | SP-05 (`qompack flush` client + daemon op) | SP-08 owns `observer.OnSessionEnd`; it calls `store.Flush`, the session-index write and `store.GC`, whose mechanics SP-06 owns. No subplan other than SP-08 writes code in `internal/observer`. |

**Segment lifecycle.** SP-08 opens a segment at session start and appends turns/verbatim prompts
to it; SP-12 closes segments on a changepoint, todo completion, or passing test and advances the
frontier (O5). `SegmentLog` itself is SP-06's.

### 5.22a `internal/redact` (§13 invariant 7, `runtime.redact`)

Secrets must never reach `objects/`. Redaction is therefore applied at the single choke point —
`store.Put`/`PutBytes`, before canonicalization and chunking — not at the audit stage.

```go
type Match struct{ Offset, Len int; Rule string }
type Redactor interface {
    // Redact replaces every match with a fixed-width placeholder "«redacted:<rule>»".
    // MUST be deterministic and MUST NOT grow the input beyond a bounded factor, so chunk
    // boundaries stay stable between a redacted and an unredacted read of the same file.
    Redact(in []byte) (out []byte, matches []Match)
    Rules() []string
}
func New(cfg config.Config) Redactor   // built-in rules + runtime.redact.patterns
func Nop() Redactor                    // tests only
```

Built-in rules: private-key PEM blocks, `AKIA…`/`ASIA…`, `ghp_`/`gho_`/`github_pat_`,
`sk-`/`sk-ant-`, bearer tokens, `password=`/`secret=`/`token=` assignments, `.env` value lines,
JWTs, and connection strings with embedded credentials. A fuzz target asserts idempotence
(`Redact(Redact(x)) == Redact(x)`) and that no rule ever matches across the whole input.

### 5.22b `internal/symbols`

One owner, four consumers: `store.Query.Symbol` (recall), `dag` shared-symbol edges,
`analyzer`'s symbol-reference counting (cheap Δ proxy), and the `mcp` symbol-aware span widener.
Heuristic and language-agnostic — regex/brace-scanning, no parser dependency.

```go
type Symbol struct{ Name, Kind string; Line, Offset, Len int }  // Kind: func|type|class|const|var
type Extractor interface {
    Extract(path string, b []byte) []Symbol
    // Enclosing returns the smallest symbol span containing off — the minimal-sufficient-span
    // resolver behind retrieval.defaultSpan = "minimal" (§8.7).
    Enclosing(path string, b []byte, off int) (Symbol, bool)
    References(b []byte, names []string) map[string]int
}
func New() Extractor
```

### 5.22 Conformance suites (D9 — the mechanism that makes waves work)

For every interface above, SP-01 ships an exported test suite in a `<pkg>test` subpackage:

```go
// package storetest
func RunStoreSuite(t *testing.T, name string, factory func(t *testing.T) store.Store)
func RunSegmentLogSuite(t *testing.T, factory func(t *testing.T) store.SegmentLog)
// package sketchtest, canontest, dagtest, negknowtest, checkpointtest, rehydratetest,
// schedulertest, mcptest, evaltest, ipctest, symbolstest, redacttest, tokenstest — same shape.
```

Rules:

- **W-1.** The stub implementation shipped by SP-01 passes the suite's *shape* tests
  (signatures, error sentinels) and is `t.Skip`ped for behaviour. The owning subplan flips those
  skips off; it is a merge blocker if any remain.
- **W-2.** A subplan that consumes an interface owned by a **same-wave** subplan must test against
  golden fixtures in `testdata/golden/contracts/<pkg>/`, generated by SP-01. The wave's
  verification checkpoint re-runs those same tests against the real implementation. Any
  fixture that the real implementation cannot reproduce is a verification failure, not a
  fixture bug.
- **W-3.** No subplan may add a method to another subplan's interface. It requests an amendment.

---

## 6. Test infrastructure

### 6.1 Framework and layout

- Standard `go test`. Assertions via `testify/require` (fail-fast) — `assert` is banned.
- Structural diffs via `go-cmp` with explicit `cmpopts` (no reflection surprises).
- Property tests via `pgregory.net/rapid` for: FastCDC boundary stability, canonicalizer
  idempotence and inverse, sketch marshal/unmarshal round-trips, submodular greedy's
  `(1−1/e)` bound against brute force on small instances, BOCD posterior normalization,
  Sequitur's two invariants (no digram twice, every rule used more than once).
- Native fuzzing (`go test -fuzz`) with seed corpora in `testdata/corpora/`: `chunk.Split`,
  every canonicalizer, `sketch.UnmarshalBinary`, `hookio.ReadEvent`, `ipc` framing,
  `config.Load`, `checkpoint` JSON. Fuzz corpora are committed; nightly CI runs 10 min/target.
- Unit tests live beside their package. Cross-package integration lives in `test/e2e`.
- **Every test that touches time takes `testutil.FakeClock`.** Wall-clock sleeps are banned;
  `devtool lint` greps for `time.Sleep` outside `test/bench`.

### 6.2 Temp-project fixture

```go
// package testutil
type Project struct{ Root string; Cfg config.Config; Clock *FakeClock; Log logging.Logger }
func NewProject(t *testing.T, opts ...ProjectOpt) *Project   // uses t.TempDir(); sets QOMPACK_PROJECT_ROOT
func (p *Project) Store(t *testing.T) store.Store
func (p *Project) RunHook(t *testing.T, name string, e hookio.Event) hookio.Output // real binary or in-proc
func (p *Project) WithFiles(t *testing.T, files map[string]string) *Project
func (p *Project) AssertAppendOnly(t *testing.T)     // §3.3 guard assertions
```

Windows specifics covered by the fixture: paths with spaces, a path > 260 chars, a
case-colliding pair (`Foo.ts` / `foo.ts`), CRLF files, and a read-only file.

**Ownership.** `internal/testutil` (fixture, `FakeClock`, golden helpers) and the `test/e2e`
harness scaffolding (build the real binary, run a hook against a real daemon, assert the JSON
response) are **SP-01** deliverables — every wave-1 subplan needs them on day one. Each subsequent
subplan adds its own `test/e2e` cases; SP-17 owns the full cross-platform matrix.
`eval.Synthesize` and the synthetic corpus are SP-02's, not `testutil`'s.

### 6.3 Session fixtures (replay tests)

Three tiers, in ascending fidelity and descending availability:

1. **Synthetic, in-repo, deterministic** — `testdata/sessions/synthetic/*.json`, 24 sessions
   generated by `eval.Synthesize(seed, spec)` and committed. Specs deliberately span the failure
   space: read-heavy, test-output-heavy (the O2 canonicalization case), refactor-across-files,
   long-idle-gap, dependency-change-mid-session (staleness), subagent-heavy (G10.1),
   thrash-loop (Sequitur), and multi-compaction. This corpus is what CI runs. `eval.minSessions`
   defaults to 20 and the corpus is 24, so the config default is satisfiable offline.
2. **Recorded, out-of-repo** — real Claude Code transcripts imported by
   `qompack eval import --from <dir>` from `~/.claude/projects/**/**.jsonl`, redacted
   (secrets, absolute home paths, emails) by `eval.Redact`, stored under `$QOMPACK_SESSIONS_DIR`.
   Never committed. Gates releases (§8), not PRs.
3. **Live fork** — `QOMPACK_EVAL_LIVE=1`, real model calls. Manual, pre-release only.

The synthetic generator is itself tested: a golden test asserts that a given seed produces a
byte-identical session, so replay numbers are comparable across commits.

### 6.4 Coverage and gates

| Package group | Line coverage floor |
|---|---|
| `config`, `store`, `sketch`, `chunk`, `canon`, `negknow`, `checkpoint`, `paths`, `redact`, `tokens` | **90%** |
| `scheduler`, `dag`, `analyzer`, `rehydrate`, `eval`, `mcp` | **85%** |
| everything else | **75%** |

Coverage is measured on the merged profile from the Linux job. A drop below the floor fails
`verify`. Coverage is a floor, never a target — subplans are graded on the conformance suite
and the replay gate.

**Composition roots are exempt.** A `main` package that declares nothing but `func main`, whose
body only constructs dependencies and hands off to a library entry point, carries no floor. The
exemption is narrow and mechanical: the moment such a package declares a second function, a
method, or a package-level variable with logic in it, the floor applies again in full. It exists
because `func main` ends in `os.Exit`, which no in-process test can survive, so the only honest
way to exercise a composition root is to spawn the real binary — and that coverage is credited to
the `test/e2e` package that did the spawning, never to the `main` package itself. Chasing the
number there would mean moving dispatch logic out of `main` for the tool's benefit rather than
the design's, or writing a test that asserts nothing. The exemption is printed in the job log
next to the stub exemptions so it stays visible rather than silent.

---

## 7. Benchmark and latency harness

`test/bench/hotpath` is a standalone Go program (not `go test -bench`) because it must measure
**real process spawns**:

```
devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon --json out.json
```

It: starts a real daemon against a temp project, pre-populates it with a realistic session
(2 000 tool uses, 40 MB of raw tool output so the CMS and DAG are warm), then spawns the real
`qompack observe tool` binary N times with a representative payload on stdin, recording
per-invocation wall time and reading the daemon-side B-B histogram at the end.

Outputs `{budget_id, n, p50, p95, p99, p999, max, pass}` for B-A, B-B, B-D. CI runs it on
ubuntu-latest, macos-latest, and windows-latest with `n=2000` (5 000 nightly) and **fails the
build if B-A p99 ≥ 15 ms or B-E p99 ≥ 2 s**. B-D is recorded and posted as a PR comment but is
never a gate — that is the honest treatment of a cost we do not own.

Micro-benchmarks (`go test -bench=. ./...`) cover FastCDC throughput (MB/s), canonicalizer
throughput, sketch op cost, `BackwardSlice` on 5 000 nodes, `scheduler.Evaluate`, and
`checkpoint.Finalize`. `devtool bench-compare` runs `benchstat` against the `develop` baseline
stored in `testdata/bench-baseline.txt`; a >10% regression on any micro-benchmark posts a warning,
a >25% regression fails. It runs locally on the baseline's own machine (V2 ruling recorded in
ci.yml's bench-gate comment): the committed baseline is single-host, and cross-machine deltas on
the I/O-bound rows would make a CI comparison fail constantly and then get switched off. Per-OS
baselines recorded on the runners are the stated precondition for wiring it into `bench-gate`.

---

## 8. CI pipeline (GitHub Actions)

`.github/workflows/ci.yml` — **runs on every push to every branch** and on every PR into
`develop` or `main`.

| Job | Runs on | Steps |
|---|---|---|
| `verify` | ubuntu | `gofumpt -l` (must be empty) · `golangci-lint run` · `go vet` · custom `nomagic` pass · import-graph layer check · test-only-dep check · `go build ./...` |
| `test` | ubuntu, macos, windows × go 1.26.x | `go test ./...` ; `-race` on ubuntu+macos, `-count=2` on windows (race nightly) |
| `cover` | ubuntu | merged profile, per-group floors (§6.4), artifact upload |
| `crossbuild` | ubuntu | `GOOS/GOARCH` matrix build for all 6 release targets |
| `bench-gate` | ubuntu, macos, windows | `devtool bench-hotpath -n 2000`; hard fail on B-A / B-E |
| `replay-gate` | ubuntu | `devtool replay --corpus testdata/sessions/synthetic --baseline develop`; enforces §11.3 (no metric regresses >2% to improve another without a `sign-off:` trailer in the PR body) and the phase exit criterion of every phase merged so far |
| `plugin-validate` | ubuntu | regenerate `plugin/**` from `internal/pluginmanifest`, `git diff --exit-code`; JSON-schema-validate `plugin.json`, `hooks.json`, `.mcp.json`; assert all 7 commands and 8 MCP tools present |
| `security` | ubuntu | `govulncheck ./...` · `gosec` · secret scan · assert zero non-test imports of `net/http`, `net/url`, `crypto/tls` anywhere; `net` only in `internal/ipc` (Unix sockets — and only `net.Dial`/`net.Listen` on `unix`, never `tcp`); `os/exec` only in `internal/daemon` (detached self-spawn), `internal/cli` and `tools/` |
| `docs` | ubuntu | `devtool gen-config-docs`, `git diff --exit-code` — `docs/config-reference.md` can never drift from `config.Defaults()` |

`.github/workflows/nightly.yml`: fuzz (10 min/target), Windows `-race`, 5 000-iteration bench,
live-mode replay when `QOMPACK_SESSIONS_DIR` secret is present.
`.github/workflows/release.yml`: tag-triggered, `goreleaser`, plugin bundle assembly, checksum +
provenance attestation.

`bench-gate` and `replay-gate` are **required checks on `develop` and `main`** from the end of
wave 1 onward (they cannot be required before SP-02 and SP-05 exist).

---

## 9. Git strategy

**The repository is not initialized. SP-01 initializes it.**

1. `git init` at `C:/Users/Quant/Documents/Programming/Projects/qompack`, default branch `main`.
2. `.gitignore` (contents below), then the **initial commit on `main`** containing exactly
   `Qompack.md`, `plans/` (this file and all 18 subplan prompts), `.gitignore`, `LICENSE`.
   Message: `chore: initial commit — design document and build plans`.
3. `git branch develop` from that commit; `develop` is the integration branch. `main` only ever
   receives merges from `develop` at release tags.
4. The rest of SP-01's work lands on `feat/sp01-foundation-toolchain-and-contracts`, cut from
   `develop`, and merges back into `develop` at the end of wave 0.

`.gitignore` (normative minimum):

```gitignore
# Qompack runtime store — never committed
/.qompack/
**/.qompack/

# Build artifacts
/bin/
/dist/
/plugin/bin/
*.exe
*.dll
*.so
*.dylib
*.test
*.out
coverage.*
/testdata/bench-*.json

# Recorded sessions contain user code and prompts
/testdata/sessions/recorded/*
!/testdata/sessions/recorded/.gitkeep

# Tooling
.idea/
.vscode/*
!.vscode/extensions.json
.DS_Store
Thumbs.db
```

**Branching.**

- One feature branch per subplan: `feat/sp<NN>-<slug>`, cut from the **current `develop`** at the
  start of its wave. Example: `feat/sp08-observer-l0`.
- Subplans in the same wave never branch from each other and never merge into each other. They
  integrate only through SP-01's interfaces and `testdata/golden/contracts/` (Rule W-2).
- At the end of a wave, all of that wave's branches merge into `develop` **in the stated order**
  (§ merge strategy in the subplan decomposition), each merge with `--no-ff` so the wave
  structure is visible in the history. Conflicts are resolved on the *incoming* branch, then
  re-merged — never with a hand-edited merge commit.
- A **verification checkpoint** then runs on `develop`. Fixes for it go on `verify/v<K>` cut from
  `develop` and merge back with `--no-ff` before the next wave is cut.
- The next wave's branches are cut from the post-verification `develop`. No wave-N branch is ever
  cut before verification V<N> is green.
- Emergency architecture changes: `arch/<reason>` off `develop`, must land before dependent work.
- Worktrees are encouraged for parallel subplans:
  `git worktree add ../qompack-sp07 feat/sp07-observer-l0`.

**Tags.** `v0.<wave>.<n>` on `develop` after each verification; `v<semver>` on `main` at release
(SP-17).

---

## 10. Commit conventions

**Conventional Commits**, enforced by a `commit-msg` hook installed by
`devtool install-hooks` and re-checked in CI:

```
<type>(<scope>): <subject>

<body — why, not what>

<footer — Refs: SP-08, G3.2, §8.1>
```

- `type` ∈ `feat fix docs test refactor perf build ci chore revert`
- `scope` = the Go package (`store`, `scheduler`, `mcp`) or the subplan slug for cross-cutting work
- subject: imperative mood, ≤ 72 chars, no trailing period
- body: wraps at 100; explains the decision, not the diff
- footer: `Refs:` naming the subplan, the gap IDs, and the `Qompack.md` sections implemented

**Commit count: 5–8 commits per subplan.** Not one giant commit, not forty. Each commit compiles
and passes `devtool test` for the packages it touches. A reasonable shape for a subplan:
(1) types and interfaces, (2) core algorithm, (3) persistence/wiring, (4) hook/CLI/MCP surface,
(5) tests and fixtures, (6) benchmarks or replay integration, (7) docs/ADR, (8) config wiring.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is verbatim and absolute. It applies to every commit, merge commit, tag message, and PR
body in this repository. CI's `verify` job greps for `Co-Authored-By`, `Signed-off-by`,
`Generated with`, and `🤖` in the commit range and fails the build if any appear.

---

## 11. Configuration

### 11.1 Appendix C schema — verbatim

The following is reproduced **verbatim** from `Qompack.md` Appendix C. It is the normative
default configuration. `config.Defaults()` must produce exactly this document (modulo the
`runtime` extension of §11.5), and a golden test asserts it.

```jsonc
{
  "store": {
    "chunk": { "min": 1024, "target": 4096, "max": 16384 },
    "compression": "zstd",
    "retention": { "days": 30, "sessions": 10 },
    "canonicalize": {
      "enabled": true,
      "strip": ["timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"],
      "minhash": { "enabled": true, "permutations": 128, "nearDupThreshold": 0.9 }
    }
  },
  "scheduler": {
    "softFloorPct": 0.55,
    "hardCeilingMargin": 20000,
    "youngDaly": { "enabled": true, "measuredDeltaSeconds": null },
    "changepoint": { "hazardRate": 0.004, "features": ["paths","tools","time","todos"] },
    "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
    "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
  },
  "checkpoint": {
    "budgetTokens": 12000,
    "incrementalSpanInstruction": true,
    "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
    "tiers": { "never": ["invariants","user_intent","eliminated"],
               "late":  ["decisions","open_questions","current_work"],
               "first": ["pointers","narrative"] }
  },
  "sketches": {
    "bloom": { "capacity": 10000, "fpRate": 0.01 },
    "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
    "hll":   { "registers": 2048 }
  },
  "eliminations": {
    "requireEvidence": true,
    "defaultScope": "session",
    "rebuildOnStale": "nextIdle",
    "staleResponse": "flag"          // "flag" | "drop"
  },
  "retrieval": {
    "ephemeralResults": true,
    "defaultSpan": "minimal",        // "minimal" | "full"
    "promoteAfterExpansions": 2
  },
  "selection": {
    "slicing": "thin",
    "deltaScoring": "cheap",
    "submodular": { "lambda": 0.4, "lazyGreedy": true }
  },
  "eval": { "replayOnPhaseGate": true, "minSessions": 20 }
}
```

### 11.2 Loading

`config.Load(Env)` composes five layers, deep-merged per leaf key, lowest precedence first:

| # | Layer | Location | Origin |
|---|---|---|---|
| 1 | Built-in defaults | `config.Defaults()` (the document above) | `OriginDefault` |
| 2 | User global | `~/.qompack/config.json` | `OriginUserFile` |
| 3 | Project | `<projectRoot>/.qompack/config.json` | `OriginProjectFile` |
| 4 | Environment | `QOMPACK_<SECTION>__<KEY>__<SUBKEY>` | `OriginEnv` |
| 5 | Flag | `--set <dotted.key>=<value>` on any subcommand | `OriginFlag` |

JSONC is accepted (the schema above contains `//` comments): a tolerant reader strips line and
block comments and trailing commas before parsing. Numbers keep JSON semantics; `null` on
`youngDaly.measuredDeltaSeconds` means "measure at runtime", not "zero", and is modelled as
`*float64`.

The daemon reloads config when the project file's mtime changes, at `SessionStart` and on every
idle tick. Reload never applies to `store.chunk.*` mid-session (it would fork the dedup space);
a change there is recorded and applied at the next `SessionStart`, logged loudly.

### 11.3 Validation

`Validate()` returns `[]Violation` (never panics). Rules include:
`chunk.min < chunk.target < chunk.max`; `compression ∈ {zstd, none}`;
`retention.days ≥ 1`, `retention.sessions ≥ 1`; `strip` values are known `canon.Class`es;
`permutations ∈ [16, 512]`; `nearDupThreshold ∈ (0,1]`; `softFloorPct ∈ (0,1)`;
`hardCeilingMargin > 0`; `hazardRate ∈ (0,1)`; `readMultiplier ∈ (0,1]` and
`writeMultiplier ≥ 1`; `ttlSeconds > 0`; `budgetTokens ∈ [1_000, 100_000]`;
`maxResidualTokens > 0`; each tier list is a subset of the known checkpoint field names and the
three lists are disjoint and cover every truncatable field; `bloom.capacity ≥ 100`,
`fpRate ∈ (0, 0.25)`; `cms.epsilon ∈ (0,1)`, `cms.delta ∈ (0,1)`; `hll.registers` a power of two
in `[64, 65536]`; `defaultScope ∈ {session, project}`; `rebuildOnStale ∈ {nextIdle, immediate, never}`;
`staleResponse ∈ {flag, drop}`; `defaultSpan ∈ {minimal, full}`;
`promoteAfterExpansions ≥ 1`; `slicing ∈ {thin, full}`; `deltaScoring ∈ {cheap, medium, expensive}`;
`submodular.lambda ≥ 0`; `eval.minSessions ≥ 1`.

**Behaviour on invalid config is not "crash".** A hook that dies takes observability with it.
`Load` returns the *validated-and-corrected* config: every violating leaf falls back to its
default, the violation is reported through `logging.Loud`, surfaced in `/qompack:status`, and
recorded in `.qompack/state/config-violations.json`. Unknown keys produce a `Warning`, never an
error (forward compatibility with newer plugin versions writing config we do not know yet).

### 11.4 Provenance and introspection

Every leaf carries an `Origin` and a `Location`. `qompack config print --provenance` prints the
effective config annotated with where each value came from, which is the first thing
`docs/troubleshooting.md` tells a user to run. `qompack config schema` emits the JSON Schema;
`devtool gen-config-docs` renders `docs/config-reference.md` — **every key, its type, its
default, its valid range, and the `Qompack.md` section that motivates it** — and CI fails if that
file is stale (§8, `docs` job).

### 11.5 The `runtime` extension namespace

Appendix C does not cover process-level concerns that the daemon architecture introduces. These
live under a clearly-marked additive namespace. **No key here may change the meaning or default
of any Appendix C key.**

```jsonc
"runtime": {
  "mode": "auto",                     // "auto" | "full" | "passive" | "off"
  "daemon": { "enabled": true, "idleExitSeconds": 1800, "maxSessions": 8,
              "ackDeadlineMs": 8, "connectDeadlineMs": 5 },
  "hotPath": { "budgetMs": 15, "breachWindows": 3, "spoolOnBreach": true,
               "maxPayloadBytes": 1048576 },
  "logging": { "level": "info", "maxFileMB": 10, "maxFiles": 5 },
  "redact": { "enabled": true, "patterns": [] },   // secrets never enter the store
  "telemetry": { "enabled": false },               // hardwired off; key exists to say so
  "rehydrate": { "minTokens": 8000, "maxTokens": 12000,   // §8.6 8–12K cap; Appendix C has no key
                 "skillIndexTokens": 450,                 // §8.6 ~450-token skill index
                 "eliminationsTopN": 8 },
  "mcp": { "spanWidenLines": 40, "maxResponseBytes": 262144 }
}
```

### 11.6 The no-hardcoding rule (D11, §12 risk "Cache multipliers change")

`scheduler.cache.readMultiplier` (`r`), `writeMultiplier` (`w`), and `ttlSeconds` are read from
config at every use site. There is no package-level `const r = 0.1` anywhere. The in-repo
`nomagic` analysis pass fails the build on any float literal in `{0.1, 1.25, 12.5, 0.55, 0.004,
0.9, 0.4}` or integer literal in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}`
appearing outside `internal/config/defaults.go`, `*_test.go`, and explicitly annotated
`//nomagic:allow <reason>` lines. The ski-rental threshold is computed as `w/r`, never written as
`12.5`. `450` is in that set, which is why `skills.Index` takes its budget from
`runtime.rehydrate.skillIndexTokens` rather than a package constant (§5.15); the set is extended
with `{8000, 12000}` when §11.5's `rehydrate` keys land in SP-01.

---

## 12. Degradation doctrine

Qompack is a sidecar (§7.1). **It must never be the reason a session gets worse.** Three
independent degradation mechanisms, each with an explicit, observable state.

### 12.1 Contract monitor (G9.3, §12 row 1)

Every `SessionStart`, before any other work, `contract.Monitor.RunAll` executes
`StandardAssertions()` (§5.19). Each assertion is a real observation, not a version check:

| Assertion | How it is asserted |
|---|---|
| `session_start.fires` | a marker written at `SessionEnd`/`PreCompact` is found by the next `SessionStart`; absence across two sessions ⇒ fail |
| `session_start.source_compact` | after a `PreCompact` is observed, the next `SessionStart` must arrive with `source == "compact"` within the same session id. Recorded in `state/contract.json` and evaluated on the *following* start |
| `hook.additional_context_delivered` | `SessionStart` emits a sentinel token in `additionalContext`; the next `UserPromptSubmit` reads the transcript tail and looks for it. Not found ⇒ fail |
| `precompact.has_time_to_write` | measured `PreCompact` wall time vs. the manifest timeout; p99 > 60% of timeout ⇒ warn, timeout hit ⇒ fail |
| `precompact.custom_instructions_accepted` | the emitted instruction's sentinel phrase is searched for in the post-compaction summary; absent ⇒ warn (advisory by design, §8.5) |
| `hook.payload_shape` | required fields present and typed as `hookio.Event` expects |
| `mcp.server_registered` | the MCP server received `initialize` at least once this session |
| `transcript.readable` | `transcript_path` exists and parses |
| `plugin.root_resolves` | `${CLAUDE_PLUGIN_ROOT}` expanded to an existing binary |

**Assertions whose observable does not exist yet.** Some assertions depend on a subsystem that a
later wave delivers: `mcp.server_registered` (SP-13), `precompact.custom_instructions_accepted`
and `precompact.has_time_to_write` (SP-10), `hook.additional_context_delivered` (SP-11). An
assertion whose *producer is absent from the build* returns `OK: true, Severity: SevInfo` with
`Observed: "not-yet-implemented"` — it must never degrade the session. Wiring this wrong would
put every wave-1 and wave-2 verification run into `degraded-passive` and silently disable the very
paths those waves are testing. A CI test asserts a freshly built `develop` reports `ModeFull`.

**On any `SevCritical` failure:** `Monitor.Degrade` is called. That means, in order:
`logging.Loud` (log file + `LOUD.log` + `systemMessage` on the next hook that may emit one),
persist the reason to `state/contract.json`, set `Mode = ModeDegradedPassive`, and record it in
`obs`. `/qompack:status` leads with a banner naming the failed assertion, what was expected, and
what was observed. **Nothing fails silently — that is the whole point of the mechanism.**

`ModeDegradedPassive` behaviour: L0 and L1 keep running (observe, chunk, store, sketches, DAG,
verbatim capture, elimination records — the store stays correct and the session's data is not
lost). Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`,
no scheduler-initiated checkpoints, no drop report. MCP retrieval tools stay available, because
they are pull-based and cannot make anything worse. The session then behaves exactly as it does
without Qompack, which is §7.1's stated requirement.

Recovery: assertions re-run every `SessionStart`. Two consecutive clean runs call
`Monitor.Restore`, logged just as loudly as the degradation.

### 12.2 Hot-path overrun (§8.1)

Described in §2.4. `sync` → `spool` submode transition on 3 consecutive breach windows; logged at
WARN; visible in `/qompack:status`; automatically reverts after 3 clean windows in a subsequent
session. Under `spool`, the client never connects: it appends and exits, and the daemon drains on
its idle tick. Data is not lost; only freshness is.

### 12.3 Everything else fails toward "do nothing"

| Failure | Response |
|---|---|
| daemon unreachable | client spools, exits 0, spawns a detached daemon for next time |
| spool write fails | drop the event, increment `obs.Counter("l0.dropped")`, `Loud` once per session |
| store corrupt (bad CRC, truncated object) | quarantine the object to `.qompack/tmp/quarantine/`, `Loud`, continue; `qompack fsck` repairs |
| checkpoint MANIFEST mismatch | `Loud`, refuse to use the affected checkpoint, fall back to its parent, degrade to passive |
| bloom load fails | rebuild from `records/eliminations.jsonl` (§3.3); if that fails, `already_tried` returns `absent` for everything — never a false positive |
| config invalid | per-leaf fallback to default + `Loud` (§11.3) |
| MCP tool panic | recovered at the handler boundary, returned as `IsError`, never kills the server |
| any hook panic | recovered in `cli`, logged, `exit 0` with empty output |
| `PreCompact` about to exceed its timeout | `Finalize` the draft as-is (importance-ordered, so a truncated checkpoint is still the best available for its size — §6.9) and return |

---

## 13. Invariants every subplan must uphold

1. **Never compress a compression (§4.6).** Any content written into a checkpoint is encoded from
   the store's original chunks or from structured records. `SourceSet` (§5.14) makes the
   alternative uncompilable; `SegmentLog.MarkEncoded` makes it detectable; the injection tags make
   surviving in-context injections identifiable and ignorable.
2. **Append-only means append-only (§7.4).** `checkpoints/`, `pins/`, `sketches/tried.bloom`.
   Enforced by `paths` and by `TestAppendOnlyGuard`.
3. **The bloom filter is a cache, never the source of truth (§8.3).** Every membership answer is
   backed by a record lookup or explicitly flagged `BloomOnly`.
4. **Nothing scattered before `p` (§5.3).** `analyzer.NewSelector` refuses blocks with
   `Pos < p`. Do not add a bypass.
5. **No code snippets in checkpoints (§4.4).** Files are `{path, hash, why}`. A test greps
   checkpoint goldens for multi-line code blocks and fails.
6. **Hooks exit 0. Always.**
7. **No network. No telemetry. No writes outside `.qompack/`** (plus `~/.qompack/` for the global
   layer). CI asserts the import graph and a runtime test asserts the write set.
8. **Every constant that §12 says might change is a config key** (§11.6).
9. **Every latency budget is measured, not assumed** (§7). Adding work to L0 without a bench
   result is a review rejection.
10. **Degradation is loud** (§12). A silent fallback is a bug, regardless of how well it works.

---

## 14. Wave and subplan map

| Wave | Subplans | Verification |
|---|---|---|
| 0 | SP-01 foundation, toolchain, contracts | V1 |
| 1 | SP-02 replay/Belady/baseline · SP-03 sketches · SP-04 chunking+canonicalization+symbols · SP-05 daemon/IPC/hot path · SP-06 store+redaction · SP-07 dependence DAG + slicing | V2 |
| 2 | SP-08 observer L0 · SP-09 negative knowledge | V3 |
| 3 | SP-10 checkpointer L4 · SP-11 rehydrator L5 · SP-12 scheduler L3 · SP-13 MCP retrieval | V4 |
| 4 | SP-14 slash commands + observability · SP-15 analyzer selection + grammar · SP-16 Phase 7 refinements | V5 |
| 5 | SP-17 packaging/hardening/release · SP-18 documentation + UAT | V6 |

Three placements are load-bearing and must not be "optimized" back:

- **`dag` is wave 1, not wave 2.** It imports foundation packages only (§3.2), so nothing forces
  it later, and both wave-2 subplans plus SP-12's `segment_coupling(p)` consume it. Leaving it in
  wave 2 would make the observer, the negknow heuristic detector, and p-selection all depend on a
  same-wave sibling.
- **MCP is wave 3, not wave 2.** `already_tried`/`record_eliminated` need SP-09's ledger, `why`
  needs SP-10's `checkpoint.Reader`, and `dropped` needs SP-11's `DropReporter`. In wave 2 three
  of the eight tools would be built against stubs their own wave could not satisfy.
- **Slash commands are wave 4, not wave 3.** `commands.Deps` names `checkpoint.Writer`,
  `scheduler.Runtime`, and the MCP handlers — all wave 3.

Ownership of every coverage-checklist item, the per-wave merge order, and verification gating are
specified in the subplan decomposition that accompanies this document and are restated at the top
of each `plans/NN-*.md` file.

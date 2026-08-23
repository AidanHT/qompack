# SP-17: Production: installable plugin packaging, cross-platform validation, security and error-handling audit, hardening, and the release pipeline

> **Recommended model: Opus 5 · xhigh effort**
>
> Six-target cross-compilation, launcher shims, deterministic bundle assembly, security/fault/platform audits and a release pipeline. Broad and verification-heavy but mechanical — the judgment calls are already made in the plan.

**Branch:** `feat/sp17-packaging-hardening-and-release` (cut from `develop`) | **Wave:** 5 | **Prerequisites:** the branches of SP-01, SP-05, SP-06, SP-10, SP-11, SP-12, SP-13, SP-14, SP-15, SP-16 already merged into `develop` (post-V5 `develop`; SP-02, SP-03, SP-04, SP-07, SP-08, SP-09 are transitively present because waves 1–4 all merged) | **Runs in parallel with:** sibling subplans of wave 5 — SP-18 (documentation + UAT). SP-18 merges **after** SP-17 so it documents the artifact that actually ships. No file is shared: SP-18 owns `README.md`, `docs/user-guide.md`, `docs/troubleshooting.md`, `docs/config-reference.md`, `docs/cannot-do.md`, `docs/upstream-issues.md`, `docs/uat.md`; SP-17 owns `CHANGELOG.md`, `docs/security.md`, `docs/release.md`, `docs/install.md`. **`docs/security.md` ownership, resolved:** an earlier draft of SP-18 also listed that file in its `OwnedDocs()` inventory and its §13 required-section list. **SP-17 is the sole author**; SP-18 links to it and asserts nothing about its body beyond the link resolving. The price of owning it is that SP-17's spec (§12 below) writes the **union** of both plans' required `##` sections, so nothing SP-18's documentation tests expected to find is lost. | **Design sections:** §7.5, §9 (G9.2 row), §12 (all plugin-actionable hardening) | **Gaps closed:** G9.2

---

## Mission

`Qompack.md` §9 records gap **G9.2 — "The community keeps rebuilding this layer by hand — plan/context/tasks three-file patterns, MemoryForge-style hook systems, PreCompact state writers"** — and the matrix closes it with exactly five words: **"This plugin *is* the layer, packaged."** Every other subplan built the layer. SP-17 does the packaging, and packaging is the entire closure of the gap: an unpackaged, uninstallable, unverified Go module is not a replacement for a hand-rolled three-file pattern, it is one more thing to hand-roll. This slice is the difference between a working system and a product a stranger can install in thirty seconds and trust with their source code.

Three bodies of work, all of them verification-heavy rather than feature-heavy. **First, the bundle**: goreleaser cross-compiling one static `CGO_ENABLED=0` binary to the six targets of 00-ARCHITECTURE §2.6 (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64), a deterministic plugin-bundle assembler that lays out `plugin/.claude-plugin/plugin.json`, `plugin/hooks/hooks.json`, `plugin/.mcp.json`, the seven `plugin/commands/*.md`, and `plugin/bin/` per 00-ARCHITECTURE §3.4; per-platform archives where `bin/qompack` *is* the native binary and pays no launcher cost, plus a universal archive carrying all six binaries behind a launcher shim; SHA-256 checksums and GitHub build-provenance attestation; and version stamping that ties the binary, `internal/core.Version`, the tag and the CHANGELOG heading into one number that CI refuses to let drift.

**Second, the audits.** Cross-platform validation as a real matrix rather than an assumption: paths past the Windows 260-character limit, paths with spaces and non-ASCII characters, `Foo.ts`/`foo.ts` collisions on case-insensitive filesystems, CRLF content, read-only files, eight processes racing to start one daemon, Unix-socket and named-pipe permission behaviour, and Microsoft Defender's real-time filter driver sitting between us and every `NtCreateFile`. A security audit that *verifies* rather than re-implements: 00-ARCHITECTURE §13 invariant 7 says **"No network. No telemetry. No writes outside `.qompack/`"**, so SP-17 proves it three ways — an import-graph assertion over `go list -deps`, a runtime socket-inventory assertion, and a filesystem write-set diff over a full session — and separately proves that the redaction SP-06 already applies at `store.Put`/`PutBytes` actually keeps secrets out of `objects/`, `index/`, `logs/`, `spool/` and `checkpoints/`. An error-handling audit that fault-injects nine failure modes against all six hook subcommands and asserts the §2.3 rule **"Hook subcommands must always `exit 0`"** holds in all fifty-four combinations, that every MCP handler is panic-isolated, and that no unbounded allocation is reachable from untrusted input.

**Third, hardening and release.** Every row of the 00-ARCHITECTURE §12.3 degradation table gets a real failure driven against it and a documented recovery; the two operator commands the architecture names in §2.3 but no subplan has built — `qompack fsck` (checkpoint MANIFEST re-hash, object quarantine repair, JSONL tail repair, bloom rebuild, stale-lock reclamation) and `qompack doctor` (sixteen environment checks with a remedy line each) — are implemented in `internal/cli`; and the tag-to-artifact pipeline lands with changelog generation, an immutable-tag policy and a written rollback procedure.

**What exists in the repo when you start.** A `develop` that has passed verification V5: the full `internal/` tree of 00-ARCHITECTURE §3.1 with every package real, `plugin/` carrying committed manifest files generated from `internal/pluginmanifest`, `.github/workflows/ci.yml` with **ten** jobs — `verify`, `lint-windows`, `test`, `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security` and `docs` — `tools/devtool` with the §2.6 task list, `test/e2e`, `test/bench/hotpath` and `test/replay`, and `testdata/` with the 24-session synthetic corpus. `plugin/bin/` is gitignored and empty. `CHANGELOG.md` does not exist.

**Two start-state facts this slice's design turns on, stated exactly.**

1. **`.github/workflows/release.yml` is not a placeholder and does not echo anything.** It is already a fully wired tag-triggered job: `on: push: tags: ['v*']`; `permissions: contents/id-token/attestations: write`; `env: GOTOOLCHAIN: local, CGO_ENABLED: '0'`; a single `release` job on `ubuntu-latest` that checks out with `fetch-depth: 0`, runs `actions/setup-go@v5` pinned to Go `1.26.6` with `cache: true`, gates on `go run ./tools/devtool ci-local`, and then runs `goreleaser/goreleaser-action@v6` with `args: release --clean` and `GITHUB_TOKEN`. **Every one of those pieces of wiring is load-bearing and must be preserved**: the `fetch-depth: 0` checkout is what makes `git branch --contains` answerable in `guard`, the pinned Go version is what keeps the release build on the same toolchain CI proved, the `ci-local` gate is the cheap pre-flight, and the goreleaser action plus its `GITHUB_TOKEN` are the publish mechanism. SP-17 **extends** that file into the multi-job pipeline of §12 — it does not start from a blank file, and the §12 sketch is a description of the finished file, not of a replacement for something worthless. Likewise `.goreleaser.yaml` is a working six-target skeleton that already stamps `-X github.com/qompack/qompack/internal/core.Version={{ .Version }}`; §1 below extends it and keeps that flag.
2. **The `fsck`/`doctor` stub exits NON-zero, not 0.** `internal/cli/commands.go` registers both in the `notImplemented` table, whose `Run` prints `qompack <name>: not implemented in this build` to **stderr** and returns `fmt.Errorf("%w: %s", errAlreadyReported, core.ErrNotImplemented)`; `internal/cli/dispatch.go` maps a non-hook command error to `ExitError`. So a non-hook subcommand exiting non-zero is already the status quo in this tree, and §7's exit-policy argument below rests on the §2.3 prose rule ("**Hook** subcommands must always `exit 0`") rather than on a claim that nothing but `self-test` has ever exited non-zero. `qompack version` also already exists and prints `core.Version`; §6 below extends it with `BuildInfo` and `--json` rather than introducing it.

**What exists when you finish.** A tag `v1.0.0` on `main` produces, reproducibly: six binary archives, six per-platform plugin bundles, one universal plugin bundle, `checksums.txt`, and a provenance attestation — every one of which has been installed and exercised end to end on ubuntu-latest, macos-latest and windows-latest by CI, with all seven `hooks.json` entries fired through the real `${CLAUDE_PLUGIN_ROOT}` command strings (six distinct hook subcommands; `observe stop` appears twice), the MCP server handshaked and its eight tools listed, and all seven slash commands run exactly as their `commands/*.md` frontmatter invokes them. `qompack fsck` and `qompack doctor` are real. `docs/security.md`, `docs/release.md` and `docs/install.md` are written. Four new CI jobs — `package-gate`, `platform-matrix`, `fault-gate`, `install-gate` — are required checks, taking `ci.yml` from ten jobs to **fourteen**. §6.3 tier 3 has a caller for the first time: `test/replay --live` plus the `SetLiveRunner` wiring 00-ARCHITECTURE §5.18 assigns to this slice, exercised once in the pre-release run and never by a workflow. And the answer to "is it safe to point this at my source tree" is a test result rather than a promise.

---

## Design context (verbatim from Qompack.md)

### §7.5 — Plugin manifest sketch (the logical manifest this slice ships)

```json
{
  "name": "qompack",
  "version": "0.1.0",
  "description": "Cache-aware, retrieval-backed context compaction",
  "hooks": {
    "PostToolUse":       [{ "command": "qompack observe tool" }],
    "UserPromptSubmit":  [{ "command": "qompack observe prompt" }],
    "PreCompact":        [{ "command": "qompack checkpoint", "timeout": 20 }],
    "SessionStart":      [{ "command": "qompack session-start" }],
    "SessionEnd":        [{ "command": "qompack flush" }]
  },
  "mcpServers": {
    "qompack": { "command": "qompack", "args": ["mcp"] }
  },
  "commands": [
    "status", "recall", "pin", "checkpoint", "why", "dropped", "eval"
  ]
}
```

### §9 — the G9.2 row and the closure statement

| Gap | Closed by | Residual |
|---|---|---|
| G9.2 hand-rebuilt layer | This plugin *is* the layer, packaged | — |

> **Not closeable from a plugin:** G7.2 (cheap summarizer model) and the underlying bugs behind G1.4 and G7.6. Those require upstream changes and should be filed as issues, not worked around.

### §9 — the two adjacent rows this slice's audits verify rather than implement

| Gap | Closed by | Residual |
|---|---|---|
| G9.1 not durable | L4 immutable versioned checkpoints | — |
| G9.3 undocumented contracts | §12 contract monitor with fail-loud detection | Risk remains, but becomes visible |

### §12 — risk register (every row, verbatim; the plugin-actionable ones are this slice's hardening list)

| Risk | Severity | Mitigation |
|---|---|---|
| **Undocumented hook contracts change** (G9.3): `SessionStart` `source=compact`, `additionalContext` reaching context, `PreCompact` timing | High | Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently. |
| `PreCompact` timeout too short to write a checkpoint | Medium | Write incrementally on the scheduler's cadence so `PreCompact` only finalizes. Never depend on doing all the work in the hook. |
| Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |
| Hook latency on the hot path | Medium | Async queue-and-drain fallback; hard p99 budget |
| Rehydration crowds out working context | Medium | Hard budget cap; pointer-first design; measured in harness |
| Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |
| Plugin and Claude Code compaction fight each other | Medium | Soft floor well below the auto threshold; plugin acts first by design |
| Cache multipliers change | Low | Read `r` and `w` from config, never hardcode |
| **Stale negative knowledge blocks a now-viable approach** | High | Evidence-linked eliminations with `depends_on` hashes; Bloom rebuilt from active records on dependency change (§8.3). This risk is why the filter is a cache, never the source of truth. |
| Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |
| Summarizer ignores the incremental-span instruction | Low | `custom_instructions` is advisory; the checkpoint remains authoritative and the rehydrator never depends on summary compliance (§8.5) |

### §12 — What this plugin cannot do (verbatim; `docs/security.md` and `qompack doctor` must not contradict a single line)

> State these plainly rather than discovering them in month three:
>
> - **Cannot use a cheaper model for summarization** (G7.2). Upstream constraint.
> - **Cannot prevent a compaction** — only compact earlier and better.
> - **Cannot place or move `cache_control` breakpoints.** Claude Code manages its own cache markers, so the breakpoint-placement analysis in §5.6 is measurement-and-port material, not a plugin feature.
> - **Cannot access attention weights**, so H2O-style eviction stays out of reach; only perplexity proxies are available.
> - **Cannot force the `cache_edits` path** — where it is gated, the prefix constraint binds fully.
> - **Cannot change PTL retry, the circuit breaker, or the blocking-limit cliff.** It can only keep sessions away from those regions.
> - **Cannot modify the message array directly.** Everything flows through `additionalContext`.
> - **Cannot guarantee the summarizer honours focus instructions** — span narrowing (§8.5) and snippet prohibition (G3.4) are advisory; the durable checkpoint is the backstop for both.

### §7.4 — Directory layout and the append-only invariant (what a clean uninstall must leave, and what `fsck` walks)

```
.qompack/                       # gitignored, project-root
├── config.json
├── objects/                       # content-addressed, zstd-compressed
│   └── ab/cd/abcdef…              # sha256, 2-level fanout
├── index/
│   ├── tool_use.jsonl             # tool_use_id → root hash, ts, tool, args
│   ├── files.json                 # path → [(ts, root_hash)] version history
│   └── segments.jsonl             # changepoint-delimited segment log
├── sketches/
│   ├── tried.bloom                # negative knowledge — NEVER regenerated
│   ├── touch.cms                  # file-touch frequency
│   └── explore.hll                # exploration cardinality
├── dag/
│   └── deps.jsonl                 # dependence edges for slicing
├── grammar/
│   └── actions.seq                # Sequitur state over the action log
├── checkpoints/
│   ├── 0001.json                  # immutable, importance-ordered
│   └── 0002.json
├── pins/
│   └── invariants.json            # user- and agent-pinned, never summarized
└── eval/
    ├── replay/                    # counterfactual fork logs
    └── opt/                       # Belady keep-sets
```

> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### §8.1 — the performance budget the launcher shim must not eat

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

### §8.2 — garbage collection and retention (what `fsck --repair` may and may not delete)

> **Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.

### §8.3 — the bloom-is-a-cache rule that `fsck --repair` implements

> 1. **The structured `eliminated[]` records are the source of truth; the Bloom filter is only a cache over them.** This was implicitly true; it is now load-bearing.
> 3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.

### §11.3 — guardrails (quoted verbatim in the Exit criteria section too)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §11.4 — Watch for (the bloom thresholds `doctor` renders against)

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### §10 Phase 1 exit criterion (`doctor` reports the dedup ratio against this number)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

### Appendix C — the config keys `fsck` and `doctor` read (verbatim fragment; `…` marks an elision, every quoted key/value is byte-exact)

```jsonc
  "store": {
    "chunk": { "min": 1024, "target": 4096, "max": 16384 },
    "compression": "zstd",
    "retention": { "days": 30, "sessions": 10 },
    // "canonicalize": { … }   — elided, not read by fsck or doctor
  },
  …
  "sketches": {
    "bloom": { "capacity": 10000, "fpRate": 0.01 },
    "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
    "hll":   { "registers": 2048 }
  },
```

### Appendix A — Bloom filter sizing (the numbers `doctor` prints beside the fill ratio)

```
m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7
```

---

## Normative extracts from `plans/00-ARCHITECTURE.md`

These are *how* decisions, quoted so this document is self-contained.

**§2.3 — runtime shape and the exit-code rule.**

> ```
> qompack (one binary, ~15 MB, static, no cgo)
> ├── qompack observe tool        ← hook, thin client   (hot path)
> ├── qompack observe prompt      ← hook, thin client   (hot path)
> ├── qompack observe stop        ← hook, thin client   (hot path)
> ├── qompack session-start       ← hook, thin client   (warm path; starts daemon)
> ├── qompack checkpoint          ← hook, thin client   (PreCompact, ≤20 s)
> ├── qompack flush               ← hook, thin client   (SessionEnd)
> ├── qompack mcp                 ← long-lived stdio MCP server
> ├── qompack daemon              ← resident per-project daemon
> ├── qompack status|recall|pin|why|dropped|eval|config|fsck|doctor|bench
> └── qompack self-test           ← contract monitor, exits non-zero (the ONLY one that may)
> ```
>
> **Hook subcommands must always `exit 0`.** A non-zero hook exit surfaces noise to the user and, for some hooks, can block the turn. Every error path logs and exits 0. This is a hard rule and CI enforces it with a test that fault-injects every dependency of every hook subcommand.

**§2.4 — latency budgets.**

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-A** | `hook_controlled` — client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (§11.3 L0) | CI on linux/macos/windows, 5 000 iterations |
| **B-B** | `l0_ingest` — daemon read → WAL append returned | p99 < 2 ms | daemon self-metrics + CI |
| **B-C** | `l0_process` — WAL → fully chunked, stored, DAG/sketches updated (async) | p99 < 50 ms | soft |
| **B-D** | `hook_wall` — includes host process creation | reported, not gated | — |
| **B-E** | `checkpoint_finalize` — `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | CI |
| **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

**§2.5 — the closed dependency list.** Runtime: `github.com/klauspost/compress/zstd`, `github.com/Microsoft/go-winio` (build-tagged `windows`). Test-only: `testify/require`, `go-cmp`, `pgregory.net/rapid`, never imported by non-`_test.go` files.

**§3.4 — the physical bundle** (`plugin/.claude-plugin/plugin.json`, `plugin/hooks/hooks.json` with the seven `${CLAUDE_PLUGIN_ROOT}/bin/qompack …` command strings and their `timeout` values, `plugin/.mcp.json`, `plugin/commands/*.md`).

**§8 — the `security` CI job this slice extends.**

> `security` | ubuntu | `govulncheck ./...` · `gosec` · secret scan · assert zero non-test imports of `net/http`, `net/url`, `crypto/tls` anywhere; `net` only in `internal/ipc` (Unix sockets — and only `net.Dial`/`net.Listen` on `unix`, never `tcp`); `os/exec` only in `internal/daemon` (detached self-spawn), `internal/cli` and `tools/`

**§9 — tags.** `v0.<wave>.<n>` on `develop` after each verification; `v<semver>` on `main` at release (SP-17).

**§12.3 — everything else fails toward "do nothing"** (the nine rows this slice hardens):

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

**§13 invariant 7.** "No network. No telemetry. No writes outside `.qompack/`" (plus `~/.qompack/` for the global layer). CI asserts the import graph and a runtime test asserts the write set.

---

## Out of scope

Each item names the sibling that owns it. SP-17 **verifies** several of these; verifying is not owning, and a verification failure is a bug report against the owner's package that SP-17 fixes surgically in its hardening commit without redesigning anything.

| Out of scope for SP-17 | Owner |
|---|---|
| The `.gitignore`, repo init, `go.mod`, `golangci-lint` config, the `nomagic` pass, the import-graph *layer* check, and `ci.yml`'s original nine jobs | SP-01 |
| `internal/pluginmanifest`'s typed manifest source and `devtool plugin-validate`'s diff behaviour (SP-17 adds only the four assertions enumerated in Implementation spec §5a, and no symbol at all to that package) | SP-01 |
| `internal/core/version.go` — the single stamped version variable. SP-17 **stamps** it at release and **reads** it everywhere; it does not move it, shadow it or add a second one | SP-01 |
| `internal/testutil` fixture design and the `test/e2e` harness scaffolding (SP-17 adds cases, not scaffolding) | SP-01 |
| The replay harness, Belady OPT, divergence metrics, the 24-session corpus, and the `replay-gate` 2% rule | SP-02 |
| Sketch serialization formats, CRC headers, `ResizeTarget`, `RebuildBloom` mechanics | SP-03 |
| FastCDC parameters, canonicalizer rules, `symbols` extraction | SP-04 |
| The daemon, IPC transport, spool/WAL, the hot-path B-A budget histograms, the sync→spool submode transition, and `internal/contract`'s assertion set | SP-05 |
| `internal/redact`'s rules and the choke point at `store.Put`/`PutBytes` — SP-17 asserts secrets never land on disk; it does **not** add or move a redaction rule | SP-06 |
| Object layout, GC mark-and-sweep, `MarkEncoded`, exact token accounting | SP-06 |
| The dependence DAG and slicing | SP-07 |
| Observer semantics, tombstones, supersession | SP-08 |
| The elimination ledger, staleness flip, three-way `already_tried` | SP-09 |
| The checkpoint JSON schema, `checkpoint/migrate.go`, focus instructions, pins — SP-17 quarantines an unreadable or newer-versioned checkpoint; it does **not** define schema v2 | SP-10 |
| The rehydrator, drop report, rules/skills restoration | SP-11 |
| The scheduler, BOCD, p-selection, idle model, frontier advancement | SP-12 |
| The MCP server, the eight tool handlers, span resolution, the Promoter | SP-13 |
| `/qompack:status` rendering and the other six slash commands — `doctor` is a *diagnostic* surface for the environment, `status` is the *observability* surface for the session; they must not duplicate each other and SP-17 changes no `commands` output | SP-14 |
| Δ-scoring, submodular selection, Sequitur | SP-15 |
| Warm start, per-segment blooms, ski-rental, promotion tuning | SP-16 |
| `README.md`, `docs/user-guide.md`, `docs/troubleshooting.md`, `docs/config-reference.md`, `docs/cannot-do.md`, `docs/upstream-issues.md`, `docs/uat.md`, and the issue templates | SP-18 |
| Publishing to any plugin marketplace or registry, homebrew/scoop/winget packaging, and code signing / notarization (no signing identity exists; `docs/release.md` records this as a deliberate, documented omission with the exact steps to add it later) | nobody — explicitly deferred, recorded in `docs/release.md` §"Deferred" |

---

## Interface contract

### Consumes (exact signatures, from 00-ARCHITECTURE §4–§5)

```go
// internal/core (§4)
type Hash [32]byte
func (h Hash) String() string
func HashBytes(domain string, b []byte) Hash
type SessionID string; type CheckpointSeq int; type Tokens int; type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
var ErrNotFound, ErrAppendOnly, ErrDegraded, ErrContract error

// internal/paths (§3.3)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte) error
func AppendOnly(p string) (*os.File, error)
func CreateNew(p string) (*os.File, error)

// internal/config (§5.1)
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
func (c Config) Validate() []Violation

// internal/logging, internal/obs (§5.2)
func New(dir string, lvl Level) (Logger, io.Closer, error)
type Logger interface{ …; Loud(msg string, kv ...any) }
type Registry interface{ Snapshot() Snapshot; CheckBudgets(cfg config.Config) []BudgetBreach }

// internal/store (§5.8)
func Open(root string, cfg config.Config, deps Deps) (Store, error)
type Store interface {
    PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
    GetChunk(ctx context.Context, h core.Hash) ([]byte, error)
    Has(h core.Hash) bool
    Stats(ctx context.Context) (Stats, error)
    GC(ctx context.Context, p GCPolicy) (GCReport, error)
    Flush(ctx context.Context) error
    Close() error
    Segments() SegmentLog
}
type Stats struct{ Objects int; Bytes, RawBytes int64; DedupRatio float64; ToolUses, Segments, Files int; Sketches map[string]int }

// internal/checkpoint (§5.14)
type Reader interface {
    Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
    Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
    List(ctx context.Context) ([]Ref, error)
    Verify(ctx context.Context) ([]core.CheckpointSeq, error)   // MANIFEST re-hash; fsck
}
type Ref struct{ Seq core.CheckpointSeq; Path string; SHA256 core.Hash; Bytes int64; Tokens core.Tokens; Frontier core.TurnIndex; Created core.UnixMilli }

// internal/pins (§5.14)
type Store interface{ All(ctx context.Context) ([]Invariant, error); Materialize(ctx context.Context) error }

// internal/negknow (§5.10)
type Ledger interface {
    All(ctx context.Context) ([]Record, error)
    Active(ctx context.Context, scope Scope) ([]Record, error)
    RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
    Health() Health
    Close() error
}
type Health struct{ Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool }

// internal/sketch (§5.7)
func Load(p string, s Sketch) error
func (b *Bloom) FillRatio() float64
func (b *Bloom) EstimatedFPRate() float64
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool)

// internal/dag (§5.9)
func Open(root string, cfg config.Config, log logging.Logger) (Graph, error)
type Graph interface{ Stats() GraphStats; Flush(ctx context.Context) error }

// internal/ipc (§5.4)
func Resolve(projectRoot string) (Addr, error)
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client
type Client interface{ Send(ctx context.Context, req Request, deadline time.Duration) (Response, error); Close() error }
type Op string   // "status" is the op fsck/doctor use for a live-daemon probe

// internal/contract (§5.19)
type Monitor interface{ RunAll(ctx context.Context, e Env) ([]Result, Mode); Mode() Mode; Report() []Result }
type Mode uint8  // ModeFull, ModeDegradedPassive, ModeOff

// internal/mcp (§5.16) — panic_test builds a server in-process, so all three methods are used
func NewServer(name, version string, log logging.Logger) Server
func RegisterAll(s Server, d ToolDeps) error
type Server interface {
    Register(t Tool) error
    Serve(ctx context.Context, in io.Reader, out io.Writer) error
    Tools() []Tool
}

// internal/commands (§5.17)
func All(d Deps) []Command
type Command interface{ Name() string; Run(ctx context.Context, args []string, out io.Writer) error }

// internal/core (§3.4, version.go) — the SINGLE stamped version source, consumed unchanged.
// Overridden at link time by -X github.com/qompack/qompack/internal/core.Version=<tag>.
var Version = "0.1.0"

// internal/pluginmanifest (§3.4) — SP-01's generator, consumed unchanged.
// Default builds the manifest for a version; Files returns bundle-relative path → generated bytes
// for ".claude-plugin/plugin.json", "hooks/hooks.json", ".mcp.json", "commands/<name>.md".
func Default(version string) Manifest
func (m Manifest) Files() (map[string][]byte, error)
func Write(dir string, m Manifest) error
func Validate(dir string, m Manifest) []Diff
```

> **Adaptation note, resolved:** the accessor is a **method on `Manifest`**, not a package function — the call is `pluginmanifest.Default(core.Version).Files()`, exactly as `tools/devtool/pluginvalidate.go` and `internal/daemon/handlers.go` already make it. Use it verbatim and do **not** rename it (Rule W-3). **SP-17 introduces no new exported symbol in `internal/pluginmanifest` at all.**
>
> **Version source, resolved — there is exactly one, and it already exists.** `internal/core.Version` is it. Its own doc comment says so ("the single source the plugin manifest, the MCP server handshake and `qompack version` all read"), and the tree is already wired to it end to end: `internal/cli/commands.go` prints it for `qompack version`, `internal/cli/dispatch.go` puts it in the help banner, `internal/daemon/lock.go` stamps it into the lock file, `internal/daemon/handlers.go` returns it in the status payload and passes it to `pluginmanifest.Default`, `tools/devtool/pluginvalidate.go` defaults `--version` to it, `tools/devtool/util.go` builds `-X …/internal/core.Version=` for every dev build, and the committed `.goreleaser.yaml` already stamps it. An earlier draft of this plan introduced a second source, `pluginmanifest.DefaultVersion`, and stamped only that one; that would have shipped a `v1.0.0` binary whose `qompack version`, daemon lock, daemon status payload and MCP handshake all still said `0.1.0` while every version assertion passed — the exact drift those assertions exist to prevent. **`pluginmanifest.DefaultVersion` does not exist and must not be created.**

### Produces (what later readers and CI rely on)

```go
// ── internal/cli (new files; cli is a composition root per §3.2 and may import anything) ──
// Build identity. Version is NOT declared here: it is internal/core.Version, already stamped.
// Only the two build-provenance fields are new, and only they get their own -X flags.
var (
    buildCommit = "unknown"
    buildDate   = "unknown"
)
type BuildInfo struct {
    Version   string `json:"version"`                  // internal/core.Version
    Commit    string `json:"commit"`                   // buildCommit
    Date      string `json:"date"`                     // buildDate
    Go        string `json:"go"`
    OS        string `json:"os"`
    Arch      string `json:"arch"`
    Manifest  string `json:"plugin_manifest_version"`  // also core.Version, by construction
}
func Version() BuildInfo
func RunVersion(args []string, out io.Writer) int   // `qompack version [--json]`; always returns 0

// ── qompack fsck ──────────────────────────────────────────────────────────
// SupportedCheckpointVersions lives HERE, in internal/cli, not in internal/checkpoint:
// defining the schema is SP-10's (out of scope, below), but knowing which schemas *this
// build* can read is a property of this binary's operator tooling. A checkpoint whose
// "version" is not in this list is quarantined by fsck check 5 and FAILed by doctor's
// `checkpoints` check with the "upgrade qompack" remedy.
var SupportedCheckpointVersions = []int{1}

type FsckSeverity uint8
const (
    FsckOK FsckSeverity = iota
    FsckInfo
    FsckWarn
    FsckError
)
type Finding struct {
    Check    string       `json:"check"`     // "objects" "checkpoints" "jsonl-tails" …
    Severity FsckSeverity `json:"severity"`
    Path     string       `json:"path,omitempty"`
    Detail   string       `json:"detail"`
    Repair   string       `json:"repair,omitempty"`   // what --repair would do / did
    Repaired bool         `json:"repaired"`
}
type FsckOptions struct {
    ProjectRoot string
    Repair      bool
    Deadline    time.Duration     // default 60s; honoured per check, resumable
    JSON        bool
    Strict      bool              // only --strict may produce a non-zero exit (§2.3)
    Clock       core.Clock
}
type FsckReport struct {
    Version   string    `json:"version"`
    Root      string    `json:"root"`
    StartedAt string    `json:"started_at"`
    Duration  string    `json:"duration"`
    Checks    []string  `json:"checks"`
    Findings  []Finding `json:"findings"`
    Counts    map[string]int `json:"counts"`   // severity name → count
    Truncated bool      `json:"truncated"`     // deadline hit; re-run to continue
    OK        bool      `json:"ok"`
}
func RunFsck(ctx context.Context, o FsckOptions, out io.Writer) (FsckReport, int)

// ── qompack doctor ────────────────────────────────────────────────────────
type CheckStatus uint8
const (
    CheckPass CheckStatus = iota
    CheckInfo
    CheckWarn
    CheckFail
)
type Check struct {
    ID       string      `json:"id"`        // "binary" "plugin_root" "project_root" …
    Title    string      `json:"title"`
    Status   CheckStatus `json:"status"`
    Observed string      `json:"observed"`
    Expected string      `json:"expected"`
    Remedy   string      `json:"remedy,omitempty"`
    Duration string      `json:"duration"`
}
type DoctorOptions struct {
    ProjectRoot string
    JSON        bool
    Strict      bool
    SkipMCP     bool          // CI uses this when a child process would deadlock the runner
    Clock       core.Clock
}
type DoctorReport struct {
    Version string  `json:"version"`
    Build   BuildInfo `json:"build"`
    Checks  []Check `json:"checks"`
    Counts  map[string]int `json:"counts"`
    OK      bool    `json:"ok"`
}
func RunDoctor(ctx context.Context, o DoctorOptions, out io.Writer) (DoctorReport, int)
```

**Non-Go artifacts produced (contracts in their own right):**

| Artifact | Contract |
|---|---|
| `packaging/launcher/qompack.sh` | POSIX `sh`; resolves `uname -s`/`uname -m` → `qompack-<os>-<arch>`; `exec`s it; exits 0 on every launcher-internal failure; propagates the child's exit code otherwise |
| `packaging/launcher/qompack.cmd` | Windows batch; `PROCESSOR_ARCHITECTURE`/`PROCESSOR_ARCHITEW6432` → `qompack-windows-<arch>.exe`; `exit /b %ERRORLEVEL%`; `exit /b 0` when no binary is found |
| `packaging/launcher/qompack.ps1` | PowerShell equivalent; `$env:PROCESSOR_ARCHITECTURE`; `exit $LASTEXITCODE` |
| `dist/plugin-<os>-<arch>/` and its archive | Bundle with `bin/qompack` (or `bin/qompack.exe`) as the **native binary** — zero launcher cost; recommended install |
| `dist/plugin-universal/` and its archive | Bundle with six binaries plus the three launchers; `bin/qompack` is the sh launcher |
| `dist/plugin-<os>-<arch>/bin/SHA256SUMS` and `dist/plugin-universal/bin/SHA256SUMS` | `<64 lowercase hex><two spaces><filename>\n`, LF endings, sorted bytewise by filename, no header |
| `dist/qompack-plugin-<version>-<flavor>.sha256` | Same one-line format, naming the archive |
| `devtool` tasks | `package`, `verify-version`, `set-version`, `changelog`, `release-guard`, `platform-matrix`, `security-audit`, `fault-inject`, `install-check`, `uninstall-check` — additive to the §2.6 list, none renamed |

---

## Implementation spec

### 0. Ground rules for this slice

1. Work happens on `feat/sp17-packaging-hardening-and-release`, cut from the post-V5 `develop`.
2. **No §5 interface is changed.** No amendment commit to `00-ARCHITECTURE.md` is required or permitted by this subplan. Every new symbol lands in `internal/cli` (a composition root that "may import anything; nothing may import them", §3.2) or in non-Go directories (`packaging/`, `tools/devtool/`, `test/`, `.github/`, `docs/`).
   **New top-level directories are additive, not an amendment.** This slice creates `packaging/` and `test/{install,platform,security,fault}/`. 00-ARCHITECTURE §3.1's tree fixes *where owned things live*; it does not forbid sibling test directories, and §6.2 already assigns "the full cross-platform matrix" to SP-17. Adding them therefore requires no `arch/` branch. Nothing under `internal/` gains a package.
   **The single sanctioned edit to another subplan's non-composition-root package that is not a proven-defect fix** is the four-line telemetry hardwire in `internal/config/load.go` (commit 3), which is mandated by 00-ARCHITECTURE §11.5's `"telemetry": { "enabled": false }` comment *"hardwired off; key exists to say so"*. It is listed in that commit's body as `Hardening: internal/config (SP-01) — telemetry hardwire per §11.5`.
3. `Qompack.md` is never modified.
4. Hardening fixes in packages owned by other subplans are permitted **only** when a test in this slice proves a defect, must be the smallest change that makes the test pass, and must be listed in the commit body under `Hardening:` with the owning subplan named.
5. Every new test must run green on ubuntu, macos and windows or carry an explicit, justified `t.Skip` naming the platform and the reason. "Flaky on Windows" is not a reason.

### 1. `.goreleaser.yaml` — six-target cross-compilation

Extends SP-01's working skeleton; the `-X …/internal/core.Version={{ .Version }}` flag it already carries stays exactly as it is. Pin the action to `version: '~> v2.6'` in the workflow (SP-01 pinned a bare `~> v2`): `archives.formats` — the plural list form — is the non-deprecated spelling from v2.6 onward, and `TestGoreleaserConfigValid` asserts `goreleaser check` reports **zero deprecation notices**, which the singular `format` would trip.

```yaml
version: 2
project_name: qompack

before:
  hooks:
    - go mod download
    # Gated on IsSnapshot. Under --snapshot goreleaser fills .Version from snapshot.version_template
    # below (e.g. 0.1.1-dev+abc1234), which by construction matches neither core.Version nor the
    # CHANGELOG heading nor plugin.json — so an ungated hook would exit 1 and abort every dry run,
    # including DoD item 13's. `test … || cmd` runs the verifier only on a real tag.
    - sh -c 'test "{{ .IsSnapshot }}" = true || go run ./tools/devtool verify-version --tag {{ .Version }}'

builds:
  - id: qompack
    main: ./cmd/qompack
    binary: qompack
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
      # ONE version flag, and it is the one SP-01 already wrote. core.Version is what
      # `qompack version`, the help banner, the daemon lock, the daemon status payload, the MCP
      # handshake and pluginmanifest.Default() all read; a second stamped variable anywhere would
      # be a second truth. Do not add one.
      - -X github.com/qompack/qompack/internal/core.Version={{ .Version }}
      - -X github.com/qompack/qompack/internal/cli.buildCommit={{ .FullCommit }}
      - -X github.com/qompack/qompack/internal/cli.buildDate={{ .CommitDate }}
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    mod_timestamp: '{{ .CommitTimestamp }}'

archives:
  - id: binaries
    ids: [qompack]
    name_template: 'qompack_{{ .Version }}_{{ .Os }}_{{ .Arch }}'
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files: [LICENSE, CHANGELOG.md]

checksum:
  name_template: 'checksums.txt'
  algorithm: sha256

snapshot:
  version_template: '{{ incpatch .Version }}-dev+{{ .ShortCommit }}'

changelog:
  disable: true          # CHANGELOG.md is generated by `devtool changelog`, not by goreleaser

release:
  draft: true            # the workflow publishes only after install-gate passes on all 3 OSes
  prerelease: auto
  # No `extra_files`. goreleaser runs FIRST with `--clean`, which deletes dist/ before it
  # builds — any plugin bundle written there beforehand would be erased, and any glob
  # evaluated at goreleaser time would match nothing. The plugin bundles are therefore
  # attached afterwards by `gh release upload` in the same job (§12).
```

`ldflags` note: `-s -w` strips DWARF and the symbol table; the binary-size budget below assumes it. `mod_timestamp` pinned to the commit timestamp is what makes two builds of the same commit byte-identical.

`archives.files` note: `README.md` is deliberately **absent**. SP-18 has not merged when SP-17 lands, so `README.md` does not exist on this branch and goreleaser fails on a `files` entry that matches nothing. Adding `README.md` to this list is a one-line change SP-18 makes in its own commit; `docs/release.md` records it under "Deferred to SP-18" so it is not forgotten.

**Binary-size budget** (from §2.2 "a single 12–20 MB static binary"): `linux/amd64` ≤ **20 MiB**, every other target ≤ **24 MiB**, asserted in `package-gate`. A breach is a hard failure; the remedy is a dependency review, not a raised limit.

### 2. `packaging/launcher/qompack.sh`

Mode 0755 in every bundle. Written to be `dash`-safe (no bashisms).

```sh
#!/bin/sh
# qompack launcher (universal bundle). Selects the platform binary in this directory.
# HOOK SAFETY: every launcher-internal failure exits 0 (00-ARCHITECTURE §2.3). The child's
# exit code is propagated verbatim via exec, so `qompack self-test` can still exit non-zero.
set -u
d=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd 2>/dev/null) || {
  echo "qompack: cannot resolve launcher directory" >&2; exit 0; }
os=$(uname -s 2>/dev/null || echo unknown)
arch=$(uname -m 2>/dev/null || echo unknown)
case "$os" in
  Linux)  o=linux ;;
  Darwin) o=darwin ;;
  *) echo "qompack: unsupported OS '$os' — install the per-platform bundle" >&2; exit 0 ;;
esac
case "$arch" in
  x86_64|amd64)  a=amd64 ;;
  arm64|aarch64) a=arm64 ;;
  *) echo "qompack: unsupported arch '$arch' — install the per-platform bundle" >&2; exit 0 ;;
esac
b="$d/qompack-$o-$a"
[ -x "$b" ] || { echo "qompack: missing or non-executable binary $b" >&2; exit 0; }
exec "$b" "$@"
```

Resolution table, asserted by test:

| `uname -s` | `uname -m` | binary |
|---|---|---|
| `Linux` | `x86_64` | `qompack-linux-amd64` |
| `Linux` | `aarch64` | `qompack-linux-arm64` |
| `Darwin` | `x86_64` | `qompack-darwin-amd64` |
| `Darwin` | `arm64` | `qompack-darwin-arm64` |
| `FreeBSD` | any | none → stderr line, exit 0 |
| `Linux` | `riscv64` | none → stderr line, exit 0 |

### 3. `packaging/launcher/qompack.cmd`

```bat
@echo off
setlocal EnableExtensions
set "QP_DIR=%~dp0"
set "QP_ARCH=amd64"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "QP_ARCH=arm64"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "QP_ARCH=arm64"
set "QP_BIN=%QP_DIR%qompack-windows-%QP_ARCH%.exe"
if not exist "%QP_BIN%" (
  echo qompack: missing binary "%QP_BIN%" 1>&2
  exit /b 0
)
"%QP_BIN%" %*
exit /b %ERRORLEVEL%
```

**No self-promotion, deliberately.** An earlier design copied the arch-appropriate binary to `bin\qompack.exe` on first run so `PATHEXT` would resolve `.EXE` before `.CMD` and remove the `cmd.exe` spawn forever. It is rejected: it writes outside `.qompack/` and `~/.qompack/`, violating §13 invariant 7 for a latency win we get for free by recommending the per-platform bundle, whose `bin\qompack.exe` *is* the native binary. `docs/install.md` states this in one sentence and `qompack doctor` emits an INFO check when it detects it is running through the universal launcher on Windows.

### 4. `packaging/launcher/qompack.ps1`

```powershell
# qompack launcher (universal bundle), PowerShell variant.
$ErrorActionPreference = 'Continue'
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
$arch = 'amd64'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $arch = 'arm64' }
$bin = Join-Path $dir "qompack-windows-$arch.exe"
if (-not (Test-Path -LiteralPath $bin)) {
  [Console]::Error.WriteLine("qompack: missing binary $bin")
  exit 0
}
& $bin @args
exit $LASTEXITCODE
```

All three launchers are shipped in the universal bundle because Claude Code resolves a hook `command` string through the platform shell and both `cmd.exe` and PowerShell apply `PATHEXT`/`.ps1` resolution to an extensionless path-qualified command; shipping all three removes the need to know which shell is in play.

### 5. `tools/devtool/package.go` — the bundle assembler

`go run ./tools/devtool package [--version <semver>] [--targets all|<os>/<arch>,…] [--out dist] [--universal-only] [--skip-build]`

Algorithm:

1. Resolve version: `--version` if given, else `core.Version`. Reject anything not matching `^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`. The **build-metadata group is mandatory**, not cosmetic: `snapshot.version_template` in §1 emits `<x.y.z>-dev+<sha7>`, so a regex without `(\+…)?` rejects the version every snapshot dry run produces and DoD item 13 becomes unreachable.
2. Build (unless `--skip-build`): for each of the six targets, `go build -trimpath -ldflags "<the same three -X flags §1 lists, plus -s -w>" -o dist/bin/qompack-<os>-<arch>[.exe] ./cmd/qompack` with `CGO_ENABLED=0`, `GOOS`, `GOARCH` set. `tools/devtool/util.go` already has `versionLdflags(version)`, which builds `-s -w -X …/internal/core.Version=<v>`; extend that one helper with the two provenance flags rather than writing a second flag builder.
3. Generate manifest files by calling `pluginmanifest.Default(version).Files()` — **never** by copying `plugin/**` from the worktree; the committed files are the diff target for `plugin-validate`, the generator is the source of truth. Assert the generated `.claude-plugin/plugin.json` has `"version": "<resolved version>"`.
4. Assemble each per-platform tree `dist/plugin-<os>-<arch>/`:
   ```
   .claude-plugin/plugin.json     0644
   hooks/hooks.json               0644
   .mcp.json                      0644
   commands/checkpoint.md         0644
   commands/dropped.md            0644
   commands/eval.md               0644
   commands/pin.md                0644
   commands/recall.md             0644
   commands/status.md             0644
   commands/why.md                0644
   bin/qompack        (or bin/qompack.exe on windows)   0755  ← the native binary
   bin/SHA256SUMS                 0644
   LICENSE                        0644
   ```
5. Assemble `dist/plugin-universal/`: the same manifest/command/LICENSE set, plus
   ```
   bin/qompack                    0755  ← packaging/launcher/qompack.sh
   bin/qompack.cmd                0755  ← packaging/launcher/qompack.cmd
   bin/qompack.ps1                0755  ← packaging/launcher/qompack.ps1
   bin/qompack-linux-amd64        0755
   bin/qompack-linux-arm64        0755
   bin/qompack-darwin-amd64       0755
   bin/qompack-darwin-arm64       0755
   bin/qompack-windows-amd64.exe  0755
   bin/qompack-windows-arm64.exe  0755
   bin/SHA256SUMS                 0644
   ```
6. Write `bin/SHA256SUMS`: for every file in `bin/` except `SHA256SUMS` itself, `fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(b), name)`, entries sorted by `sort.Strings` over the filenames, LF only.
7. Archive. `tar.gz` for every flavour; additionally `zip` for the windows flavours and for universal. **Determinism rules, all mandatory:** entries added in `sort.Strings` order of their archive paths; `ModTime` set to `time.Unix(1577836800, 0).UTC()` (2020-01-01T00:00:00Z) for every entry; `gzip.NewWriterLevel(w, gzip.BestCompression)` with `gz.ModTime` zeroed and `gz.Name` empty; zip entries written with `zip.FileHeader{Method: zip.Deflate}` and no extended timestamp extra field; uid/gid/uname/gname zeroed in tar headers; mode bits exactly as listed above.
8. Write `dist/qompack-plugin-<version>-<flavor>.sha256` for each archive.
9. Print a machine-readable manifest to stdout when `--json`: `{"version":…, "artifacts":[{"path":…,"sha256":…,"bytes":…}]}`.

Naming: `qompack-plugin-<version>-<os>-<arch>.{tar.gz,zip}` and `qompack-plugin-<version>-universal.{tar.gz,zip}`.

**Output directory and goreleaser coexistence.** `--out` defaults to `dist` (already gitignored by SP-01's `/dist/` rule, so no `.gitignore` edit is needed). In the release workflow `goreleaser release --clean` runs **before** `devtool package`, because `--clean` deletes `dist/` on entry; running it second would erase the bundles. The two write disjoint names inside `dist/` (`qompack_<v>_<os>_<arch>.*` vs `qompack-plugin-<v>-*` and `plugin-*/`), so no file is ever written twice.

### 5a. The four `plugin-validate` assertions SP-17 adds

`devtool plugin-validate` already regenerates `plugin/**` from `internal/pluginmanifest`, diffs it against the committed files, JSON-schema-validates the three manifests, and asserts all 7 commands and 8 MCP tools are present (00-ARCHITECTURE §8). SP-17 appends exactly four assertions to that task — no behaviour of the existing four changes:

| # | Assertion | Fails when |
|---|---|---|
| PV-1 | The committed `plugin/.claude-plugin/plugin.json`'s `version` equals `core.Version` | a `set-version` run edited `internal/core/version.go` but did not regenerate `plugin/**` |
| PV-2 | Every `hooks.json` `command` string starts with `${CLAUDE_PLUGIN_ROOT}/bin/qompack ` and its remainder names a subcommand that `cli` dispatch actually recognises (checked against the dispatch table, not a hard-coded list) | a subcommand is renamed without updating the manifest |
| PV-3 | The seven hook entries carry exactly the §3.4 timeouts — `PostToolUse` 5, `UserPromptSubmit` 5, `SessionStart` 15, `PreCompact` 20, `Stop` 5, `SubagentStop` 10, `SessionEnd` 20 | a timeout drifts from the architecture |
| PV-4 | `.mcp.json`'s `command` is `${CLAUDE_PLUGIN_ROOT}/bin/qompack` and `args` is exactly `["mcp"]` | the MCP entry point drifts from §3.4 |

Tested by `TestPluginValidateNewAssertions` in `test/install/bundle_test.go`: four table cases, each mutating one generated field in a temp copy of the bundle and asserting the corresponding assertion is the one that fires.

### 6. `tools/devtool/version.go` — stamping, verification, and set-version

- `verify-version --tag <v1.2.3|1.2.3>`: strips a leading `v`, compares against **`core.Version`** (the compiled-in value of `internal/core/version.go`, read by importing the package — never re-parsed out of the source file), against the first `## [x.y.z]` heading in `CHANGELOG.md`, and against `plugin/.claude-plugin/plugin.json`'s `version`. Any mismatch → exit 1 with all four values printed. This is the goreleaser `before` hook (gated on `IsSnapshot`, §1) and the release workflow's `guard` job.
- `set-version <x.y.z>`: rewrites `internal/core/version.go` (`var Version = "x.y.z"`), regenerates `plugin/**` by calling the existing `devtool plugin-validate --write` path, and inserts a dated `## [x.y.z] - YYYY-MM-DD` heading into `CHANGELOG.md` above the previous top heading, moving everything currently under `## [Unreleased]` into it and leaving an empty `## [Unreleased]`. Run by a human in the release-prep commit; never by CI. It is the **only** writer of `internal/core/version.go`, which is what keeps the three artifacts in step.

`internal/cli/version.go`:

```go
func Version() BuildInfo {
    return BuildInfo{
        Version: core.Version, Commit: buildCommit, Date: buildDate,
        Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
        // Equal to Version by construction — pluginmanifest.Default() is handed core.Version and
        // has no other source. The field stays in the JSON because it is the tripwire: if a second
        // version source is ever introduced, this is the first place the two disagree in public.
        Manifest: pluginmanifest.Default(core.Version).Plugin.Version,
    }
}
```

`qompack version` prints `qompack <version> (<commit12>, <date>) <go>/<os>/<arch>`; `--json` prints `BuildInfo` with `json.MarshalIndent(v, "", "  ")` plus a trailing newline. Always exits 0. The bare `qompack version` line that `internal/cli/commands.go` prints today (`core.Version` alone) is replaced by the richer line, and the existing `runVersion` registration is reused rather than a second `version` command being added.

### 7. `internal/cli/fsck.go` — `qompack fsck`

Flags: `--repair`, `--deadline <dur>` (default `60s`), `--json`, `--strict`, `--quarantine-dir <path>` (default `<root>/.qompack/tmp/quarantine`), `--gc-all`.

`--gc-all` is the flag SP-06 promised on its behalf and is meaningful only together with `--repair`. `internal/store`'s `GCPolicy` reads each retention axis as a tri-state — `0` inherits `config.Store.Retention`, positive overrides it, and **negative means "no window on this axis"**, which SP-06's own text names as "the only way to force collection … for tests and for `qompack fsck --gc-all` (SP-17)". So `--gc-all` sets `RetainDays: -1, RetainSessions: -1` in check 13's policy, collecting every object no live root, checkpoint, pin or elimination references, regardless of age or session recency. Without `--repair` it is a dry run like the rest of check 13. `--gc-all` without `--repair` is not an error; `--gc-all` alone simply reports the larger collectable figure, and the human report says so with the finding detail `collectable with --gc-all: <n> objects / <bytes>` so the operator sees the difference between the two windows before choosing.

Exit policy, resolved against §2.3 ("`qompack self-test` … the ONLY one that may" exit non-zero): **`fsck` exits 0 by default even when it finds errors** — the report is the product — and exits 1 only under `--strict`. CI uses `--strict`; users and `doctor` do not.

Why this does not violate §2.3: the hard rule that §2.3 states in prose is **"Hook subcommands must always `exit 0`"**, and neither `fsck` nor `doctor` is a hook subcommand — neither appears in any `hooks.json` command string (PV-2 above mechanically guarantees that, since the manifest's seven commands are `observe tool|prompt|stop`, `session-start`, `checkpoint`, `flush`), so no hook can ever inherit their exit code. The parenthetical "the ONLY one that may" in the §2.3 tree describes default behaviour; `--strict` is an opt-in flag that exists solely so a CI step can fail, and `TestFsckExitsZeroByDefault` pins the default. Same reasoning for `doctor --strict`.

Checks run in this order, each wrapped in `obs.Timed` and each honouring the remaining deadline (on expiry the check records `Truncated: true` and the loop stops):

| # | `Check` | What it does | `--repair` action |
|---|---|---|---|
| 1 | `layout` | Every directory of §7.4 + §3.3 exists and is a directory; `.qompack/.gitignore` exists and contains a line `*` | create missing dirs (0700); write `.gitignore` |
| 2 | `objects` | Walk `objects/??/??/*` — **not** `*.zst`, see the note below. For each entry: the basename is 64 lowercase hex, optionally followed by `.zst`; the two fanout dirs equal hex[0:2] and hex[2:4]; when the `.zst` suffix is present the bytes zstd-decode with a 64 MiB decoder-memory cap (bare-named entries are already plaintext); and **the 64 hex characters of `core.HashBytes(core.DomainChunk, plaintext)` equal the basename's hex** — compare on the hex form, since the store renders filenames with `hex.EncodeToString` and never with `Hash.String()`, so no `sha256:` prefix appears in any filename | move the object to `<quarantine>/objects/<basename>`, `Loud`, record |
| 3 | `roots` | For each content line of `index/roots.jsonl` that no later `{"op":"gc"}` line has already retired, every `ChunkRef.Hash` satisfies `store.Has` | **retire the root** — see "check 3's repair" below. This is where SP-06's D15 residual is discharged |
| 4 | `jsonl-tails` | For each append-only `*.jsonl` (`index/tool_use.jsonl`, `index/roots.jsonl`, `index/files.jsonl`, `index/sessions.jsonl`, `index/segments.jsonl`, `dag/deps.jsonl`, `records/eliminations.jsonl`, `checkpoints/MANIFEST.jsonl`, `pins/invariants.jsonl`): every line parses as JSON and the file ends with `\n` | copy the trailing partial bytes to `<quarantine>/tails/<file>.<unixms>.partial`, then truncate the file to the offset just after the last valid `\n` |
| 5 | `checkpoints` | `checkpoint.Reader.Verify` (MANIFEST re-hash); additionally each file's `version` field ∈ `SupportedCheckpointVersions = []int{1}` | mismatched hash or unsupported version → move the file to `<quarantine>/checkpoints/`, `Loud`. Physically removing it is what makes §12.3's existing "fall back to its parent" path apply unchanged |
| 6 | `manifest` | MANIFEST seqs strictly increasing with no gaps; every `checkpoints/NNNN.json` has a row; every row has a file | orphan file → append a re-hashed row; orphan row → record `FsckError`, no deletion |
| 7 | `readonly` | Every `checkpoints/*.json` is read-only. Detection is one portable expression — `fi.Mode().Perm()&0o222 == 0` — because Go maps `FILE_ATTRIBUTE_READONLY` onto `0444` vs `0666` on Windows; repair is `os.Chmod(p, 0o444)`, which clears/sets that attribute on Windows and sets the mode on POSIX. No `syscall` call is needed on either platform | `os.Chmod(p, 0o444)` |
| 8 | `pins` | `pins/invariants.jsonl` parses; the materialized `pins/invariants.json` view matches `pins.Store.All` | call `pins.Store.Materialize` |
| 9 | `bloom` | `sketch.Load` on `sketches/tried.bloom` succeeds (CRC + version); report `FillRatio`, `EstimatedFPRate`, `ResizeTarget` | on load failure **or** `needed == true`: `negknow.Ledger.RebuildBloom` (active records only, §8.3) |
| 10 | `sketches` | `touch.cms`, `explore.hll` load | corrupt → move to `<quarantine>/sketches/`, recreate empty from config (`NewCMS(epsilon, delta)`, `NewHLL(registers)`); they are caches, so this is lossless in kind |
| 11 | `locks` | `run/daemon.lock` exists → parse pid; probe liveness (`os.FindProcess`+`Signal(0)` on POSIX, `OpenProcess` via a `tasklist /FI "PID eq N"` exec on Windows) | dead pid → delete the lock, `Loud` |
| 12 | `spool` | Count `spool/*.ndjson` and their total bytes; flag any file older than 24h when no daemon is live | none (draining is the daemon's job; the finding tells the user to start a session) |
| 13 | `retention` | `store.Stats` + a dry-run `store.GC(GCPolicy{RetainDays: d, RetainSessions: s, DryRun: true, Deadline: remaining})`, where `d`/`s` are `cfg.Store.Retention.Days`/`.Sessions` normally and **`-1`/`-1` under `--gc-all`**; report collectable bytes for the policy that ran | run the same GC with `DryRun: false` |

**Check 2's two corrections, both load-bearing.** The store does **not** name objects `sha256(plaintext)` and does **not** always suffix them `.zst`:

- *The hash is domain-separated.* `internal/store/fsstore.go` names every chunk `core.HashBytes(core.DomainChunk, data)`, and `internal/core/hash.go` computes `sha256(domain || 0x00 || b)` with `DomainChunk = "qompack.chunk.v1"`. A plain `sha256(plaintext)` therefore equals the basename for **no object in any store**, so a check written that way quarantines every object in a perfectly healthy store the moment `--repair` is passed, and DoD item 11 ("exits 0 on a clean store") becomes unreachable. Read the domain constant from `internal/core` — `core.HashBytes(core.DomainChunk, plaintext)` — and never re-derive, re-spell or inline the string; it is a wire format (`internal/core/hash.go`'s own registry comment says so), and a local copy is exactly how it would silently drift.
- *The suffix is conditional.* `internal/store/objects.go` writes the bare 64-hex name when `store.compression` is `"none"`, and readers try `<hex>.zst` first and `<hex>` second so a store whose compression setting changed mid-life still reads. A glob of `objects/??/??/*.zst` therefore walks **zero** objects in a `compression: "none"` store and the check passes vacuously on a store it never looked at. Glob `objects/??/??/*` and branch on the suffix.

**Check 3's repair — SP-06's D15 residual, discharged here.** `plans/V2-SP-06-content-addressed-store.md` records that dropping the per-object `fsync` made "a root line without its object" a reachable crash state, "degraded-**but-detected**", and hands the repair to this slice as "an explicit **residual SP-17 inherits**". Detecting it and declining to act would leave that residual open, so `--repair` retires the root:

1. Append a retirement record to `index/roots.jsonl`, byte-identical in shape to the one `internal/store`'s GC already writes: `{"v":1,"op":"gc","root":"sha256:<64hex>","ts":<unixms>}\n`. Retirement is an **append, never a rewrite** — `internal/store/roots.go`'s `appendGCTombstone` establishes exactly that, and its comment states the invariant ("it never rewrites an existing line"), so the file stays append-only, the original record stays readable, and `store.Open` drops the root on the next load through the same code path it already uses for a GC tombstone. `fsck` writes the line through `paths.AppendOnly`; it does **not** need a new exported symbol in `internal/store`, and must not add one.
2. Scan `index/tool_use.jsonl` for the records whose root is the retired hash and list their `tool_use` IDs in the `Finding.Detail`, plus in `<quarantine>/roots/<unixms>.orphaned-roots.json` as `{"root":…, "tool_use_ids":[…], "missing_chunks":[…]}`. Those IDs are precisely what re-observation has to reproduce, and writing them down is the difference between "the index is consistent again" and "something was lost and nobody can say what".
3. Severity is `FsckError` either way; `Repaired` is true only when both steps succeeded.

The remedy is still re-observation — `fsck` cannot invent chunk bytes it does not have. What changes is that the store stops carrying a reference it can never satisfy, and the operator gets the list of what to re-run. `TestFsckOrphanedRootRetired` covers it.

Report rendering (human mode) is one line per finding, `SEVERITY  check  path  detail`, padded to aligned columns, followed by a summary line `fsck: N findings (E error, W warn, I info) in D` and, when `--repair` was not passed and errors exist, the literal line `run 'qompack fsck --repair' to repair`.

**Budget:** a store with 200 000 objects / 2 GiB completes checks 1–13 in **< 60 s** on the CI ubuntu runner when invoked with `--deadline 120s` (the deadline is a ceiling, not a target). Whenever the deadline expires first, the run stops at the current check and returns `Truncated: true` with every finding gathered so far, instead of overrunning — asserted at `--deadline 200ms` by `TestFsckDeadlineResumable`. Benchmarked by `BenchmarkFsckLargeStore`, which uses `--deadline 120s` and asserts wall < 60 s.

### 8. `internal/cli/doctor.go` — `qompack doctor`

Flags: `--json`, `--strict`, `--skip-mcp`. Same exit policy as `fsck` (0 unless `--strict`). Sixteen checks, run and reported in exactly this order, each with a `Remedy` string when not `CheckPass`:

| ID | Assertion | Fail/Warn condition | Remedy line |
|---|---|---|---|
| `binary` | `Version()`; then `core.Version` compared against the `version` field of the **installed** `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`. With one stamped source these can only disagree when the binary and the bundle around it came from different releases — a half-finished upgrade — which is a real, observable condition rather than the vacuous self-comparison a second version variable would have produced | mismatch → WARN; `core.Version` still at its unstamped compiled-in default (no `-X`) → WARN | mismatch: "the binary and the bundle are from different releases — re-extract the bundle". Unstamped: "development build — reinstall a released bundle" |
| `plugin_root` | `CLAUDE_PLUGIN_ROOT` set, exists, contains `hooks/hooks.json`, `.mcp.json`, `.claude-plugin/plugin.json`, seven `commands/*.md`; each parses; the command names equal `commands.All` names | any missing → FAIL | "reinstall the plugin bundle; see docs/install.md" |
| `launcher` | On Windows, whether the running binary's path ends in `qompack-windows-<arch>.exe` (universal bundle) rather than `qompack.exe` | universal on Windows → INFO | "the per-platform bundle removes one cmd.exe spawn per hook" |
| `project_root` | Resolution per §3.3 (env → payload/`.git` walk → cwd); print which rule matched | unresolvable → FAIL | "run inside a project, or set QOMPACK_PROJECT_ROOT" |
| `layout` | `.qompack/` exists with the §3.3 subtree; free space on its volume, read with **stdlib only** — `syscall.Statfs` on POSIX, `syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")` on Windows, in two build-tagged files. The closed dependency list of §2.5 does not gain an entry | < 200 MiB → WARN, < 20 MiB → FAIL | "free disk space; the store degrades to spool-only when writes fail" |
| `config` | `config.Load` → count `[]Warning` and `[]Violation`; read `state/config-violations.json` | any violation → WARN | "run 'qompack config print --provenance'" |
| `daemon` | `ipc.Resolve(root)` → `c.Send(ctx, ipc.Request{Op: "status", Session: sess, TS: now, Reply: true}, 2*time.Second)`; report `Response.Mode` and `Response.Hot` | no listener → WARN; `Hot == Spool` → WARN; `Mode != ModeFull` → FAIL | "start a session, or check .qompack/logs/LOUD.log" |
| `ipc_perms` | POSIX: socket parent dir mode `0700`, socket mode `0600`, both owned by `os.Getuid()`. Windows: `winio.DialPipe` from this process succeeds, and — when the SDDL is readable — it grants the current user SID and carries no `WD` (Everyone) or `AN` (Anonymous) ACE | POSIX: wrong mode/owner → FAIL. Windows: dial failure → FAIL; SDDL present but containing `WD`/`AN` → FAIL; **SDDL unreadable on this host → INFO** with `Observed: "pipe ACL not readable"` (never FAIL — an unreadable ACL is a host limitation, not a defect) | "delete .qompack/run and restart the session" |
| `contract` | `contract.Monitor.Report()` rendered as an ID/expected/observed table | any `SevCritical` false → FAIL | "see the failing assertion; the session is running degraded-passive" |
| `latency` | `obs.Registry.CheckBudgets(cfg)` for the gated budgets, plus `Snapshot()` for the reported one. **The 15 ms and 2 s numbers are never written in `internal/cli`** — D11/§11.6 forbids it; `CheckBudgets` owns them and returns `[]BudgetBreach{Budget, Observed, Limit, Windows}`, which doctor renders verbatim | any `BudgetBreach` naming B-A or B-E → FAIL, quoting its `Observed`/`Limit`; B-D p99 ≥ 25 ms → WARN (B-D is reported-not-gated per §2.4, so 25 ms is a display threshold and carries `//nomagic:allow B-D display threshold, not a §11.3 budget`) | "see the defender check; consider the per-platform bundle" |
| `store` | `store.Stats`: objects, bytes, `DedupRatio` printed against the Phase 1 criterion (≥ 4:1) | ratio < 4.0 → INFO (not a failure: it is session-shape dependent) | "read-heavy sessions should exceed 4:1; test-heavy sessions vary" |
| `sketches` | `negknow.Ledger.Health()`: `FillRatio`, `EstFPRate`, `NeedsResize`; Appendix A's `m ≈ 95 850 bits, k = 7` printed for context | `EstFPRate ≥ 0.05` → WARN, `≥ 0.10` → FAIL (§11.4) | "run 'qompack fsck --repair' to rebuild and resize tried.bloom" |
| `checkpoints` | `checkpoint.Reader.List` count, latest seq/bytes, `Verify` result, distinct `version` values against `SupportedCheckpointVersions` | MANIFEST/hash mismatch → FAIL; a `version` greater than the highest supported → FAIL | mismatch: "run 'qompack fsck --repair'". Unsupported-newer version: **"upgrade qompack: this store contains a checkpoint written by a newer version; 'qompack fsck --repair' quarantines it and the parent checkpoint stays usable"** (the two remedies are distinct strings; `TestUpgradeCheckpointSchemaBump` asserts the second) |
| `mcp` | Unless `--skip-mcp`: spawn `qompack mcp` as a child with a 5 s deadline, send `initialize`, `notifications/initialized`, `tools/list`; assert exactly 8 tools; kill the child | fewer than 8 tools or handshake failure → FAIL | "reinstall; .mcp.json must point at this binary" |
| `defender` | Windows only: `powershell -NoProfile -NonInteractive -Command "(Get-MpComputerStatus).RealTimeProtectionEnabled"` with a 1500 ms timeout | `True` **and** B-D p99 ≥ 25 ms → WARN | "Add-MpPreference -ExclusionPath '<plugin bin dir>'" |
| `telemetry` | `cfg.Runtime.Telemetry.Enabled` | `true` → FAIL (impossible after the hardwire in commit 3, so this check is the tripwire) | "telemetry is hardwired off; a true here is a build defect — file an issue" |

`doctor` completes in **< 3 s** with `--skip-mcp` and **< 8 s** with the MCP probe, both asserted.

### 9. `test/security/` — the security audit

Package `security_test` (external, drives `go list` and the built binary; imports no `internal/` composition root).

**`importgraph_test.go`.** Shells out to `go list -deps -json ./...` and `go list -deps -json ./cmd/qompack`, decodes the stream, and asserts:

- No non-test package under `github.com/qompack/qompack/...` has `net/http`, `net/http/httptest`, `net/url`, `net/rpc`, `net/smtp`, `crypto/tls`, or any `golang.org/x/net/...` in `Imports` or `Deps`.
- `net` appears in the `Deps` of exactly one first-party package: `github.com/qompack/qompack/internal/ipc`.
- `os/exec` appears in the `Imports` of only `internal/daemon`, `internal/cli`, `tools/...` and `test/...`.
- The module set of `./cmd/qompack`'s deps is exactly: the standard library, `github.com/klauspost/compress/...`, `github.com/Microsoft/go-winio/...` (windows builds only), and `golang.org/x/sys/...` (transitively required by go-winio, windows builds only). The list is a hard-coded allowlist; adding to it requires editing this test, which is the point. Run once per `GOOS` via `go list` with `GOOS` set, so the windows-only entries are proven windows-only.
- `github.com/stretchr/testify`, `github.com/google/go-cmp` and `pgregory.net/rapid` appear in **no** `Deps` of `./cmd/qompack` under any `GOOS`.

**`network_test.go`.** Static assertion above is primary. Runtime assertion, in two parts:
1. *Canary* (all platforms): start `net.Listen("tcp", "127.0.0.1:0")` in the test, export `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY=""` pointing at it, run a full synthetic session against the built binary (session-start, 200 × `observe tool`, `checkpoint`, `flush`, `qompack mcp` handshake, all seven commands), then assert the listener accepted zero connections.
2. *Socket inventory* (linux only, gated on `runtime.GOOS == "linux"`): while the daemon is live, read `/proc/<pid>/fd/*` symlinks, collect every `socket:[<inode>]`, and cross-reference `/proc/net/{tcp,tcp6,udp,udp6}` inode columns; assert **zero** matches, i.e. every socket the daemon holds is a Unix socket. Documented in `docs/security.md` as the strongest available observation and as linux-only, with the canary covering the other two platforms.

**`writeset_test.go`.** Creates a temp tree with three roots — `<tmp>/project`, `<tmp>/home` (exported as `HOME` and `USERPROFILE`), `<tmp>/tmpdir` (exported as `TMPDIR`/`TEMP`/`TMP`) — plus a fourth, `<tmp>/xdg` as `XDG_RUNTIME_DIR`. Snapshots every path under all four (relative path, mode, size, sha256 of content) before and after the full session above, then diffs. Every created or modified path must match one of exactly these prefixes:

```
project/.qompack/
home/.qompack/
xdg/qompack/
tmpdir/qompack-<uid>/          (POSIX fallback socket dir)
```

Anything else — including a stray file in `project/`, a dotfile in `home/`, or an entry in `tmpdir/` outside the socket dir — fails with the offending path printed. Windows adds no fourth prefix (the named pipe is not a filesystem object under these roots).

**`telemetry_test.go`.** Asserts `config.Defaults().Runtime.Telemetry.Enabled == false`; then writes `{"runtime":{"telemetry":{"enabled":true}}}` into each of the user layer, the project layer, `QOMPACK_RUNTIME__TELEMETRY__ENABLED=true`, and `--set runtime.telemetry.enabled=true`, and asserts that after `config.Load` the value is still `false` and a `Warning` with `Key == "runtime.telemetry.enabled"` and `Message == "telemetry is hardwired off in this build"` was returned. The hardwire is four lines at the end of `config.Load` and is this slice's only change to `internal/config`.

**`redaction_test.go`.** `internal/redact` ships **ten** built-in rules, and their names are the exact strings `internal/redact/rules.go` passes to `mk(…)`:

`pem_private_key`, `aws_access_key_id`, `github_token`, `anthropic_key`, `generic_sk_key`, `jwt`, `bearer_token`, `credentialed_uri`, `assignment_secret`, `dotenv_value`

`internal/redact/redact.go` builds every placeholder as `"«redacted:" + rule.name + "»"`, so the emitted strings are `«redacted:pem_private_key»`, `«redacted:credentialed_uri»`, `«redacted:generic_sk_key»` and so on. **Short forms such as `«redacted:pem»`, `«redacted:aws»` or `«redacted:connstring»` do not exist anywhere in the binary** and a test that expects one fails against a working redactor. Two consequences the implementer must not undo:

- **`ASIA` is not a rule of its own.** `aws_access_key_id`'s pattern is `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`, so a session token and a long-lived key both render `«redacted:aws_access_key_id»`. There is no `aws-session` rule to write a fixture for.
- **`generic_sk_key` is the tenth rule** — the `sk-`-prefixed catch-all that sits beside `anthropic_key`'s `sk-ant-`. It needs a row of its own.

The fixtures already exist: `testdata/corpora/secrets/` holds one `.txt` per rule, named after the rule, plus `adversarial-already-redacted.txt`, `mixed-deploy-log.txt` and `mixed-env-dump.txt`. **Reuse them.** The test derives each case's expected placeholder from the fixture's own basename — `"«redacted:" + strings.TrimSuffix(filepath.Base(p), ".txt") + "»"` — so the expectation is read from the rule set rather than transcribed beside it, and a rule renamed in `internal/redact` fails the test loudly instead of silently matching a stale literal. (`internal/redact` exposes the rule set only through the unexported `builtinRules()`; SP-06 owns that package and SP-17 adds nothing to it, see Out of scope, so the fixture basename is the binding and no accessor is exported to make it prettier.) Where a fixture lacks the unique 32-character marker the assertions below need, **add the marker to that fixture**; do not replace the fixture or add a parallel one. Then, per rule:

1. `store.PutBytes(fixture)` → `store.Open(root)` → read every chunk back → assert the marker is absent and the fixture's own `«redacted:<rule>»` placeholder is present.
2. Walk **every byte of every file** under `.qompack/` (objects decompressed, all `*.jsonl`, `logs/*.log`, `spool/*.ndjson`, `checkpoints/*.json`, `state/*.json`, `metrics/*.json`) and assert the marker appears **zero** times.
3. Repeat 1–2 through the real binary with the secret arriving as a `PostToolUse` `tool_response`, so the hook path is covered as well as the library path.

**`panic_test.go`.** Registers a deliberately panicking `mcp.Tool` and asserts `tools/call` returns `IsError: true` with the panic message elided and the server still answering `ping`. Then, for each of the eight real tools, sends six malformed argument shapes — `null`, `[]`, `{"query":123}`, `{"k":-1}`, `{"k":2147483647}`, a 4 MiB string — and asserts: no panic, no process exit, a JSON-RPC response in every case.

**`alloc_test.go`.** **Six** bounds, each asserted by behaviour and by a `runtime.MemStats` delta below 8 MiB:

| # | Input | Bound | Status |
|---|---|---|---|
| 1 | 100 MiB on stdin to a hook | `hookio.ReadEvent(r, 1<<20)` returns an error; the hook exits 0 | pre-existing (SP-01's `limit` parameter) — assert only |
| 2 | A 4 GiB length claim on an IPC line | the connection is dropped at `runtime.hotPath.maxPayloadBytes` (1 048 576) | pre-existing (SP-05's 1 MiB max line, §2.4) — assert only |
| 3 | 200 000-deep nested JSON array | `encoding/json` returns an error (its 10 000-depth limit); no panic, no OOM | stdlib — assert only, no code |
| 4 | `recall` with `k = 2147483647` | clamped to 100 hits | **new**, commit 6 |
| 5 | `expand` with `span = "0:9223372036854775807"` | clamped to `runtime.mcp.maxResponseBytes` (262 144) | **new**, commit 6 |
| 6 | A zstd object whose plaintext is 4 GiB | `store.GetChunk` fails cleanly with a 64 MiB decoder-memory cap (`zstd.WithDecoderMaxMemory`) | **new**, commit 6 |

Rows 4–6 are the hardening deliverables of commit 6, and rows 4 and 5 land inside SP-13's `internal/mcp` while row 6 lands inside SP-06's `internal/store`: all three are therefore ground-rule-4 fixes — smallest possible diff, proven by the row's test, listed in the commit body under `Hardening: internal/mcp (SP-13)` / `Hardening: internal/store (SP-06)`. If an owning package already enforces one, that row's test simply passes and no code changes for it.

**`tooling_test.go`.** `gosec -quiet -severity medium -confidence medium -exclude-dir testdata -exclude-dir dist ./...` and `govulncheck ./...` both exit 0. Suppressions live in `.gosec.json` with a `"reason"` field per rule/path; a test asserts every suppression carries a non-empty reason and that the suppression count is ≤ 6. Both are `testing.Short()`-skipped so the local loop stays fast; CI runs them without `-short`.

### 10. `test/platform/` — the cross-platform matrix

Package `platform_test`, driven by `testutil.NewProject` plus the built binary. `devtool platform-matrix [--json]` runs the same set outside `go test` for the CI artifact.

| Test | Setup | Expectation |
|---|---|---|
| `TestLongPathStoreRoundTrip` | project root nested so `<root>/.qompack/objects/ab/cd/<64hex>.zst` is 300+ chars | put/get round-trips; on Windows the `\\?\` prefix is applied by `paths`; no `ERROR_PATH_NOT_FOUND` |
| `TestPathWithSpacesAndUnicode` | root `<tmp>/qompack tests/проект — δοκιμή/` | `paths.Norm`/`Key` succeed; `ipc.Resolve` hash12 is stable across two calls; a full hook round-trip works |
| `TestCaseCollidingPaths` | store results for `src/Foo.ts` and `src/foo.ts` | `paths.Key` equal on windows+darwin, distinct on linux; `ToolUseRecord.Path` preserves original casing on all three; no index corruption either way |
| `TestCRLFDedupEquivalence` | the same 40 KiB file read once with CRLF and once with LF | identical `PutResult.Root.Hash`; `Reduced > 0`; a `canon.Delta` of class `crlf` recorded when `KeepDeltas` |
| `TestReadOnlySourceFile` | a 0444 source file read by a tool | stored normally; no attempt to write to it; hook exits 0 |
| `TestReadOnlyCheckpointNotRewritten` | an existing `checkpoints/0001.json` at 0444 | a second `Finalize` for seq 1 fails with `core.ErrAppendOnly`; `fsck` reports no error; the file's sha256 is unchanged |
| `TestConcurrentDaemonStart` | 8 concurrent `qompack session-start` processes against one project | all 8 exit 0; exactly one live pid; exactly one `run/daemon.lock`; the other 7 either connected or spooled; zero corrupt lines in any `*.jsonl` |
| `TestSocketPermissionsPOSIX` | POSIX only | socket dir `0700`, socket `0600`, both owned by `os.Getuid()`; the resolved path is ≤ 100 bytes or the `qp-<hash8>` fallback was used |
| `TestNamedPipeACLWindows` | Windows only | Two assertions, both unconditional: (a) the pipe `\\.\pipe\qompack.<hash12>` is listed by `os.ReadDir(\\.\pipe\)` and `winio.DialPipe` from this process succeeds; (b) the SDDL string the daemon *applied* — read back with `powershell -NoProfile -NonInteractive -Command "(Get-Acl -LiteralPath '\\.\pipe\qompack.<hash12>').Sddl"` — contains the current user's SID (from `whoami /user`) and no `WD` or `AN` ACE. If that PowerShell call errors or PowerShell is missing, (b) is recorded as `Observed: "pipe ACL not readable"` and the test asserts (a) plus that `doctor`'s `ipc_perms` check reported `CheckInfo` for the same reason — **the test never skips**, so a genuinely world-writable pipe can never hide behind a skip |
| `TestWindowsReservedNames` | tool results for `CON`, `NUL`, `aux.txt`, `trailing.` | `paths.Norm` rejects or escapes; nothing panics; the hook exits 0 |
| `TestUnicodeToolOutputRoundTrip` | tool output containing emoji, CJK, RTL, and a lone surrogate escape | byte-exact round-trip through store and `expand` |
| `TestDefenderExclusionEffect` | Windows CI only | run `bench-hotpath -n 500` with and without `Add-MpPreference -ExclusionPath <bin dir>`; record both B-D p99 into the artifact; assert **B-A p99 < 15 ms in both arms**; assert `doctor`'s `defender` check reports WARN in the un-excluded arm when B-D p99 ≥ 25 ms. If `Add-MpPreference` fails (non-admin), skip the excluded arm and still assert the un-excluded arm |

### 11. `test/fault/` — fault injection

Nine injections × six hook subcommands (`observe tool`, `observe prompt`, `observe stop`, `session-start`, `checkpoint`, `flush`) = 54 table-driven cases in `TestHooksExitZero_Matrix`, each asserting **exit code 0**, **stdout is empty or valid `hookio.Output` JSON**, and **stderr contains no stack trace**.

| Injection | How it is produced (portable, no production fault hooks) |
|---|---|
| `daemon-down` | no daemon; `run/daemon.lock` present containing a dead pid and a stale pipe path |
| `write-fails` | replace `.qompack/objects` with a **regular file** so every create under it fails `ENOTDIR`/`ERROR_DIRECTORY`; separately chmod `.qompack/spool` to `0555` (POSIX) / apply `icacls <dir> /deny "%USERNAME%":(W)` (Windows) |
| `corrupt-object` | flip byte 7 of a randomly chosen `objects/**/*.zst` |
| `truncated-jsonl` | append `{"partial":` (no newline) to every append-only `*.jsonl` |
| `killed-mid-write` | start the daemon, send 500 events, `Process.Kill()` (SIGKILL / TerminateProcess) 5 ms after the 250th ACK, restart |
| `clock-skew` | `testutil.FakeClock` jumps −1 h then +25 h between hooks; plus `os.Chtimes` setting a checkpoint's mtime one year into the future |
| `concurrent-sessions` | 8 session IDs × 4 hooks each, all concurrent, against one daemon |
| `corrupt-bloom` | zero the CRC field of `sketches/tried.bloom` |
| `oversized-payload` | an 8 MiB `tool_response` (8× `runtime.hotPath.maxPayloadBytes`) |

Dedicated assertions beyond exit codes:

- `TestDaemonDownSpools` — after `daemon-down`, `spool/client-*.ndjson` contains exactly one line per hook invocation, each a valid `ipc.Request`; a subsequently started daemon's `Drain` returns that count and deletes the files.
- `TestKilledMidWriteRecovers` — after restart, `Drain` replays the WAL, `store.Stats().ToolUses` equals the number of ACKed events, every `*.jsonl` parses, and `fsck --strict` exits 0.
- `TestCorruptObjectQuarantined` — reading the corrupt object `Loud`s once, the object lands in `tmp/quarantine/objects/`, the session continues, and `fsck` reports it as already repaired.
- `TestTruncatedJSONLTolerated` — readers skip the partial tail without error; `fsck --repair` truncates to the last valid `\n` and the quarantined tail is byte-identical to what was appended.
- `TestClockSkewBackward` / `TestClockSkewForward` — checkpoint seq stays strictly increasing; GC's retention window never deletes an object inside `max(30 days, 10 sessions)` computed under skew; `TTLState` is `unknown` rather than `cold` when `LastAPICallTS` is in the future.
- `TestConcurrentSessionsNoCorruption` — every `*.jsonl` line parses; `tool_use.jsonl` has no duplicate `ID`; one daemon.
- `TestSpoolWriteFailureCounted` — with the spool dir unwritable, `obs.Counter("l0.dropped")` increments and exactly one `Loud` per session appears in `LOUD.log`.
- `TestDegradedPassiveBehaviour` — force a `SevCritical` contract failure; assert L0/L1 still write (store objects grow, eliminations still record) while `additionalContext`, `customInstructions`, scheduler-initiated checkpoints and the drop report are all absent, and MCP tools still answer. This is §12.1's contract, asserted rather than assumed.

### 12. Release pipeline

**`.github/workflows/release.yml`** — SP-17 grows the existing single-job workflow into the seven-job pipeline below. Everything already in that file stays: the `fetch-depth: 0` checkout, the pinned `setup-go` version, the `ci-local` gate, `goreleaser-action` with `args: release --clean` and `GITHUB_TOKEN`, and the three `permissions:` grants. The `env: GOTOOLCHAIN: local, CGO_ENABLED: '0'` block moves to workflow scope so every job inherits it. Two things change and both are deliberate: the tag filter narrows (below), and the action pin goes from `~> v2` to `~> v2.6` (§1).

```yaml
name: release
on:
  push:
    # NARROWED from SP-01's 'v*'. 00-ARCHITECTURE §9 puts a `v0.<wave>.<n>` tag on `develop` after
    # EVERY verification, and `v0.0.1` / `v0.1.0` already exist. A `v*` or `v*.*.*` filter matches
    # all of them, so every wave tag that reaches origin as a NEW ref starts a release run.
    # The damage is bounded but real: `guard` is the first job and has no `needs:`, so such a run
    # dies immediately on `verify-version` (a v0.x tag cannot equal the release version) rather
    # than burning the three-OS matrix — the cost is noise and a red workflow badge on a healthy
    # repo, not wasted CI. Requiring a leading 1-9 keeps every v0.* development tag out by
    # construction while matching every real release from v1.0.0 on.
    tags: ['v[1-9]*.*.*']
permissions:
  contents: write
  id-token: write
  attestations: write
env:
  GOTOOLCHAIN: local
  CGO_ENABLED: '0'
jobs:
  guard:            # checkout with fetch-depth: 0 (shallow clones cannot answer --contains),
                    # then: go run ./tools/devtool release-guard --tag ${{ github.ref_name }}
  verify:           # needs: guard — reuse ci.yml's verify/test/cover jobs at the tag
  gates:            # needs: verify — bench-gate, replay-gate, plugin-validate, security, package-gate
  package:          # needs: gates — in this exact order, in one job:
                    #   1. goreleaser release --clean   (draft release + binary archives + checksums.txt)
                    #   2. go run ./tools/devtool package --version ${TAG#v}   (writes dist/qompack-plugin-*)
                    #   3. gh release upload ${TAG} dist/qompack-plugin-*.tar.gz dist/qompack-plugin-*.zip \
                    #                               dist/qompack-plugin-*.sha256
                    #   4. actions/upload-artifact@v4 — name: release-dist, retention-days: 1, path: |
                    #        dist/checksums.txt
                    #        dist/qompack-plugin-*.tar.gz
                    #        dist/qompack-plugin-*.zip
                    # Step 1 must precede step 2 because --clean deletes dist/ on entry.
                    # Step 4 is not optional: see "How dist/ reaches attest" below.
  install-gate:     # needs: package — matrix ubuntu/macos/windows: download the uploaded bundles
                    #   from the draft release, install, exercise (the commit-8 test set)
  attest:           # needs: install-gate
                    #   1. actions/download-artifact@v4 — name: release-dist, path: dist
                    #      (FIRST step, before any attestation: this job's runner is fresh and its
                    #       dist/ is empty until this runs)
                    #   2. actions/attest-build-provenance@v2, run twice:
                    #        subject-checksums: dist/checksums.txt        (the six binary archives)
                    #        subject-path: 'dist/qompack-plugin-*.tar.gz,dist/qompack-plugin-*.zip'
                    #          (all 10 plugin archives: 6 per-platform tar.gz, 2 windows zip,
                    #           universal tar.gz + universal zip — i.e. 7 bundle trees, 10 archives)
  publish:          # needs: attest — gh release edit ${TAG} --draft=false
```

**How `dist/` reaches `attest`.** GitHub Actions jobs do not share a filesystem: `dist/` exists only on the `package` job's runner, and `install-gate` sits between the two, so by the time `attest` starts its working directory is a fresh checkout with no `dist/` at all. `actions/attest-build-provenance` **fails** when `subject-path` matches nothing, so an `attest` job handed raw `dist/` paths cannot succeed — and the provenance attestation is named in DoD item 4, in §"What exists when you finish", and in the `gh attestation verify` command `docs/release.md` tells users to run. The upload/download pair above is therefore part of the specification, not an implementation detail: `package` step 4 uploads `dist/checksums.txt` and both plugin-archive globs under the artifact name `release-dist`, and `attest` downloads that artifact into `dist/` as its first step. `install-gate` needs no such transfer — it deliberately fetches from the draft release, which is the path a real user takes.

`release-guard` (`tools/devtool/guard.go`, task `release-guard --tag <v1.2.3> [--require-branch main]`) is a Go task rather than inline YAML precisely so it is unit-testable off CI. It exits 1 when: the tag does not parse as semver with a leading `v`; `git branch --contains <tag>` does not include the required branch (default `main`); `devtool verify-version --tag` disagrees; `devtool changelog --check` reports drift; or **the tag already has a *published* release**. It prints every failing condition, not just the first. In tests `gh` and `git` are exercised against a fixture repo with a stub `gh` earlier on `PATH`.

**The already-exists check is scoped to published releases, on purpose.** The naive form — "`gh release view <tag>` exits 0" — is wrong inside this pipeline, because `package` creates the release as a **draft** (`release: draft: true`, §1) and `publish` un-drafts it last. `gh release view` resolves drafts for an authenticated user with repo access, so from the moment `package` runs, the naive check is true for a tag that has not shipped anything. "Re-run failed jobs" is unaffected — it re-runs only the failed job and its dependents, so a successful `guard` is not re-executed and the ordinary flaky-matrix-leg recovery still works — but "Re-run all jobs", and any second attempt after a failure *inside* `package` itself once goreleaser had already created the draft, would both die at `guard` with no way forward except minting a new version number for an infrastructure flake. The check is therefore `gh release view <tag> --json isDraft -q .isDraft`, failing only when it prints `false`: a genuinely published tag is still protected — tags are immutable by policy, a bad release is superseded and never moved — while a half-finished pipeline can be driven to completion. `gh` exiting non-zero (no release at all) is the clean case. `TestGuardAllowsExistingDraft` pins the distinction alongside `TestGuardRejectsExistingRelease`.

**`CHANGELOG.md`** — keep-a-changelog. `devtool changelog --from <tag> --to <ref> [--check]` reads `git log --no-merges --pretty=%H%x00%s%x00%b` and maps conventional-commit types to sections:

| type | section |
|---|---|
| `feat` | Added |
| `fix` | Fixed |
| `perf`, `refactor` | Changed |
| any type with `!` or a `BREAKING CHANGE:` body line | Removed / Changed, listed first, prefixed `**BREAKING**` |
| `docs`, `test`, `chore`, `ci`, `build`, `revert` | omitted unless breaking |

Each line renders as `- **<scope>:** <subject> ([<sha7>](https://github.com/qompack/qompack/commit/<sha40>))`, sorted by scope then subject (both `sort.Strings`, stable), with any `Refs:` footer's gap IDs appended as ` (G3.2, G9.2)` in the order they appear in the footer. A commit with no `(<scope>)` renders as `- <subject> (…)` and sorts before all scoped lines. `--check` regenerates and diffs against the file, exiting 1 on drift; CI runs it in `guard`.

**`docs/release.md`** documents: the six-target matrix; what `guard` checks; **the tag-namespace warning** — one line, at the top of the "Cutting a release" section, reading in substance *"pushing a tag matching `v[1-9]*.*.*` starts the release pipeline; the `v0.<wave>.<n>` development tags 00-ARCHITECTURE §9 puts on `develop` after each verification deliberately do not match, and no tag should be pushed with a bare `git push --tags` — name the refspec (`git push origin refs/tags/v1.2.3`) so the operator pushes exactly the tag they mean"*; the deterministic-archive rules; the SHA256SUMS and `.sha256` formats; the provenance attestation and the exact `gh attestation verify dist/qompack-plugin-<v>-universal.zip --repo qompack/qompack` command a user runs; the **rollback procedure** (never move a tag — publish `vX.Y.Z+1` containing `git revert -m 1 <merge>`; mark the bad version `### Yanked` in `CHANGELOG.md`; edit the bad GitHub release to prerelease + prepend a yank notice; artifacts stay up so existing installs keep verifying); the **upgrade path** (extract the new bundle over the old plugin root, restart Claude Code — the daemon's idle-exit plus stale-lock reclamation handles the old process; `.qompack/` is forward-compatible within a checkpoint schema version, and a newer schema is quarantined by `fsck` with the parent checkpoint remaining usable); and a **Deferred** section naming code signing / notarization, marketplace publication, and the one-line `archives.files` addition of `README.md` that SP-18 makes when it merges — each with the exact steps to add it.

> **Cross-plan note on the same hazard.** `plans/V6-VERIFY-production-readiness-and-uat.md` §10 previously closed the whole plan set with `git push origin main develop --tags`, which pushes every local tag at once. It is being changed, in parallel with this plan, to explicit refspecs (`git push origin main develop refs/tags/v<semver>`). SP-17 does not edit that file; the narrowed trigger above means a stray `--tags` cannot fire the release pipeline even if one is typed.

**`docs/install.md`** documents both bundle flavours, the recommendation of the per-platform bundle on Windows, checksum verification, and the uninstall procedure (delete the plugin root; optionally delete `<project>/.qompack/` and `~/.qompack/`; nothing else exists).

**`docs/security.md`** — SP-17 is its sole author (see the header). Its required `##` sections are the **union** of what this plan's audits produce and what SP-18's documentation tests were written against, so nothing is lost by SP-17 owning the file. Exactly these headings, in this order:

| `##` section | Content |
|---|---|
| `Threat model in one paragraph` | Reads your code, writes to two gitignored directories, talks to nobody. One paragraph, no list |
| `No network` | The three network proofs of §9 and their platform coverage: the `go list -deps` import-graph allowlist (all three `GOOS`), the proxy-canary run (all platforms), the `/proc` socket inventory (linux only, named as linux-only and as the strongest available observation) |
| `No telemetry` | The hardwire at the end of `config.Load`, the `Warning` it emits, and the four config layers `telemetry_test.go` proves it survives |
| `Confined write set` | The four prefixes of §9's `writeset_test.go`, verbatim, and what a violation would look like |
| `Redaction` | The ten-rule inventory by its real rule names, the `«redacted:<name>»` placeholder form, and the `store.Put`/`PutBytes` choke point SP-06 owns |
| `File permissions` | `.qompack/` at 0700, objects at 0600, checkpoints at 0444, the POSIX socket dir/socket at 0700/0600, and the Windows named-pipe ACL assertion — each naming the check in `doctor` or `test/platform` that holds it |
| `Dependency posture` | The allowlist including the `golang.org/x/sys` transitive allowance with its justification; the gosec/govulncheck posture and every suppression's written reason |
| `Degradation and recovery` | The §12.3 table with a **recovery paragraph per row**, each referencing the same finding string its `TestDegradation_*` test asserts |
| `What this plugin cannot do` | The §12 list reproduced **verbatim** |
| `What to do if a secret reached the store` | The operator procedure: `qompack fsck --repair --gc-all` after removing the source, why quarantine is not deletion, and the honest statement that redaction is a choke point at write time and not a retroactive scrubber |
| `Reporting a vulnerability` | Where to file, what to include, and the response expectation |

The audit half (`Threat model` … `Dependency posture`) is written in commit 3; the recovery half (`Degradation and recovery` … `Reporting a vulnerability`) in commit 6. `TestSecurityDocSections` in `test/install/` asserts the eleven headings are present in this order — it lives there, not in `test/docs/`, because §1.18.14 of V6-VERIFY pins `test/docs` to two `internal/` imports.

### 13. CI additions to `.github/workflows/ci.yml`

| Job | Runs on | Steps |
|---|---|---|
| `package-gate` | ubuntu, macos, windows | `devtool package --json`; assert determinism by packaging twice into two out dirs and comparing archive sha256s; assert binary sizes; assert bundle layout; measure launcher overhead **of the universal bundle only** (`p99 < 4 ms` POSIX, `< 15 ms` Windows, 500 iterations) and upload the JSON artifact. The launcher spawn is host process-creation cost and is therefore reported inside **B-D**, never inside B-A — `install-gate` and `bench-gate` install the **per-platform** bundle, whose `bin/qompack[.exe]` is the native binary, so the B-A/B-E gates measure the shipping recommendation and not the fallback |
| `platform-matrix` | ubuntu, macos, windows | `go test ./test/platform/... -count=1` |
| `fault-gate` | ubuntu, macos, windows | `go test ./test/fault/... -count=1` |
| `install-gate` | ubuntu, macos, windows | `devtool install-check` then `devtool uninstall-check` against the freshly packaged bundle |
| `security` (extended) | ubuntu | existing steps plus `go test ./test/security/... -count=1` and `actions/dependency-review-action@v4` on PRs |

All five are required checks on `develop` and `main` from this branch's merge onward. The four **new** jobs take `.github/workflows/ci.yml` from the **ten** it carries today (`verify`, `lint-windows`, `test`, `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`) to **fourteen**; `security` is extended in place and is not a fourteenth entry.

### 14. `test/bench/hotpath` — the `--bundle` selector

`bench-hotpath` currently spawns whatever `qompack` binary its harness builds, which measures a development build rather than the shipping artifact. SP-17 adds one flag, and every V6 budget command depends on it:

`--bundle native|universal|source` (default `source`, the behaviour that exists today, so every committed `bench-hotpath` line in `ci.yml` and `nightly.yml` keeps its exact current meaning).

| Value | What it spawns | Why it exists |
|---|---|---|
| `source` | the harness-built binary, as today | the local loop and the existing `bench-gate`/`nightly` invocations |
| `native` | `dist/plugin-<goos>-<goarch>/bin/qompack[.exe]` from `devtool package` | B-A and B-E must be measured from the artifact a user installs; `bin/qompack[.exe]` there **is** the native binary, so no launcher sits inside the measurement |
| `universal` | `dist/plugin-universal/bin/qompack` (the sh launcher on POSIX, `qompack.cmd` on Windows) | the launcher-overhead figure LAUNCH-P/LAUNCH-W, reported inside **B-D** and never inside B-A |

`native` and `universal` fail with a clear message naming `devtool package` when the bundle is absent — never by silently falling back to `source`, which would report a development number under a shipped budget's name. `devtool bench-hotpath` forwards the flag unchanged (`tools/devtool/benchhotpath.go` already forwards all args verbatim). `TestBenchBundleSelector` asserts each value spawns the expected path and that a missing bundle is an error rather than a fallback.

### 15. `test/replay --live` — wiring §6.3 tier 3

00-ARCHITECTURE §5.18 and §6.3 both name this as SP-17's deliverable: *"Wiring tier 3 is SP-17's deliverable: a `--live` flag on `test/replay` plus the `SetLiveRunner` call that fills the seam."* Wave 1 shipped the seam and no caller — `internal/eval/replay.go` refuses non-deterministic mode twice over (once on the `QOMPACK_EVAL_LIVE` gate, once on the absent runner), `internal/eval/harness.go` has `SetLiveRunner` on the concrete type, and `test/replay/main.go` hardcodes `eval.ReplayOptions{Deterministic: true, …}` with no flag to change it. SP-17 supplies the caller:

- **`--live`** on `test/replay` sets `Deterministic: false`. It refuses to run unless `QOMPACK_EVAL_LIVE=1` **and** `--live-runner` is set, printing which of the two is missing; the gate stays exactly where wave 1 put it and is not relaxed.
- **`--live-runner <command>`** names an operator-supplied executable. The driver reaches `SetLiveRunner` through the type assertion the architecture prescribes — `if s, ok := h.(interface{ SetLiveRunner(eval.LiveRunner) }); ok { s.SetLiveRunner(r) }` — so the §5.18 `Harness` interface gains no method (Rule W-3), and the run aborts with a clear error if the assertion fails.
- **The runner is a subprocess bridge, and that is not a convenience — it is what keeps §13 invariant 7 true.** `Fork` writes one JSON request (`{"session":…, "at":…, "keep":[…], "k":…}`) to the child's stdin and decodes `[]eval.Action` from its stdout, with a per-call deadline and the child's stderr forwarded. **No HTTP client, no model SDK and no `net/*` import enters this module**, so the `test/security/importgraph_test.go` allowlist of §9 holds unchanged and the "No network" proof still covers every package in the repository. The thing that talks to a model is the operator's own command, outside the module, run by hand.
- **The nightly job stays deterministic.** `.github/workflows/nightly.yml`'s `replay-live` job runs the ordinary driver over the recorded corpus (§6.3 tier 2) and is not given `--live`; 00-ARCHITECTURE is explicit that live mode is never run by any workflow. SP-17 changes no workflow here.
- **Exit criterion.** One pre-release live run over the recorded corpus, executed by hand before the tag, its report committed to the release notes as evidence. This is the run 00-ARCHITECTURE §5.18 means by "the release gate runs live mode over the recorded corpus" — a manual gate, discharged once per release, not a job.

Tested by `TestReplayLiveRequiresGateAndRunner` (both refusals, each naming what is missing) and `TestReplayLiveRunnerSubprocess` (a stub runner script on `PATH` that echoes a fixed action list; the driver's report reflects it), both in `test/replay/`.

---

## Test plan (TDD)

Tests are written first in every commit and must fail for the stated reason before implementation.

### Commit 1 — packaging

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestGoreleaserConfigValid` | repo root | `goreleaser check` | exit 0, no deprecation errors. Skips with the message `"goreleaser not on PATH"` when `exec.LookPath("goreleaser")` fails, so the local loop does not require it; CI installs it and therefore always runs it |
| `TestBundleLayoutPerPlatform` | `devtool package --targets all` | each of 6 trees | exactly the 13 entries listed in Implementation spec §5 step 4 (3 manifests + 7 commands + `bin/qompack[.exe]` + `bin/SHA256SUMS` + `LICENSE`), no more and no fewer; `bin/qompack[.exe]` is an ELF/Mach-O/PE for the right GOOS/GOARCH (magic-byte check); modes as specified |
| `TestBundleLayoutUniversal` | same | `dist/plugin-universal/` | 6 binaries + 3 launchers + SHA256SUMS + manifests + LICENSE; nothing else |
| `TestBundleDeterministic` | package twice into two out dirs | both `.tar.gz` and `.zip` | byte-identical sha256 for every archive |
| `TestSHA256SUMSFormat` | universal bundle | `bin/SHA256SUMS` | 9 lines, `^[0-9a-f]{64}  \S+$`, sorted, LF only, each hash matches the file |
| `TestLauncherResolutionTable` | temp dir with stub `uname` on PATH emitting each row of the table, and stub binaries that print their own name | run `qompack.sh foo bar` | correct binary invoked with `foo bar`; unsupported rows print to stderr and exit 0 |
| `TestLauncherPropagatesExitCode` | stub binary exiting 7 | `qompack.sh` | exit 7 |
| `TestLauncherExitsZeroOnMissingBinary` | empty dir | `qompack.sh` | exit 0, one stderr line |
| `TestLauncherCmdArchSelection` | windows only; `PROCESSOR_ARCHITECTURE` set to `AMD64` then `ARM64` | `qompack.cmd` | selects `-amd64.exe` then `-arm64.exe` |
| `TestLauncherPs1ArchSelection` | windows only | `qompack.ps1` | same |
| `TestVersionStamping` | build with the three `-X` flags of §1 set to known values | `qompack version --json` | all seven fields exact; `version` and `plugin_manifest_version` both equal the stamped `core.Version` |
| `TestOneStampedVersionSource` | `grep` the built `.goreleaser.yaml` and `tools/devtool`'s ldflags helper | the `-X` flags they emit | exactly one of them targets a version variable, and it is `github.com/qompack/qompack/internal/core.Version`; no `-X` names `pluginmanifest` at all |
| `TestVerifyVersionRejectsMismatch` | CHANGELOG at 0.2.0, `core.Version` 0.1.0 | `devtool verify-version --tag v0.2.0` | exit 1, all four values printed |
| `TestSetVersionRoundTrip` | clean tree | `devtool set-version 0.9.0` | `internal/core/version.go`, `plugin/.claude-plugin/plugin.json` and `CHANGELOG.md` all updated; `verify-version --tag v0.9.0` exits 0; `plugin-validate` clean |
| `TestBinarySizeBudget` | all 6 binaries | file sizes | linux/amd64 ≤ 20 MiB, others ≤ 24 MiB |
| `TestPackageAcceptsSnapshotVersion` | — | `devtool package --version 0.1.1-dev+abc1234 --skip-build` | accepted: the version regex admits semver build metadata, which is what `snapshot.version_template` emits |
| `TestGoreleaserSnapshotSucceeds` | repo root, `goreleaser` on PATH | `goreleaser release --snapshot --clean` | exit 0 — the `before` hook's `IsSnapshot` guard skips `verify-version`. Skips with `"goreleaser not on PATH"` exactly like `TestGoreleaserConfigValid` |
| `TestBenchBundleSelector` | a packaged `dist/` | `bench-hotpath --bundle native`, `--bundle universal`, `--bundle source`, and `--bundle native` with `dist/` removed | each of the first three spawns the path §14 names; the fourth exits non-zero naming `devtool package`, never falling back to `source` |
| `TestShimOverheadBudget` (bench) | universal bundle, 500 spawns of `qompack version` via the launcher vs. direct | wall-clock deltas | launcher p99 overhead < 4 ms POSIX / < 15 ms Windows (**budget: launcher overhead, reported inside B-D**) |

Fixtures: one stub `uname` per row of the §2 resolution table — `testdata/packaging/stub-uname/{linux-x86_64,linux-aarch64,darwin-x86_64,darwin-arm64,freebsd-amd64,linux-riscv64}.sh`, each echoing its `-s`/`-m` answers — and `testdata/packaging/stub-binary.go` (prints `argv[0]` plus its arguments and exits with `$QP_STUB_EXIT`, default 0). `TestLauncherResolutionTable` is table-driven over exactly those six.

### Commit 2 — platform matrix

Every test named in §10 of the Implementation spec, each with the setup and expectation given there. Additional property test: `TestPathKeyIdempotentUnderUnicode` (rapid) — for any random UTF-8 path, `paths.Key(paths.Key(p)) == paths.Key(p)` and `paths.Key` never returns an absolute path.

Fixtures: `testdata/fixtures/platform/longpath/` (a generator, not committed paths — CI creates them), `testdata/corpora/toolout/crlf-vs-lf/{crlf.txt,lf.txt}` (identical modulo line endings, 40 KiB).

### Commit 3 — security audit

Every test named in §9 of the Implementation spec. The ten `redaction_test.go` cases, one per shipped rule. **The Rule column is the fixture's basename under `testdata/corpora/secrets/`, which is also the rule name inside the placeholder** — the test computes the Expected column from it rather than carrying these strings as literals, so this table is the reader's copy and the fixture set is the source. Every fixture already exists; the only edit is adding the 32-character marker where a fixture does not already carry one.

| Rule (fixture basename) | Fixture content (marker in bold) | Expected |
|---|---|---|
| `pem_private_key` | `@@SEC_PEM_RSA_BEGIN@@\nMII**MARKER0001**…\n@@SEC_PEM_RSA_END@@` | `«redacted:pem_private_key»`, marker absent everywhere |
| `aws_access_key_id` | `AKIA**MARKER0002**XYZ` **and** `ASIA**MARKER0010**XYZ` in the same fixture — one rule matches both prefixes | `«redacted:aws_access_key_id»` twice |
| `github_token` | `ghp_**MARKER0003**` | `«redacted:github_token»` |
| `anthropic_key` | `sk-ant-**MARKER0004**` | `«redacted:anthropic_key»` |
| `generic_sk_key` | `sk-**MARKER0011**` (the `sk-` catch-all beside `anthropic_key`'s `sk-ant-`) | `«redacted:generic_sk_key»` |
| `bearer_token` | `Authorization: Bearer **MARKER0005**` | `«redacted:bearer_token»` |
| `assignment_secret` | `password=**MARKER0006**` | `«redacted:assignment_secret»` |
| `dotenv_value` | `API_KEY=**MARKER0007**` in a `.env` read | `«redacted:dotenv_value»` |
| `jwt` | `eyJhbGciOi….**MARKER0008**.sig` | `«redacted:jwt»` |
| `credentialed_uri` | `postgres://u:**MARKER0009**@h/db` | `«redacted:credentialed_uri»` |

A guard case, `TestRedactionFixturesCoverEveryRule`, asserts three sets are equal: the per-rule fixture basenames, the placeholder names the redactor actually emits across the **whole** directory, and the ten names listed above (carried in the test as a literal, mirroring this table). That catches a rule renamed in `internal/redact`, a fixture whose secret no longer matches its rule, and — because the multi-secret `mixed-*.txt` fixtures are part of the corpus — a rule *added* to `internal/redact` whose placeholder then shows up in the emitted set without a name to match. The three non-rule fixtures (`adversarial-already-redacted.txt`, `mixed-deploy-log.txt`, `mixed-env-dump.txt`) are excluded from the *basename* set by name and exercised separately: the adversarial one asserts re-redaction is a no-op, the two mixed ones assert multi-rule documents lose every secret.

Property test: `TestRedactIdempotentAndNonGrowing` (rapid) — for random inputs, `Redact(Redact(x)) == Redact(x)` and `len(out) ≤ 4*len(in)`. (SP-06 owns the rules; this asserts the property SP-06 promised.)

### Commit 4 — fault injection

`TestHooksExitZero_Matrix` with the 54 cases, plus the nine dedicated tests named in §11. Every case runs the **real binary** (not in-process) so the `cli` panic recovery and exit path are what is measured.

### Commit 5 — fsck and doctor

| Test | Setup | Expected |
|---|---|---|
| `TestFsckCleanStore` | a project with 500 tool uses, 3 checkpoints, 20 eliminations | 0 findings above `FsckInfo`; `OK: true`; exit 0 |
| `TestFsckCleanStoreUncompressed` | the same project built with `store.compression: "none"`, so every object carries the **bare** 64-hex name | 0 findings above `FsckInfo`, and the `objects` check reports a non-zero scanned count — the assertion that catches a `*.zst` glob passing vacuously over a store it never walked |
| `TestFsckObjectHashUsesChunkDomain` | a healthy store; independently recompute one object's name | the recomputation that matches the basename is `core.HashBytes(core.DomainChunk, plaintext)`, and a plain `sha256(plaintext)` does **not** match — pinning the domain so a future "simplification" fails here rather than quarantining a healthy store |
| `TestFsckDetectsCorruptObject` | flip a byte in one object | one `objects` finding, `FsckError`, correct path, `Repaired: false` |
| `TestFsckRepairQuarantines` | same, `--repair` | object moved under `tmp/quarantine/objects/`, `Repaired: true`, `Loud` written |
| `TestFsckOrphanedRootRetired` | delete one object out from under a live root line, leaving `index/roots.jsonl` referencing a chunk that is gone | without `--repair`: one `roots` `FsckError` naming the root and the missing chunk. With `--repair`: a `{"v":1,"op":"gc","root":…,"ts":…}` line is **appended** to `roots.jsonl` (the file's earlier bytes byte-identical); `<quarantine>/roots/<unixms>.orphaned-roots.json` lists the affected `tool_use` IDs; a reopened store no longer reports the root; a second `fsck` run is clean. This is SP-06's D15 residual, discharged |
| `TestFsckGCAll` | a store whose only collectable objects are inside the 30-day / 10-session retention window | plain `--repair` collects nothing; `--repair --gc-all` collects them (`RetainDays: -1, RetainSessions: -1`); `--gc-all` without `--repair` reports the larger figure and deletes nothing |
| `TestFsckTruncatedTailRepair` | append `{"partial":` to `dag/deps.jsonl` | without `--repair`: one `jsonl-tails` error; with: file truncated to the last `\n`, quarantined tail byte-identical |
| `TestFsckTailRepairCoversFilesAndSessions` | append `{"partial":` to `index/files.jsonl` and to `index/sessions.jsonl` | both are in check 4's list: two `jsonl-tails` errors, both repaired, both quarantined tails byte-identical, and `store.Open` succeeds afterwards. Without these two entries a torn tail in either file is never detected — SP-06 declared them precisely so SP-14 and SP-17 could enumerate the write set |
| `TestFsckManifestMismatch` | rewrite `checkpoints/0002.json` by one byte (after clearing read-only) | `checkpoints` error naming seq 2; `--repair` quarantines it; `checkpoint.Reader.Latest` then returns seq 1 |
| `TestFsckManifestGapRepair` | delete the MANIFEST row for 0003 | `manifest` error; `--repair` re-appends a row whose sha256 matches the file |
| `TestFsckNewerCheckpointVersionQuarantined` | hand-written `0004.json` with `"version": 2` | `checkpoints` error `"schema version 2 > supported 1"`; `--repair` quarantines; parent still readable |
| `TestFsckBloomRebuild` | zero the bloom CRC | `bloom` error; `--repair` calls `RebuildBloom`; the rebuilt filter tests positive for every **active** record and negative for a known-stale one |
| `TestFsckSketchRecreate` | corrupt `touch.cms` | quarantined and recreated empty with the configured epsilon/delta |
| `TestFsckStaleLockRemoved` | lock file with pid 999999 | `locks` warn; `--repair` deletes it |
| `TestFsckReadOnlyRestored` | chmod a checkpoint to 0644 | `readonly` warn; `--repair` restores 0444 |
| `TestFsckDeadlineResumable` | 200 000-object store, `--deadline 200ms` | returns with `Truncated: true` inside 400 ms; a second run with a generous deadline completes |
| `TestFsckExitsZeroByDefault` | corrupt store | exit 0 |
| `TestFsckStrictExitsNonZero` | same, `--strict` | exit 1 |
| `TestFsckJSONShape` | any run with `--json` | output unmarshals into `FsckReport`; `Counts` keys are exactly `{"ok","info","warn","error"}` |
| `BenchmarkFsckLargeStore` | 200 000 objects / 2 GiB | < 60 s wall (**budget: fsck full pass**) |
| `TestDoctorAllChecksPresent` | healthy project + live daemon | all 16 `Check.ID`s present, in the documented order; on non-Windows both `launcher` and `defender` report `CheckInfo` with `Observed: "n/a"` rather than being omitted |
| `TestDoctorDaemonDown` | no daemon | `daemon` WARN with the remedy line; exit 0; other checks still run |
| `TestDoctorReportsDegradedMode` | forced `SevCritical` contract failure | `contract` FAIL naming the assertion, expected and observed |
| `TestDoctorBloomThresholds` | bloom at 6% and at 12% est FP | WARN then FAIL, quoting §11.4 |
| `TestDoctorMCPProbe` | real binary | `mcp` PASS with `observed: "8 tools"` |
| `TestDoctorSkipMCP` | `--skip-mcp` | `mcp` INFO `"skipped"`; total wall < 3 s |
| `TestDoctorJSONShape` | `--json` | unmarshals into `DoctorReport`; `Build` matches `qompack version --json` |
| `TestDoctorDefenderCheckWindows` | Windows | check runs, completes within 1500 ms, never fails the process when PowerShell is missing |

### Commit 6 — hardening

One test per §12.3 row, named `TestDegradation_DaemonUnreachable`, `_SpoolWriteFails`, `_StoreCorrupt`, `_ManifestMismatch`, `_BloomLoadFails`, `_ConfigInvalid`, `_MCPToolPanic`, `_HookPanic`, `_PreCompactTimeout`. Each drives the real failure, asserts the documented response verbatim (including that a `Loud` line was written and that `/qompack:status` would surface it — asserted through `contract.Monitor.Report()` and `obs.Registry.Snapshot()`, not by re-rendering status), and asserts the recovery paragraph in `docs/security.md` matches by referencing the same finding string.

`TestPreCompactTimeoutFinalizesDraft`: drive `qompack checkpoint` with a draft large enough that full finalization would exceed 2 s, assert the emitted checkpoint is truncated tier-3-first (tier 1 intact, `dropped[]` populated), the hook exits 0 within budget **B-E p99 < 2 s**, and `checkpoint.Reader.Get` reads it back.

### Commit 7 — release pipeline

| Test | Setup | Expected |
|---|---|---|
| `TestChangelogGeneration` | a fixture git repo with 12 commits spanning every type incl. one `feat!` | exact expected markdown, golden-compared |
| `TestChangelogCheckDetectsDrift` | delete one line from CHANGELOG | `--check` exits 1 naming the missing entry |
| `TestChangelogOmitsChore` | chore/ci/test commits only | `## [Unreleased]` with `_No user-facing changes._` |
| `TestGuardRejectsTagOffMain` | fixture git repo, tag placed on a feature branch, stub `gh` on `PATH` | `devtool release-guard --tag v9.9.9` exits 1 and prints the `--contains` failure |
| `TestGuardRejectsExistingRelease` | fixture repo, tag on `main`, stub `gh release view --json isDraft` printing `false` | `devtool release-guard` exits 1 naming the already-published release |
| `TestGuardAllowsExistingDraft` | same, but the stub prints `true` | exit 0 — a draft left behind by a failed mid-pipeline run is re-runnable, and the immutable-tag policy still protects a published one |
| `TestGuardAcceptsCleanTag` | fixture repo, tag on `main`, stub `gh release view` exiting 1 (not found), versions aligned | exit 0, no output on stderr |
| `TestWorkflowsLint` | `actionlint` over `.github/workflows/*.yml` | exit 0. Skips with the message `"actionlint not on PATH"` when `exec.LookPath("actionlint")` fails, exactly like `TestGoreleaserConfigValid`; CI installs it and therefore always runs it |
| `TestReleaseTriggerExcludesWaveTags` | parse `.github/workflows/release.yml`'s `on.push.tags` | the pattern matches `v1.0.0` and `v2.13.4`; it does **not** match `v0.0.1`, `v0.1.0`, `v0.5.1` or any other `v0.<wave>.<n>` development tag 00-ARCHITECTURE §9 puts on `develop` |
| `TestReleaseWorkflowTransfersDist` | parse `.github/workflows/release.yml` | `package` has an `actions/upload-artifact` step whose `path` covers `dist/checksums.txt` and both `dist/qompack-plugin-*` globs, and `attest`'s **first** step is an `actions/download-artifact` of the same artifact name into `dist` — the attestation cannot succeed otherwise |
| `TestReleaseWorkflowPreservesSP01Wiring` | parse `.github/workflows/release.yml` | every job that builds or publishes checks out with `fetch-depth: 0`; `setup-go` is pinned to an explicit `go-version`; the `ci-local` gate step survives; `goreleaser-action` is present with `args: release --clean` and `GITHUB_TOKEN`. This is a regression test against "replace the placeholder" being read as "start from an empty file" |
| `TestReleaseArtifactManifest` | `devtool package --json` | the `artifacts` array has exactly 10 entries — 6 per-platform `.tar.gz`, 2 windows `.zip`, universal `.tar.gz` and universal `.zip` — and every one has a matching `<name>.sha256` file whose 64-hex content equals the recomputed digest of the archive |
| `TestReplayLiveRequiresGateAndRunner` | `test/replay --live` with (a) no `QOMPACK_EVAL_LIVE`, (b) the gate set but no `--live-runner` | each exits non-zero naming exactly what is missing; neither ever falls back to deterministic mode |
| `TestReplayLiveRunnerSubprocess` | `--live --live-runner <stub>` with `QOMPACK_EVAL_LIVE=1`, the stub echoing a fixed `[]Action` list | the driver's report reflects the stub's actions; the stub received one JSON request per fork on stdin |

### Commit 8 — install, upgrade, uninstall

| Test | Setup | Expected |
|---|---|---|
| `TestInstallHookCommandStrings` | extract a bundle to `<tmp>/plugin`, set `CLAUDE_PLUGIN_ROOT`, read `hooks/hooks.json`, expand `${CLAUDE_PLUGIN_ROOT}` and run **each of the seven command strings** (the seven `hooks.json` entries of 00-ARCHITECTURE §3.4, covering the six hook subcommands — `observe stop` appears twice, once with `--subagent`) through the platform shell with a realistic payload on stdin | all exit 0 within their manifest `timeout` (5/5/15/20/5/10/20 s); stdout empty or valid `hookio.Output`; `.qompack/` created in the project |
| `TestInstallMCPRegistration` | spawn `.mcp.json`'s command+args | `initialize` → `tools/list` returns exactly the 8 §8.7 tool names |
| `TestInstallAllSevenCommands` | parse each `commands/*.md` frontmatter and extract the `qompack <sub> …` invocation **exactly as written there** — that string is what Claude Code will run, so it, and not an invented flag set, is what must be exercised | each invocation exits 0 within 10 s and writes non-empty stdout; where the frontmatter's own invocation passes `--json`, stdout must additionally unmarshal into `map[string]any`; the seven command names equal the §7.5 list `status, recall, pin, checkpoint, why, dropped, eval`. **Adding `--json` to a command that does not have it is out of scope (SP-14 owns command output, see Out of scope)** — if a command needs an argument to be meaningful the frontmatter supplies it, and if it does not, that is an SP-14 defect this test reports rather than papers over |
| `TestUninstallLeavesNothing` | snapshot the project tree (path→sha256) before install; install, run a session, delete the plugin root, delete `<project>/.qompack/` and `<home>/.qompack/` | the tree is byte-identical to the pre-install snapshot; zero residual files anywhere under the four temp roots |
| `TestUpgradeAcrossVersions` | install `0.1.0`, run a session producing ≥ 2 checkpoints and ≥ 5 eliminations, install `0.2.0` over it, run another session | old daemon lock reclaimed; store opens; both checkpoints listed and verified; eliminations intact; `fsck --strict` exit 0; `doctor` reports `0.2.0` |
| `TestUpgradeCheckpointSchemaBump` | drop a hand-written `version: 2` checkpoint into the upgraded store | `fsck --repair` quarantines it; `checkpoint.Reader.Latest` returns the highest supported seq; the session runs; `doctor`'s `checkpoints` check FAILs with the "upgrade qompack" remedy before the repair and PASSes after |
| `TestFreshProjectSelfIgnores` | fresh project with no `.gitignore` | after the first hook, `.qompack/.gitignore` exists containing `*`; `git status --porcelain` shows nothing |

---

## Commit plan

Exactly **8 commits**, in order. Each compiles, each passes `go run ./tools/devtool test` for the packages it touches, and each is committed only after its own tests are green.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `build(packaging): goreleaser six-target build, launcher shims and the deterministic bundle assembler`

- [ ] Cut the branch: `git checkout develop && git pull && git checkout -b feat/sp17-packaging-hardening-and-release`
- [ ] Write failing tests first: `test/install/bundle_test.go` (`TestBundleLayoutPerPlatform`, `TestBundleLayoutUniversal`, `TestBundleDeterministic`, `TestSHA256SUMSFormat`, `TestBinarySizeBudget`, `TestPackageAcceptsSnapshotVersion`, `TestPluginValidateNewAssertions`), `test/install/launcher_test.go` (`TestLauncherResolutionTable`, `TestLauncherPropagatesExitCode`, `TestLauncherExitsZeroOnMissingBinary`, `TestLauncherCmdArchSelection`, `TestLauncherPs1ArchSelection`, `TestShimOverheadBudget`, `TestBenchBundleSelector`), `internal/cli/version_test.go` (`TestVersionStamping`), `tools/devtool/version_test.go` (`TestVerifyVersionRejectsMismatch`, `TestSetVersionRoundTrip`, `TestOneStampedVersionSource`), `test/install/goreleaser_test.go` (`TestGoreleaserConfigValid`, `TestGoreleaserSnapshotSucceeds`)
- [ ] Run `go test ./test/install/... ./internal/cli/... ./tools/devtool/...` — must fail with "devtool: unknown task package" and missing symbols
- [ ] Add `packaging/launcher/{qompack.sh,qompack.cmd,qompack.ps1}` and `testdata/packaging/{stub-uname/*,stub-binary.go}`
- [ ] **Do not add a version variable.** `internal/core/version.go` already declares the one stamped source and the whole tree already reads it. Confirm with `go run ./tools/devtool plugin-validate` that `plugin.json` matches `core.Version`, and move on
- [ ] Add the initial `CHANGELOG.md` — keep-a-changelog header, an empty `## [Unreleased]`, and a `## [0.1.0] - <today>` heading with a single `### Added` line. **It must exist in commit 1**, because `verify-version` compares the tag against its first `## [x.y.z]` heading and `goreleaser`'s `archives.files` lists it; commit 7 adds the generator and regenerates the body from history, it does not create the file
- [ ] Add `internal/cli/version.go` (`buildCommit`/`buildDate`, `BuildInfo`, `Version`, `RunVersion`) and point the **existing** `version` command's `Run` at `RunVersion`; do not register a second one
- [ ] Add `tools/devtool/package.go` and `tools/devtool/version.go`; register tasks `package`, `verify-version`, `set-version`; extend `versionLdflags` in `tools/devtool/util.go` with the two provenance `-X` flags
- [ ] Add `--bundle native|universal|source` to `test/bench/hotpath` (§14), defaulting to `source` so every committed `bench-hotpath` line keeps its current meaning
- [ ] Extend `.goreleaser.yaml` to the six-target configuration of §1, **keeping** the `-X …/internal/core.Version={{ .Version }}` flag it already carries and gating the `before` hook on `IsSnapshot`
- [ ] Run `go test ./test/install/... -count=1` on this machine, `devtool package --json`, and `goreleaser release --snapshot --clean` — all green
- [ ] Commit body footer: `Refs: SP-17, G9.2, §7.5`

### Commit 2 — `test(platform): cross-platform matrix for paths, filesystems, daemons and Defender`

- [ ] Write `test/platform/{paths_test.go,fs_test.go,daemon_test.go,perms_test.go,defender_windows_test.go}` with every test named in the plan — all failing or skipped-for-the-right-reason
- [ ] Run `go test ./test/platform/... -count=1` — must fail (long-path, case-collision and concurrent-start cases first)
- [ ] Add `tools/devtool/platform.go` (task `platform-matrix`)
- [ ] Fix only what the tests prove broken, in the smallest possible diff, listing each under `Hardening:` with the owning subplan named
- [ ] Add the `platform-matrix` job to `.github/workflows/ci.yml`
- [ ] Run `go test ./test/platform/... -count=1` on Windows and (via CI on the pushed branch) on ubuntu and macos — all green
- [ ] Commit body footer: `Refs: SP-17, §12, 00-ARCHITECTURE §3.3 §6.2`

### Commit 3 — `test(security): no-network, no-telemetry, confined write set and redaction verification`

- [ ] Write `test/security/{importgraph_test.go,network_test.go,writeset_test.go,telemetry_test.go,redaction_test.go,panic_test.go,alloc_test.go,tooling_test.go}`. **Reuse the existing `testdata/corpora/secrets/*.txt` fixtures** — one per rule plus three multi-rule/adversarial files, already named after the real rule names — adding the 32-character marker only where a fixture does not already carry one. Do not write ten new fixtures and do not rename the existing ones; their basenames are what the test reads its expected placeholders from
- [ ] Run `go test ./test/security/... -count=1` — must fail on `telemetry` (not yet hardwired) and on any allocation bound not yet enforced
- [ ] Hardwire telemetry off at the end of `config.Load` with a `Warning` (four lines, `internal/config/load.go`), recorded in the commit body as `Hardening: internal/config (SP-01) — telemetry hardwire per 00-ARCHITECTURE §11.5`
- [ ] Add `.gosec.json` with reasoned suppressions; add `tools/devtool/security.go` (task `security-audit`)
- [ ] Write the audit half of `docs/security.md` — the first seven `##` sections of the table in **Implementation spec §12**: `Threat model in one paragraph`, `No network`, `No telemetry`, `Confined write set`, `Redaction`, `File permissions`, `Dependency posture`
- [ ] Extend the `security` job in `ci.yml` with `go test ./test/security/...` and `dependency-review-action`
- [ ] Run `go test ./test/security/... -count=1` and `devtool security-audit` — all green, `govulncheck` and `gosec` clean
- [ ] Commit body footer: `Refs: SP-17, §12, 00-ARCHITECTURE §13`

### Commit 4 — `test(fault): nine failure modes against six hooks, every one exiting 0`

- [ ] Write `test/fault/{matrix_test.go,inject.go,recovery_test.go}` with `TestHooksExitZero_Matrix` (54 cases) and the nine dedicated tests
- [ ] Run `go test ./test/fault/... -count=1` — must fail on at least the `write-fails` and `oversized-payload` rows
- [ ] Add `tools/devtool/fault.go` (task `fault-inject`) and the `fault-gate` CI job
- [ ] Fix each proven defect minimally, listing it under `Hardening:`
- [ ] Run `go test ./test/fault/... -count=1 -race` — green on ubuntu/macos, `-count=2` on Windows
- [ ] Commit body footer: `Refs: SP-17, §12, 00-ARCHITECTURE §2.3 §12.3`

### Commit 5 — `feat(cli): qompack fsck and qompack doctor`

- [ ] Write `internal/cli/fsck_test.go` and `internal/cli/doctor_test.go` with every test in the plan, plus `BenchmarkFsckLargeStore`
- [ ] Run `go test ./internal/cli/... -run 'Fsck|Doctor'` — must fail with `not implemented`
- [ ] Add `internal/cli/fsck.go` (13 checks, `FsckOptions`/`Finding`/`FsckReport`, `--gc-all`, human + `--json` rendering). Check 2 hashes with `core.HashBytes(core.DomainChunk, …)` and globs `objects/??/??/*`; check 3's `--repair` appends the retirement record; check 4's list includes `index/files.jsonl` and `index/sessions.jsonl`
- [ ] Add `internal/cli/doctor.go` (16 checks, `DoctorOptions`/`Check`/`DoctorReport`)
- [ ] Replace the `fsck` and `doctor` entries in `internal/cli/commands.go`'s `notImplemented` table with real `Cmd` registrations — they are entries in that table today, not absent, so this is an edit rather than an addition; keep hot-path subcommands free of any new init
- [ ] Run `go test ./internal/cli/... -count=1` and `go test -bench=BenchmarkFsckLargeStore ./internal/cli/` — green and within the 60 s budget
- [ ] Commit body footer: `Refs: SP-17, §8.2, §8.3, §11.4, 00-ARCHITECTURE §3.3 §12.3`

### Commit 6 — `fix(hardening): a driven failure and a documented recovery for every §12 degradation path`

- [ ] Write `test/fault/degradation_test.go` with the nine `TestDegradation_*` tests and `TestPreCompactTimeoutFinalizesDraft`
- [ ] Run `go test ./test/fault/... -run Degradation -count=1` — must fail on at least the MCP-panic and PreCompact-timeout rows
- [ ] Land the three **new** allocation bounds of `alloc_test.go` rows 4–6 — `recall` k clamp to 100 and `expand` span clamp to `runtime.mcp.maxResponseBytes` (both `Hardening: internal/mcp (SP-13)`), and the 64 MiB `zstd.WithDecoderMaxMemory` cap (`Hardening: internal/store (SP-06)`) — and assert rows 1–3 (`hookio` read limit, IPC frame limit, stdlib JSON depth), changing no code where the bound already holds
- [ ] Land the minimal fixes each degradation test proves necessary
- [ ] Write the recovery half of `docs/security.md` — the last four `##` sections of the table in **Implementation spec §12**: `Degradation and recovery` (one paragraph per Qompack.md §12.3 row), `What this plugin cannot do` (the Qompack.md §12 list verbatim), `What to do if a secret reached the store`, `Reporting a vulnerability`. Add `TestSecurityDocSections` in `test/install/` asserting all eleven headings in order
- [ ] Run `go test ./test/fault/... ./test/security/... -count=1` — all green
- [ ] Commit body footer: `Refs: SP-17, §12, 00-ARCHITECTURE §12.3 §13`

### Commit 7 — `ci(release): tag-triggered pipeline, version stamping, CHANGELOG generation and rollback`

- [ ] Write `tools/devtool/changelog_test.go` (`TestChangelogGeneration`, `TestChangelogCheckDetectsDrift`, `TestChangelogOmitsChore`) with a fixture git repo, and `test/install/workflow_test.go` (`TestGuardRejectsTagOffMain`, `TestGuardRejectsExistingRelease`, `TestGuardAllowsExistingDraft`, `TestGuardAcceptsCleanTag`, `TestWorkflowsLint`, `TestReleaseTriggerExcludesWaveTags`, `TestReleaseWorkflowTransfersDist`, `TestReleaseWorkflowPreservesSP01Wiring`, `TestReleaseArtifactManifest`), and `test/replay/live_test.go` (`TestReplayLiveRequiresGateAndRunner`, `TestReplayLiveRunnerSubprocess`)
- [ ] Run `go test ./tools/devtool/... ./test/install/... ./test/replay/... -count=1` — must fail with "unknown task changelog" and "unknown task release-guard"
- [ ] Add `tools/devtool/changelog.go` (task `changelog`, `--from/--to/--check`) and `tools/devtool/guard.go` (task `release-guard`, scoping the already-exists check to **published** releases via `gh release view <tag> --json isDraft`)
- [ ] Add `--live` and `--live-runner` to `test/replay` with the `SetLiveRunner` type-assertion wiring of §15
- [ ] Regenerate the `## [0.1.0]` body of the `CHANGELOG.md` created in commit 1 from the repo's history to date; leave `## [Unreleased]` empty
- [ ] **Extend** `.github/workflows/release.yml` into the seven jobs in the stated `needs:` order, preserving every piece of the existing wiring §"Two start-state facts" enumerates (`fetch-depth: 0`, the pinned `setup-go`, the `ci-local` gate, `goreleaser-action` + `GITHUB_TOKEN`, the three `permissions:` grants); narrow `on.push.tags` to `['v[1-9]*.*.*']`; add the `upload-artifact`/`download-artifact` pair that carries `dist/` from `package` to `attest`; add the `package-gate` job to `ci.yml`
- [ ] Write `docs/release.md` (pipeline, the tag-namespace warning, determinism, attestation + `gh attestation verify`, rollback, upgrade path, Deferred: signing and marketplaces)
- [ ] Run `actionlint .github/workflows/*.yml`, `devtool changelog --check`, `devtool verify-version --tag v0.1.0` — all clean
- [ ] Commit body footer: `Refs: SP-17, G9.2, §7.5, 00-ARCHITECTURE §8 §9`

### Commit 8 — `test(e2e): install, upgrade and uninstall validation on all three platforms`

- [ ] Write `test/install/{install_test.go,upgrade_test.go,uninstall_test.go}` with the seven tests in the plan
- [ ] Run `go test ./test/install/... -count=1` — must fail (no `install-check` task yet)
- [ ] Add `tools/devtool/install.go` (tasks `install-check`, `uninstall-check`)
- [ ] Add the `install-gate` job to `ci.yml` and reference it from `release.yml`
- [ ] Write `docs/install.md` (both flavours, checksum verification, the Windows per-platform recommendation, uninstall)
- [ ] Run `devtool ci-local` end to end, then push and confirm all CI jobs green on ubuntu, macos and windows
- [ ] Commit body footer: `Refs: SP-17, G9.2, §7.5, §12`

**Merge.** `git checkout develop && git merge --no-ff feat/sp17-packaging-hardening-and-release`. SP-17 merges **before** SP-18 in wave 5.

---

## Subagent strategy

This subplan is **heavy** and its work partitions cleanly along directory lines: four of the eight commits are test suites over disjoint directories that share no Go package. Partition as follows, keeping the commit sequence strictly serial in the main session.

**Main session keeps, and never delegates:**

- Cutting the branch, every `git commit`, and the commit ordering.
- `.goreleaser.yaml`, `.github/workflows/release.yml`, and the `ci.yml` job additions — one file, four contributors, guaranteed conflicts otherwise.
- Every hardening fix inside a package owned by another subplan. Subagents *report* defects with a failing test; the main session decides the minimal fix, because only it holds the whole picture of what wave-5 is allowed to touch.
- `internal/config` (the four-line telemetry hardwire) and `internal/cli` dispatch wiring.
- Final `docs/security.md` assembly (two subagents produce halves; the main session merges them so the audit findings and the recovery paragraphs stay consistent).

**Subagent A — packaging (commit 1).** Files: `packaging/launcher/*`, `tools/devtool/{package.go,version.go,util.go}`, `internal/cli/version.go`, `test/bench/hotpath/main.go` (the `--bundle` flag only), `test/install/{bundle_test.go,launcher_test.go,goreleaser_test.go}`, `testdata/packaging/**`, and the **skeleton** `CHANGELOG.md` (header + empty `## [Unreleased]` + `## [0.1.0]` heading) that `verify-version` needs in commit 1; subagent F later regenerates its body and must not recreate the file. Returns: the file set, the `devtool package --json` output for a local run, the six measured binary sizes, and the measured launcher overhead p99 per platform it could run on.

**Subagent B — platform matrix (commit 2).** Files: `test/platform/**`, `tools/devtool/platform.go`. Returns: the file set plus a defect list — for each failing case, the failing test name, the package at fault, the owning subplan, and the smallest fix it believes is correct (as a diff proposal, **not** applied).

**Subagent C — security audit (commit 3).** Files: `test/security/**`, `testdata/corpora/secrets/**` (**marker additions to the existing fixtures only** — no new fixtures, no renames), `.gosec.json`, `tools/devtool/security.go`, and the *audit half* of `docs/security.md` (its first seven `##` sections). Returns: the file set, the `go list -deps` allowlist it derived per `GOOS`, the gosec/govulncheck output, and any defect list in the same shape as B.

**Subagent D — fault injection (commit 4) and degradation (commit 6 tests).** Files: `test/fault/**`, `tools/devtool/fault.go`, and the *recovery half* of `docs/security.md` (its last four `##` sections). Returns: the 54-case matrix result table, the nine degradation results, and a defect list.

**Subagent E — fsck and doctor (commit 5).** Files: `internal/cli/{fsck.go,fsck_test.go,doctor.go,doctor_test.go}`. This is the only subagent writing production Go in a package another subagent also touches (`internal/cli/version.go` from A), so it runs **after** A's output is committed. Returns: the two files, the benchmark number, and the JSON shapes of both reports.

**Subagent F — release tooling (commit 7 non-workflow parts) and install e2e (commit 8).** Files: `tools/devtool/{changelog.go,guard.go,install.go}`, the regenerated body of `CHANGELOG.md` (subagent A owns its creation), `test/replay/{main.go,live_test.go}` (the `--live`/`--live-runner` wiring of §15), `test/install/{install_test.go,upgrade_test.go,uninstall_test.go,workflow_test.go}`, `docs/release.md`, `docs/install.md`. Returns: the file set and the generated CHANGELOG for review.

**Parallelism plan.** A, B, C, D, F launch together (five parallel agents; their file sets are disjoint). E launches when A's commit lands. The main session integrates in commit order 1→8, running each commit's tests itself before committing — a subagent's "tests pass" is evidence, not authority (00-ARCHITECTURE §13 invariant 10's spirit: nothing silent). Every defect list from B, C and D is triaged in one pass before commit 6 so the hardening commit is a single coherent diff rather than six scattered patches.

**Integration rule.** If two subagents both want to change a file outside their assigned set, neither does: they report it, and the main session makes the change once. The commit plan stays sequential regardless of how the work was produced.

---

## Exit criteria

### Quoted verbatim from `Qompack.md`

The gap this slice closes, §9:

> | G9.2 hand-rebuilt layer | This plugin *is* the layer, packaged | — |

The guardrails that must still hold at the packaged artifact, §11.3:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

The hot-path budget the launcher must not consume, §8.1:

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. … If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

The bloom watch-for `doctor` enforces, §11.4:

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

The store criterion `doctor` reports against, §10 Phase 1:

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions … hook p99 < 15ms.

### Local, measurable Definition of Done

1. `devtool package` produces six per-platform bundle trees, one universal bundle tree, the resulting ten archives with matching `.sha256` files, and byte-identical archives across two consecutive runs.
2. Every release binary is under budget: linux/amd64 ≤ 20 MiB, all others ≤ 24 MiB.
3. Universal-bundle launcher overhead p99 < 4 ms on POSIX and < 15 ms on Windows over 500 spawns, reported inside **B-D**; and **B-A p99 < 15 ms** and **B-E p99 < 2 s** on all three platforms with the **per-platform** bundle installed (its `bin/qompack[.exe]` is the native binary, so no launcher sits in the measurement), including the un-excluded Defender arm on Windows.
4. `install-gate` is green on ubuntu-latest, macos-latest and windows-latest: all seven `hooks.json` command strings exit 0 through the platform shell within their manifest timeouts, `.mcp.json` handshakes and lists exactly 8 tools, and all seven `commands/*.md` invocations exit 0 with non-empty stdout (parseable JSON wherever the frontmatter itself passes `--json`).
5. `TestUninstallLeavesNothing` proves the project tree is byte-identical to its pre-install snapshot once the plugin root, `<project>/.qompack/` and `~/.qompack/` are removed.
6. `TestUpgradeAcrossVersions` and `TestUpgradeCheckpointSchemaBump` are green: no data loss across a version bump, and a newer checkpoint schema is quarantined with the parent still usable.
7. All 54 cases of `TestHooksExitZero_Matrix` exit 0, with empty-or-valid stdout and no stack trace on stderr.
8. `test/security` is green: the import-graph allowlist holds under all three `GOOS` values; the network canary accepts zero connections; the linux socket inventory shows only Unix sockets; the write set is confined to the four documented prefixes; telemetry is `false` from every config layer; none of the eleven secret markers appears in any byte under `.qompack/`, and every one of the ten shipped `internal/redact` rules has a fixture; every MCP handler survives a panic and six malformed argument shapes; all six allocation bounds hold.
9. `gosec` and `govulncheck` exit 0 with ≤ 6 suppressions, each carrying a written reason.
10. Every row of the §12.3 table has a driven-failure test and a recovery paragraph in `docs/security.md`.
11. `qompack fsck --strict` exits 0 on a clean store — including a store built with `store.compression: "none"`, whose objects carry bare 64-hex names — exits 1 on each of the ten seeded corruptions (the nine originals plus the orphaned root of check 3), and repairs all ten with `--repair`; the full pass over a 200 000-object store completes in < 60 s and is resumable under a short deadline.
12. `qompack doctor` runs all 16 checks in < 3 s with `--skip-mcp` and < 8 s with the MCP probe, in both human and `--json` modes.
13. `devtool verify-version` and `devtool changelog --check` are clean; `actionlint` is clean; a dry-run `goreleaser release --snapshot --clean` succeeds (the `before` hook's `IsSnapshot` guard is what makes that possible, and `devtool package` accepts the `+<sha>` version it produces).
14. Full CI green on the branch — all **fourteen** jobs: the ten `ci.yml` already carries (`verify`, `lint-windows`, `test`, `cover` with floors held, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`) plus the four this slice adds (`package-gate`, `platform-matrix`, `fault-gate`, `install-gate`).
15. `gofumpt -l` empty, `golangci-lint run` clean, `nomagic` clean, coverage floors unchanged (`internal/cli` is in the 75% group and stays above it).
16. Exactly one stamped version source: `grep -R -- '-X ' .goreleaser.yaml tools/devtool/` names `internal/core.Version` and no other version variable, and `pluginmanifest.DefaultVersion` exists nowhere in the tree.
17. **The pre-release live run.** One manual `test/replay --live --live-runner <cmd>` over the recorded corpus, with `QOMPACK_EVAL_LIVE=1`, executed before the release tag and its report attached to the release notes. This is the tier-3 run 00-ARCHITECTURE §5.18 assigns to this slice; it is a hand-run gate, and no workflow runs live mode.

---

## Done checklist

- [ ] Branch `feat/sp17-packaging-hardening-and-release` cut from a post-V5 `develop` with SP-01, SP-05, SP-06, SP-10, SP-11, SP-12, SP-13, SP-14, SP-15 and SP-16 merged.
- [ ] Exactly 8 commits, in the stated order, each with its conventional-commit message and `Refs:` footer.
- [ ] **No commit, merge commit, tag message or PR body contains `Co-Authored-By`, `Signed-off-by`, `Generated with`, or `🤖`.**
- [ ] TDD honoured: in each commit the enumerated tests were written and run failing before implementation.
- [ ] Spec coverage self-review against the Design context section: §7.5 manifest (bundle assembler + `install-gate`), §9 G9.2 row (packaged, installable, verified artifact), §12 risk table (every plugin-actionable row driven and documented), §12 "cannot do" list (reproduced verbatim in `docs/security.md`), §7.4 layout and append-only invariant (`fsck` checks 1, 4, 5, 6, 7, 8), §8.1 budget (launcher overhead + B-A), §8.2 retention (`fsck` check 13), §8.3 bloom-as-cache (`fsck` check 9), §11.3 guardrails (exit criteria 3, 14), §11.4 bloom thresholds (`doctor` check 12), §10 Phase 1 ratio (`doctor` check 11), Appendix A bloom sizing (printed by `doctor`), Appendix C keys (read by both commands).
- [ ] Placeholder scan over the artifacts this slice produces: `grep -rniE "TBD|TODO|FIXME|XXX|implement appropriately|add error handling|handle edge cases" packaging/ tools/devtool/ internal/cli/fsck.go internal/cli/doctor.go internal/cli/version.go test/platform/ test/security/ test/fault/ test/install/ test/replay/ test/bench/hotpath/ docs/security.md docs/release.md docs/install.md .goreleaser.yaml .github/workflows/release.yml CHANGELOG.md` returns nothing.
- [ ] Type consistency with the Interface contract: `FsckOptions`/`Finding`/`FsckReport`/`RunFsck` and `DoctorOptions`/`Check`/`DoctorReport`/`RunDoctor` and `BuildInfo`/`Version`/`RunVersion`/`SupportedCheckpointVersions` match the signatures declared above exactly; every one of them lives in `internal/cli`, the composition root §3.2 permits this slice to extend; **no new exported symbol was added in any other package** — `internal/core.Version` and `internal/pluginmanifest`'s existing accessors are consumed as they stand; no §5 interface was changed, added to, or removed.
- [ ] Scope discipline: no file under `internal/{observer,store,chunk,canon,symbols,sketch,dag,negknow,grammar,analyzer,scheduler,checkpoint,pins,rehydrate,rules,skills,mcp,commands,eval}` was modified except as a `Hardening:`-tagged minimal fix proven by a test in this slice, with the owning subplan named in the commit body.
- [ ] `Qompack.md` is unmodified (`git diff develop --stat -- Qompack.md` is empty).
- [ ] `plans/00-ARCHITECTURE.md` is unmodified; no amendment was required.
- [ ] Wave-5 file disjointness verified against SP-18: SP-17 touched `CHANGELOG.md`, `docs/security.md`, `docs/release.md`, `docs/install.md` and none of SP-18's seven files. **`docs/security.md` is SP-17's alone** — confirm SP-18's merged branch neither writes it nor lists it in `OwnedDocs()`, and that `docs/security.md` carries all eleven `##` sections of Implementation spec §12 so nothing SP-18 links to is missing.
- [ ] All four new CI jobs (`package-gate`, `platform-matrix`, `fault-gate`, `install-gate`) are configured as required checks on `develop` and `main`, bringing `ci.yml` to fourteen jobs.
- [ ] `docs/security.md` §"What this plugin cannot do" is byte-identical to the §12 list quoted in this plan.
- [ ] The release workflow still carries every piece of SP-01's wiring it started with: `fetch-depth: 0`, a pinned `setup-go` version, the `ci-local` gate, `goreleaser-action` with `args: release --clean` and `GITHUB_TOKEN`, and the three `permissions:` grants. Its `on.push.tags` matches no `v0.*` development tag.
- [ ] SP-06's three residuals are discharged, not inherited: check 4 lists `index/files.jsonl` and `index/sessions.jsonl`; `--gc-all` exists and drives both retention axes negative; check 3's `--repair` retires an unrecoverable root by appending a retirement record and names the affected `tool_use` IDs (SP-06 D15).
- [ ] A dry-run release (`goreleaser release --snapshot --clean` + `devtool package`) was executed locally and its artifact list matches `docs/release.md`.
- [ ] The pre-release live run of DoD item 17 was executed and its report is attached to the release notes.

# SP-05: L0 substrate: resident daemon, IPC transport, thin hook client, the <15ms p99 budget, async queue-and-drain, and the contract monitor

> **Recommended model: Fable 5 · high effort**
>
> **Pay up here.** Resident daemon, WAL-backed ingest queue, worker pool, spool fallback, idle controller and a Windows-named-pipe/Unix-socket transport split, all under a hard 15 ms p99 — concurrency and cross-platform faults are precisely the failure class that passes tests and breaks in production. `high` (not `xhigh`) because the plan is exhaustively specified; you are buying judgment, not extra exploration.

**Branch:** `feat/sp05-daemon-ipc-and-hot-path` (cut from `develop`) | **Wave:** 1 | **Prerequisites:** the branches of `["SP-01"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 1 (SP-02, SP-03, SP-04, SP-06, SP-07) | **Design sections:** §7.1, §8.1 (performance budget), §9 (G9.3 row), §12 (contract monitor, hook latency rows) | **Gaps closed:** G9.3

---

## Mission

This subplan builds the runtime substrate on which every other layer of Qompack executes. Qompack's design document sets one hard, non-negotiable runtime constraint — *"the hook is on the hot path of every tool call. Target < 15 ms p99"* (§8.1) — and one hard failure doctrine — *"Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today"* (§7.1). Neither is achievable by any single component. The 15 ms budget is achievable only by pairing a fast-cold-start compiled client with a resident daemon that already holds the sketches, DAG, grammar and BOCD posterior in memory (00-ARCHITECTURE §2.1, decision D2). The degradation doctrine is real only if there is an observable state machine that flips modes loudly, a spool that catches every event the daemon cannot take, and a contract monitor that notices when Claude Code's undocumented hook contracts drift. SP-05 owns all of that.

Concretely: SP-05 delivers `internal/ipc` (address resolution, Windows named pipe via `go-winio` with a per-user SID ACL, Unix domain socket with XDG/TempDir resolution and a `sun_path` length guard, NDJSON framing, 1-byte ACK/NAK, the op-routing table, the client whose `Send` never returns a propagating error, the spool, and the server), `internal/daemon` (per-project singleton lock, lazy detached spawn, session registry, WAL-backed ingest queue, worker pool, drain, idle controller, idle exit, config hot reload, and the extension seams later waves wire into), the B-A/B-B/B-D/B-E latency budget definitions with rolling histograms and `obs.CheckBudgets`, the async queue-and-drain fallback implemented as an observable `sync` → `spool` submode transition after three consecutive breach windows, and `internal/contract` in full — the G9.3 assertion set, the fail-loud degradation to passive recording, and two-clean-run restoration. It also owns the `qompack session-start` dispatch and daemon start, the thin-client bodies of all six hook subcommands, `qompack daemon`, `qompack self-test`, the `test/bench/hotpath` real-process-spawn harness, and the CI bench-gate on ubuntu/macos/windows.

**What exists in the repo when you start.** SP-01 has merged into `develop`. That means: the Go 1.26 module `github.com/qompack/qompack` with `gofumpt`, `golangci-lint`, the in-repo `nomagic` analysis pass, `tools/devtool`, the import-graph DAG check and the full GitHub Actions pipeline (the `bench-gate` job exists and is wired but not yet a required check, and its script is a placeholder that SP-05 replaces). The complete Appendix C configuration system plus the §11.5 `runtime` extension namespace (`runtime.mode`, `runtime.daemon.*`, `runtime.hotPath.*`, `runtime.logging.*`, `runtime.redact.*`, `runtime.telemetry.enabled`, `runtime.rehydrate.*`, `runtime.mcp.*`) with five-layer precedence, per-leaf fallback-not-crash validation and provenance. `internal/paths` with `WriteAtomic`, `AppendOnly`, `CreateNew`, `Norm`, `Key` and Windows long-path handling. `internal/core` (`Hash`, `HashBytes`, `SessionID`, `ToolUseID`, `TurnIndex`, `UnixMilli`, `Clock`, the sentinel errors). `internal/logging` with the `Loud` channel, `internal/obs` with `Histogram`/`Registry`/`HistSnapshot`/`BudgetBreach`, `internal/hookio`, `internal/cli` with hand-rolled dispatch and six **no-op** hook entry points, `internal/tokens` baseline, `internal/testutil` and the `test/e2e` harness scaffolding, plus compiling `ErrNotImplemented` stubs and `<pkg>test` conformance suites for every interface in §5 — including `ipctest`, and including the `store`, `sketch`, `dag`, `grammar`, `negknow`, `scheduler` and `checkpoint` stubs this subplan composes but does not implement.

**What exists when you finish.** A `qompack daemon` that starts itself lazily, holds per-project state, ACKs a hook in well under the budget, spools when it cannot, drains what it spooled, exits when idle, and reloads config without a restart. Six hook subcommands that are thin clients and that exit 0 under every fault we can inject. A measured, CI-gated B-A p99 under 15 ms on all three platforms, produced by a harness that spawns 5 000 real processes against a warm daemon. An observable `sync`/`spool` submode that flips on three consecutive breach windows and reverts in the next session. A contract monitor that runs nine assertions at every session start, reports `SevInfo`/`not-yet-implemented` for any assertion whose producer is absent from the build, and degrades loudly to passive recording — L0 and L1 still recording, every acting path off — when a critical assertion fails. And a set of extension seams (`ipc.Router`, `Options.Handle`, `Options.Bind`, `daemon.Services`, `IdleController.Register`) that let SP-08 through SP-13 wire themselves in without editing a single line of daemon internals.

---

## Design context (verbatim from Qompack.md)

### §7.1 The fundamental constraint (quoted in full)

> **A plugin cannot replace Claude Code's compaction. It can only surround it.**
>
> This is the single most important design constraint and it shapes everything below. Qompack is a *sidecar*: it observes via hooks, maintains external durable state, and re-injects via `additionalContext`. It never intercepts the summarizer, never modifies the message array directly, and never assumes it can prevent a compaction from happening.
>
> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

### §8.1 Performance budget (quoted verbatim)

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

### §7.3 Hook surface (the rows this subplan dispatches)

> | Hook | Layer | Responsibility |
> |---|---|---|
> | `PostToolUse` | L0 | Chunk and store tool results; update DAG, sketches, Sequitur; detect redundancy |
> | `UserPromptSubmit` | L0 | Capture user intent **verbatim and immutably** (closes G2.3); update BOCD features |
> | `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |
> | `PreCompact` | L4 | Write immutable checkpoint; emit focus instructions via `custom_instructions` |
> | `PostToolUse` (todo/git) | L3 | Task-boundary signals for the scheduler |
> | `Stop` / `SubagentStop` | L0 | Capture subagent detail before it is double-compressed (closes G10.1) |
> | `SessionEnd` | L1 | Flush, compact the store, write session index |

### §9 gap traceability — the G9.3 row (verbatim)

> | G9.3 undocumented contracts | §12 contract monitor with fail-loud detection | Risk remains, but becomes visible |

### §9 — the gap text this closes (verbatim, from §3 G9)

> | G9.3 | **Those workarounds rest on undocumented contracts** — `SessionStart` firing with `source=compact`, `additionalContext` in `hookSpecificOutput` reaching context, `PreCompact` having time to write. Silent breakage if any changes. |

### §11.3 Guardrails (verbatim)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §10 Phase 1 exit criterion (the clause this subplan owns, verbatim)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

### §12 Risk register — the four rows this subplan implements (verbatim)

> | **Undocumented hook contracts change** (G9.3): `SessionStart` `source=compact`, `additionalContext` reaching context, `PreCompact` timing | High | Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently. |
> | `PreCompact` timeout too short to write a checkpoint | Medium | Write incrementally on the scheduler's cadence so `PreCompact` only finalizes. Never depend on doing all the work in the hook. |
> | Hook latency on the hot path | Medium | Async queue-and-drain fallback; hard p99 budget |
> | Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |

### §11.2 — the latency metrics this subplan reports (verbatim rows)

> | **Compaction pause** | Wall-clock of the summarization call; target O(delta) under frontier advancement (O5) |
> | **Residual span at compaction** | Tokens between frontier N and the compaction point — the direct driver of pause time |
> | **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### §8.4 — the idle signal this subplan's `IdleController` implements (verbatim)

> **Idle-time background work (O3).** User think-time is free compute. During detected idle, the scheduler advances the shadow checkpoint incrementally, runs store GC, precomputes backward slices from the current criterion set, and refreshes Δ-scores — so that when compaction does fire, the expensive analysis is already done and `PreCompact` only finalizes. This is also the natural moment to *perform* a deep cut: the cache is dying anyway and no user is waiting on latency.

### Appendix C — the configuration keys this subplan reads (verbatim excerpt)

```jsonc
  "scheduler": {
    "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
    "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
  },
```

### §12 "What this plugin cannot do" — the bullets that constrain this subplan (verbatim)

> - **Cannot prevent a compaction** — only compact earlier and better.
> - **Cannot modify the message array directly.** Everything flows through `additionalContext`.

---

## Normative context from 00-ARCHITECTURE.md (quoted verbatim — this subplan implements these tables)

### §2.4 Transport (verbatim)

> - Windows: named pipe `\\.\pipe\qompack.<hash12>` via `github.com/Microsoft/go-winio`, ACL'd to the current user SID only. `hash12` = first 12 hex chars of `sha256(normalizedAbsProjectRoot)`.
> - POSIX: `SOCK_STREAM` Unix socket. Path resolution order:
>   1. `$XDG_RUNTIME_DIR/qompack/<hash12>.sock`
>   2. `<os.TempDir()>/qompack-<uid>/<hash12>.sock`
>   Directory `0700`, socket `0600`. If the resolved path exceeds 100 bytes (macOS `sun_path` is 104), fall back to `<os.TempDir()>/qp-<hash8>.sock`.

### §2.4 Framing (verbatim)

> **Framing.** One request per line: UTF-8 JSON, `\n`-terminated, 1 MiB max line. Response for fire-and-forget requests is a single byte `\x06` (ACK) or `\x15` (NAK). Requests that need data back (`UserPromptSubmit` additionalContext, MCP-over-daemon, `status`) set `"reply": true` and receive one NDJSON response line instead.
>
> **Why ACK instead of pure fire-and-forget.** A bare write followed by immediate `exit` is *usually* delivered but is not guaranteed to be observed before the daemon's read loop is torn down on abnormal daemon exit, and on Windows message-mode pipes a client close can race the server read. One byte costs ~50 µs and converts "usually" into "provably enqueued." Budget allows it.

### §2.4 Daemon lifecycle (verbatim)

> - Started by `qompack session-start` (off the hot path, generous hook timeout). Also started lazily by any client that finds no listener, using a detached spawn (`SysProcAttr{HideWindow:true, CreationFlags: CREATE_NO_WINDOW|DETACHED_PROCESS}` on Windows; `Setsid` on POSIX) — the *spawning* client does not wait for it, it spools and exits.
> - Singleton per project via `.qompack/run/daemon.lock` (`O_CREATE|O_EXCL`, contains pid + start time + pipe path). A lock whose pid is dead is stale and reclaimable.
> - Idle-exits after `runtime.daemonIdleExitSeconds` (default 1800) with zero live sessions.
> - Drains `.qompack/spool/*.ndjson` on start and on every idle tick, then deletes drained files.
> - Crash-safe: the WAL (`spool/wal-<session>.ndjson`, `O_APPEND`, no fsync) is the durability boundary. ACK is sent after the WAL append returns, before any indexing work.

### §2.4 Latency budgets (verbatim, normative — these are what CI gates on)

> | ID | Clock | Budget | Enforced |
> |---|---|---|---|
> | **B-A** | `hook_controlled` — client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (§11.3 L0) | CI on linux/macos/windows, 5 000 iterations |
> | **B-B** | `l0_ingest` — daemon read → WAL append returned | p99 < 2 ms | daemon self-metrics + CI |
> | **B-C** | `l0_process` — WAL → fully chunked, stored, DAG/sketches updated (async) | p99 < 50 ms | soft; overrun → sampling + backpressure, never blocking |
> | **B-D** | `hook_wall` — includes host process creation | reported, not gated; tracked in `/qompack:status` and the bench artifact | — |
> | **B-E** | `checkpoint_finalize` — `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | CI |
> | **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |
>
> B-A is the number the design document names. B-D is reported honestly because process creation is the host's cost and no plugin architecture can remove it; hiding it inside B-A would be dishonest measurement, which is exactly the sin §1.3 RC-3 indicts.

### §2.4 Budget-exceeded fallback (verbatim)

> **When the budget is exceeded (§8.1 fallback).** The daemon keeps a rolling 512-sample HDR histogram per hook. If B-A p99 exceeds budget for 3 consecutive 512-sample windows, the daemon sets `hotPathMode = spool` in the session registry and returns it in the next ACK's NAK-with-hint frame; clients then skip the connect entirely and append straight to the spool for the rest of the session. This is the "degrade to async queue-and-drain rather than blocking" clause, implemented as an observable state transition, logged at WARN, and surfaced by `/qompack:status`.

### §12.1 Contract-monitor assertion table (verbatim)

> | Assertion | How it is asserted |
> |---|---|
> | `session_start.fires` | a marker written at `SessionEnd`/`PreCompact` is found by the next `SessionStart`; absence across two sessions ⇒ fail |
> | `session_start.source_compact` | after a `PreCompact` is observed, the next `SessionStart` must arrive with `source == "compact"` within the same session id. Recorded in `state/contract.json` and evaluated on the *following* start |
> | `hook.additional_context_delivered` | `SessionStart` emits a sentinel token in `additionalContext`; the next `UserPromptSubmit` reads the transcript tail and looks for it. Not found ⇒ fail |
> | `precompact.has_time_to_write` | measured `PreCompact` wall time vs. the manifest timeout; p99 > 60% of timeout ⇒ warn, timeout hit ⇒ fail |
> | `precompact.custom_instructions_accepted` | the emitted instruction's sentinel phrase is searched for in the post-compaction summary; absent ⇒ warn (advisory by design, §8.5) |
> | `hook.payload_shape` | required fields present and typed as `hookio.Event` expects |
> | `mcp.server_registered` | the MCP server received `initialize` at least once this session |
> | `transcript.readable` | `transcript_path` exists and parses |
> | `plugin.root_resolves` | `${CLAUDE_PLUGIN_ROOT}` expanded to an existing binary |

### §12.1 The not-yet-implemented rule (verbatim — normative for this subplan)

> **Assertions whose observable does not exist yet.** Some assertions depend on a subsystem that a later wave delivers: `mcp.server_registered` (SP-13), `precompact.custom_instructions_accepted` and `precompact.has_time_to_write` (SP-10), `hook.additional_context_delivered` (SP-11). An assertion whose *producer is absent from the build* returns `OK: true, Severity: SevInfo` with `Observed: "not-yet-implemented"` — it must never degrade the session. Wiring this wrong would put every wave-1 and wave-2 verification run into `degraded-passive` and silently disable the very paths those waves are testing. A CI test asserts a freshly built `develop` reports `ModeFull`.

### §12.1 Degradation semantics (verbatim)

> **On any `SevCritical` failure:** `Monitor.Degrade` is called. That means, in order: `logging.Loud` (log file + `LOUD.log` + `systemMessage` on the next hook that may emit one), persist the reason to `state/contract.json`, set `Mode = ModeDegradedPassive`, and record it in `obs`. `/qompack:status` leads with a banner naming the failed assertion, what was expected, and what was observed. **Nothing fails silently — that is the whole point of the mechanism.**
>
> `ModeDegradedPassive` behaviour: L0 and L1 keep running (observe, chunk, store, sketches, DAG, verbatim capture, elimination records — the store stays correct and the session's data is not lost). Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`, no scheduler-initiated checkpoints, no drop report. MCP retrieval tools stay available, because they are pull-based and cannot make anything worse. The session then behaves exactly as it does without Qompack, which is §7.1's stated requirement.
>
> Recovery: assertions re-run every `SessionStart`. Two consecutive clean runs call `Monitor.Restore`, logged just as loudly as the degradation.

### §12.2 and §12.3 (verbatim)

> Described in §2.4. `sync` → `spool` submode transition on 3 consecutive breach windows; logged at WARN; visible in `/qompack:status`; automatically reverts after 3 clean windows in a subsequent session. Under `spool`, the client never connects: it appends and exits, and the daemon drains on its idle tick. Data is not lost; only freshness is.

> | daemon unreachable | client spools, exits 0, spawns a detached daemon for next time |
> | spool write fails | drop the event, increment `obs.Counter("l0.dropped")`, `Loud` once per session |
> | config invalid | per-leaf fallback to default + `Loud` (§11.3) |
> | any hook panic | recovered in `cli`, logged, `exit 0` with empty output |

### §11.5 runtime keys this subplan consumes (verbatim excerpt — `redact`, `rehydrate` and `mcp` elided, they belong to other subplans)

```jsonc
"runtime": {
  "mode": "auto",                     // "auto" | "full" | "passive" | "off"
  "daemon": { "enabled": true, "idleExitSeconds": 1800, "maxSessions": 8,
              "ackDeadlineMs": 8, "connectDeadlineMs": 5 },
  "hotPath": { "budgetMs": 15, "breachWindows": 3, "spoolOnBreach": true,
               "maxPayloadBytes": 1048576 },
  "logging": { "level": "info", "maxFileMB": 10, "maxFiles": 5 },
  "telemetry": { "enabled": false }                // hardwired off; key exists to say so
}
```

**Key-spelling reconciliation (decided, do not re-litigate).** 00-ARCHITECTURE §2.4's daemon-lifecycle bullet writes the idle-exit key as `runtime.daemonIdleExitSeconds`. That is prose shorthand; §11.5 is the normative schema and spells it `runtime.daemon.idleExitSeconds`. This subplan reads `cfg.Runtime.Daemon.IdleExitSeconds` (default **1800**) everywhere and never introduces a `runtime.daemonIdleExitSeconds` key.

### §7 Benchmark harness (verbatim)

> `devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon --json out.json`
>
> It: starts a real daemon against a temp project, pre-populates it with a realistic session (2 000 tool uses, 40 MB of raw tool output so the CMS and DAG are warm), then spawns the real `qompack observe tool` binary N times with a representative payload on stdin, recording per-invocation wall time and reading the daemon-side B-B histogram at the end.
>
> Outputs `{budget_id, n, p50, p95, p99, p999, max, pass}` for B-A, B-B, B-D. CI runs it on ubuntu-latest, macos-latest, and windows-latest with `n=2000` (5 000 nightly) and **fails the build if B-A p99 ≥ 15 ms or B-E p99 ≥ 2 s**. B-D is recorded and posted as a PR comment but is never a gate — that is the honest treatment of a cost we do not own.

### §2.3 Exit-code policy (verbatim)

> **Hook subcommands must always `exit 0`.** A non-zero hook exit surfaces noise to the user and, for some hooks, can block the turn. Every error path logs and exits 0. This is a hard rule and CI enforces it with a test that fault-injects every dependency of every hook subcommand.

### §5.21 Split ownership of the two multi-owner hooks (verbatim — the rows binding this subplan)

> | `SessionStart` | SP-05: `qompack session-start` dispatch, daemon start, `contract.Monitor.RunAll` before any other work | SP-08: `startup`/`resume` branch (session registration, store open, warm state). SP-11: the `compact` branch (rehydration) and `clear` reset. The `source` switch itself lives in `observer.OnSessionStart` and delegates. |
> | `SessionEnd` | SP-05 (`qompack flush` client + daemon op) | SP-08 owns `observer.OnSessionEnd`; it calls `store.Flush`, the session-index write and `store.GC`, whose mechanics SP-06 owns. No subplan other than SP-08 writes code in `internal/observer`. |

---

## Out of scope

Each item names the sibling subplan that owns it. Do not implement any of these.

| Out of scope for SP-05 | Owner |
|---|---|
| `internal/observer` — any file in it, including `OnToolUse`, `OnUserPrompt`, `OnStop`, `OnSessionStart`, `OnSessionEnd`, `Tombstone`, `ExtractSignals`. SP-05 provides the **function seams** these bind to and calls them when non-nil. | SP-08 |
| Everything the daemon's worker pool eventually does with an event: chunking, canonicalization, storing, DAG edges, sketch feeds, Sequitur symbols, redundancy detection | SP-08 (semantics), SP-04/SP-06/SP-03/SP-07 (mechanics) |
| `internal/store` and all L1 mechanics: objects, roots, tool_use index, file version history, segments, GC, `Flush` | SP-06 |
| `internal/sketch` implementations (Bloom/CMS/HLL/Misra-Gries/MinHash and their serialization). SP-05 defines only `daemon.SketchSet`, the in-memory holder, and calls SP-03's constructors and `Save`/`Load`. | SP-03 |
| `internal/chunk`, `internal/canon`, `internal/symbols` | SP-04 |
| `internal/dag` — the graph, slicing, `CrossingEdges` | SP-07 |
| `internal/eval`, `test/replay`, the replay-gate, Belady OPT, divergence metrics | SP-02 |
| `internal/scheduler` — BOCD, Young–Daly, p-selection, the composite trigger, `scheduler.Runtime`'s **implementation** (which lives in `internal/daemon` but is written by SP-12, in its own file `internal/daemon/schedruntime.go`). SP-05 ships only the `Services.Sched` seam and the `IdleController` it registers into. | SP-12 |
| `internal/checkpoint`, `internal/pins`, the PreCompact **semantics**, `FocusInstructions`, `Advance`, `Finalize`. SP-05 ships the `qompack checkpoint` thin client, the `checkpoint` op route, the `Services.PreCompact` seam and the B-E histogram. | SP-10 |
| `internal/rehydrate`, `internal/rules`, `internal/skills`, the eight-item injection, the drop report. SP-05 emits only the contract **sentinel** in `additionalContext`, never rehydration content. | SP-11 |
| `internal/mcp` — the JSON-RPC server and the eight tools. SP-05 ships the `mcp` op route and the `Services.MCPInitialized` seam that feeds the `mcp.server_registered` assertion. | SP-13 |
| `internal/commands` and `/qompack:status` rendering. SP-05 produces the `status` op payload (a JSON snapshot); SP-14 renders it. | SP-14 |
| `internal/config` schema, defaults, validation, provenance, the `runtime` namespace keys themselves; `internal/paths`, `internal/core`, `internal/logging`, `internal/hookio`, `internal/testutil`, the `test/e2e` scaffolding, and the six no-op hook entry points SP-05 replaces the bodies of | SP-01 |
| `internal/obs`'s `Histogram`, `Registry`, `Counter`, `Gauge`, `Snapshot` implementations. SP-05 adds exactly one new file, `internal/obs/budgets.go`, and replaces the single stub method `CheckBudgets`. | SP-01 |
| goreleaser packaging, the `plugin/bin` launcher shim, cross-platform install validation, `qompack fsck`, `qompack doctor`, the security audit, hardening of §12 paths against real-world failures | SP-17 |
| All user documentation (`README.md`, `docs/user-guide.md`, `docs/troubleshooting.md`, …) | SP-18 |

---

## Interface contract

### Consumes (exact signatures from 00-ARCHITECTURE.md §4, §5 — call these, do not change them)

```go
// package core (§4)
type Hash [32]byte
func (h Hash) String() string
func (h Hash) Short() string
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
var (
    ErrNotImplemented = errors.New("qompack: not implemented")
    ErrNotFound       = errors.New("qompack: not found")
    ErrAppendOnly     = errors.New("qompack: append-only violation")
    ErrDegraded       = errors.New("qompack: running in degraded mode")
    ErrContract       = errors.New("qompack: host contract violated")
)

// package paths (§3.3)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte) error
func AppendOnly(p string) (*os.File, error)
func CreateNew(p string) (*os.File, error)

// package config (§5.1, §11.5)
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
type Env struct { ProjectRoot, HomeDir string; Getenv func(string) string; Flags map[string]string }
// Runtime namespace reached as cfg.Runtime.Daemon.AckDeadlineMs, cfg.Runtime.Daemon.ConnectDeadlineMs,
// cfg.Runtime.Daemon.IdleExitSeconds, cfg.Runtime.Daemon.MaxSessions, cfg.Runtime.Daemon.Enabled,
// cfg.Runtime.HotPath.BudgetMs, cfg.Runtime.HotPath.BreachWindows, cfg.Runtime.HotPath.SpoolOnBreach,
// cfg.Runtime.HotPath.MaxPayloadBytes, cfg.Runtime.Mode, cfg.Scheduler.Idle.DetectAfterSeconds.
// The JSON keys in §11.5 are normative; if SP-01's Go field spelling differs, use SP-01's spelling.

// package logging (§5.2)
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    Loud(msg string, kv ...any)
}
func New(dir string, lvl Level) (Logger, io.Closer, error)
func Nop() Logger

// package obs (§5.2)
type Histogram interface { Observe(d time.Duration); Snapshot() HistSnapshot; Reset() }
type HistSnapshot struct{ N int64; P50, P95, P99, P999, Max time.Duration }
type Registry interface {
    Hist(name string) Histogram
    Counter(name string) Counter
    Gauge(name string) Gauge
    Snapshot() Snapshot
    CheckBudgets(cfg config.Config) []BudgetBreach
}
type BudgetBreach struct{ Budget string; Observed, Limit time.Duration; Windows int }

// package hookio (§5.3)
type Event struct {
    HookEventName  string; SessionID core.SessionID; TranscriptPath string; CWD string
    Source string; Trigger string; ToolName string; ToolUseID core.ToolUseID
    ToolInput json.RawMessage; ToolResponse json.RawMessage; Prompt string
    StopHookActive bool; Extra map[string]json.RawMessage
}
func ReadEvent(r io.Reader, limit int64) (Event, []byte, error)
type Output struct {
    Continue *bool; SuppressOutput *bool; HookSpecificOutput *HSO; SystemMessage string
}
type HSO struct{ HookEventName, AdditionalContext, CustomInstructions string }
func WriteOutput(w io.Writer, o Output) error
func Empty() Output

// Consumed as nil-tolerant late-bound seams only (stubs in wave 1; never called when nil):
// store.Store, negknow.Ledger, dag.Graph, grammar.Sequitur, scheduler.Runtime, checkpoint.Writer,
// sketch.NewBloom/NewCMS/NewHLL/NewMisraGries, sketch.Save, sketch.Load.
```

### Produces (exact signatures later subplans rely on)

```go
// ── package ipc ─────────────────────────────────────────────────────────────
type AddrKind uint8
const (AddrNamedPipe AddrKind = iota; AddrUnixSocket)
type Addr struct{ Kind AddrKind; Path string }
func Resolve(projectRoot string) (Addr, error)
func ProjectHash12(projectRoot string) string
var ErrAddrTooLong = errors.New("qompack: ipc address exceeds sun_path limit")

type Op string
const (
    OpObserveTool   Op = "observe.tool"
    OpObservePrompt Op = "observe.prompt"
    OpObserveStop   Op = "observe.stop"
    OpSessionStart  Op = "session.start"
    OpCheckpoint    Op = "checkpoint"
    OpFlush         Op = "flush"
    OpStatus        Op = "status"
    OpMCP           Op = "mcp"
    OpAdminPing     Op = "admin.ping"
    OpAdminDrain    Op = "admin.drain"
    OpAdminReload   Op = "admin.reload"
    OpAdminIdle     Op = "admin.idle"
    OpAdminShutdown Op = "admin.shutdown"
)
func KnownOps() []Op
func (o Op) Valid() bool
func (o Op) HotPath() bool // true for observe.tool / observe.prompt / observe.stop

type Request struct {
    Op      Op              `json:"op"`
    Session core.SessionID  `json:"s"`
    TS      core.UnixMilli  `json:"t"`
    Reply   bool            `json:"r,omitempty"`
    Event   *hookio.Event   `json:"e,omitempty"`
    Raw     json.RawMessage `json:"x,omitempty"`
}
type HotPathMode uint8
const (HotSync HotPathMode = iota; HotSpool)
func (m HotPathMode) String() string
type Response struct {
    OK     bool            `json:"ok"`
    Mode   contract.Mode   `json:"mode"`
    Hot    HotPathMode     `json:"hot"`
    Output *hookio.Output  `json:"out,omitempty"`
    Err    string          `json:"err,omitempty"`
    Data   json.RawMessage `json:"data,omitempty"`
}

type Client interface {
    Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
    Close() error
}
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client
type ClientOptions struct {
    ProjectRoot     string
    State           State                                  // from ReadState; supplies mode, hot, deadlines, limits
    Self            string                                 // os.Executable(); "" disables lazy spawn
    Spawn           func(projectRoot, self string) error   // set by cli to daemon.SpawnDetached; nil disables lazy spawn
    ConnectDeadline time.Duration                          // 0 → State.ConnectDeadlineMs
    AckDeadline     time.Duration                          // 0 → State.AckDeadlineMs
    MaxLine         int                                    // 0 → DefaultMaxLine
    Clock           core.Clock                             // nil → core.SystemClock()
}
func NewClientWithOptions(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry, o ClientOptions) Client

type SpoolWriter interface{ Append(req Request) error; Path() string }
func NewSpool(dir string) (SpoolWriter, error)
func SpoolFiles(dir string) ([]string, error)
var ErrSpoolFull = errors.New("qompack: spool file at cap")
// ExternalizeThreshold reports the encoded-request size at or above which the client moves the
// oversized member to a side blob: min(cfg.Runtime.HotPath.MaxPayloadBytes, DefaultMaxLine).
func ExternalizeThreshold(cfg config.Config) int

const (
    ACK byte = 0x06
    NAK byte = 0x15
)
const DefaultMaxLine = 1 << 20 // 1 MiB, 00-ARCH §2.4 "1 MiB max line"
var ErrLineTooLong = errors.New("qompack: ipc line exceeds max")
type LineReader struct{ /* … */ }
func NewLineReader(r io.Reader, maxLine int) *LineReader
func (lr *LineReader) ReadLine() ([]byte, error)

type Handler func(ctx context.Context, req Request) Response
type Router struct{ /* … */ }
func NewRouter() *Router
func (r *Router) Handle(op Op, h Handler)
func (r *Router) Route(ctx context.Context, req Request) Response
func (r *Router) Ops() []Op
func (r *Router) SetFallback(h Handler)

type Server interface {
    Serve(ctx context.Context, h Handler) error
    Addr() Addr
    Close() error
}
func NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error)

// state.bin — the 32-byte hot-path state record (see Implementation spec)
type State struct {
    Mode              contract.Mode
    Hot               HotPathMode
    ConnectDeadlineMs uint16
    AckDeadlineMs     uint16
    DaemonEnabled     bool
    SpoolOnBreach     bool
    MaxPayloadBytes   uint32
    DaemonPID         uint32
    Written           core.UnixMilli
}
func StatePath(projectRoot string) string
func ReadState(projectRoot string, fallback config.Config) State
func WriteState(projectRoot string, s State) error
func RemoveState(projectRoot string) error
func StateFromConfig(cfg config.Config) State

// ── package daemon ──────────────────────────────────────────────────────────
type Daemon interface {
    Run(ctx context.Context) error
    Registry() *SessionRegistry
    Drain(ctx context.Context) (int, error)
    Idle() IdleController
    Stop(ctx context.Context) error
}
type Options struct {
    ProjectRoot string; Cfg config.Config; Log logging.Logger
    Metrics obs.Registry; Clock core.Clock
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    Routes *ipc.Router
    binds  []func(*Services)
}
func NewOptions(projectRoot string, cfg config.Config) Options
func New(o Options) (Daemon, error)
func (Options) Handle(op ipc.Op, h ipc.Handler)     // normative §5.4
func (o *Options) Bind(fn func(*Services))          // late binding for waves 2–3
var ErrOptionsUninitialized = errors.New("qompack: daemon.Options not built with NewOptions")

type Services struct {
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    // Function seams — nil until the owning subplan binds them. Every call site checks for nil.
    ObserveTool    func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObservePrompt  func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObserveStop    func(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error) // SP-08
    SessionStart   func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08 + SP-11
    SessionEnd     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    PreCompact     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-10
    Rehydrate      func(ctx context.Context, e hookio.Event) (string, error)        // SP-11
    MCPInitialized func() bool                                                      // SP-13
    StatusExtra    func(ctx context.Context) map[string]any                         // SP-14
}
func ServicesFrom(ctx context.Context) *Services
func RegistryFrom(ctx context.Context) *SessionRegistry
func DaemonFrom(ctx context.Context) Daemon
func DeclareProducers(s *Services)

type IdleController interface {
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}

type SessionState struct {
    ID core.SessionID; Source string; TranscriptPath string
    StartedTS, LastActivityTS, EndedTS core.UnixMilli
    Events, Dropped, Externalized int64
    Hot ipc.HotPathMode
    Live bool
}
type SessionRegistry struct{ /* … */ }
func (r *SessionRegistry) Ensure(e hookio.Event, now core.UnixMilli) *SessionState
func (r *SessionRegistry) Touch(id core.SessionID, now core.UnixMilli)
func (r *SessionRegistry) End(id core.SessionID, now core.UnixMilli)
func (r *SessionRegistry) Live() int
func (r *SessionRegistry) Snapshot() []SessionState
func (r *SessionRegistry) LastActivity() core.UnixMilli   // newest LastActivityTS across all sessions
func (r *SessionRegistry) HotMode() ipc.HotPathMode
func (r *SessionRegistry) SetHotMode(m ipc.HotPathMode, reason string)

type SketchSet struct{ /* … */ }
func NewSketchSet(cfg config.Config) *SketchSet
func (s *SketchSet) Load(projectRoot string, log logging.Logger) error
func (s *SketchSet) Save(projectRoot string, log logging.Logger) error
func (s *SketchSet) Read(fn func(*SketchSet))
func (s *SketchSet) Write(fn func(*SketchSet))

type LockInfo struct{ PID int; Started core.UnixMilli; Addr string; Version string }
type Lock struct{ /* … */ }
func AcquireLock(projectRoot string, a ipc.Addr, clk core.Clock) (*Lock, error)
func ReadLock(projectRoot string) (LockInfo, bool)
func (l *Lock) Heartbeat() error
func (l *Lock) Release() error
var ErrLockHeld = errors.New("qompack: daemon already running for this project")

func EnsureRunning(projectRoot, self string, log logging.Logger, clk core.Clock) (spawned bool, err error)
func SpawnDetached(projectRoot, self string) error

type StatusSnapshot struct {
    Mode            string                     `json:"mode"`
    Hot             string                     `json:"hot"`
    PID             int                        `json:"pid"`
    Addr            string                     `json:"addr"`
    UptimeSeconds   float64                    `json:"uptime_seconds"`
    Sessions        []SessionState             `json:"sessions"`
    Budgets         []obs.BudgetBreach         `json:"budget_breaches"`
    Latency         map[string]obs.HistSnapshot `json:"latency"`
    Counters        map[string]int64           `json:"counters"`
    Contract        []contract.Result          `json:"contract"`
    SpoolFiles      int                        `json:"spool_files"`
    LoudTail        []string                   `json:"loud_tail"`
    Extra           map[string]any             `json:"extra,omitempty"`
}

// ── package contract ────────────────────────────────────────────────────────
type ID string
const (
    CSessionStartFires         ID = "session_start.fires"
    CSessionStartSourceCompact ID = "session_start.source_compact"
    CAdditionalContext         ID = "hook.additional_context_delivered"
    CPreCompactTiming          ID = "precompact.has_time_to_write"
    CPreCompactCustomInstr     ID = "precompact.custom_instructions_accepted"
    CHookPayloadShape          ID = "hook.payload_shape"
    CMCPRegistered             ID = "mcp.server_registered"
    CTranscriptReadable        ID = "transcript.readable"
    CPluginRootResolves        ID = "plugin.root_resolves"
)
type Severity uint8
const (SevInfo Severity = iota; SevWarn; SevCritical)
type Result struct {
    ID ID; OK bool; Severity Severity
    Expected, Observed string; TS core.UnixMilli; Detail string
}
type Mode uint8
const (ModeFull Mode = iota; ModeDegradedPassive; ModeOff)
func (m Mode) String() string
func ParseMode(s string) (Mode, bool)
func (m Mode) MayAct() bool     // false for ModeDegradedPassive and ModeOff
func (m Mode) MayRecord() bool  // false only for ModeOff

type Assertion struct { ID ID; Severity Severity; Description string; Check func(ctx context.Context, e Env) Result }
type Env struct {
    ProjectRoot string; Event hookio.Event; Cfg config.Config
    Store store.Store; Log logging.Logger; Clock core.Clock
    History History
}
type Monitor interface {
    Register(a Assertion) error
    RunAll(ctx context.Context, e Env) ([]Result, Mode)
    Mode() Mode
    Degrade(reason string, results []Result)
    Restore(reason string)
    Report() []Result
}
func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor
func StandardAssertions() []Assertion

type History struct {
    Version               int              `json:"version"`
    Sessions              int              `json:"sessions"`
    LastSessionID         core.SessionID   `json:"last_session_id"`
    LastMarkerTS          core.UnixMilli   `json:"last_marker_ts"`
    StartsWithoutMarker   int              `json:"starts_without_marker"`
    LastPreCompactTS      core.UnixMilli   `json:"last_precompact_ts"`
    LastPreCompactSession core.SessionID   `json:"last_precompact_session"`
    PreCompactWallMs      []int            `json:"precompact_wall_ms"`   // capped at the 64 newest
    PreCompactTimeoutMs   int              `json:"precompact_timeout_ms"` // written by the daemon from pluginmanifest; 0 = unknown
    PreCompactInstr       string           `json:"precompact_instr,omitempty"` // ≤256 chars of the emitted customInstructions
    AwaitingCompactStart  bool             `json:"awaiting_compact_start"`
    Sentinel              Sentinel         `json:"sentinel"`
    MCPInitialized        bool             `json:"mcp_initialized"`
    CleanRuns             int              `json:"clean_runs"`
    Mode                  Mode             `json:"mode"`
    DegradedReason        string           `json:"degraded_reason,omitempty"`
    DegradedSince         core.UnixMilli   `json:"degraded_since,omitempty"`
    Last                  []Result         `json:"last"`
}
func LoadHistory(statePath string) History
func SaveHistory(statePath string, h History) error

type Sentinel struct {
    Token     string         `json:"token"`
    IssuedTS  core.UnixMilli `json:"issued_ts"`
    Session   core.SessionID `json:"session"`
    Observed  bool           `json:"observed"`
    Chances   int            `json:"chances"`
}
func MintSentinel(sess core.SessionID, now core.UnixMilli) Sentinel
func RenderSentinel(s Sentinel) string          // the additionalContext line
func ScanTranscriptTail(path string, token string, tailBytes int64) (bool, error)

func DeclareProducer(id ID)
func HasProducer(id ID) bool
func ResetProducers()   // tests only
func MarkerPath(projectRoot string) string
func WriteMarker(projectRoot string, sess core.SessionID, now core.UnixMilli) error

// ── package obs (one added file: budgets.go) ────────────────────────────────
type Budget struct {
    ID      string        // "B-A" … "B-F"
    Metric  string        // histogram name
    Limit   time.Duration
    Gated   bool
    Clock   string        // the §2.4 "Clock" column, verbatim
}
func Budgets(cfg config.Config) []Budget
func BudgetByID(cfg config.Config, id string) (Budget, bool)
const (
    MetricHookControlled = "hook.controlled"          // B-A (estimated, daemon-side)
    MetricHookObserved   = "hook.controlled.observed" // B-A lower bound, purely daemon-observed
    MetricL0Ingest       = "l0.ingest"                // B-B
    MetricL0Process      = "l0.process"               // B-C
    MetricHookWall       = "hook.wall"                // B-D (bench harness only)
    MetricCheckpointFin  = "checkpoint.finalize"      // B-E
    MetricMCPToolCall    = "mcp.tool_call"            // B-F
)
```

---

## Implementation spec

### Global rules for this subplan

1. All work happens on `feat/sp05-daemon-ipc-and-hot-path`, cut from `develop` with SP-01 already merged. Never commit to `develop` directly. Never modify `Qompack.md`.
2. Import discipline (00-ARCH §3.2, CI-enforced): `ipc` may import **only** the foundation (`core`, `paths`, `config`, `logging`, `obs`) plus `hookio` and `contract`. `contract` may import the foundation plus `hookio` and `store`. `daemon` and `cli` are composition roots and may import anything; nothing may import them.
3. `net` is permitted **only** in `internal/ipc`, and only `net.Dial`/`net.Listen` on `"unix"` — never `"tcp"`. `os/exec` is permitted only in `internal/daemon` (detached self-spawn), `internal/cli`, and `tools/`. The `security` CI job asserts both.
4. The `nomagic` pass forbids the literals `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` outside `internal/config/defaults.go`, `*_test.go`, and `//nomagic:allow <reason>` lines. Every deadline, budget and window size in this subplan comes from `config.Config` or from a named constant in `internal/obs/budgets.go` annotated with its §2.4 row.
5. Every package that takes time takes a `core.Clock`. `time.Sleep` is banned outside `test/bench` (`devtool lint` greps for it). Use `time.NewTicker`/`time.After` inside `select` with a context, and `testutil.FakeClock` in tests.
6. No file outside `.qompack/` (project) and `~/.qompack/` (global) is ever written.

---

### `internal/ipc/addr.go`

**Responsibility.** Deterministic, I/O-free resolution of the transport address from the project root.

```go
func ProjectHash12(projectRoot string) string
func ProjectHash8(projectRoot string) string
func Resolve(projectRoot string) (Addr, error)
```

`normalizeRoot(projectRoot)`: `filepath.Abs` → `filepath.Clean` → replace `\` with `/` → strip a trailing `/` (except a bare root) → on `windows` and `darwin` only, `strings.ToLower`. This mirrors `paths.Key` case semantics so two spellings of one project resolve to one daemon.

`ProjectHash12` and `ProjectHash8`:

```go
func projectHash(root string, n int) string {
    sum := sha256.Sum256([]byte(normalizeRoot(root)))   // array must be bound before slicing
    return hex.EncodeToString(sum[:])[:n]
}
func ProjectHash12(root string) string { return projectHash(root, 12) }
func ProjectHash8(root string) string  { return projectHash(root, 8) }
```

**Deliberately plain `sha256`, not `core.HashBytes`,** because 00-ARCH §2.4 specifies `sha256(normalizedAbsProjectRoot)` with no domain separator; a comment states this so nobody "fixes" it.

Resolution:

- `windows`: `Addr{Kind: AddrNamedPipe, Path: pipePrefix + ProjectHash12(root)}` where `const pipePrefix = ` `` `\\.\pipe\qompack.` `` (a Go raw string literal, so the backslashes are literal), yielding `\\.\pipe\qompack.<hash12>`. No length guard is needed (the pipe namespace allows 256 chars).
- everything else: try in order
  1. `filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "qompack", hash12+".sock")` — only if `XDG_RUNTIME_DIR` is non-empty and absolute;
  2. `filepath.Join(os.TempDir(), fmt.Sprintf("qompack-%d", os.Getuid()), hash12+".sock")`;
  and if the chosen path is `> 100` bytes, fall back to `filepath.Join(os.TempDir(), "qp-"+hash8+".sock")`. If *that* is still `> 100` bytes, return `Addr{}, ErrAddrTooLong`.
- `QOMPACK_IPC_ADDR` overrides everything (tests, CI, and the bench harness use it to keep sockets inside `t.TempDir()`); it is parsed as `pipe:<name>` or `unix:<path>` and still subject to the 100-byte guard.

`os.Getuid()` does not exist on Windows in a useful form; the POSIX branch lives in `addr_unix.go` (build tag `!windows`) and the Windows branch in `addr_windows.go`, with `addr.go` holding `normalizeRoot`, the hashes and `ErrAddrTooLong`.

**Failure modes.** `ErrAddrTooLong` is the only error. Callers (`NewClient`) treat it as "spool-only for this process" — never a hook failure.

---

### `internal/ipc/frame.go`

**Responsibility.** The wire format, byte-for-byte.

```go
const (
    ACK byte = 0x06
    NAK byte = 0x15
)
const DefaultMaxLine = 1 << 20 // 1 MiB, §2.4 "1 MiB max line"

func EncodeRequest(req Request) ([]byte, error)          // compact JSON + '\n', no HTML escaping
func DecodeRequest(line []byte) (Request, error)
func EncodeResponse(resp Response) ([]byte, error)
func DecodeResponse(line []byte) (Response, error)
func NewLineReader(r io.Reader, maxLine int) *LineReader
func (lr *LineReader) ReadLine() ([]byte, error)          // returns ErrLineTooLong past maxLine
var ErrLineTooLong = errors.New("qompack: ipc line exceeds max")
```

Encoding uses a `json.Encoder` with `SetEscapeHTML(false)` writing into a pooled `bytes.Buffer` (`sync.Pool`) so the hot path allocates once. `json.Encoder.Encode` already appends `\n`; do not add a second one. `LineReader` wraps `bufio.Reader` with an explicit growth cap rather than `bufio.Scanner`, so an oversize line yields `ErrLineTooLong` *and* the reader can resynchronize by discarding to the next `\n`.

Byte-exact request example (this is a golden fixture, `testdata/golden/contracts/ipc/observe_tool.ndjson`):

```
{"op":"observe.tool","s":"sess-01","t":1730000000000,"e":{"hook_event_name":"PostToolUse","session_id":"sess-01","tool_name":"Read","tool_use_id":"tu_1"}}
```

Fire-and-forget exchange: client writes the line; server writes exactly one byte, `0x06` or `0x15`. Reply exchange (`"r":true`): client writes the line; server writes one NDJSON `Response` line. There is no length prefix and no other framing.

---

### `internal/ipc/op.go`

**Responsibility.** The op vocabulary and the **op-routing table**, which is data rather than a `switch` so wave-3 subplans register handlers instead of editing daemon internals.

```go
type Router struct {
    mu       sync.RWMutex
    m        map[Op]Handler
    fallback Handler
}
func NewRouter() *Router { return &Router{m: make(map[Op]Handler, len(KnownOps()))} }
func (r *Router) Handle(op Op, h Handler)   // last registration wins; logs at Debug on replace
func (r *Router) SetFallback(h Handler)
func (r *Router) Ops() []Op                 // sorted, for `self-test` and `status`
func (r *Router) Route(ctx context.Context, req Request) Response {
    r.mu.RLock(); h, ok := r.m[req.Op]; fb := r.fallback; r.mu.RUnlock()
    if !ok { if fb == nil { return Response{OK: false, Err: "unknown op: " + string(req.Op)} }; h = fb }
    defer func() { /* recover → Response{OK:false, Err:"panic: …"}; never propagate */ }()
    return h(ctx, req)
}
```

The panic recovery is inside `Route`, not in each handler, and is the mechanism behind §12.3 "MCP tool panic → recovered at the handler boundary, never kills the server".

`Op.HotPath()` returns true for `OpObserveTool`, `OpObservePrompt`, `OpObserveStop` — the three that flow through the spool submode.

---

### `internal/ipc/state.go` — the 32-byte hot-path state record

**Responsibility.** Give the thin client everything it needs to decide *whether and how to connect* in **one** `os.ReadFile` of 32 bytes, so the hot path never runs `config.Load` (two file opens plus env scan) and never stats several files.

Path: `<projectRoot>/.qompack/run/state.bin`. Written by the daemon on start, on every mode/submode transition, and on every config reload. Removed on clean daemon stop.

Layout (little-endian, exactly 32 bytes):

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | magic `'Q','P','S','1'` |
| 4 | 1 | `mode` (0 = full, 1 = degraded-passive, 2 = off) |
| 5 | 1 | `hot` (0 = sync, 1 = spool) |
| 6 | 2 | `connectDeadlineMs` uint16 |
| 8 | 2 | `ackDeadlineMs` uint16 |
| 10 | 2 | `flags` uint16 — bit 0 `daemonEnabled`, bit 1 `spoolOnBreach` |
| 12 | 4 | `maxPayloadBytes` uint32 |
| 16 | 4 | `daemonPID` uint32 |
| 20 | 8 | `written` int64 unix milliseconds |
| 28 | 4 | `crc32c` (Castagnoli) over bytes `[0:28]` |

```go
func ReadState(projectRoot string, fallback config.Config) State
```
reads the file; on any of *missing*, *short*, *bad magic*, *bad CRC*, it returns `StateFromConfig(fallback)` without logging (a missing state file is the normal first-run case). `WriteState` uses `paths.WriteAtomic` — 32 bytes through the `.qompack/tmp` staging path, so a client never observes a torn record.

The client's `fallback` is `config.Defaults()`, which is a pure function with no I/O. That is the entire reason this design exists: on the hot path we pay one 32-byte read and zero config parsing.

---

### `internal/ipc/spool.go`

**Responsibility.** Client-side durable fallback queue (decision D4: "the queue-and-drain degradation of §8.1 must exist **below** the daemon, not inside it").

```go
type spool struct {
    dir   string
    path  string        // <dir>/client-<pid>.ndjson
    f     *os.File
    bytes int64
    err   error         // sticky: after a write failure the spool reports itself dead once
}
func NewSpool(dir string) (SpoolWriter, error)
func (s *spool) Append(req Request) error
func (s *spool) Path() string
func SpoolFiles(dir string) ([]string, error)  // sorted: wal-* first, then client-*
```

`NewSpool` does **not** open the file — it only records the directory, so a process that never spools pays nothing. `Append` lazily `os.MkdirAll(dir, 0o700)` then opens with `O_APPEND|O_CREATE|O_WRONLY, 0o600` (via `paths.AppendOnly`, which is the only permitted write path into `*.ndjson`), writes the encoded line, and keeps the handle for the process lifetime. No `fsync` — §2.4 makes the WAL, not the spool, the durability boundary, and the spool is a best-effort fallback below it.

**Externalized payloads.** If the encoded request reaches `ExternalizeThreshold(cfg)` = `min(cfg.Runtime.HotPath.MaxPayloadBytes, DefaultMaxLine)` (both default to 1 MiB), the client writes the oversized member to `<dir>/blob-<pid>-<n>.bin` and spools/sends the request with `Raw` replaced by `{"blob":"blob-<pid>-<n>.bin","bytes":<n>,"field":"e.tool_response"}` and `Event.ToolResponse` set to `null`. The daemon resolves blobs during handling and during `Drain`, then deletes them. This keeps a 40 MB tool result off the socket and out of the 1 MiB line limit while losing nothing. Counter: `obs.Counter("l0.externalized")`.

**Spool cap.** A daemon that never comes back must not let the spool grow without bound and fill the user's disk (that would violate §7.1 directly). `Append` refuses once the process's own spool file reaches

```go
const spoolMaxBytes = 64 << 20 // 64 MiB per client file, matching walRotateBytes
var ErrSpoolFull = errors.New("qompack: spool file at cap")
```

returning `ErrSpoolFull`, which takes the failure path below. The daemon deletes drained files, so the cap is only ever reached when nothing is draining — exactly the case where dropping is correct.

**Failure mode (§12.3 "spool write fails").** On the first `Append` error of any kind — permission denied, `ENOSPC`, `ErrSpoolFull`: drop the event, `obs.Counter("l0.dropped").Inc()`, `log.Loud("spool write failed — event dropped", "err", err)` **once per process**, set the sticky `err` so subsequent appends are cheap no-ops, and return `nil` to the caller. The hook still exits 0.

---

### `internal/ipc/listen_windows.go` / `internal/ipc/listen_unix.go`

Build-tagged transport primitives; the only files that import `go-winio` or call `net.Listen`.

```go
// listen_windows.go   //go:build windows
func listen(a Addr, maxLine int) (net.Listener, error)
func dial(ctx context.Context, a Addr) (net.Conn, error)
```

Windows `listen`: build the SDDL from the current user's SID — `u, err := user.Current()`; `sddl := "D:P(A;;GA;;;" + u.Uid + ")"` (on Windows `user.User.Uid` is the SID string). `P` makes the DACL protected so no inherited ACE widens it, and the single ACE grants `GENERIC_ALL` to the invoking user only. Then:

```go
const pipeBufferBytes = 64 << 10 // 64 KiB kernel buffer hint per pipe instance

winio.ListenPipe(a.Path, &winio.PipeConfig{
    SecurityDescriptor: sddl,
    MessageMode:        false,           // byte mode: NDJSON is a stream protocol
    InputBufferSize:    pipeBufferBytes,
    OutputBufferSize:   pipeBufferBytes,
})
```

**Why the buffer is 64 KiB and not `maxLine`.** In byte mode the buffer size is a kernel-allocated hint, not a message-size limit: a 1 MiB NDJSON line streams through a 64 KiB buffer without truncation, and `maxLine` is enforced by `LineReader` in user space. Sizing the buffers at `maxLine` would pin ~2 MiB of non-paged pool per concurrent pipe instance for no benefit. `maxLine` is therefore *not* passed to `winio`; `listen` keeps the parameter only so the two build-tagged files share one signature.

If `user.Current()` fails, fall back to `"D:P(A;;GA;;;CO)"` (creator-owner) and `log.Loud` once — never fall back to a permissive descriptor. Windows `dial`: `winio.DialPipeContext(ctx, a.Path)`; a `windows.ERROR_FILE_NOT_FOUND`/`os.ErrNotExist` result means "no daemon", which is the lazy-spawn trigger.

POSIX `listen`: `os.MkdirAll(filepath.Dir(a.Path), 0o700)`; probe for a live listener by dialling with a 2 ms deadline — if the dial succeeds, return `ErrLockHeld`; if it fails with `ECONNREFUSED` or `ENOENT`, `os.Remove` the stale socket; then `net.Listen("unix", a.Path)` and `os.Chmod(a.Path, 0o600)`. POSIX `dial`: `(&net.Dialer{}).DialContext(ctx, "unix", a.Path)`.

Neither file contains protocol logic; both are ≤ 60 lines.

---

### `internal/ipc/client.go` — the thin client

**Responsibility.** The hot path. `Send` **never returns an error a hook could propagate**; every failure route ends in a spool append and `(Response{OK:false}, nil)`.

```go
type client struct {
    addr   Addr
    spool  SpoolWriter
    log    logging.Logger
    m      obs.Registry
    o      ClientOptions
    hot    HotPathMode      // read once from state.bin at construction
    mode   contract.Mode
    threshold int           // ExternalizeThreshold, precomputed at construction
    spawned bool            // at most one lazy spawn attempt per process
}
```

`NewClient(addr, spool, log, m)` = `NewClientWithOptions(addr, spool, log, m, ClientOptions{})` with zero values filled from `config.Defaults()`; when `ClientOptions.ProjectRoot` is set, `ReadState` populates `hot`, `mode`, both deadlines, `daemonEnabled`, `spoolOnBreach` and `maxPayloadBytes`.

`Send(ctx, req, deadline)` — the exact algorithm:

```
 1. if c.mode == contract.ModeOff            → return Response{OK:true, Mode:ModeOff}, nil   (do nothing at all)
 2. if !c.o.State.DaemonEnabled              → spoolAndReturn(req)
 3. if c.hot == HotSpool && req.Op.HotPath() → spoolAndReturn(req)          // never connects (§2.4)
 4. line, err := EncodeRequest(req)
    if err != nil                            → externalize(req) then retry once; on failure spoolAndReturn
    if len(line) >= c.threshold → req = externalize(req); re-encode
        // c.threshold = min(int(c.o.State.MaxPayloadBytes), c.o.MaxLine), i.e. ExternalizeThreshold
 5. dctx, cancel := context.WithTimeout(ctx, c.o.ConnectDeadline)          // default runtime.daemon.connectDeadlineMs = 5
    conn, err := dial(dctx, c.addr); cancel()
    if err != nil                            → c.lazySpawn(); spoolAndReturn(req)
 6. defer conn.Close()
    conn.SetWriteDeadline(now + c.o.AckDeadline)                            // runtime.daemon.ackDeadlineMs = 8
    if _, err := conn.Write(line); err != nil → spoolAndReturn(req)
 7. if !req.Reply:
       conn.SetReadDeadline(now + c.o.AckDeadline)
       var b [1]byte; n, err := io.ReadFull(conn, b[:])
       if err != nil || n != 1               → spoolAndReturn(req)
       if b[0] == ACK                        → return Response{OK:true, Mode:c.mode, Hot:HotSync}, nil
       if b[0] == NAK                        → c.hot = HotSpool; spool(req)
                                               return Response{OK:false, Mode:c.mode, Hot:HotSpool}, nil
       otherwise (unknown byte)              → spoolAndReturn(req)
 8. if req.Reply:
       conn.SetReadDeadline(now + deadline)                                 // caller-supplied, e.g. 10s for session.start
       line, err := NewLineReader(conn, c.o.MaxLine).ReadLine()
       if err != nil                         → spoolAndReturn(req)
       resp, err := DecodeResponse(line)
       if err != nil                         → spoolAndReturn(req)
       if resp.Hot == HotSpool               → c.hot = HotSpool
       return resp, nil
```

`spoolAndReturn(req)` appends to the spool (swallowing its error per §12.3), increments `obs.Counter("l0.spooled")`, and returns `Response{OK:false, Mode:c.mode, Hot:c.hot}, nil`.

`lazySpawn()` runs at most once per process and only when `c.o.Self != ""` and `c.o.Spawn != nil`: it takes `<projectRoot>/.qompack/run/spawn.lock` with `paths.CreateNew` (`O_EXCL`) containing the current unix-milli; if the file already exists and is younger than **10 s**, it returns immediately (another client is already spawning); if it is older, it is removed and retried once. On success it calls `c.o.Spawn(projectRoot, self)`. It cannot call `daemon.SpawnDetached` directly, because `ipc` may not import `daemon` (§3.2); that is precisely why `ClientOptions.Spawn` is a function field, which `internal/cli` sets to `daemon.SpawnDetached`. When `Spawn` is nil, lazy spawn is skipped entirely. The spawning client **does not wait**: it spools and exits, exactly as §2.4 requires.

`Close()` closes the spool file if it was opened and returns its error; it is the only method that may return a non-nil error, and `cli` ignores it after logging.

**Instrumentation.** The client observes nothing itself (measuring on the hot path costs more than it reveals, and B-A is authoritatively measured by `test/bench/hotpath`). It stamps `Request.TS = clock.Now().UnixMilli()` as the *first* statement of the hook subcommand, before flag parsing and before reading stdin, so the daemon can compute the observed portion of B-A.

---

### `internal/ipc/server.go`

```go
type server struct {
    addr Addr; ln net.Listener; log logging.Logger; m obs.Registry
    maxLine int; wg sync.WaitGroup; closed atomic.Bool
}
func NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error)
func (s *server) Serve(ctx context.Context, h Handler) error
```

`Serve` runs an accept loop; each connection gets a goroutine that reads NDJSON lines until EOF (so a long-lived MCP-over-daemon connection can multiplex), and for each line:

1. `DecodeRequest`; on error write `NAK`, increment `obs.Counter("ipc.decode_error")`, and continue reading.
2. `resp := h(ctx, req)`.
3. If `req.Reply`, write `EncodeResponse(resp)`; else write one byte — `ACK` when `resp.OK`, `NAK` when not.
4. A write error terminates that connection only.

`ErrLineTooLong` writes `NAK` and discards to the next newline. Accept errors that are `net.Error` and temporary are retried after a bounded backoff driven by a ticker (never `time.Sleep`); a permanent accept error returns. `Close` sets `closed`, closes the listener (which unblocks `Accept`), waits for in-flight connections with a 2 s bound, and on POSIX unlinks the socket path.

---

### `internal/daemon/options.go` — the extension seams

This file is the reason wave-3 subplans do not collide inside one package. Three seams, all shipped now, all exercised by tests now.

**Symbol note.** 00-ARCHITECTURE §5.2 fixes `obs.Registry`, `obs.Counter` and `obs.Gauge` as interfaces but does not spell their constructor or their increment method. Use the exact names SP-01 shipped in `internal/obs` (read `internal/obs/registry.go` once, at the start of commit 4) — this subplan writes `obs.NewRegistry()` and `Counter.Inc()` as placeholders **for those exact shipped names**, not as a request to invent them. Do not add either symbol to `internal/obs` if it is spelled differently there.

```go
func NewOptions(projectRoot string, cfg config.Config) Options {
    return Options{
        ProjectRoot: projectRoot, Cfg: cfg,
        Log: logging.Nop(), Metrics: obs.NewRegistry(), Clock: core.SystemClock(),
        Routes: ipc.NewRouter(),
        Sketches: NewSketchSet(cfg),
    }
}

// Handle registers an op handler. Signature is normative (00-ARCH §5.4).
func (o Options) Handle(op ipc.Op, h ipc.Handler) {
    if o.Routes == nil {                       // Options built as a bare literal
        if o.Metrics != nil { o.Metrics.Counter("daemon.route.dropped").Inc() }
        if o.Log != nil { o.Log.Loud("daemon.Handle called on uninitialized Options — route dropped", "op", op) }
        return
    }
    o.Routes.Handle(op, h)
}

// Bind registers a late-binding hook run once, in registration order, at daemon start.
func (o *Options) Bind(fn func(*Services)) { o.binds = append(o.binds, fn) }
```

`New(o Options)` returns `ErrOptionsUninitialized` when `o.Routes == nil`, so a mis-built `Options` fails at construction (which is off the hot path and inside the composition root) instead of silently losing routes.

`Services` is the late-bound dependency set. `New` seeds it from the `Options` fields (`Store`, `Ledger`, `Sketches`, `Graph`, `Grammar`, `Sched`, `Checkpoints`), then applies every `Bind` function in order, then calls `DeclareProducers(svc)`. The daemon stores `*Services` and injects it into every handler context:

```go
type ctxKey int
const (ctxServices ctxKey = iota; ctxRegistry; ctxDaemon)
func ServicesFrom(ctx context.Context) *Services  // never nil inside a handler
func RegistryFrom(ctx context.Context) *SessionRegistry
func DaemonFrom(ctx context.Context) Daemon
```

**Nil tolerance is normative.** Every call site in `internal/daemon` that touches a `Services` member is written as:

```go
if svc.ObserveTool == nil {
    m.Counter("l0.unhandled.observe_tool").Inc()
    return ipc.Response{OK: true, Mode: mode, Hot: hot}   // WAL already holds it; ACK anyway
}
```

The event is already durable in the WAL when this runs, so an unbound seam costs freshness, never data. A table test (`TestServicesAllNil`) drives every op through the daemon with a fully nil `Services` and asserts every response is `OK:true` and no panic occurs.

`DeclareProducers` is the bridge to the contract monitor:

```go
func DeclareProducers(s *Services) {
    // The five SP-05 itself produces: it observes every hook arrival, writes the SessionEnd/
    // PreCompact marker, and records the PreCompact→SessionStart(source=compact) sequence.
    contract.DeclareProducer(contract.CSessionStartFires)
    contract.DeclareProducer(contract.CSessionStartSourceCompact)
    contract.DeclareProducer(contract.CHookPayloadShape)
    contract.DeclareProducer(contract.CTranscriptReadable)
    contract.DeclareProducer(contract.CPluginRootResolves)
    // The four §12.1 names as later-wave. Exactly these four, no more and no fewer.
    if s.PreCompact != nil || s.Checkpoints != nil {   // SP-10
        contract.DeclareProducer(contract.CPreCompactTiming)
        contract.DeclareProducer(contract.CPreCompactCustomInstr)
    }
    if s.Rehydrate != nil { contract.DeclareProducer(contract.CAdditionalContext) }   // SP-11
    if s.MCPInitialized != nil { contract.DeclareProducer(contract.CMCPRegistered) }  // SP-13
}
```

That is the whole "producer absent from the build" mechanism of §12.1, and it requires no later subplan to edit `internal/contract`.

**Why `session_start.source_compact` is in the always-declared set.** 00-ARCH §12.1 enumerates the later-wave assertions exhaustively — `mcp.server_registered` (SP-13), `precompact.custom_instructions_accepted` and `precompact.has_time_to_write` (SP-10), `hook.additional_context_delivered` (SP-11) — and `session_start.source_compact` is not among them. Its observable is purely a hook arrival pattern (a `checkpoint` op followed by a `session.start` op), and SP-05 owns both routes, so it is live from wave 1 and does not wait on SP-10. Five always-declared, four producer-gated.

---

### `internal/daemon/lock.go` — per-project singleton

```go
type Lock struct{ path string; f *os.File; hb string; clk core.Clock }
func AcquireLock(projectRoot string, a ipc.Addr, clk core.Clock) (*Lock, error)
func ReadLock(projectRoot string) (LockInfo, bool)
func (l *Lock) Heartbeat() error
func (l *Lock) Release() error
```

`.qompack/run/daemon.lock` is created with `paths.CreateNew` (`O_CREATE|O_EXCL|O_WRONLY`, `0o600`) and contains one JSON object:

```json
{"pid":12345,"started":1730000000000,"addr":"\\\\.\\pipe\\qompack.7f3a91c20b4e","version":"0.1.0"}
```

On `EEXIST`, run the **staleness protocol**, in this order, stopping at the first decisive answer:

1. Read the lock. If it is unparseable → stale.
2. Dial the recorded `addr` with a 20 ms deadline. Success → **alive**, return `ErrLockHeld` (this is the authoritative liveness test and costs nothing).
3. POSIX only: `syscall.Kill(pid, 0)`. `ESRCH` → stale; `EPERM` → alive.
4. All platforms: read `.qompack/run/daemon.hb`. If its mtime is older than `staleAfter = 90 * time.Second` → stale. Otherwise → alive.
5. Stale → `os.Remove` the lock and the heartbeat, then retry `CreateNew` exactly once. A second `EEXIST` returns `ErrLockHeld` (another daemon won the race, which is the correct outcome).

The daemon calls `Heartbeat()` on a 30 s ticker; it `os.Chtimes`es `daemon.hb`, creating it if absent. `Release()` closes and removes the lock and the heartbeat and is safe to call twice.

Steps 3 and 4 are separated deliberately: Windows has no cheap, dependency-free `kill(pid,0)` equivalent (`os.FindProcess` always succeeds there), so the heartbeat file carries liveness on that platform. `syscall.Kill` lives in `lock_unix.go`; `lock_windows.go` provides `func pidAlive(int) (bool, bool) { return false, false }` meaning "no opinion".

---

### `internal/daemon/spawn.go` (+ `spawn_windows.go`, `spawn_unix.go`)

```go
func EnsureRunning(projectRoot, self string, log logging.Logger, clk core.Clock) (spawned bool, err error)
func SpawnDetached(projectRoot, self string) error
```

`EnsureRunning` dials the resolved address with a 20 ms deadline; on success returns `(false, nil)`. Otherwise it calls `SpawnDetached` and then polls the address with a ticker every 25 ms for up to **1 500 ms**, returning `(true, nil)` on the first successful dial and `(true, ErrNotFound)` if the daemon never came up — which `session-start` treats as "log, spool, exit 0", never a hook failure.

`SpawnDetached` builds `exec.Command(self, "daemon", "--project", projectRoot)`, sets `Stdin`, `Stdout`, `Stderr` to `os.DevNull` handles, sets `Env` to `append(os.Environ(), "QOMPACK_PROJECT_ROOT="+projectRoot)` with `QOMPACK_FAULT` removed, applies the platform `SysProcAttr`, calls `Start()`, then `Process.Release()` so no zombie is left and the parent can exit immediately.

```go
// spawn_windows.go  //go:build windows
const createNoWindow = 0x08000000   // CREATE_NO_WINDOW
const detachedProcess = 0x00000008  // DETACHED_PROCESS
func sysProcAttr() *syscall.SysProcAttr {
    return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | detachedProcess}
}
// spawn_unix.go  //go:build !windows
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
```

The two flag constants are declared locally rather than pulled from `golang.org/x/sys/windows`, because §2.5 closes the runtime dependency list at `klauspost/compress/zstd` and `Microsoft/go-winio`.

---

### `internal/daemon/registry.go` — session registry and hot state

`SessionRegistry` holds `map[core.SessionID]*SessionState` behind an `sync.RWMutex`, plus the project-wide `hot ipc.HotPathMode` and its transition reason.

- `Ensure(e, now)` creates or returns the state, sets `Source`/`TranscriptPath` from the event when non-empty, marks `Live = true`, and **resets the hot submode to `HotSync` when the session id is new** — this is §12.2's "automatically reverts after 3 clean windows in a subsequent session", made deterministic: a new session always starts in `sync` and re-degrades only if it breaches again. The reset is logged at INFO with the previous mode and reason.
- `Touch(id, now)` updates `LastActivityTS` and increments `Events`. It is called on every accepted request and is the input to `IdleController.Notify`.
- `End(id, now)` sets `Live = false` and `EndedTS`.
- `Live()` counts `Live == true` states; when it reaches zero the idle-exit timer starts.
- Eviction: when the number of states exceeds `cfg.Runtime.Daemon.MaxSessions` (default 8), drop the ended state with the oldest `EndedTS`. If **all** are live, keep them all, `log.Loud("live sessions exceed runtime.daemon.maxSessions", …)` once, and continue — refusing a session is never an option (§7.1).

---

### `internal/daemon/ingest.go` — WAL, ring, worker pool

```go
type job struct{ req ipc.Request; recv core.UnixMilli; key core.Hash }
type ingest struct {
    root string; cfg config.Config; log logging.Logger; m obs.Registry; clk core.Clock
    mu sync.Mutex; wals map[core.SessionID]*walFile
    ring chan job
    seen *seenSet
    wg sync.WaitGroup
}
func newIngest(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) *ingest
func (i *ingest) Accept(req ipc.Request, line []byte) error   // B-B clock
func (i *ingest) Start(ctx context.Context, workers int, run func(context.Context, ipc.Request))
func (i *ingest) CloseSession(sess core.SessionID) error
func (i *ingest) Close() error
```

`Accept` is the **B-B path** and does exactly three things, timed end to end into `obs.MetricL0Ingest`:

1. Resolve or open `spool/wal-<session>.ndjson` via `paths.AppendOnly` (`O_APPEND|O_CREATE|O_WRONLY`, `0o600`). One handle per session, cached.
2. `f.Write(line)` — the **exact bytes received**, so replay is byte-identical. **No `fsync`** (§2.4: "`O_APPEND`, no fsync"). Rotate to `wal-<session>.<n>.ndjson` past `walRotateBytes`.
3. Non-blocking enqueue into `ring`, whose capacity is the named constant

   ```go
   const ringCapacity = 4096 //nomagic:allow in-memory queue depth, not a config default (§2.4 B-C backpressure)
   const walRotateBytes = 64 << 20 // 64 MiB
   ```

   If the ring is full, do **not** block: increment `obs.Counter("l0.ring_full")`, append the line to the client spool directory instead, and return — the daemon has the data and will drain it. This is the B-C "overrun → sampling + backpressure, never blocking" clause.

The ACK is written by the server **after `Accept` returns and before any worker touches the job** — §2.4's "ACK is sent after the WAL append returns, before any indexing work."

The worker pool is `max(2, runtime.NumCPU()/2)` goroutines, each pulling from `ring`, timing the handler into `obs.MetricL0Process` (B-C), recovering panics into `obs.Counter("l0.worker_panic")` plus a `Loud` line, and never re-panicking.

`seenSet` is a capacity-bounded (65 536 entries) FIFO of `core.Hash` dedup keys, `key = core.HashBytes("qompack.wal.v1", line)`. It makes `Drain` idempotent within a daemon lifetime; across lifetimes, idempotency rests on the file-completion record in `state/drain.json` plus the content-addressed store's own idempotency.

---

### `internal/daemon/drain.go`

```go
func (d *daemon) Drain(ctx context.Context) (int, error)
```

Runs on daemon start, on every idle tick, and on the `admin.drain` op. Algorithm:

1. `files := ipc.SpoolFiles(spoolDir)` — `wal-*` first (they are the daemon's own, and replaying them first restores session state before client fallbacks are applied), then `client-*`, each group sorted lexically so `<pid>` ordering is stable.
2. Load `state/drain.json`: `map[string]struct{Size int64; Offset int64; Done bool}` keyed by base name.
3. For each file: skip if `Done` and the current size equals the recorded size. Otherwise open, `Seek(offset)`, and read lines with a `LineReader`. For each line: compute the dedup key, skip if `seen`; resolve any `blob:` reference (read the blob file, restore the field, delete the blob); dispatch **synchronously** through the same handler the workers use, with a per-line context deadline of 5 s; advance `offset`.
4. On EOF: mark `Done`, persist `state/drain.json` via `paths.WriteAtomic`, then `os.Remove` the file (§2.4: "then deletes drained files"). WAL files for sessions that are still live are **not** deleted — they are marked with the consumed offset and re-scanned on the next tick.
5. Honour `ctx` between lines; a cancelled drain persists its offsets and returns `(n, ctx.Err())`, so it resumes exactly where it stopped.

Return value is the number of requests dispatched. Errors on individual files are logged at WARN, counted in `obs.Counter("drain.file_error")`, and never abort the whole drain.

---

### `internal/daemon/idle.go` — `IdleController`

```go
type idleTask struct{ name string; prio int; fn func(context.Context) error }
type idleController struct {
    mu sync.Mutex; tasks []idleTask
    lastActivity core.UnixMilli
    afterSeconds int          // cfg.Scheduler.Idle.DetectAfterSeconds (Appendix C default 120)
    clk core.Clock; log logging.Logger; m obs.Registry
    mode func() contract.Mode // supplied by New; RunOnce skips "act."-prefixed tasks when !MayAct()
}
```

- `Register(name, prio, fn)` appends and re-sorts by ascending `prio` (lower runs first), replacing a task with the same name. This is the seam SP-12 uses for O3/O5 (`advance_frontier`, `gc`, `precompute_slice`, `refresh_delta`, `rebuild_bloom`, `compact_dag`) and SP-09 uses for the bloom rebuild — neither edits daemon internals.
- `Notify(lastActivity)` records the newest activity timestamp.
- `IsIdle(now)` = `now-lastActivity >= afterSeconds*1000`. This deliberately keys on *activity*, matching §8.4's idle model.
- `RunOnce(ctx, budget)` runs tasks in priority order until the budget is spent, giving each task a sub-context of `budget - elapsed`, recovering panics per task, recording `obs.Hist("idle.task."+name)`, and returning the names that actually ran. A task returning an error is logged at WARN and does not stop the rest. A task whose name begins `act.` is skipped (and omitted from `ran`) when `!mode().MayAct()` — the mode-enforcement rule of the handlers section, applied once, here.

SP-05 itself registers three tasks at priority 10, 20 and 30: `drain` (calls `Drain`), `sketches` (`SketchSet.Save`), and `metrics` (write `.qompack/metrics/latency.json` from `obs.Registry.Snapshot()`). None of the three carries the `act.` prefix: all three are recording/maintenance work that must keep running in `degraded-passive`.

---

### `internal/daemon/budget.go` — the hot-path breach detector and the `sync` → `spool` transition

```go
type breachDetector struct {
    window   []time.Duration   // fixed 512-sample ring
    n        int
    limit    time.Duration     // cfg.Runtime.HotPath.BudgetMs
    need     int               // cfg.Runtime.HotPath.BreachWindows (default 3)
    breaches int
    clean    int
}
const sampleWindow = 512 // 00-ARCH §2.4 "rolling 512-sample HDR histogram per hook"
type Transition uint8
const (NoTransition Transition = iota; ToSpool; ToSync)
func (b *breachDetector) Observe(d time.Duration) Transition
func (b *breachDetector) Reset()
```

`Observe` pushes the sample; when the ring fills (every 512th sample) it closes a window:

```go
p99 := percentile(window, 0.99)   // copy, sort.Slice, idx = int(math.Ceil(0.99*512))-1 = 506
if p99 >= b.limit { b.breaches++; b.clean = 0 } else { b.clean++; b.breaches = 0 }
b.n = 0
if b.breaches >= b.need { b.breaches = 0; return ToSpool }
if b.clean >= b.need    { b.clean = 0;    return ToSync }
return NoTransition
```

Window closure runs on a worker goroutine, not on the ACK path.

**What is measured.** The daemon cannot observe the client's `main()` entry → `exit` interval directly, and inventing a number would be the exact dishonesty §2.4 warns against. So two histograms are kept and both are reported:

- `obs.MetricHookObserved` (`hook.controlled.observed`) = `recvTS - req.TS`, where `req.TS` is stamped as the first statement of the hook subcommand. This is a strict **lower bound** on B-A: it covers process start through the daemon's read.
- `obs.MetricHookControlled` (`hook.controlled`) = that value plus `hotPathTailAllowance`, a documented **1 ms** constant covering the un-observable client tail (ACK read + process exit), measured by the bench harness on all three platforms to be under 0.4 ms. The allowance deliberately over-counts so the fallback fires early rather than late.

The breach detector consumes `hook.controlled`. `/qompack:status` shows both, labelled. The authoritative B-A number remains `test/bench/hotpath`, which measures real spawns end to end and is what CI gates on.

On `ToSpool`: `registry.SetHotMode(ipc.HotSpool, reason)`, `ipc.WriteState` (so the next client process skips the connect entirely), `log.Warn("hot path degraded to spool submode", "p99_ms", …, "budget_ms", …, "windows", need)`, `obs.Counter("hotpath.degraded").Inc()`. Every subsequent **fire-and-forget** request from a hot-path op (`observe.tool`, `observe.stop`) answers `NAK` instead of `ACK`, which is §2.4's "NAK-with-hint frame" — the in-flight client learns immediately and spools that same request rather than losing it. `observe.prompt` is a reply request (see the handlers section), so it carries the same hint in `Response.Hot = HotSpool`, which client step 8 honours identically. When `cfg.Runtime.HotPath.SpoolOnBreach` is false, the transition is logged and counted but not applied.

On `ToSync`: the inverse, logged at INFO plus a `Loud` line (degradation transitions in both directions are loud, per invariant 10).

**Two consequences that look like bugs and are not — state them in comments so nobody "fixes" them.**

1. *The NAK duplicates one request, deliberately.* When the daemon answers `NAK`, the request has **already been WAL-appended** by `ingest.Accept`; the client then also spools its copy (client step 7). The duplicate is absorbed by `seenSet`: `Drain` computes `core.HashBytes("qompack.wal.v1", line)` for every line it reads and skips keys it has already dispatched, and the WAL is replayed before the client spool precisely so the daemon's own copy wins. Duplicating and deduplicating is the correct trade — the alternative is a request that neither side keeps.
2. *`ToSync` cannot be driven by hot-path traffic while `HotSpool` is active,* because hot-path clients stop connecting, so no new B-A samples arrive. That is why §12.2 says the revert happens "in a subsequent session" and why `SessionRegistry.Ensure` resets a **new** session id to `HotSync` unconditionally. The `ToSync` transition still exists and is unit-tested, and it fires in the case that does produce samples: a session that re-enters `sync` via that reset and then runs three clean windows. `TestBreachDetectorRevertsAfterThreeCleanWindows` tests the detector; `TestRegistryNewSessionResetsHotMode` tests the path that actually reaches it in production.

---

### `internal/daemon/handlers.go` — the default op routes

Registered by `New` into `Options.Routes` **only if the op is not already registered**, so a later subplan's `Handle` call always wins.

| Op | Reply | Behaviour |
|---|---|---|
| `observe.tool` | no | `registry.Touch`; `ingest.Accept`; worker calls `svc.ObserveTool` when non-nil |
| `observe.prompt` | **yes** | `registry.Touch`; `ingest.Accept` (so the WAL holds it before anything else); then, **synchronously and inside the request deadline**, `svc.ObservePrompt` when non-nil and `mode.MayAct()` — its `hookio.Output` becomes `Response.Output`. When nil or not acting, `Output = hookio.Empty()`. The `hook.additional_context_delivered` sentinel scan runs on a worker afterwards, off the reply path (below) |
| `observe.stop` | no | same as `observe.tool` via `svc.ObserveStop`, `subagent` read from `req.Raw` `{"subagent":true}` |
| `session.start` | **yes** | full warm path (below) |
| `checkpoint` | **yes** | records the PreCompact observation into `History` (`LastPreCompactTS`, `LastPreCompactSession`, `AwaitingCompactStart = true`, `PreCompactTimeoutMs` from `pluginmanifest`, the wall time appended to `PreCompactWallMs`); `contract.WriteMarker`; then `svc.PreCompact` when non-nil **and** `mode.MayAct()`, timed into `obs.MetricCheckpointFin` (B-E); records the first 256 chars of any returned `CustomInstructions` into `History.PreCompactInstr`; returns its `hookio.Output` |
| `flush` | **yes** | `registry.End`; `ingest.CloseSession`; `svc.SessionEnd` when non-nil; `contract.WriteMarker`; `SketchSet.Save`; `Drain` |
| `status` | **yes** | `Data` = JSON of `StatusSnapshot` (below) |
| `mcp` | **yes** | when `svc.MCPInitialized` is nil, `Response{OK:false, Err:"mcp not built"}`; SP-13 replaces this route |
| `admin.ping` | **yes** | `{OK:true, Data:{"pid":…,"version":…,"uptime_seconds":…}}` |
| `admin.drain` | **yes** | `Drain`, returns the count |
| `admin.reload` | **yes** | config reload (below) |
| `admin.idle` | **yes** | `Idle().RunOnce(ctx, 5s)`, returns the names that ran |
| `admin.shutdown` | **yes** | `Stop` after replying |

**Why `observe.prompt` is a reply request.** 00-ARCH §2.4 names `UserPromptSubmit` additionalContext as one of the three cases that "set `"reply": true` and receive one NDJSON response line instead" — SP-08's verbatim-capture acknowledgement and SP-15's `grammar.FormatWarning` thrash line (§5.11: "one-line text injected via `UserPromptSubmit`") both need that return channel. It is still a `HotPath()` op, so the `spool` submode suppresses the connect and therefore the injection — exactly the documented "data is not lost; only freshness is." The reply deadline is a named constant:

```go
const promptReplyDeadline = 250 * time.Millisecond // well inside the manifest's 5 s UserPromptSubmit timeout
```

On timeout the client spools and writes `hookio.Empty()`; a prompt is never blocked on the daemon.

**Mode enforcement — normative, because §12.1 defines `degraded-passive` by what it turns off.** Every route consults `monitor.Mode()` at exactly the points below; there is no other mode check in `internal/daemon`.

| Route / step | `ModeFull` | `ModeDegradedPassive` | `ModeOff` |
|---|---|---|---|
| `ingest.Accept` (WAL append), `registry.Touch`, worker dispatch to `svc.ObserveTool` / `ObserveStop` | run | **run** — L0/L1 recording is explicitly preserved | skipped; ACK returned, nothing written |
| `svc.ObservePrompt` reply, `svc.SessionStart` output, sentinel emission, any `Output.HookSpecificOutput.AdditionalContext` | run | **suppressed** — `Output = hookio.Empty()` | suppressed |
| `svc.PreCompact` and any `Output.HookSpecificOutput.CustomInstructions` | run | **suppressed** — the route still records the timing observation and the marker | suppressed |
| scheduler-initiated checkpoints and drop reports (`IdleController` tasks registered by SP-12/SP-11 whose names begin `act.`) | run | **skipped by `RunOnce`** | skipped |
| SP-05's own idle tasks (`drain`, `sketches`, `metrics`) and `Drain` | run | run | run |
| `status`, `admin.*`, `mcp` | run | run | run — pull-based, cannot make anything worse (§12.1) |

`Mode.MayAct()` is the single predicate for rows 2–4; `Mode.MayRecord()` gates row 1. `IdleController.Register` therefore adopts one naming rule: a task whose name is prefixed `act.` is an *acting* task and `RunOnce` skips it when `!mode.MayAct()`; every other task is recording/maintenance and always runs. `TestDegradedPassiveSuppressesActingPaths` and `TestDegradedPassiveStillRecords` prove both halves.

The **`session.start` warm path**, in order (this ordering is normative — §5.21 says the monitor runs "before any other work"):

1. `registry.Ensure(event, now)`.
2. Build `contract.Env{ProjectRoot, Event, Cfg, Store: svc.Store, Log, Clock, History: contract.LoadHistory(statePath)}`.
3. `results, mode := monitor.RunAll(ctx, env)`.
4. `ipc.WriteState` with the resulting mode.
5. If `mode.MayAct()` and `svc.SessionStart != nil` → call it and take its `hookio.Output`; else `hookio.Empty()`.
6. If `mode.MayAct()` → mint a sentinel, persist it in `History`, and append `contract.RenderSentinel(s)` to `Output.HookSpecificOutput.AdditionalContext` (creating the `HSO` with `HookEventName: "SessionStart"` when absent). The sentinel is appended *after* any rehydration text so it never displaces content.
7. If the monitor just degraded, set `Output.SystemMessage` to the banner (`"Qompack: degraded to passive recording — <assertion> expected <x>, observed <y>. See /qompack:status."`).
8. `History.Sessions++` and persist. **The `session_start.fires` marker is *not* written here** — 00-ARCH §12.1 defines it as "a marker written at `SessionEnd`/`PreCompact` [and] found by the next `SessionStart`", so `contract.WriteMarker` is called by the `flush` and `checkpoint` routes only. Writing it at session start would make the assertion tautological and would stop detecting a host that has stopped firing the terminal hooks.
9. Return `Response{OK:true, Mode:mode, Hot:registry.HotMode(), Output:&out}`.

The **sentinel scan** on `observe.prompt`: when `History.Sentinel.Token != ""` and `!History.Sentinel.Observed`, a worker (not the reply path) calls `contract.ScanTranscriptTail(event.TranscriptPath, token, 256<<10)`. Found → `Observed = true`; not found → `Chances++`. Both persist to `state/contract.json`. This is off the reply path and therefore outside B-A and outside `promptReplyDeadline`.

**`StatusSnapshot` assembly** (the `status` route, and the only place these five come together):

- `Mode` = `monitor.Mode().String()`; `Contract` = `monitor.Report()`.
- `Hot` = `registry.HotMode().String()`; `Sessions` = `registry.Snapshot()`.
- `Latency` = one entry per `obs.Budgets(cfg)` metric name from `obs.Registry.Snapshot()`, plus `MetricHookObserved`; `Budgets` = the breaches from the most recent `CheckBudgets` call; `Counters` = the registry's counter map.
- `SpoolFiles` = `len(ipc.SpoolFiles(spoolDir))`.
- `LoudTail` = the last **five** non-empty lines of `.qompack/logs/LOUD.log`, read with a bounded 64 KiB tail seek (matching 00-ARCH §5.17 "the last five `Loud` messages"); a missing file yields an empty slice, never an error.
- `Extra` = `svc.StatusExtra(ctx)` when non-nil, else omitted. SP-14 renders this payload; SP-05 only produces it.

---

### `internal/daemon/reload.go`

At every idle tick and at every `session.start`, stat `<projectRoot>/.qompack/config.json`. If mtime or size changed since the last load, run `config.Load` with the same `Env`, apply the result, rewrite `state.bin`, and log at INFO with the changed dotted keys. **`store.chunk.*` changes are not applied mid-session** (§11.2: "it would fork the dedup space"): the new values are recorded in `state/config-pending.json`, `log.Loud("store.chunk.* change deferred to next SessionStart", …)` fires, and the in-memory config keeps the old chunk block. Validation violations fall back per leaf and surface through `logging.Loud` — `config.Load` already does this; the daemon only logs the returned `[]Warning`.

---

### `internal/daemon/sketchset.go`

```go
type SketchSet struct {
    mu sync.RWMutex
    Tried   *sketch.Bloom
    Touch   *sketch.CMS
    Explore *sketch.HLL
    Top     *sketch.MisraGries
    dirty   bool
}
```

`NewSketchSet(cfg)` constructs from `cfg.Sketches` (`bloom.capacity`, `bloom.fpRate`, `cms.epsilon`, `cms.delta`, `hll.registers`) and `NewMisraGries(64)`. `Load(root, log)` calls `sketch.Load` for `sketches/tried.bloom`, `touch.cms`, `explore.hll`; any error that is `core.ErrNotFound` or `core.ErrNotImplemented` is expected in wave 1 and logged at Debug, and the in-memory sketch is kept — the daemon must run correctly against SP-03's stub. Any *other* error is `Loud`ed and the sketch is kept in memory. `Save(root, log)` is the inverse and is a no-op when `!dirty`. `tried.bloom` is **never written by this method** (§3.3: it may be replaced only by `negknow.RebuildBloom`); `Save` skips it and a unit test asserts the file is untouched.

---

### `internal/daemon/metrics.go`

One idle task and one helper, both trivial and both named here so they are not reinvented:

```go
func writeLatencyJSON(root string, m obs.Registry, cfg config.Config) error
func loudTail(root string, n int) []string
```

`writeLatencyJSON` renders `{"generated":<unixMilli>,"budgets":[{id,metric,limit_ms,gated,p50,p95,p99,p999,max,n}]}` for every `obs.Budgets(cfg)` entry plus `MetricHookObserved`, and writes it to `.qompack/metrics/latency.json` with `paths.WriteAtomic`. `loudTail` reads the last 64 KiB of `.qompack/logs/LOUD.log` and returns the last `n` non-empty lines, oldest first; a missing file returns an empty slice and no error. Both are called from the `metrics` idle task and from the `status` route.

---

### `internal/daemon/daemon.go` — `New`, `Run` and `Stop`

`New(o Options)`, after the `ErrOptionsUninitialized` check and the `Services` assembly described in `options.go`, additionally constructs the single per-daemon **contract monitor** — nothing else in the process constructs one:

```go
mon := contract.NewMonitor(o.Log, o.Metrics, filepath.Join(o.ProjectRoot, ".qompack", "state", "contract.json"))
for _, a := range contract.StandardAssertions() { _ = mon.Register(a) }
```

`Options` has no `Monitor` field and gains none: the monitor's lifetime is the daemon's, `monitor.Mode()` is the single mode source consulted by the handlers table and by `idleController.mode`, and `monitor.Report()` backs `StatusSnapshot.Contract`. `New` also seeds `idleController.mode = mon.Mode` and registers SP-05's three idle tasks.

`Run(ctx)`:

1. `addr, err := ipc.Resolve(root)`; `ErrAddrTooLong` → `Loud` and return (the client stays spool-only, which is a correct degraded system).
2. `AcquireLock`; `ErrLockHeld` → return nil (another daemon owns this project; this is success, not failure).
3. `SketchSet.Load`; `ingest` start; `NewServer`; `ipc.WriteState`; delete `run/spawn.lock`.
4. `Drain(ctx)` — pick up everything spooled while we were down.
5. Start the heartbeat ticker (30 s), the idle ticker (`min(30s, idleExitSeconds/10)`), and `server.Serve`.
6. Idle tick: `Idle().Notify(registry.LastActivity())`; if `IsIdle(now)` → `RunOnce(ctx, 2*time.Second)`; if `registry.Live() == 0` and the zero-live duration exceeds `cfg.Runtime.Daemon.IdleExitSeconds` (default 1800) → `Stop` and return.
7. Return when `ctx` is done.

`Stop(ctx)`: stop accepting; drain the ring with a 5 s bound; `ingest.Close()` (flush and close WAL handles); `SketchSet.Save`; write `metrics/latency.json`; `ipc.RemoveState`; `server.Close`; `lock.Release`. Idempotent via `sync.Once`.

---

### `internal/obs/budgets.go` (the only file SP-05 adds to SP-01's package)

Collision rule: SP-05 creates **exactly one new file** here and deletes **exactly one stub method** (`CheckBudgets` on SP-01's concrete registry, if SP-01 shipped it as a stub in `registry.go`). Nothing else in `internal/obs` is touched.

```go
// Limits whose value is not a config key are fixed by 00-ARCHITECTURE §2.4 and quoted in place.
const (
    limitL0Ingest      = 2 * time.Millisecond   //nomagic:allow §2.4 B-B "p99 < 2 ms"
    limitL0Process     = 50 * time.Millisecond  //nomagic:allow §2.4 B-C "p99 < 50 ms" (soft)
    limitCheckpointFin = 2 * time.Second        //nomagic:allow §2.4 B-E "p99 < 2 s"
    limitMCPToolCall   = 250 * time.Millisecond //nomagic:allow §2.4 B-F "p95 < 250 ms"
)

func Budgets(cfg config.Config) []Budget {
    ba := time.Duration(cfg.Runtime.HotPath.BudgetMs) * time.Millisecond
    return []Budget{
        {"B-A", MetricHookControlled, ba,                 true,  "hook_controlled — client main() entry → exit (connect + write + ACK)"},
        {"B-B", MetricL0Ingest,       limitL0Ingest,      true,  "l0_ingest — daemon read → WAL append returned"},
        {"B-C", MetricL0Process,      limitL0Process,     false, "l0_process — WAL → fully chunked, stored, DAG/sketches updated (async)"},
        {"B-D", MetricHookWall,       0,                  false, "hook_wall — includes host process creation"},
        {"B-E", MetricCheckpointFin,  limitCheckpointFin, true,  "checkpoint_finalize — PreCompact entry → exit"},
        {"B-F", MetricMCPToolCall,    limitMCPToolCall,   true,  "mcp_tool_call — request → response"},
    }
}
```

`CheckBudgets(cfg)` is **stateful and window-oriented**: it holds `map[string]int` of consecutive breach counts, and the daemon calls it exactly once per closed 512-sample window. For each `Gated` budget with `Limit > 0` and `Snapshot().N > 0`, compare `P99` (`P95` for B-F, per its row) against `Limit`; on breach increment the count and emit `BudgetBreach{Budget: b.ID, Observed: p, Limit: b.Limit, Windows: count}`; on no breach reset the count to 0. B-D is never returned as a breach — its `Limit` is 0 and it is reported, never gated.

---

### `internal/contract/*` — the G9.3 monitor

**`producers.go`.** A package-level `map[ID]bool` behind a mutex, with `DeclareProducer`, `HasProducer`, `ResetProducers`. Declaration is idempotent.

**`history.go`.** `LoadHistory(statePath)` reads `.qompack/state/contract.json`, returning a zero `History` with `Version: 1` on any error (a missing file is the first-run case). `SaveHistory` uses `paths.WriteAtomic`. `History.Last` is capped at the 9 most recent `Result`s.

**`marker.go`.** `MarkerPath(projectRoot)` = `<projectRoot>/.qompack/run/marker.json`. `WriteMarker(projectRoot, sess, now)` writes `{"session":"<id>","ts":<unixMilli>}` with `paths.WriteAtomic` (it is a mutable one-record view, not an append-only artifact, so `WriteAtomic` is the correct primitive and `.qompack/run/` is outside the §3.3 append-only set). It is called by the daemon's `flush` and `checkpoint` routes — the SessionEnd and PreCompact terminal hooks — and by nothing else. The `session_start.fires` assertion reads it; it is never cleared, because it is overwritten by the next terminal hook and the assertion only ever compares its recorded session id against the current one.

**`sentinel.go`.**
`MintSentinel(sess, now)` → token `"qompack-contract-" + core.HashBytes("qompack.sentinel.v1", []byte(string(sess)+strconv.FormatInt(int64(now),10))).Short()` (12 hex chars).
`RenderSentinel(s)` → exactly `"<!-- qompack-contract-probe " + s.Token + " -->"` on its own line — an HTML comment so it is inert in any rendering path and unmistakable in a transcript.
`ScanTranscriptTail(path, token, tailBytes)` opens the file, seeks to `max(0, size-tailBytes)`, reads to EOF, and reports `bytes.Contains`. A missing or unreadable file returns `(false, err)`; the caller treats that as "no observation yet", never as a failure.

**`monitor.go`.** `RunAll(ctx, env)`:

1. If `env.Cfg.Runtime.Mode` is `"off"` → return `(nil, ModeOff)` immediately without running anything.
2. If it is `"passive"` → run the assertions for reporting, then force `ModeDegradedPassive`.
3. If it is `"full"` → run the assertions for reporting, then force `ModeFull` (an escape hatch for a user whose host build breaks our detection).
4. `"auto"` (the default) → run every registered assertion, each inside a `recover` (a panicking assertion yields `Result{OK:false, Severity:SevWarn, Observed:"assertion panicked"}` — an assertion bug must not degrade a session), collect results, then:
   - any `Result` with `!OK && Severity == SevCritical` → `Degrade(reason, results)`, mode = `ModeDegradedPassive`, `History.CleanRuns = 0`;
   - otherwise, if the current persisted mode is `ModeDegradedPassive`: `History.CleanRuns++`; when it reaches **2**, `Restore("two consecutive clean assertion runs")` and mode = `ModeFull`; below 2, mode stays `ModeDegradedPassive`;
   - otherwise mode = `ModeFull`.
5. Persist `History` (mode, reason, `Last`), record `obs.Counter("contract.fail."+id)` per failure and `obs.Gauge("contract.mode")`, and return.

`Degrade(reason, results)` performs §12.1's ordered steps exactly: `log.Loud("qompack degraded to passive recording", "reason", reason, "assertion", id, "expected", exp, "observed", obs)` → persist to `state/contract.json` (`Mode`, `DegradedReason`, `DegradedSince`) → set the in-memory mode → `obs.Counter("contract.degrade").Inc()`. `Restore(reason)` is symmetric and equally `Loud`. Both are idempotent: degrading while degraded re-logs only if the reason changed.

**`assertions.go` — `StandardAssertions()`**, the nine assertions of 00-ARCH §12.1 in that order, each with its severity **and the exact observation it makes**. Severity in the middle column is the severity used *when the producer is declared*; a missing producer is handled once, by `gated`, below.

| ID | Severity when producer declared | Check |
|---|---|---|
| `session_start.fires` | `SevCritical` | `run/marker.json` exists and its session id differs from the current one → OK. Absent → `History.StartsWithoutMarker++`; fail only at `>= 2` ("absence across two sessions ⇒ fail"). First-ever session (`History.Sessions == 0`) → OK, `Observed: "first-session"` |
| `session_start.source_compact` | `SevCritical` | if `History.AwaitingCompactStart` (a PreCompact was observed for this session id) then `env.Event.Source == "compact"` → OK, else fail with `Expected:"compact"`, `Observed:env.Event.Source`. Clears the flag either way. Not awaiting → OK, `Observed:"no-precompact-pending"` |
| `hook.additional_context_delivered` | `SevCritical` | `History.Sentinel.Observed` → OK. Not observed and `Chances < 2` → OK, `Observed:"not-yet-observed"`. Not observed and `Chances >= 2` → fail |
| `precompact.has_time_to_write` | `SevCritical` on timeout, `SevWarn` at >60% | p99 of `History.PreCompactWallMs` (the 64 newest samples) against `History.PreCompactTimeoutMs`: `>= 100%` → fail `SevCritical`; `> 60%` → fail `SevWarn`; else OK. `PreCompactTimeoutMs == 0` (never observed) → OK, `Observed:"timeout-unknown"` |
| `precompact.custom_instructions_accepted` | `SevWarn` (advisory by design, §8.5) | `History.PreCompactInstr` empty → OK, `Observed:"no-instructions-emitted"`. Otherwise take its **first line of ≥ 24 characters** as the probe phrase and `ScanTranscriptTail(TranscriptPath, phrase, 256<<10)`: found → OK; absent → warn, `Observed:"instruction phrase not found in transcript tail"` |
| `hook.payload_shape` | `SevCritical` | `env.Event.HookEventName != ""` and `env.Event.SessionID != ""` and (`CWD != ""` or `TranscriptPath != ""`) and no `Extra` key collides with a known field name |
| `mcp.server_registered` | `SevWarn` | `History.MCPInitialized` |
| `transcript.readable` | `SevWarn` | `TranscriptPath` non-empty, `os.Stat` succeeds, and the last non-empty line parses as JSON. Empty path → OK, `Observed:"no-transcript-path"` |
| `plugin.root_resolves` | `SevWarn` | `os.Getenv("CLAUDE_PLUGIN_ROOT")`: empty → OK `Observed:"unset"`; set → the directory exists and contains `bin/qompack` or `bin/qompack.exe` |

**The not-yet-implemented rule, implemented once, in a wrapper — this is the normative mechanism:**

```go
func gated(id ID, sev Severity, desc string, check func(context.Context, Env) Result) Assertion {
    return Assertion{ID: id, Severity: sev, Description: desc,
        Check: func(ctx context.Context, e Env) Result {
            if !HasProducer(id) {
                // §12.1: an assertion whose producer is absent from the build must never degrade
                // the session. A CI test asserts a freshly built develop reports ModeFull.
                return Result{ID: id, OK: true, Severity: SevInfo,
                    Expected: desc, Observed: "not-yet-implemented", TS: now(e)}
            }
            r := check(ctx, e); r.ID = id
            if r.Severity == 0 && !r.OK { r.Severity = sev }
            return r
        }}
}
```

Every one of the nine is constructed through `gated`. The **five** SP-05 always declares (`session_start.fires`, `session_start.source_compact`, `hook.payload_shape`, `transcript.readable`, `plugin.root_resolves`) are live from wave 1; the **four** §12.1 names as later-wave (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`) report `not-yet-implemented` until their owning subplan binds its `Services` seam.

**On `hook.additional_context_delivered` specifically.** SP-05 mints, emits and scans for the sentinel from wave 1, so the *mechanism* is complete and unit-tested here (`TestSentinelRoundTrip`, `TestSentinelNotObservedGivesTwoChances`, which call the check directly with the producer declared). But §12.1 assigns its producer to SP-11, so the assertion itself stays `not-yet-implemented` — and therefore cannot degrade a session — until SP-11's rehydrator binds `Services.Rehydrate`. Do **not** invent a SevWarn-then-escalate intermediate state: `gated` short-circuits before the check runs when the producer is absent, so any such state would be unreachable code contradicting its own wrapper. `TestAdditionalContextGatedUntilRehydrator` pins both halves.

**Nil tolerance in `Env`.** `Env.Store` is a wave-1 stub and may be nil; no assertion dereferences it. `Env.Event` may be a zero value when `self-test` synthesizes an `Env` from persisted `History`; every assertion that reads `Event` treats empty fields as "no observation yet" and returns OK rather than failing. `Env.Clock` nil → `core.SystemClock()`.

---

### `internal/cli/*` — the subcommand surface

SP-05 replaces the **bodies** of SP-01's six no-op hook entry points and adds three subcommands. Every hook subcommand follows one shared skeleton in `cli/hookclient.go`:

```go
func runHook(op ipc.Op, reply bool, deadline time.Duration, args []string) int {
    ts := core.SystemClock().Now().UnixMilli()          // FIRST statement — the B-A origin
    defer recoverToZero()                                // §12.3 "any hook panic → recovered in cli, exit 0"

    root := resolveProjectRoot(nil)                      // env → process cwd walked to nearest .git → cwd
    st := ipc.ReadState(root, config.Defaults())         // one 32-byte read; no config.Load
    if st.Mode == contract.ModeOff { hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }

    ev, _, err := hookio.ReadEvent(os.Stdin, int64(st.MaxPayloadBytes)*4)
    if err != nil { logQuiet(err); hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }

    // The payload is authoritative for the project root, and it only arrives now. If it
    // disagrees with the pre-stdin guess, redo the two cheap resolutions against it.
    if r2 := resolveProjectRoot(&ev); r2 != root {
        log.Debug("project root re-resolved from payload", "was", root, "now", r2)
        root, st = r2, ipc.ReadState(r2, config.Defaults())
        if st.Mode == contract.ModeOff { hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }
    }

    req := ipc.Request{Op: op, Session: ev.SessionID, TS: core.UnixMilli(ts),
        Reply: reply, Event: &ev, Raw: rawExtras(args)}
    sp, _ := ipc.NewSpool(filepath.Join(root, ".qompack", "spool"))
    defer func() { _ = sp.(io.Closer).Close() }()

    addr, aerr := ipc.Resolve(root)
    if aerr != nil {                                     // ErrAddrTooLong → spool-only for this process
        _ = sp.Append(req); logQuiet(aerr)
        hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0
    }

    c := ipc.NewClientWithOptions(addr, sp, log, metrics, ipc.ClientOptions{
        ProjectRoot: root, State: st, Self: selfPath(), Clock: core.SystemClock(),
        Spawn: daemon.SpawnDetached,
    })
    defer c.Close()

    resp, _ := c.Send(context.Background(), req, deadline)
    out := hookio.Empty()
    if resp.Output != nil { out = *resp.Output }
    hookio.WriteOutput(os.Stdout, out)
    return 0                                             // ALWAYS
}
```

`SpoolWriter` is an interface (00-ARCH §5.4) with no `Close`; the concrete `*spool` has one, and the type assertion above is the guarded form (`if cl, ok := sp.(io.Closer); ok`) written compactly. `ipc.Client.Close()` closes the spool it was given, so on the path where a client is constructed the assertion is a no-op double close, which `*spool` tolerates via `sync.Once`.

The unexported helpers the skeleton uses all live in `internal/cli/hookclient.go` and are defined once:

- `recoverToZero()` — `defer`red `recover()` that writes `hookio.Empty()` to stdout if nothing has been written yet, logs the panic and stack at `Error`, and lets the caller return 0. This is §12.3's "any hook panic → recovered in `cli`, logged, `exit 0` with empty output".
- `selfPath() string` — `os.Executable()`; returns `""` on error, which disables lazy spawn rather than failing.
- `logQuiet(err error)` — appends to `.qompack/logs` **only if the log directory already exists**; a hook must never create directories on the error path of a failing project.
- `rawExtras(args []string) json.RawMessage` — builds `Request.Raw` from subcommand flags only: `{"subagent":true}` for `observe stop --subagent`, `{"trigger":"<manual|auto>"}` for `checkpoint`, and `nil` for every other subcommand. It never embeds the raw payload bytes; the parsed `Event` already carries them, and duplicating them would double the line size on the hot path.
- `resolveProjectRoot(ev *hookio.Event) string` — §3.3's order: `QOMPACK_PROJECT_ROOT` → (when `ev != nil`) the payload's `cwd`/`project_dir`, else the process `cwd`, walked upward to the nearest `.git` → that directory itself. Called twice per hook, once before stdin is read and once after, because the state file must be read before stdin (it decides whether to read stdin at all) but the payload is authoritative about which project this is.
- `log` and `metrics` are package-level values initialized lazily by `hookclient.go` to `logging.Nop()` and SP-01's registry constructor, so a hook that never fails allocates neither.

Per-subcommand wiring:

| Subcommand | Op | Reply | Deadline |
|---|---|---|---|
| `qompack observe tool` | `observe.tool` | no | `AckDeadline` (manifest timeout 5 s) |
| `qompack observe prompt` | `observe.prompt` | **yes** | `promptReplyDeadline` = 250 ms (manifest timeout 5 s) |
| `qompack observe stop` (`--subagent`) | `observe.stop` | no | `AckDeadline` (manifest timeout 5 s / 10 s subagent) |
| `qompack session-start` | `session.start` | **yes** | 10 s (manifest timeout 15 s) |
| `qompack checkpoint` | `checkpoint` | **yes** | 15 s (manifest timeout 20 s) |
| `qompack flush` | `flush` | **yes** | 15 s (manifest timeout 20 s) |

`qompack session-start` additionally calls `daemon.EnsureRunning(root, selfPath(), log, clk)` **before** `Send`, because §2.4 makes it the designated daemon starter and its hook timeout is generous. If the daemon fails to come up it still sends (which spools), writes `hookio.Empty()` and exits 0.

**`cli/daemon.go`** — `qompack daemon [--project <root>] [--foreground]`: loads config, builds the logger against `.qompack/logs`, `daemon.NewOptions`, `daemon.New`, installs a `signal.Notify` for `os.Interrupt`/`syscall.SIGTERM` that cancels the run context, and calls `Run`. Exits 0 on clean stop and on `ErrLockHeld`; exits 0 with a `Loud` line on any other error (a daemon that cannot start must not surface a non-zero exit through a lazy spawn).

**`cli/selftest.go`** — `qompack self-test [--json]`, **the only subcommand permitted to exit non-zero** (§2.3). It runs, in order: config load and validation; `.qompack/` writability; `paths.AppendOnly`/`CreateNew` guard behaviour; `ipc.Resolve`; daemon reachable or spawnable; a full IPC round trip using `admin.ping`; `Router.Ops()` coverage against `ipc.KnownOps()`; and `contract.StandardAssertions` executed against a synthetic `Env` built from the last persisted `History`. Output is a fixed-width table plus a summary line, or a JSON document under `--json` with `{checks:[{id,ok,severity,expected,observed,detail}], mode, exit}`. Exit code: `0` when no check failed at `SevCritical`, `1` otherwise.

**`cli/fault.go`** — the fault-injection seam used by the exit-0 test suite. `QOMPACK_FAULT` accepts a comma-separated list of `site[:arg]`. **Eleven sites, all concretely defined** (the assignment names daemon-down, spool-full, disk-full, corrupt-config and panic explicitly, so none of them may be left implicit):

| Site | Injection | Expected hook behaviour |
|---|---|---|
| `stdin-eof` | `hookio.ReadEvent` is handed an empty reader | `logQuiet`, empty JSON out, exit 0 |
| `stdin-garbage` | stdin is replaced with `{{{not json` | same |
| `oversize` | the event's `ToolResponse` is inflated to 4 MiB before encoding | externalized to a blob, or spooled; exit 0 |
| `daemon-down` | `ipc.Resolve` returns an address nothing is listening on and `ClientOptions.Spawn` is set to a no-op | one spool line, exit 0 |
| `spool-readonly` | the spool directory is made non-writable (POSIX `chmod 0o500`; Windows `FILE_ATTRIBUTE_READONLY` + a deny-write ACE) | `l0.dropped` +1, one `Loud`, exit 0 |
| `spool-full` | `spool.Append` returns `ErrSpoolFull` once the process has written `faultArg` lines (default 1) | identical to `spool-readonly`: drop, count, `Loud` once, exit 0 |
| `disk-full` | `spool.Append`, `logQuiet` and `ipc.WriteState` return `syscall.ENOSPC` from their first write | drop, count, `Loud` once, exit 0 — no retry loop, no panic |
| `state-corrupt` | `run/state.bin` is overwritten with 32 random bytes (bad CRC) | `ReadState` falls back to `config.Defaults()` silently; exit 0 |
| `config-corrupt` | `.qompack/config.json` is replaced with `{"store":{"chunk":{"min":`  | the hot path never parses config, so this is inert for hooks and exercised by `qompack daemon`/`self-test`: per-leaf fallback + `Loud` (§11.3); exit 0 |
| `panic:hook` | `runHook` panics immediately after `ReadEvent` | `recoverToZero` writes empty JSON, exit 0 |
| `panic:client` | `ipc.Client.Send` panics | recovered by `recoverToZero`, exit 0 |

`faultActive(site string) (arg string, on bool)` is checked at each named site; when `QOMPACK_FAULT` is unset the parsed map is nil and the check is a single nil-map lookup (~2 ns), which is why it may live on the hot path. `TestFaultSitesInertWhenUnset` asserts `faultActive` returns `false` for all eleven sites with the variable unset **and** that a hook run with it unset produces byte-identical output to one built with `-tags noinject`. The `security` CI job greps that the string `QOMPACK_FAULT` appears in exactly one non-test file, `internal/cli/fault.go`, and that `SpawnDetached` strips it from the child environment (it does, see `spawn.go`).

**`cmd/qompack/main.go`** — add `daemon` and `self-test` to the dispatch switch. `self-test` is the only case that returns a non-zero code; every other case returns 0.

---

### `test/bench/hotpath/main.go` — the B-A / B-B / B-D harness

A standalone Go program (not `go test -bench`) because it must measure **real process spawns** (§7).

```
devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon --json out.json [--project <dir>]
```

1. Create a temp project (or use `--project`), `go build` the real binary into it, set `QOMPACK_PROJECT_ROOT` and `QOMPACK_IPC_ADDR` so the socket stays inside the temp dir.
2. Start a real daemon as a child process and wait for `admin.ping`.
3. **Warm it**: send 2 000 `observe.tool` requests over a persistent connection carrying 40 MB of synthesized tool output in total (a deterministic generator seeded at 1, mixing `Read`, `Bash`, `Grep`, `Edit` payloads of 4 KB–256 KB), so the WAL, registry, ring and — once SP-06/SP-03/SP-07 have merged — the CMS, DAG and store are warm. In wave 1 those are stubs, so B-C is omitted from `budgets` and the reason is stated in the `notes` array; the wave-2 verification re-runs this harness against the real implementations.
4. Measure the **process-creation floor** first: spawn `qompack version` 200 times and record its wall-time distribution. `qompack version` prints `qompack <semver>` and exits 0 without reading stdin, resolving a project root, or touching `.qompack/`; it is therefore the same binary, the same loader and the same OS process cost with none of the hook work. *Decision:* SP-05 adds `version` to `cmd/qompack/main.go`'s dispatch switch if SP-01 did not already ship it — a two-line case, and the only honest way to make the floor auditable.
5. Spawn the real `qompack observe tool` binary `N` times, one at a time, with a representative payload on stdin, timing each spawn with `time.Now()` around `cmd.Run()`. That interval is **B-D** (`hook_wall`, includes host process creation).
6. Derive **B-A** per sample, not per percentile: `B-A_i = max(0, B-D_i − floor_p50)`, then compute the percentiles over the adjusted samples. Subtracting p99 from p99 would be statistically meaningless (`p99(X−Y) ≠ p99(X) − p99(Y)`), and B-A is the number CI gates on, so the derivation is stated in the artifact as `"b_a_method": "per-sample subtraction of spawn_floor_ms.p50"` and `spawn_floor_ms` is reported in full. This is the same honesty rule §2.4 applies to B-D.
7. Measure **B-E**: spawn `qompack checkpoint` 50 times with a `PreCompact` payload and record the wall time. The CI gate names B-E ("fails the build if B-A p99 ≥ 15 ms **or B-E p99 ≥ 2 s**"), so the harness must produce the number even though SP-10 owns what happens inside the route; in wave 1 `svc.PreCompact` is nil and the measurement is the route's floor, which is the correct baseline to regress against later.
8. Read the daemon-side histograms via the `status` op for **B-B** and the observed/estimated `hook.controlled` pair.
9. Emit `out.json`:

```json
{"platform":"windows/amd64","n":2000,"b_a_method":"per-sample subtraction of spawn_floor_ms.p50",
 "spawn_floor_ms":{"n":200,"p50":6.1,"p99":11.4},
 "notes":["B-C not measured in wave 1: the processing seams are stubs"],
 "budgets":[{"budget_id":"B-A","n":2000,"p50":2.1,"p95":4.0,"p99":6.8,"p999":9.9,"max":14.2,"limit_ms":15,"pass":true},
            {"budget_id":"B-B","n":2000,"p50":0.10,"p95":0.31,"p99":0.62,"p999":1.1,"max":1.9,"limit_ms":2,"pass":true},
            {"budget_id":"B-D","n":2000,"p50":8.2,"p95":13.9,"p99":18.1,"p999":24.0,"max":31.5,"limit_ms":null,"pass":null},
            {"budget_id":"B-E","n":50,"p50":14.0,"p95":22.5,"p99":31.0,"p999":31.0,"max":31.0,"limit_ms":2000,"pass":true}]}
```

10. Exit non-zero if any `Gated` budget with a non-null `limit_ms` fails — B-A, B-B and B-E. B-D has `limit_ms: null` and can never fail the run. `time.Sleep` is permitted in this directory only (`devtool lint` exempts `test/bench`).

`tools/devtool/task_benchhotpath.go` adds the `bench-hotpath` task (SP-01 declared the name; SP-05 implements it), forwarding flags and printing a human summary.

---

### `.github/workflows/ci.yml` — the bench gate

Replace the placeholder `bench-gate` job body with:

```yaml
  bench-gate:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x' }
      - run: go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-${{ matrix.os }}.json
      - uses: actions/upload-artifact@v4
        with: { name: bench-${{ matrix.os }}, path: bench-${{ matrix.os }}.json }
```

The task exits non-zero when B-A p99 ≥ 15 ms or B-E p99 ≥ 2 s, which fails the job. B-D is uploaded and never gated. `nightly.yml` gets the same job at `--iterations 5000`. Mark `bench-gate` a required check on `develop` and `main` (00-ARCH §8: required "from the end of wave 1 onward").

---

## Test plan (TDD)

Tests are written **before** the implementation in each commit and must fail for the stated reason first. Assertions use `testify/require`; structural diffs use `go-cmp`; property tests use `pgregory.net/rapid`; every test that touches time uses `testutil.FakeClock`. Fixtures live under `testdata/golden/contracts/ipc/` and `testdata/golden/contracts/contract/`.

### `internal/ipc` — addressing and framing (`addr_test.go`, `frame_test.go`)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestProjectHash12Deterministic` | — | `C:\Proj\Foo` and `C:/Proj/Foo/` | identical 12-char lowercase hex; `len == 12` |
| `TestProjectHash12CaseFoldsOnWindowsAndDarwin` | — | `/Proj/Foo` vs `/proj/FOO` | equal on `windows`/`darwin`, different on `linux` |
| `TestResolveUnixPrefersXDG` | `XDG_RUNTIME_DIR=/run/user/1000` | root `/p` | `Addr{AddrUnixSocket, "/run/user/1000/qompack/<h12>.sock"}` |
| `TestResolveUnixFallsBackToTempDir` | `XDG_RUNTIME_DIR` unset | root `/p` | path under `os.TempDir()/qompack-<uid>/` |
| `TestResolveUnixSunPathGuard` | `XDG_RUNTIME_DIR` = a 120-char dir | root `/p` | path is `<TempDir>/qp-<h8>.sock`, `len <= 100` |
| `TestResolveUnixSunPathImpossible` | `TMPDIR` = a 200-char dir, `XDG` unset | root `/p` | `ErrAddrTooLong` |
| `TestResolveWindowsPipeName` | `windows` only | root `C:\p` | `\\.\pipe\qompack.<h12>` |
| `TestEncodeRequestByteExact` | fixed `Request` | golden `observe_tool.ndjson` | encoded bytes equal the golden byte-for-byte, exactly one trailing `\n`, no `\u003c` escaping |
| `TestDecodeRequestRoundTrip` | — | 20 rapid-generated `Request`s | `DecodeRequest(EncodeRequest(r)) == r` (`cmp.Diff` empty) |
| `TestLineReaderRejectsOversize` | `maxLine=64` | a 100-byte line then a 10-byte line | first returns `ErrLineTooLong`; second returns the 10-byte line (resynchronization works) |
| `FuzzDecodeRequest` | seed corpus: the golden, `{}`, `` , 1 MiB of `a` | arbitrary bytes | never panics; returns a value or an error |

### `internal/ipc` — state file (`state_test.go`)

| Test | Input | Expected |
|---|---|---|
| `TestStateRoundTrip` | `State{ModeDegradedPassive, HotSpool, 5, 8, true, true, 1048576, 4242, 1730000000000}` | `ReadState` returns exactly that; file is exactly 32 bytes |
| `TestStateMissingFallsBackToDefaults` | no file | `ReadState(root, config.Defaults())` returns `ModeFull`, `HotSync`, `AckDeadlineMs == 8`, `ConnectDeadlineMs == 5` |
| `TestStateBadCRCFallsBack` | write a valid record, flip byte 12 | returns the defaults, no error, no log |
| `TestStateShortFileFallsBack` | 17 bytes | returns the defaults |
| `TestStateWriteIsAtomic` | 500 concurrent `WriteState` + `ReadState` under `-race` | every read yields a valid CRC; no torn record |
| `BenchmarkReadState` | warm file cache | budget note: must be **< 100 µs/op**, since it is the only I/O on the hot path before `dial` |

### `internal/ipc` — client, spool, server (`client_test.go`, `spool_test.go`, `server_test.go`)

| Test | Setup | Expected |
|---|---|---|
| `TestSendACKPath` | in-process server ACKing every request | `Response{OK:true, Hot:HotSync}`, `err == nil`, spool file does not exist |
| `TestSendNAKSwitchesToSpool` | server returns `NAK` | `Response{OK:false, Hot:HotSpool}`, `err == nil`, the request **is** in the spool, and a second `Send` never dials (assert with a listener that counts accepts: count stays 1) |
| `TestSendDaemonDownSpoolsAndReturnsNilError` | no listener | `Response{OK:false}`, `err == nil`, one line in `client-<pid>.ndjson`, `Spawn` called exactly once |
| `TestSendNeverReturnsError` | rapid: 200 random `Request`s × {no listener, listener that closes immediately, listener that hangs past the deadline, listener that returns a bogus byte} | `err == nil` in every case; total wall time per call `< 3 ×` ack deadline |
| `TestSendHotSpoolSkipsConnect` | `State.Hot = HotSpool`, listener counting accepts | accepts stay 0 for `observe.tool`; `session.start` **does** connect |
| `TestSendModeOffDoesNothing` | `State.Mode = ModeOff` | no dial, no spool write, `Response{OK:true, Mode:ModeOff}` |
| `TestSendReplyPath` | server returns a `Response` with `Output.HookSpecificOutput.AdditionalContext = "hi"` | client returns that output verbatim |
| `TestSendOversizeExternalizes` | payload 2 MiB, `MaxPayloadBytes` 1 MiB | the line on the wire is `< 4 KiB` and carries `{"blob":…}`; the blob file exists with the full 2 MiB |
| `TestSpoolAppendOnly` | pre-create the spool file with content | after `Append`, the original content is intact and the new line is at the end; opening with `O_TRUNC` is rejected by `paths.AppendOnly` |
| `TestSpoolWriteFailureDropsAndLoudsOnce` | spool dir chmod `0o500` (Windows: read-only attribute) | `Append` returns nil, `l0.dropped` counter is 1 per event, exactly one `Loud` line for 10 events |
| `TestServerRoutesAndACKs` | router with a handler for `admin.ping` | one ACK byte for a non-reply request; one NDJSON line for `Reply:true` |
| `TestServerUnknownOpNAKs` | empty router, no fallback | one `NAK` byte, connection stays open, a following valid request still ACKs |
| `TestServerHandlerPanicIsContained` | handler that panics | `NAK`, server still serving, `ipc.route.panic` counter 1 |
| `TestServerMultiplexesLines` | 3 requests on one connection | 3 ACK bytes in order |
| `TestServerConcurrentClients` | 64 goroutines × 50 requests, `-race` | 3 200 ACKs, no data race, no dropped request |
| `TestUnixSocketPermissions` | `!windows` | socket mode is `0600`, directory `0700` |
| `TestWindowsPipeACLRejectsOtherUser` | `windows`, skipped unless `QOMPACK_TEST_ACL=1` | the SDDL string contains the current SID and begins `D:P` |
| `TestStaleUnixSocketReclaimed` | leave a socket file with no listener | `NewServer` removes it and binds successfully |
| `BenchmarkServerRoundTrip` | warm in-process server | budget: **p99 < 2 ms** for the daemon-side portion of B-A |

### `internal/daemon` (`lock_test.go`, `registry_test.go`, `ingest_test.go`, `drain_test.go`, `idle_test.go`, `budget_test.go`, `daemon_test.go`, `options_test.go`)

| Test | Setup | Expected |
|---|---|---|
| `TestAcquireLockExclusive` | two `AcquireLock` on one root | second returns `ErrLockHeld` |
| `TestStaleLockReclaimed` | lock file with pid 999999 and a 10-minute-old heartbeat, no listener | `AcquireLock` succeeds and rewrites the file |
| `TestLiveLockNotReclaimed` | a real listener at the recorded addr | `ErrLockHeld` even with an old heartbeat (the dial probe wins) |
| `TestHeartbeatUpdatesMtime` | `FakeClock` +60 s | `daemon.hb` mtime advances |
| `TestRegistryNewSessionResetsHotMode` | set `HotSpool`, then `Ensure` a **new** session id | `HotMode() == HotSync`; `Ensure` of the *same* id leaves it `HotSpool` |
| `TestRegistryEvictsEndedOverMax` | `maxSessions=2`, 3 sessions with 1 ended | the ended one is evicted; the 2 live ones remain |
| `TestRegistryAllLiveOverMaxKeepsAll` | `maxSessions=2`, 3 live | all 3 kept, one `Loud` line |
| `TestIngestWALIsExactBytes` | `Accept` with a known line | `spool/wal-<sess>.ndjson` contains exactly that line |
| `TestIngestRingFullSpillsToSpool` | ring capacity 2, no workers, 5 requests | 5 WAL lines, 3 spool lines, `l0.ring_full == 3`, `Accept` never blocks (assert each call `< 1 ms` on the `FakeClock`) |
| `TestIngestACKPrecedesProcessing` | worker blocked on a channel | the ACK is observed by the client while the worker is still blocked |
| `TestDrainIsIdempotent` | a spool file with 10 lines; `Drain` twice | 10 dispatches total, not 20; file deleted after the first pass |
| `TestDrainResumesAfterCancel` | 1 000 lines, cancel after ~200 | second `Drain` dispatches the remaining ~800, total exactly 1 000 |
| `TestDrainResolvesBlobs` | a spooled line with `{"blob":…}` plus the blob file | the handler receives the full payload; the blob file is deleted |
| `TestDrainSurvivesCorruptLine` | 3 valid lines, 1 line of `{{{`, 1 valid | 4 dispatches, `drain.file_error` 1, file still deleted |
| `TestIdleRunsByPriority` | tasks `c`(30) `a`(10) `b`(20) | `ran == ["a","b","c"]` |
| `TestIdleRespectsBudget` | task A consumes the whole budget | `ran == ["a"]`, B never runs, no error |
| `TestIdleTaskPanicIsolated` | task A panics, B does not | `ran == ["b"]`, `idle.task.panic` 1, `RunOnce` returns nil error |
| `TestIsIdleUsesDetectAfterSeconds` | `detectAfterSeconds=120`, `FakeClock` | false at +119 s, true at +120 s |
| `TestBreachDetectorTransitionsAfterThreeWindows` | limit 15 ms, `need=3`; feed 512×20 ms three times | `NoTransition, NoTransition, ToSpool` |
| `TestBreachDetectorResetsOnCleanWindow` | 512×20 ms, 512×1 ms, 512×20 ms, 512×20 ms | no `ToSpool` (the clean window reset the counter) |
| `TestBreachDetectorRevertsAfterThreeCleanWindows` | in spool mode, 3×512 samples of 1 ms | `ToSync` on the third |
| `TestSpoolOnBreachFalseDoesNotTransition` | `spoolOnBreach=false` | mode stays `HotSync`; the WARN log and counter still fire |
| `TestHotModeTransitionWritesStateAndNAKs` | force `ToSpool` | `state.bin` shows `hot=1`; the next `observe.tool` gets `NAK` |
| `TestServicesAllNil` | `Services{}` entirely nil; drive all 13 ops | every response `OK:true` (except `mcp`, which is `OK:false` with `Err:"mcp not built"`); zero panics; WAL holds every hot-path event |
| `TestObservePromptRepliesWithinDeadline` | `svc.ObservePrompt` returns an `Output` with `AdditionalContext:"x"` | the client receives it; a variant whose `ObservePrompt` blocks past `promptReplyDeadline` returns `hookio.Empty()` and spools, and the hook still exits 0 |
| `TestDegradedPassiveSuppressesActingPaths` | monitor forced to `ModeDegradedPassive`; drive `session.start`, `observe.prompt`, `checkpoint` with all `Services` bound | every `Output.HookSpecificOutput` is nil (no `additionalContext`, no `customInstructions`); `svc.PreCompact` and `svc.SessionStart` are **not** called; an `act.`-prefixed idle task does not appear in `RunOnce`'s `ran` |
| `TestDegradedPassiveStillRecords` | same, then drive 20 × `observe.tool` | 20 WAL lines; `svc.ObserveTool` called 20 times; `drain`/`sketches`/`metrics` idle tasks all ran |
| `TestModeOffSkipsIngest` | monitor forced to `ModeOff` | ACK returned, WAL file never created, `svc.ObserveTool` never called |
| `TestNAKDuplicateIsDedupedOnDrain` | force `ToSpool`, send one `observe.tool` (daemon WALs it and NAKs, client spools the same line), then `Drain` | the handler sees the request exactly **once**; both files are consumed |
| `TestHandleOverridesDefaultRoute` | `opts.Handle(OpStatus, custom)` before `New` | the custom handler runs, not the default |
| `TestHandleOnBareOptionsIsSafe` | `Options{}` literal, `Handle(...)` | no panic; `New` returns `ErrOptionsUninitialized` |
| `TestBindRunsInOrderAndDeclaresProducers` | two `Bind`s, second sets `Rehydrate` | both ran in order; `contract.HasProducer(CAdditionalContext)` true |
| `TestSketchSetNeverWritesTriedBloom` | pre-write `tried.bloom` with known bytes; `Save` | the file's bytes and mtime are unchanged |
| `TestConfigReloadDefersChunkChange` | change `store.chunk.target`, touch mtime, idle tick | in-memory chunk block unchanged; `state/config-pending.json` written; one `Loud` line |
| `TestIdleExitWithZeroSessions` | `idleExitSeconds=1`, `FakeClock` | `Run` returns after the tick past the threshold; lock and `state.bin` removed |
| `TestRunReturnsNilWhenLockHeld` | daemon A running | daemon B's `Run` returns nil promptly |
| `BenchmarkIngestAccept` | warm WAL handle | budget **B-B: p99 < 2 ms** |

### `internal/contract` (`monitor_test.go`, `assertions_test.go`, `sentinel_test.go`)

| Test | Setup | Expected |
|---|---|---|
| `TestFreshBuildReportsModeFull` | no producers declared beyond SP-05's five; a well-formed `Event`; empty `History` | `mode == ModeFull`; the four later-wave assertions (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`) report `OK:true, SevInfo, Observed:"not-yet-implemented"` — **this is the CI test §12.1 demands** |
| `TestDeclaredProducerSetMatchesArchitecture` | `daemon.DeclareProducers(&Services{})` on a clean registry | exactly the five always-declared ids are present and the four §12.1 later-wave ids are absent; with `Services{Rehydrate: fn, MCPInitialized: fn, PreCompact: fn}` all nine are present |
| `TestCriticalFailureDegrades` | declare `CPreCompactTiming`; `PreCompactWallMs` p99 at 100% of timeout | `mode == ModeDegradedPassive`; exactly one `Loud` line; `state/contract.json` carries the reason and `DegradedSince` |
| `TestTwoCleanRunsRestore` | start degraded; run clean twice | run 1 → still `ModeDegradedPassive`, `CleanRuns==1`; run 2 → `ModeFull` and a `Loud` restore line |
| `TestOneCleanRunDoesNotRestore` | start degraded; one clean run | still `ModeDegradedPassive` |
| `TestRuntimeModeOffShortCircuits` | `runtime.mode="off"` | `RunAll` returns `(nil, ModeOff)`; zero assertions executed (assert with a counting assertion) |
| `TestRuntimeModePassiveForces` | `runtime.mode="passive"`, all assertions clean | `ModeDegradedPassive`, results still populated |
| `TestPanickingAssertionDoesNotDegrade` | register an assertion that panics | `mode == ModeFull`; its result is `OK:false, SevWarn, Observed:"assertion panicked"` |
| `TestSessionStartFiresNeedsTwoMisses` | no marker, `History.Sessions=1` then `2` | first run OK (`StartsWithoutMarker==1`), second run fails |
| `TestSourceCompactEvaluatedOnFollowingStart` | mark `AwaitingCompactStart`; `Event.Source="startup"` | fail, `Expected:"compact"`, `Observed:"startup"`; the flag is cleared |
| `TestSentinelRoundTrip` | write a transcript whose tail contains the rendered sentinel | `ScanTranscriptTail` true; the assertion reports OK |
| `TestSentinelNotObservedGivesTwoChances` | `DeclareProducer(CAdditionalContext)`; transcript without the token | `Chances` 1 → OK; `Chances` 2 → fail `SevCritical` |
| `TestAdditionalContextGatedUntilRehydrator` | run with and then without `DeclareProducer(CAdditionalContext)` | without → `OK:true, SevInfo, Observed:"not-yet-implemented"` and the check body never executes (assert with a counting closure); with → the real check runs at `SevCritical` |
| `TestPreCompactTimeoutUnknownIsOK` | `History.PreCompactTimeoutMs == 0`, producer declared | OK, `Observed:"timeout-unknown"` |
| `TestCustomInstructionsProbePhrase` | `History.PreCompactInstr` with a 40-char first line; transcript containing / not containing it | OK / `SevWarn` fail |
| `TestMarkerIsWrittenByFlushAndCheckpointOnly` | grep the `contract` and `daemon` sources; then drive `session.start` alone against an empty `run/` | no `marker.json` after `session.start`; one after `flush`; one after `checkpoint` |
| `TestSentinelRenderIsAnInertComment` | — | exactly `<!-- qompack-contract-probe <12hex> -->`; token is 12 lowercase hex chars |
| `TestHookPayloadShapeRejectsEmptySession` | `Event{HookEventName:"PostToolUse"}` | fail, `Observed` names the missing field |
| `TestPluginRootUnsetIsOK` | `CLAUDE_PLUGIN_ROOT` unset | OK, `Observed:"unset"` |
| `TestHistoryPersistsAcrossMonitors` | monitor A degrades; construct monitor B on the same path | B reports `ModeDegradedPassive` before running anything |
| `TestDegradeIsIdempotent` | degrade twice with the same reason | one `Loud` line; two with different reasons → two lines |

### `internal/obs` (`budgets_test.go`)

| Test | Expected |
|---|---|
| `TestBudgetsMatchArchitectureTable` | six budgets in order B-A…B-F, with the exact `Clock` strings from §2.4 and `Gated` true for B-A, B-B, B-E, B-F |
| `TestBudgetALimitFollowsConfig` | `runtime.hotPath.budgetMs = 9` → B-A limit is 9 ms |
| `TestCheckBudgetsCountsConsecutiveWindows` | three breach calls → `Windows` 1, 2, 3; one clean call → the next breach is `Windows` 1 |
| `TestCheckBudgetsNeverGatesBD` | B-D observed at 900 ms | no `BudgetBreach` for B-D |
| `TestCheckBudgetsIgnoresEmptyHistograms` | `N == 0` | no breaches |

### `test/e2e` (`daemon_e2e_test.go`, `faultinject_test.go`)

Uses SP-01's `testutil.Project` and the real built binary.

| Test | Setup | Expected |
|---|---|---|
| `TestE2EHookRoundTrip` | build the binary, `qompack session-start`, then 50 × `qompack observe tool` | every exit code 0; `wal-<sess>.ndjson` has 50 lines; `admin.ping` reports one live session |
| `TestE2ELazySpawn` | no daemon; run `qompack observe tool` once | exit 0, one spool line; within 1.5 s a daemon is listening; a second `observe tool` ACKs and the spool is drained to the WAL |
| `TestE2EIdleExit` | `runtime.daemon.idleExitSeconds=1`, `flush`, wait for exit | the process exits; `run/daemon.lock` and `run/state.bin` are gone |
| `TestE2ESelfTestExitsZeroOnHealthy` | healthy project | `qompack self-test --json` exits 0; JSON parses; `mode == "full"` |
| `TestE2ESelfTestExitsNonZeroOnCritical` | pre-degraded `state/contract.json` | exits 1 with a banner naming the assertion |
| `TestE2ESpoolSubmodeEndToEnd` | force `state.bin` `hot=1` | `observe tool` writes to the spool without connecting (assert daemon accept counter unchanged), exit 0, and the next idle tick drains it |
| `TestHooksExitZeroUnderFaults` | table over {6 hook subcommands} × {`daemon-down`, `spool-readonly`, `spool-full`, `disk-full`, `state-corrupt`, `config-corrupt`, `stdin-garbage`, `stdin-eof`, `oversize`, `panic:hook`, `panic:client`} | **every one of the 66 combinations exits 0** and writes valid (possibly empty) JSON to stdout |
| `TestFaultSitesInertWhenUnset` | `QOMPACK_FAULT` unset | `faultActive` false for all eleven sites; a hook run's stdout is byte-identical to a `-tags noinject` build's |
| `TestSelfTestIsTheOnlyNonZeroExit` | run every subcommand in the dispatch table under `daemon-down` | only `self-test` may exit non-zero |

### Benchmarks tied to a named budget

| Benchmark | Budget | Gate |
|---|---|---|
| `test/bench/hotpath` B-A | **B-A p99 < 15 ms** (§8.1, §11.3) | CI hard fail on ubuntu, macos, windows |
| `test/bench/hotpath` B-B | B-B p99 < 2 ms (§2.4) | CI hard fail |
| `test/bench/hotpath` B-E | **B-E p99 < 2 s** (§11.3 L4, §2.4) | CI hard fail |
| `test/bench/hotpath` B-D | reported only (§2.4) | artifact + PR comment |
| `BenchmarkReadState` | hot-path prelude < 100 µs/op | `benchstat` warn at >10%, fail at >25% |
| `BenchmarkIngestAccept` | B-B | as above |
| `BenchmarkServerRoundTrip` | daemon-side B-A portion < 2 ms p99 | as above |
| `BenchmarkEncodeRequest` | < 5 µs/op for a 4 KB payload | as above |

### Fixtures to create

- `testdata/golden/contracts/ipc/observe_tool.ndjson` — the byte-exact request line above.
- `testdata/golden/contracts/ipc/response_reply.ndjson` — a `Reply` response with an `Output`.
- `testdata/golden/contracts/ipc/state_degraded.bin` — a 32-byte state record, `mode=1`, `hot=1`.
- `testdata/golden/contracts/contract/history_degraded.json` — a persisted degraded `History`.
- `testdata/golden/contracts/contract/transcript_with_sentinel.jsonl` — 40 lines, the sentinel in the tail.
- `testdata/corpora/ipcframe/` — fuzz seeds for `DecodeRequest` (the golden, `{}`, empty, 1 MiB of `a`, invalid UTF-8).

---

## Commit plan

Exactly **7 commits**, in this order, each on `feat/sp05-daemon-ipc-and-hot-path`. Every commit compiles and passes `go run ./tools/devtool test` for the packages it touches, plus `gofumpt -l` empty and `golangci-lint run` clean.

**Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1

```
feat(ipc): address resolution, NDJSON framing, and the hot-path state record

Windows named pipes and Unix sockets resolve from a plain sha256 of the normalized
project root per 00-ARCHITECTURE §2.4, with the sun_path guard macOS requires. The
32-byte state record exists so the hot path pays one small read instead of a config
load before it decides whether to connect at all.

Refs: SP-05, §8.1, 00-ARCHITECTURE §2.4
```

- [ ] Write `internal/ipc/addr_test.go`, `frame_test.go`, `state_test.go` and the fuzz target first; run `go test ./internal/ipc/...` and confirm they **fail to compile** (symbols absent).
- [ ] Add `internal/ipc/addr.go`, `addr_unix.go`, `addr_windows.go`, `frame.go`, `op.go`, `state.go`, `doc.go`.
- [ ] Add fixtures `testdata/golden/contracts/ipc/{observe_tool.ndjson,response_reply.ndjson,state_degraded.bin}` and `testdata/corpora/ipcframe/*`.
- [ ] Run `go test ./internal/ipc/... -race`, `go test -run Fuzz -fuzz FuzzDecodeRequest -fuzztime 30s ./internal/ipc`, `go run ./tools/devtool fmt lint vet`.
- [ ] All tests green before committing.

### Commit 2

```
feat(ipc): thin client with spool fallback, detached spawn seam, and the server

Send never returns an error a hook could propagate: every failure path ends in a
spool append and exit 0, because a hook that fails takes observability with it.
The 1-byte ACK converts "usually delivered" into "provably enqueued"; NAK is the
in-band hint that carries the spool submode back to an in-flight client.

Refs: SP-05, §7.1, §12, 00-ARCHITECTURE §2.4, D4
```

- [ ] Write `internal/ipc/client_test.go`, `spool_test.go`, `server_test.go` first, including the `rapid` property test `TestSendNeverReturnsError`; confirm they fail.
- [ ] Add `internal/ipc/spool.go`, `client.go`, `server.go`, `listen_unix.go`, `listen_windows.go`.
- [ ] Add `github.com/Microsoft/go-winio` to `go.mod` (it is on §2.5's closed allowed list; no other dependency may be added).
- [ ] Flip off the `t.Skip`s in SP-01's `ipctest` conformance suite and make it pass (Rule W-1: leftover skips are a merge blocker).
- [ ] Run `go test ./internal/ipc/... -race -count=2`; `GOOS=windows go build ./...` and `GOOS=darwin go build ./...` to prove both build tags compile.

### Commit 3

```
feat(daemon): singleton lock, session registry, WAL ingest queue, and drain

The WAL is the durability boundary: the ACK is written after the append returns and
before any indexing work, so a daemon crash costs freshness and never data. Drain is
idempotent and resumable because it records consumed offsets, which is what lets the
idle tick pick up whatever the client spooled while we were down.

Refs: SP-05, §8.1, 00-ARCHITECTURE §2.4
```

- [ ] Write `internal/daemon/lock_test.go`, `registry_test.go`, `ingest_test.go`, `drain_test.go` first; confirm they fail.
- [ ] Add `internal/daemon/lock.go`, `lock_unix.go`, `lock_windows.go`, `spawn.go`, `spawn_unix.go`, `spawn_windows.go`, `registry.go`, `ingest.go`, `drain.go`, `sketchset.go`.
- [ ] Run `go test ./internal/daemon/... -race`; run `go run ./tools/devtool lint` and confirm the import-graph check still passes (daemon is a composition root; nothing may import it).

### Commit 4

```
feat(daemon): extension seams, idle controller, latency budgets, sync->spool fallback

The op-routing table, IdleController.Register and the late-bound Services set exist
so wave-3 subplans wire in without editing daemon internals and without colliding
inside one package. The §8.1 "degrade to async queue-and-drain" clause is implemented
as an observable state transition after three consecutive 512-sample breach windows,
not as an implicit slowdown.

Refs: SP-05, §8.1, §8.4, §12, 00-ARCHITECTURE §2.4, §5.4
```

- [ ] Write `internal/daemon/options_test.go`, `idle_test.go`, `budget_test.go`, `daemon_test.go` and `internal/obs/budgets_test.go` first, including `TestServicesAllNil`; confirm they fail.
- [ ] Add `internal/daemon/options.go`, `idle.go`, `budget.go`, `handlers.go`, `reload.go`, `metrics.go`, `daemon.go`.
- [ ] Add `internal/obs/budgets.go`; delete SP-01's stub `CheckBudgets` method (and nothing else in `internal/obs`).
- [ ] Run `go test ./internal/daemon/... ./internal/obs/... -race -count=2`; `go run ./tools/devtool lint` (the `nomagic` pass must accept the four annotated budget constants and reject an unannotated one — verify by temporarily removing an annotation).

### Commit 5

```
feat(contract): G9.3 assertion set, fail-loud degradation, two-clean-run restore

Every assertion is a real observation rather than a version check, and an assertion
whose producer is absent from the build reports SevInfo/not-yet-implemented so a
wave-1 build cannot degrade itself into passivity and silently disable the paths it
is meant to be testing.

Refs: SP-05, G9.3, §9, §12
```

- [ ] Write `internal/contract/monitor_test.go`, `assertions_test.go`, `sentinel_test.go` first — starting with `TestFreshBuildReportsModeFull`, which is the CI test §12.1 explicitly demands; confirm they fail.
- [ ] Add `internal/contract/contract.go`, `monitor.go`, `assertions.go`, `history.go`, `sentinel.go`, `marker.go`, `producers.go`.
- [ ] Add fixtures `testdata/golden/contracts/contract/{history_degraded.json,transcript_with_sentinel.jsonl}`.
- [ ] Flip off the `t.Skip`s in SP-01's `contracttest` conformance suite (if present) and make it pass.
- [ ] Run `go test ./internal/contract/... -race -count=2`; run `go test ./internal/... ` to confirm nothing else regressed.

### Commit 6

```
feat(cli): session-start dispatch, daemon start, thin hook clients, and self-test

Hook subcommands become thin clients that stamp the B-A origin as their first
statement and exit 0 on every path; session-start is the designated daemon starter
and runs the contract monitor before any other work, per the split-ownership table.
self-test is the only subcommand permitted a non-zero exit.

Refs: SP-05, §7.3, §12, 00-ARCHITECTURE §2.3, §5.21
```

- [ ] Write `test/e2e/daemon_e2e_test.go` and `test/e2e/faultinject_test.go` first (the full 66-combination table plus `TestFaultSitesInertWhenUnset`); confirm they fail.
- [ ] Add `internal/cli/hookclient.go`, `daemon.go`, `sessionstart.go`, `selftest.go`, `fault.go`; replace the six no-op hook bodies; add `daemon`, `self-test` and (if SP-01 did not ship it) `version` to `cmd/qompack/main.go`.
- [ ] Run `go test ./test/e2e/... -race`; run every hook subcommand by hand with an empty stdin and confirm `echo $?` / `$LASTEXITCODE` is 0.
- [ ] Run `go run ./tools/devtool ci-local`.

### Commit 7

```
test(sp05): hot-path bench harness, three-platform bench gate, and fixtures

B-A is measured with real process spawns against a warm daemon because that is the
only honest way to measure it; B-D is reported alongside it and never gated, since
process creation is the host's cost and hiding it inside B-A would be the exact
unmeasured-system sin the design document indicts.

Refs: SP-05, §8.1, §11.3, 00-ARCHITECTURE §7, §8
```

- [ ] Write `test/bench/hotpath/report_test.go` first — percentile computation against a hand-checked 512-sample fixture, the per-sample floor subtraction, and a golden of the `out.json` shape including `b_a_method`, `spawn_floor_ms`, `notes` and the B-A/B-B/B-D/B-E rows; confirm it fails (symbols absent).
- [ ] Add `test/bench/hotpath/main.go`, `report.go`, `payload.go`; add `tools/devtool/task_benchhotpath.go`.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-local.json` on the Windows dev machine and confirm `B-A.pass == true` and `B-E.pass == true`.
- [ ] Update `.github/workflows/ci.yml` (`bench-gate` on ubuntu/macos/windows at `n=2000`) and `.github/workflows/nightly.yml` (`n=5000`); mark `bench-gate` a required check on `develop` and `main`.
- [ ] Add `BenchmarkReadState`, `BenchmarkEncodeRequest`, `BenchmarkIngestAccept`, `BenchmarkServerRoundTrip` and record their numbers into `testdata/bench-baseline.txt` via `benchstat`.
- [ ] Push and confirm CI is green on all three platforms before opening the merge into `develop`.

---

## Subagent strategy

This subplan is **heavy**. Partition it across four parallel subagents plus a main session. The commit plan stays strictly sequential and is executed **only by the main session** — subagents produce files and test results, never commits.

**Main session keeps (never delegated):**

- The seam design: `internal/daemon/options.go` (`Options`, `Services`, `Handle`, `Bind`, `DeclareProducers`, the context accessors) and `internal/ipc/op.go` (`Op` constants, `Router`). Every subagent depends on these, so they are written **first**, in the main session, before any subagent starts, and handed to each subagent as read-only context.
- `internal/ipc/state.go` and `internal/obs/budgets.go` — small, cross-cutting, and touched by three subagents.
- All `git` operations, all seven commits, the branch, and the CI workflow edits.
- Integration: resolving any signature drift between subagents, running `go run ./tools/devtool ci-local` after each merge of subagent output.

**Subagent A — transport (`internal/ipc` minus `op.go`/`state.go`).**
Files: `addr.go`, `addr_unix.go`, `addr_windows.go`, `frame.go`, `spool.go`, `client.go`, `server.go`, `listen_unix.go`, `listen_windows.go`, plus their `_test.go` files and the `ipc` fixtures.
Returns: the file set, the output of `go test ./internal/ipc/... -race -count=2`, the fuzz run summary, and a one-paragraph note on any platform-specific behaviour it could not test locally.
Feeds commits 1 and 2.

**Subagent B — daemon core (`internal/daemon` minus `options.go`/`budget.go`/`handlers.go`).**
Files: `lock.go`, `lock_unix.go`, `lock_windows.go`, `spawn.go`, `spawn_unix.go`, `spawn_windows.go`, `registry.go`, `ingest.go`, `drain.go`, `sketchset.go`, `reload.go`, `metrics.go`, plus tests.
Returns: the file set, `go test ./internal/daemon/... -race` output, and the measured `BenchmarkIngestAccept` p99 against B-B.
Feeds commit 3. **Must not** touch `Services` or the router; it consumes them through the frozen `options.go` the main session wrote.

**Subagent C — contract monitor (`internal/contract`, complete).**
Files: `contract.go`, `monitor.go`, `assertions.go`, `history.go`, `sentinel.go`, `marker.go`, `producers.go`, tests, and the two `contract` fixtures.
Returns: the file set, `go test ./internal/contract/... -race -count=2` output, and an explicit confirmation that `TestFreshBuildReportsModeFull` passes with only SP-05's **five** always-declared producers registered.
Feeds commit 5. It is fully independent of A and B — it imports only the foundation, `hookio` and `store` — so it can run start-to-finish in parallel with them.

**Subagent D — harness and fault injection (`test/bench/hotpath`, `test/e2e`, `tools/devtool`).**
Files: `test/bench/hotpath/{main.go,report.go,payload.go}`, `tools/devtool/task_benchhotpath.go`, `test/e2e/{daemon_e2e_test.go,faultinject_test.go}`.
Returns: the file set, a local `bench-local.json` from the dev machine, and the 66-row pass/fail table from `TestHooksExitZeroUnderFaults`.
Feeds commits 6 and 7. It starts **after** the main session has written `internal/cli/fault.go`'s `faultActive` signature and the subcommand table (both are small and land in the first hour), because its tests drive them.

**Integration order and rules.** A, B, C start together; D starts once the `cli` skeleton exists. The main session merges A's output first (commits 1–2), then B's (commit 3), then writes `budget.go`/`handlers.go`/`daemon.go` itself (commit 4, because that file is where A's and B's work actually meets and a subagent cannot see both halves), then merges C (commit 5), then the `cli` surface (commit 6), then D (commit 7). Any subagent that believes it needs a signature change to a file it does not own **stops and reports** rather than editing — Rule W-3 applies inside this subplan exactly as it applies between subplans. No subagent may add a Go module dependency; only `go-winio` is added, by the main session, in commit 2.

---

## Exit criteria

### Quoted verbatim from Qompack.md

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking. — §8.1

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms. — §10 Phase 1 (SP-05 owns and satisfies the second clause; the ratio clause belongs to SP-06/SP-08)

> - Hook p99 latency < 15ms (L0), < 2s (L4) — §11.3

> Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently. — §12

> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today. — §7.1

### Local, measurable criteria — all must hold on the branch head

- [ ] `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon` reports **B-A p99 < 15 ms**, **B-B p99 < 2 ms** and **B-E p99 < 2 s** on ubuntu-latest, macos-latest **and** windows-latest in CI, with the artifact uploaded, `spawn_floor_ms` and `b_a_method` present, and B-D reported but not gated.
- [ ] `go test ./... -race` green on ubuntu and macos; `go test ./... -count=2` green on windows.
- [ ] Line coverage ≥ **75%** for `internal/ipc`, `internal/daemon`, `internal/contract` and the SP-05 files in `internal/cli` (the §6.4 floor for "everything else"), measured on the merged Linux profile.
- [ ] `gofumpt -l` prints nothing; `golangci-lint run` clean; `go vet ./...` clean; `staticcheck` clean; the `nomagic` pass clean with exactly **five** annotated allowances in the whole subplan — four in `internal/obs/budgets.go` (`limitL0Ingest`, `limitL0Process`, `limitCheckpointFin`, `limitMCPToolCall`) and one in `internal/daemon/ingest.go` (`ringCapacity = 4096`, a queue depth that collides with the forbidden chunk-size literal). No other forbidden literal appears outside `*_test.go`.
- [ ] The import-graph check passes: `ipc` imports only foundation + `hookio` + `contract`; `contract` imports only foundation + `hookio` + `store`; nothing imports `daemon` or `cli`.
- [ ] The `security` job passes: zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` and only `unix`; `os/exec` only in `internal/daemon`, `internal/cli`, `tools/`.
- [ ] `TestHooksExitZeroUnderFaults` passes all 66 combinations (6 hook subcommands × 11 fault sites, including `spool-full` and `disk-full`); `TestFaultSitesInertWhenUnset` and `TestSelfTestIsTheOnlyNonZeroExit` pass.
- [ ] `TestFreshBuildReportsModeFull` passes — a freshly built `develop` with SP-05 merged reports `ModeFull`, and the **four** later-wave assertions report `not-yet-implemented`; `TestDeclaredProducerSetMatchesArchitecture` pins the five/four split against 00-ARCH §12.1.
- [ ] `TestServicesAllNil` passes — every op is answered without panic with a fully nil `Services`.
- [ ] `TestDegradedPassiveSuppressesActingPaths`, `TestDegradedPassiveStillRecords` and `TestModeOffSkipsIngest` pass — the three-mode state machine is enforced at exactly the sites listed in the mode-enforcement table and nowhere else.
- [ ] `TestMarkerIsWrittenByFlushAndCheckpointOnly` passes — the `session_start.fires` marker is produced by the terminal hooks, per 00-ARCH §12.1, never by `session.start` itself.
- [ ] `TestBreachDetectorTransitionsAfterThreeWindows` and `TestBreachDetectorRevertsAfterThreeCleanWindows` pass, and `TestHotModeTransitionWritesStateAndNAKs` proves the transition is observable in `state.bin`, in the NAK frame, in the WARN log and in the `status` payload.
- [ ] SP-01's `ipctest` conformance suite has zero remaining `t.Skip`s (Rule W-1).
- [ ] `bench-gate` is configured as a required check on `develop` and `main`.
- [ ] `git log --format=%B develop..HEAD` contains no `Co-Authored-By`, `Signed-off-by`, `Generated with`, or `🤖`; the commit count is exactly 7.

---

## Done checklist

- [ ] Branch `feat/sp05-daemon-ipc-and-hot-path` was cut from `develop` **after** SP-01 merged, and all work landed on it.
- [ ] `Qompack.md` is byte-identical to its state at branch point.
- [ ] Every constant, formula, table and threshold in the **Design context** section above has a corresponding implementation and at least one test: the 15 ms B-A budget, the 2 ms B-B budget, the 50 ms B-C soft budget, the 2 s B-E budget, the 250 ms B-F budget, the 512-sample window, the 3 breach windows, the 1 MiB line limit, the 8 ms ACK deadline, the 5 ms connect deadline, the 250 ms prompt reply deadline, the 1800 s idle exit, the 8-session cap, the 120 s idle detection, the 100-byte `sun_path` guard, the 12-hex-char project hash, the `0x06`/`0x15` frame bytes, the 32-byte state record, the 4096-entry ring, the 64 MiB WAL/spool caps, the 90 s lock staleness window with its 30 s heartbeat, the nine assertion IDs with their five/four producer split, the two-chance sentinel, the two-clean-run restore, and the three-mode state machine with its mode-enforcement table.
- [ ] Placeholder scan: `grep -rniE "TBD|TODO|FIXME|XXX|implement appropriately|add error handling|similar to|handle edge cases" internal/ipc internal/daemon internal/contract internal/cli internal/obs/budgets.go test/bench/hotpath test/e2e` returns nothing.
- [ ] Type consistency verified against the **Interface contract** section: `ipc.Addr`, `ipc.Request`, `ipc.Response`, `ipc.Op`, `ipc.Client`, `ipc.SpoolWriter`, `ipc.Server`, `ipc.Handler`, `daemon.Daemon`, `daemon.Options`, `daemon.Options.Handle`, `daemon.IdleController`, `daemon.SessionRegistry`, `contract.ID`, `contract.Severity`, `contract.Result`, `contract.Mode`, `contract.Assertion`, `contract.Env`, `contract.Monitor`, `contract.NewMonitor`, `contract.StandardAssertions`, `obs.BudgetBreach` — each matches 00-ARCHITECTURE §5.2/§5.4/§5.19 exactly, with no changed or removed member and no interface method added.
- [ ] No method was added to any interface owned by another subplan (Rule W-3); `internal/obs` received exactly one new file and one stub-method replacement; no other package outside SP-05's ownership was modified except the six hook bodies in `internal/cli`, `cmd/qompack/main.go` (the `daemon`, `self-test` and `version` dispatch cases only), `tools/devtool`, and the two CI workflows.
- [ ] Only `github.com/Microsoft/go-winio` was added to `go.mod`; the runtime dependency list of §2.5 is otherwise unchanged.
- [ ] Commit count is between 5 and 8 — verified as exactly **7** with `git rev-list --count develop..HEAD`.
- [ ] No commit message, merge commit, tag message or PR body contains `Co-Authored-By` or any other attribution trailer.
- [ ] Every commit message follows Conventional Commits with a `Refs:` footer naming SP-05, the gap IDs and the `Qompack.md` sections.
- [ ] CI is green on the branch across `verify`, `test`, `cover`, `crossbuild`, `bench-gate`, `plugin-validate`, `security` and `docs`.
- [ ] Self-review complete: every "Out of scope" row was respected, and no file under `internal/observer`, `internal/store`, `internal/sketch`, `internal/chunk`, `internal/canon`, `internal/symbols`, `internal/dag`, `internal/scheduler`, `internal/checkpoint`, `internal/rehydrate`, `internal/mcp`, `internal/negknow`, `internal/eval` or `internal/commands` was created or modified.

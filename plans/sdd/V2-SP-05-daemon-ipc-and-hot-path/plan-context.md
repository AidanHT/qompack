# SP-05: L0 substrate: resident daemon, IPC transport, thin hook client, the <15ms p99 budget, async queue-and-drain, and the contract monitor

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

## Implementation spec

### Global rules for this subplan

1. All work happens on `feat/sp05-daemon-ipc-and-hot-path`, cut from `develop` with SP-01 already merged. Never commit to `develop` directly. Never modify `Qompack.md`.
2. Import discipline (00-ARCH §3.2, CI-enforced): `ipc` may import **only** the foundation (`core`, `paths`, `config`, `logging`, `obs`) plus `hookio` and `contract`. `contract` may import the foundation plus `hookio` and `store`. `daemon` and `cli` are composition roots and may import anything; nothing may import them.
3. `net` is permitted **only** in `internal/ipc`, and only `net.Dial`/`net.Listen` on `"unix"` — never `"tcp"`. `os/exec` is permitted only in `internal/daemon` (detached self-spawn), `internal/cli`, and `tools/`. The `security` CI job asserts both.
4. The `nomagic` pass forbids the literals `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` outside `internal/config/defaults.go`, `*_test.go`, and `//nomagic:allow <reason>` lines. Every deadline, budget and window size in this subplan comes from `config.Config` or from a named constant in `internal/obs/budgets.go` annotated with its §2.4 row.
5. Every package that takes time takes a `core.Clock`. `time.Sleep` is banned outside `test/bench` (`devtool lint` greps for it). Use `time.NewTicker`/`time.After` inside `select` with a context, and `testutil.FakeClock` in tests.
6. No file outside `.qompack/` (project) and `~/.qompack/` (global) is ever written.

---


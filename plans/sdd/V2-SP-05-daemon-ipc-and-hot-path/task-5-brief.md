# Task 5 — daemon composition: extension seams, idle controller, breach detector, op routes, reload, lifecycle (the plan's "Commit 4")

You are completing `internal/daemon` in `github.com/qompack/qompack` (worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`). Already landed: the full ipc transport (Tasks 1–2), the daemon building blocks — lock/spawn/registry/ingest/drain/sketchset (Task 3), and the full contract package — SessionHistory, LoadHistory/SaveHistory/HistoryPath, producers, marker, sentinel, real gated assertions, monitor runtime-mode handling (Task 4). **Read the actual code of all of these first; build against what they ship.**

Read IN ORDER:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md` — SP-01 inventory; shipped spellings win.
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md` — mission, out-of-scope (SP-05 provides seams; internal/observer & friends are OTHER subplans — never create them), global rules.
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-5-spec.md` — verbatim requirements: options.go seams spec, idle.go, budget.go (breach detector), handlers.go (the 13-route table, mode-enforcement table, session.start warm path, sentinel scan, StatusSnapshot), reload.go, metrics.go, daemon.go lifecycle, the obs/budgets section (SUPERSEDED — see rulings), the daemon test table, and the commit checklist (labelled "Commit 4" — that is THIS task's commit).
4. Existing source: `internal/daemon/` (all of it), `internal/ipc/`, `internal/contract/`, `internal/obs/` (registry.go, budgets.go — read carefully; you consume, never modify), `internal/logging/loud.go` (LastLoud ring), `internal/pluginmanifest/` (PreCompact timeout source), `internal/config/`.

## Binding rulings (controller decisions amending the plan text)

- **internal/obs is NOT touched.** SP-01 shipped Budgets()/CheckBudgets complete. The spec section "internal/obs/budgets.go" in task-5-spec.md is historical context only. Histogram names come from `obs.Budgets()` lookup by BudgetID (B-A "hook_controlled", B-B "l0_ingest", B-C "l0_process", B-E "checkpoint_finalize"); write one helper to resolve them. The observed-lower-bound histogram is a daemon-owned name: `hook_controlled_observed`. The `hotPathTailAllowance = 1ms` estimated-B-A constant lives in budget.go with its doc comment.
- **Options keeps SP-01's shape** (pointer-receiver Handle into the unexported handlers map; Handler(op)/Ops() accessors). Add: `NewOptions(projectRoot string, cfg config.Config) Options` seeding Log=logging.Nop(), Metrics=obs.New(clock), Clock=core.SystemClock(), Sketches=NewSketchSet(cfg); `Bind(fn func(*Services))` appending to an unexported binds slice; NO ipc.Router, NO ErrOptionsUninitialized (a bare Options literal is safe by construction — test that instead, per the table's TestHandleOnBareOptionsIsSafe adapted).
- **Services** per the spec's struct (Store/Ledger/Sketches/Graph/Grammar/Sched/Checkpoints + the nine nil-tolerant function seams + MCPInitialized + StatusExtra). `New` seeds it from Options fields, applies binds in order, then calls `DeclareProducers(svc)` (the function lives in THIS package, options.go, exactly per the spec's five-always/four-gated split — CSessionStartFires, CSessionStartSourceCompact, CHookPayloadShape, CTranscriptReadable, CPluginRootResolves always; CPreCompactTiming+CPreCompactCustomInstr iff PreCompact≠nil||Checkpoints≠nil; CAdditionalContext iff Rehydrate≠nil; CMCPRegistered iff MCPInitialized≠nil). Context accessors ServicesFrom/RegistryFrom/DaemonFrom via unexported ctxKey.
- **Route dispatch**: the daemon composes a single `ipc.Handler` passed to the server: look up the op in the Options map (defaults registered by New only where not already registered, so a later `Handle` call wins), unknown op → `Response{OK:false, Err:"unknown op: …"}`, panic in a handler → recovered to `Response{OK:false, Err:"panic: …"}` + counter `ipc_handler_panic` (the ipc server additionally NAKs on its own panics — belt and braces; don't remove either).
- **Mode source**: `New` constructs the ONE monitor (`contract.NewMonitor(log, metrics, <root>/.qompack/state/contract.json)` + Register all StandardAssertions) — no Options.Monitor field. idleController's mode func = monitor.Mode. Mode enforcement happens at EXACTLY the sites in the spec's mode-enforcement table via Mode.MayAct()/MayRecord().
- **History flow**: session.start builds `contract.Env{ProjectRoot, Event, Cfg, Store: svc.Store, Log, Clock, History: h}` where `h = contract.LoadHistory(contract.HistoryPath(root))` (concrete *SessionHistory). After RunAll and the warm-path steps, the daemon mutates h (Sessions count ++ / sentinel mint) and `contract.SaveHistory`. The checkpoint route records PreCompact observations into h and saves; the flush route WriteMarker + saves; the sentinel scan worker updates h.Sentinel and saves. Serialize history access behind one daemon-owned mutex (routes run concurrently). PreCompactTimeoutMs comes from internal/pluginmanifest (find its API; if the manifest exposes no PreCompact timeout, record 0 = unknown and note it in your report).
- **Marker discipline**: `contract.WriteMarker` is called by the `flush` and `checkpoint` routes ONLY — never session.start (spec explains why; the table's TestMarkerIsWrittenByFlushAndCheckpointOnly asserts it, and `TestDeclaredProducerSetMatchesArchitecture` moved into THIS task's test set).
- **metrics.go**: the `metrics` idle task and status route use the SHIPPED `obs.Registry.Persist(paths.Of(root))` for metrics/latency.json (do not write a custom writeLatencyJSON shape). `loudTail` uses the shipped `logging.LastLoud()` ring (last five, §5.17) — no file tailing.
- **StatusSnapshot** per the spec's struct; Latency map = one entry per obs.Budgets() Hist name (+ hook_controlled_observed) from Snapshot(); Budgets = most recent CheckBudgets result (cache it on the daemon); LoudTail via LastLoud; SpoolFiles count via ipc.SpoolFiles; Extra from svc.StatusExtra when non-nil.
- **Breach detector** per spec (512-sample ring, percentile by copy+sort, need=cfg.Runtime.HotPath.BreachWindows, window closure on a worker not the ACK path). It consumes the estimated `hook_controlled` values (observed + hotPathTailAllowance). On ToSpool: registry.SetHotMode, ipc.WriteState, WARN log, counter `hotpath_degraded`, and subsequent hot-path fire-and-forget requests get NAK (reply requests carry Hot:HotSpool). SpoolOnBreach=false → log+count only. Both deliberate-looking-bug comments from the spec (NAK duplicate dedup; ToSync starvation) go in as comments.
- **observe.prompt** is a reply request: WAL first, then synchronous svc.ObservePrompt when non-nil && MayAct(), `promptReplyDeadline = 250*time.Millisecond` named const; sentinel scan afterwards on a worker (ScanTranscriptTail 256<<10 tail), updating h.Sentinel (Observed / Chances++) + SaveHistory.
- **Wave-1 stub tolerance**: sketch Save/Load are ErrNotImplemented stubs — SketchSet.Load/Save (Task 3) already tolerate; daemon Run must not fail on them.
- **Run/Stop** exactly per the spec's ordered lists, including: ErrAddrTooLong → Loud + return nil-equivalent (a correct degraded system — return nil, not an error); ErrLockHeld → return nil; delete run/spawn.lock after listen; Drain on start; heartbeat 30s ticker; idle ticker min(30s, idleExitSeconds/10); idle-exit on zero live sessions past cfg.Runtime.Daemon.IdleExitSeconds; Stop idempotent via sync.Once (drain ring 5s bound, ingest close, sketches save, metrics persist, RemoveState, server close, lock release).
- **Reload** per spec: stat config.json mtime/size at idle tick + session.start; config.Load re-run; store.chunk.* deferred to state/config-pending.json + Loud; rewrite state.bin; log changed keys at INFO (dotted keys best-effort — a simple reflect/JSON diff of the two configs is fine).
- The stubs guard (test/guards) requires daemon constructor behavior stays compatible: `daemon.New(Options{})` must still succeed. Composition roots have no walked seam, so implementing Run for real is fine.
- Every duration/window from config or a §-annotated named constant (`ringCapacity` pattern from Task 3). No time.Sleep. Clock injected everywhere; FakeClock in tests.

## Tests

Implement every row of the daemon test table in task-5-spec.md not already landed by Task 3 (options/idle/budget/handlers/daemon rows: TestServicesAllNil (13 ops, fully nil Services, every response OK:true except mcp OK:false "mcp not built"), TestObservePromptRepliesWithinDeadline (+ blocking variant), TestDegradedPassiveSuppressesActingPaths, TestDegradedPassiveStillRecords, TestModeOffSkipsIngest, TestNAKDuplicateIsDedupedOnDrain, TestHandleOverridesDefaultRoute, TestHandleOnBareOptionsIsSafe (adapted: no panic, routes land, New succeeds), TestBindRunsInOrderAndDeclaresProducers (use contract.ResetProducers with t.Cleanup), breach-detector rows, TestSpoolOnBreachFalseDoesNotTransition, TestHotModeTransitionWritesStateAndNAKs, TestSketchSetNeverWritesTriedBloom (if not landed in Task 3), TestConfigReloadDefersChunkChange, TestIdleExitWithZeroSessions, TestRunReturnsNilWhenLockHeld, idle-controller rows incl. act.-prefix skipping, TestDeclaredProducerSetMatchesArchitecture, TestMarkerIsWrittenByFlushAndCheckpointOnly). In-process tests use QOMPACK_IPC_ADDR (Task 1's override) to keep endpoints in t.TempDir() — on Windows use a per-test uniquely named pipe.

## Process

- TDD: failing tests first (RED evidence), implement to green.
- Before committing: `go test ./internal/daemon/... -race -count=2`, `go test ./internal/... `, `go test ./test/guards/...`, GOOS=linux+darwin builds, devtool fmt/lint/vet, full `go test ./...` once.
- Exactly ONE commit, exact message below, no attribution trailers:

```
feat(daemon): extension seams, idle controller, sync->spool fallback

The op-routing table, IdleController.Register and the late-bound Services set exist
so wave-3 subplans wire in without editing daemon internals and without colliding
inside one package. The §8.1 "degrade to async queue-and-drain" clause is implemented
as an observable state transition after three consecutive 512-sample breach windows,
not as an implicit slowdown.

Refs: SP-05, §8.1, §8.4, §12, 00-ARCHITECTURE §2.4, §5.4
```

## Report

Full report → `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-5-report.md` (TDD evidence, files changed, self-review, concerns — especially any seam whose shape you had to adjust). Reply with ONLY: Status, commit SHA+subject, one-line test summary, concerns, report path.

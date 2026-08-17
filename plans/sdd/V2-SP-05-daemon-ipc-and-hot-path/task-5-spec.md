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

### `internal/obs` (`budgets_test.go`)

| Test | Expected |
|---|---|
| `TestBudgetsMatchArchitectureTable` | six budgets in order B-A…B-F, with the exact `Clock` strings from §2.4 and `Gated` true for B-A, B-B, B-E, B-F |
| `TestBudgetALimitFollowsConfig` | `runtime.hotPath.budgetMs = 9` → B-A limit is 9 ms |
| `TestCheckBudgetsCountsConsecutiveWindows` | three breach calls → `Windows` 1, 2, 3; one clean call → the next breach is `Windows` 1 |
| `TestCheckBudgetsNeverGatesBD` | B-D observed at 900 ms | no `BudgetBreach` for B-D |
| `TestCheckBudgetsIgnoresEmptyHistograms` | `N == 0` | no breaches |

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


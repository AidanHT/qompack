# Task 5 review — daemon composition (commit `115ba19d`, `feat(daemon): extension seams, idle controller, sync->spool fallback`)

Reviewed: `review-task5.diff` (`29585ce1..115ba19d`, one commit, 12 files, +2978/−235), the shipped source in
`internal/daemon/`, and the packages it consumes (`internal/ipc`, `internal/contract`, `internal/obs`,
`internal/config`, `internal/logging`, `internal/pluginmanifest`). Working tree clean; exactly one commit as
required; commit message matches the brief verbatim (no attribution trailers).

Verification I ran myself (read-only):

| Command | Result |
|---|---|
| `go test ./internal/daemon/ -count=1` | ok (2.9 s) |
| `go test ./internal/daemon/ -race -count=1` ×3 | ok, ok, ok |
| `go test ./internal/daemon/ -race -count=2` ×2 | ok, ok |
| `go test ./internal/daemon/ -race -run 'Lock\|Spawn' -count=10` ×2 | **FAIL both times** — pre-existing `internal/ipc` race, see "ipc race diagnosis" |
| `go vet ./internal/daemon/...` | clean |
| `go test ./test/guards/...` | ok (18.7 s) — `daemon.New(Options{})` still succeeds |

---

## VERDICT 1 — SPEC COMPLIANCE

### The 13-route table

| Op | Reply | Mode-enforcement site | WAL / history / marker side effects | Verdict |
|---|---|---|---|---|
| `observe.tool` | no | `acceptHotPathEvent` → `Mode().MayRecord()` (handlers.go:266) | `registry.Touch`, `ing.Accept` (WAL), worker → `svc.ObserveTool` (daemon.go:456) | **met** |
| `observe.prompt` | yes | `MayRecord()` gates ingest (handlers.go:291); `MayAct()` gates the seam (handlers.go:301) | WAL first, then synchronous `ObservePrompt` under `promptReplyDeadline`; sentinel scan on the ingest worker (daemon.go:473) | **met** |
| `observe.stop` | no | same as `observe.tool` | subagent decoded from `req.Raw` in `runIngested` (daemon.go:465) | **met** |
| `session.start` | yes | `MayAct()` gates `svc.SessionStart` and the sentinel mint | `LoadHistory` → `RunAll` → `WriteState` → seam → sentinel → `SessionCount++` → `SaveHistory`; **no marker** | **met** (two defects below: I-2, I-5) |
| `checkpoint` | yes | `MayAct()` gates `svc.PreCompact` (handlers.go:498) | history observation (`LastPrecompactTS`/`Session`/`AwaitingCompactStart`/`PrecompactTimeoutMs`/wall sample/`PrecompactInstr`), `WriteMarker`, B-E timing, `SaveHistory` | **met** |
| `flush` | yes | `MayRecord()` gates `svc.SessionEnd` (extra site, see M-3) | `registry.End`, `ing.CloseSession`, `WriteMarker`, `Sketches.Save`, `Drain` | **met**, but the `Drain` call is the Critical C-1 |
| `status` | yes | none (correct) | none | **met** |
| `mcp` | yes | none | `OK:false,"mcp not built"` when the seam is nil | **met** |
| `admin.ping` | yes | none | `{pid,version,uptime_seconds}`, `OK:true` unconditional | **met** — post-brief ruling honoured, pinned by `TestAdminPingAlwaysAnswersOK` |
| `admin.drain` | yes | none | `Drain`, returns count | **met**, also exposed to C-1 |
| `admin.reload` | yes | none | forced `reloadConfig` | **met**, but see I-4 (no `state.bin` rewrite) |
| `admin.idle` | yes | `RunOnce` applies the `act.` rule | `adminIdleBudget = 5s` | **met** |
| `admin.shutdown` | yes | none | replies, then async `Stop`; `runCancel` added so `Stop` unblocks `Run` | **met** |

Dispatch itself (`dispatchOp`, handlers.go:121): one composed `ipc.Handler`, context injection of
Services/Registry/Daemon, unknown-op → `OK:false,"unknown op: …"`, panic → `OK:false,"panic: …"` + `ipc_handler_panic`
(same counter name as the ipc server's own belt-and-braces recovery, both kept). NAK forcing applies to
fire-and-forget hot-path ops only; `observe.prompt` carries the hint via `Response.Hot`. All per ruling. **met**

### Mode-enforcement table

| Row | Verdict |
|---|---|
| ingest.Accept / registry.Touch / worker dispatch: run, run, skipped-with-ACK | **met**, with a nit: `registry.Touch` runs *before* the `MayRecord` gate, so under `ModeOff` the registry is still touched (M-2) |
| `ObservePrompt` reply / `SessionStart` output / sentinel / any AdditionalContext: suppressed outside `MayAct` | **met** — `TestDegradedPassiveSuppressesActingPaths` asserts nil HSO on all three routes |
| `PreCompact` + CustomInstructions suppressed, timing observation and marker still recorded | **met** |
| `act.`-prefixed idle tasks skipped by `RunOnce` | **met** (`idle.go:157`, two dedicated tests) |
| SP-05's own `drain`/`sketches`/`metrics` idle tasks always run | **met** — none carries the prefix; `TestDegradedPassiveStillRecords` asserts all three ran |
| `status`/`admin.*`/`mcp` always run | **met** |
| "there is no other mode check in `internal/daemon`" | **adapted** — `handleFlush` adds a `MayRecord()` gate on `svc.SessionEnd`, which the table does not list (M-3) |

`Mode.MayAct()`/`MayRecord()` are the only predicates used; ruling #24 (`RunAll("off")` forces `ModeOff`) is honoured
end-to-end — `TestModeOffSkipsIngest` drives `runtime.mode=off` through `session.start` and asserts
`monitor.Mode()==ModeOff`, no WAL file, seam never called.

### `DeclareProducers` five-always / four-gated

Verbatim per the ruling (options.go:141–157): `CSessionStartFires`, `CSessionStartSourceCompact`, `CHookPayloadShape`,
`CTranscriptReadable`, `CPluginRootResolves` unconditionally; `CPreCompactTiming`+`CPreCompactCustomInstr` iff
`PreCompact != nil || Checkpoints != nil`; `CAdditionalContext` iff `Rehydrate != nil`; `CMCPRegistered` iff
`MCPInitialized != nil`. Called by `New` after the binds, in order. `TestDeclaredProducerSetMatchesArchitecture`
asserts all nine positively and negatively, with `contract.ResetProducers` + `t.Cleanup`. **met**

### Other binding rulings

| Ruling | Verdict |
|---|---|
| `internal/obs` untouched | **met** — `git diff --stat` shows only `internal/daemon/*` |
| Histogram names via `obs.Budgets()` lookup (`histName`), `hook_controlled_observed` daemon-owned | **met** (metrics.go:102, handlers.go:56) |
| `hotPathTailAllowance = 1ms` in budget.go with doc comment | **met** (budget.go:19, `//nomagic:allow`) |
| Options keeps SP-01 shape; `NewOptions`/`Bind`; no Router, no `ErrOptionsUninitialized` | **met** — `TestHandleOnBareOptionsIsSafe` adapted exactly as the brief directs |
| Services: 7 struct fields + the nil-tolerant function seams + context accessors | **met** — nine function seams shipped (`ObserveTool`, `ObservePrompt`, `ObserveStop`, `SessionStart`, `SessionEnd`, `PreCompact`, `Rehydrate`, `MCPInitialized`, `StatusExtra`); the brief's "nine + MCPInitialized + StatusExtra" phrasing is ambiguous, but no tenth/eleventh seam is named anywhere in the spec's call sites, so this reading is right. `Rehydrate` is declared and producer-gated but never invoked by any route — correct for wave 1 (SP-11 owns the call site) |
| ONE monitor built in `New`, no `Options.Monitor` | **met** (daemon.go:153-157); `idleController.mode = monitor.Mode` |
| History behind one daemon mutex at `HistoryPath` | **met structurally** — exactly three `LoadHistory`/`SaveHistory` sites (handlers.go:345/351, 371/417, 488/523), all under `historyMu`; flush touches no history. See I-5 for the *width* of those critical sections |
| Daemon never writes `LastSessionID` | **met** — `grep -rn LastSessionID internal/` shows writes only in `contract/assertions.go` (`checkSessionStartFires`) |
| Ruling #26 contract observability counters | **met** — `contract_fail_<underscored id>` per failing assertion + `contract_mode_change`, underscore idiom, `contract` package untouched (handlers.go:428-448) |
| Marker only from flush/checkpoint | **met** — `TestMarkerIsWrittenByFlushAndCheckpointOnly` proves all three legs |
| Metrics via `obs.Registry.Persist` + `LastLoud` | **half met** — `loudTail` uses `logging.LastLoud()` ✅; the `metrics` idle task still writes a custom `writeLatencyJSON` shape to the same path `Persist` writes. **See I-1: this is a dropped ruling with a real consequence** |
| `StatusSnapshot` shape | **met** — Mode/Contract/Hot/Sessions/Latency(+`hook_controlled_observed`)/Budgets(cached)/Counters/SpoolFiles/LoudTail/Extra |
| Breach detector 512/3/NAK semantics | **met** — fixed `[512]time.Duration`, `need` from `cfg.Runtime.HotPath.BreachWindows`, ceiling nearest-rank p99 by copy+sort, window closure on `hotPathWorker` not the ACK path, `SpoolOnBreach=false` logs+counts without flipping |
| Both "looks-like-a-bug" comments | **met** — the NAK-duplicate rationale is in `dispatchOp`'s doc comment; the ToSync-starvation rationale lives in Task 3's `registry.go:145-150` (`Ensure`) rather than budget.go, which is acceptable placement |
| `observe.prompt` 250 ms + sentinel scan on a worker | **met** (`promptReplyDeadline`, `scanSentinelForPrompt` via `runIngested`) |
| Run/Stop ordered lists | **met** with one omission — see I-7 (`ctx.Done()` returns without `Stop`) |
| Reload with `store.chunk` deferral | **met except the `state.bin` rewrite** — see I-4 |
| No `time.Sleep`; durations named/config-derived | **met** — `sleepcheck` clean, every window is a named const or a config read |

### Test table, row by row

| Row | Verdict |
|---|---|
| `TestServicesAllNil` (13 ops, all-nil Services) | **met** — asserts 13 ops, `OK:true` except `mcp`, no panics, WAL present. *Partial:* WAL is asserted for `observe.tool`/`observe.stop` only, not `observe.prompt` (M-9) |
| `TestObservePromptRepliesWithinDeadline` + blocking variant | **met** (both, blocking variant proves `hookio.Empty()` and prompt return) |
| `TestDegradedPassiveSuppressesActingPaths` | **met** — three routes + the `act.` idle task |
| `TestDegradedPassiveStillRecords` | **met** — 20 WAL'd events, 20 seam calls, all three idle tasks ran |
| `TestModeOffSkipsIngest` | **met** |
| `TestNAKDuplicateIsDedupedOnDrain` | **met** — one dispatch, both files consumed |
| `TestHandleOverridesDefaultRoute` | **met** |
| `TestHandleOnBareOptionsIsSafe` (adapted) | **met** — no panic, route lands, `New` succeeds |
| `TestBindRunsInOrderAndDeclaresProducers` | **met** — order pinned, `ResetProducers` + `t.Cleanup` |
| breach-detector rows (3 windows / clean reset / revert) | **met**, plus two extra rows (partial window, Reset) |
| `TestSpoolOnBreachFalseDoesNotTransition` | **met** — HotSync preserved, counter still 1 |
| `TestHotModeTransitionWritesStateAndNAKs` | **met** — `state.bin` hot=spool, next `observe.tool` NAK'd, `Response.Hot` carries the hint |
| `TestSketchSetNeverWritesTriedBloom` | **met** — pre-existing in `sketchset_test.go` (Task 3), not duplicated |
| `TestConfigReloadDefersChunkChange` | **met** — live chunk block unchanged, `config-pending.json` written. *Partial:* the brief's "one Loud line" is not asserted (M-9) |
| `TestIdleExitWithZeroSessions` | **met** — `Run` returns, `state.bin` and `daemon.lock` gone |
| `TestRunReturnsNilWhenLockHeld` | **met** |
| idle-controller rows (priority / budget / panic isolation / IsIdle / `act.` prefix both ways / replace-in-place / error-not-fatal) | **met** |
| `TestDeclaredProducerSetMatchesArchitecture` | **met** |
| `TestMarkerIsWrittenByFlushAndCheckpointOnly` | **met** |
| Extra rows added beyond the table | `TestAdminPingAlwaysAnswersOK` (required by the post-brief ruling), `TestUnknownOpIsRefusedNotPanicked`, `TestHandlerPanicIsRecovered`, `TestAdminShutdownStopsTheDaemon`, `TestPercentileDurationP99`, `TestServicesFromNeverReturnsNil` — all welcome |

**No silent drops in the route table, the mode table or the producer split.** The two spec items that *were* dropped
without being called out in the report are I-1 (`obs.Registry.Persist` ruling) and I-4 (reload's `state.bin` rewrite);
both are recorded as findings, not as adaptations, because neither the report nor a code comment mentions them.

**Spec compliance verdict: compliant apart from I-1 and I-4 (two dropped requirements), plus the ambiguity-resolutions
noted above, which are all defensible.**

---

## VERDICT 2 — CODE QUALITY

### Critical

**C-1. Re-entrant `Drain` self-deadlocks the daemon, and the startup drain reaches it before `Serve` starts.**
`internal/daemon/daemon.go:485-491` (`drainDispatch`), `internal/daemon/handlers.go:567` (`handleFlush`),
`internal/daemon/handlers.go:646` (`handleAdminDrain`), `internal/daemon/drain.go:100-102` (`dr.mu`).

*What.* `drainer.Drain` holds a plain `sync.Mutex` for the whole replay, including every `Dispatch` call
(drain.go:101-102 → drain.go:234). `drainDispatch` sends every **non**-hot-path op through the full `dispatchOp`, so a
drained `flush` line lands in `handleFlush`, whose last step is `d.Drain(ctx)` — re-entering `drainer.Drain` on the same
goroutine and blocking on the already-held, non-reentrant `dr.mu` forever. `admin.drain` is the same shape.
The per-line `context.WithTimeout(ctx, drainLineDeadline)` (drain.go:227) cannot rescue it: a context deadline does not
unblock `Mutex.Lock`.

*Why it matters.* `ipc.client.Send` spools **every** op on **every** failure path (`spoolAndReturn`, client.go:318-321) —
there is no hot-path filter — so a `SessionEnd` hook that fires while the daemon is down leaves a `flush` line in
`spool/client-<pid>.ndjson`. The next daemon start hits it in `Run`'s step-4 startup drain (daemon.go:357), which runs
**before** `go server.Serve(...)` (daemon.go:377). Result: a daemon that holds `daemon.lock`, has written `state.bin`,
and never accepts a connection — every client then fails to connect and spools forever, and the wedged goroutine also
holds `dr.mu` so no later drain can ever run. `Stop`'s own bounded drain (daemon.go:509) hits the identical trap.
This is not a rare interleaving; it is a plain ordinary sequence (daemon down at SessionEnd, daemon up next session).

*Why the tests miss it.* Every `flush` test drives `dispatchOp` directly on a daemon whose `d.drain` is still nil
(`Drain` returns `(0, nil)` at daemon.go:443), and `TestNAKDuplicateIsDedupedOnDrain` spools an `observe.tool` line,
which takes the `runIngested` shortcut.

*Suggested fix.* Make the drain-time route set non-re-entrant rather than trying to make the mutex reentrant. Either
(a) give `drainDispatch` an explicit allow-list — hot-path ops → `runIngested`, `session.start`/`checkpoint` → `dispatchOp`,
`flush` → a `handleFlush` variant that skips the trailing `Drain`, `admin.*` → skipped entirely (an admin op replayed
from a stale spool file has no operator waiting on it anyway); or (b) guard `Daemon.Drain` with an
`atomic.Bool`/`TryLock` "already draining on this daemon" check that returns `(0, nil)` instead of blocking. Add a
regression test that seeds `client-*.ndjson` with a `flush` line and asserts `Drain` returns.

### Important

**I-2. `session.start` wipes `SentinelState.Observed` and `Chances`, which the contract package documents as
never-resettable.** `internal/daemon/handlers.go:400`.
```go
h.Sentinel = contract.SentinelState{Token: s.Token, Session: ev.SessionID, MintedAt: now}
```
`SentinelState.Observed` is documented (`contract/history.go:44-46`) as "true once a scan has ever found Token … It never
resets to false: once §12.1's mechanism is proven to work, it stays proven", and `RecordSentinelScan` is careful never to
clear it. A whole-struct assignment clears both `Observed` and `Chances` on every `session.start` that may act. Effect
once SP-11 binds `Rehydrate` and `CAdditionalContext` goes live: the assertion re-proves itself every session and can
report a fresh SevCritical failure (→ degrade-to-passive) on a session where the transcript merely could not be read
twice. This is the same class of hazard as the `LastSessionID` pre-write that Task 4's fix round 2 forbade — a daemon
overwriting a field another package owns the lifecycle of. *Fix:* assign the three token fields individually and leave
`Observed` alone; decide `Chances` deliberately (`= 0` on a fresh token is defensible, but state it in a comment).

**I-1. Ruling #20's `obs.Registry.Persist` half was dropped, and the two writers now collide on one file.**
`internal/daemon/metrics.go:34-82` (`writeLatencyJSON`), `internal/daemon/handlers.go:697-699` (`idleWriteMetrics`),
`internal/daemon/daemon.go:521-525` (`Stop` → `d.m.Persist`). The ruling says the `metrics` idle task and the status
route use the shipped `obs.Registry.Persist(paths.Of(root))` and explicitly "do not write a custom `writeLatencyJSON`
shape". Both writers target the same path — `obs/registry.go`'s `latencyFileName = "latency.json"` under
`paths.Of(root).Metrics`, and `metrics.go:77` computes exactly that path — with two *different* JSON shapes
(`{generated, budgets:[…]}` vs. the `obs.Snapshot`). Whichever ran last wins, so `metrics/latency.json` has a
nondeterministic schema for SP-14/`/qompack:status` to parse. *Fix:* make `idleWriteMetrics` call `d.m.Persist(paths.Of(d.root))`
and either delete `writeLatencyJSON` or point it at a distinct filename with controller sign-off.

**I-3. `CheckBudgets` is called once per hot-path *sample*, not once per closed window, inflating `BudgetBreach.Windows`
by up to 512×.** `internal/daemon/handlers.go:189-214`. `hotPathWorker` calls `updateBudgetsCache()` after **every**
sample, and `obs.Registry.CheckBudgets` is documented as stateful — "Windows counts consecutive over-limit calls per
budget" (`obs/registry.go:30-33`, 45-48) — with the spec pinning "the daemon calls it exactly once per closed 512-sample
window" and "the real degrade decision fires at Windows == 3". As written, `Windows` reaches 3 after three *samples*, so
the number `/qompack:status` renders (and anything SP-14 keys off it) is meaningless. *Fix:* have `breachDetector.Observe`
report window closure (e.g. return `(Transition, closed bool)` or expose `WindowsClosed()`), and call
`updateBudgetsCache()` only when a window actually closed.

**I-4. Reload never rewrites `state.bin`.** `internal/daemon/reload.go:42-101`. The spec and the brief both list
"rewrite `state.bin`" as a step of the reload. `reloadConfig` applies the config, defers `store.chunk.*`, logs the
changed keys — and never calls `ipc.WriteState`. `state.bin` carries `ConnectDeadlineMs`, `AckDeadlineMs`,
`MaxPayloadBytes`, `SpoolOnBreach` and `DaemonEnabled` (daemon.go:256-269), all of which clients read at construction, so
a reload of any of those keys is invisible to clients until the next `session.start` happens to write state for its own
reasons (handlers.go:384). The idle-tick reload path (daemon.go:408) never propagates at all. *Fix:* one
`_ = ipc.WriteState(d.root, d.currentState())` after the config swap, when `len(changed) > 0`.

**I-5. `historyMu` is held across unbounded third-party seam calls and across `monitor.RunAll`.**
`internal/daemon/handlers.go:368-421` (`session.start`) and `handlers.go:485-527` (`checkpoint`). Both routes take
`historyMu` at the top with `defer Unlock` and then call out to `svc.SessionStart` / `svc.PreCompact` — seams a wave-3
subplan supplies, whose B-E budget alone is 2 s — plus `monitor.RunAll`, `WriteState` and `WriteMarker` file I/O. Two
consequences: (a) one slow seam serialises `session.start`, `checkpoint` and every `observe.prompt` sentinel scan behind
it for its whole duration; (b) any future seam that calls back into the daemon through `DaemonFrom(ctx)`/a route that
touches history self-deadlocks on a non-reentrant mutex, with nothing in the seam's signature warning about it. The
ownership contract only requires exclusive ownership of the `*SessionHistory` value, not exclusivity across the seam
call. *Fix:* narrow both critical sections — lock → load → mutate the pre-seam fields → unlock → call the seam → lock →
re-load (or re-apply) → save; or at minimum document loudly at the `Services` seam declarations that a seam must never
re-enter the daemon.

**I-6. The B-A histograms and the degrade decision are fed straight from an unvalidated wire field.**
`internal/daemon/handlers.go:168-184`. `observed = recvTS - req.TS` with only a negative clamp; `ipc.DecodeRequest`
(`ipc/frame.go:60-66`) performs no validation, so a request whose `"t"` is absent or zero yields an "observed" latency of
~55 years, which lands in `hook_controlled_observed` and `hook_controlled` (the histogram CI gates B-A on and `/qompack:status`
renders) and feeds the breach detector — six such samples in a 512-window are enough to push p99 over any budget and
degrade the hot path to spool. A zero `TS` costs nothing to reject. *Fix:* skip the sample (and count it, e.g.
`hotpath_sample_invalid`) when `req.TS <= 0`, and consider a sanity ceiling on `observed`.

**I-7. `Run`'s `ctx.Done()` path returns without `Stop`, so nothing is flushed and nothing is cleaned up.**
`internal/daemon/daemon.go:382-385`. The idle-exit path calls `d.Stop(ctx)` (daemon.go:421) but the cancellation path
returns `nil` directly. That leaves sketches unsaved, ingest WAL handles unflushed/unclosed, `metrics/latency.json`
unwritten, `state.bin` still on disk (clients keep dialling a dead endpoint until the connect fails) and `daemon.lock`
still held. `Stop` is idempotent via `sync.Once`, so calling it here is free. There is no production caller of `Run` yet
(`cmd/` does not wire it), which is why nothing has noticed. *Fix:* `case <-ctx.Done(): cancel(); <-serveErrCh; return d.Stop(context.Background())`
— note the parent ctx is already done, so `Stop` must not inherit it or its own bounded drain is a no-op.

**I-8. `breachDetector.Reset()` is dead code, and its doc comment promises a behaviour nothing implements.**
`internal/daemon/budget.go:100-109`. The comment says it is "used when a new session resets the daemon-wide hot-path
submode (registry.Ensure's own reset, task-3) so stale samples from a previous, unrelated session can never contribute to
this session's first window" — but `grep -rn '\.Reset()' internal/daemon` finds only `budget_test.go:70`.
`SessionRegistry.Ensure` resets `HotMode` to `HotSync` for a new session id (registry.go:157-164) while the detector
keeps its partially-filled ring and both streaks, so a 511-sample breaching window from the previous session closes on
the new session's very first request. *Fix:* call `d.breach.Reset()` from `handleSessionStart` when `Ensure` reported a
new session (or have `Ensure` report that fact), or delete the method and the claim.

### Minor

- **M-1** `internal/daemon/daemon.go:119` — `wg sync.WaitGroup` on the `daemon` struct is never used (the `i.wg` hits are
  `ingest`'s). Delete.
- **M-2** `internal/daemon/handlers.go:261-267` — `registry.Touch` runs before the `MayRecord()` gate; the mode table
  groups `Touch` with `ingest.Accept` as "skipped" under `ModeOff`. Keeping liveness tracking is arguably better, but it
  is a deviation and deserves a comment if intentional.
- **M-3** `internal/daemon/handlers.go:553` — `svc.SessionEnd` is gated on `MayRecord()`, a mode-check site the normative
  table does not list ("there is no other mode check in internal/daemon"). Defensible; call it out in a comment.
- **M-4** `internal/daemon/handlers.go:111-120` — `dispatchOp`'s doc says "It is also `drainer.Dispatch`'s function",
  but `drainDispatch` (daemon.go:485) bypasses it for hot-path ops. Stale comment; it also masks C-1 from a reader.
- **M-5** `internal/daemon/reload.go:74-86` — a reload swaps `d.cfg` but re-applies nothing to already-constructed
  components: `breachDetector.limit/need`, `SessionRegistry.SetMaxSessions`, `idleController.afterSeconds` and the ingest
  ring all keep their `New`-time values. `applyHotPathTransition` then logs the *new* `budget_ms`/`windows` while the
  detector still gates on the old ones (handlers.go:227-230) — actively misleading. Either re-apply or log the values the
  detector actually holds.
- **M-6** `internal/daemon/daemon.go:346-349` — if `ipc.NewServer` fails, `Run` returns the error without releasing the
  lock or cancelling `runCtx`; the ingest workers and hot-path worker keep running and `daemon.lock` stays on disk with a
  live pid, so no replacement daemon can take the project while the process lives.
- **M-7** `internal/daemon/metrics.go:69` — `time.Now().UnixMilli()` rather than the injected clock (acknowledged in the
  report; the signature is spec-fixed, so this is fine, but a package-level `clk` parameter would cost nothing).
- **M-8** `internal/daemon/handlers.go:624-629` — `handleMCP` returns `OK:true` without ever calling the bound
  `MCPInitialized` seam. Cosmetic (SP-13 replaces the route), but as written the seam's only effect is producer gating.
- **M-9** Test coverage nits: `TestConfigReloadDefersChunkChange` does not assert the `Loud` line the brief's row calls
  for; `TestServicesAllNil` asserts the WAL for `observe.tool`/`observe.stop` but not `observe.prompt`.
- **M-10** `internal/daemon/daemon_test.go:96-110` — `TestServicesAllNil` drives `admin.shutdown`, which spawns an async
  `Stop()` that calls `d.ing.Close()` concurrently with the test's own `t.Cleanup(func(){ _ = dd.ing.Close() })`. It
  happens to be safe today (`ingest.Close` is mutex-guarded and idempotent), but it is an unnecessary latent flake in a
  table test; skip `admin.shutdown` there or await the goroutine.
- **M-11** `internal/daemon/registry.go:185-194` + `daemon.go:403-426` — `Touch` is a no-op for a session `Ensure` never
  saw, so hot-path traffic belonging to a session that started under a *previous* daemon process never refreshes
  `LastActivity` or `Live()`; the idle-exit path can then fire while events are actively flowing. Consider `Ensure`-on-first-touch
  or keying idle-exit on last request time rather than live-session count.
- **M-12** `internal/daemon/handlers.go:500` and `:579` dereference `d.m` without the `if d.m != nil` guard used
  everywhere else in the same file. Safe today (`New` always seeds a registry) but inconsistent.

### Things I checked and found correct

- Panic recovery: both layers present, same counter name, deliberately not deduplicated (handlers.go:150-161 +
  ipc/server.go:222-236); `TestHandlerPanicIsRecovered` pins the daemon layer.
- Breach detector: window closure happens only on `hotPathWorker` in production (daemon.go:334); the ACK path only does a
  non-blocking channel send with a `default:` drop (handlers.go:180-183).
- `ToSpool`: `SetHotMode` + `WriteState` + WARN + Loud + `hotpath_degraded`, with `SpoolOnBreach=false` short-circuiting
  *after* the log and the counter — exactly the required split, and the NAK path is verified end-to-end by
  `TestHotModeTransitionWritesStateAndNAKs`.
- History mutex: three sites, all covered; flush touches no history; no lock-order inversion inside the package
  (`historyMu` → `cfgMu`/monitor/registry only, never the reverse); `contract.SessionHistory`'s "one goroutine at a time"
  contract is honoured for the value's whole lifetime. The residual risk is I-5's re-entrancy, not ordering.
- `session.start` ordering: `Ensure` → `LoadHistory` → `RunAll` (monitor first, §5.21) → `WriteState` → seam →
  sentinel → banner → `SessionCount++` → `SaveHistory`; `LastSessionID` never written; marker never written.
- Idle controller: ascending priority with stable sort, replace-in-place, `act.` skipping evaluated once per `RunOnce`
  under the lock, per-task sub-context of `budget - elapsed` measured through the injected clock, panic-excluded-from-`ran`.
- Idle tick math: `min(30s, idleExitSeconds/10)` with a `<= 0` fallback; idle-exit arms on the first zero-live tick and
  disarms on any live session.
- Reload gating: `os.Stat` mtime **and** size, `force` bypass for `admin.reload`, missing `config.json` is not an error,
  `config.Load` warnings surfaced via `Loud`, chunk block preserved from the *old* config while the full new config goes
  to `state/config-pending.json`.
- Lifecycle: `ErrAddrTooLong` → Loud + `nil`; `ErrLockHeld` → `nil`; `spawn.lock` deleted after listen (with the Windows
  read-only `chmod` first); startup `Drain`; 30 s heartbeat; `Stop` idempotent via `sync.Once` in the spec's order
  (cancel → bounded drain → `ing.Wait`/`Close` → sketches → `Persist` → `RemoveState` → `server.Close` → `lock.Release`).
- The `runCancel` fix the implementer found mid-task is real and correctly done: `Stop` takes `runCancelMu`, calls the
  cancel `Run` stored, and `Run` distinguishes a `Stop`-driven `Serve` return (`d.stopped` closed → `nil`) from a genuine
  transport failure. `TestAdminShutdownStopsTheDaemon` exercises the whole path over a real transport and would hang
  without it.
- Goroutine leaks: `hotPathWorker` and the ingest pool die with `runCtx`; `serveErrCh` is buffered; the only intentional
  abandonment is the `ObservePrompt` watchdog goroutine, which is documented and uses a buffered channel so it cannot
  block forever on send.
- Clock injection: `core.Clock` exposes only `Now`/`Since`, so `time.NewTicker` in `Run` is unavoidable; every decision
  timestamp (`zeroLiveSince`, `startTS`, `NowMilli`, idle budget accounting) goes through the injected clock, and the
  tests use `FakeClock` where the interface allows it.

**Code-quality verdict: 1 Critical, 8 Important, 12 Minor.**

---

## ipc race diagnosis

*(Separate mandate: `internal/ipc/server.go` is Task-2, branch-owned code. Diagnosis only — I have not changed it.)*

**Reproduction.** `go test ./internal/daemon/ -race -run 'Lock|Spawn' -count=10` failed on **both** attempts here
(5 of 6 lock tests + `TestEnsureRunning_AlreadyRunningNeverSpawns` on run 1). The full package at `-race -count=1` (×3)
and `-race -count=2` (×2) was green, which matches the implementer's evidence: the window needs the repeated
construct/serve/close churn that `-count=10` produces.

**The report.**
```
WARNING: DATA RACE
Write at 0x00c00119c058 by goroutine 86:
  runtime.racewrite()
Previous read at 0x00c00119c058 by goroutine 82:
  runtime.raceread()
  ...daemon.TestLiveLockNotReclaimed.func2()  lock_test.go:79      <- the `go srv.Serve(ctx, …)` goroutine
Goroutine 86 created at:
  ipc.(*server).waitForConns()  server.go:138                      <- the `go func(){ s.wg.Wait(); close(done) }()`
  ipc.(*server).Close.func1()   server.go:291
```
The bare `runtime.racewrite`/`raceread` frames with no `sync` frames are the signature of `sync.WaitGroup`'s own misuse
detector, not of ordinary memory: `waitgroup.go:115` does `race.Read(unsafe.Pointer(&wg.sema))` inside `Add` when the
counter goes 0→positive, and `waitgroup.go:190` does `race.Write(unsafe.Pointer(&wg.sema))` inside `Wait` when it
registers as the first waiter. The raced address is `&s.wg.sema`; the write is `waitForConns`'s `wg.Wait()`, the read is
the accept loop's `s.wg.Add(1)`. In plain words the detector is reporting **"WaitGroup.Add called concurrently with
Wait"**.

**The interleaving.** `Close` → `closed.Store(true)`, `ln.Close()`, `closeTrackedConns()`, `waitForConns()` →
`wg.Wait()`. Concurrently, `Serve`'s accept loop is inside the window between `s.ln.Accept()` having **already returned a
connection** (server.go:110) and `s.wg.Add(1)` (server.go:117). Closing the listener unblocks a *pending* `Accept`; it
cannot retract an `Accept` that already returned. So the `Add` that takes the counter 0→1 runs with no happens-before
relationship to the `Wait` that is (or was just) observing zero — precisely the reuse the WaitGroup contract forbids.

**Is it N-2a?** Yes — this is exactly task-2-review.md's deferred N-2a ("`wg.Add` happens outside the `connsMu` critical
section"), now manifesting rather than theoretical. `waitForConns`'s own doc comment argues it is safe because "every
`s.wg.Add` lives inside the accept loop, and that loop's own `Accept` calls only ever fail once the listener … has
closed". That argument is the bug: it reasons about `Accept` *failing*, and the hazardous window is an `Accept` that has
already *succeeded*. The same window also leaks: a connection accepted after `closeTrackedConns()` has iterated is never
added to the closed set, so its `handleConn` survives `Close` for up to `connIdleTimeout` (10 minutes).

**Minimal fix** (both defects at once, no structural change): move `wg.Add(1)` inside `connsMu`, and let `Close`'s flag
be set under the same lock.
```go
// server: add `closing bool` guarded by connsMu.
func (s *server) trackConn(conn net.Conn) bool {   // returns false if the server is already closing
    s.connsMu.Lock(); defer s.connsMu.Unlock()
    if s.closing { return false }
    s.conns[conn] = struct{}{}
    s.wg.Add(1)                                    // paired with Close's flag under one lock
    return true
}
// Serve: if !s.trackConn(conn) { _ = conn.Close(); continue }   // and drop the separate s.wg.Add(1)
// Close: connsMu.Lock(); s.closing = true; close every tracked conn; connsMu.Unlock(); then waitForConns()
```
Every `Add` now either happens-before `closing` is set (so the conn is in the map, is closed by `Close`, and `Wait`
legitimately waits for it) or does not happen at all. `handleConn` keeps its `defer s.wg.Done()` and its untrack step
(which must only `delete`, never `Add`). A regression test that hammers `NewServer`/`Serve`/`Close` in a loop under
`-race` — the shape `-count=10` produces today — should accompany it.

---

## Verdict

**Needs fixes (1 Critical, 8 Important)** — plus 12 Minor, and the separately-owned pre-existing `internal/ipc` N-2a
WaitGroup race documented above, which must be fixed by its owner before merge.

---

# Re-review (fix round 1)

Scope: `04f3c81f..ef50515f` (`review-task5-fix1.diff`), 7 files, +527/-114, amended into the single task commit.
I confirmed `04f3c81f` is the replayed twin of the reviewed `115ba19d`: `git diff 115ba19d 04f3c81f` touches only
`internal/ipc/server.go` and `internal/ipc/server_test.go` (the Task-2 N-2a fix), so every `internal/daemon` file is
byte-identical to what the first review examined. `git log --oneline 04f3c81f..ef50515f` = one commit; message is the
brief's verbatim text; a grep for co-authored/generated-with/signed-off trailers finds nothing. Working tree clean.

Verification I ran (read-only):

| Command | Result |
|---|---|
| `go vet ./internal/daemon/...` | clean |
| `go test ./internal/daemon/ -race -count=2` | ok |
| `go test ./internal/daemon/ -race -run 'Lock\|Spawn' -count=10` x3 | **ok, ok, ok** (failed 2/2 before the ipc fix) |
| `go test ./internal/ipc/ -race -count=2` | ok |
| `go test ./internal/...` | ok, no FAIL/panic anywhere |
| `go test ./test/guards/...` | ok — `daemon.New(Options{})` still succeeds |

## C-1 — re-entrant `Drain` self-deadlock: **FIXED**

`drainDispatch` (daemon.go:491-528) is now an explicit per-family allow-list instead of a blanket `dispatchOp` call:
hot-path → `runIngested`; `ipc.OpFlush` → `flushRoute(ctx, req, false)`; `strings.HasPrefix(op, ipc.OpAdminPrefix)` →
skipped with `OK:true`; everything else → `dispatchOp`. `handleFlush` is now a one-line wrapper over
`flushRoute(ctx, req, true)`, and the `drain` parameter gates only the trailing `d.Drain` (handlers.go:691-694), so a
drained flush still performs `registry.End`, `ing.CloseSession`, `svc.SessionEnd`, `WriteMarker` and `Sketches.Save`.

**Is the hole closed on both paths?** Yes, and I checked the residual routes rather than taking it on trust:

- The `default:` branch reaches `session.start`, `checkpoint`, `status`, `mcp` and unknown ops. I re-read all four
  handlers — none calls `Drain`, directly or transitively (`handleStatus` only calls `ipc.SpoolFiles`;
  `handleSessionStart` calls `maybeReloadConfig`, which does not drain).
- `admin.drain` and `admin.shutdown` — the two other `Drain` callers — are unreachable from a drained line now,
  and `admin.shutdown`'s async `Stop`→`Drain` is removed as a question rather than left to goroutine indirection.
- Live path: `handleFlush` → `Drain` → `drainDispatch` → a *second* spooled flush → `flushRoute(drain=false)`: no
  recursion. Startup path (`Run` daemon.go:357, before `go server.Serve`) and `Stop`'s bounded drain (daemon.go:509)
  both go through the same `drainDispatch`, so both are covered by construction.
- `ipc.OpAdminPrefix` is the exported `"admin."` const (`ipc/wire.go:32`), so the prefix test cannot drift from the op
  spellings.

**Is the RED→GREEN revert claim plausible from the tests' shape?** Yes — both new tests fail loudly rather than hang,
which is what makes the claim checkable. `TestDrainOfSpooledFlushLineDoesNotDeadlock` (daemon_test.go) writes a real
`Op:"flush", Reply:true` line into `client-88888.ndjson`, runs `dd.Drain` on a goroutine and selects against a 10 s
`drainDeadlockGuard`, failing with the exact message quoted in the report; with the old blanket `dispatchOp` the drain
goroutine blocks on `dr.mu` forever and the `done` channel never fires, so the timeout arm is the only reachable one.
`TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun` is the end-to-end twin: it seeds the spool *before* `New`, runs
`Run`, and `require.Eventually`s on `ipc.Probe(addr)` — which can only succeed once `Run` has passed the startup drain
and reached `go server.Serve`. Neither test is tautological, and neither can pass against the pre-fix code.

## I-1..I-8 — all **FIXED**

(The implementer renumbered; verified by content, not by label.)

| Finding | Status | Evidence |
|---|---|---|
| Sentinel wipe (`Observed`/`Chances`) | **fixed** | handlers.go:481-489 assigns `Token`/`Session`/`MintedAt` individually and resets `Chances` deliberately with a stated rationale; `Observed` is never written. `TestSessionStartNeverWipesSentinelObserved` seeds `Observed:true, Chances:1` through a real `SaveHistory` and asserts survival plus a fresh token |
| `CheckBudgets` per sample | **fixed** | `Observe` now returns `(Transition, closed bool)`, `true` only on the `sampleWindow`-th call (budget.go:79-112); `hotPathWorker` gates `updateBudgetsCache()` on `closed` (handlers.go:233-243). `TestBreachDetectorObserveReportsClosedOnlyOnTheWindowBoundary` asserts `false` for all 511 preceding samples |
| Reload never rewrote `state.bin` | **fixed** | reload.go:97-107 calls `ipc.WriteState(d.root, d.currentState())` when `len(changed) > 0`, on both the idle-tick and `admin.reload` paths. `TestConfigReloadDefersChunkChange` now reloads `runtime.daemon.ackDeadlineMs: 99` and reads it back out of `state.bin` via `ipc.ReadState` |
| `historyMu` across seams/`RunAll` | **fixed** | see the dedicated analysis below |
| Unvalidated `req.TS` | **fixed** | `validHotPathTS` (handlers.go:199-226) rejects `ts <= 0`, >250 ms future, >10 s stale, with a `hotpath_sample_invalid` counter, *before* any histogram or the detector channel is touched. `TestRecordHotPathSampleRejectsInvalidTS` covers zero/negative/absurd-past and pins that a valid sample still lands |
| `Run` `ctx.Done` without `Stop` | **fixed** | daemon.go:389-398 returns `d.Stop(context.Background())`, with the "not the already-cancelled ctx" reasoning stated. `TestRunCtxDoneGoesThroughStop` asserts `state.bin` and `daemon.lock` are gone after cancellation |
| `breachDetector.Reset()` dead | **fixed** | `handleSessionStart` snapshots `registry.Get` *before* `Ensure` and resets on a genuinely new id (handlers.go:420-429), mirroring `Ensure`'s own new-session test without touching Task-3's `registry.go`. `TestSessionStartResetsBreachDetectorForNewSession` fills the ring to `sampleWindow-1` and proves one post-start sample cannot close a window |
| Two writers of `metrics/latency.json` | **fixed** | `writeLatencyJSON` deleted; `writeMetricsSnapshot` is a two-line wrapper over `m.Persist(paths.Of(root))`. A grep for `.Persist(` across `internal/` shows exactly two daemon callers (`Stop`, `idleWriteMetrics`), both routing to the same shipped writer, and `latencyFileName` now exists only in `internal/obs/registry.go:54`. **`obs.Registry.Persist` is the ONE writer everywhere** |

### `historyMu` narrowing — single-writer contract and lost-update analysis

Both routes are now three-phase. `session.start`: (1) lock → `LoadHistory` → `RunAll` (this package's own bounded
Checks) → `WriteState` → `SaveHistory` → unlock; (2) unlocked seam call; (3) lock → **re-load** → sentinel fields →
`SessionCount++` → `SaveHistory` (deferred unlock). `checkpoint`: (1) lock → load → PreCompact observation → save →
unlock; then `WriteMarker`; (2) unlocked seam call timed into B-E; (3) lock → **re-load** → `SetPrecompactInstr` →
`AddPrecompactWallSample` → save.

- **Single-writer contract holds.** Each phase's `*SessionHistory` value is loaded, mutated and saved entirely inside
  one critical section; the phase-1 value is never touched after its unlock (phase 3 reassigns `h` from a fresh
  `LoadHistory`). I checked the one way that could go wrong — a retained pointer — by grepping
  `internal/contract/monitor.go` for any `History` reference: there is none, so `RunAll` does not keep `env.History`
  past its own return, and the assertions mutate it synchronously inside phase 1.
- **No lost update between `RunAll`'s results and the phase-3 persist.** The mutations `RunAll` makes (including
  `LastSessionID`, `StartsWithoutMarker`, `Seen`, `AwaitingCompactStart`) are saved at the end of phase 1, and phase 3
  re-reads from disk before applying its own, so a concurrent `checkpoint`/sentinel-scan save during phase 2 is
  preserved rather than clobbered — which the old single-hold structure could not have done anyway, because it never
  ran concurrently at all. `results`/`mode`/`justDegraded` are carried across phase 2 as read-only locals.
- **Panic safety improved, not weakened**: a panic in phase 2 now unwinds with no lock held; a panic in phase 3 is
  covered by the deferred unlock.
- **`LastSessionID` still never written by the daemon** — re-grepped after the restructure: writes remain confined to
  `contract/assertions.go`'s `checkSessionStartFires`.
- `checkpoint`'s `WriteMarker` also moved to *before* the seam call, which is what the spec's own route description
  orders ("history observation; `contract.WriteMarker`; then `svc.PreCompact`") — an ordering deviation the first
  review did not catch, now incidentally corrected.
- Residual, correctly scoped: phase 1's `SaveHistory` failing (disk full) means phase 3 re-loads pre-`RunAll` state.
  Error-path-only, logged, strictly better than the pre-fix behaviour of losing the whole route's work.

## Minors — adjudication

**Accepted as fixed (9), spot-checked in the delta:** M-1 (`wg` field gone — a grep for `d.wg` is clean), M-2/M-3 (both
deviating mode-check sites now carry the reasoning inline at handlers.go:314-319 and :671-675), M-4 (`dispatchOp`'s doc
rewritten to state it is *not* `drainDispatch`'s function and to point at C-1), M-5 (`breachDetector.Config()` accessor;
`applyHotPathTransition` logs the detector's own limit/need while still reading `SpoolOnBreach` from the live config —
correct split), M-6 (lock released on the `NewServer` failure path), M-9 half (`observe.prompt` added to
`TestServicesAllNil`'s WAL assertion), M-10 (`t.Cleanup` now calls the idempotent, synchronous `dd.Stop` instead of
racing `admin.shutdown`'s goroutine), M-12 (documented at the `handleCheckpoint` call site).

**Accepted as left, rationale sound:** M-7 is genuinely moot — `writeLatencyJSON` and its `time.Now()` no longer exist,
and `obs.Registry.Persist` owns the timestamp. M-8 is cosmetic on a route SP-13 replaces wholesale. M-11 is framed as
"consider" in my own review, interacts with the new-session detection I-8 just introduced, and deserves its own design
pass. M-9's Loud-line half: the rationale (a process-wide `logging.LastLoud()` ring asserted from a `t.Parallel()` test
is a flake generator) is correct, and the load-bearing behaviour is already asserted. Nothing in the delta contradicts
any of these.

## New defects

**None blocking.** Two non-blocking observations from the fix delta, for the record — neither needs action before merge:

- `validHotPathTS`'s `hotPathSampleMaxAge` (10 s) *discards* an extreme-latency live sample rather than clamping it.
  Drained lines never reach `recordHotPathSample` (they bypass `dispatchOp`), so the only source of a >10 s live sample
  is a genuinely wedged daemon — the exact case the spool fallback exists for. Clamping to the ceiling instead of
  dropping would keep such an outlier counting toward p99. Low impact (the request must both be live and take >10 s),
  and the 250 ms future-slop bound is well judged.
- `Run`'s `ctx.Done` arm now propagates `Stop`'s error, while the idle-exit arm still does `_ = d.Stop(ctx); return nil`.
  A benign `server.Close()`/`lock.Release()` error would now turn an ordinary cancellation into a `Run` error. Risk is
  low (`Close` is `sync.Once`-guarded and the listener close already happened via `Serve`'s `AfterFunc`), and
  `TestRunCtxDoneGoesThroughStop` pins `NoError`, but the two exit paths disagree about whether shutdown errors matter.

## ipc race concern — closed at the daemon level

The Task-2 commit applied my minimal-fix recipe essentially verbatim: `closing bool` guarded by `connsMu`, a single
`registerConn` that checks `closing` and performs `wg.Add(1)` inside the same critical section (refusing and closing a
connection accepted after `Close` began), `unregisterConn` for the `Done`+delete side, and `atomic.Bool closed` replaced
by `isClosing()`. `waitForConns`'s incorrect safety argument was replaced with an accurate one. My reproducer
(`go test ./internal/daemon/ -race -run 'Lock|Spawn' -count=10`), which failed on both attempts against the pre-fix
tree, is now clean on three consecutive runs, and `internal/ipc` itself is clean at `-race -count=2`.

## Final verdict

**Approved.**

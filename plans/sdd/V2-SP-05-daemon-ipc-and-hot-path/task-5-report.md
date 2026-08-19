# Task 5 report — daemon composition: extension seams, idle controller, breach detector, op routes, reload, lifecycle

Status: DONE
Commit: `115ba19d1ca5c573678f3bc297b063b201dfa5ef` — `feat(daemon): extension seams, idle controller, sync->spool fallback`

## What was built

`internal/daemon` was completed from Task 3's building blocks (lock/spawn/registry/ingest/drain/sketchset, all left untouched) into a fully working daemon:

- **`internal/daemon/options.go`** (new) — `Options` (moved out of `daemon.go`, kept SP-01's pointer-receiver `Handle`/`Handler`/`Ops` shape exactly), `NewOptions`, `Bind`, `Services` (7 struct-typed fields mirroring `Options` + 9 nil-tolerant function seams — `ObserveTool`, `ObservePrompt`, `ObserveStop`, `SessionStart`, `SessionEnd`, `PreCompact`, `Rehydrate`, `MCPInitialized`, `StatusExtra`), `DeclareProducers` (five always-declared + four producer-gated, verbatim per the brief's ruling #15 pseudocode), the `ctxKey`/`ServicesFrom`/`RegistryFrom`/`DaemonFrom` context accessors.
- **`internal/daemon/idle.go`** (new) — real `idleController`: priority-ordered `Register` (replace-in-place), `Notify`/`IsIdle` (activity-keyed), `RunOnce` (per-task sub-context, panic isolation excludes a panicking task from `ran`, `act.`-prefix skip when `!mode().MayAct()`), each run timed into `idle_task_<name>`.
- **`internal/daemon/budget.go`** (new) — `breachDetector`: fixed 512-sample ring (`sampleWindow`), `Observe`/`Reset`, copy+sort p99, `Transition` (`NoTransition`/`ToSpool`/`ToSync`), `hotPathTailAllowance` (1ms, annotated).
- **`internal/daemon/handlers.go`** (new) — the composed `dispatchOp` (context injection, panic recovery reusing `ipc_handler_panic`, unknown-op refusal, NAK-forcing on `HotSpool` for fire-and-forget hot-path ops), all 13 default routes, `StatusSnapshot`, the session.start warm path (monitor-first, mode-gated sentinel mint/append, `SystemMessage` banner on just-degraded, `session_start.fires` marker deliberately never written here), checkpoint (History observation + B-E timing + marker), flush (End/CloseSession/SessionEnd/marker/Sketches.Save/Drain), the off-reply-path sentinel scan, `recordHotPathSample`/`hotPathWorker`/`applyHotPathTransition`, ruling #26's `contract_fail_<id>` / `contract_mode_change` counters.
- **`internal/daemon/reload.go`** (new) — mtime/size-gated `config.Load` reload, `store.chunk.*` deferral to `state/config-pending.json` + Loud, best-effort dotted-key diff logged at INFO.
- **`internal/daemon/metrics.go`** (extended) — `writeLatencyJSON` (per-budget-ID shape) and `loudTail` (now backed by `logging.LastLoud()`, per ruling #20 — not file tailing).
- **`internal/daemon/daemon.go`** (rewritten) — real `daemon` struct, `New` (Services assembly, the one monitor construction + `StandardAssertions` registration, route table composition where `Options.Handle` always wins), `Run` (resolve/lock/sketch-load/ingest-start/hot-path-worker/drain-construct/server/write-state/spawn-lock-cleanup/startup-drain/heartbeat+idle tickers/idle-exit), `Stop` (idempotent via `sync.Once`, now also cancels the same context the worker pool and server run under — see "seam I had to adjust" below), `Registry`/`Idle`/`Drain`.
- **`internal/daemon/spawn.go`** (small addition) — `removeSpawnLockFile`, since `ipc`'s own `spawnLockName`/`removeSpawnLock` are unexported and `daemon` may not reach them.

## Deviation from the historical spec text (superseded by the brief's realities list)

- `NewOptions`/`Options` do **not** carry an `ipc.Routes`/`ipc.Router` field or `ErrOptionsUninitialized` — none of that exists in this codebase's real `ipc`/`daemon` shape (confirmed by reading the actual shipped code, matching the brief's ruling). `TestHandleOnBareOptionsIsSafe` was adapted accordingly: a bare `Options{}` never panics and `New` still succeeds.
- The `obs/budgets.go` section of `task-5-spec.md` is historical: `internal/obs` already ships `Budgets()`/`CheckBudgets` complete (from an earlier task), confirmed untouched (`git status` shows no changes there). One helper, `histName`, already existed in `metrics.go` from Task 3 and is reused for both B-A/B-E histogram lookups and the daemon-owned `hook_controlled_observed` name.
- `loudTail` reads `logging.LastLoud()` rather than tailing `LOUD.log` (ruling #20).

## A design bug I found and fixed mid-task

`admin.shutdown`'s asynchronous `Stop()` call originally had no way to unblock `Run`'s own `select` loop or the ingest worker pool: `Run` created its own `context.WithCancel(ctx)` and never exposed the cancel function anywhere `Stop` could reach it, so `Stop` would call `d.ing.Wait()` and hang forever waiting for workers that were still blocked on a context nobody had cancelled, while `Run` itself never noticed the shutdown. I added a `runCancel context.CancelFunc` field, set once in `Run`, that `Stop` now calls before draining/closing anything. I also had to make the `serveErrCh` case in `Run`'s select distinguish an intentional `Stop()`-driven shutdown (report `nil`) from a genuine transport failure (report the raw error) — `Stop` closes a `stopped` channel before cancelling, and `Run` checks it. This is covered by a new test, `TestAdminShutdownStopsTheDaemon`, which failed with "context canceled" before the fix and passes after it.

## TDD evidence

Work proceeded file-by-file: for each new file (`idle.go`, `budget.go`, `options.go`, `handlers.go`+`daemon.go`, `reload.go`) the corresponding test file was written and run against the *not-yet-existing* symbols first (compile failures — the RED state), then the implementation was added until `go test ./internal/daemon/...` was green for that slice, before moving to the next file. The `TestNAKDuplicateIsDedupedOnDrain` and drain-related tests additionally caught a real design bug during the RED→GREEN loop: `drainer.Dispatch` was originally wired to the full `dispatchOp` (which re-calls `ingest.Accept` for hot-path ops), which would have self-appended a drained WAL line back into the very file being drained, and never actually reached the bound `ObserveTool`/`ObserveStop` seam. Fixed by adding `drainDispatch`, which routes hot-path ops straight to `runIngested` (the same function the live worker pool uses) and everything else through the full `dispatchOp`.

## Test summary

`go test ./internal/daemon/... -v` — **78 tests, 0 failures**. Every test-table row from the brief that names an explicit test name was implemented: `TestServicesAllNil` (13 ops, mcp excepted), `TestObservePromptRepliesWithinDeadline` + blocking variant, `TestDegradedPassiveSuppressesActingPaths`, `TestDegradedPassiveStillRecords`, `TestModeOffSkipsIngest`, `TestNAKDuplicateIsDedupedOnDrain`, `TestHandleOverridesDefaultRoute`, `TestHandleOnBareOptionsIsSafe`, `TestBindRunsInOrderAndDeclaresProducers`, `TestBreachDetectorTransitionsAfterThreeWindows`/`ResetsOnCleanWindow`/`RevertsAfterThreeCleanWindows`, `TestSpoolOnBreachFalseDoesNotTransition`, `TestHotModeTransitionWritesStateAndNAKs`, `TestConfigReloadDefersChunkChange`, `TestIdleExitWithZeroSessions`, `TestRunReturnsNilWhenLockHeld`, `TestIdleRunsByPriority`/`RespectsBudget`/`TaskPanicIsolated`/act.-prefix rows, `TestDeclaredProducerSetMatchesArchitecture`, `TestMarkerIsWrittenByFlushAndCheckpointOnly`. `TestSketchSetNeverWritesTriedBloom` already existed from Task 3 (`sketchset_test.go`) — confirmed present, not duplicated. Added `TestAdminPingAlwaysAnswersOK` per ruling #1's explicit requirement, plus `TestUnknownOpIsRefusedNotPanicked`, `TestHandlerPanicIsRecovered`, `TestAdminShutdownStopsTheDaemon`.

Verification commands run, all green:
- `go test ./internal/daemon/... -race -count=2` — pass (run repeatedly to confirm; see concerns below on flakiness elsewhere in the package from pre-existing code)
- `go test ./internal/...` — pass, all packages
- `go test ./test/guards/...` — pass
- `GOOS=linux go build ./...`, `GOOS=darwin go build ./...` — both clean
- `go run ./tools/devtool fmt` — one gofmt alignment fix applied and committed
- `go run ./tools/devtool lint` — golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips all PASS
- `go test ./... ` (full repo, once) — pass, all packages
- `go vet ./...` — clean

## Concerns

1. **Pre-existing flaky `-race` failure, unrelated to this task (as of the original submission — RESOLVED as of fix round 1).** At submission time, `internal/daemon/lock_test.go` (`TestLiveLockNotReclaimed`) and `spawn_test.go` (`TestEnsureRunning_AlreadyRunningNeverSpawns`) intermittently triggered a data race inside `internal/ipc/server.go`'s `waitForConns`/`Close` path under `-race`. Verified pre-existing at the time (reproduced via `git stash -u` against the untouched Task-3/4 code). Per the coordinator's fix-round-1 message, the branch's Task-2 history was replayed to fix this at the source (`internal/ipc`, not a file this task owns); it did not reproduce across this round's `-race -count=2` runs (repeated 4×).
2. **`precompactTimeoutMs()`** reads the PreCompact hook's manifest timeout via `pluginmanifest.Default(core.Version).Hooks.Hooks["PreCompact"][0].Hooks[0].Timeout` — the manifest does expose a real value (20 seconds → 20000ms), so this is not the "record 0 = unknown" fallback case the brief anticipated; that fallback path exists in the code for robustness only.
3. ~~`writeLatencyJSON`'s `"generated"` timestamp used `time.Now().UnixMilli()` directly~~ — moot as of fix round 1: `writeLatencyJSON` was deleted (I-1) in favor of `obs.Registry.Persist`, which owns its own timestamping.
4. **The nine "nil-tolerant function seams"** on `Services` were inferred (not spelled verbatim anywhere in the read source) from the handlers table + `DeclareProducers` pseudocode: `ObserveTool`, `ObservePrompt`, `ObserveStop`, `SessionStart`, `SessionEnd`, `PreCompact`, `Rehydrate`, `MCPInitialized`, `StatusExtra` — exactly nine, matching the brief's count. Signatures were designed to fit each route's actual call site; a later wave binding one of these should find the shapes natural, but they are this task's own invention where the brief did not fix them exactly.

## Files changed

- `internal/daemon/options.go` (new), `internal/daemon/idle.go` (new), `internal/daemon/budget.go` (new), `internal/daemon/handlers.go` (new), `internal/daemon/reload.go` (new)
- `internal/daemon/daemon.go` (rewritten — real `New`/`Run`/`Stop`/`Registry`/`Idle`/`Drain`)
- `internal/daemon/metrics.go` (extended — `loudTail`; originally also `writeLatencyJSON`, deleted in fix round 1, see below)
- `internal/daemon/spawn.go` (small addition — `removeSpawnLockFile`)
- `internal/daemon/options_test.go`, `internal/daemon/idle_test.go`, `internal/daemon/budget_test.go` (new)
- `internal/daemon/daemon_test.go` (fully rewritten — was `package daemon_test` pinning stub behavior; now `package daemon` white-box, pinning the real behaviors listed above)

---

## Fix round 1

Reviewer verdict on commit `115ba19d`: Needs fixes (1 Critical, 8 Important, 12 Minor). Full review at
`task-5-review.md`. All fixes amended into the single task commit (now `04f3c81f` after the branch's Task-2
history was replayed to fix an unrelated pre-existing `internal/ipc` race — same content, new SHA, per the
coordinator's instruction).

### Critical

**C-1 — re-entrant `Drain` self-deadlock.** `drainDispatch` previously routed every non-hot-path op through the
full `dispatchOp`, so a drained `flush` line landed in `handleFlush`, whose last step called `d.Drain(ctx)` again
— re-entering `drainer.Drain`'s plain, non-reentrant `sync.Mutex` on the same goroutine that already held it,
forever. Since `ipc.client.Send` spools every op (not just hot-path ones) on every connect failure, a `SessionEnd`
hook firing while the daemon is down leaves exactly this line for the next daemon's **startup drain** — which
runs before `Serve` ever accepts a connection — to trip over, wedging the daemon while it still holds
`daemon.lock`.

Fixed per the review's suggested option (a): `handleFlush` now delegates to a new `flushRoute(ctx, req, drain
bool)`; `drainDispatch` is an explicit, non-reentrant allow-list — hot-path ops → `runIngested`, `flush` →
`flushRoute(..., drain=false)`, `admin.*` → skipped entirely (no operator is waiting on a stale spooled admin
reply, and `admin.drain`/`admin.shutdown` would hit the identical hazard), everything else (`session.start`,
`checkpoint`, `status`, `mcp`) → the full `dispatchOp`, since none of those routes ever calls `Drain`.

Verified RED before the fix: reverted `drainDispatch` to the old blanket-`dispatchOp` shape, confirmed
`TestDrainOfSpooledFlushLineDoesNotDeadlock` hangs and times out (`daemon_test.go` log: "Drain of a spooled flush
line did not return — self-deadlock on drainer.Drain's own mutex"), then restored the fix and confirmed GREEN.
Added `TestDrainOfSpooledFlushLineDoesNotDeadlock` (direct) and `TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun`
(end-to-end through `Run`, asserting the daemon actually starts accepting connections).

### Important (all 8 fixed)

- **I-2 (sentinel wipe).** `handleSessionStart` assigned `h.Sentinel` as a whole struct literal on every
  acting session start, silently clearing `Observed`/`Chances` — fields `contract/history.go` documents as
  "never resets to false". Now only `Token`/`Session`/`MintedAt` are assigned individually; `Chances` is
  deliberately reset to 0 (a fresh token has had zero chances to be found yet, stated in a comment); `Observed`
  is never touched. New test: `TestSessionStartNeverWipesSentinelObserved`.
- **I-3 (`CheckBudgets` per-sample, not per-window).** `hotPathWorker` called `updateBudgetsCache()` (which wraps
  the stateful `obs.Registry.CheckBudgets`) after every sample rather than once per closed 512-sample window,
  inflating `BudgetBreach.Windows` by up to 512×. `breachDetector.Observe` now returns `(Transition, closed
  bool)`; `hotPathWorker` calls `updateBudgetsCache` only when `closed`. New test:
  `TestBreachDetectorObserveReportsClosedOnlyOnTheWindowBoundary`.
- **I-4 (reload never rewrote `state.bin`).** `state.bin` carries `ConnectDeadlineMs`/`AckDeadlineMs`/
  `MaxPayloadBytes`/`SpoolOnBreach`/`DaemonEnabled`, which every client reads at construction; a reload of any of
  those keys was invisible until the next unrelated `WriteState` call. `reloadConfig` now calls
  `ipc.WriteState(d.root, d.currentState())` whenever `len(changed) > 0`. New assertion added to
  `TestConfigReloadDefersChunkChange` (a reloaded `runtime.daemon.ackDeadlineMs` now reaches `state.bin`).
- **I-5 (`historyMu` held across `RunAll` + unbounded seam calls).** Both `handleSessionStart` and
  `handleCheckpoint` restructured into three phases: (1) locked — this package's own bounded work (`RunAll`,
  or the PreCompact history observation), saved, unlocked; (2) unlocked — the wave-3 seam call (`SessionStart`/
  `PreCompact`, whose own budget or re-entrancy must never serialize every other history-touching route behind
  it); (3) re-locked — re-load history (a concurrent route may have saved its own changes during phase 2), apply
  this route's remaining mutations, save. `checkpoint`'s ordering was also corrected to match the spec exactly
  (`WriteMarker` now runs before the `PreCompact` seam call, as specified, not after).
- **I-6 (unvalidated `req.TS`).** `ipc.DecodeRequest` performs no validation, so an absent/zero `"t"` produced an
  ~55-year "observed" latency feeding straight into the B-A histograms and the breach detector. Added
  `validHotPathTS` (rejects `ts <= 0`, more than `hotPathSampleFutureSlop` (250ms) ahead of `recvTS`, or more
  than `hotPathSampleMaxAge` (10s) stale) and a `hotpath_sample_invalid` counter; `recordHotPathSample` now
  rejects before touching any histogram or the breach-detector channel. New test:
  `TestRecordHotPathSampleRejectsInvalidTS`. (Two existing tests, `feedBreachingWindow` and `budget_test.go`'s
  direct `Observe` calls, used `TS:0` to simulate old samples — updated to `TS:1`/`recvTS:21` to stay valid under
  the new check while preserving the intended 20ms "observed" value.)
- **I-7 (`Run`'s `ctx.Done()` returned bare `nil`).** Sketches were never saved, WAL handles never flushed/closed,
  `metrics/latency.json` never written, `state.bin` left on disk, `daemon.lock` left held. `Run`'s `ctx.Done()`
  case now returns `d.Stop(context.Background())` (deliberately not the already-cancelled `ctx`, or `Stop`'s own
  bounded drain would be a no-op). New test: `TestRunCtxDoneGoesThroughStop`.
- **I-8 (`breachDetector.Reset()` dead code).** Its own doc comment promised a reset "when a new session resets
  the daemon-wide hot-path submode", but nothing called it. `handleSessionStart` now checks `registry.Get` before
  `registry.Ensure` and calls `d.breach.Reset()` when the session id is new — mirroring exactly the "new session"
  test `registry.Ensure` itself uses to decide whether to reset `HotMode`. `registry.go` (Task 3, out of scope)
  was not touched. New test: `TestSessionStartResetsBreachDetectorForNewSession`.
- **I-1 (ruling #20 dropped — two writers of `metrics/latency.json`).** `writeLatencyJSON` wrote a custom
  `{generated, budgets:[...]}` shape to the exact path `obs.Registry.Persist` also writes, with a different
  schema — whichever ran last won, making the file's shape nondeterministic. Deleted `writeLatencyJSON` entirely;
  `idleWriteMetrics` now calls a two-line `writeMetricsSnapshot` wrapper around `m.Persist(paths.Of(root))`, the
  ONE writer. `Stop`'s existing direct `d.m.Persist(...)` call was already correct and is unchanged.

### Minor — fixed vs. left

Fixed: **M-1** (deleted the unused `daemon.wg` field), **M-2**/**M-3** (documented the two mode-check sites that
deviate from the normative table, with the reasoning inline rather than silently), **M-4** (rewrote `dispatchOp`'s
stale doc comment — it is no longer `drainDispatch`'s function, and the old comment masked C-1 from a reader),
**M-5** (`applyHotPathTransition` now logs `breachDetector.Config()`'s own actual limit/need via a new accessor,
not the live — possibly since-reloaded and therefore different — `cfg` values), **M-6** (`ipc.NewServer` failure
in `Run` now releases the lock before returning; the deferred `cancel()` already stopped the workers), **M-9**
(added the missing `observe.prompt` WAL assertion to `TestServicesAllNil`; the `TestConfigReloadDefersChunkChange`
Loud-line assertion was left — see below), **M-10** (`TestServicesAllNil`'s cleanup now calls `dd.Stop` — which
is idempotent and synchronous via `sync.Once` — instead of racing `admin.shutdown`'s async `Stop()` goroutine with
a bare `dd.ing.Close()`), **M-12** (documented, at the one call site the comment is most useful, why `d.m` is
dereferenced unguarded in `handleCheckpoint`/`handleStatus`: `New` always seeds it, unlike `ingest`/`drain`, which
are also constructible directly by tests without going through `New`).

Left: **M-7** (moot — `writeLatencyJSON`, the site M-7 was about, no longer exists after I-1's fix; the
replacement `writeMetricsSnapshot` has no timestamp logic of its own, `obs.Registry.Persist` owns that). **M-8**
(`handleMCP` returns `OK:true` without calling `MCPInitialized`; cosmetic per the review itself — SP-13 replaces
the whole route). **M-9**'s Loud-line assertion (left unassessed to avoid a flaky cross-test dependency on the
process-wide `logging.LastLoud()` ring under `t.Parallel()`; the load-bearing behavior — the live config staying
on the old chunk block and `state/config-pending.json` being written — is already asserted). **M-11**
(`registry.Touch`'s no-op for a session `Ensure` never saw, across a daemon restart, is a real architectural
question the review itself frames as "consider" rather than a required fix; addressing it properly interacts with
the I-8 new-session-detection logic just added and deserves its own design pass rather than a rushed change here).

### Verification (all green)

- `go test ./internal/daemon/... -race -count=2` — pass, repeated 4× to confirm (the previously-flaky
  `internal/ipc` N-2a `WaitGroup` race the reviewer diagnosed is gone at the source, per the coordinator's
  note — not reproduced once across this round's runs)
- `go test ./internal/... ./test/guards/...` — pass, all packages
- `GOOS=linux go build ./...`, `GOOS=darwin go build ./...` — clean
- `go run ./tools/devtool fmt` — no changes needed this round
- `go run ./tools/devtool lint` — golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips
  all PASS
- `go vet ./...` — clean
- `go test ./...` (full repo, once) — pass, all packages
- `internal/daemon` test count: 91 (was 78; +13 new/expanded regression tests this round)

---

## Final fix wave

Whole-branch review (`final-review.md`) approved everything on the branch except 2 Importants + 5
small minors, spread across three commits (daemon, cli, bench). Implemented via history replay —
`git checkout --detach ef50515f` (the daemon commit as it stood after fix round 1), apply +
`commit --amend`, `git cherry-pick` the next commit, apply + amend, repeat — rather than a fourth
commit, per the coordinator's explicit instruction to fold each fix into its owning commit. All
seven items landed; no cherry-pick conflicts.

### DAEMON (folded into commit 5)

- **FR-1 (Important) — data race on `SessionState.Live`.** `sessionIsLive` (the drainer's `IsLive`
  callback) read `s.Live` on the shared `*SessionState` pointer `registry.Get` returns, AFTER the
  registry's RLock was released — racing `Ensure`/`Touch`/`End`, which mutate that same struct
  under the write lock. Added `SessionRegistry.IsLive(id) bool`, which reads `Live` entirely under
  RLock; `sessionIsLive` now calls it instead of `Get`+field-read. New tests:
  `TestSessionRegistry_IsLive` and `TestSessionRegistry_IsLiveConcurrentWithEndIsRaceFree` (the
  latter drives `IsLive` and `End`/`Ensure` concurrently under `-race`).
- **FR-2 (Important) — `handleObservePrompt` swallowed WAL-append failures silently.**
  `observe.tool`/`observe.stop`'s own failure paths return `OK:false` (a NAK the client answers by
  spooling, so the event stays durable either way); `observe.prompt` always ACKs, so its
  `EncodeRequest`/`ing.Accept` errors were the one hot-path event that could vanish with zero
  observability. Now both error paths call `log.Warn` and increment a new `l0_accept_error`
  counter (underscore idiom). New test: `TestObservePromptWALFailureIsObservable`, which forces
  `ingest.Accept`'s own `os.MkdirAll` to fail deterministically and cross-platform (a regular file
  sits where `.qompack` must be a directory) and asserts the counter fires exactly once while the
  reply still ACKs.
- **FR-3 (Minor) — unvalidated `blobRef.Blob` path.** A hostile or corrupt spool/WAL line could
  name a `Blob` value that escapes `spool/` via `..`, an absolute path, or a separator;
  `resolveBlob` would read (and, on success, delete) whatever file it named. Added a check —
  `filepath.Base(ref.Blob) != ref.Blob` or missing the shipped client's own `blob-` prefix both
  refuse the descriptor before it is ever joined — costing zero legitimate cases (the shipped
  client only ever writes `blob-<pid>-<n>.bin`). New test: `TestResolveBlob_RefusesPathTraversal`,
  four traversal shapes, asserting the descriptor is left untouched and a real out-of-spool file
  survives unread and undeleted.
- **FR-4 (Minor) — genuine `Serve`-failure arm skipped `Stop`.** The `ctx.Done()` arm already
  called `Stop` (fix round 1, I-7); the sibling arm — an unexpected transport failure reaching
  `case err := <-serveErrCh` with `d.stopped` still open — returned the raw error directly, leaving
  the lock held, `state.bin` advertising a dead daemon, and WAL/sketches/metrics unflushed. Now
  calls `_ = d.Stop(context.Background())` before returning `err` (the original error is preserved
  verbatim; `Stop`'s own, ordinarily-nil error is deliberately not allowed to shadow it). No
  dedicated new test — this arm is only reachable via a genuine transport-layer failure, which is
  impractical to trigger deterministically without touching `internal/ipc` (out of scope); the
  existing `Stop` test coverage (`TestRunCtxDoneGoesThroughStop`, `TestAdminShutdownStopsTheDaemon`)
  already proves `Stop`'s own cleanup is correct regardless of which arm calls it, and `Stop` is
  idempotent via `sync.Once`.

### CLI (folded into commit 6)

- **FR-6 (Minor) — `runtime.daemon.enabled=false` did not stop session-start from spawning.**
  `spec.preSend` (`daemon.EnsureRunning`) ran unconditionally, before and independently of the
  `st.DaemonEnabled` check `Send` applies — an operator who disabled the daemon still got a
  resident process spawned at every session start (serving nothing, since every client spools
  under `DaemonEnabled=false`, but its idle drain quietly processed the spool anyway).
  `hookSpec.preSend`'s signature grew a `st ipc.State` parameter (the same 32-byte record `doHook`
  already read, so no second disk read); `ensureDaemonRunning` now returns immediately when
  `!st.DaemonEnabled`, alongside its two existing early-return guards. New test:
  `TestEnsureDaemonRunning_GatedOnDaemonEnabled`, both states — `self` names a nonexistent path, so
  an attempted `EnsureRunning` reaches `SpawnDetached` (which fails fast, no process ever actually
  starts) and logs "spawn failed" via `Warn`, an observable, deterministic proxy for "EnsureRunning
  ran" that needs no real daemon process on either side of the assertion.
- **N-6 (Minor) — stale "exactly one non-test file" claim.** `fault_noinject.go`'s own comment
  claimed the `QOMPACK_FAULT` literal appears in exactly one non-test file, contradicting
  `fault.go`'s own (correct) two-file contract. Verified via `grep -rln QOMPACK_FAULT internal/ |
  grep -v _test.go`: the two files are `internal/cli/fault.go` and `internal/daemon/spawn.go`
  (which strips the variable from a spawned daemon's environment). Comment corrected to name both.

### BENCH (folded into commit 7)

- **R2-2 (Minor) — unconditional "stays hook-spawn-dominated" claim.** `buildNotes`'s warm-up
  composition note asserted the gated B-A population "stays hook-spawn-dominated" regardless of
  the actual `--iterations` value used for the run — true at the default n=2000 (64/2064 ≈ 3.1%)
  but not necessarily true for a smaller run, which is exactly the kind of unmeasured claim this
  harness's own doctrine (honesty of measurement, §1.3 RC-3) exists to forbid. The note now computes
  and prints `100 * warmHotTranche / (iterations + warmHotTranche)` for the actual run. New tests:
  `TestBuildNotes_WarmUpProportionIsComputedNotAsserted` (both n=2000 and a small n=10, where the
  tranche is a large share, proving the note reflects the real number either way) and
  `TestBuildNotes_NoWarmUpOmitsTheProportionNote` (unchanged negative case).

### Replay verification

- `git checkout --detach ef50515f` → applied items 1-4 → `go test ./internal/daemon/... -race
  -count=2` green (repeated) → `commit --amend --no-edit` → **commit 5'' = `3bd359944a233afcdd831d330f5f490536feced2`**
- `git cherry-pick 6fc27a3b` (clean, no conflicts) → applied items 5-6 → `go test ./internal/cli/...
  -race -count=2` green → `commit --amend --no-edit` → **commit 6'' = `d040fc07b64487a065860d2edb9c7c81c5b11dd4`**
- `git cherry-pick b6181b7a` (clean, no conflicts) → applied item 7 → `go test ./test/bench/... -race`
  green → `commit --amend --no-edit` → **commit 7'' = `93f712622202050d0a9de0e936ba5a4b30fdefc0`**
- `git branch -f feat/sp05-daemon-ipc-and-hot-path HEAD && git checkout feat/sp05-daemon-ipc-and-hot-path`
- Verified: exactly 7 commits on `develop..HEAD`; commits 1-4 (`71f50ba`, `fe73ad1`, `e9a5453`,
  `6bd2dff`) byte-identical (unchanged SHAs); commits 5''/6''/7'' carry their original subjects and
  bodies verbatim (diffed against the pre-wave commit messages), no attribution trailers on any of
  the three.

### Full gate (all green)

- `go test ./...` (full repo, once) — pass, all packages
- `go test ./test/e2e/... -race` (once) — pass (89.6s)
- `go test ./test/guards/...` — pass
- `GOOS=linux go build ./...`, `GOOS=darwin go build ./...` — clean
- `go run ./tools/devtool fmt` / `lint` (golangci-lint, nomagic, importgraph, testdeps, bindeps,
  sleepcheck, stubskips) — all PASS
- `go vet ./...` — clean
- One full bench-harness run (`go run ./test/bench/hotpath --warm-daemon --json ...`, n=2000,
  windows/amd64): **B-A [PASS]** (p99=4.096ms vs 15ms limit), **B-B [PASS]** (p99=0.704ms vs 2ms
  limit), **B-E [PASS]** (p99=195.058ms vs 2000ms limit); B-D and B-A_spawn_estimate reported, not
  gated, as designed. The R2-2 note printed "is 3.1% of the gated population for this run" —
  confirming the fix computes rather than asserts.

---

## Shutdown-race fix

The branch-finishing pre-merge gate found `TestAdminShutdownStopsTheDaemon` failing intermittently
on Windows: `go test ./internal/daemon/ -run TestAdminShutdownStopsTheDaemon -count=10` reproduced
`testing.go:1464: TempDir RemoveAll cleanup: unlinkat ...\.qompack\tmp: The directory is not
empty.` within 10 runs (confirmed here too: reproduced on run 13/25 before the fix, ~2.3s to
trigger).

**Root cause.** `admin.shutdown`'s handler runs `Stop` asynchronously
(`go func(){ _ = d.Stop(context.Background()) }()`) so its reply write is never blocked on
shutdown. `Stop`'s own cleanup sequence closes `d.stopped` and cancels `runCtx` as its FIRST two
actions — everything else (drain, ingest close, sketches save, **`d.m.Persist(paths.Of(d.root))`**,
state removal, server close, lock release) runs afterward, still inside the same asynchronous
goroutine. `Run`'s own select loop returns as soon as that early `d.stopped` close/cancel fires
(it does not, and structurally cannot, wait for the REST of `Stop`'s cleanup, since `Stop` is
running on a different goroutine it has no handle to). The test only waited for `Run` to return,
then returned itself — racing `t.TempDir()`'s automatic `RemoveAll` against `Stop`'s still-running
`d.m.Persist` call, which writes `.qompack/metrics/latency.json` through `paths.WriteAtomic`:
`WriteAtomic` creates its temp file via `os.CreateTemp(paths.Of(root).Tmp, tempFilePrefix)` (traced
in `internal/paths/atomic.go`'s `tmpDirFor`) and renames it into place a few statements later. When
`RemoveAll` lists `.qompack/tmp/` in the narrow window between that `CreateTemp` and the rename (or
the deferred cleanup-on-error `os.Remove`), it finds a file that did not exist when the walk
started, and Windows refuses to delete a non-empty directory. This is exactly the shape the
coordinator's diagnosis anticipated (option 1: "the test simply doesn't wait for the daemon to
fully stop") — confirmed, not a bug in `Stop`'s own ordering or in `WriteAtomic`'s failure-path
cleanup, and no files were ever deleted from the test to work around it.

**Fix.** Added a second, product-level completion signal distinct from the existing `d.stopped`
(which intentionally closes early, so `Run`'s select loop can tell a `Stop`-driven `Serve` return
from a genuine transport failure — fix round 2, FR-4): `stopDone chan struct{}`, closed by a
`defer` at the very top of `Stop`'s `sync.Once` body, so it fires only once EVERY cleanup step in
that closure has actually returned. `TestAdminShutdownStopsTheDaemon` now waits on `dd.stopDone`
(white-box, matching this file's existing pattern) after `errCh`, with the same `t.Fatal`-on-timeout
shape the rest of the file already uses — no `time.Sleep`, no polling.

**Verification.**
- `go test ./internal/daemon/ -run TestAdminShutdownStopsTheDaemon -count=25` — clean; also run at
  `-count=50` — clean (was previously failing within 10-13 runs)
- `go test ./internal/daemon/... -race -count=2` — clean, repeated twice
- Full gate re-run: `go test ./...` (once, all packages including `test/e2e` and `test/guards`),
  `GOOS=linux/darwin go build ./...`, `go run ./tools/devtool fmt`/`lint`, `go vet ./...` — all
  clean

**Replay.** `git checkout --detach 3bd35994` (commit 5 as it stood after fix round 1) → applied the
fix → verified → `commit --amend --no-edit` → **commit 5''' = `3cd8719ad5afa9bc5bc0cff40765997ec6c3cd0b`**
→ `git cherry-pick d040fc07` (clean, no conflicts, no changes needed this round) →
**commit 6''' = `b69d7f37a2ae9463b5664998678a3425f07a0b17`** → `git cherry-pick 93f71262` (clean, no
conflicts, no changes needed this round) → **commit 7''' = `10ca6fcf5c25ee33dee8b4ee13835b9e68e161c3`**
→ `git branch -f feat/sp05-daemon-ipc-and-hot-path HEAD && git checkout
feat/sp05-daemon-ipc-and-hot-path`. Verified: exactly 7 commits on `develop..HEAD`; commits 1-4
unchanged; commits 5'''/6'''/7''' carry their original subjects/bodies verbatim, no attribution
trailers.

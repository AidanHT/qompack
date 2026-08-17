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


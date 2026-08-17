# Task 3 review — `feat(daemon): singleton lock, session registry, WAL ingest queue, and drain`

Reviewer: task reviewer (read-only). Commit under review: `a73a2384739def27b1d9c8b2d0f8daa599f5ddb0`
Range: `dd2f5e22..a73a2384` — **exactly one commit** (`git rev-list --count` = 1), working tree clean.

## Verification actually run (not taken from the report)

| Command | Result |
|---|---|
| `git show -s --format=%B a73a2384` | Subject and body match the brief **verbatim**; no `Co-Authored-By`, no `Generated with`, no attribution trailers. Author == committer == `Aidan <aidantran120@gmail.com>`. |
| `go vet ./internal/daemon/...` | clean |
| `go test ./internal/daemon/... -race -count=1` | `ok … 4.365s` |
| `go test ./test/guards/... -count=1` | `ok … 21.013s` (stubs guard, network guard, writeset guard all green) |
| `GOOS=linux go build ./...` / `GOOS=darwin go build ./...` | both clean |
| `go run ./tools/devtool lint` | `PASS golangci-lint`, `PASS nomagic`, **`PASS importgraph`**, `PASS testdeps`, `PASS bindeps`, **`PASS sleepcheck`**, `PASS stubskips` |
| `go test -bench BenchmarkIngestAccept -benchtime 2000x` | `6708 ns/op` on this host (report said ~4.1 µs; same order, ~0.3 % of the 2 ms B-B budget) |

Global constraints spot-checked and **held**: WAL is `spool/wal-<session>.ndjson` via `paths.AppendOnly` with no fsync; ACK ordering is structurally enforced (`Accept` returns before enqueue is observable — proven by `TestIngestACKPrecedesProcessing`); `ringCapacity = 4096` with the exact `//nomagic:allow` annotation the spec's snippet shows; dedup key is `core.HashBytes("qompack.wal.v1", line)`; `drain.json` offsets exist; no `time.Sleep` outside tests (sleepcheck); no writes outside `.qompack/` (every path goes through `paths.Of(root).{Run,Spool,State,Sketches}`); `net` is not imported by `internal/daemon` (importgraph + `test/guards/network_test.go`); `os/exec` is used only in `spawn.go`, which `doc.go` already sanctions; counter names use the underscore idiom and reuse `l0_dropped`/`l0_externalized` from `internal/ipc` rather than redeclaring them; `internal/obs`, `internal/contract`, `internal/cli`, `Qompack.md` and every wave-3 package are untouched by the diff.

---

# VERDICT 1 — SPEC COMPLIANCE

**Result: met, with five declared adaptations (all covered by binding rulings) and three undeclared adaptations. Nothing was silently dropped.**

## Deliverables (commit-3 checklist)

| Deliverable | Status |
|---|---|
| `internal/daemon/lock.go` | **met** |
| `internal/daemon/lock_unix.go` / `lock_windows.go` | **met** — `pidAlive(int) (alive, known bool)`, `//go:build !windows` / `windows`, `(false,false)` = "no opinion" on Windows exactly as specified |
| `internal/daemon/spawn.go` / `spawn_unix.go` / `spawn_windows.go` | **met** — local `createNoWindow = 0x08000000`, `detachedProcess = 0x00000008`, `HideWindow: true`; `Setsid: true` on unix; both constants declared locally per §2.5's closed dependency list |
| `internal/daemon/registry.go` | **met** (signature adaptation, below) |
| `internal/daemon/ingest.go` | **met** |
| `internal/daemon/drain.go` | **met** (standalone `drainer` per binding ruling) |
| `internal/daemon/sketchset.go` | **met** — `Top *sketch.MisraGries` kept per ruling; `mu`, `dirty`, `NewSketchSet/Load/Save/Read/Write` all present |
| Tests written for lock/registry/ingest/drain | **met** — `lock_test.go`, `registry_test.go`, `ingest_test.go`, `drain_test.go`, plus `spawn_test.go`, `sketchset_test.go`, `bench_test.go` |
| `go test ./internal/daemon/... -race`; devtool lint importgraph green | **met** (re-verified above) |

## Symbol-by-symbol against the spec's signatures

| Spec | Shipped | Status |
|---|---|---|
| `AcquireLock(projectRoot string, a ipc.Addr, clk core.Clock) (*Lock, error)` | identical | met |
| `ReadLock(projectRoot) (LockInfo, bool)` | identical | met |
| `(*Lock).Heartbeat() error`, `(*Lock).Release() error` | identical | met |
| `Lock struct{ path; f *os.File; hb; clk }` | `f *os.File` dropped | **adapted, justified** — the ruling directs the JSON in via `paths.CreateNew`, which returns no handle (`internal/paths/appendonly.go:113`), so the field would be permanently nil. Declared in report §1. |
| lock file `0o600` | `paths.CreateNew` ends with `os.Chmod(p, 0o444)` | **adapted, forced by the ruling.** `removeLockFiles`/`Release` chmod back to `0o600` before unlink — the Windows read-only-blocks-delete trap was correctly anticipated (`lock.go:192-196`, `228`) |
| 5-step staleness protocol | `lockIsStale` (`lock.go:116-147`) implements steps 1→4 in order, stopping at the first decisive answer; step 5 (`remove + retry once + ErrLockHeld`) at `lock.go:87-93` | met |
| `staleAfter = 90 * time.Second`, hb = `.qompack/run/daemon.hb` | `lock.go:23`, `lock.go:32` | met |
| `EnsureRunning(projectRoot, self, log, clk) (bool, error)` / `SpawnDetached(projectRoot, self) error` | identical | met |
| 20 ms dial, 25 ms ticker, 1500 ms bound as §-annotated named constants | `spawn.go:17`, `25-28` — ticker, no `Sleep` | met |
| env = `os.Environ()` + `QOMPACK_PROJECT_ROOT`, `QOMPACK_FAULT` stripped | `buildSpawnEnv` (`spawn.go:120-129`), tested twice | **met — the QOMPACK_FAULT strip is present and asserted** |
| devnull stdio + `Process.Release()` | `spawn.go:94-104` | met |
| `Ensure(e, now)` | `Ensure(e, now, cfg config.Config)` | **adapted, undeclared** — see Minor 10 |
| `Touch/End/Live/Snapshot/LastActivity/HotMode/SetHotMode` + new-session HotSync reset (INFO-logged with previous mode+reason) + eviction rules | all present (`registry.go`), reset logged at `registry.go:141-142` | met |
| `job`, `ingest` struct shape, `newIngest`, `Accept`, `Start`, `CloseSession`, `Close` | all present; struct gains `spoolDir` + `clientSpool` | met |
| `ringCapacity`/`walRotateBytes`/`seenCapacity = 65536`, worker pool `max(2, NumCPU/2)` | `ingest.go:21,24,27,34-40` | met |
| `Drain(ctx) (int, error)` on `*daemon` | standalone `drainer` + `DrainConfig` | **adapted per binding ruling** (brief line 20) |
| Drain algorithm steps 1–5 | see below | met except step-4 ordering (Important 4) |
| `NewSketchSet(cfg)` from `cfg.Sketches`, `NewMisraGries(64)`, `Load` tolerant of `ErrNotFound`/`ErrNotImplemented` at Debug, other errors `Loud`, `Save` skips `tried.bloom` unconditionally, no-op when `!dirty` | `sketchset.go` | met |

## Test table — rows in Task 3's scope

| Row | Status |
|---|---|
| `TestAcquireLockExclusive` | **met** (`lock_test.go:22`) |
| `TestStaleLockReclaimed` (pid 999999, 10-min-old hb, no listener) | **met** (`lock_test.go:41`) — asserts the file is rewritten with `os.Getpid()` |
| `TestLiveLockNotReclaimed` (real listener wins over dead pid + old hb) | **met** (`lock_test.go:63`) |
| `TestHeartbeatUpdatesMtime` (FakeClock +60 s) | **met** (`lock_test.go:93`) |
| `TestRegistryNewSessionResetsHotMode` | **met** (`registry_test.go:17`) — both halves (new id resets, same id does not) |
| `TestRegistryEvictsEndedOverMax` | **met** (`registry_test.go:37`) |
| `TestRegistryAllLiveOverMaxKeepsAll` | **met** (`registry_test.go:63`) — also asserts the Loud fires exactly once across two over-limit Ensures |
| `TestIngestWALIsExactBytes` | **met** (`ingest_test.go:24`) |
| `TestIngestRingFullSpillsToSpool` (5 WAL, 3 spool, `l0_ring_full == 3`, each `Accept` < 1 ms on the FakeClock) | **met with a weakened assertion** — the non-blocking check is `< time.Second` real wall clock, not "< 1 ms on the FakeClock" (Minor 3) |
| `TestIngestACKPrecedesProcessing` | **met, adapted per brief line 22** (unit-level: `Accept` returns while a worker is blocked on a channel) |
| `TestDrainIsIdempotent` | **met** (`drain_test.go:56`) |
| `TestDrainResumesAfterCancel` (1000 lines, cancel ~200, total exactly 1000) | **met** (`drain_test.go:83`) |
| `TestDrainResolvesBlobs` | **met** (`drain_test.go:120`) — asserts the restored payload, the cleared `Raw`, and the deleted blob |
| `TestDrainSurvivesCorruptLine` (4 dispatches, `drain_file_error` 1, file still deleted) | **partially met** — 4 dispatches and the delete are asserted; the counter is **not**, because the drainer is built with no `Metrics` registry (Minor 4) |
| `TestSketchSetNeverWritesTriedBloom` | **met** (`sketchset_test.go:17`) — bytes *and* mtime asserted, with `Write` first so `dirty` is set and the assertion is not vacuous on the `dirty` short-circuit |
| `BenchmarkIngestAccept` (B-B p99 < 2 ms) | **met** — 6.7 µs/op measured |

Bonus rows added beyond the table (all reasonable): `TestReleaseIsIdempotent`, `TestReadLock_UnparseableFileReportsNotOK`, `TestIngestWALRotates`, `TestDrainKeepsLiveSessionWAL`, `TestWalSessionID`, `TestBuildSpawnEnv_*`, `TestBuildSpawnCommand_*`, `TestEnsureRunning_*`, three extra `SketchSet` rows.

## Undeclared adaptations (flagged, not hidden — but not in the report either)

1. **B-B timing scope** — the spec times all *three* of `Accept`'s steps end to end; the implementation times only the WAL append. Defensible (`internal/obs/budgets.go:17` defines B-B as "daemon read to WAL append returned"), but it means the ring-full spill's file write is unmeasured. → Minor 1.
2. **`state/drain.json` persistence point** — spec step 4 persists *per file, on EOF, before the remove*; the implementation persists once at the end of `Drain`. → Important 4.
3. **`SessionRegistry.Ensure` signature** gained a `cfg config.Config` parameter. → Minor 10.

Nothing else was dropped. The three "Task 4's rows" (options/idle/budget/handlers) are correctly absent.

---

# VERDICT 2 — CODE QUALITY

## Critical (0)

None.

## Important (5)

### I-1 — Blob resolution exists only on the drain path; the hot path dispatches externalized requests with an empty payload, and the dedup set makes it unrecoverable
`internal/daemon/ingest.go:257-277` (`dispatch`) together with `internal/daemon/drain.go:299-323` (`resolveBlob`).

**What.** `internal/ipc/client.go:209-214` externalizes on the **live wire path**, not just the spool path: any request whose encoded line reaches `ExternalizeThreshold` is rewritten with `Event.ToolResponse = nil` and `Raw = {"blob":…,"field":"e.tool_response"}` *before* it is written to the socket. So the daemon routinely receives descriptor lines on the hot path. `ingest.dispatch` hands `j.req` straight to `run` with no blob resolution — the handler sees an empty `ToolResponse`. Worse, `dispatch` then records `j.key` in `seenSet` (`ingest.go:267`), so when `Drain` later reads the same line out of the WAL it hits the `Seen.SeenOrAdd` short-circuit at `drain.go:234` and returns **before** `resolveBlob` at `drain.go:238`. The blob is never resolved and never deleted.

**Why it matters.** Silent loss of exactly the largest and most valuable tool outputs, plus an unbounded `blob-*.bin` leak in `.qompack/spool/` that nothing GCs (`ipc.SpoolFiles` only enumerates `wal-*`/`client-*`, so no sweeper will ever see them). It is invisible: no counter, no log line, and no test in this commit covers a blob arriving through `Accept`.

**Fix.** Lift `resolveBlob` out of `drainer` into a package-level helper over `(root, log, req)`, call it in `ingest.dispatch` before `run` (and before the `SeenOrAdd`, or at minimum keep the resolve ahead of the dedup check on both paths), and add an `TestIngestResolvesBlobs` row. If the controller prefers to defer, it must become a binding note in Task 4's brief — Task 4 can fix it inside its `run` func, but only if it is told to.

### I-2 — Windows lock-acquisition race: the heartbeat is written *after* the lock file, so a competing acquirer in that window declares a just-won lock stale and takes it
`internal/daemon/lock.go:83-102`, with `lock.go:142-146`.

**What.** `AcquireLock` does `CreateNew(lockPath)` → (window) → `touchFile(hbPath)`. A second daemon whose `CreateNew` fails inside that window runs `lockIsStale`: step 2's dial fails (the winner has not called `Serve` yet), step 3 returns "no opinion" on Windows (`lock_windows.go`), and step 4 does `os.Stat(hbPath)` → `ErrNotExist` → `return true` ("no heartbeat at all: nothing to call recently alive"). The loser then `removeLockFiles` — deleting the winner's lock — and re-`CreateNew`s successfully. **Both processes now believe they hold the per-project singleton.**

POSIX is safe (step 3's `kill(0)` decides before step 4 is reached). Windows is the exposed platform, and it is the primary host. The window is microseconds but widens under a cold filesystem, AV scanning, or CI contention, and `CreateNew` does an `f.Sync()` plus an `os.Chmod` inside it (`paths/appendonly.go:114-129`), which is not free.

**Why it's only Important, not Critical.** `lock.go:36-40`'s own doc comment already anticipates the second line of defence: the loser's subsequent `ipc.NewServer` bind will fail with `ipc.ErrAddrInUse`, which the caller is told to wrap as `ErrLockHeld`. So the damage is bounded to a redundant daemon start — provided Task 4 actually implements that wrapping.

**Fix.** Close the window rather than relying on the bind: in step 4, when `daemon.hb` is absent, fall back to the **lock file's own** mtime (or its recorded `Started` field) instead of returning `true`. One-line change at `lock.go:142-146`, and it also fixes the symmetrical case of a hand-written lock with no heartbeat. Alternatively write `daemon.hb` *before* `CreateNew(lockPath)`.

### I-3 — `EnsureRunning`'s liveness criterion is stricter than the spec's, and the failure mode is a 1500 ms stall on every hook invocation
`internal/daemon/spawn.go:63` and `:76`, via `probeAlive` at `internal/daemon/lock.go:154-172`.

**What.** The spec says `EnsureRunning` "dials the resolved address with a 20 ms deadline; on success returns `(false, nil)`". The implementation instead requires a full round trip that ends in `Response.OK == true`. `internal/ipc/server.go:219-222` sends `ACK` **iff `resp.OK`** — so this is not "is anyone listening", it is "did the daemon's op router return OK for `admin.ping`".

**Why it matters.** If Task 4/5's routing table returns `OK:false` for `admin.ping` (unrouted-op default, admin namespace refusal, degraded mode, `ModeOff`), then on **every** hook invocation `EnsureRunning` sees a live daemon as dead, calls `SpawnDetached` (a real process launch), then burns the full `ensureRunningPollBound = 1500 ms` polling before returning `core.ErrNotFound`. That is a spawn storm plus a 1.5 s stall on the B-A path whose budget is 15 ms. The report flags this only for `lock.go`, where the consequence is benign; in `spawn.go` it is not benign.

**Fix.** Preferred: export a raw dial probe from `internal/ipc` (`func Probe(a Addr, timeout time.Duration) bool` wrapping the unexported `dial` + immediate `Close`) and use it in both `probeAlive` call sites — `net` stays inside `ipc`, the constraint is honoured, and the semantics match the spec. Minimum acceptable: a guard test in Task 4/5 asserting `admin.ping` is routed to an unconditional `Response{OK:true}`, plus a `//` note at `spawn.go:63` recording the dependency.

### I-4 — `state/drain.json` is persisted once at the end of `Drain`, not per file on EOF; a crash mid-drain loses every offset recorded so far
`internal/daemon/drain.go:146-149` versus spec step 4 ("On EOF: mark `Done`, persist `state/drain.json` via `paths.WriteAtomic`, **then** `os.Remove` the file").

**What.** `drainFile` sets `fs.Done = true` and removes the file (`drain.go:254-263`); `saveState` runs only after the whole file loop (`drain.go:146`). A crash between file A's removal and the end of `Drain` leaves `drain.json` describing neither A (gone, fine) nor B (half-consumed, offset unrecorded) — so on restart B is re-read from offset 0 and every line already dispatched from it is dispatched again.

**Why it matters.** This is precisely the property the commit message claims: "Drain is idempotent and resumable because it records consumed offsets." The cancel path is covered (tested), the crash path is not, and the crash path is the one §2.4 cares about. Cost of the fix is one `WriteAtomic` per drained file, of which there are a handful.

**Fix.** Move `saveState(st)` into `drainFile`, immediately after `fs.Done = true` and **before** the `os.Remove` — exactly the spec's ordering — and keep the end-of-`Drain` save for the cancelled/partial case.

### I-5 — The ring-full spill is redundant with the WAL append that always precedes it; it manufactures duplicates and a file the drainer deletes out from under a live handle
`internal/daemon/ingest.go:134-141` and `:221-226`; `internal/daemon/drain.go:270-275` (`shouldDelete`). **This is the implementer's concern (b), re-scoped.**

**What.** `Accept` appends to the WAL *before* attempting the ring enqueue (`ingest.go:117-127` then `134`). Every spilled line is therefore **already durable in `wal-<session>.ndjson`**, which `Drain` reads first. The spill to `client-<daemonpid>.ndjson` adds a second copy of the same event. Two consequences:

- **Duplicate dispatch whenever dedup misses.** The two copies collapse only if `HashBytes(domain, walLine) == HashBytes(domain, spillLine)`. The spill re-encodes via `ipc.EncodeRequest` (`spool.Append`, `internal/ipc/spool.go:108`) rather than writing the received bytes; byte-identity therefore depends on `EncodeRequest(DecodeRequest(x)) == x` holding for every request shape. It does hold for the current `Request`/`hookio.Event` structs (fixed field order, `json.RawMessage` verbatim, `null` round-trips), but it is an undocumented invariant that no test pins, and `hookio.Event.Extra` (`json:"-"`) is already a field that does *not* survive the round trip. Any future `Event` field with `omitempty`, a map, or a custom marshaller silently turns this into double processing.
- **Orphaned inode on POSIX.** `shouldDelete` returns `true` for every `client-*` name, so the drainer deletes the ingest's own live spill target while `ipc.spool` still holds the handle (`internal/ipc/spool.go:56`, `f` is cached for the process lifetime and never reopened). On POSIX the unlink succeeds and all later spills go to an unreachable inode whose blocks are retained up to `spoolMaxBytes` (64 MiB). On Windows the `os.Remove` fails, logs a WARN, and the next drain recovers via the size check — so the platforms diverge.

**Why it is Important and not Critical.** No data is lost: the WAL copy is authoritative and is always drained. The damage is duplicate work, up-to-64 MiB of unreclaimable disk on POSIX, and platform-divergent behaviour.

**Fix (preferred, and it is a deletion).** Drop the spill entirely. The spec's own justification for it — "the daemon has the data and will drain it" — is satisfied by the WAL append two lines earlier. Keep `l0_ring_full` (the signal is real), drop `clientSpool` and `spill()`, and rewrite `TestIngestRingFullSpillsToSpool` to assert "5 WAL lines, `l0_ring_full == 3`, `Accept` never blocks". If the controller wants the spill kept for spec-literalism, then `DrainConfig` needs a `SkipDelete func(base string) bool` (or the ingest must expose `SpoolPath()`) so a live spill target is offset-marked like a live WAL rather than deleted.

## Minor (12)

1. **`ingest.go:116-124` — B-B timing excludes the ring enqueue and the spill.** `obs.Timed` wraps only `appendWAL`; the spec times all three steps end to end, and the spill (a real file write) is the expensive branch. Defensible against `obs/budgets.go:17`'s own definition, but the divergence is undocumented. *Fix:* either widen the timed closure or add a one-line comment at `ingest.go:117` citing `budgets.go:17`.
2. **`metrics.go:14-25` — `histName` allocates on every hot-path call.** `obs.Budgets()` (`internal/obs/budgets.go:63`) constructs a fresh 6-element slice with 6 closures per call, and `histName` is invoked once per `Accept` (B-B, a *gated* budget) and once per `dispatch`. *Fix:* resolve both names once — `var histBB = histName(obs.BB)` package-level, or cache them on `ingest` in `newIngest`.
3. **`ingest_test.go:60-65` — the non-blocking assertion is `< time.Second` real wall clock**, where the spec row says "assert each call `< 1 ms` on the `FakeClock`". A one-second bound would not catch a regression that made `Accept` block for 900 ms. *Fix:* assert against the injected `FakeClock`, or tighten the wall-clock bound to single-digit ms.
4. **`drain_test.go:194` — `TestDrainSurvivesCorruptLine` builds the drainer with no `Metrics`**, so the spec row's `drain.file_error == 1` half is never asserted. *Fix:* pass `obs.New(clk)` and assert `m.Counter(counterDrainFileError).Value() == 1`.
5. **`lock.go:218-244` — `Heartbeat`/`Release` never verify ownership.** A daemon whose lock was reclaimed out from under it (I-2, or a legitimate 90 s-stale takeover) keeps refreshing, and then deletes, the *new* owner's `daemon.lock` and `daemon.hb`. *Fix:* re-read the file and compare `PID` (and ideally `Started`) against this process before `Chtimes`/`Remove`.
6. **`drain.go:315-322` — a blob descriptor on a request with a nil `Event` has its blob read, discarded, `Raw` cleared, and the file deleted.** Unreachable from the shipped client (it only externalizes when `Event != nil`) but reachable from a corrupt or hostile spool line. *Fix:* move the `os.Remove` inside the `req.Event != nil` branch.
7. **`drain.go:238-242` — `Dispatch`'s `ipc.Response` is discarded and the offset advances unconditionally.** A dispatch that hits the 5 s deadline, or returns `OK:false`, still consumes the line permanently. The spec prescribes the unconditional advance, so this is conformant — but it deserves a comment recording that a failed dispatch is an accepted loss, since the surrounding prose is all "never data".
8. **`ingest.go:79, 232-240` — `wg` is `Add`ed but never `Wait`ed, and there is no `Wait`/`Stop`.** `Close()` explicitly declines to stop the pool. Task 4 has no way to join workers on shutdown, so a worker can still be mid-`run` when the daemon tears down the store. *Fix:* add `func (i *ingest) Wait() { i.wg.Wait() }`.
9. **`ingest.go:92 — `ipc.NewSpool(spoolDir)` gives the spill writer `logging.Nop()` and a nil registry** (`internal/ipc/spool.go:66-68`). A daemon spill failure is therefore completely silent: no `l0_dropped`, no `Loud`. Since `newSpool` is unexported this cannot be fixed from `daemon` today — note it, or (better) it disappears with I-5's deletion.
10. **`registry.go:130 — `Ensure` takes `cfg config.Config`** where the spec says `Ensure(e, now)`. Threading config through a per-event hot-path call is awkward and forces every caller to hold a `config.Config`. *Fix:* store the max-sessions limit on the registry (a `SetMaxSessions(int)` alongside `SetLogger`), matching how the logger is already injected.
11. **`spawn.go:67-70 — `EnsureRunning` returns `spawned = true` when `SpawnDetached` *failed`.** Nothing was spawned; the flag is a lie to the caller. *Fix:* `return false, serr`.
12. **`spawn.go:120-129 — `QOMPACK_FAULT` stripping is case-sensitive.** Windows environment variable names are case-insensitive, so `qompack_fault=…` in the parent survives into the child. Narrow, but the whole point of the strip is that fault injection must never leak. *Fix:* `strings.EqualFold` on the pre-`=` segment.

Also noted, below Minor and needing no action: `sketchset.go:66-80`'s `Read`/`Write` pass `*SketchSet` to the callback, so a re-entrant callback self-deadlocks (worth one doc sentence); `ingest.go:170-176`'s rotation `seq` resets to 0 across daemon restarts, so a restarted daemon reopens a full segment and then rotates into a possibly-existing `wal-<sess>.1.ndjson` (append-only, so no corruption, only interleaving); `seenSet`'s `order = order[1:]` + `append` idiom periodically reallocates a 64Ki-entry array (bounded, but a ring buffer would avoid the churn); `drain.go:246`'s `fs.Size` is the pre-read stat, so a concurrently-appended file records `Size < Offset` (self-correcting on the next pass).

## What is genuinely well done

- **Windows filesystem semantics are handled with real care**, not incidentally: the explicit `f.Close()` before `os.Remove` in `drainFile` with a comment explaining why a `defer` would be wrong (`drain.go:182-184, 244`); the `os.Chmod(0o600)` before unlinking a `CreateNew`-created (and therefore `0o444`) lock file in both `removeLockFiles` and `Release`. Both were found by actually running the tests, per the report's RED→GREEN evidence, and both are the kind of thing that is normally discovered in production.
- **`histName(obs.BudgetID)`** correctly refuses to hardcode `"l0_ingest"`/`"l0_process"`, exactly as the brief required.
- **`TestIngestACKPrecedesProcessing`** proves the right property at the right level given no server is wired yet, and the `done`/`entered` two-phase structure additionally proves the job was *queued* rather than dropped — a weaker test would have passed on a `Accept` that silently discarded.
- **The `Loud`-once latch** (`registry.go:60, 252-257`) is asserted across two over-limit `Ensure` calls, not just one.
- **`TestSketchSetNeverWritesTriedBloom` calls `Write` first**, so the assertion is not vacuously satisfied by the `!dirty` short-circuit — a subtle trap the test author saw.
- Comment quality throughout is unusually high and consistently explains *why*, with §-references. `lock.go:36-40`'s note about `ErrAddrInUse` needing to be wrapped on the daemon side (because `ipc` may not depend on `daemon`) is exactly the kind of cross-task hand-off that normally gets lost.

---

# Adjudication of the implementer's two flagged concerns

### Concern (a) — `lock.go`'s liveness probe assumes a future daemon answers `OpAdminPing` with `OK:true` (Task 5 dependency)

**Split verdict.**

- **In `lock.go` (`lockIsStale` step 2): acceptable forward dependency.** Verified against `internal/ipc/server.go:219-222`: `ACK` is written iff `resp.OK`, so the implementer's reading of the dependency is accurate. But the degradation is genuinely safe: a non-OK `admin.ping` turns step 2 into "no opinion" and the protocol falls through to step 3 (POSIX `kill(0)`) and step 4 (heartbeat mtime, refreshed every 30 s against a 90 s window). A live daemon is still correctly judged alive on both platforms. `probeAlive` also correctly passes a **nil** `SpoolWriter`, so a failed probe takes `client.appendToSpool`'s nil-spool drop path (`internal/ipc/client.go:334-345`) and does **not** inject a spurious `admin.ping` line into the spool that a later `Drain` would replay — an easy trap that was avoided. No action required beyond the note the implementer asks for.
- **In `spawn.go` (`EnsureRunning`): not acceptable — see Important I-3.** Here the same assumption has teeth. A non-OK `admin.ping` does not degrade to a slower-but-correct answer; it produces a redundant `SpawnDetached` plus a full 1500 ms poll on **every hook invocation**, against a 15 ms B-A budget. This needs either an exported raw-dial probe in `internal/ipc` or a guard test in Task 4/5 pinning `admin.ping` → `OK:true`.

### Concern (b) — the drainer can delete a fully-drained `client-<pid>.ndjson` that is the same process's own live ring-full spill target

**Real defect, but the implementer's severity assessment is wrong in both directions — and the right fix is a deletion, not a guard. See Important I-5.**

The report frames this as "future spills via the already-open handle become invisible to later drains", i.e. data loss. It is **not** data loss: `Accept` writes the WAL *before* it attempts the ring enqueue (`ingest.go:117-127` precedes `:134`), so every spilled line is already durable in `wal-<session>.ndjson`, which `Drain` reads *first*. Nothing is lost even if the spool file is orphaned.

What is actually wrong is more interesting and slightly worse than a race:

1. **The spill is redundant by construction.** It is a second copy of a line the WAL already holds. Its only defence against double dispatch is byte-identity of two independently-produced encodings — the WAL's received bytes versus `spool.Append`'s `ipc.EncodeRequest` re-encoding. That identity currently holds, but it is an unpinned, undocumented invariant, and `hookio.Event.Extra` (`json:"-"`) is already a field that does not survive a decode/encode round trip.
2. **The orphaned-inode effect is platform-divergent, not universal.** POSIX unlinks successfully and retains up to `spoolMaxBytes` (64 MiB) of unreachable blocks for the daemon's lifetime; Windows fails the `os.Remove`, logs a WARN, and self-corrects on the next drain via the size check. Divergent behaviour between the two supported platforms is itself a defect.

**Ruling: fix now, in this task, by deleting the spill path** (`clientSpool`, `spill()`, and the spool half of `TestIngestRingFullSpillsToSpool`), keeping the `l0_ring_full` counter. This removes a redundant write from the hot path, removes the duplicate-dispatch surface, removes the platform divergence, and removes the file the drainer would have deleted — all at once. If the controller instead insists on spec-literal retention of the spill, the minimum is a `DrainConfig.SkipDelete` seam so a live spill target is offset-marked like a live WAL, plus a round-trip-stability test on `EncodeRequest(DecodeRequest(x))`. Deferring to Task 4 unchanged is not acceptable: Task 4 would be wiring together two components whose interaction is known-wrong.

---

# Severity counts

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 5 (I-1 blob resolution absent from the hot path; I-2 Windows lock-acquisition race; I-3 `EnsureRunning` liveness criterion; I-4 `drain.json` persistence point; I-5 redundant ring-full spill / drain deletes live spill target) |
| Minor | 12 |

**Verdict: Needs fixes (0 Critical, 5 Important)**

---

# Re-review (fix round 1)

Commit under re-review: `eeca065` — the original `a73a2384` **amended in place**. Re-verified: subject and body still byte-identical to the brief, no attribution trailers, author == committer == `Aidan <aidantran120@gmail.com>`, `git rev-list --count dd2f5e2..eeca065` = **1**, working tree clean. New files in the delta: `internal/ipc/probe.go` + `probe_test.go`, `internal/daemon/blob.go` + `blob_test.go`.

## Verification re-run against `eeca065`

| Command | Result |
|---|---|
| `go vet ./...` | clean |
| `GOOS=linux go vet ./internal/daemon/...` | clean (covers the unix-only build tags) |
| `go test ./internal/daemon/... ./internal/ipc/... -race -count=2` | all `ok` |
| `go test ./internal/ipc/... ./internal/daemon/... -race -count=3` | all `ok` (ipc 33.0s, ipctest 2.8s, daemon 2.3s) |
| `go test ./... -count=1` (full repo) | every package `ok` |
| `go test ./test/guards/... -count=1` | `ok … 25.7s` |
| `GOOS=linux go build ./...` / `GOOS=darwin go build ./...` | both clean |
| `go run ./tools/devtool lint` | `PASS` × 7 — golangci-lint, nomagic, **importgraph**, testdeps, bindeps, **sleepcheck**, stubskips |
| `BenchmarkIngestAccept -benchtime 2000x` | 5155 ns/op (was 6708 ns/op pre-fix — the cached `histBB` removed a per-call allocation) |

`ipc.Probe` lives in `internal/ipc` and imports only `time`; `internal/daemon` still never imports `net` (importgraph + `test/guards/network_test.go` both green).

## Per-finding status

### I-1 — blob resolution absent from the hot path → **FIXED**

`internal/daemon/blob.go` now owns `resolveBlob(root, log, req)` as shared package-level code, called from **both** `ingest.dispatch` (`ingest.go:274`) and `drainer.drainFile` (`drain.go:226`). One place knows the descriptor shape, one place deletes the blob file.

**The seenSet keying — the thing I was asked to look at hardest — is correct.** Both sides key on the line *as received / as read*, before any resolution:

- `Accept` computes `j.key = core.HashBytes(walHashDomain, line)` (`ingest.go:126`) from the raw wire bytes, and `dispatch` calls `SeenOrAdd(j.key)` at `:270` **before** `resolveBlob` at `:274`.
- `drainFile` computes `key` from the file line at `drain.go:221`, calls `SeenOrAdd` at `:222`, and only then calls `resolveBlob` at `:226`.

So a record resolved on the ingest path and the identical still-descriptor bytes drain later reads out of the WAL hash to the same key. Whichever side's `SeenOrAdd` runs first wins; only one ever reaches the handler. There is no resolved/unresolved double-dispatch, and no path where a blob is deleted twice or a second consumer finds it missing — the losing side returns at the dedup check, before it would have touched the file. Had the fix instead keyed on the *post*-resolution request, the two sides would have hashed differently and every externalized record would have double-dispatched; that trap was avoided.

Test coverage is strong: `TestIngestResolvesBlobsEndToEnd` (`ingest_test.go:183`) is a genuine end-to-end — real `ipc.Server`, real `ipc.Client` with `MaxPayloadBytes: 64` to force `externalize()`, a 256-byte `ToolResponse` — and asserts the dispatched event carries the **full** payload, `Raw` is cleared, and no `blob-*` file survives. Plus three focused `TestResolveBlob_*` units.

### I-2 — Windows lock-acquisition race → **FIXED**

`lock.go:145-149` is exactly the fix I recommended: `lastSeen := time.UnixMilli(info.Started)`, overridden by `daemon.hb`'s mtime only when that file exists. The "no heartbeat yet" case can no longer be mistaken for "never reported in".

I re-derived the reworked staleness logic end to end and found no new hole:

- **Fresh lock, no hb (the race window):** `Started` is approximately now, so not stale, so `ErrLockHeld`. Correct, and pinned by the new `TestAcquireLockRaceWindowIsNotStale` (`lock_test.go:96`), which reproduces the window by hand.
- **Old lock, no hb (hard crash with the hb removed):** `Started` hours ago, so stale, so reclaimed. Correct, and only reachable once steps 2 and 3 have already declined.
- **Old lock, fresh hb:** hb wins, alive. Correct.
- **Corrupt or absent `Started` (`0`):** `time.UnixMilli(0)` is 1970, so stale. Correct direction.
- **`os.Stat` fails for a non-NotExist reason:** falls back to `Started`, erring toward "alive". Conservative and correct — the failure mode of this protocol must never be "reclaim a live lock".
- **Clock consistency:** `Started` is written with the injected clock and compared with the same injected clock, so FakeClock tests and production `SystemClock` are each internally consistent.

The test uses `os.Getpid()` as the lock's PID, so on POSIX step 3 decides before step 4 is reached — the new fallback is exercised only on Windows (this host, and the platform the bug existed on). That is acceptable, not a gap worth blocking on: the assertion holds on both platforms, just via different steps.

Minor 5 was folded in correctly: `(*Lock).owned()` (`lock.go:198-201`) gates both `Heartbeat` (errors) and `Release` (silent no-op), pinned by `TestLockRefusesAfterReclaim`. I checked the leaked-`daemon.hb` case this introduces (Release declines while an orphan hb remains) — it self-heals, because any subsequent `AcquireLock` either `removeLockFiles`s both or `touchFile`s the hb fresh. Not a defect. One hand-off note for Task 4, not a fix: `Heartbeat()` now returns a non-nil error meaning "you no longer own the lock", which the 30 s ticker should treat as a shutdown signal rather than log-and-continue.

### I-3 — `EnsureRunning` liveness criterion → **FIXED (Ruling #22 satisfied)**

`internal/ipc/probe.go` exports `Probe(a Addr, timeout time.Duration) bool`: dial, close, return. It writes nothing and reads nothing, so it cannot depend on how any future op-routing table answers any op. Both call sites converted — `lock.go:129` (staleness step 2) and `spawn.go:74, 87` (`EnsureRunning`) — and the old `probeAlive` round-trip helper is **deleted**, not left dead.

`TestProbe_DoesNotWaitForAResponse` is the right test for the ruling: it stands up a server whose handler is `select {}` (hangs forever) and asserts `Probe` still returns `true` in under a second — proving it never gets far enough to invoke a handler. `TestProbe_FalseWhenNothingListens` covers the negative.

Minor 11 folded in: `EnsureRunning` now returns `(false, serr)` when `SpawnDetached` fails. `clk` is retained as an unused parameter to hold the pinned spec signature, with a comment saying so; `GOOS=linux go vet` and golangci-lint are both clean on it.

### I-4 — `drain.json` persisted only at end-of-`Drain` → **FIXED**

`drain.go:248-255`: `fs.Done = true`, then `saveState(st)`, then `shouldDelete`/`os.Remove`. That is the spec's step-4 ordering exactly, and the end-of-`Drain` save is correctly retained for the cancelled/partial-file path (`drain.go:134`).

`TestDrainPersistsPerFileBeforeMovingOn` (`drain_test.go:61`) proves it the hard way rather than by inspection: from inside the `Dispatch` callback for the *second* file's first line, it re-reads `state/drain.json` **off disk** and asserts the first file is already recorded `Done`. A fix that only moved the save one line earlier without actually flushing would fail this.

### I-5 — redundant ring-full spill → **FIXED (Ruling #23 satisfied)**

`ingest.clientSpool`, `spill()`, and the `ipc.NewSpool` construction in `newIngest` are gone. A ring-full request now only increments `l0_ring_full` and is dropped (`ingest.go:128-134`), with a doc comment stating why that is safe — the WAL append two lines earlier already made the line durable. `ingest.Close()` no longer reaches for a spool handle.

This removes all three problems at once: the duplicate-dispatch surface, the unpinned `EncodeRequest(DecodeRequest(x))` byte-identity dependency, and the POSIX/Windows divergence on deleting a file with a live handle. `TestIngestRingFullSpillsToSpool` was rewritten to assert 5 WAL lines, `l0_ring_full == 3`, and — the load-bearing new assertion — that the spool directory contains **exactly one** file, the WAL. (The test name is now a slight misnomer, since nothing spills any more; not worth a rename that would break the spec-row cross-reference.)

## Minors — spot-check

Spot-checked 5 of 12 in the source rather than re-verifying all: **#2** cached `histBB`/`histBC` on the struct at `newIngest` (`ingest.go:72-73, 100-101`) — confirmed, and visible in the benchmark drop; **#8** `func (i *ingest) Wait()` present at `ingest.go:304`; **#10** `Ensure(e, now)` restored to the spec signature exactly (`registry.go:151`) with `SetMaxSessions` (`:94`) alongside `SetLogger`, `NewSessionRegistry` seeding `maxSessions: defaultMaxSessions`, and `evictLocked` keeping its own non-positive guard as belt-and-braces; **#12** `strings.Cut` + `strings.EqualFold` (`spawn.go:137`) with `TestBuildSpawnEnv_StripIsCaseInsensitive`; **#4** `TestDrainSurvivesCorruptLine` now builds `obs.New(clk)` and asserts `counterDrainFileError.Value() == 1` (`drain_test.go:228-236`). The remaining seven are corroborated by the delta's shape and by lint/vet/test being green. The table's claims match the code everywhere I looked — no overclaiming.

## Flakiness adjudications

### (a) The two "environmental flakiness" fixes — **test hygiene, not masked product races**

**`TestIngestRingFullSpillsToSpool`** (dropped `t.Parallel()`, per-call bound became 500 ms total for 5 calls). The non-blocking property is *structural*, not statistical: `select { case i.ring <- j: default: }` (`ingest.go:128-134`) cannot block, and the failure mode of losing the `default` arm is an unbounded hang, not a slow call — which any finite bound catches. So a looser wall-clock bound cannot hide the regression this test exists to catch. Dropping `t.Parallel()` is legitimate for a test that measures elapsed time: leaving it parallel with a suite full of real-socket tests under `-race` is what made it a scheduler-jitter detector rather than an `Accept` detector. Two caveats worth recording, neither blocking: the comment's claim that 500 ms would catch "a spill re-appears and does synchronous I/O" is **overstated** — three small appends cost single-digit milliseconds and would sail through; and the spec row's "< 1 ms on the `FakeClock`" is now further from the letter, though it was never implementable as written (`Accept` does not advance a FakeClock, so a FakeClock assertion here would be vacuous — the wall-clock bound is the honest substitute).

**`TestIngestACKPrecedesProcessing`** (dropped `t.Parallel()`, 2 s channel waits became 10 s). Pure hygiene, and structurally incapable of masking anything: the property under test is the *ordering* of two channel operations, and the timeouts are `t.Fatal` liveness guards, not the assertion. If `Accept` ever blocked on the worker, `done` would never close and the test would fail at 2 s, 10 s, or any other value. Raising the bound to match the repo's own `ipctest.suiteWait` of 10 s is consistent, not permissive.

**`TestEnsureRunning_AlreadyRunningNeverSpawns`** (a third one, in the report's list: dropped `t.Parallel()`, added a `require.Eventually` reachability pre-check). Also hygiene, and arguably the most clearly correct of the three: the flake was the test racing its *own* server's `Serve` goroutine into existence, then blaming `EnsureRunning`'s real, spec-mandated 20 ms dial for not finding it. Establishing the precondition before exercising the behaviour is the right fix; widening the product's 20 ms would have been the wrong one, and was not done.

I re-ran the daemon and ipc packages under `-race -count=2` and `-race -count=3` and saw no flakes.

### (b) The once-observed `internal/ipc/ipctest` transient — **ledger entry for the final review, not a fix now**

The report does **not name** the failing test — that is itself the deficiency here, and the recommendation follows from it.

- **Not reproducible.** I ran `./internal/ipc/... -race -count=2`, then `./internal/ipc/... ./internal/daemon/... -race -count=3`, then the full repo suite and the guards package: `ipctest` was `ok` every time (2.6s / 2.8s).
- **`ipctest` is untouched by this commit** — it is Task 1/2 surface. Speculatively editing it here would put unrelated changes in a commit whose scope is already settled.
- **The plausible culprits are load-sensitive deadline guards, not correctness assertions.** `internal/ipc/ipctest/suite.go:68` sets `suiteWait = 10 * time.Second`, consumed by `requireServeReturns` (`behaviour.go:230-242`, "Serve did not return within %s") and `receiveRequest` (`:343-354`, "the handler was not called within %s"). A 10 s miss under a heavy `-race -count=2` run on a contended Windows host is the same class of artifact as (a), in a package that already has the right convention — it just needs a named sighting before anyone tunes it.

**Recommendation:** record it in the SP-05 ledger for `V2-VERIFY` as "unnamed transient failure in `internal/ipc/ipctest`, observed once under `-race -count=2` on a Windows dev host, not reproduced in 3 subsequent runs; suspected `suiteWait` deadline guard". Fixing it now is out of scope and would be guesswork. **The actionable ask on the implementer is process, not code: a transient must be reported with its test name and failure text, or the sighting carries no information a later investigation can use.**

## New defects introduced by the delta

**None found.** Beyond the two areas I was asked to scrutinise (both clean — see I-1 and I-2 above), I specifically checked: `Probe`'s dial-and-close leaves no server-side goroutine parked (`handleConn` takes a clean EOF and returns); `EnsureRunning` no longer nil-guards `clk` but also never dereferences it; `Accept`'s widened `obs.Timed` closure still returns early on a WAL failure so nothing non-durable is ever enqueued; `resolveBlob` cannot race itself across the ingest and drain paths, because the client mints a unique `blob-<pid>-<n>.bin` per externalization and the dedup set collapses the only case where two consumers could target the same line; `NewSessionRegistry` seeds `maxSessions` so the reverted `Ensure(e, now)` signature cannot regress into a zero limit that evicts everything; `TestDrainPersistsPerFileBeforeMovingOn`'s self-referencing `dr` closure is safe (Drain is single-goroutine, and `-race` agrees).

One piece of **report hygiene**, not code: the report's original "Self-review" items 2, 5 and 7 still describe the pre-fix `probeAlive` round trip, the ring-full spill, and the old `clk` threading — all three superseded by Fix round 1. The "Remaining concerns after fix round 1" section does say so, but a reader of the earlier section alone would be misled. Worth a strikethrough or a forward-pointer before this report is archived.

## Re-review verdict

All five Important findings are genuinely fixed — not papered over — and each landed with a test that would fail if the fix were reverted, which is the standard I was checking against. The two rulings (#22 dial-only `ipc.Probe`, #23 spill deletion) are implemented as directed. The 12/12 minors table is accurate everywhere I spot-checked. Both flakiness fixes are test hygiene. No new defects.

**Verdict: Approved**

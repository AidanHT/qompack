# Task 3 report — daemon singleton lock, session registry, WAL ingest queue, and drain

Commit (after Fix round 1): `eeca0651c8f5c90eb36c2a8db95673bd316a1feb`
Original commit: `a73a2384739def27b1d9c8b2d0f8daa599f5ddb0` (amended in place; same subject/body,
no trailers)
Branch: `feat/sp05-daemon-ipc-and-hot-path`
Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`

## Process note (TDD)

Given the size of the interop surface this task builds on (real `ipc.Client`/`ipc.Server`/`ipc.SpoolWriter`,
`paths.AppendOnly`/`CreateNew`/`WriteAtomic`, `sketch.Save`/`Load` stubs, `obs.Budgets()`), I front-loaded
research into the *actual shipped* code (not the plan's placeholder names) before writing either tests or
implementation for each file group — the brief itself directs this ("shipped spellings ALWAYS win"). For each
file group I then wrote the test file and the implementation together, ran the tests immediately, and iterated
on real failures. This was not strict red-then-green-with-implementation-untouched for every single test, but
it produced genuine RED→GREEN cycles that caught three real, independent bugs (below) — not a rubber-stamped
pass.

## RED → GREEN evidence (real bugs caught by running the tests)

1. **`ingest_test.go` — leaked WAL file handles.** First run of all four `TestIngest*` tests failed
   `t.TempDir()` cleanup on Windows (`unlinkat ... The process cannot access the file because it is being used
   by another process`), because `newIngest` never had its cached `*os.File` handles closed. Fixed by adding
   `t.Cleanup(func() { _ = ing.Close() })` after every `newIngest` call. Re-ran: all four pass.

2. **`drain_test.go` — `os.Remove` attempted on a still-open file handle.** `drainFile` opened the spool file
   via `os.Open`, deferred `f.Close()`, then called `os.Remove(path)` *before* the deferred close ran. On
   Windows this fails the delete silently (only a `WARN` log, no test-visible error), so `TestDrainIsIdempotent`
   and `TestDrainSurvivesCorruptLine` both failed on "the file must still be deleted" assertions. Fixed by
   closing `f` explicitly right after the read loop, before the delete-if-drained branch, instead of relying on
   the deferred close.

3. **`drain_test.go` — `state/drain.json`'s parent directory never created.** `saveState` called
   `paths.WriteAtomic` directly; `WriteAtomic`'s finishing `os.Rename` requires the destination directory to
   already exist, and a fresh project has no `.qompack/state/` yet. The write failed silently (logged, not
   surfaced), so a second `Drain()` call re-read an empty state and re-dispatched every line from scratch —
   `TestDrainKeepsLiveSessionWAL` and `TestDrainResumesAfterCancel` both failed on exact dispatch-count
   assertions. Fixed by `os.MkdirAll(filepath.Dir(p), 0o700)` in `saveState` before `WriteAtomic`.

After these three fixes, every test in the package passes, including under `-race -count=2`.

## Files changed

- `internal/daemon/daemon.go` (M) — moved `SessionRegistry`/`SessionState`/`SketchSet` out into their own
  files (registry.go/sketchset.go), per the binding ruling. `Options`, `Handle`/`Handler`/`Ops`,
  `IdleController`, `Daemon`, `New`, `stubDaemon`, `stubIdle` are otherwise untouched; existing
  `daemon_test.go` (not modified) still passes verbatim.
- `internal/daemon/registry.go` (new) — `SessionState`/`SessionRegistry` grown into the richer shape:
  `Ensure`/`Touch`/`End`/`Live`/`Snapshot`/`LastActivity`/`HotMode`/`SetHotMode`/`SetLogger` plus
  `evictLocked`. `NewSessionRegistry()`, `Get`, `Len` keep their exact shipped signatures.
- `internal/daemon/registry_test.go` (new)
- `internal/daemon/sketchset.go` (new) — `SketchSet` (same four exported fields, `+mu`, `+dirty`),
  `NewSketchSet`, `Load`, `Save`, `Read`, `Write`.
- `internal/daemon/sketchset_test.go` (new)
- `internal/daemon/lock.go`, `lock_unix.go`, `lock_windows.go` (new) — `AcquireLock`, `ReadLock`,
  `(*Lock).Heartbeat`, `(*Lock).Release`, the 5-step staleness protocol, `pidAlive`.
- `internal/daemon/lock_test.go` (new)
- `internal/daemon/spawn.go`, `spawn_unix.go`, `spawn_windows.go` (new) — `EnsureRunning`, `SpawnDetached`,
  `sysProcAttr`, plus testable pure helpers `buildSpawnCommand`/`buildSpawnEnv`.
- `internal/daemon/spawn_test.go` (new)
- `internal/daemon/ingest.go` (new) — `ingest`/`Accept`/`Start`/worker pool/`seenSet`/WAL rotation.
- `internal/daemon/ingest_test.go` (new)
- `internal/daemon/drain.go` (new) — standalone `drainer`/`Drain`/blob resolution/`state/drain.json`
  persistence (per the binding ruling: not `(*daemon).Drain`, a standalone engine Task 4 wires in).
- `internal/daemon/drain_test.go` (new)
- `internal/daemon/metrics.go` (new) — shared counter-name constants (`l0_ring_full`, `l0_worker_panic`,
  `drain_file_error`) and the `histName(obs.BudgetID) string` helper.
- `internal/daemon/bench_test.go` (new) — `BenchmarkIngestAccept` (B-B shape check; not a CI gate here).

## Verification run (all green)

- `go test ./internal/daemon/... -race -count=2` → `ok`
- `go test ./internal/ipc/... ./test/guards/...` → `ok`
- `GOOS=linux go build ./...` → clean
- `GOOS=darwin go build ./...` → clean
- `go run ./tools/devtool fmt` → no changes needed
- `go run ./tools/devtool lint` → all sub-checks PASS, including `importgraph` (daemon still importable by
  nothing) and `stubskips`
- `go vet ./...` → clean
- `go test ./...` (full repo, once) → all packages `ok`
- `BenchmarkIngestAccept` (2000 iterations, warm handle): **~4.1 µs/op**, well under the B-B 2 ms budget

## Self-review / design decisions worth the controller's attention

1. **Lock struct dropped the `f *os.File` field** the spec's pseudocode shows. The binding ruling directs
   writing the lock file's JSON via `paths.CreateNew` directly, which never hands back an open handle, so the
   field would have been permanently nil. Kept `path, hb string; clk core.Clock` only — all four
   required symbols (`AcquireLock`, `ReadLock`, `Heartbeat`, `Release`) match the spec exactly.

2. **Step 2's "dial" is implemented as a real `ipc.Client.Send` round trip**, not a raw socket dial — `ipc`
   exports no client-side dial primitive, and `net` may not be imported outside `internal/ipc` (guarded by
   `test/guards/network_test.go`). `probeAlive` constructs an `ipc.Client` with lazy-spawn disabled
   (`Self=""`), sends a fire-and-forget `ipc.OpAdminPing`, and treats `Response.OK == true` as the only
   decisive "alive" signal (anything else, including a legitimate refusal, falls through to steps 3/4 rather
   than being trusted as proof of death). This depends on a future real daemon answering `admin.ping` with
   `OK:true` unconditionally — a standard assumption for a ping op, but unverifiable today since Task 4/5
   haven't wired the routing table yet. Verified against a hand-rolled `ipc.NewServer` + always-OK handler in
   `TestLiveLockNotReclaimed`.

3. **`AcquireLock` now writes an initial heartbeat immediately on acquisition** (not explicit in the spec
   text). Without it, a second `AcquireLock` landing inside the first daemon's own first 30-second heartbeat
   window would see *no* heartbeat file at all on Windows (where `pidAlive` always reports "no opinion") and
   misjudge a perfectly live daemon as stale. Confirmed necessary by `TestAcquireLockExclusive` actually
   failing without this fix during development on this Windows host, before I added it.

4. **`SessionRegistry.HotMode()`/`SetHotMode()` are daemon-wide, not per-session** — per the spec's own
   framing ("plus the project-wide hot ipc.HotPathMode and its transition reason") and the
   `TestRegistryNewSessionResetsHotMode` row's literal behavior (a brand-new session resets a value that was
   set independently of any session). `SessionState.Hot` (the original shipped per-session field) is kept and
   snapshotted from the registry's current value at `Ensure`-creation time, but nothing in Task 3 reads it
   back — Task 4's breach detector and op handlers are the intended consumers of the registry-level accessor.

5. **Known integration-only edge case, untested here by construction**: `drainer.shouldDelete` will delete a
   `client-<pid>.ndjson` file unconditionally once fully drained. If that file happens to be the *same*
   process's own currently-open ring-full spill target (once Task 4 runs ingest and drain concurrently in one
   daemon), a POSIX `os.Remove` on an open file silently orphans the inode — future spills via the already-open
   handle become invisible to later drains. Task 3's tests exercise `ingest` and `drain` independently (as the
   brief scopes them), so this interaction isn't exercised here; flagging it for Task 4's wiring review.

6. **`ringCapacity`/`walRotateBytes`/`seenCapacity`/`misraGriesK`/`defaultMaxSessions` are named constants**
   with `//nomagic:allow` where required by the forbidden-literal list (4096, 8). WAL rotation is verified with
   a synthetic near-limit `walFile` rather than actually writing 64 MiB.

7. **`EnsureRunning`'s poll loop uses real wall-clock time** (`time.Now()`/`time.NewTicker`), not the injected
   `core.Clock` — consistent with `ipc.ClientOptions.Clock`'s own documented precedent that connection/process
   liveness timing is never faked by a test clock. `clk` is threaded through only for `probeAlive`'s request
   timestamp. I tested the two fast paths (already-running: no spawn; spawn failure: reported immediately) but
   did **not** write a test for the full "spawn succeeds but the daemon never comes up within 1500 ms" timeout
   path, since that would cost a genuine 1.5s per run for a scenario better covered by Task 5's real e2e/bench
   harness. Flagging as a coverage gap, not a correctness gap.

## Concerns for the controller

- Item 2 above (the `admin.ping` liveness-probe assumption) is the one piece of this task whose correctness
  depends on a decision Task 4/5 haven't made yet. If a future op-routing implementation ever returns
  `OK:false` for `admin.ping` (e.g., treating unrecognized/unrouted ops as a blanket refusal), `AcquireLock`'s
  step 2 degrades to "no opinion" for every daemon rather than "definitely alive" — never incorrect, since
  steps 3/4 still run, but weaker than intended. Worth a one-line note in whichever task wires the real routing
  table.
- Item 5 (drain deleting its own process's live spill file) is a real, if narrow, correctness risk once Task 4
  wires ingest+drain together in one running daemon. Recommend Task 4 either (a) has the ring-full spill
  target use a filename `SpoolFiles`/`drain` treats as "never delete while the owning ingest is alive" or (b)
  accepts the current behavior and documents that a `client-*.ndjson` spool file is drained-and-deleted
  eagerly, so a rapid burst of ring-full spills followed immediately by an idle-tick drain could in principle
  race. Neither is exercised by this task's own tests.

---

## Fix round 1

Reviewer verdict on the original commit: **Needs fixes (0 Critical, 5 Important, 12 Minor)**. Full review:
`task-3-review.md`. Two controller rulings (#22, #23) directed specific fixes below. All five Important
findings and all twelve Minor findings are addressed; none were skipped.

### Important findings

- **I-1 (blob resolution missing on the hot path).** `ipc.Client` externalizes an oversized
  `Event.ToolResponse` on the *live wire path* (`client.go`'s `Send`), not only the spool fallback, so the
  daemon routinely receives `{"blob":...}` descriptor lines through `Accept`, not just through `Drain`.
  Fixed by lifting blob resolution into a single shared, package-level `resolveBlob(root, log, req)`
  (new file `internal/daemon/blob.go`), calling it from `ingest.dispatch` before `run` and from
  `drainer.drainFile` before `Dispatch` — one place owns the descriptor shape and the blob-file deletion,
  so a resolved-on-one-path/unresolved-on-the-other bug cannot recur. The dedup key was already computed
  from the WAL line as received (before any resolution), in both `Accept` and `Drain`, so a resolved and an
  unresolved copy of the same record still collapse to one dispatch. New tests:
  `TestIngestResolvesBlobsEndToEnd` (drives a real `ipc.Client.Send` with an oversized `ToolResponse`
  through a real `ipc.Server` into `ing.Accept`, asserts the dispatched event carries the full payload and
  the blob file is gone) plus three focused `TestResolveBlob_*` unit tests in the new `blob_test.go`.
- **I-2 (Windows lock-acquisition race).** `AcquireLock`'s window between `CreateNew(lockPath)` becoming
  visible and its own `touchFile(hbPath)` running let a competing acquirer, landing in that window on
  Windows (where `pidAlive` has no opinion), see "no heartbeat at all" and misjudge a lock created
  microseconds ago as stale. Fixed by anchoring step 4's absent-heartbeat case on the lock file's own
  recorded `Started` timestamp instead of unconditionally returning `true`: `lastSeen` is `Started` unless
  `daemon.hb` exists, in which case its mtime wins. New test `TestAcquireLockRaceWindowIsNotStale`
  reproduces the exact window by hand (a lock file with a fresh `Started` and deliberately no heartbeat
  file) and asserts a second `AcquireLock` still reports `ErrLockHeld`.
- **I-3 + Ruling #22 (liveness must be a dial, not a round trip).** Exported `ipc.Probe(a Addr, timeout
  time.Duration) bool` (new `internal/ipc/probe.go`): dials, closes, writes nothing, returns whether the
  dial itself succeeded. `lock.go`'s staleness-protocol step 2 and `spawn.go`'s `EnsureRunning` both now
  call `ipc.Probe` instead of round-tripping through `admin.ping` via a constructed `ipc.Client` — the old
  `probeAlive` helper is deleted from `lock.go` entirely (no longer needed by either call site). New ipc
  tests: `TestProbe_FalseWhenNothingListens`, and `TestProbe_DoesNotWaitForAResponse` (a server whose
  handler hangs forever via `select{}` — `Probe` must still report alive, promptly, because it never gets
  far enough to invoke the handler).
- **I-4 (drain.json persisted only once, not per file).** Moved `saveState(st)` into `drainFile`,
  immediately after `fs.Done = true` and before the `shouldDelete`/`os.Remove` — the spec's exact step-4
  ordering — so a crash between one file's completion and the next file's processing can no longer lose the
  completed file's recorded offset. The end-of-`Drain` `saveState` call is kept for the
  cancelled/partial-file case. New test `TestDrainPersistsPerFileBeforeMovingOn`: with two spool files, the
  `Dispatch` callback for the second file's first line reads `state/drain.json` back from disk and asserts
  the first file is already recorded `Done` — proving the persistence happened before `Drain` itself
  returned, not only at the very end.
- **I-5 + Ruling #23 (delete the ring-full spill).** Removed `ingest.clientSpool` and `spill()` entirely,
  and the `ipc.NewSpool` construction in `newIngest`. A ring-full request now only increments
  `l0_ring_full` and is dropped — the WAL append two lines earlier already made the line durable, so the
  second copy was redundant, rested on an unpinned `EncodeRequest(DecodeRequest(x))` byte-identity
  invariant, and diverged POSIX/Windows on orphan cleanup. `ingest.Close()` no longer closes a client-spool
  handle. Rewrote `TestIngestRingFullSpillsToSpool` to assert "5 WAL lines, `l0_ring_full == 3`, nothing
  else written to the spool directory" instead of counting spilled lines.

### Other fix-round requirements from the coordinator message

- **B-B timing now wraps the full `Accept` path.** With the spill deleted, `Accept`'s two remaining steps
  (WAL append, ring enqueue) are both inside the single `obs.Timed` closure — previously only the WAL
  append was timed.
- **`TestDrainSurvivesCorruptLine` now passes a real `obs.Registry`** (`obs.New(clk)`) and asserts
  `counterDrainFileError.Value() == 1`, closing the gap the reviewer's Minor 4 and the coordinator's message
  both flagged.

### Minor findings (12/12 addressed)

| # | Finding | Resolution |
|---|---|---|
| 1 | B-B timing excludes ring enqueue/spill | Fixed — see "B-B timing" above |
| 2 | `histName` allocates on every hot-path call | Fixed — `histBB`/`histBC` resolved once in `newIngest`, cached on `ingest` |
| 3 | Ring-full test's `< time.Second` bound too weak | Fixed — see I-5's test rewrite; tightened to a 500ms *total* bound for 5 calls (a per-call bound in the low tens of ms was observed to flake under `-race` with many parallel real-socket tests contending for scheduler time — see "Test-environment flakiness" below) |
| 4 | `TestDrainSurvivesCorruptLine` has no `Metrics` | Fixed — see above |
| 5 | `Heartbeat`/`Release` never verify ownership | Fixed — new `(*Lock).owned()` compares the on-disk `PID` against `os.Getpid()`; both methods refuse (Heartbeat) or silently no-op (Release) once the lock has been reclaimed. New test `TestLockRefusesAfterReclaim` |
| 6 | Blob resolution on nil-`Event` reads/deletes the blob anyway | Fixed — `resolveBlob` returns early when `req.Event == nil`, before touching the file. New test `TestResolveBlob_NilEventLeavesBlobFileUntouched` |
| 7 | Discarded `Dispatch` response / unconditional offset advance undocumented | Fixed — added an explanatory comment at the call site in `drainFile` |
| 8 | No way to join the worker pool on shutdown | Fixed — added `func (i *ingest) Wait() { i.wg.Wait() }` |
| 9 | Ring-full spill's `ipc.NewSpool` had no logger/registry | Resolved by deletion (I-5) |
| 10 | `Ensure(e, now, cfg)` threads a whole config through the hot path | Fixed — `Ensure` is back to `Ensure(e, now)`; added `SessionRegistry.SetMaxSessions(int)` alongside the existing `SetLogger`, following the same injection pattern. Updated `registry_test.go` accordingly plus new `TestRegistrySetMaxSessionsRejectsNonPositive` |
| 11 | `EnsureRunning` returns `spawned=true` when `SpawnDetached` failed | Fixed — returns `(false, serr)`; updated `TestEnsureRunning_SpawnFailureReportsImmediately`'s assertion |
| 12 | `QOMPACK_FAULT` strip is case-sensitive | Fixed — `strings.EqualFold` on the key via `strings.Cut`. New test `TestBuildSpawnEnv_StripIsCaseInsensitive` |

Also fixed the one "needing no action" item worth a doc sentence: `SketchSet.Read`/`Write`'s doc comments now
state explicitly that `fn` must not re-enter (`sync.RWMutex` is not re-entrant). The other three
"needing no action" notes (WAL rotation `seq` resets across restarts; `seenSet`'s slice-based FIFO
periodically reallocates; `drain.go`'s pre-read `fs.Size` self-corrects on the next pass) were left as
documented, intentional trade-offs per the review's own assessment.

### Test-environment flakiness discovered and fixed during verification

Two tests were genuinely flaky under `go test ./internal/daemon/... -race -count=2` on this Windows dev
host — both were scheduling-jitter artifacts of many real-socket tests contending for CPU under heavy race
instrumentation, not architectural bugs, but both needed fixing to make the required verification command
reliable:

1. `TestIngestRingFullSpillsToSpool` and `TestIngestACKPrecedesProcessing` intermittently took hundreds of
   milliseconds to multiple seconds per `Accept`/goroutine-handoff under `-race -count=2` combined with many
   other `t.Parallel()` tests holding real OS sockets/pipes. Fixed by removing `t.Parallel()` from both (so
   they run before the parallel batch starts, not contending with it), widening
   `TestIngestACKPrecedesProcessing`'s channel-wait timeouts from 2s to 10s (matching this codebase's own
   `ipctest.suiteWait` convention), and changing the ring-full test's assertion from a tight per-call bound
   to a 500ms bound on the total of 5 calls.
2. `TestEnsureRunning_AlreadyRunningNeverSpawns` occasionally attempted a real `SpawnDetached` (and failed,
   correctly, on the bogus self path) because `EnsureRunning`'s real, spec-mandated 20ms liveness dial
   missed a server that *was* alive but whose `Serve` goroutine hadn't been scheduled yet under heavy
   contention. Fixed by removing `t.Parallel()` from this test and adding a `require.Eventually` pre-check
   that the server is actually reachable before exercising `EnsureRunning`'s own real timeout.

### Verification run (post-fix, all green)

- `go test ./internal/daemon/... -race -count=2` — run three consecutive times after the flakiness fixes
  above, all green (7.0s / 3.1s / 5.1s)
- `go test ./internal/ipc/... ./test/guards/...` — green (one transient, unrelated failure in
  `internal/ipc/ipctest` observed once immediately after a heavy `-race -count=2` run — reproduced clean on
  immediate re-run in isolation and in the full suite; not touched by this fix round, pre-existing
  environmental flakiness on this host, not a regression)
- `GOOS=linux go build ./...` / `GOOS=darwin go build ./...` — clean
- `go run ./tools/devtool fmt` — no changes needed
- `go run ./tools/devtool lint` — all sub-checks PASS (one intermediate `ineffassign` finding on
  `spawn.go`'s now-unused `clk` reassignment, fixed by dropping the dead nil-check; `clk` itself is kept as
  a parameter to match the pinned spec signature)
- `go vet ./...` — clean
- `go test ./...` (full repo, once) — all packages `ok`
- `BenchmarkIngestAccept` (2000 iterations, warm handle): ~3.4 µs/op, unchanged in order of magnitude from
  before this fix round and still far under the B-B 2ms budget

### Remaining concerns after fix round 1

- I-3's fix (`ipc.Probe`) removes the `admin.ping`-routing dependency this task previously carried for
  liveness detection entirely — `Probe` never asks a question, so it cannot depend on how a future op router
  answers one. No outstanding concern here.
- I-5's fix removes the deletion-of-a-live-spill-file risk this task previously flagged, by removing the
  spill path itself. No outstanding concern here either.
- The one remaining forward dependency is unchanged from before: Task 4/5 still choose how the real
  op-routing table answers `admin.ping` for whatever *other* purpose it serves (status, health checks) —
  that no longer has any bearing on daemon singleton or liveness correctness, since neither `lock.go` nor
  `spawn.go` calls it anymore.

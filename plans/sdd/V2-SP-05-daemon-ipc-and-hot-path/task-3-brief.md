# Task 3 — Commit 3: daemon singleton lock, detached spawn, session registry, WAL ingest queue, drain, sketch set

You are extending `internal/daemon` in `github.com/qompack/qompack` (worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`). Tasks 1–2 (already committed) delivered the full `internal/ipc` transport: frame/LineReader, op helpers, state record, real client/spool/server, `SpoolFiles`, dial/listen. **Read the actual internal/ipc code first — build against what it ships.**

Read IN ORDER:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md` — SP-01 inventory; shipped spellings win.
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md` — mission, out-of-scope, global rules.
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-3-spec.md` — verbatim requirements: lock.go, spawn.go, registry.go, ingest.go, drain.go, sketchset.go specs; the daemon test table (your commit covers the lock/registry/ingest/drain rows — the options/idle/budget/handlers rows are Task 4's); the commit-3 checklist.
4. Existing source: `internal/daemon/daemon.go` (+ daemon_test.go, doc.go), `internal/ipc/`, `internal/sketch/` (constructors + Save/Load are ErrNotImplemented stubs — expected), `internal/config/` (sketch + runtime fields), `internal/paths/`, `internal/testutil/` (FakeClock etc.).

## Binding rulings (controller decisions amending the plan text)

- The shipped `daemon.go` stays the package's seam file for now; Task 4 replaces the stub daemon. Task 3 adds the building blocks in their own files and may extend the shipped `SessionRegistry`/`SessionState` IN PLACE (registry.go: move/grow them into the plan's richer shape — keep the shipped `Get`/`Len` methods working or update their few callers/tests; SP-05 owns this package). Keep the shipped `SketchSet` field set INCLUDING `Top *sketch.MisraGries` (the plan omits Top — keep it) and add `NewSketchSet(cfg)`, `Load`, `Save`, `Read`, `Write` with an internal mutex per the sketchset spec. If converting SketchSet's direct field access to accessor discipline breaks daemon_test.go expectations, adapt the tests minimally.
- `paths` reality: `WriteAtomic(p, b, perm)`; `AppendOnly(p) (io.WriteCloser, error)`; `CreateNew(p string, b []byte) error` (creates AND writes — the lock file's JSON goes in via CreateNew directly); `paths.Long(p)` for raw os calls on Windows; `paths.Layout`/`Of(root)` for directory names — use Layout fields rather than hand-joining ".qompack/..." where a field exists (check layout.go; spool dir, run dir, state dir, logs dir).
- obs idiom: `Counter.Add(1)`, underscore metric names. Use: `l0_ring_full`, `l0_worker_panic`, `drain_file_error`, `l0_dropped`, `l0_externalized` (client already uses the last two — reuse the same strings; put shared metric-name constants in ONE place in the daemon package if you need them in several files). Histogram names come from `obs.Budgets()`: find the B-B budget's `Hist` field ("l0_ingest") via `obs.Budgets()` lookup, and B-C's ("l0_process") — never hardcode those two strings; write a small helper `histName(id obs.BudgetID) string`.
- Clock discipline: every component takes `core.Clock`; tickers via `time.NewTicker` in selects; NO `time.Sleep` (devtool lint greps). `EnsureRunning`'s 25ms poll uses a ticker; its 20ms dial deadlines and 1500ms bound are named constants with §-comments.
- Lock staleness: exactly the 5-step protocol in the spec (parse → dial 20ms → POSIX kill(0) via `lock_unix.go`/`lock_windows.go` `pidAlive` split → heartbeat mtime vs `staleAfter = 90*time.Second` → remove+retry once). Heartbeat file `.qompack/run/daemon.hb`, 30s ticker owned by the daemon (Task 4) — Task 3 ships `Heartbeat()` + `Release()` methods and unit tests with FakeClock where possible (mtime checks need real files; use os.Chtimes to backdate).
- Spawn: `SpawnDetached(projectRoot, self string) error` with platform SysProcAttr files exactly per spec (local const CREATE_NO_WINDOW / DETACHED_PROCESS on windows; Setsid on unix), env = os.Environ() + QOMPACK_PROJECT_ROOT, with any QOMPACK_FAULT entry stripped; Stdin/Stdout/Stderr → os.DevNull handles; Process.Release(). `EnsureRunning(projectRoot, self, log, clk)` per spec. os/exec is permitted in internal/daemon (import rules allow it).
- Ingest: `Accept(req ipc.Request, line []byte) error` = WAL append of the EXACT received bytes (per-session `spool/wal-<session>.ndjson` via paths.AppendOnly, cached handle, rotate past `walRotateBytes = 64<<20`), timed end-to-end into the B-B histogram; then non-blocking ring enqueue (`ringCapacity = 4096` — annotate `//nomagic:allow` exactly as the spec's snippet shows since 4096 collides with a forbidden literal); ring-full → spill the line to the client-spool directory (append to a daemon-owned `client-<pid>.ndjson` style file or reuse ipc.NewSpool — your choice, but drained files must be picked up by SpoolFiles) and return without blocking. Worker pool `max(2, runtime.NumCPU()/2)`, panics recovered → `l0_worker_panic` + Loud, handler timing into B-C's histogram. `seenSet`: capacity-bounded 65536-entry FIFO of `core.HashBytes("qompack.wal.v1", line)` keys.
- Drain: per the spec — wal-* first then client-* (SpoolFiles already orders), `state/drain.json` offsets via WriteAtomic, blob resolution (read blob file → restore field → delete blob), synchronous dispatch through a supplied handler func with 5s per-line deadline, ctx honoured between lines, live-session WALs not deleted (offset-marked). Drain in this task is a standalone engine (e.g. `type drainer` or functions taking explicit deps + a `func(ctx, ipc.Request)` dispatch); Task 4's Daemon wires it into `Daemon.Drain`.
- Blob field restoration: the externalized shape is `{"blob":"<name>","bytes":<n>,"field":"e.tool_response"}` in Request.Raw with Event.ToolResponse nulled (see Task 2's client code). Drain restores Event.ToolResponse from the blob bytes and clears Raw.
- The plan's `TestIngestACKPrecedesProcessing` needs the server+ingest wired — implement it as a unit test proving `Accept` returns before any worker dispatch happens (worker blocked on a channel), which is the property the ACK ordering rests on.
- Registry: plan shape (Ensure/Touch/End/Live/Snapshot/LastActivity/HotMode/SetHotMode + eviction rules + new-session HotSync reset, all per spec). Eviction over `cfg.Runtime.Daemon.MaxSessions`; all-live overflow keeps all + one Loud.

## Process

- TDD per file group: failing tests first (RED evidence), implement to green. Use `testutil.FakeClock` for time-dependent logic.
- Windows host: unix-only bits (`lock_unix.go` kill probe, spawn_unix Setsid) compile-check with `GOOS=linux go build ./...` and `GOOS=darwin go build ./...`; give their tests `//go:build !windows` tags.
- Before committing: `go test ./internal/daemon/... -race`, cross-GOOS builds, devtool fmt/lint/vet (import-graph check must stay green: nothing imports daemon), full `go test ./...` once.
- Exactly ONE commit, exact message below, no attribution trailers:

```
feat(daemon): singleton lock, session registry, WAL ingest queue, and drain

The WAL is the durability boundary: the ACK is written after the append returns and
before any indexing work, so a daemon crash costs freshness and never data. Drain is
idempotent and resumable because it records consumed offsets, which is what lets the
idle tick pick up whatever the client spooled while we were down.

Refs: SP-05, §8.1, 00-ARCHITECTURE §2.4
```

## Report

Full report → `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-3-report.md` (TDD evidence, files changed, self-review, concerns). Reply with ONLY: Status, commit SHA+subject, one-line test summary, concerns, report path.

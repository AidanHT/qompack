# Task 2 — Commit 2: ipc thin client with spool fallback, spawn seam, and the server

You are extending `internal/ipc` in the Go module `github.com/qompack/qompack` (worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`). Task 1 (already committed) added frame.go (EncodeRequest/DecodeRequest/EncodeResponse/DecodeResponse/LineReader/ErrLineTooLong), op.go helpers (admin op constants, KnownOps/Valid/HotPath), state.go (32-byte state record: State/StatePath/ReadState/WriteState/RemoveState/StateFromConfig), exported ProjectHash12/ProjectHash8, ErrAddrTooLong, QOMPACK_IPC_ADDR override, and contract.ParseMode + Mode.MayAct/MayRecord. **Read the actual Task-1 code first and build against what it shipped, not against assumptions.**

Read IN ORDER:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md` — SP-01 inventory; shipped spellings win.
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md` — mission, out-of-scope, global rules.
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-2-spec.md` — your verbatim requirements: the spool.go / listen / client.go / server.go implementation specs (including the exact Send algorithm steps 1–8), the client/spool/server test table, and the commit-2 checklist. Exact values live there.
4. Existing source: all of `internal/ipc/` (including `ipctest/suite.go` + `suite_test.go` — the conformance suites you must satisfy), `internal/logging/`, `internal/obs/registry.go`, `internal/paths/appendonly.go`.

## Binding rulings (controller decisions amending the plan text)

- go-winio is ALREADY in go.mod, and `dial(a Addr, timeout time.Duration) (net.Conn, error)` is ALREADY shipped real for both platforms (dial_windows.go / dial_other.go). Reuse it; if the client algorithm's context-based connect deadline needs a ctx-aware dial, adapt minimally (e.g. compute a timeout from the deadline) rather than rewriting the shipped files' rationale comments. The commit-2 checklist line "Add go-winio to go.mod" is void.
- Shipped spellings win: `MaxLineBytes`, `NamedPipe`/`UnixSocket`, `Client`/`SpoolWriter`/`Server`/`Handler` interfaces as shipped in client.go. `NewClient(addr, spool, log, m)` keeps its shipped 4-arg signature and becomes `NewClientWithOptions(addr, spool, log, m, ClientOptions{})` with defaults from `config.Defaults()`.
- **No ipc.Router** (routing lives in the daemon). The Server takes a plain `Handler`; unknown-op handling and panic containment are asserted at THIS layer only as: a Handler panic must be contained per the server spec in task-2-spec.md (server writes NAK, keeps serving, increments `ipc.route.panic`-style counter — spell the counter `ipc.handler_panic` to match obs's underscore idiom).
- Counter names: obs uses underscore idiom and `Counter.Add(1)` (no Inc). Use `l0_spooled`, `l0_dropped`, `l0_externalized`, `ipc_decode_error`, `ipc_handler_panic`. (The plan's dotted names are respelled to the repo idiom; keep them consistent because later tasks assert them — they are listed here so Task 3+ uses the same strings.)
- `ExternalizeThreshold(cfg config.Config) int` = min(cfg.Runtime.HotPath.MaxPayloadBytes, MaxLineBytes).
- ipctest suites: wire ALL FOUR factories (`RunClientSuite`, `RunSpoolSuite`, `RunServerSuite`, `RunTransportSuite`) to the real implementations in `internal/ipc/ipctest/suite_test.go` (or wherever the suite runner file sits — follow its existing structure). After this commit `go test ./internal/ipc/... -race` must show ZERO Rule W-1 skips. Your `NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error)` defines the server constructor shape — match what RunServerSuite/RunTransportSuite factories need.
- `paths.AppendOnly(p) (io.WriteCloser, error)` — the spool keeps that handle; the spool's `Close()` is on the concrete type (SpoolWriter interface stays as shipped, no Close method), tolerating double-close via sync.Once.
- The stubs guard (test/guards) calls every method of `ipc.NewClient(ipc.Addr{}, nil, logging.Nop(), obs.New(...))` with zero-valued args and requires NO PANIC and (nil error | ErrNotImplemented). Your real client with a zero Addr, nil spool, nil log, background ctx, zero deadline must therefore return (Response{OK:false}, nil) without panicking — nil spool means "spool unavailable": take the drop path (count if metrics non-nil, Loud once if log non-nil). Nil log/metrics are tolerated everywhere (substitute logging.Nop() / skip).
- Client instrumentation: the client itself observes no latency histograms (plan: measuring on the hot path costs more than it reveals). It does count spooled/dropped/externalized events.
- `lazySpawn` per the spec: guarded by `<root>/.qompack/run/spawn.lock` via `paths.CreateNew(p, b)` (NOTE the shipped 2-arg signature writes content), 10s staleness, at most one attempt per process, only when Self != "" and Spawn != nil. ClientOptions.Spawn is a plain func field — ipc must NOT import daemon.
- ClientOptions fields per the plan's Produces section (see plan-interfaces.md §Produces if needed — path: `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-interfaces.md`): ProjectRoot, State, Self, Spawn, ConnectDeadline, AckDeadline, MaxLine, Clock. Zero values fall back to State's deadlines / MaxLineBytes / core.SystemClock().
- Spool cap constant `spoolMaxBytes = 64 << 20` with a //nomagic:allow-style §-comment if the nomagic pass flags it (check how shipped code annotates; 64<<20 may not be a forbidden literal — only annotate if needed).
- Blob externalization per spec: `<spoolDir>/blob-<pid>-<n>.bin`, request's Raw replaced by `{"blob":"...","bytes":n,"field":"e.tool_response"}`, Event.ToolResponse nulled. Client-side only in this task (daemon resolves in Task 3).

## Windows host caveats

- POSIX listen paths (0700/0600 modes, stale-socket unlink probe) go in `listen_unix.go` (`//go:build !windows`); Windows named-pipe listen with the per-user SID SDDL (`D:P(A;;GA;;;<SID>)`, fallback `D:P(A;;GA;;;CO)` + Loud once) in `listen_windows.go`; both ≤ ~60 lines, no protocol logic. You can only RUN the Windows side locally; compile-check the rest via `GOOS=linux go build ./...` and `GOOS=darwin go build ./...`. Write the unix-only tests with `//go:build !windows` tags so CI runs them.
- In-process client/server tests on Windows use the named pipe (fast); tests that need a "listener that hangs" etc. can use a raw net.Listener via the test's own pipe/socket — keep `net` imports inside internal/ipc package tests (import discipline: net only inside internal/ipc, and only "unix" for sockets; winio for pipes).

## Deliverables

- `internal/ipc/spool.go`, `internal/ipc/client.go` (replace the stub bodies; keep/move the stub types out), `internal/ipc/server.go`, `internal/ipc/listen_windows.go`, `internal/ipc/listen_unix.go`.
- Tests: every row of the client/spool/server test table in task-2-spec.md (adapted to shipped spellings), including the rapid property test `TestSendNeverReturnsError` (200 random requests × 4 listener behaviors) and `BenchmarkServerRoundTrip`. `TestWindowsPipeACLRejectsOtherUser` runs only under `QOMPACK_TEST_ACL=1` per spec; assert the SDDL string shape unconditionally where cheap.
- ipctest wired, zero skips.
- Accept-loop backoff uses a ticker/timer inside select — `time.Sleep` is BANNED (devtool lint greps for it).

## Process

- TDD: failing tests first (record RED evidence), then implement to green.
- Before committing: `go test ./internal/ipc/... -race -count=2`, `GOOS=linux go build ./...`, `GOOS=darwin go build ./...`, devtool fmt/lint/vet tasks, then full `go test ./...` once.
- Exactly ONE commit, exact message below, no attribution trailers:

```
feat(ipc): thin client with spool fallback, spawn seam, and the server

Send never returns an error a hook could propagate: every failure path ends in a
spool append and exit 0, because a hook that fails takes observability with it.
The 1-byte ACK converts "usually delivered" into "provably enqueued"; NAK is the
in-band hint that carries the spool submode back to an in-flight client.

Refs: SP-05, §7.1, §12, 00-ARCHITECTURE §2.4, D4
```

## Report

Full report → `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-2-report.md` (TDD RED/GREEN evidence, files changed, self-review, concerns). Reply with ONLY: Status, commit SHA+subject, one-line test summary, concerns, report path.

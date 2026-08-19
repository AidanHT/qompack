# Task 2 report — ipc thin client, spool fallback, spawn seam, and the server

Commit (after Fix round 1): `e4dbd3b9266d844d9b60b7866ee6e36b785eae31` (amended from
`13c448f9d72ed49aec25c1d971c4570dd9cac394`)
Subject: `feat(ipc): thin client with spool fallback, spawn seam, and the server`

## Summary

Replaced SP-01's stub `Client`/`SpoolWriter` with the real hot-path implementations, added
`Server`, and added the two build-tagged `listen()` primitives. All four `ipctest` conformance
suites (`RunClientSuite`, `RunSpoolWriterSuite`, `RunServerSuite`, `RunTransportSuite`) now run
their full behaviour blocks against real implementations with zero Rule W-1 skips (the six
remaining W-1 skips in `internal/ipc/ipctest` are the suite's own permanently-fake local stub
types — `fakeStubClient`/`fakeStubServer`/etc. — that exist specifically to prove the skip
mechanism itself still works; they are unrelated to `ipc`'s real `NewClient`/`NewServer`).

## Files changed

- `internal/ipc/client.go` — replaced stub bodies; added `ClientOptions`, `NewClientWithOptions`,
  the real `client` struct, `Send`'s 8-step algorithm (`awaitACK`/`awaitReply`/`connect`),
  `spoolAndReturn`/`appendToSpool`, `externalize`/`writeBlob` (blob externalization),
  `lazySpawn`/`spawnLockIsStale`/`removeSpawnLock`, and `Close`. Kept `Client`/`SpoolWriter`/
  `Server`/`Handler` type declarations in place (unchanged shapes).
- `internal/ipc/spool.go` (new) — real `spool` type: `NewSpool`/`newSpool` (log/metrics-wired
  internal constructor), `Append` (size-refusal vs. sticky write-failure paths), `Close`,
  `ExternalizeThreshold`, `SpoolFiles`, `ErrSpoolFull`.
- `internal/ipc/server.go` (new) — real `server` type: `NewServer`, `Serve` (accept loop with
  bounded-backoff retry via `context.AfterFunc`+timer, never `time.Sleep`), `handleConn`
  (multiplexed NDJSON reads, ACK/NAK/Reply dispatch), `dispatch` (panic containment), `Close`
  (idempotent via `sync.Once`, POSIX socket unlink).
- `internal/ipc/listen_unix.go` / `listen_windows.go` (new) — `listen(a Addr, log logging.Logger,
  maxLine int) (net.Listener, error)`. Deviates from task-2-spec.md's 2-arg sketch by adding a
  `log` parameter (see Concerns).
- `internal/ipc/op.go` — the deferred Task-1 fix: `Op.Valid()` now does a map lookup against a
  package-level precomputed set instead of re-sorting `KnownOps()` on every call.
- `internal/ipc/ipctest/suite_test.go` — wired `newQompackServer`/`newQompackTransport` factories
  and two new test functions (`TestRunServerSuite_AgainstQompackServer`,
  `TestRunTransportSuite_AgainstQompackServer`); added `Close`-on-cleanup to the existing
  client/spool factories (needed once the client/spool became real — an open spool file handle
  blocks `t.TempDir()`'s cleanup on Windows).
- `internal/ipc/stub_test.go` — deleted; it asserted the now-false "every operation reports
  `ErrNotImplemented`" stub contract. Its still-valid assertions (nil-dependency tolerance,
  "constructing touches nothing") were folded into `client_test.go`/`spool_test.go`.
- New test files: `client_test.go`, `spool_test.go`, `server_test.go`, `server_unix_test.go`
  (`//go:build !windows`), `server_windows_test.go` (`//go:build windows`).

## TDD evidence

Given the scope (client/spool/server/listen are all new, interdependent files), I wrote each
file's tests immediately alongside its implementation rather than one global RED pass, but did
capture RED before every GREEN:

**RED — replacing the stub broke the stub-contract tests (expected regression, proves the real
implementation is no longer a stub):**
```
$ go test ./internal/ipc/... -run . -v
--- FAIL: TestNewClient_ConstructsAndEveryOperationIsNotImplemented
    Error: Target error should be in err chain: expected "qompack: not implemented"
--- FAIL: TestNewSpool_EveryOperationIsNotImplemented
--- FAIL: TestRunClientSuite_AgainstQompackStub/.../behaviour/send_always_reports_a_mode...
--- FAIL: TestRunSpoolWriterSuite_AgainstQompackStub/.../behaviour/... (4 subtests)
```
(These are exactly the tests I then deleted/rewrote/re-wired to the real contract.)

**RED — spool_test.go's own new assertions, before the design fix:** `TestSpoolCap` initially
failed (`expected: int(1), actual: int64(0)`) because my first draft set `s.bytes` before ever
opening the file, and the file-open path resets `s.bytes` from a fresh `Stat()`. Fixed by doing
one real Append first (to open the file), then forcing `s.bytes`.

**RED — Windows TempDir cleanup:** `TestSpoolAppendOnly`/`TestSpoolCap`/client-suite tests failed
their `t.Cleanup` step with `unlinkat ...: The process cannot access the file because it is being
used by another process` — the real spool keeps its file handle open for the process lifetime (by
design), so anything that never calls `Close()` leaks a handle Windows can't unlink. Fixed by
adding `Close()`-on-cleanup everywhere a real spool/client is constructed in tests (including
`ipctest/suite_test.go`'s two pre-existing factories).

**RED — `TestSendOversizeExternalizes`:** `got.Event.ToolResponse` was `[]byte("null")`, not
empty, after the round trip; `require.Empty` failed. This is the same documented quirk
`frame_test.go`'s `rawMessageNullIsNil` comparer exists for (a nulled `json.RawMessage` marshals
as the literal `null` and decodes back as those 4 bytes, not nil) — fixed the *test assertion*,
not the implementation.

**GREEN — full package, race, twice:**
```
$ go test ./internal/ipc/... -race -count=2 -timeout 300s
ok  	github.com/qompack/qompack/internal/ipc	20.811s
ok  	github.com/qompack/qompack/internal/ipc/ipctest	2.133s
```

**GREEN — cross-platform compile (build + vet, including test files):**
```
$ GOOS=linux GOARCH=amd64 go build ./...   # clean
$ GOOS=darwin GOARCH=arm64 go build ./...  # clean
$ GOOS=linux go vet ./...                  # clean (also type-checks _test.go)
$ GOOS=darwin go vet ./...                 # clean
```

**GREEN — devtool fmt/lint/vet:**
```
$ go run ./tools/devtool fmt    # reformatted 2 files once, then clean on rerun
$ go run ./tools/devtool lint   # golangci-lint, nomagic, importgraph, testdeps, bindeps,
                                 # sleepcheck, stubskips all PASS
$ go run ./tools/devtool vet    # clean
```

**GREEN — full suite once:**
```
$ go test ./...
ok all packages, including test/guards (the stubs guard calling
ipc.NewClient(ipc.Addr{}, nil, logging.Nop(), obs.New(...)) with zero-valued args) and
internal/daemon.
```

**Benchmark sanity** (not part of the required commands, run for my own confidence):
```
$ go test ./internal/ipc/ -run '^$' -bench BenchmarkServerRoundTrip -benchtime 2000x
BenchmarkServerRoundTrip-22    2000    67775 ns/op   # ~68µs mean, well under the 2ms p99 budget
```

## Design decisions / how ambiguities were resolved

1. **`spool` struct carries `log`/`m` fields despite the task-2-spec.md sketch omitting them.**
   The spec's prose ("obs.Counter(\"l0.dropped\").Inc(), log.Loud(...) ... return nil to the
   caller") is unimplementable through `NewSpool(dir string)`'s fixed 2-arg signature alone. I
   added an unexported `newSpool(dir, log, m)` that `NewClientWithOptions` doesn't currently call
   directly (the client receives an already-built `SpoolWriter`, matching every call site in the
   repo today) — it exists so `spool_test.go` (white-box, `package ipc`) can exercise the real
   Loud/counter behavior the spec describes. `NewSpool` itself still wires `logging.Nop()`/`nil`,
   matching the shipped shape exactly.
2. **Counter semantics on sticky failure**: the spec's "Loud once" is satisfied by `sync.Once`,
   but I decoupled the *counter* from that gate — `l0_dropped` increments on every dropped Append
   (10 calls → counter=10), while `Loud` fires exactly once. This is what
   `TestSpoolWriteFailureDropsAndLoudsOnce`'s table row ("l0.dropped counter is 1 **per event**,
   exactly one Loud line **for 10 events**") describes.
3. **`listen(a Addr, log logging.Logger, maxLine int)`** — added the `log` parameter (spec sketch
   shows only `a Addr, maxLine int`) because Windows' SDDL-fallback path needs to `Loud` when
   `user.Current()` fails, and `listen` has no other way to reach a logger. `NewServer` already
   has `log`, so this is a one-line plumb-through, not a new dependency.
4. **`dial`'s shipped signature preserved** per the binding ruling: `client.connect` computes an
   effective timeout from `ConnectDeadline` and `ctx`'s own deadline, then calls the shipped
   `dial(addr, timeout)` rather than a context-aware variant.
5. **POSIX `listen`'s "already in use" case has no named sentinel.** task-2-spec.md's prose says
   "return `ErrLockHeld`", but `ErrLockHeld` is `daemon.ErrLockHeld` per `plan-interfaces.md`, and
   `ipc` cannot import `daemon`. `listen_unix.go` returns a plain wrapped error instead; nothing
   in `ipc`'s test table depends on a specific sentinel there.
6. **`TestWindowsPipeACLRejectsOtherUser`** is gated behind `QOMPACK_TEST_ACL=1` in both
   directions — unset, it skips with a `platform:` reason; set, it *still* skips, because proving
   denial needs dialling as a second, genuinely different Windows principal, which this sandbox
   does not have provisioned. `TestWindowsSDDL_Shape` (unconditional) covers the "assert the SDDL
   string shape unconditionally where cheap" half of the deliverable. This is a real coverage gap,
   flagged below.
7. **Blob externalization targets only `Event.ToolResponse`** (per spec). A request whose bulk is
   in `Raw` instead (as in `ipctest`'s own `oversizeRequest()` fixture) is *not* shrunk by
   `externalize`; it travels the wire oversized and is rejected server-side via `ErrLineTooLong`
   → NAK. Traced through both `runOversizeFrameCase` (ipctest) and my own `TestSendNeverReturnsError`
   as a real, correct, tested path — not an oversight.

## Self-review findings (fixed before commit)

- `lazySpawn` never created `<root>/.qompack/run/` before calling `paths.CreateNew`, so on a cold
  project (no daemon has ever started) the lock-take would fail with ENOENT and lazy spawn would
  silently never fire. Added `os.MkdirAll` before `CreateNew`.
- Removed a leftover custom `minInt` helper in `spool.go` in favor of the Go 1.21+ builtin `min`
  (go.mod targets 1.26).
- `golangci-lint` flagged unnecessary `byte(NAK)`/`byte(ACK)` conversions (they're already typed
  `byte`) and a stale `copyloopvar`-era `behavior := behavior` shadow — both fixed.
- Ran `devtool fmt` twice to confirm idempotence after the above fixes.

## Concerns

- **`TestWindowsPipeACLRejectsOtherUser` has no real assertion path in this environment** (item 6
  above). The unconditional SDDL-shape test covers the cheap half; the actual cross-principal
  denial is untested here and would need a CI job with a second provisioned Windows account.
- **`listen`'s extra `log` parameter** is a deliberate, documented deviation from task-2-spec.md's
  literal 2-arg sketch (item 3 above) — flagging in case the controller wants it reconciled
  differently.
- **`ExternalizeThreshold`'s degenerate-zero case**: `client.threshold` falls back to `MaxLine`
  when `State.MaxPayloadBytes <= 0`, rather than collapsing to 0 as a literal `min()` would. A real
  `State` always carries a positive `MaxPayloadBytes` (config schema floors it at 4096), so this
  only differs from the spec's literal formula for a hand-built zero-valued `State{}` in a test.

## Verification commands run (all clean)

```
go test ./internal/ipc/... -race -count=2
GOOS=linux go build ./... / go vet ./...
GOOS=darwin go build ./... / go vet ./...
go run ./tools/devtool fmt / lint / vet
go test ./...
```

---

## Fix round 1

Review verdict: 1 Critical, 7 Important, 13 Minor. Amended commit
`e4dbd3b9266d844d9b60b7866ee6e36b785eae31` (subject/body unchanged, no attribution trailers) —
still exactly one commit ahead of `71f50ba1`.

### Critical

**C-1 — double newline in every spooled record.** Fixed. `writeLocked` was writing
`s.f.Write(append(line, '\n'))`, but `EncodeRequest` already returns a `\n`-terminated line
(`frame.go`'s `encodeLine` uses `json.Encoder.Encode`, which appends exactly one). Changed to
`s.f.Write(line)`, and corrected the two size guards that had absorbed the same off-by-one
(`len(line)+1 > MaxLineBytes` → `len(line) > MaxLineBytes`, in both `Append`'s pre-check and
`writeLocked`'s cap check). Added `TestSpoolAppend_WritesExactlyOneNewlinePerRecord`
(`spool_test.go`), which counts raw `\n` bytes and asserts `strings.Contains(raw, "\n\n") ==
false` — it does **not** filter empty lines the way the existing readers did, which is exactly
what let this bug ship unnoticed.

### Important

- **I-2 — failed spool append miscounted as spooled.** Fixed in `client.go`'s `appendToSpool`:
  a non-nil `spool.Append` error now takes the same drop-count-Loud-once path as a nil spool,
  instead of unconditionally incrementing `l0_spooled`. Added
  `TestSendFailedSpoolAppendCountsAsDropped`: a Raw-only oversized request (no `Event`, so
  `externalize` is a no-op) reaches `Append` intact, gets refused, and the test asserts
  `l0_dropped == 1`, `l0_spooled == 0`, and no spool file is left behind.
- **I-3 — `externalize` clobbering a pre-existing `Raw`.** Fixed by nesting, per the reviewer's
  suggested resolution: `blobRef` gained an `X json.RawMessage \`json:"x,omitempty"\`` field, set
  to the request's original `Raw` when non-empty, so a request that legitimately used both `Event`
  and `Raw` keeps both — the blob descriptor now carries `{"blob","bytes","field","x"}` instead of
  discarding `x`. Added `TestSendOversizeExternalizePreservesExistingRaw`.
- **I-4 — unbounded `Serve` wait / leaked connections.** Fixed in `server.go`: the `server` struct
  gained a `conns map[net.Conn]struct{}` (guarded by `connsMu`); `Serve`'s accept loop tracks every
  accepted connection via `trackConn(conn, true)`, `handleConn` untracks its own via a deferred
  `trackConn(conn, false)`, and `Close` now calls `closeTrackedConns()` (closing every accepted
  connection, which unblocks any goroutine wedged in `ReadLine`) before waiting. Both `Serve`'s
  shutdown paths and `Close` now go through one shared `waitForConns()` bounded by
  `serverCloseWait`, so neither can hang indefinitely. Also added a per-read
  `SetReadDeadline(connIdleTimeout)` (10 min, named constant) in `handleConn` as the coordinator's
  message asked for explicitly, as a second, independent backstop beyond connection-closing. Added
  `TestServerCloseWithLiveConnection`: opens a raw connection that never sends anything, then
  asserts both `Close` and `Serve` return within 5 s.
- **I-5 — racy post-wait `os.Remove`.** Fixed by deleting it entirely (the reviewer's first
  option): `net.UnixListener.Close` already unlinks the socket as part of closing the listener, so
  the extra `os.Remove` — which could run up to `serverCloseWait` later — was redundant in the
  happy path and, in the unhappy path, could unlink a replacement daemon's brand-new socket.
  `Close`'s doc comment now explains why it does *not* do this.
- **I-6 — `ErrAddrInUse` sentinel (controller ruling #21).** Added `var ErrAddrInUse =
  errors.New(...)` to `resolve.go` (alongside `ErrAddrTooLong`, not in `listen_unix.go`, so the
  symbol exists on every platform even though only POSIX can currently produce it — see M-10).
  `listen_unix.go`'s "a live daemon already owns this socket" path now wraps it via
  `fmt.Errorf("%w: %s", ErrAddrInUse, a.Path)`. Added `TestListenReturnsErrAddrInUse`
  (`server_unix_test.go`): binds a server, then asserts a second `NewServer` at the same address
  returns an error satisfying `errors.Is(err, ErrAddrInUse)`. `ipc` still does not import `daemon`;
  `daemon.AcquireLock` (Task 3+) is expected to wrap this one into its own `ErrLockHeld`.
- **I-7 — leaked `rapid.checks` flag.** Fixed: `TestSendNeverReturnsError` now reads
  `flag.Lookup("rapid.checks").Value.String()` before mutating it and restores it via
  `t.Cleanup`. Verified `frame_test.go`'s `TestDecodeRequestRoundTrip` (Task 1's property test)
  runs at the full default 100 checks again — confirmed in the `-v` run: `frame_test.go:124:
  [rapid] OK, passed 100 tests`.
- **I-8 — no `ipc_decode_error` coverage.** Added `TestServerDecodeErrorNAKsAndCounts`
  (`server_test.go`): writes literal malformed bytes (`{not json\n`) on a raw connection, asserts
  NAK, `ipc_decode_error == 1`, zero handler invocations, and that a following well-formed request
  on the same connection still ACKs.

### Minors — fixed (all reviewed as trivial, no behavior change beyond the fix itself)

- **M-6** — reworded `ErrSpoolFull`'s doc comment to state plainly that no caller ever observes it
  (it is swallowed into the drop path).
- **M-7** — collapsed `spoolDirPerm` (0o700, `spool.go`) into the existing `dirPerm` (0o700,
  `addr.go`); updated the three call sites (`spool.go`, `client.go`'s `writeBlob`,
  `spool_test.go`). Left `writeBlob`'s bare `0o600` alone (see "Minors left" — no existing constant
  actually fits without inventing a new, semantically-loose one).
- **M-9** — reworded the `Server.Serve` interface doc comment (`client.go`) to say it returns
  `ctx`'s own error on a cancellation-driven shutdown, not unconditionally `nil`.
- **M-10** — added an explicit doc comment on `listen_windows.go`'s `listen` recording that
  Windows has no equivalent to POSIX's live-listener probe and cannot return `ErrAddrInUse`; the
  daemon-level lock is the real defence there.
- **M-11** — `lazySpawn`'s doc comment now states the undocumented third precondition
  (`ProjectRoot != ""`) explicitly, alongside `Self`/`Spawn`.

### Minors — attempted, reverted (premise did not hold)

- **M-4** — the review's suggested one-liner (`paths.OpenFile(s.Path(), O_WRONLY|O_TRUNC, 0o600)`
  → `require.ErrorIs(core.ErrAppendOnly)`) does **not** hold for the spool path. I added it, ran
  the suite, and it failed: `paths.IsProtected` only covers `sketches/tried.bloom`,
  `checkpoints/*`, and `pins/*` — the spool directory is not on that list, so `paths.OpenFile` with
  `O_TRUNC` against a spool file succeeds rather than returning `ErrAppendOnly`. The real guarantee
  is structural, not access-controlled: `paths.AppendOnly`'s own signature takes no flags
  parameter, so there is no way to reach `O_TRUNC` *through it* — but a caller that bypassed
  `AppendOnly` and called `paths.OpenFile` directly with `O_TRUNC` on a spool file today would
  succeed, not be refused. I reverted the assertion rather than ship a false claim. This looks like
  a genuine (pre-existing, not introduced by this commit) gap between the §7.4 append-only guard's
  documented scope and the spool tier — worth the controller's attention, but extending
  `IsProtected` to cover `spool/` is a `paths`-package design decision beyond a trivial fix here
  and risks side effects on other `spool/*` write paths I have not audited.

### Minors — left for the ledger

M-1 (ACL test can never fail — needs either real cross-principal dial infra or a scope decision to
delete the placeholder), M-2 (no automated p99 budget assertion on `BenchmarkServerRoundTrip`),
M-3 (property-test bounds looser than the letter of the spec — changing either the count or the
wall-clock slack meaningfully changes runtime/flakiness and needs a considered call, not a
<5-line patch), M-5 (NAK-overload-permanently-disables-hot-path — explicitly flagged by the
reviewer as spec-mandated, not a defect of this commit), M-8 (`spawn.lock` never cleaned up after
a successful spawn — correct cleanup timing needs `Spawn` to report back when the daemon is
actually up, which this commit's fire-and-forget `Spawn` signature does not support), M-12 (op.go
change out of scope — reviewer explicitly not asking for a revert), M-13 (`client.threshold`
degenerate-zero fallback — reviewer says "keep the comment", already disclosed in the original
report, no action needed).

### Verification (all clean, re-run after every fix)

```
go build ./...
go vet ./internal/ipc/...
go test ./internal/ipc/... -v                       # all green, incl. new regression tests
go test ./internal/ipc/... -race -count=2 -timeout 300s
    ok  	github.com/qompack/qompack/internal/ipc	22.727s
    ok  	github.com/qompack/qompack/internal/ipc/ipctest	2.399s
GOOS=linux  GOARCH=amd64 go build ./... / go vet ./...   # clean, incl. _test.go files
GOOS=darwin GOARCH=arm64 go build ./... / go vet ./...   # clean, incl. _test.go files
go run ./tools/devtool fmt                           # clean second run (idempotent)
go run ./tools/devtool lint                          # golangci-lint/nomagic/importgraph/
                                                       # testdeps/bindeps/sleepcheck/stubskips: PASS
go run ./tools/devtool vet                            # clean
go test ./...                                         # all green, incl. test/guards, internal/daemon
```

Task 1's property test confirmed restored to full rigor:
```
frame_test.go:124: [rapid] OK, passed 100 tests (1.6358ms)   # TestDecodeRequestRoundTrip
```
(previously silently reduced to 50 by `TestSendNeverReturnsError`'s unrestored flag mutation).

### Concerns carried forward

- M-1/M-2/M-3/M-8 above are genuine, disclosed gaps left in the ledger per the coordinator's
  instruction.
- The M-4 investigation surfaced a real, pre-existing scope gap in `paths.IsProtected` (spool/ is
  not append-only-guarded the way checkpoints/pins/sketches are) that this task did not introduce
  and is not positioned to fix unilaterally — flagging for the controller rather than silently
  dropping it.
- `connIdleTimeout` (10 min) is a new named constant with no config-driven equivalent; it is a
  transport-layer backstop, not a protocol value, so I did not thread it through
  `config.Config`/`ClientOptions` — flagging in case a future task wants it operator-tunable.

---

## Fix round 2

Re-review confirmed all 8 fix-round-1 findings landed correctly, and surfaced one new Important
(N-3) plus two trivial Minors (N-1, N-2). Amended commit
`dd2f5e2` (subject/body unchanged, no attribution trailers) — still exactly one commit ahead of
`71f50ba1`.

### N-3 (Important) — `TestStaleUnixSocketReclaimed` would fail on real POSIX

**Confirmed correct, fixed.** `stale.Close()` on the listener `listen()` returns (concretely a
`*net.UnixListener`, since it comes from `net.Listen("unix", ...)`) unlinks the socket file as
part of an ordinary `Close()` — that is Go's documented default (`net/unixsock_posix.go`:
`UnixListener.Close` removes the socket path unless `SetUnlinkOnClose(false)` was called, and the
default `unlink` state for a listener package `net` itself created via `Listen`/`ListenUnix` is
`true`). So the test's very next assertion, `os.Stat(addr.Path)` expected to succeed ("the stale
socket file must still exist"), would in fact see `ErrNotExist` on Linux/macOS — the premise never
held; only Windows execution (where this file doesn't build) let it go unnoticed.

**Fix** (`server_unix_test.go`): type-assert the returned `net.Listener` to `*net.UnixListener`,
call `SetUnlinkOnClose(false)` before `Close()`, so the socket file is left behind exactly as an
unclean shutdown (SIGKILL, panic before cleanup) would leave it — the scenario the test's own name
and doc comment already claimed to simulate. Added a comment citing
`net/unixsock_posix.go` and explaining why the flip is necessary.

**How N-3 and the rest of `server_unix_test.go` were verified**, per the coordinator's fallback
instructions:

- **WSL**: `wsl -l -v` lists exactly one distro, `docker-desktop`, currently `Stopped` — Docker
  Desktop's own internal utility VM, not a general-purpose Linux userland, and it has no Go
  toolchain. No general WSL distro (Ubuntu/Debian/etc.) is installed. Not usable.
- **Docker**: `docker version` returns a client (v29.6.1) but fails to reach the engine
  (`failed to connect to the docker API at npipe:////./pipe/dockerDesktopLinuxEngine`); `tasklist`
  shows no Docker process running at all — Docker Desktop itself is not started. Starting the
  Desktop GUI/VM from here would be slow, unreliable in a sandboxed session, and is exactly the
  kind of heavyweight, uncertain operation the fallback path exists to avoid. Not usable.
- **Fallback taken**: desk-verification against `net/unixsock_posix.go` and POSIX `AF_UNIX`
  semantics, cross-checked by getting `GOOS=linux`/`GOOS=darwin` `go vet` to *type-check*
  `server_unix_test.go` (confirming `*net.UnixListener.SetUnlinkOnClose(bool)` exists and the file
  compiles under both target platforms — vet would reject an undefined method). Per-test reasoning:

  - **`TestUnixSocketPermissions`**: `listen()`'s `net.Listen("unix", ...)` creates the socket file
    at an OS-determined mode, but `listen_unix.go` immediately follows with an explicit
    `os.Chmod(a.Path, socketPerm)` (0600) — the test's assertion is against that explicit chmod,
    not against whatever `net.Listen` happened to pick, so it is umask-independent. The directory
    check is against `os.MkdirAll(dir, dirPerm)` (0700): `MkdirAll`'s requested mode is `&`-masked
    by the process umask, but a umask can only *clear* bits, never set them, and 0700 has no
    group/other bits for a conventional umask (022/002/077) to clear — the owner bits themselves
    are never masked by any umask value used in practice. High confidence.
  - **`TestStaleUnixSocketReclaimed`** (fixed): traced step by step above — `dial()` against an
    orphaned `AF_UNIX` `SOCK_STREAM` path (file exists, nothing listening) returns `ECONNREFUSED`,
    which `isStaleSocketError` already matches, triggering the `os.Remove` + rebind path this test
    exists to exercise. This is standard, well-documented POSIX socket behavior (an orphaned
    socket special-file refuses connections at the kernel level; it does not report "no such
    file"). High confidence.
  - **`TestListenReturnsErrAddrInUse`**: the second `NewServer`'s probe `dial()` connects to the
    *first*, still-live listener. A local `AF_UNIX` `connect()` completes purely inside the kernel
    (no network stack, no round trip) and succeeds as soon as the listening socket's backlog has
    room — it does not require the peer to have called `accept()` yet — so `posixListenProbeTimeout`
    (2 ms) is enormous headroom for this, not a tight budget. This exact probe-into-a-live-listener
    path was, however, never exercised on Windows (`listen_windows.go` has no probe at all — see
    M-10), so this specific mechanism has zero prior local execution history; the confidence here
    rests on first-principles POSIX socket semantics rather than on having watched it pass before.
    High confidence, flagged as the one row with the least direct precedent.
  - Everything downstream of a successful bind (the raw ACK/NAK wire exchange each test performs
    afterward) is platform-neutral code in `server.go`/`client.go`/`frame.go` with no build tags,
    already exercised exhaustively — including under `-race -count=2` — via the identical code
    path on Windows (`listen_windows.go` supplies a different `net.Listener` implementation, but
    `Serve`/`handleConn`/`dispatch` never branch on which one it is). Very high confidence.

  I am not asserting these three tests are proven to pass on POSIX with the same certainty as a
  real CI run would provide — only that I have traced every assertion against documented stdlib
  and POSIX behavior and found no remaining false premise. CI (which does run on
  ubuntu-latest/macos-latest per the subplan) is the actual executing environment for this file;
  this desk-check is the best substitute available in this sandboxed session.

### N-1 (Minor) — fixed

`spool.go`'s `Append` doc comment still claimed externalization makes the size-refusal path
unreachable from `Client.Send` — the exact false premise I-2's fix in `client.go` had already
disproved (a Raw-only oversized request bypasses `externalize` entirely). Reworded to state the
real relationship: externalization *reduces* how often the refusal triggers but cannot prevent it
in general, and a non-nil return must always be treated as a real, counted drop.

### N-2 (Minor) — fixed, both halves

- **Comment overclaim**: `waitForConns`' doc comment claimed the accept loop "has already stopped"
  by the time either caller (Serve's own shutdown path or an independently-invoked `Close`) reaches
  it — not actually guaranteed for the `Close`-invoked case, which can run concurrently with a
  still-live `Accept`. Reworded to state the real safety argument: `sync.WaitGroup` permits an `Add`
  that takes the counter from zero to positive to race with a `Wait` (per its own documentation);
  what is actually unsafe — calling `Add` again after a `Wait` has already observed zero and
  returned — cannot happen here because every `Add` lives inside the accept loop, which stops
  admitting new connections once the listener this shutdown closes.
- **Loose bound**: `TestServerCloseWithLiveConnection`'s `shutdownTestBound` was 5 s, which is
  ≥ 2×`serverCloseWait` (4 s) — loose enough that a regression silently dropping
  `closeTrackedConns` would still pass, since each of the two `waitForConns` calls (one in `Close`,
  one in `Serve`'s post-`Close` return path) is independently capped at `serverCloseWait` by its
  own internal timer, so the pathological worst case under that regression is close to
  `2×serverCloseWait`, not unbounded. Tightened to `3*serverCloseWait/2` (3 s) — expressed relative
  to `serverCloseWait` rather than as a bare literal so it stays correct if that constant changes,
  and confirmed the real (working) case returns in single-digit milliseconds
  (`go test -run TestServerCloseWithLiveConnection -count=3`: 0.01–0.02 s each run), leaving ample
  margin between "normal" and "regressed but still under the old 5 s bound."

### Verification (all clean, re-run after every fix)

```
go build ./...
go vet ./internal/ipc/...
GOOS=linux  GOARCH=amd64 go build ./... / go vet ./internal/ipc/...   # clean, incl. server_unix_test.go
GOOS=darwin GOARCH=arm64 go build ./... / go vet ./internal/ipc/...   # clean, incl. server_unix_test.go
go test ./internal/ipc/... -v                                        # all green
go test ./internal/ipc/ -run TestServerCloseWithLiveConnection -v -count=3   # 0.01-0.02s each
go test ./internal/ipc/... -race -count=2 -timeout 300s
    ok  	github.com/qompack/qompack/internal/ipc	23.730s
    ok  	github.com/qompack/qompack/internal/ipc/ipctest	2.519s
go run ./tools/devtool fmt                            # clean (no reformatting needed)
go run ./tools/devtool lint                            # PASS across all sub-checks
go run ./tools/devtool vet                             # clean
go test ./...                                          # all green, incl. test/guards, internal/daemon
```

### Concerns carried forward

- N-3's three POSIX-only tests remain desk-verified rather than execution-verified in this
  session; `TestListenReturnsErrAddrInUse`'s probe-into-a-live-listener path in particular has no
  prior local execution history on any platform (Windows has no equivalent probe). CI is the first
  environment that will actually run this file.
- All fix-round-1 carried-forward concerns (M-1/M-2/M-3/M-8, the `paths.IsProtected`/spool-tier
  gap surfaced by M-4, `connIdleTimeout` being unconfigurable) still stand; nothing in fix round 2
  touched them.

---

## N-2a race fix + history replay

New assignment: N-2a — a confirmed, reproducible `sync.WaitGroup` misuse race in
`server.Close`/`waitForConns` vs. `Serve`'s `wg.Add(1)` — plus deferred N-2b (tighten
`TestServerCloseWithLiveConnection`'s bound). Fixed via exact history surgery on the unpushed
branch, per the coordinator's process.

### Diagnosis, confirmed

`Close`'s old `waitForConns` reasoning ("safe because the accept loop has already stopped by the
time Close's Wait runs") was wrong: closing the listener only unblocks a *pending* `Accept`, never
a connection `Accept` had already returned. In the window after `Accept` returns a connection but
before `Serve` calls `wg.Add(1)` for it, a concurrent `Close→waitForConns→wg.Wait()` could already
be registered as a waiter on a zero counter — the exact precondition `sync.WaitGroup`'s own runtime
check panics on (`"WaitGroup misuse: Add called concurrently with Wait"`), and short of the panic,
the same window could leak the connection past `closeTrackedConns`.

### Fix (`internal/ipc/server.go`)

- `server` gained a `closing bool` guarded by the existing `connsMu` (replacing the separate
  `closed atomic.Bool`).
- `registerConn(conn) bool` is now the *only* place that calls `wg.Add` — it does so under
  `connsMu`, after checking `closing`. It returns `false` (no tracking, no `wg.Add`) if `Close` has
  already begun; the caller (`Serve`'s accept loop) then refuses the connection outright
  (`conn.Close()`, no `handleConn` goroutine).
- `unregisterConn(conn)` (replacing `trackConn(conn, false)` + the old inline `wg.Done()`) untracks
  and marks the `wg` slot done together.
- `Close` sets `closing = true` and closes every already-tracked connection inside one `connsMu`
  critical section (the same lock `registerConn` takes) — so "has shutdown begun" and "what is
  currently tracked" are one atomic fact, never two that could disagree.
- Comments on `waitForConns`, `registerConn`, `unregisterConn`, `Close`, and the `server` struct
  itself were rewritten to state the actual safety argument (documented above) rather than the
  disproven one.

### A second, deeper finding: go-winio's own `Close` can hang (Windows)

Building the regression stress test the coordinator asked for (`TestServerConcurrentDialVsClose`,
~100 iterations of concurrent dial-vs-Close) surfaced a *separate*, pre-existing issue: on Windows,
go-winio v0.6.2's `win32PipeListener.Close` (`pipe.go`) sends on an unbuffered `closeCh` and waits
synchronously for its internal listener goroutine to acknowledge shutdown via `doneCh`. When `Close`
races a freshly-issued `Accept` that has not yet connected, the close signal is instead consumed by
`makeConnectedServerPipe`'s own inner `select`, which aborts the pending connect by closing the
half-open pipe handle and then waits for the `connectPipe` goroutine's overlapped I/O to actually
return before acknowledging shutdown. In this exact race, that wait was observed exceeding 20s
(untested how much further) in a substantial fraction of iterations — confirmed, via a throwaway
diagnostic test, to require the concurrent dial specifically: 100 rapid server create/close cycles
*without* any dial completed in ~60ms total, every time.

This is not something my own accept-loop or `registerConn` logic causes — it is entirely inside
go-winio's own `Accept`/`Close` synchronization, present before this fix round, on a code path
(`s.ln.Close()`) my own `Close` already called in exactly the same form. Since I was already
modifying `Close` for N-2a, I hardened it with the same "an orderly shutdown must have an upper
bound" philosophy `waitForConns` already uses: `closeListenerBounded` runs `s.ln.Close()` in its
own goroutine and gives up after `serverCloseWait` if it hasn't returned, so `Close` itself now
*always* returns promptly (confirmed: zero `Close` timeouts across roughly 2,000 stress-test
iterations run while building and tuning this fix). This does not fully eliminate the underlying
go-winio behaviour: `Serve`'s own goroutine still cannot return until its blocked `Accept` call
does, which depends on the platform listener's `Close` actually taking effect — a separate,
decoupled liveness property this package cannot force from outside without a much larger change
(wrapping `Accept` itself in an independently-cancellable call). `closeListenerBounded`'s doc
comment in `server.go` documents this in full, including the exact file/function names in go-winio
so a future investigator does not have to rediscover it.

`TestServerConcurrentDialVsClose` was designed around this reality: it asserts `Close`'s
promptness tightly per iteration (the actual N-2a guarantee, and the one that held in every run),
and drains all 100 `Serve` goroutines on a best-effort basis afterward — any that return within
`serveDrainBound` (5s) must report a nil error, and any stragglers are logged, not failed, since
failing on them would make this test's pass/fail turn on the separate go-winio limitation rather
than on the registerConn race it exists to guard. Across the runs used to validate this design,
straggler counts ranged from 0 to 84 out of 100 — i.e., this go-winio behaviour is not a rare edge
case but a substantial fraction of the time under this exact adversarial pattern. **This is flagged
prominently for the controller**: a Windows daemon shutting down at the exact moment a hook client
connects and disconnects (the normal steady-state pattern — connect once, send one request, close)
could see its `Serve` goroutine linger well past `serverCloseWait`, even though `Close` itself
returns promptly. `Close`'s own promptness means the *caller* (`cli daemon` command, Task 6) is not
blocked, but the `Serve` goroutine itself is a real, if bounded-in-practice-by-process-exit,
straggler. A dedicated follow-up investigating either a fixed/newer go-winio version or wrapping
`Accept` in its own cancellable path seems warranted.

### N-2b: `TestServerCloseWithLiveConnection`'s bound

Tightened `shutdownTestBound` from `3*serverCloseWait/2` to `serverCloseWait/2` (1s), per the
coordinator's exact instruction, and corrected the comment to explain why (the real, working case
returns in low single-digit milliseconds, so a bound at or above `serverCloseWait` itself would let
a regression that fell back to `waitForConns`' own internal timer sneak through). Verified stable
at `-count=3`.

### History surgery

```
1. git status --porcelain empty; HEAD = 115ba19d                                    — confirmed
2. git checkout --detach dd2f5e22                                                    — done
3. Implemented the fix + TestServerConcurrentDialVsClose; go test ./internal/ipc/... -race -count=2 green
4. git add -A && git commit --amend --no-edit
   → C2' = fe73ad1dcf7be3f94fd937752a2fad5c135c71e3
     "feat(ipc): thin client with spool fallback, spawn seam, and the server" (unchanged)
5. git cherry-pick eeca0651 29585ce1 115ba19d — zero conflicts
     → C3' = e9a54530dd4d5a873d8f78170259b543105e50ba
       "feat(daemon): singleton lock, session registry, WAL ingest queue, and drain"
     → C4' = 6bd2dff9651074e0d9673f8f720cfda62cdbf15f
       "feat(contract): G9.3 assertions, fail-loud degradation, two-clean-run restore"
     → C5' = 04f3c81fd76b0b884bcf34f140467f28dcf59440
       "feat(daemon): extension seams, idle controller, sync->spool fallback"
6. Reproducer + full verification (below) — all green
7. git branch -f feat/sp05-daemon-ipc-and-hot-path HEAD && git checkout feat/sp05-daemon-ipc-and-hot-path
8. git log --oneline develop..HEAD — exactly 5 commits, subjects unchanged (verified against
   the original 71f50ba/dd2f5e22-as-fe73ad1/eeca0651/29585ce1/115ba19d sequence)
```

### Verification (step 6, all clean)

```
go test ./internal/daemon/ -race -run 'Lock|Spawn' -count=10 -v
    ok  	github.com/qompack/qompack/internal/daemon	6.022s     (10/10 clean, no panics)
go test ./internal/ipc/... -race -count=2 -timeout 300s
    ok  	github.com/qompack/qompack/internal/ipc	43.939s
    ok  	github.com/qompack/qompack/internal/ipc/ipctest	2.230s
go test ./...                                          — all green (incl. internal/daemon,
                                                            internal/contract, test/guards)
go run ./tools/devtool fmt / lint / vet                 — all clean/PASS
GOOS=linux  GOARCH=amd64 go build ./...                 — clean
GOOS=darwin GOARCH=arm64 go build ./...                 — clean
```

Additional stress validation performed while building the fix (beyond what step 6 required):
`TestServerConcurrentDialVsClose` and `TestServerCloseWithLiveConnection` run clean at `-race
-count=10`; zero panics and zero `Close` timeouts observed across on the order of 2,000 total
dial-vs-Close iterations run during development of this fix.

### Concerns

- **The go-winio `Serve`-straggler finding above is new and unresolved** — flagged prominently for
  the controller. It is a real, substantial-frequency (not rare) behaviour on Windows, decoupled
  from `Close`'s own correctness, and outside what a server.go-level fix can fully close without a
  larger `Accept`-cancellation redesign.
- All concerns carried forward from fix rounds 1 and 2 (M-1/M-2/M-3/M-8, the `paths.IsProtected`
  spool-tier gap, `connIdleTimeout` being unconfigurable, N-3's desk-verified-not-executed POSIX
  tests) still stand.

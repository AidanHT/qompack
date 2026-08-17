### `internal/ipc/spool.go`

**Responsibility.** Client-side durable fallback queue (decision D4: "the queue-and-drain degradation of §8.1 must exist **below** the daemon, not inside it").

```go
type spool struct {
    dir   string
    path  string        // <dir>/client-<pid>.ndjson
    f     *os.File
    bytes int64
    err   error         // sticky: after a write failure the spool reports itself dead once
}
func NewSpool(dir string) (SpoolWriter, error)
func (s *spool) Append(req Request) error
func (s *spool) Path() string
func SpoolFiles(dir string) ([]string, error)  // sorted: wal-* first, then client-*
```

`NewSpool` does **not** open the file — it only records the directory, so a process that never spools pays nothing. `Append` lazily `os.MkdirAll(dir, 0o700)` then opens with `O_APPEND|O_CREATE|O_WRONLY, 0o600` (via `paths.AppendOnly`, which is the only permitted write path into `*.ndjson`), writes the encoded line, and keeps the handle for the process lifetime. No `fsync` — §2.4 makes the WAL, not the spool, the durability boundary, and the spool is a best-effort fallback below it.

**Externalized payloads.** If the encoded request reaches `ExternalizeThreshold(cfg)` = `min(cfg.Runtime.HotPath.MaxPayloadBytes, DefaultMaxLine)` (both default to 1 MiB), the client writes the oversized member to `<dir>/blob-<pid>-<n>.bin` and spools/sends the request with `Raw` replaced by `{"blob":"blob-<pid>-<n>.bin","bytes":<n>,"field":"e.tool_response"}` and `Event.ToolResponse` set to `null`. The daemon resolves blobs during handling and during `Drain`, then deletes them. This keeps a 40 MB tool result off the socket and out of the 1 MiB line limit while losing nothing. Counter: `obs.Counter("l0.externalized")`.

**Spool cap.** A daemon that never comes back must not let the spool grow without bound and fill the user's disk (that would violate §7.1 directly). `Append` refuses once the process's own spool file reaches

```go
const spoolMaxBytes = 64 << 20 // 64 MiB per client file, matching walRotateBytes
var ErrSpoolFull = errors.New("qompack: spool file at cap")
```

returning `ErrSpoolFull`, which takes the failure path below. The daemon deletes drained files, so the cap is only ever reached when nothing is draining — exactly the case where dropping is correct.

**Failure mode (§12.3 "spool write fails").** On the first `Append` error of any kind — permission denied, `ENOSPC`, `ErrSpoolFull`: drop the event, `obs.Counter("l0.dropped").Inc()`, `log.Loud("spool write failed — event dropped", "err", err)` **once per process**, set the sticky `err` so subsequent appends are cheap no-ops, and return `nil` to the caller. The hook still exits 0.

---

### `internal/ipc/listen_windows.go` / `internal/ipc/listen_unix.go`

Build-tagged transport primitives; the only files that import `go-winio` or call `net.Listen`.

```go
// listen_windows.go   //go:build windows
func listen(a Addr, maxLine int) (net.Listener, error)
func dial(ctx context.Context, a Addr) (net.Conn, error)
```

Windows `listen`: build the SDDL from the current user's SID — `u, err := user.Current()`; `sddl := "D:P(A;;GA;;;" + u.Uid + ")"` (on Windows `user.User.Uid` is the SID string). `P` makes the DACL protected so no inherited ACE widens it, and the single ACE grants `GENERIC_ALL` to the invoking user only. Then:

```go
const pipeBufferBytes = 64 << 10 // 64 KiB kernel buffer hint per pipe instance

winio.ListenPipe(a.Path, &winio.PipeConfig{
    SecurityDescriptor: sddl,
    MessageMode:        false,           // byte mode: NDJSON is a stream protocol
    InputBufferSize:    pipeBufferBytes,
    OutputBufferSize:   pipeBufferBytes,
})
```

**Why the buffer is 64 KiB and not `maxLine`.** In byte mode the buffer size is a kernel-allocated hint, not a message-size limit: a 1 MiB NDJSON line streams through a 64 KiB buffer without truncation, and `maxLine` is enforced by `LineReader` in user space. Sizing the buffers at `maxLine` would pin ~2 MiB of non-paged pool per concurrent pipe instance for no benefit. `maxLine` is therefore *not* passed to `winio`; `listen` keeps the parameter only so the two build-tagged files share one signature.

If `user.Current()` fails, fall back to `"D:P(A;;GA;;;CO)"` (creator-owner) and `log.Loud` once — never fall back to a permissive descriptor. Windows `dial`: `winio.DialPipeContext(ctx, a.Path)`; a `windows.ERROR_FILE_NOT_FOUND`/`os.ErrNotExist` result means "no daemon", which is the lazy-spawn trigger.

POSIX `listen`: `os.MkdirAll(filepath.Dir(a.Path), 0o700)`; probe for a live listener by dialling with a 2 ms deadline — if the dial succeeds, return `ErrLockHeld`; if it fails with `ECONNREFUSED` or `ENOENT`, `os.Remove` the stale socket; then `net.Listen("unix", a.Path)` and `os.Chmod(a.Path, 0o600)`. POSIX `dial`: `(&net.Dialer{}).DialContext(ctx, "unix", a.Path)`.

Neither file contains protocol logic; both are ≤ 60 lines.

---

### `internal/ipc/client.go` — the thin client

**Responsibility.** The hot path. `Send` **never returns an error a hook could propagate**; every failure route ends in a spool append and `(Response{OK:false}, nil)`.

```go
type client struct {
    addr   Addr
    spool  SpoolWriter
    log    logging.Logger
    m      obs.Registry
    o      ClientOptions
    hot    HotPathMode      // read once from state.bin at construction
    mode   contract.Mode
    threshold int           // ExternalizeThreshold, precomputed at construction
    spawned bool            // at most one lazy spawn attempt per process
}
```

`NewClient(addr, spool, log, m)` = `NewClientWithOptions(addr, spool, log, m, ClientOptions{})` with zero values filled from `config.Defaults()`; when `ClientOptions.ProjectRoot` is set, `ReadState` populates `hot`, `mode`, both deadlines, `daemonEnabled`, `spoolOnBreach` and `maxPayloadBytes`.

`Send(ctx, req, deadline)` — the exact algorithm:

```
 1. if c.mode == contract.ModeOff            → return Response{OK:true, Mode:ModeOff}, nil   (do nothing at all)
 2. if !c.o.State.DaemonEnabled              → spoolAndReturn(req)
 3. if c.hot == HotSpool && req.Op.HotPath() → spoolAndReturn(req)          // never connects (§2.4)
 4. line, err := EncodeRequest(req)
    if err != nil                            → externalize(req) then retry once; on failure spoolAndReturn
    if len(line) >= c.threshold → req = externalize(req); re-encode
        // c.threshold = min(int(c.o.State.MaxPayloadBytes), c.o.MaxLine), i.e. ExternalizeThreshold
 5. dctx, cancel := context.WithTimeout(ctx, c.o.ConnectDeadline)          // default runtime.daemon.connectDeadlineMs = 5
    conn, err := dial(dctx, c.addr); cancel()
    if err != nil                            → c.lazySpawn(); spoolAndReturn(req)
 6. defer conn.Close()
    conn.SetWriteDeadline(now + c.o.AckDeadline)                            // runtime.daemon.ackDeadlineMs = 8
    if _, err := conn.Write(line); err != nil → spoolAndReturn(req)
 7. if !req.Reply:
       conn.SetReadDeadline(now + c.o.AckDeadline)
       var b [1]byte; n, err := io.ReadFull(conn, b[:])
       if err != nil || n != 1               → spoolAndReturn(req)
       if b[0] == ACK                        → return Response{OK:true, Mode:c.mode, Hot:HotSync}, nil
       if b[0] == NAK                        → c.hot = HotSpool; spool(req)
                                               return Response{OK:false, Mode:c.mode, Hot:HotSpool}, nil
       otherwise (unknown byte)              → spoolAndReturn(req)
 8. if req.Reply:
       conn.SetReadDeadline(now + deadline)                                 // caller-supplied, e.g. 10s for session.start
       line, err := NewLineReader(conn, c.o.MaxLine).ReadLine()
       if err != nil                         → spoolAndReturn(req)
       resp, err := DecodeResponse(line)
       if err != nil                         → spoolAndReturn(req)
       if resp.Hot == HotSpool               → c.hot = HotSpool
       return resp, nil
```

`spoolAndReturn(req)` appends to the spool (swallowing its error per §12.3), increments `obs.Counter("l0.spooled")`, and returns `Response{OK:false, Mode:c.mode, Hot:c.hot}, nil`.

`lazySpawn()` runs at most once per process and only when `c.o.Self != ""` and `c.o.Spawn != nil`: it takes `<projectRoot>/.qompack/run/spawn.lock` with `paths.CreateNew` (`O_EXCL`) containing the current unix-milli; if the file already exists and is younger than **10 s**, it returns immediately (another client is already spawning); if it is older, it is removed and retried once. On success it calls `c.o.Spawn(projectRoot, self)`. It cannot call `daemon.SpawnDetached` directly, because `ipc` may not import `daemon` (§3.2); that is precisely why `ClientOptions.Spawn` is a function field, which `internal/cli` sets to `daemon.SpawnDetached`. When `Spawn` is nil, lazy spawn is skipped entirely. The spawning client **does not wait**: it spools and exits, exactly as §2.4 requires.

`Close()` closes the spool file if it was opened and returns its error; it is the only method that may return a non-nil error, and `cli` ignores it after logging.

**Instrumentation.** The client observes nothing itself (measuring on the hot path costs more than it reveals, and B-A is authoritatively measured by `test/bench/hotpath`). It stamps `Request.TS = clock.Now().UnixMilli()` as the *first* statement of the hook subcommand, before flag parsing and before reading stdin, so the daemon can compute the observed portion of B-A.

---

### `internal/ipc/server.go`

```go
type server struct {
    addr Addr; ln net.Listener; log logging.Logger; m obs.Registry
    maxLine int; wg sync.WaitGroup; closed atomic.Bool
}
func NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error)
func (s *server) Serve(ctx context.Context, h Handler) error
```

`Serve` runs an accept loop; each connection gets a goroutine that reads NDJSON lines until EOF (so a long-lived MCP-over-daemon connection can multiplex), and for each line:

1. `DecodeRequest`; on error write `NAK`, increment `obs.Counter("ipc.decode_error")`, and continue reading.
2. `resp := h(ctx, req)`.
3. If `req.Reply`, write `EncodeResponse(resp)`; else write one byte — `ACK` when `resp.OK`, `NAK` when not.
4. A write error terminates that connection only.

`ErrLineTooLong` writes `NAK` and discards to the next newline. Accept errors that are `net.Error` and temporary are retried after a bounded backoff driven by a ticker (never `time.Sleep`); a permanent accept error returns. `Close` sets `closed`, closes the listener (which unblocks `Accept`), waits for in-flight connections with a 2 s bound, and on POSIX unlinks the socket path.

---

### `internal/ipc` — client, spool, server (`client_test.go`, `spool_test.go`, `server_test.go`)

| Test | Setup | Expected |
|---|---|---|
| `TestSendACKPath` | in-process server ACKing every request | `Response{OK:true, Hot:HotSync}`, `err == nil`, spool file does not exist |
| `TestSendNAKSwitchesToSpool` | server returns `NAK` | `Response{OK:false, Hot:HotSpool}`, `err == nil`, the request **is** in the spool, and a second `Send` never dials (assert with a listener that counts accepts: count stays 1) |
| `TestSendDaemonDownSpoolsAndReturnsNilError` | no listener | `Response{OK:false}`, `err == nil`, one line in `client-<pid>.ndjson`, `Spawn` called exactly once |
| `TestSendNeverReturnsError` | rapid: 200 random `Request`s × {no listener, listener that closes immediately, listener that hangs past the deadline, listener that returns a bogus byte} | `err == nil` in every case; total wall time per call `< 3 ×` ack deadline |
| `TestSendHotSpoolSkipsConnect` | `State.Hot = HotSpool`, listener counting accepts | accepts stay 0 for `observe.tool`; `session.start` **does** connect |
| `TestSendModeOffDoesNothing` | `State.Mode = ModeOff` | no dial, no spool write, `Response{OK:true, Mode:ModeOff}` |
| `TestSendReplyPath` | server returns a `Response` with `Output.HookSpecificOutput.AdditionalContext = "hi"` | client returns that output verbatim |
| `TestSendOversizeExternalizes` | payload 2 MiB, `MaxPayloadBytes` 1 MiB | the line on the wire is `< 4 KiB` and carries `{"blob":…}`; the blob file exists with the full 2 MiB |
| `TestSpoolAppendOnly` | pre-create the spool file with content | after `Append`, the original content is intact and the new line is at the end; opening with `O_TRUNC` is rejected by `paths.AppendOnly` |
| `TestSpoolWriteFailureDropsAndLoudsOnce` | spool dir chmod `0o500` (Windows: read-only attribute) | `Append` returns nil, `l0.dropped` counter is 1 per event, exactly one `Loud` line for 10 events |
| `TestServerRoutesAndACKs` | router with a handler for `admin.ping` | one ACK byte for a non-reply request; one NDJSON line for `Reply:true` |
| `TestServerUnknownOpNAKs` | empty router, no fallback | one `NAK` byte, connection stays open, a following valid request still ACKs |
| `TestServerHandlerPanicIsContained` | handler that panics | `NAK`, server still serving, `ipc.route.panic` counter 1 |
| `TestServerMultiplexesLines` | 3 requests on one connection | 3 ACK bytes in order |
| `TestServerConcurrentClients` | 64 goroutines × 50 requests, `-race` | 3 200 ACKs, no data race, no dropped request |
| `TestUnixSocketPermissions` | `!windows` | socket mode is `0600`, directory `0700` |
| `TestWindowsPipeACLRejectsOtherUser` | `windows`, skipped unless `QOMPACK_TEST_ACL=1` | the SDDL string contains the current SID and begins `D:P` |
| `TestStaleUnixSocketReclaimed` | leave a socket file with no listener | `NewServer` removes it and binds successfully |
| `BenchmarkServerRoundTrip` | warm in-process server | budget: **p99 < 2 ms** for the daemon-side portion of B-A |

### Commit 2

```
feat(ipc): thin client with spool fallback, detached spawn seam, and the server

Send never returns an error a hook could propagate: every failure path ends in a
spool append and exit 0, because a hook that fails takes observability with it.
The 1-byte ACK converts "usually delivered" into "provably enqueued"; NAK is the
in-band hint that carries the spool submode back to an in-flight client.

Refs: SP-05, §7.1, §12, 00-ARCHITECTURE §2.4, D4
```

- [ ] Write `internal/ipc/client_test.go`, `spool_test.go`, `server_test.go` first, including the `rapid` property test `TestSendNeverReturnsError`; confirm they fail.
- [ ] Add `internal/ipc/spool.go`, `client.go`, `server.go`, `listen_unix.go`, `listen_windows.go`.
- [ ] Add `github.com/Microsoft/go-winio` to `go.mod` (it is on §2.5's closed allowed list; no other dependency may be added).
- [ ] Flip off the `t.Skip`s in SP-01's `ipctest` conformance suite and make it pass (Rule W-1: leftover skips are a merge blocker).
- [ ] Run `go test ./internal/ipc/... -race -count=2`; `GOOS=windows go build ./...` and `GOOS=darwin go build ./...` to prove both build tags compile.


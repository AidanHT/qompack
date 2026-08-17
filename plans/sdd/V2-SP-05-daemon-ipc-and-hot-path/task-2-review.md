# Task 2 review — ipc thin client, spool fallback, spawn seam, and the server

Commit reviewed: `13c448f9` (`4dc422b5..13c448f9`, exactly one commit).
Reviewer verification run locally in the worktree:

```
go vet ./internal/ipc/...                          # clean
go test ./internal/ipc/... -race -count=1          # ok (17.8s + 2.2s)
go run ./tools/devtool lint                        # PASS (incl. nomagic, importgraph, sleepcheck, stubskips)
go test ./internal/ipc/... -v | grep SKIP          # only the 6 deliberate fake-stub proofs + the platform-gated ACL test
git show -s --format=%B 13c448f9                   # exact subject + body, no attribution trailers
```

---

## Verdict 1 — SPEC COMPLIANCE

### Commit-level constraints

| Constraint | Status |
|---|---|
| Exactly ONE commit, subject `feat(ipc): thin client with spool fallback, spawn seam, and the server` | **Met.** Body matches the brief verbatim (the brief's "spawn seam" wins over the spec's "detached spawn seam"). No `Co-Authored-By`/`Generated with` trailers. |
| `Send` never returns a caller-visible error | **Met** in every branch of `Send`/`awaitACK`/`awaitReply`/`connect`; graded by `TestSendNeverReturnsError` and ipctest's `send_never_propagates_an_error`. |
| 1-byte ACK (0x06) / NAK (0x15), NAK carries the spool-submode hint | **Met** (`client.go` `awaitACK`, `server.go` `handleConn`/`writeByte`). |
| `MaxLineBytes` 1 MiB cap both directions | **Met** — `NewLineReader(conn, c.o.MaxLine)` client side, `NewLineReader(conn, s.maxLine)` server side, `ErrLineTooLong` → NAK + discard. |
| Oversize `tool_response` externalized to `<spoolDir>/blob-<pid>-<n>.bin`, Raw → `{"blob","bytes","field"}`, `Event.ToolResponse` nulled | **Met** for the specified shape (`externalize`/`writeBlob`/`blobRef`, `blobField = "e.tool_response"`), but see **I-3** for a collateral data-loss path. |
| `net` only inside `internal/ipc`, only `"unix"`; go-winio for pipes | **Met.** `net` appears in client.go / server.go / listen_*.go / *_test.go, all in package `ipc`; `net.Listen("unix", …)` is the only network string; winio only in `listen_windows.go` / `dial_windows.go`. `importgraph` lint passes. |
| Windows SDDL `D:P(A;;GA;;;<SID>)` with `D:P(A;;GA;;;CO)` fallback + one Loud line | **Met** (`windowsSDDL`, `windowsFallbackSDDL`, `log.Loud` gated on `fellBack`). |
| `time.Sleep` banned outside test/bench; accept backoff via timer in select | **Met** (`waitBackoff` uses `time.NewTimer` + `select`). `sleepcheck` lint passes. |
| No file writes outside `.qompack/`; spool cap 64 MiB; `paths.AppendOnly` | **Met.** `spoolMaxBytes = 64 << 20`; the only openers are `paths.AppendOnly` (spool), `paths.OpenFile` (blob), `paths.CreateNew` (spawn.lock). |
| Stubs guard: zero-valued Client must not panic and returns `(Response{OK:false}, nil)` | **Met** — verified by `TestNewClient_ToleratesNilDependencies` and by the guard package in the report's `go test ./...` run. `appendToSpool` handles `c.spool == nil` as "spool unavailable → counted drop". |
| Counter names `l0_spooled` / `l0_dropped` / `l0_externalized` / `ipc_decode_error` / `ipc_handler_panic`; no dotted names | **Met**, spelled as five named constants. |
| Durations/magic numbers from config or §-annotated named constants | **Met** (`spawnLockStaleAfter`, `posixListenProbeTimeout`, `acceptBackoffInitial/Max`, `serverCloseWait`, `pipeBufferBytes`, `spoolMaxBytes`). Two cosmetic duplications noted in **M-7**. |

### `internal/ipc/spool.go`

Every element of the spec is present: the struct (dir/path/f/bytes/sticky err, plus a mutex, log, registry and two `sync.Once`s), `NewSpool`/`Append`/`Path`/`SpoolFiles`, no-open-at-construction, lazy `MkdirAll 0700` + `paths.AppendOnly` + handle retained, no `fsync`, `spoolMaxBytes`/`ErrSpoolFull`, and §12.3's drop-count-Loud-once-sticky-return-nil. `SpoolFiles` sorts wal-* before client-* and treats a missing dir as empty. The `log`/`m` fields beyond the spec's sketch are a justified adaptation (the spec's own prose demands a Loud and a counter the 1-arg constructor cannot supply); `NewSpool` keeps its shipped shape and `newSpool` is unexported.

One implementation defect, not a spec gap: the NDJSON framing itself is wrong (**C-1**).

### `internal/ipc/listen_windows.go` / `listen_unix.go`

Both ≤ 60 lines (57 / 58), no protocol logic, exactly the specified behaviour on each platform. `maxLine` is accepted-and-unused on both, as the spec explicitly prescribes.

- **Deviation (b) — `log` parameter.** *Accepted, no finding.* The spec's own prose ("fall back to `D:P(A;;GA;;;CO)` and `log.Loud` once") is unimplementable through its 2-arg sketch, and the brief restates the Loud as binding. The 3-arg signature is the minimal reconciliation, and `NewServer` already holds the logger.
- **Deviation (c) — no `daemon.ErrLockHeld`.** *Reasoning accepted, execution incomplete.* `ipc` genuinely may not import `daemon` (§3.2), so the sentinel cannot be reused. But the replacement is a bare `fmt.Errorf` with no sentinel of its own, which leaves Task 3 nothing but string matching. See **I-6**.

### `internal/ipc/client.go`

Send's steps 1–8 are implemented in order and with the specified semantics:

| Step | Spec | Shipped |
|---|---|---|
| 1 | `ModeOff` → `{OK:true, Mode:ModeOff}`, do nothing | ✔ (also stamps `Hot`, harmless superset) |
| 2 | `!DaemonEnabled` → spoolAndReturn | ✔ |
| 3 | `hot == HotSpool && Op.HotPath()` → spoolAndReturn, never connects | ✔ |
| 4 | encode; on error externalize + retry once; `len(line) >= threshold` → externalize + re-encode | ✔ |
| 5 | connect bounded by `ConnectDeadline`; on error `lazySpawn()` then spoolAndReturn | ✔ (`connect` also honours an earlier ctx deadline — a correct tightening) |
| 6 | `defer Close`; write deadline `now + AckDeadline`; write error → spoolAndReturn | ✔ |
| 7 | non-Reply: read 1 byte; ACK → `{OK:true,Hot:HotSync}`; NAK → set HotSpool + spool + `{OK:false,Hot:HotSpool}`; other → spoolAndReturn | ✔ (the NAK arm reaches the same response through `spoolAndReturn`, which reads the already-updated `hot`) |
| 8 | Reply: read deadline `now + deadline`; ReadLine; DecodeResponse; `resp.Hot == HotSpool` → latch; return resp | ✔ |

`ClientOptions` carries all eight required fields with the documented zero-value fallbacks. `NewClient` keeps its 4-arg shape and delegates. `Close` reaches the concrete spool through a type assertion and is nil-safe and idempotent. The client observes no latency histograms, only the three `l0_*` counters — as ruled. `lazySpawn` is at-most-once, `spawn.lock` via `paths.CreateNew`, 10 s staleness, reclaim-and-retry, `Spawn` as a plain func field (no `daemon` import).

`ExternalizeThreshold(cfg) = min(cfg.Runtime.HotPath.MaxPayloadBytes, MaxLineBytes)` — exact, and tested.

### `internal/ipc/server.go`

Matches the spec point for point: accept loop, one goroutine per connection reading to EOF, decode-error → NAK + `ipc_decode_error` + continue, `h(ctx, req)`, Reply → `EncodeResponse` else 1-byte ACK/NAK, write error kills only that connection, `ErrLineTooLong` → NAK + discard, temporary-accept backoff via timer, permanent accept error returns, `Close` sets `closed`, closes the listener, waits 2 s, unlinks on POSIX. Panic containment lives in `dispatch` and increments `ipc_handler_panic`, per the no-`ipc.Router` ruling.

Two correctness issues in this file: **I-4** (unbounded `Serve` wait / connection leak) and **I-5** (post-wait `os.Remove` of the socket).

### Test table — row by row

| Row | Status |
|---|---|
| `TestSendACKPath` | Met, incl. "spool file does not exist". |
| `TestSendNAKSwitchesToSpool` | Met. Counts handler invocations rather than accepts; equivalent here because the client dials per Send. |
| `TestSendDaemonDownSpoolsAndReturnsNilError` | Met, incl. `Spawn` called exactly once. |
| `TestSendNeverReturnsError` | Met in substance; 200 checks *total* rather than 200 per behaviour, and the wall-clock bound is relaxed to `3×deadline + 500 ms`. See **M-3**. Also carries **I-7**. |
| `TestSendHotSpoolSkipsConnect` | Met, both halves (`observe.tool` → 0 accepts, `session.start` → 1). |
| `TestSendModeOffDoesNothing` | Met. |
| `TestSendReplyPath` | Met (`AdditionalContext == "hi"` verbatim). |
| `TestSendOversizeExternalizes` | Met: wire line < 4 KiB, blob ref shape asserted, 2 MiB blob on disk, `ToolResponse` nulled (with the documented `null`-RawMessage quirk). |
| `TestSpoolAppendOnly` | **Partially met** — the append-only half is asserted; "opening with `O_TRUNC` is rejected by `paths.AppendOnly`" is not. See **M-4**. |
| `TestSpoolWriteFailureDropsAndLoudsOnce` | Met (file chmod rather than dir chmod — a cross-platform-correct adaptation); 10 drops, 1 Loud. |
| `TestServerRoutesAndACKs` | Met (adapted to Handler, per the no-Router ruling). |
| `TestServerUnknownOpNAKs` | Met (adapted the same way). |
| `TestServerHandlerPanicIsContained` | Met; counter asserted under its ruled name. |
| `TestServerMultiplexesLines` | Met. |
| `TestServerConcurrentClients` | Met (64 × 50 = 3200, race-clean). |
| `TestUnixSocketPermissions` | Met (`!windows` tagged). |
| `TestWindowsPipeACLRejectsOtherUser` | **Substance met, form degraded.** See deviation (a) below and **M-1**. |
| `TestStaleUnixSocketReclaimed` | Met. |
| `BenchmarkServerRoundTrip` | Present; the spec's "p99 < 2 ms" budget is not asserted anywhere. See **M-2**. |

**Deviation (a) — `TestWindowsPipeACLRejectsOtherUser`.** *Substantively acceptable; the form should be fixed.* Read literally, the spec row's Expected column is "the SDDL string contains the current SID and begins `D:P`" — precisely what the unconditional `TestWindowsSDDL_Shape` asserts. Nothing the row *asks for* was dropped, and the brief itself only asks for the shape assertion "unconditionally where cheap". The problem is the residue: the named test now skips in **both** directions, so a CI operator who sets `QOMPACK_TEST_ACL=1` gets a green skip and a false sense of coverage from a test that cannot fail.

### Other checklist items

- **ipctest wired, zero W-1 skips.** Met and verified: all four factories (`RunClientSuite`, `RunSpoolWriterSuite`, `RunServerSuite`, `RunTransportSuite`) run against real implementations, and the only remaining skips are the six deliberate fake-stub proofs plus the platform-gated ACL test. `stubskips` reports these as expected, not as W-1 violations.
- **go-winio in go.mod.** Void per the controller ruling; already present.
- **Nothing silently dropped.** The one spec line with no counterpart at all is POSIX `listen`'s `ErrLockHeld`, which the report flags explicitly (deviation c) — disclosed, not dropped.

---

## Verdict 2 — CODE QUALITY

### Critical

**C-1 — `internal/ipc/spool.go:190` (`writeLocked`): every spooled record is written with two newlines.**
`EncodeRequest` returns a line that **already ends in `\n`** — `encodeLine` uses `json.Encoder.Encode`, and `frame_test.go:61` asserts `bytes.Count(got, "\n") == 1` with a `\n` suffix. `writeLocked` then does:

```go
n, err := s.f.Write(append(line, '\n'))
```

so `client-<pid>.ndjson` gets a blank line after every record. Why it matters: the spool file is the data contract Task 3's `Drain` reads back, and a blank line is not valid JSON — a drain that does the obvious `for scanner.Scan() { json.Unmarshal(...) }` will error on every other line. It also silently inflates the file against the 64 MiB cap.

This is invisible to the entire test suite because both readers strip empties: `client_test.go:627` (`if l != ""`) and `ipctest/behaviour.go:50` (same). The `len(line)+1 > MaxLineBytes` guard at `spool.go:108` is the same off-by-one propagated into the size check.

*Fix:* `s.f.Write(line)`, and change the guard to `len(line) > MaxLineBytes`. Add a spool test that asserts the raw bytes contain exactly `N` newlines for `N` appends (i.e. do **not** filter empties) so the regression cannot come back.

### Important

**I-2 — `internal/ipc/client.go:376` (`appendToSpool`): a failed spool append is counted as `l0_spooled`.**

```go
_ = c.spool.Append(req)
if c.m != nil { c.m.Counter(counterL0Spooled).Add(1) }
```

The comment above it asserts a non-nil error "can only be the size-refusal case, which this path never reaches". That is not true. `spool.Append` returns a real error for (a) an encode failure and (b) `core.ErrBudget` when the line exceeds `MaxLineBytes` — and (b) is reachable on exactly the path the report's own design note 7 describes: `externalize` is a no-op when `req.Event == nil`, when `Event.ToolResponse` is empty (bulk in `Raw`), or when `c.spool == nil`, so an oversized request reaches `Append` intact. The server NAKs it, `awaitACK` latches `HotSpool` and calls `spoolAndReturn`, `Append` refuses it — and the event is **silently lost while `l0_spooled` claims it was durably enqueued and `l0_dropped` stays at zero**. That inverts the binding constraint's own accounting ("spool append, **or counted drop**").

*Fix:*

```go
if err := c.spool.Append(req); err != nil {
    c.dropOnce.Do(func() { c.log.Loud("ipc: spool append refused — event dropped", "op", string(req.Op), "err", err) })
    if c.m != nil { c.m.Counter(counterL0Dropped).Add(1) }
    return
}
if c.m != nil { c.m.Counter(counterL0Spooled).Add(1) }
```

**I-3 — `internal/ipc/client.go:432` (`externalize`): `out.Raw = ref` silently destroys a pre-existing `Request.Raw`.**
Externalization triggers on *total encoded line size*, but only ever moves `Event.ToolResponse`. A request that carries both a non-empty `x` (`Raw`) payload and a `ToolResponse`, and that crosses the threshold, has its `Raw` overwritten by the blob reference — the original `x` bytes never reach the daemon and are not written to the blob either. The spec says "Raw replaced by `{...}`" but never contemplates a request that already used the field, and the wire struct (`wire.go:79`) makes both simultaneously legal.

*Fix:* guard the overwrite — if `req.Raw != nil`, either refuse to externalize (fall through to the oversize/NAK path) or nest the ref (`{"blob":…,"x":<original>}`) so nothing is lost. At minimum, document the precondition and assert it.

**I-4 — `internal/ipc/server.go:1528/1543` (`Serve`) + `handleConn`: unbounded shutdown wait and leaked connections.**
`handleConn` sets no read deadline and never selects on `ctx.Done()`; it blocks in `lr.ReadLine()` until the peer writes or closes. `ln.Close()` does not close already-accepted connections, and `Close` does not track them. Consequences:

- `Close`'s 2 s bound expires and `Close` returns, but `Serve`'s post-Accept `s.wg.Wait()` is **unbounded**, so `Serve` never returns and the daemon's stop sequence hangs on a single idle client — precisely the long-lived "MCP-over-daemon" connection `handleConn`'s own doc comment invokes as the reason for the read loop.
- The goroutine and its `net.Conn` leak for the life of the process.

Every current test avoids this only because the test client closes its connection at cleanup.

*Fix:* keep a `map[net.Conn]struct{}` (or a `sync.Map`) of live connections and close them in `Close` after the listener; and bound `Serve`'s wait the same way `Close` does, so `Serve` returns even if a handler is wedged.

**I-5 — `internal/ipc/server.go:1669` (`Close`): the POSIX `os.Remove` is both redundant and racy.**

```go
s.closeErr = s.ln.Close()          // Go's UnixListener already unlinks the socket here
… up to serverCloseWait (2s) …
if ua, ok := s.ln.Addr().(*net.UnixAddr); ok { _ = os.Remove(paths.Long(ua.Name)) }
```

`net.UnixListener.Close` unlinks the socket file it created, so the removal is a no-op in the happy case. In the unhappy case it is worse than a no-op: it runs up to 2 seconds after the path was freed, so a supervisor that starts a replacement daemon in that window has its brand-new socket unlinked by the *old* server's shutdown — clients then dial ENOENT and spool forever against a daemon that is running.

*Fix:* delete the `os.Remove` entirely, or hoist it to immediately after `s.ln.Close()` and before the wait.

**I-6 — `internal/ipc/listen_unix.go:35`: "already in use" has no sentinel.**

```go
return nil, fmt.Errorf("ipc: listen: %s: a live daemon already owns this socket", a.Path)
```

The decision not to return `daemon.ErrLockHeld` is correct (§3.2 forbids `ipc` → `daemon`), but returning an error that wraps nothing leaves Task 3 with string matching as its only option for a condition the plan names explicitly.

*Fix:* add an exported `var ErrAddrInUse = errors.New("qompack: ipc endpoint already owned by a live listener")` and wrap it here; `daemon` can then map/wrap it into `ErrLockHeld`. Cheap now, expensive after Task 3 depends on the message text.

**I-7 — `internal/ipc/client_test.go:508`: `flag.Set("rapid.checks", "50")` is a permanent, process-wide side effect.**
The flag is never restored. Go runs a package's tests in source order across alphabetically-ordered files, so `client_test.go` precedes `frame_test.go`, whose `rapid.Check` at `frame_test.go:124` (Task 1's request/response round-trip property) now runs at 50 checks instead of rapid's default 100. This commit silently halves the rigor of a shipped property test in a way nothing reports.

*Fix:* save and restore inside the test —
```go
old := flag.Lookup("rapid.checks").Value.String()
require.NoError(t, flag.Set("rapid.checks", "50"))
t.Cleanup(func() { _ = flag.Set("rapid.checks", old) })
```
— or drop the flag entirely and let rapid's default stand (200 checks × 4 behaviours is still fast).

**I-8 — no test exercises `ipc_decode_error`.**
The server spec's step 1 and the brief's counter list both name it, and later tasks are told to assert the exact string, but nothing in this commit ever sends malformed JSON. `TestServerUnknownOpNAKs` sends *well-formed* JSON with a bogus op, which decodes cleanly and is refused by the handler — a different path entirely.

*Fix:* one raw-connection case: `conn.Write([]byte("{not json\n"))` → expect `NAK`, `reg.Counter(counterIPCDecodeError).Value() == 1`, and a following valid request still ACKs on the same connection.

### Minor

**M-1 — `server_windows_test.go:2015`: a test that can never fail.** `TestWindowsPipeACLRejectsOtherUser` skips whether or not `QOMPACK_TEST_ACL=1` is set, so the gate is decorative and a CI job that sets it gets a false green. Either make the gated branch attempt the cross-principal dial and `t.Fatal` if the second principal is absent (so the gap is loud where it matters), or delete the placeholder and track the CI job as follow-up. `TestWindowsSDDL_Shape` already carries the substance.

**M-2 — `server_test.go:1868`: `BenchmarkServerRoundTrip` asserts no budget.** The spec states "p99 < 2 ms"; nothing enforces it, and the report's ~68 µs figure was a manual observation. Consider a `-bench`-gated assertion or a note in the bench harness that the budget lives in `test/bench/hotpath`.

**M-3 — `client_test.go:1050`: the property test's bounds are looser than specified.** 50 checks × 4 behaviours = 200 total, where the spec reads "200 random Requests × {4 behaviours}"; and the per-call wall-clock assertion is `3×testDeadline + 500ms` (650 ms) rather than the spec's `3 × ack deadline` (150 ms). The 500 ms slack is wide enough to hide a real deadline regression.

**M-4 — `spool_test.go:2322`: `TestSpoolAppendOnly` skips the row's second clause.** "Opening with `O_TRUNC` is rejected by `paths.AppendOnly`" is not asserted. One line (`paths.OpenFile(s.Path(), os.O_WRONLY|os.O_TRUNC, 0o600)` → `require.ErrorIs(core.ErrAppendOnly)`) closes it.

**M-5 — NAK is overloaded five ways, and the client latches on all of them.** The server emits NAK for a decode error, an oversize line, a handler refusal, an unknown op, and a handler panic; `awaitACK` treats every one as "daemon degraded → `HotSpool` for the rest of the process". A single malformed frame therefore permanently disables the hot path. This is spec-mandated (step 7), so it is **not a defect of this commit** — flagging it for the controller before Task 3 hardens the daemon side.

**M-6 — `ErrSpoolFull` is exported but unobservable.** `Append` swallows it into the drop path and always returns nil, so no caller can ever see it. Either unexport it or document it as an internal signal only (the doc comment currently reads as if callers receive it).

**M-7 — duplicated constants.** `spoolDirPerm = 0o700` (spool.go:2068) duplicates `dirPerm` (addr.go:29); `writeBlob` (client.go:449) uses a bare `0o600` where a named file-permission constant already exists. Collapse to one spelling each.

**M-8 — `spawn.lock` is never cleaned up.** After a successful spawn the lock file lingers in `.qompack/run/`, read-only (`paths.CreateNew` chmods 0o444), until some later client finds it stale. Harmless but permanent litter, and it will show up confusingly in `/qompack:status`. Consider removing it once `Spawn` returns.

**M-9 — `Serve`'s return value contradicts the interface doc.** `Serve` returns `ctx.Err()` on a cancellation-driven shutdown, while `Server.Serve`'s doc comment (client.go, unchanged) still says "It returns nil on an orderly shutdown". ipctest's `requireKnownServeError` tolerates both, so nothing fails — reconcile the comment.

**M-10 — Windows `listen` cannot detect a second binder.** `winio.ListenPipe` will happily create another instance of an existing pipe name, so two daemons can silently share a pipe and round-robin connections; POSIX gets the stale/live probe, Windows gets nothing. The daemon-level lock (Task 3) is the real defence — worth an explicit comment in `listen_windows.go` recording that assumption.

**M-11 — `lazySpawn` adds an undocumented third precondition.** It also requires `ProjectRoot != ""` beyond the spec's `Self != ""` and `Spawn != nil`. Necessary (the lock path derives from it) and tested, but the doc comment should say so.

**M-12 — `op.go` change is outside Task 2's scope.** The `Op.Valid` map lookup + defensive `KnownOps` copy is a genuine improvement and behaviour-preserving, but it is not in the deliverables or the commit checklist and rides along in a task specified as exactly one commit. Noting for the controller's record, not asking for a revert.

**M-13 — `client.threshold` degenerate-zero fallback.** `threshold <= 0 → MaxLine` differs from the spec's literal `min()`. Correct behaviour, disclosed in the report, and only observable for a hand-built zero `State{}`. Keep the comment.

### What is well done

Worth recording, because it is load-bearing for the next tasks: the eight-step algorithm is transcribed faithfully and readably; `connect`'s honouring of both `ConnectDeadline` and an earlier ctx deadline is a correct tightening of a spec that could not express it through the shipped `dial`; `hot` is the only post-construction mutable field and it is properly mutex-guarded (race-clean under `-race`); `dispatch`'s panic containment is exactly the right granularity (per-request, not per-connection); `writeBlob`'s `O_EXCL` is defence-in-depth rather than a collision workaround; `spawnLockIsStale` treating an unreadable lock as stale is the right failure direction; and the Windows-TempDir handle-leak discovery in the report led to a genuinely correct fix in the ipctest factories rather than a papered-over cleanup.

---

**Needs fixes (1 Critical, 7 Important)**

---

## Re-review (fix round 1)

Scope: verification only of the 1 Critical + 7 Important findings above, plus a sanity check of the
fix delta. Minors deliberately left (M-1/M-2/M-3/M-5/M-8/M-12/M-13) are not re-flagged.

Amended commit `e4dbd3b9` — still exactly one commit ahead of `4dc422b5`, subject and body
unchanged, no attribution trailers. Re-verified locally:

```
go build ./...                                     # clean
go vet ./internal/ipc/...                          # clean
GOOS=linux go vet ./internal/ipc/...               # clean
go test ./internal/ipc/... -race -count=1          # ok (23.8s + 2.8s)
go test ./test/... ./internal/daemon/... -count=1  # ok (e2e, guards, daemon)
go run ./tools/devtool lint                        # PASS
```

### Per-finding status

| # | Finding | Status | Evidence |
|---|---|---|---|
| **C-1** | Double newline per spooled record | **Fixed** | `spool.go` `writeLocked` now writes `s.f.Write(line)`; both absorbed off-by-ones corrected (`Append`'s `len(line)+1 > MaxLineBytes` becomes `len(line) > ...`, and the cap check `s.bytes+len(line)+1` becomes `s.bytes+len(line)`). Not test-masked: the new `TestSpoolAppend_WritesExactlyOneNewlinePerRecord` counts **raw** newline bytes (`bytes.Count(raw, "\n") == n`) and asserts `NotContains "\n\n"` — it filters nothing, which is precisely what let the bug hide. Passes. |
| **I-2** | Failed `Append` miscounted as `l0_spooled` | **Fixed** | `appendToSpool` now routes a non-nil `Append` error through the same drop-count-Loud-once path as a nil spool. `TestSendFailedSpoolAppendCountsAsDropped` drives it end to end through `Send` (Raw-only oversize request, so `externalize` is a genuine no-op) and asserts `l0_dropped == 1`, `l0_spooled == 0`, and no spool file created. Passes. |
| **I-3** | `externalize` clobbering a pre-existing `Raw` | **Fixed** | `blobRef` gained an `X json.RawMessage` field tagged `x,omitempty`, populated from `req.Raw`. Additive and omitempty, so the specified `{"blob","bytes","field"}` shape is byte-identical when there is no pre-existing Raw — the spec's wire contract is not broken. `TestSendOversizeExternalizePreservesExistingRaw` asserts the original payload survives under `ref.X` after a real round trip. Passes. |
| **I-4** | Unbounded `Serve` wait / leaked connections | **Fixed** | `server` gained `conns map[net.Conn]struct{}` plus `connsMu`; `Serve` tracks on accept, `handleConn` untracks via defer (LIFO order is correct: `conn.Close()`, then untrack, then `wg.Done()`), and `Close` calls `closeTrackedConns()` before waiting. Both `Serve` shutdown paths and `Close` now share one `waitForConns()` bounded by `serverCloseWait`. Not test-masked: `TestServerCloseWithLiveConnection` opens a silent connection and completes in **0.01 s** — if the fix were only the timeout bound it would take ~2-4 s, so the connection-closing path is empirically exercised, not just the bound. |
| **I-5** | Racy post-wait `os.Remove` of the socket | **Fixed** | Deleted outright, along with the now-unused `os`/`paths` imports in `server.go`; `Close`'s doc records why. Confirmed redundant against the toolchain's own source: `net/unixsock_posix.go:230` sets `unlink: true` and `:190-192` unlinks on `Close`. |
| **I-6** | POSIX "already in use" had no sentinel | **Fixed, per ruling #21** | `ErrAddrInUse` declared in `resolve.go` (platform-neutral, beside `ErrAddrTooLong`, so the symbol exists on Windows too) and wrapped with `%w` in `listen_unix.go`, making it `errors.Is`-able. No `daemon` import; translation deferred to `daemon.AcquireLock`, exactly as ruled. Caveat: its regression test `TestListenReturnsErrAddrInUse` lives in the `//go:build !windows` file and therefore **has never been executed** — see N-3. |
| **I-7** | Leaked `rapid.checks` flag | **Fixed** | Save-and-restore via `flag.Lookup(...).Value.String()` plus `t.Cleanup`. Independently verified rather than taken on report: `go test -v` now prints `frame_test.go:124: [rapid] OK, passed 100 tests` alongside `client_test.go:595: ... passed 50 tests` four times. Task 1's property test is back to full rigor. |
| **I-8** | No `ipc_decode_error` coverage | **Fixed** | `TestServerDecodeErrorNAKsAndCounts` writes literal `{not json` plus a newline on a raw connection and asserts NAK, `ipc_decode_error == 1`, **zero** handler invocations, and that a following valid request still ACKs on the same connection. Passes. |

**8 / 8 fixed. None test-masked.**

### M-4 adjudication — the implementer is right; my suggestion was factually wrong

Verified directly against `internal/paths/appendonly.go:23-39`. `IsProtected` allow-lists exactly
three locations: `sketches/tried.bloom`, everything under `checkpoints/`, and everything under
`pins/`. `spool/` is not among them, so `OpenFile`'s O_TRUNC and non-append guards
(`appendonly.go:49-56`) never engage for a spool file, and my suggested assertion
(`paths.OpenFile(s.Path(), O_WRONLY|O_TRUNC, ...)` expecting `ErrAppendOnly`) would have failed.
Reverting it rather than shipping a false claim was the correct call.

The guarantee that *does* hold for the spool is structural rather than access-controlled:
`paths.AppendOnly` takes no flags parameter, so `O_TRUNC` is unreachable *through it*, and it is the
only opener `spool.writeLocked` uses. A caller that bypassed it and reached `paths.OpenFile`
directly with `O_TRUNC` on a spool file would succeed today.

This is a **pre-existing gap in `internal/paths`**, not introduced by this task: the §7.4 append-only
guard's documented scope simply never covered the spool tier. Recommend ledgering it for a later
`paths`-owned task; extending `IsProtected` to `spool/` would need an audit of every `spool/*` write
path (the daemon's WAL rotation and drain-and-delete in particular) and is correctly out of scope
here. The spec's `TestSpoolAppendOnly` row's second clause should be treated as describing an
intended invariant that does not yet exist, not as a missed assertion.

### `connIdleTimeout = 10 * time.Minute` — acceptable unconfigurable at this layer

It is a transport-layer liveness backstop, not a protocol value or a budget an operator tunes: the
primary defence against a wedged connection is `closeTrackedConns`, and this deadline only covers
the narrow residual case of a peer that goes silent while nobody has called `Close` (plus the
accept-races-Close window, where a connection can be tracked just after `closeTrackedConns` swept
the map). It satisfies the branch's "durations from config or §-annotated named constants" rule as a
named, commented constant, and 10 minutes is far enough above any real hot-path interval to be
invisible. Threading it through `config.Config`/`ClientOptions` now would add an operator knob whose
only correct value is "large", which is worse than a constant.

One hand-off note rather than a change request: whoever builds the long-lived MCP-over-daemon
connection (Task 4+) should re-evaluate — a legitimately idle multiplexed session quieter than 10
minutes gets dropped mid-session. Today's client dials per `Send`, so the exposure is nil.

### New defects introduced by the delta

None material. Three notes:

- **N-1 (Minor, doc)** — `spool.go:92-95`: `Append`'s doc comment still claims the over-size refusal
  "never happens on Client.Send's own path because externalize() keeps every line the client hands to
  Append under the threshold". That is the exact false premise I-2 removed from `appendToSpool`, and
  `TestSendFailedSpoolAppendCountsAsDropped` now proves it false. The sibling comment was updated;
  this one was missed. Delete the clause.
- **N-2 (Minor, doc / test strength)** — `server.go`'s `waitForConns` comment ("by the time either
  caller reaches this point the accept loop that calls Add has already stopped") overstates the case
  for an external `Close` racing a still-running accept loop; the `ln.Close()` that precedes it makes
  the window vanishingly small and `-race` is clean over repeated runs, so this is wording, not
  behaviour. Separately, `TestServerCloseWithLiveConnection`'s 5 s bound exceeds
  `2 x serverCloseWait`, so a future regression back to "timeout-only" would still pass it;
  tightening the bound to ~1 s would make the test assert what its own comment says it asserts.
  (Current behaviour is verified good — the test runs in 0.01 s.)
- **N-3 (Important, pre-existing — surfaced, not introduced)** — `server_unix_test.go`'s
  `TestStaleUnixSocketReclaimed` will **fail on POSIX**. It does `stale, _ := listen(...)`,
  `stale.Close()`, then asserts `os.Stat(addr.Path)` succeeds ("the stale socket file must still
  exist"). But `net.Listen("unix", ...)` returns a `*net.UnixListener` with `unlink: true`
  (`net/unixsock_posix.go:230`) whose `Close` unlinks the path (`:190-192`), so the file is gone and
  the assertion fails. This shipped in round 1 and the delta did not change it — but it means the
  entire `//go:build !windows` file has never been executed anywhere: cross-platform validation on
  this branch has been `go build` / `go vet` only, which type-check tests without running them. That
  also leaves I-6's own regression test (`TestListenReturnsErrAddrInUse`, same file) unexecuted, and
  leaves `listen_unix.go`'s stale-socket reclaim path unexercised in practice, since `ln.Close()`
  cannot produce the condition the test is trying to stage. *Fix:* stage the stale socket without a
  live listener — either call `SetUnlinkOnClose(false)` on the `*net.UnixListener` before `Close`, or
  bind, close, and then re-create a plain file at `addr.Path`. Please confirm with an actual
  `GOOS=linux go test ./internal/ipc/...` run rather than `go vet`.

### Hand-off note (not a finding)

I-3's `blobRef.X` creates an obligation for Task 3: the daemon's blob resolution must now restore
*both* halves — the blob bytes back into `Event.ToolResponse` **and** `ref.X` back into
`Request.Raw` — before deleting the blob file. Resolving only the blob would reintroduce the data
loss I-3 fixed, one layer further down.

### Re-review verdict

All 8 findings from the first pass are genuinely fixed and independently verified; the fix delta
introduces no new defect beyond two documentation nits. The one blocker is N-3, a pre-existing
POSIX-only test failure surfaced by this pass — a small fix, but approving with a test that goes red
on two of three target platforms would not be honest.

**Needs fixes (0 Critical, 1 Important — N-3; plus N-1/N-2 Minor)**

---

## Re-review (fix round 2)

Scope: N-3, N-1, N-2 only, plus a sanity check of the delta and a closure call on desk-verification.
Amended commit `dd2f5e2` — still exactly one commit ahead of `4dc422b5`, subject/body unchanged, no
trailers. Re-verified locally: `GOOS=linux go vet ./internal/ipc/...`, `GOOS=darwin go vet
./internal/ipc/...`, `go test ./internal/ipc/... -race -count=1` — all clean.

The delta touches production code in exactly one place — a comment in `server.go`. Every behavioural
change is confined to tests. No new runtime defect is reachable from it.

### Per-item status

| Item | Status | Assessment |
|---|---|---|
| **N-3** — `TestStaleUnixSocketReclaimed` fails on POSIX | **Fixed, and it now genuinely drives the reclaim path** | Traced independently, end to end. `listen()` returns the `*net.UnixListener` from `net.Listen("unix", …)`, so the type assertion holds; `SetUnlinkOnClose(false)` before `Close()` leaves the socket special-file in place, which is precisely the unclean-shutdown state (SIGKILL / panic before cleanup) the test's own name claims to stage. `NewServer` then re-enters `listen`, whose probe `dial(a, 2ms)` connects to an orphaned `AF_UNIX` `SOCK_STREAM` pathname → `ECONNREFUSED` (documented `connect(2)`/`unix(7)` behaviour on both Linux and Darwin; an orphaned socket file refuses at the kernel level rather than reporting ENOENT) → `isStaleSocketError` matches → `os.Remove` → `net.Listen` succeeds. The error chain also unwraps correctly for `errors.Is`: `*net.OpError` → `*os.SyscallError` → `syscall.Errno`, both of which implement `Unwrap`. The trailing raw-client round trip then proves the rebound listener actually serves. So the test no longer merely asserts a false premise — it exercises the `os.Remove` branch that was previously dead. |
| **N-1** — stale `Append` doc comment | **Fixed** | `spool.go` now states the real relationship: `externalize()` *reduces* how often the size refusal triggers but cannot prevent it, a Raw-heavy request reaches `Append` at full size, and a non-nil return must be treated as a real counted drop. Accurate, and consistent with `client.go`'s `appendToSpool` and with `TestSendFailedSpoolAppendCountsAsDropped`. |
| **N-2a** — `waitForConns` comment overclaim | **Partially addressed** — one overclaim replaced by a different, incorrect one. Minor. | The rewrite correctly concedes that an independently-invoked `Close` can run concurrently with a live `Accept` (round 1 denied this). But its justification inverts the contract it cites. `sync.WaitGroup.Add`'s doc (`sync/waitgroup.go:68-71`) reads: *"Note that calls with a positive delta that occur when the counter is zero must happen before a Wait."* The new comment claims the opposite — that such an `Add` "may race with a `Wait`" and only post-`Wait` reuse is forbidden. `sync` enforces the real rule at `waitgroup.go:121`: `if w != 0 && delta > 0 && v == int32(delta) { panic("sync: WaitGroup misuse: Add called concurrently with Wait") }`. **How reachable is it, in practice?** Narrower than the comment's error suggests. A waiter only registers when the counter is already > 0, so a panic needs a three-way race: a parked `waitForConns` waiter, the *last* `handleConn`'s `wg.Done()` driving the counter to 0, and the accept loop's `wg.Add(1)` for a connection `Accept` returned just before `ln.Close()` landed — all inside the few instructions between `sync`'s decrement-to-zero and its `state.Store(0)`. I modelled exactly that shape in a standalone gated harness and could **not** reproduce it in 200 000 trials. So: a real documented-misuse pattern with a vanishingly narrow window, wrongly recorded as sanctioned. Keep the honesty, drop the false citation. *Cleanest structural fix, near-free since the accept path already takes `connsMu`:* move `s.wg.Add(1)` inside `trackConn`'s critical section together with a `s.closed.Load()` re-check that rejects (and closes) a connection accepted after `Close` swept the map, and have `Close` set `closed` + sweep under that same lock. Then the pattern cannot arise at all and the comment can simply say so. |
| **N-2b** — `shutdownTestBound` | **Requirement met as I literally wrote it; the goal it exists for is still not achieved.** Minor. | `3 * serverCloseWait / 2` = 3 s is indeed `< 2 × serverCloseWait`, and expressing it relative to the constant rather than as a literal is the right instinct. But the premise both the new comment and my round-1 note rest on — that a regression would burn `serverCloseWait` *twice, serially* — is wrong: the two waits run **concurrently**. `Close` does `ln.Close()` (server.go, `closeOnce`) *before* its own `waitForConns()`, and that same `ln.Close()` is what unblocks `Serve`'s `Accept`, so `Serve` enters its `waitForConns()` at essentially the same instant. Both are independently capped at `serverCloseWait`, so a regression that dropped `closeTrackedConns()` would see `closeDone` **and** `serveDone` fire at ≈ 2 s — comfortably inside a 3 s bound, and the test would still pass. To catch it the bound must sit **below** `serverCloseWait`: `serverCloseWait / 2` (1 s) still leaves ~50–100× headroom over the measured 0.01–0.02 s. This one is substantially my fault — my round-1 text gave the right number ("~1 s") wrapped in the wrong diagnostic frame ("exceeds 2 × serverCloseWait"), and the implementer reasonably built to the frame. |

### N-3 closure call — desk-verification + CI accepted

Accepted as sufficient. Three reasons, in order of weight:

1. **CI genuinely executes this file before merge.** `.github/workflows/ci.yml:52-67` runs a
   three-way matrix — `ubuntu-latest`, `macos-latest`, `windows-latest` — with `go test -race ./...`
   on the two POSIX legs. This is not a build-only or vet-only gate, and it is not deferred to
   nightly. The `//go:build !windows` file will actually run.
2. **The change is small and its semantics are documented, not inferred.** `SetUnlinkOnClose` is a
   stdlib API with a stated contract; `ECONNREFUSED` from `connect(2)` on an orphaned `AF_UNIX`
   pathname socket is specified POSIX behaviour, not an implementation detail. I re-derived the
   whole chain independently above and reached the implementer's conclusion without relying on their
   trace.
3. **The environment survey was done honestly and the negative result is credible.** WSL exposing
   only Docker Desktop's utility VM, and a Docker client with no reachable engine, are both real
   dead ends rather than convenient ones; the report says plainly which rows rest on first
   principles rather than precedent (`TestListenReturnsErrAddrInUse`'s live-listener probe), which is
   the right disclosure.

One condition on that acceptance, for the controller's ledger rather than the implementer: **the
branch must not be merged on a green Windows leg alone.** The ubuntu-latest and macos-latest legs of
the `test` job need to be observed green on this commit, because three tests in
`server_unix_test.go` — `TestUnixSocketPermissions`, `TestStaleUnixSocketReclaimed`,
`TestListenReturnsErrAddrInUse` — plus `listen_unix.go` itself have never executed anywhere. If any
of them fails, it is a test-staging bug of exactly N-3's kind, not a defect in the shipped transport.

### Anything new from the delta

No. The only production change is a comment; the test changes are a const value, a `net` import, and
two added lines in one POSIX test. Local `-race` run is green, cross-platform vet is clean on both
POSIX targets, and the commit remains a single, correctly-messaged, trailer-free commit.

### Re-review verdict

N-3 — the one Important — is genuinely fixed, and fixed in the way that makes the test do real work
rather than merely stop failing. N-1 is fixed. N-2 leaves two Minor residues: a comment that is now
honest about the race but cites the `sync.WaitGroup` contract backwards, and a test bound that is
still too loose to catch the regression it names. Neither affects shipped behaviour; both are
one-line changes that belong with the other deferred Minors in the whole-branch review.

**Approved** (2 Minor carried forward: N-2a comment rationale, N-2b bound → `serverCloseWait / 2`)

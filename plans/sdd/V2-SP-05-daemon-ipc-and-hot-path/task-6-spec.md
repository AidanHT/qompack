### `internal/cli/*` — the subcommand surface

SP-05 replaces the **bodies** of SP-01's six no-op hook entry points and adds three subcommands. Every hook subcommand follows one shared skeleton in `cli/hookclient.go`:

```go
func runHook(op ipc.Op, reply bool, deadline time.Duration, args []string) int {
    ts := core.SystemClock().Now().UnixMilli()          // FIRST statement — the B-A origin
    defer recoverToZero()                                // §12.3 "any hook panic → recovered in cli, exit 0"

    root := resolveProjectRoot(nil)                      // env → process cwd walked to nearest .git → cwd
    st := ipc.ReadState(root, config.Defaults())         // one 32-byte read; no config.Load
    if st.Mode == contract.ModeOff { hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }

    ev, _, err := hookio.ReadEvent(os.Stdin, int64(st.MaxPayloadBytes)*4)
    if err != nil { logQuiet(err); hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }

    // The payload is authoritative for the project root, and it only arrives now. If it
    // disagrees with the pre-stdin guess, redo the two cheap resolutions against it.
    if r2 := resolveProjectRoot(&ev); r2 != root {
        log.Debug("project root re-resolved from payload", "was", root, "now", r2)
        root, st = r2, ipc.ReadState(r2, config.Defaults())
        if st.Mode == contract.ModeOff { hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0 }
    }

    req := ipc.Request{Op: op, Session: ev.SessionID, TS: core.UnixMilli(ts),
        Reply: reply, Event: &ev, Raw: rawExtras(args)}
    sp, _ := ipc.NewSpool(filepath.Join(root, ".qompack", "spool"))
    defer func() { _ = sp.(io.Closer).Close() }()

    addr, aerr := ipc.Resolve(root)
    if aerr != nil {                                     // ErrAddrTooLong → spool-only for this process
        _ = sp.Append(req); logQuiet(aerr)
        hookio.WriteOutput(os.Stdout, hookio.Empty()); return 0
    }

    c := ipc.NewClientWithOptions(addr, sp, log, metrics, ipc.ClientOptions{
        ProjectRoot: root, State: st, Self: selfPath(), Clock: core.SystemClock(),
        Spawn: daemon.SpawnDetached,
    })
    defer c.Close()

    resp, _ := c.Send(context.Background(), req, deadline)
    out := hookio.Empty()
    if resp.Output != nil { out = *resp.Output }
    hookio.WriteOutput(os.Stdout, out)
    return 0                                             // ALWAYS
}
```

`SpoolWriter` is an interface (00-ARCH §5.4) with no `Close`; the concrete `*spool` has one, and the type assertion above is the guarded form (`if cl, ok := sp.(io.Closer); ok`) written compactly. `ipc.Client.Close()` closes the spool it was given, so on the path where a client is constructed the assertion is a no-op double close, which `*spool` tolerates via `sync.Once`.

The unexported helpers the skeleton uses all live in `internal/cli/hookclient.go` and are defined once:

- `recoverToZero()` — `defer`red `recover()` that writes `hookio.Empty()` to stdout if nothing has been written yet, logs the panic and stack at `Error`, and lets the caller return 0. This is §12.3's "any hook panic → recovered in `cli`, logged, `exit 0` with empty output".
- `selfPath() string` — `os.Executable()`; returns `""` on error, which disables lazy spawn rather than failing.
- `logQuiet(err error)` — appends to `.qompack/logs` **only if the log directory already exists**; a hook must never create directories on the error path of a failing project.
- `rawExtras(args []string) json.RawMessage` — builds `Request.Raw` from subcommand flags only: `{"subagent":true}` for `observe stop --subagent`, `{"trigger":"<manual|auto>"}` for `checkpoint`, and `nil` for every other subcommand. It never embeds the raw payload bytes; the parsed `Event` already carries them, and duplicating them would double the line size on the hot path.
- `resolveProjectRoot(ev *hookio.Event) string` — §3.3's order: `QOMPACK_PROJECT_ROOT` → (when `ev != nil`) the payload's `cwd`/`project_dir`, else the process `cwd`, walked upward to the nearest `.git` → that directory itself. Called twice per hook, once before stdin is read and once after, because the state file must be read before stdin (it decides whether to read stdin at all) but the payload is authoritative about which project this is.
- `log` and `metrics` are package-level values initialized lazily by `hookclient.go` to `logging.Nop()` and SP-01's registry constructor, so a hook that never fails allocates neither.

Per-subcommand wiring:

| Subcommand | Op | Reply | Deadline |
|---|---|---|---|
| `qompack observe tool` | `observe.tool` | no | `AckDeadline` (manifest timeout 5 s) |
| `qompack observe prompt` | `observe.prompt` | **yes** | `promptReplyDeadline` = 250 ms (manifest timeout 5 s) |
| `qompack observe stop` (`--subagent`) | `observe.stop` | no | `AckDeadline` (manifest timeout 5 s / 10 s subagent) |
| `qompack session-start` | `session.start` | **yes** | 10 s (manifest timeout 15 s) |
| `qompack checkpoint` | `checkpoint` | **yes** | 15 s (manifest timeout 20 s) |
| `qompack flush` | `flush` | **yes** | 15 s (manifest timeout 20 s) |

`qompack session-start` additionally calls `daemon.EnsureRunning(root, selfPath(), log, clk)` **before** `Send`, because §2.4 makes it the designated daemon starter and its hook timeout is generous. If the daemon fails to come up it still sends (which spools), writes `hookio.Empty()` and exits 0.

**`cli/daemon.go`** — `qompack daemon [--project <root>] [--foreground]`: loads config, builds the logger against `.qompack/logs`, `daemon.NewOptions`, `daemon.New`, installs a `signal.Notify` for `os.Interrupt`/`syscall.SIGTERM` that cancels the run context, and calls `Run`. Exits 0 on clean stop and on `ErrLockHeld`; exits 0 with a `Loud` line on any other error (a daemon that cannot start must not surface a non-zero exit through a lazy spawn).

**`cli/selftest.go`** — `qompack self-test [--json]`, **the only subcommand permitted to exit non-zero** (§2.3). It runs, in order: config load and validation; `.qompack/` writability; `paths.AppendOnly`/`CreateNew` guard behaviour; `ipc.Resolve`; daemon reachable or spawnable; a full IPC round trip using `admin.ping`; `Router.Ops()` coverage against `ipc.KnownOps()`; and `contract.StandardAssertions` executed against a synthetic `Env` built from the last persisted `History`. Output is a fixed-width table plus a summary line, or a JSON document under `--json` with `{checks:[{id,ok,severity,expected,observed,detail}], mode, exit}`. Exit code: `0` when no check failed at `SevCritical`, `1` otherwise.

**`cli/fault.go`** — the fault-injection seam used by the exit-0 test suite. `QOMPACK_FAULT` accepts a comma-separated list of `site[:arg]`. **Eleven sites, all concretely defined** (the assignment names daemon-down, spool-full, disk-full, corrupt-config and panic explicitly, so none of them may be left implicit):

| Site | Injection | Expected hook behaviour |
|---|---|---|
| `stdin-eof` | `hookio.ReadEvent` is handed an empty reader | `logQuiet`, empty JSON out, exit 0 |
| `stdin-garbage` | stdin is replaced with `{{{not json` | same |
| `oversize` | the event's `ToolResponse` is inflated to 4 MiB before encoding | externalized to a blob, or spooled; exit 0 |
| `daemon-down` | `ipc.Resolve` returns an address nothing is listening on and `ClientOptions.Spawn` is set to a no-op | one spool line, exit 0 |
| `spool-readonly` | the spool directory is made non-writable (POSIX `chmod 0o500`; Windows `FILE_ATTRIBUTE_READONLY` + a deny-write ACE) | `l0.dropped` +1, one `Loud`, exit 0 |
| `spool-full` | `spool.Append` returns `ErrSpoolFull` once the process has written `faultArg` lines (default 1) | identical to `spool-readonly`: drop, count, `Loud` once, exit 0 |
| `disk-full` | `spool.Append`, `logQuiet` and `ipc.WriteState` return `syscall.ENOSPC` from their first write | drop, count, `Loud` once, exit 0 — no retry loop, no panic |
| `state-corrupt` | `run/state.bin` is overwritten with 32 random bytes (bad CRC) | `ReadState` falls back to `config.Defaults()` silently; exit 0 |
| `config-corrupt` | `.qompack/config.json` is replaced with `{"store":{"chunk":{"min":`  | the hot path never parses config, so this is inert for hooks and exercised by `qompack daemon`/`self-test`: per-leaf fallback + `Loud` (§11.3); exit 0 |
| `panic:hook` | `runHook` panics immediately after `ReadEvent` | `recoverToZero` writes empty JSON, exit 0 |
| `panic:client` | `ipc.Client.Send` panics | recovered by `recoverToZero`, exit 0 |

`faultActive(site string) (arg string, on bool)` is checked at each named site; when `QOMPACK_FAULT` is unset the parsed map is nil and the check is a single nil-map lookup (~2 ns), which is why it may live on the hot path. `TestFaultSitesInertWhenUnset` asserts `faultActive` returns `false` for all eleven sites with the variable unset **and** that a hook run with it unset produces byte-identical output to one built with `-tags noinject`. The `security` CI job greps that the string `QOMPACK_FAULT` appears in exactly one non-test file, `internal/cli/fault.go`, and that `SpawnDetached` strips it from the child environment (it does, see `spawn.go`).

**`cmd/qompack/main.go`** — add `daemon` and `self-test` to the dispatch switch. `self-test` is the only case that returns a non-zero code; every other case returns 0.

---

### `test/e2e` (`daemon_e2e_test.go`, `faultinject_test.go`)

Uses SP-01's `testutil.Project` and the real built binary.

| Test | Setup | Expected |
|---|---|---|
| `TestE2EHookRoundTrip` | build the binary, `qompack session-start`, then 50 × `qompack observe tool` | every exit code 0; `wal-<sess>.ndjson` has 50 lines; `admin.ping` reports one live session |
| `TestE2ELazySpawn` | no daemon; run `qompack observe tool` once | exit 0, one spool line; within 1.5 s a daemon is listening; a second `observe tool` ACKs and the spool is drained to the WAL |
| `TestE2EIdleExit` | `runtime.daemon.idleExitSeconds=1`, `flush`, wait for exit | the process exits; `run/daemon.lock` and `run/state.bin` are gone |
| `TestE2ESelfTestExitsZeroOnHealthy` | healthy project | `qompack self-test --json` exits 0; JSON parses; `mode == "full"` |
| `TestE2ESelfTestExitsNonZeroOnCritical` | pre-degraded `state/contract.json` | exits 1 with a banner naming the assertion |
| `TestE2ESpoolSubmodeEndToEnd` | force `state.bin` `hot=1` | `observe tool` writes to the spool without connecting (assert daemon accept counter unchanged), exit 0, and the next idle tick drains it |
| `TestHooksExitZeroUnderFaults` | table over {6 hook subcommands} × {`daemon-down`, `spool-readonly`, `spool-full`, `disk-full`, `state-corrupt`, `config-corrupt`, `stdin-garbage`, `stdin-eof`, `oversize`, `panic:hook`, `panic:client`} | **every one of the 66 combinations exits 0** and writes valid (possibly empty) JSON to stdout |
| `TestFaultSitesInertWhenUnset` | `QOMPACK_FAULT` unset | `faultActive` false for all eleven sites; a hook run's stdout is byte-identical to a `-tags noinject` build's |
| `TestSelfTestIsTheOnlyNonZeroExit` | run every subcommand in the dispatch table under `daemon-down` | only `self-test` may exit non-zero |

### Commit 6

```
feat(cli): session-start dispatch, daemon start, thin hook clients, and self-test

Hook subcommands become thin clients that stamp the B-A origin as their first
statement and exit 0 on every path; session-start is the designated daemon starter
and runs the contract monitor before any other work, per the split-ownership table.
self-test is the only subcommand permitted a non-zero exit.

Refs: SP-05, §7.3, §12, 00-ARCHITECTURE §2.3, §5.21
```

- [ ] Write `test/e2e/daemon_e2e_test.go` and `test/e2e/faultinject_test.go` first (the full 66-combination table plus `TestFaultSitesInertWhenUnset`); confirm they fail.
- [ ] Add `internal/cli/hookclient.go`, `daemon.go`, `sessionstart.go`, `selftest.go`, `fault.go`; replace the six no-op hook bodies; add `daemon`, `self-test` and (if SP-01 did not ship it) `version` to `cmd/qompack/main.go`.
- [ ] Run `go test ./test/e2e/... -race`; run every hook subcommand by hand with an empty stdin and confirm `echo $?` / `$LASTEXITCODE` is 0.
- [ ] Run `go run ./tools/devtool ci-local`.


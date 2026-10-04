# Wave 19d cmdconnect: no probe deadline, one state read (C4.5)

Branch `closeout/w19b-cmdconnect`. Workflow `wf_38a4c903-991`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:cmdconnect: status `done`, head `4bdb4acd`

### Root cause

(1) go-winio's tryDialPipe checks ctx.Done() before each CreateFile, so under a 50 ms budget a thread starved before its first attempt gets ErrTimeout instead of the os.ErrNotExist refusal. (2) newCommandClient and commandStatusSources each called daemonClientState, which reads state.bin, so the status reason's DaemonEnabled could come from a different read than the client's own.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go).

Wave 19d closed both of wave 19c's cmdconnect review nits (C4.5, D60(e)) on closeout/w19b-cmdconnect in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect. Two commits on top of 722ef8e7. Nothing pushed, and no other worktree was touched.

Nit 1: wall-clock margin in TestStatusProbe_OutlastsAListenerThatIsReArming (internal/cli/status_probe_windows_test.go)
- Cause: go-winio's tryDialPipe checks ctx.Done() before each CreateFile. With winio.DialPipe(nowhere.Path, &budget) and a 50 ms budget, a thread starved past the budget before its first attempt gets ErrTimeout instead of the refusal, so both assertions fail.
- Failing case shown: a throwaway test (deleted, never committed) dialled a pipe that does not exist with an already-expired budget and got "i/o timeout", isTimeout=true.
- Fix: the refused dial now uses winio.DialPipeContext(context.Background(), nowhere.Path). It always reaches CreateFile and is refused with os.ErrNotExist. The address comes from a fresh TempDir and cannot exist, so a dial that waited on it instead of being refused would hang and fail the row. No verdict rests on a timer now.
- Both assertions are unchanged: ErrorIs os.ErrNotExist, and not ErrTimeout. The second can no longer fire because the dial has no deadline; it was kept rather than removed so no check is dropped. The returned conn is closed if non-nil.
- The other dials in the row already gave the same answer however the threads were scheduled. The DialPipe and ipc.Probe calls against a listener that never calls Accept can only time out, and the busy-dial half has no timer.

Nit 2: status read state.bin twice (internal/cli/qompack_commands.go)
- Cause: newCommandClient built the client from one daemonClientState(root, cfg) read. commandStatusSources then read state.bin again for the `enabled` that fetchDaemonStatus branches on. If a daemon rewrote state.bin between the two reads, status described a client that was never built: "disabled" for a client that dialed, or "no connection on both of two attempts" for a client that never dialed.
- Failing test first: TestStatus_ReasonUsesTheStateTheClientWasBuiltWith in internal/cli/status_connect_test.go. Its seam is the existing newCommandIPCClient stand-in, which rewrites state.bin from inside client construction (after the client's read, before buildCommandDeps builds the sources), so nothing is raced.
  - It runs through real `status --json`, with the real ipc client aimed at an address nothing listens on, spawning off, the probe stubbed and stepClock timing the sends.
  - Two subtests: "enabled then disabled" (must give the connect-miss reason, 2 sends, 1 probe) and "disabled then enabled" (must give the disabled reason, 1 send, 0 probes).
  - Before the fix both failed, each giving the other subtest's reason. After it, both pass.
- Fix: newCommandClient now returns `commandClient{ipc.Client; daemonEnabled bool}`, built from the single daemonClientState read. commandStatusSources passes client.daemonEnabled to fetchDaemonStatus and no longer reads state.bin.
- The value is still assignable to ipc.Client, so buildCommandMCPProxy, doctor.go and the fetchDaemonStatus call in TestStatus_CallDeadlineExpiryStillSaysSilent compile unchanged.
- daemon_disabled_clients_test.go's constructor map wraps newCommandClient in a closure because of the new return type. No assertion changed.

Changes to criteria: none. No check was weakened or skipped, no golden was regenerated, no bound was widened, and no new numeric constant was added. The new row's expected send and probe counts come from fetchDaemonStatus's existing resend rule.

Run patterns quoted below match real tests exactly. `-v` showed 3 top-level PASS lines for that pattern, and `go vet` compiled all three test files for GOOS windows, linux and darwin.

### Commits

- 8ae9c514 test(cli): dial the refused probe pipe with no deadline
- 4bdb4acd fix(cli): give the status reason the client's own state

### Tests

- `go test -p 2 -count=1 -run '^TestStatus_ReasonUsesTheStateTheClientWasBuiltWith$' ./internal/cli/ (before the fix)`: FAIL as intended: both subtests gave the other subtest's reason (disabled vs connect miss)
- `go test -count=1 -v -run '^TestZZScratchStarvedRefusedDial$' ./internal/cli/ (throwaway test, deleted, not committed)`: err=i/o timeout isTimeout=true: an expired budget turns the refused dial into ErrTimeout (nit 1 failure mode)
- `go test -p 2 -count=50 -run '^(TestStatus_ReasonUsesTheStateTheClientWasBuiltWith|TestStatusProbe_OutlastsAListenerThatIsReArming|TestDaemonClients_HonourDisabledDaemonOverStaleState)$' ./internal/cli/`: ok (17.4s)
- `go test -p 2 -race -count=5 -run '^(TestStatus_ReasonUsesTheStateTheClientWasBuiltWith|TestStatusProbe_OutlastsAListenerThatIsReArming|TestDaemonClients_HonourDisabledDaemonOverStaleState)$' ./internal/cli/`: ok (5.7s)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/`: ok (95.0s), exit 0
- `GOOS=windows|linux|darwin go vet ./internal/cli/`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/`: clean
- `go run ./tools/devtool fmt-check`: clean, after gofumpt reflowed the new table literal in status_connect_test.go
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers (387 plan documents)

### Open issues

- commandStatusSources still takes a config.Config it no longer reads (named `_`, with a doc note). It stays because doctor.go:1333, owned by a wave 20 seat, passes s.cfg. Removing the parameter is a one-token change at that call site, for whoever next edits doctor.go.
- Remaining corner case, not fixed because it is in internal/ipc, outside this seat's files: ipc.NewClientWithOptions re-reads state.bin when it is handed an all-zero State. daemonClientState returns zero only for a state.bin with a valid CRC that encodes all-zero fields, which StateFromConfig never writes. Only in that case could the client's effective DaemonEnabled differ from commandClient.daemonEnabled, and only if state.bin also changed in between. A full fix would have the ipc client report the State it actually used.
- Observation, not a flake: in the same probe row, the first DialPipe against the listener that never calls Accept still asserts ErrTimeout. A starved thread can time out before its first CreateFile and still pass. That check never fails spuriously, but on such a pass it does not prove the dial met ERROR_PIPE_BUSY. The row's own doc comment already scopes what each half proves.

## review:cmdconnect:0:r1: verdict `sound`, 0 finding(s)


## verify:cmdconnect: verdict `sound`, 0 finding(s)



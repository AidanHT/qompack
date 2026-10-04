# Wave 19b cmdconnect: a command client's own connect budget (C4.5, D60(e))

Branch `closeout/w19b-cmdconnect`. Workflow `wf_4e6db3de-c66`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:cmdconnect: status `done`, head `089c87b2`

### Root cause

I confirmed the chain in the code. newCommandClient (internal/cli/qompack_commands.go) built its ipc client without a ConnectDeadline. ipc.NewClientWithOptions then falls back to State.ConnectDeadlineMs, the hooks' hot-path budget, which is 25 ms on Windows and buys about three named-pipe CreateFile attempts. A dial that lands while the listener is re-arming its next instance (ERROR_PIPE_BUSY) can miss that budget. When client.connect fails, Send returns through spoolAndReturn as OK=false with an empty Err. That is the same signal the client gives for a reply-deadline expiry, so fetchDaemonStatus, having seen daemonListening() true, mapped every such answer to statusSilentDaemonReason, which quotes the 10s commandCallDeadline.

Second, separate defect: SessionRegistry.evictLocked (internal/daemon/registry.go) used a strict '<' on EndedTS while ranging over a map, so a tie went to whichever session Go's randomized iteration order reached first.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go and internal/daemon/registry.go).

**1. Command-client connect misses in status (C4.5, D53(a), per D60(e))**
- **Connect budget.** newCommandClient now sets `ConnectDeadline: commandConnectDeadline`, and `commandConnectDeadline = hookConnectDeadlineFloor` (250 ms). That is the existing derived floor for non-hot-path dials, already used by session-start, checkpoint, flush and self-test's admin.ping, so this adds no new number. Every frontend built on newCommandClient gets it: status, doctor's status rows, recall, why and dropped. Hooks keep runtime.daemon.connectDeadlineMs unchanged.
- **Out of scope by choice.** `qompack mcp` (newMCPClient) was left alone. It is a long-lived server, not a one-shot command, and it already retries 10 times.
- **Telling the failures apart.** fetchDaemonStatus now times each send. The client's read deadline starts only after the connect, and the connect is bounded by a budget far inside commandCallDeadline. So:
  - A send that took commandCallDeadline or longer means the deadline expired. It keeps statusSilentDaemonReason and is not retried.
  - A send that failed sooner, with the listener alive and ctx live, is sent once more. If that fails too, the reason is the new statusConnectMissReason. It names a connect miss within the 250ms connect budget, or a connection that closed before a reply, and never quotes the call deadline. The client reports a connect miss and a dropped connection the same way, so the text names both.
- **Why one retry.** A status read is idempotent. go-winio's dial returns any CreateFile error other than ERROR_PIPE_BUSY immediately, so no dial budget can absorb that kind of miss.
- **Test seam.** `newCommandIPCClient` is a variable so a test can make a real ipc client miss its connect, by aiming it at an address nothing listens on, while a real daemon listens at the project's own address.
- **Order row fixture.** The w19 order row (TestStatus_RepeatedReadsOfUnchangedStateAgree) no longer needs its 250 ms connectDeadlineMs config override. I removed it, so the row now runs on the shipped configuration. It passed -count=50 on Windows.
- **Docs.** docs/troubleshooting.md, in the `qompack status` section, now states both reason texts, the single retry, and that commands have their own 250 ms connect budget, separate from connectDeadlineMs. The generated docs are unchanged; all three generators report up to date.

**2. Eviction tie-break**
evictLocked now breaks an EndedTS tie by the smallest session id: `|| (s.EndedTS == oldestEnded && id < oldestID)`.

**Red first** (on the base, with only the seam and the new constants added, no behaviour change):
- TestCommandClient_HasItsOwnConnectBudget failed: expected 250ms, got 0s.
- TestStatus_ConnectMissNamesTheConnectBudget failed: the reason contained 'did not answer within 10s', from a real daemon in a temp project, in 0.14s.
- TestStatus_TransientConnectMissStillReadsTheDaemon failed: read 1 had source none, so the two reads disagreed.
- TestRegistryEvictionBreaksAnEndedTSTieBySessionID failed at trial 1.
- TestStatus_CallDeadlineExpiryStillSaysSilent is a guard row. It passes on the base and on head; it waits out the real 10s deadline once and also checks that the handler was called exactly once. TestStatusSource_ASilentDaemonIsNotReportedAbsent still passes unchanged: the new reason legitimately contains 'listening' and 'did not answer'.

All checks passed (each exit 0):
- go vet, on Windows and GOOS=linux
- pinned golangci-lint on ./internal/cli/... and ./internal/daemon/...
- devtool fmt-check
- devtool lint --only=docmarkers,runpatterns (PASS both)
- test/docs
- gen-config-docs, gen-command-docs and gen-mcp-docs --check
- the full internal/cli package (103s) and the full internal/daemon package (363s), both with -p 2

Changed files:
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/qompack_commands.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/status_connect_test.go (new)
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/status_order_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/daemon/registry.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/daemon/registry_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/docs/troubleshooting.md

### Commits

- 3b2569c9 fix(daemon): break an eviction tie by session id
- 089c87b2 fix(cli): give command clients their own connect budget

### Tests

- `go test -p 2 -count=1 -run "^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestStatus_CallDeadlineExpiryStillSaysSilent)$" ./internal/cli/ (base 738d67c7 + seam/constants only, no behaviour change)`: FAIL 3 of 4: TestCommandClient_HasItsOwnConnectBudget (expected 250ms, actual 0s); TestStatus_ConnectMissNamesTheConnectBudget (reason contains 'did not answer within 10s' after 0.14s); TestStatus_TransientConnectMissStillReadsTheDaemon (read 1 source none). TestStatus_CallDeadlineExpiryStillSaysSilent passed: it is a guard row
- `go test -p 2 -count=3 -run "^TestRegistryEvictionBreaksAnEndedTSTieBySessionID$" ./internal/daemon/ (base registry.go)`: FAIL: trial 1, 'the smallest id (a) must be the one evicted'
- `go test -p 2 -count=1 -v -run "^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_RepeatedReadsOfUnchangedStateAgree)$" ./internal/cli/ (head)`: ok: all 7 PASS (call-deadline row 10.03s)
- `go test -p 2 -count=20 -run "^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon)$" ./internal/cli/`: ok 11.0s
- `go test -p 2 -count=20 -run "^TestRegistryEvictionBreaksAnEndedTSTieBySessionID$" ./internal/daemon/`: ok
- `go test -p 2 -count=50 -run "^TestStatus_RepeatedReadsOfUnchangedStateAgree$" ./internal/cli/ (connectDeadlineMs override removed, shipped config)`: ok 75.7s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/...`: ok 103.0s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/...`: ok 362.8s
- `go test -p 2 -count=1 ./test/docs`: ok
- `go vet ./internal/cli ./internal/daemon && GOOS=linux go vet ./internal/cli ./internal/daemon`: exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... ./internal/daemon/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 each, all up to date

### Criterion changes

- C4.5 / D53(a): when a daemon is listening, status now has three distinct outcomes. A send that ran out the call deadline reports statusSilentDaemonReason ('did not answer within 10s') and is not retried. A send that failed earlier is sent once more; if it fails again, status reports statusConnectMissReason, which names the 250ms connect budget or a connection closed before a reply. If the retry succeeds, the read comes from the live daemon. Pinned by TestStatus_ConnectMissNamesTheConnectBudget, TestStatus_TransientConnectMissStillReadsTheDaemon and TestStatus_CallDeadlineExpiryStillSaysSilent.
- D60(e): command clients dial with commandConnectDeadline (= hookConnectDeadlineFloor, 250 ms), not runtime.daemon.connectDeadlineMs. Pinned by TestCommandClient_HasItsOwnConnectBudget, which also checks commandConnectDeadline < commandCallDeadline because the classification depends on it.
- TestStatus_RepeatedReadsOfUnchangedStateAgree no longer writes a connectDeadlineMs=250 project config. It runs on the shipped configuration, so it now also guards the connect budget.
- SessionRegistry eviction is deterministic: an EndedTS tie goes to the smallest session id. Pinned by TestRegistryEvictionBreaksAnEndedTSTieBySessionID.

### Open issues

- ipc.Client returns OK=false with no text both for a connect miss and for a connection dropped before the reply, so statusConnectMissReason has to name both. An exact reason would need ipc to expose which stage failed. That is an internal/ipc change, outside this seat's files.
- Pre-existing, not changed: when the listener is alive but a command client's connect misses, ipc's lazySpawn still claims spawn.lock and spawns a second daemon (when Self is set). That daemon then loses the daemon lock and exits. The larger budget makes this rarer; the retry adds no second spawn because spawnOnce applies per client.
- `qompack mcp` (newMCPClient) still dials with runtime.daemon.connectDeadlineMs. It is a long-lived server with a 10-attempt retry loop, not a one-shot command, so I left it out of D60(e). The coordinator may want to rule on whether D60(e) covers it.
- docs/troubleshooting.md was edited in the `qompack status` paragraph that w19-statusorder also touched. If another seat edits the same paragraph, merge hunk by hunk with git merge-file, not checkout --theirs.
- The live candidate 8 re-check (status --json read twice with two or more sessions on a real Windows host) is still what closes C4.5 on a real host.

### Needs owner

- No new product number. commandConnectDeadline reuses hookConnectDeadlineFloor (250 ms, derived in internal/cli/hookclient.go). This does change which budget one-shot commands dial with, from runtime.daemon.connectDeadlineMs (5 ms, or 25 ms on Windows) to 250 ms, as D60(e) directs; the owner may want to confirm the reuse.
- The single bounded retry of a status send that failed before the call deadline while the listener was alive was my decision, which D60(e) left to this seat. Evidence: go-winio returns non-PIPE_BUSY CreateFile errors immediately, so no budget absorbs them; a status read is idempotent; an expired call deadline is never retried (guarded by a handler call-count of 1).

## review:cmdconnect:all: verdict `sound`, 2 finding(s)

- **minor** `internal/cli/qompack_commands.go:302-312 (fetchDaemonStatus retry gate) with statusProbeTimeout at :270 (= selfTestProbeTimeout, 50ms, selftest.go:34)`: There is still a low-probability route where two reads of unchanged state disagree and the reason text is false. It goes through the liveness probe, not the send. The retry only runs when `wasListening` is true. If daemonListening's 50 ms probe misses on a live daemon (an instant CreateFile failure while the listener re-arms, or ERROR_PIPE_BUSY past 50 ms) and the single send then also misses, status reports statusNoDaemonReason ('none is listening for this project yet') with source none. It does not retry. The send's lazySpawn also claims spawn.lock, so a duplicate daemon starts and exits. This takes two misses in a row, which is why it is minor. Still, it is the same class of failure as C4.5: a millisecond connect miss reported as a false reason.
  - Evidence: The gate is `if err == nil && noAnswer(resp) && wasListening && took < commandCallDeadline && ctx.Err() == nil { resend }`. Then `case !wasListening: return statusNoDaemonReason`. The probe dials with statusProbeTimeout = selfTestProbeTimeout = 50ms, below commandConnectDeadline (250ms), so the probe is now the weakest dial on the status path. The implementer's own evidence is that go-winio returns non-PIPE_BUSY CreateFile errors at once, and no budget can absorb those. That applies to the probe as much as to the send. The w19-statusorder report lists 'retry a no-answer once when the listener was alive' as the remedy, which leaves the probe-miss case open.
  - Fix: The cheapest fix is to give the probe the same budget: `statusProbeTimeout = commandConnectDeadline`. This reuses the existing constant, and an absent daemon still fails at once on a missing pipe or socket. Alternatively, when `!wasListening` and the send failed fast, probe once more before choosing statusNoDaemonReason, and pick the reason from that. Add a row that injects a probe miss followed by one send miss, using a seam on daemonListening. Or record this under open_issues for the candidate 8 live re-check.
- **nit** `internal/cli/status_connect_test.go:49-66 (injectCommandConnectMisses)`: The injected miss aims a client at an address with no pipe, so the dial fails instantly with 'not found'. It never times out inside the connect budget. The rows therefore cover the retry and the reason classification. Nothing exercises the 250 ms budget's behaviour: TestCommandClient_HasItsOwnConnectBudget only checks the option value. The claim in statusConnectMissReason that the connection was not made 'within the 250ms connect budget' is pinned only for an instant miss.
  - Evidence: `nowhere, err := ipc.Resolve(t.TempDir())`, and the miss client is built with `prev(nowhere, ...)`. On Windows, winio.DialPipe on a nonexistent name returns ERROR_FILE_NOT_FOUND without waiting. The assertion on ConnectDeadline is `require.Equal(t, commandConnectDeadline, (*built)[0].ConnectDeadline)`.
  - Fix: Optional. Add a row that holds the daemon's only pipe instance busy, or points the client at a listener that never accepts, so the dial waits out commandConnectDeadline. Assert that the send returns in roughly 250ms plus one dialBusyRetryQuantum, well under commandCallDeadline, and that the reason is statusConnectMissReason. Otherwise, note in the test comment that budget expiry is covered only by the option-value row.

## fix:cmdconnect: status `done`, head `b320f3b6`

### Root cause

The implementer's chain holds, and I confirmed it in the code. newCommandClient built its ipc client with no ConnectDeadline, so it fell back to State.ConnectDeadlineMs, the hooks' 25 ms Windows budget. A missed connect comes back through spoolAndReturn as OK=false with an empty Err. fetchDaemonStatus had already seen the daemon listening, so it mapped that answer to statusSilentDaemonReason, which says 'did not answer within 10s'. Separately, SessionRegistry.evictLocked broke EndedTS ties in Go's random map order.

The reviewer found a residual route, and it is real. fetchDaemonStatus resends a fast failure only when daemonListening saw a listener first. That probe dialed with statusProbeTimeout = selfTestProbeTimeout (50 ms), well below the 250 ms commandConnectDeadline, so after 089c87b2 the probe was the weakest dial on the status path. Here is how a miss happens: go-winio keeps the pipe name alive through a first handle that is never connected, so between instances CreateFile returns ERROR_PIPE_BUSY until the daemon's next Accept (go-winio@v0.6.2 pipe.go, makeServerPipeHandle 'initially disconnected state' and tryDialPipe). If that wait runs past 50 ms on a live daemon and the following send also misses, status reports statusNoDaemonReason ('none is listening') and never resends. That is the same false, millisecond reason C4.5 is about.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go: statusProbeTimeout, in this fix round; the implementer's two commits changed internal/cli/qompack_commands.go and internal/daemon/registry.go).

## Review resolution
**Finding 1 (minor): the liveness probe's 50 ms budget leaves a route to a false 'none is listening'. CONFIRMED and FIXED in b320f3b6.**
- **Checked independently.**
  - The retry runs only when `wasListening` is true, and `case !wasListening` returns statusNoDaemonReason.
  - daemonListening dialed with statusProbeTimeout = selfTestProbeTimeout = 50 ms (selftest.go:34), below commandConnectDeadline (250 ms).
  - In go-winio v0.6.2, ListenPipe's first handle is created 'initially disconnected', so a live listener keeps its pipe name between Accepts. A dial in that window gets ERROR_PIPE_BUSY and retries every 10 ms until its budget runs out. A 50 ms probe gives up on a daemon whose accept loop takes longer than about 60 ms to re-arm.
- **Red first.** Both new rows failed on 089c87b2:
  - TestStatusProbe_HasTheCommandConnectBudget: expected 250ms, got 50ms.
  - TestStatusProbe_OutlastsAListenerThatIsReArming (Windows only): a real go-winio listener at the project's address calls its first Accept 100 ms after the probe starts, and daemonListening returned false.
- **Fix.** `statusProbeTimeout = commandConnectDeadline`, the reviewer's preferred option. It reuses an existing constant, so there is no new product number. An absent daemon still fails the probe at once: a missing pipe gives ERROR_FILE_NOT_FOUND, and a missing or stale Unix socket gives ENOENT or ECONNREFUSED. So no-daemon status pays nothing extra.
- **Alternative not taken: probe again after a fast send failure.** fetchDaemonStatus deliberately probes before the send. A probe after it could see the daemon that the send's own lazy spawn started, and would then report a truly absent daemon as a connect miss.
- **Docs.** docs/troubleshooting.md, `qompack status` section: one clause saying status checks for a listening daemon within the same 250 ms.

**Correction to the implementer's evidence for the retry.** The implementer said 'go-winio returns non-PIPE_BUSY CreateFile errors at once, so no budget absorbs them'. That is true of the dial loop. But on a live go-winio listener the name persists, so the miss between instances is ERROR_PIPE_BUSY, which a budget can absorb. The single retry still holds up, because a status read changes nothing and three cases remain:
- a re-arm window longer than 250 ms on a loaded host;
- a connection dropped before the reply, which the client reports the same way as a connect miss;
- on Unix, a connect refused at once (EAGAIN) when the listen backlog is full.

## Implementer's work (verified, unchanged)
- **3b2569c9.** evictLocked breaks an EndedTS tie by the smallest session id. Pinned by TestRegistryEvictionBreaksAnEndedTSTieBySessionID.
- **089c87b2.**
  - Command clients dial with commandConnectDeadline = hookConnectDeadlineFloor (250 ms), per D60(e).
  - fetchDaemonStatus times each send. A send that took the full commandCallDeadline keeps statusSilentDaemonReason and is never retried. A send that failed sooner, while the daemon was listening, is sent once more. If that also fails, status reports statusConnectMissReason.
  - The newCommandIPCClient seam, four rows in internal/cli/status_connect_test.go, the order row now running on the shipped configuration, and the docs paragraph.

## Files changed in this round
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/qompack_commands.go (statusProbeTimeout and its comment)
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/status_connect_test.go (TestStatusProbe_HasTheCommandConnectBudget)
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/internal/cli/status_probe_windows_test.go (new: TestStatusProbe_OutlastsAListenerThatIsReArming)
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19b-cmdconnect/docs/troubleshooting.md

## Checks after the fix, all exit 0
- go vet on ./internal/cli, Windows and GOOS=linux
- pinned golangci-lint on ./internal/cli/...
- devtool fmt-check
- devtool lint --only=docmarkers,runpatterns
- test/docs
- gen-config-docs, gen-command-docs and gen-mcp-docs --check
- full internal/cli package (103.7s)
- status rows at -count=20

internal/daemon is untouched in this round; the implementer's full run of it passed at 089c87b2.

### Commits

- 3b2569c9 fix(daemon): break an eviction tie by session id
- 089c87b2 fix(cli): give command clients their own connect budget
- b320f3b6 fix(cli): give the status liveness probe the connect budget

### Tests

- `go test -p 2 -count=1 -run "^(TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatusProbe_HasTheCommandConnectBudget)$" ./internal/cli/ (on 089c87b2 plus only the two new rows)`: FAIL 2 of 2: TestStatusProbe_HasTheCommandConnectBudget (expected 250ms, actual 50ms); TestStatusProbe_OutlastsAListenerThatIsReArming (daemonListening false for a live listener that called Accept 100ms after the probe started)
- `go test -p 2 -count=1 -run "^(TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatusProbe_HasTheCommandConnectBudget)$" ./internal/cli/ (head b320f3b6)`: ok 0.514s
- `go test -p 2 -count=20 -run "^(TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatusProbe_HasTheCommandConnectBudget|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon)$" ./internal/cli/`: ok 13.9s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/...`: ok 103.7s (includes TestStatus_CallDeadlineExpiryStillSaysSilent and TestStatus_RepeatedReadsOfUnchangedStateAgree)
- `go test -p 2 -count=1 ./test/docs`: ok 3.9s
- `go vet ./internal/cli && GOOS=linux go vet ./internal/cli`: exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 each
- `(implementer, 089c87b2) go test -p 2 -count=1 -timeout=30m ./internal/daemon/...`: ok 362.8s; internal/daemon not touched in this round

### Criterion changes

- C4.5 / D53(a): status's liveness probe (daemonListening) now dials with commandConnectDeadline (250 ms), not self-test's 50 ms bound. A probe miss on a live daemon that is re-arming its listener no longer becomes 'none is listening' with no resend. Pinned by TestStatusProbe_HasTheCommandConnectBudget, and on Windows by TestStatusProbe_OutlastsAListenerThatIsReArming.
- Carried from the implementer: with a daemon listening, status has three outcomes. If the send ran out the call deadline, it reports statusSilentDaemonReason and does not retry. If the send failed sooner, it is sent once more; if that also fails, status reports statusConnectMissReason. If the retry succeeds, the read comes from the live daemon. Pinned by TestStatus_ConnectMissNamesTheConnectBudget, TestStatus_TransientConnectMissStillReadsTheDaemon and TestStatus_CallDeadlineExpiryStillSaysSilent.
- Carried: D60(e). Command clients dial with commandConnectDeadline (= hookConnectDeadlineFloor, 250 ms). Pinned by TestCommandClient_HasItsOwnConnectBudget.
- Carried: TestStatus_RepeatedReadsOfUnchangedStateAgree runs on the shipped configuration, without the connectDeadlineMs=250 override.
- Carried: SessionRegistry eviction breaks an EndedTS tie by the smallest session id. Pinned by TestRegistryEvictionBreaksAnEndedTSTieBySessionID.

### Open issues

- TestStatusProbe_OutlastsAListenerThatIsReArming depends on timing by construction. Its listener calls Accept 100 ms after the probe starts (2 x selfTestProbeTimeout), against a 250 ms probe budget, so it has a 150 ms margin. It passed -count=20 on an idle Windows host. Under the nightly co-load or -race, a goroutine stalled more than 150 ms could fail it, and the result would be a false red. It is not a pass criterion here; the constant row TestStatusProbe_HasTheCommandConnectBudget is.
- Carried: ipc.Client returns OK=false with no text both for a connect miss and for a connection dropped before the reply, so statusConnectMissReason has to name both. An exact reason would need an internal/ipc change, outside this seat's files.
- Carried, pre-existing: when the listener is alive but a command client's connect misses, ipc's lazySpawn still claims spawn.lock and spawns a second daemon (when Self is set), and that daemon loses the daemon lock and exits. The larger probe and connect budgets make this rarer, but it is not fixed.
- Carried: `qompack mcp` (newMCPClient) still dials with runtime.daemon.connectDeadlineMs. It is a long-lived server with a 10-attempt retry loop, so the implementer left it out of D60(e). The coordinator may want to rule on whether D60(e) covers it.
- Carried: docs/troubleshooting.md's `qompack status` paragraph is also touched by w19-statusorder. Merge it hunk by hunk with git merge-file, not checkout --theirs.
- Carried: the live candidate 8 re-check (status --json read twice with two or more sessions on a real Windows host) is still what closes C4.5 on a real host.

### Needs owner

- No new product number. commandConnectDeadline reuses hookConnectDeadlineFloor (250 ms, derived in internal/cli/hookclient.go), and statusProbeTimeout now reuses commandConnectDeadline. The probe's budget moves from 50 ms to 250 ms; an absent daemon still fails at once. The owner may want to confirm both reuses.
- The single bounded retry of a status send that failed before the call deadline while a listener was alive was the implementer's decision under D60(e). The evidence is corrected in this round. On a live go-winio listener a miss between instances is ERROR_PIPE_BUSY, which a budget can absorb, so the retry is justified instead by three cases: a re-arm window longer than 250 ms on a loaded host, a connection dropped before the reply, and on Unix a connect refused at once (EAGAIN) when the backlog is full. A status read changes nothing, and an expired call deadline is never retried (a handler call count of 1 guards it).

## verify:cmdconnect: verdict `sound`, 0 finding(s)



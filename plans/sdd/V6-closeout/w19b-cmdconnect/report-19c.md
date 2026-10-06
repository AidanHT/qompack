# Wave 19c cmdconnect: deterministic probe and connect-budget rows (C4.5, D61(c))

Branch `closeout/w19b-cmdconnect`. Workflow `wf_558a3480-754`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:cmdconnect: status `done`, head `05f38db47ea73c19bbbe5e8b562ea8dc948cd7d0`

### Root cause

Two of the rows this branch added got their pass/fail from wall-clock time. (1) TestStatusProbe_OutlastsAListenerThatIsReArming passed only if a goroutine reached ln.Accept() within 100 ms of the probe starting, against a 250 ms budget. That is a 150 ms scheduling margin, so it fails on a loaded host. (2) fetchDaemonStatus timed each send with time.Now/time.Since. That made the resend decision and the connect-miss reason (took < commandCallDeadline) depend on a real send returning within 10 s, and TestStatus_ConnectMissNamesTheConnectBudget and TestStatus_TransientConnectMissStillReadsTheDaemon inherited that margin. ConnectMissNamesTheConnectBudget also needed a real daemon only so the real probe would succeed within 250 ms. A scratch check during the work (no file kept) confirmed the premise the probe row depends on: a go-winio v0.6.2 listener with no Accept pending holds its pipe name, so a dial against it times out with i/o timeout (busy) and is not refused.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go: two test seams and one comment; production behaviour is unchanged)

All four items are done. One residual is listed under open issues: three end-to-end rows still need the real transport to connect within the product's own budgets.

**What changed in the product file**
- `statusProbeDial = ipc.Probe` is now the dial `daemonListening` makes.
- `statusSendClock = core.SystemClock()` now times `fetchDaemonStatus`'s sends, in place of time.Now/time.Since.
- The `newCommandIPCClient` comment now describes what the seam is for.
- The `commandConnectDeadline` comment records ruling D61(e): `qompack mcp` is a long-lived server, so it keeps runtime.daemon.connectDeadlineMs and its own retry loop (mcpRetryAttempts tries, mcpRetryDelay apart, cmd_mcp.go).

**(1) The probe row is rewritten** (internal/cli/status_probe_windows_test.go, same test name). It proves the same two things without racing a listener against a budget:
- On a real go-winio listener that never calls Accept, a dial waits until its budget runs out and fails with winio.ErrTimeout, which shows the busy window is real. A missing pipe is refused with os.ErrNotExist instead. `ipc.Probe` at the old 50 ms bound reads the live listener as absent. None of these outcomes depends on scheduling.
- Through the `statusProbeDial` seam, a listener that re-arms at 2×selfTestProbeTimeout (100 ms) reads as listening under statusProbeTimeout. The row also checks that the probe dials the project's own address, once.

**The other rows on this branch, checked the same way**
- `TestStatus_ConnectMissNamesTheConnectBudget` no longer starts a daemon. The probe seam reports a listener, the send clock is a frozen stepClock, and the misses are still the real ipc client's answer for a missing address. The miss count is now exact: both sends must have missed (it was "fewer than").
- `TestStatus_TransientConnectMissStillReadsTheDaemon` keeps its real daemon. The send clock and probe are now seams, so the resend no longer depends on how fast the real miss returns.
- `TestStatus_CallDeadlineExpiryStillSaysSilent` is left real, on purpose. It waits out the real 10 s deadline once, and its verdict needs no margin: on the monotonic clock the send cannot take less than the read deadline. Only its comment changed.
- `TestCommandClient_HasItsOwnConnectBudget`, `TestStatusProbe_HasTheCommandConnectBudget` and the daemon row `TestRegistryEvictionBreaksAnEndedTSTieBySessionID` (commit 3b2569c9) do no real I/O and have no timing dependence.

**(2) New row `TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon`.** `lateConnectClient` models a listener that accepts at readyAt = halfway between the hot-path connectDeadlineMs and commandConnectDeadline. Time moves only on a stepClock. It has three cases:
- A command client reads the daemon with one send.
- The same listener dialed with the hot-path budget (how command clients dialed before D60(e)) misses.
- A listener still busy past commandConnectDeadline misses.

Both missing cases report statusConnectMissReason. They make exactly two sends, the clock moves exactly 2×budget, and they never report the 10 s reason or "none is listening". Red check: with the pre-fix budgets (no ConnectDeadline on the command client, statusProbeTimeout = selfTestProbeTimeout), this row and the probe row both FAIL. They pass on HEAD.

**(3)** Done in the comment above.

**(4)** docs/troubleshooting.md, `qompack status` section: one known-limit sentence after the connect-budget paragraph. It says that when the dial made by `status` (or another frontend that may start a daemon; doctor never does) to a running daemon misses its connect budget, the command also asks a daemon to start; the second daemon finds the running one's lock and exits, and nothing is lost (`ipc.client.lazySpawn`; `daemon.Run` returns nil on ErrLockHeld). The session-order paragraph already in the section is untouched; only that one hunk was added.

**Checks, all exit 0**
- go vet ./internal/cli for windows, linux and darwin.
- Pinned golangci-lint on ./internal/cli/..., run on Windows and again with GOOS=linux analysis.
- devtool fmt-check; devtool lint --only=docmarkers,runpatterns (PASS both).
- go test ./test/docs; gen-config-docs, gen-command-docs and gen-mcp-docs --check.
- The branch's rows at -count=20 and -race -count=3 on Windows and on Linux.
- Full ./internal/cli/... once on Windows and once on Linux.

Linux ran in qompack-v6-linux-verification as qompack-test (uid 10001), -p 2, with GOPROXY=off. I started the container because it was stopped, then removed my /work copy and stopped it again. One scratch Windows test of mine hung (its Accept had no client). I stopped only that process, my own cli.test.exe PID 64572, and the scratch file was removed. No criterion was changed and no new number was introduced.

### Commits

- 452b779f fix(cli): make the command-connect rows deterministic
- 05f38db4 docs(troubleshooting): record the lazy-spawn duplicate limit

### Tests

- `go test -p 2 -count=20 -timeout=30m -run '^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon|TestStatusProbe_HasTheCommandConnectBudget|TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree)$' -v ./internal/cli/ (Windows)`: ok 319.0s, 160/160 top-level PASS
- `go test -p 2 -race -count=3 -timeout=30m -run '^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon|TestStatusProbe_HasTheCommandConnectBudget|TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree)$' -v ./internal/cli/ (Windows)`: ok 67.1s, 24/24 PASS, no DATA RACE
- `go test -p 2 -count=1 -run '^(TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon|TestStatusProbe_OutlastsAListenerThatIsReArming)$' ./internal/cli/ (mutation: newCommandClient without ConnectDeadline, statusProbeTimeout = selfTestProbeTimeout)`: FAIL as expected: the late-connect row fails its 'command budget, accepted after the hot path's' and 'busy past it' subtests, and the probe row fails; on HEAD both PASS
- `go test -p 2 -count=20 -run '^TestRegistryEvictionBreaksAnEndedTSTieBySessionID$' ./internal/daemon/ && go test -p 2 -race -count=3 -run '^TestRegistryEvictionBreaksAnEndedTSTieBySessionID$' ./internal/daemon/`: ok / ok (pure row, no I/O)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/... (Windows)`: ok 184.4s
- `linux container, user qompack-test: go test -p 2 -count=20 -timeout=30m -run "^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon|TestStatusProbe_HasTheCommandConnectBudget|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree)$" ./internal/cli/`: ok 346.6s (the Windows-only probe row is excluded by its build constraint)
- `linux container, user qompack-test: go test -p 2 -race -count=3 -timeout=30m -run "^(TestCommandClient_HasItsOwnConnectBudget|TestStatus_ConnectMissNamesTheConnectBudget|TestStatus_TransientConnectMissStillReadsTheDaemon|TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon|TestStatusProbe_HasTheCommandConnectBudget|TestStatus_CallDeadlineExpiryStillSaysSilent|TestStatus_RepeatedReadsOfUnchangedStateAgree)$" ./internal/cli/`: exit 0, ok 39.4s
- `linux container, user qompack-test: go test -p 2 -count=1 -timeout=30m ./internal/cli/...`: exit 0, ok 140.5s
- `go vet ./internal/cli/ && GOOS=linux go vet ./internal/cli/ && GOOS=darwin go vet ./internal/cli/`: exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... (plus the same binary with GOOS=linux)`: exit 0 both
- `go run ./tools/devtool fmt-check; go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0; PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: ok 14.0s; all three up to date

### Criterion changes

- TestStatus_ConnectMissNamesTheConnectBudget: the miss-count assertion is stronger. It now requires both of status's sends to have missed (counter == 1<<30-2); it used to require only fewer than 1<<30. The row no longer starts a daemon, because the listener the probe sees is the statusProbeDial seam. What it proves is unchanged: with a daemon listening and every connect missing, the reason names the connect budget and not the 10 s deadline.
- TestStatusProbe_OutlastsAListenerThatIsReArming: the timed Accept-after-100ms race is replaced by a deterministic real-listener busy check (winio.ErrTimeout compared with os.ErrNotExist, and ipc.Probe at 50 ms reading false) plus a seam row in which a 100 ms re-arm reads as listening under statusProbeTimeout. The claim is the same; the outcome no longer depends on scheduling.

### Open issues

- Three end-to-end rows still need the real transport, by design: TestStatus_TransientConnectMissStillReadsTheDaemon (two real sends), TestStatus_RepeatedReadsOfUnchangedStateAgree (60 reads, each a real probe plus a real send; this branch removed its connectDeadlineMs override, so its budget comes from commandConnectDeadline, the same 250 ms) and TestStatus_CallDeadlineExpiryStillSaysSilent (one real probe and one real connect). Each needs a real listener to re-arm within the product's own 250 ms connect budget. This is not a margin the rows designed, but a host stalled for more than 250 ms could still fail them. They passed -count=20 on Windows and Linux under daytime load. Removing this residual would need a dial seam inside internal/ipc, which this seat does not own.
- Known limit now documented: when the dial status makes to a running daemon misses its connect budget, its client still calls lazySpawn. The duplicate daemon finds the lock and exits. Not fixed, per D61(e).
- Carried from statusorder: the C4.5 row in docs/uat.md still records the candidate 7 FAIL. The candidate 8 live rerun must read status --json twice with two or more sessions to close it on a real host.

## review:cmdconnect:all:r1: verdict `needs-fixes`, 3 finding(s)

- **minor** `internal/cli/qompack_commands.go:315-352 (fetchDaemonStatus) and :238-247 (commandStatusSources)`: Lens (b): the reason text is false on one reachable path. When runtime.daemon.enabled is false but a daemon is still listening (the config was changed while it ran), daemonListening reports a listener, but the client never dials: ipc.Client.Send step 2 spools and returns {OK:false} with no text, in about 0 ms. fetchDaemonStatus then resends and reports statusConnectMissReason, which says no connection was made within the 250ms connect budget or the connection closed before a reply. Neither happened: no dial was attempted. This came in with 089c87b2, in this workstream rather than this round. Before that commit the same path claimed the 10s silent reason, which was also false. Nothing on the branch covers it.
  - Evidence: Read-only overlay probe (scratch zz_review_probe_test.go via -overlay): bootstrapProject + bootstrapDaemon, then writeProjectConfig(`{"runtime":{"daemon":{"enabled":false}}}`), then `status --json` -> source=none reason="daemon: a daemon is listening for this project but did not answer this command: on both of two attempts, no connection to it was made within the 250ms connect budget or the connection closed before a reply...". Path: newCommandClient -> daemonClientState (DaemonEnabled = state && cfg.Runtime.Daemon.Enabled) -> client.go:208-210 `if !c.o.State.DaemonEnabled { return c.spoolAndReturn(req) }`.
  - Fix: Pass the client's effective DaemonEnabled (daemonClientState(root, cfg).DaemonEnabled) into commandStatusSources/fetchDaemonStatus. When it is false, return a distinct reason without probing or resending, for example: "runtime.daemon.enabled is false for this project, so this command does not ask the daemon". Add a row: a seamed probe that sees a listener, enabled=false, the new reason, one send, and neither the connect-miss nor the silent reason. Add one sentence to the troubleshooting status section.
- **nit** `internal/cli/status_probe_windows_test.go:58-70`: The seam half of the rewritten probe row is a tautology. accepts(d) is d >= rearm, and daemonListening always passes statusProbeTimeout, so the half reduces to statusProbeTimeout >= 2*selfTestProbeTimeout, nearly the same check as TestStatusProbe_HasTheCommandConnectBudget. The address-once assertion is the only new content. The real half shows that a busy dial times out rather than being refused. No row any longer exercises a real dial that was busy and then connects once Accept is called within budget. That relies on go-winio's 10 ms busy poll (dial_windows.go:27-38). The old row covered it only racily, so dropping it was the right call, but the comment says the row "proves both halves".
  - Evidence: `accepts := func(d time.Duration) bool { return d >= rearm }`, then `require.True(t, daemonListening(root)())`, which only checks that statusProbeTimeout >= rearm.
  - Fix: Reword the comment: the second half pins the budget relation, and the busy-to-accept wake is go-winio's poll, which no row exercises. Alternatively, add a real-listener check whose verdict cannot depend on ordering: DialPipeContext with an unbounded context in a goroutine, then ln.Accept(), asserting the dial succeeds. That proves busy dials are not abandoned, though it does not guarantee the busy branch runs every time.
- **nit** `internal/cli/status_connect_test.go:28-31 (file header)`: The header says "None of them times a real dial for its verdict (D61)", but TestStatus_TransientConnectMissStillReadsTheDaemon (its second send and second read) and TestStatus_CallDeadlineExpiryStillSaysSilent (its real probe and connect) still need a real connect inside the product's 250 ms budget to pass. The seat's open_issues says so, but the in-code comment claims more than the code does.
  - Evidence: In the Transient row, misses=1 makes the resend and the second status read go through the real command client to the real daemon. In the CallDeadline row, the real daemonListening runs against a real ipc.NewServer and the dial is real.
  - Fix: Reword to: no row's verdict rests on a margin of its own choosing; the transient-miss, call-deadline and repeated-reads rows still need the real transport to connect within commandConnectDeadline, which is the product's own budget.

## fix:cmdconnect:r1: status `done`, head `213cf617e66585a6832b219d21b75049b02d086d`

### Root cause

When runtime.daemon.enabled was false, fetchDaemonStatus could not tell. The command client's State.DaemonEnabled comes from daemonClientState, and when it is false the client never dials: ipc.Client.Send step 2 returns spoolAndReturn, which is OK false with no text, at once. That is the same answer as a missed connect. With a daemon still listening (for example, the config was changed while it ran), the probe saw the listener and status resent the read. It then reported statusConnectMissReason, which says no connection was made within the 250ms connect budget, but no dial had been made. Before 089c87b2 the same path gave the 10s silent reason, which was also false.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go and doctor.go: `qompack status` and doctor's status row now say when the daemon is disabled. Nothing else changes.)

The minor finding is fixed with a row that failed first, and both nits are fixed. Everything was checked on Windows and on Linux.

## Review resolution

**Minor: false reason when the daemon is disabled but a daemon is still listening. CONFIRMED and fixed (3675a771).**
- **Confirmed by reading the code.** `daemonClientState` sets `DaemonEnabled = state && cfg.Runtime.Daemon.Enabled`. `client.go:211` then returns `spoolAndReturn` before any dial, and `fetchDaemonStatus` treated that answer as a connect miss.
- **Failing row first.** I added `TestStatus_DaemonDisabledIsNamedNotMissed` and the reason constant, then ran the row on the old logic. It failed: "expected: 1 actual: 2" sends, meaning the read was resent.
- **What the row sets up:**
  - the real command client, wrapped only to count sends;
  - the project config `{"runtime":{"daemon":{"enabled":false}}}`;
  - a probe stand-in that reports a listener and counts its calls;
  - a stepClock.
- **What the row asserts:**
  - the client is built with `DaemonEnabled` false;
  - exactly one send and zero probes;
  - the reason contains `statusDaemonDisabledReason`;
  - the reason contains none of the connect-miss, silent or none-listening texts.
- **The fix:**
  - `commandStatusSources(ctx, root, cfg, client)` passes `daemonClientState(root, cfg).DaemonEnabled` into `fetchDaemonStatus(ctx, client, enabled, listening)`. It reads the same config the client was built with.
  - When `enabled` is false, `fetchDaemonStatus` sends once, does not probe or resend, and returns the new reason: "runtime.daemon.enabled is false for this project, so this command does not ask a daemon, even one that is still running". It still sends once, so a real answer still wins if the client's state ever disagrees.
  - Both callers are updated: `buildCommandDeps` passes `deps.Cfg` and doctor's `statusRows` passes `s.cfg`.
  - The three tests that call `fetchDaemonStatus` directly now pass `true`. Their clients use a zero State, which `NewClientWithOptions` reads from disk as enabled by default (`ReadState(root, config.Defaults())`), so nothing they check changes.
- **Docs.** One sentence was added to the `qompack status` section of docs/troubleshooting.md, quoting the new line. The statusorder and lazy-spawn text there is untouched.

**Nit 1: half of the probe row checks nothing (status_probe_windows_test.go). CONFIRMED and fixed (213cf617).**
- `accepts(d)` is `d >= rearm`, so that half only checks `statusProbeTimeout >= rearm`. The comment now says it pins the budget relation only, and that `TestStatusProbe_HasTheCommandConnectBudget` pins the value.
- I also added the real-listener check the reviewer suggested:
  - A `winio.DialPipeContext` goroutine with no deadline of its own and an `ln.Accept()` goroutine both run against a listener that has just been shown busy. Both must succeed.
  - Either order must connect, so the result does not depend on scheduling. No wait ends on a timer: each ends on the other side's result, or on the row's own failure (cancel the context, then the listener's Close in cleanup).
  - The comment says this does not guarantee go-winio's 10 ms busy poll (`tryDialPipe`) runs on every pass.

**Nit 2: the header of status_connect_test.go claimed too much. CONFIRMED and fixed (213cf617).** It now says no row's verdict rests on a timing margin of its own choosing. It names the three rows that still need a real connect within `commandConnectDeadline`, the product's own budget: the transient-miss row, the call-deadline row and `TestStatus_RepeatedReadsOfUnchangedStateAgree`.

## What I ran (all exit 0)
- `go vet ./internal/cli/` with GOOS windows, linux and darwin.
- Pinned golangci-lint on `./internal/cli/...` on Windows, and again as a prebuilt binary with GOOS=linux.
- devtool fmt-check, and devtool lint `--only=docmarkers,runpatterns` (both PASS).
- `go test ./test/docs`, and gen-config-docs, gen-command-docs and gen-mcp-docs `--check` (all up to date).
- The changed rows at -count=20 and at -race -count=3, on Windows and on Linux.
- The full `./internal/cli/...` once on Windows and once on Linux.

Linux ran in qompack-v6-linux-verification as qompack-test (uid 10001), -p 2, GOPROXY=off, on a git archive of 213cf617. The container was stopped before; I started it, removed my /work copy and /tmp files afterwards, and stopped it again. I killed no processes.

No criterion was changed and no new number was added. The new reason is text, not a constant number.

### Commits

- 3675a771 fix(cli): name a disabled daemon in status, not a connect miss
- 213cf617 test(cli): say what the cmdconnect rows prove, and no more

### Tests

- `go test -p 2 -count=1 -run '^TestStatus_DaemonDisabledIsNamedNotMissed$' ./internal/cli/ (before the fix, with only the reason constant added)`: FAIL as expected: 'a client that never dials is asked once, not resent' expected 1 actual 2
- `go test -p 2 -count=20 -timeout=30m -run '^(TestStatus_DaemonDisabledIsNamedNotMissed|TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_CallDeadlineExpiryStillSaysSilent)$' -v ./internal/cli/ (Windows)`: ok 221.7s, 100/100 PASS, 0 FAIL
- `go test -p 2 -race -count=3 -timeout=30m -run '^(TestStatus_DaemonDisabledIsNamedNotMissed|TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_CallDeadlineExpiryStillSaysSilent)$' -v ./internal/cli/ (Windows)`: ok 39.0s, 15/15 PASS, no DATA RACE
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/... (Windows)`: exit 0, ok 155.9s
- `linux container, user qompack-test: go test -p 2 -count=20 -timeout=30m -run '^(TestStatus_DaemonDisabledIsNamedNotMissed|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_CallDeadlineExpiryStillSaysSilent)$' -v ./internal/cli/`: exit 0, ok 204.3s, 80/80 PASS (the probe row is Windows-only by build constraint)
- `linux container, user qompack-test: go test -p 2 -race -count=3 -timeout=30m -run '^(TestStatus_DaemonDisabledIsNamedNotMissed|TestStatusSource_NoDaemonNamesTheReason|TestStatusSource_ASilentDaemonIsNotReportedAbsent|TestStatus_CallDeadlineExpiryStillSaysSilent)$' -v ./internal/cli/`: exit 0, ok 32.3s, 12/12 PASS, no DATA RACE
- `linux container, user qompack-test: go test -p 2 -count=1 -timeout=30m ./internal/cli/...`: exit 0, ok 157.7s
- `go vet ./internal/cli/ && GOOS=linux go vet ./internal/cli/ && GOOS=darwin go vet ./internal/cli/`: exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... (and the same binary prebuilt, run with GOOS=linux)`: exit 0 both
- `go run ./tools/devtool fmt-check; go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0; PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: ok 5.9s; all three up to date

### Criterion changes

- None weakened. New row TestStatus_DaemonDisabledIsNamedNotMissed adds a criterion. With runtime.daemon.enabled false and a listener present, status makes exactly one send and no probe, and reports statusDaemonDisabledReason, not the connect-miss, silent or none-listening reason.
- TestStatusSource_NoDaemonNamesTheReason, TestStatusSource_ASilentDaemonIsNotReportedAbsent and TestStatus_CallDeadlineExpiryStillSaysSilent now call fetchDaemonStatus(ctx, client, true, ...). The new enabled argument matches their clients' default State (enabled). Their assertions are unchanged.
- TestStatusProbe_OutlastsAListenerThatIsReArming gains one criterion: a real busy-then-accept dial must connect. Its comment no longer claims the seam half proves the re-arm is outlasted; it says that half pins the budget relation only.

### Open issues

- Carried: three end-to-end rows still need the real transport to connect within commandConnectDeadline (250 ms), the product's own budget, not a margin of their own: TestStatus_TransientConnectMissStillReadsTheDaemon, TestStatus_RepeatedReadsOfUnchangedStateAgree and TestStatus_CallDeadlineExpiryStillSaysSilent. A host that stalls for more than 250 ms could still fail them. Removing this would need a dial seam inside internal/ipc, which this seat does not own. The header of status_connect_test.go now names all three.
- Carried: the new real busy-then-accept check in TestStatusProbe_OutlastsAListenerThatIsReArming proves that a busy dial with no deadline of its own connects once Accept runs. It does not guarantee go-winio's 10 ms busy poll runs on every pass: the scheduler decides whether the dial meets the busy pipe first. The comment says so.
- Carried known limit (D61(e), documented in docs/troubleshooting.md): when the dial status makes to a running daemon misses its connect budget, its client still calls lazySpawn. The duplicate daemon finds the lock and exits, and nothing is lost. Not fixed.
- Carried from statusorder: the C4.5 row in docs/uat.md still records the candidate 7 FAIL. The candidate 8 live rerun must read status --json twice with two or more sessions to close it on a real host.
- Not changed: statusNoDaemonReason still says 'This command asked one to start unless runtime.daemon.enabled is false'. With this fix that reason is only shown when the client is enabled, so the clause is now redundant but not false. doctor (which never spawns) shows the same pre-existing text. I left it alone because docs/uat.md quotes it.

## review:cmdconnect:all:r2: verdict `needs-fixes`, 2 finding(s)

- **major** `internal/cli/status_probe_windows_test.go:64-91 (TestStatusProbe_OutlastsAListenerThatIsReArming, the busy-then-accept block added in 213cf617)`: Lens (a): whether this row finishes depends on scheduling, and when it loses it hangs instead of failing. The dial goroutine closes its conn as soon as DialPipeContext returns. go-winio v0.6.2's listenerRoutine creates a new pipe instance, which is already in the listening state, and only then calls ConnectNamedPipe from a separate goroutine (pipe.go:429-441). A client can connect to that instance and close it before ConnectNamedPipe runs. ConnectNamedPipe then returns ERROR_NO_DATA, and the listener loops to wait for another client (pipe.go:471-477) that never comes. ln.Accept never returns, and the test's `for range 2` select waits forever. The comment's escape hatch ('cancel, then the listener's Close') is in t.Cleanup, which only runs after the function returns, so nothing breaks the wait. The package run dies at -timeout (30m in the house convention). The seat's -count=20 pass was luck: at about 1% per pass, 20 clean passes happen about 82% of the time.
  - Evidence: (1) Read-only overlay probe (zz_review_probe_windows_test.go via -overlay; a verbatim copy of the block run 3000 times, with a 5 s watchdog per pass): `passes=3000 hangs=30 dialErrs=0 acceptErrs=0`, each logged as 'HANG after 1 of 2 results' (the dial returned; Accept never did). (2) The real row: `go test -p 1 -count=50 -timeout=6m -run '^TestStatusProbe_OutlastsAListenerThatIsReArming$' ./internal/cli/` under co-load (a concurrent go test of ./internal/ipc ./internal/store). 2 passes, then `panic: test timed out after 6m0s`. The goroutine dump shows the test goroutine at status_probe_windows_test.go:83 [select, 5 minutes], the Accept goroutine at go-winio pipe.go:559 [chan receive], and listenerRoutine back inside makeConnectedServerPipe/connectPipe (pipe.go:441/548), i.e. waiting for a second client. (3) The same overlay with the dialer's conn held open until Accept returned: `passes=3000 hangs=0`.
  - Fix: Keep the client end open until Accept has returned. Have the dial goroutine send its net.Conn (and error) on the channel, close it in the test goroutine after both results are in, and close the accepted conn in the same place. With the client still connected, ConnectNamedPipe returns success or ERROR_PIPE_CONNECTED, never ERROR_NO_DATA (0/3000 hangs in the probe above). Also correct the comment's claim that every wait ends 'on the row's own failure (cancel, then the listener's Close)'. That only holds if a failure can break the select. Either say the held conn is what guarantees Accept returns, or bound the loop with a failure path that cancels ctx and closes ln inline before calling t.Fatal, not in Cleanup. Re-run the row at -count=50 -p 1 under co-load and at -race -count=5.
- **nit** `internal/cli/qompack_commands.go:279-284 (statusDaemonDisabledReason) and docs/troubleshooting.md:130-133`: Lens (b): one reachable but rare path gets a false reason. `enabled` is daemonClientState(...).DaemonEnabled, which is state.bin's DaemonEnabled AND the config's. That is false when the config now says true but a stale state.bin says false. That happens when a daemon reloaded runtime.daemon.enabled=false, rewrote state.bin (reload.go:131-136), then died without a clean stop, and the user set the key back to true. The line then reads 'runtime.daemon.enabled is false for this project', but the project's config says true. The constant's comment admits the state.bin case ('in its loaded configuration or in the state.bin its daemon last wrote'), but the printed text and the doc sentence do not.
  - Evidence: daemonClientState (qompack_commands.go:176-180): `st.DaemonEnabled = st.DaemonEnabled && cfg.Runtime.Daemon.Enabled`, where st comes from ipc.ReadState, which returns the decoded state.bin when it is valid (state.go:235-237) and falls back to config only when the file is missing or corrupt. daemon.stateWithHot writes DaemonEnabled from the live config at reload.
  - Fix: Make the text cover both sources, for example: "runtime.daemon.enabled is false for this project (in its configuration, or in the state.bin its daemon last wrote), so this command does not ask a daemon, even one that is still running". Update the troubleshooting sentence to match. Or pass cfg.Runtime.Daemon.Enabled separately so that only the config-false case gets the current wording.

## fix:cmdconnect:r2: status `done`, head `722ef8e7410fa699a971d709fb02653106e2a06a`

### Root cause

Major: in TestStatusProbe_OutlastsAListenerThatIsReArming, the busy-then-accept block closed the dialled conn as soon as DialPipeContext returned. go-winio v0.6.2's listenerRoutine first creates a pipe instance that clients can already reach, then calls ConnectNamedPipe on it from a separate goroutine (pipe.go:429-441). A client that connects and closes in that gap makes ConnectNamedPipe return ERROR_NO_DATA. The listener then loops to wait for another client (pipe.go:471-477) that never comes, so Accept never returns, and the row's select, which had no failure path that could break it, waited until the package -timeout. Nit: daemonClientState ANDs state.bin's DaemonEnabled with the config's. A daemon that reloaded runtime.daemon.enabled to false rewrites state.bin (reload.go:131-136), and if it dies without a clean stop, state.bin stays false after the config is set back to true. statusDaemonDisabledReason then said the key was false 'for this project', which the config contradicts.

### Summary

PRODUCT CODE CHANGED (internal/cli/qompack_commands.go: the text of statusDaemonDisabledReason only. Nothing else in the product changed.)

I confirmed both findings myself, reproduced each before fixing it, and checked the fixes on Windows and on Linux.

## Review resolution

**Major: the probe row hangs instead of failing. CONFIRMED and fixed in 6262705c.**
- **Confirmed by reading go-winio v0.6.2.** `makeConnectedServerPipe` calls `makeServerPipe` and only then runs `connectPipe` in a goroutine (pipe.go:429-441). `listenerRoutine` retries on `ERROR_NO_DATA` (pipe.go:471-477).
- **Reproduced first.** I swapped a scratch version of status_probe_windows_test.go in with `-overlay`, keeping the real test name. It loops the block 3000 times, with a fresh listener each pass and a 5 s watchdog that exists only in the scratch copy.
  - With the client conn closed at once, as in 213cf617: `passes=2967 hangs=33 dialErrs=0 acceptErrs=0`. Every hang was "HANG after 1 of 2 results", meaning the dial returned and Accept never did.
  - With the client conn held open until Accept returned: `passes=3000 hangs=0`.
- **The fix.**
  - Each goroutine now sends its `net.Conn` along with its error. The test goroutine closes both conns only after both results are in.
  - With the client still connected, ConnectNamedPipe returns success or ERROR_PIPE_CONNECTED, never ERROR_NO_DATA.
  - The select now has failure paths that run inline, not in Cleanup. A failed dial closes `ln`, so Accept returns. A failed Accept cancels `ctx`, so `DialPipeContext` returns. No wait ends on a timer, and none is unbounded.
  - The comment now says the held conn is what guarantees that Accept returns, cites the go-winio mechanism, and no longer claims Cleanup breaks the wait. No assertion changed.

**Nit: the disabled reason is false when state.bin is stale. CONFIRMED and fixed in 722ef8e7.**
- **Confirmed by reading the code.** `ReadState` returns the decoded state.bin whenever it is valid (state.go:235-237), and `daemonClientState` ANDs that with the config (qompack_commands.go:177).
- **Failing row first.** The new row `TestStatus_StaleStateBinDisabledIsNamed` uses the default config (enabled) and writes a state.bin with `DaemonEnabled=false` through `ipc.WriteState`. On the old text it failed: the reason did not contain "state.bin".
- **The fix.** The reason now reads: "runtime.daemon.enabled is false for this project (in its configuration, or in the state.bin its daemon last wrote), so this command does not ask a daemon, even one that is still running". The constant's comment explains why both places are named.
- **Docs.** docs/troubleshooting.md quotes the new line and adds one sentence saying what the state.bin case is. The statusorder and lazy-spawn text is untouched.
- **Test structure.** The existing disabled row's assertions moved, unchanged, into the helper `requireStatusNamesTheDisabledDaemon`, which both rows use.
- **Recovery advice left out.** I first wrote a recovery hint for the docs but removed it before committing, because I could not verify it. That led to a new open item (see open_issues).

## Machine limits
- I could not do the reviewer's co-load re-run. A concurrent go test as a load generator would break the brief's one-go-test-at-a-time and no-load-generator rules.
- In its place: the 3000-pass overlay loop, the real row at `-p 1 -count=50`, and the focused runs, all on a laptop that was already shared with the ten-agent audit.

## What I ran (all exit 0 unless noted)
- `go vet ./internal/cli/` with GOOS windows, linux and darwin.
- Pinned golangci-lint on `./internal/cli/...` on Windows, and the same binary built locally and run with GOOS=linux.
- devtool fmt-check, and devtool lint `--only=docmarkers,runpatterns` (both PASS).
- `go test ./test/docs`, and gen-config-docs, gen-command-docs and gen-mcp-docs `--check` (all up to date).
- The focused rows and full-package runs listed under tests, on Windows and on Linux.

**Linux.** It ran in qompack-v6-linux-verification as qompack-test (uid 10001), -p 2, with GOPROXY=off, on a git archive of 722ef8e7. The probe row is Windows-only by build constraint.
- The container was stopped when I found it. I started it, removed my /work copy and /tmp home afterwards, and stopped it again.
- Its status reads "Exited (137)", the same as before: PID 1 is `sleep infinity`, which ignores SIGTERM, so `docker stop` kills it.

I killed no processes, and the worktree is clean.

The scratch overlay files are in my scratchpad only: C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w19c/probe_loop_windows_test.go and overlay.json.

### Commits

- 6262705c fix(cli): hold the probe row's client until accept returns
- 722ef8e7 fix(cli): name state.bin in the disabled-daemon status reason

### Tests

- `go test -p 2 -count=1 -timeout=20m -overlay <scratch overlay.json: replaces internal/cli/status_probe_windows_test.go with a 3000-pass loop of the busy-then-accept block> -run '^TestStatusProbe_OutlastsAListenerThatIsReArming$' -v ./internal/cli/ with QP_MODE=old (client conn closed at once, as in 213cf617)`: FAIL as expected: passes=2967 hangs=33 dialErrs=0 acceptErrs=0, every hang 'after 1 of 2 results' (Accept never returned)
- `same overlay, QP_MODE=new (client conn held until Accept returns)`: PASS: passes=3000 hangs=0 dialErrs=0 acceptErrs=0
- `go test -p 2 -count=1 -run '^(TestStatus_StaleStateBinDisabledIsNamed|TestStatus_DaemonDisabledIsNamedNotMissed)$' -v ./internal/cli/ (before the text fix)`: FAIL as expected: TestStatus_StaleStateBinDisabledIsNamed, the reason does not contain "state.bin"; TestStatus_DaemonDisabledIsNamedNotMissed PASS
- `go test -p 1 -count=50 -timeout=30m -run '^TestStatusProbe_OutlastsAListenerThatIsReArming$' -v ./internal/cli/ (Windows)`: ok 5.9s, 50/50 PASS
- `go test -p 2 -count=20 -timeout=30m -run '^(TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatus_StaleStateBinDisabledIsNamed|TestStatus_DaemonDisabledIsNamedNotMissed)$' -v ./internal/cli/ (Windows)`: ok 5.3s, 60/60 PASS
- `go test -p 2 -race -count=5 -timeout=30m -run '^(TestStatusProbe_OutlastsAListenerThatIsReArming|TestStatus_StaleStateBinDisabledIsNamed|TestStatus_DaemonDisabledIsNamedNotMissed)$' -v ./internal/cli/ (Windows)`: ok 5.2s, 15/15 PASS, no DATA RACE
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/... (Windows)`: ok 162.1s
- `linux container, user qompack-test: go test -p 2 -count=20 -timeout=30m -run '^(TestStatus_StaleStateBinDisabledIsNamed|TestStatus_DaemonDisabledIsNamedNotMissed)$' -v ./internal/cli/`: ok 3.4s, 40/40 PASS
- `linux container, user qompack-test: go test -p 2 -race -count=3 -timeout=30m -run '^(TestStatus_StaleStateBinDisabledIsNamed|TestStatus_DaemonDisabledIsNamedNotMissed)$' -v ./internal/cli/`: ok 1.6s, 6/6 PASS, no DATA RACE
- `linux container, user qompack-test: go test -p 2 -count=1 -timeout=30m ./internal/cli/...`: ok 159.8s
- `go vet ./internal/cli/ && GOOS=linux go vet ./internal/cli/ && GOOS=darwin go vet ./internal/cli/ (and go vet in the linux container)`: exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/... (and the same binary built locally, run with GOOS=linux)`: exit 0 both
- `go run ./tools/devtool fmt-check; go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0; PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: ok 4.0s; all three up to date

### Criterion changes

- None weakened, and no new number was added.
- New row TestStatus_StaleStateBinDisabledIsNamed adds a criterion. With the default config (enabled) and a state.bin that says DaemonEnabled=false, status gets exactly the same disabled-daemon answer as TestStatus_DaemonDisabledIsNamedNotMissed: one send, no probe, statusDaemonDisabledReason, and none of the connect-miss, silent or none-listening reasons. The reason must also contain "state.bin".
- TestStatus_DaemonDisabledIsNamedNotMissed: its assertions moved, unchanged, into the shared helper requireStatusNamesTheDisabledDaemon. The expected reason text now includes the parenthetical naming the config or state.bin, because the constant itself changed.
- TestStatusProbe_OutlastsAListenerThatIsReArming: no assertion changed. The busy-then-accept block now holds both conns until both results are in, and breaks a one-sided failure inline (close the listener, or cancel the dial) instead of hanging until -timeout. Reason: the hang reproduced 33 times in 3000 passes.

### Open issues

- NEW, pre-existing, not fixed (outside this seat's files and scope): a stale state.bin with DaemonEnabled=false is sticky. Hooks AND it with the config the same way (hookclient.go:424), and ensureDaemonRunning returns early when it is false (sessionstart.go:87). After a daemon reloads enabled=false and dies without a clean stop, setting the key back to true never brings a daemon back, because nothing spawns one to rewrite state.bin. By ipc.ReadState's code, deleting .qompack/state.bin falls back to the config, but I did not run that, so it is not documented as a recovery step. The coordinator should rule on whether this needs a fix or a documented limit.
- Carried: three end-to-end rows still need the real transport to connect within commandConnectDeadline (250 ms), the product's own budget, not a margin of their own: TestStatus_TransientConnectMissStillReadsTheDaemon, TestStatus_RepeatedReadsOfUnchangedStateAgree and TestStatus_CallDeadlineExpiryStillSaysSilent. A host that stalls for more than 250 ms could fail them. Removing that would need a dial seam inside internal/ipc, which this seat does not own. The header of status_connect_test.go names all three.
- Carried: the busy-then-accept check in TestStatusProbe_OutlastsAListenerThatIsReArming proves that a busy dial with no deadline of its own connects once Accept runs. It does not guarantee go-winio's 10 ms busy poll runs on every pass: the scheduler decides whether the dial meets the busy pipe first. The comment says so.
- Carried: the reviewer's co-load re-run (the probe row at -count=50 beside a concurrent go test) was not done, because the brief's machine limits forbid a second go test process or a load generator. The 3000-pass overlay loop (0 hangs with the fix) and -p 1 -count=50 stand in for it.
- Carried known limit (D61(e), documented in docs/troubleshooting.md): when status's dial to a running daemon misses its connect budget, its client still calls lazySpawn. The duplicate daemon finds the lock and exits, and nothing is lost. Not fixed.
- Carried from statusorder: the C4.5 row in docs/uat.md still records the candidate 7 FAIL. The candidate 8 live rerun must read status --json twice with two or more sessions to close it on a real host.
- Carried, not changed: statusNoDaemonReason still says 'This command asked one to start unless runtime.daemon.enabled is false'. That clause is now redundant but not false, and docs/uat.md quotes it.

## review:cmdconnect:all:r3: verdict `needs-fixes`, 2 finding(s)

- **nit** `internal/cli/status_probe_windows_test.go:59-62 (TestStatusProbe_OutlastsAListenerThatIsReArming, the no-listener contrast)`: Lens (a). The no-listener dial still has a wall-clock budget: `winio.DialPipe(nowhere.Path, &budget)` with budget = selfTestProbeTimeout (50 ms). go-winio sets absTimeout = time.Now()+50ms, and tryDialPipe checks ctx.Done() before its first CreateFile (pipe.go:207-213). If the thread is starved for 50 ms or more between those two points, the dial returns ctx.Err(), which DialPipe maps to ErrTimeout. Both `require.ErrorIs(err, os.ErrNotExist)` and `require.False(errors.Is(err, winio.ErrTimeout))` then fail. That needs an extreme stall, so the risk is tiny, but it is a margin the row chooses for itself, and the row's header says no outcome depends on scheduling. Part 1 (the busy listener reads as ErrTimeout, and the Probe returns false) has no margin, because the pipe stays busy forever.
  - Evidence: go-winio v0.6.2 pipe.go:236-249 (DialPipe: absTimeout from time.Now(), DeadlineExceeded -> ErrTimeout) and 207-213 (the ctx.Done select comes before CreateFile). It did not fail in my runs: the row passed 50/50 at -count=50 and 5/5 under -race -count=5 while a `go build -a` load loop ran.
  - Fix: Dial the absent pipe with no deadline: `_, err = winio.DialPipeContext(context.Background(), nowhere.Path)`. A missing pipe returns ERROR_FILE_NOT_FOUND at once and can never return ErrTimeout, so the contrast holds with no timer. Keep the two assertions.
- **nit** `internal/cli/qompack_commands.go:244-250 (commandStatusSources) vs :207-219 (newCommandClient)`: Lens (b). The `enabled` that fetchDaemonStatus branches on comes from a second, separate read of state.bin (daemonClientState(root, cfg)), not from the State the client was built with. If state.bin changes between the two reads, the reason can be false. Example: a daemon dies cleanly and RemoveState runs, or a daemon reloads runtime.daemon.enabled. The client is then built disabled while sources see enabled. Status probes, sees no listener, and reports statusNoDaemonReason ("asked one to start") although the client never dialed. The reverse order gives the disabled reason for a client that did dial. The window is microseconds, and the cause is the one this round's fix 722ef8e7 centres on (state.bin drifting from the configuration).
  - Evidence: newCommandClient passes `State: daemonClientState(root, cfg)` into ipc.ClientOptions, and Send step 2 (client.go:211) branches on that copy. commandStatusSources calls `daemonClientState(root, cfg).DaemonEnabled` again, reading state.bin a second time (ipc.ReadState). buildCommandDeps (qompack_commands.go:139-141) and doctor.statusRows (doctor.go:1331-1333) both make the two calls one after the other.
  - Fix: Read state.bin once. Have newCommandClient take or return the ipc.State it built the client with, for example `st := daemonClientState(root, cfg)` in the caller, passed to both newCommandClient and commandStatusSources(ctx, root, st.DaemonEnabled, client). Status then branches on exactly the DaemonEnabled the client uses. The existing rows keep working, because they assert on the built options (built[0].State.DaemonEnabled).

## verify:cmdconnect: verdict `sound`, 0 finding(s)



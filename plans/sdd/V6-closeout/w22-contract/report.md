# Wave 22 contract seat

Branch `closeout/w22-contract`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/contract/assertions.go:103-123 (checkSessionStartFires); docs/troubleshooting.md:173-187`: Two or three sessions opened at once on a fresh project (no run/marker.json yet) push StartsWithoutMarker to 2. That is a SevCritical failure, and the daemon degrades to degraded-passive on a healthy store where every hook fired. The cause: an absent marker is counted as an absence even while the session it would come from (LastSessionID) is still live and so has had no terminal hook yet. With only two windows the row reads marker-absent-once (pending). It stays pending through both sessions' compactions, until a third session starts. That contradicts troubleshooting.md:186-187, which says a healthy project reads '0 pending' and 'That stays true after a compaction or a --resume of any of its sessions'. W20 fixed concurrent restarts only; concurrent fresh starts are the same false-critical class as the first audit's major. The bug predates this round, but D62 records it as 'same-session-restart is recognized per session with concurrent sessions'.
- **major** `internal/contract/assertions.go:165-190 (checkSessionStartSourceCompact); internal/daemon/handlers.go:1353`: A compaction that the user cancels (Esc during 'Compacting conversation') or that fails after the PreCompact hook fired leaves AwaitingCompactStart set, and nothing clears it except the session's next start. If the user then exits and runs --resume (or --continue) on that session, the start arrives with source=resume. session_start.source_compact then fails at SevCritical, the project degrades to degraded-passive, and the banner blames the host. So a legitimate restart reaches SevCritical, which this dimension requires never to happen. The code is unchanged since candidate 7, but the restart work in this round did not cover it.
- **minor** `internal/contract/assertions.go:113-118; internal/daemon/handlers.go:1022-1033 (heldBack covers only source_compact)`: A session.start whose live reply missed the client's deadline is spooled and replayed with the same nonce (withdrawLostStartAnswer designs for exactly this). If that session's PreCompact was handled before the replay and another session started in between, the replayed startup finds its own marker. Because its source is startup and LastSessionID names the other session, it counts an absence. Two such replays reach SevCritical. heldBack already guards the analogous replay-order problem for session_start.source_compact, but session_start.fires has no guard.
- **minor** `internal/cli/qompack_commands.go:278-279 (statusNoDaemonReason), used by internal/cli/doctor.go:1343-1364`: doctor builds its status client with Self stripped (doctorNoSpawnEnv), so it never spawns a daemon. Its status.primary row still quotes statusNoDaemonReason, 'This command asked one to start unless runtime.daemon.enabled is false; run status again once it is up', and the same detail then says 'doctor never SPAWNS a daemon'. The reason text is false for doctor, and the row contradicts itself. W19c noted the text but left it.
- **minor** `internal/cli/qompack_commands.go:356-397 (fetchDaemonStatus)`: When runtime.mode is "off" (in the configuration, or in the Mode of the state.bin a daemon wrote), the command client's Send returns at step 1 with OK:true and no Data, without dialing. fetchDaemonStatus treats that as a live answer and json.Unmarshal(nil) fails. So status and doctor give the reason 'daemon: decoding status: unexpected end of JSON input', an internal error that is untrue: no daemon was asked and nothing was malformed. cmd_mcp.go:344 already handles this case (resp.Mode == ModeOff && len(Data)==0).
- **minor** `internal/store/provenance.go:58-90 (ContentOrigins ranges over rootIndex, toolUse, fileHist maps); internal/mcp/authorize.go:265-269 (authorizeHash returns the first refusal)`: MCP results are nondeterministic (D53(a)). ContentOrigins returns origins in map-iteration order, and authorizeHash returns the refusal of whichever refused origin comes first. When one hash or chunk has several refused origins, the withheld reason that recall, why, re_read and dropped report changes from read to read. For example, a chunk shared by a host-denied file and an out-of-project path reads 'the host's current permission rules deny reading ...' on one read and 'the associated path is outside the project ...' on the next. The verdict is always withheld, so this is reason text only. Shared chunks are common under content-defined chunking. The bug predates this round, but the sweep for every map iteration feeding MCP results missed it.
- **minor** `internal/cli/qompack_commands.go:175-179 (daemonClientState); internal/cli/hookclient.go:424; internal/cli/sessionstart.go:87-89; docs/troubleshooting.md:131-135`: A state.bin left with DaemonEnabled=false is sticky. A daemon that reloads runtime.daemon.enabled=false keeps running and writes it, and a reboot or kill before a clean stop leaves it behind. Setting the key back to true never brings a daemon back: hooks AND state.bin with the configuration, ensureDaemonRunning returns at once, and nothing ever rewrites state.bin. The status reason now names state.bin correctly, but neither it nor troubleshooting.md says how to recover (delete .qompack/run/state.bin). W19c reported this as a new open issue, and it is not in the D45-D65 ledger.
- **minor** `internal/cli/hookclient.go:424 and :479-480; internal/cli/sessionstart.go:87; docs/troubleshooting.md:133-135`: A stale state.bin that says DaemonEnabled=false is sticky, and nothing rules on it (w19c cmdconnect open issue: 'The coordinator should rule on whether this needs a fix or a documented limit'). Say a daemon reloads runtime.daemon.enabled=false, which rewrites state.bin (reload.go:154), and then dies without a clean stop, so RemoveState never runs. Hooks AND state.bin's flag with the config, and ensureDaemonRunning returns early when the result is false. So after the key is set back to true, no daemon is ever spawned to rewrite state.bin, and every hook spools forever. troubleshooting names the state.bin case but gives no recovery (deleting .qompack/run/state.bin).
- nit `internal/cli/status_probe_windows_test.go:115-127`: The third half of TestStatusProbe_OutlastsAListenerThatIsReArming is platform-neutral: through the statusProbeDial stub it requires daemonListening to dial the project's own address exactly once, with a budget that outlasts a re-arm. It lives in a _windows_test.go file, so Linux and macOS never assert which address the probe dials or how often. Only the budget-equality row, TestStatusProbe_HasTheCommandConnectBudget, runs there. Status's unix-socket path therefore has no row for the dialed address.
- nit `internal/daemon/reload.go:35-52 (ConfigFileStamp, StampConfigFile) and daemon.go:410`: The new start stamp means the first reload check no longer reloads unconditionally. A change after the stamp is detected only when it alters config.json's mtime or size. An edit that keeps the size, inside the filesystem's timestamp granularity and between the stamp and the load, is now missed until the next change, and before this change the first check always reloaded. That granularity is the coarse kernel clock on ext4 without multigrain timestamps (one jiffy), 2 s on exFAT/FAT, and coarse on some SMB/NFS mounts. The same-size, same-tick blind spot already exists for every later reload; the start window is new.
- nit `docs/security.md:119-122; docs/adr/0011-rehydration-budget-and-item-order.md item 8 (lines ~1310, 1345)`: Under D64(1) a root holding `~` has no root unit, so every summary that spells it is withheld. The docs give OneDrive for Business (`OneDrive - Contoso`) as the everyday Windows example, but not its macOS counterpart. A project in iCloud Drive's real path (`~/Library/Mobile Documents/com~apple~CloudDocs/...`) loses the unit in the same way, so macOS users see every root-spelling summary withheld without a documented reason.
- nit `internal/daemon/rehydrate_service_test.go:965-984 (pre-existing, 4970058b)`: TestService_StateWriteFailureStillEmits makes state/ unwritable with chmod 0500 and skips only on Windows. As root on Linux the directory stays writable and the row fails. Wave 19g's own Linux container run hit this (report-19g line 57/79). Tonight's lanes are not affected: linux-nonroot-inner.sh runs as uid 10001, no night lane passes --as-root, and hosted ubuntu runs as a non-root user. The usual golang:* container default (root) and WSL as root still fail it.

## impl:contract: status `done`, head `62b0efb718317e27452c4d0f2aab34659edf7eb8`

### Summary

Seat contract, branch closeout/w22-contract (worktree qompack-cx-w22-contract), base 2bf29705, HEAD 62b0efb7. 7 commits, unpushed. Every commit has the footer "Refs: V6-VERIFY, C4.5" and no attribution trailers. Every assigned finding is fixed. Each fix has a row that was red on 2bf29705 (all recorded in contract-evidence/red-on-base-2bf29705.log). For #8 and #9 the genuine failures are pinned too, and those rows are green both before and after the fix.

#8 (session_start.fires, concurrent fresh starts): an absent marker is now counted only once the session the history last saw start (LastSessionID) is no longer live. The check uses Env.SessionLive, which is the daemon registry. Such a start counts nothing, leaves LastSessionID on the running session, and reads the new idle spelling prior-session-live. A session the daemon does not know still counts as ended, so genuine absences after a daemon restart or an abandoned session still count, and two of them still fail critical.

#10 (replayed startup after its own PreCompact or SessionEnd): Env gains StartTS, which the daemon sets with hookTime(req, now). A marker that names the starting session and was written at or after StartTS counts as a restart. An unknown StartTS keeps the old behaviour.

#9 (cancelled or failed compaction, then --resume): a prompt or SessionEnd of the compacting session, fired after its PreCompact, now sets CompactStartLapsed in history.json. The field is omitempty, so the frozen golden is unchanged. The prompt scan sets it, and so does endSession, whether live or drained. The session's next non-compact start reads precompact-not-completed, which is idle, not a failure. A new PreCompact clears the record. A PreCompact followed by a non-compact start of the same session with nothing of that session in between still fails critical. A prompt or SessionEnd fired before the PreCompact but replayed after it does not count as in between.

#13: ContentOrigins sorts origins by path, then tool.

#11/#12: commandClient now carries modeOff and mayStart, and fetchDaemonStatus takes the commandClient. Doctor's reason now says it does not start a daemon. With runtime.mode off, status gives statusModeOffReason instead of "decoding status". The unreachable "unless runtime.daemon.enabled is false" clause is gone.

#15/#67 (D67(c)): daemonEnabledFor now decides DaemonEnabled for doHook (all six hooks, including session-start's daemon start) and for daemonClientState (status, doctor, the frontends, qompack mcp). A configuration that says false always decides. A state.bin that says false decides only while a daemon holds a fresh, non-foreign lock (heartbeat within daemon.StaleAfter) or answers a 250 ms probe. The check runs only when state.bin says false and the configuration says true.

Class sweeps: all three contract.Env builders (daemon, self-test, contracttest), every PreCompact-arming path, the live and drained prompt and flush routes, every reader of ContentOrigins, and every reader of state.bin's DaemonEnabled (hookclient, sessionstart, compactUnanswered, daemonClientState → newCommandClient/newMCPClient). The other three nits (reload.go stamp, the iCloud wording in security.md, the root skip in the rehydrate test) are outside this seat's files and were not touched. I did not edit the ledger, which is read-only to this seat; see needs_owner.

### Commits

- 7f047772 fix(contract): count no marker absence while its session runs (#8, #10)
- 737acb10 fix(contract): pass a resume after a cancelled compaction (#9)
- e63faa50 fix(store): return content origins in one order (#13)
- 088235ee fix(cli): trust a disabled state.bin only while its daemon lives (#15, #67, D67(c))
- 69f5b375 fix(cli): name the mode-off and no-start status reasons (#11, #12)
- 66bf330b test(cli): run the status probe's address row on every OS (linux nit)
- 62b0efb7 docs(troubleshooting): describe the wave 22 status readings

### Findings resolution

- **fixed**: #8 major: concurrent fresh starts on a project with no marker reach SevCritical
  - Commit 7f047772. Rows that were red first, in internal/daemon/session_start_fires_w22_test.go: TestSessionStartFires_ConcurrentFreshStartsStayFull. On 2bf29705 it failed with "sess-b startup: no session has ended, so no marker was due (observed marker-absent-once)". It covers three concurrent startups and then two compactions, and every reading is holding or idle, never pending or failing. Contract row: TestSessionStartFires_NoAbsenceWhileThePriorSessionRuns. Genuine direction pinned (green on base and after): TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails/{abandoned,daemon_restarted}, where two absences still give SevCritical and degraded-passive, and TestSessionStartFires_AbsenceOnceThePriorSessionEndedStillCounts. troubleshooting.md section 1 is reworded.
- **fixed**: #9 major: a cancelled or failed compaction, then --resume, reaches SevCritical
  - Commit 737acb10. Row red first: TestSessionStartSourceCompact_CancelledCompactionThenResumeStaysFull, with subtests prompt, session end, and session end then a daemon restart. All three were red on base with observed "resume" and the ingest in degraded mode. Pinned the other way (green on base and after): TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails, with subtests nothing, a prompt fired before the PreCompact, and a SessionEnd fired before the PreCompact; each still gives SevCritical, observed resume and degraded-passive. Contract rows: TestSourceCompact_ASessionThatWentOnReadsNotCompleted and TestNoteCompactLapse_OnlyTheCompactingSessionAfterItsPreCompact.
- **fixed**: #10 minor: a replayed startup after its own PreCompact counts an absence (no heldBack-style guard for fires)
  - Commit 7f047772. Rows red first: TestSessionStartFires_ReplayedStartupAfterItsOwnPreCompact, the audit's sequence, where base counted 1 and would reach 2. Also TestSessionStartFires_ReplayedStartupAfterAnEndedSessionsMarkerWasOverwritten, where on base the session it awaited had ended and its marker was overwritten, and base read marker-absent-once. Contract row: TestSessionStartFires_OwnMarkerWrittenAfterTheStartIsARestart. A marker written before StartTS, or an unknown StartTS, still counts.
- **fixed**: #11 minor: doctor's status.primary row says the command asked a daemon to start
  - Commit 69f5b375. Rows red first: TestDoctor_NoDaemonReasonNeverSaysItAskedOneToStart, and TestStatus_NoDaemonReasonSaysItAskedOneToStartOnlyWhenItCan with subtests for no executable and with an executable (spawner stubbed, no process started). The docs/uat.md quote is still a substring of the new spawning reason, so uat.md was not touched.
- **fixed**: #12 minor: runtime.mode off makes status report 'decoding status: unexpected end of JSON input'
  - Commit 69f5b375. Row red first: TestStatus_ModeOffNamesTheModeNotADecodeError. A client built with mode off now returns statusModeOffReason with no send and no probe. troubleshooting.md section 1 has the matching sentence.
- **fixed**: #13 minor: the MCP withheld reason is nondeterministic (ContentOrigins map order)
  - Commit e63faa50. Rows red first: TestContentOrigins_OneOrderOnEveryCall (store, 200 calls covering all three indices). Also TestAuthorizeHash_OneReasonOnEveryRead (MCP): on base, 200 expands of one object gave the outside-the-project reason 26 times and the host-deny reason 174 times; now one reason. The sort is in ContentOrigins, so every consumer gets it.
- **fixed**: #15 minor: a stale state.bin with DaemonEnabled=false is sticky
  - Commit 088235ee, implementing D67(c). Rows red first: TestDaemonClientState_DeadDaemonsDisabledStateDefersToTheConfiguration and TestStatus_DeadDaemonsDisabledStateIsNotReportedDisabled. Live direction pinned (green before and after): TestDaemonClientState_LiveDaemonsDisabledStateIsTrusted, with subtests where the daemon answers and where it holds the lock.
- **fixed**: #67 minor (complete): nothing rules on the sticky disabled state.bin; session-start never spawns
  - Commit 088235ee, same mechanism. Row red first: TestSessionStart_DeadDaemonsDisabledStateStillStartsADaemon/daemon_gone. On base, session-start's pre-send step saw DaemonEnabled=false; now it sees true. The daemon_holds_the_lock subtest still sees false. Recording this in the ledger is for the coordinator; see needs_owner.
- **fixed**: nit (linux): the status probe's address row lives only in a _windows_test.go file
  - Commit 66bf330b. The stub-based half moved to TestStatusProbe_DialsTheProjectsAddressOnce in status_connect_test.go, a platform-neutral file. The Windows file keeps the go-winio halves. No assertion changed.

### Tests

- `go test -p 2 -count=1 -run '<14 new rows>' on 2bf29705 (daemon, cli, store, mcp), log contract-evidence/red-on-base-2bf29705.log`: Red on base as intended: Concurrent, Cancelled (3 subtests), both Replayed rows, all six cli fix rows, ContentOrigins, AuthorizeHash. The pin rows (AbsenceAfterAnEnded, NonCompactStartRightAfterPreCompact, LiveDaemonsDisabledStateIsTrusted) were green.
- `GOOS={windows,linux,darwin} go vet ./internal/contract/... ./internal/daemon/ ./internal/cli/ ./internal/store/ ./internal/mcp/ ./test/e2e/`: clean on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/contract/... ./internal/store/ ./internal/mcp/ ./internal/cli/ ./internal/daemon/ (Windows, HEAD 62b0efb7)`: all ok (contract, contracttest, store, mcp, cli, daemon); log contract-evidence/win-full.log
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows, exported commit-1 tree)`: ok 837s; the commit is bisectable
- `go test -p 2 -count=1 -timeout=30m ./test/e2e/ (Windows, no co-load declared)`: Timed out at 30m inside X11 (TestV3_HotPathUnchangedWithLedgerResident, 14m+). TestE2E_SessionStartLatency failed with p99 2.39 s. An A/B on base vs HEAD under the same load (sessionstart-latency-abab.log): base 1.527 s FAIL then 1.286 s pass; HEAD 0.79 s and 1.17 s, both pass. This is co-load, not a regression.
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -skip '^TestV' ./test/e2e/`: Every test passed except TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting: no daemon answered within the bound on its very first session-start, on a fresh project with no state.bin. Rerun alone at HEAD it passed in 142 s (win-e2e-unknownschema.log). Includes both changed e2e rows.
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -run '^TestV' -skip '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e/`: ok 1195s
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -run '^TestV' ./test/e2e/`: Timed out: X11 alone ran 26m33s under daytime co-load without finishing. X11 is the 2,250-process timing row, and its hot path is untouched (daemonEnabledFor short-circuits when state.bin says true).
- `go test -p 2 -count=20 -timeout=30m -run '<new rows>' per package: ./internal/contract/ ./internal/daemon/ ./internal/cli/ ./internal/store/ ./internal/mcp/`: all ok (contract 28.6 s, daemon 204.7 s, cli 92.7 s, store 5.7 s, mcp 48.4 s); log new-rows-stress.log <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -race -count=3 -timeout=30m -run '<new rows>' for the same five packages`: all ok, no DATA RACE
- `sh .../linux-nonroot-gate.sh --out .../wave22/linux-contract 62b0efb7 w22-contract-internal --gomaxprocs 2 -- ./internal/contract/... ./internal/store ./internal/mcp ./internal/cli ./internal/daemon (uid 10001, -race)`: contract 216/0, contracttest 29/0, store 978/0 and daemon 1795/0 all pass. Two failures came from the whole-package co-loaded run only. mcp TestBudgetBF: p95 393 ms against the 250 ms timing budget. cli TestSecondPreCompactCarriesItsOwnSpan: checkpoint seq 3 where 2 was expected. Alone in the same container, TestBudgetBF passed at both base and HEAD, and TestSecondPreCompactCarriesItsOwnSpan passed 5 of 5 at base (--count 5) and 5 of 5 at HEAD.
- `sh .../linux-nonroot-gate.sh ... 62b0efb7 w22-contract-e2e-head --gomaxprocs 2 --run '^(TestE2ESelfTestExitsNonZeroOnCritical|TestSelfTestIsTheOnlyNonZeroExit)$' -- ./test/e2e`: PASS 12/0 (2 tests plus 10 subtests)

### Criterion changes

- session_start.fires (#8): an absent marker counts only once LastSessionID's session is no longer live. A start beside a running session counts nothing and reads prior-session-live, which is added to noObservationSpellings and is idle, not pending. Rationale: a running session has had no terminal hook, so its marker is not due. Unknown sessions still read as ended, so absences after a restart or an abandoned session still count.
- session_start.fires (#10): a marker naming the starting session, written at or after Env.StartTS, counts as that session's own restart and moves neither field. Rationale: only the session's own later terminal hook can write it, and that hook overwrote the evidence the start would have read. An unknown StartTS keeps the old count, so TestSessionStartFires_FreshStartNamingItselfStillCounts stands unchanged.
- session_start.source_compact (#9): once the compacting session prompts or ends after its PreCompact, its next non-compact start reads precompact-not-completed, which is idle. Rationale: a cancelled or failed compaction cannot be told apart from a compact start the host never sent, and neither justifies SevCritical. As a consequence, a completed compaction whose compact start never arrived, followed by a prompt, is no longer reported. A non-compact start with nothing of the session in between still fails, and that case is pinned.
- e2e fixtures: TestE2ESelfTestExitsNonZeroOnCritical and TestSelfTestIsTheOnlyNonZeroExit used to provoke the critical failure with three back-to-back starts on one daemon while every session was still running, which is #8's healthy case. Now each session ends with no terminal hook and its daemon exits (e2eShutdownIfReachable) before the next start, which is a genuine absence. The convergence waits also require SessionCount. The assertions are unchanged.
- TestSessionStartReplay_OwesTheBannerItsLostAnswerCarried: the next live start found the project still degraded only because of #8's false critical. The session now ends without a terminal hook first (abandonAll), which makes the second absence genuine. The banner assertion is unchanged.
- state.bin DaemonEnabled=false (D67(c)) is trusted only while a daemon holds the lock or answers. Four fixtures that model a running daemon which disabled itself now say so through the stateDaemonAlive seam: TestStatus_StaleStateBinDisabledIsNamed, TestStatus_ReasonUsesTheStateTheClientWasBuiltWith, TestSessionStartCompact_UnansweredNoteOnlyWhereARehydrationWasDue/daemon_disabled, and TestHookCapture_RedactsBeforeEveryLegacySpoolMode/daemon_disabled. Without the seam they would silently exercise the dead-daemon path, or Send step 5 instead of step 2. Their assertions are unchanged.
- statusNoDaemonReason no longer has the unreachable 'unless runtime.daemon.enabled is false' clause. There is a new statusNoDaemonNoStartReason for clients that cannot start a daemon, and a new statusModeOffReason. fetchDaemonStatus now takes the commandClient instead of (client, enabled), and its three direct test callers were updated mechanically.

### Open issues

- Release-notes known issue (a sibling of #15 in the same class, not closed): a .qompack/run/state.bin left saying runtime.mode=off by a daemon that died without a clean stop is still trusted. The persisted-ModeOff short-circuit of the hooks is pinned by TestHooks_ModeOffShortCircuits, so I did not change it. It is reachable only when a session start reaches the daemon after the configuration switched to off (for example a spooled start that gets replayed) and the daemon then dies. troubleshooting.md section 1 gives the recovery: delete .qompack/run/state.bin.
- Residual of #8 (behaviour before the fix, now narrower): on a project that has never had a compaction or a session end, a still-open session that the daemon does not know counts as ended. That happens after a daemon crash or restart, or once a window has been silent for 30 minutes and was abandoned. An absence can then be counted, and two such starts still reach SevCritical. Once any terminal hook has written a marker, this cannot happen.
- Residual of #9: a PreCompact that is replayed from a spool after the session's prompt or SessionEnd was already handled live re-arms the obligation with no lapse recorded. The replay ordering this needs is narrow.
- Co-load only, classified: X11 cannot finish inside -timeout=30m under daytime co-load (2,250 process spawns). TestE2E_SessionStartLatency fails on base too under the same load. TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting and the Linux whole-run TestBudgetBF and TestSecondPreCompactCarriesItsOwnSpan all pass when run alone.

### Needs owner

- The ledger is read-only to this seat, so the coordinator needs to record D67(c) as implemented in 088235ee. The rule: a state.bin DaemonEnabled=false speaks for the project only while a daemon holds a fresh lock (within daemon.StaleAfter) or answers a 250 ms probe; otherwise the configuration decides. Also record the state.bin runtime.mode=off sibling as a known issue with its documented recovery.
- Owner ruling to confirm: the #9 trade-off. A compact start the host never sends, followed by a prompt or session end, is no longer reported, because it cannot be told apart from a cancelled compaction. A non-compact start with nothing of the session in between still fails critical.

## review:contract:r1: verdict `needs-fixes`, 2 finding(s)

- **major** `internal/daemon/handlers.go:1356-1361 (handleCheckpoint arming branch; the seat's new line `h.CompactStartLapsed = false`)`: #9 is not closed for the whole class. A PreCompact the daemon already handled live can be replayed later from the hook's spooled copy. This happens when the hook's reply missed its deadline. If the copy is replayed after the session's prompt or SessionEnd has recorded CompactStartLapsed, the obligation is re-armed and the lapse cleared without condition. The cancelled compaction's --resume then fails session_start.source_compact at SevCritical, degrades the project and shows the banner, which is the same false critical as #9. The seat lists this as a residual in open_issues, but it did not fix it and did not mark it deferred-known-issue. It can be closed in the seat's own file, and the line that wipes the lapse is one the seat added.
  - Evidence: Probe in a scratch clone of HEAD 62b0efb7 (kept at C:/Users/Quant/AppData/Local/Temp/claude/vc22/zz_vfy_probe_test.go.keep). Sequence: startRequest(sess, startup) live; checkpointRequest live; then either promptScan at pre.TS+1000 or sessionEndRequest at pre.TS+1000 live; then dd.drainDispatch of the same PreCompact request; then a resume at pre.TS+2000. Output for both the prompt and the session-end subtests: 'after prompt: lapsed=true awaiting=true', then 'after replay: lapsed=false awaiting=true', then 'source_compact ok=false sev=2 observed="resume" mode=degraded-passive banner="Qompack: degraded to passive recording — session_start.source_compact expected \"compact\", observed \"resume\"..."'. Why it is reachable: handleCheckpoint's own comment says a spooled PreCompact copy may arrive after the daemon already handled it live. The live checkpoint route records no seen key (seenSet.begin is used only by ingest and session_end.go), so the drain dispatches the copy. The replay re-arms whenever registry.StartedSince is false, which it is after a cancelled compaction. endSession notes the lapse at its top (handlers.go:1522), and its own final drain replays client spools after that. The window is narrow: the checkpoint route's own watcher kick must miss the copy. The rest of the seat's work verified independently. Every new row is red on 2bf29705 and every pin row is green there, re-run with overlays. The touched packages pass in full on Windows (contract, contracttest, store 624s, mcp 297s, cli 268s, daemon 548s). The two changed e2e rows pass. GOOS windows/linux/darwin vet, golangci-lint, fmt-check, test/docs, docmarkers and runpatterns are clean. The new daemon rows pass -count=20 and the new cli rows pass -race. test/guards passes. test/integration's TestIntegration_HotPathWarmWithRealResidentState fails identically on base under the same co-load, so it is not a regression. Commits have no trailers, every subject is 64 characters or fewer, and every footer is correct. I could not re-run the Linux non-root gate: the Docker engine is stopped (wsl lists docker-desktop as Stopped) and I did not start it. I inspected the seat's artifacts instead: commit 62b0efb7, uid 10001, contract, store and daemon pass, plus the two co-load failures the seat reported.
  - Fix: In handleCheckpoint's arming branch, keep CompactStartLapsed when the request is a replay of the PreCompact already recorded: spoolReplay(ctx) && ev.SessionID == h.LastPrecompactSession && hookTime(req, now) <= h.LastPrecompactTS. The cleaner fix is not to re-arm a replayed PreCompact at all once its session has prompted or ended since req.TS, the way StartedSince already handles starts. Add the probe sequence as a red-first daemon row with prompt and session-end subtests. Keep TestCheckpointReplay_StillArmsWhenNoStartHasFollowed and TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails green.
- **nit** `docs/troubleshooting.md:188-189 (session_start.fires passage)`: The passage says only that a session the daemon does not know because it restarted counts as no longer running. The daemon also ends a live session that has been silent for the idle-exit window (default 1800 s) through EndAbandoned, and that session counts as no longer running too. So a window left idle for 30 minutes and still open counts as ended, and the next start reads marker-absent-once. A user who sees that reading with every window still open will not find the explanation. The seat's open_issues states this residual, but the document does not.
  - Evidence: internal/daemon/daemon.go:898 (idleExitDue calls registry.EndAbandoned(now, window), where window is IdleExitSeconds, default 1800). internal/daemon/registry.go:298-315 sets Live=false. checkSessionStartFires reads Env.SessionLive = registry.IsLive. The pinned row TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails/abandoned depends on exactly this behaviour.
  - Fix: Add a clause to the sentence: '... because the daemon restarted since, or because the session was silent for runtime.daemon.idleExitSeconds (30 minutes by default) and the daemon ended it, counts as no longer running.'

## fix:contract:r1: status `done`, head `b77227e9d327c24cadb1727cb8b0522953004286`

### Summary

Seat contract, fix round 1. Branch closeout/w22-contract (worktree qompack-cx-w22-contract). Base 2bf29705, previous HEAD 62b0efb7, new HEAD b77227e9. One new commit, unpushed. It has the footer "Refs: V6-VERIFY, C4.5", no attribution trailers, and a 63-character subject.

The verifier's major is fixed for the whole class, not only for the copy case. The class: a replayed PreCompact armed session_start.source_compact as of when the drain ran, not as of when the host fired the hook, so the order of arrival could produce a false critical. The arming now lives in one helper, armPrecompact in internal/daemon/handlers.go. handleCheckpoint is its only caller and the only path that arms the obligation; I swept every writer of LastPrecompactTS and AwaitingCompactStart. The helper has three rules:

(1) Persistent guard. A replay fired no later than the PreCompact history already records (h.LastPrecompactTS, for any session) changes nothing. This covers the verifier's case: the spooled copy carries the same nonce and TS (hookclient reuses one Request value), so it keeps its lapse. It also stops an older PreCompact from taking a newer obligation's place. history.json is on disk, so the guard holds across a daemon restart. The restart variant was red too.

(2) Order independence. A prompt or SessionEnd of a session, fired after its PreCompact but handled before it, is kept in a new in-memory map, daemon.wentOn, guarded by historyMu. It is fed by the only two NoteCompactLapse sites, scanSentinelForPrompt and noteCompactLapse at session end, for both live and replayed hooks. Whatever arms the obligation reads the map and lapses the new obligation at once. This closes the variant where the daemon never saw the PreCompact live and a session end's own final drain replays it after the end. The map keeps only hooks fired after the latest recorded PreCompact and is pruned on every arming. A live PreCompact that arrives in order has nothing of its session after it, so its behaviour is unchanged.

(3) StartedSince still stops a replay that a start has followed (unchanged).

Siblings checked: every history load/save site reloads under historyMu; the held-back replayed start (heldBack) leaves CompactStartLapsed untouched; a start of another session drops the obligation as it did on base.

The verifier's probe (zz_vfy_probe_test.go.keep, run in place and then removed) now prints "after replay: lapsed=true awaiting=true" and "source_compact ok=true observed=\"precompact-not-completed\" mode=full banner=\"\"" for both the prompt and the session-end subtests. One sentence was added to section 1 of docs/troubleshooting.md, the status passage this seat owns.

The Linux non-root gate could not run: the Docker engine is stopped (wsl -l -v shows docker-desktop Stopped), and the rules forbid starting it. The Linux check is limited to GOOS=linux and GOOS=darwin go vet, both clean.

### Commits

- 7f047772 fix(contract): count no marker absence while its session runs (#8, #10)
- 737acb10 fix(contract): pass a resume after a cancelled compaction (#9)
- e63faa50 fix(store): return content origins in one order (#13)
- 088235ee fix(cli): trust a disabled state.bin only while its daemon lives (#15, #67, D67(c))
- 69f5b375 fix(cli): name the mode-off and no-start status reasons (#11, #12)
- 66bf330b test(cli): run the status probe's address row on every OS (linux nit)
- 62b0efb7 docs(troubleshooting): describe the wave 22 status readings
- b77227e9 fix(daemon): arm a replayed PreCompact as of when it fired (#9) [fix round 1]

### Findings resolution

- **fixed**: FIX ROUND 1 major: a spooled copy of a PreCompact the daemon already handled live, replayed after the session's prompt or SessionEnd recorded CompactStartLapsed, re-armed the obligation and cleared the lapse, so the cancelled compaction's --resume failed source_compact at SevCritical (handlers.go handleCheckpoint arming branch)
  - Commit b77227e9. The fix is armPrecompact plus noteWentOn in internal/daemon/handlers.go, with the daemon.wentOn field in daemon.go. New rows are in internal/daemon/checkpoint_replay_lapse_w22_test.go; the red log is contract-evidence/fr1-red-on-head-62b0efb7.log.

Red on 62b0efb7 (each failed with "observed \"resume\"", critical and degraded, or with the newer obligation replaced by sess-older):
- TestCheckpointReplay_ACopyAfterTheSessionWentOnKeepsItsLapse, subtests prompt / session end / prompt, then a daemon restart / session end, then a daemon restart. This is the verifier's probe sequence, with explicit TS.
- TestCheckpointReplay_ANeverSeenPreCompactAfterTheSessionWentOnIsLapsed, subtests prompt and session end: the class variant where the daemon never saw the PreCompact live.
- TestCheckpointReplay_AnOlderPreCompactLeavesTheNewerObligation, two subtests. In the first, on 62b0efb7 the newer session's genuine failure was lost and read precompact-pending-for-another-session. In the second, after a restart the older session's finished compaction failed its --resume.

The genuine direction is pinned, green before and after: TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails, with six subtests: a copy of the live PreCompact, a PreCompact never seen live, a prompt fired before it, a SessionEnd fired before it, another session's prompt after it, and another session's SessionEnd after it. Each still reads SevCritical, expected compact, observed resume, degraded-passive.

The pins the verifier named stay green: TestCheckpointReplay_StillArmsWhenNoStartHasFollowed and TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails. So do TestCheckpointReplay_DoesNotReopenACompactStartAlreadyObserved, TestSessionStartReplay_AStartFromBeforeThePreCompactLeavesItsObligationPending and TestSessionStartSourceCompact_CancelledCompactionThenResumeStaysFull.

On 2bf29705 the new file does not compile, because CompactStartLapsed only exists from 737acb10. So the meaningful red base for this round is 62b0efb7.

### Tests

- `go test -p 2 -count=1 -v -run '^(TestCheckpointReplay_ACopyAfterTheSessionWentOnKeepsItsLapse|TestCheckpointReplay_ANeverSeenPreCompactAfterTheSessionWentOnIsLapsed|TestCheckpointReplay_AnOlderPreCompactLeavesTheNewerObligation|TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails)$' ./internal/daemon/ on 62b0efb7 + the new test file (log contract-evidence/fr1-red-on-head-62b0efb7.log)`: Red as intended: all 8 subtests of the three fix rows failed. The pin row passed all 6 subtests.
- `same -run pattern plus TestCheckpointReplay_StillArmsWhenNoStartHasFollowed, TestCheckpointReplay_DoesNotReopenACompactStartAlreadyObserved, TestSessionStartReplay_AStartFromBeforeThePreCompactLeavesItsObligationPending, TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails and TestSessionStartSourceCompact_CancelledCompactionThenResumeStaysFull, at b77227e9`: all PASS
- `verifier probe C:/Users/Quant/AppData/Local/Temp/claude/vc22/zz_vfy_probe_test.go.keep copied in, go test -run '^TestZZVfy_ReplayedPreCompactCopyAfterLapse$' ./internal/daemon/, then removed`: PASS. Both subtests print 'after replay: lapsed=true awaiting=true' and 'source_compact ok=true observed="precompact-not-completed" mode=full banner=""'.
- `GOOS={windows,linux,darwin} go vet ./internal/daemon/ ./internal/contract/...`: clean on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS (after waiting out another seat's golangci-lint lock)
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok 10.4s
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows, HEAD b77227e9; log contract-evidence/fr1-win-daemon-full.log)`: ok 484.2s
- `go test -p 2 -count=20 -timeout=30m -run '<the 4 new rows + 4 pin rows above>' ./internal/daemon/ (log fr1-stress-count20.log)`: ok 141.5s <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -race -count=3 -timeout=30m -run '<same rows>' ./internal/daemon/ (log fr1-race-count3.log)`: ok 23.8s, 0 DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=1 -v -timeout=30m -run '^(TestE2E_CheckpointDegradedPassiveSealsNothing|TestV5_EveryContractAssertionHasARealProducer|TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent)$' ./test/e2e/ (the e2e rows that drive a live PreCompact through the real binary)`: all 3 PASS
- `sh .../linux-nonroot-gate.sh ... -- ./internal/daemon`: NOT RUN: the Docker engine is stopped (wsl -l -v shows docker-desktop Stopped), and this seat may not start it. Covered so far only by the GOOS=linux vet; D67(n) runs the gate on merged wave 22 before the freeze.

### Criterion changes

- session_start.source_compact arming (fix round 1): a PreCompact arms the obligation as of when the host fired it. A replayed PreCompact fired no later than the one history records (h.LastPrecompactTS, any session) changes nothing. A prompt or SessionEnd of the session that fired after the PreCompact but was handled before it lapses the obligation as soon as it is armed. Rationale: the spooled copy of a live-handled PreCompact carries the same TS, and the order in which a drain replays hooks is Qompack's, not the host's. A non-compact start with nothing of its session after the PreCompact still fails critical, and this is pinned for copies, for PreCompacts never seen live, for prompts and SessionEnds fired before the PreCompact, and for other sessions' hooks. Live in-order arming is unchanged. No existing assertion, golden or test was changed.

### Open issues

- The Linux non-root gate for b77227e9 was not run, because the Docker engine is stopped and the seat may not start it. The previous round's Linux run (62b0efb7: daemon 1795/0, uid 10001) predates this commit. D67(n)'s pre-freeze gate on merged wave 22 must cover ./internal/daemon.
- Narrow residual, by design (in-memory, as StartedSince already is): if the daemon restarts between a session's prompt or SessionEnd and the replay of a PreCompact it never recorded, the restarted daemon has no memory of that prompt or end. It then arms the replayed PreCompact with no lapse, which is the behaviour before this round. A copy of a PreCompact that history has already recorded is covered across restarts by the on-disk guard.
- Carried from the previous round, unchanged: the release-notes known issue that a state.bin left saying runtime.mode=off by a dead daemon is still trusted (recovery: delete .qompack/run/state.bin). The #8 residual for a session the daemon does not know. The co-load-only e2e and Linux timing classifications recorded in the previous result.

### Needs owner

- Coordinator, unchanged from the previous round: record D67(c) as implemented in 088235ee and the state.bin runtime.mode=off sibling as a known issue. Confirm the #9 trade-off: a compact start the host never sent, followed by a prompt or session end, is no longer reported.
- Coordinator, new this round: replays now use a monotonic rule. A replayed PreCompact older than the one history records, for any session, never takes the newer one's slot. This matches the order live delivery would give and the existing heldBack rule for replayed starts. History still has a single slot, so two sessions compacting at the same time keep only the newer obligation, as they already did live.


---

## Wave 22b contract seat (rounds after the restart)

Branch `closeout/w22-contract`. Workflow `wf_77be3fb4-369`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## review:contract:r2: verdict `needs-fixes`, 4 finding(s)

- **major** `internal/contract/assertions.go:126-134 (checkSessionStartFires, the sessionLive branch); internal/daemon/handlers.go:1026 (SessionLive: d.registry.IsLive); internal/daemon/registry.go:298-315 (EndAbandoned); docs/troubleshooting.md:184-188`: #8 is not fixed for the whole class. A window that stays open but is quiet for runtime.daemon.idleExitSeconds (1800 s by default) gets abandoned by the idle tick, so IsLive reads false and the next start counts an absence. On a project that has never had a compaction or a session end, two quiet stretches between new windows reach SevCritical and degraded-passive, while every window is still open and in use. The count of 2 also survives the open window's own compaction: checkSessionStartFires tests StartsWithoutMarker >= 2 before the restart branch, so that compaction stays degraded and gets no rehydration. Only a start that finds another session's marker clears it. The seat pinned exactly this sequence as genuine (TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails/abandoned), but an idle-abandoned session has not been shown to have ended. troubleshooting.md:186-188 says a start beside a running session 'counts nothing, however many windows are opened at once on a project that has never had a compaction or a session end', and it names only a daemon restart as making a session count as no longer running. That is the round-1 verifier's nit, which was never addressed. Base 2bf29705 reaches critical in this sequence too (sooner), so this is an incomplete class fix, not a regression.
  - Evidence: Probe TestZZVfy_IdleOpenWindowsReachCritical, kept at C:/Users/Quant/AppData/Local/Temp/claude/vc22r2/zz_vfy_idle_probe_test.go. It was run in a scratch clone of b77227e9 using the seat's abandonAll, startRequest, checkpointRequest and reportOf helpers. Output:
- 'sess-a startup: fires ok=true observed="first-session" count=0 mode=full'
- abandonAll
- 'sess-b startup: ... observed="marker-absent-once" count=1 mode=full'
- registry.Touch(sess-a) gives 'a live again: true'. registry.go:262 revives an abandoned session, so the window was open all along.
- abandonAll
- 'sess-c startup: fires ok=false sev=2 observed="no marker from a prior terminal hook across two consecutive sessions" count=2 mode=degraded-passive'
- sess-a then runs PreCompact and its compact start: 'fires ok=false ... count=2 mode=degraded-passive mayAct=false contextLen=0'. The compact answer is built only when mode.MayAct() (handlers.go handleSessionStart).
  - Fix: For session_start.fires only, treat a session the registry abandoned for silence (SessionState.abandoned, no SessionEnd seen) as possibly still running. Bind this through a separate Env field or registry method rather than changing IsLive, which mcp.server_registered and transcript.readable also read. A genuine absence is then counted once the daemon no longer knows the session (it idle-exits once every session is abandoned), which TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails/daemon_restarted already pins. Flip the /abandoned subtest to expect prior-session-live, and add the probe's sequence (the Touch revival plus the compaction) as a red-first row. The restart variant (windows left open across two daemon idle-exits) stays a documented residual. If the coordinator instead accepts the idle residual, record it in the known issues, and correct troubleshooting.md:184-188 so it names idle abandonment (runtime.daemon.idleExitSeconds) beside the daemon restart and drops 'however many windows'.
- **minor** `internal/daemon/handlers.go:1432-1467 (armPrecompact, noteWentOn); internal/daemon/daemon.go:137-142 (wentOn is in memory only); docs/troubleshooting.md:203-205`: The round-1 major is fixed for the copy case and within one daemon's lifetime. It is still open across a daemon restart: a PreCompact the daemon never saw live, replayed by a restarted daemon after the session's prompt or SessionEnd was handled, arms the obligation with no lapse. The cancelled compaction's --resume then fails session_start.source_compact at SevCritical with the host-blaming banner. fix_r1 lists this under open_issues as a 'narrow residual, by design', but findings_resolution has no deferred-known-issue entry and gives no release-notes sentence. troubleshooting.md:203-205 claims the opposite: 'The order in which the hooks reach the daemon does not change this: a PreCompact replayed from a spool after the session already prompted or ended reads the same'. Reachability is narrow. It needs a PreCompact that missed its connect, a cancelled compaction, the session's next hook handled live, and a daemon death before the spool drain replays the PreCompact. The fix is within this seat's files, because SessionHistory is in internal/contract/history.go.
  - Evidence: Probe TestZZVfy_NeverSeenPreCompactReplayedAfterARestart, kept at C:/Users/Quant/AppData/Local/Temp/claude/vc22r2/zz_vfy_restart_probe_test.go and run in a scratch clone of b77227e9 with the seat's lapseFixture, goOn and resumeAt. Sequence: live startup; PreCompact not delivered; prompt (or SessionEnd) at pre.TS+1000 handled live; replayProbeDaemonAt(same root), which is the restart; drainDispatch(pre); resume at pre.TS+2000. Both subtests print 'after replay: awaiting=true lapsed=false', then 'source_compact ok=false sev=2 observed="resume" mode=degraded-passive banner="Qompack: degraded to passive recording — session_start.source_compact expected \"compact\", observed \"resume\". See /qompack:status."'.
  - Fix: Persist the went-on record in state/history.json. For example, give SessionHistory a bounded per-session hook time for the last prompt or SessionEnd, kept only while it is later than LastPrecompactTS and pruned on arming exactly as wentOn is now. armPrecompact then reads it across restarts. Add the probe as a red-first row and keep TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails green. If the residual is kept instead, report it as deferred-known-issue with a one-sentence release note, and qualify troubleshooting.md:203-205 to say 'unless the daemon restarted between them'.
- **minor** `internal/cli/hookclient.go:334-336 and :399-401 (early ModeOff return on state.bin's Mode); internal/cli/qompack_commands.go:173-176 and :281 (daemonClientState, modeOff); docs/troubleshooting.md:143-145`: The sibling of #15/#67 in D67(c)'s class is deferred, although it can be closed in this seat's own files. A state.bin left saying runtime.mode=off by a daemon that died without a clean stop is trusted for good. Every hook returns at the early ModeOff check, before the configuration is loaded, so nothing is recorded or spooled. Session-start never reaches the daemon start that would rewrite state.bin. Status and the MCP server (daemonClientState feeds cmd_mcp.go:207 and :344) report the project as off. This lasts until the user deletes state.bin, even after the configuration stops saying off. The seat documents that recovery and lists it as a release-notes known issue. The brief allows deferral only for a minor that cannot be closed in the seat's files, and every site here is in hookclient.go or qompack_commands.go. Reachability is narrow: a daemon writes Mode=off only when a session.start reaches it while runtime.mode is off (in practice a spooled start replayed after the key was set; handlers.go:1050 writes state.bin after RunAll forces ModeOff), and only a clean stop removes the file (daemon.go:1247).
  - Evidence: Probe TestZZVfy_StaleModeOffStateBin, kept at C:/Users/Quant/AppData/Local/Temp/claude/vc22r2/zz_vfy_modeoff_probe_test.go and run in a scratch clone of b77227e9. It writes state.bin = StateFromConfig(defaults) with Mode=off, with no lock, no listener and the default configuration. Output: 'stateDaemonAlive=false', then 'session-start pre-send (daemon start) reached: 0 times; spool entries: 0; out="{}\n"'.
  - Fix: Apply daemonEnabledFor's rule to the mode: trust state.bin's ModeOff only while stateDaemonAlive(root), and otherwise let the configuration decide. In doHook's two early checks, for example, fall through to admitHookCapture, which already refuses runtime.mode off from the configuration. In daemonClientState, use the configuration's mode for the command client and the MCP server. Add red-first rows for both directions: with the daemon gone, session-start reaches the daemon start and status does not say off; with the daemon alive, the state.bin off still holds. Then drop the 'delete .qompack/run/state.bin' recovery sentence at troubleshooting.md:143-145. If it is kept deferred, the release-notes sentence must say it is the mode sibling of D67(c) and was left by choice.
- **nit** `tools/devtool/test.go:114 (wholeTreeTestTimeout = 30m, used by stubskips); internal/mcp TestBudgetBF`: The two reds in this round's gates come from the environment, not from this seat.

(1) The full `go run ./tools/devtool lint` at b77227e9 passes golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, runpatterns, docmarkers and coveragefloors. It fails stubskips with one problem: 'github.com/qompack/qompack/test/e2e: test binary killed for running past -timeout=30m'. That was in pass 2, which ran test/e2e alone, on a machine carrying other seats' test runs and lints. This seat's two edited e2e rows take 1.64 s (TestE2ESelfTestExitsNonZeroOnCritical) and 3.06 s (TestSelfTestIsTheOnlyNonZeroExit). The seat's own full e2e runs at 62b0efb7 already took 1802-1807 s under co-load, and stubskips.go records about 1188 s on hosted windows-latest. The headroom belongs to wholeTreeTestTimeout and to the cliwork seat (#84/#53).

(2) TestBudgetBF fails in the Linux full run (p95 458.752ms against the 250ms limit). mcp and store have not changed since 62b0efb7, and the seat's isolated runs there gave p95 163.84ms on both base 2bf29705 and head.
  - Evidence: Lint log: C:/Users/Quant/AppData/Local/Temp/claude/vc22r2/lint-full.log.

Linux non-root gate at b77227e9 (uid 10001, -race, gomaxprocs 2). The previous attempt of this round's verifier launched run cx-linux-w22-contract-vr2b-b77227e-20261005T015227Z; it completed after the laptop resumed, and its artifacts are in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-contract-verify-r2b/. Results: contract 216/0, contracttest 29/0, store 978/0, cli 567/0, daemon 1813/0, mcp 442/1 (TestBudgetBF only).

Windows at HEAD:
- internal/daemon full: ok 1060.2s.
- contract, contracttest, store, mcp and cli: ok, from the previous attempt's log vc22r2/win-pkgs.log at the same SHA.
- test/docs ok; test/guards ok.
- GOOS windows, linux and darwin vet: clean. fmt-check: exit 0.
- The five touched or checkpoint e2e rows: PASS.
- The fix-round rows and their pins: -race -count=3 ok.
- The fix-round rows are red on 62b0efb7 (8 subtests).

The round-1 major's copy case is fixed. The commits carry no trailers, have subjects of 64 characters or fewer, and every footer reads 'Refs: V6-VERIFY, C4.5'.
  - Fix: Nothing for this seat. The coordinator should not count the stubskips e2e timeout or the TestBudgetBF co-load red against seat contract. Re-run stubskips on a quiet machine, or raise wholeTreeTestTimeout under #84.

## fix:contract:r2: status `done`, head `22148f04abcf2e505c1d87fff7a5cb9a3264a654`

### Summary

Seat contract, fix round 2. Branch closeout/w22-contract in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-contract. Base 2bf29705, round-1 HEAD b77227e9, new HEAD 22148f04. Nothing is pushed.

An earlier attempt at this round was cut off by the restart and the end of the coordinator's session. It left three commits, 16b6ea1e, 82650a56 and 22148f04, one per verifier finding, and a clean tree. It also left a devtool lint log that stopped at runpatterns. I took over from HEAD: I re-reviewed each commit against the findings and redid my own call-site sweep. I re-ran the red-first evidence and the verifier's own three probes, then ran every check in the brief. The Linux non-root gate ran this time, and so did the FULL `go run ./tools/devtool lint`, stubskips included. I made no further code changes, because the review found nothing left to fix. All 11 branch commits have subjects of 64 characters or fewer, the footer "Refs: V6-VERIFY, C4.5", and no Co-Authored-By or Claude-Session trailers.

1. Major #8, quiet open windows (16b6ea1e). The idle tick (SessionRegistry.EndAbandoned) ends a session only because it has been silent, and that session may still be open. `session_start.fires` now reads a new registry method, MayStillRun (live, or abandoned with no SessionEnd), through a new field, contract.Env.SessionMayRun. IsLive is unchanged, so `mcp.server_registered` and `transcript.readable` read exactly what they read before.
   - The verifier's probe now reads `prior-session-live` at every start, holds `same-session-restart` on a's compaction, and stays in full mode with the rehydration delivered (contextLen=61).
   - The genuine direction still fails critical in two cases: a session ended by its SessionEnd that left no marker, and a session the daemon forgot across a restart.
   - troubleshooting.md now names idle abandonment beside the restart, drops "however many windows", and states the residual.

2. Minor #9, restart (82650a56). The went-on record moved from the in-memory daemon.wentOn into state/history.json as SessionHistory.WentOn.
   - It holds the latest start, prompt or SessionEnd time per session, kept only while it is later than LastPrecompactTS. At most 16 sessions are kept, and the record is pruned on arming.
   - armPrecompact now calls SessionHistory.ArmCompactStart.
   - Starts are recorded too, because StartedSince is in memory as well. The same change closes a live PreCompact handled after its own compact start.
   - The verifier's restart probe now reads `lapsed=true` and `precompact-not-completed`, with no banner.

3. Minor, mode sibling of D67(c) (22148f04). modeFor and stateModeOffHolds apply daemonEnabledFor's rule to runtime.mode:
   - A state.bin that says off holds only while its daemon is alive, or while the configuration also says off.
   - Otherwise the hook goes on to admission and the client works under the configuration's mode.
   - daemonClientState uses modeFor for the command client and the MCP server.
   - The verifier's probe now reaches the daemon start once and spools one entry.
   - troubleshooting.md drops the "delete state.bin" recovery.

Results:
- Windows, full touched packages: contract, contracttest, cli 143 s and daemon 685 s all ok.
- Linux non-root race gate (uid 10001, gomaxprocs 2): contract 220/0, cli 577/0, daemon 1825/0 and store 978/0. mcp had one failure, TestBudgetBF: a timing budget that also failed in rounds 0 and 1 under -race co-load and passes alone at HEAD. mcp is unchanged since b77227e9.
- New rows passed at -count=20 and at -race -count=3.
- vet for windows, linux and darwin is clean, fmt-check passes, test/docs and test/guards are ok.
- The full devtool lint passed all ten steps; stubskips is OK.
- A full test/e2e run passed everything except TestV3_HotPathUnchangedWithLedgerResident. That row breached its B-A and B-B wall-clock limits while another seat's e2e.test and a container gate ran on the same host. Re-run alone under QOMPACK_UNDER_COLOAD=1, the procedure the test's own doc comment prescribes for a shared host, it passes, with B-E_cpu still gated.

### Commits

- 16b6ea1e fix(contract): count no absence while a quiet window may run [fix round 2, verifier major on #8]
- 82650a56 fix(daemon): keep a session's later hooks across a restart (#9) [fix round 2, verifier minor on #9]
- 22148f04 fix(cli): trust a mode-off state.bin only while its daemon lives [fix round 2, verifier minor: mode sibling of D67(c)]
- b77227e9 fix(daemon): arm a replayed PreCompact as of when it fired (#9) [fix round 1]
- 62b0efb7 docs(troubleshooting): describe the wave 22 status readings
- 66bf330b test(cli): run the status probe's address row on every OS
- 69f5b375 fix(cli): name the mode-off and no-start status reasons (#11, #12)
- 088235ee fix(cli): trust a disabled state.bin only while its daemon lives (#15, #67, D67(c))
- e63faa50 fix(store): return content origins in one order (#13)
- 737acb10 fix(contract): pass a resume after a cancelled compaction (#9)
- 7f047772 fix(contract): count no marker absence while its session runs (#8, #10)

### Findings resolution

- **fixed**: FIX ROUND 2 major (#8, whole class): a window that stays open but is quiet for runtime.daemon.idleExitSeconds is abandoned by the idle tick. IsLive then reads false, two quiet stretches between new windows reach SevCritical/degraded-passive, and the open window's own compaction stays degraded with no rehydration. troubleshooting.md:184-188 said 'however many windows' and named only a restart.
  - Commit 16b6ea1e.

What changed:
- New SessionRegistry.MayStillRun (internal/daemon/registry.go): live, or abandoned with no SessionEnd.
- It is bound in handleSessionStart as the new contract.Env.SessionMayRun (internal/contract/assertion.go).
- checkSessionStartFires (internal/contract/assertions.go:134) uses sessionMayRun instead of sessionLive.
- IsLive is unchanged, so mcp.server_registered (assertions.go:404) and transcript.readable (:443) read exactly as before. I swept both. Each fails only once its awaited session has had a prompt, and by then the handshake or transcript was already due, so idle abandonment cannot make their failure false.
- An evicted or restart-forgotten session still counts as no longer running.

Red first:
- TestSessionStartFires_QuietOpenWindowsStayFull (internal/daemon/session_start_fires_w22_test.go) is the verifier's probe sequence, including the Touch revival and a's PreCompact plus compact start. On b77227e9 it read marker-absent-once (contract-evidence/fr2-red-daemon-on-b77227e9.log). I re-showed it red at HEAD with checkSessionStartFires overlaid back to sessionLive (expected prior-session-live, actual marker-absent-once), and green as committed (contract-evidence/fr2b-red-contract-overlay.log).
- The contract-level row TestSessionStartFires_ASessionThatMayStillRunCountsNoAbsence is red under the same overlay and green at HEAD.
- The verifier's probe zz_vfy_idle_probe_test.go, overlaid at HEAD, prints prior-session-live at b and at c (count=0, mode=full) and 'sess-a compact: fires ok=true observed="same-session-restart" count=0 mode=full mayAct=true contextLen=61' (contract-evidence/fr2b-verifier-probes-at-head.log).

Genuine direction pinned, green before and after: TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails.
- 'ended without its marker': SessionEnd, then the marker removed, twice, gives SevCritical and degraded-passive.
- 'abandoned, then the daemon restarted': gives SevCritical.
- 'daemon restarted': gives SevCritical.

Docs: troubleshooting.md section 1 now names idle abandonment (runtime.daemon.idleExitSeconds, MayStillRun) beside the restart, drops 'however many windows', and states the residual.
- **fixed**: FIX ROUND 2 minor (#9 across a daemon restart): wentOn was in memory only, so a PreCompact the daemon never saw live, replayed by a restarted daemon after the session's prompt or SessionEnd was handled, armed with no lapse. The --resume then failed source_compact at SevCritical with the host-blaming banner, and troubleshooting.md:203-205 claimed the arrival order never matters.
  - Commit 82650a56.

What changed:
- SessionHistory.WentOn, a map persisted in state/history.json as went_on (internal/contract/history.go). NoteWentOn keeps the latest hook time per session, only while it is later than LastPrecompactTS, capped at 16 sessions by capWentOn (earliest first, ties by session id, also applied on load). ArmCompactStart arms the obligation, lapses it through NoteCompactLapse when WentOn shows the session already went on, and prunes records no later than the arming.
- armPrecompact now calls ArmCompactStart, and daemon.wentOn and noteWentOn are deleted.
- The writers are scanSentinelForPrompt (now saved when WentOn changed; it runs in the ingest worker, not on the reply path), noteCompactLapse at session end, and handleSessionStart.
- Starts are recorded because StartedSince is in memory too. This also closes a live PreCompact that is handled after its own compact start.

Sweep:
- The prompt scan and the session end both run regardless of capture mode (daemon.go:1094, handlers.go:1565).
- heldBack reads history only; StartedSince is the other in-memory input and is now backed by WentOn.

Red first on b77227e9 (contract-evidence/fr2-red-daemon-on-b77227e9.log): TestCheckpointReplay_ThePreCompactsSessionWentOnBeforeARestart, with subtests prompt, session end, compact start (each followed by a restart) and 'a live PreCompact handled after its compact start'. All failed with observed "resume".

The verifier's probe zz_vfy_restart_probe_test.go, overlaid at HEAD, prints 'after replay: awaiting=true lapsed=true' and 'source_compact ok=true observed="precompact-not-completed" mode=full banner=""' for both subtests.

Genuine direction pinned, green before and after:
- TestCheckpointReplay_NonCompactStartAfterARestartedReplayStillFails: only a startup; a prompt fired before the PreCompact; another session's prompt after it; another session's start after it. All still fail critical across a restart.
- TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails stays green.

Contract rows in internal/contract/history_wenton_w22_test.go: TestSessionHistory_WentOnSurvivesASaveAndLapsesALaterArming, TestSessionHistory_NoteWentOnKeepsOnlyWhatALaterArmingCanRead and TestSessionHistory_WentOnIsBounded.

Docs: troubleshooting.md now says the reading holds 'also when the daemon restarted in between', naming went_on.
- **fixed**: FIX ROUND 2 minor (mode sibling of #15/#67, D67(c)): a state.bin left saying runtime.mode=off by a daemon that died uncleanly was trusted for good. Hooks returned before recording or spooling, session-start never started a daemon, and status and the MCP server reported the project off. The fix was deferred although it is in this seat's files.
  - Commit 22148f04.

What changed in internal/cli/qompack_commands.go:
- modeFor: the configuration's off always decides. state.bin's off holds only while stateDaemonAlive (the lock heartbeat is within StaleAfter, or a daemon answers); otherwise the configuration's mode applies. daemonClientState applies it, which covers the command client (newCommandClient, modeOff) and the MCP server's client (cmd_mcp.go:207). cmd_mcp.go:344 reads the response mode that client produces.
- stateModeOffHolds, for the hook: the daemon is alive, or projectModeOff (the configuration in effect says off).

What changed in internal/cli/hookclient.go:
- doHook's two early ModeOff returns (:339 and :404) happen only when stateModeOffHolds.
- Otherwise admission loads the configuration, and a leftover off in st is replaced by the configuration's mode (:437). The liveness probe runs only when state.bin says off.

Sweep:
- Every ipc.ReadState caller: doctor.go:936 reads only Hot.
- selftest.go's admin.ping client (not in my files) builds from a zero State, which reads state.bin itself. Production always reaches it after selfTestDaemonReachable has found or spawned a daemon, and a spawned daemon rewrites state.bin at startup (daemon.go:752), so its OK cannot be wrong. Only a Self-less test env could see a stale off, and there daemon.reachable already warns. I left it alone.

Red first on b77227e9 (contract-evidence/fr2-red-cli-on-b77227e9.log): TestDaemonClientState_DeadDaemonsModeOffStateDefersToTheConfiguration, TestSessionStart_DeadDaemonsModeOffStateStillStartsADaemon/daemon_gone (the daemon start was never reached and nothing was spooled) and TestStatus_DeadDaemonsModeOffStateIsNotReportedOff.

The other direction, green before and after: TestDaemonClientState_LiveDaemonsModeOffStateIsTrusted (answers / holds the lock), TestSessionStart_DeadDaemonsModeOffStateStillStartsADaemon 'daemon holds the lock' and 'daemon gone, configuration off' (writes nothing, '{}'), and TestStatus_LiveDaemonsModeOffStateIsReportedOff.

The verifier's probe zz_vfy_modeoff_probe_test.go, overlaid at HEAD, prints 'session-start pre-send (daemon start) reached: 1 times; spool entries: 1'.

Docs: troubleshooting.md:143-147 drops the delete-state.bin recovery and describes modeFor. The previous round's release-notes known issue for this is withdrawn.
- **fixed**: FIX ROUND 1 major (#9): a spooled copy of a live-handled PreCompact, replayed after the session's prompt or SessionEnd, re-armed the obligation and cleared the lapse.
  - Commit b77227e9 (round 1). It stays green at HEAD in the Windows full daemon run and in the Linux gate. 82650a56 replaced its in-memory went-on map with the persisted WentOn. The persisted guard (a replay no later than LastPrecompactTS changes nothing) is unchanged.
- **fixed**: Original findings #8/#10, #9, #11, #12, #13, #15/#67 (implement round)
  - Commits 7f047772 (#8, #10), 737acb10 (#9), 69f5b375 (#11, #12), e63faa50 (#13: ContentOrigins sorted; mcp/authorize.go caller), 088235ee (#15/#67, D67(c)), 66bf330b (linux nit) and 62b0efb7 (docs), as reported in the implement round. All their rows are green at HEAD: Windows full contract, cli and daemon; Linux gate contract, cli, daemon and store. In mcp only TestBudgetBF failed, and it passes alone. The verifier of round 2 raised no new finding against them.

### Tests

- `GOOS={windows,linux,darwin} go vet ./internal/contract/... ./internal/daemon ./internal/cli ./internal/mcp ./internal/store`: exit 0 on all three
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 -overlay <checkSessionStartFires back to sessionLive> -run '^(TestSessionStartFires_ASessionThatMayStillRunCountsNoAbsence)$' ./internal/contract/ and -run '^(TestSessionStartFires_QuietOpenWindowsStayFull)$' ./internal/daemon/, then both again without the overlay (contract-evidence/fr2b-red-contract-overlay.log)`: RED under the pre-fix rule (expected prior-session-live, actual marker-absent-once) in both packages; ok at HEAD
- `earlier attempt's red-first runs on b77227e9 (contract-evidence/fr2-red-daemon-on-b77227e9.log, fr2-red-cli-on-b77227e9.log)`: RED: TestCheckpointReplay_ThePreCompactsSessionWentOnBeforeARestart (4 subtests), TestSessionStartFires_QuietOpenWindowsStayFull, TestDaemonClientState_DeadDaemonsModeOffStateDefersToTheConfiguration, TestSessionStart_DeadDaemonsModeOffStateStillStartsADaemon, TestStatus_DeadDaemonsModeOffStateIsNotReportedOff. Pins green: TestCheckpointReplay_NonCompactStartAfterARestartedReplayStillFails, TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails, TestDaemonClientState_LiveDaemonsModeOffStateIsTrusted, TestStatus_LiveDaemonsModeOffStateIsReportedOff
- `verifier probes overlaid at HEAD: go test -v -overlay -run '^(TestZZVfy_IdleOpenWindowsReachCritical|TestZZVfy_NeverSeenPreCompactReplayedAfterARestart)$' ./internal/daemon/ and -run '^TestZZVfy_StaleModeOffStateBin$' ./internal/cli/ (contract-evidence/fr2b-verifier-probes-at-head.log; worktree untouched)`: All three now read correctly: prior-session-live with count=0, full mode and contextLen=61 on the compaction; lapsed=true and precompact-not-completed with no banner; the daemon start reached once and 1 spool entry
- `go test -p 2 -count=1 -timeout=30m ./internal/contract/... ./internal/cli ./internal/daemon (Windows, HEAD 22148f04; contract-evidence/fr2b-win-full.log)`: ok: contract 3.5s, contracttest 1.4s, cli 143.0s, daemon 685.4s
- `sh linux-nonroot-gate.sh --out .../wave22/linux-contract-fr2b 22148f04 w22-contract-fr2b --gomaxprocs 2 -- ./internal/contract/... ./internal/cli ./internal/daemon ./internal/mcp ./internal/store (uid 10001, -race)`: contract 220/0, contracttest 29/0 (4 stub skips), cli 577/0, daemon 1825/0 (1 skip), store 978/0. mcp 442/1: TestBudgetBF, a B-F timing budget (p95 262ms against a 250ms limit under -race while another seat's gate shared the container). It failed the same way at 62b0efb7 and b77227e9, and mcp has not changed since b77227e9.
- `sh linux-nonroot-gate.sh ... 22148f04 w22-contract-fr2b-budgetbf --gomaxprocs 2 --run '^TestBudgetBF$' -- ./internal/mcp`: PASS alone (gate exit 0)
- `go test -p 2 -count=20 -run '<4 contract rows>' ./internal/contract/; '<6 daemon rows: QuietOpenWindowsStayFull, AbsenceAfterAnEndedSessionStillFails, ThePreCompactsSessionWentOnBeforeARestart, NonCompactStartAfterARestartedReplayStillFails, OwesTheBannerItsLostAnswerCarried, NonCompactStartRightAfterAReplayedPreCompactStillFails>' ./internal/daemon/; '<6 cli rows incl. TestHooks_ModeOffShortCircuits>' ./internal/cli/ (contract-evidence/fr2b-new-rows-stress.log)`: ok: contract 1.8s, daemon 118.7s, cli 8.6s <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `same three patterns with -race -count=3`: ok: contract 2.5s, daemon 20.9s, cli 5.2s; no DATA RACE
- `go test -p 2 -count=1 ./test/docs/... ./test/guards/...; go run ./tools/devtool lint --only=docmarkers,runpatterns (contract-evidence/fr2b-docs-guards.log)`: ok docs 20.1s, guards 59.8s; PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m -run '^(TestIntegration_ContractMonitorRunsAgainstRealStore|TestIntegration_DegradedPassiveStillWritesToTheRealStore)$' ./test/integration/`: both PASS
- `go test -p 2 -count=1 -timeout=45m ./test/e2e/ (Windows, HEAD; contract-evidence/fr2b-win-e2e.log)`: Every row passed except TestV3_HotPathUnchangedWithLedgerResident, whose no-ledger run breached B-A and B-B wall p99 (spawn floor p99 135ms; 593 deferrals; 0 lost) while another seat's e2e.test and a container gate ran on this host.
- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e/ (contract-evidence/fr2b-win-e2e-x11-coload.log)`: ok 755.2s. The row's doc comment prescribes this declaration for a run on a shared host; B-E_cpu stays gated. The row measures observe.tool, whose daemon path this round does not touch, and the hook pays for the state.bin liveness probe only when state.bin says off.
- `GOFLAGS=-p=2 go run ./tools/devtool lint (FULL, HEAD 22148f04; contract-evidence/fr2b-devtool-lint-full.log)`: exit 0. PASS on all ten steps: golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips (pass 1: 72 packages, 24m11s; pass 2: test/e2e, 26m18s; 'stubskips: OK'), runpatterns, docmarkers and coveragefloors.

### Criterion changes

- TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails/abandoned (round 1) expected a session the idle tick abandoned to count as a genuine absence. It is replaced by 'ended without its marker' (SessionEnd, marker removed) and 'abandoned, then the daemon restarted'. Rationale: abandonment is a guess from silence that the session's next hook disproves (Touch), so while the daemon knows the session it may still be open. The verifier's expectation, prior-session-live for an abandoned session, is pinned by the new TestSessionStartFires_QuietOpenWindowsStayFull. The genuine absence is still critical once the session ended or the daemon forgot it.
- TestSessionStartReplay_OwesTheBannerItsLostAnswerCarried modelled 'the session ended' with abandonAll. It now ends the session with its SessionEnd and removes the marker (endWithoutMarker), which is the genuinely ended case. The assertions are unchanged.
- TestHooks_ModeOffShortCircuits now stands in a live daemon (useStateDaemonAlive(true)) for its persisted ModeOff. Rationale (D67(c) applied to the mode): a dead daemon's off is the configuration's to decide. The short-circuit itself, with nothing read and nothing written, is asserted unchanged.
- session_start.source_compact arming: a start of the session is now a 'went on' hook too (SessionHistory.WentOn records starts, prompts and SessionEnds). A PreCompact armed after a later start of its session, whether replayed after a restart or held up live, lapses at once, and the session's next start reads precompact-not-completed instead of failing. Rationale: that start already judged the obligation, and StartedSince, the same rule, is in memory only. Cost: a non-compact start that reaches the daemon before its own preceding PreCompact is not reported. That order needs the PreCompact's handling to lose a race with the next start, and before the fix it gave a false critical on the following start instead.
- session_start.fires: a session the daemon ended only for silence is no longer counted as gone while the daemon knows it. Cost: if a host never fired SessionEnd at all, the missing marker is counted only once the daemon forgets those sessions (eviction past maxSessions, or an idle exit and restart), not at the next start.

### Open issues

- Documented residual of #8, in docs/troubleshooting.md section 1 and to go into the release notes' known issues under D66(d). Proposed sentence: "Windows left open but quiet past runtime.daemon.idleExitSeconds (30 minutes by default) across two daemon idle-exits, each followed by a new window before the old ones send a hook, still count a missing session-end marker each time, so the second such start fails session_start.fires and degrades the project to passive recording until a later new session finds a marker." Closing it would mean treating a session the daemon does not know as possibly running, which would give up the genuine direction after a restart.
- Linux gate: mcp TestBudgetBF failed once in the full race run and passed alone. This is the same pre-existing timing-budget signature as rounds 0 and 1, and mcp is unchanged since b77227e9. D67(n)'s pre-freeze Linux gate on merged wave 22 will re-run it.
- Windows e2e: TestV3_HotPathUnchangedWithLedgerResident failed on B-A and B-B wall p99 only while another seat's e2e.test and a container gate shared the host. It passed alone under QOMPACK_UNDER_COLOAD=1 as its doc comment prescribes. The undeclared, quiet-host verdict belongs to hosted ci.yml's test-e2e job under D67(n).
- Seen in the sweep but not in my files: internal/cli/selftest.go's admin.ping client builds from a zero State, so it reads state.bin's mode itself. In production it always runs after a daemon has been found or spawned (and a spawned daemon rewrites state.bin at startup), so its result cannot be wrong. Left alone.

### Needs owner

- Coordinator: record D67(c) as implemented for both DaemonEnabled (088235ee) and runtime.mode (22148f04). The earlier known issue 'a state.bin left saying runtime.mode=off is still trusted; delete .qompack/run/state.bin' is withdrawn and must not go into the release notes.
- Coordinator: add the #8 idle-exit residual sentence from open_issues to the release notes' known issues (D66(d)/D67(o)). Also confirm the trade-off: a host that never sends SessionEnd is now detected only after the daemon forgets those sessions, not at the next start.
- Coordinator, carried from round 1: confirm the #9 trade-offs. A compact start the host never sent, followed by a prompt, a SessionEnd or now a start of the session, is no longer reported. Replays follow the monotonic rule (an older PreCompact never takes a newer one's slot).


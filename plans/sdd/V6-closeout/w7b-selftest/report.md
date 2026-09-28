# w7b-selftest: user commands honour a disabled daemon; install and troubleshooting docs

Branch `closeout/w7b-selftest`. Workflow `wf_ec574dc8-a1f`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `9e015934`

### Root cause

(1) internal/cli/selftest.go runSelfTestChecks called selfTestDaemonReachable without checking runtime.daemon.enabled. When nothing answered and Env.Self was set, that function called daemon.EnsureRunning. So self-test spawned a resident daemon and created run/daemon.lock (and run/spawn.lock) for a project with the daemon disabled, and that daemon drained the spool. The hook path has checked the switch since FR-6 (hookclient.go ANDs st.DaemonEnabled with the config before building the client; sessionstart.go ensureDaemonRunning returns early). Self-test never got the same check. (1b) The same kind of gap existed in the two other user-facing clients that can lazily spawn a daemon: newMCPClient (cmd_mcp.go, `qompack mcp`) and newCommandClient (qompack_commands.go: status, recall, why, dropped and the other frontends). Both built their State from ipc.ReadState(root, cfg) alone. ReadState returns state.bin whenever a valid record exists, and a daemon that died without a clean stop leaves one behind with DaemonEnabled=true. So after the operator disabled the daemon, those commands still dialled and lazily spawned one. Without a state.bin they fell back to the config and were already correct. (3) troubleshooting.md said a spooled request is replayed "at its next idle drain, within 30 s, or its next start". The idle drain only runs after scheduler.idle.detectAfterSeconds (default 120 s) of project-wide idleness, checked on an idle tick of at most 30 s. The client-spool watcher (spool_watch.go) only looks at the spool after a served request kicks it (noteServed calls kickSpoolWatch). So a start spooled because no daemon was listening kicked nothing: the next daemon's startup drain replays it in Run, before that daemon serves anything, or its redrainOnceServing does if the line landed after that drain. A start that got its answer too late did reach the daemon, so it kicked the watcher. (2) install.md §3 recorded the 2.1.263 CLI. On 2.1.280 install, update and uninstall take --json (so does marketplace list), and uninstall without --keep-data removes ~/.claude/plugins/data/<id>/. It also glossed --prune as "remove the cached payload", but the CLI help says it removes auto-installed dependencies that are no longer needed.

### Summary

## W7b-selftest report

### (1) Product defect: self-test starts a daemon for a daemon-disabled project

**Failing test first.** `TestSelfTest_DaemonDisabledStartsNoDaemon` (internal/cli/selftest_test.go):
- **Setup:** a project whose `.qompack/config.json` is `{"runtime":{"daemon":{"enabled":false}}}`. `Env.Self` points at a path that does not exist, so any EnsureRunning attempt reaches SpawnDetached, fails fast and logs "spawn failed". This is the same proxy `TestEnsureDaemonRunning_GatedOnDaemonEnabled` uses. `CLAUDE_PLUGIN_ROOT` is cleared, so nothing is ever staged under a home directory.
- **Assertions:** `daemon.reachable` and `admin.ping` are OK, SevInfo, and read `skipped: runtime.daemon.enabled is false`. No `run/daemon.lock` and no `run/spawn.lock` exist. No "spawn failed" line appears in the logs.
- **Red on the base:** `daemon.reachable OK:false ... Observed: spawn attempted but the daemon did not come up in time`. Log: plans/sdd/V6-closeout/w7b-selftest/runs/selftest-red.txt.

**Fix (409c56f, internal/cli/selftest.go).** runSelfTestChecks now runs selfTestDaemonReachable and selfTestAdminPing only when `cfg.Runtime.Daemon.Enabled` is true. Otherwise the new `selfTestDaemonDisabled(addr)` runs:
- It never calls EnsureRunning and never sends a request.
- It does one `ipc.Probe` liveness dial, which creates nothing. If a daemon answers anyway (one started before the switch was set, or run by hand), `daemon.reachable` becomes a SevWarn, not-OK row, "a daemon is reachable although runtime.daemon.enabled is false". That is not critical, so the exit code stays 0. `admin.ping` still reads skipped.
- `TestSelfTest_DaemonDisabledButReachableWarns` pins this branch. It uses the existing `replyDaemon` helper and records the ops it receives, which must be none.
- The config comes from the soft loader's cfg, which self-test already loads. If that load fails, it falls back to the defaults, where the daemon is enabled.

**Every other caller that can start a daemon** (grep for EnsureRunning / SpawnDetached / lazySpawn / NewClientWithOptions / ClaimSpawn in internal/cli and cmd):
- **`qompack mcp` (newMCPClient) and the command frontends (newCommandClient: status, recall, why, dropped, etc.): real gap, fixed in 264c93c.**
  - Both trusted a stale state.bin over the config (see root_cause).
  - New helper `daemonClientState(root, cfg)` in qompack_commands.go returns `ipc.ReadState` with `DaemonEnabled &&= cfg.Runtime.Daemon.Enabled`, the same rule as the hook path. A disabled client never dials or spawns (ipc Send, step 2).
  - Test seam: a package var `spawnDaemon = daemon.SpawnDetached`, following the existing `repairDeliverySeal` convention. Both constructors pass it as `ClientOptions.Spawn`.
  - Test: `TestDaemonClients_HonourDisabledDaemonOverStaleState`. It seeds a stale state.bin saying "enabled", then checks each constructor with the daemon disabled (no spawn recorded, no spawn.lock) and enabled (spawn recorded, which proves the recorder is reachable).
  - Red on the base: mcp/disabled and commands/disabled failed ("must never spawn a daemon"). Log: runs/clients-red.txt.
  - The test is not t.Parallel and restores the var in Cleanup. Go runs parallel top-level tests only after the sequential ones, so there is no race; the Linux -race run is clean.
- **doctor: correct.** It builds its status client through newCommandClient with `doctorNoSpawnEnv`, which clears Self, so it can never spawn. It now also gets daemonClientState.
- **Hooks (doHook lazySpawn): correct.** `st.DaemonEnabled && cfg.Runtime.Daemon.Enabled` is applied before the client is built.
- **session-start's ensureDaemonRunning: correct.** It returns early on `!st.DaemonEnabled`, pinned by TestEnsureDaemonRunning_GatedOnDaemonEnabled.
- **self-test's admin.ping client: correct.** It passes `Self: ""`, and after the fix it is not built at all when the daemon is disabled.
- **backup / fsck / admin / config / version: correct.** None of them constructs a spawning client.
- **`qompack daemon` itself: left unchanged.** It is the process the spawners launch, and every spawner now checks the switch. Running it by hand is an explicit operator request. Whether it should also refuse is listed under needs_owner.

### (2) docs/install.md §3, corrected from the CLI's own help
Source: `claude --version` = 2.1.280, plus `claude plugin install|update|uninstall|validate|list --help` and `claude plugin marketplace add|update|remove|list --help`. These are help output only: no session was started and the real ~/.claude was not touched.
- **§3:**
  - install, update and uninstall take `--json` ("one machine-readable result line", same exit codes; not with `--prune` on uninstall).
  - `validate`, `list` and `marketplace list` take `--json`; `marketplace add`, `update` and `remove` do not.
  - Uninstall without `--keep-data` removes `~/.claude/plugins/data/<id>/` (quoted from the help). Qompack writes nothing there: no code reads CLAUDE_PLUGIN_DATA.
  - `-y` answers different prompts: on install/update it accepts a marketplace-declared command; on uninstall it skips the `--prune` confirmation.
  - Everything new is marked as recorded from CLI help, not rehearsed. The Task 8 rehearsal on 2.1.263 stays as it was.
- **§6, same file, same staleness:**
  - `--prune` was glossed "also remove the cached payload"; the help says it removes auto-installed dependencies that are no longer needed.
  - The plugins/data table row now says the directory is removed unless `--keep-data`.

### (3) docs/troubleshooting.md, spooled-start passages (~736 and ~769)
- **~736 (late answer):** the served request kicked the client-spool watcher, which replays the spooled copy about two check intervals (2 s each) after it was written. Failing that, the idle drain replays it after `scheduler.idle.detectAfterSeconds` (120 s) idle, or the session's flush or the next daemon's startup drain does.
- **~769:** which drain replays a start depends on why it was spooled.
  - No daemon listening: the start kicked nothing, so the watcher does not pick it up. The next daemon's startup drain in Run replays it before that daemon serves anything; if the line was written after that drain had read the spool, `redrainOnceServing` replays it.
  - Answer too late: the request was served, so it kicked the watcher.
- The wrong "idle drain, within 30 s" claim is gone from both passages.
- §8 Step 4 now says self-test, mcp and the command frontends honour the switch too, and names `.qompack/run/state.bin`.
- The Defender section was not touched.

### Commands and results
- Red before each fix:
  - `go test ./internal/cli -run 'TestSelfTest_DaemonDisabledStartsNoDaemon$' -count=1 -v`: FAIL (runs/selftest-red.txt).
  - `go test ./internal/cli -run 'TestDaemonClients_HonourDisabledDaemonOverStaleState$' -count=1 -v`: FAIL on mcp/disabled and commands/disabled (runs/clients-red.txt).
- `go test ./internal/cli -run 'TestSelfTest_' -count=1 -v`: PASS, 11 tests.
- `go test ./internal/cli -count=1 -timeout=30m` (full package, Windows): ok, 53.3 s (runs/cli-full-windows.txt).
- Focused e2e runs on Windows, all ok:
  - `go test ./test/e2e -run '^TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent$' -count=1 -timeout=30m -v`, including its daemon_disabled subtest (runs/e2e-x15-windows.txt).
  - `go test ./test/e2e -run '^TestE2ESelfTestExitsZeroOnHealthy$' -count=1`
  - `go test ./test/e2e -run '^TestE2ESelfTestExitsNonZeroOnCritical$' -count=1` (runs/e2e-selftest-windows.txt).
- Linux, non-root, -race, commit 264c93c: `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7b-selftest --repo C:/.../qompack-cx-w7b-selftest --out C:/.../runs 264c93c0 cli-race --timeout 30m -- ./internal/cli`. Result: PASS internal/cli, pass=434 fail=0 skip=0, go_test_exit=0, no race log. Artifacts are in runs/cx-w7b-selftest-cli-race-264c93c-20260928T023726Z-artifacts. That commit has the same code as HEAD; only docs and logs follow it.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/cli` (Windows and GOOS=linux): clean.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.
- `go test ./test/docs -count=1`: ok.
- No wall-clock failures occurred, so there was nothing to re-run alone. No background processes or load generators were started.

### Criterion changes
None. No check was weakened. The self-test behaviour change is the product fix itself: under the disabled switch, `daemon.reachable` and `admin.ping` go from a spawn attempt (and a SevWarn failure when it failed) to OK "skipped by configuration". A live daemon under the switch is now a warning.

### Files
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/internal/cli/selftest.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/internal/cli/selftest_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/internal/cli/cmd_mcp.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/internal/cli/qompack_commands.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/internal/cli/daemon_disabled_clients_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/docs/install.md
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/docs/troubleshooting.md
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest/plans/sdd/V6-closeout/w7b-selftest/runs/

### Commits

- 409c56fd fix(cli): skip self-test daemon checks when the daemon is disabled
- 264c93c0 fix(cli): honour daemon.enabled in mcp and command clients
- 2ce25d84 docs(troubleshooting): say which drain replays a spooled start
- 7b56cc8e docs(install): record the 2.1.280 plugin CLI flags
- 9e015934 chore(v6): record w7b-selftest evidence runs

### Tests

- `go test ./internal/cli -run 'TestSelfTest_DaemonDisabledStartsNoDaemon$' -count=1 -v (before fix)` — FAIL as expected: daemon.reachable 'spawn attempted but the daemon did not come up in time' (runs/selftest-red.txt)
- `go test ./internal/cli -run 'TestDaemonClients_HonourDisabledDaemonOverStaleState$' -count=1 -v (before fix)` — FAIL as expected on mcp/disabled and commands/disabled; enabled subtests PASS (runs/clients-red.txt)
- `go test ./internal/cli -run 'TestSelfTest_' -count=1 -v` — PASS (11 tests incl. both new ones)
- `go test ./internal/cli -count=1 -timeout=30m (Windows, full package)` — ok 53.3s
- `go test ./test/e2e -run '^TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent$' -count=1 -timeout=30m -v` — PASS incl. daemon_disabled subtest
- `go test ./test/e2e -run '^TestE2ESelfTestExitsZeroOnHealthy$' -count=1` — ok
- `go test ./test/e2e -run '^TestE2ESelfTestExitsNonZeroOnCritical$' -count=1` — ok
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7b-selftest --repo C:/.../qompack-cx-w7b-selftest --out C:/.../runs 264c93c0 cli-race --timeout 30m -- ./internal/cli` — PASS internal/cli pass=434 fail=0 skip=0, non-root uid 10001, -race, no race log
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/cli (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0
- `go test ./test/docs -count=1` — ok

### Open issues

- docs/troubleshooting.md §8 Step 3 (runtime.mode=off) still says `qompack status` and `qompack self-test` 'start a daemon whatever the mode says'. That is about mode=off, not runtime.daemon.enabled, so it was left alone. It is accurate for mode=off, but the coordinator may want it checked against the owner's intent.
- The install.md 2.1.280 flag behaviour (--json output, and uninstall removing plugins/data/<id>/) is recorded from CLI help only, not rehearsed. test/e2e install_test still rehearses 2.1.263 semantics and was not re-run against 2.1.280 (running it needs the claude CLI against a disposable CLAUDE_CONFIG_DIR, and it was out of scope here).

### Needs the owner

- Should `qompack daemon` itself refuse to run when runtime.daemon.enabled is false? That would be defence in depth against an older binary's spawner, or an operator's hand start, creating the resident process and lock file that docs/release.md section 4 promises never exist. Every spawner in this build now checks the switch; the daemon command was deliberately left unchanged because running it is an explicit request. No new budget or bound constants were introduced.

## Independent review

### review:selftest: needs-fixes

- **minor** `docs/troubleshooting.md:737-739 and :780-781` — The new wording says a late-answered request 'kicked the watcher, which replays the spooled copy about two check intervals (2 s each) after it was written.' That only holds if the spool line lands within about one interval of the kick, or if later served requests keep kicking the watcher. The kick fires when the request is dispatched (dispatchOp -> noteServed -> kickSpoolWatch), not when the answer is written. After a kick the watcher looks once, then makes one trailing look an interval later (about 2 s), then goes back to waiting for a kick. A start that is late because its processing was slow (a compaction takes about 5 s, and the reply wait can be up to 10 s) is spooled after that trailing look. The watcher then does not see it until another request is served. Item (3) asked for exact wording, and this sentence overstates the watcher the same way the old 'within 30 s' text overstated the idle drain.
  - Evidence: internal/daemon/handlers.go:225-226: noteServed runs at the top of dispatchOp, before the handler does its work. internal/daemon/spool_watch.go watchClientSpools/lookAtClientSpools: after a kick the loop takes one trailing look after `every` (spoolCheckInterval = idleRunBudget = 2 s, daemon.go:38), and with no new kick and no unsettled spool it returns more=false and blocks on w.kick again. The file's own comment says the trailing look is for 'a late ACK [that] spools after the request was served', meaning shortly after, not seconds later.
  - Fix: Reword both passages along these lines: 'the served request kicked the client-spool watcher. If the spooled copy was written within about one check interval (2 s) of that request arriving, the watcher replays it about two intervals later. Otherwise the watcher picks it up about two intervals after the next request the daemon serves, for example the session's next prompt. Failing that, the idle drain after scheduler.idle.detectAfterSeconds, the session's flush, or the next daemon's startup drain replays it.'
- **nit** `docs/install.md:115-124` — The 2.1.280 correction quotes only half of the --json help line for install/update. It also omits the new --accept-command <sha256> flag, which the help ties to --json and -y for non-interactive installs. The full help text reads '(same exit codes; a marketplace-declared command is still shown and must be confirmed — pass -y when not interactive)'. A reader scripting installs with --json could conclude that --json alone makes them non-interactive.
  - Evidence: `claude plugin install --help` and `claude plugin update --help` on 2.1.280 (re-run by the reviewer, help output only) list --accept-command and the full --json text. uninstall's '(same exit codes; not with --prune)' is recorded correctly.
  - Fix: Quote the full --json parenthetical for install/update. Add one sentence that 2.1.280 also has --accept-command <sha256> (counts as -y for exactly the command a previous --json run reported), recorded from CLI help and not rehearsed. A bundle installed from a local directory declares no command, so Qompack's documented flow is unaffected.
- **nit** `git log 898bb8b..HEAD: 2ce25d84, 7b56cc8e, 9e015934` — The two code commits carry 'Refs: V6-VERIFY, C6.1'. The two docs commits and the evidence commit have no Refs footer, so part of the C6.1 work cannot be traced back from its commits. The subjects are conventional and no commit has attribution trailers.
  - Evidence: `git log --format=%b` shows no Refs trailer on 2ce25d84 (docs(troubleshooting)), 7b56cc8e (docs(install)) or 9e015934 (chore(v6)).
  - Fix: Before integration, add 'Refs: V6-VERIFY, C6.1' to those three commit bodies with a reword-only rebase of the branch. The branch is unpushed, so this is safe.

## Fix seat (review resolution) — status `done`, head `9cc6a7c4`

### Root cause

self-test called daemon.EnsureRunning without checking runtime.daemon.enabled. The mcp and command-frontend clients took their spawn permission from state.bin (ipc.ReadState) alone, and a daemon that did not stop cleanly can leave that file saying the daemon is enabled. The troubleshooting text said the client-spool watcher replays any late spool about two intervals after it is written. In fact the watcher is kicked when a request is dispatched and makes only one trailing look about 2 s later.

### Summary

FIX seat for w7b-selftest, on worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-selftest (branch closeout/w7b-selftest, base 898bb8b, head now 9cc6a7c4). The reviewer raised one minor finding. I checked it against the code and it is correct, so I fixed it with one docs-only commit on top of the implementer's five.

What was delivered, implementer and fix seat together:
(1) Product defect. `qompack self-test` now calls daemon.EnsureRunning only when runtime.daemon.enabled is true (internal/cli/selftest.go, runSelfTestChecks). When the daemon is disabled, selfTestDaemonDisabled reports daemon.reachable and admin.ping as OK/Info, observed "skipped: runtime.daemon.enabled is false". It spawns nothing and creates no lock. It does make one read-only ipc.Probe, and if a daemon answers anyway it warns. The same gap existed in the lazily spawning clients of `qompack mcp` (internal/cli/cmd_mcp.go, newMCPClient) and of the command frontends such as `qompack status` (internal/cli/qompack_commands.go, newCommandClient). Both trusted state.bin (ipc.ReadState) alone, and a daemon that stopped uncleanly can leave that file saying the daemon is enabled. Both now use daemonClientState, which also requires cfg.Runtime.Daemon.Enabled, as doHook does. The remaining callers are already correct. The hook path checks the switch (doHook / hookclient.go:491, gated with the config). session-start's ensureDaemonRunning (sessionstart.go:89) has the FR-6 check. The internal/daemon spawn.go and ipc lazySpawn code is only the mechanism, and every caller now passes a gated State.
(2) docs/install.md §3 now records --json on `claude plugin install`, `update` and `uninstall`, and says that `uninstall` without --keep-data removes ~/.claude/plugins/data/<id>/. The implementer took this from the CLI's help output.
(3) docs/troubleshooting.md now says a start spooled because no daemon was listening is replayed by the next daemon's startup drain (Run), or by redrainOnceServing for a line written after that drain, and not by the watcher. The Defender section was not touched.

Review resolution:
- Finding (minor, docs/troubleshooting.md:737-739 and :780-781, the watcher replay promise overstated) -> CONFIRMED and FIXED in 9cc6a7c4. Evidence:
  - internal/daemon/handlers.go dispatchOp calls d.noteServed() at the top, before the handler runs. noteServed (daemon.go:884) calls kickSpoolWatch.
  - internal/daemon/spool_watch.go watchClientSpools/lookAtClientSpools: after a kick the watcher looks once, then makes one trailing look after w.every (spoolCheckInterval = idleRunBudget = 2 s, daemon.go:38). If there is no new kick and no unsettled or retry-due entry, it returns (0,false) and blocks on w.kick again.
  - So a spool line written more than about one interval after the request arrived is missed until the next served request. That happens to a compaction (about 5 s) or a start whose reply wait (up to 10 s) expired.
  Both passages now say: the kick happens when the request arrives, before processing. The watcher looks then and once more 2 s later. A copy spooled within about one interval of arrival is replayed about two intervals after arrival. Otherwise it is picked up about two intervals after the next request the daemon serves (the session's next or first prompt). Failing that, the idle drain after scheduler.idle.detectAfterSeconds, the session's flush or the next daemon's startup drain replays it. No criterion was changed.

Tests and checks (run on a loaded machine; nothing failed, so no wall-clock re-runs were needed):
- The three focused tests pass.
- go test ./test/docs: ok.
- fmt-check: clean. lint docmarkers and runpatterns: PASS.
- I did not re-run go vet or the full internal/cli package, because my change touched no Go code. The implementer's full-package evidence (ok, 53.268s) is in plans/sdd/V6-closeout/w7b-selftest/runs/cli-full-windows.txt. The Linux non-root -race evidence for 264c93c is under runs/cx-w7b-selftest-cli-race-264c93c-20260928T023726Z-artifacts/.

No new budget or bound constants were introduced. I started no background processes and left none running.

### Commits

- 409c56fd fix(cli): skip self-test daemon checks when the daemon is disabled
- 264c93c0 fix(cli): honour daemon.enabled in mcp and command clients
- 2ce25d84 docs(troubleshooting): say which drain replays a spooled start
- 7b56cc8e docs(install): record the 2.1.280 plugin CLI flags
- 9e015934 chore(v6): record w7b-selftest evidence runs
- 9cc6a7c4 docs(troubleshooting): bound when the spool watcher sees a late spool

### Tests

- `go test -count=1 -timeout=30m -run 'TestDaemonClients_HonourDisabledDaemonOverStaleState|TestSelfTest_DaemonDisabledStartsNoDaemon|TestSelfTest_DaemonDisabledButReachableWarns' -v ./internal/cli` — exit 0; all three PASS (ok 0.561s)
- `go test ./test/docs` — ok 2.163s (exit 0)
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers (exit 0)
- `go test ./internal/cli (full package, implementer run, Windows)` — ok 53.268s (recorded in plans/sdd/V6-closeout/w7b-selftest/runs/cli-full-windows.txt)

### Open issues

- The coordinator must commit the report; the harness does not let subagents write report.md.

### Needs the owner

- Should `qompack daemon` itself refuse to run when runtime.daemon.enabled is false? That would be defence in depth against an older binary's spawner, or an operator's hand start, creating the resident process and lock file that docs/release.md section 4 promises never exist. Every spawner in this build now checks the switch; the daemon command was deliberately left unchanged because running it is an explicit request. No new budget or bound constants were introduced.

## Independent verification of the fix seat: sound

- **nit** `docs/troubleshooting.md:741-742 and :787-788` — The reviewer's finding is fixed. The kick-on-arrival explanation, the one trailing look, the within-one-interval case and the fallback drains now match the code. One number is off: the new text says a spool written after the trailing look is picked up 'about two intervals after the next request the daemon serves'. The code replays it about one interval after that request, plus up to one pass budget. The fix seat copied this number from the reviewer's suggested wording. The text errs on the late side, so no reader will expect a replay sooner than it happens.
  - Evidence: internal/daemon/spool_watch.go lookAtClientSpools. The `entries` map persists across kicks. A spool written after the t0+2s trailing look is absent at that look, so the next kick at t1 sees it as new (unsettled) and schedules a look at t1+every. At t1+2s its size is unchanged, so it is settled, retryDue returns true (passes==0) and the pass runs then. That is one interval after t1, not two. The first case is stated correctly: a spool written within (t0, t0+2s) is first seen at t0+2s, settles at t0+4s and is replayed about two intervals after arrival. go test ./test/docs passes at HEAD 9cc6a7c4.
  - Fix: Optional: change 'about two intervals after the next request the daemon serves' to 'about one interval (2 s) after the next request the daemon serves' in both passages. Or leave it as a conservative upper bound.

